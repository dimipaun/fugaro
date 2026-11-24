# Credentials belong to models, not repositories

**Superseded 2026-11-21 by [model-credentials-routing](../model-credentials-routing/README.md): every task not merged is withdrawn there and carried by a task of it, except CM2, which is not replaced (CM1, CM3, CM22 and CM32 had merged); no task of this plan is launched.** Former status: plan only; nothing is implemented; no task approved. See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `d57d71ddb`.

A repository has no auth mode. `agent.auth` is retired: a credential is declared once in the
project, a craft's access names the credential or pool its model calls go out on, and an
unwritten access means the first declared credential that reaches the model. Vertex is used only
where it is named. It carries out D3007
of [every model key through the credentials service](../keys-through-service/README.md) and D3026
of always a budget, and is the third spec of that series.

**Superseded in part on 2026-11-22 by [model credentials routing](../model-credentials-routing/README.md)
(D3170).** That plan sets how a model is reached once, in the project's routing table, and its
table supersedes this plan's craft access: the credential stops being a craft's. **Every task of
this plan that is not merged is withdrawn**, carried by a task of that plan except CM2
(per-repository crafts), which is not replaced: one table per project, and that plan's RT2
carries admission. **Contract C1 is withdrawn, its draft pull request #3460 still open and to be
closed unmerged**, re-cut as that plan's C1. What is merged is kept — CM1, CM3, CM22, CM32 and CM35
— with this plan's two-release removal of `agent.auth` (R3) and its rule that an unmigrated
repository runs as its file says. The withdrawn tasks are stopped by the owner by hand before any
launches (that plan's O1). The task-by-task
table follows; the graph marks the withdrawn nodes; this folder's own `docs/status.md` row is not
touched by the amending pull request.

| Earlier task | State | Carried by, in model credentials routing |
| --- | --- | --- |
| CM35, CM1, CM3, CM22, CM32 | merged | kept, as merged |
| C1 (the access contract) | draft #3460 | withdrawn; its C1 |
| CM2 (per-repository crafts) | not started | withdrawn, not replaced: one table per project, and its RT2 carries admission |
| CM4, CM19 | not started | MG1, MG2 |
| CM5, CM31 | not started | RT4 and MG3; X4 |
| CM6, CM36 | not started | CS1, PL3 |
| CM7 | not started | CS2 |
| CM8 | not started | CS6, CS13 |
| CM9, CM11 | not started | CS7 |
| CM10 | not started | CS8 |
| CM12 | not started | CS9 |
| CM13 | not started | CS11 |
| CM14, CM15 | not started | CS10, CS12, CS14, CS15, RT7 |
| CM16 | not started | RT3 |
| CM17, CM18 | not started | RT6, RT7 |
| CM20 | not started | MG5 |
| CM21 | not started | VS1 |
| CM23 | not started | MG6 |
| CM34 | not started | MG4 |
| I1 | not started | I1 |
| CM24 to CM27 | not started | DOC1 to DOC4 |
| CM28, CM29, CM30, CM33 | not started | X1, X2, X3, X5; its X6 to X8 are new |
| Checks 810 and 811 | scripts merged, not run | replaced by its Checks 884 and 885; its DOC2 marks them so in the live checklist |

**Decisions the amendment reverses or amends.** D3060 (a craft's access names the credential) is
superseded by D3170, the table superseding craft access, and D3171 keeps one chain per model;
D3064 (the cascade's pool, marked as the cascade's) is reversed by D3183 — the existing accounts
become the shared pool, with the same members and order; D3066 (Vertex on the job's identity
through the gateway) is amended by D3174 — the credentials service calls Vertex on its own
identity, capped as far as a key is and no further; D3068 (per-stage accounts become their craft's
access) is reversed by D3171 — per-stage accounts and the mix are retired, and `update` folds them
into one pool; D3069 (`fugaro run --token` stays) is reversed by D3176 — the flag is rejected with
a fix naming `--pool`. D3061's intent is kept: Vertex is reached only where a chain names it, and
nothing is implied. D3063 and D3072 (the retired keys, read and named for one release and then
rejected with no version bump) stand.

**The keys plan's KS13 now waits on that plan's I1**, not on this plan's (that plan's A1, D3170).

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's rulings, quoting his words; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, findings F1–F50 (F1 is the census of every reader, F3 of every wire format), decisions D3060–D3073 with the owner's answers, resolutions R1–R15, open items O1–O15, UNVERIFIED U1–U8, and the amendment of 2026-11-22 (D3170) with the task-by-task table of what it withdraws. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks CM1 to CM36, integration I1 (38 runs over two releases, and one contract change that runs only if raised); levels, critical path, shared files; Checks 810 and 811. Every task but CM1, CM3, CM22, CM32 and CM35 is marked withdrawn by the 2026-11-22 amendment above, and its section is kept as the record of what was withdrawn and what carries it |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it, with each withdrawn task's node marked in its title |

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

**And, from 2026-11-22:** the tasks and levels below are the record of a plan that
[model credentials routing](../model-credentials-routing/README.md) supersedes in part. Every task
but CM1, CM3, CM22, CM32 and CM35 is withdrawn (D3170), so the levels, the critical path, the
contract change of §7 and Checks 810 and 811 are no longer a launch order — the owner stops the
withdrawn tasks by hand, and that plan carries the work.
