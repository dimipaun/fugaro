# A launcher does not mint a run's identity

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `7987acb99`.

Today every launcher, every operator and the resident orchestrator can sign a token for any
identity in the Firebase project, so one launcher can spend as a run another launched. This spec
takes that grant away: the credentials service issues a run's identity, as a one-time ticket the
launcher asks for and the run itself claims, and `fugaro update` moves the grant once the service
answers. It is the spec batch-run-identity's F24 and O6 said was not written.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's ruling; its ruling section is a placeholder the owner has not filled (D3120 confirms the reading). No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A6, the census (who signs, who holds a signing grant, where a launch starts, who verifies), findings F1–F22, what each option does to the trust boundary, decisions D3120–D3130 with the owner's answers, resolutions R1–R15, open items O1–O11, UNVERIFIED U1–U8. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks C1, LM0 to LM19 and I1 (22 runs); levels, critical path, shared files; Checks 840 and 841 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Other plans read, and where.** batch-run-identity on its branch `docs/batch-run-identity` at
`910a545a7` (pull request #3323, open); credentials-belong-to-models on
`docs/credentials-belong-to-models` at `d78e355ff` (pull request #3315, open). always-a-budget,
keys-through-service, workspace-service and the others on `main` at `7987acb99`.

**Numbers reserved:** decisions **D3120–D3139** (D3120–D3130 used), checks **840–849** (840 and
841 used). First reading, 2026-11-20 19:33–19:36 UTC, against `main` at `7987acb99` and the diff
of each of the 61 open pull requests: the highest decision on `main` was D3099 (location-preference,
D3080–D3099); the highest anywhere was D3119, reserved by the open pull request #3323
(batch-run-identity, D3100–D3119, checks 830–839); #3315 holds D3060–D3079 and checks 810–819.
The highest check on `main` was 821. No open pull request took a number in D3120–D3139 or
840–849. SECOND_READING

**Owner answers of 2026-11-20:** D3120 to D3130, **every one as recommended**; none is marked
"not the recommendation" and none was "your call". Two answers reach outside this folder:
**D3121 amends the rule** that the credentials service account's only grant is its database
role, and **D3129 puts on hold** the go batch-run-identity's C1 was given.

REVIEW_ROUNDS
