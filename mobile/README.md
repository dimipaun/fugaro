# `mobile/` — the Fugaro app

The Expo (React Native) app for the phone. It is **its own artifact**: outside
the Go module, with its own release path, and it merges without touching the
CLI (mobile plan D125, Phase 3).

**Status.** Phase 3: tasks **P3.1** (the app skeleton and the account store),
**P3.2** (the feed reader and the contract's document rules), **P3.3** (the job
list) and **P3.6** (the mailbox client) are here
([`docs/mobile/implementation-plan.md`](../docs/mobile/implementation-plan.md)).
P3.4 (the audio interfaces) and P3.5 (the voice screen) are not. What exists is
the shell, the account model, the two device ports, the fences around them, the
feed (the four granted families read directly from the Realtime Database, the
document they add up to, and the screen's rules over it), the job list (numbers
and states, and no run row), and the phone's mailbox client -- which is the only
place in the app that writes to the database.

**P3.4 is not merged, so P3.6 defines the one audio seam it needs** —
`src/mailbox/speaker.js`, `speak(text)` answering whether the utterance was
spoken, which is what the ack's rule is written against. The secure store is
**not** stubbed: P3.1's `SecretStore` is reused, and it gained one method
(`use`) for the reason `src/mailbox/README.md` gives.

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

## The layout

| Path | What it is |
| --- | --- |
| `index.js` | The entry. The one file that knows what a device is: it imports `expo`, `expo-secure-store`, `@react-native-async-storage/async-storage`, `react` and `react-native`, builds the two ports and the store, and registers the root. No decisions of its own, and no test (see "What is not verified"). |
| `app.json` | The Expo config. No credential, no installation, no bundle identifier, no Android package — see `src/app-config.test.js`. |
| `src/accounts/` | The account store: the installation tuple (`account.js`), the two items (`secret.js`), the secure store (`secure-store.js`), the settings store and its fences (`settings.js`), the per-account listeners and caches (`resources.js`), the store itself (`store.js`), and the two device ports (`expo-secure-store.js`, `expo-async-storage.js`). `index.js` is the one door. |
| `src/app/root.js` | The root shell. Renders the current account's id, and nothing else an installation was built from. |
| `src/mailbox/` | The mailbox client: the write paths and their key rules (`paths.js`), the app's SHA-256 and HMAC (`sha256.js`, `mac.js`), the redactor's seam (`redact.js`), the database port and its double (`store.js`), the audio stub (`speaker.js`) and the client itself (`client.js`). `index.js` is the one door; `write-path.test.js` is the scan that enforces the one-write-path claim; `mutation-proof.sh` proves the guards; `README.md` is the module's own rule sheet. |
| `src/feed/` | The feed reader: the four granted families and the gate every read passes (`paths.js`), the wire node shapes (`nodes.js`), the contract's document rules (`document.js`) and the two figures the phone recomputes (`recompute.js`), the screen's rules (`view.js`) and the reader itself (`reader.js`). `index.js` is the one door; `fixtures/nodes.js` is the synthetic recording the tests read. |
| `src/jobs/` | The job list (P3.3): the field table (`fields.js`), the project's row (`row.js`), the heartbeat line (`heartbeat.js`), the list and its wiring (`list.js`), what the screen says (`lines.js`) and the scope scan (`scope.test.js`). `index.js` is the one door; the section below is the rule sheet. |
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

## The job list: numbers and states, and nothing else

The list is what the phone is for — the spend, the cap, the kill switch and
whether the other end is alive — and the resolution of 2026-10-17 fixed its
scope in one sentence: **numbers and states only**, "with no run titles, no
repositories, no queued rows, no finished rows and no PR link". `src/jobs/` is
that screen's module, and its files are:

| File | What it holds |
| --- | --- |
| `fields.js` | the table: one entry per field on the row, each carrying the granted family it is read from (`JOB_FIELDS`), plus the closed vocabularies (`KILL_STATES`, `HEARTBEAT_STATES`) and the caption `NOTHING` |
| `row.js` | the project's row, built from P3.2's document: every field must be a **figure** the feed built, and a field that is not is a refusal rather than a coerced value |
| `heartbeat.js` | P3.6's presence reading as one state. `unknown` when nothing has read the mailbox — which is **not** `offline` (rule 3, "silence is not death", seen from this side) |
| `list.js` | `jobList()` — the pure judgement of a snapshot plus a presence reading — and `createJobList()`, the wiring the feed's door names as this task's |
| `lines.js` | the screen's lines, printed through the feed's own `shown` |
| `index.js` | the door |
| `scope.test.js` | the scan below |
| `row.test.js`, `heartbeat.test.js`, `list.test.js`, `lines.test.js` | the behavioural half |

### The row, and the three absences that must not be confused

`n/a` is what `shown` says for a field the document **did not carry**
(`unavailable`) — the feed owns that word, which is why this module defines no
second copy of it; `—` (`GAP_MARK`) is what a screen draws when the **series
broke**; and `not known` (`NOTHING`) is what a line says when the **thing
itself** is absent: the day, the project. Three absences, three different
strings — a test holds them apart, because two of them spelled the same would be
a screen a reader has to guess at. A `0` is a measurement and appears only where
a counter measured zero.
That difference is the whole of §10.7's "must not draw the zero": on a day
break the reader assembles the new day's document, which carries a zero counter,
and `jobList` draws `row: null` for it — so the zero cannot reach a screen that
forgot to check. A degraded feed (§10.8) is the same: it renders as degraded,
never as a spend of zero.

### What the list deliberately does not show

**No repository row and no repository name.** This is the reading of "no
repositories" this task took, and it is the smaller one: `config/caps/repos/$slug`
and `spend/$day/repos/$slug` *are* granted and P3.2's document carries a block
built from them, but nothing in `src/jobs/` reads that block — not the slug it is
keyed by, and not the repository's own name. A reviewer who reads the resolution
as "per-repository rows, without their identity" would want a row per slug with
no name on it; that reading needs the document's `repos` block and a screen that
draws it, and it is a bigger change than this task's text describes. The
project's own row is what is left, and `scope.test.js` refuses the token `repos`
in this module's production files so the choice is a line of code a later task
edits on purpose rather than a drift.

**No link of any kind.** The rows that carry a PR link live in the runs bucket
and the grant does not cover them — but the sharper reason is P0.3: any link the
app ever opens goes through `shared/url/index.js`'s validator, so a link built
here would be a link built around the gate. Nothing in this module imports it,
because nothing here builds a link. **P0.3 stays a dependency of P3.3**: the day
a row on this list wants a URL, the door to it is that validator and not a
`Linking.openURL`.

**No run title, and no code path that asks for one.** The plan's own test case
for this task, held as a scan (below) and as behaviour: the fake port records
every path the wiring asks for, and every one of them passes the grant's own
gate.

### The wiring

`createJobList({ port, account, repositories, registry, readPresence, now })`
builds P3.2's `FeedReader` from a **port it is handed** — no database SDK and no
`firebase` module is imported by this module, so a second subscription to the
same project is not something an import can buy — and takes P3.6's presence
report as a **function**. `poll()` stores the raw reading and `snapshot()`
judges it once, so the heartbeat has one judge and not two that can disagree. A
refusal from the mailbox client is passed through unchanged: its codes are its
own, and this module does not re-label another module's decision.

### The scan: the resolution's "not shown" as rules

`scope.test.js` holds what no behavioural test can hold on its own — that no
line reaches for these tomorrow — the way P3.1's boundary scan holds the
credentials: **tokens that may appear in a named list of files and nowhere
else**, read over the module's **code and not its prose** (this tree's comments
discuss the very tokens the rules forbid), with a **positive control per rule**
so a mistyped pattern cannot pass for a clean tree, and with test files exempt
(a test that proves a denied family is never requested has to name it).

| Rule | Why |
| --- | --- |
| `agents` / `outcomes` | the two families the read grant does not name (tension (b1)) |
| `repos` | the resolution's "no repositories" |
| `pr_url`, `prUrl`, `pr_number`, `prNumber`, `Linking`, `openURL`, `openExternal`, `WebBrowser` | the runs bucket's fields, and every way of opening a link |
| `../feed/` other than `../feed/index.js` | the feed is reached through its **door**: `paths.js`'s gate and `nodes.js`'s raw readers are the feed's business, and a screen that composed a path would be composing a read |

One rule is not a token rule: the door's surface is asserted as an **exact set**
(the rule P3.2's `index.test.js` gives) and **no name on it may be dead** — used
nowhere. The first version of that rule counted a name's own declaration as a use
and the scan file's own enumeration of the names as another, so it could not fail
and three dead exports (`NO_VALUE`, `PRINTED_FIELDS`, `jobField`) were on the door
behind it. The declarations are stripped before the search now, the scan file is
excluded from it, and the rule's three controls are the two shapes a dead export
has (declared-and-unused, and declared nowhere) and the shape that must not be
flagged (declared and used).

### The job list's mutations

Each was planted, the run went red, the bug was removed. Three were planted
**after** the rest and found claims with no test behind them: the tests they
went red against did not exist yet, so fixing that was writing them. The last
four are the review's own pass over the module's surface: three dead exports
(`NO_VALUE`, `PRINTED_FIELDS`, `jobField`, all gone), a caption mark spelled the
same as the contract's gap mark, and the dead-name rule itself — which passed a
planted dead export, that being the finding that the rule could not fail.

| Mutation | Went red |
| --- | --- |
| a field that is not a figure is put on the row anyway | `a figure the feed did not build is refused, not put on the row` |
| a rate marked `known` over no number renders as "not shown" | `a rate marked known over no number is refused…` |
| the kill switch always reads `off` | `a kill switch is on or off…` |
| a document flagged `degraded` is built into a row | `a row is only built from a live document` |
| a field with no figure prints a `0` | `a missing field prints n/a, and never a 0` |
| a field the feed did not build is printed as its bare value | `a field the feed did not build is not printed as a number` |
| a broken series draws the rollover document | `a broken series draws the gap mark…` |
| a degraded feed is drawn as a live one | `a degraded feed renders as degraded…` |
| every snapshot claims the day did not change | `the list is built from the feed and the mailbox…` |
| an unread mailbox is reported `offline` | `the heartbeat is unknown until something has read it…` |
| an unsigned reading counts as verified | `an unsigned heartbeat is carried as unverified…` |
| a negative age is carried | `the fields a caller could smuggle a claim through are judged` |
| the module reaches for a repository | `no repository row and no repository name` |
| a screen reaches around the feed's door | `the feed is reached through its door…` |
| a run is named | `no run is named, and neither family the grant denies is asked for` |
| the kill switch's two words are swapped (`KILL_STATES`) | `a kill switch is on or off…` |
| the caption mark becomes the gap mark | `the three absences are three different strings` |
| the door re-exports a name nothing uses | `the door carries the module's surface, and no dead name` |
| a declared-and-unused export on the door survives the rule | the same test, whose control asserts the two shapes are flagged |

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
* **`WHEN_UNLOCKED_THIS_DEVICE_ONLY`'s name and value**, which come from the
  documented `expo-secure-store` API and are not exercised against the package.
  The port refuses to construct if the constant is missing, so an SDK rename is
  a loud failure rather than a silent downgrade to the platform default — but
  the constant has not been seen on a device here.
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
* **The job list on a screen (P3.3).** `src/jobs/` fixes what the list *says*
  — `lines.js` returns the labelled strings a screen draws — and mounts nothing:
  no React tree, no component, no device, and no renderer has run. "No run title
  is on screen" is therefore a fact about a string list and a scan over the
  module's code, not about pixels. The list's heartbeat is P3.6's **reading**,
  handed in as a function; no mailbox has been read for it here, and the MAC
  check behind the reading is the mailbox client's (check **84**'s, owner-run).
  And **P0.3's validator is a dependency this task does not exercise**: nothing
  on the list is a link, so nothing calls `shared/url/index.js` — the day a row
  wants a URL, that module is the gate, and there is no test here that says it
  works.
