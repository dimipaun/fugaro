# The project workflow secret: implementation plan

**Status: a design, and nothing is built, and nothing here is approved.** Every task below is written
to a **recommendation** in [reconciliation.md](reconciliation.md) §3, and **no decision is answered**:
the tasks exist so the owner can price the build, not because it may start. **Where this plan and
[reconciliation.md](reconciliation.md) disagree, the reconciliation wins**; where a task and the
reconciliation disagree, the reconciliation wins.

Each task touches at most twelve files, names its tests, starts with a **red step**, and carries a
**SEC** flag where it moves a security boundary — and five of the eight carry one, because the whole
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
- **The shared project job path.** D1266 refuses it; W5 writes the refusal. No task makes a profile
  honour a per-repository list.
- **The live checks.** No task adds, runs or edits one, and `docs/gcp-live-checklist.md` is
  untouched. Every test here is hermetic (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`), and
  the 65-repository scale the issue names is not reproduced by any of them.

## 2. Order, and what forces it

**The `Needs` column is a code dependency: what must be merged for that task to build, test and be
reviewed on its own.**

| Level | Tasks | Needs (code) |
| --- | --- | --- |
| 0 | **W0** | nothing |
| 1 | **W1** | W0 |
| 2 | **W2**, **W5** | W0, W1 for W2; W0 for W5 |
| 3 | **W3**, **W4** | W0–W2 for W3; W2 for W4 |
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

**Critical path: W0 → W1 → W2 → W3 → W6 → W7** (six tasks). W4 and W5 are off it: W4 is tests over
behaviour W2 already gives, and W5 is a refusal in a package nothing else in this plan imports.

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
| `internal/infra/spec.go` | W2 | `ProjectSecretCandidate` (`:1344`) gains the block as a source through `config.AllowedForProjectSecret`'s shape. It is the only eligibility edit |
| `internal/infra/tfvars.go` | W2 | `projectSecretContainers` (`:852`) derives the container from the new block's entries |
| `internal/cli/secrets.go` | W3 | The store path: `projectWideAccepted`, `projectWideRefusal`, `projectWideHint`, `projectScopeName`, `projectSecretAllowed`, and the `set` help (`:308`) and store-time note (`:859`) |
| `internal/settings/registry.go` + `schemas/settings.schema.json` | W0 | One row and one key for `workflow_secrets.*.allow_data_to` (F13), plus the count pins their own tests hold |
| `SECURITY.md` | W6 | Two sentences amended (`:28`, `:38`) and one section extended (D1273) |
| `docs/project-workflow-secrets/` | W7 | W7 is the only task that edits this folder, and it changes a *status* line, never a decision |
| `plugin/skills/setup/SKILL.md` | W6 | One sentence at `:130`; no command or flag is added, so `plugin/skillcli_test.go` should pass unchanged — if it does not, W6 reports it rather than widening the skill |

## W0. `internal/config`: the block, its type and its refusals (D1260, D1261, D1265, D1275, D1276). **SEC**

Without it every later task would invent its own key rule, which is the drift D1261 refuses.

- **Interface.** One new file, `internal/config/workflow_secrets.go`: `ValidateWorkflowSecrets(secrets
  map[string]ProjectSecret) []Problem` and `AllowedForWorkflowSecret(secrets, name, repo) bool`, the
  second `AllowedForProjectSecret`'s body over the new map. The type is **`ProjectSecret`**, reused
  rather than copied, so `AllowsData`, `matchRepoPattern` and `invalidAllowDataToEntry` are one
  implementation for both blocks. The key rule is D1261's list and nothing else; the `proxy`
  refusal is D1275's, worded with `ValidateProjectSecrets`'s own sentence shape; an entry rule is
  `invalidAllowDataToEntry`'s, unchanged.
- **Red step.** `TestWorkflowSecretRefusesAReservedName` (each of `config.ReservedSecrets`' four, a
  configured provider's convention name, `claude-oauth-token-x`, `codex-oauth-token`, `creds-bundle`,
  and the name a provider's own `secret:` holds — each refused, and each refusal naming the entry),
  which fails to compile against a package with no such function. Then
  `TestWorkflowSecretRefusesTheProxySwitch`, `TestWorkflowSecretRefusesABareStar` and
  `TestWorkflowSecretAcceptsASegmentWildcard` (the entry rules are `project_secrets`' own, held here
  again so a later edit to one block cannot change the other silently).
- **Files (≤7).** `internal/config/workflow_secrets.go`, `internal/config/workflow_secrets_test.go`,
  a registry row in `internal/settings/registry.go` and its test's own key map, the schema key, and
  `internal/config/project_secrets.go` **only if** a helper must be exported — the recommendation is
  that it must not be.
- **Not in this task:** the local config's field (W1), the plan-time predicate (W2), any CLI wording
  (W3).

## W1. `internal/localcfg`: the block, its parse and its validation (D1260, D1263). **SEC**

- **Interface.** One field, `WorkflowSecrets`, a map from a secret's name to a
  `config.ProjectSecret` under the yaml key `workflow_secrets`, validated through
  `config.ValidateWorkflowSecrets` beside `ValidateProjectSecrets`'s call
  (`localcfg.go:1605`), and dropped by `localcfg/shared.go:79`'s publish path the way
  `ProjectSecrets` is. Strict decoding already refuses an unknown field, so a misplaced block is an
  error rather than a silent ignore.
- **Where it belongs.** Operator-local: never `fugaro.yaml`, never the project layer, never the
  shared config, never published. The field's comment states the reach in the vocabulary
  `ProjectSecrets`' own comment uses, and links this folder.
- **Red step.** `TestWorkflowSecretsAreRefusedInTheSharedConfig` (a shared config carrying the block
  loses it, as `ProjectSecrets` does) and `TestLocalConfigRefusesAReservedWorkflowSecretName` (the
  W0 rule, reached through the real parse rather than the validator directly) — both failing against a
  struct with no such field.
- **Files (≤5).** `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go`, the two test files
  above, and `internal/cli/config_list.go`'s output with `config_list_test.go` if the listing
  enumerates the key set (it names `project_secrets` today, so it owes the new block a line).

## W2. `internal/infra`: eligibility and the container (D1262, D1263, D1264). **SEC**

The one function every reader of the decision shares (F3), plus the container the plan derives.

- **Interface.** `ProjectSecretCandidate` (`spec.go:1344`) gains one source: `config.AllowedForWorkflowSecret(lc.WorkflowSecrets, logical, repo)`, read **before** the D2 sweep and with **no
  sweep for a workflow secret** (D1262 — absent or empty means nobody). `projectSecretContainers`
  (`tfvars.go:852`) iterates the new block's entries with a non-empty `allow_data_to`, exactly as it
  iterates `lc.ProjectSecrets` today (D1263). `resolveSecretID` is **read and never edited** (D1264).
- **Red step.** `TestProjectSecretCandidateTakesAWorkflowSecret` (a repository the entry lists gets
  `gcp.ProjectSecretID(name, logical)`) and `TestProjectSecretCandidateRefusesAnUnlistedRepository`
  (an entry that names another repository returns `"", false` — the issue's own guarantee), both
  failing against the predicate as it stands. Then
  `TestWorkflowSecretHasNoProjectScopeDefault` (a workflow secret with **no** entry is not eligible,
  which is the assertion that keeps D1262 from being quietly reversed later), and
  `TestProjectSecretContainerIsDerivedFromTheEntry`.
- **Files (≤5).** `internal/infra/spec.go`, `internal/infra/tfvars.go`, `internal/infra/spec_test.go`,
  `internal/infra/tfvars_test.go`, and — only if the container's labels need a test of their own —
  a case in `internal/infra/discover_test.go` holding the adoption of a container the store path
  created first (F16).
- **Not in this task:** the repository's own copy winning (D1264) is `resolveSecretID`'s existing
  behaviour; the task adds the test that holds it for a workflow secret and no line of code.

## W3. `internal/cli/secrets.go`: the store path and the two sentences (D1265, D1273). **SEC**

- **Rules.** `projectWideAccepted` gains branch 1's shape for the new block (an entry present with a
  non-empty `allow_data_to`), **not** a new branch at the end: a workflow secret is refused with
  `projectWideRefusal`'s sentence naming `workflow_secrets.<name>.allow_data_to`, and a
  present-and-empty entry is refused with `projectWideHint`'s second form. `projectScopeName`
  (`:666`) answers through `ProjectSecretCandidate` as it does today, so the bare command from a
  checkout stores project-wide exactly when the entry lists that checkout's repository.
  `projectSecretAllowed` (`:1145`) gains the new block as its first source for the ALLOWED column.
  The `set` help (`:308`) and the store-time note (`:859`) get D1273's reach sentence: the note names
  the reach through `repoMayUseProject`, never a fixed "every enrolled repository".
- **What must not change.** The value path, the name-vs-value refusals, `CheckSecretName`, the
  `--repo` path's `declares` check, and the precedence note's `repoMayUseProject` call.
- **Red step.** `TestSecretsSetRefusesAWorkflowSecretWithoutAnEntry` (today's sentence, kept word for
  word for a name with no entry) and `TestSecretsSetStoresAWorkflowSecretTheEntryLists` (a checkout
  whose origin the entry lists, with no flag, writes the project container) — the first passes today
  and the second cannot compile, so the task's red is the pair, not one test. Then
  `TestSecretsSetRefusesAWorkflowSecretForAnUnlistedRepository` (the bare command in a checkout the
  entry excludes writes the repository's own copy and says so), and
  `TestSecretsSetNeverStoresTheValueAsAnArgument` (unchanged behaviour, held so this task cannot
  weaken it by accident).
- **Files (≤6).** `internal/cli/secrets.go`, `internal/cli/secrets_test.go`, and the pty and
  bitbucket-text test files the help text's edits touch (`secrets_pty_test.go`,
  `secrets_bitbucket_text_test.go`), named at implementation time by the pin gate.
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

## W5. `internal/projectpolicy`: a profile may not mount one (D1266). **SEC**

- **Interface.** `profiles.go`'s secrets loop (`:302`–`:330`) gains one refusal beside the Claude
  OAuth token's (`:322`), with that refusal's own reasoning: a shared job is shared by every
  repository admitted to the profile, so a per-repository allow-list is not one it can honour. The
  refusal is written **in the validator**, so `publish`, the deployer and both readers of the node
  hold one profile to one rule.
- **No policy version bump.** The node's shape does not change: the refusal is a rule about an entry
  the node already carries, and D1268 publishes nothing.
- **Red step.** `TestProfileRefusesAProjectWorkflowSecret` — a profile whose `secrets` names a
  workflow secret is refused, and the refusal does not echo the name in a way that would quote a
  value (the block's own rule). Then `TestProfileStillAcceptsAReservedName`, which is the test that
  keeps the refusal from widening to names a profile legitimately mounts today.
- **Files (≤4).** `internal/projectpolicy/profiles.go`, `internal/projectpolicy/profiles_test.go`,
  and whichever of the package's other tests pin the profile-secrets refusals (named by the pin
  gate).

## W6. The documents the change cannot ship without (D1272, D1273). **SEC**

The feature's reach is invisible in a repository's own `fugaro.yaml`, so the files below are where a
person decides whether storing one value for sixty-five repositories is safe.

- **Edits.** `SECURITY.md:28` (the "no project-wide meaning" clause gains the new block),
  `SECURITY.md:38` (`workflows.<n>.secrets` **can** be shared this way, through the operator's own
  entry), a new paragraph in the same section carrying D1273's sentence (the wildcard's reach, the
  repo-wins rule, and the fact that the value is what the repository's build and tests read),
  `docs/project-layer.md:244` (the parenthesis that says a name with a project-wide meaning cannot be
  shared through a workflow's block), `docs/design/project-secrets.md` §3 step 4 (a pointer to this
  folder, not a rewrite), and `plugin/skills/setup/SKILL.md:130` (a covered workflow secret is not
  prompted for either).
- **Red step.** Extend `internal/cli/docs_project_wide_git_test.go`'s discipline in a new
  `internal/cli/docs_project_workflow_secrets_test.go` — which W7 finishes — with the two sentences
  this task writes, so the docs cannot land without them. The test is **written here and completed in
  W7**; the shared-files table says so.
- **Files (≤6).** The five documents above plus the docs test file.
- **SEC.** It is the sentence a reader uses before typing the command. A reach that is documented
  wrongly is worse than one that is undocumented, because it is believed.

## W7. This folder's status lines, and the census (D1277)

- **Edits.** Each of this folder's four documents' status lines: from "nothing is approved" to what
  the owner actually ruled, decision by decision, and never a decision's content. The release notes'
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
- It **promises nothing about a hostile run**: the allow-list decides which jobs mount a value, not
  what a run does with it (reconciliation §5).
