# The console, v2: the later phases

*Status: 2026-10-15/16. Design: [desktop-console-v2.md](../design/desktop-console-v2.md) (the reconciliation, decisions D21–D38 — **the owner answered all eighteen on 2026-10-15/16: D22, one write, launch, through `fugaro run`, and no command queue (which also answers D21), and D34, not yet — cancel and retry stay unscheduled, both on 2026-10-16; the other fifteen as the design recommended**). The plan this one follows: [2026-10-15-desktop-console.md](2026-10-15-desktop-console.md) (phases 0–5, phase 0 merged). Spec source: [desktop-console-spec-v2.md](../design/desktop-console-spec-v2.md).*

**Spec:** [desktop-console-spec-v2.md](../design/desktop-console-spec-v2.md) (the owner's text, verbatim), reconciled by [desktop-console-v2.md](../design/desktop-console-v2.md), whose decisions **D21–D38** each gate a phase below. **Related:** the v1 plan [2026-10-15-desktop-console.md](2026-10-15-desktop-console.md), unchanged; the v1 design [desktop-console.md](../design/desktop-console.md), decisions D1–D20, unchanged; [2026-10-14-project-centric.md](2026-10-14-project-centric.md), the release train and the project record.

## OWNER APPROVAL GATE

**Nothing in v2 is approved or built, and no task in this plan may be started before the owner says so.**

- **The design's decisions D21–D38 are all answered, and an answer is not an approval.** **On 2026-10-16 the owner answered two of them.** **D22 — one write, launch, through `fugaro run`, behind the CLI's budget pre-check and the app's own confirmation, with no command queue — and D21 by that answer's own words ("with no command queue").** **D34 — not yet: launch only in the first slice; cancel and retry stay unscheduled, to be revisited once launch has run for a while** (each write widens the allow-list, and `fugaro cancel` has no confirmation of its own). **The other fifteen — D23–D33 and D35–D38 — are answered as the design recommended, on 2026-10-15/16.** This plan is written so that a task's **first line names the decision it needs**, and every one of those decisions now carries an answer, so no task here is waiting on a question. **An answered decision is not an approved plan:** D22's answer says what V2.5.1 must do, D34's says what the first slice must *not* build, and neither lifts this gate — **no task here, V2.5.1 included, may be started before the owner says so.**
- **The v1 console plan's gate still stands and is unchanged.** Phases 0–5 of [2026-10-15-desktop-console.md](2026-10-15-desktop-console.md) are approved as they are, and nothing here reopens, reorders or re-scopes them.
- **This plan is a proposal for later phases.** It exists so the owner can see what the second spec would cost, in tasks, before deciding. The one thing that is genuinely cheap and independent — the planning skill (phase V2.8) — is cheap *because* it is independent, not because it is approved.
- **Two of phase V2.5's three tasks move the fence, and the third writes nothing.** D2 (strictly read-only v1) and D14's accepted recommendation (read-only enforced by an exact-argument allow-list alone) are the console's whole safety argument. V2.5.1 (launching) — whose shape **the owner answered on 2026-10-16, as the one write the first slice gets** — and V2.5.2 (cancel and retry), **which the owner answered on 2026-10-16 with "not yet" — so it is not in the first slice and is unscheduled until launch has run for a while** — each put a world-changing command in the spawn allow-list, which is the fence moving. V2.5.3 is the other case: it shows the operator a command, the operator runs it, and the app holds no path into the checkout (D30, **answered 2026-10-15/16: a read-only roadmap with a "copy the change" command, and no drag-and-drop write**) — it is security-sensitive for what it must **not** do, not for a write it makes. Each of the three is a separate answer and a separate PR, and none of them may be bundled with a read.
- **No agent-run step in this plan touches a secret, and none of them is a live action.** The sandbox installation is the only installation any of this runs against; **the `belong` installation is never touched** (`belong` holds `edgeappinc/edgeserver` and `edgeappinc/edgeweb`: no agent touches it, ever). Nothing here produces, stores, reads or transmits a credential.
- **No file under `.github/workflows/` is added or edited by this plan.** If a phase ever needs one, it is staged under `docs/pending-workflows/` and applied by the owner, which is the rule the v1 console plan's phase 4 already follows for the signing workflow.

## 1. What this plan is

The second spec asks for six new things (design §1's findings F1–F33). Four of them need **reads that do not exist yet**, one is a **write path**, and one is a **skill upgrade**. This plan orders the work so that:

1. **the reads come first** (phase V2.1), because a view built on an absent field is the one mistake this kind of work reliably makes;
2. **the graph comes before anything that draws it** (V2.2 before V2.3, V2.6), because "ready", "blocked" and "scheduled earlier than its dependency" are all functions of the graph and of nothing else;
3. **the writes come last and separately** (V2.5), because V2.5.1 and V2.5.2 each move the fence D2 and D14 put up, and because by then the reads they act on are shipped and testable — and **of the two, only V2.5.1 is in the first slice: the owner answered D34 "not yet" on 2026-10-16**, so the cancel-and-retry spawns are unscheduled and unapproved until launch has run for a while. The phase's other task, V2.5.3, is answered too — **D30, on 2026-10-15/16: a read-only roadmap with a "copy the change" command and no drag-and-drop write** — and lands last for the same reason, but it is read-only — it prints a command and the operator runs it (D30);
4. **the skill is independent** (V2.8) and can land at any point, including before the console reads anything — its output is a file a person can read too.

**Releases.** The console is its own artifact published from the CLI's own `vX.Y.Z` tag (design D11). **v2 adds no release and is not scheduled against one** (design D37): its first shippable version is whatever tag follows the release that carries the v1 console's phase 4. The release train this repository records is **R4 of [2026-10-14-project-centric.md](2026-10-14-project-centric.md)**: project-centric phase 0 on `main`, phases 1–6 on `next`, released together as **0.8.0**. The highest release in `docs/releases/` is `v0.7.5.md`.

**Global constraints.** The v1 console plan's Global Constraints apply to every task here, verbatim and without restatement: no live action, no secret handled by an agent, the sandbox installation only, the `belong` installation never, no `.github/workflows/` edits, and the docs tests. Two more are v2's own:

11. **A read may not become a write because it is convenient.** Every task below says which of the two it is; the only writes in this plan are V2.5.1 and V2.5.2 — and **the first slice contains only V2.5.1** (D34, answered "not yet" on 2026-10-16), so the launch is the single write an approved first slice would carry.
12. **Every cited symbol was checked against `origin/main`.** Where a task says "the field `x`", the field `x` exists at the commit the design cites; a task that finds it missing stops and says so rather than inventing it.

## 2. The check numbers, allocated

The live checks live in [docs/gcp-live-checklist.md](../gcp-live-checklist.md), and a plan that writes one has to say which number is whose — a fact about the owner's allocation and about `origin/main`, not about this plan's wishes. **The allocation, stated once for every document in this line of work:**

| Number | Whose it is |
|---|---|
| **47** | the highest `## Check` section written in the file (read from `origin/main` at `a573870`); 1–47 are written — **stale, and superseded by the re-read below: `origin/main` at `85758e1` carries sections through `69b`** |
| **48–56** | the project-centric plan's ([2026-10-14-project-centric.md](2026-10-14-project-centric.md)'s phase table; 56 is that plan's `fugaro project export`) |
| **57** | written as a summary row; its section is elsewhere |
| **58** | **artifact runs**: [artifact-runs.md](../design/artifact-runs.md) §11 ("Check 58, in outline") and its plan — **merged as `fd06635`, PR #393, 2026-10-15 14:08**, five hours before the `a573870` this table reads. The reservation is **on `main`**, in a design and a plan; only the checklist section is unwritten (that design says the check is not part of its own pull request) |
| **59**, written as **59a** and **59b** | **the console, v1**: [2026-10-15-desktop-console.md](2026-10-15-desktop-console.md)'s, also a reservation in a merged plan rather than a section in the checklist |
| **60** | **the budget-token channel** |
| **61** | **the monorepo folder export** |
| **62** | **this plan's** — the one owner-run check in §4 |
| **63** | **this plan's second slot, and it is held rather than spent**: a check that grows a second half is written as **62a** and **62b** in the 59a/59b shape, and 63 is for work in this line that turns out to need a number of its own rather than for a second heading on the same check |
| **64–66** | **voice** (#428) |
| **67 onward** | **managed projects** (#429) |

The blocks are handed out a line of work at a time — this one 62 and 63, voice 64–66, managed projects 67 onward — and a block is held for the work it was given even if only part of it is ever used: **an unused number is not a free number.**

**The rule that produces this table is the checklist's own, and it is stated there in as many words** (`docs/gcp-live-checklist.md`, "Numbering ruling (owner's assistant, 2026-10-13)"): **a check number, once a plan reserves it, is never reused or renumbered, even when its section lands out of order** — later work takes the next free block. So a number is claimed by an **allocation, made before the work is written**, and never by the order two pull requests happen to merge. **Two sentences this tree carries state the other rule, and are superseded:** the checklist's own register states it in one paragraph ("whichever of two open claims merges **second** renumbers **itself**, to the number free at its own merge"), and the v1 console plan's "Open items" list still quotes it (its item 8, "Check 59's number is contended, and now carries two sections": "whichever of the two merges second renumbers its own section, summary row and references together" — [2026-10-15-desktop-console.md](2026-10-15-desktop-console.md)). Under the rule this plan applies, that sentence is wrong rather than merely old: it would make 60 and 61 movable and this plan's own check a coincidence of timing. **The decision numbers are allocated the same way** — this work keeps **D21–D38**, voice keeps **D39–D54**, managed projects takes **D55 onward** (the design's §2 states it) — and a decision number, once allocated, is never reused or renumbered either.

**The brief that carried the second spec said "the budget-token channel 60, the monorepo folder export 61".** Both numbers are now theirs by the owner's allocation. When this plan was written, **neither was a number any document in this tree carried**: no `Check 60` and no `Check 61` appear anywhere under `docs/`, the token channel is **phase 3b** of the project-centric plan (a design task with no check number), and the nearest thing to a folder export is that plan's phase 6 `fugaro project export`, whose number is **56**. That is why an earlier revision of this section reserved **60** for this plan and said it would move to **62** "if the owner holds 60 or 61" — and the owner does hold them, which is why **this plan's check is 62** and why the moving is done here, once, rather than left to whichever pull request merges second. **Nothing in this document reserves 60 or 61, and no task may write them.**

**Before Check 62 is written, its task re-reads `origin/main`** with the three commands the v1 plan's register names (`git show origin/main:docs/gcp-live-checklist.md | grep -n '^## Check 4[5-9]'`, a grep of the summary rows, and a grep of `docs/plans/` and `docs/design/` for the number), because this table is a claim about `main`, and a reservation lives in a merged design or plan rather than as a `## Check` section more often than not — 58 and 59 are both that kind, so a grep of the checklist alone finds neither. **The re-read is a record, not a renegotiation**: 60 and 61 having no section yet does not free them, and 62 does not move. (An earlier revision of this table said 58 was reserved by an **open, unmerged** PR #393 and treated `artifact-runs.md` as a second claimant of the same number. Both halves were wrong: #393 **is** `artifact-runs.md`, and it merged as `fd06635` at 14:08 on 2026-10-15, before the `a573870` this table reads. The correction is recorded rather than quietly applied.)

**The re-read, done — `origin/main` at `85758e1`, 2026-10-18 (V2.0.2).** The three commands were run again and this is their result. **The table above is a claim about `main` at `a573870`**, so what has moved is recorded beside it rather than written into it. **What `main` carries now: 34 `## Check` sections — 19, 20–38, 40–47, 49, 50, 52, 58, 69a and 69b — and a summary table whose highest row is `69b`.** The row above that says 47 is the highest section and "1–47 are written" is therefore **stale in two ways**: 39 has never had a section (its 39a–39f live in the retire-image-mirror plan, as [voice.md](../design/voice.md) §5.3 records), and 49, 50, 52, 58, 69a and 69b have been written since. **39, 48, 51, 53–57 and 59–68 still have no `## Check` section**, and neither does anything from 70 onward. (Two smaller facts of the same file, recorded because a re-read is a record: 24 and 25 carry sections but no summary row, and the sections that exist are not the same set as the rows that exist.) **The reservations are unchanged where it matters:** 48–56 are still the project-centric plan's; 57 is still a summary row whose section is [compute-backend.md](../design/compute-backend.md) §7; 58 is now **written** as a section as well as reserved by the artifact-runs work; 59, as 59a and 59b, is still the v1 console plan's; **60 is still the budget-token channel's**; **61 is still the monorepo folder export's**; 62 and 63 are still this plan's; 64–66 voice's; 67 onward managed projects'. **Two sentences of this section are corrected by the re-read rather than repeated.** The first is the table row for 47, above. The second is the paragraph above that says no `Check 60` and no `Check 61` appear anywhere under `docs/`, which **was already false when this plan was written**: [budget-token-channel.md](../design/budget-token-channel.md) is on `main` (#399, `d39f936`, 2026-10-15) and names `Check 60` nine times, including its section "The check outline: Check 60, owner-run, sandbox only". The sentence that paragraph needs is the narrower one — **no `## Check 60` section is written into the checklist** — and the number is the channel's by the owner's allocation *and* by its own merged design, which is the stronger evidence of the two. That correction is [voice.md](../design/voice.md) §5.3's, made independently and before this re-read; it is recorded here too because the false sentence is this plan's own. **No number moves.** 60 and 61 having no checklist section does not free them, every section written since `a573870` is above this plan's block rather than inside it, and **this plan's check is 62** — which no document has written and which this re-read does not take.

## 3. Phases and tasks

Each task is independently mergeable: it lands with its own tests, and **nothing in phase *n* is required by phase *n-1*'s merge**. Tasks marked **SECURITY-SENSITIVE** get an independent review by a second opus agent before merge, in the v1 plan's sense — the reviewer's question is "what does this let the app do that it could not do before?", not "is this good code?". **Every `needs` line below names an answered decision — D21–D38 are all answered (2026-10-15/16) — or a task that precedes it**, so what a task still waits on is the owner approval gate above, and nothing else.

### Phase V2.0 — the gate, made checkable

**V2.0.1 — the decisions, answered and pinned. (D21–D38 are all answered — D22 and D34 on 2026-10-16, D21 with D22, and the other fifteen as recommended on 2026-10-15/16; what is left is to pin them by a test, which is an agent's task)**
Every decision in [desktop-console-v2.md](../design/desktop-console-v2.md)'s tables now carries the owner's answer — the answers given on 2026-10-15/16 are recorded and not re-asked. Then one task records the answers as a test, in the shape the v1 design's D-table already has: every row D21–D38 carries an answer (D21's in its prose paragraph, since it has no row), and the answer text names the task in this plan that implements it.
*Files:* `docs/design/desktop-console-v2.md`; `internal/cli/docs_desktop_console_v2_test.go` (new).
*Verification:* `GOFLAGS=-p=2 go test ./internal/cli -run 'DocsDesktopConsoleV2' -count=1` — green, and red if a row is deleted or its answer is blanked (the mutation is proven red in the PR that adds it, as this tree's docs tests do).
*Independent merge:* yes — it touches one document and one test.

**V2.0.2 — the check-number re-check. (needs nothing)**
Re-read `origin/main` for `## Check` sections and summary rows and record the result in §2 of this plan before any task writes a number. **This plan's check is 62 and the number does not move** — the owner's allocation in §2 assigns 60 and 61 elsewhere and 62 here, and a number once reserved is never reused or renumbered. The re-read is a record of what `main` now carries; a number below 62 that is still unwritten is not a number this plan may take.
*Files:* `docs/plans/2026-10-15-desktop-console-v2.md`; `internal/cli/docs_desktop_console_v2_test.go`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/cli -run 'DocsDesktopConsoleV2' -count=1`.
*Independent merge:* yes.

### Phase V2.1 — the two reads everything else draws on

**V2.1.1 — the installations, as a document. (needs D31) — SECURITY-SENSITIVE**
Closes v1's gap 5, and the design's F1/F2 say exactly how much is already there. The read is the one D13 itself names — **`fugaro config list --json`**, a subcommand of the command whose subcommands are `example`, `show`, `layer`, `init` and `publish` (`internal/cli/configcmd.go`, `config_show.go`, `config_init.go`, `config_publish.go`) — and it enumerates the installations in the home config (`internal/localcfg/localcfg.go`'s `Projects(getenv)`, `ProjectsDir`, `ProjectPath`, `LoadProject`), printing a document that names **only** the fields the console needs: the project name, the GCP project, the region, whether a shared config was merged, and when the shared copy was last checked (`internal/localcfg/shared.go`'s `SameInstallation`/`MergeShared`, `internal/localcfg/sharedcache.go`'s `SharedCacheEntry`, `SharedFreshFor`, `SharedOfflineFor`).
*Why security-sensitive:* this is the document that decides **what the app is allowed to know about the operator's machine**, and it is one field away from being a credential dump. It must never be a marshalled `localcfg.Config` — that struct carries more than the console needs, and a field added to it later would silently join this document. The fields are named in the document type, and a test asserts that the document type is not `localcfg.Config` and that no field of the config outside the named set reaches the output. The app still reads nothing itself (§3.4 rule 7): the operator picks from what the CLI printed, and D13's tuple and pairing check are unchanged.
**Landed 2026-10-20, with one field more than the five above: `checkouts`.** It is the other half of D13's question — not only which installations this machine has but where the checkout of each one's repositories is — and it arrives in this document rather than through a second command, because a second command would answer the same question twice. One entry per remembered path: the repository identity, the path, whether it is still a checkout of that project and that repository, and a reason code when it is not (`internal/cli/config_list.go`'s `validateCheckoutPath` over `internal/localcfg/checkouts.go`'s state file, re-validated on the read and never written by it). It is local state: never published to the runs bucket, never merged into the shared config, and absent — not empty — in the document an older CLI prints (contract §6.8).

*Files:* `internal/cli/configcmd.go` (the subcommand), `internal/cli/docs_desktop_console_v2_test.go`, `console/docs/json-contract.md`, `docs/design/desktop-console.md` (a **new** sentence beside gap 5's old one, since v1 is merged), `docs/design/desktop-console-v2.md`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/cli -run 'ConfigList|DocsDesktopConsoleV2' -count=1`, plus the boundary test `go test ./internal/cli -run 'ConsoleBoundary' -count=1`.
*Independent merge:* yes; it is one new command and one new document.

**V2.1.2 — the files a run changed, recorded. (needs D27) — SECURITY-SENSITIVE**
The design's F17: no document the console can read carries the changed-file set, and the only place it exists today is transient (`internal/followup/prompt.go`'s `PromptData.DiffStat`, clipped to `maxDiffStatBytes`, filled by `internal/runner/followup.go`'s `sessionGit(ctx, r.repo, "diff", "--stat", …)` and defined in `internal/runner/session.go`). The run records the **path set** in its own record — paths only, no contents, no diff, bounded, and with the truncation recorded as a fact rather than hidden — and a `--json` read exposes it (`internal/cli/runs.go`'s `lsDoc`).
*Why security-sensitive:* a path can name a client, a person or a project, and this is the first time this repository stores one outside a prompt. The task must state the bound, state what is **not** stored (contents, diffs, commit messages), and prove the bound with a test that feeds it an oversized change and asserts the record says it was truncated.
*Files:* `internal/runstore/runstore.go`, `internal/runner/` (the writer), `internal/runview/runview.go` (the row), `internal/cli/runs.go`, `console/docs/json-contract.md`, `docs/design/desktop-console-v2.md`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/runstore ./internal/runner ./internal/cli -run 'ChangedFiles|RunsLS' -count=1`.
*Independent merge:* yes, and it is a **schema change**, so the version-skew handshake the v1 plan builds in phase 3 is what keeps an older console honest about a field it does not know.

### Phase V2.2 — the graph

**V2.2.1 — the graph document, its schema, and the read that prints it. (needs D25)**
A plan-side artifact written beside the plan it describes (design F6/F33 and D25), and a read-only command that prints it — the console reads it the way it reads everything, over the allow-list, and never opens the file itself. The schema is small and it is written down in the design first: nodes (id, title, the run it belongs to if any, the task's target version, the footprint of V2.2.2's shape), edges (dependency, with the reason), the plan it came from, and a schema version so a console can refuse a graph it does not understand.
*Files:* `internal/plan/` (new package: the document type, the parser, the validation), `internal/cli/plan.go` (new), `internal/cli/root.go`, `console/docs/json-contract.md`, `docs/design/desktop-console-v2.md`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/plan ./internal/cli -run 'Plan|DocsDesktopConsoleV2' -count=1` — including a fixture graph, a malformed graph, a graph from a future schema version, and a graph with a cycle (which is a **refusal**, not a render).
*Independent merge:* yes.

**V2.2.2 — the footprint. (needs D28)**
The planner's expected footprint per task, in the shape D28 recommends: glob-shaped paths, advisory, and labelled as a prediction in the document itself. The matching used for display is this repository's own glob semantics — **doublestar**, `github.com/bmatcuk/doublestar/v4`, as `internal/imagecheck` (`doublestar.Match` over the git tree), `internal/cache/key.go` (`doublestar.Glob`), `internal/config/validate.go` and `internal/verify/reports.go` (`doublestar.ValidatePattern`) already use it — not a second implementation. `internal/gitops` has no pattern matching and is not the reference here.
*Files:* `internal/plan/`, `internal/cli/plan.go`, `docs/design/desktop-console-v2.md`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/plan -run 'Footprint' -count=1`.

**V2.2.3 — readiness, and the critical path. (needs D26)**
`ready` and `blocked` computed **where the graph is read** (design F7), so two consoles cannot disagree; and the bottleneck ranking by critical path, with the raw downstream count as the fallback when no footprint carries a weight (D26).
*Files:* `internal/plan/`, `internal/cli/plan.go`, `console/docs/json-contract.md`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/plan -run 'Ready|CriticalPath' -count=1` — table-driven, with a graph whose raw-count hub is *not* the critical-path bottleneck, since that is the case the feature exists for.
*Independent merge:* yes; it depends on V2.2.1's schema and on nothing else.

### Phase V2.3 — the graph on screen

**V2.3.1 — the graph view, and the four colours. (needs V2.2.3; the roadmap is V2.6, and its D30 is answered — a read-only "copy the change" affordance)**
The console renders the graph document: the four colours (done, running, ready, blocked), the bottleneck highlight, and the "what else can we start in parallel?" answer. Every colour is a field the document carries (design F4's rule) and "needs attention" is **not** a colour until it is a definition (F4, U6).
*Files:* `console/` (the screen, its fixture, its test), `console/docs/json-contract.md` if a field is added.
*Verification:* the console's own test command for the view, plus `GOFLAGS=-p=2 go test ./internal/cli -run 'ConsoleBoundary' -count=1` so the boundary still holds.
*Independent merge:* yes.

### Phase V2.4 — overlap

**V2.4.1 — the comparison, in Go. (needs V2.1.2 and D27)**
Two recorded sets (V2.1.2) compared against each other and against the graph's footprints (V2.2.2), with the **warning computed in the CLI** so two consoles cannot disagree about whether two branches overlap. Soft warnings only, never locks (design F21): `internal/lock` is a run's branch lock and is not the console's to take.
*Files:* `internal/plan/` or `internal/runview/` (wherever the two sets meet), `internal/cli/plan.go`, `console/docs/json-contract.md`, `docs/design/desktop-console-v2.md`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/plan ./internal/runview -run 'Overlap' -count=1` — with a disjoint pair, an overlapping pair, a footprint-only prediction, and a truncated record (which must **not** be reported as "no overlap").
*Independent merge:* yes.

### Phase V2.5 — the world-changing tasks (the launch, which D22 answered; cancel and retry, unscheduled by D34; and one read-only affordance, which D30 answered)

**The first slice of this phase is V2.5.1 and nothing else.** **No task in the first slice builds a cancel or retry spawn, and neither command gets an allow-list entry**: the owner answered D34 on 2026-10-16 with "not yet", each write widens the spawn allow-list, and `fugaro cancel` has no confirmation of its own to inherit (F8). V2.5.2's text is kept below, in its own **unscheduled and unapproved** section, so the owner can see what the pair would cost when he revisits the answer; V2.5.3 stays where it is — **D30 is answered (2026-10-15/16: a read-only roadmap with a "copy the change" command, and no drag-and-drop write)**, so it waits on the same owner approval gate as every other task here.

**V2.5.1 — launching, through the CLI. (needs D23 — **answered 2026-10-15/16: the CLI's claim, unchanged**; **D22 is answered — 2026-10-16: one write, the launch, and this is it** — and this task is the launch only) — SECURITY-SENSITIVE**
The console spawns `fugaro run` (with `--task-file` for a task handed over as a file) behind the CLI's own budget pre-check and **the app's own confirmation**, and shows `already-launched` (`internal/cli/run.go`'s `launchResult.status`) as a fact rather than an error. The claim stays the CLI's (`internal/runstore/launch.go`'s `Claim`, `ClaimKey`, `ClaimTTL`). **`fugaro run` does not confirm anything itself** — no `--yes`, no prompt, and stdin is read only for `--task-file -` (design F8) — so the app's dialog is the only confirmation between a click and a launch, and this task must build it: it shows the exact argv it assembled, field by field, and the run id or the task file it is handing over, before the spawn. **D22 is answered, and it is the answer this task is written to (2026-10-16): one write, the launch, through `fugaro run`, behind the CLI's budget pre-check and the app's own confirmation — which the answer names as the only confirmation in the chain — with no command queue.** So the dialog **is** required, and what it shows is this design's recommendation rather than part of the answer: the exact argv assembled field by field, and the run id or the task file being handed over. **This task is the launch and nothing else** — cancel and retry are V2.5.2, **which the owner answered on 2026-10-16 with "not yet"**, so this task must not build, spawn, prompt for or allow-list either command. The mechanical guards (the budget pre-check, the claim, the command's own refusals) stay in the CLI either way.
*Why security-sensitive:* **this is the decision that moves the fence.** The v1 design's §5.1 **rule 6** ("The spawn allow-list is exact argv, not a command name") and D14's recommendation — *"Treat every identity as a possible admin, and give the read-only guarantee to the allow-list alone."*, which the owner **accepted as recommended** — were written for a read-only app; a launch is the first entry in the allow-list that changes the world. The reviewer's questions are: what is the new argument vector, exactly; can any field of any document reach it; what does the app's own confirmation show, given that the CLI shows none; and what does the app do when the CLI refuses.
*Files:* `console/` (the spawn, the app's confirmation and what it renders, the test), `docs/design/desktop-console.md` (a **new** decision row recording the fence's move, and beside it the fact design F8 records — that D2's "typed confirmation" is true of the budget writes and not of `fugaro cancel`, so the app's own confirmation is the only one in a spawn's chain; v1 is merged and is not edited), `docs/design/desktop-console-v2.md`.
*Verification:* the console's own test command, plus a test that the argument vector is built from named fields and never from a document's free text, plus a test that the confirmation the app renders shows that vector and the run id and is composed from named fields too (the app's dialog is the only confirmation in the chain, so it is part of the surface under test), plus `GOFLAGS=-p=2 go test ./internal/cli -run 'ConsoleBoundary' -count=1`.
*Independent merge:* yes, and it is the only task in the phase that may land before the others.

**V2.5.3 — the roadmap's "copy the change". (needs D30 — **answered 2026-10-15/16: a read-only roadmap with a "copy the change" command and no drag-and-drop write**) — SECURITY-SENSITIVE**
The console shows the exact command that would change a task's target version; the operator runs it in their own terminal; the guard on that command is the CLI's, and since the command does not exist yet the guard has to be specified with it rather than assumed (design F8: the CLI's existing writes carry a **confirmation — typed, or `--yes`** — and `fugaro run` and `fugaro cancel` carry none at all). The console does **not** edit the plan file in the operator's checkout.
*Why security-sensitive:* **this task does not move the fence** — it writes nothing and spawns nothing; only V2.5.1 does. It is marked because of what it must not do: the string it prints is built from document-derived data (the task's target version) and ends up pasted into a shell, so it has to be a command the operator can read and the app must hold **no path into the checkout** — the test it ships is the one that proves that, and the alternative D30 rejects (the console editing the plan file) is a write into the operator's working tree.
*Files:* `console/`, `docs/design/desktop-console-v2.md`.
*Verification:* the console's own test command, plus a test that the app holds no path into the checkout.
*Independent merge:* yes.

### Unscheduled and unapproved: the cancel-and-retry spawns (V2.5.2)

**Nothing here is in the first slice, and this section is not a task list to pick up.** The owner answered D34 on **2026-10-16** with **"not yet"**: the first slice launches and nothing else, cancel and retry stay unscheduled, and the answer is to be **revisited once launch has run for a while** — each write widens the spawn allow-list, and `fugaro cancel` has no confirmation of its own to inherit. So the task below **keeps its number, its gate and its `SECURITY-SENSITIVE` mark, and has no place in any first-slice group**; it moves into the plan only when the owner revisits D34, and nothing in V2.5.1 may depend on it.

**V2.5.2 — cancel and retry. (D34 answered "not yet" on 2026-10-16: not in the first slice; to be revisited once launch has run for a while) — SECURITY-SENSITIVE**
Spawns of `fugaro cancel RUN` and `fugaro run --retry RUN`, with **the app's own confirmation** — neither command prompts (design F8: `fugaro cancel` has no `--yes` and reads nothing from stdin, and `--retry` is `fugaro run`) — the exact argv shown to the operator, and the opt-in spawn log (v1 design D20) as the audit. What the dialog shows (the run id, and for a cancel the marker it is about to write through the CLI) is D34's to settle when the owner revisits it. No console-side cancel marker is ever written.
*Files:* `console/`, `docs/design/desktop-console-v2.md`.
*Verification:* the console's own test command, plus a negative test that no code path writes to the runs bucket, plus a test that the confirmation shows the run id and the argv and refuses to spawn anything when it is declined.
*Independent merge:* yes — if it is ever picked up.

### Phase V2.6 — the version roadmap

**V2.6.1 — the version column. (needs D29)**
The version lives in the graph document beside the task today (D29's recommendation), and moves into the project record when the project-centric plan's phase 6 (`fugaro project export`, Check 56) lands — with the console reading it through the CLI in both cases, so the view does not change when the source does.
*Files:* `internal/plan/`, `console/`, `docs/design/desktop-console-v2.md`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/plan -run 'Version' -count=1`, plus the console's own test command.

**V2.6.2 — the dependency-aware warning. (needs V2.2.1 and V2.6.1)**
"Flags a task scheduled earlier than something it depends on, and offers to pull the dependency forward" — a pure function of the graph and the version column (design F24), computed where the graph is read. The "pull it forward" half changes the operator's plan and is therefore V2.5.3's shape — the console prints the exact command and the operator runs it — not a new mechanism.
*Files:* `internal/plan/`, `console/`, `console/docs/json-contract.md`.
*Verification:* `GOFLAGS=-p=2 go test ./internal/plan -run 'Roadmap' -count=1`.

### Phase V2.7 — the rest of the spec

**V2.7.1 — the review inbox. (needs nothing beyond v1)**
Finished jobs with their PR numbers and URLs **as text**, never as a link, until a document carries the repository's provider (v1 design §5.1 rule 2 and its gap 7). The row already exists in the feed (`internal/watch/json.go`'s `JSONFinished`, `internal/runview/runview.go`'s `Row`).
*Files:* `console/`, `docs/design/desktop-console-v2.md`.
*Verification:* the console's own test command, plus a test that no `pr_url` reaches `openExternal`.

**V2.7.2 — cap alerts. (needs D35) — SECURITY-SENSITIVE (the threshold is a policy)**
A read-side computation over figures the CLI already prints (`internal/cli/budget.go`'s `budgetShowDoc`; `internal/budget/model.go`'s `PathCapsRepo`, `PathSpendGlobal`, `PathSpendRepo`, `PathSpendRun`), with the threshold a **local setting of the app** and the existing rate alert (`internal/localcfg/localcfg.go`'s `BurnAlert()` into `internal/cli/watch.go`'s `watch.Config`) left exactly as it is.
*Why security-sensitive:* an alert is a claim about money. It must say which installation it is about and how fresh the figure is (design D32), and it must never sum across installations.
*Files:* `console/`, `docs/design/desktop-console-v2.md`.
*Verification:* the console's own test command, plus a fixture with two installations whose figures must appear as two rows and never as one total.

**V2.7.3 — desktop notifications. (needs D33) — SECURITY-SENSITIVE**
Derived from the watch feed by the console's own long-lived reader, **off by default**, content limited to facts a document carries, and never a string from a hostile field (design F27, and the `pr_url` rule again). A notification renders outside the window and outside the CSP, which is why this is a security task and not a UI one.
*Files:* `console/`, `docs/design/desktop-console-v2.md`.
*Verification:* the console's own test command, plus a test that a hostile `pr_url`, a hostile repository name and a hostile branch name cannot reach a notification body.

### Phase V2.8 — the independence-maximizing planning skill

**V2.8.1 — upgrade `parallelism`, and give it the format. (needs D36)**
`plugin/skills/parallelism/SKILL.md` is the skill that owns fan-out (design F31), and at 47 lines and 3245 bytes it has room: the lint budgets in `plugin/skills_lint_test.go` are `maxSkillLines` 400 / `maxSkillBytes` 24576 for a `SKILL.md` and `maxReferenceLines` 300 / `maxReferenceBytes` 16384 for a reference file, with frontmatter rules (`name`, a `description` containing "use when", 40–1024 characters). The task adds the **output format** for the graph and the per-task footprint (V2.2.1's schema, V2.2.2's shape) to the skill, in the skill's own words, and adds one reference file if the SKILL.md budget gets tight.
*Files:* `plugin/skills/parallelism/SKILL.md`, `plugin/skills/parallelism/reference/` (new file, only if needed), `docs/design/desktop-console-v2.md`.
*Verification:* the **skill lint**, which lives in its own package — `GOFLAGS=-p=2 go test ./plugin -run 'PluginSkillsLintClean|LintSizeBudget|LintByteBudget|LintChecksReferenceFiles' -count=1` (`plugin/skills_lint_test.go`, package `plugin_test`) — plus the docs tests that read `docs/design/*.md`, `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1`. The skill lint is what decides new-file-versus-inline, so the test is the verification and not a formality; a `-run` pattern aimed at `./internal/cli` cannot reach it.
*Independent merge:* yes, and it is the **cheapest useful task in this plan**: its output is a file a person can read, so it pays for itself before any console reads it.

## 4. The owner-run check: Check 62

**Check 62 (OWNER-RUN, NOT RUN)**, in [docs/gcp-live-checklist.md](../gcp-live-checklist.md), on the **sandbox installation** (`fugaro`) and the sandbox repository only — **never the `belong` installation**. Written with FACT lines, a restore step, and **Check 43's full guard verbatim**: the three hard stops, `--project fugaro` on every `fugaro` command and `--project "${P:?}"` on every `gcloud` command, in a block re-run from a fresh shell before every step. That requirement is not optional — the future `TestChecklistChecksAreGuarded` (project-centric plan Task 6.3) asserts that every `## Check 48`-onward section carries Check 43's three hard stops and its project pinning, not merely a `:?` guard.

**In outline**, and only for the phases that are built by then: with a plan whose graph has a ready task, a running task and a blocked task, and with two runs whose recorded path sets overlap —

1. the installations document lists the sandbox installation and **does not** contain any field outside the named set (the negative half is the point);
2. the graph reads back with the same nodes, edges, versions and footprints the plan file has, and a cycle in a scratch copy is refused rather than rendered;
3. the overlap warning appears for the two runs that share a path and **not** for the two that do not, and a truncated record reports "unknown", not "no overlap";
4. **if** V2.5.1 has landed: one launch, from the console — first **declined** at the app's own confirmation, which must leave no run behind, since that dialog is the only confirmation in the chain (`fugaro run` does not prompt; design F8) — and then made, with the argv it built read back from the spawn log (`FUGARO_CONSOLE_SPAWN_LOG`, v1 design D20), the run appearing in `fugaro runs ls --json`, and the same task launched twice answering `already-launched` rather than starting a second container;
5. the restore step removes every object and every local file the check created, and the check deletes the spawn log it asked for.

**This check cannot be written until the tasks it exercises are approved and built** — which is what the owner approval gate at the top of this document means. Its **number is 62** by the owner's allocation (§2), settled before the work rather than at its merge, and it does not move: 60 and 61 are the budget-token channel's and the monorepo folder export's, and a number once reserved is never reused or renumbered. If this check grows a second half by then, the two halves are written as **62a** and **62b** in the 59a/59b shape rather than as a new number.

## 5. PR groups

The v1 plan groups its tasks so that each group is one reviewable pull request. The same shape here, in the order they may land:

| Group | Tasks | One PR contains |
|---|---|---|
| **V-A** | V2.0.1, V2.0.2 | the answers to D21–D38, and the test that keeps them answered |
| **V-B** | V2.1.1 | the installations document, and v1 gap 5's correction |
| **V-C** | V2.1.2 | the changed-file record and the read that exposes it |
| **V-D** | V2.2.1, V2.2.2, V2.2.3 | the graph package, its schema, its footprint, its ranking and its read |
| **V-E** | V2.3.1, V2.4.1 | the graph view and the overlap comparison |
| **V-F** | V2.6.1, V2.6.2 | the version column and the dependency-aware warning |
| **V-G** | V2.7.1, V2.7.2, V2.7.3 | the review inbox, cap alerts and notifications |
| **V-H** | V2.8.1 | the planning skill |
| **V-I** | V2.5.1, V2.5.3 — and V2.5.2 **only if and when the owner revisits D34** | **one PR each** — one write (the launch, whose **D22 is answered**) and one read-only affordance (whose **D30 is answered too: a read-only roadmap with a "copy the change" command, no drag-and-drop write**); **V2.5.2 is out of the group and unscheduled: D34 is answered "not yet"** (its text sits in §3's "Unscheduled and unapproved" section). **V2.5.1 and V2.5.3 are the two the owner may schedule** — V2.5.3 writes nothing — and only after he lifts the gate |

**V-H may land first and V-I last**, and neither ordering breaks anything: the skill's output is a file a person reads, and the two writes act on reads that are already shipped by then.

## 6. Cross-task dependencies

Stated so a reviewer can check the claim that each task is independently mergeable:

- **V2.1.2 → V2.4.1.** Overlap needs two recorded sets; without the record there is nothing to compare (design F17).
- **V2.2.1 → V2.2.2 → V2.2.3.** The footprint and the ranking are properties of the graph's nodes; the schema comes first.
- **V2.2.3 → V2.3.1, V2.6.2.** "Ready", "blocked" and "scheduled earlier than its dependency" are computed where the graph is read (design F7, F24).
- **V2.1.1 → nothing.** The installations document is a read with no dependents in this plan; the sidebar consumes it, and the sidebar is v1's phase 2 work.
- **V2.5.\* → V2.0.1 and its own decision.** No world-changing task lands before the owner answers the decision that governs it, and none is bundled with another. **All three decisions are now answered: D22 (the launch) and D34 (cancel and retry — "not yet") on 2026-10-16, and D30 (the roadmap affordance — read-only, "copy the change") on 2026-10-15/16** — so V2.5.2 stays unscheduled until the owner revisits D34, V2.5.3 is a read-only affordance the owner may schedule with V2.5.1, and an answer to one decision does not carry to the next task in the phase.
- **V2.8.1 → nothing.** The skill writes a document; whether the console reads it is V2.2's business.
- **Everything → the owner approval gate.** This is the dependency that matters, and it is the first one, not the last.

## 7. What this plan does not do

- **It does not reopen v1.** Phases 0–5 of [2026-10-15-desktop-console.md](2026-10-15-desktop-console.md) and decisions D1–D20 of [desktop-console.md](../design/desktop-console.md) stand as they are; where v2 corrects a sentence in a merged document (F2's gap 5, the fence's move in V2.5.1), it writes a **new** sentence beside the old one and says so.
- **It does not schedule the command queue.** The design recommends against it in every slice (D21), and **the owner answered that on 2026-10-16, in D22's own words: no command queue** — so the plan contains no task for it, and the answer's scope is the slice the owner was deciding. If the owner rules the other way, the queue is a design of its own: who may write it, what runs it, the claim, the ack, the replay and the kill switch (design F11) are six answers before it is one task.
- **It does not put cancel and retry in the first slice.** **The owner answered D34 on 2026-10-16 with "not yet"** — each write widens the spawn allow-list and `fugaro cancel` has no confirmation of its own — so V2.5.2's spawns are unscheduled and unapproved (§3, "Unscheduled and unapproved"), no first-slice task builds one, and neither `fugaro cancel` nor `fugaro run --retry` gets an allow-list entry until the owner revisits the answer.
- **It does not touch a secret, run anything live, or go near the `belong` installation.** No agent-run step in any task here does any of those three things.
- **It does not add a workflow file.** Any workflow a later phase needs is staged under `docs/pending-workflows/` and applied by the owner.
