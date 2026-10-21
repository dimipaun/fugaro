// website/test/console-screens.mjs — the console page's screenshot slider (owner feedback
// 2026-10-19; the four captures of 2026-10-21).
//
// The owner asked for "a screenshot slider at the top" of the console page (2026-10-19) and
// then, reading the preview, said the page's screenshots were missing (2026-10-21:
// "console page: screenshots missing"). The four pictures are in the tree now —
// `website/public/screenshots/console-{live,run,spend,installations}.webp`, 1920 x 1200 each,
// the fixture captures `MANIFEST.json` describes — and the rows in
// `website/data/console-screens.yaml` are what the page renders them from. This file is about
// the slider and the rows it draws, in the four states a build can be in:
//
//   1. **The four rows are the slides the page renders.** Every `<img>`'s `src`, `alt`,
//      `width` and `height` is the row's, read out of the data file rather than typed into the
//      component (Global Constraint 6), the `src` is DERIVED from the row's file name rather
//      than written down, and each caption is the row's line under its own picture. The
//      `alt` texts are held to `public/screenshots/MANIFEST.json` **verbatim** — the manifest
//      is what the capture wrote beside the picture, and it is the only place the four alt
//      texts are recorded outside the data file — and the sizes are held to each WebP's own
//      header, read here, so a row that declared a box the file is not is refused.
//   2. **The pictures' loading is deliberate**: the first slide, the one the frame shows, is
//      fetched with the page (`loading="eager"`), the three a reader scrolls or presses a dot
//      to reach are fetched when they come near the viewport (`loading="lazy"`), and every
//      picture declares `decoding="async"`. A slider that fetched all four up front, or that
//      let a 1920-wide WebP's decode hold the frame, is a page whose first screen waits for
//      pictures nobody is looking at yet.
//   3. **One slide renders one slide and no control at all.** A single screenshot is a legal
//      data file, and it is the one branch where the component suppresses the dots: a dot that
//      can only take a reader where they already are is noise, so a one-slide slider carries
//      no dot, no link, no button — nothing whose name says previous or next — and the track
//      is the only thing a reader moves. That is why this state is asserted rather than left
//      to the four-slide case: it is the branch a later change would delete without noticing
//      (the guard is `screens.length > 1`), and it is also what a data file with no row in it
//      renders — no section, no frame, no picture-shaped hole.
//   4. **The slider is usable with JavaScript off.** It is the browser's own scrolling:
//      the track is a focusable, labelled scroll container with `scroll-snap`, each dot is
//      an `<a href="#…">` naming the slide it shows — focusable, operable with Enter, named for
//      a screen reader — and the CSS carries no script, no autoplay and no timer; the bar the
//      script drives is in the markup `hidden` and only while there is more than one slide, so a
//      reader with no script sees four screenshots and four dots rather than two dead buttons.
//      This is the half a hermetic test can hold — that the markup and the stylesheet are what
//      makes the slider work — and it is the half that would rot silently, because a slider that
//      needs a script to move is a slider that does nothing at all on a page the site's own rule
//      keeps script-free. The no-script fallback is the page itself: the pictures are `<img>`
//      elements in the document and the first of them is the one the frame opens on.
//   5. **The script's half, run rather than read** (parts 5a and 5b below). The owner's
//      feedback of 2026-10-21 asked for the count, for controls that click and for an autoplay,
//      and those are the three things no stylesheet can do: `public/scripts/site.js` reveals the
//      bar, writes `n / N` and the current dot from the track's own scroll position, moves the
//      track on an arrow or a dot press, and advances a slide every six seconds while nobody is
//      reading — stopping for a pointer over the slider, a focus inside it, a tab in the
//      background and a visitor who asked for less motion. `lib/dom.mjs` is the stub browser both
//      these cases and `copy.mjs` run the file in, and **each case below is proved by planting
//      the defect it names** (5b), because a reading of a script that never disagrees with the
//      script is a reading of nothing.
//   6. **The refusals.** A URL where a file name belongs, a name with no extension at all, a
//      format the slider does not serve, an empty `alt`, an empty caption, a width, a height
//      or a whole-number size that is not one, and a key no page reads — at the document's
//      root and in a row — each stop the build naming the row. Between them they are the two
//      halves of Global Constraint 9: the name-shape rule keeps a URL out of `file`, and the
//      row's own key list keeps a URL out of a `src:` line, because the slide's path is
//      DERIVED from the name and a row may not carry a second copy of it. And the data file
//      cannot render a picture nobody can identify (`alt`, caption) or a box the browser
//      cannot reserve (the two sizes).
//
// **The rows are read here, literally.** The slider's facts are compared against the data file
// as this gate reads it — one line at a time, the way `test/caps-and-costs.mjs` reads
// `typical-costs.yaml` — rather than against a second copy of the four pictures' text typed
// into this file, which would be the copy that drifts first.
//
// **The fixture's text is this file's own.** A fixture build writes rows into a copy of the
// repository rather than into this checkout, so the cases below measure the shipped page
// against the data file that ships and a two-row build against a fixture; both go through the
// same reading, which is what makes the reading one rule rather than two.
//
// How it builds: `test/lib/build.mjs`'s `buildCopy` stages a copy of the repository in a temp
// directory and builds it — the shape `console.mjs` and `layout.mjs` use. Every build is
// network-free: the only executable started is the site's own installed Astro.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { buildCopy } from './lib/build.mjs'
import { fakeDOM, SCRIPT_SOURCE } from './lib/dom.mjs'
import { SITE, parse, textOf, walk } from './lib/html.mjs'

/** The data file the slider renders, as every message below names it. */
const SCREENS_YAML = 'website/data/console-screens.yaml'
/** The directory a screenshot's file goes in, under `website/public/`. */
const SCREENS_DIR = 'screenshots'
/** The four captures' own record, written beside them by the run that took them. */
const MANIFEST = 'website/public/screenshots/MANIFEST.json'
/** The hook the component marks its section with: what "there is no slider" is about. */
const SLOT = 'console-screens'

/** The repository root: the paths below are the ones a message names, from `website/` up. */
const REPO = path.resolve(SITE, '..')

/** A file of this checkout, as text, by its path from the repository root. */
function repoFile(rel) {
  return readFileSync(path.join(REPO, rel), 'utf8')
}

/** The data file as it is written in this repository. */
function screensFile() {
  return repoFile(SCREENS_YAML)
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

/** The built console page of a build that is expected to have succeeded. */
function consolePage(built) {
  assert.equal(built.status, 0, `the build failed:\n${built.output}`)
  const page = path.join(built.dist, 'console', 'index.html')
  return readFileSync(page, 'utf8')
}

// ---------------------------------------------------------------------------------------
// The data file and the pictures, read here.
// ---------------------------------------------------------------------------------------

/** A YAML scalar as the site's own reader reads it: quoted or plain, and no escapes beyond
 * the two its reader knows (`\"` and `\\`), which is the subset these rows are written in. */
function unquote(raw) {
  const text = raw.trim()
  if (text.startsWith('"') && text.endsWith('"')) return text.slice(1, -1).replace(/\\(["\\])/g, '$1')
  return text
}

/**
 * `website/data/console-screens.yaml`, read literally: one row per `- file:` line, with the
 * four keys under it. Deliberately this gate's own reader rather than the build's — the loader
 * is what puts these rows on the page, so a gate that read them through it would be asking the
 * page whether it agrees with itself — and a key this reader does not know is a key the cases
 * below stop seeing.
 */
function readScreens() {
  const out = []
  let row = null
  for (const line of screensFile().split('\n')) {
    if (line.trim() === '' || line.trim().startsWith('#')) continue
    let m
    if ((m = /^ {2}- file: (\S+)$/.exec(line))) out.push((row = { file: m[1] }))
    else if (row && (m = /^ {4}(alt|caption|height|width): (.*)$/.exec(line))) {
      row[m[1]] = m[1] === 'width' || m[1] === 'height' ? Number(m[2].trim()) : unquote(m[2])
    }
  }
  return out
}

/**
 * A WebP's own pixel size, read out of the file's header: the RIFF container's first chunk is
 * `VP8X` (extended), `VP8 ` (lossy) or `VP8L` (lossless), and each carries the two numbers at a
 * different offset. Nothing here decodes the picture — it is the numbers the browser reads to
 * reserve the box before the file arrives, and the numbers a row has to carry.
 */
function webpSize(name) {
  const bytes = readFileSync(path.join(SITE, 'public', SCREENS_DIR, name))
  assert.equal(bytes.slice(0, 4).toString('latin1'), 'RIFF', `${name} is not a RIFF file, so it is not the WebP this reader can measure`)
  assert.equal(bytes.slice(8, 12).toString('latin1'), 'WEBP', `${name} is a RIFF file that is not a WebP`)
  const chunk = bytes.slice(12, 16).toString('latin1')
  if (chunk === 'VP8X') {
    return { width: (bytes[24] | (bytes[25] << 8) | (bytes[26] << 16)) + 1, height: (bytes[27] | (bytes[28] << 8) | (bytes[29] << 16)) + 1 }
  }
  if (chunk === 'VP8 ') {
    return { width: bytes.readUInt16LE(26) & 0x3fff, height: bytes.readUInt16LE(28) & 0x3fff }
  }
  if (chunk === 'VP8L') {
    const bits = bytes.readUInt32LE(21)
    return { width: (bits & 0x3fff) + 1, height: ((bits >> 14) & 0x3fff) + 1 }
  }
  assert.fail(`${name}'s first chunk is ${JSON.stringify(chunk)}, and this reader measures VP8X, VP8 and VP8L`)
}

/** The four captures' record, as the run that took them wrote it. */
function readManifest() {
  return JSON.parse(repoFile(MANIFEST))
}

/**
 * Every way the rows depart from the pictures they name and the record written beside them:
 *
 *   - a file that is not there, or whose name is not one of the formats the slider serves;
 *   - a declared box that is not the file's own — the two numbers the browser reserves the
 *     slide with, so a row that counted the capture's pixels at twice the density, or that
 *     swapped the pair, would reserve the wrong shape and scale the picture into it;
 *   - and an `alt` that is not the manifest's own text for that capture, verbatim: the
 *     manifest is the account the capture wrote, and a data file that paraphrased it is a
 *     second account of a screen nobody can compare with the picture.
 */
function pictureFailures(rows, manifest) {
  const out = []
  const captures = new Map(manifest.screens.map((screen) => [screen.file.replace(/\.[a-z0-9]+$/i, ''), screen]))
  for (const [index, row] of rows.entries()) {
    const what = `${SCREENS_YAML} rows[${index}] (${row.file})`
    const stem = String(row.file).replace(/\.[a-z0-9]+$/i, '')
    const capture = captures.get(stem)
    if (!capture) {
      out.push(`${what} names a picture ${MANIFEST} does not record: every slide's alt text is the capture's own, and a row the manifest does not carry has no account of its screen`)
      continue
    }
    const size = webpSize(row.file)
    if (row.width !== size.width || row.height !== size.height) {
      out.push(`${what} declares ${row.width} x ${row.height} and the file is ${size.width} x ${size.height}: the two numbers are the picture's own pixels, which is what reserves the slide's box before the file arrives`)
    }
    if (capture.width / capture.height !== row.width / row.height) {
      out.push(`${what} declares ${row.width} x ${row.height} and ${MANIFEST} records the capture as ${capture.width} x ${capture.height}: the served picture is the capture at a smaller size and the same shape, and a row whose shape differs is a box the browser reserves wrongly`)
    }
    if (row.alt !== capture.alt) {
      out.push(`${what} carries alt text ${MANIFEST} does not: the manifest holds the account of that screen as the capture wrote it, and the row is that text verbatim`)
    }
  }
  const missing = [...captures.keys()].filter((stem) => !rows.some((row) => String(row.file).replace(/\.[a-z0-9]+$/i, '') === stem))
  if (missing.length > 0) {
    out.push(`${SCREENS_YAML} carries no row for ${missing.join(', ')}, which ${MANIFEST} records: the manifest lists the screenshots this site has`)
  }
  return out
}

/**
 * Everything wrong with a built slider's relation to the rows it was handed, as the lines a
 * reader of a failure gets. It returns the lines rather than asserting them, so the case beside
 * it can plant the ways a slide stops being the row's and watch each refusal fire:
 *
 *   - one slide per row, and each row's own `src`, `alt` and declared box;
 *   - the first slide fetched with the page and the rest lazily, all of them decoding async;
 *   - one caption per row, inside the slide it belongs to, carrying the row's own line;
 *   - one dot per slide, dot n naming slide n and each dot named (it carries no text);
 *   - and a track the keyboard can reach and a screen reader can name.
 */
function sliderFailures(page, rows) {
  const out = []
  const slider = sliderOf(page)
  if (!slider) {
    return [
      `the console page carries no element marked data-slot="${SLOT}" while ${SCREENS_YAML} carries ${rows.length} row(s): the slider is the rows, and a page with no frame is a page whose pictures nobody sees`,
    ]
  }
  const inside = [...walk(slider)]

  const images = inside.filter((el) => el.name === 'img')
  if (images.length !== rows.length) {
    out.push(`the slider renders ${images.length} <img> for ${rows.length} row(s) in ${SCREENS_YAML}`)
  }
  const slides = inside.filter((el) => el.name === 'li' && hasClass(el, 'screen'))
  if (slides.length !== rows.length) {
    out.push(`the track carries ${slides.length} slide(s) for ${rows.length} row(s) in ${SCREENS_YAML}`)
  }

  for (const [index, row] of rows.entries()) {
    const img = images[index]
    if (!img) continue
    const where = `the ${index + 1}th slide`
    // The file is the row's NAME under `public/screenshots/`, and the site path is derived
    // from it here rather than written in the data file: a `file` that were a URL would be a
    // second copy of a fact the build composes (Global Constraint 9).
    if (img.attrs.get('src') !== `/${SCREENS_DIR}/${row.file}`) {
      out.push(`${where} loads ${JSON.stringify(img.attrs.get('src'))}, want the row's own file under /${SCREENS_DIR}/`)
    }
    if (img.attrs.get('alt') !== row.alt) {
      out.push(`${where}'s alt is not the row's alt text, which is what a reader gets when the picture does not arrive and what a screen reader reads instead of it`)
    }
    if (img.attrs.get('width') !== String(row.width) || img.attrs.get('height') !== String(row.height)) {
      out.push(`${where} declares ${JSON.stringify(img.attrs.get('width'))} x ${JSON.stringify(img.attrs.get('height'))} and the row carries ${row.width} x ${row.height}: the box the browser reserves is the file's own`)
    }
    const loading = index === 0 ? 'eager' : 'lazy'
    if (img.attrs.get('loading') !== loading) {
      out.push(`${where} is loaded ${JSON.stringify(img.attrs.get('loading') ?? null)}, and the first slide is the one the frame shows so it is fetched with the page (eager) while every other one arrives when a reader scrolls to it (lazy)`)
    }
    if (img.attrs.get('decoding') !== 'async') {
      out.push(`${where} declares decoding ${JSON.stringify(img.attrs.get('decoding') ?? null)}, and every slide decodes asynchronously so a 1920-wide WebP does not hold up the frame around it`)
    }
  }

  // The captions, in order, each inside the slide it belongs to: a caption read apart from
  // its picture is a caption about the picture before it.
  const captions = inside.filter((el) => el.name === 'figcaption')
  if (captions.length !== rows.length) {
    out.push(`the slider renders ${captions.length} caption(s) for ${rows.length} row(s) in ${SCREENS_YAML}`)
  }
  for (const [index, row] of rows.entries()) {
    const caption = captions[index]
    if (!caption) continue
    if (textOf(caption).trim() !== row.caption) {
      out.push(`the ${index + 1}th slide's caption is ${JSON.stringify(textOf(caption).trim())} and the row's is ${JSON.stringify(row.caption)}: the line under a picture is the row's own`)
    }
    if (slides[index] && ![...walk(slides[index])].includes(caption)) {
      out.push(`the ${index + 1}th caption is not inside the ${index + 1}th slide, so it reads as a caption about the picture before it`)
    }
  }

  // The dots are the slides' own order read twice: dot n names slide n, exactly as the
  // captions follow the pictures. One dot per slide, unless there is one slide — a control
  // that can only take a reader where they already are is noise, and the one-slide case below
  // is the case that holds that branch.
  const dots = inside.filter((el) => el.name === 'a' && hasClass(el, 'screen-dot'))
  if (rows.length > 1 && dots.length !== slides.length) {
    out.push(`the slider carries ${dots.length} dot(s) for ${slides.length} slide(s)`)
  }
  for (const [index, slide] of slides.entries()) {
    const id = slide.attrs.get('id')
    if (!id) {
      out.push(`the ${index + 1}th slide carries no id, so no dot can reach it`)
      continue
    }
    const dot = dots[index]
    if (!dot) continue
    if (dot.attrs.get('href') !== `#${id}`) {
      out.push(`the ${index + 1}th dot points at ${JSON.stringify(dot.attrs.get('href'))} and the ${index + 1}th slide is #${id}: a dot that points elsewhere sends a reader to the wrong picture`)
    }
    if ((dot.attrs.get('aria-label') ?? '').trim() === '') {
      out.push(`the ${index + 1}th dot has no accessible name, and it carries no text: a screen reader announces it as a link and nothing else`)
    }
  }

  // The track: one focusable, named scroll container, so the keyboard can reach the slides
  // that are off the right-hand edge (`tabindex="0"` on a scroll container is what makes it
  // scrollable by arrow keys at all).
  const track = inside.find((el) => hasClass(el, 'screens-track'))
  if (!track) {
    out.push('the slider carries no track element, so there is nothing the slides sit in')
  } else {
    if (track.attrs.get('tabindex') !== '0') {
      out.push('the slider\'s track is not focusable, so a keyboard cannot scroll it')
    }
    if ((track.attrs.get('aria-label') ?? '').trim() === '') {
      out.push('the slider\'s track has no accessible name: a focusable scroll container is announced as an unnamed group')
    }
  }
  return out
}

// ---------------------------------------------------------------------------------------
// 1. The shipped rows: the four captures, and the page that renders them.
// ---------------------------------------------------------------------------------------

test('the four captures the data file names are the slides the console page renders', (t) => {
  // The premise: this checkout's own data file carries rows, because the owner's feedback of
  // 2026-10-21 was that the page's screenshots were missing. A data file that had gone empty
  // again would make this case assert nothing about the state it names.
  const rows = readScreens()
  assert.ok(
    rows.length > 0,
    `${SCREENS_YAML} carries no slide, and the console page's screenshots are what it renders: the owner's feedback of 2026-10-21 was "console page: screenshots missing"`,
  )

  const built = buildCopy(t, () => {})
  const failures = sliderFailures(consolePage(built), rows)
  assert.deepEqual(
    failures,
    [],
    `the slider the data file's ${rows.length} row(s) draw is not the slider this page promises:\n  ${failures.join('\n  ')}`,
  )
})

test('every row is a picture this site serves, at the shape and alt text the manifest records', () => {
  // No build here: this half is about the files and the record beside them, and it is the half
  // a row that named a picture nobody took, sized a box wrongly or paraphrased an alt text is
  // refused by. The manifest is what the run that took the four captures wrote down, so it is
  // the one place outside the data file where their size and their account are stated.
  const rows = readScreens()
  assert.ok(rows.length > 0, `${SCREENS_YAML} carries no slide for this case to check against ${MANIFEST}`)
  const failures = pictureFailures(rows, readManifest())
  assert.deepEqual(
    failures,
    [],
    `a row and the picture it names disagree:\n  ${failures.join('\n  ')}`,
  )
})

test('a slide stops being the row\'s when its picture, its box, its loading or its caption moves', (t) => {
  // The case above proves what the shipped page is, not what the reading refuses. Each plant
  // below is one of the ways a slide stops being the row it renders — the picture another
  // file's, the alt text nobody wrote, the box the file is not, the first slide fetched late,
  // a decode that blocks, a dot with no name, a caption that lost its picture — and each has to
  // be refused by the sentence that names it. Without them a reading that stopped seeing a
  // slide's `loading`, or its caption, would pass the case above and hold nothing.
  const rows = readScreens()
  const page = consolePage(buildCopy(t, () => {}))
  const shipped = sliderFailures(page, rows)
  assert.deepEqual(
    shipped,
    [],
    `the shipped slider is already refused before any plant, so the plants below would prove nothing:\n  ${shipped.join('\n  ')}`,
  )

  const firstFile = `/${SCREENS_DIR}/${rows[0].file}`
  const cases = [
    ['a slide loading another file', page.replace(firstFile, '/screenshots/other.webp'), /want the row's own file under/],
    ['a slide whose alt text nobody wrote', page.replace(rows[0].alt, 'A screenshot of the console.'), /is not the row's alt text/],
    ['a declared box that is not the file\'s', page.replace(`width="${rows[0].width}"`, `width="${rows[0].width + 8}"`), /the box the browser reserves is the file's own/],
    ['the first slide fetched lazily, so the frame waits for nothing and shows nothing', page.replace('loading="eager"', 'loading="lazy"'), /the first slide is the one the frame shows/],
    ['a slide that decodes synchronously, holding up the frame around it', page.replaceAll(' decoding="async"', ''), /every slide decodes asynchronously/],
    ['a dot with no accessible name', page.replace(/aria-label="Show screenshot 2 of \d+"/, 'aria-label=""'), /dot has no accessible name/],
  ]
  for (const [what, planted, expected] of cases) {
    assert.notEqual(planted, page, `${what} left the built page as it is, so the plant is not a plant`)
    const failures = sliderFailures(planted, rows)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused by the sentence that names it:\n  ${failures.join('\n  ') || '(nothing refused)'}`,
    )
  }

  // And the picture reading's own refusal, on the rows rather than on the page: a row whose
  // declared box disagrees with the file it names is the state the case above cannot be in.
  const manifest = readManifest()
  const wrongBox = pictureFailures([{ ...rows[0], height: rows[0].height + 1 }, ...rows.slice(1)], manifest)
  assert.ok(
    wrongBox.some((line) => /declares .* and the file is/.test(line)),
    `a row whose declared box is not the file's own was not refused:\n  ${wrongBox.join('\n  ') || '(nothing refused)'}`,
  )
  const paraphrased = pictureFailures([{ ...rows[0], alt: 'A screen.' }, ...rows.slice(1)], manifest)
  assert.ok(
    paraphrased.some((line) => /carries alt text .* does not/.test(line)),
    `a row that paraphrased an alt text was not refused:\n  ${paraphrased.join('\n  ') || '(nothing refused)'}`,
  )
})

// ---------------------------------------------------------------------------------------
// 2. A fixture build: the rows are the only source of the slider's facts.
// ---------------------------------------------------------------------------------------

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

test('two slides render two slides, two dots and the data file\'s own text', (t) => {
  const built = buildWithScreens(t, fixtureYAML(FIXTURE))
  const page = consolePage(built)
  const slider = sliderOf(page)
  assert.ok(slider, `the console page renders no element marked data-slot="${SLOT}" with two slides in ${SCREENS_YAML}`)

  // The whole relation between the page and the rows, which is one reading for the shipped
  // four and for this fixture: a fact the component typed rather than rendered — a caption, a
  // size, a `loading` — is refused here whether the row is the owner's picture or this file's.
  const failures = sliderFailures(page, FIXTURE)
  assert.deepEqual(failures, [], `the fixture's two slides are not the rows that were written:\n  ${failures.join('\n  ')}`)

  const images = [...walk(slider)].filter((el) => el.name === 'img')
  for (const [index, row] of FIXTURE.entries()) {
    assert.equal(
      images[index].attrs.get('src'),
      `/${SCREENS_DIR}/${row.file}`,
      `the ${index + 1}th slide's src is ${JSON.stringify(images[index].attrs.get('src'))}, want the row's own file under /${SCREENS_DIR}/`,
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
// 3. One slide: one slide, and no control at all — the branch the dots are suppressed on.
// ---------------------------------------------------------------------------------------

test('one slide renders one slide, the row\'s own text, and no control at all', (t) => {
  // The branch the component takes when `screens.length > 1` is false: the dots are
  // suppressed, because a dot that can only take a reader where they already are is noise. A
  // later change that rendered the dots for any non-empty list would delete that branch
  // silently, which is what this case is for. It is also the state a data file with NO row in
  // it renders: no picture, no frame, no slider section — a page that draws a picture-shaped
  // hole says a broken image where the site means "there is no picture".
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
  assert.equal(images[0].attrs.get('loading'), 'eager', 'the one slide is not the first slide the frame shows, so it is fetched with the page')

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

test('a data file with no row in it renders no slider at all, and no picture-shaped hole', (t) => {
  // The other end of the same branch, and the state `src/content.ts` still reads as legal:
  // nothing in the build can make a screenshot, so an empty list is a document and not a
  // failure — and what the page must not do then is draw a frame around nothing. A reader who
  // met an empty box could not tell it from a picture that failed to arrive.
  const built = buildWithScreens(t, 'version: 1\nscreens: []\n')
  const page = consolePage(built)
  assert.equal(sliderOf(page), null, `the console page draws the slider's section with no row in ${SCREENS_YAML}`)
  // Reading the markup rather than the raw bytes: the component's stylesheet is inlined into
  // the page whoever renders it, so a regex over the file would find the class names in the
  // CSS. What must not be there is an element that draws a slide.
  const slides = [...walk(parse(page))].filter(
    (el) => ['img', 'figure', 'ul', 'section'].includes(el.name) && /(^|\s)screen(-image|-figure|-track|-placeholder)?(\s|$)/.test(el.attrs.get('class') ?? ''),
  )
  assert.deepEqual(
    slides.map((el) => el.name),
    [],
    'the console page carries slider markup with no row in the data file: with nothing to show, the slider is not there at all',
  )
  // And the page is otherwise the page it was: the sections a reader met before the slider
  // existed are all still there, so the slider is not the whole page.
  for (const id of ['downloads', 'console-facts']) {
    assert.ok(page.includes(`id="${id}"`), `the console page lost its #${id} section`)
  }
})

// ---------------------------------------------------------------------------------------
// 4. The slider works with no script: the browser's own scrolling and its own anchors.
// ---------------------------------------------------------------------------------------

test('the slider is the browser\'s own scrolling, with nothing of its own to run', (t) => {
  const built = buildCopy(t, () => {})
  const slider = sliderOf(consolePage(built))
  assert.ok(slider, `the console page renders no slider with ${SCREENS_YAML}'s rows in the data file`)

  // No script, inline or linked, inside the slider: the site ships one first-party file
  // (D183) and the slider needs none of it. A slider that shipped its own script would be a
  // second file, a CSP decision and a page that needs JavaScript to be read — the three
  // things the site's own rules refuse.
  assert.deepEqual(
    [...walk(slider)].filter((el) => el.name === 'script').map((el) => el.attrs.get('src') ?? '(inline)'),
    [],
    'the slider ships a script, and the slider works with JavaScript off: the track scrolls, the dots are anchors, and nothing here needs code',
  )

  // The dots are links, which is what makes them keyboard-operable: a focusable element a
  // reader reaches with Tab and follows with Enter, named for a screen reader (a11y.mjs reads
  // the names too, and this case is the one that names the slider when one goes missing).
  const dots = [...walk(slider)].filter((el) => el.name === 'a' && hasClass(el, 'screen-dot'))
  assert.ok(dots.length > 1, `the slider carries ${dots.length} dot(s), and this case is about the control the rows are navigated with`)

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

  // And the things the script does drive are the script's: the bar is in the markup `hidden`, so
  // a reader who runs no script is shown the four screenshots and the four dots rather than two
  // arrows that do nothing and a count nothing keeps in step. That is the same rule the copy
  // control's markup is held to (`test/copy.mjs`), and it is the half of the fallback the
  // stylesheet cannot state.
  const bar = [...walk(slider)].find((el) => hasClass(el, 'screens-bar'))
  assert.ok(bar, 'the slider carries no control bar, and the arrows and the count are what the owner asked for on 2026-10-21 (the case below reads the bar itself)')
  assert.ok(
    bar.attrs.has('hidden'),
    'the slider\'s control bar is not emitted `hidden`: it is the script that reveals it, and a bar the markup renders visible is two buttons and a count that do nothing for a reader whose browser runs no script (D183)',
  )
})

// ---------------------------------------------------------------------------------------
// 5. The script's half of the control (the owner's feedback of 2026-10-21).
//
// 5a. The bar in the markup: the two arrows, the count, `hidden`, and only while there is
//     somewhere to go.
// 5b. The same bar with `public/scripts/site.js` run against it — what it counts, where a press
//     takes the track, and what stops the autoplay — each reading proved by a planted defect.
// ---------------------------------------------------------------------------------------

/** The hooks the bar is written in: what the script finds it by, named once. */
const BAR = '[data-slider-bar]'
const COUNTER = '[data-slider-counter]'
const PREV = '[data-slider-prev]'
const NEXT = '[data-slider-next]'
const TRACK = '.screens-track'
const DOT = '.screen-dot'

/** The width the stub gives every element: the track's box, which is the slide's own width. */
const STUB_WIDTH = 1000

test('the bar is in the markup, emitted hidden, with two named arrows and an unwritten count', (t) => {
  // The bar is the script's to drive and the markup's to describe: the arrows carry their names
  // here because a chevron drawn by the stylesheet has no text, the count arrives empty because
  // the script is what writes it, and the whole bar is `hidden` because a reader with no script
  // has no script to reveal it with. Each of those is one edit away from being wrong in a way
  // nothing else on the page would notice.
  const built = buildCopy(t, () => {})
  const slider = sliderOf(consolePage(built))
  assert.ok(slider, `the console page renders no slider with ${SCREENS_YAML}'s rows in the data file`)
  const inside = [...walk(slider)]

  const bars = inside.filter((el) => hasClass(el, 'screens-bar'))
  assert.equal(
    bars.length,
    1,
    `the slider carries ${bars.length} control bar(s) marked .screens-bar, and it has exactly one: the Previous arrow, the count and the Next arrow`,
  )
  const bar = bars[0]
  assert.ok(
    bar.attrs.has('hidden'),
    'the bar is not emitted `hidden`: the script reveals it, and a bar the markup renders visible is two buttons that do nothing for a reader whose browser runs no script (D183)',
  )

  // The two arrows, in the order the board draws them, each a real button with its own name:
  // a `<button>` with no `type` in a form submits it, and a chevron has no text for a screen
  // reader to announce (WCAG 4.1.2).
  const arrows = [...walk(bar)].filter((el) => el.name === 'button')
  assert.deepEqual(
    arrows.map((el) => (el.attrs.has('data-slider-prev') ? 'previous' : el.attrs.has('data-slider-next') ? 'next' : `(unhooked ${el.name})`)),
    ['previous', 'next'],
    'the bar\'s buttons are not the two arrows the owner asked for, hooked in order: a control the script cannot find is a dead button, and this is the hook it finds them by',
  )
  for (const [which, arrow] of [['previous', arrows[0]], ['next', arrows[1]]]) {
    assert.equal(arrow.attrs.get('type'), 'button', `the ${which} arrow is <button type="${arrow.attrs.get('type') ?? ''}">: a control that is not a button is not one a keyboard presses, and a bare <button> in a form submits it`)
    assert.match(
      arrow.attrs.get('aria-label') ?? '',
      new RegExp(which, 'i'),
      `the ${which} arrow's accessible name is ${JSON.stringify(arrow.attrs.get('aria-label') ?? '')}, and it carries no text of its own: a chevron drawn by the stylesheet is announced as an unnamed button`,
    )
  }

  // The count: one element, empty, and a polite live region — the script writes `n / N` into it
  // as the reader moves, and a number written by the markup is a number nothing keeps in step.
  const counters = [...walk(bar)].filter((el) => el.attrs.has('data-slider-counter'))
  assert.equal(counters.length, 1, `the bar carries ${counters.length} element(s) hooked as the count; it has exactly one, and the script writes the slides' count into it`)
  assert.equal(
    textOf(counters[0]).trim(),
    '',
    'the count carries text in the markup: the script writes `n / N` into it with every move, and a number the markup wrote is a number that is wrong the moment the reader moves',
  )
  assert.equal(counters[0].attrs.get('role'), 'status', 'the count is not a status region, so a screen reader is not told which screenshot the slider is on')
  assert.equal(
    (counters[0].attrs.get('aria-live') ?? '').toLowerCase(),
    'polite',
    `the count is aria-live="${counters[0].attrs.get('aria-live') ?? ''}": a slide changing under a reader is announced politely, and an assertive one interrupts what they are hearing`,
  )
  // Between the two arrows, which is where the board draws it: `[...walk]` is document order, so
  // the count standing after the previous arrow and before the next one is a statement about the
  // markup rather than about the string.
  const order = [...walk(bar)]
  assert.ok(
    order.indexOf(arrows[0]) < order.indexOf(counters[0]) && order.indexOf(counters[0]) < order.indexOf(arrows[1]),
    'the count is not between the two arrows: the bar reads Previous, count, Next, and a count drawn somewhere else is a reading the reader has to hunt for',
  )

  // **And a one-slide slider carries no bar at all.** The same branch the dots are suppressed
  // on: a control that can only take a reader where they already are is noise — and here it is
  // worse than noise, because the script would reveal two arrows that go nowhere. The one-slide
  // case above holds the whole of that branch; this is the bar's half of it, stated where the
  // bar is read.
  const single = buildWithScreens(t, fixtureYAML([FIXTURE[0]]))
  assert.deepEqual(
    [...walk(sliderOf(consolePage(single)))].filter((el) => hasClass(el, 'screens-bar')).length,
    0,
    'a one-slide slider renders the control bar: with one screenshot there is nowhere to go, and the script would reveal two arrows that go nowhere',
  )
})

/**
 * What one run of `site.js` makes of the slider, as the lines it failed: an empty array is the
 * script's half of the control being the half the owner asked for on 2026-10-21.
 *
 * The reading drives the page the way a reader does — presses the arrows, presses a dot, scrolls
 * the track, puts the pointer over the slider, moves the focus in and out, hides the tab and
 * changes the motion preference — and after each of those it reads the three things the page
 * shows: the count, the dot the reader is on, and where the track stands. `when` is the state
 * each reading is taken in, so a failure says which move was wrong rather than which number was.
 */
function scriptFailures(page, { width = STUB_WIDTH } = {}) {
  const out = []
  const fail = (why) => out.push(why)
  const slider = page.find('[data-slider]')[0]
  if (!slider) return ['the page carries no [data-slider] for the script to drive']
  const bar = slider.querySelector(BAR)
  if (!bar) return ['the script has no bar to reveal: the markup carries no [data-slider-bar] inside the slider']
  if (bar.hidden) {
    fail('the bar is still `hidden` with the script running: the arrows and the count are the owner\'s feedback of 2026-10-21, and a bar nobody reveals is two buttons that never appear')
  }
  const track = slider.querySelector(TRACK)
  const dots = slider.querySelectorAll(DOT)
  const count = bar.querySelector(COUNTER)
  const prev = bar.querySelector(PREV)
  const next = bar.querySelector(NEXT)
  if (!track || !count || !prev || !next || dots.length < 2) {
    return [...out, `the stub page is not a slider (track ${!!track}, count ${!!count}, arrows ${!!prev}/${!!next}, dots ${dots.length}), so none of the readings below could be taken`]
  }
  const total = dots.length
  const shown = () => (count.textContent ?? '').trim()
  const current = () => dots.map((dot) => dot.getAttribute('aria-current')).filter((mark) => mark === 'true')
  const running = () => page.running()

  /** What the slider must read and where it must stand when it is on the `at`th slide. */
  const read = (when, at) => {
    const want = `${at + 1} / ${total}`
    if (shown() !== want) fail(`${when}: the count reads ${JSON.stringify(shown())} and the slider stands on ${want}`)
    if (track.scrollLeft !== at * width) {
      fail(`${when}: the track is at ${track.scrollLeft}px, and slide ${at + 1} of ${total} stands at ${at * width}px of a ${width}px-wide box`)
    }
    const marks = current()
    if (marks.length !== 1 || dots[at].getAttribute('aria-current') !== 'true') {
      fail(`${when}: ${marks.length} dot(s) carry aria-current="true" and the ${at + 1}th is ${dots[at].getAttribute('aria-current') === 'true' ? '' : 'not '}one of them, so a screen reader is not told which screenshot is the one on screen`)
    }
  }

  // What the page reads before anything moved, and the advance that is the whole point of the
  // autoplay: six seconds, one slide, and the count and the mark following it.
  read('at load', 0)
  const timers = running()
  if (timers.length !== 1) {
    fail(`the slider runs ${timers.length} timer(s) with nobody reading it, and a visitor who touches nothing is shown one advance every six seconds (the owner's "~6s")`)
  } else if (timers[0].ms !== 6000) {
    fail(`the autoplay's interval is ${timers[0].ms}ms, and the owner asked for about six seconds — long enough to look at a screenshot the size of a page`)
  } else {
    timers[0].fn()
    read('after the autoplay fired', 1)
  }

  // The two arrows, and the wrap at either end: a Next on the last slide is the first slide
  // rather than a button that does nothing, which is what makes two arrows the whole of the
  // navigation.
  next.dispatch('click')
  read('after a Next press', 2 % total)
  prev.dispatch('click')
  read('after a Previous press', 1 % total)
  prev.dispatch('click')
  read('after a Previous press back to the first slide', 0)
  prev.dispatch('click')
  read('after a Previous press that wraps from the first slide', total - 1)
  next.dispatch('click')
  read('after a Next press that wraps from the last slide', 0)

  // A dot press: the anchor is what a reader with no script follows, and with a script the press
  // is the script's own move — so it cancels the anchor's jump and lands on the slide the dot
  // points at, with the count and the mark behind it.
  const third = Math.min(2, total - 1)
  const press = dots[third].dispatch('click')
  read('after a dot press', third)
  if (!press.defaultPrevented) {
    fail('a dot press left the anchor to do its own jump: the script takes the press over so that the count and the mark follow the move it makes, and the anchor is what a reader with no script gets')
  }

  // The reader's own swipe: the track is the source of truth, so a scroll it did not ask for
  // moves the count and the mark too — otherwise a trackpad reader watches a count that describes
  // a slide nobody is on.
  track.scrollLeft = width
  track.dispatch('scroll')
  read('after the reader scrolled the track themselves', 1)

  // And the four things that stop it, each of which is a reader the slider must not move under.
  const stops = [
    ['the pointer arriving over the slider', () => slider.dispatch('pointerenter'), () => slider.dispatch('pointerleave'), 'a reader looking at a screenshot is a reader whose screenshot does not move'],
    // The focus leaving the slider is a `focusin` somewhere else, which is what a browser sends
    // whatever the reader moved to; `main` is the page the slider sits in.
    ['a focus inside the slider', () => page.document.dispatch('focusin', { target: next }), () => page.document.dispatch('focusin', { target: page.find('main')[0] }), 'a keyboard reader standing on an arrow is not finished reading'],
    ['the tab going to the background', () => page.document.setHidden(true), () => page.document.setHidden(false), 'a tab nobody is looking at has no reader to move for'],
    ['the visitor asking for less motion', () => page.setReduce(true), () => page.setReduce(false), 'prefers-reduced-motion: reduce is a visitor saying that things which move on their own must not (D105)'],
  ]
  for (const [what, stop, start, why] of stops) {
    stop()
    if (running().length !== 0) fail(`${what} did not stop the autoplay: ${why}`)
    start()
    if (running().length !== 1) fail(`${what} stopped the autoplay and letting it go did not start it again: a slider that stops for the pointer and never resumes is a slider that stopped`)
  }
  return out
}

test('the script reveals the bar, counts the slides, and stops the moment a reader is reading', (t) => {
  const built = buildCopy(t, () => {})
  const html = consolePage(built)
  const failures = scriptFailures(fakeDOM(html, { width: STUB_WIDTH }))
  assert.deepEqual(
    failures,
    [],
    `the slider the script drives is not the slider the owner asked for on 2026-10-21:\n  ${failures.join('\n  ')}`,
  )

  // The two states a page can load in where there is nothing to start: a visitor who has already
  // asked for less motion, and a tab that is already in the background. `setReduce` and
  // `setHidden` above are the same two settings arriving later; these are the page never having
  // started at all, which is the branch `mayAdvance` is read on before the first tick.
  for (const [why, options] of [
    ['a visitor whose system asks for less motion', { reduce: true }],
    ['a tab loaded in the background', { hidden: true }],
  ]) {
    const page = fakeDOM(html, { width: STUB_WIDTH, ...options })
    assert.deepEqual(page.running(), [], `the slider started its autoplay for ${why}: the first thing the script reads before it advances anything is whether it may`)
  }
})

test('a slider the script does not drive is refused, plant by plant', (t) => {
  // The reading above is only worth its lines if it disagrees with a script that does the wrong
  // thing, so every plant below is one of the ways this stops being the owner's slider, and each
  // one has to be refused by name. The edits are made in `site.js`'s own text — the file the
  // build copies into `dist/` verbatim, which is why no plant needs a rebuild.
  const built = buildCopy(t, () => {})
  const html = consolePage(built)
  const defects = [
    {
      what: 'a bar nobody reveals',
      plant: (src) => src.replace('if (bar) bar.hidden = false', 'if (bar) bar.hidden = true'),
      wants: /bar is still `hidden`/,
    },
    {
      what: 'a count the script never writes',
      plant: (src) => src.replace('    if (bar) bar.hidden = false\n    paint()', '    if (bar) bar.hidden = false'),
      wants: /at load: the count reads ""/,
    },
    {
      what: 'an autoplay that advances every fifteen seconds',
      plant: (src) => src.replace('var AUTO_MS = 6000', 'var AUTO_MS = 15000'),
      wants: /interval is 15000ms/,
    },
    {
      what: 'an autoplay that never runs at all',
      plant: (src) => src.replace('timer = window.setInterval(forward, AUTO_MS)', 'return'),
      wants: /runs 0 timer\(s\)/,
    },
    {
      what: 'an autoplay that runs on for a reader who asked for less motion',
      plant: (src) => src.replace('!(query && query.matches)', 'true'),
      wants: /asking for less motion did not stop the autoplay/,
    },
    {
      what: 'an autoplay that keeps running in a background tab',
      plant: (src) => src.replace('!document.hidden && ', ''),
      wants: /tab going to the background did not stop the autoplay/,
    },
    {
      what: 'an autoplay the pointer over the slider does not stop',
      plant: (src) => src.replace('return !over && !inside && !document.hidden', 'return true'),
      wants: /pointer arriving over the slider did not stop the autoplay/,
    },
    {
      what: 'an autoplay a focus inside the slider does not stop',
      plant: (src) => src.replace('inside = section.contains(event.target)', 'inside = false'),
      wants: /focus inside the slider did not stop the autoplay/,
    },
    {
      what: 'an arrow that goes past the end instead of round it',
      plant: (src) => src.replace('index = (to + slides.length) % slides.length', 'index = to < 0 ? 0 : to > slides.length - 1 ? slides.length - 1 : to'),
      wants: /Previous press that wraps from the first slide/,
    },
    {
      what: 'a dot press that is not taken over',
      plant: (src) => src.replace('          show(at)', '          return'),
      wants: /after a dot press: the count reads/,
    },
    {
      what: 'a count that does not follow the reader\'s own scrolling',
      plant: (src) => src.replace('      var at = shown()', '      var at = index'),
      wants: /after the reader scrolled the track themselves/,
    },
  ]
  for (const { what, plant, wants } of defects) {
    const planted = plant(SCRIPT_SOURCE)
    assert.notEqual(planted, SCRIPT_SOURCE, `${what}: the planted edit changed nothing, so this fixture proves nothing`)
    const failures = scriptFailures(fakeDOM(html, { width: STUB_WIDTH, script: planted }))
    assert.ok(
      failures.some((line) => wants.test(line)),
      `${what} was not refused (${wants}); the reading said:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
})

// ---------------------------------------------------------------------------------------
// 6. The refusals: the data file cannot carry a URL, a foreign format, an empty text, a size
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
      // The row would render `<img src="/screenshots/png">` while the refusal's own message
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
      yaml: fixtureYAML([FIXTURE[0]], [`    src: /${SCREENS_DIR}/runs-list.png`]),
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
