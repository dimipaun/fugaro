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
// The owner's sentence is the whole specification of the drawn form: "the
// person's name, else the email local part (short form in the screens); the
// full identity only in the expanded or run view and in JSON". Two decisions
// fall out of it, and both are this module's:
//
//   1. **the cut is the CLI's own rule** (`runview.LauncherShort`): the part
//      before an address's first `@`, and the whole value for anything else --
//      a service account name, a CI identity, whatever an automated launch
//      recorded. The console's column and `fugaro runs ls`'s column therefore
//      name a run the same way (`dana@example.com` is `dana`, which is what
//      tells two colleagues' runs apart), and the owner's "the email local
//      part" is what the console really draws. An address with nothing before
//      its `@` shortens to `''`: that is the empty cell, and the caller draws
//      no element for it.
//   2. **the shortened string is drawn as a `label`, not as a `person`.** The
//      `person` class draws a bare address (as the console's marker) or a login
//      `[A-Za-z0-9-]{1,39}`, and refuses everything else -- so a dotted local
//      part (`dana.lopez@example.com`, the commonest corporate shape) and a
//      display name would be a refused cell on every row, and the column the
//      owner asked for would read `n/a` for most of the people who use it. The
//      short form is **not an address**: the cut is at the first `@`, so no `@`
//      survives it and the label class's address rule cannot fire on it. What
//      the class is here for is what a cell needs and the owner's test list
//      names -- the invisible characters (a bidi override, a zero-width space)
//      are stripped, a value that is not one line or is longer than
//      `MAX_LABEL_LENGTH` is refused rather than drawn, and a credential shape
//      is masked by the free-text rules like every other label. **The whole
//      value, in the Run header, is a `person`**: that is the class the wire
//      key carries (`WIRE_TEXT_CLASSES`), so an address is the console's marker
//      there as it is in every other person cell, and no address is ever drawn.
//
// A row without a launcher draws **no element at all**, never another person's
// name and never an empty span: the Live row's requester cell already has that
// shape (`live.ts`'s `if (row.requestedBy !== null)`), and an empty span in
// these flex rows is not invisible -- it adds a `gap` and moves the row.

import { isMarkerText } from './sanitize'

/** launcherShort is a recorded launcher as a column shows it: the CLI's own
 * short form (`runview.LauncherShort`, the part before an address's first `@`,
 * the whole value for anything else).
 *
 * A marker the CLI already wrote is kept whole rather than shortened: the
 * scrubbed fixtures carry `redacted@example.invalid` for a launcher, and a cut
 * `redacted` would draw as a person's name. Every class in `sanitize.ts` keeps
 * a marker as it is, for that reason. */
export function launcherShort(raw: string): string {
  if (isMarkerText(raw)) return raw
  const at = raw.indexOf('@')
  return at === -1 ? raw : raw.slice(0, at)
}

/** launcherCell is the text a column draws for a recorded launcher, or `null`
 * when there is nothing to draw: no value at all (the record has none, which is
 * the contract's `omitempty`), or an address with nothing before its `@`.
 *
 * `null` means **no element**: a row without a launcher shows an empty cell,
 * never another person's name. A value the sanitizer *refuses* is not this
 * function's business -- the caller hands the string to `putText` in the class
 * this module's header names, which draws its own `n/a` and its reason. */
export function launcherCell(raw: string | null | undefined): string | null {
  if (typeof raw !== 'string' || raw === '') return null
  const short = launcherShort(raw)
  return short === '' ? null : short
}

/** launcherFull is the text the Run screen's header draws: the **whole**
 * recorded identity, which is what the owner's "the full identity only in the
 * expanded or run view" asks for, drawn through the `person` class -- so an
 * address is the console's marker and a login is itself. `null` is the same
 * "nothing to draw" as `launcherCell`'s. */
export function launcherFull(raw: string | null | undefined): string | null {
  return typeof raw === 'string' && raw !== '' ? raw : null
}
