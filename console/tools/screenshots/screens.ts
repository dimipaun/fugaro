// What the screenshot run captures: one entry per picture, its alt text, the
// selector the run waits for, and the two facts about the set (the size every
// capture is taken at, and the picture the owner's ruling names that this run
// does not take).
//
// It is a file of its own rather than a block inside `capture.ts` because it is
// the half of the run that can be *read* without a display and without
// Playwright: the unit suite holds these names, alt texts and sizes against the
// fixtures (`test/unit/screenshots.test.ts`), and `capture.ts` -- which launches
// Electron and is therefore never imported by a test -- imports them from here.

import { CAPTURE_WINDOW } from './manifest'
import type { Omission } from './manifest'

/** RUN_ID is the run the run-detail picture is of: the first row of the live
 * fixture's `runs[]`, and the run `diagnose.json` is the answer for. The unit
 * suite holds the three files to this one id, because the run detail is reached
 * by clicking the Live screen's row and draws the diagnosis it asked for -- two
 * files naming two runs would be a picture of a run nobody clicked. */
export const RUN_ID = '20261020-103045-aa01'

/** OneScreen is one capture: its file name, its alt text, and the selector the
 * run waits for.
 *
 * The selector is deliberately **not** the screen's root (`section.live` is
 * drawn the moment the panel switches, before any read has answered) but an
 * element only a *drawn* screen has: a run row, a log line, a point of the
 * report's curve, a row of the picker's list. A capture taken at the root's
 * arrival would be a picture of the console's skeleton. */
export interface OneScreen {
  readonly file: string
  readonly alt: string
  readonly drawn: string
}

/** SCREENS is the captures of the dashboard's launch, in the order they are
 * taken: the Live screen as it draws on arrival, then the run the Live screen's
 * first row names, then Spend.
 *
 * **The alt text is drafted from the fixture, and says what the picture shows.**
 * It is written here rather than derived from the screen because the fixture is
 * what is being drawn, and a sentence a program assembled from the document
 * would be a worse description than one a person wrote while looking at the
 * fixture. Every fact in it is one the screen actually draws -- the Live screen
 * draws `document.runs`, the two `repos` rows and the `total` block, and
 * nothing else from the frame (`src/renderer/screens/live.ts`); the frame's
 * `finished[]` list is decoded and never drawn, so a sentence that counted it
 * would describe a screen nobody took a picture of.
 */
export const LIVE_SCREEN: OneScreen = Object.freeze({
  file: 'console-live.png',
  drawn: '.live-run',
  alt: "The console's Live screen for a fictional acme-platform installation: three runs -- one implementing the second round of a scheduler port, one in review and close to its deadline, one queued -- two repositories, and the day's spend at 34.85% of a $120 daily cap.",
})

/** RUN_SCREEN is the run detail, reached from the Live screen's own row. The
 * log line is what the capture waits for: the diagnosis and the log window are
 * two reads, and the screen is only whole once both have drawn. */
export const RUN_SCREEN: OneScreen = Object.freeze({
  file: 'console-run.png',
  drawn: '.run-log-line',
  alt: 'The run-detail screen for run 20261020-103045-aa01 (acme/platform-core): its stage ledger, its build, test and lint verification, three findings and the tail of its log.',
})

/** SPEND_SCREEN is the spend view. `.spend-point` is a point of the report's
 * curve: the caps panel draws from the budget read and the curve from the report
 * read, so waiting for a point is waiting for the second of the two. */
export const SPEND_SCREEN: OneScreen = Object.freeze({
  file: 'console-spend.png',
  drawn: '.spend-point',
  alt: 'The Spend screen for a fictional acme-platform installation: thirty days of daily spend, the account totals, and the per-repository and default daily and per-run caps.',
})

/** SCREENS is the dashboard's three captures, in the order they are taken. */
export const SCREENS: readonly OneScreen[] = Object.freeze([LIVE_SCREEN, RUN_SCREEN, SPEND_SCREEN])

/** PICKER_SCREEN is the fourth capture, from the **same launch** as the other
 * three: the picker is opened from the console's own `Installations…` menu item
 * (C9's one door), so the console under the camera is the console already
 * running against the fixture and its chosen installation.
 *
 * **The rail is not this picture's subject and cannot be.** `drawRail`
 * (`src/renderer/entry.ts`) draws one row -- the active installation -- because
 * `installation.current` is the only installation read the dashboard's bridge
 * has (design section 4.1, section 11); a row per *stored* installation would
 * need a list read this console does not have. The three installations the
 * fixture holds are therefore drawn where the console draws them: the picker.
 *
 * The sentence is `rowText`'s and `viewOf`'s own (`src/renderer/installations.ts`):
 * each row is `project — binary · …`, the chosen one carries `active`, a row
 * nobody has probed carries `(unknown)`, and the status line names the active
 * project. The binary path in that row is the fake in this run's scratch tree,
 * which is a different path on every run -- so the alt describes it rather than
 * quoting it.
 */
export const PICKER_SCREEN: OneScreen = Object.freeze({
  file: 'console-installations.png',
  drawn: '#picker-list li',
  alt: 'The installation picker on a console that has three installations -- acme-platform, acme-web and acme-data -- each naming the fixture CLI binary this run installed in its own temporary directory; acme-platform is the one this console is showing and the other two have not been read.',
})

/** OMISSIONS is what this run does not capture, and why. It is a *fact* the
 * manifest carries rather than a silence: a reader who expected
 * `console-first-run.png` because the owner's ruling named it finds the reason
 * here instead of wondering whether the run failed. */
export const OMISSIONS: readonly Omission[] = Object.freeze([
  {
    file: 'console-first-run.png',
    because:
      'the no-installation onboarding page is #751, which is not on main: there is no onboarding screen in this console to capture, and the first-run picture this run does take is the installation picker D13 opens.',
  },
])

/** CAPTURE_PIXELS is the size a capture has to be: the window's CSS size times
 * the device scale factor. `png.ts` refuses a file that is not exactly this, so
 * a window that came up at another size fails the run rather than being
 * described as the size the manifest declares. */
export const CAPTURE_PIXELS = Object.freeze({
  width: CAPTURE_WINDOW.width * CAPTURE_WINDOW.deviceScaleFactor,
  height: CAPTURE_WINDOW.height * CAPTURE_WINDOW.deviceScaleFactor,
})
