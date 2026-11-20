# Credentials belong to models, not repositories

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `d57d71ddb`.

A repository has no auth mode. `agent.auth` is retired: a credential is declared once in the
project, a craft's access names the credential or pool its model calls go out on, and an
unwritten access means the first declared credential that reaches the model. Vertex is used only
where it is named. It carries out D3007
of [every model key through the credentials service](../keys-through-service/README.md) and D3026
of always a budget, and is the third spec of that series.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's rulings, quoting his words; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, findings F1–F23 (F1 is the census of every reader, F3 of every wire format), decisions D3040–D3053 with the owner's answers, resolutions R1–R13, open items O1–O9, UNVERIFIED U1–U6. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks CM1 to CM31, integration I1 (33 runs over two releases, and one contract change that runs only if raised); levels, critical path, shared files; Checks 800 and 801 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3040–D3059** (D3040–D3053 used), checks **800–809** (800 and
801 used). Read on 2026-11-20: the highest decision on `main` was D3026, cited from the keys
plan, with D3000–D3019 that plan's block; D3020–D3039 are held by the always-a-budget pull request
(#3302, open); no diff of the 11 open pull requests wrote a decision at or above D3040. The
highest check on `main` was 782, in the keys plan's block 780–789, with 790–799 held by #3302; no
open pull request wrote a check at or above 800.

**The two plans it follows, and what was read.** The keys plan is merged (#3299, `b5560a075`) and
was read on `main`. Always a budget is not merged: pull request #3302, branch
`docs/always-a-budget`, was read at commit `8bf13231f`.

**Owner answers of 2026-11-20:** D3040–D3053, every one as recommended. **D3041 was then amended
by the owner**, in his own words: "I don't like the idea of falling through to vertex, it will
result in unexpected charges. User should explicitly specify it." Vertex is never implied, and an
unset access is one credential, not a chain. Four answers change an earlier ruling or rule, and
say so in their row: D3040 reverses model crafts' F24, D3041 reverses D1624, D3045 retires the consent switch O2, and D3052 sets the rule that a removed field bumps
its format's version aside for `fugaro.yaml`.

**What it changes in the two plans before it** (reconciliation F19 to F22): the keys plan's KS6,
KS9 and KS10 are replaced by tasks of this plan, and its KS3, KS13 and KS19 are amended; always a
budget's AB1b and AB15 land first and are then built on. Neither plan's own documents are edited
by this pull request (O4).

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. Nothing launches before always a budget's integration has merged and the keys plan's
documents say KS6, KS9 and KS10 are withdrawn. Release 2 (CM28 to CM31) is not launched before
release 1 is cut, and CM28 not before the keys plan's KS13. Checks 800 and 801 are owner-run.
