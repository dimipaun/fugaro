# The Fugaro CLI on Windows — the documents

**Status: Slice 0 and Slice 1 approved (D16, 2026-10-20); Slice 0 is written; no code is implemented; D1–D6 are answered and D7–D15 are open.** The owner's
brief of 2026-10-20 asked for a design and a plan, not code: *"Let's work out a
plan on how to make the CLI work on Windows."* The owner gave the context the
same day — the desktop console ships for macOS (and Linux, unsigned, for
0.10.0) because its data source is the CLI and the CLI has no Windows build,
and Windows packaging for the console was deferred for that reason and for code
signing (`docs/design/desktop-console.md`, **D10**).

**The owner answered D1–D6 and D16 on 2026-10-20 and approved Slice 0 and Slice 1 only** (**D16**): D1 **both** — WSL documented now, the native port
behind it; D2 **amd64 only**; D3 **Scoop first**; D4 **unsigned first**; D5 a
Windows CI job that is **advisory** at first; D6 **read-only first**. **D7–D15
are still open**, each with its recommendation and its reason in
[reconciliation.md](reconciliation.md) §5, whose decision texts still read as
written: the ruling is recorded here and not there. Answering a decision is not
approving a task, and no code beyond Slice 1's — which is in the tree, below —
no config, workflow, Terraform, schema, skill and no live action follows from
these documents. The gate at the end of the implementation plan was **passed on
2026-10-20 for Slice 0 and Slice 1 and for nothing later**.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-20. **Base:**
`main` at `54cb02a8` (`release 0.9.5`).

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's brief, **verbatim**, committed alone in its own first commit: the one sentence of 2026-10-20, then the context the owner gave with it the same day (the console's macOS-and-Linux-only state, and its deferral of Windows). Nothing in it was changed. |
| [reconciliation.md](reconciliation.md) | What the tree says about the brief: corrections **C1–C3**, findings **F1–F42** against `main` (`54cb02a8`), each classified **Wrong / Exists / Gap / Rule / Open** with a path and a Go symbol and its consequence; the headline table; the census (every construct the brief named, each count with **the exact command** beside it, and F37's rule that the commands and not the figures are the durable part); **decisions D1–D16**, each with a recommendation, the alternative and its cost, **as written before the owner's answers of 2026-10-20**; items O1–O6; the **UNVERIFIED** table **U1–U13**. Where it disagrees with `spec.md`, it wins. |
| [wsl.md](wsl.md) | The route that works today, and the one document here a Windows reader can act on: a Linux `fugaro` inside WSL is the Linux path exactly (**F39**), the three things that bite (a checkout under `/mnt/c`, gcloud's ADC as a *different file* inside the distribution, Claude Code needing to be inside WSL too), and what native Windows does not do yet (**F31**, **F3**, **F6**, **F20**, **F26**, **U3**, **F38**, **F40**). The plan's T0.1, written under **D16**'s approval of Slice 0 and Slice 1 (2026-10-20). |
| [implementation-plan.md](implementation-plan.md) | The plan: Slice 0 (say what works today — the WSL route, which is worth doing under every answer), Slice 1 (compile, start, read the right files), Slice 2 (the read-only commands tell the truth), Slice 3 (the test suite's five Windows classes), Slice 4 (setup and launch), Slice 5 (release and distribution), Slice 6 (CI and the gate); the support tier per command group, the minimum viable first cut and what it does **not** do, each task with files, a red step, a verification command, dependencies, a reviewer tier and a commit message, the edges, the levels and the critical path, the effort in tasks, and the **OWNER APPROVAL GATE**. **Slice 0 and Slice 1 are the approved tasks (D16, 2026-10-20); no others are** — and both are in the tree (Slice 1 below). |

## The headline, in one paragraph

**"The CLI has no Windows build" is true of the release, not of the tree
(C1).** It is also not as close to building as one command suggests:
`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` stops at the first
failing package and reports that one, so the honest count comes from compiling
**one package at a time with `-gcflags=-e`** — and that count **was** 14
non-test sites in nine packages, `internal/pluginwire`'s `syscall.O_NOFOLLOW`
and `internal/procgroup`'s process group being the two that a single command
shows you. **All fourteen are fixed**: Slice 1 is in the tree, and the command
that proves it is below. `internal/cli`'s 225 test files do **not** all type-check for
windows/amd64 either: four of them fail, and so do ten other packages' test
files (**F36**). The tree is already partly Windows-aware on purpose: five
documented `unix`/`other` splits, a CRLF-preserving YAML write, a `\r`-folding
Dockerfile parser, a platform-aware temp-root list, and one test file gated
`//go:build !windows` (C2). So the first cut is a port of **nine** packages
plus four platform lookups — one construct repeated, no new design, but nine
packages — and what makes the work substantial is that none of it can be
verified in this repository's CI until a Windows runner exists, and that the
four things a user notices first are behaviour rather than compilation.

## The first cut, and the exact command that proves it

**The first cut builds.** Slice 1 of the plan is in the tree as of 2026-10-20,
after the owner's approval of Slice 0 and Slice 1 (D16): all fourteen non-test
sites in nine packages are fixed — `internal/pluginwire`'s `syscall.O_NOFOLLOW`
symlink guard and `internal/procgroup`'s process group first, then the six
other `O_NOFOLLOW` sites as one `unix`/`other` pair per package,
`internal/image`'s three `syscall.Stat_t` sites, and the two platform lookups
(the config, cache and state directories, and gcloud's ADC file). The command
the headline's claim rests on, run from a Linux checkout:

```console
$ GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/fugaro.exe ./cmd/fugaro
$ echo $?
0
$ file /tmp/fugaro.exe
/tmp/fugaro.exe: PE32+ executable for MS Windows 10.00 (console), x86-64, 16 sections
```

and the per-package form the plan's `[cross]` uses — one package at a time,
with `-gcflags=-e`, because a single `go build ./...` stops at the first
failing package (C1) — is clean over every package `go list ./...` names; its
only output is the four entries that carry no non-test Go file
(`internal/e2e`'s build constraints, and `plugin`, `schemas` and `scripts`).

**What this does not claim.** No Windows artifact exists: nothing is released
for Windows until Slice 5 and no Windows CI job runs until Slice 6, and the
`fugaro.exe` above was produced by a cross-compile and has never been executed
— the build is not a run. The test files do not all type-check for
windows/amd64 yet (F36, Slice 3's T3.5): four of `internal/cli`'s and ten
other packages'. And the four things a user notices first — `doctor`,
`config ls`, `init` and the text the wizard prints — are behaviour rather than
compilation (Slice 2, Slice 4). **D7–D15 are still open**: the two rules this
slice needed — D8's config and cache directories, and the Windows answer to
the symlink guards, the process group and the ownership read — are implemented
where the plan marks them independent of a decision and are listed in the pull
request instead of being taken here.

One thing this slice deliberately does **not** do: `fugaro.yaml`'s
`commands.build` still builds the host platform only. Adding `GOOS=windows`
there would make every contributor's `fugaro verify build` cross-compile, which
is Slice 6's decision and costs every Linux build a Windows build's worth of
time (T1.5).

## The POSIX behaviour this slice changes

**Three POSIX behaviours change in this slice: a relative `$XDG_CONFIG_HOME`, `$XDG_CACHE_HOME` or `$XDG_STATE_HOME` stops deciding where fugaro writes, the ADC lookup stops consulting `$XDG_CONFIG_HOME` at all, and the watch acks file's fallback for a machine that names no usable home directory moves.**
**(1) A relative XDG variable is ignored.** The XDG spec's own rule is that
such a path "should be considered invalid and ignored", and one of the state
paths already did that — `internal/infra/workdir.go`'s lookup required an
absolute value before this port touched it. The others did not:
`internal/localcfg`'s `configDir` and `cachePath`, `internal/updatecheck`'s
`cachePath` and `internal/watch`'s `AckPath` took any non-empty value, so a
*relative* one was joined onto the process's working directory and decided
where the project config, the name check, the update check and the watch acks
were written. All of them now go through one place, `localcfg.UserDir`, which
ignores a relative value and falls back to `~/.config`, `~/.cache` and
`~/.local/state` — `%APPDATA%` and `%LOCALAPPDATA%` on Windows — exactly as the
workdir state path already did. The same rule now applies to the home directory
the fallback is built from, and there it is a refusal rather than a fallback:
main joined a relative `$HOME` and wrote the result under the working directory
(`rel/.config/fugaro`), where `UserDir` refuses it by name instead. An
**absolute** value is untouched, so the escape hatch every POSIX user has, and
every spec-abiding environment, sees the same paths as before; what changes is
a setting the spec calls invalid.

**(2) The ADC lookup no longer consults `$XDG_CONFIG_HOME`.** `init`'s
`adcInfo` and `managed apply`'s `adcFile` tried `$CLOUDSDK_CONFIG`, then
`$XDG_CONFIG_HOME/gcloud`, then `~/.config/gcloud`, so with an **absolute**
`$XDG_CONFIG_HOME` set, fugaro looked for
`application_default_credentials.json` there. The one builder
(`internal/cli/adcpath.go`) is gcloud's own rule instead: `$CLOUDSDK_CONFIG`
verbatim, else `%APPDATA%\gcloud` on Windows and `~/.config/gcloud` elsewhere
(F29, T1.4), because gcloud reads `$CLOUDSDK_CONFIG` and no XDG variable — the
directory the old step named is one gcloud never writes. A POSIX user who
keeps an ADC file under `$XDG_CONFIG_HOME/gcloud` by hand finds gcloud's file
consulted now and not hers; `$GOOGLE_APPLICATION_CREDENTIALS`, which names any
path at all, is how to point one that is not gcloud's. This is the change that
is a fix on Windows, where all three copies of the rule put the file under
`.config` — and it is still a POSIX path that moves, which is why it is here.
The relative-home refusal of (1) reaches this builder too: `os.UserHomeDir`'s
answer was used verbatim, and only an absolute `$HOME` (or `%USERPROFILE%`) is
now.

**(3) The acks file's no-home fallback moves.** `internal/watch`'s `AckPath`
read `$XDG_STATE_HOME` and `os.UserHomeDir()` itself and fell back to `.` when
there was no home at all, so a machine with `$HOME` unset wrote
`./.local/state/fugaro/watch-acks.json` and a relative `$HOME` was taken at
face value (`rel/.local/state/…`). Through `localcfg`'s state rule both end at
`./fugaro/watch-acks.json`. Both are relative to the working directory, and
both are the best-effort case — acks are local, and losing the file only shows
those failures again — but they are not the same file, so such a machine shows
the acknowledged failures once more. This is also where the last copy of the
directory rule went (F5, F29): the acks file was the one piece of state still
computed by hand, and on Windows it was the wrong answer
(`%USERPROFILE%\.local\state` beside `%LOCALAPPDATA%` for everything else).

It is recorded here rather than left to a comment because the plan's hardest
constraint is that no slice changes a POSIX code path, and these are the three
that do. The pins are `TestARelativeXDGVariableIsIgnoredOnPOSIX` in
`internal/localcfg` (with the relative rows of
`TestConfigDirFollowsThePlatform`, which carry a relative `$HOME` too),
`TestARelativeXDGCacheHomeIsIgnoredOnPOSIX` in `internal/updatecheck`,
`TestAckPathFollowsThePlatformStateDirectory` in `internal/watch` (its relative
row and the row for a machine that names no state directory) and
`TestADCPathFollowsThePlatform` in `internal/cli` (the two rows saying an
`$XDG_CONFIG_HOME` does not move gcloud's file).

## The one security deferral this slice leaves open

**A Windows release must not ship without D12.** `fugaro managed apply` writes
the operator's own refresh token — wrapped for the deployer account — into a
directory of its own under the system's temporary directory
(`internal/cli/managed_apply.go`, `writeImpersonationFile`): the file `0600`,
its directory `0700`. On Windows neither mode is what keeps another local user
out: there are no POSIX modes there, and Go's `os.Chmod` sets the read-only
attribute and nothing more, so what protects the file is the ACL `%TEMP%`
carries and the removal that follows every apply. That is weaker than the Linux
answer, and the answer is not this slice's to take — **D12 (file modes)** is
open in [reconciliation.md](reconciliation.md) §5, the code site says so in
place, and **Slice 5** is the slice that releases a Windows binary, so it is
the last one that can decide it before a user's token is on disk. Slice 1 did
not change this file's permissions; what it does is write the question down
where the release is planned.

The status line at the top of this file is the plan's own promise about the
folder and moves where the plan says it does — T0.3 and then T6.3, each with
the evidence that licenses the move — so it still reads as it did; what Slice 1
changed is recorded here.

## The numbers this work holds

| Block | State |
|---|---|
| **D1–D16** | this work's decisions, all written: **D1–D6 and D16 answered by the owner on 2026-10-20, D7–D15 still open**. They are a *folder-local* namespace and they overlap [docs/setup-wizard/](../setup-wizard/)'s D1–D12 **numerically**; the register of every block is [docs/coordinator/README.md](../coordinator/README.md) §"The numbers this work holds", which records [docs/credentials-service/](../credentials-service/) holding a global block instead (D130–D159), [docs/in-job-review/](../in-job-review/) holding **no D-block at all** (its questions were Q1–Q17), and **D193 onward as free** — which is the block this work would take if the owner prefers one sequence. **Recommendation: keep D1–D16 local and distinct by folder, or move this work to D193 onward; renumbering is mechanical and changes nothing but the numbers** (O6 below) |
| **Check 112** | **proposed** for the one part of this work that is genuinely a check in the register's sense — a first run on a real Windows machine, from a downloaded archive, by the owner, in three terminals (the U3, U5, U6, U8, U10 and U11 checks of [reconciliation.md](reconciliation.md)'s UNVERIFIED table, run as one sitting) — and **not claimed and not written** by this pull request. The six runner U-checks — U1, U2, U4, U7, U9 and U12 — are **not** register material: they run on a GitHub-hosted `windows-latest` runner, need no secret and no owner, and the register's subject is owner-run live checks (implementation-plan §6). The thirteenth, U13, belongs to the console's own design document (F40) |

D-numbering and check numbers are allocated, never reused and never
renumbered once taken.

## Items found while planning, kept here so they are not lost

The full list is **O1–O6** in [reconciliation.md](reconciliation.md) §6. One of
them is a document-level question and is repeated here because it is about
*these* documents rather than about the code:

- **O6 — the D-number namespace.** This work's D1–D16 and
  [docs/setup-wizard/](../setup-wizard/)'s D1–D12 are numerically the same
  range and are distinguished only by their folder. The register is
  [docs/coordinator/README.md](../coordinator/README.md) §"The numbers this
  work holds": it records
  [docs/credentials-service/](../credentials-service/) holding a global block
  (D130–D159) and **D193 onward as free**, and it also records that the two
  most recent folders took **no D-block at all** — in-job-review answered its
  owner's questions as Q1–Q17 and the coordinator allocated nothing. So three
  conventions exist in the tree, and this work is asked to pick one. Its
  recommendation: **keep the local namespace** — the folder is always named
  when a decision is cited, and the alternative (moving to D193 onward, as the
  credentials work did with D130–D159) is a rename with no benefit beyond the
  one this paragraph already provides.

## Related

- [docs/design/desktop-console.md](../design/desktop-console.md) — **D10**
  ("Windows and Linux: package them now, or later? — **Later**"), the ruling
  this work's context comes from; the console joins when the CLI has a Windows
  build and when the code-signing question is answered (F40).
- [SECURITY.md](../../SECURITY.md) — the claims this work must not repeat as
  Windows claims: the mode guarantee is POSIX-only, because Windows reports
  exactly two file modes and the CLI sets no ACL (F8, D12).
- [docs/release.md](../release.md) and [website/data/install.yaml](../../website/data/install.yaml)
  — the two files that name the released platforms in prose, pinned to
  `.goreleaser.yaml` by `internal/cli/docs_website_install_test.go` (C3, F35).
- [docs/pending-workflows/README.md](../pending-workflows/README.md) — the
  staging convention the Windows CI job must follow, because no Fugaro run may
  write under `.github/workflows/` (F38).
- [docs/gcp-setup.md](../gcp-setup.md) — the guide a first-time user follows.
  It carried no Windows text at all (**O3** was the finding that named the gap);
  it now carries the Windows and WSL forms of the two commands it has a user
  run, which is this work's **T0.2** (2026-10-20).
- [docs/setup-wizard/](../setup-wizard/) and
  [docs/credentials-service/](../credentials-service/) — the shape this folder
  follows: README index with a status line, `spec.md` verbatim and committed
  alone, `reconciliation.md` with F/D/O numbers, `implementation-plan.md` with
  slices, red steps and an owner gate.
