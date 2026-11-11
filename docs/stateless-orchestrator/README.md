# The stateless orchestrator

**Status (2026-11-10): twelve of the [implementation plan](implementation-plan.md)'s twenty tasks are merged on `main`** (O0-O8, O10, O17, O18);
O9 (#2193) and O12 (#2290) are open drafts; O11, O13 and O14 are not started. **This revision makes O16 and O19 tasks**: O16 splits into
**O16a** (the MCP state scopes, catalogue rows and read handlers, dormant — **approved and launchable**, after the open #1994) and **O16b**
(serving them — approved, waits on the MCP front door's wiring task) and **O19** (a workspace's refusal pauses the queue — approved, waits on
W7, #2275). It writes the MCP amendments T3 and T4 into `docs/mcp-server` and puts **D2020-D2022** to the owner, **all answered as recommended
on 2026-11-10** (reconciliation section 8; D2023-D2029 reserved). **The owner approved all tasks on 2026-11-10.** The status of 2026-11-08
follows, kept.

**Status (2026-11-08): six tasks of the [implementation plan](implementation-plan.md) are built, all merged on `main`.** Task **O0**, the state schema,
the typed patch and the validator (#1823, `36abeffc`, 2026-11-07): `internal/orchestrator/state.go`, `patch.go`, `validate.go`, `summary.go`, their tests,
`testdata/state-a.json` and the two `schemas/` documents. Task **O1**, the deterministic security-sensitive classifier (#1822, `405dae8b`, 2026-11-07), is its
own four files in `internal/orchestrator/sensitivity/` (the package also holds SC1's `floor` and `pattern` files); **O2** (#1945, `5053cbce`), the `orchestrator/`
subtree and its rules, **O3** (#1954, `70ec7800`), the state store and the audit log, **O4** (#1825, `8e534f5d`), the guardrail policy fields, and **O6** (#1950, `6145e445`), the launcher, are merged too. Every other task is plan only, including **BF2's long-run triage rule (2026-11-08): task O14 and the decisions D1244-D1247, design only, no task approved.**

**Check 300 has run: PASSED 2026-11-10.** The owner ran `bash scripts/live-checks/check300.sh --installation fugaro` with CLI 0.22.1 on the dogfood installation (project `fugaro`, GCP `fugaro-dev`) and every fact passed — the `orchestrator/` subtree and its marker on the real database, the person's own credential writing the state node (D1241's answer) while a credential-less client was refused the same node, and the deployed document byte-for-byte unchanged — so O2's fence is **verified live**; the register's row and section (`docs/gcp-live-checklist.md`) carry the record. **Checks 301 to 304 stay the owner's, and 305 stays reserved** for O16.

*The status as first written, kept:* **Status: plan only; nothing is implemented; no task approved. Every decision D1220-D1243 and every question the plan raised (Q1-Q4) is answered — Q2 twice — and the last tension the answers left open, T7, was ruled on 2026-11-05, so nothing is left for the owner to rule** (reconciliation section 6).

**Spec owner: the owner (Dimi Paun). Date: 2026-11-04.** Checked against `origin/main` at `5998d67c7`.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's spec verbatim: the stateless orchestrator loop and its daemon addendum. Not edited |
| [reconciliation.md](reconciliation.md) | The spec against the code (F1-F23), the hard questions, the decisions **D1220-D1247** with the owner's answers of 2026-11-04 and 2026-11-08 (BF2: D1244-D1247, the long-run triage rule), the pass of 2026-11-10 (section 8: F24-F29, R1-R3, **D2020-D2022 answered 2026-11-10, as recommended**), the UNVERIFIED list (U1-U12) and the tensions (T1-T7; T6 and T7 closed by the owner's answers, T7 not the recommendation) |
| [implementation-plan.md](implementation-plan.md) | Tasks O0-O19 (O16 split into O16a and O16b on 2026-11-10) in three slices, the parallel levels, the shared-files table, the owner-run Checks 300-305 (300 **PASSED 2026-11-10**) and the owner questions Q1-Q4 |

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
- **BF2, 2026-11-08: the long-run triage rule (D1244-D1247), all four the recommendation.** One task, **O14**, and it adds no ruling that is not a
  recommendation; the four outcomes are tried in a fixed order — escalate, then recommend-only, then merge-as-is, then restart: the two that act on the run come after the fail-closed one, so an input nobody could read is a print and never a cancel. The two defaults it rests on are the owner's:
  **merge as-is with follow-ups** — a **finished** run (never one still in flight) whose pull request is **ready**, its whole diff inside the routine trees, green CI,
  no critical finding and none high in a security path, is merged by code and the findings it leaves become a follow-up task — and **a run
  that produced nothing usable is split into follow-up sub-tasks** rather than re-run on the same spec. A **readable critical** finding, or a **high** one
  in a security path, stops the run and asks the owner (a `waiting_on: person` decision). Everything else **fails closed** into the fourth,
  **recommend-only** outcome — BF1's file absent, `CIUnknown`, a truncated path set, a severity the triage does not know — tried before the two that act, so it merges nothing, cancels
  nothing and restarts nothing, and prints what it could not read. Every triage is written to the orchestrator audit log under `<run>:triage` and printed read-only by
  **`fugaro runs watch`**; until BF1 lands that is every long run (U10), and what fires it is the per-mode ladder table of `docs/outer-loop-escalation` — local-only keys, one set per rigor mode (D2032) — rather than a single age threshold.

D1241 (the person's own credential writes the `orchestrator/` node), D1242 (the terminal Typed confirmation now, a paired-device channel later) and
D1243 (the session-start print reads a local cache the daemon writes) are all the recommendation, and the gates the plan had put on them are lifted.

## The number block

**D1220-D1249**, confirmed by the owner. D1220-D1247 are written (D1244-D1247 by BF2); **D1248-D1249 are reserved and unused.** Checks **300-309** are
reserved for the plan (300-305 used, 306-309 reserved), verified free on 2026-11-04. **Check 300 has run — PASSED 2026-11-10** — and
301-305 stay the owner's, with 305 reserved until O16 names its files.

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
