// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The one declaration `semver` needs, and nothing else (desktop-console plan
// Task 4.4; design section 5.2's pinning rule).
//
// `electron-updater` 6.8.10's `out/AppUpdater.d.ts` line 4 imports `SemVer`
// from `semver`, and the `semver` it nests (7.7.4, under its own
// `node_modules/`) ships no declarations: its `package.json` has no `types`
// field and the package carries no `.d.ts`. `skipLibCheck` is deliberately off
// in this project (tsconfig.json), so the import is a hard `TS7016` -- "could
// not find a declaration file for module 'semver'" -- over a package this app
// does not depend on, cannot pin and never calls.
//
// **Why this file rather than `@types/semver`.** The task this belongs to adds
// exactly one npm dependency, and a types-only package would be a second: a new
// entry in package.json, a new resolution in the lockfile, and a second name in
// the supply chain of a build whose whole point is that it has one. What the
// type checker actually needs is one name, in one position
// (`readonly currentVersion: SemVer`), and this is that name.
//
// `unknown` and not `any`, and not a stand-in for the real type: a member the
// library adds to `SemVer` would be an error here rather than a silent `any`,
// which is the direction that gets noticed. If `@types/semver` is ever added
// for a real reason, this file goes -- the two cannot both declare the module.
declare module 'semver' {
  export type SemVer = unknown
}
