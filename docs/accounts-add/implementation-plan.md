# `fugaro accounts add`: the implementation plan

**Nothing here is approved.** This is the task breakdown the owner reads before deciding anything in
[reconciliation.md](reconciliation.md) §4. Every task is written to that document's **recommendation**;
an owner who answers a `D` the other way changes the task, not just a sentence. Where this plan and
[spec.md](spec.md) disagree, [reconciliation.md](reconciliation.md) wins.

Every task states its files (at most 12), the tests that name it, the **red step** that has to fail
first, and whether it carries a **SEC** flag — a flag that means the change needs an independent review
before it merges, per `CLAUDE.md` and the security rules in `.claude/rules/`. All tests are hermetic:
`internal/gcpfake`, `internal/testutil`, `t.TempDir()`, no network, no home directory, no real cloud, and
**no secret-shaped literal anywhere** — the fixtures use a token-shaped string built at run time from a
recorded length and alphabet, never a plausible credential in the source.

## The order

| Level | Task | Blocked by |
| --- | --- | --- |
| 0 | **E1** `internal/yamledit` (#1467, merged) · **E3** `internal/cli/settings_write.go` (#1464's S5: the design merged, the file is not in the tree) · **E2** `internal/cli/init_policy_migrate.go` publishing from the local config (#1464's S3: the design merged, the stage still only migrates) | other work streams |
| 1 | **A0** the pty answer, taken and pinned | D862/D863; E5's owner |
| 2 | **A1** the local config edit | E1, E3, A0 |
| 3 | **A2** the converge half, and the test that says the command is one command | A1, **E2 landed** |
| 4 | **A3** `--claude` | A1; the guard's floor and alphabet from LC1–LC3 |
| 4 | **A4** `accounts rm` | A2 |
| 5 | **A5** the page becomes short | A1–A4 (the page names a command that must exist) |
| 5 | **A6** the surfaces that name the old pair, and the handoffs | A1–A4 |

**LC1–LC3 are runnable today** and block nothing. They are the owner's, they take ten minutes, and their
answers decide A3's guard before anyone writes it (spec §9).

**A2 does not start until E2 has landed.** The tempting shortcut — start A2 against today's `Policy`
stage, which migrates in place and re-derives nothing (F10), and let the publish half arrive later —
would ship the exact HARD RULE violation this design exists to remove, and the only way to make its red
step green in the meantime is to weaken or skip it. If the owner wants an accounts command before E2
lands, the answer is D860's alternative, and this plan is rewritten rather than partly skipped.

## A0 — the pty answer, taken and pinned

**SEC.** Goal: the `--claude` path's only cross-work dependency (E5) is either paid for or avoided, and
the tree's dependency posture stays a statement somebody made on purpose.

- **If D862 answers "pty" and D863 answers "refuse Windows":** change the comment on
  `github.com/creack/pty`'s existing `require` line in `go.mod`, update `consoleGoModRequireBaseline` in
  `internal/cli/docs_console_boundary_test.go` to match, and add the build-tagged runner file. The module
  is already required — no version moves, no `require` line is added.
- **If D862 answers "the owner pastes":** the task is the refusal file alone, and neither `go.mod` nor
  the pin is touched, and **E5 disappears**.

*Files (≤4):* `go.mod`, `internal/cli/docs_console_boundary_test.go`,
`internal/cli/accounts_add_login_pty.go`, `internal/cli/accounts_add_login_pty_windows.go`.
*Named tests:* `TestGoModHasNoConsoleDependency` (existing; its baseline moves),
`TestAccountsAddClaudeRefusesWithoutTerminals`.
*Red step:* edit the comment on the `creack/pty` require line **first** and touch nothing else.
`GOFLAGS=-p=2 go test ./internal/cli/ -run TestGoModHasNoConsoleDependency -count=1` fails, because the
baseline holds that line verbatim and the tree no longer says it. The source file comes after the
baseline has moved: a new build-tagged file that imports an **already-required** module changes no
`go.mod` line, so adding it on its own moves nothing and is not a red step. If reading the pin shows it
pins only the module list and not the trailing comment, then A0 has no red step at all — the PR says so
plainly rather than manufacturing one, and the two-line change is its own evidence.
*The separate pin gate for this task:* that same `-run` filter on its own, and the console boundary's
whole file only if the baseline change turns out to move more than the one line.
*Shared:* the require comment is a claim about the whole tree, so the change goes in with a sentence in
the PR saying why a pseudo-terminal is needed at all and what the alternative cost — the same sentence
the console-boundary stream would have to write if it were theirs.

## A1 — the local config edit

Goal: spec §2.1–§2.4 and §2.6 without the secret and without the converge: the name, the `--repos`
default, the subset-rule check **before any write**, the comment-preserving edit of
`claude_accounts.<name>.allow_data_to` and `profiles.<p>.claude_accounts`, the diff, the confirmation,
and the refusals.

*Files (≤5):* `internal/cli/accounts_add.go`, `internal/cli/accounts.go` (the parent's `AddCommand` —
**not** `internal/cli/root.go`, see F19), `internal/cli/accounts_add_test.go`,
`internal/cli/accounts_add_refuse_test.go`.
*Named tests:* `TestAccountsAddWritesTheDeclarationAndTheProfile`,
`TestAccountsAddKeepsCommentsAndOrder` (**the red step**),
`TestAccountsAddRefusesInAnAgentSession`, `TestAccountsAddRefusesAName` (a table over
`ClaudeAccountNameRE`'s edges, `default`'s own spelling, and `bitbucket-token`),
`TestAccountsAddRefusesASubsetViolation` (the profile's exact `allow_repos` is the intersection, not the
union), `TestAccountsAddSubsetRuleIsCheckedBeforeAnyWrite` (a refusal leaves the file byte-identical),
`TestAccountsAddWithoutACheckoutRequiresRepos`, `TestAccountsAddPrintsNoValueShape`.
*Red step:* `TestAccountsAddKeepsCommentsAndOrder` is written against a project config carrying comments,
a hand-written key order and a `claude_accounts` entry the owner added by hand. It fails until the write
goes through `internal/yamledit` (E1) instead of `yaml.Marshal` — which is the whole reason F11 and F12
exist.
*SEC:* yes. This task writes the file that decides where a credential may be sent. D868's confirmation
belongs to it: a widening of `allow_data_to` asks for the account's name typed.
*Independent review:* yes, together with A2 — the pair is one security surface, and reviewing them apart
would review half a rule.

## A2 — the converge half

**SEC.** Goal: spec §2.2 steps 5–8 and §2.5. Store the secret project-wide, clear the mark, invoke the
`Policy`, `Installation` and running-checkout `Repository` stages in-process through `initStages`, read
back with `accounts ls`, and end on the line that names a `fugaro run`.

*Files (≤5):* `internal/cli/accounts_add.go`, `internal/cli/accounts_add_converge.go`,
`internal/cli/accounts_add_converge_test.go`, `internal/cli/accounts_add_converge_fake_test.go`.
*Named tests:* `TestAccountsAddLeavesNothingToRun` (**the red step**: after the command returns, the
published node carries `accounts.<name>.allow_repos` for the named repositories **and** the job set
carries `<profile>/<provider>/<name>` with `secretAccessor` on that account's secret and no other account's, and
the command's own last line names a `run` and not an `update`),
`TestAccountsAddPublishHoldsTheAdoptionGate` (a database in a Firebase project the installation never
adopted is skipped, exactly as the Rules stage skips it — the sibling of the settings design's
`TestUpdatePolicyPublishSkipsTheDatabaseTheRulesStageSkips` — the settings design's pin, arriving with E2
and absent from `main`, F14),
`TestAccountsAddStoresProjectWideAndNothingElse` (the value lands only under `claude-oauth-token-<name>`;
a repository-scoped intent is refused **before** the value is read),
`TestAccountsAddReplacesTheTokenOnRerun` (re-running is rotation: version 2, one secret, one job),
`TestAccountsAddClearsTheBrokenMark`, `TestAccountsAddNamesTheRepositoriesItCouldNotConverge` (D865's
corner: a `--repos` repository with no recorded checkout is printed, not silently skipped).
*Red step:* `TestAccountsAddLeavesNothingToRun`, written before the converge exists, fails at the
published node — which is precisely what would happen to the owner if the converge were dropped, and
what F18 records that nothing in the tree notices today.
*SEC:* yes. The `secretAccessor` grant is the narrowest surface in the design and the test asserts the
negative half (no other account's secret).
*Shared:* the publish reaches the node through `policyMigrationFor` and nothing else — no second writer,
no weaker gate. A new stage registered in `initStages` is **not** part of this task.

## A3 — `--claude`

**SEC, and it gets the independent review on its own** — it is the only code in the design that runs
another program. Goal: spec §3. Find the binary through `claudeplugin.Find`, run it as a child **on a
pty** (Linux, macOS), copy its output a line at a time through the guard and flush a partial line once
the child goes idle (§3.2; a prompt with no newline otherwise hangs the login), offer every maximal run
of `[A-Za-z0-9_-]` of 40 characters or more to the capture and withhold it, treat a run on a line
containing `://` as **no candidate at all** (D872: the `code_challenge` is 43 characters from the same
alphabet, and offering it would store a non-credential), store nothing when two or more **distinct**
candidates were seen (D871), and fall back to the hidden prompt when any of that leaves the command
unsure.

*Files (≤6):* `internal/cli/accounts_add_login.go` (the runner and the guard),
`internal/cli/accounts_add.go` (the flag), `internal/cli/accounts_add_login_test.go`,
`internal/cli/accounts_add_login_pty_test.go` (next to `secrets_pty_test.go`, whose pattern it follows),
`internal/cli/accounts_add_claude_test.go`.
*Named tests:* `TestAccountsAddClaudeGuardWithholdsTokenRuns` (**the red step**; a table: a token-shaped
run of the recorded length and alphabet inside prose is withheld; a 39-character run is not; a run on a
line containing `://` is **forwarded whole and stored nowhere** — the case a first cut that captures URL
runs must fail; a line that is only a URL keeps its query string),
`TestAccountsAddClaudeGuardRefusesTwoDistinctCandidates` (D871: two distinct runs anywhere in the login
store nothing and fall back, two *identical* runs are one candidate and store),
`TestAccountsAddClaudeGuardDoesNotStoreAChallenge` (the fake child prints a login URL whose
`code_challenge` is a 43-character run and a token-shaped run after it; only the second is ever offered),
`TestAccountsAddClaudeFlushesAPromptWithoutANewline` (the fake child prints `paste the code` and then
waits; the text reaches the terminal before the child is answered — the hang the line-at-a-time copier
has — and a run split across the flush and the rest of its line is offered once, whole),
`TestAccountsAddClaudeRestoresTheTerminalOnEveryExit` (the pty test: the owner's terminal is in raw mode
for the duration and out of it when the child exits non-zero, when the login is cancelled, and when the
guard falls back — spec §3.1 step 3, whose Ctrl-C claim is only true while the restore runs),
`TestAccountsAddClaudeWritesTheValueNowhere` (the fake child prints a value in every position the guard
knows about; nothing reaching stdout, the config tree, a log line or an error message holds it),
`TestAccountsAddClaudeFallsBackToTheHiddenPrompt` (an unparseable child is not a failure — the prompt
runs, and the command says why),
`TestAccountsAddClaudeRefusesWithoutTerminals`, `TestAccountsAddClaudeRefusesAHostileClaude`
(`exec.ErrDot`, a non-directory-safe path, a directory),
`TestAccountsAddClaudeRefusesAnExportedCredential` (**the red step's sibling, and the spec's rule, not
a stripping one**: with `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` set in the command's own
environment, the command **stops and says so** and **no child is ever started** — the fake child's
marker file is absent afterwards — because either variable decides which account the login lands on and
the command declines to guess rather than editing the owner's environment, spec §3.1 step 4 and §6(b)).
*Red step:* `TestAccountsAddClaudeGuardWithholdsTokenRuns` fails against a first copier that forwards the
child's output verbatim. That failure **is** the reason the guard exists; the test is written first and
the copier is written until it passes.
*Hermetic:* the child is a compiled helper or a script under `t.TempDir()`, never a real `claude`; the
pty test uses the package `secrets_pty_test.go` already uses (E5).
*Unknowns this task does not invent answers to:* spec §3.4's five, and LC1–LC3 settle four of them.

## A4 — `accounts rm`

Goal: spec §4. Refuse while a profile still lists the account, remove the declaration and the profile
entry through the same editor, delete the secret through the store's own `Delete`, converge, and print
the secret's ID **before** it goes.

*Files (≤4):* `internal/cli/accounts_rm.go`, `internal/cli/accounts.go`,
`internal/cli/accounts_rm_test.go`.
*Named tests:* `TestAccountsRmRefusesWhileAProfileListsIt` (**the red step**),
`TestAccountsRmRemovesTheDeclarationAndTheProfileEntry`,
`TestAccountsRmDeletesTheSecretAndConverges` (the job and its service account go, and the value is gone),
`TestAccountsRmLeavesAnotherAccountsSecretAlone`, `TestAccountsRmKeepsTheOtherRepositoriesComments`.
*Red step:* against a first cut that removes only the declaration, `TestAccountsRmRefusesWhileAProfileListsIt`
fails one step later than it should — the file it leaves is invalid under
`config.ValidateProfileClaudeAccounts`'s subset rule, so the command would hand the owner a config that
its own next load refuses.
*SEC:* yes — this is the first caller of `Secrets.Delete` (F16's sibling in spec §4), and the negative
test is the point.

## A5 — the page becomes short

Goal: `docs/project-claude-accounts.md` says one command where it says seven steps, keeps the subset rule
and the job shape as the rules they are, and keeps the version-3 rollout as history.

*Files (≤6):* `docs/project-claude-accounts.md`, `internal/cli/docs_project_named_oauth_tokens_test.go`,
`internal/cli/docs_project_policy_reseed_test.go`, `internal/cli/docs_named_oauth_tokens_test.go`,
`docs/design/named-oauth-tokens.md`.
*Named tests:* `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m` (**the red step**:
the seven `TestDocsProjectClaudeAccounts*` tests and `TestDocsProjectPolicyReseed*` fail on the old
wording first — that is how the pins are found rather than guessed),
plus `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsProjectClaudeAccounts|TestDocsProjectNamedOAuthTokens|TestDocsNamedOAuthTokens' -count=1`.
*Keep:* `…CommandsExist` resolves every `fugaro …` line on the page against the real command tree, so
the page cannot name `accounts add` before A1–A4 land, and `…ExamplesValidate` runs the page's own
examples — check every example against the command's real acceptance before writing it.
*Shared:* §5 of the page is cited by `docs_project_policy_reseed_test.go`, and the settings design's own
docs work (#1464, merged on 2026-10-31) has just rewritten neighbouring sentences in the same tree. Check
before editing, and keep the pinned
fact, not the pinned wording — the rule `CLAUDE.md` states.

## A6 — the surfaces that name the old pair, and the handoffs

Goal: the `claude setup-token`-then-`fugaro secrets set` sequence stops being what a *named* account's
fix tells the owner to type, in the doctor's fix strings, the setup flow's next steps, the repository
root's `step.Commands`, the plugin's setup skill, and the four goldens.

*Files (≤12):* `internal/cli/doctor.go`, `internal/cli/doctor_render.go`, `internal/cli/doctor_test.go`,
`internal/cli/initsecrets.go`, `internal/cli/initsecrets_test.go`, `internal/infra/repo.go`,
`plugin/skills/setup/SKILL.md`, and the goldens `internal/cli/testdata/init_commands.golden`,
`internal/cli/testdata/init_retire.golden`, `internal/infra/testdata/repo_root_addresses.golden`,
`internal/infra/testdata/repo_root_credentials_addresses.golden` — whichever of the four actually moves,
regenerated with their own `-update` flags, never hand-edited.
*Named tests:* `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDoctor' -count=1`,
`GOFLAGS=-p=2 go test ./internal/infra/ -run 'TestRepoRoot|TestRepositoryRoot|TestInit' -count=1`,
`GOFLAGS=-p=2 go test ./plugin/... -count=1` (**the red step**: the plugin's coverage tests resolve every
command and flag a skill names against the real CLI, so a skill that names `accounts add` fails on a
branch where the command does not exist, and a command that exists but is undocumented fails where the
skill's own lint expects it).
*Two rows deliberately not in the file list:* `plugin/**` outside the one skill file and `website/**` are
reserved by the 2026-10-30 rulings; if the coverage tests demand another plugin edit, this task stops and
reports it rather than editing a reserved file.

### Handoffs (this work reports them; it does not write them)

| What | Where it goes | By whom |
| --- | --- | --- |
| LC1–LC5 | `docs/gcp-live-checklist.md`, under the number free at that merge, as Check 35's own row asks (D870) | the work stream that owns the checklist |
| The `### For operators` line | `docs/releases/v0.15.0.md` — "run `fugaro update`", plus what it will ask; nothing else, because `accounts add` needs no operator step by construction | the release owner; `docs/releases/**` is reserved |
| The CLI reference | `website/content/cli-reference.md`, regenerated by its own tooling | the website stream |
| The release summary line | `website/data/release-summaries.yaml`, no backticks | the release owner |

## Shared files

| File | Who else holds it | What this work needs from it | The rule |
| --- | --- | --- | --- |
| `internal/yamledit/**` | **#1467** (E1) | `SetScalar`, `AddListItem`/`RemoveListItem`, `UpsertSeqItemByKey`, `Changed`, and refusals that never echo document bytes | Call it. A second splice is what the settings design's shared-files table forbids, and it is the one thing A1's red step would otherwise let through |
| `internal/cli/settings_write.go` | **#1464's S5** (E3; the design merged, the file has not) | the diff, the typed confirmation, the agent refusal, `backupFile`, `writeFileAtomic` | Call it, do not copy it |
| `internal/cli/init_policy_migrate.go` | **#1464's S3** (design merged, stage unchanged), **#1460** (E2, F14) | a `Policy` stage that publishes from the local config and reaches the node through `policyMigrationFor` | Do not edit it here, and do not write a second publisher |
| `internal/cli/init_stages.go` | the update-door streams | the stage list A2 invokes | Read it. Registering a new stage here is not this work's edit |
| `internal/cli/accounts.go` | nobody | the `accounts` parent, which already owns its `AddCommand` (F19) | This work's file; `internal/cli/root.go` stays closed |
| `go.mod`, `internal/cli/docs_console_boundary_test.go` | the console-boundary stream (E5) | the pty require comment and the pin that holds it | One PR changes both, with the reason in the body; if D862 avoids the pty, neither changes |
| `plugin/skills/setup/SKILL.md` | reserved | the one paragraph that tells the owner what to type | This work edits it **only** if the rulings' reservation is lifted for it; otherwise it is a handoff |
| `plugin/*_test.go` | reserved | the coverage tests that must go green with the command | Report, do not edit |
| `docs/project-claude-accounts.md` | #1464's docs work, possibly | the page and its pins | Keep the pinned facts, change the wording, and run the Docs filter |
| `internal/cli/testdata/init_commands.golden`, `internal/cli/testdata/init_retire.golden`, `internal/infra/testdata/repo_root_addresses.golden`, `internal/infra/testdata/repo_root_credentials_addresses.golden` | whoever changes the init flow | the new next-step lines | Regenerated by their own `-update` flags; never hand-edited |
| `docs/gcp-live-checklist.md`, `docs/releases/**`, `website/**` | reserved | LC1–LC5, the operator line, the CLI reference | Handoffs, above |
| `deploy/terraform/**`, `schemas/**` | — | **nothing** | No `.tf` file and no schema changes: the job set is computed by `infra.ProjectJobs` and the policy node has no schema (spec §8). If A2 ever needs a `cover.go` entry, that is a new resource type and a stop-and-report, not a table widening |

## Gates

Every task, before its PR:

- `git diff --stat origin/main..HEAD` names exactly the files it lists, and no reserved file.
- The **wider pin gate**: `grep -rln '<base name>' --include='*_test.go' internal/ cmd/ scripts/ deploy/ schemas/ plugin/ ee/ images/` for each changed or added file, then each hit with a `-run` that names the tests found.
- If any doc changed: `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m`.
- `go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing) and
  `GOFLAGS=-p=2 go test ./internal/eeboundary/... -count=1`.
- **SEC tasks (A0–A4) additionally:** an independent review before merge, and a PR body section that
  names the threat model entries the change touches (spec §6), what it does **not** close, and the
  negative test that stands for each closed entry.
- **Never** `internal/cli` or `internal/runner` whole, and never without `GOFLAGS=-p=2`.

## Live checks

LC1–LC5 live in [spec.md](spec.md) §9 and are the owner's, in the owner's own terminal. LC1–LC3 settle
`--claude` before A3 is written and need no Fugaro build at all; LC4 and LC5 run after A2/A3 and are the
only checks that can falsify the design rather than the code. **No live step runs from an agent session**,
no `terraform apply`, no `fugaro update`, no `creds set`, and nothing secret is typed into a chat: every
recorded answer is a shape — a length, an alphabet, which line — never a value.

## Release

**0.15.0.** The smallest shippable slice of this work is A1 + A2 with E1, E2 and E3 landed, and that
slice is a feature, not a fix:

1. **It cannot be a 0.14.2 at all.** The publish half depends on E2, a change to what the `Policy` stage
   does (#1464's S3) — a stage change is 0.15.0 work, and a patch that shipped `accounts add` without it
   would ship the second command the HARD RULE forbids. So a 0.14.2 here is not "smaller"; it is a
   different, wrong design.
2. **The HARD RULE is itself the argument.** `fugaro update` is the whole upgrade, and this change makes
   an installed 0.14.x tell its owner to type `fugaro accounts add` in a page that used to say seven
   steps. A patch release's notes are a fix to the version they patch; a credential flow that changes
   what the documentation instructs is a behaviour change, and `docs/releases/README.md`'s own rule puts
   it where the release notes are read before the upgrade, not after.
3. **The dependency posture moves.** A0 turns a package the tree calls test-only into a production one
   (or refuses Windows to avoid it). That is a whole-release statement, not a patch note.
4. **The doc half is shared.** A5 rewrites a page whose pins the settings design's own docs work may be
   touching in the same window; landing it as a patch against a released tag costs a second rewrite.

The only thing that could be a 0.14.2 is D862's paste alternative with D860's alternative — a command
that declares the account and stores the token and tells the owner to run `fugaro update` — and that is
the design this whole document exists to argue against. If the owner wants a small release, the honest
answer is that nothing in **this** work is one: the smallest slice with the HARD RULE intact still needs
E1, E2 and E3, which is 0.15.0 work by whoever lands it.
