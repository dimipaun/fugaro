# Subscriptions: implementation plan

## Opening and status

This plan builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)**, which
wins where they differ. Base: `origin/main` at `d9b675a31`, 2026-10-27.

**Status: plan only. Nothing is implemented, and NO TASK IS APPROVED.** Each task needs the owner's
explicit go; answering D430–D448 and B1–B17 approved none of them. Several slices also wait on an
owner ruling on an open tension (T10–T23), on counsel (S8.4) or on the new entity (S8.1); each task
says which.

The owner's answers of 2026-10-27 that shape it (non-price; figures are private, D430):

- **D431** counsel's business licence stays; no FSL. **D447** one key-gated binary (`ee` tag).
- **D432** one free evaluation seat per organisation with no subscription; open-source teams free.
  **D433** a seat is anyone who launches jobs by any means, as a pricing rule (not the
  recommendation). **D439** an advisory seat estimate counting all launchers.
- **D434** future coordinator versions and **D444** spend and cost reports move to the
  business-licensed folder (both not the recommendation); **D435** orchestrators and the hosted MCP
  server start there.
- **D436** no perpetual fallback: read-only in the console and the app after the 30-day grace
  (B17); the MIT CLI keeps launching and cancelling (not the recommendation).
- **D438** a signed offline key for every user; **D440** a hosted signing service, a self-serve key
  website, a manual queue and an admin page (both not the recommendation).
- **D441/B9** a new entity, copyright assigned now; **D442** Paddle for Team checkout.
- **D446** roles enforced system-wide, designed first. **D448** counsel signs off D432, D433 and
  D436 before console 0.113.0 and the first mobile release.

## Conventions every part follows

- `CLAUDE.md`: targeted tests only (`-run`), gofmt and vet, no new dependency or framework without
  the owner's yes, comments say why, search before writing a helper, docs are tested
  (`GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`).
- `.claude/rules/ee.md` for every `ee/**` task: the two-line SPDX notice on every file, nothing in
  the core imports `ee/`, `cmd/fugaro/ee_on.go` is the only route, **no key material in any file**,
  and `go test ./internal/eeboundary/ -count=1` after each change. Never edit a boundary test to
  admit a route: that is the owner's.
- `.claude/rules/console.md` for `console/**`: a new spawned command or IPC operation is
  security-sensitive (exact argv in `src/main/spawn.ts` and `CONSOLE_OPERATIONS` in
  `src/main/ipc.ts`); new dependencies pinned exactly.
- `.claude/rules/mobile.md` for `mobile/**`: `mobile/` is business-licensed, `shared/` MIT; `npm
  test` is the only check (no CI). An Ed25519 verifier in the app needs a dependency: the owner
  approves it first.
- `.claude/rules/website.md` for `website/**`: `npm run check`; the CSP is `script-src 'self'` plus
  the analytics host; privacy and legal copy change only with the owner's approval.
- `.claude/rules/terraform.md`: **`deploy/terraform` is embedded in the Go binary**, so the signing
  service's infrastructure never lives there (it would ship to every customer).
- `.claude/rules/credentials-service.md` is the model for the signing service's security posture
  (D438's note) but does not load for its files; S3.1 states which of its rules apply.
- **No figure from the private business repository enters any file** (D430): S0.1 pins it.
- No secret-shaped literal in tests, docs or fixtures: verifier tests generate keys at test time;
  committed vectors hold public keys and signatures only.
- No live cloud action by an agent; every live check and every deploy is OWNER-RUN. No agent
  holds, generates for use, or reaches the production signing key (R5).
- No agent edits `.github/workflows/`.
- Each part that needs a step after deploy (the signing service's deploy, a release that sets the
  `ee` tag) writes it into its release notes' `### For operators` section (`docs/releases/README.md`).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No price, currency amount, discount offer, fee rate or Enterprise floor in the repository | S0.1 |
| The run images and the job images build `fugaro` without the `ee` tag (R1) | S1.7 |
| The core never imports `ee/`; paid commands reach the binary only through `ee/register` | S1.2, S1.3 (existing boundary tests) |
| The MIT CLI launches and cancels at every licence state (D436) | S1.4, S1.5 tests that the untagged build still launches; S6.2 |
| A key is verified over the raw bytes before parsing, offline, with no network call | S2.3, S2.4, S2.5, S2.6 |
| Grace is 30 days after `expires` (B17), then read-only in the console and the app | S2.1, S6.2, S6.3 |
| Seats are warned, never enforced (D439) | S4.2 |
| The signing service mints only after its checks; the admin page refuses anyone but the owner | S3.2, S3.4, Checks 171–173 |
| The signing key cannot be exported or used by any identity but the service's | S3.5, Check 174 |
| An issuing outage never changes a key's validity | Check 175 |
| Personal data at sign-up is the minimum, with retention stated | S3.1, S7.3 |

**Review focus for the independent reviewers:** the signing service as an attacker would see it
(mint a key for a domain you do not own; a second free key for one organisation; reach the admin
page; replay a verification email; forge an open-source eligibility), the verifier (malleable
encodings, key id confusion, expiry arithmetic, clock set back), the `ee/register` extension point
(can an untagged build be made to load paid code; can a paid command shadow a core one), and the
read-only state (does it ever remove cancel or the kill switch, T12).

## Slices and tasks

### Slice S0: the licence and Terms documents (docs only)

#### S0.1 A docs test that no price figure enters the repository

- **Files:** `internal/cli/docs_subscriptions_test.go` (new).
- **Interfaces:** reads `docs/subscriptions/*.md`, `docs/licensing/*.md` and
  `website/src/pages/pricing.astro` once it exists. **Its limit:** commit messages, PR bodies and
  the rest of the site are outside it; the independent review of each PR covers them.
- **Tests:** `TestDocsSubscriptionsCarryNoPriceFigures` (a currency sign or code beside a digit, a
  percentage offered as a discount, the words that name a fee rate; the Terms draft's existing
  bracketed values are allowed by an exact list), `TestDocsSubscriptionsPriceScanCatchesPlants`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsSubscriptions' -count=1`.
- **Red step:** the plant test feeds a fixture line with a currency amount and fails until the scan
  exists.
- **Depends on:** none. **Reviewer:** standard.
- **Commit:** `docs(subscriptions): a docs test that no price figure enters the repository`.

#### S0.2 The counsel packet

- **Files:** `docs/subscriptions/counsel-packet.md` (new), `docs/subscriptions/README.md`.
- **Content:** the T19 amendment list as tasks for counsel; O3's questions; the Terms draft's own
  counsel questions; T15 (the free seat, "organisation", individuals) and T23 (supported versions); the three sign-offs D448 requires (D432, D433, D436) with what each answer
  unblocks; no figure (the private repository holds them).
- **Tests:** S0.1's scan once merged; `go test ./internal/cli/ -run Docs`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (a new document); S0.1's scan is its gate.
- **Depends on:** none. **Reviewer:** standard.
- **Commit:** `docs(subscriptions): the counsel packet for the Terms amendments and sign-offs`.

#### S0.5 Supported versions (T23)

- **Files:** `SECURITY.md`, the docs tests that pin its "Supported versions" section (found by
  `git grep -l 'Supported versions' internal/cli`).
- **Change:** the owner's ruling on T23 (the core keeps "latest only", or both follow B8).
- **Tests:** the existing pins, updated to the new sentence.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** change the pin first; it fails until `SECURITY.md` says it.
- **Depends on:** the owner's ruling on T23. **Reviewer:** standard.
- **Commit:** `security: supported versions per the owner's ruling (B8)`.

#### S0.3 The licence documents follow D434, D435 and D444

- **Files:** `LICENSING.md`, `ee/README.md`, `.claude/rules/ee.md`,
  `internal/cli/docs_ee_reserved_test.go`.
- **Change:** D302's "nothing moves out of the core" becomes "future versions of the coordinator and
  the spend reports move; released MIT versions stay MIT" in the words S1.1 settles.
- **Tests:** the existing `TestDocsEELicensed` and the `docs_ee_reserved_test.go` pins, updated
  to the new sentence (the pinned fact kept, the wording changed in both places).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs.*EE|TestDocsEE' -count=1` and
  `go test ./internal/eeboundary/ -count=1`.
- **Red step:** change the pin first; it fails until the three files say it.
- **Depends on:** S1.1; the owner's rulings on T10 and T11. #1140 (open, edits `LICENSING.md` and
  `ee/README.md`) lands or closes first. **Reviewer:** opus + mandatory independent review
  (licence wording).
- **Commit:** `licensing: the core-to-ee moves the owner ruled (D434, D444)`.

#### S0.4 The Terms amendment run (after counsel)

- **Files:** `docs/licensing/subscription-terms.md`.
- **Change:** every T19 row, in counsel's words where counsel gave them; figures stay brackets
  pointing at the private repository.
- **Tests:** S0.1's scan; `go test ./internal/cli/ -run Docs`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none; S0.1 is the gate.
- **Depends on:** S8.4; #1140 (which edits the same file) landed or closed. **Reviewer:** opus +
  mandatory independent review.
- **Commit:** `docs(licensing): the Subscription Terms amended per the owner's answers and counsel`.

### Slice S1: the business-folder moves (design first)

#### S1.1 Design: what moves into `ee/` and how

- **Files:** `docs/design/ee-boundary-moves.md` (new).
- **Content:** the command registry the core exposes and `ee/register` fills; for the coordinator
  (T10): the command, the sweep, the store and intent API used by the launch (F31) and the
  project rules, the job image (F32); for the reports (T11): `fugaro report`, `fugaro runs digest`,
  `internal/report`, the console's Spend screen, the plugin skill; the orchestrators against #831
  (T20); the released binary's labels (F34, T21); R1's test (S1.7) and the coordinator job's
  shared history image (F32); how the key reaches a paid CLI command when the console spawns it
  (F41, with S2.1); which tree `website/content/cli-reference.md` and the plugin skills describe
  (F45); the digest inside a run (F46); the migration for users of the released MIT versions.
- **Tests:** `go test ./internal/cli/ -run Docs`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** the owner's rulings on T10, T11, T20, T21. **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `docs(design): the core-to-ee moves for the coordinator and the reports`.

#### S1.2 A command registry in the core

- **Files:** `internal/cli/extend.go` (new), `internal/cli/extend_test.go` (new),
  `internal/cli/root.go`.
- **Interfaces:** produces a registration function a package outside the core may call at init to
  add a cobra command; refuses a name a core command holds.
- **Tests:** `TestRegisteredCommandJoinsTheRoot`, `TestRegisteredCommandCannotShadowACoreCommand`,
  `TestRegisteredCommandCannotReplaceCoreSubcommandsOrPersistentFlags`,
  `TestLateOrDuplicateRegistrationIsRefused`, `TestNoRegistrationLeavesTheRootUnchanged`.
- **Command:** `go test ./internal/cli/ -run 'TestRegistered|TestNoRegistration' -count=1` and
  `go test ./internal/eeboundary/ -count=1`.
- **Red step:** the shadowing test, written first, fails with no registry.
- **Depends on:** S1.1. **Reviewer:** opus + mandatory independent review (licence boundary).
- **Commit:** `cli: a command registry ee/register can add to`.

#### S1.3 `ee/register` and the assembly file

- **Files:** `ee/register/register.go` (new), `ee/register/register_test.go` (new),
  `cmd/fugaro/ee_on.go` (new).
- **Interfaces:** consumes S1.2's registry; `ee_on.go` is exactly the shape
  `internal/eeboundary/boundary_test.go` allows.
- **Tests:** `go build -tags ee ./cmd/fugaro`, `go test -tags ee ./ee/...`, and the boundary tests
  untagged and tagged.
- **Command:** `go test ./internal/eeboundary/ -count=1 && go test -tags ee ./ee/register/ -count=1`.
- **Red step:** `TestRegisterAddsNothingWithoutAFeature` fails before the package exists.
- **Depends on:** S1.2. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the register package and the one assembly file`.

#### S1.4 Future coordinator versions move

- **Files:** per S1.1; at least `internal/cli/coordinator.go`, `internal/cli/coordinator_view.go`,
  `internal/cli/root.go`, `ee/coordinator/` (new), `ee/register/register.go`,
  `docs/coordinator/README.md`, `website/content/cli-reference.md` and its test
  `internal/cli/docs_website_cliref_test.go` (F45), and, if T10 moves the sweep,
  `deploy/terraform/gcp/modules/installation/coordinator.tf`, `internal/infra/firebase.go` and the
  image build under `images/`.
- **Interfaces:** the launch's declaration (F31) keeps working in an untagged build.
- **Tests:** `TestUntaggedBuildStillDeclaresLaunches`, `TestCoordinatorCommandNeedsAKey` (tagged),
  the moved commands' existing tests under `ee/coordinator`.
- **Command:** `go test ./internal/cli/ -run 'TestUntaggedBuild|TestCoordinator' -count=1 && go
  test -tags ee ./ee/coordinator/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestCoordinatorCommandNeedsAKey` fails while the command is core.
- **Depends on:** S1.3, S2.3, the owner's ruling on T10. **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `coordinator: future versions under the business licence (D434)`.

#### S1.5 Future spend-report versions move

- **Files:** per S1.1; at least `internal/cli/report.go`, `internal/cli/root.go`, `ee/report/`
  (new), `ee/register/register.go`, `plugin/skills/working/SKILL.md`,
  `internal/cli/docs_desktop_console_test.go` (pins `report.JSONDoc`),
  `website/content/cli-reference.md`, `console/src/renderer/screens/spend.ts` if the screen's
  source changes, and, if T11 moves the digest, `internal/cli/runs_digest.go` and
  `plugin/skills/working/reference/periodic-reviews.md`.
- **Tests:** the plugin's command checks, `TestReportCommandNeedsAKey` (tagged), the moved tests.
- **Command:** `go test ./internal/cli/ -run 'TestReport|TestRunsDigest' -count=1 && go test
  -tags ee ./ee/report/ -count=1 && go test ./plugin/... -count=1`.
- **Red step:** `TestReportCommandNeedsAKey` fails while the command is core.
- **Depends on:** S1.4 (both edit `internal/cli/root.go`), the owner's ruling on T11.
  **Reviewer:** opus + mandatory independent review.
- **Commit:** `report: future spend-report versions under the business licence (D444)`.

#### S1.6 The release that sets the `ee` tag

- **Files:** `.goreleaser.yaml` and every label `TestEETagNeedsTheLabels` reads
  (`scripts/notices_test.go`), `docs/releases/README.md`.
- **Tests:** `TestEETagNeedsTheLabels`, `TestGoreleaserReleaseBuild`.
- **Command:** `go test ./scripts/ -run 'TestEETagNeedsTheLabels|TestGoreleaserReleaseBuild' -count=1`.
- **Red step:** set the tag first; `TestEETagNeedsTheLabels` fails until every label names the
  enterprise licence.
- **Depends on:** S1.5, S2.2, the owner's ruling on T21. **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `release: the ee build tag with every licence label`.

#### S1.7 R1 pinned: the images build `fugaro` untagged

- **Files:** `images/build_tags_test.go` (new).
- **Tests:** `TestImagesBuildFugaroWithoutTheEETag` over the Dockerfiles under `images/`, and
  `TestBuildTagScanCatchesPlants`.
- **Command:** `go test ./images/ -run 'TestImagesBuildFugaroWithoutTheEETag|TestBuildTagScanCatchesPlants' -count=1`.
- **Red step:** the plant (a Dockerfile line with `-tags ee`) fails before the scan exists.
- **Depends on:** S1.1, and the owner's ruling on T10 (whether the coordinator's job image is an
  exception) must be in S1.1 before it. **Reviewer:** opus + mandatory independent review (R1).
- **Commit:** `images: the run images build fugaro without the ee tag (R1)`.

### Slice S2: the key, offline verification and the release date

#### S2.1 The key design note (licensing L3.1, amended)

- **Files:** `docs/licensing/licence-key.md` (new), `docs/licensing/README.md`.
- **Content:** D308 plus D438: claims, encoding, the organisation binding (F35), the key id and
  rotation, the 30-day grace and the read-only state (D436), no revocation and short free-key
  lives (D440), the covered-versions date's role (O4), the release date, test vectors without
  private keys; **where each product stores the key and how the console hands it to the CLI it
  spawns, never in argv** (F41); the no-key state (F43).
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** the owner's answer to O4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `docs(licensing): the licence-key design note (L3.1, D438)`.

#### S2.2 The release date in every CLI build

- **Files:** `.goreleaser.yaml`, `internal/cli/releaseinfo.go`, `internal/cli/releaseinfo_test.go`.
- **Interfaces:** produces `internal/cli.ReleaseDate` (empty in a development build), shown by
  `fugaro version`.
- **Tests:** `TestReleaseDateDefaultsToDevelopment`, `TestGoreleaserStampsTheReleaseDate`.
- **Command:** `go test ./internal/cli/ -run 'TestReleaseDate|TestGoreleaserStamps' -count=1`.
- **Red step:** `TestGoreleaserStampsTheReleaseDate` fails until the ldflag exists.
- **Depends on:** S2.1. **Reviewer:** standard.
- **Commit:** `release: stamp the release date beside the version and commit`.

#### S2.3 The Go verifier

- **Files:** `ee/licence/key.go` (new), `ee/licence/store.go` (new; the CLI's key file per S2.1),
  `ee/licence/key_test.go` (new), `ee/licence/testdata/vectors.json` (new; public keys and signed
  claims only).
- **Interfaces:** produces `Verify(raw []byte, now time.Time) (Claims, State, error)` with states
  valid, grace, expired; consumes the embedded public keys by key id.
- **Tests:** `TestVerifyRefusesBeforeParsing`, `TestVerifyUnknownKeyID`,
  `TestGraceIsThirtyDays`, `TestExpiredAfterGrace`, `TestVectorsRoundTrip` (the vectors are
  produced by a test-time key whose private half is never written),
  `TestTestKeyIDIsNotInTheProductionKeyring`, and negative vectors (a non-canonical signature, a
  key id naming another key, a truncated claim set).
- **Command:** `go test -tags ee ./ee/licence/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestVerifyRefusesBeforeParsing` with a tampered byte fails before `Verify` exists.
- **Depends on:** S2.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the offline licence-key verifier`.

#### S2.4 The console's verifier and release date

- **Files:** `console/src/main/licence.ts` (new), `console/test/unit/licence.test.ts` (new), the
  console's build configuration (the file U5 finds) for the release date.
- **Tests:** the S2.3 cases in TypeScript with Node's built-in Ed25519.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2
  --testTimeout=60000 test/unit/licence.test.ts`.
- **Red step:** the tampered-byte case fails before the module exists.
- **Depends on:** S2.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `console: the offline licence-key verifier and the release date`.

#### S2.5 The app's verifier

- **Files:** `mobile/src/licence/index.js` (new; the module's only entry point),
  `mobile/src/licence/verify.js` (new), `mobile/src/licence/verify.test.js` (new),
  `mobile/package.json` (one Ed25519 dependency, on the owner's yes),
  `mobile/src/dependencies.test.js`.
- **Tests:** the S2.3 cases in JavaScript; the dependency test admits the one new dependency.
- **Command:** `cd mobile && npm test`.
- **Red step:** the tampered-byte case fails before the module exists.
- **Depends on:** S2.1; the owner's yes on the dependency. **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `mobile: the offline licence-key verifier`.

#### S2.6 One set of vectors for all three verifiers

- **Files:** `console/test/unit/licence-vectors.test.ts` (new),
  `mobile/src/licence/vectors.test.js` (new).
- **Interfaces:** consume `ee/licence/testdata/vectors.json`, including its negative vectors.
- **Tests:** every vector gives the same state in all three verifiers.
- **Command:** `cd console && npm run test:unit -- --maxWorkers=2 test/unit/licence-vectors.test.ts`
  and `cd mobile && npm test`.
- **Red step:** a vector whose expected state is wrong fails in both.
- **Depends on:** S2.3, S2.4, S2.5. **Reviewer:** standard.
- **Commit:** `licence: one set of vectors for the Go, console and app verifiers`.

#### S2.7 The console's key entry and hand-off

- **Files:** `console/src/main/licence-store.ts` (new), `console/src/renderer/screens/licence.ts`
  (new), `console/src/main/ipc.ts`, `console/src/preload/index.ts`,
  `console/test/unit/licence-store.test.ts` (new).
- **Interfaces:** a screen to paste a key; the key stored as S2.1 says; handed to the spawned CLI
  as S2.1 says, never in argv.
- **Tests:** `licence-store.test.ts` (a stored key survives a restart; a bad key is refused; the
  spawn argv never carries the key), `ipc.test.ts` and `preload-surface.test.ts` updated.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2
  test/unit/licence-store.test.ts test/unit/ipc.test.ts test/unit/preload-surface.test.ts`.
- **Red step:** the "argv never carries the key" case fails before the store exists.
- **Depends on:** S2.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `console: enter and keep the licence key`.

#### S2.8 The app's key entry

- **Files:** `mobile/src/licence/store.js` (new), `mobile/src/licence/store.test.js` (new),
  `mobile/src/licence/index.js`, and the settings screen S2.1 names.
- **Tests:** the key kept in the app's secure store; a bad key refused.
- **Command:** `cd mobile && npm test`.
- **Red step:** the bad-key case fails before the store exists.
- **Depends on:** S2.5. **Reviewer:** opus + mandatory independent review.
- **Commit:** `mobile: enter and keep the licence key`.

### Slice S3: the signing service, the key website and the admin queue

#### S3.1 Design and threat model (first)

- **Files:** `docs/design/key-signing-service.md` (new).
- **Content:** T13 item by item: where it runs and who operates it (U12), the key in a managed,
  non-exportable key store (U9), the service identity as the only signer, no agent identity with
  any grant, the checks (email verification, free-mail list, one per organisation by domain, the
  open-source check through the code host's public API and its limits, U11), the manual queue and
  owner notification, the admin page's authentication, rate limits, the issuance record and its
  retention, personal data and the privacy notice (S7.3), outage semantics (issuance only), where
  the code lives (`ee/keyservice/`, published, or the private repository: a security trade-off,
  T13 item 11) and where its infrastructure lives (never `deploy/terraform`, which is embedded in
  the CLI); **paid keys** from the merchant of record's purchase, renewal and cancellation events,
  signature-verified and idempotent, and Enterprise keys from the admin page (F42); **proof of
  control** for the open-source check (F47); uniform answers that never reveal whether a domain
  holds a key; single-use, expiring verification tokens; the admin page's authentication, step-up,
  CSRF defence, escaping, audit log and per-tier limits; abuse throttling within the site's CSP.
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** S2.1; the owner's rulings on T13. **Reviewer:** opus + mandatory independent
  review.
- **Commit:** `docs(design): the key signing service and its threat model`.

#### S3.2 The issuing service

- **Files:** `ee/keyservice/` (new: the HTTP handlers, the checks, the issuance record, tests),
  location per S3.1.
- **Interfaces:** consumes S2.3's claim encoding; signs through the key store's API only; produces
  a key by email to a verified address.
- **Tests:** `TestFreeMailDomainGoesToTheQueue`, `TestSecondFreeKeyForADomainGoesToTheQueue`,
  `TestOpenSourceCheckRefusesAPrivateOrNonOSIRepository`, `TestUnverifiedEmailMintsNothing`,
  `TestSignerIsCalledOnlyAfterEveryCheck`, `TestOpenSourceCheckNeedsProofOfControl`,
  `TestVerificationTokenSingleUse`, `TestAnswersDoNotRevealAnExistingKey`,
  `TestPurchaseEventNeedsAValidSignature`, `TestPurchaseEventIsIdempotent`,
  `TestRenewalReissuesAndCancellationStops`, with a fake key store, a fake code host and a fake
  merchant.
- **Command:** `go test -tags ee ./ee/keyservice/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestSignerIsCalledOnlyAfterEveryCheck` fails before the handler exists.
- **Depends on:** S3.1, S2.3. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the key issuing service`.

#### S3.3 The key website's forms

- **Files:** `website/src/pages/keys.astro` (new), `website/test/keys.mjs` (new).
- **Content:** the open-source form, the commercial free-seat form, the manual request form; each
  posts to the service; the privacy notice linked (S7.3). The CSP's `form-action 'self'` blocks a
  post to another origin (F44): its change, and the site tests that hold it, are the owner's
  approval before this task starts.
- **Tests:** `keys.mjs` (the three forms, the privacy link, no third-party load); the CSP tests
  updated only as the owner approved.
- **Command:** `cd website && npm run check`.
- **Red step:** `keys.mjs` asserts the three forms and the privacy link before the page exists.
- **Depends on:** S3.1; the owner's approval of the CSP change. **Reviewer:** opus + mandatory
  independent review (personal data, CSP).
- **Commit:** `website: the key request forms`.

#### S3.4 The admin page and the manual queue

- **Files:** `ee/keyservice/admin.go` (new), `ee/keyservice/queue.go` (new), their tests.
- **Tests:** `TestAdminRefusesAnyoneButTheOwner`, `TestAdminRefusesACrossSiteRequest`,
  `TestQueueEscapesApplicantText` (the page and the notification email), `TestEveryMintIsAudited`,
  `TestPerTierMintLimit`, `TestQueueNotifiesTheOwner`, `TestApprovalMintsOnce`.
- **Command:** `go test -tags ee ./ee/keyservice/ -run 'TestAdmin|TestQueue|TestApproval' -count=1`.
- **Red step:** `TestAdminRefusesAnyoneButTheOwner` fails before the handler exists.
- **Depends on:** S3.2. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the key service's admin page and manual queue`.

#### S3.5 The service's infrastructure

- **Files:** per S3.1, outside `deploy/terraform` (for example `ee/keyservice/terraform/`, new),
  with a `terraform`-tagged validate test beside it.
- **Tests:** `terraform validate` and a plan test that the key store's key is non-exportable and
  only the service's identity holds the signer role.
- **Command:** `go test -tags terraform ./ee/keyservice/... -count=1` (per S3.1).
- **Red step:** the identity test fails while any other member holds the signer role.
- **Depends on:** S3.1. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the key service's infrastructure`.

#### S3.6 Register Checks 170–179

- **Files:** `docs/gcp-live-checklist.md`, `docs/coordinator/README.md` (its numbers table, read by
  `TestDocsCoordinatorNumbersTableIsReadFromItsSources`).
- **Tests:** the numbers-table test and the register's own docs tests.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** adding `## Check 170` fails the numbers-table test until the README names the block.
- **Depends on:** S3.1, S6.1. **Reviewer:** standard.
- **Commit:** `docs: register Checks 170-179, the key service and the lapse state (owner-run)`.

#### S3.7 Deploy and live checks (OWNER-RUN)

- **What the owner does:** applies S3.5 in the project S3.1 names, sets the service's secrets,
  deploys S3.2/S3.4, and runs Checks 170–175. No agent performs any step.
- **Depends on:** S3.2, S3.3, S3.4, S3.5, S3.6, S7.3, S8.1, S8.3, S8.4, S8.5 (Check 179).

### Slice S4: the seat estimate

#### S4.1 `fugaro seats` (advisory)

- **Files:** `internal/cli/seats.go` (new), `internal/cli/seats_test.go` (new),
  `internal/cli/root.go`, `website/content/cli-reference.md` (F45).
- **Interfaces:** consumes `terraform.launchers` and `terraform.operators` (`localcfg.Terraform`)
  and run records' requesters over 90 days; produces a count with groups flagged as unknown size
  and `--json`; stores and sends nothing.
- **Tests:** `TestSeatsCountsLaunchersOperatorsAndRequesters`, `TestSeatsFlagsGroups`,
  `TestSeatsDeduplicatesPeople`, `TestSeatsReadsNinetyDays`.
- **Command:** `go test ./internal/cli/ -run 'TestSeats' -count=1`.
- **Red step:** `TestSeatsFlagsGroups` fails before the command exists.
- **Depends on:** S1.2 (both edit `internal/cli/root.go`); S1.1 says whether it is core or `ee/`
  (T11); T14 (who counts). **Reviewer:** standard.
- **Commit:** `cli: fugaro seats, an advisory seat estimate (D439)`.

#### S4.2 The console's seats panel

- **Files:** `console/src/renderer/screens/seats.ts` (new), `console/src/main/spawn.ts`,
  `console/src/main/ipc.ts`, `console/test/unit/seats.test.ts` (new).
- **Tests:** the estimate beside the key's seats, an overage warning, no blocking.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2
  test/unit/seats.test.ts test/unit/ipc.test.ts`.
- **Red step:** the overage case fails before the screen exists.
- **Depends on:** S4.1, S2.4, S6.2 (both edit `console/src/main/ipc.ts`). **Reviewer:** opus +
  mandatory independent review (a new spawned command).
- **Commit:** `console: seats used against the licence's seats, advisory`.

### Slice S5: roles (design only here)

#### S5.1 Design: viewer, operator and admin, system-wide

- **Files:** `docs/design/roles.md` (new).
- **Content:** T16: the identity each surface uses, what IAM, the database rules and the console
  enforce per role, which part is core and which `ee/`, the free viewer (D433), `fugaro pair`.
  Its build is a later plan of its own.
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** the owner's ruling on T16. **Reviewer:** opus + mandatory independent review.
- **Commit:** `docs(design): roles enforced system-wide (D446)`.

### Slice S6: the lapse state in the console and the app

#### S6.1 Design: read-only per feature

- **Files:** `docs/design/lapse-read-only.md` (new).
- **Content:** per view of the console (all fifteen operations are reads, F20) and per screen and
  action of the app, what read-only keeps (T12: the app's cancel through the `orchestrator`
  mailbox agent, logs, Spend, the coordinator's view), the banner, the no-key state and the
  upgrade from console 0.112.0 (F43), the free evaluator's expiry (F39); enforcement in the main
  process (the IPC handler and the spawn allow-list), never only in the renderer.
- **Tests:** none beyond the Docs suite (a design).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design).
- **Depends on:** S2.1; the owner's ruling on T12. **Reviewer:** opus + mandatory independent
  review.
- **Commit:** `docs(design): the read-only state after a lapse (D436)`.

#### S6.2 The console's lapse state

- **Files:** `console/src/main/licence-state.ts` (new), `console/src/main/ipc.ts`,
  `console/src/preload/index.ts`, `console/src/renderer/entry.ts`,
  `console/test/unit/licence-state.test.ts` (new).
- **Tests:** within grace everything works; after grace the paid operations S6.1 names are
  refused **in the main process** (a renderer call to `spend.report` or `coordinator.status` after
  grace is refused by the IPC handler); what S6.1 keeps stays; a console with no key behaves as
  S6.1 says.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2
  test/unit/licence-state.test.ts test/unit/ipc.test.ts test/unit/preload-surface.test.ts`.
- **Red step:** the after-grace main-process refusal fails before the state exists.
- **Depends on:** S6.1, S2.7 (both edit `ipc.ts` and `preload/index.ts`), S8.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `console: read-only after the grace period (D436)`.

#### S6.3 The app's lapse state

- **Files:** `mobile/src/licence/state.js` (new), `mobile/src/licence/state.test.js` (new), and
  the screens S6.1 names, `mobile/src/licence/index.js`.
- **Tests:** within grace everything works; after grace the app is read-only as S6.1 says; no key
  behaves as S6.1 says.
- **Command:** `cd mobile && npm test`.
- **Red step:** the after-grace case fails before the state exists.
- **Depends on:** S6.1, S2.8 (both edit `index.js`), S8.4. **Reviewer:** opus + mandatory independent review.
- **Commit:** `mobile: read-only after the grace period (D436)`.

### Slice S7: the website (figures stay private)

#### S7.1 The Community page's licence section

- **Files:** `website/src/pages/community.astro`, `website/test/faq-community.mjs`.
- **Change:** what D431–D433 settle, after counsel (D448).
- **Tests:** `faq-community.mjs`'s pins, updated.
- **Command:** `cd website && npm run check`.
- **Red step:** the updated pin fails first.
- **Depends on:** S8.4. **Reviewer:** standard.
- **Commit:** `website: the community page states the licence and the free seat`.

#### S7.2 The pricing page

- **Files:** `website/src/pages/pricing.astro` (new), `website/test/pricing.mjs` (new).
- **Change:** tiers and features; figures per the owner's ruling on T22 (none in the repository
  unless the owner rules otherwise); S0.1 scans the page.
- **Tests:** `pricing.mjs` (three tiers, the feature rows, no figure unless T22 allows one).
- **Command:** `cd website && npm run check` and `go test ./internal/cli/ -run TestDocsSubscriptions`.
- **Red step:** `pricing.mjs` asserts the three tiers before the page exists.
- **Depends on:** S0.4, the owner's ruling on T22. **Reviewer:** standard.
- **Commit:** `website: the pricing page (tiers and features)`.

#### S7.3 The privacy notice for key sign-up

- **Files:** `website/src/pages/privacy.astro`, `website/test/privacy.mjs`.
- **Change:** counsel's text for the data D440 collects (owner-approved copy only); the page's
  "nothing the Fugaro project operates" and `privacy.mjs` rule 5 ("The project operates no host
  the software talks to") restated for a project that runs an issuing service the software never
  calls (F44), with the owner's approval.
- **Tests:** `privacy.mjs`, its rule 5 kept as a scan of the software's sources.
- **Command:** `cd website && npm run check`.
- **Red step:** the new section's pin fails first.
- **Depends on:** S8.4. **Reviewer:** opus + mandatory independent review (legal copy).
- **Commit:** `website: the privacy notice covers key sign-up`.

#### S7.4 Publish (OWNER-RUN)

- **What the owner does:** the website preview and promote (`docs/website/preview-promote.md`).
- **Depends on:** S7.1, S7.2, S7.3, S3.3, S3.7.

### Slice S8: the owner's, counsel's and the accountant's steps (all OWNER-RUN)

- **S8.1 The entity.** The owner incorporates it with the accountant. Depends on: none.
- **S8.2 The copyright assignment.** Counsel's instrument, the accountant's mechanics (O5). Depends
  on: S8.1.
- **S8.3 The copyright-line commit** (an agent's commit on the owner's go after S8.2 is signed).
  **Files:** `LICENSE`, `ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md`,
  `LICENSES/LicenseRef-Fugaro-Enterprise.txt`, `internal/cli/docs_licensing_test.go` (the root
  licence's pinned hash in `TestDocsLicensingRootLicenceUnchanged`), in one commit. **Tests:**
  `TestDocsLicensingRootLicenceUnchanged` and the licence tests in `internal/eeboundary`.
  **Command:** `go test ./internal/eeboundary/ -count=1 && GOFLAGS=-p=2 go test ./internal/cli/
  -run Docs -count=1`. **Red step:** change the root licence's line; the hash test fails until its
  pin is updated with the owner's go. **Depends on:** S8.2, #1140 landed or closed.
  **Reviewer:** opus + mandatory independent review. **Commit:** `licence: the copyright passes to
  the licensing entity (D441, B9)`.
- **S8.4 Counsel.** Sends S0.2; counsel answers O3, words D433, confirms D432 and D436 and the
  post-lapse read-only grant (F37), reviews the privacy notice and the Terms amendments, sets the
  interest rate. **Its sign-off gates console 0.113.0 and the first mobile release (D448, T17).**
  Depends on: S0.2.
- **S8.5 The Paddle account** in the entity's name. Depends on: S8.1.
- **S8.6 The figures** (prices, discounts, the Enterprise floor, O1) in the private repository.
  Depends on: none.

## Parallel execution

**Edges** (from the Depends-on lines; owner rulings and external gates in brackets):
S0.2→S8.4; S8.4→S0.4, S6.2, S6.3, S7.1, S7.3, S3.7; S8.5→S3.7; S2.4→S2.7; S2.5→S2.8;
S2.7→S6.2; S2.8→S6.3; S6.2→S4.2; S0.4→S7.2; S1.1→S0.3, S1.2, S1.7, (S4.1);
S1.2→S1.3, S4.1; S1.3→S1.4; S2.3→S1.4, S2.6, S3.2; S1.4→S1.5; S1.5→S1.6; S2.2→S1.6;
S2.1→S2.2, S2.3, S2.4, S2.5, S3.1, S6.1; S2.4→S2.6, S4.2; S2.5→S2.6; S3.1→S3.2,
S3.3, S3.5, S3.6; S3.2→S3.4; S6.1→S3.6, S6.2, S6.3; S4.1→S4.2; S3.2, S3.3, S3.4, S3.5, S3.6,
S7.3, S8.1, S8.3→S3.7; S8.1→S8.2, S8.5; S8.2→S8.3; S3.7, S7.1, S7.2, S7.3, S3.3→S7.4.
[T10, T11, T20, T21]→S1.1; [T12]→S6.1; [T13]→S3.1; [T16]→S5.1; [T22]→S7.2; [O4]→S2.1;
[T23]→S0.5; [T14]→S4.1; [CSP approval]→S3.3; [#1140]→S0.3, S0.4, S8.3.

| Level | Tasks (run concurrently) |
| --- | --- |
| 0 | S0.1, S0.2, S0.5, S1.1, S2.1, S5.1; owner: S8.1, S8.6 |
| 1 | S0.3, S1.2, S1.7, S2.2, S2.3, S2.4, S2.5, S3.1, S6.1; owner/counsel: S8.2, S8.4, S8.5 |
| 2 | S0.4, S1.3, S2.6, S2.7, S2.8, S3.2, S3.3, S3.5, S3.6, S4.1, S7.1, S7.3, S8.3 |
| 3 | S1.4, S3.4, S6.2, S6.3, S7.2 |
| 4 | S1.5, S4.2, S3.7 (owner) |
| 5 | S1.6, S7.4 (owner) |

**Critical path:** two chains of six levels each, both behind owner rulings:
**S1.1 → S1.2 → S1.3 → S1.4 → S1.5 → S1.6** (the moves and the `ee` release; gated by T10, T11,
T21) and **S2.1 → S3.1 → S3.2 → S3.4 → S3.7 → S7.4** (the signing service live; gated by O4,
T13, the entity S8.1 and counsel S8.4). **The release-blocking path is shorter but external:
S2.1 → S2.4 → S2.7 → S6.2 (and S2.5 → S2.8 → S6.3), with S0.2 → S8.4 (counsel) joining at
S6.2/S6.3 → console 0.113.0 and the first mobile release.**

**What must be sequential and why:** S1.4 before S1.5 and S1.2 before S4.1 (all edit
`internal/cli/root.go`); S1.2 → S1.3 (the registry is the interface `ee/register` fills); S2.3
before S3.2 and S1.4 (the claim encoding and the key gate); S3.2 before S3.4 (one package);
S8.2 before S8.3 (the assignment must be signed before the copyright line changes); S2.7 → S6.2
→ S4.2 and S2.8 → S6.3 (one file each: `ipc.ts`, `index.js`).

### Shared files (no level holds two writers of one file)

| File | Writers (level) |
| --- | --- |
| `internal/cli/root.go` | S1.2 (1), S4.1 (2), S1.4 (3), S1.5 (4) |
| `website/content/cli-reference.md` | S4.1 (2), S1.4 (3), S1.5 (4) |
| `ee/register/register.go` | S1.3 (2), S1.4 (3), S1.5 (4) |
| `SECURITY.md` | S0.5 (0) |
| `internal/cli/docs_licensing_test.go` | S8.3 (2) |
| `internal/cli/docs_desktop_console_test.go` | S1.5 (4) |
| `.claude/rules/ee.md`, `LICENSING.md`, `ee/README.md` | S0.3 (1) |
| `.goreleaser.yaml` | S2.2 (1), S1.6 (5) |
| `docs/licensing/README.md` | S2.1 (0) |
| `docs/subscriptions/README.md` | S0.2 (0) |
| `docs/licensing/subscription-terms.md` | S0.4 (2) |
| `internal/cli/docs_ee_reserved_test.go` | S0.3 (1) |
| `docs/coordinator/README.md` | S3.6 (2), S1.4 (3) |
| `docs/gcp-live-checklist.md` | S3.6 (2) |
| `console/src/main/ipc.ts` | S2.7 (2), S6.2 (3), S4.2 (4) |
| `console/src/main/spawn.ts` | S4.2 (4) |
| `console/src/preload/index.ts` | S2.7 (2), S6.2 (3) |
| `console/src/renderer/entry.ts` | S6.2 (3) |
| `console/src/renderer/screens/spend.ts` | S1.5 (4) |
| `mobile/package.json`, `mobile/src/dependencies.test.js` | S2.5 (1) |
| `mobile/src/licence/index.js` | S2.5 (1), S2.8 (2), S6.3 (3) |
| `mobile/src/licence/` (other files) | S2.5 (1, `verify*`), S2.6 (2, `vectors.test.js`), S2.8 (2, `store*`), S6.3 (3, `state*`): distinct files |
| `ee/licence/` | S2.3 (1) |
| `ee/keyservice/` | S3.2 (2), S3.5 (2, `terraform/` only), S3.4 (3) |
| `plugin/skills/working/SKILL.md` | S1.5 (4) |
| `website/src/pages/*`, `website/test/*` | S3.3 (2, `keys` and the CSP tests the owner approves), S7.1 (2, `community`, `faq-community`), S7.3 (2, `privacy`), S7.2 (3, `pricing`): distinct files, provided S3.3's CSP change does not touch the privacy page or test |
| The five licence files | S8.3 (2) |

S3.2 and S3.5 both write under `ee/keyservice/` in level 2, in disjoint paths (`terraform/` is
S3.5's alone); if S3.1 puts them in one Go package, S3.5 moves to level 3.

## Checks this plan takes (170–179)

All OWNER-RUN, none run. Check 176 measures the views S6.1 keeps (the console has no launch to
gate today, F20). S3.6 writes them into `docs/gcp-live-checklist.md`.

| Check | Measures | Runs after |
| --- | --- | --- |
| 170 | A key minted for a verified work-email domain verifies in the CLI (`ee` build), the console and the app with the network off | S3.7 |
| 171 | A free-mail domain, and a second free key for the same organisation's domain, go to the manual queue, not to a key | S3.7 |
| 172 | The open-source check refuses a private or non-OSI repository and accepts an all-public OSI one | S3.7 |
| 173 | The admin page refuses an unauthenticated visitor and any account but the owner's; a queued request notifies the owner; approval mints one key | S3.7 |
| 174 | The signing key cannot be exported, and no identity but the service's can sign (an agent's identity is refused) | S3.7 |
| 175 | With the issuing service stopped, every valid key keeps working; only new keys fail | S3.7 |
| 176 | In the packed console: a key within grace works fully; after grace the console keeps only the views S6.1 keeps, refused in the main process; with no key it behaves as S6.1 says; the MIT CLI still launches and cancels | S6.2 and a console release |
| 177 | The same on a device with the app | S6.3 and an app build |
| 178 | `fugaro version` and the console's About show the release date of a release build | S2.2, S2.4 and a release |
| 179 | A purchase in the merchant of record's test mode mints a Team key by email; a renewal reissues it; a cancellation stops the reissue | S3.7 |

## Release actions

- The signing service's deploy (S3.7) is the licensor's, not an operator's: it has no
  `### For operators` entry, and its runbook is S3.1's.
- The release that sets the `ee` tag (S1.6) writes an operator entry if S1.1 moves the sweep's
  image (T10): the installation's coordinator job needs the new image, per installation
  (`fugaro update`).
- A console or app release carrying S6.2 or S6.3 states the lapse behaviour in its release notes.
