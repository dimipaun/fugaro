// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
//   2. **which row is opened**: `openProject` over those rows, which is the
//      owner's rule -- the remembered choice when it can be read, the first
//      readable one otherwise, and the first row when nothing is readable -- with
//      the remembered installation keeping its place and its mark.
//
// The project names are invented (`aurora`, `orbit`) and every path is one the
// test itself wrote.

import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
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
  '~/.config/fugaro/projects/aurora.yaml could not be read: field base_imagez not found in type config.Project'

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
function over(options: {
  readonly userData: string
  readonly stub?: Stub
  readonly onChosen?: Parameters<typeof createInstallationPicker>[0]['onChosen']
}) {
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
    ...(options.onChosen === undefined ? {} : { onChosen: options.onChosen }),
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

/** ask drives one of the picker's operations with a picker frame. */
async function ask(ipcMain: ReturnType<typeof fakeIpcMain>, name: string, args: Record<string, string>): Promise<ConsoleReply> {
  const listener = ipcMain.listeners.get(installationChannel(name as 'list'))
  const frame = { url: PICKER_ENTRY_URL }
  return (await listener?.({ senderFrame: frame, sender: { mainFrame: frame } }, args)) as ConsoleReply
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
    expect(aurora.state.command).toBe(`${DOCTOR_COMMAND} --project aurora`)
    expect(aurora.state.sentence).toContain('edit ~/.config/fugaro/projects/aurora.yaml:')
    expect(aurora.state.sentence).toContain('field base_imagez not found')
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
    // The row the console is **open on** is the readable one, and it is exactly
    // one row: the rail draws it as its selected row, and the main area reads
    // from it.
    expect(reply.rows.filter((row) => row.open).map((row) => row.project)).toEqual(['orbit'])
  })

  it('opens the remembered choice when it can be read, wherever it sits in the CLI’s order', async () => {
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
    // in the CLI's order -- and it is the one the console is open on, because a
    // console that opened the CLI's first row at every launch would throw the
    // operator's choice away each time it started.
    expect(rowOf(reply, 'orbit').active).toBe(true)
    expect(rowOf(reply, 'orbit').open).toBe(true)
    expect(rowOf(reply, 'aurora').open).toBe(false)
  })

  it('opens the first readable installation when the console has chosen none', async () => {
    // A console that has never been used: nothing is stored, the CLI lists two
    // readable installations, and one of them is open -- the rail is drawn with a
    // row for each and no picker screen in front of it.
    const document = {
      installations: [
        { name: 'aurora', gcp_project: 'aurora-482301', region: 'europe-west4' },
        { name: 'orbit', gcp_project: 'orbit-482301', region: 'europe-west4' },
      ],
    }
    const userData = freshDirectory()
    const { ipcMain } = over({ userData, stub: stubWith(document) })
    const reply = await list(ipcMain)

    expect(reply.rows.map((row) => row.active)).toEqual([false, false])
    expect(reply.rows.filter((row) => row.open).map((row) => row.project)).toEqual(['aurora'])
  })

  it('opens the first row when nothing is readable, which is the row the issue view is for', async () => {
    const userData = freshDirectory()
    writeFileSync(join(userData, INSTALLATIONS_FILE_NAME), 'not json at all', { mode: 0o600 })
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    expect(reply.rows.every((row) => !row.state.readable)).toBe(true)
    expect(reply.rows.filter((row) => row.open).map((row) => row.project)).toEqual(['aurora'])
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
    expect(orbit.state.command).toBe(`${DOCTOR_COMMAND} --project orbit`)
    expect(orbit.state.sentence).toContain('stale')
    expect(orbit.state.sentence).toContain(`${DOCTOR_COMMAND} --project orbit`)
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

  it('carries what each installation records as repositories, and no path', async () => {
    // The Live view's launch control (V2.5.1b) offers these and passes the chosen
    // one back to the main process, which matches it against the installation's
    // own records -- so the field is the identities themselves, in the CLI's
    // order, each one once, and the checkout paths beside them are not in it.
    const userData = store([
      { id: '0123456789abcdef', project: 'aurora', checkout: '/home/operator/src/aurora' },
      { id: 'fedcba9876543210', project: 'vega' },
    ])
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    // `orbit`'s document lists its one repository twice, as two checkouts of it,
    // one of them under the machine's temporary directory: one identity, and no
    // path from either row.
    expect(rowOf(reply, 'orbit').repositories).toEqual(['github/owner/orbit'])
    // `aurora`'s config could not be read, so it records no repository at all --
    // `[]` is a fact, and the form draws its own sentence for it rather than an
    // empty control.
    expect(rowOf(reply, 'aurora').repositories).toEqual([])
    // `vega` is in the console's own store and in no CLI list, which is the
    // ordinary case for a store written by an older console, and it records none
    // either.
    expect(rowOf(reply, 'vega').listed).toBe(false)
    expect(rowOf(reply, 'vega').repositories).toEqual([])
    for (const row of reply.rows) {
      expect(row.repositories.filter((one) => one.includes('/home/') || one.startsWith(TMP))).toEqual([])
    }
  })

  it('keeps the CLI’s order, drops the repeat, and keeps a repository whose path has moved', async () => {
    // A launch names a repository, not a directory, so none of the three flags a
    // checkout row carries (`enrolled`, `valid`, `path`) decides whether a
    // repository is offered: a repository this machine once checked out and no
    // longer has is still one a run can be launched on.
    const document = {
      installations: [
        {
          name: 'orbit',
          gcp_project: 'orbit-482301',
          region: 'europe-west4',
          checkouts: [
            { repository: 'bitbucket/team/orbit', path: '/home/operator/src/orbit', enrolled: true, valid: true },
            { repository: 'github/owner/orbit', path: '/home/operator/src/orbit', enrolled: true, valid: true },
            { repository: 'bitbucket/team/orbit', path: '/home/operator/gone', enrolled: false, valid: false },
          ],
        },
      ],
    }
    const userData = store([{ id: '0123456789abcdef', project: 'orbit' }])
    const { ipcMain } = over({ userData, stub: stubWith(document) })
    const reply = await list(ipcMain)

    expect(rowOf(reply, 'orbit').repositories).toEqual([
      'bitbucket/team/orbit',
      'github/owner/orbit',
    ])
  })
})

describe('the console’s own open rule at startup', () => {
  it('decides from one read, and the rail’s first list answers that read rather than spawning again', async () => {
    // The startup and the rail ask the same question in the same instant, and
    // `config list --json` is a child process: one read answers both, and every
    // read after that is the screen's own (a removal, a checkout, the picker
    // window's refresh).
    const stub = stubWith(DOCUMENT, [CONFIG_LINE])
    const userData = store([{ id: '0123456789abcdef', project: 'orbit' }])
    const { picker, ipcMain } = over({ userData, stub })
    // The read itself, counted by its argv rather than by the spawn total: rule
    // 4's walk probes the binary it finds with a `version`, which is a spawn of
    // its own and not the document this test is about.
    const lists = (): number => stub.reports().filter((report) => report.argv[1] === 'list').length
    await picker.openInstallation()
    expect(lists()).toBe(1)
    await list(ipcMain)
    expect(lists(), 'the rail’s first read spawned the same command twice').toBe(1)
    await list(ipcMain)
    expect(lists(), 'a later read was served from the boot read').toBe(2)
  })

  it('decides once per process: a second handshake neither reads again nor re-decides', async () => {
    // `readCliVersion` runs `openInstallation` and the handshake can run again (a
    // retry); a re-read there would spawn `config list` behind the operator's
    // back and could move the console to another installation.
    const stub = stubWith(DOCUMENT, [CONFIG_LINE])
    const userData = store([{ id: '0123456789abcdef', project: 'aurora' }])
    const { picker } = over({ userData, stub })
    const lists = (): number => stub.reports().filter((report) => report.argv[1] === 'list').length
    await picker.openInstallation()
    const decided = picker.installation()
    await picker.openInstallation()
    expect(lists()).toBe(1)
    expect(picker.installation()).toEqual(decided)
  })

  it('opens the first readable installation for the run, and writes nothing to the store', async () => {
    // The owner's case, end to end: `aurora` is the operator's remembered choice
    // and its config cannot be read, `orbit` can. The console is open on `orbit`
    // -- so the source, the header and the screens read from it -- and the store
    // is **byte for byte** what it was, because the remembered choice is not
    // dropped.
    const userData = store([{ id: '0123456789abcdef', project: 'aurora', checkout: '/home/operator/src/aurora' }])
    const storePath = join(userData, INSTALLATIONS_FILE_NAME)
    const before = readFileSync(storePath, 'utf8')
    const { picker, ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })

    await picker.openInstallation()
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'orbit' })
    expect(readFileSync(storePath, 'utf8')).toBe(before)

    const reply = await list(ipcMain)
    expect(rowOf(reply, 'aurora').active, 'the remembered choice was dropped').toBe(true)
    expect(rowOf(reply, 'aurora').open).toBe(false)
    expect(rowOf(reply, 'orbit').open).toBe(true)
    // The reply's own `active` is the session's answer too, so the line the
    // dashboard draws and the row the rail selects are the same installation.
    expect(reply.active).toMatchObject({ chosen: true, project: 'orbit' })
  })

  it('answers the store’s own record when the remembered choice can be read', async () => {
    const userData = store([{ id: '0123456789abcdef', project: 'orbit' }])
    const { picker } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })

    await picker.openInstallation()
    // The record's own id: the session is the store's record and not a copy of
    // it, so nothing about this installation changes shape at a boot.
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'orbit', id: '0123456789abcdef' })
  })

  it('leaves the store’s own answer in place when the read fails', async () => {
    // A console whose `config list` could not run is a console that still opens,
    // on what the store says: the open rule has no rows to decide from, and a
    // failed read must not take the installation away.
    const userData = store([{ id: '0123456789abcdef', project: 'aurora' }])
    const { picker } = over({ userData })
    await picker.openInstallation()
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'aurora', id: '0123456789abcdef' })
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

  it('collapses the menu to one entry per repository, drops what the CLI did not re-validate, and counts the rest', async () => {
    // **What the dropdown is drawn from** (design §3.4 rule 7): one entry per
    // repository, over the rows carrying `valid: true` and `enrolled: true`, with
    // the tool-owned and temporary directories dropped. `orbit` records three
    // directories of one repository -- one ordinary, one under the machine's
    // temporary root, one the CLI refused -- and one of another repository the
    // project does not enroll; the menu is two entries, and the one that stands
    // for `orbit` says how many directories it stands for.
    const document = {
      installations: [
        {
          name: 'orbit',
          gcp_project: 'orbit-482301',
          region: 'europe-west4',
          checkouts: [
            { repository: 'github/owner/orbit', path: '/home/operator/src/orbit', enrolled: true, valid: true },
            { repository: 'github/owner/orbit', path: `${TMP}/orbit-scratch`, enrolled: true, valid: true },
            { repository: 'github/owner/orbit', path: '/home/operator/src/orbit-two', enrolled: true, valid: true },
            { repository: 'github/owner/orbit', path: '/home/operator/src/gone', enrolled: true, valid: false, reason: 'missing' },
            { repository: 'github/owner/other', path: '/home/operator/src/other', enrolled: false, valid: false, reason: 'not_enrolled' },
            { repository: 'github/owner/aurora', path: '/home/operator/src/aurora', enrolled: true, valid: true },
          ],
        },
      ],
      warnings: [],
    }
    const userData = store([{ id: '0123456789abcdef', project: 'orbit' }])
    const { ipcMain } = over({ userData, stub: stubWith(document) })
    const reply = await list(ipcMain)

    expect(rowOf(reply, 'orbit').checkoutChoices).toEqual([
      { repository: 'github/owner/orbit', path: '/home/operator/src/orbit', more: 1 },
      { repository: 'github/owner/aurora', path: '/home/operator/src/aurora', more: 0 },
    ])
    // The picker window's own list is the one it always was: every path the CLI
    // re-validated, the refused row's included nowhere, and no repository pairing.
    expect(rowOf(reply, 'orbit').checkouts).toEqual([
      '/home/operator/src/orbit',
      '/home/operator/src/orbit-two',
      '/home/operator/src/aurora',
    ])
  })

  it('offers a row the CLI did not list nothing at all', async () => {
    // A record this console holds and the CLI did not list has no document
    // behind it: the menu is empty rather than drawn from the store, and the
    // chooser says so in the console's own words.
    const userData = store([{ id: '0123456789abcdef', project: 'acme', checkout: '/home/operator/src/acme' }])
    const { ipcMain } = over({ userData, stub: stubWith(DOCUMENT, [CONFIG_LINE]) })
    const reply = await list(ipcMain)

    const row = reply.rows.find((each) => each.project === 'acme')
    expect(row?.listed).toBe(false)
    expect(row?.checkoutChoices).toEqual([])
    // ...while the directory the operator recorded is still the row's own
    // checkout, which is what the rail draws it with.
    expect(row?.checkout).toBe('/home/operator/src/acme')
  })
})

describe('removing the installation the console is open on', () => {
  const BOTH_READABLE = {
    installations: [
      { name: 'aurora', gcp_project: 'aurora-482301', region: 'europe-west4' },
      { name: 'orbit', gcp_project: 'orbit-482301', region: 'europe-west4' },
    ],
  }

  it('rebinds the console to the next readable installation at once, through the handshake', async () => {
    const chosen: string[] = []
    const userData = store([
      { id: '0123456789abcdef', project: 'aurora' },
      { id: 'fedcba9876543210', project: 'orbit' },
    ])
    // `aurora` is the console's own record and the CLI does not list it, so
    // removing the record removes the row; `orbit` is the next readable one. (A
    // record the CLI does list stays a row after removal: the console forgot it,
    // the machine did not.)
    const bound: string[] = []
    const { picker, ipcMain } = over({
      userData,
      stub: stubWith({ installations: [{ name: 'orbit', gcp_project: 'orbit-482301', region: 'europe-west4' }] }),
      onChosen: async (installation) => {
        chosen.push(installation.project)
        // What the reopened window's handshake and source read, at the moment
        // the window is asked for.
        const now = picker.installation()
        bound.push(now.chosen ? now.project : '')
      },
    })
    await list(ipcMain)
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'aurora' })
    const removed = await ask(ipcMain, 'remove', { project: 'aurora' })
    expect(removed.ok).toBe(true)
    // The handshake for the next one ran, and the window it reopens is that
    // installation's: no source is left bound to the record that went.
    expect(chosen).toEqual(['orbit'])
    expect(bound).toEqual(['orbit'])
    // The handshake's answer is remembered for it, as a choose's is, so the
    // header reads the mode and the version rather than 'version not read'.
    expect(picker.installation()).toMatchObject({ mode: 'ok' })
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'orbit' })
    if (removed.ok) {
      const data = removed.data as { rows: readonly InstallationsListRow[] }
      expect(data.rows.filter((row) => row.open).map((row) => row.project)).toEqual(['orbit'])
    }
  })

  it('does not rebind when the row removed was not the open one', async () => {
    const chosen: string[] = []
    const userData = store([
      { id: '0123456789abcdef', project: 'aurora' },
      { id: 'fedcba9876543210', project: 'orbit' },
    ])
    const { ipcMain } = over({
      userData,
      stub: stubWith(BOTH_READABLE),
      onChosen: async (installation) => {
        chosen.push(installation.project)
      },
    })
    await list(ipcMain)
    expect((await ask(ipcMain, 'remove', { project: 'orbit' })).ok).toBe(true)
    expect(chosen).toEqual([])
  })

  it('does not rebind to an installation it cannot read, and answers the onboarding when none remain', async () => {
    const chosen: string[] = []
    const userData = store([{ id: '0123456789abcdef', project: 'aurora' }])
    const { ipcMain } = over({
      userData,
      stub: stubWith({ installations: [] }),
      onChosen: async (installation) => {
        chosen.push(installation.project)
      },
    })
    await list(ipcMain)
    const removed = await ask(ipcMain, 'remove', { project: 'aurora' })
    expect(removed.ok).toBe(true)
    expect(chosen).toEqual([])
    if (removed.ok) {
      const data = removed.data as { rows: readonly unknown[]; onboard: { kind: string } }
      expect(data.rows).toEqual([])
      expect(data.onboard.kind).toBe('first-installation')
    }
  })
})

describe('a choose reopens the window on the chosen installation', () => {
  it('answers the chosen installation inside onChosen, after a first run opened another one', async () => {
    // Review M1: a first run with no remembered choice opens the first readable
    // row as the session; a choose must not leave that session answering while
    // the window it reopens runs its handshake and binds its source.
    const document = {
      installations: [
        { name: 'aurora', gcp_project: 'aurora-482301', region: 'europe-west4' },
        { name: 'orbit', gcp_project: 'orbit-482301', region: 'europe-west4' },
      ],
    }
    const bound: string[] = []
    const userData = freshDirectory()
    const { picker, ipcMain } = over({
      userData,
      stub: stubWith(document),
      onChosen: async () => {
        const now = picker.installation()
        bound.push(now.chosen ? now.project : '')
      },
    })
    await picker.openInstallation()
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'aurora' })
    const chosen = await ask(ipcMain, 'choose', { project: 'orbit' })
    expect(chosen.ok, chosen.ok ? '' : chosen.reason).toBe(true)
    expect(bound).toEqual(['orbit'])
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'orbit' })
  })
})
