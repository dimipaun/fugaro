// ETXTBSY (text file busy), the one launch failure `test/support/launch.ts`
// retries -- and the reason the retry is bounded to it and to nothing else.
//
// Linux refuses to `exec` a file that some process still holds open for
// writing. The console's e2e run meets that state for one reason only: the
// Electron runtime is not on disk after `npm ci --ignore-scripts` (design
// section 5.2; console/README.md, "Commands"), so the electron package
// downloads and unpacks it the first time something resolves the binary
// (`node_modules/electron/index.js` spawns its own `install.js` when `dist/`
// is missing). `test/support/globalSetup.ts` is what makes that happen once,
// before any worker exists -- but the failure this module answers for was real
// in CI (the `console (ubuntu-latest)` job of run 39174336894, attempt 1:
// `Error: electron.launch: spawn ETXTBSY`, followed by the cleanup's
// `TypeError: Cannot read properties of undefined (reading 'close')`), and a
// test that fails on a two-line race is a release gate that blocks a release
// for no reason.
//
// So the launch helper retries ETXTBSY, and **only** ETXTBSY:
//
//   - three attempts, 250 ms apart: a bounded wait for the writer -- a download
//     that is still unpacking -- to finish, not a general tolerance for a
//     flaky machine. Three attempts at a quarter second is a one-second window
//     around a race that resolves in milliseconds once the writer is done;
//   - every other error, ENOENT and the refused-handshake ones included, is
//     rethrown on the first attempt unchanged. A launch that failed because the
//     app refused a version, because a profile was locked, or because the
//     binary is genuinely missing must fail the spec with its own message and
//     at once, not be retried into a timeout;
//   - when the attempts are exhausted the helper throws **one** clear error
//     naming the errno, the number of attempts and the last underlying error,
//     which is the last ETXTBSY as its `cause`. "Failed after 3 attempts" with
//     the real errno in it is a diagnosis; the raw ETXTBSY alone is not.
//
// The predicate reads the errno two ways on purpose. Node's own `spawn` sets
// `code: 'ETXTBSY'` (that is the shape `spawnSync` reports, and the shape this
// repository's repro measures), but Playwright rewrites the message of the
// error it rethrows -- `Error: electron.launch: spawn ETXTBSY ... Call log:`
// is the message CI shows -- and a predicate that only read `code` would be
// untested against the spelling the runner actually produces. Both readings are
// narrow: the token, or the errno, never a substring of a longer word.

/** The number of `launch` attempts, the first one included. */
export const TEXT_FILE_BUSY_ATTEMPTS = 3

/** How long the helper waits between two attempts, in milliseconds. */
export const TEXT_FILE_BUSY_DELAY_MS = 250

/** errnoOf is an error's `code`, when it has one: Node's spawn errors carry
 * `'ETXTBSY'` there, and Playwright's wrappers may not carry it at all. */
function errnoOf(error: unknown): string | undefined {
  if (typeof error !== 'object' || error === null) return undefined
  const code = (error as { readonly code?: unknown }).code
  return typeof code === 'string' ? code : undefined
}

/** isTextFileBusy reports whether this error is the one the retry exists for:
 * `ETXTBSY`, however it was spelled. */
export function isTextFileBusy(error: unknown): boolean {
  if (errnoOf(error) === 'ETXTBSY') return true
  return error instanceof Error && /\bETXTBSY\b/.test(error.message)
}

/** sleep is the real wait; the unit test passes its own, so the retry's shape
 * is asserted without spending the second it describes. */
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/** messageOf is an error's message, or its `String` for a value thrown that is
 * not an `Error` at all -- a `throw 'nope'` must still reach the report. */
function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

/**
 * retryOnTextFileBusy runs `launch` until it succeeds, it fails with something
 * that is not ETXTBSY, or the attempts run out.
 *
 * It is the whole of the retry policy: the launch helper passes Playwright's
 * `electron.launch` and nothing else, and the unit test passes a fake that
 * throws what it is told to throw.
 */
export async function retryOnTextFileBusy<T>(
  launch: () => Promise<T>,
  wait: (ms: number) => Promise<void> = sleep,
): Promise<T> {
  let last: unknown
  for (let attempt = 1; attempt <= TEXT_FILE_BUSY_ATTEMPTS; attempt += 1) {
    try {
      return await launch()
    } catch (error) {
      if (!isTextFileBusy(error)) throw error
      last = error
      if (attempt < TEXT_FILE_BUSY_ATTEMPTS) await wait(TEXT_FILE_BUSY_DELAY_MS)
    }
  }
  throw new Error(
    `electron.launch was refused with ETXTBSY ${String(TEXT_FILE_BUSY_ATTEMPTS)} times, ` +
      `${String(TEXT_FILE_BUSY_DELAY_MS)} ms apart: the Electron binary ` +
      `(console/node_modules/electron/dist/electron) was still open for writing when the ` +
      `launch tried to exec it, which is the electron package unpacking itself while a ` +
      `launch raced it. Last error: ${messageOf(last)}`,
    { cause: last },
  )
}
