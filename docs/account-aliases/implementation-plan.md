# Account aliases: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (F1–F16, D2070–D2082).
**Status as of 2026-11-10: plan only, 0/5 merged; nothing is implemented; no task is approved; see
[docs/status.md](../status.md) for the current table.** Every decision is **OPEN**: the plan is written
to the recommendations, and a task that names a decision starts only after the owner answers it — and
after the owner's separate, explicit go on that task. An answer is not a go.

## Conventions

`CLAUDE.md` (a field never changes meaning; comments say why; hermetic tests; no secret-shaped literal
in a test, a doc or a fixture; docs are tested with `go test ./internal/cli/ -run Docs`; never edit
`.github/workflows/`). `.claude/rules/credentials-service.md` does not load (no `internal/creds` or
`internal/credsvc` file is touched). `.claude/rules/terraform.md` loads for nothing here: no module and
no root changes; the job set moves only through `internal/infra/spec.go`'s Go, and no task runs
`terraform`, `fugaro setup`, `fugaro update` or a live cloud action. Targeted tests only
(`GOFLAGS=-p=2`, `-run` names; never a whole `internal/cli` or `internal/runner` run). Security-tree
tasks (the launch's account choice, the runner's stage credential, the job set) are launched with
`fugaro run --review-levels` asking for level 3 in the run, never a second local review.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| An alias never reaches the node, the task or the record by its own name (F4) | AL1 `TestPublishWritesTheResolvedAccountNotTheAlias`, `TestLaunchTaskCarriesTheResolvedAccount` |
| Resolution comes before the allow-list; an alias never admits an account the repository is not admitted to (F6) | AL1 `TestLaunchRefusesAnAliasWhoseTargetIsNotAdmitted` |
| An alias is never in the shared file, the project layer or `fugaro.yaml` (F5, F7) | AL0 `TestSharedDropsAliases`, `TestSharedConfigRefusesAliases` |
| One namespace: an alias never shares a name with an account, credential or pool (F8, D2072) | AL0 `TestAliasNameCollisionsRefused` |
| No chains, no pool targets, no non-account targets (D2073) | AL0 `TestAliasTargetRules` |
| A run in flight keeps the account its launch resolved (the ruling; F3, D2074) | AL2 `TestMixedRunKeepsItsLaunchAccountAfterARepoint` |
| The mixed-stage fallback admits nothing the node does not (D2074) | AL2 `TestMixedRunRefusesALaunchAccountTheNodeDoesNotAdmit` |
| A repoint never deletes a job variant (D2075) | AL1 `TestRepointKeepsTheOldTargetVariant` |
| The repoint is refused in a coding agent's session (D2077) | AL3 `TestAccountsAliasSetRefusedInAgentSession` |
| `fugaro update` converges an installation with no alias block byte for byte (HARD RULE; F4) | AL0 `TestLocalConfigWithoutAliasesRoundTrips` |
| `accounts ls --json` only gains a member (a field never changes meaning) | AL3 `TestAccountsLsJSONListsAliasesAdditively` |

## Slices and tasks

**Slice A — the inert vocabulary:** AL0. **Slice B — resolution and the in-flight guard:** AL1, AL2.
**Slice C — the command and the reads:** AL3. **Slice D — docs:** AL4. **Owner-run:** Check 550.

### AL0 — the alias block, its rules and the resolver (inert)

- **Files:** `internal/config/account_aliases.go` (new), `internal/config/account_aliases_test.go` (new),
  `internal/localcfg/localcfg.go` (field `ClaudeAccountAliases map[string]string
  \`yaml:"claude_account_aliases,omitempty"\``, its load-time validation, and `choiceRefProblem`
  admitting an alias), `internal/localcfg/shared.go` (`Shared` drops it),
  `internal/localcfg/account_aliases_test.go` (new), `internal/cli/sharedcfg.go` (`sharedForbidden` and `sharedNeverPublished` gain the key).
- **Interfaces produced:** `config.ValidateAccountAliases(aliases map[string]string, accounts
  map[string]ClaudeAccount, creds []Credential, pools []Pool) []Problem`;
  `config.ResolveAccountAlias(aliases map[string]string, name string) (account, alias string)` (one
  step, `alias == ""` when `name` is not an alias); `(*localcfg.Config).ResolveAccount(name string)
  (account, alias string)`. **Consumed:** `config.ClaudeAccountNameRE`, `credentialNameProblem`,
  `config.AxisNonAccountValue`.
- **Schema/config change:** the local config gains one optional block. No node, task, record or
  `fugaro.yaml` change.
- **Tests:** `TestAliasNameCollisionsRefused`, `TestAliasTargetRules` (chain, pool, credential of
  another type, `config.AnthropicKeyAccount`, undefined account), `TestChoiceKeysMayNameAnAlias`,
  `TestSharedDropsAliases`, `TestSharedConfigRefusesAliases`, `TestLocalConfigWithoutAliasesRoundTrips`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config ./internal/localcfg -run 'Alias|ChoiceKeys|RoundTrips' -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'SharedConfigRefusesAliases' -count=1`.
- **Red step:** `TestChoiceKeysMayNameAnAlias` fails on `choiceRefProblem`'s "not defined here" sentence.
- **Depends on:** D2070, D2072, D2073 answered; **#2274 (MC5) and #2247 merged** (both edit
  `internal/localcfg/localcfg.go`), and #1994 if it is still open then (same file).
- **Tier:** opus; level 3 (it decides what a credential-choosing key may hold).
- **Commit:** `config: account aliases in the local config, their rules and the resolver (AL0)`.

### AL1 — every reader resolves at the launch, the publish and the job set

- **Files:** `internal/cli/run.go` (`claudeAccountFor` resolves the flag, rung 2 and rung 4 before
  `claudeAccountAllowed`/`claudeAccountMembership`; `stageAccountsOf`, `stageAccountOf`,
  `mixedStageAccount` read resolved names; the launch line prints `ALIAS -> ACCOUNT`),
  `internal/cli/init_policy_publish.go` (`configAccountValues` writes the resolved account),
  `internal/cli/validate.go` (`:803` judges resolved names), `internal/cli/project_cmd.go` (`:1029`),
  `internal/infra/spec.go` (`mixedStageAccounts`, `mixedDefaultAccount` resolve), and the tests
  `internal/cli/account_alias_launch_test.go` (new), `internal/infra/account_alias_spec_test.go` (new).
- **Interfaces:** consumes AL0's `ResolveAccount`; produces no new exported symbol.
- **Tests:** `TestLaunchResolvesAnAliasOnEveryRung`, `TestLaunchRefusesAnAliasWhoseTargetIsNotAdmitted`,
  `TestLaunchTaskCarriesTheResolvedAccount`, `TestPublishWritesTheResolvedAccountNotTheAlias`,
  `TestRepointIsAPublishWidening` (the existing `policyAccountWidening` line, F10),
  `TestJobSetBuildsTheTargetVariant`, `TestRepointKeepsTheOldTargetVariant` (D2075: the old target's
  variant stays in `WorkflowAccounts` until a retirement — the mechanism is this task's, chosen after the
  red test settles the UNVERIFIED item), and, if #2252 has merged, `TestCraftMigrationResolvesAnAlias`
  in `internal/cli/init_craft_migrate.go`'s test (D2082).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Alias|RepointIsAPublishWidening|CraftMigrationResolves' -count=1 && GOFLAGS=-p=2 go test ./internal/infra -run 'JobSetBuildsTheTargetVariant|RepointKeepsTheOldTargetVariant' -count=1`.
- **Red step:** `TestLaunchResolvesAnAliasOnEveryRung` fails: `claudeAccountAllowed` refuses the alias
  as an undefined account.
- **Depends on:** AL0; D2071, D2075, D2082 answered.
- **Serialization against open PRs:** `internal/cli/run.go` is edited by #2240, #2246, #2250, #2202 and
  #2170; `internal/infra/spec.go` by #2282, #2170, #2166 and #1994; `init_policy_publish.go` by #2274 and
  #2247. AL1 starts on whatever of those has merged and rebases on the rest; it shares no level with
  another task of this plan.
- **Tier:** opus; level 3 (launch credential choice and job mounts).
- **Commit:** `cli: aliases resolve at the launch, the publish and the job set (AL1)`.

### AL2 — the in-flight guard for a mixed run

- **Files:** `internal/runner/agentenv.go` (`resolveMixedStages`: when the run holds a named account
  (`spec.OAuthToken`) and the node now names another for a mixed stage, serve the stage with the
  run's own account if the node's `accounts.<name>.allow_repos` admits the repository, log the
  difference, refuse otherwise), `internal/runner/account_alias_inflight_test.go` (new).
- **Interfaces:** consumes `projectpolicy.AdmitRepo` and the memoized node; produces nothing exported.
- **Tests:** `TestMixedRunKeepsItsLaunchAccountAfterARepoint`,
  `TestMixedRunRefusesALaunchAccountTheNodeDoesNotAdmit`, `TestMixedRunWithoutARepointIsUnchanged`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'MixedRun' -count=1`.
- **Red step:** `TestMixedRunKeepsItsLaunchAccountAfterARepoint` fails with `stageAgentEnv`'s refusal of
  a token the job does not hold.
- **Depends on:** D2074 answered. Independent of AL0: it guards a hand edit of a stage key as well.
  **Serialization:** #2256 edits `internal/runner/agentenv.go`; AL2 waits for it.
- **Tier:** opus; level 3 (a stage's credential in the runner).
- **Commit:** `runner: a mixed run keeps the account its launch resolved (AL2)`.

### AL3 — `fugaro accounts alias`, `accounts rm` and the reads

- **Files:** `internal/cli/accounts_alias.go` (new: `alias set`, `alias rm`, writing through
  `applySettingsEdit` and converging through `accountsAddRunStages` with the door `accounts alias`),
  `internal/cli/accounts_alias_test.go` (new), `internal/cli/accounts.go` (`AddCommand` at `:37`; the
  text read and `accountsListDoc` gain `aliases`), `internal/cli/accounts_rm.go` (refuse a target),
  `internal/settings/registry.go` (row `claude_account_aliases.*`, owner `accounts`),
  `schemas/settings.schema.json`, `website/content/cli-reference.md` (regenerated, never hand-edited),
  and the plugin's coverage tests if a skill names the command (`plugin/skillcli_test.go` stays green).
- **Interfaces:** consumes AL0 and AL1; produces the two subcommands and `accountsListDoc.Aliases
  map[string]string \`json:"aliases,omitempty"\``.
- **Tests:** `TestAccountsAliasSetWritesAndConverges` (fake converge),
  `TestAccountsAliasSetRefusedInAgentSession`, `TestAccountsAliasRefusesChainPoolAndUnknownTarget`,
  `TestAccountsAliasRmRefusedWhileAKeyNamesIt`, `TestAccountsRmRefusesAnAliasTarget`,
  `TestAccountsLsJSONListsAliasesAdditively`, `TestSettingsRegistryHasTheAliasRow`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'AccountsAlias|AccountsRmRefusesAnAliasTarget|AccountsLsJSON|SettingsRegistryHasTheAliasRow' -count=1 && GOFLAGS=-p=2 go test ./internal/settings ./schemas -count=1`.
- **Red step:** `TestAccountsAliasSetWritesAndConverges` fails: `accounts alias` is an unknown command.
- **Depends on:** AL0, AL1; D2076, D2077, D2079, D2081 answered.
- **Serialization:** `internal/cli/accounts.go` (#2282), `internal/cli/accounts_rm.go` (#2252),
  `internal/settings/registry.go` and `schemas/settings.schema.json` (#2274, #2281, #2245, #2247, #2162,
  #1994).
- **Tier:** opus; level 3 (an owner-only write that moves which credential stages spend).
- **Commit:** `accounts: alias set and rm, one command that repoints new launches (AL3)`.

### AL4 — docs and skills

- **Files:** `docs/project-claude-accounts.md` (a short section: aliases, the command, what a run in
  flight keeps), `docs/multi-model.md` if it names the stage keys' values, `plugin/skills/*` only where a
  skill already names `default_claude_account` or the stage keys, and the docs tests those pages pin.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`, `go test ./plugin/ -count=1`.
- **Depends on:** AL3 merged (names settled). **Tier:** sonnet. **Commit:** `docs: account aliases (AL4)`.

### Check 550 — OWNER-RUN

A repoint with a run in flight, on a throwaway installation (never the project `belong`): launch an
`api-key` mixed run whose review stage names an alias, repoint the alias while the run is implementing,
and confirm the run's review stage is served by the launch's account (`stage_accounts` in its record)
while a new launch uses the new target. It ships with AL3 as one idempotent script in
`scripts/live-checks/` with `--dry-run`, PASS/FAIL and a hermetic fake-gcloud test; the owner types the
script's path only. Checks 551–554 are reserved.

## Parallel execution

Edges: AL0 → AL1 → AL3 → AL4; AL2 has no edge inside the plan (external: #2256). Check 550 after AL3.

| Level | Tasks |
| --- | --- |
| 0 | AL0, AL2 |
| 1 | AL1 |
| 2 | AL3 |
| 3 | AL4; Check 550 (owner) |

**Critical path:** AL0 → AL1 → AL3 → AL4, behind the external merges of #2274, #2247 and #2252. AL1 must
follow AL0 (it consumes `ResolveAccount`), AL3 must follow AL1 (the command converges through the
publish and job set AL1 teaches to resolve), and AL4 names what AL3 ships.

| File | Writers (level) |
| --- | --- |
| `internal/config/account_aliases.go`, its test | AL0 (0) |
| `internal/localcfg/localcfg.go`, `shared.go`, their new test | AL0 (0) |
| `internal/cli/sharedcfg.go` | AL0 (0) |
| `internal/runner/agentenv.go`, its new test | AL2 (0) |
| `internal/cli/run.go`, `init_policy_publish.go`, `validate.go`, `project_cmd.go` | AL1 (1) |
| `internal/infra/spec.go`, its new test | AL1 (1) |
| `internal/cli/accounts_alias.go`, `accounts.go`, `accounts_rm.go` | AL3 (2) |
| `internal/settings/registry.go`, `schemas/settings.schema.json`, `website/content/cli-reference.md` | AL3 (2) |
| `docs/project-claude-accounts.md`, `docs/multi-model.md`, `plugin/skills/*` | AL4 (3) |

No level holds two writers of one file.

## For operators

None. No format moves, nothing to migrate, and the release note for the release that ships AL3 is the
usual "run `fugaro update`", which asks nothing new.

## Later (not tasks)

- Craft access naming an alias, after model-crafts MC5 (D2080).
- A follow-up that follows a repoint, if D2078 is answered (b).
- An alias field in the run record, if D2079 is answered (b).
