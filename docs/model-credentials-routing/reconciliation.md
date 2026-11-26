# Model credentials routing: reconciliation

The spec ([spec.md](spec.md)) as amended against the code. **Where this document disagrees with
the spec, this document wins.** Checked against `origin/main` at `dfe066618` on 2026-11-21, and rebased onto `aa75a696c` the
same day; the symbols cited were not read again one by one at the new commit (O17). The
owner (Dimi Paun) answered D3170–D3197 on 2026-11-21 (D3194–D3197 after the pull request opened, as his answers
to open items O13–O16). Four answers were amended the same day: D3176 by the owner,
and D3174, D3179 and D3190 after review, each confirmed by him.

Modules, approved by the owner on 2026-11-21 (the capability map): `routing-table`, `pools`,
`chain-serving`, `pause-resume`, `visibility`, `migration`. Every finding, decision and task
names one.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `routing-table` | The project's table: a model pattern to an ordered chain of sources; its config, schema, validation and publication | none |
| `pools` | Subscription tokens only in pools; pool rules; ownership by a person, a service account or the project; designation; the launcher's pool | none |
| `chain-serving` | The credentials service walks the chain per step and per call; model equivalence and written downgrades; a paid fallback counted and recorded | `routing-table`, `pools` |
| `pause-resume` | Suspend when every source is exhausted, the paused state, the resumer, the maximum wait, hotfix fails fast | `chain-serving` |
| `visibility` | Console and mobile: the source of each step, the fallback flag, pool headroom, the paused notice | `chain-serving`, `pause-resume` |
| `migration` | `fugaro update` moves `agent.auth`, craft access, `routing.claude` and existing tokens into the table and pools; old fields are then rejected | `routing-table`, `pools` |

## Assumptions

- A1. "Project" is the Fugaro project (one installation, many repositories and people), and
  "project level" means the installation's config (`internal/localcfg`) published in the policy
  (`internal/projectpolicy`), never a repository's `fugaro.yaml`.
- A2. "Roles" in the spec are what the repository calls **crafts** (`coder`, `review`,
  `senior-review`, `security-review`, `merge-judge`, in `internal/config/craft.go`); "role" already
  means a person's permissions here (D1620).
- A3. "The LLM proxy" is the pair that exists: the in-job gateway (`internal/gateway`) and the
  credentials service (`internal/credsvc`). R2 says which of the two owns the table.
- A4. "A job" is a run, and "a step" is a stage of a run (`implement`, `fix`, `review`, ...).
- A5. "OAuth token" is a Claude subscription token: a credential of type `claude-oauth`.
- A6. "Firestore" is read as "the installation's live store" (F31, R6).
- A7. "The scheduler" names no existing component (F33, D3186).
- A8. "The credentials security spec and secrets addendum" are `docs/credentials-service/` and
  `docs/secrets-brokering/` with `SECURITY.md` and `.claude/rules/credentials-service.md`.
- A9. Git-provider tokens, workflow secrets and Bitbucket credentials are out of scope: the spec
  is about model credentials only.

## What the spec names, and what the repository already has

| Spec concept | The repository today |
| --- | --- |
| A repository is an "API key repo" or an "OAuth token repo" | `config.Agent.Auth` (`vertex`, `api-key`, `oauth`, `cascade`; `config.AgentAuths`), already ruled retired by credentials-belong-to-models (D3060–D3073), 5 of its 38 tasks merged |
| Roles | Crafts (`config.Craft{Model, Access}`); recipes name step kinds and two mappable roles (`internal/recipe`) |
| The project maps roles to models | Five rungs (`craft.ModelRungs`); a repository's `fugaro.yaml` may also set a craft's model |
| Routing table by model pattern | None. Access is per craft (D3060). Patterns exist for provider routes only (`config.PatternMatches`) |
| Ordered chain of sources | A pool's `members`; the cascade's fixed tier order (`creds.Selector.Select`); `routing.claude.via` |
| Launcher's pool | None. `config.Pool.Owner` and `AllowedUsers` (D1936), and a verified launcher on a slip (`creds.Slip.Owner`) |
| Shared pool | `config.Pool` with `LegacyOpen` or `Cascade`; project-shared secret containers `fugaro_p_<name>` (`internal/secretname`) |
| Anthropic API key, OpenRouter key (project) | Credential types `anthropic`, `openrouter`; secrets `anthropic-api-key` and `<provider>-api-key` |
| Vertex | Built-in credential `vertex`; one whole-run upstream on the job's identity (`gateway.Upstream.Kind`) |
| Pool rules | `Pool.Strategy`: `failover` (least recently exhausted) or `round-robin` (`config.PoolStrategies`) |
| A principal designates a pool | None |
| Service-account launcher | A launcher class (`credsvc.VerifyServiceAccountLauncher`, D3123), with no pool (D2976) |
| Per-call retry on the next source | Built in the service: `credsvc.Server.forward` and `retryPool`, marks in `internal/creds/marks.go` |
| Paid usage counted | `gateway.settle`: a subscription call is notional, a key call is money against the caps (D140) |
| "Fallback" flag | None. `runstore.StageTiming.Credential` and `runstore.Record.Switches` exist and no screen draws them |
| Pause until the reset | An in-job wait: `waitForCredential` in `internal/runner/creds_wait.go`, `budget.AgentEntry.WaitingUntil` (D138) |
| Suspend, exit, resume in a fresh job | Deferred by D138 as credentials-service task C3.1, unscheduled and unbuilt |
| Scheduler | Cloud Scheduler as fixed crons only; the resident orchestrator's `ActionReissue` |
| Migration of the repository setting | credentials-belong-to-models R3, R7, CM4, and its two-release removal (D3063, D3072) |

## Findings

### routing-table

- **F1 (Exists, Rule).** Retiring the repository's auth mode is already decided and partly built.
  `docs/credentials-belong-to-models` (D3060–D3073, all tasks approved 2026-11-20) retires
  `agent.auth`; CM1 (`craft.ByReach`), CM3 (`config.RetiredKeyNotes`), CM22, CM32
  (`config.ValidateCascadePools`) and CM35 are merged, and its contract C1 is the draft pull
  request #3460. `agent.auth` is still read everywhere on `main`. That plan routes by a craft's
  access, not by a model table. → D3170.
- **F2 (Rule).** D3060 says "a craft's access names the credential or pool", and its
  reconciliation declined "a per-model list of credentials" as "a second config surface beside
  crafts and pools". The spec is that per-model list. The spec is the owner's later word and
  supersedes D3060; what is lost is a different credential for two crafts on one model
  (D3068, per-stage accounts and the mix). → D3171.
- **F3 (Rule).** D3061, as the owner amended it on 2026-11-20: an unset access is "the first
  declared credential that reaches the craft's model, and nothing after it. Vertex is never
  implied", and "moving on from one credential to another needs a pool someone wrote". A chain
  written in the table is that written statement, so the two agree once nothing is implied. → R1.
- **F4 (Exists, Open).** The pattern grammar exists: an exact id or a prefix ending in `*`
  (`config.PatternMatches`), with any overlap refused (`config.PatternsOverlap`,
  `providerOverlapProblems`). The spec's first open question is the precedence. → D3172.
- **F5 (Rule).** A Claude id may never be claimed by a pattern in a provider's `models`
  (`config.CheckClaudeClaim`; D1a of `docs/mixed-credentials`), and `routing.claude.models` is an
  explicit list. The spec's own example is `claude-*`. D1a is about which provider claims a
  model; it stays for a provider's `models`, and the table is a different list. → D3173, R1.
- **F6 (Exists).** Two settings already say where a model's calls go: `routing.claude.via`
  (`direct`, `openrouter`, `auto`, `cascade`; `config.RoutingClaudeFor`) and each provider's
  `models`. → D3173.
- **F7 (Gap).** "The project maps roles to models" is not project-only today: a repository's
  `fugaro.yaml` sets `agent.crafts.<craft>.model`, `agent.models` and `agent.model`. The spec
  says this layer "already exists and stays", so the repository keeps its model choice, and the
  table then decides the source from the model whoever chose it. The repository still cannot
  choose spend: it can only pick a model the project's table and `budget.allowed_models` allow.
  **Amended 2026-11-23 (D3237, lift L8 of the repo-coupling audit): the repository does not keep
  its model choice.** D3237 reverses that half of this finding: the project layer
  (`defaults.agent.model`, `defaults.agent.models.*`, `defaults.agent.crafts.*.model`) chooses each
  role's model, and the routing table still decides which source serves it, exactly as A1 reads
  "the routing table decides". `agent.model`, `agent.models.*` and `agent.crafts.*.model` — and,
  by D3291, `agent.levels.lN.model` — move to the project: for one release a repository's value is
  read, dropped and named, and the next release refuses it, with `fugaro models use --layer
  project` as the fix (D3237 as the spec of [repo-override-rulings](../repo-override-rulings/spec.md)
  §5.1 applies it, its F6 and R10 name this amendment, task A1 writes it). The repository still
  cannot choose spend, and `budget.allowed_models` still narrows per repository; `--model` still
  overrides. The sentences above are kept as what was found on 2026-11-21.
- **F8 (Gap).** A model no entry matches. → R1 (refused with the fix).
- **F9 (Gap).** "No repo-level overrides" meets two things that vary by repository inside the
  project's config: the per-repository crafts block credentials-belong-to-models planned (CM2),
  which D3170 withdraws, and per-repository admission of an account (`ClaudeAccount.AllowDataTo`,
  `projectpolicy.Account.AllowRepos`), which D3065 made the consent. → D3182.

### pools

- **F10 (Wrong).** "Raw standalone tokens are not supported" contradicts three things on `main`
  and in an approved plan: a craft's access may name one `claude-oauth` credential (D3060),
  `fugaro run --token <account>` stays (D3069), and the bundle's default account
  (`config.DefaultClaudeAccount`) is a lone token. → D3176.
- **F11 (Rule).** A pool of two or more subscription tokens is inert unless
  `subscription_spread` is on (`localcfg`, `projectpolicy.Policy.SubscriptionSpread`; D1622,
  D1636), except the pool marked as the cascade's (D3064). The spec's rotation needs it gone or
  on. → D3177.
- **F12 (Gap).** "Fixed order" does not exist: `failover` picks the least recently exhausted
  member (`leastRecentlyExhausted` in `internal/creds/select.go`), and the listed order only
  breaks ties; across kinds the tier (subscription, then key, then provider key) outranks any
  order. → R4, R5.
- **F13 (Gap).** "Moving off a token at 80% of its limit" has no data source. The service reads
  only the reset instant, and only after a limit is hit (`usageLimitReset` in
  `internal/credsvc/forward.go`); nothing counts use per token (D142 deferred it). → D3178, U1.
- **F14 (Exists, Gap).** A pool has an owner and allowed users (`config.Pool.Owner`,
  `AllowedUsers`, `Pool.Serves`), and a slip binds a run to its verified launcher
  (`creds.Slip.Owner`, the Google subject). No pool is "the launcher's": a chain cannot name it,
  and nobody designates one. → D3179, R7.
- **F15 (Exists, Rule).** A person's own token is planned twice and served by neither: the
  project's per-user containers (`fugaro_u_<sub>_<name>`, built, and
  `docs/workspace-credentials/reconciliation.md` F4: "no model call can use a person's own token
  today"), and the workspace plan (0 of 18 built; D2960 stores it once at the workspace, D2962 and
  D2970 serve only its owner's runs). That plan's D3141 (a project credential shadows the personal
  token), D3142 (used automatically), D3144 (exhausted, move to the shared entries) and D2974 (a
  missing personal token is refused, never a fall-through) are each a fixed answer to what the
  chain would decide. → D3179 (amended after review: personal pools live in the project, so that
  plan is left as it is), D3180.
- **F16 (Rule).** D2976: "a run launched by a service account (the coordinator, the
  orchestrator) never holds a personal token". The spec gives such a run a pool of its own.
  → D3181.
- **F17 (Gap).** "People contribute deliberately" to the shared pool has no mechanism: a shared
  container is written by an admin (`fugaro secrets shared`). → R9.
- **F18 (Wrong).** "Convert existing tokens into per-user pools" cannot be done from what is
  recorded: a named account has no owner and serves any launcher of an admitted repository
  (`creds.Tier` calls it "someone's donated quota"). → D3183.
- **F19 (Exists).** "Admins see usage and headroom, never the tokens": names and states are
  shown and values never are (`fugaro accounts ls`, the console's
  `console/src/renderer/manage/kinds/people.ts`). No usage or headroom figure exists per token
  or pool. → D3188, D3178.

### chain-serving

- **F20 (Rule).** "The LLM proxy owns the routing table": the credential is chosen by the
  credentials service (`credsvc.Server.candidates`, `creds.Selector.Select`), and the in-job
  gateway holds no key on that path (D135, D143; `SECURITY.md` rests on it). A table owned by the
  in-job gateway would put selection beside the agent. → R2.
- **F21 (Exists).** Per-call transparent failover is built on the service path: a 429 or a 401
  marks the credential and the call is retried on the next unsent candidate, before any byte is
  streamed; the answer names the switch to the gateway only (`X-Fugaro-Switch`,
  `dropResponseHeaders`). A limit found inside a streamed answer marks the credential and does
  not retry (`usageLimitScanner`). The lost prompt cache is already priced
  (`runstore.Switch.CacheMissUSD`).
- **F22 (Wrong).** "Resolved fresh for each step" is not today's rule: the placement is sticky
  for the whole run (D154). → R16.
- **F23 (Gap).** Vertex cannot be a chain member as built: it is one whole-run upstream on the
  job's identity, the service is not called for it (D3066), it has no dollar cap, and it is
  refused under `budget.mode: enforce` (`VertexBudgetRefusal`, `config.PoolHasVertexMember`).
  The spec says a Vertex fallback "counts against project spending caps". A cap in the in-job
  gateway cannot bind on that path: the job's own account holds the Vertex permission
  (`roles/aiplatform.user` in `deploy/terraform/gcp/modules/workflow/sa.tf`), and the agent acts
  as that account, so it can call Vertex without the gateway (found by review round 1).
  → D3174, R2.
- **F24 (Rule).** A Claude model through OpenRouter in the service is refused until Check 50
  passes (D150, D3000, D3013). The spec's complex example names it. → R3.
- **F25 (Gap).** "The same model family and version" has no representation. A pool requires the
  identical model string from every member (D1637, `config.PoolMemberServesModel`); the only
  equivalence map is `claudeRoutes` in `internal/pricing/canonical.go` (canonical id to the
  OpenRouter-style route id: six rows, three of them mapped), with none for Vertex; and "the gateway never rewrites a
  request body": the runner rewrites pins once per run. A per-call move to another provider needs
  the `model` field rewritten per call. → D3175, U5.
- **F26 (Exists, Rule).** A paid fallback is already money against the caps (D140), and D152
  requires `credentials.fallback: auto` with `budget.mode: enforce` and a per-run cap before a
  subscription run may reach a key. `credentials.wait_threshold` (D153) decides wait-or-switch.
  A chain says the first in its own order. It does not say the second: a `fallback: auto`
  project waits up to 15 minutes for a reset before it pays, and a strict chain pays at once.
  → R4, R17, D3192.
- **F27 (Gap).** No "fallback" mark exists in the run record. `StageTiming.Credential` names one
  credential per stage; nothing says whether it was the chain's first source. → D3188.

### pause-resume

- **F28 (Exists, Rule).** A wait for the reset is built, inside the job: the service answers
  typed `exhausted_until` with the reset (`creds.ExhaustedUntil`), the gateway answers 503 and
  never holds the call, and the runner ends the stage, sleeps, sets `WaitingUntil` and resumes
  the same session (`waitForCredential`, at most `maxCredentialResumes` times; the gateway's
  answer is wired to `noteExhausted` in `internal/runner/gateway.go`). D138 chose this
  and deferred "resume after job death" as C3.1. The spec replaces the mechanism and un-defers
  C3.1; "never hold a proxy call open" agrees with D138. → D3184.
- **F29 (Wrong).** "A paused job costs nothing while waiting" is the opposite of today: the
  in-job wait spends `timeouts.total` and billed container time.
- **F30 (Gap).** There is no project maximum wait. The only bound is the job's own timeout, past
  which the run halts with `HaltNoCredential`. → D3187.
- **F31 (Wrong).** "Marks itself paused in Firestore": the installation's live state is the
  Realtime Database (`budget.AgentEntry` at `agents/<slug>/<run>`), and D136 chose it over
  Firestore for exactly this state; `internal/firestore` holds only the spend history. → R6.
- **F32 (Gap).** No paused status exists (`runstore.Status`: running, succeeded, failed,
  unreviewed, infra error, cancelled, halted). The set is closed in `schemas/result.schema.json`,
  the budget rules' outcome words (`outcomeStatuses`) and the console's
  `console/src/shared/schema.ts`; a finished execution writes one terminal outcome and its
  registry entry is deleted (`Session.Finish`). → D3185, R15.
- **F33 (Gap).** Nothing starts a job at a named time. Cloud Scheduler runs fixed crons; there
  is no Cloud Tasks. The resident orchestrator (opt-in) reissues a recorded attempt whose run
  never started (`ActionReissue`), ticking every minute. → D3186.
- **F34 (Gap).** The session is saved only at writeback or on a failed finalize
  (`saveSession`), and restored only for a follow-up whose saved head is an ancestor
  (`restoreSession`). A suspend must save it on the way out, and uncommitted work in the checkout
  is lost unless it is pushed first (`docs/checkpoint-pushes`).
- **F35 (Gap).** Hotfix is a rigor mode (`rigor.Hotfix`) nothing in the service or the gateway
  knows about. → R10.
- **F36 (Gap).** A paused run can span a `fugaro update`. The update must not rewrite what it
  holds, the resumed job runs the image of the day, and the saved state must still be readable
  by it (`CLAUDE.md`, "safe while runs are in flight").
- **F37 (Gap, trust).** A resumed job is launched by the orchestrator's service account but must
  draw on the original launcher's pool. If the suspended job's own record named that launcher, a
  hostile agent could name another person and spend their tokens. → R8.

### visibility

- **F38 (Open).** The spec's second open question. The data exists per stage
  (`StageTiming.Credential`, `ModelNotionalUSD`, `Record.Switches`) and the console draws the
  notional and paid split only. → D3188.
- **F39 (Wrong).** "Notifies in the console and on mobile": the console polls
  `fugaro watch --json` and already carries `waiting_until`; mobile push is designed and not
  built (`docs/mobile/design/push-notifications.md`), and D141 recorded that no alert channel
  exists. The orchestrator's notice kinds (`NotifyRunFinished`, ...) have only a log sink. → R13.

### migration

- **F40 (Exists).** The shape of the migration is settled and stays: `fugaro update` does it in
  place and asks nothing; the retired key is read and named for one release and rejected in the
  next, behind the image-age gate (`checkImageSince`), with no `fugaro.yaml` version bump (D3063,
  D3072). → R14.
- **F41 (Gap).** One table cannot reproduce two repositories that differ today (one `oauth`, one
  `api-key`, same model): a generated chain "pool, then key" would send the first to a paid key
  it never used. Per-repository admission of every source kind keeps each where it was. → D3182.
- **F42 (Gap).** The carriers of the auth mode outside the config: the record
  `creds/repoauth/<slug>` (`creds.RepoAuth`), the headers `X-Fugaro-Auth-Mode` and
  `X-Fugaro-Auth-Account`, `budget.AgentEntry.Auth`, and `runs[].auth` in `watch --json`, which
  the console's parser requires. Terraform never sees the value itself; it sees
  `model_secrets_served` (`infra.ModelSecretsServed`), whose three values are defined by auth
  mode, and the flip list `config.ServiceServedAuths`, which the rules file changes only on the
  owner's go. Each is a format with outside readers.
- **F43 (Rule).** `credentials.fallback`, `credentials.wait_threshold` and
  `subscription_spread` are published policy fields. Removing one is a policy change `update`
  migrates in place, never "delete and publish again".

### Found by review round 1

- **F46 (Exists, migration).** A grade is a second place a repository names a credential:
  `model_grades.<grade>.access` (the `grade` command in `internal/cli/accounts_craft.go`). It,
  `agent.auth`, craft access and `agent.oauth_token` are already retired rows in
  `internal/config/scope.go` (credentials-belong-to-models CM3); their notes send the reader to a
  craft's access and must now name the table. → MG1, MG4, MG5, X2.
- **F47 (Gap, chain-serving).** A run calls models that are not a craft's: the rungs
  (`craft.ModelRungs`) also choose by the task's grade, a level's model, a pinned role of
  `agent.models` (coder, reviewer, background, vision; `internal/config/pins.go`) and
  `agent.model`, and `--model` overrides them. Shared project jobs
  (`internal/runner/projectjob.go`), the review job (`internal/credsvc/review.go`) and the
  diagnose agent (`internal/diagnoseagent/creds.go`) also reach a model by account or auth mode.
  Each needs an entry and the same path. → R22, RT3, CS10, CS13.
- **F48 (Gap, migration).** A named account derives a job, an account and a secret of its own
  (`WorkflowVariant` in `internal/backend/gcp/names.go`). With `--token` gone they are orphans
  `fugaro update` must retire, named in its in-flight line. → X6.
- **F49 (Rule, routing-table).** The policy's blocks decode strictly: a new member inside
  `credentials` or a pool makes a release-behind reader refuse the whole policy while runs are
  in flight; only an unknown top-level key is tolerated (the comment on the catalog entry members
  in `internal/projectpolicy/policy.go`). → R21, R11.
- **F50 (Rule, all).** `website/content/cli-reference.md` is pinned to every command and flag
  (`TestCliReferenceNamesEveryCommandAndFlag`), so every task that adds one writes that file and
  the plan orders them.
- **F51 (Rule, pools).** A project database admin can forge a project slip (D1943 of
  secrets-brokering's dynamic delivery), which is why workspace-credentials D2970 gave the
  workspace its own grant route. With personal pools in the project (D3179 as amended), a
  person's pool rests on the project slip, as every per-user secret of a project does today. That
  is an accepted risk of the project, said in `SECURITY.md` (DOC1). → R7, R8, R23.
- **F52 (Rule, pause-resume).** Launcher-does-not-mint D3136 lets the issuer serve the
  orchestrator only an attempt on its own record. A resume derived from a record a job wrote
  would widen that. → D3189, R19.
- **F53 (Gap, pause-resume).** Nothing bounds how often a chain pauses: each resume has a fresh
  `timeouts.total` and a subscription call is notional. → D3190.
- **F54 (Gap, visibility).** The console's status set is closed; what a release-behind console
  does with `paused` is not known. → O8, PR7.

### Found by review round 2

- **F55 (Rule, pools).** A chain cannot span two services. The workspace plan fixes one model
  route per run (workspace-service D2989; workspace-credentials D3141, "it follows the route"),
  its service refuses a project slip, and the project's service cannot read a workspace value.
  "Launcher's pool, then shared pool, then key" with the first at the workspace and the rest in
  the project has no design. → D3179 (amended).
- **F56 (Rule, chain-serving).** A call an agent builds itself and posts straight to the service
  is served under the policy's rules and is not counted against the run's cap: the service does
  not price, reserve or settle (`SECURITY.md`, D3003). So "the service calls Vertex" caps Vertex
  exactly as far as a key is capped, and while a job's account still holds the Vertex permission
  the direct route of F23 stays open. → D3174 (amended again), R23, CS11, X8.
- **F57 (Rule, chain-serving).** A release-behind gateway decodes the service's refusal strictly
  (`DisallowUnknownFields` in `internal/gateway/service.go`) and strips response headers by an
  explicit list (`dropResponseHeaders`). `fugaro update` deploys the service at once and job
  images lag, so a new member would halt an older run and a new header would reach its agent.
  → R24, CS1.
- **F58 (Wrong, chain-serving).** The spec's `gemini-* → Vertex` does not run on what exists:
  the Vertex path speaks Anthropic's publisher only, with the model in the URL path
  (`internal/gateway/vertex.go`), and the spelling map holds Claude ids only. → D3193, CS4, GV1
  to GV4.
- **F59 (Rule, pause-resume).** A resume is launched by the orchestrator's service account and
  must draw on a person's pool, against D2976 as written. → D3191.

### Found by review round 3

- **F60 (Wrong, pools).** Not every run has a slip, and no slip names a service account: the
  grant admits the person class only (`VerifyLauncher` in `internal/credsvc/slip.go`;
  `VerifyServiceAccountLauncher` is called only from `internal/credsvc/identity_callers.go`), and
  `fugaro run` starts the run without a slip when the grant fails. → CS14 admits the other
  classes; CS1 and PL4 say what a run with no slip is served.
- **F61 (Rule, pools).** The database rules cannot hold a person to their own designation:
  people reach the database by IAM, which bypasses rules, and every launcher holds the viewer
  role. Only a route of the service keyed on the verified subject can; what those nodes hold is
  readable by every launcher and is display data. → PL4, PL9, PL10, PR3.
- **F62 (Rule, pools).** The intake refuses a member's write under a shared container
  (`internal/intake/handlers.go`; dynamic-delivery D1935). Contributing one's own token to a
  shared pool (R9) reverses that by one case. → D3194, PL5.
- **F63 (Rule, migration).** A policy whose version a reader does not know is refused whole
  (`internal/projectpolicy/policy.go`), so a version bump in release 2 would stop release-1
  images and a paused run resuming on one. → R25.
- **F64 (Gap, pause-resume).** No guard counts a paused chain as in flight: the pull request
  would read stalled and a duplicate or follow-up launch would be admitted between a pause and
  its resume. → D3195, PR12, PR13.
- **F65 (Gap, migration).** Some of today's configurations migrate to a table the checks refuse:
  `routing.claude.via: openrouter` or `auto` (allowed today with a doctor warning) becomes a
  chain R3 refuses until Check 50, and a Vertex model has no checked-in spelling until Check 882.
  → D3196: MG1 and MG2 leave such an entry on the release-1 bridge.
- **F66 (Rule, pause-resume).** The service has no admin surface (D143), so it cannot tell a
  project admin from anyone else. → D3197: PR10 serves the launcher only.
- **F67 (Gap, pause-resume).** The gateway decodes the service's refusal strictly as error,
  message, reset time and tried list, so the new action and resume time need the gateway's and
  the wire's files. → PR2, PR3.

### Completeness of the spec

- **F44 (Open).** No success criteria are stated as tests. The plan's acceptance lines stand in
  for them, and the owner-run checks (880–886) prove the live behaviour.
- **F45 (Exists).** Commands, folders, style and boundaries are the repository's own
  (`CLAUDE.md`, `.claude/rules/credentials-service.md`, `.claude/rules/terraform.md`,
  `.claude/rules/console.md`, `.claude/rules/mobile.md`).

## Decisions

All answered by the owner on 2026-11-21; D3198 on 2026-11-26.

| # | Module | Question | Recommendation | Answer |
| --- | --- | --- | --- | --- |
| D3170 | all | How this spec relates to credentials-belong-to-models, in flight | Supersede craft access now: stop its not-started tasks that write or read craft access, re-cut them around the table, one migration for users | **As recommended.** One migration; C1 (#3460) is re-cut |
| D3171 | routing-table | Keep a different credential for two crafts on one model (D3068)? | Drop it: one chain per model | **As recommended.** Per-stage accounts and the mix are retired; `update` folds them into one pool |
| D3172 | routing-table | Precedence when several patterns match (the spec's open question) | The existing grammar; the most specific wins (exact beats prefix, longer prefix beats shorter); identical patterns refused | **As recommended** |
| D3173 | routing-table | What becomes of `routing.claude.via` and a provider's `models` | The table replaces `via`; a provider's `models` stays as what it can serve | **As recommended** |
| D3174 | chain-serving | A chain naming Vertex on an `enforce` project | Cap Vertex in this plan, so it is an ordinary paid source | **As recommended** when first asked. **Amended after review round 1, confirmed by the owner:** the gateway cannot cap Vertex on the job's identity (F23), so the credentials service calls Vertex on its own identity, as it serves a key. Amends D3066. **Amended after review round 2, confirmed by the owner:** Vertex is then capped as far as a key is and no further (F56); a hand-built call straight to the service is the same accepted risk as for a key (D3003), and a job's account loses its Vertex permission in release 1 wherever no run in flight can hold it (an `enforce` project), in release 2 elsewhere |
| D3175 | chain-serving | May the service rewrite the `model` field when a call moves down the chain? | Yes, from a checked-in map extended to Vertex; an unmapped source is refused at validation; a written downgrade is the same rewrite | **As recommended** |
| D3176 | pools | `fugaro run --token` (D3069) | Retire it; `update` wraps each lone account in a pool of one | **As recommended** when first asked: retire it. **Amended by the owner, 2026-11-21**, in his words: "Maybe --token should become --pool, wdyt?" So: `--token` is rejected with a fix naming `--pool`, and `fugaro run --pool <name>` makes "the launcher's pool" resolve, for that run, to the named pool the launcher owns or is allowed on. The chain is unchanged; the flag is refused where no chain of the run's models names the launcher's pool; the service checks the entitlement; a resumed run keeps the pool its launch named. Reverses D3069 |
| D3177 | pools | The `subscription_spread` switch | Retire it | **As recommended.** The owner's to confirm as to the provider's terms; answered. Reverses D1622 and D1636 |
| D3178 | pools | The usage-threshold rule | Behind an owner-run check; round robin and fixed order ship now | **As recommended** |
| D3179 | pools | Where personal pools live | On workspace-credentials; its D3141, D3142, D3144 and D2974 amended by a documents task | **As recommended** when first asked: at the workspace. **Amended after review round 2, by the owner's answer to the question it raised: in the project.** A chain cannot span two services (F55), so a personal pool uses the project's per-user containers and one service walks the whole chain. Cost, as told to him: a person stores tokens once per project, and a project database admin can forge a grant, as for any project secret today. Workspace-credentials is left unchanged |
| D3180 | pools | The launcher has designated no pool | Skip to the next source, flagged as a fallback; a chain of only the launcher's pool is refused with the fix | **As recommended.** (First recorded as amending D2974; with D3179 amended it touches no decision of the workspace plan) |
| D3181 | pools | A service account's pool | A project pool owned by that principal, filled by an admin; a resumed or reissued run keeps its original launcher's pool | **As recommended.** D2976 stands, but for the one case D3191 adds |
| D3182 | routing-table | Per-repository admission | Keep it, written in the project, for every source kind | **As recommended.** Nothing moves on upgrade |
| D3183 | migration | Whose pools existing accounts become | The shared pool, same members and order; each person moves their own token later | **As recommended.** Not per-user on day one |
| D3184 | pause-resume | Keep a short in-job wait? | Always suspend | **Not the recommendation: wait in the job under a floor**; anything longer suspends (R11) |
| D3185 | pause-resume | What the resumed job is | A new run in the same chain; the suspended run ends `paused` | **As recommended** |
| D3186 | pause-resume | Who starts the fresh job | The resident orchestrator; a project without it fails fast with the fix | **As recommended** |
| D3187 | pause-resume | The default maximum wait | 6 hours | **As recommended** |
| D3188 | visibility | What the console shows (the spec's open question) | The source per step, a fallback badge, the switch log with reason and cache cost, the paid amount spent on fallback, and a pools screen | **As recommended** |
| D3189 | pause-resume | May the issuer serve the orchestrator a resume, which is not an attempt on its own record (D3136)? | Yes, for a pause the credentials service itself recorded | **As recommended** (asked after review round 1). Widens D3136 by that one case |
| D3190 | pause-resume | What bounds repeated pauses of one chain | Three pauses per chain | **Not the recommendation: total paused time only.** A chain's paused time may not pass `pause.max_pause`; there is no count. **Amended after review round 2, by the owner's answer:** a pause is never shorter than `pause.min_pause` (30 minutes by default), so at most 12 resumes in the default 6 hours |
| D3191 | pause-resume | A resume is launched by a service account but must draw on a person's pool (D2976) | A resume is still the person's run | **As recommended.** D2976 is amended by that one case |
| D3192 | migration | A `fallback: auto` project waits up to 15 minutes before paying; a strict chain pays at once | Pay at once; `update` says so | **As recommended** |
| D3193 | chain-serving | Non-Claude models on Vertex (the spec's `gemini-*` example) | Out of this plan | **Not the recommendation: add Gemini on Vertex.** The service translates to and from Vertex's Google-publisher wire (GV1 to GV4) |
| D3194 | pools | May a member write their own token under a shared container through the intake (O13; reverses D1935 by one case)? | Yes, only into a pool an admin marked as taking contributions, as a member in the contributor's own name | **As recommended** |
| D3195 | pause-resume | The word for a pull request whose chain is paused (O14) | Live | **Not the recommendation: a fifth word, `paused`.** A new pull-request state beside live, ready, stalled and orphan. My reading, not his words: the chain still counts as in flight to the guards, so a duplicate launch or a follow-up is refused while the pause stands (F64) |
| D3196 | migration | What `update` does with a configuration that migrates to a table the checks refuse (O15) | Leave that entry on the release-1 bridge, name it, converge | **As recommended** |
| D3197 | pause-resume | May a project admin cancel another person's paused run (O16)? | Not in this plan | **As recommended.** Only the launcher cancels; D143 stands |
| D3198 | pools | Does `fugaro accounts add` put a new subscription token in a pool of one named after the account (PL2)? | No: the command writes a lone account, and a pool is made with `fugaro accounts pool add` | **The owner's ruling of 2026-11-26** (run 4d2e's review decision): `accounts add` writes a lone account, and `--pool` names a pool the config already defines, which the token joins. Retires PL2's first clause. The shipped command still writes `pools.<name>` for a new `--type claude-oauth` account where no `--pool` is given, so PL2's rework owes the change |

D3199 is reserved and unused.

## Resolutions

One defensible answer each; stated, not asked.

- **R1 (routing-table).** A chain is what someone wrote and nothing more. A model no entry
  matches is refused with the fix; Vertex is reached only where a chain names it; nothing is
  implied (D3061's intent, kept). A `claude-*` entry is allowed: D1a still governs a provider's
  `models` and is not about the table.
- **R2 (chain-serving).** The credentials service owns the table, walks the chain and makes
  every call that needs a credential, Vertex included (D3174 as amended). The in-job gateway
  holds no key and no table; it sends the run's stage, and the service reads the model from the
  request's body. This is what "the LLM proxy owns the routing table" means here (D135 and D143
  stand; D3066 is amended).
- **R3 (chain-serving).** A chain that reaches a Claude model through OpenRouter is refused at
  validation until Check 50 has passed (D3013 stands).
- **R4 (chain-serving).** The chain's order is strict and replaces the fixed tier order.
  `credentials.fallback` and `credentials.wait_threshold` are retired: `update` writes
  `fallback: auto` as the key in the chain and `oauth-only` as no key in the chain.
- **R5 (pools).** Pool rules by name: `round-robin` stays; `ordered` is new and is the spec's
  fixed order (the first listed member that is usable); `failover` keeps its meaning (least
  recently exhausted), since a value never changes meaning. The threshold is a separate optional
  number on the pool (D3178).
- **R6 (pause-resume).** The paused mark lives in the Realtime Database, with the durable record
  in the runs bucket. Not Firestore (D136 stands).
- **R7 (pools).** "The launcher" is the verified principal on the run's slip (a person's Google
  subject, or a verified service account) and never display text (`requested_by`,
  `launched_by`). A personal pool is served on that slip, as a per-user secret is today (F51).
- **R8 (pause-resume).** The launcher of a paused run, and the pool its launch named, are the
  service's own record of the run's grant, and the resumed run is bound to them (D3191). The
  job's word about who launched it is never read.
- **R9 (pools).** Contributing a token to the shared pool is the token holder's own command,
  from their own session. Nobody contributes another person's token; an admin can remove one.
- **R10 (pause-resume).** A hotfix run never pauses and never waits: when every source is
  exhausted it halts with `HaltNoCredential`. This one rule is the runner's, which knows the
  mode; every other choice between waiting, pausing and halting is the service's (R19).
- **R11 (pause-resume).** Three new project settings under one new top-level policy key, `pause`
  (F49): `pause.wait_in_job` (default 5 minutes; a reset nearer than this is waited out in the
  job, D3184), `pause.min_pause` (default 30 minutes; a pause is never shorter, D3190) and
  `pause.max_pause` (default 6 hours, D3187, which also bounds a chain's total paused time). A
  reset beyond `max_pause` halts at once.
- **R12 (pause-resume).** Paused time counts against no timeout: each execution has its own
  `timeouts.total`. The budget carries along the chain as it does today (D1045–D1047), and the
  suspended execution releases its lease on exit.
- **R13 (visibility).** A paused run is visible wherever a run is: `fugaro watch`, `runs ls`,
  the console and the mobile feed. A push notification is one new notice kind in
  `internal/orchestrator/notify.go` that the mobile push plan delivers when it lands. This plan
  builds no channel (D141 stands).
- **R14 (migration).** `fugaro update` is the whole migration: in place, asking nothing, safe
  with runs in flight, with a test that an installation at the previous version converges.
  Retired keys are read and named for one release, then rejected behind the image-age gate
  (D3063 and D3072 stand).
- **R15 (pause-resume).** `paused` is a new status word. `halted` and `running` are not reused.
  The result schema, the budget rules and the console's set gain it, and `update` migrates the
  rules.
- **R16 (chain-serving).** A placement is sticky for one step, not for the run (amends D154). At
  each step the chain is walked again from its first source.
- **R17 (chain-serving).** A chain that names a paid source after a pool needs
  `budget.mode: enforce` and a per-run cap, as D152 required of `fallback: auto`.
- **R18 (pause-resume).** A suspend saves the session and pushes the work in progress as a
  checkpoint before it exits (F34). A resume whose saved state the current image cannot read
  ends with a named outcome and never guesses (F36).

- **R19 (pause-resume).** The service, not the job, decides between waiting, pausing and
  halting, computes the resume time from its own knowledge of the reset, and is the only writer
  of the paused node. The job writes only what the run that resumes it reads (F52, D3189).
- **R20 (routing-table).** A subscription account keeps its own per-repository admission
  (`allow_data_to`); the table's `admit:` covers a key, a provider key and Vertex. A pool member
  a repository is not admitted to is skipped for it, so two repositories admitted to two
  different accounts can share one pool and neither moves.
- **R21 (routing-table, pools).** Nothing published changes shape under a release-behind
  reader: the table, the admissions and the pools in their new shape travel under one new
  top-level policy key; a pool's principal and rule are new members, and `owner` keeps its
  meaning (F49).
- **R22 (chain-serving).** "A model a run can call" means every model the rungs can choose and a
  `--model` override (F47). Each needs an entry; a run or a project job whose model has none is
  refused at launch, not mid-run.
- **R23 (chain-serving).** Two residual risks are accepted and said in `SECURITY.md`, not hidden
  behind the word "capped": a hand-built call straight to the service is served under the
  policy's rules and escapes the run's cap, for Vertex as for a key (D3003, F56); and a project
  database admin can forge a slip, a designation or a paused node, as for any project state
  (D1943, F51).
- **R24 (chain-serving).** The service sends a new answer member or header only to a caller that
  sent the stage header, so a release-behind gateway meets exactly today's answer (F57).
- **R25 (migration).** Release 2 empties the retired policy members and keeps them decodable,
  so a release-1 image still reads the policy (F63). Taking them out of the format, which bumps
  its version, is a later release's, with a gate of its own.
- **R26 (pools, pause-resume).** A designation, a personal pool's membership and a pause are
  written only through routes of the service keyed on a verified principal; the nodes are
  readable by every launcher and are never an authorization input to anything but the service
  and the orchestrator (F61). `pause.json` is written inside the job, like the run record a
  follow-up already trusts; that trust is unchanged.

## What this changes in other plans

- **credentials-belong-to-models** (D3170). Kept: what is merged (CM1, CM3, CM22, CM32, CM35),
  the two-release removal of `agent.auth` and its rule for an unmigrated repository (R3 there).
  Withdrawn: every not-started task that writes or reads a craft's access, and contract C1 as
  drafted. Reversed or amended: D3060 (access per craft), D3068 (per-stage accounts), D3069
  (`--token`, now a narrow `--pool`), D3064 (the cascade pool becomes the shared pool and the generated chain). The
  task-by-task table is in the plan; its first task amends that plan's documents.
- **workspace-credentials.** Unchanged (D3179 as amended). Its D2976 is amended by one case in
  a project: a run that resumes a person's paused run draws on that person's pool (D3191). A
  documents task of this plan (A2) notes it there and in secrets-brokering's dynamic delivery.
- **workspace-service.** D2989 (no fall-through between services) stands.
- **keys-through-service.** Serving every key from the service is what the chain needs and is
  unchanged. Its Check 50 gates OpenRouter for Claude (R3).
- **credentials-service.** D138 is replaced for waits beyond the floor (D3184); C3.1 is
  un-deferred as this plan's `pause-resume`; D152 and D153 are re-keyed (R4, R17); D154 is
  amended (R16); D136 stands.
- **model-crafts.** D1622 and D1636 are reversed (D3177); D1637 is amended (D3175).
- **always-a-budget.** Its Vertex refusal under `enforce` is lifted for a chain the service
  serves (D3174); an unmigrated `auth: vertex` repository is still refused.
- **launcher-does-not-mint.** D3136 is widened by one case (D3189).

## Open tensions

None. No two answers pull against each other.

## Open items

- **O1.** The owner stopped KS6, KS9 and KS10 by hand when the last plan superseded them. The
  credentials-belong-to-models tasks D3170 withdraws must be stopped the same way before any of
  them launches; the plan names them.
- **O2.** D3177 extends what the owner confirmed of the provider's terms on 2026-11-20 to
  rotation within a pool, including a shared pool of several people's tokens. Recorded as his
  answer; restated here because it is a legal reading and his alone.
- **O3.** Check 50 has not run; until it does, no chain reaches Claude through OpenRouter (R3).
- **O4.** D3184 was answered against the recommendation; the floor's default (5 minutes, R11)
  was named in the question and not asked on its own.
- **O5.** `pause.wait_in_job`, `pause.min_pause` and `pause.max_pause` are names this document
  proposes.
- **O6.** A project without the resident orchestrator never pauses (D3186). Whether the
  orchestrator should become part of every installation is a separate question, not asked here.

- **O7.** `config.ServiceServedAuths` is a flip list the rules file changes only on the owner's
  go. CS11 leaves it; retiring it with `agent.auth` in release 2 needs that go.
- **O8.** What a release-behind console does with the status `paused` (F54). PR7 tests it and,
  if that parser refuses the word, bumps the version of the formats that carry a status.
- **O9.** D3190 was answered against the recommendation: there is no count of pauses. With the
  minimum pause he then added, one chain resumes at most 12 times in the default 6 hours.
- **O10.** Withdrawn: personal pools no longer rest on the workspace plan's store (D3179).
- **O11.** D3193 was answered against the recommendation. Whether the agent's harness works
  acceptably on a Gemini model through a translated wire is not known (U8); GV4's end-to-end test
  and Check 886 are what will say.
- **O12.** Closed: review round 3 ran.
- **O13.** Closed by D3194.
- **O14.** Closed by D3195. Open within it: whether a paused pull request should still refuse a
  follow-up, which the plan assumes and the owner was not asked in those words.
- **O15.** Closed by D3196.
- **O16.** Closed by D3197.
- **O17.** Review round 3's fixes are the last commit and were not reviewed again. The branch
  was rebased from `dfe066618` onto `aa75a696c`, where the credentials service's Terraform and
  `SECURITY.md` had changed; the symbols cited were not read again at the new commit.

## UNVERIFIED

- **U1.** Whether the provider's responses on a subscription token carry a utilisation figure a
  threshold rule could read. Settled by Check 880, owner-run.
- **U3.** What `localcfg`'s `Me` returns under a service account. R7 does not depend on it.
- **U4.** How the issuer of launcher-does-not-mint recognises a node the credentials service
  wrote (D3189). That plan's I1 is not merged; PR4 is gated on it and settles it there.
- **U5.** The Vertex spelling of each Claude model in the canonical map. Settled by the owner's
  run of Check 882 against the project's Vertex.
- **U6.** The census of `agent.auth` readers (67 files in 14 packages, F1 of
  credentials-belong-to-models) was not re-read symbol by symbol at `dfe066618`.
- **U7.** Whether a limit found inside a streamed answer can be retried at all. Today it is not
  (F21); the plan keeps that and the step's next call moves on.
- **U8.** Vertex's Google-publisher request and answer for a Gemini model, and how faithfully
  tool calls and streaming map to what the agent's harness expects. Not in this repository.
  Settled by the provider's current documentation, which GV1 and GV2 cite field by field, and by
  the owner's run of Check 886.
