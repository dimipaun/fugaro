# The stateless orchestrator

**Status: plan only; nothing is implemented; no task approved. Every decision D1220-D1243 and every question the plan raised (Q1-Q4) is answered — Q2 twice — and the last tension the answers left open, T7, was ruled on 2026-11-05, so nothing is left for the owner to rule** (reconciliation section 6).

**Spec owner: the owner (Dimi Paun). Date: 2026-11-04.** Checked against `origin/main` at `5998d67c7`.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's spec verbatim: the stateless orchestrator loop and its daemon addendum. Not edited |
| [reconciliation.md](reconciliation.md) | The spec against the code (F1-F23), the hard questions, the decisions **D1220-D1243** with the owner's answers of 2026-11-04, the UNVERIFIED list (U1-U9) and the tensions (T1-T7; T6 and T7 closed by the owner's answers, T7 not the recommendation) |
| [implementation-plan.md](implementation-plan.md) | Tasks O0-O19 in three slices, the parallel levels, the shared-files table, the owner-run Checks 300-305 and the owner questions Q1-Q4 |

## The owner's answers

All of D1220-D1243 were answered on 2026-11-04. Most are the recommendation; **three rulings are not** — D1235, the retry rule and T7 — and Q2 was ruled twice:

- **D1235: cut over at once**, with no shadow week. The old scripts are kept 14 days and the rollback is a documented, manual one (stop the daemon,
  re-arm the sweep, the watcher and the queue scripts); no rollback command is built.
- **The retry rule: one approval covers the first attempt and at most one automatic retry** of that same approved spec after a first failure, so one
  approval can launch two runs. The spec's "Task failed, first time: Code retries" row stands; a refusal (a failed `Precheck`, the kill switch, a
  spend cap, a validation refusal) is never retried, a second failure needs a person's new approval, and the caps still bound it.
- **Q2: the owner's answer, 2026-11-04 — a different answer from the one this PR first recorded, which is superseded.** **`fugaro setup` installs
  the daemon** (a listed step of its plan, at a real terminal, refused under a coding agent's marker, and in a managed project no extra confirmation),
  **`fugaro orchestrator install` upgrades it**, and **`fugaro update` never touches the unit.** The question the first answer collided with — what
  an offer does on a managed project — is gone, so **T6 is closed**; what the answer left behind was **T7** in reconciliation section 6, and that is
  ruled too.
- **T7: ruled 2026-11-05, and not the recommendation — it amends Q2.** The owner refused the carve-out and made the HARD RULE literally true:
  **`fugaro update` re-renders the daemon's unit when the rendered template changed**, behind the same plan-and-confirm rules as every other `update`
  step (a write to the person's own home and service manager, not a cloud write), so no release's `### For operators` ever needs a second command.
  **`update` only ever re-renders a unit that is already installed; it never installs one.** `fugaro setup` still installs the first one (Q2), and
  `fugaro orchestrator install` stays as the explicit command — a machine that skipped setup, or a repair — but no release depends on it. The daemon's
  self-restart on a changed binary (it exits when the binary the unit names differs from the one it is running, so launchd or systemd starts the new
  one) and its start-up state migration stay as the plan has them.

D1241 (the person's own credential writes the `orchestrator/` node), D1242 (the terminal Typed confirmation now, a paired-device channel later) and
D1243 (the session-start print reads a local cache the daemon writes) are all the recommendation, and the gates the plan had put on them are lifted.

## The number block

**D1220-D1249**, confirmed by the owner. D1220-D1243 are written; **D1244-D1249 are reserved and unused.** Checks **300-309** are reserved for the
plan (300-305 used, 306-309 reserved), verified free on 2026-11-04.

## The spec's typo and numbering fixes

None. The spec was only renamed from `fugaro-stateless-orchestrator-spec.md`; its two references to "fugaro-coordinator-spec" and
"fugaro-mcp-server-spec" are `docs/coordinator/spec.md` and `docs/mcp-server/spec.md`.

## Amended-by notes

`docs/coordinator/README.md` and `docs/central-orchestrator/README.md` each carry a one-line note that this folder amends them (D1236); both
folders exist on `main`, and their owner-verbatim specs are unchanged.

## What a reader should not take from here

Not an approval to build: each task needs the owner's go by name. Not a plan for the hosted MCP state group before the MCP server exists
(T2-T4). Not a shadow-mode migration: the owner chose to cut over at once. Not a plan the owner has not seen: T7 was the last open question and the
owner ruled it on 2026-11-05 (`fugaro update` re-renders an installed unit, so the HARD RULE is literally true), so the daemon's self-upgrade (O8), the
install in `fugaro setup` and `fugaro orchestrator install` (O9) and the update-side re-render (O9) are all the ruled design rather than a reading
reported up.
