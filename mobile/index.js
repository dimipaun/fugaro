// The app's entry: the one file that knows what a device is
// (mobile plan, Task P3.1; D111, D114, D125).
//
// **Why this file is untested, and why that is acceptable.** Everything under
// `src/` is written as a function of its platform module, so it can be tested
// with fakes and no device (Global Constraint 10); this file is where the real
// modules are plugged in, and it has no decisions of its own left to test --
// the accessibility of the Keychain item, the account-isolation rules and the
// shapes of the calls all live in `src/`, in files with tests. What can still
// go wrong here is a wrong import, a wrong port on the wrong store, and an SDK
// that is not installed; the first two are visible in the twelve lines below
// and the third is verified when the app is first built on a machine with the
// toolchain, which is listed as unverified in `mobile/README.md` and in this
// task's pull request.
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

import AsyncStorage from '@react-native-async-storage/async-storage';
import { registerRootComponent } from 'expo';
import * as SecureStore from 'expo-secure-store';
import { createElement } from 'react';
import { Text, View } from 'react-native';

import { asyncStoragePort } from './src/accounts/expo-async-storage.js';
import { expoSecureStorePort } from './src/accounts/expo-secure-store.js';
import {
  AccountStore,
  ResourceRegistry,
  SecretStore,
  SettingsStore,
} from './src/accounts/index.js';
import { createRoot } from './src/app/root.js';

const registry = new ResourceRegistry();

const store = new AccountStore({
  secrets: new SecretStore(expoSecureStorePort(SecureStore)),
  settings: new SettingsStore(asyncStoragePort(AsyncStorage)),
  registry,
});

// The four modules are passed in rather than imported by `src/`, which is what
// lets every assertion about the tree run in CI with no device (see
// `src/app/root.js`).
registerRootComponent(createRoot({ React: { createElement }, store, View, Text }));
