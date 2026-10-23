// The picker's home: the app's own environment's `HOME`, never Electron's
// `app.getPath('home')`.
//
// On Linux the two agree -- both read `HOME`. **On macOS they do not.** Electron
// answers `home` with Chromium's `base::GetHomeDir()`, whose Apple
// implementation is Foundation's `NSHomeDirectory()`, which reads the user
// *record* and ignores `HOME`; `homedir()` (libuv) answers with `HOME` first. So
// a launch whose `HOME` is not the account's -- every e2e launch, because
// `test/support/cliStub` hands the app a scratch home so that the stub is what
// rule 4 finds -- had the picker resolve `/Users/<owner>/.local/bin` while the
// same process's startup handshake resolved the stub in the scratch home. GitHub
// CI is where that showed: `test/e2e/installations.spec.ts`'s first-run case
// passed on Linux and failed on macOS, drawing "install the Fugaro CLI first"
// over a console that had already read a CLI, because the picker and the
// handshake disagreed about which directory `~` is.
//
// The console resolves `~` with the process's `HOME` everywhere else
// (`binDirectories` defaults to `homedir()`, `childEnvironment` hands the child
// the app environment's `HOME`), so the picker was the one exception and this
// file is what keeps it from becoming one again. **The rule itself is not here
// and does not change**: rule 4's order -- the store's override first, then the
// platform's well-known prefixes -- its list, and the refusal to walk the app's
// `PATH` are asserted in `test/unit/spawn.test.ts` and in
// `test/unit/installations-ipc.test.ts`. What is asserted here is only which
// directory the `~` in that list stands for, and that it is the environment's
// answer rather than Electron's.
//
// **What this file can and cannot show.** It drives the shipped wiring
// (`installInstallationPicker`, which is the entry point's one call) with a fake
// Electron whose `app.getPath('home')` deliberately answers something *other*
// than the environment's home -- which is macOS's divergence, reproduced on any
// platform. The three cases under "the picker's home" read it off the store
// refusal, which is decided before any binary is looked for: one for a home the
// environment names, and one for each of the two ways it can name none. The
// last case reads it off the resolution itself, which is the behaviour the e2e
// is about: it skips on a machine that has its own `fugaro` in one of the
// platform's *absolute* prefixes, because there the machine decides the answer
// before the `~` entry is reached -- the same reason, and the same honesty, as
// the e2e's own `resolveConsoleBinary` skip.

import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterAll, describe, expect, it } from 'vitest'

import { INSTALLATIONS_FILE_NAME, installationChannel } from '../../src/main/installations'
import type { ConsoleEventLike, ConsoleReply, IpcMainLike } from '../../src/main/ipc'
import { PICKER_ENTRY_URL, installInstallationPicker } from '../../src/main/picker'
import { FUGARO_BINARY_NAME, isExecutableFile, locationsFor } from '../../src/main/spawn'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import { installStub } from './stub'
import type { Stub } from './stub'

const CONSOLE_DIR = fileURLToPath(new URL('../..', import.meta.url))

/** scratch is the tree these cases own: a home for the app's environment, a home
 * for Electron to answer with, and the profile each launch writes. */
const scratch = mkdtempSync(join(tmpdir(), 'fugaro-picker-home-'))
afterAll(() => {
  rmSync(scratch, { recursive: true, force: true })
})

/** accountHome is what the fake Electron answers for `home`: **not** the
 * environment's home, which is the whole point. On a real macOS run this is
 * `NSHomeDirectory()`'s answer -- `/Users/<owner>` -- while `HOME` is whatever
 * the launching process set. Nothing is ever installed here. */
function accountHome(name: string): string {
  const directory = join(scratch, name)
  mkdirSync(directory, { recursive: true })
  return directory
}

/** installableStub puts a stub CLI in the well-known `~/.local/bin` of the given
 * home. It is the `test/unit/stub` fake, so the console can spawn it: it answers
 * the version handshake and the picker's one read (`config list --json`, with no
 * installations), and refuses everything else. */
function installableStub(envHome: string): Stub {
  return installStub(join(envHome, '.local', 'bin'), {
    commands: {
      version: { stdout: `${CLI_VERSION_BUILT_AGAINST}\n` },
      'config list': { stdout: '{"installations":[],"warnings":[]}\n' },
    },
  })
}

/** Picker is what pickerOver answers: the picker the entry point would have
 * built, and the one way to ask it something (`answer`). */
interface Picker {
  readonly refusal: string | null
  answer(name: Parameters<typeof installationChannel>[0]): Promise<ConsoleReply>
}

/** pickerOver builds the picker exactly as `src/main/index.ts` does -- through
 * `installInstallationPicker`, not through `createInstallationPicker` -- so what
 * these cases observe is the wiring that ships and not a second copy of it. The
 * Electron surface is a fake because the case under test is the *difference*
 * between Electron's answer and the environment's, and only a fake can hold that
 * difference on a machine where the two agree. */
function pickerOver(options: {
  readonly envHome: string
  readonly accountHome: string
  readonly userData: string
  readonly appEnv?: Readonly<Record<string, string | undefined>>
}): Picker {
  const listeners = new Map<string, (event: ConsoleEventLike, raw: unknown) => Promise<unknown>>()
  const ipcMain: IpcMainLike = {
    handle(channel, listener) {
      listeners.set(channel, listener as unknown as (event: ConsoleEventLike, raw: unknown) => Promise<unknown>)
    },
  }
  const electron = {
    // These cases never open a window: they read the picker's own refusal and
    // one reply, both of which are decided before a `BrowserWindow` exists. A
    // constructor that refused loudly is better than one that pretended.
    BrowserWindow: function BrowserWindow(): never {
      throw new Error('these cases open no window')
    },
    session: {},
    ipcMain,
    Menu: {},
    shell: {},
    app: {
      getPath(name: string): string {
        if (name === 'userData') return options.userData
        if (name === 'home') return options.accountHome
        throw new Error(`these cases do not describe Electron's ${name}`)
      },
      getAppPath: () => CONSOLE_DIR,
      quit: () => undefined,
    },
  } as unknown as Parameters<typeof installInstallationPicker>[0]['electron']
  const picker = installInstallationPicker({
    electron,
    appEnv: { HOME: options.envHome, ...options.appEnv },
  })
  return {
    refusal: picker.refusal,
    answer: async (name) => {
      const listener = listeners.get(installationChannel(name))
      expect(listener, name).toBeDefined()
      const frame = { url: PICKER_ENTRY_URL }
      return (await listener?.({ senderFrame: frame, sender: { mainFrame: frame } }, {})) as ConsoleReply
    },
  }
}

/** expectRefused is the assertion the three home cases below make: the picker
 * starts with no store and says why, rather than writing `installations.json`
 * into the CLI's configuration. It reads the sentence off the refusal -- the `??`
 * is so that a picker which refused nothing fails with the sentence it did not
 * print, rather than with an assertion about `null` -- and then checks that no
 * store was written. */
function expectRefused(picker: Picker, userData: string): void {
  expect(picker.refusal ?? '(nothing refused: the store was written)').toContain(
    'refuses to keep its installations',
  )
  expect(existsSync(join(userData, INSTALLATIONS_FILE_NAME))).toBe(false)
}

/** The two spellings of "this environment names no home", which are one branch in
 * the picker. The blank one is the one a case is easy to leave out: `HOME=''` is
 * not `undefined`, so a case that only drops the key cannot see whether the blank
 * was handled. */
const noHome = [
  { spelling: 'names no home at all', appEnv: { HOME: undefined } },
  { spelling: 'names a home that is empty', appEnv: { HOME: '' } },
] as const

describe('the picker’s home', () => {
  it('is the app’s own environment’s, asked of the path rule that refuses the CLI’s own config directory', () => {
    // `~/.config/fugaro` is the one entry of that rule the home decides -- the
    // other two are `$XDG_CONFIG_HOME` and `$FUGARO_CONFIG`, which come out of
    // this same environment -- so a profile *inside the environment's home's*
    // `.config/fugaro` must be refused: the console starts with no store and
    // says why (review S7) rather than write `installations.json` where the
    // operator's CLI reads. A picker that asked Electron for `home` would look
    // in the account home instead, find nothing to refuse, and go on to write
    // the file: that is the assertion below, and it fails on the code before
    // this file's reason existed.
    const envHome = accountHome('refused-home')
    const userData = join(envHome, '.config', 'fugaro', 'console')
    const picker = pickerOver({
      envHome,
      accountHome: accountHome('refused-account-home'),
      userData,
    })
    expectRefused(picker, userData)
  })

  it.each(noHome)('falls back to Electron’s answer when the environment $spelling', ({ appEnv }) => {
    // The other half of the same decision: a missing `HOME` is not a home, and
    // neither is an empty one. The blank spelling is the worse of the two
    // because it is *silently relative*: `join('', '.local', 'bin')` is
    // `.local/bin` and `join('', '.config', 'fugaro')` is `.config/fugaro`, and
    // both are then read against the directory the app happens to have been
    // started in. Nothing downstream catches that -- rule 4 pushes every `~`
    // entry into the candidate list whether or not its home made it absolute
    // (`resolveBinary` refuses a relative **configured** path, not an expansion
    // of `~`), and the refusal resolves the config entry the same way. So
    // without the fallback a launch that scrubbed `HOME` would look for the CLI
    // in `./.local/bin` and stop refusing to write into `./.config/fugaro`.
    // Electron's own answer is the home the account actually has, so it is the
    // one to fall back to.
    const account = accountHome('fallback-account-home')
    const userData = join(account, '.config', 'fugaro', 'console')
    const picker = pickerOver({
      // A home that is *not* Electron's answer, so a case that stopped reading
      // the environment -- or stopped overriding it with the spelling above --
      // fails rather than passing for the wrong reason.
      envHome: accountHome('fallback-home'),
      accountHome: account,
      userData,
      appEnv,
    })
    expectRefused(picker, userData)
  })
})

/** machineFugaro is a `fugaro` this machine has in one of the platform's own
 * **absolute** prefixes -- the ones rule 4 walks before the `~` entry the case
 * below controls. It is the guard the e2e spells as a skip: a machine that has
 * one decides the resolution before the console's own home is ever reached, so
 * a case about which `~` was used would prove nothing there. A `~` prefix is
 * skipped because after this file's change it is the app's environment home,
 * which the case sets. */
const machineFugaro = ((): string | undefined => {
  for (const prefix of locationsFor(process.platform).binaryPrefixes) {
    if (prefix.startsWith('~')) continue
    const candidate = join(prefix, FUGARO_BINARY_NAME)
    if (isExecutableFile(candidate)) return candidate
  }
  return undefined
})()

describe('the picker’s resolution of the well-known `~` prefix', () => {
  it.skipIf(machineFugaro !== undefined)(
    `finds the stub CLI in the environment home's ~/.local/bin, and asks the operator for no binary${
      machineFugaro === undefined ? '' : ` (this machine has its own ${machineFugaro})`
    }`,
    async () => {
      // This is the e2e's first-run case, in the unit suite: an unseeded profile,
      // no stored override to point rule 4 at, and a stub CLI sitting in the
      // *environment home's* `~/.local/bin`. A console that resolves that
      // directory in Electron's home finds no CLI, and the picker draws "install
      // the Fugaro CLI first"; one that resolves it where the handshake and
      // every child do finds the stub, and the invitation it draws is the
      // machine having no installation -- the state the e2e asserts.
      const envHome = accountHome('resolution-home')
      const stub = installableStub(envHome)
      const userData = join(scratch, 'resolution-profile')
      mkdirSync(userData, { recursive: true })
      const picker = pickerOver({
        envHome,
        // Electron's answer is a home with nothing in it, which is macOS's
        // divergence: `NSHomeDirectory()` where `HOME` says otherwise.
        accountHome: accountHome('resolution-account-home'),
        userData,
      })
      const listed = await picker.answer('list')
      // A refusal here is a failure too, so it is raised rather than read: the
      // assertions below are about the data reply.
      expect(listed.ok).toBe(true)
      if (!listed.ok) throw new Error(listed.reason)
      const data = listed.data as {
        readonly onboard: { readonly kind: string }
        readonly rows: readonly unknown[]
      }
      expect(data.rows).toEqual([])
      expect(data.onboard.kind).not.toBe('install-cli')
      expect(data.onboard.kind).toBe('first-installation')
      // And the CLI that answered is *this* stub, read off the stub's own
      // report: the claim is not only that some binary was found, but that the
      // one in the environment home's `~/.local/bin` was, which is what a home
      // resolved anywhere else cannot produce.
      expect(stub.calls()).toBeGreaterThan(0)
    },
  )
})
