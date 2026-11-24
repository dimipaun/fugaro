// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The fake CLI's own contract (desktop-console plan Task 1.3's helper).
//
// `stub.ts` is the only thing the spawn tests ever run, so a defect in it is a
// defect in every test that trusts it. The review of Task 3.2 found the one this
// file exists for: `commandKey` was exported and used nowhere, its docstring
// claimed a test that did not exist, and the generated child carried a second,
// inlined copy of the same rules -- so a test author who keyed a scenario with
// the spawn table's own spelling (`'logs --json'`, which is what `spawn.test.ts`
// pins verbatim) fell through to `scenario.default`: exit 0, no output, and a
// green assertion about a fallback rather than about their fixture.

import { mkdtempSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { SETTINGS_EDIT_OPERATION, SPAWN_COMMANDS } from '../../src/main/spawn'
import { SETTINGS_EDIT_KEY, STUB_COMMAND_KEYS, commandKey, installStub } from './stub'

/** freshDirectory is a temporary directory this test owns. */
function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-console-stub-'))
}

describe('the stub’s command keys', () => {
  it('are the spawn table’s operations, read through the child’s own reader', () => {
    // The docstring's claim, finally as a test: every row of the console's spawn
    // table produces a key the stub answers, and the keys are what the settings
    // above say they are. `commandKey` is the function the generated child runs
    // (its own source, injected), so this is the agreement itself and not a
    // second spelling of it.
    expect([...STUB_COMMAND_KEYS]).toEqual([
      'watch',
      'budget show',
      'report',
      'runs ls',
      'runs stats',
      // Console 0.119.0's jobs-free totals read (`runs.summary` /
      // `summaryRuns`, B4's console half): the one row here whose child reads
      // neither a run object nor a listing -- the budget database's day
      // counters and the spend history's daily documents are the whole read.
      'runs summary',
      'plan status',
      // The graph plan's CG9a (`graph.read` / `graphRead`): the dependency graph
      // as one versioned document, spawned with no checkout at all. Its key is
      // the command word, because `--source` and its value are the row's own
      // argument and never a word of the key.
      'graph',
      // The graph plan's CG9b (`graph.watch` / `graphWatch`): the same document
      // as a live feed, on a long-lived child. It shares the read's command
      // word, so `--watch` is what tells the two apart -- and a key shared with
      // the read above would leave this row's scenarios answered by `default`.
      'graph --watch',
      // The dependency-graph plan's GV3.2 (`roadmap.move` /
      // `moveRoadmapTask`): `coordinator roadmap move`, the console's third
      // write and the one this table's group prefix narrows -- the key is the
      // command's three words, because the task, `--to` and `--expect` are the
      // row's own arguments and never words of the key.
      'coordinator roadmap move',
      'diagnose',
      'logs',
      'logs --url',
      'secrets ls',
      'accounts ls',
      // Console 0.119.0's offline models read (`models.list` / `listModels`,
      // the read-only models panel, the owner's ruling of 2026-11-02): the one
      // key here whose command path is also a group the never table refuses,
      // so `models ls` is the whole of what the stub may be asked for.
      'models ls',
      // The models write's two phases (MW1, D2903): `models.add.plan` is the
      // dry run and `models.add` is the write, so `--dry-run` is the mode word
      // that tells the two apart -- the same shape `setup`'s `--plan-only` and
      // the three `--stream` reads have. The model ID behind `--` and the three
      // `=`-form values are the rows' own arguments and never words of the key.
      'models add --dry-run',
      'models add',
      // The image write's two phases (MW2, D2903), the models write's shape one
      // group over: `image.add.plan` is the dry run and `image.add` is the
      // write, and `--dry-run` is the mode word that tells the two apart. The
      // entry's name behind `--` and the three `=`-form values are the rows' own
      // arguments and never words of the key.
      'image add --dry-run',
      'image add',
      // The settings-edit plan's C1 (`settings.read` / `readSettings`): the
      // Project tab's third read. Its key is the command alone -- the group's
      // other words are not on the spawn table at all -- and the scenario
      // keyed `'settings'` is what a test hands the section's fixture.
      'settings',
      // The settings-edit plan's C3 (`settings.edit` / `editSetting`): the
      // write, and the one key a row's own words cannot produce. `set`/`unset`
      // and the key it names are the edit's, so the child's leading words are
      // `settings set <KEY>` -- a key per edit -- and the read above would
      // otherwise own the whole group. Its key is the operation, and both
      // spellings of a write answer to it (the assertions below).
      'settings edit',
      'version',
      'doctor',
      'config list',
      'coordinator status',
      // Coordinator v2 C3's `coordinator.show` / `coordinatorShow`: the pull
      // request read next to the project view's, and the second row here whose
      // child mints a credential of the host's (`run`'s below is the first).
      'coordinator show',
      // Coordinator v2 C4's `coordinator.intents` / `coordinatorIntents`: the
      // declarations the fleet's writers stored, and the one row here with no
      // argument at all -- its words are the whole argv.
      'coordinator intents',
      // The console-fixes plan's CG7a (`orchestrator.map` / `orchestratorMap`,
      // D3221): the Coordinator tab's map read, and the second row here with no
      // argument at all -- its words are the whole argv, so its key is the
      // command path, `orchestrator map`, and nothing of a caller's reaches the
      // child. The row is behind `ORCHESTRATOR_MAP_CLI_FLOOR`
      // (`src/shared/version.ts`), which is unset today, so the read is refused
      // at the source and no scenario reaches this key through a real spawn --
      // the key is here because the row is: the list is derived from the table
      // below, and a row the stub cannot key is the silent fallback this file
      // exists to catch.
      'orchestrator map',
      // The managed screens' plan's CM4 (`managed.status` / `managedStatus`):
      // what the two marks say about the project and the newest audit record,
      // with no argument at all -- so its key is its command words, `managed
      // status`, and nothing of a caller's reaches the child.
      'managed status',
      // The same plan's `policy.show` / `policyShow`: the published policy node
      // the runs read, and the read the Project tab's policy panel was missing
      // (F4). Its key is the command path, `project policy show`, because the
      // group's other words -- `policy publish`, `repos add`, `repos rm`, `sync`
      // -- are on no row of this table.
      'project policy show',
      // The repos-management plan's RE5 (`repos.ls` / `listRepos` and
      // `repos.status` / `reposStatus`): the `project repos` group's two reads
      // and the whole of its reach -- `repos add` and `repos rm` are on the
      // never table, so neither is a key here. Each key is the command path,
      // because `--json` is the row's own word and neither row carries an
      // argument (`repos.status`'s optional repository is deliberately not one,
      // so no scenario can ask for it either).
      'project repos ls',
      'project repos status',
      // The subscriptions plan's S4.2 (`seats.read` / `readSeats`): the Project
      // tab's advisory seat estimate, and the one read here whose child lists
      // the installation's bucket -- its key is the command word, because the
      // 90 days it is measured over and the `--json` are the row's own words.
      'seats',
      // The roles-and-scopes plan's RS7 (`members.list` / `listMembers`): the
      // Members view's roster read. Its key is the command path, `user ls`,
      // because the `--json` is the row's own word and the `user` group's other
      // words -- `grant`, `revoke`, `add`, `rm`, `offboard` -- are on no row of
      // this table and sit on the never table instead.
      'user ls',
      'run',
      // The composite-commands plan's AP8 (`apply.pending` / `readPendingSteps`):
      // the pending-steps read. Its key is the command word alone, because
      // `--status` and `--json` are the row's own words -- and `--status` is a
      // word of the row's command path, so no scenario key ever spells it.
      'apply',
      // The console improvements' eight rows (task AL). Four of them are a
      // **second row on a command word already above**, and the mode word is
      // what tells the pair apart -- the same arrangement `logs --url` and
      // `graph --watch` have: the live read of the runs list (`runs ls
      // --stream`), the spend report (`report --stream`), the caps
      // (`budget show --stream`) and the caller's own role (`user ls --me`).
      // A key shared with the one-shot read above would leave these rows'
      // scenarios answered by `default`, which is this file's whole subject.
      'runs ls --stream',
      'report --stream',
      'budget show --stream',
      'user ls --me',
      // ...and four that are a group's first: the image catalog's one *read*
      // (`image ls`; the group's writers -- `image rm`, `image promote`, `image
      // build`, `image gate`, `image record`, `image refresh` -- are on the
      // never table, and `image add` is a row of its own above) and the
      // recipes (`recipes ls`; `recipes publish` and `recipes validate` are
      // not rows). Both carry no argument, so no scenario key can name one.
      'image ls',
      'recipes ls',
      // The first-run wizard's two rows (`setup.plan` / `setup.apply`), and
      // the second pair here told apart by a mode word: `--plan-only` is the
      // word that makes the child read instead of create, and it is why the
      // apply's own key is the bare command word.
      'setup --plan-only',
      'setup',
    ])
    // The console's one spawn table, which since the owner's design ruling of
    // 2026-10-25 includes the installation rail's own read (`config list
    // --json`, the discovery the whole first run is built on), since V2.5.1a
    // part 2a the surface's first write (`run.launch`, whose argv is `run
    // --task-file - --json`, so the child's key is the command word `run`) and
    // since the settings-edit plan's C3 the second (`settings.edit`, the one
    // row whose words do not produce its own key: see `SETTINGS_EDIT_KEY`). A
    // stub that answered every row but one would make every discovery test pass
    // for the wrong reason.
    expect(STUB_COMMAND_KEYS).toEqual(
      SPAWN_COMMANDS.map((row) =>
        row.operation === SETTINGS_EDIT_OPERATION ? SETTINGS_EDIT_KEY : commandKey([...row.words]),
      ),
    )
    // One key per operation: two rows sharing one would make one of them
    // unreachable and its scenario silently wrong.
    expect(new Set(STUB_COMMAND_KEYS).size).toBe(STUB_COMMAND_KEYS.length)
    expect(STUB_COMMAND_KEYS).toHaveLength(SPAWN_COMMANDS.length)
  })

  it('tells the two log operations apart by their own flag', () => {
    // `logs` is two rows: `--json` (the console's `--json` read) and `--url` (the
    // link). They share the command path, so the flag is what the child sees.
    expect(commandKey(['logs', '20261015-120000-aa01', '--json'])).toBe('logs')
    expect(commandKey(['logs', '20261015-120000-aa01', '--url'])).toBe('logs --url')
    expect(commandKey(['version'])).toBe('version')
    expect(commandKey(['budget', 'show', '--json', '--project', 'fugaro'])).toBe('budget show')
  })

  it('tells a command word’s two rows apart by the mode word, the console improvements’ included', () => {
    // The three live reads the console improvements add sit on a command word
    // that already had a one-shot read, and so does the caller's own role; the
    // wizard's two rows are the same arrangement with `--plan-only`. These are
    // real argvs, in the order `argvFor` emits them (`spawn.test.ts` pins the
    // vectors themselves), because the word has to be read off the argv the
    // child really gets rather than off a row's `words`.
    expect(commandKey(['runs', 'ls', '--stream', '--json', '--since', '24h', '--project', 'fugaro'])).toBe(
      'runs ls --stream',
    )
    expect(commandKey(['runs', 'ls', '--json', '--since', '24h', '--project', 'fugaro'])).toBe('runs ls')
    expect(commandKey(['report', '--stream', '--json', '--project', 'fugaro'])).toBe('report --stream')
    expect(commandKey(['report', '--json', '--project', 'fugaro'])).toBe('report')
    expect(commandKey(['budget', 'show', '--stream', '--json', '--project', 'fugaro'])).toBe(
      'budget show --stream',
    )
    expect(commandKey(['budget', 'show', '--json', '--project', 'fugaro'])).toBe('budget show')
    expect(commandKey(['user', 'ls', '--me', '--json', '--project', 'fugaro'])).toBe('user ls --me')
    expect(commandKey(['user', 'ls', '--json', '--project', 'fugaro'])).toBe('user ls')
    // The wizard: the plan's own argv, and the apply's, which is the plan's
    // less the word that decides whether the child creates anything.
    expect(
      commandKey(['setup', '--plan-only', '--answers-file', '-', '--events', '--json', '--project', 'fugaro']),
    ).toBe('setup --plan-only')
    expect(commandKey(['setup', '--answers-file', '-', '--events', '--json', '--project', 'fugaro'])).toBe('setup')
    // The two rows of the console improvements with no mode word at all: each
    // is its group's first read, so the command path is the whole key.
    expect(commandKey(['image', 'ls', '--json', '--project', 'fugaro'])).toBe('image ls')
    expect(commandKey(['recipes', 'ls', '--json', '--project', 'fugaro'])).toBe('recipes ls')
  })

  it('tells the settings write apart from the settings read, under one key for both spellings', () => {
    // The settings group is two operations on one command word, and the write's
    // own words are not a key: `set`/`unset` and the key it names vary per edit,
    // so a child reading them would compute `settings set budget.cap_usd` -- a
    // key no scenario can name, and one that changes the day a different key is
    // edited. These are real write argvs, in the order `argvFor` emits them
    // (`spawn.test.ts` pins the vectors themselves): the subcommand, the key,
    // the layer, the phase and, for a set, the value behind the terminator.
    expect(
      commandKey(['settings', 'set', 'budget.cap_usd', '--layer', 'local', '--dry-run', '--json', '--project', 'fugaro']),
    ).toBe('settings edit')
    expect(
      commandKey(['settings', 'unset', 'budget.cap_usd', '--layer', 'local', '--project', 'fugaro', '--config', '/tmp/config.toml']),
    ).toBe('settings edit')
    // ...and the read keeps its own key, which is the key the row's words
    // produce and the one every settings section's fixture already uses.
    expect(commandKey(['settings', '--json', '--project', 'fugaro'])).toBe('settings')
  })

  it('carries the key derivation into the child rather than a second copy', () => {
    // The generated script is the child's whole program, and the review's
    // finding was that it held its own inlined reading of argv. It now runs this
    // function's source, so there is one implementation and no drift.
    const directory = freshDirectory()
    installStub(directory, { commands: { version: { stdout: '0.8.1\n' } } })
    const script = readFileSync(join(directory, 'fugaro-stub.mjs'), 'utf8')
    // The function, as source (the transpiler's own quoting), and the one call
    // that produces the key. The lines pinned are the three that read something
    // other than the command path: `logs` and its positional, the settings
    // write, and the mode words that tell a command word's two rows apart.
    expect(script).toContain('function commandKey(argv)')
    expect(script).toMatch(/words\[0\] === "logs"/)
    expect(script).toContain('logs --url')
    expect(script).toMatch(/words\[0\] === "settings"/)
    expect(script).toContain('"--plan-only"')
    expect(script).toContain('const key = commandKey(argv)')
    // Exactly one place reads argv into a key: the copy the review found is
    // gone, so there is nothing left that can drift from `commandKey`.
    expect(script.match(/words\.join\(/g) ?? []).toHaveLength(1)
  })
})

describe('a scenario the stub cannot answer', () => {
  it('is refused at install time, with the key to use instead', () => {
    // `'logs --json'` is the spawn table's spelling and the one `spawn.test.ts`
    // pins verbatim; the stub's key is the command path the child sees. Before
    // this guard the scenario fell through to `default` and the test passed
    // against the fallback.
    expect(() => installStub(freshDirectory(), { commands: { 'logs --json': { stdout: '{"a":1}' } } })).toThrow(
      /did you mean "logs"\?/,
    )
    expect(() => installStub(freshDirectory(), { commands: { 'budget show --json': { stdout: '{}' } } })).toThrow(
      /did you mean "budget show"\?/,
    )
  })

  it('names the commands it can answer when there is nothing to suggest', () => {
    let message = ''
    try {
      installStub(freshDirectory(), { commands: { 'logs --follow': { stdout: '{}' } } })
    } catch (error) {
      message = error instanceof Error ? error.message : String(error)
    }
    expect(message).toContain('"logs --follow"')
    expect(message).toContain('"logs --url"')
    expect(message).toContain('"version"')
  })

  it('is refused when it is written in a second time, not only at install', () => {
    // `write` replaces the scenario, which is how a test asks the same stub for
    // something else mid-run. It ran no guard, so a misspelling there reached
    // the child and was answered by `default` -- the fallback this whole guard
    // exists to refuse (review). The call sites are `spawn.test.ts` and
    // `spend-controls.test.ts`.
    const stub = installStub(freshDirectory(), { commands: { version: { stdout: '1.4.2\n' } } })
    expect(() => stub.write({ commands: { 'logs --json': { stdout: '{}' } } })).toThrow(
      /did you mean "logs"\?/,
    )
    expect(() => stub.write({ commands: { 'runs ls': { stdout: '{}' } } })).not.toThrow()
  })

  it('accepts the keys it derives, and a scenario with no commands at all', () => {
    for (const key of STUB_COMMAND_KEYS) {
      expect(() => installStub(freshDirectory(), { commands: { [key]: { stdout: '' } } }), key).not.toThrow()
    }
    expect(() => installStub(freshDirectory(), { default: { stdout: '' } })).not.toThrow()
  })
})
