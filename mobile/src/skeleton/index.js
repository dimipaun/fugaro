// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The app skeleton (mobile plan, Task P3.1).
//
// This module carries no logic. It exists so that the package's manifest has
// something the glob can find before the account store does, which is what
// makes P3.1's red step a real one: `src/skeleton/index.test.js` was written
// first and failed with ERR_MODULE_NOT_FOUND until this file existed, so a
// green run over an empty directory cannot be mistaken for a green run.
//
// D125 gives the app its own artifact: its own directory outside the Go
// module, its own release path and cadence, and no place in the CLI tag's
// release gate. The name below is the one the manifest carries, and the test
// beside this file asserts the two agree.

export const SKELETON = 'fugaro-mobile';
