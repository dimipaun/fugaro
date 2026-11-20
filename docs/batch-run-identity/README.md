# A run on Cloud Batch gets its identity

**Status: plan only; nothing is implemented; four tasks approved, none launched.** The owner
approved C1, BRS, BR0 and BR6 by name on 2026-11-20; every other task still needs his go. See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-20. Checked against `origin/main` at `d57d71ddb`, rebased onto `0a8b4e221`.

A run launched on Cloud Batch receives the key to its sealed budget token from the credentials
service, which releases it only to that run's own VM. The plan also closes the other gaps that
stop a Batch run from completing, because the owner chose to finish the backend rather than
remove it. It lifts the refusal that D3021 of [always a budget](../always-a-budget/README.md)
adds; that plan was read on branch `docs/always-a-budget` at `8bf13231f` and has since merged
(#3302, #3305), with its tasks approved and launching.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's rulings; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: the module map, assumptions A1–A5, findings F1–F31, the threat model of each carrier, decisions D3060–D3070 with the owner's answers, resolutions R1–R14, open items O1–O14, UNVERIFIED U1–U8. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks BRS and BR0 to BR18, the contract change that carries the owner's answer after Check 810, integration I1 (23 runs), and one contract change that runs only if raised; levels, critical path, shared files; Checks 810 and 811 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3060–D3079** (D3060–D3070 used), checks **810–819** (810 and
811 used). Read on 2026-11-20 against `main` and every open pull request: the highest decision on
`main` was D3026 (the keys plan's documents cite it), with D3020–D3039 held by always-a-budget
(then pull request #3302) and D3040–D3059 with checks 800–809 set aside for the "credentials belong to models" spec, which
has no branch or pull request yet; the highest check on `main` was 782, and always-a-budget holds 790–799.

**Owner answers of 2026-11-20**, all as recommended: D3060 finish the backend; D3061 the
VM-attested release; D3062 the release lives in the credentials service, which **amends the
standing rule** that the service account's only project-level grant is the database role; D3063
Batch learns shared project jobs; D3064 a Batch run's credentials come from the service alone. The
owner also approved the six-module map.

**Review round 1** found that every launcher can already mint a run's identity (F24), which the
first asking of D3061 did not say. The owner was asked again and **kept the VM-attested release**
(as recommended). It added D3065 (launchers get `actAs` on shared project job accounts where
Batch is provisioned: accepted, as recommended) and D3066 (the reaper ends a sealed run that
queues past its token's life: as recommended). **D3062 is amended after review**: it was asked as
"one read-only permission", and the code shows the service needs two (F26); that awaits the
owner's confirmation (O1). The round also added BR14, four guards on the release (R1), and moved
BR5 behind Check 810.

**Review round 2** found the owner's earlier ruling D2075 (Batch keeps mounted workflow secrets,
and no Batch caller class in the service), which D3064 and D3061 reverse. Asked, the owner ruled
that **D3064 supersedes D2075** (D3067, as recommended). It found that a second launcher could
replace a queued job and receive its key; the owner chose to close it by having the launcher
register the job (D3068, as recommended). **D3062 was asked again**: the second read is
decided after Check 810 (as recommended). The round split BR6, added BR15 to BR17, made the
contract change a task that always runs, and rebased onto a `main` that had moved.

**Review round 3** changed no answer and was the last. It fixed how the registration is stored
(one entry per digest, written once), named the launcher class as its only caller, and added the
pinned tests and tables three tasks would have broken. Its fixes are the last commit and were
not reviewed again. It left two questions nobody had asked the owner.

**After the pull request was opened** the owner answered them, the same day. D3069: the
registration is for people on the launcher roster only, so a service account cannot launch on
Batch (as recommended). **D3070 is not the recommendation**: `fugaro update` applies the compute
root only where Batch is selected, where applying it wherever a `batch:` block exists was
recommended; BR18 was added so that selecting the backend does not wait for a key only the
update creates. He also had AB9's text in always-a-budget amended here to name this plan, and
chose to record the minting question (O6) without a spec. These changes were not reviewed by an
independent reviewer.

**What the reconciliation found that the brief did not know:** the token's carrier is one of seven
gaps (F7 to F13); any carrier the run's service account opens can be replayed by a second
launcher (F4); and the chosen design rests on one platform fact that only a live probe can settle
(U1, Check 810).

**What a reader should not take from here:** only C1, BRS, BR0 and BR6 are approved; every other
task needs the owner's go by name. Nothing is launched. Checks 810 and 811 are owner-run; BR3, BR5 and BR12 do not start before the
owner has run Check 810 and answered O1. No CLI release is cut between the merge of BR1 or BR16
and I1.
