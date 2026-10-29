// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The run's failure path: the one sentence a failed run ends with, and the
// handler that makes Node print that sentence rather than dying on a library's
// own stack.
//
// **Why a handler is needed at all.** `main()` catches what it awaits, but the
// launch it awaits is Playwright's, and a launch that fails rejects on promises
// nobody holds: `_electron.launch` reports the child's exit, its stderr and its
// timeout as rejections of its own, alongside the one the caller awaits. Under
// Node 22's default `--unhandled-rejections=throw` the first of those ends the
// process with a playwright-core stack -- so the owner sees a library's
// internals and no exit code, and the sentence this run promises ("the
// screenshot run failed: …") is never printed. The handler below is what makes
// that promise hold for every failure, not only the awaited ones.
//
// **Why it sets the exit code rather than exiting.** A rejection can arrive
// while the run is still recovering -- `test/support/launch.ts` retries a launch
// that failed on a text file busy, and Playwright reports the failed attempt
// before the retry succeeds -- and a handler that called `process.exit(1)` would
// kill a run that was about to work. Setting the exit code fails the run either
// way and lets the retry finish.
//
// **Why it is a file of its own.** `capture.ts` cannot be imported by a test:
// its bootstrap is `require.main`, and its import graph reaches Playwright's
// `_electron` as a value. This module is neither, so the unit suite drives it
// with a stand-in process (`test/unit/screenshots.test.ts`, §6) -- which is how
// the sentence, the event name and the exit code are held to something.

/** FAILURE_SENTENCE is the opening of the one line a failed run prints. It is
 * exported so the test asserts the words rather than a copy of them, and so the
 * entry point cannot grow a second sentence that says the same thing. */
export const FAILURE_SENTENCE = 'the screenshot run failed: '

/** RunProcess is the slice of `process` this module uses. It is an interface
 * rather than `NodeJS.Process` so a test can pass a stand-in and read what the
 * run would have printed and what it would have exited with. */
export interface RunProcess {
  on(event: 'unhandledRejection', listener: (reason: unknown) => void): unknown
  stderr: { write(chunk: string): unknown }
  exitCode: number | string | null | undefined
}

/** failureLine is what a failed run prints, whatever the failure was: a whole
 * line, because Playwright's own stderr and this run's report are written to the
 * same stream by different writers and neither may run into the other.
 *
 * A reason that is not an `Error` is printed as itself rather than as
 * `[object Object]`-shaped guesswork: `String` on a string is the string, and a
 * rejection reason is very often a string. */
export function failureLine(reason: unknown): string {
  return `\n${FAILURE_SENTENCE}${reason instanceof Error ? reason.message : String(reason)}\n`
}

/** installFailureHandler makes an unhandled rejection a failed run: the
 * sentence on stderr and a non-zero exit code, with no stack trace and no
 * rethrow.
 *
 * The listener must not throw. `emit` runs listeners in order and a throw here
 * would leave the process in the state this handler exists to prevent. */
export function installFailureHandler(target: RunProcess): void {
  target.on('unhandledRejection', (reason: unknown) => {
    target.stderr.write(failureLine(reason))
    target.exitCode = 1
  })
}

/** exitCodeFor is the code the process ends with, given the code the work
 * returned. A run that saw an unhandled rejection did **not** succeed, even if
 * the work it was doing recovered from one -- so a `0` from `main()` does not
 * clear the handler's `1`. Anything else is the work's own answer. */
export function exitCodeFor(code: number, target: RunProcess): number {
  return code === 0 && target.exitCode === 1 ? 1 : code
}
