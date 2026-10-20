# Rigor modes — POC, Startup, Pro, Critical, Hotfix

**Status: plan only; nothing is implemented; decisions open.**

**Owner:** the Fugaro owner
**Written:** 2026-10-20
**Base:** `main` at `1d325928` (`1d32592836a089cce2d72ff53e29116a00e1c433`)

## The four documents

| Document | What it is |
| --- | --- |
| [spec.md](spec.md) | the owner's spec, **verbatim**, in its own commit. Nothing is added to it, and its open questions stay open here. |
| [reconciliation.md](reconciliation.md) | what the tree says back: five corrections to the premises, **F1–F34** findings each classified Wrong / Exists / Gap / Rule / Open, the census with its commands, **D1–D19** decisions recommended but not taken, **O1–O7** items, and the **U1–U8** UNVERIFIED table. |
| [implementation-plan.md](implementation-plan.md) | nine slices that each merge alone and leave `main` green, each task with its files, tests, red step, verification, dependency and reviewer — and an **OWNER APPROVAL GATE** that is **not passed**. |
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
**Critical** — today's behaviour — so the first cut relaxes nothing, and every
slice is ordered to leave an unconfigured repository behaving exactly as it does
now.

The owner's 2026-10-20 spec was written without seeing this repository, so
several of its premises moved work: the top-level `review:` configuration it
implies does not exist (and one is pinned *absent*), the finding vocabulary is
not the tree's, and the practice it formalises lives in `docs/dogfooding.md`,
`docs/credentials-service/implementation-plan.md` and `docs/plans/`, not in
`docs/coordinator/`. §2 of the reconciliation lists all five, first, because a
design built on any of them would be designing against a repository that does
not exist. **The modes are a formalisation of the owner's own written practice**,
and each mode names the sentence it automates.

## The numbers this work holds

| Numbers | State |
| --- | --- |
| **F1–F34** | the reconciliation's findings. Every one carries a path and a symbol and **no line numbers**, and one of the five class words. |
| **D1–D19** | this work's decisions, all written, **all open**. They are a *folder-local* namespace, distinct by folder — the same choice [docs/windows-cli/](../windows-cli/) made with its D1–D16 and [docs/setup-wizard/](../setup-wizard/) with its D1–D12. The register of every block is [docs/coordinator/README.md](../coordinator/) §"The numbers this work holds": **D1–D192 is the owner's allocation, D193 onward is free**. **Recommendation: keep D1–D19 local and distinct by folder, or move this work to D193 onward; renumbering is mechanical and changes nothing but the numbers** (O7). |
| **O1–O7** | items for the owner that are deliberately not decisions — a measurement (calibration), two questions about the strictest mode, the drain of a ready Hotfix PR, an App permission, the CLI's own names, and the D-block above. |
| **U1–U8** | claims this work could not check from the tree, each with what would verify it. Two of them — whether a cheap model scores reliably (U1) and whether a mode is in fact cheaper (U2) — are answered by the live checks and by the plan's T2.4, not by reading. |
| **Check 119** | the live check this work proposes. The register is [docs/gcp-live-checklist.md](../gcp-live-checklist.md): **1–109 are taken or reserved**, and **110–118 are all held** — 110 claimed by the always-on orchestrator design (PR #632) and named by the mobile P5.3 design, and written by nobody either way, 111 taken by [docs/setup-wizard/](../setup-wizard/), 112 proposed by [docs/windows-cli/](../windows-cli/), and 113–118 taken by [docs/mobile/voice-session/](../mobile/voice-session/)'s six numbered checks. Those five are claims rather than written sections, so this work does **not** move up into a gap if one is yielded: 119 is free whichever way that collision falls, and the register's own rule applies — **whichever of two open claims merges second renumbers itself**, never a number an earlier plan has already written. |
| **No new D-block, no new check range, no new schema** | the three registers this folder touches, and the reason each is written down here rather than discovered later. |

## What this folder is not

- **Not an approval.** A recommendation is not a go, and the plan's
  [OWNER APPROVAL GATE](implementation-plan.md#owner-approval-gate) says in its
  own words that it is not passed. Nothing follows from these documents on its
  own.
- **Not a product document.** When the modes ship they get one —
  `docs/rigor-modes.md`, the plan's T7.1 — and this folder is where the design
  and the plan live until then. The two are separate on purpose: a folder that is
  a plan ages into a design record, and a product document is a promise.
- **Not a change.** No code, config, schema or workflow is touched here, and the
  plan's slices say what would touch what.

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
  (F30). The modes are a formalisation of sentences the owner already wrote.
- [docs/credentials-service/implementation-plan.md](../credentials-service/implementation-plan.md)
  — Global Constraint 10, the independent-review rule the security section keeps
  (F31, §6 of the reconciliation).
- [docs/coordinator/README.md](../coordinator/) — the two registers this folder
  reads: the D-blocks and the check numbers.
