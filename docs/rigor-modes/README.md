# Rigor modes — POC, Startup, Pro, Critical, Hotfix

**Status: approved to build on the branch `rigor-modes`; the owner approval gate is passed.**

**Owner:** the Fugaro owner
**Written:** 2026-10-20
**Base:** `main` at `1d325928` (`1d32592836a089cce2d72ff53e29116a00e1c433`)

## The four documents

| Document | What it is |
| --- | --- |
| [spec.md](spec.md) | the owner's spec, **verbatim**, in its own commit. Nothing is added to it, and its open questions stay open here. |
| [reconciliation.md](reconciliation.md) | what the tree says back: five corrections to the premises, **F1–F48** findings each classified Wrong / Exists / Gap / Rule / Open, the census with its commands, **D1–D32** decisions **answered by the owner on 2026-10-20** (each answer in place of the recommendation it displaces, the recommendation kept beneath it), **O1–O7** items, and the **U1–U12** UNVERIFIED table. |
| [implementation-plan.md](implementation-plan.md) | nine slices that each merge alone and leave `main` green, each task with its files, tests, red step, verification, dependency and reviewer — and an **OWNER APPROVAL GATE** that **is passed**: the owner answered §5's decisions — **D1–D32** — on 2026-10-20 and the plan is approved to build on the branch `rigor-modes`. |
| this file | the index, and the numbers this work holds. |

## What the work is, in one paragraph

The owner's spec asks for rigor to be a dial: POC, Startup, Pro, Critical and an
off-axis Hotfix, differing mainly in what happens to a reviewer's findings rather
than in how hard the reviewer looks. The repository has no such dial today, and
it is further from one than the spec assumes — a finding's severity is a
free-form string that no line of Go compares against anything, every finding
blocks equally, and readiness is a single rule that `docs/recipes.md` states is
not configurable. So the reconciliation is where the work is: it finds that the
score has to be additive to a schema whose item is open, that the policy has to
live *inside* `runner.Decide` rather than beside it, that the five words
`blocker`/`high`/`medium`/`low`/`nit` are a vocabulary this repository does not
have, and that Hotfix's "ship immediately" cannot mean a merge or a deploy
because `gitprov` has no merge call and the runner never deploys. What is
already there is reused rather than rebuilt: `doublestar/v4` for path rules, one
trust list for authority, the recorded `ChangedFiles` for a post-hoc floor check,
the existing CI on `main` for the post-flight suite. The migration default is
**Pro** (D19). What this branch builds changes two things for a repository that
configures nothing: its runs resolve and record Pro, and Pro's per-run budget
default is 0.85 of the deployed cap (T6.1). It does **not** change what "ready"
means yet: `runner.Decide` takes no mode until Slice 3 (the run loop that applies
the mode) is built, and that is the slice that will change readiness for an
unconfigured repository. The loud migration note plus the one-line pin
`rigor.default: critical` are what a repository uses to keep today's behaviour;
the security-sensitive tier (the D18 floors) stays **Critical**, so every claim
that a cut "relaxes nothing" or is "byte-identical to today" holds for
**Critical mode only**.

The owner's 2026-10-20 spec was written without seeing this repository, so
several of its premises moved work: the top-level `review:` configuration it
implies does not exist (and one is pinned *absent*), the finding vocabulary is
not the tree's, and the practice it formalises lives in `docs/dogfooding.md`,
`docs/credentials-service/implementation-plan.md` and `docs/plans/`, not in
`docs/coordinator/`. §2 of the reconciliation lists all five, first, because a
design built on any of them would be designing against a repository that does
not exist. **The modes are a formalisation of the owner's own written practice**,
and each mode names the sentence it automates.

## The scope ruling: a default per project, overridable per repo, then per area

The owner ruled on 2026-10-20 that the rigor mode "has a default per PROJECT,
overridable per REPO, and then overridable DYNAMICALLY per AREA". This folder
takes that as the shape of the mode's configuration, and the
[reconciliation](reconciliation.md) §1.2 is the index of where each part lands.
In one paragraph: the project-wide default is a new member of the project layer's
closed `defaults:` (a per-project value, where every other per-project value
lives); a repository's own `rigor:` block in `fugaro.yaml` overrides it and
carries the repository's areas; an area is a named `doublestar` glob with a mode,
and which area applies is chosen **per run, from the files the run touches** —
from the paths the caller declares at bootstrap, from the run's own diff after
each review round, and from `runstore.Record.ChangedFiles` before the PR is
marked ready — never falling within a run. One resolver
(`internal/rigor`'s `Resolve`) orders the three, and the order is stated once, in
`docs/project-layer.md` §2's existing rule, one rank longer; this folder cites
it and does not restate it. A run that touches several areas resolves to the
strictest of them and records every area it matched, strictest first (D23). Every
one of the six decisions the ruling raised (D20–D25) is **answered**, with the
answer in place of the recommendation and the recommendation kept beneath it as
the alternative the answer refused.

## The backlog ruling: one Markdown file per item

The owner ruled on 2026-10-20 that where the modes have to save or file tickets for
the backlog, the backlog is **Markdown files in the repository under `docs/`**: one
file per item, plus a generated index that a small tool regenerates and a test pins
as current, so that parallel pull requests filing tickets never conflict on one
file. The [reconciliation](reconciliation.md) §1.3 is the index of where each part
lands, and the design is **D26–D32**: the id and the file name (D26), the front
matter and the three-word status vocabulary (D27), the index, its tool and its two
pins (D28), **which repository holds a project's items** (D29 — the owner's own
question, and the one value in the block still his to name), how each of the five modes files (D30), how
an item is closed (D31), and the relation to the deferred-leftover practice so that
there is **one backlog and not two** (D32). The plan builds it in Slice 4's last
five tasks — the format and the first items (T4.3), the index and its pins (T4.4),
the filing command and the run that files (T4.5), the per-mode rule (T4.6), and
closing plus the practice (T4.7) — each of which merges alone. **D9's answer
orders the work**: those five are picked up first, and Slice 3 (the run loop that
first tolerates a finding) depends on T4.5, because a mode may not tolerate what
there is no file to file.

Two things this ruling does not change: the [spec](spec.md) stays verbatim, and
the answers are written in the reconciliation rather than in the spec. What it
does change is where a deferred leftover is written down: a run files an item into **its own repository and
its own branch**, because that is the one repository its installation token reaches
and the one it can push (F43, F44), and the designated project repository holds the
items filed in it — which is why D29 is a decision for the owner rather than a
setting this folder picks.

## The numbers this work holds

| Numbers | State |
| --- | --- |
| **F1–F48** | the reconciliation's findings. Every one carries one of the five class words; every finding that claims something about code names a **path and a symbol** and **no line number**, and the two that record what the ruling rejects rather than what the tree holds — **F34** and **F48** — name the rejected item or practice instead. |
| **D1–D32** | this work's decisions, all written, **all answered by the owner on 2026-10-20** — with one value still his to name, **D29**'s designated repository for a project's items. They are a *folder-local* namespace, distinct by folder — the same choice [docs/windows-cli/](../windows-cli/) made with its D1–D16 and [docs/setup-wizard/](../setup-wizard/) with its D1–D12. The register of every block is [docs/coordinator/README.md](../coordinator/) §"The numbers this work holds": **D1–D192 is the owner's allocation**, its own figure — *"the highest number written anywhere in `docs/` at `c6009b5f` is D192"* — quoted with the anchor it was read at, and **D193 onward is free**. **Read at this work's own base, that free range is not this tree's.** At `1d325928` the register's own command returns **D220**, because [docs/mobile/voice-session/](../mobile/voice-session/) writes **D201–D220** there; so **D193–D200** are written nowhere but are not free either — voice-session left them alone and started at **D201** rather than take **D200**, which the owner's brief records as an already-taken ruling this tree cannot cite (its F20 and O5). **Recommendation: keep D1–D32 local and distinct by folder, as the two folders above did; if the owner prefers one sequence, the next free block read from this branch's base is D221 onward, not D193 onward; renumbering is mechanical and changes nothing but the numbers** (O7, which states the reading, the command and the collision). |
| **O1–O7** | items for the owner that are deliberately not decisions — a measurement (calibration), two questions about the strictest mode, the drain of a ready Hotfix PR, an App permission, the CLI's own names, and the D-block above. |
| **U1–U12** | claims this work could not check from the tree, each with what would verify it. Two of them — whether a cheap model scores reliably (U1) and whether a mode is in fact cheaper (U2) — are answered by the live checks and by the plan's T2.4, not by reading. |
| **Check 119** | the live check this work proposes. The register is [docs/gcp-live-checklist.md](../gcp-live-checklist.md): **1–109 are taken or reserved**, and **110–118 are all held** — 110 claimed by the always-on orchestrator design (PR #632) and named by the mobile P5.3 design, and written by nobody either way, 111 taken by [docs/setup-wizard/](../setup-wizard/), 112 proposed by [docs/windows-cli/](../windows-cli/), and 113–118 taken by [docs/mobile/voice-session/](../mobile/voice-session/)'s six numbered checks. Those five are claims rather than written sections, so this work does **not** move up into a gap if one is yielded: 119 is free whichever way that collision falls, and the register's own rule applies — **whichever of two open claims merges second renumbers itself**, never a number an earlier plan has already written. |
| **The backlog ruling** | one Markdown file per item under `docs/backlog/`, a generated index, and one backlog rather than two: §1.3, F42–F48, D26–D32, plan T4.3–T4.7. It adds no config key except D29's designated repository, no schema and no check number. |
| **No new D-block, no new check range, no new schema** | the three registers this folder touches, and the reason each is written down here rather than discovered later. |

## For operators

When this branch ships, three facts go into the release notes' `### For
operators` section:

- **An unconfigured repository now resolves Pro** (D19): a repository whose
  `fugaro.yaml` has no `rigor:` block, under a project layer that sets no rigor
  default, runs at Pro, and Pro's per-run budget default is **0.85 of the
  deployed per-run cap** (D16, plan T6.1). A repository that wants the whole cap
  and today's readiness rule pins `rigor: { default: critical }`.
- **Backlog items are filed by hand** (the owner's ruling of 2026-10-24, after
  the runner stopped filing): no run writes an item under `docs/backlog/` — a
  run's own record and its pull request's status section carry what it left
  open — and `fugaro backlog add` is what files one, in every repository, whether
  or not its `fugaro.yaml` has a `rigor:` block.
- **An older CLI is unaffected**: its runs use the runner image it deploys,
  which has no modes, so nothing changes until the CLI is upgraded.

## What this folder is not

- **Not implemented.** Not in full: the decisions are answered and the plan's
  [OWNER APPROVAL GATE](implementation-plan.md#owner-approval-gate) is passed, and
  part of the plan is built — Slice 0, Slice 1's mode layers and where they show
  (the run record, `fugaro runs ls`, `fugaro diagnose`, the pull request's status
  line, and the run's own `fugaro run --mode`/`--path` request), T2.1–T2.3 of
  Slice 2 (a score the run loop does not act on yet), the backlog (T4.3–T4.7),
  T5.1 (the task field that request travels in, `task.Spec.Rigor` and
  `task.Spec.Paths`, with its image-skew gate), T5.2 (a follow-up keeps the mode
  its pull request recorded and may only raise it) and the per-mode budgets
  (T6.1). The rest is not: Slice 3, which is what would make the mode decide
  readiness, Slice 5's Hotfix gate, ready PR and recorded follow-up task
  (T5.3–T5.5), and Slices 7 and 8.
- **Not a product document.** When the modes ship they get one —
  `docs/rigor-modes.md`, the plan's T7.1 — and this folder is where the design
  and the plan live until then. The two are separate on purpose: a folder that is
  a plan ages into a design record, and a product document is a promise.
- **Not the record of what is built.** This folder is documents: the slices say
  what would touch what, and the commits on the branch are what wrote each one.

## Related work

- [docs/in-job-review/](../in-job-review/) — the sibling design, and the
  questions it already answered (**Q1–Q17**), which this work must not
  contradict. The verdict contract, the finding model and the readiness rule all
  come from there (F33).
- [docs/recipes.md](../recipes.md) — the loop the modes sit beside, the
  readiness sentence, and the reserved keys no mode may use.
- [docs/windows-cli/](../windows-cli/) — the most recent folder of this shape,
  and the convention this one follows: its own pin test, its own D-block, its own
  status line.
- [docs/dogfooding.md](../dogfooding.md) — the practice the modes automate
  (F30). The modes are a formalisation of sentences the owner already wrote, and
  the document the backlog ruling's practice sentence lands in (D32, T4.7).
- [docs/plans/2026-10-17-managed-projects-review-follow-ups.md](../plans/2026-10-17-managed-projects-review-follow-ups.md)
  — the deferral practice the backlog replaces (F46), and what D32's sweep would
  convert into items.
- [docs/credentials-service/implementation-plan.md](../credentials-service/implementation-plan.md)
  — Global Constraint 10, the independent-review rule the security section keeps
  (F31, §6 of the reconciliation).
- [docs/coordinator/README.md](../coordinator/) — the two registers this folder
  reads: the D-blocks and the check numbers.

## The 2026-10-22 addendum: Rigor Levels

On 2026-10-22 the owner gave a second spec for the same dial, written as **five personas** (`poc`, `startup`, `pro`, `critical`, `hotfix`) with a new way to scope them (areas of interest that need not be one repository), a **promote** task, **review matching** and a **debt artefact** per level. It **layers on** the approved work above and changes none of its answers except where it says so: after an independent review, five of them (D6, D20, D23, D27, D30) carry an **amended after review** pointer to the addendum's §3a, **confirmed by the owner on 2026-10-22**. **Status of the addendum:** every decision and every review item is answered except three open items (POC guardrails, floors under Hotfix, the configuration shape); nothing in it is approved to build or implemented. The documents:

| Document | What it is |
| --- | --- |
| [levels-spec.md](levels-spec.md) | the owner's second spec, **verbatim**, in its own commit |
| [levels-reconciliation.md](levels-reconciliation.md) | what the tree says back: **LF1–LF14**, the owner's answers **LD1–LD11** (2026-10-22), the amendments after review (§3a, confirmed 2026-10-22), the review's items with the owner's answers (§3b), three open items and the UNVERIFIED list |
| [levels-implementation-plan.md](levels-implementation-plan.md) | the overlay plan: seven slices (LV1–LV7), the approved tasks it changes, parallel levels, critical path; **nothing is approved to build** |

The numbers this addendum holds: decisions **LD1–LD11**, findings **LF1–LF14**, no new check number (Slice 8's Check 119 gains one step per level). The word is **level** from here on (LD2); the 2026-10-20 documents keep saying *mode* until LV1.1 renames the code and LV1.2 restates their sentences.
