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
//   1. **The homepage's lead is the promise, its prompt line and its call to action, and
//      nothing else.** Between the page's `h1` and its first `h2` there are exactly three
//      elements: the prompt line the owner's feedback of 2026-10-19 puts under the headline
//      (item 1), the lead paragraph and the row of links under it. This is the shape P5 gave
//      the hero, with the one thing the changes spec of 2026-10-18 adds to it (item 4: the
//      promise, its primary call to action, then the picture) and the one thing the feedback
//      of 2026-10-19 adds (the request, in the reader's own words, as the centrepiece of
//      what the page says about driving Fugaro through their agent) — so the promise and the
//      one line it is made of are the whole of the prose a reader meets before the first
//      heading, and so the lead ceiling in `budget.mjs` is a ceiling on the two of them rather
//      than on the commands (D188's lead is the region between the two headings, wherever each
//      of them sits). The first `h2` is the chat's own "Through your agent" — the chat is the
//      first block under the promise (review round 5) and its heading is inside it — which is
//      what lets that block sit directly under the promise without entering the lead region:
//      the block the first `h2` belongs to is taken out of the region whole, so a figure that
//      lost its heading would push the whole of itself into the lead and be refused here. A
//      hero whose install block sat between the promise and that heading would put the
//      commands, the chat and the "every install path" link inside the lead, and the page
//      would say nothing about the promise above them.
//   2. **Getting Started's first section opens with its commands.** Inside `#install`, no
//      paragraph of the section's own precedes the first command block, and the section's
//      own sentence is still there, after the paths. P5 moved that sentence from under the
//      heading to the end of the section for exactly this reason: a reader who came for the
//      command they run met a sentence about the page first, which is the ordering the plan
//      asks this part to fix.
//   3. **Every page that prints a command prints it under a heading.** The page's first `h2`
//      comes before its first `pre`, on every route that prints one — three of the eleven
//      today — so no page opens with a command that nothing above it names. This is the
//      general form of the two cases above and the rule a future page keeps: a command block
//      belongs to a section, and the section's heading is what a reader reads before it.
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file, and
// `foldFailures()` is the whole of the gate: one function over one directory, so the refusals
// are kept the way the other gates keep theirs — with a synthetic `dist` written by the case
// below (`distOf`, the mechanism `budget.mjs` uses; `FUGARO_GATES_DIST` points the other gates
// at a planted copy by hand). A gate whose refusals have never been seen to fire is a gate
// nobody knows the shape of, which is why every rule here has a fixture that breaks it.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, builtPages, find, parse, textOf, walk } from './lib/html.mjs'

/**
 * The elements of a built page's `main`, in document order, with `main` beside them; `null`
 * when the page has no `main`. `main` comes back too because rule 1 needs the page's own
 * top-level blocks: the homepage's first `h2` is the chat's own heading, and the chat's
 * figure — its title, its turns, its caption — sits between the `h1` and that heading
 * without being the lead.
 */
function elementsOf(page) {
  const main = find(parse(page.html), 'main')
  return main === null ? null : { main, els: [...walk(main)] }
}

/** True when `el` is `block` or sits inside it. */
function within(el, block) {
  for (let node = el; node !== null; node = node.parent) if (node === block) return true
  return false
}

/** The direct child of `main` an element belongs to: the top-level block it is part of. */
function blockOf(el, main) {
  let node = el
  while (node.parent !== null && node.parent !== main) node = node.parent
  return node
}

/**
 * Every way a built `dist` breaks the ordering the fold claim rests on, as the sentences a
 * reader of the failure gets: the three rules above, over every page the directory holds.
 *
 * A page that is not there is not a failure here — a missing route is `linkcheck.mjs`'s and
 * `a11y.mjs`'s business, and this gate says nothing about a page it cannot read — and neither
 * is a page whose `main` is missing, for the same reason. What it does say is what the rules
 * above are about, and the message names the page and the rule each time.
 */
export function foldFailures(dist) {
  const out = []

  for (const page of builtPages(dist)) {
    const read = elementsOf(page)
    if (read === null) continue
    const { main, els } = read

    const firstH2 = els.findIndex((el) => el.name === 'h2')
    const firstPre = els.findIndex((el) => el.name === 'pre')

    // Rule 3, and the half of rule 1 that is about the homepage's own command: a command
    // above the page's first heading is a command nothing above it names.
    if (firstPre !== -1 && (firstH2 === -1 || firstH2 > firstPre)) {
      out.push(
        `${page.rel} prints a command before its first \`h2\`: a command block belongs to a section, and the heading is what a reader reads above it (P5)`,
      )
    }

    // Rule 1: the lead region of the homepage is the promise and its call to action, and
    // nothing else.
    if (page.url === '/') {
      const h1 = els.findIndex((el) => el.name === 'h1')
      if (h1 !== -1 && firstH2 !== -1) {
        // The region between the `h1` and the first `h2`, with the elements of the block that
        // `h2` belongs to taken out. On the homepage that heading is the chat's own title, so
        // the figure, its turns, its caption and the watch block under them all sit between
        // the `h1` and it: they are the illustration the page puts directly under the promise
        // (review round 5), and none of them is the lead.
        const owner = blockOf(els[firstH2], main)
        const lead = els.slice(h1 + 1, firstH2).filter((el) => !within(el, owner))
        const shape = lead.map((el) => el.name + (el.attrs.get('class') ? `.${el.attrs.get('class')}` : ''))
        // What the lead region may hold: the promise, the prompt line the owner's feedback
        // of 2026-10-19 puts under the headline — with the `>` mark that draws it as something a
        // reader types, which is a span inside that line rather than a second thing in the
        // region — the row of links under them, and the links in that row. Nothing else. A
        // `figure`, an `ol`, a `pre` or a section's own paragraph is what the refusal below is
        // about, and the first of them is named in it.
        const allowed = ['p.hero-prompt', 'span.hero-prompt-mark', 'p.hero-lead', 'p.hero-actions', 'a.hero-cta', 'a.hero-cta-quiet']
        const foreign = shape.filter((element) => !allowed.includes(element))
        if (foreign.length > 0) {
          out.push(
            `${page.rel} holds ${lead.length} element(s) between its \`h1\` and its first \`h2\` (${shape.join(', ')}), and ${JSON.stringify(foreign[0])} is not the promise, its prompt line or its call to action: the request the page is about, the lead paragraph and the links under them are the whole of the page's gist above the fold, so the diagram, the chat and the install block belong below it (changes spec 2026-10-18, item 4; the owner's feedback of 2026-10-19; P5, D188)`,
          )
        }
        const promise = lead.find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('hero-lead'))
        if (promise !== undefined && textOf(promise).trim() === '') {
          out.push(`${page.rel} opens with an empty lead paragraph: the page's gist above the fold is its tagline and nothing else (P5, RF5)`)
        }
      } else if (h1 !== -1) {
        out.push(`${page.rel} renders no \`h2\` after its \`h1\`, so it has no first section and no lead region to read (P5, D188)`)
      }
    }

    // Rule 2: the install section opens with the commands, and keeps its own sentence.
    if (page.url === '/getting-started/') {
      const section = els.find((el) => el.attrs.get('id') === 'install')
      if (section !== undefined) {
        const inside = [...walk(section)]
        const pre = inside.findIndex((el) => el.name === 'pre')
        if (pre === -1) {
          out.push(`${page.rel}'s #install section prints no command, and a reader who came for the command they run has nowhere to find it (P5)`)
        } else {
          const before = inside.slice(0, pre).filter((el) => el.name === 'p' && el.parent === section)
          if (before.length > 0) {
            out.push(
              `${page.rel}'s #install section opens with a sentence of its own before its first command (${JSON.stringify(textOf(before[0]).trim())}): the section opens with the paths themselves, and what it says about them comes after them (P5)`,
            )
          }
          if (inside.slice(pre + 1).filter((el) => el.name === 'p' && el.parent === section).length === 0) {
            out.push(
              `${page.rel}'s #install section carries no sentence of its own after the commands: the section's own note belongs at its end, and a gate that only refused prose before them would pass a page that dropped it (P5)`,
            )
          }
        }
      }
    }
  }

  return out
}

// ---- the built site ---------------------------------------------------------------------

test("the gist above the fold is ordered on every built page", () => {
  const failures = foldFailures(DIST)
  assert.deepEqual(
    failures,
    [],
    `the built site breaks the ordering P5's fold claim rests on:\n  ${failures.join('\n  ')}`,
  )
})

// ---- the refusals, kept -----------------------------------------------------------------

function distOf(t, files) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-fold-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  for (const [rel, body] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dist, rel)), { recursive: true })
    writeFileSync(path.join(dist, rel), body)
  }
  return dist
}

const page = (body) => `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main>${body}</main></body></html>`

test('each rule refuses the ordering it is about, and passes the page that keeps it', (t) => {
  // The three defects are the three orderings P5 fixed, planted one per rule, beside a page
  // that keeps every one of them: the gate has to report each defect and leave the good page
  // alone, or it is a gate whose refusals have never been seen to fire.
  const dist = distOf(t, {
    // The homepage with its install block above the section heading: the promise and its call
    // to action are the lead, and the command is neither — it is in the lead region and above
    // the heading that names it (rules 1 and 3).
    'index.html': page(
      '<h1>t</h1><p class="hero-lead">the promise</p><p class="hero-actions"><a class="hero-cta" href="/getting-started/">Get started</a></p><pre>brew install fugaro</pre><h2>Install</h2>',
    ),
    // The install section with a sentence about itself under its heading, before the paths
    // (rule 2, the ordering P5 changed). Its own closing sentence is there as well, so the
    // one failure this page earns is the ordering and not the other half of the rule.
    'getting-started/index.html': page(
      '<h1>t</h1><section id="install"><h2>Install</h2><p>a sentence about the section</p><pre>brew install fugaro</pre><p>and the section\'s own sentence</p></section>',
    ),
    // And a page that keeps all three rules: a promise, then the heading, then the command,
    // then the section's own sentence.
    'ok/index.html': page(
      '<h1>t</h1><p>the promise</p><h2>Install</h2><pre>brew install fugaro</pre><p>and the section\'s own sentence</p>',
    ),
  })

  const failures = foldFailures(dist)
  const of = (rel) => failures.filter((line) => line.startsWith(rel))

  assert.equal(of('index.html').length, 2, `the homepage's two broken rules are not both reported:\n${failures.join('\n')}`)
  assert.match(of('index.html')[0], /prints a command before its first `h2`/)
  assert.match(
    of('index.html')[1],
    /holds 4 element\(s\) between its `h1` and its first `h2` \(p\.hero-lead, p\.hero-actions, a\.hero-cta, pre\), and "pre" is not the promise, its prompt line or its call to action/,
  )

  assert.equal(of('getting-started/index.html').length, 1, `the install section's broken ordering is not reported:\n${failures.join('\n')}`)
  assert.match(of('getting-started/index.html')[0], /opens with a sentence of its own before its first command/)

  assert.deepEqual(
    of('ok/index.html'),
    [],
    'a page that keeps every rule was refused, so the gate reports the shape it is meant to pass',
  )
})

test('a page that drops the install section\'s own sentence is refused too', (t) => {
  // The other half of rule 2: the sentence is not only moved, it is still there. A gate that
  // refused prose before the commands and said nothing about the sentence after them would
  // pass a page that deleted it, which is the failure a "the section opens with the commands"
  // check is most likely to have.
  const dist = distOf(t, {
    'getting-started/index.html': page('<h1>t</h1><section id="install"><h2>Install</h2><pre>brew install fugaro</pre></section>'),
  })
  const failures = foldFailures(dist)

  assert.equal(failures.length, 1, failures.join('\n'))
  assert.match(failures[0], /carries no sentence of its own after the commands/)
})

test('the promise, its prompt line and its call to action above the fold are enough, and nothing else is', (t) => {
  // The shape the changes spec of 2026-10-18 asks for (item 4: the promise, its call to
  // action, then the picture) with the one element the owner's feedback of 2026-10-19 adds to it
  // (item 1: the request, under the headline), on its own: the lead region holds the prompt
  // line, the promise and the row of links, the first `h2` opens the block below it — the
  // chat's own title, on the homepage as it now stands — and the gate passes it. The
  // case beside this one plants the same page with a command block added, so the two together
  // are what makes "the prompt line and the call to action are allowed and the command is not"
  // a checked rule rather than a description of today's markup.
  const dist = distOf(t, {
    'index.html': page(
      '<h1>t</h1><p class="hero-prompt"><span class="hero-prompt-mark" aria-hidden="true">&gt;</span> Fan out these three bug fixes.</p><p class="hero-lead">the promise</p><p class="hero-actions"><a class="hero-cta" href="/getting-started/">Get started</a><a class="hero-cta-quiet" href="/how-it-works/">How it works</a></p><div class="home-chat"><figure class="chat"><h2 class="chat-title">Through your agent</h2></figure></div>',
    ),
  })
  assert.deepEqual(foldFailures(dist), [], 'a homepage that keeps the prompt line, the promise and its call to action as its whole lead region was refused')
})

test('a homepage with an empty lead, or with no section after its h1, is refused', (t) => {
  // The two remaining refusals of rule 1, each planted alone: a lead paragraph with no words
  // in it (the page's gist above the fold would be nothing), and a homepage with no `h2`
  // after its `h1` (no first section, so no lead region to read). Both were unexercised
  // before this case: removing either branch from `foldFailures` left every case green.
  const empty = distOf(t, { 'index.html': page('<h1>t</h1><p class="hero-lead"></p><h2>Install</h2><pre>x</pre>') })
  const emptyFailures = foldFailures(empty)
  assert.equal(emptyFailures.length, 1, emptyFailures.join('\n'))
  assert.match(emptyFailures[0], /opens with an empty lead paragraph/)

  const noH2 = distOf(t, { 'index.html': page('<h1>t</h1><p class="hero-lead">the promise</p>') })
  const noH2Failures = foldFailures(noH2)
  assert.equal(noH2Failures.length, 1, noH2Failures.join('\n'))
  assert.match(noH2Failures[0], /renders no `h2` after its `h1`/)
})
