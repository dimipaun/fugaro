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
//      commands, the terminal and the "every install path" link inside the lead, and the page
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

/** The elements of a built page's `main`, in document order; `null` when it has no `main`. */
function elementsOf(page) {
  const main = find(parse(page.html), 'main')
  return main === null ? null : [...walk(main)]
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
    const els = elementsOf(page)
    if (els === null) continue

    const firstH2 = els.findIndex((el) => el.name === 'h2')
    const firstPre = els.findIndex((el) => el.name === 'pre')

    // Rule 3, and the half of rule 1 that is about the homepage's own command: a command
    // above the page's first heading is a command nothing above it names.
    if (firstPre !== -1 && (firstH2 === -1 || firstH2 > firstPre)) {
      out.push(
        `${page.rel} prints a command before its first \`h2\`: a command block belongs to a section, and the heading is what a reader reads above it (P5)`,
      )
    }

    // Rule 1: the lead region of the homepage is the promise, and nothing else.
    if (page.url === '/') {
      const h1 = els.findIndex((el) => el.name === 'h1')
      if (h1 !== -1 && firstH2 !== -1) {
        const lead = els.slice(h1 + 1, firstH2)
        const shape = lead.map((el) => el.name + (el.attrs.get('class') ? `.${el.attrs.get('class')}` : '')).join(', ')
        if (shape !== 'p.hero-lead') {
          out.push(
            `${page.rel} holds ${lead.length} element(s) between its \`h1\` and its first \`h2\` (${shape}): the promise is the whole of the page's gist above the fold, so the install block, the terminal and the link after them belong to the section the heading opens (P5, D188)`,
          )
        }
        if (lead.length === 1 && textOf(lead[0]).trim() === '') {
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
    // The homepage with its install block above the section heading: the command is in the
    // lead, and above the heading that names it (rules 1 and 3).
    'index.html': page('<h1>t</h1><p class="hero-lead">the promise</p><pre>brew install fugaro</pre><h2>Install</h2>'),
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
  assert.match(of('index.html')[1], /holds 2 element\(s\) between its `h1` and its first `h2` \(p\.hero-lead, pre\)/)

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
