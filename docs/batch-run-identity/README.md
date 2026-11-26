# A run on Cloud Batch gets its identity

**Status: amended on 2026-11-21 by LM19 of
[launcher-does-not-mint](../launcher-does-not-mint/README.md); plan only, and nothing here is
approved.** The release this plan was written around — the key to a sealed budget token — has been
re-planned as a **VM-attested claim** on that plan's contract C1, which is merged:
`internal/identitywire` and its `contract.md`. **C1's and BR6's approvals of 2026-11-20 are on
hold**, by that plan's D3129 (its O16); BRS and BR0 keep theirs: a re-cut contract is one nobody
approved, and every amended answer is put to him again — the list is "Answers amended by LM19" in
[reconciliation.md](reconciliation.md). Three tasks merged while this document was being written
(C1 #3380, BRS #3379, BR0 #3381) and the key-release half of C1 is what BR23 now retires. See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-21. **Check 830 has been run once, on 2026-11-25, and failed** (the owner's block is in
[docs/gcp-live-checklist.md](../gcp-live-checklist.md)'s Check 830 section, and what it settles is
below): O1 and O8 are answered, U8 is 10 s, and U2 and U3 still wait on the rerun.

A run launched on Cloud Batch no longer opens anything: the launcher asks the issuer for a ticket
that names the Batch job it is about to create, the job carries that ticket as
`FUGARO_IDENTITY_TICKET`, and the runner claims its identity exactly as a Cloud Run run does — with
one thing more, its own VM's identity token, which the service accepts only from a VM of the job
the record names. The plan also closes the other gaps that stop a Batch run from completing,
because the owner chose to finish the backend rather than remove it. It lifts the refusal that
D3021 of [always a budget](../always-a-budget/README.md) adds; that plan was read on branch
`docs/always-a-budget` at `8bf13231f` and has since merged (#3302, #3305), with its tasks approved
and launching.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's rulings; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: the amendment's answer by answer, the F18 task-by-task table, the module map, assumptions A1–A5, findings F1–F38, the threat model of each carrier, decisions D3100–D3115 with the owner's answers and each amendment marked, resolutions R1–R21, open items O1–O22, UNVERIFIED U1–U9. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contract C1 (re-cut as the claim's attestation and the registration), tasks BRS and BR0 to BR23, the contract change that carries the owner's answer after Check 830, integration I1 (**28 runs**), and one contract change that runs only if raised; levels, critical path, shared files; Checks 830 and 831 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |
| [contract-batch-claim.md](contract-batch-claim.md) | C1's prose half, written by C1: the ticket's job field, the claim's attestation field, the registration route and the record's two job fields. The Go is `internal/identitywire/batchclaim.go`, `internal/identitywire/wire.go` and `internal/credsvc/identity_config.go` |
| [contract-key-release.md](contract-key-release.md) | **Retired, and BR23 deletes it**: the wrapped blob's envelope, the three seal-key paths, `JobLink`, `KeyOpener` and `Registry`, the `batch.seal_key.*` key fact. It is merged on `main` (#3380) and no acceptance of this plan reads it |

**Numbers reserved:** decisions **D3100–D3119** (D3100–D3115 used), checks **830–839** (830 and
831 used). **Renumbered on 2026-11-20.** This spec first took D3060–D3079 and checks 810–819,
which were free on `main` and in every open pull request when it started; pull request #3315
(credentials-belong-to-models) took the same block and was opened first, so this one moved. The
owner was asked every question under the old numbers: D3060 to D3070 then are D3100 to D3110
now, and Checks 810 and 811 are 830 and 831. Read again at the renumbering: D3080–D3099 and
checks 820–829 are location-preference's, on `main`; no decision at or above D3100 and no check
at or above 830 is on `main` or in an open pull request.

**Owner answers of 2026-11-20**, all as recommended: D3100 finish the backend; D3101 the
VM-attested carrier; D3102 the carrier lives in the credentials service, which **amends the
standing rule** that the service account's only project-level grant is the database role; D3103
Batch learns shared project jobs; D3104 a Batch run's credentials come from the service alone. The
owner also approved the six-module map.

**Review round 1** found that every launcher can already mint a run's identity (F24), which the
first asking of D3101 did not say. The owner was asked again and **kept the VM-attested carrier**
(as recommended). It added D3105 (launchers get `actAs` on shared project job accounts where
Batch is provisioned: accepted, as recommended) and D3106 (the reaper ends a run that queues past
its ticket's life: as recommended). **D3102 is amended after review**: it was asked as
"one read-only permission", and the code shows the service needs two (F26); that awaits the
owner's confirmation (O1). The round also added BR14, four guards on the carrier (R1), and moved
BR5 behind Check 830.

**Review round 2** found the owner's earlier ruling D2075 (Batch keeps mounted workflow secrets,
and no Batch caller class in the service), which D3104 and D3101 reverse. Asked, the owner ruled
that **D3104 supersedes D2075** (D3107, as recommended). It found that a second launcher could
replace a queued job and receive its key; the owner chose to close it by having the launcher
register the job (D3108, as recommended) — under the amendment that registration carries the job's
id, which is what refuses a re-created job. **D3102 was asked again**: the second read is
decided after Check 830 (as recommended). The round split BR6, added BR15 to BR17, made the
contract change a task that always runs, and rebased onto a `main` that had moved.

**Review round 3** changed no answer and was the last. It fixed how the registration is stored
(one entry per digest, written once), named the launcher class as its only caller, and added the
pinned tests and tables three tasks would have broken. Its fixes are the last commit and were
not reviewed again. It left two questions nobody had asked the owner.

**After the pull request was opened** the owner answered them, the same day. D3109: the
registration is for people on the launcher roster only, so a service account cannot launch on
Batch (as recommended) — **superseded by the amendment**, which admits a roster service account as
a launcher (D3123, D3137, R20). **D3110 is not the recommendation**: `fugaro update` applies the
compute root only where Batch is selected, where applying it wherever a `batch:` block exists was
recommended; BR18 was added so that selecting the backend does not wait for a key only the
update creates. He also had AB9's text in always-a-budget amended here to name this plan, and
chose to record the minting question (O6) without a spec. **The amendment re-cuts D3110**: the
apply turns on the published opt-in fact, whatever the updating machine selects (R2).

**A fourth review, at the owner's request,** read those changes and found that D3110 as answered
could not work: the trigger was a per-machine setting that is never published, and selecting
the backend deadlocked against the apply it was meant to cause (F32). **D3110 is amended after
review**: asked again, the owner chose a published opt-in fact (as recommended). BR19 was added,
BR6 now depends on C1 and moved to level 1, and the spec was renumbered (see Numbers reserved). The
fixes for that review were not reviewed again at that point.

**After it** the owner confirmed BR6 and answered O16. **D3111 is not the recommendation**: an
operator can opt the installation out, where an opt-in that is never withdrawn was recommended.
The update then removes the Batch grants (R16); BR20 and BR21 were added. He asked for one more
independent review of everything since the fourth, and chose that nothing is launched from this
session.

**That fifth review** reported a hold. Its main claim, that any launcher can write the published
block and so forge the opt-in, was checked against the grants and does not hold: only an
operator (the role `admin`) can write the shared file (F33). Five smaller findings did hold and
are fixed: the merge now lets no machine's copy override the published fact (R15), the retiring
update keeps the custom role and waits for a Batch run in flight (R16), the grant gets a
removal class (BR5), and the claim that launches are refused "from the withdrawal on" was wrong
for a machine with a local config. That last one went to the owner as D3112, answered as
recommended: the credentials service refuses the ticket and the registration where Batch is off
(R17, BR22). These fixes were not reviewed again, and the owner chose no further review. He
accepted O18 as stated (as recommended), approved no further task, and left the pull request open
to read.

**The amendment of 2026-11-21** is the launcher plan's LM19, and it is the first thing a reader
should know: the launcher plan's contract C1 merged (#3380) and its issuer **never mints**, so a
carrier in which this plan's service *releases* a key to a VM is a second minting path rather than
the same one. The carrier is now the attestation the VM itself presents (D3101, D3114): the ticket
names the job, the claim carries the VM's identity token, the record carries the job's id, and the
key fact, the wrapped envelope, the release route and the KMS key go with the old carrier (BR23,
R21). What of the merged C1 stays: the opt-in fact and its three states, the deploy input, the
job-name shape, the block's validation entry points, the classes, the update stages and
doctor/validate. D3108 keeps its object and gains the id; D3109 is settled against that plan's
D3123 and D3137; D3102, D3106, D3110 and D3112 are re-cut; D3113, D3114 and D3115 are new and
unanswered. **Every amended answer is marked in [reconciliation.md](reconciliation.md) and put to
the owner again.**

**What the reconciliation found that the brief did not know:** the token's carrier is one of seven
gaps (F7 to F13); any carrier the run's service account opens can be replayed by a second
launcher (F4), which is why the claim is bound to the VM's own job and its id rather than to
anything the ticket carries (F35); and the chosen design rests on one platform fact that only a
live probe can settle (U1, Check 830).

**What a reader should not take from here:** nothing is approved — the approvals of 2026-11-20 are
on hold until the owner answers D3113 to D3115 and the amended answers, and no task has his go by
name under the amended text. Nothing is launched. Checks 830 and 831 are owner-run; **the owner ran
Check 830 once, on 2026-11-25, and it FAILED** — `probe-ran` left no line of the probe container's
own, so **U2 and U3 are unmeasured**, and `u1-handmade` found that a hand-made VM can carry every
field the probe's VM carries, so **O1 is answered** (the link is a field of the instance's own
object, read with `compute.instances.get`) and **R4 returns the design to the owner rather than to
K1**; **U8 is 10 s**. The check now reads a second channel for U2 and U3 and captures what a run
with no log line owes the reader, so the rerun answers them; **BR2, BR3, BR5, BR12, BR13 and BR23
still wait on that result**, and BR3 and BR5 wait on O1's answer with it. No CLI release is cut
between the merge of BR1, BR6, BR16, BR18 or
BR22 and I1, BR23's retirement included. The merged files of the old carrier
(`internal/budget/token/wrap_contract.go`, `internal/credsvc/sealkey_wire.go`,
`internal/credsvc/sealkey_stub.go`, `internal/localcfg/batch_sealkey.go`) are still in the tree, and
no acceptance of this plan depends on them.
