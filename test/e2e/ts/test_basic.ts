/*
 * Phase 1 acceptance test for edvabe — TypeScript mirror of
 * test/e2e/python/test_basic.py. Exercises the same hot path:
 *
 *     create sandbox → run commands → read/write files → list → PTY → watch → kill
 *
 * Expected environment (set by `make test-e2e-ts` or manually):
 *
 *     E2B_API_URL=http://localhost:3000
 *     E2B_DOMAIN=localhost:3000
 *     E2B_API_KEY=edvabe_local
 *     E2B_SANDBOX_URL=http://localhost:3000
 *
 * The SANDBOX_URL override makes the SDK route envd data-plane calls
 * through edvabe on plain HTTP instead of the default
 * `https://49983-<id>.<domain>` host form. The SDK still sends the
 * `E2b-Sandbox-Id` / `E2b-Sandbox-Port` headers edvabe dispatches on.
 */

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { setTimeout as sleep } from 'node:timers/promises'

import { Sandbox } from 'e2b'

async function withSandbox<T>(fn: (sbx: Sandbox) => Promise<T>): Promise<T> {
  const sbx = await Sandbox.create({ timeoutMs: 60_000 })
  try {
    return await fn(sbx)
  } finally {
    try {
      await sbx.kill()
    } catch {
      /* ignore */
    }
  }
}

test('create and kill', async () => {
  const sbx = await Sandbox.create({ timeoutMs: 60_000 })
  assert.ok(sbx.sandboxId, 'sandboxId should be set')
  await sbx.kill()
})

test('commands.run echo', async () => {
  await withSandbox(async (sbx) => {
    const result = await sbx.commands.run('echo hello from edvabe')
    assert.equal(result.stdout.trim(), 'hello from edvabe')
    assert.equal(result.exitCode, 0)
  })
})

test('files.write and read', async () => {
  await withSandbox(async (sbx) => {
    await sbx.files.write('/home/user/foo.txt', 'hello')
    const contents = await sbx.files.read('/home/user/foo.txt')
    assert.equal(contents, 'hello')
  })
})

test('files.list', async () => {
  await withSandbox(async (sbx) => {
    await sbx.files.write('/home/user/foo.txt', 'hello')
    const entries = await sbx.files.list('/home/user')
    assert.ok(
      entries.some((e) => e.name === 'foo.txt'),
      'foo.txt not in listing'
    )
  })
})

test('pty create + send_stdin', async () => {
  await withSandbox(async (sbx) => {
    const chunks: Uint8Array[] = []
    const handle = await sbx.pty.create({
      rows: 24,
      cols: 80,
      onData: (data) => {
        chunks.push(data)
      },
      timeoutMs: 15_000,
    })
    try {
      await sbx.pty.sendInput(handle.pid, new TextEncoder().encode('echo in pty\n'))
      await sbx.pty.sendInput(handle.pid, new TextEncoder().encode('exit\n'))

      const deadline = Date.now() + 10_000
      while (Date.now() < deadline) {
        const joined = Buffer.concat(chunks.map((c) => Buffer.from(c))).toString('utf8')
        if (joined.includes('in pty')) {
          return
        }
        await sleep(100)
      }
      const joined = Buffer.concat(chunks.map((c) => Buffer.from(c))).toString('utf8')
      assert.fail(`pty output did not include 'in pty': ${JSON.stringify(joined)}`)
    } finally {
      try {
        await handle.kill()
      } catch {
        /* ignore */
      }
    }
  })
})

test('files.watchDir', async () => {
  await withSandbox(async (sbx) => {
    let seen = false
    const watcher = await sbx.files.watchDir('/home/user', (event) => {
      if (event.name === 'bar.txt') {
        seen = true
      }
    })
    try {
      await sbx.files.write('/home/user/bar.txt', 'x')
      const deadline = Date.now() + 10_000
      while (Date.now() < deadline && !seen) {
        await sleep(100)
      }
      assert.ok(seen, "did not receive filesystem event for bar.txt")
    } finally {
      try {
        await watcher.stop()
      } catch {
        /* ignore */
      }
    }
  })
})
