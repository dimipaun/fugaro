# Every model key through the credentials service

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-19, amended 2026-11-20. Checked against `origin/main` at `45658d8a8`; the symbols review
round 2 named were read again at `39344123a`.

The owner's ruling D2975: every model credential a run uses is served by the credentials service,
and no model key is mounted in a job. It finishes what the credentials service and secrets
brokering started, for the keys still on the runner path (provider keys such as OpenRouter's) and
the mixed stage account, and it removes the Terraform that mounts a key. Budget mode off, the one
path where a key must sit in the job, is removed under a spec of its own (D3009, D3011).

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's ruling; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, findings F1–F47, decisions D3000–D3015 with the owner's answers, resolutions R1–R13, open items O1–O12, UNVERIFIED U1–U7. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks KS1–KS20 (KS7 and KS8 dropped, their numbers not reused), integration I1 (20 runs over two releases, and one contract change that runs only if raised); levels, critical path, shared files; Checks 50, 780 and 781 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3000–D3019** (D3000–D3015 used), checks **780–789** (780 and
781 used; 782 withdrawn and not reused). Read on 2026-11-19: the highest decision on `main` and in the open pull requests was D2992,
the highest check 773 with 774–779 reserved.

**Owner answers of 2026-11-19:** D3000–D3004 and D3006 as recommended; D3002 and D3004 were later
withdrawn by D3009. **D3005 is not the
recommendation**: an old key secret is deleted once copied, which reverses the owner's ruling of
2026-11-06 for that class of secret. **D3007 is the owner's own answer**: no repository tie-in to
keys; this plan does the part of it that touches provider keys, and the rest is an open item (O4).
D3003–D3007 were added by review round 1. **D3000 replaces an answer the owner gave
earlier the same day** (every provider key behind Check 50), after the code showed the gate is
wider than the decision it implements. **It reverses D1634 and D1670** by the owner's ruling D2975.

**Review round 2** added D3008 to D3012. D3008 (one bundle entry per repository), D3010 (`observe`
stays), D3011 (removing budget mode off is its own spec) and D3012 (an update stops before it
would take a last mount, answered 2026-11-20) are as recommended. **D3009 is the owner's own
answer and withdraws D3002 and D3004**: "We should always have a budget. If anyone doesn't care,
they can set a high number." Tasks KS7 and KS8 and Check 782 went with them.

**Review round 3** added D3013 to D3015, answered 2026-11-20. **D3013 is not the recommendation**:
the owner runs Check 50 before release A is cut, because a Claude model through a provider works
today and release A would stop it. D3014 (the copy follows a newer version and names it) and D3015
(an account's token stays per account, which narrows D3008) are as recommended. Round 3's fixes
are the last commit and were not reviewed again.

**What a reader should not take from here:** no task is approved; each needs the owner's go by name.
Release A is not cut before the owner has run Check 50 and KS20 has merged. Release B (KS13,
KS11, KS14 to KS16) is not launched before release A is cut. KS14 and KS15 also
wait on the plan that removes budget mode off, which is not written yet, and KS16 on secrets
brokering's B5.7, which is not started. Checks 50, 780 and 781 are owner-run.
