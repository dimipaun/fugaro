# The Fugaro Console on Windows — the documents

**Status: approved to build on 2026-10-21 — target release 0.11.0, branch `console-windows-wsl`; the
twelve decisions are answered, the OWNER APPROVAL GATE is passed and Part A (the fake `wsl.exe`) is
built.**
The owner ruled on **2026-10-20** that **the Console gets a Windows build in which the Fugaro CLI is
the *Linux* binary running inside WSL** — *"the CLI resolver runs
`wsl.exe -d <distro> -- fugaro …`"* — and that the **Windows-native CLI is a separate track**,
[docs/windows-cli/](../windows-cli/README.md). This directory is the plan for the console half of
that ruling and nothing else: it writes no production Go, no TypeScript, no `package.json`, no
workflow under `.github/workflows/` and no live action. Everything it asks for started at the plan's
OWNER APPROVAL GATE, which **passed on 2026-10-21**: the build is approved, the target release is
**0.11.0** — the ruling of 2026-10-20 named 0.10.0, which shipped without it — and the parts collect
on the long-lived branch **`console-windows-wsl`** until then.

**Everything outside this directory that the *planning* pull request touches, in full** — seven files,
and the list is the claim, so it is written out rather than summarized. The build's own files are not
in it: each is named by the task that writes it in [implementation-plan.md](implementation-plan.md),
the first two being Part A's `console/test/unit/wslstub.ts` and `console/test/unit/wslstub.test.ts`.

1. **`docs/pending-workflows/console-windows.yml`** — the staged release-time pack job itself.
2. **`docs/pending-workflows/README.md`** — the section that describes it, plus the repository's
   counts of the files still pending there, which staging this file moves: a count left behind is
   `main` red.
3. **`website/README.md`** — the same count and the same names, on the site's page.
4. **`website/test/readme.mjs`** — its words map gains `four`, because the sentence counts in words.
5. **`internal/cli/docs_desktop_console_test.go`** — one marker moved from `file is pending` to
   `files are pending` so that it stops depending on the count. This is **a change to an existing
   test file**, deliberately, and it is the only one.
6. **`internal/cli/docs_windows_console_test.go`** (new) — this folder's own guard, because a
   document is a claim and every other folder's documents in this repository are held to theirs by a
   Go guard. It pins the twelve answers, this plan's re-cut around the D7 ruling, the staged job's
   triggers, the two sentences outside this folder that describe it, and the release checklist's
   by-hand dry run.
7. **`docs/release.md`** — the release-time text and the by-hand dry run the release checklist
   gains, which the owner asked for in so many words: with the pack at release time, the dispatch
   before the tag is the only dry run there is.

**No production file changes** — no Go outside these two test files, no TypeScript, no
`package.json`, no workflow under `.github/workflows/` — **and `docs/design/desktop-console.md` and
`console/README.md` are not touched**: their dated notes are Part H's (O7, below).

**The build is the parts' own work, and it started on 2026-10-21.** Everything above describes the
*planning* pull request, which is what this folder is. The parts then write what their tasks name —
Part A's two files under `console/test/unit/` are the first, and they touch no `console/src/` file —
and each part's pull request carries its own surface. Nothing in the plan's §5 is relaxed by the
approval: the copy-in of the staged workflow, the register row and `website/data/install.yaml` are
still the owner's.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-20; **approved to build 2026-10-21.**
**Base:** `main` at `ccc406b2`
(`console: a Linux build (AppImage and .deb, unsigned) for 0.10.0 (#758)`).

| File | What it is |
|---|---|
| [reconciliation.md](reconciliation.md) | The task brief **verbatim** as §0 — the source this work is reconciled against — then corrections **C1–C6** to what the tree says today; findings **F1–F47** against `ccc406b2`, each classified **Wrong / Exists / Gap / Rule / Open** with a path and a symbol and its consequence; the headline table; the **census** (every construct the brief names, each count with **the exact command** beside it, and the rule that the commands and not the figures are the durable part); **decisions D1–D12**, each with its recommendation first, the alternative and its cost, and **all twelve answered by the owner on 2026-10-20** in the table at the head of §4, each with its one-line why; items **O1–O7**; and the **UNVERIFIED** table **U1–U19**. Where it disagrees with the brief, it wins and says so. |
| [implementation-plan.md](implementation-plan.md) | The plan: **Part A** the test seam (a fake `wsl.exe` built on the existing stub), **Part B** the packer and the fuse table, **Part C** the resolver, the wrapper and the distro, **Part D** paths, **Part E** the credential boundary, **Part F** spawning, streaming, cancellation and exit codes, **Part G** the first-run pages and the distro picker, **Part H** the release-time pack job, the release text and the staging. Each part merges alone, each task has files, a **red step**, a verification command, dependencies, a reviewer tier and a commit message, and every new test is mutation-proven. Then **§4 the owner's manual checklist** (Check 119, proposed and unclaimed) and **§5 the OWNER APPROVAL GATE**, which **passed on 2026-10-21** (target release 0.11.0, branch `console-windows-wsl`). **Part A is built, and the rest land in the order §3 gives.** |

## The headline, in one paragraph

**The console is already built to run a CLI it did not write, and the WSL route needs almost none of
its design to change — but the packer refuses Windows outright, and three of the resolver's
load-bearing POSIX assumptions make the unit suite unrunnable on a Windows runner.** `fuseTarget`
(`console/src/main/fuses.ts:379`) **throws** for any `electronPlatformName` outside
`SUPPORTED_PLATFORMS` (`fuses.ts:298` = `['darwin','linux']`), and `afterPack` is the pack's only
fuse flipper, so `electron-builder --win` cannot produce an artifact at all until the table gains a
`win32` row — there is no "pack it and fix it later". Under that, the resolver's three POSIX
assumptions are each a **one-line** change in a module that is already a pure function of what it is
given — and they are the **runner's** blockers rather than the route's, because the WSL route never
walks the host's directories (C6): `isExecutableFile`'s `stat.mode & 0o111` (`spawn.ts:850`,
meaningless on Windows, which
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
| **D1–D12** | this work's decisions, **all twelve answered by the owner on 2026-10-20** — the brief asks for "decisions the owner must make: numbered, recommended option first", every one is written that way in [reconciliation.md](reconciliation.md) §4, and the answer is the recommendation for eleven of them: **D1–D5 and D8–D12 are (a), D6 is (a)** (the Linux pid captured and killed inside the distribution, with the residual window written down) and **D7 is (c)** (the Windows pack runs **at release time only** — the tag or a manual dispatch before it, **never on a pull request** — which is the option the recommendation had refused; the accepted risk and the by-hand dry run are in the plan). **Answering a decision was not approving the build**: the two are separate dates — the answers are 2026-10-20 and the owner approved the plan itself on **2026-10-21** (target release 0.11.0, branch `console-windows-wsl`). They are a *folder-local* namespace, the convention [docs/windows-cli/](../windows-cli/README.md) and [docs/setup-wizard/](../setup-wizard/) both took, and they overlap those folders' ranges **numerically** and are distinguished only by their folder. The register is [docs/coordinator/README.md](../coordinator/README.md) §"The numbers this work holds", which records **D193 onward as free**; **recommendation: keep D1–D12 local** (O6) |
| **Check 119** | **proposed** for the one part of this work that is a check in the register's sense — the **owner's manual checklist** in [implementation-plan.md](implementation-plan.md) §4, run on one real Windows machine in one sitting — and **not claimed and not written** by this pull request. The register's written sections run to **89**; 90–99 are the credentials service's; 100–109 the functional spec's (105–109 reserved and unused); **110** is named by the P5.3 design and written by nobody; **111** is [docs/setup-wizard/](../setup-wizard/)'s; **112** is **proposed** by [docs/windows-cli/](../windows-cli/README.md); **113–118** are [docs/mobile/voice-session/](../mobile/voice-session/README.md)'s, **numbered and not written**. So **119** is the next free number after reading the register name by name, and the register's own collision rule (*whichever of two open claims merges second renumbers itself*) does not apply to it: no document claims 119. **None of the runner checks is register material** — they run on a GitHub-hosted `windows-latest` runner, need no secret and no owner, and the register's subject is owner-run live checks |

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
  rewrite. **One of the three is written by this pull request**: `docs/release.md`'s console
  paragraph and its "Before you tag" checklist carry the release-time note and the by-hand dry run,
  which the owner asked for here, and the paragraph says the build is *planned and staged* rather
  than shipped, so it does not claim a Windows build 0.10.0 does not have. **The other two —
  [docs/design/desktop-console.md](../design/desktop-console.md) §4.2, §7.6's rule 3, the owner-run
  table's signing row and D10's row, and [console/README.md](../../console/README.md)'s Linux
  section — are still untouched and still say Windows stays out.** They are **Part H**'s (T-H3), and
  until Part H lands the two are the older, narrower sentence rather than a contradiction: the
  ruling is a plan, the plan is **approved to build but unbuilt**, and nothing on Windows has shipped
  (F43, C4).

## Related

- [docs/windows-cli/](../windows-cli/README.md) — the **other track**: the CLI on native Windows.
  Its `wsl.md` is the authority for the WSL route this work drives (the install steps, the three
  traps, and the F40 line this work **supersedes for the console**). Its `reconciliation.md` does
  **not** hand the console's own Windows question to this folder: **U13, in that document's §7
  (UNVERIFIED) and not its §4, puts "anything about the desktop console on Windows" out of the CLI
  track's scope and says `docs/design/desktop-console.md` D10 owns it.** What this folder is
  written against is the owner's ruling of 2026-10-20 and that ruling's brief, not a hand-off from
  the CLI track — and the console's Windows *build* is the part of D10 the ruling moved, which is
  why D10 and this folder's §4 meet in Part H (O7, T-H3).
- [docs/design/desktop-console.md](../design/desktop-console.md) — the console's design: §3.4's
  rules 4, 5 and 7 (binary resolution, the fixed environment and working directory, the
  `~/.config/fugaro` refusal), §4.2 (what is out, including Windows as a *packaged* deliverable),
  §6.3's degradation table (the no-binary page this work's three new pages extend), §7.6 and
  **D10**.
- [docs/design/console-styling.md](../design/console-styling.md) — the theme and the layout the
  three new pages are drawn in: §2.7–§2.9 (the degraded, unavailable and refusal screens and the
  one table of every state variant), §3.5 (never colour alone) and §4 (the window's structure).
- [docs/pending-workflows/README.md](../pending-workflows/README.md) — the staging convention the
  pack job follows, because no Fugaro run may write under `.github/workflows/` (F44). Its
  `## console-windows.yml` section is this work's staged job — one `windows-latest` job that
  typechecks, runs the unit suite and packs the unsigned NSIS installer, **at release time only** (the
  release tag or a manual dispatch before it, and **never on a pull request**) and outside the release
  gate — and its header says in its first paragraph what must land in `console/` before the copy-in.
- [SECURITY.md](../../SECURITY.md) — the claims the Windows leg must not repeat as Windows claims.
  The credential boundary this work must preserve is stated there in the terms the console already
  meets (F21). **The mode guarantee is not**: `SECURITY.md` carries no Windows, POSIX, `chmod` or
  `0600`/`0700` sentence at all, so there is no POSIX-only caveat in it to cite — its mode claims
  are POSIX claims (windows-cli F8) and the statement that they are POSIX-only is **owed**
  (windows-cli D12), which is why this folder says the limit itself rather than pointing at a
  document that does not say it (F45, U12).
- [docs/mobile/](../mobile/README.md) and [docs/coordinator/README.md](../coordinator/README.md) —
  the register of every D-block and check block, and the folder shape this one follows: README index
  with a status line, `reconciliation.md` with F/D/O/U numbers, `implementation-plan.md` with parts,
  red steps and an owner gate.
