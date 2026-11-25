# A run on Cloud Batch gets its identity: implementation plan

**Status: plan only, and amended on 2026-11-21 by LM19 of
[launcher-does-not-mint](../launcher-does-not-mint/README.md): the release is re-planned as a
VM-attested claim on that plan's merged C1, so nothing here is approved and nothing is launched.**
Three tasks merged while this document was being written — C1 (#3380), BRS (#3379) and BR0
(#3381) — and the key-release half of C1 is what BR23 retires. **C1's and BR6's approvals of
2026-11-20 are on hold** by that plan's D3129 (its O16); BRS and BR0 keep theirs. A re-cut contract
is a contract nobody approved, D3113 to D3115 are unanswered, and **every amended answer is put to
him again** — the list is "Answers amended by LM19" in [reconciliation.md](reconciliation.md). It
builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins where the
two disagree.

**What the amendment changes, in one paragraph.** A launcher no longer mints and a Batch run no
longer opens anything: the launcher asks the issuer for a ticket and names the Batch job it is
about to create, the job carries that ticket as `FUGARO_IDENTITY_TICKET`, and the runner claims its
identity exactly as a Cloud Run run does — with one thing more, the **attestation**: its own VM's
identity token, which the service accepts only from a VM of the job the record names. The KMS key,
the wrapped blob, the release route, the key fact in the block and its settings entry all go with
the old carrier; the opt-in fact, the registration and Check 830 stay. The answers that shaped the
unamended plan are of 2026-11-20 (D3100 to D3104 as recommended): the backend is finished, not
removed; the carrier is the one that meets "no other launcher"; the service is the credentials
service; Batch learns shared project jobs; a Batch run's credentials come from the service alone.
Review round 1 added D3105 and D3106; review round 2 added D3107 and D3108; round 3's two questions
were answered as D3109 and D3110 (the second not the recommendation); the review the owner then
asked for found F32 and brought D3110's amendment and D3111 (not the recommendation); D3112
followed the fifth review. The amendment of 2026-11-21 leaves D3100, D3103, D3104, D3105, D3107,
D3111 as they were, re-cuts D3101, D3102, D3106, D3108, D3110 and D3112, settles D3109 against
D3123 and D3137 of the launcher plan, and adds D3113, D3114 and D3115.

It is **28 runs**: one contract, the contract change that always runs, 25 tasks and one integration.
Every one is a security path.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, the launcher plan's `contract.md` (C1, BR1, BR2, BR4, BR17),
`.claude/rules/credentials-service.md` (BR3, BR5, BR12, BR22), `.claude/rules/terraform.md` (BR5,
BR6), `docs/no-prompt-policy/`, `docs/design/update-in-flight.md`.

- **Always.** Targeted tests, never a whole package. Hermetic: `internal/gcpfake`,
  `internal/testutil`, `t.TempDir()`. A task that makes a pinned sentence false changes the
  sentence and its test in the same change, and runs `go test ./internal/cli/ -run Docs -count=1`.
  A task names the `BR#` and `D#` it implements in its pull request.
- **Ask first.** A new dependency; a permission or role beyond the ones this plan names; a change
  to a file of the launcher plan's merged C1 beyond the two fields, the route and the interface
  field this plan's own C1 names — that is a cross-plan change, and it is put to the owner (D3113,
  O21); a task that wants to start before the launcher plan's task that owns the file it edits has
  merged. Anything that would make a command wait for input.
- **Never.** A live cloud call from an agent or a test. A secret-shaped literal in a test, a
  document, a log or a fixture: keys in tests are generated in the test. The ticket or the
  attestation in a log line, an error, an audit line or a run record — Batch prints a job's
  variables itself (F3) and Fugaro prints them nowhere (R7). An edit under `.github/workflows/`. A
  deletion of a secret, a secret version or a KMS key version.
- **Review.** Every task is reviewed independently on the strongest model before it merges, and
  never merges on an in-loop verdict alone. Every task carries at least one mutation check in its
  review.
- **Live checks** are scripts with a hermetic self-test; the owner runs them, on `fugaro-dev`
  only, never `belong`.

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A Batch run's identity is served only to a VM of the job the launcher recorded, and never to another job's VM, to a VM whose job has ended, to a job created outside the bound, to a job re-created under the same name, to a VM outside the installation's project, or to a VM that is not a Batch job's (R1, D3108, O8) | BR2, BR3, BR17 |
| A claim whose record names a job and carries no attestation is refused (D3114) | BR2 |
| Only the launcher identity that asked for a run's ticket may register that run's job, once, before the claim, and no token holder can write the registration (D3108, R19) | BR17 |
| The claim admits the job classes and the roster service account, and refuses the rest (R1, R20) | BR2 |
| Each refusal after the caller is verified writes exactly one audit line, and none carries the ticket or the attestation (R7) | BR2 |
| The ticket is a variable of the Batch job, in no run record and in no line Fugaro writes (D3115, R7) | BR1, BR4 |
| A Batch job carrying a sealed entry stops the launch (R5, R8; AB9's own rule, and the ticket is not one) | BR1 |
| The service unreachable stops the run after a bounded retry; it never proceeds without an identity (F23) | BR1, BR4 |
| Where the block's opt-in fact is absent or withdrawn the launch is refused before anything is created, with opting in and `fugaro update` as its fix; no launch reads the cloud to decide (R3) | BR1, BR7 |
| `fugaro update` on an installation at the previous version converges, with a Batch run in flight keeping everything it holds (R9, R16) | BR6, BR21, I1 |
| The reaper's role gains nothing. Launchers gain `actAs` on shared project job accounts where Batch is provisioned (D3105) and nothing else | BR5, BR6 |
| The credentials service account gains, only where Batch is provisioned: `batch.jobs.get` conditioned on the Fugaro job-name prefix, and the one read Check 830 names for the link; it gains no decrypt and holds no key (D3102 as amended, O1) | BR5 |
| `fugaro update` touches the compute root only where the published block carries the opt-in fact, whatever the updating machine selects; an installation nobody opted in is left alone; a non-operator's update skips the stage with one line (D3110 as amended, R2) | BR6 |
| Publishing a block never drops the opt-in fact, and a withdrawal is a written value (R15) | BR19 |
| An opt-out is refused while a Batch job is queued or running; after it the update removes the grants, keeps the role and says so in one line (D3111, R16) | BR20, BR21 |
| Selecting the backend is not refused for any check only the update can satisfy, and every launch is refused with `fugaro update` as the fix until it has run (R14) | BR7, BR18, BR1 |
| A caller outside the launcher class cannot register a job, and a roster service account launching on Batch is one (D3123, R20) | BR17, BR1 |
| A Batch run queued past its ticket's life does not start: the reaper ends it at that life less its interval, and one that still starts late stops at its claim (D3106 as amended, F31) | BR14 |
| The merged key-release code is retired in one task, after the sides that used it have stopped (R21) | BR23, I1 |
| No CLI release is cut from `main` between the merge of BR1, BR6, BR16, BR18 or BR22 and I1, BR23's retirement included | the launcher of the plan |
| Nothing is created per run (F18) | I1 |
| The claim cannot be proved end to end before the launcher plan's own issuer is wired (its I1); this plan's I1 waits on it | I1, §7 |

## 3. The contract

`planning.decoupling` is `true` for this repository, and the repository's rigor default is
`startup`. This contract is over credentials and a wire format, so it is written in full and
reviewed at the strictest tier before any side starts, whatever the default says.

### C1: the claim's attestation and the registration (`claim-attestation`)
- Acceptance: the shapes below compile; the two fields the claim's wire gains and the two the
  issuance record gains are the only wire change, a body naming a field the service does not know
  is still refused by name, the service refuses a claim whose record names a job and carries no
  attestation, the registration route is in the wire's route table, so `RouteFor` names it and the
  merged route-count test the added route breaks is updated in the same task (the mount that admits
  the path at the service's door, and the `not_served` a caller gets there until the link is wired,
  are BR17's: `internal/credsvc/server.go` is BR17's and I1's file, not this contract's), and
  `contract-batch-claim.md` says what each side may rely on. The merged key-release code is
  untouched by this task: BR23 retires it, and nothing here reads it.
- Holds: the ticket's job field (the full name of the Batch job the launcher is about to create,
  empty for a Cloud Run launch) and the claim's attestation field (the VM's identity token in full
  format, empty for a Cloud Run launch); the `Job` and `JobID` fields of the issuance record, the
  first written with the ticket and the second by the registration (R19), with `JobID` empty on a
  record that names no job and on one whose job is not yet registered; the registration route
  (`POST /v1/identity/job`), its request, its response and its refusals; the `VMIdentity`
  interface (is this attestation a VM of this job, and is the job live and created within the
  bound) and the one field it fills on the service's identity configuration; the rule that a
  record naming a job is a Batch launch and is served only with an attestation (D3114); the
  classes the claim serves (D3123, R20); the opt-in fact with its three states (D3110, D3111) and
  the deploy input that says whether Batch is on (D3112, R17); the job-name shape; the link's
  permission, stated as "named by Check 830" and not guessed (O1).
- Files: `docs/batch-run-identity/contract-batch-claim.md` (new),
  `internal/identitywire/batchclaim.go` (new: the attestation's shape, the registration route and
  its bodies, and the job-name shape the release wire held,
  `validBatchJobNameRE` and `checkRunRef`, which move here rather than being retyped in BR23),
  `internal/identitywire/wire.go` (two fields on two bodies, two on the record, and the
  registration route in the table `RouteFor` reads), `internal/identitywire/wire_test.go` (the one
  count the added route breaks: the merged test that pins the table at five routes),
  `internal/credsvc/identity_config.go` (one interface and the field it fills),
  `internal/credsvc/identity_stub.go` (the door the identity routes are answered at, and **this
  contract's one stub**: its answer is the same for every route the table declares, so no line of
  it changes here, and I1 replaces its answer and deletes the file when the real link is wired),
  and the contract's own tests. Six files; two of them are one count and one field.
- Tests: `TestTheTicketNamesAJobAndTheClaimCarriesAnAttestation`,
  `TestABodyNamingAFieldTheServiceDoesNotKnowIsRefusedByName`,
  `TestAJobRecordClaimedWithoutAnAttestationIsRefused`,
  `TestARecordWithoutAJobIsACloudRunClaim`,
  `TestTheRegistrationRouteNamesTheJobAndItsId`.
- Command: `go test ./internal/identitywire/ -run 'TicketNamesAJob|RefusedByName|RegistrationRouteNames' -count=1 && go test ./internal/credsvc/ -run 'JobRecordClaimedWithoutAnAttestation|RecordWithoutAJob|Identity' -count=1`.
- Red step: a claim whose record names a job and carries no attestation is signed, and the ticket
  carries no job.
- Depends on: none. It is written against the launcher plan's merged C1 — its wire and its
  document, `internal/identitywire/wire.go` and that plan's `contract.md` — and **the graph draws
  no edge for that**: an edge may only name a node of this plan (that plan's O11). The fields it
  adds to that plan's files, the route and the route count's own test are the whole of the
  cross-plan change, and D3113 puts it to the owner.
- Commit: `batch: the claim's attestation, the ticket's job and the registration (C1, D3108, D3114)`.
- Reviewer tier: opus, mandatory independent review before any side is written against it.

No other task's Files names a file of C1, except I1 for the one file the contract leaves as a stub
(`internal/credsvc/identity_stub.go`, which it deletes) and the change requests against it. A task
that finds the contract wrong raises a contract change (section 7).

## 4. Slices and tasks

**Slice 0, inert:** C1, BRS, BR0 — the level-0 tasks. C1 rewrites nothing a user meets, because its
sides do not exist yet; BRS and BR0 are merged.
**Slice 1, the sides:** the level-1 tasks, BR1, BR2, BR4, BR6, BR7, BR8, BR9, BR13, BR14,
BR19 and the contract change. They are not all inert: BR6 and BR16 change what `fugaro update`
applies, and BR1 with BR16 turns the refusal into a launch that cannot yet be claimed. So no CLI
release is cut from `main` between the merge of BR1, BR6, BR16, BR18 or BR22 and I1, BR23's
retirement included — a release that ships half of this pair would ship a Batch launch that no
service can serve.
**Slice 2** is levels 2 and 3 (BR3, BR5, BR10, BR11, BR12, BR16, BR17, BR15, BR18, BR21, BR22, BR23)
and **slice 3** is level 4 (BR20) with I1. After I1, and after the launcher plan's own I1, an updated
installation on Batch launches.

**Every task F18 of the launcher plan lists is rewritten, kept or dropped one by one, from its own
Depends line, in [reconciliation.md](reconciliation.md)'s "Task by task, from each Depends line"** —
that table is the amendment's answer and this section's tasks are its detail. Each task below says
which it is.

### BRS: the job definition, split along its seams (`job-environment`)
**Merged** (#3379, 2026-11-20) and kept by the amendment exactly as merged.
- Acceptance: `jobDefinition` builds byte for byte the job it built, from four helpers in four
  files, `Options` carries the fields the sides fill (unused, zero), and the deployed-job variable
  has its one name (R6).
- Files: `internal/backend/batch/run.go`, `internal/backend/batch/batch.go`, `env.go` (new),
  `seal.go` (new), `account.go` (new), `secrets.go` (new), `internal/backend/deployedjob.go` (new),
  `internal/cli/docs_budget_token_channel_test.go`, `deploy/terraform/secret_readers_test.go`.
- Interfaces: produces the four helpers BR1, BR8, BR10 and BR11 each own one of, and the
  variable's name BR8 writes and BR9 reads. Each helper is a side under construction, not a stub:
  the helpers are real code the sides replace.

### BR0: Check 830, the probe (`live-proof`)
**Merged** (#3381, 2026-11-20) and kept by the amendment. **Its U1 and U2 rows are more load-bearing
than they were**: the link is now the whole of the attestation rather than one guard on a key
release, so what it measures is the only evidence the claim stands on (R4 as re-cut).
- Acceptance: one script measures, on a throwaway Batch job in `fugaro-dev`: what the identity
  token in full format carries inside the task's container (U2); which Google-made fact links the
  VM to its job, and whether a second job can forge it through labels or any job field (U1);
  whether a marker variable's value appears in Cloud Logging, instance metadata or the `CreateJob`
  audit record (U3); how soon a deleted job's name can be used again (U8). It prints one `PASS` or
  `FAIL` per fact and deletes the job it made.
- Files (as merged): `scripts/live-checks/check-830.sh`, `scripts/live-checks/check-830_test.sh`,
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`.
- Owner-run: `CHK830_OWNER_RUN=yes bash scripts/live-checks/check-830.sh`, then paste the result
  block into the checklist. No agent performs any step of it. **BR2, BR3, BR5, BR12, BR13 and BR23
  wait on this result (R4 as re-cut).** Its U3 row reads the wrapped marker's name out of this
  folder, which the amendment retires; the row still measures what it measured, and the
  ticket-specific measurement is BR13's (U3 as re-cut).
- **Run of 2026-11-25 (the first run of it): FAIL.** The owner ran it in `fugaro-dev` and pasted the
  block into the checklist's Check 830 section; `fugaro version` was not recorded, and `probe-ran`
  and `u1-handmade` FAILED. It settles the owner's **O1** (the link is a field of the instance's own
  object, read with `compute.instances.get` — a read that cannot be scoped to Fugaro's VMs) and his
  **O8** (a hand-made VM CAN carry every field the probe's VM carries, so no field a launcher can
  write may be the link), and **U8** is 10 s. **U2 and U3 are unmeasured** — no line of the probe
  container's own arrived, so the block's two rows read as FAIL on an unread row rather than as
  answers against the design, which is the reading a fix takes out. That fix also gives the check a
  second channel for U2 and U3 (the task's exit status, read with the `batch.jobs.get` the service
  already holds) and the readings a run with no line of its own owes the reader, taken before the
  job is deleted. **R4 returns the design to the owner**: the link may not rest on fields a
  hand-made VM can carry, and what it rests on instead is the open question (D3101's signed claims,
  once U2 is measured). **BR3, BR5 and BR12 still wait (R4 as re-cut).** The rerun is the same one
  command.
- Depends on: none.

### BR1: the launcher gives a Batch launch its ticket, or refuses (`claim-attestation`)
**Rewritten by LM19** (was: "the backend wraps, or refuses"). Same id, same level.
- Acceptance: a launch on an installation whose block carries the opt-in fact asks the issuer for
  the run's ticket with the **full name of the Batch job it is about to create**, sets it on the
  job as `identitywire.TicketEnv` and nothing else, and after the create returns registers the job
  with its Google-assigned id through BR17's route (R1); the job carries no `secretVariables`, no
  `encryptedVariables` and no sealed entry: a Batch launch with a sealed entry is refused, which is
  AB9's own rule and stays true (F21, R8); the plain `FUGARO_BUDGET_TOKEN` is never a job variable;
  where the block's opt-in fact is absent or withdrawn, or the ticket cannot be obtained, the
  launch is refused **before anything is created**, with opting in and `fugaro update` as the fix
  and with the service's own reason printed where there is one (R17, O15); the ticket prints
  redacted and is in no run record (R7, D3115); it asks nothing, and the two design documents'
  pinned sentence and the test that holds it say what the new refusal says (F28).
- Files: `internal/cli/launch_identity.go` (new in the launcher plan, its LM6: **BR1 starts after
  LM6 merges**, §7), `internal/cli/launch_batch.go` (new), `internal/cli/launch_batch_test.go`
  (new), `internal/backend/batch/seal.go`, `internal/backend/batch/seal_test.go` (new),
  `internal/backend/batch/register.go` (new: the id call), `internal/backend/batch/register_test.go`
  (new), `internal/cli/docs_budget_token_channel_test.go`, `docs/budget-token-channel/spec.md`,
  `docs/compute-backend/spec.md`. Past five; the last three are one sentence each.
- Interfaces: consumes C1 (the ticket's job field, the registration's route) and BRS (`seal.go`);
  produces the ticket BR4 claims and the registration BR17 answers.
- Tests: `TestABatchLaunchCarriesTheTicketAndNothingElse`, `TestTheTicketNamesTheJob`,
  `TestTheJobIsRegisteredAfterTheCreateWithItsId`, `TestASealedBatchLaunchIsRefused`,
  `TestABatchLaunchWithoutTheOptInIsRefusedWithItsFix`, `TestTheLaunchNamesTheServicesReason`,
  `TestTheTicketIsRedactedAndNotRecorded`, `TestAFailedTicketCreatesNothing`.
- Command: `go test ./internal/backend/batch/ -run 'BatchLaunchCarriesTheTicket|TicketNamesTheJob|JobIsRegisteredAfter|SealedBatchLaunchIsRefused|WithoutTheOptInIsRefused|TicketIsRedacted|FailedTicketCreatesNothing' -count=1 && go test ./internal/cli/ -run 'LaunchBatch|NamesTheServicesReason|Docs' -count=1`.
- Red step: the plain marker appears among the job's variables, or the ticket is set without the
  job's name.
- Depends on: C1 (the ticket's job field and the registration's route); BRS (kept: one file,
  `seal.go`). `docs/budget-token-channel/spec.md` is also AB9's file in always-a-budget: BR1
  starts after AB9 merges if AB9 has an open pull request.
- Commit: `batch: a Batch launch carries the ticket that names its job, or is refused (BR1, D3101 as amended, D3108)`.
- Reviewer tier: opus, mandatory independent review.

### BR2: the attestation the claim is served on (`claim-attestation`)
**Rewritten by LM19** (was: "the release route"). Same id, same level, new object.
- Acceptance: the claim route serves the run's identity to a verified job account whose claim
  carries an attestation that `VMIdentity` accepts for the job the record names, and to nobody
  else: a record that names a job and whose claim carries no attestation is refused (D3114); only
  the job classes and the roster service account are admitted (D3123, R20); the token's project
  must be the installation's and its zone in the installation's region; the job's id must be the
  one the record holds (BR17), the job must exist, must not be finished and must have been created
  within the bound, with a stated clock tolerance (R1); a job re-created under the same name has
  another id and is refused (F30, D3108). Each refusal after the caller is verified writes exactly
  one audit line, naming the field and never its content.
- Files: `internal/credsvc/identity_claim.go` (new in the launcher plan, its LM2: **BR2 starts
  after LM2 merges**, §7), `internal/credsvc/claim_attestation.go` (new),
  `internal/credsvc/claim_attestation_test.go` (new), `internal/credsvc/audit.go`.
- Interfaces: consumes C1 and BR3's `VMIdentity` implementation; it is the caller LM2's claim route
  makes before it signs.
- Tests: `TestTheIdentityIsServedToTheJobsOwnVM`, `TestAJobRecordClaimedWithoutAnAttestationIsRefused`,
  `TestAnAttestationFromAnotherJobsVMIsRefused`, `TestAnAccountOutsideTheJobTableIsRefused`,
  `TestARecreatedJobIsRefusedByItsId`, `TestAStaleRecordIsRefused`,
  `TestAPersonClassAndAnUnlistedServiceAccountAreRefused`,
  `TestAVMOutsideTheProjectIsRefused`, `TestTheBoundAllowsTheStatedClockTolerance`,
  `TestEachRefusalWritesOneAuditLine`, `TestNoAuditLineCarriesTheTicketOrTheAttestation`.
- Command: `go test ./internal/credsvc/ -run 'ServedToTheJobsOwnVM|ClaimedWithoutAnAttestation|AnotherJobsVM|OutsideTheJobTable|RecreatedJobIsRefusedByItsId|StaleRecord|UnlistedServiceAccount|VMOutsideTheProject|BoundAllowsTheStatedClock|RefusalWritesOne|CarriesTheTicketOrTheAttestation' -count=1`.
- Red step: a claim whose record names a job is signed without an attestation.
- Depends on: C1 alone. Its old waits on BR1 and on BR3 are the two C1 broke — §5's broken table,
  "BR2 after BR1" and "BR3 after BR2" — because what this task needs from either of them is a shape
  the contract declares: the link implements an interface the claim calls, so BR2 is written
  against C1's `VMIdentity` and never against the link's code. Its behaviour is where it was, and it
  is the link's: this task asks the link whether the VM is the job's own — the route and the link
  are two sides, and the link is the one that decides.
- Commit: `credsvc: the identity is served to the job's own VM, and to nobody else (BR2, D3101 as amended, D3114)`.
- Reviewer tier: opus, mandatory independent review.

### BR3: the link from a VM to its job (`claim-attestation`)
**Rewritten by LM19** in its file and its interface, kept in substance: the link is now what decides
a claim rather than one guard on a key release.
- Acceptance: `VMIdentity` accepts a VM only on the fact Check 830 recorded, for a job that exists,
  is not finished and was created within the bound; it refuses a VM of another job, a VM whose
  launcher-set labels claim the job, a plain Compute Engine VM that is no Batch job's (O8), and a
  job whose id is not the one the record holds; before the check runs it returns the read the claim
  asks for and never a guess.
- Files: `internal/credsvc/joblink.go` (new), `internal/credsvc/joblink_test.go` (new),
  `internal/gcpfake/batch.go`, `internal/gcpfake/compute.go`.
- Interfaces: implements C1's `VMIdentity`; consumes the recorded result of Check 830.
- Tests: `TestTheLinkAcceptsTheJobsOwnVM`, `TestALabelIsNotALink`, `TestAPlainVMIsRefused`,
  `TestAFinishedJobIsRefused`, `TestARecreatedJobIsNotTheRecordedOne`.
- Command: `go test ./internal/credsvc/ -run 'LinkAccepts|LabelIsNotALink|PlainVMIsRefused|FinishedJob|RecreatedJobIsNotTheRecordedOne' -count=1`.
- Red step: a VM carrying only a forged label is accepted.
- Depends on: C1; `R-C1-link-permission` (kept, data: it carries the owner's result of Check 830
  and his answer on the second read, R4, O1).
- Commit: `credsvc: a VM is its job's by what Google says, not by a label (BR3, R1, R4)`.
- Reviewer tier: opus, mandatory independent review.

### BR4: the runner attests its VM (`claim-attestation`)
**Rewritten by LM19** (was: "the runner fetches its key").
- Acceptance: on Batch the runner claims its identity as it does on Cloud Run and adds its VM's
  identity token in full format, read from the metadata server; it registers the ticket with the
  redactor before anything can print it and never prints the attestation; a runner with no ticket,
  or one whose claim is refused, stops with the service's reason and `fugaro update` where that is
  the fix (R18, F37); an unreachable service stops the run after a bounded retry and the run never
  proceeds without an identity (F23); the ticket is stripped from every git call and from the
  session's environment.
- Files: `internal/runner/identity_claim.go` (new in the launcher plan, its LM7: **BR4 starts
  after LM7 merges**, §7), `internal/runner/budget_session.go`, `internal/runner/batch_attest.go`
  (new), `internal/runner/batch_attest_test.go` (new), `internal/gitops/gitops.go`.
- Interfaces: consumes C1 (the claim's attestation field, the ticket's name).
- Tests: `TestABatchRunAttestsItsOwnVM`, `TestARunWithNoTicketStops`,
  `TestARefusedClaimStopsWithTheServicesReason`, `TestAnUnreachableServiceStopsTheRunAfterTheBound`,
  `TestTheAttestationIsNeverPrinted`, `TestGitNeverSeesTheTicket`.
- Command: `go test ./internal/runner/ -run 'BatchRunAttestsItsOwnVM|RunWithNoTicketStops|RefusedClaimStops|UnreachableServiceStops|AttestationIsNeverPrinted' -count=1 && go test ./internal/gitops/ -run Ticket -count=1`.
- Red step: the runner opens its session with no attestation when it is on a Batch VM.
- Depends on: C1.
- Commit: `runner: a Batch run claims its identity with its VM's attestation (BR4, R1, R18)`.
- Reviewer tier: opus, mandatory independent review.

### BR5: the link's permission, and the grants that go with Batch (`update-converge`)
**Rewritten by LM19**: no key ring, no KMS key, no decrypt (they are not created at all).
- Acceptance: the compute root plans the link's read for the credentials service account, a custom
  role holding `batch.jobs.get` conditioned on the Fugaro job-name prefix, and the one read Check
  830 named for the link, granted only where Batch is provisioned; launchers get nothing on any
  key and the reaper's role is unchanged; one input turns the grants off and leaves the custom role
  planned as it is: with it off, the service's binding to the role, its `batch.jobs.get`, the one
  read and whatever the root was fed for the shared project job accounts are not planned, and the
  per-repository accounts keep theirs (R16); the removal classes `cover.go` needs are asked for the
  bindings and grants that are left, so the removal R16 needs is not refused and a destroy of
  anything data-bearing still is; `.claude/rules/credentials-service.md` states the account's
  grants as they now are, in this same change (F19, D3102 as amended).
- Files: `deploy/terraform/gcp/modules/compute/iam.tf`, `.../compute/apis.tf`, `.../compute/outputs.tf`,
  `.../compute/variables.tf`, `deploy/terraform/gcp/roots/compute/main.tf`, `.../outputs.tf`,
  `.../variables.tf`, `.../tests/compute.tftest.hcl`, `internal/infra/tf/cover.go`, the golden plans
  (regenerated by `scripts/gen-golden-plans.sh`, never by hand), `deploy/terraform/terraform_test.go`
  (the custom-role snapshot pins the new role), `.claude/rules/credentials-service.md`. Past five;
  each root file is a pass-through line.
- Interfaces: takes the output and input names from C1's contract document, and the link's
  permission from the recorded result of Check 830.
- Tests: the root's `terraform test`, with a run for the grants-off input that asserts the
  bindings are absent and the role and the provisioning are present;
  `TestTheComputeRootsNewTypesAreCovered`; `TestALinkGrantRemovalIsAllowed`; the custom-role
  snapshot test.
- Command: `terraform fmt -check -recursive deploy/terraform && (cd deploy/terraform/gcp/roots/compute && TF_DATA_DIR=$(mktemp -d) terraform init -backend=false -lockfile=readonly && terraform validate && terraform test) && scripts/gen-golden-plans.sh && go test ./internal/infra/tf/ -run 'ComputeRootsNewTypes|LinkGrantRemoval|Golden' -count=1 && go test -tags terraform ./internal/infra/... && go test ./deploy/terraform/ -run 'Role|Snapshot' -count=1 && tflint --chdir=deploy/terraform/gcp && sh deploy/terraform/scan.sh`.
- Red step: the tftest fails on a role holding a permission beyond the two named.
- Depends on: `R-C1-link-permission` (kept, data: it carries the owner's result of Check 830 and
  his answer on the second read, and it comes after C1, whose names this task uses).
- Commit: `terraform: the Batch link's read, and who may use it (BR5, D3102 as amended, R9)`.
- Reviewer tier: opus, mandatory independent review.

### BR6: update applies the compute root (`update-converge`)
**Kept by LM19**, with one sentence fewer: there is no key to apply.
- Acceptance: `fugaro update` applies the compute root where the published block carries the
  opt-in fact, whatever the updating machine's own `backend:` says, and leaves alone an installation
  nobody opted in; on a machine without operator rights (the role `admin`, reconciliation A2) it
  skips the stage with one line; where the block says "in" and the root was never provisioned it
  skips with one line naming `fugaro setup --provision-batch` (D3110 as amended, R2); it asks
  nothing, is refused in an agent's session, and does not wait; an apply that changes only a role
  or a grant starts no rebuild of the preload image, and one that changes the image's inputs says
  so in one line with the cost (R12); with a Batch run in flight it says so in one line and changes
  nothing the run holds.
- Files: `internal/cli/init_stages.go`, `internal/cli/init_compute.go`, `internal/cli/update.go`,
  `internal/apply/steps.go`, `internal/cli/update_batch_test.go` (new).
- Interfaces: consumes C1 (the opt-in fact).
- Tests: `TestUpdateAppliesTheComputeRootWhereTheInstallationOptedIn`,
  `TestUpdateLeavesAnInstallationNobodyOptedInAlone`, `TestTheUpdatingMachinesOwnBackendDoesNotDecide`,
  `TestANonOperatorsUpdateSkipsTheStage`, `TestAnOptedInInstallationNeverProvisionedIsSkipped`,
  `TestTheUpdateSaysABatchRunKeepsRunning`, `TestTheComputeStageUnderUpdateAsksNothing`,
  `TestTheComputeStageUnderUpdateIsRefusedInAnAgentSession`, `TestAGrantOnlyApplyStartsNoPreloadRebuild`.
- Command: `go test ./internal/cli/ -run 'UpdateAppliesTheComputeRoot|NobodyOptedInAlone|OwnBackendDoesNotDecide|NonOperatorsUpdateSkips|NeverProvisionedIsSkipped|ABatchRunKeepsRunning|ComputeStageUnderUpdate|GrantOnlyApplyStartsNoPreload|RefusalMatrix|Docs' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: an update on an installation whose block says "in" leaves the compute root unapplied.
- Depends on: C1. This task was approved on 2026-11-20 as a task with no dependency that applied
  the root wherever a block exists; it changed (D3110); the owner confirmed the approval for the
  amended text the same day, and **the approval is on hold now that the contract it consumes is
  re-cut** (the launcher plan's O16).
- Commit: `update: the compute root is applied by the one command, where the installation opted in (BR6, R2, R12, D3110)`.
- Reviewer tier: opus, mandatory independent review.

### BR16: update writes the block (`update-converge`)
**Rewritten by LM19**: the key fact leaves the block; the opt-in fact and the accounts stay.
- Acceptance: after `fugaro update` the block carries the opt-in fact and no key fact — a key fact
  written by the release behind is removed from the machine's file in place, and R10's rule holds
  for it; the retired key is dropped **by name as text**, and the writer's own code stops naming its
  field and its type — `sameSealKey`, and the copy in the block's builder — which is the behaviour
  BR23's deletion waits for, so BR23 never reaches into this file; `batch.secrets` and the single
  `service_account` are gone from the file, in place; the root is fed the project job accounts
  beside the per-repository ones, which is what gives launchers `actAs` on them (D3105); the block
  is published; the release notes' `### For operators` says "run `fugaro update`", and the feature
  notes say how Batch is chosen; where the root has no output yet (BR5 not applied) the launch
  refusal stays and the opt-in fact is still written (R13 dropped: no settings command writes a
  fact the installation's root owns).
- Files: `internal/cli/init_compute.go`, `internal/cli/init_compute_block_test.go`,
  `internal/infra/compute.go`, `internal/infra/compute_test.go`, the next release's notes under
  `docs/releases/`.
- Interfaces: consumes C1 (the block's opt-in fact, the root's names).
- Tests: `TestUpdateRemovesTheKeyFactInPlace`, `TestUpdateRemovesBatchSecretsInPlace`,
  `TestTheRootIsFedTheProjectJobAccounts`, `TestALaunchReadsTheOptInFromThePublishedBlock`,
  `TestNoKeyFactIsEverWritten`, `TestTheOperatorStepIsRunUpdate`.
- Command: `go test ./internal/cli/ -run 'UpdateRemovesTheKeyFact|UpdateRemovesBatchSecrets|NoKeyFactIsEverWritten|OperatorStepIsRunUpdate' -count=1 && go test ./internal/infra/ -run RootIsFedTheProjectJobAccounts -count=1`.
- Red step: after an update the block still names a key.
- Depends on: C1; BR6 (kept: one file, `init_compute.go`).
- Commit: `update: the opt-in fact and the accounts reach the block, and the key fact leaves it (BR16, R10, D3105)`.
- Reviewer tier: opus, mandatory independent review.

### BR7: doctor and validate say where the claim stands (`update-converge`)
**Rewritten by LM19**: the check is about the opt-in fact and the link, not a key.
- Acceptance: on an installation that opts in, the Batch check passes where the opt-in fact is
  present and the link's read is grantable, and fails with the right fix where either is missing —
  `fugaro update` for the grant, opting in for the fact; `validate` agrees offline; the failure a
  differing or missing key fact used to be is gone with the key fact (R13 dropped); each Batch
  check whose fix is the compute root under `fugaro update` carries a mark saying so, which BR18
  reads (R14).
- Files: `internal/cli/doctor_batch.go`, `internal/cli/doctor_batch_test.go`,
  `internal/cli/validate_batch.go`, `internal/cli/validate_batch_test.go`.
- Interfaces: consumes C1 (the block's opt-in fact).
- Tests: `TestDoctorPassesAnInstallationThatOptedIn`, `TestDoctorNamesTheUpdateWhereTheLinkGrantIsMissing`,
  `TestDoctorNamesOptingInWhereTheFactIsMissing`, `TestValidateAgreesWithDoctorOnTheClaim`,
  `TestNoCheckMentionsAKey`, `TestTheChecksOnlyTheUpdateFixesAreMarked`.
- Command: `go test ./internal/cli/ -run 'DoctorPassesAnInstallationThatOptedIn|NamesTheUpdateWhereTheLinkGrant|NamesOptingInWhereTheFact|ValidateAgreesWithDoctorOnTheClaim|NoCheckMentionsAKey|OnlyTheUpdateFixesAreMarked' -count=1`.
- Red step: doctor still fails an installation that has everything the claim needs.
- Depends on: C1. Both files are also AB9's in always-a-budget: BR7 starts after AB9 merges if AB9
  has an open pull request.
- Commit: `doctor: a Batch installation is ready once the claim can be made (BR7, R3, R14)`.
- Reviewer tier: opus, mandatory independent review.

### BR8: a Batch job carries what a deployed job carries (`job-environment`)
**Kept by LM19** as written: nothing of it is built on the key, and the ticket rides on top of the
environment it builds.
- Acceptance: the job's variables are every variable the deployed job's template would carry for
  the same workflow (the bucket, the budget backend, the credentials service's URL, the price
  tables, the caps and the rest, F8), built from the same values the template is built from, plus
  the name of the deployed job the run stands in for; each is reserved against a launch entry and
  the ticket's name is reserved with them; a test fails when the template gains a variable the
  Batch job lacks; the stale comments about unbuilt wiring are gone.
- Files: `internal/backend/batch/env.go`, `internal/backend/batch/env_test.go` (new),
  `internal/backend/launchenv.go`, `internal/infra/spec.go` (the template's variables, exported as
  one list), `internal/infra/spec_batch_test.go` (new).
- Interfaces: consumes BRS (`env.go`, the deployed-job name).
- Tests: `TestABatchJobNamesItsBucketAndBudgetBackend`, `TestABatchJobNamesItsDeployedJob`,
  `TestABatchJobCarriesEveryVariableADeployedJobDoes`, `TestALaunchEntryCannotSetThem`.
- Command: `go test ./internal/backend/batch/ -run 'NamesItsBucketAndBudget|NamesItsDeployedJob|LaunchEntryCannotSet' -count=1 && go test ./internal/backend/ -run 'LaunchEnv|Reserved' -count=1 && go test ./internal/infra/ -run BatchJobCarriesEveryVariable -count=1`.
- Red step: the job has no bucket variable.
- Depends on: BRS (kept: one file, and the name it defines).
- Commit: `batch: the job's environment is a deployed job's (BR8, F8, R6)`.
- Reviewer tier: opus, mandatory independent review.

### BR9: the runner places a Batch run (`job-environment`)
**Kept by LM19** as written.
- Acceptance: on Batch the runner places the run by the deployed-job variable, exactly as it places
  a Cloud Run run by its job name, and sends that name to the service; a Batch run with no such
  variable, or one that matches no candidate, stops as an unplaceable run does today.
- Files: `internal/runner/projectjob.go`, `internal/runner/projectjob_batch_test.go` (new).
- Interfaces: consumes BRS (the name).
- Tests: `TestABatchRunIsPlacedByItsDeployedJob`, `TestABatchRunWithNoDeployedJobStops`.
- Command: `go test ./internal/runner/ -run 'BatchRunIsPlaced|BatchRunWithNoDeployedJob' -count=1`.
- Red step: a Batch run ends at `cannotPlaceJob`.
- Depends on: BRS (kept: the variable's name is a constant BRS adds; no contract is needed for one
  name).
- Commit: `runner: a Batch run is placed by the job it stands in for (BR9, F9, R6)`.
- Reviewer tier: opus, mandatory independent review.

### BR10: Batch runs a project job's workflow (`project-jobs`)
**Kept by LM19** as written (D3103, and the account question D3103 turns on).
- Acceptance: a launch whose workflow belongs to a shared project job creates a Batch job under
  that project job's account with that job's environment; a per-repository workflow runs under its
  own job's account; the block's single account is no longer read.
- Files: `internal/backend/batch/account.go`, `internal/backend/batch/account_test.go` (new),
  `internal/backend/batch/run.go`.
- Interfaces: consumes BRS (`account.go`), BR8's environment and `LaunchSpec.Job`. If the account
  cannot be derived from what `LaunchSpec` carries, it raises `R-C1-project-job-account`.
- Tests: `TestAProjectJobWorkflowRunsUnderThatJobsAccount`,
  `TestAPerRepositoryWorkflowRunsUnderItsOwnAccount`, `TestTheBlocksAccountIsNotRead`.
- Command: `go test ./internal/backend/batch/ -run 'ProjectJobWorkflowRuns|PerRepositoryWorkflowRuns|BlocksAccountIsNotRead' -count=1`.
- Red step: the launch is refused with "no project jobs yet".
- Depends on: BRS (kept: one file); BR8 (kept, behaviour: the project job's environment is built by
  BR8's helper).
- Commit: `batch: a project job's workflow runs on Batch under that job's account (BR10, D3103)`.
- Reviewer tier: opus, mandatory independent review.

### BR11: nothing is mounted on a Batch job (`service-credentials`)
**Kept by LM19** as written (D3104, D3107), with one correction: it writes
`internal/cli/launch_batch.go` and its test, which BR1 creates, so it amends BR1's files and runs a
level below BR1 rather than writing them beside it.
- Acceptance: a Batch job carries no `secretVariables`; a launch on Batch for a repository the
  credentials service does not serve is refused before the mint, with its fix; row E5 leaves the
  secret-readers table and its document together, saying D3107 supersedes D2075.
- Files: `deploy/terraform/secret_readers_test.go`, `docs/secrets-brokering/secret-readers.md`,
  `internal/backend/batch/secrets.go`, `internal/backend/batch/secrets_test.go` (new),
  `internal/cli/launch_batch.go`, `internal/cli/launch_batch_test.go`,
  `internal/cli/run.go`, `internal/cli/cloud_openers_test.go`. Past five; the first two are one row.
- Interfaces: consumes BRS (`secrets.go`).
- Tests: `TestABatchJobMountsNothing`, `TestAnUnservedRepositoryIsRefusedOnBatchWithItsFix`,
  `TestTheRefusalComesBeforeTheMint`.
- Command: `go test ./internal/backend/batch/ -run BatchJobMountsNothing -count=1 && go test ./internal/cli/ -run 'UnservedRepositoryIsRefusedOnBatch|RefusalComesBeforeTheMint|Openers|Docs' -count=1 && go test ./deploy/terraform/ -run SecretReaders -count=1`.
- Red step: a job with `batch.secrets` set still carries `secretVariables`.
- Depends on: BRS (kept: one file); BR1 (kept: two files, `launch_batch.go` and
  `launch_batch_test.go`, which this task amends rather than creates).
- Commit: `batch: a run's credentials come from the service alone (BR11, D3104, D3107, R10)`.
- Reviewer tier: opus, mandatory independent review.

### BR12: the documents (`live-proof`)
**Rewritten by LM19**: a Batch run is issued an identity rather than handed a key.
- Acceptance: `SECURITY.md` says who can and cannot obtain a Batch run's identity, that the ticket
  is printed by Batch and is worth nothing without the recorded job's VM (D3115), and names the
  service's grants as they now are; the website page is regenerated; `docs/backends.md` says how to
  reach a launching Batch installation; `SECURITY.md` says that launchers hold `actAs` on shared
  project job accounts where Batch is provisioned (D3105) and that a launcher no longer mints, in
  the words the launcher plan's own document uses; its sentence that launchers hold the Batch
  editor role "on the project" is corrected to the job-name condition the grant carries; its
  sentence on the service account's only other grant names the grants as they now are.
- Files: `SECURITY.md`, `website/content/security.md` (generated), `docs/backends.md`,
  `internal/cli/docs_security_test.go`. The first two are the launcher plan's LM16 files: BR12
  starts after LM16 merges if it has not yet, or rebases on it (§7).
- Tests: the Docs tests; `website`'s `security.mjs`.
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`.
- Red step: the website's security test fails until the page is regenerated.
- Depends on: C1; `R-C1-link-permission` (kept, data: the page names the service's grants as the
  owner settled them). `docs/backends.md` is also AB9's file: BR12 starts after AB9 merges if AB9
  has an open pull request.
- Commit: `docs: a Batch run's identity, and who can reach it (BR12, D3101 as amended, D3105)`.
- Reviewer tier: opus, mandatory independent review.

### BR13: Check 831, the proof (`live-proof`)
**Rewritten by LM19**: the four attacks are four attacks on the claim.
- Acceptance: one script launches a sandbox run on Batch, reads that it opened its session and was
  metered, and then, as a second launcher account, fails to obtain the first run's identity four
  ways: reading the job and **replaying its ticket into a job of the second launcher's own**, which
  the attestation refuses because their VM is another job's; calling the claim from the second
  launcher's own VM without a ticket; **deleting a queued job and re-creating it under the same
  name**, which the record's id refuses (D3108, F30); and reading the issuance record directly. It
  also reads where Batch printed the ticket (the logs, the job's own document and the audit
  record) so that D3115's rule is measured rather than assumed (U3 as re-cut). It ends with the
  cost and with nothing left to delete.
- Files: `scripts/live-checks/check-831.sh` (new), `scripts/live-checks/check-831_test.sh` (new),
  `scripts/live_checks_test.go` (its list of scripts), `docs/gcp-live-checklist.md`.
- Tests: the self-test.
- Command: `bash scripts/live-checks/check-831_test.sh && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the self-test fails when the replay step is removed.
- Depends on: C1; BR0 (kept: two files, the checklist and the scripts' list).
- Commit: `checks: Check 831, a Batch run's identity end to end (BR13, R11)`.
- Reviewer tier: opus, mandatory independent review.
- Owner-run, after the release and `fugaro update` on `fugaro-dev`:
  `CHK831_OWNER_RUN=yes bash scripts/live-checks/check-831.sh --second-launcher <account>`. No
  agent performs any step of it.

### BR14: the reaper ends a Batch run at its ticket's life (`job-environment`)
**Rewritten by LM19** in its object, kept in its rule: the run no longer holds a token before it
starts.
- Acceptance: a job whose environment carries the ticket and has been queued for the ticket's life
  less the reaper's own interval and a stated start margin is deleted by the reaper, reported with
  that reason, and reads as ended in the queue; the boundary is tested at one tick either side
  (F31); a job with no ticket keeps today's queue deadline; the reaper never reads the ticket's
  value; nothing else the reaper deletes changes.
- Files: `internal/backend/batch/reap.go`, `internal/backend/batch/reap_claim_test.go` (new).
- Interfaces: consumes C1 (the ticket's name) and the issuer's stated ticket life.
- Tests: `TestAJobQueuedPastItsTicketIsDeleted`, `TestTheTicketDeadlineLeavesATickAndAMargin`,
  `TestAJobWithoutATicketKeepsTheQueueDeadline`, `TestTheReaperNeverReadsTheTicketsValue`.
- Command: `go test ./internal/backend/batch/ -run 'QueuedPastItsTicket|TicketDeadlineLeavesATick|WithoutATicketKeepsTheQueueDeadline|NeverReadsTheTicketsValue|Reap' -count=1`.
- Red step: a job queued for two hours is left to start.
- Depends on: C1.
- Commit: `reaper: a Batch run does not outlive its ticket in the queue (BR14, D3106 as amended)`.
- Reviewer tier: opus, mandatory independent review.

### BR15: doctor and the readers' table follow (`service-credentials`)
**Kept by LM19** as written (D3107); `SECURITY.md` says what BR12 says about mounts.
- Acceptance: doctor's secret check for the block and its Batch row in the secret-readers check are
  gone, and with them the readers' table's row for that check (E23); `SECURITY.md` says that a
  Batch run mounts nothing and that D3107 supersedes D2075; the tests that pin those sentences
  agree.
- Files: `internal/cli/doctor_batch.go`, `internal/cli/doctor_batch_test.go`,
  `internal/cli/doctor_secret_readers.go`, `deploy/terraform/secret_readers_test.go`,
  `docs/secrets-brokering/secret-readers.md`, `docs/secrets-brokering/reconciliation.md` (D2075's
  cell, marked superseded), `SECURITY.md`, `website/content/security.md` (generated),
  `internal/cli/docs_security_test.go`. Past five; five are one sentence or one row each.
- Tests: `TestDoctorHasNoBatchSecretsCheck`, the Docs tests.
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run 'DoctorHasNoBatchSecretsCheck|DoctorBatch|SecretReaders|Docs' -count=1 && go test ./deploy/terraform/ -run SecretReaders -count=1 && (cd website && npm run check)`.
- Red step: the readers' table test fails on the row whose check is gone.
- Depends on: BR7 (kept: one file, `doctor_batch.go`); BR12 (kept: one file, `SECURITY.md`).
- Commit: `doctor, docs: a Batch run mounts nothing; D2075 is superseded (BR15, D3107)`.
- Reviewer tier: opus, mandatory independent review.

### BR18: selecting the backend does not wait for the claim (`update-converge`)
**Kept by LM19** in substance: the checks it names are the link's and the accounts'.
- Acceptance: `fugaro backends use cloud-batch` is refused for every failing Batch check except
  those BR7 marks as fixed only by the update; with only those failing it selects, an operator's
  selection writes and publishes the opt-in fact, and it prints one line saying that `fugaro
  update` is the next step and that launches are refused until it has run; a person who is not an
  operator selects locally and is told an operator must opt the installation in; `docs/backends.md`
  says the order (R2, R14). The `batch-budget-seal` id and the differing-key-fact refusal are gone
  with the key.
- Files: `internal/cli/backends.go`, `internal/cli/backends_use_test.go`, `docs/backends.md`.
- Interfaces: consumes BR7's check, which says which failure it is.
- Tests: `TestBackendsUseSelectsWhenOnlyTheLinkGrantIsMissing`,
  `TestBackendsUseSelectsOnASharedJobsInstallationBeforeTheApply`,
  `TestAnOperatorsSelectionPublishesTheOptIn`, `TestANonOperatorsSelectionDoesNotOptIn`,
  `TestBackendsUseNamesTheUpdateAsTheNextStep`, `TestNoSelectionFailureMentionsAKey`.
- Command: `go test ./internal/cli/ -run 'BackendsUseSelects|OperatorsSelection|BackendsUseNamesTheUpdate|NoSelectionFailureMentionsAKey|BackendsUse|Docs' -count=1`.
- Red step: on an installation with shared jobs and no apply yet, the selection is refused.
- Depends on: BR7 (kept, behaviour: it reads which failure the check reports); BR12 (kept: one
  file, `docs/backends.md`).
- Commit: `backends: selecting Batch does not wait for the claim's grants (BR18, D3110, R14)`.
- Reviewer tier: opus, mandatory independent review.

### BR19: publishing never drops the opt-in (`update-converge`)
**Rewritten by LM19**: one fact to keep, not two.
- Acceptance: when a machine publishes a `batch:` block, the published copy's value for the opt-in
  fact is kept, whether the machine's own block lacks it or holds another value; a withdrawn opt-in
  is a value of its own and is kept the same way; the writers R15 names pass their new value
  explicitly; the publish rewrites the machine's own copy of the field from the published one;
  every other field merges as it does today, and a key fact an older block still carries is dropped
  in place like `batch.secrets` (R10). Where the published copy is refused or too large and is
  replaced, the machine's value is published and the publish says so in its existing warning (O18).
- Files: `internal/localcfg/shared.go`, `internal/localcfg/shared_test.go`,
  `internal/cli/sharedcfg.go`, `internal/cli/sharedcfg_test.go`.
- Interfaces: consumes C1 (the opt-in fact).
- Tests: `TestPublishingKeepsTheOptIn`, `TestPublishingKeepsAWithdrawal`,
  `TestAStaleLocalOptInDoesNotOverwriteAWithdrawal`,
  `TestPublishingRefreshesTheLocalCopyOfTheOptIn`,
  `TestEveryOtherBatchFieldMergesAsBefore`, `TestAKeyFactIsNotPublished`.
- Command: `go test ./internal/localcfg/ -run 'PublishingKeeps|StaleLocalOptIn|EveryOtherBatchFieldMerges|AKeyFactIsNotPublished|Shared' -count=1 && go test ./internal/cli/ -run 'PublishingRefreshesTheLocalCopy|SharedConfig' -count=1`.
- Red step: a machine with no opt-in publishes and the published block loses it.
- Depends on: C1.
- Commit: `localcfg: publishing a batch block keeps the opt-in (BR19, R15)`.
- Reviewer tier: opus, mandatory independent review.

### BR20: an operator opts out (`update-converge`)
**Kept by LM19** as written (D3111).
- Acceptance: an explicit command withdraws the opt-in fact and publishes it; merely selecting
  `cloud-run` on a machine does not; it asks nothing; it is refused with the reason while a Batch
  run of the installation is in flight, queued ones included, read as the update reads runs in
  flight and with no Batch API call (R16, U9), and for a person who is not an operator; after it, a
  launch on Batch from a machine whose block or published copy says withdrawn is refused before
  anything is created, with opting in again as the fix; `docs/backends.md` and the CLI reference
  say so, and say that a machine with a local config follows at its next `fugaro update` (R16,
  O19).
- Files: `internal/cli/backends.go`, `internal/cli/backends_optout_test.go` (new),
  `internal/cli/launch_batch.go` (the launch's refusal), `docs/backends.md`,
  `website/content/cli-reference.md` (generated), the plugin skill that names the `backends`
  commands if the plugin's tests require it.
- Interfaces: consumes C1 (the fact's three states).
- Tests: `TestAnOperatorOptsOut`, `TestSelectingCloudRunDoesNotOptOut`,
  `TestOptOutIsRefusedWhileABatchJobIsLive`, `TestOptOutCountsAQueuedBatchRun`,
  `TestANonOperatorCannotOptOut`, `TestALaunchAfterOptOutNamesOptingIn`.
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run 'OperatorOptsOut|SelectingCloudRunDoesNotOptOut|OptOutIsRefusedWhile|OptOutCountsAQueued|NonOperatorCannotOptOut|LaunchAfterOptOut|CliReference|Docs' -count=1 && go test ./plugin/... -count=1`.
- Red step: selecting `cloud-run` withdraws the fact.
- Depends on: BR18 (kept: two files, `backends.go` and `docs/backends.md`); BR11 (kept: one file,
  `launch_batch.go`, two levels earlier).
- Commit: `backends: an operator can opt the installation out of Batch (BR20, D3111, R16)`.
- Reviewer tier: opus, mandatory independent review.

### BR21: update retires the grants after an opt-out (`update-converge`)
**Rewritten by LM19**: the bindings are the role's, the job read's and the shared accounts'; there
is no decrypt on a key to remove.
- Acceptance: on an installation whose published fact is withdrawn, `fugaro update` applies the
  compute root with the grants off, so the credentials service's binding to its role, its
  `batch.jobs.get` and the one read, and what the root was fed for the shared project job accounts
  are gone, and the custom role and the provisioning are untouched; it prints one line naming what
  it removes; with a Batch run in flight, queued ones included, it removes nothing, says so in one
  line with the count, and a later update removes them; opting in again restores the bindings at
  the next update; an installation at the previous version that opted out converges;
  `docs/design/update-in-flight.md` gains the step's row (R16, R9).
- Files: `internal/cli/init_compute.go`, `internal/cli/update_batch_optout_test.go` (new),
  `internal/apply/steps.go`, `docs/design/update-in-flight.md`.
- Interfaces: consumes C1 (the fact's states) and BR5's input that turns the grants off.
- Tests: `TestUpdateRemovesTheGrantsAfterAnOptOut`, `TestTheRoleSurvivesAnOptOut`,
  `TestOptingInAgainRestoresTheGrants`, `TestTheUpdateNamesWhatAnOptOutRemoves`,
  `TestTheRetiringUpdateWaitsForABatchRunInFlight`, `TestTheRetiringUpdateCountsAQueuedBatchRun`.
- Command: `go test ./internal/cli/ -run 'RemovesTheGrantsAfterAnOptOut|RoleSurvivesAnOptOut|OptingInAgainRestores|NamesWhatAnOptOutRemoves|RetiringUpdate|RefusalMatrix|Docs' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: after an opt-out and an update the service still binds the link's role.
- Depends on: BR16 (kept: one file, `init_compute.go`); BR5 (kept, behaviour: it applies the root
  with the input BR5 adds).
- Commit: `update: an opted-out installation loses the Batch grants and keeps its role (BR21, D3111, R16)`.
- Reviewer tier: opus, mandatory independent review.

### BR22: the service refuses a ticket or a registration where Batch is off (`claim-attestation`)
**Rewritten by LM19** in where it reads: the refusal moves to the ticket and the registration.
- Acceptance: the credentials service takes one deploy input that says whether Batch is on;
  `fugaro update` sets it from the published opt-in fact, on for "in" and off for absent or
  withdrawn; where it is off a ticket ask that names a job and a registration are both refused —
  the first before any job exists — with one audit line each and a reason the launch prints with
  its fix (an operator opts in and runs `fugaro update`); the claim route does not read the input,
  so a run in flight is served as before (R16); an installation at the previous version converges
  with the input off; the input's name is in `.claude/rules/credentials-service.md` (R17, D3112).
- Files: `internal/credsvc/batchclaim_register.go`, `internal/credsvc/batchclaim_register_test.go`
  (new), `internal/cli/credentials_serve.go`, `internal/cli/init_credentials.go`,
  `internal/infra/tfvars.go`, `deploy/terraform/gcp/modules/installation/credentials.tf` and its
  variable, the golden plans (regenerated by `scripts/gen-golden-plans.sh`),
  `.claude/rules/credentials-service.md`. Past five; the Terraform is one `env` block and one
  variable field. `credentials_serve.go` and `tfvars.go` are the launcher plan's files (its LM22
  and LM13): BR22 starts after those merge or rebases on them (§7).
- Interfaces: consumes C1 (the fact's three states, the registration's route).
- Tests: `TestATicketAndARegistrationAreRefusedWhereBatchIsOff`,
  `TestTheClaimRouteIgnoresTheSwitch`, `TestTheUpdateSetsTheSwitchFromThePublishedFact`,
  `TestAWithdrawnFactDeploysTheSwitchOff`, `TestTheOffRefusalWritesOneAuditLine`,
  `TestTheLaunchPrintsTheServicesReasonAndFix`.
- Command: `go test ./internal/credsvc/ -run 'RefusedWhereBatchIsOff|ClaimRouteIgnoresTheSwitch|OffRefusalWritesOne' -count=1 && go test ./internal/cli/ -run 'SetsTheSwitchFromThePublishedFact|WithdrawnFactDeploysTheSwitchOff|PrintsTheServicesReasonAndFix|Docs' -count=1 && scripts/gen-golden-plans.sh && go test ./internal/infra/tf/ -run Golden -count=1 && terraform fmt -check -recursive deploy/terraform`.
- Red step: with the input off, a ticket that names a job is issued.
- Depends on: BR17 (kept: one file, `batchclaim_register.go`); BR5 (kept: one file,
  `.claude/rules/credentials-service.md`, and the golden plans).
- Commit: `credsvc: no ticket and no registration where Batch is off (BR22, D3112, R17)`.
- Reviewer tier: opus, mandatory independent review.

### BR17: the registration (`claim-attestation`)
**Rewritten by LM19**: the record registers the job instead of a blob's digest.
- Acceptance: a launcher's verified identity registers the job it created on the run's issuance
  record, with the job's Google-assigned id, and the record's `Job` field was written with the
  ticket (R19); the id is written once, never overwritten, only by the identity that asked for the
  ticket and only before the claim; a retry is a new run id with a record of its own; a second
  identity cannot register another's job; an id that does not belong to the job the record names is
  refused; only the launcher class is admitted and every other class is refused — a roster service
  account is a launcher (D3123, R20); no token holder can write the record, by the database rules;
  the route is mounted at the service's door — the path joins `servedRoute`'s list and the refusal
  that answers a path the service does not serve names it — and answers the service's `not_served`
  where the identity configuration is not wired (C1's stub answer); each refusal writes one audit
  line. If the rules' version has to move,
  `fugaro update` carries it and a test shows an installation at the previous rules version
  converging.
- Files: `internal/credsvc/batchclaim_register.go` (new),
  `internal/credsvc/batchclaim_register_test.go` (new), `internal/credsvc/server.go`,
  `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/testdata/rules.golden.json`, the
  rules' own tests beside them.
- Interfaces: consumes C1 (the route, its bodies, the record's two fields).
- Tests: `TestTheLauncherThatAskedForTheTicketRegistersTheJob`,
  `TestAnotherIdentityCannotRegisterTheJob`, `TestTheJobIdIsWrittenOnce`,
  `TestAnIdThatIsNotTheJobsIsRefused`, `TestARetryIsANewRunId`,
  `TestOnlyTheLauncherClassRegisters`, `TestNoTokenHolderWritesTheRegistration`,
  `TestTheRegistrationRouteAnswersNotServedUntilItIsWired`,
  `TestEachRegistrationRefusalWritesOneAuditLine`.
- Command: `go test ./internal/credsvc/ -run 'LauncherThatAskedForTheTicket|AnotherIdentityCannotRegister|JobIdIsWrittenOnce|NotTheJobsIsRefused|RetryIsANewRunId|OnlyTheLauncherClassRegisters|RegistrationRouteAnswersNotServed|RegistrationRefusalWritesOne' -count=1 && go test ./internal/budget/... -run 'Rules' -count=1 && go test -tags firebase ./internal/budget/rules/ -run NoTokenHolderWritesTheRegistration -count=1` (the last needs the rules emulator the `firebase` tag's tests already use; no live project).
- Red step: a run's own token writes the record's job id.
- Depends on: C1; BR2 (kept: one file, `server.go`).
- Commit: `credsvc: the launcher that asked for a ticket registers the job it created (BR17, D3108)`.
- Reviewer tier: opus, mandatory independent review.

### BR23: the key-release code retires (`claim-attestation`)
**New in LM19.** A merge is on `main` and cannot be un-merged by a document, so the amendment plans
the retirement: the code the old carrier was built on goes, in one task, after the sides that used
it have stopped. Until it runs, that code is in the tree and no acceptance of this plan depends on
it (R21).
- Acceptance: the wrapped envelope and its size bound, the release wire with `JobLink`, `KeyOpener`,
  `Registry` and its two registration paths, the stub that answers the release route, the key fact
  with `ParseSealKeyPublicKey` and its block type, the settings registry's entry for it, the schema
  entry and the golden's line, and `contract-key-release.md` are all gone; the names that stay go
  with the work that keeps them: the job-name shape and `checkRunRef` were moved into the
  contract's file by C1, and the opt-in fact with its three states, the deploy input and the
  block's validation entry points stay in the files this plan names (C1's, BR17's and
  `internal/localcfg/batch.go`, which this task renames from `batch_sealkey.go` rather than
  deleting); `git grep` finds no user of a deleted name outside the contract's files, the
  registration's file and the two places that name the retired key as text without a field of it —
  the block writer (BR16's, which this task waits on for exactly that) and this task's own reader,
  which skips the one retired key so that a block an un-updated machine still holds loads; the
  deletion is refused for a name still referenced, so the task lands only after BR1, BR2, BR4, BR16
  and BR17; `docs/` carries no sentence describing a Batch run's key.
- Files: `internal/budget/token/wrap_contract.go` (deleted), `internal/budget/token/wrap_contract_test.go`
  (deleted), `internal/credsvc/sealkey_wire.go` (deleted), `internal/credsvc/sealkey_wire_test.go`
  (deleted), `internal/credsvc/sealkey_stub.go` (deleted), `internal/credsvc/sealkey_stub_test.go`
  (deleted), `docs/batch-run-identity/contract-key-release.md` (deleted),
  `internal/localcfg/batch_sealkey.go` → `internal/localcfg/batch.go` (the key fact's half deleted,
  the opt-in fact and the job-name shape kept), `internal/localcfg/batch_sealkey_test.go` →
  `internal/localcfg/batch_test.go`, `internal/localcfg/localcfg.go` (the block's key field and its
  validation), `internal/localcfg/shared_test.go`,
  `internal/localcfg/testdata/shared.golden.yaml`, `internal/settings/registry.go`,
  `schemas/settings.schema.json`, `internal/cli/sharedcfg.go`,
  `internal/cli/sharedcfg_validate_test.go`, `internal/cli/writers_labels.go` (the two
  `batch.seal_key.*` rows). Past five; eight are a deletion, a rename or a line.
- Interfaces: reads C1 (the shapes that survive the deletion, and the file the job-name shape moved
  to) — an edge onto it would have to stand in place of a break, and this task has none — and
  consumes BR16's file, where the writer stops naming the key, and the block's own file, which it
  renames.
- Tests: `TestNoConstructorNamesTheSealKey`, `TestTheSettingsRegistryHasNoBatchKeyFact`,
  `TestAnOldBlockWithAKeyFactStillLoads`, `TestTheOptInFactStaysWhereItMoved`.
- Command: `go build ./... && ! git grep -nE 'BatchSealKey|ParseSealKeyPublicKey|FUGARO_BUDGET_TOKEN_WRAPPED|sealkey_wire|wrap_contract' -- '*.go' ':!*_test.go' && go test ./internal/localcfg/ -run 'OldBlockWithAKeyFact|OptInFactStays' -count=1 && go test ./internal/settings/ -run RegistryCoversEveryConfigField -count=1 && go test ./internal/backend/batch/ ./internal/credsvc/ -count=1`.
  The grep names the identifiers this task deletes, **not the words `seal` and `key`**, which code
  this task does not own spells: `ErrSealKey` and the Cloud Run seal in `internal/budget/token/`
  stay until the launcher plan's LM18, and the Batch backend's `SealKey` option is a field of
  `internal/backend/batch/seal.go`, BR1's file. Nothing is excluded for the two places the
  acceptance keeps: both name the retired key as text, and a text mention matches none of the five
  names, so the grep stays able to fail on a real user — `internal/cli/init_compute.go`, BR16's
  writer, included: this task waits on BR16 for it.
- Red step: the build fails on a deleted file's last user.
- Depends on: BR1, BR2, BR4, BR16, BR17 and BR19 (kept: each stops using the code this deletes or
  removes a name of it, one or two files at a time). **It does not wait on C1, where it did until
  now**: a wait on a contract stands in place of an edge the contract broke and this task has none
  left — its old wait on BR16 is the behaviour this task keeps, since `internal/cli/init_compute.go`
  names `Batch.SealKey` and `localcfg.BatchSealKey` today, and only BR16 removes them, so a deletion
  that landed first would leave the build broken in a file outside this task's footprint. The
  shapes C1 declares it still reads are in the contract's own file and in the files this task
  renames; a wait on them would state a dependency no break stands behind. It starts after the
  owner has run Check 830, so that the link's read is the one the surviving code names (R4).
- Commit: `batch: the key-release code retires (BR23, R21, D3101 as amended)`.
- Reviewer tier: opus, mandatory independent review.

### I1: integration (all)
**Rewritten by LM19**: the real link and the registration are wired, the stub's answer for the
registration route is replaced, and the merged code's retirement is BR23's rather than this task's.
- Acceptance: the real `VMIdentity` and the registration are wired into the service where
  `credentials serve` builds its options, the stub answers the registration instead of `not_served`
  and `internal/credsvc/identity_stub.go` — C1's one stub — is deleted; `batchOptions` fills the
  backend from the block and the configuration; end to end
  and hermetically, a Batch launch on an opted-in installation reaches an open session, an
  installation at the previous version converges under `fugaro update`, a Batch run in flight
  across that update keeps running, no resource was created per run, and a second job carrying the
  first run's ticket claims nothing. **The claim itself is the launcher plan's issuer (its C1, its
  I1): this plan's I1 cannot pass before that plan's I1 has merged, and the graph cannot draw that
  edge (§7).**
- Files: `internal/credsvc/identity_stub.go` (deleted), `internal/credsvc/server.go`,
  `internal/cli/credentials_serve.go`, `internal/cli/cloud.go`,
  `internal/cli/batch_claim_end_to_end_test.go` (new),
  `internal/cli/update_in_flight_batch_test.go` (new). `credentials_serve.go` is the launcher
  plan's I1 file and `cloud.go` its LM6's environment: I1 starts after both merge (§7).
- Tests: `TestABatchRunClaimsItsIdentityEndToEnd`, `TestASecondJobCannotClaimTheFirstRunsIdentity`,
  `TestARecreatedJobCannotClaimTheFirstRunsIdentity`,
  `TestUpdateConvergesABatchInstallationAtThePreviousVersion`,
  `TestUpdateLeavesABatchRunInFlightRunning`, `TestNothingIsCreatedPerBatchRun`.
- Command: `go build ./... && go vet ./... && go test ./internal/cli/ -run 'BatchRunClaimsItsIdentity|SecondJobCannotClaim|RecreatedJobCannotClaim|ConvergesABatchInstallation|LeavesABatchRunInFlight|NothingIsCreatedPerBatchRun' -count=1`.
- Red step: the registration route answers `not_served` while a launch waits for its id.
- Depends on: C1, `R-C1-link-permission` and every task: BRS, BR0 to BR23.
- Commit: `batch: a run on Cloud Batch gets its identity, end to end (I1, D3100)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

**Levels.** Tasks in one level run at once.

| Level | Tasks |
| --- | --- |
| 0 | C1, BRS, BR0 |
| 1 | BR1, BR2, BR4, BR6, BR7, BR8, BR9, BR13, BR14, BR19; and `R-C1-link-permission` and `R-C1-project-job-account` once the owner has run Check 830 and answered O1 |
| 2 | BR3, BR5, BR10, BR11, BR12, BR16, BR17 |
| 3 | BR15, BR18, BR21, BR22, BR23 |
| 4 | BR20 |
| 5 | I1 |

`R-C1-project-job-account` runs only if BR10 raises it, against the same document as
`R-C1-link-permission`, and starts only after that one has merged.

The levels are the ones `fugaro plan graph --plan docs/batch-run-identity/implementation-plan.md`
prints from the artifact beside this file, `implementation-plan.graph.json`: six levels, its
critical path BR0 → `R-C1-link-permission` → BR12 → BR18 → BR20 → I1, and its bottleneck BR0.

**Edges broken by C1**, each because the dependent needs a shape and not a behaviour:

| Was | Why the contract is enough |
| --- | --- |
| BR16 after BR5 | the update needs the root's output and input names, not the resources |
| BR1 after BR16 | the backend needs the block's opt-in fact, not the update that writes it |
| BR7 after BR16 | doctor needs the same fact |
| BR2 after BR1 | the service needs the attestation's shape, not the launcher that sends one |
| BR3 after BR2 | the link implements an interface the claim calls; it does not need the claim |
| BR4 after BR2 | the runner needs the claim's request shape, not the claim |
| BR12 after BR2 | the documents quote names, not code |
| BR13 after BR4 | the script reads names, not the runner |
| BR14 after BR1 | the reaper needs the ticket's name, not the code that writes it |
| BR17 after BR1 | the registration needs the route's bodies, not the launcher that calls it |
| BR6 after BR5 | the update's stage needs the opt-in fact and the root's input names, not its resources |
| BR19 after BR16 | the merge needs the fact's shape, not the update that writes it |

**Edges kept:**

| Edge | Why a contract would not do |
| --- | --- |
| BR1, BR8, BR10, BR11 after BRS | one file: each owns a helper BRS creates |
| BR9 after BRS | the one name BRS defines; a contract for one constant would cost more than the edge |
| BR11 after BR1 | two files, `launch_batch.go` and `launch_batch_test.go`: BR11 amends the launcher BR1 creates |
| `R-C1-link-permission` after BR0; BR3, BR5 and BR12 after it | data: the owner's measurement and his answer decide which fact is the link and which permission reads it (R4, O1) |
| BR16 after BR6 | one file, `init_compute.go` |
| BR21 after BR16 and BR5 | one file, `init_compute.go`; and behaviour: it applies the root with the input BR5 adds |
| BR22 after BR17 and BR5 | one file each: `batchclaim_register.go`; `.claude/rules/credentials-service.md` and the golden plans |
| BR20 after BR18 and BR11 | two files, `backends.go` and `docs/backends.md`; and one file, `launch_batch.go` |
| BR17 after BR2 | one file, `server.go` |
| BR15 after BR7 and BR12 | one file each, `doctor_batch.go` and `SECURITY.md` |
| BR18 after BR7 and BR12 | behaviour: it reads which failure BR7's check reports; and one file, `docs/backends.md` |
| BR10 after BR8 | behaviour: a project job's environment is what BR8 builds |
| BR13 after BR0 | two files, the checklist and the scripts' list |
| BR23 after BR1, BR2, BR4, BR16 and BR17 | behaviour: each stops using the code it deletes, and a deletion that lands first leaves the build broken; BR16's block writer names the retired key's field and its type, which this task removes |
| BR23 after BR19 | two files, `sharedcfg.go` and `shared_test.go` |
| I1 after all | it wires the real sides and proves them together |

**The gain**, as `fugaro plan graph` reports it: 12 edges broken, 26 kept, 4 added; the critical
path was 8 tasks of weight 16 and is 6 tasks of weight 11. **The critical path** is BR0,
`R-C1-link-permission`, BR12, BR18, BR20, I1 — and BR0 is the bottleneck: between it and the
contract change stand the owner's run of Check 830 and his answer on the second read, and the whole
of the claim now stands on what it measures. Everything that does not need that answer runs
meanwhile.

**Shared files.** No level holds two writers of one file.

| File | Writers (level) |
| --- | --- |
| `internal/backend/batch/run.go` | BRS (0), BR10 (2) |
| `internal/backend/batch/batch.go` | BRS (0) |
| `internal/backend/batch/seal.go` | BRS (0), BR1 (1) |
| `internal/backend/batch/env.go` | BRS (0), BR8 (1) |
| `internal/backend/batch/account.go` | BRS (0), BR10 (2) |
| `internal/backend/batch/secrets.go` | BRS (0), BR11 (2) |
| `internal/credsvc/server.go` | BR17 (2), I1 (5) |
| `internal/credsvc/audit.go` | BR2 (1) |
| `internal/credsvc/identity_claim.go` | BR2 (1) |
| `internal/gcpfake/batch.go`, `internal/gcpfake/compute.go` | BR3 (2) |
| `internal/backend/batch/reap.go` | BR14 (1) |
| `internal/cli/docs_budget_token_channel_test.go` | BRS (0), BR1 (1) |
| `deploy/terraform/secret_readers_test.go` | BRS (0), BR11 (2), BR15 (3) |
| `docs/secrets-brokering/secret-readers.md` | BR11 (2), BR15 (3) |
| `internal/cli/docs_security_test.go` | BR12 (2), BR15 (3) |
| `deploy/terraform/terraform_test.go` | BR5 (2) |
| `docs/budget-token-channel/spec.md`, `docs/compute-backend/spec.md` | BR1 (1) |
| `.claude/rules/credentials-service.md` | BR5 (2), BR22 (3) |
| `internal/credsvc/batchclaim_register.go` | BR17 (2), BR22 (3) |
| `internal/cli/credentials_serve.go` | BR22 (3), I1 (5) |
| `internal/cli/init_credentials.go`, `internal/infra/tfvars.go`, the installation module's `credentials.tf` | BR22 (3) |
| `internal/infra/spec.go`, `internal/backend/launchenv.go` | BR8 (1) |
| `scripts/live_checks_test.go` | BR13 (1) |
| `internal/cli/doctor_batch.go`, `internal/cli/doctor_batch_test.go` | BR7 (1), BR15 (3) |
| `internal/cli/doctor_secret_readers.go`, `docs/secrets-brokering/reconciliation.md` | BR15 (3) |
| `internal/cli/validate_batch.go` | BR7 (1) |
| `internal/cli/backends.go` | BR18 (3), BR20 (4) |
| `internal/cli/backends_use_test.go` | BR18 (3) |
| `internal/cli/run.go`, `internal/cli/cloud_openers_test.go` | BR11 (2) |
| `internal/runner/budget_session.go`, `internal/gitops/gitops.go` | BR4 (1) |
| `internal/runner/projectjob.go` | BR9 (1) |
| `internal/cli/cloud.go` | I1 (5) |
| `internal/cli/init_compute.go` | BR6 (1), BR16 (2), BR21 (3) |
| `internal/apply/steps.go` | BR6 (1), BR21 (3) |
| `internal/cli/init_stages.go`, `internal/cli/update.go` | BR6 (1) |
| `internal/localcfg/localcfg.go` | BR23 (3, the block's key field and its validation) |
| `internal/infra/compute.go`, the release notes | BR16 (2) |
| `internal/localcfg/shared.go` | BR19 (1) |
| `internal/localcfg/shared_test.go`, `internal/cli/sharedcfg.go`, `internal/cli/sharedcfg_validate_test.go` | BR19 (1), BR23 (3) |
| `internal/cli/launch_batch.go` | BR1 (1), BR11 (2), BR20 (4) |
| `internal/cli/launch_batch_test.go` | BR1 (1), BR11 (2) |
| `docs/design/update-in-flight.md` | BR21 (3) |
| `internal/infra/tf/cover.go`, the compute module and root | BR5 (2) |
| `internal/settings/registry.go`, `schemas/settings.schema.json`, `internal/localcfg/testdata/shared.golden.yaml`, `internal/cli/writers_labels.go` | BR23 (3) |
| `internal/identitywire/wire.go` | C1 (0) |
| `internal/identitywire/batchclaim.go`, the contract document | C1 (0), `R-C1-link-permission` (1); `R-C1-project-job-account`, if BR10 raises it, after `R-C1-link-permission` has merged |
| `internal/credsvc/identity_config.go` | C1 (0, the interface the link fills) |
| `internal/credsvc/identity_stub.go` | C1 (0, its one stub), I1 (5, deleted) |
| `internal/budget/token/wrap_contract.go`, `internal/credsvc/sealkey_wire.go`, `internal/credsvc/sealkey_stub.go`, `docs/batch-run-identity/contract-key-release.md` | BR23 (3, all deleted) |
| `internal/localcfg/batch.go` (renamed from `internal/localcfg/batch_sealkey.go`) | BR23 (3) |
| `docs/gcp-live-checklist.md` | BR13 (1) |
| `docs/backends.md` | BR12 (2), BR18 (3), BR20 (4) |
| `SECURITY.md`, `website/content/security.md` | BR12 (2), BR15 (3) |

**Files another plan also writes.** `internal/cli/launch_identity.go` is the launcher plan's LM6;
`internal/credsvc/identity_claim.go` its LM2; `internal/runner/identity_claim.go` its LM7;
`internal/cli/init_stages.go` its LM14; `internal/cli/credentials_serve.go` its LM22 and its I1;
`SECURITY.md` and `website/content/security.md` its LM16; `internal/infra/tfvars.go` its LM13. Each
of BR1, BR2, BR4, BR6, BR12, BR15, BR22 and I1 starts after the task that owns the file it edits
has merged, or rebases on it — **the waits the graph cannot draw, listed in section 7.** Also AB9's
in always-a-budget, which is approved: `internal/backend/batch/run.go`, `internal/cli/doctor_batch.go`,
`internal/cli/validate_batch.go`, `docs/backends.md` and `docs/budget-token-channel/spec.md`. A
launcher checks for an open pull request on each before starting BRS, BR1, BR7 and BR12.

## 6. The estimate

28 runs: one contract, the contract change that always runs, 25 tasks and one integration.
`R-C1-project-job-account` runs only if raised and is not counted. At the repository's own caps the
worst case is 28 times one run's cap, plus 28 independent reviews. No run is proposed for launch
here: nothing starts until the owner approves the amended answers and tasks by name.

## 7. Contract changes anticipated, and the waits the graph cannot draw

- **`R-C1-link-permission`. It always runs**, and is counted in the 28. Once Check 830 has named
  the link's read and the owner has answered O1, it writes that read into the contract. BR3, BR5
  and BR12 wait on it. If the owner's answer is a release service of its own, it does not run:
  D3102 is reversed and this plan is re-sliced first.
- **`R-C1-project-job-account`.** If `LaunchSpec` cannot name the project job's account (BR10), it
  records the field in the contract document. It reaches BR10. It writes the same document as
  `R-C1-link-permission` and starts only after that one has merged.

The second runs only if raised and is not counted.

**The waits into the launcher plan.** An edge or a change request may only name a node of this
plan (that plan's O11), so these eight waits stand in prose and in each task's `Depends on` line.
They are also why D3113 is asked: the two plans share files, and neither graph can hold the other's
nodes.

| Task | Waits for | Why |
| --- | --- | --- |
| C1 | that plan's C1 (merged) | it adds two fields, one route and one interface field to a contract that is on `main` |
| BR1 | its LM6 (`internal/cli/launch_identity.go`) | the ticket ask is LM6's, and BR1 adds the job's name to it |
| BR2 | its LM2 (`internal/credsvc/identity_claim.go`) | the claim route is LM2's, and BR2 is the check it makes before signing |
| BR4 | its LM7 (`internal/runner/identity_claim.go`) | the claim is LM7's, and BR4 adds the attestation to it |
| BR6 | its LM14 (`internal/cli/init_stages.go`) | the stage list is LM14's |
| BR12, BR15 | its LM16 (`SECURITY.md`, the website page) | the two plans' documents are the same two files |
| BR22 | its LM13 (`internal/infra/tfvars.go`) and its LM22 (`credentials_serve.go`) | the deploy input and the service's environment are theirs |
| I1 | its I1 (`credentials_serve.go`, the issuer) | there is no claim to prove end to end until its issuer is wired |

## 8. Checks the plan takes

From the reserved block 830 to 839:

| Check | Measures | Who | When |
| --- | --- | --- | --- |
| 830 | What links a Batch VM to its job and which permission reads it, whether a launcher can forge it, what the identity token carries, where Batch writes variables, how soon a job's name can be reused (U1, U2, U3, U8). Merged as BR0. **Run 2026-11-25: FAIL** — `probe-ran` and `u1-handmade`, so U2 and U3 are unmeasured; O1 and O8 are answered and R4 returns the design to the owner, and BR3, BR5 and BR12 still wait | the owner | before `R-C1-link-permission`, BR2, BR3, BR5, BR12, BR13 and BR23 start |
| 831 | The brief's item 6: launch, session, metering, and a second launcher failing four ways on the claim (R11, D3108, D3115) | the owner | after the release, on an updated `fugaro-dev` |

832 to 839 are reserved and unused. Check 124 stays as it is; 831 does not replace its preemption
and cost parts.

## 9. After-deploy steps

One, written in BR16's own pull request under `### For operators`: run `fugaro update`. It applies
the compute root where an operator has opted the installation in, removes any key fact the release
behind wrote into the block, feeds the root the project job accounts, and deploys the service with
the switch the claim's two refusals read. Choosing Batch (`fugaro backends use cloud-batch`, at an
operator's own terminal) is an opt-in to a feature and is described in the feature notes, not as an
upgrade step. It asks nothing. Nothing this plan creates is a secret or a key, and nothing per run
is created at all; the KMS key and key ring the plan asked for before the amendment are **not**
created, by this plan or by the release behind it, and nothing asks anyone to delete one.

The launcher plan's own after-deploy steps (its LM14's grant move, its LM18's removal of the
bridge) are its own and are not repeated here; its release notes are the ones that carry them.

## 10. Considered and declined

- **Removing the Batch backend**, carrier only, and parking (D3100's other options).
- **Keeping K3, the seal key released to the run's own VM** (D3101 before the amendment): it needs
  a KMS key, a ring, a wrapped blob, a release route and a decrypt grant to serve an identity the
  issuer serves anyway, and it admits the printed-variable replay only through a second guard. The
  amendment's carrier is the attestation, and the key plays no part (D3101 as amended, D3114).
- **A claim route of Batch's own**, instead of the two fields and the one route on the identity
  wire (D3113): two claim paths where requirement 2 asks for one scheme.
- **Leaving the fields to the launcher plan to declare** (D3113): a second contract there, a second
  go, and this plan's C1 waiting on another plan's task.
- **Serving a claim whose record names a job with no attestation** (D3114): the account check alone
  is exactly the replay F4 describes.
- **Keeping the ticket off the Batch job** (D3115): no carrier is left, which is K3 again; and
  **a per-run Secret Manager secret**: F5's (a), declined.
- **K1, `encryptedVariables`**, and **K2, a per-run secret with an expire time** (D3101 as it was
  first asked): a second launcher replays either (F4).
- **A separate release service** (D3102).
- **Batch for per-repository jobs only** (D3103), and **mounts on a Batch job** (D3104).
- **Falling back to K1 if Check 830 fails** (R4): the work returns to the owner instead.
- **Letting a queued run start on a dead ticket with no reaper deadline** (D3106 as amended).
- **Reversing D3103 to avoid the `actAs` widening** (D3105).
- **Writing the key fact from the root and failing doctor on a differing one** (R13, dropped with
  the key fact).
- **An opt-in that is never withdrawn** (the recommendation of D3111): the owner chose an opt-out.
- **Accepting a stale launch after an opt-out, and a launch that reads the cloud** (D3112's other
  options).
- **Deleting the custom role on an opt-out**: its id stays taken for seven days, so opting in again
  would fail; its binding is removed instead (R16).
- **Listing Batch jobs to find a run in flight**: the operator's Batch grant is conditioned on a
  job-name prefix, which a list may not satisfy; the run records answer without a new grant.
- **Triggering the apply by the updating machine's own `backend:` key** (D3110 as first answered):
  it is never published (F32).
- **Applying the compute root wherever a `batch:` block exists** (the recommendation of D3110): the
  owner chose that nothing is applied until an operator opts the installation in (D3110 as
  amended).
- **Refusing a service-account launch on Batch, and dropping the registration** (D3109, before its
  supersession): the issuer admits a roster service account (D3123, D3137, R20).
- **Keeping D2075's mount for workflow secrets** (D3107), and **accepting the re-created job**
  (D3108).
- **A record written at first release**: it names whichever job asks first (F30); the launcher
  registers the id instead.
- **Retiring the merged key-release code as part of another task**: a deletion folded into a task
  that also changes behaviour is a deletion nobody reviews on its own (BR23).
- **A per-run service account**: what phase 3 removed, with an account to delete per run.
- **Binding the run's identity to the job's unique id at the ticket**: Google assigns it at
  creation, after the ticket is issued; the registration writes it once it exists (D3108, R19).
