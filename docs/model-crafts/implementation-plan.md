# Model crafts and credentials: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (D1620–D1638, F1–F26). **2026-11-10 (F26, D1638): the API-key credential type's runner and service halves are `docs/api-key-review`'s AK6a (merged), AK6b (in review) and AK7 (not started); MC5 and MC6 EXTEND what they built and never replace it, and both are held until AK6b and AK7 have merged.** That folder's AK9 and AK10 — written and unapproved — are folded into MC6 (its README and plan record it). **Nothing is implemented. No task is approved: each needs the owner's explicit go; answering a decision is not a go.** Owner answers that shaped it (2026-11-07): the word *craft* (D1620); generalise in place and migrate in `update` (D1621); pools ship, a pool of two or more subscription credentials stays off until the D875 switch (D1622); OpenRouter, Anthropic key, Vertex and Claude OAuth now, OpenAI key and Codex OAuth a documented "not yet" (D1623). D1624–D1633 were answered 2026-11-07 as the recommendations (owner delegated reading to the PR); tasks that name one are no longer blocked by it.

External dependencies (other plans, not built here): composite-commands (#1907: the step registry; a credential or pool change runs publish and update steps), roles-scopes (#1908: who may change), no-prompt (#1906: value entry, owner-only cap raise), diagnose (#1909: credential order).

## Conventions

`CLAUDE.md`; `.claude/rules/terraform.md` (cover tables are not widened to pass a plan); `.claude/rules/credentials-service.md` and `SECURITY.md` for anything touching `internal/creds`, `internal/credsvc` and the gateway (security-sensitive: independent review before merging); hermetic tests (`internal/gcpfake`, `t.TempDir()`); no secret-shaped literal in tests, docs or fixtures; config and wire formats decode strictly and have JSON Schemas in `schemas/` (new keys get a schema and a `testdata/` corpus); **a field never changes meaning** (new names, old keys migrated and removed, no aliases: pre-1.0); docs are tested (`go test ./internal/cli -run Docs`); plugin skills carry byte budgets and the lint checks every command and flag named; `### For operators` is "run `fugaro update`" plus what it asks (HARD RULE). No agent runs a live cloud action; live checks are owner-run scripts in `scripts/live-checks/`, one idempotent script with `--dry-run` and PASS/FAIL, refusing the `belong` project. In-run level 3 for security tasks is asked with `fugaro run --review-levels` (never a local second review).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A repository can never introduce a credential or loosen a pool (F12, D1628) | MC3 `TestRepoCannotAddCredential`, `TestRepoCanOnlyNarrowPool` |
| A secret value is never in config, a log, the record or `craft ls` output | MC2/MC7 redaction test; `craft ls --json` golden has no value |
| A pool of two or more OAuth subscription credentials is inert while the D875 switch is off (D1622) | MC5 `TestSubscriptionPoolInertWithoutSwitch` |
| OpenAI key / Codex OAuth refuse clearly (D1623) | MC2 `TestNotYetTypesRefuse` |
| Every pool member is checked against `allow_data_to` for the repository (F10, D1627) | MC5/MC6 |
| A vertex member refuses under `budget.mode: enforce` (F8) | MC5 `TestVertexPoolRefusedUnderEnforce` |
| The migration is derived from each installation's own keys; a run before and after resolves the same model and credential (F17) | MC4 converge test |
| `stage_accounts` keeps its meaning; `craft_credentials` is a new additive field (D1629) | MC8 |
| No craft key turns level 3 on (F14) | MC1 `TestSecurityCraftDoesNotEnableLevel3` |
| A job receives only the secrets of the crafts it runs; a review-class credential never lands in a coder job; cross-secret failover only at a stage boundary or in credsvc (F20, D1635) | MC5 `TestJobEnvHoldsOnlyItsCraftsSecrets`, MC6 `TestReviewCraftPoolOnlyReviewClass` |
| A pool is admitted only if every member serves the model ID as written (F23) | MC5 `TestPoolRefusedWhenAMemberDoesNotServeTheModel` |
| Subscription spread is off by default, its writes refuse in an agent session, and the inert rule holds on the credsvc path too (F22) | MC5 `TestPoolWritesRefuseInAgentSession`, MC6 `TestSubscriptionPoolInertInCredsvc` |

## Slices and tasks

**Slice A — vocabulary and read (inert):** MC1, MC2, MC3. **Slice B — migration and commands:** MC4, MC5. **Slice C — selection:** MC6, MC7. **Slice D — surfaces:** MC8, MC9. **Owner-run checks:** MC10.

### MC1 — craft types and keys
- **Files:** `internal/config/craft.go` (new), `internal/config/config.go`, `internal/config/layer.go`, `internal/localcfg/localcfg.go`, `internal/projectpolicy/policy.go`, `schemas/*.schema.json`, `internal/config/testdata/` (corpus).
- **Interfaces:** produces `Credential{Name,Type}` (types `openrouter|anthropic|claude-oauth|vertex`, `vertex` built in), `Pool{Name,Strategy,Members}`, `Craft{Model,Access}` for the four crafts; consumes `ClaudeAccountNameRE`.
- **Schema change:** yes (new keys; old `agent.models.coder/reviewer`, `default_claude_account`, `stages.*.claude_account`, `claude_account_auto` removed by MC4).
- **Tests:** `TestCraftInheritance`, `TestSecurityCraftDoesNotEnableLevel3`, `TestNotYetTypesRefuse`. **Command:** `GOFLAGS=-p=1 go test ./internal/config ./internal/localcfg ./internal/projectpolicy -run 'Craft|Credential|Pool' -count=1`.
- **Depends on:** nothing. **Red step:** the inheritance test fails before the types exist. **Tier:** standard. **Commit:** `config: credentials, pools and crafts as typed keys (MC1)`.

### MC2 — the resolver: effective craft with provenance
- **Files:** `internal/craft/resolve.go`, `internal/craft/resolve_test.go` (new package, one place that answers "which model and which credential or pool, and from where").
- **Interfaces:** consumes MC1 types and the layers; produces `Effective(craft) {Model, ModelFrom, Access, AccessFrom, Inert}`.
- **Tests:** model and access inherit separately; security craft changes only the model and keeps the senior pool; provenance names the layer. **Command:** `go test ./internal/craft -count=1`.
- **Depends on:** MC1. **Red step:** a table test with the spec's own example fails first. **Tier:** standard. **Commit:** `craft: one resolver for the effective model and access (MC2)`.

### MC3 — layering and policy merge (tightening)
- **Files:** `internal/policy/craft.go` (new), `internal/policy/merge.go`, `internal/policy/craft_test.go` (new).
- **Interfaces:** consumes MC1; produces the merge rule of D1628 and a looser value recorded in `Effective.Ignored`.
- **Tests:** `TestRepoCannotAddCredential`, `TestRepoCanOnlyNarrowPool`, `TestLooserPoolNeverWins`, `TestRepoCannotReorderOrChangeStrategy` (narrow = subsequence of the project's list, order kept; failover is tighter than round-robin, F12). A repository's keys come from the owner-written policy node, never the shared file (F25). **Command:** `go test ./internal/policy -run Craft -count=1`.
- **Depends on:** MC1; **D1628 answered**. **Red step:** a repository layer adding a credential must fail first. **Tier:** opus + mandatory independent review (spending and egress grant); in-run level 3. **Commit:** `policy: a repository selects and narrows crafts, never adds (MC3)`.

### MC4 — migration inside `fugaro update`
- **Files:** `internal/cli/init_craft_migrate.go` (new), `internal/cli/init.go`, `internal/cli/settings_set.go`, `internal/localcfg/localcfg.go`, `internal/cli/testdata/` (previous-version fixtures).
- **Interfaces:** consumes MC1, MC2; produces the in-place conversion (accounts → credentials; auto list → pool; `agent.models.*` and stage keys → craft keys; route `secret`s, `agent.oauth_token`, `verifier_accounts`, `review_floor` → credentials and craft keys), with a format-version bump for every JSON document whose fields are removed (`models ls --json`, policy node; F17), for the local, project and repository layers.
- **Tests:** `TestUpdateConvergesPreviousInstallation` (an installation at the previous version converges and `fugaro run` resolves the same model and credential before and after), `TestUpdateIsIdempotent`, `TestCommittedOldKeysRefuseWithTheFixCommand`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli -run 'CraftMigrate|Converges' -count=1`.
- **Depends on:** MC1, MC2. **Red step:** the converge test fails on an installation with `default_claude_account` set. **Tier:** standard + converge test (HARD RULE); release notes `### For operators`: "run `fugaro update`". **Commit:** `update: migrate accounts, auto list and model keys to credentials, pools and crafts (MC4)`.

### MC5 — credential commands and pool selection in the runner
- **Files:** `internal/cli/accounts_add.go`, `internal/cli/accounts.go`, `internal/cli/accounts_pool.go` (new), `internal/runner/routing.go`, `internal/runner/gateway.go`, `internal/config/stage_accounts.go`.
- **Interfaces:** consumes MC1–MC3; produces `fugaro accounts add --type …`, `fugaro accounts pool add|rm|ls`, the ordered failover of a pool in a run, the D1622 inert rule, the vertex-under-enforce refusal (reuses `runner.VertexBudgetRefusal`), the built-in `vertex` credential (D1624).
- **Tests:** `TestSubscriptionPoolInertWithoutSwitch`, `TestVertexPoolRefusedUnderEnforce`, `TestPoolFailsOverOnExhaustion` (fake 429/402/529 upstream), `TestMemberOutsideAllowDataToSkipped`, `TestJobEnvHoldsOnlyItsCraftsSecrets`, `TestPoolRefusedWhenAMemberDoesNotServeTheModel`, `TestPoolWritesRefuseInAgentSession`. The writes (`accounts pool add/rm`, strategy change, the `subscription_spread` flag) refuse under `agentMarker` as `accounts_add.go` does; failover on the runner path happens at a stage boundary with the next secret re-fetched, never by retrying inside `gateway.go` (F20); the `subscription_spread` flag replaces `claude_account_auto` and keeps its refusals (F22). **Command:** `GOFLAGS=-p=1 go test ./internal/cli ./internal/runner -run 'Pool|AccountsAdd|Vertex' -count=1`.
- **Depends on:** MC1, MC2, MC3; **AK6b and AK7 merged** (`docs/api-key-review`; F26, D1638); **D1624, D1626, D1627, D1631, D1632, D1634–D1637 answered**; F5's signal table written first. **Shared with AK6b and AK7:** `internal/runner/routing.go` alone — AK5's admission and AK7's upstream over it write it first (`docs/api-key-review` implementation-plan §2 books the file twice, at L2 and L3), so this task reads the merged file and re-decides none of it; `internal/runner/gateway.go` is MC5's own, because AK7 writes no gateway file (D1418 puts the provider's base URL and auth shape in the bundle, so the gateway's upstream kind never moves). **Held until AK6b and AK7 have merged** (F26, D1638): no part of MC5 starts before then, so it extends the merged shapes rather than this package's sketches of them. **Red step:** the failover test fails with a pool of one exhausted credential. **Tier:** opus + mandatory independent review (credentials, egress, selection); level 3. **Commit:** `accounts: typed credentials and pools, failover in a run (MC5)`.

### MC6 — the credentials service serves Anthropic-key and OAuth pool members
- **Files:** `internal/credsvc/cascade.go`, `internal/credsvc/pool.go` (new), `internal/creds/select.go`, `internal/credsvc/pool_test.go` (new); **and the two `docs/api-key-review` tasks folded into this one on 2026-11-10** (that folder records it): AK9's files — `internal/credsvc/auth.go` and its test, `internal/credsvc/server.go` and its test, `internal/cli/validate.go` and its test, `internal/cli/credentials_serve.go` and its test, `internal/infra/tfvars.go`, `deploy/terraform/gcp/modules/installation/variables.tf`, `deploy/terraform/gcp/modules/installation/credentials.tf` — and AK10's — `internal/cli/creds.go`, `internal/cli/creds_test.go`, `internal/cli/creds_skew_test.go` (new), `internal/cli/recipes_skew.go`.
- **Interfaces:** consumes MC5's pool; the cascade selects **by tier** (A OAuth before B key, listed order only a tie-break, sticky per run: D154); `TierC` stays reserved and OpenRouter/Vertex members never reach credsvc (they run on MC5's runner path; D1634). Back-off and probe unchanged.
- **Tests:** the cascade's own tests stay green; `TestPoolMembersAreServedByTierOrder`, `TestOpenRouterAndVertexMembersNeverReachCredsvc`, `TestExhaustedMemberBackedOffAndProbed`, `TestReviewCraftPoolOnlyReviewClass` (F21: `reviewCandidates` keeps "never an entry the cascade could fall to"), `TestSubscriptionPoolInertInCredsvc` (F22); and the folded tasks' own — AK9's `TestReviewAccountRefusalsNameTheAxisAndNotAnAccount`, `TestNewAuthenticatorRefusesAnIncompleteConfig` and `TestValidateNoteNamesTheAxisAndNotAClaudeAccount`, and AK10's five `TestCredsSet…` cases, the skew guard on the bundle write. **Command:** `go test ./internal/credsvc ./internal/creds -run 'Pool|Cascade|Review' -count=1`.
- **Depends on:** MC5; **AK6b and AK7 merged** (`docs/api-key-review`; F26, D1638). **Shared with AK6b:** `internal/credsvc/cascade.go` (AK4's candidate list, then AK6b's tier map) and `internal/creds/select.go` (the tier-C candidate the provider key is offered as, whose `checkCandidates` refusal and `TestSelectRefusesReservedTierC` are AK6b's own) — both read as merged, neither re-decided; `internal/credsvc/pool.go` and its test are MC6's new files, and the AK9/AK10 files above are its by the fold. **Held until AK6b and AK7 have merged** (F26, D1638): no part of MC6 starts before then, and the fold's own ordering is satisfied by that — AK10's guard judges a document only a CLI carrying AK6b's provider entry can write. **Red step:** a pool with an API key listed first must still be served OAuth-first. **Tier:** opus + mandatory independent review; level 3. **Commit:** `credsvc: pool members are served by tier order (MC6)`.

### MC7 — round-robin
- **Files:** `internal/credsvc/roundrobin.go` (new), `internal/credsvc/pool.go`, `internal/budget/` or `internal/credsvc/state` (the counter, per D1625), tests.
- **Interfaces:** consumes MC6; produces the strategy `round-robin` (skip failing members).
- **Tests:** `TestRoundRobinSpreadsAcrossRuns`, `TestRoundRobinSkipsBackedOffMember`, `TestRoundRobinCompareAndSetUnderConcurrentSelectors` (several Selector instances, one counter written with compare-and-set; F6), `TestRoundRobinNeverMovesAStickyRun` (per run or per stage, never per call: D154, D874). **Command:** `go test ./internal/credsvc -run RoundRobin -count=1`.
- **Depends on:** MC6; **D1625 answered**. **Red step:** three selections must not all pick member 1. **Tier:** opus + independent review; level 3. **Commit:** `credsvc: round-robin selection over a pool (MC7)`.

### MC8 — the effective-craft command, the record field and the console read
- **Files:** `internal/cli/craft.go` (new: `fugaro craft ls [--json]`), `internal/cli/project.go` (`consoleReadPaths` row) and `docs/design/desktop-console.md` §5.1's allow-list table (the Go test holds the two equal; not `spawn.ts`), `internal/runstore/` (additive `craft_credentials`), `schemas/`.
- **Interfaces:** consumes MC2 and MC5/MC6 (what served).
- **Tests:** golden `craft ls --json` (versioned, no secret value), the §5.1 table equality test, `TestRunRecordNamesCraftCredentials`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli ./internal/runstore -run 'Craft|ConsoleRead|DesktopConsole' -count=1`.
- **Depends on:** MC2 (the read ships before the keys are writable: D1633, which **must be answered**); the record part also on MC6. **Red step:** the §5.1 equality test fails when the row is added to `consoleReadPaths` only. **Tier:** opus + independent review (console spawn/IPC allow-list); level 3. **Commit:** `craft: fugaro craft ls and the record's craft_credentials (MC8)`.

### MC9 — docs, skills and website
- **Files:** `docs/project-claude-accounts.md`, `docs/multi-model.md`, `docs/models-commands/`, `plugin/skills/*`, `README.md`, `docs/releases/` entry, `website/` pages naming the old keys.
- **Tests:** `go test ./internal/cli -run Docs`, `go test ./plugin/ -count=1`, `npm run check` in `website/` if content changed. **Depends on:** MC4, MC5, MC8 merged (names settled). **Tier:** standard. **Commit:** `docs: crafts, credentials and pools (MC9)`.

### MC10 — owner-run live checks (OWNER-RUN)
- **Check 420:** pool failover on a real installation: add two API-key credentials, exhaust one (a deliberately tiny cap at the provider), run, see the second serve and `craft_credentials` name it. **Check 421:** round-robin spread over N runs. **Check 422:** `fugaro update` on an installation at the previous version converges with no manual step. Each ships as **one idempotent script** in `scripts/live-checks/` with `--dry-run`, PASS/FAIL, teardown, refusing `belong`, plus a hermetic fake-gcloud test. The owner types the script's path only.
- **Depends on:** MC4 (422), MC6 (420), MC7 (421). **Tier:** standard; script reviewed in-run.

## Parallel execution

Edges: MC1→MC2→(MC3, MC4, MC5, MC8-read); MC3→MC5; MC4 after MC1, MC2; MC5→MC6→MC7; MC6→MC8-record; (MC4, MC5, MC8)→MC9; MC4/MC6/MC7→MC10.

**One edge leaves this plan** (F26, D1638): **MC5 and MC6 are held until `docs/api-key-review`'s AK6b and AK7 have merged**, so the two levels below start only after those two land there. AK7 writes exactly one file MC5 also writes (`internal/runner/routing.go`), and MC5 reads it as merged; AK6a is already merged.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | MC1 |
| 1 | MC2 |
| 2 | MC3, MC4 |
| 3 | MC5 (needs MC3, and AK6b and AK7 merged) |
| 4 | MC6 (needs MC5, and AK6b and AK7 merged) |
| 5 | MC7, MC8 |
| 6 | MC9, MC10 |

**Critical path:** MC1 → MC2 → MC3 → (**AK6b → AK7**, `docs/api-key-review`) → MC5 → MC6 → MC7 → MC10. What must be sequential and why: MC5 consumes MC3's merge rule and MC6 consumes MC5's pool shape; MC7 builds on MC6's candidate list; and MC5 and MC6 are written against the API-key credential type the api-key review path already built (F26, D1638), so AK6b and AK7 come first rather than being re-designed here.

**Shared-files table** (no level holds two writers of one file):

| File | Writers (level) |
| --- | --- |
| `internal/config/config.go`, `layer.go` | MC1 (0) |
| `internal/localcfg/localcfg.go` | MC1 (0), MC4 (2) |
| `internal/projectpolicy/policy.go` | MC1 (0) |
| `internal/config/stage_accounts.go` | MC5 (3) |
| `internal/cli/init.go`, `settings_set.go` | MC4 (2) |
| `internal/cli/accounts_add.go`, `accounts.go` | MC5 (3) |
| `internal/runner/routing.go` | MC5 (3); `docs/api-key-review`'s AK5 and AK7 write it first |
| `internal/runner/gateway.go` | MC5 (3) |
| `internal/credsvc/cascade.go` | MC6 (4); `docs/api-key-review`'s AK4 and AK6b write it first |
| `internal/creds/select.go` | MC6 (4); `docs/api-key-review`'s AK6b writes it first |
| `internal/credsvc/pool.go` (new) | MC6 (4); MC7 (5): different levels |
| `internal/credsvc/auth.go`, `server.go` | MC6 (4) — AK9's, folded in 2026-11-10 |
| `internal/cli/validate.go`, `credentials_serve.go` | MC6 (4) — AK9's, folded in 2026-11-10 |
| `internal/infra/tfvars.go`, `deploy/terraform/gcp/modules/installation/variables.tf`, `credentials.tf` | MC6 (4) — AK9's, folded in 2026-11-10 |
| `internal/cli/creds.go`, `recipes_skew.go` | MC6 (4) — AK10's, folded in 2026-11-10 |
| `internal/cli/project.go` | MC8 (5) |
| `schemas/*` | MC1 (0), MC8 (5) |
| `plugin/skills/*`, `docs/*` | MC9 (6) |

**No level holds two writers of one file**, and the rows that name a task of `docs/api-key-review` are not a second writer inside a level: those files are written by that folder's tasks, which **merge before this plan's MC5 and MC6 start** (F26, D1638), so MC5 and MC6 read them as merged and re-decide nothing in them. AK9's and AK10's rows are MC6's own by the fold of 2026-11-10, in the same commit as the rest of MC6. A file a later task wants to move between levels says so here first.

## Checks

Checks **420, 421, 422** are the owner-run live checks of MC10; **423–429** are reserved and unused.

## Release actions

MC4's PR carries the `### For operators` entry: run `fugaro update`; it converges accounts, the auto list and model keys to credentials, pools and crafts in place; nothing else.
