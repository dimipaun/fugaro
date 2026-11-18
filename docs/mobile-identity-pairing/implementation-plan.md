# Mobile identity, pairing and text interface: implementation plan

**Status: plan only. Nothing is implemented, and no task is approved.** Each task below needs the owner's own explicit go; an answer in [reconciliation.md](reconciliation.md) settles a question, it does not approve a task. Owner: Dimi Paun. Date: 2026-11-18. Checked against `origin/main` at `01fe41f30`. Decisions are `D#`, findings `F#`, modules `mip-*` ([capability map](reconciliation.md#capability-map-approved-2026-11-18)).

This builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). The shape the owner's answers gave it: the spec's "workspace" is the **project** (D2780); identity and the assertion signer live in a **dedicated identity project** (D2781); the project-side security service is **credsvc's code** (D2782, not the recommendation), served by a **second public Cloud Run service** of the same image with a separate signer account, because the existing one is IAM-only (amended after review, F31, O8 answered with a correction); sessions are a **Fugaro refresh token with an hourly custom token** (D2783); restore is silent with a **per-project strict switch** (D2784); the first pairing is **recorded on first use** under an accepted-provider policy (D2785); confirmations **amend D162 for costly modes only** (D2786); start and cancel go **through the mailbox and the P5.1 consumer** (D2787); the text view drives **the operator's own Claude Code session** (D2788); `mip-context` is **spike first** (D2789).

## Conventions every part follows

Rules files and `CLAUDE.md` are pointed at, not restated: `.claude/rules/credentials-service.md` (P3, P4, P5, K2, I1, D1), `.claude/rules/ee.md` (A1-A3, K1), `.claude/rules/terraform.md` (A3, K1, P5), `.claude/rules/mobile.md` (M1-M3, K3, J2-J4, X1-X3, Z1-Z3). The commands are the repository's: `go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); `go test <pkg> -run 'TestX|TestY' -count=1`; `cd mobile && npm test -- <path>` and `cd shared && npm test` (neither runs in CI until `docs/pending-workflows/shared-tests.yml` is copied in, a person's step); `go run ./internal/clidoc` after any `SECURITY.md` edit.

**Boundaries of this work**

- **Always.** A red test first; tests before every commit; `git status --short` and `git diff --stat` before a commit and `git add <paths>`; no secret-shaped literal in a test, doc or fixture (signing tests generate a throwaway key in the test and ship public keys and signature vectors only); every refusal after identity is verified writes exactly one audit line, naming a field and never a value; a wire shape adds fields and never changes one (`shared/mobile-session/vectors.json` and its Go twin change together); each task's pull request names its `T#` and `D#`.
- **Ask first.** A public invoker on any service (P5, O8, answered); a new dependency in `mobile/package.json` (providers, share target, local authentication: M0 proposes, the owner rules before M1 or X3 starts); any new Terraform resource type or kind of principal (`internal/infra/tf/cover.go` needs a row on purpose); a change to `config.ServiceServedAuths`; a rules-generation bump.
- **Never.** An agent runs a cloud action, a `terraform apply`, a key ceremony or `fugaro update`; an agent holds, mints with or operates the production signing key (`.claude/rules/ee.md`); a command asks a question or waits for input (no-prompt policy: a phone read-back is the app's, not a CLI prompt); the identity project stores a workspace session token (spec); edit `.github/workflows/`; edit `docs/status.md` from a task's own pull request; touch `spec.md`.

**Test filters.** `npm test -- <filter>` appends to a fixed glob, so a filter that matches nothing is silently ignored and the whole suite runs; a task's Command names its test file where the runner honours a path, and the targeted claim is only as good as the runner's.

**Provider-side order.** `fugaro plan graph` records contract edges only from consumers, so the provider-side tasks that write to C1's shapes (A1, A2, K1, P2) carry no edge to C1 in the graph; launch them after C1 merges.

**Defaults the plan uses for the spec's vague figures (O4 asks the owner to confirm):** QR nonce 10 minutes; "recent enough" authentication 24 hours, per project; assertion lifetime 2 minutes; signing key rotation 90 days with 30 days of overlap; access token about one hour; refresh token about 30 days idle; long paste 100000 characters without loss.

## Global constraints and review focus

Inputs and failure modes the spec implies; each is pinned by a named task.

| Case | Pinned by |
| --- | --- |
| Assertion replayed (same `jti`), wrong `aud`, expired, bad signature, unknown `kid`, `alg` swapped to `none` or HMAC | P1 |
| JWKS fetch fails: the cache serves until its bound; rotation overlap accepts the previous key; a key past overlap is refused | P1, A2 |
| QR nonce expired, reused, or presented by an identity whose email does not match the invite | P2, P3 |
| Email reassigned: same email, different provider `sub` | P3 |
| Unverified email, Apple private-relay address, provider not accepted (error lists the accepted ones), authentication too old | P3, M1 |
| Refresh token replayed: the whole family is revoked; refresh after the member is removed fails; idle 30 days expires | P4 |
| Refresh token used from another device key | P4, M3 |
| Admin revokes one device; sign out everywhere; a stolen phone cannot cancel or start | P4, P7, M3 |
| Restore with strict switch on needs a fresh QR; every restore leaves one audit line | K2 |
| Paired list readable only by a signed-in owner through the function; a dump of the store reveals no endpoint | K1 |
| Sync off: the list is not sent, and the platform keychain backup still restores | K3 |
| Identity project outage: existing sessions keep refreshing; only new pairings and restores fail | I1 |
| Viewer cannot cancel or start; operator and admin can | J3, J4 |
| A costly start asks a read-back; a plain start does not; the kill switch and budget writes stay refused by voice | J3 |
| Long paste of 100000 characters survives a voice-to-text switch and a rotation | X2 |
| Dense output goes to the screen and voice says so | X2 |
| A secret never appears in an error, a log line or a screen | P3, P4, M1, M3 |
| Every existing credsvc caller class is refused on the mobile routes, and a mobile caller is refused on every existing route | P3, P5, I1 |
| A revoked refresh family or a failed membership re-check leaves no device node, so no Firebase refresh token keeps database access | P4, P7, K2 |
| `jti`, nonce and challenge are single-use across several credsvc instances | P1, P3, K2 |
| The QR's `aud` derives from its endpoint (a relayed QR is refused) | P2, P3 |
| Revoking a device does not erase the member's recorded `sub`; the next pairing must match it | P3 |
| Removing a member deletes the member record, every device node of that email and every refresh family; restore after that is refused and role comes from the member record | P3, P4, P7, K2 |
| The public service mounts only the mobile routes from a fail-closed allowlist and the private service mounts none of them | P5, I1 |
| Restore applies the same policy as pairing (membership, provider, age, `mobile.enabled`, recorded `sub`) | K2 |
| A costly start asks the phone's read-back; a modified client launching a costly mode without one is refused by the mailbox gate | J3, J5a |
| A device node with a public key accepts signatures only and refuses a MAC; a node without one accepts a MAC only; a message signed by another device's key is refused | J5b |
| A newly paired phone verifies replies and heartbeats with the pinned consumer key; re-pairing deletes the old MAC key from the keychain, the Secret Manager copy and the local config | J5c |
| A policy published before the `mobile` key existed reads `mobile.enabled` as off (`mobile_disabled`), never as the default on | P3, K2, P6 |
| Running jobs: no `outcomes` entry means running within a bounded window; a run past the repository's total timeout or from an earlier day shows as stale, not running; an unreachable project shows as such | J1, J2 |

## Slices and tasks

**Slice 1, ground and contracts.** DA, T0, C1, C2, owner-run M0 and Z0. Inert: documents, a pin test, two contracts with stubs.
**Slice 2, the signer.** A1-A3, K1. Agent-written code and Terraform in `ee/`; the owner deploys (Check 730).
**Slice 3, the project side.** P0-P7, K2, UP1.
**Slice 4, the app and the mailbox.** M1-M3, K3, J1-J4, J5a-J5c, X1-X3, Z1-Z3.
**Slice 5, joins.** I1, I2, D1.

Every task touches about five files or fewer. The Reviewer tier is the mode the task's own review runs at: `standard`, or `opus` with a mandatory independent review (`fugaro review --pr N`) for credentials, authentication, IAM, a write guard or a wire format. A contract over authentication is reviewed at the strictest rigor of its consumers before either side is written against it.

### DA: amendment notes in the mobile-auth and mobile folders
- Acceptance: `docs/mobile-auth/README.md` and `docs/mobile/README.md` each carry a dated amendment saying D114 stands, D232 is amended (one secure-store item per signed-in identity), D162 is amended for costly modes, D171 is amended (a per-message device signature replaces the MAC for newly paired phones) and D226 and D229 are amended so an admin may name another member's email, citing D2786, D2790, D2799 and O6; the existing pins still pass.
- Files: `docs/mobile-auth/README.md`, `docs/mobile/README.md`, `internal/cli/docs_mobile_auth_test.go` (only if a pin needs the new sentence).
- Interfaces: none.
- Tests: `TestDocsMobileAuth*`, `TestDocsMobile*`.
- Command: `go test ./internal/cli/ -run 'Docs.*Mobile' -count=1`
- Red step: add the pin for the amendment sentence; it fails until the README carries it.
- Depends on: none; 
- Commit: `docs(mobile): amendment notes for D114, D162, D171, D226, D229 and D232 (D2786, D2790, D2799, O6)`
- Reviewer tier: standard.

### T0: the pin test and the live checks
- Acceptance: a docs test pins this folder (documents exist, `spec.md` verbatim, decisions numbered D2780-D2799, tasks in order, every cited path exists, no secret shape); `docs/gcp-live-checklist.md` carries Checks 730-736 as one command each for the owner.
- Files: `internal/cli/docs_mobile_identity_pairing_test.go` (new), `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: `TestDocsMobileIdentityPairing*`.
- Command: `go test ./internal/cli/ -run 'DocsMobileIdentityPairing' -count=1`
- Red step: the pin fails before the checklist lines exist.
- Depends on: none.
- Commit: `docs(mobile-identity-pairing): pin the folder and add Checks 730-736`
- Reviewer tier: standard.

### C1: the assertion and pairing wire contract
- Acceptance: a Go package states the assertion claims (`iss`, `aud`, `sub`, `email`, `email_verified`, `provider`, `auth_time`, `jti`, `exp`, challenge), the request and response of pair, refresh, restore challenge, restore and revoke-self, the error codes (`provider_not_accepted` with the list, `auth_too_old`, `not_a_member`, `nonce_expired`, `strict_new_device_needs_qr`, `device_revoked`, `mobile_disabled`), the secure-store item names, the invite and member-record shapes and a `MemberStore` interface, the paired-list entry shape (opaque project id, endpoint, identity used), the interfaces `Verifier`, `Policy` (membership, accepted provider, authentication age, `mobile.enabled`, recorded `sub`), `InviteStore`, `RefreshStore`, `Minter`; a device public key in the pair request for proof of possession and message signatures; the consumer's public key in the pair response (O12); the policy values a session response carries to the phone (costly modes, biometric switch, compaction threshold); a challenge-and-`jti` compare-and-set store interface; a stub that returns fixed shapes and does nothing else; one `vectors.json` both Go and JS read; `contract.md` says what each side may rely on.
- Files: `internal/mobilesession/contract.go` (new), `internal/mobilesession/stub.go` (new), `shared/mobile-session/vectors.json` (new), `docs/mobile-identity-pairing/contract.md` (new).
- Interfaces: produces everything the consumers P1, P3, P4, P7, K2, M1, M2, M3, K3 build against; the provider-side tasks A1, A2, K1 and P2 write to the shapes it fixes. Consumes D2783, D2785, D2791, D2792. These files belong to the contract and its changes alone.
- Tests: a vector round-trip test in Go and a loader test in JS.
- Command: `go test ./internal/mobilesession/ -count=1 && (cd shared && npm test -- mobile-session)`
- Red step: the vector test fails with no vectors file.
- Depends on: none.
- Commit: `mobilesession: the assertion and pairing contract with its stub (D2792, D2783)`
- Reviewer tier: **opus**, mandatory independent review, strictest rigor, before either side starts.

### C2: the app session port
- Acceptance: a JS port states what an app surface may rely on from a paired project's session (project id, endpoint, role, the policy values C1's session response carries, an `idToken()` accessor that refreshes, a `signMessage(bytes)` that signs with the device key (O6), a `signedOut` event) and a stub that returns a fixed fake session; `session-port.md` says so.
- Files: `shared/mobile-session/port.js` (new), `shared/mobile-session/stub.js` (new), `shared/mobile-session/port.test.js` (new), `docs/mobile-identity-pairing/session-port.md` (new).
- Interfaces: produces the port J2, J3, J4, X1 build against and M3 implements.
- Tests: the port's shape test; the stub honours it.
- Command: `cd shared && npm test -- mobile-session/port`
- Red step: the shape test fails with no port.
- Depends on: none.
- Commit: `shared: the mobile session port and its stub`
- Reviewer tier: standard.

### P0: the credsvc audit purposes for mobile
- Acceptance: `internal/credsvc/audit.go`'s closed purpose set carries the mobile purposes (pair, refresh, restore, revoke) with the run/job pairing rules they need, so P3, P4 and K2 add routes without touching it; the one-line-per-refusal rule holds for them.
- Files: `internal/credsvc/audit.go`, `internal/credsvc/audit_mobile_test.go` (new).
- Interfaces: the purposes named in C1's error list.
- Tests: `TestAuditMobilePurposes*`.
- Command: `go test ./internal/credsvc/ -run 'TestAuditMobilePurposes|TestAudit' -count=1`
- Red step: an audit line with a mobile purpose is refused until the set carries it.
- Depends on: none.
- Commit: `credsvc: audit purposes for the mobile routes`
- Reviewer tier: **opus** (credentials-service rules).

### M0: app spike, owner-run (Check 733)
- Acceptance: `app-spike.md` records, from a real iPhone and Android phone, whether Google, Microsoft and Apple sign-in yield tokens usable with REST `signInWithIdp` and with two identities at once (U4), whether a non-exportable signing key exists (U5), the share-extension and intent-filter route and its package (U7), and whether the synced platform keychain works with the secure-store option the app uses (U10); it names the packages the owner approves.
- Files: `docs/mobile-identity-pairing/app-spike.md` (new).
- Interfaces: produces the dependency choice M1, M2 and X3 wait on.
- Tests: none; the result is the document.
- Command: none for an agent.
- Red step: none.
- Depends on: none.
- Commit: `docs(mobile-identity-pairing): the app spike result`
- Reviewer tier: standard.
- Owner-run: Check 733, on two real phones; the check lists the commands.

### Z0: context spike, owner-run (Check 732)
- Acceptance: `context-spike.md` records, with evidence, whether the operator's Claude Code session exposes its context fullness, accepts `/compact` when idle, has a configurable auto-compact threshold, and whether five-hour and weekly limits are readable per token within Anthropic's terms (U1-U3).
- Files: `docs/mobile-identity-pairing/context-spike.md` (new).
- Interfaces: produces the capability list Z1-Z3 wait on.
- Tests: none.
- Command: none for an agent.
- Red step: none.
- Depends on: none.
- Commit: `docs(mobile-identity-pairing): the context spike result`
- Reviewer tier: standard.
- Owner-run: Check 732.

### A1: the assertion signer
- Acceptance: a function verifies a Firebase ID token from the identity project (signature, `aud`, `iss`, `exp`, `email_verified` for password sign-in), then signs an EdDSA assertion with the Cloud KMS key for exactly one `aud` project and challenge, with a 2-minute lifetime; it refuses an unverified password email, an `aud` the caller did not name, and a missing challenge; it logs no token.
- Files: `ee/identitysvc/sign.go`, `ee/identitysvc/idtoken.go`, `ee/identitysvc/handler.go`, `ee/identitysvc/sign_test.go`, `ee/identitysvc/cmd/identitysvc/main.go` (all new; SPDX notice per `ee.md`; the `main` and image build are part of the task).
- Interfaces: consumes C1's claim names via the vectors; reuses the pattern of `ee/keyservice/kmssigner.go` and `ee/coordinator/oauth/workspace_assertion.go`.
- Tests: `TestSign*` with a fake KMS signer and a generated test key.
- Command: `go test ./ee/identitysvc/ -run 'TestSign' -count=1`
- Red step: a test asking for an assertion with a mismatched `aud` must fail until the refusal exists.
- Depends on: none (the claim names are fixed by D2792).
- Commit: `ee/identitysvc: sign project-bound assertions with the KMS key (D2792)`
- Reviewer tier: **opus**, independent review.

### A2: JWKS publication and key rotation
- Acceptance: a handler publishes the public keys as a JWKS (OKP Ed25519) with `kid`, the current key and the previous key during the overlap; rotation logic computes which keys are published at a time; the published set never contains private material.
- Files: `ee/identitysvc/jwks.go`, `ee/identitysvc/rotation.go`, `ee/identitysvc/jwks_test.go` (all new).
- Interfaces: produces the JWKS URL P1 caches.
- Tests: `TestJWKS*`, `TestRotation*` (90-day period, 30-day overlap).
- Command: `go test ./ee/identitysvc/ -run 'TestJWKS|TestRotation' -count=1`
- Red step: the overlap test fails with only the current key published.
- Depends on: none.
- Commit: `ee/identitysvc: JWKS publication and rotation overlap (D2792)`
- Reviewer tier: **opus**.

### A3: the identity project Terraform
- Acceptance: a Terraform root defines the project's KMS key ring and Ed25519 signing key (`ASYMMETRIC_SIGN`, non-exportable), the signer service on Cloud Run with the narrowest custom role, the Firebase Auth provider configuration (Google, Microsoft, Apple, email and password with verification), and the JWKS route; `infra_test.go` pins purpose, algorithm and protection level, as the key service's does.
- Files: `ee/identitysvc/terraform/main.tf`, `ee/identitysvc/terraform/keyring.tf`, `ee/identitysvc/terraform/auth.tf` (new), `ee/identitysvc/infra_test.go` (new).
- Interfaces: consumes the key ring pattern in `ee/keyservice/terraform/keyring/main.tf`.
- Tests: `terraform fmt -check`, `init`, `validate`, `terraform test`; the pin test.
- Command: `terraform fmt -check -recursive ee/identitysvc/terraform && go test ./ee/identitysvc/ -run 'TestInfra' -count=1`
- Red step: the pin test fails until the key purpose and algorithm are set.
- Depends on: none.
- Commit: `ee/identitysvc: Terraform for the identity project (D2781)`
- Reviewer tier: **opus**, independent review before apply; Trivy stays clean.
- Owner-run: Check 730, the apply and the key ceremony, with a release-actions entry; no agent applies.

### P1: the assertion verifier
- Acceptance: `internal/assertion` verifies an assertion: EdDSA only, `iss` equal to the pinned issuer, the JWKS fetched only from the pinned URL (F38, O7), `kid` from a cached JWKS (with the overlap rule and a bounded stale-serving cache), `aud` equal to this project, challenge equal to the expected one, `jti` unused (single use through C1's compare-and-set store, not process memory, remembered for the assertion's lifetime plus skew), `exp`/`iat` with bounded skew; every refusal is a typed error naming the field.
- Files: `internal/assertion/verify.go`, `internal/assertion/jwks_cache.go`, `internal/assertion/jti.go`, `internal/assertion/verify_test.go` (all new).
- Interfaces: implements C1's `Verifier`; no `ee/` import.
- Tests: `TestVerify*` for every row of the global table that names P1.
- Command: `go test ./internal/assertion/ -count=1`
- Red step: a replayed assertion must fail first.
- Depends on: C1 (the contract replaces the edge to A1).
- Commit: `assertion: verify identity-project assertions (D2792)`
- Reviewer tier: **opus**.

### P2: invites through the existing `fugaro pair --email --role`
- Acceptance: `fugaro pair` keeps its flags and gains the invitation: an admin may name another member's email (amending D226, D2799), D224's role resolution and both its refusals (`errPairResolvedAbove`, `errPairRoleDisagrees`) stay, and the role is refused rather than defaulted for another person, an invite (nonce, 10-minute expiry, email, role, accepted providers) is written to a node only IAM can write and creates the member record (email, the resolved role, no `sub` yet), and the QR carries the endpoint, email and nonce and no credential, with `aud` derived from the endpoint; `pairQRVersion` is bumped and `schemas/pair-event.schema.json` carries the new fields; the command waits for nothing and asks nothing.
- Files: `internal/cli/pair.go`, `internal/cli/pair_events.go` (`pairQRVersion`), `internal/cli/pair_invite.go` (new, holds the invite type), `internal/cli/pair_invite_test.go` (new), `schemas/pair-event.schema.json`. If a plugin skill names these flags, `plugin/skills/**` changes in the same task.
- Interfaces: writes the invite P3 reads (C1's `InviteStore`).
- Tests: `TestPairInvite*`, the schema test, the plugin flag test if a skill names the flags.
- Command: `go test ./internal/cli/ -run 'TestPairInvite' -count=1 && go test ./schemas/ ./plugin/... -count=1`
- Red step: naming another person's email with no `--role` must refuse first.
- Depends on: none in the graph (see "Provider-side order" below); O11 is answered.
- Commit: `pair: invite a named person; the QR carries no credential (F4, D2799)`
- Reviewer tier: **opus**.

### P3: the credsvc pairing endpoint
- Acceptance: credsvc verifies the assertion through `Verifier`, consumes the invite nonce once, applies the policy (member, accepted providers, authentication age, `mobile_enabled`), records `provider` and `sub` on first use in a **member record that outlives the device** (D2791 amended) and refuses a later mismatch, derives and checks `aud` from the endpoint, stores the device's public key on the device node for message signatures (O6), serves from the second public service with a cross-class test (F31), writes the device node, and returns the project config, a refresh token and a first custom token; exactly one audit line per refusal, none containing a token or key; `config.ServiceHeldSecretNames` stays empty.
- Files: `internal/credsvc/mobile_pair.go`, `internal/credsvc/mobile_policy.go` (implements C1's `Policy`, reading the member record for membership and role), `internal/credsvc/mobile_member.go`, `internal/credsvc/mobile_pair_test.go` (all new).
- Interfaces: implements C1's pair handler; consumes `Verifier`, `InviteStore`.
- Tests: `TestMobilePair*` for each row naming P3.
- Command: `go test ./internal/credsvc/ -run 'TestMobilePair' -count=1`
- Red step: an assertion for a reassigned email (different `sub`) must be refused first.
- Depends on: C1, P0 (`audit.go`), P6 (reads `mobile.*` for `Policy`).
- Commit: `credsvc: the mobile pairing endpoint (D2782, D2785)`
- Reviewer tier: **opus**, independent review (credentials-service rules).

### P4: the credsvc session
- Acceptance: a refresh endpoint rotates a device-key-bound refresh token (hashed at rest, replay revokes the family, 30-day idle expiry), re-checks membership and role, and mints a one-hour custom token through the separate device-only signer account (O8); revoke-self revokes the caller's family; a replayed token, a revoked family or a failed membership re-check **deletes the device node** so no Firebase refresh token keeps database access (F32); refresh needs proof of possession by the device key (C1's public key); the Firebase refresh token is never returned by this service.
- Files: `internal/credsvc/mobile_session.go`, `internal/credsvc/mobile_refresh_store.go`, `internal/credsvc/mobile_mint.go`, `internal/credsvc/mobile_session_test.go` (all new).
- Interfaces: implements C1's `RefreshStore`, `Minter`; consumes the device node shape (D2791).
- Tests: `TestMobileSession*`.
- Command: `go test ./internal/credsvc/ -run 'TestMobileSession' -count=1`
- Red step: a replayed refresh token must revoke the family first.
- Depends on: C1, P0; it serves on the public service (O8, answered).
- Commit: `credsvc: rotating device-bound refresh and hourly custom tokens (D2783)`
- Reviewer tier: **opus**, independent review.

### P5: the mobile service, its signer account and its grants (O1 and O8 answered)
- Acceptance: a second Cloud Run service of the credsvc image with a public invoker and its own runtime service account, started in `mobile` mode (I1 owns what that mode mounts); a **separate device-only signer account created in the Firebase project**, beside the existing `google_service_account.signer`, because Firebase accepts custom tokens only from an account of the Firebase project and an installation can span two projects; the runtime account holds `signJwt` on that signer account only; the one project-level binding added is the unchanged custom role `fugaroCredentialsDatabase` to the runtime account (whole instance, rules bypassed; O1); the existing service stays private and its invoker untouched; the task states that IAM cannot limit the claims the signer signs (`orchestrator.tf` calls `signJwt` the widest grant in its table) and that the route allowlist and tests are the control; a cover row with a sentence saying why; golden plans regenerate.
- Files: `deploy/terraform/gcp/modules/installation/credentials.tf`, `deploy/terraform/gcp/modules/installation/mobile_service.tf` (new), `deploy/terraform/gcp/modules/firebase/iam.tf`, `internal/infra/tf/cover.go`, `internal/infra/credentials_mobile_test.go` (new), golden plans under `internal/infra/` from `scripts/gen-golden-plans.sh`.
- Interfaces: none.
- Tests: `TestCredentialsMobile*`, `terraform fmt`, `init`, `validate`, `terraform test`.
- Command: `terraform fmt -check -recursive deploy/terraform && go test -tags terraform ./internal/infra/... -run 'TestCredentialsMobile' -count=1`
- Red step: the test asserts the private service's invoker has no public member, the signer is in the Firebase project, and the only project-level binding added is `fugaroCredentialsDatabase` to the new runtime account; it fails with any other.
- Depends on: none; its grants have the owner's go in the plan (O1, 2026-11-18), and the task still needs its own go and an independent review before any apply.
- Commit: `terraform: a public mobile service beside credsvc with its own signer account (D2782, F31)`
- Reviewer tier: **opus**, independent review before any apply.

### P6: project policy fields
- Acceptance: the published project policy carries `mobile.enabled` (default on, answering only after `update` publishes the policy; O9 answered), `mobile.sync` (default on), `mobile.accepted_providers`, `mobile.max_auth_age` (24 hours), `mobile.strict_new_device` (off), `mobile.costly_modes` (`pro`, `critical`), `mobile.biometric_on_costly` (off); the fields are a tolerated top-level `mobile` key with **no `Version` bump**, because `version.go` says only `Version` can make a node refused and a bump would make runners on the previous image refuse it while `update` must be safe with runs in flight (`docs/design/update-in-flight.md`); a policy with no `mobile` key reads `mobile.enabled` as off, and the default on is what `update` writes when it publishes (O9); `fugaro doctor` prints in one line what is stored in the identity project when sync is on.
- Files: `internal/projectpolicy/mobile.go` (new), `internal/projectpolicy/mobile_test.go` (new), `internal/cli/doctor_mobile.go` (new); `schemas/project-layer.schema.json` only if the fields belong to the layer it pins.
- Interfaces: fields P3, K2, J3 and Z2 read.
- Tests: `TestProjectPolicyMobile*`, a previous-version-converges test (the published policy tolerates unknown top-level keys, so the test reads the typed fields, not rejection).
- Command: `go test ./internal/projectpolicy/ -run 'Mobile' -count=1`
- Red step: reading `mobile.strict_new_device` from a published policy fails until the typed struct exists; a policy with no `mobile` key must read as disabled.
- Depends on: none.
- Commit: `projectpolicy: the mobile switches and the accepted-provider list (D2784, D2785, D2796)`
- Reviewer tier: **opus**.

### P7: admin revoke reaches the refresh family
- Acceptance: `fugaro pair revoke` also revokes the device's refresh family and keeps the member record; removing a member (an explicit flag) deletes the record, every device node of that email and every refresh family, so a stolen phone loses its session at its next refresh and cannot refresh again.
- Files: `internal/cli/pair.go`, `internal/cli/pair_revoke.go` (new), `internal/cli/pair_revoke_test.go` (new).
- Interfaces: consumes C1's revoke shape through `RefreshStore`.
- Tests: `TestPairRevokeFamily*`.
- Command: `go test ./internal/cli/ -run 'TestPairRevoke' -count=1`
- Red step: after `pair revoke`, a refresh with the revoked family's token must be refused; the test fails first because revoke does not yet touch the family.
- Depends on: P2 (the same file, `internal/cli/pair.go`), C1 (replaces the edge to P4).
- Commit: `pair revoke: revoke the refresh family too (D2794)`
- Reviewer tier: **opus**.

### K1: the encrypted paired-list store
- Acceptance: a function stores, per signed-in user, the list of opaque project ids and endpoints, encrypted with a Cloud KMS key in RTDB; only the function decrypts and only for its owner, with the uid bound as KMS additional data so one user's ciphertext cannot be copied under another's uid; each entry records which identity was used; client rules deny all; a dump of the store shows no endpoint.
- Files: `ee/identitysvc/pairedlist.go`, `ee/identitysvc/pairedlist_test.go`, `ee/identitysvc/terraform/pairedlist.tf`, `ee/identitysvc/rtdb.rules.json` with its `.license` sibling (`ee.md`) (all new).
- Interfaces: C1's list entry shape.
- Tests: `TestPairedList*`, including another user's token and a raw dump.
- Command: `go test ./ee/identitysvc/ -run 'TestPairedList' -count=1`
- Red step: reading another user's list must fail first.
- Depends on: none.
- Commit: `ee/identitysvc: the encrypted paired-list store`
- Reviewer tier: **opus**.

### K2: credsvc restore
- Acceptance: a challenge endpoint issues a single-use challenge; restore verifies an assertion bound to it, applies C1's `Policy` exactly as pairing does (membership, accepted provider, age, `mobile.enabled`, recorded `sub`), refuses with `strict_new_device_needs_qr` when the project's strict switch is on, otherwise returns a refresh token and custom token for the new device; one audit line per restore.
- Files: `internal/credsvc/mobile_restore.go`, `internal/credsvc/mobile_restore_test.go` (both new; its audit purposes come from P0).
- Interfaces: implements C1's restore shapes.
- Tests: `TestMobileRestore*`.
- Command: `go test ./internal/credsvc/ -run 'TestMobileRestore' -count=1`
- Red step: strict on must refuse before the allow path exists.
- Depends on: C1, P0, P6 (`mobile.*` for `Policy`).
- Commit: `credsvc: new-device restore with a strict switch (D2784)`
- Reviewer tier: **opus**.

### M1: app identities
- Acceptance: the app holds several signed-in identities, each with email, provider, `sub` and last authentication time; add, remove and re-authenticate work independently; sign-in is REST with the provider's token; an Apple private-relay address is flagged unusable for pairing; one secure-store item per identity and no log or error carries a credential.
- Files: `mobile/src/identity/store.js`, `mobile/src/identity/providers.js`, `mobile/src/identity/index.js` (all new), `mobile/src/accounts/secure-store.js`, `mobile/package.json` (the packages M0 names, after the owner's go).
- Interfaces: C1's assertion request shape and secure-store item names.
- Tests: `identity/*.test.js`; the scan tests in `src/boundary.test.js` stay green.
- Command: `cd mobile && npm test -- src/identity src/boundary`
- Red step: a relay address must be refused for pairing first.
- Depends on: C1 (replaces the edge to A1), M0 (which packages).
- Commit: `mobile: several identities with relay detection (D2790, D2793)`
- Reviewer tier: **opus**.

### M2: the app pairing flow
- Acceptance: the app parses the new QR (endpoint, email, nonce) as hostile input, picks the identity whose email matches and discloses only it, asks the identity project for an assertion bound to the nonce, sends it to the project, stores the result, and shows each error code as a clear screen (accepted providers listed, "sign out and back in", "not a member", "nonce expired", "add a Google, Microsoft or verified email identity" for relay).
- Files: `mobile/src/pairing/payload.js`, `mobile/src/pairing/session.js`, `mobile/src/pairing/index.js`, `mobile/src/pairing/errors.js` (new).
- Interfaces: C1's pair request, response and error codes.
- Tests: `pairing/*.test.js`.
- Command: `cd mobile && npm test -- src/pairing`
- Red step: a QR with a credential field must be refused first.
- Depends on: C1 (replaces the edge to P3), M0.
- Commit: `mobile: pair by identity assertion, not by a bearer QR (F4)`
- Reviewer tier: **opus**.

### M3: the app session
- Acceptance: the app keeps a device key (hardware-backed if M0 found one, otherwise software and the plan's docs say so), refreshes each project's session every hour through the project, rotates the refresh token, never stores the Firebase refresh token, implements C2's port, and signs out everywhere (identity project plus each live project's revoke-self).
- Files: `mobile/src/session/refresh.js`, `mobile/src/session/devicekey.js`, `mobile/src/session/index.js` (new), `mobile/src/accounts/secret.js`.
- Interfaces: C1's refresh shapes; implements C2.
- Tests: `session/*.test.js`.
- Command: `cd mobile && npm test -- src/session src/boundary`
- Red step: a refresh with a rotated-out token must fail and sign the project out first.
- Depends on: M1 (the shared secure-store files), C1 (replaces the edge to P4).
- Commit: `mobile: the device-bound session and the hourly refresh (D2783, D2794)`
- Reviewer tier: **opus**.

### K3: app continuity
- Acceptance: on pairing the app writes the project's opaque id and endpoint to the identity project when the project's sync switch is on; on a new device it requests a challenge from each project, an assertion bound to it, and restores silently; with sync off it restores from the platform's synced keychain; readable names stay on the device.
- Files: `mobile/src/continuity/sync.js`, `mobile/src/continuity/restore.js`, `mobile/src/continuity/index.js` (new), `mobile/src/accounts/expo-secure-store.js`.
- Interfaces: C1's restore shapes; the C2 port.
- Tests: `continuity/*.test.js`.
- Command: `cd mobile && npm test -- src/continuity src/boundary`
- Red step: with sync off nothing may be sent; fails first.
- Depends on: M3 (the shared secure-store file and the real refresh), C1 (replaces the edge to K2).
- Commit: `mobile: the paired list, restore and keychain backup (D2784, F15)`
- Reviewer tier: **opus**.

### J1: a device can read `outcomes`
- Acceptance: a paired device's token reads `outcomes` (and keeps its existing read of `spend/$day/runs/$slug/$run`), nothing wider, so the phone derives a running run as a run entry with no outcome; the rules generation bumps and `update` migrates it in place; the emulator test pins that a device still cannot read `runs/$slug/$run` or write either node.
- Files: `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/emulator_runs_test.go` (new), `internal/projectpolicy/version.go`.
- Interfaces: none.
- Tests: `TestEmulatorRuns*` (the emulator tag as the neighbours use), `rules_test.go`.
- Command: `go test -tags firebase ./internal/budget/rules/ -run 'TestEmulatorRuns' -count=1`
- Red step: a device reading `outcomes` fails first.
- Depends on: none (`RulesVersion` in `version.go` is J1's alone). The read shows every run's `requestedBy` to viewers; the owner accepted that for all roles (O3).
- Commit: `rules: paired devices read outcomes (F30, D2797)`
- Reviewer tier: **opus**.

### J2: the running-jobs view
- Acceptance: operators, admins and viewers see running jobs across paired projects (a run with no `outcomes` entry), each with its project, age and spend; a run with no outcome that is older than the repository's total timeout, or from an earlier day, shows as stale and not running; an unreachable project shows as such; `scope.test.js` is amended to allow run rows and still forbids repository rows.
- Files: `mobile/src/jobs/list.js`, `mobile/src/jobs/running.js` (new), `mobile/src/jobs/scope.test.js`, `mobile/src/jobs/index.js`.
- Interfaces: the C2 port.
- Tests: `jobs/*.test.js`.
- Command: `cd mobile && npm test -- src/jobs`
- Red step: a project with an outcome must not list first.
- Depends on: C2 (replaces the edge to M3). The owner accepted the `outcomes` read for all roles (O3).
- Commit: `mobile: the running-jobs view (D2797)`
- Reviewer tier: standard.

### J3: confirmation policy
- Acceptance: cancel needs a spoken read-back; a start in a mode named by the session's `costly_modes` needs a read-back and, with the biometric switch, biometric re-auth through P5.1g's path; a plain start needs none; the kill switch and budget writes stay refused. The confirm machine and its Go mirror change in parity (`internal/mailbox/action.go`, `shared/confirm/vectors.json`, `TestConfirmParityWithShared`). **The gate's refusal is J5a's:** a launch is not a mailbox action today, so refusing a modified client comes with J5a's typed launch action (O10); until J5a merges the read-back is the phone's and the caps bound the cost.
- Files: `shared/confirm/policy.js` (new), `shared/confirm/index.js`, `shared/confirm/vectors.json`, `internal/mailbox/action.go`, `shared/confirm/policy.test.js` (new; it also covers the new export).
- Interfaces: the C2 port's session policy values.
- Tests: `confirm/policy.test.js`, `TestConfirmParityWithShared`.
- Command: `(cd shared && npm test -- confirm) && go test ./internal/cli/ -run 'TestConfirmParityWithShared' -count=1 && go test ./internal/mailbox/ -count=1`
- Red step: a costly launch with no read-back fails first, in the JS machine and in its Go mirror.
- Depends on: C2, P6.
- Commit: `confirm: read-backs for cancel and costly starts (D2786)`
- Reviewer tier: **opus** (it changes a confirmation ruling).

### J4: start and cancel from the phone
- Acceptance: the phone sends the existing `cancel` mailbox action and a start as a signed message to the orchestrator, as D162 and `shared/mailbox-schema/README.md` have it (a launch is a message, not an action, until J5a adds the typed launch action and J5b switches the phone to it); the phone hides what the session's role cannot do; the app's one write path stays the mailbox client. The consumer's refusal of a viewer by `internal/access` (`ActionLaunch`, `ActionCancel`) is the P5.1 consumer's, pinned there (see "What this plan does not own"); J4 tests only the phone side.
- Files: `shared/command/verbs.js` (new), `shared/command/index.js` (the module's sole entry point; it holds the closed command set), `shared/command/index.test.js`, `mobile/src/mailbox/client.js`.
- Interfaces: the C2 port; `internal/mailbox/message.go` `Actions` as it is.
- Tests: `write-path.test.js`, `command/index.test.js`.
- Command: `cd mobile && npm test -- src/mailbox && cd ../shared && npm test -- command`
- Red step: `write-path.test.js` must fail for any second write path.
- Depends on: C2.
- Commit: `mobile: start and cancel through the mailbox (D2787)`
- Reviewer tier: **opus** (write path).

### J5a: the typed launch action (O10)
- Acceptance: the mailbox field set gains a `launch` action with a `mode` field in both validators (Go `Actions` and the JS `ACTIONS`); there is no version field, the field set is the version, so readers that predate it refuse it fail-closed; the mailbox gate, as a library check this task owns and tests, refuses a launch in a costly mode that carries no confirmation. Wiring that check into the running consumer is the P5.1 orchestrator's (see "What this plan does not own").
- Files: `internal/mailbox/message.go`, `internal/mailbox/action.go`, `shared/mailbox-schema/index.js`, `shared/mailbox-schema/vectors.generate.mjs` (which generates `vectors.json`), `internal/mailbox/action_test.go`.
- Interfaces: amends D162 for costly modes; J3's confirm machine.
- Tests: `TestAction*Launch*`, the generated vectors in both languages, `TestConfirmParityWithShared`.
- Command: `go test ./internal/mailbox/ -count=1 && go test ./internal/cli/ -run 'TestConfirmParityWithShared' -count=1 && (cd shared && npm test)`
- Red step: a costly launch with no confirmation is accepted by the gate first.
- Depends on: J3 (the same file, `internal/mailbox/action.go`).
- Commit: `mailbox: a typed launch action with a mode (D162, O10)`
- Reviewer tier: **opus**, independent review (wire format).

### J5b: device signatures on inbound messages (O6)
- Acceptance: a phone paired the new way signs every mailbox message with its device key through the session port, and the gate verifies it against the public key on the device node; **the device node chooses the scheme**: a node with a public key accepts signatures only and refuses a MAC-tagged message, a node without one accepts a MAC only; a message signed by another device's key is refused; deleting the node ends acceptance at once; the phone's start becomes the typed launch.
- Files: `internal/mailbox/mac.go`, `internal/mailbox/verify.go` (new), `shared/mailbox-schema/index.js`, `mobile/src/mailbox/mac.js`, `mobile/src/mailbox/client.js`.
- Interfaces: the C2 port's `signMessage`; the device public key C1 carries; amends D171.
- Tests: `TestGateSignature*` (including the downgrade case), `mailbox/mac.test.js`, `write-path.test.js` still green.
- Command: `go test ./internal/mailbox/ -count=1 && (cd mobile && npm test -- src/mailbox)`
- Red step: a MAC-tagged message for a node that has a public key is accepted first.
- Depends on: C2 (replaces the edge to M3), J5a (the same file, `shared/mailbox-schema/index.js`), J4 (the same file, `mobile/src/mailbox/client.js`).
- Commit: `mailbox: device signatures chosen by the device node (D171, O6)`
- Reviewer tier: **opus**, independent review (authentication).

### J5c: the consumer's signing key and retiring the MAC key (O12)
- Acceptance: the consumer signs outbox replies and heartbeats with its own key pair for phones paired the new way (the MAC signers stay for the others); its public key is published where the pairing endpoint can return it and the phone pins it and verifies with it; re-pairing a phone deletes its old MAC key from all three homes: the laptop keychain (`pairStoreMACKey`), the Secret Manager copy (`pairWriteCloudCopy`, `internal/mailbox/keychain/cloud.go`) and the local config item; a new pairing produces no MAC key and no `Delivery.MACKey`.
- Files: `internal/mailbox/mac.go`, `internal/cli/pair.go`, `internal/mailbox/keychain/cloud.go`, `internal/localcfg/localcfg.go`, `mobile/src/mailbox/client.js`.
- Interfaces: the consumer public key in C1's pair response; `NewOutboxSigner`, `NewHeartbeatSigner`.
- Tests: `TestOutboxSignature*`, `TestPairRetiresMACKey*`, `mailbox/client.test.js`.
- Command: `go test ./internal/mailbox/ ./internal/cli/ -run 'OutboxSignature|PairRetiresMACKey' -count=1 && (cd mobile && npm test -- src/mailbox)`
- Red step: after re-pairing, the old MAC key is still in the keychain first.
- Depends on: J5b (the same files, `internal/mailbox/mac.go` and `mobile/src/mailbox/client.js`), P7 (the same file, `internal/cli/pair.go`).
- Commit: `mailbox: a pinned consumer key for replies; re-pairing retires the MAC key (D171, O12)`
- Reviewer tier: **opus**, independent review (credentials).

### X1: one transcript for voice and text
- Acceptance: voice and text are two views of one conversation with the operator's session; switching loses nothing; replies arrive through the mailbox outbox.
- Files: `mobile/src/text/transcript.js` (new), `mobile/src/text/screen.js`, `mobile/src/text/index.js`, `mobile/src/voice/screen.js`.
- Interfaces: the C2 port; the session bridge is the voice and mailbox design's, not owned here.
- Tests: `text/*.test.js`.
- Command: `cd mobile && npm test -- src/text src/voice`
- Red step: a utterance sent by voice must appear in the text view first.
- Depends on: C2.
- Commit: `mobile: one transcript for voice and text (D2788)`
- Reviewer tier: standard.

### X2: long paste and detail-to-screen
- Acceptance: pasting 100000 characters loses nothing across a mode switch and a rotation; when output is a list of run ids or a table, voice says the detail is on screen and the text view shows it.
- Files: `mobile/src/text/composer.js` (new), `mobile/src/text/detail.js` (new), `mobile/src/text/index.js`.
- Interfaces: X1's transcript.
- Tests: `text/composer.test.js`, `text/detail.test.js`.
- Command: `cd mobile && npm test -- src/text`
- Red step: a 100000-character paste must survive a mode switch; fails first.
- Depends on: X1 (the same `index.js`).
- Commit: `mobile: long paste and detail-to-screen`
- Reviewer tier: standard.

### X3: the share target
- Acceptance: a text or a file shared from another app opens the composer with the text loaded and the prompt prefixed with `/fugaro:spec-to-pr`; the shared content is never sent without the user pressing send.
- Files: `mobile/app.config.js`, `mobile/src/share/receive.js` (new), `mobile/src/share/index.js` (new), `mobile/package.json` (the package M0 names, after the owner's go).
- Interfaces: X2's composer.
- Tests: `share/*.test.js`; the native config guard tests.
- Command: `cd mobile && npm test -- src/share native-config-guard`
- Red step: a share must not send itself; fails first.
- Depends on: M0 (the package), M1 (the same file, `mobile/package.json`), X2 (the composer), Z0 (whether a phone-injected slash command is honoured, U1).
- Commit: `mobile: the share-sheet target (D2788)`
- Reviewer tier: standard.
- Owner-run: Check 734 (the store build and the share extension on a device).

### Z1-Z3: the context module (waits on Check 732)
- **Z1 context gauge.** Acceptance: a gauge shows the session's context fullness if Z0 found it observable, and says "unavailable" if not. Files: `mobile/src/context/gauge.js`, `mobile/src/context/index.js`, `mobile/src/context/gauge.test.js`. Tests: `context/gauge.test.js`. Interfaces: the C2 port and Z0's capability list. Command: `cd mobile && npm test -- src/context`. Red step: the "unavailable" state first. Depends on: Z0 (what is observable is data). Commit: `mobile: the context gauge (D2789)`. Reviewer tier: standard.
- **Z2 compaction.** Acceptance: a compact button and an idle-only auto-compaction at a per-project threshold, if Z0 found injection safe. Files: `mobile/src/context/compaction.js`, `mobile/src/context/compaction.test.js`, `mobile/src/context/index.js`, `internal/projectpolicy/mobile.go`. Command: `cd mobile && npm test -- src/context && go test ./internal/projectpolicy/ -run Mobile -count=1`. Red step: a send while the agent is busy must be refused first. Depends on: Z0, P6 (the same policy file), Z1 (`context/index.js`). Commit: `mobile: compact and idle auto-compaction (D2789)`. Reviewer tier: standard.
- **Z3 usage meter.** Acceptance: five-hour and weekly limits per named token and the active account, if Z0 found them readable within Anthropic's terms. Files: `mobile/src/context/usage.js`, `mobile/src/context/usage.test.js`, `mobile/src/context/index.js`. Command: `cd mobile && npm test -- src/context`. Red step: a token with no readable limit shows "unknown" first. Depends on: Z0, Z2 (`context/index.js`). Commit: `mobile: the usage meter (D2789)`. Reviewer tier: standard.

### I1: the end-to-end join
- Acceptance: the stubs in `internal/mobilesession/stub.go` are deleted; credsvc has two modes: the default mounts **no** mobile route, and `mobile` mode (what P5's service starts in) mounts only the allowlisted mobile routes and none of the existing ones, with a cross-class test showing every existing caller class refused on the mobile routes and a mobile caller refused on every existing route; a hermetic test with a fake signer runs pair, refresh, rotation replay, revoke, restore, strict mode and an identity-project outage against the real verifier and credsvc, and an app-side test runs the same flow through the real identity, pairing, session and continuity modules.
- Files: `internal/credsvc/server.go`, `internal/cli/credentials_serve.go`, `internal/mobilesession/stub.go` (deleted), `internal/credsvc/mobile_e2e_test.go` (new), `mobile/src/session/e2e.test.js` (new).
- Interfaces: wires C1's two sides; covers every edge C1 broke.
- Tests: `TestMobileE2E*`, `session/e2e.test.js`.
- Command: `go test ./internal/credsvc/ -run 'TestMobileE2E' -count=1 && (cd mobile && npm test -- src/session/e2e)`
- Red step: with the stub still mounted, the replay case must fail.
- Depends on: A1, A2, P1, P2, P3, P4, P7, K1, K2, M1, M2, M3, K3.
- Commit: `mobile identity: wire the signer, verifier, credsvc and app together`
- Reviewer tier: **opus**, independent review.

### I2: the surfaces on the real port
- Acceptance: the stub in `shared/mobile-session/stub.js` is deleted; jobs, text, confirmations and verbs run on M3's real session; a test signs the session out and every surface stops.
- Files: `shared/mobile-session/stub.js` (deleted), `mobile/src/jobs/index.js`, `mobile/src/text/index.js`, `mobile/src/session/surfaces.test.js` (new).
- Interfaces: covers every edge C2 broke.
- Tests: `session/surfaces.test.js`.
- Command: `cd mobile && npm test -- src/session/surfaces`
- Red step: with the stub mounted, the sign-out case must fail.
- Depends on: J2, J3, J4, J5b, J5c, X1, X2 (the same file, `mobile/src/text/index.js`), M3.
- Commit: `mobile: surfaces on the real session port`
- Reviewer tier: standard.

### D1: SECURITY.md, its mirror and the claims
- Acceptance: `SECURITY.md` has a subsection in the existing shape (what is reachable, what a stolen token or phone reaches, what revocation does not do, the single point of compromise of F8, the reach of the public service's signer account, which IAM cannot limit to device claims (O8), the mailbox authority a compromise of that service gains by planting a device key (O13), residual risk); the existing paragraph on the pairing key's cloud copy is amended for phones that no longer have one (O6), mirrored byte for byte into `website/content/security.md` by `go run ./internal/clidoc`; `website/data/mobile-claims.yaml` says the identity project is a runtime dependency of pairing and restore and what the synced list holds (D2798).
- Files: `SECURITY.md`, `website/content/security.md`, `website/data/mobile-claims.yaml`, `internal/cli/docs_mobile_identity_security_test.go` (new).
- Interfaces: none.
- Tests: `TestWebsiteSecurityPage*`, `TestDocsMobileIdentitySecurity*`, and `npm run check` in `website/` (which runs `test/mobile.mjs` over the claims file).
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run 'WebsiteSecurityPage|DocsMobileIdentitySecurity' -count=1`
- Red step: the new pin fails before the subsection exists.
- Depends on: I1 (it describes what I1 wires), J5c (it describes the mailbox keys as they end up).
- Commit: `security: the identity project, the session and the synced list (D2798, F27)`
- Reviewer tier: **opus**.

### UP1: `fugaro update` converges the previous version
- Acceptance: after `brew upgrade`, `fugaro update` deploys the credsvc endpoints and the P5 grant, migrates the policy in place, leaves every phone paired the old way working with its D171 MAC (the consumer accepts MAC and device signature during the transition) and offers it identity pairing, and says in one line that it waits for no running run and what each step that can reach one does; `TestUpdateLeavesARunInFlightRunning` still passes and a test of an installation at the previous version converges. The pull request carries a `### For operators` entry: run `fugaro update`, after Checks 730 and 731.
- Files: `internal/cli/update_mobile.go` (new), `internal/cli/update_mobile_test.go` (new), the release note file for the next version under `docs/releases/` (named by `docs/releases/README.md`'s process).
- Interfaces: none.
- Tests: `TestUpdateMobile*`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateMobile|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: an installation at the previous version must fail to converge first.
- Depends on: I1, P5, P6, P7; the second service and its grant are applied by `update`.
- Commit: `update: converge to identity pairing with runs in flight (D2795)`
- Reviewer tier: **opus**, independent review.

## Parallel execution

The graph is [implementation-plan.graph.json](implementation-plan.graph.json), read with `fugaro plan graph --plan docs/mobile-identity-pairing/implementation-plan.md`. It reads it without refusal.

**Levels (tasks in one level run at once)**

| Level | Tasks |
| --- | --- |
| 0 | DA, T0, P0, C1, C2, M0*, Z0*, P5†, A1, A2, A3, P2, P6, K1, J1 |
| 1 | P1, P3, P4, K2, P7, M1, M2, J2, J3, J4, X1, Z1 |
| 2 | M3, X2, Z2, J5a |
| 3 | K3, X3, Z3, J5b |
| 4 | I1, J5c |
| 5 | D1, UP1, I2 |

\* owner-run, on devices. † grants given in the plan (O1, O8); the task still needs its own go. The graph also lists the anticipated contract changes CR1 and CR2 (level 2 after C1 and P3; level 3 after C2, J3 and Z2); they run only if a consumer finds its contract wrong, and only after it.

**Critical path (as the read prints it):** `C1 -> M1 -> M3 -> K3 -> I1 -> D1`, bottleneck C1. It is sequential because M3 builds on M1's identity records, K3 needs M3's real storing and refresh behaviour, and I1 joins everything.

**Estimate, as the read printed it:** `ready in 540–2160 min, no history yet for 42 tasks (42 tasks; seeds for 42; from the seeds: no history is cached on this machine yet)`. This is informational and reserves no budget: it is what the tiers and this machine's history predict, and it moves as runs finish.

### The dependencies the plan broke (14) and what they were

`fugaro plan graph` reports **14 broken, 31 kept, 6 added; before 10 tasks, weight 20; after 6 tasks, weight 14; improves.** Each is an edge where the dependent needed an interface only:

| Dependent | Was waiting on | Contract | Integration | Why an interface is enough |
| --- | --- | --- | --- | --- |
| P1 | A1 | C1 | I1 | the verifier needs the assertion's claims and JWKS shape, not the signer's behaviour |
| P3 | P1 | C1 | I1 | P3 calls the `Verifier` interface and needs its signature, not its checks |
| P4 | P3 | C1 | I1 | the refresh path needs the device node's shape, not the pairing code |
| K2 | P3 | C1 | I1 | restore needs the pair response shape, not the pairing code |
| M1 | A1 | C1 | I1 | identities ask for an assertion by the contract's request shape |
| M2 | P3 | C1 | I1 | the app needs the pair request and error codes, not the endpoint |
| M3 | P4 | C1 | I1 | the loop needs the refresh request and response shapes |
| K3 | K2 | C1 | I1 | the sync client needs the restore shapes |
| P7 | P4 | C1 | I1 | revoke needs the `RefreshStore` shape, not the session code |
| J2 | M3 | C2 | I2 | the view needs the session's endpoint, role and token accessor |
| J3 | M3 | C2 | I2 | the policy needs the session's role |
| J4 | M3 | C2 | I2 | the verbs send through the session port |
| X1 | M3 | C2 | I2 | the transcript runs over the session port |
| J5b | M3 | C2 | I2 | message signing goes through the port's `signMessage`, not M3's key code |

### The edges kept, and why a contract would not do

| Edge | Reason |
| --- | --- |
| P7 after P2 | the same file, `internal/cli/pair.go` |
| P3, K2 after P6 | `Policy` reads P6's `mobile.*` fields |
| X3 after M1 | the same file, `mobile/package.json` |
| I2 after X2 | the same file, `mobile/src/text/index.js` |
| P3, P4, K2 after P0 | the shared closed purpose set in `internal/credsvc/audit.go`; P0 is small and settles it once |
| M3 after M1 | M3 attaches each session's refresh state to the identity records M1 defines, an identity id keying the per-identity items: M1's store behaviour, not only a shape |
| K3 after M3 | restore hands each project's first session to M3's store and loop, so K3 needs M3's real storing behaviour |
| M1, M2, X3 after M0; Z1, Z2, Z3 after Z0 | behaviour: which packages work, what the session exposes, are data a spike finds |
| J3 after P6; Z2 after P6 | the policy fields P6 defines |
| J5a after J3 | the same file, `internal/mailbox/action.go` |
| J5b after J5a and J4 | `shared/mailbox-schema/index.js` and `mobile/src/mailbox/client.js` |
| J5c after J5b and P7 | `internal/mailbox/mac.go`, `mobile/src/mailbox/client.js` and `internal/cli/pair.go` |
| X2 after X1; X3 after X2 | `text/index.js`; the share lands in X2's composer |
| Z2 after Z1; Z3 after Z2 | `context/index.js` |
| I1, I2, D1, UP1 after what they join or describe | the integration runs both sides; the documents and the update describe behaviour I1 wires |

### Contract changes anticipated

- **CR1** (amends C1): P3 may find the strict-switch state missing from the pair response. It would reach M2, K3 and K2; launched ones get a follow-up run on their pull request naming `internal/mobilesession/contract.go` by path.
- **CR2** (amends C2): J3 or Z2 may find a policy value the phone needs missing from the session. It would reach J2, J4, X1 and M3.

### Shared-files table (no level holds two writers of one file)

| File | Writers, by level |
| --- | --- |
| `internal/cli/pair.go` | P2 (0), P7 (1), J5c (4) |
| `internal/credsvc/audit.go` | P0 (0) |
| `shared/confirm/*`, `internal/mailbox/action.go` | J3 (1) |
| `shared/command/*` | J4 (1) |
| `mobile/src/mailbox/client.js` | J4 (1), J5b (3), J5c (4) |
| `internal/mailbox/action.go` | J3 (1), J5a (2) |
| `internal/mailbox/mac.go` | J5b (3), J5c (4) |
| `shared/mailbox-schema/index.js` | J5a (2), J5b (3) |
| `internal/projectpolicy/version.go` | J1 (0) |
| `internal/projectpolicy/mobile.go` | P6 (0), Z2 (2) |
| `mobile/src/accounts/secure-store.js` | M1 (1) |
| `mobile/src/accounts/secret.js` | M3 (2) |
| `mobile/src/accounts/expo-secure-store.js` | K3 (3) |
| `mobile/package.json` | M1 (1), X3 (3) |
| `internal/cli/pair_events.go` | P2 (0) |
| `mobile/src/text/index.js` | X1 (1), X2 (2), I2 (5) |
| `mobile/src/jobs/index.js` | J2 (1), I2 (5) |
| `mobile/src/context/index.js` | Z1 (1), Z2 (2), Z3 (3) |
| `shared/mobile-session/stub.js` | C2 (0), I2 (5, deletes it) |
| `internal/mobilesession/stub.go` | C1 (0), I1 (4, deletes it) |
| `ee/identitysvc/terraform/*` | A3 and K1 write different files |
| `docs/gcp-live-checklist.md` | T0 (0) |
| `SECURITY.md`, `website/content/security.md` | D1 (5) |

## Checks the plan takes (reserved 730-739, owner-run)

| Check | What it measures | Who runs it |
| --- | --- | --- |
| 730 | the identity project exists; the KMS key is non-exportable; the signer and JWKS answer; the key ceremony | owner |
| 731 | the Google, Microsoft and Apple provider consoles are set up; one email under several providers behaves as the plan assumes (U8); whether a Microsoft token carries a tenant-verified email flag (U9) | owner |
| 732 | context fullness, injected `/compact` when idle, the auto-compact threshold and the usage limits (U1-U3) | owner |
| 733 | provider tokens, the device key and the share route and the synced keychain on two real phones (U4, U5, U7, U10) | owner |
| 734 | store builds, the share extension, store review declarations | owner |
| 735 | live, after `fugaro update`: pair, refresh after an hour, revoke, restore, strict mode, outage, with two phones | owner |
| 736 | the Firebase Auth, KMS and Cloud Run quotas at the stated scale (U6) | owner |

## After-deploy steps

Order: Check 730 (apply the identity project), Check 731 (provider consoles), release, then `fugaro update` on each installation (UP1's `### For operators` entry: run `fugaro update` and what it asks), then Check 735. Mobile ships under its own `mobile-vX.Y.Z` tag with notes in `docs/releases/mobile/`.

## Considered and declined

- Putting identity in the existing `fugaro-prod` (D2781): one boundary for website deploys, licence signing and every customer's pairing.
- A new per-installation service for the security role (D2782): the owner chose credsvc instead; its consequences are P0, P3-P5, O1, O8 and T1. The second public Cloud Run service of the credsvc image (F31) is close to this declined option in substance; the owner confirmed it, and reconfirmed it after the signer correction (O8).
- Plain Firebase sessions (D2783): the spec's device-bound, idle-expiring, re-checked refresh is not achievable with them.
- Always requiring a QR for a new device (D2784): drops the spec's continuity; the strict switch offers it per project.
- A workspace user directory (D2780): reverses D1295 on `main`; open pull request #3195 proposes workspace people and roles, so O5 re-checks this if it lands.
- Direct service calls for start and cancel (D2787): a second write path.
- A cloud interactive session for the text view (D2788): a new product surface.
- The identity project storing workspace session tokens: the spec forbids it.

## What this plan does not own

The P5.1 always-on orchestrator that consumes the mailbox, and P5.1g's biometric re-auth: J3 and J4 build against them and I1/I2 do not need them to pass; the end-to-end start and cancel on a real project (Check 735) does. **Requirement on that consumer, stated here so its own plan pins it:** it checks the sender's role with `internal/access` (`ActionLaunch`, `ActionCancel`) at consume time against the current device node, never trusting a role carried in the message, and enforces the confirmation J3's Go side defines.

## Coverage

Every decision and finding lands in a task or in a declined note: D2780 (P3, P6), D2781 (A3), D2782 (P0, P3-P5), D2783 (P4, M3), D2784 (K2, P6, D1), D2785 (P3, P6, M1), D2786 (J3, J5a, P6, DA), D2787 (J4), D2788 (X1, X3), D2789 (Z0-Z3), D2790 (M1, DA), D2791 (P2, P3), D2792 (C1, A1, A2, P1), D2793 (M1), D2794 (P4, P7, M3), D2795 (UP1), D2796 (P6), D2797 (J1, J2), D2798 (D1), D2799 (P2). F1-F41 land in the same tasks (F31 in P5, F32 in P4, P7, K2, F33 in O6, O12, J5b and J5c, F34 in P3, F35 in C1 and K2, F36 in J3, F37 in U9, F38 in P1 and O7, F39 across A1, K1, P1, P3, P4, C1 and M0, F40 in O9, F41 in O5); F24 in the conventions; F26 in T0, DA and the task commits; F28, F29 in Checks 731, 734, 736.

## Open items the plan carries

**Answered 2026-11-18:** O6 (a per-message session signature in place of the MAC: J5b and J5c, not the recommendation), O8 (a public second service with a separate signer account; the owner reconfirmed the corrected consequence, that IAM cannot limit its claims and the route allowlist, tests and review are the control), O10 (a typed launch action: J5a), O11 (D2799 confirmed), O12 (a consumer signing key the phone pins: J5c), O13 (the public service's mailbox reach accepted and stated in `SECURITY.md`: D1), O1 (the signer account's grants, the database role and the cover row: the owner's go in the plan), O3 (the `outcomes` read for all roles) and O9 (`mobile.enabled` on in policy, the routes answering only after `update` publishes it). **Still open:** O2 (the amended D162 and D232 wording), O4 (the defaults), O5 (#3195), O7 (key pinning). Each is in [reconciliation.md](reconciliation.md).
