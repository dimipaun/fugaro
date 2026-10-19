# Push notifications to the phone — P5.2

**Status: design only; nothing is implemented, and no push service, device-token store,
sender, rules change or app code exists anywhere in this repository or in any other.**
This is **Task P5.2** of [the mobile plan](../implementation-plan.md), **Phase 5
("later, unscheduled")**, whose four tasks are recorded so that a later reader finds the
reasoning rather than rediscovering the question. It writes nothing but this file and the
one documentation test beside it (`internal/cli/docs_mobile_push_test.go`, by the same rule
[budget-token-channel.md](../../design/budget-token-channel.md) states for its own test: the
test is red until this document exists). **No decision number is allocated here**: the
questions at the end are **Q1, Q2 …** for the owner, and a later pass assigns the numbers.

**Why this document exists, and why it is not a reversal.** **D121 was answered by the owner
on 2026-10-17: "no push in v1."** The answer is the reconciliation's own recommendation, and
it defers push to "a **separate decision** with its own service, its own device-token store
and its own retention question" ([reconciliation.md](../reconciliation.md), D121) — which is
this task. The brief that asked for this design says the same thing: **"depends on D121
answered yes"**, and D121's answer was neither a yes nor a no to push as such; it was "not in
v1, and a decision of its own later". So this document decides nothing on its own. It exists
so that the owner can answer that later decision against real options rather than from
scratch, and so that the answer's cost is priced before anything is built.

Every claim about the code below was checked against `origin/main` at **`371038f`** (read
2026-10-19 — the tree in which **#601 has merged**: rules generation 3, the device node, the
pairing nodes and the MAC symbols), by reading the file and the symbol named. Symbols are cited
by name rather than by line number, because the lines move. **An earlier revision of this
document was written against `90ba554`** (v0.8.7), where #601 was still an open branch and the
device node, `RulesVersion = 3` and the MAC symbols were **not in the tree**; every claim that
rested on that branch point is rewritten here as a statement about the merged tree, and each
relocation is recorded where it was made rather than left to age silently.

**Related.** [reconciliation.md](../reconciliation.md) (D112, D113, D117, D118, D121,
D126), [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) (D165,
D166, D169 — the pairing, the device node and the MAC), [implementation-plan.md](../implementation-plan.md)
(P4.1 the consumer, P5.1 the orchestrator, phase 6), [voice.md](../../design/voice.md) (D49,
D50 and the F38 residual), [desktop-console-v2.md](../../design/desktop-console-v2.md) (D33 —
the same question asked of the desktop console, which rejected "a push service (FCM)"), [m9-budget-and-dashboard.md](../../design/m9-budget-and-dashboard.md)
§6.2 and §6.4 (the mailbox tree and its rules), [budget-token-channel.md](../../design/budget-token-channel.md)
(a token the phone also holds, and the audience rule it states), [project-centric.md](../../design/project-centric.md),
[managed-projects.md](../../design/managed-projects.md) (D55–D78, the gate an always-on
server would sit behind), [SECURITY.md](../../../SECURITY.md), [docs/gcp-live-checklist.md](../../gcp-live-checklist.md).

## 1. The problem

A run of a repository is asynchronous and long. It is launched from a laptop, runs in a Cloud
Run job for minutes or hours, and ends in one of a handful of states a person wants to know
about **while away from the desk**:

1. **it finished** — the run reached a settled outcome and a pull request is ready for review;
2. **it failed** — the run ended `infra_error`, errored, or was halted (the budget halt, the
   kill switch, a cancellation);
3. **it needs the owner's answer** — the mailbox carries a prompt — an inbox/outbox message of
   `kind: prompt` (**D168 as amended**, plan **P6.3** for the field and **P6.5** for the queue
   and the interrupt policy; D160, D173) — that the orchestrator cannot proceed without, and
   that expires at **D118's TTL** (the owner's 72 hours, set on 2026-10-17);
4. **a budget threshold was crossed** — a cap, a kill switch or a per-run ceiling the person
   set is now binding on a run.

**Two of those four are not the owner's to receive, and the design must say so rather than
list them.** The interrupt set the owner actually chose is **D160** (2026-10-17): the **kill
switch**, **cap reached** and **consumer offline**, with **cap-near available but off by
default**, and D127's switch able to narrow or widen it to **any prompt**. Categories 3 and 4
above are that set — the prompt is the widening D127 already anticipates, and the budget
category is D160's cap/kill pair. Categories 1 and 2 **are not**, and the reason is recorded
in D160 itself: failed and halted runs were left out **because "the phone cannot see them"**.
"Finished" is in the same position for the same reason — the phone cannot see it either.

That is not a technicality. Under tension **(b1)** (resolved 2026-10-17; `reconciliation.md`
§4) the phone may read exactly four families — `config/caps/**`, `spend/$day/**`,
`config/kill/**` and its own mailbox (outbox and heartbeat) — and it has **no read of
`agents/$slug/$run` and no `.read` on `outcomes/**`**. So there is no place in the
authenticated mailbox for the app to resolve "a run finished" or "a run failed" against. The
app's §3.9 rule — an id or category it cannot resolve is ignored — would silently drop them,
which is worse than not offering them: a category that can never be confirmed is a lock-screen
lie the app itself posts. And the sender could not fill that gap even if it wanted to: P6.3
requires a **`mac`** on every outbox message, and the sender holds no per-pairing key — the key
is D169/D171's, held by the phone and the laptop consumer, and §5's "*the rules cannot verify a
tag*" paragraph is why no one else can write the tag (§4 rule 11 is what makes the app check it).
*(§3.6's own "no key" argument is about a different credential — the **FCM** one — and not about
this key.)*

**So the reconciled category set is D160's**: the **kill switch**, **cap reached** and
**consumer offline**, with **cap-near off by default**, and the **prompt** category that
D127's switch can widen to. "Finished" and "failed" are **not** in the default set. They stay
in this document as the two the owner may **choose to add**, and they cannot be added without
widening the phone's read grant — which is a change to tension (b1) and is the owner's, not
this design's. **Q4 now asks that in those terms**, with the narrower alternative that needs no
widening: the consumer writes a **derived, non-sensitive status** into the mailbox (a category
and a boolean, MAC-signed like every other message, naming nothing), and the app resolves that
instead of the run record it cannot read.

Today the phone learns none of these by itself. The app's whole data path is a **foreground
one**: it reads its own mailbox over the authenticated database connection (D112's user-claim
read grant), and derives "the orchestrator is offline" from the heartbeat's staleness
(D117, `shared/presence/index.js`'s `evaluatePresence`). Nothing wakes the phone, and
nothing runs when the app is not in front of the user. **D121 named exactly this and accepted
it for v1**, on the ground that "the voice feature is a foreground activity".

The question this document answers is therefore narrow and honest: **if the owner later wants
the phone to be told, what would be built, what would it send, what would carry it, who would
hold the credential, and what could go wrong?** It is not "should push ship in v1" — that was
answered.

## 2. What exists today

**Nothing that sends a notification exists.** In particular:

- **No push library, no push service, no push token.** No Go package under `internal/` or
  `mobile/` imports FCM, APNs, Expo's push service or `firebase-admin/messaging`;
  `go.mod` carries no FCM client, and `mobile/package.json` declares **no runtime dependency
  at all** (the manifest is deliberately empty until the Expo toolchain is pinned, and
  `mobile/src/skeleton/index.test.js` keeps it so). **The permission exists in a catalogue and
  nowhere else**: `deploy/terraform/testdata/custom-role-permissions.txt` lists
  `firebasecloudmessaging.messages.create` and its siblings among the permissions this
  repository knows the names of *(the file is in this tree: it landed on
  `main` after this branch's base, `82ba600`, and `371038f` carries it)*, but that file is a **permission catalogue**, not a
  dependency — **no code, no manifest and no Terraform resource uses any of them**, and no
  role in `deploy/terraform/` grants one. On `origin/main` before this document, every occurrence of the phrase "push token" is the **git**
  credential (comments in `internal/runner/provision.go` around `provisionGitToken`, and in
  `provision_test.go`). **This document introduces a second sense, the FCM/APNs registration
  token, and uses "push token" only for that sense.** **D165's pairing mints two
  Firebase custom tokens, neither of which is a push token**: the **pairing** token
  (`p~<random>`, `exp` ≤ 5 minutes) that the QR carries and that may write only its own
  pairing-request node, and the **device** token (`m~<random>`, `mu = uid`, never `r~`) that
  `fugaro pair` mints **only after** the operator types the code confirming the phone's key
  (D165; plan P6.6, which calls the delivered token the "device token"). They are different
  objects with confusingly similar names, and this document uses **"push token" for the
  FCM/APNs registration token only** — a thing that exists nowhere yet. **It does not merge
  the other two senses either:** "device token" in `docs/mobile/`'s plan is that durable
  custom token of D165/P6.6, while D121's own words, "its own device-token store", are about
  the *push* store this design is a proposal for. Both readings are real, they name different
  objects, and a reader who takes one for the other will look for a token the rules' `USER`
  macro has nothing to do with.
- **No always-on server.** `grep` for `google_cloud_run_v2_service` under `deploy/terraform/`
  finds nothing: this repository deploys **Cloud Run jobs**, never Cloud Run services. The one
  per-run network process, `internal/gateway`'s `Server` (started by `internal/runner/gateway.go`'s
  `gateway.Start`), is a **loopback** proxy inside the run's own container and dies with the
  run. `voice.md` D49's words — "push notifications need a server component this project does
  not have" — are still literally true.
- **No FCM in Terraform.** `deploy/terraform/gcp/modules/firebase/` configures **Realtime
  Database** (`firebase.tf`'s `google_firebase_database_instance.this`), **Firebase
  Auth/Identity Platform** (enabled over REST by `fugaro init --firebase`, with
  `google_apikeys_key.web` restricted to Identity Toolkit and Secure Token),
  **Firestore** (`apis.tf`), and the signer/minter IAM (`iam.tf`). **Cloud Messaging is not
  among them**, and there is no Cloud Functions deployment.
- **No device-token store.** The device node `config/admin/devices/$uid` **is in the tree**
  as of `origin/main` at `371038f` (read 2026-10-19; **merged as #601**, mobile plan P6.4,
  decisions D166 and D165), and it is an **allowlist whose whole meaning is its existence** —
  not a store of anything. `internal/budget/rules/rules.go`'s `userClaim` carries the conjunct
  `root.child('config/admin/devices').child(auth.uid).exists()`, and the constant's own comment
  reads "*the child `config/admin/devices/<uid>` existing IS 'this account is paired to a phone
  that has not been revoked'*". The generated rules
  `internal/budget/rules/rules.json.tmpl` carry `config/admin/devices/$uid` inside
  `"admin": { ".read": false, ".write": false }`, and
  `internal/projectpolicy/version.go`'s `RulesVersion` is **3**, whose comment says generation
  3 "*is P6.4 … the device allowlist `config/admin/devices/<uid>` that USER now requires
  (D166), and the two pairing nodes of D165*". **What is still missing is anything that
  *writes* a device node**: `fugaro pair` is **P6.6**, approved 2026-10-18 and **not built**,
  so until it lands the only way to write one is by hand over IAM (the same sentence
  `version.go` carries). There is therefore nowhere a **push** token is stored, and no grant
  that would admit one — an allowlist keyed by uid is not a token store, and this design keeps
  its own data in a **sibling** node precisely so that stays true (§3.4). **There is also no
  path that could carry one yet**: the pairing session's one client write admits only the
  phone's public key and its timestamp (`rules.json.tmpl` generation 3), and the mailbox's
  message node names its fields and refuses every other one — so the design asks for one
  bounded field and records it as a prerequisite rather than assuming it (§3.5 (b), the plan's
  P5.2). *(The earlier
  revision of this bullet recorded the pre-#601 state — `RulesVersion = 2`, no device conjunct,
  no `devices` subtree — as a branch point; #601 has merged and the bullet is now the tree.)*

**What does exist, and what this design builds on:**

- **The mailbox**, at the top-level node `mailbox/$mb/{inbox,outbox,heartbeat}`
  (`rules.json.tmpl`; design m9 §6.4), one node per account keyed by the account's Firebase
  Auth uid. The phone is the `MAILBOX` identity, the orchestrator-side writer is the
  `CONSUMER` (`mc == $mb`), and `USER` (rules.go's `userClaim`) is the app's read claim on the
  budget families. A prompt is an inbox/outbox message of `kind: prompt`
  (`shared/mailbox-schema/index.js`), and the schema requires a **`mac`** on every message and
  on the heartbeat (P6.3).
- **The message schema and its validator** (`shared/mailbox-schema/index.js`'s
  `validateMessage`, `validateHeartbeat`, `canonicalBytes`), which is the contract a push body
  would have to agree with — or deliberately not agree with, which is the point of §4.
- **The presence rule** (`shared/presence/index.js`'s `evaluatePresence`), which already
  distinguishes `live` / `stale` / `offline` / `clock-differs` from the heartbeat, with
  `MIN_INTERVAL_MS` as D117's floor. A push is not the offline signal — the heartbeat is — and
  this design must not turn a notification into one.
- **The scheduled cloud components in the tree, and they are all jobs**: the history job,
  `deploy/terraform/gcp/modules/installation/history.tf`'s `google_cloud_run_v2_job.history`
  (running `fugaro budget history --sweep`, `internal/cli/history.go`), driven by
  `google_cloud_scheduler_job.history_sweep` (`*/15 * * * *`) and `history_rollover`
  (`30 0 * * *`, paused until the owner resumes it after the first hand-run backfill). Its
  account holds the custom role `fugaroHistory` and, from
  `deploy/terraform/gcp/modules/firebase/iam.tf`, `roles/firebasedatabase.admin`,
  `roles/firebaseauth.admin` and `roles/datastore.user`. Its
  own comment states the property this design leans on for §6: **"The job has no project
  config file … the account's identity is its only credential"** — there is no key on any
  laptop. **Two more Scheduler jobs are in the tree and neither is a service either:**
  `deploy/terraform/gcp/modules/repo/check.tf`'s `google_cloud_scheduler_job.check`, the
  **daily image check** — `internal/infra/spec.go`'s `Schedule` derives its minute from the
  repository's slug ("a minute between 05:00 and 06:59 UTC … so repositories don't all build at
  once"), it starts `google_cloud_run_v2_job.check` as the installation's scheduler service
  account, and it is **paused** until every checked workflow has a build record; and
  `deploy/terraform/gcp/modules/compute/preload.tf`'s
  `google_cloud_scheduler_job.preload_rebuild`, the **weekly** rebuild of the preloaded Batch
  boot image (`0 3 * * 1`, `var.preload_schedule`) that posts to the Cloud Build API as the
  preload service account. Both are a Scheduler job starting a job or an API, under an ambient
  service-account identity — **none of the three is an always-on server**, which is the
  property §3.6 and Q1 read off them.
- **One place the site already speaks to a provider**: `website/src/pages/privacy.astro`,
  which describes Google Analytics 4 and says plainly what it does **not** cover ("the Fugaro
  program" goes to `/security/`). §10 prices whether push touches it.

## 3. The proposal

### 3.1 What is sent: a category, and nothing that names anything

A push carries **exactly two things**:

- a **coarse category**, drawn from §1's **reconciled set** — D160's three (kill switch, cap
  reached, consumer offline), **cap-near** (D160 answers "available, off by default", so it is
  in the set and off unless the owner turns it on — §1, §3.8, Q4(i)), plus the prompt category
  D127's switch widens to, and **not** the finished/failed pair §1's numbered list opens with
  and then excludes — the
  closest thing to "status" that is safe on a lock screen; and
- an **opaque identifier**, a value that means nothing outside the phone's own authenticated
  mailbox.

**It carries nothing else.** Not a run title, not a repository name, not an owner/name, not an
error string, not a spend figure, not an amount in any currency, not a model name, not a
branch, not a PR number, not a code excerpt. The reason is not politeness: a lock screen is
read by whoever is standing there, and the push provider and the OS see the payload in the
clear. The provider's logs are not this project's to redact.

**This is a statement about the sender, not about the channel, and the difference is load
bearing.** "Content-free" describes what **this design's sender** writes; it is not a property
of FCM, of APNs, or of a notification. A push an attacker writes is **attacker-authored**, and
it can carry whatever title and body that attacker chooses — "your run needs approval: tap to
confirm" is entirely available to it (§5 attacker 2). Nothing in §3 or §4 can prevent that,
because the attacker is not sending through this design. What §3.9 and §4 rules 3, 7, 8 and 9
bound is the **consequence**: a forged push can lie, and cannot make the app act or make the
app's own screen agree with the lie.

**The opaque identifier must be genuinely opaque, and this is a real requirement rather than a
word.** A run id is not opaque: `internal/task/task.go`'s `NewRunID` is a timestamp plus two
random bytes, and `Slug` is computable from the repository path (budget-token-channel.md §2
makes exactly this point about an enumerable name). A push carrying a run id therefore tells
anyone who reads it roughly *when* a run of *some* repository happened, and a push carrying a
slug tells them *which*. So the identifier is a **random, server-minted value** that the
sender stores beside the pending notification and that maps — server-side, behind
authentication — to the detail. Even then, §3.2 makes the stronger claim: **the app needs no
identifier at all**, because it can fetch what changed from its own mailbox. The identifier
exists only so that a duplicate notification can be recognised; it is offered here as the
weaker of two options and Q6 asks the owner to choose.

### 3.2 The app fetches detail over the authenticated mailbox after opening

The push is a **doorbell, never a letter**. Tapping it opens the app; the app signs in (its
Firebase Auth ID token from platform secure storage, `mobile/src/accounts/secure-store.js`),
reads its own mailbox and budget families under the `MAILBOX`/`USER` claims it already holds,
and renders the detail from **that** — never from the payload. The payload is not stored, not
logged, and not trusted for anything.

Two consequences fall out of this and both are deliberate:

- **A push can be lost without harm.** The mailbox is authoritative and the push is
  best-effort; a dropped notification costs a delay, never a message.
- **A push cannot be forged into an action.** Because nothing actionable is in *this design's*
  payload and because tapping opens the app and nothing else, a spoofed notification cannot
  cause a cancel, a merge, a confirmation or any other act — it can only make the app open,
  and what it opens on is state read from the authenticated mailbox, not from the push. It can
  still **lie on the lock screen** before the owner ever opens the app (§3.9, §5 attacker 2),
  and that residual is stated there rather than argued away. §5 turns the action half of this
  into the document's central rule.

### 3.3 The channel: FCM, with APNs behind it — and the multi-installation problem

**Candidate: Firebase Cloud Messaging**, delivering to both platforms — FCM's HTTP v1 API for
Android, and FCM's APNs relay for iOS. The reasons are the project's own:

- **The app is already on Firebase.** An installation *is* a Firebase project
  (`project-centric.md`; `mobile/src/accounts/account.js` carries `firebaseProjectId`,
  `authUid` and `fugaroProject` per account), the app signs in with Firebase Auth custom
  tokens (D165), and it reads the Realtime Database directly (D112). FCM needs **no new third
  party**: Google is already the party holding the project's data.
- **One channel, two platforms.** FCM relays to APNs, so there is one push API and one token
  type rather than an FCM integration *and* a direct APNs one.
- **It is already paid for.** A Firebase project exists per installation and FCM has no
  per-message charge (§7).

**The multi-installation problem, which reverses part of that.** *This section is the one the
first draft of this document got wrong, and it is stated here as the correction.* **An
installation is its own Firebase project, and the app is multi-account** (D114: an account
*is* an installation, with real isolation between entries; `account.js` carries
`firebaseProjectId` per account). Two consequences:

- **FCM HTTP v1 addresses a project.** A message is sent as
  `POST /v1/projects/<project-id>/messages:send`, and it is delivered only to registration
  tokens issued **for that project** (a token minted for another project is rejected —
  FCM's `SENDER_ID_MISMATCH`/`UNREGISTERED` class of error). So a per-installation sender
  serves only that installation's devices, and **the app must hold one registration per
  installation it is signed into** — not one registration for the app. That is a real change to
  §3.4's store, and the unit it changes is the **node**: **the node is per paired ACCOUNT** —
  one (device × installation) — because `config/admin/push/$uid` is keyed by **one
  installation's** Auth uid and lives in **that installation's** database, and D166 pairs a
  fresh `m~` uid per installation. A phone signed into two installations therefore has **two
  nodes, in two databases**, each holding exactly **one** registration: the token that
  installation's project issued, and no other — a token minted for another project is rejected
  by this one (the `SENDER_ID_MISMATCH` above). **A single node could not hold a list of
  installations' tokens**, and no principal of one installation can read another's database to
  fetch the rest: installation A's launchers hold IAM in A only. (§3.4's diagram draws the
  node and §3.5's unit sentence states the unit; both say this and nothing else.)
- **iOS needs an APNs key in every installation's project.** For FCM's relay to reach an iOS
  device, that device's *Firebase project* must carry an APNs authentication key. An APNs
  `.p8` key is **team-wide**: it can send to every app of the Apple Developer team that
  issued it, not to one app. So for any installation the app's publisher does not own, direct
  FCM means either **handing the publisher's team key to somebody else's Firebase project** —
  giving that project's owners the ability to push to every app on the publisher's team — or
  **having no iOS push for that installation at all**. The store build is the owner's (D120),
  so the team is the owner's.

**This is exactly the problem a central relay exists to solve** — Expo's push service, or a
relay the publisher runs — and it may **reverse Q7's recommendation**. Under a relay, the
publisher holds the APNs key once, in one place, and installations hold an Expo (or publisher)
token instead; the price is a third party in the path, or a service the publisher must run.
**Q7 now puts the multi-installation case to the owner as the deciding question**, not the
convenience it was priced as before.

**Two claims the first draft made are withdrawn as false:**

- "FCM direct avoids the `.p8` key handling." **It does not.** The `.p8` is *required* to be
  uploaded to Firebase for the APNs relay — §7 said so while §3.3 said the opposite — and the
  multi-installation case above is precisely where the key's team-wide reach bites.
- "One credential." **Not for a multi-installation app.** One FCM sender identity per
  Firebase project, plus an APNs key per project that has iOS devices.

**What FCM and APNs see, in full rather than in outline.** The first draft understated this.
A provider in this path sees: the **payload**; the **registration token**, which is a stable
per-app-installation address; the **project** (sender ID) it belongs to; the **app's bundle ID
or package name**, which identifies the app as Fugaro; the **Firebase Installation ID** (FID)
that FCM assigns; the **IP address** the device registered from and the network it sends over;
**device and OS details** (platform, version, locale, and for APNs the device token and
topic); and hence a **time series per device** — when the phone was reachable, and *from §3.1's
category alone* when runs finish, fail, need answers or bind a budget. Stating it as "a
provider learns a category and a device address" understates it by four fields and a timing
series. **No Firebase Analytics SDK is linked** (`mobile/package.json` declares no runtime
dependency at all), so Google does not additionally receive app-usage events from this build —
which is true of the recommended shape and would stop being true if a later task links one.

**The alternative: Expo's push service.** The app is an Expo app (`mobile/package.json`,
`mobile/app.json`), and `expo-notifications` obtains an Expo push token and posts to Expo's
endpoint, which relays to FCM and APNs. **What Expo sees**: the payload **and** the Expo push
token **and** a mapping from the token to the underlying FCM/APNs token and the project — a
party that is neither the owner nor Google/Apple. Against that: **one relay for every
installation** (which is the multi-installation fix above), the APNs key held once by the
publisher, and no per-project FCM registration for a device the owner does not own.

**Recommendation, revised: FCM direct only where the owner owns every installation's project;
otherwise a relay.** The first draft recommended direct FCM on privacy grounds alone, and that
reasoning is right for a single-installation app and wrong for this one. **Q7 carries the
decision**, with the team-key consequence named. *(The platform claims above — HTTP v1's
per-project token rule, its error classification, the `.p8` key's team scope — were checked
against this repository's own account model, and against Google's and Apple's public pages as
far as they could be read: **UNVERIFIED at the provider**, because both documentation sites
render their content client-side and the sentences could not be extracted. The reviewer's own
note says the same. They are marked here rather than presented as verified.)*

### 3.4 Where the push tokens live, and who can read them

**Recommendation: a SIBLING of the device node — `config/admin/push/$uid` — never a child of
it. Both are nodes of rules generation 3.**

**Why not a child of `config/admin/devices/$uid`, which is what the first draft proposed.**
The device node's **existence is the allowlist**, in the tree rather than on a branch:
`internal/budget/rules/rules.go`'s `userClaim` — merged with #601, `origin/main` at `371038f`
— is
`(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())`,
and the constant's own comment reads "*the child `config/admin/devices/<uid>` existing IS 'this
account is paired to a phone that has not been revoked'*". Deleting that child is `fugaro pair
revoke`'s whole act (D166, P6.6). **So any write that recreates the child re-pairs the
device** — and the first draft put push data under it and named three writers besides
`fugaro pair`: §3.5(b)'s re-registration principal, §3.8's prefs principal, and the sender
"marking the device unregistered" (§6). A re-registration the phone sent *before* a revoke and
the consumer processes *after* it is the everyday version of this: the write recreates
`config/admin/devices/$uid/push/...`, the node exists again, every `USER` read works again, and
**§3.5's "one act, one revocation" is quietly false**. That is a security regression, not a
tidy-up: `devices/**` becomes a routinely written node when its only value is that nothing
writes it but pairing.

**The shape, corrected:**

```
config/admin/devices/$uid/            # D166's node. WRITTEN AND DELETED ONLY by `fugaro pair`
  ...                                 #   / `fugaro pair revoke` (P6.6): mac key name, added at.
                                      #   Its existence IS the allowlist (userClaim, P6.4).
config/admin/push/$uid/               # THIS design's node — a SIBLING, never a child, and
                                      #   ONE PER PAIRED ACCOUNT: `$uid` is one
                                      #   installation's Auth uid and the node lives in
                                      #   that installation's database, so a phone signed
                                      #   into two installations has one node in each
                                      #   (§3.5's unit sentence)
  token                               #   the registration token THAT installation's
                                      #   project issued — exactly one per node (§3.3:
                                      #   FCM rejects a token minted for another project)
  platform                            #   "ios" | "android"
  updated_at                          #   epoch ms, server-written
  prefs/                              #   §3.8's per-category switches and quiet hours
  ledger/                             #   §3.6/§3.7: THE SENDER'S OWN STATE, and its only home —
                                      #   the dedupe keys, the cap window's timestamps and the
                                      #   pending intents §6's "stays queued" retries. Written
                                      #   and read by the sender over IAM; no content (§3.6)
```

**The two rules that make it safe:**

1. **No writer other than `fugaro pair` and `fugaro pair revoke` ever touches `devices/**`.**
   Not re-registration, not the prefs toggle, not the sender, not the reaper, not the app.
   A rules-level statement rather than a convention: `config/admin` stays `"admin": { ".read":
   false, ".write": false }` in `rules.json.tmpl` and its `config/admin/devices/$uid` child
   restates exactly that pair, with no client grant anywhere under it (merged as #601). Nothing
   has to be added for the sibling either: a `push/**` node under the same `config/admin` is
   client-denied by inheritance, and a later task that writes it into the template must keep it
   that way.
2. **The sender sends only when BOTH nodes exist**: `devices/$uid` (the device is still paired)
   **and** `push/$uid` (a registration exists). No device node means no send — revocation is
   still one act, and it is still immediate, because the send-time check reads the node the
   rules also read. A leftover `push/$uid` after a revoke **is inert**: nothing consults it,
   the rules never read it, and it cannot re-admit anybody.

**Who deletes the push node, stated once so §7 and Q10 can cite it rather than assert it.**
There are two deletion paths, and they are not alternatives:

- **`fugaro pair revoke` deletes BOTH nodes, in the same act** — `config/admin/devices/$uid`
  (D166's deletion, which is what stops the reads) **and** the sibling
  `config/admin/push/$uid`. That is what makes the *data's* lifetime bounded by the pairing
  rather than by a timer (§7, Q10(i)): revoke is the one act, and it takes the token, the
  platform, the prefs, the quiet-hours zone and the sender's `ledger` (§3.6) with it. **The
  pairing command writes the DEVICE node only, and it writes the push node not at all** —
  D165's pairing flow has nothing that could carry a registration token (§3.5 (a), read off
  `rules.json.tmpl`), so the push node's first write is Q3's registration principal's, after
  pairing. Revoke is the same admin act that removes both nodes; the writers table below
  states the whole set, because "the pairing command is the only writer of the push node" was
  the first draft's claim and it is false twice over: pairing is not a writer of it at all,
  and §3.5 (b)'s registration principal — which is also §3.8's preferences writer — and the
  sender are.
- **The sender reaps an orphaned `push/$uid` on its next sweep** — a push node whose
  `devices/$uid` is gone. This is the **net, not the mechanism**: it covers a revoke written by
  a principal that removed only the device node, **a re-registration the consumer had in flight
  when the revoke landed — a create, which §3.4's table permits (§3.5 (b))** — and any other
  path that leaves the two nodes out of step.
  It is idempotent with the first path — the node is already gone.

  **So the answer to "does revoke delete the push node?" is yes, and the reaping is why a
  leftover node is harmless rather than why it exists.** Rule 2's "inert" describes the window
  between the two deletions, in which the device node is gone and the push node is not yet:
  **nothing is sent to it and the rules do not read it** — it is a deletion pending, not a live
  registration. The first draft left this split across §3.4, §3.5 and §6 and never said which
  one owned the deletion, which is the ambiguity this paragraph closes.

**Who writes which node, in one table, so no section has to be read for it.** The design names
**two** writers of the push node — §3.5's registration principal (Q3's recommendation (b)),
which is §3.8's preferences writer too, and §3.6's sender, whose only write to either node is
its own `ledger` — and they both write the **sibling**. The device node has exactly one pair of
writers, and they are the same command in its two modes. **Pairing and revoke are the only
writers of `devices/**`, and pairing writes nothing under `push/**` at all**: D165's pairing
session carries no registration token, so there is none for it to write (§3.5 (a)). So no
`devices/**` row may claim a `push/**` write, and no `push/**` row may claim a `devices/**`
one, and none does. **The node is one per paired account (device × installation) and holds
that installation's one registration (§3.3).**

| node | writer | what it writes | what it must never do |
| --- | --- | --- | --- |
| `config/admin/devices/$uid` | **`fugaro pair`** (D165/P6.6) | creates the node, with the pairing's own fields | never re-creates a node `pair revoke` deleted (only a fresh, typed-code pairing does); **never writes `push/**`** — it has no registration token to write (§3.5 (a)), so the rows below are the push node's whole set |
| `config/admin/devices/$uid` | **`fugaro pair revoke`** (D166/P6.6) | **deletes** the node — the one revocation act — **and the sibling `push/$uid` with it** (next-to-last row) | never writes anything before deleting |
| `config/admin/push/$uid` (token, platform, updated_at) — one node per paired account, holding that installation's one registration | **the registration principal** — §3.5 (b), Q3's recommendation, and the **only** registration path: the principal that applies the app's mailbox registration message, holding `roles/firebasedatabase.admin` (the consumer beside the orchestrator where the operator holds the role) | the token and platform from that message — **including a create**, since it is the node's first write: pairing does not make one, and after a revoke this recreates the node revoke deleted (§3.5 (b): that write after a revoke is the orphan §3.4 reaps) | never writes a MAC-less or unvalidated token, and **never writes `devices/**`** — because the admission is pairing's alone, whatever this principal writes leaves a device un-admitted |
| `config/admin/push/$uid/prefs` | **the same registration principal** (§3.8: the controls are written the same way, through the mailbox and (b)) | the per-category switches and the quiet-hours zone name and window | never writes a switch the app did not ask for, and never `devices/**` |
| `config/admin/push/$uid/ledger` | **the sender** (§3.6, §3.7) — its **only** write to either node | the dedupe key of an event it has sent, the cap window's timestamps, and the pending intent it will retry (§6) | never writes a token, a platform or a pref, never writes **content** (§3.6: a device, a category, a timestamp, a state-change key), never `devices/**`, and never **creates** a node whose `devices/$uid` is absent |
| `config/admin/push/$uid` | **the sender** (§3.6) | **deletes** a stale token entry, and deletes the whole node when it is empty or orphaned | never writes a marker, never writes a token, never **creates** a node (§3.4 "reaping deletes; it never marks") |
| `config/admin/push/$uid` | **`fugaro pair revoke`** | **deletes** the whole node in the same act as `devices/$uid` | never writes anything before deleting — like row 2, it deletes and does nothing else |
| **both nodes** | **no client, no run account, no app** | — | any write at all: the rules deny `config/admin` to every client token (§3.4 rule 1, `rules.json.tmpl`) |

**Read off the table, the three sentences every section has to agree with:** *the device node is
written by `fugaro pair` and deleted by `fugaro pair revoke`, and by nothing else*; *the push node
is written by the registration principal — the token, the platform, `updated_at` and, per §3.8,
the prefs — and by the sender's ledger, **never by pairing**, and deleted by revoke and by the
sender's reaping*; *no client, run account or app writes either*. §3.5 (a)/(b), §3.6, §3.7, §3.8
and §6 each state their own row of it.

**Why the table lets the registration principal create the push node, decided from
`rules.json.tmpl` rather than from taste.** The template says exactly one thing about
`config/admin`: `"admin": { ".read": false, ".write": false }`, restated on its
`devices/$uid` child — a denial aimed at **client** tokens, which says nothing at all about an
IAM principal. The registration principal is one: it holds `roles/firebasedatabase.admin`
(§3.5 (b)), and **database admin bypasses the rules**, so no rule can couple `push/$uid` to
`devices/$uid` on the write side. A row forbidding that create would be describing an
enforcement the rules do not have — and it would forbid the node's **first** write too, since
pairing makes none (§3.5 (a)): a push node could then never exist at all. §3.5 (b) also relies
on the write's landing after a revoke — which is the orphan §3.4 reaps. **What makes the sibling safe is therefore not what the rules
do about the write, but what happens on the read and afterwards**: the sender sends only when
**both** nodes exist (rule 2), no client can read either node, and the reaper deletes an orphan —
so a `push/$uid` created after the device node is gone is **inert**, and it can never re-admit a
device, because this principal's write is not itself an admission.

**The ledger's home is the push node and nowhere else.** The sender's dedupe and cap state is
**per paired account** — the node's unit (§3.5): §3.7 keys it on `(device, category,
state-change key)` and caps it per hour, and those rows live in a node that is one per paired
account, so the same phone paired into two installations has one ledger in each and each
installation's sender keeps its own count. It must be unreadable to every client, and its
lifetime should be the pairing's — and
`config/admin/push/$uid/ledger` is the one place that is all three at once: inside the
client-denied `config/admin` (§3.4 rule 1), in the node `fugaro pair revoke` deletes in the same
act (§7, Q10(i)), and with **no second store, no second grant and no Firestore
`roles/datastore.user`** for the sender to hold. §6's *pending intent* lives there too, for the
same reason: it is the sender's own state about a device, and it should die with the pairing
rather than outlive it. **This is stated once, here, and §3.6, §3.7, §7 and Q10 cite it** — the
earlier drafts put the ledger "under `config/admin` **or in Firestore**", which made §7's and
Q10(i)'s "deleted with the push node on revoke" false in one of the two readings and left the
table above silent about the only write the sender ever makes.

**Reaping deletes; it never marks.** A stale token (`UNREGISTERED`) or an orphaned `push/$uid`
whose `devices/$uid` is gone is **deleted** — the token child, or the whole push node. The
first draft's "marks the device not registered" is withdrawn for the same reason as the child
node: a marker write is a write to a node whose meaning is its existence. §6's row says the
same thing now.

**Who can read the push node:**

- **No client can** — it is inside `config/admin`, which `rules.json.tmpl` denies to clients
  (`.read: false`, `.write: false`). The app never reads its own push token, and neither does
  any other phone; the app only ever *hands one over* (§3.5).
- **Over IAM, the same principals that can already read `config/admin`, and no new ones**: per
  `deploy/terraform/gcp/modules/firebase/iam.tf`, `roles/firebasedatabase.admin` to
  `admins` and `budget_admins`, and `roles/firebasedatabase.viewer` to launchers and operators
  (that read **bypasses the rules** — m9 §6.4 and G13's residual, unchanged by this design).
  The **sender** (§3.6) reads the node over IAM with its own service-account identity.
- **The honest sentence:** a push token is readable by every launcher and operator of the
  installation, and by the sender. **It is not a credential** — holding it lets a principal
  send to that device only if it also holds the FCM send credential (§3.6) — and §3.3's
  multi-installation change leaves each *node* belonging to one **paired account** (§3.5's unit
  sentence): it lives in that installation's database, it holds that installation's single
  registration, and **no launcher can read another installation's node at all**: the IAM roles
  above are granted inside one installation, so a phone paired into two of them is invisible to
  the other's principals rather than readable by them.

**The generation.** Both nodes land in **rules generation 3, and that generation is in the
tree**: `origin/main` at `371038f` (read 2026-10-19) carries
`internal/projectpolicy/version.go`'s `RulesVersion = 3`, whose comment reads "*Generation 3 is
P6.4 (mobile plan task P6.4, decisions D161, D165, D166, D169, D171): the mailbox's message
fields as P6.3's schema defines them … the device allowlist `config/admin/devices/<uid>` that
USER now requires (D166), and the two pairing nodes of D165*"; and `userClaim` in
`internal/budget/rules/rules.go` carries the device conjunct quoted at the top of this section.
**#601 (`fugaro/20261018-151654-3ddb`, head `14e5ff4` when this document first read it) has
merged as that generation**, so the file names above are read from the tree and not from a
branch. **A push store does not need a generation of its own**: it is a sibling inside
`config/admin`, whose rules that generation already rewrote. **The one part of the design that
does need a later generation is the registration message's field, not the store** — §3.5 (b)
asks for a bounded registration kind, because the mailbox node refuses every field it does not
name — and the plan's P5.2 dependency row records that prerequisite beside P6.4 and P6.6.

### 3.5 Rotation and revocation

**Revocation is the device node's deletion, and it is immediate — and it is still the only
revocation act.** Because the sender checks `config/admin/devices/$uid` at send time (and
§3.4's rule 2 makes that check one of *two* it must pass), **no device node means no send**;
and because D166's `USER` requires the node, the same deletion also stops the app's reads at
once (G14's property). One act, one revocation — **and the act deletes the sibling push node
too** (§3.4: `fugaro pair revoke` removes `config/admin/devices/$uid` *and*
`config/admin/push/$uid`, so the stored token, prefs and zone do not outlive the pairing; the
sender's reaping of an orphaned node is the net for a revoke that removed only the device
node, not a second mechanism). **§3.4 is what keeps that true rather than
merely asserting it**: the token lives in a *sibling*, so no re-registration, prefs write or
reap can recreate the node the rules consult — an orphaned `push/$uid` is inert and gets
deleted, never resurrected into an admission. This is the design's strongest property, and it
survives only in the sibling shape.

**The unit, stated once so no section states it differently: the node is per paired ACCOUNT —
one device × one installation — and holds that installation's single registration.**
`config/admin/push/$uid` is keyed by the uid D166 paired **in that installation** (`m~<random>`,
`mu = uid`), and the node lives in **that installation's** database, so §3.3's
multi-installation phone has **one node in each installation it is paired into** and no node
holds two installations' tokens: a token is meaningful only to the project that issued it
(§3.3), and one installation's principals cannot read another's database at all. **(§3.3
states the requirement, §3.4's diagram draws the shape, the writers table and Q2 name the
node; this is the sentence they all agree with.)**

**Rotation is the harder half, and it is an open question rather than a solved one (Q3).** Push
tokens rotate: FCM and APNs issue new registration tokens on reinstall, on restore, on
app-data clear, and occasionally otherwise, and FCM reports a stale token back as
`UNREGISTERED`. Three shapes are available:

- **(a) Register at pairing, and let the sender reap stale tokens — the registration half is
  withdrawn, the reaping half is not.** **D165/P6.6's pairing flow cannot carry the phone's
  registration token to the CLI, and that is a fact about the deployed rules, not a
  preference.** Read off `rules.json.tmpl` (generation 3, merged as #601), the pairing subtree
  admits exactly this and nothing more: `pairing/$uid/request` is
  `".read": false, ".write": "PAIRING && newData.exists() && !data.exists()", ".validate":
  "newData.hasChildren(['key', 'at'])"` with `key` a public key and `at` a timestamp, a
  `"$other": { ".validate": false }` catch-all **refusing every other field**, and
  `pairing/$uid/delivery` is `".read": "PAIRING", ".write": false` — the CLI writes it, the
  phone reads it. So the session's one client write carries the phone's **public key** and
  nothing else: `internal/budget/rules/emulator_pairing_test.go` proves it, asserting that a
  request carrying a token is **denied** ("a request carrying a field the node does not have"),
  and that file's own comment says the request "carries no secret — the key is public by
  construction, and the token and the MAC key come back encrypted to it", where **that** token
  is D165's `m~` custom token, not an FCM registration token (§2 keeps the two senses apart).
  Nothing in D165's flow — QR, typed code, delivery — ever sees the phone's registration token,
  so `fugaro pair` has nothing to write as a registration and **does not write the push node**
  (§3.4's table). *(Making the request node admit a bounded `token` field would be a rules
  change — a later generation, not this one — and the design does not ask for it: it would buy shape (a)'s
  one-shot registration, which is stale for exactly the case that matters, at the price of a
  change to the one node whose "carries no secret" property D165's one-shotness rests on. The
  other reading, "the phone writes the token itself after pairing", is (c) below, and is
  rejected.)* **What survives from (a) is its second half, and it is not optional in any
  shape:** the sender **deletes** a token FCM reports `UNREGISTERED` — the token child, or the
  whole push node (§3.4: reaping never writes a marker) — leaving the device unregistered until
  the app registers again. That reaping is the net, not a registration path.
- **(b) Registration over the authenticated mailbox — the only shape that can carry the token,
  and therefore the design's only registration path.** The app, on next open — which includes
  the first open after pairing — writes its current token into the account's inbox; a
  principal holding `roles/firebasedatabase.admin`
  applies it to `config/admin/push/$uid`, creating that node on the first such message. No new
  grant to the app, no new write path for the phone, and it reuses the mailbox the design
  already trusts. **It is not a choice between equals: (a)'s registration cannot be built at
  all, so the push node's existence depends on this message** — which is why §3.4's table gives
  this principal the create and why the plan's P5.2 records the consumer as a prerequisite.
  **That consumer is a named task**: it is **P6.7**, the task that reads the account's inbox
  in the `fugaro mailbox` consumer P4.1 builds (the plan names the file at P4.1 and P6.7; it
  is **not in the tree yet**), and applying a registration message is one more thing it does
  with a message it has already verified.
  **But read the message node before promising that this costs nothing, because the mailbox is
  as closed as the pairing node was.** `mailbox/$mb/inbox/$msg` in `rules.json.tmpl` names
  exactly `id`, `sender`, `text`, `at`, `mac`, `agent`, `kind`, `options`, `reply_to` and
  `action`, ends in `"$other": { ".validate": false }`, and the schema closed its list from
  the other side too: `shared/mailbox-schema/index.js`'s `FIELDS` is annotated "any other
  field is refused". **The two closed lists are not identical, and the difference is worth
  stating rather than glossing as "the same":** `FIELDS` has **eleven** members where the
  inbox names ten — its extra one is **`acked`**, which `rules.json.tmpl` puts on the
  **outbox** node only (the phone's write on a message the consumer wrote, §6.2/D113) and the
  inbox's `$other` refuses. The validator has no direction check, so it accepts `acked`
  wherever a message carries it and the rules do not; that one member aside the lists agree.
  **`token` is in neither**, so a message carrying a `token` field is refused **by the rules
  and by the validator** — `internal/budget/rules/emulator_mailbox_test.go` proves the rules'
  half with a named field the inbox does not have ("a message carrying the unknown field
  `acked`", and `expires_at` beside it).
  **So today the only field that can carry a registration token is `text`** (≤ `MaxText =
  4096`, rules.go), and the design **does not recommend** that: `text` is the mailbox's
  human-and-agent channel, and a consumer that decides "this message is a registration" by
  pattern-matching free text is one a launcher- or user-authored message can imitate. **The
  design's recommendation is a bounded field instead — a registration kind with a required
  `token` — and that is a change to `shared/mailbox-schema/index.js` (the kind vocabulary and
  the (kind, field) table) and to `rules.json.tmpl` (the `kind` enumeration and the new child),
  i.e. a later rules generation and its own task; the plan's P5.2 dependency row records it.**
  *(The mailbox schema's own comment says why the change is a version change and not a
  formality: "the field set IS the version", and a message from a future protocol carrying an
  extra field "is refused by the rules **and** here rather than silently accepted". This
  design asks for exactly that deliberate change, rather than smuggling a token through
  `text`.)* **And the same bounded field carries §3.8's switches**, which are written the same
  way (§3.5 (b), again): one message from the app carries the token, the platform and the
  prefs, so the kind this design asks for is the app's **registration-and-preferences**
  message, and §3.8's controls stand under the same prerequisite rather than pretending a
  quiet-hours zone fits in `text` either. **The premise is conditional and
  this document states it as such rather than as a fact:** §3.4 records that, per
  `deploy/terraform/gcp/modules/firebase/iam.tf`, `roles/firebasedatabase.admin` is held by
  **`admins` and `budget_admins` only** — the very reason reconciliation G13 and D166 call
  **pairing itself an admin act**. So where the operator holds that role, the consumer beside
  the orchestrator applies it directly; **where the operator does not, re-registration is an
  admin act in the same sense pairing is**, and it is not something this design can promise for
  free. **And the write lands on the push node, never on `devices/**`** — which is what makes
  (b) safe to run on a timer and after a revoke: a re-registration message the phone sent
  before the revoke and the consumer processes after it **creates** `push/$uid` — the node
  revoke deleted, and §3.4's table records that this principal may create it, because it writes
  with `roles/firebasedatabase.admin` and the rules deny only clients — a node the rules do not
  read and the sender does not send to while `devices/$uid` is gone. (a)'s surviving reaping is
  the net, and that orphan is exactly what it is for; **Q3 therefore asks the owner one
  question, not two — whether the operator holds the role this path needs — because the other
  registration path does not exist**.
- **(c) Let the app write its own push node.** A rules grant for the phone to write
  `config/admin/push/$uid/**` under the `MAILBOX` identity. Cheapest for the user and **the
  one shape this design rejects**: it reopens `config/admin` to a client write, which D166
  deliberately closed — and it would give a phone a write path into the family whose
  client-denial is what makes the device node trustworthy. *(This is the shape the first draft
  rejected as a child of `devices/$uid`; the sibling node makes it less fatal but no less
  rejected.)*

**Recommendation: (b) — the only registration path there is, with the bounded registration
field it needs — and (a)'s reaping as the safety net.** Q3 asks the owner.

### 3.6 What writes a push

**The producer must not send, and the sender must not be the operator's machine.** The brief
is explicit that push **"must not need a standing credential on the operator's machine"**, and
the tree supports that: every scheduled cloud component today runs under an **ambient service
account identity with no key anywhere** (`internal/cli/history.go`: "the account's identity is
its only credential"). So the FCM send credential is an **OAuth2 access token minted from a
service account's identity**, never a downloaded key and never a secret on a laptop.

The candidates, priced:

- **(A) The producer sends, in its own process.**
  - *The cap categories*: the gateway at a threshold (`internal/gateway`'s `Server`) decides
    the budget case in-process and calls FCM directly. Lowest latency, and the event is in
    hand. *(The runner at finalize, `internal/runner`'s `finalize`, is the producer a
    "finished" or "failed" notification would need — and those are **not** in §1's reconciled
    set, so (A) is priced here for the categories that exist, not for the pair the owner
    declined.)*
  - *Cost*: it widens **every run's job account** with the FCM send permission — the same
    account that, under project-centric phase 3, is **shared between repositories**
    (budget-token-channel.md §2). A push-send grant on a shared job account is a grant every
    run of the profile holds, and a compromised run could then notify the owner of a
    fabricated "needs your answer" — exactly the phishing surface §5 attacker 2 bounds and
    cannot close, and here it would be authored by the installation's *own* run rather than by
    an outsider. **Reject for the job account.** *(The first draft said "the console's D33
    already rejected the analogous widening for the desktop" — **D33 says no such thing**.
    D33 rejected a **push service**: "a new service, a new credential and a notification that
    leaves the machine", with the notification's *content* limited to "facts a document
    carries, never a string from a hostile field". It is cited here for that content rule,
    which §4 rule 1 copies, and not for a job-account argument it never made.)*
  - *The one place (A) is defensible*: the **gateway**'s threshold case, where the budget
    decision is made in-process — but the gateway shares the run's container and identity, so
    it inherits (A)'s cost. **Reject.**
- **(B) One cloud sender that derives the events itself.**
  - *Recommended.* A single cloud component **derives** what to send from state it already
    reads, and calls FCM. **No producer writes anything** — and that is the point, because
    the run-side producers (the runner at finalize, the gateway at a threshold) are exactly
    the accounts (A) refuses to widen. The natural home is the **history job**
    (`history.tf`'s `google_cloud_run_v2_job.history`), which already runs every 15 minutes
    under `fugaroHistory` and already holds `roles/firebasedatabase.admin` on the project's
    database — `deploy/terraform/gcp/modules/firebase/iam.tf`'s `google_project_iam_member.history_database`,
    whose own comment is "only this account (and no job account) holds one on the FP". It
    reads the spend and kill nodes and the mailbox's outbox — the sources §1's reconciled
    category set has — and it gains exactly one thing: the FCM send permission, held by
    **one account**, not by every run. *(It also reads
    the run registry — `internal/budget/registry.go`'s entries — for its own sweep; this design
    does **not** turn that into a category. "Finished" and "failed" are not in the reconciled
    set (§1, D160), so a sender that derived them from the registry would build the category
    the owner declined.)* Keeping the
    sender off the run accounts is not a nicety: because a push has no sender signature
    (§3.9), any account that can send can lie on the lock screen.
  - *What that account may hold, named exactly.* The permission is
    **`firebasecloudmessaging.messages.create`**, granted through a **custom role** on the
    Firebase project — not `roles/firebasecloudmessaging.admin`, which carries topic and
    subscription management this sender never needs (`deploy/terraform/testdata/custom-role-permissions.txt`,
    in the tree — see §2 — is the permission catalogue the custom roles are read against, and it lists
    `firebasecloudmessaging.messages.create` beside the `topicSubscriptions.*` set the design
    does not want). The **FCM API must be enabled** on the project
    (`firebasecloudmessaging.googleapis.com`), which is a change `apis.tf` does not carry
    today. And the sender must take **nothing from its arguments or environment** — no
    message text, no target, no token — because the **scheduler account holds `run.jobs.run`
    *and* `run.jobs.runWithOverrides`** on the history job
    (`deploy/terraform/gcp/modules/installation/history.tf`'s
    `google_cloud_run_v2_job_iam_member.history_runner`, whose comment says "which also need
    run.jobs.runWithOverrides, and roles/run.invoker lacks it"): whoever can override that
    job's environment can steer anything the sender reads from it. The sender derives every
    field from the database instead (§3.6(B)) — which is what "no producer write" buys, and
    why an env- or arg-driven sender would be a different, weaker design. **Whoever can change
    the history job's image controls the sender**, and that is the same trust as controlling
    the sweeper today.
  - *The "one account" claim, corrected.* "Exactly one account *gains* the permission" is
    true. **"One account is the smallest number of places that power can live" was an
    overclaim and is withdrawn:** project **owners and editors** hold FCM send through basic
    roles, and an owner's application-default credential on a laptop can send. *(Not verified
    against live IAM — the reviewer could not verify it either; it is stated as the likely
    shape of the basic roles, not as a checked fact.)* The design's claim is the narrow one:
    no **run** and no **operator's machine** gains anything, and the installation's send
    permission is a custom role on one service account.
  - *Where the sender's own state lives*: a **dedupe and cap ledger**, written **by the
    sender's own admin identity** and read by it, in **`config/admin/push/$uid/ledger`** — the
    push node of the device the entry is about, §3.4's node diagram and writers table carrying
    it. It is inside `config/admin`, which `rules.json.tmpl` denies to every client
    (`"admin": { ".read": false, ".write": false }` — no client read or write, ever), it needs
    **no second store and no second grant** (the sender's existing
    `roles/firebasedatabase.admin` covers it, §3.4 "Who can read the push node"), and it is
    deleted with the node on revoke (§7, Q10(i)). **No run and no phone writes it**, so this
    design widens no run account; and the rows carry **no content** — a device, a category, a
    timestamp and the state-change key, §3.1's rules applying to the ledger as much as to the
    payload. *(The earlier draft's "under `config/admin` **or in Firestore**" alternative is
    withdrawn: a second store would have made Q10(i)'s "deleted with the push node on revoke"
    false and left the ledger outside the one place §3.4's table accounts for.)*
  - *Latency*: the 15-minute sweep is the floor, and for "needs your answer" it is tolerable
    but not always — a prompt expires at D118's 72 hours. **The only way to beat the sweep is
    to let a run-side producer poke the sender**, and the honest price is that this needs
    `run.jobs.run` on the sender held by a run's account — **the same class of run-account
    widening (A) is rejected for**, not a free refinement of (B). Q1 puts that trade to the
    owner as what it is: sweep-only (no widening) against on-demand (a run-account grant).
- **(C) A new always-on service.** A Cloud Run service with a socket that a producer pokes.
  **Reject as this design's invention**: it is a standing server this repository has never
  deployed, and it would be P5.1's orchestrator's problem, behind D55–D78.
- **(D) The orchestrator of P5.1, when it lands — the natural low-latency sender.** *(Added
  after the review, which is right that (C) was dismissed too fast: the service is not this
  document's invention, it is a sibling design already written.)* **PR #632**
  (`fugaro/20261019-001437-eb9c`, `docs/mobile/design/always-on-orchestrator.md`) proposes
  exactly the component (C) waves away: **one Cloud Run service per installation, in the
  installation's own GCP project, `min-instances = 0`, woken by a Cloud Scheduler tick every
  minute**, with a dedicated service account holding the launcher's grants and no model
  credential. If that lands, it **is** the sender this design wants, and better: it is the
  prompt's own author (so "needs your answer" needs no re-derivation), it holds
  `roles/firebasedatabase.admin`-class reach already, and a per-minute tick makes (B)'s 15-minute
  sweep unnecessary. **The cost is the same shape (A) is rejected for** — a resident component
  with send power — bounded by being **one** account per installation rather than every run's,
  which is §3.6(B)'s own argument applied to a bigger process. **Q1 now carries (D) as the
  option the owner should take if phase 2 is approved**, and (B) remains the recommendation
  only for the case where phase 2 is not built.

**Recommendation: (B), sweep-only, with (D) named for the case where P5.1 lands.** One sender,
one credential, ambient identity, **no producer write and therefore no run-account widening**;
latency is bounded by the sender's schedule. The one account the design widens is the sender's,
with the FCM send permission (`firebasecloudmessaging.messages.create`) and nothing else.
**"No key on a laptop" is not a claim this design makes** — the sender needs no downloaded
key, but owners and editors can send with an ordinary application-default credential, as the
paragraph above says. Q1 records the latency/grant trade and (D).

### 3.7 Rate limiting and dedupe

- **One notification per state change.** The key is the *transition*, not the event: a cap that
  is still reached on the next sweep sends one "cap reached", not one per sweep; a prompt
  sends one "needs answer", not one per outbox re-delivery (the consumer already re-delivers
  until the ack, `shared/mailbox/index.js`'s dedupe ledger `state.seen`). The sender
  dedupes on `(device, category, state-change key)` over a window, so a re-derived event
  is not a second notification. The window and the keys are rows in
  that paired account's own `config/admin/push/$uid/ledger` (§3.4, §3.6 — the ledger's one home), written by the sender
  and by nothing else.
- **A storm cap.** At most **N notifications per paired account per hour** (N is small — the
  design suggests **6**, and Q4 asks), and at most **one per category per run per hour**. *(Per
  paired account, not per phone: the count is a row in the node, and the node is one per
  paired account — §3.5's unit sentence.)* Ten runs of
  one repository crossing their cap in a minute produce one alert and a quiet count, not ten
  buzzes. The
  cap is enforced **at the sender**, because the sender is the single writer and the phone
  cannot enforce anything about its own buzz — the window's timestamps are the same
  `ledger` rows, so the cap and the dedupe share one store and one lifetime (Q10(ii)).
- **The cap is a floor on nuisance, not a loss of information.** Everything a push would have
  carried is in the mailbox regardless — the push is a doorbell (§3.2) — so a suppressed
  notification costs nothing but a buzz.

### 3.8 User controls

- **Per-category off switches**, one per §1 category, stored in the **push node's** `prefs`
  child (§3.4 — the sibling, never `devices/**`). Off means the sender does not send that
  category to that device — the switch is honoured **server-side**, because a client-side
  switch cannot stop a push from reaching the OS and the provider.
- **Defaults, stated rather than left open.** **Push is opt-in per device and per
  installation**: no registration exists until the phone registers one, so a device that never
  does is never sent to, and §6's first row ("no token, no send") is the default state rather
  than a failure. **Each category switch defaults on** for the categories D160 leaves
  interrupting and **off** for the ones it keeps off by default — cap-near, the one D160
  answers "available, off by default" — so the default set is D160's own answer and not a
  new one (§1, Q4). **Quiet hours default off** (below).
- **Quiet hours**, a window per paired account, server-side, stored in `prefs`. This is the design's
  one uncomfortable privacy surface and it should be named rather than buried: honouring quiet
  hours server-side requires knowing the device's **zone**, which is a location-adjacent datum
  the app otherwise never sends. The alternatives are worse — a client-side filter still buzzes
  and still reaches the provider — or the feature is not offered. **Recommendation: store the
  IANA zone name and the window, default off.** *(The first draft proposed "an explicit UTC
  offset … never a timezone name", and that is wrong in a way worth recording: a stored offset
  is a **fixed** offset, so it silently shifts by an hour at every DST transition — the window
  drifts onto the wrong hour twice a year, and the failure is invisible. A zone name is one
  string, it is the datum the OS itself holds, and it is what makes the window mean what the
  user set. Q5 asks, and now asks the right question.)*

**How the controls are written** is Q3's rotation question in another guise: the app cannot
write `config/admin` (§3.4), so the switch is written either by the CLI (an admin act, awkward
for a toggle the user expects to flip in the app) or by the app through the mailbox and a
principal holding `roles/firebasedatabase.admin` (§3.5's (b), **conditional on that role
exactly as (b) is**). **Recommendation: (b) again** — the mailbox is the app's only write
path, and this design does not add a second one; where the role is not held, the switch is an
admin act like pairing, and this document does not pretend a toggle in the app is free of it.
**The message is §3.5 (b)'s bounded registration-and-preferences kind, not a new one**: the
controls, the quiet-hours zone and the platform ride back with the token, so this section
inherits §3.5 (b)'s prerequisite (the rules and schema change that gives the message its
field) instead of assuming a field the inbox does not have.
**Every one of these writes lands on `config/admin/push/$uid` and never on `devices/**`**
(§3.4 rule 1).

### 3.9 The phone's side: what it does with a push it did not expect

§3.1's content-free payload is a rule the **sender** obeys. The phone cannot rely on it, and
this subsection is where the design says so instead of letting §3.1 imply a guarantee the
channel does not give. Three facts bound what the phone can do:

- **The OS will not tell the app who wrote the notification.** iOS and Android deliver a
  remote notification from the installation's FCM project identically whether the owner's
  sender or an attacker holding the same credential sent it; there is no sender signature on
  a push, and this design does not pretend otherwise.
  **The lock screen is not an authenticated surface, and it cannot be made one.** A forged
  push's title and body are shown to the user by the OS, in the attacker's words, before any
  Fugaro code runs.
- **The app's own screen is the authenticated surface, and it is built from the mailbox.**
  Tapping a push opens the app; the app signs in and renders **only** what it reads from its
  own authenticated mailbox and budget families under the claims it already holds (§3.2). A
  lure in a notification's title has no path into that screen: it is not stored, not rendered
  as state, and not quoted.
- **The app composes its own alert text — and that text obeys the same content rule as the
  payload.** The recommended shape is a **data-only** push — the payload is a category (and,
  under Q6 *(ii)*, an opaque id) with no display text — and the app builds the user-visible
  notification **locally** from state it resolved through the mailbox. §4 rule 1 binds only the
  *payload*, so the first draft left the app's own words unbound, and that is a hole rather
  than a detail: nothing would have stopped the app writing "PR ready: owner/repo — fix auth
  bypass" on a lock screen, which is the bystander leak rule 1 exists to prevent, moved from
  the provider to the app. **§4 rule 1 now binds both**, and the app's text is **generic per
  category** and built from **no free-text mailbox field** (§4 rule 10) — console v2's D33 is
  the precedent, in its own words: a notification's content is "a fact from a document and
  never a string from a hostile field". **And an id the app cannot resolve is ignored** — an
  unknown id produces no alert at all. *(Under Q6's recommended option (i) the payload carries
  no id, so "unknown id ignored" protects nothing by itself; the rule that does the work in
  that shape is §4 rule 11: a local alert is posted only when the mailbox shows **a new,
  MAC-verified change of that category since the last one the app saw**. Both shapes are
  stated so that whichever the owner picks has a rule rather than an assumption.)*
- **On iOS, "wherever the platform allows" is nearly nowhere, and this document says so.** A
  **data-only** push on iOS cannot display text by itself; a silent `content-available` push is
  **throttled by the system, is not delivered after the user force-quits the app**, and is
  explicitly best-effort. App-composed *visible* text on iOS therefore needs a **Notification
  Service Extension** (`mutable-content`), which runs **before** the app and must therefore be
  able to read what it needs on its own. That extension authenticating means sharing the
  **ID token and the per-pairing MAC key** with it through a **keychain access group** — which
  **touches D111**: tension (b2) resolved that the phone holds exactly two items per account
  "in these words and no wider ones", and a second process reading them is a widening of where
  they may be reached, not just where they are stored. **Q6 now asks this**, with the
  alternatives (a visible generic payload instead of local text; a publisher relay that renders
  the notification server-side; or iOS notifications off, which is the honest baseline).
  Android has no such constraint (a foreground service or the app's own notification builder
  covers it), so a design that reads "data-only everywhere" is an iOS-only problem wearing a
  cross-platform sentence.
- **Nothing in the notification is a route or an input.** No notification **category** or
  **action button** (`UNNotificationCategory` on iOS, Android notification actions) and **no
  inline reply**: a forged push can name a category the app registered and put a real
  "Approve" button on the lock screen, wired to app code — so the app registers **no**
  actionable category, and §4 rule 3's "never an action" covers the button as much as the tap.
  **No routing by payload** either: `click_action`, `fcm_options.link` and `data.url` are the
  common deep-link patterns and every one of them lets a forged push choose where the app
  lands, so the payload carries no URL, no deep link and no target. **And no background
  handler writes anything on receipt** — no `acked`, no seen-marker, no token refresh, no
  write of any kind: a handler that writes gives a forged push a write primitive on the
  device's own mailbox. §4 rules 3, 8 and 12 state these as requirements.

**What the user is told, and it is part of the design rather than a footnote:** *a push is
only a doorbell — verify in the app.* The app says it where it can (the notification channel's
own description, the app's first screen after a tap, the pairing screen), because the one
control that works against a forged push is the user's habit of opening the app rather than
acting on the banner. This is also why §4 rule 3 is absolute: the moment a push could carry a
confirmation, a forged push would carry one too.

**Residual risk, stated plainly and not argued away — and made larger by the review, not
smaller.** An attacker who holds the installation's FCM send credential **can** buzz the owner's
phone with a false, urgent-sounding message of their choosing, at a time of their choosing, and
no part of this design prevents it; the app's screen shows the truth only *after* the owner
taps. **The first draft then said the app's screen is "the truth". For part of the state it is
not, and the correction is §5 attacker 2's:** every principal that holds or would hold the send
permission in this design **also holds `roles/firebasedatabase.admin`** on the Firebase
project — the history account (`iam.tf`'s `history_database`), admins and budget admins
(`local.admins`), and project owners and editors — and **database admin bypasses the rules**.
The rules check only the **`mac` field's shape** — and the symbols that do it are **in the tree
now**, in the generation #601 merged: §5's "*the rules cannot verify a tag*" paragraph reads them
out of `internal/budget/rules/rules.go` at `origin/main`'s **`371038f`** (read 2026-10-19), with
the comment that says so. What matters here is the consequence: the rules can require a
well-formed tag and nothing more. So:

- **For mailbox state, the only bound is the app verifying the MAC with the per-pairing key**,
  and §4 rule 11 makes that a requirement: **no mailbox-derived state is shown, and no alert is
  posted from it, before the app has verified the `mac` over the canonical bytes** (P6.3's
  `canonicalBytes`, D169). A sender that is a minter can write anything it likes; the key is
  what the phone can check and the database cannot.
- **The budget families carry no MAC at all** (`config/kill/**`, `config/caps/**`,
  `spend/**` are numbers and flags, not signed messages). A compromised sender can therefore
  forge **both** the push and the in-app state the push points to, for the budget category.
  **For that category the app's screen is not "the truth"**, and §5 attacker 2 says so.
- **The realistic holder of attacker 2's capability is the sender account itself** — the most
  likely way to get the credential is to compromise the component that holds it — so the
  residual is not "some outsider with a stolen key" but "whoever controls the sender", which is
  §3.6's whole reason for making the sender one narrow account and not a run.

The fully-closed alternative is not to have push at all, which is what §10's A and B and Q9
already offer; the mitigation here bounds the **harm**, not the **lie**.

## 4. What a push must never carry, and what it must never do

This is the document's spine. Each line is a requirement, not a preference.

1. **Never content — and this binds the app's own words too, not only the payload.** No run
   title, repository name, owner/name, slug, branch, PR number, error text, log line, code,
   file path, model name, spend figure, cap, amount or currency — **in the payload (§3.1), in
   the sender's fallback title, and in the text the app composes locally (§3.9)**. The first
   draft bound only the payload, which would have moved the bystander leak from the provider
   to the app. The locally composed text is **generic per category**, and it is posted with
   **private lock-screen visibility** — Android `VISIBILITY_PRIVATE` with a redacted public
   version, iOS's hidden-preview placeholder — so that a visible notification is a category
   and not a sentence about the owner's work.
2. **Never a secret or a credential** — no token, no MAC key, no claim, no capability, nothing
   from `mobile/src/accounts/secret.js`'s set.
3. **Never a confirmation, and never an action.** A push **cannot** carry an answer, a confirm
   id, an action record, or anything a consumer (`fugaro mailbox`'s action path, P6.7) would
   accept as a decision; and **receiving one must never cause the app to submit anything**.
   The D128 amendment's whole enforcement is a typed, MAC-verified action record that the
   consumer checks (D169, D171) — a push that could stand in for one would undo it. This is
   the rule the brief states as "a push must never carry or trigger a confirmation, only open
   the app", and it is absolute. **"An action" includes the notification's own affordances**:
   the app registers **no notification category and no action button**
   (`UNNotificationCategory`, Android notification actions) and **accepts no inline reply**, so
   a forged push cannot name a registered category and put a working "Approve" on the lock
   screen (§3.9).
4. **Never the only delivery path.** The mailbox is authoritative; a push is best-effort.
   Nothing may be *lost* by a push's failure, because nothing is *carried* by it.
5. **Never a write path to the operator's machine.** The sender runs in the cloud under an
   ambient identity; no credential lives on the laptop (§3.6).
6. **Never a widening beyond one sender.** Exactly one account gains the FCM send permission
   (§3.6 rule (B)); no run's job account gains it.
7. **Never a lock-screen leak *from this sender* — and never a claim that the lock screen is
   safe.** Because (1) holds, **this design's** pushes put a category and nothing else on the
   lock screen. That is a statement about the sender, not about the channel: a push an
   attacker writes is attacker-authored and can display any text at all (§3.1, §5 attacker 2),
   and the OS cannot tell the two apart (§3.9). The rule binds what we write; §3.9's "only a
   doorbell" is what covers what an attacker writes.
8. **Never trusted input.** The app reads the payload for a category (and, under Q6 *(ii)*, an
   opaque id) and **nothing else**: no field of it becomes state, becomes a link, or becomes
   rendered text. **It is never a route**: no `click_action`, no `fcm_options.link`, no
   `data.url`, no deep link, no target chosen by the payload — the app opens on its own screen
   and nowhere a push names (§3.9). An id it cannot resolve through the mailbox is ignored and
   produces no alert, and the user-visible text is composed by the app from the mailbox rather
   than taken from the push, wherever the platform allows.
9. **Never an authenticated surface.** The lock screen and the notification shade are not the
   app and cannot be made trustworthy by anything this design does (§3.9); the app's own
   screen, behind sign-in, is the only place the truth is shown — **and, for the budget
   families, that screen is not authenticated either** (§3.9's correction: they carry no
   `mac`, so a sender that is also a database admin can forge them). Any user-facing text
   about push says so — "a push is only a doorbell; verify in the app".
10. **Never a sentence built from a hostile field.** The app's locally composed text (§3.9) is
    drawn from a **closed enumeration of generic category phrases** and from **nothing else**
    — never a prompt's text or its options, never an agent's or a repository's name, never a
    free-text mailbox field, which are consumer- or agent-authored and hostile by D118's own
    read-side ruling. Console v2's **D33** is the precedent and its words are the rule: a
    notification's content is "a fact from a document and never a string from a hostile
    field".
11. **Never state, and never an alert, before the MAC is verified.** No mailbox-derived state
    is shown and no local alert is posted from it until the app has verified the message's
    **`mac`** over P6.3's `canonicalBytes` with the per-pairing key (D169, D171, P6.7) — the
    bound the database cannot enforce (`rules.go`: the rules check the tag's length and hex
    shape, "never the value"). In Q6's recommended shape, with no id in the payload, the alert
    is posted **only when the mailbox shows a new, MAC-verified change of that category since
    the last one the app saw** — that, and not "unknown id ignored", is what makes the
    doorbell mean anything.
12. **Never a write on receipt.** No background push handler writes anything — no `acked`, no
    seen-marker, no token refresh, no mailbox write of any kind (§3.9). A handler that writes
    hands a forged push a write primitive on the device's own mailbox.

## 5. The threat model, with the attacker's capabilities stated

**Attacker 1 — a compromised push token.** *Capability*: the attacker knows a device's
registration token. *What it buys*: **nothing on its own.** A registration token is an
address, not a credential: sending to it requires the **project's FCM send credential**, which
the attacker does not have. Knowing the token lets an attacker who *already* holds that
credential skip one lookup. *What this design does about it*: the token is not readable by any
client (§3.4), it is deletable with the device node (§3.5), and it grants no read of anything.

**Attacker 2 — the FCM send credential.** *Capability*: the attacker can send an arbitrary
notification to any device of the installation, with an arbitrary payload and arbitrary title.
This is the **real** push-specific adversary, and it is worth stating without softening: a
compromised FCM credential can make the phone buzz with anything. *What it buys*: a buzz, **an
arbitrary title and body on the lock screen**, and a record in the provider's and OS's logs.
**The spoof is not content-free.** §3.1's content-free payload is a property of what *this
design's sender* writes, not of the channel — an attacker holding the credential authors the
payload, so "your run needs approval: tap to confirm" is fully available to them, and the OS
displays it in their words before any Fugaro code runs (§3.9). Anyone who reads §3.1 as a
security property of push has read it wrong; this paragraph is the correction.

*What it does not buy* — **and the first draft overclaimed two of these three, so they are
stated here at the strength they actually have**: **it cannot make the app act** — tapping
opens the app and nothing else, there is no confirmation, cancel, merge or answer reachable
from a notification, and the app registers no action button or inline reply for a forged push
to borrow (§4 rule 3, §3.9); it **cannot choose where the app lands**, because the payload is
no route (§4 rule 8); and it **cannot override the per-pairing MAC** for mailbox state, because
the app verifies the tag with the key (§4 rule 11). But — the correction the review is right
about — **"it cannot forge mailbox content" and "it cannot make the app show the lie" are
overclaims as written**, because **every principal that holds or would hold the send
permission in this design also holds `roles/firebasedatabase.admin` on the Firebase project**,
and database admin **bypasses the rules**:

- the history account (`deploy/terraform/gcp/modules/firebase/iam.tf`'s
  `google_project_iam_member.history_database`, "only this account (and no job account) holds
  one on the FP");
- `admins` and `budget_admins` (`local.admins`);
- project owners and editors, through basic roles *(the reviewer's finding, which this document
  accepts as the likely shape of the basic roles and does not claim to have checked against
  live IAM)*.

**The rules cannot verify a tag — the symbols, the commit and the quote.** Stated precisely,
because the first draft of this correction cited symbol names that did not exist on `main` at
the time, and that is exactly the kind of citation a later implementer cannot check; the
symbols **are in the tree now**, so this cites the tree rather than a branch:

- **In the tree** — `origin/main` at **`371038f`**, read 2026-10-19, where the file arrived with
  **#601 merging** (mobile plan P6.4) — `internal/budget/rules/rules.go` carries
  `MaxMacLength = 64`, whose comment is: "*MaxMacLength is the length of a message's or a
  heartbeat's MAC, in hex characters: 32 bytes of HMAC-SHA-256 (§3.5, D169, D171, O7). The rules
  check the length and the lowercase-hex characters (patternHex), never the value: verifying a
  tag needs the key, which the database never holds. TestMailboxRulesAgreeWithTheSharedSchema
  holds the number to the schema's MAC_MAX.*" The same file defines `patternHex`, the regex
  `/^[0-9a-f]+$/`, and the `MACOK` macro (which `rules.json.tmpl` applies to every message's and
  the heartbeat's `mac` child, three times) expands to
  `(newData.isString() && newData.val().length == 64 && newData.val().matches(/^[0-9a-f]+$/))`,
  with the comment "*A tag's shape: a string of exactly MAC_MAX characters, every one of them
  the schema's lowercase hex (HEX_PATTERN). The characters are checked; the *value* never is,
  and cannot be: verifying a tag needs the key, which is D171's and never passes through the
  database, so the rules can require a well-formed tag and nothing more (P6.7 is what verifies
  one).*" *(When this document was first written, on `90ba554`, none of the three symbols was in
  the tree and the paragraph named #601's branch as the place to find them; #601 has merged.)*
- **What that replaced, on the tree as it was before #601** (`90ba554`, v0.8.7): none of those
  symbols existed there, `userClaim` was
  `(auth != null && auth.token.mu != null && auth.token.mu == auth.uid)` — no device conjunct —
  `RulesVersion` was **2**, and the mailbox rules validated
  `newData.hasChildren(['id', 'sender', 'text', 'at'])` with `"$other": { ".validate": false }`,
  so **a message carrying a `mac` was refused there outright**. The schema was ahead of those
  rules: `shared/mailbox-schema/index.js` declared `REQUIRED_FIELDS = ['id', 'sender', 'text',
  'at', 'mac']` and `MAC_MAX = 64`, which is the disagreement #601's rules close.
- **The schema side**, which did not move: `shared/mailbox-schema/index.js`'s `macField`
  requires exactly 64 lowercase hex characters (`HEX_PATTERN`, refusing with `BAD_MAC`: "*A mac
  is not 64 lowercase hex characters: a 32-byte HMAC-SHA-256 tag*"), and `verifyMac` in
  `shared/mailbox-schema/mac.js` is the separate, keyed question — HMAC-SHA-256 over
  `canonicalBytes` with **D171's per-pairing key**, which "*nothing here reads, stores, derives,
  logs or defaults*".

**So the conclusion the paragraph above reaches stands, on true evidence rather than on the
first draft's invented symbols**: the rules can require a well-shaped tag and cannot check its value, and the **only**
thing that catches a forged one is the **app verifying the tag with the per-pairing key** (§4
rule 11, P6.3/D169, P6.7). A compromised sender — **which is the realistic way attacker 2 gets
the credential, and should be named as the capability's holder rather than "an outsider"** —
can write mailbox content with a well-shaped `mac` that the rules admit — on the generation-3
rules in the tree today, where the `mac` child's shape is checked and its value cannot be;
before #601 merged, generation 2 refused that write for the field's mere presence, which was a
narrower door and not a verification either. And **the budget families carry no MAC at all** (`config/kill/**`,
`config/caps/**`, `spend/$day/**` are numbers and flags): for the budget category a compromised
sender can forge **both** the push **and** the in-app state it points to, so **the app's own
screen is not "the truth" for that category** — the truth there is what the database says, read
under a claim the sender does not control, and that is all it is.

*What the design does about it, stated as what it is rather than as a cure*: §4 rules (1), (3),
(7), (8), (9), (10), (11) and (12) — nothing actionable in our payload, no rendering of any
payload as state or as a route, no action affordances, no write on receipt, no alert before the
MAC is verified, no sentence built from a hostile field, and no claim that the shade is
trustworthy; §3.9's app-side shaping (a **data-only** push whose alert text the app composes
locally, and an ignored unknown id); the user-facing instruction that **a push is only a
doorbell, verify in the app**; and the sender credential being one narrow account's (§3.6)
rather than every run's — which limits *who can become attacker 2*, and is the strongest thing
this design has against them.

**Residual risk, plainly: this design does not prevent a forged push from lying to the owner.**
It prevents the lie from becoming an act, and it shortens the lie's life to the moment the app
is opened; it cannot stop the buzz, the text, or the timing, because there is no sender
signature on a push and the OS cannot distinguish our sender from an attacker who holds the
same credential (§3.9). The honest defences are therefore the ones this document already
argues for elsewhere: keep the credential on **one** account with no key on any laptop (§3.6)
— **which is exactly why the sender is not the shared job account**, since a run that could
send pushes could name a repository in the title and the app-side mitigations would be all
that stood between the owner and a targeted lure; keep every high-value confirmation in the
app, MAC-verified (D169); and, if the owner judges even that window too wide, §10's A and B and
Q9 are the alternative — the way to close it completely is not to have push.

**Attacker 3 — the push provider, the OS, and whoever reads the lock screen.** *Capability*:
Google (and Apple, for the relay), the OS, and any bystander see the payload — **and, in full
rather than in outline, they also see**: the registration token and the project it belongs to;
the **bundle ID / package name**, which identifies the app as Fugaro; the **Firebase
Installation ID**; the **IP address** at registration and delivery; **device and OS details**;
and through all of that a **per-device time series of category events** — when runs finish,
fail, need answers or bind a budget (§3.3). The first draft's "a provider learns a category and
a device address" **understated this by four fields and a timing series**, and is corrected
here. **No Firebase Analytics SDK is linked** (`mobile/package.json` declares no runtime
dependency at all), so this build sends Google no app-usage events beyond the push path itself.
*What this design does*: makes the payload carry nothing (§3.1). The design does **not** claim
end-to-end secrecy — it cannot, FCM and APNs see the payload by construction — and it does not
pretend the payload is encrypted for the phone, because the phone has no key the push path
would need. The mitigation is that **the sender's** payload is content-free (§3.1), and it is
sufficient for *this* attacker's view of the *content* — a repository, a run, an amount are
never in it — precisely because §3.2 fetches the truth elsewhere. It is not a mitigation
against attacker 2,
who writes their own payload.

**Attacker 4 — a stolen or lost phone.** *Capability*: the device, its push token, and the
app's storage. *Bounded by*: D166's revocation (delete the node — no delivery, no reads),
D111's platform secure storage, and the per-pairing MAC key (D169, D171). Push adds no new
power here: a push grants no read the phone did not already have.

**Attacker 5 — a malicious or compromised run.** *Capability*: code inside a run of a shared
job account. *What this design must ensure*: that it **cannot send a push**. §3.6 (B) is
chosen for exactly this, and §6's "what it must never do" repeats it. The attacker can still
write mailbox content within its rules, which is phase 6's residual (m9 §6.4's "the claims
bind against clients, not against minters"), unchanged by this design.

**Out of scope, stated so it is not implied:** a compromised **owner account** (it can read
`config/admin` and pair/revoke), a compromised **Google account** (it holds the project), and
the **FCM service itself** being down or malicious. The last is priced in §7, not defended
against.

## 6. Failure modes

| failure | behaviour | what the user sees |
| --- | --- | --- |
| the device node is missing, or the push node is missing, or the push node holds no token | **no send**; the sender writes no ledger entry — there is no event to dedupe and nothing to cap (§3.4 rule 2 for the send condition, §3.6/§3.7 for the ledger) | nothing — the app still shows everything on open |
| the token is stale (FCM `UNREGISTERED`) | the sender drops the token and **deletes the entry in `config/admin/push/$uid`** — it never writes a marker, and it never touches `config/admin/devices/$uid` (§3.4, §3.5). If that leaves the push node empty the sender **deletes the push node**; it does not retry. *(This is the same deletion `fugaro pair revoke` performs in its one act — §3.4 states the two paths; the sender's is the net for an orphaned node, not the mechanism)* | nothing until the app re-registers (§3.5 (b)) |
| FCM or APNs is down, or the send fails | the intent **stays queued** — a pending row in the same device's `config/admin/push/$uid/ledger` (§3.4, §3.6) — and the sweep retries; after the retry window it is dropped, never escalated | a delayed or missing buzz; the mailbox is unaffected |
| a duplicate event is derived | deduped on `(device, category, key)` (§3.7) | one buzz |
| a storm (many runs cross their cap at once) | the sender's cap suppresses beyond N/hour (§3.7) | one buzz, and the mailbox has the rest |
| the sender job itself is down | alerts stop; **nothing else does** | the mailbox still fills; the heartbeat still shows liveness (D117) |
| quiet hours | suppressed at the sender before FCM is called | nothing during the window |
| the payload would need the detail to make sense | it does not carry it — the app fetches (§3.2) | a generic buzz, then the truth on open |
| the phone is offline when the push arrives | FCM queues or drops it per platform policy; the mailbox is unaffected | possibly nothing; the app catches up on open |
| an attacker spams spoofed pushes (§5 attacker 2) | **nothing can be done at the sender** — it is not our sender, so no rule of §4 binds it. The design bounds the *harm*, not the nuisance **and not the lie**: a forged push is attacker-authored, so its title and body can say anything (§3.1, §3.9) and the OS shows them; what it cannot do is make the app act, or make a **MAC-verified** mailbox message say something it did not say (§4 rules 3, 8, 11). **And the app's screen is not uniformly "the truth" either**: the budget families (`config/kill/**`, `config/caps/**`, `spend/**`) carry **no `mac` at all** (§5 attacker 2), so a sender that holds the push permission *and* database admin — which in this project is the same principals — can forge the budget state the app shows under this category as well | buzzes carrying the attacker's text; the app shows the truth on open **for the MAC-signed families**, and the owner may revoke and re-pair, which stops our sender, not the attacker |

**The rule every row obeys: a push failing is never worse than no push.** No row ends in a
false action, a lost message, or a blocked run.

## 7. Cost

- **FCM: free.** Firebase Cloud Messaging has **no per-message charge** on either platform;
  the APNs relay through FCM is likewise not billed by FCM. There is no metered cost to a push
  volume, which is why §3.7's storm cap is about nuisance and not money.
- **Cloud Run job invocations**: the sender runs as a job. A sweep every 15 minutes is already
  the history job's cadence and adds nothing to the schedule; a per-event invocation is one
  short job execution — fractions of a vCPU-second, in the same "negligible" band as the
  existing check jobs. **No new always-on service, so no new idle cost** — the thing (C) would
  have cost.
- **Cloud Scheduler**: **$0.10 per job per month** beyond the three free jobs per billing
  account; this design adds **at most one** job (and possibly zero, if it rides the history
  job's existing sweep).
- **The APNs side**: an **Apple Developer Program membership** (the owner's, per D120's
  owner-run rule) with its annual fee, and an APNs key the owner generates. This is the only
  genuinely new money, it is annual and fixed, and it exists on **either** channel — FCM's
  relay and Expo's both require APNs underneath. It is not push's cost alone; it is the cost
  of shipping to iOS at all (D120, D125). **Under §3.3's multi-installation problem it is not
  only money**: an `.p8` key is Apple-team-wide, so serving iOS push to an installation the
  owner does not own means either **uploading the publisher's team key into someone else's
  Firebase project** (a standing grant of the power to push to *every* app on the publisher's
  team) or **having no iOS push there**. That is a cost the direct-FCM comparison has to
  carry, and §3.3 and Q7 now do.
- **Retention — the question D121 says it has, asked here rather than left implicit.** D121's
  own words are that the push store has "its own retention question"; this document does not
  answer it, because the answer is the owner's, but it must at least **ask** it, and **Q10**
  does. What is stored and for how long: the **registration token, the platform, the
  preference flags and the dedupe ledger** — all of it in `config/admin/push/$uid`, the ledger
  in its `ledger` child (§3.4, §3.6: one home, no second store); what happens to them
  on **revoke** (§3.4 deletes the push node — so the data's lifetime is bounded by the
  pairing, not by a timer); and what **FCM itself keeps** on Google's side, which this project
  cannot delete and which §5 attacker 3 enumerates. **Q10 names all three.**
- **The engineering cost**, not counted in dollars: one sender, one permission, one node,
  one generation bump (riding P6.4's), and the app's registration path — all of it *after*
  this document is answered and a task is approved.

**Nothing here is spent until the owner approves a task**, and D121's answer means nothing is
spent in v1.

## 8. The checks it would need

**Check 88 — allocated in advance, and this design chooses no number.** The mobile plan's check
block is **80–89** — the plan's own words, "**The owner allocated 80–89 to this work on
2026-10-16**, in advance" — taken as allocated and **never reused and never renumbered** (the
plan's "Check numbers this plan takes (80–89)" states the rule), and its check table already
assigns **88** to **Push delivery — only on the owner's later yes to push (P5.2)** (D121 was
answered "no push in v1" on 2026-10-17), task **P5.2**. *(That row is quoted as this design's own
pull request leaves it: the earlier text, "Push delivery — **only if D121 says yes**", read as a
question still open when D121 had been answered, and the row was rewritten in the same change that
added this document. A quotation of a plan row that the same pull request rewrites is exactly the
kind of citation this document's other quotes are checked against.)* So this design **proposes
nothing and renumbers nothing**: it reads
the number the plan holds. `docs/gcp-live-checklist.md` carries **no `## Check 88` section and
no `| 88 |` row** on `main` today, because the check has not been written yet — not because
the number is unclaimed.

**Who writes it: the plan says P4.2, and this design says so too rather than claiming it.**
The plan allocates the *writing* explicitly — "**checks 80 and 83–88 by P4.2**", and Task
**P4.2: Checks 80–89, written into the register** — and P4.2 is **scheduled** while **P5.2 is
not**. So the register entry for 88 is written by P4.2, in the register's own block form, under
the plan's rule that a check is never run before its text is in the register; the check is
**run by the owner only if the owner builds push**. This design **names Check 88 and does not write it**,
and it does not move the writing to a task that may never be scheduled. It is
**owner-run — no agent performs any step of it**.

**What Check 88 would have to prove** (outline only; P4.2 owns the register text the owner
runs):
a real notification arrives on a real device for each category of §1's reconciled set (D160's
three, cap-near, and the prompt); **the payload
as the provider sees it carries a category and nothing else** (read the FCM/APNs record, not
the lock screen); the app shows the detail only after the authenticated mailbox fetch; deleting
the device node stops delivery at once **and a later push write cannot bring it back** (§3.4,
§3.5); a suppressed category and quiet hours suppress
at the sender; the storm cap holds; a stale token is reaped; and — the step that matters most —
**a hand-crafted push with a lure in the title causes the app to open and show the truth, never
to act and never to render the lure as state or as a link** (under Q6 (i) the payload carries no
id, so the alert is posted only on a new MAC-verified mailbox change) — §4 rules 3, 8 and 11,
§3.9, §5 attacker 2. A check is
never run before its text is in the register.

## 9. The privacy statement, and whether the website's privacy page is where it goes

**This section used to answer "no", and that answer is withdrawn: the website's privacy page
now carries the software's statement.** The evidence the old paragraph rested on no longer
exists. It said `website/src/pages/privacy.astro` "describes **the site**" and that it "says so
itself under 'What this page does not cover'", with **"the Fugaro program"** sent to
`/security/`. Both of those sentences were true of the page as it was; neither is true of the
page as it ships. The owner's request of 2026-10-19 rewrote it into **two named parts** —
**"The website"**, which is what it always said (Google Analytics 4, the consent banner, the
cookies a withdrawal deletes), and **"The software"**, which states what the CLI, the console
and the phone app do — because the App Store and Play listings need **one** privacy policy URL
and that URL has to cover the software as well as the site. The "What this page does not
cover" section is **gone**, so a citation of it is a citation of nothing, and `/security/` is
no longer where the program's statement is sent. `website/test/privacy.mjs` holds the page's
two parts, their order and their sentences to the repository — including the phone app's two
credentials and their storage, and the crash and usage reporting the app will carry with its
opt-in switch (D193, the same owner's ruling) — and `website/test/consent.mjs` refuses a page
whose two parts are not siblings, which is what keeps the two scopes readable.

**What that changes here, and what it does not.** A push disclosure now has a stated home on
the site: it would be **a subsection of "The software"**, beside the credentials and the
reporting — and it is not there today, because push is not built (Q9). **Push notifications
are still not a thing the site does**, and the site is still a static Astro build
(`website/dist`, `firebase.json`'s hosting block); what moved is where the software's
statement is written, not what the site is. This document claims
nothing about the page beyond that: it says what the app does with a reader's data, and the
page's software part is where such a sentence goes when there is one. The two homes below
still stand, and the first is the one that would carry the trust-model half — the page states
what the app sends, and `SECURITY.md` is where this installation's reach is set out.

**Where a push privacy statement belongs, if the owner wants one:**

- **`SECURITY.md`**, whose trust model currently says **nothing at all about the mobile app,
  the mailbox or notifications.** Stated exactly, because this document's value is that its
  stated checks hold: a grep of the file finds **no occurrence of "mailbox", "mobile", "phone"
  or "notification"**, and the occurrences of "push" are all the **git** verb ("anyone who can
  push to it", "pushes only to", "can push") and never the notification sense — so the
  absence this design relies on is of the *subject*, not of the word. That is a gap this
  design surfaces but does not close: the natural home for "what the phone sends, what the
  provider sees" is the trust model, **and what it would sit beside there is the budget-adjacent
  material — the launcher's budget identity, the budget token left in the runs bucket, the
  shared job's bucket reach. It is a new section, not an extension of an existing one: the
  file carries no mailbox disclosure to extend**, and neither this document nor a later task
  may point at one, because the sentence above is this document's own evidence that there is
  none.
- **An app-facing statement** in the app or on the install page, if one exists when the app
  ships (D120's store listings are owner-run and are the other natural home).
- **"The software", the privacy page's second part** — the site-side line, and the one a store
  reviewer would read: it is where the app's credentials, its storage and its reporting are
  stated today, so a push subsection would sit there rather than in "The website", which is
  about what the site does with a visitor's browser. It is a *place*, not a promise: no push
  sentence is on the page now, and `website/test/privacy.mjs` would hold one to this design
  the way it holds the reporting sentences to D193's.

**What such a statement would have to say**, in the design's own words: the phone registers a
push token with Google's Firebase Cloud Messaging (and, on iOS, Apple's APNs by relay);
notifications **that Fugaro sends** contain **no run title, repository name, code, error text
or amount** — only a category — and the detail is fetched by the app over an authenticated
connection, so a notification is never the source of truth (a notification Fugaro did not send
is not covered by this sentence, and the statement should say so rather than imply it — §3.9);
the token is
stored in the installation's own Firebase project and deletable by revoking the device; **the
quiet-hours zone name is stored** where a user sets quiet hours (§3.8, Q5); **retention is
stated as Q10 answers it** — the push data lives only as long as the pairing, the dedupe ledger
(the push node's `ledger` child, deleted with the node) is windowed, and **what Google and Apple keep on their side is not this project's to delete**,
so the statement must not imply the provider forgets. **On the channel, the statement must not
claim a settled path, because there no longer is exactly one**: §3.3 and Q7 make the choice
conditional — **FCM direct where every installation's project is the owner's**, and **a relay
(Expo's, or one the publisher runs) wherever an installation belongs to someone else**, since
an APNs `.p8` is team-wide and FCM addresses one project per token. So the disclosure has to be
written **per channel**: with direct FCM, no third party beyond Google and Apple is in the path;
**with a relay, the relay is a third party that sees the payload and the token** and the
statement must name it. *(The first draft's sentence — "no third party … on the recommended
channel — which is itself an argument for not choosing Expo's relay" — asserted the settled
recommendation that §3.3 and Q7 withdrew, and is replaced here.)*

## 10. The alternatives, with a recommendation

| | what it needs | when it works | privacy | cost |
| --- | --- | --- | --- | --- |
| **A. In-app polling only** | **nothing new** — the app already holds a live database connection (D112) and a mailbox stream | while the app is **open and foregrounded** | **best**: no provider, no token, no server, nothing leaves the device | zero |
| **B. Local notifications while the app is open** | `expo-notifications` locally; **no server, no token, no provider** | while the app is **open** (the OS posts a banner from a change the app itself observed) | **best**: the notification never leaves the device | zero |
| **C. Push (FCM + APNs)** | a sender (§3.6), a token store (§3.4), a generation bump, an Apple membership, an FCM credential — **and, per installation, a registration and (for iOS) the publisher's team-wide APNs key in that installation's Firebase project** (§3.3) | **whenever the phone is on**, app closed or backgrounded | **bounded, and not closed**: the payload **our sender** writes is content-free by construction, but Google/Apple/the OS see it *plus* the app id, installation id, IP and a per-device timing series (§3.3), a **forged** push is attacker-authored and the OS cannot tell the two apart (§3.9), the token is a new stored datum, and multi-installation either spreads the publisher's team key or gives up iOS push outside the owner's own projects | §7 |

**Recommendation: A and B in v1; C only when the owner decides the foreground experience is
insufficient — and then C exactly as §3 designs it.**

Why: **A and B are already possible and cost nothing** — the app's whole data path exists
(D112's read grant, the mailbox, the heartbeat) and a foreground notification is a local API
call. **C is the only one that buys background alerts**, and that is the only thing it buys;
against it stand a new sender, a new credential, a new stored datum per paired account, a rules
generation, a provider relationship, and §5's attacker 2. D121 already chose A and B for v1,
and this design's honest position is that **nothing about the design changes that** — C is
worth building only if the owner finds, in use, that "the app must be open" is a real
limitation. If the owner does want C, §3 is the shape, and the two things that must not be
bent are §4's rules (content-free, never-a-confirmation) and §3.6's choice of one sender.

## 11. What this must never do, in one place

- Never carry content, a secret, a confirmation or an action (§4).
- Never let the app act on a payload, or on a tap beyond opening — and never offer a
  **notification category, action button or inline reply**, never **route** by payload
  (`click_action`, `fcm_options.link`, `data.url`), and never **write** on receipt (§4 rules 3
  and 12).
- Never post an alert — or show, or act on, any state a push points to — **before the MAC is
  verified** with the per-pairing key; for the families that carry no MAC at all, say plainly
  that the screen is not authenticated rather than implying it is (§4 rule 11, §5 attacker 2).
- Never let the app compose lock-screen text from a **free-text mailbox field**, or any text
  that names a repository, a run or an amount — the local text is §4 rule 1's, not only the
  payload (§3.9, §4 rules 1 and 10, console v2 D33).
- Never let any writer other than `fugaro pair` and `fugaro pair revoke` touch
  `config/admin/devices/**`; push data lives in the sibling `config/admin/push/$uid`, so a
  routine write can never recreate a revoked device node (§3.4).
- Never be the only delivery path, or a *signal* the phone must trust for liveness — the
  heartbeat is D117's and no push redefines "offline" (§2).
- Never widen a run's job account with the ability to send (§3.6).
- Never require a credential on the operator's machine (§3.6).
- Never reopen `config/admin` to a client write (§3.5, rejecting (c)).
- Never claim the payload is hidden from the provider — it is not, and §3.1's content-free
  payload is the honest mitigation (§5 attacker 3).
- Never claim a push is safe because it is content-free: that describes what **our sender**
  writes, and a **forged** push is attacker-authored, can say anything on the lock screen, and
  cannot be told apart by the OS. What is bounded is the act and the app screen, not the lie
  (§3.1, §3.9, §5 attacker 2, §4 rules 3, 7, 8 and 9).
- Never ship before the device node it hangs off is **deployed and writable**: P6.4's
  generation 3 is approved (2026-10-18) and **merged** (#601), so the rules are in the tree,
  but `fugaro pair` is **P6.6** — approved, not built — and until it lands nothing writes a
  device node, so no phone can be paired and no registration can exist (§2, §3.4).

## 12. Open for the owner

Each question carries the options, the recommendation and the reasoning. **A later pass
assigns decision numbers**; none is allocated here (the brief's rule), and D110–D129 and
D160–D179 are not this document's to take.

**Q1. Which component sends the push?**
- *(A) the producer sends in-process* (the runner at finalize, the gateway at a threshold):
  lowest latency; **widens every run's shared job account** with the FCM send permission, and
  a run that can send can name a repository in a title — which collapses §5 attacker 2's whole
  argument. **Recommended against.**
- *(B) one cloud sender — the history job — that derives the events itself and calls FCM*,
  with its own dedupe and cap ledger written under its own admin identity, in the per-device
  push node — `config/admin/push/$uid/ledger` (§3.4, §3.6): **recommended, sweep-only.** Exactly one account **gains** the permission, it
  uses its ambient identity, and **no producer write** — so no run account is widened and §4
  rule 6 holds literally. The ledger's writer and its grant are the sender's own
  (`roles/firebasedatabase.admin` on the installation's database, which
  `deploy/terraform/gcp/modules/firebase/iam.tf` grants to that account and, in its own words,
  to no job account); **no run and no phone writes it.** **What it does not claim** (§3.6 B):
  "one account" is not "one principal can send" — project owners and editors hold FCM send
  through basic roles, and an owner's application-default credential on a laptop can send
  (not verified against live IAM); and the account's *ambient* identity is only as narrow as
  the permission actually granted, which is why §3.6 names
  `firebasecloudmessaging.messages.create` rather than `roles/firebasecloudmessaging.admin`.
- *(B′) (B) plus an on-demand poke*, so a run-side producer can ask the sender to run now
  rather than wait for the sweep: **not recommended as the default**, and priced honestly
  rather than as a free refinement — the poke needs the producer to hold `run.jobs.run` on
  that **one** job, which is a grant on a **run's account**, the same class of widening (A) is
  rejected for (§3.6). If the owner wants sub-sweep latency, this is the option and this is
  its price; otherwise the sweep's up-to-15-minute delay is accepted.
- *(C) a new always-on service*: rejected — a standing server this repository has never
  deployed, behind P5.1 and D55–D78.
- *(D) the P5.1 orchestrator, if it lands — the per-installation, scale-to-zero service of the
  sibling design #632 (`always-on-orchestrator.md`)*: it is woken by a per-minute scheduler
  tick, it already exists for the prompt latency P5.1 buys, and it is **the prompt's own
  author** — so for "needs answer" it is the only sender that buys sub-15-minute latency
  **without** widening a run's account (the price (B′) pays). It carries the launcher's grants
  and no model credential, so the send permission would be a **further grant on that service's
  account**, and it is reachable only if P5.1 is approved and lands. **Named, not recommended
  now**: it depends on a task that is unapproved and behind D55–D78. If P5.1 lands, **(D)
  replaces (B) for the prompt category** and (B) remains the sweep for the rest.
- ***Recommendation: (B), sweep-only, with (D) named for the case where P5.1 lands.*** *Why:*
  (B) is the only shape available today that keeps §4 rule 6 ("never a widening beyond one
  sender") true, and the latency it pays is bounded and owner-visible rather than bought with
  a run-account grant.

**Q2. Where is the push token stored, and does it wait for P6.4?**
- *(i) **A SIBLING of D166's device node — `config/admin/push/$uid`** — riding P6.4's rules
  generation 3.* **Recommended.** The device node keeps exactly one meaning: *this account is
  paired to a phone that has not been revoked*, written by `fugaro pair` and `fugaro pair
  revoke` and by **nothing else**. Push data (token, platform, prefs, updated_at) lives beside
  it, sent to only while **both** nodes exist. One revocation act still stops delivery *and*
  reads, because §3.4's rule is "no device node, no send"; the generation is P6.4's rather than
  a new one; and no client grant is added.
  - **Why not a child — the reason this is (i) and the first draft's (i) is withdrawn.** In
    the tree (generation 3, merged as #601), `userClaim` admits a phone if
    `config/admin/devices/<uid>` merely **exists**. A
    child of that node makes **every routine push write** — re-registration (§3.5 (b)), a prefs
    toggle (§3.8), a stale-token cleanup (§6) — a potential **re-pairing primitive**: a
    re-registration message the phone sent before `fugaro pair revoke` and the consumer
    processes after it would recreate `config/admin/devices/$uid/push/...`, so the node exists
    again and every `USER` read works again. Revocation stops being final, which is exactly the
    property §3.5 calls the design's strongest. A sibling has no such write.
    **Stated as the rule it is:** only `fugaro pair` and `fugaro pair revoke` ever write
    `config/admin/devices/**`; the sender sends only when `devices/$uid` **and** `push/$uid`
    both exist, so an orphaned push node is inert; and reaping **deletes** the push node
    rather than writing a marker into it.
- *(ii) A separate registry elsewhere* (its own top-level node, or Cloud Storage): its own
  revocation act, and two acts can disagree — which is the failure D166 was designed to avoid.
  **Recommended against.** *(This is what the first draft rejected under the name "a separate
  registry"; the sibling in (i) is not that: it is admin-only, keyed by the same uid, and
  inert without the device node, so it adds no second revocation act.)*
- ***Recommendation: (i), the sibling `config/admin/push/$uid`.*** *Why:* D166 already solved
  revocation through the device node's **existence**, and that property is only safe if
  nothing else ever writes under `devices/**`. The consequence is that **push cannot ship
  before P6.4**; **the store needs no generation of its own** (the sibling rides generation 3,
  and pairing writes nothing into it). The **registration message** is the one part that does
  need a later change — a bounded field Q3 (b) asks for — and generation 4 is that change's,
  not the store's.

**Q3. How does the push token rotate and the user's switches get written, given the app cannot
write `config/admin`?**
- *(a) register at pairing; reap `UNREGISTERED`*: **its registration half cannot be built** —
  D165's pairing-request node admits only the phone's public key and its timestamp, and refuses
  every other field (`rules.json.tmpl`, generation 3; `internal/budget/rules/emulator_pairing_test.go`
  asserts the refusal), so no pairing session ever carries a registration token (§3.5 (a)).
  A rules change adding a bounded field to that node would buy nothing (b) does not already
  give, and the design does not ask for one. **Its reaping half is kept in every shape.**
- *(b) register over the authenticated mailbox, and a principal holding
  `roles/firebasedatabase.admin` writes **`config/admin/push/$uid`** from it*: **recommended,
  and the only registration path** — the mailbox is the
  app's only write path, it reuses the MAC-authenticated channel (D169), and it puts the write
  on a principal that already exists. **And it needs one thing this document does not
  pretend it already has:** the inbox message node names its fields and refuses every other
  one (`"$other": { ".validate": false }` in `rules.json.tmpl`, `FIELDS` in
  `shared/mailbox-schema/index.js`), so a registration token has no field to travel in today.
  A bounded **registration kind with a required `token`** is the recommendation, and it is a
  **rules-generation change and a schema change** — a later task, recorded in the plan's P5.2
  dependency row. The alternative, the token as a mailbox message's `text`, needs no change
  and is what §3.5 (b) recommends against (free text is not a discriminator).
  **State the dependency, do not assume it:** per §3.4 and
  `deploy/terraform/gcp/modules/firebase/iam.tf`, that role is held by **`admins` and
  `budget_admins` only** — which is why G13 and D166 call pairing an admin act. So (b) is
  free where the operator holds the role (the consumer beside the orchestrator writes it), and
  **where the operator does not, it is an admin act like pairing** — the same role, the same
  person, and no discount for the app's convenience. The design does not claim otherwise.
  **And it is where the push node comes from**: pairing does not create it, so the first
  registration message does.
- *(c) the phone writes its own push node after pairing — a rules grant for
  `config/admin/push/$uid/**`*: **rejected** — it reopens
  `config/admin` to a client write, which D166 deliberately closed. (§3.5 (c) and §3.8 keep
  the same shape: **every** write of push data goes through (b) onto the sibling
  `config/admin/push/$uid`, and **none** touches `devices/**`.)
- ***Recommendation: (b) — the only registration path — with (a)'s reaping as a net.*** This
  also answers §3.8's switches: they are written the same way. **The owner's question is
  therefore not "which shape" but "does the operator hold `roles/firebasedatabase.admin`"**:
  where they do, registration is free; where they do not, it is an admin act like pairing.

**Q4. The categories — D160's set, or D160's set plus the two the phone cannot see — and the
storm cap.**
- *(i) **D160's set, which is what the app can actually resolve**: the **kill switch**, **cap
  reached** and **consumer offline** (cap-near off by default), plus the **prompt** category
  D127's switch widens to*: **recommended.** Every category has a source in the four families
  the phone may read under tension (b1), so §3.9's "ignore what you cannot resolve" never
  fires and no alert the app posts is unsupported. Cap **6 per paired account per hour**, one per
  category per run per hour.
- *(ii) D160's set plus **finished** and **failed**, which needs the phone's read grant
  widened*: **now labelled as what it is — a change to tension (b1), not a category tweak.**
  It requires a `.read` the phone does not have (`agents/$slug/$run`, `outcomes/**`) and it
  contradicts D160's own recorded reason for excluding failed runs ("the phone cannot see
  them"). If the owner wants these two, the widening is the owner's to grant.
- *(iii) D160's set plus a **derived, non-sensitive status written by the consumer***: the
  consumer writes a category and a boolean into the mailbox — MAC-signed like every other
  message (P6.3), naming no repository, run or amount — and the app resolves *that* instead of
  the run record it cannot read. **The narrower alternative to (ii)**, and the one that adds
  no read grant: it makes finished/failed reachable without reopening (b1). Its cost is a new
  mailbox field and a writer, which is where the console v2 D33 content rule applies.
- ***Recommendation: (i) today; (iii) if the owner wants finished/failed; (ii) only as an
  explicit (b1) widening.*** *Why:* the cap, not the category list, is the nuisance control —
  but a category the app cannot resolve against the authenticated mailbox is not a fact, it is
  a lock-screen string, and offering one would break the doorbell model the rest of the design
  rests on.

**Q5. Quiet hours: server-side with a stored zone, or not offered?**
- *(i) Server-side, storing an explicit window **and the IANA zone name** it is in
  (`Europe/Berlin`), default off*: **recommended** — the only shape that actually suppresses
  before FCM is called. **A bare UTC offset is not enough and the first draft's "never a
  timezone name" was wrong**: a stored offset goes wrong across a DST change (a `+02:00` saved
  in summer suppresses the wrong hour in winter), so the design stores the **zone identifier**,
  which is a place-shaped datum — smaller than a location and larger than an offset, and
  §5/§9's disclosures are written to include it. **Q10's retention covers how long it is kept.**
- *(ii) Client-side*: rejected — the buzz and the provider still happen; it silences nothing
  real.
- *(iii) Not offered*: acceptable, and the honest baseline.
- ***Recommendation: (i), default off.*** *Why:* a control that does not stop the send is not a
  control, and a zone name is the least data that makes it work across DST.

**Q6. Does the payload carry the opaque id at all, or only the category — and can the app
really compose the lock-screen text?**
- *(i) Category only*: the app fetches whatever changed from its own mailbox after opening;
  **recommended** — it is the strongest privacy and needs no server-side id mapping, and §3.2
  already establishes the app needs no pointer. This is also the shape §3.9 pairs with a
  **data-only** push whose alert text the app itself posts, generic per category (§4 rules 1,
  10).
  - **Two things this option must not hide.** (1) Under (i) the payload has **no id**, so
    "ignore an unknown id" **protects nothing** — the rule that does the work is §4 rule 11:
    post a local alert only when mailbox resolution shows a new, **MAC-verified** change of
    that category since the last one the app saw. (2) **On iOS, "the app composes the text"
    effectively means a Notification Service Extension**: a data-only `content-available` push
    cannot show text by itself, is throttled and is not delivered after a force-quit, and a
    **visible** app-composed alert needs an NSE (`mutable-content`) — which has to authenticate,
    which means sharing `idToken`/`macKey` through a **keychain access group**, which **touches
    D111** ("Exactly two items, per account, both in platform secure storage (Keychain / Keystore)
    … and nothing else"). **Q6 now asks the owner for that.**
    Where the platform allows is "Android freely; iOS only with an NSE and a D111 amendment".
- *(ii) Category plus a random opaque id*, mapped server-side to the detail: useful only if the
  app ever needs to know *which* thing changed before it can read (it does not), and it adds a
  stored mapping whose theft is a correlation surface.
- ***Recommendation: (i).*** *Why:* the identifier is the only field that could ever correlate
  a push to a run, and the app does not need it — provided the owner accepts the iOS
  consequence above (an NSE and a D111 amendment) or accepts no visible iOS push.

**Q7. FCM direct, or Expo's push relay — given that each installation is its own Firebase
project?**
- *FCM direct*: **recommended only where the owner owns every installation's Firebase project.**
  No third party beyond Google/Apple; the project is already a Firebase project. **But FCM
  HTTP v1 sends only to registration tokens issued for the sending project**, and an `.p8` key
  is Apple-team-wide — so for an installation owned by someone else, direct FCM means either
  **the publisher's team key uploaded into that installation's project** or **no iOS push**
  (§3.3). **The first draft's "one credential" and "avoids p8 handling" were false and are
  withdrawn.**
- *A relay — Expo's push service, or a publisher-run one*: **what a central relay exists to
  solve.** It holds the tokens centrally and sends with the publisher's own credentials, so
  per-installation Firebase projects stop being a problem. **It costs a party in the message
  path that sees the payload and the token**, plus an Expo access token (or a publisher-run
  service that is itself a new always-on component).
- ***Recommendation: FCM direct where the owner owns every installation's project; otherwise a
  relay — and this may reverse the earlier direct-FCM recommendation.*** *Why:* §10's privacy
  column is the deciding one, and it decides **for** direct FCM only when the key never has to
  leave the owner's own projects. **(The provider-behaviour claims here — HTTP v1's
  cross-project token rule, the team-wide `.p8` — are marked UNVERIFIED in §3.3: no cloud was
  queried.)**

**Q8. Where does the push privacy statement live?**
- *(i) `SECURITY.md`* — **recommended**, because the trust model is where this installation's
  credential-and-reach disclosures already live (the budget identity a launcher can mint, the
  budget token left in the runs bucket, the shared job's bucket reach) and it currently says
  **nothing at all** about the phone, the mailbox or notifications (§9). **Stated as what it
  is: the phone's disclosure would be a new section, not an addition to an existing mailbox
  one — SECURITY.md carries no mailbox disclosure today**, and §9's grep is the evidence.
- *(ii) "The software", the second part of the website's privacy page*: **accepted, and it is
  the shipped state's line rather than a rejected one.** The rejection this bullet carried
  ("that page is about **the site**, and it says so itself; push is not a site behaviour") was
  written against a page that had one part and a "What this page does not cover" section. The
  owner's request of 2026-10-19 gave the page two named parts precisely so the software's
  statement has a URL the App Store and Play listings can point at, and the software part now
  states the phone app's credentials, their storage and its crash and usage reporting. Push is
  still not a site behaviour — which is why a push sentence would go in that part and never in
  "The website", whose subject is the visitor's browser.
- *(iii) an app-facing statement at ship time* (D120's store listings, owner-run): also
  appropriate, and not either/or with (i).
- ***Recommendation: (i) now as a gap to close when a task is approved, (ii) in the commit that
  adds push (the page's software part is where the reader and the store reviewer both look), and
  (iii) at ship.***

**Q9. Is push built at all, given D121?**
- *Not in v1, A and B (foreground polling and local notifications) only*: **recommended**, and
  consistent with D121's own answer of 2026-10-17.
- *C, as §3 designs it, when the owner decides the foreground experience is insufficient.*
- ***Recommendation: A and B now; C on a later yes.*** *Why:* D121 already answered this, and
  nothing in this design argues with it — the design exists so the later yes, if it comes, has
  a shape rather than a blank page.

**Q10. Retention: how long is the push data kept, in the store and at the provider?**
- **What D121 already says**: the push store has "its own retention question" — and the design
  did not ask it. It does now, with the three parts the reviewer named.
- *(i) **Lifetime bounded by the pairing** — the token, platform, prefs, quiet-hours zone and
  the dedupe ledger (the push node's `ledger` child, §3.4/§3.6) all live in
  `config/admin/push/$uid` and are **deleted with the push node on
  revoke** (§3.4); a re-pair starts empty*: **recommended.** It needs no timer, no sweeper and
  no second rule, and it is the property revocation already gives. **This is true of the
  ledger only because §3.6 puts it in the node** — a ledger in Firestore would need an answer
  of its own here, and there is not one.
- *(ii) A time bound too* (drop a dedupe entry after N hours, a token unused for N days):
  **also needed for one part** — the **dedupe ledger** is per-event and has no reason to
  outlive its window, so (i) plus a ledger window is the concrete answer. Concretely: the
  sender prunes `config/admin/push/$uid/ledger` rows older than the dedupe window (and any
  pending intent past the retry window, §6) on its own sweep, so the node holds state about
  the last window only — the cap's per-hour timestamps and the pending intents are the same
  rows and go with them.
- *(iii) What FCM itself keeps*: **not this project's to delete.** Google retains the
  registration token and delivery metadata on its side under its own policies (§5 attacker 3
  enumerates what is visible); the app's **`deleteToken`** on revoke, and asking for the
  provider's own deletion path, are the only levers, and they are named rather than promised.
- ***Recommendation: (i) + (ii)'s ledger window, with (iii) written into §9's disclosure so the
  statement does not imply the provider forgets.*** *Why:* a retention answer that stops at
  "we delete our copy" is half an answer.

## What this document does not decide

It does not decide D121's later question (that is the owner's, and Q9 recommends against
building now), **the category set** (Q4 reconciles it with D160 and puts the two categories the
phone cannot resolve — with the read widening they would need — to the owner rather than
assuming them), **the retention answer** (Q10 asks the question D121 only named), **the
notification data's shape or its node** (Q2 puts the sibling `config/admin/push/$uid` and the
one-writer rule for `devices/**` to the owner), the device node's own shape (D166's, plan P6.4),
the mailbox or its MAC (P6.3, D169), the pairing (D165, P6.6), the orchestrator (P5.1, unapproved
and behind D55–D78), the Expo/store path (D120, D125), or whether `SECURITY.md`'s silence about
the phone is closed (a gap this design names and hands to whoever writes that statement). **It decides what push
should be if the owner wants it, and it says plainly that nothing is built until the owner
says so — no service, no token store, no rules change, no app code.**
