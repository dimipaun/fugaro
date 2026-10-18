# `mobile/` — the Fugaro app

The Expo (React Native) app for the phone. It is **its own artifact**: outside
the Go module, with its own release path, and it merges without touching the
CLI (mobile plan D125, Phase 3).

**Status.** Phase 3, tasks **P3.1** and **P3.4** — the app skeleton and the
account store
([`docs/mobile/implementation-plan.md`](../docs/mobile/implementation-plan.md)
line 455), and the audio interfaces. P3.2 (the feed reader), P3.3 (the job
list), P3.5 (the voice screen) and P3.6 (the mailbox client) are not here. What
exists is the shell, the account model, the two device ports and the fences
around them, and the three voice ports with the fakes the voice screen's logic
is written and tested against.

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
| `src/audio/` | The voice ports and their fakes (P3.4): the capture path (`audio-in.js`), transcription (`stt.js`), speech output (`tts.js`), the transcript-to-message rule (`message.js`) and the pipeline that wires them (`pipeline.js`). **No engine, no device, no SDK**: `index.js` is the one door, and its test scans the directory for exactly that. |
| `src/boundary.test.js` | The scan that says **which files may write a credential** (below). |
| `src/skeleton/` | The trivial module and test that proved the runner runs (P3.1's red step). |

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
| **SpeechToText** (`stt.js`) | `begin()`, `end()`, `cancel()`, `listening()`, `on(kind, handler)` | one turn at a time, `partial` events while the user speaks, one `final` per turn |
| **TextToSpeech** (`tts.js`) | `speak(text)`, `cancel()`, `speaking()` | `{ ok: true, spoken: true }`, `interrupted`, or a `bad-text` / `already-speaking` refusal |

**The fakes are the point, not a convenience.** D52 requires them from the
first task and Global Constraint 10 requires the voice logic to be in CI: every
test in `src/audio/` runs against `FakeAudioIn`, `FakeStt` and `FakeTts`, with
**no microphone, no synthesizer and no audio anywhere**. There is no timer in
any of them — a test decides when an utterance ends and when the engine
answers — so a barge-in is asserted at the exact moment it is decided, and the
~300 ms of §5 is the owner-run half (check 86). One fake is deliberately
*honest about the hard case*: `FakeStt.lateFinal()` delivers a final with no
turn open, which is what a real engine's flush does when it races a cancel, so
the pipeline's protection against it is a test rather than a comment.

**The three rules the pipeline holds**, each with its own test:

1. **A partial is not a message.** §5's screen shows what the user is still
   saying; only a `final` is composed by `message.js` and handed to the shared
   schema (P0.2), whose verdict is passed through unchanged. A transcript the
   schema refuses — the cough that produced the empty string — is a `refused`
   event and no message at all.
2. **A final with no turn open is dropped, before an id is drawn.** The engine's
   flush after a `stop()`, or racing a cancel, must not become a message: §5's
   grace period exists so that "cancel that" is possible, and this is the line
   that makes it true.
3. **Any speech during playback interrupts it** — both of the capture path's
   two ways of saying that. `barge-in` is the echo-cancelled signal; a plain
   `speech-start` is all a simpler path can say, and a pipeline that honoured
   only the first would be one where the assistant talks over the user on
   exactly the devices that can least afford it.

**The turn outlives the end of speech.** The pipeline's state is `idle` /
`open` / `flushing`, mirroring the port: `speech-end` flushes the engine and
the engine's `final` is what ends the turn. A pipeline that closed its turn on
`speech-end` would drop every final — every message, always — which is the
first mutation in the table below.

**What the module deliberately does not have.** No capture path, no speech
engine, no audio decoder: D123 inherits Voice D46 (on-device engines only,
chosen by a bake-off measured on the oldest supported device — owner-run check
**85**, phase 5), and a cloud engine needs a second explicit owner decision.
`src/audio/index.test.js` is what keeps that true — the directory's files are a
literal list, the door's exports are a literal list, and the code is scanned
for the things an engine would need:

| Rule | Why |
| --- | --- |
| no package import at all (production files) | an SDK here *is* the engine; the tests may import `node:` builtins and this directory only |
| no clock, no random source | D117's clock and the message id are injected (`now`, `newId`), which is what keeps every test deterministic |
| no timer | §5's grace period and any silence timeout are the engine's business |
| no network | D123/D46: on-device only |
| no device API (`getUserMedia`, `MediaRecorder`, `AudioContext`, `navigator`) | D122: the microphone is reached through the port, and the OS permission through whoever implements it |
| no samples, in any encoding (`pcm`, `samples`, `ArrayBuffer`, `Float32Array`, `Blob`, `base64`) | D118/F26: text crosses these ports and audio never does — so there is no buffer here to leak one through |
| no `console.` | a transcript in a log line is a transcript outside the mailbox |
| no `process.env` | an engine chosen by an environment variable is a configuration this module cannot see or test |

Every rule has a **positive control** (it must match a planted line of real
code) and a **prose control** (it must not match the same line in a comment),
which is what lets these files' headers explain the rules they are held to.

### Mutations run against this task

Each was planted, the run went red on the named test, and the bug was removed.
The tests are run with `node --test "src/**/*.test.js"` (178 tests, all
passing); the first three sentences of `pipeline.test.js` are the plan's own
verification.

| Mutation | Went red |
| --- | --- |
| `endTurn` closes the turn instead of flushing it | the plan's sentence one, and six more |
| the `barge-in` signal is not wired to the speech handler | the plan's sentence three, and fifteen more (the file's tests cancel as a promise never settles) |
| speech no longer cancels the playback it interrupts | the plan's sentence three, `the barge-in is a speech-start too` |
| a partial is composed into a message | `a partial is shown and is never sent` |
| the `idle` guard is removed from `onFinal` | `a final with no turn open is dropped` |
| an empty transcript is repaired (`\|\| ' '`) instead of refused | `a transcript the schema refuses is refused here too` |
| the `unavailable` event loses the OS code | `D122: the OS refusing the microphone…` |
| every message reuses one id | `each message is its own id, sender and clock` |
| `FakeStt.end()` closes the turn (the engine's final has nowhere to land) | eight tests, including `the end of speech flushes the turn` |
| a partial is allowed out of a flushed turn | `the end of speech flushes the turn` |
| an interrupted utterance settles as finished | four tests, including the plan's sentence three |
| a second utterance is queued instead of refused | `one utterance at a time` and the pipeline's refusal test |
| the two OS denials collapse into one code | both D122 tests |
| an event carries `samples: []` | `an event carries the event, and never anything heard`, and the no-samples rule |
| a draft accepts a field the protocol does not have | `a field this draft does not have is refused with the protocol's own code` |
| `refuse()` stops freezing its verdict | `a refusal is a frozen value with a code` |
| the door grows one more export | `the door exports exactly this` |
| a production file imports `@react-native-voice/voice` | three test files fail to load (module resolution) — the suite goes red before the rule can report it; the rule's own positive control is what proves the pattern matches |
| `Date.now()` planted in `pipeline.js` | `no clock` |

One mutation found a bug in the **tests**: `messageDraft` was composing
`{ id: draft.id, … }`, so an absent field reached the schema as an explicit
`undefined` and its refusal was `wrong-type` rather than `missing-field` — the
caller would have been told the wrong fault. The draft now preserves presence,
and `missing-field` is reachable and asserted.

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
* **The voice screen (P3.5) and the mailbox client (P3.6).** Neither is here.
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
