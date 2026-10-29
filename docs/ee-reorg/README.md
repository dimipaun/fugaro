# Governance, the cost report and the coordinator move under `ee/`

**Status: all decisions answered; plan only; nothing is implemented; no task approved.** Decisions D520–D536 are written
with recommendations; all are answered (2026-10-29), D523, D531 and D533 NOT as recommended: **no licence notice at all until the verifier, the key-minting service and the launch are ready (the warn tasks are deferred); the Terms text is edited now (E3.6); the 0.13.0 notes carry no operator steps.** O1 and O2 are answered; O3 (the agent-code reading) is still the owner's confirmation; **O7 is answered (2026-10-29): the 0.13.0 notes carry one line — refresh images to get governance — and it is not a required operator step** (D533 against D524: governance is available only after an image refresh). The CLI 0.13.0 tag is held until governance is under
`ee/` and the CLA placeholder is replaced (the owner's rulings).

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
| [reconciliation.md](reconciliation.md) | The correction (§0), findings F1–F18 (with F16a–F16c) against the code, resolutions R1–R8, decisions D520–D536 (all answered), open items O1–O7, UNVERIFIED U1–U6. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices E0–E3, tasks E0.1–E3.5 (with E1.7; E2.4 withdrawn), levels L0–L8, the critical path, the shared-files table, Checks 200–201. Supersedes subscriptions S1.1–S1.5, S1.7, S1.9. **Nothing in it is approved** |

## Typos fixed in the spec

None. It is byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D520–D549** | this work's block. **D520–D536** written and all answered; **D537–D549** reserved and unused. Taken above the highest decision on `main` (D519, secrets-brokering's block D490–D519) and in every open PR's docs (D260) |
| **Checks 200–209** | this work's block. **200–201** proposed by the plan (written by E3.4, run by the owner, NOT RUN); **202–209** reserved and unused. Above Checks 190–199 (secrets-brokering) |

## Known discrepancy: being fixed

The Subscription Terms (`docs/licensing/subscription-terms.md`, `website/content/subscription-terms.md`)
still call the coordinator part of the Core. The owner ruled on 2026-10-29 (D531) to edit the text
now rather than wait for counsel's revision; task E3.6 makes the edit and the owner tells counsel in
the next revision round.

## Key-minting service

Owner ruling of 2026-10-29: the licensing key-minting service belongs under `ee/` (task note E4.1),
its deploy is owner-run, and it is deferred with the key work.
