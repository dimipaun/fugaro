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

import { SPAWN_COMMANDS } from '../../src/main/spawn'
import { STUB_COMMAND_KEYS, commandKey, installStub } from './stub'

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
      'plan status',
      'diagnose',
      'logs',
      'logs --url',
      'secrets ls',
      'accounts ls',
      'version',
      'doctor',
      'config list',
      'coordinator status',
      // Coordinator v2 C3's `coordinator.show` / `coordinatorShow`: the pull
      // request read next to the project view's, and the second row here whose
      // child mints a credential of the host's.
      'coordinator show',
      'run',
    ])
    // The console's one spawn table, which since the owner's design ruling of
    // 2026-10-25 includes the installation rail's own read (`config list
    // --json`, the discovery the whole first run is built on) and, since V2.5.1a
    // part 2a, the surface's one write (`run.launch`, whose argv is `run
    // --task-file - --json`, so the child's key is the command word `run`). A
    // stub that answered every row but one would make every discovery test pass
    // for the wrong reason.
    expect(STUB_COMMAND_KEYS).toEqual(SPAWN_COMMANDS.map((row) => commandKey([...row.words])))
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

  it('carries the key derivation into the child rather than a second copy', () => {
    // The generated script is the child's whole program, and the review's
    // finding was that it held its own inlined reading of argv. It now runs this
    // function's source, so there is one implementation and no drift.
    const directory = freshDirectory()
    installStub(directory, { commands: { version: { stdout: '0.8.1\n' } } })
    const script = readFileSync(join(directory, 'fugaro-stub.mjs'), 'utf8')
    // The function, as source (the transpiler's own quoting, hence the
    // quote-agnostic patterns), and the one call that produces the key.
    expect(script).toContain('function commandKey(argv)')
    expect(script).toMatch(/words\[0\] === "logs"/)
    expect(script).toContain('logs --url')
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
