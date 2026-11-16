// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's entry: the one file that knows what a device is
// (mobile plan, Task P3.1; D111, D114, D125).
//
// **Why this file is untested, and why that is acceptable.** Everything under
// `src/` is written as a function of its platform module, so it can be tested
// with fakes and no device (Global Constraint 10); this file is where the real
// modules are plugged in, and it has no decisions of its own left to test --
// the accessibility of the Keychain item, the account-isolation rules and the
// shapes of the calls all live in `src/`, in files with tests. What can still
// go wrong here is a wrong import, a wrong port on the wrong store, an SDK that
// is not installed, and the platform the app is running on; the first three are
// visible in the wiring below and the last is passed in from `Platform.OS`, and
// all four are verified when the app is built and run on a machine with the
// toolchain, which is listed as unverified in `mobile/README.md` and in this
// task's pull request.
//
// **The crash of 2026-10-21, and what changed here.** The owner installed the
// `preview` APK on Android and it opened and closed with no screen at all. The
// cause was on the third line of the wiring below:
//
//   expoSecureStorePort(SecureStore)          // <- required an iOS-only constant
//   expoSecureStorePort(SecureStore, { platform: Platform.OS })   // now
//
// `expo-secure-store`'s Keychain accessibility levels are declared by its
// **Swift** module only -- its Kotlin module registers no `Constant(...)` at
// all -- so on Android the constant the port insisted on reading is
// `undefined`, and the port threw while the port was being *built*. That build
// happens here, at module scope: before `registerRootComponent`, before React,
// before anything can be drawn. `src/accounts/expo-secure-store.js` carries the
// full account of it, and the port now takes the platform, because which
// platform this is decides whether that constant is required.
//
// The other half of the answer is the guard around the whole of the wiring
// (`src/app/startup.js`): a throw from any line below now renders a screen that
// says what failed, with the message and a "Copy details" line, instead of
// closing the app. A release build on a phone has no terminal and no red box,
// so a startup failure that is not *drawn* is a startup failure nobody can
// report. Everything that can fail early -- the two ports, the store, the demo,
// the screens and the shell -- is built inside `startup.root(...)` for exactly
// that reason, and `startup.report` is the same door for an async step (the
// demo's own entry is the app's first one today).
//
// **The pairing that matters is the next four lines.** `expoSecureStorePort`
// wraps the Keychain and is given to `SecretStore` -- D111's two items.
// `asyncStoragePort` wraps the UNENCRYPTED store and is given to
// `SettingsStore` -- D114's tuple, and the fences in `settings.js` are what
// keep a credential from ever being written through that second door. Swapping
// these two arguments would be the worst bug this file could have, and it is
// caught by the boundary scan (`src/boundary.test.js`) as well as by review:
// `SecretStore` refuses anything that is not a `Secret`, and a `Secret` is
// refused by the settings store, so a swapped pair fails both ways at the
// first write rather than storing anything in the clear.
//
// **The demo's wiring, added 2026-10-19.** This file also builds the demo
// controller and the two screens, because the shell may import neither React's
// hooks nor react-native itself (`src/app/root.js` explains why); the four
// imports below are the whole of the demo's reach into the app, and the demo
// reaches no further than a reviewer's tap.
//
// **The voice session's wiring, added 2026-11-13 (VS1.3).** The session is
// built here too, and *only* here: §5's port is handed in and never imported
// (`src/voice-session/port.js` says why), this is the file that hands one in,
// and `src/boundary.test.js` pins that the constructor appears here and nowhere
// else under `mobile/`. The demo is on the other side of that fence by
// construction (F10, D212): it has no session to build one with.

import AsyncStorage from '@react-native-async-storage/async-storage';
import { registerRootComponent } from 'expo';
import * as SecureStore from 'expo-secure-store';
import React, { createElement, useSyncExternalStore } from 'react';
import { Platform, Pressable, Text, View } from 'react-native';

import { asyncStoragePort } from './src/accounts/expo-async-storage.js';
import { expoSecureStorePort } from './src/accounts/expo-secure-store.js';
import {
  AccountStore,
  ResourceRegistry,
  SecretStore,
  SettingsStore,
} from './src/accounts/index.js';
import { createFirstScreen } from './src/app/first-screen.js';
import { createRoot } from './src/app/root.js';
import { createGuardedStartup } from './src/app/startup.js';
import { FakeAudioIn, FakeStt, FakeTts, createVoicePipeline } from './src/audio/index.js';
import { createDemoMode } from './src/demo/index.js';
import { createDemoScreen } from './src/demo/screen.js';
import { createVoiceScreen } from './src/voice/index.js';
import { FakeVoiceSessionNative } from './src/voice-session/fake.js';
import * as machine from './src/voice-session/index.js';

// The guard, before anything it guards. It is built from `React` (it needs
// `Component` for the error boundary -- a class is the only mechanism React
// offers for catching a render throw) and from the two primitives the failure
// screen draws with, and it holds no device and no store of its own, which is
// why it can be built first and cannot be the thing that fails.
const startup = createGuardedStartup({ React, View, Text });

// **Everything that can throw is inside this call.** `root(build)` runs `build`
// in a `try`, and a throw from any line of it becomes the readable screen
// instead of a process that exits.
registerRootComponent(startup.root(() => {
  const registry = new ResourceRegistry();

  const store = new AccountStore({
    secrets: new SecretStore(expoSecureStorePort(SecureStore, { platform: Platform.OS })),
    settings: new SettingsStore(asyncStoragePort(AsyncStorage)),
    registry,
  });

  // The demo (owner ruling of 2026-10-19): built here, entered only by the
  // reviewer tapping "Try the demo" on the first screen. Nothing about it runs at
  // startup -- `createDemoMode` builds no client, no store and no queue until
  // `enter()` is called -- and it takes no port, no account store and no key
  // source, which is why a demo that could read this phone's Keychain does not
  // exist to be written. `src/demo/safety.test.js` is the fence around both.
  const demo = createDemoMode();

  // **The voice session (VS1.3), and every stand-in it is built from.** VS1.2's
  // fake answers §5's port where a phone will, exactly as P3.4's three fakes
  // answer the audio ports: the app has no microphone, no engine, no speaker
  // and no voice module today (reconciliation F23), and the fake is what lets
  // it run unchanged while the native halves (VS3, VS4) are written. The values
  // the app cannot supply yet are named rather than hidden, each with the task
  // that replaces it -- the writing identity (D114's `authUid`, from P6.8's
  // pairing), this installation's name for P1.2's lease (the same pairing), the
  // mailbox client (P3.6's, which needs that account and a database connection
  // before it can be built), the id source (P3.6's) and the clock (D117's
  // server-anchored one, which arrives with the mailbox).
  //
  // **Nothing reads any of them today, and `UNPAIRED_CLIENT` is the reason one
  // of them is not a lie.** No control starts this session and `createRoot` is
  // not given it (VS6 is the task that puts one on a screen), the pipeline is
  // over the fakes so no transcript ever arrives, and a session nothing drives
  // composes no message and takes no lease. The client refuses every call with
  // a code of its own instead of answering `ok`, because a stand-in that said a
  // message had been written is the one failure this wiring could hide.
  //
  // **D127's switch is deliberately not passed.** `createVoiceScreen` defaults
  // it to `OFF`, the owner has set nothing, and the setting is never written
  // down -- so the honest wiring is the default and not a stated preference.
  const UNPAIRED_SENDER = 'unpaired';
  const UNPAIRED_HOLDER = 'unpaired-installation';
  /** §8's window. D201's default is the owner's decision and still open, so
   * this is §8's number rather than a settled one. */
  const RESUME_TIMEOUT_MS = 180_000;
  /** The refusal `UNPAIRED_CLIENT` answers with, in D213's shape: a code and
   * the field it names, and no message. */
  const NOT_WIRED = Object.freeze({ ok: false, code: 'mailbox-not-wired', field: null });
  const unpairedClient = Object.freeze({
    send: async () => NOT_WIRED,
    poll: async () => NOT_WIRED,
    licenceRefusal: async () => NOT_WIRED,
  });
  let unpairedIds = 0;

  const voiceNative = new FakeVoiceSessionNative();
  const voicePipeline = createVoicePipeline({
    audioIn: new FakeAudioIn(),
    stt: new FakeStt(),
    tts: new FakeTts(),
    sender: UNPAIRED_SENDER,
    newId: () => `unpaired-${(unpairedIds += 1)}`,
    now: () => Date.now(),
  });
  const voiceScreen = createVoiceScreen({
    pipeline: voicePipeline,
    client: unpairedClient,
    now: () => Date.now(),
    holder: UNPAIRED_HOLDER,
  });
  // The door carries the machine and the controller (VS0.3, widened by VS1.3),
  // so the one import is both arguments: `reduce` and `INITIAL_SESSION_STATE`
  // are what the session reads off it, and nothing else.
  const session = machine.createVoiceSession({
    machine,
    screen: voiceScreen,
    pipeline: voicePipeline,
    native: voiceNative,
    now: () => Date.now(),
    resumeTimeoutMs: RESUME_TIMEOUT_MS,
    title: 'Fugaro',
  });
  // React's own subscription over the demo's `subscribe`/`state` pair, which is
  // the shape `useSyncExternalStore` takes: the shell re-renders when the demo
  // changes, and never polls it.
  const useDemo = () => useSyncExternalStore(demo.subscribe, demo.state);

  // **The app's first async step, and the reason `startup.guard` exists.** A
  // `try` around this call cannot catch what `enter()` does after its first
  // `await`, so a rejection is handed to the guard instead: the reviewer sees
  // the failure on the screen rather than a control that does nothing. The
  // handler is a tap on a `Pressable`, which React runs from the platform's
  // event loop rather than from the wiring's `try`, so the **synchronous** half
  // has to be caught too -- `startup.guard` does both (its own docstring says
  // why the sync half is the likelier one), and a later task's SecureStore read,
  // database listener or fetch joins through the same door, never through a
  // `console` this tree refuses to have.
  const screens = {
    // The screen the demo is entered from, with the seam `pairingScreen` that
    // P6.8's pairing screen will occupy -- above the demo control, never instead
    // of it (`src/app/first-screen.js`).
    first: createFirstScreen({
      React: { createElement },
      View,
      Text,
      Pressable,
      onTryDemo: startup.guard('entering the demo', () => demo.enter()),
      pairingScreen: null,
    }),
    demo: createDemoScreen({
      React: { createElement },
      View,
      Text,
      Pressable,
      onConfirm: startup.guard('confirming the demo command', () => demo.confirm()),
    }),
  };

  // The four modules are passed in rather than imported by `src/`, which is what
  // lets every assertion about the tree run in CI with no device (see
  // `src/app/root.js`).
  return createRoot({
    React: { createElement },
    store,
    View,
    Text,
    Pressable,
    useDemo,
    screens,
    onLeaveDemo: () => demo.leave(),
  });
}));
