/*
 * Phase 3 — Task 15: external template acceptance.
 *
 * Builds an unmodified, externally maintained template definition (for
 * example a Chrome template from a downstream consumer) against edvabe
 * and boots a sandbox from it. This is the Phase 3 ship gate.
 *
 * Requires EXTERNAL_TEMPLATE_PATH pointing at a TypeScript module that
 * exports `template` (an E2B `Template()` builder). Use an absolute path:
 * `make test-e2e-ts` runs from test/e2e/ts. Skipped when unset.
 *
 * Expected environment (set by `make test-e2e-ts` or manually):
 *
 *     E2B_API_URL=http://localhost:3000
 *     E2B_DOMAIN=localhost:3000
 *     E2B_API_KEY=edvabe_local
 *     E2B_SANDBOX_URL=http://localhost:3000
 *     EXTERNAL_TEMPLATE_PATH=/path/to/template.ts
 */

import { test } from 'node:test'
import assert from 'node:assert/strict'
import path from 'node:path'

import { Template, Sandbox, defaultBuildLogger } from 'e2b'

const templatePath = process.env.EXTERNAL_TEMPLATE_PATH

test('external template build + sandbox', { skip: !templatePath ? 'EXTERNAL_TEMPLATE_PATH not set' : undefined, timeout: 600_000 }, async () => {
  // Dynamically import the external template definition unchanged.
  const mod = await import(path.resolve(templatePath!))
  const template = mod.template

  // Build the template against edvabe.
  const name = `external-template-${Date.now()}`
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
    const result = await sbx.commands.run('echo "external template sandbox ok"')
    assert.equal(result.exitCode, 0)
    assert.equal(result.stdout.trim(), 'external template sandbox ok')
  } finally {
    try {
      await sbx.kill()
    } catch {
      /* ignore */
    }
  }
})
