# Reconciliation: the hands-free voice session (`voice-session`) against the tree

**Status: plan only; nothing is implemented; decisions open.** This document
resolves nothing. It reads the owner's [spec.md](spec.md) — written **without
seeing this repository** — against the code on `main`, names every place the
spec is **Wrong**, already **Exists**, is a **Gap**, restates a **Rule** this
tree has already made or leaves something **Open**, states the **consequence**
of each for the design, and hands the choices to the owner as **D201–D220**,
every row with a recommendation, the alternative and its cost. **No answer is
recorded here and none is taken.**

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-20. **Base:** `main`
at `c92093b6` (the mobile tree at `12fbe60e`, mobile P3.5, #677).
**Index.** [README.md](README.md). **Plan.** [implementation-plan.md](implementation-plan.md).

---

## 0. How this was checked

| | |
|---|---|
| **Baseline** | `main` at `c92093b6` — "website: retire the orphaned four-step flow and give its gates to what the pages show now (#756)", 2026-10-20. The mobile tree it carries is `12fbe60e` ("mobile P3.5: the voice screen — the interrupt switch, the queue, the offline refusal, the lease", #677), which is the commit `mobile/src/voice/` arrives at. |
| **Date of the check** | 2026-10-20 |
| **Read** | `mobile/` (all of `src/`, `index.js`, `app.json`, `app.config.js`, `eas.json`, `package.json`, `README.md`), `docs/mobile/**`, `docs/voice-module/**`, `docs/design/voice.md`, `docs/design/desktop-console.md` §7.4, `docs/gcp-live-checklist.md`, `docs/coordinator/README.md`, `docs/setup-wizard/**` (the folder shape this one follows). |
| **Fetched** | Every platform claim below was read from Apple's or Google's **own** documentation on 2026-10-20, by URL, and the URL is in the table. Apple's pages are read in their Markdown form (`<url>.md`), which is the form Apple publishes *for* automated readers. |
| **Citations** | Every finding cites a **path and a symbol**, never a line number. Where a figure is volatile (a count that any edit changes) the **command** is given instead of the figure. |
| **What this document is not** | It is not a second spec, and it decides nothing. Where it disagrees with [spec.md](spec.md), **it wins** — the spec was written without the repository and this was written against it. |

---

## 1. What `voice-session` is, and what it is not

This is the finding the rest of the document depends on, so it comes first.

**`voice-session` is a third layer, and it is not `docs/voice-module/`.**

| | `docs/voice-module/` | `mobile/src/audio/` (P3.4) + `mobile/src/voice/` (P3.5) | this work's `voice-session` |
|---|---|---|---|
| **Owns** | the **pipeline**: STT → orchestrator/LLM → TTS, backend swapping, VAD, wake word, barge-in, filtering and summarising | the **ports and their fakes** (audio in, STT, TTS), the pipeline object, and the screen: the interrupt switch, the queue, the offline refusal, the lease | the **audio session and the session lifecycle**: is a session running, and what the OS, a call, an unplugged headset or a killed process did to it |
| **Where** | the Electron console first, the phone later — `docs/voice-module/spec.md`'s own Goal | `mobile/`, in the tree, merged (#677) | `mobile/`, not written |
| **State** | reconciliation only; Q1–Q13 answered 2026-10-19, nothing implemented | shipped, and **voiceless**: no engine exists (F7) | plan only |

The spec's own §2 non-goals say the same thing in one line — *"The speech
pipeline itself (STT, LLM, TTS). This module owns the audio session and
lifecycle, not the content."* The evidence for the first column is
`docs/voice-module/spec.md` ("Pipeline, not speech-to-speech: speech-to-text →
orchestrator/LLM → text-to-speech") and its own **"Phone-specific
considerations"** bullet — *"Interruptions: calls, Siri, other apps taking the
audio session; pause and resume gracefully"* — which is one line, and which this
spec is the full treatment of.

**So the answer to the brief's question is: neither.** `voice-session` is not
`docs/voice-module/` (that is the pipeline, and the two must not be merged: the
pipeline's ports are the thing this module starts and stops) and it is not a
part of it. It is **the layer the pipeline sits inside**, and the two meet at
exactly one seam — `mobile/src/audio/pipeline.js`'s `createVoicePipeline`, whose
`start()`, `stop()`, `speaking()` and `turn()` are the four things a session has
to drive and read.

The one sentence of the voice module's spec that this work **reverses** is its
Goal's last clause: *"App stays in the foreground while talking (no background
listening)."* That was the console's ruling, and the phone's spec asks for the
opposite. Finding **F2**.

---

## 2. The headline, in one table

| What the spec asks for | What the tree already has | Where |
|---|---|---|
| One platform-agnostic JS API for the session | A JS **pipeline** with its own small state machine (`idle`/`open`/`flushing` + `turnId`) and a JS **screen** with four decisions of its own | `mobile/src/audio/pipeline.js` `createVoicePipeline`, `isVoicePipeline`, `PIPELINE_EVENTS`; `mobile/src/voice/screen.js` `createVoiceScreen` |
| A state machine of eight states (§4) | Two machines already, neither of them this one — and the pipeline's owner-turn state is the one P3.5's interrupt rule reads | `mobile/src/audio/pipeline.js` (`speaking()`, `turn()`); `mobile/src/voice/interrupt.js` `interruptDecision` |
| Mic alive in the background | D122 declared the background audio mode **"only if barge-in needs it"**; `AUDIO_IN_CODES` already carries `background-audio-denied` | `docs/mobile/reconciliation.md` D122; `mobile/src/audio/audio-in.js` |
| A Live Activity with buttons | Nothing at all; the app has **no** widget target, no App Group, no `NSSupportsLiveActivities` | `mobile/app.json`, `mobile/app.config.js` |
| An Android foreground service + notification + focus + call state | Nothing at all; the app declares **no** Android permission | `mobile/app.json` (`android: {}`) |
| Crash/kill recovery | Nothing; `mobile/index.js` constructs no audio at all | `mobile/index.js` |
| "Unit tests: the JS state machine against a mocked native event stream" | Exactly this shape is CI-able and is what Global Constraint 10 asks for | `docs/mobile/implementation-plan.md` Global Constraint 10 |

**Nothing in the spec's iOS or Android half exists.** The whole of the value
below is in saying *where* it goes and *what it collides with*.

---

## 3. Findings

Each finding: its class, the evidence (path and symbol), and **the consequence
for the design**.

### F1 — `voice-session` is a third layer: neither `docs/voice-module/` nor P3.5. **Rule.**

**Evidence.** `docs/voice-module/spec.md` (Goal: *"Pipeline, not
speech-to-speech"*; *"App stays in the foreground while talking (no background
listening)"*). `mobile/src/audio/pipeline.js` (`createVoicePipeline`,
`PIPELINE_EVENTS`). `mobile/src/voice/screen.js` (`createVoiceScreen`).
`docs/voice-module/README.md` (*"reconciliation only; nothing is implemented"*).
[spec.md](spec.md) §2's non-goals.

**Consequence.** §1 above is the answer, and the plan may not put pipeline code
in the new module or session code in the pipeline. The seam is four calls:
`pipeline.start()`, `pipeline.stop()`, `pipeline.speaking()`, `pipeline.turn()`.

---

### F2 — The console's voice module ruled "no background listening"; this spec reverses it for the phone. **Open.**

**Evidence.** `docs/voice-module/spec.md`'s Goal. `docs/mobile/reconciliation.md`
**D122**, answered 2026-10-17: the microphone is an OS permission with a
usage-description string the owner writes, *"the background audio mode is
declared **only if barge-in needs it**"*, and the app *"degrades to a visible
'tap to speak' control"* when the OS refuses. `mobile/src/audio/audio-in.js`'s
`AUDIO_IN_CODES.BACKGROUND_AUDIO_DENIED`.

**Consequence.** Background execution is a capability the phone's app has never
had, and D122's conditional is now answered by the spec: it **is** needed, and
for more than barge-in — the session is supposed to survive the app being
backgrounded. D122's own fence still holds (`NSMicrophoneUsageDescription` the
owner writes; the two denial codes; the visible fallback), so this is an
**extension** of D122 and not a reversal of it. It is worth a decision only
because D122's text says "only if barge-in needs it" and a reader will otherwise
think it was breached silently. Decision **D206**.

---

### F3 — P3.4 already owns a state machine, and P3.5's interrupt rule reads it. **Exists** (with a **Gap**).

**Evidence.** `mobile/src/audio/pipeline.js`: `createVoicePipeline({ audioIn,
stt, tts, sender, newId, now })`, `isVoicePipeline` = `['start','stop','say',
'speaking','turn','on']`, `PIPELINE_EVENTS` =
`['transcript','message','utterance','refused','unavailable']`, and the module's
own states `idle` / `open` / `flushing` plus a `turnId`. `mobile/src/voice/interrupt.js`:
`interruptDecision({ switch, severity, highest, speaking, turn })` — the
`speaking` and `turn` arguments come from the **pipeline**, and
`mobile/src/voice/screen.js`'s `drain()` asks for them on every pass.

**Consequence.** The spec's eight states are a **second** machine, and it must
not be a second copy of this one. The split is by **subject**:

* the **pipeline** keeps the *turn* — whose turn it is, whether an utterance is
  in flight, whether the last one was `finished` or `interrupted`;
* **`voice-session`** owns the *session* — whether there is one at all, whether
  it is paused and why, and what ended it.

So `listening` and `speaking` in the new machine must be **derived** from
`pipeline.turn()` and `pipeline.speaking()`, never stored beside them. A stored
copy would make `interruptDecision` judge a flag that had gone stale — and the
failure that produces (`an update spoken over the user`) is exactly the failure
P3.5 exists to prevent. Decision **D205**.

---

### F4 — P3.5's screen has no session, no mute and no end reason. **Gap.**

**Evidence.** `mobile/src/voice/screen.js`'s `state()` returns `{ switch,
presence, offline, microphone, queued, lease, leaseRefused, speaking, turn,
transcript, refused }` — and nothing else. Its `start()` calls `pipeline.start()`
and records D122's two denials; its `stop()` calls `pipeline.stop()`.

**Consequence.** The four decisions P3.5 owns (the switch, the queue, the
offline refusal, the lease) stay where they are and are **not** re-implemented.
`screen.start()` and `screen.stop()` become calls the app makes *through* the
session controller rather than around it — one path to the microphone, not two.
The session's own fields (`muted`, `endReason`, the pause reason, the elapsed
clock) are new and belong to the new module.

---

### F5 — `voice-session` cannot live under `src/audio/`, and a native module changes the manifest. **Rule.**

**Evidence.** `mobile/src/audio/index.test.js` — the door's export list is pinned
against a literal list, and a recursive scan asserts that no file under
`src/audio/` imports **any** package, that it reaches outside `mobile/` only into
`shared/`, and that no file reads a clock, a random source, a file or the
network. `mobile/src/dependencies.test.js` — every bare import must be declared
in `mobile/package.json`, and the lockfile must match the manifest entry for
entry (`npm ci` refuses the pair otherwise); a note there also records that
`src/audio/index.js` names `expo-av` and `react-native-voice` **only in prose**,
which is why the scan takes the comments out first.

**Consequence.** The JS controller is a **new** directory —
`mobile/src/voice-session/` — with its own door and its own scan, shaped like
`src/audio/` and `src/voice/`. The native module is a **local Expo module**
(`mobile/modules/voice-session/`, its own `expo-module.config.json`), which the
app's JS imports by package name, so **`mobile/package.json` and
`mobile/package-lock.json` change in the same commit as the first import** or the
dependency scan goes red. The comment-stripping in that scan is why a *prose*
mention of a native API is safe and an *import* is not.

---

### F6 — Every native declaration lands in `app.config.js`, and the App Group entitlement cannot go in `app.json`. **Exists.**

**Evidence.** `mobile/app.json` carries `plugins: ["expo-secure-store"]`,
`ios: { supportsTablet: false }` and `android: {}` — no permission, no usage
string, no identifier. `mobile/src/app-config.test.js` asserts
`'bundleIdentifier' in CONFIG.expo.ios === false`, `'package' in
CONFIG.expo.android === false`, and — the fence that matters here —
`assert.equal(RAW.includes('com.'), false, 'no reverse-DNS identity is
committed here')`. `mobile/app.config.js` is the dynamic config that resolves
`FUGARO_IOS_BUNDLE_ID` / `FUGARO_ANDROID_PACKAGE` from the environment and
merges `ios.infoPlist`. `mobile/src/eas-config.test.js` keeps both identities out
of `eas.json` for the same reason.

**Consequence — and this one is concrete.** The App Group entitlement is
`com.apple.security.application-groups`. That string contains `com.`, so it
**cannot be written into `app.json` without turning an existing test red on
purpose.** Every declaration the spec's §6.6 and §7.6 ask for therefore goes
through `app.config.js` (or a plugin it applies): `UIBackgroundModes`,
`NSMicrophoneUsageDescription`, `NSSupportsLiveActivities`, the App Group, the
Android `uses-permission` list, the FGS type, and the widget target's own
settings. **The widget's bundle identifier** takes the same route — it is the
app's own identifier plus a suffix, so it is **derived from
`FUGARO_IOS_BUNDLE_ID`** rather than added as a third environment variable,
because each new variable is another thing the owner must set in three EAS
environments and in a local shell.

**The Apple Team ID is a different thing and is not derived from anything.** It
is the **ten-character identifier Apple assigns to the owner's team**; it cannot
be computed from a bundle identifier, and no string transformation produces it.
It is also **not a secret and not an identity this work introduces**: EAS already
holds it with the owner's Apple credentials (the certificates and provisioning
profiles it manages are the team's), and the app target is already signed with it
whenever the owner runs an `internal` build. So the plan **reads it from EAS's
Apple credentials** where the generated extension target needs it, and the
**owner's only job is the three portal steps of §6.1** — it is never written into
`app.config.js`, `app.json` or `eas.json` as a literal, which the identity fences
(`mobile/src/app-config.test.js`'s `com.` assertion, `mobile/src/eas-config.test.js`)
would in any case refuse.

---

### F7 — The app is voiceless by the owner's ruling of 2026-10-19. **Rule.**

**Evidence.** `mobile/README.md`: *"It ships **without an engine**: the first
store build is voiceless (the owner's ruling of 2026-10-19), so what is here is
the screen's logic over the interfaces, which is what D126 asks CI to hold."*
`docs/mobile/implementation-plan.md` P5.3 and D123/D46 (on-device engines only,
chosen by a bake-off — owner-run **Check 85**, which the register shows as not
run and not written). `mobile/src/audio/index.js` names the refused packages in
prose.

**Consequence — the plan's hardest ordering constraint.** A session can be
started, paused, resumed and ended with **no speech engine at all** — the
microphone can be opened and closed, the Live Activity and the notification can
be driven, the interruptions can be observed. What cannot happen without an
engine is *anything being said*. So every device check this work writes must be
runnable with the session up and the pipeline silent, or must wait for Check 85.
Decision **D208**.

---

### F8 — No test may need a device, a microphone or a live cloud. **Rule.**

**Evidence.** `docs/mobile/implementation-plan.md` Global Constraint 10; D126 in
`docs/mobile/reconciliation.md`; the fakes in `mobile/src/audio/`.

**Consequence.** The spec's §11 unit tests — *"the JS state machine against a
mocked native event stream, including timeout and `shouldResume: false`
paths"* — are exactly the shape this tree allows, and they are the **whole** of
what CI can prove here. Everything native is an owner-run check. The new module
should be built so that the machine is a pure function over an event stream, with
the native module as a port behind it — the twin of `src/audio/`'s fakes.

---

### F9 — No audio is stored or uploaded. **Rule.**

**Evidence.** `docs/mobile/implementation-plan.md` Global Constraint 12 (with its
2026-10-19 note for the evaluation switch only);
`docs/mobile/design/voice-evaluation-switch.md` §1a;
`mobile/src/audio/audio-in.js`'s `AUDIO_IN_EVENT_FIELDS` is `['kind']` — one
field, and no samples cross the port.

**Consequence.** Two design consequences, both of them load-bearing:

1. A paused session has **nothing to replay**. Resuming re-opens the microphone;
   it does not resume a stream, because there is no buffer to resume from. The
   spec's §10 *"do not replay the cut-off response mid-sentence"* is therefore
   consistent with a rule this tree already has, not merely a UX preference.
2. The state the widget and the notification read (F25) must be a handful of
   scalars — a state name, a boolean, a timestamp, a title — and never a
   transcript. A transcript on a lock screen is content the app would be
   publishing to a surface it does not authenticate.

---

### F10 — Demo mode is text-only, and its fences are pinned. **Exists.**

**Evidence.** `mobile/src/demo/index.js` — `createDemoMode` takes `{ now }` and
nothing else, and `mobile/src/demo/safety.test.js` pins that (`createDemoMode.length
=== 0`); its three scans are *no network*, *no credential or secure storage*, *no
secret*. Nothing under `src/demo/` imports `src/audio/`. The demo's
"speaking" is `mobile/src/mailbox/speaker.js`'s `MemorySpeaker`, a port double.
`docs/mobile/play-review-access.md` is the Play form text that describes it.

**Consequence.** A voice session in demo mode would open the microphone on the
exact screen a Play reviewer is told to tap — and the demo's own safety test
would have to be widened to allow it. The honest answer is that the demo does
**not** enter voice mode, and the plan says so. Decision **D212**.

---

### F11 — There is no telemetry in this tree, and the brief's ruling is not here. **Wrong.**

**Evidence.** `docs/design/desktop-console.md` §7.4 — *"no telemetry, no
analytics"*, cited by `docs/mobile/design/voice-evaluation-switch.md`.
`docs/mobile/design/push-notifications.md`: *"**No Firebase Analytics SDK is
linked** (`mobile/package.json` declares no Firebase or …)"*. `mobile/package.json`
declares `expo`, `expo-secure-store`, `@react-native-async-storage/async-storage`,
`react`, `react-native` — and nothing else.

**Consequence.** "Session lifecycle errors are telemetry candidates" (the brief)
is true as a **candidate** and false as a **capability**: there is no Crashlytics
and no Analytics in the app, no decision in this tree adopted them, and adding
one is a new SDK, a new manifest entry, a Data safety form change and an egress
question. So the first cut reports a session failure where the app already
reports everything — on its own screen, and (later) through the mailbox — and a
crash reporter is its own decision with its own cost. Decision **D213**.

---

### F12 — The push design is a different subject, and D121 said "no push in v1". **Rule.**

**Evidence.** `docs/mobile/design/push-notifications.md` (P5.2, design only, its
Q1–Q10 open for the owner); `docs/mobile/reconciliation.md` **D121**, answered
2026-10-17: no push in v1, deferred to "a separate decision with its own service,
its own device-token store and its own retention question". [spec.md](spec.md)
§8's *"optionally a normal, dismissible local notification"*.

**Consequence.** The "Tap to resume" entry point is a **local** notification: no
token, no sender, no rules and no server. It inherits none of the push design's
machinery and none of its open questions — but it does inherit its **privacy
posture**, because the lock screen is a surface anybody can read: a resume
notification says *that a session ended*, and nothing about what was said.
Decision **D211**.

---

### F13 — The microphone is already a decided permission; the new asks are store declarations. **Exists.**

**Evidence.** `docs/mobile/reconciliation.md` **D122** (the recommendation, and
the owner's answer of 2026-10-17: *"minimal OS permissions, with
usage-description strings the owner writes"*). `docs/mobile/functional-spec-reconciliation.md`
**G22** (the text fallback is wider than D122's "tap to speak").
`mobile/src/audio/audio-in.js`.

**Consequence.** The spec adds no new *permission principle* — but it adds three
things D122 never named, and each is a store-facing declaration the owner alone
can make: the Android **foreground-service type** and its Play declaration (F19),
the **Live Activity** and its Info.plist key, and the **App Group** with the extra
bundle identifier (F15). They belong in the plan's owner-steps, not in a code
task.

---

### F14 — `expo-apple-targets` does not exist; the package is `@bacons/apple-targets`. **Wrong.**

**Evidence.** The npm registry has no package named `expo-apple-targets` (the
name 404s). The plugin the spec means is `@bacons/apple-targets`, latest **5.0.0**
(published 2026-07-17; 50 versions since 2023-12-04), a **community** plugin by
Evan Bacon, with peer dependency `expo >= 52`, depending on `@expo/prebuild-config`
and `@bacons/xcode` (a pbxproj parser). Its README requires **CocoaPods 1.16.2,
Xcode 16, macOS 15 Sequoia and Expo SDK 53+**, and drives targets with `npx
create-target <kind>` and a `targets/<name>/expo-target.config.js` at the Expo
project root.

**Consequence.** The plan names the real package, pins `5.0.0` and says whose
plugin it is (not Expo's own). The spec's tree `targets/voice-session-widget/`
resolves to **`mobile/targets/voice-session-widget/`**, because the Expo project
root is `mobile/`. And the plugin's floor (SDK 53+) is satisfied by
`mobile/package.json`'s `expo: ^58.0.16` — but see the **UNVERIFIED** table: the
plugin's own dependency pins `@expo/prebuild-config ~55.0.6`, an older line than
SDK 58's, and nothing in this tree has run it against SDK 58.

---

### F15 — EAS Build's support for app extensions is declared in the config and is experimental. **Gap.**

**Evidence.** Expo's *iOS App Extensions* reference: a CNG project declares its
extensions under `extra.eas.build.experimental.ios.appExtensions` in the app
config *"so that EAS CLI can know what app extensions exist before the build
starts (before the Xcode project has been generated) to ensure that the required
credentials are generated and validated"* — and the section is headed **"CNG
projects (experimental support)"**. Expo's *iOS capabilities* reference: *"EAS
Build automatically synchronizes capabilities on the Apple Developer Console with
your local entitlements configuration when you run `eas build`"*, from
`ios.entitlements`, and **App Groups
(`com.apple.security.application-groups`) is in its table of supported
capabilities**. `@bacons/apple-targets`' README: targets that can use App Groups
*"automatically mirror the `ios.entitlements['com.apple.security.application-groups']`
array from the `app.json`"*; entitlements are written to a generated file under
`ios/.targets/<productName>/generated.entitlements` and pointed at by
`CODE_SIGN_ENTITLEMENTS`. `mobile/eas.json`.

**Consequence.** The widget target and the App Group are declarable in
`app.config.js` and EAS generates the extension's credentials and enables the
capability — **which is why the owner does not have to create a provisioning
profile by hand** (see §6). But this is the plan's one experimental corner, so
the owner-run check reads the **introspected config** and the build's own target
list, rather than trusting the plugin's silence.

---

### F16 — The profile the owner builds with is `internal`, and it is a store build. **Exists.**

**Evidence.** `mobile/eas.json` — `build.development` (`developmentClient: true`,
`distribution: internal`), `build.preview`, `build.internal`
(`distribution: store`, `android.buildType: app-bundle`, `autoIncrement: true`),
`cli.requireCommit: true`, `cli.appVersionSource: remote`, and `submit.internal`
with `ios.ascAppId` and `android.track: internal`.
`docs/gcp-live-checklist.md` **Check 89** (owner-run; the store identity is what
it stops on).

**Consequence.** Two build paths, and the checks must say which one they need:
the **`internal`** build (store-distributed, so it reaches TestFlight / the Play
internal track) for everything that must survive signing — the Live Activity, the
App Group, the Play declaration, the notification — and the **`development`**
dev-client build for the fast loop. An `expo start` session cannot show any of
the native behaviour at all.

---

### F17 — Check 86 already owns "hands-free audio". **Rule.**

**Evidence.** `docs/mobile/implementation-plan.md`'s check table — **86**,
*"Hands-free audio: the background mode, the microphone ask, Bluetooth, and
barge-in"*, attributed to P3.5 and P4.2, and run by the owner; `docs/gcp-live-checklist.md`
carries no `## Check 86` section yet (checks 80–89 are the mobile block);
`docs/mobile/README.md` **reserves** 80–89 for the mobile work and writes none of
them (*"Checks 80–89 — this work — reserved here, and no row or section is
written by this pull request"*). The **owner-run** fact is not in that file but
in **Global Constraint 10** of `docs/mobile/implementation-plan.md`: *"No test may
need a live cloud, a real device or a real microphone. Those are checks 80–89,
owner-run (F30)."*

**Consequence.** This work **does not rewrite 86 and does not claim it**. Where
the two touch — the Bluetooth route, and the microphone ask — 86 keeps the
subject it was allocated and the new checks cover what it does not name: a call
arriving mid-session, an unplug **mid-response**, the Live Activity's buttons,
the Android notification's actions, and what a force-kill leaves behind. The
boundary is stated per check in the plan so that the owner runs each once.

---

### F18 — The register's next free number was **112**; it is claimed, so this work takes **113–118**. **Rule.**

**Evidence.** `docs/gcp-live-checklist.md`: written sections run to **89** (81,
82 and 89 are the mobile block's, and 89's section is the last);
`docs/credentials-service/README.md` holds **90–99**;
`docs/mobile/functional-spec-reconciliation.md` holds **100–109** (100–104
proposed, 105–109 **reserved and unused**); `docs/mobile/design/voice-evaluation-switch.md`
**names 110** and writes none of it (its Q4, answered 2026-10-19); the
setup-wizard work takes **111** — `docs/setup-wizard/reconciliation.md` (*"110 is
claimed by an open pull request … **111 is therefore the next free number**"*),
its `implementation-plan.md` (*"the next free number, and it is taken here"*) and
its `README.md`'s number table, which is the folder's index and not the sentence's
source. **And 112 is proposed by another work**: `docs/windows-cli/` — **merged to
`main` as `58bd6b05` (PR #757) and not present in this branch** —
*"**Check 112** — **proposed** for the one part of this work that is genuinely a
check in the register's sense — a first run on a real Windows machine, from a
downloaded archive, by the owner, in three terminals … and **not claimed and not
written** by this pull request"*; the command is
`git grep -n 'Check 112' origin/main -- docs/windows-cli/`. The four rules this
finding rests on, each attributed to the file it is in rather than lumped as the
register's: the register's own are *"a reserved number is never reused or
renumbered"* and the rule that decides between two claims, *"whichever of two
open claims merges second renumbers itself"*
([../../gcp-live-checklist.md](../../gcp-live-checklist.md)); *"the phase that
ships the check writes them"* is
[../../design/voice.md](../../design/voice.md) §5.3's convention — the one the
project-centric plan sets and the console plan follows, and **not** a sentence of
the register's; and *"A check is never run before its text is in the register"* is
[docs/mobile/implementation-plan.md](../implementation-plan.md)'s own sentence.

**Consequence.** The register's collision rule decides it: **#757 is on `main`
and this branch is not, so this branch merges second and yields 112.** This work
therefore takes **113–118** — six checks, listed in the plan — exactly as
[docs/setup-wizard/](../../setup-wizard/) recorded for the 110/111 collision, and
**writes none of them into the register**, because the phase that ships a check
writes it and no phase here is approved. Decision **D219**.

---

### F19 — The decision register: this work takes **D201–D220**. **Rule.**

**Evidence.** `docs/coordinator/README.md`'s block table: D1–D20 console,
D21–D38 console v2, D39–D54b voice, D55–D78 managed projects, **D79–D89
deliberately unused** (*"an unused number is not a free number"*), D90–D109
website, D110–D129 mobile, D130–D159 credentials service, D160–D179 mobile
functional spec (D174–D179 reserved), D180–D192 website redesign (proposed), and
*"**D193 onward** — free"*. **Anchored to this work's base commit `c92093b6`**,
the highest number that command finds anywhere in `docs/` is **D193** —
`git grep -hoE '\bD[0-9]{2,3}\b' c92093b6 -- docs/ | sort -t D -k2 -n | tail -1`
— and the hit is the coordinator table's **own free-block label** (*"D193
onward"*, the row that allocates the next block), **not a written decision**: the
highest number ever *written as a decision* is **D192**, the last of the website
redesign's proposed block. The distinction is the point of the anchor, and the
figure moves twice over: this folder's own **D201–D220** are written under `docs/`
by the commit that carries this reconciliation, so from that commit onward the
same command returns D220. (`sort -u` would not have surfaced the maximum even at
the base commit, because `D200` sorts before `D54`; a figure this volatile is
given as a **command against a commit**, never as a bare number — §0's rule.)

**Consequence.** The brief records **D200** as an already-taken ruling
(*"development builds only, no Expo Go"*, F20) and no such row exists in this
tree. So this work starts at **D201** and leaves D193–D200 alone rather than
colliding with a number the owner's own notes may hold. A number this tree cannot
read is still a number this tree should not take.

---

### F20 — "Development builds only, no Expo Go" is ruled in the brief and is not in this tree. **Open.**

**Evidence.** [spec.md](spec.md)'s header (*"Platforms: iOS and Android, via Expo
(development builds only, no Expo Go)"*). `grep -rn 'Expo Go' docs/ mobile/`
finds the phrase nowhere outside this folder: its seven hits are this folder
citing the brief — the spec's header, [README.md](README.md)'s number table and
this document's own rows — and the ruling itself is in no other file.
`docs/coordinator/README.md`'s table stops at D192; no D200 row exists.
`mobile/package.json` declares no `expo-dev-client` (only `eas.json`'s
`build.development.developmentClient: true`).

**Consequence.** The ruling is **taken as given** — the plan depends on it,
because a local Expo module and any config plugin cannot run in Expo Go at all —
and the README says where it comes from and that the tree cannot cite it. It is
not silently assumed.

---

### F21 — This is the app's first store-facing declaration. **Exists.**

**Evidence.** `mobile/app.json` declares no permission in either platform block.
`docs/mobile/design/push-notifications.md`'s privacy-statement work is still
open. `docs/mobile/play-review-access.md` is the only Play form text the repo
has. Check 89 is the owner's signed-build check.

**Consequence.** Three owner-only store steps arrive with this work, and all three
are new: the Play **foreground-service-type declaration** (§6), the App Store
**bundle identifier for the widget extension** plus the **App Group** (§6), and
an **updated Data safety / App privacy** answer for the microphone. None of them
is a code change and none can be done by an agent (Global Constraint 9, D120).

---

### F22 — The mobile README and the plan disagree about what is merged. **Open** (documentation hygiene, not a design constraint).

**Evidence.** `mobile/README.md`'s Status says P3.1–P3.6 are in the tree, and
`git log -- mobile/src/voice/` shows `12fbe60e` (P3.5, #677).
`docs/mobile/implementation-plan.md`'s Phase 3 tables still read as unbuilt.

**Consequence.** Nothing for the design; the reconciliation happens to have read
the tree at a commit newer than the plan's prose. The plan below cites the
**tree**, and the README says which commit it read.

---

### F23 — The app constructs no audio at all today. **Exists.**

**Evidence.** `mobile/index.js` builds `AccountStore`, `createDemoMode()`, the
first screen and the demo screen, and imports only `expo`,
`@react-native-async-storage/async-storage`, `expo-secure-store`, `react` and
`react-native`. `mobile/src/boundary.test.js` and `mobile/src/dependencies.test.js`
fence that file.

**Consequence.** `mobile/index.js` is the app's one wiring file and is the place
the session module is constructed — which means it is the file where a wrong port
swap would be worst (its own header says so) and where the manifest scan looks
for new imports. The plan wires the session there in **its own part**, not
scattered across the native parts.

---

### F24 — The secure store is unreadable while the phone is locked. **Rule** (and a genuinely non-obvious one).

**Evidence.** `mobile/src/accounts/expo-secure-store.js` writes with
`keychainAccessible: WHEN_UNLOCKED_THIS_DEVICE_ONLY`, and its own comments say why
(`THIS_DEVICE_ONLY` keeps a credential out of an iCloud Keychain backup;
`WHEN_UNLOCKED` rather than `AFTER_FIRST_UNLOCK` means the item is *"not readable
while the phone is locked"*). `mobile/README.md` records the constant as **not
exercised** here. `mobile/src/accounts/secret.js`'s two items are `idToken` and
`macKey`.

**Consequence.** A session that is paused on a call, with the screen locked and
the app in the background, **cannot read the pairing key**. Anything the session
does while paused — sign a message, ack a mailbox item, refresh a token — will
fail closed, and it will look like a bug rather than the security property it is.
So the module's paused state must do nothing that needs a credential, and the
plan's tests must pin that (a paused session issues **no** mailbox call). This is
the sharpest thing in this reconciliation, and it is not in the spec at all.

---

### F25 — The App Group is a new shared container, and it must carry no secret. **Gap.**

**Evidence.** Nothing in the app shares a container today; the two credential
items live in the Keychain (`mobile/src/accounts/secure-store.js`,
`expo-secure-store.js`), and `mobile/src/boundary.test.js` holds that no other
file calls the platform's `setItem`. `@bacons/apple-targets` mirrors an App Group
entitlement into the targets (F15).

**Consequence.** The App Group is where the widget reads the session's state, and
it is a **less protected** store than the Keychain. The rule the plan writes:
exactly four scalars cross it — the state name, `muted`, `startedAt` and the
title — and **no** credential, no token, no transcript and no message. The
widget's own source lives in `mobile/targets/…`, which is not under `src/`, so
the existing scans do not reach it: the plan gives it its own scan.

---

### F26 — Android's own boot/background rules bound the whole design. **Rule.**

**Evidence.** Google's *Foreground service types* reference: for the
`microphone` type, *"The `RECORD_AUDIO` runtime permission is subject to
while-in-use restrictions. For this reason, you cannot create a microphone
foreground service while your app is in the background"*, and *"If your app
targets Android 14 or higher, the operating system checks when you create a
foreground service to make sure your app has all the appropriate permissions for
that service type … If you don't have that permission, the system throws a
`SecurityException`."* Google's *Restrictions on starting foreground services*:
*"if an app wants to launch a foreground service that needs while-in-use
permissions (for example, body sensor, camera, microphone, or location
permissions), it cannot create the service while the app is in the background,
even if the app falls into one of the exemptions from background start
restrictions."* Play's *Understanding foreground service and full-screen intent
requirements*: the declared use case for `TYPE_MICROPHONE` is *"Capture audio
input, for example, voice commands for virtual assistant without saving"*, and
the evidence Play asks for is *"Show the user initiate the microphone actively
recording audio or receiving voice commands."*

**Consequence.** Three consequences, and the third is the one worth a decision:

1. The service **must** be started from a foregrounded app, which means the
   session's `start()` is a foreground-only operation and the plan cannot offer a
   "start a session from the notification" path.
2. A `SecurityException` is the failure mode when the permission has been revoked
   between the ask and the start — so the session must **re-check
   `RECORD_AUDIO` immediately before `startForeground`**, and a refusal is D122's
   `microphone-denied`, not a crash.
3. Play's evidence line names a **user-initiated** recording. An **automatic
   resume** after a call is the same session resumed, but a reviewer reading
   §8's timeout rule next to this line can reasonably ask. Decision **D220**.

---

### F27 — Android's `POST_NOTIFICATIONS` does not gate the service, only the notification. **Wrong** (the spec's parenthetical).

**Evidence.** Google's notification-permission page: *"Apps don't need to request
the `POST_NOTIFICATIONS` permission in order to launch a foreground service.
However, apps must include a notification when they start a foreground service,
just as they do on previous versions of Android."* The permission is Android 13
(API 33)+ and runtime.

**Consequence.** The spec's §7.6 gloss — *"otherwise the FGS notification is
hidden"* — is right about the **notification** and wrong if it is read as gating
the **service**. The design must therefore decide what the session does when
notifications are denied: the service runs, the controls are unreachable from the
shade, and the only remaining control is the app itself. That is exactly one of
the spec's own test-matrix rows (its §11 item 9), and the answer must be "the
session may still run, and the app says the controls are not reachable" rather
than "start anyway and say nothing".

---

### F28 — D121's "no push in v1" plus this spec's Live Activity is a new iOS surface. **Open.**

**Evidence.** `docs/mobile/reconciliation.md` D121 (no push in v1);
`docs/mobile/design/push-notifications.md` (design only, Q1–Q10 open);
[spec.md](spec.md) §2's non-goal (*"Remote push updates to the Live Activity. All
updates are local"*).

**Consequence.** The Live Activity is **not** push: Apple exposes ActivityKit
push for remote updates, and this spec explicitly refuses it, so no APNs key, no
`pushType: .token`, no `config/admin/push/$uid` node and no sender is involved.
That keeps P5.2's open questions untouched — and it is worth saying in writing
precisely because a reader who knows ActivityKit will assume the opposite.

---

### F29 — Notification *actions* on Android are the service's, not the app's. **Rule.**

**Evidence.** [spec.md](spec.md) §7.2 (*"the notification is owned by the native
service to keep lifecycle in one place"*; `PendingIntent` with
`FLAG_IMMUTABLE`). `mobile/src/voice/screen.js` is the JS screen, and
`mobile/src/mailbox/` is the only thing in the app that writes to the database.

**Consequence.** A notification action runs in the **service's** process even
when no JS is alive, so the button's handler must be native, must re-post the
notification itself, and must emit `remoteAction` for JS to pick up **if** JS is
alive. The consequence for the design is that mute and hang-up must be
idempotent and must be latched natively: a user who hangs up from the shade
while the JS thread is dead still gets a torn-down session. See the plan's
recovery part.

---

### F30 — The spec's own test matrix needs two devices and one of them may not exist. **Open.**

**Evidence.** [spec.md](spec.md) §11 (*"Manual test matrix on physical devices
(simulators/emulators don't model calls reliably)"*) and items 9–10 (an Android
13+ device with notifications denied, an Android 14+ device, and an **iOS 16
device**). The brief records that the owner has an **iPhone** for TestFlight and
may not have an Android device.

**Consequence.** §6 below says, per part, what a device must show and what an
emulator cannot. The plan's parts are ordered so that the iOS half is useful on
its own — because it is the half the owner can check unaided.

---

## 4. The platform facts, checked one by one

Every row was read on **2026-10-20** from Apple's or Google's own documentation,
by URL. Apple's pages are read in their published Markdown form. **Verified**
means the sentence below is the document's own claim; **Wrong** means the spec
says something the document contradicts; **Unverified** means it could not be
established from a first-party page and is **not** asserted anywhere in the plan.

| # | The spec's claim | Verdict | What the document says, and where |
|---|---|---|---|
| P1 | A Live Activity is available from **iOS 16.1** | **Verified** | ActivityKit's framework availability is **iOS 16.1.0 –**, and `NSSupportsLiveActivities` is **iOS 16.1.0 –**. `developer.apple.com/documentation/activitykit.md`; `…/bundleresources/information-property-list/nssupportsliveactivities.md` |
| P2 | Interactive buttons need **iOS 17+** (`LiveActivityIntent`) | **Verified** | `LiveActivityIntent` availability is **iOS 17.0 –**. `developer.apple.com/documentation/appintents/liveactivityintent.md` |
| P3 | `LiveActivityIntent` **runs in the app's process** | **Verified, with the document's own nuance** | *"In general, your app needs to be in the foreground to start a Live Activity. However, you can use a `LiveActivityIntent` and start the Live Activity in its `perform()` method. When the system performs the intent, **the system launches your app process without opening the app**, performs the intent, and starts the Live Activity."* — the sentence is about **starting** an activity from an intent; that it runs in the app process and not the widget's is the documented behaviour. `developer.apple.com/documentation/appintents/liveactivityintent.md` |
| P4 | `Text(timerInterval:)` renders elapsed time with **no per-second updates** | **Verified** | `Text.init(timerInterval:pauseTime:countsDown:showsHours:)` — *"Creates an instance that displays a timer counting within the provided interval"*; availability **iOS 16.0 –**, i.e. older than ActivityKit itself. `developer.apple.com/documentation/swiftui/text/init(timerinterval:pausetime:countsdown:showshours:).md` |
| P5 | A **`staleDate`** means *"a killed app doesn't leave a 'live' activity forever"* (§9) | **Wrong** | `ActivityContent.staleDate`: *"When time reaches the configured stale date, the system considers the Live Activity out of date, and the `ActivityState` of the Live Activity changes to `ActivityState.stale`."* — **it marks; it does not end or remove.** The bound that does remove it is the system's own: *"A Live Activity can be active for up to eight hours unless its app or a person ends it … After the eight-hour limit, the system automatically ends the Live Activity, and immediately removes it from the Dynamic Island. However, the Live Activity remains on the Lock Screen until a person removes it or for up to four additional hours … a maximum of 12 hours."* So the spec's §9 sentence is **half true**: `staleDate` is a *signal to the reader* that the data is old, and the **12-hour** system bound is what actually removes it. `…/activitykit/activitycontent/staledate.md`; `…/activitykit/displaying-live-data-with-live-activities.md` |
| P6 | `UIBackgroundModes: audio` is needed or iOS suspends the mic | **Verified** | `AVAudioSession.Category.playAndRecord`: *"To continue playing audio when your app transitions to the background (for example, when the screen locks), add the `audio` value to the `UIBackgroundModes` key in your information property list file."* `…/avfaudio/avaudiosession/category-swift.struct/playandrecord.md` |
| P7 | `.voiceChat` mode *"enables system echo cancellation and AGC"* (§6.1) | **Wrong as written** | Apple's `voiceChat` page says what the mode does: *"the session optimizes the device's tonal equalization for voice and reduces the set of allowed audio routes to only those suitable for voice chat … Setting this mode also causes the system to automatically apply the `allowBluetoothHFP` category option."* Echo cancellation and gain are **not** promised by the mode; the page points at AudioToolbox's **Voice I/O audio unit** for *"automatic gain correction, adjustment of voice processing, and muting"*. `…/avfaudio/avaudiosession/mode-swift.struct/voicechat.md` |
| P8 | Interruptions carry **`shouldResume`** on `.ended`, and `.began` means the session is deactivated | **Verified, with a caveat the spec misses** | `interruptionNotification`: *"If the interruption type is `began`, the system interrupted your app's audio session and it's no longer active. If … `ended`, this dictionary also contains the `AVAudioSessionInterruptionOptionKey` key."* The caveat: *"Starting in iOS 10, the system deactivates an app's audio session when it suspends the app process. When the app starts running again, it receives an interruption notification … This notification is necessarily delayed in time"*, and the dictionary then carries `AVAudioSessionInterruptionWasSuspendedKey`. **So an interruption notification is not always an interruption** — a suspended-and-resumed app gets one too, and the design must read that key or it will report a call that never happened. `…/avfaudio/avaudiosession/interruptionnotification.md` |
| P9 | `.oldDeviceUnavailable` means headphones unplugged / Bluetooth gone | **Verified** | `AVAudioSession.RouteChangeReason.oldDeviceUnavailable` — *"A value that indicates that the previous audio output path is no longer available."* `routeChangeNotification` is posted on a **secondary thread** and carries `AVAudioSessionRouteChangeReasonKey`. `…/avfaudio/avaudiosession/routechangereason/olddeviceunavailable.md`; `…/routechangenotification.md` |
| P10 | `mediaServicesWereResetNotification` should rebuild the engine | **Verified, and the spec's "rebuild the audio engine" is understated** | *"Respond to these events by reinitializing your app's audio objects (such as players, recorders, converters, or audio queues) and resetting your audio session's category, options, and mode configuration. **Your app shouldn't restart its media playback, recording, or processing until initiated by user action.**"* — i.e. a media-services reset must **end or pause** the session, not silently resume it. `…/avfaudio/avaudiosession/mediaserviceswereresetnotification.md` |
| P11 | Android 14+ blocks starting a **microphone** FGS from the background | **Verified** | *"The `RECORD_AUDIO` runtime permission is subject to while-in-use restrictions. For this reason, you cannot create a microphone foreground service while your app is in the background"*; and *"If your app targets Android 14 or higher, the operating system checks when you create a foreground service to make sure your app has all the appropriate permissions for that service type … If you don't have that permission, the system throws a `SecurityException`."* `developer.android.com/develop/background-work/services/fgs/service-types`; `…/services/fgs/restrictions-bg-start` |
| P12 | `android:foregroundServiceType="microphone"`, `FOREGROUND_SERVICE_MICROPHONE`, `startForeground(…, FOREGROUND_SERVICE_TYPE_MICROPHONE)` | **Verified** | The `microphone` row of Google's *Foreground service types* table gives exactly those three, plus the runtime prerequisite `RECORD_AUDIO`. Android 14 requires the type: *"If your app targets Android 14, it must specify appropriate foreground service types."* Same URLs as P11, and `developer.android.com/about/versions/14/changes/fgs-types-required` |
| P13 | Play requires a foreground-service **declaration** | **Verified** | *"Android 14 and above requires you to do the following before you use a foreground service: Declare foreground service type(s) in your manifest … When your apps target Android 14 and above, you'll need to declare any foreground service types that you use in a new declaration on the **App content** page (Monitor and improve > App content) in Play Console. For each foreground service type you declare, you'll need to … Provide a description of the app functionality that is using each foreground service type. Describe the user impact if: the task is deferred …"* — and for `TYPE_MICROPHONE` the use case is *"Background Audio Access — Capture audio input, for example, voice commands for virtual assistant without saving"*, with the evidence *"Show the user initiate the microphone actively recording audio or receiving voice commands."* `support.google.com/googleplay/android-developer/answer/13392821` |
| P14 | `POST_NOTIFICATIONS` *(Android 13+; otherwise the FGS notification is hidden)* | **Verified, and the parenthetical is Wrong as a gating claim** | *"Apps don't need to request the `POST_NOTIFICATIONS` permission in order to launch a foreground service. However, apps must include a notification when they start a foreground service."* The permission is API 33+. `developer.android.com/develop/ui/views/notifications/notification-permission` |
| P15 | `TelephonyCallback.CallStateListener` on API 31+ | **Verified** | *"Added in API level 31 … Interface for call state listener"*, registered with `TelephonyManager.registerTelephonyCallback(Executor, TelephonyCallback)`. `developer.android.com/reference/android/telephony/TelephonyCallback.CallStateListener` |
| P16 | The call-state listener **requires `READ_PHONE_STATE`** | **Unverified** | The API-reference page's `onCallStateChanged` did not render its "Requires permission" line to a non-JavaScript reader, so this is **not** asserted in the plan. What *is* established: this app declares **no** Android permission today, and `READ_PHONE_STATE` would be its fourth runtime ask. See **D209** and the UNVERIFIED table. |
| P17 | `READ_PHONE_STATE` has a Play **declaration** requirement | **Unverified** | Play's *Permissions and APIs that Access Sensitive Information* page describes **Restricted Permissions** and its declaration form for **SMS and Call Log** — `READ_PHONE_STATE` is not among the ones the page names. Whether Play's Data safety form treats it specially is **not** established here. See the UNVERIFIED table. |
| P18 | `ACTION_AUDIO_BECOMING_NOISY` is the "route was lost" signal | **Verified** | *"Broadcast intent, a hint for applications that audio is about to become 'noisy' due to a change in audio outputs. For example, this intent may be sent when a wired headset is unplugged, or when an A2DP audio sink is disconnected, and the audio system is about to automatically switch audio route to the speaker. Applications that are controlling audio streams may consider pausing, reducing volume or some other action on receipt of this intent so as not to surprise the user with audio from the speaker."* Added in API level 3. `developer.android.com/reference/android/media/AudioManager` |
| P19 | The audio-focus constants mean what §7.3 says | **Verified** | `AUDIOFOCUS_LOSS_TRANSIENT` — *"a transient loss of audio focus"*; `AUDIOFOCUS_GAIN` — *"a gain of audio focus, or a request of audio focus, of unknown duration"*; `AUDIOFOCUS_LOSS` — *"a loss of audio focus of unknown duration"*. `developer.android.com/reference/android/media/AudioManager` |
| P20 | *"Use `AudioManager.MODE_IN_COMMUNICATION` while active **so the platform applies echo cancellation**"* (§7.3) | **Wrong as written** (the mode is real and the spec's *use* of it is fine; the echo-cancellation attribution is not supported by the reference — see **U3**) | The reference documents `MODE_IN_COMMUNICATION` as *"In communication audio mode"* and nothing more. Echo cancellation on Android is a **separate effect**, `AcousticEchoCanceler`, which *"an application creates … to instantiate and control an AEC engine in the audio capture path"* and attaches to a particular `AudioRecord`'s session id — and *"On some devices, an AEC can be inserted by default in the capture path by the platform according to the `MediaRecorder.AudioSource` used."* So it is **device-dependent**, and the plan may not assume it. `developer.android.com/reference/android/media/audiofx/AcousticEchoCanceler`; `…/NoiseSuppressor` |
| P21 | `PendingIntent` with `FLAG_IMMUTABLE` | **Verified as an API** | `FLAG_IMMUTABLE` — *"Flag indicating that the created PendingIntent should be immutable"*; it is part of the instance's identity, so the same flags must be supplied to retrieve it. `developer.android.com/reference/android/app/PendingIntent` |
| P22 | `START_NOT_STICKY` keeps the system from restarting a mic service | **Verified** | `Service.onStartCommand` — *"`START_STICKY` is used for services that are explicitly started and stopped as needed, while `START_NOT_STICKY` or `START_REDELIVER_INTENT` are used for services that should only remain running while processing any commands sent to them."* (This is the **general** contract, not a microphone-specific rule: the plan states it as the general contract.) `developer.android.com/reference/android/app/Service` |
| P23 | `expo-apple-targets` | **Wrong** | No such npm package (404). See **F14** for the real one, `@bacons/apple-targets`. |
| P24 | App extensions under EAS + CNG | **Verified, and labelled experimental by Expo** | See **F15**. `docs.expo.dev/build-reference/app-extensions/` |
| P25 | App Groups is a capability EAS can enable | **Verified** | Expo's *iOS capabilities* table lists **App Groups — `com.apple.security.application-groups`** as supported, and the page says EAS synchronizes capabilities on the Apple Developer Console from `ios.entitlements` on `eas build`. `docs.expo.dev/build-reference/ios-capabilities/` |

**Five of the spec's platform claims do not survive the check.** Three are
**Wrong** — P5 (`staleDate` presented as something that ends a Live Activity: it
marks one stale; Apple's own 8-hour and 12-hour bounds are what remove it), P7
(`.voiceChat` presented as enabling echo cancellation and AGC: the mode sets
voice-optimised equalisation and a reduced route set, and promises neither) and
P23 (`expo-apple-targets` is not a package) — and two more are **wrong as
written in a way that changes a design** — P14 (the `POST_NOTIFICATIONS`
parenthetical reads as gating the foreground service; it gates the notification,
not the service) and P20 (`MODE_IN_COMMUNICATION` is presented as what makes the
platform apply echo cancellation; the reference does not say that, and AEC is the
separate, device-dependent `AcousticEchoCanceler`). P16 and P17 are **Unverified**
and are therefore asserted nowhere in the plan (§4's own rule), and the remaining
**eighteen** rows hold.

Of the five, **two would have been built exactly as the spec writes them** — P5's
`staleDate` and P7's `.voiceChat` — because both read as a platform guarantee
until the platform's own page is opened; the other three (P14's parenthetical,
P20's attribution and P23's package name) would have cost a rework rather than a
wrong feature.

---

## 5. Decisions for the owner — **D201–D220, none answered**

Each row: the question, a **recommended** option with its reason, and the
alternative with its **cost**. **No row is filled in.**

**The spec's own §12 asks four questions, and all four are carried here in
order: D201 (item 1, the timeout), D202 (item 2, the "I'm back" cue), D203
(item 3, whether Hang Up opens the app) and D204 (item 4, the minimum iOS
version).** The other sixteen rows — D205–D220 — are what the reconciliation
exposed: the composition with P3.4's machine, where the module's code and the
widget's target live, what a device check may assume while the app is voiceless,
the two Android permission questions (`READ_PHONE_STATE` and notifications
denied), the "Tap to resume" entry point the spec's §8 mentions *optionally*
(D211 — **not** one of the §12 questions), demo mode, telemetry, the paused
activity's content, the throttle, the notification category, this work's check
numbers and the resume rule.

| # | The question | Recommended, and why | The alternative, and its cost |
|---|---|---|---|
| **D201** | **`resumeTimeoutMs` default: 2 or 3 minutes** (spec §12 item 1; the spec's own §8 says 180,000 ms). | **2 minutes (120,000 ms).** The cost of being wrong is a tap either way, but the two failure modes are not symmetric: a *longer* timeout keeps a paused session — and its Live Activity and its foreground-service notification — alive through a conversation the session has nothing to do with, which is precisely the state this module exists to end cleanly, and it is the state Play's `TYPE_MICROPHONE` evidence asks about (P13). Two minutes also exercises the **timeout path** — the one that ends a session — far more often in the owner's own use, so the less-travelled branch of the machine is the one the owner actually meets. | **3 minutes, as the spec writes it** (`resumeTimeoutMs` default 180,000). Cost: the indicator and the notification outlive the call by up to another minute, and the timeout path stays the rare branch — so the rule with the most ways to be wrong is the one nothing exercises. **Or make it an owner setting** (the phone's, like D127's switch): cost: a third device-local setting, a screen to hold it, and no default to test. |
| **D202** | **The "I'm back" cue: earcon, short spoken phrase, or both** (spec §12 item 2). | **A short earcon, and nothing spoken.** It is local, instantaneous, and needs no engine — which matters enormously while the app is **voiceless** (F7): a spoken cue is a TTS utterance that cannot be produced at all until Check 85 lands, so recommending the phrase would tie a part of this work to an unrun check. An earcon also cannot be mistaken for the assistant answering a question. | **A short spoken phrase** ("I'm listening"). Cost: it is an utterance that can fail (`TTS_CODES`), it needs an engine, and it is the app **talking at a moment the user did not ask it to** — which the spec's own §1 forbids in as many words. **Or both**: cost: the two failure modes above, plus a settings question nobody asked. |
| **D203** | **Does Hang Up from outside the app open the app to a summary, or stay silent?** (spec §12 item 3). | **Stay silent.** The spec's own §10 already says *"Full teardown, no app launch required"*, and §1 promises the app will *"never … start talking or listening at a surprising moment"* — yanking the user into an app they did not open is the same surprise in a different medium. The intent runs in the app's process on iOS 17 anyway (P3), so nothing is gained by opening it. | **Open to a summary.** Cost: a background hang-up pulls the user out of whatever they were doing; and on Android, where the same action comes from the shade, it would open an activity from a service — which is the background-activity-launch restriction this work otherwise never has to touch. **Or open only when the app is already in the foreground**: cost: a third behaviour to test for one outcome. |
| **D204** | **Minimum iOS version: 16.1 or 17** (spec §12 item 4). | **iOS 17.** It is **one** Live Activity, one set of buttons and one device check. Apple's own availability is the argument: `LiveActivityIntent` — the thing that makes the buttons work at all — is **iOS 17.0+** (P2), so 16.1 buys a second presentation (`widgetURL` deep-link only), a second state (a tap that opens the app instead of performing an intent) and a second check row, to cover a device generation that cannot run the feature's point. The app is unreleased; its first store build is still ahead of it. | **16.1** (the spec's floor, P1). Cost: two UIs in the widget target, two behaviours for "the user pressed Hang Up", an extra check row for a device only the owner may have, and the permanent risk that the 16.x path rots because nobody runs it. **Or 16.1 with the buttons simply absent**: cost: the spec's §2 Goal 2 — controls outside the app — is not met on those devices, which is worse than not shipping to them. |
| **D205** | **Where the state machine lives: JavaScript, or native.** | **JavaScript — one machine, in `mobile/src/voice-session/`**, with the native module as a port that emits the spec's §5 events and executes `start`/`updateStatus`/`setMuted`/`end`. Every reason the tree already has points here: the spec's own §11 asks for the unit tests this makes possible, Global Constraint 10 forbids CI needing a device, and `src/audio/`'s fakes are the precedent. One machine also means one place where "why did this session end" is answered. | **A native machine on each platform** (which is what survives a JS teardown). Cost: **three** machines rather than one — Kotlin, Swift and JS — with three sets of transitions to keep in step and **nothing testable in CI at all**; and the drift between them would show up only as the app behaving differently on the two phones. **Or native state with the JS machine as a mirror**: cost: two sources of truth for the same question, which is the failure the tree's `sender`/`newId`/`now` injection exists to avoid. |
| **D206** | **How the new machine composes with P3.4's pipeline** (F3). | **By subject, and derived, never duplicated:** `voice-session` owns *is there a session, is it paused, why did it end*; the pipeline keeps *whose turn it is and whether an utterance is in flight*; the session's `listening`/`speaking` are **read** from `pipeline.turn()` / `pipeline.speaking()` on every render. This is what keeps `interruptDecision`'s two arguments current, and it is why no second `speaking` flag is stored. | **A stored `state` field that the pipeline updates.** Cost: `interruptDecision` can then judge a flag that is one event stale, and the failure it produces — an update spoken over the user — is the failure P3.5 was built to prevent. **Or flatten the two machines into one**: cost: P3.5's tests, P6.12's seam and `src/audio/`'s door all move, for no gain in behaviour. |
| **D207** | **What background audio means for D122's conditional** (F2: D122 says the mode is declared *"only if barge-in needs it"*). | **Extend D122, and say so in one place.** The background audio mode is declared, and the reason is now the session's whole existence and not only barge-in. Every other half of D122 is unchanged: `NSMicrophoneUsageDescription` is the owner's sentence, the two denial codes stay, and the visible "tap to speak" fallback stays. | **Leave D122 as written and declare the mode as "barge-in"**. Cost: the declaration would be described by a rule narrower than the reason, which is how a permission string and its purpose drift apart — and a store reviewer reading `UIBackgroundModes: audio` against a spec that says "only if barge-in" has a fair question to ask. |
| **D208** | **What a device check may assume about speech, while the app is voiceless** (F7). | **Every check is written to run with the session up and the pipeline silent.** Start, background, interrupt, route change, mute, hang up, force-kill, recover — all of it is observable without one word being said, and the pipeline's `say`/`turn` can be driven by the fakes. The one thing that must wait for **Check 85** is the "I'm back" cue and anything that depends on real speech. | **Hold the whole work until Check 85 is run.** Cost: the lifecycle half — which is what this spec is entirely about — waits on an unrelated measurement, and the owner's iPhone can prove nothing in the meantime. **Or ship the checks with a synthetic audio path only**: cost: the same checks then never meet a real microphone, and the thing most likely to be wrong (what iOS and Android actually do to a microphone on a call) is the thing not tested. |
| **D209** | **Does the app request `READ_PHONE_STATE` for the Android call-state listener** (spec §7.4; F13 and F21 — the new asks are store declarations — and the platform facts P16/P17)? | **No — audio focus only, in the first cut.** It would be the app's fourth runtime ask for a signal that audio focus already carries in the ordinary case, and the spec's own §7.4 already names the fallback as acceptable (*"If the user denies it, fall back to audio focus only"*). Fewer permissions is the posture D122 was answered with, and a permission that is only sometimes granted is a second behaviour that must be tested on a device the owner may not have (F30). | **Yes, request it.** Cost: a fourth runtime permission whose Play treatment this reconciliation could **not** establish (P16, P17), a Data safety answer to change, a denial path to test, and a privacy-form sentence to write — for a case (`some calls don't produce clean focus changes`) the spec itself describes as exceptional. |
| **D210** | **What the app does when notifications are denied on Android 13+** (F27). | **Run the session, and say on the app's own screen that the controls are not reachable from the shade** — and keep the Live-Activity-equivalent promise honest by never claiming a control that is not there. The service is not gated by the permission (P14), so this is a *disclosure* problem and not a functional one. | **Refuse to start a session without notifications.** Cost: a user who has muted notifications for everything loses the feature entirely, which is a larger denial than the platform itself makes. **Or start and say nothing**: cost: the owner's own test matrix (§11 item 9) names this case, and silence is the answer that makes the check unwritable. |
| **D211** | **Is the "Tap to resume" entry point in the first cut, and is it a local notification or an in-app banner** (spec §8, F12)? | **An in-app banner only, in the first cut**, with the local notification a later part of its own. A banner cannot be misread on a lock screen, needs no permission, and is checkable on the owner's own phone today; and the spec's own §8 already marks the notification *"optionally"*. | **Both now.** Cost: a notification channel, a `POST_NOTIFICATIONS` ask and a lock-screen surface that must be kept content-free (F12) — three things to get right for an entry point that only appears after a three-minute call. **Or neither**: cost: §2's Goal 4 (*"a one-tap way back in"*) is unmet, and the session simply disappears. |
| **D212** | **Does a voice session run in demo mode** (F10)? | **No.** The demo is a Play-review path that is text-only by construction, its three fences are pinned by `safety.test.js`, and its entry is the first screen a reviewer taps. A microphone that opens there is the worst possible place for one, and widening the demo's own scan to allow it would weaken a fence that exists for a reason. | **A scripted demo session over the fakes.** Cost: the demo would have to own native audio on a store-review device (or visibly fake the microphone, which is worse), and its "no credential, no network" contract would have to be re-argued in public. **Or a video/GIF of the feature in the store listing**: cost: a store asset that can go stale, and nothing the reviewer can operate. |
| **D213** | **What a session failure reports, and to where** (F11 — there is no crash reporter in the app). | **The app's own screen, in the shape P3.5 already uses** (a refusal code and the field it names, rendered by the screen that made the call), **plus lifecycle events — state names, reason codes and durations — and never audio, never a transcript, never a key.** The tree's posture (no telemetry, no analytics) holds, the module stays pure and CI-able, and the owner can see every failure by looking at the phone. | **Add Crashlytics / Analytics now.** Cost: a new SDK in `mobile/package.json` (which the dependency scan then requires), a manifest and config change, a Data safety and App privacy change, and an **egress decision this tree has never taken** — for a class of event (a session ended) the screen already shows. **Or log locally to a ring buffer the app can show**: cost: a file on the device holding session history, which is a small transcript by another name unless the fields are strictly limited to codes and durations. |
| **D214** | **What "the session is still alive" means when JS is dead** (spec §9, F29). | **A native watchdog with a bound**: the service and the Live Activity hold the last state they were given, and the Live Activity's `staleDate` is refreshed on every `updateStatus`, so an app that stops updating shows a **stale** activity rather than a live one (P5) — and the check reads the state, not the uptime. | **Trust the process.** Cost: a killed app leaves an activity that *looks* live until Apple's own 8-hour bound removes it (P5) — the exact failure the spec's §9 says `staleDate` prevents, and which `staleDate` does not prevent. **Or end the activity on a timer**: cost: a session the user is happily using ends because a timer ran out. |
| **D215** | **What the always-visible control is, and what happens when there is none.** | **Three surfaces, one rule: the in-app indicator, the Live Activity, and the Android notification — and the microphone is never open without at least one of them visible.** When the OS refuses (D122's two codes, F13) what runs is the visible "tap to speak" fallback and **not** a silent session. | **The indicator alone.** Cost: the whole point of the spec is that the controls exist outside the app; and a backgrounded session with no indicator is a session the user cannot see or stop. **Or allow a session while the indicator is denied**: cost: a microphone running with nothing on screen saying so, which is the one thing this module must never do. |
| **D216** | **What the Live Activity and the notification show while the session is paused.** | **The status word changes ("Paused — on a call", "Paused — headphones") and the timer keeps running**, because it is elapsed *session* time and a frozen timer reads as a hung app. The pause reason is the useful information, and it is one of the spec's own states. | **Freeze the timer.** Cost: the user cannot tell a paused session from a broken one, and the check cannot tell either. **Or hide the activity while paused**: cost: a session that vanishes from the lock screen at the moment the user is most likely to want to end it. |
| **D217** | **How often `updateStatus` is called.** | **On a state change or a mute change, plus one `staleDate` refresh when the session changes state.** The spec's §5 already says to throttle; the tree's reason for doing so is that `updateStatus` is the only thing that writes to the Activity and re-posts the Android notification, and a per-frame writer is a battery cost with no reader. | **On a timer.** Cost: wake-ups and notification re-posts for a screen whose timer is already rendered by the platform (`Text(timerInterval:)`, P4) — i.e. work whose entire output the system produces for free. |
| **D218** | **The Android notification's category** (spec §7.2 offers `CATEGORY_CALL` **or** `CATEGORY_SERVICE`). | **`CATEGORY_SERVICE`.** The spec's own non-goals say the session is *"not presented as a phone call"* and there is no CallKit/ConnectionService integration; `CATEGORY_CALL` sorts the notification with calls and implies a call is in progress. | **`CATEGORY_CALL`.** Cost: the shade ranks an assistant session beside a real call, which is a small lie the app tells every time; and the spec would be arguing against its own non-goals. |
| **D219** | **Which check numbers this work takes** (F18). | **113–118**, the first free numbers after reading the register name by name (written sections to 89; 90–99 the credentials service; 100–104 proposed and 105–109 reserved for the functional spec; **110 named and unwritten** by the P5.3 design; **111 taken** by the setup-wizard work; **112 proposed** by `docs/windows-cli/`, which is on `main` (PR #757) while this branch is not, so the register's two-claims rule renumbers this branch — F18). Six checks, and **none of them is written into the register** by this plan. | **Extend check 86** (which already owns hands-free audio, F17). Cost: 86 is allocated to P3.5's subject and to P4.2, and its text is not yet written — folding a Live Activity, a foreground service and a crash-recovery case into it would make one check carry two subjects and two owners. **Or take 118+ and leave a gap**: cost: nothing, but there is no reason to. |
| **D220** | **May the session resume by itself after an interruption** (F26 point 3, against Play's evidence line for `TYPE_MICROPHONE`). | **Yes, within the timeout, exactly as the spec's §4 says** — the session was started by the user, the resume is the *same* session, and the user is looking at a notification that says it is paused (D216). The design's protection is not a second confirmation; it is that the paused state is **visible** and that the resume never happens after the timeout (spec §8's own rule). | **Always require a tap to resume.** Cost: the spec's primary promise — hands-free — is gone for the duration of every call and every alarm, and §2's Goal 3 is unmet. **Or resume only when the app is in the foreground**: cost: the headset case (paused on a Bluetooth drop) then behaves differently from the call case, for no reason the user can see. |

**Nothing above is answered.** Every row is a question with a recommendation; the
recommendation is not a decision, and answering a decision is not approving a
task — the plan says so in its own gate.

---

## 6. What each part costs the owner, and what a device must show

The owner has an **iPhone** for TestFlight and **may not have an Android device**
(F30). Every item below is a step only the owner can take (Global Constraint 9,
D120: no agent runs a store step or touches a signing key).

### 6.1 Apple — the portal steps, exactly

These are the only Apple-side steps this work needs, and they arrive with the
**widget part** (the plan's VS4):

1. **Register a new App ID for the widget extension.** In *Certificates,
   Identifiers & Profiles → Identifiers*, add an **App ID** with an **explicit
   bundle identifier** for the extension — conventionally the app's own
   identifier plus a suffix (the plan derives it from `FUGARO_IOS_BUNDLE_ID`, so
   the owner registers the identifier that derivation produces, not a fourth
   value to remember).
2. **Create the App Group.** In the same section, add an **App Group**
   identifier (conventionally `group.<the app's bundle identifier>`).
3. **Enable the App Groups capability on both App IDs** — the **app's** and the
   **extension's** — and tick the group created in step 2 for both.
4. **Nothing else by hand.** EAS Build declares the extension, generates its
   credentials and synchronizes the capability from `ios.entitlements`
   (`extra.eas.build.experimental.ios.appExtensions` + `ios.entitlements`; F15),
   so **no provisioning profile has to be created, downloaded or edited by
   hand**, and **no Team ID is supplied by hand either**: EAS already holds the
   owner's team identifier with the Apple credentials it signs the app with today
   (F6) — that is the whole reason this shape was chosen. If EAS's sync is
   unavailable, the fallback is Apple's own *Profiles → +* for the two
   identifiers, which is the step the plan's check would then record.

**No secret is involved in any of these steps**, and none is pasted anywhere: an
App ID and an App Group identifier are public names that reach the built app.

### 6.2 Google Play — the portal steps, exactly

Arriving with the **Android part** (VS3):

1. **Declare the foreground-service type** on *Monitor and improve → App content*
   (the FGS declaration form, P13): the type is **microphone**, the description is
   what the session does, and the "user impact if the task is deferred" sentence
   is the owner's. Play's own evidence line for this type — *"Show the user
   initiate the microphone actively recording audio or receiving voice
   commands"* — is why the session is always user-started (F26) and why the
   paused state is always visible (D216, D220).
2. **Update Data safety** for the microphone and for the new permission(s).
3. **Nothing else by hand**: the manifest entries (`RECORD_AUDIO`,
   `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_MICROPHONE`, `POST_NOTIFICATIONS`,
   and `READ_PHONE_STATE` only if D209 says yes) come from `app.config.js`.

### 6.3 What a device must show, and what an emulator cannot

| The behaviour | iPhone | Android device | Android emulator |
|---|---|---|---|
| The mic ask, the background mode, the indicator | **Required** | **Required** | Partly: the permission dialog can be answered, but the *background* behaviour is not the same |
| An incoming call interrupting the session | **Required** | **Required** | **No.** `gsm call` in the emulator console can inject a call, but it does not model the audio-focus and while-in-use interactions the design depends on |
| Answering and hanging up, and the resume | **Required** | **Required** | **No** |
| Unplugging a headset / dropping Bluetooth **mid-response** | **Required** | **Required** | **No.** `ACTION_AUDIO_BECOMING_NOISY` is documented as a *hint* (P18), and no emulator produces a real route change |
| The Live Activity's buttons (Dynamic Island, lock screen) | **Required** (iOS 17+, D204) | — | — |
| The Android notification's actions | — | **Required** | **Partly.** The notification and its `PendingIntent`s can be exercised; `setOngoing` behaviour under Doze cannot |
| Backgrounding for 10+ minutes | **Required** | **Required** | **Partly** |
| Force-kill mid-session, then relaunch | **Required** | **Required** | **Yes** (`adb shell am force-stop`), which is why the recovery check is the one Android check that can be rehearsed without a phone |
| Android 13+ with notifications denied; Android 14+ start attempt | — | **Required** | **Partly** |

**The honest summary:** the iOS half is checkable by the owner unaided, and the
plan is ordered so that the iOS half is useful on its own (F30). The Android
half's checks are written but their **call** and **route** rows need an Android
device; an emulator can rehearse the permission ask, the notification and the
force-kill, and cannot show the three that matter most.

---

## 7. Open items — for the record, and none of them is a decision

| # | Item |
|---|---|
| **O1** | The app is **voiceless** until Check 85 runs (F7). Nothing in this work changes that, and nothing in this work should be read as scheduling it. |
| **O2** | `expo-apple-targets` is not a package (F14). If the owner has a different plugin in mind — Expo's own future support, or a bare `ios/` target — the plan's widget part names what changes; the plugin choice is not a decision this reconciliation made. |
| **O3** | The spec's §11 test matrix is a **device** matrix, and three of its ten rows (2, 3, 4) need a real call of a known length. They are the owner's time, and the plan's checks are written as steps, not as timings. |
| **O4** | `docs/mobile/implementation-plan.md`'s Phase 3 prose is older than the tree (F22). Nothing here corrects it, and nothing here depends on it. |
| **O5** | "Development builds only, no Expo Go" is ruled in the brief (D200) and is not in this tree (F20). If the owner wants it recorded, it wants its own row in its own document; this work cites the brief and says so. |
| **O6** | The **`READ_PHONE_STATE`** question (P16, P17) is the one place this reconciliation could not establish a platform fact from a first-party page. D209 is written so that its recommended option needs no answer to it. |
| **O7** | `mobile/README.md` records the secure-store accessibility constant as unverified on a device. F24 depends on it being what it says; the plan's first check can confirm it cheaply while it is on a device for something else. |
| **O8** | The Live Activity's **implementation** — compact, minimal and expanded Dynamic Island presentations plus the lock screen view — is UI this reconciliation did not design. The spec's §6.4 names it and the plan leaves it to the widget part. |

---

## 8. UNVERIFIED

Nothing in this table is asserted anywhere above or in the plan.

| # | What is not verified | Why it could not be | Where it would be settled |
|---|---|---|---|
| U1 | That `TelephonyCallback.CallStateListener` requires `READ_PHONE_STATE` | The API reference's "Requires permission" line does not render to a reader without JavaScript, and no other first-party page states it plainly | `developer.android.com/reference/android/telephony/TelephonyCallback.CallStateListener` read in a browser, or a device |
| U2 | Whether Play treats `READ_PHONE_STATE` as needing a declaration or a Data safety answer of its own | Play's *Permissions and APIs that Access Sensitive Information* page describes declaration forms for **SMS and Call Log** and does not name this permission | The Play Console's own *App content* forms, which show what they ask for |
| U3 | That `AudioManager.MODE_IN_COMMUNICATION` makes the platform apply echo cancellation | The reference documents the mode as a name and nothing more (P20); AEC is the separate `AcousticEchoCanceler`, and even that page says the platform inserts one *"on some devices … according to the `MediaRecorder.AudioSource` used"* | A device, by measurement — it is part of Check 86's subject and check 113's |
| U4 | That `@bacons/apple-targets` 5.0.0 works with the Expo SDK in `mobile/package.json` (`^58.0.16`) | The package's peer range is `expo >= 52`, but it pins `@expo/prebuild-config ~55.0.6`, an older line than SDK 58's; nothing in this tree has run it | `npx expo prebuild -p ios --clean` after the widget part, and EAS Build |
| U5 | Whether EAS's capability sync enables **App Groups** on the extension's App ID as well as the app's | Expo's page says App Groups is supported and that EAS syncs capabilities from entitlements; it does not say, in as many words, that it does so for a second target's identifier | The first `eas build --profile internal` after the widget part, and the Apple portal afterwards |
| U6 | That the app's `WHEN_UNLOCKED_THIS_DEVICE_ONLY` Keychain item behaves as documented on a real device (F24) | `mobile/README.md` records the constant as unexercised; no device has run the store | A device, while the phone is locked mid-session |
| U7 | That the iOS "I'm back" earcon can be played with the audio session in the state the resume leaves it | Depends on the resume path this work has not built, and on a real call | A device, in check 113 |
| U8 | The exact behaviour of a **Live Activity started from a `LiveActivityIntent`** when the app process is launched but not opened (P3) — in particular whether a session that was never foregrounded can pass Android's analog on iOS | Apple's page states the process is launched without opening the app; it does not say what the app may then do with the microphone | A device, in check 114 |
| U9 | That the Android emulator's `gsm call` cannot model the call case | Stated here as a reading of the platform's audio-focus behaviour, not measured | The owner's first Android check run |
| U10 | Whether an `internal`-profile EAS build (store distribution) can be installed on the owner's device **with** the extension's new identifiers before the Apple portal steps of §6.1 are done | Depends on Apple's and EAS's behaviour at build time | The first `eas build --profile internal` after the widget part |

---

**Related.** [spec.md](spec.md) (verbatim) · [implementation-plan.md](implementation-plan.md) ·
[README.md](README.md) · [../README.md](../README.md) (the mobile work this
belongs to) · [../design/voice-evaluation-switch.md](../design/voice-evaluation-switch.md)
(P5.3, the local/remote switch) · [../design/push-notifications.md](../design/push-notifications.md)
(P5.2, and why this spec's local notification is not it) ·
[../../voice-module/](../../voice-module/) (the pipeline this module drives) ·
[../../design/voice.md](../../design/voice.md) (D39–D54b) ·
[../../gcp-live-checklist.md](../../gcp-live-checklist.md) (the register, and
checks 113–118) · [../../../mobile/README.md](../../../mobile/README.md) (the app).
