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
config (D3087); a changed region is refused at once (D3088); an installation made before this work
gets a missing database in its derived place (D3089); the location's own note is removed
everywhere (D3090); a database found elsewhere is adopted, recorded and reported (D3091).

**What always-a-budget's AB7 waits on is LP2** (D3029, R9). LP2 needs only LP1, and both are small.

## Conventions every part follows

`CLAUDE.md` and, for LP2, LP5a, LP6 and LP14, `.claude/rules/terraform.md`; for LP11,
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
| The record is read from the cloud before the first plan of an update, so an existing installation's secrets never plan a new replication | LP14 (`TestTheRecordIsReadBeforeTheFirstPlan`) |
| A run in flight is untouched: the record is a config field no run reads, and nothing a run holds is replaced | LP14 (`TestUpdateLeavesARunInFlightRunning` still passes, unchanged) |
| No fallback leaves its place, the scheduler's included | LP1 (`TestNoFallbackLeavesItsPlace`) |
| A new EU installation plans every located resource in the EU | LP14 (`TestANewEUInstallationPlansEverythingInTheEU`, on the golden plans) |
| Setup with no terminal and no flag fails at once, naming the flags | LP4 (`TestSetupWithNoTerminalNamesTheLocationFlags`) |
| A Realtime Database that exists is never given the mapping's region | LP2 (`TestAnExistingInstanceKeepsItsOwnRegion`) |
| `--location` and `--region` cannot disagree | LP4 (`TestALocationAndARegionThatDisagreeAreRefused`) |

## Slices and tasks

**Slice 1, the table and the budget database (useful alone: it is what AB7 waits on):** LP1, LP2.
**Slice 2, the record:** LP0, LP7, LP8. **Slice 3, the other located things:** LP3, LP5a, LP5b,
LP6. **Slice 4, what a person sees:** LP4, LP9, LP10, LP12. **Slice 5, joined and proven:** LP14.
**Documents and checks:** LP11, LP13.

### LP1: the mapping
- Acceptance: `internal/place` answers, with no cloud call, for any region: its place (US, EU or
  none, with London and Zürich in none); for a place, its default region (`us-east5`,
  `europe-west1`, D3086); and for a region, where a new installation's Firestore database,
  Realtime Database, secrets and run log bucket go, each with the reason when it is not the region
  itself (the table in the reconciliation). The lists it carries are dated against Google's pages.
- Files: `internal/place/place.go` (new), `internal/place/place_test.go` (new),
  `docs/location-preference/locations.md` (new: the lists and the date they were read).
- Interfaces: produces `place.Of`, `place.DefaultRegion`, `place.For`. Consumes
  `gcp.SchedulerRegion` in its test only.
- Tests: `TestThePlaceOfARegion`, `TestTheMappingForUSAndEU`, `TestNoFallbackLeavesItsPlace`,
  `TestARegionInNeitherPlaceStatesItsFallbacks`, `TestTheListsAreDated`.
- Command: `go test ./internal/place/ -count=1`.
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
  `internal/infra/discover_firebase.go`, and the `firebase*.plan.json` golden plans (generated).
  Past five for the root, its test and the generated plans.
- Interfaces: consumes `place.For` (LP1). Produces `FirebaseSpec`'s database region.
- Tests: `TestAnExistingInstanceKeepsItsOwnRegion`, `TestANewInstanceGoesWhereTheMappingSays`,
  `TestAnInstanceElsewhereIsAdopted`, and the root's `terraform test`.
- Command: `go test ./internal/infra/ -run 'AnExistingInstanceKeeps|ANewInstanceGoes|AnInstanceElsewhere|Firebase' -count=1 && go test -tags terraform ./internal/infra/... -run 'Firebase|Golden' -count=1 && (cd deploy/terraform/gcp/roots/firebase && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test)`.
- Red step: `TestAnInstanceElsewhereIsAdopted` meets today's refusal of a default instance outside
  `firebaseDBRegion`.
- Depends on: LP1 (kept, behaviour: it needs the mapping's answer, and the mapping is smaller than
  a contract for it would be).
- Commit: `firebase: the budget database's region is the mapping's, and an existing one keeps its own (LP2, D3029, D3089, D3091)`.
- Reviewer tier: opus, mandatory independent review (Terraform, a data-bearing type).

### LP0: the locations record
- Acceptance: the project config carries an optional `locations` block: the Firestore location,
  the Realtime Database location, the secrets' replication (`automatic` or a list of regions) and
  the log bucket's location. It validates strictly, is carried to a teammate by the shared config
  as the region is, and an absent key means "not known yet". A short document says what a writer
  and a reader may rely on (R5).
- Files: `internal/localcfg/locations.go` (new), `internal/localcfg/localcfg.go` (the one field
  and its validation call), `internal/localcfg/shared.go`, `internal/settings/registry.go`,
  `docs/location-preference/record.md` (new).
- Interfaces: produces `localcfg.Locations` and `Config.Locations`.
- Tests: `TestLocationsValidate`, `TestLocationsAreSharedLikeTheRegion`,
  `TestAConfigWithoutLocationsStillLoads`.
- Command: `go test ./internal/localcfg/ -run 'Locations|AConfigWithoutLocations' -count=1 && go test ./internal/settings/ -count=1`.
- Red step: `TestLocationsValidate` fails on an unknown field `locations`.
- Depends on: none.
- Commit: `localcfg: the locations record (LP0, D3087)`.
- Reviewer tier: opus (LP5a places secrets by it, so it is reviewed at that task's rigor).

### LP3: Firestore goes where the mapping says, and an existing one is adopted
- Acceptance: `FirestoreStep` creates the database in the location it is given, which is
  `place.For`'s answer for the installation's region (D3085, D3089); a native-mode database that
  exists is adopted wherever it is, if empty or ours, and a line says where it is when that is
  outside the place (R3, D3091); a database that is not native mode is still refused.
  `infra.FirestoreLocation` is gone. The location's note is still printed, with the location the
  step holds (LP10 removes it).
- Files: `internal/infra/firestore.go`, `internal/infra/firestore_test.go`,
  `internal/cli/init_firebase.go` (the step's construction and the three texts that name the
  constant).
- Interfaces: consumes `place.For` (LP1).
- Tests: `TestFirestoreIsCreatedWhereTheMappingSays`, `TestAFirestoreDatabaseElsewhereIsAdopted`,
  `TestADatastoreModeDatabaseIsStillRefused`.
- Command: `go test ./internal/infra/ -run 'Firestore' -count=1 && go test ./internal/cli/ -run 'Firestore|Firebase' -count=1`.
- Red step: `TestAFirestoreDatabaseElsewhereIsAdopted` meets `checkExisting`'s refusal.
- Depends on: LP1 (kept, behaviour).
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
  `website/content/cli-reference.md`, `plugin/skills/setup/SKILL.md`. Past five for the flag's
  censuses.
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
- Acceptance: the six Terraform secret resources take their replication from one input: the
  record's (LP0) where it has one, else `place.For`'s. An installation whose record says
  `automatic` plans no change to any secret; a new one plans user-managed replication in its
  region (D3082, F9).
- Files: `deploy/terraform/gcp/modules/installation/secrets.tf`, `licence.tf`, `orchestrator.tf`,
  `credentials.tf`, `variables.tf` (all in `modules/installation`),
  `deploy/terraform/gcp/modules/repo/secrets.tf`, `deploy/terraform/gcp/modules/repo/variables.tf`,
  the two roots' `variables.tf` and `main.tf`, `internal/infra/tfvars.go`, and the generated
  `installation*.plan.json` and `repo*.plan.json`. Past five: one input through two modules.
- Interfaces: consumes `localcfg.Locations` (LP0), `place.For` (LP1).
- Tests: `TestAnAutomaticInstallationPlansNoSecretChange`,
  `TestANewInstallationsSecretsAreInItsRegion`, and `terraform test` in the `installation` and
  `repo` roots.
- Command: `go test ./internal/infra/ -run 'AnAutomaticInstallation|ANewInstallationsSecrets' -count=1 && go test -tags terraform ./internal/infra/... -run 'Golden|Installation|Repo' -count=1 && for r in installation repo; do (cd deploy/terraform/gcp/roots/$r && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test); done && sh deploy/terraform/scan.sh`.
- Red step: `TestANewInstallationsSecretsAreInItsRegion` finds `auto {}`.
- Depends on: LP0 (the record's shape), LP1 (kept, behaviour).
- Commit: `secrets: a new installation's secrets are replicated in its region (LP5a, D3082)`.
- Reviewer tier: opus, mandatory independent review (secrets, Terraform).

### LP5b: secrets created outside Terraform follow the same policy
- Acceptance: the two Go creators of a secret take the installation's replication from the record,
  else the mapping, and the fake accepts and checks a user-managed policy.
- Files: `internal/backend/gcp/secrets.go`, `internal/intake/gcp.go`,
  `internal/gcpfake/secrets.go`, and their tests.
- Interfaces: consumes `localcfg.Locations` (LP0), `place.For` (LP1).
- Tests: `TestASecretCreatedByTheCLIFollowsThePolicy`, `TestAnIntakeSecretFollowsThePolicy`.
- Command: `go test ./internal/backend/gcp/ ./internal/intake/ ./internal/gcpfake/ -run 'Secret' -count=1`.
- Red step: the fake refuses a request without `automatic`.
- Depends on: LP0, LP1 (kept, behaviour).
- Commit: `secrets: the CLI's own secrets follow the installation's replication (LP5b, D3082)`.
- Reviewer tier: opus, mandatory independent review (secrets).

### LP6: a new installation's run log bucket is in its region
- Acceptance: the log bucket's location is an input: the record's where it has one, else
  `place.For`'s. Discovery reads the bucket where the record says it is, the import address is
  built from that location, and so is the log view's path. An installation whose bucket is `global`
  plans no change (F10).
- Files: `deploy/terraform/gcp/modules/installation/logging.tf`,
  `deploy/terraform/gcp/modules/installation/variables.tf`, `internal/infra/discover.go`,
  `internal/infra/imports.go`, `internal/infra/tfvars.go`, and the generated
  `installation*.plan.json`.
- Interfaces: consumes `localcfg.Locations` (LP0), `place.For` (LP1).
- Tests: `TestAGlobalLogBucketPlansNoChange`, `TestANewLogBucketIsInTheRegion`,
  `TestTheLogBucketIsImportedWhereItIs`.
- Command: `go test ./internal/infra/ -run 'LogBucket' -count=1 && go test -tags terraform ./internal/infra/... -run 'Golden|Installation' -count=1 && (cd deploy/terraform/gcp/roots/installation && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test)`.
- Red step: `TestANewLogBucketIsInTheRegion` finds `global`.
- Depends on: LP0; LP5a (kept: two files, `modules/installation/variables.tf` and
  `internal/infra/tfvars.go`, and the same golden plans).
- Commit: `logging: a new installation's run log bucket is in its region (LP6, D3082)`.
- Reviewer tier: opus, mandatory independent review (Terraform, a data-bearing type).

### LP7: the recorder
- Acceptance: one function reads what the cloud holds (the Firestore database's location, the
  default Realtime Database instance's, the Fugaro log bucket's, whether any Fugaro secret exists
  and how it is replicated, the runs bucket's) and returns the record: a key for each thing that
  exists, a recorded key corrected where the cloud says otherwise, and nothing for what does not
  exist (R5, D3087). It writes nothing but the config, asks nothing, and with a permission missing
  it leaves that key as it was and says so in one line.
- Files: `internal/infra/locations_record.go` (new), `internal/infra/locations_record_test.go`
  (new), and a fake it needs in a new file under `internal/gcpfake` (never
  `internal/gcpfake/secrets.go`, which is LP5b's).
- Interfaces: consumes `localcfg.Locations` (LP0). Produces `infra.RecordLocations`.
- Tests: `TestTheRecordOfAnInstallationFromBeforeThisWork`, `TestTheRecordIsCorrectedFromTheCloud`,
  `TestNothingIsRecordedForWhatDoesNotExist`, `TestAnUnreadableThingLeavesItsKey`.
- Command: `go test ./internal/infra/ -run 'TheRecord|NothingIsRecorded|AnUnreadableThing' -count=1`.
- Red step: the recorder does not exist.
- Depends on: LP0 (kept, behaviour: it fills the record's type).
- Commit: `infra: the locations record is read from what the cloud holds (LP7, D3087, R5)`.
- Reviewer tier: standard.

### LP8: a region that is not the installation's is refused
- Acceptance: a `--region` that differs from the project config's region, or from the runs
  bucket's own location on a machine with no config yet, is refused before anything is planned,
  with the installation's region and the reason it cannot move (D3088, R6, F4, F12).
- Files: `internal/cli/project.go`, `internal/cli/init_new_machine.go`,
  `internal/cli/region_refusal_test.go` (new).
- Interfaces: none new.
- Tests: `TestAChangedRegionIsRefused`, `TestANewMachinesRegionMustBeTheInstallations`,
  `TestTheSameRegionIsAccepted`.
- Command: `go test ./internal/cli/ -run 'AChangedRegion|ANewMachinesRegion|TheSameRegion|RefusalMatrix' -count=1`.
- Red step: `TestAChangedRegionIsRefused` sees the config rewritten.
- Depends on: none.
- Commit: `cli: a region that is not the installation's is refused (LP8, D3088)`.
- Reviewer tier: standard.

### LP9: a workspace member's database region comes from the member
- Acceptance: linking a member records its `database_region` from the member's own database URL or
  record, so a member in the EU is read at its own host with no flag; the flag still wins when
  given, and a member already linked is left as it is (F14, D3084).
- Files: `internal/cli/workspace.go`, `internal/cli/workspace_region_test.go` (new).
- Interfaces: consumes `localcfg.Locations` (LP0).
- Tests: `TestAMembersDatabaseRegionIsReadFromTheMember`, `TestTheFlagStillWins`.
- Command: `go test ./internal/cli/ -run 'AMembersDatabaseRegion|TheFlagStillWins|Workspace' -count=1`.
- Red step: a member with a `europe-west1` database is linked with an empty region.
- Depends on: LP0.
- Commit: `workspace: a member's database region is read from the member (LP9, D3084)`.
- Reviewer tier: opus, mandatory independent review (the workspace's credential path reads it).

### LP10: no step confirms a location
- Acceptance: `confirmLocation` is gone. Where the Firestore database is created the line saying
  where, and that it is permanent, is printed and the database is created, in every session the
  stage itself may run in (D3090). `stepConfirmation`'s comment names two steps that are the
  person's alone, not three. AB7's test of the note's order asserts instead that no note is
  printed (O2). `docs/no-prompt-policy/reconciliation.md` records that D12 of 2026-10-20 no longer
  covers the location.
- Files: `internal/cli/init_firebase.go`, `internal/cli/init.go`,
  `internal/cli/init_firebase_always_test.go` (AB7's, if merged), the no-prompt lint's allow-list
  if it names the site, `docs/no-prompt-policy/reconciliation.md`.
- Interfaces: none.
- Tests: `TestNoStepConfirmsALocation`, `TestTheLocationLineIsPrintedBeforeTheDatabase`, and the
  no-prompt lint.
- Command: `go test ./internal/cli/ -run 'NoStepConfirmsALocation|TheLocationLine|NoPrompt|Docs' -count=1`.
- Red step: `TestNoStepConfirmsALocation` finds the note and a `NeedsYouError` with no terminal.
- Depends on: LP3 and LP4 (kept: one file each, `init_firebase.go` and `init.go`). **Not launched
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
- Depends on: LP0, LP1 (kept, behaviour).
- Commit: `doctor: where the installation's data is (LP12, D3091)`.
- Reviewer tier: standard.

### LP14: the record is written by setup and update, and the whole is proven
- Acceptance: `fugaro update` and `fugaro setup` call the recorder before the first plan and
  again after the last apply, and write the config. An installation at
  the previous version converges with `fugaro update` alone: its record reads `us-east5`,
  `us-central1`, `automatic` and `global`, no root plans a destroy or a replace, nothing is asked
  and a run in flight is untouched. A new EU installation plans every located resource in the EU.
- Files: `internal/cli/init_stages.go`, `internal/cli/locations_end_to_end_test.go`
  (new), and golden plans `installation-eu.plan.json` and `firebase-eu.plan.json` (new, generated,
  with their entries in `scripts/gen-golden-plans.sh`).
- Interfaces: consumes everything above.
- Tests: `TestAnInstallationFromBeforeTheRecordConverges`, `TestTheRecordIsReadBeforeTheFirstPlan`,
  `TestANewEUInstallationPlansEverythingInTheEU`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'AnInstallationFromBeforeTheRecord|TheRecordIsReadBefore|ANewEUInstallation|UpdateLeavesARunInFlight' -count=1 && go test -tags terraform ./internal/infra/... -count=1 && go test -timeout 40m ./internal/cli/ -count=1`.
- Red step: `TestAnInstallationFromBeforeTheRecordConverges` finds no record after the update.
- Depends on: LP0, LP2, LP3, LP4, LP5a, LP5b, LP6, LP7, LP9, LP12 (kept, behaviour: it runs
  them together). **It shares
  `internal/cli/init_stages.go` with always-a-budget's AB7** and is not launched while AB7's run
  is live.
- Commit: `update: an installation's locations are recorded from what it has (LP14, D3087, D3089)`.
- Reviewer tier: opus, mandatory independent review (the upgrade path).

### LP11: the documents say where the data lives
- Acceptance: `SECURITY.md` and `website/content/security.md` carry the same section: what
  choosing US or EU places where, the exceptions D3082 names, that model calls are outside it
  (D3083), what an installation made before this work has, and what a workspace across places
  means (D3084); `docs/gcp-setup.md` describes the question; `docs/design/m9-budget-and-dashboard.md`
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
- Depends on: LP14 and LP10 (kept, behaviour: it states what ships).
- Commit: `docs: where an installation's data lives (LP11, D3082, D3083, D3084)`.
- Reviewer tier: opus, mandatory independent review (`SECURITY.md`).

### LP13: the two live checks, as scripts
- Acceptance: one script per check, each one command for the owner, with its steps file and its
  row in `docs/gcp-live-checklist.md`.
- Files: `scripts/live-checks/check-820.sh` (new), `scripts/live-checks/check-821.sh` (new),
  `docs/location-preference/check820-steps.md` (new), `docs/location-preference/check821-steps.md`
  (new), `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: `go test ./scripts/ -run 'LiveChecks' -count=1`.
- Command: the same.
- Red step: the scripts' census test finds no entry for 820.
- Depends on: LP14 (kept, behaviour: the scripts run the shipped commands).
- Commit: `checks: 820 and 821, the location preference's live checks (LP13)`.
- Reviewer tier: standard.
- Owner-run: `sh scripts/live-checks/check-820.sh <a scratch GCP project>` and
  `sh scripts/live-checks/check-821.sh`. No agent runs either.

## Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository, read with
`fugaro settings planning.decoupling --repo dimipaun/fugaro --json`), and the repository's rigor is
`startup`, so an edge is broken wherever a shape is enough and the read reports a gain.

| Level | Tasks |
| --- | --- |
| 0 | LP0, LP1, LP8 |
| 1 | LP2, LP3, LP4, LP5a, LP5b, LP7, LP9, LP12 |
| 2 | LP6, LP10 (after always-a-budget's AB7) |
| 3 | LP14 |
| 4 | LP11, LP13 |

**Critical path:** LP0, LP5a, LP6, LP14, LP11: five tasks. LP1, LP2 is the two-task path AB7 waits
on, and it starts at once.

**What must be sequential, and why.** LP6 follows LP5a for two files and the same golden plans.
LP10 follows LP3 and LP4 for one file each. LP14 follows everything it joins. LP11 and LP13 describe
and exercise what ships.

### Shared files

| File | Writers, by level |
| --- | --- |
| `internal/cli/init_firebase.go` | LP3 (1), LP10 (2); always-a-budget's AB7 between them |
| `internal/cli/init.go` | LP4 (1), LP10 (2) |
| `internal/cli/init_stages.go` | LP14 (3); always-a-budget's AB7 before it |
| `internal/cli/init_new_machine.go` | LP8 (0); always-a-budget's AB7 after it |
| `internal/infra/firebase.go` | LP2 (1); always-a-budget's AB7 after it |
| `internal/infra/tfvars.go`, `modules/installation/variables.tf` | LP5a (1), LP6 (2) |
| `installation*.plan.json` | LP5a (1), LP6 (2), LP14 (3, a new file) |
| `firebase*.plan.json` | LP2 (1), LP14 (3, a new file) |
| `internal/gcpfake/secrets.go` | LP5b (1) only |
| `website/content/cli-reference.md` | LP4 (1) only |
| `internal/localcfg/localcfg.go`, `shared.go`, `locations.go` | LP0 (0) only |
| `internal/infra/locations_record.go` | LP7 (1) only |

No level holds two writers of one file.

## The dependencies the plan broke and kept

**Broken: none.** The pass was run over every edge, and one break was tried and undone.

- **Tried and undone: the record as a contract.** The five readers of the record (LP5a, LP5b, LP6,
  LP9, LP12) need its shape and not the recorder's code, so the first draft put the shape in a
  contract with a stub recorder and an integration task. `fugaro plan graph` read it as `no gain`:
  before 4 tasks, weight 8; after 5 tasks, weight 10. The reason is that the shape is a task with
  no dependency of its own, so it already sits at level 0 and the readers already start at level 1.
  The plan keeps the shape as its own small task, LP0, with no stub, and the recorder (LP7) beside
  the readers.

**Kept, each with its reason:**

- LP2, LP3, LP4, LP5a, LP5b and LP12 on LP1: each needs the mapping's answers. The mapping is one
  small pure file; a contract for it would be nearly the task itself, and LP1 is already at
  level 0.
- LP5a, LP5b, LP6, LP7, LP9 and LP12 on LP0: the record's type. It is the interface, and it is
  the whole of LP0.
- LP6 on LP5a: shared files.
- LP10 on LP3 and LP4: shared files. LP10 on always-a-budget's AB7: a shared file and AB7's test.
- LP14 on its ten sides; LP11 on LP14 and LP10; LP13 on LP14: behaviour.

**The critical path** is five tasks, LP0, LP5a, LP6, LP14, LP11, with and without the break. No
contract change is anticipated, because there is no contract.

## The estimate

`fugaro plan graph --plan docs/location-preference/implementation-plan.md` printed, on 2026-11-20:

`ready in 270–1080 min, no history yet for 16 tasks (16 tasks; seeds for 16; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's
history predict, and it moves as runs finish and as the graph changes.

## Runs proposed

Sixteen runs: LP0 to LP4, LP5a, LP5b and LP6 to LP14. None starts without the owner's go. The worst case is sixteen times
the per-run cap the gateway enforces (`agent.max_budget_usd`; the settings read shows 25 in one
layer and 500 in another, so $400 if the lower one is the repository's). Ten of the sixteen ask
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
- **Multi-region Firestore and Artifact Registry.** Declined by D3085: a higher price for a
  history database that is not on a run's path.
- **Moving an existing installation.** Out of the spec's scope.
- **Pinning model calls.** Declined by D3083.
