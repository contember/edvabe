"""
Template-build E2E test for the Python SDK.

    build template (with a copied file) → create sandbox → read files

The generated Python API client parses only the status code E2B
documents for each endpoint and treats any other 2xx as a failure, so
this catches status-code drift the TypeScript test does not.

Expected environment (see conftest.py):

    E2B_API_URL=http://localhost:3000
    E2B_DOMAIN=localhost:3000
    E2B_API_KEY=edvabe_local
    E2B_SANDBOX_URL=http://localhost:3000
"""

import time

from e2b import Sandbox, Template, wait_for_timeout


def test_template_build_and_create(tmp_path):
    (tmp_path / "greeting.txt").write_text("hello from copy\n")
    name = f"edvabe-py-smoke-{int(time.time())}"

    template = (
        Template(file_context_path=tmp_path)
        .from_image("oven/bun:slim")
        .copy("greeting.txt", "/etc/copied-greeting")
        .run_cmd('echo "hello from build" > /etc/greeting')
        .set_user("root")
        .set_start_cmd("sleep infinity", wait_for_timeout(1000))
    )

    build_info = Template.build(template, name, cpu_count=1, memory_mb=1024)
    assert build_info.template_id

    sbx = Sandbox.create(build_info.template_id, timeout=60)
    try:
        assert sbx.commands.run("cat /etc/greeting").stdout.strip() == "hello from build"
        assert sbx.commands.run("cat /etc/copied-greeting").stdout.strip() == "hello from copy"
    finally:
        try:
            sbx.kill()
        except Exception:
            pass
