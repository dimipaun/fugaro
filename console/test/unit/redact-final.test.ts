// The redactor against the third independent review (desktop-console plan Task
// 1.4, SECURITY-SENSITIVE). Every input below was, at 96b81997, either written
// into a fixture with identity still in it or took time quadratic in its
// length. Each block is named after the review item it pins.

import { describe, expect, it } from 'vitest'

import { FixtureRefusal, loadFixture, writeFixture } from '../../src/source/fixture'
import {
  ADDRESS_MARKER,
  BRANCH_MARKER,
  LITERAL_MARKER,
  MAX_REDACTABLE_LINE_LENGTH,
  MAX_REDACTABLE_STRING_LENGTH,
  PERSON_PSEUDONYM_RE,
  PROJECT_MARKER,
  REPO_PSEUDONYM_RE,
  RedactionRefusal,
  TOKEN_MARKER,
  assertRedacted,
  createRedactor,
  foldConfusables,
} from '../../src/source/redact'
import { SYNTHETIC_META } from './synthetic'
import { inert } from '../support/fakeSecrets'

const KEY = 'redact-final-test-only-key'

function red(value: unknown, literals: readonly string[] = []): string {
  return JSON.stringify(createRedactor(literals, { pseudonymKey: KEY }).redactDocument(value))
}

function message(run: () => unknown): string {
  try {
    run()
  } catch (error) {
    return (error as Error).message
  }
  return ''
}

describe('a person-class value is pseudonymised wherever else it appears (review item 1)', () => {
  it("takes the requester out of a queued run's title, which the CLI fills with it", () => {
    // internal/watch/queued.go: a queued run with no workflow is titled by its
    // requester, and `requested_by` is the config's free-form `user:`.
    const redactor = createRedactor([], { pseudonymKey: KEY })
    for (const name of ['Ana Smith', 'anasmith']) {
      const out = redactor.redactDocument({ runs: [{ title: name, requested_by: name, queued: true }] }) as {
        runs: { title: string; requested_by: string }[]
      }
      const run = out.runs[0]
      expect(run?.requested_by).toMatch(PERSON_PSEUDONYM_RE)
      expect(run?.title).toBe(run?.requested_by)
    }
  })

  it('finds the value inside other strings, in any case, in NFKC, and across documents of runs', () => {
    const out = red({
      runs: [
        { requested_by: 'anasmith', title: 'queued by ANASMITH for review' },
        { title: 'ａｎａｓｍｉｔｈ asked again', note: 'ana​smith' },
      ],
    })
    expect(out.toLowerCase()).not.toContain('anasmith')
    expect(out).not.toContain('ａｎａｓｍｉｔｈ')
    expect(out).toContain('queued by person-')
  })

  it('uses the same pseudonym as the field it came from', () => {
    const out = createRedactor([], { pseudonymKey: KEY }).redactDocument({
      kill: { by: 'Ana Smith' },
      reason: 'switched off by Ana Smith',
    }) as { kill: { by: string }; reason: string }
    expect(out.reason).toBe(`switched off by ${out.kill.by}`)
  })

  it('refuses a document that keeps a person verbatim where no rule rewrites it', () => {
    // A report's dimension is kept as written; a requester with the same
    // spelling is a person the audit will not let through.
    expect(message(() => createRedactor().redactDocument({ requested_by: 'week', report: { by: 'week' } }))).toMatch(
      /a person the document names in a person field/,
    )
  })

  it('does not spread a placeholder requester into the words around it', () => {
    const out = JSON.parse(red({ runs: [{ requested_by: '-', title: '- a quiet run -' }] })) as {
      runs: { title: string }[]
    }
    expect(out.runs[0]?.title).toBe('- a quiet run -')
  })
})

describe('every rule runs in linear time, and a string has a hard cap (review item 2)', () => {
  const redactor = createRedactor(['proj-x', 'edge-devel-ana', 'ana', '/Users/ana', 'anas-laptop.local'])
  const shapes: Record<string, (n: number) => string> = {
    letters: (n) => 'a'.repeat(n),
    dashA: (n) => '-a'.repeat(n / 2),
    dots: (n) => 'a.'.repeat(n / 2),
    underscores: (n) => '_'.repeat(n),
    percent: (n) => 'a%'.repeat(n / 2),
    percentHex: (n) => '%41'.repeat(n / 3),
    slashSpace: (n) => `/${'a '.repeat(n / 2)}`,
    slashes: (n) => '/a'.repeat(n / 2),
    at: (n) => `${'a'.repeat(n)}@`,
    atMany: (n) => 'a@'.repeat(n / 2),
    digitsDots: (n) => '1.'.repeat(n / 2),
    colons: (n) => '1:'.repeat(n / 2),
    project: (n) => 'project '.repeat(n / 8),
    tokenEq: (n) => 'token='.repeat(n / 6),
    mixed: (n) => 'aB3'.repeat(n / 3),
    base64: (n) => 'QUFB'.repeat(n / 4),
    jwt: (n) => 'eyJ-'.repeat(n / 4),
    sk: (n) => 'sk-'.repeat(n / 3),
    pem: (n) => inert('-----BEGIN ', 'PRIVATE KEY-----').repeat(n / 27),
    labels: (n) => `${'a'.repeat(60)}.`.repeat(n / 61),
    accented: (n) => 'é'.repeat(n),
    ipish: (n) => '1.1.1.'.repeat(n / 6),
    scheme: (n) => 'a.https'.repeat(n / 7),
    spaces: (n) => `project${' '.repeat(n)}`,
  }

  function time(text: string): number {
    const start = performance.now()
    redactor.redactText(text)
    return performance.now() - start
  }

  it.each(Object.keys(shapes))('redacts 1 MB of %s in well under the time a quadratic rule took', (name) => {
    const shape = shapes[name] ?? ((): string => '')
    // At 96b81997 50 KB of `a` took 6.8 s; a megabyte would have taken hours.
    expect(time(shape(1_000_000))).toBeLessThan(5_000)
  })

  it.each(Object.keys(shapes))('scales %s linearly: 16x the input is nowhere near 256x the time', (name) => {
    // This was 4x the input under a 10x bound, one timing each, and it failed
    // when the whole unit suite ran in parallel (a measured 12.7 for `sk`):
    // one descheduled slice on the small run moved the ratio past the bound.
    // Two changes keep the property and drop the load sensitivity: the inputs
    // are 16x apart, so linear is 16 and quadratic 256 and the bound (64) sits
    // a factor of 4 from each; and each size is timed three times, interleaved,
    // keeping the fastest -- load only ever adds time, so the minimum is the
    // run closest to the work itself.
    const shape = shapes[name] ?? ((): string => '')
    const smallText = shape(25_000)
    const largeText = shape(400_000)
    time(smallText) // warm up
    let small = Number.POSITIVE_INFINITY
    let large = Number.POSITIVE_INFINITY
    for (let round = 0; round < 3; round += 1) {
      small = Math.min(small, time(smallText))
      large = Math.min(large, time(largeText))
    }
    expect(large / Math.max(small, 2)).toBeLessThan(64)
  })

  it('refuses a string over the cap before any rule runs, with a clear message', () => {
    const start = performance.now()
    const said = message(() => redactor.redactDocument({ t: 'a'.repeat(MAX_REDACTABLE_STRING_LENGTH + 1) }))
    expect(performance.now() - start).toBeLessThan(1_000)
    // Refused by the bound itself, not by the audit after a full redaction.
    expect(said).toMatch(/^a string is longer than the redactor accepts/)
    expect(() => redactor.redactDocument({ ['k'.repeat(MAX_REDACTABLE_STRING_LENGTH + 1)]: 1 })).toThrow(RedactionRefusal)
    expect(() => assertRedacted({ t: 'a'.repeat(MAX_REDACTABLE_STRING_LENGTH + 1) })).toThrow(RedactionRefusal)
    // A string at the cap is fine.
    expect(() => redactor.redactDocument({ t: 'a'.repeat(MAX_REDACTABLE_STRING_LENGTH) })).not.toThrow()
  })

  it('refuses a feed line over the cap, and the loader holds the same cap', () => {
    // Every string in it is short: only the line's own bound refuses it.
    const line = JSON.stringify({ t: new Array<string>(Math.ceil(MAX_REDACTABLE_LINE_LENGTH / 6) + 16).fill('a b') })
    expect(line.length).toBeGreaterThan(MAX_REDACTABLE_LINE_LENGTH)
    expect(message(() => redactor.redactFeedLine(line))).toMatch(/^a feed line is longer than the redactor accepts/)
    const records = [{ at: '2026-10-15T12:00:00Z', document: { t: 'a '.repeat(MAX_REDACTABLE_STRING_LENGTH) } }]
    expect(() => loadFixture(writeFixture(records, SYNTHETIC_META), 'big.ndjson')).toThrow(FixtureRefusal)
  })
})

describe('compatibility characters are folded before any rule runs (review item 3)', () => {
  it('reads a fullwidth @ as an @, so the local part goes too', () => {
    expect(red({ t: 'ana.smith＠acme.com' })).toBe(JSON.stringify({ t: ADDRESS_MARKER }))
    expect(red({ t: 'mail ana.smith＠acme.com today' })).toBe(JSON.stringify({ t: `mail ${ADDRESS_MARKER} today` }))
  })
})

describe('a percent-encoded address is decoded first (review item 4)', () => {
  it.each([['u=ana.smith%40acme.com'], ['u=ana.smith%2540acme.com'], ['to%3Dana.smith%40acme.com']])('redacts %s', (text) => {
    const out = red({ t: text })
    expect(out).not.toContain('ana.smith')
    expect(out).toContain(ADDRESS_MARKER)
    expect(() => {
      assertRedacted({ t: text })
    }).toThrow(RedactionRefusal)
  })
})

describe('invisible characters and look-alikes do not split a value (review item 5)', () => {
  it.each([
    ['a zero-width space', 'ana​@acme.com'],
    ['a soft hyphen', 'an­a@acme.com'],
    ['a bidi override', 'ana‮@acme.com'],
    ['a word joiner', 'ana⁠@acme.com'],
    ['a NUL', 'ana\u0000@acme.com'],
  ])('redacts an address split by %s', (_what, text) => {
    const out = red({ t: `mail ${text} now` })
    expect(out).not.toMatch(/ana/)
    expect(out).toContain(ADDRESS_MARKER)
  })

  it('redacts a literal split by an invisible character or spelled with Cyrillic look-alikes', () => {
    const literals = ['edge-devel-ana', 'anas']
    for (const text of ['edge-devel‍-ana', 'edge-dеvel-ana', 'EDGE-DEVЕL-ANA', 'hi аnаs']) {
      const out = red({ t: text }, literals)
      expect(out, JSON.stringify(text)).toContain(LITERAL_MARKER)
      expect(out.toLowerCase()).not.toContain('devel')
      expect(out).not.toMatch(/nаs|nas"/)
    }
  })

  it('folds confusables one code unit to one, so positions survive', () => {
    expect(foldConfusables('bеlоng')).toBe('belong')
    expect(foldConfusables('ΑΒΕ')).toBe('ABE')
    expect(foldConfusables('plain')).toBe('plain')
  })
})

describe('hostile shapes (review item 6)', () => {
  it("treats a reserved key's whole subtree as its class", () => {
    expect(red({ branch: { name: 'acme-merger-codename' } })).toBe(JSON.stringify({ branch: { name: BRANCH_MARKER } }))
    const person = JSON.parse(red({ requested_by: { name: 'Ana Smith', login: 'anasmith', ids: ['x1y2'] } })) as {
      requested_by: { name: string; login: string; ids: string[] }
    }
    expect(person.requested_by.name).toMatch(PERSON_PSEUDONYM_RE)
    expect(person.requested_by.login).toMatch(PERSON_PSEUDONYM_RE)
    expect(person.requested_by.ids[0]).toMatch(PERSON_PSEUDONYM_RE)
    expect(() => {
      assertRedacted({ branch: { name: 'acme-merger-codename' } })
    }).toThrow(/not this class's marker/)
  })

  it('reads camelCase and hyphenated credential keys', () => {
    const out = JSON.parse(
      red({ githubToken: 'abc', accessToken: 'abc', 'X-Api-Key': 'k', apiKey: 'k2', sessionId: 's3cr3tSess', clientSecret: 'x' }),
    ) as Record<string, string>
    for (const value of Object.values(out)) expect(value).toBe(TOKEN_MARKER)
    // Counts that merely end in "tokens" are not credentials.
    const counts = { input_tokens: 12, max_tokens: 4096, outputTokens: 7 }
    expect(red(counts)).toBe(JSON.stringify(counts))
  })

  it('redacts a number under a reserved key, and a number that is a literal', () => {
    expect(red({ project_number: 123456789012 })).toBe(JSON.stringify({ project_number: PROJECT_MARKER }))
    expect(red({ token: 123456789012345 })).toBe(JSON.stringify({ token: TOKEN_MARKER }))
    expect(red({ gcp_project_number: 987654321098 })).toBe(JSON.stringify({ gcp_project_number: PROJECT_MARKER }))
    expect(red({ n: 123456789012 }, ['123456789012'])).toBe(JSON.stringify({ n: LITERAL_MARKER }))
    expect(() => {
      assertRedacted({ project_number: 123456789012 })
    }).toThrow(RedactionRefusal)
    const plain = { round: 3, n: 1234, on: true, kill: { on: false } }
    expect(red(plain, ['123456789012'])).toBe(JSON.stringify(plain))
  })

  it("reads a key named after an Object method as the plain key it is", () => {
    const value = JSON.parse('{"constructor":{"repo":"acme/secret"},"toString":"x"}') as unknown
    const out = JSON.parse(red(value)) as { constructor: { repo: string }; toString: string }
    expect(out.constructor.repo).toMatch(REPO_PSEUDONYM_RE)
    expect(out.toString).toBe('x')
  })

  it('refuses a __proto__ key instead of silently dropping it', () => {
    const hostile = JSON.parse('{"__proto__":{"repo":"acme/secret"},"x":1}') as unknown
    expect(message(() => createRedactor().redactDocument(hostile))).toMatch(/__proto__/)
    expect(() => {
      assertRedacted(hostile)
    }).toThrow(/__proto__/)
  })

  it('redacts a token split across adjacent array elements', () => {
    const out = red({ parts: [inert('gh', 'p_'), 'abcdEFGH', '1234abcdEFGH1234'] })
    expect(out).not.toContain('abcdEFGH')
    expect(out).toContain(TOKEN_MARKER)
    expect(red({ parts: ['access_token=', 'hunter2'] })).not.toContain('hunter2')
    expect(() => {
      assertRedacted({ parts: [inert('gh', 'p_'), 'abcdEFGH', '1234abcdEFGH1234'] })
    }).toThrow(RedactionRefusal)
    const words = { labels: ['bug', 'console', 'p1'] }
    expect(red(words)).toBe(JSON.stringify(words))
  })
})

describe('inputs only one step catches (review item 8)', () => {
  it('decodes a percent-wrapped assignment the entropy rule cannot see', () => {
    const out = red({ t: 'q=access%5Ftoken%3Dhunter2' })
    expect(out).not.toContain('hunter2')
  })

  it('decodes a base64 run too short for the entropy rule', () => {
    const wrapped = Buffer.from(inert('gh', 'p_', 'abcdEFGH')).toString('base64')
    expect(wrapped.length).toBeLessThan(20)
    expect(red({ t: `see ${wrapped} now` })).toBe(JSON.stringify({ t: `see ${TOKEN_MARKER} now` }))
  })

  it('reads kill.by as a person even when it spells a report dimension', () => {
    const out = JSON.parse(red({ kill: { on: true, by: 'week' } })) as { kill: { by: string } }
    expect(out.kill.by).toMatch(PERSON_PSEUDONYM_RE)
  })
})

describe('a refusal names no identity (review item 10)', () => {
  it('keeps keys and values out of the message, and still names the class', () => {
    const said = message(() => {
      assertRedacted({ 'ana@example.com': { title: 'mail ana@example.com' }, project: 'secretproject' })
    })
    expect(said).not.toContain('ana@example.com')
    expect(said).not.toContain('secretproject')
    expect(said).toMatch(/still holds an address/)
    expect(said).toMatch(/not this class's marker/)
  })

  it('does not echo a refused feed line', () => {
    const said = message(() => createRedactor().redactFeedLine('not json ana@example.com'))
    expect(said).not.toContain('ana@example.com')
    expect(said).toMatch(/not a JSON object/)
  })
})
