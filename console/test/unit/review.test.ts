// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The review inbox (desktop-console v2 plan Task V2.7.1; v1 design section 6.2
// screen 1, section 5.1 rule 2 and its gap 7, design note F26).
//
// The plan's sentence is one line -- "finished jobs with their PR numbers and
// URLs **as text**, never as a link, until a document carries the repository's
// provider" -- and its verification is two things: the console's own test
// command, and "a test that no `pr_url` reaches `openExternal`". Both halves of
// that sentence are held here, and each of them is a claim about a different
// layer:
//
//   1. **the list is the CLI's own.** `ready[]` is what `internal/watch/json.go`
//      marshals from `ReadyForReview` (`internal/watch/filter.go`: finished rows
//      whose outcome is `ready`, newest first) and what `internal/watch/render.go`
//      prints as `Ready for your review (%d)`. The console re-derives nothing --
//      not the order, not which finished runs belong -- so a frame whose
//      `finished[]` and `ready[]` disagree is drawn from `ready[]`, and the
//      model's rows are the document's list, in the document's order.
//   2. **the pull request is text.** A `pr_number` is drawn as `#412` and a
//      `pr_url` through `putText`'s `url` class, and neither is written into
//      `data-href` -- the one attribute `src/renderer/app.ts` leaves a link
//      destination in, and the only thing `link.openExternal` acts on. That is
//      the sharp half: the URL used below **is** one the main process's
//      allow-list would open (`https://github.com/...`), and the inbox still
//      builds no link from it, because no document on this tree says which
//      provider the run's repository is on (gap 7, still open).
//   3. **the section is drawn whether or not it has a row.** "Nothing is waiting
//      for you" is an answer, and it says "in this feed" because `ready[]` also
//      ships empty when the runs bucket could not be read -- a distinction the
//      CLI's own `queued_note` carries on the header (`live-note`), not a second
//      reason invented here.
//
// The last case in this file drives the whole entry, which is the only way to
// make the plan's `openExternal` claim about the *wiring* rather than about the
// drawing: a screen that writes no `data-href` and an entry that never turns one
// into a call are two different facts, and the plan asks for the pair.

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK, formatReading, measured } from '../../src/shared/availability'
import { decodeSnapshot } from '../../src/shared/schema'
import type { LiveDocument } from '../../src/shared/schema'
import type { JSONDoc } from '../../src/source/source'
import { DESTINATION_ATTRIBUTE } from '../../src/renderer/app'
import { bootDashboard, VIEW_PANEL_CLASS } from '../../src/renderer/entry'
import {
  BurnHistory,
  LIVE_PR_NO_LINK_REASON,
  LIVE_REVIEW_CAP,
  LIVE_REVIEW_EMPTY,
  LIVE_REVIEW_TITLE,
  buildLiveModel,
  renderLive,
} from '../../src/renderer/screens/live'
import type { LiveFactory, LiveModel, ScreenElement } from '../../src/renderer/screens/live'
import { PR_NO_LINK_REASON } from '../../src/renderer/screens/run'
import {
  FakeDocument,
  FakeNode,
  anOkCompatibility,
  everyNode,
} from '../support/fakeDom'
import { SYNTHETIC_SOURCE } from './synthetic'

// ---------------------------------------------------------------------------
// The documents. One is built here rather than read from the recording,
// because the recorded feed is the place the CLI's `ready[]` is *not*: every
// one of its frames carries `ready: []` beside a finished run whose outcome is
// `ready`, which `ReadyForReview` cannot produce (the fixture is regenerated
// from `synthetic.ts`, so a case that needs a ready run builds the list it
// means). Everything else is the synthetic source's own first frame, so the
// documents here are the same shape the rest of the suite reads from disk.
// ---------------------------------------------------------------------------

/** aReadyRow is one `ready[]` entry on the wire: exactly the keys
 * `internal/watch/json.go`'s `JSONFinished` marshals, which `schema.ts` closes
 * a finished row over. */
function aReadyRow(overrides: JSONDoc = {}): JSONDoc {
  return {
    run: '20261015-101500-aa02',
    slug: 'r-4c25cf20/r-f1aa9214',
    repo: 'fixture-owner/fixture-repo',
    title: 'merge feat/console-fixtures for fixture-owner/fixture-repo',
    status: 'ready',
    outcome: 'ready',
    failed: false,
    pr_url: 'https://github.com/fixture-owner/fixture-repo/pull/412',
    pr_number: 412,
    age_seconds: 6400,
    ...overrides,
  }
}

/** withoutKeys is a row with keys removed, which is how the wire spells an
 * `omitempty` field: not `null`, and not there. */
function withoutKeys(row: JSONDoc, ...keys: readonly string[]): JSONDoc {
  const out: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(row)) {
    if (!keys.includes(key)) out[key] = value
  }
  return out
}

/** theSyntheticFrame is the invented feed's first document, undecoded. It is the
 * frame every case here starts from: a repository, a running run, nothing
 * finished. */
function theSyntheticFrame(): JSONDoc {
  const first = SYNTHETIC_SOURCE[0]
  if (first === undefined || !('document' in first)) {
    throw new Error('the synthetic source’s first record is not a document')
  }
  return first.document
}

/** aFrame is one document the schema accepted: the synthetic frame with the
 * review list (and anything else a case moves) in it. It goes **through
 * `decodeSnapshot`** rather than around it, so what the screen is handed is the
 * same value a live feed would produce -- and the two doors differ by field: a
 * key the wire does not carry is refused here (`schema.ts` closes a finished row
 * with `closed(item, FINISHED_KEYS, …)`), while a `pr_number` that is a string
 * is **not** refused -- `optionalInt` answers `null` for anything that is not a
 * finite number, which is the same answer it gives a key that is absent, so the
 * row draws with no number rather than stopping the screen. */
function aFrame(ready: readonly JSONDoc[], overrides: JSONDoc = {}): LiveDocument {
  const decoded = decodeSnapshot(
    { ...theSyntheticFrame(), ready: [...ready], ...overrides },
    'the review case',
  )
  if (!decoded.ok) throw new Error(`the schema refused the document: ${decoded.skew.because}`)
  return decoded.value
}

/** aModel is the screen's model for a list of ready rows, with anything else a
 * case moves in the frame beside them. */
function aModel(ready: readonly JSONDoc[], overrides: JSONDoc = {}): LiveModel {
  return buildLiveModel(aFrame(ready, overrides), new BurnHistory())
}

/** readyRows is `count` rows, each with its own run id, minted from the index:
 * `…101500…`, `…101501…` and so on, so the ids **ascend** and the list is
 * oldest-first -- which is deliberately *not* the order `ReadyForReview` writes
 * (`sort.SliceStable` on `StartedAt`, descending). The console must keep
 * whatever order the document carried rather than impose one, and a fixture
 * already in the CLI's order could not tell "kept it" apart from "sorted it
 * back". The pull request number and URL move with the id, so the first row of
 * the document is `#412`. */
function readyRows(count: number): readonly JSONDoc[] {
  const out: JSONDoc[] = []
  for (let index = 0; index < count; index += 1) {
    out.push(
      aReadyRow({
        run: `20261015-1015${String(index).padStart(2, '0')}-aa02`,
        pr_number: 412 + index,
        pr_url: `https://github.com/fixture-owner/fixture-repo/pull/${String(412 + index)}`,
      }),
    )
  }
  return out
}

// ---------------------------------------------------------------------------
// The fake screen, the shape `live.test.ts` uses: the unit suite has no
// `document`, and the point is the observation. Every attribute that could make
// a window do something throws, so a drawing that reached for one fails here.
// ---------------------------------------------------------------------------

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
      throw new Error(`the review inbox may not set the ${name} attribute`)
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

const factory: LiveFactory = {
  create: (tag) => new FakeScreen(tag),
  createNS: (tag) => new FakeScreen(tag),
}

/** drawnSection is the review section of a model's drawing. It is found by
 * class rather than returned by a private function, so the case is about what
 * `renderLive` put on the screen. */
function drawnSection(model: LiveModel): FakeScreen {
  const root = renderLive(factory, model, 'dark') as FakeScreen
  const found = walk(root).filter((node) => node.classes.includes('live-review'))
  expect(found, 'the Live screen drew no review inbox').toHaveLength(1)
  const section = found[0]
  if (section === undefined) throw new Error('no review inbox')
  return section
}

/** walk visits every node of a tree, the root first. */
function walk(node: ScreenElement): readonly FakeScreen[] {
  const fake = node as FakeScreen
  return [fake, ...fake.childNodes().flatMap((child) => walk(child))]
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

/** texts is the text of every node carrying a class. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((leaf) => leaf.textContent)
}

/** drawnText is all the text under a tree, including every `title`: a refusal
 * that drew the value it refused would show up here rather than in a cell. */
function drawnText(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => `${leaf.textContent ?? ''} ${leaf.getAttribute('title') ?? ''}`)
    .join('\n')
}

// ---------------------------------------------------------------------------
// The model: the CLI's list, the CLI's count.
// ---------------------------------------------------------------------------

describe('the review inbox’s model', () => {
  it('is the frame’s ready[], in the frame’s order, and re-derives neither', () => {
    // Three ready runs, in the order `readyRows` minted them -- ascending ids,
    // so oldest first, which is the opposite of `ReadyForReview`'s own order
    // (`sort.SliceStable` on `StartedAt`, descending) precisely so that this
    // case can see the order being kept rather than restored. The console sorts
    // nothing and filters nothing: a console that re-sorted would be a second
    // opinion about a list the CLI already decided, and the two would disagree
    // the day the CLI changed its mind.
    const rows = readyRows(3)
    const model = aModel(rows)
    expect(model.review.map((row) => row.run)).toEqual([
      '20261015-101500-aa02',
      '20261015-101501-aa02',
      '20261015-101502-aa02',
    ])
    expect(model.reviewCount).toBe(3)
    // Every field is the document's own, copied: the repository, the title, the
    // number, the URL and the age.
    expect(model.review[0]).toMatchObject({
      repo: 'fixture-owner/fixture-repo',
      title: 'merge feat/console-fixtures for fixture-owner/fixture-repo',
      prNumber: 412,
      prUrl: 'https://github.com/fixture-owner/fixture-repo/pull/412',
    })
    expect(formatReading(model.review[0]?.age ?? measured(0), 'seconds')).toBe('6,400 s')
  })

  it('is ready[] and not finished[], over a frame where the two disagree', () => {
    // The plan's list is the one waiting on a human. A finished run whose
    // outcome is `draft` or `none` is waiting on nobody, and `finished[]`
    // carries every finished run there is: a screen that drew the whole of it
    // would be a history, and `watch`'s own text view draws only the ready ones.
    //
    // **The frame disagrees with itself on purpose**, which is the headline
    // claim of this file made checkable: `ready[]` is `ReadyForReview`'s
    // selection out of `finished[]`, so a frame the CLI writes has the two in
    // step, and a case built from one of those cannot tell which list a screen
    // read. So both directions are here -- a finished run whose outcome is
    // `ready` that `ready[]` does not carry (a screen deriving the inbox from
    // `finished[]` would draw it) and a `ready[]` row no `finished[]` entry
    // mentions (a screen intersecting the two would lose it).
    const model = aModel([aReadyRow({ run: '20261015-101500-aa02' })], {
      finished: [
        aReadyRow({ run: '20261015-090000-aa01' }),
        aReadyRow({ run: '20261015-080000-aa00', status: 'draft', outcome: 'draft' }),
      ],
    })
    expect(model.review.map((row) => row.run)).toEqual(['20261015-101500-aa02'])
    expect(model.reviewCount).toBe(1)
    // The model carries no finished row at all: `finished[]` reaches this screen
    // nowhere, which is why the disagreement cannot be resolved anywhere else.
    expect(Object.keys(model)).not.toContain('finished')
  })

  it('bounds the list at LIVE_REVIEW_CAP and keeps the CLI’s own count beside it', () => {
    // `ready[]` is deliberately unfiltered by `--keep`, so a project nobody has
    // caught up with can carry thousands of rows. The cap is the drawn list's;
    // the count is the document's, taken before the cap, which is what makes
    // the section's heading the CLI's own number rather than the number of rows
    // that happened to fit.
    const model = aModel(readyRows(LIVE_REVIEW_CAP + 5))
    expect(model.review).toHaveLength(LIVE_REVIEW_CAP)
    expect(model.reviewCount).toBe(LIVE_REVIEW_CAP + 5)
    // What the cap drops is the **tail** of the list the document carried, and
    // the document here ascends by id -- so in this fixture the newest go
    // undrawn, which is the reverse of the CLI's own newest-first order and
    // exactly why the assertion below is written as "the first `CAP` rows of the
    // document" rather than as "the newest": the cap is a bound on the list, not
    // a rule about time.
    expect(model.review.map((row) => row.run)).toEqual(
      readyRows(LIVE_REVIEW_CAP).map((row) => row['run']),
    )
  })

  it('draws no pull request number that is not a positive whole number', () => {
    // `schema.ts`'s `optionalInt` guarantees a finite number and not a whole
    // one, and `#1.5`, `#0` and `#-3` are not pull requests. `null` here is "no
    // pull request to name", which the section draws by leaving the cell out --
    // the same rule `run.ts` keeps for the same fact.
    const model = aModel([
      aReadyRow({ run: 'a', pr_number: 412 }),
      aReadyRow({ run: 'b', pr_number: 0 }),
      aReadyRow({ run: 'c', pr_number: -3 }),
      aReadyRow({ run: 'd', pr_number: 1.5 }),
      aReadyRow({ run: 'e', pr_number: null }),
    ])
    expect(model.review.map((row) => row.prNumber)).toEqual([412, null, null, null, null])
  })

  it('is a row with no pull request at all, which the wire spells by omitting both keys', () => {
    // `pr_url` and `pr_number` are `omitempty` in `internal/watch/json.go`, so a
    // run that opened no pull request carries neither key. The model's `null` is
    // that fact, and never a value nobody filled in.
    const row = withoutKeys(aReadyRow({ run: '20261015-101500-aa02' }), 'pr_url', 'pr_number')
    const model = aModel([row])
    expect(model.review[0]?.prNumber).toBeNull()
    expect(model.review[0]?.prUrl).toBeNull()
  })

  it('carries a number the frame has and no URL beside it', () => {
    // `omitempty` is per key, so the two halves of "there is a pull request" can
    // arrive apart: `pr_number` without `pr_url` is a run whose URL the CLI did
    // not write (or the recorder redacted away), and the number is still this
    // row's own fact. Held on its own because every other case in this file
    // moves the two together, and a model that read `prNumber` only when
    // `pr_url` was there would pass all of them.
    const model = aModel([withoutKeys(aReadyRow({ run: '20261015-101500-aa02' }), 'pr_url')])
    expect(model.review[0]?.prNumber).toBe(412)
    expect(model.review[0]?.prUrl).toBeNull()
  })

  it('carries a URL the frame has and no number beside it', () => {
    // The other half, and the one the CLI's own `pr_number: 0` case looks like
    // on the wire: a finished run that opened a pull request the CLI recorded a
    // URL for and no number. The URL is drawn as text like any other, and the
    // number cell is simply not there -- which is not the same as a `#n/a`.
    const model = aModel([withoutKeys(aReadyRow({ run: '20261015-101500-aa02' }), 'pr_number')])
    expect(model.review[0]?.prNumber).toBeNull()
    expect(model.review[0]?.prUrl).toBe(
      'https://github.com/fixture-owner/fixture-repo/pull/412',
    )
  })
})

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

describe('the review inbox’s drawing', () => {
  it('draws the CLI’s heading, the CLI’s count and one row per ready run', () => {
    const section = drawnSection(aModel(readyRows(2)))
    // **The heading is the CLI's own line and it is one text run** (review of
    // 0.10.1). A heading drawn as four elements -- the word, `(`, the count and
    // `)` -- is four flex items, and the heading's `gap` then spaces them out to
    // `Ready for your review ( 2 )`: text neither the CLI (`internal/watch/
    // render.go`'s `readyForReview`) nor either design doc prints. The two
    // assertions are the pair the drawing has to keep: the run is single, so
    // there is nothing for a stylesheet to space, and the words are the CLI's,
    // so the parentheses hug the number.
    const heading = one(section, 'live-review-title')
    expect(heading.nodes, 'the heading is drawn as more than one run').toEqual([])
    expect(heading.textContent).toBe(`${LIVE_REVIEW_TITLE} (2)`)
    expect(texts(section, 'live-review-number')).toEqual(['#412', '#413'])
    expect(texts(section, 'live-review-url')).toEqual([
      'https://github.com/fixture-owner/fixture-repo/pull/412',
      'https://github.com/fixture-owner/fixture-repo/pull/413',
    ])
    expect(texts(section, 'live-review-repo')).toEqual([
      'fixture-owner/fixture-repo',
      'fixture-owner/fixture-repo',
    ])
    expect(texts(section, 'live-review-age')).toEqual(['6,400 s', '6,400 s'])
    expect(texts(section, 'live-review-run-id')).toEqual([
      '20261015-101500-aa02',
      '20261015-101501-aa02',
    ])
    // And nothing is drawn beside an empty list: the empty sentence is not here.
    expect(all(section, 'live-review-empty')).toEqual([])
  })

  it('draws the section, and says so, when nothing is ready', () => {
    const section = drawnSection(aModel([]))
    // The heading carries a zero, and **that zero is this console's own
    // decision, not the CLI's**: both renderers omit the whole section when
    // nothing is ready (`if len(ready) > 0`, `internal/watch/plain.go:89`;
    // `if !o.Help && len(o.Ready) > 0`, `internal/watch/render.go:302`), so
    // `Ready for your review (0)` is a line the CLI never prints. The section
    // stays -- "nothing is waiting for you" is an answer, and the sentence below
    // is what gives it -- and the count in the heading is drawn the way every
    // other count is, which is what the assertion is for.
    expect(one(section, 'live-review-title').textContent).toBe(`${LIVE_REVIEW_TITLE} (0)`)
    expect(one(section, 'live-review-empty').textContent).toBe(LIVE_REVIEW_EMPTY)
    expect(all(section, 'live-review-row')).toEqual([])
    // With no row there is no link to explain, so the reason is not drawn --
    // a sentence about a pull request that is not on the screen would be the
    // section talking about itself.
    expect(all(section, 'live-review-note')).toEqual([])
  })

  it('says it about this feed, because an empty ready[] has two causes', () => {
    // `ready[]` ships empty when nothing is waiting **and** when the runs bucket
    // could not be read at all; the CLI's own `queued_note` is what tells the
    // two apart, and the header draws it (`live-note`). A sentence here claiming
    // "nothing is waiting for you" would be the console answering a question the
    // document did not answer.
    expect(LIVE_REVIEW_EMPTY).toBe('no finished run in this feed is ready for review')
  })

  it('draws the pull request as text and writes no destination at all', () => {
    // The plan's own sentence, at the screen: the number and the URL are facts
    // about a finished run, not a control. `data-href` is where a destination
    // goes and the only attribute `link.openExternal` acts on, and no node of
    // this section carries one -- nor is any node an anchor, because an anchor
    // with nothing to open is a control that does nothing.
    const section = drawnSection(aModel([aReadyRow()]))
    for (const node of walk(section)) {
      expect(node.getAttribute(DESTINATION_ATTRIBUTE), `${node.tag} carries a destination`).toBeNull()
      expect(node.tag.toLowerCase()).not.toBe('a')
    }
    // Non-vacuity: the URL really is on the screen, as text.
    expect(texts(section, 'live-review-url')).toEqual([
      'https://github.com/fixture-owner/fixture-repo/pull/412',
    ])
  })

  it('draws each half of a pull request the frame carries on its own', () => {
    // `pr_url` and `pr_number` are two `omitempty` keys, so a row can carry one
    // without the other, and each cell is drawn from the key that is there: the
    // number as text, the URL through `putText`. What must **not** happen is a
    // placeholder for the missing half -- an `n/a` under a number, or a `#n/a`
    // where the number would be -- because the cell is left out entirely and the
    // row reads as a row that names one fact about its pull request.
    const numbered = drawnSection(
      aModel([withoutKeys(aReadyRow({ run: '20261015-101500-aa02' }), 'pr_url')]),
    )
    expect(texts(numbered, 'live-review-number')).toEqual(['#412'])
    expect(texts(numbered, 'live-review-url')).toEqual([])

    const linked = drawnSection(
      aModel([withoutKeys(aReadyRow({ run: '20261015-101500-aa02' }), 'pr_number')]),
    )
    expect(texts(linked, 'live-review-number')).toEqual([])
    expect(texts(linked, 'live-review-url')).toEqual([
      'https://github.com/fixture-owner/fixture-repo/pull/412',
    ])
    // Neither drawing smuggled an unavailable mark in for the absent half.
    expect(drawnText(numbered)).not.toContain(UNAVAILABLE_MARK)
    expect(drawnText(linked)).not.toContain(UNAVAILABLE_MARK)
  })

  it('draws the CLI’s own reason that no link is built, in run.ts’s words', () => {
    // One sentence, twice: the Run screen says it beside a run's pull request
    // and the inbox says it under its rows, and the two copies are held equal
    // here so they cannot drift into two different promises. It is a second copy
    // rather than an import because the screens are siblings and none owns the
    // others.
    expect(LIVE_PR_NO_LINK_REASON).toBe(PR_NO_LINK_REASON)
    const section = drawnSection(aModel([aReadyRow()]))
    expect(one(section, 'live-review-note').textContent).toBe(LIVE_PR_NO_LINK_REASON)
  })

  it('refuses a URL the console will not draw, and draws its own sentence instead', () => {
    // The URL is a job-written string like any other: it goes through
    // `putText`'s `url` class, so a `javascript:` URL, an `http` one, a URL
    // with whitespace in it and a URL carrying a credential in its query are
    // refused -- drawn as the unavailable mark with the console's reason in the
    // title, and **never the value that was refused**, which is exactly the
    // string that must not reach a document.
    const hostile = 'javascript:alert(1)'
    const section = drawnSection(aModel([aReadyRow({ pr_url: hostile })]))
    const cell = one(section, 'live-review-url')
    expect(cell.textContent).toBe(UNAVAILABLE_MARK)
    expect(cell.getAttribute('title')).not.toContain('alert')
    expect(cell.getAttribute(DESTINATION_ATTRIBUTE)).toBeNull()

    const notHttps = 'http://github.com/o/n/pull/1'
    expect(one(drawnSection(aModel([aReadyRow({ pr_url: notHttps })])), 'live-review-url').textContent).toBe(
      UNAVAILABLE_MARK,
    )
  })

  it('draws a URL with a credential in its userinfo minus the userinfo', () => {
    // The class's other answer, and it is not a refusal: `URL` parses
    // `%75ser:p%61ss@` as userinfo, so the text is rebuilt without it rather
    // than drawn as it arrived. What is held here is the property that matters
    // for an inbox full of job-written URLs -- the credential is not on the
    // screen, in the text or in a title -- not the exact spelling of the
    // rebuild, which `sanitize.test.ts` owns.
    const credential = aReadyRow({
      pr_url: 'https://octocat:swordfish@github.com/o/n/pull/1',
    })
    const section = drawnSection(aModel([credential]))
    const cell = one(section, 'live-review-url')
    expect(cell.textContent).toBe('https://github.com/o/n/pull/1')
    expect(drawnText(section)).not.toContain('swordfish')
    expect(cell.getAttribute(DESTINATION_ATTRIBUTE)).toBeNull()
  })

  it('draws a redaction as the redaction it is', () => {
    // The recorder replaces a url field whole, so a run whose pull request URL
    // was redacted carries `[redacted-url]`. That is not a link and it is not a
    // refusal: it is the truth about the field, and the CLI has a URL this
    // console is not being shown.
    const section = drawnSection(aModel([aReadyRow({ pr_url: '[redacted-url]' })]))
    expect(one(section, 'live-review-url').textContent).toBe('[redacted-url]')
    expect(one(section, 'live-review-url').getAttribute(DESTINATION_ATTRIBUTE)).toBeNull()
  })

  it('draws a hostile title and repository as the text they are', () => {
    // A title is a job's own string (the branch it merged, the repository it
    // merged into), and it is drawn as a `sentence` like every other free
    // string: terminal sequences stripped, one line. A repository with no slash
    // is the CLI's slug fallback, and the text class says so.
    const section = drawnSection(
      aModel([
        aReadyRow({
          repo: 'r-f1aa9214',
          title: 'merge \u001b[31mred\u001b[0m <script>alert(1)</script>',
        }),
      ]),
    )
    expect(one(section, 'live-review-run-title').textContent).toBe(
      'merge red <script>alert(1)</script>',
    )
    expect(one(section, 'live-review-repo').textContent).toBe('r-f1aa9214')
  })

  it('draws the count of the rows the cap left undrawn', () => {
    // The number under this label is the number of rows the cap left **out**,
    // not the number on the screen: `run.ts`'s log panel and `projects.ts`'s job
    // list both read `<label>: <not drawn> of <total>`, and the heading above
    // already carries the number of rows that fit. Two undrawn of 52, then.
    const section = drawnSection(aModel(readyRows(LIVE_REVIEW_CAP + 2)))
    const note = one(section, 'live-review-capped')
    // The console's own words are the `p`'s text and the two numbers are the
    // readings beside it, which is the shape the log panel's own "rows not
    // drawn" note has.
    expect(note.textContent).toBe('rows not drawn:')
    expect(note.nodes.map((node) => node.textContent)).toEqual(['2', 'of', '52'])
    expect(one(section, 'live-review-dropped').textContent).toBe('2')
    // The heading above carries the document's whole count -- 52, not the 50
    // rows that fit -- which is the other half of the same note.
    expect(one(section, 'live-review-title').textContent).toBe(`${LIVE_REVIEW_TITLE} (52)`)
    // And the note is absent when every row was drawn.
    expect(all(drawnSection(aModel(readyRows(1))), 'live-review-capped')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The wiring: the row is a door, and the URL is not.
// ---------------------------------------------------------------------------

/** A_REPLY is a bridge method's answer. */
type AReply = (args: unknown) => unknown

/** recordingBridge is the preload seam, with the calls recorded. It carries
 * `openExternal` as well as the reads, because the plan's claim is about what
 * the entry does **not** call: a fake without the method would pass a test that
 * reached for it and threw instead of recording. */
function recordingBridge(options: {
  readonly document: JSONDoc
  readonly replies?: Readonly<Record<string, AReply>>
}): { bridge: Record<string, unknown>; calls: { name: string; args: unknown }[] } {
  const calls: { name: string; args: unknown }[] = []
  const bridge: Record<string, unknown> = {
    getCompatibility: (): unknown => ({ ok: true, data: anOkCompatibility() }),
    getInstallation: (): unknown => ({
      ok: true,
      data: {
        chosen: true,
        id: '0123456789abcdef',
        project: 'acme',
        binary: '/opt/fugaro/bin/fugaro',
        cliVersion: '1.2.3',
        mode: 'ok',
      },
    }),
    openExternal: (args: unknown): unknown => {
      calls.push({ name: 'openExternal', args })
      return { ok: true, data: null }
    },
  }
  for (const name of ['diagnoseRun', 'readRunLogs', 'runLogLink'] as const) {
    bridge[name] = (args: unknown): unknown => {
      calls.push({ name, args })
      const reply = options.replies?.[name]
      return reply === undefined ? { ok: false, reason: `the console did not read ${name}` } : reply(args)
    }
  }
  // The one read the boot makes is the Live feed, and a case that does not name
  // its own answer gets the frame it handed in -- through the envelope
  // `CliBackedHandlers` builds (`src/source/source.ts`).
  const subscribe = options.replies?.['subscribeLive']
  bridge['subscribeLive'] = (args: unknown): unknown => {
    calls.push({ name: 'subscribeLive', args })
    return subscribe === undefined
      ? {
          ok: true,
          data: {
            document: options.document,
            receivedAt: '2026-10-15T12:00:00Z',
            sequence: 1,
            day: '2026-10-15',
            rolled: false,
            notices: [],
          },
        }
      : subscribe(args)
  }
  return { bridge, calls }
}

/** classed finds the drawn elements carrying one class. */
function classed(root: FakeNode, className: string): readonly FakeNode[] {
  return everyNode(root).filter((node) =>
    (node.attributes.get('class') ?? '').split(/\s+/).includes(className),
  )
}

/** flush lets the entry's own microtasks run: every read is answered through a
 * promise, and a drawing lands a turn after the call that asked for it. */
async function flush(): Promise<void> {
  for (let i = 0; i < 8; i += 1) await Promise.resolve()
}

/** boot runs the entry against a recording bridge over one frame, and answers
 * the document it drew into. */
async function boot(document: JSONDoc): Promise<{
  readonly drawn: FakeDocument
  readonly calls: readonly { readonly name: string; readonly args: unknown }[]
}> {
  const { bridge, calls } = recordingBridge({ document })
  const drawn = new FakeDocument()
  await bootDashboard({
    fugaro: bridge,
    document: drawn as unknown as Document,
    navigator: undefined,
  })
  return { drawn, calls }
}

describe('the review inbox’s wiring', () => {
  it('makes a review row the same door to Run detail a live row is', async () => {
    const frame = { ...theSyntheticFrame(), ready: [aReadyRow({ run: '20261015-101500-aa02' })] }
    const { drawn, calls } = await boot(frame)
    const rows = classed(drawn.body, 'live-review-row')
    expect(rows, 'the inbox drew no row for the frame’s ready run').toHaveLength(1)
    // The door is the row itself, and the run it opens is the one the row names
    // -- read off the drawn cell, exactly as `wireRuns` reads a live row's.
    ;(rows[0] as FakeNode).click()
    await flush()
    expect(calls.filter((call) => call.name === 'diagnoseRun').map((call) => call.args)).toEqual([
      { run: '20261015-101500-aa02' },
    ])
  })

  it('never hands a pr_url to openExternal, however clickable it looks', async () => {
    // **The plan's own test.** The URL here is one the main process's allow-list
    // would open (`ALLOWED_EXTERNAL_HOSTS` names `github.com`), and the inbox
    // still offers nothing to click: no element of the section carries a
    // destination, and the entry's whole click surface over it produces reads of
    // the run and no `openExternal` call at all. That is v1 design section 5.1
    // rule 2 holding at the layer below the drawing: a link built from a
    // `pr_url` would name a provider the document never stated (gap 7).
    const frame = {
      ...theSyntheticFrame(),
      ready: [
        aReadyRow({ run: '20261015-101500-aa02', pr_url: 'https://github.com/o/n/pull/412' }),
        aReadyRow({ run: '20261015-101501-aa02', pr_url: 'javascript:alert(1)' }),
      ],
    }
    const { drawn, calls } = await boot(frame)
    const panel = everyNode(drawn.body).find(
      (node) => node.attributes.get('class') === VIEW_PANEL_CLASS,
    )
    if (panel === undefined) throw new Error('the entry drew no panel')

    // The feed reached the screen: the allow-listed URL is drawn as text, and
    // the refused one as the unavailable mark. A case asserting only "no
    // openExternal" over a section that drew nothing would pass vacuously.
    expect(classed(panel, 'live-review-url').map((node) => node.textContent)).toEqual([
      'https://github.com/o/n/pull/412',
      UNAVAILABLE_MARK,
    ])

    // Nothing in the whole drawing carries a destination -- not the review
    // inbox, not the Live screen around it.
    const carrying = everyNode(drawn.body).filter(
      (node) => node.getAttribute(DESTINATION_ATTRIBUTE) !== null,
    )
    expect(carrying.map((node) => node.tagName)).toEqual([])

    // And the wiring agrees: every node of the section is clicked, and the only
    // calls that come out of it are reads of the run a row names.
    const section = classed(panel, 'live-review')[0]
    if (section === undefined) throw new Error('the panel drew no review inbox')
    for (const node of everyNode(section)) node.click()
    await flush()
    // Non-vacuity: the sweep did drive the entry (the rows are doors).
    expect(calls.filter((call) => call.name === 'diagnoseRun')).toHaveLength(2)
    expect(calls.some((call) => call.name === 'openExternal')).toBe(false)
  })
})
