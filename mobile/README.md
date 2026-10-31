# `mobile/` — the Fugaro app

The Expo (React Native) app for the phone. It is **its own artifact**: outside
the Go module, with its own release path, and it merges without touching the
CLI (mobile plan D125, Phase 3).

**Status.** Phase 3: tasks **P3.1** (the app skeleton and the account store),
**P3.2** (the feed reader and the contract's document rules), **P3.3** (the job
list), **P3.4** (the audio interfaces), **P3.5** (the voice screen) and **P3.6**
(the mailbox client) are here
([`docs/mobile/implementation-plan.md`](../docs/mobile/implementation-plan.md)).
What exists is the shell, the account model, the two device ports, the fences
around them, the feed (the four granted families read directly from the Realtime
Database, the document they add up to, and the screen's rules over it), the job
list (numbers and states, and no run row), the phone's mailbox client -- which
is the only place in the app that writes to the database -- the three voice
ports with the fakes the voice screen's logic is written and tested against, and
the voice screen itself: the interrupt switch (D127), the queue it gates, P1.4's
offline refusal and P1.2's lease. It ships **without an engine**: the first
store build is voiceless (the owner's ruling of 2026-10-19), so what is here is
the screen's logic over the interfaces, which is what D126 asks CI to hold.

**Demo mode (the owner's ruling of 2026-10-19).** The app carries a built-in
demo, so that a Google Play reviewer -- who cannot create an account and cannot
pair a phone -- can reach everything the app shows. It runs the app's own code
on fixed sample data (`src/demo/`), it can never touch the network, secure
storage or a credential (`src/demo/safety.test.js`), and every screen it draws
says "Demo mode: sample data" with an always-visible way out (the shell,
`src/app/root.js`). **How to enter it** is the section below.

**The store path is here too (Task P4.4, added 2026-10-19).** The MVP is
voice-less and goes to **internal testing** first, so `app.config.js` (the
build-time config that resolves the two release identities from EAS
environment variables) and `eas.json` (the `development`, `preview` and
`internal` profiles) are in the tree, with their tests — see "Building for
internal testing". **Nothing is built or submitted by any of this**: Check 89
is owner-run, and no `eas` command has been run here. (The owner's own first
iOS submission, on **2026-10-20**, did succeed — EAS created the App Store
Connect app record and printed its `ASC App ID`, which `eas.json` now records so
EAS never asks for it again — but that was the owner's terminal, not this
repository.)

**The toolchain is declared too (added 2026-10-20).** `eas build --profile
internal` could not detect an SDK, because the manifest declared no dependency
at all; it now names **Expo SDK 58** and the four packages `index.js` imports,
`package-lock.json` pins the tree `npm ci` installs, and `metro.config.cjs` adds
the repository root to Metro's `watchFolders` so the app's `shared/` imports
bundle. `npx expo export` builds a JavaScript bundle for both platforms; a
native build is still owner-run — see "Building for internal testing" and "What
is not verified here".

**The Android launch crash, and the two things that came out of it (added
2026-10-21).** The owner installed the `preview` APK on Android on 2026-10-21
and it **opened and closed with no screen at all**. The cause was a `TypeError`
thrown while `index.js` was being *evaluated*: `expoSecureStorePort` required
`SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY`, and that constant **does not exist
on Android** — `expo-secure-store`'s `ios/SecureStoreModule.swift` registers
seven `Constant(...)` entries and its
`android/.../SecureStoreModule.kt` registers **none**, so the SDK's own
`export const WHEN_UNLOCKED_THIS_DEVICE_ONLY = ExpoSecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY`
is `undefined` there. Nothing else about the build was wrong:
`expo-secure-store` 58.0.2 is the version `node_modules/expo/bundledNativeModules.json`
pins for SDK 58, its config plugin is listed in `app.json`, and its native
module is linked and works on Android — it simply has no accessibility levels to
name. Two changes answer it:

* **the port takes the platform** (`expoSecureStorePort(SecureStore, { platform:
  Platform.OS })`). On iOS the constant is still required, so a rename is a loud
  refusal rather than a silent downgrade to `WHEN_UNLOCKED` — the item that
  rides an iCloud backup onto another phone; on Android no constant is asked for
  and the write carries no `keychainAccessible`, which the Kotlin
  `SecureStoreOptions` record has no field for. `src/accounts/expo-secure-store.js`
  has the full account, `src/accounts/expo-secure-store.test.js` the tests.
* **the startup is guarded** (`src/app/startup.js`, wired in `index.js`). The
  whole of the wiring — both ports, the store, the demo, the screens, the shell
  — is built inside `startup.root(build)`, so a throw from any of it renders a
  plain screen: what failed, the error's message, and a **"Copy details"** line
  a reviewer can long-press and paste. A root error boundary catches a
  render-time throw the same way, and `startup.guard(what, run)` — built on
  `report(error, what)` — is the door for a failure that arrives *after* a
  successful start, catching **both** halves: a rejected promise (its `try`
  cannot see what happens after an `await`) and a **synchronous** throw (a tap
  handler runs from the platform's event loop, not from `root`'s `try`, so a
  throw there is an uncaught error in a release build; the demo's entry is the
  app's first guarded step today, and a future SecureStore read, listener or
  fetch joins it). A release build on a phone has no terminal and no
  red box, so a startup failure that is not *drawn* is one nobody can report.

**Two audio seams, and now one of them is wired.** P3.6 defined the one audio
seam it needed -- `src/mailbox/speaker.js`, `speak(text)` answering whether the
utterance was spoken, which is what the ack's rule is written against -- before
P3.4 was merged. P3.4's `src/audio/` (`tts.js`) is the full speech-output port.
**P3.5 wired them** with `src/voice/speaker.js`, which puts the pipeline behind
the port the client asks for, so the client's "ack only after it has spoken"
now runs over the real audio ports (`src/voice/speaker.test.js` asserts both
halves through the real client). The mailbox module keeps its own
`MemorySpeaker`: it is the double that module's tests are written against, and
P3.5 does not own `src/mailbox/`. The secure store is **not** stubbed: P3.1's
`SecretStore` is reused, and it gained one method (`use`) for the reason
`src/mailbox/README.md` gives.

## Running the tests

```sh
cd mobile && npm test          # node --test "src/**/*.test.js"
```

**No install is needed to run them.** The app's runtime packages are declared in
`package.json` (see "Building for internal testing") and no test imports one:
every test reaches the platform through a fake, and `index.js` — the file that
imports the real modules — is not tested (see "What is not verified here").
`npm test` therefore runs in a fresh clone with no `node_modules`, and
`npm install` is a prerequisite of *bundling* the app, not of testing it.
`src/dependencies.test.js` is what keeps the declaration and the imports in
step.

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

1. Start the app on a device or simulator with the Expo toolchain, declared in
   `package.json` since this task (Expo SDK 58) and installed with
   `npm install` — see "Building for internal testing". **No `expo start` has
   been run in this environment** (see "What is not verified here"). The first
   screen appears: "Try the demo".
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
| `index.js` | The entry. The one file that knows what a device is: it imports `expo`, `expo-secure-store`, `@react-native-async-storage/async-storage`, `react` and `react-native`, builds the two ports (the Keychain one with `Platform.OS`, the constant that decides its rule being iOS's), the store and the demo controller, wires the two screens, and registers the root **inside the guarded startup**. No decisions of its own, and no test (see "What is not verified"). |
| `package.json`, `package-lock.json` | The manifest and the tree `npm ci` installs. The dependencies are the packages `index.js` and `src/` actually import — `expo`, `react`, `react-native`, `expo-secure-store` and `@react-native-async-storage/async-storage` — plus `expo-splash-screen`, which nothing imports and which `app.json` names as a **config plugin** (a plugin that is not installed is a prebuild that stops): `src/dependencies.test.js` fails on an undeclared import and on a manifest that lacks `expo`, `react` or `react-native`. |
| `metro.config.cjs` | Metro's config, and the one entry in it that matters: `watchFolders` includes the **repository root**, because the app imports `shared/` (`../../../shared/confirm/index.js` and its siblings) and Metro's project root is `mobile/`. Without it the bundle fails to resolve them — `npx expo export` stops at `Unable to resolve module ../../../shared/confirm/index.js` — so it is not removable, and `src/dependencies.test.js` fails if it goes. `.cjs`, because `package.json` is `"type": "module"`. |
| `app.json` | The Expo config. No credential, no installation, no bundle identifier, no Android package — see `src/app-config.test.js`. Three fields are what SDK 58 needs: `icon` and `android.adaptiveIcon` (`./assets/icon.png` and `./assets/adaptive-icon.png`, the two brand masters copied in — see `assets/` below and `src/app-config.icons.test.js`), `plugins: ["expo-secure-store", ["expo-splash-screen", …]]` (the module that holds D111's two items, whose config plugin is what configures it for a native build — `npx expo install` asks for it — and the splash SDK 58 has no top-level key for) and **no `edgeToEdgeEnabled` and no `newArchEnabled`**: `npx expo-doctor` reports each of the two as `Additional property "…" is not allowed` against the SDK 58 schema (one error per run, so removing the first is what makes the second visible — 19/20, then 19/20, then 20/20). Both flags ask for what SDK 58 builds anyway: edge-to-edge is mandatory from Android 16 (Expo's own `withEdgeToEdge` says so), and the new architecture is the only architecture. |
| `app.config.js` | **The build-time config** (P4.4), and the one place the two release identities come from: it resolves `FUGARO_IOS_BUNDLE_ID` and `FUGARO_ANDROID_PACKAGE` from the environment over `app.json`'s `expo` object — see "Building for internal testing" and `src/app-config.ids.test.js`. |
| `eas.json` | The EAS profiles: `development`, `preview` and `internal` (the store profile Check 89 builds and submits with). It carries exactly one store-side **identifier** — `ascAppId`, the App Store Connect app record's own public number, so EAS is never asked for it (the submit track, the store distribution and the AAB build type are store-side *settings*, not identifiers, and "Building for internal testing" below is where the track is called C8's whole content) — and no credential path, no credential value and no identity value — see `src/eas-config.test.js`. |
| `assets/` | The app's icons (the owner's task of 2026-10-21): `icon.png` and `adaptive-icon.png` are **byte copies** of `brand/app-icon-1024.png` (the opaque master — iOS refuses an icon with transparency) and `brand/app-icon-android-foreground-1024.png` (the transparent adaptive-icon foreground), which `scripts/gen-brand-icons.py` writes from `brand/fugaro-logo.png` alone — the masters are never edited by hand, and neither is this copy: change an icon by re-running the script and copying again, and `src/app-config.icons.test.js` fails on the bytes if it is forgotten. The splash shows the same `icon.png` over the masters' own ground colour, so the app's first screen is the logo on the brand's dark ground. |
| `src/accounts/` | The account store: the installation tuple (`account.js`), the two items (`secret.js`), the secure store (`secure-store.js`), the settings store and its fences (`settings.js`), the per-account listeners and caches (`resources.js`), the store itself (`store.js`), and the two device ports (`expo-secure-store.js`, `expo-async-storage.js`). `index.js` is the one door. |
| `src/app/root.js` | The root shell. Renders the current account's id, and nothing else an installation was built from; while demo mode is on it draws the banner and the way out on **every** screen, above whatever the screen holds. |
| `src/app/startup.js` | The guarded startup and the root error boundary (added 2026-10-21, after the Android launch crash): `startup.root(build)` builds the app inside a `try` and renders the failure screen if it throws, `report(error, what)` is the same door for a failure that arrives later, `guard(what, run)` wraps one step the app runs later -- a tap handler, an async startup step -- and catches **both** its synchronous throw and its rejected promise, because a handler runs from the platform's event loop and not from any `try` in this file, and `failureDetails`/`createFailureScreen` turn anything thrown -- a string, `undefined`, a circular object -- into what the phone shows: the step, the message, and a selectable "Copy details" line. `React` arrives as an argument that must carry `Component`, because an error boundary is a class. `src/app/startup.test.js` drives it with a fake React, a renderer and all. |
| `src/app/first-screen.js` | The first screen: what the app is waiting for, the "Try the demo" control, and the slot (`pairingScreen`) where P6.8's pairing screen goes -- above the demo control, never instead of it. |
| `src/demo/` | Demo mode (the owner's ruling of 2026-10-19): the fixed sample data (`sample.js`), the placeholder key source (`keys.js`), the transport that plants the consumer's side in the port double (`transport.js`), the controller (`index.js`) and the screen (`screen.js`). `safety.test.js` is the scan that holds the three fences (no network, no secure storage, no credential). |
| `src/mailbox/` | The mailbox client: the write paths and their key rules (`paths.js`), the app's SHA-256 and HMAC (`sha256.js`, `mac.js`), the redactor's seam (`redact.js`), the database port and its double (`store.js`), the connection port and its double and the offline gate's decision (`connection.js`, `gate.js` — P6.10/D167), the audio stub (`speaker.js`) and the client itself (`client.js`). `index.js` is the one door; `write-path.test.js` is the scan that enforces the one-write-path claim; `mutation-proof.sh` proves the guards; `README.md` is the module's own rule sheet. |
| `src/feed/` | The feed reader: the four granted families and the gate every read passes (`paths.js`), the wire node shapes (`nodes.js`), the contract's document rules (`document.js`) and the two figures the phone recomputes (`recompute.js`), the screen's rules (`view.js`) and the reader itself (`reader.js`). `index.js` is the one door; `fixtures/nodes.js` is the synthetic recording the tests read. |
| `src/jobs/` | The job list (P3.3): the field table (`fields.js`), the project's row (`row.js`), the heartbeat line (`heartbeat.js`), the list and its wiring (`list.js`), what the screen says (`lines.js`) and the scope scan (`scope.test.js`). `index.js` is the one door; the section below is the rule sheet. |
| `src/audio/` | The voice ports and their fakes (P3.4): the capture path (`audio-in.js`), transcription (`stt.js`), speech output (`tts.js`), the transcript-to-message rule (`message.js`) and the pipeline that wires them (`pipeline.js`). **No engine, no device, no SDK**: `index.js` is the one door, and its test scans the directory for exactly that. |
| `src/voice-session/` | The voice session's state machine (VS0.1–VS0.3 of `docs/mobile/voice-session/implementation-plan.md`): the event contract — the six kinds, their fields and the audio routes exactly as the spec's §5 writes them, plus the one field P8 adds (`interruptionBegan.suspended`, so a suspension notification is not read as a call) (`events.js`) — and the machine that reduces it (`machine.js`) — §4's table as one pure function, `reduce(state, event, ctx)`, with `ctx` carrying `{ now, resumeTimeoutMs, pipeline }`. **No clock, no timer, no I/O and no stored `speaking`**: `listening`/`speaking` are asked of P3.4's pipeline on every call that enters the pair (D205, D206, F3), §8's three minutes is a `tick` the caller injects, and D201 is open so the timeout is the caller's value with no default here. `index.js` is the one door, and its test scans the directory for exactly that — including two fences of its own: a paused session reaches no credential and no mailbox call (F24: nothing here resolves into `src/accounts/` or `src/mailbox/`, and the module's own code imports nothing outside its directory) and no file here carries words the user said (F9, D213). Nothing in the app imports it yet: the controller that drives it is VS1.1, and the native port that emits §5's events is VS1.2. Approved by the owner on 2026-10-22 (D205 answered as recommended; **VS0.1–VS0.3 and nothing else**). |
| `src/voice/` | The voice screen (P3.5) and the spoken confirmation (P6.12): the interrupt switch and the pure decision it gates (`interrupt.js`), the screen itself — the queue, P1.4's offline refusal, P1.2's lease and D122's microphone state (`screen.js`) — the adapter that puts P3.4's pipeline behind P3.6's speaker port (`speaker.js`), and the dialogue that routes a transcript, speaks a read-back and waits (`dialogue.js`). **No engine, no device, no database**: `index.js` is the one door, and its test scans the directory for exactly that. |
| `src/boundary.test.js` | The scan that says **which files may write a credential** (below). |
| `src/dependencies.test.js` | The scan that says **which packages the app is made of**: `expo`, `react` and `react-native` are declared, every bare import under `index.js` and `src/` is declared, and `package-lock.json` is the lockfile `npm ci` installs. It is what makes `eas build` able to find an SDK. |
| `src/skeleton/` | The trivial module and test that proved the runner runs (P3.1's red step). |

## Building for internal testing

The MVP is **voice-less** and goes to **internal testing first** (the owner's
ruling, 2026-10-19): TestFlight internal testers on iOS, the Play
internal-testing track on Android. Nothing public is intended or asked for — a
public release is not reversible in the way an internal one is (C8), and **no
agent runs any of the commands below**: each needs the owner's Expo, Apple and
Google accounts, and no Expo token, App Store Connect key or Play
service-account key goes into an agent's session (D120, Global Constraint 9).

**A development build, not Expo Go (D260).** The Firebase SDKs are native and
the app reads its Firebase configuration from native config files, so **Expo Go
cannot run this app** — every build below is a **development build** made by
EAS, and `npx expo start` against Expo Go is not a way to run it. The two native
files (`google-services.json` for Android, `GoogleService-Info.plist` for iOS)
reach the build as **EAS file environment variables**: on the worker each
variable's value is the path EAS wrote the file to, and the build's config
resolves that path onto `ios.googleServicesFile` / `android.googleServicesFile`
for the `@react-native-firebase` config plugin. **Neither file is ever
committed** — `mobile/.gitignore` names both — so an EAS build whose environment
carries no such file (or a path that names no file) **fails at config time, by
design**: the failure names the platform, the variable `mobile/app.config.js`
reads for it, and this section. Prebuild refuses a missing file as well, from
the plugin's own code and once the upload has already happened; this check is
the earlier and plainer stop on the same fact. The guard is
`native-config-guard.js`, and `src/native-config-guard.test.js` is its test.

**Prerequisites: run `npm install` in `mobile/` first; the SDK is Expo SDK 58.**
The manifest declares the app's runtime packages (`expo` 58.x, `react-native`
0.88, `react` 19.3, `expo-secure-store` 58.x and
`@react-native-async-storage/async-storage` 2.2), the versions `npx expo
install` picks for that SDK, and `package-lock.json` pins the tree `npm ci`
installs. `metro.config.cjs` is the other half of a bundle: it adds the
repository root to `watchFolders`, because the app imports `shared/`. What
**is** verified here is that the JavaScript resolves — `npx expo export` for
both platforms, see the pull request that added this paragraph — and what is
**not** is a native build or a submission: those need the owner's accounts, and
the list is at the end of this file ("What is not verified here").

**The two identities, and where they are not.** The iOS bundle identifier and
the Android package name are one public reverse-DNS name each — the same name
for both stores, per the ruling. They are **not in `app.json`** (the identity
is the owner's and the app's release path is its own, D125; `src/app-config.test.js`
is the boundary test that refuses them there) and **not in `eas.json`**
(`src/eas-config.test.js` refuses them there too). A build reads them from two
**EAS environment variables**, through
`mobile/app.config.js`, Expo's dynamic config — the `({ config }) => ({ ...config, ... })`
form Expo calls with the `expo` object it read from `app.json`:

| Variable | What it is |
| --- | --- |
| `FUGARO_IOS_BUNDLE_ID` | the iOS bundle identifier (`expo.ios.bundleIdentifier`): letters (either case), digits and hyphens in two or more dot-separated segments — Apple allows **no underscore** |
| `FUGARO_ANDROID_PACKAGE` | the Android package name (`expo.android.package`): an application id — two or more dot-separated segments, each **starting with a letter**, letters/digits/underscores only (**no hyphen**). **It can never change** once the app exists on Play, which is why a shape Play would refuse is refused here, while the fix is still free |

**The owner sets them, once per environment, in the EAS project** — the Expo
account that owns it, which is the owner's. Both values are the one public
reverse-DNS name the ruling gives; the commands below write it as
`<reverse-dns-name>`, because the identity is **not a value this repository
commits** — not in a build file, and not here — and the pull request that added
this section carries the two commands with the name in them for the owner to
paste.

```sh
# --- as the owner, signed in as the Expo account that owns the EAS project ---
IDENTITY='<the reverse-DNS identity>'   # one name for both stores (the 2026-10-19 ruling)
# All three environments: each build profile names the one it reads (eas.json),
# so whichever profile is built, the variables are there.
for env in development preview production; do
  eas env:set --environment "$env" --name FUGARO_IOS_BUNDLE_ID --value "$IDENTITY" --visibility plaintext
  eas env:set --environment "$env" --name FUGARO_ANDROID_PACKAGE --value "$IDENTITY" --visibility plaintext
done
```

`eas env:set` is the current EAS CLI command; `eas env:create` and
`eas env:update` are hidden and print a deprecation warning, so they are not
named here (the refusal messages in `app.config.js` name the same command).

`--visibility plaintext` is deliberate and is not a mistake to correct: these
are public identifiers, not credentials, and a value that is not secret gains
nothing from being hidden and loses the readability that makes a wrong one
obvious. **No credential goes into an environment variable, a profile or a
file of this repository** — the signing keys and the store credentials stay on
the EAS project (server-side, under the owner's account) or on the owner's own
machine, and `eas.json` names no key, no key path and no credential file
(`src/eas-config.test.js` walks it for exactly that).

```sh
# --- as the owner, at your own terminal: Check 89 (P4.4), owner-run ---
# `eas-cli` resolves app.config.js LOCALLY before it uploads anything, and it
# needs both identities there (to configure credentials and to submit) --
# `EAS_BUILD` is set by the build worker, not by the CLI. So export them for
# these commands; the value is the public identity, and the EAS environment of
# the profile carries it again on the worker.
export FUGARO_IOS_BUNDLE_ID='<the reverse-DNS identity>'
export FUGARO_ANDROID_PACKAGE="$FUGARO_IOS_BUNDLE_ID"
eas build --platform all --profile internal --non-interactive
eas submit --platform ios --profile internal --latest
eas submit --platform android --profile internal --latest
```

`internal` is the store profile: `distribution: store`, an Android
`app-bundle` (the Play app-signing path) and `submit.internal.android.track`
**exactly `internal`** — C8's whole content is that one value. The iOS submit
side names **exactly one** thing, and it is the app's own record on Apple's
side: `submit.internal.ios.ascAppId` is the App Store Connect app id EAS printed
when it created that record — `ASC App ID: 6824584935`, from the owner's first
submission of **2026-10-20** — so EAS is never asked for it again. It is a
**public number** on Apple's side of the submission and not a credential and not
one of the two identities: `src/eas-config.test.js` admits that one key, with
the digits Apple's record carries, and refuses every other entry of the section
— the bundle identifier, the Android package, the Apple ID, the team id and an
App Store Connect API key id included, whether it arrives as the name of an
entry or as the value of that one key. `cli.appVersionSource` is
`remote`, and what that puts on the project is the **build number** —
`expo.ios.buildNumber` and `expo.android.versionCode`: EAS holds it, so no file
here carries one. Remote means *stored* remotely, not *moved*: the `internal`
profile therefore sets `autoIncrement: true`, which is what makes EAS raise the
build number for each build — and that is not a convenience but the thing that
keeps a second internal build and submit from being refused, because App Store
Connect rejects a build whose number repeats one it already has (a *redundant
binary*) and Play rejects a duplicate version code, so a store profile with
`remote` and no `autoIncrement` builds once and then stops. It is on `internal`
alone, and the reason is the same one that names it the store profile:
`development` and `preview` are internal-distribution builds that reach no
store, so incrementing for them would spend this release's build numbers on
artifacts nobody ever submits. The build number is **not** the version the
stores show, and it is not the version check 89 step 4 compares with the tag:
that one is `expo.version` in `app.json`, the line the app's own release tag
(`mobile-vX.Y.Z`) names — and `version` in `package.json` with it, the two being
one number that `src/app-config.test.js` pins together. So releasing this app
means setting **both** to the version being released, in one commit, before the
tag (`0.0.0` in the tree is the placeholder a release replaces — nothing here
bumps either for you, and the staged release workflow refuses a tag that
disagrees with either). And `cli.requireCommit` is `true` (a store build records the commit it was
made from). `development` is a dev client on internal distribution and
`preview` is an internal-distribution build — neither reaches a store — and
each profile
names the EAS environment it builds with (`development`, `preview`, and
`production` for `internal`), which is why the variables are created in all
three above: a profile that named none would resolve EAS's default environment
instead.

**The submit step, on your own machine.** `eas build` uploads nothing to a
store: the submission is `eas submit`, and **it needs the two identities in that
shell as well**. `eas submit` resolves `app.config.js` in its own process, where
`EAS_BUILD` is unset (above), and on your machine it reads no EAS environment at
all — the `internal` profile's variables live in the EAS project's `production`
environment, which EAS loads for a *build job* and not for a local command. So
without them the submit stops before it uploads anything, with
`Specify ios.bundleIdentifier`; the owner's first submission (2026-10-20) hit
exactly that. Two ways to run it, and they are the same command:

```sh
# --- as the owner, at your own terminal: the submit, two ways ---
FUGARO_IOS_BUNDLE_ID=<the reverse-DNS identity> \
FUGARO_ANDROID_PACKAGE=<the reverse-DNS identity> \
  eas submit --profile internal --platform ios            # ... and --platform android
# or, with the environment the profile names loaded into this one command:
eas env:exec production 'eas submit --profile internal --platform ios'
```

`--latest` — the flag the Check 89 block above uses — submits the profile's most
recent finished build instead of asking which build; the two forms just above
are the same command without it. **Neither form has been run from this
repository**, and no agent runs either: the submission is the owner's (D120,
Check 89). What follows is what the stores do with the upload.

**After the iOS upload: TestFlight.** The submission is not the install — Apple
processes the build first, and the owner does four things:

1. **Apple processes the build** (about 5 to 10 minutes) and sends an email when
   it is done; in App Store Connect the build goes *Processing* → *Ready to
   test*. Nothing is installable until then.
2. **App Store Connect → TestFlight → the build → add yourself as an internal
   tester.** Internal testers need **no Apple review** — that is the whole
   reason this first release is an internal one (C8) — so the build is testable
   as soon as it is processed.
3. **Answer the export-compliance question, if the console asks it.** The build
   already carries the answer: `app.config.js` merges
   `ios.infoPlist.ITSAppUsesNonExemptEncryption: false` (the owner's declaration
   of 2026-10-20, in "The export-compliance answer is declared, not asked
   again." below), which is why the question is normally not put at all; if App
   Store Connect shows it anyway, the answer is the declared one.
4. **Install through the TestFlight app** on the device — the console hands out
   no build by itself. This is Check 89 step 4's install, and what it records is
   the version the app reports against the release's own tag, `mobile-vX.Y.Z`
   (C6's "the app is versioned with the CLI's tag" is superseded).

**The Android equivalent.** `eas submit --profile internal --platform android`
uploads the **AAB** to Play's **internal testing** track — `eas.json`'s
`submit.internal.android.track` is exactly `internal` (C8's one value) and the
AAB is the signed app bundle the Play app-signing path takes (Check 89 step 2's
FACT) — and then the release is handed to the testers the same way TestFlight's
is: the console shows it on the internal testing track, testers are added to
that track there, and the app installs from the Play internal-testing link.
**One prerequisite is Google's and not EAS's, and nothing here has verified
it**: a brand-new Play app's **first** upload has to be made once through the
Play Console's own UI, because neither the Play API nor `eas submit` can create
an app's first release; `eas submit` publishes the ones after it. No Play
command has been run from this repository — that step, the console's exact
wording and whether the internal-testing track needs anything else are read off
Google's documentation and from the owner's console, **not observed here**, and
they are on the unverified list below with the rest of the store path.

**What `app.config.js` refuses, and what it does not ask for.** Under a build —
`EAS_BUILD` is set on EAS's build servers, or the owner asks for the same check
locally with `FUGARO_REQUIRE_IDS=1` — a variable that is missing or malformed
is a **thrown error naming the variable and the fix**, so a build cannot
quietly produce an artifact with no identity or with a typo'd one; the message
describes the shape **that store** requires and **never repeats the value** (a
build log is the last place a mistyped variable should print). With neither
switch set — a developer's `expo start`, and `npm test` — **nothing is
required**, so no store account is needed to run anything here; a variable that
is set is still validated and used (that is the state `eas-cli` resolves the
file in, above), and a static identity planted in `app.json` is dropped rather
than passed through, so the file stays the single site for them. **The native
config files are required by the same two switches**, with one difference: when
that check runs, `EAS_BUILD_PLATFORM` narrows it to the platform EAS named — so
`FUGARO_REQUIRE_IDS=1` on its own, and any build whose environment names no
platform (which is what `eas-cli`'s local resolution looks like), requires
**both** files, while one naming `android` requires only Android's. The file
reads the two identities, the two native config variables, the two switches and
that platform name, and **nothing else in the environment reaches the config**:
it holds no credential, and it cannot.

**The export-compliance answer is declared, not asked again.** `app.config.js`
merges `ios.infoPlist.ITSAppUsesNonExemptEncryption: false` into every
resolution — the owner's declaration of **2026-10-20** ("write it, it's OK":
the app uses only standard/exempt encryption), made after `eas build` printed
that it cannot write that key into a dynamic config — while **the owner answers
Apple's other export questions in App Store Connect**.

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
WHEN_UNLOCKED_THIS_DEVICE_ONLY` **on iOS** (`src/accounts/expo-secure-store.js`;
Android's store is SharedPreferences behind the Keystore, declares no
accessibility constant and takes no such option, which is the platform split
the 2026-10-21 crash was about). That
choice is the one security decision the port makes, and it has its own test: it
keeps an item out of an **iCloud Keychain backup** and off a **restored-to-
another-phone** device. The MAC key proves possession of the pairing, so a key
that could be restored anywhere would restore the ability to speak as this
installation — `per-pairing` in name only. **On iOS** it is also not readable
while the phone is locked, which is what `WHEN_UNLOCKED` rather than
`AFTER_FIRST_UNLOCK` buys and what the voice-session reconciliation's **F24**
(and VS5.1, a paused session touches no credential) rests on. **That sentence is
iOS's, and this branch has just made that distinction load-bearing**: Android's
store is SharedPreferences behind the Keystore, has no accessibility constant to
set, and its key is **not** unlock-gated, so F24 is **not** a property of the
Android build — an Android session paused on a call can still read its pairing
key. That is read off the installed module, not assumed:
`expo-secure-store/android/.../encryptors/AESEncryptor.kt:64` is
`.setUserAuthenticationRequired(options.requireAuthentication)`, and this app
never sets `requireAuthentication`, so the Android key is generated
authentication-free (there is no `setUnlockedDeviceRequired` call either).
Nothing in this repository asserts a cross-platform version of F24: F24's own
evidence is the port's **iOS** rule, and neither platform's constant has been
exercised on a device (see the unverified list below).
`requireAuthentication` (a Face ID prompt on every read) is
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
forgot to check. A degraded feed (§10.8) draws no row either, and never a spend
of zero.

**A degraded feed is not a broken series.** §10.8's state is its own: the feed's
view reports `broken: false` beside `degraded: true`, and the Series line has a
third word, `degraded`. What the two share is that neither can draw a row — that
is `row === null`, one flag for the screen's word and one for the drawing, and
folding them together is how "break" reached a screen whose feed never said its
series broke. A degraded document is also the one thing that names its project
and carries the CLI's static `notice`, and both are on the list: the view's
`note` belongs to a skipped or refused line and a successful ingest clears it,
so the sentence would otherwise be lost between the document and the screen.

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

### The heartbeat line, and a negative age

**A negative age is a reading, not a claim smuggled in**, and it is why neither
`heartbeat.js` nor `lines.js` floors one at zero. P1.4 computes
`ageMs = now - at`, so a heartbeat stamped *after* the phone's clock arrives
negative: `live` while the skew is inside its future tolerance (P1.4's own test
pins `ageMs = -60000`) and `clock-differs` beyond it — that branch is
`ageMs < -futureToleranceMs` and nothing else, which is D117's fourth state and
the reason it exists. Refusing the sign made the wiring drop a good reading back
into `unknown` and made `jobList` refuse the whole list — spend, caps and the
kill switch — for the one phone whose clock is ahead. The reading is judged on
what it is made of (`'3s'`, `NaN`, `Infinity` are refused), not on its sign.

The screen draws it the other way round: **an age the phone cannot state is not
printed as one.** The state is printed alone for a negative age, because
`live (-60s ago)` and `clock-differs (-600s ago)` are durations with a minus
sign on them, and for `clock-differs` the age is not a fact this phone can state
at all. The sign is P1.4's arithmetic; the state is the fact. A reading with an
age the phone *can* state keeps it — `stale (190s ago)`.

**And a value that is not a number is not a duration either.** `heartbeatRow`
refuses one, so no reading the wiring makes can carry it — but `lines()` sits on
the screen's edge and asks only that the list be `jobList`-**shaped**, so a list
assembled by hand reaches `ageText` with anything in the heartbeat. `NaN < 0` is
false and `'3s' < 0` is false, so a sign test alone let both through to the
arithmetic and the line read `live (NaNs ago)`. The state is printed alone
there too, and the test is `Number.isFinite` so that `Infinity` goes with them.

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

Two rules are not token rules. The door's surface is asserted as an **exact set**
(the rule P3.2's `index.test.js` gives) and **no name on it may be dead** — used
nowhere. The set is read from the **module** (`import()` the door, `Object.keys`),
not from its text: the first version collected `export { … } from '…'` blocks out
of the source, which is one of the forms an export can be written in, so an
`export const GHOST = 1` appended to the door was reachable from a screen and
invisible both to that rule and to the dead-name rule below it — one blind spot
shared by two rules. The first version of that rule counted a name's own declaration as a use
and the scan file's own enumeration of the names as another, so it could not fail
and three dead exports (`NO_VALUE`, `PRINTED_FIELDS`, `jobField`) were on the door
behind it. The declarations are stripped before the search now, the scan file is
excluded from it, and the rule's three controls are the two shapes a dead export
has (declared-and-unused, and declared nowhere) and the shape that must not be
flagged (declared and used).

The second walks the **members of an exported vocabulary** rather than the door's
names, which is the gap `LIST_CODES.BAD_NOW` shipped through: a refusal code
declared beside the five that are used, exported on the door as part of
`LIST_CODES`, and produced by no code path here -- the clock refusal a caller
actually sees is `reader.js`'s own `READER_CODES.BAD_NOW`. A member nothing
produces is a member a reader has to check is still true.

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
| a negative age is refused, so a skewed clock is no reading at all | `a negative age is carried, because P1.4 is where one comes from`, and the same case through the wiring |
| the age is printed with its sign (`live (-60s ago)`) | `an age the phone cannot state is not printed as one` |
| the module reaches for a repository | `no repository row and no repository name` |
| a screen reaches around the feed's door | `the feed is reached through its door…` |
| a run is named | `no run is named, and neither family the grant denies is asked for` |
| the kill switch's two words are swapped (`KILL_STATES`) | `a kill switch is on or off…` |
| the caption mark becomes the gap mark | `the three absences are three different strings` |
| the door re-exports a name nothing uses | `the door carries the module's surface, and no dead name` |
| a declared-and-unused export on the door survives the rule | the same test, whose control asserts the two shapes are flagged |
| a degraded feed is folded back into `broken` | `a degraded feed is degraded, and not a broken series` |
| the Series line has no word for a degraded read | the same test |
| a degraded read draws a row after all | the same test |
| the degraded document's project is discarded | `a degraded read still says which project it is about, and why` |
| the degraded document's notice is discarded | the same test |
| a refusal code nothing produces is declared again (`BAD_NOW`) | `every member of an exported vocabulary is produced somewhere` |
| an age that is not a number is printed as one (`live (NaNs ago)`) | `an age the phone cannot state is not printed as one` |
| a name is added to the door as `export const GHOST = 1` (a form the old scanner could not see) | `the door carries the module's surface, and no dead name` |
| a name is added to the door as `export function GHOST() {}`, and as `export { LOCAL as GHOST }` | the same test |

### The follow-up round: the review's leftovers, checked

The follow-up run of 2026-10-19 was handed what the review was said to have left
open against #666: a degraded feed folded into `broken`, the degraded document's
`project` and `notice` discarded, and `LIST_CODES.BAD_NOW` declared and produced
nowhere. **Each was read against the merged code before anything was changed,
and all three were already held there** -- the merge carries the commit that
closed them (`mobile: a degraded read is its own state, and BAD_NOW is gone`),
and the tests that hold them are the rows above. A
finding whose guard is in the tree is dismissed rather than re-fixed to look
like work, so this round changed no production file and no test.

What the round ran was the table's own instrument, against `main` again: each
bug planted, the suite seen red, the bug removed.

| Planted against the merged code | Went red |
| --- | --- |
| `broken: judged.broken \|\| judged.degraded` (`list.js`) | `a degraded feed is degraded, and not a broken series` |
| the Series line has no `degraded` word (`lines.js`) | the same test |
| `project: null` for a degraded read (`list.js`) | `a degraded read still says which project it is about, and why` |
| the degraded document's `notice` dropped (`list.js`) | the same test |
| `BAD_NOW` declared beside the five codes that are produced (`fields.js`) | `every member of an exported vocabulary is produced somewhere` |

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

## The voice screen (P3.5)

The screen P3.4's ports and P3.6's client were built for, and the four
decisions the plan gives it. The plan's verification is four sentences, and
`src/voice/screen.test.js` and `src/voice/interrupt.test.js` are those four, in
order, over the fakes -- **no device, no network, no database, no microphone**
(Global Constraint 10). The pipeline, the mailbox client and the three audio
ports are the **real** merged modules, each built the way the app builds it,
with only the device replaced: the database is `MemoryMailboxStore`, the
speaker is P3.4's pipeline, and the microphone, the engine and the voice are
P3.4's fakes. That is what makes "the app refuses to accept a message" a claim
about **the store's write log** rather than about a returned value.

**It ships without an engine.** The first store build is voiceless (the owner's
ruling of 2026-10-19), so this is the screen's *logic*: no speech engine, no
speech recognition, no audio. D123 inherits Voice D46 -- on-device engines only,
chosen by a bake-off measured on the oldest supported device, **owner-run check
85** -- and D52 is why the logic is in CI at all.

| Piece | What it is |
| --- | --- |
| `interrupt.js` | **D127**: `INTERRUPT_SWITCH` (a local phone setting, never written to the database) and `interruptDecision({ switch, severity, highest, speaking, turn })`, a pure function over P1.3's ladder |
| `screen.js` | The screen: the queue the switch drains, P1.4's offline refusal at the one door every message goes through, P1.2's lease, D122's microphone state, and one frozen snapshot a screen renders |
| `speaker.js` | The wiring `mobile/README.md` said this task would do: P3.4's pipeline answering P3.6's `speak(text)` port, so the client's "ack only after it has spoken" runs over the real audio ports |

**The four rules, each with its own tests:**

1. **With the switch off a proactive update is queued, and not spoken over the
   user.** The switch is judged before the severity and before the boundary, so
   the reason a screen shows names the switch -- and with it off nothing is ever
   spliced out of the queue. Turning it on *is* the boundary: the queue drains
   at once if nothing else is happening.
2. **With it on, an update is spoken at the next boundary and never over an
   in-progress utterance.** A boundary is an event the pipeline reports, not a
   delay: an utterance finishing (`utterance`), a turn closing (the engine's
   `final` becoming a `message`), or the owner turning the switch on. The
   decision is asked with the pipeline's own `speaking()` and `turn()`, so an
   update that may not interrupt **stays queued** rather than being lost to a
   refusal, and one utterance is spoken at a time.
3. **The offline state shows from P1.4, and the app refuses to accept a
   message while offline.** The presence is P1.4's own answer read through the
   client (`poll`), and the refusal is one line at the one door every message
   goes through -- the pipeline's `message` event and a typed one alike. The
   spec's sentence is *rather than accepting messages into the void*, and it is
   asserted as **the store's write log**, before and after. **Only `offline`
   refuses**: a `stale` orchestrator is one the phone can still talk to (the
   mailbox is not the heartbeat), and `clock-differs` is not a state P1.4 has
   yet. Two things about that refusal are the review's rather than the plan's,
   and both are rules that only hold where they are used: the snapshot's
   `refused` is **the last refusal, cleared by the message that is accepted
   after it** (a status line that went on saying `offline` over a message that
   had just left the phone would be showing a state the phone is not in), and
   the presence is adopted through **one guard on both paths** -- `poll` calls
   the same `setPresence` a caller does, so "a state outside P1.4's four is
   refused rather than stored" is not a rule enforced only where the app does
   not go.
4. **The digest's highest severity is the only thing that interrupts when the
   switch is on.** `highest` is P1.3's `digest.severity` -- the most severe item
   the document carries -- and an update whose severity is not it waits. The
   consequence is stated rather than hidden: an update **above** a changed top
   (a `fast_burn` queued while a failure was the top, after the digest stops
   carrying one) also waits, because the rule is equality with the digest's own
   highest. The queue is bounded and **refuses rather than evicting**, which is
   what eventually surfaces it; a silent drop is the failure this module is
   written against.

**The lease is surfaced, not enforced.** P3.5's sentence is *a lease held by
another consumer is surfaced rather than ignored*, and that is the whole of what
this does: P1.2's verdict is returned **verbatim** (`already-claimed` with its
`existing` claim, `not-holder` on a release that is not the phone's), the holder
is in the snapshot a screen renders, and the state P1.2 hands back is adopted
whether or not the take succeeded. **Nothing is gated on it**, deliberately: a
lease held by a laptop that died mid-run would otherwise silence the owner's
phone -- a failure mode worse than the one it prevents, and a policy the plan
does not ask for.

**D122's degradation is a state, not a control.** The OS refusing the microphone
(or the background audio mode) is shown in the snapshot, which is what the "tap
to speak" control is built on; the screen's queue, switch and mailbox keep
working, and the refusal itself is P3.4's value passed through untouched.

**Where the plan's text and the merged code disagree.** P3.5's verification says
the digest's highest severity is *the only thing* that interrupts when the
switch is on. `shared/prompts/` (P6.5, merged before this task) carries a wider
`evaluateInterrupt` table: a cap being reached, a cap being near and the
live-to-offline transition also interrupt there. **P3.5's dependency list names
P1.2, P1.3, P1.4, P3.4 and P3.6 and not P6.5**, so this module holds the
sentence the plan writes for this task and nothing else, and says so in
`interrupt.js`'s header. When P6.5's queue is wired to this screen, the wider
table is the thing to compose with, deliberately and with its own tests. The
same is true of P6.10, which widens the offline refusal later.

**P6.12's seam is `accept` and the pipeline's events, and it is taken.**
P3.5 left the screen with one door for a message and no speech of its own; P6.12
(below) passes the screen an optional `utterance` delegate, so a final
transcript is routed instead of accepted, and the words the router calls agent
text go back through `accept` -- which is why the offline refusal still stands
over the voice path. Absent the delegate the screen is what P3.5 shipped.

**The scan, and what it keeps true.** `src/voice/index.test.js` pins the
module's files **at every depth** (the walk is recursive and the recursion has
its own control, because a fence one directory deep is not a fence), pins the
door's exports against a literal, and scans the code for the things an engine
or a second write path would need. This module sits above `src/audio/`,
`src/mailbox/` and `src/accounts/` and wires them together, so its import rule
is **"inside `mobile/` or `shared/`, and no package at all"** -- the fence
`boundary.test.js` uses, rather than the narrower one P3.4's scan can afford at
the bottom of the tree. `boundary.test.js` cannot close that gap itself: it
judges *path* specifiers, so a package name like `react-native-voice` is not a
thing it can see.

| Rule | Exactly what the voice scan forbids | Why |
| --- | --- | --- |
| no clock | `Date`, `performance.now`, `hrtime` | D117: the clock is the server-anchored one the screen is handed, so this module reads none |
| no random source | `Math.random`, `crypto` | the message id is the client's (`newId`), and a random source here would be one no test could pin |
| no timer | `setTimeout`, `setInterval`, `setImmediate`, `requestAnimationFrame` | a boundary is an event the pipeline reports, never a delay this module waits out |
| no network | `fetch(`, `WebSocket`, `XMLHttpRequest`, `EventSource` | D123/D46: on-device engines only, and every byte that leaves the phone leaves through the client |
| no device API | `getUserMedia`, `MediaRecorder`, `AudioContext`, `navigator`, `Permissions` | D122: the microphone is reached through the pipeline, and the OS permission through whoever implements the port |
| no database of its own | `firebase`, `Firestore`, `getDatabase`, `reference(`, `AsyncStorage` | D113: the mailbox client is the app's only write path, and a screen that could reach a database could be a second one |
| no samples, in any encoding | `pcm`, `samples`, `ArrayBuffer`, `Float32Array`, `Uint8Array`, `base64`, `Blob` | D118/F26: text crosses these ports and audio never does -- so there is no buffer here to leak one through |
| no logging | `console.` | a transcript in a log line is a transcript outside the mailbox |
| nothing is read from the environment | `process.env` | a screen configured by an environment variable is a screen no test can build the same way twice |

The middle column is not a summary of the rule, it **is** the rule: the scan
builds its pattern from exactly these tokens, and
`the README's rule table and the scan are the same list` reads this table and
compares it to the scan's own list, row for row and token for token. The header
is deliberately **not** the same string as P3.4's, so the two tables cannot be
confused for one another. Every rule has a **positive control** (it must match
a planted line of real code) and a **prose control** (it must not match the same
line in a comment), which is what lets these files' headers explain the rules
they are held to.

### Mutations run against this task

Each was planted, the suite went red on the named tests, and the bug was
removed. **The whole table is reproduced by `src/voice/mutation-proof.sh`**,
which prints one line per mutation with the tests that went red beside it, so
the table can be checked rather than trusted. **Two things keep that claim
honest, and a review found both wanting.** The names are read out of the
runner's own output, and node 23+'s spec reporter prints a `✖ failing tests:`
**summary header** above the failures it lists in full -- which the filter read
as a test name, so every line of the run carried one name no suite has; it is
dropped by name now, and the two reporters (TAP's `not ok N - name` and the spec
reporter's `✖ name`) yield the same list. And the table itself had gone stale:
two rows named two of the three tests their mutation really reddens, because the
third test was added to the suite after those rows were written. The whole table
was regenerated from the script's output, and all 49 rows now agree with it
name for name (the last seven are P6.12's, added with
the dialogue below) -- which is the comparison a reader can repeat, so it is worth
repeating after any change to this suite. That script runs this module's own
suite (`node --test 'src/voice/*.test.js'`, from `mobile/`) and not the app's
whole one, because the mutations are all here and a failure in a neighbouring
module would be indistinguishable from a killed guard; it **finds a working
node** rather than assuming one (this repository's base image keeps node off
`PATH` on purpose); and it refuses to report a verdict it could not have run --
the pristine suite must pass first, with a test count, because `node --test`
exits 0 over a glob that matched nothing, and then a mutation that *must* be
caught is planted before any real one is trusted. `npm test` --
`node --test "src/**/*.test.js"`, the app's whole suite, **667 tests, all
passing** -- is what the pull request reports as the module's verification.

**The column names the tests that went red; it does not count them**, for the
reason P3.4's table gives: a count is a claim about a suite that grows, and a
cascade makes it meaningless even sooner.

One line is deliberately **not** in the table. `drain` returns after the one
utterance it starts, and removing that `return` does **not** make the suite go
red: the loop asks `interruptDecision` again on its next pass, and P3.4's fake
flips `speaking()` synchronously inside `say`, so a second update is refused for
the reason the tests already prove. The `return` is there for an engine that
answers asynchronously, and no test written against a synchronous fake can tell
the two apart -- so it is written down here rather than claimed as a kill.

| Mutation | Went red |
| --- | --- |
| `the ladder is ranked, not compared as a word` | `a switch that is neither off nor on, and a severity off the ladder, are wiring bugs and they throw` |
| `the boundary is judged after the severity` | `the severity is judged before the boundary, so a low severity says so even while the user speaks` |
| `the switch is judged first, before the ladder` | `with the switch off nothing interrupts, whatever the severity is` |
| `an utterance in flight is a boundary` | `an in-progress utterance is never spoken over` and `with the switch on, an update waits for the boundary and is never spoken over an utterance` and `one utterance at a time: two queued updates are spoken one after the other, not together` |
| `the user's turn is a boundary` | `the user's turn is a boundary too: nothing interrupts while they are being listened to` and `with the switch on, an update waits for the user's turn to close, and is spoken after it` |
| `the switch is validated` | `a switch that is neither off nor on, and a severity off the ladder, are wiring bugs and they throw` |
| `the queue is drained only when the switch is on` | `the plan, sentence one: with the switch off a proactive update is queued, and not spoken` and `the queue is bounded, and refuses rather than dropping what is in it` |
| `an update below the digest's top is not spoken` | `the plan, sentence four: the digest's highest severity is the only thing that interrupts` |
| `the pipeline's own speaking() and turn() are what the decision sees` | `with the switch on, an update waits for the boundary and is never spoken over an utterance` and `with the switch on, an update waits for the user's turn to close, and is spoken after it` and `one utterance at a time: two queued updates are spoken one after the other, not together` |
| `an update is queued only against a digest that has arrived` | `a digest that arrives after the queue does not speak an update it does not cover` |
| `an update below the digest's top is queued, not dropped` | `the plan, sentence four: the digest's highest severity is the only thing that interrupts` |
| `a full queue refuses rather than evicting` | `the queue is bounded, and refuses rather than dropping what is in it` |
| `the utterance ending is a boundary` | `with the switch on, an update waits for the boundary and is never spoken over an utterance` and `one utterance at a time: two queued updates are spoken one after the other, not together` |
| `the turn's final is a boundary` | `with the switch on, an update waits for the user's turn to close, and is spoken after it` |
| `the turn's final is a message the app accepts` | `with the switch on, an update waits for the user's turn to close, and is spoken after it` and `a transcript that arrives while offline is refused too, and nothing is written` and `a final transcript goes to the spoken dialogue when the app wires one in (P6.12)` |
| `the OS's refusal of the microphone is recorded` | `D122: a refused microphone is shown, and the screen keeps working without it` |
| `the app refuses to accept a message while offline` | `the plan, sentence two: the offline state shows from P1.4, and the app refuses to accept a message` and `the refusal a screen shows is the last one, and the message that is accepted clears it` and `a transcript that arrives while offline is refused too, and nothing is written` |
| `the refusal is shown, not only returned` | `the plan, sentence two: the offline state shows from P1.4, and the app refuses to accept a message` and `the refusal a screen shows is the last one, and the message that is accepted clears it` and `a transcript that arrives while offline is refused too, and nothing is written` |
| `only offline refuses: stale is still an orchestrator` | `stale is not offline: a heartbeat three intervals old still accepts a message` |
| `the message is judged before the presence is` | `the plan, sentence two: the offline state shows from P1.4, and the app refuses to accept a message` |
| `an empty message is refused as an empty message` | `the plan, sentence two: the offline state shows from P1.4, and the app refuses to accept a message` |
| `an accepted message clears the refusal it followed` | `the refusal a screen shows is the last one, and the message that is accepted clears it` |
| `the poll's presence goes through the same guard a caller's does` | `the poll's presence goes through that same guard, which is the path the app takes` |
| `a presence outside P1.4's four is refused rather than stored` | `P1.4's answer is adopted through one guard, whether a caller or the poll brings it` and `the poll's presence goes through that same guard, which is the path the app takes` and `an update that is not an update, and a switch that is not a switch, are refusals with a code` |
| `a lease another consumer holds is surfaced` | `the plan, sentence three: a lease held by another consumer is surfaced rather than ignored` and `a lease the other consumer has let go of is the phone's to take, and it is taken` and `the lease does not gate the app: the phone sends while another consumer holds it` |
| `the state P1.2 hands back is adopted, refused or not` | `a lease the other consumer has let go of is the phone's to take, and it is taken` |
| `a release is never a takeover` | `the plan, sentence three: a lease held by another consumer is surfaced rather than ignored` |
| `a lease state P1.2 will not read is refused` | `a lease state that is not one is refused, and the screen keeps the one it had` |
| `the lease does not gate the app` | `the lease does not gate the app: the phone sends while another consumer holds it` |
| `the switch has two values and no third` | `an update that is not an update, and a switch that is not a switch, are refusals with a code` |
| `the digest's severity is on P1.3's ladder` | `an update that is not an update, and a switch that is not a switch, are refusals with a code` |
| `an interrupted utterance is not a spoken one` | `a reply that played out is a spoken verdict, and one that did not is not` |
| `the one refusal a caller can fix is carried through as itself` | `a reply that played out is a spoken verdict, and one that did not is not` |
| `the port refuses a pipeline that is not one` | `the port refuses a pipeline that is not one, at the point the app is built` |
| `the door's export list is the module's surface` | `the door exports exactly this, and every name is the screen, its switch or the speaker seam` |
| `the file walk is recursive` | `the walk is recursive, so a file in a subdirectory is part of the module` |
| `the module reads no clock` | `no clock — D117: the clock is the server-anchored one the screen is handed, so this module reads none` |
| `the module reaches no database of its own` | `no database of its own — D113: the mailbox client is the app's only write path, and a screen that could reach a database could be a second one` |
| `the README's rule table is the scan's own header` | `the README's rule table and the scan are the same list` |
| `the README's tokens are in the scan's order` | `the README's rule table and the scan are the same list` |
| `the README names every token the scan carries` | `the README's rule table and the scan are the same list` |
| `the README loses no rule row` | `the README's rule table and the scan are the same list` |
| `an unheard read-back is dropped, and cannot be confirmed later` (P6.12) | `the plan, sentence one: a destructive command speaks the read-back from the resolved target and waits` and `the read-back names the resolved target, never the words the speaker used` and `the plan, sentence three: a misheard confirm is refused` and `the plan, sentence four: a timeout says so and drops the action` and `the plan, sentence five: a merge read back at one head speaks the head that is there now` and `the plan, sentence six: a queued prompt is not spoken over the user` and `the pause speaks the interrupt policy’s reason, in the phone’s words` and `a sentence that did not play out marks nothing heard` and `an unheard read-back cannot be confirmed, and nothing is sent on an unconfirmed transcript` and `the three routes are kept apart: agent text is sent, an app command is not, an unmatched word is not` and `a launch is not confirmed and does travel: it goes as the owner’s own words (D162)` and `the offline refusal stays the client’s, and the command still does not travel` and `cancel that drops the read-back, and bad input is refused without throwing` |
| `an app command is never forwarded as text` (P6.12) | `the plan, sentence three: a misheard confirm is refused` and `the plan, sentence four: a timeout says so and drops the action` and `the plan, sentence five: a merge read back at one head speaks the head that is there now` and `an unheard read-back cannot be confirmed, and nothing is sent on an unconfirmed transcript` and `the three routes are kept apart: agent text is sent, an app command is not, an unmatched word is not` and `cancel that drops the read-back, and bad input is refused without throwing` |
| `a launch is sent as the owner's own words` (P6.12) | `the three routes are kept apart: agent text is sent, an app command is not, an unmatched word is not` and `a launch is not confirmed and does travel: it goes as the owner’s own words (D162)` |
| `a command that carried its own confirmation is refused` (P6.12) | `the plan, sentence two: a confirm in the same utterance as the command is refused` and `the plan, sentence three: a misheard confirm is refused` and `the plan, sentence four: a timeout says so and drops the action` and `the plan, sentence five: a merge read back at one head speaks the head that is there now` and `the plan, sentence six: a queued prompt is not spoken over the user` and `the pause speaks the interrupt policy’s reason, in the phone’s words` and `a sentence that did not play out marks nothing heard` and `an unheard read-back cannot be confirmed, and nothing is sent on an unconfirmed transcript` and `the three routes are kept apart: agent text is sent, an app command is not, an unmatched word is not` and `a launch is not confirmed and does travel: it goes as the owner’s own words (D162)` and `the offline refusal stays the client’s, and the command still does not travel` and `cancel that drops the read-back, and bad input is refused without throwing` |
| `a timed-out read-back is not offered again` (P6.12) | `the plan, sentence four: a timeout says so and drops the action` |
| `a moved target is re-proposed and read back again` (P6.12) | `the plan, sentence five: a merge read back at one head speaks the head that is there now` |
| `a question is marked heard only when its announcement played out` (P6.12) | `the pause speaks the interrupt policy’s reason, in the phone’s words` and `a sentence that did not play out marks nothing heard` |

### The spoken confirmation (P6.12)

`src/voice/dialogue.js` is the speech P6.1's router, P6.2's machine and P6.5's
prompt queue are wired into, and it is the spoken half of the path
`src/text/input.js` (P6.9) built for a keyboard. `createVoiceDialogue({ pipeline,
send, now, newId, router?, confirm? })` — `send` is the sink for **agent text**,
and the app wires it to the screen's own `accept`, so the offline refusal and the
app's one write path still stand over the voice path.

**The routing.** One final transcript goes through `routeUtterance`: agent text
is the client's, an app command is this module's, and a wake-word utterance that
matched no command is answered with P6.1's own sentence and **never forwarded**
(D164's fail-closed half). The four app commands this screen does not own —
switch-project, talk-to-agent, where-am-i and list — answer `elsewhere` rather
than being re-implemented here: they are P6.8's and P3.3's.

**A destructive command is a resolved target, never a parse.** P6.1's grammar
holds no destructive row at all, deliberately, and nothing in the merged tree
resolves a verb and a target from an utterance — `Fugaro, cancel run 42` is *not
understood*, and a test asserts exactly that. So `propose({ text, verb, target,
actionId })` takes the command the app has already resolved — the run id it
looked up, or the repository, PR and head it read — and does the half that is
the phone's: run it through the machine and **speak** P6.2's read-back, built
from the resolved target and nothing else. A target guessed from the speaker's
own words would be the paraphrase D47's Rule forbids, so
`Fugaro, cancel the docs refactor` against `run-42` is read back as `run-42`.

**An unheard read-back is dropped, and that is this task's headline rule.** The
read-back is the whole of D47's Rule: the speaker confirms what they *heard*.
So the speech is awaited, and a read-back that did not play to the end — the
output port refused it, or the user barged in mid-sentence — is **aborted in the
machine before `propose` answers**, with the honest code
`read-back-not-spoken`. Leaving it pending is the failure D169 exists to
prevent: a question the speaker was never told, still answerable by saying the
phrase. `src/voice/mutation-proof.sh` proves this one by inverting the condition.

**A confirmation is one utterance, later, and about a target that still holds.**
The phrase must be its own utterance (the machine refuses the utterance that
issued the command, and one carrying a command *and* the phrase); the phrase is
P6.2's answer rather than a grammar here, so "confirm it", "conform" and a bare
"yes" confirm nothing — with the wake word they are `not-understood`, and
without it P6.1 forwards them to the agent as text (D164); and one confirmation
authorises one action, so a replay is refused by the machine's ledger. The
timeout is P6.2's, measured on D117's injected clock, and it **names itself**:
`confirmation-expired` with the action dropped, not `no-pending-action`.

**A merge is re-read at confirmation time, and the read-back says so.** D168's
amendment is the reason — a prompt can be answered days later, and "approve the
merge" answered days later approves whatever the PR then holds. So a caller may
pass the target **as it is now** when the phrase arrives: the head it has just
re-read. A head that moved is `different-target`, nothing is authorised, and the
dialogue proposes the command again against the fresh target so the sentence
spoken next names the head that is there now. The speaker hears the current head
and confirms again; nothing is ever merged against a head they did not hear.

**A launch is not confirmed, and it does travel (D162).** `propose` answers a
launch with `sent`: the owner's own words go to the same `send` sink agent text
goes to, which is what "no confirmation" means on the wire. It is the one command
that skips the read-back, because there is no destructive effect to read back,
and it is the same call P6.9's typed path makes for the same verb
(`return send(text)` in `src/text/input.js`). The result carries `verb` and
`actionId: null` — nothing was proposed, so nothing can be confirmed. What is
unwritten for a launch is the same *record* the paragraph below is about, not the
launch itself.

**A question is marked heard only when it was.** `pause({ queues, active, digest,
presence, caps, switch })` composes P6.5: nothing is spoken while the assistant
is speaking, while a turn is open, or while a read-back is outstanding — a
question is not read over the user *or* over the question they still have to
answer. At a real pause one sentence is spoken: the interrupt reason's (P6.5's
policy, D160's wider table read through D127's switch) or P6.5's own count
("One question waiting"). The answer's `queue` has the active installation's due
prompts marked heard — `announcedAt` is when the user last *heard* the question,
so `marked: true` is said only when the sentence spoken was their own
announcement **and** it played out. A pause that spoke the kill switch's sentence
instead, a barge-in, and a port that refused the sentence all answer
`marked: false` with the queue exactly as it was, so the questions are announced
at the next pause rather than recorded as told to someone who never heard them.

**What a confirmed action does not do, stated rather than left to be found.**
The machine's record is **returned, not written**: `delivered: false` with
`no-action-write`. The client this app ships has one send — `send(text)` — which
writes `{ id, sender, text, at, mac }` and cannot carry an action, and sending a
destructive command as its own text is exactly what D169 forbids. So nothing is
written rather than the wrong thing; carrying the record is P6.7's (the
consumer) and P6.10's (the offline gate), and P6.9's typed path makes the same
call in the same words.

**Nothing is sent on an unconfirmed transcript**, and the tests say so in the
form that can be checked: `send` is a spy, every test that proposes, confirms or
expires a destructive command asserts it was never called, and the tests that do
see it called (`dialogue.test.js`'s routing test) assert its contents exactly —
agent text, and nothing else but D162's launch. No transcript is ever sent
because a phrase was expected.

## What is not verified here

* **The toolchain: declared, and half of it run.** The manifest declared no
  runtime dependency at all until the task that made this app buildable — which
  is why `eas build --profile internal` could not detect an SDK — and now
  declares the five the app imports. In the environment that ran that task
  (Node 22.23.3, npm 10.9.9), three things were run and are the whole of the
  verified surface beyond `package.json`'s `test` script:
  `npx expo-doctor` (**20/20 checks pass**), and
  `npx expo export --platform ios` and `--platform android`, both of which
  bundle `index.js` and everything it reaches, `shared/` included, with no
  unresolved import. That is a **JavaScript** proof: Metro resolved the graph
  and produced a bundle.
  **Re-run on 2026-10-21**, after the Android launch crash, in the same
  toolchain (Node 22.23.3, npm 10.9.9): `npx expo-doctor` **20/20**,
  `npx expo install --check` → *Dependencies are up to date* (so every
  installed package matches the SDK 58 `bundledNativeModules.json`), and
  `npx expo export --platform android` produced an Android bundle of
  **655 modules** (654 before the guard was added). `npm test` was **640/640**
  green. None of that is a device: the bundle *builds*, it has not been *run*.
  **Still not run, and it is the larger half:** no `expo start`, no `expo lint`,
  no `eas build`, no `expo prebuild`, no Xcode or Gradle run, no simulator and
  no device. So a **native** iOS or Android build — the pods, the Gradle
  modules, the config plugins, the signing — is unverified, and so is a
  submission to either store: both need the owner's Expo, Apple and Google
  accounts, and no agent runs them (D120, Check 89 is owner-run). The two
  identities, too, are exercised only as values this repository validates: a
  bundle was exported here with **test** values in the shell, and no build has
  ever read the owner's real ones.
  **This includes P4.4's two files**: `app.config.js` is exercised as a
  function of a base config and an environment (which is what its tests call
  it with), and **Expo's own loader has read it — as of this task, and only with
  the CLI's own reads**: with both variables exported in the shell,
  `npx expo config --type public --json` returns
  `ios.bundleIdentifier = com.example.testapp` and
  `android.package = com.example.testapp` — the values the two variables held,
  resolved through this file, which is what proves the `({ config }) =>` form
  and the ESM `export default` are what `@expo/config`'s `evalConfig` calls (it
  resolves `result.default` and calls it with the `app.json` `expo` object as
  `config`, `Config.ts`). `npx expo export` reads the same file the same way.
  What is still unverified is the rest of the path: **no `expo start` and no
  `eas build`** have resolved it, and the value that has been through the loader
  is a shell test value, never the owner's own identity.
  `eas.json` is parsed and asserted as JSON; **no `eas` command
  has been run**, so the profiles are as correct as the register's text says
  they must be and not as correct as a build would show — including the two
  claims about `eas-cli` that the local build path rests on (that it resolves
  `app.config.js` in its own process before uploading, and that a profile's
  `environment` is the environment its build job reads). The first of the two
  **was observed on 2026-10-20**, by the owner's first submission: `eas submit`
  stopped with `Specify ios.bundleIdentifier` until the two variables were in the
  shell, which is the CLI resolving the config locally with `EAS_BUILD` unset
  ("the submit step", above) — the values, the builds and the submission itself
  are still the owner's, and none of that ran here. The second (a profile's
  `environment` reaches its build job) is read off Expo's documentation and has
  not been observed anywhere.
* **The SDKs themselves.** `expo`, `expo-secure-store`,
  `@react-native-async-storage/async-storage`, `react` and `react-native` are
  faked in every test that touches them: what is tested is the shape of the
  calls this repository makes (including the accessibility constant, read off
  the module **when the platform has one** — the Android half of that was the
  2026-10-21 crash), not what the modules do. `index.js`, which imports the
  real ones, is untested for that reason, and the guard around it
  (`src/app/startup.js`) is what turns the next untested import into a screen
  somebody can read instead of an app that closes.
* **`useSyncExternalStore`, and the demo as a running app.** The demo's
  subscription is `React.useSyncExternalStore(demo.subscribe, demo.state)`,
  passed into the shell as the `useDemo` hook (`index.js`); every test supplies
  its own. That React's hook calls `subscribe` the way this code assumes, that
  `Pressable` renders a tappable control, and that a reviewer can actually read
  the demo on a phone are all unverified here -- there has been no `expo start`
  and no device run, which is the first bullet in this list.
* **`WHEN_UNLOCKED_THIS_DEVICE_ONLY`'s name and value**, which come from the
  documented `expo-secure-store` API and are not exercised against the package.
  The port requires the constant **where the platform declares it** (iOS) and
  refuses to construct if it is missing, so an SDK rename is a loud failure
  rather than a silent downgrade to the platform default — but the constant has
  not been seen on a device here. That requirement is now **per platform**,
  which is the 2026-10-21 fix: `expo-secure-store`'s Kotlin module registers no
  `Constant(...)` at all (`mobile/src/accounts/expo-secure-store.js` has the
  grep), so requiring it everywhere was a `TypeError` at module scope on
  Android — which is the crash — and requiring it nowhere would silently drop
  the iOS accessibility level. Both halves are tested against a fake module on
  each platform; neither has been run on either.
* **The voice-session reconciliation's F24, on Android.** F24 says the secure
  store is unreadable while the phone is locked, and that is a property of the
  **iOS** Keychain item (`WHEN_UNLOCKED_THIS_DEVICE_ONLY` and nothing to do with
  this app's code). Its Android half is a claim **read off the installed
  module's source** — `AESEncryptor.kt:64` sets `setUserAuthenticationRequired`
  from the caller's `requireAuthentication`, which this app never passes — and
  never observed: no device here has been locked mid-session with the store
  open. A test on a device, or a design that does not lean on unlock-gating on
  Android, is what would make VS5.1's "a paused session touches no credential"
  true on both platforms rather than on one.
  (O7 in the reconciliation's open list tracks the same gap.)
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
* **The screen's own edges, and where it stops.** P3.5 (above) is the screen,
  and `src/voice/speaker.js` wires the client's `speak(text)` port to this
  pipeline, so "ack only after it has spoken" now runs over the real audio
  ports. What is still not exercised: **a real engine** (the first store build
  is voiceless, and the pipeline is driven by P3.4's fakes), **P6.12's spoken
  confirmation as a device feature** (the logic is below and is tested over the
  fakes; what a real engine hears and how a real voice sounds is check 85/86)
  and **P6.5's wider interrupt table**, which the screen deliberately does not
  compose with -- `src/voice/interrupt.js` and the P3.5 section above say so
  (P6.12's dialogue composes with it, in the pause below).
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
  Android package is committed: those are the owner's, and the app's release
  path is its own (D125) — they reach a build from the EAS environment through
  `app.config.js` (P4.4, "Building for internal testing"), and **the value in a
  variable has never been through a real build here**.
* **The two limits the module names.** `src/mailbox/README.md` records them with
  their decisions: the phone does not check how old an outbox message is (a
  signed one with an old `at` is spoken; a replay needs the consumer's identity,
  which the rules require), and `acked` is outside the tag (P6.3/D171), so a
  token minted for this uid could pre-ack a genuine message -- which hides a
  message and cannot make anything act.
* **The mailbox against a real database, and its deployed rules.** P3.6's client
  is tested against the port's double, which models the two rules a re-send
  meets and, since P6.10, the SDK's **offline write queue** (`setOffline`,
  `pendingWrites`, `flushQueue`, `cancelPending`) — which is not a rule of the
  tree at all but a behaviour of the client library — and nothing else: no auth,
  no listeners, no quota, no project. Checks **81** and **82** (the deployed
  rules' fence and the rules deploy) are owner-run and have not been run, so
  *what the deployed rules do with a re-write* is asserted here only as the
  double's model of them. **No adapter is wired for either new port**: the
  connection the gate reads is `MemoryConnection`, so the Firebase SDK's own
  connection state is unverified here (no project, Global Constraint 10).
* **Metro, and the `shared/` imports.** `src/mailbox/` imports
  `../../../shared/mailbox-schema/index.js`, `../../../shared/presence/index.js`
  and `../../../shared/digest/redact.js`; node resolves them (the tests run), the
  D119 boundary rule allows them, and **Metro resolves them too** — that was the
  missing half until this task added `metro.config.cjs`, whose `watchFolders`
  entry is the repository root, and `npx expo export` now bundles `shared/` into
  both platforms' bundles. There has still been no `expo start` here, so the
  *dev server* is unverified — see `src/mailbox/README.md`.
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
