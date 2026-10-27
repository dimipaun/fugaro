// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The launch helper's ETXTBSY retry, driven with a fake spawn (review of the
// `console (ubuntu-latest)` flake: `Error: electron.launch: spawn ETXTBSY`).
//
// `test/support/textFileBusy.ts` is what `test/support/launch.ts` wraps around
// Playwright's `electron.launch`, and it is deliberately a pure function of a
// launch and a wait: this file passes a fake that throws exactly what it is
// told to throw and a wait that records its argument instead of sleeping, so
// the retry's *shape* -- how many attempts, how far apart, which errors are
// retried, what the exhausted case reports -- is asserted without a display, an
// Electron runtime or a second of real time.
//
// Nothing here launches Electron, and that is the point: on a machine with no
// Chromium libraries (a container, this one included) the e2e suite cannot run
// at all, and the retry policy would otherwise be the one part of this fix with
// no test behind it.

import { describe, expect, it } from 'vitest'

import {
  TEXT_FILE_BUSY_ATTEMPTS,
  TEXT_FILE_BUSY_DELAY_MS,
  isTextFileBusy,
  retryOnTextFileBusy,
} from '../support/textFileBusy'

/** busy is ETXTBSY as Node's own `spawn` reports it: the errno in `code`, and a
 * message naming the executable. */
function busy(where = '/tmp/node_modules/electron/dist/electron'): Error {
  const error = new Error(`spawn ${where} ETXTBSY`) as Error & { code?: string }
  error.code = 'ETXTBSY'
  return error
}

/** busyAsPlaywrightSpellsIt is the same failure as it reaches a spec: what CI's
 * log shows is `Error: electron.launch: spawn ETXTBSY` with a Call log under
 * it, and no `code` this test may rely on. The retry has to recognise this
 * spelling too, or it would never fire on the runner it exists for. */
function busyAsPlaywrightSpellsIt(): Error {
  return new Error(
    'electron.launch: spawn ETXTBSY\nCall log:\n' +
      '  - <launching> /tmp/node_modules/electron/dist/electron --no-sandbox /tmp/console\n',
  )
}

/** recorder is the fake spawn: it throws the errors it was given, one per
 * attempt, and returns `result` once they run out. It records how many times it
 * was asked, which is the count every assertion below is really about. */
function recorder(errors: readonly Error[], result = 'the app'): { launch: () => Promise<string>; attempts: () => number } {
  let attempts = 0
  return {
    launch: (): Promise<string> => {
      const next = errors[attempts]
      attempts += 1
      if (next !== undefined) return Promise.reject(next)
      return Promise.resolve(result)
    },
    attempts: () => attempts,
  }
}

/** waits records the delays instead of sleeping through them. */
function waits(): { wait: (ms: number) => Promise<void>; delays: number[] } {
  const delays: number[] = []
  return {
    wait: (ms: number): Promise<void> => {
      delays.push(ms)
      return Promise.resolve()
    },
    delays,
  }
}

describe('retryOnTextFileBusy', () => {
  it('recovers from ETXTBSY, waiting the documented delay between attempts', async () => {
    const spawn = recorder([busy(), busy()])
    const clock = waits()

    await expect(retryOnTextFileBusy(spawn.launch, clock.wait)).resolves.toBe('the app')

    expect(spawn.attempts(), 'the launch is retried until it succeeds').toBe(3)
    expect(clock.delays, 'each retry waits 250 ms, and only between attempts').toEqual([
      TEXT_FILE_BUSY_DELAY_MS,
      TEXT_FILE_BUSY_DELAY_MS,
    ])
    expect(TEXT_FILE_BUSY_ATTEMPTS, 'three attempts, the first one included').toBe(3)
    expect(TEXT_FILE_BUSY_DELAY_MS).toBe(250)
  })

  it('recovers from the spelling Playwright rethrows: the errno in the message, with no code', async () => {
    // The shape CI's log shows. If the predicate read only `code`, this is the
    // test that would fail -- and it is the spelling the runner actually
    // produces, so a retry that missed it would be no fix at all.
    const spawn = recorder([busyAsPlaywrightSpellsIt()])
    const clock = waits()

    await expect(retryOnTextFileBusy(spawn.launch, clock.wait)).resolves.toBe('the app')

    expect(spawn.attempts()).toBe(2)
    expect(clock.delays).toEqual([TEXT_FILE_BUSY_DELAY_MS])
  })

  it('does not retry another error: it is rethrown at once, unchanged', async () => {
    // ENOENT is the *other* half of the same race (the file is not written yet)
    // and is deliberately NOT retried: the global setup is what makes the
    // binary present, and a retry that swallowed a genuinely missing runtime
    // would hide a broken install behind a timeout.
    const missing = new Error('Failed to launch: Error: spawn /tmp/electron ENOENT') as Error & { code?: string }
    missing.code = 'ENOENT'
    const spawn = recorder([missing])
    const clock = waits()

    await expect(retryOnTextFileBusy(spawn.launch, clock.wait)).rejects.toBe(missing)

    expect(spawn.attempts(), 'one attempt, no retry').toBe(1)
    expect(clock.delays, 'and no wait at all').toEqual([])
  })

  it('does not retry an error that is not an errno: the app refusing a read is not a busy file', async () => {
    const refused = new Error('the CLI is outside the supported range')
    const spawn = recorder([refused])
    const clock = waits()

    await expect(retryOnTextFileBusy(spawn.launch, clock.wait)).rejects.toBe(refused)

    expect(spawn.attempts()).toBe(1)
    expect(clock.delays).toEqual([])
  })

  it('reports the last ETXTBSY, with the attempts and the delay, when the retries run out', async () => {
    const first = busy('/tmp/first/electron')
    const second = busy('/tmp/second/electron')
    const last = busy('/tmp/last/electron')
    const spawn = recorder([first, second, last])
    const clock = waits()

    const thrown = await retryOnTextFileBusy(spawn.launch, clock.wait).then(
      () => null,
      (error: unknown) => error,
    )

    expect(thrown, 'three busy launches must fail rather than resolve').toBeInstanceOf(Error)
    const reported = thrown as Error & { cause?: unknown }
    expect(reported.message).toContain('ETXTBSY')
    expect(reported.message).toContain('3 times')
    expect(reported.message).toContain(`${String(TEXT_FILE_BUSY_DELAY_MS)} ms apart`)
    // The last underlying error, not the first: a diagnosis that quoted the
    // first attempt would point at a launch whose state is long gone.
    expect(reported.message).toContain(last.message)
    expect(reported.cause, 'the last error is the cause').toBe(last)
    expect(spawn.attempts()).toBe(TEXT_FILE_BUSY_ATTEMPTS)
    expect(clock.delays).toHaveLength(TEXT_FILE_BUSY_ATTEMPTS - 1)
  })
})

describe('isTextFileBusy', () => {
  it('reads the errno and the message, and neither one loosely', () => {
    expect(isTextFileBusy(busy())).toBe(true)
    expect(isTextFileBusy(busyAsPlaywrightSpellsIt())).toBe(true)

    // Word-boundary, not substring: a code or a message that merely contains
    // the letters is not this errno, and retrying it would be the "any other
    // launch error" this retry must never touch.
    const nearMissCode = new Error('spawn /tmp/electron ETXTBSYX') as Error & { code?: string }
    nearMissCode.code = 'ETXTBSYX'
    expect(isTextFileBusy(nearMissCode)).toBe(false)
    expect(isTextFileBusy(new Error('the file said ETXTBSYX'))).toBe(false)
    expect(isTextFileBusy(new Error('the file said XETXTBSY'))).toBe(false)

    expect(isTextFileBusy(new Error('spawn /tmp/electron ENOENT'))).toBe(false)
    expect(isTextFileBusy(undefined)).toBe(false)
    expect(isTextFileBusy('ETXTBSY')).toBe(false)
  })
})
