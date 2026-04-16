/*
 * Phase 3 — Task 14: Template-build E2E test.
 *
 * Exercises Flow A from docs/06-phases.md:
 *
 *     build template → create sandbox → read file → pause → connect → verify
 *
 * Expected environment (set by `make test-e2e-ts` or manually):
 *
 *     E2B_API_URL=http://localhost:3000
 *     E2B_DOMAIN=localhost:3000
 *     E2B_API_KEY=edvabe_local
 *     E2B_SANDBOX_URL=http://localhost:3000
 */

import { test } from 'node:test'
import assert from 'node:assert/strict'

import { Template, Sandbox, defaultBuildLogger, waitForTimeout } from 'e2b'

test('template build + create + read + pause + connect', async () => {
  const name = `edvabe-smoke-${Date.now()}`

  // 1. Define a template from oven/bun:slim with a runCmd that writes a file.
  const template = Template()
    .fromImage('oven/bun:slim')
    .runCmd('echo "hello from build" > /etc/greeting')
    .setUser('root')
    .setStartCmd('sleep infinity', waitForTimeout(1000))

  // 2. Build the template.
  const buildInfo = await Template.build(template, name, {
    onBuildLogs: defaultBuildLogger(),
  })

  assert.ok(buildInfo.templateId, 'buildInfo.templateId should be set')

  // 3. Create a sandbox from the built template.
  const sbx = await Sandbox.create(buildInfo.templateId, { timeoutMs: 60_000 })
  try {
    assert.ok(sbx.sandboxId, 'sandboxId should be set')

    // 4. Verify the file written during build is present.
    const result = await sbx.commands.run('cat /etc/greeting')
    assert.equal(result.stdout.trim(), 'hello from build')
    assert.equal(result.exitCode, 0)

    // 5. Pause the sandbox.
    await sbx.pause()

    // 6. Reconnect (auto-resumes the paused sandbox).
    const sbx2 = await Sandbox.connect(sbx.sandboxId, { timeoutMs: 60_000 })
    try {
      // 7. Verify the sandbox still works after resume.
      const result2 = await sbx2.commands.run('echo resumed')
      assert.equal(result2.exitCode, 0)
      assert.equal(result2.stdout.trim(), 'resumed')
    } finally {
      try {
        await sbx2.kill()
      } catch {
        /* ignore */
      }
    }
  } finally {
    try {
      await sbx.kill()
    } catch {
      /* ignore */
    }
  }
})
