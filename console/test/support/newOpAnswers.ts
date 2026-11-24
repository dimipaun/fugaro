// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The nine new operations' answers as fixtures (C-OPS wrote them with the
// contract; I-OPS re-homed them here, out of the renderer, when the stub bridge
// was deleted).
//
// **Six of the nine answers are the CLI's own goldens**, transcribed line for
// line and byte for byte, from five recorded files:
// `internal/cli/testdata/runs_stream.golden.ndjson`, `whoami.golden.json`,
// `setup_events.golden.ndjson`, `budget_stream.golden.ndjson` and
// `ee/report/cli/testdata/report_stream.golden.ndjson`. They are what the CLI
// records its own output as, so a fixture here is the document a screen would
// really be handed -- and `ops-contract.test.ts` renders each one back into the
// CLI's framing (NDJSON lines, or the two-space document) and compares it with
// the golden file, so a fixture that drifts from the recording fails there
// rather than in a screen drawn from a document the CLI never writes.
//
// The three that are not golden-backed are the two one-shot reads the CLI
// records no golden for (`image.catalog.ls`, `recipes.ls`) and the settings
// editor's outcome, which spawns nothing and so has no CLI output at all. Their
// values are C-OPS's own and stay plausible rather than recorded: what is pinned
// is their **shape** -- the members a decoder or a screen reads -- and never a
// byte of output no task recorded.
//
// **A fixture is copied on the way out** (`newOpAnswersBridge`): the cases in
// `new-ops.test.ts` take a field out of the answers they are handed, and a
// fixture shared by reference would leave the next reader a document with a hole
// in it.

import { NEW_OPERATION_METHODS, NEW_OPERATION_NAMES } from '../../src/shared/new-ops'
import type { NewOperationName } from '../../src/shared/new-ops'
import type { JSONObject } from '../../src/main/spawn'

// ---------------------------------------------------------------------------
// The recorded answers: one constant per run the CLI recorded, in the order it
// wrote them. The setup golden is three runs -- the plan, the apply and the
// refused `--check` -- and the last is here for the reader's own tests rather
// than for an operation's answer, because making sense of a refused feed is the
// console's job and not the CLI's.
// ---------------------------------------------------------------------------

/** RUNS_STREAM_LINES is `runs ls --stream --json`'s recording: three rows and
 * the `end` line that says the read finished. */
const RUNS_STREAM_LINES: readonly JSONObject[] = Object.freeze([
  {"type":"row","row":{"run":"acme-app-dc4d4e59e884fc44/20261109-115900-cccc","repo":"acme/app","workflow":"web","title":"x","run_id":"20261109-115900-cccc","status":"unlaunched","requested_by":"someone@example.com","created":"2026-11-09T11:59:00Z","cost":{"model_usd":0,"compute_usd":0,"compute_estimated":true,"total_usd":0,"estimate":true,"model_basis":"api-list"},"terminal":false,"settled":true,"pushed":false,"follow_up":false}},
  {"type":"row","row":{"run":"acme-app-dc4d4e59e884fc44/20261109-115800-bbbb","repo":"acme/app","workflow":"web","title":"x","run_id":"20261109-115800-bbbb","status":"failed","stage":"verify","reason":"the run did not finish its work","failure_class":"no_commits","requested_by":"someone@example.com","created":"2026-11-09T11:58:00Z","cost":{"model_usd":1,"compute_usd":0,"compute_estimated":true,"total_usd":1,"estimate":true,"model_basis":"api-list"},"terminal":true,"settled":true,"pushed":false,"follow_up":false}},
  {"type":"row","row":{"run":"acme-app-dc4d4e59e884fc44/20261109-115700-aaaa","repo":"acme/app","workflow":"web","title":"x","run_id":"20261109-115700-aaaa","status":"failed","stage":"verify","reason":"the run did not finish its work","failure_class":"tests_failed","requested_by":"someone@example.com","created":"2026-11-09T11:57:00Z","cost":{"model_usd":4,"compute_usd":0,"compute_estimated":true,"total_usd":4,"estimate":true,"model_basis":"api-list"},"terminal":true,"settled":true,"pushed":false,"follow_up":false}},
  {"type":"end","count":3,"version":1},
])

/** BUDGET_STREAM_LINES is `budget show --stream --json`'s: two repositories,
 * the kill switch as a row of its own, and an `end` carrying no `version` --
 * because the caps document carries none either (the contract's rule 5). */
const BUDGET_STREAM_LINES: readonly JSONObject[] = Object.freeze([
  {"type":"row","row":{"slug":"acme-app-dc4d4e59e884fc44","repo":"acme/app","daily_cap_usd":25,"daily_cap_source":"repo","per_run_cap_usd":4,"per_run_cap_source":"repo","counted_usd":3.5,"spent_usd":1.25,"notional_usd":0.5,"leased_usd":2.25,"headroom_usd":21.5,"kill":{"by":"dana@example.com","at":"2026-11-09T10:00:00Z","reason":"a run looped on the same failing test"},"running":[{"slug":"acme-app-dc4d4e59e884fc44","run":"20261109-090000-aaaa","repo":"acme/app","workflow":"web","title":"add the export page","stage":"implement","round":2,"spent_usd":0,"started_at":"2026-11-09T11:15:00Z","updated_at":"2026-11-09T11:59:40Z"}]}},
  {"type":"row","row":{"slug":"acme-other-1140789cbb83e234","daily_cap_usd":null,"daily_cap_source":"none","per_run_cap_usd":20,"per_run_cap_source":"global","counted_usd":1,"spent_usd":1,"notional_usd":0,"leased_usd":0,"headroom_usd":95.5,"kill":null,"running":[]}},
  {"type":"row","row":{"slug":"kill","kill":{"by":"dana@example.com","at":"2026-11-09T11:00:00Z","reason":"the day's spend reached the cap"}}},
  {"type":"end","count":3,"version":1},
])

/** SPEND_STREAM_LINES is `report --stream --json`'s (`ee/report`): three days
 * and a totals line. */
const SPEND_STREAM_LINES: readonly JSONObject[] = Object.freeze([
  {"type":"row","row":{"key":"2026-10-14","start":"2026-10-14","end":"2026-10-14","partial":false,"model_micros":1500000,"model_usd":"1.500000","notional_micros":0,"notional_usd":"0.000000","compute_micros":200000,"compute_usd":"0.200000","compute_estimated":true,"run_hours":1.5,"runs":1,"calls":3}},
  {"type":"row","row":{"key":"2026-10-15","start":"2026-10-15","end":"2026-10-15","partial":false,"model_micros":2000000,"model_usd":"2.000000","notional_micros":700000,"notional_usd":"0.700000","compute_micros":null,"compute_usd":null,"compute_estimated":false,"run_hours":null,"runs":1,"calls":3}},
  {"type":"row","row":{"key":"2026-10-16","start":"2026-10-16","end":"2026-10-16","partial":false,"model_micros":500000,"model_usd":"0.500000","notional_micros":0,"notional_usd":"0.000000","compute_micros":null,"compute_usd":null,"compute_estimated":false,"run_hours":null,"runs":1,"calls":3}},
  {"type":"end","count":3,"version":1,"totals":{"key":"TOTAL","end":"2026-10-16","partial":false,"model_micros":4000000,"model_usd":"4.000000","notional_micros":700000,"notional_usd":"0.700000","compute_micros":200000,"compute_usd":"0.200000","compute_estimated":true,"run_hours":4.5,"runs":3,"calls":9}},
])

/** SETUP_PLAN_LINES is the setup golden's first run: the plan-only feed, which
 * finished with `exit_code` 0. */
const SETUP_PLAN_LINES: readonly JSONObject[] = Object.freeze([
  {"type":"stage","name":"preflight","state":"done","detail":"","after":"","undone":false},
  {"type":"stage","name":"installation","state":"will-do","detail":"0 imports, 1 creates, 0 updates","after":"","undone":false},
  {"type":"stage","name":"firebase","state":"skipped","detail":"no budget database was created: nothing answers this run's confirmation (no terminal, --json, a pipe and a coding agent never do); a project owner runs fugaro setup once","after":"","undone":false},
  {"type":"stage","name":"images","state":"skipped","detail":"no image to pin or copy: the history image goes with the history job (which this run deploys where it has just created or adopted the budget database, or where --firebase-project names it), and a base image with --base KIND, with a checkout whose fugaro.yaml names it, or with a project image catalog whose entries this installation builds","after":"","undone":false},
  {"type":"stage","name":"installation-2","state":"skipped","detail":"the installation has no budget backend, so there is no history job to deploy: run fugaro update once a project owner has created one","after":"","undone":false},
  {"type":"stage","name":"secrets","state":"skipped","detail":"not in a checkout of a repository: run fugaro setup there to take its secrets","after":"","undone":false},
  {"type":"stage","name":"plugin","state":"skipped","detail":"a development build has no release plugin to install","after":"","undone":false},
  {"type":"stage","name":"repository","state":"skipped","detail":"not in a checkout: run fugaro setup --repo in a checkout of the repository to onboard it","after":"","undone":false},
  {"type":"stage","name":"rules","state":"skipped","detail":"the project config records no shared Firebase budget backend (budget.firebase_project): fugaro update --firebase-project proj-1234 adopts one, and the security rules are deployed with it","after":"","undone":false},
  {"type":"stage","name":"model-prices","state":"done","detail":"<config> writes every cache price under the name this release documents","after":"","undone":false},
  {"type":"stage","name":"policy","state":"skipped","detail":"the project's config names no Firebase budget backend, so there is no published policy node to read","after":"","undone":true},
  {"type":"stage","name":"credentials-service","state":"skipped","detail":"a development build resolves no released image","after":"","undone":false},
  {"type":"stage","name":"credentials-copy","state":"skipped","detail":"the project's config carries no credentials_service block, and its presence is what turns the credentials service on: this installation has no credentials service, so there is no bundle for the copy to write a model key into","after":"","undone":false},
  {"type":"stage","name":"workspace-project","state":"skipped","detail":"this installation declares no workspace of its own: aurora's project config carries neither workspace_members nor workspace_cap, so there is no workspace service account to create and no database grant to make here (a member's project holds no grant on the workspace's project, spec §5)","after":"","undone":false},
  {"type":"stage","name":"workspace","state":"skipped","detail":"the project aurora declares no workspace: it has no workspace_members table and no workspace_cap, so it publishes no workspace record — a member's config names the workspace it joined (workspace: and workspace_pin:) and never publishes one","after":"","undone":false},
  {"type":"stage","name":"credentials-route","state":"skipped","detail":"the project's config names no Firebase budget backend, so there is no database to publish the credential route in","after":"","undone":false},
  {"type":"stage","name":"layer","state":"skipped","detail":"the project layer's bucket (gs://fugaro-runs-proj-1234) could not be read, so this run leaves the project layer and its local layer file as they are: fugaro: test tried to open the real gs:// bucket gs://fugaro-runs-proj-1234; use a bucket_url: file:// fixture or override openRecordBucket","after":"","undone":true},
  {"type":"stage","name":"checkout","state":"skipped","detail":"a development build has no release to refresh to","after":"","undone":false},
  {"type":"stage","name":"unit","state":"skipped","detail":"the login unit is written by a run you are watching at your own terminal: this one has nobody at it, so nothing is written (fugaro orchestrator install writes one by hand)","after":"","undone":false},
  {"type":"done","exit_code":0},
])

/** SETUP_APPLY_LINES is its second: the apply, which finished `done` with
 * `exit_code` 1 -- the steps left for the operator, which is a finished run and
 * not a failure (the contract's rule 2). */
const SETUP_APPLY_LINES: readonly JSONObject[] = Object.freeze([
  {"type":"step","stage":"preflight","name":"preflight","detail":""},
  {"type":"stage","name":"preflight","state":"done","detail":"","after":"","undone":false},
  {"type":"step","stage":"installation","name":"installation","detail":""},
  {"type":"left_for_you","stage":"installation","kind":"prompt","text":"in your own terminal window, not through a coding agent or a pipe, run the command below: it asks you nothing and the apply goes ahead","commands":["fugaro setup"]},
  {"type":"stage","name":"installation","state":"needs-you","detail":"a coding agent's session is present (CLAUDECODE is set), so nothing is applied: run this in your own terminal window, not through a coding agent","after":"","undone":false},
  {"type":"stage","name":"firebase","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"images","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"installation-2","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"secrets","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"step","stage":"plugin","name":"plugin","detail":""},
  {"type":"stage","name":"plugin","state":"skipped","detail":"machine-local: ran although installation waits for you; a development build has no release plugin to install","after":"","undone":false},
  {"type":"stage","name":"repository","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"rules","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"model-prices","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"policy","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"credentials-service","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"credentials-copy","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"workspace-project","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"workspace","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"credentials-route","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"layer","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"checkout","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"stage","name":"unit","state":"blocked","detail":"waits for installation","after":"","undone":false},
  {"type":"done","exit_code":1},
])

/** SETUP_REFUSED_LINES is its third: the one-line `error` feed a refused
 * `--check` prints. It is not an operation's answer -- the wizard's two are
 * above -- and it is here because the console's own reader is what has to make
 * sense of it: `new-ops.test.ts` reads this feed as a **failed** one. */
export const SETUP_REFUSED_LINES: readonly JSONObject[] = Object.freeze([
  {"type":"error","stage":"","error":"fugaro setup does not take --check: that is fugaro update's (it reports what a converge that has already run would do, and a first run has nothing to report: fugaro setup --check has no installation to read)","fix":"","exit_code":1},
])

/** WHOAMI_DOCUMENT is `user ls --me --json`'s answer, as written: the caller's
 * role, the scope it reaches and the repositories that scope covers. */
const WHOAMI_DOCUMENT: JSONObject = Object.freeze({
  "version": 1,
  "role": "operator",
  "scope": "repos",
  "repos": [
    "github/acme/*"
  ]
})

/** NEW_OP_ANSWERS is one answer per operation, keyed by the operation's own
 * name: the reply's `data` half, which is what the contract's decoder admits
 * (the CLI document under `document`, or the settings editor's outcome object
 * itself). */
export const NEW_OP_ANSWERS: Readonly<Record<NewOperationName, unknown>> = Object.freeze({
  'runs.stream': { document: { lines: RUNS_STREAM_LINES } },
  'spend.stream': { document: { lines: SPEND_STREAM_LINES } },
  'budget.stream': { document: { lines: BUDGET_STREAM_LINES } },
  whoami: { document: WHOAMI_DOCUMENT },
  // The installation's catalog. `digest` is a placeholder rather than a hash:
  // the CLI writes the last build's own digest, and no task recorded one.
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
  'setup.plan': { document: { lines: SETUP_PLAN_LINES } },
  'setup.apply': { document: { lines: SETUP_APPLY_LINES } },
  // The settings editor's outcome, the shape `settings.edit` answers: a key that
  // exists was opened, and the sentence says which one.
  'settings.editor.open': {
    ok: true,
    sentence: 'editing agent.models.coder for this project',
  },
})

/** NewOpAnswersBridge is the shape `newOpsFrom` reads: one method per
 * operation, each answering the console's own reply envelope. */
export type NewOpAnswersBridge = Readonly<Record<string, (payload: unknown) => Promise<unknown>>>

/** copy is a fixture as a value of its own, so a caller that edits what it was
 * handed cannot change what the next caller reads. The fixtures are plain JSON
 * by construction, so a round trip is exact. */
function copy<Value>(value: Value): Value {
  return JSON.parse(JSON.stringify(value)) as Value
}

/**
 * newOpAnswersBridge is a bridge that answers all nine operations from the
 * fixtures above, whatever payload it is handed: it validates nothing, because a
 * payload's validation is the allow-list's job in main and a test bridge that
 * checked one would be a second copy of a rule it does not own.
 *
 * It is a **consumer's** bridge and not the console's: the real ones are SRC's
 * handlers behind `CONSOLE_OPERATIONS` (I-OPS), reached through the preload.
 * What this is for is a unit test that draws a screen, a section or a gate from
 * answers it controls -- and it lives under `test/support` rather than in
 * `src/` so that no renderer module can reach it.
 */
export function newOpAnswersBridge(): NewOpAnswersBridge {
  const bridge: Record<string, (payload: unknown) => Promise<unknown>> = {}
  for (const name of NEW_OPERATION_NAMES) {
    bridge[NEW_OPERATION_METHODS[name]] = () =>
      Promise.resolve({ ok: true, data: copy(NEW_OP_ANSWERS[name]) })
  }
  return bridge
}
