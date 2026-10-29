// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The redactor's person pass against the fourth independent review (desktop-
// console plan Task 1.4, SECURITY-SENSITIVE). At 8cd1950d the person pass ran
// *before* every pattern rule, so a requester named after a piece of a token, a
// host or an address switched that rule off; it ran each person as its own
// regular expression over every string, so the cost was persons x text; and
// several of its branches had no test that would notice them gone. Every name
// below is invented.

import { inspect } from 'node:util'

import { describe, expect, it } from 'vitest'

import { FixtureRefusal, loadFixture, writeFixture } from '../../src/source/fixture'
import {
  BUCKET_MARKER,
  HOST_MARKER,
  MAX_DISTINCT_PERSONS,
  MAX_PERSON_WORDS,
  MAX_REDACTABLE_LINE_LENGTH,
  MAX_REDACTABLE_STRING_LENGTH,
  PERSON_PSEUDONYM_RE,
  RedactionRefusal,
  TOKEN_MARKER,
  assertRedacted,
  createRedactor,
} from '../../src/source/redact'
import { SYNTHETIC_META } from './synthetic'
import { inert, pemBlock } from '../support/fakeSecrets'

const KEY = 'redact-persons-test-only-key'

function redactor(literals: readonly string[] = []): ReturnType<typeof createRedactor> {
  return createRedactor(literals, { pseudonymKey: KEY })
}

function red(value: unknown, literals: readonly string[] = []): string {
  return JSON.stringify(redactor(literals).redactDocument(value))
}

function message(run: () => unknown): string {
  try {
    run()
  } catch (error) {
    return `${(error as Error).name}: ${(error as Error).message}`
  }
  return ''
}

// A fixed "random" body, so a failure prints the same thing twice.
const BODY60 = 'Q7xk2PzR9mWb4LcV8nTy3HdJ6fGs1AeU5oKi0rZqXwYvBpNjMhCtDlEgFuSa'
const HEX12 = 'a1b2c3d4e5f6'
/** SPLIT_BODY is a forge-shaped token, assembled here because three tests below
 * embed it in a sentence: a literal one would be a shape a scanner matches, and
 * the tests are about what the redactor does with the shape, not the value. */
const SPLIT_BODY = inert('gh', 'p_', 'abcdEFGH1234abcdEFGH1234abcdEFGH1234')

describe('people are matched after every pattern rule, so a person cannot switch one off', () => {
  // [the requester, the text, what must not survive, what must be there]
  const cases: readonly (readonly [string, string, string, string])[] = [
    ['ya29', inert('ya', '29.', BODY60), BODY60.slice(0, 20), TOKEN_MARKER],
    ['password', 'login failed: password=hunter2xyz rejected', 'hunter2xyz', TOKEN_MARKER],
    ['bearer', `Authorization: Bearer ${HEX12}`, HEX12, TOKEN_MARKER],
    ['BEGIN', pemBlock('MIIabcDEF'), 'MIIabcDEF', TOKEN_MARKER],
    ['private', pemBlock('MIIabcDEF'), 'MIIabcDEF', TOKEN_MARKER],
    ['local', 'on zoras-laptop.local now', 'zoras', HOST_MARKER],
    ['com', 'on zoras-laptop.corp.acme.com now', 'acme', HOST_MARKER],
    ['internal', 'dial db.acme.internal', 'acme', HOST_MARKER],
    ['fugaro', 'wrote to fugaro-runs-acme-x9', 'acme-x9', BUCKET_MARKER],
    ['runs', 'wrote to fugaro-runs-acme-x9', 'acme-x9', BUCKET_MARKER],
    ['42', 'dial 10.42.7.13:443', '7.13', HOST_MARKER],
  ]

  it.each(cases)('requester %j leaves %j redacted', (person, text, secret, marker) => {
    const out = redactor().redactDocument({ requested_by: person, t: text }) as { t: string }
    expect(out.t).not.toContain(secret)
    expect(out.t).toContain(marker)
  })

  it.each(cases)('requester %j: the output is a fixed point and audits clean', (person, text) => {
    const r = redactor()
    const once = r.redactDocument({ requested_by: person, t: text })
    expect(r.redactDocument(once)).toStrictEqual(once)
    expect(() => {
      assertRedacted(once)
    }).not.toThrow()
  })

  it('still replaces the person wherever the pattern rules left them', () => {
    const out = redactor().redactDocument({
      requested_by: 'Zora Kell',
      t: `Zora Kell pushed ${SPLIT_BODY} from zoras-laptop.local`,
    }) as { requested_by: string; t: string }
    expect(out.t).toBe(`${out.requested_by} pushed ${TOKEN_MARKER} from ${HOST_MARKER}`)
  })

  it('redacts a shape the person pass itself makes, and the result is a fixed point', () => {
    // `Ana/wxyz` is no repository (owner under four characters); with the
    // name replaced it would read as one.
    const r = redactor()
    const once = r.redactDocument({ requested_by: 'Ana', t: 'see Ana/wxyz now' })
    expect(r.redactDocument(once)).toStrictEqual(once)
    expect(() => {
      assertRedacted(once)
    }).not.toThrow()
  })
})

describe('the person pass is linear, and the distinct persons per document are capped', () => {
  function document(persons: number, textBytes: number): string {
    const runs: unknown[] = []
    for (let i = 0; i < persons; i += 1) {
      runs.push({ requested_by: `user${i.toString(36)}x`, title: `by user${i.toString(36)}x` })
    }
    const chunk = 'lorem ipsum dolor sit amet '.repeat(40)
    const notes: string[] = []
    for (let size = 0; size < textBytes; size += chunk.length) notes.push(chunk)
    return JSON.stringify({ runs, notes })
  }

  function time(line: string): number {
    const start = performance.now()
    redactor(['proj-x']).redactFeedLine(line)
    return performance.now() - start
  }

  it('pins the cap', () => {
    expect(MAX_DISTINCT_PERSONS).toBe(4096)
  })

  it('refuses 16k distinct persons in a 1 MiB line promptly, with a clear message', () => {
    const line = document(16_384, 0)
    expect(line.length).toBeLessThan(MAX_REDACTABLE_LINE_LENGTH)
    const start = performance.now()
    const said = message(() => redactor().redactFeedLine(line))
    expect(performance.now() - start).toBeLessThan(2_000)
    expect(said).toMatch(/^RedactionRefusal: a feed document names more than 4096 distinct persons/)
    expect(said).not.toMatch(/user/)
  })

  it('redacts a 1 MiB line at the cap in bounded time, every requester out of every title', () => {
    const line = document(MAX_DISTINCT_PERSONS, 1_048_576 - document(MAX_DISTINCT_PERSONS, 0).length - 3_300)
    expect(line.length).toBeGreaterThan(1_000_000)
    expect(line.length).toBeLessThanOrEqual(MAX_REDACTABLE_LINE_LENGTH)
    const start = performance.now()
    const out = redactor().redactFeedLine(line)
    expect(performance.now() - start).toBeLessThan(5_000)
    expect(out).not.toMatch(/user[0-9a-z]+x/)
  })

  it('scales linearly: 4x the persons and 4x the text is under 10x the time', () => {
    time(document(64, 10_000)) // warm up
    const small = Math.max(time(document(1_000, 200_000)), 5)
    const large = time(document(4_000, 800_000))
    expect(large / small).toBeLessThan(10)
  })
})

describe('which values are persons, and where a person is replaced', () => {
  it('prefers the longest person at a position', () => {
    const out = redactor().redactDocument({
      runs: [{ requested_by: 'Ana' }, { requested_by: 'Ana Smith' }],
      t: 'asked by Ana Smith today',
    }) as { runs: { requested_by: string }[]; t: string }
    expect(out.t).toBe(`asked by ${String(out.runs[1]?.requested_by)} today`)
  })

  it('never takes a marker word for a person, so a marker stays a marker', () => {
    for (const person of ['token', 'redacted', 'person', 'host', 'redacted-token']) {
      const out = redactor().redactDocument({ requested_by: person, t: `key ${SPLIT_BODY} on db.acme.com` }) as {
        t: string
      }
      expect(out.t, person).toBe(`key ${TOKEN_MARKER} on ${HOST_MARKER}`)
    }
  })

  it('collects a by-field that is not a report dimension, and leaves one that is', () => {
    const out = JSON.parse(red({ report: { by: 'Ana Smith' }, title: 'switched by Ana Smith' })) as {
      report: { by: string }
      title: string
    }
    expect(out.report.by).toMatch(PERSON_PSEUDONYM_RE)
    expect(out.title).toBe(`switched by ${out.report.by}`)
    const dim = JSON.parse(red({ report: { by: 'week' }, title: 'by week' })) as { title: string }
    expect(dim.title).toBe('by week')
  })

  it("collects a person from anywhere in a person field's subtree", () => {
    const out = JSON.parse(red({ requested_by: { name: 'Ana Smith' }, title: 'for Ana Smith' })) as {
      requested_by: { name: string }
      title: string
    }
    expect(out.title).toBe(`for ${out.requested_by.name}`)
  })

  it('refuses a person spelled in more words than it looks for, and accepts one at the bound', () => {
    const words = (n: number): string => Array.from({ length: n }, (_, i) => `w${String(i)}x`).join(' ')
    expect(MAX_PERSON_WORDS).toBe(16)
    expect(message(() => redactor().redactDocument({ requested_by: words(17), t: words(17) }))).toMatch(
      /^RedactionRefusal: a document names a person in more than 16 words/,
    )
    const out = redactor().redactDocument({ requested_by: words(16), t: `by ${words(16)}` }) as {
      requested_by: string
      t: string
    }
    expect(out.t).toBe(`by ${out.requested_by}`)
  })

  it('does not take a one-character requester for a name', () => {
    const out = JSON.parse(red({ runs: [{ requested_by: 'a', title: 'a quiet run' }] })) as { runs: { title: string }[] }
    expect(out.runs[0]?.title).toBe('a quiet run')
  })

  it.each([['Ana  Smith'], ['Ana-Smith'], ['Smith, Ana'], ['ana_smith'], ['ANA\tSMITH']])(
    'replaces the variant %j of the requester',
    (variant) => {
      const out = JSON.parse(red({ requested_by: 'Ana Smith', title: `fix by ${variant} today` })) as {
        requested_by: string
        title: string
      }
      expect(out.title).toBe(`fix by ${out.requested_by} today`)
    },
  )

  it('never rewrites a contract field, and never takes a contract keyword for a person', () => {
    const doc = {
      runs: [
        { run: 'run-a-1', stage: 'queued', auth: 'api', health: 'ok', slug: '-', title: 'queued run', requested_by: 'queued' },
        { run: 'run-a-2', stage: 'coding', auth: 'api', title: 'a run on the api', requested_by: 'run' },
        { run: 'run-a-3', stage: 'coding', auth: 'oauth', title: 'x', requested_by: 'api' },
      ],
      finished: [{ run: 'run-b-1', status: 'succeeded', outcome: 'ready', title: 'done' }],
    }
    const out = JSON.parse(red(doc)) as typeof doc
    expect(out.runs.map((run) => [run.run, run.stage, run.auth])).toStrictEqual([
      ['run-a-1', 'queued', 'api'],
      ['run-a-2', 'coding', 'api'],
      ['run-a-3', 'coding', 'oauth'],
    ])
    expect(out.runs[0]?.slug).toBe('-')
    expect(out.runs[0]?.title).toBe('queued run')
    expect(out.runs[1]?.title).toBe('a run on the api')
    expect(out.finished[0]).toStrictEqual(doc.finished[0])
    // A real person in a contract field's place is still a person everywhere else.
    const named = JSON.parse(red({ runs: [{ run: 'run-zora-1', title: 'zora asked', requested_by: 'zora' }] })) as {
      runs: { run: string; title: string; requested_by: string }[]
    }
    expect(named.runs[0]?.title).toBe(`${String(named.runs[0]?.requested_by)} asked`)
    // The run id is the CLI's own identifier, and the person pass leaves it as written.
    expect(named.runs[0]?.run).toBe('run-zora-1')
  })
})

describe('the caps are pinned as numbers, and every audit holds them', () => {
  it('a string is 64 KiB and a line 1 MiB', () => {
    expect(MAX_REDACTABLE_STRING_LENGTH).toBe(65_536)
    expect(MAX_REDACTABLE_LINE_LENGTH).toBe(1_048_576)
  })

  it('refuses a key past the string cap in the audit too', () => {
    expect(message(() => assertRedacted({ ['k'.repeat(65_537)]: 1 }))).toMatch(/a key is longer than the redactor accepts/)
    expect(() => {
      assertRedacted({ ['k'.repeat(65_536)]: 1 })
    }).not.toThrow()
  })

  it('the loader refuses a record line over 2 MiB, even one whose strings are all clean and short', () => {
    const words = new Array<string>(Math.ceil((2 * 1_048_576) / 6) + 64).fill('a b')
    const text = writeFixture([{ at: '2026-10-15T12:00:00Z', document: { t: words } as never }], SYNTHETIC_META)
    expect(message(() => loadFixture(text, 'big.ndjson'))).toMatch(/longer than a feed line can be/)
  })
})

describe('the audit reads the prepared form, and the redactor the form as written', () => {
  it.each([['ana＠acme.com'], ['ana​@acme.com']])('the audit refuses %j, which only its prepared form shows', (text) => {
    expect(() => {
      assertRedacted({ t: text })
    }).toThrow(/still holds an address/)
  })

  it('redacts a token only the form as written shows', () => {
    // The zero-width space is what keeps the run from starting after a dot;
    // prepared, the dot is the run's neighbour and no rule fires.
    const text = 'a.​Q1w2E3r4T5y6U7i8O9p0'
    expect(red({ t: text })).not.toContain('Q1w2')
  })
})

describe('hostile shapes are refusals, not crashes', () => {
  it('refuses a document nested 10000 deep as a RedactionRefusal, and the loader as a FixtureRefusal', () => {
    const depth = 10_000
    const line = `{"a":${'['.repeat(depth)}${']'.repeat(depth)}}`
    expect(message(() => redactor().redactFeedLine(line))).toMatch(/^RedactionRefusal: .*nested deeper than/)
    const deepObject = `{"requested_by":${'{"a":'.repeat(depth)}"x y"${'}'.repeat(depth)}}`
    expect(message(() => redactor().redactFeedLine(deepObject))).toMatch(/^RedactionRefusal: .*nested deeper than/)
    const deep = JSON.parse(line) as never
    expect(message(() => assertRedacted(deep))).toMatch(/^RedactionRefusal: .*nested deeper than/)
    const text = writeFixture([{ at: '2026-10-15T12:00:00Z', document: deep }], SYNTHETIC_META)
    expect(() => loadFixture(text, 'deep.ndjson')).toThrow(FixtureRefusal)
    // An ordinary depth is fine.
    expect(() => redactor().redactFeedLine(`{"a":${'['.repeat(100)}${']'.repeat(100)}}`)).not.toThrow()
  })

  it('refuses a string whose compatibility form is more than twice its length, promptly', () => {
    const flood = 'ﷺ'.repeat(60_000)
    const start = performance.now()
    expect(message(() => redactor().redactDocument({ t: flood }))).toMatch(/^RedactionRefusal: .*compatibility form/)
    expect(message(() => assertRedacted({ t: flood }))).toMatch(/^RedactionRefusal: .*compatibility form/)
    expect(performance.now() - start).toBeLessThan(1_000)
    // A short one is refused by its own ratio, well under the document's budget.
    // Refused before any rule runs, not only by the audit after them.
    expect(message(() => redactor().redactDocument({ t: '\ufdfa'.repeat(10) }))).toMatch(
      /^RedactionRefusal: a string whose compatibility form/,
    )
    // A line of many short ones is refused by the document's budget.
    const many = JSON.stringify({ t: new Array<string>(200_000).fill('ﷺ') })
    const t0 = performance.now()
    expect(message(() => redactor().redactFeedLine(many))).toMatch(/^RedactionRefusal: .*compatibility form/)
    expect(performance.now() - t0).toBeLessThan(3_000)
    // An ellipsis, a ligature or one such character in a sentence is fine.
    expect(() => red({ t: '…', u: 'ﬁx the ﬂow', v: 'said ﷺ today' })).not.toThrow()
    expect(red({ w: 'ﬁx by ana@acme.com' })).toBe(JSON.stringify({ w: 'fix by redacted@example.invalid' }))
  })

  it('keeps the installation literals out of a serialised or inspected redactor', () => {
    const r = createRedactor(['acme-secret-project', '/Users/zora'])
    expect(JSON.stringify(r)).not.toContain('acme-secret-project')
    expect(inspect(r, { depth: 10 })).not.toContain('acme-secret-project')
    expect(inspect(r, { depth: 10 })).not.toContain('/Users/zora')
  })
})

describe('a person refusal names no person', () => {
  it('says what was refused without the value', () => {
    const said = message(() => createRedactor().redactDocument({ requested_by: 'week', report: { by: 'week' } }))
    expect(said).toMatch(/a person the document names in a person field/)
    expect(said).not.toContain('week"')
  })

  it('reports RedactionRefusal instances', () => {
    expect(() => createRedactor().redactFeedLine(`{"a":${'['.repeat(5_000)}${']'.repeat(5_000)}}`)).toThrow(RedactionRefusal)
  })
})

describe('a notice goes through the person pass too (review round 2 of PR 478)', () => {
  // A notice is free text with no fields, so the only people the redactor can
  // find in it are the ones the recording's documents named. At da86190 a
  // notice saw no people at all: `redactText` ran with an empty person table,
  // and a requester's name went into the fixture verbatim while the same name
  // in a document became a pseudonym.
  const name = 'Dana Whitfield'

  it('replaces a person a document named, with the pseudonym the document gave them', () => {
    const r = redactor()
    const document = r.redactDocument({ runs: [{ requested_by: name }] }) as { runs: { requested_by: string }[] }
    const pseudonym = document.runs[0]?.requested_by ?? ''
    expect(pseudonym).toMatch(PERSON_PSEUDONYM_RE)
    const notice = r.redactText(`queued run 1234 by ${name} for review`)
    expect(notice).toBe(`queued run 1234 by ${pseudonym} for review`)
    expect(r.redactText(`queued for whitfield, dana`)).toBe(`queued for ${pseudonym}`)
  })

  it('keeps people across documents, so a later notice still knows an earlier requester', () => {
    const r = redactor()
    r.redactDocument({ runs: [{ requested_by: name }] })
    r.redactDocument({ runs: [{ requested_by: 'Ola Brandt' }] })
    const notice = r.redactText(`retrying for ${name} and Ola Brandt`)
    expect(notice).not.toContain('Whitfield')
    expect(notice).not.toContain('Brandt')
  })

  it('cannot know a person no document has named yet, which is why the recorder re-runs notices at the end', () => {
    const r = redactor()
    expect(r.redactText(`queued by ${name}`)).toBe(`queued by ${name}`)
    r.redactDocument({ runs: [{ requested_by: name }] })
    expect(r.redactText(`queued by ${name}`)).not.toContain('Whitfield')
  })

  it('audits against the same people, so a name left in a notice is a problem', () => {
    // `problems()` is the audit a recording runs over what it is about to
    // write. Audited against no people, a requester's name left in a notice
    // was clean; against the redactor's own table, it is a finding.
    const r = redactor()
    expect(r.problems(`queued by ${name}`)).toEqual([])
    r.redactDocument({ runs: [{ requested_by: name }] })
    expect(r.problems(`queued by ${name}`).join('\n')).toMatch(/a person the document names/)
    expect(r.problems(r.redactText(`queued by ${name}`))).toEqual([])
  })
})
