# Every model key through the credentials service

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-19. Checked against `origin/main` at `45658d8a8`.

The owner's ruling D2975: every model credential a run uses is served by the credentials service,
and no model key is mounted in a job. It finishes what the credentials service and secrets
brokering started, for the keys still on the runner path (provider keys such as OpenRouter's), the
budget-off mode and the mixed stage account, and it removes the Terraform that mounts a key.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's ruling; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, findings F1–F28, decisions D3000–D3007 with the owner's answers, resolutions R1–R12, open items O1–O7, UNVERIFIED U1–U5. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks KS1–KS18, integration I1 (20 runs over two releases, and one contract change that runs only if raised); levels, critical path, shared files; Checks 50 and 780–782 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3000–D3019** (D3000–D3007 used), checks **780–789** (780–782
used). Read on 2026-11-19: the highest decision on `main` and in the open pull requests was D2992,
the highest check 773 with 774–779 reserved.

**Owner answers of 2026-11-19:** D3000–D3004 and D3006 as recommended. **D3005 is not the
recommendation**: an old key secret is deleted once copied, which reverses the owner's ruling of
2026-11-06 for that class of secret. **D3007 is the owner's own answer**: no repository tie-in to
keys; this plan does the part of it that touches provider keys, and the rest is an open item (O4).
D3003–D3007 were added by review round 1. **D3000 replaces an answer the owner gave
earlier the same day** (every provider key behind Check 50), after the code showed the gate is
wider than the decision it implements. **It reverses D1634 and D1670** by the owner's ruling D2975.

**What a reader should not take from here:** no task is approved; each needs the owner's go by name.
Release B (KS13 to KS16) is not launched before release A is cut, and KS16 also waits on secrets
brokering's B5.7, which is not started. Checks 50 and 780–782 are owner-run.
