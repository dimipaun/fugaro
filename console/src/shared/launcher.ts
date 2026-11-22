// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Who started a run, as a screen draws it (owner request 2026-10-18: "a
// 'Started by' column", wording from the 2026-10-22 watch spec; the wire field
// is `launched_by`, console/docs/json-contract.md section 2's `JSONRun` row).
//
// `launched_by` is the launcher a run's own `launch.json` recorded at launch
// (`runstore.Launch.LaunchedBy`, written from the task's `requested_by`): a
// person's address, or the identity an automated launch runs as -- a service
// account, a CI runner. It is **display text and not an authority**
// (SECURITY.md D4: every launcher can write `launch.json`), so it is untrusted
// like every other bucket-derived string, and it is a *person's* string, which
// is the class the recorder gives it by its `_by` suffix.
//
// **The whole recorded value is what a screen draws** (D3213, 2026-11-21). This
// module used to cut a launcher at its first `@` -- the CLI's own rule
// (`runview.LauncherShort`, `internal/watch/plain.go`'s `launcherText`), which
// `fugaro runs ls`'s `BY` column still makes -- so `dana@example.com` was drawn
// as `dana`, and a marker the recorder had already written
// (`redacted@example.invalid`) was kept whole as the one exception to it (a cut
// `redacted` reads as a person's *name*, the one thing a redaction must never
// look like). The console is the operator's own tool and an address is the
// answer a column saying who started a run exists to give, so the cut is gone:
// the Live column and the run view's header both draw the identity the record
// holds, and the CLI's own column is now the one place the two forms differ.
//
// What remains here is the one question every screen drawing a launcher asks
// before it puts text on a row -- is there anything to draw at all:
//
//   1. **a row without a launcher draws no element at all**, never another
//      person's name and never an empty span: the Live row's requester cell
//      already has that shape (`live.ts`'s `if (row.requestedBy !== null)`), and
//      an empty span in these flex rows is not invisible -- it adds a `gap` and
//      moves the row. The record's `omitempty` arrives as `''` through
//      `schema.ts`, and that is the same nothing.
//   2. **a value the class would draw as an empty string is that same empty
//      cell.** `blank` below is the question asked of it, in the passes the
//      class makes -- the terminal sequences gone, the invisible characters
//      gone, the whitespace trimmed -- so "blank" here and "the class draws an
//      empty string" are one question asked with the sanitizer's vocabulary
//      rather than a second copy of it. A value of only whitespace, only
//      invisible characters or only a terminal escape sequence would otherwise
//      arrive on the row as `''`.
//
// **The class is the caller's, and this module never names one.** The value is
// an identity either way, and `launched_by` is classified `person` on the wire
// (`schema.ts`'s `WIRE_TEXT_CLASSES`); the Run screen's header draws it in that
// class, while the Live column draws it in `label` -- one line per row, with the
// 200-character bound the column needs and the single-line refusal a cell needs
// (`console/README.md`'s class table and `docs/desktop-console/spec.md` §6.2
// record that one departure from the map). A value the class refuses is not this
// module's business: the caller hands the string to `putText`, which draws its
// own `n/a` and its reason.

import { invisibleRe, stripTerminalSequences } from './sanitize'

/** INVISIBLE_RE is a module-level matcher for the sanitizer's own invisible
 * class, for the same reason `sanitize.ts` and `main/ipc.ts` keep one: it is
 * read on every row of a column, and `replace` with a global matcher does not
 * carry state between calls. */
const INVISIBLE_RE = invisibleRe()

/** blank reports whether a launcher is nothing a cell can draw. The three
 * passes are the label class's own, in its own order -- the terminal sequences
 * gone (`stripTerminalSequences`), the invisible characters gone
 * (`INVISIBLE_RE`), then the whitespace `oneLine` trims -- so "blank" here and
 * "the class draws an empty string" are the same question, asked with the
 * sanitizer's vocabulary rather than a second copy of it. */
function blank(text: string): boolean {
  return stripTerminalSequences(text).replace(INVISIBLE_RE, '').trim() === ''
}

/** launcherFull is a recorded launcher as a screen draws it: **the whole value
 * the record holds** -- a person's address, a service account, a CI runner, a
 * marker the recorder wrote -- or `null` when there is nothing to draw (no
 * value at all, the record's `omitempty`, or a value the class would draw as
 * `''`, which on these rows is an empty span and not an empty cell).
 *
 * `null` means **no element**: a row without a launcher shows an empty cell,
 * never another person's name. The CLI's `BY` column cuts the same value at its
 * first `@` (`runview.LauncherShort`); the console does not, and this module's
 * header is where that divergence is argued. */
export function launcherFull(raw: string | null | undefined): string | null {
  if (typeof raw !== 'string' || raw === '') return null
  return blank(raw) ? null : raw
}
