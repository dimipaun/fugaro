# The CLI on Windows — implementation plan

**Status: plan only; nothing is implemented; decisions open.** This is the
ordered plan that follows [reconciliation.md](reconciliation.md) and answers
[spec.md](spec.md). Every task names the files it touches, the test it must
turn red first, the command that proves it, what it depends on, and what kind
of reviewer it needs. **Nothing here is approved, and no task may be started
before the [OWNER APPROVAL GATE](#owner-approval-gate) at the end is passed.**

The findings below are cited by number (**F1**, **D4**, **C1**) and live in
[reconciliation.md](reconciliation.md) — the whole set is **F1–F42**, the
decisions are **D1–D16** (all open) and the owner-run checks the plan needs are
**U1–U13** in that document's UNVERIFIED table; the ranges are stated here so a
citation in this plan can be read without opening the other document. Where a
task cites a decision, the task is written to the *recommended* option and
states what changes if the owner answers differently.

**The shape of the whole thing, in one paragraph.** The tree already compiles
for Windows in every package the CLI is made of except two (**C1**), it
already carries five documented `unix`/`other` splits and a CRLF-aware YAML
writer and a Windows-aware temp-root list (**C2**), and `internal/cli`'s 224
test files already type-check for windows/amd64 (**F36**). So this is a port
of two packages, four platform lookups, one shell choice, one test harness and
a release matrix — not a rewrite. What makes it *substantial* anyway is that
none of it can be verified inside this repository's CI until a Windows runner
exists, and that four of the things a user notices first (`doctor`'s advice,
the mode claims, the goldens, the TUI) are behaviour, not compilation.

---

## 0. How to read a task

Each task has:

- **Files** — the files it creates, edits or deletes. Nothing else is in
  scope for the task.
- **Red step** — the test or command that must fail *before* the change and
  pass after. If a task has no red step it is a `Rule` task: it exists to keep
  something true, and its verification is the guard test that already owns it.
- **Verification** — the exact command an agent runs. Two of them are used
  constantly and are abbreviated afterwards:
  - **`[pins]`** = `GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'Docs|Lint|CliRef' -count=1 -timeout 40m`
  - **`[cross]`** = `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./... && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`
- **Tier** — who can review it:
  - **Tier A** — an owner or agent with no Windows machine: the change is
    source-readable and verifiable by cross-compilation, a `[pins]` run, or a
    unit test that does not need Windows.
  - **Tier B** — needs a Windows machine or a `windows-latest` runner to
    accept as *done*, though the code can be reviewed anywhere.
  - **Tier C** — owner-only: a decision, a secret, a certificate, a paid
    service or an external account.
- **Depends on** — the tasks that must have landed.

**Documentation tasks.** Five of the tasks below change documents and nothing
else — T0.1 (`docs/windows-cli/wsl.md`), T0.2 (`docs/gcp-setup.md`), T0.3 and
T1.5 (this folder's `README.md`) and T6.3 (its status line) — and the two
bullets above are not relaxed for them: each names a red step below, and each
is owned by a pin test in the style this repository's documentation folders
use. There is no registry over `docs/`, so a folder that wants its documents
pinned writes its own `internal/cli/docs_*_test.go` — the convention
`internal/cli/docs_in_job_review_test.go` states for `docs/in-job-review/` —
and this folder's is `internal/cli/docs_windows_cli_test.go`, whose standing
guards are `TestDocsWindowsCLIIsWritten` (the index, its status line and the
files it names), `TestDocsWindowsCLIFindingsDecisionsAndItemsAreNumbered` (the
F, D, O and U series numbered from 1 with no gap, and the ranges the index
states, checked against the documents' own counts),
`TestDocsWindowsCLISupportTableNamesRealCommands` (§1's Commands column against
the command tree), `TestDocsWindowsCLICensusStatesItsCommands` (§4's rows, each
with its command) and `TestDocsWindowsCLIEveryDocTaskNamesItsGuard` (this
paragraph). T0.2's document lives outside this folder, so its guard is a pin of
its own. A red step for a documentation task is the pin that fails before the
edit and passes after — which is what makes a document a deliverable rather
than a draft.

**Every slice is independently mergeable and leaves `main` green on Linux and
macOS.** That is the plan's hardest constraint and the reason for the ordering
below: no slice changes a POSIX code path, and every slice's `[pins]` and
`[cross]` runs must be green before it merges. Cross-compiling for Windows
does **not** become part of `[cross]`'s contract for the repository until Slice
6; before that it is a review aid that an agent runs and reports.

---

## 1. Support tiers, per command group

The first thing the owner has to be able to state. A tier is a promise to a
reader, so it is per *command group* and it is stated in
`website/data/install.yaml` and `README.md` at the end of Slice 5 (**C3**,
**D15**).

| Group | Commands | Tier at the end of this plan | Why |
|---|---|---|---|
| Identity | `version`, `doctor` | **Supported** | No shell, no cloud, no paths beyond the config dir; `doctor` gains the checks in F26. |
| Reading config | `validate`, `config list`, `config show`, `config layer`, `backends ls` | **Supported** | `internal/config` + `internal/pluginwire`; the two-package fix in Slice 1 is exactly this path. |
| Reading state | `runs ls`, `artifacts ls`, `stats`, `budget show`, `logs`, `diagnose` | **Supported** | Read-only against the bucket, through the Go client libraries, with the ADC path fixed in Slice 1. |
| Watching | `watch` (TUI, `--plain`, `--json`) | **Supported** | Bubble Tea supports Windows (F17, F18); the plain and JSON modes are the escape hatch. |
| Setup | `init`, `secrets`, `accounts`, `allow-delete`, `recipes`, `backends ls`, `project` | **Supported** | The wizard reads the terminal through `x/term` (F14) and has a non-interactive mode; `secrets set` needs a real Windows hidden-prompt check (**U3**). |
| Running | `run` (`--attach`), `plan`, `cancel`, `report`, `logs` (`--follow`), `restore` | **Supported** | Needs the whole auth path and, for `run`, the git and CLI wiring that Slice 4 covers. |
| Repository tooling | `verify` | **Supported, with the shell rule of D7** | Runs the repository's own configured commands; the shell is chosen by platform. |
| Local images | `image build --local`, `image selftest` | **Best effort** | Needs Docker Desktop; unverified until **U10**. |
| In-job | `exec` (hidden) | **Not supported, not shipped as a user command** | Hidden, Linux-only, and untouched (F41). |
| Plugin | `update-skills`, `upgrade --plugin` | **Supported** | Skills only, no hooks (F34), with the shell note of D14. |
| Managed infrastructure | `managed`, `restore` (Terraform half) | **Best effort** | `terraform` on Windows is fine (F19), the Terraform *state* is not platform-specific, but nothing here is verified on Windows in this plan. |
| Console | — | **Out of scope** | `docs/design/desktop-console.md` D10 owns it (F40). |

**"Best effort" is a promise too**, and it is the one this plan can least
defend, for the reason `D15` gives: it is the status hardest to keep honest.
The plan therefore uses it twice and only where the dependency is a
third-party product (Docker Desktop, Terraform) rather than Fugaro's code.

---

## 2. The minimum viable first cut

**Slice 0 and Slice 1, reviewed together.** Slice 0 is a document. Slice 1 is
94 lines-ish across five files and two new ones, and it is the whole distance
between `fugaro.exe` not existing and `fugaro.exe --version` printing a
version on Windows. Everything after it is behaviour.

**What the first cut does *not* do**, said plainly, because the temptation is
to call it "the CLI works on Windows" and it is not: `fugaro doctor` still
stays silent about a missing `git` and `gcloud` (F26); the printed Fix
commands are still POSIX (F6); `fugaro secrets set`'s hidden prompt is still
unverified (**U3**); the goldens still break under Git for Windows' default
CRLF handling (F20); `fugaro verify` still wants a `sh` (F3); no Windows
archive is published (F31); and no Windows CI job exists (F38). The first cut
is "it builds, it starts, and the config it reads is the right file".

**Why that is the right first cut rather than a bigger one.** It is the
largest change whose correctness an owner with no Windows machine can accept
from a Linux box: `[cross]` proves the two packages compile, and the config-dir
and ADC-path changes are ordinary Go with unit tests that run everywhere. It is
also the change that makes every later slice testable on a runner, because a
runner cannot run a binary that does not build.

---

## 3. The slices

### Slice 0 — Say what works today

*Goal: a Windows reader is told the WSL route, today, and told the truth about
the native CLI. Mergeable now, before any decision on D1 is answered — it is
worth doing under every answer.*

**T0.1 — `docs/windows-cli/wsl.md`.** Files: `docs/windows-cli/wsl.md` (new),
`docs/windows-cli/README.md` (the row). Red step:
`internal/cli/docs_windows_cli_test.go`'s `TestDocsWindowsCLIIsWritten` — add
`[wsl.md](wsl.md)` and its index row to that guard's file list first, then run
it and watch it fail on the document that is not written yet. This is a
documentation task, and a `Rule` task: its pin is its red step. Verification: `[pins]`, which now includes
`GOFLAGS=-p=2 go test ./internal/cli -run TestDocsWindowsCLIIsWritten -count=1`,
plus `grep -c 'wsl.md' docs/windows-cli/README.md`. Tier A. Depends on:
nothing.
Content: what WSL is for, that a Linux `fugaro` inside WSL is the Linux path
exactly (F39), the two traps (a checkout under `/mnt/c` has no real file modes
and is slow; gcloud's ADC inside WSL is a *different file* from the Windows
user's), that Claude Code must be installed inside WSL for the skills to find
a `fugaro`, and that Fugaro does not support the console on Windows (F40).
Commit: `docs(windows-cli): the WSL route, which works today`.

**T0.2 — the Windows text `docs/gcp-setup.md` is missing.** Files:
`docs/gcp-setup.md`, `internal/cli/docs_gcp_setup_test.go` (**new**) — no
guard pins that guide at all today — plus `internal/cli/docs_*.go` if a guard
turns out to pin its headings (check before editing). Red step:
`internal/cli/docs_gcp_setup_test.go`'s `TestDocsGCPSetupNamesTheWindowsForm`:
write the pin first, naming the two commands in the Windows and WSL forms the
guide must carry, and watch it fail against today's guide. This documentation
task's guard does not exist yet, so the guard is the task's first
deliverable. Verification:
`GOFLAGS=-p=2 go test ./internal/cli -run DocsGCPSetup -count=1`, then
`[pins]`. Tier A. Depends on: **D1** (if the answer is WSL-only, this task is
the *only* one that ships). Content: the two commands the guide has a user run
(`gcloud auth application-default login` and the Fix commands), in a Windows
form and a WSL form, without deciding F6 for the CLI's own printed text.
Commit: `docs(gcp-setup): the Windows and WSL forms of the two commands the user runs`.

**T0.3 — state the plan's own status where a reader looks.** Files:
`docs/windows-cli/README.md`. Red step:
`internal/cli/docs_windows_cli_test.go`'s `TestDocsWindowsCLIIsWritten` asserts
the status line word for word, so the change and its evidence arrive together —
move the line in the guard first, watch the guard fail against the unchanged
`README.md`, then move the document and the line together. This is a documentation task:
it changes this folder's index and nothing else. Verification:
`[pins]`. Tier A. Depends on: T0.1. This is the slice's index task and it is what makes the first cut
reviewable as a unit.

---

### Slice 1 — Compile, start, and read the right files

*Goal: `fugaro.exe --version` runs on Windows, `fugaro config ls` reads the
file the user's other tools would read, and `fugaro doctor` sees the ADC the
user actually has.*

**T1.1 — `internal/pluginwire`: the `unix`/`other` split (F1).** Files:
`internal/pluginwire/fsx.go` (edit), `internal/pluginwire/openfile_unix.go`
(new), `internal/pluginwire/openfile_other.go` (new). Red step:
`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./internal/pluginwire` —
today it fails with `undefined: syscall.O_NOFOLLOW`. Verification: the same
command passes, then `[cross]`, then `go test ./internal/pluginwire`. Tier A.
Depends on: nothing. The `unix` half keeps `O_NOFOLLOW` exactly; the `other`
half is `os.O_RDONLY` with a comment that states what is lost (the
`checkPath` `Lstat` and the `SameFile` comparison remain, which is why the
loss is small) — the shape and the comment style of
`internal/blobx/openfile_other.go`, which is the existing precedent (**C2**).
Commit: `pluginwire: O_NOFOLLOW only where it exists, and say what the rest keeps`.

**T1.2 — `internal/procgroup`: the `unix`/`other` split (F2).** Files:
`internal/procgroup/procgroup.go` (edit), `internal/procgroup/procgroup_unix.go`
(new), `internal/procgroup/procgroup_other.go` (new). Red step:
`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./internal/procgroup` —
`unknown field Setpgid` and three `undefined: syscall.Kill`. Verification:
same command, then `[cross]`, then `go test ./internal/procgroup`. Tier A.
Depends on: nothing. The `unix` half is today's code, moved. The `other` half
sets no `SysProcAttr` field and kills the **direct child** only, with the
comment that says so — the exact wording precedent is
`internal/claudeplugin/procattr_other.go` ("only the direct child is killed at
a timeout"). What this loses is real and must be stated in the commit message:
on Windows a grandchild survives a kill. The Windows-correct answer (a job
object, `windows.CreateJobObject` + `AssignProcessToJobObject` through
`golang.org/x/sys/windows`) is **not** in this task and is not in this plan
until **U9** says the direct-child kill is not enough. Commit:
`procgroup: a process group where there is one, the direct child where there is not`.

**T1.3 — the config directory (F5, D8).** Files:
`internal/localcfg/localcfg.go` (`configDir`), `internal/localcfg/checkcache.go`
(`cachePath`), `internal/updatecheck/updatecheck.go`, `internal/infra/workdir.go`
(`workdir`), plus a new `internal/localcfg/platformdirs.go` holding the one
fallback function. Red step: a new table test — `internal/localcfg/platformdirs_test.go`'s
`TestConfigDirFollowsThePlatform` — that fails today because the fallback does
not exist; and `internal/localcfg/localcfg_test.go` gains the case where
`HOME` is empty and `USERPROFILE` is set. Verification:
`go test ./internal/localcfg ./internal/updatecheck ./internal/infra`, then
`[cross]`. Tier A. Depends on: nothing. The rule the helper encodes, and the
comment that must state it: `XDG_CONFIG_HOME` when set (unchanged, the escape
hatch); else the platform's own config directory (`%APPDATA%` on Windows,
`~/.config` elsewhere); caches to `%LOCALAPPDATA%` on Windows and
`$XDG_CACHE_HOME`/`~/.cache` elsewhere; state to `%LOCALAPPDATA%` and
`$XDG_STATE_HOME`/`~/.local/state`. Every existing error message that names
`HOME` is reworded to name the *directories* it tried, so a Windows user's
refusal is actionable. Commit: `localcfg: the platform's own config and cache directories, XDG still first`.

**T1.4 — the ADC path (F29).** Files: `internal/cli/init_project.go`
(`adcInfo`), `internal/cli/managed_apply.go` (`adcFile`),
`internal/cli/init_inputs.go` (`adcPath`), and the shared path builder the
three should have been sharing. They are not three copies of one thing today:
`adcInfo` and `adcFile` both try `$CLOUDSDK_CONFIG`, then
`$XDG_CONFIG_HOME/gcloud`, then `~/.config/gcloud`, while `adcPath` has **no
`$XDG_CONFIG_HOME` step at all** — it goes straight from `$CLOUDSDK_CONFIG` to
`os.UserHomeDir()/.config/gcloud`. All three are wrong on Windows at the
`.config/gcloud` segment; only the two are wrong about XDG. F29 states this. Red step: a new
test in `internal/cli` that sets `APPDATA` and no `XDG_CONFIG_HOME` and
asserts the path is `<APPDATA>/gcloud/application_default_credentials.json`;
it fails today. Verification: `go test ./internal/cli -run 'ADC|Init|Managed' -count=1`,
then `[pins]`, then `[cross]`. Tier A. Depends on: T1.3 (the same helper
decides where a config directory is). `$CLOUDSDK_CONFIG` stays first and
unchanged — it is gcloud's own documented override and it is correct on every
platform. **U11** is the check that `%APPDATA%\gcloud` is right for the pinned
gcloud version; if it is not, this task's answer changes and nothing else does.
Commit: `cli: gcloud's ADC file where gcloud keeps it, per platform`.

**T1.5 — the first cut's own guard.** Files: `docs/windows-cli/README.md`
(the numbers), plus — if the owner agrees at review — a build-tagged
cross-compile check. Red step:
`internal/cli/docs_windows_cli_test.go`'s
`TestDocsWindowsCLIFindingsDecisionsAndItemsAreNumbered` checks the ranges the
index states against the documents' own counts, so a headline figure tightened
without the document that carries it fails it — tighten the figure in the guard
first and watch it fail. This is a documentation task whose subject is the
first cut's own numbers. Verification: `[cross]` must be clean from a Linux
checkout, and the commit message records the command and its output. Tier A. Depends on: T1.1–T1.4. **Do not add
`GOOS=windows` to `fugaro.yaml`'s `commands.build` here**: that would make
every contributor's `fugaro verify build` cross-compile, which is a different
decision (it is Slice 6's, and it costs every Linux build a Windows build's
worth of time). Commit: `docs(windows-cli): the first cut, and the exact command that proves it`.

---

### Slice 2 — The read-only commands tell the truth

*Goal: the commands a Windows user runs first give Windows advice.*

**T2.1 — `doctor` learns the two tools it never checked (F26).** Files:
`internal/cli/doctor.go`. Red step: a table test that fails today because
there is no git row and no gcloud-binary row. Verification:
`go test ./internal/cli -run Doctor -count=1`, `[pins]`, `[cross]`. Tier A
(the check itself is `exec.LookPath`, verified correct on Windows by F13;
**U2** confirms it against a real `PATH`). Depends on: T1.4 (the gcloud row
should name the ADC file too, and its path is T1.4's). The rows to add: `git`
(the CLI assumes it everywhere and reports "not in a checkout" when it is
missing — a misleading failure); `gcloud`, as a *binary* presence check,
separate from the ADC *file* check that exists, with the fix being
`gcloud auth application-default login` and the note that the CLI itself never
execs gcloud (F28). Commit: `doctor: check the two tools it assumed, and say which of the two gcloud facts is missing`.

**T2.2 — the printed commands get a platform (F6, F27).** Files:
`internal/shellword/shellword.go` (a platform-aware entry point beside the
existing `Quote`), the sites that print a command to be pasted — **15** direct
`shellword.Quote` call sites and the **51** `quoteWord` call sites that reach
it through `internal/cli/init_repo_target.go`'s alias, **24 files** between
them (F6 states the three commands that produce those figures, and they are
volatile state: re-run them, do not quote them) — `internal/cli/doctor.go`,
`internal/preflight/preflight.go`. Red step: a table test asserting
`QuoteFor("windows", "it's")` and the `cmd.exe` case; it fails today. Tier A
for the code, **Tier B** to accept *done*, because the only real proof is
pasting the string into `cmd.exe` and PowerShell. Verification:
`go test ./internal/shellword ./internal/cli ./internal/preflight`, `[pins]`,
`[cross]`. Depends on: nothing. Two sub-choices with different costs, and the
task should take the cheap one first: (a) make the quoting platform-aware
(correct, and it means `Quote`'s callers must pass the platform down: a
signature change at 15 call sites in 6 files, or the same effect for the 51
that go through it by changing the one-line `quoteWord` alias at
`internal/cli/init_repo_target.go:102`, which is the cheap shape of this
option); or (b) keep POSIX quoting and reword the
*prose* Fix strings that are not commands — "unset K" becomes "remove the
variable K from this shell's environment" — which touches no signature and is
what the two prose sites in F27 need anyway. The plan does (b) in this task
and leaves (a) as a follow-up named in the task's own commit message, unless
the owner says otherwise. Commit: `doctor: advice that names the variable instead of assuming a POSIX shell`.

**T2.3 — the ANSI gate (F16, D9).** Files: `cmd/fugaro/main.go` (or a small
`internal/cli/console_windows.go` / `console_other.go` pair), the four files
that emit escapes. Red step: none that is runnable on Linux — this is the one
task whose red step is a **Windows** run (**U6**), and the task must say so
rather than invent a Linux test for a Windows console. Verification:
`go test ./internal/watch ./internal/cli -run 'Render|Runs' -count=1`, `[pins]`,
`[cross]`, and **U6** on Windows. Tier B. Depends on: T1.1, T1.2 (nothing runs
before it builds). The change: enable virtual-terminal processing on
`os.Stdout`'s console handle once at startup (a `unix`/`other` pair, the same
shape again), keeping `--no-color`/`NO_COLOR`/`TERM=dumb` (which
`internal/watch/tui.go`'s `UseNoColor` already honours) and `--ascii`
(`UseASCII`) as the escape hatches. Commit: `cli: turn on VT processing where the console needs asking`.

**T2.4 — reserved names and the long-path report (F25, F24, D13).** Files:
the project-name validator (wherever `localcfg.ProjectPath`'s `[a-z0-9-]` rule
lives — check before editing), `internal/cli/doctor.go`. Red step: a test that
a project named `con` is refused with the reason; it fails today. Verification:
`go test ./internal/cli ./internal/localcfg -count=1`, `[pins]`, `[cross]`.
Tier A. Depends on: T2.1. Two small pieces: the validator refuses Windows'
reserved device names (they are refused by the *providers* as repository names
too, so this is consistency, not a new rule — **O2**'s case-folding question is
**not** this task); and `doctor` prints the deepest path it would create and
whether it is near the limit, which is the check T2.4 itself cannot perform on
Linux (**U12**). Commit: `cli: refuse a project name Windows reserves, and report the deepest path doctor would create`.

---

### Slice 3 — The test suite's four Windows classes

*Goal: a Windows checkout's `go test ./internal/cli` fails for reasons that
are about the change under test, and not for four platform reasons that drown
it out.*

**T3.1 — `.gitattributes` (F20).** Files: `.gitattributes` (new). Red step:
none on Linux; the failure is on Windows (**U7**) and the task must say so.
Verification: `git check-attr text eol -- internal/cli/testdata/init_commands.golden`
must answer `text: set`, `eol: lf`; and `git ls-files --eol internal/cli/testdata`
must show `lf` for every golden on a machine with `core.autocrlf=true` — the
second is the real check and it is **Tier B**. Tier A to review, Tier B to
accept. Depends on: nothing — and it is deliberately *first* in this slice,
because until it lands every other Windows test result is noise. Content:
`* text=auto eol=lf`, which is the repository-wide answer and is what the
twelve goldens and the `docs/` corpus both need; a narrower pattern
(`internal/cli/testdata/*.golden`) is not enough, because
`internal/cli/docs_*_test.go` compares documents byte-for-byte too. Commit:
`gitattributes: LF in the working tree, so the golden files compare and the docs pins hold`.

**T3.2 — the mode assertions (F9, D12).** Files: the 18 test files F9 lists,
plus one shared helper. Red step: on Linux every one of the 28 assertions
still passes (this task must not change Linux behaviour), and the new helper
has its own test asserting it skips under a Windows-ish condition — an
injected predicate, not `runtime.GOOS`, so it is testable on Linux.
Verification: `go test ./internal/localcfg ./internal/pluginwire ./internal/watch ./internal/cli -count=1`,
then `[pins]`, then `[cross]`. Tier A. Depends on: T3.1. The helper skips with
the **one** stated reason from D12 ("Windows reports 0444 or 0666 for every
file; the mode guarantee is POSIX"), and the same commit states in the prose
that owns the claim (SECURITY.md or the design document that makes it — check
which before editing) that the mode guarantee is POSIX-only and that the
Windows protection is the directory ACL, which Fugaro does not set. Commit:
`tests: mode assertions where modes exist, and the claim they belong to`.

**T3.3 — the symlink helper (F11).** Files: a new helper in
`internal/testutil`, and the 19 sites in `internal/cli` plus the four more in
`internal/config`, `internal/blobx`, `internal/cache`. Red step: the helper's
own test injects the Windows failure and asserts the skip message; it fails
before the helper exists. Verification: the same package list as T3.2 plus
`[cross]`. Tier A. Depends on: T3.1. The skip reason is stated once:
`A required privilege is not held by the client` — Developer Mode, or an
elevated process, is what Windows needs to create a link. **Do not** replace
the guard tests with something weaker: the guards are `Lstat`/`EvalSymlinks`
reads (F10) and they keep working on links that exist; only *creating* the
link needs the privilege. Commit: `tests: one place that says why a Windows test cannot create a link`.

**T3.4 — the fake-command harness (F12, D11).** Files:
`internal/testutil/fakecmd.go`, `internal/testutil/fakecmd_windows.go` (new),
`internal/testutil/fakecmd_other.go` (new, holding today's `/bin/sh` writer),
`internal/testutil/fakecmd_test.go`. Red step: `internal/testutil`'s own test
must exercise both halves; the Windows half's red step is its absence.
Verification: `go test ./internal/testutil ./internal/cli ./internal/watch -count=1`,
`[pins]`, `[cross]`. Tier A to review, Tier B to accept (the four call sites'
tests only really run on Windows). Depends on: T3.1, T3.3. The API
(`FakeCommand`, `PrepareFakeCommands`) does not change, which is what makes
the four call sites' port a no-op — the same reason `internal/cli/tempxdg.go`
and the `blobx` pair are shaped the way they are (C2). Commit:
`testutil: a fake command that a Windows PATH can find`.

**T3.5 — the `syscall` tests outside `internal/cli` (F36).** Files:
`internal/budget/rules/harness_test.go`, `internal/gitops/gitops_test.go`,
`internal/procgroup/procgroup_test.go`, `internal/testutil/git_test.go`,
`scripts/release_test.go`, `internal/updatecheck/updatecheck_test.go`.
Red step: `[cross]` — today these are three of the five vet failures, and
each is a `syscall.Kill`/`Setpgid`/`Mkfifo` that becomes a `unix`-gated test
file (a `_unix_test.go` rename or a `//go:build unix` line) with the reason
stated. Verification: `[cross]` clean, then `[pins]`, then the five packages'
own tests on Linux. Tier A. Depends on: T1.2 (the `procgroup` split is what
`procgroup_test.go` and `release_test.go` are testing). Commit:
`tests: the process-group and fifo tests, where those exist`.

---

### Slice 4 — Setup and launch

*Goal: the wizard, the secret prompt, `verify` and `run` on Windows.*

**T4.1 — `fugaro verify`'s shell (F3, D7).** Files:
`internal/verify/verify.go` (the `procgroup.Cmd{Name: "sh", Args: []string{"-c", command}}`
site), plus a `shell` helper with a `unix`/`other` pair. Red step: a test
asserting the command line the helper builds on each platform; it fails
today because there is no helper. Verification: `go test ./internal/verify -count=1`,
`[pins]`, `[cross]`. Tier A, **Tier B** to accept (running `fugaro verify test`
on Windows is the only real proof). Depends on: T1.2. Under the recommended
D7 the Windows half honours `COMSPEC` and passes `/c`, and the commit message
says the consequence out loud: a `commands:` block in `fugaro.yaml` is
platform-specific free text, and a repository that wants one config for both
writes two. Commit: `verify: the platform's own shell, and COMSPEC where it is set`.

**T4.2 — the hidden prompt on Windows (F14, U3).** Files:
`internal/cli/secrets.go`, `internal/cli/initsecrets.go`, the three `pty` test
files. Red step: none on Linux — **U3** on Windows, and the task says so.
Verification: `go test ./internal/cli -run 'Secrets|Prompt' -count=1`, `[pins]`,
`[cross]`, then **U3** by hand in three terminals. Tier B. Depends on: T2.3
(the same console question). Expected to work unchanged, because
`x/term` implements `MakeRaw`/`ReadPassword` on Windows — the task is mostly
verifying that, and the `pty` tests stay `darwin || linux` gated (F14) rather
than being ported, with the reason in their build tag line. Commit:
`cli: the hidden prompt on Windows, and the pty tests it does not need`.

**T4.3 — the wizard and `init` (D10).** Files: `internal/initflow`, the
`internal/cli/init*.go` family, `internal/cli/init_project_unix_test.go` (which
already carries `//go:build !windows`, **C2** — it must be *kept*, not
replaced, and its reason re-read). Red step: **U9**'s failure list, after
Slice 3 removes the four known classes. Verification: `go test ./internal/cli -run Init -count=1`,
`[pins]`, `[cross]`, and the non-interactive paths
(`--yes`, `--non-interactive`, `--json`) on Windows. Tier B. Depends on: T1.3,
T1.4, T3.2, T3.3. Commit: `init: the wizard on Windows, non-interactively first`.

**T4.4 — `run` and the attach path (F41's neighbourhood).** Files: whatever
**U9** names; nothing is predicted here, and the task is written to inherit
U9's list rather than to guess at it. Verification: `[pins]`, `[cross]`, and a
real run from Windows against a real project (**owner-owned, and the one task
in this plan that needs a live cloud action** — it is out of scope for Fugaro
runs, which never touch a live cloud). Tier B, owner-run. Depends on: T4.1,
T4.2, T4.3. Commit to be written once U9 has run.

---

### Slice 5 — Release and distribution

*Goal: a Windows archive exists, is verifiable, and the site says so.*

**T5.1 — `windows` in the build (F31, D2).** Files: `.goreleaser.yaml`. Red
step: a local `goreleaser release --snapshot --clean` (or `goreleaser build
--snapshot`) that today produces no Windows artifact; the task must not merge
without one. Verification: `goreleaser check`, then a snapshot build, then
`unzip -l dist/fugaro_*_windows_amd64.zip` showing `fugaro.exe`, `LICENSE`
and `README.md`. Tier A. Depends on: Slice 1 (a `goos` entry that cannot
build is worse than none). Content: `goos: [darwin, linux, windows]`; an
`archives[0].format_overrides` entry for `windows` producing `zip` — required,
because the explicit `formats: [tar.gz]` overrides GoReleaser's own
windows-is-zip default (F31); SBOMs, `checksums.txt` and the cosign bundle
need nothing, and the commit message says so rather than leaving it to be
checked. amd64 only, per D2. Commit:
`release: a Windows zip, and the SBOM, checksum and signature it already gets`.

**T5.2 — the three-file platform claim (C3, F35, D15).** Files:
`website/data/install.yaml`, `README.md`, `docs/release.md`, and — in the same
commit, because `TestWebsiteInstallFactsNameNoUnbuiltPlatform` requires
it — nothing else. Red step: `[pins]` — today the install facts and
`.goreleaser.yaml` agree that Windows is untested; the moment T5.1 lands, they
disagree and the guard test fails. That failure **is** the red step.
Verification: `[pins]` green again after the edit. Tier A. Depends on: T5.1.
Content: Windows moves from `untested` to `supported` with its own install
path (the `go-install` path's platform list too), the prose in `README.md`'s
requirements line stops saying "Windows: untested", `docs/release.md` names
the `goos` list correctly, and each install path's `platforms:` list stays
pinned to its own authority as C3's rule 5 states — only the `archive` path's
list is the build's `goos` exactly, the other three follow `README.md` and
their own markers, so a path whose list has to change is a change to the
authority it follows first, and — per D15 — WSL gets its own `paths:` entry
rather than a note. If the owner answers D15 the other way (a third "best
effort" status), this task grows a guard-test change and the site's renderer,
and that cost belongs in the answer. Commit:
`release: Windows in the platform list, the install facts, and the requirements line`.

**T5.3 — Scoop (D3).** Files: a bucket manifest (owner-owned — likely
`dimipaun/scoop-bucket`, the counterpart of `dimipaun/homebrew-tap`), and
`.goreleaser.yaml`'s `scoops` block if GoReleaser publishes it. Red step:
none until a bucket exists. Verification: `scoop install fugaro` on Windows
(**owner-run**). Tier C. Depends on: T5.1. What the manifest needs from the
release, and what T5.1 must therefore be asked to keep producing: the archive
URL's shape (which the `name_template` fixes, F31) and the SHA-256 from
`checksums.txt`. Windows and amd64 only, per D2. Commit (in the bucket
repository, by the owner): `fugaro 0.10.0`. **If the owner answers D3 as
"neither, just the archive", this task is dropped and T5.2's install path is
the archive plus a `PATH` note.**

**T5.4 — what the site's install page shows a Windows reader, and the
unblock step (F33's cheap half).** Files: `website/data/install.yaml` (the
path's own text), `README.md`. Red step: `[pins]`. Verification: `[pins]`,
then the site's own build. Tier A. Depends on: T5.2, and **D4** — under the
recommended "unsigned first", this task states the SmartScreen behaviour and
the SHA-256 verification honestly instead of implying a signed experience.
Commit: `docs: what Windows says before it runs a downloaded fugaro`.

**T5.5 — Authenticode, if D4 says so.** Files: `.goreleaser.yaml`'s `signs`
(a second entry, Windows-specific), and a **new** `.github/workflows/` file
that the owner creates — Fugaro runs cannot write there (F38), so if this
task is ever taken it is staged as `docs/pending-workflows/` content and
copied in, exactly as the console's release pipeline was (`O4`). Tier C,
owner-only: the certificate, its key and the protected workflow are all
owner work. Depends on: D4 = "sign now", T5.1. This task has no
recommendation — D4 does.

---

### Slice 6 — CI, and the gate

*Goal: Windows stops being a claim and becomes a check.*

**T6.1 — the Windows job, staged (F38).** Files:
`docs/pending-workflows/windows-tests.yml` (new) and
`docs/pending-workflows/README.md` (its row). **No file under
`.github/workflows/` is created, edited or deleted by any Fugaro run**, and
the staged file is the *complete* intended content for the owner to copy, not
a diff — the convention `docs/pending-workflows/README.md` states and
`TestConsoleTestsWorkflowIsStagedAndPinned` enforces (`O4`). Red step:
`[pins]` — `docs/pending-workflows/README.md` has guard tests that name its
rows (`TestDocsPendingWorkflowsReadmeNamesTheConsoleRows` and its sibling
`TestPendingWorkflowsReadmeNamesTheConsoleTestsRow`, both in
`internal/cli/docs_desktop_console_test.go` — an earlier draft of this task
named a test that does not exist), so a
new row without its name in the right places fails them. Verification:
`[pins]`, plus a YAML parse of the staged file. Tier A to write, **Tier C to
install** (`.github/workflows/` is owner-only). Depends on: Slice 3 (a job
that runs before the four known classes are fixed only ever fails) and **D5**
(advisory or gating). Content: `runs-on: windows-latest`, `actions/setup-go`
pinned to the toolchain `mise.toml` and `go.mod` name, `go build ./...`,
`go vet ./...`, and the suite **minus** the packages `fugaro.yaml` already
excludes for the container's reasons and minus `internal/runner` and
`internal/e2e`; `GOFLAGS=-p=2` for the same memory reason this repository's
own container runs use it. Cost, stated for D5: `windows-latest` bills at
twice the Linux rate and a full suite is the same wall-clock as Linux's, so
this job roughly doubles the CI bill for the matrix leg it adds. Commit:
`docs(pending-workflows): the Windows test job, for the owner to copy in`.

**T6.2 — the flip (D5).** Files: the staged workflow again, and the repository
once the owner has copied it. Verification: one green run on `main`, then one
green run on a pull request. Tier C. Depends on: T6.1 installed, and **U9**
having produced an *empty* remaining-failure list — that is the entry
condition, not "the job is green", because the job can be green while running
nothing. Commit: `ci: the Windows job gates, one release after it went green`.

**T6.3 — the support tier goes into the docs' own convention.** Files:
`docs/windows-cli/README.md` (the status line changes from `plan only; nothing
is implemented; decisions open` to the tier the work actually reached). Red
step: `internal/cli/docs_windows_cli_test.go`'s `TestDocsWindowsCLIIsWritten`,
which pins the status line word for word — write the new line into the guard
first, watch it fail against the document that still carries the old one, and
land the two together with the evidence that licenses the move. This is a
documentation task, and the last one in the plan. Verification:
`[pins]`. Tier A. Depends on: everything the
status line claims. This task exists because the status line is a promise the
same way `website/data/install.yaml`'s platform list is, and it must not be
moved earlier than the evidence.

---

## 4. Edges, levels and the critical path

| # | Task | Depends on | Level |
|---|---|---|---|
| 1 | T0.1 WSL route | — | 0 |
| 2 | T0.2 gcp-setup Windows text | D1 | 0 |
| 3 | T0.3 README status | T0.1 | 1 |
| 4 | T1.1 pluginwire split | — | 0 |
| 5 | T1.2 procgroup split | — | 0 |
| 6 | T1.3 config directories | — | 0 |
| 7 | T1.4 ADC path | T1.3 | 1 |
| 8 | T1.5 first-cut guard | T1.1–T1.4 | 2 |
| 9 | T2.1 doctor's two tools | T1.4 | 2 |
| 10 | T2.2 platform prose | — | 0 |
| 11 | T2.3 ANSI gate | T1.1, T1.2 | 1 |
| 12 | T2.4 reserved names, long path | T2.1 | 3 |
| 13 | T3.1 `.gitattributes` | — | 0 |
| 14 | T3.2 mode assertions | T3.1 | 1 |
| 15 | T3.3 symlink helper | T3.1 | 1 |
| 16 | T3.4 fake-command harness | T3.1, T3.3 | 2 |
| 17 | T3.5 syscall tests | T1.2 | 1 |
| 18 | T4.1 verify's shell | T1.2 | 2 |
| 19 | T4.2 hidden prompt | T2.3 | 2 |
| 20 | T4.3 wizard and init | T1.3, T1.4, T3.2, T3.3 | 3 |
| 21 | T4.4 run and attach | T4.1–T4.3 | 4 |
| 22 | T5.1 windows in the build | Slice 1 | 2 |
| 23 | T5.2 three-file platform claim | T5.1 | 3 |
| 24 | T5.3 Scoop | T5.1 | 3 |
| 25 | T5.4 unblock text | T5.2 | 4 |
| 26 | T5.5 Authenticode | D4, T5.1 | 3 |
| 27 | T6.1 staged Windows job | Slice 3, D5 | 3 |
| 28 | T6.2 the flip | T6.1, U9 | 5 |
| 29 | T6.3 the status line | all | 6 |

**Level 0 is five tasks and they are genuinely independent**: T0.1 (a
document), T1.1 and T1.2 (the two compiler failures, two different packages),
T1.3 (the config directories) and T2.2 (the prose). **All five are Tier A** and
can be read and merged without a Windows machine; T2.2 is the one whose
*acceptance* is Tier B (a printed command is only proven by pasting it into a
real shell), which is why §5 counts it as B.

**The critical path is T1.1 → T3.x → T4.3 → T4.4**, and the reason is worth
stating: nothing runs on Windows until T1.1 and T1.2 are both in, the four
test classes have to be cleared before a Windows run says anything, and the
wizard is the last thing that depends on all of it. Slice 5 and Slice 6 hang
off the side — T5.1 needs only Slice 1, so **a release could publish a Windows
archive before the TUI or the wizard were verified**, and D5's answer decides
whether that is acceptable (the plan's own answer is no: the tier in §1 is not
promised until T6.2, and T5.2's commit is what moves the platform list).

**What the critical path is *not* blocked by**: any AWS, GCP, Firebase,
Terraform or signing credential. Every task except five is verifiable on a
Linux box plus, at the end, one `windows-latest` runner: **T4.4** (an
owner-run live run), **T5.3** and **T5.5** (an owner account, a bucket and a
certificate), and **T6.1** and **T6.2** (which need the owner to copy the
staged job into `.github/workflows/`, because no Fugaro run may write there).
That is deliberate — the owner is the only person who can run those five, and
the plan must not stall on them.

---

## 5. Effort, in tasks

Not days. A task is one commit's worth of work with one verification, and the
count is what matters for the decision the owner is making.

**The counting rule, stated once so the table and the task texts cannot drift:**
a task is counted at the **highest** tier it needs, which is the tier needed to
accept it as done. Five tasks below are split — their code is Tier A but they
cannot be *signed off* without a Windows run — and each says so in its own
text ("Tier A for the code, Tier B to accept"). Those five are counted here as
Tier B or C, not as Tier A, because this table answers "who signs this off"
rather than "who can read the diff".

| Slice | Tasks | Tier A | Tier B | Tier C |
|---|---|---|---|---|
| 0 — say what works today | 3 | 3 | — | — |
| 1 — compile, start, right files | 5 | 5 | — | — |
| 2 — read-only tells the truth | 4 | 2 | 2 | — |
| 3 — the test suite's four classes | 5 | 3 | 2 | — |
| 4 — setup and launch | 4 | — | 3 | 1 (owner-run) |
| 5 — release and distribution | 5 | 3 | — | 2 |
| 6 — CI and the gate | 3 | 1 | — | 2 |
| **Total** | **29** | **17** | **7** | **5** |

The split tasks, named so the count can be checked: Slice 2's T2.2 (Tier B to
accept), Slice 3's T3.1 and T3.4, Slice 4's T4.1 — plus T4.4, which is Tier B
*and* owner-run, and is counted once, as C.

**Seventeen tasks need no Windows machine to accept.** Seven need a Windows
machine or a runner. Five are owner-only — T4.4, T5.3, T5.5, T6.1 and T6.2 —
and three of those five are decided purely by D3, D4 and D5, which is the
honest summary of this plan: the code is seventeen tasks, and the
*distribution* questions are where the owner's time actually goes.

**The smallest useful answer is 8 tasks** (Slice 0 and Slice 1) and it makes
`fugaro.exe --version` work. **The smallest *defensible* answer is 24** — every
slice except the release: Slices 0–4 (21 tasks) plus Slice 6's Windows job (3
tasks), which is a native binary that builds, starts, reads the right files and
has a CI job watching it, with nothing published. **The whole thing is 29.**

---

## 6. The live checks this plan needs, and who runs them

The brief asked for the checks the owner must run by hand, with a
GitHub-hosted `windows-latest` runner proposed wherever no secret is involved.
**All thirteen are listed as U1–U13 in [reconciliation.md](reconciliation.md)'s
UNVERIFIED table, with what each one confirms.** Split by who can run them:

| Check | Needs a secret? | Where it runs |
|---|---|---|
| U1 start and print a version | no | a `windows-latest` runner, or any Windows box |
| U2 `LookPath` finds `gcloud.cmd`/`terraform.exe` | no | runner |
| U3 the hidden prompt in three terminals | no | a Windows box (a runner has no interactive terminal) |
| U4 rename-over-open-file and the retry | no | runner |
| U5 the TUI in two terminals, resized | no | a Windows box |
| U6 escapes with VT processing off | no | a Windows box (`cmd.exe`) |
| U7 Git for Windows' CRLF default vs the goldens | no | runner, with `core.autocrlf=true` |
| U8 SmartScreen on an unsigned archive | no | a Windows box, from a real release |
| U9 the whole suite, once, for the full failure list | no | runner |
| U10 Docker Desktop and `image build --local` | no | a Windows box with Docker Desktop |
| U11 where the pinned gcloud keeps ADC | no | a Windows box |
| U12 the deepest path `doctor` would create | no | runner (measured from `doctor`'s own line) |
| U13 the desktop console on Windows | — | out of scope (F40) |

**Every check with a `runner` in that column is a candidate for the T6.1 job
and needs no secret and no owner: six of them** — U1, U2, U4, U7, U9 and U12.
**The remaining seven split as six owner checks and one that is out of scope**:
U3, U5, U6, U8, U10 and U11 need a terminal, a GUI or a product install and
stay at the owner's own machine, and U13 belongs to the console's own design
document (F40). That is the split the brief asked for, and it means T6.1 is not
just a regression gate — it is where six of the thirteen checks live, and the
six it can run are the six that need no human at a screen.

**U9 is the one that gates T6.2 and therefore the whole tier claim.** It is
"run the suite once and read the entire failure list", and it is the check
this plan most explicitly cannot do from here: every failure class in Slice 3
was found by reading, and the plan's own honesty depends on there being no
fifth class. T6.2's entry condition is that U9's list is empty, not that the
job is green.

---

## 7. What could not be verified in this plan, and why

Repeated here so the plan carries its own limits rather than pointing at
another document:

- **Nothing was run on Windows.** The container is Linux.
- **Every behaviour claim** (the `os.Rename` sharing violation, the mode
  reporting, `LookPath`'s `PATHEXT` behaviour, `x/term`'s console
  implementation, Bubble Tea's Windows support) is a claim from Go's and the
  libraries' own source and documentation, not from a run. U1–U12 are how
  each one becomes a fact.
- **No cloud action of any kind was taken**, and T4.4 is the only task in the
  plan that needs one — it is owner-run by construction.
- **The counts in [reconciliation.md](reconciliation.md)'s census are
  volatile** and the commands, not the figures, are the durable part (F37).
- **The `.github/workflows/` change this plan needs is staged, not written**
  (T6.1, F38): Fugaro runs cannot push workflow files, and this plan does not
  try to.

---

## OWNER APPROVAL GATE

**Nothing in this plan may be started until the owner answers, in writing,
the decisions they consider load-bearing, and says which slice to begin.**
The gate is passed when all four of the following are true:

1. **D1 is answered** — native, WSL-only, or both. It decides whether Slices
   1–6 exist at all, so nothing else can be scheduled before it. (Slice 0 is
   worth doing under every answer, and it is the one slice that could be
   approved on its own.)
2. **D2, D3, D4 and D5 are answered or explicitly deferred** — amd64 or both;
   Scoop, winget, both or neither; unsigned or signed first; and whether the
   Windows CI job gates. Each of these sizes a task (T5.1, T5.3, T5.5, T6.1)
   and three of them are the only Tier C work in the plan.
3. **D6 is answered** — the first cut is read-only first, or the whole CLI at
   once. This is the one decision that changes the *order* rather than the
   content, and taking the second option changes the plan's central promise
   ("every slice merges alone and leaves `main` green") into something weaker,
   so it should be answered deliberately rather than by default.
4. **The status line in [README.md](README.md) is still
   `plan only; nothing is implemented; decisions open`.** It moves only in
   T6.3, only as far as the evidence reaches, and only with the owner's
   agreement.

**Answering D7–D16 is helpful but does not gate the gate**: each of those
sizes one task, and a task can be written to its recommended option and
rebased when the answer arrives. D8 (where the config lives), D12 (the mode
claim) and D15 (the platform status) are the three worth answering early,
because they are the three that change what a *user* sees rather than what a
task does.

**The gate is about the work, not about the document.** Reviewing,
correcting or rejecting anything in this plan is not approving a task, and the
owner can take Slice 0 and stop, take Slice 0 and Slice 1 and stop, or answer
D1 as WSL-only and stop — the last two are the two smallest useful answers
§5 prices.
