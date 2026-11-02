// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The compiled preload, as Electron actually loads it (desktop-console plan Task
// 3.4a, step 2; design section 5.1 rule 5).
//
// **Why this file is about the build rather than about the source.**
// `test/unit/ipc.test.ts` already holds the *source* surface to its promises: it
// imports `src/preload/index.ts`, builds the bridge over a recording
// `ipcRenderer`, and pins the twenty-two methods, the frozen object, the key, and
// the reply envelope. All of that runs under `vitest`'s Node environment, where
// `hasRendererContext` is false and the file's guarded block does nothing.
//
// That is exactly the gap this file closes. The file Electron runs is
// `dist/preload/index.js`, and the block at the bottom of the source -- the one
// that reads `globalThis.window` and `document`, `require`s Electron, and calls
// `exposeInMainWorld` -- has never run in any test. It is four lines and every
// one of them is a way the console can be wrong:
//
//   - **the guard must come before the `require`.** A build that reached the
//     `require` first would fetch Electron's runtime on the first line, and a
//     preload loaded by mistake somewhere that is not a renderer would take the
//     whole process down with it. The test below runs the built file in a scope
//     with no `document` and asserts the recording `require` was never called --
//     which is the only form of that claim that can be checked without a browser;
//   - **the one `require` must have the literal specifier `'electron'`.** A
//     sandboxed preload resolves `electron`, `events`, `timers` and `url` and
//     nothing else, so a second specifier, or a computed one, is a preload that
//     throws the moment Electron loads it -- and *nothing* in the app would work,
//     which is a bad way to find out;
//   - **what lands on the renderer's `window` is the frozen bridge and nothing
//     else**, under the one key `fugaro` (design rule 5). This is checked here
//     over the built artifact, with a sentinel `ipcRenderer` that the walk below
//     then fails to find anywhere in what was exposed.
//
// The sentinel is the mutation the review cares about: a preload that exposed
// `ipcRenderer` itself, or one of its methods, would hand the renderer the whole
// allow-list's worth of channel names and everything above it. So the walk is
// deep, and it compares against the sentinel, the sentinel's `invoke`, and every
// function reachable from the sentinel -- a leak can be the object or the
// function that would do the work.

import { readFileSync } from 'node:fs'
import { createContext, runInContext } from 'node:vm'

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS } from '../../src/main/ipc'
import { CONSOLE_BRIDGE_KEY, EXPOSED_OPERATIONS, exposeConsoleBridge } from '../../src/preload/index'
import type { ContextBridgeLike, RendererLike } from '../../src/preload/index'
import {
  CONFIRM_BRIDGE_KEY,
  CONFIRM_BRIDGE_PREFIX,
  createConfirmBridge,
  isConfirmReply,
} from '../../src/preload/confirm'
import { builtFile, builtPath } from '../support/built'

/**
 * SENTINEL is the `ipcRenderer` the console never wants the renderer to hold. It
 * is not a fake of Electron's: it is the object whose `invoke` takes a channel,
 * which is the capability rule 5 is about, and every reachable value in what was
 * exposed is compared against it and against its methods.
 */
const SENTINEL: RendererLike & { readonly treasury: string } = Object.freeze({
  treasury: 'acme/treasury',
  async invoke(channel: string, ...args: unknown[]): Promise<unknown> {
    return { channel, args }
  },
})

/**
 * recordingBridge is `contextBridge` as far as this file uses it: it records
 * every `exposeInMainWorld` call, and (when it is given the world to expose into)
 * puts the key on it, which is the half of `exposeInMainWorld` this test reads
 * back. The object itself is not cloned into a second world here -- that copy is
 * Electron's, and what a renderer would get is the same key with the same
 * methods on it, which is what is asserted.
 */
function recordingBridge(target?: Record<string, unknown>): ContextBridgeLike & {
  readonly exposed: readonly { readonly key: string; readonly api: unknown }[]
} {
  const exposed: { key: string; api: unknown }[] = []
  return {
    exposed,
    exposeInMainWorld(key: string, api: unknown): void {
      exposed.push({ key, api })
      if (target !== undefined) target[key] = api
    },
  }
}

/**
 * runBuiltPreload runs `dist/preload/index.js` the way a CommonJS loader would,
 * in a fresh realm with a recording `require`.
 *
 * The realm is the point: the built file reads `globalThis`, and a `vm` context
 * is the only way to give it a `window`/`document` pair (or the absence of one)
 * without lying to the module about what kind of process it is in. `require` is
 * the loader's, and it records what was asked for -- so "the only specifier is
 * `electron`" and "the `require` came after the guard" are both facts this test
 * holds rather than properties a reader has to take on trust.
 *
 * It returns what the module did, not what it exported: this file has no exports
 * a caller wants (its whole job is a side effect on `window`), and the assertion
 * is about that side effect.
 */
function runBuiltPreload(scope: {
  readonly window?: Record<string, unknown>
  readonly document?: unknown
}): {
  readonly required: readonly string[]
  readonly exposed: readonly { readonly key: string; readonly api: unknown }[]
  readonly window: Record<string, unknown> | undefined
} {
  const source = builtFile('preload/index.js')
  const required: string[] = []
  // The window the preload sees *is* the object this test reads afterwards, so
  // `window.fugaro` below is what the built file put there rather than something
  // this test reconstructed. A scope with no window gets none: the whole point of
  // the second half of the test below is a realm where `globalThis.window` is
  // absent, and a default here would quietly supply one.
  const bridge = recordingBridge(scope.window)
  const electron = { contextBridge: bridge, ipcRenderer: SENTINEL }
  const sandbox: Record<string, unknown> = {
    ...scope,
    require(specifier: string): unknown {
      required.push(specifier)
      if (specifier !== 'electron') throw new Error(`the preload required ${specifier}`)
      return electron
    },
    module: { exports: {} },
    exports: {},
    console,
  }
  runInContext(source, createContext(sandbox), { filename: 'preload/index.js' })
  return { required, exposed: bridge.exposed, window: scope.window }
}

/** reachable is every value a renderer can walk to from what was exposed:
 * the api, its own properties, and (guarding against a getter that answers a new
 * object each read) nothing deeper than a small depth, with a visited set so a
 * cycle cannot hang the test. */
function reachable(root: unknown): readonly unknown[] {
  const seen = new Set<unknown>()
  const found: unknown[] = []
  const walk = (value: unknown, depth: number): void => {
    if (depth > 4 || seen.has(value)) return
    if (typeof value !== 'object' && typeof value !== 'function') return
    if (value === null) return
    seen.add(value)
    found.push(value)
    for (const key of [...Object.getOwnPropertyNames(value), ...Object.getOwnPropertySymbols(value)]) {
      let next: unknown
      try {
        next = (value as Record<string | symbol, unknown>)[key]
      } catch {
        continue
      }
      walk(next, depth + 1)
    }
  }
  walk(root, 0)
  return found
}

describe('the built preload, as Electron loads it', () => {
  it('is the file the dashboard window loads, and it is not empty', () => {
    // The non-vacuity guard for everything below: an empty file passes a test
    // that says "the require was never reached" without the preload working.
    const source = builtFile('preload/index.js')
    expect(source.length).toBeGreaterThan(0)
    expect(source).toContain('exposeInMainWorld')
  })

  it('puts the frozen bridge on the renderer world and nothing else', () => {
    const { exposed, window, required } = runBuiltPreload({ window: {}, document: {} })
    // One call, one key: the design's rule 5 is "exactly one key on `window`",
    // and a second `exposeInMainWorld` would be a second surface.
    expect(exposed.map((entry) => entry.key)).toEqual([CONSOLE_BRIDGE_KEY])
    expect(Object.keys(window ?? {}).sort()).toEqual([CONSOLE_BRIDGE_KEY])
    const api = (window ?? {})[CONSOLE_BRIDGE_KEY] as Record<string, unknown>
    expect(Object.isFrozen(api)).toBe(true)
    // Every operation's method and the two fields, by name -- and the fields are the
    // bridge's own, so a method renamed in one file and not the other fails here.
    expect(Object.keys(api).sort()).toEqual(
      ['operations', 'version', ...CONSOLE_OPERATIONS.map((operation) => operation.method)].sort(),
    )
    // And the one require it took to get there: Electron, by literal.
    expect(required).toEqual(['electron'])
  })

  it('is inert where there is no renderer, and does not reach Electron first', () => {
    // The guard's order, and the reason this test can exist at all: a preload
    // loaded under Node (or by any tool that imported it) must not call
    // `require('electron')`, because Electron's own `index.js` fetches a browser
    // runtime the first time it is required. So the recording `require` is the
    // evidence, and it is empty -- not merely "it did not throw".
    const { required, exposed, window } = runBuiltPreload({ window: {} })
    expect(required).toEqual([])
    expect(exposed).toEqual([])
    expect(window).toEqual({})
    // The other half of the guard: no `window` at all (a main process, a
    // worker) is the same answer.
    const withoutWindow = runBuiltPreload({ document: {} })
    expect(withoutWindow.required).toEqual([])
    expect(withoutWindow.exposed).toEqual([])
  })

  it('never lets the renderer reach the ipcRenderer it was built over', () => {
    // The mutation this file exists for. Everything reachable from what was
    // exposed -- by own name, by symbol, through a getter -- must not be the
    // sentinel, a function of the sentinel, or anything else that carries a
    // channel. A preload that spread `ipcRenderer` in, or closed over it and
    // handed it out, fails here.
    const { window } = runBuiltPreload({ window: {}, document: {} })
    const api = (window ?? {})[CONSOLE_BRIDGE_KEY]
    const found = reachable(api)
    expect(found.length).toBeGreaterThan(CONSOLE_OPERATIONS.length)
    // Identity, not deep equality: the claim is "this object is not reachable",
    // and a deep comparison would also refuse an equal-looking copy, which is a
    // different (and much weaker) statement.
    const forbidden: readonly (readonly [string, unknown])[] = [
      ['the ipcRenderer itself', SENTINEL],
      ['the ipcRenderer’s invoke', SENTINEL.invoke],
      ['a value the ipcRenderer carries', SENTINEL.treasury],
      ['the ipcRenderer’s prototype', Object.getPrototypeOf(SENTINEL)],
    ]
    for (const value of found) {
      for (const [what, target] of forbidden) {
        expect(value === target, `the exposed surface reaches ${what}`).toBe(false)
      }
    }
    // Every function that *is* reachable is one of the api's own methods, and
    // every one of those takes at most the operation's own argument -- so no
    // reachable function is a way to name a channel.
    const methods = Object.entries(api as object).filter(([, value]) => typeof value === 'function')
    expect(methods.map(([name]) => name).sort()).toEqual(
      CONSOLE_OPERATIONS.map((operation) => operation.method).sort(),
    )
    for (const [name, method] of methods) {
      expect(found.includes(method), `${name} is not the value the api holds`).toBe(true)
      expect((method as (...args: unknown[]) => unknown).length, `${name} takes many arguments`).toBeLessThanOrEqual(1)
    }
    expect(JSON.stringify(api)).not.toContain('treasury')
  })
})

describe('the surface is the same object whichever way it is built', () => {
  it('exposes the identical shape through `exposeConsoleBridge` as through the build', () => {
    // The source path and the built path have to agree, or the unit suite's
    // pins on `createConsoleBridge` are pins on a file Electron does not run.
    // The two keys, the frozen-ness and the method set, compared directly.
    const bridge = recordingBridge()
    exposeConsoleBridge(bridge, SENTINEL)
    const fromSource = bridge.exposed[0]?.api as Record<string, unknown>
    const { window } = runBuiltPreload({ window: {}, document: {} })
    const fromBuild = (window ?? {})[CONSOLE_BRIDGE_KEY] as Record<string, unknown>
    expect(Object.keys(fromBuild).sort()).toEqual(Object.keys(fromSource).sort())
    expect(Object.isFrozen(fromSource) && Object.isFrozen(fromBuild)).toBe(true)
    expect(fromBuild['version']).toBe(fromSource['version'])
    // Spread rather than compared in place: the two objects are from different
    // realms, and `toEqual` across realms would be comparing constructors too.
    expect([...(fromBuild['operations'] as readonly string[])]).toEqual([
      ...(fromSource['operations'] as readonly string[]),
    ])
  })

  it('is CommonJS, so the guarded require is the only way it touches Electron', () => {
    // `tsc` emits CommonJS, and the guarded block's `require` only exists
    // because it does: an ESM build would have to import Electron at the top of
    // the file, which is the exact thing the guard is for. This is the scan that
    // says so, so a later change to `tsconfig.build.json` fails here rather than
    // in a packaged console nobody can run.
    const source = builtFile('preload/index.js')
    expect(source).toMatch(/\brequire\(['"]electron['"]\)/)
    expect(source).not.toMatch(/^\s*import\s/m)
    expect(source).not.toMatch(/\bimport\s*\(/)
    // And the read of the file that produced it, so a build that silently
    // emitted nothing is not mistaken for a build that emitted this.
    expect(readFileSync(builtPath('preload/index.js'), 'utf8')).toBe(source)
  })
})

// ---------------------------------------------------------------------------
// The confirmation window's preload (V2.5.1a part 2b-1).
// ---------------------------------------------------------------------------

/** runBuiltConfirm runs `dist/preload/confirm.js` the way a CommonJS loader
 * would, in a fresh realm with a recording `require` **and** a recording
 * `ipcRenderer`, so what the document can reach and what it sends are both
 * facts this file holds rather than reads of the source. */
function runBuiltConfirm(
  scope: { readonly window?: Record<string, unknown>; readonly document?: unknown },
  reply: unknown = { ok: true, data: {} },
): {
  readonly required: readonly string[]
  readonly exposed: readonly { readonly key: string; readonly api: unknown }[]
  readonly window: Record<string, unknown> | undefined
  readonly sent: readonly { readonly channel: string; readonly args: readonly unknown[] }[]
} {
  const source = builtFile('preload/confirm.js')
  const required: string[] = []
  const sent: { channel: string; args: readonly unknown[] }[] = []
  const bridge = recordingBridge(scope.window)
  const renderer: RendererLike = {
    async invoke(channel: string, ...args: unknown[]): Promise<unknown> {
      sent.push({ channel, args })
      return reply
    },
  }
  const sandbox: Record<string, unknown> = {
    ...scope,
    require(specifier: string): unknown {
      required.push(specifier)
      if (specifier !== 'electron') throw new Error(`the preload required ${specifier}`)
      return { contextBridge: bridge, ipcRenderer: renderer }
    },
    module: { exports: {} },
    exports: {},
    console,
  }
  runInContext(source, createContext(sandbox), { filename: 'preload/confirm.js' })
  return { required, exposed: bridge.exposed, window: scope.window, sent }
}

describe('the confirmation window’s built preload', () => {
  it('exposes exactly fields and answer, under its own key, and nothing else', () => {
    const { exposed, window, required } = runBuiltConfirm({ window: {}, document: {} })
    expect(exposed.map((entry) => entry.key)).toEqual([CONFIRM_BRIDGE_KEY])
    expect(Object.keys(window ?? {}).sort()).toEqual([CONFIRM_BRIDGE_KEY])
    const api = (window ?? {})[CONFIRM_BRIDGE_KEY] as Record<string, unknown>
    expect(Object.isFrozen(api)).toBe(true)
    expect(Object.keys(api).sort()).toEqual(['answer', 'fields'])
    expect(required).toEqual(['electron'])
  })

  it('sends the two channels and nothing else', async () => {
    const { window, sent } = runBuiltConfirm({ window: {}, document: {} })
    const api = (window ?? {})[CONFIRM_BRIDGE_KEY] as {
      fields: () => Promise<unknown>
      answer: (yes: boolean) => Promise<unknown>
    }
    expect(await api.fields()).toStrictEqual({ ok: true, data: {} })
    expect(await api.answer(true)).toStrictEqual({ ok: true, data: {} })
    expect(sent).toStrictEqual([
      { channel: `${CONFIRM_BRIDGE_PREFIX}fields`, args: [] },
      { channel: `${CONFIRM_BRIDGE_PREFIX}answer`, args: [true] },
    ])
  })

  it('is inert where there is no renderer, and does not reach Electron first', () => {
    const { required, exposed, window } = runBuiltConfirm({ window: {} })
    expect(required).toEqual([])
    expect(exposed).toEqual([])
    expect(window).toEqual({})
  })

  it('refuses anything that is not a reply, and a renderer that rejects', async () => {
    const bridge = createConfirmBridge({
      invoke: async () => 'not a reply',
    })
    expect(await bridge.fields()).toStrictEqual({
      ok: false,
      reason: 'the confirmation window answered something that is not a reply',
    })
    const failing = createConfirmBridge({
      invoke: async () => {
        throw new Error('no answer')
      },
    })
    expect(await failing.answer(false)).toStrictEqual({
      ok: false,
      reason: 'the confirmation window could not be reached (no answer)',
    })
    expect(isConfirmReply({ ok: true, data: null })).toBe(true)
    expect(isConfirmReply({ ok: false, reason: 'no' })).toBe(true)
    expect(isConfirmReply({ ok: false })).toBe(false)
    expect(isConfirmReply({ ok: true })).toBe(false)
    expect(isConfirmReply('yes')).toBe(false)
  })

  it('is a third surface: the dashboard’s names no confirm channel anywhere', () => {
    // The mutation this catches: a `confirm:` row added to the dashboard's
    // operation table, or a dashboard method that reaches this window's
    // channels. The dashboard's surface is unchanged at twenty-five rows.
    expect(EXPOSED_OPERATIONS).toHaveLength(25)
    for (const [name, method] of [...EXPOSED_OPERATIONS, ...CONSOLE_OPERATIONS.map((row) => [row.name, row.method] as const)]) {
      expect(name, `${method} is on the confirm prefix`).not.toContain('confirm:')
      expect(method).not.toBe('fields')
      expect(method).not.toBe('answer')
    }
    expect(CONFIRM_BRIDGE_PREFIX).toBe('confirm:')
    expect(CONSOLE_BRIDGE_KEY).not.toBe(CONFIRM_BRIDGE_KEY)
  })
})
