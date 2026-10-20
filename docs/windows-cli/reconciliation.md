# The CLI on Windows — reconciliation

**Status: plan only; nothing is implemented; decisions open.** This document
reads the brief in [spec.md](spec.md) against the tree at `main`
(`54cb02a8`, `release 0.9.5`) and says what the code, the build, the tests and
the release actually do about Windows today. It corrects the brief where the
tree disagrees with it (**C1–C3**), numbers every finding **F1–F42** and
classifies each **Wrong / Exists / Gap / Rule / Open**, states the census each
count comes from with the exact command beside it, and lists the choices only
the owner can make as **D1–D16** — each with a recommendation first, the
alternative and its cost, and **none taken**. Where this document disagrees
with [spec.md](spec.md), **this document wins**.

**Nothing here is an approval of a task.** Answering a decision is not
approving a task, and no code, config, workflow, Terraform, schema or live
action follows from this document. The gate at the end of
[implementation-plan.md](implementation-plan.md) is not passed.

**Checked against.** `main` at `54cb02a8ea43e74231e42e38f130b5dd37f1563e`,
on 2026-10-20, with the Go toolchain go1.27.1 pinned by `mise.toml` and
`go.mod`. Every path and Go symbol below was located with `git grep`,
`go list` or a build and then read in place; line numbers are never cited.

**The one thing this document cannot tell you** is how much of the tree
*runs* on Windows. Everything below that is a claim about behaviour rather
than about source text is marked in the [UNVERIFIED table](#unverified) and
is a check someone has to run — see §6.

---

## 1. Corrections to the brief

**C1 — "the CLI has no Windows build" is true of the release, not of the
tree.** The sentence in [spec.md](spec.md) is right about what ships: no
Windows archive is published and no Windows platform is supported. It is not
right about the source. Cross-compiling the whole module for Windows today:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

fails in **exactly two packages**, with five errors:

```
internal/pluginwire/fsx.go:71:50: undefined: syscall.O_NOFOLLOW
internal/procgroup/procgroup.go:57:41: unknown field Setpgid in struct literal of type syscall.SysProcAttr
internal/procgroup/procgroup.go:126:15: undefined: syscall.Kill
internal/procgroup/procgroup.go:130:16: undefined: syscall.Kill
internal/procgroup/procgroup.go:135:14: undefined: syscall.Kill
```

The same command with `go vet` (which also type-checks every test file) adds
**three more packages**, all of them test-only:

```
internal/testutil/git_test.go:41:16: undefined: syscall.Kill
internal/updatecheck/updatecheck_test.go:175:20: undefined: syscall.Mkfifo
scripts/release_test.go:745:41: unknown field Setpgid in struct literal of type syscall.SysProcAttr
```

**Consequence.** The first, smallest, most valuable cut of this work is a
port, not a rewrite: unblock two packages, then measure behaviour. It also
means the estimate in [implementation-plan.md](implementation-plan.md) is
*smaller* than the brief's framing implies, and that the plan must say why the
work is still substantial despite the tree compiling — see C2 and §3.

**C2 — the tree is already partly Windows-aware, on purpose.** Five
`_unix.go`/`_other.go` pairs sit behind `//go:build unix` and `//go:build
!unix`, each with a comment that states what is lost on the non-Unix side:

| Pair | What the non-Unix half does |
|---|---|
| `internal/blobx/openfile_unix.go` / `openfile_other.go` | `OpenSource` is `os.Open`; the `fstat` and the bounded copy are the only guards |
| `internal/config/readfile_unix.go` / `readfile_other.go` | `openRegular` is `os.Open`; the `Lstat` and the `SameFile` check are the only guards |
| `internal/runner/openfile_unix.go` / `openfile_other.go` | `oNoFollow` is `0`; `O_EXCL` is what refuses a path something is already at |
| `internal/claudeplugin/procattr_unix.go` / `procattr_other.go` | `isolate` is a no-op; only the direct child is killed at a timeout |
| `internal/cli/initsecrets_core_unix.go` / `initsecrets_core_other.go` | `noCoreDumps` has no equivalent |

Three more places branch on the platform or fold the platform's difference
away:

- `internal/cli/tempxdg.go` — `tempRoots` adds `/var/folders` only on darwin
  and `/tmp`, `/private/tmp` and `/var/tmp` only when `runtime.GOOS !=
  "windows"`. Windows' `os.TempDir()` root is already the first entry.
- `internal/cli/init_fugaroyaml.go` — the `gcp_project:` insertion reads the
  line's own terminator and writes the line back with the file's own EOL,
  `\r\n` included (`eol = "\r\n"`).
- `internal/config/dockerfile.go` — `parseDockerfile` does `strings.TrimRight(raw, "\r")` on
  every line, so a CRLF Dockerfile parses.

And one test file is already gated away from Windows:
`internal/cli/init_project_unix_test.go` carries `//go:build !windows`.

**Consequence.** The design starts from an existing, documented compromise
rather than from zero, and the plan must *keep* the `unix`/`other` split as
its shape rather than inventing a third convention.

**C3 — the position the website already takes, and the three files that hold
it.** `website/data/install.yaml` carries a reader-facing platform list in
which Windows is `status: untested` with the note *"Windows is untested: this
repository builds no Windows binary."* and a `build:` block that mirrors
`.goreleaser.yaml`'s `builds[0]`. `README.md`'s "## Requirements" ends with
"macOS or Linux (Windows: untested). Docker is optional, for local image
builds." `internal/cli/docs_website_install_test.go`
(`TestWebsiteInstallFactsNameNoUnbuiltPlatform`) pins all of it to
`.goreleaser.yaml`: the supported list must equal the built goos list, the
untested list must be exactly the goos names the build does *not* produce, an
unbuilt platform named in any prose block must carry the word "untested", and
each install path's own platform list must be exactly the platforms the build
produces.

**Consequence.** Any change to what Windows *is* is a three-file change
(`.goreleaser.yaml`, `website/data/install.yaml`, `README.md`) plus whatever
`docs/release.md` and the site's install page render, and the pin test will
refuse a half-change. This is a `Rule` for the plan, not a task: it is the
mechanism that keeps the site from promising a platform the release does not
build.

---

## 2. What the brief asked to reconcile, and what the tree says

The headline table. Each row is a subject the brief named; "the tree" is the
finding that answers it.

| Subject the brief named | The tree | Finding |
|---|---|---|
| syscall / `x/sys/unix` in the CLI's packages | Two non-test packages fail to build; `x/sys/unix` appears in **five non-test files, all tests' termios helpers**, and nowhere in the CLI's own code | F1, F2, F14 |
| `os.Chmod`, `0600`/`0700` and the checks that read them | Modes are *written*, never *checked*; on Windows a file is reported `0444` or `0666` whatever was passed | F8, F9 |
| symlinks, `EvalSymlinks`, `Lstat` guards | The guards exist and degrade as the `_other.go` files already say; but `os.Symlink` is a **test** dependency, and Windows needs developer mode for it | F10, F11 |
| `os.Rename` atomicity | `os.Rename` on Windows is `MoveFileEx(…, MOVEFILE_REPLACE_EXISTING)`, so the pattern holds — with two real limits | F3 |
| file locking | **No file locking anywhere in the CLI.** `internal/lock` is a name, not a lock | F4 |
| signals | One `signal.NotifyContext` at the entry point; `SIGTERM` and `SIGHUP` exist as constants on Windows and never arrive | F15 |
| process groups, `exec` of `sh`/`git`/`gcloud`/… | `internal/procgroup` is Unix-only and is on the path of `verify`, `watch` and every git operation; `gcloud` is **never exec'd** | F2, F3, F19, F28 |
| PATH lookup, `.exe`, `.cmd` shims | `exec.LookPath` is the right call and finds `gcloud.cmd`; what it does *not* find is a test's fake named `terraform` | F13 |
| shell quoting | `internal/shellword.Quote` emits POSIX single-quoting for commands printed to be pasted | F6 |
| `/dev/tty`, raw mode, hidden prompts | `golang.org/x/term` only, which is cross-platform; no `/dev/tty` anywhere | F14 |
| ANSI colour, cursor control, terminal width | Escapes in four non-test files; width comes from Bubble Tea, never from an ioctl | F16, F17 |
| `$HOME`/`XDG_*` vs `%APPDATA%`/`%LOCALAPPDATA%` | Four sites read `HOME` and refuse when it is unset — which is the Windows default | F5 |
| temp directories | Already platform-aware (`tempRoots`) | C2 |
| path separators, drive letters, `checkoutProject`, git remotes | `git rev-parse --show-toplevel` and `filepath.Join`; `filepath.ToSlash`/`FromSlash` at 35 sites | F22 |
| case-insensitive file systems | Projects are lowercase-only by construction; the guards that compare paths use string equality | F23 |
| CRLF in files the CLI writes or reads | The writers are CRLF-aware; the **12 golden files** are compared byte-for-byte and there is no `.gitattributes` | F20, F21 |
| long paths, reserved names, Unicode | Nothing addresses them; `internal/infra/workdir.go` builds the deepest paths in the tree | F24, F25 |
| `.goreleaser.yaml`, archives, SBOMs, checksums, Sigstore | One `goos` list, one archive `formats: [tar.gz]`, a macOS-only Homebrew cask | F29, F30, F31 |
| the version ldflags | Platform-independent | F32 |
| `go install` | Works on Windows unchanged | Exists (§3) |
| Authenticode, SmartScreen | Nothing signs Windows binaries; there is no Windows release step at all | F33 |
| the tests | `internal/cli`'s 224 test files type-check for windows/amd64 today; what fails is behaviour, and it is enumerable | F35, F36, F37, F38 |
| docs pin tests that name paths | The docs guards are text and file pins; none of them reads a platform | C3 |
| external tools (`gcloud`, Terraform, Git, Docker, Claude Code) | `fugaro doctor` checks Terraform, Docker and cosign; it has **no git check and no gcloud-binary check at all** | F26, F27, F28 |
| the plugin and its skills | Skills only — there is no `hooks/` and no shell hook anywhere in `plugin/` | F34 |
| WSL | Nothing detects WSL and nothing needs to; a Linux binary inside WSL is the Linux path exactly | F39 |
| how the console follows | `docs/design/desktop-console.md` D10, "Later" | F40 |
| the owner-run live checks | Everything except the four checks that need a real cloud login and a real Windows shell | §6 |

---

## 3. Findings

Each finding names the file and the Go symbol, the class, and what it costs.
**Wrong** means the tree does something that is wrong when read as a Windows
claim. **Exists** means the tree already does the right thing and the finding
records what it guarantees. **Gap** means something is missing.
**Rule** means a conflict with a rule the repository enforces (a guard test,
SECURITY.md, a design document). **Open** means the tree cannot answer it and
the answer is a decision.

### The two packages that do not build

**F1 — `internal/pluginwire/fsx.go`, `readFile`: `os.OpenFile(path,
os.O_RDONLY|syscall.O_NOFOLLOW, 0)`.** Class: **Wrong**. This one expression
is why the CLI does not build for Windows. `internal/pluginwire` is imported
by roughly fifty files under `internal/cli/` (`go list -deps ./cmd/fugaro`
reaches it through the config readers, `doctor`, `init`, `secrets`, `upgrade`,
`validate` and more), so it is not a leaf: it is the package every config
read goes through. `replaceFile` in the same file writes a temporary beside
the target and renames over it, and reads the *current* mode with
`fi.Mode().Perm()` to restore it — see F8 for what that returns on Windows.
Cost: a `unix`/`other` split in `internal/pluginwire`, the repository's sixth,
with the same shape and the same documented loss as the five in C2.

**F2 — `internal/procgroup/procgroup.go`: `syscall.SysProcAttr{Setpgid:
true}` and `syscall.Kill(-pgid, …)`, in `Run` and its kill path.** Class:
**Wrong**. `procgroup` is the CLI's way of running a child so that the whole
tree of processes it leaves behind can be killed, and it is on the path of:

| Caller | What it runs |
|---|---|
| `internal/verify/verify.go` (`procgroup.Run` with `Name: "sh"`) | the repository's `commands.build`, `commands.test`, `commands.lint` |
| `internal/gitops/gitops.go` | every `git` the CLI runs, including `git worktree add` |
| `internal/watch/prstate.go` | the `gh` whose answers the watch TUI shows |
| `internal/agent` (through the runner) | the agent process inside a job |

Cost: a `unix`/`other` split in `procgroup` too, with what the non-Unix half
loses stated in the comment — on Windows a job object is the equivalent of a
process group, and the `_other.go` half must at minimum kill the direct child
and say so, exactly as `internal/claudeplugin/procattr_other.go` already does.

**F3 — `internal/verify/verify.go`, `Run`/`run`: `procgroup.Run(… Name:
"sh", Args: []string{"-c", command}, …)`.** Class: **Gap**. `fugaro verify`
does not run the repository's command directly: it hands it to `sh -c`. On
Windows there is no `sh` unless Git for Windows is installed and on `PATH`,
and even then the configured commands (`GOFLAGS=-p=2 go test ./…`,
`terraform fmt -check`) are written as shell lines in `fugaro.yaml`'s
`commands:` block, which the same YAML feeds to a Linux job. Cost: three
options, and D7 asks the owner to pick. Note also that
`internal/config/validate.go` validates `commands:` as opaque strings, so
nothing in the schema is Unix-specific today.

**F4 — there is no file locking in the CLI.** Class: **Exists**. The brief
asked about it, and the honest answer is that there is nothing to port:
`rg -n 'Flock|LOCK_EX' --glob '!*_test.go'` over the module finds one hit,
`internal/infra/statebucket.go`'s comment about a Terraform `*.tflock` object.
`internal/lock` is the names of things, not a lock — it holds the notion of a
run lock, not a `flock`. The real cross-process mutual exclusion the CLI
relies on is `O_EXCL` (`internal/pluginwire/fsx.go`'s `replaceFile`,
`internal/config/readfile.go`'s guard, `internal/runner/openfile_other.go`'s
comment) and `os.Rename`, both of which behave differently on Windows — see
F8 and F21.

**F5 — `getenv("HOME")`, four sites, no Windows fallback.** Class: **Wrong**.
`internal/localcfg/localcfg.go`'s `configDir`, `internal/localcfg/checkcache.go`'s
`cachePath`, `internal/updatecheck/updatecheck.go` and
`internal/infra/workdir.go`'s `workdir` all read `HOME` directly and return an
error when it is empty:

```
neither XDG_CONFIG_HOME nor HOME is set, so there is no project config directory; pass --config
```

Windows does not set `HOME`. It sets `USERPROFILE`, which is what
`os.UserHomeDir()` reads, and its per-user config and cache directories are
`%APPDATA%` and `%LOCALAPPDATA%`. Cost: the CLI's own project configs and its
three caches would be unreachable on a stock Windows shell, and the error a
user sees names a variable that does not exist on their platform. The fix is
not one line per site: `configDir` is the root of the project config path, so
its answer decides where `fugaro config ls` looks *and* what
`internal/localcfg/localcfg.go`'s `Save` writes. See D8.

**F6 — `internal/shellword.Quote`: POSIX single-quoting, printed for a user to
paste.** Class: **Gap**. `Quote` is correct POSIX and is used by seven call
sites to build text the user is told to run: `internal/infra/hardening.go`,
`internal/infra/projectcreate.go`, `internal/cli/doctor_signers.go`,
`internal/preflight/google.go`, `internal/cli/init_retire.go`,
`internal/cli/init_project.go` and `internal/cli/project_cmd.go`. In
`cmd.exe` a single quote is an ordinary character, so `'-x'` is passed to the
program with the quotes attached; PowerShell treats `'…'` as a literal string,
which is right, but PowerShell's escaping of an embedded `'` is `''` and not
`'\''`, so the `it's` case is wrong there too. The same class covers prose
Fix strings: `internal/preflight/preflight.go`'s `Environment` says "unset
K", and `internal/cli/doctor.go` says "unset it and rerun" — neither is a
Windows command. Cost: the printed commands are the CLI's instruction to a
human, so on Windows they must either be shell-aware or be reworded to name
the variable and let the platform document the syntax.

**F7 — `os.Rename` over an existing target: correct, with two limits.**
Class: **Exists**. Go's `os.Rename` on Windows is
`internal/syscall/windows.Rename`, which calls
`MoveFileEx(from, to, MOVEFILE_REPLACE_EXISTING)` — so the atomic-write
pattern the CLI uses at eight non-test sites (`internal/agent/settings.go`,
`internal/blobx/blobx.go`'s `PutFile`, `internal/cli/init.go`'s
`writeFileAtomic`, `internal/localcfg/checkcache.go`,
`internal/pluginwire/fsx.go`'s `replaceFile`,
`internal/updatecheck/updatecheck.go`, `internal/watch/acks.go`) *does*
replace, on the same volume, and Go applies its long-path fixup first. Two
limits are real and are not visible in the source: (a) `MoveFileEx` fails with
a sharing violation where the POSIX call succeeds if **any** process holds the
destination open — an editor, an indexer, an antivirus scanner, or a second
`fugaro` process reading `watch`'s acks file — so the pattern needs a bounded
retry on Windows and none on POSIX; (b) the call cannot replace a directory,
which no current call site tries. Cost: one helper, retried, and a comment
that says why it exists on one platform and not the other.

**F8 — file modes are written, never checked, and mean something else on
Windows.** Class: **Rule**. Four non-test `os.Chmod` calls
(`internal/cli/managed_apply.go`, `internal/infra/workdir.go` twice,
`internal/watch/acks.go`) and roughly fifty `0o600`/`0o700` literals set a
mode. Nothing in the CLI *reads* a mode to decide anything: the only
`Perm()` reads in non-test code are `internal/image/selftest.go`'s
uid/gid ownership checks (which run inside a Linux image and are out of
scope) and `internal/pluginwire/fsx.go`'s mode-preserving write. On Windows
`syscall.Chmod` maps the mode onto the read-only attribute alone and a file is
reported as `0444` or `0666`, so `0o600` is not merely unenforced — it is
unrepresentable. This is a **Rule** finding because SECURITY.md's claims about
where a credential file lives and who can read it are POSIX claims, and the
plan must not repeat them as Windows claims. Cost: state the limit where the
claim is made; the alternative (an ACL) is a decision, D12.

**F9 — 28 test assertions compare a file's mode exactly.** Class: **Gap**.
The census:

```sh
grep -rn --include="*_test.go" -E 'Perm\(\) *(==|!=|&) *0o?[0-7]{3}' internal/ cmd/ scripts/ | wc -l   # 28, across 18 files
```

Representative sites: `internal/localcfg/sharedcache_test.go` ("file mode %v,
want 0600" and "dir mode %v, want 0700"), `internal/pluginwire/pluginwire_test.go`
("mode = %v, want 0600 kept"), `internal/watch/acks_test.go`,
`internal/cli/gitcred_test.go`, `internal/cli/init_gate_test.go`,
`internal/cli/init_test.go`, `internal/cli/managed_apply_test.go` ("it holds
the operator's own refresh token"), `internal/localcfg/layercache_test.go`,
`internal/localcfg/recipecache_test.go`, `internal/localcfg/checkcache_test.go`,
and `internal/cli/artifacts_test.go`, which asserts the *absence* of the bits
(`Perm()&0o077 != 0` and `Perm()&0o111 != 0`) and therefore fails on Windows
for the opposite reason. Every one of these is a security assertion that is
true on POSIX and meaningless on Windows, and none of them is skippable by a
blanket rule: the plan must decide per class whether the assertion is
"the mode is private" (POSIX-only, skip with that reason) or "the file is not
executable" (portable via the same read-only-bit reasoning, or also POSIX-only).
See D12.

**F10 — the symlink guards: correct in intent, weaker on Windows, and
already documented as such.** Class: **Exists**. The CLI refuses to write
through or read through a link in several places: `internal/pluginwire/fsx.go`'s
`checkPath`, `internal/cli/artifacts.go` (`Lstat`, `EvalSymlinks`), `internal/cli/upgrade.go`'s
`checkoutKey` and `fugaroCheckout`, `internal/cli/run_attach.go`'s `Lstat`
rule, `internal/cli/init.go`'s `EvalSymlinks`, `internal/claudeplugin/plan.go`'s
`EvalSymlinks`, `internal/cache/archive.go`'s `Lstat`/`Readlink` walk,
`internal/config/readfile.go`'s `Lstat` + `SameFile`, `internal/pluginwire/wire.go`'s
`sameFile`. The `_other.go` halves in C2 already say what is lost where
`O_NOFOLLOW` does not exist. What this finding adds is the Windows-specific
third leg: creating a symlink on Windows needs Developer Mode or an elevated
process, so the *guards* keep working on links that exist but the *tests*
that create them do not — F11.

**F11 — `os.Symlink` is a test-only dependency: 19 sites in 11 files under
`internal/cli` alone.** Class: **Gap**.

```sh
cd internal/cli && grep -rn --include="*_test.go" 'os\.Symlink' . | wc -l   # 19 sites, 11 files
```

Plus `internal/config/readfile_test.go`, `internal/blobx/blobx_test.go`,
`internal/cache/keyfile_test.go` and `internal/testutil/fakecmd.go`. Every one
fails on a Windows machine without Developer Mode with
`A required privilege is not held by the client`. Cost: a shared test helper
that skips with *that* reason, rather than 19 identical `t.Skip` calls —
because the guard tests that do not need a link still have to run, and a
helper is where the reason can be written once.

**F12 — `internal/testutil/fakecmd.go` is a `/bin/sh` harness, and four call
sites depend on it.** Class: **Gap**. `FakeCommand` writes
`<name>.sh` holding the test's script, writes one shared `#!/bin/sh` wrapper
in `TestMain` (`PrepareFakeCommands`) and symlinks `<name>` to it, putting
the directory first on `PATH`. On Windows there is no `/bin/sh`, no exec bit,
and `exec.LookPath` will not find a file called `terraform` with no
extension. Call sites: `internal/cli/main_test.go`,
`internal/cli/watch_prstate_test.go`, `internal/watch/main_test.go`,
`internal/watch/prstate_test.go`, and the harness's own test,
`internal/testutil/fakecmd_test.go`. Cost: the harness is small and its
callers are few, so the honest port is a second implementation behind the same
API — a generated `.cmd` shim, or a compiled Go fake the tests build once —
not a rewrite of the callers. See D11.

**F13 — `exec.LookPath`: right call, and one consequence the tests feel.**
Class: **Exists**. Eleven non-test sites
(`internal/basepin/cosign.go`'s `Find`, `internal/claudeplugin/claudeplugin.go`'s
`Find`, `internal/cli/doctor.go`'s `doctorLookPath`, `internal/cli/init.go`,
`internal/cli/managed_apply.go`, `internal/cli/upgrade_plugin.go`,
`internal/image/tools.go`, `internal/image/selftest.go` twice,
`internal/preflight/preflight.go`, `internal/watch/prstate.go`). On Windows
`LookPath` appends each `PATHEXT` extension to a name that has none, which is
exactly what makes `gcloud` resolve to `gcloud.cmd` and what makes a
`.exe`-less shim work — the brief's question about `gcloud.cmd` has a good
answer, and it is already in the code. The consequence is on the other side:
a **test's** fake, written as a bare file with no extension, is *not* found.
That is F12's problem, and it is the reason F12 cannot be solved by renaming
anything.

**F14 — terminal work is `golang.org/x/term` only, and that is
cross-platform.** Class: **Exists**. Five non-test sites:
`internal/cli/init.go`'s `stdinIsTerminal` (`term.IsTerminal`),
`internal/cli/initsecrets.go` (`term.MakeRaw`),
`internal/cli/secrets.go` (`term.ReadPassword`),
`internal/cli/updatewarn.go` (`term.IsTerminal`). `x/term` implements all
three on Windows through the console API. There is **no `/dev/tty`** anywhere
in the module, and the pty dependency (`github.com/creack/pty`, in `go.mod`
marked "tests only: the hidden-prompt test needs a pseudo-terminal") is used
by three test files: `internal/cli/secrets_pty_test.go`,
`internal/cli/initsecrets_pty_test.go`, `internal/cli/docs_console_boundary_test.go`
(and `internal/e2e/init_test.go`). `creack/pty` has no Windows support.
Cost: the pty tests are gated `//go:build darwin || linux` already — the
pattern is in the tree, and the plan extends it rather than inventing
one.

**F15 — signals: one call site, and the two signals Windows does not have.**
Class: **Exists**. `cmd/fugaro/main.go`'s `signalContext` is
`signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM,
syscall.SIGHUP)`. Go defines `SIGTERM` and `SIGHUP` as constants on Windows
(`syscall/types_windows.go`), so this compiles; on Windows only
`os.Interrupt` is ever delivered, which is what Ctrl-C and Ctrl-Break
produce, and that is the case the CLI acts on. Cost: nothing to change at the
call site; what matters is that the **kill** path is `procgroup`'s (F2), not
this one — the context cancels and `procgroup` is what escalates, so a
Windows `procgroup` must implement the escalation or the CLI would leave the
child running.

**F16 — ANSI escapes in four non-test files, with no platform gate.** Class:
**Gap**. `internal/cli/runs.go`, `internal/cli/watch.go`,
`internal/watch/render.go` and `internal/runner/project.go` emit `\033[`
sequences. Windows Terminal and current `conhost` render VT sequences once
virtual-terminal processing is enabled on the console handle; a process that
has not enabled it prints the escapes literally. Go's runtime does not enable
it. Cost: a small decision with a cheap implementation — detect and disable,
or set the console mode once at startup. `internal/watch/render.go`'s
`glyphsOf(o.ASCII)` and the `--ascii`/`NoColor` options show the code already
has a "this terminal cannot do it" mode; the Windows gate belongs beside it.
See D9.

**F17 — terminal size comes from Bubble Tea, never from an ioctl.** Class:
**Exists**. `internal/watch/tui.go` handles `tea.WindowSizeMsg` and
`internal/watch/render.go` falls back to `Width: 80` when nothing has been
reported. The only `TIOCGWINSZ`-family constants in the tree are
`internal/cli/termios_linux_test.go` and `internal/cli/termios_darwin_test.go`,
which are test-only and already gated by their `_linux`/`_darwin` filename
suffix — so they are invisible to a Windows build rather than broken by it.
`github.com/charmbracelet/bubbletea` supports Windows.

**F18 — the watch TUI, plain mode and JSON.** Class: **Exists**.
`internal/watch/tui.go` is the Bubble Tea model; `internal/watch/plain.go` and
`internal/watch/json.go` are the non-TTY and machine-readable modes, and
`internal/cli/watch.go` chooses between them. On Windows the TUI should work;
what a Windows user without a capable terminal needs is the plain mode, which
exists. This is why D10 asks only about *when*, not *whether*.

**F19 — every external program the CLI launches, exhaustively.** Class:
**Exists** (as a census) with F2, F3 and F26 as its consequences. Over the
CLI binary's dependency set:

```sh
PKGS=$(go list -deps -f '{{.Dir}}' ./cmd/fugaro | grep -v '/internal/runner$' | grep '^/work/repo')
grep -rn --include='*.go' 'exec.Command\|exec.CommandContext' $PKGS | grep -v '_test.go'
```

— `git` (`internal/gitops/gitops.go`, `internal/gitops/rejected.go`,
`internal/imagecheck/gittree.go`, `internal/localcfg/localcfg.go`,
`internal/cli/image.go`, `internal/cli/imagecheck.go`,
`internal/cli/init_repo_target.go`'s `gitCmd`, `internal/cli/validate.go`,
`internal/gitprov/fake/fake.go`), `docker` (`internal/image/local.go`, four
sites), `terraform` (`internal/preflight/preflight.go`,
`internal/cli/init.go`, `internal/cli/managed_apply.go`),
`cosign` (`internal/basepin/cosign.go`), `claude`
(`internal/claudeplugin/claudeplugin.go`), `mise` and `fugaro` itself
(`internal/image/selftest.go`, which runs inside a Linux image), and
`sh -c` (F3). **`gcloud` is not in that list** — F28. Cost: the Windows
requirement is a `git` on `PATH` (Git for Windows, whose installer offers to
put it there), a `terraform`, a `gcloud.cmd`, an optional Docker Desktop, and
a Claude Code — F26 and F27.

**F20 — the golden files are compared byte-for-byte and the repository has no
`.gitattributes`.** Class: **Gap**, and the finding most likely to make a
Windows contributor's first test run red for a reason that has nothing to do
with code. Twelve goldens live under `internal/cli/testdata/`
(`init_commands.golden`, `init_review.golden`, `init_retire.golden`,
`cloudrun_cost.golden`, `config_list_keys.golden`,
`config_list_installation_keys.golden`, four `plan_graph_*.golden`,
`watch_json_keys.golden`, `watch_json_ready_keys.golden`), and the comparison
is exact: `internal/cli/init_output_test.go`'s `TestPrintedCommandsGolden`
does `if string(want) != b.String()`, `internal/cli/init_review_test.go`'s
`TestReviewScreenGolden` the same. Git for Windows' installer default is
"Checkout Windows-style, commit Unix-style line endings", i.e.
`core.autocrlf=true`, which rewrites every text file on checkout — the
goldens included — so every golden test fails on a stock Windows clone. There
is no `.gitattributes` anywhere in the repository (`find . -name .gitattributes`
finds none). Cost: one `.gitattributes` (`* text=auto eol=lf`, or the golden
pattern alone), which is a repository-wide file and therefore worth its own
task and its own review.

**F21 — the writers are already CRLF-aware; the readers are not the
problem.** Class: **Exists**. `internal/cli/init_fugaroyaml.go` reads the
target line's terminator and writes the inserted `gcp_project:` line with the
file's own EOL; `internal/config/dockerfile.go` trims a trailing `\r`;
`internal/cli/restore.go` preserves the line's terminator when it rewrites a
line; `internal/cli/secrets.go` accepts and strips a trailing `\r\n` from a
piped value and says so. `fugaro.yaml` itself is read through
`gopkg.in/yaml.v3`, which is CRLF-safe. Cost: nothing here, and this is worth
saying because the brief asked — the CRLF work is F20, in the tests, not in
the CLI.

**F22 — path construction and detection: forward slashes, drive letters and
`filepath`.** Class: **Exists**. The two places the brief named both behave:
`internal/cli/project.go`'s `checkoutProject` runs `git rev-parse
--show-toplevel` through `internal/cli/init_repo_target.go`'s `gitCmd` (git
prints forward slashes on Windows even for a drive path, and `filepath.Join(root,
"fugaro.yaml")` is separator-agnostic), and `internal/repourl/cloneurl.go`'s
`CloneURL`/`ProviderHost` parse provider-and-repository strings, never a
filesystem path. `filepath.ToSlash`/`FromSlash` appear 35 times
(`grep -rn 'filepath.ToSlash\|filepath.FromSlash' --include='*.go' internal/ cmd/ | grep -v _test.go | wc -l`).
Cost: nothing; the pattern to *keep* is that every path printed for a human or
written into YAML is `ToSlash`'d, and that rule is already followed.

**F23 — case-insensitive file systems.** Class: **Open**. Two places could
matter. `internal/config`'s project-name rule is `[a-z0-9-]` (see
`localcfg.ProjectPath`'s error text), so two Fugaro project configs cannot
differ only by case, and the project config file `<name>.yaml` is therefore
unambiguous. Repository slugs come from the provider and are compared as
strings in the allow-list, which is a case-sensitive comparison the providers
themselves define (GitHub's owner and repository names are
case-insensitive; `internal/repourl` and the allow-list do not fold them).
`internal/cli/artifacts.go`'s root/destination checks use `EvalSymlinks` and
`os.SameFile`, which is volume-aware and correct on Windows. Cost: one
decision for the owner — whether the CLI should fold repository owners and
names on every platform, or only document that the providers' own case rules
apply. See O2; it is not on the critical path for a first Windows cut.

**F24 — long paths.** Class: **Gap**. `internal/infra/workdir.go` builds the
deepest paths in the tree: `$XDG_STATE_HOME/fugaro/terraform/<project>/…`
(`PrepareWorkdir`, `installationDir`, `computeDir`, `repoDir`), by design
"outside the tree". On Windows `XDG_STATE_HOME` is unset, so F5's fix decides
the base — and `%LOCALAPPDATA%\fugaro\terraform\<project>\…` is a long prefix
before the Terraform module's own tree is added. Go applies its long-path
fixup (`fixLongPath`) to **absolute** paths over the old limit, so most of
this works — but a long *relative* path is not fixed, and `\\?\` paths skip
the normalisation Windows otherwise does (trailing dots and spaces, `.` and
`..`). Cost: a short base, a `fugaro doctor` line that reports the length of
the deepest path it would create, and a rule that the CLI passes absolute
paths to `os`. See D13's neighbourhood; low risk, cheap to check.

**F25 — reserved names and Unicode.** Class: **Gap**. Windows reserves
`CON`, `PRN`, `AUX`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9` as file names in
every directory, and refuses a name ending in a dot or a space. Fugaro's own
names are safe by construction — a project name is `[a-z0-9-]` starting and
ending with a letter or digit, so `con` *is* reachable as a project name and
would be a reserved file `con.yaml` under `%APPDATA%\fugaro\projects\` — and a
repository slug comes from the provider, which forbids these names too. Cost:
one line in the project-name validator, or the same one line in the plan's
"known limits". Unicode in paths is not a problem: Go uses UTF-16 Win32 calls
throughout, and the CLI's own output escapes non-printables
(`pluginwire.Printable`, `safetext`).

**F26 — the external tools a Windows user needs, and what `fugaro doctor`
sees.** Class: **Gap**.

| Tool | Needed for | Detected today |
|---|---|---|
| `git` | every checkout, every remote, `fugaro exec`'s clone | **not detected at all** — it is assumed, and `checkoutProject` returns "not in a checkout" when it is missing |
| `terraform` | `init`, `managed`, `restore`, the installation apply | `internal/preflight/preflight.go`'s `Terraform`, which runs `terraform version -json` and refuses anything outside `>= 1.9.0, < 2.0.0` |
| `docker` | `image build --local`, `image selftest` | `internal/preflight/preflight.go`'s `Docker`, only when a local build is asked for |
| `gcloud` | `gcloud auth application-default login`, the Fix commands the CLI prints | **not detected as a binary**; only ADC's *file* is read (F28) |
| `cosign` | verifying a base pin's signature | `internal/cli/doctor.go`'s cosign check and `internal/basepin/cosign.go`'s `Find` |
| `claude` | the plugin's skills, `fugaro upgrade --plugin` | `internal/cli/upgrade_plugin.go`'s `claudeLookPath` |

Cost: `fugaro doctor` is the command that exists to answer "is this machine
ready", and on Windows it would answer for three of the six tools and stay
silent about the two the user is most likely to be missing (`git` and
`gcloud`). The plan's Slice 1 gives it the missing checks, and F6 gives the
Fix strings a platform.

**F27 — `fugaro doctor`'s Fix strings are POSIX commands.** Class: **Gap**,
folded into F6. Named here because `doctor` is the command a Windows user
will run first and its advice is the CLI's own instruction:
`internal/cli/doctor.go` prints "unset it and rerun",
`internal/preflight/preflight.go`'s `Environment` prints "unset K",
`internal/preflight/google.go` prints `gcloud …` lines through
`shellword.Quote`.

**F28 — `gcloud` is never executed; it is only read and only named.** Class:
**Exists**, and it is the answer to a question the brief asks directly. Seven
non-test mentions, all of them either an instruction the *user* runs or a
path the CLI reads:

- printed Fix text: `internal/infra/forget.go`, `internal/infra/hardening.go`,
  `internal/infra/projectcreate.go` (two), `internal/preflight/google.go`,
  `internal/cli/doctor_signers.go` (two), `internal/cli/doctor.go`,
  `internal/cli/init_project.go`, `internal/cli/init_retire.go`;
- a credential path: `internal/cache/key.go`'s `credentialPaths` (F30);
- an allowlist entry the *agent* may use inside a job:
  `internal/agent/relay.go`'s `"gcloud"` tool set.

The CLI talks to Google through the Go client libraries with ADC, so a
Windows user's `gcloud` matters for **one** thing the CLI does not do for
them: `gcloud auth application-default login`. Cost: nothing to port, and it
means the Windows documentation's gcloud requirement is one command, not a
tool dependency of the CLI.

**F29 — `gcloud`'s ADC file location is wrong on Windows, in three places.**
Class: **Wrong**. `internal/cli/init_project.go`'s `adcInfo`,
`internal/cli/init_inputs.go` and `internal/cli/managed_apply.go` each build
the path as `$CLOUDSDK_CONFIG/application_default_credentials.json`, else
`$XDG_CONFIG_HOME/gcloud/…`, else `~/.config/gcloud/…`. gcloud on Windows
keeps its configuration in `%APPDATA%\gcloud` and reads neither `XDG_CONFIG_HOME`
nor `~/.config`. Cost: on Windows the CLI cannot see that the user is signed
in, so `doctor`'s ADC checks and `init`'s quota-project refusal report the
wrong thing — and the refusal the user gets ("no ADC") is not the problem they
have. Note the irony: `adcInfo` is deliberately careful (it reads a bounded
regular file, never a FIFO — `internal/cli/init_project_unix_test.go` proves
it) and passes the right *file*; only the *directory* is Unix.

**F30 — the credential-path denylist is written in POSIX home-relative
names.** Class: **Rule**. `internal/cache/key.go`'s `credentialPaths` refuses
a cache that is, is inside, or contains `.ssh`, `.config/gcloud`,
`.config/gh`, `.git-credentials`, `.npmrc`, `.netrc`, `.docker`, `.aws`,
`.kube`, `.gnupg`, and any first component starting with `.claude`. The
comparison is already case-insensitive (`strings.EqualFold`), which is the
right instinct for Windows — but the *names* are Unix layouts. On Windows the
same secrets live at `%APPDATA%\gcloud`, `%APPDATA%\npm\npmrc`,
`%USERPROFILE%\.ssh` and `%USERPROFILE%\.aws`. And `holdsCredentials` splits
the path on `/` (`strings.Split(clean, "/")`), which is correct only because
the caller has already `ToSlash`'d. This is a **Rule** finding because
`internal/cache/key.go` is the guard that keeps the agent's sessions and
credentials out of a cache that is uploaded to the runs bucket, and its
comment says so. Cost: name the Windows locations too, or state that the
guard is POSIX-only and that a Windows cache is not protected — which would be
a bad answer, since the cache is uploaded either way.

**F31 — `.goreleaser.yaml`: what adding Windows means.** Class: **Gap**.
Today: `builds[0]` has `goos: [darwin, linux]` and `goarch: [amd64, arm64]`,
`CGO_ENABLED=0`, `-trimpath`; `archives[0]` has `formats: [tar.gz]` and a
`name_template` of `fugaro_{{ .Version }}_{{ .Os }}_{{ .Arch }}`, shipping
`LICENSE` and `README.md`; `checksum.name_template: checksums.txt`; one SBOM
per archive through syft (`sboms[0].artifacts: archive`); a keyless cosign
signature of `checksums.txt` with a `.sigstore.json` bundle (`signs[0]`); and
`homebrew_casks[0]`, which uploads to `dimipaun/homebrew-tap` and skips
cleanly when `HOMEBREW_TAP_GITHUB_TOKEN` is empty. Adding Windows means: the
`goos` list gains `windows`; the archive needs `format_overrides` so that
`windows` produces a **zip** (GoReleaser's default for windows in v2 is zip,
but the explicit `formats: [tar.gz]` here overrides the default, so it must be
said); the SBOM, checksum and signature steps are platform-agnostic and need
nothing; and the archive `name_template` produces
`fugaro_<v>_windows_amd64.zip`, which the release notes and
`website/data/install.yaml`'s `name_template` and `extension` fields pin (C3).
Cost: small, mechanical, and it is the *first* task of the release slice
because everything else in that slice depends on the artifact existing.

**F32 — the version-embedding ldflags are platform-independent.** Class:
**Exists**. `ldflags: -s -w -X github.com/dimipaun/fugaro/internal/cli.Version={{ .Version }}
-X github.com/dimipaun/fugaro/internal/cli.Commit={{ .FullCommit }}` and
`mod_timestamp: "{{ .CommitTimestamp }}"`. Nothing to change. `go install
github.com/dimipaun/fugaro/cmd/fugaro@latest` also works on Windows
unchanged, which is why the site's `go-install` path (marked
`dev_only: true`) needs no Windows-specific text beyond its platform list.

**F33 — Authenticode: nothing signs a Windows binary, and there is no
Windows release step at all.** Class: **Gap**, owner-only. `.goreleaser.yaml`
has no `signs` entry for Authenticode (its one `signs` entry is the cosign
bundle over `checksums.txt`), `.github/workflows/release.yml` has three jobs
(`check`, `dry-run`, `release`), all `ubuntu-latest`, and the only
platform-specific signing in the repository at all is the **console's** Apple
pipeline (`docs/pending-workflows/console-release.yml`). What an unsigned
Windows binary gets: Microsoft Defender SmartScreen's "Windows protected your
PC" interstitial for a downloaded file with low reputation, with "Don't run"
as the default button, and the "Unknown publisher" line in the UAC prompt if
the binary ever asks for elevation. What signing would need, owner-only: an
**OV** code-signing certificate (a real-world identity check, an annual fee,
and a hardware token or cloud HSM under the CA/Browser Forum's rules since
June 2023), or an **EV** certificate (a stricter identity check, a higher fee,
and the reputation SmartScreen needs immediately rather than accumulating);
then the certificate or its cloud key must reach the release job as a secret,
and the release job must be a separate, protected file for the same reason the
console's is (`docs/pending-workflows/console-release.yml`'s own comment says
exactly this: a job holding signing material must not sit in a file that runs
on every pull request). Cheaper alternatives that are not code signing:
publishing the archive's SHA-256 (already done — `checksums.txt`) and telling
the reader to unblock the file, and Scoop's `checkver`/`hash` flow, which
records the hash in the manifest the user installs from. See D4.

**F34 — the plugin is skills only, and that is the whole Windows answer for
it.** Class: **Exists**. `plugin/` holds `.claude-plugin/plugin.json` and five
skills (`parallelism`, `routing`, `setup`, `upgrade`, `working`) with their
`reference/` documents. There is **no `hooks/` directory, no `.sh` file and no
hook of any kind**;
`grep -rn 'hooks' plugin/ --include='*.json' --include='*.md'` finds one hit,
and it is `plugin/skills/setup/SKILL.md` describing `mise.toml`'s own
`[hooks]` table. What the CLI wires is `pluginwire`, which writes the
*project's* `.claude/settings.json` (the marketplace entry and the plugin pin)
and refuses the user's own `~/.claude` directory
(`pluginwire.checkPath` + `pluginwire.userConfigDir`, which already reads
`CLAUDE_CONFIG_DIR` and `os.UserHomeDir()`). The skills' command blocks are
`bash`-fenced; Claude Code on Windows runs them through its own shell. Cost:
nothing structural. The plan's documentation task should say which shell the
skills assume rather than forking the skills per platform, and D14 asks
whether that is enough.

**F35 — the build and release documentation and the site are a three-file
pin.** Class: **Rule**, folded from C3. `docs/release.md` states in prose that
GoReleaser "builds `fugaro` for darwin and linux on amd64 and arm64, writes
`checksums.txt`, an SBOM per archive (syft), a keyless cosign signature of
`checksums.txt`, and the GitHub Release; it also pushes the Homebrew cask";
`.goreleaser.yaml`'s `release.header` prints the Homebrew and `go install`
lines; `website/data/install.yaml` and `README.md` hold the reader-facing
platform claim; and `internal/cli/docs_website_install_test.go` holds the two
of them to `.goreleaser.yaml`. Cost: the Windows change lands in five files
and one guard test; a change that touches the matrix and not the prose is
caught, which is the point.

**F36 — the test suite's Windows census.** Class: **Gap**. The counting
commands and their answers, all run at `54cb02a8`:

| What | Command | Answer |
|---|---|---|
| packages that fail `go build` for windows | `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` | 2 |
| packages that fail `go vet` (test files included) | `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...` | 5 |
| test files in the module | `find . -name '*_test.go' \| wc -l` | 680 |
| test files in `internal/cli` | `ls internal/cli/*_test.go \| wc -l` | 224 |
| `internal/cli` test files that fail `go vet` for windows | — | **0** |
| exact-mode assertions | `grep -rn --include='*_test.go' -E 'Perm\(\) *(==\|!=\|&) *0o?[0-7]{3}' internal/ cmd/ scripts/ \| wc -l` | 28 (18 files) |
| `os.Symlink` in `internal/cli` tests | `grep -rn --include='*_test.go' 'os\.Symlink' internal/cli \| wc -l` | 19 (11 files) |
| `syscall.` in `internal/cli` tests | `grep -rn --include='*_test.go' 'syscall\.' internal/cli \| wc -l` | 24 (9 files) |
| `#!/bin/sh` bodies in `internal/cli` tests | `grep -rn --include='*_test.go' '/bin/sh' internal/cli \| wc -l` | 5 (4 files) |
| test files behind a `go:build` tag, module-wide | `grep -rl 'go:build' --include='*_test.go' internal/ cmd/ \| wc -l` | 24 |

**The important number is the sixth row's neighbour: zero.** `internal/cli`'s
224 test files — the CLI's whole suite — type-check for windows/amd64 at
`54cb02a8`. What fails is behaviour, and the failures are the four classes in
F9, F11, F12 and F20. Cost: this is why the plan can promise that main stays
green on Linux and macOS while the port proceeds, and why it can be honest
that the Windows job will be red for a while.

**F37 — the copy-count caveat.** Every count above is volatile state: a
committed test changes it. The commands are recorded with the figures so that
a later reader can re-run them, not so that the figures can be quoted as
facts. This is the same rule `docs/setup-wizard/reconciliation.md` states for
its own `fugaro init` count.

**F38 — a Windows CI job cannot be pushed by a run like this one.** Class:
**Rule**. `.github/workflows/` is off limits to Fugaro runs (GitHub refuses
the push), so a Windows job is staged in `docs/pending-workflows/` as the
file's full intended content and copied in by the owner —
`docs/pending-workflows/README.md` is the convention and its own index is
pinned by guard tests. Every job in `.github/workflows/ci.yml` is
`ubuntu-latest` (`changes`, `test`, `rules`, `terraform`); the only `matrix.os`
in the repository is `console-tests.yml`'s `jobs.<name>.strategy.matrix.os`,
and it does not
include Windows. A `windows-latest` job would run on GitHub-hosted Windows
Server, billed at twice the Linux rate, with the same 6-hour ceiling. The
`test` job's own comment in `ci.yml` records the owner's ruling of 2026-10-16
that **pull requests are not merge-gated by CI**, so a Windows job is
advisory on pull requests and runs on `main` by the same mechanism as the
rest, unless D5 says otherwise.

**F39 — WSL.** Class: **Exists**. Nothing in the CLI detects WSL and nothing
needs to. A `fugaro` built for linux/amd64 inside WSL *is* the Linux path, and
two of the three things that could bite are outside the CLI: where the
checkout lives (a checkout under `/mnt/c` is on the Windows filesystem, where
file modes are not real, where `os.Chmod` does nothing, and where every git
operation is far slower than on the ext4 side), and where the credentials are
(gcloud's ADC inside WSL is `~/.config/gcloud` *inside WSL*, a different file
from the Windows user's `%APPDATA%\gcloud`, so signing in on Windows does not
sign the user in for WSL). Claude Code must also be installed inside WSL for
the plugin's skills to have a `fugaro` to call. The desktop console cannot
reach a WSL-only installation for the same reason it cannot reach a
Linux-only one — see F40. Cost: WSL costs documentation and no code. That is
the whole argument for D1.

**F40 — how the console follows.** Class: **Open**, out of scope.
`docs/design/desktop-console.md`'s D10 records the ruling: Windows and Linux
packaging is "**Later**", "Keep the code cross-platform and run the Linux CI
pass so it does not rot, but sign and distribute macOS first; two more signing
identities is owner work with no v1 user." The console's data source is the
CLI, so a Windows console needs a Windows CLI *and* an Authenticode-grade
decision; the design's §"Two runners" says "Windows joins when D10 says so".
This plan therefore states the dependency and changes nothing about the
console; the console's own follow-up is a separate document.

**F41 — `fugaro exec` is hidden and is the in-job entry point.** Class:
**Exists**. `internal/cli/exec.go`'s `newExecCmd` is `Hidden: true` with
`use: "exec"` and the short "Execute a run inside a Fugaro worker (the
container entrypoint)". It builds for Windows only because `internal/runner`
does — and the runner is the one thing the brief puts out of scope. A Windows
build would ship this command, and it should stay hidden and stay unused: the
plan says so and adds no work for it. `internal/runner` is the only package
in the CLI binary's dependency set that is explicitly Linux-and-only-Linux,
and F1/F2 are the two packages that fail to compile *around* it.

**F42 — `fugaro image build --local` needs Docker and runs on Windows through
Docker Desktop.** Class: **Open**. `internal/image/local.go` execs `docker
version --format {{.Server.Version}}` and then builds and runs the image; the
Dockerfile it builds is a Linux image, which Docker Desktop runs in its Linux
VM. The pieces that need checking rather than assuming: the build context's
path handling, the stdin it feeds the container, and whether the
`image selftest` half (which runs `mise`, `getcap` and `fugaro verify build`
*inside* the container) is affected. It is not on the critical path.

---

## 4. The census, and what it is scoped to

"The CLI's packages" is defined here as the packages the CLI binary actually
contains, minus the runner:

```sh
go list -deps ./cmd/fugaro | grep -c 'dimipaun/fugaro'          # 60
go list -deps -f '{{.Dir}}' ./cmd/fugaro | grep -v '/internal/runner$' | grep '^/work/repo' | wc -l   # 59
```

The whole set, for the record: `cmd/fugaro`, `deploy/terraform`, `images`,
`internal/{agent,backend,backend/batch,backend/gcp,basepin,blobx,budget,budget/rules,budget/token,cache,claudeplugin,cli,config,firestore,followup,gateway,gitops,gitprov,gitprov/bitbucket,gitprov/fake,gitprov/github,gitprov/httpjson,gitprov/providers,image,imagecheck,infra,infra/managed,infra/tf,initflow,localcfg,lock,logtail,managed,mirror,plan,pluginwire,policy,preflight,pricing,pricing/catalog,procgroup,projectpolicy,recipe,report,repourl,rtdb,runstore,runview,safetext,shellword,stats,task,updatecheck,userstats,verify,watch}`.
`internal/runner` is in the binary because of the hidden `fugaro exec` (F41)
and is excluded from every count below.

| Construct | Non-test sites | Including tests | Command |
|---|---|---|---|
| `syscall.` | 30 | 75 | `grep -rn --include='*.go' 'syscall\.' $PKGS` |
| `O_NOFOLLOW` | 18 | 23 | as above |
| `os.Chmod` | 4 | 41 | as above |
| `EvalSymlinks` | 10 | 18 | as above |
| `os.Symlink` | 0 | 55 | as above |
| `os.Rename` | 8 | 14 | as above |
| `signal.Notify` | 1 | 2 | as above |
| `SysProcAttr` | 2 | 4 | as above |
| `exec.LookPath` | 11 | 40 | as above |
| `getenv("HOME")` | 4 | 4 | as above |
| `os.TempDir` | 2 | 8 | as above |
| `os.UserHomeDir` | 7 | 7 | as above |
| `golang.org/x/term` | 5 | 6 | as above |
| `golang.org/x/sys/unix` | **0** | 5 (test-only, `_linux`/`_darwin`-suffixed) | as above |
| `os.DevNull` | 2 | 7 | as above |
| `"/dev/` | **0** | 10 | as above |

Two of those rows are the answer to a fear the brief's question list encodes:
**`golang.org/x/sys/unix` appears in the CLI's packages only in tests, and
`"/dev/"` appears in the CLI's packages only in tests.** Non-test CLI code
reaches for neither.

---

## 5. Decisions for the owner

None is taken. Each has a recommendation first with its reason, then the
alternative and what it costs. Answering one is not approving a task.

### D1 — native Windows, or WSL-only, or both?

**Recommend: both, in this order — document WSL as the route that works
*today* (Slice 0, a documentation task, mergeable now), and build the native
port behind it (Slices 1–6).** The reason: WSL is free (F39 — no code, only a
document, and a Linux binary inside WSL is exactly the Linux path the
repository already tests), and it unblocks the reader who is on Windows today
while the native port is built. It is *not* a substitute for the port, because
the owner asked for it in these terms ("make the CLI work on Windows"), and
because WSL fails two things the native path gets right: the console cannot
reach a WSL-only installation (F40), and the credentials boundary between the
Windows user and the WSL user is a real source of confusion (F39).

**Alternative: WSL-only, permanently.** Cost: two commands of documentation
and nothing else — genuinely cheap — but the CLI's promise to a Windows user
becomes "install a Linux distribution first", the desktop console stays
macOS-and-Linux-only forever through F40, and the seven packages' worth of
Windows awareness the tree already carries (C2) stays dead code. If the owner
takes this, Slices 1–5 are cancelled and only Slice 0 and the console decision
remain.

**Alternative: native only, no WSL text.** Cost: the user who already runs WSL
and would be happier there reads nothing that says so, and the F39
file-system and credential traps are learned the hard way. The document is
cheap; skipping it buys nothing.

### D2 — amd64 only, or amd64 and arm64?

**Recommend: amd64 only for the first release, and say so in the platform
list; add arm64 when a release needs it.** The reason: Windows-on-ARM is a
real platform but a small share of developer machines, `GOARCH=arm64` costs a
second archive, a second SBOM, a second line in `checksums.txt` and a second
Scoop/winget architecture entry, and — the part that is not mechanical — it
costs a second *verification* platform that nobody in this repository can run
(F33's Windows checks are already the scarcest resource). amd64 binaries run
under Windows' own x64 emulation on ARM anyway, which is an acceptable story
for a first cut.

**Alternative: both, from the first release.** Cost: one more archive and one
more verification target per release, forever, for a platform with no known
user; the emulation path covers the first users.

### D3 — winget, Scoop, both, or neither, for the first release?

**Recommend: Scoop, and neither winget nor a package manager at all until
Scoop is proven.** The reason: Scoop is a **manifest in a Git repository**
(`ScoopInstaller/Main` for anything widely used, or a bucket of the owner's
own, exactly as `dimipaun/homebrew-tap` is), so it fits the release workflow
the way the Homebrew cask already does — a file uploaded to a repository the
owner controls, from the same job, with the hash the release already
computes. winget needs a **pull request against `microsoft/winget-pkgs`**
reviewed by Microsoft's moderators for every version, which is a queue, a
process and an identity the owner does not have yet.

**Alternative: winget first or instead.** Cost: a Microsoft account, a signed
(or at least published) package, and a review round-trip on every release; and
winget's own `InstallerType: zip` + `NestedInstallerType: portable` flow is
the only shape an unsigned, non-MSI archive can take today.

**Alternative: both.** Cost: two manifests to keep in step on every release,
and a second updater to test; the plan's Slice 5 is sized for one.

**Alternative: neither, just the archive.** Cost: nothing mechanical at all —
the archive and `checksums.txt` are already there (F31) — and a Windows reader
unblocks a file and edits their `PATH` by hand, which is the honest state of
"untested" today (C3). This is a perfectly defensible first cut and is what
D4's answer interacts with: an unsigned zip from GitHub is exactly what
SmartScreen warns about.

### D4 — unsigned first (SmartScreen warning), or buy an Authenticode certificate first?

**Recommend: unsigned first, with the warning documented, and revisit signing
when a Windows user other than the owner exists.** The reason: the certificate
is an owner-only expense and process (F33: OV or EV, a hardware token or cloud
HSM, an annual fee, and a protected release file for the key), and SmartScreen
is a *reputation* judgement that a new OV certificate does not clear
immediately — so buying the certificate does not make the warning go away on
day one, it only changes who the warning is about. The archive's SHA-256 is
already published and cosign-signed, which is a stronger integrity answer than
SmartScreen's.

**Alternative: buy an EV certificate first.** Cost: the highest fee and the
strictest identity check, in exchange for SmartScreen reputation from the
first download; and the release workflow then needs a protected file holding
the key, which is a security review of its own (the console's Apple pipeline
is the precedent, `docs/pending-workflows/console-release.yml`).

**Alternative: an OV certificate.** Cost: cheaper than EV, same hardware
requirement, and it does not clear SmartScreen's warning for a new identity
until reputation accumulates — which is the worst of both.

### D5 — does the Windows CI job gate merges, or is it advisory at first?

**Recommend: advisory at first, and running on `main` like every other job.**
The reason: `ci.yml`'s own comment records the owner's ruling of 2026-10-16
that pull requests are not merge-gated by CI at all (F38), so a Windows job
is advisory by the existing rule and needs no new decision to be so; and while
the port is in progress the Windows job *will* be red, so making it a gate
would block every merge including the ones that fix it. The plan's last slice
flips it to a gate once it is green, which is a change to the staged workflow
file and therefore owner-only anyway.

**Alternative: gate from the first commit.** Cost: `main` is red and every
merge is blocked until the port is complete — which is the same as freezing
the branch for the duration.

### D6 — the first cut: read-only first, or the whole CLI?

**Recommend: read-only first, then setup, then launch — the order in the
plan's Slices 1–4.** The reason: it is the order in which each slice is
*independently verifiable* without a cloud login and without a second
machine. `fugaro --version`, `validate`, `config ls`/`show`, `runs ls`,
`artifacts ls`, `budget show`, `doctor` and the offline `image render` all run
against a local config and a real bucket, need no shell and no Terraform, and
are what a Windows user tries first. `run` needs the whole auth path. The
per-command `sh` dependency (F3) and the TUI (F18) come later because they are
the two things that need a decision (D7, D10) rather than a port.

**Alternative: the whole CLI in one slice.** Cost: one enormous change that
cannot be merged until it is all correct, on a platform the repository cannot
test in CI yet — which is the opposite of "each part merges alone and leaves
main green" that the brief asks for.

### D7 — `fugaro verify` on Windows: require Git Bash, choose the shell by platform, or out of scope?

**Recommend: choose the shell by platform — `sh -c` on unix, `cmd /c` (or a
`COMSPEC`-honouring equivalent) on Windows — and keep `commands:` as free
text.** The reason: `fugaro verify` exists to run *this repository's* own
commands, and the commands a Windows repository configures are Windows
commands; giving the YAML a platform switch would put the choice in every
user's config and make a config that works on one platform fail on another.
`internal/config/validate.go` treats `commands:` as opaque strings, so
nothing in the schema changes.

**Alternative: require Git for Windows and keep `sh -c`.** Cost: zero code,
and the CLI then requires a `sh` on `PATH` on Windows — which is a hidden
dependency on one of two possible Git installations, and the machine that has
`git.exe` alone (GitHub Desktop, a CI image) would fail with `exec: "sh": executable file not found`.

**Alternative: out of scope; `fugaro verify` is not supported on Windows.**
Cost: the command is the repository's own gate (`fugaro verify build` /
`verify test`) and this run was told to build and test only through it, so
declaring it unsupported would make a Windows checkout a second-class
contributor for the one thing contributors do most.

### D8 — where does the CLI's config live on Windows?

**Recommend: keep `$XDG_CONFIG_HOME` when it is set (the escape hatch stays),
and fall back to `%APPDATA%\fugaro` for the config and `%LOCALAPPDATA%\fugaro`
for the caches, via `os.UserHomeDir()`-style platform defaults rather than a
bare `HOME` read.** The reason: it is what a Windows user's other tools do
(`%APPDATA%` is Roaming, which is where a small config belongs;
`%LOCALAPPDATA%` is not, which is where a cache belongs), it keeps the
existing XDG path working for anyone who set it, and `os.UserHomeDir()` is
already used at seven sites in the CLI so the fallback is the same call. It
also fixes F5 in the one place that decides all four paths.

**Alternative: require the user to set `HOME`.** Cost: zero code, and a
Windows user's `fugaro config ls` refuses until they set a variable no other
Windows tool reads — the worst first impression available, and one the error
message would have to teach.

**Alternative: `~/.config/fugaro` under `%USERPROFILE%`, i.e. `%USERPROFILE%\.config\fugaro`.**
Cost: keeps the paths byte-identical to POSIX and keeps `$HOME`-relative
reasoning working, at the price of writing a dotted directory into a Windows
profile that no Windows tool puts there.

### D9 — colour and VT sequences on Windows.

**Recommend: enable virtual-terminal processing once at startup and keep the
existing colour code, with the existing `--no-color`/`NoColor` path as the
escape hatch.** The reason: `internal/watch/render.go` already has a
non-colour mode and `glyphsOf(o.ASCII)` already has a non-Unicode mode, so the
fallback exists; what is missing is that nobody turns VT processing on, so the
escapes print literally under `conhost`. One call at startup fixes every
`runs ls`, `watch` and progress line at once.

**Alternative: detect and strip.** Cost: colour is lost on every Windows
terminal including the ones that can do it, and the two modes of
`internal/watch/render.go` become three.

### D10 — is the interactive wizard, or the `watch` TUI, Windows-later?

**Recommend: both are in scope, in the last slices, after the non-interactive
paths — and neither is a blocker for the release.** The reason: the wizard is
`internal/initflow`, which reads a terminal through `x/term` (F14, portable)
and has a non-interactive mode (`--yes`, `--non-interactive`, `--json`) that
already works; the TUI is Bubble Tea (F17, portable). So both are *expected*
to work and neither is *verified*, and F36's rule — do not claim what was not
run — says to keep them in the plan as tasks with their own verification
rather than as assumptions.

**Alternative: declare the TUI Windows-later and ship the plain mode only.**
Cost: `fugaro watch`'s whole point is the screen; a Windows user gets the
plain mode and a note, which is a worse answer than the one Bubble Tea
probably already gives.

### D11 — the fake-command test harness: port it, or skip the tests that use it?

**Recommend: port it, behind the same API, as a generated `.cmd` shim or a
compiled fake.** The reason: only four call sites depend on it
(`internal/cli/main_test.go`, `internal/cli/watch_prstate_test.go`,
`internal/watch/main_test.go`, `internal/watch/prstate_test.go`), and the two
packages it serves are exactly the two whose Windows behaviour matters most
(`cli` and `watch`). Skipping them would leave `watch`'s Windows behaviour
with no test at all.

**Alternative: build tags and skips for the four files.** Cost: two packages
lose their whole fake-command surface on Windows, and the skip reason ("this
harness needs `/bin/sh`") is a reason that stops being true the moment someone
ports the harness — so it would be re-investigated later, at higher cost.

### D12 — the POSIX mode assertions and the security claim.

**Recommend: keep every mode where it is set (it is free and it is right on
POSIX), gate the 28 assertions behind a shared helper that skips on Windows
with the one stated reason ("Windows reports 0444 or 0666 for every file"), and
state in SECURITY.md-adjacent prose that the mode guarantee is POSIX-only —
because on Windows the *directory* ACL is what protects the file and Fugaro
does not set it.** The reason: the modes protect something real on POSIX and
protect nothing on Windows, so the honest move is to stop *claiming* the
property there rather than to fake it. An ACL-based alternative exists
(`SetNamedSecurityInfo` through `golang.org/x/sys/windows`, i.e. inherit-only
ACLs) and is a bigger change than the whole port.

**Alternative: an explicit ACL on the config and cache directories.** Cost:
a Windows-only code path that has to be understood and tested, for a threat
model (another local user on the same machine) that is genuinely weaker on
Windows than on a shared POSIX host.

**Alternative: refuse to run on Windows when a credential file would be
written.** Cost: the CLI's first-run wizard writes exactly that file, so this
is a refusal to support the platform at all.

### D13 — long paths and reserved names.

**Recommend: add one `fugaro doctor` line that reports the length of the
deepest path the installation would create and whether a reserved name is in
play, and change nothing else.** The reason: F24's analysis says Go's long
path fixup already covers absolute paths, and F25's reserved names are only
reachable through a project name (`con`), which the validator can refuse for
one line. A doctor line is verifiable without a Windows machine from the
source, and it tells a user which of the two problems they have.

**Alternative: do nothing.** Cost: a user whose Terraform state path exceeds
the limit gets an unexplained failure from `init`, which is the command least
able to afford one.

### D14 — the plugin skills on Windows: document the shell assumption, or fork the skills?

**Recommend: document it — one paragraph, in the plugin's own reference, that
the command blocks assume a POSIX shell and that Git Bash (or WSL) is the
shell Claude Code should use on Windows.** The reason: F34 — the plugin has no
hooks and no scripts, only `bash`-fenced blocks the agent runs; forking five
skills per platform would double the surface the plugin's own tests pin
(`plugin/skills_lint_test.go`, `plugin/setup_skill_test.go`,
`plugin/upgrade_skill_test.go`) for text that is already correct on the shell
that runs it.

**Alternative: fork the skills.** Cost: every skill edit lands twice, and the
lint test's checks double.

**Alternative: nothing.** Cost: a Windows user's agent tries a `bash` block
under PowerShell and gets a syntax error, with nothing in the repository
explaining why.

### D15 — the `website/data/install.yaml` and `README.md` platform claim while Windows is "best effort".

**Recommend: leave Windows `untested` until the Windows CI job is green and a
release publishes a Windows archive; then move it to `supported` in the same
commit that adds `windows` to `.goreleaser.yaml`'s `goos`, and add a WSL row
as its own entry rather than as Windows prose.** The reason: C3's guard test
requires an unbuilt platform to be marked untested and forbids a support
claim; the moment the archive exists the test requires the *opposite*, so the
two moves are one commit. And a WSL install path is a *path* in the file's own
vocabulary (`paths:` with `platforms:`), not a note.

**Alternative: mark Windows "best effort" — a third status.** Cost: a new
status the guard test does not know (`supported` or `untested` today), so it
is a change to the guard test and to the site's renderer; and it is the status
that is hardest to keep honest, because nobody can say what "best effort" does
not cover.

### D16 — which slice does the owner want first, given that Slice 0 is free?

**Recommend: Slice 0 and Slice 1 only, reviewed together, before anything
touches `.goreleaser.yaml`.** The reason: Slice 0 is documentation and the
WSL route (D1) and changes no code; Slice 1 is the two-package build fix (F1,
F2) plus the config-directory fix (F5) and the ADC-path fix (F29), which
together are the whole difference between "does not compile" and "starts and
prints its version". Both are verifiable from a Linux machine (the
cross-compile command in C1 is the verification for Slice 1's first half), so
an owner without a Windows machine can still review and merge them.

**Alternative: Slice 0 alone, and stop.** Cost: legitimate — it is the
cheapest possible answer and it makes the WSL route official — but it leaves
the tree's own Windows awareness (C2) unexercised and the console's
dependency (F40) unresolved.

---

## 6. Items found while planning (not decisions)

**O1 — the seven `unix`/`other` pairs are five today, and two of the pairs
this work needs are new.** C2 lists five. F1 and F2 add two more
(`internal/pluginwire`, `internal/procgroup`). The convention they must follow
is *the existing one* — a comment that states what the non-Unix half loses —
and the plan's Slice 1 is where the convention is applied rather than
invented.

**O2 — the allow-list's case folding (F23) is a separate, small question.**
GitHub owner and repository names are case-insensitive; `internal/repourl` and
the policy's allow-list compare them as strings. This is a Windows *adjacent*
finding rather than a Windows one — it bites on any two machines, not only on
a case-insensitive filesystem — and it is listed here so that it is not lost
and not smuggled into this plan.

**O3 — the `docs/gcp-setup.md` guide has no Windows text at all.**
`grep -n -i windows docs/gcp-setup.md` finds nothing. It is the guide a
first-time user follows, and two of its steps are shell commands the user
runs (`gcloud auth application-default login`, the Fix commands). Whatever D1
and D8 decide, that guide needs the Windows or WSL text, and the plan's Slice
0 or Slice 6 is where it goes.

**O4 — `internal/cli/docs_desktop_console_test.go` pins the console's CI
workflow byte-for-byte against `docs/pending-workflows/console-tests.yml`.**
F38's staging mechanism has a live example with a guard test; the Windows job
must follow it or the guard will not be there to catch a half-copy.

**O5 — the release's `homebrew_casks[0]` has a `post.install` hook that runs
`/usr/bin/xattr -dr com.apple.quarantine`.** It is guarded by `if OS.mac?`, so
it is already safe, and it is named here only because a reader of F33 may
wonder whether the quarantine step has a Windows counterpart. It does not:
SmartScreen's mark is stored in an alternate data stream on the downloaded
file and is cleared by the *user* unblocking the file, not by the installer.

---

## 7. UNVERIFIED

Everything below is a claim about **behaviour on Windows**, which this run
could not run: the container is Linux, and the brief forbids live cloud action
of any kind. Each is a check for §6 of the plan.

| # | What could not be verified | How to verify it |
|---|---|---|
| U1 | That the CLI, once F1 and F2 are fixed, starts and prints `fugaro version` on Windows | run the built `fugaro.exe --version` on a Windows machine or a `windows-latest` runner |
| U2 | That `exec.LookPath` resolves `gcloud.cmd`, `terraform.exe` and a Claude Code installation on a real Windows `PATH` | `where gcloud` and `fugaro doctor` on Windows |
| U3 | That `golang.org/x/term`'s `MakeRaw` and `ReadPassword` give `fugaro secrets set` a working hidden prompt on Windows | run `fugaro secrets set NAME` in Windows Terminal, cmd.exe and PowerShell |
| U4 | That `os.Rename` over an existing target, with the destination held open, fails as F7 says, and that a bounded retry fixes it | a small Windows test that opens a file and renames over it |
| U5 | That Bubble Tea's TUI renders, resizes and restores the screen in Windows Terminal and in `conhost` | `fugaro watch` on Windows, both terminals, resized |
| U6 | That the ANSI escapes print literally under a console with VT processing off (F16) | `fugaro runs ls` in a fresh `cmd.exe` |
| U7 | That a stock Git for Windows clone really does rewrite the 12 goldens to CRLF (F20) | clone on Windows with the installer's default and run `go test ./internal/cli -run Golden` |
| U8 | That an unsigned `fugaro.exe` from a GitHub release shows SmartScreen's interstitial and how it reads (F33) | download the archive on a stock Windows 11 machine |
| U9 | That the 224 `internal/cli` test files' *behaviour* fails only in the four classes F36 enumerates — i.e. that after the mode, symlink, harness and CRLF work there is no fifth class | run the suite on a `windows-latest` runner and read the whole failure list once |
| U10 | That `fugaro image build --local` works against Docker Desktop (F42) | run it on Windows with Docker Desktop's WSL 2 backend |
| U11 | That `%APPDATA%\gcloud` is where a Windows gcloud puts ADC, for the version the repository's docs pin | `gcloud info --format='value(config.paths.global_config_dir)'` on Windows |
| U12 | That a Terraform working directory under `%LOCALAPPDATA%` stays inside the long-path limit for a realistically-named project (F24) | create it on Windows and measure |
| U13 | Anything about the desktop console on Windows | out of scope; `docs/design/desktop-console.md` D10 owns it |

**Nothing in this document was verified by running Fugaro on Windows.** The
source claims (everything in §3 that names a path and a symbol) were verified
by reading the tree at `54cb02a8` and by the three compiler invocations
recorded in C1 and F36; the Go-runtime claims (F7, F8) were verified by
reading Go 1.27.1's own source, `syscall/syscall_windows.go`'s `Chmod` and
`Rename`, `os/file_windows.go`'s `rename`, and `os/types_windows.go`'s mode
mapping; the `.goreleaser.yaml`, `ci.yml` and `install.yaml` claims were
verified by reading those files.
