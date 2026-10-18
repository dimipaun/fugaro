// website/test/fold.mjs — the gist above the fold (website redesign, part P5; RF5, RF23;
// D187, D188).
//
// P5's claim is a claim about the first screen: the homepage's tagline, its one-line
// promise and its first install command fit a 390 × 844 viewport, and Getting Started's
// first screen is the command a reader came for. **The fold itself is the owner's check**
// (70b): whether the promise and the command are inside a real first screen is measured by
// the owner with the capture tooling, on a real browser, and no test in this repository can
// see a viewport (D187 chose the word ceiling plus that one live check over a browser-driven
// gate, and P5 does not change that).
//
// What a hermetic test *can* prove is the ordering and the content the fold claim rests on,
// and that is what this file does — on the built artifact, which is what a reader receives:
//
//   1. **The homepage's lead is the promise and nothing else.** Between the page's `h1` and
//      its first `h2` there is one element, and it is the lead paragraph. This is the shape
//      P5 gave the hero: the install section's heading sits above the commands, so the
//      promise is the whole of the text a reader meets before the first section — and so the
//      lead ceiling in `budget.mjs` is a ceiling on the promise rather than on the commands
//      (D188's lead is the region between the two headings, wherever each of them sits).
//      A hero whose install block sat between the promise and the heading would put the
//      commands, the terminal and the "every install path" link inside the lead: the page
//      would still pass the ceiling at 134 words only by being refused, and it would say
//      nothing about the promise. Moving the heading is the defect this case plants.
//   2. **Getting Started's first section opens with its commands.** Inside `#install`, no
//      paragraph of the section's own precedes the first command block, and the section's
//      own sentence is still there, after the paths. P5 moved that sentence from under the
//      heading to the end of the section for exactly this reason: a reader who came for the
//      command they run met a sentence about the page first, which is the ordering the plan
//      asks this part to fix.
//   3. **Every page that prints a command prints it under a heading.** The page's first `h2`
//      comes before its first `pre`, on all eleven routes, so no page opens with a command
//      that nothing above it names. This is the general form of the two cases above and the
//      rule a future page keeps: a command block belongs to a section, and the section's
//      heading is what a reader reads before it.
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file:
// every fact here is a statement about the artifact a reader receives, and none of it is
// read from a page's own sources.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { builtPages, find, parse, textOf, walk } from './lib/html.mjs'

/** A built page's `main`, as a flat list of its elements in document order. */
function mainInDocumentOrder(html) {
  const main = find(parse(html), 'main')
  assert.ok(main !== null, 'a built page carries no `main` landmark, so the ordering of its text cannot be read')
  return [...walk(main)]
}

/** The built page at a route: `'/'`, `'/getting-started/'`. */
function builtPage(url) {
  const page = builtPages().find((candidate) => candidate.url === url)
  assert.ok(page !== null && page !== undefined, `the build produced no ${url} page, and this check reads the built artifact`)
  return page.html
}

/** The `id` of a section of a page's `main`, found by its own `id` attribute. */
function sectionWithId(html, id) {
  const section = [...walk(find(parse(html), 'main'))].find((el) => el.attrs.get('id') === id)
  assert.ok(section !== undefined, `the page's \`main\` carries no #${id} section, and this check reads the section a reader lands in`)
  return section
}

test("the homepage's lead is the promise alone, and the first section starts after it", () => {
  const els = mainInDocumentOrder(builtPage('/'))
  const h1 = els.findIndex((el) => el.name === 'h1')
  const firstH2 = els.findIndex((el) => el.name === 'h2')

  assert.ok(h1 !== -1, 'the homepage renders no `h1`, so it has no lead to read')
  assert.ok(firstH2 > h1, 'the homepage renders no `h2` after its `h1`, so it has no first section and no lead region')

  const lead = els.slice(h1 + 1, firstH2)
  assert.deepEqual(
    lead.map((el) => `${el.name}.${el.attrs.get('class') ?? ''}`),
    ['p.hero-lead'],
    `the homepage's lead holds ${lead.length} element(s) between its \`h1\` and its first \`h2\`: ${lead.map((el) => el.name).join(', ')}. The promise is the whole of the page's gist above the fold, so the install block, the terminal and the link that follows them belong to the section the heading opens (P5, D188)`,
  )
  assert.ok(
    textOf(lead[0]).trim().length > 0,
    'the homepage\'s lead paragraph is empty, so the page\'s gist above the fold is its tagline and nothing else',
  )

  // And the heading that ends the lead opens the section the commands are in: the first
  // command of the page comes after it, never between the promise and it.
  const firstPre = els.findIndex((el) => el.name === 'pre')
  assert.ok(firstPre !== -1, 'the homepage prints no command at all, and its whole first screen is the install command (P5, RF5)')
  assert.ok(
    firstPre > firstH2,
    'the homepage prints a command above its first `h2`: the command is then part of the lead and above the heading that names it, which is the ordering P5 fixed',
  )
})

test('getting started opens its install section with the commands, and keeps its own sentence after them', () => {
  const html = builtPage('/getting-started/')
  const section = sectionWithId(html, 'install')
  const els = [...walk(section)]
  const firstPre = els.findIndex((el) => el.name === 'pre')

  assert.ok(firstPre !== -1, 'the install section prints no command, and a reader who came for the command they run has nowhere to find it')

  // A paragraph of the section's own — a `p` whose parent is the section rather than an
  // install path's article — is prose about the section. One above the first command is the
  // sentence P5 moved: the reader meets an explanation before the thing it explains.
  const proseBefore = els.slice(0, firstPre).filter((el) => el.name === 'p' && el.parent === section)
  assert.deepEqual(
    proseBefore.map((el) => textOf(el).trim()),
    [],
    'the install section opens with a sentence of its own before its first command: the section opens with the paths themselves, and what it says about them comes after them (P5)',
  )

  const proseAfter = els.slice(firstPre + 1).filter((el) => el.name === 'p' && el.parent === section)
  assert.ok(
    proseAfter.length > 0,
    "the install section carries no sentence of its own after the commands, so P5's move dropped it rather than moved it: the section's own note belongs at its end",
  )
})

test('every page that prints a command prints it under a heading', () => {
  const pages = builtPages()
  assert.ok(pages.length > 0, 'the build produced no page, so this case would check nothing')

  let withCommands = 0
  for (const page of pages) {
    const els = mainInDocumentOrder(page.html)
    const firstPre = els.findIndex((el) => el.name === 'pre')
    if (firstPre === -1) continue
    withCommands++
    const firstH2 = els.findIndex((el) => el.name === 'h2')
    assert.ok(
      firstH2 !== -1 && firstH2 < firstPre,
      `${page.rel} prints a command before its first \`h2\`: a command block belongs to a section, and the heading is what a reader reads above it (P5)`,
    )
  }

  // The rule has to be looking at pages that print commands, or it is a rule about nothing.
  assert.ok(withCommands > 0, 'no built page prints a command, so this case checks nothing')
})
