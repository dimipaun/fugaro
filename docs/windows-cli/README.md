# The Fugaro CLI on Windows — the documents

**Status: plan only; nothing is implemented; decisions open.** The owner's
brief of 2026-10-20 asked for a design and a plan, not code: *"Let's work out a
plan on how to make the CLI work on Windows."* The owner gave the context the
same day — the desktop console ships for macOS (and Linux, unsigned, for
0.10.0) because its data source is the CLI and the CLI has no Windows build,
and Windows packaging for the console was deferred for that reason and for code
signing (`docs/design/desktop-console.md`, **D10**).

**D1–D16 in [reconciliation.md](reconciliation.md) §5 are open** — D16 is the
approval of Slice 0 and Slice 1 as work, which is not an answer to any of the
other fifteen. Each carries
a recommendation and its reason first, then the alternative and its cost;
**none is taken.** Answering a decision is not approving a task, and no code,
config, workflow, Terraform, schema, skill or live action follows from these
documents. The gate at the end of the implementation plan is **not passed**.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-20. **Base:**
`main` at `54cb02a8` (`release 0.9.5`).

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's brief, **verbatim**, committed alone in its own first commit: the one sentence of 2026-10-20, then the context the owner gave with it the same day (the console's macOS-and-Linux-only state, and its deferral of Windows). Nothing in it was changed. |
| [reconciliation.md](reconciliation.md) | What the tree says about the brief: corrections **C1–C3**, findings **F1–F42** against `main` (`54cb02a8`), each classified **Wrong / Exists / Gap / Rule / Open** with a path and a Go symbol and its consequence; the headline table; the census (every construct the brief named, each count with **the exact command** beside it, and F37's rule that the commands and not the figures are the durable part); **decisions D1–D16**, each with a recommendation, the alternative and its cost, **none taken**; items O1–O6; the **UNVERIFIED** table **U1–U13**. Where it disagrees with `spec.md`, it wins. |
| [implementation-plan.md](implementation-plan.md) | The plan: Slice 0 (say what works today — the WSL route, which is worth doing under every answer), Slice 1 (compile, start, read the right files), Slice 2 (the read-only commands tell the truth), Slice 3 (the test suite's five Windows classes), Slice 4 (setup and launch), Slice 5 (release and distribution), Slice 6 (CI and the gate); the support tier per command group, the minimum viable first cut and what it does **not** do, each task with files, a red step, a verification command, dependencies, a reviewer tier and a commit message, the edges, the levels and the critical path, the effort in tasks, and the **OWNER APPROVAL GATE**. **No task beyond Slice 0 and Slice 1 is approved** — the owner approved those two on 2026-10-20 (D16), and Slice 1 is in the tree (below). |

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
| **D1–D16** | this work's decisions, all written, **all open**. They are a *folder-local* namespace and they overlap [docs/setup-wizard/](../setup-wizard/)'s D1–D12 **numerically**; the register of every block is [docs/coordinator/README.md](../coordinator/README.md) §"The numbers this work holds", which records [docs/credentials-service/](../credentials-service/) holding a global block instead (D130–D159), [docs/in-job-review/](../in-job-review/) holding **no D-block at all** (its questions were Q1–Q17), and **D193 onward as free** — which is the block this work would take if the owner prefers one sequence. **Recommendation: keep D1–D16 local and distinct by folder, or move this work to D193 onward; renumbering is mechanical and changes nothing but the numbers** (O6 below) |
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
- [docs/gcp-setup.md](../gcp-setup.md) — the guide a first-time user follows;
  it has no Windows text at all today (O3, implementation-plan T0.2).
- [docs/setup-wizard/](../setup-wizard/) and
  [docs/credentials-service/](../credentials-service/) — the shape this folder
  follows: README index with a status line, `spec.md` verbatim and committed
  alone, `reconciliation.md` with F/D/O numbers, `implementation-plan.md` with
  slices, red steps and an owner gate.
