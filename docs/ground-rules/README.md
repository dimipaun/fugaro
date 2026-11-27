# Ground rules

**Status: plan only; nothing is implemented; no task approved.** Every decision is answered.

**Spec owner:** Dimi Paun. Received, reconciled and planned on 2026-11-26, against `origin/main` at
`ad84f51c5`.

Six rules that win over any design choice they conflict with:
- `fugaro update` is the one project-wide update;
- agents may run Fugaro commands, `update` included;
- a delete is gated on what it deletes;
- output is one plain line per step, and the detail goes to the log;
- a run fetches a usable baked repository or clones fresh;
- a run upgrades itself to the version that launched it, so images catch up overnight.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received |
| [reconciliation.md](reconciliation.md) | The module map, findings F1–F31, decisions D3420–D3449 with the owner's answers, tension T1, UNVERIFIED U1–U3, and §A, the audit of agent refusals. **It wins over the spec** |
| [implementation-plan.md](implementation-plan.md) | 34 tasks in seven slices and six levels, with the critical path, the shared files and Checks 950–953. Nothing in it is approved |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan as `fugaro plan graph` reads it |

**Numbers taken:**
- decisions D3420–D3459: D3420–D3449 used, D3450–D3459 reserved and unused;
- Checks 950–959: 950–953 used, 954–959 reserved and unused.

The block starts above D3411, the highest number on `main` (`docs/coordinator/README.md`).

**What this supersedes.**
- `docs/update-from-anywhere/`: U11 is cancelled and U12 no longer waits on it (D3420 amends D3353).
- `docs/managed-agent-converge/`: gate rule 1 now judges the class of what is deleted (D3427).
- CLAUDE.md's HARD RULE on `update` is rewritten by task DOC1 (D3447).

No spec typo was fixed.
