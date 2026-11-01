# Implementation plan: one `fugaro update`, the plugin at user scope, a session-start nudge

Eight tasks, one pull request each, in the order below, plus one external dependency. **Nothing here is
approved**, and no task starts before the owner answers the decision it carries
([reconciliation.md](reconciliation.md) §3). Each task's decision is named so a task whose answer came
back the other way is redrawn before it is written, not after.

## Order, and what this plan does not own

| | Task | Blocked by | Ships |
| --- | --- | --- | --- |
| **U0** | The test that decides the design: an agent session in an un-enrolled repository still gets the plugin moved | — | a failing test and nothing else |
| **U1** | The plugin step leaves the checkout stage and becomes `LocalUnderAgent` | U0 | D941, F12–F15 |
| **U2** | The pin becomes opt-in: `fugaro update-skills --pin`, and `setup` writes no `.claude/settings.json` | U1 | D940, D944, D949 |
| **U3** | doctor: the user-scope check, the classes, and `--offline` | U2 | D942, D945, D946, D951, D952 |
| **U4** | One command: `fugaro upgrade` is removed and `update` takes `--check` | U1, U2, U3 | D948, D950, F16, F18 |
| **U5** | The migration of a repository carrying an old pin | U2, U4 | D943, F19 |
| **U6** | The skills: the nudge, the `update` skill, and the `plugin/` gate that keeps gating | U3, U4 | D947, D953, F8 |
| **U7** | The living documents, and the release that carries them | U1–U6 | the census's class 1 ([reconciliation.md](reconciliation.md) §4a) |
| **E1** | The release | U0–U7 | the notes, the summary line, and **no operator step** |

**The order is a dependency, not a preference.** U1 must land before anything that assumes the plugin step
is reachable in an agent session; U3's doctor fix strings must point at `fugaro update` **before** U4
deletes `fugaro upgrade`; U5's migration has nothing to remove until U2 has made the pin opt-in.

**What this plan does not own.** It does not touch `internal/runner/runner.go`, `internal/cli/root.go`,
`internal/cli/api.go`, `website/**`, `plugin/**`, `console/**`, `mobile/**`, `shared/**`, `ee/**`,
`docs/releases/**`, `docs/release.md`, `docs/design/v1.md`, `docs/gcp-live-checklist.md`, `SECURITY.md`,
`LICENSING.md`, `README.md`, `.goreleaser.yaml` or `.github/workflows/**`: U7's tasks name them, and the
runs that own those paths make the edits. **This design-only pull request edits none of them.**

**The merge rule.** The whole 0.15.0 body of work, this plan included, waits for the 0.14.2 tag; the
coordinator merges after review. A task does not merge `main` into a branch that a follow-up run will use
if `main` changed `.github/workflows/` (`CLAUDE.md`).

## Gates (every task)

- `go build ./... && gofmt -l . && go vet ./...` — **gofmt prints nothing**.
- Targeted tests only, always `GOFLAGS=-p=2`: `internal/cli` and `internal/runner` take ~25 minutes each
  in full, so a task names its tests (`-run 'TestX|TestY'`). A change to a doc runs
  `go test ./internal/cli/ -run Docs -count=1 -timeout 40m` as well; a change to the backlog runs
  `-run 'Docs|Backlog'`.
- Each of the task's changed or added files: `grep -rln '<base name>' --include='*_test.go' internal/ cmd/
  scripts/ deploy/ schemas/ plugin/ ee/ images/`, and run every hit with a `-run` filter. **Never** the
  whole `internal/cli` or `internal/runner` package.
- `go test ./plugin/ -count=1` for anything that touches a skill (the release gate, F8);
  `go test ./internal/eeboundary/... -count=1`; `go test -race ./internal/runner/...` when a task reaches
  the runner; `go test -tags terraform ./internal/infra/...` when it reaches the infra layer.
- `terraform fmt -check -recursive deploy/terraform` when a task reaches `deploy/terraform`.
- **Red first.** Every task writes its failing test, sees it fail for the reason it claims, and only then
  implements. No mutation proofs beyond that.
- Hermetic: no network, no home directory, no real cloud — `internal/gcpfake`, `internal/testutil`,
  `t.TempDir()`. No secret-shaped literal anywhere, and no real name: `owner/repo-a`, `person-1`,
  `workspace-a`.
- **No goldens.** Checked, not assumed: `git grep -l 'fugaro upgrade' -- '*testdata*' '*golden*'` is empty
  on `main` at `3d6ed04d`, and no fixture in the tree carries a `.claude/settings.json` — every
  `pluginwire` test builds its own in `t.TempDir()`. A task that adds a golden for new output says so in
  its own pull request; none of these eight needs one.

## U0. The test that decides the design

**What it is.** The one claim in this design that could ship as a bug if it is wrong: a coding agent's
session, in a repository whose repository stage returns needs-you, still reaches the plugin step
(F13). U0 writes that test and nothing else, and it **fails** at the end of the task.

**Files.** `internal/cli/init_checkout_upgrade_test.go` (the harness the F13 case needs already exists
there), or a new `internal/cli/init_plugin_stage_agent_test.go` if the case reads better beside the plugin
stage's own tests.

**Interface.** The test drives the converge with the agent marker set the way
`internal/initflow/loop.go` applies `LocalUnderAgent`, and a repository fixture whose repository stage
returns needs-you — `internal/cli/init_test.go`'s `fakeTerraform` shape, and `localcfg` in `t.TempDir()`.
It asserts: the plugin step is *applied* (not `left_for_you`), and the cloud step is still parked.

**Red step.** It fails today, for the honest reason: the plugin step lives in the checkout stage, which the
run never reaches, so the step is never applied. The failure message names that, not a missing symbol.

**Docs, release notes.** None. This task ships a test.

## U1. The plugin step leaves the checkout stage (D941)

**What it is.** `checkoutSteps` loses `pin` and `plugin` and keeps `cloud`; the plugin step becomes the
plugin stage's own, and the plugin stage becomes `LocalUnderAgent`. `fugaroCheckout` loses the second way
of being a Fugaro checkout (a wired `.claude/settings.json`) — a repository that never opted in is still
one. A subdirectory and a linked worktree are asserted, not assumed (F12, F14), and a checkout of another
project keeps skipping, with its wording corrected (F15).

**Files.** `internal/cli/init_checkout_upgrade.go` (the step list, `LocalUnderAgent`, `checkout()`,
`planFor`'s `pinSkip`), `internal/cli/init_plugin_stage.go` (the marker and the message that names the
step), `internal/cli/upgrade.go` (`fugaroCheckout`), `internal/initflow/stage.go` if the marker's
documentation names the stage; tests `internal/cli/{init_checkout_upgrade_test.go,
init_plugin_stage_test.go,checkouts_test.go,cascade_test.go,left_commands_test.go}`.

**Interface.** The step lines the converge prints do not change their names (`plugin:`), only which stage
emits them; `left_for_you` is no longer the plugin's answer in an agent session; the checkout stage's
parked-cloud behaviour is untouched.

**Red step.** U0's test, plus a case that a checkout wired by an older Fugaro is still recognised as one
after `fugaroCheckout` loses its disjunct — written to fail against today's code path.

**Tests.** `GOFLAGS=-p=2 go test ./internal/cli/ -run
'TestInitCheckoutUpgrade|TestCheckout|TestCheckouts|TestCascade|TestLeftCommands|TestInitPluginStage'
-count=1`.

**Docs and release notes.** `docs/design/upgrade.md`'s "the plugin step is the checkout's" sentence is
U7's; this task leaves it alone and says so in its description.

## U2. The pin becomes opt-in (D940, D944, D949)

**What it is.** The plugin stage's default is the user-scope install and nothing else: no
`.claude/settings.json` is written, in `setup` or in `update`. `fugaro update-skills --pin` is the flag
that keeps today's merge for a repository that wants the skills required, and `--check` reports both
halves. `claudeplugin.Decide` stops maintaining project and local installs (`plan.go`'s loop becomes
`user` only), while `Applying`/`Effective` keep answering "what applies to this checkout". No `claude`
argv changes: the install was already `--scope user` (F5).

**Files.** `internal/cli/updateskills.go` (the flag, the output struct's `settings`/`changed` members),
`internal/cli/init_plugin_stage.go` (the default path), `internal/claudeplugin/plan.go`,
`internal/claudeplugin/claudeplugin.go` (the scope list's comment), `internal/pluginwire/status.go` (the
`Fix()` strings, which stop offering a pin to a repository that has none); tests
`internal/cli/{updateskills_test.go,init_plugin_stage_test.go,plugin_notices_test.go}`,
`internal/claudeplugin/*_test.go`, `internal/pluginwire/*_test.go`.

**Interface.** `fugaro update-skills --json`'s document keeps every member's meaning (F: "A field never
changes meaning"): `settings` and `changed` are empty/`false` without `--pin` rather than absent, and
`--pin` restores the old values exactly.

**Red step.** A test that the plugin stage writes no file in a checkout with no `settings.json` — failing
today, where the stage's plan creates it.

**Tests.** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestUpdateSkills|TestInitPluginStage|
TestPluginNotices' -count=1`, `go test ./internal/claudeplugin/... ./internal/pluginwire/... -count=1`.

**Docs and release notes.** `plugin/skills/setup/SKILL.md`'s step 9 and the plugin README are U6's and
U7's; this task changes the code and its own tests only.

## U3. doctor: the user-scope check, the classes, and `--offline` (D942, D945, D946, D951, D952)

**What it is.** Three edits to one command, which is why they are one task:

1. **`plugin-install` is re-aimed** at the user-scope install against this CLI (against the pin where one
   is opted in); `not installed` becomes a warning; the ID moves from `doctorMeasureIDs` into
   `doctorUpdatePrefixes`; its fix line becomes ``run `fugaro update` ``.
2. **`plugin-pin` keeps every meaning** and `not wired` becomes `info`, so `fugaro doctor --plugin
   --strict` stops failing a repository that never opted in (F21).
3. **`fugaro doctor --offline`** runs the update class's local-file checks and records a skip for each
   check it cannot answer, with `skipped: true`, `ok: false` and a `problem` saying why — the one additive
   field D946 adds. `--plugin` keeps its documented meaning; the network stays out of `--offline` (D952).

**Files.** `internal/cli/doctor.go` (the flag, the check table, the skip plumbing),
`internal/cli/doctor_render.go` (`doctorUpdatePrefixes`, `doctorMeasureIDs`, the two fix lines and the
shorts), `internal/pluginwire/status.go` (`Fix` and the severity of `not wired`); tests
`internal/cli/{doctor_render_test.go,doctor_policy_test.go,doctor_test.go}`,
`internal/pluginwire/*_test.go`.

**Interface.** The JSON document is doctor's (already `console/docs/json-contract.md` §6.4); no member
changes meaning, one optional member is added, and `skipped` is absent on every ordinary run.

**Red step.** Three: a `--plugin --strict` failure for an un-wired checkout (fails today for the wrong
reason), a `plugin-install` fix string that names `fugaro update` (fails today), and `doctor --offline
--json` naming `cli-version` as skipped (fails today: no such flag).

**Tests.** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDoctor|TestPlugin' -count=1`,
`go test ./internal/pluginwire/... -count=1`.

**Docs and release notes.** `console/docs/json-contract.md` is U7's (it is a `console/` path, so the run
that owns the console makes that edit). This task's pull request states the added member and leaves the
contract file to that run.

## U4. One command: `fugaro upgrade` is removed (D948, D950)

**What it is.** `--check` moves onto `fugaro update` (read-only, local files only, exit 1 when stale);
`--local` is gone; `fugaro upgrade` and its file stop existing; nothing of it is left as a tombstone. The
census in [reconciliation.md](reconciliation.md) §4 and the red-test inventory in its §5 are this task's
checklist, and it re-runs the two greps rather than trusting the counts.

**One resolution this task must get right, and it is not obvious.** `internal/cli/docs_release_notes_test.go`
pins `{"upgrade","local"}` and `{"upgrade",""}` against **`docs/releases/v0.9.0.md`, which may not be
edited** — a release note is a dated record. The file already carries the resolution: `notesCommand`
translates a dated spelling (`init` → `setup`, `:441`) and `notesFlag` translates a dated flag
(`--firebase` → `--firebase-project`, `:453`), each with the comment that "these notes are a dated record
that keeps its own spelling". U4 adds the same two translations — `upgrade` → `update`, and `--local` to
the bare command — and the note's own sentence stays as written. A tombstone would avoid this and is
rejected by D950 for the reason in [spec.md](spec.md) §5.

**Files.** Delete `internal/cli/upgrade.go` and `internal/cli/upgrade_plugin.go` (folding what survives
into `update`'s own files); `internal/cli/init.go` (the `--check` flag), `internal/cli/init_stages.go`,
`internal/cli/init_checkout_upgrade.go` (the `upgradeCtx` it shared), `internal/cli/skillwarn.go`
(`skillWarnCommands`), `internal/cli/{run.go,launch_budget.go,project.go,project_cmd.go,image_refresh.go,
init_policy_migrate.go,init_rules_stage.go,init_review.go}` (their prose and help text); every `upgrade*`
test file under `internal/cli/` moves with its subject; `internal/cli/docs_release_notes_test.go` (the two
translations).

**Interface.** `fugaro update --check` prints the converge's own verdict and exits 1 when stale.
`fugaro upgrade` is cobra's unknown command. **A field never changes meaning**: `update --json`'s document
gains no member and retypes none.

**Red step.** `fugaro update --check` not existing, and `fugaro upgrade` still resolving — both asserted
by tests written first, in the shape `internal/cli/left_commands_test.go` uses for the W1.2 retirement.

**Tests.** `GOFLAGS=-p=2 go test ./internal/cli/ -run
'TestUpgrade|TestUpdate|TestDocsNameUpgrade|TestLeftCommands|TestImageRefresh|TestInitCheckout|
TestInitPolicy|TestInitRules|TestInitReview|TestSkillWarn|TestRunAllowlist|TestCheckouts|TestCascade'
-count=1`, then the whole `-run Docs` filter at the end of the task.

**Docs and release notes.** The living docs are U7's, except `internal/cli`'s own comments and help text,
which this task rewrites as it goes. `internal/cli/docs_upgrade_test.go` and
`docs_upgrade_local_test.go` are U7's, because they pin documents another run owns.

## U5. The migration of a repository carrying an old pin (D943)

**What it is.** The plugin stage offers to remove a `fugaro@fugaro` pin it finds: it prints the two entries
as a diff, says in one sentence what the repository loses, removes them, and never commits. `--yes`
confirms; a terminal asks. **In an agent's session it prints the diff and writes nothing**, `--yes`
included, and says the exact line to run in the user's own terminal. It never unsets the marker that says
the session is one, and no flag, environment variable or non-interactive mode gets around the refusal. A
pin it cannot remove — invalid JSON, a fork's marketplace, a ref that is not a release tag — is left with
its existing refusal text, and the run continues.

**Files.** `internal/cli/init_plugin_stage.go` (the removal plan), `internal/pluginwire` (the inverse of
the merge: take the two entries out, keep everything else byte-identical, refuse a file it cannot parse —
the package's existing rules, not new ones); tests `internal/cli/init_plugin_stage_test.go`,
`internal/pluginwire/*_test.go`.

**Interface.** Nothing new on the wire. The stage's line for this is text, not a state: a new state in
`pluginwire.State` would change a vocabulary the console reads.

**Red step.** A test that the stage removes both entries and leaves a third key's bytes untouched — failing
today, where nothing removes a pin (F19). A second, failing test that the same run **with the agent marker
set** writes nothing and prints the diff.

**Tests.** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestInitPluginStage' -count=1`,
`go test ./internal/pluginwire/... -count=1`.

**Docs and release notes.** `docs/design/upgrade.md`'s migration paragraph is U7's. **No `### For
operators` entry**: the removal is inside `fugaro update`, which is the HARD RULE.

## U6. The skills: the nudge, the `update` skill, and the gate (D947, D953)

**What it is.** `plugin/skills/upgrade/` becomes `plugin/skills/update/`, its body rewritten for two
commands and its `reference/states.md` moved and trimmed of the `pin:`/`plugin:` step rows that no longer
exist. The nudge sentence ([spec.md](spec.md) §7) opens `working` and `setup` — and its second half is
`plugin/skills/upgrade/SKILL.md:27`'s own words, which `plugin/upgrade_skill_test.go` already pins, so the
pinned strings travel with the sentence instead of being rewritten.

**This task is the release gate, and it must be left stronger than it was found.** The `plugin/` tests
check every command and flag a skill names against the CLI tree (F8): `plugin/plugin_test.go:134`'s list
swaps `upgrade` for `update` (D953 — a rename, not a drop, so
`internal/cli/upgrade_readme_roadmap_test.go`'s "the five plugin skills" survives verbatim, which is this
task's own check); `plugin/skills_lint_test.go:583`'s `userRunsAllowed` and `:746`'s `upgradeYesOnly`
re-point at `fugaro update`; `plugin/readme_test.go:45`'s phrase list follows the README paragraph. No
skill gains `allowed-tools` — the lint allows only `name` and `description`, and that refusal is unchanged.

**Files.** `plugin/skills/update/**` (renamed), `plugin/skills/{working,setup}/SKILL.md`,
`plugin/skills/working/reference/followup.md`, `plugin/README.md`,
`plugin/{plugin_test.go,skills_lint_test.go,readme_test.go,update_skill_test.go}`. This task re-runs the
two greps of [reconciliation.md](reconciliation.md) §4 over `plugin/` rather than trusting that list: the
skills are where a stale command name does the most damage, because the lint reads them and a person
follows them.

**Red step.** `go test ./plugin/ -count=1` fails on the skill list before the rename, and the nudge's
sentence is asserted in `working` and `setup` before either carries it.

**Tests.** `go test ./plugin/ -count=1`, and `GOFLAGS=-p=2 go test ./internal/cli/ -run
'TestUpgradeSkillNamesEveryState|TestDocsNameUpgrade' -count=1` for the file that reads a skill's path.

**Docs and release notes.** The plugin README is this task's; the website and `docs/release.md` are U7's.

## U7. The living documents (the census's class 1)

**What it is.** Every living document in [reconciliation.md](reconciliation.md) §4a that names the command,
each in the pull request of the run that owns its path:

- **This repository's:** `CLAUDE.md` (its two `fugaro upgrade` sentences and the plugin-pin clause),
  `.claude/skills/new-release/SKILL.md`, `docs/design/upgrade.md` (rewritten as the one-command design and
  its test `internal/cli/docs_upgrade_test.go`'s `TestDocsDesignListsEveryUpgradeStep` with it),
  `docs/policy-migration.md`, `docs/base-image-migration.md` and the design docs that name the command in
  passing.
- **Another run's, named here and not edited here:** `README.md`, `docs/release.md`,
  `docs/design/v1.md`, `website/{content/cli-reference.md,data/install.yaml,src/pages/getting-started.astro}`,
  `console/docs/json-contract.md` (U3's added member).
- **The dated records stay as they are**: `docs/releases/` and `docs/plans/` (class 2), except the two
  translations U4 adds to `docs_release_notes_test.go`.

**Red step.** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m` fails on each
rewritten sentence before it is rewritten — the doc tests are the pin, and the task changes the wording
both places.

**Tests.** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m`, and
`npm run check` in `website/` for any website content. The backlog census is re-run and must still read
its own count (it is not this work's to change).

## E1. The release

**What it is.** The release that carries U0–U7, through `scripts/release.sh X.Y.Z` from a clean clone: the
notes are `docs/releases/vX.Y.Z.md` plus one line in `website/data/release-summaries.yaml` (no backticks).

**It carries no `### For operators` section, and that is a requirement, not a preference.** The HARD RULE
says `fugaro update` is the whole upgrade: a user runs `brew upgrade dimipaun/tap/fugaro` and then `fugaro
update`, and everything this plan changed — the plugin's move to user scope, the removal of `fugaro
upgrade`, the removal of an old pin, the published policy and the job images — happens inside that one
run. **A task that finds a step which is another command stops and reports** rather than writing the
section: the release must not ship an operator step, and a step this plan cannot fold into `update` is the
HARD RULE failing, which is reported, not documented.

**Known limits the notes should state plainly** (they are [spec.md](spec.md) §10's, in the user's words):
every teammate installs Fugaro on their own machine; a repository that wants the skills required keeps the
opt-in pin; the plugin's marketplace follows the default branch, so the installed plugin can be newer than
the CLI on `PATH`.

**The release that ships it also ships the version bump every other 0.15.0 run needs**, so E1's notes are
written once, by whoever tags, from the branch that holds them all.
