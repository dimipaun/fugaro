# Credentials belong to models, not repositories

**Superseded 2026-11-21 by [model-credentials-routing](../model-credentials-routing/README.md): every task not merged is withdrawn and carried there (CM1, CM3, CM22 and CM32 had merged); no task of this plan is launched.** Former status: plan only; nothing is implemented; no task approved. See
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
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, findings F1–F50 (F1 is the census of every reader, F3 of every wire format), decisions D3060–D3073 with the owner's answers, resolutions R1–R15, open items O1–O13, UNVERIFIED U1–U8. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks CM1 to CM36, integration I1 (38 runs over two releases, and one contract change that runs only if raised); levels, critical path, shared files; Checks 810 and 811 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3060–D3079** (D3060–D3073 used), checks **810–819** (810 and
811 used). Read twice on 2026-11-20. The first reading found D3040–D3059 and 800–809 free, and
the decisions were asked under those numbers; before this branch was pushed, pull request #3308
(ci-affected-tests, opened at 15:54 UTC) had reserved exactly those blocks, so every number here
was moved up by one block: **the owner answered D3060–D3073 as D3040–D3053, in the same order.**
The second reading: the highest decision on `main` was D3029 (always a budget, D3020–D3039), the
keys plan holds D3000–D3019, #3308 holds D3040–D3059 and checks 800–809, and no diff of the open
pull requests wrote a decision at or above D3060 or a check at or above 810.

**The two plans it follows, and what was read.** The keys plan is merged (#3299, `b5560a075`) and
was read on `main`. Always a budget was not merged when this was written: pull request #3302,
branch `docs/always-a-budget`, was read at commit `8bf13231f`. It merged the same day (`c5310b9a5`),
and gained D3027 to D3029, which change nothing here.

**Main moved under this branch.** Every code claim was checked at `d57d71ddb`. The branch was then
rebased onto `0a8b4e221`, where three things had landed: always a budget's documents, the keys
plan's contract C1 (#3303, so that plan has started and CM35 is urgent), and workspace service's
WS1 (#3280), which edits `internal/credsvc`. The symbols this plan cites were not read again one
by one at the new commit (O14).

**Owner answers of 2026-11-20:** D3060–D3073, every one as recommended when first asked; two
were amended afterwards (D3061, D3064) and one made precise by review (D3063), with two readings of his answers the reviews added (F38,
F46). **He confirmed all three, and the provider-terms question U1, on 2026-11-20** (O10). **D3061 was then amended
by the owner**, in his own words: "I don't like the idea of falling through to vertex, it will
result in unexpected charges. User should explicitly specify it like 'vertex' or 'native', wdyt?" Vertex is never implied, and an
unset access is one credential, not a chain. Four answers change an earlier ruling or rule, and
say so in their row: D3060 reverses model crafts' F24, D3061 reverses D1624, D3065 retires the consent switch O2, and D3072 sets the rule that a removed field bumps
its format's version aside for `fugaro.yaml`.

**Review round 1** found that a pool does not serve what the cascade serves (F24), so **D3064 is
amended after review**, by the owner's answer to the question it raised: the pool `update` writes
is marked as the cascade's and keeps serving its subscriptions. It also added F25 to F32, the
tasks CM32 and CM33, and the rule that an unmigrated repository runs as its file says (R3).

**Review round 2** found that no job image reads the per-repository policy node (F33), that a
record of what one run resolves to would refuse runs the plan keeps (F34), that the keys plan's
KS13 waits on tasks this plan withdraws (F37) and that a model grade is a second place a file
names a credential (F38). It rewrote R4 and R15, added CM34 and CM35, and changed no answer.

**Review round 3** found that a call with no header went round the new record (F42), that the
record as round 2 left it would let the service move from a subscription to a paid key, against
D3061 (F43), that an older CLI also reads the per-repository node strictly (F47) and that a run
could still land on Vertex because its file names nothing (F49). It rewrote R4 and R15 once more,
split CM32 into CM32 and CM36, and changed no answer. **Its fixes are the last commit and were
not reviewed again** (O12).

**What it changes in the two plans before it** (reconciliation F19 to F22): the keys plan's KS6,
KS9 and KS10 are replaced by tasks of this plan, and its KS3, KS13 and KS19 are amended; always a
budget's AB1b and AB15 land first and are then built on. Neither plan's own documents are edited
by this pull request: the keys plan is amended by this plan's first task, CM35 (O4, O11).

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. Nothing launches before always a budget's integration and the keys plan's serving tasks have
merged; CM35 is first, and waits on nothing. Release 2 (CM28 to CM31 and CM33) is not launched before
release 1 is cut, and CM28 not before the keys plan's KS13. Checks 810 and 811 are owner-run.
