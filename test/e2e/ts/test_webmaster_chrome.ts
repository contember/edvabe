/*
 * Phase 3 — Task 15: Webmaster chrome template acceptance.
 *
 * Builds the unmodified webmaster chrome template against edvabe and
 * boots a sandbox from it. This is the Phase 3 ship gate.
 *
 * Requires WEBMASTER_REPO env var pointing at a checked-out webmaster
 * tree. Skipped when unset.
 *
 * Expected environment (set by `make test-e2e-ts` or manually):
 *
 *     E2B_API_URL=http://localhost:3000
 *     E2B_DOMAIN=localhost:3000
 *     E2B_API_KEY=edvabe_local
 *     E2B_SANDBOX_URL=http://localhost:3000
 *     WEBMASTER_REPO=/path/to/webmaster
 */

import { test } from 'node:test'
import assert from 'node:assert/strict'
import path from 'node:path'

import { Template, Sandbox, defaultBuildLogger } from 'e2b'

const webmasterRepo = process.env.WEBMASTER_REPO

test('webmaster chrome template build + sandbox', { skip: !webmasterRepo ? 'WEBMASTER_REPO not set' : undefined, timeout: 600_000 }, async () => {
  const templatePath = path.join(webmasterRepo!, 'containers/templates/chrome/template.ts')

  // Dynamically import the real webmaster template definition.
  const mod = await import(templatePath)
  const template = mod.template

  // Build the template against edvabe.
  const name = `webmaster-sandbox-chrome-${Date.now()}`
  const buildInfo = await Template.build(template, name, {
    memoryMB: 4096,
    onBuildLogs: defaultBuildLogger(),
  })

  assert.ok(buildInfo.templateId, 'buildInfo.templateId should be set')

  // Create a sandbox from the built template.
  const sbx = await Sandbox.create(buildInfo.templateId, { timeoutMs: 120_000 })
  try {
    assert.ok(sbx.sandboxId, 'sandboxId should be set')

    // Verify the sandbox can execute a command.
    const result = await sbx.commands.run('echo "chrome sandbox ok"')
    assert.equal(result.exitCode, 0)
    assert.equal(result.stdout.trim(), 'chrome sandbox ok')
  } finally {
    try {
      await sbx.kill()
    } catch {
      /* ignore */
    }
  }
})
