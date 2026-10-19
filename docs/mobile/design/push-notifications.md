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

Every claim about the code below was checked against `origin/main` at `82ba600`, by reading
the file and the symbol named. Symbols are cited by name rather than by line number, because
the lines move.

**Related.** [reconciliation.md](../reconciliation.md) (D112, D113, D117, D118, D121,
D126), [functional-spec-reconciliation.md](../functional-spec-reconciliation.md) (D165,
D166, D169 — the pairing, the device node and the MAC), [implementation-plan.md](../implementation-plan.md)
(P4.1 the consumer, P5.1 the orchestrator, phase 6), [voice.md](../../design/voice.md) (D49,
D50 and the F38 residual), [desktop-console-v2.md](../../design/desktop-console-v2.md) (D33 —
the same question asked of the desktop console and answered "no FCM"), [m9-budget-and-dashboard.md](../../design/m9-budget-and-dashboard.md)
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
3. **it needs the owner's answer** — the mailbox carries a prompt (D166's prompt node, plan
   P6.3) that the orchestrator cannot proceed without, and that expires at **D118's TTL**
   (the owner's 72 hours, set on 2026-10-17);
4. **a budget threshold was crossed** — a cap, a kill switch or a per-run ceiling the person
   set is now binding on a run.

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

- **No push library, no push service, no push token.** Nothing in the tree imports or
  declares Firebase Cloud Messaging, APNs, Expo's push service, `firebase-admin/messaging` or
  any messaging dependency; `go.mod` carries no FCM client, and `mobile/package.json`
  declares **no runtime dependency at all** (the manifest is deliberately empty until the
  Expo toolchain is pinned, and `mobile/src/skeleton/index.test.js` keeps it so). Every
  occurrence of the phrase "push token" in the tree is the **git** credential
  (`internal/runner/provision.go`'s `provisionGitToken`), and every "device token" is the
  **pairing** token of D165 — a one-time Firebase custom token — not a notification
  registration token. They are different objects with confusingly similar names, and this
  document uses "push token" for the FCM/APNs registration token only.
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
- **No device-token store.** The device node `config/admin/devices/$uid` is a **proposal**,
  not code: it is named in `internal/budget/rules/rules.go`'s `userClaim` doc comment as the
  node **`USER` will require** "(D166, plan P6.4)", and in `functional-spec-reconciliation.md`'s
  D166 and G14. In the generated rules today, `internal/budget/rules/rules.json.tmpl` carries
  `"admin": { ".read": false, ".write": false }` and **the `devices` subtree does not exist**;
  the rules' `USER` macro (rules.go's `userClaim`) has no device conjunct. So there is nowhere
  a push token is stored, and no grant that would admit one.

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
- **The only scheduled cloud components in the tree**: the history job,
  `deploy/terraform/gcp/modules/installation/history.tf`'s `google_cloud_run_v2_job.history`
  (running `fugaro budget history --sweep`, `internal/cli/history.go`), driven by
  `google_cloud_scheduler_job.history_sweep` (`*/15 * * * *`) and `history_rollover`. Its
  account holds the custom role `fugaroHistory` and, from
  `deploy/terraform/gcp/modules/firebase/iam.tf`, `roles/firebasedatabase.admin`,
  `roles/firebaseauth.admin` and `roles/datastore.user`. Its
  own comment states the property this design leans on for §6: **"The job has no project
  config file … the account's identity is its only credential"** — there is no key on any
  laptop.
- **One place the site already speaks to a provider**: `website/src/pages/privacy.astro`,
  which describes Google Analytics 4 and says plainly what it does **not** cover ("the Fugaro
  program" goes to `/security/`). §10 prices whether push touches it.

## 3. The proposal

### 3.1 What is sent: a category, and nothing that names anything

A push carries **exactly two things**:

- a **coarse category**, one of the four in §1, drawn from a small fixed enumeration — the
  closest thing to "status" that is safe on a lock screen; and
- an **opaque identifier**, a value that means nothing outside the phone's own authenticated
  mailbox.

**It carries nothing else.** Not a run title, not a repository name, not an owner/name, not an
error string, not a spend figure, not an amount in any currency, not a model name, not a
branch, not a PR number, not a code excerpt. The reason is not politeness: a lock screen is
read by whoever is standing there, and the push provider and the OS see the payload in the
clear. The provider's logs are not this project's to redact.

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
- **A push cannot be forged into an action.** Because nothing actionable is in the payload,
  the strongest a spoofed notification can do is make the app open on a screen that shows the
  truth. §5 turns this into the document's central rule.

### 3.3 The channel: FCM, with APNs behind it

**Recommendation: Firebase Cloud Messaging**, delivering to both platforms — FCM's HTTP v1 API
for Android, and FCM's APNs relay for iOS. The reasons are the project's own:

- **The app is already on Firebase.** An installation *is* a Firebase project
  (`project-centric.md`; `mobile/src/accounts/account.js` carries `firebaseProjectId`,
  `authUid` and `fugaroProject` per account), the app signs in with Firebase Auth custom
  tokens (D165), and it reads the Realtime Database directly (D112). FCM is the one push
  channel that needs **no new third party**: Google is already the party holding the project's
  data.
- **One channel, two platforms.** FCM relays to APNs, so there is one sender, one credential,
  one token type, one code path — rather than an FCM integration *and* an APNs integration and
  the p8 key handling the latter drags in.
- **It is already paid for.** A Firebase project exists per installation and FCM has no
  per-message charge (§9).

**What FCM sees.** The device's registration token, the payload, the delivery time, and the
project it belongs to. **What APNs sees** on iOS: the same payload, relayed. **What the OS and
the lock screen see**: the payload, in the clear. All three are the reason §3.1's payload is
content-free — the design does not attempt to hide the payload from the provider, because it
cannot; it makes the payload not worth hiding.

**The alternative: Expo's push service.** The app is an Expo app (`mobile/package.json`,
`mobile/app.json`), and Expo's push service is the path of least resistance for an Expo build:
`expo-notifications` obtains an Expo push token and posts to Expo's endpoint, and Expo relays
to FCM and APNs. **What Expo sees is strictly more than FCM sees**: it sees the payload
**and** the Expo push token **and** a mapping from the token to the underlying FCM/APNs token
and the project — a party that is neither the owner nor Google/Apple, added to the message
path. It also adds a second credential (an Expo access token) and a second vendor relationship
to the owner's privacy surface. It buys convenience for an app that is not yet built, and this
design does not recommend it; Q7 keeps it open for the owner, because the eventual Expo
toolchain (D120, D125) may make the direct FCM path more work than this document assumes.

**Recommendation: FCM direct, not Expo's relay.** The privacy cost of the relay is real and
the convenience is speculative; if the owner later finds the direct path expensive in the Expo
toolchain, Expo's relay is the fallback, priced here rather than discovered later.

### 3.4 Where the push tokens live, and who can read them

**Recommendation: under the device node of D166 — `config/admin/devices/$uid` — which is the
node of rules generation 3.**

The device node is the right store because it already exists as a decision. D166 (owner,
2026-10-17) chose `config/admin/devices/$uid` as **the allowlist the rules' `USER` macro
requires**, precisely so that **deleting the node revokes at once** — G14's finding was that a
claim-only allowlist revokes slowly (a Firebase ID token lives an hour), while a node the
rules consult does not. A push token stored in the same node **inherits that revocation**: see
§3.5. The shape proposed is a child of the node, so that deleting the node is still the one
revocation act:

```
config/admin/devices/$uid/            # D166's node: the pairing, admin-written over IAM
  ...                                 #   what D166/P6.6 already plan (mac key name, added at)
  push/                               # THIS design's addition
    token/<id>                        #   the provider registration token (opaque to the app)
    platform                          #   "ios" | "android"
    updated_at                        #   epoch ms, server-written
    prefs/                            #   §3.8's per-category switches and quiet hours
```

**Who can read them:**

- **No client can.** `rules.json.tmpl` carries `"admin": { ".read": false, ".write": false }`,
  and D166's design keeps `config/admin` client-denied — the app never reads its own push
  token, and neither does any other phone. A push token is not a secret the app needs; the app
  only ever *hands one over* at pairing time (§3.5).
- **Over IAM, the same principals that can already read `config/admin`, and no new ones.** Per
  `modules/firebase/iam.tf`, that is `roles/firebasedatabase.admin` to `admins` and
  `budget_admins`, and `roles/firebasedatabase.viewer` to launchers and operators (the
  `firebasedatabase.viewer` read **bypasses the rules** — the residual m9 §6.4 and G13 already
  state, and this design adds nothing to it). The **sender** (§3.6) reads the node over IAM
  with its own service-account identity, exactly as the history job reads the database it
  sweeps.
- **The honest sentence:** a push token is readable by every launcher and operator of the
  installation, and by the sender. **It is not a credential** — holding it lets a principal
  send a notification to that device *only if it also holds the FCM send credential*, which is
  a different and narrower grant (§3.6, §6). The token is a device address, not a key.

**The generation.** This design's node lands in **rules generation 3**. `internal/projectpolicy/version.go`
carries `RulesVersion = 2` today — its doc comment states "Generation 2 is the mailbox (mobile
plan task P2.1)" — and D166's device node is already planned to bump it (plan P6.4 names
`internal/projectpolicy/version.go` among its files). **A push-token store does not need a
generation of its own**: it is a leaf inside a node P6.4 already adds, so it rides P6.4's
generation 3 rather than claiming a 4. If the owner ever builds push *before* P6.4 approves
the device node, the store has no home and push waits — which is Q2.

### 3.5 Rotation and revocation

**Revocation is the device node's deletion, and it is immediate.** Because the sender reads
tokens from `config/admin/devices/$uid` at send time, **no node means no token means no
send**; and because D166's `USER` requires the node, the same deletion also stops the app's
reads at once (G14's property). One act, one revocation. This is the design's strongest
property and the reason to prefer the device node over a separate token registry: a second
store would need its own revocation act, and two acts can disagree.

**Rotation is the harder half, and it is an open question rather than a solved one (Q3).** Push
tokens rotate: FCM and APNs issue new registration tokens on reinstall, on restore, on
app-data clear, and occasionally otherwise, and FCM reports a stale token back as
`UNREGISTERED`. Three shapes are available:

- **(a) Register at pairing, and let the sender reap stale tokens.** The CLI writes the token
  during D165/P6.6's pairing (an admin act, `roles/firebasedatabase.admin`), and the sender
  drops a token FCM reports `UNREGISTERED` and marks the device "not registered" until the
  next pairing. Simple, needs no new write grant — and **stale for exactly the case that
  matters** (a user who reinstalls and never re-pairs gets no pushes).
- **(b) Re-registration over the authenticated mailbox.** The app, on next open, writes its
  current token as an ordinary mailbox message; the **consumer** — which already holds admin
  credentials on the machine beside the orchestrator — updates the device node. No new grant
  to the app, no admin act for the user, and it reuses the mailbox the design already trusts.
  This is the recommendation, because it puts the write on the principal that is already
  allowed to make it.
- **(c) Let the app write its own push child.** A rules grant for the phone to write
  `config/admin/devices/$uid/push/**` under the `MAILBOX` identity. Cheapest for the user and
  **the one shape this design rejects**: it reopens `config/admin` to a client write, which
  D166 deliberately closed, and it would make the device node partly client-writable — a node
  whose whole point is that only an admin may change it.

**Recommendation: (b), with (a)'s reaping as the safety net.** Q3 asks the owner.

### 3.6 What writes a push

**The producer must not send, and the sender must not be the operator's machine.** The brief
is explicit that push **"must not need a standing credential on the operator's machine"**, and
the tree supports that: every scheduled cloud component today runs under an **ambient service
account identity with no key anywhere** (`internal/cli/history.go`: "the account's identity is
its only credential"). So the FCM send credential is an **OAuth2 access token minted from a
service account's identity**, never a downloaded key and never a secret on a laptop.

The candidates, priced:

- **(A) The producer sends, in its own process.**
  - *Run finished / failed*: the runner at finalize (`internal/runner`'s `finalize`) or the
    gateway at a threshold (`internal/gateway`'s `Server`) calls FCM directly. Lowest latency,
    and the event is in hand.
  - *Cost*: it widens **every run's job account** with the FCM send permission — the same
    account that, under project-centric phase 3, is **shared between repositories**
    (budget-token-channel.md §2). A push-send grant on a shared job account is a grant every
    run of the profile holds, and a compromised run could then notify the owner of a
    fabricated "needs your answer" — which is exactly the phishing surface §5 forbids. **Reject
    for the job account; the console's D33 already rejected the analogous widening for the
    desktop.**
  - *The one place (A) is defensible*: the **gateway**'s threshold case, where the budget
    decision is made in-process — but the gateway shares the run's container and identity, so
    it inherits (A)'s cost. **Reject.**
- **(B) One cloud sender, draining a pending-push node.**
  - *Recommended.* The producer **records an intent** — a row saying "device D, category C, at
    T, key K" — and a single cloud component reads the rows and calls FCM. The natural home is
    the **history job** (`history.tf`'s `google_cloud_run_v2_job.history`), which already runs
    every 15 minutes under `fugaroHistory` + `firebasedatabase.admin`, already holds an ambient
    identity, and already talks to the mailbox's database. It gains exactly one thing: the FCM
    send permission, held by **one account**, not by every run.
  - *Latency*: the 15-minute sweep is the floor. For a "run finished" alert that may be
    enough; for "needs your answer" it is poor, because the prompt expires at D118's 72 hours
    and a 15-minute delay is usually tolerable but not always. **The fix is to let the
    producer invoke the sender**: the producer holds `run.jobs.run` on **that one job** and
    calls it on demand, with the scheduler as the backstop. That is a narrow grant (one job,
    `run.jobs.run`) rather than a project-wide one — but it is still a grant on a run's
    account, and Q1 asks the owner whether to take it or to accept the sweep's latency.
  - *Where the rows live*: **not** in the rules-readable mailbox. A pending-push row is
    sender-internal state, and `config/admin` (deny-all, IAM-only) is the shape that already
    fits; §3.4's node is the sibling. The rows must carry **no content** — the category and the
    device are all a sender needs, and §3.1 already forbids the rest.
- **(C) A new always-on service.** A Cloud Run service with a socket that the producer pokes.
  **Reject**: it is a standing server this repository has never deployed, it is P5.1's
  orchestrator's problem (unapproved, and behind D55–D78), and it buys latency (B) can buy with
  one narrow grant.

**Recommendation: (B).** One sender, one credential, ambient identity, no key on a laptop, and
the producer's write is an intent rather than a send. Q1 records the latency/grant trade.

### 3.7 Rate limiting and dedupe

- **One notification per state change.** The key is the *transition*, not the event: a run
  that ends `infra_error` sends one "failed", not one per retry of the finalize path; a prompt
  sends one "needs answer", not one per outbox re-delivery (the consumer already re-delivers
  until the ack, `shared/mailbox/index.js`'s dedupe ledger `state.seen`). The sender
  dedupes on `(device, category, state-change key)` over a window, so a re-derived event
  is not a second notification.
- **A storm cap.** At most **N notifications per device per hour** (N is small — the design
  suggests **6**, and Q4 asks), and at most **one per category per run per hour**. A repository
  that fails ten runs in a minute produces one alert and a quiet count, not ten buzzes. The
  cap is enforced **at the sender**, because the sender is the single writer and the phone
  cannot enforce anything about its own buzz.
- **The cap is a floor on nuisance, not a loss of information.** Everything a push would have
  carried is in the mailbox regardless — the push is a doorbell (§3.2) — so a suppressed
  notification costs nothing but a buzz.

### 3.8 User controls

- **Per-category off switches**, one per §1 category, stored in the device node's `prefs`
  child (§3.4). Off means the sender does not send that category to that device — the switch
  is honoured **server-side**, because a client-side switch cannot stop a push from reaching
  the OS and the provider.
- **Quiet hours**, a window per device, also server-side and stored in `prefs`. This is the
  design's one uncomfortable privacy surface and it should be named rather than buried:
  honouring quiet hours server-side requires knowing the **device's timezone or an offset**,
  which is a location-adjacent datum the app otherwise never sends. The alternatives are worse
  — a client-side filter still buzzes and still reaches the provider — or the feature is not
  offered. **Recommendation: store an explicit UTC offset and window, never a timezone name
  and never a location; default off.** Q5 asks.

**How the controls are written** is Q3's rotation question in another guise: the app cannot
write `config/admin` (§3.4), so the switch is written either by the CLI (an admin act, awkward
for a toggle the user expects to flip in the app) or by the app through the mailbox and the
consumer (§3.5's (b)). **Recommendation: (b) again** — the mailbox is the app's only write
path, and this design does not add a second one.

## 4. What a push must never carry, and what it must never do

This is the document's spine. Each line is a requirement, not a preference.

1. **Never content.** No run title, repository name, owner/name, slug, branch, PR number,
   error text, log line, code, file path, model name, spend figure, cap, amount or currency.
   (§3.1.)
2. **Never a secret or a credential** — no token, no MAC key, no claim, no capability, nothing
   from `mobile/src/accounts/secret.js`'s set.
3. **Never a confirmation, and never an action.** A push **cannot** carry an answer, a confirm
   id, an action record, or anything a consumer (`fugaro mailbox`'s action path, P6.7) would
   accept as a decision; and **receiving one must never cause the app to submit anything**.
   The D128 amendment's whole enforcement is a typed, MAC-verified action record that the
   consumer checks (D169, D171) — a push that could stand in for one would undo it. This is
   the rule the brief states as "a push must never carry or trigger a confirmation, only open
   the app", and it is absolute.
4. **Never the only delivery path.** The mailbox is authoritative; a push is best-effort.
   Nothing may be *lost* by a push's failure, because nothing is *carried* by it.
5. **Never a write path to the operator's machine.** The sender runs in the cloud under an
   ambient identity; no credential lives on the laptop (§3.6).
6. **Never a widening beyond one sender.** Exactly one account gains the FCM send permission
   (§3.6 rule (B)); no run's job account gains it.
7. **Never a lock-screen leak.** Because (1) holds, the lock screen shows a category and
   nothing else — which is the point of stating (1) as an absolute.
8. **Never trusted input.** The app treats the payload as untrusted display text at most, and
   in the recommended shape (§3.2, Q6) it does not read it at all beyond the tap.

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
compromised FCM credential can make the phone buzz with anything. *What it buys*: a buzz, and
a title in the provider's and OS's logs. *What it does not buy*: it cannot forge mailbox
content (the rules' `mac` and the claims bind that, P6.3/D169), it cannot make the app act, and
— because §4's rule (1) means a **genuine** push carries no repo, run, amount or text — **the
spoof is indistinguishable from the real thing and therefore useless for phishing.** An
attacker who wants to lure the owner into "approve this urgent merge" must put the lure
somewhere, and there is nowhere in a content-free push to put it. *What the design does*: §4
rules (1) and (3), plus the sender credential being one account's (§3.6) rather than every
run's. **This is why the sender is not the shared job account**: a run that could send pushes
*could* name a repository in the title, and the whole argument in this paragraph would collapse.

**Attacker 3 — the push provider, the OS, and whoever reads the lock screen.** *Capability*:
Google (and Apple, for the relay), the OS, and any bystander see the payload. *What this design
does*: makes the payload carry nothing (§3.1). The design does **not** claim end-to-end
secrecy — it cannot, FCM and APNs see the payload by construction — and it does not pretend
the payload is encrypted for the phone, because the phone has no key the push path would need.
The mitigation is content-free payloads, and it is sufficient precisely because §3.2 fetches
the truth elsewhere.

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
| the device node is missing or has no push token | **no send**; the sender records nothing | nothing — the app still shows everything on open |
| the token is stale (FCM `UNREGISTERED`) | the sender drops the token, marks the device unregistered, and does not retry | nothing until the app re-registers (§3.5 (b)) |
| FCM or APNs is down, or the send fails | the intent **stays queued** and the sweep retries; after the retry window it is dropped, never escalated | a delayed or missing buzz; the mailbox is unaffected |
| a duplicate event is derived | deduped on `(device, category, key)` (§3.7) | one buzz |
| a storm (many runs fail at once) | the sender's cap suppresses beyond N/hour (§3.7) | one buzz, and the mailbox has the rest |
| the sender job itself is down | alerts stop; **nothing else does** | the mailbox still fills; the heartbeat still shows liveness (D117) |
| quiet hours | suppressed at the sender before FCM is called | nothing during the window |
| the payload would need the detail to make sense | it does not carry it — the app fetches (§3.2) | a generic buzz, then the truth on open |
| the phone is offline when the push arrives | FCM queues or drops it per platform policy; the mailbox is unaffected | possibly nothing; the app catches up on open |
| an attacker spams spoofed pushes (§5 attacker 2) | **nothing can be done at the sender** — the design bounds the *harm*, not the nuisance: the payload is content-free, so the spam is indistinguishable from real alerts and carries no lure | buzzes; the owner may revoke and re-pair |

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
  of shipping to iOS at all (D120, D125).
- **The engineering cost**, not counted in dollars: one sender, one permission, one node,
  one generation bump (riding P6.4's), and the app's registration path — all of it *after*
  this document is answered and a task is approved.

**Nothing here is spent until the owner approves a task**, and D121's answer means nothing is
spent in v1.

## 8. The checks it would need

**Check 88 — and it is free.** `docs/gcp-live-checklist.md` carries **no `## Check 88`
section and no `| 88 |` row** on `main`, so the number is unclaimed in the register; the
mobile plan's own check table already proposes **88** for "Push delivery — only if D121 says
yes", task P5.2. This design **names Check 88 and does not write it**: per the plan's Global
Constraints and P4.2, a check's text is written by the task that owns it (here, a P5.2 that is
unscheduled), into `docs/gcp-live-checklist.md`, in the register's own block form, and it is
**owner-run — no agent performs any step of it**.

**What Check 88 would have to prove** (outline only; the task that writes it owns the steps):
a real notification arrives on a real device for each of §1's four categories; **the payload
as the provider sees it carries a category and nothing else** (read the FCM/APNs record, not
the lock screen); the app shows the detail only after the authenticated mailbox fetch; deleting
the device node stops delivery at once (§3.5); a suppressed category and quiet hours suppress
at the sender; the storm cap holds; a stale token is reaped; and — the step that matters most —
**a hand-crafted push with a lure in the title causes the app to open and show the truth, never
to act** (§4 rule 3, §5 attacker 2). A check is never run before its text is in the register.

## 9. The privacy statement, and whether the website's privacy page is where it goes

**The website's privacy page is not the place, and this is verifiable rather than a
preference.** `website/src/pages/privacy.astro` describes **the site** — Google Analytics 4,
the consent banner, the cookies a withdrawal deletes — and it says so itself under "What this
page does not cover": the host's request logs and **"the Fugaro program"** go to `/security/`.
The site is a static Astro build (`website/dist`, `firebase.json`'s hosting block); **push
notifications are not a thing the site does**, and putting an app's data flow on the site's
privacy page would be the page claiming something that is not its subject.

**Where a push privacy statement belongs, if the owner wants one:**

- **`SECURITY.md`**, whose trust model currently says **nothing at all about the mobile app,
  the mailbox or notifications** (a check of the file finds no occurrence of "mailbox",
  "mobile", "phone", "push" or "notification"). That is a gap this design surfaces but does
  not close: the natural home for "what the phone sends, what the provider sees" is the trust
  model, beside the budget-token paragraph and the mailbox residual it already carries.
- **An app-facing statement** in the app or on the install page, if one exists when the app
  ships (D120's store listings are owner-run and are the other natural home).

**What such a statement would have to say**, in the design's own words: the phone registers a
push token with Google's Firebase Cloud Messaging (and, on iOS, Apple's APNs by relay);
notifications contain **no run title, repository name, code, error text or amount** — only a
category — and the detail is fetched by the app over an authenticated connection; the token is
stored in the installation's own Firebase project and deletable by revoking the device; and no
third party beyond Google (and Apple) is in the path **on the recommended channel** — which is
itself an argument for not choosing Expo's relay (§3.3).

## 10. The alternatives, with a recommendation

| | what it needs | when it works | privacy | cost |
| --- | --- | --- | --- | --- |
| **A. In-app polling only** | **nothing new** — the app already holds a live database connection (D112) and a mailbox stream | while the app is **open and foregrounded** | **best**: no provider, no token, no server, nothing leaves the device | zero |
| **B. Local notifications while the app is open** | `expo-notifications` locally; **no server, no token, no provider** | while the app is **open** (the OS posts a banner from a change the app itself observed) | **best**: the notification never leaves the device | zero |
| **C. Push (FCM + APNs)** | a sender (§3.6), a token store (§3.4), a generation bump, an Apple membership, an FCM credential | **whenever the phone is on**, app closed or backgrounded | **bounded**: the payload is content-free by construction, but Google/Apple/the OS see it, and the token is a new stored datum | §7 |

**Recommendation: A and B in v1; C only when the owner decides the foreground experience is
insufficient — and then C exactly as §3 designs it.**

Why: **A and B are already possible and cost nothing** — the app's whole data path exists
(D112's read grant, the mailbox, the heartbeat) and a foreground notification is a local API
call. **C is the only one that buys background alerts**, and that is the only thing it buys;
against it stand a new sender, a new credential, a new stored datum per device, a rules
generation, a provider relationship, and §5's attacker 2. D121 already chose A and B for v1,
and this design's honest position is that **nothing about the design changes that** — C is
worth building only if the owner finds, in use, that "the app must be open" is a real
limitation. If the owner does want C, §3 is the shape, and the two things that must not be
bent are §4's rules (content-free, never-a-confirmation) and §3.6's choice of one sender.

## 11. What this must never do, in one place

- Never carry content, a secret, a confirmation or an action (§4).
- Never let the app act on a payload, or on a tap beyond opening (§4 rule 3).
- Never be the only delivery path, or a *signal* the phone must trust for liveness — the
  heartbeat is D117's and no push redefines "offline" (§2).
- Never widen a run's job account with the ability to send (§3.6).
- Never require a credential on the operator's machine (§3.6).
- Never reopen `config/admin` to a client write (§3.5, rejecting (c)).
- Never claim the payload is hidden from the provider — it is not, and §3.1's content-free
  payload is the honest mitigation (§5 attacker 3).
- Never ship before the device node it hangs off is approved (P6.4's generation 3).

## 12. Open for the owner

Each question carries the options, the recommendation and the reasoning. **A later pass
assigns decision numbers**; none is allocated here (the brief's rule), and D110–D129 and
D160–D179 are not this document's to take.

**Q1. Which component sends the push?**
- *(A) the producer sends in-process* (the runner at finalize, the gateway at a threshold):
  lowest latency; **widens every run's shared job account** with the FCM send permission, and
  a run that can send can name a repository in a title — which collapses §5 attacker 2's whole
  argument. **Recommended against.**
- *(B) one cloud sender — the history job — draining a pending-push node, invoked on demand by
  the producer*, with the 15-minute sweep as backstop: **recommended.** One account, one
  credential, ambient identity, no key on a laptop. The open part is the invocation: it needs
  the producer to hold `run.jobs.run` on that **one** job — a narrow grant, but a grant on a
  run's account — **or** the owner accepts the sweep's up-to-15-minute latency. Q1's real
  question is that trade.
- *(C) a new always-on service*: rejected — a standing server this repository has never
  deployed, behind P5.1 and D55–D78.
- ***Recommendation: (B).*** *Why:* it is the only shape that keeps §4 rule 6 ("never a
  widening beyond one sender") true, and the latency cost is a bounded, owner-visible trade.

**Q2. Is the push token stored under D166's device node, and does it wait for P6.4?**
- *(i) Yes — a child of `config/admin/devices/$uid`, riding P6.4's rules generation 3.*
  **Recommended:** one revocation act (delete the node stops delivery *and* reads), no new
  client grant, and the generation is P6.4's rather than a new one.
- *(ii) A separate registry* (its own node, or Cloud Storage): its own revocation act, and two
  acts can disagree — which is the failure D166 was designed to avoid. **Recommended against.**
- ***Recommendation: (i).*** *Why:* D166 already solved revocation; this design should inherit
  it rather than build a parallel one. The consequence is that **push cannot ship before P6.4**
  and the generation stays 3.

**Q3. How does the push token rotate and the user's switches get written, given the app cannot
write `config/admin`?**
- *(a) register at pairing; reap `UNREGISTERED`*: no new grant, but a reinstalled-and-not-
  re-paired device goes silent.
- *(b) re-register over the authenticated mailbox, the consumer writes the node*:
  **recommended** — the mailbox is the app's only write path, the consumer already holds the
  admin credential beside the orchestrator, and it reuses the MAC-authenticated channel (D169).
- *(c) a rules grant letting the phone write its own push child*: **rejected** — it reopens
  `config/admin` to a client write, which D166 deliberately closed.
- ***Recommendation: (b), with (a)'s reaping as a net.*** This also answers §3.8's switches:
  they are written the same way.

**Q4. The categories and the storm cap.**
- All four categories (finished, failed, needs-answer, budget), with a cap of **6 per device
  per hour** and one per category per run per hour — **recommended**, because all four are
  states the owner named and the mailbox holds the detail a suppressed buzz would have carried.
- A subset (e.g. needs-answer and failed only) — cheaper on nuisance, and defensible if
  "finished" is noise.
- ***Recommendation: all four, cap 6.*** *Why:* the cap, not the category list, is the nuisance
  control; a missing category is a missing fact.

**Q5. Quiet hours: server-side with a stored offset, or not offered?**
- *(i) Server-side, storing an explicit UTC offset and window, default off*: **recommended** —
  the only shape that actually suppresses before FCM is called, at the cost of storing an
  offset (never a timezone name, never a location).
- *(ii) Client-side*: rejected — the buzz and the provider still happen; it silences nothing
  real.
- *(iii) Not offered*: acceptable, and the honest baseline.
- ***Recommendation: (i), default off.*** *Why:* a control that does not stop the send is not a
  control, and an offset is the least data that makes it work.

**Q6. Does the payload carry the opaque id at all, or only the category?**
- *(i) Category only*: the app fetches whatever changed from its own mailbox after opening;
  **recommended** — it is the strongest privacy and needs no server-side id mapping, and §3.2
  already establishes the app needs no pointer.
- *(ii) Category plus a random opaque id*, mapped server-side to the detail: useful only if the
  app ever needs to know *which* thing changed before it can read (it does not), and it adds a
  stored mapping whose theft is a correlation surface.
- ***Recommendation: (i).*** *Why:* the identifier is the only field that could ever correlate
  a push to a run, and the app does not need it.

**Q7. FCM direct, or Expo's push relay?**
- *FCM direct*: **recommended** — no third party beyond Google/Apple; the project is already a
  Firebase project; one sender, one credential.
- *Expo's relay*: less work if the Expo toolchain makes the direct path awkward (D120, D125);
  **costs a party in the message path that sees the payload and the token**, plus an Expo
  access token. Acceptable only if the owner prefers the convenience.
- ***Recommendation: FCM direct.*** *Why:* §10's privacy column is the deciding one, and the
  convenience is speculative until the app exists.

**Q8. Where does the push privacy statement live?**
- *(i) `SECURITY.md`* — **recommended**, because the trust model already carries the budget
  token's and the mailbox's disclosures and currently says **nothing** about the phone.
- *(ii) the website's privacy page*: rejected — that page is about **the site**, and it says so
  itself; push is not a site behaviour.
- *(iii) an app-facing statement at ship time* (D120's store listings, owner-run): also
  appropriate, and not either/or with (i).
- ***Recommendation: (i) now as a gap to close when a task is approved, (iii) at ship.***

**Q9. Is push built at all, given D121?**
- *Not in v1, A and B (foreground polling and local notifications) only*: **recommended**, and
  consistent with D121's own answer of 2026-10-17.
- *C, as §3 designs it, when the owner decides the foreground experience is insufficient.*
- ***Recommendation: A and B now; C on a later yes.*** *Why:* D121 already answered this, and
  nothing in this design argues with it — the design exists so the later yes, if it comes, has
  a shape rather than a blank page.

## What this document does not decide

It does not decide D121's later question (that is the owner's, and Q9 recommends against
building now), the device node's own shape (D166's, plan P6.4), the mailbox or its MAC (P6.3,
D169), the pairing (D165, P6.6), the orchestrator (P5.1, unapproved and behind D55–D78), the
Expo/store path (D120, D125), or whether `SECURITY.md`'s silence about the phone is closed
(a gap this design names and hands to whoever writes that statement). **It decides what push
should be if the owner wants it, and it says plainly that nothing is built until the owner
says so — no service, no token store, no rules change, no app code.**
