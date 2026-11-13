// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The portal's one build and test config (P1.4). Build and tests share it on
// purpose: the runner transforms what the build ships, so a JSX setting or an
// alias that worked in a test and not in the bundle cannot be a difference
// between two files nobody reads together.
//
// The test environment is jsdom because the portal is a browser app: every
// screen it will ever have reads and writes the document, and a suite that ran
// in the node environment would need each of them to say so again.
//
// `test.include` is deliberately left at the runner's default rather than
// pinned to `test/`. The skeleton's own suites live in `test/`, and the screen
// tasks put a screen's suite beside it (`src/screens/<area>/<area>.test.tsx`,
// which their own `npx vitest run src/screens/<area>` commands collect); a
// pinned include would make those commands match nothing and pass.

import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    restoreMocks: true,
  },
})
