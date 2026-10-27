// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// Terminal escape sequences and control strings (review round 3 of PR 494).
//
// Three layers read CLI text that may carry ECMA-48 sequences: the sentence
// class (`sanitizeText`), the refusal door (`sanitizeRefusal`) and the recorder
// (`createRedactor().redactText`). They share one linear-time scanner,
// `stripTerminalSequences`, defined once in `src/shared/sanitize.ts`. This file
// holds three properties of it:
//
//   1. **it is linear**: a hostile 64 KiB input costs each layer milliseconds,
//      never seconds (the regex it replaced rescanned an unterminated OSC from
//      every U+009D: about 17 s per call);
//   2. **it leaves no residue**: nF, Fp, Fe and Fs escapes, CSI, OSC, DCS, SOS,
//      PM and APC (ESC or C1 introduced, with zero-width or fullwidth padding)
//      are removed whole, so nothing is glued to a token;
//   3. **an ambiguous sequence is read every way**: `ESC [` followed by a letter
//      is either a parameterless command or an introducer before text, and the
//      credential rules run over every reading of each such sequence, the union
//      redacted (sanitize-ambiguity.test.ts holds the gaming inputs).
//
// Every credential-shaped value is built at run time (test/support/fakeSecrets).

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  MAX_TEXT_INPUT_LENGTH,
  TOKEN_PLACEHOLDER,
  resolveAmbiguousSequences,
  sanitizeRefusal,
  sanitizeText,
  stripTerminalSequences,
} from '../../src/shared/sanitize'
import { createRedactor } from '../../src/source/redact'
import * as fake from '../support/fakeSecrets'

const ESC = String.fromCharCode(0x1b)
const BEL = String.fromCharCode(0x07)
const ST = `${ESC}\\`
const ZWSP = String.fromCharCode(0x200b)
const c1 = (code: number): string => String.fromCharCode(code)

function drawn(result: ReturnType<typeof sanitizeText>): string {
  return result.ok ? result.text : `refused: ${result.reason}`
}

/** fill repeats `unit` up to the 64 KiB bound both layers accept. */
function fill(unit: string, prefix = ''): string {
  const room = MAX_TEXT_INPUT_LENGTH - prefix.length
  return prefix + unit.repeat(Math.floor(room / unit.length))
}

const HOSTILE: readonly (readonly [string, string])[] = [
  ['repeated U+009D', fill(c1(0x9d))],
  ['repeated U+009D + letter', fill(`${c1(0x9d)}a`)],
  ['repeated ESC', fill(ESC)],
  ['repeated ESC ]', fill(`${ESC}]`)],
  ['repeated ESC [', fill(`${ESC}[`)],
  ['repeated ESC P', fill(`${ESC}P`)],
  ['repeated ESC + ZWSP', fill(`${ESC}${ZWSP}`)],
  ['ESC [ + 64K parameter bytes', fill('1;', `${ESC}[`)],
  ['ESC + 64K intermediate bytes', fill(' ', ESC)],
  ['unterminated OSC', fill('a', `${ESC}]0;`)],
  ['unterminated DCS', fill('a', `${ESC}P`)],
  ['unterminated SOS', fill('a', `${ESC}X`)],
  ['unterminated PM', fill('a', `${ESC}^`)],
  ['unterminated APC', fill('a', `${ESC}_`)],
  ['unterminated C1 OSC', fill('a', c1(0x9d))],
  ['unterminated C1 DCS', fill('a', c1(0x90))],
  ['OSC with ESC not followed by backslash', fill(`${ESC}a`, `${ESC}]`)],
  ['alternating ESC openers', fill(`${ESC}[${ESC}]${ESC}P${ESC}X${ESC}^${ESC}_${ESC}(`)],
  ['alternating C1 openers', fill(`${c1(0x9b)}${c1(0x9d)}${c1(0x90)}${c1(0x98)}${c1(0x9e)}${c1(0x9f)}`)],
  ['repeated ESC + fullwidth bracket', fill(`${ESC}［`)],
]

/** BOUND_MS is far below the 17 s the old pattern cost and far above what a
 * linear pass costs, so the test is not flaky on a loaded machine. */
const BOUND_MS = 250

function timed(run: () => unknown): number {
  const start = performance.now()
  try {
    run()
  } catch {
    // A refusal is an answer; only the time it took matters here.
  }
  return performance.now() - start
}

describe('a hostile 64 KiB input costs every layer milliseconds', () => {
  it.each(HOSTILE)('%s', (_name, input) => {
    expect(input.length).toBeLessThanOrEqual(MAX_TEXT_INPUT_LENGTH)
    expect(timed(() => stripTerminalSequences(input))).toBeLessThan(BOUND_MS)
    expect(timed(() => resolveAmbiguousSequences(input))).toBeLessThan(BOUND_MS)
    expect(timed(() => sanitizeText(input, 'sentence'))).toBeLessThan(BOUND_MS)
    expect(timed(() => sanitizeText(input, 'label'))).toBeLessThan(BOUND_MS)
    expect(timed(() => sanitizeRefusal(input))).toBeLessThan(BOUND_MS)
    expect(timed(() => createRedactor().redactText(input))).toBeLessThan(BOUND_MS)
  })
})

/** GARBAGE is every sequence shape a terminal swallows, built at run time. */
const GARBAGE: readonly (readonly [string, string])[] = [
  ['tput sgr0 after bold', `${ESC}[1m${ESC}(B${ESC}[m`],
  ['nF designate', `${ESC}(0`],
  ['nF with two intermediates', `${ESC}$(B`],
  ['Fp keypad', `${ESC}=`],
  ['Fp save cursor', `${ESC}7`],
  ['nF DEC test', `${ESC}#8`],
  ['Fs reset', `${ESC}c`],
  ['Fe index', `${ESC}D`],
  ['DCS', `${ESC}P1$r0m${ST}`],
  ['SOS', `${ESC}Xstart of string${ST}`],
  ['PM', `${ESC}^privacy${ST}`],
  ['APC', `${ESC}_app${BEL}`],
  ['OSC hyperlink', `${ESC}]8;;https://example.invalid/x${ST}`],
  ['ESC ZWSP CSI', `${ESC}${ZWSP}[31m`],
  ['ESC fullwidth CSI', `${ESC}［31m`],
  ['ESC ZWSP inside parameters', `${ESC}[3${ZWSP}1m`],
  ['C1 CSI', `${c1(0x9b)}1;31m`],
  ['C1 OSC', `${c1(0x9d)}0;title${BEL}`],
  ['C1 DCS to C1 ST', `${c1(0x90)}q${c1(0x9c)}`],
  ['C1 APC', `${c1(0x9f)}x${c1(0x9c)}`],
  ['lone C1 ST', c1(0x9c)],
  ['lone ESC backslash', ST],
]

function credentials(): readonly (readonly [string, string])[] {
  return [
    ['github token', fake.githubToken()],
    ['github pat', fake.githubPat()],
    ['gitlab token', fake.gitlabToken()],
    ['npm token', fake.npmToken()],
    ['provider key', fake.providerApiKey()],
    ['slack token', fake.slackToken()],
    ['aws key id', fake.awsKeyId()],
    ['google api key', fake.googleApiKey()],
    ['google access token', fake.googleAccessToken()],
    ['jwt', fake.jwt()],
    ['high-entropy run', fake.highEntropyRun()],
  ]
}

const EXPECTED = `denied for ${TOKEN_PLACEHOLDER} while reading`

function allThree(sentence: string): readonly [string, string, string] {
  return [
    drawn(sanitizeText(sentence, 'sentence')),
    drawn(sanitizeRefusal(sentence)),
    createRedactor().redactText(sentence),
  ]
}

describe('a sequence glued to a token leaves no residue in any layer', () => {
  it('redacts every credential behind every garbage prefix, in all three layers alike', () => {
    for (const [garbageName, garbage] of GARBAGE) {
      for (const [credentialName, token] of credentials()) {
        const label = `${garbageName} + ${credentialName}`
        const sentence = `denied for ${garbage}${token} while reading`
        expect(allThree(sentence), label).toEqual([EXPECTED, EXPECTED, EXPECTED])
      }
    }
  })

  it('redacts every credential behind every prefix that swallows its first bytes (review round 4)', () => {
    // Each of these is incomplete on its own: a terminal completes it with the
    // token's first letter, so only a reading that keeps that letter finds it.
    const swallowing = [
      `${ESC}[`,
      `${ESC}[1`,
      `${ESC}[;`,
      `${ESC}[?`,
      `${ESC}[ `,
      c1(0x9b),
      `${c1(0x9b)}1`,
      `${ESC}(`,
      `${ESC}#`,
      `${ESC} `,
      ESC,
      `${ESC}[K${ESC}[`,
      `${ESC}c${ESC}[`,
      `${ESC}[1m${ESC}(B${ESC}[m${ESC}[1`,
    ]
    for (const prefix of [...GARBAGE.map(([, garbage]) => garbage), ...swallowing]) {
      for (const [credentialName, token] of credentials()) {
        const sentence = `denied for ${prefix}${token} while reading`
        expect(allThree(sentence), `${JSON.stringify(prefix)} + ${credentialName}`).toEqual([EXPECTED, EXPECTED, EXPECTED])
      }
    }
  })

  it('redacts every credential followed by every garbage suffix', () => {
    for (const [garbageName, garbage] of GARBAGE) {
      for (const [credentialName, token] of credentials()) {
        const sentence = `denied for ${token}${garbage} while reading`
        expect(allThree(sentence), `${credentialName} + ${garbageName}`).toEqual([EXPECTED, EXPECTED, EXPECTED])
      }
    }
  })

  it('draws a token-free sentence with no fragment of any sequence', () => {
    for (const [garbageName, garbage] of GARBAGE) {
      const sentence = `permission ${garbage}denied`
      expect(drawn(sanitizeText(sentence, 'sentence')), garbageName).toBe('permission denied')
      expect(drawn(sanitizeRefusal(sentence)), garbageName).toBe('permission denied')
      expect(drawn(sanitizeText(sentence, 'label')), garbageName).toBe('permission denied')
    }
  })

  it('drops an unterminated control string to the end of the input', () => {
    for (const opener of [`${ESC}]`, `${ESC}P`, `${ESC}X`, `${ESC}^`, `${ESC}_`, c1(0x9d), c1(0x90)]) {
      const sentence = `permission denied ${opener}${fake.githubToken()} and more`
      expect(drawn(sanitizeText(sentence, 'sentence')), JSON.stringify(opener)).toBe('permission denied')
      expect(drawn(sanitizeRefusal(sentence)), JSON.stringify(opener)).toBe('permission denied')
      expect(createRedactor().redactText(sentence)).not.toContain(fake.githubToken())
    }
  })
})

describe('a sequence before a letter is read every way', () => {
  it('redacts a token whose first letter a terminal would take as the final byte', () => {
    for (const [name, token] of credentials()) {
      if (!/^[A-Za-z]/.test(token)) continue
      for (const introducer of [`${ESC}[`, c1(0x9b), `${ESC}${ZWSP}[`, `${ESC}［`, ESC]) {
        const sentence = `denied for ${introducer}${token} while reading`
        expect(allThree(sentence), `${JSON.stringify(introducer)} + ${name}`).toEqual([EXPECTED, EXPECTED, EXPECTED])
      }
    }
  })

  it('still strips the letter when it really is the command and the text after it is a token', () => {
    // ESC [ K is "erase to end of line": the token starts after it.
    for (const [name, token] of credentials()) {
      const sentence = `denied for ${ESC}[K${token} while reading`
      expect(allThree(sentence), name).toEqual([EXPECTED, EXPECTED, EXPECTED])
    }
  })

  it('reads the letter as the final byte when no rule finds anything either way', () => {
    expect(drawn(sanitizeText(`${ESC}[Kpermission denied`, 'sentence'))).toBe('permission denied')
    expect(drawn(sanitizeRefusal(`${ESC}[Kpermission denied`))).toBe('permission denied')
  })

  it('strips as a terminal does, and masks only what a reading found', () => {
    expect(stripTerminalSequences(`x${ESC}[Ay`)).toBe('xy')
    expect(stripTerminalSequences(`x${ESC}[1Ay`)).toBe('xy')
    expect(resolveAmbiguousSequences(`x${ESC}[Ay`)).toEqual({ text: 'xy', masked: false, sequences: true, credential: false })
    expect(resolveAmbiguousSequences(`x${ESC}[1Ay`)).toEqual({ text: 'xy', masked: false, sequences: true, credential: false })
  })
})

describe('one scanner', () => {
  it('is defined once, in the display layer, and imported by the recorder', () => {
    const root = join(__dirname, '..', '..', 'src')
    const recorder = readFileSync(join(root, 'source', 'redact.ts'), 'utf8')
    const display = readFileSync(join(root, 'shared', 'sanitize.ts'), 'utf8')
    expect(recorder).toMatch(/import \{[^}]*\bstripTerminalSequences\b[^}]*\} from '\.\.\/shared\/sanitize'/)
    for (const source of [recorder, display]) {
      expect(source).not.toMatch(/ANSI_RE/)
      expect(source).not.toMatch(/\/[^/\n]*\\u001b\[[^/\n]*\/g/)
    }
    expect(display).toMatch(/export function stripTerminalSequences\(/)
  })
})

describe('the recorder and a control string', () => {
  it('never writes a sequence into a fixture, and the audit refuses one', () => {
    const token = fake.githubToken()
    const redactor = createRedactor()
    const document = { message: `denied ${ESC}P${token}${ST} here`, title: `${ESC}[31mred${ESC}[0m` }
    const recorded = JSON.stringify(redactor.redactDocument(document))
    expect(recorded).not.toContain(token)
    expect(recorded).not.toContain('\\u001b')
    expect(redactor.problems(redactor.redactDocument(document))).toEqual([])
    expect(redactor.problems(document)).not.toEqual([])
    expect(redactor.problems({ title: `${ESC}[31mred` })).not.toEqual([])
  })
})
