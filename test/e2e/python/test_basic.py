"""
Phase 1 acceptance test for edvabe — exercises the same hot path the
docs/06-phases.md acceptance criterion calls out:

    create sandbox → run commands → read/write files → list → PTY → watch → kill

Expected environment (set by `make test-e2e-python` or manually):

    E2B_API_URL=http://localhost:3000
    E2B_DOMAIN=localhost:3000
    E2B_API_KEY=edvabe_local
    E2B_SANDBOX_URL=http://localhost:3000

The SANDBOX_URL override makes the SDK route envd data-plane calls
through edvabe on plain HTTP instead of the default
`https://49983-<id>.<domain>` form. The SDK still sends the
`E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers edvabe dispatches on.

Each feature lives in its own test so a failure points at the specific
layer to fix (proxy, envd-in-Docker behavior, control plane handler).
"""

import threading
import time

import pytest
from e2b import Sandbox
from e2b.sandbox.commands.command_handle import PtySize


@pytest.fixture
def sbx():
    s = Sandbox.create(timeout=60)
    try:
        yield s
    finally:
        try:
            s.kill()
        except Exception:
            pass


def test_create_and_kill():
    s = Sandbox.create(timeout=60)
    assert s.sandbox_id
    s.kill()


def test_commands_run_echo(sbx):
    result = sbx.commands.run("echo hello from edvabe")
    assert result.stdout.strip() == "hello from edvabe"
    assert result.exit_code == 0


def test_files_write_read(sbx):
    sbx.files.write("/home/user/foo.txt", "hello")
    assert sbx.files.read("/home/user/foo.txt") == "hello"


def test_files_list(sbx):
    sbx.files.write("/home/user/foo.txt", "hello")
    entries = sbx.files.list("/home/user")
    assert any(e.name == "foo.txt" for e in entries)


def test_pty(sbx):
    chunks: list[bytes] = []
    handle = sbx.pty.create(size=PtySize(rows=24, cols=80), timeout=15)

    def drain():
        try:
            handle.wait(on_pty=lambda data: chunks.append(data))
        except Exception:
            pass

    t = threading.Thread(target=drain, daemon=True)
    t.start()
    try:
        sbx.pty.send_stdin(handle.pid, b"echo in pty\n")
        sbx.pty.send_stdin(handle.pid, b"exit\n")
        t.join(timeout=10)
        output = b"".join(chunks)
        assert b"in pty" in output, f"pty output did not include 'in pty': {output!r}"
    finally:
        try:
            handle.kill()
        except Exception:
            pass


def test_watch_dir(sbx):
    watcher = sbx.files.watch_dir("/home/user")
    try:
        sbx.files.write("/home/user/bar.txt", "x")
        deadline = time.monotonic() + 10
        seen = False
        while time.monotonic() < deadline and not seen:
            for evt in watcher.get_new_events():
                if evt.name == "bar.txt":
                    seen = True
                    break
            if not seen:
                time.sleep(0.2)
        assert seen, "did not receive filesystem event for bar.txt"
    finally:
        try:
            watcher.stop()
        except Exception:
            pass
