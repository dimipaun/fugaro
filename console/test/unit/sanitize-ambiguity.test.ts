// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Ambiguous escape sequences are read every way, and the union is redacted
// (review round 4 of PR 494).
//
// A sequence a terminal completes with a printable byte -- `ESC [ K`, `ESC [ 1 A`,
// `ESC ( B`, `ESC c`, C1 CSI + `1A` -- swallows that byte, and in text never
// meant for a terminal the byte may be the first letter of a token. Round 3
// read the whole string two ways and drew whichever reading redacted more
// tokens; the gate review gamed that three ways:
//
//   1. two keys, one found by each reading, the same count each: the pick drew
//      the other key (`a ESC[K<key> b ESC[<key> c` gave `a KAKIA<key> ...`);
//   2. the conservative reading applied to the whole string glued a real
//      one-letter command onto the token after it (`ESC[K ESC[<npm token>` is
//      `Knpm_...` there and `pm_...` in the terminal reading: no reading found
//      it);
//   3. only a *bare* `ESC [` + letter was ambiguous, so `ESC [ 1`, `ESC [ ;`,
//      `ESC [ ?`, `ESC [ ` (space), C1 CSI + `1`, `ESC (`, `ESC #` and `ESC ` (space)
//      still ate a token's first letter.
//
// The rule now (`resolveAmbiguousSequences`) is the union, per sequence: every
// combination of readings of the sequences in one run of text is scanned, and a
// character a terminal draws is redacted if any reading's credential rules
// matched it. Every credential-shaped value is built at run time
// (test/support/fakeSecrets); none is written down.

import { describe, expect, it } from 'vitest'

import {
  MAX_AMBIGUOUS_SCAN,
  MAX_SPLIT_SCAN,
  MAX_TEXT_INPUT_LENGTH,
  TOKEN_PLACEHOLDER,
  resetScanWork,
  resolveAmbiguousSequences,
  sanitizeRefusal,
  sanitizeText,
  scanWorkDone,
  stripTerminalSequences,
  tracedRefusalRules,
} from '../../src/shared/sanitize'
import { createRedactor } from '../../src/source/redact'
import * as fake from '../support/fakeSecrets'

const ESC = String.fromCharCode(0x1b)
const CSI1 = String.fromCharCode(0x9b)

function drawn(result: ReturnType<typeof sanitizeText>): string {
  return result.ok ? result.text : `refused: ${result.reason}`
}

function allThree(sentence: string): readonly [string, string, string] {
  return [drawn(sanitizeText(sentence, 'sentence')), drawn(sanitizeRefusal(sentence)), createRedactor().redactText(sentence)]
}

/** leaks answers whether `out` holds any piece of `token` four characters or
 * longer that is not also a piece of the marker -- "no part of any token". */
function leaks(out: string, token: string): string | null {
  for (let start = 0; start + 4 <= token.length; start += 1) {
    const piece = token.slice(start, start + 4)
    if (TOKEN_PLACEHOLDER.includes(piece)) continue
    if (out.includes(piece)) return piece
  }
  return null
}

function expectNoLeak(sentence: string, tokens: readonly string[], label: string): void {
  for (const [layer, out] of ['sentence', 'refusal', 'recorder'].map((name, index) => [name, allThree(sentence)[index] ?? ''] as const)) {
    for (const token of tokens) {
      expect(leaks(out, token), `${label} / ${layer}: ${JSON.stringify(out)}`).toBeNull()
    }
  }
}

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

describe('round 4, finding 1: two readings that each find one key', () => {
  it('redacts both keys, in all three layers alike', () => {
    const key = fake.awsKeyId()
    const sentence = `a ${ESC}[K${key} b ${ESC}[${key} c`
    const expected = `a ${TOKEN_PLACEHOLDER} b ${TOKEN_PLACEHOLDER} c`
    expect(allThree(sentence)).toEqual([expected, expected, expected])
  })

  it('redacts both, whatever the two credentials are', () => {
    for (const [firstName, first] of credentials()) {
      for (const [secondName, second] of credentials()) {
        const sentence = `a ${ESC}[K${first} b ${ESC}[${second} c`
        expectNoLeak(sentence, [first, second], `${firstName} then ${secondName}`)
      }
    }
  })
})

describe('round 4, finding 2: a real one-letter command before an ambiguous one', () => {
  it('does not glue the command onto the token', () => {
    const token = fake.npmToken()
    const sentence = `w ${ESC}[K${ESC}[${token} v`
    const expected = `w ${TOKEN_PLACEHOLDER} v`
    expect(allThree(sentence)).toEqual([expected, expected, expected])
  })

  it('does the same after a reset (ESC c) and for every credential', () => {
    for (const [name, token] of credentials()) {
      for (const command of [`${ESC}[K`, `${ESC}c`, `${ESC}[2J`, `${ESC}[0m`]) {
        const sentence = `w ${command}${ESC}[${token} v`
        expectNoLeak(sentence, [token], `${JSON.stringify(command)} + ${name}`)
      }
    }
  })
})

/** INTRODUCERS are the sequences that swallow a token's first letter, or its
 * first letter and the bytes before it (finding 3). */
const INTRODUCERS: readonly string[] = [
  `${ESC}[`,
  `${ESC}[1`,
  `${ESC}[;`,
  `${ESC}[?`,
  `${ESC}[ `,
  `${ESC}[1;31`,
  `${CSI1}`,
  `${CSI1}1`,
  `${ESC}(`,
  `${ESC}#`,
  `${ESC} `,
  `${ESC}$(`,
  ESC,
]

describe('round 4, finding 3: every sequence whose final byte is the token', () => {
  it('redacts the token behind every introducer, in all three layers alike', () => {
    for (const introducer of INTRODUCERS) {
      for (const [name, token] of credentials()) {
        const sentence = `w ${introducer}${token} v`
        const label = `${JSON.stringify(introducer)} + ${name}`
        expectNoLeak(sentence, [token], label)
        expect(allThree(sentence), label).toEqual([`w ${TOKEN_PLACEHOLDER} v`, `w ${TOKEN_PLACEHOLDER} v`, `w ${TOKEN_PLACEHOLDER} v`])
      }
    }
  })

  it('redacts a token whose leading digits a terminal takes as parameters', () => {
    // `ESC [` + `1234` + the rest: the CSI takes the digits and the next letter.
    const key = fake.awsKeyId()
    const sentence = `w ${key.slice(0, 4)}${ESC}[${key.slice(4)} v`
    expectNoLeak(sentence, [key], 'digits as parameters')
  })

  it('keeps a token-free sentence as a terminal draws it', () => {
    for (const introducer of INTRODUCERS) {
      expect(drawn(sanitizeText(`permission ${introducer}Kdenied`, 'sentence')), JSON.stringify(introducer)).toBe('permission denied')
    }
  })
})

describe('the union is mutation-proof against picking one reading', () => {
  it('needs both readings of one string at once', () => {
    // Only the union of the two readings redacts both: a rule that drew either
    // reading's own result would draw the other key's remainder.
    const key = fake.awsKeyId()
    const out = createRedactor().redactText(`a ${ESC}[K${key} b ${ESC}[${key} c`)
    expect(out).not.toContain(key.slice(1))
    expect(out).not.toContain(`K${key}`)
  })
})

describe('the resolver', () => {
  it('returns nothing to do for text whose sequences swallowed nothing', () => {
    expect(resolveAmbiguousSequences('plain text')).toBeUndefined()
    expect(resolveAmbiguousSequences(`${ESC}]0;title\u0007after`)).toBeUndefined()
  })

  it('maps a find through invisible characters', () => {
    const key = fake.awsKeyId()
    const result = resolveAmbiguousSequences(`x ​${ESC}[​${key} y`)
    expect(result?.text).toBe(`x ${TOKEN_PLACEHOLDER} y`)
    expect(result?.masked).toBe(true)
  })

  it('refuses a run of text with more combinations than it reads (fail closed)', () => {
    const glued = `${ESC}[1A`.repeat(10)
    expect(resolveAmbiguousSequences(`x${glued}y`)).toBeNull()
    expect(drawn(sanitizeText(`x${glued}y`, 'sentence'))).toMatch(/^refused: /)
    expect(drawn(sanitizeRefusal(`x${glued}y`))).toMatch(/^refused: /)
    expect(createRedactor().redactText(`x${glued}y`)).toBe(TOKEN_PLACEHOLDER)
  })

  it('refuses an ambiguous credential in a validating class and in a url', () => {
    const token = fake.githubToken()
    expect(sanitizeText(`${ESC}[${token}`, 'branch').ok).toBe(false)
    expect(sanitizeText(`${ESC}[1${fake.awsKeyId()}`, 'person').ok).toBe(false)
    expect(sanitizeText(`https://example.invalid/${ESC}[K${token}`, 'url').ok).toBe(false)
  })

  it('traces the same text the refusal rules write', () => {
    const corpus = [
      ...credentials().map(([, token]) => `denied for ${token} while reading`),
      `see https://user:pw@example.invalid/x and ${fake.githubToken()}`,
      `${fake.bearerHeader()} then`,
      `api_key=hunter2 and password: ${fake.highEntropyRun()}`,
      `u=${encodeURIComponent(fake.githubToken())}`,
      `blob ${Buffer.from(fake.googleApiKey()).toString('base64')} end`,
      fake.pemBlock(),
      'nothing at all here',
    ]
    for (const text of corpus) {
      const viaRules = drawn(sanitizeRefusal(text))
      expect(tracedRefusalRules(text).replace(/\s+/g, ' ').trim(), text).toBe(viaRules)
    }
  })
})

describe('a line break inside a sequence (finding 4a)', () => {
  it('ends the sequence and is kept', () => {
    expect(stripTerminalSequences(`a${ESC}[1\nx`)).toBe('a\nx')
    expect(stripTerminalSequences(`a${ESC}[1\tx`)).toBe('a\tx')
    expect(stripTerminalSequences(`a${ESC}[1\rx`)).toBe('a\rx')
    expect(stripTerminalSequences(`a${ESC}\nx`)).toBe('a\nx')
    expect(stripTerminalSequences(`a${ESC}(\nx`)).toBe('a\nx')
    expect(createRedactor().redactText(`line one${ESC}[1\nline two`)).toBe('line one\nline two')
  })
})

describe('the recorder never mangles its own marker (finding 4b)', () => {
  it('writes only whole markers, whatever sequence sits before a token', () => {
    const circled = String.fromCharCode(0x2460)
    const prefixes = [ESC, `${ESC}[`, `${ESC}[1`, CSI1, `${ESC}(`, `${ESC}\n`, `${ESC}​`]
    for (const prefix of prefixes) {
      for (const [name, token] of [...credentials(), ['aws key + circled digit', `${fake.awsKeyId()}${circled}`] as const]) {
        const out = createRedactor().redactText(`x ${prefix}${token} y`)
        const label = `${JSON.stringify(prefix)} + ${name}: ${JSON.stringify(out)}`
        expect(out.replaceAll(TOKEN_PLACEHOLDER, ''), label).not.toMatch(/edacted|redacted-token\]/)
        expect(out, label).not.toContain(ESC)
      }
    }
  })
})

/** SCALING_RATIO is how much more work eight times the text may cost. The
 * property is the design's own: the sanitiser is linear in the text and not
 * quadratic in the number of ambiguous sites, so 10,000 sites in separate words
 * cost a pass over the text rather than a pass per site. Eight times the text is
 * eight times the work; a resolver that read once per site would cost
 * sixty-four. */
const SCALING_RATIO = 16

/** CEILING_MS is the sanity net this test's absolute bound became: ten times
 * the 250 ms a healthy machine reads for a 64 KiB input. It is not the property
 * -- a wall-clock bound tight enough to catch a quadratic pass is a bound a busy
 * runner trips on, which is what happened (`expected 263.27 to be less than
 * 250` on a loaded ubuntu runner, passing on rerun). The counters are the
 * property: the same number in every run on every machine (`scanWork` in
 * sanitize.ts, and `split-secrets.test.ts`, deflaked the same way after a
 * loaded runner turned its 45 ms pass into 435 ms). */
const CEILING_MS = 2_500

/** workCap is the ceiling the 250 ms bound was really pinning, in the unit the
 * counters count: the characters every reading scans over one value of `length`.
 * It is the resolver's own arithmetic, in the terms `split-secrets.test.ts`
 * states it -- the ambiguous sequence clusters, which `resolve` refuses rather
 * than read past `MAX_AMBIGUOUS_SCAN`; the split readings, at most
 * `2 * (MAX_SPLIT_SCAN + length)` because that budget divides by the prepared
 * form while each reading carries a SPLIT_MARK per character of it; and the one
 * reading of the prepared form every value pays. */
const workCap = (length: number): number => MAX_AMBIGUOUS_SCAN + 2 * (MAX_SPLIT_SCAN + length) + length

/** Answer is what a layer did with the input: read it and drawn something, or
 * refused it. A refusal is an answer too, and it is the one the counters cannot
 * see -- a layer that refuses costs no work at all, so a case that asserted
 * only work would pass a layer that refuses everything. That is exactly the
 * shape of the regression this file exists for: a resolver that takes a
 * cluster's readings as a *sum* rather than their maximum spends
 * `MAX_AMBIGUOUS_SCAN` at its own pre-check and refuses the whole input before a
 * single reading, `scanWork` collapses to 0 and every work assertion passes
 * while the drawn value becomes a refusal. So the expected answer is a column of
 * the table below, the same for the eighth of the input as for the whole of it. */
type Answer = 'read' | 'refused'

/** Layer is one of the four doors a value goes through, and how to read its own
 * answer: the resolver answers with `null` for a refusal, the two sanitisers
 * with a result whose `ok` is false, and the recorder with the marker it writes
 * in place of a value it will not read. */
interface Layer {
  readonly name: string
  readonly run: (input: string) => unknown
  readonly refused: (out: unknown) => boolean
}

const resultRefused = (out: unknown): boolean => !(out as { ok?: boolean }).ok

/** Cost is one call, all three ways: what the layer answered (a refusal costs no
 * work, so a case that did not ask would not know it had been refused), the
 * characters every reading scanned (`scanWork`, the quantity `MAX_AMBIGUOUS_SCAN`
 * bounds and the one that grows with the number of sites once a resolver stops
 * being linear), and the time it took, which is only ever read against the
 * coarse ceiling. */
interface Cost {
  readonly work: number
  readonly ms: number
  readonly answer: Answer
}

function cost(layer: Layer, input: string): Cost {
  resetScanWork()
  const start = performance.now()
  let answer: Answer = 'read'
  try {
    if (layer.refused(layer.run(input))) answer = 'refused'
  } catch {
    // A throw is a refusal by another name, and the work it took is still work.
    answer = 'refused'
  }
  return { work: scanWorkDone(), ms: performance.now() - start, answer }
}

/** fill builds the input a case reads: the unit repeated to the layer's own
 * limit, or to the `divisor`th of it -- the eighth is what the scaling below
 * compares the whole input against. */
function fill(unit: string, divisor = 1): string {
  return unit.repeat(Math.floor(MAX_TEXT_INPUT_LENGTH / divisor / unit.length))
}

describe('10,000 ambiguous sites cost every layer linear work, and milliseconds', () => {
  // Every unit but the glued one is read: its sites sit in separate words, which
  // is the shape the resolver's own doc prices at four readings for 10,000 of
  // them, and the value is drawn rather than refused. The glued unit is one
  // cluster of 10,000 combinations, past `MAX_AMBIGUOUS_COMBINATIONS`, so
  // refusing it is the fail-closed answer the resolver's other cases pin.
  const units: readonly (readonly [string, string, Answer])[] = [
    ['ESC [ K word, apart', `${ESC}[Kab `, 'read'],
    ['ESC [ 1 A word, apart', `${ESC}[1Ab `, 'read'],
    ['ESC c, apart', `${ESC}c `, 'read'],
    ['C1 CSI 1 A, apart', `${CSI1}1Ax `, 'read'],
    ['ESC [ K glued', `${ESC}[Ka`, 'refused'],
    ['colour codes around words', `${ESC}[31mred${ESC}[0m `, 'read'],
    ['ESC [ before a token fragment', `${ESC}[${fake.githubToken().slice(0, 6)} `, 'read'],
    ['ESC [ before a whole token', `${ESC}[${fake.githubToken()} `, 'read'],
  ]
  const layers: readonly Layer[] = [
    {
      name: 'resolveAmbiguousSequences',
      run: (input) => resolveAmbiguousSequences(input),
      refused: (out) => out === null,
    },
    { name: 'sanitizeText', run: (input) => sanitizeText(input, 'sentence'), refused: resultRefused },
    { name: 'sanitizeRefusal', run: (input) => sanitizeRefusal(input), refused: resultRefused },
    {
      name: 'redactText',
      run: (input) => createRedactor().redactText(input),
      refused: (out) => out === TOKEN_PLACEHOLDER,
    },
  ]
  it.each(units)('%s', (_name, unit, expected) => {
    const input = fill(unit)
    const eighth = fill(unit, 8)
    expect(input.length).toBeLessThanOrEqual(MAX_TEXT_INPUT_LENGTH)
    for (const layer of layers) {
      const smaller = cost(layer, eighth)
      const larger = cost(layer, input)
      // The answer first, because it is the assertion the work cannot make: a
      // resolver that stopped being linear refuses this input, and a refusal
      // costs no work, so the two assertions below would hold over a value that
      // is no longer read at all.
      expect(smaller.answer, `${layer.name}: the eighth's answer`).toBe(expected)
      expect(larger.answer, `${layer.name}: the whole input's answer`).toBe(expected)
      // Eight times the text, at most sixteen times the work. The eighth is
      // floored at one character because a value the resolver refuses costs no
      // work at all -- the bound working, not a reading -- and the whole input
      // is refused for the same reason rather than read in more readings.
      expect(larger.work, `${layer.name}: the work eight times the text costs`).toBeLessThanOrEqual(
        Math.max(smaller.work, 1) * SCALING_RATIO,
      )
      expect(larger.work, `${layer.name}: every reading, against the resolver's own cap`).toBeLessThanOrEqual(
        workCap(input.length),
      )
      expect(larger.ms, `${layer.name}: the time`).toBeLessThan(CEILING_MS)
    }
  })
})
