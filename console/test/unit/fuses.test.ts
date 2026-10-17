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
//      passes, the binary it names, its refusal on a platform that is not
//      darwin, and the check that reads the wire back afterwards;
//   3. **the rest of the build configuration** -- asar on, the files allow-list,
//      no publish configuration at all, and the names Check 59a's launch line
//      depends on.
//
// **Why the wire is read back.** Flipping is a write into the binary and a
// silent no-op is possible -- run against a path that is not an Electron binary,
// `@electron/fuses` succeeds and changes nothing. So the hook flips and then
// reads the wire and fails, naming the fuse and the path, rather than trusting
// the write. The fake below is what proves that check can fail: one wire with
// `RunAsNode` left enabled, which is the plan's red step's second half.
//
// **What is deliberately not here.** No packaged macOS binary is inspected: the
// unit suite runs on Linux, where there is none, so it reads the configuration
// the packer will pass. The signature those fuses sit inside is verified on a
// Mac, by the owner, in Check 59a.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { FuseState, FuseV1Options, FuseVersion } from '@electron/fuses'
import { describe, expect, it } from 'vitest'

import {
  FLIPPED_FUSES,
  LEFT_ALONE_FUSES,
  afterPack,
  fuseConfig,
  macExecutable,
  type AfterPackContext,
  type FuseTools,
} from '../../src/main/fuses'

const PACKAGE_JSON = fileURLToPath(new URL('../../package.json', import.meta.url))
const BUILD_CONFIG = fileURLToPath(new URL('../../tsconfig.build.json', import.meta.url))

const build = JSON.parse(readFileSync(PACKAGE_JSON, 'utf8')).build as {
  readonly appId: string
  readonly productName: string
  readonly asar?: unknown
  readonly files?: readonly string[]
  readonly afterPack?: string
  readonly directories?: { readonly output?: string }
  readonly electronFuses?: unknown
  readonly publish?: unknown
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
      for (const [option, value] of flips) {
        flipped[option] = value ? FuseState.ENABLE : FuseState.DISABLE
      }
      return { version: FuseVersion.V1, ...flipped, ...wire }
    },
  } as FuseTools & { readonly calls: { readonly path: string; readonly config: unknown }[] }
}

/** CONTEXT is what electron-builder hands an `afterPack` hook for a mac build.
 * `productFilename` is the bundle name electron-builder derived from
 * `productName` (see the assertion on the executable's path below), and it is
 * the whole of the context this hook reads. */
const CONTEXT: AfterPackContext = {
  electronPlatformName: 'darwin',
  appOutDir: '/tmp/out/mac-arm64',
  packager: { appInfo: { productFilename: 'Fugaro Console' } },
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

describe('the two fuses left at their upstream default', () => {
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

  it('accounts for every fuse this pin knows, so an Electron bump that adds one fails here', () => {
    // The pin is what makes the count a fact: `@electron/fuses` 2.0.0 knows
    // eight V1 fuses, six are flipped, two are named above. A newer Electron
    // with a ninth arrives with this test red rather than with a fuse nobody
    // decided about.
    const known = Object.values(FuseV1Options).filter(
      (value): value is FuseV1Options => typeof value === 'number',
    )
    expect(known).toHaveLength(8)
    const decided = new Set<number>([
      ...FLIPPED_FUSES.map((entry) => entry.option),
      ...LEFT_ALONE_FUSES.map((entry) => entry.option),
    ])
    expect(known.filter((option) => !decided.has(option))).toEqual([])
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

  it('refuses a platform that is not darwin rather than shipping an unflipped bundle', async () => {
    // The console is a macOS application (design D4, and this MVP). A build on
    // any other platform would produce a bundle this hook cannot find, and
    // silently skipping would be the one outcome that ships the fuses unset.
    const fuses = fakeFuses()
    const elsewhere: AfterPackContext = { ...CONTEXT, electronPlatformName: 'linux' }
    await expect(afterPack(elsewhere, fuses)).rejects.toThrow(/darwin/)
    expect(fuses.calls).toEqual([])
  })
})

describe('the packaging configuration the fuses belong to', () => {
  it('packs an asar, which the integrity and only-load fuses need', () => {
    expect(build.asar).toBe(true)
  })

  it('ships the compiled main process, the document the window loads, and nothing else', () => {
    // The served root is `src/renderer` (see `rendererRoot` in
    // src/main/index.ts) -- `tsc` emits JavaScript into dist/ and copies
    // nothing, so the document has to be listed here by hand or a packaged
    // console opens a window with no page in it. This is the seam
    // tsconfig.build.json's comment hands to Task 4.1, settled by naming the
    // file rather than by moving it.
    expect(build.files).toEqual(['dist/**', 'src/renderer/**/*.html', '!**/*.map'])
  })

  it('ships no source map', () => {
    // The build emits them (tsconfig.build.json sets sourceMap), and a map in
    // the asar is a full copy of the sources with a pointer to it.
    expect(build.files).toContain('!**/*.map')
  })

  it('has no publish configuration and no updater feed: this release has no auto-update', () => {
    // Task 4.4 is the auto-update task and it is not part of this MVP. A
    // `publish` key here would make every `npm run package` upload somewhere,
    // and the feed is what Task 4.4 decides about.
    expect(build.publish).toBeUndefined()
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
