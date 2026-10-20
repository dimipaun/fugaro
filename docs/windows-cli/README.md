# The Fugaro CLI on Windows — the documents

**Status: plan only; nothing is implemented; decisions open.** The owner's
brief of 2026-10-20 asked for a design and a plan, not code: *"Let's work out a
plan on how to make the CLI work on Windows."* The owner gave the context the
same day — the desktop console ships for macOS (and Linux, unsigned, for
0.10.0) because its data source is the CLI and the CLI has no Windows build,
and Windows packaging for the console was deferred for that reason and for code
signing (`docs/design/desktop-console.md`, **D10**).

**D1–D16 in [reconciliation.md](reconciliation.md) §5 are open.** Each carries
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
| [wsl.md](wsl.md) | The route that works today, and the one document here a Windows reader can act on: a Linux `fugaro` inside WSL is the Linux path exactly (**F39**), the three things that bite (a checkout under `/mnt/c`, gcloud's ADC as a *different file* inside the distribution, Claude Code needing to be inside WSL too), and what native Windows does not do yet (**F31**, **F3**, **F6**, **F20**, **F26**, **U3**, **F38**, **F40**). The plan's T0.1, written under **D16**'s approval of Slice 0 and Slice 1 (2026-10-20). |
| [implementation-plan.md](implementation-plan.md) | The plan: Slice 0 (say what works today — the WSL route, which is worth doing under every answer), Slice 1 (compile, start, read the right files), Slice 2 (the read-only commands tell the truth), Slice 3 (the test suite's five Windows classes), Slice 4 (setup and launch), Slice 5 (release and distribution), Slice 6 (CI and the gate); the support tier per command group, the minimum viable first cut and what it does **not** do, each task with files, a red step, a verification command, dependencies, a reviewer tier and a commit message, the edges, the levels and the critical path, the effort in tasks, and the **OWNER APPROVAL GATE**. **No task is approved.** |

## The headline, in one paragraph

**"The CLI has no Windows build" is true of the release, not of the tree
(C1).** It is also not as close to building as one command suggests:
`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` stops at the first
failing package and reports that one, so the honest count comes from compiling
**one package at a time with `-gcflags=-e`** — and that count is **14 non-test
sites in nine packages**, `internal/pluginwire`'s `syscall.O_NOFOLLOW` and
`internal/procgroup`'s process group being the two that a single command shows
you. `internal/cli`'s 225 test files do **not** all type-check for
windows/amd64 either: four of them fail, and so do ten other packages' test
files (**F36**). The tree is already partly Windows-aware on purpose: five
documented `unix`/`other` splits, a CRLF-preserving YAML write, a `\r`-folding
Dockerfile parser, a platform-aware temp-root list, and one test file gated
`//go:build !windows` (C2). So the first cut is a port of **nine** packages
plus four platform lookups — one construct repeated, no new design, but nine
packages — and what makes the work substantial is that none of it can be
verified in this repository's CI until a Windows runner exists, and that the
four things a user notices first are behaviour rather than compilation.

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
