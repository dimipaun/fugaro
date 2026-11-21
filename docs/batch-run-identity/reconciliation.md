# A run on Cloud Batch gets its identity: reconciliation

**Where this document disagrees with [spec.md](spec.md), this document wins.** Checked against
`origin/main` at `d57d71ddb` on 2026-11-20, and rebased onto `0a8b4e221` the same day. The
always-a-budget documents were read on branch `docs/always-a-budget` at `8bf13231f`; they have
since merged (#3302, #3305), and its tasks are approved and launching, AB9 among them. Cloud Batch,
Secret Manager, Cloud KMS and IAM documentation was fetched on 2026-11-20; what a fetched page did
not state is under UNVERIFIED, never in a finding.

**The amendment of 2026-11-21 (LM19 of [launcher-does-not-mint](../launcher-does-not-mint/README.md)).**
This document was written on 2026-11-20, when the answer to "where does a run's identity come
from" was a launcher-minted token the run opens with a key nobody else may obtain. The next day the
owner ruled, in another plan, that **a launcher does not mint a run's identity**: the credentials
service issues a one-time ticket, the launcher sets it on the execution, and the run claims its
identity from the service (its D3122). Under D3129 he then held every task of this plan that
touches the seal key or its C1 until that plan's **LM19** re-planned them, which is this
document's amendment. Two things merged in between and are read as merged: **this plan's C1**
(`internal/budget/token/wrap_contract.go`, `internal/credsvc/sealkey_wire.go`, its stub, the
`batch:` block's two facts, the settings entry and [contract-key-release.md](contract-key-release.md),
#3380, 2026-11-20) and **BRS** (#3379, the job definition split along its seams).

The amended reading: the release is re-planned as a **VM-attested claim on that plan's C1**. A
Batch run claims its identity exactly as a Cloud Run run does — one ticket, one claim route, one
signed token, one identity scheme — and the claim is bound to the job by the VM that makes it. The
claim must come from a VM of the very Batch job the launcher recorded when it asked for the ticket,
so a second launcher who reads the ticket out of the job (Batch prints a job's variables, F3)
cannot use it: their VM is another job's. What that costs is stated in the amendment section below:
the KMS key, the wrapped blob, the release route, the key fact in the block and its settings entry
all go, and with them four of this plan's tasks and half of two more; the opt-in fact, the
registration's intent and Check 830 stay. **D3101, D3102, D3106, D3108, D3110 and D3112 are amended
and D3109 is superseded by that plan's D3123; D3113 to D3115 are new. Every one of them is put to
the owner, marked in the Decisions table and listed in one place below.** Nothing here is approved:
the amendment re-opens answers, so C1's and BR6's approvals of 2026-11-20 are on hold (that plan's
O16) and the re-cut asks for a go of its own.

## What the amendment does, answer by answer

Read this before the tables below: it is what changed, and the tables are the detail.

- **The carrier is the claim (D3101, amended).** K3's object was the seal key: a KMS key, a blob
  the launcher wrapped to it, and a release route that opened it for one VM. The owner's later
  ruling removes the thing being carried — the run no longer opens a token, it is issued an
  identity — so the VM attestation guards the claim instead of the key. K3's requirement is met more
  cheaply: there is no key to hold, nothing per run at rest, and no new at-rest secret at all.
- **The service is the same one (D3102, amended in wording).** The release lived in the credentials
  service; the attestation does too, at the claim route the other plan's C1 already mounts. What
  the service needs reads for is unchanged in kind and smaller in number: the job (`batch.jobs.get`,
  conditioned on the Fugaro job-name prefix) and Check 830's link read (O1), and **no** decrypt on
  a key. The recommendation stands; the wording is put to the owner because the grants in
  `.claude/rules/credentials-service.md` change with it.
- **The registration keeps its intent and changes its object (D3108, amended).** It registered a
  blob's digest and then the job's Google-assigned id, so that a job re-created under the same name
  received nothing. It now registers the job on the **issuance record** the other plan's C1 already
  writes: the launcher names the job it is about to create in its ticket ask, and after the create
  returns it names the job's id on a route of this plan's own. The two calls, the once-only writes,
  the launcher class alone and the re-created job's refusal all stand; they are written on a record
  that already exists rather than on the budget record, and they carry no digest because a claim
  carries no blob.
- **The ticket is a job variable, and Batch prints it (F3, new D3115).** The old posture was
  absolute — the plain key is never a variable of a Batch job — and the ticket is one. That is
  acceptable only because the ticket alone claims nothing: the claim must come from the recorded
  job's own VM. Batch's F3 and U3 are re-read accordingly, and the check measures that a ticket
  read out of a job is not enough to claim.
- **The opt-in fact and the update's side stay (D3110, D3111).** The compute root still holds the
  link's permission, the shared project job accounts and the launchers' `actAs` (D3105), so the
  published opt-in fact, its three states, the opt-out and the update's convergence are all as
  before; only the key fact leaves the block. D3110's answer stands and its wording loses "and
  create the key"; D3111's stands and R16 is re-cut for what an opt-out now removes.
- **A service account may launch on Batch (D3109, superseded by D3123).** D3109 answered that the
  registration is for people on the launcher roster only, so a service account could not launch on
  Batch. The other plan's D3123 admits a `serviceAccount:` roster entry as a class at the issuer,
  and D3137 admits it unscoped, so the ticket and the attestation serve it like anyone else. The
  later, wider answer wins, and this is recorded rather than asked again.
- **Four tasks go and the rest are rewritten (F18 of the other plan, task by task).** BR2's release
  route, BR3's `JobLink`, BR4's fetch and BR17's digest registration were built on the key; the
  sections below say for each of the twenty-two tasks that plan's F18 lists whether it is rewritten,
  kept or dropped, read from its own Depends line. Nothing that is free of the key changes (BRS,
  BR0, BR8 to BR11), which is what D3129 said could go on.
- **The merged code retires in a task of its own (BR23).** A merge is on `main` and cannot be
  un-merged by a document, so the amendment plans the retirement: the envelope, the wire, its stub,
  the key fact, the settings entry, the schema and the golden go, in one task, after the sides that
  used them have stopped. Until it runs, the merged code is dead weight that no task's acceptance
  depends on.

### Task by task, from each Depends line (the other plan's F18)

The list is that finding's own list, read on `main` at `42d2dc568`. "Free of both" is what D3129
let go on.

| Task | Built on the key or on C1? | What the amendment does |
| --- | --- | --- |
| C1 | it *is* the contract | **Re-cut.** The wire, the release route and the key fact go; the opt-in fact and the classes stay; the delta (the attestation, the ticket's job, the job route) takes their place, in a new document, `contract-batch-claim.md`, while `contract-key-release.md` becomes BR23's to delete. Rewritten, same id, same level |
| BR1 | yes (C1, the block's key fact) | **Rewritten.** The Batch launch carries the ticket and refuses a sealed entry; it no longer wraps |
| BR2 | yes (the release route) | **Rewritten.** The route becomes the attestation the claim route calls; same id and level, new object |
| BR3 | yes (`JobLink`) | **Rewritten.** The link is unchanged in substance — it is what decides a claim now — and is written against the delta |
| BR4 | yes (the fetch) | **Rewritten.** The runner attaches its VM's identity token to the claim instead of fetching a key |
| BR5 | yes (the key and its grants) | **Rewritten.** The root keeps the link's permission and the accounts; the KMS key, its ring and the decrypt grant go |
| BR6 | yes (C1's opt-in fact) | **Kept.** It applies the compute root where the installation opted in; the key fact it was to write is BR16's and goes there |
| BR7 | yes (the block's key fact) | **Rewritten.** Doctor and validate speak about the opt-in fact and the link, not a key |
| BR12 | yes (C1) | **Rewritten.** The documents say how a Batch run's identity is claimed, and that a launcher can no longer mint (the other plan's D3126, D3127) |
| BR13 | yes (C1, BR4) | **Rewritten.** Check 831's four attacks are four attacks on the claim: reading the job, replaying the ticket into a job of one's own, claiming from another VM, re-creating the job under the same name |
| BR14 | yes (the marker) | **Rewritten.** D3106's rule keeps its object's *shape* and changes it: the reaper ends a job queued past its **ticket's** life |
| BR15 | through BR7 and BR12 | **Kept.** Doctor's Batch secrets check and the readers' table row go, as D3107 said |
| BR16 | yes (C1's key fact) | **Rewritten.** The block's opt-in fact, `batch.secrets` and the accounts stay; the key fact's half goes |
| BR17 | yes (the digest registration) | **Rewritten.** The registration writes the job on the issuance record: name at the ticket, id after the create |
| BR18 | yes (BR7's check) | **Kept.** Selecting the backend still waits for nothing only the update can fix |
| BR19 | yes (both facts) | **Rewritten.** Publishing keeps the opt-in fact; there is no key fact left to keep |
| BR20 | yes (C1's three states) | **Kept.** An operator still opts out |
| BR21 | yes (BR5's input) | **Rewritten.** The retirement removes the role's binding, the job read and the shared accounts' grants; there is no decrypt to remove |
| BR22 | yes (C1) | **Rewritten.** The service still refuses where Batch is off; the refusal moves to the ticket and the registration, before any job exists |
| `R-C1-link-permission` | yes (the wire and the document) | **Kept.** It writes Check 830's fact and O1's read into the re-cut contract; the file it writes changes, its object does not |
| `R-C1-project-job-account` | yes (the document) | **Kept**, and still runs only if BR10 raises it |
| I1 | it wires them | **Rewritten.** It wires the attestation and proves the claim end to end; the merged key-release code's retirement is BR23, so the only stub I1 deletes is the re-cut C1's own (`internal/credsvc/identity_stub.go`) |
| **BRS** | **free** | **Merged** (#3379). Kept exactly as merged: the four helpers, the deployed-job name |
| **BR0** | **free** | **Kept**, and its Check 830 is more load-bearing than it was: the link is now the whole of the claim, not one guard on a key release |
| **BR8, BR9, BR10, BR11** | **free** | **Kept as written.** F8, F9, F10 and D3103/D3104 are untouched by the claim; each still depends on BRS alone (BR10 on BR8) |

## Modules

The owner approved this map on 2026-11-20; `key-release` is renamed by the amendment and
`update-converge` no longer depends on a key. Every finding, decision and task names one id.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `claim-attestation` | The launcher names the job it will create and registers it; the claim carries the VM's identity; the service serves the identity only to the recorded job's own VM | nothing |
| `job-environment` | A Batch job carries what a deployed job's template carries; the runner places a Batch run | nothing |
| `project-jobs` | Batch runs a workflow that belongs to a shared project job | `job-environment` |
| `service-credentials` | A Batch run's model and git credentials come from the credentials service alone | nothing |
| `update-converge` | `fugaro update` applies the compute root and the grants; the refusal lifts where the claim is in place; an opt-out removes the grants | nothing |
| `live-proof` | Checks 830 and 831, the documents, `SECURITY.md` | all |

## Assumptions

- A1. "Its token" is the budget token. **Amended:** it is no longer minted by the launcher and
  sealed in `tokens/<slug>/<run>/budget-token`; the run is issued an identity by the credentials
  service, and what a Batch job carries is the ticket, not a key to anything.
- A2. "Launcher" is a person or account holding the installation's launcher grants, not the
  operator or the project owner. An owner can grant themselves anything, on either backend.
  **The two words are this document's, not the role names.** "Launcher" here is the role
  `operator` (the local config's `terraform.operators`, Terraform's `var.launchers`): a person who
  starts runs. "Operator" here is the role `admin` (`terraform.admins`, Terraform's
  `var.operators`): a person who applies Terraform and holds `objectAdmin` on the runs bucket
  (`google_storage_bucket_iam_member.runs`, `deploy/terraform/gcp/modules/installation/iam.tf`).
  A test named `NonOperator` in the plan means a person without the role `admin`.
- A3. "The run" includes the run's own agent. The agent shares the container with the runner.
  **Amended:** on Batch the agent can obtain its own VM's identity token (the metadata server is
  reachable from the task's container, U2) and can therefore claim this run's identity first. That
  is this run's identity either way, and it is what `docker: true` already means (O5).
- A4. "Nothing left behind per run" means no cloud resource a person must delete. **Amended:** the
  amendment creates none at all: the ticket is a variable on a job that goes when the job does, and
  the job's name and id are two fields on the issuance record the run already has.
- A5. "An installation on Batch" is one whose config carries a `batch:` block, whether or not it
  selects the backend. **Contradicted by D3110**: for `fugaro update`, it is one an operator has
  opted in. **Amended:** it is the same opt-in fact, and the block no longer carries a key.

No finding contradicts A1 to A4. A2 is what D3101 turned on; A2 is now what the ticket's class
turns on.

## What the brief names, and what the repository has

| The brief | The repository |
| --- | --- |
| "the key … travels on the execution" | `LaunchSpec.SecretEnv` (`internal/backend/backend.go`), the marker `FUGARO_BUDGET_TOKEN` whose value is the key (`sealMarker`). **Amended:** what travels is the ticket, `identitywire.TicketEnv` (`FUGARO_IDENTITY_TICKET`, the other plan's C1), which is a variable of the job on Batch. |
| "the Batch backend refuses any sealed launch" | `jobDefinition` in `internal/backend/batch/run.go`, on any `kv.Secret` entry (BRS moved it to `seal.go`) |
| "the refusal D3021 adds" | Task AB9 of always-a-budget: approved, not merged. **Amended:** it stays true — a Batch job is never sealed — and R8 is re-cut. |
| "the same credentials service admission" | `verifyJob` and `verifyRun`, `internal/credsvc/auth.go`, and now the claim route's own table (the other plan's LM2) |
| "the reaper" | `internal/backend/batch/reap.go`, role `google_project_iam_custom_role.reaper` |
| "its live check" | Check 124 (four parts, one script) and Check 57b, both NOT RUN |

## Findings

Findings are grouped by subject; their numbers follow the order they were found, so a group can
hold a late number. The same holds for resolutions, open items and UNVERIFIED. **A finding the
amendment re-read carries an "Amended:" line; a finding it adds is numbered from F34.**

### The platform (`claim-attestation`)

**F1 — Open, answered: Cloud Batch still has no per-execution place.** The v1 methods on a job are
`create`, `get`, `list`, `delete` and `cancel`. There is no run or execute verb, and no patch in
v1; v1alpha's patch "currently only supports increasing the value of the first `taskCount` field".
One job is one run, which is how this backend already uses it (`JobID` hashes the run id). The
release notes of 2025 and 2026 change nothing about secrets, KMS or identity. Consequence: the
earlier design's premise holds; D3101. **Amended:** a job created once and never patched is also
why the launcher must name the job in its ticket ask *before* the create — there is no way to put
the job's id on the job after the fact.

**F2 — Open, answered: Batch offers two secret carriers, and both are opened by the job's service
account.** `Environment.secretVariables` maps a name to a Secret Manager version path, and "The VM
will access the named secrets"; the role goes "to the job's service account".
`Environment.encryptedVariables` holds a KMS key name and a ciphertext, with no field for
additional authenticated data. Who decrypts is not documented (U2). Consequence: F4. **Amended:**
the amendment uses neither carrier; D3104's service-only rule stands for credentials, and the
ticket is an ordinary variable.

**F3 — Gap: Batch may print variables.** Google's own page says "Batch prints out variables
(including any secrets) in plain text" and that a secret "can be recorded in unauthorized places,
like Cloud Logging". Consequence: the plain seal key must never be a job variable of any kind, and
Check 830 reads the logs for the wrapped value (U3). **Amended, and it is the amendment's sharpest
edge:** the ticket *is* a job variable, so it is expected in the logs, in the instance's metadata
and in the `CreateJob` audit record. The rule that replaces the old one is the whole of D3115: a
ticket read out of a job is worth nothing on its own, because the claim must come from the recorded
job's own VM. Check 830 measures exactly that, and the plan's BR1 and BR13 pin it.

**F4 — Gap: any carrier the run account opens can be replayed by a second launcher.** A launcher
holds `roles/batch.jobsEditor` on the Fugaro job prefix and `roles/iam.serviceAccountUser` on the
run accounts (`launcher_jobs_editor`, `launcher_user`, `deploy/terraform/gcp/modules/compute/iam.tf`),
and reads the whole runs bucket (`runs_reader`, `modules/installation/iam.tf`). So a second
launcher can read the first job, create a job of their own under the same account that carries
the same secret reference or ciphertext, and have that run open it. That defeats requirement 1's
"no other launcher" for `secretVariables`, for `encryptedVariables`, and for any per-run secret
granted to the account. Consequence: only an opener that is one run's alone meets the requirement;
D3101. **Amended:** the same replay, one step on: the second launcher reads the first job's
**ticket** and creates a job of their own carrying it. What stops it is the same fact the KEY
release rested on — the VM making the claim must be the recorded job's VM — so F4's consequence
becomes "only an identity served to one run's own VM meets requirement 1" (D3101 as amended) and
its two carriers above stay declined.

**F24 — Gap, found in review round 1: a launcher can already mint the identity itself.** Every
launcher and operator holds the signer's `signJwt` (`google_service_account_iam_member.minter`,
`deploy/terraform/gcp/modules/firebase/iam.tf`, granted to launchers and operators alike), and
`docs/budget-token-channel/spec.md` puts that out of the seal's scope. So a second launcher can
mint a token for the first run's identity without the first run's key. K3 stops a launcher from
obtaining that key; it does not stop one from spending as that run. Against a launcher, K3's gain
over K1 is the letter of requirement 1, and a boundary only if minting is ever taken off
launchers. Against the reaper and other readers of the job, K1 and K3 are equal. Consequence: put
to the owner again; D3101 stands. **Amended, and closed by the other plan:** the owner ruled that a
launcher does not mint (its D3122, D3126, D3127), `fugaro update` takes the people's and the
orchestrator's signing rows away once the issuer answers (its LM14), and the CLI no longer signs
locally once the row is gone (its LM18). So what F24 called the boundary now exists, and this
plan's O6 is closed rather than recorded: against a launcher, the attestation is the boundary, not
the letter of it. The residual the other plan states is a launcher who claims **first** on Cloud
Run (its R5, F30); on Batch the attestation is what closes it (its R5 says so, D3129).

**F5 — Open, answered: why the earlier design rejected a per-run secret, and whether it holds.**
`docs/budget-token-channel/spec.md` rejected C2 for three reasons. (a) The account is shared, so
every peer reads every peer's secret: still true, and F4 makes it worse. (b) Revocation is not
fail-closed: no longer true as stated, because a secret can carry an expire time at which Google
deletes it ("the secret and all of its secret versions are irreversibly deleted without warning").
(c) Cost and a new launcher permission: still true (`google_project_iam_custom_role.launcher` holds
only `secretmanager.secrets.list` and `secretmanager.versions.list`). The rejection stands on (a).
Consequence: D3101, option declined. **Amended:** under the amendment no per-run secret is created
at all, so (a) and (c) are moot; the rejection is kept because it records why the design did not go
that way, and the plan's section 10 keeps the declined option named.

### The threat model of each candidate (`claim-attestation`)

The threat model of the carriers the old design weighed, kept because it is what D3101 was decided
on. What matters is who can obtain the thing that opens the run's identity.

| Candidate | At rest | In transit | A second launcher | The reaper | A compromised peer run |
| --- | --- | --- | --- | --- | --- |
| K0. Plain job variable | The key, in the job, for as long as the job is kept | The `CreateJob` request (audit record unverified) | Reads it with `batch.jobs.get` | Reads it (`batch.jobs.get`, `batch.jobs.list`) | Cannot read the job; holds nothing |
| K1. `encryptedVariables` under one KMS key the run account decrypts | Ciphertext in the job; nothing per run | Ciphertext only | **Replays the ciphertext into a job of their own and reads the key** (F4) | Reads ciphertext, cannot decrypt | Holds decrypt, lacks the ciphertext |
| K2. Per-run Secret Manager secret, `secretVariables` | A secret per run, deleted by Google at its expire time | A version path | **Launches a run under the same account and reads the secret** (F4) | Reads the name only | **Reads it by name** if it learns or guesses the name |
| K3. VM-attested release (declined by the amendment) | A blob in the job that only the credentials service can decrypt; nothing per run | The blob; the key travels once, over TLS, to the VM that proved itself | Reads the blob, cannot decrypt it, and cannot present the first run's VM identity | Reads the blob, cannot decrypt | Has its own VM identity only; the service refuses it for another job |
| **K4. VM-attested claim (chosen by the amendment)** | **Nothing: the ticket is a one-time handle and the record holds no secret** | **The ticket, in the clear (F3); the claim carries the VM's identity token** | **Reads the ticket, writes the claim, and is refused: their VM is not the recorded job's** | Reads the job, including the ticket; can serve nothing with it | Has its own VM identity only; the service refuses it for another job |

K3's remaining readers were the run itself (A3), the credentials service, and anyone who can change
the service or the KMS key's policy: the project owner, and an operator who applies the compute
root. **K4 has no at-rest secret to read at all**, and its remaining readers are the run's own VM
(A3 — which now includes the agent, and is the run's own identity either way) and whoever can
change the service's code or the record: the project owner, an operator who applies the compute
root, and the database principals the other plan's R3 states. F24 said what the table's "second
launcher" column does not: they could mint. They no longer can (F24 amended).

**F6 — Gap: the comparison with Cloud Run is itself unverified.** Requirement 2 holds Cloud Run up
as the standard. Whether a launcher's `run.executions.get` returns an execution's override is the
half the earlier design left open (its U2). K3 is not weaker than Cloud Run on any reader in the
table above; it may be stronger against a second launcher. No consequence for the design; recorded
so nobody reads "same as Cloud Run" as a measured claim. **Amended:** requirement 2 is now met
literally rather than by analogy — one scheme, one route, one token — and the other plan's Check
840 is where the Cloud Run half is measured.

### The backend as it stands (`job-environment`, `project-jobs`, `service-credentials`)

**F7 — Wrong: "Without it the Cloud Batch backend can launch nothing" understates it.** With a
carrier and nothing else, a Batch run still cannot complete. F8 to F13 are why. Consequence: D3100.

**F8 — Gap: a Batch job carries no runs bucket and no budget backend.** `jobDefinition` set
`FUGARO_RUN`, `FUGARO_BACKEND`, `FUGARO_BATCH_JOB`, `FUGARO_PROJECT`, `FUGARO_GCP_PROJECT` and
`FUGARO_REGION`. It did not set `FUGARO_BUCKET` (its own comment: "no Batch job writes the runs
bucket, and this backend's launcher wiring is not built yet"), `FUGARO_RTDB_URL` or
`FUGARO_FIREBASE_API_KEY`. `fugaro exec` opens the bucket from that variable
(`internal/cli/exec.go`), and the runner refuses a token without a budget backend
(`internal/runner/budget_session.go`). On Cloud Run these come from the deployed job's template
(`platformEnvFor`, `internal/infra/spec.go`), which carries more than these: the remote and
checkout variables, the price tables, the budget mode and caps, the allowed models, the model
providers and, on a project job, the credentials service's URL (`internal/infra/projectjobs.go`),
without which the runner cannot reach the issuer and the claim route at all. Consequence: BR8 built
the job's environment from the same values the template is built from, and a test holds the two
name sets together.

**F9 — Gap: the runner cannot place a Batch run.** `matchJobName` (`internal/runner/projectjob.go`)
compares the run's job name with `gcp.ProjectJobName` and `gcp.JobName`. On Batch the job name is
`JobID(slug, workflow, runID)`, which matches neither, so the run ends at `cannotPlaceJob`. The
credentials service's `ValidJobName` (`internal/credsvc/wire.go`) bounds a name at 49 bytes, where
a Batch job id may be 63. No test covered either on Batch. Consequence: a Batch job names the
deployed job it stands in for, in a new variable (R6); BR8, BR9.

**F10 — Gap: Batch refuses every workflow on a shared project job.** `Launch` returns "Cloud Batch
has no project jobs yet" when `spec.Job` is set. Shared jobs are the default
(`docs/status.md`: shared-jobs-default 29/32). Consequence: D3103, BR10.

**F11 — Wrong: the Batch run account is not the shared account the earlier design assumed.**
`JobRunAccounts` (`internal/infra/compute.go`) matches display names beginning "Fugaro job " or
"Fugaro M4 job ", which are per-repository job accounts; a shared project job's account is named
"Fugaro project job " and is not matched. `batchBlockAccount` (`internal/cli/init_compute.go`)
records an account only when exactly one exists. So today a Batch installation with two
repositories has no account recorded, and one with shared jobs has none that fits. Consequence:
D3103, BR10.

**F12 — Gap: nothing grants a Batch run its secrets.** `batch.secrets` (`localcfg.Batch`) is
written by hand; `doctorBatchSecrets` checks a label and says itself that it does not read
whether the run account can read the secret. Consequence: D3104, BR11.

**F13 — Rule: `fugaro update` never applies the compute root.** `update` refuses
`--provision-batch` ("provisioning the Batch backend's root is a first run's",
`internal/cli/update.go`) and `init_stages.go` adds the compute stage only under that flag. A new
key, role or grant in the compute root would therefore need `fugaro setup --provision-batch` run
again, which the hard rule on `fugaro update` forbids. Consequence: R2, BR6.

**F14 — Open, answered: does anyone use the Batch backend?** No evidence that anyone does. No
config in the repository selects `backend: cloud-batch`. The release notes that brought the
provisioning say "no run can use the Batch backend yet", and those of v0.25.0 say "nothing turns it
on". Checks 57b and 124 are NOT RUN.
F8 to F12 mean no run could have completed on this code. The owner was asked with removal as an
option and chose to finish it (D3100).

### The reaper, the run record and doctor (`claim-attestation`, `update-converge`)

**F15 — Open, answered: the reaper.** `reap.go` reads labels and state and never a job's
environment, but its `Jobs.Get` and `Jobs.List` responses carry the variables, and its role holds
`batch.jobs.get` and `batch.jobs.list`. Under K3 it could read the wrapped blob and nothing more.
**Amended:** under K4 it reads the ticket, and the ticket is worth nothing without the recorded
job's VM (F3). It deletes a job, and the ticket with it. Consequence: none beyond K4.

**F16 — Open, answered: the run record.** `runstore.Launch` (`launch.json`) holds no environment.
`LaunchSpec` prints `SecretEnv` redacted (`internal/backend/launchspec_fmt.go`). The wrapped blob
is not a secret to its readers, but it is kept out of the record all the same (R7). **Amended:**
there is no blob; the ticket is kept out of the record for the same reason and is redacted in
output (R7 as re-cut).

**F17 — Exists: doctor and validate already speak about the seal on Batch.**
`doctorBatchBudgetSeal` (`internal/cli/doctor_batch.go`) fails a Batch installation that mints, and
`internal/cli/validate_batch.go` has the offline half. Both flip rather than being added.
Consequence: BR7.

**F18 — Exists: nothing is left behind per run today, and neither candidate adds anything to clean
up.** The sealed object is deleted by the bucket's lifecycle rule after one day
(`modules/installation/bucket.tf`, suffix `/budget-token`); a rejected launch drops it at once
(`dropBudgetToken`). K3 creates no cloud resource per run: the blob lives in the job and goes when
the job does. The registration of D3108 is one small entry per launch attempt on the budget record
the run already has, which is kept or swept as that record is today. Requirement 5 holds with no
new cleanup, and the ruling against deleting secrets is not touched. **Amended:** with the seal
itself going (the other plan's LM18) the per-run object goes too, and the registration's entry is
two fields on the issuance record that plan already writes per run (R19). Nothing is created per
run, and nothing is left.

### Rules and the other plans

**F19 — Rule: the credentials service account's grants.** `.claude/rules/credentials-service.md`
says the account's only project-level grant is the database role. K3 gave it reads on the
project (F26). Consequence: D3102; the rule's text is amended in BR5, with the grant. **Amended:**
the reads it needs are the job's and the link's, and it needs no decrypt; the rule is amended in
BR5 as before, with those.

**F20 — Rule: a field never changes meaning.** `FUGARO_BUDGET_TOKEN` means "the seal key". The
wrapped blob gets a name of its own (R5). **Amended:** the wrapped name is gone with the blob, and
the ticket has a name of its own in the other plan's C1 (`identitywire.TicketEnv`), which is why
this plan adds none.

**F21 — Gap: AB9 is approved and not merged, and its text will be wrong either way.** AB9 makes
Batch refuse every launch once every launch is sealed. If it merges first, BR1 replaces its
refusal. If this plan runs first, AB9 has nothing left to do in `run.go` beyond what BR1 leaves.
Consequence: R8, O2. **Amended:** AB9's refusal stays *true* under the amendment — a Batch launch
is never sealed, so a sealed entry on one is still refused — and what changes is only the sentence
that says why.

**F22 — Wrong: two stale lines.** `docs/budget-token-channel/spec.md` and
`internal/backend/launchenv.go` both say Batch's launcher wiring is not built. `openCloudFor` and
`batchOptions` (`internal/cli/cloud.go`) wire it. Consequence: BR1 (the document, with the
sentences F28 names), BR8 (the comment, with the code beside it).

**F23 — Gap: the credentials service is on the path of every Batch run.** Under K3 a Batch run
cannot open its session if the service is down or cold, where a Cloud Run run that needs no
credential from it could. D3104 puts it on the path regardless. Consequence: the fetch retries
within a bound and then stops the run (fail closed); BR4. **Amended:** under K4 the service is on
the path *earlier* — the launcher asks it for the ticket before anything is created, and the run
claims from it before it can open a session at all — so the bound and the fail-closed rule are
BR1's and BR4's, and the launch's refusal is the CLI's (the other plan's LM6, R1).

**F25 — Gap, found in review round 1: a token outlives no queue.** A custom token is valid for one
hour (`token.Lifetime`, `internal/budget/token/token.go`). A Batch job may sit queued up to the
reaper's `QueueDeadline`, which is far longer. A run that starts after an hour holds a dead token.
Consequence: D3106, BR14. **Amended:** the token is not minted before the run starts any more, so
what a queued job holds is a **ticket**, whose life the service bounds (the other plan's C1). A job
that queues past its ticket's life claims nothing; D3106's rule is re-cut onto the ticket, and
BR14 is the same task with that object.

**F26 — Gap, found in review round 1: `jobs.get` cannot be the link.** The Batch API's job carries
no instance name or id (`InstanceStatus` in the vendored `batch/v1` holds a boot disk, a machine
type, a provisioning model and a task pack; read in the `google.golang.org/api` module). So `batch.jobs.get` gives the job's existence, state
and creation time, and the link from a VM to its job needs a second read:
`compute.instances.get`, or the tasks API. Consequence: D3102 is amended; O1; BR5 waits on Check
830.

**F27 — Rule, found in review round 1: the compute stage asks.** Under `fugaro setup
--provision-batch` the stage refuses in an agent's session and asks for a typed confirmation
(`agentMarker`, `askTyped`, `internal/cli/init_compute.go`), and an apply that changes something
starts a rebuild of the preload image, which costs money. `fugaro update` asks nothing.
Consequence: R12.

**F28 — Gap, found in review round 1: pinned sentences.** `docs/compute-backend/spec.md` and
`docs/budget-token-channel/spec.md` both carry the sentence that an installation that mints
cannot launch on Batch, and `internal/cli/docs_budget_token_channel_test.go` holds them to the
refusal's text in `run.go`. Consequence: BRS and BR1 change the three together. **Amended:** the
three still change together, and the sentence they carry is a different one: a Batch launch carries
a ticket, so the refusal that remains is a sealed entry on a Batch job (F21) and the fix a launch
prints is the claim's (opt in, run `fugaro update`).

**F29 — Rule, found in review round 2: D2075.** The owner ruled on 2026-11-10
(`docs/secrets-brokering/reconciliation.md`) that on Batch a workflow's secrets stay
`secretVariables` as "a permanent listed exception", and declined "a Batch VM caller class in the
service" for its cost. `SECURITY.md` and row E5 of `docs/secrets-brokering/secret-readers.md` say
so. D3104 and D3101 reverse both halves. Consequence: put to the owner; D3107; BR15. **Amended:**
the same reversal holds and is now cheaper to hold: the caller class the owner declined for its
cost is one the service is given anyway for the claim, so BR15's row leaves for the same reason and
BR11's mounts leave as before.

**F30 — Wrong, found in review round 2: a release record does not close a re-created job.** While
the first job is still queued, a second launcher can read its blob, delete the job and create one
under the same name with a task of their own (`roles/batch.jobsEditor` on the prefix carries
delete and create). That job is the first to ask, so a record written at first release names the
wrong job. Consequence: D3108; the record R1 described in round 1 is replaced by a registration.
**Amended, and it is why the registration survives the re-plan:** the same attack exists against
the claim — the second launcher re-creates the job under the same name, with the same ticket, and
their VM is a VM of a job *of that name*. The record's **id** is what refuses it, exactly as under
K3 (D3108 as amended).

**F31 — Gap, found in review round 2: the reaper's clock.** The reaper runs on a schedule
(`deploy/terraform/gcp/modules/compute/reaper.tf`) and can judge a job only by its creation time,
which is after the mint. BR14 therefore cannot promise that no sealed run starts on a dead token;
it can bound the wait. A run that still starts late stops at its session, as it does today.
Consequence: BR14's acceptance says so. **Amended:** the deadline is the ticket's life and the run
that still starts late stops at its claim, which is earlier than its session.

**F32 — Wrong, found in the review the owner asked for after round 3: D3110 as first written
does not work.** Three facts. `backend:` is a per-machine key that the shared config drops
(`internal/localcfg/shared.go`), so an installation-wide apply would hang on which operator's
machine runs the update. `backendsUseGate` (`internal/cli/backends.go`) refuses the selection
while any Batch check fails, and on an installation with shared jobs the run-account checks
(`doctorBatchRunAccounts` and the `actAs` and role checks behind it, `internal/cli/doctor_batch.go`)
pass only after the apply the selection was meant to trigger. And `MergeShared` keeps a machine's
own `batch:` block whole, so a machine that never applied would publish a block with no key fact
over one that has it. Consequence: D3110 amended; R2, R14; BR18, BR19. **Amended:** unchanged in
substance — the same three facts hold for the opt-in fact, which is the one that is left.

**F33 — Found in the fifth review (the owner's second extra one); part of it does not hold.**
The review said any launcher can write the published block, so the opt-in, the withdrawal and
the key fact could be forged. Checked against the grants: a launcher holds `objectViewer` on the
runs bucket and `objectUser` under a condition naming `runs/`, `tokens/` and `issues/`
(`google_storage_bucket_iam_member.runs_launcher`, same file); only an operator (role `admin`)
holds `objectAdmin`, and the shared file is under `fugaro/`. `docs/shared-config/spec.md` §3
says the same: "only an operator can overwrite the marker or the shared file". The comments in
`internal/localcfg/shared.go` that call the file "writable by any launcher" describe an
installation whose launcher condition was never applied; the reader keeps refusing keys as
defence in depth. So the three facts are as trustworthy as the shared file, and whoever can
write it can apply the compute root anyway. What the review found that does hold:
- The publish replaces the published copy with the machine's own view when the published one is
  refused or over its size bound (`MergeShared(nil, src)`, `internal/cli/sharedcfg.go`), and
  `MergeShared` keeps a machine's own `batch:` block whole. A stale operator's machine can
  therefore publish "in" over a withdrawal. R15 amended; O18.
- A launch reads the published copy only when the machine has no local config (`sharedOK`,
  `internal/cli/cloud.go`). A launcher with a local config does not see a withdrawal until
  their own copy is refreshed, so "launches are refused from the withdrawal on" was wrong. R16
  amended; O19.
- A deleted custom role keeps its id for seven days (the comment at the top of
  `deploy/terraform/gcp/modules/compute/iam.tf`), so removing the role and opting in again
  within a week could fail the apply. R16 amended: the role stays, its binding goes.
- `google_kms_crypto_key_iam_member` has no removal class in `internal/infra/tf/cover.go`, and
  an unclassified type's deletion is refused (D1551). BR5 gained it. **Amended:** there is no KMS
  binding to remove, so BR5 keeps only the bindings that are left, and the removal class it needed
  is asked of `google_project_iam_member` and whatever the shared accounts' grants are made of.
- Nothing said what the retiring update does with a Batch run in flight. R16 amended.
Consequence: R2, R9, R14, R15, R16 amended; R17 and D3112 added; BR5, BR6, BR19, BR20, BR21
amended; BR22 added; O18 to O20; U9.

### The amendment's own findings (`claim-attestation`)

**F34 — Exists: the merged code, and what of it the amendment keeps.** Two tasks of this plan
merged on 2026-11-20: **C1** (#3380: `internal/budget/token/wrap_contract.go`, the RSA-OAEP
envelope; `internal/credsvc/sealkey_wire.go`, the release route, the two registration paths,
`JobLink`, `KeyOpener`, `Registry`, the deploy input; `sealkey_stub.go`; the `batch:` block's key
fact and opt-in fact in `internal/localcfg/batch_sealkey.go` and one field on `Local`'s `Batch`;
the settings registry entry and the schema; the block writers' labels; the contract document) and
**BRS** (#3379: `run.go` split into `env.go`, `seal.go`, `account.go`, `secrets.go` and
`internal/backend/deployedjob.go`). Read against the amendment: the **opt-in fact** and its three
states, `OptedIn()`/`OptInWithdrawn()`, the deploy input (`FUGARO_BATCH_ON`), the job-name shape
`checkRunRef` and the block's validation entry points are what this plan keeps; **the envelope,
the release route and its two registration paths, the stub, the key fact with
`ParseSealKeyPublicKey`, the settings entry, the schema entry, the golden and the contract
document** are what it retires (BR23). BRS is untouched. Consequence: the amendment is a re-cut
plus a retirement, and A1's premise is what makes both necessary.

**F35 — Rule: the ticket is the second thing Batch prints, and it is a handle, not a secret.** The
launcher plan's `identitywire.TicketEnv` is set as a job variable on Batch, where Batch prints a
job's variables (F3) and the reaper reads them (F15). Under its own design that is acceptable
because the ticket alone claims nothing: the claim must come from the record's job account, the
ticket is claimed once, and a second claim is refused. **On Batch it is only acceptable with the
attestation**, because the account is shared by every job the launcher creates (F4): a launcher
reading a ticket could otherwise run a job of their own under the same account and claim first.
So D3115's rule is a rule of this plan and not of the launcher plan's: the two together are what
make a printed ticket harmless. Consequence: BR1, BR2, BR13, and U3's re-read.

**F36 — Gap: the other plan's C1 has no field for the attestation, and it is merged.** A claim
carries the ticket and the nonce and nothing else (`ClaimRequest`, that plan's C1); its bodies are
read with `DecodeStrict`, so a field the service does not know is refused by name rather than
ignored. The delta therefore adds two fields and a route to a **merged** contract —
`TicketRequest.Job`, `ClaimRequest.Attestation`, `POST /v1/identity/job` — one interface field to
the service's identity configuration, and the merged route count that the added route breaks
(`internal/identitywire/wire_test.go`, which pins the table at five routes). The field rule permits
the fields (adding a field is fine); what does not follow from it is *who writes them*, because
that plan's C1 files are on `main` and its tasks are unlaunched. Consequence: this plan's C1 task
writes them, the collision is stated in both directions (its F18, its O11), and it is put to the
owner (D3113).

**F37 — Rule: the skew a new field makes is closed on Batch.** A service of the release behind
refuses a body it does not know, so a runner that sends the attestation to one gets a refusal
rather than a signature. That refusal is fail-closed and the run halts naming `fugaro update`,
which is the same fix a Batch installation gets for every other missing piece. It is unreachable in
practice: a Batch launch needs the compute root applied by an update of this release, and the
image runs the CLI of the same release. Consequence: BR1's and BR2's acceptance say so, and D3113's
alternative (giving Batch a claim route of its own) would avoid the skew and buy a second claim
path, which requirement 2 asks against.

**F38 — Rule: the two plans share files, and the amendment says which.** Besides the merged C1
files above, the amended tasks touch files the other plan owns: `internal/cli/launch_identity.go`
and `internal/cli/run.go` (its LM6, this plan's BR1), `internal/runner/identity_claim.go` (its LM7,
this plan's BR4), `internal/cli/init_stages.go`, `internal/cli/init_firebase.go` and
`internal/cli/update_in_flight.go` (its LM13 and LM14, this plan's BR6 and BR21),
`internal/cli/doctor_signers.go` (its LM15, this plan's BR7), `SECURITY.md` and
`website/content/security.md` (its LM16, this plan's BR12 and BR15), and
`internal/credsvc/identity_config.go` with `internal/identitywire/wire.go` (its C1, this plan's
C1). None of the other plan's tasks has run, so the amended plan's tasks rebase on them rather
than colliding, and the launcher of the plan checks for an open pull request on each before
starting the task. Consequence: BR1, BR4, BR6, BR7, BR12, BR15, BR21, C1 name them; O21.

## Decisions

All answered by the owner on 2026-11-20 with `AskUserQuestion`. **D3110 and D3111 are not the
recommendation**; no other answer departs from it, except where the amendment marks one. D3112 was
asked after the fifth review. D3101 and D3102 were each asked twice (D3102 was amended in between,
after review round 1), D3105 and D3106 were added by review round 1, and D3107
and D3108 by review round 2. D3109 and D3110 answer the two questions review round 3 left open,
asked after the pull request was opened. **Every row the amendment touches carries "Amended by
LM19" in its answer cell, and the amendment's own questions are D3113 to D3115 — all of them put
to the owner again, and none of them answered.**

| # | Module | Question | Recommendation and why | Alternatives and their cost | Answer |
| --- | --- | --- | --- | --- | --- |
| **D3100** | all | What happens to the Cloud Batch backend (F7, F14)? | Finish it: the carrier plus every gap that stops a run. It is the only backend with a Docker daemon | Remove it: about 6 tasks, `update` retires the compute root, `docker: true` loses its home. Carrier only: no run can complete, Check 831 cannot run. Park: nothing built | **Finish it** (as recommended, 2026-11-20). Unchanged by the amendment: F8 to F13 are the same gaps, and the carrier is now the claim |
| **D3101** | `claim-attestation` | Which carrier (F2, F4, F5)? | K3, the VM-attested release: the only candidate that meets "no other launcher" | K1: about 3 tasks, a second launcher replays it. K2: a secret per run, new launcher permissions, the same weakness | **K3** (as recommended, 2026-11-20). **Asked again after review round 1** with F24 stated: the owner kept K3 (as recommended, 2026-11-20). **Amended by LM19 (2026-11-21), and put to the owner again:** the object changes from the seal key to the claim — K4, the VM-attested claim (R1 as re-cut). The intent ("no other launcher") and the attestation are kept; the key, the envelope, the release route and the key fact go. Recommended: K4, as the other plan's D3122 and D3129 leave it |
| **D3102** | `claim-attestation` | Where does the release live (F19)? | In the credentials service, with one read permission in a custom role, granted only where Batch is provisioned | A separate service: about 3 more tasks and a second service to deploy and review | **The credentials service** (as recommended, 2026-11-20). Amends the standing rule on that account's grants. **Amended after review:** the question said "one read-only permission"; F26 shows the service needs `batch.jobs.get` and a second read for the link. **Asked again after review round 2:** the owner decides the second read after Check 830 (as recommended, 2026-11-20); BR3 and BR5 wait for that answer (O1). **Amended by LM19, wording put to the owner:** it is the same service and the same two reads, at the claim route; there is no decrypt on a key, so the rule's text is re-amended with one grant fewer |
| **D3103** | `project-jobs` | Does Batch learn shared project jobs (F10, F11)? | Yes: otherwise Batch serves no repository once shared jobs finish | No: zero tasks, and no user afterwards | **Yes, in this plan** (as recommended, 2026-11-20). Unchanged |
| **D3104** | `service-credentials` | How does a Batch run get its credentials (F12)? | From the credentials service only; nothing is mounted; an unserved repository is refused on Batch with its fix | Mounts as well: about 3 more tasks and per-secret IAM that keys-through-service is removing | **The service only** (as recommended, 2026-11-20). Unchanged |
| **D3105** | `project-jobs` | D3103 gives every launcher `actAs` on every shared project job account, where Cloud Run confines them to the deployed job. Accept it? | Accept it, and say so in `SECURITY.md`: a Batch launcher creates the job itself | Reverse D3103: no widening, and no user for Batch after shared jobs finish | **Accepted, named** (as recommended, 2026-11-20). Unchanged |
| **D3106** | `job-environment` | A run queued longer than its token lives (F25)? | The reaper ends it at the token's lifetime, and it is launched again | Let it fail at start: a VM billed for a run that cannot work. Decide later | **The reaper ends it** (as recommended, 2026-11-20). **Amended by LM19, object put to the owner:** the run no longer holds a token before it starts, so the deadline is its **ticket's** life (R14 as re-cut, BR14). The rule and the shape of the fix are unchanged; recommended: the ticket's life |
| **D3107** | `service-credentials` | D3104 and D3101 reverse D2075 (F29). Which stands? | D3104 supersedes D2075: its premise was the cost of a caller class, which D3101 now pays | Keep D2075 for workflow secrets: about 2 more tasks, and a mount Cloud Run is losing | **D3104 supersedes D2075** (as recommended, 2026-11-20). Unchanged, and cheaper under the amendment (F29) |
| **D3108** | `claim-attestation` | A second launcher can replace a queued job and receive its key (F30). Close it? | The launcher that wrapped the key registers the job with the service, which releases only to that job | Accept it, stated: zero tasks, the letter of requirement 1 lost to one attack. Switch to K1 | **The launcher registers the job** (as recommended, 2026-11-20). **Amended by LM19, and put to the owner again:** the object changes from the key release to the claim — the launcher names the job in its ticket ask and records the job's id after the create, and the service serves the identity only to a VM of that job (R1's registration, re-cut; R19). The two calls, the once-only writes, the launcher class alone and the re-created job's refusal all stand |
| **D3109** | `claim-attestation` | The service's launcher class admits people on its roster only, so a service account cannot register a job (O11). What should the plan do? | Build for the class as it is: a service-account launch on Batch is refused with its reason | Admit named service accounts: about 2 more tasks and a new kind of principal. Drop the registration: reverses D3108 | **People on the roster only** (as recommended, 2026-11-20). **Superseded by LM19, settled against the other plan's D3123 and D3137 and not asked again:** the issuer admits a `serviceAccount:` roster entry as a class of its own and admits it unscoped, so a service account can launch on Batch and its VM attests the claim like a person's. The later, wider answer of the same owner on the same day wins; a plan that refused a whole class the issuer serves would be the second identity scheme requirement 2 forbids |
| **D3110** | `update-converge` | Where does `fugaro update` apply the compute root and create the key (O13)? | Wherever a `batch:` block exists: selection then works straight after an update | Only where the backend is selected: about 1 more task, because selecting is gated on a doctor check that wants the key first | **Only where Batch is selected. Not the recommendation** (2026-11-20). **Amended after review:** as answered, the trigger was the updating machine's own `backend:` key, which is never published (F32). Asked again, the owner chose a published opt-in fact (as recommended, 2026-11-20): his intent, no key ring until someone chooses Batch, is kept; the trigger is the installation's, not a machine's. **Amended by LM19, wording put to the owner:** the answer stands and the question loses "and create the key" — the root creates no key ring, and the fact's three states, the opt-out and the convergence are as before |
| **D3111** | `update-converge` | The opt-in fact is never withdrawn, so an installation that stops using Batch keeps the grants and the key (O16). Is that wanted? | Yes: once in, always applied, so a later tightening reaches every installation that ever opted in | Add a way to opt out: about 2 more tasks, and `update` gains a removal of grants | **Add a way to opt out. Not the recommendation** (2026-11-20). The owner was told the cost as two more tasks, a removal of grants in `update`, and care with runs in flight; R16 carries each. Unchanged by the amendment, which only re-cuts what the removal takes (R16, BR21) |
| **D3112** | `claim-attestation` | After an opt-out, a launcher whose local config still says "in" is not refused by the CLI, and once the grants are gone the run fails only after its VM has started (O19). How is that closed? | The service refuses first: the update tells the credentials service Batch is off and the registration is refused before a job exists; about 1 more task and one more deploy input | Accept it, stated: a billed VM minute per stale launch. Or every Batch launch reads the published block: about 1 more task, a bucket read per launch, and R3 reversed for this fact | **The service refuses first** (2026-11-20, as recommended). **Amended by LM19, wording put to the owner:** the same deploy input and the same rule, read where the claim's records are written — the ticket ask that names a job and the registration are refused before any job exists (R17 as re-cut, BR22); the answer stands |
| **D3113** | `claim-attestation` | Who declares the delta's fields (F36)? The attestation rides in a claim of the other plan's C1, which is merged; its tasks are unlaunched | This plan's C1 task adds them: two fields on that wire (`TicketRequest.Job`, `ClaimRequest.Attestation`), one route (`POST /v1/identity/job`), one interface field on the service's identity configuration, and the merged route count's test its route breaks. Adding a field is what the field rule allows, and one claim path is what requirement 2 asks | A claim route of Batch's own: about 1 more task, no skew at all, and two claim paths where the brief asks for one identity scheme. Or wait for that plan to declare them: a second C1 there, a second go, and this plan's C1 dependent on a task of another plan | **Put to the owner (2026-11-21). Recommended: this plan's C1 task declares them** — the two fields, the one route, the one interface field and the merged route count's test, `internal/identitywire/wire_test.go` — and the plan records the collision in both directions (the other plan's O11, this plan's O21) |
| **D3114** | `claim-attestation` | What does the service do with a claim whose record names a job but carries no attestation (F3, F35)? | Refuse it: a record that names a job is a Batch launch, and a Batch identity is served only to the recorded job's VM. Without this, a claim could omit the field and fall back to the account check alone, which is exactly the replay F4 describes | Accept it: the account check alone, which a second launcher passes. Or a claim of the release behind is *also* refused, which it already is by the strict decode | **Put to the owner (2026-11-21). Recommended: refuse it**, with one audit line naming the field, and the run halts with the claim's own reason |
| **D3115** | `claim-attestation` | Batch prints a job's variables, and the ticket is one of them (F3, F35). Accept it? | Accept it, stated: the ticket on its own claims nothing, because the claim must come from the recorded job's VM; the run's identity is served once, and a second job of the same name is refused by the id (D3108) | Keep the ticket off the job: no carrier is left on Batch (the old K3 again), about 6 tasks back. Or a per-run Secret Manager secret: F5's (a), declined | **Put to the owner (2026-11-21). Recommended: accept it, stated**, with Check 830 measuring that a ticket read out of a job is not enough to claim (U3) and BR13's second attack trying it |

D3116 to D3119 are reserved and unused.

## Answers amended by LM19, and how each is put

One list, so the owner sees them in one place. Each is a question of this document that his ruling
of the night before last left without an object; none is answered, and the amendment claims none of
them as settled. The plan is not launched on them until they are.

| Decision | What changed | What is asked |
| --- | --- | --- |
| D3101 | the carrier: the seal key → the claim | take K4, the VM-attested claim (recommended) |
| D3102 | the same service, at the claim; no decrypt on a key | confirm the answer as re-worded |
| D3106 | the deadline: the token's life → the ticket's life | take the ticket's life (recommended) |
| D3108 | the registration's object: the blob → the job, on the issuance record | take the re-cut (recommended) |
| D3109 | superseded by the other plan's D3123 and D3137 | notice: a roster service account may launch on Batch, and no further question |
| D3110 | the question loses "and create the key"; the answer stands | notice |
| D3112 | the switch is read where the claim's records are written | notice |
| D3113 | new: who declares the delta's fields of a merged contract | this plan's C1 task (recommended) |
| D3114 | new: a record that names a job, claimed without an attestation | refuse it (recommended) |
| D3115 | new: the ticket is a printed job variable on Batch | accept it, stated (recommended) |

## Resolutions

One defensible answer each; stated, not asked. **A resolution the amendment re-cuts says so, and
R13 is dropped with the key it was about.**

- **R1 (`claim-attestation`). The shape of K4, and of the registration.** *Re-cut by LM19.* On
  Batch the launcher asks the issuer for a ticket exactly as it does on Cloud Run, with one field
  more: the full name of the Batch job it is about to create (`JobID(slug, workflow, runID)`, which
  it computes before the create). The service records it on the run's issuance record — that plan's
  C1, its R3 — with the caller's verified identity, and the record then **names a job**. The launcher
  sets the ticket on the job as `FUGARO_IDENTITY_TICKET` and nothing else: no sealed entry, no
  secret of any kind. After the create returns, the same launcher tells the service the job's
  Google-assigned id, on a route of this plan's own (`POST /v1/identity/job`), and only the identity
  that asked for the ticket may, and only before the claim. In the run, the runner claims its
  identity as it does on Cloud Run, and adds **the attestation**: its VM's identity token in the
  full format, read from the VM's metadata server (U2). The service verifies the token and serves
  the identity only if all of these hold: the token is Google's, its project is the installation's
  and its zone in the installation's region; the record names a job and carries that job's id; the
  VM is a VM of **that** job, on the fact Check 830 recorded (U1), and the read that answers it is
  the one O1 settles; that job exists, is not finished, and was created within a short bound after
  the record was written, allowing for a stated clock tolerance; the caller's account is the
  account the record expects. From there the runner does what it does today. Three consequences are
  stated rather than fixed. A **claim whose record names a job and carries no attestation is
  refused** (D3114): otherwise the account check alone would serve the replay F4 describes. A job
  **re-created under the same name** has another id and is refused (D3108's intent, F30). A
  launcher who dies between the ticket and the id leaves a record that names a job with no id, and
  the run stops at its claim, which is the safe side. And the plain ticket is *not* a secret
  (D3115): it rides in a variable of a job, it is printed (F3), and the attestation is the whole of
  what makes that harmless.
- **R2 (`update-converge`). `fugaro update` applies the compute root where the published block
  carries the opt-in fact** (D3110 as amended). *Re-cut by LM19: the root creates no key ring.*
  The fact is one field of the `batch:` block. An operator's `fugaro backends use cloud-batch`
  writes it and publishes it; from then on any operator's `fugaro update` applies the root and
  records the installation as on, whatever that operator's own machine selects, and an installation
  nobody opted in is left alone, block or no block. The fact is not withdrawn by a machine that
  selects `cloud-run` again; only R16's command withdraws it. Until then the root keeps being
  applied, so a later tightening reaches every installation whose block says "in". An installation
  whose block says "in" and whose compute root was never provisioned is skipped with one line
  naming `fugaro setup --provision-batch`: the update does not do the first provisioning. A person
  who is not an operator can select the backend locally and cannot opt the installation in;
  `update` on their machine skips the stage with one line, and their launches are refused until an
  operator has opted in and updated. First provisioning stays `fugaro setup --provision-batch`.
- **R14 (`update-converge`). Selecting the backend does not wait for what only the update
  creates** (F32). *Re-cut by LM19: the checks are the link's and the accounts'.* Every Batch check
  whose fix is the compute root applied by `fugaro update` (the run accounts' roles and `actAs` for
  accounts the root has not yet been fed, and the service's read on the jobs for the link) is
  marked as such by doctor and is not among the refusals of `backends use`. By id
  (`internal/cli/doctor_batch.go`): `batch-run-accounts`, `batch-run-account-roles` and
  `batch-run-account-actas`, and the link's own check when its failure is the missing permission.
  Doctor still reports each, with `fugaro update` as its fix, and every launch is refused with
  that fix until the update has run. A key fact that differs from the published one is a
  different failure with its own id and still refuses the selection. **Dropped by LM19:** the
  `batch-budget-seal` id and the differing-key-fact failure go with the key.
- **R15 (`update-converge`). Publishing never drops the opt-in fact.** *Re-cut by LM19: the key
  fact's half is dropped with it.* The block's merge takes the opt-in fact from the published copy,
  whatever the publishing machine's own block says: a machine's copy of it is never the authority.
  Two writers change it and no other: opting in (R2) and opting out (R16). Every publish rewrites
  the machine's own copy of the field from the published one. The fact has three states (absent,
  in, withdrawn), so a withdrawal is a written value and not an absence. **One case is left**
  (F33, O18): when the published copy is refused or over its size bound, the publish replaces it
  with the machine's view, and that machine's values are what is published. Only an operator can
  publish.
- **R16 (`update-converge`). Opting out (D3111).** *Re-cut by LM19: nothing is removed from a key,
  because there is no key.* An operator withdraws the fact with an explicit command of its own,
  never by merely selecting `cloud-run` on one machine; its name is settled in its task. It asks
  nothing. It is refused, with the reason, while a Batch run of the installation is in flight,
  because the next update would take away what that run needs to claim and to be served.
  - *What the update removes.* After the withdrawal `fugaro update` applies the compute root
    with the grants off. Three bindings go: the credentials service's binding to its custom role
    (the job read the link needs), everything the root was fed for the shared project job
    accounts (the launchers' `actAs` on them and those accounts' Batch roles, which BR5 adds), and
    the link's read where it is granted as its own binding. The custom role itself stays, because
    a deleted role keeps its id for seven days and opting in again inside that week would fail; a
    role bound to nobody grants nothing. Everything `fugaro setup --provision-batch` made stays,
    including the per-repository run accounts and the launchers' `actAs` on them. The update says
    in one line what it removes.
  - *A run in flight.* The update is safe with runs in flight, always. If a Batch run is in
    flight when it reaches this step (one launched between the opt-out and the update), it
    removes nothing, says so in one line with the count, and the next update removes the grants.
    Both this and the command's own refusal read the runs in flight the way the update already
    does (`announceRunsInFlight`, `internal/cli/update_in_flight.go`), narrowed to the runs
    whose launch record names this backend. No Batch API call is made, so no new permission is
    needed (U9).
  - *Launches.* A launch reads the opt-in fact where it reads the block: the machine's own block,
    or the published copy when the machine has no local config. A machine with no local config is
    refused from the withdrawal on, with opting in again as the fix. A machine with a local config
    is refused once its copy has followed the published one, which its next `fugaro update` does.
    Until then the CLI does not refuse it (O19), and the service is what does (R17).
  - *Opting in again* restores the bindings at the next update.
- **R17 (`claim-attestation`). The credentials service refuses where Batch is off (D3112).**
  *Re-cut by LM19: the refusal moves to the ticket and the registration.* `fugaro update` deploys
  the service with one input that says whether Batch is on, taken from the published opt-in fact:
  on where it says "in", off where it is absent or withdrawn. Where it is off, a ticket ask that
  names a job and the registration of that job's id are both refused, and since a launch asks for
  its ticket before it creates anything, no job and no VM exist yet. The launch prints the
  service's reason and the fix: an operator opts in and runs `fugaro update`. The claim route does
  not read the input, so a run in flight keeps what it holds (R16). The service is the authority
  here and the CLI's own refusal (R16) is the faster message; a stale local copy can no longer
  start a VM. What is left is the time between an opt-out and the next update, when the service
  still says on: a run launched then works, and is the run the retiring update waits for.
- **R3 (`update-converge`). "The claim is in place" is a fact in the `batch:` block and in the
  root.** *Re-cut by LM19: there is no key to name.* A launch on Batch proceeds where the block
  says the installation is opted in and refuses where it is absent or withdrawn, and the refusal's
  fix is `fugaro update` (or an operator's opt-in). This is requirement 3. No launch reads the
  cloud to decide it. **Dropped with the key:** the backend does not wrap and has no key fact to
  read, so the check is one field and not two.
- **R4 (`live-proof`). Check 830 runs before the link is written.** The task that verifies the
  VM-to-job link (BR3) starts only after the owner has run Check 830 and recorded which Google-made
  fact carries the link. If the check finds no such fact, or finds a launcher can forge it, the
  work stops and returns to the owner; it does not fall back to K1. **Re-cut by LM19:** the same
  rule, and more of the plan hangs on it — the link is the whole of the attestation and no longer
  one guard on a key release, so BR2, BR13 and BR23 wait for it as well as BR3, BR5 and BR12.
- **R5 (`claim-attestation`). New names.** *Re-cut by LM19: the wrapped marker is gone.* The
  identity marker on a Batch job is the other plan's `identitywire.TicketEnv`
  (`FUGARO_IDENTITY_TICKET`), which this plan does not redeclare; `FUGARO_BUDGET_TOKEN` keeps its
  one meaning ("the seal key") for Cloud Run until the other plan's LM18 deletes the seal, and is
  never set on a Batch job. A Batch job that carries the plain marker or a sealed entry is
  malformed and the launch is refused.
- **R6 (`job-environment`). A Batch job names the deployed job it stands in for** in a new
  variable. The runner places the run by it and sends it as the `X-Fugaro-Run-Job` header, which
  decides nothing at the service (`verifyJob` decides by the verified account). It is
  launcher-set, like `FUGARO_RUN`. Unchanged by the amendment: it is what BR8's helper fills and
  BR9's placement reads, and it is a different name from the claim's job reference (which is the
  **Batch** job's name, not the deployed job's).
- **R7 (`claim-attestation`). The ticket is treated as a secret in output** although it is one no
  longer: it is redacted in logs and errors and never written to the run record, and the runner
  registers it with the redactor before anything can print it. *Re-cut by LM19 from the blob's
  rule:* the same treatment, for the same reason — what a job's variables do with it is Batch's
  business (F3, D3115), and what Fugaro prints is Fugaro's.
- **R8 (`update-converge`). AB9 and BR1 touch the same refusal.** *Re-cut by LM19.* Whichever
  merges second rebases on the first. BR1's text is the final one: a Batch launch carries a ticket
  and no seal, so a **sealed entry on a Batch job is refused** (AB9's own rule, which stays true),
  and what BR1 adds is the ticket and the opt-out's refusal with its fix. The three pinned
  sentences (F28) name the same refusal.
- **R9 (`update-converge`). Nothing this plan creates is rotated, destroyed or replaced by
  Fugaro.** *Re-cut by LM19 from the key's rule; the key is gone.* A run in flight holds a ticket
  and a claimed identity; `update` writes no secret and replaces nothing a run holds, so an update
  with Batch runs in flight changes nothing they hold. The one removal of grants is R16's, after
  an opt-out, and it waits while a Batch run is in flight. A KMS key ring cannot be deleted at all,
  which the release notes said; this plan creates no key ring, so the sentence leaves the notes
  with BR16's own text.
- **R10 (`service-credentials`). The `batch.secrets` key leaves the block inside `fugaro update`**,
  which rewrites the local file in place and republishes; a block that still carries it is read,
  not refused, until then. Unchanged.
- **R12 (`update-converge`). Under `fugaro update` the compute stage asks nothing** (F27). It is
  refused in an agent's session, as every cloud write of `update` is, and it does not wait. An
  apply that changes only a role or a grant starts no rebuild of the preload image; one that does
  change the image's inputs says so in one line, with the cost. `CLAUDE.md` says job images are
  rebuilt behind a confirmation of their own and the no-prompt policy says nothing asks; this plan
  adds no rebuild and no prompt, and leaves that difference where it found it (O7). The typed
  confirmation stays where it is, on `fugaro setup --provision-batch`, which this plan does not
  touch. *Re-cut by LM19: "only the key, a role or a grant" is "only a role or a grant".*
- **R13. Dropped by LM19.** *(The key fact is written by `fugaro update` alone, from the root's
  own output, on every run, and published with the block; no settings command writes it and doctor
  fails a machine whose local key fact differs from the published one.* The key fact leaves the
  block, so the rule has no object; **its intent is kept** as the settings-entry rule it was:
  nothing a settings command can write is a fact the installation's root owns. The number is not
  reused.)*
- **R11 (`live-proof`). Check 831 is the brief's item 6**, run by the owner on `fugaro-dev` after
  the release, as one script with a hermetic self-test. Its second principal is a real second
  launcher account, and it tries the four attacks the claim has to survive: reading the first job
  and **replaying its ticket into a job of the second launcher's own**, calling the claim from the
  second launcher's own VM, **deleting a queued job and re-creating it under the same name**
  (D3108), and reading the issuance record directly. *Re-cut by LM19 from the key's four attacks:
  the replay is the ticket's now, and a direct read of the key is a direct read of the record.*
- **R18 (`claim-attestation`). The attestation is the run's own, and its absence is a refusal.**
  A claim carries the attestation where the record names a job and carries none where it does not
  (a Cloud Run run sends the same body it always did). A service of the release behind refuses the
  unknown field; that is fail-closed, it names `fugaro update`, and Batch cannot launch before the
  compute root is applied by an update of this release (F37). Stated: the attestation is the VM's,
  not the job's, so a run whose agent runs code on the same VM can obtain the same attestation —
  it is that run's own identity either way (A3).
- **R19 (`claim-attestation`). The registration's two writes, on the record that already exists.**
  The job's name is written with the ticket ask, on the issuance record, and the job's id on the
  route R1 names; each is written **once** and never overwritten, the id only by the identity that
  asked for the ticket and only before the claim, and a retry or a relaunch is a new run id with a
  record of its own. The field names, their refusal and the once-only rule are C1's (the re-cut);
  the record is the other plan's, which is why this plan adds no rule to the database and no node
  under `creds/`.
- **R20 (`claim-attestation`). A roster service account attests like a person.** D3123 and D3137
  admit it, so a Batch launch by a `serviceAccount:` roster entry gets a ticket and claims it, and
  the attestation does not care what class asked: the VM is the job's. Stated: a leaked
  service-account key can launch on every repository (that plan's D3137 cost), and on Batch it can
  then claim as the run it launched — which is the same power it has on Cloud Run.
- **R21 (`claim-attestation`). What retires, and when (BR23).** The merged key-release code is
  deleted in one task after the sides stop using it: the envelope, the wire and its stub, the key
  fact with its parser, the block's key field and its validation, the settings entry with its
  schema and golden, the two `batch.seal_key.*` writer rows, and the contract document. The names
  that stay are the opt-in fact's, which BR23 keeps in the block's file (renamed from
  `batch_sealkey.go`), the deploy input's and the job-name shape's, which the re-cut C1 holds,
  moved out of the wire it deletes. Until BR23 runs, the code is in the tree and no acceptance
  depends on it; the plan states that rather than shipping a plan whose C1 and its own tree
  disagree.

## Open tensions

None open. One was found in review round 2 and closed by the owner the same day: D3104 against
his earlier D2075 (F29, D3107). **The amendment adds one that is not a tension but a hand-off:**
four tasks of this plan were built on a mechanism the other plan replaces, and D3129 has already
decided which way it goes; what is left is the owner's answer to the four amended decisions above.

## Open items

- **O1. D3102's reads, decided after Check 830** (the owner's answer of 2026-11-20, re-cut by
  LM19). The service needs `batch.jobs.get`, conditioned on the Fugaro job-name prefix, and one
  more read for the link (F26). If the check names `compute.instances.get`, it cannot be scoped to
  Fugaro's VMs, and the owner then chooses between that read and a release service of its own. The
  answer is written into the re-cut contract by `R-C1-link-permission`; BR3, BR5 and BR12 wait on
  it. **The read is at the claim now**, and it is the whole of the attestation's evidence rather
  than one guard on a key release.
- **O2. Closed, and re-cut.** The owner asked for AB9's text to be amended in this pull request
  (2026-11-20): one sentence in always-a-budget's plan and its O4 now point here. A run of AB9
  already going does not read the change; R8 covers that. **LM19's reading:** AB9's refusal stays
  true (a Batch job is never sealed) and the sentence it carries changes with BR1 (F21, F28).
- **O3.** The wire of the delta — the claim's attestation, the ticket's job field and the job route
  — is settled in the re-cut C1, reviewed at the strictest tier before either side is written. Its
  exact fields are not decided in this document.
- **O4.** D3104 means provider keys reach a Batch run only after keys-through-service lands. Until
  then Check 831 runs on a repository whose credentials the service already serves.
- **O5.** Whether `docker: true` on Batch changes the threat model. With the Docker socket the
  agent is root on its own VM and can obtain that VM's identity token; that is its own run's
  identity (A3, R18). Stated for the reviewer of BR2; no task changes it.
- **O6. Closed by the other plan.** F24: minting stays with launchers was recorded here without a
  spec. The owner later ruled that a launcher does not mint at all (its D3122, D3126, D3127), and
  its LM14 takes the signing rows away. What remains stated is that plan's own R5 residual. No
  question is left here, and no backlog item was filed (the backlog is written by
  `fugaro backlog add`, which this work did not run).
- **O7.** R12 has `fugaro update` apply the compute root with no confirmation, where `setup`
  types one. Under D3110 that happens only on an installation an operator opted in; one that
  never chose Batch gets nothing applied. That is the no-prompt policy's and the hard rule's
  answer; it is listed so the owner sees it.
- **O8.** The link must also refuse a plain Compute Engine VM that someone with
  `compute.instances.create` and `actAs` on a run account made by hand. Check 830 tries it; no
  Fugaro role holds that pair today.
- **O9.** BR2 and BR4 rest on U2. **Corrected by LM19, to R4's own list:** BR2 is not built before
  Check 830 runs — it waits for the check, as R4 as re-cut says, because the attestation it serves
  is the fact the check records. **Re-cut by LM19:** the attestation *is* U2 — if the task's
  container cannot reach the metadata server, or the full-format token carries no instance there,
  the claim cannot be attested on Batch at all and the work returns to the owner rather than
  falling back to K1.
- **O10.** The `secrets` and `service_account` fields stay in `localcfg.Batch` so an old file
  still loads (R10). Deleting the fields is a later change, not in this plan.
- **O11. Settled by D3109's supersession.** The class question ("a service account cannot register
  a job") is answered by the other plan's D3123 and D3137: a roster service account may launch on
  Batch (R20). Nothing is asked again.
- **O12.** The launcher's identity token is a bearer token for an audience wider than this
  service, valid for an hour. The registration proves the caller is on the launcher roster, not
  that the caller minted this run. A launcher can no longer mint at all (O6 closed), and the
  ticket it asks for is bound to its own record; what remains is the other plan's O5, unchanged.
- **O13. Closed by D3110**, which also narrows O7.
- **O14.** "No CLI release between the merge of BR1, BR6, BR16 or BR18 and I1" is a rule for
  whoever cuts releases. Nothing enforces it. **Re-cut by LM19:** the set is BR1, BR6, BR16, BR18
  and BR22 (the retirement's BR23 must land inside the same window, since it deletes code the
  merged C1 introduced and no release may ship a half-retired tree).
- **O15.** D3109's refusal: the service answers every unverified caller alike, so the launch's
  message cannot say which reason applied. It names the roster as the likely one and how a person
  joins it (the launcher grant, then `fugaro update`). The roster is read only by a machine with
  a local config; a service account with none already runs on Cloud Run. **Re-cut by LM19:** the
  refusal is the issuer's (the other plan's C1, its `caller_not_admitted`), and this plan adds no
  refusal of its own for it.
- **O16. Closed by D3111** (not the recommendation): an operator can opt out, and the update then
  removes the grants (R16).
- **O17. Closed by the amendment.** The key still cost its small monthly amount and the ring
  stayed for good, and nothing Fugaro may do removed either (the old item). The amendment creates
  no key and no ring, so the cost and the permanence go; what stays planned is the custom role,
  bound to nobody (R16).
- **O18.** A stale operator's machine can publish "in" over a withdrawal, or the reverse, when
  the published copy is refused or too large and is replaced by that machine's view (F33, R15).
  Only an operator can publish, and an operator can opt in or out outright; the risk is a
  mistake, not an attack. Doctor reports the published state. **Accepted as stated** by the owner
  (2026-11-20, as recommended). Unchanged.
- **O19.** After an opt-out, a launcher whose local config still says "in" is not refused by the
  CLI until their next `fugaro update` (F33, R16). Before the update that removes the grants
  such a run works and is the run that update waits for. After it, the credentials service
  refuses the ticket and the run ends at its door with its VM already started. The same holds
  after opting in again and before the update. **Closed by D3112** (as recommended): the service
  refuses first (R17).
- **O20.** Every update on an opted-out installation that removes a binding writes the "warned"
  audit record a deletion writes, which `fugaro doctor` then prints. Stated. **Re-cut by LM19:**
  the bindings are the role's, the job read's and the shared accounts', and there is no decrypt.
- **O21. The delta edits a merged contract of another plan** (F36, F38). The two fields, the one
  route, the one interface field and the merged route count's test
  (`internal/identitywire/wire_test.go`, which names the five routes as a number) are written by
  this plan's C1 task into files that are on `main`, and the collision is stated there too (that
  plan's F18, its O11 says how it names such waits). Put to the owner as D3113.
- **O22. Nothing is launched on the amended answers.** C1's and BR6's approvals of 2026-11-20 are
  on hold by D3129 (that plan's O16), the re-cut C1 needs a go of its own, and D3113 to D3115 are
  unanswered. The plan says so in its status line and the folder's README says so.

## UNVERIFIED

- **U1.** Which Google-made fact links a Batch VM to its job (the instance's name, a label Batch
  sets, or neither), and whether a launcher can forge it through `AllocationPolicy.labels` or any
  job field. Settled by Check 830, run by the owner. **Re-cut by LM19:** the fact is now the whole
  of the attestation, so the check also answers whether a launcher can make a job of their own
  carry a fact that names the recorded job.
- **U2.** Whether a task's container on a Batch VM can reach the metadata server, and whether the
  identity token in full format carries the instance's id, name, project and zone there. Google's
  Compute Engine documentation says the full format does on a VM; not fetched for Batch. Check 830.
  **Re-cut by LM19:** this is the attestation itself, not a guard on it (O9).
- **U3.** Whether Batch writes a job's variables to Cloud Logging, to instance metadata or to the
  `CreateJob` audit record, and **whether a ticket read out of a job is enough to claim** (F3,
  F35). Check 830 reads all three places for the ticket and tries a claim from a job that is not
  the recorded one. *Re-cut by LM19: the old check read the logs for a wrapped value that no
  longer exists.*
- **U8.** How soon Batch lets a deleted job's name be used again. Check 830 measures it; the
  registration of D3108 does not depend on the answer, because the record holds the job's **id**
  (F30, R19).
- **U9.** That the update's reading of runs in flight counts a Batch run that is queued and has
  no VM yet. The launch record is written at launch and names its backend
  (`internal/runstore/launch.go`); how `announceRunsInFlight` decides a run is still in flight
  was not read. BR20 and BR21 pin it with a test each.
- **U4.** Who decrypts `encryptedVariables` (F2). Not needed by K4; not pursued.
- **U5.** Secret Manager's price per version and the 60-day deletion of finished Batch jobs: read
  from search summaries only. Neither is relied on. **Re-cut by LM19:** with the per-run secret
  and the key both gone, even the price is moot.
- **U6.** That a Batch run completes at all once F8 to F12 are closed. Nothing has measured this
  backend on an installation that selects it. Check 831 is the first measurement.
- **U7.** Whether the sandbox repository of `fugaro-dev` is on a shared project job. It decides
  which half of BR10 Check 831 exercises. The owner knows.
