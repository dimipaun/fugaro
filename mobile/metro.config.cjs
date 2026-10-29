// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// Metro's config (the file that makes `expo export`, `expo start` and an EAS
// build able to bundle this app at all).
//
// **Why it exists.** The app imports `shared/` -- the pure, tested modules
// Phase 0-2 put on `main` -- with relative paths that leave `mobile/`:
// `src/demo/index.js` imports `../../../shared/confirm/index.js` and
// `../../../shared/prompts/index.js`, and `src/demo/transport.js` imports
// `../../../shared/mailbox-schema/index.js`. Node resolves them (that is what
// `node --test` runs on) and D119's boundary rule allows them, but Metro's
// project root is `mobile/`, and a path above the project root is outside the
// bundle's graph until it is added to `watchFolders`. Without this file
// `npx expo export` fails with:
//
//   Unable to resolve module ../../../shared/confirm/index.js from
//   /work/repo/mobile/src/demo/index.js
//
// `mobile/README.md` recorded exactly this gap ("bundling `mobile/` and
// `shared/` together needs a Metro `watchFolders` entry that no task has added
// yet"); the task that declared the SDK dependencies is the task that closed
// it, because declaring them is what makes a bundle possible for the first
// time.
//
// **How to check that this file is still needed, and why the answer is not
// "try it without the cache".** The failure above is reproduced from a clean
// tree by deleting this file and emptying Metro's cache first:
//
//   mv metro.config.cjs /tmp/ && rm -rf /tmp/metro-cache /tmp/metro-file-map-expo-* dist
//   npx expo export --platform ios        # -> Unable to resolve module .../shared/confirm/index.js
//
// Three neighbouring cases were tried and **all three also fail**, so a warmer
// cache does not hide the problem and neither does dev mode:
//
//   * deleting this file with the cache left warm from a run that HAD it;
//   * `npx expo export --dev --platform ios` without it;
//   * `EXPO_USE_METRO_WORKSPACE_ROOT=1 npx expo export --platform ios` without
//     it — Expo's own monorepo default (`getWatchFolders`) returns an empty
//     list here, because it looks for a workspace root above `mobile/` and
//     finds no root `package.json` with a `workspaces` field to add.
//
// Restored, both platforms bundle again, and `dist/` is written. `watchFolders`
// is the whole of the difference, so its content is asserted by a test rather
// than left to a run: `src/dependencies.test.js` fails if this file is missing
// or if it stops adding the repository root to `watchFolders`.
//
// **Why `.cjs`, and not `.js`.** `mobile/package.json` is `"type": "module"`,
// so a `metro.config.js` would be read as an ES module while Metro loads its
// config with `require`. `.cjs` says "this one file is CommonJS" without
// touching the package's type, which is the choice Expo's own documentation
// makes for an ESM project. Metro and Expo both search for `metro.config.cjs`
// (`metro-config`'s `SEARCH_JS_EXTS`).
//
// **What it does NOT do.** It adds no transformer, no resolver alias and no
// extra asset type: the app's imports are ordinary relative paths and the
// default Expo config resolves everything else. `watchFolders` is the whole
// change -- one directory, because `shared/` is the only thing outside the
// project root the app imports.

const path = require('node:path');

const { getDefaultConfig } = require('expo/metro-config');

const projectRoot = __dirname;
// The repository root, one level up: `shared/` lives there and the app's
// `../../../shared/...` imports resolve through it.
const repoRoot = path.resolve(projectRoot, '..');

const config = getDefaultConfig(projectRoot);

config.watchFolders = [repoRoot];

// The dependency tree stays the one `npm install` writes in `mobile/`: there is
// no `node_modules` above this package to confuse, and saying so here keeps a
// future workspace root from silently changing which copy of `react` the app
// bundles.
config.resolver.nodeModulesPaths = [path.resolve(projectRoot, 'node_modules')];

module.exports = config;
