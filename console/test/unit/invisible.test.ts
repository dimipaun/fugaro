// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// invisible.test.ts holds the three readers of CLI text -- the recorder's
// redactor (`src/source/redact.ts`), the display layer (`src/shared/sanitize.ts`)
// and the IPC argument check (`src/main/ipc.ts`) -- to one set of invisible
// characters, walked over the whole Unicode range rather than listed.
//
// The set a reader must treat as invisible is every format character (Cf),
// every control but tab, line feed and carriage return (Cc), the line and
// paragraph separators (Zl, Zp), every default-ignorable code point (variation
// selectors, tag characters, fillers), every bidi control, every private-use
// code point (Co) -- the console's own sentinel hold is built from two of them,
// and nothing draws any of them -- the interlinear annotation characters
// U+FFF9..U+FFFB and the Mongolian vowel separator. A character in that set that
// one layer strips and another keeps is a place a credential can be split so
// that the recorder writes it into a committed fixture while the screen joins it
// back together -- which is what U+FFF9 did.

import { describe, expect, it } from 'vitest'

import { validateArguments } from '../../src/main/ipc'
import { sanitizeText } from '../../src/shared/sanitize'
import { auditRedacted, createRedactor } from '../../src/source/redact'
import * as fake from '../support/fakeSecrets'

/** EXPECTED is the property, written from the categories rather than from any
 * layer's own definition, so a layer that narrows its set fails here. */
const EXPECTED =
  /[\p{Cf}\p{Co}\p{Cc}\p{Zl}\p{Zp}\p{Default_Ignorable_Code_Point}\p{Bidi_Control}\u{fff9}-\u{fffb}\u{180e}\u{e0000}-\u{e007f}\u{fe00}-\u{fe0f}\u{e0100}-\u{e01ef}]/u

/** KEPT are the three controls every layer handles as line structure, not as
 * invisible characters. */
const KEPT = new Set([0x09, 0x0a, 0x0d])

/** PRIVATE_USE matches the private-use code points, the one family the walk
 * samples rather than enumerates: it is six thousand BMP code points plus two
 * whole planes, uniform by construction and each one the same test, and every
 * point the walk keeps is run through four layers. A stride keeps the property
 * -- no layer strips one of them and keeps another -- while the suite stays
 * seconds rather than minutes. Every other family is walked whole. */
const PRIVATE_USE = /\p{Co}/u
const PRIVATE_USE_STRIDE = 613

/** ALWAYS_WALKED are the private-use code points the walk keeps whatever the
 * stride: the two the console's own sentinel hold is built from (`sanitize.ts`),
 * and the first and last of the family in the BMP and in a supplementary
 * plane. */
const ALWAYS_WALKED = new Set([0xe000, 0xe001, 0xf8ff, 0xf0000, 0x10fffd])

function invisibleCodePoints(): number[] {
  const found: number[] = []
  for (let point = 0; point <= 0x10ffff; point += 1) {
    if (point >= 0xd800 && point <= 0xdfff) continue
    if (KEPT.has(point)) continue
    if (PRIVATE_USE.test(String.fromCodePoint(point))) {
      if (!ALWAYS_WALKED.has(point) && point % PRIVATE_USE_STRIDE !== 0) continue
    }
    if (EXPECTED.test(String.fromCodePoint(point))) found.push(point)
  }
  return found
}

const POINTS = invisibleCodePoints()

function name(point: number): string {
  return `U+${point.toString(16).toUpperCase().padStart(4, '0')}`
}

describe('one set of invisible characters across the recorder, the display and the IPC layer', () => {
  it('walks a set that carries every family the set exists for', () => {
    for (const point of [0x00ad, 0x061c, 0x180e, 0x200b, 0x202e, 0x2028, 0x2029, 0x2066, 0xfe0f, 0xfeff, 0xfff9, 0xfffa, 0xfffb, 0xe0041, 0xe0100, 0x0001, 0x0085, 0xe000, 0xe001, 0xf8ff, 0xf0000, 0x10fffd]) {
      expect(POINTS, name(point)).toContain(point)
    }
  })

  it('the recorder redacts a credential split by any of them, and its audit refuses the unredacted form', () => {
    const redactor = createRedactor()
    const key = fake.awsKeyId()
    const head = key.slice(0, 4)
    const tail = key.slice(4)
    const missed: string[] = []
    const unaudited: string[] = []
    for (const point of POINTS) {
      const split = `key ${head}${String.fromCodePoint(point)}${tail} here`
      if (redactor.redactText(split).includes(tail)) missed.push(name(point))
      if (auditRedacted({ t: split }).length === 0) unaudited.push(name(point))
    }
    expect(missed, 'split credentials the recorder left in place').toEqual([])
    expect(unaudited, 'split credentials the audit passed').toEqual([])
  })

  it('the recorder redacts an address split by an interlinear annotation character', () => {
    const redactor = createRedactor()
    for (const point of [0xfff9, 0xfffa, 0xfffb]) {
      const split = `mail ana${String.fromCodePoint(point)}@acme.com now`
      const out = redactor.redactText(split)
      expect(out, name(point)).not.toContain('acme.com')
      expect(auditRedacted({ t: split }).length, name(point)).toBeGreaterThan(0)
    }
  })

  it('the display layer strips every one of them', () => {
    // `a` + ESC + `b` is the escape sequence `ESC b`, removed whole, so the
    // property is that the character is gone and what came before it is drawn,
    // not that the result is exactly `ab`.
    const kept: string[] = []
    for (const point of POINTS) {
      const character = String.fromCodePoint(point)
      const result = sanitizeText(`a${character}b`, 'sentence', 'the value')
      if (!result.ok || result.text.includes(character) || !result.text.startsWith('a')) kept.push(name(point))
    }
    expect(kept).toEqual([])
  })

  it('the IPC layer refuses an argument carrying any of them', () => {
    // The refusal must be the hidden-character check's, which names the code
    // point: the repository grammar refuses any non-ASCII value on its own, so
    // `ok: false` alone would hold for a check that saw nothing.
    const unnamed: string[] = []
    for (const point of POINTS) {
      const decision = validateArguments('runs.list', { repo: `acme/a${String.fromCodePoint(point)}pp` })
      if (decision.ok || !decision.reason.includes(name(point))) unnamed.push(name(point))
    }
    expect(unnamed).toEqual([])
  })
})
