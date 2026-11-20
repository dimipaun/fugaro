# A run on Cloud Batch gets its identity

**Status: plan only; nothing is implemented; four tasks approved, none launched.** The owner
approved C1, BRS, BR0 and BR6 by name on 2026-11-20; every other task still needs his go. BR6
changed after its approval (D3110), and he confirmed the approval for its present text. See
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
| [reconciliation.md](reconciliation.md) | The amended spec: the module map, assumptions A1–A5, findings F1–F33, the threat model of each carrier, decisions D3100–D3112 with the owner's answers, resolutions R1–R17, open items O1–O20, UNVERIFIED U1–U9. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1, tasks BRS and BR0 to BR22, the contract change that carries the owner's answer after Check 830, integration I1 (27 runs), and one contract change that runs only if raised; levels, critical path, shared files; Checks 830 and 831 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3100–D3119** (D3100–D3112 used), checks **830–839** (830 and
831 used). **Renumbered on 2026-11-20.** This spec first took D3060–D3079 and checks 810–819,
which were free on `main` and in every open pull request when it started; pull request #3315
(credentials-belong-to-models) took the same block and was opened first, so this one moved. The
owner was asked every question under the old numbers: D3060 to D3070 then are D3100 to D3110
now, and Checks 810 and 811 are 830 and 831. Read again at the renumbering: D3080–D3099 and
checks 820–829 are location-preference's, on `main`; no decision at or above D3100 and no check
at or above 830 is on `main` or in an open pull request.

**Owner answers of 2026-11-20**, all as recommended: D3100 finish the backend; D3101 the
VM-attested release; D3102 the release lives in the credentials service, which **amends the
standing rule** that the service account's only project-level grant is the database role; D3103
Batch learns shared project jobs; D3104 a Batch run's credentials come from the service alone. The
owner also approved the six-module map.

**Review round 1** found that every launcher can already mint a run's identity (F24), which the
first asking of D3101 did not say. The owner was asked again and **kept the VM-attested release**
(as recommended). It added D3105 (launchers get `actAs` on shared project job accounts where
Batch is provisioned: accepted, as recommended) and D3106 (the reaper ends a sealed run that
queues past its token's life: as recommended). **D3102 is amended after review**: it was asked as
"one read-only permission", and the code shows the service needs two (F26); that awaits the
owner's confirmation (O1). The round also added BR14, four guards on the release (R1), and moved
BR5 behind Check 830.

**Review round 2** found the owner's earlier ruling D2075 (Batch keeps mounted workflow secrets,
and no Batch caller class in the service), which D3104 and D3101 reverse. Asked, the owner ruled
that **D3104 supersedes D2075** (D3107, as recommended). It found that a second launcher could
replace a queued job and receive its key; the owner chose to close it by having the launcher
register the job (D3108, as recommended). **D3102 was asked again**: the second read is
decided after Check 830 (as recommended). The round split BR6, added BR15 to BR17, made the
contract change a task that always runs, and rebased onto a `main` that had moved.

**Review round 3** changed no answer and was the last. It fixed how the registration is stored
(one entry per digest, written once), named the launcher class as its only caller, and added the
pinned tests and tables three tasks would have broken. Its fixes are the last commit and were
not reviewed again. It left two questions nobody had asked the owner.

**After the pull request was opened** the owner answered them, the same day. D3109: the
registration is for people on the launcher roster only, so a service account cannot launch on
Batch (as recommended). **D3110 is not the recommendation**: `fugaro update` applies the compute
root only where Batch is selected, where applying it wherever a `batch:` block exists was
recommended; BR18 was added so that selecting the backend does not wait for a key only the
update creates. He also had AB9's text in always-a-budget amended here to name this plan, and
chose to record the minting question (O6) without a spec.

**A fourth review, at the owner's request,** read those changes and found that D3110 as answered
could not work: the trigger was a per-machine setting that is never published, and selecting
the backend deadlocked against the apply it was meant to cause (F32). **D3110 is amended after
review**: asked again, the owner chose a published opt-in fact (as recommended), which keeps his
intent that an installation gets no key ring until someone chooses Batch. BR19 was added, BR6
now depends on C1 and moved to level 1, and the spec was renumbered (see Numbers reserved). The
fixes for that review were not reviewed again at that point.

**After it** the owner confirmed BR6 and answered O16. **D3111 is not the recommendation**: an
operator can opt the installation out, where an opt-in that is never withdrawn was recommended.
The update then removes the Batch grants and keeps the key (R16); BR20 and BR21 were added. He
asked for one more independent review of everything since the fourth, and chose that nothing is
launched from this session.

**That fifth review** reported a hold. Its main claim, that any launcher can write the published
block and so forge the opt-in, was checked against the grants and does not hold: only an
operator (the role `admin`) can write the shared file (F33). Five smaller findings did hold and
are fixed: the merge now lets no machine's copy override the published fact (R15), the retiring
update keeps the custom role and waits for a Batch run in flight (R16), the key's grant gets a
removal class (BR5), and the claim that launches are refused "from the withdrawal on" was wrong
for a machine with a local config. That last one went to the owner as D3112, answered as
recommended: the credentials service refuses the registration where Batch is off (R17, BR22).
These fixes were not reviewed again. O18 and O20 are left open.

**What the reconciliation found that the brief did not know:** the token's carrier is one of seven
gaps (F7 to F13); any carrier the run's service account opens can be replayed by a second
launcher (F4); and the chosen design rests on one platform fact that only a live probe can settle
(U1, Check 830).

**What a reader should not take from here:** only C1, BRS, BR0 and BR6 are approved; every other
task needs the owner's go by name. Nothing is launched. Checks 830 and 831 are owner-run; BR3, BR5 and BR12 do not start before the
owner has run Check 830 and answered O1. No CLI release is cut between the merge of BR1, BR6,
BR16 or BR18 and I1.
