// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The update policy, rule by rule (desktop-console plan Task 4.4, "Auto-update
// -- SECURITY-SENSITIVE"; design section 7.3 for the cadence, the refused
// downgrade and the off switch, section 7.4 for "no update check that carries an
// identifier").
//
// A signed update replaces the app that holds the console's launch write path,
// so every rule this module holds is a test rather than a sentence: the gate,
// the cadence, the stable-only feed, the two-URL allow-list, the refusal to
// downgrade, the header set that carries no identifier, and the rule that
// nothing installs while a launch is in flight. Each `it` below names the rule
// it holds, and the fakes record what actually crossed the boundary -- the URLs,
// the headers, the updater calls -- so a rule that stops being enforced is a
// failure here rather than a note in a document.

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  MAX_LISTING_BYTES,
  RELEASES_URL,
  UPDATE_INTERVAL_MS,
  createUpdateController,
  feedUrlFor,
  newestStableConsoleRelease,
} from '../../src/main/updates'
import type { UpdatePolicyDeps, UpdaterPort } from '../../src/main/updates'

const RUNNING = '0.116.0'
const FEED = 'https://github.com/dimipaun/fugaro/releases/download/console-v0.120.0/'

/** listing is GitHub's answer for the release endpoint: one JSON body, built
 * here from the records a test names, so what the policy reads is legible in the
 * test rather than encoded in a fixture. */
function listing(releases: readonly unknown[]): string {
  return JSON.stringify(releases)
}

/** release is one entry of that listing, with the two booleans GitHub always
 * sends and a tag; a test overrides whichever field it is about. */
function release(tag: unknown, overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return { tag_name: tag, draft: false, prerelease: false, name: tag, id: 1, ...overrides }
}

/** FetchCall is one request the policy made, as the fake saw it. */
interface FetchCall {
  readonly url: string
  readonly headers: Readonly<Record<string, string>>
}

/** listingFake is the injected `fetchReleases`: it records every call and
 * answers with the bodies the test gave it, in order. */
function listingFake(bodies: readonly string[]): {
  readonly calls: readonly FetchCall[]
  readonly fetchReleases: UpdatePolicyDeps['fetchReleases']
} {
  const calls: FetchCall[] = []
  let index = 0
  return {
    calls,
    fetchReleases: (url, headers) => {
      calls.push({ url, headers })
      const body = bodies[Math.min(index, bodies.length - 1)] ?? ''
      index += 1
      return Promise.resolve(body)
    },
  }
}

/** UpdaterFake is the injected updater. It starts in the state a pass-through
 * adapter would hand over *before* the policy configures it -- downloads off,
 * downgrades and pre-releases allowed, an identifying header -- so a test that
 * asserts the configured values is asserting the policy actually wrote them. */
interface UpdaterFake extends UpdaterPort {
  readonly feedUrls: readonly string[]
  readonly checks: number
  readonly quits: number
  readonly events: readonly string[]
  emit(event: 'update-downloaded' | 'error', payload?: unknown): void
}

function updaterFake(): UpdaterFake {
  const feedUrls: string[] = []
  const events: string[] = []
  const listeners = new Map<string, ((...args: unknown[]) => void)[]>()
  let checks = 0
  let quits = 0
  const updater = {
    autoDownload: false,
    autoInstallOnAppQuit: false,
    allowDowngrade: true,
    allowPrerelease: true,
    requestHeaders: { 'X-Machine-Id': 'machine-1' } as Record<string, string>,
    setFeedURL(options: { provider: 'generic'; url: string }): void {
      feedUrls.push(options.url)
    },
    checkForUpdates(): Promise<unknown> {
      checks += 1
      return Promise.resolve(null)
    },
    quitAndInstall(): void {
      quits += 1
    },
    on(event: string, listener: (...args: unknown[]) => void): unknown {
      events.push(event)
      const existing = listeners.get(event) ?? []
      existing.push(listener)
      listeners.set(event, existing)
      return updater
    },
    emit(event: 'update-downloaded' | 'error', payload?: unknown): void {
      for (const listener of listeners.get(event) ?? []) listener(payload)
    },
  }
  Object.defineProperty(updater, 'feedUrls', { get: () => feedUrls })
  Object.defineProperty(updater, 'checks', { get: () => checks })
  Object.defineProperty(updater, 'quits', { get: () => quits })
  Object.defineProperty(updater, 'events', { get: () => events })
  return updater as unknown as UpdaterFake
}

/** TimersFake is the injected timer. Nothing here reaches the wall clock: a
 * test moves six hours by calling `fire()`. */
interface TimersFake {
  readonly delays: readonly number[]
  readonly live: boolean
  readonly armed: number
  readonly setInterval: UpdatePolicyDeps['timer']['setInterval']
  readonly clearInterval: UpdatePolicyDeps['timer']['clearInterval']
  fire(): void
}

/** timersFake keeps every armed handle, so an interval the policy forgot to
 * clear stays visible as a second live handle rather than being overwritten. */
function timersFake(): TimersFake {
  const delays: number[] = []
  const handlers = new Map<number, () => void>()
  let next = 0
  const timers = {
    get delays() {
      return delays
    },
    get live() {
      return handlers.size > 0
    },
    get armed() {
      return handlers.size
    },
    setInterval: (handler: () => void, ms: number) => {
      delays.push(ms)
      next += 1
      handlers.set(next, handler)
      return next as unknown as ReturnType<typeof setInterval>
    },
    clearInterval: (handle: unknown) => {
      handlers.delete(handle as number)
    },
    fire: () => {
      for (const handler of [...handlers.values()]) handler()
    },
  }
  return timers as unknown as TimersFake
}

/** Harness is one controller and everything it could reach. */
interface Harness {
  readonly updater: UpdaterFake
  readonly timers: TimersFake
  readonly fetches: readonly FetchCall[]
  readonly ready: readonly string[]
  readonly setSwitch: (on: boolean) => void
  readonly setIdle: (idle: boolean) => void
  readonly controller: ReturnType<typeof createUpdateController>
}

/** harness builds that controller for the newest stable release, with every dep
 * at the value the gate admits; `overrides` is how a test states the one dep it
 * is about (a platform that is not darwin, a busy launch flow, a hostile body).
 * A test that replaces `fetchReleases` keeps the fake's own recording by
 * reaching for `fetchFake` instead of writing its own. */
function harness(
  overrides: Partial<UpdatePolicyDeps> = {},
  bodies: readonly string[] = [listing([release('console-v0.120.0')])],
): Harness {
  const updater = updaterFake()
  const timers = timersFake()
  const fetchFake = listingFake(bodies)
  const ready: string[] = []
  let on = true
  let idle = true
  const deps: UpdatePolicyDeps = {
    platform: 'darwin',
    isPackaged: true,
    env: {},
    currentVersion: RUNNING,
    switchOn: () => on,
    timer: { setInterval: timers.setInterval, clearInterval: timers.clearInterval },
    fetchReleases: fetchFake.fetchReleases,
    isLaunchFlowIdle: () => idle,
    onReadyToRestart: (version) => {
      ready.push(version)
    },
    updater,
    ...overrides,
  }
  return {
    updater,
    timers,
    fetches: fetchFake.calls,
    ready,
    setSwitch: (next) => {
      on = next
    },
    setIdle: (next) => {
      idle = next
    },
    controller: createUpdateController(deps),
  }
}

/** settle lets the check's promises run: the policy's work is asynchronous all
 * the way down, so an assertion made before this would read the state of a check
 * that has not finished. */
function settle(): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, 0)
  })
}

/** heldFetch is a `fetchReleases` that answers only when the test lets it, so a
 * check can be inspected while its request is still in flight. */
function heldFetch(body: string): {
  readonly calls: readonly FetchCall[]
  readonly fetchReleases: UpdatePolicyDeps['fetchReleases']
  answer(): void
} {
  const calls: FetchCall[] = []
  let open: (() => void) | null = null
  const gate = new Promise<void>((resolve) => {
    open = resolve
  })
  return {
    calls,
    fetchReleases: (url, headers) => {
      calls.push({ url, headers })
      return gate.then(() => body)
    },
    answer: () => open?.(),
  }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('the gate: nothing at all until every condition holds', () => {
  const closed: readonly { readonly why: string; readonly overrides: Partial<UpdatePolicyDeps> }[] = [
    { why: 'the platform is linux', overrides: { platform: 'linux' } },
    { why: 'the platform is win32', overrides: { platform: 'win32' } },
    { why: 'the build is not packaged', overrides: { isPackaged: false } },
    { why: 'CI is set', overrides: { env: { CI: 'true' } } },
    { why: 'the check is switched off in the environment', overrides: { env: { FUGARO_NO_UPDATE_CHECK: '1' } } },
  ]

  for (const { why, overrides } of closed) {
    it(`does nothing when ${why}`, async () => {
      const { controller, fetches, updater, timers } = harness(overrides)
      await controller.start()
      await settle()
      expect(fetches).toHaveLength(0)
      expect(updater.checks).toBe(0)
      expect(updater.feedUrls).toEqual([])
      expect(updater.events).toEqual([])
      expect(updater.autoDownload).toBe(false)
      expect(updater.autoInstallOnAppQuit).toBe(false)
      expect(updater.allowDowngrade).toBe(true)
      expect(updater.allowPrerelease).toBe(true)
      expect(updater.requestHeaders).toEqual({ 'X-Machine-Id': 'machine-1' })
      expect(timers.delays).toEqual([])
      expect(timers.live).toBe(false)
    })
  }

  it('does nothing while the switch is off', async () => {
    const { controller, fetches, updater, timers } = harness({ switchOn: () => false })
    await controller.start()
    await settle()
    expect(fetches).toHaveLength(0)
    expect(updater.checks).toBe(0)
    expect(timers.delays).toEqual([])
    expect(timers.live).toBe(false)
  })

  it('checks on an empty CI and on any FUGARO_NO_UPDATE_CHECK that is not 1', async () => {
    const { controller, fetches, timers } = harness({ env: { CI: '', FUGARO_NO_UPDATE_CHECK: '0' } })
    await controller.start()
    await settle()
    expect(fetches).toHaveLength(1)
    expect(timers.delays).toEqual([UPDATE_INTERVAL_MS])
  })

  it('rearms when the switch goes on after start', async () => {
    const { controller, fetches, setSwitch } = harness()
    setSwitch(false)
    await controller.start()
    await settle()
    expect(fetches).toHaveLength(0)
    setSwitch(true)
    await controller.start()
    await settle()
    expect(fetches).toHaveLength(1)
  })
})

describe('the cadence: one check at start, then every six hours', () => {
  it('checks once at start and asks the timer for six hours', async () => {
    const { controller, fetches, timers } = harness()
    await controller.start()
    expect(fetches).toHaveLength(1)
    expect(timers.delays).toEqual([UPDATE_INTERVAL_MS])
    expect(UPDATE_INTERVAL_MS).toBe(6 * 60 * 60 * 1000)
  })

  it('checks only when the timer fires, and arms one timer rather than one per check', async () => {
    const { controller, fetches, timers } = harness()
    await controller.start()
    await settle()
    await settle()
    expect(fetches).toHaveLength(1)
    expect(timers.delays).toEqual([UPDATE_INTERVAL_MS])
  })

  it('keeps one timer armed however many times start() runs', async () => {
    const { controller, timers } = harness()
    await controller.start()
    await controller.start()
    await settle()
    expect(timers.armed).toBe(1)
  })

  it('checks again on each six-hour tick', async () => {
    const { controller, fetches, timers } = harness()
    await controller.start()
    timers.fire()
    await settle()
    expect(fetches).toHaveLength(2)
    timers.fire()
    await settle()
    expect(fetches).toHaveLength(3)
  })

  it('stops the timer at once on stop()', async () => {
    const { controller, fetches, timers } = harness()
    await controller.start()
    controller.stop()
    expect(timers.live).toBe(false)
    timers.fire()
    await settle()
    expect(fetches).toHaveLength(1)
  })

  it('clears the timer at once when the switch goes off', async () => {
    const { controller, fetches, timers, setSwitch } = harness()
    await controller.start()
    setSwitch(false)
    timers.fire()
    await settle()
    expect(timers.live).toBe(false)
    expect(fetches).toHaveLength(1)
  })

  it('abandons a check the switch closed while its request was in flight', async () => {
    const held = heldFetch(listing([release('console-v0.120.0')]))
    const { controller, updater, setSwitch } = harness({ fetchReleases: held.fetchReleases })
    const started = controller.start()
    await settle()
    expect(held.calls).toHaveLength(1)
    setSwitch(false)
    held.answer()
    await started
    expect(updater.feedUrls).toEqual([])
    expect(updater.checks).toBe(0)
  })

  it('abandons a check stop() interrupted, so nothing downloads after the app said stop', async () => {
    const held = heldFetch(listing([release('console-v0.120.0')]))
    const { controller, updater } = harness({ fetchReleases: held.fetchReleases })
    const started = controller.start()
    await settle()
    controller.stop()
    held.answer()
    await started
    expect(updater.feedUrls).toEqual([])
    expect(updater.checks).toBe(0)
  })
})

describe('the stable-only feed', () => {
  const dropped: readonly { readonly why: string; readonly entry: unknown }[] = [
    { why: 'a draft', entry: release('console-v9.9.9', { draft: true }) },
    { why: 'a pre-release', entry: release('console-v9.9.9', { prerelease: true }) },
    { why: 'a CLI tag', entry: release('v0.20.0') },
    { why: 'a two-component console tag', entry: release('console-v1.2') },
    { why: 'a leading zero', entry: release('console-v01.2.3') },
    { why: 'a pre-release suffix', entry: release('console-v1.2.3-rc.1') },
    { why: 'a build suffix', entry: release('console-v1.2.3+build.4') },
    { why: 'a nine-digit-plus component', entry: release('console-v1.2.1234567890') },
    { why: 'a non-string tag', entry: release(1234) },
    { why: 'a missing tag', entry: { draft: false, prerelease: false } },
    { why: 'a missing draft field', entry: { tag_name: 'console-v9.9.9', prerelease: false } },
    { why: 'a missing prerelease field', entry: { tag_name: 'console-v9.9.9', draft: false } },
    { why: 'a draft field that is not the boolean false', entry: release('console-v9.9.9', { draft: 'false' }) },
    { why: 'a null entry', entry: null },
    { why: 'a string entry', entry: 'console-v9.9.9' },
    { why: 'a downgrade', entry: release('console-v0.100.0') },
    { why: 'the running version itself', entry: release('console-v0.116.0') },
  ]

  for (const { why, entry } of dropped) {
    it(`drops ${why}`, () => {
      expect(newestStableConsoleRelease(JSON.parse(listing([entry])) as unknown, RUNNING)).toBeNull()
    })
  }

  it('picks the newest by number, not by string', () => {
    const body = listing([
      release('console-v0.99.0'),
      release('console-v0.120.0'),
      release('console-v0.9.0'),
      release('console-v0.116.1'),
    ])
    expect(newestStableConsoleRelease(JSON.parse(body) as unknown, RUNNING)).toBe('console-v0.120.0')
  })

  it('ignores the order the releases arrive in', () => {
    const body = listing([release('console-v0.120.0'), release('console-v0.121.0'), release('console-v0.119.0')])
    expect(newestStableConsoleRelease(JSON.parse(body) as unknown, RUNNING)).toBe('console-v0.121.0')
  })

  it('answers with nothing when the listing is not a list, or the running version is not one', () => {
    const newer = listing([release('console-v0.120.0')])
    expect(newestStableConsoleRelease({}, RUNNING)).toBeNull()
    expect(newestStableConsoleRelease(null, RUNNING)).toBeNull()
    expect(newestStableConsoleRelease('console-v9.9.9', RUNNING)).toBeNull()
    expect(newestStableConsoleRelease(JSON.parse(listing([null])) as unknown, RUNNING)).toBeNull()
    for (const running of ['dev', '0.116', 'v0.116', '0.116.0-rc.1', '', '0.116.0.1']) {
      expect(newestStableConsoleRelease(JSON.parse(newer) as unknown, running)).toBeNull()
    }
  })

  it('accepts a running version carrying the CLI tag prefix', () => {
    const body = listing([release('console-v0.120.0')])
    expect(newestStableConsoleRelease(JSON.parse(body) as unknown, `v${RUNNING}`)).toBe('console-v0.120.0')
  })
})

describe('no downgrade: a candidate at or below the running version is refused', () => {
  for (const candidate of ['console-v0.115.0', 'console-v0.116.0', 'console-v0.0.0']) {
    it(`sets no feed and asks for no update for ${candidate}`, async () => {
      const { controller, updater, fetches } = harness({}, [listing([release(candidate)])])
      await controller.start()
      await settle()
      expect(fetches).toHaveLength(1)
      expect(updater.feedUrls).toEqual([])
      expect(updater.checks).toBe(0)
    })
  }

  it('sets the feed and asks the updater once there is something newer', async () => {
    const { controller, updater } = harness()
    await controller.start()
    expect(updater.feedUrls).toEqual([FEED])
    expect(updater.checks).toBe(1)
  })
})

describe('the two URLs the console may ever build', () => {
  const refused: readonly { readonly why: string; readonly input: unknown }[] = [
    { why: 'the CLI tag shape', input: 'v0.20.0' },
    { why: 'a bare version', input: '1.2.3' },
    { why: 'a two-component tag', input: 'console-v1.2' },
    { why: 'a leading zero', input: 'console-v01.2.3' },
    { why: 'a pre-release suffix', input: 'console-v1.2.3-rc.1' },
    { why: 'a build suffix', input: 'console-v1.2.3+b4' },
    { why: 'a path traversal', input: 'console-v1.2.3/../../evil' },
    { why: 'an encoded separator', input: 'console-v1.2.3%2F..' },
    { why: 'an encoded dot run', input: 'console-v1%2e2%2e3' },
    { why: 'a query', input: 'console-v1.2.3?x=1' },
    { why: 'a fragment', input: 'console-v1.2.3#x' },
    { why: 'a backslash', input: 'console-v1.2.3\\..' },
    { why: 'another host', input: 'https://example.invalid/console-v1.2.3' },
    { why: 'a leading slash', input: '/console-v1.2.3' },
    { why: 'an empty tag', input: '' },
    { why: 'a trailing space', input: 'console-v1.2.3 ' },
    { why: 'an uppercase prefix', input: 'Console-v1.2.3' },
    { why: 'a newline', input: 'console-v1.2.3\n' },
    { why: 'a non-string', input: 42 },
    { why: 'a component past nine digits', input: `console-v1.2.${'9'.repeat(10)}` },
  ]

  for (const { why, input } of refused) {
    it(`refuses ${why}`, () => {
      expect(() => feedUrlFor(input as string)).toThrow()
    })
  }

  it('builds exactly the feed URL for a tag that passed the filter', () => {
    expect(feedUrlFor('console-v1.2.3')).toBe('https://github.com/dimipaun/fugaro/releases/download/console-v1.2.3/')
  })

  it('sends nothing but the listing URL and that feed URL, whatever the listing says', async () => {
    const { controller, updater, fetches } = harness({}, [
      listing([
        release('v9.9.9'),
        release('console-v9.9.9', { prerelease: true }),
        release('console-v0.120.0'),
        release('console-v0.900.0', { draft: true }),
        release('https://example.invalid/console-v0.999.0'),
        release('http://github.com/dimipaun/fugaro/releases/download/console-v0.998.0/'),
      ]),
    ])
    await controller.start()
    await settle()
    expect(fetches.map((call) => call.url)).toEqual([RELEASES_URL])
    expect(updater.feedUrls).toEqual([FEED])
    expect(updater.feedUrls[0]?.startsWith('https://github.com/')).toBe(true)
  })
})

describe('the listing is one page, and all of it', () => {
  it('asks for a hundred releases per page', () => {
    // The check reads one page and never paginates, so the page size is the
    // whole of its reach. GitHub's default is thirty, the CLI releases faster
    // than the console, and a burst of thirty CLI tags between two console
    // releases would push the newest console tag off the only page this app
    // ever asks for -- a check that answers "nothing new" for months. The
    // literal is asserted rather than the constant, because a query string is
    // exactly the part of a URL an edit removes without anything else noticing.
    expect(RELEASES_URL).toBe('https://api.github.com/repos/dimipaun/fugaro/releases?per_page=100')
  })

  it('bounds the listing above the page it asks for, whatever the page size becomes', () => {
    // The bound and the page size are one decision: the read stops at
    // `MAX_LISTING_BYTES`, so a bound below a page GitHub will really answer is
    // a check that fails on every tick and never offers a release again. Twenty
    // KB is a generous release object -- a few KB of notes and an asset list --
    // and a page is up to `per_page` of them, so the bound has to clear that
    // with room to spare.
    const perPage = Number(new URL(RELEASES_URL).searchParams.get('per_page'))
    expect(perPage).toBe(100)
    expect(MAX_LISTING_BYTES).toBeGreaterThanOrEqual(perPage * 20 * 1024)
  })
})

describe('the request carries no identifier', () => {
  it('sends exactly two fixed headers on the listing request', async () => {
    const { controller, fetches } = harness()
    await controller.start()
    expect(fetches).toHaveLength(1)
    expect(fetches[0]?.headers).toEqual({
      Accept: 'application/vnd.github+json',
      'User-Agent': 'fugaro-console',
    })
  })

  it('sends the same two headers on every check, and no others', async () => {
    const { controller, fetches, timers } = harness()
    await controller.start()
    timers.fire()
    await settle()
    expect(fetches).toHaveLength(2)
    for (const call of fetches) {
      expect(Object.keys(call.headers).toSorted()).toEqual(['Accept', 'User-Agent'])
      expect(call.headers['User-Agent']).toBe('fugaro-console')
    }
  })

  it('names no version, platform, machine, user or installation in what it sends', async () => {
    const { controller, fetches, updater } = harness({ currentVersion: '0.120.0' }, [
      listing([release('console-v0.121.0')]),
    ])
    await controller.start()
    await settle()
    const sent = JSON.stringify({
      urls: fetches.map((call) => call.url),
      headers: fetches[0]?.headers,
      clientHeaders: updater.requestHeaders,
    })
    for (const absent of ['0.120.0', 'darwin', 'machine', 'person', 'installation', 'fugaro-console/']) {
      expect(sent).not.toContain(absent)
    }
    // The feed URL is the one thing that names the release, and it names only
    // the tag: scheme, this repository, and a shape that cannot carry more.
    expect(updater.feedUrls).toEqual(['https://github.com/dimipaun/fugaro/releases/download/console-v0.121.0/'])
    expect(updater.feedUrls[0]).toMatch(
      /^https:\/\/github\.com\/dimipaun\/fugaro\/releases\/download\/console-v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\/$/,
    )
  })

  it('gives the updater an empty, writable header table', async () => {
    // Empty, and deliberately not frozen. Nothing in 6.8.10 writes into this
    // table -- `addAuthHeader` reassigns the field, `computeFinalHeaders` builds
    // the object it hands out -- so a frozen one would not throw today; what the
    // writable table is for is a later version that does write in place, which
    // would otherwise throw inside the library on every check.
    const { controller, updater } = harness()
    await controller.start()
    expect(updater.requestHeaders).toEqual({})
    expect(Object.isFrozen(updater.requestHeaders)).toBe(false)
    updater.requestHeaders['X-Probe'] = 'written'
    expect(updater.requestHeaders).toEqual({ 'X-Probe': 'written' })
  })
})

describe('the install: only the user restarts', () => {
  it('configures the updater as the rules require', async () => {
    const { controller, updater } = harness()
    await controller.start()
    expect(updater.autoDownload).toBe(true)
    expect(updater.autoInstallOnAppQuit).toBe(false)
    expect(updater.allowDowngrade).toBe(false)
    expect(updater.allowPrerelease).toBe(false)
    expect(updater.requestHeaders).toEqual({})
    expect(updater.events.toSorted()).toEqual(['error', 'update-downloaded'])
  })

  it('tells the app a download is ready, and does not restart on its own', async () => {
    const { controller, updater, ready } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])
    expect(updater.quits).toBe(0)
  })

  it('prompts once for one finished download, however many times the event is repeated', async () => {
    const { controller, updater, ready } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])
  })

  it('names the release whose download finished, not the one a later check moved the feed to', async () => {
    const { controller, updater, ready, timers } = harness({}, [
      listing([release('console-v0.120.0')]),
      listing([release('console-v0.121.0')]),
    ])
    await controller.start()
    timers.fire()
    await settle()
    expect(updater.feedUrls).toEqual([
      FEED,
      'https://github.com/dimipaun/fugaro/releases/download/console-v0.121.0/',
    ])
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])
  })

  it('ignores a completion for a release no feed was ever pointed at', async () => {
    const { controller, updater, ready } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '9.9.9' })
    expect(ready).toEqual([])
    expect(updater.quits).toBe(0)
  })

  it('refuses the bytes of a release it did not choose, and leaves the install-on-quit flag off', async () => {
    // The version is what ties the download to the tag the feed was pointed at,
    // and the zip's SHA-512 comes from the manifest beside it: a completion for
    // a version this policy never asked for is a completion for bytes nobody
    // checked, so it is not offered to the operator. The flag is read on both
    // sides of the event because a refusal is the moment a write of `true` would
    // be easiest to leave behind; it is the port's state this test is about, not
    // what the library would do with it -- that is the real client's, in
    // `updater-electron.test.ts`.
    const { controller, updater, ready } = harness()
    await controller.start()
    expect(updater.autoInstallOnAppQuit).toBe(false)
    updater.emit('update-downloaded', { version: '9.9.9' })
    expect(ready).toEqual([])
    expect(updater.autoInstallOnAppQuit).toBe(false)
  })

  it('configures the client before a check that start() did not begin', async () => {
    const { controller, updater, ready } = harness()
    await controller.checkNow()
    expect(updater.autoDownload).toBe(true)
    expect(updater.autoInstallOnAppQuit).toBe(false)
    expect(updater.allowDowngrade).toBe(false)
    expect(updater.allowPrerelease).toBe(false)
    expect(updater.requestHeaders).toEqual({})
    expect(updater.events.toSorted()).toEqual(['error', 'update-downloaded'])
    expect(updater.feedUrls).toEqual([FEED])
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])
  })

  it('installs when the user asks, and not before', async () => {
    const { controller, updater, ready } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])
    expect(updater.quits).toBe(0)
    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(1)
  })

  it('installs only the download the answered notice named, never one that landed after it', async () => {
    // Two notices are open at once and the operator answers the older one: the
    // version in the answer is what the user was shown, and the bytes on disk
    // are the later download's, so the press installs nothing. The client's own
    // event carries the version of the download it finished, which is how the
    // controller can tell the two apart at all.
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const { controller, updater, ready, timers } = harness({}, [
      listing([release('console-v0.120.0')]),
      listing([release('console-v0.121.0')]),
    ])
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    timers.fire()
    await settle()
    updater.emit('update-downloaded', { version: '0.121.0' })
    expect(ready).toEqual(['0.120.0', '0.121.0'])

    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(0)
    expect(logged.mock.calls).toEqual([
      ['fugaro console: the restart is refused; the update on disk is not the version the notice named'],
    ])

    controller.restartRequested('0.121.0')
    expect(updater.quits).toBe(1)
  })
})

describe('never during a launch', () => {
  it('does not prompt while a launch is in flight, and prompts once it is idle', async () => {
    const { controller, updater, ready, setIdle } = harness()
    setIdle(false)
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual([])
    expect(updater.quits).toBe(0)
    setIdle(true)
    controller.launchFlowIdle()
    expect(ready).toEqual(['0.120.0'])
    controller.launchFlowIdle()
    expect(ready).toEqual(['0.120.0'])
  })

  it('does not restart while a launch is in flight, and restarts once it is idle', async () => {
    const { controller, updater, setIdle } = harness()
    setIdle(false)
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(0)
    setIdle(true)
    controller.launchFlowIdle()
    expect(updater.quits).toBe(1)
    controller.launchFlowIdle()
    expect(updater.quits).toBe(1)
  })

  it('restarts at once when the flow was already idle', async () => {
    const { controller, updater } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(1)
  })

  it('delivers both deferrals when the flow goes idle', async () => {
    const { controller, updater, ready, setIdle } = harness()
    setIdle(false)
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    controller.restartRequested('0.120.0')
    setIdle(true)
    controller.launchFlowIdle()
    expect(ready).toEqual(['0.120.0'])
    expect(updater.quits).toBe(1)
  })

  it('stays deferred however many checks pass while the flow is busy', async () => {
    const { controller, updater, ready, timers, setIdle } = harness()
    setIdle(false)
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    controller.restartRequested('0.120.0')
    timers.fire()
    await settle()
    expect(ready).toEqual([])
    expect(updater.quits).toBe(0)
  })

  it('delivers nothing on a launchFlowIdle() the flow itself contradicts', async () => {
    const { controller, updater, ready, setIdle } = harness()
    setIdle(false)
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    controller.restartRequested('0.120.0')
    controller.launchFlowIdle()
    expect(ready).toEqual([])
    expect(updater.quits).toBe(0)
    setIdle(true)
    controller.launchFlowIdle()
    expect(ready).toEqual(['0.120.0'])
    expect(updater.quits).toBe(1)
  })
})

describe('a press names a version, and only the download holding it installs', () => {
  it('installs nothing for a version no download ever landed', async () => {
    // The button's answer is the version its notice was opened with, so a press
    // for anything the policy is not holding is a press about bytes that are not
    // there -- and installing whatever is on disk instead is what this refuses.
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const { controller, updater } = harness()
    await controller.start()
    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(0)
    expect(logged).toHaveBeenCalledTimes(1)
  })

  it('re-checks the version it deferred, so a download that replaced it installs nothing', async () => {
    // The press is deferred while a launch is in flight, and the download it was
    // about is replaced before the flow settles: the deferral is a version, so
    // it is checked again where it is delivered rather than trusted from where
    // it was recorded.
    const { controller, updater, ready, timers, setIdle } = harness({}, [
      listing([release('console-v0.120.0')]),
      listing([release('console-v0.121.0')]),
    ])
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])
    setIdle(false)
    controller.restartRequested('0.120.0')
    timers.fire()
    await settle()
    updater.emit('update-downloaded', { version: '0.121.0' })
    expect(ready).toEqual(['0.120.0'])

    setIdle(true)
    controller.launchFlowIdle()
    expect(updater.quits).toBe(0)
    expect(ready).toEqual(['0.120.0', '0.121.0'])
  })
})

describe('the off switch after a download began', () => {
  it('prompts nobody for a download that finishes after the switch went off', async () => {
    const { controller, updater, ready, setSwitch } = harness()
    await controller.start()
    setSwitch(false)
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual([])
    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(0)
  })

  it('leaves the flag off for a download that finished after the switch went off', async () => {
    const { controller, updater, setSwitch } = harness()
    await controller.start()
    setSwitch(false)
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(updater.autoInstallOnAppQuit).toBe(false)
    setSwitch(true)
    await controller.start()
    expect(updater.autoInstallOnAppQuit).toBe(false)
  })

  it('leaves the flag off for a download that finished while the gate was shut for another reason', async () => {
    // The switch is not the only thing that closes the gate, and the tick that
    // would notice a closed one is up to six hours away -- so a download that
    // lands in between is refused where it lands.
    const env: Record<string, string | undefined> = {}
    const { controller, updater } = harness({ env })
    await controller.start()
    env['CI'] = 'true'
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(updater.autoInstallOnAppQuit).toBe(false)
  })
})

describe('the install-on-quit flag, which no path turns on', () => {
  it('leaves it off for a download the policy accepted, and installs nothing on its own', async () => {
    // Owner ruling of 2026-10-30: an update is installed only through the
    // notice's "Restart to update" action. The accepted download is the moment
    // the write of `true` used to happen, so the flag and the client's own
    // install call are both read here -- and the pair in
    // `updater-electron.test.ts` is what the flag means to 6.8.10.
    const { controller, updater, ready, timers } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])
    expect(updater.autoInstallOnAppQuit).toBe(false)
    expect(updater.quits).toBe(0)
    timers.fire()
    await settle()
    expect(updater.autoInstallOnAppQuit).toBe(false)
  })

  it('leaves it off for a download the policy refused, and the switch coming back on does not turn it on', async () => {
    // The bytes on disk are the last download's, so a refusal is a refusal for
    // whatever is installed from them: the switch's return trip must not arm an
    // install for bytes the policy did not choose.
    const { controller, updater, ready, setSwitch } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '9.9.9' })
    expect(ready).toEqual([])
    expect(updater.autoInstallOnAppQuit).toBe(false)
    setSwitch(false)
    setSwitch(true)
    await controller.start()
    expect(updater.autoInstallOnAppQuit).toBe(false)
  })
})

describe('after stop(), nothing the client reports is acted on', () => {
  it('prompts nobody, and writes nothing to the client, for a download that lands while the app is quitting', async () => {
    // `stop()` is called from `before-quit`, and a download that began before it
    // can finish after it: the notice would open over a quitting app. The client
    // is left exactly as `configure()` left it, because after `stop()` this
    // policy has no business writing to it at all -- and `configure()` is where
    // the install-on-quit flag is turned off for good.
    const { controller, updater, ready } = harness()
    await controller.start()
    controller.stop()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual([])
    expect(updater.autoInstallOnAppQuit).toBe(false)
  })

  it('delivers no deferral when a launch goes idle while the app is quitting', async () => {
    const { controller, updater, ready, setIdle } = harness()
    setIdle(false)
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    controller.restartRequested('0.120.0')
    controller.stop()
    setIdle(true)
    controller.launchFlowIdle()
    expect(ready).toEqual([])
    expect(updater.quits).toBe(0)
  })

  it('does nothing for a press after stop(), and records nothing for a later idle flow', async () => {
    // `stop()` means the app is quitting, and a notice answered after it is an
    // answer about the process that is already on its way out: `live` refuses
    // the press where `launchFlowIdle` already refuses the deferrals.
    const { controller, updater } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    controller.stop()
    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(0)
    controller.launchFlowIdle()
    expect(updater.quits).toBe(0)
  })
})

describe('a stop() forgets the download it was holding', () => {
  it('installs nothing for a press whose download a stop() put behind it (U4b)', async () => {
    // `stop()` is not only the quitting process's: the switch going off stops
    // this controller and the switch coming back on starts it again, and the
    // bytes a download left on disk are not the bytes this run chose. A version
    // that survived the stop is one a later press installs, so the press has to
    // find nothing here to install.
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const { controller, updater, ready } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])

    controller.stop()
    await controller.start()
    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(0)
    expect(logged.mock.calls).toEqual([
      ['fugaro console: the restart is refused; the update on disk is not the version the notice named'],
    ])
  })

  it('installs nothing for a press the same release downloaded again must not revive (U4c)', async () => {
    // The deferral is the press's, and a press is about the notice the operator
    // answered -- not about a version string. The switch going off and on again,
    // and the same release downloaded again, is a *new* download, and the press
    // that came before it is not an answer to a notice nobody has seen: only the
    // clearing at `stop()` keeps the old press from installing it, because
    // `installable` is the same version again and a deferral left standing
    // matches it exactly where the flow goes idle.
    const { controller, updater, ready, setIdle, setSwitch } = harness()
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual(['0.120.0'])

    // A launch is in flight and "Restart to update" is pressed: deferred, not
    // delivered.
    setIdle(false)
    controller.restartRequested('0.120.0')
    expect(updater.quits).toBe(0)

    setSwitch(false)
    controller.stop()
    setSwitch(true)
    await controller.start()

    // The same release finishes downloading again, and the launch ends.
    updater.emit('update-downloaded', { version: '0.120.0' })
    setIdle(true)
    controller.launchFlowIdle()

    // The notice for the new download is delivered, and nothing is installed:
    // the press went with the download the stop forgot.
    expect(ready).toEqual(['0.120.0', '0.120.0'])
    expect(updater.quits).toBe(0)
  })

  it('prompts nobody for a download a stop() put behind it (U4b)', async () => {
    // The prompt is deferred while a launch is in flight, and the stop lands in
    // between: a flow that goes idle after the controller started again must not
    // deliver a notice about a download that is no longer this policy's.
    const { controller, updater, ready, setIdle } = harness()
    setIdle(false)
    await controller.start()
    updater.emit('update-downloaded', { version: '0.120.0' })
    expect(ready).toEqual([])

    controller.stop()
    setIdle(true)
    await controller.start()
    controller.launchFlowIdle()
    expect(ready).toEqual([])
  })
})

describe('a failure is one line and a retry', () => {
  it('swallows a listing failure, names no host and no query, and checks again on the next tick', async () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    let first = true
    const { controller, updater, timers } = harness({
      fetchReleases: (url: string, headers: Readonly<Record<string, string>>) => {
        if (!first) return Promise.resolve(listing([release('console-v0.120.0')]))
        first = false
        expect(url).toBe(RELEASES_URL)
        expect(headers['User-Agent']).toBe('fugaro-console')
        return Promise.reject(new Error('connect ECONNREFUSED https://example.invalid/x?token=abc'))
      },
    })
    await controller.start()
    await settle()
    expect(logged).toHaveBeenCalledTimes(1)
    expect(logged.mock.calls[0]).toHaveLength(1)
    const text = String(logged.mock.calls[0]?.[0])
    for (const absent of ['example.invalid', 'token', 'ECONNREFUSED', 'abc']) {
      expect(text).not.toContain(absent)
    }
    expect(text).not.toContain('\n')
    timers.fire()
    await settle()
    expect(updater.feedUrls).toEqual([FEED])
  })

  it('swallows a body that is not JSON, and one too long to accept', async () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    // Both bodies are derived from the bound rather than written out: a literal
    // that stops being over the bound turns this test into one about a body the
    // policy accepts.
    for (const body of ['not json at all', '9'.repeat(MAX_LISTING_BYTES + 1)]) {
      const { controller, updater } = harness({ fetchReleases: () => Promise.resolve(body) })
      await controller.start()
      await settle()
      expect(updater.feedUrls).toEqual([])
      expect(updater.checks).toBe(0)
      expect(logged).toHaveBeenCalledTimes(1)
      logged.mockClear()
    }
  })

  it('swallows a failure the updater reports, and keeps the timer armed', async () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const { controller, updater, timers } = harness()
    await controller.start()
    updater.emit('error', new Error('the download failed'))
    expect(logged).toHaveBeenCalledTimes(1)
    expect(timers.live).toBe(true)
  })
})
