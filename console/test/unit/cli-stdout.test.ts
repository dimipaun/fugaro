// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// parseStdout reads a one-shot read's stdout. Every one-shot command in the
// console's set prints an INDENTED document (json-contract.md section 9.2): a
// line parser is right only for `watch` and `logs`. These tests feed it the
// repository's own golden output, so the shape is the CLI's, not a stub's.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { parseStdout } from '../../src/source/cli'

const GOLDEN = join(__dirname, '..', '..', '..', 'internal', 'cli', 'testdata', 'cloudrun_cost.golden')

/** goldenBlock is the first `--- <name>` section of the golden file. */
function goldenBlock(name: string): string {
  const lines = readFileSync(GOLDEN, 'utf8').split('\n')
  const start = lines.indexOf(`--- ${name}`)
  expect(start).toBeGreaterThanOrEqual(0)
  const out: string[] = []
  for (const line of lines.slice(start + 1)) {
    if (line.startsWith('--- ') || line.startsWith('== ')) break
    out.push(line)
  }
  return out.join('\n') + '\n'
}

describe('parseStdout reads the indented one-shot documents whole', () => {
  it('decodes the golden `ls --json` document', () => {
    const read = parseStdout(goldenBlock('ls --json'))
    expect(read.document).not.toBeNull()
    expect(read.document?.['project']).toBe('proj-1234')
    expect(Array.isArray(read.document?.['runs'])).toBe(true)
    expect(read.notices).toEqual([])
  })

  it('keeps a human line around the document as a notice', () => {
    const stdout = `no budget backend: showing run records only\n${goldenBlock('ls --json')}`
    const read = parseStdout(stdout, 'T')
    expect(read.document?.['project']).toBe('proj-1234')
    expect(read.notices).toEqual([{ text: 'no budget backend: showing run records only', receivedAt: 'T' }])
  })

  it('never adopts a nested fragment that sits alone on a line', () => {
    const stdout = JSON.stringify({ runs: [{}], extra: {} }, null, 2) + '\n'
    // `{}` sits alone on an (indented) line in that output.
    expect(stdout.split('\n').some((line) => line.trim() === '{}')).toBe(true)
    const read = parseStdout(stdout)
    expect(read.document).toEqual({ runs: [{}], extra: {} })
  })

  it('still reads a compact one-line document', () => {
    expect(parseStdout('{"a":1}\n').document).toEqual({ a: 1 })
  })

  it('answers null for stdout with no document, and for a torn one', () => {
    expect(parseStdout('just a sentence\n').document).toBeNull()
    expect(parseStdout('{\n  "a": 1,\n').document).toBeNull()
  })

  it('refuses two documents rather than choosing one', () => {
    expect(parseStdout('{"a":1}\n{"b":2}\n').document).toBeNull()
  })
})
