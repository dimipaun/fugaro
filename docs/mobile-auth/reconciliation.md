# The mobile app's sign-in, pairing and allowlist, reconciled against the code

**Status: plan only; no code is implemented; every decision in it is answered.** The owner answered
**D221–D236 as recommended on 2026-10-22**, and approved the plan's **P0 and P1** — the two parts
that wait on nothing built; **P3, P4 and P5 wait on the mobile Phase 6 tasks** this work extends,
which [implementation-plan.md](implementation-plan.md) §11 records. This document reads
the owner's [spec.md](spec.md) ("Fugaro Mobile App Auth Spec", 2026-10-20) against the tree at
`main` `d9dda8f3` (`cli: fugaro --version and fugaro -v print the version, like fugaro version
(#794)`) on **2026-10-21**, and against the neighbouring documents it names. Every path and symbol
below was re-read at that commit and is cited **by path and symbol, never by line number** — the
documents this reconciliation reads (`docs/mobile/`, `docs/central-orchestrator/`) gain sections
and rows while this work sits in review, and a line number into one of them moves under the
sentence it was written for without anything looking wrong. **Where this document disagrees with
[spec.md](spec.md), this document wins**; the plan is written to this document, and the
reconciliation is written to the decisions in §4.

**Owner: Dimi Paun. Date: 2026-10-21.** The spec is the owner's own text of 2026-10-20, copied
byte for byte into [spec.md](spec.md); it needed no typo fix (see [README.md](README.md)).

**Revised 2026-10-21, twice, and the revisions are visible rather than folded in.** A second reading
of the rules found that the device node's new fields **need no rules generation** (§F29, which is
written at the end of §2 for that reason), and it corrected **F27**'s count of the pins that read
`docs/mobile/`. The glossary's rules-generation entry, D235 and D236 read accordingly, and
[implementation-plan.md](implementation-plan.md)'s **P2** is written against §F29 rather than against
a generation bump. **A review then found §0.4 still carrying the old counts** and two claims of its
own that the tree contradicts — `docs/central-orchestrator/` *is* named by a `_test.go`
(`internal/cli/docs_coordinator_test.go`, which names it as a string the coordinator's README must
carry, not as a document it reads) — so §0.4 now says what is true and cites the same eight pins as
§F27. The review's findings against the plan (§9's reading of check 110 and of 112, P4's reviewer
tier against the plan's own counts, P0's citation rule) are fixed in place there, and none of them
changes a finding, a decision or a part. **No decision number moved**: D221–D236 are the same sixteen
questions, and the corrections are about what one part of the plan costs — a deploy the owner would
otherwise have had to run on every installation — and about what the plan's citations actually say.

**One thing this document is not.** It is not a second opinion about the mobile work. The app's
pairing, its device node and its allowlist are **already answered by the owner** — D165, D166 and
the whole of `docs/mobile/`'s D110–D129, plus the mobile functional spec's D160–D173 — and the
**owner's answer of 2026-10-20** to `docs/central-orchestrator/`'s D6 defines what an allowlist
entry *is*. This spec arrives after all of that and **reverses the part of it that says how a
phone signs in**. So the reconciliation's job is narrow and load-bearing: to say exactly which
recorded rulings the spec's flow would replace, what replacing them would change in every
project's Firebase configuration, and what the rules would then have to prove — and then to put
the choice in front of the owner as numbered decisions rather than resolving it in prose.

---

## 0. Corrections to the brief this reconciliation was written from

Six premises of the task this document was written under do not match the tree. Each is stated
here so that a reader does not look for something that is not there, and each is corrected rather
than quietly worked around:

1. **"the owner's D6 answer" is not in `docs/central-orchestrator/`.** That folder's D6 —
   `docs/central-orchestrator/reconciliation.md` §5's *"### D6 — The allowlist, the sign-in
   methods, and the domain restriction"* — carries a **recommendation and a cost, and nothing
   else**: its README says *"No decision has been answered, no part of the plan has been
   accepted"*, its §5 says *"All twenty-six are open"* and its §6 says *"Nothing below is approved
   and nothing is answered"*. The owner's answer of **2026-10-20** is recorded **in the mobile
   folder**: `docs/mobile/functional-spec-reconciliation.md` **§3.7** (*"the owner's own word,
   relayed to the change that wrote this section, and no document in this repository carries it"*)
   and `docs/mobile/functional-spec-v0.1.md`'s §4.2 note (edit 7). **This reconciliation cites the
   answer at §3.7, not at D6**, and D6 is cited for the recommendation it holds and for the two
   things the answer took away from it.
2. **"custom tokens minted by the security service" is not what the tree does today.** The
   credentials service is **plan only** — `docs/credentials-service/README.md`'s *"Status: plan
   only; nothing is implemented"* — and the custom token the tree mints is the **run's**, minted by
   the **launcher**: `internal/budget/token`'s `Mint`, called from `internal/cli/launch_budget.go`'s
   `cloudEnv.mintBudgetToken`, exchanged by `Exchange` against
   `accounts:signInWithCustomToken`. `docs/credentials-service/reconciliation.md`'s D134 keeps that
   mechanism and has the service **verify** it, not mint it. So "the security service mints custom
   tokens" is a **plan**, and the sentence is a statement about 0.11.0-or-later rather than about
   today.
3. **`fugaro allow …` does not exist, and the word `allow` is already taken twice.** There is no
   `allow`, `pair` or `devices` command: `internal/cli/root.go`'s `root.AddCommand` registers
   neither. What exists is `internal/cli/allow_delete.go`'s `newAllowDeleteCmd` —
   `fugaro allow-delete [ADDRESS...]`, the **managed projects** gate over Terraform addresses
   (D72, D74) — and the **repository** allow-list, which is what this tree calls an allow-list
   today: `internal/cli/project_cmd.go`'s `newProjectCmd` describes itself as *"The project's
   policy, its allow-list and the repositories enrolled"* and the field is
   `projectpolicy.Policy.AllowRepos`. `fugaro pair` is **planned and unbuilt**
   (`docs/mobile/implementation-plan.md` Task P6.6, approved 2026-10-18), as are its files
   `internal/cli/pair.go` and `internal/budget/token/device.go`.
4. **`docs/central-orchestrator/` has no test of its own.** **No test reads a file under it** —
   `internal/cli/docs_coordinator_test.go` is the only `_test.go` that names the folder at all, and
   what it names is the **string** `docs/central-orchestrator/README.md` as something the
   *coordinator's* README has to carry, not a document it opens — which is why §0.1's provenance
   rests on the documents' own words. `docs/mobile/` **is** pinned: **eight** tests in
   `internal/cli`'s `docs_*_test.go` family name it, **four of them named for it**
   (`docs_mobile_plan_test.go`, `docs_mobile_checks_review_test.go`, `docs_mobile_push_test.go`,
   `docs_mobile_mailbox_plan_test.go`) and four quoting it beside their own folder's claims. The
   numbers register is pinned by `internal/cli/docs_coordinator_test.go` and
   `internal/cli/docs_voice_switch_test.go`. **`docs/mobile-auth/` had no pin at this reading**, and
   P0 of the plan is the part that gave it one — `internal/cli/docs_mobile_auth_test.go`, landed
   2026-10-22. (This paragraph and §F27 are the same count; the earlier
   revision of both got the number wrong.)
5. **The provider guidance cannot be read from here.** Expo SDK 58's options for Google (Credential
   Manager versus the Google Sign-In SDK), for Microsoft (browser OAuth) and for Apple
   (`expo-apple-authentication` or Sign in with Apple JS) are **platform facts this container
   cannot verify** — there is no network, `mobile/node_modules` is absent and no Node is installed
   (`node --version` finds nothing). What the tree does say is in `mobile/package.json`: **Expo
   `^58.0.16`, and no Firebase, Google, Apple or Microsoft auth library in the dependency list at
   all.** Every provider claim in this reconciliation is therefore either about the tree or in §5's
   UNVERIFIED table.
6. **The Apple rule the spec cites is cited, not verified.** *"App Store rules generally require it
   when Google or Microsoft login is offered"* is App Store Review Guideline 4.8 ("Login
   Services"), which this repository does not quote anywhere and this container cannot read. It is
   treated as the owner's statement about a rule rather than as a fact established here (§5).

---

## 1. The spec's concepts, and what the repository has

| The spec's word | What the repository has | Verdict |
| --- | --- | --- |
| **Sign-in at first launch, "an account already on the phone"** | The app's first screen is `mobile/src/app/first-screen.js`, which carries the demo entry (*"Open the app and tap Try the demo on the first screen. It runs on built-in sample data and needs no account, sign-in or network."*, `docs/mobile/play-review-access.md`) and the seam `docs/mobile/implementation-plan.md`'s Task P6.8 puts pairing into. There is no sign-in anywhere in `mobile/` | **Gap, and a promise to keep**: the demo's own test (`mobile/src/demo/safety.test.js`) holds that sentence, so a sign-in gate in front of the first screen is a change to what the app promises Play's reviewers |
| **"The provider credential … on the device"** | `mobile/src/accounts/secret.js`'s `SECRET_ITEMS` is `['idToken','macKey']` — **two** items (D111) — behind `mobile/src/accounts/secure-store.js`'s `SecretStore`, with `mobile/src/boundary.test.js` counting the files allowed to write a credential | **Wrong as written**: a provider credential is a third item in a set that is deliberately closed |
| **"a project QR code … Firebase config and pairing info only"** | `fugaro init` records `FirebaseProject`, `RTDBURL` and `FirebaseAPIKey` (`internal/cli/init_firebase.go`), and D165's QR carries that plus a **one-shot pairing token** for a `p~<random>` uid (`docs/mobile/functional-spec-reconciliation.md`, D165; the `PAIRING` macro and `pairingClaim` in `internal/budget/rules/rules.go`) | **Right and incomplete**: the spec omits the token, which is what makes pairing work without a sign-in |
| **"exchanges the stored credential for a Firebase Auth session in that project"** | `internal/cli/init_firebase.go`'s `refusePublicSignUp` refuses a project whose Identity Platform config enables any public sign-up path, with the message *"only custom tokens signed by Fugaro's signer may work"*; `internal/infra/identityplatform.go`'s `Initialize` initialises Identity Platform *"with no sign-in provider"* and `PublicSignUp` lists `signIn.email.enabled`, `signIn.anonymous.enabled`, `signIn.phoneNumber.enabled`, `defaultSupportedIdpConfigs … enabled`, `oauthIdpConfigs … enabled` and `client.permissions.disabledUserSignup is false` | **Wrong**: this is the reversal, and it is the one thing `fugaro init` is built to refuse |
| **"the allowlist checks the verified email"** | The allowlist is the **device node**: `internal/budget/rules/rules.go`'s `PathDevices` (`config/admin/devices`), required by `userClaim` (the `USER` macro), unreadable and unwritable to the client that holds it (`rules.json.tmpl`: `.read: false, .write: false`), and deleting it revokes *"at once"*. The owner's answer of 2026-10-20 adds an **email the administrator writes** as a field on that entry and refuses an **unverified** email **at pairing** | **Wrong as written, answered otherwise**: the record is keyed by `uid`, not by email |
| **"Roles … from day one"** | Nothing in the tree reads a role. Admin rights over the node are **IAM** (`roles/firebasedatabase.admin`; `docs/mobile/functional-spec-reconciliation.md` G13), not data | **Gap**: a role field has a home and no reader |
| **"`fugaro allow add <email\|domain> --role operator`"** | `fugaro allow-delete` (Terraform addresses) and `allow_repos` (repositories). `fugaro pair` is planned, unbuilt | **Wrong name, right need**: see §0.3 |
| **"removing someone from the allowlist takes effect immediately through the security rules"** | True, and the tree says why: `PathDevices`'s own comment — *"It is deliberately not a claim. A claim is minted by whoever signs the custom token and cannot be taken back before the ID token expires (G14), while deleting this node revokes at once"* | **Right, and it is the tree's answer already** — but it is true only while the decision rests on the node and not on a claim or a provider token |
| **"Allowlist changes are saved to the project Git repo"** | The tree's model is **publish from config**: `internal/cli/project_cmd.go`'s `newProjectPolicyPublishCmd` (*"Publish the project's policy from the operator's config (budget admins)"*) writes `config/policy/shared` with an ETag-guarded write, seeded from the checkout's `fugaro.yaml` and the machine's config, and `publishAllowRepos`/`seedAllowRepos` implement *"a later publish never re-adds a name an operator removed"* | **Right in spirit, and the mechanism already exists** — for `allow_repos`, not for people |
| **"one Firebase app instance per paired project"** | `mobile/package.json` has **no Firebase SDK**; the app's reader is a pure port (`mobile/src/feed/reader.js`: *"The phone reads nodes, not a child's stdout. D112's answer is direct RTDB reads"*), and the account model is D114's: an account **is** an installation, several stored as non-overlapping entries, *"the switcher tears down the previous account's listeners and caches before it opens the next"* | **Gap**, and the model it would extend is already answered |
| **"the security service mints custom tokens so one email login covers all projects"** | Plan only (§0.2) | **Later**, by the spec's own word |

---

## 2. Findings

Each finding cites a path and a symbol and states its consequence. The classification is the one
the neighbouring reconciliations use: **Wrong** (the spec asserts something the tree contradicts),
**Exists** (the tree already has it), **Gap** (the tree has nothing), **Rule** (a standing ruling
the spec would change) and **Open** (this reconciliation cannot settle it).

### Identity and sign-in

**F1 — Wrong. "The app exchanges the stored credential for a Firebase Auth session in that
project" reverses a standing platform rule.** `internal/cli/init_firebase.go`'s
`refusePublicSignUp` refuses a project whose Identity Platform configuration enables a public
sign-up path, and says why in its own message: *"The web API key is public, so only custom tokens
signed by Fugaro's signer may work"*. `internal/infra/identityplatform.go`'s `PublicSignUp` is the
list it refuses on, and `Initialize` initialises the platform *"with no sign-in provider"*.
`docs/mobile/functional-spec-reconciliation.md` §3.7 records the owner's answer of 2026-10-20 as
*"the sign-in rule stays custom tokens only"*. **Consequence:** the spec's step 4 is not a feature
to add; it is the removal of a refusal, in every project, and `fugaro init` is the program that
would have to stop refusing.

**F2 — Wrong. The allowlist is a record keyed by `uid`, and an email is not a key.** `PathDevices`
is `config/admin/devices`, `userClaim` requires
`root.child('config/admin/devices').child(auth.uid).exists()`, and the entry is written over IAM.
**Consequence:** an email-keyed allowlist is a second record with no writer and no reader, and it
does not replace the node — the node is what `USER` requires.

**F3 — Rule. The pairing flow is CLI-only and it is discovery, not identity.** T3 (CLI only) and
the owner's answer to D165 are recorded in `docs/mobile/functional-spec-reconciliation.md` §3.7
(edit 2, the sentence moved to §4.2 as an authorisation statement), and
`docs/mobile/functional-spec-v0.1.md`'s §4.1 sentence *"Only someone who already has access to the
project (CLI or console) can generate a pairing code"* is kept word for word there. **Consequence:** "sign-in comes first, pairing comes
second" puts the phone's own act before the admin act the tree's authorisation model starts from —
and a sign-in is not an authorisation, so the order is a UX change rather than a security one
*provided* nothing is admitted on the strength of the sign-in alone.

**F4 — Gap, and it is precise. An exact email cannot be looked up in the rules; a domain can be
matched.** An RTDB key may not contain `.`, and the rules language has no function that rewrites a
string (`internal/budget/rules/rules.go` uses `String.matches` and `String.beginsWith` and no
other string operation), so `root.child('config/admin/allow').child(auth.token.email)` cannot
resolve an address, and rules cannot iterate a node's children. What **is** expressible is a
comparison against a value on a node **keyed by the path-safe `auth.uid`** — the administered email
field D6's answer adds is exactly such a value — and, for a domain, a `matches()` test against a
literal pattern. **Consequence:** the spec's *"exact address or domain"* splits: the exact-address
half has a home on the device node, and the domain half is a predicate over `auth.token.email`
whose *dynamic* form (a pattern read from data) is UNVERIFIED (§5).

**F5 — Rule. Revocation is immediate only while the decision rests on the node.** `PathDevices`'s
comment states the whole argument: a claim *"cannot be taken back before the ID token expires
(G14)"* while deleting the node *"revokes at once: the next read or write of every family USER
reaches fails its existence test"*, and *"Deleting an Auth user does not revoke on its own — an
issued ID token keeps passing until it expires — which is the whole reason D166 chose a node."*
**Consequence:** the spec's own claim (*"removing someone from the allowlist takes effect
immediately through the security rules"*) is **true of the tree's design and would become false**
of the spec's, in which the authorisation input is a provider-signed claim that outlives a removal
by up to an hour.

**F6 — Wrong. "The provider credential … refreshable" is a credential the app is designed not to
hold.** Voice D42 and D54b (`docs/design/voice.md`) are the "the app holds no credential" ruling,
and `mobile/src/accounts/index.js` states the same boundary for the app's own store: *"the files
that may write a credential are two, they are named there, and a third one has to be added to that
list deliberately"*. D112's direct reads were granted under a **user-claim read grant** with no new
credential class (`docs/mobile/reconciliation.md`, tension (b1)). **Consequence:** a provider
refresh token per paired project is a new credential class on the device, and the app's boundary
tests are where that decision would have to be recorded.

**F7 — Gap. Nothing in the tree reads `auth.token.email`.** The rules read `fp`, `fs`, `fr`, `fx`,
`rb`, `mu`, `mc` and `pu` (`internal/budget/rules/rules.go`), all of them minted by Fugaro or by a
launcher. **Consequence:** the spec's allowlist check would be the first predicate in this database
that trusts a **provider-signed** claim, and it would be the first whose input no Fugaro component
wrote.

**F8 — Open. Whether `email_verified` is present and true is a per-provider question, and the tree
has only one verified-email check.** `internal/cli/init_members.go`'s `authenticatedUser` calls
`userEmail`, which refuses `case !u.Verified: return "", fmt.Errorf("the email %s is not
verified", …)` — and it refuses it for a **Google userinfo** response, read by the CLI with the
operator's own token, not for a Firebase ID token's claim. **Consequence:** the spec's *"verified
email"* means, in the tree's vocabulary, "the CLI checked it at pairing", and the per-provider
claim question is F16's and §3's.

**F9 — Rule. The web API key is public by design, and the QR carries it.** `PublicSignUp`'s own
comment and the refusal's message say so, and the app receives `FirebaseAPIKey` from the QR
(`docs/mobile/functional-spec-reconciliation.md` G18). **Consequence:** with providers enabled, the
QR stops being a pairing invitation and becomes **the project's front door**: anyone who can read
a QR can obtain a session, and the only thing left between that session and the data is the rules'
default-deny.

**F10 — Exists. The rules are already default-deny at the root, and every grant is a macro.**
`internal/budget/rules/rules.json.tmpl` opens with `".read": false, ".write": false`, and
`internal/budget/rules/rules.go` expands `TOKEN`, `USER`, `RUN`, `PAIRING` and `MAILBOX` from named
predicates. **Consequence:** a provider session that the rules admit nowhere reaches nothing —
this is the one part of the spec's path that the tree already guarantees, and the part a reviewer
must be shown rather than told.

### The allowlist, the roles and the record

**F11 — Gap. "The project creator becomes the first admin" has no mechanism.** No code writes a
device node (`internal/cli/pair.go` does not exist), no Auth `uid` exists before a person signs in,
and `fugaro init` writes none. **Consequence:** the sentence needs a writer, a moment and an
identity; §4's D230 recommends which.

**F12 — Gap. The role has no reader.** Nothing in `internal/budget/rules/rules.go` or the template
reads a role, and admin authority over the node is IAM. **Consequence:** "roles are in the data
model from day one" is satisfiable by a field and a resolver, and "v1 only checks admin" is then a
statement about the CLI and the console, not about the rules.

**F13 — Open, and interesting. "An exact email overrides its domain's role" is a resolution rule,
and the rules cannot resolve.** A predicate can test one field of one node
(`root.child('config/admin/devices').child(auth.uid).child('role').val() == 'admin'` is
expressible, and immediate), but "prefer the exact entry, else the domain entry" is an ordering
over a set — and a set cannot be read by a rule. **Consequence:** the resolution belongs where the
entry is written (§4's D224), and the node stores the answer.

**F14 — Rule. Writing or deleting the node is an admin act, over IAM.** G13
(`docs/mobile/functional-spec-reconciliation.md`) records it: the node needs
`roles/firebasedatabase.admin`, and the template denies every client `config/admin` (`.read: false,
.write: false`). **Consequence:** "Admin: can manage the allowlist" is IAM in v1, and a role field
that says `admin` grants nothing by itself.

**F15 — Gap. There is no record of allowlist changes anywhere.** `config/admin/devices` has no
history, no `at`, no `by`; the tree's audit-shaped nodes are elsewhere (the override log is the
coordinator's, `docs/coordinator/reconciliation.md` D197). **Consequence:** the spec's *"saved to
the project Git repo"* is asking for a record that does not exist in either place, and §4's D231
recommends which place gets it.

**F16 — Gap. The Apple private-relay address.** Nothing in `docs/` mentions
`@privaterelay.appleid.com`, and `mobile/` has no Apple sign-in at all. **Consequence:** an
allowlist of exact addresses cannot match a relay address by construction, and a **domain** entry
for the relay domain would admit every Apple user who hides their address — so the relay is not a
detail of the Apple row, it is the reason Apple's identity cannot be the allowlist key (§4's D227).

**F17 — Open. The spec's *"later projects repeat steps 3 to 5 with no new sign-in"* is already
true of the tree's model, for a different reason.** D114 (`docs/mobile/reconciliation.md`) answers
that an account **is** an installation and that several are stored as non-overlapping entries, with
the switcher tearing down the previous account's listeners and caches before it opens the next.
Pairing a second project needs no sign-in today either, because pairing is CLI-administered and the
QR carries a one-shot token. **Consequence:** the sentence describes a benefit the spec's flow does
not create and the tree's flow does not lack — and the part of it that **is** new (one provider
sign-in serving many projects) is exactly what §4's D235 defers to the credentials service.

### The app, the QR and the per-project instance

**F18 — Gap. There is no Firebase app instance concept in the app, and no Firebase SDK.**
`mobile/package.json` lists Expo `^58.0.16`, `expo-secure-store`, `react-native`, `react` and
`@react-native-async-storage/async-storage`, and no auth or Firebase library. `mobile/src/feed/`
is a pure port with no database client. **Consequence:** "one Firebase app instance per paired
project" is a dependency, a lifecycle and a listener budget that do not exist, and §4's D234
recommends whether v1 has them at all.

**F19 — Rule. The app's store identity is not committed, and App Links need it.** `mobile/app.json`
carries no `ios.bundleIdentifier` and no `android.package`; `mobile/app.config.js`'s
`resolveConfig` builds them from `ID_VARIABLES` at build time; and
`mobile/src/app-config.test.js` asserts *"no reverse-DNS identity is committed here"* (D125: the
app is its own artifact and the identity is the owner's). **Consequence:** an email link's action
URL and App Links/Universal Links are **per-project public artifacts keyed by that identity** —
`assetlinks.json` and `apple-app-site-association` on the project's hosting domain — so the
spec-as-written path needs an identity the repository deliberately does not hold, in **every**
project (§3).

**F20 — Gap. No authorised domains, no OAuth client allow-list, nothing.** `firebase.json` has no
`auth` block; `deploy/terraform/gcp/modules/firebase/firebase.tf` says *"Identity Platform is
deliberately not a resource here"*; `internal/infra/identityplatform.go`'s `Config` reads the
configuration and never writes it, and `Initialize` is the only write, with no providers.
**Consequence:** the spec's *"Each project must list the Fugaro app's OAuth client ID as an allowed
client, which project setup handles once"* is new `init`/Terraform surface plus a per-platform OAuth
registration the owner performs **outside** the repository — and it is refused today by
`refusePublicSignUp` the moment it exists.

**F21 — Open. The Expo SDK 58 library choices are not verifiable here** (§0.5), and the spec's
provider table names specific mechanisms — Android Credential Manager, the iOS Google Sign-In SDK,
a browser OAuth for Microsoft, Firebase's email link. **Consequence:** any part that adopts them
carries a platform-fact table of its own, in the shape
`docs/mobile/voice-session/reconciliation.md` uses (each fact with its URL and its verdict), before
a line is written.

**F22 — Rule. The demo mode's promise is a test, not a paragraph.** `docs/mobile/play-review-access.md`
holds the sentence Play's reviewers read, and `mobile/src/demo/safety.test.js` requires it to
describe a control the app actually has. **Consequence:** a sign-in that gates the first screen
either breaks that sentence or has to be behind the demo entry — and the plan's P4 says which.

### The commands, the record and the numbers

**F23 — Gap. `fugaro pair` is approved and unbuilt; the allowlist's CLI does not exist.** Task P6.6
(`docs/mobile/implementation-plan.md`, approved 2026-10-18, SECURITY-SENSITIVE) is
`internal/cli/pair.go` + `internal/budget/token/device.go` + one line of `internal/cli/root.go`;
Task P6.8 is `mobile/src/pairing/` and `mobile/src/overview/`. The pairing **nodes** are in the tree
(rules generation 3, merged as #601). **Consequence:** this work extends an unbuilt task, and every
part of its plan that touches the CLI depends on P6.6 landing first (§2 of the plan).

**F24 — Rule. The name `allow` is taken twice** (§0.3): `fugaro allow-delete` over Terraform
addresses, and `allow_repos` over repositories. **Consequence:** `fugaro allow add|rm|ls` would be
a third meaning for one word, in a tree whose operator pages already distinguish the other two.

**F25 — Exists, and it is the model to copy. The tree already publishes declared state to the node
and already refuses to re-add what an operator removed.** `internal/cli/project_cmd.go`'s
`newProjectPolicyPublishCmd` publishes `config/policy/shared` from the checkout's `fugaro.yaml` and
the machine's config with an ETag-guarded write, `requirePolicyRules` gates it on the deployed rules
marker, and `publishAllowRepos`'s doc says *"a later publish never re-adds a name an operator
removed"*. **Consequence:** "allowlist changes are saved to the project Git repo" has a working
shape in this tree already, and the node stays what the rules read.

**F26 — Open. The release train.** `docs/releases/` ends at v0.9.5, `docs/release.md` names 0.9.6
for the managed deployer, 0.10.0 for the console's Linux packaging, and
`docs/central-orchestrator/implementation-plan.md` and the rigor modes work both target **0.11.0**.
**Consequence:** this work cannot be 0.11.0 unless P6.6 and P6.8 land there; §4's D236 recommends
where it goes.

**F27 — Rule. This folder has no pin, and the folders it reconciles mostly do not either.** Eight
pins in `internal/cli`'s `docs_*_test.go` family name `docs/mobile/` — **four are named for it**
(`docs_mobile_plan_test.go`, `docs_mobile_checks_review_test.go`, `docs_mobile_push_test.go`,
`docs_mobile_mailbox_plan_test.go`) and four more quote it beside their own folder's claims
(`docs_in_job_review_test.go`, `docs_coordinator_test.go`, `docs_voice_switch_test.go`,
`docs_voice_module_test.go`) — **no test reads `docs/central-orchestrator/` at all**, and the
register is pinned by `internal/cli/docs_coordinator_test.go` and
`internal/cli/docs_voice_switch_test.go`.
**Consequence:** P0 of the plan gave this folder a guard — `internal/cli/docs_mobile_auth_test.go`,
landed 2026-10-22 — and the guard's own comment states what it does **not** hold.

**F28 — Open. `belong`.** D114 (`docs/mobile/reconciliation.md`) records that `belong` is one of
the app's installations, *"added by the owner by hand"*, and that *"no agent ever touches it"*.
**Consequence:** nothing in this work reads, writes or names it beyond this sentence; no part of
the plan goes near it.

### Found on a second reading, and it corrects the first

**F29 — Rule. The node's new fields need no rules generation.** `internal/budget/rules/rules.json.tmpl`
declares the device node as `config/admin/devices/$uid` with `.read: false, .write: false` — **no
`.validate` on the node and no `$other` refusal beneath it** — and `userClaim`
(`internal/budget/rules/rules.go`, the `USER` macro) turns on the node's **existence**,
`root.child('config/admin/devices').child(auth.uid).exists()`. Nothing in the deployed document names
a child of that node. **Consequence:** an `email` and a `role` written beside it change **nothing the
document does** — a client's read of the node and a client's write to it are refused exactly as
before, the `USER` predicate is untouched, and a node carrying the two fields is admitted exactly as
a bare node is — so the fields take **no rules generation**, an installation that never redeploys
its rules is unaffected, and nothing in the plan owes the owner a deploy. The tree's own precedent
says the same thing in `internal/projectpolicy/version.go`'s generation-3 note: the credentials
children "deliberately did not make a generation 4", because they are "optional, validated children
of nodes a run already writes … so an installation that has not opted in behaves identically", where
a bump "would make `projectpolicy.Read` answer 'not found' and drop every published policy … on any
installation whose owner has not redeployed the rules yet". **What would force a generation later:**
the first rule that **reads** one of these fields — a role predicate, the later half of §4's D225 —
or a `validate` that constrains the node's shape. Both are decisions this work does not take, and
§4's D225 recommends not taking the first in v1. **Where this changes the plan:** P2 of
[implementation-plan.md](implementation-plan.md) is written against this finding rather than against
a generation bump, the glossary's rules-generation entry below is corrected with it, and D235 and
D236 read accordingly. **The finding is placed last on purpose**: it was not in this document's
first reading, and a reader who wants to see what a correction looks like in this folder should find
it where it was found rather than renumbered into the sequence.

---

## 3. The path the spec describes, written out in full

The owner may take §4's alternatives, in which case this section is the specification of what
follows. It is written out here rather than left as a cost sentence, because a decision taken
against a one-line cost is a decision taken blind. **Every item below is a change to *each*
project** — Fugaro is one Firebase project per installation (`docs/design/project-centric.md`,
`docs/mobile/reconciliation.md` D114), so "once" means "once per project, by its owner".

### 3.1 What would have to change in each project's Firebase configuration

| What | Where it lives today | What the spec-as-written path needs |
| --- | --- | --- |
| **Enabled providers** | `internal/infra/identityplatform.go`'s `PublicSignUp` reads `defaultSupportedIdpConfigs` and `oauthIdpConfigs`; `Initialize` writes neither | `google.com` and `microsoft.com` idp configs enabled, and — for the email link — `signIn.email.enabled`, which is the **first** field `PublicSignUp` reports |
| **The OAuth client ID allow-list** | Nothing configures it anywhere (§F20) | Each platform's OAuth client (Android's, iOS's, the web client) registered for the app and admitted by the project; the client IDs are the **owner's**, created in Google Cloud and in Entra ID, outside this repository |
| **Authorised domains** | `firebase.json` has no `auth` block; the config is read and never written | The action URL's host and every dynamic-link host added to the project's authorised domains |
| **The email-link action URL, and App Links** | Nothing; the app commits no identity (F19) | A per-project action URL plus `assetlinks.json` on the project's hosting domain for Android and `apple-app-site-association` for iOS, each keyed by the app's package/bundle identifier and, for Android, by the signing certificate's SHA-256 fingerprint |
| **The refusal itself** | `internal/cli/init_firebase.go`'s `refusePublicSignUp`, called from `identityPlatform` and from `idpStep.initialize` | The refusal lifted **for exactly the enabled fields**, with the rest of `PublicSignUp`'s list still refused — a narrower rule than the one the tree has, and one that has to be written and tested rather than switched off |
| **The rules' input** | `USER` = a `mu` claim plus the device node; `TOKEN` = `fp` + `fx` | A predicate over a **provider-signed** claim (`email`, `email_verified`) that no Fugaro component wrote (F7) |

### 3.2 What that does to the attack surface

- **Anyone with a Google, Microsoft or Apple account can obtain a session in any project whose QR
  they can read.** The web API key is public (F9) and the QR carries it; today the key buys a
  `signInWithCustomToken` that only Fugaro's signer can satisfy, and after the change it buys a
  session. The QR stops being an invitation and becomes the front door.
- **The rules become the only fence, so they must deny everything not on the allowlist.** The tree
  is already default-deny (F10) — `".read": false, ".write": false` at the root, every grant a named
  macro — and that property must survive the change **and be proved**, not asserted: an
  unauthenticated read, a session with no matching entry, and a session whose `email_verified` is
  false must each be denied, in the emulator, per family.
- **The verified-email requirement is per provider, and it is not uniform.** Google's ID token
  carries `email_verified` for a verified address; Microsoft's (Entra ID) identity is a directory
  identity whose `email` may be absent, may be a `preferred_username`, or may be an unverified
  address a tenant allows; Apple's may be a private relay address that is not the person's address
  at all (F16). **So "the verified email" is not one property across the three rows of the spec's
  own provider table** — and the one verified-email check the tree has (`userEmail`'s
  `email_verified` refusal, over Google's userinfo) covers exactly one of them.
- **The removal path changes meaning.** With the node, deletion revokes on the next request (F5);
  with a provider-signed claim, it revokes when the ID token expires — up to an hour — and the
  spec's own sentence (*"removing someone from the allowlist takes effect immediately"*) stops being
  true. The only way to keep it true in the spec's path is for the rules to **also** consult a node
  the operator can delete, which is the tree's design arriving back through the side door.
- **A provider session is a session in *every* project the phone has paired.** Under D114's model
  the accounts are separate, but the *credential* is one per provider, so a leak of the app's stored
  credential is a leak of the material for every project at once — where today the material is a
  per-pairing token the app never keeps in that form (D111's two items).
- **What does not change:** no Fugaro component gains a credential, no run's identity moves, the
  budget caps and the `TOKEN` family are untouched, and `config/admin` stays client-denied (F14).

### 3.3 The allowlist's rules, and how they map onto the device node

The owner's answer of 2026-10-20 already fixes the mapping, and the spec's *"exact email or a whole
domain, each with a role, exact overrides domain"* fits it as follows:

| The spec's rule | On the device node | Enforceable in the rules? |
| --- | --- | --- |
| An exact address is allowed | the node at `config/admin/devices/<uid>` exists, and its `email` field equals the token's address | **Yes** — `auth.uid` is path-safe and the email is compared as a **value** (F4) |
| A whole domain is allowed | a domain entry resolved **when the entry is written**: the person's phone is paired, and the node carries the address | **Only as a literal pattern** over `auth.token.email`; a dynamic pattern read from data is UNVERIFIED (§5), and D6's answer puts the rule at administration instead |
| An exact address overrides its domain's role | the node carries the **resolved** role, so there is nothing to resolve at read time | **Yes** — one field of one node, which is immediate (F13) |
| Roles: viewer / operator / admin | a `role` field on the node, written day one, read by the CLI and the console; no rule reads it in v1 | **Yes, later** — `child('role').val()` is expressible and immediate; v1 grants every paired device the same `USER` and splits nothing (F12) |
| Removal takes effect immediately | deleting the node | **Yes** — and this is the property the spec's own path loses (§3.2) |

---

## 4. The decisions planning found: D221–D236

**Each row is a decision, not a question**, and each is written **recommendation first**, with the
alternative and its cost. The plan is written to the recommendations; a part whose decision is
answered the other way is rewritten before it starts, not after. **Every one of the sixteen was
answered by the owner on 2026-10-22, each as recommended**, and each answer stands under its own
decision below; the plan's **P0 and P1 are approved**, and **P3, P4 and P5 still wait** on the
mobile Phase 6 tasks, exactly as the plan's §11 records. The numbers are taken from the register
the way
`docs/mobile/voice-session/README.md` takes its own: the highest **written** decision number in
`docs/` at `d9dda8f3` is **D220** (that folder's own block), **D193–D197** are the coordinator's, and
**D198–D200** are free but **D200** is recorded there as possibly held by the owner's notes — so
this work starts **above every written number** and takes **D221–D236**.

### D221 — Does a provider session reach the project's database?

**Recommendation: no.** Sign-in stays what it is: **custom tokens only**, per project, with the
pairing that D165/D166 already answer. A provider sign-in, if the owner wants one at all, is an
**identity claim checked once, at pairing**, by the CLI that writes the node — never a session the
database admits. This keeps `refusePublicSignUp` intact, keeps `USER` keyed by `uid`, keeps
revocation immediate (F5), and leaves every project's Firebase configuration exactly as it is.
**Alternative:** the spec as written — `signInWithCredential` per project, the rules reading the
token's email. **Cost:** §3 in full: the refusal lifted per project, providers and OAuth clients
and authorised domains and an action URL configured per project (§3.1), a QR that is the front door
(§3.2), a per-provider verified-email question the tree answers for one provider only, the loss of
immediate revocation unless the rules keep reading a node anyway, and an authorisation input no
Fugaro component wrote (F7). **The recommendation is not a refusal of the idea**: a provider
sign-in that *identifies* a person to the admin who is pairing them is cheap and useful, and it is
what D222 and D226 shape.

**Answered 2026-10-22: as recommended** — no provider session reaches a project's database:
sign-in stays custom tokens only, and a provider sign-in, if the owner ever wants one, is an
identity claim checked once, at pairing.

### D222 — What is the allowlist, and where does it live?

**Recommendation: the device node, with the administered email and role as fields on it** — exactly
what the owner's answer of 2026-10-20 defines (`docs/mobile/functional-spec-reconciliation.md`
§3.7, edit 7) — and **no new email-keyed map anywhere**. **Alternative:** a new
`config/admin/allow/<key>` node keyed by address, which is what the spec's sentence suggests.
**Cost:** an address cannot be an RTDB key (F4), so the key would have to be an encoding the rules
cannot reproduce from `auth.token.email`; and the node would be a **second** record of the same
fact as the device node, with the two able to disagree — which is the state D166's "delete the node
and it is over" was written to avoid.

**Answered 2026-10-22: as recommended** — the allowlist is the device node, with the
administered email and the role as fields on it, and no email-keyed map anywhere.

### D223 — Where is the domain rule enforced?

**Recommendation: at administration — when the entry is written.** An admin pairs a phone whose
address is in an allowed domain; the node records the address; nothing at read time tests a domain.
This is the recommendation of `docs/central-orchestrator/reconciliation.md`'s D6, which the owner's
answer of 2026-10-20 took, and it needs no change to any project's sign-in platform.
**Alternative:** the domain rule as a rules predicate over `auth.token.email`. **Cost:** the
literal-pattern form admits exactly one pattern per project (a set cannot be read by a rule, F4),
the dynamic form is UNVERIFIED (§5), and either way it admits an address **before** anyone has
looked at it — which is the opposite of an allowlist.

**Answered 2026-10-22: as recommended** — the domain rule is enforced at administration, when
the entry is written; nothing at read time tests a domain.

### D224 — Where does "an exact email overrides its domain's role" get resolved?

**Recommendation: once, in the CLI, at the write.** The resolver takes the entry being added, the
exact entries and the domain entries, and writes the **resolved** role onto the node; the node is
then the only thing any reader consults, and a later change to a domain entry does not silently
re-role anyone (a re-role is a write to the node, and it is recorded as one).
**Alternative:** store the unresolved input and resolve at read time. **Cost:** every reader — the
CLI, the console, the consumer — reimplements the ordering, and the rules cannot do it at all
(F13), so the one place where the rule would be missing is the one place it matters.

**Answered 2026-10-22: as recommended** — the exact-over-domain resolution runs once, in the
CLI, at the write, and the node stores the resolved role.

### D225 — Where does the role live, and what reads it in v1?

**Recommendation: a `role` field on the device node, written from day one and read in v1 by the
CLI and the console — never by the rules** (the spec's own *"v1 only checks admin and everyone
else"*), with the first rule that needs it reading it as a sibling field of the node, which is
expressible and immediate. **Alternative:** put the role in a claim on a minted token. **Cost:** a
claim cannot be revoked before it expires (`PathDevices`'s comment, F5), so a demoted admin stays
an admin for up to an hour — the exact failure D166 chose a node to prevent.

**Answered 2026-10-22: as recommended** — the role is a field on the device node, written day
one, read in v1 by the CLI and the console and **never by the rules**.

### D226 — The verified email: which claim, checked where?

**Recommendation: the CLI checks it at pairing, and no rule trusts a provider's email claim.** The
shape already exists: `internal/cli/init_members.go`'s `userEmail`, called by `authenticatedUser`,
refuses an unverified address with *"the email %s is not verified"*. The administered email on the
node is what the CLI **wrote after checking**, and it is compared to the token's address only if
the owner takes D221's alternative. **Alternative:** `auth.token.email_verified` in the rules.
**Cost:** it is one property in the spec's table and three in reality (§3.2) — Google verifies,
Entra ID's directory identity may carry no address at all, and Apple's may be a relay (F16) — so
the predicate would be true of some people and silently false of others, in the one place where a
false negative locks a person out and a false positive lets one in.

**Answered 2026-10-22: as recommended** — the CLI checks the verified email at pairing, in the
shape `userEmail` already uses, and no rule trusts a provider's email claim.

### D227 — Sign in with Apple, and the private-relay address

**Recommendation: Apple is offered on iOS only when the identity it returns can be matched by
something the administrator wrote — and a relay address is never an allowlist key, and the relay
domain is never a domain entry.** Under D221's recommendation this costs nothing: pairing keys the
node by `uid` and records the address the admin confirmed, and Apple's row in the spec's provider
table becomes a **sign-in that identifies a person to the admin**, not an authorisation.
**Alternative:** the spec's Apple row as written, with the allowlist matching the address Apple
returns. **Cost:** `@privaterelay.appleid.com` is per-app and per-Apple-ID, is not the person's
address, changes when the relay is switched off, and can only be matched by a domain entry that
admits **every** Apple user who hides their address (F16). **And note what the spec itself says:**
Apple's ID token *"can't be silently refreshed on the client"*, so Apple is the one provider whose
row in the spec's own table requires a Face ID prompt **per pairing** — which is the friction the
first two rows were chosen to avoid.

**Answered 2026-10-22: as recommended** — Apple is identity-only: a private-relay address is
never an allowlist key and the relay domain is never a domain entry.

### D228 — The email link

**Recommendation: refused in v1, by name.** It is the one provider in the spec's table whose
enablement is *exactly* `signIn.email.enabled` — the first field `refusePublicSignUp` reports
(F20) — and it needs a per-project action URL plus App Links/Universal Links keyed by an app
identity the repository deliberately does not commit (F19). **Alternative:** adopt it as the spec
describes, per project, with the pending-pairing state and the deep link. **Cost:** §3.1's action
URL and App Links surface **in every project**, the app's committed identity restored (D125
reversed), and a second authorisation path (the address in the link) that has to be reconciled with
the node — plus the one requirement the spec states and cannot be engineered around: *"the link must
be opened on the same phone"*.

**Answered 2026-10-22: as recommended** — the email link is refused in v1, by name.

### D229 — The command: `fugaro allow add|rm|ls`, or `fugaro pair`?

**Recommendation: `fugaro pair`, extended — not a new `allow` family.** P6.6's planned
`internal/cli/pair.go` already owns `pair` (add), `pair ls` and `pair revoke` (delete the node,
which is the revocation), and the allowlist's entry **is** the paired device, so one command family
covers one record. The email and the role arrive as flags (`--email`, `--role`). **Alternative:**
`fugaro allow add|rm|ls` as the spec writes it. **Cost:** the word `allow` already means two other
things in this tree — `fugaro allow-delete` over Terraform addresses and `allow_repos` over
repositories (F24) — so a third meaning would make "the allowlist" a question rather than a
reference, in the operator pages that have to distinguish all three.

**Answered 2026-10-22: as recommended** — the command is **`fugaro pair`, extended**, with the
email and the role arriving as flags and not as a new `allow` family.

### D230 — The project creator becomes the first admin: how?

**Recommendation: the first pairing writes it, and `fugaro init` seeds the address it will match.**
The project's creator holds `roles/firebasedatabase.admin` (which is what writing the node needs,
F14), so the first `fugaro pair` run by the creator writes the first entry with `role: admin`; and
`fugaro init` records the creator's address as the project's **seed** — a declared value, published
the way the policy is published (F25) — so the first pairing of that address is admitted without a
second admin existing. **Alternative:** `fugaro init` writes the entry itself. **Cost:** no Auth
`uid` exists before the creator signs in, and the node is keyed by `uid` (F2), so `init` would have
to write an entry it cannot key — or write a *second* record keyed by address, which D222 refuses.

**Answered 2026-10-22: as recommended** — the first pairing writes the first entry with `role:
admin`, and `fugaro init` records the seed address it will match.

### D231 — "Allowlist changes are saved to the project Git repo": what file, who merges, and what is
the source of truth?

**Recommendation: the node is the source of truth for the decision, and the project's Git repo
holds the declared entry list as a seed and a record — published to the node by an ETag-guarded
write, in the shape `fugaro project policy publish` already uses.** The declared list lives in the
project's `fugaro.yaml` (the checkout's config, which `localcfg` already reads); the CLI writes it
to `config/admin/devices` with the rules-marker gate `requirePolicyRules` already applies; a
removal is a `pair revoke`, and the publish **never re-adds** an entry an operator removed — which
is `publishAllowRepos`'s existing rule applied to people (F25). A change to the file is a pull
request to the project's repository, reviewed and merged by whoever reviews that repository; it
**seeds** the node and does not overrule it. **Alternative:** Git as the source of truth, synced
into the RTDB by a job. **Cost:** a sync job in every project, a window in which Git and the node
disagree about whether a person is admitted, and revocation that stops being one write — which is
the property F5 exists to keep. **What the alternative buys, stated:** an auditable history of who
changed what, which is the real thing the spec's sentence asks for; the recommendation buys it with
a record beside the node rather than a record in front of it.

**Answered 2026-10-22: as recommended** — the node is the source of truth; the declared list in
the project's `fugaro.yaml` is a seed and a record, published to the node, and a publish never
re-adds what an operator removed.

### D232 — What the app stores on the device

**Recommendation: two items stay two** — `mobile/src/accounts/secret.js`'s `SECRET_ITEMS`
(`idToken`, `macKey`, D111) — and no provider credential is stored, because under D221 there is no
provider session to refresh. **Alternative:** the spec's stored provider credential. **Cost:** a
third item in a deliberately closed set, a refresh path per provider, and the app's boundary tests
(`mobile/src/boundary.test.js`, `mobile/src/accounts/index.js`) rewritten to admit it — which is
exactly the deliberate act they exist to force, so the alternative is *possible*; the recommendation
is that it is not worth what it costs while D221 stands.

**Answered 2026-10-22: as recommended** — two secure-store items stay two (`idToken`,
`macKey`), and no provider credential is stored.

### D233 — The QR payload

**Recommendation: unchanged — the project's Firebase configuration and the one-shot pairing token,
and never an identity, never an email, never a credential.** The spec's *"not an identity"* is
right and should be kept verbatim in the app's payload validator (Task P6.8's *"a scanned payload is
validated before anything is stored"*); the token the spec omits is what makes pairing possible
without a sign-in (F4 of §1, D165). **Alternative:** add the project's email domain, or the
creator's address, to the payload so the app can pre-check. **Cost:** the payload is a public,
photographable artifact; anything on it is disclosed to anyone who sees the screen, and a
pre-check on the phone is a check nobody is accountable for.

**Answered 2026-10-22: as recommended** — the QR payload is unchanged: the Firebase
configuration and the one-shot pairing token, never an identity.

### D234 — The per-project Firebase app instance on the phone

**Recommendation: v1 has none, and the account model stays D114's** — an account **is** an
installation, several stored as non-overlapping entries, the switcher tearing down the previous
account's listeners and caches before it opens the next. Under D221 the app needs no Firebase SDK
at all, so the memory, the refresh timers and the listener count are all zero. **Alternative:** the
spec's model — one app instance per paired project, each with its own session and RTDB access.
**Cost:** a Firebase SDK dependency in `mobile/package.json` where there is none (F18), one live
session and one listener set per paired project held simultaneously, a refresh path per project,
and the promise of the spec's step 6 (no new sign-in for later projects) bought with the credential
class D232 refuses. **If the owner ever takes D221's alternative**, this decision is the one that
has to be re-taken first, because it decides how much of the device the model occupies.

**Answered 2026-10-22: as recommended** — v1 has no per-project Firebase app instance, and the
account model stays D114's.

### D235 — What is v1, and what is later?

**Recommendation.** **v1** is the allowlist as the tree already plans it plus the two fields the
owner's 2026-10-20 answer adds: the administered **email**, the **role**, the **unverified-email
refusal at pairing**, and the CLI that writes them — `fugaro pair` extended (D229), the node's new
fields **beside the existing one and in no rules generation** (F29; the plan's P2), and the app's
first screen unchanged except for the
"ask your admin" state. **Later**, and only by a separate decision: provider sign-in at all (D221),
the email link (D228), the per-project app instance (D234), and the console's allowlist screen —
the spec's *"later from the console"*, which needs the console's operator page, itself unbuilt
(`docs/design/desktop-console-v2.md`'s Overview). **Latest**, and dependent on unbuilt work:
**custom tokens minted by the credentials service so one email login covers all projects** — which
is the spec's own deferral, and which needs `docs/credentials-service/`'s first slice to exist
(§0.2).

**Answered 2026-10-22: as recommended** — v1 is the allowlist's two fields and the CLI that
writes them; the provider half, the console's screen and the minted custom tokens are later.

### D236 — Which release does this belong to?

**Recommendation: the first release after the mobile Phase 6 tasks it extends — 0.12.0, or whatever
the release after Phase 6 is called — and not 0.11.0.** 0.11.0 is where
`docs/central-orchestrator/implementation-plan.md` puts the central orchestrator and where the rigor
modes work (open PR #773) puts itself (F26); this work extends `internal/cli/pair.go` and
`mobile/src/pairing/` (Task P6.6 and P6.8, approved 2026-10-18 and unbuilt), and no part of it can
be merged before they are. **Alternative:** 0.11.0. **Cost:** it makes an unbuilt, approved task a
prerequisite of a release whose contents are already decided, and it puts a new `fugaro pair` flag
set and a second writer of the authorisation record in a release that has to ship two other works'
security surfaces. **No
part of the plan assumes a release that has not shipped**, so a later release costs nothing but
time.

**Answered 2026-10-22: as recommended** — the first release after the mobile Phase 6 tasks it
extends, 0.12.0 or whatever that release is called, and not 0.11.0.

---

## 5. UNVERIFIED

Everything here was **not** established by this reconciliation, and a part that depends on one of
these rows must settle it first — by reading the vendor's page, by a spike, or by an owner-run live
check.

| # | The claim this work cannot make | Why, and what would settle it |
| --- | --- | --- |
| U1 | That Firebase's Realtime Database rules expose `auth.token.email` and `auth.token.email_verified` for a provider-signed session, and that `String.matches` accepts a **pattern read from data** rather than a literal | No network, and no rule in this tree reads a provider claim (F7). The tree proves the *shape* — `auth.token.mu`/`fp` are read, and `newData.val().matches(/…/)` is used with a literal (`internal/budget/rules/rules.go`) — and nothing proves the rest. Settled by the rules emulator, `-tags firebase`, on a synthetic provider token |
| U2 | Whether Entra ID (Microsoft) returns a verified address at all, and under which claim | The tree has no Microsoft identity path. Settled by the owner's own tenant, or by reading Microsoft's and Firebase's documentation on the day a part needs it |
| U3 | Whether Sign in with Apple returns a private-relay address for a given user, and whether it can be turned off per app | F16: nothing in `docs/` mentions the relay. Settled by Apple's documentation and one owner-run sign-in |
| U4 | App Store Review Guideline 4.8's current text and its exceptions | §0.6. Settled by reading the guideline; the spec cites it and this work does not |
| U5 | Expo SDK 58's library options for Google (Credential Manager vs. the Google Sign-In SDK), Microsoft (browser OAuth) and Apple, and their current versions | §0.5: no network, no `node_modules`, no Node. Settled by a platform-fact table in the shape `docs/mobile/voice-session/reconciliation.md` uses, each row with its URL and its verdict |
| U6 | That a provider-signed Firebase ID token for a project whose Identity Platform config has providers enabled still passes the tree's `USER` macro **nowhere** (i.e. the default-deny proof, live) | It follows from the template (`".read": false` at the root, no grant for a session carrying none of the claims) and is *not* measured. Settled by the emulator, and by an owner-run check on a real project |
| U7 | Whether the app's own test runner can be run in this container | No Node is installed (`node --version` finds nothing), so every part that touches `mobile/` is verified by CI and by the owner, and this change touches no `mobile/` file |
| U8 | Whether `docs/central-orchestrator/`'s D6 answer will be recorded in its own cell | §0.1: the answer is the owner's word of 2026-10-20, relayed into `docs/mobile/`. Settled by the owner, and by the change that writes it there |

---

## 6. Glossary

- **The allowlist** — in this document, and in `docs/mobile/`, the record that decides who may use a
  project from a phone: the device nodes at `config/admin/devices` (`internal/budget/rules/rules.go`'s
  `PathDevices`), each carrying the email and the role the owner's 2026-10-20 answer defines.
  **Not** the follow-up comment allowlist of `docs/design/v1.md` (the GitHub accounts whose PR
  comments a follow-up obeys), and **not** `allow_repos` (the repositories a project admits) and
  **not** `fugaro allow-delete`'s Terraform addresses. Four things in this repository are called an
  allow-list; this work means the first.
- **The device node** — one child of `config/admin/devices`, keyed by the Auth `uid` of a paired
  phone's account, unreadable and unwritable to the client it names, deleted by `fugaro pair
  revoke`, and the reason revocation is immediate (D166, F5).
- **The administered email** — the address an administrator writes onto a device node after
  checking it, per the owner's answer of 2026-10-20. It is a **field on the entry**, not an email
  list, and it is what a domain rule is applied to.
- **Pairing** — the CLI-administered act that turns a scanned QR into a device: the phone generates
  a key pair, publishes its public key into `config/pairing/<uid>/request`, an operator types the
  code that is a fingerprint of that key, and the CLI delivers the device's token and MAC key
  encrypted to it (D165, Task P6.6). **Discovery, not identity.**
- **The user claim (`mu`)** — the claim a paired phone's custom token carries, which must equal
  `auth.uid`; with the device node it is the whole of `USER` (`internal/budget/rules/rules.go`'s
  `userClaim`).
- **The provider path** — the spec's flow: a Google, Microsoft or Apple ID token exchanged with
  `signInWithCredential` for a Firebase Auth session in each project. Named throughout as the
  alternative to D221's recommendation, and written out in §3 so a decision against it is not taken
  blind.
- **`refusePublicSignUp`** — `internal/cli/init_firebase.go`'s refusal of any Identity Platform
  configuration with a public sign-up path enabled, and the program that would have to change for
  the provider path to exist.
- **Rules generation** — `internal/projectpolicy/version.go`'s `RulesVersion`, currently **3**; the
  device node and the pairing nodes arrived with it (#601). **The node's new fields are not a
  generation** (F29): the deployed document names no child of the device node, nothing in it reads
  one, and the template's `.read: false, .write: false` on `config/admin/devices` is what a client
  meets either way. A generation would be the first rule that **reads** one of the fields (D225's
  later half), or a `validate` over the node's shape — neither of which this work takes.

---

## 7. Open for the owner

The decisions of §4 are the owner's to answer. These five things are **not** decisions and are
recorded here because a reader will look for them:

1. **Where the D6 answer lives.** The owner's answer of 2026-10-20 is recorded in
   `docs/mobile/functional-spec-reconciliation.md` §3.7 and not in the document whose D6 it answers
   (§0.1). Writing it into `docs/central-orchestrator/`'s D6 cell is a change to **that** folder,
   which this work does not make; the owner may ask for it, and until then §0.1's provenance
   sentence is the citation a reader needs.
2. **Whether `fugaro init` should write anything about the allowlist.** D230's recommendation has
   `init` record a seed address. That is a change to what `init` writes — and therefore to what
   `init` asks the operator to confirm — and the owner may prefer that `init` write nothing and the
   first pairing be the only writer.
3. **The console's half.** The spec's *"later from the console"* names a screen on a console whose
   project-wide view is unbuilt (`docs/design/desktop-console-v2.md`). Nothing in this work plans
   it; D235 records it as later.
4. **The release.** D236 recommends the release after Phase 6 and gives its reason; the owner names
   releases.
5. **The app's store identity for App Links.** D228's refusal is cheap partly because the app
   commits no identity (F19). If the owner ever wants the email link, the first thing needed is a
   decision about the identity in `mobile/app.json` — which is D125's, not this work's.
