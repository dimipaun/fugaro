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

**What always-a-budget's AB7 waits on is LP2** (D3029, R9). LP2 needs only LP1 and LP8, and all
three are small. Because AB7 writes files LP3, LP4 and LP8 also write, the plan asks that AB7 be
launched once LP2, LP3, LP4 and LP8 have merged (O3).

**Review round 1 changed the design in one place.** What Terraform and the CLI are told about a
thing that exists is read from the cloud, for that thing, every time. The record is written from
that reading and is never read back as an input (R5). The first draft fed the record to Terraform,
which would have planned a replacement of every existing secret between two merges.

## Conventions every part follows

`CLAUDE.md` and, for LP2, LP5a, LP6 and LP14, `.claude/rules/terraform.md`; for LP15, `.claude/rules/ee.md`; for LP11,
`.claude/rules/credentials-service.md` and `.claude/rules/website.md`. They are not restated here.

- **Always:** targeted tests before a commit (`-run`, never a whole package unless the task says
  so); `terraform fmt -check -recursive deploy/terraform`; golden plans regenerated with
  `scripts/gen-golden-plans.sh` and never edited by hand; `TF_DATA_DIR` outside the tree; a new
  refusal prints its fix; a task's pull request names its task id and the `D#` it implements.
- **Ask first:** a new resource type or a new kind of principal in `internal/infra/tf/cover.go`
  (none is expected); a new dependency (none is expected); any change to `.github/workflows/`
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
| A thing that exists is never given the mapping's answer: each existing secret keeps its own replication, the log bucket and both databases their own location, on every path that plans, at every commit of `main` between this plan's merges | LP5a (`TestAnExistingSecretKeepsItsOwnReplication`), LP6 (`TestAGlobalLogBucketPlansNoChange`), LP2 (`TestAnExistingInstanceKeepsItsOwnRegion`) |
| The record is never an input: a config whose record says anything at all plans what a config with no record plans | LP14 (`TestTheRecordIsNeverAnInput`) |
| A mixed installation (automatic and user-managed secrets side by side) plans no change | LP5a (`TestAMixedInstallationPlansNoSecretChange`) |
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
  `gcp.SchedulerRegion` in its test only.
- Tests: `TestThePlaceOfARegion`, `TestTheMappingForUSAndEU`, `TestNoFallbackLeavesItsPlace`,
  `TestARegionInNeitherPlaceStatesItsFallbacks`, `TestTheListsAreDated`,
  `TestEveryRegionOfAPlaceHasASchedulerLocationInIt`.
- Command: `go test ./internal/place/ -count=1 && go test ./internal/backend/gcp/ -run 'SchedulerRegion' -count=1`.
- Red step: the package does not exist; `TestThePlaceOfARegion` fails to compile.
- Depends on: none.
- Commit: `place: the mapping from a region to where an installation's data goes (LP1, D3080, D3081, D3085, D3086)`.
- Reviewer tier: standard.

### LP2: a new budget database goes where the mapping says
- Acceptance: the Firebase module's Realtime Database region is an input. For an instance that
  exists it is that instance's own location, wherever that is, and the instance is adopted under
  the rules that already decide it is ours (R3, R4, D3091); for none it is `place.For`'s answer
  (D3089). An installation whose instance is in `us-central1` plans no change.
- Files: `deploy/terraform/gcp/modules/firebase/firebase.tf`,
  `deploy/terraform/gcp/modules/firebase/variables.tf`,
  `deploy/terraform/gcp/roots/firebase/variables.tf`, `deploy/terraform/gcp/roots/firebase/main.tf`,
  `deploy/terraform/gcp/roots/firebase/tests/firebase.tftest.hcl`, `internal/infra/firebase.go`,
  `internal/infra/discover_firebase.go`, the Firebase tfvars fixtures under
  `internal/infra/testdata` that `scripts/gen-golden-plans.sh` reads, and the
  `firebase*.plan.json` golden plans (generated). Past five for the root, its test and the
  generated plans.
- Interfaces: consumes `place.For` (LP1). Produces `FirebaseSpec`'s database region.
- Tests: `TestAnExistingInstanceKeepsItsOwnRegion`, `TestANewInstanceGoesWhereTheMappingSays`,
  `TestAnInstanceElsewhereIsAdopted`, and the root's `terraform test`.
- Command: `go test ./internal/infra/ -run 'AnExistingInstanceKeeps|ANewInstanceGoes|AnInstanceElsewhere|Firebase' -count=1 && go test -tags terraform ./internal/infra/... -run 'Firebase|Golden' -count=1 && (cd deploy/terraform/gcp/roots/firebase && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test)`.
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
- Command: `go test ./internal/localcfg/ -run 'Locations|AConfigWithoutLocations|AnOlderSharedConfig' -count=1 && go test ./internal/settings/ ./schemas/ -count=1 && go test ./internal/cli/ -run 'WritersLabels|Settings' -count=1`.
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
  step holds (LP10 removes it).
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
- Command: `go test ./internal/infra/ ./internal/gcpfake/ -run 'Firestore' -count=1 && go test ./internal/cli/ -run 'Firestore|Firebase|Confirm|Review|Converge|ManagedMoney|RefusalMatrix|TestUpdate' -count=1 && go test ./scripts/ -run 'TestLiveCheckScripts' -count=1`.
- Red step: `TestAFirestoreDatabaseElsewhereIsAdopted` meets `checkExisting`'s refusal.
- Depends on: LP1 (kept, behaviour); LP8 (kept, order, as LP2).
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
  `internal/cli/init_inputs_test.go`, `internal/cli/testdata/region_prompts.golden`. Past five
  for the flag's censuses.
- Interfaces: consumes `place.Of`, `place.DefaultRegion`, `place.For` (LP1).
- Tests: `TestSetupAsksThePlace`, `TestALocationAndARegionThatDisagreeAreRefused`,
  `TestSetupWithNoTerminalNamesTheLocationFlags`, `TestSetupPrintsWhereEachThingGoes`,
  `TestSetupAnswersSchema`.
- Command: `go test ./internal/cli/ -run 'SetupAsksThePlace|ALocationAndARegion|SetupWithNoTerminalNames|SetupPrintsWhere|SetupAnswers|CliReferenceNames' -count=1 && go test ./plugin/... -count=1 && go test ./ee/clidoc/ -run CliReferenceIsCurrent -count=1`.
- Red step: `TestSetupAsksThePlace` finds the `--region` question.
- Depends on: LP1 (kept, behaviour).
- Commit: `setup: the first question is the place, and --location answers it (LP4, D3080, D3081, D3086)`.
- Reviewer tier: opus, mandatory independent review (a plugin skill changes).

### LP5a: a new installation's secrets are replicated in its region
- Acceptance: each of the six Terraform secret resources takes its replication per container. A
  container that exists keeps its own, as `infra.ObserveLocations` (LP7) read it from the cloud
  for that container. A container not yet created takes the installation's policy: automatic if
  any Fugaro container that exists is automatic, else `place.For`'s, which is user-managed in the
  region (D3082, F9, R5). The record is not read. An installation whose containers are automatic
  plans no change to any of them, a mixed one plans no change, and a new one plans user-managed
  replication. The intake service is told the policy by an environment variable (LP5c reads it).
- Files: `deploy/terraform/gcp/modules/installation/secrets.tf`, `licence.tf`, `orchestrator.tf`,
  `credentials.tf`, `intake.tf`, `variables.tf` (all in `modules/installation`),
  `deploy/terraform/gcp/modules/repo/secrets.tf`, `deploy/terraform/gcp/modules/repo/variables.tf`,
  the two roots' `variables.tf` and `main.tf`, `internal/infra/tfvars.go`, the tfvars fixtures
  under `internal/infra/testdata`, and the generated `installation*.plan.json` and
  `repo*.plan.json`. Past five: one input through two modules.
- Interfaces: consumes `infra.ObserveLocations` (LP7), `place.For` (LP1). Produces the intake
  service's variable.
- Tests: `TestAnExistingSecretKeepsItsOwnReplication`, `TestAMixedInstallationPlansNoSecretChange`,
  `TestANewInstallationsSecretsAreInItsRegion`, `TestANewSecretOnAnAutomaticInstallationIsAutomatic`,
  and `terraform test` in the `installation` and `repo` roots.
- Command: `go test ./internal/infra/ -run 'AnExistingSecretKeeps|AMixedInstallation|ANewInstallationsSecrets|ANewSecretOnAnAutomatic' -count=1 && go test -tags terraform ./internal/infra/... -run 'Golden|Installation|Repo' -count=1 && for r in installation repo; do (cd deploy/terraform/gcp/roots/$r && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test); done && sh deploy/terraform/scan.sh`.
- Red step: `TestANewInstallationsSecretsAreInItsRegion` finds `auto {}`.
- Depends on: LP7 (kept, behaviour: it must read what exists before it plans, at its own merge
  and not at a later one), LP1 (kept, behaviour).
- Commit: `secrets: a new installation's secrets are replicated in its region, and an existing one keeps its own (LP5a, D3082)`.
- Reviewer tier: opus, mandatory independent review (secrets, Terraform).

### LP5b: a secret the CLI creates follows the installation's policy
- Acceptance: the CLI's creator of a secret container asks `infra.ObserveLocations` for the
  installation's policy (automatic if any Fugaro container that exists is automatic, else the
  mapping's) and creates with it; where the policy cannot be read it refuses with the permission
  it needs and creates nothing, so no container is made under a guess. The fake accepts and
  checks a user-managed policy.
- Files: `internal/backend/gcp/secrets.go`, `internal/backend/gcp/secrets_test.go`,
  `internal/gcpfake/secrets.go`.
- Interfaces: consumes `infra.ObserveLocations` (LP7), `place.For` (LP1).
- Tests: `TestASecretCreatedByTheCLIFollowsThePolicy`, `TestAnUnreadablePolicyCreatesNothing`.
- Command: `go test ./internal/backend/gcp/ ./internal/gcpfake/ -run 'Secret|UnreadablePolicy' -count=1`.
- Red step: the fake refuses a request without `automatic`.
- Depends on: LP7, LP1 (kept, behaviour).
- Commit: `secrets: the CLI's own secrets follow the installation's replication (LP5b, D3082)`.
- Reviewer tier: opus, mandatory independent review (secrets).

### LP5c: the intake service's secrets follow the installation's policy
- Acceptance: the deployed intake service, which has no project config, creates a container with
  the policy its environment names, and its shape check accepts a container of that policy as
  well as an automatic one. With the variable absent (a service deployed before LP5a) it behaves
  as today.
- Files: `internal/intake/gcp.go`, `internal/intake/gcp_test.go`, `internal/cli/intake_serve.go`.
- Interfaces: consumes the variable LP5a's module sets.
- Tests: `TestAnIntakeSecretFollowsThePolicy`, `TestIntakeWithNoPolicyIsAutomatic`,
  `TestTheShapeCheckAcceptsTheInstallationsPolicy`.
- Command: `go test ./internal/intake/ -run 'Intake|ShapeCheck' -count=1 && go test ./internal/cli/ -run 'IntakeServe' -count=1`.
- Red step: `TestAnIntakeSecretFollowsThePolicy` finds an automatic container.
- Depends on: LP5a (kept, behaviour: the variable's name and the module that sets it).
- Commit: `intake: its secrets follow the installation's replication (LP5c, D3082)`.
- Reviewer tier: opus, mandatory independent review (secrets).

### LP6: a new installation's run log bucket is in its region
- Acceptance: the log bucket's location is an input. A bucket that exists keeps its own, as
  `infra.ObserveLocations` read it; for none it is `place.For`'s. Discovery, the import address,
  the log view's path and doctor's sink check are built from that location and stop assuming
  `global`. An installation whose bucket is `global` plans no change (F10).
- Files: `deploy/terraform/gcp/modules/installation/logging.tf`,
  `deploy/terraform/gcp/modules/installation/variables.tf`, the installation root's
  `variables.tf` and `main.tf`, `internal/infra/discover.go`, `internal/infra/imports.go`,
  `internal/infra/tfvars.go`, `internal/cli/doctor_batch.go` (`logBucketPath`), the tfvars
  fixtures, and the generated `installation*.plan.json`. Past five: one location through the
  module, its discovery and its one other reader.
- Interfaces: consumes `infra.ObserveLocations` (LP7), `place.For` (LP1).
- Tests: `TestAGlobalLogBucketPlansNoChange`, `TestANewLogBucketIsInTheRegion`,
  `TestTheLogBucketIsImportedWhereItIs`, `TestDoctorReadsTheLogBucketWhereItIs`.
- Command: `go test ./internal/infra/ -run 'LogBucket' -count=1 && go test ./internal/cli/ -run 'DoctorReadsTheLogBucket|DoctorBatch' -count=1 && go test -tags terraform ./internal/infra/... -run 'Golden|Installation' -count=1 && (cd deploy/terraform/gcp/roots/installation && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test)`.
- Red step: `TestANewLogBucketIsInTheRegion` finds `global`.
- Depends on: LP5a (kept: shared files, `modules/installation/variables.tf`, the root's two files,
  `internal/infra/tfvars.go` and the same golden plans; LP7 and LP1 come with it).
- Commit: `logging: a new installation's run log bucket is in its region (LP6, D3082)`.
- Reviewer tier: opus, mandatory independent review (Terraform, a data-bearing type).

### LP7: what the cloud holds
- Acceptance: `infra.ObserveLocations` reads, with no write and no question: the Firestore
  database's location; the default Realtime Database instance's; the Fugaro log bucket's; each
  Fugaro secret container's replication, by container; and the runs bucket's location. It returns
  its own plain type, with nothing for what does not exist and, for a thing it may not read, an
  explicit "unreadable" that names the permission, never a guess. It runs as part of discovery, so
  every path that plans has it.
- Files: `internal/infra/locations_observe.go` (new), `internal/infra/locations_observe_test.go`
  (new), and a fake it needs in a new file under `internal/gcpfake` (never
  `internal/gcpfake/secrets.go`, which is LP5b's).
- Interfaces: produces `infra.ObserveLocations` and `infra.Observed`.
- Tests: `TestWhatAnInstallationFromBeforeThisWorkHolds`, `TestEachSecretsReplicationIsReadByContainer`,
  `TestNothingIsReportedForWhatDoesNotExist`, `TestAnUnreadableThingIsNamedNotGuessed`.
- Command: `go test ./internal/infra/ -run 'WhatAnInstallationFromBefore|EachSecretsReplication|NothingIsReported|AnUnreadableThing' -count=1`.
- Red step: the function does not exist.
- Depends on: none.
- Commit: `infra: what the cloud holds, by location (LP7, R5)`.
- Reviewer tier: opus, mandatory independent review (it decides each secret's replication).

### LP8: a region that is not the installation's is refused
- Acceptance: a `--region` that differs from the project config's region, or from the runs
  bucket's own location on a machine with no config yet, is refused before anything is planned.
  The refusal names the installation's region, says it cannot move, and prints the fix: the same
  command with that region (D3088, R6, F4, F12). Where the bucket's location cannot be read (a
  launcher may lack `storage.buckets.get`), the typed region is accepted and one line says it was
  not checked (R10).
- Files: `internal/cli/project.go`, `internal/cli/init_new_machine.go`,
  `internal/cli/region_refusal_test.go` (new).
- Interfaces: none new.
- Tests: `TestAChangedRegionIsRefused`, `TestANewMachinesRegionMustBeTheInstallations`,
  `TestTheSameRegionIsAccepted`, `TestAnUnreadableBucketIsSaidNotRefused`.
- Command: `go test ./internal/cli/ -run 'AChangedRegion|ANewMachinesRegion|TheSameRegion|AnUnreadableBucket|RefusalMatrix' -count=1`.
- Red step: `TestAChangedRegionIsRefused` sees the config rewritten.
- Depends on: none.
- Commit: `cli: a region that is not the installation's is refused (LP8, D3088)`.
- Reviewer tier: standard.

### LP9: a workspace member's database region comes from the member
- Acceptance: linking a member records its `database_region` from the member's own database URL,
  so a member in the EU is read at its own host with no flag; the flag still wins when given, and
  a member already linked is left as it is (F14, D3084). The value becomes a host label, so it is
  held to the three Realtime Database locations and not only to
  `projectpolicy.ValidDatabaseRegion`'s characters; the service's check of the token's issuer
  still bounds what a wrong one reaches.
- Files: `internal/cli/workspace.go`, `internal/cli/workspace_region_test.go` (new).
- Interfaces: consumes the member's `budget.rtdb_url`.
- Tests: `TestAMembersDatabaseRegionIsReadFromTheMember`, `TestTheFlagStillWins`,
  `TestARegionThatIsNoDatabaseLocationIsRefused`.
- Command: `go test ./internal/cli/ -run 'AMembersDatabaseRegion|TheFlagStillWins|Workspace' -count=1`.
- Red step: a member with a `europe-west1` database is linked with an empty region.
- Depends on: none.
- Commit: `workspace: a member's database region is read from the member (LP9, D3084)`.
- Reviewer tier: opus, mandatory independent review (the workspace's credential path reads it).

### LP10: no step confirms a location
- Acceptance: `confirmLocation` is gone. Where the Firestore database is created the line saying
  where, and that it is permanent, is printed and the database is created, in every session the
  stage itself may run in (D3090). `stepConfirmation`'s comment names two steps that are the
  person's alone, not three. AB7's test of the note's order asserts instead that no note is
  printed (O2). `docs/no-prompt-policy/reconciliation.md` records that D12 of 2026-10-20 no longer
  covers the location.
- Files: `internal/cli/init_firebase.go`, `internal/cli/init.go`, the tests that pin the note
  (`init_confirm_test.go`, `init_converge_test.go`, `init_inputs_test.go`, `init_review_test.go`,
  `setup_refusals_test.go`, `update_test.go`, `testdata/region_prompts.golden`, all in
  `internal/cli`), AB7's `init_firebase_always_test.go` (it does not exist on `main` today; AB7
  adds it), the no-prompt lint's allow-list if it names the site, and
  `docs/no-prompt-policy/reconciliation.md`. Past five: one note pinned in seven tests.
- Interfaces: none.
- Tests: `TestNoStepConfirmsALocation`, `TestTheLocationLineIsPrintedBeforeTheDatabase`, and the
  no-prompt lint.
- Command: `go test ./internal/cli/ -run 'NoStepConfirmsALocation|TheLocationLine|NoPrompt|RefusalMatrix|Confirm|Converge|Review|SetupRefusals|TestUpdate|Docs' -count=1`.
- Red step: `TestNoStepConfirmsALocation` finds the note and a `NeedsYouError` with no terminal.
- Depends on: LP3 and LP4 (kept: shared files, `init_firebase.go`, `init.go` and the tests and
  golden file both of them write). **Not launched
  before always-a-budget's AB7 has merged** (F19, O2).
- Commit: `init: no step confirms a location (LP10, D3090)`.
- Reviewer tier: opus, mandatory independent review (a guard is removed).

### LP12: `fugaro doctor` says where the data is
- Acceptance: doctor prints one row with the installation's region, its place and where each
  recorded thing is; a thing outside the place is information, never a failure (D3091); an
  installation with no record yet is told to run `fugaro update`.
- Files: `internal/cli/doctor_locations.go` (new), `internal/cli/doctor_locations_test.go` (new),
  `internal/cli/doctor.go` (the registration).
- Interfaces: consumes `localcfg.Locations` (LP0), `place.Of` (LP1).
- Tests: `TestDoctorSaysWhereTheDataIs`, `TestADatabaseOutsideThePlaceIsInformation`,
  `TestDoctorWithNoRecordNamesUpdate`.
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
  (log isolation turned off, and the build and system logs Fugaro leaves in `_Default`); `docs/gcp-setup.md` describes the question; `docs/design/m9-budget-and-dashboard.md`
  D4 gets a note that it holds for installations made before this work; the release notes'
  `### For operators` says run `fugaro update` and that it moves nothing (R8). A docs test pins the
  section in both files to the mapping.
- Files: `SECURITY.md`, `website/content/security.md`, `docs/gcp-setup.md`,
  `docs/design/m9-budget-and-dashboard.md`, `internal/cli/docs_location_test.go` (new), and the
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
  row in `docs/gcp-live-checklist.md`.
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
  in `europe-west1` is found; one that exists nowhere is still not found.
- Files: `ee/keyservice/workspaces.go`, `ee/keyservice/workspaces_test.go`.
- Interfaces: none.
- Tests: `TestAnInstallationInEuropeIsFound`, `TestNoDatabaseAnywhereIsNoInstallation`,
  `TestTheLegacyHostIsStillReadFirst`.
- Command: `go test ./ee/keyservice/ -run 'AnInstallationInEurope|NoDatabaseAnywhere|TheLegacyHost' -count=1`.
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
| 0 | LP0, LP1, LP7, LP8, LP9, LP15 |
| 1 | LP2, LP3, LP4, LP5a, LP5b, LP12 |
| 2 | LP5c, LP6, LP10 (after always-a-budget's AB7) |
| 3 | LP14 |
| 4 | LP11, LP13 |

**Critical path:** five tasks, as `fugaro plan graph` reads it: LP1, LP5a, LP5c, LP14, LP11.
LP7, LP5a, LP6, LP14, LP11 is as long. LP8 and LP1, then LP2, is the path AB7
waits on, and it starts at once.

**What must be sequential, and why.** LP2 and LP3 follow LP8, so a mistyped region is refused
before a region decides a permanent location. LP5a, LP5b and LP6 follow LP7, because each must
read what exists at its own merge. LP6 and LP5c follow LP5a for shared files and its variable.
LP10 follows LP3 and LP4 for shared files. LP14 follows everything it joins. LP11 and LP13
describe and exercise what ships.

**Across plans.** Always-a-budget's AB7 writes `internal/cli/init_firebase.go`, `init.go`,
`init_new_machine.go`, `init_stages.go`, `internal/infra/firebase.go` and
`website/content/cli-reference.md`. The graph cannot carry an edge to another plan, so the order
is a rule for whoever launches: AB7 is launched once LP2, LP3, LP4 and LP8 have merged; LP10 and
LP14 are launched once AB7 has merged. No run of this plan is live on one of those files while
AB7's is.

### Shared files

| File | Writers, by level |
| --- | --- |
| `internal/cli/init_firebase.go` | LP3 (1), then AB7, then LP10 (2) |
| `internal/cli/init.go` | LP4 (1), then AB7, then LP10 (2) |
| `internal/cli/init_stages.go` | AB7, then LP14 (3) |
| `internal/cli/init_new_machine.go` | LP8 (0), then AB7 |
| `internal/infra/firebase.go` | LP2 (1), then AB7 |
| `website/content/cli-reference.md` | LP4 (1), then AB7 |
| `internal/cli/testdata/region_prompts.golden`, `init_inputs_test.go` | LP4 (1), LP10 (2) |
| `init_confirm_test.go`, `init_review_test.go`, `init_converge_test.go`, `update_test.go` | LP3 (1), LP10 (2) |
| `setup_refusals_test.go` | LP10 (2) only; AB7 before it |
| `internal/infra/tfvars.go`, `modules/installation/variables.tf`, the installation root's `variables.tf` and `main.tf` | LP5a (1), LP6 (2) |
| `installation*.plan.json`, the installation tfvars fixtures | LP5a (1), LP6 (2), LP14 (3, new files) |
| `firebase*.plan.json`, the Firebase tfvars fixtures | LP2 (1), LP14 (3, new files) |
| `scripts/gen-golden-plans.sh` | LP14 (3) only |
| `internal/gcpfake/secrets.go` | LP5b (1) only |
| `internal/gcpfake/firestore.go` | LP3 (1) only |
| `internal/backend/gcp/names.go` | LP1 (0) only |
| `internal/localcfg/localcfg.go`, `locations.go` | LP0 (0) only |
| `internal/infra/locations_observe.go` | LP7 (0) only |
| `internal/cli/doctor.go` | LP12 (1) only; `doctor_batch.go` is LP6's (2) |
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

- LP2, LP3, LP4, LP5a, LP5b and LP12 on LP1: each needs the mapping's answers. The mapping is one
  small pure file at level 0; a contract for it would be nearly the task itself.
- LP5a and LP5b on LP7: behaviour, not shape. Each must read what exists at its own merge, or
  `main` plans a replacement of every existing secret until a later task lands. A stub that
  returned "nothing exists" is exactly that failure.
- LP2 and LP3 on LP8: order, for safety.
- LP6 and LP5c on LP5a: shared files and the variable it adds.
- LP10 on LP3 and LP4: shared files. LP10 on always-a-budget's AB7: shared files and AB7's test.
- LP12 on LP0: the record's type, which is the whole of LP0.
- LP14 on its eleven sides; LP11 on LP14, LP10 and LP15; LP13 on LP14: behaviour.

**The critical path** is five tasks: LP1, LP5a, LP5c, LP14, LP11 (LP7 and LP6 tie with LP1 and
LP5c). No contract change is
anticipated, because there is no contract.

## The estimate

`fugaro plan graph --plan docs/location-preference/implementation-plan.md` printed, on 2026-11-20:

`ready in 300–1200 min, no history yet for 18 tasks (18 tasks; seeds for 18; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's
history predict, and it moves as runs finish and as the graph changes.

## Runs proposed

Eighteen runs: LP0 to LP4, LP5a, LP5b, LP5c and LP6 to LP15. None starts without the owner's go. The worst case is eighteen times
the per-run cap the gateway enforces (`agent.max_budget_usd`; the settings read shows 25 in one
layer and 500 in another, so $450 if the lower one is the repository's). Twelve of the eighteen ask
for an independent review job besides.

## Checks the plan takes

From the block **820–829**.

| Check | What it measures | Who |
| --- | --- | --- |
| **820** | A first `fugaro setup --location eu` in a scratch project: every located resource reads back in the EU (both databases, the buckets, the registry, a secret's replication, the log bucket); a run launches, leases budget and writes its history. Settles U1, U2 and U6 | owner-run, after the release |
| **821** | `fugaro update` on the dogfood installation: the record reads `us-east5`, `us-central1`, `automatic`, `global`; no root plans a change; nothing is asked; a run launched before the update finishes | owner-run, after the release |

## After-deploy steps

None beyond `fugaro update`. The release notes' `### For operators` entry, written in LP11's pull
request: run `fugaro update`; it records where the installation's data already is and moves
nothing.

## Considered and declined

- **A contract for the mapping, and one for the record.** LP1 is smaller than its contract would
  be, and the record's contract measured no gain (above).
- **The record as an input to Terraform, and the record published in the shared config.** Both
  were in the first draft. The first breaks an existing installation between two merges and lets
  a wrong record plan a replacement; the second stops a teammate's older CLI (review round 1).
- **A stated fallback for Vertex.** A region with no Claude endpoint is outside the promise
  (D3083); the model's own refusal says so.
- **Multi-region Firestore and Artifact Registry.** Declined by D3085: a higher price for a
  history database that is not on a run's path.
- **Moving an existing installation.** Out of the spec's scope.
- **Pinning model calls.** Declined by D3083.
