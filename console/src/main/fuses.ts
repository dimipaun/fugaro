// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The fuses the console's shipped binary is built with (design section 5.1 rule
// 4; desktop-console plan Task 4.1).
//
// This module is not part of the running application. It is a **packaging hook**
// -- electron-builder calls its `afterPack` export once per platform build, after
// the app bundle has been assembled and before it is signed -- and the app never
// requires it (`src/main/index.ts` imports nothing from here). The packer loads
// the compiled `dist/main/fuses.js` from the project directory, not from the
// asar, so `build.files` excludes it from the bundle: it needs a devDependency
// the shipped app does not carry. `package.json`'s `build.afterPack` names the
// compiled path.
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
// `LEFT_ALONE_FUSES` names those three so that "left alone" is a decision in this
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
// 2.1.3 is an ES module and this file is the console's CommonJS build (`module:
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
  /** upstreamDefault is the state the pinned Electron 44.7.2 ships the fuse
   * in, read off the real binary; the hook refuses a wire that disagrees. */
  readonly upstreamDefault: boolean
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
 * "left at its upstream default" is written down: the three below are asserted
 * absent from the configuration, and the suite also asserts that the two lists
 * together account for every fuse the pinned `@electron/fuses` knows.
 *
 * `WasmTrapHandlers` (index 8, enabled by default) lets V8 use guard pages and a
 * signal handler for WebAssembly out-of-bounds checks instead of explicit
 * bounds checks. It was a fuse beyond the pin until `@electron/fuses` 2.1 named
 * it, and the decision is unchanged by the package learning its name: it loads
 * no code and reads no environment variable or command-line flag (so it is not
 * one of the "an Electron binary is not an Electron application" holes rule 4
 * closes), the console runs no WebAssembly of its own, and turning it off
 * trades a performance path for explicit checks without closing any avenue an
 * attacker has.
 */
export const LEFT_ALONE_FUSES: readonly LeftAloneFuse[] = [
  {
    name: 'EnableCookieEncryption',
    option: FuseV1Options.EnableCookieEncryption,
    upstreamDefault: false,
    why: 'the console keeps no cookie store: no sign-in is stored, and upstream defaults stand.',
  },
  {
    name: 'LoadBrowserProcessSpecificV8Snapshot',
    option: FuseV1Options.LoadBrowserProcessSpecificV8Snapshot,
    upstreamDefault: false,
    why: 'a startup optimisation for a browser process that loads web content; not this app.',
  },
  {
    name: 'WasmTrapHandlers',
    option: FuseV1Options.WasmTrapHandlers,
    upstreamDefault: true,
    why: 'a WebAssembly bounds-check strategy: it loads no code and reads no environment, and the console runs no WebAssembly.',
  },
]

/** BeyondPinFuse is a fuse the pinned *Electron binary* carries that the pinned
 * `@electron/fuses` has no name for: its wire is longer than the package's enum.
 * Such a fuse cannot be set through `flipFuses` at all (the package's wire
 * builder emits one entry per enum member and leaves every later byte as it
 * found it), so the only decision available is "leave it at the binary's
 * default" -- and that decision is written down here, with the default the
 * hook then verifies, rather than left as a byte nobody looked at. */
export interface BeyondPinFuse {
  /** name is Electron's own name for the fuse (its `fuses.json5`). */
  readonly name: string
  /** index is its position on the fuse wire. */
  readonly index: number
  /** upstreamDefault is the state the pinned Electron ships it in, which the
   * hook requires it still holds after the flip. */
  readonly upstreamDefault: boolean
  readonly why: string
}

/**
 * BEYOND_PIN_FUSES is every fuse on the pinned Electron 44.7.2 wire past the
 * nine `@electron/fuses` 2.1.3 knows. There is none: the package names every
 * fuse the binary carries. The list stays because an Electron bump can run
 * ahead of the package again, and `fuses.test.ts` reads the real binary's wire
 * length when the Electron distribution is installed, so a tenth fuse arrives
 * as a red test that is answered here or in the lists above.
 */
export const BEYOND_PIN_FUSES: readonly BeyondPinFuse[] = []

/** ACCOUNTED_FUSE_COUNT is how many fuses this file has decided about, and so
 * how long the pinned binary's fuse wire must be: the hook refuses a binary
 * whose wire is longer (a fuse nobody decided about) or shorter. */
export const ACCOUNTED_FUSE_COUNT =
  FLIPPED_FUSES.length + LEFT_ALONE_FUSES.length + BEYOND_PIN_FUSES.length

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
 * hook reads, written out structurally rather than imported: the packer is a
 * devDependency, but its types live in app-builder-lib (a transitive package
 * this one does not declare), and the hook reads five fields. The shape is the
 * packer's own -- `electronPlatformName`, `appOutDir`, `packager.appInfo`'s
 * `productFilename` and `sanitizedName`, and `packager.executableName`.
 *
 * The last two are the Linux half, and neither is on the base packager:
 * `LinuxPackager` declares `executableName` and is the only class that assigns
 * it -- to `sanitizeFileName(linux.executableName)` when the configuration sets
 * that key, and to `appInfo.sanitizedName` (`sanitizeFileName(package.json name)`
 * lowercased) when it does not. `PlatformPackager` is where an executable name
 * is read for the other platforms, and it guards those reads with
 * `this instanceof LinuxPackager`, falling back to `appInfo.productFilename`;
 * that is why this field is optional here and why it is read on linux alone.
 * Both are optional because neither is a field this file may require: on darwin
 * the bundle is named by `productFilename` and neither is read.
 */
export interface AfterPackContext {
  readonly electronPlatformName: string
  readonly appOutDir: string
  readonly packager: {
    readonly appInfo: {
      readonly productFilename: string
      readonly sanitizedName?: string
    }
    readonly executableName?: string
  }
}

/**
 * SUPPORTED_PLATFORMS is the set of `electronPlatformName`s this build produces
 * a binary for, and so the set the fuse hook flips. It is one list, and the
 * hook's refusal is its complement: a platform that is not in it is a hard
 * failure rather than a skip.
 *
 * `win32` is deliberately absent. The owner's ruling of 2026-10-20 is "yes,
 * Linux for 0.10.0, unsigned"; Windows stays out of this release (design D10:
 * the CLI has no Windows build, and signing there is a certificate the owner
 * would have to buy and hold). A Windows leg would need its own executable
 * resolution (`<appOutDir>/<productFilename>.exe`) and its own signing story,
 * and neither is decided yet -- so the hook refuses it rather than flipping a
 * path it has never been run against.
 */
export const SUPPORTED_PLATFORMS: readonly string[] = Object.freeze(['darwin', 'linux'])

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
 * macExecutable is the path `@electron/fuses` is given:
 * `<appOutDir>/<productFilename>.app/Contents/MacOS/<productFilename>`.
 *
 * It mirrors electron-builder's own resolution rather than inventing one. In
 * app-builder-lib, `AppInfo.productFilename` is `sanitizeFileName(productName)`
 * unless a platform-specific `executableName` overrides it (nothing here does),
 * and `addElectronFuses` joins it to `appOutDir` with the `.app` extension on
 * darwin. The bundle and the executable are therefore both named by
 * `package.json`'s `productName`, which is `Fugaro Console` -- the name Check
 * 59a's launch line and the window's own title depend on.
 *
 * **It is not the file the fuse bytes land in**, and the pinned package's own
 * resolution is why: given this path on darwin, `flipFuses` flips the binary
 * inside `<productFilename>.app/Contents/Frameworks/Electron Framework.framework`
 * (the one Electron's code signature covers), which is the path it resolves
 * relative to this one. This function names the entry point the library takes,
 * not the byte it writes; `fuses.test.ts` drives the real package over a bundle
 * layout and records that.
 */
export function macExecutable(context: AfterPackContext): string {
  const name = context.packager.appInfo.productFilename
  return join(context.appOutDir, `${name}.app`, 'Contents', 'MacOS', name)
}

/**
 * linuxExecutable is the path `@electron/fuses` is given on Linux:
 * `<appOutDir>/<executableName>` -- there is no bundle, no `.app` and no
 * `Contents/MacOS`, just the one ELF the packer wrote into `linux-unpacked`.
 *
 * It mirrors electron-builder's resolution exactly. In app-builder-lib,
 * `LinuxPackager`'s constructor sets `executableName` to `sanitizeFileName` of
 * the configured `linux.executableName`, or to
 * `appInfo.sanitizedName.toLowerCase()` when none is configured; and
 * `addElectronFuses` joins `appOutDir` to it with an empty extension on linux
 * (`{ darwin: '.app', mas: '.app', win32: '.exe', linux: '' }`), which is the
 * same convention electron-builder's own fuse support uses when
 * `build.electronFuses` is set -- a setting this build deliberately does not
 * use, so that there is one flipper and it is this file.
 *
 * The build sets `linux.executableName` to `fugaro-console`, so the fallback
 * below is not what names the shipped binary; it is here so that a
 * configuration edit that dropped the key flips the file the packer actually
 * wrote rather than a path this hook made up. A context that carries neither
 * name is refused rather than guessed at: `join(appOutDir, '')` is a directory,
 * and `flipFuses` on a directory is not a flip.
 */
export function linuxExecutable(context: AfterPackContext): string {
  const name = context.packager.executableName ?? context.packager.appInfo.sanitizedName?.toLowerCase()
  if (name === undefined || name === '') {
    throw new Error(
      'electron-builder handed the fuse hook a linux context with no executable name ' +
        '(neither packager.executableName nor packager.appInfo.sanitizedName): the hook has ' +
        'no file to flip, and a build that flipped nothing while reporting success is the ' +
        'one outcome it must not produce',
    )
  }
  return join(context.appOutDir, name)
}

/**
 * fuseTarget is the executable the fuses are written into, resolved per
 * platform **in one place**: `afterPack` calls this and nothing else, so there
 * is one table of "where the binary is on this platform" rather than one per
 * call site. A platform that is not in `SUPPORTED_PLATFORMS` is refused here,
 * naming the platform and the ones that are packaged -- not skipped, because a
 * skipped flip is a binary that ships with the fuses unset.
 */
export function fuseTarget(context: AfterPackContext): string {
  switch (context.electronPlatformName) {
    case 'darwin':
      return macExecutable(context)
    case 'linux':
      return linuxExecutable(context)
    default:
      throw new Error(
        `the Fugaro console is packaged for ${SUPPORTED_PLATFORMS.join(' and ')}, and the fuse ` +
          `hook was called for ${context.electronPlatformName}: this build produces no binary on ` +
          'that platform to flip, and shipping one with the fuses unset is not an option this ' +
          'build may take',
      )
  }
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
 * The build is universal (`--universal`), and electron-builder 26 calls this
 * hook three times for it: once per architecture on each thin app, and once on
 * the merged app before signing. Each thin binary is therefore flipped and read
 * back on its own, and the merged binary is flipped again (`flipFuses` writes
 * every slice of a fat binary) and read back before the packer signs it.
 *
 * **The same six fuses, on both platforms this build packages.** macOS
 * (`darwin`) and Linux are the two, and the set is a property of the
 * *application* rather than of the platform, so the configuration below is
 * identical on both and there is no per-platform fuse table. On darwin the
 * library resolves the file it writes from `Contents/MacOS/<name>` to the
 * Electron Framework binary inside the bundle; on Linux the path is the
 * executable itself, because that is the ELF the fuse bytes live in. One of the
 * six -- `EnableEmbeddedAsarIntegrityValidation` -- is enforced by Electron on
 * macOS only; it is set on Linux anyway, because a fuse set that differed by
 * platform would be a second policy to keep in step, and because an
 * unenforced-but-set fuse is the state a later Electron that starts enforcing
 * it needs.
 *
 * A platform that is not packaged is a hard failure rather than a skip (see
 * `fuseTarget`): the one outcome that must not be possible is a binary that
 * ships with the fuses unset because the hook decided to do nothing.
 *
 * Nothing is logged on success. The hook's output is the packer's log at pack
 * time, and a "flipped the fuses" line in it would be a claim rather than the
 * check -- the check is the wire read below, and its failure is the message.
 */
export async function afterPack(
  context: AfterPackContext,
  fuses: FuseTools = ELECTRON_FUSES,
): Promise<void> {
  const executable = fuseTarget(context)
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
  // The wire's length is the binary's own count of its fuses. A wire longer
  // than this file accounts for carries a fuse nobody decided about (an
  // Electron bump), and the build stops here rather than signing it.
  const wireLength = Object.keys(wire).filter((key) => key !== 'version').length
  if (wireLength !== ACCOUNTED_FUSE_COUNT) {
    throw new Error(
      `the fuse wire in ${executable} holds ${wireLength} fuses and src/main/fuses.ts decides ` +
        `about ${ACCOUNTED_FUSE_COUNT}: every fuse the binary carries must be flipped, left alone ` +
        'or listed beyond the pin, with its reason, before the binary is signed',
    )
  }
  // "Left alone" is checked by value, not only by count: a left-alone fuse
  // whose state is not the default this file records is a binary this file was
  // not written against (an Electron bump that changed a default, or a
  // distribution someone else flipped).
  const byIndex = wire as unknown as Record<number, FuseState | undefined>
  const untouched = [
    ...LEFT_ALONE_FUSES.map((fuse) => ({ name: fuse.name, index: fuse.option as number, upstreamDefault: fuse.upstreamDefault })),
    ...BEYOND_PIN_FUSES,
  ]
  for (const fuse of untouched) {
    const expected = fuse.upstreamDefault ? FuseState.ENABLE : FuseState.DISABLE
    const actual = byIndex[fuse.index]
    if (actual !== expected) {
      throw new Error(
        `the ${fuse.name} fuse is ${wireState(actual)} in ${executable}, and this build leaves it ` +
          `at Electron's default (${fuse.upstreamDefault ? 'enabled' : 'disabled'}): the binary is ` +
          'not the one this file was written against',
      )
    }
  }
}
