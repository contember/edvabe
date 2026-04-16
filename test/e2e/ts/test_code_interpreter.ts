/*
 * Phase 2 acceptance test for edvabe — TypeScript code interpreter SDK.
 *
 * Expected environment (set by `make test-e2e-code-interpreter-ts` or manually):
 *
 *     E2B_API_URL=http://localhost:3000
 *     E2B_DOMAIN=localhost:3000
 *     E2B_API_KEY=edvabe_local
 *     E2B_SANDBOX_URL=http://localhost:3000
 *
 * Prerequisites:
 *     edvabe build-image --template=code-interpreter
 *     edvabe serve (running)
 */

import { test } from 'node:test'
import assert from 'node:assert/strict'

import { Sandbox } from '@e2b/code-interpreter'

async function withSandbox<T>(fn: (sbx: Sandbox) => Promise<T>): Promise<T> {
  const sbx = await Sandbox.create({ timeoutMs: 300_000 })
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

test('run_code simple expression', async () => {
  await withSandbox(async (sbx) => {
    const execution = await sbx.runCode('1 + 1')
    assert.equal(execution.results.length, 1)
    assert.equal(execution.results[0].text, '2')
    assert.equal(execution.error, undefined)
  })
})

test('run_code stdout', async () => {
  await withSandbox(async (sbx) => {
    const execution = await sbx.runCode('print("hello from code interpreter")')
    assert.ok(
      execution.logs.stdout.some((line: string) =>
        line.includes('hello from code interpreter')
      ),
      `stdout should contain greeting, got: ${execution.logs.stdout}`
    )
  })
})

test('run_code multiline', async () => {
  await withSandbox(async (sbx) => {
    const execution = await sbx.runCode('x = 42\ny = x * 2\ny')
    assert.equal(execution.results.length, 1)
    assert.equal(execution.results[0].text, '84')
  })
})

test('run_code pandas', async () => {
  await withSandbox(async (sbx) => {
    const execution = await sbx.runCode(`
import pandas as pd
pd.DataFrame({"a": [1, 2, 3], "b": [4, 5, 6]})
`)
    assert.equal(execution.results.length, 1)
    const result = execution.results[0]
    // Pandas DataFrames render as HTML in Jupyter
    assert.ok(
      result.html !== undefined || result.text !== undefined,
      'expected HTML or text result from pandas DataFrame'
    )
  })
})

test('run_code error handling', async () => {
  await withSandbox(async (sbx) => {
    const execution = await sbx.runCode('1 / 0')
    assert.ok(execution.error !== undefined, 'expected an error')
    assert.ok(
      execution.error!.name.includes('ZeroDivisionError'),
      `expected ZeroDivisionError, got: ${execution.error!.name}`
    )
  })
})
