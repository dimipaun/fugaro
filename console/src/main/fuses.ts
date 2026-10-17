// The fuses the console's shipped binary is built with (design section 5.1 rule
// 4; desktop-console plan Task 4.1).
//
// This module is not part of the running application. It is a **packaging hook**
// -- electron-builder calls its `afterPack` export once per platform build, after
// the app bundle has been assembled and before it is signed -- and the app never
// requires it (`src/main/index.ts` imports nothing from here, so the compiled
// `dist/main/fuses.js` sits in the asar as the record of how the binary around it
// was built). `package.json`'s `build.afterPack` names the compiled path.
//
// **A fuse is not a setting.** The three the console disables are the three ways
// an Electron binary is not an Electron application:
//
//   RunAsNode                             ELECTRON_RUN_AS_NODE=1 turns the
//                                         executable into a Node interpreter --
//                                         for anyone who can run it, not just
//                                         the owner.
//   EnableNodeOptionsEnvironmentVariable  NODE_OPTIONS=--require ... is code
//                                         injected through the environment.
//   EnableNodeCliInspectArguments         --inspect hands the process to a
//                                         debugger.
//
// The design's rule 10 is the same rule from the other side: the console does
// not read its own environment (`FUGARO_CONSOLE_SPAWN_LOG` is written by the
// app and read by the operator), and this file is what stops its *process* from
// being configured by one. The two enabled fuses are the loading rule: the app is
// loaded from `app.asar` and nowhere else, and only if the asar is the one that
// was packed. Every other fuse is left at its upstream default, and
// `LEFT_ALONE_FUSES` names those two so that "left alone" is a decision in this
// file rather than an omission from it.
//
// **The hook verifies what it wrote.** `flipFuses` writes the wire and reports
// how many fuses it changed, not what the binary now holds -- so a table entry
// carrying the index of the fuse above it flips the wrong byte and the packer
// says nothing. `afterPack` therefore reads the wire back and fails, naming the
// fuse and the binary, if a flip did not take: reading it back is the only way
// this build can state what the binary it is about to sign actually holds.
//
// It is not a guard against a path that is not an Electron binary: the pinned
// `@electron/fuses` refuses that itself -- both calls reject with "Could not
// find sentinel in the provided Electron binary, fuses are only supported in
// Electron 12 and higher" (`fuses.test.ts` asserts exactly that, so a version
// which started returning quietly would be a red test here rather than a hook
// that had grown to trust it).
//
// **What this file deliberately does not do.** It does not sign, staple or
// notarise -- electron-builder signs after this hook, `entitlements.mac.plist`
// holds what the signature is allowed, and the verification of the signed result
// is the owner's Check 59a. It does not decide the app's identity: `package.json`
// carries no signing identity on purpose, and the certificate arrives in the
// environment (`CSC_LINK`/`CSC_NAME`), which is the task's rule that no identity
// is ever written into a file in this repository.
//
// **A note on the import below, because it carries a directive.** `@electron/fuses`
// 2.0.0 is an ES module and this file is the console's CommonJS build (`module:
// Node16`, and package.json has no `"type"`), so `tsc` refuses the static import
// of it with TS1479 -- the module-format rule, not a resolution failure: the
// package's types still land on every binding, which is why the `@ts-ignore`
// directly above the import is a suppression of that one rule and nothing else.
// What the emitted `require("@electron/fuses")` relies on is `require(esm)`,
// which Node has supported since 22.12: the floor in `engines.node`, the
// mechanism electron-builder's own fuse support uses, and something this file
// was run against rather than assumed (see the README). The directive is
// `@ts-ignore` rather than `@ts-expect-error` because the two `tsc` runs of this
// directory do not agree here: `tsconfig.build.json` reports TS1479, and the
// type checker (`tsconfig.json`) resolves the import in ESM mode and reports
// nothing, so an expect-error would fail *there* as unused.
//
// The cost is bounded on purpose. A suppressed import line is a line `tsc` will
// not flag if this package ever changes its API, so the suite carries that
// check instead: `fuses.test.ts` imports the same package and asserts that it
// exports both calls and that every name in the table below is a member of the
// pinned `FuseV1Options`. It is a devDependency: the packer needs it, the app
// does not, and `test/unit/pinning.test.ts` keeps `dependencies` exactly as it
// was.
// @ts-ignore -- TS1479, this file's CommonJS build, and `require(esm)` at 22.12.
import { FuseState, FuseV1Options, FuseVersion, flipFuses, getCurrentFuseWire } from '@electron/fuses'
import { join } from 'node:path'

// FuseConfig is the shape of the configuration `@electron/fuses` takes and
// returns, written structurally rather than imported as a type, because a
// *type-only* import across the same boundary is refused too (TS1541, which a
// `resolution-mode` attribute would silence in the build config and which the
// type checker does not report). Nothing here is unchecked: the alias only
// *names* what the two functions already return, and they carry the package's
// own types.
type FuseConfig<State = boolean> = { readonly version: FuseVersion } & Partial<
  Record<FuseV1Options, State>
>

/** FlippedFuse is one fuse this build sets, with the reason it is set that way.
 * The reason is prose and it is not decoration: `fuses.test.ts` asserts every
 * entry carries one, so a fuse cannot be added here as a bare boolean. */
export interface FlippedFuse {
  /** name is the fuse's own name in the pinned `@electron/fuses`, and it is
   * written out beside the enum member on purpose: the two must agree, and
   * `fuses.test.ts` asserts that they do, so a copy-pasted entry carrying the
   * index of the fuse above it is a red test rather than the wrong byte
   * flipped. It is also what the hook's failure message names. */
  readonly name: string
  readonly option: FuseV1Options
  readonly value: boolean
  readonly why: string
}

/** LeftAloneFuse is one fuse this build does *not* set, with the reason the
 * upstream default is the right one for the console. Absence is asserted for
 * each of these, so an Electron bump that changes a default fails a named test
 * here rather than changing the shipped binary quietly. */
export interface LeftAloneFuse {
  /** name, as `FlippedFuse.name`: the pinned package's own name for the fuse. */
  readonly name: string
  readonly option: FuseV1Options
  readonly why: string
}

/**
 * FLIPPED_FUSES is the whole of the design's rule 4, in the order the fuse wire
 * holds them: the six fuses the console's binary is built with, and nothing
 * else. Each one is asserted by name in `test/unit/fuses.test.ts`.
 */
export const FLIPPED_FUSES: readonly FlippedFuse[] = [
  {
    name: 'RunAsNode',
    option: FuseV1Options.RunAsNode,
    value: false,
    why: 'ELECTRON_RUN_AS_NODE must not turn the shipped console into a Node interpreter.',
  },
  {
    name: 'EnableNodeOptionsEnvironmentVariable',
    option: FuseV1Options.EnableNodeOptionsEnvironmentVariable,
    value: false,
    why: 'NODE_OPTIONS must not reach the console: it is code injected through the environment.',
  },
  {
    name: 'EnableNodeCliInspectArguments',
    option: FuseV1Options.EnableNodeCliInspectArguments,
    value: false,
    why: '--inspect must not attach a debugger to the console.',
  },
  {
    name: 'EnableEmbeddedAsarIntegrityValidation',
    option: FuseV1Options.EnableEmbeddedAsarIntegrityValidation,
    value: true,
    why: 'the app is loaded only when its asar is the one that was packed, byte for byte.',
  },
  {
    name: 'OnlyLoadAppFromAsar',
    option: FuseV1Options.OnlyLoadAppFromAsar,
    value: true,
    why: 'the app is loaded from app.asar and from nowhere else (design section 5.1 rule 4).',
  },
  {
    name: 'GrantFileProtocolExtraPrivileges',
    option: FuseV1Options.GrantFileProtocolExtraPrivileges,
    value: false,
    why: 'file:// keeps no extra privileges; the console serves itself over app:// (Task 1.1).',
  },
]

/**
 * LEFT_ALONE_FUSES is every fuse this build does not set. It exists so that
 * "left at its upstream default" is written down: the two below are asserted
 * absent from the configuration, and the suite also asserts that the two lists
 * together account for every fuse the pinned `@electron/fuses` knows.
 */
export const LEFT_ALONE_FUSES: readonly LeftAloneFuse[] = [
  {
    name: 'EnableCookieEncryption',
    option: FuseV1Options.EnableCookieEncryption,
    why: 'the console keeps no cookie store: no sign-in is stored, and upstream defaults stand.',
  },
  {
    name: 'LoadBrowserProcessSpecificV8Snapshot',
    option: FuseV1Options.LoadBrowserProcessSpecificV8Snapshot,
    why: 'a startup optimisation for a browser process that loads web content; not this app.',
  },
]

/**
 * fuseConfig is the configuration the packer passes, built from
 * `FLIPPED_FUSES` so the list above and what is written into the binary cannot
 * drift. It is deliberately a *partial* record: a fuse this file does not name
 * is one the packer leaves at its upstream default, which is what
 * `LEFT_ALONE_FUSES` records. `resetAdHocDarwinSignature` is not set either --
 * electron-builder re-signs the bundle after this hook, which is the same order
 * its own fuse support uses (see `addElectronFuses` in app-builder-lib).
 */
export function fuseConfig(): FuseConfig {
  const flips: Record<number, boolean> = {}
  for (const fuse of FLIPPED_FUSES) flips[fuse.option] = fuse.value
  // The cast is the partial record: a `Record<number, boolean>` spread is not
  // assignable to `Partial<Record<FuseV1Options, boolean>>`, and the keys are
  // enum members by construction (they come from `FLIPPED_FUSES`).
  return { version: FuseVersion.V1, ...flips } as FuseConfig
}

/**
 * AfterPackContext is the part of electron-builder's `AfterPackContext` this
 * hook reads, written out structurally rather than imported: the packer is
 * deliberately not a dependency of this package (see console/README.md), so its
 * types are not available to `tsc` here. The shape is the packer's own --
 * `electronPlatformName`, `appOutDir` and `packager.appInfo.productFilename`.
 */
export interface AfterPackContext {
  readonly electronPlatformName: string
  readonly appOutDir: string
  readonly packager: { readonly appInfo: { readonly productFilename: string } }
}

/** FuseTools is the two `@electron/fuses` calls this hook makes, as an
 * injectable pair so the suite can drive `afterPack` with a fake -- including a
 * fake that returns a wire where a flip did not take. */
export interface FuseTools {
  flipFuses(pathToElectron: string, config: FuseConfig): Promise<number>
  getCurrentFuseWire(pathToElectron: string): Promise<FuseConfig<FuseState>>
}

/** ELECTRON_FUSES is the real pair, and the default argument of `afterPack`. */
const ELECTRON_FUSES: FuseTools = { flipFuses, getCurrentFuseWire }

/**
 * macExecutable is the binary inside the packed bundle that carries the fuse
 * wire: `<appOutDir>/<productFilename>.app/Contents/MacOS/<productFilename>`.
 *
 * It mirrors electron-builder's own resolution rather than inventing one. In
 * app-builder-lib, `AppInfo.productFilename` is `sanitizeFileName(productName)`
 * unless a platform-specific `executableName` overrides it (nothing here does),
 * and `addElectronFuses` joins it to `appOutDir` with the `.app` extension on
 * darwin. The bundle and the executable are therefore both named by
 * `package.json`'s `productName`, which is `Fugaro Console` -- the name Check
 * 59a's launch line and the window's own title depend on.
 */
export function macExecutable(context: AfterPackContext): string {
  const name = context.packager.appInfo.productFilename
  return join(context.appOutDir, `${name}.app`, 'Contents', 'MacOS', name)
}

/** wireState spells a wire byte the way a failure message should read. */
function wireState(state: FuseState | undefined): string {
  if (state === FuseState.ENABLE) return 'enabled'
  if (state === FuseState.DISABLE) return 'disabled'
  if (state === FuseState.REMOVED) return 'removed rather than set'
  if (state === FuseState.INHERIT) return 'left to inherit'
  return `absent (${String(state)})`
}

/**
 * afterPack is what electron-builder calls: it flips the six fuses into the
 * packed binary and then reads the wire back, failing if any of them is not what
 * this build asked for.
 *
 * A platform that is not darwin is a hard failure rather than a skip. The
 * console is a macOS application (design D4, and this MVP ships no other
 * platform): a build here would produce no `Contents/MacOS` binary to flip, and
 * the one outcome that must not be possible is a bundle that ships with the
 * fuses unset because the hook decided to do nothing.
 *
 * Nothing is logged on success. The hook's output is the packer's log at pack
 * time, and a "flipped the fuses" line in it would be a claim rather than the
 * check -- the check is the wire read below, and its failure is the message.
 */
export async function afterPack(
  context: AfterPackContext,
  fuses: FuseTools = ELECTRON_FUSES,
): Promise<void> {
  if (context.electronPlatformName !== 'darwin') {
    throw new Error(
      `the Fugaro console is a macOS (darwin) application, and the fuse hook was called for ` +
        `${context.electronPlatformName}: no app bundle was produced to flip, and shipping ` +
        'one with the fuses unset is not an option this build may take',
    )
  }
  const executable = macExecutable(context)
  await fuses.flipFuses(executable, fuseConfig())
  const wire = await fuses.getCurrentFuseWire(executable)
  for (const fuse of FLIPPED_FUSES) {
    const expected = fuse.value ? FuseState.ENABLE : FuseState.DISABLE
    const actual = wire[fuse.option]
    if (actual !== expected) {
      throw new Error(
        `the ${fuse.name} fuse is ${wireState(actual)} in ${executable}, ` +
          `and this build requires it ${fuse.value ? 'enabled' : 'disabled'} -- the flip did ` +
          'not take, so the binary must not be signed as it stands',
      )
    }
  }
}
