// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The stub `fugaro` the end-to-end specs install (`test/fixtures/bin/fugaro`,
// desktop-console plan Task 3.3), driven from plain Node.
//
// The e2e suite is the only place this script is otherwise run, and it needs
// Electron and a display. The answers it gives are not a detail of that suite:
// the picker's **whole** first-run state is `config list --json`'s output, so a
// stub that answered the wrong thing would put every spec that reads it in front
// of a screen this console does not have. Three of those answers are worth a test
// here -- in the fast suite, with no app at all:
//
//   - **the empty document.** With no `installations.ndjson` seeded, the stub
//     prints `{"installations":[],"warnings":[]}`: a `config list --json` that
//     succeeded on a machine with no installations. That is the document the
//     "set up your first installation" screen is drawn for
//     (`src/main/picker.ts`: no rows and no records), and it goes through the
//     console's own parser here rather than through a hand-written `JSON.parse`
//     -- what is asserted is that the stub and the console agree.
//   - **the documents a screen is a function of.** `plan status --json` and
//     `coordinator status --from-store --all --json` are the Graph's and the
//     project view's whole input, and the committed fixtures are what the stub
//     answers with: a shape either decoder refuses is a spec that opens the
//     refusal screen instead of the view, which is a failure worth having here
//     rather than in a suite that needs a display. The project view's case also
//     holds the **states** its spec's words are drawn for -- a draft pull
//     request, a record in flight, a record with no run behind it and an unread
//     pull request -- since those are facts about the fixture and not about the
//     browser.
//   - **the refusals.** `config list --json` takes no `--project` and no
//     arguments it was not written for, a command outside the allow-list is
//     refused by name -- with `config list --json` in the sentence, so a failing
//     spec prints the command it should have been asked for -- and
//     `coordinator status` holds its three fixed words, which are the spawn
//     row's and not a screen's.

import { copyFileSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'

import { describe, expect, it } from 'vitest'

import { parseInstallationsDocument } from '../../src/main/installlist'
import { decodeCoordinatorStatus, decodePlanStatus } from '../../src/shared/schema'

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

  it('prints the plan document for `plan status --json`, and the console reads it', () => {
    // The Graph view's own read (GV2.3b): the console spawns
    // `plan status --json --all --project P`, and the document that comes back
    // is what the screen is a function of. A stub that printed a shape the
    // decoder refuses would put every e2e that opens the Graph in front of the
    // refusal screen instead of a graph.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    // The committed document, which is the one `cliStub.ts` writes beside the
    // stub the e2e specs install.
    const seeded = readFileSync(join(__dirname, '..', 'fixtures', 'plan-status.json'), 'utf8')
    writeFileSync(join(directory, 'plan-status.json'), seeded)
    const answered = run(directory, ['plan', 'status', '--json', '--all', '--project', 'e2e'])
    expect(answered.status).toBe(0)
    expect(answered.stdout).toBe(seeded)
    expect(decodePlanStatus(JSON.parse(answered.stdout)).ok).toBe(true)
    // `--all` is the screen's control over how much of the document the CLI
    // lists, not a different read: the same document comes back without it.
    expect(run(directory, ['plan', 'status', '--json', '--project', 'e2e']).stdout).toBe(seeded)
  })

  it('prints the coordinator document for `coordinator status`, and the console reads it', () => {
    // The project view's own read (Bitbucket credentials T1.2, D364): the console
    // spawns `coordinator status --from-store --all --json --project P`, and the
    // document that comes back is what the screen is a function of. A stub that
    // printed a shape the decoder refuses would put every e2e that opens the
    // project view in front of the refusal screen instead of the view.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    // The committed document, which is the one `cliStub.ts` writes beside the
    // stub the e2e specs install.
    const seeded = readFileSync(join(__dirname, '..', 'fixtures', 'coordinator-status.json'), 'utf8')
    writeFileSync(join(directory, 'coordinator-status.json'), seeded)
    const answered = run(directory, [
      'coordinator',
      'status',
      '--from-store',
      '--all',
      '--json',
      '--project',
      'e2e',
    ])
    expect(answered.status).toBe(0)
    expect(answered.stdout).toBe(seeded)
    const decoded = decodeCoordinatorStatus(JSON.parse(answered.stdout))
    expect(decoded.ok).toBe(true)
    // **The states the e2e's words are read off, held here where there is no
    // Electron.** `coordinator.spec.ts` asserts that `draft`, `in flight`, `no
    // run behind it` and `state unread` reach a real window; whether the document
    // carries the state each of those words is drawn for is a fact about this
    // fixture, and a spec that had to launch an app to find it out would be
    // asserting it in the slowest place there is.
    const repos = decoded.ok ? decoded.value.repos : []
    expect(repos.map((repo) => repo.stored)).toEqual([true, false])
    const stored = repos[0]
    expect(stored?.view.records[0]?.pr?.draft).toBe(true)
    expect(stored?.view.records[0]?.inFlight).toBe(true)
    expect(stored?.view.records[0]?.runs[0]?.terminal).toBe(false)
    expect(stored?.view.records[1]?.runs).toEqual([])
    expect(stored?.view.unread).toHaveLength(1)
    expect(stored?.stale).not.toBeNull()
  })

  it('refuses a `coordinator status` without its fixed words, and a document that was never seeded', () => {
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    // `--from-store` and `--all` are the spawn row's own words and not a screen's
    // control (`src/main/spawn.ts`), so a console that dropped either would be
    // making a read this build must never make: the refusal is the spec's only
    // way of seeing that, and it names the whole argv.
    const dropped = run(directory, ['coordinator', 'status', '--all', '--json', '--project', 'e2e'])
    expect(dropped.status).toBe(2)
    expect(dropped.stderr).toContain("answers 'coordinator status --from-store --all --json'")
    // Unseeded, the read is refused by name rather than by `cat`'s own message.
    const missing = run(directory, ['coordinator', 'status', '--from-store', '--all', '--json', '--project', 'e2e'])
    expect(missing.status).toBe(2)
    expect(missing.stderr).toContain('no coordinator document is seeded')
  })

  it('refuses a word `plan status --json` does not take, and a document that was never seeded', () => {
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    const extra = run(directory, ['plan', 'status', '--json', '--all', '--project', 'e2e', '--everything'])
    expect(extra.status).toBe(2)
    expect(extra.stderr).toContain("was asked for '--everything'")
    // Unseeded, the read is refused by name: `cat` of a missing file would fail
    // with the shell's own message and no sentence saying what is wrong.
    const missing = run(directory, ['plan', 'status', '--json', '--project', 'e2e'])
    expect(missing.status).toBe(2)
    expect(missing.stderr).toContain('no plan document is seeded')
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
    expect(answered.stderr).toContain('plan status --json')
    expect(answered.stderr).toContain('coordinator status --from-store --all --json')
    expect(answered.stderr).toContain('watch --json')
    expect(answered.stderr).toContain("was asked for 'runs'")
  })
})
