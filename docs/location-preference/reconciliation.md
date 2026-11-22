# Reconciliation: one location preference for an installation

Checked against `origin/main` at `252329076` on 2026-11-20. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. Decisions are
**D3080–D3091**, all answered by the owner on 2026-11-20, every one as recommended. **D3087 was
amended after review round 1 and D3088 after round 3** (below); the owner confirmed both
amended texts the same day, and answered **D3092–D3097** on the items the reviews left open (D3097 is not the recommendation). It carries out
**D3029** of [always a budget](../always-a-budget/reconciliation.md) (in that plan's open follow-up,
pull request #3305, merged on 2026-11-20), it replaces **D4** of `docs/m9-budget-and-dashboard/spec.md` for a new
installation, and it reverses the ruling **D12 of 2026-10-20** for the Firestore location (D3090).

The places each Google product offers were read from Google's own pages on 2026-11-20 and are
listed under [What each product offers](#what-each-product-offers).

## Assumptions

- **A1.** "An installation" is one Fugaro project: one GCP project, one project config, one runs
  bucket. A workspace is several of them and has no location of its own beyond its own project's.
- **A2.** "Chosen once at setup" means `fugaro setup`'s first-run questions, which are asked at a
  terminal and fail at once without one. It is a question, not a confirmation.
- **A3.** "Every regional resource the installation creates" means what Fugaro creates in the
  customer's GCP project. It does not mean what Google, a git host or a model provider store
  elsewhere, nor a resource the customer's project already had (its `_Default` log bucket).
- **A4.** "Recorded" means written where a teammate's machine also reads it, as the region is
  today.
- **A5.** "Keeps what it has" covers the two databases and also every other resource whose
  location cannot change: the buckets, the secrets' replication and the log bucket.

No finding contradicts A1 or A2. A3 became D3082, A5 became F9 to F11, and A4 is contradicted by
F17: the shared config cannot carry a new field, so the record is each machine's own (D3087 as
amended).

## What the spec names, and what the repository has

| The spec's concept | What the repository has |
| --- | --- |
| The location preference | Nothing. The nearest thing is `region` in the project config (`localcfg.Config.Region`), which setup asks as a first-run question (`wizard_stage.go`, the `--region` question; `defaultRegion` in `init_inputs.go`) |
| The mapping the product owns | One instance of it: `gcp.SchedulerRegion` and `nearestSchedulerRegion` (`internal/backend/gcp/names.go`), which place Cloud Scheduler's jobs where the region has no Scheduler |
| "Setup says so before anything is created" | The wizard asks its questions before the project config is loaded and before anything is applied (`gatherInputs`, `init_inputs.go`) |
| The confirmation of a permanent location | `confirmLocation` (`internal/cli/init_firebase.go`), reached from `deployDatabase` |
| "Accepted as it is, not refused" | The opposite, twice: `FirestoreStep.checkExisting` (`internal/infra/firestore.go`) and `discovery.firebaseDB` (`internal/infra/discover_firebase.go`) refuse a database anywhere else |
| The preference recorded | The shared config carries the region to a teammate (`keep(&out.Region, …)` in `internal/localcfg/shared.go`) |
| A workspace whose projects differ | `database_region` on a workspace member (`localcfg`'s `DatabaseRegion`, `projectpolicy.ValidDatabaseRegion`, `credsvc`'s `workspaceDatabase`) already lets a member's database live in another region |

## The census

Every region or location literal under `deploy/terraform/gcp` (tests aside) and `internal/infra`
(tests aside), and every reader of the installation's region.

**Fixed in code today (four):**

| What | Where | Value | Can it change later? |
| --- | --- | --- | --- |
| The run history's Firestore database | `infra.FirestoreLocation` (`internal/infra/firestore.go`) | `us-east5` | No |
| The budget's Realtime Database | `google_firebase_database_instance.this` (`modules/firebase/firebase.tf`) and `firebaseDBRegion` (`internal/infra/discover_firebase.go`) | `us-central1` | No |
| Fugaro's log bucket and its view | `google_logging_project_bucket_config.fugaro` and `google_logging_log_view_iam_member.runs` (`modules/installation/logging.tf`); `locations/global` in `internal/infra/discover.go`, `internal/infra/imports.go` and `logBucketPath` (`internal/cli/doctor_batch.go`) | `global` | No |
| Every secret's replication | `replication { auto {} }` on six resources: `project` and `voice_provider_keys` (`modules/installation/secrets.tf`), `licence_renewal` (`licence.tf`), `pairing_key` (`orchestrator.tf`), `credentials_bundle` (`credentials.tf`), `this` (`modules/repo/secrets.tf`); and two creators in Go, `internal/backend/gcp/secrets.go` (the CLI) and `internal/intake/gcp.go` (the deployed intake service, which has no project config) | automatic: wherever Google chooses | No |

**Already follows the installation's region (`var.region`, from `localcfg.Config.Region`):**
every Cloud Run job and service (the project and workflow jobs, the history, licence, reports,
catalog-check, coordinator, orchestrator and reaper jobs, the credentials, mobile, voice and intake
services); every Artifact Registry repository (`registry.tf`, `build_identity.tf`,
`modules/repo/registry.tf`); the runs bucket and the MCP auth store (`location = upper(var.region)`
in `bucket.tf` and `mcpauth_store.tf`); the Terraform state bucket (`infra.CreateStateBucket`) and
the managed state bucket (`managed.EnsureManagedStateBucket`, `internal/managed/statebucket.go`); the compute root's network
(`modules/compute/network.tf`); Cloud Build (`localcfg.Config.BuildRegion`, the region unless
`build.region` says otherwise); and Vertex model calls (`CLOUD_ML_REGION`, set from the region in
`internal/infra/spec.go` and `projectjobs.go`).

**Follows the region through a mapping:** the Cloud Scheduler jobs (`scheduler_region`), by
`gcp.SchedulerRegion`.

**Not regional, and Fugaro's to state rather than to place:** the API key's `locations/global`
(`discover_firebase.go`), Identity Platform (initialised over REST in `init_firebase.go`), the
service accounts and IAM.

**Readers of the region:** `newProjectConfig` (`internal/cli/init.go`), `localcfg.Config.Override`
(called from `internal/cli/project.go`), `gcpOptions` (`init.go`), `BuildRegion`, the
`SchedulerRegion` resolutions in `internal/infra/spec.go`, `firebase.go`, `reportsjob.go` and
`compute.go`, the shared config (`shared.go`), the update record (`localcfg/updaterecord.go`) and
the setup answers file (`setupAnswerQuestions`, `schemas/setup-answers.schema.json`).

## What each product offers

Read on 2026-11-20 from the pages named; the dates are each page's own "last updated".

| Product | Places | Fixed once created? | Page |
| --- | --- | --- | --- |
| Realtime Database | `us-central1`, `europe-west1`, `asia-southeast1` and no other | Yes | firebase.google.com/docs/database/locations (2026-11-18) |
| Firestore | Regions including `us-east5` and thirteen in the EU (`europe-west1`, `-west2`, `-west3`, `-west4`, `-west6`, `-west8`, `-west9`, `-west10`, `-west12`, `europe-southwest1`, `europe-north1`, `europe-north2`, `europe-central2`); multi-regions `nam5`, `nam7`, `eur3` | Yes | firebase.google.com/docs/firestore/locations (2026-11-18) |
| Cloud Run | 43 regions, `us-east5` and `europe-west1` among the Tier 1 ones | n/a | docs.cloud.google.com/run/docs/locations (2026-11-18) |
| Cloud Scheduler | 34 regions; no `us-east5`; nine named `europe-*`, seven of them in the EU (London and Zürich are not); no `europe-west10` | n/a | docs.cloud.google.com/scheduler/docs/locations (2026-11-10) |
| Artifact Registry | Regions, and multi-regions `us`, `europe`, `asia` | Not stated | docs.cloud.google.com/artifact-registry/docs/repositories/repo-locations (2026-11-10) |
| Secret Manager | Automatic replication ("without restriction", globally) or user-managed (a named set of locations) | Yes: "the locations in the replication policy can't be updated" | docs.cloud.google.com/secret-manager/docs/choosing-replication (2026-11-10) |
| Cloud Storage | Regions, dual-regions, multi-regions `US`, `EU`, `ASIA` | A bucket can now be relocated, under conditions Fugaro does not meet by default | docs.cloud.google.com/storage/docs/locations (2026-11-17) |
| Cloud Build | 42 regions; a build is pinned by naming the region, with no private pool | n/a | docs.cloud.google.com/build/docs/locations (2026-11-13) |
| Cloud Logging buckets | `global`, multi-regions `us` and `eu`, and regions | Yes | docs.cloud.google.com/logging/docs/region-support (2026-11-20) |
| Claude on Vertex AI | `global`, multi-regions `us` and `eu`, and regions including `us-east5` and `europe-west1`; the global endpoint "doesn't support data residency requirements" | n/a | docs.cloud.google.com/vertex-ai/generative-ai/docs/learn/locations (2026-11-20) |

Google's own definition, which D3082 follows: "Data residency describes where your data is stored
at rest" (docs.cloud.google.com/assured-workloads/docs/data-residency).

## Findings

**F1 — Wrong: neither database accepts another location today.** The spec's item 5 reads as if a
database elsewhere were a state the code meets. `FirestoreStep.checkExisting` refuses any location
but `infra.FirestoreLocation`, and `discovery.firebaseDB` refuses a default instance outside
`firebaseDBRegion`. So no installation that exists has a database anywhere but `us-east5` and
`us-central1`, and item 5 changes two refusals into adoptions. It applies to a Firebase project
that already had a database before Fugaro met it. Resolved by R3 and D3091.

**F2 — Exists: setup already asks one location question.** The wizard's `--region` question
(`wizard_stage.go`) is a first-run question with a default (`defaultRegion`, `us-east5`), an
answers-file key (`region` in `setupAnswerQuestions`) and a refusal that names the flag when
nothing can answer (`firstRunMissing`). The preference is that question in a person's words, not a
second one. Decided by D3080.

**F3 — Exists: the scheduler's mapping is the pattern.** `gcp.SchedulerRegion` is a table the
product owns, dated against Google's page, with a fixed fallback per region. Every entry of
`nearestSchedulerRegion` stays on the continent of its key. It is not complete: `europe-west10`
is a Cloud Run and Firestore region with no Scheduler and no entry, so `gcp.SchedulerRegion`
refuses it and asks for `--scheduler-region`. The new mapping follows the pattern, LP1 adds the
missing entries, and a test pins that every region of a place has a Scheduler location in it.

**F4 — Rule: the region can be overridden after setup, and then nothing agrees.** `--region` is a
flag on every cloud command (`cloudFlags`, `internal/cli/cloud.go`: "overrides the project
config's"), and `localcfg.Config.Override`, called from `internal/cli/project.go`, applies it in
memory for that invocation. No caller compares it with the installation. On a read command that
is a tested use (`TestLsInAnotherRegionNamesTheRunsRegion`, doctor's and `update --check`'s). On
`setup` and `update` it plans in the typed region. The runs bucket is regional (`upper(var.region)`), so the next
plan replaces a bucket and is refused late by the data-bearing rule (D1551,
`docs/no-prompt-policy/reconciliation.md`). The comment on `defaultRegion` says the region is
"changeable on a later run"; with the place derived from it (D3080) it is not. Decided by D3088,
whose scope review round 3 narrowed to the commands that plan.

**F5 — Rule: the location's own confirmation is a ruling the spec reverses.** `confirmLocation`
holds the Firestore location's note, and `stepConfirmation` (`internal/cli/init.go`) names "the
Firestore location" among the three steps that are "the person's alone (D12, 2026-10-20)". The
spec's item 4 removes that step. D3027 of always-a-budget moved the budget database's write ahead
of the note; with the note gone that order no longer matters. Decided by D3090.

**F6 — Rule: `docs/m9-budget-and-dashboard/spec.md` D4 fixes both locations.** It says the
Realtime Database goes in `us-central1` and Firestore in `us-east5`, with no fallback. The design
is replaced for a new installation by the mapping below; an existing one still matches it. The
design document gets a note (LP11), not a rewrite.

**F7 — Gap: `defaultRegion`'s comment forbids what the spec asks.** It says the region and the
Firestore location "happen to hold the same value and are never derived from one another". D3085
derives one from the other. The comment goes with the constant (LP3, LP4).

**F8 — Gap: changing the Realtime Database's region in Terraform replaces the instance.** The
resource carries `prevent_destroy`, and the region is a creation-time field. So an instance that exists
must never be given the mapping's answer. Discovery cannot be what guarantees it: `DiscoverFirebase`
skips every resource already in Terraform's state, which is every installation after its first
apply. So the resource ignores changes to its region, as LP5a's secrets ignore their replication,
and discovery supplies the real location only for an instance not yet in state, which it imports
there. LP2 tests a managed instance in `us-central1` under a European region: no change.

**F9 — Gap: secrets replicate "without restriction".** All six Terraform secret resources and both
Go creators use automatic replication, which Google documents as global. The comment in
`secrets.tf` says why it cannot simply change: "any other replication would replace them, which
deletes every version first". An EU promise that covers credentials (D3082) needs user-managed
replication on a new installation and automatic kept on an existing one, so a container that exists must keep what it has. Terraform is told to ignore the replication of
a container it already manages or adopts, and every container created from then on is replicated
in the region (R5). The intake service's creator runs in the cloud with no project config and is
told the region by its environment (LP5a, LP5c); its shape check (`Shape`,
`internal/intake/server.go`) does not read replication and is unchanged.

**F10 — Gap: the run log bucket is `global`.** It holds each run's output, which is the code and
the agent's transcript, and its location is permanent. The same shape as F9: a new installation's
bucket goes in the place, an existing one stays `global`, and the import addresses in
`internal/infra/imports.go`, the read in `internal/infra/discover.go` and `logBucketPath` in
`internal/cli/doctor_batch.go` stop assuming `global`.

**F11 — Gap: an installation made before this work cannot be told from its region alone.** Its
secrets are automatic and its log bucket global whatever its region. So nothing that exists is ever given the
mapping's answer: a secret by Terraform ignoring that field, a database and the log bucket by
discovery reading where they are (R5). One path plans with no discovery at all,
`fugaro update --forget` (`ForgetSpec`, `internal/infra/forget.go`). It keeps every secret
declared, which the ignored field makes safe, and it turns log isolation off and deletes the log
bucket, as it does today; LP5a and LP6 each test that it plans what it plans today. `fugaro update` writes what the cloud holds down as the record (D3087, D3089).

**F12 — Gap: a teammate's machine types the region again.** `fugaro update` on a new machine asks
`--gcp-project` and `--region` (`firstRunMissing`), and nothing compares the answer with the
installation. The runs bucket's own location is the installation's region and cannot drift. LP8
reads it before it trusts a typed region (R6).

**F13 — Exists, with one exception: the readers of a database URL accept a regional host.** The validators
in `internal/rtdb/client.go`, `internal/infra/tfvars.go`, `internal/infra/firebase.go`,
`internal/cli/credentials_serve.go`, `orchestrator_serve.go` and `voice_serve.go` accept both
`<db>.firebaseio.com` and `<db>.<region>.firebasedatabase.app`, and the URL a run uses is the
Firebase root's `rtdb_url` output, not a string built from a constant. A budget database in
`europe-west1` needs no change in those. **The exception is the vendor's key service:**
`productionDatabase` in `ee/keyservice/workspaces.go` is the legacy host alone, and the service
reads it to decide whether an installation exists (D2205). An EU installation is not served
there, so it would read as no installation. LP15.

**F14 — Gap: a workspace finds a member's database by `database_region`, which a person types.**
`fugaro workspace` writes `workspace_members.<project>.database_region` from a flag
(`internal/cli/workspace.go`), and an empty one means `us-central1` (`credsvc`'s
`workspaceDatabase`). Once a member can be in `europe-west1` by choosing EU, a member linked
without the flag is read at the wrong host. LP9 fills it from the member's own database URL. D3084 lets
members differ.

**F15 — Open: the `(default)` Firestore database and the project's default resource location.**
Firebase's page says the default database is tied to a project's default resource location where
one was set. A Firebase project that already has one may refuse a Firestore database elsewhere.
It is U1, and Check 820 measures it.

**F16 — Rule: `fugaro update` is the whole upgrade.** Nothing here may ship a step other than
`fugaro update`. The record (F11) is written by `update` itself, with no question, and a run in
flight is untouched: no resource is replaced, and the record is a config field no run reads.

**F17 — Rule: a field never changes meaning, and the shared config decodes strictly.** `region`
keeps its meaning. The record and the answers-file key are new names; nothing is removed or
retyped. But the published shared config (`Shared`, `SharedVersion`, `internal/localcfg/shared.go`)
refuses an unknown field, and its own comment says a new one "would stop a teammate whose Fugaro
is older". So the record is not published there: it stays in each machine's own config, written
by that machine's `fugaro update` from the cloud (D3087 as amended).

**F18 — Gap: the documents a customer reads say nothing about where data lives.** `SECURITY.md`
and `website/content/security.md` name `us-east5` only inside an image path. The spec's item 7 is
new text, pinned by a docs test (LP11).

**F19 — Gap: always-a-budget's AB7 and this plan share one file.** AB7 and this plan's LP3 and
LP10 all edit `internal/cli/init_firebase.go`, and AB7's
`TestTheBudgetDatabaseIsWrittenBeforeTheLocationNote` asserts an order around a note LP10 removes.
AB7 also writes `init.go`, `init_new_machine.go`,
`init_stages.go`, `setup.go`, `init_images.go`, `init_review.go`, `setup_refusals_test.go`,
`internal/infra/firebase.go` and `website/content/cli-reference.md`, most of which this plan
writes too. The plan orders AB7 after LP2, LP4 and LP8, and LP3, LP6, LP10 and LP14 after AB7,
and says what AB7's test becomes (O2, O3). The help texts in `setup.go` and `init.go` that say
`us-east5` is permanent and separately confirmed, and their copy in the CLI reference, are LP10's.

**F20 — Gap: run output is not only in the run log bucket.** With `--no-log-isolation`
(`log_isolation` in `modules/installation/logging.tf`) there is no Fugaro bucket and every run's
output is in the project's `_Default` bucket, which is `global`. And on every installation the
sink's filter leaves Cloud Build's logs, the check jobs' lines and Cloud Run's system logs in
`_Default` (the comments in `logging.tf`). Neither can be placed by Fugaro. Both are named among
what D3082 does not promise, and in LP11.

**F21 — Gap: nothing says what the region check does when it cannot read the bucket.**
`storage.buckets.get` on the runs bucket is the deployer's; a launcher may lack it. R10.

## The mapping

What a new installation gets. It is the product's table (`internal/place`, LP1); the two named
places are D3081's.

| | US | EU | A region in neither |
| --- | --- | --- | --- |
| Regions that derive the place | `us-*` | `europe-*` except `europe-west2` (London) and `europe-west6` (Zürich), which are not in the EU | all others, those two included |
| Region when only the place is chosen | `us-east5` | `europe-west1` (D3086) | n/a: a region is typed |
| Compute, registry, buckets, builds | the region | the region | the region |
| Cloud Scheduler | `gcp.SchedulerRegion`, as today | the same | the same |
| Firestore (D3085) | the region where Firestore offers it, else `us-east5` | the region where offered, else `europe-west1` | the region where offered, else a stated fallback |
| Realtime Database | `us-central1` | `europe-west1` | the nearest of the three, stated |
| Secrets (D3082) | user-managed, in the region | user-managed, in the region | user-managed, in the region |
| Run log bucket (D3082) | the region where Cloud Logging offers it, else `us` | the region where offered, else `eu` | the region where offered, else `global`, stated |

Where the table says "stated", setup prints the line before anything is created (the spec's
item 2). A region in neither place carries no promise and its Realtime Database is outside its
country: setup says that too.

## Decisions

All answered by the owner on 2026-11-20. Of D3080–D3091 none departs from the recommendation;
D3097, in the second table, does.

| # | Question | Answer | Why |
| --- | --- | --- | --- |
| **D3080** | How do the preference and the compute region relate (item 3)? | **The region stays the one stored setting; the place is derived from it.** Setup asks the place and takes its default region; `--region` stays as the precise form | as recommended: two values that are one cannot disagree |
| **D3081** | Which places are offered at first? | **US and EU are named and promised. Any other region still works, with each fallback stated before anything is created** | as recommended: no capability setup has today is removed |
| **D3082** | What does EU promise? | **All data at rest that Fugaro creates in a new installation is stored in the place**: databases, buckets, registry, secrets, run logs. The exceptions are listed in the documents | as recommended: it is Google's own meaning of residency, and credentials and run output are the data that matters |
| **D3083** | Are model calls in the promise? | **No, and the documents say so plainly.** Vertex calls go to the installation's region, as today | as recommended: nothing Fugaro enforces would hold for the other providers |
| **D3084** | A workspace whose members are in different places? | **Allowed. Each project keeps its own place**, and the documents say the workspace's service reads each member's ledger across places | as recommended: the code already carries a member's database region |
| **D3085** | Where does a new Firestore database go? | **The installation's own region where Firestore offers it, else the place's default** | as recommended: one region for everything where it can be |
| **D3086** | Which region does EU give with no `--region`? | **`europe-west1`** | as recommended: the Realtime Database's only EU location, so everything is in one region |
| **D3087** | Where is "what this installation has" kept? | **A locations record in the project config, written by setup and by update from what the cloud holds; where it and the cloud disagree the cloud wins and the record is corrected.** *Amended after review:* the record is each machine's own and is not published in the shared config, and nothing reads it as an input | as recommended, then **amended after review round 1**; the original answer was "shared with teammates like the region is". **The owner confirmed the amended text on 2026-11-20** |
| **D3088** | A `--region` that differs from the installation's? | **Refused at once, with the fix**: the refusal names the installation's region and prints the command with it. *Amended after review:* in `setup` and `update`, the commands that plan; every other command keeps `--region` as the per-invocation override it is | as recommended, then **amended after review round 3, and confirmed by the owner on 2026-11-20** (read commands keep the override): today it ends in a late Terraform refusal |
| **D3089** | An installation made before this work, with compute in the EU and a database not yet created? | **`fugaro update` creates it in the derived place**, prints the line and asks nothing | as recommended: it is what D3029 held AB7 back for |
| **D3090** | The Firestore location's own note (D12 of 2026-10-20)? | **Removed everywhere.** The line is printed and the database is created, in any session the stage itself may run in | as recommended: the brief's item 4; the stage's own fences still apply |
| **D3091** | A database found outside the installation's place? | **Adopted if empty or ours, recorded where it really is, said once, and reported by `fugaro doctor` as information** | as recommended: accepted is not the same as hidden |

**Answered after the reviews, 2026-11-20:**

| # | Question | Answer | Why |
| --- | --- | --- | --- |
| **D3092** | On an installation made before this work, what is a secret created afterwards (O8)? | **Regional, everywhere.** What exists is left alone; what is new is in the region | as recommended: one rule with nothing inferred |
| **D3093** | What does choosing EU not promise, beyond D3082's first list (O8)? | **All four are confirmed**: log isolation turned off; the build and system logs left in `_Default`; a database adopted outside the place; and the log bucket's fallback to the place's multi-region where its region has none | as recommended |
| **D3094** | In what order are always-a-budget's AB7 and this plan launched (O3)? | **AB7 once LP2, LP4 and LP8 have merged; LP3, LP6, LP10 and LP14 once AB7 has** | as recommended: no two runs live on one file |
| **D3095** | How is a release gated on the key service's redeploy (O9)? | **A line in `docs/release.md`**, the owner's release steps | as recommended |
| **D3096** | A typed region that cannot be checked against the runs bucket (O7, R10)? | **Accepted, with one line saying it was not checked** | as recommended |
| **D3097** | How is the real provider's behaviour under `ignore_changes` proven, round 3's fixes being unreviewed (O11)? | **A fourth review of the documents** | **not the recommendation** (an owner's `fugaro update --plan-only` on the dogfood installation from LP2's and LP5a's branches before each merges). The owner sets the three-round cap aside for this plan |

**What D3097 leaves open.** A review of documents cannot run the real provider, so U9 stays
unverified until Check 821, after the release. That is the cost stated with the option, and it is
the owner's choice.

**What changed in D3087, and why.** Two facts about the code bear on the answer as given.
First, the shared config decodes strictly, so a new field there stops a teammate's older CLI
(F17). That alone does not force the change: the same file already leaves fields out for older
CLIs, so a shared record could have been published that way. Second, a record that Terraform reads would, on any machine where it is absent, stale or
edited, plan a new replication for secrets that exist; every one of them carries
`prevent_destroy`, so the update would fail until someone repaired it by hand, which the rule
that `fugaro update` is the whole upgrade forbids (F9, R5). The owner's intent is kept: there is a
record, update writes it with no question, and the cloud wins. What changed is that the record is
this machine's own, and that it is written from the cloud and never read back as an input. A
teammate's machine writes its own on its next `fugaro update`. What is left of the record is
small, and the owner should weigh it: its one reader is `fugaro doctor` (LP12), and a person
whose update cannot read the cloud is told by doctor to run `fugaro update` until one can.

**What changed in D3088, and why.** The question was asked as if `--region` only ever rewrote an
installation's region. It is also a per-invocation override on every cloud command, and read
commands use it so, with tests (F4). Refusing it everywhere would break those; the answer's
intent, that a region which is not the installation's never reaches a plan, is kept by refusing it
in `setup` and `update`, with `--repo` and `--plan-only` included. The owner confirmed that read
commands keep the override (O12).

**What D3082 does not promise.** The documents list each of these by name: prompts and code sent
to a model provider (D3083); the git host; Google's global control plane (IAM, service accounts,
resource names and labels, the API key, Identity Platform's configuration); the project's own
`_Default` and `_Required` log buckets, which Google creates in `global` and which hold whatever
the customer's project routes there; everything an installation made before this work already
has, and a database an installation adopts outside its place (D3091: whoever may create a
database in the project can put one there first, and setup says so in one line); and two things
about run output that are Fugaro's own (F20): an installation set up with
`--no-log-isolation` has no run log bucket, so its run output is in `_Default`; and on every
installation the build logs, the check jobs' lines and Cloud Run's system logs stay in
`_Default`. On an installation made before this work the secrets that exist stay automatic and
every secret created afterwards is in the region, so such an installation is mixed, and the
record says so.

**What D3090 means.** A coding agent's session and a scripted `fugaro update` can now create a
database whose location is permanent, where the stage itself may run. What keeps an agent out is
unchanged: the refusal of cloud writes in an agent's session and the managed gate (D3028 of
always-a-budget). The ruling of 2026-10-20 still holds for creating the project and linking the
billing account.

## Resolutions

One defensible answer each, so stated and not asked.

- **R1.** The mapping is one pure package, `internal/place`, with no cloud call, tested against a
  dated copy of Google's lists, like `gcp.SchedulerRegions` (F3).
- **R2.** `--location us|eu` is a new setup flag and a new answers-file key. With `--region` it
  must agree, and a disagreement is refused at once. With neither and no terminal, setup fails at
  once naming both flags, as it does for `--region` today (F2). At a terminal the question's
  default is US, which is today's default region.
- **R3.** An existing database is adopted wherever it is, under the rules that already decide
  whether it is ours: empty, or carrying our mark. A Firestore database that is not native mode is
  still refused, because that is not a location (F1).
- **R4.** An instance of the Realtime Database that Terraform manages keeps its region because
  the resource ignores changes to it; one that exists outside state is imported at the location
  discovery finds; the mapping answers only when there is none (F8).
- **R5.** Nothing that exists is given the mapping's answer, and no rule infers a policy from
  what exists. A secret container keeps its replication because Terraform ignores that field on
  a container it manages or adopts; every container created from then on, by Terraform, by the
  CLI or by the intake service, is replicated in the region, on an old installation too. A
  log bucket that exists is found by discovery, which lists every location for its id, and keeps
  its location; a Realtime Database keeps its region by R4 and a Firestore database by its step's
  own read; the mapping answers only for one that does not exist. The record (D3087) is written from a reading of the
  cloud (`infra.ObserveLocations`, LP7) and is never an input, so a wrong, stale or edited record
  changes no plan (F9, F11).
- **R6.** A typed region that differs from the runs bucket's own location is refused (F12), by the
  same refusal as D3088.
- **R7.** Setup prints where each thing will go, one line per row of the mapping, before anything
  is created, and asks nothing about it (items 2 and 4).
- **R8.** No release note carries a step: `### For operators` says run `fugaro update`, and that
  it records where the installation's data already is and moves nothing (F16).
- **R10.** Where the runs bucket's location cannot be read, the typed region is accepted and one
  line says it was not checked. Refusing would stop a launcher's first update on a new machine for
  a permission the launcher is not meant to hold; Terraform's own refusal of a bucket's
  replacement stays behind it (F21). It is the owner's to overrule (O7).
- **R9.** The part AB7 waits on is **LP2** (F8, D3029): after it, a budget database created by any
  path goes where the mapping says.

## Open tensions

None. No two answers pull against each other.

## Open items

- **O1.** U1 and U2 are measured only by a live setup in the EU (Check 820, owner-run). Until it
  passes, the EU row is tested against fakes alone.
- **O2.** AB7 of always-a-budget tests that the budget database is written before the location's
  note (D3027). LP10 removes the note and is ordered after AB7; it rewrites that test to assert
  that no note is printed. If AB7 has not merged when LP10 is ready, LP10 waits.
- **O3.** *Answered by D3094.*
- **O3 (as written).** Always-a-budget's text (D3029, merged as #3305) says AB7 waits on "the part that says
  where a new budget database goes". That part is LP2. Because AB7 shares files with this plan
  (F19), this plan asks for more: AB7 launched once LP2, LP4 and LP8 have merged, and LP3, LP6,
  LP10 and LP14 launched once AB7 has. That
  is a change to another plan's order and is the owner's to accept; always-a-budget's documents
  are not edited here.
- **O4.** An installation in neither place (D3081) gets its Realtime Database on another
  continent in some cases (South America to `us-central1`). The fallback table in LP1 is the
  product's guess at "nearest" and is the owner's to correct.
- **O5.** The workspace root (`deploy/terraform/gcp/roots/workspace`) is not in CI's Terraform
  loop until the owner copies the staged workflow in (`.claude/rules/terraform.md`). LP5a and LP6
  touch modules the workspace root does not use; if that changes, the root is validated by hand.

- **O6.** *Confirmed by the owner, 2026-11-20.*
- **O6 (as written).** D3087 was amended after review round 1: the record is each machine's own, not shared,
  and never an input. The owner is asked to confirm the amended text.
- **O7.** *Answered by D3096.*
- **O7 (as written).** R10 (a region that cannot be checked is accepted with a line) is a resolution the owner
  may overrule.
- **O8.** *Answered by D3092 and D3093.*
- **O8 (as written).** D3082 was sharpened in review, and the owner is asked to confirm four things. The list
  of what is not promised grew: log isolation turned off, the logs Fugaro leaves in `_Default`
  (F20), and a database adopted outside the place. On an installation made before this work,
  secrets created afterwards are regional while the old ones stay automatic (R5); the owner's
  answer spoke of new installations only. And the log bucket falls back to the place's
  multi-region (`us`, `eu`) where its region has no log bucket; the answer said "a regional
  location".
- **O9.** *Answered by D3095.*
- **O9 (as written).** The vendor's key service must be redeployed with LP15 before a release ships LP4. It is
  the owner's step, and the one step in this plan that `fugaro update` does not carry, because it
  is not an installation's. It is a line in `docs/release.md`; nothing else gates the release on
  it.
- **O11.** *Answered by D3097: a fourth review round ran and the README reports it. It found no
  path on which an existing installation plans a replacement. Its own fixes are the last commit
  and are unreviewed. U9 stays open.*
- **O11 (as written).** Review round 3's fixes are the last commit and were not reviewed again; three rounds
  is the cap. They are: LP2's ignored region for a managed instance, LP8's scope, LP6's listing of
  every location and its added files, LP10's added files, LP7 after LP5b, LP12's silence with no
  record, and the key service's step moved to `docs/release.md`. The "plans no change" tests of
  LP2 and LP5a run under the mock provider; what the real provider does is U9, measured by Check
  821 only after the release. An owner's `fugaro update --plan-only` on the dogfood installation
  from each of those two branches, before it merges, would close that sooner.
- **O12.** *Confirmed by the owner, 2026-11-20: `setup` and `update` only.*
- **O12 (as written).** D3088 was amended after review round 3: the refusal is in `setup` and `update` only.
  The owner is asked to confirm, and to say whether `--region` should stay an override on the
  read commands.
- **O13.** Always-a-budget's row in `docs/status.md` still says this spec is "not written yet".
  It is that plan's row, and whoever next moves it corrects it.
- **O10.** Pull requests #3315 and #3323 both use D3060–D3079 and checks 810–819. Neither
  touches this plan's block; it is noted for whoever lands them.

## UNVERIFIED

- **U1.** Whether a Firebase project with a default resource location already set refuses a
  `(default)` Firestore database in another location. Settled by Check 820, or by Firebase's API
  on a scratch project; the owner can.
- **U2.** Whether `google_firebase_database_instance` creates a `DEFAULT_DATABASE` instance named
  `<project>-default-rtdb` in `europe-west1` and returns a
  `<project>-default-rtdb.europe-west1.firebasedatabase.app` URL. Firebase's page says a default
  instance may be in any of the three locations; the provider's behaviour is not checked. Settled
  by Check 820.
- **U3.** Whether Cloud Run jobs are offered in every Cloud Run region. Google's page lists no
  exception for jobs and does not say so either.
- **U4.** Where Cloud Build's `global` default pool runs. Fugaro names a region for every build
  (`BuildRegion`), so it matters only if some build does not; none was found.
- **U5.** Which services Google's own residency commitment covers (cloud.google.com/terms/data-residency
  could not be read in full). The documents in LP11 state what Fugaro configures, not what Google
  commits to, and link Google's page.
- **U7.** Which regions Secret Manager's user-managed replication and Cloud Logging's buckets
  offer, region by region. The pages were read for the regions named here, not for every region
  the mapping accepts; LP1 reads the full lists and dates them.
- **U8.** What the legacy host and each regional host answer for a default database that lives
  at another: a redirect, a 404 or something else. LP15's read depends on it. Settled by Check
  820, which reads all three hosts for the scratch project.
- **U9.** That `ignore_changes` holds, with the provider version the modules pin, for a secret's
  replication on a container Terraform adopts by import and for a Realtime Database instance's
  region. The tests of LP5a and LP2 run under the mock provider, which cannot say. Settled by
  Check 821, or sooner by O11's plan-only run.
- **U6.** Whether a user-managed secret in one region is readable by a Cloud Run job in the same
  region with no other change. Secret Manager's page implies it; Check 820 measures it.
