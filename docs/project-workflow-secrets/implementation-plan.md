# The project workflow secret: implementation plan

**Status: a design, and the build has landed.** Every task below is written to an **answered decision**
in [reconciliation.md](reconciliation.md) §4: the owner answered **all eighteen on 2026-11-04, all as
recommended** — **D1260** (`workflow_secrets`), **D1266** (refuse) and **D1267** (untouched) by name,
and the other fifteen as recommended — so **the build is the 0.19.0 theme (D1277)**: the tasks
**W0–W6 are on `main`**, and W7 is the folder's own status lines. **Where this plan and
[reconciliation.md](reconciliation.md) disagree, the reconciliation wins**; where a task and the
reconciliation disagree, the reconciliation wins.

Each task touches at most twelve files, names its tests, starts with a **red step**, and carries a
**SEC** flag where it moves a security boundary — and six of the eight carry one, because the whole
design is a gate on which repositories may mount a credential the operator stored once.

Slot: **CLI 0.19.0** (D1277). Nothing here is a console, mobile, website or `ee` change, and no task
edits `deploy/terraform/**`, `internal/credsvc/**`, `docs/secrets-brokering/**` or
`.github/workflows/**`.

## 1. What this plan does not own

- **`deploy/terraform/`.** No task edits a `.tf` file: the container, the labels, the per-repository
  grant and the mount already carry any logical name (reconciliation F2, F7). A task that finds
  itself needing a `.tf` line has misread the design and **stops and reports**.
- **`internal/credsvc/`, `internal/creds/` and `docs/secrets-brokering/`.** D1267 leaves the bundle
  and the vault fetch alone, and F11 shows the brokering guard needs no new row. When B3.1 (S3) is
  scheduled, **its** task adds the sentence about reading the same container; no task here writes it.
- **The value-handling contract of `fugaro secrets set`.** stdin-only, the hidden prompt, the length
  bounds, the trailing-newline rule, the refusal to take a value from an argument or a flag: W3
  touches the *name* and the *scope* decision and nothing else.
- **The reserved names' own rules.** D2's sweep, `PerRepositorySecret`, the provider convention, the
  two harness OAuth namespaces and the `creds-bundle` refusal are read by W0 and amended by none.
- **`fugaro managed`, its gate, the delete list and the run record.** Another design's
  (D1011–D1040). This design adds no command, so the gate's command set does not change.
- **The shared project job path, and `internal/projectpolicy`.** D1266 refuses it, and the refusal is
  the deployer's: `internal/infra/projectjobs.go`'s `projectJobSecrets` already refuses every profile
  secret `projectSecretEnv` cannot map. W5 **pins** that fence; no task makes a profile honour a
  per-repository list, and **no task edits `internal/projectpolicy/`** — a rule written there reads
  a list it never sees and would fail open.
- **The live checks.** No task adds, runs or edits one, and `docs/gcp-live-checklist.md` is
  untouched. Every test here is hermetic (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`), and
  the 65-repository scale the issue names is not reproduced by any of them.
- **The config-management work's G8** (`fugaro secrets rm` and the secret policy flags, D1171, PR
  #1691). That design owns the command; this one depends on it and extends it to the new block (§2,
  W3). No task here writes `rm` from scratch, and no task here may land before it.

## 2. Order, and what forces it

**The `Needs` column is a code dependency: what must be merged for that task to build, test and be
reviewed on its own.**

| Level | Tasks | Needs (code) |
| --- | --- | --- |
| 0 | **W0** | nothing |
| 1 | **W1** | W0 |
| 2 | **W2** | W0, W1 |
| 3 | **W3**, **W4**, **W5** | W0–W2 for W3, and **G8 merged**; W2 for W4; W1–W2 for W5 |
| 4 | **W6** | W3 (the pinned help sentences) |
| 5 | **W7** | W0–W6 |

Three ordering facts are load-bearing:

- **W0 lands the block and its key rule before anything reads it.** Every later task asks
  `config.ValidateWorkflowSecrets` or the block's map; a task that opened the block before its
  refusals existed would be a config path that accepts a reserved name as a workflow secret — the one
  state D1261 exists to make unreachable.
- **W2 lands the eligibility answer before the CLI stores by it.** `ProjectSecretCandidate` is the
  one function the store path, the stage, the listing and the doctor checks share (F3), and a store
  path shipped first would write containers the plan never mounts, which is the drift
  `initsecrets.go:414`'s own comment names.
- **W3 lands before W6** because W6's docs test pins the help text and the store-time note W3
  writes, and a documentation task that guessed those sentences would pin nothing.
- **W3 lands after G8** — the config-management work's `fugaro secrets rm` and the secret policy
  flags (PR #1691, `internal/cli/secrets_rm.go`) — because both tasks edit `internal/cli/secrets.go`
  and because `rm` is the command that has to withdraw the new block's entry: a `rm` that knows only
  `project_secrets.<name>` would delete the container and leave `workflow_secrets.<name>` standing,
  which is the pair D1171 exists to remove together. Landing W3 first makes that a conflict someone
  resolves by hand; landing it after makes it a task W3 owns.

**Critical path: W0 → W1 → W2 → W3 → W6 → W7** (six tasks). W4 and W5 are off it: W4 is tests over
behaviour W2 already gives, and W5 is a test over behaviour W1 and W2 have given — the fence it pins
is `internal/infra`'s, and it needs the field and the container before the name it refuses can exist
in a local config at all.

## 3. Gates (every task)

- `go build ./... && gofmt -l . && go vet ./...` — gofmt prints nothing.
- The task's named tests, `GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1 -timeout 40m`.
  **Never the whole `internal/cli` or `internal/runner` package.**
- The **wider pin gate**: for every file the task changes or adds,
  `grep -rln '<its base name>' --include='*_test.go' internal/ cmd/ scripts/ deploy/ schemas/ plugin/ ee/ images/`,
  and each hit's tests run with a `-run` filter naming them. Any doc touched:
  `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m`.
- `go test ./internal/eeboundary/... -count=1`, and — for W6, which edits a skill —
  `go test ./plugin/ -run 'Test' -count=1`, whose `skillcli_test.go` checks every command and flag a
  skill names against the CLI.
- **A test this plan adds that fails locally stops the task**: never push over it, never weaken, skip
  or delete it. Report it.
- **No new dependency** (`go.mod` unchanged), no TODO comment, no section-header comment, no comment
  that restates a name.

## 4. The shared-files table

Files more than one task touches, and the rule for each. **A task that needs a file in this table
that another task owns adds its edit only after that task has merged**, and never re-derives what the
owner landed.

| File | Tasks | What each one does with it |
| --- | --- | --- |
| `internal/config/project_secrets.go` | **none** | **Untouched.** D1260's whole argument is that this block's key rule is not weakened; W0 adds a sibling file in the same package and reads these helpers (`invalidAllowDataToEntry`, `matchRepoPattern`) without editing them. A task that finds itself editing this file has chosen D1260's first alternative by accident and **stops and reports** |
| `internal/localcfg/localcfg.go` | W1 | One field and one validation call, beside `ProjectSecrets` (`:186`, `:1605`) |
| `internal/localcfg/shared.go` | W1 | The publish path drops the new block, the way it drops `ProjectSecrets` (`:79`) — an operator-local allow-list never reaches the shared config |
| `internal/infra/spec.go` | W2 | `ProjectSecretCandidate` (`:1374`) gains the block as a source through `config.AllowedForProjectSecret`'s shape. It is the only eligibility edit |
| `internal/infra/tfvars.go` | W2 | `projectSecretContainers` (`:852`) derives the container from the new block's entries |
| `internal/cli/secrets.go` | **G8**, then W3 | G8 adds the secret policy flags (`--allow-data-to`, `--proxy`); W3 adds the store path: `projectWideAccepted`, `projectWideRefusal`, `projectWideHint`, `projectScopeName`, `projectSecretAllowed`, and the `set` help (`:308`) and store-time note (`:859`). Two tasks, one file, and **W3 lands second** |
| `internal/cli/secrets_rm.go` | **G8**, then W3 | G8's file, on an open branch (#1691): `fugaro secrets rm NAME` deletes the container and removes the `project_secrets.NAME` entry. W3 extends it to the new block once G8 has merged — `workflow_secrets.<name>` is the key `rm` drops for a project-scoped workflow secret, and `secretsRmWhat`'s sentence, the dry run's `key` and the `rm` help all name the block the entry lives in |
| `internal/settings/registry.go` + `schemas/settings.schema.json` | W0 | One row and one key for `workflow_secrets.*.allow_data_to` (F13), the row's **Owner** being `secrets` — the family `project_secrets.*.allow_data_to`'s row names and `--allow-data-to` writes — plus the count pins their own tests hold: the registry's row count is pinned by another folder's docs test (`internal/cli/docs_config_management_test.go`'s 323, and `docs/config-management/README.md:47`'s figure), so W0 moves that figure in the same commit rather than leaving a red test in a folder it does not own |
| `SECURITY.md` | W6 | Two sentences amended (`:28`, `:38`) and one section extended (D1273) |
| `docs/project-workflow-secrets/` | W7 | W7 is the only task that edits this folder, and it changes a *status* line, never a decision |
| `plugin/skills/setup/SKILL.md` | W6 | One sentence at `:130`; no command or flag is added, so `plugin/skillcli_test.go` should pass unchanged — if it does not, W6 reports it rather than widening the skill |

## W0. `internal/config`: the block, its type and its refusals (D1260, D1261, D1265, D1275, D1276). **SEC**

Without it every later task would invent its own key rule, which is the drift D1261 refuses.

- **Interface.** One new file, `internal/config/workflow_secrets.go`: `ValidateWorkflowSecrets(secrets
  map[string]ProjectSecret) []Problem` and `AllowedForWorkflowSecret(secrets, name, repo) bool`, the
  second `AllowedForProjectSecret`'s body over the new map. The type is **`ProjectSecret`**, reused
  rather than copied, so `AllowsData`, `matchRepoPattern` and `invalidAllowDataToEntry` are one
  implementation for both blocks. The key rule is D1261's list, and **the function holds only the half
  it can compute: the rules that are a function of the name alone** — `config.ReservedSecrets`, the
  `claude-oauth-token` prefix namespace, `CodexOAuthSecret` and `CredentialsBundleSecret`, which is
  the set `internal/config/validate.go:264`–`:302` already refuses for a name in a repository's own
  workflow. The two rules that read the operator's configured providers cannot be written here (the
  signature takes the block and nothing else, and there is no `lc.Providers` to ask) and are **W1's**,
  where the map is in hand. The `proxy` refusal is D1275's, worded with `ValidateProjectSecrets`'s own
  sentence shape; an entry rule is `invalidAllowDataToEntry`'s, unchanged.
- **Red step.** `TestWorkflowSecretRefusesAReservedName` (each of `config.ReservedSecrets`' four,
  `claude-oauth-token-x`, `codex-oauth-token`, `creds-bundle` — each refused, and each refusal naming
  the entry), which fails to compile against a package with no such function. Then
  `TestWorkflowSecretRefusesTheProxySwitch`, `TestWorkflowSecretRefusesABareStar` and
  `TestWorkflowSecretAcceptsASegmentWildcard` (the entry rules are `project_secrets`' own, held here
  again so a later edit to one block cannot change the other silently). The names a **configured**
  provider holds are not this task's red: `ValidateWorkflowSecrets` cannot see them, and their test
  belongs to W1, where the cross-block half of the key rule is written.
- **Files (≤7).** `internal/config/workflow_secrets.go`, `internal/config/workflow_secrets_test.go`,
  a registry row in `internal/settings/registry.go` and its test's own key map, the schema key, the
  registry's row count in `internal/cli/docs_config_management_test.go` and the figure in
  `docs/config-management/README.md` (§4: the count is pinned in a folder this plan does not own), and
  `internal/config/project_secrets.go` **only if** a helper must be exported — the recommendation is
  that it must not be.
- **Not in this task:** the local config's field and its cross-block refusals (W1), the plan-time
  predicate (W2), any CLI wording (W3).

## W1. `internal/localcfg`: the block, its parse and its validation (D1260, D1263). **SEC**

- **Interface.** One field, `WorkflowSecrets`, a map from a secret's name to a
  `config.ProjectSecret` under the yaml key `workflow_secrets`, validated through
  `config.ValidateWorkflowSecrets` beside `ValidateProjectSecrets`'s call
  (`localcfg.go:1605`), and dropped by `localcfg/shared.go:79`'s publish path the way
  `ProjectSecrets` is. Strict decoding already refuses an unknown field, so a misplaced block is an
  error rather than a silent ignore.
- **The cross-block half of the key rule is this task's, because this is where `Providers` is
  visible.** W0's `ValidateWorkflowSecrets(secrets)` holds the name-only rules; the two that read the
  operator's configured providers are checked **here**, beside the `ValidateWorkflowSecrets` call in
  `validate()` (`localcfg.go:1605`), where `lc.Providers` and `lc.ClaudeAccounts` are in hand: a key
  that is a configured provider's own `secret:` (`config.ProviderBySecret`) or the conventional name
  `config.ReservedProviderSecret` derives from the provider map, refused for the same reason
  `CheckWorkflowProviderSecrets` refuses them in a repository's own workflow. The shape to copy is
  that function (`internal/config/providers.go:779`, which takes the providers map as an argument and
  is called from the layer holding it), not a second rule invented here; the refusal names the entry
  and never a value.
- **Where it belongs.** Operator-local: never `fugaro.yaml`, never the project layer, never the
  shared config, never published. The field's comment states the reach in the vocabulary
  `ProjectSecrets`' own comment uses, and links this folder.
- **Red step.** `TestWorkflowSecretsAreRefusedInTheSharedConfig` (a shared config carrying the block
  loses it, as `ProjectSecrets` does) and `TestLocalConfigRefusesAReservedWorkflowSecretName` (the
  W0 rule, reached through the real parse rather than the validator directly) — both failing against a
  struct with no such field. Then the cross-block pair, which cannot be reached at all today because
  the field does not exist: `TestLocalConfigRefusesAProvidersOwnSecretAsAWorkflowSecretKey` (a
  `workflow_secrets` key that is a configured provider's `secret:`) and
  `TestLocalConfigRefusesAProvidersConventionalNameAsAWorkflowSecretKey` (a key the convention gives
  that provider, whatever its `secret:` is spelled as) — both refused, with the entry named and no
  value echoed. They are the tests that would have passed vacuously in W0.
- **Files (≤6).** `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go`, the two test files
  above, and `internal/cli/config_list.go`'s output with `config_list_test.go` if the listing
  enumerates the key set (it names `project_secrets` today, so it owes the new block a line).

## W2. `internal/infra`: eligibility and the container (D1262, D1263, D1264). **SEC**

The one function every reader of the decision shares (F3), plus the container the plan derives.

- **Interface.** `ProjectSecretCandidate` (`spec.go:1374`) gains one source:
  `config.AllowedForWorkflowSecret(lc.WorkflowSecrets, logical, repo)` — with **no sweep for a workflow secret**, so the entry
  is the whole gate (D1262: absent or empty means nobody). It is consulted **only for a name no other source claims**: `config.AllowedForProjectSecret` (the `project_secrets` entry), the D2 sweep,
  `config.ClaudeAccountBySecret` and `config.ProviderBySecret` answer first, and the new block is the
  last question asked, so a hand-written entry can never widen a name those sources already answer
  for — the predicate fails closed. `projectSecretContainers` (`tfvars.go:852`) iterates the new
  block's entries with a non-empty `allow_data_to`, exactly as it iterates `lc.ProjectSecrets` today
  (D1263). `resolveSecretID` is **read and never edited** (D1264).
- **`projectSecretEnv` (`projectjobs.go:517`) is not widened to the new block, and this is
  load-bearing rather than tidy.** Once `projectSecretContainers` derives a container for the entry,
  the deployer's first check on a profile secret (`projectJobSecrets`, `projectjobs.go:426`) stops
  firing for that name; what refuses a shared job the value is the second one (`:429`), which asks
  `projectSecretEnv`. If this task taught that function a workflow secret's variable, a profile naming
  the name would mount a value whose allow-list is per repository on a job every repository the
  profile admits runs — D1266's whole subject, undone by a line in a function this task owns. W5 pins
  it.
- **Red step.** `TestProjectSecretCandidateTakesAWorkflowSecret` (a repository the entry lists gets
  `gcp.ProjectSecretID(name, logical)`) and `TestProjectSecretCandidateRefusesAnUnlistedRepository`
  (an entry that names another repository returns `"", false` — the issue's own guarantee), both
  failing against the predicate as it stands. Then
  `TestWorkflowSecretHasNoProjectScopeDefault` (a workflow secret with **no** entry is not eligible,
  which is the assertion that keeps D1262 from being quietly reversed later),
  `TestWorkflowSecretEntryCannotWidenAReservedName` (an entry whose key is one the sweep or a provider
  already answers for leaves that source's answer alone — the predicate's order, held so a later edit
  cannot put the new block first), and `TestProjectSecretContainerIsDerivedFromTheEntry`. The issue's
  own guarantee already has a home at
  the layer a run reads: `projectsecret_coverage_test.go`'s
  `TestProjectSecretCoverageUnlistedRepoIsNotCovered` gains the workflow-secret case, so the listing
  and `Readiness` cannot drift on it either.
- **Files (≤6).** `internal/infra/spec.go`, `internal/infra/tfvars.go`, `internal/infra/spec_test.go`,
  `internal/infra/secretmounts_test.go`, `internal/infra/projectsecret_coverage_test.go`, and — only
  if the container's labels need a test of their own — a case in `internal/infra/discover_test.go`
  holding the adoption of a container the store path created first (F16).
- **Not in this task:** the repository's own copy winning (D1264) is `resolveSecretID`'s existing
  behaviour; the task adds the test that holds it for a workflow secret and no line of code.

## W3. `internal/cli/secrets.go`: the store path, the two sentences and the `rm` half (D1265, D1273). **SEC**

- **Rules.** `projectWideAccepted` gains branch 1's shape for the new block (an entry present with a
  non-empty `allow_data_to`), **not** a new branch at the end: a workflow secret is refused with
  `projectWideRefusal`'s sentence naming `workflow_secrets.<name>.allow_data_to`, and a
  present-and-empty entry is refused with `projectWideHint`'s second form. `projectScopeName`
  (`:666`) answers through `ProjectSecretCandidate` as it does today, so the bare command from a
  checkout stores project-wide exactly when the entry lists that checkout's repository.
  `projectSecretAllowed` (`:1145`) gains the new block as its first source for the ALLOWED column.
  The `set` help (`:308`) and the store-time note (`:859`) get D1273's reach sentence: the note names
  the reach through `repoMayUseProject`, never a fixed "every enrolled repository".
- **G8, and what `rm` owes the new block.** `fugaro secrets rm NAME` (the config-management work's,
  PR #1691) already accepts a workflow secret's name — its name check is
  `config.SecretNameRE` — and already deletes the right container, because the container's ID is
  name-agnostic (F2). What it does **not** know is the new block, so the task extends it: for a
  project-scoped workflow secret `rm` removes `workflow_secrets.<name>`, not `project_secrets.<name>`,
  and says so in the three places that name the entry — `secretsRmWhat`'s confirmation sentence, the
  dry run's `key`, and the `rm` help's "stored once for the whole project (`project_secrets.NAME`)".
  Deleting the container and leaving the entry standing is the failure D1171's own comment says the
  command exists to prevent, and it is the same failure with the new block's key. **W3 lands after G8**
  (§2), so this is an extension of a merged file, not a conflict.
- **G8's other flag, `--allow-data-to`: it writes `workflow_secrets.<name>.allow_data_to`.** The two
  readings were "**it stays refused for the new block**" and this one, and the plan takes **this
  one**, for three reasons. The issue asks for a gate "like `project_secrets.<name>.allow_data_to`"
  (spec.md), and that list's writer is this flag: leaving the new block out would give the operator
  one mechanism with two spellings, one of them hand-edited. The registry row's Owner decides the
  writer (`settings_set.go:43`: a row a family owns is refused by the generic path so that two paths
  cannot write one key), and `workflow_secrets.*.allow_data_to`'s Owner is `secrets` (W0) — so with
  the flag refusing as well, the key would have no command writer at all and each refusal would point
  at the other path. And the security work is already there rather than new: naming one more
  repository is a **widening**, classified by the same settings write path every other widening pays —
  the project's own name at a terminal, or `--yes` outside a coding agent's session — so a session is
  refused, which is the property the task has to pin. **The outright refusal is G8's, and W3 keeps
  it.** Beside the typed-widening path, G8 refuses a secret's policy write in a coding agent's session
  outright — `internal/cli/secrets.go:747` on G8's branch is the line, and that branch's
  `TestSecretsSetPolicyRefusesAnAgentSession` is the test, neither of them readable here until G8
  merges — and `--yes` does not lift it. W3 writes the new block's entry through that same call and
  adds no route of its own, because a second path into one key is the door that refusal exists to
  close. What the task owes, therefore: the name check
  answers for either block (a `config.ReservedSecrets` name writes `project_secrets.<name>`; any
  other name writes `workflow_secrets.<name>` **when the new block holds it**; a name in neither is
  refused by G8's existing key-rule refusal, and this task points that refusal at the right block for
  a workflow secret); `--proxy` stays refused for a workflow secret, because D1275 refuses the field
  in the new block entirely and a flag that wrote one would be the spelling that refusal exists to
  prevent; and the red test below shows a session turned away.
- **What must not change.** The value path, the name-vs-value refusals, `CheckSecretName`, the
  `--repo` path's `declares` check, and the precedence note's `repoMayUseProject` call. `rm`'s two
  halves stay together — one diff, one confirmation, the deletion last — and its note about a
  repository's own copy winning is D1264's fact and is not amended.
- **Red step.** `TestSecretsSetRefusesAWorkflowSecretWithoutAnEntry` (today's sentence, kept word for
  word for a name with no entry) and `TestSecretsSetStoresAWorkflowSecretTheEntryLists` (a checkout
  whose origin the entry lists, with no flag, writes the project container) — the first passes today
  and the second cannot compile, so the task's red is the pair, not one test. Then
  `TestSecretsSetRefusesAWorkflowSecretForAnUnlistedRepository` (the bare command in a checkout the
  entry excludes writes the repository's own copy and says so), and
  `TestSecretsSetNeverStoresTheValueAsAnArgument` (unchanged behaviour, held so this task cannot
  weaken it by accident). For the `rm` half, `TestSecretsRmRemovesTheWorkflowBlockEntry` (a project
  container and a `workflow_secrets.<name>` entry: `rm` deletes the container **and** drops that key,
  and the dry run's `key` reads `workflow_secrets.<name>`) fails against G8's `rm`, which drops
  `project_secrets.<name>`. For the flag, `TestSecretsSetAllowDataToWritesTheWorkflowBlockEntry` (a
  name the new block holds: the flag writes `workflow_secrets.<name>.allow_data_to`, and the
  confirmation prints the list the entry ends up with rather than the flag's own value, as G8's does
  for a reserved name) and `TestSecretsSetAllowDataToOnAWorkflowSecretRefusesAnAgentSession` — the
  test the widening owes, written first and red against G8, where the name is one `project_secrets`
  cannot hold. Then `TestSecretsSetProxyStaysRefusedForAWorkflowSecret`, which is D1275 read through
  the flag.
- **Files (≤8).** `internal/cli/secrets.go`, `internal/cli/secrets_test.go`,
  `internal/cli/secrets_rm.go`, `internal/cli/secrets_rm_test.go`, a new
  `internal/cli/secrets_policy_workflow_test.go` if the flag's two tests do not belong beside G8's in
  `secrets_policy_test.go`, and the pty and bitbucket-text test files the help text's edits touch
  (`secrets_pty_test.go`, `secrets_bitbucket_text_test.go`), named at implementation time by the pin
  gate.
- **SEC.** It is the write path for a credential's project copy. Its refusals must be fail-closed on
  the three shapes D1261 names: an unknown name, a reserved name, and an entry that is present and
  empty.

## W4. `internal/cli/initsecrets.go`: the stage's tests (D1269)

- **Interface.** **No code.** `find()` (`:447`) and `projectScope` (`:424`) already answer through
  `ProjectSecretCandidate` and the project listing, so a covered workflow secret is not prompted for
  and is stored project-wide once W2 has landed. This task exists so that "no code" is a **tested**
  claim rather than an assumption — and so that a later edit to the stage cannot quietly reintroduce
  the 130 prompts.
- **Red step.** `TestInitDoesNotPromptForAProjectWorkflowSecret` — a fake Secret Manager holding the
  project container for a name the entry lists, with nothing in the repository's own container, and
  the stage reports it covered. It is **red before W2 and green after**, which is the dependency the
  level table records.
- **Files (≤4).** `internal/cli/initsecrets_test.go`, `internal/cli/init_repo_project_secret_test.go`,
  and the stage's own fixtures. If the stage needs a line of code to pass, that is a **finding to
  report**, not a line to write: the design says this task is tests alone.

## W5. `internal/infra/projectjobs_test.go`: the deployer's fence, pinned (D1266). **SEC**

- **Interface.** **No code, and no file under `internal/projectpolicy/`.** The refusal a shared job
  needs already exists and is where it must be: `projectJobSecrets` (`projectjobs.go:419`) refuses
  every `profiles.<p>.secrets` name that `projectSecretEnv` (`:517`) cannot map to a variable, and
  `projectSecretEnv` maps only `config.ReservedSecrets` and a configured provider's own secret — so a
  workflow secret is refused **today**, on the operator's own machine, where the entry's list is in
  hand. Writing the rule in `projectpolicy` instead, beside the Claude OAuth token's refusal
  (`profiles.go:322`), is the reading this task exists to close off: `CheckProfiles`
  (`internal/projectpolicy/profiles.go:560`) takes the policy's profiles and accounts and never the
  local config, so the rule would be asked where the list is absent and the profile would be admitted
  — a refusal that fails open, and that reads as enforced.
- **What it pins.** The exact path a workflow secret takes today: `projectSecretContainers` derives
  the container from the entry (W2), and the name is then refused by `projectSecretEnv`. The test is
  therefore also the one that fails if W2 widens either function, which is why it is a task and not a
  sentence in §1.
- **Red step.** `TestProfileRefusesAProjectWorkflowSecret`, **via `ProjectJobs`**: a local config
  whose `workflow_secrets` block holds a name (an entry listing one repository) and a profile whose
  `secrets` lists that name is refused, through the real deployer path, and the refusal names the
  profile entry and never a value. It cannot compile before W1's field, and it is the assertion that
  fails the moment either half of the fence is widened. Then `TestProfileStillAcceptsAProviderKey`
  and `TestProfileStillAcceptsAReservedName`, which keep the refusal from widening to the names a
  shared job legitimately mounts today — and the neighbouring
  `TestProjectJobSecretsMustBeProjectWide`'s own backstop ("no local config can reach it") is
  amended, not deleted: after W2 a local config **does** reach `projectSecretEnv` with a workflow
  secret's name, which is the state the two tests together describe.
- **No policy version bump, and no published document.** The node's shape does not change, and D1268
  publishes nothing; this task writes no `internal/projectpolicy/` line at all.
- **Files (≤3).** `internal/infra/projectjobs_test.go`, and whichever of the package's tests pin the
  container map and the env resolver (`internal/infra/tfvars_test.go` or `projectjobs_test.go`
  itself), named by the pin gate.

## W6. The documents the change cannot ship without (D1272, D1273). **SEC**

The feature's reach is invisible in a repository's own `fugaro.yaml`, so the files below are where a
person decides whether storing one value for sixty-five repositories is safe.

- **Edits.** `SECURITY.md:28` (the "no project-wide meaning" clause gains the new block),
  `SECURITY.md:38` (`workflows.<n>.secrets` **can** be shared this way, through the operator's own
  entry), a new paragraph in the same section carrying D1273's sentence (the wildcard's reach, the
  repo-wins rule, and the fact that the value is what the repository's build and tests read),
  `docs/project-layer.md:244` (the parenthesis that says a name with a project-wide meaning cannot be
  shared through a workflow's block), `docs/design/project-secrets.md` §4 item 4 (a pointer to this
  folder, not a rewrite — that document's §4 *Decision: storage*, the fourth point of "Who writes a
  version"), and `plugin/skills/setup/SKILL.md:128` (a covered workflow secret is not prompted for
  either).
- **Red step.** Extend `internal/cli/docs_project_wide_git_test.go`'s discipline in a new
  `internal/cli/docs_project_workflow_secrets_test.go` — which W7 finishes — with the two sentences
  this task writes, so the docs cannot land without them. The test is **written here and completed in
  W7**; the shared-files table says so.
- **Files (≤6).** The five documents above plus the docs test file.
- **SEC.** It is the sentence a reader uses before typing the command. A reach that is documented
  wrongly is worse than one that is undocumented, because it is believed.

## W7. This folder's status lines, and the census (D1277)

- **Edits.** Each of this folder's four documents' status lines, from "a design, and nothing is
  built" to what the build actually shipped: the answers the owner gave are already recorded here and
  the task restates the outcome, never a decision's content. The release notes'
  `### For operators` section — which for this build is **"run `fugaro update`"** and nothing else,
  because the feature adds no operator step, no migration and no policy version — or, if W1's field
  turns out to need one, the step it names ([docs/releases/README.md](../releases/README.md)).
- **The census.** `docs/backlog/README.md` reads **2 items: 2 open** and this design files none; a
  task that finds an engine-filed item here deletes it and restores the index from `main` rather than
  regenerating it.
- **Red step.** None — this task writes no behaviour. Its gate is the pair of commands the plan's
  brief names, run last: `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m`.
- **Files (≤6).** `docs/project-workflow-secrets/README.md`, `spec.md`, `reconciliation.md`,
  `implementation-plan.md`, the release notes file, and `website/data/release-summaries.yaml` if and
  only if the release script's own rules ask for a line.

## 5. What the build does not do

- It adds **no command and no flag**, so the CLI's command surface and the plugin's command check are
  unchanged.
- It changes **no wire format**: `secrets ls`'s `--json` document keeps its two fields and gains no
  value (F17), and no version is bumped.
- It **publishes nothing**: no policy version, no shared-config key, no schema version (D1268).
- It touches **no Terraform**, so no installation has anything to converge beyond the container the
  first store creates (D1263).
- It **weakens no existing refusal.** `projectSecretEnv` keeps mapping only the reserved names and a
  configured provider's secret, so the deployer's fence keeps refusing a shared job that names a
  workflow secret (D1266, W5); `projectpolicy` is not edited at all; and the eligibility predicate
  answers for the new block only where no earlier source does (W2).
- It **promises nothing about a hostile run**: the allow-list decides which jobs mount a value, not
  what a run does with it (reconciliation §6).
