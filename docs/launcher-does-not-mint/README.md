# A launcher does not mint a run's identity

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `7987acb99`, and again after `c16781dc8` was merged in
during review (the cited symbols were re-read on the merged tree).

Today every launcher, every operator and the resident orchestrator can sign a token for any
identity in the Firebase project, so one launcher can spend as a run another launched. This spec
takes that grant away: the credentials service issues a run's identity, as a one-time ticket the
launcher asks for and the run itself claims, and `fugaro update` moves the grant once the service
answers. It is the spec batch-run-identity's F24 and O6 said was not written.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's ruling; its ruling section is a placeholder the owner has not filled (D3120 confirms the reading). No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A6, the census (who signs, who holds a signing grant, where a launch starts, who verifies), findings F1–F30, what each option does to the trust boundary, decisions D3120–D3131 with the owner's answers, resolutions R1–R16, open items O1–O16, UNVERIFIED U1–U10. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks C1, LM0 to LM23 without LM9, and I1 (25 runs); levels, critical path, shared files; Checks 840 and 841 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Other plans read, and where.** batch-run-identity was first read on its branch at `910a545a7`
while pull request #3323 was open; it merged as `b03f9147a` (branch tip `69f76967d`, which added
D3112 and BR22) and was read again on `main`. credentials-belong-to-models (#3315) merged as
`7abc5eb2f` and is read on `main` too. always-a-budget, keys-through-service, workspace-service,
roles-scopes and the others on `main`.

**Numbers reserved:** decisions **D3120–D3139** (D3120–D3131 used), checks **840–849** (840 and
841 used). First reading, 2026-11-20 19:33–19:36 UTC, against `main` at `7987acb99` and the diff
of each of the 61 open pull requests: the highest decision on `main` was D3099 (location-preference,
D3080–D3099); the highest anywhere was D3119, reserved by the open pull request #3323
(batch-run-identity, D3100–D3119, checks 830–839); #3315 holds D3060–D3079 and checks 810–819.
The highest check on `main` was 821. No open pull request took a number in D3120–D3139 or
840–849. SECOND_READING

**Owner answers of 2026-11-20:** D3120 to D3131, **every one as recommended**; none is marked
"not the recommendation" and none was "your call". **Two are marked amended after review, D3129
and D3130.** Each was first asked on a wrong reading of other code or another plan, review found
it, the question was asked again the same day, and the owner's last answer is the one recorded:
D3129 was asked three times (rounds 1 and 2 each corrected what of batch-run-identity depends on
its C1), D3130 twice (round 1 found the roles-and-scopes launch check). D3131 was added by round
2 and answered. Two answers reach outside this folder: **D3121 amends the rule** that the
credentials service account's only grant is its database role, and **D3129 holds** everything in
batch-run-identity that touches its key or its C1 until this plan's LM19, **which puts on hold
the go the owner gave that plan's C1 and BR6**.

REVIEW_ROUNDS
