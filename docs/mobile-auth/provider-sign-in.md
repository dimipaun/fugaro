# The provider path, written out and refused

**Status: refused. Nothing below is built, planned or approved, and this document is not a part, a
task or a plan.**

This is **P6** of [implementation-plan.md](implementation-plan.md): **D221's alternative,
discharged as documentation and not built**. The decision it belongs to is
[reconciliation.md](reconciliation.md) §4's **D221** — *does a provider session reach the project's
database?* — whose recommendation is **no** and whose alternative is the owner's own spec of
2026-10-20, taken as written. **The owner answered D221 on 2026-10-22, taking the recommendation**,
and **D228** refused the email link in v1 by name. **No decision is taken here, and D221's answer is
not reopened by it.** Its purpose is the one `docs/pending-workflows/` has: the next person who
wants this should read a document, not re-derive a decision.

**Written 2026-11-13 against `main` at `637948b5`**, and every path and symbol below was re-read
there — twice, because the first reading found that several facts [reconciliation.md](reconciliation.md)
§3 states about the tree have moved since it read `d9dda8f3`. That document wrote this path out in
full first, and it is the authority for **why** the path was refused; **where the two disagree about
a fact of the tree, this document wins, because it was read later, and no decision, part or number
moves because of it.** Every citation is by **path and symbol, never by line number** — a line
number into a document that gains sections, or into a file that gains a function above the one it
means, moves under the sentence it was written for without anything looking wrong.

**What this document is not.** It is not a plan, an approval, a design or a spike, and it is not the
first half of one. It prices a path nobody has taken; the tree it describes is the tree as it is,
with the changes it would need **named and not made**. The plan's §3 states the same thing from the
other side: *"Any provider session, anywhere. No `signInWithCredential`, no enabled provider in any
project, no change to `refusePublicSignUp` in v1 (D221)."*

---

## 1. The path, in one paragraph

The spec's step 4 has the app exchange a **Google, Microsoft or Apple ID token** for a Firebase Auth
session **in each paired project**, with a passwordless email link as a third door
([spec.md](spec.md), §"Flow" and §"Per-project Firebase sign-in"). Enabling that means, in **every
project**: Identity Platform providers switched on, a per-platform OAuth client registered against
the project, the email link's action URL and its App Links/Universal Links artifacts published on
the project's hosting domain, and `refusePublicSignUp` narrowed to admit exactly those fields and
nothing else. What it buys is a **session the project's own security rules must then fence**,
obtainable by anyone who holds the web API key the pairing QR already carries. What it costs is §2
to §5 below, and the one property the tree's design exists to keep (§5).

Fugaro is **one Firebase project per installation** (`docs/design/project-centric.md`), so every
"once" below means **once per project, by its owner** — and there is no mechanism in this tree that
enables a provider in somebody's project for them: `fugaro setup` and `fugaro update` apply the
Terraform that creates the project, and `deploy/terraform/gcp/modules/firebase/firebase.tf` says of
Identity Platform that it *"is deliberately not a resource here"*.

---

## 2. The configuration delta, per project

| What | Where it lives today | What the path needs |
| --- | --- | --- |
| **Enabled providers** | `internal/infra/identityplatform.go`'s `PublicSignUp` **reads** `defaultSupportedIdpConfigs` and `oauthIdpConfigs`; `Initialize` writes neither, and `internal/cli/init_firebase.go`'s `refusePublicSignUp` refuses any of them that is enabled | an enabled `google.com` and `microsoft.com` in one of those two lists, and — for the email link — `signIn.email.enabled`, which is the **first** field `PublicSignUp` reports |
| **The OAuth client registration** | nothing configures it anywhere; the app's Firebase registration is the owner's, made in the console (below) | each platform's OAuth client registered against the project and admitted by it — the client IDs are **the owner's**, created in Google Cloud and in Entra ID, **outside this repository** |
| **Authorised domains** | `firebase.json` has no `auth` block; the Identity Platform configuration is read by `Config` and never written | the action URL's host, and every dynamic-link host, added to the project's authorised domains |
| **The email link's action URL and link artifacts** | nothing; the email-link flow is D228's refusal | a per-project action URL, plus `assetlinks.json` on the project's hosting domain for Android and `apple-app-site-association` for iOS, each keyed by the app's package/bundle identifier and, for Android, by the signing certificate's SHA-256 fingerprint |
| **The refusal itself** | `refusePublicSignUp`, called from `identityPlatform` (the read before the confirmation) and from `idpStep.initialize` (the read-back after `Initialize`) | a **narrower** refusal, by name, in the shape §2.5 writes out |
| **The rules' input** | `USER` is a minted `mu` claim **and** the device node; `TOKEN` is a minted `fp` and `fx` | a predicate over a **provider-signed** claim (`email`, `email_verified`) that no Fugaro component wrote |

### 2.1 The enabled providers

`internal/infra/identityplatform.go`'s `PublicSignUp` is the list the refusal is built on, and it
reports an enabled IdP as `defaultSupportedIdpConfigs <name> enabled` or
`oauthIdpConfigs <name> enabled`, where `<name>` is the API's own **resource path** —
`projects/<project>/<field>/<id>` — and not the bare id. That shape is why the two IdP fields are
compared by their **last path segment** wherever this tree already admits one by name
(`workspaceSignInPath`, §2.5), and a reader who compares them whole gets a match that never fires.

Enabling a provider is therefore **two writes**, one per list, and Firebase reports an IdP under
whichever list the project created it in. A narrowing that admitted `google.com` in one list and not
the other would refuse a correctly configured project — which is the failure mode
`refusePublicSignUp`'s own comment describes for the workspace's pin.

### 2.2 The OAuth client registration, per platform

This half is **outside the repository and outside Terraform**: the client IDs are created by the
owner in Google Cloud and in Entra ID, and the spec's own sentence — *"Each project must list the
Fugaro app's OAuth client ID as an allowed client, which project setup handles once"* — is asking
for an `init`/Terraform surface that does not exist. What **does** exist now, and did not when
[reconciliation.md](reconciliation.md) was written, is the app's side of it: the app carries Firebase
through three `@react-native-firebase` packages (`mobile/package.json`: `app`, `analytics`,
`crashlytics`), `mobile/app.config.js` registers their config plugins and points them at the two
native config files, and those files — `google-services.json` for Android and
`GoogleService-Info.plist` for iOS — are **the owner's**, written by the Firebase console, named in
`mobile/.gitignore` and delivered to a build as EAS **file** environment variables.

So the delta is precise, and it is not "register the app": the app is **already registered** in the
project the owner chose, and what a provider session adds is the **OAuth client** half — a client per
platform, its ID admitted by the project, and an **Auth package** the app does not carry today
(`mobile/package.json` lists no `@react-native-firebase/auth`). Enabling a provider without that
package changes the project and nothing the phone can do; adding the package is the act D234 and
D232 price.

### 2.3 The authorised domains

`firebase.json` carries hosting and headers and **no `auth` block**; `mobile/firebase.json` carries
the app's Analytics settings and no `auth` block either. The action URL's host and every
dynamic-link host are per-project console state, added on top of what Terraform applies — which
means the path puts a **hand-made step in every project**, immediately after `fugaro setup`, that
nothing in this tree checks for and no convergence repairs.

### 2.4 The email link, the action URL and the link artifacts

This is the row D228 refused by name, and the refusal is cheap for one reason: **the app commits no
identity**, and the artifacts need one. `mobile/app.json` carries no `ios.bundleIdentifier` and no
`android.package`; `mobile/app.config.js`'s `resolveConfig` builds both from `ID_VARIABLES` at build
time; and `mobile/src/app-config.test.js` asserts that no reverse-DNS identity is committed
(D125 — the app is its own artifact and the identity is the owner's).

So an email link's action URL and its App Links/Universal Links artifacts are **per-project public
artifacts keyed by an identity this repository deliberately does not hold**: `assetlinks.json` and
`apple-app-site-association` published on each project's hosting domain, each naming the app's
package/bundle identifier and, for Android, the signing certificate's fingerprint. The link also
carries the one requirement the spec states and nothing can engineer around — *"the link must be
opened on the same phone"* — and a second authorisation input (the address in the link) that has to
be reconciled with the device node the rules actually read.

### 2.5 The refusal, narrowed — and the one project where it is already narrowed

`refusePublicSignUp` (`internal/cli/init_firebase.go`) refuses an existing Identity Platform
configuration that enables **any** public sign-up path, and says why in its own message: *"The web
API key is public, so only custom tokens signed by Fugaro's signer may work."* The list it refuses
on is `PublicSignUp`, and the narrower rule would have to be written **entry by entry** — not
switched off, and not a flag:

| `PublicSignUp`'s entry | The provider path admits it? | Why |
| --- | --- | --- |
| `defaultSupportedIdpConfigs projects/<p>/…/google.com enabled` | **yes** | the spec's Google row |
| `defaultSupportedIdpConfigs projects/<p>/…/microsoft.com enabled` | **yes** | the spec's Microsoft row |
| `oauthIdpConfigs projects/<p>/…/google.com enabled` | **yes** | Firebase reports an IdP under either list, and a rule that admitted one and not the other refuses a correctly configured project |
| `oauthIdpConfigs projects/<p>/…/microsoft.com enabled` | **yes** | the same |
| `signIn.email.enabled` | **only if the email link is taken** — and D228 refuses it in v1 | the link's enablement *is* this field, which is also why the field is the first `PublicSignUp` reports |
| `signIn.anonymous.enabled` | **no** | an anonymous sign-in proves no address at all — the sentence `refusePublicSignUp` already writes for the workspace's pin |
| `signIn.phoneNumber.enabled` | **no** | a second identity path nobody decided on |
| `…/apple.com enabled` in either list | **no**, under D227 | Apple is identity-only; the spec's Apple row would need it admitted, and §3 is the per-provider question that row opens |
| any other IdP name, present or future | **no** | a name this tree did not decide on; a newer Firebase that adds one must not silently widen the project |
| `client.permissions.disabledUserSignup is false` | **no** | self-service account creation stays off. A Google sign-in still creates an account in the project — no rule can prevent that — but the *sign-up switch* is a different thing, and admitting it here would be admitting the workspace's own carve-out. |

**The precedent, and why it is not a licence.** This tree already carries a **narrower refusal**, and
it is the closest thing to the provider path that exists: the **workspace's own Firebase project** is
the one project where a person signs in with a real provider on purpose, and what is guarded there is
not "no provider" but **which** ones. `workspaceSignInPath` admits exactly `signIn.email.enabled`,
`google.com` under either IdP list (by last path segment), and the sign-up setting;
`docs/workspaces/spec.md` §3 is the pin, and `scripts/live-checks/check361.sh` is the live reading of
it. Two facts keep that from being the provider path in miniature, and both are load-bearing:

1. **The session it produces reaches one endpoint and no database.** A workspace token is a Firebase
   ID token of the workspace's project, and it is presented **at the workspace's signer endpoint
   alone**: no member's Firebase accepts it at all (`docs/workspaces/spec.md` §3, D1293), and the
   members' own rules are untouched. The provider path's session is *the* session the project's own
   database admits. That is the whole difference, and it is the reason the workspace carve-out costs
   `refusePublicSignUp` a pin while the provider path costs it a rewrite.
2. **The pin is a closed set, and the provider path's set is not.** `workspaceSignInPath` names its
   entries; the provider path would have to name its own per project, and every later provider (or
   every provider the owner enables in the console by hand) is a change to a security check that
   lives in **this repository** while the configuration lives in **the owner's console**. **The two
   sets are also not one a subtraction reaches**: against the workspace's three admissions, the
   table above adds `microsoft.com`, refuses the sign-up setting the workspace admits, and takes the
   email link only if D228 is reopened (§6.4).

The narrower refusal is therefore a **security surface of its own**, reviewed as one: what it admits
is a project-wide door, and the two entries that would do the damage — `signIn.anonymous.enabled` and
an IdP name it does not know — are exactly the ones a careless narrowing drops.

---

## 3. The attack surface

- **The QR becomes the front door.** `internal/cli/init_firebase.go` records `FirebaseProject`,
  `RTDBURL` and `FirebaseAPIKey` from the Terraform outputs, and the pairing QR carries that
  configuration (D165; `internal/cli/pair.go` is the command that displays it). The web API key is
  **public by design** — that is `PublicSignUp`'s own comment and the refusal's own sentence — and
  today what it buys is a `signInWithCustomToken` that only Fugaro's signer can satisfy
  (`internal/budget/token/mint.go`'s `Mint`, exchanged by `internal/budget/token/exchange.go`'s
  `Exchange` against `accounts:signInWithCustomToken`). After the change it buys a **session**. A QR
  on a screen, a screenshot, a photograph of a laptop in a café: each is a pair of credentials that
  anyone with a Google or Microsoft account can turn into a session in that project.
- **The rules become the only fence, and they must deny everything not on the allowlist.** The tree
  is already default-deny — `internal/budget/rules/rules.json.tmpl` opens `".read": false,
  ".write": false`, and every grant is a macro named in `internal/budget/rules/rules.go` (`TOKEN`,
  `USER`, `RUN`, `PAIRING`, `MAILBOX`) — so a session the rules admit nowhere reaches nothing. That
  is the one part of the path this tree already guarantees, and it is **the part a reviewer must be
  shown rather than told**: an unauthenticated read, a session with no matching entry, and a session
  whose `email_verified` is false must each be **denied, per family, in the emulator** (§6).
- **One credential is material for every project at once.** Under D114's model the app's accounts
  are separate installations with non-overlapping entries — but a provider credential is **one per
  provider on the device**, so a leak of it is a leak of the material for every project the phone has
  paired. Today the material is a per-pairing device token and a per-pairing MAC key, held as exactly
  two secure-store items (`mobile/src/accounts/secret.js`'s `SECRET_ITEMS`, D111, D232), and a
  revocation deletes one node rather than waiting on a token to expire.
- **The email link is a second door and needs an identity the repository does not commit** (§2.4),
  plus a second authorisation input that has to be reconciled with the node the rules read.
- **What would not change, stated so the list is not read as larger than it is.** No Fugaro component
  gains a credential; no run's identity moves; the budget caps and the `TOKEN` family are untouched;
  `config/admin` stays client-denied (`config/admin/devices/$uid` carries `.read: false,
  .write: false` in `internal/budget/rules/rules.json.tmpl`, and writing it is an IAM act behind
  `roles/firebasedatabase.admin`).
- **What the app already holds, said plainly, because the honest version of this section is not
  "the app has no Firebase".** The app **does** carry Firebase packages and does read the project's
  database directly under the user-claim read grant (D112). The fence the provider path would
  cross is narrower than "the app has no Firebase": it is that **no provider is enabled in any
  project** and **no Auth package is in the app**, and both of those are the second half of D221.

---

## 4. The verified email, per provider

The spec asks for *"the allowlist checks the verified email"*, and the path's rules would have to
read a **provider-signed** claim — the first predicate in this database whose input no Fugaro
component wrote. **"The verified email" is one property in the spec's provider table and three in
reality**, and the difference is not a detail:

- **Google.** An ID token for a verified address carries `email_verified`. This is the row the one
  verified-email check in the tree covers — and it covers it **elsewhere**:
  `internal/cli/init_members.go`'s `userEmail`, called by `authenticatedUser`, refuses
  `case !u.Verified: return "", fmt.Errorf("the email %s is not verified", …)` over a **Google
  userinfo** response read by the CLI with the operator's own token. That is not an ID token's claim
  and not the app's session, so the check the tree has is a **precedent for the shape**, not an
  implementation of it.
- **Microsoft (Entra ID).** The identity is a **directory** identity: the token may carry no `email`
  at all, may carry a `preferred_username` that is not an address, or may carry an address the tenant
  never verified for that person. What an Entra ID token actually answers is **U2** of
  [reconciliation.md](reconciliation.md) §5 — unverified, and unverifiable from here.
- **Apple.** The address may be a **private relay** address (`@privaterelay.appleid.com`) that is per
  app and per Apple ID, is not the person's address, and changes when the relay is switched off
  (F16, D227). An allowlist of exact addresses cannot match a relay address by construction, and a
  **domain** entry for the relay domain would admit every Apple user who hides their address — which
  is why the relay is not a detail of the Apple row but the reason Apple's identity cannot be the
  allowlist key.

The predicate would therefore be true of some people and silently false of others, **in the one place
where a false negative locks a person out and a false positive lets one in** — and the failure is
invisible from this repository, because it happens in a token the owner's project minted.

---

## 5. Revocation, and the node the rules would keep reading

**The property the tree chose, in its own words.** `internal/budget/rules/rules.go`'s `PathDevices`
comment says the device node *"is deliberately not a claim. A claim is minted by whoever signs the
custom token and cannot be taken back before the ID token expires (G14), while deleting this node
revokes at once."* The emulator proves that half today:
`internal/budget/rules/emulator_pairing_test.go`'s `TestDeviceNodeGatesEverythingUserReaches` walks
one account through **never written, written, deleted** and requires every family the `USER` macro
reaches to be refused in the first and last states.

**What the path costs.** With a provider-signed claim as the authorisation input, a removal revokes
**when the ID token expires** — up to an hour — and the spec's own sentence (*"removing someone from
the allowlist takes effect immediately through the security rules"*) stops being true of the system
it describes. The only way to keep it true is for the rules to **also** consult a node an operator can
delete — which is the tree's design arriving back through the side door, on top of the provider
plumbing, with two records of the same fact able to disagree. That is the state D166's *"delete the
node and it is over"* was written to avoid, and D222 refuses a second record for the same reason.

So the honest shape of the path is: **a provider session whose authorisation is still the node** —
which is D221's recommendation with a sign-in bolted in front of it, and not the spec's flow.

### 5.1 The allowlist predicate this would need, word for word

The reconciliation wrote this mapping out at
[reconciliation.md](reconciliation.md) §3.3, and it is what the session above would be checked
against. It is quoted here rather than re-derived, because it is the half of §3 that decides whether
the path is usable at all — every row that is *not* enforceable in the rules is a row that has to
move out of them:

| The spec's rule | On the device node | Enforceable in the rules? |
| --- | --- | --- |
| An exact address is allowed | the node at `config/admin/devices/<uid>` exists, and its `email` field equals the token's address | **Yes** — `auth.uid` is path-safe and the email is compared as a **value** (F4) |
| A whole domain is allowed | a domain entry resolved **when the entry is written**: the person's phone is paired, and the node carries the address | **Only as a literal pattern** over `auth.token.email`; a pattern read from data is **U1**, unverified — and D223 puts the rule at administration instead |
| An exact address overrides its domain's role | the node carries the **resolved** role, so there is nothing to resolve at read time | **Yes** — one field of one node, which is immediate (F13) |
| Roles: viewer / operator / admin | a `role` field on the node, written day one, read by the CLI and the console; no rule reads it in v1 | **Yes, later** — `child('role').val()` is expressible and immediate; v1 grants every paired device the same `USER` and splits nothing (F12) |
| Removal takes effect immediately | deleting the node | **Yes** — and this is the property the provider path's own session loses (§5) |

Two things the table settles that a reader of §1 alone would miss. The comparison the rules can
make is **between two values** — the token's address and a field of a node keyed by the path-safe
`auth.uid` — so a provider session changes what the token carries and **not** what the rules
compare; and the domain row is the one row that cannot be made enforceable at read time, which is
D223's answer and not a gap in it.

---

## 6. What would have to be proved before a line is written

Each of these is a **proof**, not a reading, and each is named with the vehicle that would carry it.
None of them exists, and none is proposed here as work to do:

1. **The default-deny, per family, over a provider-shaped token.** `internal/budget/rules/emulator_test.go`'s
   `TestDenialTable` already denies *"a user without claims"* at every path it walks — the token is
   built by `internal/budget/rules/harness_test.go`'s `unsignedToken` — and that is the right entry
   point for a **provider-shaped** account: a token carrying `email` and `email_verified` and none of
   `fp`, `fs`, `fr`, `fx`, `rb`, `mu`, `mc` or `pu`. It must be denied on **every** family the `USER`
   macro reaches, and the run has to be one that cannot pass vacuously: `go test -tags firebase
   ./internal/budget/rules/...` with `FUGARO_REQUIRE_EMULATOR=1`, which turns a missing emulator into
   a failure rather than a skip (the CI rules job sets it). **What the emulator cannot prove** is
   anything about a real provider: it drives **unsigned synthetic** tokens, so it proves the rules'
   half, and nothing about what Firebase issues.
2. **That the claims exist at all, per provider.** Whether `auth.token.email` and
   `auth.token.email_verified` are exposed for a provider-signed session, and whether a pattern read
   **from data** is accepted by the rules language, is **U1** — the tree proves only the shape
   (`auth.token.mu` and `fp` are read, and `matches()` is used with **literals**). U2 (Entra ID's
   claim) and U3 (Apple's relay) are §4's, and no emulator can settle them; a real project, and one
   owner-run sign-in per provider, is the only vehicle.
3. **That no rule reads the claim by accident.** No rule in
   `internal/budget/rules/rules.go` reads `email` or `email_verified` today, and the first that does
   is the first predicate over an input Fugaro did not write (F7). A provider-shaped token must be
   denied **before** that predicate exists and admitted for exactly one address after — and the
   second half needs an administered email field that the token can be compared against, which is
   F4's constraint: an RTDB key may not contain a `.`, so the comparison has to be on a value of a
   node keyed by the path-safe `auth.uid`.
4. **That the refusal's narrowing cannot be walked around.** Per §2.5, the entry-by-entry table is
   the specification, and the proof is a **test in `internal/cli`** over `PublicSignUp`'s output: a
   configuration enabling `google.com` and `microsoft.com` is admitted; one enabling
   `signIn.anonymous.enabled`, or `apple.com`, or an IdP name the table does not carry, is refused
   **by name**. The workspace's `workspaceSignInPath` and its tests are the shape to copy, and **the
   difference from that function is not one field — it runs both ways**, which is why the table and
   not the function is the specification. Against `workspaceSignInPath`'s three admissions
   (`signIn.email.enabled`, the sign-up setting, and `google.com` under either IdP list), the
   provider path **adds** `microsoft.com`, **drops** the sign-up setting
   (`client.permissions.disabledUserSignup is false` is refused, §2.5), and admits
   `signIn.email.enabled` — which the workspace pin admits outright — **only if D228's refusal is
   reopened**. So a narrowing derived from the workspace's function by subtraction is wrong in both
   directions at once: it refuses every correctly configured Microsoft project, and it admits the
   email link this folder refused by name.
5. **That the per-project console steps are performed and stay performed.** Authorised domains, the
   OAuth client registration and the action URL are hand-made, per project, in the console. Nothing
   in this repository reads them today; a check that reads them back is the precondition, in the
   shape `scripts/live-checks/check361.sh` gives the workspace's pin.
6. **A live check that a provider session reaches nothing**, on a real project, with a real
   provider-signed token. The plan already proposes by name the check whose meaning this path
   reverses — *"`fugaro init` still refuses a provider"* (P6, owner-run,
   [implementation-plan.md](implementation-plan.md) §9) — and a path that admits providers needs the
   opposite check written beside its replacement: sign in with a real Google account, then attempt,
   from that session, **every family the `USER` macro reaches**, and read each refusal.
7. **The threat model read as one, by somebody who did not write it.** The plan's P6 reviewer tier is
   **Careful** *because this document changes no behaviour*; a path that changed behaviour would be
   **SECURITY-SENSITIVE**, and its review would have to cover what §2.5's table admits, what §5 keeps
   reading, and what §6.6 measures.

---

## 7. What taking the path would change in this tree's own decisions

Named so that a reader sees the blast radius is **decisions, not files**:

- **D221 itself is re-taken**, and it is not alone. The plan's §11 records the set: *"P4, P6 and
  D232–D234 all move with it, because the app's boundary, its dependency list and its
  secure-store item set are the same question."* **D232** (two secure-store items stay two),
  **D233** (the QR carries no identity) and **D234** (no per-project Firebase app instance) are the
  app's half of the same answer; **D228** (the email link) and **D227** (Apple is identity-only) are
  the provider rows'.
- **A rules generation.** The first rule that **reads** a provider claim opens a family to a session
  the deployed document does not admit under, and that is what `internal/projectpolicy/version.go`'s
  `RulesVersion` (currently **4**) exists to signal: a generation is a deploy the owner has to run on
  **every installation**, and [reconciliation.md](reconciliation.md) §F29 and the plan's §3 both make
  "a generation" a thing a part stops and asks about rather than quietly adds. The generated document
  is pinned by `internal/budget/rules/testdata/rules.golden.json`, and the denial table by the
  emulator tests beside it — both would move.
- **The app's boundary tests, deliberately.** `mobile/src/boundary.test.js` and
  `mobile/src/accounts/secret.js` exist so that a third credential item is a **deliberate act**: the
  list is closed, `secure-store.js` refuses an item name that is not on it, and a provider credential
  would have to be added to that list in a change that says so.
- **The number this work holds.** The path adds no check number here, and no decision number: it
  describes an alternative to a decision that is answered, and the register's rule is that the phase
  that ships a check writes it.

---

## 8. What is not verified here

Everything [reconciliation.md](reconciliation.md) §5 lists — **U1** (whether `auth.token.email` and
`auth.token.email_verified` are exposed, and whether `matches()` takes a pattern read from data),
**U2** (Entra ID's claim), **U3** (Apple's relay), **U4** (App Store Guideline 4.8's current text),
**U5** (the Expo SDK 58 library options), **U6** (the default-deny proof, live), **U7** (the app's
test runner here) and **U8** (where the D6 answer is recorded) — plus two this reading adds:

- **No Node and no `mobile/node_modules` in this container.** Every claim above about `mobile/` was
  **read, not executed**: `node --version` finds nothing, and not one of the app's tests was run
  while writing this. The app's boundary, its dependency list and its native-config routing are
  citations.
- **No network, and the vendor pages are unread.** The per-platform OAuth registration, the console
  steps and the App Store rule are cited as things a person does in a console, from the spec's own
  words and the tree's own code — not from a page read on the day.
