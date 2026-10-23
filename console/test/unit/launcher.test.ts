// The Started-by column's own rules: the short form, and the two shapes that
// draw nothing (owner request 2026-10-18, wording from the 2026-10-22 watch
// spec; `console/src/shared/launcher.ts`).
//
// Three things are held here, and each of them is a sentence from the request:
//
//   1. **the short form is the CLI's own cut** -- `runview.LauncherShort`, the
//      part before an address's first `@`, the whole value for anything else.
//      The table below is the Go test's table (`internal/runview/runview_test.go`,
//      `TestLauncherShort`), value for value, because two renderers that shorten
//      one recorded string two ways are two answers to "who started this run";
//   2. **the person string goes through the sanitizer on the text path**, and a
//      person name is untrusted data: the request names the cases to test --
//      control characters, a very long name, bidi marks, an empty string, a
//      missing key. A value the sanitizer refuses is not drawn, and a value it
//      masks is drawn masked;
//   3. **it fails private**: a row without `launched_by` draws nothing at all --
//      never another person's name -- and the drawn cell never carries the
//      address the record named, because the cut is at the first `@` and no `@`
//      survives it. The screen tests assert the element is absent
//      (`live.test.ts`, `projects.test.ts`); this file asserts the string.
//
// **No line of this file contains a credential-shaped string**: the one input
// that is shaped like a token comes from `test/support/fakeSecrets.ts`, which
// assembles it at run time, for the reason `sanitize.test.ts` states.

import { describe, expect, it } from 'vitest'

import { launcherCell, launcherFull, launcherShort } from '../../src/shared/launcher'
import { ADDRESS_PLACEHOLDER, MAX_LABEL_LENGTH, TOKEN_PLACEHOLDER, sanitizeText } from '../../src/shared/sanitize'
import { githubToken } from '../support/fakeSecrets'

/** drawn is the cell as the screens draw it: the class `launcher.ts`'s header
 * names, through the one text door (`app.ts`'s `putText` calls this). */
function drawn(cell: string): string {
  const result = sanitizeText(cell, 'label', 'runs[].launched_by')
  return result.ok ? result.text : `refused: ${result.reason}`
}

describe('launcherShort', () => {
  it('cuts at the first @, exactly as `runview.LauncherShort` does', () => {
    // The Go table, value for value (`internal/runview/runview_test.go`): the
    // console's column and `fugaro runs ls`'s column must name a run the same
    // way, and this is the pair of tests that keeps them equal.
    const cases: readonly (readonly [string, string])[] = [
      ['dana@example.com', 'dana'],
      ['dana.person+fugaro@example.com', 'dana.person+fugaro'],
      ['fugaro-runner@proj.iam.gserviceaccount.com', 'fugaro-runner'],
      ['ci-runner', 'ci-runner'],
      ['', ''],
      ['@example.com', ''],
      ['a@b@c', 'a'],
    ]
    for (const [input, want] of cases) {
      expect(launcherShort(input), input).toBe(want)
    }
  })

  it('keeps a marker the CLI already wrote whole, rather than cutting it to a name', () => {
    // A scrubbed fixture records `redacted@example.invalid` for a launcher.
    // Cutting it would draw the bare word `redacted`, which reads as a person's
    // name -- the one thing a redaction must never look like. Every class in
    // `sanitize.ts` keeps a marker as it is; this one does too.
    expect(launcherShort(ADDRESS_PLACEHOLDER)).toBe(ADDRESS_PLACEHOLDER)
    expect(launcherShort('person-1a2b3c4d')).toBe('person-1a2b3c4d')
  })
})

describe('launcherCell', () => {
  it('draws nothing for a row the record does not name', () => {
    // The request's "fail private", as a value: `null` is no element at all. An
    // empty string is the same state -- the contract's `omitempty` arrives as
    // `''` through `schema.ts` -- and so is an address with nothing before its
    // `@`, which shortens to nothing rather than to a name.
    for (const raw of [null, undefined, '', '@example.com']) {
      expect(launcherCell(raw), JSON.stringify(raw)).toBeNull()
    }
  })

  it('draws the short form of a recorded launcher', () => {
    expect(launcherCell('dana@example.com')).toBe('dana')
    expect(launcherCell('dana.person+fugaro@example.com')).toBe('dana.person+fugaro')
    expect(launcherCell('ci-runner')).toBe('ci-runner')
    expect(launcherCell('fugaro-runner@proj.iam.gserviceaccount.com')).toBe('fugaro-runner')
  })

  it('never carries the address it was given, whatever the shape', () => {
    // The cut is at the first `@`, so no `@` -- and therefore no domain, and no
    // address -- survives it. This is the property that lets the cell be drawn
    // in a class that is not `person` (`launcher.ts`'s header): there is no
    // address left for the class's address rule to find.
    const shapes = [
      'dana@example.com',
      'dana.person+fugaro@example.com',
      'fugaro-runner@proj.iam.gserviceaccount.com',
      'a@b@c',
      '"dana"@example.com',
    ]
    for (const shape of shapes) {
      const cell = launcherCell(shape)
      expect(cell, shape).not.toBeNull()
      expect(cell, shape).not.toContain('@')
    }
  })
})

describe('the person string on the text path', () => {
  it('strips a control character and a bidi mark rather than drawing them', () => {
    // Two of the request's cases, through the class the cell is drawn in. The
    // control character goes with the other controls (`stripInvisible`), and
    // the bidi override with the format characters: a value drawn with one in it
    // is a value a screen can be made to spell something else.
    expect(drawn(launcherCell('dana\u0000lopez@example.com') ?? '')).toBe('danalopez')
    expect(drawn(launcherCell('dana‮lopez@example.com') ?? '')).toBe('danalopez')
    expect(drawn(launcherCell('dana​lopez@example.com') ?? '')).toBe('danalopez')
  })

  it('refuses a name that is not one line, and one longer than a label', () => {
    // The label class's own two refusals, which are why it is the class here:
    // a cell is one line, and it has a bound. A refused value is drawn as the
    // console's `n/a` with the reason (app.ts's `putText`), never as the value.
    expect(drawn(launcherCell('dana\nlopez@example.com') ?? '')).toContain('refused:')
    expect(drawn(launcherCell(`${'a'.repeat(MAX_LABEL_LENGTH + 1)}@example.com`) ?? '')).toContain('refused:')
  })

  it('masks a launcher shaped like a credential, and never draws it', () => {
    // A launcher is display text a launcher wrote (SECURITY.md D4), so a value
    // that is shaped like a token is masked in place by the free-text rules --
    // the direction the request's "fail private" asks for.
    const token = githubToken()
    const cell = launcherCell(`${token}@example.com`)
    expect(cell).not.toBeNull()
    const text = drawn(cell ?? '')
    expect(text).not.toContain(token)
    expect(text).toBe(TOKEN_PLACEHOLDER)
  })

  it('draws a dotted local part and a name, which the person class would refuse', () => {
    // The reason the cell is not drawn in `person`: that class admits a bare
    // address or a login `[A-Za-z0-9-]{1,39}` and refuses everything else, so
    // `dana.lopez` -- the commonest corporate shape -- would be a refused cell
    // on every row of the column the owner asked for.
    expect(drawn(launcherCell('dana.lopez@example.com') ?? '')).toBe('dana.lopez')
    expect(drawn(launcherCell('Ana Lopez <ana@example.com>') ?? '')).toBe('Ana Lopez <ana')
    const refused = sanitizeText('dana.lopez', 'person', 'runs[].launched_by')
    expect(refused.ok).toBe(false)
  })
})

describe('launcherFull', () => {
  it('carries the whole recorded identity, and nothing for a row without one', () => {
    // The run view's form: the owner's "the full identity only in the expanded
    // or run view". The cut is the column's, not the model's.
    expect(launcherFull('dana@example.com')).toBe('dana@example.com')
    expect(launcherFull('fugaro-runner@proj.iam.gserviceaccount.com')).toBe('fugaro-runner@proj.iam.gserviceaccount.com')
    for (const raw of [null, undefined, '']) {
      expect(launcherFull(raw), JSON.stringify(raw)).toBeNull()
    }
  })
})
