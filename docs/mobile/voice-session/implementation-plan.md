# Implementation plan — the hands-free voice session (`voice-session`)

**Goal.** Do what [spec.md](spec.md) asks for **as amended by
[reconciliation.md](reconciliation.md)**: one platform-agnostic JavaScript API
for a voice session that survives the app being backgrounded, pauses on a call
or an unsafe route and resumes within a time limit, shows its controls outside
the app (a Live Activity with buttons on iOS, an ongoing foreground-service
notification on Android), ends cleanly after a long interruption and never
leaves a "live" session behind after a crash. Where the spec and the
reconciliation disagree, **the reconciliation wins** — the spec was written
without seeing this repository.

**Status: plan only; nothing is implemented; decisions open.** Not one line of
any task below has been written or run. **D201–D220 in
[reconciliation.md](reconciliation.md) §5 are open**: every one carries a
recommendation, and **none is taken**. Answering a decision is not approving a
task. The gate at the end of this file is **not passed**.

**Checked against.** `main` at `c92093b6` (the mobile tree at `12fbe60e`), the
same commit as [reconciliation.md](reconciliation.md). Every path and symbol
below was located with `git grep` and re-checked to exist before it was written;
a path marked **new** does not exist yet. **Line numbers are never cited.**

**Spec.** [spec.md](spec.md) (verbatim). **Reconciliation.** [reconciliation.md](reconciliation.md)
— F1–F30, D201–D220, O1–O8, the platform-fact table, the UNVERIFIED table.
**Index.** [README.md](README.md).

**Related.** [../README.md](../README.md) (the mobile work, its Global
Constraints and the check table) · [../implementation-plan.md](../implementation-plan.md)
(Phases 3–6, and the constraint this plan is written under) ·
[../../voice-module/](../../voice-module/) (the pipeline this module drives) ·
[../design/voice-evaluation-switch.md](../design/voice-evaluation-switch.md)
(P5.3) · [../design/push-notifications.md](../design/push-notifications.md)
(P5.2) · [../../gcp-live-checklist.md](../../gcp-live-checklist.md) (the
register, and checks 113–118) · [../../../mobile/README.md](../../../mobile/README.md)
(the app, and its **voiceless** status).

---

## What this does not do

- **No pipeline.** STT, the orchestrator and TTS are [docs/voice-module/](../../voice-module/)'s
  subject and `mobile/src/audio/`'s code (P3.4). This module **starts, pauses,
  resumes and stops** the pipeline; it never speaks, transcribes or routes a
  model. F1.
- **No second state machine.** The pipeline's `idle`/`open`/`flushing` +
  `turnId` stays where it is, and P3.5's four decisions (the interrupt switch,
  the queue, the offline refusal, the lease) stay in `mobile/src/voice/screen.js`.
  The new machine owns the **session**; the turn stays with the pipeline. F3,
  F4, D206.
- **No reply to the voice-module's ruling, except the one that must be said.**
  "App stays in the foreground while talking" is reversed **only** for the phone,
  and the reversal is named in the reconciliation (F2) rather than made in
  passing. Every other half of D122 — the owner's usage-description sentence,
  `AUDIO_IN_CODES`'s two denials, the visible "tap to speak" fallback — is
  unchanged. D207.
- **No push.** No APNs key, no device token, no sender, no FCM. The Live
  Activity is updated locally only, and D121's "no push in v1" is untouched.
  F12, F28.
- **No store step by an agent.** The Apple and Play steps in §"Owner-only
  steps" are the owner's, start to finish (Global Constraint 9, D120).
- **No audio and no transcript anywhere but the microphone and the speaker.**
  Nothing is buffered for a resume (there is nothing to replay — F9), nothing
  is written to a shared container but four scalars (F25), and nothing reaches a
  log or a crash reporter (F11, D213).
- **No demo-mode session** (F10, D212), and **no test that needs a device, a
  real microphone or a live cloud** (Global Constraint 10).
- **No change to `.github/workflows/`.** Nothing here needs one.

---

## Builds on merged code

| What exists | Where | Used by |
|---|---|---|
| The pipeline and its ports: `createVoicePipeline`, `isVoicePipeline` = `['start','stop','say','speaking','turn','on']`, `PIPELINE_EVENTS`, `PIPELINE_EVENT_FIELDS`, the internal `idle`/`open`/`flushing` + `turnId` | `mobile/src/audio/pipeline.js` | VS1.1, VS0.2 |
| The fakes the machine is tested against: the audio-in port (`AUDIO_IN_CODES`, `AUDIO_IN_EVENTS`, `AUDIO_IN_EVENT_FIELDS`), the TTS port (`TTS_CODES`) | `mobile/src/audio/audio-in.js`, `tts.js`, and the fakes beside them | VS0.1, VS0.3 |
| The door, its export list and its fences (no package imports; no clock, random, file or network) | `mobile/src/audio/index.js`, `index.test.js` | VS0.3, VS1.2 |
| The screen: `createVoiceScreen({ pipeline, client, now, holder, switch, queueCap })`, `SCREEN_CODES`, `QUEUE_CAP`, `UPDATE_FIELDS`, the `switch`/`offline`/`lease`/`queue` fields | `mobile/src/voice/screen.js` | VS1.1, VS1.2 |
| The interrupt rule the session must not stale: `interruptDecision({ switch, severity, highest, speaking, turn })`, `INTERRUPT_SWITCH`, `INTERRUPT_REASON` | `mobile/src/voice/interrupt.js` | VS1.1, VS0.2 |
| The speaker port | `mobile/src/voice/speaker.js` (`createPipelineSpeaker`) | VS0.3 |
| The app's one wiring file, and the fences over it | `mobile/index.js`, `mobile/src/boundary.test.js`, `dependencies.test.js` | VS1.3 |
| The identity fences: no `com.` in `app.json`; the owner's own reverse-DNS identity in **no file** under `mobile/` or `docs/mobile/` (the pin's `IDENTITY_ALLOWED` is empty, and what it looks for is that value and not reverse-DNS literals in general -- the fictional `com.example.sample` fixtures under `mobile/src/` are allowed and are what the tests use); `bundleIdentifier`/`package` absent from `app.json` | `mobile/src/app-config.test.js`, `app-config.ids.test.js` | VS2.1, VS2.3 |
| The dynamic config and its two environment variables: `ID_VARIABLES = { ios: 'FUGARO_IOS_BUNDLE_ID', android: 'FUGARO_ANDROID_PACKAGE' }`, the `ios.infoPlist` merge | `mobile/app.config.js` | VS2.1, VS2.2, VS4.1 |
| The dependency scan: every bare import declared in `package.json`, the lockfile matching it, `metro.config.cjs` present | `mobile/src/dependencies.test.js` | VS1.3, VS3.1, VS4.2 |
| The build profiles: `development` (`developmentClient: true`), `preview`, `internal` (`distribution: store`, `android.buildType: app-bundle`), `cli.requireCommit`, `submit.internal.ios.ascAppId` | `mobile/eas.json`, `mobile/src/eas-config.test.js` | VS2.2, VS3.3, VS4.3, VS7.1 |
| The secure store that is unreadable while the phone is locked — **on iOS** (`keychainAccessible: WHEN_UNLOCKED_THIS_DEVICE_ONLY`; Android has no such option and its Keystore key is not unlock-gated, see F24 and `mobile/README.md`): `SECRET_ITEMS = ['idToken','macKey']` | `mobile/src/accounts/expo-secure-store.js`, `secret.js`, `secure-store.js` | VS0.3 (a paused session touches no credential), VS5.1 (**the iOS half; the Android half is not established**) |
| The demo's fences: `createDemoMode.length === 0`, no network, no secure storage, no secret | `mobile/src/demo/safety.test.js` | VS1.3 (the demo stays out of it) |
| The check register, its numbering rule, and the house style of a check ("guard block, FACT lines, Passes/Fails when, Paste back, Restore") | `docs/gcp-live-checklist.md` | VS7.1–VS7.6 |
| The decision register's block table ("D193 onward — free") | `docs/coordinator/README.md` | the D-row block this work takes |

**Nothing below exists yet:** `mobile/src/voice-session/` (JS),
`mobile/modules/voice-session/` (the local Expo module), and
`mobile/targets/voice-session-widget/` (the iOS widget extension). F5, F14.

---

## Part summary

Parts are ordered so that **each is mergeable alone and leaves `main` green**.
VS0 takes one decision (D205) and nothing else; VS1 takes D205 and **D213**
(its reporting shape); VS2 takes D205 and **D207** (the background mode it
declares) — the full mapping is §"What waits for what"; VS3 and VS4 are the two
platform halves and are independent of each other; VS5 and VS6 sit on top; VS7 is
the owner's.

| Part | What it is | Tasks | Mergeable alone? | What it waits on |
|---|---|---|---|---|
| **VS0 — The machine, and its tests** | `mobile/src/voice-session/`: the state machine as a **pure reducer over the spec's §5 event stream**, with its own door, its own scan and a test for every transition — including the timeout and `shouldResume: false` paths the spec's §11 asks for. **No native, no device, no decision but D205.** | VS0.1–VS0.3 | **Yes** — nothing imports it yet | D205 (where the machine lives); safe to start the moment it is answered |
| **VS1 — The seam** | The controller that joins the machine to reality: it drives `pipeline.start()`/`stop()`, reads `pipeline.speaking()`/`turn()` (never storing them), keeps P3.5's switch and queue where they are, and registers itself as `mobile/index.js`'s one construction site. Native is a **port** with a fake, exactly like `src/audio/`. | VS1.1–VS1.3 | **Yes** — the fake means the app runs unchanged | VS0; **D213** (VS1.2's reporting shape) |
| **VS2 — The declarations** | Everything the two platforms need *declared*, through `app.config.js` and a config plugin (F6): the iOS background mode, the usage string, `NSSupportsLiveActivities` and the App Group; the Android permissions and the FGS type. **No native code yet** — this part is what turns the fence red if it is done in `app.json`. | VS2.1–VS2.3 | **Yes** — the declarations are inert until a module uses them | VS1 (the policy scan needs the module to exist); **D207** (VS2.1's background mode) |
| **VS3 — Android** | The local Expo module's Kotlin half: `VoiceSessionModule.kt`, `VoiceSessionService.kt`, `AudioFocusController.kt`, the notification, the route signal, the `START_NOT_STICKY` contract. | VS3.1–VS3.3 | **Yes**, and independent of VS4 | VS2; **D209** (call state), **D210** (notifications denied), **D218** (the category) |
| **VS4 — iOS** | The module's Swift half, the `ios/Shared/` sources compiled into both targets, the widget target and the Live Activity with its buttons. **The only part with owner-only steps in the Apple portal.** | VS4.1–VS4.4 | **Yes**, and independent of VS3 | VS2; **D204** (the minimum iOS version), **D203** (what Hang Up does), **D216**, **D217** |
| **VS5 — What a kill leaves behind** | `cleanupStaleSessions()` on every launch, the Android self-stop when JS goes quiet, and the assertion that a **paused** session touches no credential (F24) and no transcript (F9). | VS5.1–VS5.2 | **Yes** | VS3 and VS4 |
| **VS6 — The way back in** | The "Tap to resume" in-app banner on next open (D211's recommended first cut), and — its own task, after that — the dismissible local notification. | VS6.1–VS6.2 | **Yes** | VS5; D211 |
| **VS7 — The checks** | Checks **113–118**, written into the register in the house style and run by the owner. | VS7.1–VS7.6 | Owner-run; no commit but the recorded result | A release (F16), and the parts each check covers |

---

## What waits for what

| | Rule |
|---|---|
| **D205** (where the machine lives) gates **all of VS0–VS2**, which is the part of this plan that is otherwise safe to start at once. Its recommended answer — JavaScript — is the one the rest of the plan is written against, and a different answer rewrites VS0, VS2, VS3 and VS4. |
| **D204** (16.1 versus 17) gates **VS4.3 and VS4.4**, because it is the difference between one Live Activity UI and two, and between buttons and a deep link. Its recommended answer — iOS 17 — makes VS4.4's fallback row disappear. |
| **D207** (background audio, extending D122) gates **VS2.1**, the commit that declares `UIBackgroundModes`. |
| **D209** gates **VS3.2**'s call-state half: the recommended answer removes the `READ_PHONE_STATE` request and the whole denial path. |
| **D210** gates **VS3.2**'s notification half. |
| **D211** gates **VS6.2** only; **VS6.1** (the banner) needs no decision. |
| **D212** gates nothing in the plan — it is the answer that keeps `src/demo/` untouched. An owner who wants a demo session instead opens a task that is not here. |
| **D213** gates **VS1.2**'s reporting shape. Its recommended answer needs no SDK and no decision about egress. |
| **D201–D203, D206, D208, D214–D220** are **constants in the code** the plan writes (`resumeTimeoutMs`, the cue, the resume rule, the throttle, the category, the check numbers) — each is a named default in a named place, and changing one is a one-line change plus a test. The plan says where each lives so that answering one is cheap. |

**Nothing waits on Check 85.** Every part — and every check — is written so that
the session runs **with the pipeline silent** (F7, D208). The "I'm back" cue is
the one behaviour that needs an engine, and it is the only line of
[spec.md](spec.md) this plan leaves for after the bake-off.

---

## Check numbers this plan takes

**113–118.** The register's written sections run to **89**; **90–99** are the
credentials service's; **100–104** are the mobile functional spec's, with
**105–109 reserved and unused**; **110** is named by
[../design/voice-evaluation-switch.md](../design/voice-evaluation-switch.md) as
the next free number and was assigned by it; and **111** is taken by
[docs/setup-wizard/](../../setup-wizard/README.md) ("110 is claimed by an open pull
request … **111 is therefore the next free number**, and it is taken here").

**112 is taken too, and this branch merges second, so this work renumbers itself
to 113–118.** `docs/windows-cli/` — **on `main` and not in this
branch**, which is why it is a code path and not a link here — **proposes Check
112** for its own owner-run check, and is merged as `58bd6b05` (PR #757) (*"a
first run on a real Windows machine, from a downloaded archive, by the owner, in
three terminals"*), and states that it is *"proposed … and not claimed and not
written by this pull request"*. The register's own rule decides between two open
claims: *"whichever of two open claims merges second renumbers itself"*
([../../gcp-live-checklist.md](../../gcp-live-checklist.md)). #757 is on `main`
and this branch is not, so **this branch is the second** and yields — exactly as
[docs/setup-wizard/](../../setup-wizard/) recorded for the 110/111 collision. This
work therefore takes **113–118**, and **112 stays the Windows work's proposal**.

The commands that show the allocation, rather than a figure that any edit changes:

```
git grep -n -E '^## Check 1(0|1)[0-9]' -- docs/gcp-live-checklist.md
git grep -n 'Check 11[0-9]' -- docs/
git grep -n 'Check 112' origin/main -- docs/windows-cli/
```

**Check 86 is not touched.** It already owns *"Hands-free audio: the background
mode, the microphone ask, Bluetooth, and barge-in"* (P3.5/P4.2, owner-run), its
text is unwritten, and it keeps its number and its subject. The six below cover
what it does not name. F17.

**None of the six is written into the register by this plan.** The register's own
rules are that *a check is never run before its text is in the register*, that
*the phase that ships the check writes it*, and that *a reserved number is never
reused or renumbered* — and no phase here is approved. D219 records the choice.

| # | The check's subject (the text is VS7's) | Covers | Runs on |
|---|---|---|---|
| **113** | **Interruptions and the timeout**: a call arrives mid-session, is declined; is answered briefly; runs past the timeout. The `shouldResume: false` path. The suspension-notification caveat (P8). | F2, F26; P6, P8, P11, P19 | iPhone and Android device |
| **114** | **The Live Activity and its buttons**: it appears, it survives backgrounding, its timer runs with no per-second update, Mute and Hang Up work from the lock screen and the Dynamic Island, and the paused state reads as paused (D216). | F25, F28; P1–P5 | iPhone (iOS 17+ under D204) |
| **115** | **The Android service**: the notification, its two actions, the foreground-service type, the start-while-foreground rule and the `SecurityException` path, notifications denied, and what Doze does not do to it. | F26, F27, F29; P11–P14, P21, P22 | Android 14+ device; an emulator can rehearse the permission ask, the notification and the actions |
| **116** | **Route changes**: unplug wired headphones and drop Bluetooth **mid-response**, on both platforms — the session pauses, nothing comes out of the loudspeaker, and a *connect* does not pause (spec §10). | F9; P9, P18 | iPhone and Android device; **an emulator cannot show this** |
| **117** | **A kill, and what it leaves**: force-kill mid-session and relaunch — the activity is gone or reads stale, the notification is gone, no orphan service, no live-looking timer. Plus a media-services reset (P10). | F24, F29, D214; P5, P10, P22 | Either platform; `adb shell am force-stop` makes this the Android check an emulator can rehearse |
| **118** | **The microphone's invariants**: the ask and the usage string, the indicator always visible while the mic is open, the mic never open while the app is locked and showing nothing, nothing recorded without the indicator, no audio or transcript in any log or report, no transcript in the App Group, and no credential read while paused. | F9, F24, F25, D213, D215 | iPhone and Android device |

**Owner-only steps** — the portal and the device work, in full, are
[reconciliation.md](reconciliation.md) §6: the Apple portal (a new App ID for
the widget extension, an App Group identifier, the capability on both App IDs,
and **no hand-made provisioning profile** — EAS declares the extension and syncs
the capability, F15), the Play Console (the foreground-service-type declaration,
Data safety, and nothing else by hand), and what a device must show that an
emulator cannot.

---

## VS0 — The machine, and its tests

The whole of this part is **CI-runnable, on any machine, with no device** — which
is what the spec's own §11 unit-test line asks for and what Global Constraint 10
allows. Nothing here imports a native module, a clock, a random source, a file
or the network.

### Task VS0.1: The event contract, as the spec's §5 writes it — **new**

**Files.** `mobile/src/voice-session/events.js` (new; the spec's §5 types in the
tree's plain-JS-with-JSDoc style, next to `mobile/src/audio/pipeline.js`);
`mobile/src/voice-session/events.test.js` (new).
**Depends on.** D205.
**What it does.** Freezes the six event kinds and `AudioRoute` exactly as
[spec.md](spec.md) §5 writes them, plus `SESSION_STATES` (the spec's §4 eight),
`END_REASONS` and the two pause reasons, as frozen objects in the shape
`PIPELINE_EVENTS`/`AUDIO_IN_EVENTS` already use. A single exported validator
(`isVoiceSessionEvent`) mirrors `isVoicePipeline`, so the module rejects a
malformed event at the boundary rather than at the transition.
**Tests.** Every kind and every route is exported; an unknown `type`, an unknown
`route` and a missing `shouldResume` are each refused; the objects are frozen.
**Verification.** `cd mobile && node --test src/voice-session/events.test.js` —
the runner `mobile/package.json`'s `test` script already uses
(`node --test "src/**/*.test.js"`), pointed at one file — or the whole suite with
**`cd mobile && npm test`**, or through **`fugaro verify test`**. The dependency
scan stays green: this file imports nothing.
**Reviewer.** standard.

### Task VS0.2: The state machine, as a pure reducer — **new**

**Files.** `mobile/src/voice-session/machine.js` (new);
`mobile/src/voice-session/machine.test.js` (new).
**Depends on.** VS0.1.
**What it does.** One exported function, `reduce(state, event, ctx)` — no
clock, no timers, no I/O — implementing [spec.md](spec.md) §4's table and its
key transitions, with `ctx` carrying `{ now, resumeTimeoutMs, pipeline }` where
`pipeline` is the **only** source of `speaking` and `turn` (F3, D206: the machine
reads them, it never stores them). The pause reasons are `interruptionBegan`'s
`source` and `routeChanged`'s `unsafe`, so "why is it paused" is answered by the
event and not by a second flag. The timeout is not a timer in the machine: it is
a `tick` the caller injects, exactly as `sender`/`newId`/`now` are injected in
`src/audio/`.
**Tests.** The spec's §11 line, in full: `idle → starting → listening`;
`listening ⇄ speaking` driven by the pipeline; each active state →
`paused_interruption` → back to `listening` on `interruptionEnded` **with**
`shouldResume: true` **within** the timeout; → `ended`
(`endReason: 'interruption_timeout'`) when the tick passes the timeout first;
→ `ended` on `shouldResume: false`; `listening|speaking → paused_route` and
resume **only** on an explicit action; `hangUp()` from every active state →
`ending → ended`; mute as an orthogonal flag that changes no state; an unknown
event is refused and changes nothing; and the **P8 caveat** — an
`interruptionBegan` carrying the suspension key must not be treated as a call.
**Verification.** `cd mobile && node --test src/voice-session/machine.test.js`, or
**`cd mobile && npm test`**, or **`fugaro verify test`**.
**Reviewer.** standard.

### Task VS0.3: The door, its scan, and the fences — **new**

**Files.** `mobile/src/voice-session/index.js` (new);
`mobile/src/voice-session/index.test.js` (new).
**Depends on.** VS0.2.
**What it does.** Exports **the machine and the events, and nothing else** —
the session controller does not exist until VS1.1, and the door is only extended
to carry it in VS1.3, the task that constructs it. One door, in the shape of
`mobile/src/audio/index.js` and `mobile/src/voice/index.js` — the app imports
the door and never a file inside.
**Tests.** The door's export list is pinned against a literal list (the
machine and the events in this task; VS1.3 widens the literal when it adds the
controller, in the same commit as the export); the same
three recursive scans `src/audio/index.test.js` runs (no package imports; only
`shared/` outside `mobile/`; no clock, random, file or network) run over
`src/voice-session/`; and two **new** assertions this work adds: **a paused
session touches no credential** (no import of `src/accounts/`, matching F24) and
**no file here builds a string from `transcript`** (F9, D213).
**Verification.** `cd mobile && node --test src/voice-session/index.test.js`; then
**`cd mobile && npm test`** or **`fugaro verify test`** for the whole suite — the
dependency scan and the boundary tests are the ones that would notice an
accident.
**Reviewer.** standard.

**Commit.** `mobile/voice-session: the state machine, as a pure reducer over the spec's event stream`.

---

## VS1 — The seam

### Task VS1.1: The session controller over the pipeline and P3.5's screen — **new**

**Files.** `mobile/src/voice-session/session.js` (new);
`mobile/src/voice-session/session.test.js` (new).
**Depends on.** VS0.3.
**What it does.** `createVoiceSession({ machine, screen, pipeline, native, now,
resumeTimeoutMs, title })` — the object the app talks to. `start()` asks the
native port to begin, then `screen.start()` (which is already the one path to
`pipeline.start()`); `pause`/`resume`/`hangUp` drive the machine; every render
reads `speaking` and `turn` from `pipeline` and passes them to
`interruptDecision` unchanged (D206); `updateStatus` is pushed to the native
port **only on a state or mute change, plus one `staleDate` refresh** (D217).
P3.5's switch, queue, offline refusal and lease are **read, never re-implemented**
(F4).
**Tests.** Start drives both in the right order; a state change pushes exactly
one `updateStatus` and a mute change pushes exactly one more; a render with no
change pushes none; `interruptDecision` sees the pipeline's live `speaking`/`turn`
and a stale stored copy is never used (the assertion is that mutating the
pipeline changes the decision with no event in between); a paused session issues
**no** mailbox call and reads **no** credential (F24); the machine's `ended`
releases the lease exactly once.
**Verification.** `cd mobile && node --test src/voice-session/session.test.js`, or
**`cd mobile && npm test`**, or **`fugaro verify test`**.
**Reviewer.** standard.

### Task VS1.2: The native port, and its fake — **new**

**Files.** `mobile/src/voice-session/port.js` (new);
`mobile/src/voice-session/port.test.js` (new); `mobile/src/voice-session/fake.js`
(new).
**Depends on.** VS1.1.
**What it does.** The port is the spec's §5 `VoiceSessionNative`
(`start`, `updateStatus`, `setMuted`, `end`, `cleanupStaleSessions`) plus an
event subscription — and nothing else. `fake.js` is the twin of `src/audio/`'s
fakes: it records calls and lets a test inject the event stream, which is what
makes VS0's and VS1's tests device-free. Its `error` reporting is D213's
recommended shape: a code and the field it names, in the manner of
`SCREEN_CODES`, and **never audio, never a transcript**.
**Tests.** The fake satisfies the same shape assertion the real port must; a
malformed event is refused (VS0.1's validator); the recorded calls match the
`updateStatus` throttle; the error path carries a code and no free text.
**Verification.** `cd mobile && npm test` (the mobile runner's own glob picks the
whole folder up), or **`fugaro verify test`**.
**Reviewer.** standard.

### Task VS1.3: The app's one construction site — **edited**

**Files.** `mobile/index.js` (edited); `mobile/src/voice-session/index.js`
(edited); `mobile/src/boundary.test.js` (only if a guard needs to name the new
module).
**Depends on.** VS1.2.
**What it does.** Constructs the session with the **fake** port, next to the
`AccountStore`/`createDemoMode()` wiring `mobile/index.js` already does, and
passes it to the voice screen. Nothing in the app's visible behaviour changes:
a session can be started and stopped, and nothing is shown outside the app yet.
The demo path is left exactly as it is (F10, D212).
**Tests.** The existing boundary and dependency tests stay green; one new
assertion pins that the demo does not construct a session.
**Verification.** **`fugaro verify build`** then **`fugaro verify test`**.
**Reviewer.** standard.

**Commit.** `mobile/voice-session: the controller over the pipeline, and the port with its fake`.

---

## VS2 — The declarations

### Task VS2.1: The iOS declarations, through `app.config.js` — **edited**

**Files.** `mobile/app.config.js` (edited); `mobile/app.json` (**untouched** —
and that is the point); `mobile/src/app-config.test.js` (extended); a new
`mobile/src/voice-session-config.test.js` (new).
**Depends on.** VS1.3; **D207**.
**What it does.** Adds, from the dynamic config and never from `app.json`:
`UIBackgroundModes: ['audio']`, the owner's `NSMicrophoneUsageDescription`
sentence (the owner writes the text — D122), `NSSupportsLiveActivities: true`,
and the App Group entitlement (`com.apple.security.application-groups`). The
**widget's bundle identifier** is **derived from `FUGARO_IOS_BUNDLE_ID`** (the
app's identifier plus a suffix), not added as a new environment variable (F6).
The **Apple Team ID is not derived from anything** — it is Apple's own
ten-character team identifier, it cannot be computed from a bundle identifier, and
it is **not written here**: **EAS already holds it with the owner's Apple
credentials** and signs the app with it today, so the task reads it from those
credentials where the generated extension target needs it (F6, and the owner's
three portal steps in the reconciliation's §6.1 are the only Team-ID-adjacent work
the owner does).
**Why it is a task and not a line:** `mobile/src/app-config.test.js` asserts
`RAW.includes('com.') === false`. The App Group string contains `com.`, so this
task is exactly where that fence would be broken by accident. The new test is
what pins the *route*: the declarations are in the **resolved** config and not
in the raw `app.json`, and the raw file still contains no `com.`.
**Tests.** The resolved config carries the four; `app.json` still carries none;
no new environment variable name appears outside `app.config.js`; the derivation
of the widget identifier from `FUGARO_IOS_BUNDLE_ID` is a unit test over the
pure helper.
**Verification.** `cd mobile && npx expo config --type introspect` (the
introspection must show the four and no more; it runs **locally, offline, over
`mobile/node_modules`** — the `npm ci` that installs them is itself a network
step and is not part of the verification), then **`fugaro verify test`**.
**Reviewer.** **SECURITY-SENSITIVE-adjacent** — Global Constraint 14 applies to
the app-group and usage-string change, because those are the two declarations
that decide what the microphone and the shared container mean: **independent
review.**
**Note.** The usage-description sentence is the owner's to write. The task
lands the key with a placeholder the owner **must** replace before a store
build, and the check in VS7.6 reads the built app's own string.

### Task VS2.2: The Android declarations — **edited**

**Files.** `mobile/app.config.js` (edited); `mobile/src/voice-session-config.test.js`
(extended).
**Depends on.** VS2.1 (the same file, the same test).
**What it does.** Adds `RECORD_AUDIO`, `FOREGROUND_SERVICE`,
`FOREGROUND_SERVICE_MICROPHONE` and `POST_NOTIFICATIONS`, and the service's
`foregroundServiceType` — and adds **`READ_PHONE_STATE` only if D209 says yes**
(the recommended answer does not). The `<service>` entry and the FGS type are
declared here even though VS3 writes the Kotlin, because the manifest is CNG's
output and not a file in the tree.
**Tests.** The permission list is exactly the expected set (an equality, not a
containment — a stray permission must fail the test); `READ_PHONE_STATE` is
absent under D209's recommendation and the test says which decision it encodes.
**Verification.** `cd mobile && npx expo config --type introspect` (local and
offline over `mobile/node_modules`, as VS2.1), then **`fugaro verify test`**.
**Reviewer.** standard (the permission list is public by construction).

### Task VS2.3: The identity fence, again — **edited**

**Files.** `mobile/src/app-config.ids.test.js` (extended if it needs to be);
`mobile/src/voice-session-config.test.js` (extended).
**Depends on.** VS2.2.
**What it does.** Nothing in the app's identity moves: `FUGARO_IOS_BUNDLE_ID`
and `FUGARO_ANDROID_PACKAGE` remain the only two, `eas.json` gains no identity
(`mobile/src/eas-config.test.js` stays as it is), and no committed file gains a
reverse-DNS literal — including the new one this work adds for the widget.
**Tests.** The existing scan stays green unchanged — and note what it is: the
pin in `app-config.ids.test.js` (`identityLiterals`, `IDENTITY_ALLOWED` empty)
forbids **the owner's own reverse-DNS value**, not reverse-DNS literals in
general, so this task's own "no committed file gains one" is an intent the new
assertion must carry rather than something the existing scan already enforces.
One new assertion pins the widget identifier's derivation, so that the owner's
identity — and any hard-coded name for the widget — never has to appear.
**Verification.** **`fugaro verify test`**.
**Reviewer.** standard.

**Commit.** `mobile/voice-session: the platform declarations, through the dynamic config and not app.json`.

---

## VS3 — Android

### Task VS3.1: The local Expo module, and its Kotlin skeleton — **new**

**Files.** `mobile/modules/voice-session/expo-module.config.json` (new);
`mobile/modules/voice-session/index.js` (new); `mobile/modules/voice-session/android/src/main/java/…/VoiceSessionModule.kt`
(new); `mobile/package.json` and `mobile/package-lock.json` (**edited in the
same commit as the first import**, or `mobile/src/dependencies.test.js` goes
red — F5).
**Depends on.** VS2.2.
**What it does.** The module's Android half, built with the Expo Modules API:
`start`, `updateStatus`, `setMuted`, `end`, `cleanupStaleSessions` and the event
emitter, with the port's shape enforced from VS1.2's test. On this task the
service is a skeleton that starts and stops; VS3.2 fills in the notification,
focus and routes.
**Tests.** The dependency scan stays green; the module's JS door exports the
port's shape and nothing else; a JVM unit test (if the module's Gradle setup has
one) asserts the module's method names — otherwise the port test is the whole of
it, and the plan says so rather than pretending.
**Verification.** **What an agent runs:** `cd mobile && npm test` (**`fugaro
verify test`** for the whole suite) — the dependency scan, the port's shape test
and the module door are all CI-assertable, and they are the whole of this task's
verification under Global Constraint 13. **Owner-run and local, and deliberately
not an agent's verification:** `cd mobile && npx expo prebuild -p android --clean`
— it needs `mobile/node_modules` (installed by `npm ci`, itself a network step),
it downloads and writes native projects, and Global Constraint 13 excludes both a
network and a device from a task's verification. When the owner runs it, the
generated manifest must carry VS2.2's declarations.
**Reviewer.** standard.

### Task VS3.2: The service, the notification, focus, routes — **new**

**Files.** `mobile/modules/voice-session/android/src/main/java/…/VoiceSessionService.kt`
(new), `AudioFocusController.kt` (new), and the module's notification builder.
**Depends on.** VS3.1; **D209**, **D210**, **D218**.
**What it does.** The spec's §7.1–§7.5, with the four corrections the
reconciliation established: the service **re-checks `RECORD_AUDIO` immediately
before `startForeground`** and a refusal is `AUDIO_IN_CODES.MICROPHONE_DENIED`
rather than a `SecurityException` crash (P11); the start is foreground-only and
the module refuses a background start with a named code (P11); `START_NOT_STICKY`
(P22); `POST_NOTIFICATIONS` is asked and its **denial does not stop the session**
— the app says the shade controls are unreachable instead (P14, D210); the
notification's actions are handled **in the service** and are idempotent, so a
hang-up from the shade works with no JS alive (F29); the category is
`CATEGORY_SERVICE` under D218; `ACTION_AUDIO_BECOMING_NOISY` and an
`AudioDeviceCallback` are the route signals (P18); focus follows the spec's §7.3
table, and the call-state listener is registered **only if D209 says yes**.
**Tests.** The route/focus/unplug transitions are tested in **JS** through the
fake (VS0.2's machine) — the Kotlin's own transitions are the checks in VS7.4,
because a JVM test cannot produce a call.
**Verification.** **What an agent runs:** `cd mobile && npm test` (**`fugaro
verify test`** for the whole suite) — the focus, route and unplug transitions live
in the machine and are asserted over the fake (VS0.2 extended). **Owner-run:**
`npx expo prebuild -p android --clean` and a **`development`-profile build** on a
device for the fast loop — a build is a store-credential step (Global Constraint
9) and a device is Global Constraint 10's exclusion, so neither is an agent's
verification.
**Reviewer.** **SECURITY-SENSITIVE** — this is the foreground service that holds
the microphone open, and Global Constraint 14 applies: **independent review.**

### Task VS3.3: The self-stop, and the launch cleanup — **new**

**Files.** the module's Kotlin, plus `mobile/src/voice-session/session.js`
(edited).
**Depends on.** VS3.2.
**What it does.** The spec's §9 for Android: a native watchdog that stops the
service when JS has gone quiet for longer than a bound, `cleanupStaleSessions()`
on every launch stopping a running service whose JS session is not there and
cancelling its notification, and the `serviceStopped { reason }` event.
**Tests.** The machine's `serviceStopped` transitions (VS0.2 extended) and the
launch call being made exactly once.
**Verification.** A `development`-profile build, `adb shell am force-stop`, and
the check in VS7.6; then **`fugaro verify test`**.
**Reviewer.** standard.

**Commit.** `mobile/voice-session: the Android service, the notification, focus, routes and the self-stop`.

---

## VS4 — iOS

**This is the only part with owner-only steps**, and they are
[reconciliation.md](reconciliation.md) §6.1: a new **App ID** for the widget
extension, a new **App Group** identifier, and the **App Groups capability
enabled on both App IDs**. No provisioning profile is created by hand — EAS
declares the extension and synchronizes the capability from the entitlements
(F15, P25).

### Task VS4.1: The module's Swift half — **new**

**Files.** `mobile/modules/voice-session/ios/VoiceSessionModule.swift` (new);
`mobile/modules/voice-session/ios/Shared/VoiceSessionAttributes.swift` (new);
`…/Shared/VoiceSessionIntents.swift` (new); `expo-module.config.json` (extended
to build the `Shared/` sources into **both** targets — F15).
**Depends on.** VS2.1; **D204**.
**What it does.** The spec's §6.1–§6.3, with the three corrections: `.voiceChat`
is set **for its documented effect** (voice-optimised equalisation and a reduced
route set) and the design does **not** claim it enables echo cancellation (P7) —
AEC is asked for explicitly or not at all; an `interruptionNotification`
carrying `AVAudioSessionInterruptionWasSuspendedKey` is **not** reported as a
call (P8); and a `mediaServicesWereResetNotification` **ends the session** for
the app to restart on user action, rather than silently rebuilding and resuming
(P10). `routeChangeNotification`'s `.oldDeviceUnavailable` emits
`routeChanged { unsafe: true }` (P9), on the secondary thread it is posted on.
**Tests.** The route and interruption transitions are the machine's, tested in
JS; the Swift's job is to emit the right event, which VS7.2 checks on a device.
**Verification.** **What an agent runs:** `cd mobile && npm test` (**`fugaro
verify test`** for the whole suite) — the route and interruption transitions are
the machine's and are asserted over the fake. **Owner-run and local:**
`cd mobile && npx expo prebuild -p ios --clean` — it needs **macOS with Xcode and
CocoaPods** as well as `mobile/node_modules`, so it cannot be an agent's
verification at all (Global Constraint 13).
**Reviewer.** **SECURITY-SENSITIVE** — the audio session and the microphone
(F2, Global Constraint 14): **independent review.**

### Task VS4.2: The widget target — **new**

**Files.** `mobile/targets/voice-session-widget/expo-target.config.js` (new);
`…/VoiceSessionLiveActivity.swift` (new); `mobile/app.config.js` (edited:
`extra.eas.build.experimental.ios.appExtensions`, F15); `mobile/package.json` and
the lockfile (edited — `@bacons/apple-targets` **5.0.0**, the real package name;
F14/P23).
**Depends on.** VS4.1; the owner's portal steps in §6.1 of the reconciliation.
**What it does.** The Live Activity: `VoiceSessionAttributes` with a static
`title` and a `ContentState` of `state`, `muted` and `startedAt`, rendered with
`Text(timerInterval:pauseTime:countsDown:showshours:)` so the timer needs **no**
per-second update (P4). `staleDate` is set on every update and **is described
for what it does** — it marks the activity stale, and it is Apple's own
8-hour/12-hour bounds that remove it (P5, D214). Lock screen, compact, minimal
and expanded presentations.
**Tests.** The App Group writer is asserted to write exactly the four scalars
(`state`, `muted`, `startedAt`, `title`) and **nothing else** — no credential,
no token, no transcript (F25) — in a JS test over the shared writer, and the
widget's own source gets its **own scan** (it is outside `src/`, so the existing
scans do not reach it — F25).
**Verification.** **What an agent runs:** `cd mobile && npm test` (**`fugaro
verify test`**) — the App Group writer's four scalars and the widget source's own
scan are pure and CI-assertable. **Owner-run and local, on macOS:**
`cd mobile && npx expo prebuild -p ios --clean` shows the target, and an
**`internal`-profile** build (store distribution, F16) is what the check
installs. **Fugaro never runs the build** — the owner does (Global Constraint 9),
and Global Constraint 13 keeps `prebuild` out of an agent's verification.
**Reviewer.** standard.

### Task VS4.3: The buttons — **new**

**Files.** `mobile/modules/voice-session/ios/Shared/VoiceSessionIntents.swift`
(edited); `…/VoiceSessionLiveActivity.swift` (edited).
**Depends on.** VS4.2; **D204**.
**What it does.** `ToggleMuteIntent` and `HangUpIntent` conforming to
`LiveActivityIntent` (**iOS 17.0+**, P2), inside `Button(intent:)`. The intent
runs in the **app's** process — Apple's own words: *"the system launches your app
process without opening the app, performs the intent, and starts the Live
Activity"* (P3) — so it reaches the controller directly, emits `remoteAction`,
and **does not open the app** (spec §10; D203's recommended answer).
**Under D204's alternative (iOS 16.1)** this task instead adds the `widgetURL`
deep link and **no** buttons, which is a second UI and a second behaviour to
check — the cost D204's row names.
**Tests.** None device-free beyond the shared scan; the behaviour is VS7.3.
**Verification.** As VS4.2: an agent runs `cd mobile && npm test` / **`fugaro
verify test`**; the `prebuild` and the `internal`-profile build are the owner's,
on macOS (Global Constraints 9 and 13).
**Reviewer.** standard.

### Task VS4.4: The activity's lifecycle — **new**

**Files.** `VoiceSessionModule.swift` (edited), `VoiceSessionLiveActivity.swift`
(edited).
**Depends on.** VS4.3.
**What it does.** Start on `start()` **while the app is in the foreground**,
`activity.update(...)` from `updateStatus` (throttled, D217), and
`activity.end(…, dismissalPolicy: .immediate)` on hang up; the `staleDate`
refresh; and `cleanupStaleSessions()` ending every
`Activity<VoiceSessionAttributes>` on launch (§9).
**Tests.** The `updateStatus` throttle is already VS1.1's; the launch cleanup is
asserted to be called once.
**Verification.** As VS4.2, plus the check VS7.3.
**Reviewer.** standard.

**Commit.** `mobile/voice-session: the iOS audio session, the widget target and the Live Activity's lifecycle`.

---

## VS5 — What a kill leaves behind

### Task VS5.1: `cleanupStaleSessions()` on both platforms — **new**

**Files.** `mobile/src/voice-session/session.js` (edited), the two native
halves (edited), `mobile/index.js` (edited).
**Depends on.** VS3.3, VS4.4.
**What it does.** The spec's §9, in one place: the call on every launch, the
`serviceStopped` event, and the assertion that the cleanup itself **reads no
credential** — because the launch may be a locked phone (F24).
**Tests.** The call happens exactly once per launch; a cleanup with a locked
store still completes (the fake's secure store throws, and the cleanup must not
depend on it).
**Verification.** The check VS7.6 on each platform; **`fugaro verify test`**.
**Reviewer.** standard.

### Task VS5.2: The paused session's contract — **new** (a test-only task, and deliberately its own commit)

**Files.** `mobile/src/voice-session/session.test.js` (extended),
`mobile/src/voice-session/index.test.js` (extended).
**Depends on.** VS5.1.
**What it does.** Writes down, as tests, the two invariants the reconciliation
found and the spec does not state: a **paused** session issues no mailbox call
and reads no credential (F24 — the store is unreadable while the phone is
locked, and the failure would look like a bug), and **nothing is buffered for a
resume** (F9 — there is nothing to replay, and the state that crosses to the
widget is four scalars).
**Tests.** As above, and one that mutates the app-group payload and fails if it
ever grows a fifth field.
**Verification.** **`fugaro verify test`**.
**Reviewer.** **SECURITY-SENSITIVE** (the credential and the shared container):
**independent review.**

**Commit.** `mobile/voice-session: the launch cleanup, and the paused session's two invariants as tests`.

---

## VS6 — The way back in

### Task VS6.1: The "Tap to resume" banner — **new**

**Files.** `mobile/src/voice-session/resume.js` (new);
`mobile/src/voice-session/resume.test.js` (new); the screen that shows it
(edited).
**Depends on.** VS5.1; D201, D211 (the banner is the recommended first cut and
needs no permission).
**What it does.** After `endReason: 'interruption_timeout'`, the next open shows
a dismissible banner whose one action starts a **fresh** session (spec §8's
step 3). It says *why* the session ended and nothing about what was said.
**Tests.** The banner appears once per ended session; dismissing it leaves no
state; the one tap calls `start()` with a fresh session; **the banner's text
carries no transcript** (F9).
**Verification.** **`fugaro verify test`**.
**Reviewer.** standard.

### Task VS6.2: The local notification — **later, and its own task**

**Files.** the two native halves (edited).
**Depends on.** VS6.1; **D211**.
**What it does.** The spec's §8's *"optionally a normal, dismissible local
notification"*: a channel, the `POST_NOTIFICATIONS` ask (Android), and a
lock-screen surface that says **that a session ended** and nothing else (F12).
It inherits none of P5.2's push machinery (no token, no sender, no server) and
all of its privacy posture.
**Verification.** The check VS7.3/VS7.4 extensions; **`fugaro verify test`**.
**Reviewer.** **SECURITY-SENSITIVE** (a lock-screen surface): **independent
review.**

**Commit.** `mobile/voice-session: the tap-to-resume banner, and the local notification behind it`.

---

## VS7 — The checks

Six checks, written into the register in the house style of
[../../gcp-live-checklist.md](../../gcp-live-checklist.md) — a guard block, the
`FACT:` lines, the *Passes when* / *Fails when* pair, what to paste back, and the
restore — each **simple and scripted where a script can stand in for a hand**,
each run by the **owner**, and **none written until the part it covers is
approved and shipped**. A check is never run before its text is in the register.

### Task VS7.1: Checks 113–118, as text — **OWNER-RUN**

**Files.** `docs/gcp-live-checklist.md` (the six new sections, added by the
work that ships each one — see the six tasks below; this task is where their
**text** is agreed).
**Depends on.** The parts each check covers, and a release (F16).
**Verification.** Each section exists, carries its number, its guard block and
its restore; and **Check 86's section is untouched** (F17).
**Reviewer.** owner.

### Tasks VS7.2–VS7.6: The runs — **OWNER-RUN**

| Task | Check | Runs after |
|---|---|---|
| **VS7.2** | **113** — interruptions and the timeout | VS4.4 |
| **VS7.3** | **114** — the Live Activity and its buttons | VS4.4 |
| **VS7.4** | **115** — the Android service, its actions, and notifications denied | VS3.3 |
| **VS7.5** | **116** — route changes, both platforms | VS4.4 and VS3.3 |
| **VS7.6** | **117 and 118** — a kill's leavings, and the microphone's invariants | VS5.2, VS6.1 |

**Files.** None; the owner records the `FACT:` lines and pastes the results back.
**Depends on.** The part named above, and an **`internal`-profile** build (F16)
on the owner's iPhone for the iOS rows. The Android rows need an Android 14+
device; an emulator can rehearse the permission ask, the notification and its
actions, and the force-kill, and **cannot** show a real call, a real route change
or Doze ([reconciliation.md](reconciliation.md) §6.3).
**Verification.** The owner runs each check on a **test account, sandbox only,
never `belong`**, and the result is recorded. **Reviewer.** owner. **No agent
performs any step.**

---

## Risks

- **The app is voiceless until Check 85 runs** (F7). Everything here is written
  to run with the pipeline silent (D208), but the *value* of the feature is
  audible, and the owner will not hear it until the engine bake-off lands. The
  plan's ordering does not depend on it, and nothing here schedules it (O1).
- **`@bacons/apple-targets` against Expo SDK 58 is unverified** (F14, U4): its
  peer range is `expo >= 52`, but it pins an older `@expo/prebuild-config` line,
  and EAS's extension support is labelled **experimental** by Expo (F15). If it
  fails, the fallback is Expo's own `ios.appExtensions` config or a bare target in
  a checked-in `ios/` — a larger change, and the reason VS4.2 is its own task.
- **The App Group carries state to a less-protected container** (F25). The
  mitigation is the four-scalar assertion in VS4.2 and VS5.2, and it is a
  **SECURITY-SENSITIVE** change with independent review.
- **F24 is the subtlest risk in this work**: the secure store is unreadable while
  the phone is locked, so a resumed or paused session must need no credential.
  VS5.2 exists to pin it, and it is the finding most likely to be rediscovered
  as a bug if it is not written down.
- **Android's background-start rule is absolute** (F26): a microphone service
  cannot start from the background at all, so any design that wants to *start* a
  session from a notification is impossible, not merely discouraged. The plan
  never offers one.
- **Five of the spec's platform claims do not survive the check** (§4 of the
  reconciliation): three are Wrong (P5, P7, P23) and two are wrong as written in
  a way that changes a design (P14, P20). Two of the five would have been built
  **exactly as the spec writes them** — a `staleDate` described as something that
  ends an activity (it marks one stale), and `.voiceChat` described as enabling
  echo cancellation (it does not promise it) — because each reads as a platform
  guarantee until the platform's own page is opened.
- **The owner may not have an Android device** (F30). VS7.4's and VS7.5's
  Android rows are written, and three of their subjects cannot be shown on an
  emulator — stated rather than discovered late.
- **D205 is the plan's load-bearing decision.** Its recommended answer
  (JavaScript) is what makes VS0–VS2 CI-runnable and cheap; the alternative
  rewrites four parts and gives up every device-free test in this plan.

---

## OWNER APPROVAL GATE

**Not passed. No part is approved. Nothing is implemented.** D201–D220 in
[reconciliation.md](reconciliation.md) §5 are open; each carries a
recommendation, and **answering one is not a go**.

To start any part the owner names it (or a task) and says go. What each part
waits for, in one line:

| To start | The owner must have |
|---|---|
| **VS0** | answered **D205** (where the machine lives) — and no other decision |
| **VS1** | VS0 merged, and **D213** (VS1.2's reporting shape) |
| **VS2** | VS1 merged, and **D207** (the background mode VS2.1 declares) |
| **VS3** | VS2 merged, and **D209**, **D210**, **D218** |
| **VS4** | VS2 merged, **D204**, **D203**, and the **Apple portal steps** of the reconciliation's §6.1 | 
| **VS5** | VS3 and VS4 merged |
| **VS6** | VS5 merged, and **D211** for its second task |
| **VS7** | the part each check covers, merged, and a release |

**The tasks that wait on no decision at all** — only on D205, which is a
one-line question about where the code lives — are **VS0.1, VS0.2 and VS0.3**:
the event contract, the reducer and its tests. They ship no native code, touch
no manifest, open no microphone and require no device, and they are the whole of
what the spec's own §11 asks CI to hold. **They are the minimum first cut that is
useful and safe**, and they are useful even if every other part of this plan is
never approved: they are the machine, written down, before anything can be built
on a wrong idea of what it is.

**Not before the gate:** no `prebuild`, no `eas build`, no native module, no
manifest change, no portal step, no check run. The gate is the owner's, and this
file does not pass it.
