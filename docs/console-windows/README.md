# The Fugaro Console on Windows — the documents

**Status: reconciliation only; no code is written, no decision is answered and no task is approved.**
The owner ruled on **2026-10-20** that **0.10.0 gets a Windows build of the Console in which the
Fugaro CLI is the *Linux* binary running inside WSL** — *"the CLI resolver runs
`wsl.exe -d <distro> -- fugaro …`"* — and that the **Windows-native CLI is a separate track**,
[docs/windows-cli/](../windows-cli/README.md). This directory is the plan for the console half of
that ruling and nothing else: it writes no Go, no TypeScript, no `package.json`, no workflow under
`.github/workflows/` and no live action. Everything it asks for starts at the plan's OWNER APPROVAL
GATE.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-20. **Base:** `main` at `ccc406b2`
(`console: a Linux build (AppImage and .deb, unsigned) for 0.10.0 (#758)`).

| File | What it is |
|---|---|
| [reconciliation.md](reconciliation.md) | The task brief **verbatim** as §0 — the source this work is reconciled against — then corrections **C1–C5** to what the tree says today; findings **F1–F45** against `ccc406b2`, each classified **Wrong / Exists / Gap / Rule / Open** with a path and a symbol and its consequence; the headline table; the **census** (every construct the brief names, each count with **the exact command** beside it, and the rule that the commands and not the figures are the durable part); **decisions D1–D12**, each with its recommendation first, the alternative and its cost, and none of them answered; items **O1–O7**; and the **UNVERIFIED** table **U1–U19**. Where it disagrees with the brief, it wins and says so. |
| [implementation-plan.md](implementation-plan.md) | The plan: **Part A** the packer and the fuse table, **Part B** the resolver, **Part C** paths, **Part D** the credential boundary, **Part E** spawning, streaming and cancellation, **Part F** the first-run pages, **Part G** the installer job and the release text, **Part H** the test strategy; each part ordered so it merges alone, each task with files, a **red step**, a verification command, dependencies, a reviewer tier and a commit message; the **owner manual checklist** (Check 119, proposed); and the **OWNER APPROVAL GATE**. **Nothing is approved: Part A is the first part that may start, and only after the gate passes.** |

## The headline, in one paragraph

**The console is already built to run a CLI it did not write, and the WSL route needs almost none of
its design to change — but three of its load-bearing POSIX assumptions are compile-time-adjacent
blockers, and the packer refuses Windows outright.** `fuseTarget`
(`console/src/main/fuses.ts:379`) **throws** for any `electronPlatformName` outside
`SUPPORTED_PLATFORMS` (`fuses.ts:298` = `['darwin','linux']`), and `afterPack` is the pack's only
fuse flipper, so `electron-builder --win` cannot produce an artifact at all until the table gains a
`win32` row — there is no "pack it and fix it later". Under that, the resolver's three POSIX
assumptions are each a **one-line** change in a module that is already a pure function of what it is
given: `isExecutableFile`'s `stat.mode & 0o111` (`spawn.ts:850`, meaningless on Windows, which
reports two modes), `binDirectories`' and `childPath`'s `':'` separator (`spawn.ts:825`,
`spawn.ts:920`), and `privateWorkingDirectory`'s uid/chmod/lstat chain (`spawn.ts:955`) — which
Windows does not have at all. **The credential boundary needs no change and must not get one**:
`childEnvironment` (`spawn.ts:894`) already builds the child's whole environment from the fixed
allow-list `CHILD_ENV_KEYS` (`spawn.ts:247`) with **no merge with `process.env` anywhere in the
file**, `nodeSpawner` (`spawn.ts:1082`) already passes `stdio: ['ignore','pipe','pipe']` so stdin is
closed, and `SPAWN_FLAGS` (`spawn.ts:272`) has no secret-bearing flag to emit — so the Windows
environment cannot leak into the distribution and no secret can ride on `wsl.exe`'s argv. What is
genuinely new is the **translation layer** (a Windows path is not a Linux path, and the console must
call `wslpath` in both directions and keep its own store on the host), the **cancellation** problem
(killing the Windows-side `wsl.exe` is not documented to kill the Linux child, and `runToCompletion`
settles on `close` and kills with `SIGTERM`/`SIGKILL`, `spawn.ts:1224`), and **three first-run
pages** in the existing no-binary pattern (`console/src/renderer/screens/degraded.ts:575`).

## The numbers this work holds

| Block | State |
|---|---|
| **D1–D12** | this work's decisions, **all twelve open** — the brief asks for "decisions the owner must make: numbered, recommended option first", and every one is written that way in [reconciliation.md](reconciliation.md) §4. They are a *folder-local* namespace, the convention [docs/windows-cli/](../windows-cli/README.md) and [docs/setup-wizard/](../setup-wizard/) both took, and they overlap those folders' ranges **numerically** and are distinguished only by their folder. The register is [docs/coordinator/README.md](../coordinator/README.md) §"The numbers this work holds", which records **D193 onward as free**; **recommendation: keep D1–D12 local** (O6) |
| **Check 119** | **proposed** for the one part of this work that is a check in the register's sense — the **owner's manual checklist** in [implementation-plan.md](implementation-plan.md) §7, run on one real Windows machine in one sitting — and **not claimed and not written** by this pull request. The register's written sections run to **89**; 90–99 are the credentials service's; 100–109 the functional spec's (105–109 reserved and unused); **110** is named by the P5.3 design and written by nobody; **111** is [docs/setup-wizard/](../setup-wizard/)'s; **112** is **proposed** by [docs/windows-cli/](../windows-cli/README.md); **113–118** are [docs/mobile/voice-session/](../mobile/voice-session/README.md)'s, **numbered and not written**. So **119** is the next free number after reading the register name by name, and the register's own collision rule (*whichever of two open claims merges second renumbers itself*) does not apply to it: no document claims 119. **None of the runner checks is register material** — they run on a GitHub-hosted `windows-latest` runner, need no secret and no owner, and the register's subject is owner-run live checks |

D-numbering and check numbers are allocated, never reused and never renumbered once taken.

## Items found while planning, kept here so they are not lost

The full list is **O1–O7** in [reconciliation.md](reconciliation.md) §5. Two are repeated here
because they are about *these* documents rather than about the code:

- **O6 — the D-number namespace.** This work's D1–D12, [docs/windows-cli/](../windows-cli/README.md)'s
  D1–D16 and [docs/setup-wizard/](../setup-wizard/)'s D1–D12 are numerically the same range,
  distinguished only by their folder, while [docs/credentials-service/](../credentials-service/) took
  a global block (D130–D159) and [docs/in-job-review/](../in-job-review/) took no block at all
  (Q1–Q17). Four conventions now exist in the tree. **Recommendation: keep the local namespace** —
  every citation names the folder, and moving to D193 onward is a rename with no benefit beyond the
  one this paragraph already gives.
- **O7 — the three documents that still say Windows is out.** [docs/design/desktop-console.md](../design/desktop-console.md)
  §4.2 and its D10 row, [docs/release.md](../release.md)'s console paragraphs and
  [console/README.md](../../console/README.md)'s Linux section all end with a sentence that Windows
  stays out of 0.10.0. The owner's ruling of 2026-10-20 changes the console's Windows *build*, not
  the *signing* answer D10 gave, so each of those sentences needs a dated note rather than a
  rewrite — and **this directory writes none of them**, because the brief is docs-only under
  `docs/console-windows/`. Part G of the plan is where they land (F43, C4).

## Related

- [docs/windows-cli/](../windows-cli/README.md) — the **other track**: the CLI on native Windows.
  Its `wsl.md` is the authority for the WSL route this work drives (the install steps, the three
  traps, and the F40 line this work **supersedes for the console**), and its
  `reconciliation.md` §4 U13 hands the console's own Windows question to this folder.
- [docs/design/desktop-console.md](../design/desktop-console.md) — the console's design: §3.4's
  rules 4, 5 and 7 (binary resolution, the fixed environment and working directory, the
  `~/.config/fugaro` refusal), §4.2 (what is out, including Windows as a *packaged* deliverable),
  §6.3's degradation table (the no-binary page this work's three new pages extend), §7.6 and
  **D10**.
- [docs/design/console-styling.md](../design/console-styling.md) — the theme and the layout the
  three new pages are drawn in: §2.7–§2.9 (the degraded, unavailable and refusal screens and the
  one table of every state variant), §3.5 (never colour alone) and §4 (the window's structure).
- [docs/pending-workflows/README.md](../pending-workflows/README.md) — the staging convention the
  installer job follows, because no Fugaro run may write under `.github/workflows/` (F44).
- [SECURITY.md](../../SECURITY.md) — the claims the Windows leg must not repeat as Windows claims:
  the mode guarantee is POSIX-only, and the credential boundary this work must preserve is stated
  there in the terms the console already meets.
- [docs/mobile/](../mobile/README.md) and [docs/coordinator/README.md](../coordinator/README.md) —
  the register of every D-block and check block, and the folder shape this one follows: README index
  with a status line, `reconciliation.md` with F/D/O/U numbers, `implementation-plan.md` with parts,
  red steps and an owner gate.
