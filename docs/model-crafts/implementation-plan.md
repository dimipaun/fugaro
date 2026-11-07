# Model crafts and credentials: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (D1620–D1633, F1–F19). **Nothing is implemented. No task is approved: each needs the owner's explicit go; answering a decision is not a go.** Owner answers that shaped it (2026-11-07): the word *craft* (D1620); generalise in place and migrate in `update` (D1621); pools ship, a pool of two or more subscription credentials stays off until the D875 switch (D1622); OpenRouter, Anthropic key, Vertex and Claude OAuth now, OpenAI key and Codex OAuth a documented "not yet" (D1623). D1624–D1633 are open; tasks that depend on one say so and **do not start before it is answered**.

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
- **Tests:** `TestRepoCannotAddCredential`, `TestRepoCanOnlyNarrowPool`, `TestLooserPoolNeverWins`. **Command:** `go test ./internal/policy -run Craft -count=1`.
- **Depends on:** MC1; **D1628 answered**. **Red step:** a repository layer adding a credential must fail first. **Tier:** opus + mandatory independent review (spending and egress grant); in-run level 3. **Commit:** `policy: a repository selects and narrows crafts, never adds (MC3)`.

### MC4 — migration inside `fugaro update`
- **Files:** `internal/cli/init_craft_migrate.go` (new), `internal/cli/init.go`, `internal/cli/settings_set.go`, `internal/localcfg/localcfg.go`, `internal/cli/testdata/` (previous-version fixtures).
- **Interfaces:** consumes MC1, MC2; produces the in-place conversion (accounts → credentials; auto list → pool; `agent.models.*` and stage keys → craft keys; route `secret`s → credentials), for the local, project and repository layers.
- **Tests:** `TestUpdateConvergesPreviousInstallation` (an installation at the previous version converges and `fugaro run` resolves the same model and credential before and after), `TestUpdateIsIdempotent`, `TestCommittedOldKeysRefuseWithTheFixCommand`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli -run 'CraftMigrate|Converges' -count=1`.
- **Depends on:** MC1, MC2. **Red step:** the converge test fails on an installation with `default_claude_account` set. **Tier:** standard + converge test (HARD RULE); release notes `### For operators`: "run `fugaro update`". **Commit:** `update: migrate accounts, auto list and model keys to credentials, pools and crafts (MC4)`.

### MC5 — credential commands and pool selection in the runner
- **Files:** `internal/cli/accounts_add.go`, `internal/cli/accounts.go`, `internal/cli/accounts_pool.go` (new), `internal/runner/routing.go`, `internal/runner/gateway.go`, `internal/config/stage_accounts.go`.
- **Interfaces:** consumes MC1–MC3; produces `fugaro accounts add --type …`, `fugaro accounts pool add|rm|ls`, the ordered failover of a pool in a run, the D1622 inert rule, the vertex-under-enforce refusal (reuses `runner.VertexBudgetRefusal`), the built-in `vertex` credential (D1624).
- **Tests:** `TestSubscriptionPoolInertWithoutSwitch`, `TestVertexPoolRefusedUnderEnforce`, `TestPoolFailsOverOnExhaustion` (fake 429/402/529 upstream), `TestMemberOutsideAllowDataToSkipped`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli ./internal/runner -run 'Pool|AccountsAdd|Vertex' -count=1`.
- **Depends on:** MC1, MC2, MC3; **D1624, D1626, D1627, D1631, D1632 answered**; F5's signal table written first. **Red step:** the failover test fails with a pool of one exhausted credential. **Tier:** opus + mandatory independent review (credentials, egress, selection); level 3. **Commit:** `accounts: typed credentials and pools, failover in a run (MC5)`.

### MC6 — the credentials service selects from a pool
- **Files:** `internal/credsvc/cascade.go`, `internal/credsvc/pool.go` (new), `internal/creds/` (selector input), `internal/credsvc/pool_test.go` (new).
- **Interfaces:** consumes MC5's pool; produces the candidate ordering the existing cascade (`Select`, `retryPool`, `probeDue`) walks, with back-off and probe unchanged.
- **Tests:** the cascade's own tests stay green; `TestPoolOrderIsTheCandidateOrder`, `TestExhaustedMemberBackedOffAndProbed`. **Command:** `go test ./internal/credsvc ./internal/creds -run 'Pool|Cascade' -count=1`.
- **Depends on:** MC5. **Red step:** a pool's second member must be tried only after the first backs off. **Tier:** opus + mandatory independent review; level 3. **Commit:** `credsvc: a pool is the cascade's candidate list (MC6)`.

### MC7 — round-robin
- **Files:** `internal/credsvc/roundrobin.go` (new), `internal/credsvc/pool.go`, `internal/budget/` or `internal/credsvc/state` (the counter, per D1625), tests.
- **Interfaces:** consumes MC6; produces the strategy `round-robin` (skip failing members).
- **Tests:** `TestRoundRobinSpreadsAcrossRuns`, `TestRoundRobinSkipsBackedOffMember`, concurrency test with two simultaneous selections. **Command:** `go test ./internal/credsvc -run RoundRobin -count=1`.
- **Depends on:** MC6; **D1625 answered**. **Red step:** three selections must not all pick member 1. **Tier:** opus + independent review; level 3. **Commit:** `credsvc: round-robin selection over a pool (MC7)`.

### MC8 — the effective-craft command, the record field and the console read
- **Files:** `internal/cli/craft.go` (new: `fugaro craft ls [--json]`), `internal/cli/project.go` (`consoleReadPaths` row), `console/src/main/spawn.ts` and its equality test, `internal/runstore/` (additive `craft_credentials`), `schemas/`.
- **Interfaces:** consumes MC2 and MC5/MC6 (what served).
- **Tests:** golden `craft ls --json` (versioned, no secret value), the exact-argv equality test, `TestRunRecordNamesCraftCredentials`. **Command:** `GOFLAGS=-p=1 go test ./internal/cli ./internal/runstore -run 'Craft|ConsoleRead' -count=1` and `npm run test:unit -- --maxWorkers=2` in `console/` for the equality test.
- **Depends on:** MC2 (the read ships before the keys are writable: D1633, which **must be answered**); the record part also on MC6. **Red step:** the fence equality test fails when the row is added on one side only. **Tier:** opus + independent review (console spawn/IPC allow-list); level 3. **Commit:** `craft: fugaro craft ls and the record's craft_credentials (MC8)`.

### MC9 — docs, skills and website
- **Files:** `docs/project-claude-accounts.md`, `docs/multi-model.md`, `docs/models-commands/`, `plugin/skills/*`, `README.md`, `docs/releases/` entry, `website/` pages naming the old keys.
- **Tests:** `go test ./internal/cli -run Docs`, `go test ./plugin/ -count=1`, `npm run check` in `website/` if content changed. **Depends on:** MC4, MC5, MC8 merged (names settled). **Tier:** standard. **Commit:** `docs: crafts, credentials and pools (MC9)`.

### MC10 — owner-run live checks (OWNER-RUN)
- **Check 420:** pool failover on a real installation: add two API-key credentials, exhaust one (a deliberately tiny cap at the provider), run, see the second serve and `craft_credentials` name it. **Check 421:** round-robin spread over N runs. **Check 422:** `fugaro update` on an installation at the previous version converges with no manual step. Each ships as **one idempotent script** in `scripts/live-checks/` with `--dry-run`, PASS/FAIL, teardown, refusing `belong`, plus a hermetic fake-gcloud test. The owner types the script's path only.
- **Depends on:** MC4 (422), MC6 (420), MC7 (421). **Tier:** standard; script reviewed in-run.

## Parallel execution

Edges: MC1→MC2→(MC3, MC4, MC5, MC8-read); MC3→MC5; MC4 after MC1, MC2; MC5→MC6→MC7; MC6→MC8-record; (MC4, MC5, MC8)→MC9; MC4/MC6/MC7→MC10.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | MC1 |
| 1 | MC2 |
| 2 | MC3, MC4 |
| 3 | MC5 (needs MC3) |
| 4 | MC6 |
| 5 | MC7, MC8 |
| 6 | MC9, MC10 |

**Critical path:** MC1 → MC2 → MC3 → MC5 → MC6 → MC7 → MC10. What must be sequential and why: MC5 consumes MC3's merge rule and MC6 consumes MC5's pool shape; MC7 builds on MC6's candidate list.

**Shared-files table** (no level holds two writers of one file):

| File | Writers (level) |
| --- | --- |
| `internal/config/config.go`, `layer.go` | MC1 (0) |
| `internal/localcfg/localcfg.go` | MC1 (0), MC4 (2) |
| `internal/projectpolicy/policy.go` | MC1 (0) |
| `internal/config/stage_accounts.go` | MC5 (3) |
| `internal/cli/init.go`, `settings_set.go` | MC4 (2) |
| `internal/cli/accounts_add.go`, `accounts.go` | MC5 (3) |
| `internal/runner/routing.go`, `gateway.go` | MC5 (3) |
| `internal/credsvc/cascade.go`, `pool.go` | MC6 (4); `pool.go` again MC7 (5): different levels |
| `internal/cli/project.go` | MC8 (5) |
| `schemas/*` | MC1 (0), MC8 (5) |
| `plugin/skills/*`, `docs/*` | MC9 (6) |

## Checks

Checks **420, 421, 422** are the owner-run live checks of MC10; **423–429** are reserved and unused.

## Release actions

MC4's PR carries the `### For operators` entry: run `fugaro update`; it converges accounts, the auto list and model keys to credentials, pools and crafts in place; nothing else.
