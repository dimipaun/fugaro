# Fugaro portal: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (findings F1–F24,
decisions D2180–D2195).

## Status

**Plan only. Nothing is implemented. No task is approved**: each task needs the owner's own explicit go,
by name. Answering a decision is not approving a task.

The owner's answers that shape this plan (2026-11-12): the backend is Go and extends `ee/keyservice`
(D2180); the key is the v2 bundle and a seat change reissues a successor that `licence refresh` fetches
(D2181); a key is never stored, and "reveal" rotates it (D2182); #2457 and #2458 merge as planned
(D2183); the free seat is minted at the first workspace (D2184); trials and open source stay, in the
portal (D2185); admins and then operators by grant order hold the seats (D2186, held with K3.4); the
operations organisation's owners and admins mint after a fresh sign-in (D2187); the SPA reaches data
through the API only (D2188); resellers accrue daily against a credit limit (D2189) and owe Fugaro
(D2190); resellers create their customers' organisations (D2191); one Paddle subscription per workspace
(D2192); the SPA lives in `ee/portal/` (D2193); everything runs in `fugaro-prod`, staging in
`fugaro-staging` (D2194); a sole owner cannot delete their account (D2195).

## Conventions every part follows

- `CLAUDE.md` and [.claude/rules/ee.md](../../.claude/rules/ee.md): every file under `ee/` starts with
  the two-line SPDX notice; a JSON file gets a sibling `.license` file (`TestEEFilesCarryTheNotice`);
  nothing outside `ee/` imports `ee/` (`go test ./internal/eeboundary/ -count=1` after each Go task).
- **No agent deploys, operates or holds a signing key for the service** (ee.md, R5 as amended by D440).
  Every deploy, project, provider account and signing key is OWNER-RUN (slice P0), and the scripts say
  so.
- Hermetic tests: no network, no real cloud, no home directory. The Firestore adapter's tests run under
  a new `firestore` build tag against the emulator and never in default CI; everything else runs on the
  in-memory store. Paddle, Resend and the minter are `httptest` fakes.
- No secret-shaped literal in a test, fixture or doc: a Paddle webhook secret, a renewal secret and a
  Resend key are built at test time.
- Config and wire formats decode strictly. The API's shapes are JSON Schemas in `ee/portal/schemas/`
  (F20); adding a field is allowed, and a changed meaning gets a new name.
- Never edit `.github/workflows/`; the portal's CI job is staged under `docs/pending-workflows/` (P9.2).
- Comments explain why. No TODO comments.
- New dependencies (F24), each added by one task: `cloud.google.com/go/firestore` (P1.2), `github.com/getsentry/sentry-go` (P1.6); in
  `ee/portal/package.json`: `react`, `react-dom`, `react-router-dom`, `firebase`, `@sentry/react`,
  `@paddle/paddle-js`, `vite`, `@vitejs/plugin-react`, `typescript`, `vitest`, `@testing-library/react`,
  `jsdom`, `ajv` (P1.4). Nothing else without asking.
- Targeted tests only: each task names its `-run` filter or its test file.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| Operations and reseller organisations refuse any provider but `google.com` and `microsoft.com`, on every request | P1.3 `TestEmailLinkIsRefusedForOperations`, P2.1 `TestEmailLinkUserIsRefusedInAResellerOrg` |
| An invitation is accepted only by its own verified address | P2.2 `TestInvitationIsBoundToItsAddress` |
| No key secret is stored or mailed, ever | P3.2 `TestNoKeyIsStored`, P2.2 `TestResendMailerSendsNoKey` |
| A reveal rotates, needs a fresh sign-in, and tells the other owners | P3.2 |
| The API never signs; only the minter's identity can | P1.5 `TestOnlyTheMinterCanSign`, Check 571 |
| A minted claim beyond the tier's cap is refused (D454) | P5.1 `TestMintIsCappedPerTier` |
| Every operations action, reads included, is audited | P2.1 `TestOperationsReadsAreAudited`, P5.1 `TestEveryOperationsActionIsAudited` |
| A Paddle event is stored as received before it is processed, and a redelivery changes nothing | P4.2 |
| A reseller sees only its customers and never a secret | P6.1 |
| Two concurrent reseller mints cannot pass the credit limit together | P6.2 `TestConcurrentMintsCannotExceedTheLimit` |
| Firestore rules deny every client access | P1.5 `TestFirestoreRulesDenyClients` |
| No analytics request before consent; Sentry sends no personal data | P1.4 |
| A staging key never verifies in production | existing `TestStagingKeyIDIsRefused`; Check 574 |

## Limits this plan accepts

- A reveal rotates a live key: an installation without a renewal job (TN5 of licence-key-v2) must run
  `fugaro licence set` again with the new bundle after 24 hours.
- The operations second factor is the identity provider's MFA, which Fugaro cannot see (D2187).
- One project (D2194): the isolation of the signer rests on IAM inside `fugaro-prod`, measured by
  Check 571, not on a project boundary.
- The free seat's abuse control is the terms (D2184).

## Slices and tasks

Every task's reviewer tier is **opus + mandatory independent review** where it says SECURITY, otherwise
standard. Each task's Red step is its first named test, written first and failing because the code it
tests does not exist yet, unless the task says otherwise.

### Slice P0: owner-run (no agent performs any step)

- **P0.1 Projects and Firebase. OWNER-RUN.** In `fugaro-prod` and `fugaro-staging`: enable Auth's
  Google, Apple (a Services ID in the Apple developer account), Microsoft (an app registration) and email
  link providers; add `app.fugaro.dev` (staging: a staging host) to the authorised domains; create the
  `portal` hosting site and its DNS record. Depends on: none.
- **P0.2 Provider accounts. OWNER-RUN.** Paddle sandbox and live (subscriptions S8.5, licence-key-v2
  K5.2): one product per policy row, the webhook pointed at `https://app.fugaro.dev/api/paddle/webhook`;
  Resend and its DNS records (staging uses Resend's test mode and Paddle's sandbox only); the Sentry
  project and its DSN; the analytics property. Each secret goes into
  Secret Manager by the owner: `gcloud secrets versions add <name> --data-file=-` with the names P1.5
  creates. Depends on: P1.5 applied (P0.4).
- **P0.3 Counsel. OWNER-RUN.** The terms of service and licence terms the portal records acceptance of,
  reseller invoicing and sales tax, the privacy notice (Firestore personal data, analytics, Sentry), D457
  retention. Gates P0.5's production deploy, not any task.
- **P0.4 Apply the Terraform. OWNER-RUN.** `terraform -chdir=ee/keyservice/terraform/keyring apply
  -var project=fugaro-staging` then `services`; then the same for `fugaro-prod`. If a `fugaro-licence` ring
  exists (UNVERIFIED), destroy its key versions in the console; the ring itself cannot be deleted.
  Depends on: P1.5.
- **P0.5 Deploy and seed. OWNER-RUN.** `scripts/deploy-portal.sh --project fugaro-staging --dry-run`, then
  without `--dry-run`; Check 570–575 on staging; then production. Seed the operations organisation:
  `scripts/deploy-portal.sh --seed-operations <owner address> --project <p>`. Depends on: P9.2, P2.1, P0.4.

### Slice P1: foundations (inert alone)

- **P1.0 The API mount. SECURITY.** Files: `ee/keyservice/api.go` (new), `ee/keyservice/service.go`
  (`Handler` mounts the `/api/` registry), `ee/keyservice/api_test.go` (new). Interfaces produced:
  `registerAPI(method, path string, h apiHandler)`, called from each area file's `init`, so no later task
  edits `service.go`; one JSON error shape; a body cap per route; no CORS (Hosting rewrites `/api/**` to
  the service, same origin). Tests: `TestAPIRoutesComeFromTheRegistry`, `TestAPIErrorsAreUniform`,
  `TestAPIBodyIsCapped`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestAPI' -count=1`. Depends on:
  #2457 merged (one writer of `service.go`). Commit: `ee: the portal API's route registry`.
- **P1.1 Wire schemas and Go types.** Files: `ee/portal/schemas/*.schema.json` (new: organisation,
  membership, invitation, workspace, key metadata, reveal bundle, checkout context, subscription mirror,
  ledger entry, statement, report rows, audit entry, error), each with a `.license` file;
  `ee/portal/schemas/fixtures/*.json` (new); `ee/keyservice/wire.go`, `wire_test.go` (new). Tests:
  `TestWireFixturesDecodeStrictly`, `TestWireRefusesUnknownFields`, `TestRevealBundleIsTheOnlySecretShape`.
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestWire|TestRevealBundle' -count=1`. Depends on: none.
  Commit: `ee: the portal's wire schemas`.
- **P1.2 The document store.** Files: `ee/keyservice/docstore/` (new package: `docs.go` the interface with
  get, put, query and a transaction; `memory.go`; `firestore.go`; `conformance_test.go` run on memory;
  `firestore_test.go` with `//go:build firestore` running the same suite on the emulator), `go.mod`,
  `go.sum`; `ee/keyservice/firestore_store.go` (new: the existing `Store` interface on `docstore`).
  Tests: `TestDocstoreConformance`, `TestTransactionRetriesOnConflict`, `TestStoreOnDocstoreConformance`.
  Command: `go test ./ee/keyservice/... -tags ee -run 'TestDocstore|TestTransaction|TestStoreOnDocstore' -count=1`;
  the emulator half `go test -tags 'ee firestore' ./ee/keyservice/docstore/ -count=1` (needs the emulator,
  never in default CI). Depends on: none. Commit: `ee: a document store on Firestore`.
- **P1.3 Sign-in for the API. SECURITY.** Files: `ee/keyservice/portalauth.go`, `portalauth_test.go` (new).
  Verifies Firebase ID tokens of the deploying project by reusing `ee/coordinator/mcpauth/firebase.go`
  (audience, issuer, `email_verified` pinned); answers the caller's address (folded as `access.Member`
  folds it), provider and `auth_time`; `RequireSSO(orgType)` and `FreshWithin(5*time.Minute)`; verifies
  Cloud Scheduler's OIDC token for the job endpoints (P6.3, P7.1). Licence-key-v2's K2.4 consumes this
  verifier instead of writing its own (P9.1). Tests: `TestEmailLinkIsRefusedForOperations` (both
  `password` and `emailLink`, UNVERIFIED which one Firebase sends), `TestTokenFromAnotherProjectIsRefused`,
  `TestUnverifiedEmailIsRefused`, `TestStaleSignInIsRefusedForMint`, `TestSchedulerTokenFromAnotherAccountIsRefused`.
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestEmailLink|TestTokenFrom|TestUnverified|TestStale|TestSchedulerToken' -count=1`.
  Depends on: none. Commit: `ee: the portal API verifies sign-in and its provider`.
- **P1.4 The SPA skeleton.** Files (all new, under `ee/portal/`): `package.json`, `package-lock.json`
  (each with `.license`), `vite.config.ts`, `tsconfig.json` (+ `.license`), `index.html`, `src/main.tsx`,
  `src/app.tsx`, `src/routes.ts` (collects `src/screens/*/route.tsx` with `import.meta.glob`, so later
  tasks add folders and edit no shared file), `src/api/client.ts`, `src/api/types.ts`, `src/auth/*`,
  `src/telemetry.ts` (Sentry: no personal data, no replay, DSN from the build environment, absent means
  off), `src/consent.ts` (analytics only after consent, the site's D93 rule), `src/styles.css` importing
  `website/src/styles/tokens.css`, `src/screens/signin/*`, `test/*`. Tests: `types.test.ts` (every
  fixture of P1.1 validates with ajv and type-checks), `signin.test.tsx` (four providers offered),
  `consent.test.ts` (no analytics request before consent), `telemetry.test.ts` (no user fields sent).
  Command: `cd ee/portal && npm ci && npx vitest run && npx tsc --noEmit`. Depends on: P1.1.
  Commit: `ee: the portal app's skeleton and sign-in`.
  Layouts are mobile-first and work at a 360px width (the spec's "responsive").
- **P1.6 Backend error reporting.** Files: `ee/keyservice/errreport.go`, `errreport_test.go` (new),
  `ee/keyservice/api.go` (wraps the registry's handlers), `go.mod`, `go.sum` (`github.com/getsentry/sentry-go`).
  Panics and 5xx answers go to Sentry with no request body, header or address; no DSN means off. Tests:
  `TestPanicIsReportedWithoutTheBody`, `TestNoDSNReportsNothing`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestPanicIs|TestNoDSN' -count=1`.
  Depends on: P1.0, P1.2 (one writer of `go.mod` per level). Commit: `ee: the portal API reports errors to Sentry`.
- **P1.5 Infrastructure. SECURITY.** Files: `ee/keyservice/terraform/keyring/*.tf` (the default project
  becomes the variable's required value; one key id per apply, so staging's ring holds only the staging
  key), `ee/keyservice/terraform/services/*.tf` (the Firestore database `portal`, the API service, the
  minter, the secrets' empty containers, `scheduler.tf` with the statement and reminder jobs **paused**,
  the minter's signer grant on the ring only), `ee/portal/firestore.rules` (+ `.license`: deny all),
  `ee/keyservice/terraform/infra_test.go`. Tests: `TestOnlyTheMinterCanSign`, `TestNoProjectLevelKMSRole`,
  `TestStagingRingHoldsOnlyTheStagingKey`, `TestFirestoreRulesDenyClients`, `TestNoServiceRunsAsADefaultAccount`.
  Command: `go test -tags 'ee terraform' ./ee/keyservice/terraform/ -count=1`. Depends on: none.
  Commit: `ee: the portal's infrastructure in one project (D2194)`.

### Slice P2: tenancy

- **P2.1 Organisations and memberships. SECURITY.** Files: `ee/keyservice/orgs.go`, `api_orgs.go`,
  `audit.go`, `orgs_test.go` (new). Sign-up creates a customer organisation and records the terms'
  version and time; types operations, customer, reseller; roles owner, admin, member in membership
  documents; last-used organisation; operations reads across organisations, each audited; the SSO rule per
  organisation type on every request. Tests: `TestSignUpRecordsTermsAcceptance`, `TestRolesLiveInMemberships`,
  `TestEmailLinkUserIsRefusedInAResellerOrg`, `TestOperationsReadsAreAudited`, `TestLastUsedOrg`,
  `TestOnlyOperationsCreatesTheOperationsOrg`. Command: `go test ./ee/keyservice/ -tags ee -run
  'TestSignUp|TestRoles|TestEmailLinkUser|TestOperationsReads|TestLastUsed|TestOnlyOperationsCreates' -count=1`.
  Depends on: P1.0, P1.1, P1.2, P1.3. Commit: `ee: portal organisations and memberships`.
- **P2.2 Invitations and mail. SECURITY.** Files: `ee/keyservice/invites.go`, `api_invites.go`, `resend.go`
  (a `Mailer` on Resend's HTTP API), tests (new). Tests: `TestInvitationIsBoundToItsAddress`,
  `TestInvitationIsSingleUse`, `TestOperationsInviteOutsideTheDomainListIsRefused`, `TestResellerInviteNeedsSSO`,
  `TestResendMailerSendsNoKey`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestInvitation|TestOperationsInvite|TestResellerInvite|TestResend' -count=1`.
  Depends on: P2.1. Commit: `ee: portal invitations by email`.
- **P2.3 Tenancy screens.** Files: `ee/portal/src/screens/{orgs,members,settings,sso}/*`, tests (new).
  The switcher, the "SSO required" dialog with the Google and Microsoft buttons, members and invitations,
  organisation settings. Tests: `sso.test.tsx` (selecting an operations organisation while signed in by
  email opens the dialog), `members.test.tsx`. Command: `cd ee/portal && npx vitest run src/screens/{orgs,members,settings,sso}`.
  Depends on: P1.4. Commit: `ee: the portal's organisation screens`.
- **P2.4 Deletion. SECURITY.** Files: `ee/keyservice/deletion.go`, `api_account.go`, tests (new);
  `ee/portal/src/screens/account/*` (new). Tests: `TestSoleOwnerCannotDeleteTheirAccount`,
  `TestOrgDeletionRevokesAndCancels`, `TestSalesRecordsLoseTheirPersonalFields`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestSoleOwner|TestOrgDeletion|TestSalesRecords' -count=1`
  and `cd ee/portal && npx vitest run src/screens/account`. Depends on: P2.1, P3.1, P4.3.
  Commit: `ee: self-serve account and organisation deletion (D2195)`.

### Slice P3: workspaces and keys

- **P3.1 Workspaces and the free seat. SECURITY.** Files: `ee/keyservice/workspaces.go`,
  `api_workspaces.go`, tests (new); `ee/keyservice/policy.go` (a `free-seat` row: Team, one seat,
  `kind: free`, auto renewal, renewed while the organisation exists). A workspace records its Firebase
  project id (the key's `workspace_id`, TN2). Tests: `TestFirstWorkspaceMintsTheFreeSeat`,
  `TestSecondWorkspaceGetsNoFreeSeat`, `TestFreeSeatIsOneSeatTeam`, `TestWorkspaceIDIsAFirebaseProjectID`.
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestFirstWorkspace|TestSecondWorkspace|TestFreeSeat|TestWorkspaceID' -count=1`.
  Depends on: P2.1. Commit: `ee: portal workspaces and the free seat (D2184)`.
- **P3.2 Reveal. SECURITY.** Files: `ee/keyservice/reveal.go`, `api_keys.go`, tests (new). Reveal issues a
  fresh bundle through the minter (the K2.3 rotation, with the 24-hour overlap) and returns it once; key
  metadata for every member. A key with no renewal secret (air-gapped, D2138) is re-signed with the same
  claims on reveal and offered as a downloadable licence file. Tests: `TestAirGappedRevealIsAFile`, `TestNoKeyIsStored`, `TestRevealRotatesTheSecret`,
  `TestOnlyOwnersAndAdminsReveal`, `TestRevealNeedsAFreshSignIn`, `TestRevealNotifiesOtherOwners`,
  `TestResellerAndOperationsSeeMetadataOnly`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestNoKeyIs|TestReveal|TestOnlyOwners|TestResellerAndOperations' -count=1`.
  Depends on: P3.1, #2457. Commit: `ee: a key is revealed by rotating it (D2182)`.
- **P3.3 Trial and open source in the portal. SECURITY.** Files: `ee/keyservice/api_trial.go`,
  `api_opensource.go`, tests (new). The trial reuses K2.7's rule; the open-source request reuses
  `checks.go`'s proof check and `queue.go`'s manual queue, decided by operations in P5.1's screens.
  Tests: `TestTrialFromThePortalIsOnePerWorkspace`, `TestOpenSourceRequestQueuesForOperations`.
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestTrialFromThePortal|TestOpenSourceRequest' -count=1`.
  Depends on: P3.1, K2.7 (licence-key-v2, not yet approved). Commit: `ee: trials and open-source requests in the portal (D2185)`.
- **P3.4 Workspace screens.** Files: `ee/portal/src/screens/workspaces/*`, tests (new). Workspaces with key
  metadata, seats and renewal date; reveal with copy; after a purchase or seat change, the line
  "run `fugaro licence refresh`"; trial and open-source buttons. Tests: `workspaces.test.tsx`
  (the bundle is shown once and cleared on navigation; the refresh hint appears after a seat change).
  Command: `cd ee/portal && npx vitest run src/screens/workspaces`. Depends on: P1.4.
  Commit: `ee: the portal's workspace screens`.

### Slice P4: Paddle

- **P4.1 Checkout. SECURITY.** Files: `ee/keyservice/checkout.go`, `api_checkout.go`, tests (new);
  `ee/portal/src/screens/checkout/*` (new, Paddle.js overlay). The API records the licence terms'
  acceptance and returns the checkout context with `custom_data` `{org_id, workspace_id}`. Tests:
  `TestCheckoutNeedsLicenceTermsAccepted`, `TestCustomDataCarriesOrgAndWorkspace`, `checkout.test.tsx`.
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestCheckout|TestCustomData' -count=1` and
  `cd ee/portal && npx vitest run src/screens/checkout`. Depends on: P3.1, P1.4, #2458.
  Commit: `ee: Paddle checkout from the portal`.
- **P4.2 Events as received, and the ledger. SECURITY.** Files: `ee/keyservice/merchant.go` (store the raw
  body and signature header before `apply`), `ledger.go`, tests (new). The purchase email carries a portal
  link, never a key (F3). Tests: `TestEventIsStoredAsReceivedBeforeProcessing`, `TestRedeliveryDoesNotDoubleCount`,
  `TestLedgerDerivesFromStoredEvents`, `TestEventWithoutAnOrgIsParkedNotDropped`, `TestPurchaseMailCarriesNoKey`.
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestEventIs|TestRedelivery|TestLedger|TestEventWithout|TestPurchaseMail' -count=1`.
  Depends on: P1.2, #2458. Commit: `ee: Paddle events kept as received, and the sales ledger`.
- **P4.3 Billing mirror, seats and the customer portal. SECURITY.** Files: `ee/keyservice/paddleclient.go`,
  `api_billing.go`, tests (new). Mirrors subscription state from events; "buy more seats" updates the
  subscription's quantity through Paddle's API (the event then reissues, K2.5); a customer-portal link per
  organisation; cancellation for P2.4. Tests: `TestBuySeatsUpdatesTheQuantity`, `TestCustomerPortalLinkIsPerOrg`,
  `TestOnlyOwnersAndAdminsBuy`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestBuySeats|TestCustomerPortal|TestOnlyOwnersAndAdminsBuy' -count=1`.
  Depends on: P4.2, P3.1. Commit: `ee: billing state, seat purchases and Paddle's customer portal`.
- **P4.4 Billing screens.** Files: `ee/portal/src/screens/billing/*`, tests (new). Command:
  `cd ee/portal && npx vitest run src/screens/billing`. Depends on: P1.4. Commit: `ee: the portal's billing screens`.

### Slice P5: operations

- **P5.1 Operations API. SECURITY.** Files: `ee/keyservice/ops.go`, `api_ops.go`, tests (new). Look up any
  organisation, its workspaces, keys (metadata), purchases and members; mint (any seats, expiry and
  refresh policy), extend, change seats, revoke, comp (`kind: comped`); decide the open-source queue;
  owners and admins only, sign-in within 5 minutes, D454's caps, the operations domain list (D2187).
  Supersedes licence-key-v2 K2.6. Tests: `TestOperationsMemberCannotMint`, `TestMintNeedsAFreshSignIn`,
  `TestMintIsCappedPerTier`, `TestEveryOperationsActionIsAudited`, `TestOperationsNeverSeesASecret`.
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestOperationsMember|TestMintNeeds|TestMintIsCapped|TestEveryOperations|TestOperationsNever' -count=1`.
  Depends on: P2.1, P3.1. Commit: `ee: the operations API (D2187)`.
- **P5.2 Reports.** Files: `ee/keyservice/reports.go`, `api_reports.go`, tests (new). Monthly per customer,
  by channel, and each reseller's receivable (D2190); comped and free keys are not revenue. Tests:
  `TestReportSplitsDirectAndReseller`, `TestReportMonthsAreUTC`, `TestCompedAndFreeKeysAreNotRevenue`,
  `TestResellerReceivableMatchesItsStatements`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestReport|TestComped|TestResellerReceivable' -count=1`.
  Depends on: P4.2, P6.3. Commit: `ee: monthly sales reports`.
- **P5.3 Operations screens.** Files: `ee/portal/src/screens/ops/*`, tests (new): lookup, key actions, the
  queue, the ledger, the audit log, reports. Command: `cd ee/portal && npx vitest run src/screens/ops`.
  Depends on: P1.4. Commit: `ee: the portal's operations screens`.

### Slice P6: resellers

- **P6.1 Reseller organisations and customers. SECURITY.** Files: `ee/keyservice/resellers.go`,
  `api_resellers.go`, tests (new). Operations creates a reseller organisation with its wholesale rates and
  credit limit; a reseller creates a customer organisation and invites its owner; one reseller per
  customer; operations relinks. Tests: `TestOnlyOperationsCreatesAResellerOrg`, `TestResellerSeesOnlyItsCustomers`,
  `TestResellerNeverSeesASecret`, `TestRelinkIsAudited`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestOnlyOperationsCreatesAReseller|TestResellerSees|TestResellerNever|TestRelink' -count=1`.
  Depends on: P2.2, P5.1. Commit: `ee: resellers and their customers (D2191)`.
- **P6.2 Reseller minting and accrual. SECURITY.** Files: `ee/keyservice/accrual.go`,
  `api_reseller_keys.go`, tests (new). Mint and seat changes for the reseller's own customers; daily accrual;
  the credit check in one transaction (D2189). Tests: `TestAccrualIsSeatsTimesTheDailyRate`,
  `TestMintBeyondTheCreditLimitIsRefused`, `TestRevokedKeyStopsAccruing`, `TestConcurrentMintsCannotExceedTheLimit`.
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestAccrual|TestMintBeyond|TestRevokedKeyStops|TestConcurrentMints' -count=1`.
  Depends on: P6.1, P3.1. Commit: `ee: reseller minting against a credit limit (D2189)`.
- **P6.3 Monthly statements.** Files: `ee/keyservice/statements.go`, `api_statements.go`, tests (new). A
  Scheduler-called endpoint closes the month, writes each reseller's statement, mails it; operations marks
  payment. Tests: `TestStatementSumsTheMonth`, `TestStatementRunIsIdempotent`, `TestStatementEndpointRefusesOtherCallers`,
  `TestPaymentIsRecordedByOperationsOnly`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestStatement|TestPaymentIs' -count=1`.
  Depends on: P6.2, P2.2. Commit: `ee: monthly reseller statements`.
- **P6.4 Reseller screens.** Files: `ee/portal/src/screens/reseller/*`, tests (new). Command:
  `cd ee/portal && npx vitest run src/screens/reseller`. Depends on: P1.4. Commit: `ee: the portal's reseller screens`.

### Slice P7: reminders

- **P7.1 Renewal reminders.** Files: `ee/keyservice/reminders.go`, `api_reminders.go`, tests (new). A
  Scheduler-called endpoint mails owners and admins before a key that does not renew through Paddle runs
  out (reseller, comped, open-source keys); once per term. Tests: `TestReminderIsSentOncePerTerm`,
  `TestPaddleRenewedKeysGetNoReminder`, `TestReminderEndpointRefusesOtherCallers`. Command:
  `go test ./ee/keyservice/ -tags ee -run 'TestReminder|TestPaddleRenewed' -count=1`. Depends on: P3.1, P2.2.
  Commit: `ee: renewal reminders`.

### Slice P9: docs, hosting and retirement

- **P9.1 Amend licence key v2's plan.** Files: `docs/licence-key-v2/implementation-plan.md`,
  `docs/licence-key-v2/reconciliation.md`, `docs/design/key-signing-service.md` (dated notes only):
  K2.4 uses P1.3's verifier and its pages are the SPA's; K2.6 is superseded by P5.1; K5.1 by the portal's
  screens; K2.5's mail carries a link (F3); K3.4 follows D2186 and adds a `since` field to the grant node
  (F8); design §3.4's `fugaro-licence` becomes `fugaro-prod` (D2194) and §6's owner-only page is replaced
  (D2187). Tests: `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`. Depends on: none.
  Commit: `docs: licence key v2 and the key-signing design follow the portal`.
- **P9.2 Hosting, deploy and CI. SECURITY.** Files: `firebase.json` (two hosting targets, `website` and
  `portal`; the portal's CSP admits Paddle's and Firebase Auth's origins; `/api/**` rewrites to the API
  service), `scripts/deploy-website.sh` (`--only hosting:website`), `scripts/deploy-portal.sh` (new;
  OWNER-RUN; dry-run first; refuses an agent session; `--seed-operations`), `website/test/origins.mjs`
  (the website's CSP unchanged), `ee/portal/test/hosting.test.ts` (new: the portal target's CSP and
  rewrite), `docs/pending-workflows/portal-tests.yml` (new) and its section in
  `docs/pending-workflows/README.md`. Command: `cd website && node --test test/origins.mjs` and
  `cd ee/portal && npx vitest run test/hosting.test.ts`. Depends on: P1.4. Commit: `ee: the portal's hosting target and deploy script`.
- **P9.3 Retire the forms and the admin page. SECURITY.** Files: `ee/keyservice/service.go` (the
  `/free-seat`, `/open-source`, `/request` and `/verify` routes go), `admin.go` and `admin_test.go` (the
  HTML page goes; its caps and audit moved in P5.1), `website/src/pages/keys.astro` (points to the
  portal), `website/src/lib/keys-intake.mjs` (removed), `website/test/keys.mjs`, `firebase.json`
  (`form-action` drops `keys.fugaro.dev`). Tests: `go test ./ee/keyservice/ -tags ee -count=1` and
  `cd website && npm run check`. Depends on: P3.3, P5.1, P9.2. Commit: `ee: the portal replaces the key forms and the admin page`.

## Parallel execution

External edges: #2457 (K2.3) before P1.0 and P3.2; #2458 (K2.5) before P4.1 and P4.2; K2.7 before P3.3.

Edges: P1.1 → P1.4; P1.0, P1.2 → P1.6; P1.0, P1.1, P1.2, P1.3 → P2.1; P2.1 → P2.2, P3.1, P5.1; P3.1 → P3.2, P3.3, P4.1, P4.3,
P5.1, P6.2, P7.1; P1.2 → P4.2 → P4.3; P1.4 → P2.3, P3.4, P4.1, P4.4, P5.3, P6.4, P9.2; P2.2 → P6.1, P6.3,
P7.1; P5.1 → P6.1 → P6.2 → P6.3 → P5.2; P4.2 → P5.2; P2.1, P3.1, P4.3 → P2.4; P3.3, P5.1, P9.2 → P9.3;
P1.5 → P0.4 → P0.2; P9.2, P2.1, P0.4 → P0.5.

| Level | Tasks (run together) |
| --- | --- |
| 0 | P1.0, P1.1, P1.2, P1.3, P1.5, P9.1 |
| 1 | P1.4, P1.6, P2.1, P4.2 |
| 2 | P2.2, P2.3, P3.1, P3.4, P4.4, P5.3, P6.4, P9.2 |
| 3 | P3.2, P3.3, P4.1, P4.3, P5.1, P7.1 |
| 4 | P2.4, P6.1, P9.3 |
| 5 | P6.2 |
| 6 | P6.3 |
| 7 | P5.2 |

Owner-run: P0.1 and P0.3 at any time; P0.4 after P1.5; P0.2 after P0.4; P0.5 after level 2.

**Critical path:** #2457 → P1.0 → P2.1 → P3.1 → P5.1 → P6.1 → P6.2 → P6.3 → P5.2 (eight levels). The
reseller chain is the long pole; customer self-serve (P3, P4) is complete at level 3, so a first
production deploy can go before slice P6 lands.

Why some of it is sequential: `service.go` has one writer (P1.0 opens the registry so nothing after it
edits that file until P9.3); every API task needs the auth (P1.3), the store (P1.2) and the organisation
model (P2.1); reseller accrual needs reseller organisations; reports read statements.

### Shared-files table

Only files written by more than one task are listed; every other file in the plan has one writer.

| File | Writers (level) |
| --- | --- |
| `ee/keyservice/service.go` | P1.0 (0), P9.3 (4) |
| `ee/keyservice/merchant.go` | #2458 (external, before 1), P4.2 (1) |
| `ee/keyservice/policy.go` | P3.1 (2) |
| `firebase.json` | P9.2 (2), P9.3 (4) |
| `ee/keyservice/admin.go`, `admin_test.go` | P9.3 (4) |
| `go.mod`, `go.sum` | P1.2 (0), P1.6 (1) |
| `ee/keyservice/api.go` | P1.0 (0), P1.6 (1) |
| `ee/portal/package.json`, `package-lock.json` | P1.4 (1) |
| `website/test/keys.mjs`, `website/src/pages/keys.astro` | P9.3 (4) |
| `docs/licence-key-v2/*`, `docs/design/key-signing-service.md` | P9.1 (0) |

No level holds two writers of one file. P2.4 and P4.1 each write a Go file and a screen folder that no
other task writes. Screens register through `import.meta.glob` (P1.4), and API routes through
`registerAPI` (P1.0), so adding an area edits no shared file.

## Checks this plan takes (570–579)

All are OWNER-RUN, on `fugaro-staging` first; each becomes a script under `scripts/checks/` written by the
task named, so the owner types one command.

| Check | Measures | Script written by |
| --- | --- | --- |
| 570 | A sandbox purchase end to end: checkout, the webhook, the key, reveal, `fugaro licence set` on a test installation, a seat purchase, then `fugaro licence refresh` shows the new seats | P4.3 |
| 571 | In the deployed project, only the minter's account can sign, and no project-level role that holds `useToSign` is granted to anyone but the owner | P1.5 |
| 572 | An email-link account is refused by the operations and reseller API and sees the SSO dialog | P2.3 |
| 573 | A redelivered sandbox event leaves the ledger unchanged | P4.2 |
| 574 | A staging-signed key is refused by a production build | P1.5 |
| 575 | A staging reseller's month: accrual, statement mail, a mint refused over the limit | P6.3 |

576–579 are reserved.

## After deploy

The portal is Fugaro's own service, not an installation's: no `fugaro update` step and no
`### For operators` entry follow from it. The licence-key-v2 tasks that do reach an installation keep their
own entries. The owner's post-deploy steps are P0.5 and the checks above. When P9.3 lands, the website's
deploy must follow the portal's, because the key page will point to the portal.
