# In-job review — the documents

**Status: reconciliation only; no code is implemented; the plan waits for the owner's answers.** This folder holds the owner's spec of 2026-10-18 for moving review inside the cloud job, and the reconciliation of every concrete claim it makes against the code. The reconciliation raises **Q1–Q17**, each with options, a recommendation and its cost; answering them is the gate. Nothing is approved: answering a question is not approving a task, and no code, Terraform, schema, recipe or live action follows from these documents. A later run writes `implementation-plan.md` here from the owner's answers, and only then does any work follow — each task needing its own explicit go.

**Owner:** Dimi Paun. **Date:** 2026-10-18/19 (spec 2026-10-18; reconciliation 2026-10-19).

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, "Fugaro: In-Job Review Spec" (Draft · Oct 18, 2026 · Owner: Dimi Paun), **verbatim**, committed alone in its own first commit. No typo needed fixing, so nothing in it was changed. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief it was written from; the headline table (spec concept → what the repository has); findings **#1–#43** against `origin/main` at `82ba600`, each classified **Wrong / Exists / Gap / Rule / Open** with a path and a symbol and its consequence; what the parked mixed-credentials design still contributes and what the credentials-service plan supersedes; **Q1–Q17** in "Open for the owner", each with two to four options, a recommendation first with the reason, the consequence of each, and the part of the spec it changes; eight **resolutions** (R1–R8) for findings with one defensible answer; and the **UNVERIFIED** table (U1–U6). Where it disagrees with `spec.md`, it wins. |

## What this work checked, and how

**Checked against `origin/main` at `82ba600d984a420c595cf06417af6d781c18535a`, on 2026-10-19.** `graphify` has no graph for this repository (there is no `graphify-out/`), so every claim was located with `git grep` and then read in place; every path and Go symbol cited was re-checked to exist on `82ba600` before it was written. Line numbers are never cited; a few appear inside quoted code only where the quote is the point.

Two corrections to the brief, recorded in the reconciliation itself and repeated here because they change how its findings read:

- **There is no `CLAUDE.md` and no `.claude/rules/` in this repository.** `.claude/` holds only `skills/new-release/SKILL.md`. The **Rule** class therefore means a conflict with [SECURITY.md](../../SECURITY.md), a design or plan document, or a rule the code enforces, and each Rule finding names which.
- **"Agents never force-push" is not what the repository says.** The runner pushes with `--force-with-lease` on its own `fugaro/<run-id>` branch (`internal/gitops/gitops.go` `Push`, `PushExisting`, guarded by `checkRunBranch` and `tipBelongsToRun`); checkpoint pushes are fast-forward only (`gitops.PushFastForward`, which resolves `ErrNotFastForward`); the base branch is never pushed at all. The consequence matters for §8 of the spec and is reasoned out in the reconciliation §1.2 and finding #28 — and it is why §8 is a question for the owner rather than an assumption either way.

**How this folder is pinned.** No test in `internal/cli` walks `docs/` or pins the set of docs folders; each docs package is pinned by its own file, and this one is `internal/cli/docs_in_job_review_test.go`. It fixes the status line above word for word and the two file names, [spec.md](spec.md)'s title, status line and all ten section headings (it is the owner's text, unchanged), the reconciliation's findings as #1..#43 with no gap and one of the five class words each, its questions as Q1..Q17 with no gap and this index's own ranges (checked against what the reconciliation actually carries), the two brief corrections above as statements rather than mentions, and the "no D-number, no check number" claim; and it resolves every load-bearing citation in both documents against the tree. It deliberately cites no `console/` path — the module's console boundary allows one exception only (`TestConsoleIsOutsideTheGoModule`'s `consoleBoundaryAllowList`, plan Task 0.3 a3), so where the reconciliation cites the console's own files the pin stops at the module's edge and the prose carries the claim.

## The numbers this work holds

| Block | State |
|---|---|
| **Q1–Q17** | this work's questions, written in `reconciliation.md`'s "Open for the owner". None is answered. |
| **D-numbers** | **none allocated.** This work states every finding that needs the owner as a **Q**, so the D-blocks other works hold (credentials-service holds D130–D159) are untouched. |
| **Check numbers** | **none claimed.** This work makes no live claim and writes no check section. If a later plan needs owner-run checks it takes the number free at its own merge, per the rule in [docs/gcp-live-checklist.md](../gcp-live-checklist.md). |

A reserved number is never reused and never renumbered.

## Related

- [docs/recipes.md](../recipes.md) and [docs/design/recipes.md](../design/recipes.md): the recipe format the spec's §3 and §4 run into — reserved keys, `max_rounds`, by-role models, and the readiness rule the spec's §6 would change.
- [docs/dogfooding.md](../dogfooding.md): the standing posture the spec's §2 goal changes — "the run's own review is a smoke check, not independent review: keep the real review **outside** the loop … A run's own `ship` never replaces it."
- [docs/design/mixed-credentials.md](../design/mixed-credentials.md) and the held [docs/plans/2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md): what "one job, two model credentials" costs, and what is parked.
- [docs/credentials-service/](../credentials-service/README.md): the owner's newer credential direction, which supersedes the parked design's per-call credential switching and gates any work that depends on it.
- [docs/design/budget-token-channel.md](../design/budget-token-channel.md) and [docs/design/project-centric.md](../design/project-centric.md) §6.2: the spend side of running Opus reviewers in a job.
- [SECURITY.md](../../SECURITY.md): what "reviewers are read-only" can and cannot mean here, and why a verdict is advice rather than evidence.
