// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// One operation of each consumer, in the real window (console-improvements plan
// I-OPS).
//
// **The claim this file makes, and the one it does not.** `ops-contract.test.ts`
// holds the thirteen rows to the surface, to each other and to the CLI's own
// goldens, but it builds them over a table of its own: it cannot say that the
// console the operator starts is the one whose rows are those, because it never
// starts one. This file does -- every case sends a message through the **real
// preload** (`window.fugaro`) into the **real `main`**, with the surface the
// console registered at boot and a `fugaro` resolution that found the stub CLI.
// What it does not do is re-check the two gates: `ipc.test.ts` and
// `gating.spec.ts` already hold `lapseHandlers` and `reducedModeHandlers` to
// their rows, and a case here would only say the same thing twice.
//
// **Why an answer is enough to see the difference.** Every read the console can
// ask for and every plan is behind a floor no CLI release names yet
// (`RUNS_STREAM_CLI_FLOOR` and its siblings in `src/shared/version.ts` are all
// `null`), so each of them refuses -- in the words **its own flow** composes,
// which is the whole point: the stub bridge AL left answered one sentence for all
// nine, `not available in this build`, and a console that has lost a row to-day
// is indistinguishable from a console whose CLI is too old if the two share a
// sentence. The sentences below are therefore read out of the flows themselves
// (`newReadFloorSentence`, `setupFloorSentence`, `modelsAddFloorSentence`,
// `imageAddFloorSentence`) and not retyped; and the **three applies** have no
// floor in front of them at all -- the first thing each does is look for the plan
// main is holding -- so each answers its own "no plan is waiting", which no stub
// and no floor could produce.
//
// **The two things that are not a floor.** `settings.editor.open` is the
// settings write's window and spawns nothing as itself, so it answers the
// settings read's own outcome: the stub's `settings --json` document is seeded
// beside it (`test/fixtures/settings.json`), and the case asks for a key that
// document does not carry, which is the one answer that opens no window and
// writes nothing (`SETTINGS_EDIT_NO_ROW`). And the last case drives the read
// through `newOpsFrom`, the renderer's own typed door (RS5's reader): the real
// handler's refusal must arrive as a **`refused` gap** and never as the
// `not-available` gap a bridge with no such member answers, which is the
// difference between a console that wired the operation and one that did not.

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { imageAddFloorSentence, IMAGE_ADD_NO_PLAN_REFUSAL } from '../../src/main/imageadd'
import { modelsAddFloorSentence, MODELS_ADD_NO_PLAN_REFUSAL } from '../../src/main/modelsadd'
import { CONSOLE_OPERATIONS } from '../../src/main/ipc'
import { SETTINGS_EDIT_NO_ROW } from '../../src/main/settingsedit'
import { SETUP_NO_PLAN_REFUSAL, setupFloorSentence } from '../../src/main/wizardrun'
import { NEW_READ_NAMES, newReadFloorSentence } from '../../src/source/new-reads'
import type { NewReadName } from '../../src/source/new-reads'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import type { NewOpsGapName } from '../../src/shared/new-ops'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')

/**
 * MODULES is the compiled module registry the last case injects: the closure of
 * `renderer/new-bridge.js`, which is the whole of what `newOpsFrom` and the
 * contract's decoders need. Written out because the registry has no resolver
 * beyond `./` and `../` -- `modules-registry.spec.ts` is the guard that fails
 * the day this list stops carrying everything these modules require.
 */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/schema.js',
  'shared/new-ops.js',
  'renderer/new-bridge.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/** LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that resolves `./` and `../` specifiers and nothing else, so a bundle
 * that grew an import of `node:` or of a package fails here rather than in a
 * window with no Node in it. Unchanged from `runs-stream.spec.ts` and the specs
 * before it, deliberately: this is the same window and the same constraint. */
const LOADER = `
var registry = {};
var cache = {};
function normalize(spec, from) {
  if (spec.charAt(0) !== '.') throw new Error('the renderer may not require ' + spec);
  var base = from.split('/').slice(0, -1);
  var parts = spec.split('/');
  for (var i = 0; i < parts.length; i++) {
    var part = parts[i];
    if (part === '' || part === '.') continue;
    if (part === '..') { base.pop(); continue; }
    base.push(part);
  }
  var resolved = base.join('/');
  return /\\.js$/.test(resolved) ? resolved : resolved + '.js';
}
function load(path) {
  if (cache[path]) return cache[path].exports;
  var definition = registry[path];
  if (!definition) throw new Error('no module ' + path);
  var module = { exports: {} };
  cache[path] = module;
  definition(module.exports, function (spec) { return load(normalize(spec, path)); }, module);
  return module.exports;
}
`

/**
 * RS5_SOURCE drives two of the nine through `newOpsFrom` over the window's own
 * bridge: a feed (`runs.stream`) and a one-shot read (`whoami`), so both of the
 * contract's two answer shapes cross the same door. Each answer is projected to
 * the three fields the case asserts on, because a gap's `sentence` is the whole
 * of what a screen draws and the gap's name is the whole of the difference
 * between a wired console and one that is not.
 */
const RS5_SOURCE = `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}
var bridge = load('/renderer/new-bridge.js');
var ops = bridge.newOpsFrom(window.fugaro);
return Promise.all([
  ops.streamRuns({ since: '24h' }),
  ops.whoAmI({})
]).then(function (replies) {
  return replies.map(function (reply) {
    return reply.ok
      ? { ok: true, gap: null, sentence: null }
      : { ok: false, gap: reply.gap.gap, sentence: reply.gap.sentence };
  });
});
})()`

/**
 * NOT_AVAILABLE_GAP is `new-bridge.ts`'s gap name for a bridge with no such
 * member -- what a console that never wired one of the nine answers, and what
 * the last case below must never see. Written as a literal of the contract's own
 * union rather than imported: the contract exports the sentence that gap
 * carries, not the name it is keyed by, and typing the literal here means a
 * union that stopped carrying `not-available` fails to compile.
 */
const NOT_AVAILABLE_GAP: NewOpsGapName = 'not-available'

/**
 * NOT_REACHABLE is the sentence `preload/index.ts`'s `invokeOperation` answers
 * for a channel with **no handler behind it** -- the console's surface is
 * registered from the boot's own `onDecided`, so the window is up and asking
 * before it is there, and this one answer is how a case tells the two apart.
 * The preload catches the rejection Electron raises, so the wait below reads an
 * answer rather than a thrown error.
 */
const NOT_REACHABLE = 'the console is not reachable'

/** Reply is the console's own envelope, as it comes back through the preload. */
interface Reply {
  readonly ok: boolean
  readonly reason?: string
  readonly data?: unknown
}

/** reasonOf is a reply's refusal sentence -- or a sentence saying it did not
 * refuse at all, so a case that expects a refusal and is handed `ok` says so
 * rather than comparing `undefined` with a string. */
function reasonOf(reply: Reply): string {
  if (reply.ok) return `it answered ok (${JSON.stringify(reply.data)})`
  return reply.reason ?? '(a refusal with no sentence)'
}

/** methodOf is an operation's bridge method, read out of the surface's own table
 * rather than retyped: `ops-contract.test.ts` holds the nine to the contract's
 * names, and the four writes are the surface's alone. */
function methodOf(name: string): string {
  const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === name)
  if (operation === undefined) throw new Error(`${name} is not an operation of the allow-list`)
  return operation.method
}

/** READ_PAYLOADS is one valid payload per read: the window the two feeds are
 * bound to, and `{}` for the four reads that take nothing at all. */
const READ_PAYLOADS: Readonly<Record<NewReadName, Record<string, unknown>>> = Object.freeze({
  'runs.stream': { since: '24h' },
  'spend.stream': { since: '30d' },
  'budget.stream': {},
  whoami: {},
  'image.catalog.ls': {},
  'recipes.ls': {},
})

/** seedSettings writes the settings document beside **every** copy of the stub,
 * the way `cliStub.ts`'s own seeders do: either copy can be the one the
 * console's resolution runs, and the stub answers a read from the files beside
 * itself. Only the read is written -- the three cases below never reach a dry
 * run, because the key they ask for is not in the document and the one they ask
 * for through `settings.edit` is not on the surface at all. */
function seedSettings(cli: CliStub): void {
  const read = readFileSync(join(consoleDir, 'test', 'fixtures', 'settings.json'), 'utf8')
  for (const where of [cli.dir, join(cli.home, '.local', 'bin')]) {
    mkdirSync(where, { recursive: true })
    writeFileSync(join(where, 'settings.json'), read)
  }
}

let app: ElectronApplication
let page: Page
let cli: CliStub

/**
 * asked sends one message through the console's own bridge and answers the
 * console's own reply. A bridge with no such member answers `{ok:false}` here
 * rather than throwing `undefined is not a function`: the claim is about the
 * method's answer, and a spec that died on the call would say less.
 */
function asked(method: string, payload: Record<string, unknown>): Promise<Reply> {
  return page.evaluate(
    ([method, payload]: readonly [string, Record<string, unknown>]): Promise<Reply> => {
      const api = (window as unknown as {
        fugaro?: Record<string, (payload: unknown) => Promise<unknown>>
      }).fugaro
      if (api === undefined || typeof api[method] !== 'function') {
        return Promise.resolve({ ok: false, reason: `the bridge has no ${method}` })
      }
      return api[method](payload) as Promise<{ ok: boolean; reason?: string; data?: unknown }>
    },
    [method, payload] as const,
  ) as Promise<Reply>
}

/**
 * waitForSurface waits for the boot to have put the surface on `ipcMain`. The
 * table is registered from `onDecided`, so the window can be up and asking
 * before a channel has a handler -- and the preload answers that fact with its
 * one `not reachable` sentence rather than letting the case read it as a
 * refusal (which would make every expectation below a comparison against the
 * wrong string). The probe is a read the file asks for anyway, and its own
 * answer is the first case's expectation: the wait only holds that the sentence
 * is no longer the preload's own.
 */
async function waitForSurface(): Promise<void> {
  for (let attempt = 0; attempt < 150; attempt++) {
    const reply = await asked(methodOf('whoami'), {})
    if (reasonOf(reply) !== NOT_REACHABLE) return
    await page.waitForTimeout(100)
  }
  throw new Error('the console registered no surface within fifteen seconds')
}

test.describe('one operation of each consumer, through the console’s own bridge', () => {
  // One window, one page, and the cases in order: each sends its own messages
  // into the same surface, as `entry.spec.ts` and `runs-stream.spec.ts` do. The
  // file's own timeout rather than the 30s default, because the first case waits
  // for the boot's registration on a loaded machine.
  test.describe.configure({ mode: 'serial', timeout: 90_000 })

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation: without one the
    // window opens on the picker and there is no dashboard document to ask from.
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    seedSettings(cli)
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
    await waitForSurface()
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  test('refuses the six reads in each read’s own words, never the stub’s', async () => {
    for (const name of NEW_READ_NAMES) {
      const reply = await asked(methodOf(name), READ_PAYLOADS[name])
      // The floor no release names yet, composed by the read's own flow and
      // carrying the read's own word (`NEW_READ_WORDS`): "the spend stream needs
      // a fugaro release no CLI has yet" is the spend stream's sentence, and the
      // stub's `not available in this build` is nobody's.
      expect(reasonOf(reply), `${name} through the bridge`).toBe(
        newReadFloorSentence(name, null, CLI_VERSION_BUILT_AGAINST),
      )
    }
  })

  test('refuses the wizard’s plan and the two plans, and every apply with no plan', async () => {
    // The three plans are behind their own flows' floors, so their sentences are
    // the ones those flows compose. The **three applies** are not: the first
    // thing each of them does is look for the plan main is holding -- nothing
    // else is in front of them, because what a renderer sends reaches no child --
    // and with none held the answer is the flow's own "no plan is waiting". That
    // sentence is the sharpest thing this case can assert: no stub ever had it,
    // no floor produces it, and only the real handler, holding real state, can.
    const cases: readonly (readonly [string, Record<string, unknown>, string])[] = [
      [
        'setup.plan',
        {
          answers: {
            gcp_project: 'fugaro-dev',
            region: 'europe-west1',
            name: 'fugaro',
            rigor: 'startup',
            bake_repos: true,
          },
        },
        setupFloorSentence(null, CLI_VERSION_BUILT_AGAINST),
      ],
      ['setup.apply', {}, SETUP_NO_PLAN_REFUSAL],
      [
        'models.add.plan',
        { id: 'deepseek/deepseek-v4', route: 'deepseek' },
        modelsAddFloorSentence(null, CLI_VERSION_BUILT_AGAINST),
      ],
      ['models.add', { id: 'deepseek/deepseek-v4', route: 'deepseek' }, MODELS_ADD_NO_PLAN_REFUSAL],
      [
        'image.add.plan',
        { name: 'node22', dockerfile: 'acme/images:images/node22/Dockerfile' },
        imageAddFloorSentence(null, CLI_VERSION_BUILT_AGAINST),
      ],
      [
        'image.add',
        { name: 'node22', dockerfile: 'acme/images:images/node22/Dockerfile' },
        IMAGE_ADD_NO_PLAN_REFUSAL,
      ],
    ]
    for (const [name, payload, sentence] of cases) {
      const reply = await asked(methodOf(name), payload)
      expect(reasonOf(reply), `${name} through the bridge`).toBe(sentence)
    }
  })

  test('answers the settings editor with the row the document does not carry', async () => {
    // SU1's row is the settings write's window, and its one answer that opens no
    // window and spawns no child is the one for a key the read's document does
    // not carry: the read is the stub's `settings --json`, whose rows are the
    // committed fixture's, and `agent.models.coder` is a key of no layer of it.
    // The reply is the console's ordinary accepted envelope (`ok: true`) whose
    // **data** is the flow's own outcome -- the shape the screen that pressed the
    // pencil draws.
    const reply = await asked(methodOf('settings.editor.open'), { key: 'agent.models.coder' })
    expect(reply.ok, reasonOf(reply)).toBe(true)
    expect(reply.data).toEqual({ ok: false, sentence: SETTINGS_EDIT_NO_ROW })
  })

  test('reaches those handlers through newOpsFrom, as a refusal and not a missing operation', async () => {
    // The renderer's own typed door, over the window's own bridge: a `refused`
    // gap carrying the read's own sentence is a console whose handler really ran,
    // and the `not-available` gap -- what a bridge with no such member answers --
    // is the console that lost the row. The distinction is the whole of what this
    // case adds to the four above, and it is the one a screen acts on.
    const gaps = (await app.evaluate(async ({ BrowserWindow }, body) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(body, true)) as unknown
    }, RS5_SOURCE)) as
      | { readonly error: string }
      | readonly { readonly ok: boolean; readonly gap: string | null; readonly sentence: string | null }[]

    expect(Array.isArray(gaps), `the probe answered ${JSON.stringify(gaps)}`).toBe(true)
    if (!Array.isArray(gaps)) return
    const names = gaps.map((gap) => gap.gap)
    // The same fact twice, on purpose: the first says **which** of the three
    // gaps came back, in the words of the failure this case exists for, and the
    // second pins both answers exactly -- so a case that lost the row fails with
    // a sentence about the row rather than with a diff of two three-word arrays,
    // and a case that answered something else entirely still fails.
    for (const name of names) {
      expect(name, 'a real handler was read as a missing operation').not.toBe(NOT_AVAILABLE_GAP)
    }
    expect(names).toEqual(['refused', 'refused'])
    expect(gaps.map((gap) => gap.sentence)).toEqual([
      newReadFloorSentence('runs.stream', null, CLI_VERSION_BUILT_AGAINST),
      newReadFloorSentence('whoami', null, CLI_VERSION_BUILT_AGAINST),
    ])
  })
})
