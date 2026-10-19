// website/test/console-screens.mjs — the console page's screenshot slider (owner feedback
// 2026-10-19).
//
// The owner asked for "a screenshot slider at the top" of the console page, and the
// repository has **no console screenshot in it**: the owner takes them from the released
// console, on their own Mac, and drops the files in. So the component is data-driven and
// the data file ships **empty**, and the two states of that list are what this file is
// about — the empty one, which is the state this checkout is in, and a two-slide one, which
// is a build of a copy of the repository with a fixture written into its data file:
//
//   1. **An empty list renders no slider and no placeholder.** The built console page
//      carries no element the component would draw, no `<img>` and no new heading: the page
//      a reader gets today is the page it was before the component existed, so a list with
//      nothing in it costs a reader nothing (`website/data/console-screens.yaml`).
//   2. **Two slides render two slides, two dots, and the data file's own text.** Every
//      `<img>`'s `src`, `alt`, `width` and `height` and every caption is the row's, read out
//      of the data file rather than typed into the component (Global Constraint 6), and the
//      `src` is DERIVED from the row's file name rather than written down.
//   3. **The slider is usable with JavaScript off.** It is the browser's own scrolling:
//      the track is a focusable, labelled scroll container with `scroll-snap`, each dot is
//      an `<a href="#…">` naming the slide it shows, and the CSS carries no script, no
//      autoplay and no timer. This is the half a hermetic test can hold — that the markup
//      and the stylesheet are what makes the slider work — and it is the half that would
//      rot silently, because a slider that needs a script to move is a slider that does
//      nothing at all on a page the site's own rule keeps script-free.
//   4. **The refusals.** A URL where a file name belongs, a format the slider does not
//      serve, an empty `alt`, an empty caption and a non-positive size each stop the build
//      naming the row — so the data file cannot carry a second copy of a URL (Global
//      Constraint 9) and cannot render a picture nobody can identify.
//
// **The fixture's text is this file's own, and that is the console page's tight spot.** The
// page stands at 239 of the 250 words D188 allows it, and the page ceiling counts a row's
// `alt` as well as its `caption` (`test/budget.mjs`'s `visibleText`), so two rows with a
// sentence each are already more words than the page has room for: a build carrying this
// fixture would be refused by the word gate, and that is why the fixture build is asserted
// about here rather than held to that gate. It is a real tension and the owner's to settle —
// website/README.md, "Adding a console screenshot", states the numbers, and the first
// screenshot that needs more words needs the page ceiling raised in the commit that adds it.
//
// How it builds: the fixture and the refusal cases mutate a file and build again, so they
// build a copy of the repository in a temp directory rather than this checkout — the shape
// `console.mjs` and `layout.mjs` use, and `test/lib/build.mjs`'s `buildCopy` is that
// builder. Every build is network-free: the only executable started is the site's own
// installed Astro.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { buildCopy } from './lib/build.mjs'
import { SITE, parse, textOf, walk } from './lib/html.mjs'

/** The data file the slider renders, as every message below names it. */
const SCREENS_YAML = 'website/data/console-screens.yaml'
/** The directory a screenshot's file goes in, under `website/public/`. */
const SCREENS_DIR = 'images/console'
/** The hook the component marks its section with: what "there is no slider" is about. */
const SLOT = 'console-screens'

/** The data file as it is written in this repository. */
function screensFile() {
  return readFileSync(path.join(SITE, 'data', 'console-screens.yaml'), 'utf8')
}

/**
 * The slider's own element of a built page, or null: the section the component marks with
 * `data-slot`, and nothing else on the page is one. Read by the strict reader rather than
 * by a regex, so "no slider" is a statement about the document rather than about a string.
 */
function sliderOf(html) {
  const found = [...walk(parse(html))].filter((el) => el.attrs.get('data-slot') === SLOT)
  assert.ok(found.length <= 1, `the built page carries ${found.length} elements marked data-slot="${SLOT}"; a page has one slider`)
  return found[0] ?? null
}

/**
 * Whether an element carries a class, by token: `class="screen screen-wide"` carries
 * `screen` and not `screen-wide`. Matched by substring instead, `screen` would also match
 * the dots' own `screen-dot-item`, and the slides would be counted twice — which is what
 * this helper is here to stop the cases below doing.
 */
function hasClass(el, name) {
  return (el.attrs.get('class') ?? '').split(/\s+/).includes(name)
}

/** The built page's elements, once: what the cases below read instead of the page string. */
function elementsOf(html) {
  return [...walk(parse(html))]
}

/** The built console page of a build that is expected to have succeeded. */
function consolePage(built) {
  assert.equal(built.status, 0, `the build failed:\n${built.output}`)
  const page = path.join(built.dist, 'console', 'index.html')
  return readFileSync(page, 'utf8')
}

/** Two slides, as a fixture: the rows the component renders and the test reads back. */
const FIXTURE = [
  {
    file: 'runs-list.png',
    alt: 'A list of runs with their stage, age and spend.',
    caption: 'Every run, with what it is doing and what it has cost.',
    width: 1440,
    height: 900,
  },
  {
    file: 'run-detail.png',
    alt: 'One run, with the agent output beside it.',
    caption: 'One run in detail.',
    width: 1440,
    height: 900,
  },
]

/** The fixture's rows, written as the data file writes them. */
function fixtureYAML(rows) {
  const lines = ['version: 1', 'screens:']
  for (const row of rows) {
    lines.push(`  - file: ${row.file}`)
    lines.push(`    alt: ${JSON.stringify(row.alt)}`)
    lines.push(`    caption: ${JSON.stringify(row.caption)}`)
    lines.push(`    width: ${row.width}`)
    lines.push(`    height: ${row.height}`)
  }
  return `${lines.join('\n')}\n`
}

/** Build a copy with the data file replaced, and hand back what it produced. */
function buildWithScreens(t, contents) {
  return buildCopy(t, ({ site }) => {
    writeFileSync(path.join(site, 'data', 'console-screens.yaml'), contents)
  })
}

// ---------------------------------------------------------------------------------------
// 1. The empty list: the state this checkout ships, and no slider at all.
// ---------------------------------------------------------------------------------------

test('an empty list renders the console page with no slider and no placeholder', (t) => {
  // The premise: this checkout's own data file carries no slide, so the build below is the
  // state the owner will see until they add one. A fixture that quietly grew a row would
  // make this case assert nothing about the state it names.
  const data = screensFile()
  assert.match(
    data,
    /^screens:\s*\[\s*\]\s*$/m,
    `${SCREENS_YAML} does not carry an empty \`screens: []\`, and this case is the empty state the slider has to render as no slider at all`,
  )

  const built = buildCopy(t, () => {})
  const page = consolePage(built)

  assert.equal(
    sliderOf(page),
    null,
    `the console page carries a slider with no screenshot to show: an empty list is rendered as nothing, and a section with a heading and no picture in it is the placeholder the owner did not ask for (${SCREENS_YAML})`,
  )
  // Read as a document and not as a string: the component's stylesheet is inlined into the
  // page whether or not the section rendered, so the class names are in the page's
  // `<style>` either way. The elements are what says whether the slider is there.
  const elements = elementsOf(page)
  for (const name of ['screens-track', 'screens-dots']) {
    assert.ok(
      !elements.some((el) => hasClass(el, name)),
      `the built page carries an element of class "${name}" with an empty list: the slider renders no markup at all, and a track or a row of dots with nothing in it is the placeholder the owner did not ask for`,
    )
  }
  // And no picture anywhere in the page's own content. Scoped to the `<article>` because the
  // layout's header carries the site's logo on every page: "the console page draws no
  // placeholder" is a statement about the console's content, not about the chrome around it.
  const article = elements.find((el) => el.name === 'article')
  assert.ok(article, 'the console page carries no <article>, so there is nothing to say "no picture" about')
  assert.ok(
    ![...walk(article)].some((el) => el.name === 'img'),
    'the console page renders an <img> while its data file carries no screenshot: the slider invents no picture and draws no placeholder (D190)',
  )
  // And the page is otherwise the page it was: the sections a reader met before the slider
  // existed are all still there, so "no slider" is not "no page".
  for (const id of ['downloads', 'console-facts']) {
    assert.ok(page.includes(`id="${id}"`), `the console page lost its #${id} section while the slider rendered nothing`)
  }
})

// ---------------------------------------------------------------------------------------
// 2. Two slides: the slider, its dots, and the data file's own text.
// ---------------------------------------------------------------------------------------

test('two slides render two slides, two dots and the data file\'s own text', (t) => {
  const built = buildWithScreens(t, fixtureYAML(FIXTURE))
  const page = consolePage(built)
  const slider = sliderOf(page)
  assert.ok(slider, `the console page renders no element marked data-slot="${SLOT}" with two slides in ${SCREENS_YAML}`)

  const images = [...walk(slider)].filter((el) => el.name === 'img')
  assert.equal(images.length, FIXTURE.length, `the slider renders ${images.length} <img> for ${FIXTURE.length} slide(s)`)

  for (const [index, row] of FIXTURE.entries()) {
    const img = images[index]
    // The file is the row's NAME under `public/images/console/`, and the site path is
    // derived from it here rather than written in the data file: a `file` that were a URL
    // would be a second copy of a fact the build composes (Global Constraint 9).
    assert.equal(
      img.attrs.get('src'),
      `/${SCREENS_DIR}/${row.file}`,
      `the ${index + 1}th slide's src is ${JSON.stringify(img.attrs.get('src'))}, want the row's own file under /${SCREENS_DIR}/`,
    )
    assert.equal(img.attrs.get('alt'), row.alt, `the ${index + 1}th slide's alt is not the row's alt text`)
    assert.equal(img.attrs.get('width'), String(row.width), `the ${index + 1}th slide does not carry the row's width`)
    assert.equal(img.attrs.get('height'), String(row.height), `the ${index + 1}th slide does not carry the row's height`)
  }

  // The captions, in order, and each one inside the slide it belongs to: a caption read
  // apart from its picture is a caption about nothing.
  const captions = [...walk(slider)].filter((el) => el.name === 'figcaption')
  assert.equal(captions.length, FIXTURE.length, `the slider renders ${captions.length} caption(s) for ${FIXTURE.length} slide(s)`)
  for (const [index, row] of FIXTURE.entries()) {
    assert.equal(
      textOf(captions[index]).trim(),
      row.caption,
      `the ${index + 1}th slide's caption is not the row's caption`,
    )
    // And the caption is inside the same list item as its picture: a caption read apart
    // from the slide it belongs to is a caption about the picture before it.
    const slides = [...walk(slider)].filter((el) => el.name === 'li' && hasClass(el, 'screen'))
    assert.ok(
      slides[index] && [...walk(slides[index])].includes(captions[index]),
      `the ${index + 1}th caption is not inside the ${index + 1}th slide`,
    )
  }
})

test('every slide is one list item of the slider\'s track, and the two orders agree', (t) => {
  // The dots and the slides are one order read twice: dot n names slide n. A build whose
  // dots were rendered from a second list — or sorted — would send a reader to the wrong
  // picture, which is the defect this case is written for.
  const built = buildWithScreens(t, fixtureYAML(FIXTURE))
  const slider = sliderOf(consolePage(built))
  assert.ok(slider, 'the slider rendered nothing with two slides in the data file')

  const slides = [...walk(slider)].filter((el) => el.name === 'li' && hasClass(el, 'screen'))
  assert.equal(slides.length, FIXTURE.length, `the track carries ${slides.length} slide(s) for ${FIXTURE.length} row(s)`)

  const dots = [...walk(slider)].filter((el) => el.name === 'a' && hasClass(el, 'screen-dot'))
  assert.equal(dots.length, FIXTURE.length, `the slider carries ${dots.length} dot(s) for ${FIXTURE.length} slide(s)`)

  for (const [index, slide] of slides.entries()) {
    const id = slide.attrs.get('id')
    assert.ok(id, `the ${index + 1}th slide carries no id, so no dot and no anchor can reach it`)
    assert.equal(
      dots[index].attrs.get('href'),
      `#${id}`,
      `the ${index + 1}th dot points at ${JSON.stringify(dots[index].attrs.get('href'))} and the ${index + 1}th slide is #${id}: the dots are the slides' own order, and a dot that points elsewhere sends a reader to the wrong picture`,
    )
    // A dot is a control, so it has a name a screen reader announces: it carries no text of
    // its own (it is drawn as a dot), and an unnamed link is announced as "link".
    assert.ok(
      (dots[index].attrs.get('aria-label') ?? '').trim() !== '',
      `the ${index + 1}th dot has no accessible name, and it carries no text: a screen reader announces it as a link and nothing else`,
    )
  }

  // And every id a dot names is in the document once: a duplicated id would make the
  // anchor land on whichever the browser found first (a11y.mjs refuses it too, and this is
  // the case that names the slider when it happens).
  const ids = slides.map((slide) => slide.attrs.get('id'))
  assert.equal(new Set(ids).size, ids.length, `the slider's slides share an id (${ids.join(', ')})`)
})

// ---------------------------------------------------------------------------------------
// 3. The slider works with no script: the browser's own scrolling and its own anchors.
// ---------------------------------------------------------------------------------------

test('the slider is the browser\'s own scrolling, with no script and no timer', (t) => {
  const built = buildWithScreens(t, fixtureYAML(FIXTURE))
  const slider = sliderOf(consolePage(built))
  assert.ok(slider, 'the slider rendered nothing with two slides in the data file')

  // No script, inline or linked, inside the slider: the site ships one first-party file
  // (D183) and the slider needs none of it. A slider that shipped its own script would be a
  // second file, a CSP decision and a page that needs JavaScript to be read — the three
  // things the site's own rules refuse.
  assert.deepEqual(
    [...walk(slider)].filter((el) => el.name === 'script').map((el) => el.attrs.get('src') ?? '(inline)'),
    [],
    'the slider ships a script, and the slider works with JavaScript off: the track scrolls, the dots are anchors, and nothing here needs code',
  )

  // The track: one focusable, named scroll container, so the keyboard can reach the slides
  // that are off the right-hand edge (`tabindex="0"` on a scroll container is what makes it
  // scrollable by arrow keys at all).
  const track = [...walk(slider)].find((el) => hasClass(el, 'screens-track'))
  assert.ok(track, 'the slider carries no track element')
  assert.equal(track.attrs.get('tabindex'), '0', 'the slider\'s track is not focusable, so a keyboard cannot scroll it')
  assert.ok(
    (track.attrs.get('aria-label') ?? '').trim() !== '',
    'the slider\'s track has no accessible name: a focusable scroll container is announced as an unnamed group',
  )

  // The stylesheet: `scroll-snap` on the track and a snap alignment on the slides, which is
  // what makes the browser land on a whole screenshot rather than halfway between two. The
  // built page inlines the component's stylesheet, so this is read out of the page.
  const css = [...walk(parse(consolePage(built)))].filter((el) => el.name === 'style').map((el) => el.raw ?? '').join('\n')
  assert.match(css, /scroll-snap-type\s*:\s*x\s+mandatory/, 'the slider\'s stylesheet declares no scroll-snap-type, so the track stops wherever a swipe left it')
  assert.match(css, /scroll-snap-align\s*:/, 'the slider\'s stylesheet gives its slides no scroll-snap-align, so a snap has nothing to land on')
  // And no autoplay: nothing in the component's own CSS animates on its own. `animation`
  // is the only property that could, and the reduce rule in the layout neutralises it —
  // but a slider that had one at all would be a carousel, which the owner did not ask for.
  assert.ok(
    !/animation\s*:/.test(css.replace(/animation-duration|animation-iteration-count|animation-delay/g, '')),
    'the slider\'s stylesheet declares an animation, and the slider does not move on its own: it is a scroll container with dots, not a carousel',
  )
})

// ---------------------------------------------------------------------------------------
// 4. The refusals: the data file cannot carry a URL, a foreign format or an empty text.
// ---------------------------------------------------------------------------------------

test('the build refuses a data file that would render a picture nobody can reach or read', async (t) => {
  const cases = [
    {
      what: 'a URL where a file name belongs',
      yaml: fixtureYAML([{ ...FIXTURE[0], file: 'https://example.invalid/shot.png' }]),
      wants: [/console-screens\.yaml/, /file/, /name/],
    },
    {
      what: 'a format the slider does not serve',
      yaml: fixtureYAML([{ ...FIXTURE[0], file: 'shot.svg' }]),
      wants: [/console-screens\.yaml/, /svg/],
    },
    {
      what: 'an empty alt',
      yaml: fixtureYAML([{ ...FIXTURE[0], alt: '' }]),
      wants: [/console-screens\.yaml/, /alt/],
    },
    {
      what: 'an empty caption',
      yaml: fixtureYAML([{ ...FIXTURE[0], caption: '' }]),
      wants: [/console-screens\.yaml/, /caption/],
    },
    {
      what: 'a size that is not a positive whole number',
      yaml: fixtureYAML([{ ...FIXTURE[0], width: 0 }]),
      wants: [/console-screens\.yaml/, /width/],
    },
    {
      what: 'a key nobody reads',
      yaml: `${fixtureYAML([FIXTURE[0]])}slidelabel: nope\n`,
      wants: [/console-screens\.yaml/, /slidelabel/],
    },
  ]

  for (const { what, yaml, wants } of cases) {
    const built = buildWithScreens(t, yaml)
    assert.notEqual(built.status, 0, `the build accepted ${what} in ${SCREENS_YAML}:\n${built.output}`)
    for (const want of wants) {
      assert.match(built.output, want, `the failure for ${what} does not name what was refused:\n${built.output}`)
    }
  }
})
