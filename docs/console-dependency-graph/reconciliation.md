# The console dependency graph — reconciliation

**Checked against `origin/main` at `cc50b5b23` on 2026-10-22**, and, where named, against the
branch `origin/coordinator` at `b07463c91` (coordinator P1). Every path and symbol below was
re-read at that commit; line numbers are not cited because they drift. **Where this document
disagrees with [spec.md](spec.md), this document wins.**

The spec is the console part of the owner's "Release Names, Watch Fixes and Console Dependency
Graph" spec plus its "Independent Release Streams" addendum. It was written without the repository
in view, and it reads greenfield. It is not: console v2 already designed a dependency graph and a
version roadmap (decisions D21–D38 in [desktop-console-v2.md](../design/desktop-console-v2.md),
all answered 2026-10-15/16), the graph document and its CLI read already ship, and the coordinator
(target 0.11.0) plans a project record that holds release targeting. This document says what
exists, what the spec gets wrong, what it leaves open, and what the owner's answers of
2026-10-22 (**GD1–GD8**, §3) do to each other (§4).

**Numbering.** This folder holds its own prefixes: decisions **GD1–GD8**, findings **F1–F38**,
and the plan's tasks **GV0.1–GV4.1**. Console v2 owns D21–D38 and nothing here reuses or renumbers
them. Check number **123** is taken by this work (the highest in use on `main` and in open pull
requests is 122, reserved by PR #922).

## 1. The spec's concepts against what the repository has

| Spec concept | What the repository already has | Where |
| --- | --- | --- |
| "Full task dependency graph" | A plan-side graph document and its read, `fugaro plan graph --json` (one plan at a time, schema v1) | `internal/plan/document.go` (`Document`, `Node`, `Edge`), `internal/cli/plan.go` (`newPlanGraphCmd`), `console/docs/json-contract.md` §6.9; design D25 |
| "Boxes per planned release" | `Node.Version`, a free string, "the roadmap's source of truth now" (D29); the coordinator view's `Record.Release`, read from a `release/v…` branch (branch `coordinator` only) | `internal/plan/document.go`; `origin/coordinator:internal/coordinator/view.go` (`ReleaseOf`) |
| "Critical path" | Planned, not built: V2.2.3 (D26, critical path in Go) | `docs/plans/2026-10-15-desktop-console-v2.md` V2.2.3 |
| Node state colours | Run stages in the watch feed | `internal/watch/json.go` (`JSONRun.Stage`), `internal/runner/runner.go` |
| "Clicking a node opens its PR" | The validated external open | `console/src/main/window.ts` (`openExternalValidated`), operation `link.openExternal` in `console/src/main/ipc.ts` |
| "Dragged to another release" | Ruled out: D30, read-only roadmap with "copy the change" | `docs/design/desktop-console-v2.md` D30, plan V2.5.3 |
| ELK layout | Ruled out by D9 (no renderer dependencies), pinned by tests | `docs/design/desktop-console.md` D9, `console/test/unit/pinning.test.ts`, `console/tools/bundle-renderer/index.ts` |
| Comet, pulse, animated flow | Ruled out by console-styling §3.9 ("nothing else"; "The console animates no chart") | `docs/design/console-styling.md` §3.9, `console/src/renderer/theme/base.css` |
| Three release streams | One tag releases everything; console and mobile ride the CLI tag | `docs/release.md`, `.github/workflows/console-release.yml` |
| Project-level graph and roadmap | Coordinator spec §5/§10 and plan P1/P3/P4/P6 (0.11.0) | `docs/coordinator/` |

## 2. Findings

Classes: **Wrong** (the spec contradicts the code), **Exists**, **Gap**, **Rule** (conflicts with a
recorded ruling or design), **Open**.

### A. The data

**F1 — Exists: the graph read ships.** `internal/plan/document.go` (`Document`, `Node{ID, Title,
Run, Version, Footprint}`, `Edge{Task, Needs, Reason}`, `SchemaVersion = 1`, `ArtifactSuffix =
".graph.json"`), `internal/plan/read.go` (`ReadFile`, `Parse`, `Normalize`, `findCycle`,
`MaxArtifactBytes` = 1 MiB), `internal/cli/plan.go` (`newPlanGraphCmd`, `resolveGraphArtifact`),
contract `console/docs/json-contract.md` §6.9. *Consequence:* the view is built on this document,
extended; no Markdown scraper and no second parser. The console reads it only through the CLI
(design `desktop-console.md` §5.1; v2 design's "the CLI is the only data plane").

**F2 — Gap: nothing writes a graph artifact.** No `*.graph.json` file exists anywhere in the tree;
the only graph documents are the fixtures `graph.json`, `cycle.json`, `future.json` and
`malformed.json` under `internal/plan/testdata/`. `plugin/skills/parallelism/SKILL.md` (52 lines) does not mention the
graph or the footprint, so V2.8.1 is not done. *Consequence:* the view has no data on day one; a
writer is a prerequisite (plan GV1.7).

**F3 — Wrong (spec assumption): the plan documents are not a parseable source.** About 800
task-like headings across 13 `docs/**/implementation-plan.md` and the `docs/plans/*.md` files,
about 440 "Depends on" lines in five spellings, task-id styles that vary per plan, dependency
targets that mix task ids, decision rows, owner events and cross-plan prose, and ids that collide
across plans (`W0.1` in `docs/website/` and `docs/setup-wizard/`; `P0`… in `docs/coordinator/`,
`docs/central-orchestrator/` and `docs/mobile-auth/`). Counts are grep approximations (UNVERIFIED
U4). *Consequence:* the graph comes from structured artifacts; any cross-plan reference needs a
plan namespace (`setup-wizard/W1.3`).

**F4 — Gap: no cross-plan edge is possible today.** `plan graph` reads one artifact, and
`Normalize` refuses an edge naming a node not in the same document. Newer plans live in
`docs/<topic>/implementation-plan.md`, outside the command's default `docs/plans` search.
*Consequence:* cross-release and cross-stream edges need a qualified-id form and a reader across
plans. Under GD1 that waits on the coordinator's record (§4 (d), ruled); the first slice shows
per-plan graphs side by side with no cross-plan edges.

**F5 — Gap: no stream or release per task.** `Node.Version` is free text and absent everywhere;
nothing models a release name (no `Badger`, `Camel` or `Mango` anywhere in the tree). *Consequence:*
a stream catalogue (GD7) and a per-artifact stream field (plan GV1.3) are new.

**F6 — Rule (vs. the addendum): the streams are tied together today.** `docs/release.md`: "One
SemVer tag `vX.Y.Z` releases everything"; the console's `console-vX.Y.Z` is built from the CLI's
tag (`.github/workflows/console-release.yml`, `on.push.tags: ["v[0-9]+.[0-9]+.[0-9]+"]`); mobile's
`mobile/app.json` says `0.0.0` and its release workflow is staged under `docs/pending-workflows/`.
*Consequence:* independent streams are a release-engineering change owned by the "Release Names"
part of the owner's spec, with owner-applied workflows. The graph can draw streams before that
lands, but a "Console · Mango" box describes a release the pipeline cannot cut yet.

**F7 — Superseded proposal: a roadmap file.** The recon proposed one roadmap file (releases,
assignment, cross-plan edges). **GD1 declined it** (not the recommendation): no new roadmap file
now. Kept here only so the alternative stays readable.

### B. Live state

**F8 — Exists: run state.** `fugaro watch --json` (`internal/watch/json.go` `JSONRun`,
`JSONFinished`; contract §2) carries `run`, `title`, `stage`, `round`, `health`, `halted`,
`queued`, `stuck`, `waiting_until`, `spent_usd`, and on finished rows `status`, `outcome`,
`pr_number`, `pr_url`. Stages written by the runner: `bootstrap`
(`internal/runner/budget_session.go`), `provision`, `plan`, `implement`, `review_first`, `review`,
`fix`, `check`, `finalize`, `writeback`, `render` (`internal/runner/runner.go` and siblings).
*Consequence:* blue/purple/amber map to `implement` / `review_first`+`review` / `fix`; the spec's
"extend for other known states" is about eight more (plan GV2.3 names each).

**F9 — Gap (in the feed only): a live run carries no PR number in `watch`.** Contract §2: "A live
run carries no PR field at all". But the runner records the early draft PR at once
(`internal/runner/prflow.go` `notePR` sets `r.rec.PR` and saves the record), and
`internal/runview/runview.go` copies it to `Row.RecordPR`, which `runs ls --json` prints as
`record_pr`. *Consequence:* `plan status` (GV1.6), which joins through `internal/runview`, prints a
running node's PR number with no feed change; a `watch` field (GV2.5) is optional.

**F10 — Open → GD5: linking a node to a run.** `Node.Run` is the only designed link and nothing
writes it; writing a run id back into a committed artifact is a commit per launch. Titles are free
text and not unique. *Consequence:* GD5, a plan-task id recorded at launch.

**F11 — Wrong (terminology): "the job's last four characters".** A run id is
`YYYYMMDD-HHMMSS-xxxx` (`internal/task/task.go` `NewRunID`, two random bytes in hex); "job" in this
tree is the Cloud Run job (`JSONRun.job`), shared per profile. *Consequence:* the node shows
"run …xxxx", a display hint of 65,536 values, never an identifier.

### C. The console's architecture

**F12 — Rule: the renderer has no runtime dependency, by decision and by test.**
`console/package.json` has no `dependencies` section, asserted by
`console/test/unit/pinning.test.ts`; design D9 ("TypeScript, no framework runtime, charts on the
platform", `docs/design/desktop-console.md`); `console/tools/bundle-renderer/index.ts` refuses any
bare specifier (`console/test/unit/bundle.test.ts`, "refuses a bare specifier"). *Consequence:*
GD3 reverses D9; it needs a bundler change, an exact pin, the lockfile and a supply-chain review
(plan GV2.2).

**F13 — Rule: the CSP and the served-file allow-list.** `console/src/main/protocol.ts`:
`CONTENT_SECURITY_POLICY` is `default-src 'none'; script-src 'self'; … connect-src 'none'; …` (no
`worker-src`, no `'unsafe-eval'`, no `'wasm-unsafe-eval'`), and `SERVED_FILES` is an exact
six-file list; `console/test/unit/window.test.ts` and `csp-document.test.ts` pin both; the
packer's `build.files` in `console/package.json` lists each bundle file by name. *Consequence:* a
same-origin worker script is allowed by the `script-src` fallback but needs a seventh
`SERVED_FILES` entry and a `build.files` entry; anything needing eval or WASM would need a CSP
change, which this plan does not propose. **Read from the `elkjs` tarballs (2026-10-22, in a scratch
directory, deleted):** in 0.12.0, `lib/main.js` and `lib/elk.bundled.js` contain a bare
`require('web-worker')`, which `console/tools/bundle-renderer` refuses by design. In **0.11.0**, the
version the owner pinned (§4 (c)), `lib/elk-worker.min.js` is **1,613,729 bytes**, contains no
`require`, no `eval(` and no `new Function(`, has its own `onmessage`, and the package ships its
`LICENSE.md` (EPL-2.0); 0.12.0's copy (1,595,334 bytes) is the same in those respects. So the worker file is served **verbatim** (copied, not walked by the bundler) and the
renderer talks to it with plain `postMessage`; the bare-specifier rule is untouched. Since
`protocol.ts`'s `bundleHeaders` puts the CSP on every served response, the worker runs under
`script-src 'self'` too.

**F14 — Open → GD3: the licence.** The repository is MIT (`LICENSE`). npm metadata, read
2026-10-22: `elkjs@0.11.0` is `EPL-2.0`; `elkjs@0.12.0` (published 2026-07-17) is `EPL-2.0 OR
GPL-3.0-or-later`. No licence policy exists in the console's tests or docs, and the packed app
ships no third-party notices file. *Consequence:* the owner confirmed EPL-2.0 on 2026-10-22 and
pinned **0.11.0**, shipped with its licence text and the file unmodified (§4 (c)).

**F15 — Considered: layout without ELK.** A layered layout in Go beside `internal/plan` (D26
already puts the critical path in Go) or a small hand-written layered layout in TypeScript keeps
D9 and the CSP untouched. **GD3 chose elkjs** (not the recommendation); kept as the fallback if the
licence had been declined; the owner confirmed it (§4 (c)), so this stays a note.

**F16 — Exists: rendering and sanitizing patterns.** Screens are plain TypeScript DOM
(`console/src/renderer/screens/{live,spend,projects,run,degraded}.ts`); `console/src/shared/sanitize.ts`;
design rule `desktop-console.md` §5.1 rule 7 "Job-written text is text … never innerHTML … never a
href built from it"; navigation is `VIEWS` in `console/src/renderer/entry.ts`. *Consequence:* task
titles (repository-written) and run titles (job-written) both go through the sanitizer; a node
click opens a PR only through `openExternalValidated`, never through an href built from text.

**F17 — Rule → GD4: motion.** `docs/design/console-styling.md` §3.9: motion is the 120 ms hover
transition "and **nothing else**"; "The console animates no chart". §3.5 "Never colour alone":
every coloured state carries a word or glyph. *Consequence:* GD4 amends §3.9 for this one screen,
with a text mark on every coloured state and a static fallback.

### D. The critical path

**F18 — Gap: nothing computes levels or a critical path.** No code in `internal`, `cmd` or
`console/src` computes a topological order or a critical path; `internal/plan` only detects cycles
(`findCycle`). V2.2.3 (D26) is planned and unbuilt. There are no durations, so "critical" is the
longest chain by count until a weight exists. *Consequence:* computed in Go (plan GV1.1), never in
the renderer, so two consoles cannot disagree.

### E. Streams

**F19 — Gap: no stream or name catalogue.** `console/src/shared/version.ts` knows only the CLI's
version (`CLI_VERSION_BUILT_AGAINST`, `SUPPORTED_CLI_MINORS`, `CLI_HARD_FLOOR_VERSION`).
*Consequence:* GD7's data file is new. Note the helpful fact: the console's compatibility
handshake is keyed on the **CLI's** version, not the console's own, so a console stream with its
own numbers does not break the handshake.

### F. Overlap with planned work

**F20 — Exists: console v2 already designed this view.** `docs/design/desktop-console-v2.md` F6/F7
(graph, four colours), F22–F24 (version roadmap), D25/D26/D28/D29/D30; plan
`docs/plans/2026-10-15-desktop-console-v2.md` V2.2.3, V2.3.1, V2.5.3, V2.6.1, V2.6.2, V2.8.1.
V2.2.1 and V2.2.2 merged; the plan's gate sentence "Nothing in v2 is approved or built" is stale
for those two. *Consequence:* this work is a **revision** of console v2's V2.3 and V2.6 phases,
not a parallel screen. The plan's §2 lists exactly which tasks it supersedes or amends.

**F21 — Exists (planned): the coordinator's project view.** `docs/coordinator/spec.md` §5 (state:
"PR records and the PR dependency graph", "Version roadmap: future releases and which PRs target
which version") and §10 (the console project view); plan P1 (`fugaro coordinator status`, **built
on branch `coordinator`**, `origin/coordinator:internal/coordinator/view.go` `Record.Release`),
P3 (the record), P4 (intent record with target release), P6 (the console's project view), target
0.11.0. *Consequence:* the coordinator's record is **PR-level** (records, runs, release); it holds
no task and no task-to-task edge. GD1 makes it the graph's future source, so the graph is PR- and
run-level first, task-level only when that record carries tasks (§4 (d), ruled: per-plan task graphs now).

**F22 — Exists: styling.** `docs/design/console-styling.md` (tokens, §3.4 contrast, §3.5, §4.3
keyboard, §4.4 forced colours) governs any new screen.

### G. Hard questions

**F23 — Rule → GD2: dragging is ruled out.** D30 (answered 2026-10-15/16): "there is no
drag-and-drop write: the app holds no path into the checkout" (V2.5.3); its rejected alternative
names the reason: a write into the operator's working tree "can clobber uncommitted work".
*Consequence:* GD2 reverses D30 (§4 (b), confirmed).

**F24 — Open: performance.** 1 MiB per artifact; the console spawns the CLI per read and re-reads
the live feed on a one-second floor (contract §10.1). ELK on hundreds of nodes on the renderer
thread would block input. *Consequence:* layout in a worker (GD3), recomputed only when the graph's
structure changes, never on a watch frame (plan GV2.3 pins it).

**F25 — Open: accessibility.** §3.5, §4.3 and §4.4 require text marks, keyboard traversal,
forced-colours support and reduced motion; an SVG graph also needs a non-visual equivalent.
*Consequence:* the screen ships a list view of the same document (plan GV2.3).

**F26 — Exists: platforms.** macOS signed, Linux unsigned, Windows staged. Nothing in the view is
platform-specific.

**F27 — Gap: the checkout.** `plan graph` reads files in a checkout; the console spawns in a
private working directory **only when the installation has no checkout** (`console/src/main/spawn.ts`
header rule 5: the checkout is the cwd when there is one; `PRIVATE_DIR_LEAF` otherwise), and an
installation's checkout is optional (`console/src/main/installations.ts`, `INSTALLATION_KEYS` includes
`checkout`). *Consequence:* the view works only for an installation with a checkout, shows that
checkout's branch (not `main`), and the directory comes from the spawn's cwd, which the main process
sets — never from the renderer and never as an extra argv (plan GV2.1).

**F28 — Proposal: the smallest useful first slice** — see §5.

### H. Found while reconciling (beyond the recon)

**F29 — Wrong (path in GD7's proposal): `docs/releases/streams.yaml` breaks the website build.**
`website/src/lib/changelog.mjs` `buildChangelog` reads every file in `docs/releases/` except
`README.md` and **throws** on any name that is not `vX.Y.Z.md` ("every file there is vX.Y.Z.md,
and README.md … is the only file that is not one"). *Consequence:* GD7's file moves to
**`docs/release-streams.yaml`** (amended after review of the code; accepted by the owner
2026-10-22, §4 (e)).

**F30 — Rule: the coordinator's record is read-only to people and written behind its own front
door.** Coordinator spec §7: "Local agents never touch Firestore directly. The coordinator writes
everything behind the MCP interface"; plan P3: the writer is the aggregation job's service account,
and the rules change "is a **read** grant only, for the people the CLI and the phone already
authenticate as"; the store is the realtime database's `coordinator/` subtree (D195, answered
2026-10-21). *Consequence:* a console that "writes the roadmap" (GD2) into that record has no path
under the coordinator's own design: it needs either the front door (P8, not started) or a new
write grant (a rules change the coordinator plan does not have). §4 (a): ruled read-only until 0.11.0.

**F31 — Gap: two things are called "the project record".** D29 (answered) means project-centric
phase 6's backing repository (`fugaro project export`, `docs/plans/2026-10-14-project-centric.md`,
not built: no `export` subcommand in `internal/cli`); the coordinator plan calls its realtime
database subtree "the project record" (P3's commit message). *Consequence:* GD1 picks the
coordinator's; that amends D29's "the project record later" half, and GV0.1 records it.

**F32 — Gap: `launch.json` is not trusted data.** `internal/runstore/launch.go` (`Launch`,
`WriteLaunch` create-once); `internal/cli/run.go`'s `existingLaunch` comment: "the run's service
account can write both objects". It is also re-written on a recovery path from the run record
(`existingLaunch`), which would drop a field that only the launch carried. *Consequence:* GD5's
plan-task id is a **declared, display-only join hint**: validated for shape on write and on read,
sanitized in the console, never used to authorize anything; and it is stored in `task.json`'s
`task.Spec` too, so the recovery path and `fugaro run --retry` keep it (plan GV1.2).

**F33 — Gap: the console stream's first number.** `console-v0.10.0` is already released (GitHub
release "Fugaro Console 0.10.0"), and auto-update is planned (console Task 4.4, `console/README.md`).
*Consequence:* with GD7's floor, the console stream's first own minor ("Cherry") is **0.110.0**,
never 0.10.x reused under a new name and never 0.11.0, which is the CLI's Camel (the owner's
renumbering ruling of 2026-10-24); the data file's validator pins `console ≥ 0.110.0`
(plan GV1.4, `streams.ConsoleFloor`).

**F34 — Exists: the console already writes files, but only its own.**
`console/src/main/installations.ts` writes `installations.json` into the app's own directory
(exclusive create, mode `0o600`, temp file then rename); `console/src/main/spawnlog.ts` appends
the opt-in spawn log. Neither writes into a checkout or into project state. *Consequence:* GD2's
write is the first console write into the operator's project, and the first world-changing write
beside D22's launch.

**F35 — Rule: GD2 also amends D22.** D22 (answered 2026-10-16): "one write, launch … everything
else stays read-only"; its rejected alternative names "a drag-and-drop the console performs
itself" as a cost. *Consequence:* GD2 is an amendment to **D22 and D30**, not to D30 alone (§4 (b), confirmed).

**F36 — Gap: the reduced-motion rule freezes animations, it does not restyle them.**
`console/src/renderer/theme/base.css` sets `animation-duration: 0.01ms` and
`animation-iteration-count: 1` under `prefers-reduced-motion: reduce`. A comet whose last frame
leaves the streak in one spot is a misleading static picture. *Consequence:* GD4's fallback is an
explicit static style per state (plan GV2.4), tested under emulated reduced motion.

**F37 — Exists: the coordinator view joins runs to PRs but holds no PR-to-PR edge.**
`origin/coordinator:internal/coordinator/view.go` (`Join`, `Record`, `Collision`). *Consequence:*
until the coordinator record carries edges, a PR-level node has edges only through GD5's plan-task
join into a plan artifact.

**F38 — Gap: the coordinator's console view and this screen edit the same console files.**
Coordinator P6 edits `console/src/main/ipc.ts`, `console/src/main/spawn.ts`,
`console/src/preload/index.ts`, `console/src/renderer/entry.ts`, `console/src/source/cli.ts`, and
names `console/src/main/protocol.ts` and `console/test/unit/window.test.ts` (its CSP pin) on the
`coordinator` branch; plan GV2.1, GV2.2 and GV2.3 edit the same files on `main`. *Consequence:* a merge-time conflict, not a
design conflict; noted for whoever syncs `main` into `coordinator`.

## 3. Decisions (answered by the owner, 2026-10-22)

| # | Question | Recommendation (and why) | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **GD1** | Source of truth for tasks, releases and edges | Per-plan `.graph.json` files plus one roadmap file for releases, assignment and cross-plan edges: works now, no new infrastructure | Wait for the coordinator's project record (0.11.0): no task-level whole-project graph until it carries tasks | **2026-10-22: wait for the coordinator's project record (0.11.0) — not the recommendation.** *Why:* one source of project state, the coordinator's, rather than a file that would later be migrated. *Consequence:* PR/run-level first; task-level only when the record carries tasks, releases and cross-plan edges; no roadmap file now; D29's "project record" now means the coordinator's (F31). |
| **GD2** | Does D30 (no drag write) stand? | Yes: drag becomes "copy the command" (D30's shape) | Let the console write: a new write path and its guard | **2026-10-22: let the console write the roadmap — not the recommendation; reverses D30 and amends D22 (F35).** *Why:* moving work between releases is the view's purpose. *Consequence:* the target, the write path and its security model are open (§4 (a), plan GV3). |
| **GD3** | Layout engine | Layered layout in Go or hand-written TypeScript: keeps D9 and the CSP | elkjs: D9 reversal, licence, worker file, bundle size | **2026-10-22: bring elkjs in — not the recommendation; reverses D9.** *Why:* nested release boxes are what ELK is for. *Consequence:* licence confirmed 2026-10-22 under EPL-2.0, pin 0.11.0 (§4 (c)); a worker file under the CSP, served verbatim (1,613,729 bytes, no eval; F13). |
| **GD4** | Motion | Allow comet/pulse/flow with a text mark on every coloured state and a `prefers-reduced-motion` static fallback | Keep §3.9: static states only | **2026-10-22: the recommendation.** *Why:* the motion carries meaning on this one screen, and the marks keep it accessible. Amends console-styling §3.9 for this screen only. |
| **GD5** | Join key node → run | `fugaro run` records an optional plan task id in `launch.json` (additive, skew-safe) | Match on title: not unique, free text | **2026-10-22: the recommendation.** *Why:* the only exact join that needs no commit per launch. Stored in `task.json` too (F32). |
| **GD6** | Scope | Open work by default, a toggle for everything, merged/cancelled collapse into a per-release counter | All ~800 tasks always | **2026-10-22: the recommendation.** *Why:* the useful view is what is still moving. |
| **GD7** | Stream names and tag prefixes | One data file in the repository (stream, next name, current version, tag prefix), read by the graph and the release scripts; proposed path `docs/releases/streams.yaml` | Names hard-coded in two places | **2026-10-22: the recommendation.** *Why:* one list, two readers. Names: CLI animals, no birds (Badger 0.10, Camel 0.11, Dolphin, Gecko, Hippo, Koala, Moose); Console fruits (Cherry, Mango, Melon; avoid Apple, Lemon); Mobile birds (Falcon, Pelican, Robin). Console never below 0.110.0 (amended from 0.10.0 by the renumbering ruling of 2026-10-24; F33). ***Path amended after review:*** `docs/release-streams.yaml`, because `docs/releases/` breaks the website build (F29); **accepted by the owner 2026-10-22** (§4 (e)). |
| **GD8** | Numbering | Own prefix `GD` in this folder | Take a block of the global `D` series | **2026-10-22: the recommendation.** *Why:* the folder's numbers cannot collide with console v2's D21–D38 or anyone's block. |

## 4. Tensions and open items, with the owner's rulings

**All six were ruled by the owner on 2026-10-22, each as recommended.** The choices are kept as
they were offered, so each ruling can be read against what it chose between. Nothing is open.

**(a) GD1 against GD2: the console writes a record that does not exist yet.** GD1 waits for the
coordinator's record (0.11.0); GD2 has the console write the roadmap; and the coordinator's own
design makes that record read-only to people and written only behind its front door (F30).
Choices:
1. **Read-only until 0.11.0.** The first slice is the read-only graph from `fugaro plan status
   --json` (per-plan artifacts joined to runs, plan GV1.6) plus the live feed; drag is disabled with
   a note naming the release that brings it. Holds back: any move until the coordinator's record and
   a write route into it exist.
2. **An interim write into the plan artifact** (`Node.Version` in the `.graph.json`), through a CLI
   command the console spawns. Holds back nothing in time, but writes into the operator's working
   tree — the exact risk D30 named — and creates a second source GD1 chose against.
3. **A coordinator write route** — the front door (coordinator P8, not started) or a new
   people-write grant on a `coordinator/roadmap/` node (a rules change outside the coordinator
   plan). Holds back: GD2 until that route is designed and reviewed, and it changes an approved
   plan's security model.

Whichever is chosen, two things are recommended and **not decided**: the console shows a diff
preview (task, from-release, to-release, every edge that becomes backwards) and asks for
confirmation before it writes; and every write has an undo that is the inverse write, also
previewed. A mistaken drop must be one click from reversed.

**Ruled 2026-10-22 (the owner, the recommendation):** option 1, read-only until 0.11.0; drag comes with the coordinator's record,
and GV3.1/GV3.2 stay blocked until it exists. *Why:* no interim write into the working tree and no
second source beside the record GD1 chose. The preview and undo recommendation stays with GV3.2.

**(b) GD2 and GD3 amend answered console v2 decisions.** For the owner's confirmation, every
console v2 item they change:
- **GD2 → D30** (`desktop-console-v2.md` D30 row and F23, "a drag is a write"; plan V2.5.3,
  superseded) and **D22** (D22 row and the "one write" sentences of the design's decisions
  paragraph, and the v2 plan's OWNER APPROVAL GATE and Phase V2.5 heading; the roadmap move
  becomes a second write). V2.6.2's "pull it
  forward" half, which was V2.5.3's shape, follows the new write instead.
- **GD3 → D9** (`desktop-console.md` D9 row; `console/README.md`'s "the renderer bundles no
  framework runtime and no charting library (design D9)"; `console/test/unit/pinning.test.ts`'s
  empty-`dependencies` assertion; `console/test/unit/bundle.test.ts`'s bare-specifier refusal;
  `console/src/main/protocol.ts` `SERVED_FILES`).
- **GD4 → console-styling §3.9** (this screen only).
- **GD1 → D29**'s later half (F31).
Plan GV0.1 writes the dated amendment notes.

**Ruled 2026-10-22 (the owner, the recommendation):** **confirmed** — the amendments to D22, D30, D9, D29 and console-styling §3.9,
as listed. *Why:* they follow from GD1–GD4, which the owner had already answered.

**(c) The elkjs licence.** `elkjs@0.12.0` is `EPL-2.0 OR GPL-3.0-or-later`; `0.11.0` and earlier
are `EPL-2.0`; the repository is MIT. Shipping an unmodified EPL-2.0 library inside an
MIT-licensed app is commonly done, with a notice and a pointer to the library's source; the exact
obligations for a bundled copy are UNVERIFIED and are the owner's to confirm. Choices: confirm
EPL-2.0 (pin 0.12.0 under its EPL-2.0 option, ship a third-party notices file); or decline, and
GV2.2 is replaced by F15's layout without ELK.

**Ruled 2026-10-22 (the owner, the recommendation):** **confirmed under EPL-2.0; pin `elkjs` 0.11.0** (EPL-2.0 only), ship its licence
text, and ship the worker file unmodified. *Why:* a single-licence version and an unmodified file
keep the obligation to the notice and the source pointer. GV2.2 is no longer gated on the licence.

**(d) Task-level across ~800 tasks against GD1's delay.** The spec asks for the whole task graph;
GD1 delays task-level data to the coordinator's record, which today holds PR-level records only
(F21, F37). Choices: per-plan task graphs now (GV1.6, no cross-plan edges, only plans that have an
artifact) and the whole graph at 0.11.0+; or nothing task-level until 0.11.0. The first holds back
cross-plan and cross-stream edges; the second holds back the view itself. GD6's default (open work
only) keeps either within the 1 MiB-per-artifact limit.

**Ruled 2026-10-22 (the owner, the recommendation):** option 1 — per-plan task graphs now, built on the existing `plan graph --json`
document; release assignment later, from the coordinator's record. *Why:* the view is useful now
for the plans that have an artifact, and nothing is written that the record would later migrate.
*Read as:* the existing `version` and GV1.3's per-plan `stream` are display inputs only; the
authoritative assignment of tasks to releases arrives with the record (GV4.1). Slices 1–2 no longer
wait on a ruling.

**(e) GD7's path.** Confirm `docs/release-streams.yaml` (F29), or keep `docs/releases/streams.yaml`
and have `website/src/lib/changelog.mjs` skip it by name (a website change the plan would add).

**Ruled 2026-10-22 (the owner, the recommendation):** `docs/release-streams.yaml` accepted. *Why:* no website change for a data file.

**(f) The coordinator plan.** Coordinator P4's intent record carries a target release but no plan
task; recording GD5's plan-task id in it too would let the 0.11.0 record join tasks to PRs. That is
a change to an approved plan, proposed here and not made.

**Ruled 2026-10-22 (the owner, the recommendation):** proposed for coordinator P4 only; the coordinator's documents are not changed by
this work. *Why:* the coordinator plan is approved and owned elsewhere.

## 5. The smallest useful first slice

Read-only, one stream at a time, no drag: the skill writes artifacts (GV1.7), levels and the
critical path in Go (GV1.1), the stream field and the streams file (GV1.3, GV1.4), the plan-task
join (GV1.2) and `fugaro plan status --json` (GV1.6), the console operation (GV2.1), elkjs in a
worker (GV2.2, elkjs 0.11.0), and the static screen (GV2.3). Motion (GV2.4) follows; the write
(GV3) and the cross-plan graph (GV4) wait on the coordinator's record (rulings (a) and (d)).

## 6. Security probes for the plan's reviewers

- **The new write path** (GV3): who can trigger it (a renderer click → main process → spawn); what
  the renderer may send (a node id and a release id from documents the CLI printed, never a path,
  never a command); what validates them in the main process (exact patterns, membership in the
  last document read); what a hostile plan file or job-written title can make the confirmation
  show; what a replayed or stale drop does (compare-and-set on the record, refusal on a moved
  version). Titles in the confirmation come from repository-written artifacts: sanitized and
  length-capped, so a title such as "→ Camel" cannot forge the from/to line. Under (a)'s
  option 2 the write goes into the working tree, so the CLI refuses when the artifact file has
  uncommitted changes (D30's "clobber uncommitted work").
- **The worker and the CSP** (GV2.2): `script-src 'self'` stays; no `'unsafe-eval'`, no
  `'wasm-unsafe-eval'`, no `worker-src` widening; the worker file is an exact `SERVED_FILES`
  entry; a probe that `new Function` inside the worker is refused by the CSP; the worker receives
  only the graph's structure (ids, sizes), never text; the renderer validates every reply (known
  ids, finite numbers) before a coordinate reaches an SVG attribute.
- **Path confinement** (GV2.1, GV1.6): the renderer sends no path; the main process passes the
  installation's own checkout; the CLI discovers artifacts only under `docs/` of that checkout and
  refuses a symlink that leaves it, and a symlinked `docs/` itself; the same rule and a size cap
  before decoding apply to `docs/release-streams.yaml` (repository-written YAML, possibly with
  anchors and aliases, from a contributor's branch); a plan id from the renderer is matched against
  the CLI's own listing, never joined into a path.
- **The join hint** (GV1.2): `plan_task` is job-writable (F32): shape-validated, length-capped,
  sanitized, never an authorization input. The real abuse is state spoofing: a prompt-injected run
  can claim any task and paint it running or done. So the join is scoped to the plan's own
  repository, and a run's claim never changes `ready`/`blocked` for another repository's plan.

## UNVERIFIED

- **U1** *Resolved in review round 1:* `lib/elk-worker.min.js` has no `eval(` or `new Function(`
  (F13). GV2.2's e2e still probes it in the built app.
- **U2** *Resolved in review round 1:* the served worker file is 1,613,729 bytes in the pinned 0.11.0 (F13).
- **U3** *Resolved in review round 1:* yes — `record_pr` in `runs ls --json` (F9).
- **U4** Task and "Depends on" counts are grep approximations.
- **U5** Whether a Web Worker loads from the `app://` scheme under the console's fuses and
  sandbox in the packed, signed app (Check 123).
- **U6** *Settled by the owner's ruling (c), 2026-10-22:* EPL-2.0 accepted; ship the licence text, file unmodified.
- **U7** Mobile's first store version number: `mobile/app.json` says `0.0.0`; what the test-track
  builds carry was not read.
