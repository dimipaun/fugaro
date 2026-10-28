# Governance, the cost report and the coordinator move under `ee/`

**Status: plan only; nothing is implemented; no task approved.** Decisions D520–D536 are written
with recommendations and **unanswered**. The CLI 0.13.0 tag is held until governance is under
`ee/` (the owner's ruling).

**Spec owner:** the Fugaro owner. **Ruling:** 2026-10-29. **Reconciled against** `origin/main` at
`028b46357` (2026-10-28).

**Correction of 2026-10-29 (the owner: "sorry, my mistake"): the digest stays MIT.** The spec's
answers line counts digest reports as governance and says it reverses T11; the owner withdrew that.
T11 stands: `fugaro runs digest`, the `run-digest` recipe, the runner's digest step,
`internal/digest` and the periodic-reviews skill text stay MIT, with their own copy of what they use
from `internal/report`. `spec.md` is kept verbatim; [reconciliation.md](reconciliation.md) §0
records the correction, and the plan follows it.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's ruling, **verbatim** (before the correction) |
| [reconciliation.md](reconciliation.md) | The correction (§0), findings F1–F18 (with F16a–F16c) against the code, resolutions R1–R8, decisions D520–D536 (unanswered), open items O1–O6, UNVERIFIED U1–U6. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices E0–E3, tasks E0.1–E3.4 (with E1.7; E2.4 withdrawn), levels L0–L8, the critical path, the shared-files table, Checks 200–201. Supersedes subscriptions S1.1–S1.5, S1.7, S1.9. **Nothing in it is approved** |

## Typos fixed in the spec

None. It is byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D520–D549** | this work's block. **D520–D536** written, unanswered; **D537–D549** reserved and unused. Taken above the highest decision on `main` (D519, secrets-brokering's block D490–D519) and in every open PR's docs (D260) |
| **Checks 200–209** | this work's block. **200–201** proposed by the plan (written by E3.4, run by the owner, NOT RUN); **202–209** reserved and unused. Above Checks 190–199 (secrets-brokering) |

## Known discrepancy left in place

The Subscription Terms (`docs/licensing/subscription-terms.md`, `website/content/subscription-terms.md`)
still call the coordinator part of the Core. That text is counsel's; D531 proposes that counsel's
next revision (subscriptions S0.4) fixes it.
