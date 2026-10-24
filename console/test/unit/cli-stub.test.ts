// The stub `fugaro` the end-to-end specs install (`test/fixtures/bin/fugaro`,
// desktop-console plan Task 3.3), driven from plain Node.
//
// The e2e suite is the only place this script is otherwise run, and it needs
// Electron and a display. The answers it gives are not a detail of that suite:
// the picker's **whole** first-run state is `config list --json`'s output, so a
// stub that answered the wrong thing would put every spec that reads it in front
// of a screen this console does not have. Two of those answers are worth a test
// here -- in the fast suite, with no app at all:
//
//   - **the empty document.** With no `installations.ndjson` seeded, the stub
//     prints `{"installations":[],"warnings":[]}`: a `config list --json` that
//     succeeded on a machine with no installations. That is the document the
//     "set up your first installation" screen is drawn for
//     (`src/main/picker.ts`: no rows and no records), and it goes through the
//     console's own parser here rather than through a hand-written `JSON.parse`
//     -- what is asserted is that the stub and the console agree.
//   - **the refusals.** `config list --json` takes no `--project` and no
//     arguments it was not written for, and a command outside the allow-list is
//     refused by name -- with `config list --json` in the sentence, so a failing
//     spec prints the command it should have been asked for.

import { copyFileSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'

import { describe, expect, it } from 'vitest'

import { parseInstallationsDocument } from '../../src/main/installlist'

/** STUB is the fixture this file runs, at the path the e2e helper copies from. */
const STUB = join(__dirname, '..', 'fixtures', 'bin', 'fugaro')

/** EMPTY_DOCUMENT is what the CLI prints on a machine with no installations: both
 * arrays seeded, which the contract (§1) makes a fact rather than a null. */
const EMPTY_DOCUMENT = '{"installations":[],"warnings":[]}'

interface Ran {
  readonly status: number | null
  readonly stdout: string
  readonly stderr: string
}

/** install puts a copy of the fixture stub in a fresh scratch directory -- the
 * way `test/support/cliStub.ts` does, so `here` resolves to a directory this test
 * owns -- and answers that directory. */
function install(): string {
  const scratch = mkdtempSync(join(tmpdir(), 'fugaro-cli-stub-unit-'))
  copyFileSync(STUB, join(scratch, 'fugaro'))
  return scratch
}

/** run executes the stub with these arguments, in its own directory. `sh` and not
 * the file itself: the shebang is the fixture's, and a checkout that lost the
 * executable bit (a zip download) must not turn this test red for a reason that
 * is not the stub's answers. */
function run(directory: string, args: readonly string[]): Ran {
  const result = spawnSync('sh', [join(directory, 'fugaro'), ...args], { encoding: 'utf8' })
  return { status: result.status, stdout: result.stdout, stderr: result.stderr }
}

describe('the e2e stub’s own answers', () => {
  it('prints the empty document when no list was seeded, and the console reads it as "none"', () => {
    const directory = install()
    const answered = run(directory, ['config', 'list', '--json'])
    expect(answered.status).toBe(0)
    expect(answered.stdout).toBe(`${EMPTY_DOCUMENT}\n`)
    // The claim that matters: the console's own reader takes it, and takes it as
    // the state the first-installation screen is drawn for. A stub that printed
    // *nothing* would be refused by this same parser (`JSON.parse('')` throws),
    // which is a different screen entirely.
    const parsed = parseInstallationsDocument(answered.stdout)
    expect(parsed.ok).toBe(true)
    expect(parsed.ok && parsed.document.installations).toEqual([])
    expect(parsed.ok && parsed.document.warnings).toEqual([])
  })

  it('prints the document it was seeded with, verbatim', () => {
    const directory = install()
    const seeded = '{"installations":[{"name":"e2e","gcp_project":"p","region":"r"}],"warnings":["why"]}'
    writeFileSync(join(directory, 'installations.ndjson'), `${seeded}\n`)
    const answered = run(directory, ['config', 'list', '--json'])
    expect(answered.status).toBe(0)
    expect(answered.stdout).toBe(`${seeded}\n`)
    const parsed = parseInstallationsDocument(answered.stdout)
    expect(parsed.ok && parsed.document.installations[0]?.name).toBe('e2e')
  })

  it('refuses a `--project` on the one command that has none, and names it', () => {
    const directory = install()
    const answered = run(directory, ['config', 'list', '--json', '--project', 'e2e'])
    expect(answered.status).toBe(2)
    expect(answered.stderr).toContain('takes no further arguments')
  })

  it('refuses a command outside the allow-list, and the sentence names every one it answers', () => {
    const directory = install()
    // The seeded project, so the refusal is the allow-list's and not the
    // `--project` guard's (which is asserted above, in its own case).
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    const answered = run(directory, ['runs', '--json', '--project', 'e2e'])
    expect(answered.status).toBe(2)
    // `config list --json` is the command this console added to the stub, and a
    // failing spec's message is where a reader learns what the stub would have
    // answered.
    expect(answered.stderr).toContain('config list --json')
    expect(answered.stderr).toContain('watch --json')
    expect(answered.stderr).toContain("was asked for 'runs'")
  })
})
