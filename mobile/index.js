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
import { createDemoMode } from './src/demo/index.js';
import { createDemoScreen } from './src/demo/screen.js';

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

  // React's own subscription over the demo's `subscribe`/`state` pair, which is
  // the shape `useSyncExternalStore` takes: the shell re-renders when the demo
  // changes, and never polls it.
  const useDemo = () => useSyncExternalStore(demo.subscribe, demo.state);

  // **The app's first async step, and the reason `startup.report` exists.** A
  // `try` around this call cannot catch what `enter()` does after its first
  // `await`, so a rejection is handed to the guard instead: the reviewer sees
  // the failure on the screen rather than a control that does nothing. A later
  // task's SecureStore read, database listener or fetch joins through the same
  // door, and never through a `console` this tree refuses to have.
  const guardAsync = (what, run) => () => {
    const answer = run();
    if (answer !== null && typeof answer === 'object' && typeof answer.catch === 'function') {
      answer.catch((error) => startup.report(error, what));
    }
    return answer;
  };

  const screens = {
    // The screen the demo is entered from, with the seam `pairingScreen` that
    // P6.8's pairing screen will occupy -- above the demo control, never instead
    // of it (`src/app/first-screen.js`).
    first: createFirstScreen({
      React: { createElement },
      View,
      Text,
      Pressable,
      onTryDemo: guardAsync('entering the demo', () => demo.enter()),
      pairingScreen: null,
    }),
    demo: createDemoScreen({
      React: { createElement },
      View,
      Text,
      Pressable,
      onConfirm: guardAsync('confirming the demo command', () => demo.confirm()),
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
