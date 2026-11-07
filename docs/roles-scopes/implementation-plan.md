# User roles and scopes: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Every task below needs the owner's
explicit go; answering a decision is not approving a task. It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md). Answers that shaped it (2026-11-07): D1580 (CLI and console
checks first), D1581 (rename and migrate in `update`), D1582 (a lapse limits only Subscription
surfaces), D1583 (scope grammar, amended to canonical patterns, **owner to confirm**). Tasks that
depend on an open decision say so; they cannot start before it is answered.

**External dependencies (not in this folder).** `docs/composite-commands` (D1560–D1579): the named-step
registry and the "saved, not live yet" message. `docs/no-prompt-policy` (PR #1906): no confirmations,
the refusal style, and the sibling's D1542 shape for D1590. Neither is merged; RS5 and RS6 wait for
the tasks of those plans that touch the same command files (see the shared-files table).

## Conventions every task follows

- `CLAUDE.md` and the rules files the areas load: `.claude/rules/terraform.md` (RS4), `.claude/rules/console.md`
  (RS7), `.claude/rules/credentials-service.md` is **not** touched. `internal/infra/tf/cover.go` rows are
  added on purpose, never widened to pass a plan (RS4).
- **`fugaro update` is the whole upgrade** (CLAUDE.md hard rule): RS3 carries the migration of an
  installation at the previous list shape, with a test that one at the old version converges.
- **A field never changes meaning:** the published policy gains an `access` member (new name); the old
  list keys are retired by the migration, never reused. Any `--json` shape that gains a field keeps its
  version; removing `launchers`/`operators` from a JSON output bumps that output's version.
- **Plugin skills lint:** every command and flag a skill names is checked against the CLI; the renamed
  words change the skills in the same PR as the CLI (RS3, RS5, RS10).
- No secret-shaped literal in tests or docs. Hermetic tests only (`internal/gcpfake`, `t.TempDir()`).
  **No live cloud action by an agent;** Checks 400–402 are owner-run scripts under `scripts/live-checks/`
  (one idempotent script with setup, assertions, teardown, `--dry-run`, refusing `belong`, plus a
  hermetic fake-gcloud test).
- Steps an operator must take after a deploy go under `### For operators` in the release notes, in the
  same PR (RS3 and RS4: "run `fugaro update`" and what it will do).
- Docs are tested: change a doc, run `go test ./internal/cli -run Docs`.

## Global constraints and review focus

| Failure mode the spec implies | Pinned by |
| --- | --- |
| A scoped admin widens their own scope or grants one wider than theirs (F17.1) | RS1 `TestGrantMustBeSubsetOfGrantorScope`; RS5 command test |
| The last project admin removed, or a self-demotion that leaves no admin (F17.2) | RS5 `TestRevokeRefusesLastProjectAdmin` |
| `project`/`all-repos` parsed as a repo, or a pattern that matches everything slipping in as a scoped grant (`*` alone is `all-repos`) | RS1 `TestKeywordsAreNeverRepositories`, `TestBareStarIsRefusedAsScopedPattern` |
| A pattern that matches a repo only by case or `.git` variant | RS1 `TestPatternsAreCanonicalised` |
| A stale local list granting more than the published node | RS3 `TestUpdateMigratesListsToAccessNode`, RS5 refuses on drift |
| A role check that quietly allows on a parse failure | RS1 `TestUnparseableGrantDeniesEverything` (fail closed) |
| An agent session granting or revoking a role (D1590) | RS5 `TestGrantRefusesInAgentSession` |
| The console showing or spawning something the CLI would refuse | RS7 fence equality test |
| A lapse removing assignments, or stopping the core CLI (D1582) | RS9 `TestLapseLimitsSubscriptionSurfacesOnly`, `TestLapseKeepsAssignments` |
| A scoped role described as a cloud barrier | RS10 doc test pins the stated limit (D1580) |

**Reviewer tier.** IAM, policy, pairing and the console fence are security-sensitive: **opus +
mandatory independent review, asked for IN THE RUN with level 3** (`fugaro run --review-levels`), never
also a local reviewer. RS1, RS2, RS3, RS4, RS5, RS7, RS8 and RS9 are that tier. RS6a–c and RS10 are
standard.

## Slices

- **Slice A — the resolver, inert (RS1, RS2).** Ships nothing a user sees: the new package, the policy
  node, both dormant.
- **Slice B — the rename and the viewer (RS3, RS4).** The migration in `update`; the viewer list and
  its read-only role.
- **Slice C — the commands (RS5, RS8, RS9).** `fugaro user` grants with scopes; pairing; the lapse input.
- **Slice D — enforcement in the commands and the console (RS6a–c, RS7).** Every management command
  asks the resolver; the console reads through the same commands.
- **Slice E — closing (RS10).** Docs, skills, release notes. **Later, not planned here: RS12**, per-repo
  cloud enforcement.

## Tasks

### RS1 — `internal/access`: roles, scopes, the resolver (inert)

- **Files:** `internal/access/access.go`, `scope.go`, `resolve.go`, `access_test.go` (all new).
  `internal/mobileauth/mobileauth.go` re-exports the role type from the new package (type alias).
- **Interfaces:** consumes `projectpolicy.MatchRepo` and `projectpolicy.Canonical`. Produces
  `Grant{User, Role, Scope}`, `ParseScope`, `Effective(grants, repo) Role`, `Allowed(grants, action,
  repo)`, `SubsetOf(grantee, grantor)`, `Covered(scope, allowRepos) []string`.
- **Schema/rules/config change:** none.
- **Tests:** `TestHighestRoleWins`, `TestProjectScopeOnlyForProjectActions`, `TestAllReposCoversLaterEnrolled`,
  `TestPatternStarStaysInsideASegment`, the Global-constraints tests above.
- **Command:** `GOFLAGS=-p=1 go test ./internal/access/ ./internal/mobileauth/ -count=1`
- **Depends on:** nothing. **Commit:** `access: roles, scopes and the one resolver (inert)`.
- **Red step:** `TestGrantMustBeSubsetOfGrantorScope` fails against an empty `SubsetOf`.
- **Reviewer tier:** opus + mandatory independent review (level 3 in the run). **OWNER-RUN:** no.

### RS2 — the `access` node in the published project policy

- **Files:** `internal/projectpolicy/policy.go`, `internal/projectpolicy/access.go` (new),
  `schemas/project-policy.schema.json`, `internal/projectpolicy/testdata/` corpus entries,
  `docs/project-layer.md` scope table row (a docs test pins it).
- **Interfaces:** consumes RS1. Produces `Policy.Access` (grants), decoded strictly, validated at
  load by `access.ParseScope`; an absent node means **the migrated lists' meaning**, never "everyone".
- **Schema change:** one new optional member (a new name; no format version bump).
- **Tests:** `TestPolicyAccessNodeDecodesStrictly`, `TestAccessNodeRefusesUnknownRole`,
  `TestAbsentAccessNodeGrantsNothingNew`; schema golden test.
- **Command:** `GOFLAGS=-p=1 go test ./internal/projectpolicy/ -run 'Access|Schema' -count=1`
- **Depends on:** RS1; **decision D1585** (storage) answered. **Commit:** `projectpolicy: the access node`.
- **Red step:** the strict decoder test fails before the member exists. **Tier:** opus + level 3.
  **OWNER-RUN:** no.

### RS3 — rename and migrate in `update` (D1581, D1589)

- **Files:** `internal/localcfg/localcfg.go` (keys), `internal/cli/init.go`, `init_new_machine.go`,
  `roles.go`, `internal/cli/update*.go` (the migration), `internal/infra/` input builders,
  `plugin/skills/**`, `docs/**` pins that name `--launcher`/`--operator`, `schemas/`.
  Terraform variable and resource names **do not change** (D1589).
- **Interfaces:** consumes RS2. The migration reads the old lists and writes `access` grants:
  `launchers`→operator@project, `operators`→admin@project, `budget_admins` and the project's owners
  and editors stay implicit admins (F17.3), then retires the old keys in the same update.
- **Tests:** `TestUpdateMigratesListsToAccessNode` (an installation at the previous shape converges),
  `TestPlanIsEmptyForAPureRename`, the plugin lint, `go test ./internal/cli -run Docs`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'Migrat|Roles|Docs' ./plugin/ -count=1`
- **Depends on:** RS2. **Commit:** `update: launchers/operators become operator/admin grants`.
- **Red step:** the converge test fails with the old keys still read. **Tier:** opus + level 3.
- **Release actions:** `### For operators`: run `fugaro update`; it migrates the lists in place.
  **OWNER-RUN:** Check 401.

### RS4 — the viewer list and its read-only role

- **Files:** `deploy/terraform/gcp/modules/installation/iam.tf`, `modules/firebase/iam.tf`,
  `modules/installation/variables.tf`, `internal/infra/tf/cover.go` (`grantRules` row, on purpose),
  `internal/infra/` inputs, golden plans (`scripts/gen-golden-plans.sh`, never by hand).
- **Interfaces:** the roles.md §4 grants: `roles/firebasedatabase.viewer`, `roles/datastore.viewer`, a
  read-only job role (the `fugaroLauncher` role minus `run.executions.cancel`), the log view, and the
  runs-bucket read per roles.md D683 and D684 (answered as recommended there). **No** `job_runner`, no
  token-minter, no state bucket, no `secretAccessor`.
- **Tests:** the terraform tests per root, `go test -tags terraform ./internal/infra/...`, a cover
  test that the new row requires its condition, `terraform fmt -check -recursive deploy/terraform`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/infra/tf/ -run 'Cover|Golden' -count=1` then the
  terraform roots as `.claude/rules/terraform.md` lists.
- **Depends on:** RS3. **Commit:** `terraform: the viewer list and its read-only job role`.
- **Red step:** a golden plan test fails until the module defines the role. **Tier:** opus + level 3
  (IAM, the cover table). **OWNER-RUN:** Check 400 (the live proof a viewer cannot launch or cancel).
  Release actions: `### For operators` "run `fugaro update`".

### RS5 — `fugaro user`: grant, revoke and list with scopes

- **Files:** `internal/cli/user.go`, `user_grant.go` (new), `user_ls.go` (new), their tests,
  `plugin/skills/**` (the `working` skill's command list), `docs/` command reference.
- **Interfaces:** consumes RS1, RS2, RS3 and the composite-commands step registry. `fugaro user
  grant <member> --role viewer|operator|admin --scope project|all-repos|<patterns>`, `revoke`,
  `ls [--repo R] [--json]` printing each user's pairs and the **effective role in a repo** and, for each
  pattern, the repos it matches now. Checks input: the member exists in the project's identity set, the
  patterns parse, the caller holds the role at a covering scope, the grant is a subset of the grantor's
  (F17.1), the last project admin stays (F17.2), and an **agent session refuses** (D1590). The change
  declares the steps it needs (IAM-list derive, publish) and runs them through the registry.
- **Tests:** the Global-constraints tests for this task, `TestUserLsEffectiveRoleInARepo`,
  `TestRefusalNamesRoleAndScope` (a refusal, never a prompt).
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'UserGrant|UserLs|UserRevoke' -count=1`
- **Depends on:** RS3; **D1590** answered; the composite-commands registry and the no-prompt tasks that
  edit `user.go` (external). **Commit:** `user: scoped grants, revoke and the effective-role listing`.
- **Red step:** `TestGrantMustBeSubsetOfGrantorScope` at the command level. **Tier:** opus + level 3.
  **OWNER-RUN:** no (Check 402 covers it live).

### RS6a — enforcement: run, cancel, status, logs, history

- **Files:** `internal/cli/run*.go`, `cancel*.go`, `status*.go`, `logs*.go` (exact files confirmed in the
  task's first step; each gets one `access.Require` call and a refusal sentence).
- **Interfaces:** consumes RS1, RS5. viewer: reads in scope; operator: launch, cancel (D1587) in scope.
- **Tests:** `TestViewerCannotLaunch`, `TestOperatorOnRepoAOnlyCannotLaunchRepoB`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'AccessRun|AccessCancel' -count=1`
- **Depends on:** RS5; D1586, D1587 answered. **Tier:** standard. **OWNER-RUN:** no.
- **Commit:** `cli: run, cancel and reads ask the resolver`.

### RS6b — enforcement: settings, config, policy, overrides

- **Files:** `internal/cli/settings*.go`, `config_publish.go`, `project_cmd.go`, `recipes.go`.
- **Interfaces:** each writable key is assigned a required role and scope (the table is part of the
  task, confirmed against `internal/settings`); repo-scoped admin writes go through the existing
  only-tightening merge (F9).
- **Tests:** `TestRepoAdminCannotChangeProjectGuardrails`, `TestRepoAdminChangesRepoSettingWithinGuardrail`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'AccessSettings|AccessConfig' -count=1`
- **Depends on:** RS5. **Tier:** standard (the writes themselves are unchanged); a finding on the
  managed gate goes to the owner. **OWNER-RUN:** no. **Commit:** `cli: settings and policy writes ask the resolver`.

### RS6c — enforcement: images, budget, accounts, secrets, repos

- **Files:** `internal/cli/image*.go`, `budget.go`, `accounts*.go`, `project_enroll.go`, `secrets.go`.
- **Interfaces:** catalog entries are project-level, project-scope admin only (D1584 recommended; if
  the owner answers otherwise this task grows a repo binding and moves behind a catalog-shape task).
  `budget set --repo X` needs admin on X; `--global`/`--defaults` need project scope.
- **Tests:** `TestRepoAdminCannotAddCatalogImage`, `TestAllReposAdminCannotSetGlobalBudget`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'AccessImage|AccessBudget|AccessRepos' -count=1`
- **Depends on:** RS5; D1584 answered. **Tier:** standard. **OWNER-RUN:** no.
  **Commit:** `cli: image, budget, accounts and repos commands ask the resolver`.

### RS7 — the console reads the same commands

- **Files:** `console/src/main/spawn.ts` (the exact-argv `consoleReadPaths`), `console/src/main/ipc.ts`,
  the preload surface, a Members view fixture, `internal/cli/console_reads_test.go` (the Go equality
  test), `docs/design/desktop-console.md` §5.1 row.
- **Interfaces:** consumes RS5's `fugaro user ls --json` (roles.md D691(b), a read-only Members view).
  No reader of its own and no role logic in the renderer.
- **Tests:** the Go test holding both halves equal; `npm run typecheck`, `npm run test:unit -- --maxWorkers=2`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run ConsoleRead -count=1` then the console commands.
- **Depends on:** RS5. **Tier:** opus + level 3 (console spawn/IPC allow-list). **OWNER-RUN:** no.
  **Commit:** `console: a read-only Members view over fugaro user ls --json`.

### RS8 — pairing under scopes (D1588)

- **Files:** `internal/cli/pair.go`, `pair_test.go`, `internal/mobileauth/` (no shape change).
- **Interfaces:** `pairInheritedRole` becomes the highest **project-scope** role of the pairing user;
  a user with only repo-scoped roles is refused with the role and scope needed.
- **Tests:** `TestPairInheritsHighestProjectRole`, `TestPairRefusesRepoScopedOnlyUser`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run Pair -count=1`
- **Depends on:** RS3; D1588 answered. **Tier:** opus + level 3 (pairing). **OWNER-RUN:** no.
  **Commit:** `pair: the device role is read from the access grants`.

### RS9 — the lapse input (D1582, D1587)

- **Files:** `internal/access/lapse.go` (core: a `Limit` the resolver accepts), `ee/` seam file
  (new, behind the `Set*` seam of roles.md D694), `docs/subscriptions` status line, tests.
- **Interfaces:** the licence state (subscriptions S6) calls the seam; while lapsed the resolver caps
  Subscription surfaces at viewer (cancel and the kill switch kept, D1587), the core CLI unchanged.
- **Tests:** `TestLapseLimitsSubscriptionSurfacesOnly`, `TestLapseKeepsAssignments`,
  `TestResumeRestoresAssignments`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/access/ ./ee/... -run Lapse -count=1`
- **Depends on:** RS1; subscriptions S6 (external); D1587 answered. **Tier:** opus + level 3.
  **OWNER-RUN:** no. **Commit:** `access: a lapse limits the Subscription surfaces to viewer`.

### RS10 — docs, skills and the release notes

- **Files:** `docs/design/roles.md` (answer cells D681 and the rename, status line), `docs/roles-scopes/`
  status, `plugin/skills/working/SKILL.md`, `docs/releases/` notes entry, `website/data/` only if a
  page names the words, a docs test pinning the D1580 sentence "a scoped role is a policy check, not a
  cloud barrier".
- **Tests:** `go test ./internal/cli -run Docs`, the plugin lint, `npm run check` in `website/` if touched.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ ./plugin/ -run 'Docs|Lint' -count=1`
- **Depends on:** RS3, RS5, RS6a–c. **Tier:** standard. **OWNER-RUN:** no.
  **Commit:** `docs: roles and scopes, the words and the stated limit`.

### RS12 — per-repo cloud enforcement (LATER, not planned here)

Per-job `run.invoker`, prefix-conditioned bucket grants and per-repo secrets (D1580, F4). Needs its
own reconciliation; security-sensitive; listed so it is not forgotten.

## Parallel execution

**Edges:** RS1→RS2→RS3→{RS4, RS5, RS8}; RS5→{RS6a, RS6b, RS6c, RS7}; RS1→RS9; {RS3, RS5, RS6a–c}→RS10.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | RS1 |
| 1 | RS2, RS9 |
| 2 | RS3 |
| 3 | RS4, RS5, RS8 |
| 4 | RS6a, RS6b, RS6c, RS7 |
| 5 | RS10 |

**Critical path:** RS1 → RS2 → RS3 → RS5 → RS6b → RS10.

**Shared files (no level holds two writers of one file):**

| File | Writers (level) |
| --- | --- |
| `internal/projectpolicy/policy.go` | RS2 (1) |
| `internal/localcfg/localcfg.go`, `internal/cli/init.go`, `roles.go` | RS3 (2) |
| `internal/cli/user.go` | RS5 (3) |
| `internal/cli/pair.go` | RS8 (3) |
| `internal/infra/tf/cover.go`, `modules/*/iam.tf` | RS4 (3) |
| `internal/cli/settings*.go`, `config_publish.go`, `project_cmd.go`, `recipes.go` | RS6b (4) |
| `internal/cli/image*.go`, `budget.go`, `accounts*.go`, `project_enroll.go`, `secrets.go` | RS6c (4) |
| `internal/cli/run*.go`, `cancel*.go`, `status*.go`, `logs*.go` | RS6a (4) |
| `console/src/main/spawn.ts`, `ipc.ts` | RS7 (4) |
| `plugin/skills/**` | RS3 (2), RS5 (3), RS10 (5) |
| `docs/design/roles.md` | RS10 (5) |

**The external plans edit these same command files** (no-prompt removes confirmations in `budget.go`,
`image*.go`, `settings_write.go`; composite-commands adds step calls): RS5 and RS6 must be sequenced
after those tasks, not concurrently. That ordering is a **sequential constraint across plans**, not
inside this one.

## Checks (400–409 reserved)

| Check | What it measures | Who runs it |
| --- | --- | --- |
| **400** | A viewer on a real installation cannot launch or cancel and can read status and logs | owner, one script under `scripts/live-checks/` after RS4 |
| **401** | `fugaro update` on an installation holding the old `launchers`/`operators` lists converges to the grants with an empty plan for a pure rename | owner, after RS3 |
| **402** | A repo-scoped admin changes their repo's setting and image-free resources and is refused on another repo and on the project guardrails; a refusal names role and scope | owner, after RS6 |
| 403–409 | reserved, unused | |
