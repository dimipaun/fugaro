// The rows the installation rail is drawn from (owner's design ruling of
// 2026-10-25).
//
// The rail lists **every** installation the CLI found, readable or not, and the
// console opens the first one it *can* open -- so the two facts these tests hold
// are:
//
//   1. **what a row carries**: its own classification (`state`: the mark, whether
//      a click opens it or its issue, the sentence and the command) and the
//      checkouts the console may offer it (`checkouts`: never a temporary or
//      tool-owned path, the project's own recorded checkout first);
//   2. **which row is opened**: `firstReadableIndex` over those states, which is
//      the owner's rule -- the first readable one, in the CLI's own order, with
//      the previously chosen installation keeping its place and its mark.
//
// The project names are invented (`aurora`, `orbit`) and every path is one the
// test itself wrote.

import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  INSTALLATIONS_FILE_NAME,
  installationChannel,
  serializeInstallationFile,
} from '../../src/main/installations'
import { createInstallationPicker, PICKER_ENTRY_URL } from '../../src/main/picker'
import { DOCTOR_COMMAND, firstReadableIndex } from '../../src/main/installationstate'
import { FUGARO_INSTALL_COMMAND } from '../../src/main/onboarding'
import type { ConsoleEventLike, ConsoleReply, IpcMainLike } from '../../src/main/ipc'
import type { InstallationsListReply, InstallationsListRow } from '../../src/main/onboarding'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import { installStub } from './stub'
import type { Stub } from './stub'

function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-rail-'))
}

/** fakeIpcMain records the listeners it was handed, so a test can call one. */
function fakeIpcMain(): IpcMainLike & { readonly listeners: Map<string, (event: ConsoleEventLike, raw: unknown) => Promise<unknown>> } {
  const listeners = new Map<string, (event: ConsoleEventLike, raw: unknown) => Promise<unknown>>()
  return {
    listeners,
    handle(channel, listener) {
      listeners.set(channel, listener as unknown as (event: ConsoleEventLike, raw: unknown) => Promise<unknown>)
    },
  }
}

/** TMP is the temporary root the tests name, as the shipped app names this
 * machine's: a checkout under it is one the OS may delete, and the console must
 * never offer it. */
const TMP = '/private/var/folders/x1/T'

/** The refused config's own line, spelled the way `internal/cli/config_list.go`
 * prints it: the path first, then ` could not be read: `, then the CLI's reason. */
const CONFIG_LINE =
  '~/.config/fugaro/projects/aurora.yaml could not be read: base_images.java-services: the java-services base was removed in 0.7.0'

/** The staleness line, likewise: the CLI's own sentence with its own windows. */
const STALE_LINE = "orbit's shared config was last checked 83h2m41s ago, over the 24h0m0s the reader refreshes it within"

/** DOCUMENT is a document the CLI printed: `aurora`, whose config it could not
 * read, and `orbit`, which it read, with one ordinary checkout and one under the
 * machine's temporary directory. */
const DOCUMENT = {
  installations: [
    { name: 'aurora', gcp_project: '', region: '' },
    {
      name: 'orbit',
      gcp_project: 'orbit-482301',
      region: 'europe-west4',
      checkouts: [
        { repository: 'github/owner/orbit', path: `${TMP}/orbit-scratch`, enrolled: true, valid: true },
        { repository: 'github/owner/orbit', path: '/home/operator/src/orbit', enrolled: true, valid: true },
      ],
    },
  ],
  warnings: [CONFIG_LINE],
}

/** stubWith installs a stub `fugaro` that prints `document` for its one read. */
function stubWith(document: unknown, warnings: readonly string[] = []): Stub {
  const directory = freshDirectory()
  return installStub(join(directory, 'bin'), {
    commands: {
      version: { stdout: `${CLI_VERSION_BUILT_AGAINST}\n` },
      doctor: { stdout: '{"ok":true,"checks":[]}\n' },
      'config list': { stdout: `${JSON.stringify({ ...(document as object), warnings })}\n` },
    },
  })
}

/** store writes one `installations.json` into a fresh userData directory and
 * returns the path, with the entries given and `active` naming the first.
 *
 * **No entry carries a `binaryPath`**: a record's own override is candidate one
 * of rule 4's walk, and this machine may well have a real `fugaro` at one of the
 * well-known locations -- which would make these tests drive it instead of the
 * stub. A record with no override is rule 4's ordinary case, and the walk is the
 * stub's directory. */
function store(entries: readonly { readonly id: string; readonly project: string; readonly checkout?: string }[]): string {
  const userData = freshDirectory()
  mkdirSync(userData, { recursive: true, mode: 0o700 })
  writeFileSync(
    join(userData, INSTALLATIONS_FILE_NAME),
    serializeInstallationFile({
      version: 1,
      active: entries[0]?.id ?? null,
      installations: entries.map((entry) => ({
        id: entry.id,
        project: entry.project,
        ...(entry.checkout === undefined ? {} : { checkout: entry.checkout }),
      })),
    }),
    { mode: 0o600 },
  )
  return userData
}

/** over builds a picker whose rule 4 walk is the stub's directory alone, with the
 * temporary root this test names. */
function over(options: { readonly userData: string; readonly stub?: Stub }) {
  const ipcMain = fakeIpcMain()
  const picker = createInstallationPicker({
    BrowserWindow: function BrowserWindow() {
      throw new Error('this test never opens a window')
    } as never,
    session: { setPermissionRequestHandler: () => undefined, setPermissionCheckHandler: () => undefined } as never,
    ipcMain,
    menu: { buildFromTemplate: (template) => template, setApplicationMenu: () => undefined },
    openExternal: async () => true,
    userData: options.userData,
    home: freshDirectory(),
    prefixes: options.stub === undefined ? [] : [options.stub.directory],
    tempDirectory: () => TMP,
    appPath: '/work/repo/console',
    appEnv: {},
  })
  return { picker, ipcMain }
}

/** list drives the picker's one read with a picker frame and returns the reply's
 * data. */
async function list(ipcMain: ReturnType<typeof fakeIpcMain>): Promise<InstallationsListReply> {
  const listener = ipcMain.listeners.get(installationChannel('list'))
  expect(listener, 'the picker registered no list channel').toBeDefined()
  const frame = { url: PICKER_ENTRY_URL }
  const reply = (await listener?.({ senderFrame: frame, sender: { mainFrame: frame } }, {})) as ConsoleReply
  expect(reply.ok, reply.ok ? '' : reply.reason).toBe(true)
  if (!reply.ok) throw new Error(reply.reason)
  return reply.data as InstallationsListReply
}

/** rowOf is one row by project name, and it fails loudly when there is none --
 * a missing row is the whole of what these tests are about. */
function rowOf(reply: InstallationsListReply, project: string): InstallationsListRow {
  const row = reply.rows.find((candidate) => candidate.project === project)
  expect(row, `no row for ${project}`).toBeDefined()
  return row as InstallationsListRow
}

describe('the rows the rail draws', () => {
  it('draws every installation, and marks the one whose config the CLI refused', async () => {
    // The owner's four reasons an installation "cannot be read", first shape: the
    // CLI listed `aurora` with its fields empty because it could not read its
    // config. The row is **drawn** -- the rail hides nothing -- and it carries the
    // console's own sentence, which lifts the path and the reason out of the
    // CLI's own line.
    const userData = store([{ id: '0123456789abcdef', project: 'aurora', checkout: '/home/operator/src/aurora' }])
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    expect(reply.rows.map((row) => row.project)).toEqual(['aurora', 'orbit'])
    const aurora = rowOf(reply, 'aurora')
    expect(aurora.state.issue).toBe('config')
    expect(aurora.state.readable).toBe(false)
    expect(aurora.state.command).toBe(DOCTOR_COMMAND)
    expect(aurora.state.sentence).toContain('edit ~/.config/fugaro/projects/aurora.yaml:')
    expect(aurora.state.sentence).toContain('base_images.java-services')
    expect(aurora.state.details).toEqual([CONFIG_LINE])
    // ...and the CLI read `orbit` and returned its fields: no issue at all.
    expect(rowOf(reply, 'orbit').state.issue).toBe('none')
    expect(rowOf(reply, 'orbit').state.readable).toBe(true)
  })

  it('opens the first readable installation when the previously chosen one is not', async () => {
    // The owner's own case: the chosen installation is unreadable and another is
    // readable. The console opens the readable one -- and the chosen one keeps
    // its place in the rail, its `active` flag and its mark.
    const userData = store([{ id: '0123456789abcdef', project: 'aurora', checkout: '/home/operator/src/aurora' }])
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    expect(firstReadableIndex(reply.rows.map((row) => row.state))).toBe(1)
    expect(rowOf(reply, 'aurora').active, 'the remembered choice was dropped from the store').toBe(true)
    expect(rowOf(reply, 'orbit').active).toBe(false)
  })

  it('opens the first readable installation when both are readable', async () => {
    const document = {
      installations: [
        { name: 'aurora', gcp_project: 'aurora-482301', region: 'europe-west4' },
        { name: 'orbit', gcp_project: 'orbit-482301', region: 'europe-west4' },
      ],
    }
    const userData = store([{ id: '0123456789abcdef', project: 'orbit' }])
    const { ipcMain } = over({ userData, stub: stubWith(document) })
    const reply = await list(ipcMain)

    expect(reply.rows.map((row) => row.project)).toEqual(['aurora', 'orbit'])
    expect(reply.rows.every((row) => row.state.readable)).toBe(true)
    expect(firstReadableIndex(reply.rows.map((row) => row.state))).toBe(0)
    // The chosen installation is still the console's own answer, wherever it sits
    // in the CLI's order.
    expect(rowOf(reply, 'orbit').active).toBe(true)
  })

  it('classifies a stale shared config as a warning, and still opens that installation', async () => {
    // The owner's ruling: "classify it as stale ... and still open that
    // installation if the reader returned its fields". The CLI read `orbit` and
    // answered with its fields; only the shared copy it merged is older than the
    // reader refreshes it within.
    const document = { installations: [{ name: 'orbit', gcp_project: 'orbit-482301', region: 'europe-west4' }] }
    const userData = store([{ id: '0123456789abcdef', project: 'orbit' }])
    const { ipcMain } = over({ userData, stub: stubWith(document, [STALE_LINE]) })
    const reply = await list(ipcMain)

    const orbit = rowOf(reply, 'orbit')
    expect(orbit.state.issue).toBe('stale')
    expect(orbit.state.readable).toBe(true)
    expect(orbit.state.command).toBe(DOCTOR_COMMAND)
    expect(orbit.state.sentence).toContain('stale')
    expect(orbit.state.sentence).toContain(DOCTOR_COMMAND)
    expect(orbit.state.details).toEqual([STALE_LINE])
    expect(firstReadableIndex(reply.rows.map((row) => row.state))).toBe(0)
  })

  it('marks every row when the console’s own store file is refused', async () => {
    // A refused store is not one row's problem: a choose writes that file, so the
    // console can open nothing at all while it is unreadable. Every row is marked,
    // and the sentence names the path and the refusal.
    const userData = freshDirectory()
    writeFileSync(join(userData, INSTALLATIONS_FILE_NAME), 'not json at all', { mode: 0o600 })
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    expect(reply.refusal).toBeDefined()
    for (const row of reply.rows) {
      expect(row.state.issue).toBe('store')
      expect(row.state.readable).toBe(false)
      // No command repairs the operator's own file, so the sentence carries the
      // instruction on its own.
      expect(row.state.command).toBe('')
      expect(row.state.sentence).toContain(join(userData, INSTALLATIONS_FILE_NAME))
    }
    expect(firstReadableIndex(reply.rows.map((row) => row.state))).toBe(-1)
  })

  it('marks every row when no CLI was found at all, and names the install command', async () => {
    const userData = store([{ id: '0123456789abcdef', project: 'aurora' }])
    const { ipcMain } = over({ userData })
    const reply = await list(ipcMain)

    expect(reply.onboard.kind).toBe('install-cli')
    expect(reply.rows.map((row) => row.project)).toEqual(['aurora'])
    expect(reply.rows[0]?.state.issue).toBe('binary')
    expect(reply.rows[0]?.state.readable).toBe(false)
    expect(reply.rows[0]?.state.command).toBe(FUGARO_INSTALL_COMMAND)
    expect(firstReadableIndex(reply.rows.map((row) => row.state))).toBe(-1)
  })
})

describe('the checkouts a row may offer', () => {
  it('never offers a temporary or tool-owned path, and keeps the CLI’s own order otherwise', async () => {
    const userData = store([{ id: '0123456789abcdef', project: 'orbit' }])
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    // `orbit` was listed with two checkouts, one of them under the machine's
    // temporary directory: the row offers the other one and nothing else.
    expect(rowOf(reply, 'orbit').checkouts).toEqual(['/home/operator/src/orbit'])
  })

  it('offers the project’s own recorded checkout first, even when the CLI did not list it', async () => {
    const userData = store([{ id: '0123456789abcdef', project: 'orbit', checkout: '/home/operator/src/orbit' }])
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    expect(rowOf(reply, 'orbit').checkouts).toEqual(['/home/operator/src/orbit'])
    // ...and the row still reports the record's own checkout, which is what the
    // rail draws as the directory this installation is used from.
    expect(rowOf(reply, 'orbit').checkout).toBe('/home/operator/src/orbit')
  })

  it('offers a recorded checkout that is itself tool-owned nowhere', async () => {
    // The rule is about where an installation may be used from and not about who
    // wrote the path down: a record that points into a scratch area is exactly the
    // state the owner's report described, and it is not offered back.
    const userData = store([{ id: '0123456789abcdef', project: 'aurora', checkout: `${TMP}/scratchpad/wt-merge` }])
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    expect(rowOf(reply, 'aurora').checkouts).toEqual([])
  })
})
