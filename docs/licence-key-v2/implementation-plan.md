# Licence key v2: implementation plan

Builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)** (decisions D2130–D2148,
findings F1–F20). **Nothing is implemented and no task is approved**: each task needs the owner's own
go, and answering a decision is not a go. Owner answers that shape the plan: one `fgk_` v1 format replaces the
built one (D2130); revocation through a visible job (D2131); seats enforced on `ee` surfaces only
(D2132, held by TN1); per-key `grace_days`, default 30 (D2133); a required `workspace_id` (the workspace's Firebase project id, TN2) beside
`organisation` (D2134, not the recommendation); a per-key renewal secret (D2135); the 14-day trial beside
D440's plans (D2136); the key in the runs bucket (D2137); Firebase Auth in the owner's licence project
(D2144, not the recommendation); a refund or a chargeback withdrawing the whole licence rather than the
key its event names (D2147), and `/renew` renewing only a key whose record names the subscription the
checkout sold it for, refusing a key that names none (D2148 — TN6 is what §12.1's Enterprise column needs
before K2.6, K2.7 and K4.1 can assume otherwise).

It **supersedes** licensing task L3.1 and subscriptions S2.1/S2.3 (the format) and amends S2.4, S2.5,
S2.7–S2.10, S3.1–S3.4 and S6.1 where they name the old claims; K7.2 writes those notes.

**Amended 2026-11-13 by the portal (P9.1,
[implementation-plan.md](../portal/implementation-plan.md),
[reconciliation.md](../portal/reconciliation.md) §"What this changes in licence key v2"): this plan's
server is the portal's API.** K2.4 uses P1.3's verifier, registers its routes through P1.0's `/api/`
registry and its pages are the SPA's (P3.5); K2.6 is superseded by P5.1, so the admin page is the
portal's operations API and screens; K2.7 drops its edge on K2.6, registers through the same registry
and keys trials on the organisation and the workspace's Firebase project id (D2201); K5.1 is
superseded by the portal's screens; K2.5's purchase mail carries a link and never a key; K3.3 sends
the installation's Firebase project id and binds an entitlement; K3.4 follows D2186, the grants node's
array order; and K4.1 keeps its secret when `/renew` answers without a new one, with connected keys
renewing monthly (D2200, D2202). D2194 puts the service in `fugaro-prod` (the portal plan's "Limits"
states the cost); the portal's own numbers are D2180–D2207, and the notes below cite them beside this
plan's D2130–D2146.

## Conventions every part follows

- **`ee/` rules** (`.claude/rules/ee.md`): every new file under `ee/` carries the SPDX header
  `LicenseRef-Fugaro-Subscription`; the core never imports `ee/` and never reads a key (D524); commands
  register through `ee/register`.
- **No secret-shaped literal** in tests, docs, logs or fixtures; a test key is built at run time from a
  generated keypair, and a public-key literal goes through the image secret scan first
  (`images/trivy-secret.yaml`, licensing U5).
- **No live cloud action by an agent.** Minting with the production key, deploying the key service,
  creating the Paddle account and every live check are owner-run; agents use `internal/gcpfake`,
  `internal/testutil` and `t.TempDir()`.
- **Docs are tested.** Changing a doc runs `go test ./internal/cli/ -run Docs`; website content runs
  `npm run check` in `website/`.
- **`fugaro update` is the whole upgrade.** K4.2 ships the Terraform and the converge test in the same PR.
- **A field never changes meaning.** The key's `v`, the renewal and status responses and the project-store
  object are new names; none reuses an old one.
- **Security tasks** run at `opus + mandatory independent review` (the in-job level-3 review; no local
  security reviewer on top, per the owner's ruling of 2026-11-07).
- A task's own PR does not touch `docs/status.md`.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A key is verified over its raw bytes before any claim is read; no claim from an unverified payload reaches a refusal or a log | K1.1 `TestNoClaimIsReadBeforeTheSignatureVerifies` |
| A higher `v` is refused with the upgrade message and never falls back to Free | K1.1 `TestHigherPolicyVersionIsRefusedNotFree` |
| Unknown fields are ignored for signed keys only (D2146) | K1.1 `TestUnknownFieldsInASignedKeyAreIgnored`, `TestUnknownFieldsNeedAVerifiedSignature` |
| `fugaro licence set` never takes a key in argv (F5) | K3.2 `TestLicenceSetRefusesAKeyInArgv` |
| The core never imports a licence package and an untagged build has no `licence` command | K3.2 `TestUntaggedBuildHasNoLicenceCommand`, `internal/eeboundary` |
| An outage or an unreachable server never revokes (D2140) | K4.1 `TestAnUnreachableServerNeverRevokes` |
| Renewal and status reveal nothing to a caller without the secret; unknown and wrong answer identically | K2.3 `TestRenewalAnswersAreUniform` |
| The renewal secret is never in the key, a log or an image | K2.1 `TestKeyHoldsOnlyTheSecretsHash`, K4.2 `TestRunImagesCarryNoKey` |
| A seat decrease never retroactively removes the last admin's seat | K3.4 `TestAdminsAlwaysKeepASeat` |
| Revocation and an expired key behave as D436 read-only, never delete anything | K4.1, K3.4 `TestLapseNeverDeletes` |
| `update` converges an installation at the previous version, runs in flight untouched | K4.2 `TestUpdateAddsTheRenewalJobAndLeavesARunInFlightRunning` |

## Limits this plan accepts

Revocation is **advisory**: the customer's operators can write the runs bucket and delete the job, so a
determined holder keeps a revoked key until it expires. The key is readable by every identity that reads the
bucket, a copy is refused in another workspace by the required `workspace_id` but works inside the same one; it cannot renew, since
the key holds only the secret's hash. "One trial per installation" rests on a caller-supplied id and a free
Firebase account, so it is a speed bump (K2.7 also throttles per domain). The device approve page names the
requesting installation and codes expire quickly, against phishing of the code. The loopback listener binds
the literal `127.0.0.1`.

## Slices and tasks

Reviewer tier is **standard** unless a task says `SECURITY`.

### Slice K0: owner-run

- **K0.1 Counsel and privacy text. OWNER-RUN.** The Terms' refund and "no revocation" text, and the
  privacy notice, change for the renewal and status contact (it sends `key_id`, `workspace_id` and the
  renewal secret from the customer's project). The owner sends the counsel packet; no agent edits the Terms.
- **K0.2 Production signing keys (subscriptions S3.8). OWNER-RUN.** Creates the KMS keys, then S2.9's task
  publishes the public half.

### Slice K1: the key and its verifiers (inert until a key exists)

- **K1.1 The v1 verifier. SECURITY.** Files: `ee/licence/keyv1.go` (new; K7.3 renames it to `key.go`), `ee/licence/keyv1_test.go` (new; `key_test.go` after K7.3), `ee/licence/testdata/vectors_v1.json` (new). **Additive: the old `key.go`, `store.go` and their tests stay untouched so `-tags ee` keeps compiling on `main` until K7.3 removes them.** Interfaces: produces `ClaimsV1`
  (`v`, `kid`, `key_id`, `workspace_id` (required: the workspace's Firebase project id), `organisation`, `tier` (`community|team|enterprise`),
  `seats`, `issued_at`, `paid_through`, `grace_days`, `renewal_mode`, `renew_before_days`,
  `check_interval`, `renewal_secret_hash`, `warnings`, `kind`, `note`), the text `fgk_<payload>.<sig>`,
  `State` (`active|grace|expired`), the refusal for a higher `v`, ISO durations `P<n>D|P<n>W`
  (F16), and the tier's maximum lifetime kept as `paid_through - issued_at`. Consumed by every later task.
  Schema change: the key format (a new name, `v: 1`). Tests: `TestVerifyFgkKey`,
  `TestNoClaimIsReadBeforeTheSignatureVerifies`, `TestHigherPolicyVersionIsRefusedNotFree`,
  `TestUnknownFieldsInASignedKeyAreIgnored`, `TestUnknownFieldsNeedAVerifiedSignature`,
  `TestStagingKeyIDIsRefused`, `TestKeyBeyondTheTierLifetimeIsRefused`, `TestGraceUsesTheKeysOwnDays`,
  `TestKeyWithoutWorkspaceIDIsRefused`, `TestWorkspaceIDMustMatchTheFirebaseProject` (the verifier takes a `FirebaseProjectID` the caller passes in, read by K3.1 and the console from the project's own config in the same read as the key). Command: `go test ./ee/licence/... -tags ee -run 'Test(VerifyFgk|NoClaim|Higher|Unknown|Unsigned|Staging|KeyBeyond|Grace|KeyWithout|Workspace)' -count=1`.
  Depends on: none. Commit: `ee: the v1 licence key (fgk_) and its verifier (D2130)`. Red step:
  `TestHigherPolicyVersionIsRefusedNotFree` fails against the old verifier (it has no `v`). The `v1` types are named `ClaimsV1`/`VerifyV1`; K7.3 renames them to `Claims`/`Verify` when the old ones go.
- **K1.3 The mint tool.** Files: `ee/licence/mint/main.go`, `mint.go`, `mint_test.go`. Flags for every v1 field,
  `-kind`, `-grace-days`, `-renewal-mode`, `-check-interval`; still signs only through the KMS signer and
  verifies its own output against K1.1. Tests: `TestMintedKeyVerifies`, `TestMintRefusesAKeyTheVerifierRefuses`,
  `TestTheToolHoldsNoWayToSignButTheSigner`. Command: `go test ./ee/licence/mint/... -tags ee -count=1`.
  Depends on: K1.1. Commit: `ee: the mint tool writes v1 keys`. It calls `VerifyV1`. **OWNER-RUN to use.** SECURITY.
- **K1.4 The console verifier.** Files: `console/src/main/licence.ts`, `licence-state.ts`,
  `licence-store.ts`, their `*.test.ts`. Reads K1.1's vectors; same refusals, same states, the upgrade
  message. Tests: `licence.test.ts` (vector parity). Reading the project key's state is K3.6. Command: `cd console && npm run test:unit -- licence --maxWorkers=2`.
  Depends on: K1.1. Commit: `console: the v1 licence key verifier`. SECURITY.
- **K1.5 Rewrite the key note.** Files: `docs/licensing/licence-key.md` (the v1 text; the old design's
  surviving rules, the revocation and limits sections rewritten). Tests: `go test ./internal/cli/ -run Docs -count=1`.
  Depends on: none (the text follows the amended decisions). Commit: `docs(licensing): the licence key note, v1`.

### Slice K2: the licence server (`ee/keyservice`; not deployed by any task here)

- **K2.1 The default policy and the minter. SECURITY.** Files: `ee/keyservice/policy.go` (new),
  `ee/keyservice/minter.go`, `ee/keyservice/minterclient.go`, tests. The per-tier table of the spec's §12.1
  with D2133/D2138 applied (grace 30; trial 0, air-gapped 30; `check_interval` `P1D` for trial and `null` for air-gapped), the renewal secret generated per key and only its hash in the claim, `key_id`
  generation. Tests: `TestDefaultPolicyPerTier`, `TestKeyHoldsOnlyTheSecretsHash`,
  `TestAirGappedKeyNeverChecks`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestDefault|TestKeyHolds|TestAirGapped' -count=1`.
  Depends on: K1.1. Commit: `ee: the key service mints v1 keys from the tier policy`.
- **K2.2 The store. SECURITY.** Files: `ee/keyservice/store.go`, tests. Adds the revocation list
  (`key_id`, reason, time), the renewal secret hash and last-seen per key, one trial per installation
  and per account. Tests: `TestRevokedKeyStaysRevoked`, `TestOneTrialPerInstallation`,
  `TestRevocationIsIdempotent`. Depends on: K1.1. Commit: `ee: the key store holds revocations and trials`.
- **K2.3 Renewal and status. SECURITY.** Files: `ee/keyservice/renew.go` (new; it registers `/renew` and
  `/status` through the portal's P1.0 `/api/` registry), `renew_test.go` (new). The caller sends `key_id` and the renewal secret over TLS and the server compares the secret's hash with the one it holds
  (no HMAC: the hash is in the key, which every reader of the runs bucket holds, so it must not be a signing key); response
  `{status: ok|revoked, newer_key: bool}` and the renewal's new key; uniform answers; throttled; never
  returns a key to a revoked or unknown `key_id`; **it renews only a key whose record names the
  subscription the checkout sold it for, refusing one that names none with the same uniform answer
  (D2148, failing closed), and it mints only into a term that subscription's own end already reaches —
  the merchant's renewal is what moves that end and this route never writes it (D2148)**; a replayed
  request is idempotent; a renewal returns the next key **and its new renewal secret** (reissue under a new `key_id` rotates it). **Secret handling:** 256 bits from the system CSPRNG; shown once together with the key as a two-line bundle (key line, secret line) by every issue path (the purchase email, the account page's download, the authorize code exchange and the device poll, the admin page and the mint tool), so each of those tasks returns the bundle and a re-download **rotates** the secret; after a rotation the old secret still fetches the new one for 24 hours only, then is refused. Tests:
  `TestRenewalAnswersAreUniform`, `TestTheKeyAloneCannotRenew`, `TestRenewalRefusesAWrongSecret`, `TestRevokedKeyIsNeverRenewed`,
  `TestStatusCarriesNoKey`, `TestReplayedRenewalIsIdempotent`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestRenewal|TestTheKeyAlone|TestRevoked|TestStatus|TestReplayed' -count=1`.
  Depends on: K2.1, K2.2. Commit: `ee: the renewal and status endpoints`.
  **Amended 2026-11-13 by the portal:** `service.go` is the portal's P1.0 — it mounts the `/api/`
  registry, and no licence-key-v2 task edits it (the portal's P9.3 is the file's other writer, at the
  portal plan's own level) — so this task's `/renew` and `/status` lines register from `renew.go`.
- **K2.4 Sign-in, authorize and device code. SECURITY.** Files: `ee/keyservice/signin.go` (new; it
  registers its routes through the portal's P1.0 `/api/` registry), `signin_test.go` (new). Verifies Firebase ID tokens of the owner's licence
  project by reusing `ee/coordinator/mcpauth/firebase.go` (audience, issuer and `email_verified` pinned; the purchaser binding depends on the last), serves `/cli/authorize`
  (port, `state`, PKCE challenge), the one-time `code` exchange, and the device-code start, poll and
  approve routes. Tests: `TestAuthorizeRefusesANonLoopbackRedirect`, `TestCodeIsSingleUse`,
  `TestPKCEVerifierIsRequired`, `TestDeviceCodeExpiresAndIsRateLimited`, `TestUnknownAndExpiredDeviceCodesAnswerAlike`,
  `TestTokenFromAnotherProjectIsRefused`. Depends on: K2.3, the portal's P1.3 (the verifier) and P1.0
  (the registry).
  Command: `go test ./ee/keyservice/ -tags ee -run 'TestAuthorize|TestCode|TestPKCE|TestDeviceCode|TestUnknownAndExpired|TestTokenFrom' -count=1`.
  Commit: `ee: the licence site's sign-in, authorize and device code`.
  **Amended 2026-11-13 by the portal:** the token check is P1.3's verifier
  (`ee/keyservice/portalauth.go`: audience, issuer, `email_verified`, `RequireSSO`, `FreshWithin`, the
  tenant check), not a second reader of `ee/coordinator/mcpauth/firebase.go`; the routes register
  through P1.0's `/api/` registry from this task's own `signin.go`, so `service.go` is P1.0's and
  nothing here edits it; and the authorize and device-approve pages are the SPA's
  (`ee/portal/src/screens/cli/*`, P3.5), not the website's.
- **K2.5 Paddle events to v1. SECURITY.** Files: `ee/keyservice/merchant.go`, `merchant_test.go`. Maps a
  completed transaction (renewal included) to a v1 key (Team monthly and annual), a refund or
  chargeback to a revocation (D2131) **of every key the licence holds — the purchase, a seat change's
  reissue, the merchant's own renewal and the rotations of each (D2147)** — a seat change to a reissue;
  Enterprise is not sold here (D2141).
  Tests: `TestRefundRevokesTheKey`, `TestSeatChangeReissuesUnderANewKeyID`, `TestEnterpriseIsNotACheckoutPlan`.
  Depends on: K2.1, K2.2. Command: `go test ./ee/keyservice/ -tags ee -run 'TestRefund|TestSeatChange|TestEnterprise' -count=1`.
  Commit: `ee: Paddle events mint, reissue and revoke v1 keys`.
  **Amended 2026-11-13 by the portal:** the purchase mail carries a link and never a key
  (`TestPurchaseMailCarriesNoKey`, P4.2), and P4.2 writes `merchant.go` for the portal — its adapter
  maps the notification to a canonical event and a purchase creates an entitlement, never a key
  (D2196), with the route at `/api/paddle/webhook` (P1.0).
- **K2.6 The admin page. SECURITY.** Files: `ee/keyservice/admin.go` (new), `service.go`, `admin_test.go` (new).
  Owner-only: reseller and comped keys (`kind: comped` with an expiry, excluded from revenue reporting),
  lookup by workspace or `key_id`, revoke, reissue, override policy; every action is recorded. Tests:
  `TestAdminRefusesAnyoneButTheOwner`, `TestCompedKeyHasAnExpiry`, `TestAdminActionsAreRecorded`.
  Depends on: K2.4 (`service.go`). Command: `go test ./ee/keyservice/ -tags ee -run 'TestAdmin|TestComped' -count=1`.
  Commit: `ee: the founder's admin page`.
  **Superseded by the portal (P5.1; D2187 of 2026-11-12).** The page is the portal's operations API
  (`ee/keyservice/ops.go`, `api_ops.go`) and its screens (P5.3): owners and admins act, a member only
  reads, every action needs a sign-in no older than 5 minutes with Google, D454's per-tier rate queues
  the excess (carried from this task's `MintCaps`) and D456's audit covers every action, reads included.
  Nothing of this task is built, and the level and shared-files tables below no longer schedule it.
- **K2.7 The 14-day Team trial. SECURITY.** Files: `ee/keyservice/trial.go` (new; it registers its route
  through the portal's P1.0 `/api/` registry), `trial_test.go` (new). A signed-in account asks for a trial for an installation (`kind: trial`, 14 days,
  grace 0, `check_interval` `P1D`); one per installation and account. Trial uniqueness is keyed by the Firebase project id, which is harder to mint than a caller-supplied name. Tests: `TestSecondTrialIsRefused`,
  `TestTrialIsTeamTierFourteenDays`. Depends on: K2.2 and the portal's P1.0 (the registry). Command: `go test ./ee/keyservice/ -tags ee -run 'TestSecondTrial|TestTrialIs' -count=1`.
  Commit: `ee: the Team trial`.
  **Amended 2026-11-13 by the portal:** it drops its edge on K2.6, which P5.1 supersedes, and registers
  its route through P1.0's `/api/` registry rather than editing `service.go`; and a trial is keyed on
  the organisation and the workspace's Firebase project id (D2201), not on an installation name a
  caller supplies.

### Slice K3: the project side (`ee/register`, `ee/licence`)

- **K3.1 The project-level store. SECURITY.** Files: `ee/licence/project/store.go` (new package, so the verifier's package keeps its no-network import test), `store_test.go`
  (new). Reads and writes the key object in the installation's runs bucket (through `internal/blobx`), the
  last-check record and the unreachable counter; at most two keys (overlap, T26). Tests:
  `TestProjectStoreKeepsTwoKeys`, `TestTheStoreNeverLogsAKey`, `TestAViewerReadsTheStateNotTheKey`.
  Command: `go test ./ee/licence/project/ -tags ee -run 'TestProjectStore|TestTheStore|TestAViewer' -count=1`. Where the per-machine `licence.key` and the project key differ, the project key wins and `status` says so.
  Depends on: K1.1. Commit: `ee: the licence key at project level (D2137)`.
- **K3.2 `fugaro licence set | status | refresh`. SECURITY.** Files: `ee/register/licence.go` (new),
  `ee/register/licence_test.go` (new), `internal/cli/agentsession.go` (new: exports the agent-session refusal that `ee/` commands call, since `agentMarker` is unexported), `internal/cli/untagged_licence_test.go` (new), `internal/clidoc` entries only if the clidoc table requires one.
  `set` takes the key (line 1) and the renewal secret (line 2, absent for an offline key) from stdin or `--file`, refuses a positional key, writes the key to the project store and the secret to the installation's Secret Manager secret, refuses inside a coding agent's session as every cloud write does, writing only to the Secret Manager secret that `fugaro update` created (K4.2) and refusing with one line ("run `fugaro update`") before it exists, so `update` never meets a secret it did not create; `status` prints tier, seats, expiry, state and last check and
  has `--json` with new field names; `refresh` calls K2.3 once. Hidden alias `license`. Tests:
  `TestLicenceSetRefusesAKeyInArgv`, `TestLicenceStatusJSONShape`, `TestUntaggedBuildHasNoLicenceCommand` (in `internal/cli`, no `ee` tag),
  `TestLicenceSetNeverPrintsTheKey`, `TestLicenceWritesAreRefusedInAnAgentSession` (set, refresh, login, seats assign), `TestLicenceAliasLicenseWorks`. Command: `go test ./ee/register/ -tags ee -run 'TestLicence' -count=1` and `go test ./internal/cli/ -run 'TestUntaggedBuildHasNoLicenceCommand' -count=1`.
  Depends on: K1.1, K3.1, K2.3. Commit: `ee: fugaro licence set, status and refresh`. `cli.RegisterCommand` is top-level only, so K3.2 owns `licence.go` and registers a subcommand table that K3.3, K3.4 and K4.1 fill from their own files.
- **K3.3 `fugaro licence login`. SECURITY.** Files: `ee/licence/login/login.go` (new package, network allowed), `ee/register/licence_login.go`
  (new), tests. The loopback listener (127.0.0.1, random port, one request, `state`, PKCE, 5-minute
  deadline) and the device-code fallback (`--device`, or no browser). Tests:
  `TestLoopbackBindsOnlyTheLoopback`, `TestCallbackWithTheWrongStateIsRefused`,
  `TestTheCodeIsNeverLogged`, `TestDevicePollStopsAtExpiry`. Command: `go test ./ee/licence/login/ ./ee/register/ -tags ee -run 'TestLoopback|TestCallback|TestTheCode|TestDevicePoll' -count=1`. Depends on: K2.4, K3.2. Commit: `ee: fugaro licence login`. The device approve page shows the requesting installation's name as untrusted text and requires typing the user code.
  **Amended 2026-11-13 by the portal:** the request carries the installation's Firebase project id,
  and the portal's authorize and device-approve screens list the organisation's unbound entitlements
  and **binds the chosen one** to it, with the id shown as untrusted text (P3.5).
- **K3.4 Seats. SECURITY. HELD until the owner says enforcement ships (TN1).** Files: `ee/register/seats.go`
  (new), `ee/register/lapse.go`, `internal/access/seats.go` (new: an `access.SetSeatLimit` seam taking the caller's identity, consulted while the key is active, set from `ee/register`, default limits nothing; the core still reads no key), `ee/register/seats_test.go` (new). Records seat holders on the project's
  operator and admin lists (D2139), `fugaro licence seats assign|rm|ls`, and the `access` seam answers an
  unseated operator as a viewer on Subscription surfaces; admins always keep a seat; a seat decrease applies from the next key (spec §3.3); a lapse changes
  nothing stored. Tests: `TestAnUnseatedOperatorActsAsAViewer`, `TestAdminsAlwaysKeepASeat`,
  `TestSeatsAreNotEnforcedInTheMITCLI`, `TestLapseNeverDeletes`. Command: `go test ./ee/register/ ./internal/access/ -tags ee -run 'TestAnUnseated|TestAdminsAlways|TestSeatsAre|TestLapseNever' -count=1`. Depends on: K1.1, K3.1, K3.2 (the command
  family's file `licence.go` is read, not edited). Commit: `ee: seats are assigned and enforced on the paid surfaces`.
  **Amended 2026-11-13 by the portal (D2186):** who is over the seat limit is the grants node's array
  order — admins always hold a seat and operators fill the remaining seats **in the order they were
  granted** — and no field is added, because `internal/access/store.go` decodes the node with
  `DisallowUnknownFields` and an installed reader would refuse it. Held with this task under TN1.
- **K3.5 Warnings.** Files: `ee/register/warn.go` (new), `warn_test.go` (new); the window rules of the key's
  `warnings` (roles and `grace_start[+Nd]`), a line on each tagged-CLI ee command, and the field the console
  and app read from `licence status --json`. Delivered by the console, app and ee commands only (F6).
  Tests: `TestAdminsOnlyInTheFirstWeekOfGrace`, `TestEveryoneInTheLast`, `TestNoWarningWhenActive`.
  Command: `go test ./ee/register/ -tags ee -run 'TestAdminsOnly|TestEveryone|TestNoWarning' -count=1`. Depends on: K3.2. Commit: `ee: licence warnings by role and window`.
- **K3.6 The console reads the project key's state.** Files: `console/src/main/licence-state.ts`, `licence-state.test.ts`. Reads `fugaro licence status --json` and the warning field; no key is Free (D2143). The app's reader is the amended S2.5 and is not in this plan. Command: `cd console && npm run test:unit -- licence-state --maxWorkers=2`. Depends on: K1.4, K3.5. Commit: `console: the project licence state and warnings`. SECURITY.

### Slice K4: renewal and revocation job

- **K4.1 `fugaro licence renew --job`. SECURITY. The effect on the console's lapse is HELD with K3.4.**
  Files: `ee/register/licence_renew.go` (new), `licence_renew_test.go` (new). Run by the Scheduler job:
  reads the key and the renewal secret, asks `/status` at `check_interval`, asks `/renew` from
  `renew_before_days`, writes the next key (overlap kept), records last-ok and the failure count, never
  revokes on an error, and sets the 30-day "cannot verify revocation" warning. Tests:
  `TestAnUnreachableServerNeverRevokes`, `TestRenewalWritesTheNextKeyBesideTheOld`,
  `TestRevokedKeyIsRecordedNotDeleted`, `TestOfflineKeyNeverContactsTheServer`. It verifies a renewed key (and its `workspace_id`) before writing, writes **the new secret version first, then the key**; if the stored key's hash matches no stored secret (a crash between the two), it retries with the previous secret version (inside the 24-hour window). Command: `go test ./ee/register/ -tags ee -run 'TestAnUnreachable|TestRenewalWrites|TestRevokedKey|TestOffline' -count=1`. Depends on: K1.1, K3.1, K2.3, K3.2.
  Commit: `ee: the renewal and revocation job`.
  **Amended 2026-11-13 by the portal:** it keeps its secret when `/renew` answers without a new key —
  the server returns the entitlement's newest key, and a caller that already holds it is not rotated
  (D2200) — and a connected key's term is at most 30 days, renewed while the entitlement runs, so a
  connected installation renews monthly (D2202).
- **K4.2 The Scheduler job and the update. SECURITY.** Files: `deploy/terraform/gcp/modules/installation/licence.tf`
  (new), `variables.tf`, `deployer.tf` (the job's permissions), `deploy/terraform/gcp/roots/*` only if a root
  names the switch, `internal/cli/update_licence.go` (new), tests, `docs/releases/v<next>.md`
  (`### For operators`: run `fugaro update`; it applies the licence job and its secret). The job reuses the key-gated image of the coordinator and is created **under the coordinator's own condition** (`enable_budget && coordinator != null && deploy_job`, `modules/installation/coordinator.tf`); an installation without it has no tagged image, no job, and `licence status` says renewal is by hand (TN5), the Secret Manager secret
  of the renewal secret, and a service account that reads and writes the key object and nothing else; where the condition holds it is **always applied** (the core
  never reads a key, D524) and its command no-ops with no key or an offline key. On a managed project it is
  applied without a prompt; elsewhere `update` asks as for any new resource. It also creates the empty Secret
  Manager secret for the renewal secret and grants the job's account `secretmanager.versions.add` on that one
  secret only. Tests:
  `TestUpdateAddsTheRenewalJobAndLeavesARunInFlightRunning`, `TestRunImagesCarryNoKey`,
  `TestRenewalServiceAccountReadsOnlyTheKeyObject` (terraform tag). Command: `go test -tags terraform ./internal/infra/... -run Licence -count=1`
  and `go test ./internal/cli/ -run 'TestUpdateAdds' -count=1` and `go test ./images/ -run TestRunImagesCarryNoKey -count=1`. Depends on: K4.1.
  Commit: `ee: update applies the licence renewal job`.

### Slice K5: the licence site

- **K5.1 The account, authorize and device pages.** Files: `website/src/pages/licence/*.astro` (new),
  `website/test/licence.mjs` (new), the CSP `form-action` check. Firebase Auth sign-in, account page (seats,
  key download, a Paddle invoice link), the authorize and device pages. Content only: the server is K2.4.
  Tests: `website/test/licence.mjs`; command `cd website && npm run check`. Depends on: K2.4.
  Commit: `website: the licence account and sign-in pages`. Reviewer: standard; **SECURITY** if it adds a
  script or a form target.
  **Superseded by the portal (P3.4, P3.5; D2193).** The account, authorize and device pages are the
  SPA's screens under `ee/portal/src/screens/*`, so `website/src/pages/licence/*.astro` and
  `website/test/licence.mjs` are not written; the website's own keys page points at the portal (P9.3),
  and the portal's hosting config carries the CSP (P9.2).
- **K5.2 The Paddle account (subscriptions S8.5). OWNER-RUN.**

### Slice K6: the setup wizard

- **K6.1 The wizard's key step.** Files: the setup wizard's step table (the owner's setup-wizard plan names
  the file; this task adds one step and waits for that plan's key-step slot). Optional step: browser sign-in
  (default) or paste; on success calls `licence set`. Depends on: K3.3. Commit: `setup: an optional licence key step`.
  UNVERIFIED: the wizard task that owns the step table.

### Slice K7: notes and release

- **K7.2 Supersede notes.** Files: `docs/licensing/implementation-plan.md`, `docs/subscriptions/implementation-plan.md`,
  `docs/design/key-signing-service.md` (a dated "superseded by licence-key-v2" note where S2.1, S2.3, S2.4,
  S3.1–S3.4 name the old claims). Depends on: none. Commit: `docs: licence key v2 supersedes the old key's claims`.

- **K7.3 Remove the old key format.** Files: `ee/licence/key.go`, `store.go`, `key_test.go`, `testdata/vectors.json` (the old format: the verifier and its Go readers go, but **the fixture stays** -- the console's verifier of that format and the app's read it at module top level, so it leaves with those two readers and not with the Go verifier; `keyv1.go` renamed to the package's `Claims`/`Verify`). Tests: `go test ./ee/... -tags ee -count=1` compiles and passes with no reference to the old format. Depends on: K1.3, K2.1, K2.5, K3.1. Commit: `ee: the old licence key format is removed`. SECURITY.

## Parallel execution

Edges are the "Depends on" lines.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | K0.1, K0.2 (owner); K1.1, K1.5, K7.2 |
| 1 | K1.3, K1.4, K2.1, K2.2, K3.1 |
| 2 | K2.3, K2.5, K2.7 |
| 3 | K2.4, K3.2, K7.3 |
| 4 | K3.3, K3.5, K4.1 |
| 5 | K3.6, K4.2, K6.1 |
| held | K3.4 (the owner's enforcement go, TN1), after level 3 |

**Critical path:** K1.1 → K2.1 → K2.3 → K2.4 → K3.3 → K6.1 (six levels; K2.3 → K3.2 → K3.5 → K3.6 is as long). The sequence K2.3 → K2.4 → K2.6 → K2.7 on
`ee/keyservice/service.go` is gone: the portal's P1.0 owns that file and mounts the `/api/` registry,
so K2.3, K2.4 and K2.7 register their routes from `renew.go`, `signin.go` and `trial.go` and K2.7 moves
to level 2 with its edge on K2.6 dropped (2026-11-13). K4.1 → K4.2 is sequential because the job's
runtime is what the Terraform runs.

### Shared-files table

| File | Writers (level) |
| --- | --- |
| `ee/licence/keyv1.go` (K7.3 renames it to `key.go`), `testdata/vectors_v1.json` | K1.1 (0) |
| `ee/licence/key.go`, `store.go`, `key_test.go`, `testdata/vectors.json` (the old format: K7.3 drops the verifier and keeps the fixture) | K7.3 (3) |
| `ee/licence/mint/*` | K1.3 (1) |
| `console/src/main/licence.ts`, `licence-store.ts`, `licence-state.ts` | K1.4 (1), K3.6 (5; only `licence-state.ts`) |
| `docs/licensing/licence-key.md` | K1.5 (0) |
| `ee/keyservice/minter.go`, `minterclient.go`, `policy.go` | K2.1 (1) |
| `ee/keyservice/store.go` | K2.2 (1) |
| `ee/keyservice/service.go` | nobody here: the portal's P1.0 (level 0 of the portal plan) mounts the `/api/` registry and its P9.3 (level 7) is the file's other writer, so K2.3, K2.4 and K2.7 register their routes from `renew.go`, `signin.go` and `trial.go` |
| `ee/keyservice/merchant.go` | K2.5 (2); the portal's P4.2 (level 4 of the portal plan) writes the portal's event adapter into it, and the portal's P3.6 (level 5) edits it after |
| `ee/licence/project/store.go` | K3.1 (1) |
| `internal/cli/agentsession.go`, `untagged_licence_test.go` | K3.2 (3) |
| `ee/register/licence.go` | K3.2 (3), the subcommand table; later tasks add their own files |
| `internal/access/seats.go` | K3.4 (held) |
| `ee/register/lapse.go`, `seats.go` | K3.4 (held) |
| `ee/register/warn.go` | K3.5 (4) |
| `ee/register/licence_renew.go` | K4.1 (4) |
| `ee/register/licence_login.go`, `ee/licence/login/login.go` | K3.3 (4) |
| `deploy/terraform/.../installation/licence.tf`, `variables.tf`, `deployer.tf`, `internal/cli/update_licence.go` | K4.2 (5) |
| `docs/licensing/implementation-plan.md`, `docs/subscriptions/implementation-plan.md`, `docs/design/key-signing-service.md` | K7.2 (0) |

No level holds two writers of one file. A change to any task's Files re-checks this table.

## Checks this plan takes (560–569)

All **OWNER-RUN**; the owner types the script's one command, no agent performs a step.

- **Check 560:** a real renewal through the deployed Scheduler job in the sandbox installation: a short-lived
  `renewal_mode: auto` key renews itself and `licence status` shows the new key (K4.2, K2.3).
- **Check 561:** revocation: the owner revokes a test key on the admin page and the next check records it
  (K2.6, K4.1; the page is the portal's operations screens, P5.3, since 2026-11-13); and an unreachable
  server does not revoke (block egress, wait one interval).
- **Check 562:** browser sign-in against the staging site, including the wrong-`state` refusal (K3.3, K2.4).
- **Check 563:** the device code from a machine with no browser (K3.3).
- **Check 564:** a Paddle sandbox purchase, renewal and refund end to end (K2.5; subscriptions S8.5 first).

## After deploy

Release-actions entries go in the PR of the task that needs them: K4.2's `### For operators` (the
`fugaro update` line and what it asks), and K0.2/S2.9's publication of the production public key.
