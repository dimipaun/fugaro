# Mobile identity, pairing and text interface: reconciliation

**Where this disagrees with [spec.md](spec.md), this document wins.** The spec was written without the repository in view; this is what the repository holds, what the spec gets wrong or leaves open, and the owner's answers. Checked against `origin/main` at `01fe41f30` on **2026-11-18**. Every citation is by path and symbol, never by line number. Owner: Dimi Paun.

Numbers taken for this folder: decisions **D2780-D2829** (D2780-D2789 asked, D2790-D2798 resolved, the rest reserved), checks **730-739**. Findings are `F1`.. per folder, as in the neighbouring folders.

## Capability map (approved 2026-11-18)

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `mip-assertion` | Fugaro-side signer: assertion format, Cloud KMS key, JWKS | none |
| `mip-identity` | Identity providers in the identity project; the app's several identities | `mip-assertion` (a signed-in identity is what it asserts) |
| `mip-pairing` | QR with email and nonce, verifier and policy in credsvc, session and refresh tokens, per-device revocation | `mip-assertion`, `mip-identity` |
| `mip-continuity` | Encrypted paired-list sync, the two project switches, keychain backup, restore | `mip-identity`, `mip-pairing` |
| `mip-jobs` | Running-jobs list across paired projects, cancel and start, role checks, confirmations | `mip-pairing` |
| `mip-text` | Shared voice and text session, long paste, share target, detail-to-screen | `mip-pairing` |
| `mip-context` | Context gauge, compact button, auto-compaction, per-account usage meter | none (spike first, D2789) |

Build order: assertion, identity, pairing, then continuity, jobs, text and context side by side. `mip-pairing`'s interface to continuity, jobs and text (the session token and the project endpoint) is the contract the plan settles first.

## Assumptions

- **A1.** "Workspace" in the spec means a project (one installation with its own device node), not the optional grouping `docs/workspaces` defines. Confirmed by D2780.
- **A2.** "Security service" is a service that exists per installation and is online when the owner's laptop is off. It does not exist today (F3); D2782 chooses it.
- **A3.** "Dimi uses the Claude Code text interface daily" means the app's text view drives the operator's own Claude Code session, as the voice design already does (D2788).
- **A4.** "Kill a job" is cancelling a run (`fugaro cancel`), not the budget kill switch, which stays refused by voice.
- **A5.** The app stays JavaScript on Expo; REST sign-in as today (`mobile/src/pairing/session.js`), not a second Firebase SDK instance per project.
- **A6.** Pre-1.0: no backward compatibility in shapes, but `fugaro update` must still converge an installation at the previous version (CLAUDE.md hard rule), so the migration is in F25.

## What the repository already has

| Spec concept | Repository |
| --- | --- |
| Pairing QR for a named person | `internal/cli/pair.go` `newPairCmd`, `pairQR` (config, `api_key`, one-shot `token`, `uid`, `expires_at`; no email, no nonce as such); `internal/budget/token/pairing.go` (5-minute `PairingMaxLifetime`, claim `pu`) |
| Project allowlist and roles | `config/admin/devices/$uid` (`pairDeviceNode`), `internal/mobileauth` (`Entry`, `Resolve`, `NormalizeEmail`), `internal/access` (`RoleViewer`, `RoleOperator`, `RoleAdmin`) |
| Signed assertion with a single-use id, bound to an audience | `ee/coordinator/oauth/workspace_assertion.go`, `workspace_signer.go` (workspaces design, D1290-D1295) |
| Cloud KMS asymmetric signing | `ee/keyservice/kmssigner.go`, `ee/keyservice/terraform/keyring/main.tf` (licence keys) |
| JWKS fetch and cache | `ee/coordinator/mcpauth/firebase_keys.go`, `internal/credsvc/auth.go` (`GoogleKeysURL`), Google's keys only |
| Several identities on the phone | `mobile/src/accounts/` (an account is one installation, D114), not an identity |
| Revocation | `fugaro pair revoke` deletes the device node and the Firebase Auth user |
| Text input | `mobile/src/text` (`createTypedInput`, `createTextScreen`), same pipeline as voice |
| Spoken confirmation | `shared/confirm` (read-back then phrase) |
| Running jobs | `mobile/src/jobs` (read-only: spend, caps, kill state) |
| Fugaro's own production project | `fugaro-prod` (website deploy, licence key service, mobile telemetry); not an identity project |

## Findings

- **F1. Wrong** (`mip-identity`). There is no Fugaro-Prod authentication project. `fugaro-prod` exists for the website, the licence key service and mobile telemetry (`docs/design/key-signing-service.md`, D2194); it has no Firebase Auth providers and no `functions/`. Consequence: the identity project is new (D2781).
- **F2. Rule** (`mip-assertion`). The product never calls `fugaro-prod` at runtime: licence tokens verify offline, and the docs call it "not a phone-home". The spec makes the identity project a runtime dependency of pairing, new-device restore and refresh. Consequence: this is a deliberate new dependency; its outage blocks only new pairings, restores and, after a refresh credential lapses, re-issue (spec's last corner case), and `SECURITY.md` and `website/data/mobile-claims.yaml` say so (D2798).
- **F3. Wrong** (`mip-pairing`). The "project's security service" does not exist. Device and pairing tokens are minted on the owner's machine by the CLI (`internal/budget/token`, `IAMSigner` calling `signJwt`), and `internal/credsvc` serves runs and holds secrets (`Authenticator.Verify`). Consequence: D2782 chose credsvc; this puts a new signing grant and new endpoints into a service under `.claude/rules/credentials-service.md` (F12, T1).
- **F4. Wrong** (`mip-pairing`). The spec's QR holds endpoint, email, nonce. Today's `pairQR` carries the project's `database_url`, restricted `api_key` and a one-shot custom token, and the operator types a code back (the phone's X25519 key digest, `pair.go`, `mobile/src/pairing/session.js`). Consequence: the new QR carries endpoint, email, nonce only; the phone gets `database_url` and `api_key` from the service on success; D229 already planned `fugaro pair --email --role` and this builds it.
- **F5. Rule** (`mip-identity`). D221 (no provider session ever reaches a project) holds: the assertion is an identity claim checked once. But D232 (two secure items, no provider credential stored), D234 (no per-project Firebase app on the phone) and D114 (an account is an installation) were written for a phone with no identity of its own. Consequence: D2790.
- **F6. Rule** (`mip-pairing`). D222/D225: the allowlist is the device node keyed by uid, with email and role as fields, role read by the CLI and consumers, never by the rules. The spec keys members on email plus the provider's stable id. Consequence: the node gains `provider` and `sub` fields (additive) and role enforcement stays out of the rules (D2791).
- **F7. Gap** (`mip-pairing`). Firebase refresh tokens neither rotate, bind to a device, idle-expire nor call us (`SECURITY.md`, Fugaro workspaces). The spec's 30-day device-bound refresh with re-check on every refresh is not what the app has. Consequence: D2783.
- **F8. Gap** (`mip-continuity`). Silent restore with no QR means a compromise of the signing key, or an insider, can mint an assertion for any member into every project carrying that member. The QR nonce protects only the first pairing. Consequence: D2784.
- **F9. Gap** (`mip-pairing`). An invite names only an email, and Microsoft's email claim can be set by a tenant admin unverified; Apple may relay (spec's own note). The first pairing must record the provider's stable id, and the accepted-provider policy decides what is trusted. Consequence: D2785.
- **F10. Exists** (`mip-assertion`). The workspaces assertion (single-use `jti`, `aud` bound, verified against a pinned key) and the KMS Ed25519 signer are the pattern. Consequence: D2792.
- **F11. Rule** (`mip-assertion`). `.claude/rules/ee.md`: no agent holds, mints with or operates a production signing key; the ceremony is owner-run; nothing outside `ee/` imports `ee/`. Consequence: the signer is agent-written code in `ee/`, deployed and keyed by the owner; the verifier lives in `internal/` (D2792, Check 730).
- **F12. Rule** (`mip-pairing`). Under D2782, credsvc rules apply: independent review before merge; exactly one audit line for every refusal after identity is verified; no token or key value in a line or error; its only project-level grant is the custom role `fugaroCredentialsDatabase`; `config.ServiceHeldSecretNames` stays empty; `SECURITY.md` mirrored byte for byte into `website/content/security.md`; the owner runs every runbook step. Minting custom tokens needs a grant on the project's token-signer service account that credsvc does not hold today. Consequence: T1 and O1.
- **F13. Gap** (`mip-identity`). The Firebase SDK holds one current user per app instance, so several identities need either several instances or REST sessions. Consequence: REST sign-in with a provider token, one secure-store item per identity, as `mobile/src/accounts` does for installations (D2793). Whether each provider's native SDK yields a token usable that way is UNVERIFIED (U4).
- **F14. Exists** (`mip-pairing`). Roles viewer, operator, admin exist (`internal/access`, `ActionLaunch` and `ActionCancel` need operator). The spec's "session token bound to role" is a display claim; enforcement stays at the consumer (D2791).
- **F15. Rule** (`mip-continuity`). Syncing each user's paired list (opaque ids, endpoints) to the identity project, on by default, tells Fugaro which customers' endpoints a person pairs with. `SECURITY.md` and `website/data/mobile-claims.yaml` make no-phone-home claims that this changes. The spec decided the default; the claims follow (D2798).
- **F16. Gap** (`mip-continuity`). The spec's two switches have no home: D1304 gives workspaces no mobile surface. Consequence: both are fields of the published project policy (`internal/projectpolicy`), read by credsvc, and shown by `fugaro doctor` so admins see what is stored (D2796).
- **F17. Rule** (`mip-jobs`). The no-prompt policy (`docs/no-prompt-policy`) governs the CLI and agent sessions, not a phone's spoken read-back; the existing rulings do: cancel needs a read-back, launch needs none (D162), the kill switch and budget writes stay refused by voice, and P5.1g (approved) holds the biometric path. Consequence: D2786.
- **F18. Exists, Rule** (`mip-jobs`). The jobs list is read-only and `mobile/src/jobs/scope.test.js` forbids run rows; start and cancel from the phone are not built, and P5.1d's launch half is parked as an owner decision (`docs/status.md`). The spec requires both. Consequence: D2787 and D2797.
- **F19. Wrong** (`mip-jobs`). "Kill a job" in the spec is `fugaro cancel RUN` (`internal/cli/cancel.go`), not the kill switch; the ruling that the kill switch stays refused by voice stands (A4).
- **F20. Exists** (`mip-text`). A text screen exists (`mobile/src/text`) on the voice pipeline. Missing: a shared transcript, long-paste handling, a share target (`mobile/package.json` has no share-intent package), on-screen detail for dense output and any biometric.
- **F21. Gap** (`mip-text`). spec-to-pr is interactive (it asks the user decisions), so a one-shot cloud run cannot host it, and no mechanism preselects a skill. Consequence: D2788; "preselected" is a prompt prefix naming `fugaro:spec-to-pr`, and whether a phone-injected slash command is honoured is UNVERIFIED (U1).
- **F22. Gap** (`mip-context`). No code exposes a session's context fullness, injects `/compact`, or reads five-hour and weekly subscription windows; `docs/design/failure-classes.md` O6 only notes a spent subscription has no class. The spec's remark about Claude Code's built-in auto-compact is not verifiable here. Consequence: D2789; U1-U3.
- **F23. Gap** (`mip-pairing`). "Sign out everywhere" cannot reach projects from the identity project, which never learns of them except through the synced list, and sync may be off. Consequence: D2794.
- **F24. Gap** (spec completeness). Figures the spec leaves vague, each an open item with a proposed default marked `(default)`: QR nonce lifetime (default 10 minutes), "recent enough" authentication (default 24 hours, per-project), assertion lifetime (default 2 minutes), key rotation (default 90 days with 30 days overlap), "smooth" long paste (default 100000 characters without dropping input), "about 30 days" idle (kept), "about one hour" access (kept).
- **F25. Gap** (`mip-pairing`). Installations already hold phones paired the old way (`m~` uids, custom tokens). `fugaro update` must keep them working and converge the installation to the new service in the same release, with a test that an installation at the previous version converges, and must be safe with runs in flight. Consequence: D2795.
- **F26. Rule** (all). CLAUDE.md: docs are tested (a new folder needs its own pin test), `docs/status.md` is moved by whoever merges the plan, never by a task's own pull request, and `### For operators` carries each after-deploy step.
- **F27. Rule** (`mip-pairing`, `mip-assertion`). `SECURITY.md` needs a new subsection in the existing shape: what is reachable, what a stolen token reaches, what revocation does not do, residual risk, including the F8 single point of compromise.
- **F28. Gap** (`mip-identity`). Provider setup is owner work with no agent path: Google OAuth client, Microsoft app registration, Sign in with Apple service id and key, email templates, and App Store and Play data-safety declarations (Checks 731, 734).
- **F29. Gap** (`mip-assertion`, `mip-pairing`). Platform limits assumed away at "hundreds of thousands of workspaces": Firebase Auth quotas, Cloud KMS sign requests per minute per key, credsvc cold start on the hourly refresh path. UNVERIFIED (U6).

## Decisions

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| **D2780** | What does the spec's "workspace" mean? | The project | **2026-11-18: the project (Recommended).** Membership, roles, accepted providers and both switches live per project; the grouping workspace gets no mobile surface. |
| **D2781** | Where do identity and the signer live? | A dedicated identity project | **2026-11-18: a dedicated identity project (Recommended).** It plays the spec's "Fugaro-Prod" role with its own blast radius, apart from the website deployer and licence keys. |
| **D2782** | Which component is the security service? | A new small per-installation service | **2026-11-18: add endpoints to credsvc. Not the recommendation**, followed through: F12, T1 and O1 carry its consequences. |
| **D2783** | How is the session built? | Fugaro refresh token, hourly custom token | **2026-11-18: as recommended.** The service issues a rotating, device-key-bound refresh token (hashed at rest, replay revokes the family) and mints a one-hour Firebase custom token per refresh; the app never keeps the Firebase refresh token. |
| **D2784** | Restore trust, the single point of compromise | As spec plus a per-project strict switch | **2026-11-18: as recommended.** Silent restore by default; a per-project switch requires a fresh QR for a new device; an audit line per restore; the risk stated in `SECURITY.md`. |
| **D2785** | First pairing trust | Record on first use with accepted-provider policy | **2026-11-18: as recommended.** The first (email, provider id) is recorded on the node; later pairings must match; defaults accept Google, verified email and password, Microsoft only with a tenant-verified email claim, Apple only with a real email; a project can narrow the list. |
| **D2786** | Confirmations on cancel and costly starts | Amend D162 for costly modes only | **2026-11-18: as recommended.** Cancel keeps its read-back; a start in a rigor mode the project policy names (default `pro` and `critical`) needs a read-back and, per a project switch, biometric re-auth through P5.1g's path; plain starts stay unconfirmed; the kill switch and budget writes stay refused by voice. Amends D162. |
| **D2787** | How start and cancel reach the project | Through the mailbox and the P5.1 consumer | **2026-11-18: as recommended.** One write path; answers P5.1d's parked launch question; depends on the P5.1 consumer. |
| **D2788** | Which agent session the text view drives | The operator's own Claude Code session via the mailbox | **2026-11-18: as recommended.** One transcript for voice and text; "preselected" is a prompt prefix naming the skill. |
| **D2789** | How to treat `mip-context` | Spike first, build what it proves | **2026-11-18: as recommended.** Check 732 settles what is observable and injectable; the build tasks wait on it. |

### Resolutions (one defensible answer, stated)

- **D2790** (F5). An identity is a new layer above accounts: D114 stands (a paired project is an account); D232 is **amended** to add one secure-store item per signed-in identity (its provider session, never a project credential); D221 and D234 stand.
- **D2791** (F6, F14). The device node gains `provider` and `sub` (additive, no field changes meaning, no rules change). Role stays on the node and is enforced by the consumer; the token's role claim is for display.
- **D2792** (F10, F11). The assertion is an EdDSA JWT with `iss`, `aud` (the project), `sub` (provider stable id), `email`, `email_verified`, `provider`, `auth_time`, `jti`, and the project's challenge (the QR nonce at pairing, a service-issued challenge at restore); signed in Cloud KMS (Ed25519); public keys at a JWKS URL; short lifetime. Signer in `ee/`, verifier in `internal/`.
- **D2793** (F13). Identities sign in by REST with a provider token; no second Firebase SDK instance.
- **D2794** (F23). "Sign out everywhere" signs out the identity project and, while the app holds a live session, calls each paired project's revoke-self endpoint for this identity; a lost phone with no other device is revoked by an admin (`fugaro pair revoke` also revokes the refresh family).
- **D2795** (F25). Existing paired phones keep working untouched (nodes without `sub` stay valid) and are offered identity pairing on their next sign-in; `fugaro update` deploys the new credsvc endpoints and carries the migration with a test of the previous-version installation.
- **D2796** (F16). The two switches are fields of the published project policy, default on, with admins shown what is stored.
- **D2797** (F18). The spec amends `mobile/src/jobs/scope.test.js`'s ban on run rows: a running-jobs view is in scope for operators and admins and viewers.
- **D2798** (F2, F15). `SECURITY.md`, its website mirror and `website/data/mobile-claims.yaml` are updated in the same pull requests as the behaviour.

## Open tensions

None between answers. D2782 (credsvc) and `.claude/rules/credentials-service.md` pull against each other only by consequence (T1 below).

- **T1.** credsvc gains a grant on the token-signer service account and mobile endpoints. The rules allow changes to `config.ServiceServedAuths` only on the owner's go and keep its project-level grant to one custom role.

## Open items

- **O1.** Confirm the new credsvc grant (T1): which exact permission on which service account, and that it is not a project-level grant. Needs the owner's explicit go before the credsvc task is approved.
- **O2.** Confirm the amended D162 (D2786) and D232 (D2790) wording.
- **O3.** Confirm D2797's widening of the jobs scope.
- **O4.** Confirm F24's proposed defaults.

## UNVERIFIED

- **U1.** Whether Claude Code exposes the session's context fullness to a hook or status line, and honours an injected `/compact` or slash command when idle. Settled by Check 732, run by the owner.
- **U2.** Whether Claude Code's built-in auto-compact threshold is configurable. Same check.
- **U3.** Whether the five-hour and weekly limits per OAuth token are readable by code, and whether reading them is within Anthropic's terms. Owner, Check 732.
- **U4.** Whether Google, Microsoft and Apple native SDKs on Expo yield provider tokens usable with Firebase's REST `signInWithIdp`, and with several identities at once. A mobile spike task, owner-run on devices (Check 733).
- **U5.** Whether a hardware-backed, non-exportable signing key is available in Expo for the device binding; otherwise the key is software and the plan says so. Mobile spike.
- **U6.** Firebase Auth, KMS and Cloud Run quotas at the stated scale. Owner reads the quotas pages.
- **U7.** The share-target mechanics (iOS share extension, Android intent filter) in this Expo setup. Mobile spike; owner runs the store builds (Check 734).
- **U8.** How Firebase treats one email under several providers (account linking setting) in the identity project. Owner, Check 731.
