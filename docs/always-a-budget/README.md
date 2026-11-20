# Always a budget

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `fb0aeb9aa`.

Budget mode off is removed. Every run has a gateway and an identity, every installation has the
budget database, and `fugaro update` carries an installation there alone. It carries out D3009 to
D3011 of [every model key through the credentials service](../keys-through-service/README.md),
whose tasks KS14 and KS15 wait on this plan's I1.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's rulings, quoting his words; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A4, findings F1–F19, decisions D3020–D3022 with the owner's answers, resolutions R1–R10, open items O1–O5, UNVERIFIED U1–U4. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks AB1a to AB15, integration I1 (19 runs in one release, and one contract change that runs only if raised); levels, critical path, shared files; Checks 790 and 791 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3020–D3039** (D3020–D3022 used), checks **790–799** (790 and
791 used). Read on 2026-11-20: the highest decision on `main` was D2999 (reserved), with
D3000–D3019 held by the keys plan's open pull request; the highest check was 773, with 774–779
reserved and 780–789 held by the same pull request.

**Owner answers of 2026-11-20:** D3021 as recommended (Cloud Batch refuses every launch). **D3020
is not the recommendation**: a model with no price is refused in every mode, where running it
under observe was recommended. **D3022 is not the recommendation**: a repository's `fugaro.yaml`
that says off is rejected at once, where reading it as unset for one release was recommended.
D3009 to D3011 were answered on 2026-11-19 in the keys plan.

**What a reader should not take from here:** no task is approved; each needs the owner's go by name.
No release is cut while the plan is partly merged past its level 1. Checks 790 and 791 are
owner-run after the release. R4 (the policy node keeps its version) waits for the owner's word
(O1).
