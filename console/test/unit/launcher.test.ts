// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Who started a run, as the screens draw it: the whole recorded identity, and
// the two shapes that draw nothing (owner request 2026-10-18, wording from the
// 2026-10-22 watch spec, whole values from D3213 of 2026-11-21;
// `console/src/shared/launcher.ts`).
//
// Three things are held here, and each of them is a sentence from the request:
//
//   1. **the whole value is drawn** -- `dana@example.com` is `dana@example.com`,
//      a service account's address is its address, and a display name is its
//      name. This console used to cut at the first `@` (the CLI's own
//      `runview.LauncherShort`), which drew `ana.smith@acme.com` as
//      `ana.smith`: the operator's own tool has no reason to hide the address of
//      the person who started a run, and the CLI's own `BY` column is now the
//      one place the two forms differ. The Go table that once held the console to
//      that cut is left to the Go test (`internal/runview/runview_test.go`,
//      `TestLauncherShort`), which still pins the CLI's column;
//   2. **the string goes through the sanitizer on the text path**, and a
//      launcher is untrusted data (SECURITY.md D4: every launcher can write
//      `launch.json`): the request names the cases to test -- control
//      characters, a very long name, bidi marks, an empty string, a missing key.
//      A value the sanitizer refuses is not drawn, and a value it masks is drawn
//      masked;
//   3. **it fails private**: a row without `launched_by` draws nothing at all --
//      never another person's name -- and the mark a class would draw for a blank
//      is the same empty cell. The screen tests assert the element is absent
//      (`live.test.ts`, `projects.test.ts`); this file asserts the string.
//
// **No line of this file contains a credential-shaped string**: the one input
// that is shaped like a token comes from `test/support/fakeSecrets.ts`, which
// assembles it at run time, for the reason `sanitize.test.ts` states.

import { describe, expect, it } from 'vitest'

import { launcherFull } from '../../src/shared/launcher'
import { ADDRESS_PLACEHOLDER, MAX_LABEL_LENGTH, TOKEN_PLACEHOLDER, sanitizeText } from '../../src/shared/sanitize'
import { githubToken } from '../support/fakeSecrets'

/** drawn is the cell as the screens draw it: the class `launcher.ts`'s header
 * names for the Live column, through the one text door (`app.ts`'s `putText`
 * calls this). */
function drawn(cell: string): string {
  const result = sanitizeText(cell, 'label', 'runs[].launched_by')
  return result.ok ? result.text : `refused: ${result.reason}`
}

describe('launcherFull', () => {
  it('draws the whole recorded value, address and all', () => {
    // The red step of ID2: `ana.smith@acme.com` used to be drawn as
    // `ana.smith`. Every one of these is a launcher a real record carries, and
    // each is drawn as it was written.
    const cases: readonly string[] = [
      'ana.smith@acme.com',
      'dana@example.com',
      'dana.person+fugaro@example.com',
      'fugaro-runner@proj.iam.gserviceaccount.com',
      'Ana Lopez <ana@example.com>',
      'ci-runner',
      'user:ana.smith@acme.com',
    ]
    for (const value of cases) {
      expect(launcherFull(value), value).toBe(value)
    }
  })

  it('keeps a marker the recorder wrote, as it keeps every other value', () => {
    // The marker is no longer a special case: it is simply a value, and the cut
    // that would have turned `redacted@example.invalid` into the bare word
    // `redacted` -- a string that reads as a person's *name* -- is gone.
    expect(launcherFull(ADDRESS_PLACEHOLDER)).toBe(ADDRESS_PLACEHOLDER)
    expect(launcherFull('person-1a2b3c4d')).toBe('person-1a2b3c4d')
  })

  it('draws nothing for a row the record does not name', () => {
    // The request's "fail private", as a value: `null` is no element at all. An
    // empty string is the same state -- the contract's `omitempty` arrives as
    // `''` through `schema.ts`.
    for (const raw of [null, undefined, '']) {
      expect(launcherFull(raw), JSON.stringify(raw)).toBeNull()
    }
  })

  it('draws nothing for a value the class would draw as an empty string', () => {
    // A blank value is the same empty cell as no value at all: the class strips
    // the invisible characters and trims (`stripInvisible`, `oneLine`), so
    // handing it any of these would put an **empty span** on the row -- a flex
    // `gap`, which is exactly the outcome `launcher.ts`'s header rules out. The
    // screen tests read `null` as "no element", so a blank has to be `null` and
    // not `''`. Three shapes, one per pass the class makes: only whitespace, only
    // an invisible character (a zero-width space -- `.trim()` alone would keep
    // it, which is why the check is not just a trim), and only a terminal escape
    // sequence.
    for (const raw of [' ', '  ', '\t', ' ', '​', '\u001b[31m']) {
      expect(launcherFull(raw), JSON.stringify(raw)).toBeNull()
    }
    // Whitespace *around* a name is not blank: the class trims it, so the cell
    // is still drawn.
    expect(launcherFull(' dana@example.com ')).toBe(' dana@example.com ')
  })
})

describe('the launcher string on the text path', () => {
  it('strips a control character and a bidi mark rather than drawing them', () => {
    // Two of the request's cases, through the class the Live cell is drawn in.
    // The control character goes with the other controls (`stripInvisible`), and
    // the bidi override with the format characters: a value drawn with one in it
    // is a value a screen can be made to spell something else.
    expect(drawn(launcherFull('ana\u0000smith@acme.com') ?? '')).toBe('anasmith@acme.com')
    expect(drawn(launcherFull('ana‮smith@acme.com') ?? '')).toBe('anasmith@acme.com')
    expect(drawn(launcherFull('ana​smith@acme.com') ?? '')).toBe('anasmith@acme.com')
  })

  it('refuses a name that is not one line, and one longer than a label', () => {
    // The label class's own two refusals, which are why the Live column draws it
    // in that class rather than in `person`: a cell is one line, and it has a
    // bound. A refused value is drawn as the console's `n/a` with the reason
    // (app.ts's `putText`), never as the value.
    expect(drawn(launcherFull('ana\nsmith@acme.com') ?? '')).toContain('refused:')
    expect(drawn(launcherFull(`${'a'.repeat(MAX_LABEL_LENGTH + 1)}@example.com`) ?? '')).toContain('refused:')
  })

  it('masks a launcher shaped like a credential, and never draws it', () => {
    // A launcher is display text a launcher wrote (SECURITY.md D4), so a value
    // that is shaped like a token is masked in place by the credential rules --
    // the direction the request's "fail private" asks for.
    const token = githubToken()
    const cell = launcherFull(token)
    expect(cell).not.toBeNull()
    const text = drawn(cell ?? '')
    expect(text).not.toContain(token)
    expect(text).toBe(TOKEN_PLACEHOLDER)
    // And where the token is only part of the value, the mask is the
    // credential's alone: the rest is the record's own text, drawn as it was,
    // because the console no longer shortens a launcher on the way to the cell.
    const inAddress = drawn(launcherFull(`${token}@example.com`) ?? '')
    expect(inAddress).not.toContain(token)
    expect(inAddress).toBe(`${TOKEN_PLACEHOLDER}@example.com`)
  })

  it('is drawn by the person class in the run view, whole', () => {
    // The one key this console draws in two classes: the Live column takes
    // `label` for its bound and its single-line rule, and the Run screen's header
    // takes the `person` class the wire key carries. Both draw the same whole
    // value (D3213), and the person class draws an address as the address the
    // record holds.
    const person = sanitizeText('ana.smith@acme.com', 'person', 'runs.[].launched_by')
    expect(person.ok && person.text).toBe('ana.smith@acme.com')
    const name = sanitizeText('Ana Lopez <ana@example.com>', 'person', 'runs.[].launched_by')
    expect(name.ok && name.text).toBe('Ana Lopez <ana@example.com>')
  })
})
