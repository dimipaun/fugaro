# A run on Cloud Batch gets its identity: reconciliation

**Where this document disagrees with [spec.md](spec.md), this document wins.** Checked against
`origin/main` at `d57d71ddb` on 2026-11-20, and rebased onto `0a8b4e221` the same day. The
always-a-budget documents were read on branch `docs/always-a-budget` at `8bf13231f`; they have
since merged (#3302, #3305), and its tasks are approved and launching, AB9 among them. Cloud Batch,
Secret Manager, Cloud KMS and IAM documentation was fetched on 2026-11-20; what a fetched page did
not state is under UNVERIFIED, never in a finding.

The short reading: the brief asks for a carrier, and the code shows the carrier is one of seven
things that stop a Batch run. The owner chose to finish the backend (D3100), with a carrier that
releases the seal key only to the run's own VM (D3101).

## Modules

The owner approved this map on 2026-11-20. Every finding, decision and task names one id.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `key-release` | The launcher wraps the seal key; the credentials service releases it to the run's own VM; the runner fetches it | nothing |
| `job-environment` | A Batch job carries what a deployed job's template carries; the runner places a Batch run | nothing |
| `project-jobs` | Batch runs a workflow that belongs to a shared project job | `job-environment` |
| `service-credentials` | A Batch run's model and git credentials come from the credentials service alone | nothing |
| `update-converge` | `fugaro update` applies the compute root, the key and the grants, and the refusal lifts where the carrier is in place | `key-release` (its interface) |
| `live-proof` | Checks 830 and 831, the documents, `SECURITY.md` | all |

## Assumptions

- A1. "Its token" is the budget token the launcher mints (`mintBudgetToken`,
  `internal/cli/launch_budget.go`), sealed in `tokens/<slug>/<run>/budget-token`. The thing to
  carry is the 32-byte seal key, not the token.
- A2. "Launcher" is a person or account holding the installation's launcher grants, not the
  operator or the project owner. An owner can grant themselves anything, on either backend.
  **The two words are this document's, not the role names.** "Launcher" here is the role
  `operator` (the local config's `terraform.operators`, Terraform's `var.launchers`): a person who
  starts runs. "Operator" here is the role `admin` (`terraform.admins`, Terraform's
  `var.operators`): a person who applies Terraform and holds `objectAdmin` on the runs bucket
  (`google_storage_bucket_iam_member.runs`, `deploy/terraform/gcp/modules/installation/iam.tf`).
  A test named `NonOperator` in the plan means a person without the role `admin`.
- A3. "The run" includes the run's own agent. The agent shares the container with the runner and
  can read the key there on Cloud Run today (`docs/design/budget-token-channel.md` §7.1). Nothing
  here changes that.
- A4. "Nothing left behind per run" means no cloud resource a person must delete. The sealed token
  object already expires by the bucket's lifecycle rule and is not counted.
- A5. "An installation on Batch" is one whose config carries a `batch:` block, whether or not it
  selects the backend. **Contradicted by D3110**: for `fugaro update`, it is one an operator has opted in.

No finding contradicts A1 to A4. A2 is what D3101 turns on.

## What the brief names, and what the repository has

| The brief | The repository |
| --- | --- |
| "the key … travels on the execution" | `LaunchSpec.SecretEnv` (`internal/backend/backend.go`), the marker `FUGARO_BUDGET_TOKEN` whose value is the key (`sealMarker`) |
| "the Batch backend refuses any sealed launch" | `jobDefinition` in `internal/backend/batch/run.go`, on any `kv.Secret` entry |
| "the refusal D3021 adds" | Task AB9 of always-a-budget: approved, not merged |
| "the same credentials service admission" | `verifyJob` and `verifyRun`, `internal/credsvc/auth.go` |
| "the reaper" | `internal/backend/batch/reap.go`, role `google_project_iam_custom_role.reaper` |
| "its live check" | Check 124 (four parts, one script) and Check 57b, both NOT RUN |

## Findings

Findings are grouped by subject; their numbers follow the order they were found, so a group can
hold a late number. The same holds for resolutions, open items and UNVERIFIED.

### The platform (`key-release`)

**F1 — Open, answered: Cloud Batch still has no per-execution place.** The v1 methods on a job are
`create`, `get`, `list`, `delete` and `cancel`. There is no run or execute verb, and no patch in
v1; v1alpha's patch "currently only supports increasing the value of the first `taskCount` field".
One job is one run, which is how this backend already uses it (`JobID` hashes the run id). The
release notes of 2025 and 2026 change nothing about secrets, KMS or identity. Consequence: the
earlier design's premise holds; D3101.

**F2 — Open, answered: Batch offers two secret carriers, and both are opened by the job's service
account.** `Environment.secretVariables` maps a name to a Secret Manager version path, and "The VM
will access the named secrets"; the role goes "to the job's service account". 
`Environment.encryptedVariables` holds a KMS key name and a ciphertext, with no field for
additional authenticated data. Who decrypts is not documented (U2). Consequence: F4.

**F3 — Gap: Batch may print variables.** Google's own page says "Batch prints out variables
(including any secrets) in plain text" and that a secret "can be recorded in unauthorized places,
like Cloud Logging". Consequence: the plain seal key must never be a job variable of any kind, and
Check 830 reads the logs for the wrapped value (U3).

**F4 — Gap: any carrier the run account opens can be replayed by a second launcher.** A launcher
holds `roles/batch.jobsEditor` on the Fugaro job prefix and `roles/iam.serviceAccountUser` on the
run accounts (`launcher_jobs_editor`, `launcher_user`, `deploy/terraform/gcp/modules/compute/iam.tf`),
and reads the whole runs bucket (`runs_reader`, `modules/installation/iam.tf`). So a second
launcher can read the first job, create a job of their own under the same account that carries
the same secret reference or ciphertext, and have that run open it. That defeats requirement 1's
"no other launcher" for `secretVariables`, for `encryptedVariables`, and for any per-run secret
granted to the account. Consequence: only an opener that is one run's alone meets the requirement;
D3101.

**F24 — Gap, found in review round 1: a launcher can already mint the identity itself.** Every
launcher and operator holds the signer's `signJwt` (`google_service_account_iam_member.minter`,
`deploy/terraform/gcp/modules/firebase/iam.tf`, granted to launchers and operators alike), and
`docs/design/budget-token-channel.md` puts that out of the seal's scope. So a second launcher can
mint a token for the first run's identity without the first run's key. K3 stops a launcher from
obtaining that key; it does not stop one from spending as that run. Against a launcher, K3's gain
over K1 is the letter of requirement 1, and a boundary only if minting is ever taken off
launchers. Against the reaper and other readers of the job, K1 and K3 are equal. Consequence: put
to the owner again; D3101 stands.

**F5 — Open, answered: why the earlier design rejected a per-run secret, and whether it holds.**
`docs/design/budget-token-channel.md` rejected C2 for three reasons. (a) The account is shared, so
every peer reads every peer's secret: still true, and F4 makes it worse. (b) Revocation is not
fail-closed: no longer true as stated, because a secret can carry an expire time at which Google
deletes it ("the secret and all of its secret versions are irreversibly deleted without warning").
(c) Cost and a new launcher permission: still true (`google_project_iam_custom_role.launcher` holds
only `secretmanager.secrets.list` and `secretmanager.versions.list`). The rejection stands on (a).
Consequence: D3101, option declined.

### The threat model of each candidate (`key-release`)

The sealed object is readable by every launcher and by the run account for its prefix. What
matters is who can obtain the seal key.

| Candidate | At rest | In transit | A second launcher | The reaper | A compromised peer run |
| --- | --- | --- | --- | --- | --- |
| K0. Plain job variable | The key, in the job, for as long as the job is kept | The `CreateJob` request (audit record unverified) | Reads it with `batch.jobs.get` | Reads it (`batch.jobs.get`, `batch.jobs.list`) | Cannot read the job; holds nothing |
| K1. `encryptedVariables` under one KMS key the run account decrypts | Ciphertext in the job; nothing per run | Ciphertext only | **Replays the ciphertext into a job of their own and reads the key** (F4) | Reads ciphertext, cannot decrypt | Holds decrypt, lacks the ciphertext |
| K2. Per-run Secret Manager secret, `secretVariables` | A secret per run, deleted by Google at its expire time | A version path | **Launches a run under the same account and reads the secret** (F4) | Reads the name only | **Reads it by name** if it learns or guesses the name |
| K3. VM-attested release (chosen) | A blob in the job that only the credentials service can decrypt; nothing per run | The blob; the key travels once, over TLS, to the VM that proved itself | Reads the blob, cannot decrypt it, and cannot present the first run's VM identity | Reads the blob, cannot decrypt | Has its own VM identity only; the service refuses it for another job |

K3's remaining readers are the run itself (A3), the credentials service, and anyone who can change
the service or the KMS key's policy: the project owner, and an operator who applies the compute
root. F24 says what the table's "second launcher" column does not: they can mint. K3 rests on one platform fact that is not
verified: that the link from a VM to its Batch job is made by Google and cannot be forged by a
launcher (U1). Check 830 measures it before the task that depends on it starts (R4).

**F6 — Gap: the comparison with Cloud Run is itself unverified.** Requirement 2 holds Cloud Run up
as the standard. Whether a launcher's `run.executions.get` returns an execution's override is the
half the earlier design left open (its U2). K3 is not weaker than Cloud Run on any reader in the
table above; it may be stronger against a second launcher. No consequence for the design; recorded
so nobody reads "same as Cloud Run" as a measured claim.

### The backend as it stands (`job-environment`, `project-jobs`, `service-credentials`)

**F7 — Wrong: "Without it the Cloud Batch backend can launch nothing" understates it.** With a
carrier and nothing else, a Batch run still cannot complete. F8 to F13 are why. Consequence: D3100.

**F8 — Gap: a Batch job carries no runs bucket and no budget backend.** `jobDefinition` sets
`FUGARO_RUN`, `FUGARO_BACKEND`, `FUGARO_BATCH_JOB`, `FUGARO_PROJECT`, `FUGARO_GCP_PROJECT` and
`FUGARO_REGION`. It does not set `FUGARO_BUCKET` (its own comment: "no Batch job writes the runs
bucket, and this backend's launcher wiring is not built yet"), `FUGARO_RTDB_URL` or
`FUGARO_FIREBASE_API_KEY`. `fugaro exec` opens the bucket from that variable
(`internal/cli/exec.go`), and the runner refuses a token without a budget backend
(`internal/runner/budget_session.go`). On Cloud Run these come from the deployed job's template
(`platformEnvFor`, `internal/infra/spec.go`), which carries more than these: the remote and
checkout variables, the price tables, the budget mode and caps, the allowed models, the model
providers and, on a project job, the credentials service's URL (`internal/infra/projectjobs.go`),
without which the runner cannot reach the release route at all. Consequence: BR8 builds the job's
environment from the same values the template is built from, and a test holds the two name sets
together.

**F9 — Gap: the runner cannot place a Batch run.** `matchJobName` (`internal/runner/projectjob.go`)
compares the run's job name with `gcp.ProjectJobName` and `gcp.JobName`. On Batch the job name is
`JobID(slug, workflow, runID)`, which matches neither, so the run ends at `cannotPlaceJob`. The
credentials service's `ValidJobName` (`internal/credsvc/wire.go`) bounds a name at 49 bytes, where
a Batch job id may be 63. No test covers either on Batch. Consequence: a Batch job names the
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
config in the repository selects `backend: cloud-batch`. The release notes that brought the provisioning say "no
run can use the Batch backend yet", and those of v0.25.0 say "nothing turns it on". Checks 57b and 124 are NOT RUN.
F8 to F12 mean no run could have completed on this code. The owner was asked with removal as an
option and chose to finish it (D3100).

### The reaper, the run record and doctor (`key-release`, `update-converge`)

**F15 — Open, answered: the reaper.** `reap.go` reads labels and state and never a job's
environment, but its `Jobs.Get` and `Jobs.List` responses carry the variables, and its role holds
`batch.jobs.get` and `batch.jobs.list`. Under K3 it can read the wrapped blob and nothing more. It
deletes a job, and the blob with it. Consequence: none beyond K3.

**F16 — Open, answered: the run record.** `runstore.Launch` (`launch.json`) holds no environment.
`LaunchSpec` prints `SecretEnv` redacted (`internal/backend/launchspec_fmt.go`). The wrapped blob
is not a secret to its readers, but it is kept out of the record all the same (R7).

**F17 — Exists: doctor and validate already speak about the seal on Batch.**
`doctorBatchBudgetSeal` (`internal/cli/doctor_batch.go`) fails a Batch installation that mints, and
`internal/cli/validate_batch.go` has the offline half. Both flip rather than being added.
Consequence: BR7.

**F18 — Exists: nothing is left behind per run today, and K3 adds nothing to clean up.** The sealed object is
deleted by the bucket's lifecycle rule after one day (`modules/installation/bucket.tf`, suffix
`/budget-token`); a rejected launch drops it at once (`dropBudgetToken`). K3 creates no cloud
resource per run: the blob lives in the job and goes when the job does. The registration of D3108
is one small entry per launch attempt on the budget record the run already has, which is kept or
swept as that record is today. Requirement 5 holds with no new cleanup, and the ruling against deleting secrets is not
touched.

### Rules and the other plans

**F19 — Rule: the credentials service account's grants.** `.claude/rules/credentials-service.md`
says the account's only project-level grant is the database role. K3 gives it reads on the
project (F26). Consequence: D3102; the rule's text is amended in BR5, with the grant.

**F20 — Rule: a field never changes meaning.** `FUGARO_BUDGET_TOKEN` means "the seal key". The
wrapped blob gets a name of its own (R5).

**F21 — Gap: AB9 is approved and not merged, and its text will be wrong either way.** AB9 makes Batch refuse
every launch once every launch is sealed. If it merges first, BR1 replaces its refusal. If this
plan runs first, AB9 has nothing left to do in `run.go` beyond what BR1 leaves. Consequence: R8,
O2.

**F22 — Wrong: two stale lines.** `docs/design/budget-token-channel.md` and
`internal/backend/launchenv.go` both say Batch's launcher wiring is not built. `openCloudFor` and
`batchOptions` (`internal/cli/cloud.go`) wire it. Consequence: BR1 (the document, with the sentences F28 names), BR8 (the comment, with the code
beside it).

**F23 — Gap: the credentials service is on the path of every Batch run.** Under K3 a Batch run
cannot open its session if the service is down or cold, where a Cloud Run run that needs no
credential from it could. D3104 puts it on the path regardless. Consequence: the fetch retries
within a bound and then stops the run (fail closed); BR4.

**F25 — Gap, found in review round 1: a token outlives no queue.** A custom token is valid for one
hour (`token.Lifetime`, `internal/budget/token/token.go`). A Batch job may sit queued up to the
reaper's `QueueDeadline`, which is far longer. A run that starts after an hour holds a dead token.
Consequence: D3106, BR14.

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

**F28 — Gap, found in review round 1: pinned sentences.** `docs/design/compute-backend.md` and
`docs/design/budget-token-channel.md` both carry the sentence that an installation that mints
cannot launch on Batch, and `internal/cli/docs_budget_token_channel_test.go` holds them to the
refusal's text in `run.go`. Consequence: BRS and BR1 change the three together.

**F29 — Rule, found in review round 2: D2075.** The owner ruled on 2026-11-10
(`docs/secrets-brokering/reconciliation.md`) that on Batch a workflow's secrets stay
`secretVariables` as "a permanent listed exception", and declined "a Batch VM caller class in the
service" for its cost. `SECURITY.md` and row E5 of `docs/secrets-brokering/secret-readers.md` say
so. D3104 and D3101 reverse both halves. Consequence: put to the owner; D3107; BR15.

**F30 — Wrong, found in review round 2: a release record does not close a re-created job.** While
the first job is still queued, a second launcher can read its blob, delete the job and create one
under the same name with a task of their own (`roles/batch.jobsEditor` on the prefix carries
delete and create). That job is the first to ask, so a record written at first release names the
wrong job. Consequence: D3108; the record R1 described in round 1 is replaced by a registration.

**F31 — Gap, found in review round 2: the reaper's clock.** The reaper runs on a schedule
(`deploy/terraform/gcp/modules/compute/reaper.tf`) and can judge a job only by its creation time,
which is after the mint. BR14 therefore cannot promise that no sealed run starts on a dead token;
it can bound the wait. A run that still starts late stops at its session, as it does today.
Consequence: BR14's acceptance says so.

**F32 — Wrong, found in the review the owner asked for after round 3: D3110 as first written
does not work.** Three facts. `backend:` is a per-machine key that the shared config drops
(`internal/localcfg/shared.go`), so an installation-wide apply would hang on which operator's
machine runs the update. `backendsUseGate` (`internal/cli/backends.go`) refuses the selection
while any Batch check fails, and on an installation with shared jobs the run-account checks
(`doctorBatchRunAccounts` and the `actAs` and role checks behind it, `internal/cli/doctor_batch.go`)
pass only after the apply the selection was meant to trigger. And `MergeShared` keeps a machine's
own `batch:` block whole, so a machine that never applied would publish a block with no key fact
over one that has it. Consequence: D3110 amended; R2, R14; BR18, BR19.

**F33 — Found in the fifth review (the owner's second extra one); part of it does not hold.**
The review said any launcher can write the published block, so the opt-in, the withdrawal and
the key fact could be forged. Checked against the grants: a launcher holds `objectViewer` on the
runs bucket and `objectUser` under a condition naming `runs/`, `tokens/` and `issues/`
(`google_storage_bucket_iam_member.runs_launcher`, same file); only an operator (role `admin`)
holds `objectAdmin`, and the shared file is under `fugaro/`. `docs/design/shared-config.md` §3
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
  an unclassified type's deletion is refused (D1551). BR5 gains it.
- Nothing said what the retiring update does with a Batch run in flight. R16 amended.
Consequence: R2, R9, R14, R15, R16 amended; R17 and D3112 added; BR5, BR6, BR19, BR20, BR21
amended; BR22 added; O18 to O20; U9.

## Decisions

All answered by the owner on 2026-11-20 with `AskUserQuestion`. **D3110 and D3111 are not the
recommendation**; no other answer departs from it. D3112 was asked after the fifth review. D3101 and D3102 were each asked twice (D3102 was amended in between, after review round 1), D3105 and D3106 were added by review round 1, and D3107
and D3108 by review round 2. D3109 and D3110 answer the two questions review round 3 left open,
asked after the pull request was opened.

| # | Module | Question | Recommendation and why | Alternatives and their cost | Answer |
| --- | --- | --- | --- | --- | --- |
| **D3100** | all | What happens to the Cloud Batch backend (F7, F14)? | Finish it: the carrier plus every gap that stops a run. It is the only backend with a Docker daemon | Remove it: about 6 tasks, `update` retires the compute root, `docker: true` loses its home. Carrier only: no run can complete, Check 831 cannot run. Park: nothing built | **Finish it** (as recommended, 2026-11-20) |
| **D3101** | `key-release` | Which carrier (F2, F4, F5)? | K3, the VM-attested release: the only candidate that meets "no other launcher" | K1: about 3 tasks, a second launcher replays it. K2: a secret per run, new launcher permissions, the same weakness | **K3** (as recommended, 2026-11-20). **Asked again after review round 1** with F24 stated: the owner kept K3 (as recommended, 2026-11-20) |
| **D3102** | `key-release` | Where does the release live (F19)? | In the credentials service, with one read permission in a custom role, granted only where Batch is provisioned | A separate service: about 3 more tasks and a second service to deploy and review | **The credentials service** (as recommended, 2026-11-20). Amends the standing rule on that account's grants. **Amended after review:** the question said "one read-only permission"; F26 shows the service needs `batch.jobs.get` and a second read for the link. **Asked again after review round 2:** the owner decides the second read after Check 830 (as recommended, 2026-11-20); BR3 and BR5 wait for that answer (O1) |
| **D3103** | `project-jobs` | Does Batch learn shared project jobs (F10, F11)? | Yes: otherwise Batch serves no repository once shared jobs finish | No: zero tasks, and no user afterwards | **Yes, in this plan** (as recommended, 2026-11-20) |
| **D3104** | `service-credentials` | How does a Batch run get its credentials (F12)? | From the credentials service only; nothing is mounted; an unserved repository is refused on Batch with its fix | Mounts as well: about 3 more tasks and per-secret IAM that keys-through-service is removing | **The service only** (as recommended, 2026-11-20) |
| **D3105** | `project-jobs` | D3103 gives every launcher `actAs` on every shared project job account, where Cloud Run confines them to the deployed job. Accept it? | Accept it, and say so in `SECURITY.md`: a Batch launcher creates the job itself | Reverse D3103: no widening, and no user for Batch after shared jobs finish | **Accepted, named** (as recommended, 2026-11-20) |
| **D3106** | `job-environment` | A sealed run queued longer than its token lives (F25)? | The reaper ends it at the token's lifetime, and it is launched again | Let it fail at start: a VM billed for a run that cannot work. Decide later | **The reaper ends it** (as recommended, 2026-11-20) |
| **D3107** | `service-credentials` | D3104 and D3101 reverse D2075 (F29). Which stands? | D3104 supersedes D2075: its premise was the cost of a caller class, which D3101 now pays | Keep D2075 for workflow secrets: about 2 more tasks, and a mount Cloud Run is losing | **D3104 supersedes D2075** (as recommended, 2026-11-20) |
| **D3108** | `key-release` | A second launcher can replace a queued job and receive its key (F30). Close it? | The launcher that wrapped the key registers the job with the service, which releases only to that job | Accept it, stated: zero tasks, the letter of requirement 1 lost to one attack. Switch to K1 | **The launcher registers the job** (as recommended, 2026-11-20) |
| **D3109** | `key-release` | The service's launcher class admits people on its roster only, so a service account cannot register a job (O11). What should the plan do? | Build for the class as it is: a service-account launch on Batch is refused with its reason | Admit named service accounts: about 2 more tasks and a new kind of principal. Drop the registration: reverses D3108 | **People on the roster only** (as recommended, 2026-11-20) |
| **D3110** | `update-converge` | Where does `fugaro update` apply the compute root and create the key (O13)? | Wherever a `batch:` block exists: selection then works straight after an update | Only where the backend is selected: about 1 more task, because selecting is gated on a doctor check that wants the key first | **Only where Batch is selected. Not the recommendation** (2026-11-20). **Amended after review:** as answered, the trigger was the updating machine's own `backend:` key, which is never published (F32). Asked again, the owner chose a published opt-in fact (as recommended, 2026-11-20): his intent, no key ring until someone chooses Batch, is kept; the trigger is the installation's, not a machine's |
| **D3111** | `update-converge` | The opt-in fact is never withdrawn, so an installation that stops using Batch keeps the grants and the key (O16). Is that wanted? | Yes: once in, always applied, so a later tightening reaches every installation that ever opted in | Add a way to opt out: about 2 more tasks, and `update` gains a removal of grants | **Add a way to opt out. Not the recommendation** (2026-11-20). The owner was told the cost as two more tasks, a removal of grants in `update`, and care with runs in flight; R16 carries each |
| **D3112** | `key-release` | After an opt-out, a launcher whose local config still says "in" is not refused by the CLI, and once the grants are gone the run fails only after its VM has started (O19). How is that closed? | The service refuses first: the update tells the credentials service Batch is off and the registration is refused before a job exists; about 1 more task and one more deploy input | Accept it, stated: a billed VM minute per stale launch. Or every Batch launch reads the published block: about 1 more task, a bucket read per launch, and R3 reversed for this fact | **The service refuses first** (2026-11-20, as recommended) |

D3112 to D3119 are reserved and unused.

## Resolutions

One defensible answer each; stated, not asked.

- **R1 (`key-release`). The shape of K3.** The compute root creates one asymmetric decryption key
  in Cloud KMS. The launcher needs only its public key, which `fugaro update` records in the
  `batch:` block. For a sealed launch the Batch backend encrypts, to that public key, the seal key
  together with a digest of the job's full name and the time of wrapping, and puts the result in
  the job as an ordinary variable. In the run, the runner asks the VM's metadata server for an
  identity token in the full format, sends it with the blob to a new route of the credentials
  service, and receives the seal key. The service verifies the token's signature and audience,
  decrypts the blob with the KMS key, and releases the key only if all of these hold: the caller's
  account is one of its job accounts (`verifyJob`'s own table); the VM in the token is a VM of the
  job the blob names (U1); that job exists and is not finished; and the job was created within a
  short bound after the blob was wrapped. From there the runner does what it does today. The
  encryption carries no authenticity, and needs none: a forged blob holds a key that opens nothing.
  Three guards were added in review round 1. The job's digest and the wrap time are inside the
  encrypted plaintext, never beside it. The token's project must be the installation's own, and
  its zone one of the installation's region. The route admits the per-repository and the shared
  job classes and refuses every other class. The launchers get no grant on the key: the public key
  reaches them through the published block alone.
  **The registration (D3108, review round 2).** Before it creates the job, the launcher tells the
  service the blob's digest and the job's name; the service writes them, with the launcher's
  verified identity, on the run's budget record. After the create returns, the same launcher tells
  the service the job's unique id, and only the identity that registered the digest may. The
  release requires the calling VM's job to carry that id. A job re-created under the same name has
  another id and is refused. The fields are the service's to write: the database rules refuse
  them to every token holder. A launcher that dies between the two calls leaves a run that stops
  at release, which is the safe side. The bound on creation time allows for the launcher's clock
  being off by a stated tolerance.
  Review round 3 fixed how it is stored, because the guarantee depends on it. The record holds
  one entry per digest, written once and never overwritten or removed while the run's record
  lives; the job's id is written once per entry. A `--retry`, or the relaunch D3106 leads to,
  mints again under a new key, so it has a new digest and adds an entry beside the old one. An
  entry another launcher adds for a digest of their own blocks nothing and opens nothing. The
  calls are admitted for the launcher class alone (`VerifyLauncher`, `internal/credsvc/auth.go`);
  the job, review and every other class are refused. The run's own agent can read the entries
  and cannot write them.
- **R2 (`update-converge`). `fugaro update` applies the compute root where the published block
  carries the opt-in fact** (D3110 as amended). The fact is one field of the `batch:` block. An
  operator's `fugaro backends use cloud-batch` writes it and publishes it; from then on any
  operator's `fugaro update` applies the root and records the key, whatever that operator's own
  machine selects, and an installation nobody opted in is left alone, block or no block. The
  fact is not withdrawn by a machine that selects `cloud-run` again; only R16's command
  withdraws it. Until then the root keeps being applied, so a later tightening reaches every
  opted-in installation. An installation whose block says "in" and whose compute root was never
  provisioned is skipped with one line naming `fugaro setup --provision-batch`: the update does
  not do the first provisioning. A person who is not
  an operator can select the backend locally and cannot opt the installation in; `update` on
  their machine skips the stage with one line, and their launches are refused until an operator
  has opted in and updated. First provisioning stays `fugaro setup --provision-batch`.
- **R14 (`update-converge`). Selecting the backend does not wait for what only the update
  creates** (F32). Every Batch check whose fix is the compute root applied by `fugaro update`
  (the key fact when missing, and the run accounts' roles and `actAs` for accounts the root has
  not yet been fed) is marked as such by doctor and is not among the refusals of `backends use`.
  By id (`internal/cli/doctor_batch.go`): `batch-run-accounts`, `batch-run-account-roles` and
  `batch-run-account-actas`, and `batch-budget-seal` when its failure is the missing key fact.
  Doctor still reports each, with `fugaro update` as its fix, and every launch is refused with
  that fix until the update has run. A key fact that differs from the published one is a
  different failure with its own id and still refuses the selection.
- **R15 (`update-converge`). Publishing never drops the key fact or the opt-in fact.** The
  block's merge takes those two fields from the published copy, whatever the publishing
  machine's own block says: a machine's copy of them is never the authority. Three writers
  change them and no other: opting in (R2), opting out (R16), and the update's write of the key
  fact (R13). Every publish rewrites the machine's own copy of the two fields from the published
  one. The opt-in fact has three states (absent, in, withdrawn), so a withdrawal is a written
  value and not an absence. **One case is left** (F33, O18): when the published copy is refused
  or over its size bound, the publish replaces it with the machine's view, and that machine's
  values are what is published. Only an operator can publish.
- **R16 (`update-converge`). Opting out (D3111).** An operator withdraws the fact with an explicit
  command of its own, never by merely selecting `cloud-run` on one machine; its name is settled
  in its task. It asks nothing. It is refused, with the reason, while a Batch run of the
  installation is in flight, because the next update would take away what that run needs to
  open its session. **Amended after the fifth review (F33).**
  - *What the update removes.* After the withdrawal `fugaro update` applies the compute root
    with the grants off. Three bindings go: the credentials service's binding to its custom
    role, its decrypt on the key, and everything the root was fed for the shared project job
    accounts (the launchers' `actAs` on them and those accounts' Batch roles, which BR16 added).
    The custom role itself stays, because a deleted role keeps its id for seven days and opting
    in again inside that week would fail; a role bound to nobody grants nothing. The key and its
    ring stay (R9), as does everything `fugaro setup --provision-batch` made, including the
    per-repository run accounts and the launchers' `actAs` on them. The update says in one line
    what it removes.
  - *A run in flight.* The update is safe with runs in flight, always. If a Batch run is in
    flight when it reaches this step (one launched between the opt-out and the update), it
    removes nothing, says so in one line with the count, and the next update removes the grants.
    Both this and the command's own refusal read the runs in flight the way the update already
    does (`announceRunsInFlight`, `internal/cli/update_in_flight.go`), narrowed to the runs
    whose launch record names this backend. No Batch API call is made, so no new permission is
    needed (U9).
  - *Launches.* A launch reads the opt-in fact where it reads the key fact: the machine's own
    block, or the published copy when the machine has no local config. A machine with no local
    config is refused from the withdrawal on, with opting in again as the fix. A machine with a
    local config is refused once its copy has followed the published one, which its next
    `fugaro update` does. Until then the CLI does not refuse it (O19).
  - *Opting in again* restores the three bindings at the next update and uses the same key.
- **R17 (`key-release`). The credentials service refuses a registration where Batch is off
  (D3112).** `fugaro update` deploys the service with one input that says whether Batch is on,
  taken from the published opt-in fact: on where it says "in", off where it is absent or
  withdrawn. Where it is off the registration of D3108 is refused, and since a launch registers
  before it creates the job, no job and no VM exist yet. The launch prints the service's reason
  and the fix: an operator opts in and runs `fugaro update`. The release route does not read the
  input, so a run in flight keeps what it holds (R9, R16). The service is the authority here and
  the CLI's own refusal (R16) is the faster message; a stale local copy can no longer start a VM.
  What is left is the time between an opt-out and the next update, when the service still says
  on: a run launched then works, and is the run the retiring update waits for.
- **R3 (`update-converge`). "The carrier is in place" is a fact in the `batch:` block.** The
  backend wraps when the block names the key and refuses when it does not, and the refusal's fix
  is `fugaro update`. This is requirement 3. No launch reads the cloud to decide it.
- **R4 (`live-proof`). Check 830 runs before the link is written.** The task that verifies the
  VM-to-job link (BR3) starts only after the owner has run Check 830 and recorded which Google-made
  fact carries the link. If the check finds no such fact, or finds a launcher can forge it, the
  work stops and returns to the owner; it does not fall back to K1.
- **R5 (`key-release`). New names.** The blob travels as `FUGARO_BUDGET_TOKEN_WRAPPED`.
  `FUGARO_BUDGET_TOKEN` keeps its one meaning and is never set on a Batch job. A job carrying both
  is malformed and the run stops.
- **R6 (`job-environment`). A Batch job names the deployed job it stands in for** in a new
  variable. The runner places the run by it and sends it as the `X-Fugaro-Run-Job` header, which
  decides nothing at the service (`verifyJob` decides by the verified account). It is
  launcher-set, like `FUGARO_RUN`.
- **R7 (`key-release`). The blob is treated as a secret in output** although it is not one: it is
  carried in `SecretEnv`'s place, redacted in logs and errors, and never written to the run record.
- **R8 (`update-converge`). AB9 and BR1 touch the same refusal.** Whichever merges second rebases
  on the first. BR1's text is the final one: refuse where the block names no key, wrap where it
  does.
- **R9 (`update-converge`). The key is never rotated or destroyed by Fugaro.** A run in flight
  holds a blob wrapped to the key version in the block; `update` adds grants and never replaces
  the version, so an update with Batch runs in flight changes nothing they hold. The one removal
  of grants is R16's, after an opt-out, and it waits while a Batch run is in flight. A KMS key ring
  cannot be deleted at all, which the release notes say.
- **R10 (`service-credentials`). The `batch.secrets` key leaves the block inside `fugaro update`**,
  which rewrites the local file in place and republishes; a block that still carries it is read,
  not refused, until then.
- **R12 (`update-converge`). Under `fugaro update` the compute stage asks nothing** (F27). It is
  refused in an agent's session, as every cloud write of `update` is, and it does not wait. An
  apply that changes only the key, a role or a grant starts no rebuild of the preload image; one
  that does change the image's inputs says so in one line, with the cost. `CLAUDE.md` says job
  images are rebuilt behind a confirmation of their own and the no-prompt policy says nothing
  asks; this plan adds no rebuild and no prompt, and leaves that difference where it found it
  (O7). The typed confirmation stays where it is, on `fugaro setup --provision-batch`, which
  this plan does not touch.
- **R13 (`update-converge`). The key fact is written by `fugaro update` alone**, from the root's
  own output, on every run, and published with the block, which is how a launcher who is not an
  operator receives it; no settings command writes it, and doctor fails a machine whose local key
  fact differs from the published one. A substituted public key would hand its holder every
  seal key wrapped to it, while the runs themselves fail at release.
- **R11 (`live-proof`). Check 831 is the brief's item 6**, run by the owner on `fugaro-dev` after
  the release, as one script with a hermetic self-test. Its second principal is a real second
  launcher account, and it tries both the replay of F4 and a direct read.

## Open tensions

None open. One was found in review round 2 and closed by the owner the same day: D3104 against
his earlier D2075 (F29, D3107).

## Open items

- **O1. D3102's second read, decided after Check 830** (the owner's answer of 2026-11-20). The
  service needs `batch.jobs.get`, conditioned on the Fugaro job-name prefix, and one more read for
  the link (F26). If the check names `compute.instances.get`, it cannot be scoped to Fugaro's
  VMs, and the owner then chooses between that read and a release service of its own. The answer
  is written into the contract by `R-C1-link-permission`; BR3 and BR5 wait on it.
- **O2. Closed.** The owner asked for AB9's text to be amended in this pull request (2026-11-20):
  one sentence in always-a-budget's plan and its O4 now point here. A run of AB9 already going
  does not read the change; R8 covers that.
- **O3.** The wire of the new route and of the blob is settled in contract C1, reviewed at the
  strictest tier before either side is written. Its exact fields are not decided in this document.
- **O4.** D3104 means provider keys reach a Batch run only after keys-through-service lands. Until
  then Check 831 runs on a repository whose credentials the service already serves.
- **O6. Recorded, no spec now** (the owner, 2026-11-20). F24: minting stays with launchers.
  Taking `signJwt` off them is what would make K3 a boundary against a launcher. No backlog item
  was filed: the backlog is written by `fugaro backlog add`, which this work did not run.
- **O7.** R12 has `fugaro update` apply the compute root with no confirmation, where `setup`
  types one. Under D3110 that happens only on an installation an operator opted in; one that
  never chose Batch gets no key ring. That is the no-prompt policy's and the hard rule's answer; it is listed so the
  owner sees it.
- **O8.** The link must also refuse a plain Compute Engine VM that someone with
  `compute.instances.create` and `actAs` on a run account made by hand. Check 830 tries it; no
  Fugaro role holds that pair today.
- **O11. Closed by D3109.**
- **O12.** The launcher's identity token is a bearer token for an audience wider than this
  service, valid for an hour. The registration proves the caller is on the launcher roster, not
  that the caller minted this run. A launcher can mint anyway (F24); stated, not fixed.
- **O13. Closed by D3110**, which also narrows O7.
- **O15.** D3109's refusal: the service answers every unverified launcher alike, so the launch's
  message cannot say which reason applied. It names the roster as the likely one and how a person
  joins it (the launcher grant, then `fugaro update`). The roster is read only by a machine with
  a local config; a service account with none already runs on Cloud Run.
- **O16. Closed by D3111** (not the recommendation): an operator can opt out, and the update then
  removes the grants and keeps the key (R16).
- **O17.** After an opt-out the key still costs its small monthly amount and the ring stays for
  good. Nothing Fugaro may do removes either. The custom role stays too, bound to nobody (R16).
- **O18.** A stale operator's machine can publish "in" over a withdrawal, or the reverse, when
  the published copy is refused or too large and is replaced by that machine's view (F33, R15).
  Only an operator can publish, and an operator can opt in or out outright; the risk is a
  mistake, not an attack. Doctor reports the published state. Not closed.
- **O19.** After an opt-out, a launcher whose local config still says "in" is not refused by the
  CLI until their next `fugaro update` (F33, R16). Before the update that removes the grants
  such a run works and is the run that update waits for. After it, the credentials service
  refuses the release and the run ends at its door with its VM already started. The same holds
  after opting in again and before the update. **Closed by D3112** (as recommended): the
  service refuses the registration first (R17).
- **O20.** Every update on an opted-out installation that removes a binding writes the "warned"
  audit record a deletion writes, which `fugaro doctor` then prints. Stated.
- **O14.** "No CLI release between the merge of BR1, BR6, BR16 or BR18 and I1" is a rule for whoever cuts
  releases. Nothing enforces it.
- **O9.** BR2 and BR4 rest on U2 and are built before Check 830 runs. If the check fails U2, R4
  applies and their work is at risk.
- **O10.** The `secrets` and `service_account` fields stay in `localcfg.Batch` so an old file
  still loads (R10). Deleting the fields is a later change, not in this plan.
- **O5.** Whether `docker: true` on Batch changes the threat model. With the Docker socket the
  agent is root on its own VM and can obtain that VM's identity token; that is its own run's
  identity (A3). Stated for the reviewer of BR2; no task changes it.

## UNVERIFIED

- **U1.** Which Google-made fact links a Batch VM to its job (the instance's name, a label Batch
  sets, or neither), and whether a launcher can forge it through `AllocationPolicy.labels` or any
  job field. Settled by Check 830, run by the owner.
- **U2.** Whether a task's container on a Batch VM can reach the metadata server, and whether the
  identity token in full format carries the instance's id, name, project and zone there. Google's
  Compute Engine documentation says the full format does on a VM; not fetched for Batch. Check 830.
- **U3.** Whether Batch writes a job's variables to Cloud Logging, to instance metadata or to the
  `CreateJob` audit record (F3). Check 830 reads all three for the wrapped value.
- **U8.** How soon Batch lets a deleted job's name be used again. Check 830 measures it; the
  registration of D3108 does not depend on the answer.
- **U9.** That the update's reading of runs in flight counts a Batch run that is queued and has
  no VM yet. The launch record is written at launch and names its backend
  (`internal/runstore/launch.go`); how `announceRunsInFlight` decides a run is still in flight
  was not read. BR20 and BR21 pin it with a test each.
- **U4.** Who decrypts `encryptedVariables` (F2). Not needed by K3; not pursued.
- **U5.** Secret Manager's price per version and the 60-day deletion of finished Batch jobs: read
  from search summaries only. Neither is relied on.
- **U6.** That a Batch run completes at all once F8 to F12 are closed. Nothing has measured this
  backend on an installation that selects it. Check 831 is the first measurement.
- **U7.** Whether the sandbox repository of `fugaro-dev` is on a shared project job. It decides
  which half of BR10 Check 831 exercises. The owner knows.
