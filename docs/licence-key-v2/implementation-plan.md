# Licence key v2: implementation plan

Builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)** (decisions D2130–D2146,
findings F1–F18). **Nothing is implemented and no task is approved**: each task needs the owner's own
go, and answering a decision is not a go. Owner answers that shape the plan: one `fgk_` v1 format replaces the
built one (D2130); revocation through a visible job (D2131); seats enforced on `ee` surfaces only
(D2132, held by TN1); per-key `grace_days`, default 30 (D2133); an optional `workspace_id` beside
`organisation` (D2134, not the recommendation); a per-key renewal secret (D2135); the 14-day trial beside
D440's plans (D2136); the key in the runs bucket (D2137); Firebase Auth in the owner's licence project
(D2144, not the recommendation).

It **supersedes** licensing task L3.1 and subscriptions S2.1/S2.3 (the format) and amends S2.4, S2.5,
S2.7–S2.10, S3.1–S3.4 and S6.1 where they name the old claims; K7.2 writes those notes.

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
| Unknown fields are ignored for signed keys only (D2146) | K1.1 `TestUnknownFieldsInASignedKeyAreIgnored`, `TestUnsignedFormatsStayStrict` |
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
bucket, so with `workspace_id` optional (D2134) a copied key works in another project; it cannot renew, since
the key holds only the secret's hash. "One trial per installation" rests on a caller-supplied id and a free
Firebase account, so it is a speed bump (K2.7 also throttles per domain). The device approve page names the
requesting installation and codes expire quickly, against phishing of the code. The loopback listener binds
the literal `127.0.0.1`.

## Slices and tasks

Reviewer tier is **standard** unless a task says `SECURITY`.

### Slice K0: owner-run

- **K0.1 Counsel and privacy text. OWNER-RUN.** The Terms' refund and "no revocation" text, and the
  privacy notice, change for the renewal and status contact (it sends `key_id`, `workspace_id` and an
  HMAC from the customer's project). The owner sends the counsel packet; no agent edits the Terms.
- **K0.2 Production signing keys (subscriptions S3.8). OWNER-RUN.** Creates the KMS keys, then S2.9's task
  publishes the public half.

### Slice K1: the key and its verifiers (inert until a key exists)

- **K1.1 The v1 verifier. SECURITY.** Files: `ee/licence/key.go`, `ee/licence/store.go`,
  `ee/licence/key_test.go`, `ee/licence/testdata/vectors.json`. Interfaces: produces `Claims` v1
  (`v`, `kid`, `key_id`, `workspace_id` optional, `organisation`, `tier` (`community|team|enterprise`),
  `seats`, `issued_at`, `paid_through`, `grace_days`, `renewal_mode`, `renew_before_days`,
  `check_interval`, `renewal_secret_hash`, `warnings`, `kind`, `note`), the text `fgk_<payload>.<sig>`,
  `State` (`active|grace|expired`), the refusal for a higher `v`, ISO durations `P<n>D|P<n>W`
  (F16), and the tier's maximum lifetime kept as `paid_through - issued_at`. Consumed by every later task.
  Schema change: the key format (a new name, `v: 1`). Tests: `TestVerifyFgkKey`,
  `TestNoClaimIsReadBeforeTheSignatureVerifies`, `TestHigherPolicyVersionIsRefusedNotFree`,
  `TestUnknownFieldsInASignedKeyAreIgnored`, `TestUnsignedFormatsStayStrict`,
  `TestStagingKeyIDIsRefused`, `TestKeyBeyondTheTierLifetimeIsRefused`, `TestGraceUsesTheKeysOwnDays`,
  `TestWorkspaceIDIsCheckedOnlyWhenPresent`, against a `ProjectID` the caller passes in (the installation's GCP project id, supplied by K3.1 and the console; the claim names that id and nothing broader). Command: `go test ./ee/licence/... -tags ee -run 'Test(Verify|No|Higher|Unknown|Unsigned|Staging|Key|Grace|Workspace)' -count=1`.
  Depends on: none. Commit: `ee: the v1 licence key (fgk_) and its verifier (D2130)`. Red step:
  `TestHigherPolicyVersionIsRefusedNotFree` fails on the old verifier (it has no `v`).
- **K1.3 The mint tool.** Files: `ee/licence/mint/main.go`, `mint.go`, `mint_test.go`. Flags for every v1 field,
  `-kind`, `-grace-days`, `-renewal-mode`, `-check-interval`; still signs only through the KMS signer and
  verifies its own output against K1.1. Tests: `TestMintedKeyVerifies`, `TestMintRefusesAKeyTheVerifierRefuses`,
  `TestTheToolHoldsNoWayToSignButTheSigner`. Command: `go test ./ee/licence/mint/... -tags ee -count=1`.
  Depends on: K1.1. Commit: `ee: the mint tool writes v1 keys`. **OWNER-RUN to use.** SECURITY.
- **K1.4 The console verifier.** Files: `console/src/main/licence.ts`, `licence-state.ts`,
  `licence-store.ts`, their `*.test.ts`. Reads K1.1's vectors; same refusals, same states, the upgrade
  message. Also reads the project key's state and the warning field from `fugaro licence status --json` (D2143: no key is Free). Tests: `licence.test.ts` (vector parity, no key is Free). Command: `cd console && npm run test:unit -- licence --maxWorkers=2`.
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
- **K2.3 Renewal and status. SECURITY.** Files: `ee/keyservice/renew.go` (new), `ee/keyservice/service.go`
  (the route table: `/renew`, `/status`), `renew_test.go` (new). The caller sends `key_id` and the renewal secret over TLS and the server compares the secret's hash with the one it holds
  (no HMAC: the hash is in the key, which every reader of the runs bucket holds, so it must not be a signing key); response
  `{status: ok|revoked, newer_key: bool}` and the renewal's new key; uniform answers; throttled; never
  returns a key to a revoked or unknown `key_id`; a replayed request is idempotent; a renewal returns the next key **and its new renewal secret** (reissue under a new `key_id` rotates it). Tests:
  `TestRenewalAnswersAreUniform`, `TestTheKeyAloneCannotRenew`, `TestRenewalRefusesAWrongSecret`, `TestRevokedKeyIsNeverRenewed`,
  `TestStatusCarriesNoKey`, `TestReplayedRenewalIsIdempotent`. Command: `go test ./ee/keyservice/ -tags ee -run 'TestRenewal|TestRevoked|TestStatus|TestReplayed' -count=1`.
  Depends on: K2.1, K2.2. Commit: `ee: the renewal and status endpoints`.
- **K2.4 Sign-in, authorize and device code. SECURITY.** Files: `ee/keyservice/signin.go` (new),
  `ee/keyservice/service.go`, `signin_test.go` (new). Verifies Firebase ID tokens of the owner's licence
  project by reusing `ee/coordinator/mcpauth/firebase.go` (audience, issuer and `email_verified` pinned; the purchaser binding depends on the last), serves `/cli/authorize`
  (port, `state`, PKCE challenge), the one-time `code` exchange, and the device-code start, poll and
  approve routes. Tests: `TestAuthorizeRefusesANonLoopbackRedirect`, `TestCodeIsSingleUse`,
  `TestPKCEVerifierIsRequired`, `TestDeviceCodeExpiresAndIsRateLimited`, `TestUnknownAndExpiredDeviceCodesAnswerAlike`,
  `TestTokenFromAnotherProjectIsRefused`. Depends on: K2.3 (one writer of `service.go` per level).
  Commit: `ee: the licence site's sign-in, authorize and device code`.
- **K2.5 Paddle events to v1. SECURITY.** Files: `ee/keyservice/merchant.go`, `merchant_test.go`. Maps a
  completed transaction (renewal included) to a v1 key (Team monthly and annual), a refund or
  chargeback to a revocation (D2131), a seat change to a reissue; Enterprise is not sold here (D2141).
  Tests: `TestRefundRevokesTheKey`, `TestSeatChangeReissuesUnderANewKeyID`, `TestEnterpriseIsNotACheckoutPlan`.
  Depends on: K2.1, K2.2. Commit: `ee: Paddle events mint, reissue and revoke v1 keys`.
- **K2.6 The admin page. SECURITY.** Files: `ee/keyservice/admin.go` (new), `service.go`, `admin_test.go` (new).
  Owner-only: reseller and comped keys (`kind: comped` with an expiry, excluded from revenue reporting),
  lookup by workspace or `key_id`, revoke, reissue, override policy; every action is recorded. Tests:
  `TestAdminRefusesAnyoneButTheOwner`, `TestCompedKeyHasAnExpiry`, `TestAdminActionsAreRecorded`.
  Depends on: K2.4 (`service.go`). Commit: `ee: the founder's admin page`.
- **K2.7 The 14-day Team trial. SECURITY.** Files: `ee/keyservice/trial.go` (new), `service.go`,
  `trial_test.go` (new). A signed-in account asks for a trial for an installation (`kind: trial`, 14 days,
  grace 0, `check_interval` `P1D`); one per installation and account. Tests: `TestSecondTrialIsRefused`,
  `TestTrialIsTeamTierFourteenDays`. Depends on: K2.6 (`service.go`), K2.2. Commit: `ee: the Team trial`.

### Slice K3: the project side (`ee/register`, `ee/licence`)

- **K3.1 The project-level store. SECURITY.** Files: `ee/licence/projectstore.go` (new), `projectstore_test.go`
  (new). Reads and writes the key object in the installation's runs bucket (through `internal/blobx`), the
  last-check record and the unreachable counter; at most two keys (overlap, T26). Tests:
  `TestProjectStoreKeepsTwoKeys`, `TestTheStoreNeverLogsAKey`, `TestAViewerReadsTheStateNotTheKey`.
  Command: `go test ./ee/licence/ -tags ee -run 'TestProjectStore|TestTheStore|TestAViewer' -count=1`.
  Depends on: K1.1. Commit: `ee: the licence key at project level (D2137)`.
- **K3.2 `fugaro licence set | status | refresh`. SECURITY.** Files: `ee/register/licence.go` (new),
  `ee/register/licence_test.go` (new), `internal/clidoc` entries only if the clidoc table requires one.
  `set` takes the key (line 1) and the renewal secret (line 2, absent for an offline key) from stdin or `--file`, refuses a positional key, writes the key to the project store and the secret to the installation's Secret Manager secret, refuses inside a coding agent's session as every cloud write does, and
  prints that the next `fugaro update` applies the renewal job when the key's `renewal_mode` or
  `check_interval` asks for one (F10); `status` prints tier, seats, expiry, state and last check and
  has `--json` with new field names; `refresh` calls K2.3 once. Hidden alias `license`. Tests:
  `TestLicenceSetRefusesAKeyInArgv`, `TestLicenceStatusJSONShape`, `TestUntaggedBuildHasNoLicenceCommand`,
  `TestLicenceSetNeverPrintsTheKey`, `TestLicenceWritesAreRefusedInAnAgentSession` (set, refresh, login, seats assign), `TestLicenceAliasLicenseWorks`. Command: `go test ./ee/register/ ./internal/eeboundary/ -tags ee -run 'TestLicence|TestUntagged' -count=1`.
  Depends on: K1.1, K3.1, K2.3. Commit: `ee: fugaro licence set, status and refresh`. `cli.RegisterCommand` is top-level only, so K3.2 owns `licence.go` and registers a subcommand table that K3.3, K3.4 and K4.1 fill from their own files.
- **K3.3 `fugaro licence login`. SECURITY.** Files: `ee/licence/login.go` (new), `ee/register/licence_login.go`
  (new), tests. The loopback listener (127.0.0.1, random port, one request, `state`, PKCE, 5-minute
  deadline) and the device-code fallback (`--device`, or no browser). Tests:
  `TestLoopbackBindsOnlyTheLoopback`, `TestCallbackWithTheWrongStateIsRefused`,
  `TestTheCodeIsNeverLogged`, `TestDevicePollStopsAtExpiry`. Depends on: K2.4, K3.2. Commit: `ee: fugaro licence login`.
- **K3.4 Seats. SECURITY. HELD until the owner says enforcement ships (TN1).** Files: `ee/register/seats.go`
  (new), `ee/register/lapse.go`, `internal/access/seats.go` (new: an `access.SetSeatLimit` seam taking the caller's identity, consulted while the key is active, set from `ee/register`, default limits nothing; the core still reads no key), `ee/register/seats_test.go` (new). Records seat holders on the project's
  operator and admin lists (D2139), `fugaro licence seats assign|rm|ls`, and the `access` seam answers an
  unseated operator as a viewer on Subscription surfaces; admins always keep a seat; a lapse changes
  nothing stored. Tests: `TestAnUnseatedOperatorActsAsAViewer`, `TestAdminsAlwaysKeepASeat`,
  `TestSeatsAreNotEnforcedInTheMITCLI`, `TestLapseNeverDeletes`. Depends on: K1.1, K3.1, K3.2 (the command
  family's file `licence.go` is read, not edited). Commit: `ee: seats are assigned and enforced on the paid surfaces`.
- **K3.5 Warnings.** Files: `ee/register/warn.go` (new), `warn_test.go` (new); the window rules of the key's
  `warnings` (roles and `grace_start[+Nd]`), a line on each tagged-CLI ee command, and the field the console
  and app read from `licence status --json`. Delivered by the console, app and ee commands only (F6).
  Tests: `TestAdminsOnlyInTheFirstWeekOfGrace`, `TestEveryoneInTheLast`, `TestNoWarningWhenActive`.
  Depends on: K3.2. Commit: `ee: licence warnings by role and window`.

### Slice K4: renewal and revocation job

- **K4.1 `fugaro licence renew --job`. SECURITY. The effect on the console's lapse is HELD with K3.4.**
  Files: `ee/register/licence_renew.go` (new), `licence_renew_test.go` (new). Run by the Scheduler job:
  reads the key and the renewal secret, asks `/status` at `check_interval`, asks `/renew` from
  `renew_before_days`, writes the next key (overlap kept), records last-ok and the failure count, never
  revokes on an error, and sets the 30-day "cannot verify revocation" warning. Tests:
  `TestAnUnreachableServerNeverRevokes`, `TestRenewalWritesTheNextKeyBesideTheOld`,
  `TestRevokedKeyIsRecordedNotDeleted`, `TestOfflineKeyNeverContactsTheServer`. Depends on: K1.1, K3.1, K2.3.
  Commit: `ee: the renewal and revocation job`.
- **K4.2 The Scheduler job and the update. SECURITY.** Files: `deploy/terraform/gcp/modules/installation/licence.tf`
  (new), `variables.tf`, `deployer.tf` (the job's permissions), `deploy/terraform/gcp/roots/*` only if a root
  names the switch, `internal/cli/update_licence.go` (new), tests, `docs/releases/v<next>.md`
  (`### For operators`: run `fugaro update`; it applies the licence job and its secret). The job reuses the key-gated image of the coordinator, the Secret Manager secret
  of the renewal secret, and a service account that reads and writes the key object and nothing else; it is **always applied** (the core
  never reads a key, D524) and its command no-ops with no key or an offline key. On a managed project it is
  applied without a prompt; elsewhere `update` asks as for any new resource. It also creates the empty Secret
  Manager secret for the renewal secret and grants the job's account `secretmanager.versions.add` on that one
  secret only. Tests:
  `TestUpdateAddsTheRenewalJobAndLeavesARunInFlightRunning`, `TestRunImagesCarryNoKey`,
  `TestRenewalServiceAccountReadsOnlyTheKeyObject` (terraform tag). Command: `go test -tags terraform ./internal/infra/... -run Licence -count=1`
  and `go test ./internal/cli/ -run 'TestUpdateAdds|TestRunImages' -count=1`. Depends on: K4.1.
  Commit: `ee: update applies the licence renewal job`.

### Slice K5: the licence site

- **K5.1 The account, authorize and device pages.** Files: `website/src/pages/licence/*.astro` (new),
  `website/test/licence.mjs` (new), the CSP `form-action` check. Firebase Auth sign-in, account page (seats,
  key download, a Paddle invoice link), the authorize and device pages. Content only: the server is K2.4.
  Tests: `website/test/licence.mjs`; command `cd website && npm run check`. Depends on: K2.4.
  Commit: `website: the licence account and sign-in pages`. Reviewer: standard; **SECURITY** if it adds a
  script or a form target.
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

## Parallel execution

Edges are the "Depends on" lines.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | K0.1, K0.2 (owner); K1.1, K1.5, K7.2 |
| 1 | K1.3, K1.4, K2.1, K2.2, K3.1 |
| 2 | K2.3, K2.5, K3.2 |
| 3 | K2.4, K3.5, K4.1 |
| 4 | K2.6, K3.3, K4.2, K5.1 |
| 5 | K2.7, K6.1 |
| held | K3.4 (the owner's enforcement go, TN1), after level 2 |

**Critical path:** K1.1 → K2.1 → K2.3 → K2.4 → K3.3 → K6.1 (six levels). K2.3 → K2.4 → K2.6 → K2.7 is the
sequence on `ee/keyservice/service.go`, which has one writer per level by construction. K4.1 → K4.2 is
sequential because the job's runtime is what the Terraform runs.

### Shared-files table

| File | Writers (level) |
| --- | --- |
| `ee/licence/key.go`, `store.go`, `vectors.json` | K1.1 (0) |
| `ee/licence/mint/*` | K1.3 (1) |
| `console/src/main/licence*.ts` | K1.4 (1) |
| `docs/licensing/licence-key.md` | K1.5 (0) |
| `ee/keyservice/minter.go`, `minterclient.go`, `policy.go` | K2.1 (1) |
| `ee/keyservice/store.go` | K2.2 (1) |
| `ee/keyservice/service.go` | K2.3 (2), K2.4 (3), K2.6 (4), K2.7 (5) |
| `ee/keyservice/merchant.go` | K2.5 (2) |
| `ee/licence/projectstore.go` | K3.1 (1) |
| `ee/register/licence.go` | K3.2 (2), the subcommand table; later tasks add their own files |
| `internal/access/seats.go` | K3.4 (held) |
| `ee/register/lapse.go`, `seats.go` | K3.4 (held) |
| `ee/register/warn.go` | K3.5 (3) |
| `ee/register/licence_renew.go` | K4.1 (3) |
| `ee/register/licence_login.go`, `ee/licence/login.go` | K3.3 (4) |
| `deploy/terraform/.../installation/licence.tf`, `variables.tf`, `deployer.tf` | K4.2 (4) |
| `website/src/pages/licence/*` | K5.1 (4) |
| `docs/licensing/implementation-plan.md`, `docs/subscriptions/implementation-plan.md`, `docs/design/key-signing-service.md` | K7.2 (0) |

No level holds two writers of one file. A change to any task's Files re-checks this table.

## Checks this plan takes (560–569)

All **OWNER-RUN**; the owner types the script's one command, no agent performs a step.

- **Check 560:** a real renewal through the deployed Scheduler job in the sandbox installation: a short-lived
  `renewal_mode: auto` key renews itself and `licence status` shows the new key (K4.2, K2.3).
- **Check 561:** revocation: the owner revokes a test key on the admin page and the next check records it
  (K2.6, K4.1); and an unreachable server does not revoke (block egress, wait one interval).
- **Check 562:** browser sign-in against the staging site, including the wrong-`state` refusal (K3.3, K2.4).
- **Check 563:** the device code from a machine with no browser (K3.3).
- **Check 564:** a Paddle sandbox purchase, renewal and refund end to end (K2.5; subscriptions S8.5 first).

## After deploy

Release-actions entries go in the PR of the task that needs them: K4.2's `### For operators` (the
`fugaro update` line and what it asks), and K0.2/S2.9's publication of the production public key.
