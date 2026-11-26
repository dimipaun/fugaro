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
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, findings F1–F51, decisions D3000–D3015 with the owner's answers, resolutions R1–R13, open items O1–O13, UNVERIFIED U1–U7. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks KS1–KS20 (KS6, KS9 and KS10 withdrawn, KS7 and KS8 dropped; their numbers not reused), integration I1 (17 runs over two releases, and one contract change that runs only if raised); levels, critical path, shared files; Checks 50, 780 and 781 |
| [contract-provider.md](contract-provider.md) | C1's prose half: what a routed call carries, the two refusals, the bundle's family for a named provider entry and its naming rule, the accessor, R12's fact and where it is recorded (the second fact C1 named is not needed, F50). The Go is `internal/credsvc/provider_wire.go`, `internal/gateway/providerwire.go` and `internal/config/provider_served.go` |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Amended 2026-11-20 by [credentials belong to models](../credentials-belong-to-models/README.md).**
That plan retires `agent.auth`, and the tasks here that admitted a provider pin by a repository's
auth mode or served a stage account beside one have no auth mode left to work in. **KS6, KS9 and
KS10 are withdrawn**, and what they planned is that plan's: **KS6 by its CM16 and CM15** (a model
judged by what reaches it, and the launch refusal KS6 planned), **KS9 by its CM6** (the service
narrows by the repository's access record) and **KS10 by its CM9** (a subscription token leaves
the agent's environment for every subscription, not only a stage's). **KS13 waits on that plan's
I1 in their place** (its F37, F50), so keys release B cannot drop a mount before that plan's
release 1 is integrated. **Amended 2026-11-22:** credentials belong to models is itself superseded
in part by [model credentials routing](../model-credentials-routing/README.md) (its D3170), whose
table supersedes that plan's craft access and which withdraws every task of it that is not merged
— including the CM6, CM9, CM15 and CM16 that KS6, KS9 and KS10 were replaced by. **KS13 now waits
on model credentials routing's I1** (its A1, the task that says so here). **C1's second fact, "the stage account is served", is not needed** (its
F20): one fact per repository, "its credentials are served", covers it. **KS3's clause that offers
a provider credential "under every auth the service serves" is CM6's** (its F21); KS3 keeps the
narrowed Check 50 gate and the per-repository Anthropic entry. The owner stopped KS6, KS9 and KS10
on 2026-11-20 (that plan's O11), so none of them runs meanwhile; this folder says so from here on,
in its reconciliation (F48 to F51) and its plan.

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

**Review round 3** added D3013 to D3015, answered 2026-11-20. **D3013 is not the recommendation,
and the owner amended it on 2026-11-26**: release A ships with the refusal standing and Check 50
not run, because a Claude model through a provider works today and release A stops it, and KS20
names every repository the gate stops. D3014 (the copy follows a newer version and names it) and D3015
(an account's token stays per account, which narrows D3008) are as recommended. Round 3's fixes
are the last commit and were not reviewed again.

**What a reader should not take from here:** no task is approved; each needs the owner's go by name.
Release A ships with Check 50's result not in: KS3's refusal stands and KS20 names the repositories
it stops (the owner's ruling of 2026-11-26). Release B (KS13,
KS11, KS14 to KS16) is not launched before release A is cut, and KS13 not before model
credentials routing's I1 has merged (2026-11-22; credentials belong to models' own I1 before it). KS14 and KS15 also
wait on the plan that removes budget mode off, which is not written yet, and KS16 on secrets
brokering's B5.7, which is not started. Checks 50, 780 and 781 are owner-run.
