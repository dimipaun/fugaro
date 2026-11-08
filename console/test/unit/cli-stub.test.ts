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
//   - **the launch's answer** (V2.5.1c). The stub's `run --task-file - --json`
//     is the console's one write, and it is answered through the launcher's own
//     `decodeLaunchResult` here rather than in a suite that needs a display: the
//     reply must echo the run id and the repository the console handed it, carry
//     the six required strings and the `omitempty` ones as `null`, and be refused
//     for every argv rule 11's row does not carry. Its one side effect --
//     `runs.log`, one line per invocation with the task from stdin -- is read
//     here too: it is the only place a launch spec can count the children a
//     confirmation spawned.

import { copyFileSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'

import { describe, expect, it } from 'vitest'

import { parseInstallationsDocument } from '../../src/main/installlist'
import { decodeLaunchResult } from '../../src/main/launchrules'
import { settingsDryRun } from '../../src/main/settingsview'
import { buildProjectsSettings } from '../../src/renderer/screens/projects'
import { decodeCoordinatorStatus, decodePlanStatus, decodeRunsSummary, RUNS_SUMMARY_VERSION } from '../../src/shared/schema'

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

/** run executes the stub with these arguments, in its own directory, and hands
 * it `input` on stdin -- which is how the launch's task reaches the child
 * (`run --task-file -`). `sh` and not the file itself: the shebang is the
 * fixture's, and a checkout that lost the executable bit (a zip download) must
 * not turn this test red for a reason that is not the stub's answers. */
function run(directory: string, args: readonly string[], input = ''): Ran {
  const result = spawnSync('sh', [join(directory, 'fugaro'), ...args], { encoding: 'utf8', input })
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

  it('answers `runs summary --json` for the window it was asked for, and the console reads it', () => {
    // The Coordinator view's fleet strip (console 0.119.0): the console spawns
    // `runs summary --json --since=7d --project P`, and this document is what the
    // strip's ten figures are drawn from. It is seeded per window, so the case a
    // spec is about -- a window that stayed unknown, a `compute_micros` that is
    // `null` -- is the case its own document carries.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    const seeded = `${JSON.stringify({
      v: RUNS_SUMMARY_VERSION,
      project: 'e2e',
      since: '2026-10-27',
      until: '2026-11-02',
      repositories: [],
      totals: {
        runs: 6,
        states: { succeeded: 3, failed: 2, halted: 0, cancelled: 0, infra_error: 0, other: 1 },
        spent_micros: 1_500_000,
        notional_micros: 1_150_000,
        compute_micros: null,
        compute_runs: 0,
      },
      days_unknown: ['2026-10-29'],
      notes: [],
    })}\n`
    writeFileSync(join(directory, 'runs-summary-7d.json'), seeded)
    const answered = run(directory, ['runs', 'summary', '--json', '--since=7d', '--project', 'e2e'])
    expect(answered.status).toBe(0)
    expect(answered.stdout).toBe(seeded)
    const decoded = decodeRunsSummary(JSON.parse(answered.stdout))
    expect(decoded.ok).toBe(true)
    expect(decoded.ok && decoded.value.totals.computeMicros).toBeNull()
    expect(decoded.ok && decoded.value.daysUnknown).toEqual(['2026-10-29'])
    // The window is what names the document, so a window nothing was seeded for
    // is refused by name rather than answered with the other window's figures --
    // and a read with no window at all is one this stub cannot answer either.
    const other = run(directory, ['runs', 'summary', '--json', '--since=1d', '--project', 'e2e'])
    expect(other.status).toBe(2)
    expect(other.stderr).toContain("no runs summary document is seeded for '1d'")
    const windowless = run(directory, ['runs', 'summary', '--json', '--project', 'e2e'])
    expect(windowless.status).toBe(2)
    expect(windowless.stderr).toContain('was asked for no --since')
  })
  it('answers a launch with the run id and repository it was handed, and the console reads it', () => {
    // The console's one write (design section 5.1 rule 11, V2.5.1a): a spec that
    // launches in a real window reads this answer back through the launcher's own
    // decoder, so a stub that printed a shape `decodeLaunchResult` refuses would
    // put the whole launch path in front of a refusal the CLI never made.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    const asked = [
      'run',
      '--task-file',
      '-',
      '--json',
      '--run-id',
      '20261029-044952-1a2b',
      '--repo',
      'owner/repo-a',
      '--title=a launch of the recorded repository',
      '--project',
      'e2e',
    ]
    const answered = run(directory, asked)
    expect(answered.status).toBe(0)
    const decoded = decodeLaunchResult(answered.stdout)
    expect(decoded.ok).toBe(true)
    // The two identity checks the launcher makes of every reply, held here where
    // there is no Electron: the id the console generated and the repository it
    // converted the record to are what the answer must carry back.
    expect(decoded.ok && decoded.value.runId).toBe('20261029-044952-1a2b')
    expect(decoded.ok && decoded.value.repo).toBe('owner/repo-a')
    expect(decoded.ok && decoded.value.project).toBe('e2e')
    expect(decoded.ok && decoded.value.status).toBe('launched')
    // `run` is the id as a screen draws it, and the branch the window's own
    // `ref` row would show is the document's own business (`branch`/`ref` are
    // the two fields the launcher does not check).
    expect(decoded.ok && decoded.value.run).toBe('20261029-044952-1a2b')
    expect(decoded.ok && decoded.value.recipe).toBe(null)
    expect(decoded.ok && decoded.value.projectLayer).toBe(null)
    expect(decoded.ok && decoded.value.pr).toBe(null)
  })

  it('records every launch it answers in `runs.log`, with the task it read on stdin', () => {
    // The stub's one side effect, and the only place a spec can count the
    // children a confirmation spawned: one line per invocation, the argv and --
    // after the tab -- the task the console wrote to the child's stdin. The
    // task's newlines are folded so the count of invocations is the count of
    // lines.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    const asked = (id: string): readonly string[] => [
      'run',
      '--task-file',
      '-',
      '--json',
      '--run-id',
      id,
      '--repo',
      'owner/repo-a',
      '--project',
      'e2e',
    ]
    expect(run(directory, asked('20261029-044952-1a2b'), 'first line\nsecond line\n').status).toBe(0)
    expect(run(directory, asked('20261029-045000-bcde'), 'another\n').status).toBe(0)
    // A read is not a launch and writes no line: the file is the count of
    // children a confirmation spawned, and nothing else.
    writeFileSync(join(directory, 'version.txt'), '0.12.0\n')
    expect(run(directory, ['version']).status).toBe(0)
    const lines = readFileSync(join(directory, 'runs.log'), 'utf8').split('\n').filter((line) => line !== '')
    expect(lines).toHaveLength(2)
    expect(lines[0]).toBe(
      'run --task-file - --json --run-id 20261029-044952-1a2b --repo owner/repo-a --project e2e\tfirst line second line',
    )
    expect(lines[1]).toContain('20261029-045000-bcde')
  })

  it('refuses a launch argv the row does not carry, and a launch with no seeded project', () => {
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    // A word rule 11's row has no place for. The row is `run --task-file -
    // --json --run-id <id> --repo <owner/name> [--title=<T>] [--plan-task=<P>]
    // --project <name> [--config <path>]`, and a console that asked for anything
    // else is one whose launch this stub must not answer.
    const extra = run(directory, [
      'run',
      '--task-file',
      '-',
      '--json',
      '--run-id',
      '20261029-044952-1a2b',
      '--repo',
      'owner/repo-a',
      '--project',
      'e2e',
      '--pr',
      '7',
    ])
    expect(extra.status).toBe(2)
    expect(extra.stderr).toContain("was asked for '--pr'")
    // The task arrives on stdin and never as a word.
    const positional = run(directory, ['run', '--json', '--run-id', '20261029-044952-1a2b', '--project', 'e2e'])
    expect(positional.status).toBe(2)
    expect(positional.stderr).toContain("answers 'run --task-file - --json'")
    // And the `--project` every read and this write carry is held to the seeded
    // one, like every other command's.
    const other = run(directory, [
      'run',
      '--task-file',
      '-',
      '--json',
      '--run-id',
      '20261029-044952-1a2b',
      '--repo',
      'owner/repo-a',
      '--project',
      'other',
    ])
    expect(other.status).toBe(2)
    expect(other.stderr).toContain("was asked without --project e2e")
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
    // ...and the one **write** (V2.5.1a): a launch a spec did not expect is
    // named in that same sentence rather than answered by a fallback.
    expect(answered.stderr).toContain('run --task-file - --json')
    expect(answered.stderr).toContain("was asked for 'runs'")
  })
})

// ---------------------------------------------------------------------------
// The settings arms (the settings-edit plan's C4, D1118).
// ---------------------------------------------------------------------------

/** SETTINGS_FIXTURE is the committed `settings --json` document the stub's read
 * arm answers with, at the path `cliStub.ts`-installed stubs are given it. */
const SETTINGS_FIXTURE = join(__dirname, '..', 'fixtures', 'settings.json')

/** recorded is every line the stub appended to `reads.log` beside itself. */
function recorded(directory: string): readonly string[] {
  let text: string
  try {
    text = readFileSync(join(directory, 'reads.log'), 'utf8')
  } catch {
    return []
  }
  return text.split('\n').filter((line) => line !== '')
}

/** settingsArgvs is the **command lines** the stub recorded for the settings
 * arms: the lines that begin with the stub's own path. The read arms' labels are
 * words (`settings`, `budget`), and the settings write's arm is the one that
 * records a whole invocation -- the program as `$0` and then the words it was
 * handed, which is the same list the confirmation window draws. */
function settingsArgvs(directory: string): readonly string[] {
  return recorded(directory).filter((line) => line.startsWith(`${join(directory, 'fugaro')} settings`))
}

describe('the stub’s settings arms', () => {
  it('answers `settings --json` with the committed document, and the console reads it', () => {
    // The Project tab's third read (the settings-edit plan's C1, contract section
    // 6.19): the document the section is a function of. A shape the console's own
    // reader refuses would put the tab in front of the panel's refusal instead of
    // a table, and -- worse for this plan -- the rows' three writability members
    // are what every pencil is drawn from.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    const seeded = readFileSync(SETTINGS_FIXTURE, 'utf8')
    writeFileSync(join(directory, 'settings.json'), seeded)
    const asked = ['settings', '--json', '--project', 'e2e']
    const answered = run(directory, asked)
    expect(answered.status).toBe(0)
    expect(answered.stdout).toBe(seeded)
    // Read through the console's own builder, which is what the section draws.
    const built = buildProjectsSettings({ raw: JSON.parse(answered.stdout), because: null })
    expect(built.panel.state).toBe('read')
    expect(built.panel.source).toContain('settings --json')
    // The rows the tab draws, and the two facts the pencil hangs on: the
    // document's own `writable` **and** a non-empty `target_layer` (D1102,
    // D1111). The owned row is the one that tells the two apart: its
    // `target_layer` is the layer its key may be set in -- a fact about the key,
    // which the read answers for a row the generic path does not write as well
    // (`settingsDefaultLayer`, `internal/cli/settings_set.go`) -- so what keeps
    // the pencil off it is `writable: false`. The sensitive row is not here at
    // all -- it is dropped whole rather than drawn without a value.
    expect(built.rows.map((row) => row.key)).toEqual([
      'agent.review_rounds',
      'claude_account_auto',
      'mailbox.keychain_item',
    ])
    expect(built.rows.map((row) => row.pencil)).toEqual([true, true, false])
    expect(built.rows.map((row) => row.targetLayer)).toEqual(['repo', 'local', 'local'])
    expect(built.rows[2]?.owner).toBe('fugaro secrets')
    // Two members the schema leaves free strings, and the fixture's spelling of
    // each is the read's own: `state` is drawn in the document's words, so the
    // row the cached project layer decided says the cache's age and not `ok`
    // (`staleState`, `internal/cli/settings_read.go`), and a block key's value is
    // what the read renders for a mapping -- compact JSON, pairing uid to
    // keychain item name (`renderValue`, and the name is the local config's own
    // alphabet, so the invented item name has no space in it). `value` is a free
    // string in the schema too, which is why both are pinned here rather than
    // there.
    expect(built.rows.map((row) => row.state)).toEqual(['stale 3h', 'ok', 'ok'])
    expect(built.rows[2]?.value).toBe('{"m~AAECAwQFBgcICQoLDA0ODw":"fugaro-mailbox-example-project"}')
    expect(built.rows.map((row) => row.key)).not.toContain('budget.firebase_api_key')
    // The read is one of the answers `reads.log` records, so a spec that waits on
    // it rather than on a sleep has something to wait for.
    expect(recorded(directory)).toContain('settings')
  })

  it('answers the dry run from the document seeded for its key, and records its argv', () => {
    // C3's first spawn: `settings set|unset KEY --layer <L> --dry-run --json`.
    // The document is seeded per key, the way every other answer of this stub is
    // seeded per file, and the argv is recorded so a spec can hold the window's
    // own drawing to what was actually spawned.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    const dry = `${JSON.stringify({
      version: 1,
      key: 'agent.review_rounds',
      layer: 'project',
      path: '/home/owner/.config/fugaro/projects/example.yaml',
      changed: true,
      widening: false,
      why: '',
      old: '2',
      new: '3',
      refusal: '',
    })}\n`
    writeFileSync(join(directory, 'settings-dryrun-agent.review_rounds.json'), dry)
    const asked = [
      'settings',
      'set',
      'agent.review_rounds',
      '--layer',
      'project',
      '--dry-run',
      '--json',
      '--project',
      'e2e',
      '--',
      '3',
    ]
    const answered = run(directory, asked)
    expect(answered.status).toBe(0)
    expect(answered.stdout).toBe(dry)
    // The console's own reader, which both the window and the flow read this
    // document with: `refusal` empty is the document this CLI produces.
    const read = settingsDryRun(JSON.parse(answered.stdout))
    expect(read.ok).toBe(true)
    expect(read.ok && read.dryRun.key).toBe('agent.review_rounds')
    expect(read.ok && read.dryRun.widening).toBe(false)
    expect(read.ok && read.dryRun.new).toBe('3')
    // The recorded line is the whole command line, `$0` included: that is the
    // list the confirmation window draws word for word.
    expect(settingsArgvs(directory)).toEqual([`${join(directory, 'fugaro')} ${asked.join(' ')}`])
  })

  it('answers the write with the CLI’s own sentence, and records the same argv', () => {
    // C3's second spawn: the same words with `--yes` instead of `--dry-run
    // --json` (D1107). Its answer is a sentence rather than a document -- the
    // console reads it as text and draws it (`settingsedit.ts`) -- and it ends
    // with the line every settings write ends with.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    const asked = [
      'settings',
      'set',
      'agent.review_rounds',
      '--layer',
      'project',
      '--yes',
      '--project',
      'e2e',
      '--',
      '3',
    ]
    const answered = run(directory, asked)
    expect(answered.status).toBe(0)
    expect(answered.stdout).toContain('wrote /home/owner/.config/fugaro/projects/example.yaml')
    expect(answered.stdout).toContain('next: fugaro update')
    expect(settingsArgvs(directory)).toEqual([`${join(directory, 'fugaro')} ${asked.join(' ')}`])
    // An `unset` carries no value and no terminator, and this stub answers it
    // too: the console sends the same subcommand for an empty box
    // (`openSettingsEdit`), so the arm's second spelling is reachable without a
    // second stub.
    const unset = ['settings', 'unset', 'agent.review_rounds', '--layer', 'project', '--yes', '--project', 'e2e']
    expect(run(directory, unset).status).toBe(0)
    expect(settingsArgvs(directory)).toHaveLength(2)
  })

  it('refuses what the write’s row cannot carry, and a dry run with nothing seeded', () => {
    // Every word of the row is held, the way `run`'s is: a console that asked
    // for something the spawn table cannot produce must rather fail here, with
    // the argv it was handed, than be answered by a fallback.
    const directory = install()
    writeFileSync(join(directory, 'project.txt'), 'e2e\n')
    // A verb of `settings` that is neither the read nor a write.
    const verb = run(directory, ['settings', 'ls', '--project', 'e2e'])
    expect(verb.status).toBe(2)
    expect(verb.stderr).toContain("was asked for 'settings'")
    // A `--layer` with no value, a value on an `unset`, and a `set` with no
    // value at all: the terminator is the write's own shape for a value.
    const layerless = run(directory, ['settings', 'set', 'agent.review_rounds', '--project', 'e2e', '--layer'])
    expect(layerless.status).toBe(2)
    expect(layerless.stderr).toContain("'--layer' with no value")
    const valued = run(directory, [
      'settings',
      'unset',
      'agent.review_rounds',
      '--layer',
      'project',
      '--yes',
      '--project',
      'e2e',
      '--',
      '3',
    ])
    expect(valued.status).toBe(2)
    expect(valued.stderr).toContain('an unset carries no value')
    const valueless = run(directory, ['settings', 'set', 'agent.review_rounds', '--layer', 'project', '--yes', '--project', 'e2e'])
    expect(valueless.status).toBe(2)
    expect(valueless.stderr).toContain('a set carries one behind --')
    // And a key with no dry-run document seeded beside the stub is refused by
    // name: a document for another key would be a classification of an edit the
    // console never asked about.
    const missing = run(directory, [
      'settings',
      'set',
      'agent.review_rounds',
      '--layer',
      'project',
      '--dry-run',
      '--json',
      '--project',
      'e2e',
      '--',
      '3',
    ])
    expect(missing.status).toBe(2)
    expect(missing.stderr).toContain("no dry run document is seeded for 'agent.review_rounds'")
  })
})
