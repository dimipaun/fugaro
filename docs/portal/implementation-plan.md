# Fugaro portal: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (findings F1–F42,
decisions D2180–D2207).

## Status

**Plan only. Nothing is implemented. No task is approved**: each task needs the owner's own explicit go,
by name. Answering a decision is not approving a task.

The owner's answers that shape this plan (2026-11-12):

- **Backend and keys.** The backend is Go and extends `ee/keyservice` (D2180). The key is the v2 bundle
  (D2181). A reissue for seats or a renewal keeps the secret, and refresh returns the newest key (D2200).
  "Reveal" rotates through an encrypted 24-hour handover, once a day (D2182 as amended). #2457 and #2458
  merge as planned (D2183).
- **Entitlements.** Every grant is an entitlement, bound to a Firebase project id in the portal or by
  `licence login`, then minted (D2196, D2199). The free seat is an entitlement at sign-up (D2184 as
  amended). A paid term runs from payment (D2197). Rebinding is self-serve once per 30 days (D2198).
  Trials and open source stay, in the portal (D2185).
- **Binding.** No proof of a project id; trials key on organisation and project (D2201). A project id is
  unique across organisations, operations transfers it (D2204, TN-P1). One active entitlement per
  workspace (D2204). Connected keys run at most 30 days and renew (D2202). A purchase ends the free seat
  (D2203). A binding needs an existing installation (D2205). Air-gapped keys are issued once (D2206).
- **Sign-in.** Operations is Google only; resellers Google or Microsoft pinned to a directory the verified
  token's `tid` names (D2207 as the owner amended it on 2026-11-13: "Pin the real directory id").
- **Seats.** Admins, then operators in grant order, hold the seats (D2186, held with K3.4).
- **Operations.** Owners and admins mint after a fresh sign-in (D2187).
- **Data path.** The SPA reaches data through the API only (D2188).
- **Resellers.** They accrue daily against a credit limit (D2189), owe Fugaro (D2190) and create their
  customers' organisations (D2191).
- **Billing.** One Paddle subscription per workspace (D2192).
- **Placement and deletion.** The SPA lives in `ee/portal/` (D2193). Everything runs in `fugaro-prod`
  with staging in `fugaro-staging`; the owner accepted the risk round 1 found (D2194). A sole owner
  cannot delete their account (D2195).

## Conventions every part follows

- `CLAUDE.md` and [.claude/rules/ee.md](../../.claude/rules/ee.md): every file under `ee/` starts with
  the two-line SPDX notice, and a JSON file (schemas, fixtures, `package.json`, the lockfile,
  `tsconfig.json`, `firebase.json`) gets a sibling `.license` file (`TestEEFilesCarryTheNotice`); nothing
  outside `ee/` imports `ee/` (`go test ./internal/eeboundary/ -count=1` after each Go task).
- **No agent deploys, builds a release image of, operates or holds a signing key for the service**
  (ee.md, R5 as amended by D440). Slice P0 and the live checks are OWNER-RUN.
- Hermetic tests: no network, no real cloud, no home directory. Paddle, Resend, KMS and the minter are
  `httptest` or interface fakes. The Firestore adapter's suite runs under a `firestore` tag against the
  emulator, staged in the pending portal workflow (P9.2).
- No secret-shaped literal in a test, fixture or doc: secrets are built at test time.
- Strict decoding. The API's shapes are JSON Schemas in `ee/portal/schemas/`, one file per area, and the
  Go types are `ee/keyservice/wire_<area>.go`. Both are written by P1.1. A later task changes only its
  own area's files, and every area has one task. Adding a field is allowed; a changed meaning gets a new
  name.
- **Cross-tenant tests in every API task.** An owner or admin of organisation A acting on B's workspace,
  entitlement, key, subscription or member is refused (`TestAnotherOrgsXIsRefused` in each task).
- Never edit `.github/workflows/`; the portal's workflow is staged under `docs/pending-workflows/`.
- Comments explain why. No TODO comments.
- New dependencies (F24), each added by one task: `cloud.google.com/go/firestore` (P1.2),
  `github.com/getsentry/sentry-go` (P1.6), `cloud.google.com/go/kms` (P1.7). `ee/portal/package.json`
  (P1.4) adds `react`, `react-dom`, `react-router-dom`, `firebase`, `@sentry/react`, `@paddle/paddle-js`,
  `vite`, `@vitejs/plugin-react`, `typescript`, `vitest`, `@testing-library/react`, `jsdom` and `ajv`.
  Nothing else without asking.
- Targeted tests only. Live-check scripts go under `scripts/live-checks/check-NNN.sh`
  (`TestLiveCheckScripts`), registered in `docs/gcp-live-checklist.md`.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A person who belongs to an operations or reseller organisation is refused, for any request using that membership, unless signed in with `google.com` or `microsoft.com` | P1.3 `TestEmailLinkIsRefusedForOperations`, P2.1 `TestSSORuleFollowsTheActorsMembership` |
| Operations accepts Google only; a reseller's Microsoft sign-in needs a pinned Microsoft (Entra ID) directory — the verified token's `tid`, never the Identity Platform tenant a client names (D2207) | P1.3 `TestOperationsAcceptsGoogleOnly`, `TestMicrosoftDirectoryIsPinned`, `TestAClientNamedTenantNeverAdmitsMicrosoft`, `TestResellerMicrosoftNeedsAPinnedDirectory` |
| A binding needs an existing Fugaro installation (D2205) | P3.1 `TestBindingNeedsAFugaroInstallation` |
| An air-gapped key is issued once (D2206) | P3.1, P3.2 |
| An invitation is accepted only by its own verified address | P2.2 `TestInvitationIsBoundToItsAddress` |
| No key or secret is mailed; a stored handover is encrypted | P2.2 `TestResendMailerSendsNoKey`, P3.6 `TestHandoverIsStoredEncrypted` |
| A reveal rotates, needs a fresh sign-in, is one per day, never extends a term, and tells the other owners | P3.2 |
| A seat change or renewal reaches the installation through `licence refresh` | P3.6 `TestSeatChangeIsTheNextRenewal` |
| `/renew` issues only while the entitlement runs | P3.6 `TestRenewalStopsWhenTheEntitlementEnds` |
| Revocation covers every key of an entitlement | P3.6 `TestRevocationCoversEveryRotation` |
| The minter is internal, invokable by the API only, and enforces row, term cap and policy bounds | P1.5 `TestOnlyTheAPIInvokesTheMinter`, P1.8 |
| D454's rate per tier queues the excess for operations mints | P5.1 `TestOperationsMintsQueueOverTheRate` |
| Every operations action, reads included, is audited | P2.1, P5.1 |
| A Paddle transaction is created server-side; an event is stored as received first, checked for time, and a redelivery changes nothing | P4.1, P4.2 |
| A reseller sees only its customers and never a secret | P6.1 |
| Two concurrent reseller mints cannot pass the credit limit together | P6.2 (memory, and the emulator suite) |
| Firestore rules deny every client access | P1.5 `TestFirestoreRulesDenyClients` |
| No analytics before consent; Sentry sends no personal data | P1.4, P1.6 |
| A staging key never verifies in production | `TestStagingKeyIDIsRefused`; Check 574 |

## Limits this plan accepts

- **One project (D2194, accepted by the owner):** the website's CI deployer can replace the portal's
  JavaScript, and project-level Firestore, Cloud Run or service-account grants in `fugaro-prod` reach the
  signer. Check 571 lists every such grant.
- An installation without a renewal job (TN5 of licence-key-v2) must `licence set` the new bundle within
  24 hours of a reveal.
- The operations second factor is the identity provider's (D2187).
- The free seat's abuse control is the terms and a per-account daily limit on new organisations.
- A binding is not proven (D2201): a squatter can bind a victim's project id first, until operations
  transfers it; a phished entitlement is recovered by the owner's rebind.
- The minter is stateless: a compromised API mints any row for any workspace.
- Squatting is free: any email-link account gets a free-seat entitlement and can bind a guessable
  project id first (TN-P1); operations' transfer is the only remedy.
- With 30-day keys (D2202), an installation without a coordinator (TN5) must run `fugaro licence refresh`
  every month or lapse after 30 days plus grace.
- Resellers mint with no step-up beyond their sign-in.

## Slices and tasks

Every task's reviewer tier is **opus + mandatory independent review** where it says SECURITY, otherwise
standard. Each task's Red step is its first named test, written first and failing because the code does
not exist yet.

### Slice P0: owner-run (no agent performs any step)

- **P0.1 Projects and Firebase.** In `fugaro-prod` and `fugaro-staging`, enable Auth's Google, Apple
  (Services ID), Microsoft (app registration) and email-link providers. Add `app.fugaro.dev` and the staging
  host to the authorised domains. Create the `fugaro-portal` hosting site and its DNS. Depends on: none.
- **P0.2 Provider accounts.** Paddle sandbox and live (subscriptions S8.5, licence-key-v2 K5.2): one price
  per row, the webhook at `https://app.fugaro.dev/api/paddle/webhook`. Resend and its DNS (staging uses
  Resend's test mode and Paddle's sandbox only). Sentry project and DSN. Analytics property. Each secret
  goes in with `gcloud secrets versions add <name> --data-file=- --project <p>`, under the names P1.5
  creates. Depends on: P0.4.
- **P0.3 Counsel.** The terms of service, licence terms, reseller invoicing and tax, the privacy notice,
  and D457 retention including raw Paddle events. Gates P0.5's production deploy.
- **P0.4 Apply the Terraform.** `terraform -chdir=ee/keyservice/terraform/keyring apply
  -var project=fugaro-staging -var environment=staging`, then `services` the same way, then both for
  `fugaro-prod` with `environment=production`. If a `fugaro-licence` ring exists, destroy its key versions;
  the ring cannot be deleted, and this apply uses a new ring name so it never meets
  `TestTheSigningKeyIsNeverDestroyedByAnApply`'s guard on the old one. Depends on: P1.5.
- **P0.5 Build, deploy and seed.** `scripts/deploy-portal.sh --project fugaro-staging --dry-run`, then
  without `--dry-run`. The script runs the Cloud Build of P1.7's two images, deploys both services and the
  hosting site, and seeds the operations organisation with `--seed-operations <owner address>`. Production
  follows Check 570–575. The seed is the API program's one-time `seed-operations` subcommand (P1.7),
  run once with the owner's credentials. Depends on: P0.1, P0.2, P0.4, P1.7, P9.2, P2.2.
- **P0.6 The live checks.** `scripts/live-checks/check-570.sh` … `check-575.sh` on staging. Depends on:
  P9.4, P0.5, and licence-key-v2's K2.4, K3.2 and K3.3 (Check 570 runs `licence login` and `refresh`).

### Slice P1: foundations (inert alone)

- **P1.0 The API mount and dependencies. SECURITY.**
  - Files: `ee/keyservice/api.go` (new), `ee/keyservice/service.go`, `ee/keyservice/keyservice_test.go`
    (the `Deps` fixture), `ee/keyservice/api_test.go` (new).
  - `Handler` mounts an `/api/` registry. Area files call `registerAPI(method, path, h)` from `init`, so no
    later task edits `service.go`. `Deps` and `New` gain every interface later tasks need: the document
    store, the sign-in verifier, the Paddle client, the Scheduler verifier, the handover sealer and the
    mailer. Absent ones are refused at start only when a route that needs them is mounted.
  - Also: one JSON error shape, a body cap per route, no CORS (Hosting rewrites `/api/**` to the service).
  - Tests: `TestAPIRoutesComeFromTheRegistry`, `TestAPIErrorsAreUniform`, `TestAPIBodyIsCapped`,
    `TestClientAddressBehindHostingIsTheCallers`.
  - Command: `go test ./ee/keyservice/ -run 'TestAPI|TestClientAddress' -count=1`.
  - Round 3: P1.0 also adds **every** `Config` field later tasks need (Paddle API key, Resend key, Sentry
    DSN, Scheduler audience, sealer key name, tenant lists' store) and moves the existing `/webhook` route
    to `/api/paddle/webhook`, so no later task but P9.3 edits `service.go` (`startup_test.go` follows).
  - Depends on: #2457, #2458. Commit: `ee: the portal API's route registry`.
- **P1.1 Wire schemas and types.**
  - Files: `ee/portal/schemas/<area>.schema.json` (new: orgs, invites, workspaces, entitlements, keys,
    checkout, billing, ledger, ops, resellers, statements, reports, audit, error), each with `.license`;
    `ee/portal/schemas/fixtures/*.json` with `.license` (new); `ee/keyservice/wire_<area>.go`,
    `wire_test.go` (new).
  - Tests: `TestWireFixturesDecodeStrictly`, `TestWireRefusesUnknownFields`,
    `TestRevealBundleIsTheOnlySecretShape`.
  - Command: `go test ./ee/keyservice/ -run 'TestWire|TestRevealBundle' -count=1`.
  - Depends on: none. Commit: `ee: the portal's wire schemas`.
- **P1.2 The document store.**
  - Files: `ee/keyservice/docstore/` (new: `docs.go` with get, put, query and transactions; `memory.go`;
    `firestore.go`; `conformance_test.go`; `firestore_test.go` under `//go:build firestore`), `go.mod`,
    `go.sum`, and `ee/keyservice/firestore_store.go` (new: the `Store` interface, as #2457 and #2458 leave
    it, on `docstore`).
  - Tests: `TestDocstoreConformance`, `TestTransactionRetriesOnConflict`, `TestStoreOnDocstoreConformance`.
  - Command: `go test ./ee/keyservice/... -run 'TestDocstore|TestTransaction|TestStoreOnDocstore' -count=1`.
  - Depends on: #2457, #2458. Commit: `ee: a document store on Firestore`.
- **P1.3 Sign-in for the API. SECURITY.**
  - Files: `ee/coordinator/mcpauth/firebase.go` (`Identity` gains `Provider`, `AuthTime`, `TenantID` and
    `DirectoryID`; the D923 comment names the portal as a second caller), `ee/keyservice/portalauth.go`,
    `portalauth_test.go` (new).
  - The verifier pins audience, issuer and `email_verified`. It compares addresses through `access.Member`
    on both sides and offers `RequireSSO`, `FreshWithin(5*time.Minute)` and the directory check. It also
    verifies Cloud Scheduler OIDC tokens, account **and** audience. It settles, against Firebase's
    documentation and a recorded token shape, whether Microsoft addresses arrive verified, before P2.2.
  - Tests: `TestEmailLinkIsRefusedForOperations` (both `password` and `emailLink`),
    `TestTokenFromAnotherProjectIsRefused`, `TestUnverifiedEmailIsRefused`, `TestStaleSignInIsRefusedForMint`,
    `TestMicrosoftDirectoryIsPinned`, `TestAClientNamedTenantNeverAdmitsMicrosoft`,
    `TestEmptyDirectoryListRefusesMicrosoft`, `TestOperationsAcceptsGoogleOnly`,
    `TestResellerMicrosoftNeedsAPinnedDirectory`,
    `TestSchedulerTokenNeedsItsAccountAndAudience`, plus the existing
    `go test ./ee/coordinator/mcpauth/ -count=1`.
  - Command: `go test ./ee/keyservice/ ./ee/coordinator/mcpauth/ -run 'TestEmailLink|TestTokenFrom|TestUnverified|TestStale|TestMicrosoftDirectory|TestEmptyDirectory|TestAClientNamed|TestResellerMicrosoft|TestSchedulerToken' -count=1`.
  - D2207 as the owner's ruling of 2026-11-13 ("Pin the real directory id") amends it: the operations
    organisation accepts `google.com` only. A reseller organisation accepts Google, or Microsoft only when
    the VERIFIED token's Microsoft (Entra ID) directory id — the `tid` the provider wrote into
    `firebase.sign_in_attributes`, which is the only place mcpauth reads one, because the token's own
    claim set is the custom-claims namespace and a `tid` there is a claim a project-side writer can mint
    (F43's second half) — is on its list. The Identity Platform tenant a client names at sign-in
    (`firebase.tenant`) never admits, and a token carrying no `tid` is refused: a reseller whose provider
    forwards no directory is Google-only, which is D2207's fallback. `TestMicrosoftDirectoryIsPinned` is
    also the mutation check — the admitted fixture's client-named tenant is off the list, so an
    implementation reading the tenant refuses it and the test fails — and
    `mcpauth.TestATopLevelTidIsNotADirectory` is the second one, failing for a reader that takes the
    token's own `tid`. The mcpauth suite runs whole: `go test ./ee/coordinator/mcpauth/ -count=1`.
  - Depends on: none. Commit: `ee: the portal API verifies sign-in, provider and directory`.
- **P1.4 The SPA skeleton. SECURITY** (sign-in code, and it edits the boundary guard's licence test).
  - Files (new, under `ee/portal/`): `package.json`, `package-lock.json`, `tsconfig.json` (each with
    `.license`); `vite.config.ts`, `index.html`; `src/main.tsx`, `src/app.tsx`; `src/routes.ts`, which
    collects `src/screens/*/route.tsx` with `import.meta.glob`; `src/api/client.ts`,
    `src/api/types/<area>.ts`; `src/auth/*`; `src/telemetry.ts` (Sentry with no personal data and no
    replay, off without a DSN); `src/consent.ts` (analytics after consent, D93); `src/styles.css`
    (imports `website/src/styles/tokens.css`, mobile-first); `src/screens/signin/*`; `test/*`. Also
    `internal/eeboundary/licence_test.go`: the portal's lockfile joins the copyleft audit, skipped when
    `ee/` is absent.
  - Tests: `types.test.ts` (every fixture validates with ajv and type-checks), `signin.test.tsx`,
    `consent.test.ts`, `telemetry.test.ts`.
  - Command: `cd ee/portal && npm ci && npx vitest run && npx tsc --noEmit`, plus
    `go test ./internal/eeboundary/ -count=1`.
  - Depends on: P1.1. Commit: `ee: the portal app's skeleton and sign-in`.
- **P1.5 Infrastructure. SECURITY.**
  - Files: `ee/keyservice/terraform/keyring/*.tf` (the project and one environment per apply: one key id
    per ring, deciding the open D459); `ee/keyservice/terraform/services/*.tf`.
  - The services root adds the Firestore database `portal`, the API and minter services, the minter's
    ingress set to internal with the API's account as its only invoker, and the minter's signer grant on
    its ring only. It adds a KMS encrypt/decrypt key for the handover whose decrypter is the API's account
    only, the secrets' empty containers, and `scheduler.tf` with the statement and reminder jobs **paused**.
  - Also `ee/portal/firestore.rules` (+ `.license`, deny all) and
    `ee/keyservice/terraform/infra_test.go`.
  - Tests: `TestOnlyTheMinterCanSign`, `TestOnlyTheAPIInvokesTheMinter`, `TestMinterIngressIsInternal`,
    `TestOnlyTheAPIDecryptsHandovers`, `TestRingHoldsOneEnvironmentsKey`, `TestFirestoreRulesDenyClients`,
    `TestNoServiceRunsAsADefaultAccount`.
  - Command: `go test -tags terraform ./ee/keyservice/terraform/ -count=1`.
  - The new tests extend the existing `TestOnlyTheMinterHoldsTheSignerRole` and
    `TestTheMinterHasNoPublicInvoker` in `infra_test.go` rather than duplicate them.
  - Depends on: none. Commit: `ee: the portal's infrastructure in one project (D2194)`.
- **P1.6 Backend error reporting.**
  - Files: `ee/keyservice/errreport.go`, `errreport_test.go` (new); `ee/keyservice/api.go`, which wraps
    the registry's handlers; `go.mod`, `go.sum`.
  - Panics and 5xx answers go to Sentry without body, headers or address; no DSN means off.
  - Tests: `TestPanicIsReportedWithoutTheBody`, `TestNoDSNReportsNothing`.
  - Command: `go test ./ee/keyservice/ -run 'TestPanicIs|TestNoDSN' -count=1`.
  - Depends on: P1.0, P1.2. Commit: `ee: the portal API reports errors to Sentry`.
- **P1.7 The deployable programs. SECURITY.**
  - Files (new): `ee/keyservice/cmd/portal-api/main.go`, `ee/keyservice/cmd/minter/main.go`,
    `ee/keyservice/kmssigner.go` and `kmssigner_test.go` (a `Signer` on the KMS API, fake client in
    tests), `ee/keyservice/kmsseal.go` and `kmsseal_test.go` (P1.0's handover sealer on KMS encrypt and
    decrypt), `ee/keyservice/config.go` and `config_test.go` (environment and Secret Manager, strict),
    `ee/keyservice/cmd/*/Dockerfile`, `ee/keyservice/cloudbuild.yaml` (+ `.license`); also `go.mod`, `go.sum`.
  - Tests: `TestConfigRefusesUnknownVariables`, `TestKMSSignerVerifiesWithTheKeyring`, `TestKMSSealerRoundTrips`,
    `TestMinterBinaryHoldsNoMailOrMerchantSecret`.
  - Command: `go test ./ee/keyservice/... -run 'TestConfig|TestKMSSigner|TestKMSSealer|TestMinterBinary' -count=1`.
  - The API program has a `seed-operations <address>` subcommand: it writes the operations
    organisation and its first invitation through P2.2's `SeedOperations`, and refuses once seeded.
  - Depends on: P1.0, P1.6, P2.2. Commit: `ee: the portal API and minter programs`.
- **P1.8 The minter's bounded term and policy. SECURITY.**
  - Files: `ee/keyservice/minter.go`, `ee/keyservice/policy.go`, `minter_test.go`, `policy_test.go`.
  - The minter accepts an end date that it caps at the row's term (F30). It accepts an operations
    `check_interval` within `P1D`–`P30D`, and never none outside the air-gapped row (F28). It adds a
    `free-seat` row: Team, one seat, `kind: comped`, auto renewal. A reissue under the same entitlement
    may carry the existing secret hash instead of a new secret (D2200, amending D2135). Every connected
    row's key runs at most 30 days to `paid_through` (D2202); air-gapped rows keep their term.
  - Tests: `TestMintTermNeverExceedsTheRow`, `TestRequestedEndCapsTheTerm`,
    `TestOperationsCheckIntervalIsBounded`, `TestFreeSeatRowIsComped`, `TestReissueKeepsTheSecretHash`,
    `TestConnectedKeysRunAtMostThirtyDays`.
  - Command: `go test ./ee/keyservice/ -run 'TestMintTerm|TestRequestedEnd|TestOperationsCheck|TestFreeSeatRow|TestReissueKeeps|TestConnectedKeys' -count=1`.
  - Depends on: #2457, #2458. Commit: `ee: the minter takes a capped end and a bounded check interval`.

### Slice P2: tenancy

- **P2.1 Organisations and memberships. SECURITY.**
  - Files: `ee/keyservice/orgs.go`, `api_orgs.go`, `audit.go`, `orgs_test.go` (new); also the shared files the
    table below lists against it — `api.go` (the interfaces a route is written against) and `service.go` (the
    seams a route reaches the store, the mail path and the rest through), `keyservice_test.go`,
    `portalauth.go`/`portalauth_test.go` (the adapter behind those seams) and `startup_test.go`. P2.1 is the
    first area task, so it is the one that takes them; the areas after it add files of their own.
  - Sign-up creates a customer organisation, records the terms' version and time, and creates the
    free-seat entitlement through the `onOrgCreated` hooks that P3.1 registers (D2199). Organisation types and roles; the last-used organisation; operations
    reads across organisations, audited; the SSO rule by the actor's membership; a per-account daily limit
    on new organisations.
  - Tests: `TestSignUpRecordsTermsAcceptance`, `TestOrgCreatedHooksRun`,
    `TestRolesLiveInMemberships`, `TestSSORuleFollowsTheActorsMembership`, `TestOperationsReadsAreAudited`,
    `TestLastUsedOrg`, `TestOrgCreationIsRateLimited`, `TestAnotherOrgsMembersAreRefused`.
  - Command: `go test ./ee/keyservice/ -run 'TestSignUp|TestOrgCreatedHooks|TestRoles|TestSSORule|TestOperationsReads|TestLastUsed|TestOrgCreation|TestAnotherOrgsMembers' -count=1`.
  - Depends on: P1.0, P1.1, P1.2, P1.3. Commit: `ee: portal organisations and memberships`.
- **P2.2 Invitations and mail. SECURITY.**
  - Files: `ee/keyservice/invites.go`, `api_invites.go`, `resend.go` (a `Mailer` on Resend's API), tests (new).
  - Tests: `TestInvitationIsBoundToItsAddress`, `TestInvitationIsSingleUse`,
    `TestOperationsInviteOutsideTheDomainListIsRefused`, `TestResellerInviteNeedsSSO`,
    `TestResendMailerSendsNoKey`, `TestAnotherOrgsInvitationsAreRefused`, `TestSeedIsAnInvitationBoundAtFirstSignIn`.
  - Command: `go test ./ee/keyservice/ -run 'TestInvitation|TestOperationsInvite|TestResellerInvite|TestResend|TestAnotherOrgsInvitations|TestSeedIs' -count=1`.
  - `SeedOperations` writes the operations organisation and its first invitation once
    (`TestSeedIsRefusedOnceSeeded`).
  - Depends on: P2.1. Commit: `ee: portal invitations by email`.
- **P2.3 Tenancy screens.**
  - Files: `ee/portal/src/screens/{orgs,members,settings,sso}/*` and their tests (new).
  - Tests: `sso.test.tsx` (an operations organisation selected while signed in by email opens the
    dialog), `members.test.tsx`, `switcher.test.tsx`.
  - Command: `cd ee/portal && npx vitest run src/screens/orgs src/screens/members src/screens/settings src/screens/sso`.
  - Depends on: P1.4. Commit: `ee: the portal's organisation screens`.
- **P2.4 Deletion. SECURITY.**
  - Files: `ee/keyservice/deletion.go`, `api_account.go`, tests (new); `ee/portal/src/screens/account/*` (new).
  - Deleting an organisation cancels its subscriptions and revokes every entitlement's keys. Deleting an
    account deletes the Firebase Auth user through the Identity Toolkit API, and its memberships and
    invitations.
  - Tests: `TestSoleOwnerCannotDeleteTheirAccount`, `TestOrgDeletionRevokesAndCancels`,
    `TestAccountDeletionRemovesTheAuthUser`, `TestDeletionRemovesMembershipsAndInvitations`, `TestAnotherOrgCannotBeDeleted`, `account.test.tsx`.
  - Command: `go test ./ee/keyservice/ -run 'TestSoleOwner|TestOrgDeletion|TestAccountDeletion|TestAnotherOrgCannot' -count=1`
    and `cd ee/portal && npx vitest run src/screens/account`.
  - Depends on: P2.1, P3.1, P3.6, P4.3. Commit: `ee: self-serve account and organisation deletion (D2195)`.

### Slice P3: workspaces, entitlements and keys

- **P3.1 Workspaces and entitlements. SECURITY.**
  - Files: `ee/keyservice/entitlements.go`, `workspaces.go`, `api_workspaces.go`, `api_entitlements.go`,
    tests (new).
  - A workspace may be unbound. An entitlement records its source, tier, seats, term (from payment,
    D2197) and the workspace it is attached to. Binding takes a Firebase project id and mints through the
    minter. Rebinding is once per 30 days and revokes the old key (D2198). A project id is bound by one
    workspace across all organisations (D2204); a workspace has one active entitlement, and a purchase
    supersedes a trial or free seat there; the first paid entitlement ends the organisation's free seat at
    its next renewal (D2203).
  - Tests: `TestBindingMintsTheKey`, `TestUnboundEntitlementHasNoKey`, `TestTermRunsFromPayment`,
    `TestRebindIsOncePerThirtyDays`, `TestRebindRevokesTheOldKey`, `TestWorkspaceIDIsAFirebaseProjectID`,
    `TestOnlyOwnersAndAdminsBind`, `TestAnotherOrgsEntitlementIsRefused`, `TestProjectIDIsBoundOnce`,
    `TestPurchaseSupersedesATrial`, `TestFirstPurchaseEndsTheFreeSeat`.
  - Command: `go test ./ee/keyservice/ -run 'TestBinding|TestUnbound|TestTermRuns|TestRebind|TestWorkspaceID|TestOnlyOwnersAndAdminsBind|TestAnotherOrgsEntitlement|TestProjectIDIs|TestPurchaseSupersedes|TestFirstPurchase' -count=1`.
  - Round 3 and later rulings:
    - **D2205 (owner): the project must exist.** Before binding, the API reads
      `https://<project>-default-rtdb.firebaseio.com/.json` unauthenticated (every installation creates
      that database, `deploy/terraform/gcp/modules/firebase/firebase.tf`). "Permission denied" means a
      Fugaro installation exists; "no such database" refuses with "run `fugaro setup` first"; no answer
      refuses with "try again", never binds unchecked. The project id is checked against the allowed shape
      before it is put in a host name.
    - The free-seat entitlement is created through P2.1's `onOrgCreated` hook.
    - A rebind rotates the secret, and a revoked key's secret fetches nothing. The first rebind after a
      binding is not held by the 30-day clock, so an owner can undo a phished binding at once.
    - **D2206:** an air-gapped entitlement is issued once, at binding, as a file; downloading it again
      returns the same signed text (it holds no secret); it is never revealed or rebound except by
      operations, audited.
    - Tests: `TestSignUpCreatesTheFreeSeatEntitlement`, `TestBindingNeedsAFugaroInstallation`,
      `TestUnreachableProbeNeverBinds`, `TestProjectIDShapeIsCheckedBeforeTheProbe`,
      `TestRebindRotatesTheSecret`, `TestRevokedKeysSecretFetchesNothing`, `TestFirstRebindIsImmediate`,
      `TestAirGappedIsIssuedOnce`.
  - Depends on: P2.1, P1.8. Commit: `ee: entitlements bound to a workspace mint its key (D2196)`.
- **P3.6 Renewal follows the entitlement. SECURITY.**
  - Files: `ee/keyservice/renew.go` (from #2457), `ee/keyservice/merchant.go`, `ee/keyservice/store.go`
    (lookups by entitlement, the sealed handover), `ee/keyservice/firestore_store.go`,
    `ee/keyservice/keyservice_test.go` (the `Deps` fixture), tests.
  - A seat change or renewal reissues under the entitlement with the same secret hash, and `/renew`
    answers with the newest key (D2200). `/renew` issues only while the entitlement runs (F27).
    Revocation is by entitlement (F29). The handover bundle is sealed with P1.0's sealer (D2182 as amended).
  - Tests: `TestSeatChangeIsTheNextRenewal`, `TestRenewalStopsWhenTheEntitlementEnds`,
    `TestRevocationCoversEveryRotation`, `TestHandoverIsStoredEncrypted`, `TestCompedKeysRenewOnlyWithinTheirTerm`.
  - Command: `go test ./ee/keyservice/ -run 'TestSeatChange|TestRenewalStops|TestRevocationCovers|TestHandoverIs|TestCompedKeysRenew' -count=1`.
  - Test files: `renew_test.go`, `merchant_test.go`, `store_revocation_test.go`.
  - Depends on: P3.1, P4.2. Commit: `ee: renewal and revocation follow the entitlement (D2200)`.
- **P3.2 Reveal. SECURITY.**
  - Files: `ee/keyservice/reveal.go`, `api_keys.go`, tests (new).
  - Reveal issues a fresh bundle ending on the predecessor's `paid_through`. A key with no secret
    (air-gapped) is re-signed the same way and offered as a licence file. Limits: one reveal per workspace
    per day, owners and admins only, a fresh sign-in, the other owners mailed.
  - Tests: `TestRevealNeverExtendsTheTerm`, `TestRevealRotatesTheSecret`, `TestRevealIsOncePerDay`,
    `TestOnlyOwnersAndAdminsReveal`, `TestRevealNeedsAFreshSignIn`, `TestRevealNotifiesOtherOwners`,
    `TestAirGappedRevealIsAFile`, `TestResellerAndOperationsSeeMetadataOnly`, `TestAnotherOrgsKeyIsRefused`.
  - Command: `go test ./ee/keyservice/ -run 'TestReveal|TestOnlyOwnersAndAdminsReveal|TestAirGapped|TestResellerAndOperations|TestAnotherOrgsKey' -count=1`.
  - D2206: an air-gapped key is never revealed (`TestAirGappedIsNeverRevealed`); the file of P3.1 is
    downloaded instead.
  - Depends on: P3.6. Commit: `ee: a key is revealed by rotating it (D2182)`.
- **P3.3 Trial and open source in the portal. SECURITY.**
  - Files: `ee/keyservice/api_trial.go`, `api_opensource.go`, tests (new).
  - Both create entitlements. The trial applies K2.7's rule; the open-source request applies `checks.go`'s
    proof check and `queue.go`'s queue, decided in P5.1.
  - Trials key on organisation and project id (D2201).
  - Tests: `TestTrialIsOnePerWorkspaceAndAccount`, `TestAnotherOrgCannotBurnYourTrial`,
    `TestOpenSourceRequestQueuesForOperations`.
  - Command: `go test ./ee/keyservice/ -run 'TestTrialIsOne|TestAnotherOrgCannotBurn|TestOpenSourceRequest' -count=1`.
  - Depends on: P3.1, K2.7 (licence-key-v2; not approved). Commit: `ee: trials and open-source requests in the portal (D2185)`.
- **P3.4 Workspace screens.**
  - Files: `ee/portal/src/screens/workspaces/*` and tests (new).
  - Shows workspaces, entitlements (paid, not yet bound), binding, rebind, reveal and copy, the air-gapped
    file, seats and renewal date, the line "run `fugaro licence refresh`" after a purchase or seat change,
    and the trial and open-source buttons.
  - Tests: `workspaces.test.tsx` (the bundle is shown once and cleared on navigation; an unbound
    entitlement shows how to bind; the refresh hint).
  - Command: `cd ee/portal && npx vitest run src/screens/workspaces`.
  - Depends on: P1.4. Commit: `ee: the portal's workspace screens`.
- **P3.5 `licence login` screens. SECURITY** (the device-approve page is a phishing surface).
  - Files: `ee/portal/src/screens/cli/*` and tests (new).
  - K2.4's authorize and device-approve pages. They list the organisation's unbound entitlements and bind
    the chosen one to the requesting installation's Firebase project id, shown as untrusted text.
  - Tests: `authorize.test.tsx`, `device.test.tsx` (the user code must be typed).
  - Command: `cd ee/portal && npx vitest run src/screens/cli`.
  - Depends on: P1.4, K2.4 (licence-key-v2; not approved). Commit: `ee: the licence login pages in the portal`.

### Slice P4: Paddle

- **P4.1 Checkout. SECURITY.**
  - Files: `ee/keyservice/checkout.go`, `api_checkout.go`, `paddleclient.go`, tests (new);
    `ee/portal/src/screens/checkout/*` (new).
  - Records the licence terms' acceptance, creates the Paddle transaction server-side with `custom_data`
    `{org_id, workspace_id}`, and hands the overlay only the transaction id. A second active subscription
    for one workspace is refused.
  - Tests: `TestCheckoutNeedsLicenceTermsAccepted`, `TestTransactionIsCreatedServerSide`,
    `TestSecondSubscriptionForAWorkspaceIsRefused`, `TestAnotherOrgsWorkspaceCannotBeBought`,
    `checkout.test.tsx`.
  - Command: `go test ./ee/keyservice/ -run 'TestCheckout|TestTransactionIs|TestSecondSubscription|TestAnotherOrgsWorkspace' -count=1`
    and `cd ee/portal && npx vitest run src/screens/checkout`.
  - Depends on: P3.1, P1.4. Commit: `ee: Paddle checkout from the portal`.
- **P4.2 Paddle events. SECURITY.**
  - Files: `ee/keyservice/merchant.go`, `paddleevents.go` (the adapter), `ledger.go`, tests (new).
  - The signature and a 5-minute timestamp window are verified first; the verified body is then stored as
    received with its header, keyed by event id (an undecodable one by digest), before processing. The adapter maps the notification to the canonical event; refunds resolve to the
    entitlement. A purchase creates an entitlement, never a key (D2196). An undecodable body is stored and
    answered 400. An event whose transaction or subscription did not begin with a transaction the API created is
    parked; renewals and prorations of an API-created subscription are admitted. The route becomes `/api/paddle/webhook`,
    and the mail carries a link.
  - Tests: `TestEventIsStoredAsReceivedBeforeProcessing`, `TestStaleTimestampIsRefused`,
    `TestRedeliveryDoesNotDoubleCount`, `TestPaddleBodyMapsToTheEvent`, `TestRefundFindsItsEntitlement`,
    `TestPurchaseCreatesAnEntitlement`, `TestUndecodableEventIsKeptAndRetried`,
    `TestForeignTransactionIsParked`, `TestRenewalOfAnAPISubscriptionIsAdmitted`,
    `TestUnverifiedBodyIsNeverStored`, `TestLedgerDerivesFromStoredEvents`, `TestPurchaseMailCarriesNoKey`.
  - Command: `go test ./ee/keyservice/ -run 'TestEventIs|TestStaleTimestamp|TestRedelivery|TestPaddleBody|TestRefundFinds|TestPurchaseCreates|TestUndecodable|TestForeignTransaction|TestRenewalOfAnAPI|TestUnverifiedBody|TestLedger|TestPurchaseMail' -count=1`.
  - The timestamp window **reverses design §5.2's deliberate choice** (replay is handled by idempotency).
    P4.2 first confirms, from Paddle's documentation and a recorded sandbox retry, that each delivery is
    signed with a fresh `ts`; if not, no window is added and the reversal is dropped.
  - Depends on: P1.0, P1.2, P3.1, P4.1. Commit: `ee: Paddle events kept as received, mapped and ledgered`.
- **P4.3 Billing mirror, seats and the customer portal. SECURITY.**
  - Files: `ee/keyservice/billing.go`, `api_billing.go`, tests (new).
  - Mirrors subscription state. "Buy more seats" updates the quantity, and the event then reissues
    (P3.6). Provides a customer-portal link per organisation and cancellation for P2.4.
  - Tests: `TestBuySeatsUpdatesTheQuantity`, `TestCustomerPortalLinkIsPerOrg`, `TestOnlyOwnersAndAdminsBuy`,
    `TestAnotherOrgsSubscriptionIsRefused`.
  - Command: `go test ./ee/keyservice/ -run 'TestBuySeats|TestCustomerPortal|TestOnlyOwnersAndAdminsBuy|TestAnotherOrgsSubscription' -count=1`.
  - Depends on: P4.1, P4.2, P3.1. Commit: `ee: billing state, seat purchases and Paddle's customer portal`.
- **P4.4 Billing screens.**
  - Files: `ee/portal/src/screens/billing/*` and tests (new).
  - Tests: `billing.test.tsx` (seats, renewal date, the customer-portal link).
  - Command: `cd ee/portal && npx vitest run src/screens/billing`.
  - Depends on: P1.4. Commit: `ee: the portal's billing screens`.

### Slice P5: operations

- **P5.1 Operations API. SECURITY.**
  - Files: `ee/keyservice/ops.go`, `api_ops.go`, tests (new).
  - Lookups: organisations, workspaces, entitlements, key metadata, purchases and members.
  - Key actions: create an entitlement with any seats and end date, a row and a bounded check interval;
    extend; change seats; comp; revoke.
  - The open-source queue is decided here; the domain and tenant lists are set here; operations can cut
    a handover short and transfer a project id to another workspace (D2204).
  - Owners and admins only, a fresh sign-in, D454's rate per tier queueing the excess (carried from
    `admin.go`'s `MintCaps`). Supersedes K2.6.
  - Tests: `TestOperationsMemberCannotMint`, `TestMintNeedsAFreshSignIn`,
    `TestOperationsMintsQueueOverTheRate`, `TestEveryOperationsActionIsAudited`,
    `TestOperationsNeverSeesASecret`, `TestDomainAndTenantListsAreOwnerSet`,
    `TestOperationsCutsAHandoverShort`, `TestProjectTransferIsAudited`.
  - Command: `go test ./ee/keyservice/ -run 'TestOperationsMember|TestMintNeeds|TestOperationsMints|TestEveryOperations|TestOperationsNever|TestDomainAndTenant|TestOperationsCuts|TestProjectTransfer' -count=1`.
  - Parked Paddle events are resolved here: attached to an entitlement or dismissed, audited
    (`TestParkedEventIsResolvedOnce`).
  - Depends on: P2.1, P3.1, P1.8, P3.6. Commit: `ee: the operations API (D2187)`.
- **P5.2 Reports.**
  - Files: `ee/keyservice/reports.go`, `api_reports.go`, tests (new).
  - Tests: `TestReportSplitsDirectAndReseller`, `TestReportMonthsAreUTC`, `TestCompedKeysAreNotRevenue`,
    `TestResellerReceivableMatchesItsStatements`.
  - Command: `go test ./ee/keyservice/ -run 'TestReport|TestCompedKeysAre|TestResellerReceivable' -count=1`.
  - Depends on: P4.2, P6.3. Commit: `ee: monthly sales reports`.
- **P5.3 Operations screens.**
  - Files: `ee/portal/src/screens/ops/*` and tests (new).
  - Covers lookup, entitlement and key actions, the queue, the ledger, the audit log, reports and the
    lists.
  - Tests: `ops.test.tsx` (members see no action buttons; a mint asks for a fresh sign-in).
  - Command: `cd ee/portal && npx vitest run src/screens/ops`.
  - Depends on: P1.4. Commit: `ee: the portal's operations screens`.

### Slice P6: resellers

- **P6.1 Reseller organisations and customers. SECURITY.**
  - Files: `ee/keyservice/resellers.go`, `api_resellers.go`, tests (new).
  - Operations sets each reseller's credit limit and wholesale rates. A reseller is never a member of its
    customers' organisations; it invites each customer's first owner.
  - Tests: `TestOnlyOperationsCreatesAResellerOrg`, `TestResellerSeesOnlyItsCustomers`,
    `TestResellerNeverSeesASecret`, `TestResellerIsNotAMemberOfItsCustomers`, `TestRelinkIsAudited`,
    `TestCreditLimitAndRatesAreOperationsSet`.
  - Command: `go test ./ee/keyservice/ -run 'TestOnlyOperationsCreatesAReseller|TestResellerSees|TestResellerNever|TestResellerIsNot|TestRelink|TestCreditLimitAndRates' -count=1`.
  - After the first owner's invitation a reseller cannot invite into a customer's organisation
    (`TestResellerCannotInviteAfterTheFirstOwner`).
  - Depends on: P2.2, P5.1. Commit: `ee: resellers and their customers (D2191)`.
- **P6.2 Reseller entitlements and accrual. SECURITY.**
  - Files: `ee/keyservice/accrual.go`, `api_reseller_keys.go`, tests (new).
  - Tests: `TestAccrualIsSeatsTimesTheDailyRate`, `TestMintBeyondTheCreditLimitIsRefused`,
    `TestRevokedEntitlementStopsAccruing`, `TestConcurrentMintsCannotExceedTheLimit` (memory here; the
    emulator suite adds the same case).
  - Command: `go test ./ee/keyservice/ -run 'TestAccrual|TestMintBeyond|TestRevokedEntitlement|TestConcurrentMints' -count=1`.
  - Depends on: P6.1. Commit: `ee: reseller entitlements against a credit limit (D2189)`.
- **P6.3 Monthly statements.**
  - Files: `ee/keyservice/statements.go`, `api_statements.go`, tests (new).
  - Tests: `TestStatementSumsTheMonth`, `TestStatementRunIsIdempotent`,
    `TestStatementEndpointRefusesOtherCallers`, `TestPaymentIsRecordedByOperationsOnly`.
  - Command: `go test ./ee/keyservice/ -run 'TestStatement|TestPaymentIs' -count=1`.
  - Depends on: P6.2, P2.2. Commit: `ee: monthly reseller statements`.
- **P6.4 Reseller screens.**
  - Files: `ee/portal/src/screens/reseller/*` and tests (new).
  - Tests: `reseller.test.tsx` (customers, statements; no reveal button).
  - Command: `cd ee/portal && npx vitest run src/screens/reseller`.
  - Depends on: P1.4. Commit: `ee: the portal's reseller screens`.

### Slice P7: reminders

- **P7.1 Renewal and binding reminders.**
  - Files: `ee/keyservice/reminders.go`, `api_reminders.go`, tests (new).
  - Mails owners before a key that Paddle does not renew runs out, and mails a reminder about a paid
    entitlement that is still unbound.
  - Tests: `TestReminderIsSentOncePerTerm`, `TestPaddleRenewedKeysGetNoReminder`,
    `TestUnboundEntitlementIsReminded`, `TestReminderEndpointRefusesOtherCallers`.
  - Command: `go test ./ee/keyservice/ -run 'TestReminder|TestPaddleRenewed|TestUnboundEntitlementIs' -count=1`.
  - Depends on: P3.1, P2.2. Commit: `ee: renewal and binding reminders`.

### Slice P9: docs, hosting, retirement and checks

- **P9.1 Amend licence key v2.**
  - Files: `docs/licence-key-v2/implementation-plan.md`, `docs/licence-key-v2/reconciliation.md` and
    `docs/design/key-signing-service.md` (dated notes only): the list in the reconciliation's "What this
    changes in licence key v2", plus design §3.4 and T13 with §9.1 row 5 (superseded by D2194), §5.2 (the
    timestamp window, if P4.2 adds it), §6 (replaced by P5.1), and the open D454, D455 and D459 recorded
    as decided by D2187 and D2194.
  - Command: `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
  - Depends on: none. Commit: `docs: licence key v2 and the key-signing design follow the portal`.
- **P9.2 Hosting, deploy and CI. SECURITY.**
  - Files: `ee/portal/firebase.json` (+ `.license`; the `fugaro-portal` site, its CSP for Paddle,
    Firebase Auth, Sentry and analytics, and the `/api/**` rewrite to the API service);
    `scripts/deploy-portal.sh` (new); `scripts/deploy-portal_test.sh` (new);
    `ee/portal/test/hosting.test.ts` (new); `docs/pending-workflows/portal-tests.yml` (new: vitest, the
    targeted Go tests and the `firestore` emulator suite) and its section in
    `docs/pending-workflows/README.md`; `docs/pending-workflows/website-preview.yml` (new: a staged copy of
    the preview workflow whose gate also refuses `.hosting.site` and `.hosting.target`, F40).
  - The deploy script is OWNER-RUN with `--config ee/portal/firebase.json`, dry-run first, refuses an
    agent session, runs Cloud Build and accepts `--seed-operations`.
  - The root `firebase.json` and the website's tooling are untouched.
  - Command: `bash scripts/deploy-portal_test.sh` and `cd ee/portal && npx vitest run test/hosting.test.ts`.
  - Depends on: P1.4. Commit: `ee: the portal's hosting config and deploy script`.
- **P9.3 Retire the forms and the admin page. SECURITY.**
  - Files: `ee/keyservice/service.go` (drops the `/free-seat`, `/open-source`, `/request` and `/verify`
    routes); `admin.go` and `admin_test.go` (the HTML page goes); `website/src/pages/keys.astro` (points
    to the portal); `website/src/lib/keys-intake.mjs` (removed) and its importers `website/test/brand.mjs`,
    `website/test/origins.mjs` and `website/data/site.yaml`; `website/test/keys.mjs`; root
    `firebase.json` (`form-action` drops `keys.fugaro.dev`).
  - Command: `go test ./ee/keyservice/ -count=1` and `cd website && npm run check`.
  - Depends on: P3.3, P5.1, P9.2. Commit: `ee: the portal replaces the key forms and the admin page`.
- **P9.5 Pin this package's facts.**
  - Files: `internal/cli/docs_portal_test.go` (new): the number ranges (D2180–D2229, Checks 570–579), every
    decision row has an answer cell, the levels table matches the Depends-on lines.
  - Command: `GOFLAGS=-p=2 go test ./internal/cli/ -run DocsPortal -count=1`.
  - Depends on: none. Commit: `docs: pin the portal plan's numbers and levels`.
- **P9.4 The live-check scripts.**
  - Files: `scripts/live-checks/check-570.sh` … `check-575.sh` (new), `docs/gcp-live-checklist.md`
    (their entries). Each refuses `fugaro-prod` unless `--production` is given, and refuses an agent
    session.
  - Command: `go test ./scripts/ -run TestLiveCheckScripts -count=1` and
    `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
  - Depends on: P1.5, P2.3, P3.1, P3.2, P4.3, P6.3. Commit: `scripts: the portal's live checks`.

## Parallel execution

External edges: #2457 and #2458 (merged 2026-11-12) before P1.0, P1.2 and P1.8; K2.7 before P3.3; K2.4 before
P3.5 (both licence-key-v2, not approved; after P9.1 they register through P1.0's registry and depend on it).

Edges:

- Foundations: P1.1 → P1.4; P1.0, P1.2 → P1.6; P1.0, P1.6, P2.2 → P1.7.
- Tenancy: P1.0, P1.1, P1.2, P1.3 → P2.1 → P2.2.
- Workspaces and keys: P2.1, P1.8 → P3.1. P3.1 → P3.3, P4.1, P7.1. P3.1, P4.2 → P3.6 → P3.2.
- Paddle: P1.0, P1.2, P3.1, P4.1 → P4.2. P4.1, P4.2, P3.1 → P4.3. P2.1, P3.1, P3.6, P4.3 → P2.4.
- Operations and resellers: P1.8, P3.6 → P5.1 → P6.1. P2.2 → P6.1 → P6.2 → P6.3 → P5.2. P4.2 → P5.2.
  P2.2 → P6.3, P7.1.
- SPA screens: P1.4 → P2.3, P3.4, P3.5, P4.1, P4.4, P5.3, P6.4, P9.2.
- Retirement and checks: P3.3, P5.1, P9.2 → P9.3. P1.5, P2.3, P3.1, P3.2, P4.3, P6.3 → P9.4.

| Level | Tasks (run together) |
| --- | --- |
| 0 | P1.0, P1.1, P1.2, P1.3, P1.5, P1.8, P9.1, P9.5 |
| 1 | P1.4, P2.1 |
| 2 | P1.6, P2.2, P2.3, P3.1, P3.4, P3.5, P4.4, P5.3, P6.4, P9.2 |
| 3 | P1.7, P3.3, P4.1, P7.1 |
| 4 | P4.2 |
| 5 | P3.6, P4.3 |
| 6 | P2.4, P3.2, P5.1 |
| 7 | P6.1, P9.3 |
| 8 | P6.2 |
| 9 | P6.3 |
| 10 | P5.2, P9.4 |

Owner-run: P0.1 and P0.3 at any time; P0.4 after P1.5; P0.2 after P0.4; P0.5 after P1.7, P9.2 and P2.2;
P0.6 after P9.4, P0.5 and licence-key-v2's K2.4, K3.2 and K3.3.

**Critical path:** P1.0 → P2.1 → P3.1 → P4.1 → P4.2 → P3.6 → P5.1 → P6.1 → P6.2 → P6.3 → P5.2 (eleven
levels). Customer self-serve (P2–P4, reveal) is complete at level 6, so the first deploy can precede slice
P6.

Why some of it is sequential:

- `service.go` has three writers, P1.0 (level 0), P2.1 (level 1, which takes the seams onto `Service`) and
  P9.3 (level 7); `api.go` has three as well — P1.0 (0), P2.1 (1) and P1.6 (2) — which is why P1.6 is at
  level 2 and not beside P2.1; `merchant.go` has P4.2 then P3.6.
- Every API task needs the sign-in (P1.3), the store (P1.2) and the organisation model (P2.1).
- Paddle events create entitlements (P3.1) for transactions the checkout created (P4.1).
- Renewal, reveal, operations' key actions and deletion all need the entitlement-aware renewal (P3.6).
- Reseller accrual needs reseller organisations, and reports read statements.

### Shared-files table

Files written by more than one task. Every other file has one writer.

| File | Writers (level) |
| --- | --- |
| `ee/keyservice/service.go`, `startup_test.go` | P1.0 (0), P2.1 (1), P9.3 (7) |
| `ee/keyservice/store.go` | P3.6 (5) |
| `ee/keyservice/firestore_store.go` | P1.2 (0), P3.6 (5) |
| `ee/keyservice/keyservice_test.go` | P1.0 (0), P2.1 (1), P3.6 (5) |
| `ee/keyservice/merchant.go` | P4.2 (4), P3.6 (5) |
| `ee/keyservice/renew.go` | P3.6 (5) |
| `ee/keyservice/minter.go`, `policy.go` | P1.8 (0) |
| `ee/keyservice/api.go` | P1.0 (0), P2.1 (1), P1.6 (2) |
| `ee/keyservice/portalauth.go`, `portalauth_test.go` | P1.3 (0), P2.1 (1) |
| `go.mod`, `go.sum` | P1.2 (0), P1.6 (2), P1.7 (3) |
| `ee/keyservice/orgs.go` | P2.1 (1); P3.1 registers a hook, it does not edit the file |
| `ee/keyservice/wire_<area>.go`, `ee/portal/schemas/<area>.schema.json`, `ee/portal/src/api/types/<area>.ts` | P1.1 or P1.4 (0, 1), then the one task of that area (1 or later) |
| `ee/coordinator/mcpauth/firebase.go` | P1.3 (0) |
| `ee/keyservice/admin.go`, `admin_test.go`, root `firebase.json`, website key files | P9.3 (7) |
| `docs/licence-key-v2/*`, `docs/design/key-signing-service.md` | P9.1 (0) |
| `docs/gcp-live-checklist.md` | P9.4 (10) |

No level holds two writers of one file — which is what put P1.6 at level 2 rather than beside P2.1: both write
`api.go`. API routes register through `registerAPI` (P1.0) and screens through `import.meta.glob` (P1.4), so an
area adds its own routes and screens without editing the registry or the shell. What an area does edit is the
seam it is the first to reach — `api.go` for the interfaces a route is written against, `service.go` for the
seams `Service` holds, and `portalauth.go` for the adapter that puts the verifier behind them — and those
three sit in the table above for that reason, each with the level that first wrote it.

## Checks this plan takes (570–579)

All OWNER-RUN on `fugaro-staging` first. Each is one command, `scripts/live-checks/check-NNN.sh --project <p>`,
written by P9.4.

| Check | Measures |
| --- | --- |
| 570 | Pay before setup: a sandbox purchase leaves an unbound entitlement; binding to a project with no installation is refused (D2205). `fugaro setup` on a test installation follows, then `fugaro licence login` binds and mints. A seat purchase follows, and `fugaro licence refresh` shows the new seats with no paste |
| 571 | Every identity that reaches signing in the project: the minter's signer grant, invokers and `actAs` on the minter's account, `run.services.update` on the minter, `datastore.user`, Hosting deployers, `run.services.update` and `actAs` on the API's service and account (the minter's only invoker and the handover's only decrypter), any project-level role with `useToSign`. The owner reads the list (D2194's accepted risk) |
| 572 | An email-link account is refused by the operations and reseller API and sees the SSO dialog, on a phone-width window too |
| 573 | A redelivered sandbox event leaves the ledger unchanged; a replayed one outside the window is refused |
| 574 | A staging-signed key is refused by a production build |
| 575 | A staging reseller's month: accrual, statement mail, a mint refused over the limit |

576–579 are reserved.

## After deploy

The portal is Fugaro's own service, not an installation's: no `fugaro update` step and no
`### For operators` entry follow from it. The licence-key-v2 tasks that reach an installation keep their
own entries. When P9.3 lands, the website's deploy follows the portal's, because the key page points to
the portal.
