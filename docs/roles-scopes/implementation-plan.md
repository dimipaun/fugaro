# User roles and scopes: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Every task below needs the owner's
explicit go; answering a decision is not approving a task. It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md). Answers that shaped it (2026-11-07): D1580 (CLI and console
checks first), D1581 (rename and migrate in `update`), D1582 (a lapse limits only Subscription
surfaces), D1583 (scope grammar, amended to canonical patterns, **confirmed 2026-11-07**). **Rounds 1 and 2 of an independent review (2026-11-07) are applied**: D1585 is amended (the node lives under `config/admin/`), and D1591 (how a repo-scoped admin's write reaches the cloud) was answered 2026-11-07 as recommended (option c); D1584 and D1586–D1590 are recorded as the recommendation. D1592 and D1593 (round 2) are recorded as the recommendation. No decision is open; each task still needs its own go. Tasks that
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
| The caller read from config or `git config user.email` (forgeable, F21) | RS1 `TestCallerIsTheCredentialNotGitConfig`; RS5, RS8 take `access.Caller` only |
| Every run's agent reading the members and roles (F28) | RS2 rules test: no `TOKEN` read on the node |
| Two writers each passing the last-admin rule (F23); self-demotion by `grant --role operator` | RS5 `TestConcurrentGrantsRejudgeOnETagMismatch`, `TestSelfDemotionObeysLastAdminRule` |
| A repo admin revoking or demoting a project-scope pair (F24) | RS5 `TestRepoAdminCannotRevokeProjectGrant` |
| A subset test that is wrong for hostile patterns | RS1 `TestSubsetHostileCases` (below) |
| `ls --json` reusing `operators`/`launchers` with a new meaning | RS5 `TestUserLsJSONHasNoLegacyKeys` |
| An explicit admin@project attempting a grant; a stale IAM derive re-granting a revoked member (F22, F29) | RS5 `TestExplicitAdminRefusalNamesBudgetAdmins`; RS3/RS5 `TestRevokeIsNotUndoneByAStaleDerive` |
| The package's documented claims drifting | RS10 `internal/cli/docs_roles_scopes_test.go` |

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
  `internal/mobileauth/mobileauth.go` re-exports the role type from the new package (type alias). **`internal/cli/docs_roles_scopes_test.go` (new) starts here** (decision numbers inside D1580–D1599, answered cells, named files and tests exist); RS10 extends it with the stated-limit sentences.
- **Interfaces:** consumes `projectpolicy.MatchRepo` and `projectpolicy.Canonical`. Produces
  `Grant{User, Role, Scope}`, `ParseScope`, `Input{Grants, Implicit, AllowRepos, Lapse}` (the implicit
  admins, F27, are an input; they count toward the last-admin rule, F22), `Effective(in, caller, repo) Role`, `Allowed(in, caller, action, repo)`,
  `SubsetOf(grantee, grantor)`, `KnownMatches(scope, knownRepos)` and `Caller(ctx)`, which reads the
  email from the **credential the command will use** and never from `localcfg.Me` or git (F21).
  **The subset algorithm** (conservative: false when unsure): `project` ⊇ everything; `all-repos` ⊇ any
  repo list or pattern; a pattern list is a subset of another iff every grantee pattern is a subset of
  some grantor pattern; two canonical patterns compare **segment by segment and only when the segment
  counts are equal** (`*` never crosses `/`); a grantee segment is a subset of a grantor segment iff they
  are equal, or the grantor's is `*`, or the grantor's is `prefix*` and the grantee's is `prefix…` with a
  longer literal prefix (a literal, or `prefix2*`). Hostile cases the tests hold: `api*` is **not** a
  subset of `api-*`; `api-*` **is** a subset of `api*`; `a*b*` against `a*`; `github/acme/*` does not
  cover `github/acme/a/b` (segment counts); a `*` in the provider segment is refused at parse; a scope never admits a repo outside `allow_repos` (F27). **A lone `*` is refused as a scope value (write `all-repos`, reserved to project admins)**, so `all-repos` being a superset of patterns cannot mint it.
- **Schema/rules/config change:** none.
- **Tests:** `TestHighestRoleWins`, `TestProjectScopeOnlyForProjectActions`, `TestAllReposCoversLaterEnrolled`,
  `TestPatternStarStaysInsideASegment`, `TestAllReposAdminCannotGrantBareStar`, `TestSubsetHostileCases`, `TestCallerIsTheCredentialNotGitConfig`, the Global-constraints tests above.
- **Command:** `GOFLAGS=-p=1 go test ./internal/access/ ./internal/mobileauth/ -count=1`
- **Depends on:** nothing. **Commit:** `access: roles, scopes and the one resolver (inert)`.
- **Red step:** `TestGrantMustBeSubsetOfGrantorScope` fails against an empty `SubsetOf`.
- **Reviewer tier:** opus + mandatory independent review (level 3 in the run). **OWNER-RUN:** no.

### RS2 — the `access` node (under `config/admin/`, amended D1585)

- **Files:** `internal/access/store.go` (new: read/write of the node, size cap), `internal/budget/rules/rules.json.tmpl`
  and its goldens (`internal/budget/rules/testdata/`, a new `config/admin/access` entry **only if the
  existing `config/admin` rule does not already deny it**; the test decides), `schemas/` entry for the node,
  `docs/project-layer.md` untouched. **It is NOT a member of `projectpolicy.Policy`** (F28).
- **Interfaces:** consumes RS1. Produces `access.Read(ctx, db)` returning the grants **and the ETag**
  (`rtdb.Client.GetETag`) and `access.WriteIfMatch`, decoded strictly, validated at load by
  `access.ParseScope`, bounded by a count and a byte cap (F27). An absent node means **the migrated
  lists' meaning**, never "everyone".
- **Schema change:** one new node (a new name; no format version bump). **The `.read: false` fence binds client tokens only**: every launcher/operator and the RS4 viewer list hold `roles/firebasedatabase.viewer`, which bypasses the rules, so every listed person (and an agent in their session) can read every grant; a repo-scoped-only user cannot read it at all.
- **Tests:** `TestAccessNodeDecodesStrictly`, `TestAccessNodeRefusesUnknownRole`,
  `TestAbsentAccessNodeGrantsNothingNew`, `TestAccessNodeOverCapRefused`, and the **rules test**
  `TestNoRunTokenReadsTheAccessNode` (a run token and a `USER` claim read `config/admin/access` and are denied;
  the rules golden).
- **Command:** `GOFLAGS=-p=1 go test ./internal/access/ ./internal/budget/rules/ -run 'Access' -count=1`
- **Depends on:** RS1; **D1585** (amended) confirmed. **Commit:** `access: the node under config/admin`.
- **Red step:** the rules test fails if the node were placed under `config/policy/shared`.
  **Tier:** opus + level 3 (a rules change). **OWNER-RUN:** no.

### RS3 — rename and migrate in `update` (D1581, D1589)

- **Files:** `internal/localcfg/localcfg.go` (keys), `internal/cli/init.go`, `init_new_machine.go`,
  `roles.go`, `internal/cli/update*.go` (the migration), `internal/infra/` input builders,
  `plugin/skills/**`, `docs/**` pins that name `--launcher`/`--operator`, `schemas/`.
  Terraform variable and resource names **do not change** (D1589).
- **Interfaces:** consumes RS2. The migration reads the old lists and writes `access` grants:
  `launchers`→operator@project, `operators`→admin@project, `budget_admins` and the project's owners
  and editors stay implicit admins (F17.3), then retires the old keys in the same update.
- **Also:** the migration reads the old lists **from the installation's outputs**, refuses on drift from the machine's copy, and **retires the old keys only after the node write has landed**; the IAM-list derive is taken **after the Terraform state lock is held**, from a fresh read of the node (F29a), pinned by `TestRevokeIsNotUndoneByAStaleDerive`; and the same outputs are what the review screen announces — a member the node holds and the installation's outputs do not entered the node outside this run, so the screen does not list them and a plan granting them is **not covered**: the step asks for the project's name typed at a terminal, which `--yes` does not answer, and a managed project's gate refuses the plan. group:/domain:/serviceAccount: members stay **legacy IAM-only members** (F25; the migration test covers each form); an **older CLI's** `update` refuses against a node it does not know, with a floor naming `brew upgrade` (F27; RS3's first step reads how an older CLI treats an unknown `config/admin` member).
- **The one carve-out from the empty plan is the compute root** (`--provision-batch`): it is handed the derived
  launcher list whole — the config's `terraform.operators` with the admins, because an admin operates too, the
  union the installation and repository roots already take (`launchersWithOperators`) — so a Batch-provisioned
  installation that only renames plans new `roles/batch.jobsEditor` grants for its admins on its next
  `--provision-batch` (`internal/cli/init_compute.go`, pinned by `TestComputeRootTakesTheLaunchersTheModuleGrants`;
  the stage is opt-in, prints its plan and asks for the project's name typed at a terminal). D1589 carries it too.
- **Tests:** `TestUpdateMigratesListsToAccessNode` (an installation at the previous shape converges), `TestLegacyMemberFormsKeepTheirGrants`,
  `TestPlanIsEmptyForAPureRename` (the installation and repository roots), `TestAGrantTheInstallationNeverHeldIsNotCoveredByYes`, the plugin lint, `go test ./internal/cli -run Docs`.
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
- **Interfaces:** consumes RS1, RS2, RS3, RS4 (the viewer list must exist before `grant --role viewer`
  is meaningful) and the composite-commands step registry. `fugaro user grant <member> --role
  viewer|operator|admin --scope project|all-repos|<canonical patterns>`, `revoke`, `ls [--repo R]
  [--json]`. The caller is `access.Caller` (credential, F21). **Judging and writing:** the command reads
  the node with its ETag, judges (input checks: well-formed `user:` member and parsable scopes, F27; the
  caller holds the role at a covering scope; the grant is a **subset of the grantor's** scope, F17.1;
  the **last project admin stays**, self-removal and **self-demotion** included, F17.2; **revoke or demote
  only within the caller's own admin scope**, F24) and **writes conditionally on that ETag**, re-judging
  on a mismatch (F23). An **explicit `admin@project` holds no database write (F22, D1592), so the command refuses for them with a sentence naming `budget_admins`**; a successful `grant` **prints the stored canonical scope**. An **agent session refuses** (D1590, a courtesy). `ls` prints each user's pairs,
  the effective role in a repo, and the repos a pattern matches **among those known to this machine**
  (labelled "known", F27); legacy members are listed as legacy. `--json` carries `grants` and
  `effective`, **never** the keys `operators` or `launchers` (a new name; the old keys are removed with
  a version bump). The change declares the steps it needs (IAM-list derive) and runs them through the
  registry. **What a grant means in the cloud is F22's table**: a repo-scoped grant adds no cloud grant.
- **Tests:** the Global-constraints tests for this task, `TestUserLsEffectiveRoleInARepo`,
  `TestRefusalNamesRoleAndScope` (a refusal, never a prompt).
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'UserGrant|UserLs|UserRevoke' -count=1`
- **Depends on:** RS3, RS4; **D1590 and D1591 answered**; the composite-commands registry and the
  no-prompt tasks that edit `user.go` (external). **Commit:** `user: scoped grants, revoke and the effective-role listing`.
- **Red step:** `TestGrantMustBeSubsetOfGrantorScope` at the command level. **Tier:** opus + level 3.
  **OWNER-RUN:** no (Check 402 covers it live, re-scoped below).

### RS6a — enforcement: run, cancel, status, logs, history

- **Files:** `internal/cli/run*.go`, `cancel*.go`, `status*.go`, `logs*.go` (exact files confirmed in the
  task's first step; each gets one `access.Require` call and a refusal sentence).
- **Interfaces:** consumes RS1, RS5. viewer: reads in scope; operator: launch, cancel (D1587) in scope.
- **Tests:** `TestViewerCannotLaunch`, `TestOperatorOnRepoAOnlyCannotLaunchRepoB`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'AccessRun|AccessCancel' -count=1`
- **Depends on:** RS5; D1587 answered. **Tier:** standard. **OWNER-RUN:** no.
- **Commit:** `cli: run, cancel and reads ask the resolver`. (The spend filter of D1586 is RS6c's: it lives in `budget.go`.)

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
  **Rebuilding a workflow's derived image** (`image build --workflow`, `image_refresh_steps.go`, F8) needs
  admin on that repo until the removal chain deletes it; if that chain has merged first, this part drops.
  **D1586's spend view** (a repo-scoped viewer does not see project-level spend) is filtered in `budget.go`,
  which this task owns.
  `budget set --repo X` needs admin on X; `--global`/`--defaults` need project scope.
- **Tests:** `TestRepoAdminCannotAddCatalogImage`, `TestAllReposAdminCannotSetGlobalBudget`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'AccessImage|AccessBudget|AccessRepos' -count=1`
- **Depends on:** RS5; D1584 and D1586 answered. **Tier:** standard. **OWNER-RUN:** no.
  **Commit:** `cli: image, budget, accounts and repos commands ask the resolver`.

### RS7 — the console reads the same commands

- **Files:** `internal/cli/project.go` (the CLI half of the fence, `consoleReadPaths`), `console/src/main/spawn.ts` and `ipc.ts` (the console half),
  the preload surface, a Members view fixture, the Go tests that hold the two halves equal, `docs/design/desktop-console.md` §5.1 row.
- **Interfaces:** consumes RS5's `fugaro user ls --json` (roles.md D691(b), a read-only Members view).
  No reader of its own and no role logic in the renderer.
- **Tests:** `TestConsoleAllowListedReadsWriteNothing` (`internal/cli/checkouts_test.go`) and `TestDocsDesktopConsoleDesignAllowListIsReadOnlyInTheCode` (`docs_desktop_console_test.go`); `npm run typecheck`, `npm run test:unit -- --maxWorkers=2`.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ -run 'TestConsoleAllowListedReadsWriteNothing|TestDocsDesktopConsoleDesignAllowListIsReadOnlyInTheCode' -count=1` then the console commands.
- **Depends on:** RS5. **Tier:** opus + level 3 (console spawn/IPC allow-list). **OWNER-RUN:** no.
  **Commit:** `console: a read-only Members view over fugaro user ls --json`.

### RS8 — pairing under scopes (D1588)

- **Files:** `internal/cli/pair.go`, `pair_test.go`, `internal/mobileauth/` (no shape change).
- **Interfaces:** `pairMeLookup` reads `access.Caller` (the credential, F21), not `localcfg.Me`; `pairInheritedRole` becomes the highest **project-scope** role of the pairing user;
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

- **Files:** `docs/design/roles.md` (**record D681–D695 as answered** on 2026-10-30, **mark D690 and D691 superseded by the owner's 2026-11-07 D1580 answer** and state that D446 is narrowed, F26), `internal/cli/docs_roles_scopes_test.go` (new: pins the package's claims, below), `docs/roles-scopes/`
  status, `plugin/skills/working/SKILL.md`, `docs/releases/` notes entry, `website/data/` only if a
  page names the words, the docs test pins four sentences: "a scoped role is a policy check, not a cloud barrier" (D1580), "agent sessions are detected by environment variables an agent can unset: a courtesy, not a barrier" (D1590), "anyone given the write the access node needs is effectively a project admin" (F22), and the Check 402 re-scope.
- **Tests:** `go test ./internal/cli -run Docs`, the plugin lint, `npm run check` in `website/` if touched.
- **Command:** `GOFLAGS=-p=1 go test ./internal/cli/ ./plugin/ -run 'Docs|Lint' -count=1`
- **Depends on:** RS3, RS5, RS6a–c, RS7. **Tier:** standard. **OWNER-RUN:** no.
  **Commit:** `docs: roles and scopes, the words and the stated limit`.

### RS12 — per-repo cloud enforcement (LATER, not planned here)

Per-job `run.invoker`, prefix-conditioned bucket grants and per-repo secrets (D1580, F4). Needs its
own reconciliation; security-sensitive; listed so it is not forgotten.

## Parallel execution

**Edges:** RS1→RS2→RS3→{RS4, RS8}; RS4→RS5; RS5→{RS6a, RS6b, RS6c, RS7}; RS1→RS9; {RS3, RS5, RS6a–c, RS7}→RS10.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | RS1 |
| 1 | RS2, RS9 |
| 2 | RS3 |
| 3 | RS4, RS8 |
| 4 | RS5 |
| 5 | RS6a, RS6b, RS6c, RS7 |
| 6 | RS10 |

**Critical path:** RS1 → RS2 → RS3 → RS4 → RS5 → RS6b → RS10.

**Shared files (no level holds two writers of one file):**

| File | Writers (level) |
| --- | --- |
| `internal/access/store.go`, `internal/budget/rules/rules.json.tmpl` and goldens | RS2 (1) |
| `internal/localcfg/localcfg.go`, `internal/cli/init.go`, `roles.go` | RS3 (2) |
| `internal/cli/user.go`, `user_grant.go`, `user_ls.go` | RS5 (4) |
| `internal/cli/pair.go` | RS8 (3) |
| `internal/infra/tf/cover.go`, `modules/*/iam.tf` | RS4 (3) |
| `internal/cli/settings*.go`, `config_publish.go`, `project_cmd.go`, `recipes.go` | RS6b (5) |
| `internal/cli/image*.go`, `budget.go`, `accounts*.go`, `project_enroll.go`, `secrets.go` | RS6c (5) |
| `internal/cli/run*.go`, `cancel*.go`, `logs*.go`, `runs*.go` | RS6a (5) |
| `internal/cli/project.go` (`consoleReadPaths`), `console/src/main/spawn.ts`, `ipc.ts` | RS7 (5) |
| `plugin/skills/**` | RS3 (2), RS5 (4), RS10 (6) |
| `docs/design/roles.md`, `internal/cli/docs_roles_scopes_test.go` | RS10 (6) |

**The external plans edit these same command files** (no-prompt removes confirmations in `budget.go`,
`image*.go`, `settings_write.go`; composite-commands adds step calls): RS5 and RS6 must be sequenced
after those tasks, not concurrently. That ordering is a **sequential constraint across plans**, not
inside this one.

## Checks (400–409 reserved)

| Check | What it measures | Who runs it |
| --- | --- | --- |
| **400** | A viewer on a real installation cannot launch or cancel and can read status and logs | owner, one script under `scripts/live-checks/` after RS4 |
| **401** | `fugaro update` on an installation holding the old `launchers`/`operators` lists converges to the grants with an empty plan for a pure rename | owner, after RS3 |
| **402** | **Re-scoped (F22, D1591, F29b):** the check subject holds the project-wide launcher IAM (operator@project) plus a scoped pair, and the script header says so (a person with no IAM is refused everywhere and proves nothing): a repo-scoped operator is refused on another repo and a repo admin is refused on the project guardrails and on a project-scope revoke, each refusal naming role and scope; the original "a repo admin changes their repo's settings" half moves to RS12 | owner, after RS6 |
| 403–409 | reserved, unused | |
