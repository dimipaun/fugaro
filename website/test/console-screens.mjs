// website/test/console-screens.mjs — the console page's screenshot slider (owner feedback
// 2026-10-19).
//
// The owner asked for "a screenshot slider at the top" of the console page, and the
// repository has **no console screenshot in it**: the owner takes them from the released
// console, on their own Mac, and drops the files in. So the component is data-driven and
// the data file ships **empty**, and the three states of that list are what this file is
// about — the empty one, which is the state this checkout is in, and a one-slide and a
// two-slide one, each a build of a copy of the repository with a fixture written into its
// data file:
//
//   1. **An empty list renders no slider and no placeholder.** The built console page
//      carries no element the component would draw, no `<img>` and no new heading: the page
//      a reader gets today is the page it was before the component existed, so a list with
//      nothing in it costs a reader nothing (`website/data/console-screens.yaml`).
//   2. **Two slides render two slides, two dots, and the data file's own text.** Every
//      `<img>`'s `src`, `alt`, `width` and `height` and every caption is the row's, read out
//      of the data file rather than typed into the component (Global Constraint 6), and the
//      `src` is DERIVED from the row's file name rather than written down.
//   3. **One slide renders one slide and no control at all.** A single screenshot is the state
//      the owner's first one lands in, and it is the one branch where the component suppresses
//      the dots: a dot that can only take a reader where they already are is noise, so a
//      one-slide slider carries no dot, no link, no button — nothing whose name says previous
//      or next — and the track is the only thing a reader moves. That is why this state is
//      asserted rather than left to the two-slide case: it is the branch a later change would
//      delete without noticing (the guard is `screens.length > 1`).
//   4. **The slider is usable with JavaScript off.** It is the browser's own scrolling:
//      the track is a focusable, labelled scroll container with `scroll-snap`, each dot is
//      an `<a href="#…">` naming the slide it shows, and the CSS carries no script, no
//      autoplay and no timer. This is the half a hermetic test can hold — that the markup
//      and the stylesheet are what makes the slider work — and it is the half that would
//      rot silently, because a slider that needs a script to move is a slider that does
//      nothing at all on a page the site's own rule keeps script-free.
//   5. **The refusals.** A URL where a file name belongs, a name with no extension at all, a
//      format the slider does not serve, an empty `alt`, an empty caption, a width, a height
//      or a whole-number size that is not one, and a key no page reads — at the document's
//      root and in a row — each stop the build naming the row. Between them they are the two
//      halves of Global Constraint 9: the name-shape rule keeps a URL out of `file`, and the
//      row's own key list keeps a URL out of a `src:` line, because the slide's path is
//      DERIVED from the name and a row may not carry a second copy of it. And the data file
//      cannot render a picture nobody can identify (`alt`, caption) or a box the browser
//      cannot reserve (the two sizes).
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

/** A built page's inlined stylesheets, concatenated: what the page's CSS is, as one string. */
function stylesOf(html) {
  return [...walk(parse(html))].filter((el) => el.name === 'style').map((el) => el.raw ?? '').join('\n')
}

/**
 * Every `animation` declaration a page's stylesheets carry, as the declarations themselves, or
 * []. The property is what is refused, not one spelling of it: `animation-name` with an
 * `animation-duration` beside it is a carousel exactly as much as the `animation:` shorthand
 * is, and the longhands are what a change would most plausibly write. The layout's
 * reduced-motion rule (`Base.astro`) declares the three longhands that take motion AWAY, and
 * a rule that only neutralises an animation is not a rule that adds one, so those three names
 * are stripped before the search.
 */
function animationDeclarations(css) {
  return css.replace(/animation-duration|animation-iteration-count|animation-delay/g, '').match(/animation(-[a-z-]+)?\s*:/g) ?? []
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

/**
 * One row, as the data file writes it, plus whatever lines a case adds to it: the refusal
 * cases below are about one wrong line in an otherwise good row, so the good row is written
 * once and the case appends its own.
 */
function rowLines(row, extra = []) {
  return [
    `  - file: ${row.file}`,
    `    alt: ${JSON.stringify(row.alt)}`,
    `    caption: ${JSON.stringify(row.caption)}`,
    `    width: ${row.width}`,
    `    height: ${row.height}`,
    ...extra,
  ]
}

/** The fixture's rows, written as the data file writes them. */
function fixtureYAML(rows, extra = []) {
  const lines = ['version: 1', 'screens:']
  for (const [index, row] of rows.entries()) {
    lines.push(...rowLines(row, index === rows.length - 1 ? extra : []))
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
// 3. One slide: one slide, and no control at all — the state the owner's first screenshot
//    lands in, and the one branch where the component suppresses the dots.
// ---------------------------------------------------------------------------------------

test('one slide renders one slide, the row\'s own text, and no control at all', (t) => {
  // This is the state the owner's FIRST screenshot lands in, and the branch the component
  // takes when `screens.length > 1` is false — the dots are suppressed, because a dot that can
  // only take a reader where they already are is noise. A later change that rendered the dots
  // for any non-empty list would delete that branch silently, which is what this case is for.
  const built = buildWithScreens(t, fixtureYAML([FIXTURE[0]]))
  const page = consolePage(built)
  const slider = sliderOf(page)
  assert.ok(slider, `the console page renders no element marked data-slot="${SLOT}" with one slide in ${SCREENS_YAML}`)

  const inside = [...walk(slider)]

  // One slide, with the row's own text on it: the state is not a degenerate two-slide render,
  // it is the row.
  const images = inside.filter((el) => el.name === 'img')
  assert.equal(images.length, 1, `the slider renders ${images.length} <img> for one row in ${SCREENS_YAML}`)
  assert.equal(
    images[0].attrs.get('src'),
    `/${SCREENS_DIR}/${FIXTURE[0].file}`,
    'the one slide\'s src is not the row\'s own file under /' + SCREENS_DIR + '/',
  )
  assert.equal(images[0].attrs.get('alt'), FIXTURE[0].alt, 'the one slide does not carry the row\'s alt text')

  const slides = inside.filter((el) => el.name === 'li' && hasClass(el, 'screen'))
  assert.equal(slides.length, 1, `the track carries ${slides.length} slide(s) for one row`)

  const captions = inside.filter((el) => el.name === 'figcaption')
  assert.equal(captions.length, 1, `the slider renders ${captions.length} caption(s) for one slide`)
  assert.equal(textOf(captions[0]).trim(), FIXTURE[0].caption, 'the one slide\'s caption is not the row\'s caption')
  assert.ok(
    [...walk(slides[0])].includes(captions[0]),
    'the one slide\'s caption is not inside its own slide, so it reads as a caption about nothing',
  )

  // **And no control at all.** With one slide the dots are suppressed, and the point of that
  // is not only "one dot fewer": a one-slide slider has nowhere to go, so it carries no dot,
  // no link and no button — and nothing whose accessible name says previous or next, whether
  // the component draws it as a link, a button or a labelled element. A control added here
  // would be a control a reader can press that does nothing (the same rule the component's
  // comment states), and a `next` that is really there is the defect this half is for.
  assert.ok(
    !inside.some((el) => hasClass(el, 'screens-dots') || (el.name === 'a' && hasClass(el, 'screen-dot'))),
    'the one-slide slider renders dots, and a dot that can only take a reader where they already are is noise',
  )
  const controls = inside.filter(
    (el) => el.name === 'a' || el.name === 'button' || /previous|next/i.test(el.attrs.get('aria-label') ?? ''),
  )
  assert.deepEqual(
    controls.map((el) => el.name + (el.attrs.get('class') ? `.${el.attrs.get('class')}` : '')),
    [],
    'the one-slide slider renders a control (a link, a button, or something named previous or next) with one slide to show and nowhere to go',
  )
})

// ---------------------------------------------------------------------------------------
// 4. The slider works with no script: the browser's own scrolling and its own anchors.
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
  const css = stylesOf(consolePage(built))
  assert.match(css, /scroll-snap-type\s*:\s*x\s+mandatory/, 'the slider\'s stylesheet declares no scroll-snap-type, so the track stops wherever a swipe left it')
  assert.match(css, /scroll-snap-align\s*:/, 'the slider\'s stylesheet gives its slides no scroll-snap-align, so a snap has nothing to land on')
  // And no autoplay: nothing in the component's own CSS animates on its own. `animation` is
  // the only property family that could, and the reduce rule in the layout neutralises it —
  // but a slider that had one at all would be a carousel, which the owner did not ask for.
  // Every spelling of the property counts, which is what `animationDeclarations` is for: the
  // case below builds the component with a longhand declaration and asserts it is refused.
  const animations = animationDeclarations(css)
  assert.deepEqual(
    animations,
    [],
    `the slider's stylesheet declares ${animations.join(', ')} — an animation, and the slider does not move on its own: it is a scroll container with dots, not a carousel`,
  )
})

// The gate above is about the property, not about one spelling of it. A slider that animated
// itself through the longhands — `animation-name` with an `animation-duration` beside it —
// would be a carousel just as much as one written with the shorthand, and the longhands are
// the spelling a later change would most plausibly reach for, because the shorthand is what
// the gate names. So the fixture build is repeated with one longhand written into the
// component's own stylesheet, and the gate has to refuse it.
test('the autoplay gate reads every spelling of the property, not just the shorthand', (t) => {
  const component = 'src/components/ScreenshotSlider.astro'
  const source = readFileSync(path.join(SITE, component), 'utf8')
  const mutated = source.replace('.screens-track {', '.screens-track {\n    animation-name: slide-in;')
  assert.notEqual(
    mutated,
    source,
    `${component} no longer carries a .screens-track rule, so this case would build an unmutated copy and assert nothing`,
  )
  const built = buildCopy(t, ({ site }) => {
    writeFileSync(path.join(site, 'data', 'console-screens.yaml'), fixtureYAML(FIXTURE))
    writeFileSync(path.join(site, component), mutated)
  })
  const animations = animationDeclarations(stylesOf(consolePage(built)))
  assert.ok(
    animations.includes('animation-name:'),
    `the built page declares no animation-name after ${component} was built with one: the gate in the case above reads the shorthand only, so a slider that animated itself through the longhands would pass it (read: ${animations.join(', ') || 'nothing'})`,
  )
  // And the layout's own reduced-motion rule is not what it read: the build above carries
  // it, and the only declaration the gate found is the one this case wrote.
  assert.deepEqual(animations, ['animation-name:'], `the page's animation declarations are ${animations.join(', ')}`)
})

// ---------------------------------------------------------------------------------------
// 5. The refusals: the data file cannot carry a URL, a foreign format, an empty text, a size
//    that is not a whole number of pixels, or a key no page reads — at the document's root or
//    in a row.
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
      // A name that is exactly a served format: there is no dot in it, so there is no
      // extension either — `lastIndexOf('.')` is -1 and the whole name would be read as one.
      // The row would render `<img src="/images/console/png">` while the refusal's own message
      // says a name with no extension is not a picture, which is the defect this case is for.
      what: 'a served format as the whole name, with no extension at all',
      yaml: fixtureYAML([{ ...FIXTURE[0], file: 'png' }]),
      wants: [/console-screens\.yaml/, /file/, /extension/],
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
      what: 'a width that is zero',
      yaml: fixtureYAML([{ ...FIXTURE[0], width: 0 }]),
      wants: [/console-screens\.yaml/, /width/],
    },
    {
      what: 'a height that is zero, which is the other half of the box the browser reserves',
      yaml: fixtureYAML([{ ...FIXTURE[0], height: 0 }]),
      wants: [/console-screens\.yaml/, /height/],
    },
    {
      what: 'a size that is not a whole number of pixels',
      yaml: fixtureYAML([{ ...FIXTURE[0], width: 1440.5 }]),
      wants: [/console-screens\.yaml/, /width/],
    },
    {
      what: 'a key nobody reads, at the document root',
      yaml: `${fixtureYAML([FIXTURE[0]])}slidelabel: nope\n`,
      wants: [/console-screens\.yaml/, /slidelabel/],
    },
    {
      // The row's own allow-list, which is the row-level half of Global Constraint 9: the
      // slide's `src` is DERIVED from the file name by the loader, so a row carrying one is a
      // second copy of a URL — and this is the case that makes the row's key list the thing
      // that refuses it, rather than the name-shape check happening to catch a URL.
      what: 'a row that carries a slide\'s src, which the build derives from the file name',
      yaml: fixtureYAML([FIXTURE[0]], ['    src: /images/console/runs-list.png']),
      wants: [/console-screens\.yaml/, /screens\[0\]/, /src/],
    },
    {
      what: 'a row that carries a key nobody reads',
      yaml: fixtureYAML([FIXTURE[0]], ['    slidelabel: nope']),
      wants: [/console-screens\.yaml/, /screens\[0\]/, /slidelabel/],
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
