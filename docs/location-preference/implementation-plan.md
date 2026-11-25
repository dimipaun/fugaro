# Implementation plan: one location preference for an installation

**Status: plan only; nothing is implemented; no task is approved.** Each task needs the owner's
explicit go by name. It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), checked against `origin/main` at `252329076` on 2026-11-20.

**The answers that shaped it** (all the owner's, 2026-11-20, all as recommended): the region stays
the one stored setting and the place is derived from it (D3080); US and EU are named, and any other
region works with its fallbacks stated (D3081); a new installation's data at rest is in the place,
secrets and run logs included (D3082); model calls are outside the promise (D3083); a workspace's
members may differ (D3084); Firestore goes in the installation's region where offered (D3085); EU
with no region is `europe-west1` (D3086); what an installation has is a record in the project
config (D3087, amended after review: it is this machine's own and is never an input); a changed region is refused at once (D3088); an installation made before this work
gets a missing database in its derived place (D3089); the location's own note is removed
everywhere (D3090); a database found elsewhere is adopted, recorded and reported (D3091).

**Answered after the reviews** (the owner, 2026-11-20): D3087 and D3088 confirmed as amended;
new secrets are regional on every installation (D3092); four further exceptions to the promise
(D3093); the launch order with AB7 below (D3094); the key service's redeploy is a line in
`docs/release.md` (D3095); a region that cannot be checked is accepted with a line (D3096); a
fourth review round (D3097, not the recommendation).

**What always-a-budget's AB7 waits on is LP2** (D3029, R9). LP2 needs only LP1 and LP8, and all
three are small. Because AB7 writes files this plan also writes, the plan asks that AB7 be
launched once LP2, LP4 and LP8 have merged, and that LP3, LP6, LP10 and LP14 be launched once
AB7 has, which the owner accepted (D3094).

**Review round 1 changed the design in one place.** What Terraform and the CLI are told about a
thing that exists is read from the cloud, for that thing, every time. The record is written from
that reading and is never read back as an input (R5). The first draft fed the record to Terraform,
which would have planned a replacement of every existing secret between two merges.

**Review round 2 made the secrets simpler.** A container that exists keeps its replication
because Terraform is told to ignore that one field on it; every container created from then on is
replicated in the region, on an old installation too. No policy is inferred from what exists, and
no path has to read the cloud before it plans a secret, `fugaro update --forget` included, which
plans with no discovery (`ForgetSpec`, `internal/infra/forget.go`).

**Review round 3 found two ways the plan as written would have broken something**, and both are
fixed here. A fourth round, which the owner asked for (D3097), read those fixes and found no
path on which an existing installation plans a replacement; what it did find is in the README. Discovery
skips a Realtime Database that is already in Terraform's state, which is every installation after
its first apply, so LP2 also has Terraform ignore the region of an instance it manages. And
`--region` is a flag on every cloud command, used and tested as an override on read commands, so
LP8's refusal is scoped to the commands that plan (D3088, amended after review).

## Conventions every part follows

`CLAUDE.md` and, for LP2, LP5a, LP6 and LP14, `.claude/rules/terraform.md`; for LP15, `.claude/rules/ee.md`; for LP11,
`.claude/rules/credentials-service.md` and `.claude/rules/website.md`. They are not restated here.

- **Always:** targeted tests before a commit (`-run`, never a whole package unless the task says
  so); `terraform fmt -check -recursive deploy/terraform`; golden plans regenerated with
  `scripts/gen-golden-plans.sh` and never edited by hand; `TF_DATA_DIR` outside the tree; a new
  refusal prints its fix; a task's pull request names its task id and the `D#` it implements.
- **Ask first:** a new resource type or a new kind of principal in `internal/infra/tf/cover.go`
  (none is expected; LP5a adds one environment variable's name to `intakeServiceEnv` there, on
  purpose and named in its pull request); a new dependency (none is expected); any change to `.github/workflows/`
  (staged under `docs/pending-workflows/`, never edited).
- **Never:** a confirmation step (D3090, `docs/no-prompt-policy/`); a wait on stdin; a plan that
  deletes or replaces anything on an existing installation; `terraform apply`, `fugaro setup` or
  `fugaro update` against a real project from a run; a secret-shaped literal in a test; a change to
  what `region` means (F17).
- **Live checks are the owner's.** Checks 820 and 821 are run by the owner from a script. No agent
  performs a step of them.

## Global constraints and review focus

| What no single task would test | The task that pins it |
| --- | --- |
| An installation at the previous version converges with `fugaro update` alone: no question, no other command, no destroy or replace in any root's plan | LP14 (`TestAnInstallationFromBeforeTheRecordConverges`) |
| A thing that exists is never given the mapping's answer, at every commit of `main` between this plan's merges and on every path that plans, `fugaro update --forget` (which plans with no discovery) included: each existing secret keeps its own replication, the log bucket and both databases their own location | LP5a (`TestAnExistingSecretKeepsItsOwnReplication`, `TestForgetPlansNoSecretChange`), LP6 (`TestAGlobalLogBucketPlansNoChange`, `TestForgetPlansWhatItPlansToday`), LP2 (`TestAManagedInstanceOfAnEUInstallationPlansNoChange`) |
| The record is never an input: a config whose record says anything at all plans what a config with no record plans | LP14 (`TestTheRecordIsNeverAnInput`) |
| A mixed installation (automatic and regional secrets side by side, which every old installation becomes) plans no change | LP5a (`TestAMixedInstallationPlansNoSecretChange`) |
| The promise is made only by LP11's documents, so a release cut between two of this plan's merges promises nothing it does not keep | LP11 |
| A run in flight is untouched: the record is a config field no run reads, and nothing a run holds is replaced | LP14 (`TestUpdateLeavesARunInFlightRunning` still passes, unchanged) |
| No fallback leaves its place, the scheduler's included | LP1 (`TestNoFallbackLeavesItsPlace`) |
| A new EU installation plans every located resource in the EU | LP14 (`TestANewEUInstallationPlansEverythingInTheEU`, on the golden plans) |
| Setup with no terminal and no flag fails at once, naming the flags | LP4 (`TestSetupWithNoTerminalNamesTheLocationFlags`) |
| A Realtime Database that exists is never given the mapping's region | LP2 (`TestAnExistingInstanceKeepsItsOwnRegion`) |
| `--location` and `--region` cannot disagree | LP4 (`TestALocationAndARegionThatDisagreeAreRefused`) |

## Slices and tasks

**Slice 1, the table and the budget database (useful alone: it is what AB7 waits on):** LP1, LP2.
**Slice 2, what exists:** LP0, LP7, LP8, LP15. **Slice 3, the other located things:** LP3, LP5a, LP5b,
LP5c, LP6. **Slice 4, what a person sees:** LP4, LP9, LP10, LP12. **Slice 5, joined and proven:** LP14.
**Documents and checks:** LP11, LP13.

### LP1: the mapping
- Acceptance: `internal/place` answers, with no cloud call, for any region: its place (US, EU or
  none, with London and Zürich in none); for a place, its default region (`us-east5`,
  `europe-west1`, D3086); and for a region, where a new installation's Firestore database,
  Realtime Database, secrets and run log bucket go, each with the reason when it is not the region
  itself (the table in the reconciliation). The lists it carries are dated against Google's pages.
  Every region it accepts has a Cloud Scheduler location in its own place, `europe-west10`
  included, which `nearestSchedulerRegion` lacks today. For the log bucket it answers the region
  where Cloud Logging offers it, else the place's multi-region (`us`, `eu`), else `global` with
  the reason.
- Files: `internal/place/place.go` (new), `internal/place/place_test.go` (new),
  `internal/backend/gcp/names.go` (the scheduler's missing entries),
  `docs/location-preference/locations.md` (new: the lists and the date they were read).
- Interfaces: produces `place.Of`, `place.DefaultRegion`, `place.For`. Consumes
  `gcp.SchedulerRegion` in its test only, which is therefore an external test package
  (`place_test`): `internal/backend/gcp` imports `internal/place` after LP5b.
- Tests: `TestThePlaceOfARegion`, `TestTheMappingForUSAndEU`, `TestNoFallbackLeavesItsPlace`,
  `TestARegionInNeitherPlaceStatesItsFallbacks`, `TestTheListsAreDated`,
  `TestEveryRegionOfAPlaceHasASchedulerLocationInIt`.
- Command: `go test ./internal/place/ -count=1 && go test ./internal/backend/gcp/ -run 'SchedulerRegion' -count=1`.
- Red step: the package does not exist; `TestThePlaceOfARegion` fails to compile.
- Depends on: none.
- Commit: `place: the mapping from a region to where an installation's data goes (LP1, D3080, D3081, D3085, D3086)`.
- Reviewer tier: standard.

### LP2: a new budget database goes where the mapping says
- Acceptance: the Firebase module's Realtime Database region is an input, and the resource
  ignores changes to it. So an instance Terraform already manages keeps its region whatever the
  input says: discovery skips a resource that is in state (`DiscoverFirebase`), which is every
  installation after its first apply. An instance that exists and is not yet in state is found by
  discovery wherever it is, adopted under the rules that already decide it is ours, and imported
  at its own location (R3, R4, D3091). For none the region is `place.For`'s answer (D3089). An
  installation in a European region whose instance is in `us-central1` plans no change.
  `--print-vars`, which runs no discovery by design, prints the mapping's region with a line
  saying that an instance that exists keeps its own.
- Files: `deploy/terraform/gcp/modules/firebase/firebase.tf`,
  `deploy/terraform/gcp/modules/firebase/variables.tf`,
  `deploy/terraform/gcp/roots/firebase/variables.tf`, `deploy/terraform/gcp/roots/firebase/main.tf`,
  `deploy/terraform/gcp/roots/firebase/tests/firebase.tftest.hcl`, `internal/infra/firebase.go`,
  `internal/infra/discover_firebase.go`, `internal/cli/init_firebase.go` (`initFirebase` writes
  the variables before discovery runs, so the order changes there), the Firebase tfvars fixtures under
  `internal/infra/testdata` that `scripts/gen-golden-plans.sh` reads, and the
  `firebase*.plan.json` golden plans (generated). Past five for the root, its test and the
  generated plans.
- Interfaces: consumes `place.For` (LP1). Produces `FirebaseSpec`'s database region.
- Tests: `TestAnExistingInstanceKeepsItsOwnRegion`, `TestAManagedInstanceOfAnEUInstallationPlansNoChange`,
  `TestANewInstanceGoesWhereTheMappingSays`, `TestAnInstanceElsewhereIsAdopted`,
  `TestPrintVarsSaysAnExistingInstanceKeepsItsOwn`, and the root's `terraform test`, with one
  two-run case: apply under the mock provider with `us-central1`, then plan with `europe-west1`
  and assert that the planned region is still `us-central1`. Asserting "no replacement" would
  prove nothing: the mock provider plans none with or without `ignore_changes` (measured in
  review round 4), while the planned value differs.
- Command: `go test ./internal/infra/ -run 'AnExistingInstanceKeeps|AManagedInstance|ANewInstanceGoes|AnInstanceElsewhere|Firebase' -count=1 && go test ./internal/cli/ -run 'Firebase|PrintVars' -count=1 && go test ./deploy/terraform/... -count=1 && go test -tags terraform ./internal/infra/... -run 'Firebase|Golden' -count=1 && (cd deploy/terraform/gcp/roots/firebase && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test)`.
- Red step: `TestAnInstanceElsewhereIsAdopted` meets today's refusal of a default instance outside
  `firebaseDBRegion`.
- Depends on: LP1 (kept, behaviour: it needs the mapping's answer, and the mapping is smaller than
  a contract for it would be); LP8 (kept, order: once the region decides a permanent location, a
  mistyped `--region` must already be refused).
- Commit: `firebase: the budget database's region is the mapping's, and an existing one keeps its own (LP2, D3029, D3089, D3091)`.
- Reviewer tier: opus, mandatory independent review (Terraform, a data-bearing type).

### LP0: the locations record
- Acceptance: the project config carries an optional `locations` block, on this machine only: the
  Firestore location, the Realtime Database location, the secrets' replication (`automatic`, a
  list of regions, or `mixed`) and the log bucket's location. It validates strictly, an absent key
  means "not known yet", and it is **not** published in the shared config, whose strict decoding
  would stop a teammate's older CLI on a new field (`SharedVersion`, `internal/localcfg/shared.go`).
  A short document says what the record is and that nothing reads it as an input (R5).
- Files: `internal/localcfg/locations.go` (new), `internal/localcfg/localcfg.go` (the one field
  and its validation call), `internal/settings/registry.go`, `internal/cli/writers_labels.go`,
  `schemas/settings.schema.json`, `docs/location-preference/record.md` (new). Six, for the two
  censuses a config key is listed in.
- Interfaces: produces `localcfg.Locations` and `Config.Locations`.
- Tests: `TestLocationsValidate`, `TestLocationsAreNotPublished`,
  `TestAConfigWithoutLocationsStillLoads`, `TestAnOlderSharedConfigStillDecodes`.
- Command: `go test ./internal/localcfg/ -run 'Locations|AConfigWithoutLocations|AnOlderSharedConfig' -count=1 && go test ./internal/settings/ ./schemas/ -count=1 && go test ./internal/cli/ -run 'EveryUnwrittenRow|Settings' -count=1`.
- Red step: `TestLocationsValidate` fails on an unknown field `locations`.
- Depends on: none.
- Commit: `localcfg: the locations record, this machine's own (LP0, D3087)`.
- Reviewer tier: standard.

### LP3: Firestore goes where the mapping says, and an existing one is adopted
- Acceptance: `FirestoreStep` creates the database in the location it is given, which is
  `place.For`'s answer for the installation's region (D3085, D3089); a native-mode database that
  exists is adopted wherever it is, if empty or ours, and a line says where it is when that is
  outside the place (R3, D3091); a database that is not native mode is still refused.
  `infra.FirestoreLocation` is gone. The location's note is still printed, with the location the
  step holds (LP10 removes it). The help texts that name `us-east5` are LP10's.
- Files: `internal/infra/firestore.go`, `internal/infra/firestore_test.go`,
  `internal/gcpfake/firestore.go` (its list of locations), `internal/cli/init_firebase.go` (the
  step's construction and the three texts that name the constant), and the tests and scripts that
  name the constant: `internal/cli/init_firestore_test.go`, `init_confirm_test.go`,
  `init_review_test.go`, `update_test.go`, `init_converge_test.go`,
  `managed_money_steps_test.go`, `scripts/live-checks/check27.sh` and `check27_test.sh`. Past
  five: one constant with twenty uses in tests.
- Interfaces: consumes `place.For` (LP1).
- Tests: `TestFirestoreIsCreatedWhereTheMappingSays`, `TestAFirestoreDatabaseElsewhereIsAdopted`,
  `TestADatastoreModeDatabaseIsStillRefused`.
- Command: `go test ./internal/infra/ ./internal/gcpfake/ -run 'Firestore' -count=1 && go test ./internal/cli/ -run 'Firestore|Firebase|Confirm|Review|Converge|TestManaged|RefusalMatrix|TestUpdate' -count=1 && go test ./scripts/ -run 'TestLiveCheckScripts' -count=1`.
- Red step: `TestAFirestoreDatabaseElsewhereIsAdopted` meets `checkExisting`'s refusal.
- Depends on: LP1 (kept, behaviour); LP8 (kept, order, as LP2); LP2 (kept: one file,
  `internal/cli/init_firebase.go`).
- Commit: `firestore: the location is the mapping's, and a database elsewhere is adopted (LP3, D3085, D3091)`.
- Reviewer tier: standard.

### LP4: setup asks the place
- Acceptance: a first run of `fugaro setup` asks where the installation lives (US, EU, or a region
  typed in full), with US the default; `--location us|eu` answers it, as does `location` in the
  answers file; `--region` still answers it precisely; the two together must agree or setup refuses
  at once (R2); with no terminal and neither flag it fails at once naming both. Before anything is
  created it prints where each thing will go, one line per row, and every fallback with its reason
  (R7), and asks nothing about them.
- Files: `internal/cli/wizard_stage.go`, `internal/cli/init_inputs.go`, `internal/cli/init.go`
  (the flag), `internal/cli/setup_answers.go`, `schemas/setup-answers.schema.json`,
  `website/content/cli-reference.md`, `plugin/skills/setup/SKILL.md`,
  `internal/cli/init_inputs_test.go`, `internal/cli/setup_answers_test.go`,
  `internal/cli/testdata/region_prompts.golden`. Past five
  for the flag's censuses.
- Interfaces: consumes `place.Of`, `place.DefaultRegion`, `place.For` (LP1).
- Tests: `TestSetupAsksThePlace`, `TestALocationAndARegionThatDisagreeAreRefused`,
  `TestSetupWithNoTerminalNamesTheLocationFlags`, `TestSetupPrintsWhereEachThingGoes`,
  `TestSetupAnswersSchema`.
- Command: `go test ./internal/cli/ -run 'SetupAsksThePlace|ALocationAndARegion|SetupWithNoTerminalNames|SetupPrintsWhere|SetupAnswers|CliReferenceNames' -count=1 && go test ./plugin/... -count=1 && go test ./ee/clidoc/ -run CliReferenceIsCurrent -count=1`.
- Red step: `TestSetupAsksThePlace` finds the `--region` question.
- Depends on: LP1 (kept, behaviour); LP8 (kept: one file, `internal/cli/init.go`).
- Commit: `setup: the first question is the place, and --location answers it (LP4, D3080, D3081, D3086)`.
- Reviewer tier: opus, mandatory independent review (a plugin skill changes).

### LP5a: a secret created from now on is replicated in the region
- Acceptance: each of the six Terraform secret resources declares user-managed replication in the
  installation's region and ignores changes to its replication (`ignore_changes`, as the module
  already does for other fields). So a container that exists, automatic or not, plans no change
  on any path, `fugaro update --forget` and an adopted container included, and a container
  created from now on is in the region, on an old installation too (D3082, F9, R5). The intake
  service is given the region to replicate in by an environment variable (LP5c reads it).
- Files: `deploy/terraform/gcp/modules/installation/secrets.tf`, `licence.tf`, `orchestrator.tf`,
  `credentials.tf`, `intake.tf` (all in `modules/installation`),
  `deploy/terraform/gcp/modules/repo/secrets.tf`, `internal/infra/tf/cover.go` and
  `cover_test.go` (`intakeServiceEnv` lists the intake service's variables by name, and four
  golden plans carry that service), the two roots' tests
  (`roots/installation/tests/*.tftest.hcl`, `roots/repo/tests`), and the generated
  `installation*.plan.json` and `repo*.plan.json`. Past five: six resources that must change
  together.
- Interfaces: produces the intake service's variable. Consumes `var.region`, which both modules
  already have.
- Tests: `TestAnExistingSecretKeepsItsOwnReplication`, `TestAMixedInstallationPlansNoSecretChange`,
  `TestANewSecretIsInTheRegion`, `TestForgetPlansNoSecretChange`,
  `TestAnAdoptedSecretPlansNoChange`, and `terraform test` in the `installation` and `repo` roots.
  The "plans no change" cases are two-run tests under the mock provider, since no hermetic test
  can hold a real container: apply with automatic replication declared, then plan the new
  declaration and assert that the planned replication is still automatic (the planned value, not
  the absence of a replacement, which the mock provider never plans). What the real provider does on an adopted container is
  U9, and Check 821 measures it after the release (O11).
- Command: `go test -tags terraform ./internal/infra/... -run 'AnExistingSecretKeeps|AMixedInstallation|ANewSecretIsInTheRegion|ForgetPlansNoSecret|AnAdoptedSecret|Golden' -count=1 && go test ./internal/infra/tf/ -run 'GoldenPlansAreCovered|Intake' -count=1 && go test ./deploy/terraform/... -count=1 && for r in installation repo; do (cd deploy/terraform/gcp/roots/$r && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test); done && sh deploy/terraform/scan.sh`.
- Red step: `TestANewSecretIsInTheRegion` finds `auto {}`.
- Depends on: none.
- Commit: `secrets: a secret created from now on is replicated in the region, and an existing one keeps its own (LP5a, D3082)`.
- Reviewer tier: opus, mandatory independent review (secrets, Terraform).

### LP5b: a secret the CLI creates is replicated in the region
- Acceptance: the CLI's creator of a secret container creates it with user-managed replication in
  the installation's region, which it already holds (`gcp.Options.Region`). It imports neither
  `internal/infra`, which imports it, nor `internal/place`: for a secret the mapping's answer is
  the region itself. The fake stores each container's replication, and accepts and checks a
  user-managed policy.
- Files: `internal/backend/gcp/secrets.go`, `internal/backend/gcp/secrets_test.go`,
  `internal/gcpfake/secrets.go`.
- Interfaces: none new.
- Tests: `TestASecretCreatedByTheCLIIsInTheRegion`, `TestTheFakeChecksAUserManagedPolicy`.
- Command: `go test ./internal/backend/gcp/ ./internal/gcpfake/ -run 'Secret|UserManagedPolicy' -count=1`.
- Red step: the fake refuses a request without `automatic`.
- Depends on: none.
- Commit: `secrets: the CLI's own secrets are replicated in the region (LP5b, D3082, D3092)`.
- Reviewer tier: opus, mandatory independent review (secrets).

### LP5c: the intake service's secrets are replicated in the region
- Acceptance: the deployed intake service, which has no project config, creates a container
  replicated in the region its environment names. Its shape check (`Shape`,
  `internal/intake/server.go`) does not read replication and is unchanged. With the variable absent (a service deployed before LP5a) it
  behaves as today.
- Files: `internal/intake/gcp.go`, `internal/intake/gcp_test.go`, `internal/cli/intake_serve.go`.
- Interfaces: consumes the variable LP5a's module sets.
- Tests: `TestAnIntakeSecretIsInTheRegion`, `TestIntakeWithNoRegionIsAutomatic`.
- Command: `go test ./internal/intake/ -run 'Intake' -count=1 && go test ./internal/cli/ -run 'IntakeServe' -count=1`.
- Red step: `TestAnIntakeSecretIsInTheRegion` finds an automatic container.
- Depends on: LP5a (kept, behaviour: the variable's name and the module that sets it).
- Commit: `intake: its secrets are replicated in the region (LP5c, D3082)`.
- Reviewer tier: opus, mandatory independent review (secrets).

### LP6: a new installation's run log bucket is in its region
- Acceptance: the log bucket's location is an input. Discovery lists the project's log
  buckets in every location and finds Fugaro's by its id, so a later change to the mapping cannot
  hide one; one that exists keeps its own location, and for none it is
  `place.For`'s. The import address, the log view's path and doctor's sink check are built from
  that location and stop assuming `global`. An installation whose bucket is `global` plans no
  change, and `fugaro update --forget`, which turns log isolation off, runs no discovery and deletes
  the bucket, plans what it plans today; the undelete command it prints names the bucket's own
  location, which that path reads with the same listing call and nothing more (F10).
- Files: `deploy/terraform/gcp/modules/installation/logging.tf`,
  `deploy/terraform/gcp/modules/installation/variables.tf`, the installation root's
  `variables.tf`, `main.tf` and `tests/installation.tftest.hcl` (which pins `global` five times),
  `internal/infra/discover.go` (the read and its refusal text), `internal/infra/imports.go`,
  `internal/infra/tfvars.go`, `internal/infra/forget.go` (`LogBucketUndelete`) and
  `forget_test.go`, `internal/infra/discover_test.go` and `internal/cli/init_test.go`
  (`TestInitForgetWarnsLogBucketUndelete`), `internal/e2e/update_test.go` (which pins `locations/global`),
  `internal/cli/init.go` (where discovery's findings reach the spec),
  `internal/cli/doctor_batch.go` (`logBucketPath`), the tfvars fixtures, and the generated
  `installation*.plan.json`. Past five: one location through the module, its discovery and its
  one other reader.
- Interfaces: consumes `place.For` (LP1).
- Tests: `TestAGlobalLogBucketPlansNoChange`, `TestANewLogBucketIsInTheRegion`,
  `TestTheLogBucketIsImportedWhereItIs`, `TestABucketWhereTheMappingNoLongerPointsIsFound`,
  `TestForgetPlansWhatItPlansToday`,
  `TestDoctorReadsTheLogBucketWhereItIs`.
- Command: `go test ./internal/infra/ -run 'LogBucket' -count=1 && go test ./internal/cli/ -run 'DoctorReadsTheLogBucket|DoctorBatch|Forget' -count=1 && go test ./internal/e2e/ -run 'Update' -count=1 && go test ./deploy/terraform/... -count=1 && go test -tags terraform ./internal/infra/... -run 'Golden|Installation|LogBucket' -count=1 && (cd deploy/terraform/gcp/roots/installation && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test)`.
- Red step: `TestANewLogBucketIsInTheRegion` finds `global`.
- Depends on: LP1 (kept, behaviour); LP5a (kept: the same root test file and golden plans); LP4
  (kept: one file, `internal/cli/init.go`); LP7 (kept: one file, `internal/gcpfake/logging.go`,
  whose listing route LP7 adds).
- Commit: `logging: a new installation's run log bucket is in its region (LP6, D3082)`.
- Reviewer tier: opus, mandatory independent review (Terraform, a data-bearing type).

### LP7: what the cloud holds
- Acceptance: `infra.ObserveLocations` reads, with no write and no question: the Firestore
  database's location; the default Realtime Database instance's; the Fugaro log bucket's; whether
  the Fugaro-labelled secret containers are automatic, regional or both; and the runs bucket's
  location. It returns its own plain type, with nothing for what does not exist and, for a thing
  it may not read, an explicit "unreadable" that names the permission, never a guess. Its one
  use is the record (LP14): no plan reads it.
- Files: `internal/infra/locations_observe.go` (new), `internal/infra/locations_observe_test.go`
  (new), `internal/gcpfake/logging.go` (a route that lists log buckets in every location; the
  fake answers one bucket by name today), and nothing in `internal/gcpfake/secrets.go`, which is
  LP5b's and which stores a container's replication only after LP5b.
- Interfaces: produces `infra.ObserveLocations` and `infra.Observed`.
- Tests: `TestWhatAnInstallationFromBeforeThisWorkHolds`, `TestAMixOfSecretsIsReportedAsMixed`,
  `TestNothingIsReportedForWhatDoesNotExist`, `TestAnUnreadableThingIsNamedNotGuessed`.
- Command: `go test ./internal/infra/ -run 'WhatAnInstallationFromBefore|AMixOfSecrets|NothingIsReported|AnUnreadableThing' -count=1`.
- Red step: the function does not exist.
- Depends on: LP5b (kept, behaviour: the secrets fake answers automatic for every container
  until LP5b, so a mix cannot be tested before it).
- Commit: `infra: what the cloud holds, by location (LP7, D3087)`.
- Reviewer tier: standard.

### LP8: a region that is not the installation's is refused where it would plan
- Acceptance: in `fugaro setup` and `fugaro update`, with `--repo` and with `--plan-only` too
  (both doors' two loaders, `loadInitConfig` and `loadRepoConfig`), a `--region` that differs from the project config's region, or from the runs bucket's own
  location on a machine with no config yet, is refused before anything is planned. The refusal
  names the installation's region, says it cannot move, and prints the fix: the same command with
  that region (D3088 as amended, R6, F4, F12). Where the bucket's location cannot be read (a
  launcher may lack `storage.buckets.get`), the typed region is accepted and one line says it was
  not checked (R10). Every other command keeps `--region` as the per-invocation override it is
  today (`cloudFlags`, `internal/cli/cloud.go`), and the tests that use it so
  (`TestLsInAnotherRegionNamesTheRunsRegion`, doctor's and `update --check`'s, which never reaches
  either loader) pass unchanged. One existing test of `update` changes: the `--region us-west1`
  row of `TestUpdateManagedRefusesASessionWideningFlag` would now meet this refusal before the
  one it asserts, so the row passes the installation's own region, which keeps its intent.
- Files: `internal/cli/init.go` (`loadInitConfig`, `loadRepoConfig`),
  `internal/cli/init_new_machine.go`, `internal/cli/update_managed_test.go`,
  `internal/gcpfake/gcs.go` (the fake's bucket answers no location today; it answers the one the
  bucket was made with, so existing new-machine tests are checked and silent),
  `internal/cli/region_refusal_test.go` (new).
- Interfaces: none new.
- Tests: `TestAChangedRegionIsRefusedBySetupAndUpdate`, `TestANewMachinesRegionMustBeTheInstallations`,
  `TestTheSameRegionIsAccepted`, `TestAnUnreadableBucketIsSaidNotRefused`,
  `TestReadCommandsKeepTheRegionOverride`, `TestARepoUpdateInAnotherRegionIsRefused`,
  `TestPlanOnlyInAnotherRegionIsRefused`.
- Command: `go test ./internal/cli/ -run 'AChangedRegion|ANewMachinesRegion|TheSameRegion|AnUnreadableBucket|ReadCommandsKeep|ARepoUpdateInAnotherRegion|PlanOnlyInAnotherRegion|UpdateManagedRefuses|LsInAnotherRegion|UserAddDoesNotPersistRegion|UpdateCheck|DoctorShared|RefusalMatrix|TestManaged' -count=1`.
- Red step: `TestAChangedRegionIsRefusedBySetupAndUpdate` sees the update go on in the typed region.
- Depends on: none.
- Commit: `init: a region that is not the installation's is refused before a plan (LP8, D3088)`.
- Reviewer tier: standard.

### LP9: a workspace member's database region comes from the member
- Acceptance: linking a member records its `database_region` from the member's own database URL,
  so a member in the EU is read at its own host with no flag; the flag still wins when given, and
  a member already linked is left as it is (F14, D3084). A legacy host (`<db>.firebaseio.com`) records an empty region, never
  `us-central1`, because `workspaceDatabase` builds a regional host from any non-empty value and
  `us-central1` has none. A derived value is held to `europe-west1` and `asia-southeast1`. The
  service itself still accepts any region-shaped string; its check of the token's issuer is what
  bounds a wrong one, and that is unchanged.
- Files: `internal/cli/workspace.go`, `internal/cli/workspace_region_test.go` (new).
- Interfaces: consumes the member's `budget.rtdb_url`.
- Tests: `TestAMembersDatabaseRegionIsReadFromTheMember`, `TestTheFlagStillWins`,
  `TestARegionThatIsNoDatabaseLocationIsRefused`, `TestALegacyHostRecordsNoRegion`.
- Command: `go test ./internal/cli/ -run 'AMembersDatabaseRegion|TheFlagStillWins|ARegionThatIsNoDatabase|ALegacyHost|Workspace' -count=1`.
- Red step: a member with a `europe-west1` database is linked with an empty region.
- Depends on: none.
- Commit: `workspace: a member's database region is read from the member (LP9, D3084)`.
- Reviewer tier: opus, mandatory independent review (the workspace's credential path reads it).

### LP10: no step confirms a location
- Acceptance: `confirmLocation` is gone. Where the Firestore database is created the line saying
  where, and that it is permanent, is printed and the database is created, in every session the
  stage itself may run in (D3090). `stepConfirmation`'s comment names two steps that are the
  person's alone, not three. AB7's test of the note's order asserts instead that no note is
  printed (O2). The help texts that say `us-east5` is permanent, that the location has its own
  confirmation and that a database in another location is refused (`setup.go`, the `init.go` flag
  text, and their copy in the CLI reference) say what is now true.
  `docs/no-prompt-policy/reconciliation.md` records that D12 of 2026-10-20 no longer
  covers the location.
- Files: `internal/cli/init_firebase.go`, `internal/cli/init.go`, `internal/cli/setup.go`,
  `internal/cli/update.go` (its help text), `internal/cli/restore.go` (two texts saying a
  database of another location is never adopted), `internal/cli/init_review.go` and
  `testdata/init_review.golden` (the review's line about the location),
  `internal/cli/report_test.go` (`TestInitHelpMentionsFirestoreStep`),
  `website/content/cli-reference.md`, the tests that pin the note (`init_confirm_test.go`,
  `init_converge_test.go`, `init_inputs_test.go`, `init_review_test.go`,
  `init_firestore_test.go`, `managed_money_steps_test.go`, `setup_refusals_test.go`,
  `update_test.go`, `testdata/region_prompts.golden`, all in `internal/cli`), AB7's `init_firebase_always_test.go` (it does not exist on `main` today; AB7
  adds it), the no-prompt lint's allow-list if it names the site, and
  `docs/no-prompt-policy/reconciliation.md`. Past five: one note pinned in ten tests and four help texts.
- Interfaces: none.
- Tests: `TestNoStepConfirmsALocation`, `TestTheLocationLineIsPrintedBeforeTheDatabase`, and the
  no-prompt lint.
- Command: `go test ./internal/cli/ -run 'NoStepConfirmsALocation|TheLocationLine|NoPrompt|RefusalMatrix|TheTypedSteps|Confirm|Converge|Review|Firestore|TestManaged|TestUpdate|InitHelp|InitReview|CliReferenceNames|Docs' -count=1 && go test ./ee/clidoc/ -run CliReferenceIsCurrent -count=1`.
- Red step: `TestNoStepConfirmsALocation` finds the note and a `NeedsYouError` with no terminal.
- Depends on: LP3, LP4 and LP6 (kept: shared files, `init_firebase.go`, `init.go`,
  `cli-reference.md` and the tests and golden file they write). **Not launched
  before always-a-budget's AB7 has merged** (F19, O2).
- Commit: `init: no step confirms a location (LP10, D3090)`.
- Reviewer tier: opus, mandatory independent review (a guard is removed).

### LP12: `fugaro doctor` says where the data is
- Acceptance: doctor prints one row with the installation's region, its place and where each
  recorded thing is; a thing outside the place is information, never a failure (D3091); an
  installation with no record yet prints its region and place and nothing about the rest, because
  until LP14 no command writes one.
- Files: `internal/cli/doctor_locations.go` (new), `internal/cli/doctor_locations_test.go` (new),
  `internal/cli/doctor.go` (the registration).
- Interfaces: consumes `localcfg.Locations` (LP0), `place.Of` (LP1).
- Tests: `TestDoctorSaysWhereTheDataIs`, `TestADatabaseOutsideThePlaceIsInformation`,
  `TestDoctorWithNoRecordPrintsTheRegionAlone`.
- Command: `go test ./internal/cli/ -run 'DoctorSaysWhere|ADatabaseOutsideThePlace|DoctorWithNoRecord' -count=1`.
- Red step: doctor has no such row.
- Depends on: LP0 (kept: the record's type), LP1 (kept, behaviour).
- Commit: `doctor: where the installation's data is (LP12, D3091)`.
- Reviewer tier: standard.

### LP14: the record is written by setup and update, and the whole is proven
- Acceptance: `fugaro update` and `fugaro setup` write the record from `infra.ObserveLocations`
  after the last apply, into this machine's config, and nothing reads it back as an input. An installation at
  the previous version converges with `fugaro update` alone: its record reads `us-east5`,
  `us-central1`, `automatic` and `global`, no root plans a destroy or a replace, nothing is asked
  and a run in flight is untouched. A new EU installation plans every located resource in the EU.
- Files: `internal/cli/init_stages.go`, `internal/cli/locations_end_to_end_test.go`
  (new), new tfvars fixtures for an EU installation under `internal/infra/testdata`, and golden
  plans `installation-eu.plan.json` and `firebase-eu.plan.json` (new, generated, with their
  entries in `scripts/gen-golden-plans.sh`).
- Interfaces: consumes everything above.
- Tests: `TestAnInstallationFromBeforeTheRecordConverges`, `TestTheRecordIsNeverAnInput`,
  `TestANewEUInstallationPlansEverythingInTheEU`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'AnInstallationFromBeforeTheRecord|TheRecordIsNeverAnInput|ANewEUInstallation|UpdateLeavesARunInFlight' -count=1 && go test -tags terraform ./internal/infra/... -count=1 && go test -timeout 40m ./internal/cli/ -count=1`.
- Red step: `TestAnInstallationFromBeforeTheRecordConverges` finds no record after the update.
- Depends on: LP0, LP2, LP3, LP4, LP5a, LP5b, LP5c, LP6, LP7, LP9, LP12 (kept, behaviour: it
  runs them together). **It shares
  `internal/cli/init_stages.go` with always-a-budget's AB7** and is not launched while AB7's run
  is live.
- Commit: `update: an installation's locations are recorded from what it has (LP14, D3087, D3089)`.
- Reviewer tier: opus, mandatory independent review (the upgrade path).

### LP11: the documents say where the data lives
- Acceptance: `SECURITY.md` and `website/content/security.md` carry the same section: what
  choosing US or EU places where, the exceptions D3082 names, that model calls are outside it
  (D3083), what an installation made before this work has, and what a workspace across places
  means (D3084), and the two cases in which run output is not in the run log bucket at all
  (log isolation turned off, and the build and system logs Fugaro leaves in `_Default`); `docs/gcp-setup.md` describes the question; `docs/m9-budget-and-dashboard/spec.md`
  D4 gets a note that it holds for installations made before this work; the release notes'
  `### For operators` says run `fugaro update` and that it moves nothing (R8). A docs test pins the
  section in both files to the mapping.
- Files: `SECURITY.md`, `website/content/security.md`, `docs/gcp-setup.md`,
  `docs/m9-budget-and-dashboard/spec.md`, `internal/cli/docs_location_test.go` (new), and the
  release's notes file under `docs/releases/`.
- Interfaces: consumes `place.For` in the test.
- Tests: `TestDocsSayWhereTheDataLives`.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`.
- Red step: `TestDocsSayWhereTheDataLives` finds no section.
- Depends on: LP14, LP10 and LP15 (kept, behaviour: it states what ships).
- Commit: `docs: where an installation's data lives (LP11, D3082, D3083, D3084)`.
- Reviewer tier: opus, mandatory independent review (`SECURITY.md`).

### LP13: the two live checks, as scripts
- Acceptance: one script per check, each one command for the owner, with its steps file and its
  row in `docs/gcp-live-checklist.md`; Check 27's row there no longer says the location is typed.
- Files: `scripts/live-checks/check-820.sh`, `check-820_test.sh`, `check-821.sh`,
  `check-821_test.sh` (all new, named as their siblings in that folder are),
  `docs/location-preference/check820-steps.md` (new), `docs/location-preference/check821-steps.md`
  (new), `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: `TestLiveCheckScripts`, `TestLiveCheckCIShardCoversTheSuiteOnce`.
- Command: `go test ./scripts/ -run 'TestLiveCheckScripts|TestLiveCheckCIShardCoversTheSuiteOnce' -count=1`.
- Red step: the scripts' census test finds no entry for 820.
- Depends on: LP14 (kept, behaviour: the scripts run the shipped commands).
- Commit: `checks: 820 and 821, the location preference's live checks (LP13)`.
- Reviewer tier: standard.
- Owner-run: `sh scripts/live-checks/check-820.sh <a scratch GCP project>` and
  `sh scripts/live-checks/check-821.sh`. No agent runs either.

### LP15: the key service finds an installation whose database is regional
- Acceptance: the vendor's key service decides whether an installation exists by reading the
  project's default database at each of the three hosts a default database can have, the legacy
  one first, with the same refusal-is-an-installation rule and the same bounds on each read
  (`productionDatabase`, `ee/keyservice/workspaces.go`, D2205). An installation whose database is
  in `europe-west1` is found; one that exists nowhere is still not found. What each host answers
  for a database that lives at another is not known (U8); an answer the service does not
  recognise stays "unchecked, never bound", as today.
- Files: `ee/keyservice/workspaces.go`, `ee/keyservice/workspaces_test.go`, and `docs/release.md`
  (one line in the owner's release steps: the key service is redeployed before a release that
  ships LP4). It is not a `### For operators` entry, which is a customer's and says only
  `fugaro update` (R8).
- Interfaces: none.
- Tests: `TestAnInstallationInEuropeIsFound`, `TestNoDatabaseAnywhereIsNoInstallation`,
  `TestTheLegacyHostIsStillReadFirst`.
- Command: `go test ./ee/keyservice/ -run 'AnInstallationInEurope|NoDatabaseAnywhere|TheLegacyHost' -count=1 && go test ./internal/eeboundary/ -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: `TestAnInstallationInEuropeIsFound` reads only the legacy host.
- Depends on: none.
- Commit: `keyservice: an installation is found at its database's regional host (LP15, F13)`.
- Reviewer tier: opus, mandatory independent review (`ee/`, licensing).

## Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository, read with
`fugaro settings planning.decoupling --repo dimipaun/fugaro --json`), and the repository's rigor is
`startup`, so an edge is broken wherever a shape is enough and the read reports a gain.

| Level | Tasks |
| --- | --- |
| 0 | LP0, LP1, LP5a, LP5b, LP8, LP9, LP15 |
| 1 | LP2, LP4, LP5c, LP7, LP12 |
| 2 | LP3, LP6 (both after always-a-budget's AB7) |
| 3 | LP10, LP14 |
| 4 | LP11, LP13 |

**Critical path:** five tasks, as `fugaro plan graph` reads it: LP1, LP2, LP3, LP10, LP11. LP8 and LP1, then LP2,
is the path AB7 waits on, and it starts at once.

**What must be sequential, and why.** LP2 and LP3 follow LP8, so a mistyped region is refused
before a region decides a permanent location. LP3 follows LP2 for one file, and LP4 follows LP8 for one. LP7 follows LP5b for the fake it tests
against, and LP6 follows LP7 for the logging fake. LP6 follows LP5a and
LP4 for shared files, and LP5c follows LP5a for its variable. LP10 follows LP3, LP4 and LP6 for
shared files. LP14 follows everything it joins. LP11 and LP13 describe and exercise what ships.

**Across plans.** Always-a-budget's AB7 writes `internal/cli/init_firebase.go`, `init.go`,
`init_new_machine.go`, `init_stages.go`, `setup.go`, `init_images.go`, `init_review.go`,
`setup_refusals_test.go`, `internal/infra/firebase.go` and `website/content/cli-reference.md`.
The graph cannot carry an edge to another plan, so the order is a rule for whoever launches: AB7
is launched once LP2, LP4 and LP8 have merged; LP3, LP6, LP10 and LP14 are launched once AB7 has
merged. No run of this plan is live on one of those files while AB7's is.

### Shared files

| File | Writers, by level |
| --- | --- |
| `internal/cli/init_firebase.go` | LP2 (1), then AB7, LP3 (2), LP10 (3) |
| `internal/cli/init.go` | LP8 (0), LP4 (1), then AB7, LP6 (2), LP10 (3) |
| `internal/cli/setup.go`, `init_review.go` | AB7, then LP10 (3) |
| `internal/cli/init_stages.go` | AB7, then LP14 (3) |
| `internal/cli/init_new_machine.go` | LP8 (0), then AB7 |
| `internal/infra/firebase.go` | LP2 (1), then AB7 |
| `website/content/cli-reference.md` | LP4 (1), then AB7, LP10 (3) |
| `internal/cli/testdata/region_prompts.golden`, `init_inputs_test.go` | LP4 (1), LP10 (3) |
| `init_confirm_test.go`, `init_review_test.go`, `init_converge_test.go`, `update_test.go`, `init_firestore_test.go`, `managed_money_steps_test.go` | LP3 (2), LP10 (3) |
| `setup_refusals_test.go` | AB7, then LP10 (3) |
| `roots/installation/tests/installation.tftest.hcl`, `installation*.plan.json` | LP5a (0), LP6 (2), LP14 (3, new files only) |
| `internal/infra/tfvars.go`, `internal/infra/discover.go`, `modules/installation/variables.tf`, the installation root's `variables.tf` and `main.tf`, the installation tfvars fixtures | LP6 (2) only |
| `firebase*.plan.json`, the Firebase tfvars fixtures | LP2 (1), LP14 (3, new files only) |
| `scripts/gen-golden-plans.sh` | LP14 (3) only |
| `internal/gcpfake/secrets.go` | LP5b (0) only |
| `internal/gcpfake/logging.go` | LP7 (1), LP6 (2) |
| `internal/gcpfake/gcs.go`, `update_managed_test.go` | LP8 (0) only |
| `internal/infra/tf/cover.go` | LP5a (0) only |
| `internal/cli/restore.go` | LP10 (3) only |
| `internal/gcpfake/firestore.go` | LP3 (2) only |
| `internal/backend/gcp/names.go` | LP1 (0) only |
| `internal/localcfg/localcfg.go`, `locations.go` | LP0 (0) only |
| `internal/infra/locations_observe.go` | LP7 (1) only |
| `internal/cli/doctor.go` | LP12 (1) only; `doctor_batch.go` is LP6's (2) |
| `docs/release.md` | LP15 (0) only |
| `internal/infra/forget.go`, `internal/e2e/update_test.go` | LP6 (2) only |
| `docs/gcp-live-checklist.md` | LP13 (4) only |

No level holds two writers of one file.

## The dependencies the plan broke and kept

**Broken: none.** The pass was run over every edge, and one break was tried and undone.

- **Tried and undone: the record as a contract.** The first draft had the readers of the record
  wait on a contract for its shape, with a stub recorder and an integration task.
  `fugaro plan graph` read it as `no gain`: before 4 tasks, weight 8; after 5 tasks, weight 10.
  Review round 1 then showed the design under it was wrong (the record must not be an input), so
  the edges it broke no longer exist.

**Kept, each with its reason:**

- LP2, LP3, LP4, LP6 and LP12 on LP1: each needs the mapping's answers. The mapping is one
  small pure file at level 0; a contract for it would be nearly the task itself.
- LP2 and LP3 on LP8: order, for safety. LP4 on LP8: one file.
- LP7 on LP5b: the fake's behaviour. LP6 on LP7: one file, the logging fake.
- LP3 on LP2; LP6 on LP5a and LP4; LP10 on LP3, LP4 and LP6: shared files.
- LP5c on LP5a: the variable it adds. The variable's name could be a contract, but LP5a is at
  level 0 and LP5c is small, so breaking the edge shortens nothing.
- LP12 on LP0: the record's type, which is the whole of LP0.
- LP14 on its eleven sides; LP11 on LP14, LP10 and LP15; LP13 on LP14: behaviour.
- LP3, LP6, LP10 and LP14 on always-a-budget's AB7: shared files and AB7's test.

**The critical path** is five tasks: LP1, LP2, LP3, LP10, LP11. No contract change is anticipated, because there is
no contract.

## The estimate

`fugaro plan graph --plan docs/location-preference/implementation-plan.md` printed, on 2026-11-20:

`ready in 270–1080 min, no history yet for 18 tasks (18 tasks; seeds for 18; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's
history predict, and it moves as runs finish and as the graph changes.

## Runs proposed

Eighteen runs: LP0 to LP4, LP5a, LP5b, LP5c and LP6 to LP15. None starts without the owner's go. The worst case is eighteen times
the per-run cap the gateway enforces (`agent.max_budget_usd`; the settings read shows 25 in one
layer and 500 in another, so $450 if the lower one is the repository's). Eleven of the eighteen ask
for an independent review job besides.

## Checks the plan takes

From the block **820–829**.

| Check | What it measures | Who |
| --- | --- | --- |
| **820** | A first `fugaro setup --location eu` in a scratch project: every located resource reads back in the EU (both databases, the buckets, the registry, a secret's replication, the log bucket); a run launches, leases budget and writes its history. Settles U1, U2 and U6 | owner-run, after the release |
| **821** | `fugaro update` on the dogfood installation: the record reads `us-east5`, `us-central1`, `automatic`, `global`; no root plans a change; nothing is asked; a run launched before the update finishes | owner-run, after the release |

## After-deploy steps

For an installation, none beyond `fugaro update`. The release notes' `### For operators` entry,
written in LP11's pull request: run `fugaro update`; it records where the installation's data
already is and moves nothing.

One step is the vendor's own and not an installation's: **the key service is redeployed** with
LP15 before the release that ships LP4, or an EU installation reads to it as no installation. It
is a line in `docs/release.md`, the owner's own release steps, written in LP15's pull request,
and nothing but that line gates the release on it (O9).

## Considered and declined

- **A contract for the mapping, and one for the record.** LP1 is smaller than its contract would
  be, and the record's contract measured no gain (above).
- **The record as an input to Terraform, and the record published in the shared config.** Both
  were in the first draft. The first breaks an existing installation between two merges and lets
  a wrong record plan a replacement; the second stops a teammate's older CLI (review round 1).
- **An installation-wide replication policy inferred from what exists.** In the draft review
  round 2 read. It was one-way (one automatic container made every later one automatic), had
  three separate evaluators and an import cycle. Declined for LP5a's rule: what exists is left
  alone, what is new is regional.
- **A place picker in the console's wizard.** It sends `region`, which stays valid and decides
  the place (D3080; `console/src/shared/new-ops.ts`). A picker there is the console's own work.
- **Refusing `--region` on every command.** It is a tested per-invocation override on read
  commands; the refusal is where a plan would follow it (D3088, amended).
- **The console's stub text and Check 27's banner prose** that still describe the old location
  note (`console/src/renderer/stubs/bridge-stub.ts`, `scripts/live-checks/check27.sh`). No test
  pins them; LP3 already edits Check 27's script and corrects its banner, and the console's stub
  is the console's.
- **A stated fallback for Vertex.** A region with no Claude endpoint is outside the promise
  (D3083); the model's own refusal says so.
- **Multi-region Firestore and Artifact Registry.** Declined by D3085: a higher price for a
  history database that is not on a run's path.
- **Moving an existing installation.** Out of the spec's scope.
- **Pinning model calls.** Declined by D3083.
