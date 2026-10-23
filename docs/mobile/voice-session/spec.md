# Spec: Hands-Free Voice Session (`voice-session` module)

**Status:** Draft
**Platforms:** iOS and Android, via Expo (development builds only, no Expo Go)
**Owner:** TBD

---

## 1. Summary

The mobile app supports a hands-free voice mode where the user talks to the app while it listens and speaks back. This spec covers what happens when that session leaves the "happy path":

- The app goes to the background mid-session.
- A phone call (or other audio interruption) arrives.
- The audio route changes (headphones unplugged, Bluetooth drops).
- The app crashes or is killed while a session is active.

The user should always see that a session is running, be able to mute or hang up without opening the app, and never have the app start talking or listening at a surprising moment.

## 2. Goals and non-goals

### Goals

1. Keep the microphone and playback alive while the app is backgrounded.
2. Show persistent session controls outside the app:
   - **iOS:** a Live Activity (Dynamic Island and lock screen) with Mute and Hang Up buttons.
   - **Android:** an ongoing foreground-service notification with Mute and Hang Up actions.
3. Pause automatically on calls and other interruptions, and resume automatically when they end, within a time limit.
4. End the session cleanly after long interruptions, with a one-tap way back in.
5. Expose one platform-agnostic JavaScript API, so app code never branches on platform.

### Non-goals

- The speech pipeline itself (STT, LLM, TTS). This module owns the audio session and lifecycle, not the content.
- CallKit / ConnectionService integration. The session is not presented as a phone call.
- Remote push updates to the Live Activity. All updates are local, from the running app.

## 3. Architecture

A single local Expo module, `modules/voice-session`, built with the Expo Modules API.

```
modules/voice-session/
├── src/
│   ├── VoiceSession.ts          # Public JS API + state machine
│   ├── events.ts                # Shared event contract (types)
│   └── index.ts
├── ios/
│   ├── VoiceSessionModule.swift # Audio session, interruptions, routes, Live Activity control
│   └── Shared/                  # Compiled into BOTH app and widget targets
│       ├── VoiceSessionAttributes.swift
│       └── VoiceSessionIntents.swift
├── android/
│   └── src/main/java/.../
│       ├── VoiceSessionModule.kt
│       ├── VoiceSessionService.kt   # Foreground service (microphone type)
│       └── AudioFocusController.kt
└── expo-module.config.json

targets/voice-session-widget/    # iOS widget extension, via expo-apple-targets
├── VoiceSessionLiveActivity.swift
└── expo-target.config.js
```

Layers:

1. **JS session controller** — owns the state machine. The rest of the app only talks to this.
2. **iOS native** — configures `AVAudioSession`, observes interruptions and route changes, starts/updates/ends the Live Activity.
3. **iOS widget target** — Live Activity UI and App Intent buttons.
4. **Android native** — foreground service, notification, audio focus, call-state listener.
5. **Shared event contract** — both platforms emit identical events (section 5).

## 4. State machine

States:

| State | Meaning |
|---|---|
| `idle` | No session. No audio session active, no Live Activity / notification. |
| `starting` | Requesting permissions, configuring audio, starting the foreground service / Live Activity. |
| `listening` | Mic open, waiting for or capturing user speech. |
| `speaking` | App is playing a response. Mic stays open (for barge-in) with echo cancellation. |
| `paused_interruption` | System interruption in progress (call, alarm, Siri, another app took audio focus). Mic closed, playback paused. |
| `paused_route` | Audio route became unsafe (headphones removed, Bluetooth lost). Waits for user action. |
| `ending` | Tearing down. |
| `ended` | Session over. Holds an `endReason` and, where relevant, offers "Tap to resume". |

Mute is a flag (`muted: boolean`) orthogonal to `listening` / `speaking`, not a separate state.

Key transitions:

- `idle → starting → listening` on `start()`.
- `listening ⇄ speaking` driven by the speech pipeline.
- `listening | speaking → paused_interruption` on `interruptionBegan`.
- `paused_interruption → listening` on `interruptionEnded` **with** `shouldResume: true` **and** elapsed time ≤ `resumeTimeoutMs`. Play a short "I'm back" earcon or phrase on resume.
- `paused_interruption → ended` (`endReason: "interruption_timeout"`) when the timeout fires first, or when the interruption ends with `shouldResume: false`.
- `listening | speaking → paused_route` on `routeBecameUnsafe`. Resume only on explicit user action.
- Any active state `→ ending → ended` on `hangUp()` (from app, Live Activity, or notification).

## 5. Shared event contract

Native → JS events, identical on both platforms:

```ts
type VoiceSessionEvent =
  | { type: "interruptionBegan"; source: "call" | "system" | "other_app" }
  | { type: "interruptionEnded"; shouldResume: boolean }
  | { type: "routeChanged"; route: AudioRoute; unsafe: boolean }
  | { type: "remoteAction"; action: "mute" | "unmute" | "hangUp" }   // from Live Activity / notification
  | { type: "serviceStopped"; reason: "system" | "user" | "error" }
  | { type: "error"; code: string; message: string };

type AudioRoute = "speaker" | "receiver" | "wired" | "bluetooth" | "usb" | "unknown";
```

JS → native API:

```ts
interface VoiceSessionNative {
  start(opts: { title: string; resumeTimeoutMs?: number }): Promise<void>;
  updateStatus(s: { state: SessionState; muted: boolean; startedAt: number }): Promise<void>;
  setMuted(muted: boolean): Promise<void>;
  end(reason: EndReason): Promise<void>;
  cleanupStaleSessions(): Promise<void>; // call on every app launch
}
```

`updateStatus` drives the Live Activity content state and the Android notification text. Throttle updates (e.g. only on state/mute change, not per audio frame).

## 6. iOS

### 6.1 Audio session

- Category `.playAndRecord`, mode `.voiceChat` (enables system echo cancellation and AGC).
- Options: `.allowBluetooth`, `.allowBluetoothA2DP`, `.defaultToSpeaker` as appropriate for product.
- Activate on `start()`, deactivate with `.notifyOthersOnDeactivation` on end.

### 6.2 Interruptions

- Observe `AVAudioSession.interruptionNotification`.
  - `.began` → emit `interruptionBegan`. Stop capture and pause playback.
  - `.ended` → read `AVAudioSession.InterruptionOptions.shouldResume`; emit `interruptionEnded`. Reactivate the session only if resuming.
- Do not resume on `.ended` alone; always honour `shouldResume`.
- Also observe `mediaServicesWereResetNotification` and rebuild the audio engine if it fires.

### 6.3 Route changes

- Observe `AVAudioSession.routeChangeNotification`.
- Reason `.oldDeviceUnavailable` (headphones unplugged, Bluetooth disconnected) → emit `routeChanged` with `unsafe: true`. The app must not continue speaking through the loudspeaker unexpectedly.

### 6.4 Live Activity

- `VoiceSessionAttributes: ActivityAttributes` with:
  - Static: `title`.
  - `ContentState`: `state`, `muted`, `startedAt` (render elapsed time with `Text(timerInterval:)` so no per-second updates are needed).
- Start on `start()` while the app is in the foreground. Update via `activity.update(...)`. End via `activity.end(..., dismissalPolicy: .immediate)` on hang up.
- Always set a `staleDate` (e.g. now + 15 min, refreshed on each update) so a killed app doesn't leave a "live" activity forever.
- UI: compact, minimal and expanded Dynamic Island presentations, plus lock screen view. Show status (Listening / Speaking / Paused / Muted) and elapsed time.

### 6.5 Buttons (App Intents)

- iOS 17+: `Button(intent:)` inside the Live Activity, with `ToggleMuteIntent` and `HangUpIntent` conforming to `LiveActivityIntent`.
- `LiveActivityIntent` runs in the **app's** process, so intents can call the session controller directly without opening the app. Emit `remoteAction` to JS.
- Intent and attribute source files live in `ios/Shared/` and must be compiled into both the app target and the widget target.
- iOS 16.1–16.x: no interactive buttons; tapping the activity deep-links into the app (`widgetURL`).

### 6.6 Config (via config plugin)

- `UIBackgroundModes`: `audio` (otherwise iOS suspends the mic when backgrounded).
- `NSMicrophoneUsageDescription`.
- `NSSupportsLiveActivities: true`.
- App Group shared by app and widget extension.
- Widget target added with `expo-apple-targets`.

## 7. Android

### 7.1 Foreground service

- `VoiceSessionService` with `android:foregroundServiceType="microphone"`.
- Call `startForeground(id, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE)`.
- **Must be started while the app is in the foreground.** Android 14+ blocks starting a microphone FGS from the background.
- Stop the service (and remove the notification) on hang up or session end.

### 7.2 Notification

- Dedicated channel, `IMPORTANCE_LOW` (no sound or vibration).
- `setOngoing(true)`, `setCategory(CATEGORY_CALL or CATEGORY_SERVICE)`, content intent opens the app.
- Actions: Mute/Unmute (label toggles) and Hang Up. Each is a `PendingIntent` (`FLAG_IMMUTABLE`) targeting the service; the service handles it, emits `remoteAction`, and re-posts the notification.
- Text reflects `updateStatus` (Listening / Speaking / Paused / Muted) with a chronometer from `startedAt`.

### 7.3 Audio focus

- `AudioFocusRequest` with `AudioAttributes.USAGE_VOICE_COMMUNICATION`, `CONTENT_TYPE_SPEECH`.
- Focus changes:
  - `AUDIOFOCUS_LOSS_TRANSIENT` → `interruptionBegan`.
  - `AUDIOFOCUS_GAIN` after a transient loss → `interruptionEnded { shouldResume: true }`.
  - `AUDIOFOCUS_LOSS` (permanent) → `interruptionBegan`, then treat as `shouldResume: false` unless focus is regained before the timeout.
- Use `AudioManager.MODE_IN_COMMUNICATION` while active so the platform applies echo cancellation; also enable `AcousticEchoCanceler` / `NoiseSuppressor` on the capture session where available.

### 7.4 Call state

- Some calls don't produce clean focus changes. On API 31+, register a `TelephonyCallback.CallStateListener`; on older APIs, `PhoneStateListener`.
- `CALL_STATE_RINGING` / `OFFHOOK` → `interruptionBegan { source: "call" }`; `IDLE` → `interruptionEnded`.
- Note: the call-state listener requires `READ_PHONE_STATE`. If the user denies it, fall back to audio focus only.

### 7.5 Route changes

- Listen for `AudioManager.ACTION_AUDIO_BECOMING_NOISY` and register an `AudioDeviceCallback` for device removal → `routeChanged { unsafe: true }`.

### 7.6 Manifest / permissions

- `RECORD_AUDIO`
- `FOREGROUND_SERVICE`
- `FOREGROUND_SERVICE_MICROPHONE`
- `POST_NOTIFICATIONS` (Android 13+; request at runtime, otherwise the FGS notification is hidden)
- `READ_PHONE_STATE` (optional, see 7.4)

Notifee is not used; the notification is owned by the native service to keep lifecycle in one place.

## 8. Long interruptions

- `resumeTimeoutMs` default **180,000 ms (3 min)**, configurable per session.
- Timer starts on `interruptionBegan`.
- If it fires before a resumable `interruptionEnded`:
  1. End the session (`endReason: "interruption_timeout"`).
  2. End the Live Activity / stop the foreground service.
  3. Show a "Tap to resume" entry point (in-app banner on next open; optionally a normal, dismissible local notification) that starts a fresh session in one tap.
- Never resume talking or listening on our own after the timeout.

## 9. Crash and kill recovery

- On every app launch, call `cleanupStaleSessions()`:
  - iOS: end all existing `Activity<VoiceSessionAttributes>` instances.
  - Android: stop the service if running without an active JS session; cancel its notification.
- iOS `staleDate` (6.4) covers the case where the app never relaunches.
- Android: return `START_NOT_STICKY` so the system doesn't restart a mic service without the app.

## 10. Edge cases

| Case | Behaviour |
|---|---|
| Interruption during `speaking` | Stop playback; on resume, do not replay the cut-off response mid-sentence. Return to `listening` after the "I'm back" cue. |
| User hangs up from Live Activity / notification while backgrounded | Full teardown, no app launch required. |
| Mute pressed during interruption | Update flag; applies on resume. |
| Siri / Google Assistant invoked | Treated as an interruption. |
| Alarm or timer | Treated as an interruption. |
| Permission revoked mid-session | Emit `error`, end session with `endReason: "permission"`. |
| Bluetooth connects mid-session | Route change, `unsafe: false`; continue on new route. |
| Low Power Mode / Doze | FGS (Android) and audio background mode (iOS) keep the session alive; no special handling. |

## 11. Testing

Manual test matrix on physical devices (simulators/emulators don't model calls reliably):

1. Incoming call, declined → resumes with cue.
2. Incoming call, answered for < 3 min → resumes with cue after hang-up.
3. Call longer than 3 min → session ends; "Tap to resume" works.
4. Outgoing call started from the background.
5. Unplug wired headphones / disconnect Bluetooth mid-response → pauses, no speaker output.
6. Mute and Hang Up from Dynamic Island, lock screen, and Android notification shade.
7. Background the app for 10+ min → session still active.
8. Force-kill the app mid-session → activity/notification gone on next launch (or stale on iOS).
9. Android 13+ with notifications denied; Android 14+ start attempt from background.
10. iOS 16 device: Live Activity without buttons, tap opens app.

Unit tests: the JS state machine against a mocked native event stream, including timeout and `shouldResume: false` paths.

## 12. Open questions

1. Exact `resumeTimeoutMs` default (2 vs 3 min).
2. "I'm back" cue: earcon, short spoken phrase, or both.
3. Should Hang Up from outside the app also open the app to a summary screen, or stay silent?
4. Minimum iOS version: is 16.1 support (no interactive buttons) worth the extra code path?
