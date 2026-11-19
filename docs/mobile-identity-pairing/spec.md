# Fugaro Mobile App — Identity, Pairing and Text Interface Spec

November 2026. Amends fugaro-mobile-app-spec.md and the Fugaro Mobile App Auth Spec. The hands-free voice experience (wake word "Fugaro", switching projects and agents by name) stays as already specced; this document changes authentication and pairing and adds job controls and a text interface.

## Context and principles

- The mobile app is **person-centric**, not project-centric. Unlike the CLI and console, which are tied to a workspace and project on one machine, the app represents one person who may belong to many projects across many workspaces and companies.
- There is **one global app** in the App Store and Play Store, used by every Fugaro customer, potentially hundreds of thousands of workspaces.
- The app's identity layer is the identity provider in **Fugaro-Prod**, the common production Firebase project for Fugaro itself.
- **Workspaces are independent and never report back to Fugaro-Prod.** Fugaro-Prod therefore cannot list a user's workspaces or projects. A future opt-in for workspaces to share membership for convenience may be considered, but it must never be required.
- Access to a project always comes from workspace membership and roles, as defined in the workspace user-management model. Seeing a QR code must never be enough to gain access.

## Why QR-only pairing is not enough

A QR code by itself is a bearer secret: anyone who sees it can pair. That would bypass workspace membership entirely. The QR code is kept, but only as a way to locate a project and start a pairing for a named person. It is never the credential.

## Identity providers in Fugaro-Prod

Enable in Fugaro-Prod Firebase Auth:

1. Google SSO.
2. Microsoft (Teams / Entra) SSO.
3. Sign in with Apple (required by App Store rules). Note: Apple may hide the real email behind a private relay address, which will usually not match a workspace invite. See Resolved decisions for how relayed Apple identities are handled.
4. Email and password, **with email verification required** before the identity can be used for pairing.

## Multiple identities per app

- Like Gmail's multiple accounts, the app holds several signed-in identities at once, for example a personal Google account and a work Microsoft account for each company.
- Each identity records its email, provider and last authentication time.
- Users can add, remove and re-authenticate identities independently.

## Pairing flow

1. A workspace or project admin generates a pairing QR code for a specific member. The QR contains the project endpoint (its security service), the required email, and a one-time nonce with a short expiry.
2. The app scans the QR and looks for a signed-in identity matching the required email. Only that identity is ever disclosed, so the user does not reveal their other accounts to the workspace. If no identity matches, the app prompts the user to add one.
3. The app asks a small Fugaro-Prod function for a **pairing assertion**. The function verifies the user's Fugaro-Prod ID token and returns an assertion signed with Fugaro-Prod's private key, containing: email, email verified flag, sign-in provider, auth time, the target workspace or project id as audience, and the QR nonce.
4. The app sends the assertion to the project's security service.
5. The security service checks the signature against Fugaro-Prod's published public keys, checks the audience and nonce, then applies its own policy:
   - Is this email a member of the workspace, and with which role (viewer, operator, admin)?
   - Is this provider accepted? A workspace may, for example, accept only Google SSO.
   - Is the authentication recent enough?
6. On success it returns a signed, project-scoped session token (short-lived, refreshable) bound to that identity and role. On failure it returns a clear error code, for example provider not accepted (with the accepted providers), authentication too old (sign out and back in), not a member, or nonce expired.

### Why a workspace-bound assertion instead of the raw ID token

A raw Fugaro-Prod ID token has Fugaro-Prod as its audience, so a malicious workspace could replay a user's token against another workspace within its lifetime. Binding each assertion to one workspace and one nonce prevents that replay.

## Multi-device continuity

- **Fugaro-Prod never stores workspace session tokens.** Holding live credentials for thousands of customer projects would make Fugaro-Prod a high-value target and would break workspace independence.
- Instead, the app syncs only the **list of paired workspaces and projects** (opaque ids, endpoints, which identity was used; see Resolved decisions) for each user, for example in Fugaro-Prod RTDB or Firestore.
- On a new device, after the user signs in with the same identity, the app requests a fresh assertion for each paired workspace and silently obtains new session tokens, with no QR scan needed, since the workspace already knows the member.
- Removing a member in a workspace revokes their access on every device the next time a token is refreshed.

## Job controls

The app does not need to match the console's features. Required on the road:

- See currently running jobs across paired projects.
- Kill a job (operator or admin role).
- Start a job (operator or admin role), by voice or text.

## Text interface

Modelled on the Claude Code text interface on the phone, which Dimi uses daily for development.

- **One session, two modes.** Voice and text are two views of the same conversation with the agent. The user can switch at any time without losing context.
- **Long text input.** Pasting large blocks, especially specs for the spec-to-PR skill, must work smoothly.
- **Share sheet target.** The app registers as a share target so a spec file or text can be shared directly from another app (for example Claude) into Fugaro, with the spec-to-PR skill preselected, avoiding copy and paste.
- **Detail goes to the screen.** In voice mode, when output is too dense to follow by ear (lists of PR ids, job tables), the agent says it has put the details on screen and shows them in the text view.

## Context gauge and usage meter

- A gauge at the top shows how full the interactive session's context is.
- A **compact button** right on the gauge triggers compaction in one tap.
- **Automatic compaction** at a user-set threshold (for example 40% full), sent only when the agent is idle so it never interrupts work. Configurable per project. This works around Claude Code's built-in auto-compact letting context grow very large, which drives up token use in very long-lived conversations.
- A usage meter shows the five-hour and weekly limits for each named OAuth token or account, and which account is currently active, given failover between accounts.

## Resolved decisions

- **Sign in with Apple.** Apple can be used to sign in to the app, but an Apple identity is usable for pairing only if the user shared their real email. If the email is relayed, the app asks the user to add a Google, Microsoft or verified email identity for that workspace.
- **Assertion signing keys.** Fugaro-Prod's private signing keys live in Cloud KMS and never leave it. Public keys are published at a standard keys (JWKS) URL that workspaces fetch and cache. Keys rotate every few months, with the previous key still accepted for an overlap period.
- **Session tokens.** Access tokens last about one hour. Refresh tokens are bound to the device and expire after about 30 days without use. Every refresh re-checks membership and role, so removing a member takes effect within the hour. After refresh-token expiry the app silently requests a fresh assertion if the Fugaro-Prod sign-in is still valid.
- **Workspace switches.** Each workspace has a switch allowing mobile app connections (on by default, opt-out). A second switch allows storing the user's paired-workspace entry in Fugaro-Prod for multi-device continuity, also on by default; admins are told clearly what is stored.
- **When sync is off.** Moving to a new device requires pairing again by QR code, except that the app may back up its list in the phone platform's own synced keychain (iCloud Keychain or Google's equivalent), which Fugaro-Prod never sees.
- **Storage of the paired-workspace list.** Store the minimum: opaque workspace and project ids plus endpoints; readable names stay on the device. The list is encrypted in RTDB with a key held in Cloud KMS, decryptable only by a small function and only for the signed-in user who owns it. The threat addressed is someone obtaining RTDB access or a dump; hiding the data from Fugaro itself is not a goal. Encrypting with the Firebase UID was rejected because the UID is not a secret.

## Corner cases

- **Lost or stolen phone.** The app offers "sign out everywhere", and workspace admins can revoke a single device's refresh token, so a stolen phone cannot start or kill jobs for the life of its refresh token.
- **Changed or reused email.** If an address is reassigned to a new person, they must not inherit old pairings. Workspaces key members on email plus the provider's stable user id, not email alone.
- **Risky actions by voice.** Killing a job, or starting one in a costly rigor mode, needs a spoken confirmation, and optionally Face ID or fingerprint, since anyone near the phone could speak the wake word.
- **Fugaro-Prod outage.** Existing sessions keep working on their refresh tokens; an outage only blocks new pairings and new-device restores.
