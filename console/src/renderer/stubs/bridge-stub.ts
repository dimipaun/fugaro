// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The stub bridge: the nine new operations, answered from the console's own
// fixtures (C-OPS; the contract is `src/shared/new-ops.ts`, the document is
// `docs/console-improvements/contract-ops.md`).
//
// **Why a stub at all.** A contract written before its producers exists is a
// claim nobody can run. The types in `new-ops.ts` say what each operation
// answers; this module is the same claim in runnable form, and `new-ops.test.ts`
// holds the two to each other by decoding every fixture here against the type it
// stands for. So a consumer -- a screen, a section, a wizard step -- is written
// against a bridge that answers, before the allow-list (AL) has a row, before
// the source (SRC) has a handler and before any CLI carries the flags.
//
// **Three things this module is not.**
//
//   - It is not part of the shipped console. Nothing imports it but the tests:
//     the bundler walks from the entry points, and no entry reaches this
//     directory, so a stub answer cannot be drawn in a real window. A consumer's
//     own test builds **its own** fake bridge from `new-ops.ts`'s types rather
//     than importing this one, which is why I-OPS can delete the file whole.
//   - It is not the fixtures' home after integration. I-OPS holds these answers
//     to the CLI goldens the CLI tasks record (`internal/cli/testdata/*.golden*`,
//     `ee/report/cli/testdata/report_stream.golden.ndjson`) and deletes the file
//     when the real handlers answer. Until then these values stand for the
//     goldens and are, deliberately, no more than plausible: what is pinned here
//     is the **shape** -- the numbers, the names and the rely-ons of the
//     contract -- and never a byte of a CLI's output that does not exist yet.
//   - It is not a place to hide a document this contract has not read. Every
//     fixture below is either the CLI's own document under `document` or a
//     feed's `{lines: [...]}`, and every key in it is read by a decoder or by a
//     screen that already draws that document.
//
// **The mutating consumer.** A fixture is the same object on every call, so it
// is copied on the way out: a test that edited the answer it was handed -- the
// `new-ops.test.ts` cases that take a field out of one do exactly that -- would
// otherwise leave the next reader of the same fixture a document with a hole in
// it, and the failure would land in a test that never touched it.

import { NEW_OPERATION_METHODS, NEW_OPERATION_NAMES } from '../../shared/new-ops'
import type { NewOperationName } from '../../shared/new-ops'

/** STUB_FIXTURES is one answer per operation, keyed by the operation's own name:
 * the reply's `data` half, which is what the contract's decoder admits (the CLI
 * document under `document`, or the settings editor's outcome object itself).
 * Every value is plain JSON so that `copy` below reads and never throws. */
export const STUB_FIXTURES: Readonly<Record<NewOperationName, unknown>> = Object.freeze({
  // A window with one finished run in it. The row is a `runs ls --json` row --
  // the same members, spelled the same way -- under the feed's own `row` key,
  // which is the line shape the CLI writes (`streamRowLine`,
  // `internal/cli/runs_stream.go`): the feed's rows are read by that document's
  // own reader (`decodeRunsList`, which reads the `row` member and not the
  // line), and `end` says the stream finished: `version` is the runs list
  // document's, `count` is the rows before it.
  'runs.stream': {
    document: {
      lines: [
        {
          type: 'row',
          row: {
            run: '20261118-101500-f2ff',
            repo: 'acme/app',
            status: 'succeeded',
            terminal: true,
            title: 'the retry banner loses its count on a narrow window',
            plan_task: 'console-improvements EO4',
            created: '2026-11-18T10:15:00Z',
          },
        },
        {
          type: 'row',
          row: {
            run: '20261118-091200-0a11',
            repo: 'acme/website',
            status: 'implement',
            terminal: false,
            title: 'the changelog link should open the release notes',
            plan_task: 'website W7.2',
            created: '2026-11-18T09:12:00Z',
          },
        },
        { type: 'end', version: 1, count: 2 },
      ],
    },
  },

  // Two days of spend and the report's totals, in the report document's own row
  // shape (`key`, `*_micros`, `runs`), under the line's `row` key: the screen
  // that draws them is the Spend tab's, and its reader is the one that knows the
  // rest of the row.
  'spend.stream': {
    document: {
      lines: [
        {
          type: 'row',
          row: {
            key: '2026-11-18',
            start: '2026-11-18',
            end: '2026-11-18',
            runs: 3,
            run_hours: 1.25,
            model_micros: 4125000,
            notional_micros: 0,
            compute_micros: 310000,
          },
        },
        {
          type: 'row',
          row: {
            key: '2026-11-17',
            start: '2026-11-17',
            end: '2026-11-17',
            runs: 5,
            run_hours: 2.5,
            model_micros: 6875000,
            notional_micros: 0,
            compute_micros: null,
          },
        },
        {
          type: 'end',
          version: 1,
          count: 2,
          totals: {
            runs: 8,
            run_hours: 3.75,
            model_micros: 11000000,
            notional_micros: 0,
            compute_micros: 310000,
          },
        },
      ],
    },
  },

  // The caps, repository by repository and then the kill switch, which is a row
  // of its own (RS4: "`row` lines per repository and per kill switch as their
  // reads complete"), each under the line's `row` key as the CLI writes them.
  // This end line carries no `version`, because the `budget show --json`
  // document carries none either: a console that read a missing version as a
  // broken stream would call this finished read interrupted.
  'budget.stream': {
    document: {
      lines: [
        {
          type: 'row',
          row: {
            slug: 'acme/app',
            repo: 'acme/app',
            daily_cap_usd: 25,
            daily_cap_source: 'repo',
            per_run_cap_usd: 5,
            per_run_cap_source: 'repo',
            counted_usd: 3.5,
            spent_usd: 1.25,
            notional_usd: 0,
            leased_usd: 2.25,
            headroom_usd: 21.5,
            runs: 2,
            kill: null,
          },
        },
        {
          type: 'row',
          row: {
            slug: 'acme/website',
            repo: 'acme/website',
            daily_cap_usd: 25,
            daily_cap_source: 'default',
            per_run_cap_usd: 5,
            per_run_cap_source: 'default',
            counted_usd: 0,
            spent_usd: 0,
            notional_usd: 0,
            leased_usd: 0,
            headroom_usd: 25,
            runs: 0,
            kill: null,
          },
        },
        {
          type: 'row',
          row: {
            slug: 'kill',
            kill: {
              by: 'dana@example.com',
              at: '2026-11-18T09:00:00Z',
              reason: 'the day’s spend reached the cap while a run was looping',
            },
          },
        },
        { type: 'end', count: 3 },
      ],
    },
  },

  // The caller's own role (WH1). `admin` reaching one project is the ordinary
  // shape on an owner's machine, and it is the one that enables the writes.
  whoami: {
    document: {
      version: 1,
      role: 'admin',
      scope: 'project',
      repos: ['acme/app', 'acme/website'],
    },
  },

  // The installation's catalog. `digest` is a placeholder rather than a hash:
  // the CLI writes the last build's own digest, and this fixture stands for the
  // golden and not for a build.
  'image.catalog.ls': {
    document: {
      project: 'fugaro-dev',
      entries: [
        {
          name: 'base',
          tags: ['v1', 'latest'],
          default: true,
          source: 'images/base',
          description: 'the pinned base image every repository image derives from',
          digest: 'the digest the last build recorded',
        },
        {
          name: 'node22-playwright',
          tags: ['v3'],
          default: false,
          source: 'images/node22-playwright',
          description: 'Node 22 with the browser the console’s e2e specs need',
          digest: '',
        },
      ],
    },
  },

  // The three layers' recipes for one name, the winner first, with a note the
  // CLI put beside them: `use_when` is what a routing skill matches on.
  'recipes.ls': {
    document: {
      recipes: [
        {
          name: 'quick',
          source: 'repo',
          description: 'one implement round and a review, for a small change',
          use_when: 'the task is one file and the tests already cover it',
          steps: [
            { kind: 'first_line' },
            { kind: 'review', bounce: true, max_rounds: 2 },
          ],
          launchable: true,
        },
        {
          name: 'quick',
          source: 'catalog',
          description: 'the catalog’s own quick recipe',
          use_when: 'a small change',
          steps: [{ kind: 'first_line' }],
          launchable: true,
          shadowed_by: 'repo',
        },
        {
          name: 'renovate',
          source: 'project',
          launchable: false,
          problem: 'steps[0]: unknown step kind "bump"',
        },
      ],
      notes: ['project recipe renovate has no use_when; the routing skill matches on use_when, add one'],
    },
  },

  // A plan-only run: the converge's stages, one step, and the two things only a
  // person can do -- with the commands the wizard offers to copy.
  'setup.plan': {
    document: {
      lines: [
        {
          type: 'stage',
          name: 'project',
          state: 'ready',
          detail: 'fugaro-dev exists and its APIs are on',
          after: '',
          undone: false,
        },
        {
          type: 'step',
          stage: 'project',
          name: 'billing',
          detail: 'the billing account is linked',
        },
        {
          type: 'left_for_you',
          stage: 'access',
          kind: 'command',
          text: 'grant the first administrator, so this installation has someone who can publish',
          commands: ['fugaro user grant dana@example.com admin'],
        },
        { type: 'done' },
      ],
    },
  },

  // An apply that finished: the same shape, and the same feed type.
  'setup.apply': {
    document: {
      lines: [
        {
          type: 'stage',
          name: 'project',
          state: 'ready',
          detail: 'fugaro-dev exists and its APIs are on',
          after: '',
          undone: false,
        },
        {
          type: 'stage',
          name: 'policy',
          state: 'ready',
          detail: 'the published policy is version 4',
          after: 'project',
          undone: false,
        },
        {
          type: 'left_for_you',
          stage: 'billing',
          kind: 'terminal',
          text: 'creating the project and linking billing stay yours',
          commands: [],
        },
        { type: 'done' },
      ],
    },
  },

  // The settings editor's outcome, the shape `settings.edit` answers: a key that
  // exists was opened, and the sentence says which one.
  'settings.editor.open': {
    ok: true,
    sentence: 'editing agent.models.coder for this project',
  },
})

/** copy is a fixture as a value of its own, so a caller that edits what it was
 * handed cannot change what the next caller reads. The fixtures are plain JSON
 * by construction, so a round trip is exact. */
function copy<Value>(value: Value): Value {
  return JSON.parse(JSON.stringify(value)) as Value
}

/** StubBridge is the shape `newOpsFrom` reads: one method per operation, each
 * answering the console's own reply envelope. */
export type StubBridge = Readonly<Record<string, (payload: unknown) => Promise<unknown>>>

/**
 * stubBridge is a bridge that answers all nine operations from the fixtures
 * above, whatever payload it is handed: it validates nothing, because the
 * payload's validation is the allow-list's job in main (AL) and a stub that
 * checked a payload would be a second copy of a rule it does not own.
 */
export function stubBridge(): StubBridge {
  const bridge: Record<string, (payload: unknown) => Promise<unknown>> = {}
  for (const name of NEW_OPERATION_NAMES) {
    bridge[NEW_OPERATION_METHODS[name]] = () =>
      Promise.resolve({ ok: true, data: copy(STUB_FIXTURES[name]) })
  }
  return bridge
}
