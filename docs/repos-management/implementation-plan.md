# Repository management: implementation plan

**Status: a design. Nothing here is implemented, no task is approved, and each needs the owner's explicit go.** The plan is [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), and every task is written to a **recommendation** (D2400–D2417) rather than to an answer: the owner has answered none of them yet, and a task whose recommendation the owner declines is rewritten before it is launched. **Where this plan and the reconciliation disagree, the reconciliation wins; where a task and the reconciliation disagree, the reconciliation wins.**

Slot: the release after the one in flight (§7). Number block: **D2400–D2429** (D2400–D2417 written, D2418–D2429 reserved) and **Checks 620–629** (620 written, 621–629 reserved) — [reconciliation.md](reconciliation.md) §5.

**The one thing this plan adopts rather than builds.** **RE3** is the draft another run left (`fugaro/20261114-183001-d201`, head `2bcda85d`, draft pull request **#2690**, task **RE1** of the brief): `fugaro project repos add` built as the full converge, ended at writeback with tests red. Its diff is the starting point of RE3, and [reconciliation.md](reconciliation.md) F22 lists what it got right and the two things it got wrong — the re-added `--yes` (F5) and the `pendingSites` entry in `noprompt_lint_test.go` it leaves for the no-prompt policy's own T15. **Nobody starts over.**

## 1. Conventions every task follows

Loaded from `CLAUDE.md` and the area rules rather than restated here: comments explain why and never what; no TODO and no section-header comment; no new dependency; config and wire formats decode strictly; a field never changes meaning, so a new document takes a version.

**Boundaries, in three tiers.**

- **Always**: hermetic tests only (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`) — no network, no home directory, no real cloud, no `live` tag; `go build ./... && gofmt -l . && go vet ./...` before every commit; the named tests of the task, never a whole package.
- **Ask first**: any change to `internal/cli/project_enroll.go`'s group function, any change to a published node's shape, any new flag on a command another design owns.
- **Never**: a secret-shaped literal in a test, a doc or a log; a live cloud action by an agent; a deleted or weakened test; an edit under `.github/workflows/` (stage it in `docs/pending-workflows/` if one is ever needed — none is); an edit to `docs/status.md`; a re-added `--yes` or `--non-interactive` (F5); a second writer of `allow_repos`, of `config/policy/repos/<slug>` or of the local config document; a deletion of a secret, a secret version, an image or a run's history.

**Standing safety rules.** Every live step is **owner-run** and on the sandbox installation; **never `belong`**; a run in flight is never disturbed (F17, F24); the credential a person types is read at a hidden prompt or stdin and never from an argument (D2415).

## 2. Order, and what forces it

**The `Needs` column is a code dependency: what must be merged for that task to build, test and be reviewed on its own.**

| Level | Tasks | Needs (code) |
| --- | --- | --- |
| 0 | **RE0** | nothing |
| 1 | **RE1** (`status`), **RE3** (`add`) | RE0 for both |
| 2 | **RE2** (`ls`) | RE0 (the view); RE1 and RE3, because it shares two files with them (below) |
| 3 | **RE4** (`rm`) | RE0, RE3 (the converge it inverts) |
| 4 | **RE5** (docs, the skill, the release notes, the census pin) | RE1–RE4 |
| 5 | **RE6** (Check 620, owner-run) | RE1–RE4 |

Four ordering facts are load-bearing:

- **RE0 lands the view before anything reads it.** `ls` and `status` both need "what is this repository made of", and so does `add`; a second implementation of that read is the drift this design refuses (F8, F9). RE0 ships the type and its tests and **registers no command**.
- **The read-only verb lands first.** The brief's order is `status` before `add` before `rm`, and it is also the safe order: `status` writes nothing, so it can be reviewed and shipped while the two writers are still being argued about, and it is what makes the empty container visible (F11) from the day it lands.
- **RE4 inverts RE3, and must not guess at it.** `rm` removes what `add` writes, in the same stores through the same writers; building `rm` against a `add` that is still in review is building it against a shape that will change.
- **RE2 needs RE1 and RE3 for two files, not for behaviour.** `console/docs/json-contract.md` takes a section per document and `website/content/cli-reference.md` is generated from the whole tree: two tasks in one level cannot both own them. RE2 therefore follows both, and **that is the only reason** — its own file and its own logic need RE0 alone.

**The contract-first pass, and where it was declined.** RE0 is the plan's one contract task, and the edges it breaks are RE1's and RE2's from RE3 (both need the *view*'s shape, not `add`'s behaviour). RE4→RE3 is **kept**: `rm` needs what `add` actually writes and where, which is behaviour. RE5 and RE6→RE1..RE4 are **kept**: the docs sweep and the live check read the commands, not an interface.

## 3. Gates (every task)

- `go build ./... && gofmt -l . && go vet ./...` — gofmt prints nothing.
- The task's named tests: `GOFLAGS=-p=2 go test ./internal/cli/ -run '<names>' -count=1 -timeout 40m`. **Never the whole `internal/cli` or `internal/runner` package.** One test of one's own: `go test ./internal/cli/ -run TestName -count=1`.
- Any task that changes a command's **help text or flags** regenerates the CLI reference in the same commit — `go run ./ee/clidoc` — and runs `go test -tags ee ./ee/clidoc/ -run TestCliReferenceIsCurrent -count=1`. A task that regenerates and finds **another command's** section changed stops and reports rather than committing it.
- Any task that touches a doc under `docs/`, `README.md`, `SECURITY.md` or the website: `go test ./internal/cli/ -run Docs -count=1 -timeout 40m`.
- Any task that touches `plugin/**`: `go test ./plugin/ -run 'TestPluginSkillsLintClean|TestSkillFilesQuoteCommands' -count=1`.
- The wider pin gate: for every file the task changes or adds, `grep -rln '<its base name>' --include='*_test.go' internal/ cmd/ scripts/ deploy/ schemas/ plugin/ ee/ images/`, and run each hit's package with a `-run` filter naming it.
- `go test ./internal/eeboundary/... -count=1`.
- **A test this plan adds that fails locally stops the task**: never push over it, never weaken, skip or delete it. Report it.

## 4. The shared-files table

**A task that needs a file in this table that another task owns adds its edit only after that task has merged**, except where the rule in the last column says the two edits are in different functions and may land in one level.

| File | Tasks | What each one does with it |
| --- | --- | --- |
| `internal/cli/repos_view.go` (new) | **RE0** only | RE0 owns it. No other task edits it; a task that finds it wrong raises a **contract change** rather than working around it, and names its dependents (RE1, RE2, RE3) |
| `internal/cli/project_enroll.go` — `newProjectReposCmd` (the group) | **RE1** | RE1 alone: the group's `Short` and `Long` (F1), and the one line registering `newProjectReposStatusCmd()`. **No other task edits this function** |
| `internal/cli/project_enroll.go` — `newProjectReposAddCmd` | **RE3** only | RE3 rewrites `add`'s `Long` (five steps, idempotence) and registers `--allow-list-only`; it adds **no** `--yes` (D2403). Different function from RE1's edit, so RE1 and RE3 may share level 1 |
| `internal/cli/project_enroll.go` — `newProjectReposLsCmd` / `newProjectReposRmCmd` | **RE2**, **RE4** | RE2 moves `ls`'s `RunE` into its own file and adds `--json`; RE4 rewrites `rm`'s registration and `Long`. **RE2 lands before RE4** (levels 2 and 3), and each touches only its own constructor |
| `internal/cli/project_enroll.go` — `runProjectReposEdit`, `putAllowRepos` | **shared, read** | RE3 calls `putAllowRepos` and reads `runProjectReposEdit`'s idempotence sentence; RE4 calls both. **Neither edits them**, and a change needed inside one is a contract change to the group's own file, raised before it is made |
| `internal/cli/noprompt_lint_test.go` | **RE3** | Adopted with the draft: its `pendingSites` entry for the converge's confirmation **stays**, and the no-prompt policy's **T15** removes it. RE3 does not remove it and does not add to it |
| `console/docs/json-contract.md` | **RE1**, **RE2** | RE1 writes the `status --json` section; RE2 writes the `ls --json` section. **One section each, in level order** — RE2 lands after RE1 |
| `website/content/cli-reference.md` | **RE1**, **RE2**, **RE3**, **RE4** | Generated (`go run ./ee/clidoc`). Each task regenerates it and commits **only the hunks its own command's help produces**; the four commands' sections are disjoint, so the hunks do not overlap. A task that finds another command's section changed stops and reports |
| `docs/rigor-modes/reconciliation.md` | **RE5** only | RE5 moves the census row from **139 to 140** and adds its own amendment sentence, because RE5 adds `internal/cli/docs_repos_management_test.go` (F20) |
| `docs/status.md` | **none of RE0–RE6** | **Not a task's file.** This design's own spec-and-plan sweep writes the row — `repos-management`, `0/7`, nothing merged, no run — and whoever merges the spec moves it as tasks land (A8). No task of the plan edits it |
| `console/src/main/spawn.ts` | **RE5** | The two `CONSOLE_OPERATIONS` entries (D2413), after both documents exist. RE1 and RE2 document the shapes and add no operation |
| `plugin/skills/working/**` | **RE5** only | The new reference file (D2413) |

## RE0. The enrolment view, and the two document versions (contract). *standard*

Without it `ls`, `status` and `add` each derive "what is this repository made of" for themselves, and the three answers drift.

- **Interface.** One new file, `internal/cli/repos_view.go`, exporting a `reposView` value built in one call from the project's budget/project handle plus an optional canonical repository name, carrying per repository: the canonical name; the provider; the base branch; the workflows and their catalog entries (through `catalog.JobImage`, F9); the image **and which of the four sources chose it**; the Claude accounts; the published allow-list state **with the pattern that admits it**; the local `repos:` state; and — for a named repository — the credential rows' *inputs*, never a value. Plus the two document version constants, `reposLsDocVersion` and `reposStatusDocVersion`, and one sentence-document shape per absent member, following `managedStatusDoc`'s rule (F14) rather than a zero.
- **What it must not be.** It is not a second `doctor`: it computes no check, holds no `Problem` and no `Fix`. The checks stay `doctor`'s, and `status` renders them (F12).
- **Red step.** `TestReposViewImageSourceIsNamedOnce` — a repository whose workflow names a catalog entry, one whose entry comes from the effective map, and one that falls to the catalog default produce three **different** named sources — fails to compile against a package with no such type, which is the red this task exists for. Then `TestReposViewSeenIsNotEnrolment` (a repository present only in the seen window is not in the rows) and `TestReposViewAbsentMembersCarryTheirState` (an unreadable local config yields a state sentence, never an empty object).
- **Files (3).** `internal/cli/repos_view.go` (new), `internal/cli/repos_view_test.go` (new), and — only if the state sentences live apart — `internal/cli/repos_view_state.go` (new).
- **Depends on:** nothing. **Not SEC**: it reads.
- **Commit:** `repos-management RE0: one enrolment view, and the two document versions`.

## RE1. `fugaro project repos status [REPO]` — a doctor for the enrolment (D2411, D2412, D2414, D2415). *standard — read-only*

The verb that makes the empty container visible from the day it lands. It writes nothing on any path, `--json` included.

- **Rows.** The nine of spec §2.4, in that order, reusing the checks that already exist (F12) and adding only what does not: the image map entry and its freshness (F8), `credentials_service_ready` (F13), the job and its `run.invoker` grant (F10), the project-layer copy, and the last run and its outcome. **A repository named that is not enrolled is its own line and a nonzero exit** (F25); with no argument, one line per repository plus the failing rows.
- **Every failing row names one command.** `fugaro secrets set` for the container, `fugaro image build <entry>` for an unbuilt entry, `fugaro update --repo <checkout>` for a missing job, `fugaro project repos add` for a repository the allow-list does not admit. A row that names no fix is the defect this row exists to remove (F11).
- **Exit status.** Through `checksFail`'s own rule: nonzero when a row that blocks a launch fails, zero otherwise; `ExitUserError` for usage. A warning row never changes it (D2412).
- **Secret handling.** Presence, a name, a version's state and a rotation age — **never a value**, on any path including `--json` (D2415).
- **The group's description changes here.** `newProjectReposCmd`'s `Short` and `Long` stop saying "the allow-list" and say what the group now is (F1). This is RE1's edit because RE1 lands first.
- **Gate.** `accessGateProjectRead`, after the usage check and before any read.
- **Red step.** `TestReposStatusNamesTheEmptyCredentialAndItsFix` — a fixture repository whose `github-app-key` container holds no enabled version produces a failing row whose `Fix` is the `secrets set` command, and a nonzero exit — fails today because the subcommand does not exist. Then `TestReposStatusEmptyContainerExitIsNonzero`, `TestReposStatusUnknownRepoIsItsOwnLine` (a name that is not enrolled says so and exits nonzero rather than printing an empty screen), `TestReposStatusIsReadOnly` (the shape of `TestDoctorReadOnly`: no writer is reached on any path), and `TestReposStatusJSONCarriesNoValue` (no member of the document holds a secret's value).
- **Files (5).** `internal/cli/project_status.go` (new), `internal/cli/project_status_test.go` (new), `internal/cli/project_enroll.go` (the group function only), `console/docs/json-contract.md` (its `status --json` section), `website/content/cli-reference.md` (regenerated).
- **Command.** `go test ./internal/cli/ -run 'TestReposStatus' -count=1 -timeout 40m`
- **Depends on:** RE0 (the view). **Not SEC**: it reads. It is reviewed at the rigor of a read that names credential *state*, which is why the no-value test is not optional.
- **Commit:** `repos-management RE1: fugaro project repos status, a doctor for the enrolment`.

## RE2. `fugaro project repos ls` — one row per repository, and `--json` (D2409, D2410). *standard*

- **Rows.** The columns of D2409. The **seen** list keeps its own header and its own bounded rendering, and is **not** merged into the per-repository rows — a reading of what has run read as enrolment is the misreading D2409 refuses. Its two sources are **not one window**: the runs bucket's `runs/<slug>/` prefixes are unbounded and only Firestore's `spendDaily` is read over `seenWindowDays`, so the row prints each entry's **sources** and never a window over the list.
- **The document.** One versioned document with `repos`, `allow` and `seen` (D2410), every member that can be absent carrying its own state sentence, documented in `console/docs/json-contract.md`'s own section **in this pull request**. Its shape is RE0's, produced by RE0's view; RE2 adds no read of its own.
- **No console operation yet.** D2413's `CONSOLE_OPERATIONS` entries are RE5's, after both documents exist.
- **Red step.** `TestReposLsJSONIsVersionedAndKeepsSeenApart` — a repository named only by the runs bucket appears in `seen` and in **no** `repos` row — fails today because `ls` takes no `--json` at all. Then `TestReposLsSeenCarriesSourcesAndNoWindow` — each `seen` entry names the sources it was read from, and the document carries **no** window-length member over the list, because the runs-bucket source has no window. Then `TestReposLsRowNamesPublishedAndLocalSeparately` (a repository admitted by a pattern and one admitted by name print different published states) and `TestReposLsUnreadablePolicyIsAStateNotAnEmptyList` (a policy read that did not answer says so; it does not print an empty allow-list).
- **Files (5).** `internal/cli/project_repos_ls.go` (new), `internal/cli/project_repos_ls_test.go` (new), `internal/cli/project_enroll.go` (`ls`'s constructor and `RunE` only), `console/docs/json-contract.md` (its `ls --json` section), `website/content/cli-reference.md` (regenerated).
- **Command.** `go test ./internal/cli/ -run 'TestReposLs' -count=1 -timeout 40m`
- **Depends on:** RE0 (the view) for its logic; **RE1 and RE3 for two shared files only** (§2).
- **Commit:** `repos-management RE2: fugaro project repos ls with a versioned document`.

## RE3. `fugaro project repos add` — the converge behind one confirmation (D2400–D2404, D2416). **SEC**

**This task adopts the draft.** Its starting point is `fugaro/20261114-183001-d201` at `2bcda85d` (draft #2690), read against [reconciliation.md](reconciliation.md) F22. The reviewer reads the draft's diff beside this task.

- **What it keeps from the draft.** The six steps printed as a numbered list before anything is written; `*` refused; the agent refusal **first**; `reposEnrolmentRead`'s provider order (config → `fugaro.yaml` → origin host); the image resolution merged with the published map under D1906's keep rule; the two local writes into one document; the allow-list write through `putAllowRepos`; the three `initflow` stages (Policy, Installation, Repository-when-there-is-a-checkout) with `noBuild`; the report; and the line that **the first image build is not part of this** and is `fugaro image build <entry>`'s.
- **What it corrects.** **`--yes` is dropped** (F5, D2403) — the confirmation is `confirmTyped` and `--ask` is what the command line has; and the `pendingSites` entry in `noprompt_lint_test.go` **stays**, owned by the no-prompt policy's T15, which this task names in its own report rather than removing.
- **What it adds.** `--allow-list-only` (D2404), which keeps today's single write for a pattern that has no converge; the **idempotent** and **resumable** sentences (D2402), each with its own fixture test; and the refusal sentences of D2401, each naming the one command that fixes it.
- **Gate order (D2414).** Agent refusal, then `openProjectBudget`, then `accessGateProject`, then `requirePolicyRules`, then the reads, then the confirmation.
- **Red step.** `TestReposAddPrintsTheSixStepsInOrder` (the plan lists the six before any write, and the image build is named as not covered) and `TestReposAddRefusesWithTheFixingCommand` (no checkout → the refusal names `--allow-list-only`) — the first fails today because `add` writes the allow-list and returns. Then `TestReposAddIsIdempotentOverACompletedEnrolment` (the `"is already on the allow-list; nothing changed"` sentence at `internal/cli/project_enroll.go:512`, and **no** writer is reached), `TestReposAddConvergesAHalfDoneEnrolment` (a fixture with the local entry and no allow-list entry writes only the missing one), `TestReposAddHasNoYesFlag` (the flag does not resolve — the regression test for F5, and the one a later rebase cannot quietly undo), and `TestReposAddRefusesAnAgentSession` (before any read).
- **Files (6).** `internal/cli/project_enroll_converge.go` (new, adopted), `internal/cli/project_enroll_converge_test.go` (adopted), `internal/cli/project_enroll.go` (`add`'s constructor and `Long`), `internal/cli/noprompt_lint_test.go` (adopted unchanged), `website/content/cli-reference.md` (regenerated), and the budget/gate wiring if the draft's call sites moved.
- **Command.** `go test ./internal/cli/ -run 'TestReposAdd|TestProjectReposAdd|TestNoPrompt' -count=1 -timeout 40m`
- **Depends on:** RE0 (the view; the draft has its own resolution, which this task replaces with RE0's so `add`, `ls` and `status` cannot disagree).
- **SEC.** It writes the published policy, this machine's config and a Terraform root, and the root carries the job account and its `run.invoker` grant on the credentials service.
- **Commit:** `repos-management RE3: fugaro project repos add converges behind one confirmation`.

## RE4. `fugaro project repos rm` — the whole removal (D2405–D2408). **SEC**

- **What it removes (D2405).** The published allow-list entry through `putAllowRepos`; the machine's `repos:` entry; the machine's image map entry **only when the effective map is the file's own**; the published `config/policy/repos/<slug>` node through `project policy publish`'s removal path (F18); the repository's state under `RepoStatePrefix`; and its cloud resources through the retire path — its workflow job, its check job, its Scheduler job and its job account's grants — with `deleteOrphanedJob` doing the API deletion (F16).
- **What it deliberately leaves, said twice (D2406).** The provider's App installation; the built images; the secrets and their versions; the run history; every pull request a run opened; the remote branch. Before the confirmation and after the write, in `removalWarning`'s shape.
- **An in-flight run (D2407).** `runsInFlight` first; when one is going, `confirmTypedStrict` — no flag stands in — and the sentence names the run. No step rewrites a run's record, ledger or counters, and the property is pinned in the shape of `TestUpdateLeavesARunInFlightRunning`.
- **No `--purge` (D2408).** Not registered, not mentioned in the output.
- **Red step.** `TestReposRmRemovesTheSixAndNamesTheSixItLeaves` — every removal is asserted on a fixture and every kept thing appears in the pre-write warning — fails today because `rm` writes the allow-list and returns. Then `TestReposRmIsStrictWhileARunIsGoing` (a run in flight → the strict confirmation, and no flag answers it), `TestReposRmConvergesAHalfDoneRemoval` (already absent from one store → says so rather than failing), and `TestReposRmDeletesNoSecret` (no secret-name-bearing delete is reached on any path).
- **Files (6).** `internal/cli/project_repos_rm.go` (new), `internal/cli/project_repos_rm_test.go` (new), `internal/cli/project_enroll.go` (`rm`'s constructor and `Long`), and the retirement/publish call sites it needs, named at implementation time.
- **Command.** `go test ./internal/cli/ -run 'TestReposRm|TestRetire|TestForget' -count=1 -timeout 40m`
- **Depends on:** RE0 (the view), RE3 (what the converge writes, store by store). **Kept edge** (§2).
- **SEC.** It deletes Cloud Run jobs and removes IAM grants, and its delete list is the owner's.
- **Commit:** `repos-management RE4: fugaro project repos rm removes the enrolment and says what it keeps`.

## RE5. The docs, the skill, the console's read reach, and the census pin (D2413). **SEC**

- **The skill.** A new reference under `plugin/skills/working/`: a session reads `fugaro project repos ls` and `fugaro project repos status`, acts on the fix a failing row names, and **never runs `add` or `rm`** — the rule `plugin/skills/setup/reference/decisions.md` (line 43) already states for the owner's own commands. Every `fugaro …` line and every `--flag` must resolve against the cobra tree (`plugin/skills_lint_test.go`).
- **The console.** Two literal `words:` entries in `CONSOLE_OPERATIONS` (`console/src/main/spawn.ts`) — `ls --json` and `status --json` — and nothing else. No console code constructs `add` or `rm`.
- **The docs.** `docs/project-layer.md` and `docs/checkout.md` stop implying the six-step order, naming the one command that does it; the group's four verbs get their own page or section, whichever the surrounding docs use.
- **The census pin.** A new `internal/cli/docs_repos_management_test.go`, in the shape of its siblings (it pins this folder's four documents and this spec's status line), and the census row in `docs/rigor-modes/reconciliation.md` **moved from 139 to 140** with its amendment sentence — the six tests that assert the row (F20) are the red step.
- **`docs/status.md` is no task's file (A8).** RE5's pin reads this spec's row where it stands; the row itself is written in this design's own sweep and moved by whoever merges the spec, and no task of RE0–RE6 writes it.
- **Red step.** `TestDocsReposManagementCensusRowMovedForItsPin` fails before the row is moved, in the shape of `TestDocsManagedAgentConvergeCensusRowMovedForItsPin`; that is the pin this task exists to add.
- **Files (≤10).** the new `plugin/skills/working/reference/…md`, `console/src/main/spawn.ts`, `internal/cli/docs_repos_management_test.go` (new), `docs/rigor-modes/reconciliation.md` (the census row), `docs/project-layer.md`, `docs/checkout.md`, and the release-notes file (§7).
- **Command.** `go test ./internal/cli/ -run Docs -count=1 -timeout 40m && go test ./plugin/ -run 'TestPluginSkillsLintClean|TestSkillFilesQuoteCommands' -count=1`
- **Depends on:** RE1–RE4.
- **SEC**: it edits what a coding agent's session is told it may run.
- **Commit:** `repos-management RE5: the skill, the console's read reach, the docs and the census pin`.

## RE6. Check 620 — enrol the sandbox end to end and remove it. **SEC, OWNER-RUN**

**No agent runs any step of this.** The owner runs it at their own terminal, on a throwaway installation and a throwaway checkout of `dimipaun/fugarosandbox`; **never `belong`**.

- **The script.** `scripts/live-checks/check620.sh` plus `scripts/live-checks/check620_test.sh`, the latter driving fake `gcloud`, `fugaro` and `git` on PATH — the shape Check 313 established — with its registration and description in `scripts/live_checks_test.go` (`liveCheckScripts`), a row in `docs/gcp-live-checklist.md`'s table and a `## Check 620` section with a Restore.
- **What it does, in order.** (1) On a fresh throwaway checkout, run `fugaro project repos add` and read every one of the six steps' own output; (2) **leave the `github-app-key` container empty on the first attempt** and assert that `fugaro project repos status` names that row, names `fugaro secrets set` as its fix, and **exits nonzero** — this is F11, the failure the whole order answers; (3) fill it, converge again, and assert the row turns green and the exit status becomes zero; (4) launch one run and assert it starts; (5) start a second run, run `rm` while it is going, and assert the strict confirmation fires, the removal proceeds and **the run finishes**; (6) assert the six things `rm` leaves are still there (the App installation, the image, the secret and its version, the run history, the pull request, the branch) and that `fugaro update` afterwards writes nothing new.
- **What it never does.** Read or print a secret value; touch `belong`; run anything as an agent.
- **Files (≤6).** `scripts/live-checks/check620.sh` (new), `scripts/live-checks/check620_test.sh` (new), `scripts/live_checks_test.go` (the registration and its description), `docs/gcp-live-checklist.md` (the row and the section), and — if the row's cost column needs it — `docs/repos-management/implementation-plan.md`'s §5.
- **Command.** `bash scripts/live-checks/check620_test.sh && go test ./scripts/ -run 'TestLiveCheckScripts/check620' -count=1 -timeout 30m`
- **Depends on:** RE1–RE4. **Owner-run**, and it is the plan's only owner-run step.
- **Commit:** `repos-management RE6: Check 620, the sandbox enrolment and removal, owner-run`.

## 5. Checks the plan takes

| Check | What it measures | Who runs it |
| --- | --- | --- |
| **620** | the whole order on a real installation: the six steps, the empty container named with its fix and a nonzero exit, a run started, a run finished through a removal, and the six things `rm` leaves | **the owner**, at their own terminal, on a throwaway installation — **OWNER-RUN, NOT RUN** |
| 621–629 | **reserved and unused.** A Bitbucket end to end is the obvious second (§6 of the reconciliation), and it needs one of these numbers when the owner asks for it | — |

Nothing in 620 is run by an agent, and no step of it is a claim this plan makes. **Until it runs, every one of the four verbs is built and unverified live.**

## 6. What this plan does not own

- **`fugaro doctor`, its checks and its render.** RE1 **reuses** the checks (F12) and adds no check file and no second reader of a fact doctor already computes.
- **`fugaro update`, its stages and its retire flow.** RE3 runs the `Repository` stage's own engine and RE4 the retire path; **neither edits them**. A task that finds itself needing a line inside `internal/cli/init.go`, `internal/infra/retire.go` or `internal/cli/init_retire.go` **stops and reports**.
- **The published policy's other writers.** `fugaro project policy publish`, `fugaro project sync` and `fugaro config publish` are other commands with their own designs. RE3 and RE4 **call** `putAllowRepos`; they do not change it, and they add no flag to a neighbour.
- **`fugaro project repos set`.** D1173's command is untouched. RE3 writes the same document through the same settings writer; if the writer needs a surface it does not have, that is a contract change to the settings design, raised before it is made.
- **`belong`, and any live action by an agent.**
- **`docs/status.md`.** Its `repos-management` row is this design's own sweep's (A8) and is **no task's file**, and the `--purge` question (D2408) is not this plan's either.

## 7. The slot, and the release notes

The release is the one after the release in flight. **`docs/releases/vX.Y.Z.md`'s `### For operators` is "run `fugaro update`" plus what it will ask** — and this work adds **no operator step**, which is the property D2416 is written to keep: an installation that never runs `add` converges through `update` exactly as it does today, one that runs `add` converges to the same tree, and nothing here needs a backfill, a secret change or a rollout order. Where the release notes need a line, it is the one an owner acts on: **`fugaro project repos status` now says when a repository's credential has no version, and names the command that stores one.** RE5 writes it.

## 8. The edges, in one line each

- **RE0 → RE1, RE2, RE3**: broken by the contract. The three need the *view's* shape, not `add`'s behaviour.
- **RE3 → RE4**: **kept**. `rm` removes what `add` actually writes, store by store; two tasks that must agree on four writers are one change split in two.
- **RE1, RE3 → RE2**: **kept**, and for files only (`console/docs/json-contract.md`, `website/content/cli-reference.md`), which is the one serialization this plan accepts.
- **RE1–RE4 → RE5, RE6**: **kept**. Both read the commands as shipped, and RE5's census pin counts a file RE5 adds.
- **The critical path is RE0 → RE3 → RE4 → RE5 → RE6**; RE1 and RE2 hang off RE0 and are on no path RE4 waits for.
