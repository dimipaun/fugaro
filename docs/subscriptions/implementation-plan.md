# Subscriptions: implementation plan

## Opening and status

This plan builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)**, which
wins where they differ. Base: `origin/main` at `d6f97594c`, 2026-10-27. That commit carries the
Subscription Terms amended to the owner's answers (#1146, licensing L4.5, done) and the Terms URL
in the four licence files (#1140, merged).

**Status: plan only. Nothing is implemented, and NO TASK IS APPROVED.** Each task needs the owner's
explicit go. Answering D430–D448 and B1–B17, and ruling T10–T26, O4 and the key binding, approved
none of them. Some tasks also wait on counsel (S8.4) or on the new entity (S8.1); each task says
which. **T27, the one consequence of the rulings, was ruled (b) on 2026-10-29:** the paid commands
warn, not refuse, until enforcement ships, and that warning is itself deferred with the key work
(ee-reorg D523), so nothing waits on it.

**Re-sliced 2026-10-27 after the owner's rulings** (reconciliation, "Rulings"). What changed, with
the earlier version kept visible:

- **S2.2 is withdrawn** (O4: no covered-versions date, no release-date stamping). Its number is not
  reused.
- **S1.4 now moves the coordinator's command and library** (T10), not only new parts.
- **S1.5 moves `fugaro report` and keeps `fugaro runs digest` MIT** with a small MIT copy of what it
  needs (T11).
- **S1.6 is the single mixed-licence release with the relabel** (T21).
- **New tasks:** S1.8 (re-plan the coordinator and orchestrator plans, close #831, T20), S1.9 (the
  coordinator job's key-gated image, T10), S1.10 (Check 178, owner-run), S2.10 (the owner's mint
  tool for interim hand-minted keys, T13) and S3.8 (the owner creates the dedicated project's
  signing keys, T13).
- **Release gate:** S0.7 releases console 0.113.0 and the first app release on counsel's sign-off,
  without key enforcement (T17). S6.2 and S6.3 no longer gate those releases.
- **S0.4 is the one Terms revision after counsel's feedback.** S0.5 is no longer conditional (T23,
  default).
- Check 178 measured the release date before; it now measures the coordinator's key-gated job.

**Updated 2026-10-29 (the ee-reorg rulings, D520–D536).** The ee-reorg plan
([`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md)) **supersedes** S1.1,
S1.2, S1.3, S1.4, S1.5, S1.7 and S1.9, carrying each task by task: S1.1 → E0.3, S1.2 → E1.1,
S1.3 → E1.5, S1.4 → E2.1, S1.5 → E2.2, S1.7 → E1.4, S1.9 → E1.4. Their text and numbers stay below
and are not reused. S1.6 is carried there as E3.2 and stays this plan's dependency; S1.8 stands;
**S1.10 (Check 178) is deferred until enforcement**. The base and history images will build with the
`ee` tag once the relabel has landed (E3.2, then E1.4; D524), which amends R1; no key is mounted
until enforcement.

The owner's answers and rulings that shape it (non-price; figures are private, D430):

- **D431** counsel's licence stays. **D447/T21** one key-gated binary with a mixed licence,
  relabelled (T21 is not the recommendation).
- **D432/T15** one free evaluation seat per organisation with no subscription, an individual being
  an organisation of one; open-source teams free.
- **D433/T14** a seat is anyone who launches jobs, as a pricing rule. **D439** an advisory estimate
  counting all launchers.
- **D434/T10** the coordinator's command and library move to `ee/` (not the recommendation); the
  released MIT 0.11.x–0.12.0 stays the fork point.
- **D444/T11** `fugaro report` moves and `fugaro runs digest` stays MIT (not the recommendation).
  **D435/T20** orchestrators and the MCP server are born in `ee/`; #831 closes as superseded.
- **D436/T12** no fallback. After the 30-day grace the console and the app read and cancel, and
  cannot launch or change config. The MIT CLI keeps launching and cancelling.
- **D438** one signed offline key per organisation with a seat count, expiry only (O4). The next key
  issues 30 days before expiry (T26).
- **D440/T13** the key service is owner-operated in a dedicated Fugaro GCP project (Cloud Run, a
  non-exportable KMS key, intake split from signing) and is built after the entity exists. Until
  then keys are minted by hand.
- **T16** the cloud keeps enforcing the launcher, operator and admin lists in MIT core; `ee/` adds
  the role model, its UX and the viewer.
- **T17** console 0.113.0 and the first app release ship under the business licence on counsel's
  sign-off, honour-based. Enforcement comes later.
- **T18** the copyright moves in one owner-go commit once the entity exists. **T22** the Team price
  is public on the site and Enterprise is "contact us"; the figures never enter the repository.
- **T24** the contract figures stay in the Terms. **T25** refunds are honour-based, an accepted
  risk (not the recommendation). **D442** Paddle for Team checkout.

## Conventions every part follows

- `CLAUDE.md`: targeted tests only (`-run`), gofmt and vet, no new dependency or framework without
  the owner's yes, comments say why, search before writing a helper, docs are tested
  (`GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`).
- `.claude/rules/ee.md` for every `ee/**` task:
  - the two-line SPDX notice on every file;
  - nothing in the core imports `ee/`, and `cmd/fugaro/ee_on.go` is the only route;
  - no private key material in any file (S0.6 admits public keys and signature vectors);
  - `go test ./internal/eeboundary/ -count=1` after each change.

  Never edit a boundary test to admit a route: that is the owner's call.
- `.claude/rules/console.md` for `console/**`. A new spawned command, IPC operation or child
  environment key is security-sensitive (exact argv in `src/main/spawn.ts`, `CHILD_ENV_KEYS`,
  `CONSOLE_OPERATIONS` in `src/main/ipc.ts`). New dependencies are pinned exactly.
- `.claude/rules/mobile.md` for `mobile/**`: `mobile/` is business-licensed and `shared/` is MIT.
  The mailbox client is the app's only write path. `npm test` is the only check. An Ed25519
  verifier needs a dependency, which the owner approves first.
- `.claude/rules/website.md` for `website/**`: run `npm run check`. The CSP is `script-src 'self'`
  plus the analytics host, and `form-action 'self'`. Privacy and legal copy change only with the
  owner's approval.
- `.claude/rules/terraform.md`: **`deploy/terraform` is embedded in the Go binary**, so the key
  service's infrastructure never lives there.
- No figure from the private business repository enters any file (D430, T22). S0.1 pins it.
- Every new file under `console/` and `mobile/` carries the business-licence notice. Every new file
  under `ee/` carries the SPDX notice, with a `<name>.license` sibling for JSON.
- No secret-shaped literal in tests, docs or fixtures.
- No live cloud action by an agent; every live check, deploy and key operation is OWNER-RUN. No
  agent holds or reaches a production signing key (R5).
- No agent edits `.github/workflows/`. A step after deploy goes into the release notes'
  `### For operators` section.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No price, currency amount, discount offer, fee rate or Enterprise minimum in the repository | S0.1 |
| A key verifier makes no network call | S2.3 (`TestLicencePackageImportsNoNetwork`), S7.3 (privacy rule 5 over `ee/`) |
| A leaked signing key cannot mint keys old releases accept for ever | S2.1, S2.3 (`TestKeyBeyondTheTierLifetimeIsRefused`) |
| The base and history images build `fugaro` with the `ee` tag; the credentials image does not; no key is mounted until enforcement (R1 as amended by D524) | E1.4 (ee-reorg) |
| The core never imports `ee/`; paid code reaches the binary only through `ee/register` | S1.2, S1.3 (existing boundary tests) |
| An untagged build still launches; the launch declares to the coordinator only when `ee/` registered a declarer | S1.2, S1.4 (`TestUntaggedBuildStillLaunches`) |
| `fugaro runs digest` works in an untagged build and inside a run | S1.5 (`TestDigestNeedsNoEE`) |
| After the grace period, cancel and the kill switch stay available in the console and the app; launch and config changes do not | S6.1, S6.2, S6.3, Checks 176–177 |
| Overlapping keys: the next key is valid 30 days before the current one expires, and the verifier takes the latest valid one | S2.1, S2.3 (`TestOverlappingKeysPickTheLatest`), S3.2, Check 175 |
| Seats are warned, never enforced (D439) | S4.2 |
| Only the non-public minter holds the signer role; the intake cannot sign | S3.5, Check 174 |
| Personal data at sign-up is the minimum, with retention stated | S3.1, S7.3 |

**Review focus for the independent reviewers:**

- **The key service, attacked:** mint a key for a domain you do not own; get a second free key for
  one organisation; reach the admin page or the minter; replay a verification email or a purchase
  event; forge open-source eligibility.
- **The verifier:** malleable encodings, key id confusion, expiry arithmetic across overlapping
  keys, and a clock set back.
- **The `ee/register` routes:** can an untagged build load paid code, and can a paid command shadow
  a core one?
- **The tagged job image:** whether the tagged build reaches anything it should not; no key is mounted until enforcement (D524).
- **The read-only state:** does it ever remove cancel or the kill switch (T12)?

## Slices and tasks

### Slice S0: the licence, Terms and release documents (docs only)

#### S0.1 A docs test that no price figure enters the repository

- **Files:** `internal/cli/docs_subscriptions_test.go` (new).
- **Interfaces:** reads `docs/subscriptions/*.md`, `docs/licensing/*.md` and the website's pages.
  **Its limit:** it does not scan commit messages or PR bodies; each PR's independent review covers
  those.
- **Tests:** `TestDocsSubscriptionsCarryNoPriceFigures` and
  `TestDocsSubscriptionsPriceScanCatchesPlants`.
  - The scan flags a currency sign or code beside a digit, and a digit followed by `%` or
    "percent".
  - It allows, by an exact list, the Terms' values already on `main`: the Affiliate control
    threshold and the audit threshold.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsSubscriptions' -count=1`.
- **Red step:** the plant test feeds a fixture line with a currency amount and fails until the scan
  exists.
- **Depends on:** none. **Reviewer:** standard.
- **Commit:** `docs(subscriptions): a docs test that no price figure enters the repository`.

#### S0.2 The counsel addendum

- **Files:** `docs/subscriptions/counsel-packet.md` (new), `docs/subscriptions/README.md`.
- **Content:** only what the amended Terms' counsel questions 12–18 do not already ask.
  - T12: cancel stays after the lapse.
  - T15: an individual is an organisation of one, and business-only does not exclude them.
  - T17: an honour-based first licensed release.
  - T21: the mixed-licence binary, and whether running its MIT commands needs a seat.
  - T24: the contract figures in the Terms.
  - T25: refund leakage, an accepted risk.
  - The per-organisation key (§8.1, §8.5).
  - Interim hand-minted keys (§8.3).
  - S0.4's item list.

  No figure.
- **Tests:** S0.1's scan; the Docs suite.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (a new document); S0.1's scan is its gate.
- **Depends on:** none. **Reviewer:** standard.
- **Commit:** `docs(subscriptions): the counsel addendum to the amended Terms`.

#### S0.3 The licence documents follow the rulings

- **Files:** `LICENSING.md`, `ee/README.md`, `.claude/rules/ee.md`,
  `internal/cli/docs_ee_reserved_test.go`.
- **Change:** D302's "nothing moves out" changes in all four files:
  - the coordinator's command and library move (T10), and `fugaro report` moves (T11);
  - `fugaro runs digest` stays MIT;
  - the released MIT versions stay MIT;
  - the orchestrators and the MCP server are born in `ee/` (T20).
- **Tests:** `TestDocsEELicensed` and the `docs_ee_reserved_test.go` pins, updated in both places.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsEE' -count=1 && go test
  ./internal/eeboundary/ -count=1`.
- **Red step:** change the pin first; it fails until the files say it.
- **Depends on:** S1.1 (the design it states), S0.6 (the same files). **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `licensing: the core-to-ee moves the owner ruled (D434, D444, T10, T11)`.

#### S0.4 The one Terms revision, after counsel's feedback

- **Files:** `docs/licensing/subscription-terms.md`.
- **Change:** counsel's mark-up plus the rulings, in one run. **The Terms are not revised before
  counsel answers — one exception, edited 2026-10-29 on the owner's ruling D531 (counsel not yet
  consulted: tell counsel in the next revision round).** The owner ruled the coordinator moves
  under `ee/` (the move is S1.4 and has not landed: `ee/` holds only its licence and README, so the
  coordinator is still Core by the Terms' own definition) and had section 9.1 of
  [licensing/subscription-terms.md](../licensing/subscription-terms.md) edited at once to stop
  calling the coordinator Core (the only sentence changed; the website page regenerated from it,
  [licensing/reconciliation.md](../licensing/reconciliation.md#amendment-2026-10-29-the-coordinator-leaves-the-terms-core)).
  Everything below still waits for counsel. Items:
  - **§10.4 and §17.4:** after the grace period the console and the app can still **cancel**, and
    the kill switch stays; they cannot launch or change config (T12).
  - **§8.1 and §8.5:** **one key per organisation**, with its seat count, replacing the per-User
    wording (key-binding ruling).
  - **§8.1 and §10.2:** drop every mention of the covered-versions date (O4).
  - **§4.3(a), §8.3 and §1.3:** the free seat and the public-OSI rule apply per legal person, and an
    individual is an organisation of one (T15).
  - **§3.3 note:** the coordinator's command and library and `fugaro report` move, the digest stays
    MIT, and one mixed-licence binary (T10, T11, T21).
  - **§8.3:**
    - the next key issues 30 days before expiry (T26);
    - keys are hand-minted until the key service exists (T13);
    - paid keys follow purchase, renewal and cancellation;
    - refund leakage is accepted (T25).
  - **Kept:** the contract figures stay, and prices, discounts, fees and the Enterprise minimum
    stay bracketed (T24).
- **Tests:** S0.1's scan; the Docs suite.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none; S0.1 is the gate.
- **Depends on:** S8.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `docs(licensing): the Subscription Terms revised per counsel and the owner's rulings`.

#### S0.5 Supported versions (T23, default)

- **Files:** `SECURITY.md`, `website/content/security.md` (generated by `go run ./internal/clidoc`),
  `website/test/security.mjs`.
- **Change:** "latest release only" becomes the Terms' "the current minor release and the one
  before it".
- **Tests:** `website/test/security.mjs` and the Docs suite.
- **Command:** `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1
  && cd website && npm run check`.
- **Red step:** the updated pin in `security.mjs` fails until `SECURITY.md` and its mirror say it.
- **Depends on:** none. **Reviewer:** opus + mandatory independent review (the credentials-service
  rule loads for `SECURITY.md`).
- **Commit:** `security: supported versions are the current and the previous minor (T23)`.

#### S0.6 `ee/` stops being empty

- **Files:** `ee/README.md`, `.claude/rules/ee.md`, `internal/cli/docs_ee_reserved_test.go`.
- **Change:**
  - `TestDocsEELicensed` and the README admit code under the notice rule.
  - R8 becomes R5 as amended: agents write code, never hold or reach a signing key, and never
    operate the service.
  - "No key material" admits public keys and signature vectors, never a private half.
- **Tests:** `TestDocsEELicensed`; `go test ./internal/eeboundary/ -count=1`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsEE' -count=1 && go test
  ./internal/eeboundary/ -count=1`.
- **Red step:** the updated test fails until the README and the rule say it.
- **Depends on:** none (#1140 has landed). **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the folder admits its first code; signing keys stay off agents' machines (R5)`.

#### S0.7 The release gate (D448, T17)

- **Files:** `docs/releases/console/README.md` ("The licence changes at 0.113.0"),
  `docs/releases/mobile/README.md`, `internal/cli/docs_release_gate_test.go` (new).
- **Change:** console 0.113.0 and the first app release ship under the business licence once
  counsel signs off D432, D433 and D436 (S8.4). They are **honour-based, with no key
  enforcement**; enforcement arrives with the key service in a later release.
- **Tests:** `TestDocsLicensedReleasesWaitForCounsel`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run TestDocsLicensedReleasesWaitForCounsel -count=1`.
- **Red step:** the test fails until both READMEs carry the gate.
- **Depends on:** none. **Reviewer:** standard.
- **Commit:** `docs(releases): the first licensed console and app releases wait for counsel (D448, T17)`.

### Slice S1: the business-folder moves (design first)

#### S1.1 Design: the moves, file by file

**Superseded 2026-10-29: carried as E0.3 in [`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md).**

*Earlier version: the gating model below — "a paid command is present and refuses without a key" and
"a key-gated build, with the installation's key held as a secret". Now: T27 (b), ruled 2026-10-29 —
the paid commands warn, not refuse, and that warning is itself deferred with the key work (ee-reorg
D523), and the job image is built with the `ee` tag and mounts no key (D524). E0.3 states the
corrected model; the bullets below are kept for the record.*

- **Files:** `docs/design/ee-boundary-moves.md` (new).
- **Content:** the rulings T10, T11, T20 and T21, made concrete.
  - **The coordinator (T10).** What moves and how the core keeps working:
    - **What moves.** The library `internal/coordinator/` (`intent.go`, `project.go`, `state.go`,
      `sweep.go`, `view.go`, their tests and `testdata/`), and the commands
      `internal/cli/coordinator.go`, `internal/cli/coordinator_view.go` with their tests.
    - **What stays in core and why:**
      - the coordinator's database paths (`coordinator.PathRoot`), in a small MIT package the
        project rules (`internal/cli/project_cmd.go`) and the RTDB rules' tests
        (`internal/budget/rules/coordinator_test.go`, `emulator_coordinator_test.go`,
        `rules_test.go`) import;
      - the launch's declaration (`internal/cli/run.go`), through a hook `ee/register` fills.
        An untagged build declares nothing;
      - `coordinator.DocumentID` as well as the paths, which the emulator rules test also uses
        (`PathProject`, `PathProjectDocument`, `PathRepoDocument`, `PathRepos`).
    - **The test data:** `internal/cli/testdata/coordinator_status.golden`,
      `coordinator_mint_refused.json`, `coordinator_no_requests.json` and
      `coordinator_read_token.json` move with the tests. `coordinator_token.json` stays, because
      `internal/gitprov/github/apptoken_test.go` reads it too, and the moved tests take a copy.
    - **The helper surface an `ee/` command needs.** The moved commands call unexported
      `internal/cli` helpers (`userErr`, `remote`, `oneLine`, `openCloud`, `repoSlug`, `clipRunes`;
      for the report, `addCloudFlags`, `budgetDBFor`, `openCloudFor`, `dbErr`). S1.1 names the small
      exported API S1.2 publishes for them, and nothing more.
    - **The pins:** the nine coordinator view types and `consoleContractCommands`
      (`newCoordinatorStatusCmd`, `newReportCmd`) in `internal/cli/docs_desktop_console_test.go`
      move to tagged tests under `ee/coordinator/cli` and `ee/report/cli`.
    - **Other core dependents to re-point:** `internal/cli/doctor_policy_test.go` and
      `internal/cli/docs_desktop_console_test.go`.
    - **The console's project view** (`console/src/renderer/screens/coordinator.ts`) and the
      Spend screen (`spend.ts`) keep their argv; they re-plan for a key-gated command's refusal
      (S6.1), and T27 decides when that refusal can first happen.
  - **The reports (T11).**
    - `internal/report/` (`report.go`, `render.go`, `report_test.go`) and the `fugaro report`
      command move.
    - **What stays in core** from `internal/cli/report.go`, because core MIT code uses it:
      `costLines` and `recordOfRun` (`fugaro diagnose` in `diagnose.go`,
      `cloudrun_golden_test.go`), `reportHistory`, `newReportFirestore` and `firestoreErr`
      (`project_enroll.go`), and `computeSources` (`report_source_test.go`,
      `cloudrun_golden_test.go`). They move into a core file; the command alone moves.
    - `internal/digest` gets an MIT copy of what it uses: `report.Dim`, `report.ParseDim`,
      `report.PeriodKey`, the by-day/week/month/year keys and `report.USD`.
    - The `report.JSONDoc` pin in `docs_desktop_console_test.go` moves to `ee/report/cli`.
    - The Spend screen keeps `report --json` with a key.
    - The working skill marks `fugaro report` as a paid command and the digest as free.
  - **Which command tree** `website/content/cli-reference.md` and the plugin tests read: the
    tagged one, since one binary ships (T21).
  - **The gating model:** a paid command is present and refuses without a key, so the console
    gets a clear refusal.
  - **The sweep job's image (S1.9):** a key-gated build, with the installation's key held as a
    secret.
  - **R1 narrowed** to the run images.
  - **The orchestrators and the MCP server** behind `ee/register` (T20).
  - **The key service's Go code:** where its `main` lives, and whether its dependencies may enter
    the root `go.mod` (the owner's yes).
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** T27, ruled (b) on 2026-10-29 (T10, T11, T20, T21 were ruled 2026-10-27).
  **Reviewer:** opus +
  mandatory independent review.
- **Commit:** `docs(design): the core-to-ee moves for the coordinator and the reports`.

#### S1.2 A command registry and the launch hook in the core

**Superseded 2026-10-29: carried as E1.1 in [`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md).**

- **Files:** `internal/cli/extend.go` (new), `internal/cli/extend_test.go` (new),
  `internal/cli/root.go`, `internal/cli/run.go`, and `internal/cli/api.go` (new; the exported helpers).
- **Interfaces:**
  - **Produces:** a registration function for cobra commands, a launch-declarer hook that
    defaults to nothing, and the small exported helper API S1.1 names for `ee/` commands.
  - **Refuses:** a name a core command holds.
- **Tests:**
  - `TestRegisteredCommandJoinsTheRoot`
  - `TestRegisteredCommandCannotShadowACoreCommand`
  - `TestRegistrationTakesNoCoreNameAliasOrRootFlag`
  - `TestRegistrationTakesNoEarlierRegistrationsName`
  - `TestLateOrDuplicateRegistrationIsRefused`
  - `TestNoRegistrationLeavesTheRootUnchanged`
  - `TestLaunchWithoutADeclarerDeclaresNothing`
- **Command:** `go test ./internal/cli/ -run 'TestRegistered|TestNoRegistration|TestLateOrDuplicate|TestLaunchWithoutADeclarer' -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** the shadowing test fails with no registry.
- **Depends on:** S1.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `cli: a command registry and a launch hook ee/register can fill`.

#### S1.3 `ee/register` and the assembly file

**Superseded 2026-10-29: carried as E1.5 in [`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md).**

- **Files:** `ee/register/register.go` (new), `ee/register/register_test.go` (new),
  `cmd/fugaro/ee_on.go` (new).
- **Tests:**
  - `TestRegisterFollowsTheGatingModel`
  - `go build -tags ee ./cmd/fugaro`
  - the boundary tests, tagged and untagged
- **Command:** `go test ./internal/eeboundary/ -count=1 && go test -tags ee ./ee/register/ -count=1`.
- **Red step:** `TestRegisterFollowsTheGatingModel` fails before the package exists.
- **Depends on:** S1.2, S0.6. The boundary tests already admit exactly `cmd/fugaro/ee_on.go` →
  `ee/register`; no boundary-test edit. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the register package and the one assembly file`.

#### S1.4 The coordinator's command and library move (T10)

**Superseded 2026-10-29: carried as E2.1 in [`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md).**

*Earlier version (before 2026-10-27's T10 ruling): future versions of the command only, with the
library left in core. Now: the command and the library, file by file as S1.1 lists.*

- **Files:**
  - **Moved:**
    - `internal/coordinator/` → `ee/coordinator/` (all files, `testdata/` with `.license`
      siblings);
    - `internal/cli/coordinator.go`, `internal/cli/coordinator_view.go`,
      `internal/cli/coordinator_test.go`, `internal/cli/coordinator_view_test.go` →
      `ee/coordinator/cli/`;
    - `internal/cli/testdata/coordinator_status.golden`, `coordinator_mint_refused.json`,
      `coordinator_no_requests.json`, `coordinator_read_token.json` → `ee/coordinator/cli/testdata/`
      (with `.license` siblings), plus a copy of `coordinator_token.json`, which stays.
  - **New:** the MIT package `internal/coordpath/` (new), for the coordinator's database paths and
    `DocumentID`.
  - **Re-pointed:**
    - `internal/cli/run.go` (the hook), `internal/cli/project_cmd.go`;
    - `internal/budget/rules/coordinator_test.go`, `internal/budget/rules/emulator_coordinator_test.go`,
      `internal/budget/rules/rules_test.go`;
    - `internal/cli/doctor_policy_test.go`, `internal/cli/docs_desktop_console_test.go`.
  - **Registration and docs:** `ee/register/register.go`, `internal/cli/root.go`,
    `website/content/cli-reference.md`, `docs/coordinator/README.md`.
- **Tests:**
  - `TestUntaggedBuildStillLaunches`
  - `TestCoordinatorCommandNeedsAKey` (tagged)
  - the moved tests under `ee/coordinator`
  - the rules tests against `internal/coordpath`
- **Command:** `go test ./internal/cli/ -run 'TestUntaggedBuild|TestDocsDesktop' -count=1 && go test
  ./internal/budget/rules/ -run Coordinator -count=1 && go test -tags ee ./ee/coordinator/... -count=1
  && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestCoordinatorCommandNeedsAKey` fails while the command is core.
- **Depends on:** S1.3, S2.3, S4.1 (`root.go`, `cli-reference.md`), S3.6 and S1.8
  (`docs/coordinator/README.md`). **Reviewer:** opus + mandatory independent review.
- **Commit:** `coordinator: the command and the library move to the business licence (D434, T10)`.

#### S1.5 `fugaro report` moves; the digest stays MIT (T11)

**Superseded 2026-10-29: carried as E2.2 in [`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md).**

*Earlier version: the reports, with the digest's fate open. Now: the report moves, and the digest
stays MIT with its own copy of the period helpers.*

- **Files:**
  - **Moved:** `internal/report/` → `ee/report/`; the command part of `internal/cli/report.go`
    and `internal/cli/report_test.go` → `ee/report/cli/`.
  - **Kept in core:** `internal/cli/reportcore.go` (new; `costLines`, `recordOfRun`,
    `reportHistory`, `newReportFirestore`, `firestoreErr`, `computeSources`), used by
    `internal/cli/diagnose.go`, `internal/cli/project_enroll.go`,
    `internal/cli/cloudrun_golden_test.go` and `internal/cli/report_source_test.go`.
  - **The digest's MIT copy:** `internal/digest/period.go` (new: `Dim`, `ParseDim`, `PeriodKey`,
    the period keys, `USD`), `internal/digest/digest.go`, `internal/digest/patterns.go`,
    `internal/digest/render.go`, `internal/digest/digest_test.go`, `internal/cli/runs_digest.go`,
    `internal/cli/runs_digest_test.go`.
  - **Pins and the skill:** `internal/cli/docs_desktop_console_test.go` (the `report.JSONDoc` pin
    moves to `ee/report/cli`), `plugin/skills/working/SKILL.md`.
  - **Registration and docs:** `ee/register/register.go`, `internal/cli/root.go`,
    `website/content/cli-reference.md`.
- **Tests:**
  - `TestDigestNeedsNoEE`, an untagged build that writes a digest
  - `TestReportCommandNeedsAKey` (tagged)
  - the moved tests
  - the plugin's command checks against the tagged tree
- **Command:** `go test ./internal/digest/ ./internal/cli/ -run 'TestDigest|TestRunsDigest|TestDocsDesktop' -count=1 && go test -tags ee ./ee/report/... -count=1 && go test ./plugin/... -count=1`.
- **Red step:** `TestDigestNeedsNoEE` fails while `internal/digest` imports `internal/report`.
- **Depends on:** S1.4 (`root.go`, `ee/register`, `docs_desktop_console_test.go`). **Reviewer:** opus +
  mandatory independent review.
- **Commit:** `report: fugaro report moves to the business licence; the digest stays MIT (D444, T11)`.

#### S1.6 The single mixed-licence release and its relabel (T21)

- **Files:**
  - `.goreleaser.yaml`, which sets the `ee` tag and the Homebrew cask's licence;
  - every label `TestEETagNeedsTheLabels` reads (`scripts/notices_test.go`);
  - `LICENSING.md` and `README.md`: `fugaro` described as MIT core plus business-licensed parts,
    no longer simply MIT;
  - the website's licence wording (`website/src/pages/community.astro`,
    `website/test/faq-community.mjs`);
  - `docs/releases/README.md`.
- **Tests:** `TestEETagNeedsTheLabels`, `TestGoreleaserReleaseBuild`, `faq-community.mjs`.
- **Command:** `go test ./scripts/ -run 'TestEETagNeedsTheLabels|TestGoreleaserReleaseBuild' -count=1
  && cd website && npm run check`.
- **Red step:** set the tag first; `TestEETagNeedsTheLabels` fails until every label says MIT core
  plus the business licence.
- **Depends on:** E2.2 (ee-reorg; was S1.5), S2.9, S7.1 (`community.astro`); the images task
  E1.4 (was S1.9) lands **after** this release (E3.2 → E1.4), not before. T27, ruled (b) 2026-10-29
  (deferred with the key work, D523).
  **Reviewer:** opus + mandatory independent review.
- **Commit:** `release: one binary, MIT core plus business-licensed parts (D447, T21)`.

#### S1.7 R1 pinned: the run images build `fugaro` untagged

**Superseded 2026-10-29: carried as E1.4 in [`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md). The scan is inverted there: the base and history images carry the tag, credentials does not (D524).**

- **Files:** `images/build_tags_test.go` (new).
- **Tests:**
  - `TestRunImagesBuildFugaroWithoutTheEETag`, over the base and credentials images' build (their
    Dockerfiles, their path through `images/build-base.sh`, and the Cloud Build configuration: a
    `-tags` flag, or a `GOFLAGS` that carries one). The coordinator job's image is S1.9's;
  - `TestBuildTagScanCatchesPlants`.
- **Command:** `go test ./images/ -run 'TestRunImagesBuildFugaroWithoutTheEETag|TestBuildTagScanCatchesPlants' -count=1`.
- **Red step:** the plant (a `-tags ee` line) fails before the scan exists.
- **Depends on:** S1.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `images: the run images build fugaro without the ee tag (R1)`.

#### S1.8 Re-plan the coordinator and orchestrator plans; close #831 (T10, T20)

**Stands (2026-10-29): not superseded by the ee-reorg plan.**

- **Files:** `docs/coordinator/implementation-plan.md`, `docs/coordinator/README.md`,
  `docs/central-orchestrator/implementation-plan.md`, `docs/central-orchestrator/README.md`.
- **Change:** both plans re-target their remaining parts to `ee/` behind `ee/register`: the
  coordinator's P6–P8 including the MCP front door, and the orchestrators' P2 onward. #831 closes
  as superseded, which is OWNER-RUN, or done by an agent on the owner's go with
  `gh pr close 831 --comment "Superseded: re-planned in ee/ (T20)"`.
- **Tests:** the Docs suite (the coordinator docs tests pin those READMEs).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (plans).
- **Depends on:** S1.1. **Reviewer:** standard.
- **Commit:** `docs(plans): the coordinator and orchestrators re-planned behind ee/register (T10, T20)`.

#### S1.9 The coordinator job's key-gated image (T10)

**Superseded 2026-10-29: carried as E1.4 in [`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md) (the history image is built with the tag; no separate coordinator image and no key secret until enforcement, D524).**

- **Files:**
  - the image build the job uses: a coordinator image split from the history image, or the
    history image built with the tag, as S1.1 decides (`images/history/`, `images/build-base.sh`,
    `images/images.go`);
  - `internal/infra/firebase.go` (`CoordinatorImageExists`);
  - `deploy/terraform/gcp/modules/installation/coordinator.tf`, which mounts the installation's
    key as a Secret Manager secret;
  - `images/history/Dockerfile` (it copies `cmd`, `internal`, `images` and `deploy/terraform` today,
    not `ee/`), `images/history_test.go`, `images/build_tags_test.go` (S1.7's scan learns the
    coordinator image's exception);
  - `deploy/terraform/terraform_test.go`
    (`TestTheCoordinatorJobTakesItsCredentialFromASecretAndNothingElse`), `internal/infra/firebase_test.go`
    (`TestCoordinatorTfvarsGolden`) and the `internal/infra/tf/cover_*` goldens;
  - a staged workflow change under `docs/pending-workflows/` if the image is split from the history
    image (`.github/workflows/images.yml` builds it; no agent edits `.github/workflows/`).
- **Tests:**
  - `TestCoordinatorImageCarriesTheEETag`
  - `TestCoordinatorJobMountsTheKeySecret`, a `terraform` test
  - the history job's own tests unchanged
- **Command:** `go test ./images/ -run 'TestCoordinatorImage|TestHistory|TestRunImagesBuild' -count=1
  && go test ./deploy/terraform/ -run Coordinator -count=1 && go test ./internal/infra/ -run
  Coordinator -count=1 && go test -tags terraform ./internal/infra/... -run Coordinator -count=1 &&
  terraform fmt -check -recursive deploy/terraform`.
- **Red step:** `TestCoordinatorImageCarriesTheEETag` fails before the build changes.
- **Depends on:** S1.4, S2.3. **Reviewer:** opus + mandatory independent review. **For operators:**
  each installation that uses the coordinator runs `fugaro update` and stores its key (per
  installation).
- **Commit:** `images: the coordinator's job runs the key-gated build (T10)`.

#### S1.10 Check 178 (OWNER-RUN)

**Deferred until enforcement (2026-10-29, D524): no key is mounted and nothing refuses, so the check does not apply yet.**

- **What the owner does:** on the sandbox installation, with a release from S1.6, runs Check 178.
  No agent performs any step.
- **Depends on:** S1.6.

### Slice S2: the key and its verifiers

#### S2.1 The key design note (licensing L3.1, superseded here)

- **Files:** `docs/licensing/licence-key.md` (new), `docs/licensing/README.md`,
  `docs/licensing/implementation-plan.md` (L3.1 marked superseded).
- **Content:** D308 plus the rulings:
  - **The key:** one key per organisation with a seat count (the key-binding ruling). Its claims
    are tier, features, seats, organisation, `issued`, `expires` and key id. **Expiry only: no
    covered-versions date and no release-date stamping** (O4).
  - **Lapse:** a 30-day grace, then the read-only state, which keeps cancel and the kill switch
    (T12).
  - **Renewal:** the next key issues 30 days before expiry. Two keys may be installed, and the
    verifier takes the latest valid one (T26).
  - **Limits:** a maximum lifetime per tier; no revocation; refund leakage accepted (T25).
  - **Keys for test mode:** a staging key id that production verifiers refuse.
  - **Storage and hand-off:** each product's storage; the console hands the key to the CLI it
    spawns, never in argv.
  - **States and minting:** the no-key state; interim hand minting (T13); the coordinator job's
    key (T10).
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** none (O4, T26 and the binding ruled). **Reviewer:** opus + mandatory independent review.
- **Commit:** `docs(licensing): the licence-key design note (L3.1, D438)`.

#### S2.2 (withdrawn 2026-10-27)

*Earlier version: stamp a `ReleaseDate` into every CLI build for the covered-versions date. Withdrawn
by O4 (expiry only). A version field may be added later, as a new task. The number is not reused.*

#### S2.3 The Go verifier

- **Files:** `ee/licence/key.go` (new), `ee/licence/store.go` (new; the CLI's key file per S2.1),
  `ee/licence/key_test.go` (new), `ee/licence/testdata/vectors.json` (new; public keys and signed
  claims only) and `vectors.json.license` (new).
- **Interfaces:** produces `Verify(raw []byte, now time.Time) (Claims, State, error)` with the states
  valid, grace and expired, and a choice among installed keys.
- **Tests:**
  - `TestVerifyRefusesBeforeParsing`
  - `TestVerifyUnknownKeyID`
  - `TestGraceIsThirtyDays`
  - `TestExpiredAfterGrace`
  - `TestOverlappingKeysPickTheLatest`
  - `TestKeyBeyondTheTierLifetimeIsRefused`
  - `TestTestKeyIDIsNotInTheProductionKeyring`
  - `TestStagingKeyIDIsRefused`
  - `TestLicencePackageImportsNoNetwork`
  - `TestVectorsRoundTrip`
  - the negative vectors: non-canonical signature, foreign key id, truncated claims
- **Command:** `go test -tags ee ./ee/licence/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** the tampered-byte case fails before `Verify` exists.
- **Depends on:** S2.1, S0.6. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the offline licence-key verifier`.

#### S2.4 The console's verifier

- **Files:** `console/src/main/licence.ts` (new), `console/test/unit/licence.test.ts` (new).
- **Tests:** S2.3's cases in TypeScript, with Node's built-in Ed25519.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2
  --testTimeout=60000 test/unit/licence.test.ts`.
- **Red step:** the tampered-byte case fails before the module exists.
- **Depends on:** S2.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `console: the offline licence-key verifier`.

#### S2.5 The app's verifier

- **Files:** `mobile/src/licence/index.js` (new; the only entry point), `mobile/src/licence/verify.js`
  (new), `mobile/src/licence/verify.test.js` (new), `mobile/package.json` (one Ed25519 dependency,
  on the owner's yes), `mobile/src/dependencies.test.js`.
- **Tests:** S2.3's cases in JavaScript; the dependency test admits the one new dependency.
- **Command:** `cd mobile && npm test`.
- **Red step:** the tampered-byte case fails before the module exists.
- **Depends on:** S2.1; the owner's yes on the dependency. **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `mobile: the offline licence-key verifier`.

#### S2.6 One set of vectors for all three verifiers

- **Files:** `console/test/unit/licence-vectors.test.ts` (new), `mobile/src/licence/vectors.test.js` (new).
- **Tests:** every vector in `ee/licence/testdata/vectors.json`, including the negative ones and
  the overlapping pairs, gives the same state in all three verifiers.
- **Command:** `cd console && npm run test:unit -- --maxWorkers=2 test/unit/licence-vectors.test.ts`
  and `cd mobile && npm test`.
- **Red step:** a vector with a wrong expected state fails in both.
- **Depends on:** S2.3, S2.4, S2.5. **Reviewer:** standard.
- **Commit:** `licence: one set of vectors for the Go, console and app verifiers`.

#### S2.7 The console's key entry and hand-off

- **Files:**
  - new: `console/src/main/licence-store.ts`, `console/src/renderer/screens/licence.ts`,
    `console/test/unit/licence-store.test.ts`;
  - changed: `console/src/main/ipc.ts`, `console/src/preload/index.ts`, `console/src/main/spawn.ts`
    (`CHILD_ENV_KEYS`), `console/src/main/wsl.ts`, `console/src/renderer/entry.ts`,
    `console/test/unit/ipc.test.ts`, `console/test/unit/preload-surface.test.ts`.
- **Interfaces:** paste a key, store it, and hand it to the spawned CLI as S2.1 says, never in argv.
  Console code carries no link naming the project's own host.
- **Tests:**
  - `licence-store.test.ts`: a stored key survives a restart; a bad key is refused; argv never
    carries the key;
  - `ipc.test.ts` and `preload-surface.test.ts`, updated.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2
  test/unit/licence-store.test.ts test/unit/ipc.test.ts test/unit/preload-surface.test.ts`.
- **Red step:** the argv case fails before the store exists.
- **Depends on:** S2.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `console: enter and keep the licence key`.

#### S2.8 The app's key entry

- **Files:** `mobile/src/licence/store.js` (new), `mobile/src/licence/store.test.js` (new),
  `mobile/src/licence/index.js`, the settings screen S2.1 names. Storage goes through
  `mobile/src/accounts/expo-secure-store.js`.
- **Tests:** the key is kept in the secure store; a bad key is refused.
- **Command:** `cd mobile && npm test`.
- **Red step:** the bad-key case fails before the store exists.
- **Depends on:** S2.5. **Reviewer:** opus + mandatory independent review.
- **Commit:** `mobile: enter and keep the licence key`.

#### S2.9 The production public key in every verifier

- **Files (agent, on the owner's go, after S3.8):**
  - `ee/licence/keys/production.pub` (new) and its `.license` sibling;
  - the keyrings in `ee/licence/key.go`, `console/src/main/licence.ts` and
    `mobile/src/licence/verify.js`.
- **Tests:** `TestKeyringMatchesThePublishedKey` and its console and app equivalents. The
  test-key and staging-key refusals stay green.
- **Command:** `go test -tags ee ./ee/licence/ -count=1`; `cd console && npm run test:unit --
  --maxWorkers=2 test/unit/licence.test.ts`; `cd mobile && npm test`.
- **Red step:** the match test fails until all three carry the published key.
- **Depends on:** S3.8, S2.6. **Reviewer:** opus + mandatory independent review.
- **Commit:** `licence: the production public key in the Go, console and app verifiers`.

#### S2.10 The owner's mint tool (interim hand minting, T13)

- **Files:** `ee/licence/mint/` (new): a small command that signs a claim set through the KMS key's
  signing API (never a local private key file), and its tests with a fake signer.
- **Tests:** `TestMintSignsThroughTheSignerOnly`, `TestMintRefusesAClaimBeyondTheTierLifetime`,
  `TestMintedKeyVerifies`.
- **Command:** `go test -tags ee ./ee/licence/mint/ -count=1`.
- **Red step:** `TestMintedKeyVerifies` fails before the tool exists.
- **Depends on:** S2.3, S3.1. **Reviewer:** opus + mandatory independent review. **Use is
  OWNER-RUN:** the owner mints on their own machine with their own credentials, after S3.8, until
  the service exists.
- **Commit:** `ee: the owner's mint tool for keys issued by hand`.

### Slice S3: the key service (owner-operated, built after the entity exists)

#### S3.1 Design and threat model (first)

- **Files:** `docs/design/key-signing-service.md` (new).
- **Content:**
  - **Hosting (T13):** a dedicated Fugaro GCP project the owner operates, Cloud Run, a
    non-exportable KMS key, and the internet-facing intake (forms, purchase events, admin page)
    split from a non-public minter that alone holds the signer role. It is built after the entity
    exists, and hand minting (S2.10) applies until then.
  - **The checks:**
    - email verification and the free-mail list;
    - one per organisation by domain, an individual being an organisation of one (T15);
    - a domain with a paid subscription gets no free key;
    - the open-source check with proof of control.
  - **Paid keys:** from the merchant's purchase, renewal and cancellation events (signature-verified,
    idempotent); renewal 30 days before expiry (T26); Enterprise keys from the admin page.
  - **The admin page:** authentication, step-up, CSRF defence, escaping, audit log and per-tier
    limits.
  - **Abuse and privacy:** uniform answers; single-use tokens; throttling within the site's CSP; the
    issuance record and its retention; personal data and the privacy notice (S7.3).
  - **Uptime:** an issuing-uptime target (with the overlap, an outage bites only past 60 days).
  - **Where the code and infrastructure live:** never `deploy/terraform`.
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** S2.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `docs(design): the key service and its threat model (T13)`.

#### S3.2 The issuing service

- **Files:** `ee/keyservice/` (new: intake handlers, the minter, the checks, the issuance record,
  tests), with the location per S3.1.
- **Tests:**
  - free keys and the queue:
    - `TestFreeMailDomainGoesToTheQueue`
    - `TestSecondFreeKeyForADomainGoesToTheQueue`
    - `TestDomainWithAPaidSubscriptionGetsNoFreeKey`
  - eligibility and identity:
    - `TestOpenSourceCheckNeedsProofOfControl`
    - `TestOpenSourceCheckRefusesAPrivateOrNonOSIRepository`
    - `TestUnverifiedEmailMintsNothing`
    - `TestVerificationTokenSingleUse`
    - `TestAnswersDoNotRevealAnExistingKey`
  - purchase events and renewal:
    - `TestPurchaseEventNeedsAValidSignature`
    - `TestPurchaseEventIsIdempotent`
    - `TestReplayedOrOutOfOrderEventIsIgnored`
    - `TestRenewalKeyIssuesThirtyDaysBeforeExpiry`
    - `TestSeatChangeReissues`
    - `TestCancellationStopsReissue`
    - `TestTestModeEventsMintStagingKeysOnly`
  - abuse and signing:
    - `TestApplicantFieldsCannotInjectMailHeaders`
    - `TestRequestsAreThrottled`
    - `TestOnlyTheMinterCallsTheSigner`

  All use fakes for the key store, the code host and the merchant.
- **Command:** `go test -tags ee ./ee/keyservice/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestOnlyTheMinterCallsTheSigner` fails before the handlers exist.
- **Depends on:** S3.1, S2.3, S8.1 (built after the entity exists). **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `ee: the key issuing service`.

#### S3.3 The key website's forms

- **Files:** `website/src/pages/keys.astro` (new), `website/test/keys.mjs` (new), and the CSP tests
  the owner approves changing.
- **Content:** the open-source form, the free-seat form and the manual request form, posting to the
  intake. The CSP's `form-action 'self'` changes only with the owner's approval.
- **Tests:** `keys.mjs` (the three forms, the privacy link, no third-party load).
- **Command:** `cd website && npm run check`.
- **Red step:** `keys.mjs` fails before the page exists.
- **Depends on:** S3.1; the owner's approval of the CSP change. **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `website: the key request forms`.

#### S3.4 The admin page and the manual queue

- **Files:** `ee/keyservice/admin.go` (new), `ee/keyservice/queue.go` (new), their tests.
- **Tests:**
  - `TestAdminRefusesAnyoneButTheOwner`
  - `TestAdminRefusesACrossSiteRequest`
  - `TestQueueEscapesApplicantText`
  - `TestEveryMintIsAudited`
  - `TestPerTierMintLimit`
  - `TestQueueNotifiesTheOwner`
  - `TestApprovalMintsOnce`
- **Command:** `go test -tags ee ./ee/keyservice/ -run 'TestAdmin|TestQueue|TestApproval|TestEveryMint|TestPerTier' -count=1`.
- **Red step:** `TestAdminRefusesAnyoneButTheOwner` fails before the handler exists.
- **Depends on:** S3.2. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the key service's admin page and manual queue`.

#### S3.5 The dedicated project's infrastructure

- **Files:** `ee/keyservice/terraform/` (new): the project's KMS key ring and a non-exportable
  asymmetric signing key, the production and staging keys, the two Cloud Run services and their
  identities, and a `terraform`-tagged test beside it.
- **Tests:** the key is non-exportable; only the minter's identity holds the signer role; the
  intake's identity does not.
- **Command:** `terraform fmt -check -recursive ee/keyservice/terraform`, `terraform validate` there,
  then `go test -tags terraform ./ee/keyservice/... -count=1`.
- **Red step:** the identity test fails while the intake holds the signer role.
- **Depends on:** S3.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the key service's infrastructure`.

#### S3.6 Register Checks 170–179

- **Files:** `docs/gcp-live-checklist.md`, `docs/coordinator/README.md` (the numbers table and the
  highest check section written), `internal/cli/docs_coordinator_test.go` (`coordinatorCheckBlocks`).
- **Tests:** the numbers-table test and the register's docs tests.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** adding `## Check 170` fails the numbers-table test until the README names the block.
- **Depends on:** S3.1, S6.1, S1.8 (`docs/coordinator/README.md`). **Reviewer:** standard.
- **Commit:** `docs: register Checks 170-179, the key service, the lapse state and the coordinator job (owner-run)`.

#### S3.7 Deploy the service and run its checks (OWNER-RUN)

- **What the owner does:** applies the rest of S3.5, deploys S3.2 and S3.4, configures the
  merchant's webhook, and runs Checks 170–175 and 179. No agent performs any step.
- **Depends on:** S3.2, S3.3, S3.4, S3.5, S3.6, S3.8, S7.3, S8.1, S8.3, S8.4, S8.5.

#### S3.8 Create the signing keys (OWNER-RUN)

- **What the owner does:** creates the dedicated project, applies the key-ring part of S3.5 and
  publishes both public halves (production and staging). Hand minting with S2.10 can start from
  here. This step does not need the entity: the key belongs to the project the owner operates.
- **Depends on:** S3.5.

### Slice S4: the seat estimate

#### S4.1 `fugaro seats` (advisory)

- **Files:** `internal/cli/seats.go` (new), `internal/cli/seats_test.go` (new),
  `internal/cli/root.go`, `website/content/cli-reference.md`.
- **Interfaces:** reads `terraform.launchers`, `terraform.operators` and the run requesters over 90
  days (T14: every launcher). It counts groups as unknown size and offers `--json`. It stores and
  sends nothing. It is core MIT: it reads only core data.
- **Tests:**
  - `TestSeatsCountsLaunchersOperatorsAndRequesters`
  - `TestSeatsFlagsGroups`
  - `TestSeatsDeduplicatesPeople`
  - `TestSeatsReadsNinetyDays`
- **Command:** `go test ./internal/cli/ -run 'TestSeats' -count=1`.
- **Red step:** `TestSeatsFlagsGroups` fails before the command exists.
- **Depends on:** S1.2 (`root.go`). **Reviewer:** standard.
- **Commit:** `cli: fugaro seats, an advisory seat estimate (D439)`.

#### S4.2 The console's seats panel

- **Files:** `console/src/renderer/screens/seats.ts` (new), `console/src/main/spawn.ts`,
  `console/src/main/ipc.ts`, `console/src/preload/index.ts`, `console/src/renderer/entry.ts`,
  `console/test/unit/seats.test.ts` (new), `console/test/unit/preload-surface.test.ts`.
- **Tests:** the estimate shows beside the organisation key's seats, with an overage warning and no
  blocking.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2
  test/unit/seats.test.ts test/unit/ipc.test.ts test/unit/preload-surface.test.ts`.
- **Red step:** the overage case fails before the screen exists.
- **Depends on:** S4.1, S2.7, S6.2 (the same console files). **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `console: seats used against the key's seats, advisory`.

### Slice S5: roles (design only here)

#### S5.1 Design: the role model on the existing lists (T16)

- **Files:** `docs/design/roles.md` (new).
- **Content:**
  - **Core:** the cloud keeps enforcing the launcher, operator and admin lists in MIT core (IAM, the
    RTDB rules). A small MIT core change adds the viewer's cloud enforcement: a read-only list.
  - **`ee/`:** the role model, its UX in the console and the app, and the viewer built on those
    lists.
  - **Pairing:** `fugaro pair`.

  The build is a later plan of its own.
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** none (T16 ruled). **Reviewer:** opus + mandatory independent review.
- **Commit:** `docs(design): roles on the existing lists, with the viewer (D446, T16)`.

### Slice S6: the lapse state (a later release, with enforcement, T17)

#### S6.1 Design: what read-only keeps

- **Files:** `docs/design/lapse-read-only.md` (new).
- **Content:**
  - **The console:** after the grace period it can read and cancel, and it cannot launch or change
    config (T12). The console has no launch or cancel today (fifteen read operations), so for it
    this means paid views stay readable and any later cancel stays allowed.
  - **The app:** cancel through the mailbox stays available and must not depend on a paid
    component (the `orchestrator` mailbox agent, D435), and the kill switch stays.
  - **Other states:** the banner; the no-key state; the upgrade from console 0.112.0; a free
    evaluator's expiry.
  - **Enforcement** happens in the console's main process and at the app's mailbox client.
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** S2.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `docs(design): the read-only state after a lapse keeps cancel (D436, T12)`.

#### S6.2 The console's lapse state

- **Files:** `console/src/main/licence-state.ts` (new), `console/src/main/ipc.ts`,
  `console/src/preload/index.ts`, `console/src/renderer/entry.ts`,
  `console/src/renderer/screens/coordinator.ts` and `spend.ts` (a key-gated command's refusal),
  `console/test/unit/licence-state.test.ts` (new), `console/test/unit/ipc.test.ts`,
  `console/test/unit/preload-surface.test.ts`.
- **Tests:**
  - Within grace, everything works.
  - After grace, launch and config operations are refused in the main process, and reads and
    cancel are not.
  - The no-key state behaves as S6.1 says.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2
  test/unit/licence-state.test.ts test/unit/ipc.test.ts test/unit/preload-surface.test.ts`.
- **Red step:** the after-grace main-process refusal fails before the state exists.
- **Depends on:** S6.1, S2.7, S8.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `console: read-only after the grace period, cancel kept (D436, T12)`.

#### S6.3 The app's lapse state

- **Files:** `mobile/src/licence/state.js` (new), `mobile/src/licence/state.test.js` (new),
  `mobile/src/licence/index.js`, the mailbox client, `mobile/src/mailbox/write-path.test.js`.
- **Tests:**
  - After grace, the mailbox client refuses launch and config actions and allows cancel and the
    kill switch.
  - The no-key state behaves as S6.1 says.
- **Command:** `cd mobile && npm test`.
- **Red step:** the after-grace refusal of a launch fails before the state exists.
- **Depends on:** S6.1, S2.8, S8.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `mobile: read-only after the grace period, cancel kept (D436, T12)`.

#### S6.4 Lapse checks (OWNER-RUN)

- **What the owner does:** with a packed console and an app build carrying S6.2, S6.3 and S2.9,
  runs Checks 176–177. No agent performs any step.
- **Depends on:** S6.2, S6.3, S2.9.

### Slice S7: the website (figures stay out of the repository, T22)

#### S7.1 The Community page's licence section

- **Files:** `website/src/pages/community.astro`, `website/test/faq-community.mjs`.
- **Change:** what D431–D433 and T15 settle (an individual is an organisation of one), after counsel.
- **Tests:** the pins in `faq-community.mjs`, updated.
- **Command:** `cd website && npm run check`.
- **Red step:** the updated pin fails first.
- **Depends on:** S8.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `website: the community page states the licence and the free seat`.

#### S7.2 The pricing page

- **Files:** `website/src/pages/pricing.astro` (new), `website/test/pricing.mjs` (new).
- **Change:** the three tiers and their features. The Team price comes in at the owner-run
  publication (S7.4) and never enters the repository. Enterprise is "contact us" (T22). S0.1 scans
  the page.
- **Tests:** `pricing.mjs` checks the three tiers, the feature rows, the price placeholder and the
  Enterprise contact link.
- **Command:** `cd website && npm run check` and `go test ./internal/cli/ -run TestDocsSubscriptions`.
- **Red step:** `pricing.mjs` fails before the page exists.
- **Depends on:** S0.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `website: the pricing page (tiers and features; the price is published, not committed)`.

#### S7.3 The privacy notice for key sign-up

- **Files:** `website/src/pages/privacy.astro`, `website/test/privacy.mjs`.
- **Change:**
  - counsel's text for the sign-up data, with the merchant of record as a recipient;
  - "nothing the Fugaro project operates" restated for a key service the software never calls;
  - rule 5 extended to `ee/`, excluding `ee/keyservice/`.

  All of it needs the owner's approval.
- **Tests:** `privacy.mjs`.
- **Command:** `cd website && npm run check`.
- **Red step:** the new section's pin fails first.
- **Depends on:** S8.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `website: the privacy notice covers key sign-up`.

#### S7.4 Publish (OWNER-RUN)

- **What the owner does:** previews and promotes the site (`docs/website/preview-promote.md`),
  inserting the Team price at publication from the private repository.
- **Depends on:** S7.1, S7.2, S7.3, S3.3, S3.7.

### Slice S8: the owner's, counsel's and the accountant's steps

- **S8.1 The entity.** The owner incorporates it with the accountant. **Depends on:** none.
- **S8.2 The copyright assignment.** Counsel provides the instrument and the accountant the
  mechanics (O5). **Nothing happens before the entity exists (T18).** **Depends on:** S8.1.
- **S8.3 The copyright commit.** One commit, made by an agent on the owner's go once the entity
  exists and S8.2 is signed (T18).
  - **Files:**
    - `LICENSE`, `ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md`,
      `LICENSES/LicenseRef-Fugaro-Subscription.txt`;
    - `internal/cli/docs_licensing_test.go` (the root licence's hash);
    - the Terms' Licensor line (`docs/licensing/subscription-terms.md` §1.1);
    - any website page naming the copyright holder (none found on `main` at `d6f97594c`).

    The CLA's Project Owner follows the root `LICENSE` and needs no line of its own.
  - **Tests:** `TestDocsLicensingRootLicenceUnchanged` and `internal/eeboundary`'s licence tests.
  - **Command:** `go test ./internal/eeboundary/ -count=1 && GOFLAGS=-p=2 go test ./internal/cli/
    -run Docs -count=1`.
  - **Red step:** the hash test fails until its pin is updated.
  - **Depends on:** S8.2, S0.4 (the same Terms file).
  - **Reviewer:** opus + mandatory independent review.
  - **Commit:** `licence: the copyright passes to the licensing entity (D441, B9, T18)`.
- **S8.4 Counsel.** Counsel receives the amended Terms and S0.2, then:
  - answers questions 12–18 and the addendum;
  - words D433;
  - confirms D432, D436 and T12's cancel, T15, T17's honour-based release and T21;
  - reviews the privacy notice;
  - sets the interest rate.

  **Its sign-off gates console 0.113.0 and the first app release (D448, T17, S0.7).**
  **Depends on:** S0.2.
- **S8.5 The Paddle account.** In the entity's name, with test mode. The §4.3 discounts are
  configured from the private figures, and the Team true-up (§5.6) is collected at renewal.
  **Depends on:** S8.1.
- **S8.6 The figures.** Prices, discounts and the Enterprise minimum and floor (O1) go in the private
  repository. **Depends on:** none.

## Parallel execution

S1.1–S1.5, S1.7 and S1.9 are superseded (2026-10-29) and stay in these tables for the record; their scheduling is the ee-reorg plan's E-tasks.

**Edges**, from the Depends-on lines:

- **Counsel and the entity:**
  - S0.2→S8.4.
  - S8.4→S0.4, S6.2, S6.3, S7.1, S7.3, S3.7.
  - S8.1→S8.2, S8.5, S3.2, S3.7.
  - S8.2→S8.3; S0.4→S8.3, S7.2; S8.3→S3.7; S8.5→S3.7.
- **The `ee/` folder:** S0.6→S0.3, S1.3, S2.3.
- **The moves (S1):**
  - S1.1→S0.3, S1.2, S1.7, S1.8.
  - S1.2→S1.3, S4.1.
  - S1.3, S2.3, S4.1, S3.6, S1.8→S1.4.
  - S1.4→S1.5, S1.9; S2.3→S1.9.
  - S1.5, S1.9, S2.9, S7.1→S1.6; S1.6→S1.10.
- **The key (S2):**
  - S2.1→S2.3, S2.4, S2.5, S3.1, S6.1.
  - S2.3, S2.4, S2.5→S2.6; S2.4→S2.7; S2.5→S2.8.
  - S3.8, S2.6→S2.9; S2.3, S3.1→S2.10.
- **The key service (S3):**
  - S3.1→S3.2, S3.3, S3.5, S3.6.
  - S6.1, S1.8→S3.6.
  - S3.2→S3.4; S3.5→S3.8.
  - S3.2, S3.3, S3.4, S3.5, S3.6, S3.8, S7.3→S3.7.
- **Seats and the lapse state:**
  - S4.1, S2.7, S6.2→S4.2.
  - S2.7, S6.1→S6.2; S2.8, S6.1→S6.3.
  - S6.2, S6.3, S2.9→S6.4.
- **Website:** S3.7, S7.1, S7.2, S7.3, S3.3→S7.4.

| Level | Tasks (run concurrently) |
| --- | --- |
| 0 | S0.1, S0.2, S0.5, S0.6, S0.7, S1.1, S2.1, S5.1; owner: S8.1, S8.6 |
| 1 | S0.3, S1.2, S1.7, S1.8, S2.3, S2.4, S2.5, S3.1, S6.1; owner/counsel: S8.2, S8.4, S8.5 |
| 2 | S0.4, S1.3, S2.6, S2.7, S2.8, S2.10, S3.2, S3.3, S3.5, S3.6, S4.1, S7.1, S7.3 |
| 3 | S1.4, S3.4, S6.2, S6.3, S7.2, S8.3; owner: S3.8 |
| 4 | S1.5, S1.9, S2.9, S4.2; owner: S3.7 |
| 5 | S1.6; owner: S6.4, S7.4 |
| 6 | owner: S1.10 (Check 178) |

**Critical path, before and after the rulings.**

- **Before:** seven levels, S2.1 → S3.1 → S3.2 → S3.4 → S3.7 → S2.9 → S1.6.
- **After:** six levels of agent and owner work, then one owner-run check.
  - **S2.1 → S3.1 → S3.5 → S3.8 → S2.9 → S1.6:** the signing keys exist before the service (T13's
    hand minting), so the production key no longer waits for the service to be live.
  - **S1.1 → S1.2 → S1.3 → S1.4 → S1.5 → S1.6:** the `root.go` chain is shorter by one, because S2.2
    is withdrawn.
  - Both paths end at the one-binary release, followed by S1.10 (Check 178).
  - **The service-live chain** S2.1 → S3.1 → S3.2 → S3.4 → S3.7 → S7.4 is also six levels. It is
    gated by the entity (S8.1), and is no longer on the release path.
- **Release-blocking:** console 0.113.0 and the first app release now wait **only for counsel**
  (S0.2 → S8.4, then S0.7's gate), honour-based (T17). The lapse state and the key move to a
  later, enforcing release.

**What must be sequential and why:**

- S1.2 → S4.1 → S1.4 → S1.5: all edit `internal/cli/root.go`, and the last three also
  `website/content/cli-reference.md`.
- S1.8 → S3.6 → S1.4: `docs/coordinator/README.md`.
- S0.6 → S0.3: the `ee/` docs.
- S2.7 → S6.2 → S4.2: the console's `ipc.ts`, `preload/index.ts` and `entry.ts`.
- S2.8 → S6.3: `index.js`.
- S0.4 → S8.3: the Terms file.
- S7.1 → S1.6: `community.astro`.
- S3.8 → S2.9: the key must exist.

### Shared files (no level holds two writers of one file)

| File | Writers (level) |
| --- | --- |
| `internal/cli/root.go` | S1.2 (1), S4.1 (2), S1.4 (3), S1.5 (4) |
| `internal/cli/run.go` | S1.2 (1), S1.4 (3) |
| `website/content/cli-reference.md` | S4.1 (2), S1.4 (3), S1.5 (4) |
| `ee/register/register.go` | S1.3 (2), S1.4 (3), S1.5 (4) |
| `internal/cli/docs_desktop_console_test.go` | S1.4 (3), S1.5 (4) |
| `internal/cli/api.go` | S1.2 (1) |
| `internal/cli/report.go`, `internal/cli/reportcore.go`, `internal/cli/diagnose.go`, `internal/cli/project_enroll.go`, `internal/cli/cloudrun_golden_test.go`, `internal/cli/report_source_test.go` | S1.5 (4) |
| `internal/cli/testdata/coordinator_*` | S1.4 (3) |
| `ee/README.md`, `.claude/rules/ee.md`, `internal/cli/docs_ee_reserved_test.go` | S0.6 (0), S0.3 (1) |
| `LICENSING.md` | S0.3 (1), S1.6 (5) |
| `README.md`, `.goreleaser.yaml`, `docs/releases/README.md` | S1.6 (5) |
| `SECURITY.md`, `website/content/security.md`, `website/test/security.mjs` | S0.5 (0) |
| `docs/releases/console/README.md`, `docs/releases/mobile/README.md` | S0.7 (0) |
| `docs/licensing/README.md`, `docs/licensing/implementation-plan.md` | S2.1 (0) |
| `docs/subscriptions/README.md` | S0.2 (0) |
| `docs/licensing/subscription-terms.md` | S0.4 (2), S8.3 (3) |
| `internal/cli/docs_licensing_test.go`, the five licence files | S8.3 (3) |
| `docs/coordinator/README.md` | S1.8 (1), S3.6 (2), S1.4 (3) |
| `docs/coordinator/implementation-plan.md`, `docs/central-orchestrator/*` | S1.8 (1) |
| `internal/cli/docs_coordinator_test.go`, `docs/gcp-live-checklist.md` | S3.6 (2) |
| `internal/budget/rules/*_test.go` (coordinator), `internal/cli/project_cmd.go`, `internal/cli/doctor_policy_test.go` | S1.4 (3) |
| `internal/digest/*`, `internal/cli/runs_digest.go`, `plugin/skills/working/SKILL.md` | S1.5 (4) |
| `images/*` (history/coordinator build), `internal/infra/firebase.go`, `internal/infra/firebase_test.go`, `internal/infra/tf/cover_*`, `deploy/terraform/terraform_test.go`, `deploy/terraform/gcp/modules/installation/coordinator.tf`, `docs/pending-workflows/*` | S1.9 (4) |
| `images/build_tags_test.go` | S1.7 (1), S1.9 (4) |
| `console/src/main/ipc.ts`, `console/src/preload/index.ts`, `console/src/renderer/entry.ts`, `console/test/unit/ipc.test.ts`, `console/test/unit/preload-surface.test.ts` | S2.7 (2), S6.2 (3), S4.2 (4) |
| `console/src/renderer/screens/coordinator.ts`, `console/src/renderer/screens/spend.ts` | S6.2 (3) |
| `console/src/main/spawn.ts` | S2.7 (2), S4.2 (4) |
| `console/src/main/wsl.ts` | S2.7 (2) |
| `console/src/main/licence.ts` | S2.4 (1), S2.9 (4) |
| `mobile/package.json`, `mobile/src/dependencies.test.js` | S2.5 (1) |
| `mobile/src/licence/index.js` | S2.5 (1), S2.8 (2), S6.3 (3) |
| `mobile/src/licence/verify.js` | S2.5 (1), S2.9 (4) |
| `mobile/src/licence/` (other files) | S2.5 (1, `verify.test.js`), S2.6 (2, `vectors.test.js`), S2.8 (2, `store*`), S6.3 (3, `state*`): distinct files |
| `mobile/src/mailbox/` (client, `write-path.test.js`) | S6.3 (3) |
| `ee/licence/` | S2.3 (1), S2.10 (2, `mint/` only), S2.9 (4) |
| `ee/keyservice/` | S3.2 (2), S3.5 (2, `terraform/` only), S3.4 (3) |
| `website/src/pages/community.astro`, `website/test/faq-community.mjs` | S7.1 (2), S1.6 (5) |
| other `website/src/pages/*`, `website/test/*` | S0.5 (0, `security`), S3.3 (2, `keys` and approved CSP tests), S7.3 (2, `privacy`), S7.2 (3, `pricing`): distinct files |

S3.2 and S3.5 write under `ee/keyservice/` in level 2 in disjoint paths. If S3.1 puts them in one Go
package, S3.5 and S3.8 move one level later. S2.10 writes only `ee/licence/mint/` beside S2.3's
package, one level later.

## Checks this plan takes (170–179)

All are OWNER-RUN, and none has run. S3.6 writes them into `docs/gcp-live-checklist.md`. **Check 178
changed its use on 2026-10-27.** It measured the release date before; O4 withdrew that.

| Check | Measures | Run by |
| --- | --- | --- |
| 170 | A key from the service (or hand-minted with S2.10) verifies in the `ee` CLI, the console and the app with the network off | S3.7 |
| 171 | A free-mail domain, a second free key for one organisation, and a domain with a paid subscription get no free key | S3.7 |
| 172 | The open-source check needs proof of control; it refuses a private or non-OSI repository and accepts an all-public OSI one | S3.7 |
| 173 | The admin page refuses an unauthenticated visitor and any account but the owner's; a queued request notifies the owner; approval mints one key | S3.7 |
| 174 | The signing key cannot be exported; only the minter's identity signs; the intake's and an agent's identities are refused | S3.7 |
| 175 | With the service stopped, installed keys keep working, and the next key, issued 30 days before expiry, overlaps the current one | S3.7 |
| 176 | In the packed console: within grace, everything works; after grace, reads and cancel remain while launch and config are refused in the main process; with no key, it behaves as S6.1 says; the MIT CLI still launches and cancels | S6.4 |
| 177 | In the app after grace: cancel through the mailbox and the kill switch work; launch and config are refused | S6.4 |
| 178 | **(changed; deferred until enforcement, 2026-10-29)** The coordinator's sweep job runs the tagged build; a key check and its refusal come only with enforcement (D524, nothing is mounted), and the console's project view (`coordinator status --from-store`) still reads the stored view | S1.10 |
| 179 | A purchase in the merchant's test mode mints a staging-key Team key that production verifiers refuse; a renewal reissues it; a cancellation stops the reissue | S3.7 |

## Release actions

- The key service's deploy (S3.7) and key creation (S3.8) belong to the licensor, not to an
  operator. They have no `### For operators` entry; S3.1 holds their runbook.
- The release carrying S1.9 has an operator entry. Each installation that uses the coordinator runs
  `fugaro update` and stores its organisation's key as the job's secret.
- The single-binary release (S1.6) says in its notes that `fugaro` is MIT core plus
  business-licensed parts.
- Console 0.113.0 and the first app release say they are under the business licence and
  honour-based (T17). A later console or app release carrying S6.2 or S6.3 states the lapse
  behaviour.
