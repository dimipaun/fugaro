// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The spike's entry: the one file that hands the shell to the Expo runtime
// (task M0; Check 733).

import { registerRootComponent } from 'expo';

import { App } from './src/app.js';

registerRootComponent(App);
