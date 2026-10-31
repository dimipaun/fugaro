// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The fuses the console's shipped binary is built with (desktop-console plan
// Task 4.1; design section 5.1 rule 4), and the packaging configuration they
// belong to.
//
// A fuse is not a setting. `ELECTRON_RUN_AS_NODE=1` turns an Electron binary
// into a Node interpreter for anyone who can execute the file, `NODE_OPTIONS`
// lets the process's own environment inject code into it, and `--inspect` hands
// it to a debugger -- and every one of those is a *build-time* byte in the
// binary, so the only place to refuse them is here, at pack time. Six fuses are
// flipped and two are deliberately left at their upstream defaults, and the two
// are asserted too: "left alone" is a decision, and the plan asks for it to be
// listed rather than implied.
//
// The test has three halves, and the split is the reason it is worth having:
//
//   1. **the configuration** -- `fuseConfig()` is asserted fuse by fuse, with a
//      named test each, so re-enabling `RunAsNode` cannot arrive as a packaging
//      detail in a diff nobody reads;
//   2. **the hook that applies it** -- `afterPack` is driven with a fake
//      `flipFuses`/`getCurrentFuseWire`, so what the packer will call is a fact
//      here rather than something the packer is trusted to do: the config it
//      passes, the binary it names on each platform this build packages
//      (darwin's `.app` and Linux's bare executable), its refusal on a platform
//      it does not package, and the check that reads the wire back afterwards;
//   3. **the rest of the build configuration** -- asar on, the files allow-list,
//      `publish: null` and `--publish never`, the exact set of keys the build
//      object may have, the pinned packer the `package` script runs, and the
//      names Check 59a's launch line depends on.
//
// **Why the wire is read back.** `flipFuses` reports how many fuses it changed,
// not what the binary now holds, so a table entry carrying the index of the fuse
// above it flips the wrong byte with nothing from the packer to say so. The hook
// flips and then reads the wire and fails, naming the fuse and the path, rather
// than trusting the write. The fake below is what proves that check can fail:
// one wire with `RunAsNode` left enabled, which is the plan's red step's second
// half. (It is not a guard against a path that is not an Electron binary -- the
// pinned package refuses that itself, which the last test in the pinned-package
// block asserts.)
//
// **And, where it is installed, the real binary.** When
// `node_modules/electron/dist` is present (it is not after `npm ci
// --ignore-scripts` until `node node_modules/electron/install.js` has run), the
// last block reads the pinned Electron's own fuse wire and, on macOS, runs the
// real hook over a copy of the distribution in a temporary directory: every
// flipped fuse, every untouched one, and the wire's length against the count
// `src/main/fuses.ts` accounts for. Without the binary it skips, and says why.
// The signature those fuses sit inside is verified on a Mac, by the owner, in
// Check 59a.

import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { FuseState, FuseV1Options, FuseVersion } from '@electron/fuses'
import { describe, expect, it } from 'vitest'

import {
  ACCOUNTED_FUSE_COUNT,
  BEYOND_PIN_FUSES,
  FLIPPED_FUSES,
  LEFT_ALONE_FUSES,
  SUPPORTED_PLATFORMS,
  afterPack,
  fuseConfig,
  fuseTarget,
  linuxExecutable,
  macExecutable,
  type AfterPackContext,
  type FuseTools,
} from '../../src/main/fuses'
import { SERVED_FILES } from '../../src/main/protocol'

const PACKAGE_JSON = fileURLToPath(new URL('../../package.json', import.meta.url))
const BUILD_CONFIG = fileURLToPath(new URL('../../tsconfig.build.json', import.meta.url))
const LOCKFILE = fileURLToPath(new URL('../../package-lock.json', import.meta.url))
const CONSOLE_DIR = fileURLToPath(new URL('../../', import.meta.url))
const ELECTRON_PACKAGE = fileURLToPath(new URL('../../node_modules/electron/', import.meta.url))

const manifest = JSON.parse(readFileSync(PACKAGE_JSON, 'utf8')) as {
  readonly description?: string
  readonly scripts: Record<string, string>
  readonly devDependencies: Record<string, string>
  readonly dependencies?: Record<string, string>
  readonly build: Record<string, unknown>
}

const build = manifest.build as {
  readonly appId: string
  readonly productName: string
  readonly asar?: unknown
  readonly files?: readonly string[]
  readonly afterPack?: string
  readonly directories?: { readonly output?: string }
  readonly electronFuses?: unknown
  readonly publish?: unknown
  readonly npmRebuild?: unknown
  readonly electronDownload?: { readonly checksums?: Record<string, string> }
  readonly mac?: Record<string, unknown>
  readonly linux?: Record<string, unknown>
}

/** flips is the configuration's value per fuse, keyed by the enum, with the
 * names from the enum itself so a test's failure sentence names the fuse rather
 * than a number. */
const flips = new Map<FuseV1Options, boolean>(
  FLIPPED_FUSES.map((entry) => [entry.option, entry.value] as const),
)

/** A fake for the two `@electron/fuses` calls the hook makes. It records what it
 * was asked to do, so the test asserts the call rather than the outcome the call
 * was supposed to produce -- and it can be told to return a wire that is *wrong*,
 * which is what makes the hook's own verification checkable. */
function fakeFuses(wire?: Partial<Record<FuseV1Options, FuseState>>): FuseTools & {
  readonly calls: { readonly path: string; readonly config: unknown }[]
} {
  const calls: { path: string; config: unknown }[] = []
  return {
    calls,
    async flipFuses(path: string, config: unknown): Promise<number> {
      calls.push({ path, config })
      return 0
    },
    async getCurrentFuseWire(path: string) {
      const flipped: Record<number, FuseState> = {}
      // The untouched fuses at the states the pinned Electron 45.0.0 ships them
      // in (read off the real binary: cookie encryption and the browser-process
      // snapshot off, WasmTrapHandlers on), so the fake wire is as long as the
      // real one.
      const untouched: Record<number, FuseState> = {
      }
      for (const fuse of LEFT_ALONE_FUSES) {
        untouched[fuse.option] = fuse.upstreamDefault ? FuseState.ENABLE : FuseState.DISABLE
      }
      for (const fuse of BEYOND_PIN_FUSES) {
        untouched[fuse.index] = fuse.upstreamDefault ? FuseState.ENABLE : FuseState.DISABLE
      }
      for (const [option, value] of flips) {
        flipped[option] = value ? FuseState.ENABLE : FuseState.DISABLE
      }
      return { version: FuseVersion.V1, ...untouched, ...flipped, ...wire }
    },
  } as FuseTools & { readonly calls: { readonly path: string; readonly config: unknown }[] }
}

/** CONTEXT is what electron-builder hands an `afterPack` hook for a mac build.
 * `productFilename` is the bundle name electron-builder derived from
 * `productName` (see the assertion on the executable's path below), and it is
 * the whole of the context this hook reads. */
const CONTEXT: AfterPackContext = {
  electronPlatformName: 'darwin',
  appOutDir: '/tmp/out/mac-universal',
  packager: { appInfo: { productFilename: 'Fugaro Console' } },
}

/** LINUX_CONTEXT is the same context for a Linux build. There is no bundle on
 * Linux: electron-builder's `LinuxPackager` writes one bare executable named by
 * its own `executableName` (falling back to the sanitized package name), and
 * `appOutDir` is `release/linux-unpacked`. Both names are carried here because
 * the hook reads whichever the packager set. */
const LINUX_CONTEXT: AfterPackContext = {
  electronPlatformName: 'linux',
  appOutDir: '/tmp/out/linux-unpacked',
  packager: {
    appInfo: { productFilename: 'Fugaro Console', sanitizedName: 'fugaro-console' },
    executableName: 'fugaro-console',
  },
}

describe('the six fuses the console is built with', () => {
  it('flips exactly six, and each of the six for its own reason', () => {
    expect(FLIPPED_FUSES).toHaveLength(6)
    for (const entry of FLIPPED_FUSES) expect(entry.why.trim().length).toBeGreaterThan(20)
  })

  it('leaves RunAsNode disabled: the binary is never a Node interpreter', () => {
    expect(flips.get(FuseV1Options.RunAsNode)).toBe(false)
  })

  it('leaves EnableNodeOptionsEnvironmentVariable disabled: NODE_OPTIONS cannot reach the console', () => {
    expect(flips.get(FuseV1Options.EnableNodeOptionsEnvironmentVariable)).toBe(false)
  })

  it('leaves EnableNodeCliInspectArguments disabled: --inspect cannot attach a debugger', () => {
    expect(flips.get(FuseV1Options.EnableNodeCliInspectArguments)).toBe(false)
  })

  it('enables EnableEmbeddedAsarIntegrityValidation: the app is loaded only if its asar is intact', () => {
    expect(flips.get(FuseV1Options.EnableEmbeddedAsarIntegrityValidation)).toBe(true)
  })

  it('enables OnlyLoadAppFromAsar: the app is loaded from the asar and nowhere else', () => {
    expect(flips.get(FuseV1Options.OnlyLoadAppFromAsar)).toBe(true)
  })

  it('leaves GrantFileProtocolExtraPrivileges disabled: file:// keeps no extra privileges', () => {
    expect(flips.get(FuseV1Options.GrantFileProtocolExtraPrivileges)).toBe(false)
  })
})

describe('the three fuses left at their upstream default', () => {
  it('leaves EnableCookieEncryption alone', () => {
    expect(flips.has(FuseV1Options.EnableCookieEncryption)).toBe(false)
    expect(LEFT_ALONE_FUSES.map((entry) => entry.option)).toContain(
      FuseV1Options.EnableCookieEncryption,
    )
  })

  it('leaves LoadBrowserProcessSpecificV8Snapshot alone', () => {
    expect(flips.has(FuseV1Options.LoadBrowserProcessSpecificV8Snapshot)).toBe(false)
    expect(LEFT_ALONE_FUSES.map((entry) => entry.option)).toContain(
      FuseV1Options.LoadBrowserProcessSpecificV8Snapshot,
    )
  })

  it('leaves WasmTrapHandlers alone, at its enabled default', () => {
    expect(flips.has(FuseV1Options.WasmTrapHandlers)).toBe(false)
    expect(LEFT_ALONE_FUSES.map((entry) => entry.option)).toContain(FuseV1Options.WasmTrapHandlers)
  })

  it('accounts for every fuse this pin knows, so an Electron bump that adds one fails here', () => {
    // The pin is what makes the count a fact: `@electron/fuses` 2.1.3 knows
    // nine V1 fuses, six are flipped, three are named above. A newer Electron
    // with a tenth arrives with this test red rather than with a fuse nobody
    // decided about.
    const known = Object.values(FuseV1Options).filter(
      (value): value is FuseV1Options => typeof value === 'number',
    )
    expect(known).toHaveLength(9)
    const decided = new Set<number>([
      ...FLIPPED_FUSES.map((entry) => entry.option),
      ...LEFT_ALONE_FUSES.map((entry) => entry.option),
    ])
    expect(known.filter((option) => !decided.has(option))).toEqual([])
  })

  it('carries no fuse beyond the package: the pinned Electron’s nine are all named by it', () => {
    // Electron 45.0.0's wire holds nine fuses and `@electron/fuses` 2.1.3 names
    // all nine, so nothing is left to the beyond-the-pin list.
    expect(BEYOND_PIN_FUSES).toEqual([])
    expect(ACCOUNTED_FUSE_COUNT).toBe(9)
  })
})

describe('the pinned @electron/fuses the hook is written against', () => {
  // `src/main/fuses.ts` is the console's CommonJS build, and its import of this
  // ES module carries a `@ts-ignore` for the module-format rule (TS1479). That
  // suppression means `tsc` would not flag an API change, so the checks that
  // would have caught one live here, against the same package the packer loads.
  it('exports the two calls the hook makes', async () => {
    const fuses = await import('@electron/fuses')
    expect(typeof fuses.flipFuses).toBe('function')
    expect(typeof fuses.getCurrentFuseWire).toBe('function')
  })

  it('knows every fuse this build names, at the index this build names it at', async () => {
    // The table carries both the name and the enum member, so the two have to
    // agree; the mutation this refuses is a copy-pasted entry whose index
    // belongs to the fuse above it, which would otherwise flip the wrong byte.
    const { FuseV1Options: pinned } = await import('@electron/fuses')
    const byName = pinned as unknown as Record<string, number | string>
    for (const entry of [...FLIPPED_FUSES, ...LEFT_ALONE_FUSES]) {
      expect(byName[entry.name]).toBe(entry.option)
    }
  })

  it('still spells the wire states the hook verifies against', async () => {
    const fuses = await import('@electron/fuses')
    expect(fuses.FuseState.ENABLE).toBe(FuseState.ENABLE)
    expect(fuses.FuseState.DISABLE).toBe(FuseState.DISABLE)
    expect(fuses.FuseVersion.V1).toBe(FuseVersion.V1)
  })

  it('refuses a file that is not an Electron binary, rather than succeeding quietly', async () => {
    // The hook reads the wire back because a flip can land on the wrong byte,
    // *not* because this package would return happily having changed nothing --
    // and the difference is a fact about the pin, so it is asserted here. A
    // version that started resolving on a file with no fuse wire would leave
    // `afterPack` unable to tell a hardened binary from an untouched one, and
    // this test is where that arrives.
    const fuses = await import('@electron/fuses')
    const notABinary = join(mkdtempSync(join(tmpdir(), 'fuses-')), 'not-electron')
    writeFileSync(notABinary, 'the console is not an Electron binary')
    for (const call of [
      () => fuses.flipFuses(notABinary, { version: FuseVersion.V1, 0: false }),
      () => fuses.getCurrentFuseWire(notABinary),
    ]) {
      await expect(call()).rejects.toThrow(/sentinel/)
    }
  })
})

describe('the configuration the packer will pass', () => {
  it('is the six flips and nothing else: no other fuse is named and no other option is set', () => {
    const config = fuseConfig()
    expect(Object.keys(config).sort()).toEqual(
      ['version', ...[...flips.keys()].map(String)].sort(),
    )
    expect(config.version).toBe(FuseVersion.V1)
  })

  it('carries each flip at its required value', () => {
    const config = fuseConfig() as Record<string, unknown>
    for (const [option, value] of flips) expect(config[String(option)]).toBe(value)
  })

  it('does not reset the ad-hoc darwin signature: electron-builder signs after this hook', () => {
    // `resetAdHocDarwinSignature` would strip the signature the Electron
    // distribution arrives with. The packer re-signs the bundle after the
    // `afterPack` hook, which is the same order electron-builder's own fuse
    // support uses, so the reset is not needed -- and turning it on is a
    // decision this test refuses to let in unstated.
    expect(fuseConfig()).not.toHaveProperty('resetAdHocDarwinSignature')
  })
})

describe('the afterPack hook', () => {
  it('names the app bundle electron-builder produced, by productFilename', () => {
    expect(macExecutable(CONTEXT)).toBe(
      join(CONTEXT.appOutDir, 'Fugaro Console.app', 'Contents', 'MacOS', 'Fugaro Console'),
    )
  })

  it('flips the fuses in that binary, with the configuration above', async () => {
    const fuses = fakeFuses()
    await afterPack(CONTEXT, fuses)
    expect(fuses.calls).toHaveLength(1)
    expect(fuses.calls[0]?.path).toBe(macExecutable(CONTEXT))
    expect(fuses.calls[0]?.config).toEqual(fuseConfig())
  })

  it('reads the wire back, and fails naming the fuse a flip did not take', async () => {
    // The mutation: a binary whose RunAsNode fuse is still on, which is exactly
    // what a flip against the wrong path looks like. The hook must not report
    // success for it.
    const fuses = fakeFuses({ [FuseV1Options.RunAsNode]: FuseState.ENABLE })
    await expect(afterPack(CONTEXT, fuses)).rejects.toThrow(/RunAsNode/)
    await expect(afterPack(CONTEXT, fuses)).rejects.toThrow(/Fugaro Console/)
  })

  it('fails naming a fuse that came back removed rather than disabled', async () => {
    const fuses = fakeFuses({ [FuseV1Options.OnlyLoadAppFromAsar]: FuseState.REMOVED })
    await expect(afterPack(CONTEXT, fuses)).rejects.toThrow(/OnlyLoadAppFromAsar/)
  })

  it('passes when the wire holds all six', async () => {
    await expect(afterPack(CONTEXT, fakeFuses())).resolves.toBeUndefined()
  })

  it('refuses a binary whose wire carries a fuse nobody decided about', async () => {
    // An Electron bump that adds a tenth fuse: the flips all took, and the
    // hook still stops the build before the packer signs it.
    const fuses = fakeFuses({ 9: FuseState.ENABLE } as Partial<Record<FuseV1Options, FuseState>>)
    await expect(afterPack(CONTEXT, fuses)).rejects.toThrow(/holds 10 fuses .* about 9/)
  })

  it('refuses a binary whose left-alone fuse is not at the default this file records', async () => {
    // The left-alone fuses are checked by value, not only by count.
    expect(LEFT_ALONE_FUSES.map((fuse) => [fuse.name, fuse.upstreamDefault])).toEqual([
      ['EnableCookieEncryption', false],
      ['LoadBrowserProcessSpecificV8Snapshot', false],
      ['WasmTrapHandlers', true],
    ])
    for (const fuse of LEFT_ALONE_FUSES) {
      const flipped = fakeFuses({ [fuse.option]: fuse.upstreamDefault ? FuseState.DISABLE : FuseState.ENABLE })
      await expect(afterPack(CONTEXT, flipped), fuse.name).rejects.toThrow(new RegExp(fuse.name))
    }
  })

  it('refuses a binary whose WasmTrapHandlers fuse is not at the default this file records', async () => {
    const fuses = fakeFuses({ 8: FuseState.DISABLE } as Partial<Record<FuseV1Options, FuseState>>)
    await expect(afterPack(CONTEXT, fuses)).rejects.toThrow(/WasmTrapHandlers/)
  })

  it('does not report success for a bundle whose executable carries no fuse wire', async () => {
    // The hook with its real toolbox (the default argument), pointed at a bundle
    // layout whose files are not an Electron binary. This is the shape of a pack
    // that went wrong somewhere else -- and the failure has to come from the
    // pinned package rather than be swallowed into a return value: the hook must
    // not report a hardened build it did not produce.
    //
    // Both files are written because `@electron/fuses` does not stop at the
    // executable on darwin: given `Contents/MacOS/<name>` it flips the binary
    // inside `Contents/Frameworks/Electron Framework.framework`, which is the
    // one Electron's Code Signature actually covers. That is a fact about the
    // path the hook names, and it is why the file below is the second one
    // written here rather than the only one.
    const appOutDir = mkdtempSync(join(tmpdir(), 'fuses-bundle-'))
    const name = 'Fugaro Console'
    const executable = join(appOutDir, `${name}.app`, 'Contents', 'MacOS', name)
    const framework = join(
      appOutDir,
      `${name}.app`,
      'Contents',
      'Frameworks',
      'Electron Framework.framework',
      'Electron Framework',
    )
    for (const file of [executable, framework]) {
      mkdirSync(dirname(file), { recursive: true })
      writeFileSync(file, 'the packed file, but not an Electron binary')
    }
    const context: AfterPackContext = { ...CONTEXT, appOutDir }
    await expect(afterPack(context)).rejects.toThrow(/sentinel/)
  })

  it('names the Linux executable electron-builder produced, by its executableName', () => {
    // There is no `.app` and no `Contents/MacOS` on Linux: the packer writes one
    // executable into `appOutDir`, named by `LinuxPackager.executableName`, and
    // that is the file the fuses go into. The path here is the packer's own
    // resolution (`path.join(appOutDir, executableName)`), not one this file
    // invented -- see `addElectronFuses` in app-builder-lib.
    expect(linuxExecutable(LINUX_CONTEXT)).toBe(join(LINUX_CONTEXT.appOutDir, 'fugaro-console'))
  })

  it('falls back to the packager’s sanitized name when no executableName is configured', () => {
    // electron-builder's own default: `LinuxPackager` uses
    // `appInfo.sanitizedName.toLowerCase()` when `linux.executableName` is not
    // set. The build sets it, and this mirrors the fallback anyway, so a
    // configuration edit that dropped the key would flip the right file rather
    // than a path this hook guessed.
    const context: AfterPackContext = {
      ...LINUX_CONTEXT,
      packager: { appInfo: { productFilename: 'Fugaro Console', sanitizedName: 'Fugaro-Console' } },
    }
    expect(linuxExecutable(context)).toBe(join(context.appOutDir, 'fugaro-console'))
  })

  it('refuses a Linux context that names no binary, rather than guessing a path to flip', async () => {
    // Neither `executableName` nor `sanitizedName`: there is no file this hook
    // could honestly name, and flipping nothing while reporting success is the
    // outcome the whole hook exists to prevent.
    const fuses = fakeFuses()
    const nameless: AfterPackContext = {
      ...LINUX_CONTEXT,
      packager: { appInfo: { productFilename: 'Fugaro Console' } },
    }
    await expect(afterPack(nameless, fuses)).rejects.toThrow(/executable name/)
    expect(fuses.calls).toEqual([])
  })

  it('flips the same six fuses into the Linux executable, with the same configuration', async () => {
    // The whole point of the Linux half: the fuse set is a property of the
    // application, not of the platform, so the binary linux-unpacked holds is
    // flipped with byte-for-byte the configuration the macOS bundle gets.
    const fuses = fakeFuses()
    await afterPack(LINUX_CONTEXT, fuses)
    expect(fuses.calls).toHaveLength(1)
    expect(fuses.calls[0]?.path).toBe(linuxExecutable(LINUX_CONTEXT))
    expect(fuses.calls[0]?.config).toEqual(fuseConfig())
    expect(Object.keys(fuses.calls[0]?.config as object)).toHaveLength(FLIPPED_FUSES.length + 1)
  })

  it('reads the Linux wire back too, and fails naming the fuse a flip did not take', async () => {
    // The same verification as on darwin, on the Linux path: an ELF whose
    // RunAsNode fuse is still on must stop the build before the packer packages
    // the directory into an AppImage or a deb.
    const fuses = fakeFuses({ [FuseV1Options.RunAsNode]: FuseState.ENABLE })
    await expect(afterPack(LINUX_CONTEXT, fuses)).rejects.toThrow(/RunAsNode/)
    await expect(afterPack(LINUX_CONTEXT, fuses)).rejects.toThrow(/fugaro-console/)
  })

  it('refuses a platform this build does not package, rather than shipping an unflipped binary', async () => {
    // Windows is the platform this build does not produce (design D10: the CLI
    // has no Windows build, and signing is the owner's). A build on it would
    // produce a binary this hook has no path for, and silently skipping would
    // be the one outcome that ships the fuses unset.
    expect([...SUPPORTED_PLATFORMS]).toEqual(['darwin', 'linux'])
    const fuses = fakeFuses()
    const elsewhere: AfterPackContext = { ...CONTEXT, electronPlatformName: 'win32' }
    await expect(afterPack(elsewhere, fuses)).rejects.toThrow(/win32/)
    // and the message names what *is* packaged, so the refusal is actionable
    await expect(afterPack(elsewhere, fuses)).rejects.toThrow(/darwin and linux/)
    expect(fuses.calls).toEqual([])
    expect(() => fuseTarget(elsewhere)).toThrow(/win32/)
  })
})

describe('the packaging configuration the fuses belong to', () => {
  it('packs an asar, which the integrity and only-load fuses need', () => {
    expect(build.asar).toBe(true)
  })

  it('ships the compiled main process, the document the window loads, and nothing else', () => {
    // The served root is `dist/bundle` (see `rendererRoot` in
    // src/main/index.ts, Task 3.4a): `npm run build` writes the scripts and the
    // stylesheet, copies the documents, elkjs's worker and the redesign's seven
    // vendored fonts there, and those sixteen files -- `SERVED_FILES` in
    // src/main/protocol.ts -- are listed by
    // name, or a packaged console opens a window with no page in it. The
    // preloads are named exactly: the dashboard window loads
    // `dist/preload/index.js`, the picker's window
    // `dist/preload/installations.js` (Task 3.4b) and the launch confirmation
    // window `dist/preload/confirm.js` (V2.5.1a part 2b-1), and nothing else
    // under that directory ships. Only `.js`, the documents, the served
    // stylesheet (Task
    // 6.2, which every document links), the worker's notices file
    // (`THIRD_PARTY_NOTICES.md`, GV2.2) and the console's own licence
    // (`LICENSE.md`, D311) are shipped, so no source map is;
    // `dist/renderer` is the bundler's input, not served, so it
    // is not shipped; and the two compiled files the app never loads -- the
    // packing hook (it needs a devDependency) and the fixture loader (tests and
    // the recorder) -- are excluded by name.
    expect(build.files).toEqual([
      'LICENSE.md',
      'THIRD_PARTY_NOTICES.md',
      'dist/main/**/*.js',
      '!dist/main/fuses.js',
      'dist/preload/index.js',
      'dist/preload/installations.js',
      'dist/preload/confirm.js',
      'dist/shared/**/*.js',
      'dist/source/**/*.js',
      '!dist/source/fixture.js',
      'dist/bundle/confirm.html',
      'dist/bundle/confirm.js',
      'dist/bundle/console.css',
      'dist/bundle/console.js',
      'dist/bundle/elk-worker.min.js',
      'dist/bundle/IBMPlexMono-Medium.woff2',
      'dist/bundle/IBMPlexMono-Regular.woff2',
      'dist/bundle/IBMPlexMono-SemiBold.woff2',
      'dist/bundle/InstrumentSans-Bold.woff2',
      'dist/bundle/InstrumentSans-Medium.woff2',
      'dist/bundle/InstrumentSans-Regular.woff2',
      'dist/bundle/InstrumentSans-SemiBold.woff2',
      'dist/bundle/index.html',
      'dist/bundle/installations.html',
      'dist/bundle/installations.js',
      'dist/bundle/refusal.html',
    ])
  })

  it('ships every module the main process and the preload load, and neither excluded file', () => {
    // The import closure, read from the sources, is the set of compiled files
    // the app requires at runtime; each must be shipped by the list above. The
    // matcher understands exactly the two shapes the list uses (a
    // `dir/sub/**/*.js` include and an exact path, either one negated), and
    // every pattern is asserted to be one of them, so a wider glob cannot slip
    // past it.
    const patterns = build.files ?? []
    for (const pattern of patterns) {
      expect(pattern, `${pattern} is not a shape this test can read`).toMatch(
        /^!?(?:[a-z]+\/[a-z]+\/\*\*\/\*\.js|[a-z]+(?:\/[a-z]+)*\/[a-z][a-z.-]*\.(?:js|html|css)|dist\/bundle\/[A-Za-z-]+\.woff2|THIRD_PARTY_NOTICES\.md|LICENSE\.md)$/,
      )
    }
    const ships = (file: string): boolean => {
      let shipped = false
      for (const pattern of patterns) {
        const negated = pattern.startsWith('!')
        const body = negated ? pattern.slice(1) : pattern
        const matches = body.endsWith('/**/*.js')
          ? file.startsWith(body.slice(0, -'**/*.js'.length)) && file.endsWith('.js')
          : file === body
        if (matches) shipped = !negated
      }
      return shipped
    }
    const closure = new Set<string>()
    const visit = (source: string): void => {
      if (closure.has(source)) return
      closure.add(source)
      const text = readFileSync(join(CONSOLE_DIR, source), 'utf8')
      for (const match of text.matchAll(/^(import|export)\s([^'"]*?)from\s+'(\.[^']+)'|^import\s+'(\.[^']+)'/gm)) {
        if (/^type\s/.test(match[2] ?? '')) continue
        const specifier = match[3] ?? match[4] ?? ''
        visit(`${join(dirname(source), specifier).replace(/\.js$/, '')}.ts`)
      }
    }
    visit('src/main/index.ts')
    visit('src/preload/index.ts')
    visit('src/preload/installations.ts')
    expect(closure.size, 'the closure walk found nothing: the regex no longer reads the imports').toBeGreaterThan(2)
    const missing = [...closure]
      .map((source) => source.replace(/^src\//, 'dist/').replace(/\.ts$/, '.js'))
      .filter((file) => !ships(file))
    expect(missing, `loaded at runtime but not shipped: ${missing.join(', ')}`).toEqual([])
    for (const served of SERVED_FILES) {
      expect(ships(`dist/bundle${served}`), `dist/bundle${served} is served but not shipped`).toBe(true)
    }
    for (const dead of [
      'src/renderer/index.html',
      'dist/main/fuses.js',
      'dist/source/fixture.js',
      'dist/renderer/app.js',
      'dist/main/index.js.map',
    ]) {
      expect(ships(dead), `${dead} must not ship`).toBe(false)
    }
  })

  it('ships no source map', () => {
    // The build emits them (tsconfig.build.json sets sourceMap), and a map in
    // the asar is a full copy of the sources with a pointer to it. Every
    // include in the list ends in `.js`, names one of the `.html` documents, or
    // is the served `.css` (Task 6.2) -- so no include names a `.map`, and a
    // pattern that started to would fail here.
    for (const pattern of build.files ?? []) expect(pattern).toMatch(/\.(?:js|html|css|md|woff2)$/)
  })

  it('publishes nowhere: publish is null, not merely absent', () => {
    // Leaving `publish` out is not the same as no publishing: with GH_TOKEN or
    // GITHUB_TOKEN in the environment electron-builder infers a GitHub
    // provider, writes app-update.yml into the bundle and uploads on a CI tag.
    // `null` turns the inference off; the `--publish never` asserted on the
    // script below turns the upload off from the other side. Task 4.4's
    // auto-update is unaffected: it sets its own feed URL at runtime, so
    // publishing stays off and the feed stays this file's answer.
    expect(Object.hasOwn(build, 'publish')).toBe(true)
    expect(build.publish).toBeNull()
  })

  it('has exactly the build keys this task decided, and none of the ways to widen the bundle', () => {
    // A pinned key set, so `asarUnpack`, `extraResources`, `extraFiles`, a
    // top-level `mas` block or anything else that changes what ships or how is
    // a red test here rather than a quiet line in package.json. The doors are
    // also named, so the failure says which one was opened.
    expect(Object.keys(build).toSorted()).toEqual(
      [
        'afterPack',
        'appId',
        'asar',
        'directories',
        'electronDownload',
        'files',
        'linux',
        'mac',
        'npmRebuild',
        'productName',
        'publish',
      ].toSorted(),
    )
    for (const widening of ['asarUnpack', 'extraResources', 'extraFiles', 'mas', 'dmg', 'electronDist']) {
      expect(build, `build.${widening} must be absent`).not.toHaveProperty(widening)
    }
    expect(Object.keys(build.directories ?? {})).toEqual(['output'])
  })

  it('has exactly the mac keys this task decided', () => {
    // `icon` joined the list with the icon task (the owner's brief of
    // 2026-10-19): the bundle's application icon, from `build/icon.png`, which
    // is the website's own mark. It adds nothing to what ships -- the file
    // becomes the bundle's `icon.icns` and is not packed into the asar -- and
    // `test/unit/icon.test.ts` is where that reading is asserted; the list is
    // still pinned here so that a second icon key, or a *different* one, is a
    // red test rather than a quiet line.
    expect(Object.keys(build.mac ?? {}).toSorted()).toEqual(
      ['entitlements', 'entitlementsInherit', 'hardenedRuntime', 'icon', 'target'].toSorted(),
    )
    for (const widening of ['asarUnpack', 'extraResources', 'extraFiles', 'binaries', 'identity']) {
      expect(build.mac, `build.mac.${widening} must be absent`).not.toHaveProperty(widening)
    }
  })

  it('has exactly the linux keys this task decided', () => {
    // The owner's ruling of 2026-10-20: Linux for 0.10.0, unsigned. The keys
    // below are the ones that change *what is produced* or what the generated
    // `.desktop` and deb control file say -- `category` becomes
    // `Categories=`, `description` becomes `Comment=`, `executableName`
    // becomes `Exec=` and `Icon=`, `synopsis` becomes the deb `Description:`,
    // and `maintainer` is what the deb target refuses to build without (there
    // is no `author` with an email in this package.json, so without it the
    // `deb` leg fails at pack time rather than shipping a package with an
    // empty `Maintainer:`).
    //
    // `desktop.entry` is deliberately *not* here: electron-builder computes the
    // entry from the keys above, and it refuses an `Exec` given that way
    // outright (`computeDesktopEntry` throws "Please specify executable name as
    // linux.executableName instead of linux.desktop.Exec"), so a hand-written
    // entry would be a second, unchecked copy of the same facts.
    expect(Object.keys(build.linux ?? {}).toSorted()).toEqual(
      [
        'artifactName',
        'category',
        'description',
        'executableName',
        'icon',
        'maintainer',
        'synopsis',
        'target',
      ].toSorted(),
    )
    // None of the ways to widen the bundle on this platform either: these would
    // add files to the package (or, for `files`/`asar`, replace the very rules
    // the fuses depend on) from inside the platform block, where the top-level
    // allow-list does not reach.
    for (const widening of ['asar', 'asarUnpack', 'extraResources', 'extraFiles', 'files', 'electronDist']) {
      expect(build.linux, `build.linux.${widening} must be absent`).not.toHaveProperty(widening)
    }
  })

  it('pins what the Linux build produces: AppImage and deb, x64, and the artifact names', () => {
    // The arch is pinned in the configuration rather than on the command line,
    // so `electron-builder --linux` is the whole command the CI job runs and
    // the architecture it produced is a fact in this repository. x64 only: an
    // arm64 leg would cross-build an architecture nothing in this repository
    // has ever run, and a package nobody has launched is not one to publish.
    const linux = (build.linux ?? {}) as Record<string, unknown>
    expect(linux['target']).toEqual([
      { target: 'AppImage', arch: ['x64'] },
      { target: 'deb', arch: ['x64'] },
    ])
    // The name the release notes, the checksum file and the website's data all
    // have to agree on. `${arch}` is electron-builder's own substitution, so a
    // build that quietly produced another architecture would produce another
    // name rather than a mislabelled file.
    expect(linux['artifactName']).toBe('Fugaro.Console-${version}-linux-${arch}.${ext}')
    expect(linux['executableName']).toBe('fugaro-console')
    expect(linux['icon']).toBe('build/icon.png')
    // A freedesktop main category and an additional one: the console is the
    // GUI for a developer CLI, and what it does is monitor runs. electron-builder
    // appends the trailing `;` the spec requires.
    expect(linux['category']).toBe('Development;Monitor')
    expect(typeof linux['synopsis']).toBe('string')
    expect(typeof linux['description']).toBe('string')
    expect(linux['description']).not.toBe(manifest.description)
    expect(linux['maintainer']).toMatch(/<[^<>@\s]+@[^<>\s]+>$/)
    // The artifact name is the one the workflow's checksum file and the website
    // reader look for: it must not carry a space, so the names are the same
    // whether a tool splits on whitespace or not.
    expect(linux['artifactName']).not.toMatch(/\s/)
    // `build/icon.png` exists and is the 1024px source electron-builder turns
    // into the icons it installs; `icon.test.ts` reads it.
    expect(existsSync(join(CONSOLE_DIR, 'build', 'icon.png'))).toBe(true)
  })

  it('configures no signature for Linux: the owner ruled it unsigned, and signing is the owner’s', () => {
    // The owner's ruling of 2026-10-20 is "yes, Linux for 0.10.0, unsigned".
    // There is deliberately no code-signing key here, no `forceCodeSigning` that
    // would turn the absence into a build failure, and no `afterSign` hook: the
    // console carries no signing identity of its own (the macOS identity arrives
    // in the environment as CSC_LINK/CSC_NAME and is the owner's), and an
    // AppImage is not code-signed at all -- its integrity is the SHA-256 beside
    // the download and the Sigstore bundle the release attaches.
    const linux = (build.linux ?? {}) as Record<string, unknown>
    for (const signing of [
      'cscLink',
      'cscKeyPassword',
      'identity',
      'forceCodeSigning',
      'signAndEditExecutable',
    ]) {
      expect(linux, `build.linux.${signing} must be absent`).not.toHaveProperty(signing)
    }
    for (const signing of ['forceCodeSigning', 'afterSign', 'afterAllArtifactBuild', 'afterExtract']) {
      expect(build, `build.${signing} must be absent`).not.toHaveProperty(signing)
    }
  })

  it('packages Linux with the same pinned packer, the same asar and the same files allow-list', () => {
    // The brief's rule for this task: "the asar/`build.files` rules and the
    // CSP/fuses stay exactly as strict on Linux as on macOS". `asar` and
    // `files` are top-level and neither platform block may re-state them; this
    // asserts the command the CI job runs and that nothing on the Linux side
    // re-opens them.
    expect(manifest.scripts['package:linux']).toBe('electron-builder --linux --publish never')
    expect(manifest.scripts['package:linux']).not.toMatch(/\bnpx\b/)
    expect(manifest.scripts['package:linux']).not.toMatch(/--publish\s+(?:always|onTag|onTagOrDraft)/)
    expect(manifest.scripts['package:linux']).not.toMatch(/--(?:arm64|ia32|universal)\b/)
    expect(manifest.scripts['package:linux']).not.toMatch(/electron-builder@/)
    expect(manifest.scripts['package:linux']).not.toMatch(/--dir\b/)
    // `npm run build` is *not* in that script on purpose: the CI job builds the
    // app once and both platform legs pack the same `dist`, so a second compile
    // in the middle of a packaging step is a second chance to pack something
    // the type-checked build did not produce.
    expect(manifest.scripts['package:linux']).not.toMatch(/npm run build/)
  })

  it('has no electron-builder.env, and could not ship one', () => {
    // electron-builder's CLI loads `electron-builder.env` from the working
    // directory into its environment before it builds (cli-util.js), which would
    // put back what the README's `env -i` packaging step keeps out -- and, if it
    // held secrets, put them in a file in the checkout.
    expect(existsSync(join(CONSOLE_DIR, 'electron-builder.env'))).toBe(false)
    for (const pattern of build.files ?? []) expect(pattern).not.toMatch(/\.env$|^\*|\/\*\*$/)
  })

  it('rebuilds no native module: the console has none, and a rebuild runs install scripts', () => {
    expect(build.npmRebuild).toBe(false)
  })

  it('verifies every Electron archive it downloads against the pinned Electron’s own checksums', () => {
    // A universal build downloads the darwin arm64 and x64 Electron archives at
    // pack time, and the Linux leg downloads `electron-v<version>-linux-x64.zip`
    // on the ubuntu runner. Without inline checksums electron-builder verifies
    // them against a SHASUMS256.txt fetched from the same release host; with
    // these, against hashes in this repository, which must equal the ones the
    // lockfile-pinned `electron` package carries in its `checksums.json`
    // (present after `npm ci --ignore-scripts`: it is in the tarball).
    //
    // The list is pinned exactly, so a build that started downloading an archive
    // nobody pinned -- a third arch, a different version -- fails here rather
    // than trusting a hash fetched over the network at pack time. `linux-arm64`
    // is deliberately not in it: this build produces x64 Linux only.
    const version = manifest.devDependencies.electron
    const checksums = build.electronDownload?.checksums ?? {}
    expect(Object.keys(build.electronDownload ?? {})).toEqual(['checksums'])
    expect(Object.keys(checksums).toSorted()).toEqual(
      [
        `electron-v${version}-darwin-arm64.zip`,
        `electron-v${version}-darwin-x64.zip`,
        `electron-v${version}-linux-x64.zip`,
      ].toSorted(),
    )
    for (const hash of Object.values(checksums)) expect(hash).toMatch(/^[0-9a-f]{64}$/)
    const upstream = JSON.parse(readFileSync(join(ELECTRON_PACKAGE, 'checksums.json'), 'utf8')) as Record<
      string,
      string
    >
    for (const [file, hash] of Object.entries(checksums)) expect(hash, file).toBe(upstream[file])
  })

  it('packages with the pinned local electron-builder, universal, and never publishing', () => {
    // The packer is a devDependency recorded in the lockfile (exact version,
    // integrity hash, registry host -- lockfile.test.ts covers the whole tree),
    // and the script runs its local binary. `npx` would resolve a tree outside
    // the lockfile, with install scripts, in the shell the signing secrets are
    // exported into.
    const script = manifest.scripts.package ?? ''
    expect(script).toBe('npm run build && electron-builder --mac --universal --publish never')
    expect(script).not.toMatch(/\bnpx\b/)
    expect(script).not.toMatch(/--publish\s+(?:always|onTag|onTagOrDraft)/)
    expect(script).not.toMatch(/electron-builder@/)
    const pin = manifest.devDependencies['electron-builder']
    expect(pin).toBe('26.18.0')
    expect(manifest.dependencies ?? {}).not.toHaveProperty('electron-builder')
    const lock = JSON.parse(readFileSync(LOCKFILE, 'utf8')) as {
      packages: Record<string, { version?: string; resolved?: string; integrity?: string; dev?: boolean }>
    }
    const entry = lock.packages['node_modules/electron-builder']
    expect(entry?.version).toBe(pin)
    expect(entry?.resolved).toBe(
      `https://registry.npmjs.org/electron-builder/-/electron-builder-${pin}.tgz`,
    )
    expect(entry?.integrity).toMatch(/^sha512-/)
    expect(entry?.dev).toBe(true)
  })

  it('runs this hook, compiled to the file the build emits', () => {
    expect(build.afterPack).toBe('dist/main/fuses.js')
    // and the build does emit there: `rootDir: src`, `outDir: dist`.
    const tsconfig = readFileSync(BUILD_CONFIG, 'utf8')
    expect(tsconfig).toMatch(/"outDir":\s*"dist"/)
    expect(tsconfig).toMatch(/"rootDir":\s*"src"/)
  })

  it('does not also flip fuses through electron-builder, so there is one flipper and it is this one', () => {
    expect(build.electronFuses).toBeUndefined()
  })

  it('writes its artifacts outside dist, which is the compiler output directory', () => {
    // electron-builder's default output directory is `dist`, which is where
    // `npm run build` puts the app it is packing.
    expect(build.directories?.output).toBe('release')
  })

  it('pins the bundle name Check 59a launches by', () => {
    expect(build.productName).toBe('Fugaro Console')
  })

  it('pins an app id in a namespace the project owns, and not Electron’s', () => {
    // electron-builder's default is `com.electron.<name>`, which claims
    // upstream's namespace. This is the project's GitHub home reversed, which is
    // the convention for an application with no domain of its own -- and it is a
    // deliberate edit, because the bundle id is the identity Gatekeeper and the
    // notary record: changing it later is a different application.
    expect(build.appId).toBe('io.github.dimipaun.fugaro-console')
  })
})

// The real binary, when it is installed. `npm ci --ignore-scripts` installs the
// `electron` package without its distribution; `node
// node_modules/electron/install.js` (the README's owner step, run before any
// secret is exported) downloads it, verified against the package's own
// `checksums.json`. Where it is absent this block skips, and the skip names the
// command; where it is present -- a developer's Mac, or a CI runner that ran the
// download -- it runs.
const electronPathFile = join(ELECTRON_PACKAGE, 'path.txt')
const electronBinary = existsSync(electronPathFile)
  ? join(ELECTRON_PACKAGE, 'dist', readFileSync(electronPathFile, 'utf8').trim())
  : ''
const realBinaryAbsent = electronBinary === '' || !existsSync(electronBinary)
const SKIP_REASON =
  'skipped: node_modules/electron/dist is absent (npm ci --ignore-scripts does not download it; run `node node_modules/electron/install.js`)'

describe.skipIf(realBinaryAbsent)(`the pinned Electron binary itself${realBinaryAbsent ? ` (${SKIP_REASON})` : ''}`, () => {
  it('carries exactly as many fuses as src/main/fuses.ts accounts for', async () => {
    const { getCurrentFuseWire } = await import('@electron/fuses')
    const wire = await getCurrentFuseWire(electronBinary)
    expect(Object.keys(wire).filter((key) => key !== 'version')).toHaveLength(ACCOUNTED_FUSE_COUNT)
  })

  it.skipIf(process.platform !== 'darwin')(
    'takes the real hook on a copy: six flips, every other fuse untouched (macOS only)',
    async () => {
      const { getCurrentFuseWire } = await import('@electron/fuses')
      const app = electronBinary.slice(0, electronBinary.indexOf('.app') + '.app'.length)
      const productFilename = app.slice(app.lastIndexOf('/') + 1, -'.app'.length)
      const before = (await getCurrentFuseWire(electronBinary)) as unknown as Record<string, unknown>
      const appOutDir = mkdtempSync(join(tmpdir(), 'fuses-real-'))
      try {
        // A clone where the filesystem offers one (APFS), a copy otherwise:
        // the hook writes into the framework binary, never into node_modules.
        try {
          execFileSync('cp', ['-Rc', app, appOutDir])
        } catch {
          execFileSync('cp', ['-R', app, appOutDir])
        }
        const context: AfterPackContext = {
          electronPlatformName: 'darwin',
          appOutDir,
          packager: { appInfo: { productFilename } },
        }
        await afterPack(context)
        const after = (await getCurrentFuseWire(macExecutable(context))) as unknown as Record<string, unknown>
        const indices = Object.keys(after).filter((key) => key !== 'version')
        expect(indices).toHaveLength(ACCOUNTED_FUSE_COUNT)
        for (const fuse of FLIPPED_FUSES) {
          expect(after[String(fuse.option)], fuse.name).toBe(fuse.value ? FuseState.ENABLE : FuseState.DISABLE)
        }
        const untouched = [
          ...LEFT_ALONE_FUSES.map((fuse) => [fuse.name, fuse.option] as const),
          ...BEYOND_PIN_FUSES.map((fuse) => [fuse.name, fuse.index] as const),
        ]
        for (const [name, index] of untouched) expect(after[String(index)], name).toBe(before[String(index)])
        // and the installed distribution itself was not written to
        expect(await getCurrentFuseWire(electronBinary)).toEqual(before)
      } finally {
        rmSync(appOutDir, { recursive: true, force: true })
      }
    },
    120_000,
  )
})
