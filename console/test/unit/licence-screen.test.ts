// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The licence key's section (subscriptions plan S2.7; the design is
// `docs/licensing/licence-key.md`, "Storage and hand-off (F41)", and the state
// the console draws a lapse in is `docs/design/lapse-read-only.md`).
//
// **The four things this file holds.**
//
//   - **the answer is the verifier's, drawn closed.** A state word and a claim
//     set, or the sentence that refuses a key; a state this build does not know
//     withholds everything rather than drawing the word (`LICENCE_STATE_WORDS` is
//     the console's own table, and a fourth state a later CLI grew is a reply
//     this build does not read).
//   - **the key is never drawn, and the section has nothing to redact.** Neither
//     the read's nor the save's answer carries the key text -- what an operator
//     reads is an organisation, a tier, a seat count and two days -- so the case
//     below that feeds a claim value a control character holds the door that
//     *is* there: a claim's string reaches the screen through `putText` with its
//     class and through nothing else.
//   - **no link, in any state.** The screen carries no address and no
//     "where to buy one": console code names no host of the project's own (the
//     plan's own rule for this task). The fake element below refuses the
//     navigating attribute outright, and the case scans what was drawn for a
//     link-shaped attribute and for an address in the text.
//   - **the box and the section's two states.** An installation with no key and
//     one whose key does not verify both take a paste (the design's "the key
//     screen in particular is never gated"); a read that could not be made at all
//     -- the console may not keep the record -- offers no box, because a control
//     that could only ever refuse would be a control that lied.
//
// The fake screen is `retirements.test.ts`'s, in this file's own words: it
// **throws** on the attributes a screen may never set rather than recording
// them, which is what makes "this section carries no link" a property of the
// drawing rather than a promise in a comment.

import { describe, expect, it } from 'vitest'

import {
  LICENCE_ABSENT_BECAUSE,
  LICENCE_BOX_CLASS,
  LICENCE_BECAUSE_CLASS,
  LICENCE_CLAIMS_CLASS,
  LICENCE_CLASS,
  LICENCE_FEATURES_TERM,
  LICENCE_HINT,
  LICENCE_NO_KEY_WORD,
  LICENCE_OPERATION,
  LICENCE_OUTCOME_CLASS,
  LICENCE_REFUSED_WORD,
  LICENCE_SAVED_WORD,
  LICENCE_SHAPE_BECAUSE,
  LICENCE_SOURCE,
  LICENCE_STATE_CLASS,
  LICENCE_SUBMIT_CLASS,
  LICENCE_TERM_CLASS,
  LICENCE_TIER_TERM,
  LICENCE_TITLE,
  LICENCE_UNREADABLE_WORD,
  LICENCE_VALUE_CLASS,
  LICENCE_WORD_CLASS,
  buildLicence,
  fillLicence,
  licenceSaveOutcomeOf,
  renderLicenceOutcome,
} from '../../src/renderer/screens/licence'
import type { ScreenElement, ScreenFactory } from '../../src/renderer/screens/licence'
import { REFUSAL_UNREADABLE } from '../../src/renderer/screens/degraded'

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

/** drawnSection draws one model into a fresh host and answers both. */
function drawnSection(
  inputs: Parameters<typeof buildLicence>[0],
): { readonly host: FakeScreen; readonly drawn: FakeScreen } {
  const host = new FakeScreen('div')
  fillLicence(factory, buildLicence(inputs), host)
  return { host, drawn: one(host, LICENCE_CLASS) }
}

/** A_CLAIM_SET is one verifier's claim set (`LicenceClaims`,
 * `src/main/licence-store.ts`): the five fields a reader of a key needs and the
 * feature list. **No key, and nothing key-shaped**: a claim set is what a key
 * *says*, which is the whole reason the console may draw one. */
const A_CLAIM_SET: Readonly<Record<string, unknown>> = Object.freeze({
  organisation: 'acme.example',
  tier: 'team',
  seats: 4,
  issued: 1_767_225_600,
  expires: 1_798_761_600,
  features: ['entitlements', 'fleet'],
})

describe('the licence key’s section', () => {
  it('draws the verifier’s state and claim set, and never a key', () => {
    const { drawn: section } = drawnSection({
      raw: { state: 'grace', claims: A_CLAIM_SET },
      because: null,
    })
    // The panel's own name, the state word the verifier answered and the source
    // it names -- there is no command line here, so the source is the store.
    expect(one(section, LICENCE_CLASS).getAttribute('data-operation')).toBe(LICENCE_OPERATION)
    expect(drawn(section)).toContain(LICENCE_TITLE)
    expect(drawn(section)).toContain('in grace')
    expect(drawn(section)).toContain(LICENCE_SOURCE)
    // The claim set, term by term: an organisation drawn as the free text it is
    // (the class's own rules redact a value that reads as a host), the tier as a
    // word, the seats as the number the answer carried, and the features.
    expect(all(section, LICENCE_TERM_CLASS)[0]?.textContent).toBe('Organisation')
    expect(one(section, LICENCE_CLAIMS_CLASS).childNodes()).toHaveLength(12)
    expect(drawn(section)).toContain('acme.example')
    expect(drawn(section)).toContain(LICENCE_TIER_TERM)
    expect(drawn(section)).toContain('team')
    expect(drawn(section)).toContain('4')
    expect(drawn(section)).toContain(LICENCE_FEATURES_TERM)
    expect(drawn(section)).toContain('entitlements, fleet')
    // No reason sentence is drawn beside a state the verifier answered.
    expect(all(section, LICENCE_BECAUSE_CLASS)).toHaveLength(0)
    // And the box, with the hint that says where the key goes.
    expect(one(section, LICENCE_BOX_CLASS).getAttribute('type')).toBe('text')
    expect(one(section, LICENCE_SUBMIT_CLASS).textContent).not.toBeNull()
    expect(drawn(section)).toContain(LICENCE_HINT)
  })

  it('draws only the claim rows the answer carries, and puts every value through the door', () => {
    // A claim set with no features and no tier draws no row for either: an empty
    // cell would say the key grants nothing, which is a claim about a document
    // that did not say it. And a value that arrived with a control character in
    // it reaches the screen through `putText`, which is the only door a
    // document's string has onto this screen.
    const { drawn: section } = drawnSection({
      raw: { state: 'valid', claims: { organisation: 'acme\u0000.example', seats: 2 } },
      because: null,
    })
    expect(drawn(section)).toContain('acme')
    expect(drawn(section), 'a control character reached the screen').not.toContain('\u0000')
    expect(drawn(section)).not.toContain(LICENCE_TIER_TERM)
    expect(drawn(section)).not.toContain(LICENCE_FEATURES_TERM)
    // Two rows: the organisation and the seats, each a term and a value.
    expect(one(section, LICENCE_CLAIMS_CLASS).childNodes()).toHaveLength(4)
    expect(all(section, LICENCE_VALUE_CLASS)).toHaveLength(2)
  })

  it('draws a key that does not verify as a state, with the verifier’s own sentence, and keeps the box', () => {
    const { drawn: section } = drawnSection({
      raw: { state: 'refused', reason: 'the licence key’s signature does not verify' },
      because: null,
    })
    // Not "no key": the installation holds something and nothing verifies it,
    // and the two ask different things of the operator.
    expect(one(section, LICENCE_WORD_CLASS).textContent).toBe(LICENCE_REFUSED_WORD)
    expect(drawn(section)).toContain('the licence key’s signature does not verify')
    expect(all(section, LICENCE_CLAIMS_CLASS)).toHaveLength(0)
    // The way out of this state is a paste, so the box is here.
    expect(all(section, LICENCE_BOX_CLASS)).toHaveLength(1)
  })

  it('withholds a state word this build does not know, claims and all', () => {
    // The verifier's states are a closed set and this console's words for them
    // are a table: a fourth state a later CLI grew is a reply this build does not
    // read, so nothing of it is drawn -- neither the word (an operator would have
    // to interpret it) nor the claims (this console would be vouching for a state
    // it does not understand).
    const { drawn: section } = drawnSection({
      raw: { state: 'trial', claims: A_CLAIM_SET },
      because: null,
    })
    expect(drawn(section)).not.toContain('trial')
    expect(drawn(section)).not.toContain('acme.example')
    expect(drawn(section)).toContain(LICENCE_SHAPE_BECAUSE)
    expect(all(section, LICENCE_CLAIMS_CLASS)).toHaveLength(0)
    // A read this build could not read is not a paste that would help: the box
    // is drawn only when the section knows what the store holds.
    expect(all(section, LICENCE_BOX_CLASS)).toHaveLength(0)
    expect(all(section, LICENCE_SUBMIT_CLASS)).toHaveLength(0)
  })

  it('draws no key and no reason as a key's own sentence, and offers no box where the store cannot answer', () => {
    // The one refusal that takes the box away is a read the console could not
    // make at all: a machine where the console may not keep records
    // (`refusedLicenceStore`, `src/main/licence-store.ts`). The box would take a
    // key the console has nowhere to put.
    const { drawn: section } = drawnSection({ raw: null, because: LICENCE_ABSENT_BECAUSE })
    expect(one(section, LICENCE_BECAUSE_CLASS).textContent).toBe(LICENCE_ABSENT_BECAUSE)
    expect(all(section, LICENCE_BOX_CLASS)).toHaveLength(0)
    // And a bridge that answered something that is not a licence reply at all is
    // the same state -- nothing was answered -- with the section's own sentence
    // rather than a broken one.
    const absent = drawnSection({ raw: 'not a licence reply', because: null })
    expect(drawn(absent.drawn)).toContain(LICENCE_ABSENT_BECAUSE)
    expect(all(absent.drawn, LICENCE_BOX_CLASS)).toHaveLength(0)
  })

  it('offers the box to an installation that holds no key at all, and says which state that is', () => {
    // "No key" is a state and not an error: a free installation, or one that has
    // never been given a key, is exactly the case the box is for.
    const { drawn: section } = drawnSection({ raw: { state: 'none' }, because: null })
    expect(one(section, LICENCE_WORD_CLASS).textContent).toBe(LICENCE_NO_KEY_WORD)
    expect(all(section, LICENCE_BOX_CLASS)).toHaveLength(1)
    expect(all(section, LICENCE_CLAIMS_CLASS)).toHaveLength(0)
  })

  it('carries no link and no address, in any of its states', () => {
    // The plan's own rule for this task: console code carries no link naming the
    // project's own host. The fake element throws on the navigating attribute, so
    // drawing at all is half the claim; the other half is the scan -- no node of
    // any state's tree carries a link-shaped attribute or sets an address in its
    // text.
    const states: readonly Parameters<typeof buildLicence>[0][] = [
      { raw: { state: 'valid', claims: A_CLAIM_SET }, because: null },
      { raw: { state: 'none' }, because: null },
      { raw: { state: 'refused', reason: 'nothing verifies this key' }, because: null },
      { raw: null, because: LICENCE_ABSENT_BECAUSE },
    ]
    for (const inputs of states) {
      const { drawn: section } = drawnSection(inputs)
      const nodes = walk(section)
      expect(nodes.length).toBeGreaterThan(3)
      for (const node of nodes) {
        for (const name of node.attributes.keys()) {
          expect(name.toLowerCase(), 'this section set a link-shaped attribute').not.toContain('href')
        }
      }
      expect(drawn(section).toLowerCase()).not.toContain('http')
      expect(drawn(section)).not.toContain('://')
    }
  })

  it('never draws a state word the verifier did not answer, and never an empty state', () => {
    // The two directions of the table: a state the verifier answered is drawn in
    // this console's words, and a section with no state at all draws no state
    // word rather than a blank line an operator would read as "none".
    const { drawn: section } = drawnSection({
      raw: { state: 'expired', claims: A_CLAIM_SET },
      because: null,
    })
    expect(one(section, LICENCE_WORD_CLASS).textContent).toBe('expired')
    const unavailable = drawnSection({ raw: null, because: LICENCE_ABSENT_BECAUSE })
    expect(all(unavailable.drawn, LICENCE_WORD_CLASS)).toHaveLength(0)
    // The panel still says which state it is in -- not read -- so the absence
    // of a state word is not read as a state word.
    expect(one(unavailable.drawn, LICENCE_STATE_CLASS).textContent).toBe('not read')
  })
})

describe('a save’s answer, read closed', () => {
  it('reads the envelope and nothing else', () => {
    // The reply is the envelope the main process builds, and a save has no
    // payload of its own beyond the state the verifier answers with. Anything
    // else -- a reply that is not an envelope, an `ok` with no `data`, a member
    // the contract does not give -- is a sentence saying so rather than a screen
    // that claims a write.
    expect(licenceSaveOutcomeOf({ ok: true, data: { state: 'valid', claims: A_CLAIM_SET } })).toEqual(
      { saved: true, sentence: LICENCE_SAVED_WORD },
    )
    expect(licenceSaveOutcomeOf({ ok: false, reason: 'the key does not verify' })).toEqual({
      saved: false,
      sentence: 'the key does not verify',
    })
    for (const reply of [
      null,
      'kept',
      { ok: true },
      { ok: true, data: { state: 'valid', claims: A_CLAIM_SET }, extra: 1 },
      { ok: false, reason: 'no', data: {} },
      { ok: false, reason: '' },
      { ok: 'yes', data: {} },
      { ok: true, data: 'kept' },
      { ok: true, data: { claims: A_CLAIM_SET } },
    ]) {
      const read = licenceSaveOutcomeOf(reply)
      expect(read.saved, `${JSON.stringify(reply)} was read as a write`).toBe(false)
      expect(read.sentence, `${JSON.stringify(reply)} drew no sentence`).not.toBe('')
    }
  })

  it('draws the sentence it read, and nothing else', () => {
    const host = new FakeScreen('div')
    host.append(renderLicenceOutcome(factory, { saved: true, sentence: LICENCE_SAVED_WORD }))
    expect(one(host, LICENCE_OUTCOME_CLASS).textContent).toBe(LICENCE_SAVED_WORD)
    // One element and one line of text: the entry replaces what the region held
    // with this, so a second attempt's answer is never drawn under the first's.
    expect(one(host, LICENCE_OUTCOME_CLASS).childNodes()).toHaveLength(0)
    // A sentence the console cannot draw at all is the console's own word for it
    // rather than a blank region (`REFUSAL_UNREADABLE`, `./degraded`).
    const refused = licenceSaveOutcomeOf({ ok: false, reason: 'x'.repeat(100_000) })
    expect(refused.sentence).toBe(REFUSAL_UNREADABLE)
    expect(LICENCE_UNREADABLE_WORD).not.toBe('')
  })
})
