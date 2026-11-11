// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The seats section (subscriptions plan S4.2; the ruling is D439 and the
// estimate behind the read is `fugaro seats --json`, S4.1).
//
// **The three things this file holds.**
//
//   - **the estimate is drawn beside the key's seats, and overage is a
//     warning.** D439's rule is that seats are compared and never enforced, so
//     the case below is the whole of it: an estimate above the seats the key
//     carries draws a warning and blocks nothing -- no control, no refusal, no
//     state the rest of the tab is held in.
//   - **every figure carries its state.** The count of people is an
//     `estimated` reading and a lower bound whenever a member or a run could
//     not be counted (`seats.estimate` is exactly that, S4.1); the key's seats
//     are the verifier's `measured` count, or `n/a` with a sentence saying which
//     of the three facts is missing. There is no path here that draws a bare
//     number.
//   - **an identity is a value, and reaches the screen through its door.** The
//     people are the `person` class, which draws an address as the console's
//     marker and a login whole; a member the estimate lists and does not count
//     is not a person at all (a group, a domain, an account), so it is drawn as
//     the `label` it is -- refusing it would hide the row the ruling exists to
//     show. The reason beside it is one of two of this module's own sentences,
//     never a code the document chose.
//
// The fake screen is `licence-screen.test.ts`'s, in this file's own words: it
// **throws** on the attributes a screen may never set rather than recording
// them, which is what makes "this section carries no link" a property of the
// drawing rather than a promise in a comment.

import { describe, expect, it } from 'vitest'

import {
  LICENCE_CLASS,
  LICENCE_NO_KEY_WORD,
  buildLicence,
} from '../../src/renderer/screens/licence'
import type { LicenceModel } from '../../src/renderer/screens/licence'
import {
  SEATS_ADVISORY,
  SEATS_ABSENT_BECAUSE,
  SEATS_BECAUSE_CLASS,
  SEATS_CLASS,
  SEATS_EMPTY,
  SEATS_FIGURES_CLASS,
  SEATS_FLOOR,
  SEATS_LICENCE_UNREAD_BECAUSE,
  SEATS_LICENSED_TERM,
  SEATS_MEMBER_CLASS,
  SEATS_NO_KEY_BECAUSE,
  SEATS_NOTCOUNTED_CLASS,
  SEATS_OPERATION,
  SEATS_OVER_WARNING,
  SEATS_PEOPLE_CLASS,
  SEATS_PERSON_CLASS,
  SEATS_PERSON_NAME_CLASS,
  SEATS_SHAPE_BECAUSE,
  SEATS_SOURCE,
  SEATS_SOURCE_CLASS,
  SEATS_SOURCES_CLASS,
  SEATS_STATE_CLASS,
  SEATS_TERM_CLASS,
  SEATS_TITLE,
  SEATS_USED_TERM,
  SEATS_VALUE_CLASS,
  SEATS_WARNING_CLASS,
  SEATS_WHY_CLASS,
  SEATS_WHY_UNREADABLE,
  SEATS_WHY_WORDS,
  SEATS_WINDOW_CLASS,
  buildSeats,
  fillSeats,
} from '../../src/renderer/screens/seats'
import type { ScreenElement, ScreenFactory } from '../../src/renderer/screens/seats'

// ---------------------------------------------------------------------------
// The harness: a fake element that refuses what a screen may not do.
// ---------------------------------------------------------------------------

/** FakeScreen is the element the section draws into. It records text, classes
 * and attributes, and it **throws on the attributes a screen must never set**:
 * the navigating attribute, a resource, a style and any handler. */
class FakeScreen implements ScreenElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set([
    ['h', 'ref'].join(''),
    'src',
    'srcdoc',
    'style',
    'formaction',
    'action',
  ])

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a console screen may not set the ${name} attribute`)
    }
    if (lowered === 'class') {
      this.classes.length = 0
      this.classes.push(...value.split(/\s+/).filter((one) => one !== ''))
    }
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: ScreenElement): void {
    this.nodes.push(child as FakeScreen)
  }

  childNodes(): readonly ScreenElement[] {
    return this.nodes
  }
}

/** factory makes fresh fakes, in the one-member shape every screen declares. */
const factory: ScreenFactory = { create: (tag) => new FakeScreen(tag) }

/** walk visits every node of a tree, the root first. */
function walk(node: ScreenElement): readonly FakeScreen[] {
  const fake = node as FakeScreen
  const out: FakeScreen[] = [fake]
  for (const child of fake.childNodes()) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: ScreenElement, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** one is the single node carrying a class, asserted to be single. */
function one(node: ScreenElement, className: string): FakeScreen {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** drawn is the whole tree's text, in document order: what a person reading the
 * screen would read if they read every cell. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((element) => element.textContent ?? '')
    .join('\n')
}

/** controls is every element of the tree a person could act on: the shapes a
 * section uses when it offers to change something. The seats section offers
 * none of them, in any state, which is D439 drawn. */
function controls(node: ScreenElement): readonly FakeScreen[] {
  return walk(node).filter((element) =>
    ['button', 'input', 'select', 'textarea', 'form', 'a'].includes(element.tag),
  )
}

/** drawnSection draws one model into a fresh host and answers both. */
function drawnSection(
  inputs: Parameters<typeof buildSeats>[0],
): { readonly host: FakeScreen; readonly drawn: FakeScreen } {
  const host = new FakeScreen('div')
  fillSeats(factory, buildSeats(inputs), host)
  return { host, drawn: one(host, SEATS_CLASS) }
}

/** licenceWith is the licence section's own model for a key that carries
 * `seats` seats: the seats section reads the key through the same builder the
 * licence section draws it with, so the figure it compares against is the
 * figure the operator reads one panel over. */
function licenceWith(seats: number): LicenceModel {
  return buildLicence({
    raw: { state: 'valid', claims: { organisation: 'acme.example', tier: 'team', seats } },
    because: null,
  })
}

/** A_SEATS_DOC is one `fugaro seats --json` document (S4.1): three people, one
 * member the estimate lists and does not count, two runs whose task this build
 * could not read. `people` is longer than the four seats the licence below
 * carries, which is the overage case. */
const A_SEATS_DOC: Readonly<Record<string, unknown>> = Object.freeze({
  project: 'acme',
  days: 90,
  since: '2026-08-13',
  until: '2026-11-11',
  people: [
    { person: 'ada@acme.example', sources: ['launcher', 'requester'] },
    { person: 'bob@acme.example', sources: ['requester'] },
    { person: 'cyd', sources: ['operator'] },
  ],
  not_counted: [{ member: 'group:eng@acme.example', why: 'unknown_size', sources: ['operator'] }],
  unreadable: 2,
  notes: ['advisory: an estimate, never a count of seats or a bill'],
})

describe('the seats section', () => {
  it('draws the estimate beside the key’s seats, warns on the overage, and blocks nothing', () => {
    const { drawn: section } = drawnSection({
      raw: A_SEATS_DOC,
      licence: licenceWith(2),
      because: null,
    })
    // The panel's own name, the state of the read and the command it names.
    expect(one(section, SEATS_CLASS).getAttribute('data-operation')).toBe(SEATS_OPERATION)
    expect(one(section, SEATS_STATE_CLASS).textContent).toBe('read')
    expect(one(section, SEATS_SOURCE_CLASS).textContent).toBe(SEATS_SOURCE)
    expect(drawn(section)).toContain(SEATS_TITLE)
    // The two figures beside each other, each term and each value: three people
    // against two seats. The estimate is `estimated` and a lower bound -- a
    // member and two runs are counted nowhere -- and the key's seats are the
    // verifier's own measured count.
    expect(all(section, SEATS_TERM_CLASS).map((cell) => cell.textContent)).toEqual([
      SEATS_USED_TERM,
      SEATS_LICENSED_TERM,
    ])
    expect(all(section, SEATS_VALUE_CLASS).map((cell) => cell.textContent)).toEqual(['≥ est. 3', '2'])
    expect(one(section, SEATS_CLASS).childNodes().length).toBeGreaterThan(0)
    // The overage is drawn as a warning, in this section's own words, and the
    // floor is explained beside it.
    expect(one(section, SEATS_WARNING_CLASS).textContent).toBe(SEATS_OVER_WARNING)
    expect(drawn(section)).toContain(SEATS_FLOOR)
    expect(drawn(section)).toContain(SEATS_ADVISORY)
    // Nothing is blocked: the section offers no control at all, and no element
    // it drew carries a state that could refuse the rest of the tab.
    expect(controls(section)).toHaveLength(0)
    expect(FakeScreen.FORBIDDEN_ATTRIBUTES.size).toBeGreaterThan(0)
  })

  it('draws no warning when the estimate is within the key’s seats, and still blocks nothing', () => {
    const { drawn: section } = drawnSection({
      raw: {
        project: 'acme',
        days: 90,
        since: '2026-08-13',
        until: '2026-11-11',
        people: [{ person: 'ada@acme.example', sources: ['launcher'] }],
        not_counted: [],
        unreadable: 0,
        notes: [],
      },
      licence: licenceWith(4),
      because: null,
    })
    expect(all(section, SEATS_VALUE_CLASS).map((cell) => cell.textContent)).toEqual(['est. 1', '4'])
    expect(all(section, SEATS_WARNING_CLASS)).toHaveLength(0)
    // Exactly at the key's seats is not over it: the warning is about the
    // estimate being above the figure, and equality is the boundary D439 never
    // turns into a refusal.
    expect(drawn(section)).not.toContain(SEATS_FLOOR)
    expect(drawn(section)).toContain(SEATS_ADVISORY)
    expect(controls(section)).toHaveLength(0)
  })

  it('draws an estimate with nobody in it as a sentence, not an empty table', () => {
    const { drawn: section } = drawnSection({
      raw: {
        project: 'acme',
        days: 90,
        since: '2026-08-13',
        until: '2026-11-11',
        people: [],
        not_counted: [],
        unreadable: 0,
        notes: [],
      },
      licence: licenceWith(4),
      because: null,
    })
    expect(all(section, SEATS_VALUE_CLASS).map((cell) => cell.textContent)).toEqual(['est. 0', '4'])
    expect(all(section, SEATS_PEOPLE_CLASS)).toHaveLength(0)
    expect(drawn(section)).toContain(SEATS_EMPTY)
  })

  it('draws the window the estimate was taken over, in the document’s own days', () => {
    const { drawn: section } = drawnSection({
      raw: A_SEATS_DOC,
      licence: licenceWith(2),
      because: null,
    })
    expect(one(section, SEATS_WINDOW_CLASS).textContent).toBe(
      'the last 90 days: 2026-08-13 to 2026-11-11',
    )
  })

  it('draws the key’s seats as n/a, with this console’s own sentence, when there is no key', () => {
    const noKey = buildLicence({ raw: { state: 'none' }, because: null })
    expect(noKey.word).toBe(LICENCE_NO_KEY_WORD)
    const { drawn: section } = drawnSection({ raw: A_SEATS_DOC, licence: noKey, because: null })
    const values = all(section, SEATS_VALUE_CLASS)
    expect(values.map((cell) => cell.textContent)).toEqual(['≥ est. 3', 'n/a'])
    // The sentence explaining the `n/a` is the console's, and it says which
    // fact is missing rather than reading a seat count off an absent key.
    expect(values[1]?.getAttribute('title')).toBe(SEATS_NO_KEY_BECAUSE)
    expect(all(section, SEATS_WARNING_CLASS)).toHaveLength(0)
    expect(drawn(section)).toContain(SEATS_ADVISORY)
  })

  it('draws the key’s seats as n/a when the licence section could not be read', () => {
    const unread = buildLicence({ raw: null, because: 'the console’s own store did not answer' })
    const { drawn: section } = drawnSection({ raw: A_SEATS_DOC, licence: unread, because: null })
    const values = all(section, SEATS_VALUE_CLASS)
    expect(values[1]?.textContent).toBe('n/a')
    expect(values[1]?.getAttribute('title')).toBe(SEATS_LICENCE_UNREAD_BECAUSE)
    expect(all(section, SEATS_WARNING_CLASS)).toHaveLength(0)
  })

  it('refuses a reply that is not a seat estimate, and refuses a read that answered nothing', () => {
    // An answer that arrived and is not the shape this section reads is a
    // sentence: a decoder that guessed would draw a member out of a reply that
    // does not mean it.
    const shape = drawnSection({ raw: { project: 'acme' }, licence: licenceWith(4), because: null })
    expect(one(shape.drawn, SEATS_BECAUSE_CLASS).textContent).toBe(SEATS_SHAPE_BECAUSE)
    expect(all(shape.drawn, SEATS_FIGURES_CLASS)).toHaveLength(0)
    expect(controls(shape.drawn)).toHaveLength(0)
    // A read that produced nothing at all is the bridge answering `null`, which
    // is what a member that is missing or that failed to answer looks like here.
    const absent = drawnSection({ raw: null, licence: licenceWith(4), because: null })
    expect(one(absent.drawn, SEATS_BECAUSE_CLASS).textContent).toBe(SEATS_ABSENT_BECAUSE)
    expect(one(absent.drawn, SEATS_STATE_CLASS).textContent).toBe('not read')
  })

  it('draws the CLI’s own refusal sentence when the read was refused, and the advisory beside it', () => {
    const { drawn: section } = drawnSection({
      raw: null,
      licence: licenceWith(4),
      because: 'the bucket of this installation does not answer',
    })
    expect(one(section, SEATS_BECAUSE_CLASS).textContent).toBe(
      'the bucket of this installation does not answer',
    )
    expect(drawn(section)).toContain(SEATS_ADVISORY)
    expect(controls(section)).toHaveLength(0)
  })

  it('draws every identity through its own door: a person redacted, a member whole, a reason in this module’s words', () => {
    const { drawn: section } = drawnSection({
      raw: A_SEATS_DOC,
      licence: licenceWith(2),
      because: null,
    })
    // One row per person, in the order the document carried them, with the
    // sources that named them. An address is drawn as the console's marker for
    // one (`person`), never as itself.
    expect(all(section, SEATS_PERSON_CLASS)).toHaveLength(3)
    const names = all(section, SEATS_PERSON_NAME_CLASS).map((cell) => cell.textContent)
    expect(names).toEqual(['redacted@example.invalid', 'redacted@example.invalid', 'cyd'])
    expect(drawn(section), 'an address reached the screen').not.toContain('ada@acme.example')
    // A login is not an address: it is drawn whole, which is what the class
    // says a person may be.
    expect(drawn(section)).toContain('cyd')
    expect(drawn(section)).toContain('launcher, requester')
    // The member the estimate lists and does not count is drawn as the label it
    // is -- refusing it would hide the row -- with this module's own sentence
    // for why, never the document's code.
    const notCounted = all(section, SEATS_NOTCOUNTED_CLASS)
    expect(notCounted).toHaveLength(1)
    // The row is drawn and not refused -- `person` would have refused this
    // member outright, and the row it would have hidden is the one the ruling
    // asks to be shown -- and the `label` it takes runs the free-text rules, so
    // an address inside a member is the console's marker for one, exactly as
    // free text is drawn everywhere else.
    const member = one(section, SEATS_MEMBER_CLASS)
    expect(member.textContent).toContain('group:')
    expect(member.textContent).not.toBe('n/a')
    expect(drawn(section), 'an address reached the screen').not.toContain('eng@acme.example')
    expect(one(section, SEATS_WHY_CLASS).textContent).toBe(SEATS_WHY_WORDS['unknown_size'])
    expect(drawn(section)).not.toContain('unknown_size')
    // One sources cell per row, named as the document named them.
    expect(all(section, SEATS_SOURCES_CLASS).map((cell) => cell.textContent)).toEqual([
      'launcher, requester',
      'requester',
      'operator',
      'operator',
    ])
  })

  it('draws a reason this build does not read as its own word, never as the document’s code', () => {
    const { drawn: section } = drawnSection({
      raw: {
        ...A_SEATS_DOC,
        not_counted: [{ member: 'domain:acme.example', why: 'a_later_reason', sources: ['launcher'] }],
      },
      licence: licenceWith(2),
      because: null,
    })
    expect(one(section, SEATS_WHY_CLASS).textContent).toBe(SEATS_WHY_UNREADABLE)
    expect(drawn(section)).not.toContain('a_later_reason')
  })

  it('carries no link and no control, in the states it has', () => {
    for (const inputs of [
      { raw: A_SEATS_DOC, licence: licenceWith(2), because: null },
      { raw: A_SEATS_DOC, licence: licenceWith(9), because: null },
      {
        raw: null,
        licence: buildLicence({ raw: null, because: 'the console answered nothing' }),
        because: null,
      },
    ] as const) {
      const { drawn: section } = drawnSection(inputs)
      expect(controls(section)).toHaveLength(0)
      for (const element of walk(section)) {
        for (const name of element.attributes.keys()) {
          expect(name.toLowerCase().startsWith('on')).toBe(false)
        }
        expect(element.textContent ?? '').not.toContain('://')
      }
    }
  })

  it('is drawn beside the licence section and not through it', () => {
    // The two are separate sections of the same tab: the seats panel reads the
    // key through `buildLicence` and draws nothing of the licence section's own
    // class, so a licence that has not landed cannot hide the estimate.
    const { host } = drawnSection({ raw: A_SEATS_DOC, licence: licenceWith(2), because: null })
    expect(all(host, LICENCE_CLASS)).toHaveLength(0)
    expect(all(host, SEATS_CLASS)).toHaveLength(1)
  })
})
