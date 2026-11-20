# A run on Cloud Batch gets its identity: reconciliation

**Where this document disagrees with [spec.md](spec.md), this document wins.** Checked against
`origin/main` at `d57d71ddb` on 2026-11-20. The always-a-budget documents are not on `main`: they
were read on branch `docs/always-a-budget` at `8bf13231f` (pull request #3302, open). Cloud Batch,
Secret Manager, Cloud KMS and IAM documentation was fetched on 2026-11-20; what a fetched page did
not state is under UNVERIFIED, never in a finding.

The short reading: the brief asks for a carrier, and the code shows the carrier is one of seven
things that stop a Batch run. The owner chose to finish the backend (D3060), with a carrier that
releases the seal key only to the run's own VM (D3061).

## Modules

The owner approved this map on 2026-11-20. Every finding, decision and task names one id.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `key-release` | The launcher wraps the seal key; the credentials service releases it to the run's own VM; the runner fetches it | nothing |
| `job-environment` | A Batch job carries what a deployed job's template carries; the runner places a Batch run | nothing |
| `project-jobs` | Batch runs a workflow that belongs to a shared project job | `job-environment` |
| `service-credentials` | A Batch run's model and git credentials come from the credentials service alone | nothing |
| `update-converge` | `fugaro update` applies the compute root, the key and the grants, and the refusal lifts where the carrier is in place | `key-release` (its interface) |
| `live-proof` | Checks 810 and 811, the documents, `SECURITY.md` | all |

## Assumptions

- A1. "Its token" is the budget token the launcher mints (`mintBudgetToken`,
  `internal/cli/launch_budget.go`), sealed in `tokens/<slug>/<run>/budget-token`. The thing to
  carry is the 32-byte seal key, not the token.
- A2. "Launcher" is a person or account holding the installation's launcher grants, not the
  operator or the project owner. An owner can grant themselves anything, on either backend.
- A3. "The run" includes the run's own agent. The agent shares the container with the runner and
  can read the key there on Cloud Run today (`docs/design/budget-token-channel.md` §7.1). Nothing
  here changes that.
- A4. "Nothing left behind per run" means no cloud resource a person must delete. The sealed token
  object already expires by the bucket's lifecycle rule and is not counted.
- A5. "An installation on Batch" is one whose config carries a `batch:` block, whether or not it
  selects the backend.

No finding contradicts A1 to A5. A2 is what D3061 turns on.

## What the brief names, and what the repository has

| The brief | The repository |
| --- | --- |
| "the key … travels on the execution" | `LaunchSpec.SecretEnv` (`internal/backend/backend.go`), the marker `FUGARO_BUDGET_TOKEN` whose value is the key (`sealMarker`) |
| "the Batch backend refuses any sealed launch" | `jobDefinition` in `internal/backend/batch/run.go`, on any `kv.Secret` entry |
| "the refusal D3021 adds" | Task AB9 of always-a-budget: not merged, not approved |
| "the same credentials service admission" | `verifyJob` and `verifyRun`, `internal/credsvc/auth.go` |
| "the reaper" | `internal/backend/batch/reap.go`, role `google_project_iam_custom_role.reaper` |
| "its live check" | Check 124 (four parts, one script) and Check 57b, both NOT RUN |

## Findings

### The platform (`key-release`)

**F1 — Open, answered: Cloud Batch still has no per-execution place.** The v1 methods on a job are
`create`, `get`, `list`, `delete` and `cancel`. There is no run or execute verb, and no patch in
v1; v1alpha's patch "currently only supports increasing the value of the first `taskCount` field".
One job is one run, which is how this backend already uses it (`JobID` hashes the run id). The
release notes of 2025 and 2026 change nothing about secrets, KMS or identity. Consequence: the
earlier design's premise holds; D3061.

**F2 — Open, answered: Batch offers two secret carriers, and both are opened by the job's service
account.** `Environment.secretVariables` maps a name to a Secret Manager version path, and "The VM
will access the named secrets"; the role goes "to the job's service account". 
`Environment.encryptedVariables` holds a KMS key name and a ciphertext, with no field for
additional authenticated data. Who decrypts is not documented (U2). Consequence: F4.

**F3 — Gap: Batch may print variables.** Google's own page says "Batch prints out variables
(including any secrets) in plain text" and that a secret "can be recorded in unauthorized places,
like Cloud Logging". Consequence: the plain seal key must never be a job variable of any kind, and
Check 810 reads the logs for the wrapped value (U3).

**F4 — Gap: any carrier the run account opens can be replayed by a second launcher.** A launcher
holds `roles/batch.jobsEditor` on the Fugaro job prefix and `roles/iam.serviceAccountUser` on the
run accounts (`launcher_jobs_editor`, `launcher_user`, `deploy/terraform/gcp/modules/compute/iam.tf`),
and reads the whole runs bucket (`runs_reader`, `modules/installation/iam.tf`). So a second
launcher can read the first job, create a job of their own under the same account that carries
the same secret reference or ciphertext, and have that run open it. That defeats requirement 1's
"no other launcher" for `secretVariables`, for `encryptedVariables`, and for any per-run secret
granted to the account. Consequence: only an opener that is one run's alone meets the requirement;
D3061.

**F24 — Gap, found in review round 1: a launcher can already mint the identity itself.** Every
launcher and operator holds the signer's `signJwt` (`google_service_account_iam_member.minter`,
`deploy/terraform/gcp/modules/firebase/iam.tf`, granted to launchers and operators alike), and
`docs/design/budget-token-channel.md` puts that out of the seal's scope. So a second launcher can
mint a token for the first run's identity without the first run's key. K3 stops a launcher from
obtaining that key; it does not stop one from spending as that run. Against a launcher, K3's gain
over K1 is the letter of requirement 1, and a boundary only if minting is ever taken off
launchers. Against the reaper and other readers of the job, K1 and K3 are equal. Consequence: put
to the owner again; D3061 stands.

**F5 — Open, answered: why the earlier design rejected a per-run secret, and whether it holds.**
`docs/design/budget-token-channel.md` rejected C2 for three reasons. (a) The account is shared, so
every peer reads every peer's secret: still true, and F4 makes it worse. (b) Revocation is not
fail-closed: no longer true as stated, because a secret can carry an expire time at which Google
deletes it ("the secret and all of its secret versions are irreversibly deleted without warning").
(c) Cost and a new launcher permission: still true (`google_project_iam_custom_role.launcher` holds
only `secretmanager.secrets.list` and `secretmanager.versions.list`). The rejection stands on (a).
Consequence: D3061, option declined.

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
launcher (U1). Check 810 measures it before the task that depends on it starts (R4).

**F6 — Gap: the comparison with Cloud Run is itself unverified.** Requirement 2 holds Cloud Run up
as the standard. Whether a launcher's `run.executions.get` returns an execution's override is the
half the earlier design left open (its U2). K3 is not weaker than Cloud Run on any reader in the
table above; it may be stronger against a second launcher. No consequence for the design; recorded
so nobody reads "same as Cloud Run" as a measured claim.

### The backend as it stands (`job-environment`, `project-jobs`, `service-credentials`)

**F7 — Wrong: "Without it the Cloud Batch backend can launch nothing" understates it.** With a
carrier and nothing else, a Batch run still cannot complete. F8 to F13 are why. Consequence: D3060.

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
(`docs/status.md`: shared-jobs-default 29/32). Consequence: D3063, BR10.

**F11 — Wrong: the Batch run account is not the shared account the earlier design assumed.**
`JobRunAccounts` (`internal/infra/compute.go`) matches display names beginning "Fugaro job " or
"Fugaro M4 job ", which are per-repository job accounts; a shared project job's account is named
"Fugaro project job " and is not matched. `batchBlockAccount` (`internal/cli/init_compute.go`)
records an account only when exactly one exists. So today a Batch installation with two
repositories has no account recorded, and one with shared jobs has none that fits. Consequence:
D3063, BR10.

**F12 — Gap: nothing grants a Batch run its secrets.** `batch.secrets` (`localcfg.Batch`) is
written by hand; `doctorBatchSecrets` checks a label and says itself that it does not read
whether the run account can read the secret. Consequence: D3064, BR11.

**F13 — Rule: `fugaro update` never applies the compute root.** `update` refuses
`--provision-batch` ("provisioning the Batch backend's root is a first run's",
`internal/cli/update.go`) and `init_stages.go` adds the compute stage only under that flag. A new
key, role or grant in the compute root would therefore need `fugaro setup --provision-batch` run
again, which the hard rule on `fugaro update` forbids. Consequence: R2, BR6.

**F14 — Open, answered: does anyone use the Batch backend?** No evidence that anyone does. No
config in the repository selects `backend: cloud-batch`. The release notes that brought the provisioning say "no
run can use the Batch backend yet", and those of v0.25.0 say "nothing turns it on". Checks 57b and 124 are NOT RUN.
F8 to F12 mean no run could have completed on this code. The owner was asked with removal as an
option and chose to finish it (D3060).

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

**F18 — Exists: nothing is left behind per run today, and K3 adds nothing.** The sealed object is
deleted by the bucket's lifecycle rule after one day (`modules/installation/bucket.tf`, suffix
`/budget-token`); a rejected launch drops it at once (`dropBudgetToken`). K3 creates no resource
per run: the blob lives in the job and goes when the job does. Requirement 5 holds with no new
cleanup, and the ruling against deleting secrets is not touched.

### Rules and the other plans

**F19 — Rule: the credentials service account's grants.** `.claude/rules/credentials-service.md`
says the account's only project-level grant is the database role. K3 gives it one read permission
on the project. Consequence: D3062; the rule's text is amended in BR12.

**F20 — Rule: a field never changes meaning.** `FUGARO_BUDGET_TOKEN` means "the seal key". The
wrapped blob gets a name of its own (R5).

**F21 — Gap: AB9 is not merged, and its text will be wrong either way.** AB9 makes Batch refuse
every launch once every launch is sealed. If it merges first, BR1 replaces its refusal. If this
plan runs first, AB9 has nothing left to do in `run.go` beyond what BR1 leaves. Consequence: R8,
O2.

**F22 — Wrong: two stale lines.** `docs/design/budget-token-channel.md` and
`internal/backend/launchenv.go` both say Batch's launcher wiring is not built. `openCloudFor` and
`batchOptions` (`internal/cli/cloud.go`) wire it. Consequence: BR12 (the document), BR8 (the
comment, with the code beside it).

**F23 — Gap: the credentials service is on the path of every Batch run.** Under K3 a Batch run
cannot open its session if the service is down or cold, where a Cloud Run run that needs no
credential from it could. D3064 puts it on the path regardless. Consequence: the fetch retries
within a bound and then stops the run (fail closed); BR4.

**F25 — Gap, found in review round 1: a token outlives no queue.** A custom token is valid for one
hour (`token.Lifetime`, `internal/budget/token/token.go`). A Batch job may sit queued up to the
reaper's `QueueDeadline`, which is far longer. A run that starts after an hour holds a dead token.
Consequence: D3066, BR14.

**F26 — Gap, found in review round 1: `jobs.get` cannot be the link.** The Batch API's job carries
no instance name or id (`InstanceStatus` in the vendored `batch/v1` holds a boot disk, a machine
type, a provisioning model and a task pack). So `batch.jobs.get` gives the job's existence, state
and creation time, and the link from a VM to its job needs a second read:
`compute.instances.get`, or the tasks API. Consequence: D3062 is amended; O1; BR5 waits on Check
810.

**F27 — Rule, found in review round 1: the compute stage asks.** Under `fugaro setup
--provision-batch` the stage refuses in an agent's session and asks for a typed confirmation
(`agentMarker`, `askTyped`, `internal/cli/init_compute.go`), and an apply that changes something
starts a rebuild of the preload image, which costs money. `fugaro update` asks nothing.
Consequence: R12.

**F28 — Gap, found in review round 1: pinned sentences.** `docs/design/compute-backend.md` and
`docs/design/budget-token-channel.md` both carry the sentence that an installation that mints
cannot launch on Batch, and `internal/cli/docs_budget_token_channel_test.go` holds them to the
refusal's text in `run.go`. Consequence: BRS and BR1 change the three together.

## Decisions

All answered by the owner on 2026-11-20 with `AskUserQuestion`. None departs from the
recommendation. D3061 was asked twice, D3062 is amended after review, and D3065 and D3066 were
added by review round 1.

| # | Module | Question | Recommendation and why | Alternatives and their cost | Answer |
| --- | --- | --- | --- | --- | --- |
| **D3060** | all | What happens to the Cloud Batch backend (F7, F14)? | Finish it: the carrier plus every gap that stops a run. It is the only backend with a Docker daemon | Remove it: about 6 tasks, `update` retires the compute root, `docker: true` loses its home. Carrier only: no run can complete, Check 811 cannot run. Park: nothing built | **Finish it** (as recommended, 2026-11-20) |
| **D3061** | `key-release` | Which carrier (F2, F4, F5)? | K3, the VM-attested release: the only candidate that meets "no other launcher" | K1: about 3 tasks, a second launcher replays it. K2: a secret per run, new launcher permissions, the same weakness | **K3** (as recommended, 2026-11-20). **Asked again after review round 1** with F24 stated: the owner kept K3 (as recommended, 2026-11-20) |
| **D3062** | `key-release` | Where does the release live (F19)? | In the credentials service, with one read permission in a custom role, granted only where Batch is provisioned | A separate service: about 3 more tasks and a second service to deploy and review | **The credentials service** (as recommended, 2026-11-20). Amends the standing rule on that account's grants. **Amended after review:** the question said "one read-only permission"; F26 shows the service needs `batch.jobs.get` and a second read for the link. The owner's intent, the narrowest read that does the job, is kept; the count awaits his confirmation (O1) |
| **D3063** | `project-jobs` | Does Batch learn shared project jobs (F10, F11)? | Yes: otherwise Batch serves no repository once shared jobs finish | No: zero tasks, and no user afterwards | **Yes, in this plan** (as recommended, 2026-11-20) |
| **D3064** | `service-credentials` | How does a Batch run get its credentials (F12)? | From the credentials service only; nothing is mounted; an unserved repository is refused on Batch with its fix | Mounts as well: about 3 more tasks and per-secret IAM that keys-through-service is removing | **The service only** (as recommended, 2026-11-20) |
| **D3065** | `project-jobs` | D3063 gives every launcher `actAs` on every shared project job account, where Cloud Run confines them to the deployed job. Accept it? | Accept it, and say so in `SECURITY.md`: a Batch launcher creates the job itself | Reverse D3063: no widening, and no user for Batch after shared jobs finish | **Accepted, named** (as recommended, 2026-11-20) |
| **D3066** | `job-environment` | A sealed run queued longer than its token lives (F25)? | The reaper ends it at the token's lifetime, and it is launched again | Let it fail at start: a VM billed for a run that cannot work. Decide later | **The reaper ends it** (as recommended, 2026-11-20) |

D3067 to D3079 are reserved and unused.

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
  Four guards were added in review round 1. The job's digest and the wrap time are inside the
  encrypted plaintext, never beside it. The token's project and zone must be the installation's
  own. The route admits the per-repository and the shared job classes and refuses every other
  class. And a blob is released to one job only: the service records, in the budget database it
  already writes, the unique id of the job it first released a blob to, and refuses the same blob
  for any other job, which closes a delete and re-create of the same name inside the bound. The
  launchers get no grant on the key: the public key reaches them through the block alone.
- **R2 (`update-converge`). `fugaro update` applies the compute root** for an installation that
  already carries a `batch:` block, and records the key in the block. First provisioning stays
  `fugaro setup --provision-batch`. This is the hard rule's own answer (F13).
- **R3 (`update-converge`). "The carrier is in place" is a fact in the `batch:` block.** The
  backend wraps when the block names the key and refuses when it does not, and the refusal's fix
  is `fugaro update`. This is requirement 3. No launch reads the cloud to decide it.
- **R4 (`live-proof`). Check 810 runs before the link is written.** The task that verifies the
  VM-to-job link (BR3) starts only after the owner has run Check 810 and recorded which Google-made
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
  the version, so an update with Batch runs in flight changes nothing they hold. A KMS key ring
  cannot be deleted at all, which the release notes say.
- **R10 (`service-credentials`). The `batch.secrets` key leaves the block inside `fugaro update`**,
  which rewrites the local file in place and republishes; a block that still carries it is read,
  not refused, until then.
- **R12 (`update-converge`). Under `fugaro update` the compute stage asks nothing** (F27). It is
  refused in an agent's session, as every cloud write of `update` is; it prints one line naming
  what it applies and that a changed apply rebuilds the preload image, with the cost; and it does
  not wait. The typed confirmation stays where it is, on `fugaro setup --provision-batch`, which
  this plan does not touch.
- **R13 (`update-converge`). The key fact is written by `fugaro update` alone**, from the root's
  own output, on every run; no settings command writes it, and doctor fails a machine whose local
  key fact differs from the published one. A substituted public key would hand its holder every
  seal key wrapped to it, while the runs themselves fail at release.
- **R11 (`live-proof`). Check 811 is the brief's item 6**, run by the owner on `fugaro-dev` after
  the release, as one script with a hermetic self-test. Its second principal is a real second
  launcher account, and it tries both the replay of F4 and a direct read.

## Open tensions

None. No two answers pull against each other.

## Open items

- **O1. To confirm: D3062 as amended.** The service needs `batch.jobs.get`, conditioned on the
  Fugaro job-name prefix, and one more read for the link, which Check 810 names (F26). BR5 writes
  the role only after the owner has run the check and confirmed the two permissions.
- **O2.** AB9's text (always-a-budget, not approved) should be amended to say this plan lifts its
  refusal. Not done here: that folder is another pull request's.
- **O3.** The wire of the new route and of the blob is settled in contract C1, reviewed at the
  strictest tier before either side is written. Its exact fields are not decided in this document.
- **O4.** D3064 means provider keys reach a Batch run only after keys-through-service lands. Until
  then Check 811 runs on a repository whose credentials the service already serves.
- **O6.** F24: minting stays with launchers. Taking `signJwt` off them is what would make K3 a
  boundary against a launcher. It is a spec of its own and is not written here.
- **O7.** R12 has `fugaro update` apply the compute root with no confirmation, where `setup`
  types one. That is the no-prompt policy's answer; it is listed so the owner sees it.
- **O5.** Whether `docker: true` on Batch changes the threat model. With the Docker socket the
  agent is root on its own VM and can obtain that VM's identity token; that is its own run's
  identity (A3). Stated for the reviewer of BR2; no task changes it.

## UNVERIFIED

- **U1.** Which Google-made fact links a Batch VM to its job (the instance's name, a label Batch
  sets, or neither), and whether a launcher can forge it through `AllocationPolicy.labels` or any
  job field. Settled by Check 810, run by the owner.
- **U2.** Whether a task's container on a Batch VM can reach the metadata server, and whether the
  identity token in full format carries the instance's id, name, project and zone there. Google's
  Compute Engine documentation says the full format does on a VM; not fetched for Batch. Check 810.
- **U3.** Whether Batch writes a job's variables to Cloud Logging, to instance metadata or to the
  `CreateJob` audit record (F3). Check 810 reads all three for the wrapped value.
- **U8.** How soon Batch lets a deleted job's name be used again. Check 810 measures it; R1's
  release-once guard does not depend on the answer.
- **U4.** Who decrypts `encryptedVariables` (F2). Not needed by K3; not pursued.
- **U5.** Secret Manager's price per version and the 60-day deletion of finished Batch jobs: read
  from search summaries only. Neither is relied on.
- **U6.** That a Batch run completes at all once F8 to F12 are closed. Nothing has measured this
  backend on an installation that selects it. Check 811 is the first measurement.
- **U7.** Whether the sandbox repository of `fugaro-dev` is on a shared project job. It decides
  which half of BR10 Check 811 exercises. The owner knows.
