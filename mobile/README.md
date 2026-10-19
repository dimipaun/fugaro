# `mobile/` — the Fugaro app

The Expo (React Native) app for the phone. It is **its own artifact**: outside
the Go module, with its own release path, and it merges without touching the
CLI (mobile plan D125, Phase 3).

**Status.** Phase 3: tasks **P3.1** (the app skeleton and the account store), **P3.2**
(the feed reader and the contract's document rules), **P3.4** (the audio
interfaces) and **P3.6** (the mailbox client) are here
([`docs/mobile/implementation-plan.md`](../docs/mobile/implementation-plan.md)).
P3.3 (the job list) and P3.5 (the voice screen) are not. What exists is the
shell, the account model, the two device ports, the fences around them, the
feed (the four granted families read directly from the Realtime Database, the
document they add up to, and the screen's rules over it), the phone's mailbox
client -- which is the only place in the app that writes to the database -- and
the three voice ports with the fakes the voice screen's logic is written and
tested against.

**Demo mode (the owner's ruling of 2026-10-19).** The app carries a built-in
demo, so that a Google Play reviewer -- who cannot create an account and cannot
pair a phone -- can reach everything the app shows. It runs the app's own code
on fixed sample data (`src/demo/`), it can never touch the network, secure
storage or a credential (`src/demo/safety.test.js`), and every screen it draws
says "Demo mode: sample data" with an always-visible way out (the shell,
`src/app/root.js`). **How to enter it** is the section below.

**Two audio seams coexist.** P3.6 defined the one audio seam it needed --
`src/mailbox/speaker.js`, `speak(text)` answering whether the utterance was
spoken, which is what the ack's rule is written against -- before P3.4 was
merged. P3.4's `src/audio/` (`tts.js`) is the full speech-output port. They are
not yet unified: `speaker.js` stays until the voice screen (P3.5) wires the
mailbox client to the audio pipeline. The secure store is **not** stubbed:
P3.1's `SecretStore` is reused, and it gained one method (`use`) for the reason
`src/mailbox/README.md` gives.

## Running the tests

```sh
cd mobile && npm test          # node --test "src/**/*.test.js"
```

Node 22 (`>=22 <23`) and npm 10, per the manifest's `engines`. The script is a
**glob**, deliberately (implementation-plan §"files each task owns"): no later
task has to edit this manifest to add a test file. The cost of a glob is that
`node --test` exits 0 over a pattern that matches nothing, which is the one way
this runner can lie — so `src/skeleton/index.test.js` exists as the red step
that proved the runner was alive before any module was written, and
`src/boundary.test.js` asserts the walk found the app's source before it
trusts what it found.

**No test here needs a device, a network, a database or a microphone** (Global
Constraint 10). That is not an accident of these tests: it is why every module
under `src/` is written as a function of its platform module, with the real
module plugged in by `index.js` at the bottom of the tree.

## Demo mode: how to enter it, and how to leave it

1. Start the app the way this file describes under "Running the tests" (the
   Expo toolchain, which is not installed in the environment this was written
   in). The first screen appears: "Try the demo".
2. Tap **Try the demo**. There is nothing to set up first -- no account, no
   pairing, no sign-in and no network -- and the demo runs entirely on the
   phone, on fixed sample data.
3. Every screen now carries the banner **"Demo mode: sample data"** and the
   control **"Leave demo mode"**. Tap it to leave; the app is exactly as it was
   before, and tapping "Try the demo" again starts the same demo from the
   beginning.

What a reviewer sees, and why each part is the app rather than a picture of it:
the sentences the phone sent (with the ids its own client minted), the consumer's
replies verified by the app's MAC code, spoken and acked; the orchestrator's
heartbeat read by P1.4's presence rule; the question P6.5 is holding; and a
destructive command whose read-back and confirmation go through P6.2's state
machine -- including the refusal a second tap gets. The strings, and the three
fences, are `src/demo/`'s; the words the owner pastes into the Play form are
`docs/mobile/play-review-access.md`.

## The layout

| Path | What it is |
| --- | --- |
| `index.js` | The entry. The one file that knows what a device is: it imports `expo`, `expo-secure-store`, `@react-native-async-storage/async-storage`, `react` and `react-native`, builds the two ports, the store and the demo controller, wires the two screens and registers the root. No decisions of its own, and no test (see "What is not verified"). |
| `app.json` | The Expo config. No credential, no installation, no bundle identifier, no Android package — see `src/app-config.test.js`. |
| `src/accounts/` | The account store: the installation tuple (`account.js`), the two items (`secret.js`), the secure store (`secure-store.js`), the settings store and its fences (`settings.js`), the per-account listeners and caches (`resources.js`), the store itself (`store.js`), and the two device ports (`expo-secure-store.js`, `expo-async-storage.js`). `index.js` is the one door. |
| `src/app/root.js` | The root shell. Renders the current account's id, and nothing else an installation was built from; while demo mode is on it draws the banner and the way out on **every** screen, above whatever the screen holds. |
| `src/app/first-screen.js` | The first screen: what the app is waiting for, the "Try the demo" control, and the slot (`pairingScreen`) where P6.8's pairing screen goes -- above the demo control, never instead of it. |
| `src/demo/` | Demo mode (the owner's ruling of 2026-10-19): the fixed sample data (`sample.js`), the placeholder key source (`keys.js`), the transport that plants the consumer's side in the port double (`transport.js`), the controller (`index.js`) and the screen (`screen.js`). `safety.test.js` is the scan that holds the three fences (no network, no secure storage, no credential). |
| `src/mailbox/` | The mailbox client: the write paths and their key rules (`paths.js`), the app's SHA-256 and HMAC (`sha256.js`, `mac.js`), the redactor's seam (`redact.js`), the database port and its double (`store.js`), the audio stub (`speaker.js`) and the client itself (`client.js`). `index.js` is the one door; `write-path.test.js` is the scan that enforces the one-write-path claim; `mutation-proof.sh` proves the guards; `README.md` is the module's own rule sheet. |
| `src/feed/` | The feed reader: the four granted families and the gate every read passes (`paths.js`), the wire node shapes (`nodes.js`), the contract's document rules (`document.js`) and the two figures the phone recomputes (`recompute.js`), the screen's rules (`view.js`) and the reader itself (`reader.js`). `index.js` is the one door; `fixtures/nodes.js` is the synthetic recording the tests read. |
| `src/audio/` | The voice ports and their fakes (P3.4): the capture path (`audio-in.js`), transcription (`stt.js`), speech output (`tts.js`), the transcript-to-message rule (`message.js`) and the pipeline that wires them (`pipeline.js`). **No engine, no device, no SDK**: `index.js` is the one door, and its test scans the directory for exactly that. |
| `src/boundary.test.js` | The scan that says **which files may write a credential** (below). |
| `src/skeleton/` | The trivial module and test that proved the runner runs (P3.1's red step). |

## The mailbox client, and the app's write set

`src/mailbox/` is where the app talks to the database, and it is the only place
that does (`src/mailbox/README.md` has the rule sheet). Three things about it
belong in this file:

* **The app's write set is exactly two paths** (D113): its own inbox message
  node, one per utterance with a fresh id, and the `acked` child of an outbox
  message it has already spoken. The heartbeat belongs to the orchestrator
  (D113b) and is only read. Every record it writes is signed with the
  per-pairing MAC key, and every record it reads -- outbox messages and the
  heartbeat -- is verified against that key before a word is spoken or an ack
  is written.
* **The one-write-path claim is enforced by a scan.** `src/mailbox/write-path.test.js`
  (P3.1's `src/boundary.test.js` shape) fails if any file but
  `src/mailbox/client.js` calls the store's `write`, if any file but `index.js`
  imports a `firebase` module or names a path through `.ref(` — so P3.2 and
  P3.3 cannot bring a second write set in with the real adapter.
* **One pass at a time.** `send`, `flush` and `poll` share a queue: an ack is
  written only after the message was spoken, so two passes in flight together
  would both speak it, and a screen that polls on a timer and on a reconnect
  would do that without meaning to.
* **The key is reached through `SecretStore.use`**: the material is handed to a
  callback for the duration of one call and never returned (a result that
  carries it is refused as `leaked`). No module under `src/mailbox/` can read
  the key itself -- `reveal(`'s allow-list in the boundary scan stays two files
  -- and that is the reason `use` exists at all.

## Where the phone's secrets live, and what is not stored

D111: an account has **exactly two** items, and both live in **platform secure
storage (Keychain on iOS, Keystore-backed storage on Android) via
`expo-secure-store`**, and nowhere else.

1. **The Firebase Auth ID token** (`idToken`) — the operator identity's
   credential, per account.
2. **The per-pairing MAC key** (`macKey`) — the key D113/P6.3 sign with, per
   account, per pairing.

They are written under `fugaro.account.<accountId>.<item>`, one Keychain item
per account per item, with `keychainAccessible:
WHEN_UNLOCKED_THIS_DEVICE_ONLY` (`src/accounts/expo-secure-store.js`). That
choice is the one security decision the port makes, and it has its own test: it
keeps an item out of an **iCloud Keychain backup** and off a **restored-to-
another-phone** device. The MAC key proves possession of the pairing, so a key
that could be restored anywhere would restore the ability to speak as this
installation — `per-pairing` in name only. It is also not readable while the
phone is locked. `requireAuthentication` (a Face ID prompt on every read) is
deliberately **not** set: the ID token is refreshed and read in the background,
and a prompt per refresh is a prompt the owner switches off.

**What is NOT stored, anywhere:**

* **Not in AsyncStorage or any settings file.** D111 says the two items are
  "never in a settings file". The tuple lives in AsyncStorage
  (`src/accounts/expo-async-storage.js`) precisely because that store is
  **unencrypted**; a credential in it would be readable by anyone who can read
  the app's container. Three fences in `settings.js` hold that line: a `Secret`
  cannot be a value (whatever it holds), a credential-*shaped* value is refused
  (JWT, PEM private key, `AIza…` Google key, `sk-…` model key, long hex), and a
  key that names an item or the secure store's namespace is refused.
* **Not logged.** There is no logger in the app: `console.` appears in no
  production file, and a boundary rule below keeps it that way. A `Secret` also
  cannot be printed — `String()`, a template literal, `+`, `JSON.stringify`,
  `util.inspect`, `Object.entries` and a spread all yield `[secret <item>]`,
  and each of those is asserted by name in `secret.test.js`.
* **Not in a fixture, and not in this repository.** Every value in every test
  is synthetic, and the credential-*shaped* strings are assembled at run time
  from harmless pieces (`shared/presence/index.test.js`'s convention), because
  a credential-shaped literal in a repository is a credential-shaped literal in
  every clone, every log and every paste.
* **No shared token store, and no token cache.** D114: an account's items are
  reached by its own id or by nothing. `AccountStore.secret(id, item)` is the
  only call that returns material, it needs the account and the item both, and
  `list()`/`account()` return D114's tuple and no secret.
* **No orphan, ever.** `SecretStore.forget` attempts the delete of every item
  D111 declares **unconditionally**, and never gates it on a successful read —
  a Keychain read fails exactly when the phone is locked, which is the state
  the port's own `WHEN_UNLOCKED_THIS_DEVICE_ONLY` creates, so a read-gated
  delete would leave both items in the Keychain under an account
  `AccountStore.remove` had just deleted, with no code path able to reach them.
  A delete needs no read and no unlock and is idempotent; a delete that *fails*
  is a `store-failed` refusal, which makes `remove` keep the tuple so the
  removal can be run again once the phone is unlocked.
* **No `belong`.** The `belong` installation is added by the owner by hand
  (D114, Global Constraint 11); no fixture here is recorded from it, and no
  agent touches it. `app.json` ships with no installation in it at all.

## The feed: four families, and nothing else

D112 was answered **direct Realtime Database reads** on 2026-10-17, and tension
(a) was resolved with it: the phone reads **database nodes directly**, as
documents judged by the contract's rules, from **exactly four families**
(`config/caps/**`, `spend/$day/**`, `config/kill/**`, and the phone's own
mailbox — `outbox` and `heartbeat`). D124 is kept as the **reader rules and the
document shapes**, not as the `watch --json` transport: there is no child
process on a phone, and no iOS or Android build of the Go CLI to be one.

**What the phone therefore cannot show, and does not try to.** `agents/$slug/$run`
and `outcomes/**` carry no read for a user claim, so **there are no run titles,
no run rows and no PR link** anywhere in the app. `config/caps/repos` (the
*parent* of `$slug`) carries no grant either, so the phone **cannot enumerate
repositories**: the list is the owner's, typed in, exactly as D114's account
list is. `config/mode` is not granted, so a document this app assembles carries
`mode: null` rather than a guessed `observe` — on the one setting that decides
whether a cap is enforced. And the queued and finished rows live in the runs
bucket, which the phone does not see.

The plan's own added test case is a statement about **requests**, not about
refusals: *no request is ever made for `agents/**` or `outcomes/**`*. So the
gate (`paths.js`'s `request`) judges every path **by exact shape** before the
port is called, and the test proves it the only way a request can be proved
absent — the fake port records every path it is handed, and the assertion is on
that record. A reader that asked and handled the refusal would pass a test that
only checked the refusal, and fails this one.

### The contract's reader rules, and where each one lives

| §10.13 rule | Where |
| --- | --- |
| 1. a line that is not JSON is skipped | `document.js`'s `decode` (the phone's analogue of a line is a node's value: a value that is not an object is not a counter, and reads as `unavailable` — never a throw, and never a `0`) |
| 2. the newest document, never accumulated | `view.js`: one document, replaced on every ingest; there is no arithmetic in the file, so nothing *can* accumulate |
| 3. silence is not death | `view.js`'s `livenessOf`, from the four inputs the rule names: the phone's clock, the document's `now`, its `connection.state` and its `connection.age_seconds`. The vocabulary (`LIVENESS`) has no word for a dead process, and silence reaches **`stale` and stops there** — that is as far as `State.conn` goes (`watch.LostAfter` is a *run*'s threshold, and the phone reads no run) |
| 4. a changed `day` is a break | `view.js`: `broken` is `!measured`, and a broken view's `drawable` is **`null`** — so the zero a rollover document carries (§10.7: "must not draw the zero") cannot be drawn by a screen that forgot to check |
| 5. a missing field is `unavailable`, never `0` | `availability.js` (the figure) and `document.js` (every number in a document comes out of it as one). Both kinds of the tree's "absent" are honoured: a **cap** is a pointer (`nil` = unset = `unavailable`, never a cap of zero, which would be a project allowed to spend nothing) and a **counter** is `omitempty` (absent = the zero the CLI's own document carries) |
| 6. `--repo` | not the phone's: its repository list is the owner's, and `len(repos)` is that list |
| 7. the child's exit | not the phone's: there is no child to end and nothing to loop on |
| 8. the console's allow-list | the console's |
| the degraded shape (§10.8) | `document.js` recognises the CLI's own shape — `lsDoc` (`project`, `runs[]`, `totals`, `warnings[]`) **plus** `degraded` and `notice` — by its `degraded` key, judges the `lsDoc` half for its type and **drops** it (this app renders no run row), refuses a document that carries both halves, and `view.js` renders it **as degraded**, never as a zero spend |

### The two choices P3.2 had to make

The resolution of 2026-10-17 left two things to this task, and both are stated
here because a reviewer has to be able to disagree with them:

1. **The burn rate and the cap fraction are recomputed on the phone** rather
   than not shown. The alternative would leave the live screen without the two
   numbers it exists for, and both inputs are inside the grant. What is
   recomputed is recomputed **from the CLI's own constants and formulas**
   (`internal/watch/build.go`'s `bar`, `internal/watch/state.go`'s `State.Burn`
   and `burnOf`): a 5-minute window that needs a 60-second span before it says
   anything, `perMin*60 > daily/8` for `fast`, and `100%` when the cap is zero.
   "From the CLI's constants", not "exactly as the CLI computes it", because
   there are **two places this app cannot follow** and a reviewer should be able
   to see both without reading the Go: `Build` blanks the burn block when
   `len(b.Runs) == 0` (`internal/watch/build.go:332`), and this app is not
   granted `agents/**`, so it has no runs to count; and `burnOf` prefers
   `cfg.BurnAlertPerHour` — the operator's local
   `watch.burn_alert_usd_per_hour` (`internal/localcfg/localcfg.go:145`), which
   is not in the database — so the phone only ever computes the `daily/8`
   fallback. Both divergences are written down in `src/feed/recompute.js`. The
   two figures are over **two different totals**, and that
   is the CLI's own split rather than an accident here: the fraction is over
   **counted** ("only counted feeds it; notional counts toward no cap") while the
   rate is the slope of **spent + notional**, which is what `State.observe`
   records. A rate with no window is **unknown, not zero**; so is one whose
   window was just cleared, which is what a counter that went **backwards** does
   (`State.record`: "a reset, not negative spend"). A missing input makes the
   figure `unavailable`, never `0`.
2. **The repository list is the owner's**, typed in, and never discovered:
   `config/caps/repos` carries no grant, so the database cannot be asked which
   repositories exist. The list is bounded (`MAX_PATHS`) because every entry is
   a listener, and an entry that is not a slug is **refused, not dropped** — a
   list that quietly lost an entry is a repository the phone would never show.

### The connection the phone reports

The CLI's `State.conn` is "the worst of the four streams". The phone's streams
are its granted paths, so the same rule applies to them, **in the same order**:
a **refused** read outranks everything (a permission error is the diagnosis, and
the silence that follows it is its consequence), then a read that **failed** for
any other reason, then the paths that have not answered yet, then the **oldest
node that has delivered**. `age_seconds` is the CLI's own measure, and it is
what `view.js` reads as silence.

**A permission error and a dropped socket are different facts**, and the phone
keeps them apart (`isPermissionError`, the phone's side of `errors.Is(err,
rtdb.ErrPermission)`): only a `PERMISSION_DENIED` code is `refused`/"access
refused"; a timeout, a network failure or a client that has not authenticated is
`offline`/"the read failed". Calling the second one a credential problem would
send the owner to check a token that is fine. The reasons are **static
sentences** (`CONNECTION_REASONS`): an error message from the database is not
something this app puts on a screen, and the reader drops it rather than echoing
it. Before anything has arrived the state is `offline`/`connecting` — the CLI's
own word for a stream that has not spoken yet, and not a death.

**Silence never leaves `stale`.** `State.conn` has exactly one silence
threshold (`StaleAfter`, 45 s), and §10.9's `LostAfter` (180 s) is not a
connection's: the table calls it a property of a **run** (`State.health` calls a
run `lost`), and the phone is subscribed to no run — it reads no stage, no
deadline and no agent. A feed that has been quiet for half an hour is a feed
that has been quiet for half an hour; the app says so, and it keeps the last
measurement on screen.

### The feed's mutations

Each was planted, the run went red, the bug was removed:

| Mutation | Went red |
| --- | --- |
| the gate lets a denied family through and the plan asks for `agents/…` | `no request is ever made for a denied family, and the port proves it` |
| the path bound (`MAX_PATHS`) is not enforced | `the plan is the granted shapes…` |
| the repository list is not judged | `the plan is the granted shapes…` |
| the account's uid is not judged | `the plan is the granted shapes…` |
| `ingest` stores a value for a path that was never asked for | `a value for a path the reader never asked for is refused and not stored` |
| a listener is subscribed without being registered with the account | `the listener belongs to the account…` |
| a closed account can subscribe anyway | `…not one socket is opened for a closed account` |
| the project's cap is read as the repository default | `the granted nodes add up to a document…` |
| an unset cap becomes a cap of zero | `a missing node is unavailable, never a zero…` |
| a counter reading is not sampled | `the burn window is fed spent + notional…` |
| a rollover does not move the reader to the new day | `a changed day is a break…` |
| the day is measured before its counter has been heard | `a changed day is a break…` |
| a refused read is not a connection state | `the connection is the worst of the granted paths…` |
| the kill node is passed through instead of the document's shape | `a kill switch the phone cannot read is a kill switch that is on` |
| an unreadable counter node reads as zero | `a node that cannot be read is unavailable, and never a zero` |
| a reader that was never started refreshes anyway | `a reader that was never started refuses to refresh…` |
| starting does not assemble the first, connecting document | `the connection is the worst of the granted paths…` |
| `stop` does not stop the listeners | `…stop is final` |
| starting twice is allowed | `…and stop is final` |
| the recording ignores the day it is asked for | the door's end-to-end test and `the granted nodes add up to a document…` |
| the door re-exports a path composer | `the door carries no port and no path composer` |

The ones below are the **code review** of this task: its eight findings, plus
the mutations that followed from reading the CLI's own code to fix them (the
backwards counter, and the connection's age in the two error states) — see "The
review" under the table:

| Mutation | Went red |
| --- | --- |
| the burn window is fed `counted` instead of `spent + notional` | `the burn window is fed spent + notional…` |
| a counter that goes backwards keeps the window it went back from | `a counter that goes backwards clears the window…` |
| every port error is a refusal | `a permission error is refused, and a failure to connect is not` |
| a failed read is not a connection state | `a permission error is refused…` |
| silence goes `offline` again at ten minutes | `a feed that stops arriving is a silence, not a death` |
| an instant past what a JS `Date` renders is rendered anyway | `an instant past what a JS Date can render…`, `a kill switch whose instant this app cannot render…` |
| the uid is composed without being judged | `the account is judged as the account module judges it…` |
| `refresh` does not judge its clock | `a clock that is not an instant is refused at every entry point` |
| `isNow` accepts an unrenderable instant | `a clock that is not an instant is refused…` |
| the assembly does not judge its instant | `a clock that is not an instant is refused…` |
| `stop` keeps the session | `stop forgets the session, and a restart does not inherit it` |
| `check()` relabels every figure `measured` | `the codes are the refusal vocabulary a caller matches on` |
| a refusal (or a failed read) resets the connection's age to zero | `a permission error is refused, and a failure to connect is not` |

**Two of those mutations were found by writing the tests, and both were real
bugs rather than test faults.** The first: the wire document's `kill` carries no
`on` field (presence *is* the on-ness — `jsonKill` returns nil when the switch is
off), so passing the tree's node through was an unknown field that made
`document.js` refuse the **whole document** the moment a kill switch was on. The
second: an unreadable counter node crashed the assembly — a node holding text
reached `counters.counted` on a refusal. It reads as `unavailable` now, never as
`0`, and the rest of the document is untouched.

### The review

A code review of this task found eight more, and they are worth reading as one
class rather than as eight incidents: **each was a rule the CLI has, restated
here slightly differently** — which is exactly the failure mode this module was
written to avoid, because the two implementations are meant to agree about the
same tree.

* **The burn window was fed `counted`.** `State.observe` records
  `Spent+Notional`, and `counted` is `bar()`'s number. The phone's rate and its
  `fast` flag therefore disagreed with the console's for the same tree, and the
  test encoded the wrong series — a test that would have passed either way is
  worse than no test, so it now moves `counted` alone and requires a zero slope.
* **Every port error was collapsed into `refused`.** `State.conn` calls a
  permission error `refused` and any other error a connection that is not up; a
  dropped socket was being reported as an access problem, and the port could not
  express the difference. There are two sets and two sentences now.
* **`watch.LostAfter` was used as a connection threshold.** §10.9's table calls
  it a property of a **run** (`State.health`), and `State.conn` never leaves
  `stale` for a silence — so the phone would have said `offline` where the
  console, reading the same tree, said `stale`. The constant is gone, and a test
  asserts it is not exported again.
* **An out-of-range `at` threw inside an assembly.** A nanosecond timestamp
  fits an `int64` and does not fit a JS `Date`; `toISOString()` throws, and
  because the value was stored before the assembly, every later assembly threw
  too — a feed dead for the rest of the session, with an ON switch reading as
  off. `MAX_INSTANT_MS` bounds the instant, and a switch whose instant cannot be
  read is a switch the app believes (`State.kill`), not one it claims to have
  read while quietly dropping a field.
* **The mailbox path was composed from an unjudged `authUid`.** The account id
  was judged and the uid was not, so an account without one composed
  `mailbox/undefined/outbox` — a path the gate **grants**, because `undefined`
  has a uid's shape. That is the request the plan's own case forbids, arriving
  by the back door. The account is now judged by `validateAccount`, the module
  that owns the tuple.
* **`refresh()` did not judge its clock**, so a no-argument call threw a
  `TypeError` out of `dayKey` instead of refusing with `BAD_NOW`. The same bound
  is now applied at every entry point *and* in the assembly, so a clock that
  cannot be rendered cannot reach `toISOString()` at all.
* **`stop()` left the session's state behind**, so a re-`start()` rendered the
  previous session's counters as freshly delivered — the exact confusion
  `#delivered` exists to prevent.
* **`check()` returned a `measured` figure for a notional one**, silently
  dropping the state for any caller that reads `.figure`. `measured` is a claim
  about who read the number, not a default.

Each fix has its own test, and each test was mutation-proven (the thirteen rows
above). Two divergences of the same class were found while fixing these and are
**not** in the review. `State.record` clears the burn window when a counter goes
backwards, where this sampler kept the reading it went back from — so the fix
for the first finding carries the second with it. And `State.conn` sets `Age`
whenever **any** stream has been heard, in every branch, so a refusal or a
failed read does not reset the clock: this reader was reporting an age of zero
for a refusal, which tells a screen the feed has just been heard from when the
truth is that the last thing heard was an hour ago and it was a rejection.

### The second review

Two review rounds of the same task found seven each, and the class is the same
one again: **a rule the CLI has, restated here slightly differently.** Thirteen
of the fourteen are fixed; each fix has a test and each test was mutation-proven.
(The first round's fixes were lost to a refused push — Fugaro is not allowed to
change workflow files — so the second round saw the same code and re-raised the
ones it had.)

| Mutation | Went red |
| --- | --- |
| the live field list judges the CLI's own degraded document (no discriminator first) | `the degraded shape is its own shape, recognised by its own key` |
| `DEGRADED_FIELDS` loses `totals` (or `warnings`) | the same test — the `lsDoc` half is judged, not ignored |
| the degraded branch is reached only after the live field list | the same test |
| `refresh()` does not assemble after the rollover | `the rollover breaks the series at the rollover, not at the next delivery` |
| `refresh()` subscribes before it sets `#paths`/`#day` | `the rollover subscribes the new day before a listener can answer` |
| the rollover clears `#refused` by the store instead of the plan | `a refusal on a path that never delivered does not outlive its day` |
| `ingest` judges the gate but not the plan | `ingest takes a value only for a path in the plan, not merely a granted one` |
| the kill switch's strings are bounded but not cleaned | `a kill switch's strings are cleaned the way the CLI cleans them` |
| `#dayChanged` is cleared by the first assembly inside the gap | `a changed day is a break, and the new day is re-subscribed` |
| the door re-exports `isGranted` | both `index.js` tests |

**The one refusal.** This app does **not** recompute the burn rate "exactly as
the CLI computes it", and the README no longer says so: the two places it cannot
follow are written into `recompute.js` and quoted in "The two choices P3.2 had
to make" above. The reviewer's counter — that `Build`'s `len(b.Runs) == 0`
zeroing "depends only on data the phone already holds" — is the part that does
not hold: **live** runs are `runs/**` and `agents/**`, neither of which this app
is granted, and the spend tree carries spend, not liveness.

**The two dead exports** (`FIRST_FRAME_MS`, `gap()`) were the same class and are
answered differently on purpose. `FIRST_FRAME_MS` was a threshold on the door
that decided nothing — it is gone, and a test asserts it is not exported again.
`gap()` and `STATE.GAP` are §1's **vocabulary**, not a threshold: the contract
names five availability states, and `availability.js` now says in place that the
constructor has no caller in P3.2 and that the day break §10.7 calls a gap is
drawn as `drawable: null` instead.

## The demo mode, and the mutations that prove it

Demo mode is one module and two screens, and every rule about it is either a
fence (something that must never happen) or a claim about the app's own code
being the code that ran. Both are listed here.

**The three fences, and where each is asserted.**

| Fence | Where |
| --- | --- |
| It can never touch the network. | `src/demo/safety.test.js`: a scan over the module's code (comments stripped, with a positive control per pattern) for `fetch(`, `XMLHttpRequest`, `WebSocket`, a URL, a Firebase SDK import or a database host -- and a run of the whole demo with `fetch`, `XMLHttpRequest` and `WebSocket` replaced by throwers. |
| It can never touch secure storage or a real credential. | The same file: the same scan for the storage SDK's surface (`expo-secure-store`, `ItemAsync`, `AsyncStorage`, a Keychain name, the port's own `getItem`/`setItem`/`deleteItem`), plus the demo entered, confirmed and left against the app's own `SecretStore` built over a port that records and refuses every call -- which sees none -- with that port's control (the store being asked directly) right beside it. The controller takes **no port, no account store and no key source**, so there is nothing to reach with. |
| No sample value is a credential, and every name says it is a demo. | `src/demo/sample.test.js`: every string of the `sample.js` and `keys.js` MODULES (not a hand-written list of exports) through the app's own `secretShaped` detector, and every installation, repo, uid and project name asserted to be the demo's own. |

**The claims about the app's code, and where each is asserted.**

| Claim | Where |
| --- | --- |
| The client is P3.6's client, and the demo does not answer in its place. | `src/demo/transport.test.js` plants a reply whose tag covers different bytes: the app's own verifier drops it, it is not spoken and it is not acked. |
| The tags are made by the app's own MAC code. | The same file recomputes every planted tag with `macOf(record, key, direction)` over the record without its `mac`. |
| The read-back is P6.2's, built from the resolved target. | `src/demo/index.test.js` and `src/demo/screen.test.js` compare the demo's string with `readBack(verb, target)`, require it to contain the run id and require it **not** to be the command. |
| The confirmation is a real utterance and spends one action. | `src/demo/index.test.js`: the phrase is sent (a signed write), its id is minted fresh and differs from the command's, the record carries `demo-confirmation-1`, and a second tap is refused by the machine. |
| The question is P6.5's queue. | `src/demo/index.test.js`: the queue's `pending` is what the screen shows, with the safe answer beside it. |
| Entering twice is one demo, whenever the second call arrives. | `src/demo/index.test.js`: a second call after the run returns the same state; a second call **during** it (a double tap, which lands in the same tick) joins the run in flight, so the sample sentences are sent once and the demo publishes once; and a `leave()` during a run leaves the demo off rather than letting the run land behind it. |
| Entering after a `leave()` starts a demo, even if the left run is still winding down. | The same file: a tap arriving while an abandoned run is in flight starts a whole new run (joining the abandoned one would answer it with the inactive state and start nothing), and the abandoned run ending does not take the new run's place in flight with it. |
| Leaving during a confirmation is answered, not thrown at. | The same file: `confirm()` holds the run it started with across the send, so a `leave()` in between returns the inactive state rather than reading the module's `running` (which is null by then) and throwing at a screen; a phrase left in flight is dropped rather than recorded against the demo entered after it. |
| The banner and the way out are the shell's, on every screen. | `src/app/root.test.js`, asserted with the demo off and on, and end to end with the real controller and the real screens. |
| `MemoryMailboxStore.deliver` is the other end's write and not a phone write. | `src/mailbox/store.test.js`. It is what keeps the demo from seeding the tree through `write`, which the one-write-path scan fences to the client. |
| The Play form text promises the control the app has. | `src/demo/safety.test.js` reads `docs/mobile/play-review-access.md` and requires the sentence the owner pastes, with the control's own words (`TRY_DEMO`), and the README required to carry the same three strings. |

**The mutations run against this task** (each one planted, seen red, removed):

| Mutation | Went red |
| --- | --- |
| the screen's read-back is the command instead of the machine's string | `the read-back is the machine's...`, `the destructive path shows the machine's read-back...` |
| the planted tags are made with the wrong direction | `every planted message carries the tag the app's own code makes`, `entering the demo runs the app's own pass...` |
| the sample replies answer an id nobody minted | `the demo's id source names the utterances...`, and the replies in `sample.test.js` |
| a delivery is recorded in `writes()` as a phone write | `a delivery is the other end's write...` |
| the banner is drawn for no screen | `the demo is on: the banner and the way out are on the screen...` |
| the pairing screen replaces the demo control instead of joining it | `the pairing seam is a slot...` |
| a confirmation does not advance the machine | `a second confirmation is refused...`, `the destructive path shows...` |
| entering the demo twice builds a second one | `entering the demo twice is the same demo...` |
| the in-flight guard is removed, leaving the old `running` check alone | `two overlapping enters are one demo: the double tap cannot send twice` |
| the in-flight guard is assigned only after the first await | the same test |
| a run left behind by `leave()` is allowed to land | `leaving while the demo is still being built leaves it off` |
| the in-flight slot is joined without checking the epoch it was started in | `entering again while an abandoned run is still in flight starts the demo`, `a run ending does not clear the run that replaced it` |
| an ending run clears the slot unconditionally | `a run ending does not clear the run that replaced it` |
| `confirm()` reads the module's `running` again after the send | `leaving during a confirmation is answered, not thrown at`, `a confirmation in flight does not land on the demo entered after it` |
| a reply's `acked` state is not shown | `the screen shows the app's own run...` |
| the sample clock moves by a millisecond | `the clock is fixed, and the data is frozen` |
| the key source answers for any item | `the demo's key source answers for one item...` |
| the safe answer is not one of the answers offered | `the sample data is legal where the app will use it` |
| a new sample export is key-shaped | `nothing in the sample data is a credential...` |
| a sample message or heartbeat is made schema-illegal | `the sample data is valid under the schema...` (and 15 more, before the demo can be built) |
| the README stops saying how to enter the demo | `the Play form text and the README promise the control the app has` |
| the control's words change and the Play form text does not | the same test, and `the control is the owner's words...` |

**What the demo deliberately does not do.** It does not translate the machine's
refusals into its own vocabulary (it shows the code P6.2 gave), it does not
disable the Confirm control after a confirmation (the second tap's refusal is
part of what the app does), and it does not read a clock, a random source or an
environment variable -- its clock is `sample.js`'s fixed instant, which is what
makes two runs of the demo the same run.

## Which files may write a credential

`src/boundary.test.js` is the D54b-clause-(3)-shaped half of P3.1 (reconciliation
§4 tension (b2) clause (4) asks this task for exactly that shape): a scan over
the app's source, four rules, each with an allow-list, a self-exemption and a
**positive control** so a mistyped pattern cannot pass for a clean tree.

| Rule | May appear in | Why |
| --- | --- | --- |
| `…ItemAsync(` (the Keychain SDK) | `src/accounts/expo-secure-store.js` | one module reaches the platform store; a direct SDK call bypasses the port, the `Secret` check and the accessibility |
| `reveal(` | `src/accounts/secret.js`, `src/accounts/secure-store.js` (and tests) | the material is handed to a store in exactly one place; a screen that reveals a token is one refactor from printing it |
| `fugaro.account.` | `src/accounts/secure-store.js`, `src/accounts/settings.js` | a second module that knows the namespace can address an item |
| `console.` | nothing (tests only) | D111's "never logged" is kept by having no logger |
| an import path that resolves outside `mobile/` | nothing but `shared/` (D119) | the app does not build through the CLI's or the console's tree (D125); tests are scanned too |

Each rule is mutation-proven — the bug was planted, the run went red, the bug
was removed. The mutations, in the order they were run:

| Mutation | Went red |
| --- | --- |
| `switchTo` opens the next account without checking the teardown | `a teardown that failed does not open the next account` |
| `switchTo` opens before it closes | `switching closes the previous account before it opens the next` |
| `add` drops the duplicate-installation check | `a second entry for one installation is refused, however it is named` |
| `remove` deletes the tuple before forgetting the secrets | `a removal whose secret round fails removes nothing at all` |
| `#read` stops checking the stored id against the key | `a stored tuple this module could not have written fails the whole list` |
| one cache namespace shared by every account | `no read from one account answers with another account's value` |
| the secret key composed without the account id | five secure-store tests, including `there is no shared token store` |
| `SecretStore.put` accepts a bare string | `only a Secret can reach the keychain: a bare string is refused` |
| `Secret.toString`/`toJSON`/`toPrimitive`/`inspect` returns the material | `printing a secret prints the placeholder, in every conversion` |
| the settings store stops refusing a `Secret` value | `a Secret is refused as a value, whatever its shape` |
| the settings key fence stops refusing the item names | the derivation test and two more |
| `items()` probes one of D111's two items instead of the list | three tests, including the derivation one |
| `forget()` forgets one of D111's two items | the derivation test and `forget removes both items of ONE account` |
| `forget()` gates the delete on a successful read (the orphan bug) | `forget deletes items it cannot READ`, `a removal while the keychain is locked leaves no credential behind` |
| `forget()` swallows a failed delete | `a removal whose secret round fails removes nothing at all` |
| `forget()` stops counting an unreadable item as present | `forget deletes a value it cannot re-validate, and says it was there` |
| the settings key fence stops exempting the account id | `every forbidden word is still a name the owner may give an installation`, `the owner may name an installation any word the alphabet allows` |
| `closeCurrent` leaves `#current` set | both `closeCurrent` tests |
| `closeCurrent` reports `changed` when nothing was open | `closing with nothing open is a no-op` |
| `#read` hands out a stored tuple that fails validation | `a stored tuple that is malformed is refused on every read path, never handed out` |
| the store's mutations stop queueing (`#serial` calls `fn` directly) | the two concurrent-`add`, switch-racing-removal and write-racing-removal tests |
| `switchTo` closes only the account it recorded, not every open one | `a switch closes every account open in the registry, not only the one it recorded` |
| an import of `../console/…` planted in `index.js` | `the app imports nothing outside mobile/ but the shared package` |
| `closeCurrent` swallows a failed teardown | `a close whose teardown failed still leaves no account open` |
| the settings word list regains `mac` | `a fence that refused the owner's own installation would be the wrong fence` |
| the Keychain port hardcodes the accessibility instead of reading it from the module | three adapter tests |
| the Keychain port writes without the accessibility option | three adapter tests |
| the port stops refusing a module without the constant | `a module that cannot promise THIS_DEVICE_ONLY is refused outright` |
| the AsyncStorage port calls a method the SDK does not have | `the port translates AsyncStorage's four methods, and renames one` |
| each boundary rule, planted in `src/app/root.js` | its own rule, by name |
| a boundary rule's pattern typo'd to match nothing | the positive control |

Three of those mutations found tests that **could not have failed**: `toString`
was shadowed by `Symbol.toPrimitive`, the settings item-name branch was dead
(both names were already in the word list), and the word list refused
`account.mac`, which is what the owner is likely to call their own machine. All
three were fixed rather than kept — and the same fix was then applied to
`secret.js`'s two names wherever they had been written out again
(`secure-store.js`'s `items()` and `forget()`): the list lives in one place, so
a third item is probed, forgotten and fenced by every module at once instead of
looking exactly like an item the owner never paired.

**A review of this task then found three more, and each one is worth reading as
a class rather than as an incident:**

* the read-gated delete above — a "cautious-looking" `if (!read.ok) continue`
  that produced the exact outcome the module exists to prevent;
* the `mac` fault again, one word short: the account id, which is the owner's
  word, was still being judged by the settings word list, so an installation
  called `token` or `apiKey` was refused. The account branch of the key fence is
  now its own rule, and the tests walk the **whole** word list as candidate ids
  rather than the one word that was noticed;
* `closeCurrent` had no test at all, and it is the second entrance to the same
  teardown `switchTo` is tested for. Its `changed: false` case, its
  teardown-failed case and the `current() === null` invariant are now asserted,
  and all three are mutations in the table above.

## The audio interfaces (P3.4)

Three ports, three fakes, and the thin pipeline that makes the plan's
verification a test rather than a claim. The plan's sentence is the whole
scope: *the interfaces exist (AudioIn, STT, TTS) and their fakes are used by at
least one test that exercises the voice pipeline's logic with no device and no
audio: a fake transcript becomes a message; a fake reply becomes an utterance;
an interrupted utterance is reported as interrupted.* Those three sentences are
the first three tests of `src/audio/pipeline.test.js`, in that order.

| Port | Methods | What it answers |
| --- | --- | --- |
| **AudioIn** (`audio-in.js`) | `start()`, `stop()`, `running()`, `on(kind, handler)` | `{ ok: true, changed }`, or **D122**'s `microphone-denied` / `background-audio-denied`; events `speech-start`, `speech-end`, `barge-in` |
| **SpeechToText** (`stt.js`) | `begin()`, `end()`, `cancel()`, `listening()`, `on(kind, handler)` | one turn at a time, `partial` events while the user speaks, one `final` per turn — every event carrying the `turnId` `begin()` answered with |
| **TextToSpeech** (`tts.js`) | `speak(text)`, `cancel()`, `speaking()` | `{ ok: true, spoken: true }`, `interrupted`, or a `bad-text` / `already-speaking` refusal |

**The fakes are the point, not a convenience.** D52 requires them from the
first task and Global Constraint 10 requires the voice logic to be in CI: every
test in `src/audio/` runs against `FakeAudioIn`, `FakeStt` and `FakeTts`, with
**no microphone, no synthesizer and no audio anywhere**. There is no timer in
any of them — a test decides when an utterance ends and when the engine
answers — so a barge-in is asserted at the exact moment it is decided, and the
~300 ms of §5 is the owner-run half (check 86). One fake is deliberately
*honest about the hard cases*, because a fake that only ever did what the
contract promises would make the protection untestable: `FakeStt` can deliver a
`final` or a `partial` for a turn it no longer holds (`lateFinal`,
`latePartial` — the flush that raced a cancel), and an engine that has lost the
turn outright (`outOfStep`). Those are the only ways to reach the pipeline's
guards, so without them the guards would be code no test held to anything.

**The three rules the pipeline holds**, each with its own test:

1. **A partial is not a message.** §5's screen shows what the user is still
   saying; only a `final` is composed by `message.js` and handed to the shared
   schema (P0.2), whose verdict is passed through unchanged. A transcript the
   schema refuses — the cough that produced the empty string — is a `refused`
   event and no message at all.
2. **A final from a turn the pipeline is not in is dropped, before an id is
   drawn.** The engine's flush after a `stop()`, or after a cancel, or — the
   case a state machine alone gets *wrong* — after a new `speech-start` already
   abandoned the flushing turn and opened its own, must not become a message.
   §5's grace period exists so that "cancel that" is possible, and this is the
   line that makes it true. **Which turn the words belong to is why every event
   carries a `turnId`** (`begin()`'s answer, echoed back): the state cannot say
   it, because the abandoned turn's flush arrives while the *new* turn is open,
   and composing it would send the cancelled text **and** swallow the text the
   user is actually saying.
3. **Any speech during playback interrupts it** — both of the capture path's
   two ways of saying that. `barge-in` is the echo-cancelled signal; a plain
   `speech-start` is all a simpler path can say, and a pipeline that honoured
   only the first would be one where the assistant talks over the user on
   exactly the devices that can least afford it.

**The turn outlives the end of speech, and it has an identity.** The
pipeline's state is `idle` / `open` / `flushing`, mirroring the port:
`speech-end` flushes the engine and the engine's `final` is what ends the turn.
A pipeline that closed its turn on `speech-end` would drop every final — every
message, always — which is the first mutation in the table below. Beside the
state it holds the **turn id**, and every event is compared against it: the
state says whether a turn is in progress, the id says whether the words are
*this* turn's.

**What the module deliberately does not have.** No capture path, no speech
engine, no audio decoder: D123 inherits Voice D46 (on-device engines only,
chosen by a bake-off measured on the oldest supported device — owner-run check
**85**, phase 5), and a cloud engine needs a second explicit owner decision.
`src/audio/index.test.js` is what keeps that true — the module's files, **at
every depth**, are a literal list, the door's exports are a literal list, and
the code is scanned for the things an engine would need. The walk is recursive
on purpose and the recursion has its own test: a non-recursive walk is a fence
one directory deep, and `src/audio/native/engine.js` would never enter the list,
the rules or the scan. (`boundary.test.js` walks recursively too, but it does
not close this gap: its import rule judges *path* specifiers, so a package name
like `react-native-voice` is not a thing it can see.)

| Rule | Exactly what the scan forbids | Why |
| --- | --- | --- |
| no clock | `Date`, `performance.now`, `hrtime` | D117: the clock is the server-anchored one the pipeline is handed (`now`), so this module reads none |
| no random source | `Math.random`, `crypto` | the message id is injected (`newId`), and a random source here would be one no test could pin |
| no timer | `setTimeout`, `setInterval`, `setImmediate`, `requestAnimationFrame` | §5's grace period and any silence timeout are the engine's business |
| no network | `fetch(`, `WebSocket`, `XMLHttpRequest`, `EventSource` | D123/D46: on-device only; a cloud engine needs a second, explicit owner decision |
| no device API | `getUserMedia`, `MediaRecorder`, `AudioContext`, `navigator`, `Permissions` | D122: the microphone is reached through the port, and the OS permission through whoever implements it |
| no samples, in any encoding | `pcm`, `samples`, `ArrayBuffer`, `Float32Array`, `Uint8Array`, `base64`, `Blob` | D118/F26: text crosses these ports and audio never does — so there is no buffer here to leak one through |
| no logging | `console.` | a transcript in a log line is a transcript outside the mailbox |
| nothing is read from the environment | `process.env` | an engine chosen by an environment variable is a configuration this module cannot see or test |

The middle column is not a summary of the rule, it **is** the rule: the scan
builds its pattern from exactly these tokens, and
`the README's rule table and the scan are the same list` reads this table and
compares it to the scan's own list, row for row and token for token (the two had
drifted: the device-API row did not name `Permissions`, and the samples row did
not name `Uint8Array`).

**The import rule, which is not a token scan:** no production file here imports
a package at all — every module specifier it names is one of this module's own
files or `shared/`. The tests may additionally import `node:` builtins, and that
is the whole of what they may reach; a test that needed a package would be a CI
that needed one.

Every rule has a **positive control** (it must match a planted line of real
code) and a **prose control** (it must not match the same line in a comment),
which is what lets these files' headers explain the rules they are held to.

### Mutations run against this task

Each was planted, the run went red on the named test, and the bug was removed.
The tests are run with `node --test "src/**/*.test.js"` — the app's whole
suite, all passing — and the first three sentences of `pipeline.test.js` are the
plan's own verification.

**The column names the tests that went red; it does not count them.** The cells
used to carry counts, and re-measuring every row at this revision (apply, run,
revert) found rows that no longer reproduced: `FakeStt.end()`'s count was not
the one the suite reports, the sibling-import row said test files "fail to load"
when the suite loads and the rules report those same tests as failures, and the
planted `Permissions.query` row named a single test when the undefined
identifier takes most of the audio suite down before the scan is even reached.
A count is a claim about a suite that grows — it is wrong the moment a test is
added, and a cascade makes it meaningless even sooner, because one broken
handler reports a failure in every test that opens a turn. So the rows that used
to explain a cascade with a number explain it in words, and a row that needs a
number to be interesting has a test too weak to be worth the number.

| Mutation | Went red |
| --- | --- |
| `endTurn` closes the turn instead of flushing it | the plan's sentence one first, then the tests that take a `final` |
| the `barge-in` signal is not wired to the speech handler | the plan's sentence three and `the barge-in is a speech-start too` first, then the pipeline's suite with them |
| speech no longer cancels the playback it interrupts | the plan's sentence three, `the barge-in is a speech-start too` |
| a partial is composed into a message | `a partial is shown and is never sent` |
| **`onFinal` keys on the state alone, not the turn id** | `a superseded turn's late final is not this turn's message` |
| **`onPartial` keys on the state alone, not the turn id** | `a partial from a turn this pipeline is not in is not shown` |
| `onPartial` stops checking whether the turn is open | `a partial from a turn this pipeline is not in is not shown` |
| **`begin()` reuses turn id 1 for every turn** | `every event names the turn it belongs to` and `an abandoned turn's revision is emitted for that turn, not the open one` |
| **`outOfStep` stops being validated at construction** | `an engine that has lost the turn says so` |
| **`beginTurn` stops reporting the engine's refusal** | `an engine that says it is already listening is reported` |
| **`endTurn` stops reporting the engine's refusal** | `an engine that refuses the end of speech is reported` |
| **the file walk stops recursing** | `the walk is recursive, so a file in a subdirectory is part of the module` |
| **`src/audio/native/index.js` imports `react-native-voice`** | `the module is exactly these files, at every depth`, the scan's non-empty control, and `the module imports its own files and shared/` |
| **`PIPELINE_EVENTS.includes(kind)` is removed from `on`** | `an unknown event and a handler that is not a function are programming errors` |
| **`isVoicePipeline` is weakened to two methods** | `the pipeline is six methods, and the check says so` |
| an empty transcript is repaired (`\|\| ' '`) instead of refused | `a transcript the schema refuses is refused here too` |
| the `unavailable` event loses the OS code | `D122: the OS refusing the microphone…` |
| every message reuses one id | `each message is its own id, sender and clock` |
| `FakeStt.end()` closes the turn (the engine's final has nowhere to land) | `the end of speech flushes the turn; the final is what ends it` and `one turn at a time: begin, end, and the refusals between them` |
| `FakeStt.partial` is allowed out of a flushed turn | `the end of speech flushes the turn` |
| an interrupted utterance settles as finished | the plan's sentence three, `the barge-in is a speech-start too: a simple capture path interrupts by speaking` and `stop() abandons the turn and interrupts an utterance in flight` |
| a second utterance is queued instead of refused | `one utterance at a time: a second is refused, not queued, and the first is untouched` and `a refused utterance is refused, not reported as an outcome`, and then the tests that follow them in both files — an utterance that never settles takes its file down with it |
| the two OS denials collapse into one code | `D122: the OS refusing the microphone and refusing background audio are different codes` and `D122: the OS refusing the microphone is a value, and an \`unavailable\` event` |
| an event carries `samples: []` | `an event carries the event, and never anything heard`, and the no-samples rule |
| a draft accepts a field the protocol does not have | `a field this draft does not have is refused with the protocol's own code` |
| `refuse()` stops freezing its verdict | `a refusal is a frozen value with a code` |
| the door grows one more export | `the door exports exactly this` |
| a **sibling** production file imports `@react-native-voice/voice` | the tests the nested file takes down — `the module is exactly these files, at every depth`, the scan's non-empty control, and the import rule — reported as failures, not as a file that will not load: the suite runs and the rules name the file |
| `Date.now()` planted in `pipeline.js` | `no clock` |
| **a `Permissions.query` call planted in `audio-in.js`** | `no device API` — the token the rule table did not name until this round — and, before the scan is reached, every test that constructs `FakeAudioIn`, which dies on the planted identifier itself (`start and stop are idempotent, and say what changed` is the first) |
| **a `Uint8Array` buffer planted in `stt.js`** | `no samples, in any encoding` — the other token the row had dropped |
| **the README's device-API row drops `Permissions`** | `the README's rule table and the scan are the same list` |
| **the README's samples row drops `Uint8Array`** | the same |
| **the scan gains a token (`Reflect`) the README does not name** | the same |
| **the scan drops a token (`performance.now`) the README names** | the same |
| **a README rule row is renamed** | the same |
| **the README's table header is renamed** | the same |
| **the README's tokens are reordered within a row** | the same |
| **the README loses a whole rule row** | the same |

The rule table and the scan are one list now, and the reason is that they were
two: `mobile/README.md` said the device-API rule forbade `getUserMedia`,
`MediaRecorder`, `AudioContext` and `navigator`, while the pattern beside it also
forbade `Permissions`, and the samples row listed five of the seven tokens the
pattern carried. The scan builds its pattern from its own token list
(`forbiddenPattern`) and `the README's rule table and the scan are the same
list` compares that list to the table, row for row and token for token — so the
documentation cannot promise less than the scan enforces. The prose control was
run the same way: the three tokens planted in a *comment* leave the suite green,
which is what the comment-stripping is for.

**Three tests were passing for the wrong reason, and each is now a mutation
above rather than a claim.** The `on(kind, handler)` guards threw a `TypeError`
either way — the guard, or the `handlers.get(undefined)` crash it prevents — so
they now assert the *message*; `isVoicePipeline` was probed with
`{ start: () => {} }`, which a check weakened to two methods still rejects, so
it is now probed with a real pipeline missing one method; and the partial
suppression and the two engine-refusal branches were unreachable with the
fakes as they were, so `FakeStt` grew `latePartial` and `outOfStep`.

One mutation found a bug in the **tests**: `messageDraft` was composing
`{ id: draft.id, … }`, so an absent field reached the schema as an explicit
`undefined` and its refusal was `wrong-type` rather than `missing-field` — the
caller would have been told the wrong fault. The draft now preserves presence,
and `missing-field` is reachable and asserted.

A later review found two more, and the first is worth reading as the class it
belongs to: **the pipeline was keyed on the turn's state, and a state cannot
say whose words arrived.** A review-supplied case — speech starting again while
the engine is still flushing, and the abandoned turn's `final` arriving after —
composed the cancelled text into a message *and* swallowed the text the user
was saying. The mirrored state is necessary and not sufficient; the turn id is
what makes the rule hold, and `begin()` answering with it is now part of the
port's contract. The second: the "no engine can arrive quietly" fence was one
directory deep, so `src/audio/native/engine.js` was outside the file list, the
token rules and the import scan — invisible to a directory that exists to
refuse exactly that. The walk is recursive now and has a control of its own,
because with no subdirectory in the tree yet, no other assertion in the file
could tell a recursive walk from a flat one.

## What is not verified here

* **The Expo/Metro toolchain.** It is not installed in the environment this was
  written in, and nothing under `mobile/` declares a runtime dependency yet —
  which is asserted, so the manifest cannot quietly grow one. There has been no
  `expo start`, no `expo lint`, no `eas build` and no device or simulator run.
  `package.json`'s `test` script is the whole of the verified surface.
* **The SDKs themselves.** `expo`, `expo-secure-store`,
  `@react-native-async-storage/async-storage`, `react` and `react-native` are
  faked in every test that touches them: what is tested is the shape of the
  calls this repository makes (including the accessibility constant, read off
  the module rather than guessed), not what the modules do. `index.js`, which
  imports the real ones, is untested for that reason.
* **`useSyncExternalStore`, and the demo as a running app.** The demo's
  subscription is `React.useSyncExternalStore(demo.subscribe, demo.state)`,
  passed into the shell as the `useDemo` hook (`index.js`); every test supplies
  its own. That React's hook calls `subscribe` the way this code assumes, that
  `Pressable` renders a tappable control, and that a reviewer can actually read
  the demo on a phone are all unverified here -- there has been no `expo start`
  and no device run, which is the first bullet in this list.
* **`WHEN_UNLOCKED_THIS_DEVICE_ONLY`'s name and value**, which come from the
  documented `expo-secure-store` API and are not exercised against the package.
  The port refuses to construct if the constant is missing, so an SDK rename is
  a loud failure rather than a silent downgrade to the platform default — but
  the constant has not been seen on a device here.
* **Every speech engine, and every acoustic behaviour.** Nothing under
  `src/audio/` is an engine: the ports are contracts and the tests drive them
  with fakes, which is D52's rule and P3.4's whole scope. Which engine is
  chosen is **owner-run check 85** (the bake-off, measured on the oldest
  supported device, local candidates only — D123/D46, phase 5), and the
  hands-free behaviour with its ~300 ms barge-in is **check 86**. So the
  microphone permission prompt, echo cancellation, the background audio mode
  and the actual interruption latency have not been exercised anywhere: D122's
  two denials are tested as *values the port returns*, which is what P3.5's
  "tap to speak" fallback is built on, not as an OS behaviour.
* **The voice screen (P3.5) and the mailbox client's use of this pipeline.** The
  screen is not here; the mailbox client (P3.6) is, but is not wired to this
  pipeline (it speaks through its own `src/mailbox/speaker.js` seam).
  The pipeline takes the message's `sender`, `newId` and `now` as injected
  values precisely because P3.6 owns them (D117's server-anchored clock, the
  account's identity), and **redaction, the MAC and the write are P3.6's**:
  `message.js` composes a message and the shared schema validates it, and
  nothing under `src/audio/` writes, signs or stores anything.
* **The release path and the checks that need a device.** Checks **80–89** are
  owner-run; **87** (multi-account switching with no cross-leakage) needs two
  real installations, one of them `belong`, and **89** is the store identity.
  The isolation is asserted here as a property over fakes — two accounts, no
  read answers with the other's value, no listener survives a switch — and that
  is a statement about this code, not about a phone. No iOS bundle identifier or
  Android package is committed: those are the owner's, and they differ per
  installation (D125).
* **The two limits the module names.** `src/mailbox/README.md` records them with
  their decisions: the phone does not check how old an outbox message is (a
  signed one with an old `at` is spoken; a replay needs the consumer's identity,
  which the rules require), and `acked` is outside the tag (P6.3/D171), so a
  token minted for this uid could pre-ack a genuine message -- which hides a
  message and cannot make anything act.
* **The mailbox against a real database, and its deployed rules.** P3.6's client
  is tested against the port's double, which models the two rules a re-send
  meets and nothing else: no auth, no listeners, no quota, no project. Checks
  **81** and **82** (the deployed rules' fence and the rules deploy) are
  owner-run and have not been run, so *what the deployed rules do with a
  re-write* is asserted here only as the double's model of them.
* **Metro, and the `shared/` imports.** `src/mailbox/` imports
  `../../../shared/mailbox-schema/index.js`, `../../../shared/presence/index.js`
  and `../../../shared/digest/redact.js`; node resolves them (the tests run) and
  the D119 boundary rule allows them, but **bundling `mobile/` and `shared/`
  together needs a Metro `watchFolders` entry that no task has added yet.**
  There has been no `expo start` here, so that wiring is unverified — see
  `src/mailbox/README.md`.
* **The Realtime Database itself (P3.2).** No database client is installed: the
  reader takes a port — `{ subscribe(path, { onValue, onError }) }` — and every
  test hands it a fake that records the paths it is asked for. So **no listener
  has ever been attached to a real project**, no document has ever come off a
  real tree, and the deployed grant has never been exercised from a phone. That
  is check **81**'s and check **84**'s job (owner-run), not this suite's.
* **The tree's real shapes.** `fixtures/nodes.js` is synthetic and written from
  `internal/budget/model.go`, not recorded from an installation — no fixture
  here may be recorded from `belong`, or from any live project (Global
  Constraint 11). A tree whose shape changed would be caught by the CLI's own
  tests; nothing in this app would notice a field it does not read, which is
  deliberate (the reader ignores what it does not use) and is exactly why the
  fixtures carry fields it does not use.
* **The clock.** Every instant in the feed's tests is injected, so no test has
  run against a real clock: the day rollover, the burn window and the silence
  thresholds are all arithmetic over an instant a test chose. Clock **skew** —
  "clock differs" — is check **84**'s, and `view.js` deliberately floors a
  negative age at zero rather than deciding what it means. What *is* asserted is
  that an instant this app cannot render (past `MAX_INSTANT_MS`, or not a
  number at all) is refused at every entry point rather than raising inside an
  assembly.
* **What the heartbeat means.** The reader reads `mailbox/$uid/heartbeat` and
  draws **no conclusion** from it: the heartbeat is delivered like any other
  granted node, and presence — live, stale, offline, and the boot id that says
  the orchestrator restarted — is check **84** (P4.2)'s rule, which has a real
  clock and a real database. A heartbeat that is not there is a node that is not
  there, and this task does not call that "the orchestrator is down".
