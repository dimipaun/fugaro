// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The `--by` vocabulary, held in one place: `report`'s own list of breakdowns
// (`internal/report/report.go`, `ParseDim`) and the three copies the console
// keeps -- the Spend screen's switch (`SPEND_DIMENSIONS`, `spend.ts`), the IPC
// allow-list (`REPORT_DIMENSIONS`, `ipc.ts`) and the redactor's field table
// (`REPORT_DIMENSIONS`, `redact.ts`).
//
// The three copies drifted from the CLI once already. `--by mode` shipped in
// the CLI (2026-10-28) and none of the console's lists carried the word, so a
// by-mode document was read as an unknown breakdown and the redactor collected
// the dimension itself as a person. This test reads the list from the CLI's own
// `--by` help line -- the sentence `fugaro report --help` prints, pinned in the
// repository, so there is no network and no live CLI -- and fails when any of
// the three disagrees with it, so the next word the CLI gains fails here rather
// than in a window.

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { validateArguments } from '../../src/main/ipc'
import { SPEND_DIMENSIONS } from '../../src/renderer/screens/spend'
import { createRedactor } from '../../src/source/redact'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.join(HERE, '..', '..', '..')

function read(relative: string): string {
  return readFileSync(path.join(REPO_ROOT, relative), 'utf8')
}

/**
 * cliDimensions reads `report --by`'s words out of the CLI's own help. The
 * `--by` flag's usage line in `internal/cli/report.go` is the list
 * `fugaro report --help` prints and the set `ParseDim` accepts, so reading it
 * is reading the CLI rather than a copy of it. It throws rather than returning
 * an empty list so that a renamed flag or a reworded line fails here by name.
 */
function cliDimensions(): string[] {
  const source = read('internal/cli/report.go')
  const flag = /StringVar\(&o\.by,\s*"by",\s*"[^"]*",\s*"([^"]*)"\)/.exec(source)
  if (flag === null || flag[1] === undefined) {
    throw new Error('internal/cli/report.go no longer declares --by with a usage line')
  }
  return flag[1].replace(/^group by\s+/, '').split(/,\s*|\s+or\s+/)
}

/**
 * declaredList reads the string words a named `const` array or set declares in
 * a TypeScript source file. The two `REPORT_DIMENSIONS` are module-private --
 * nothing outside their own module reads them -- so this reads them where they
 * are written rather than widening their module's surface for a test.
 */
function declaredList(relative: string, name: string): string[] {
  const source = read(relative)
  const decl = new RegExp(String.raw`\bconst\s+${name}\b`).exec(source)
  if (decl === null) throw new Error(`${relative} declares no ${name}`)
  const open = source.indexOf('[', source.indexOf('=', decl.index))
  const close = source.indexOf(']', open)
  const words = [...source.slice(open + 1, close).matchAll(/'([^']*)'/g)].map((one) => one[1] as string)
  if (words.length === 0) throw new Error(`${relative}'s ${name} declares no words`)
  return words
}

describe('the report --by vocabulary', () => {
  it("is the CLI's own list of eight words", () => {
    // The words the CLI's help prints, written out here so that a change to the
    // CLI's own list shows up as a diff in this file rather than passing
    // silently through a comparison against itself.
    expect(cliDimensions()).toEqual(['day', 'week', 'month', 'year', 'repo', 'model', 'person', 'mode'])
  })

  it('is the same list in the Spend switch, the IPC allow-list and the redactor', () => {
    const cli = cliDimensions()
    expect([...SPEND_DIMENSIONS]).toEqual(cli)
    expect(declaredList('console/src/main/ipc.ts', 'REPORT_DIMENSIONS')).toEqual(cli)
    expect(declaredList('console/src/source/redact.ts', 'REPORT_DIMENSIONS')).toEqual(cli)
  })

  it('admits every word, and keeps a by-mode dimension out of the redactor\'s persons', () => {
    // The lists above are text; these are the two behaviours they drive. The
    // IPC validator is the first lock on `--by` (the spawn allow-list is the
    // second), and the redactor treats a `by` value it does not know as a
    // person -- so a word missing from either list is not a missing label but a
    // refused read and a pseudonymised dimension.
    for (const dimension of cliDimensions()) {
      expect(validateArguments('spend.report', { by: dimension }).ok, dimension).toBe(true)
    }
    const redactor = createRedactor([], { pseudonymKey: 'report-dimensions-test-only-key' })
    const redacted = redactor.redactDocument({ by: 'mode', rows: [{ key: 'pro' }, { key: 'none' }] }) as {
      by: string
    }
    expect(redacted.by).toBe('mode')
  })
})
