// website/test/landing-data.mjs — the four landing variants' data file, and the guard that
// refuses a figure nothing backs (go-to-market plan, Task G2.1; reconciliation F6, F12, F14,
// F20, F22).
//
// G2.1 is data and a loader, and the point of both is that **the four campaign pages cannot
// carry a claim this repository cannot back**. Four rules make that true, and each has cases
// below:
//
//   1. **A published figure is never typed into a page.** `website/data/landing.yaml` holds one
//      variant per campaign audience, and each variant's `proof_number` — the one figure the band
//      prints as a measurement — is either the sentinel `unmeasured` or a figure whose `source`
//      names an entry of `website/data/typical-costs.yaml`, the owner's own measurement of this
//      project's development runs, read through the existing `loadTypicalCosts(minRuns)` exactly
//      as the caps-and-costs guide reads it, so D97's minimum stays one number rather than two
//      (F6). The campaign's own rhetoric is copy rather than a measurement — `/save`'s headline
//      is the spec's "a tenth of the price" — and what the guard refuses is a `proof_number` with
//      nothing behind it: it judges no figure's truth, wording or roundness, so a figure that
//      names a measured entry at or above the minimum is read as the file wrote it (there is a
//      case for exactly that below).
//   2. **The media is self-hosted, and the hero's screen is one the site already carries.** A
//      variant's video and the `/crew` imagery are paths under `website/`, never a YouTube or
//      Vimeo URL: an embedded player would be a fourth third party the consent work never
//      admitted. The pages build without the files, because the owner supplies them when they
//      exist (F14, F20, D107, G2.5). The hero's picture is the other way round: a variant's
//      `screenshot` is the *name* of one of the console's own captures under
//      `website/public/screenshots/`, and it has to be a row of
//      `website/data/console-screens.yaml` — the file that carries that picture's alt text, its
//      caption and its own pixel size, and that the console page's own slider renders its slides
//      from (the homepage's "See it" panel is the same component over
//      `website/data/home-screens.yaml`, whose console rows are held to this file's).
//      So no campaign page adds a picture, a re-encoded copy or an origin, and
//      the hero reads the screen's alt text and its size out of the data file rather than
//      carrying a second copy of either: a name no row carries is refused by name, with the rows
//      that exist in the message (the owner's instruction of 2026-10-30, D190).
//   3. **The data file is the only place a variant's copy is written** — its path, headline,
//      supporting line, tone and WhatsApp pre-fill message — and the loader refuses a key, a
//      path or a message the site's own rules do not know: an unknown key is a fact nothing
//      renders and nothing pins, a path is the route the page is built at and has to be one of
//      the spec's four message names, and the pre-fill message names the variant it was opened
//      from (spec §6). A headline or supporting line that is blank is refused as well, because the
//      hero prints both and this file is the only place that copy is written.
//   4. **A partner logo is a name beside a self-hosted picture, and the name is there.** The
//      social-proof section prints the partner's own name beside the logo, so an entry whose
//      `name` is blank — or spaces, which is the same hole with a different spelling — is a
//      picture nobody is named beside, which is not social proof (F11, F12, D405).
//
// The names in the file are the spec's own four campaigns (`docs/go-to-market/spec.md` §6:
// Save, Control, Scale, Crew), and the one person named anywhere in it is the owner's own first
// name, in the pre-fill message the spec itself writes (F12). No partner logo is in the file and
// no organisation is named: a logo needs the partner's written permission and stays out until
// the owner has it (F11, D405, R3) — the case below uses a plainly fake name for the shape rule,
// as the plan's conventions require.
//
// **Red step** (the plan's own, recorded here as the plan requires — the line this file printed
// before the module existed): importing `../src/lib/landing.mjs` failed with
// `Cannot find module '.../website/src/lib/landing.mjs'`, because `loadLanding` did not exist
// yet. The cases below were written against the module the plan's Files line names, before it
// was written.
//
// How the cases drive it: the loader is imported directly (it is a plain module, as
// `consent-key.mjs` and `changelog.mjs` are), a document is built here as YAML text, and the
// facts a figure may be drawn from are passed in — the same seam `loadLanding()` fills from
// `loadSiteFacts()` and `loadTypicalCosts(minRuns)`: the site's own `min_runs` and the entries of
// the measured costs. So a rule is watched failing without a copy of the repository per case, and
// the file-opening half of the loader is driven against a temp directory of its own — the one
// seam it has, `readLandingFile(root)`. The YAML text below is read by `content.ts`'s reader,
// which `landing.mjs` imports rather than copies: one dialect for every data file the site reads.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'

import { LANDING_FILE, loadLanding, parseLanding, readLandingFile } from '../src/lib/landing.mjs'
import { loadConsoleScreens } from '../src/content.ts'
import { SITE } from './lib/html.mjs'

// ---- the spec's four campaigns, and the shipped file --------------------------------------

// The four campaigns of `docs/go-to-market/spec.md` §6, in the order that section writes them,
// with the headline it quotes for each. `/crew` is the one the spec gives no headline for — it
// asks for "lighter tone, same calls to action" — so its line is the file's own, written to that
// brief and pinned here rather than left unpinned.
const SPEC = [
  { name: 'save', path: '/save', headline: 'Same PRs, a tenth of the price.' },
  { name: 'control', path: '/control', headline: 'One review policy, enforced for every developer.' },
  { name: 'scale', path: '/scale', headline: 'Your best engineer, times twenty.' },
  { name: 'crew', path: '/crew', headline: 'Work like a crew of twenty. Nap like a cat.' },
]

/** The file as bytes, read here rather than asked of the loader: the cases below compare the
 * loader's answer against the file itself. */
function landingText() {
  return readFileSync(path.join(SITE, 'data', 'landing.yaml'), 'utf8')
}

// ---- documents built here, and the facts they are read with -------------------------------

/** The pre-fill message the spec writes, with the variant's own name in it (spec §6). */
function whatsappText(name) {
  return `Hi Dimi, I saw Fugaro on the ${name} page.`
}

/** The file's own name for a variant: the path without its leading slash (spec §6's "the four
 * message names", which are also the paths). */
function nameOf(route) {
  return route.replace(/^\//, '')
}

/**
 * One variant as the document writes it. Every key is rendered from the object the case passed,
 * so a case can add a key the loader does not know (the unknown-key cases) and can leave the
 * optional media out.
 */
function variantBlock(variant) {
  const lines = [
    `  - path: ${variant.path}`,
    `    headline: ${JSON.stringify(variant.headline)}`,
    `    supporting_line: ${JSON.stringify(variant.supportingLine)}`,
    `    tone: ${variant.tone}`,
    '    proof_number:',
    ...Object.entries(variant.proofNumber).map(([key, value]) => `      ${key}: ${value}`),
    `    whatsapp_text: ${JSON.stringify(variant.whatsapp)}`,
  ]
  for (const [key, value] of Object.entries(variant)) {
    if (['append', 'headline', 'path', 'proofNumber', 'supportingLine', 'tone', 'whatsapp'].includes(key)) continue
    lines.push(`    ${key}: ${value}`)
  }
  if (variant.append !== undefined) lines.push(`    ${variant.append}`)
  return lines.join('\n')
}

const DEFAULT_VARIANTS = [
  { path: '/save', headline: SPEC[0].headline, supportingLine: 'Every run is priced per call and stopped at the cap you set.', tone: 'savings', proofNumber: { value: 'unmeasured', source: 'unmeasured' }, whatsapp: whatsappText('save') },
  { path: '/control', headline: SPEC[1].headline, supportingLine: 'The recipe in fugaro.yaml is the policy every run follows.', tone: 'control', proofNumber: { value: 'unmeasured', source: 'unmeasured' }, whatsapp: whatsappText('control') },
  { path: '/scale', headline: SPEC[2].headline, supportingLine: 'One orchestrator asks Fugaro for many runs, each with its own budget.', tone: 'scale', proofNumber: { value: 'unmeasured', source: 'unmeasured' }, whatsapp: whatsappText('scale') },
  { path: '/crew', headline: SPEC[3].headline, supportingLine: 'The same engine and the same caps while you are somewhere else.', tone: 'playful', proofNumber: { value: 'unmeasured', source: 'unmeasured' }, whatsapp: whatsappText('crew') },
]

// The document's own fields, as the cases vary them: the booking link, the number (quoted, as
// the file writes it) and the logo list.
const DOCUMENT = {
  calendly: 'https://calendly.com/dimi-fugaro/demo',
  number: '"14377842426"',
  logos: null,
}

/** The logo list as the file writes it: empty, or one block entry per partner — never a flow
 * mapping, which the file's own reader does not read. */
function logoLines(logos) {
  if (logos === null) return ['partner_logos: []']
  return [
    'partner_logos:',
    ...logos.flatMap((logo) => Object.entries(logo).map(([key, value], at) => `${at === 0 ? '  - ' : '    '}${key}: ${value}`)),
  ]
}

/**
 * A `landing.yaml` document, built as text: the shipped file's own shape with one fact changed.
 * `change` is handed each variant and returns the keys to replace, so a case names the variant
 * under test by its path and the loader's answer is read back out of what the document says.
 */
function document({ variants = DEFAULT_VARIANTS, change = () => ({}), append = null, calendly = DOCUMENT.calendly, number = DOCUMENT.number, logos = DOCUMENT.logos } = {}) {
  return [
    'version: 1',
    `calendly_url: ${calendly}`,
    `whatsapp_number: ${number}`,
    ...logoLines(logos),
    'variants:',
    ...variants.map((variant) => variantBlock({ ...variant, ...change(variant) })),
    ...(append === null ? [] : [append]),
    '',
  ].join('\n')
}

/**
 * The facts a document is read with: the site's own three halves for these rules — `min_runs`
 * (website/data/site.yaml, D97, as `loadSiteFacts()` returns it), the entries
 * `loadTypicalCosts(minRuns)` returns for website/data/typical-costs.yaml, and the rows
 * `loadConsoleScreens()` returns for website/data/console-screens.yaml. The ids are that
 * file's own three rows (`task`, `follow_up`, `review`), with their own samples, plus one row
 * below the minimum: the state that file refuses for itself, kept here so the loader's own
 * reading of the threshold is watched failing too.
 */
const FACTS = {
  facts: { minRuns: 20 },
  // The console's screens as `loadConsoleScreens()` returns them: one row per capture, with the
  // site path composed from the file's name and the row's own alt text, caption and pixels. Two
  // rows are enough for the rule below — the fixtures name one of them and are refused when they
  // name anything else — and the strings are plainly stand-ins rather than the shipped screens'
  // own sentences, which `website/test/console-screens.mjs` holds to the files they describe.
  screens: [
    { file: 'console-spend.webp', src: '/screenshots/console-spend.webp', alt: 'The Spend screen of a test installation.', caption: 'Spend', width: 1920, height: 1200 },
    { file: 'console-run.webp', src: '/screenshots/console-run.webp', alt: 'The run detail screen of a test installation.', caption: 'One run', width: 1920, height: 1200 },
  ],
  typicalCosts: {
    jobs: [
      { id: 'task', name: 'A first task run', runs: 396, medianUSD: 0.36, middleHalfUSD: null, nineInTenUnderUSD: 1.27, worstUSD: 5.24 },
      { id: 'follow_up', name: 'A follow-up run', runs: 531, medianUSD: 0.35, middleHalfUSD: null, nineInTenUnderUSD: 1.0, worstUSD: 8.36 },
      { id: 'review', name: 'A review-only run', runs: 53, medianUSD: 0.11, middleHalfUSD: null, nineInTenUnderUSD: 0.22, worstUSD: null },
      { id: 'thin', name: 'A run below the minimum', runs: 5, medianUSD: 0.2, middleHalfUSD: null, nineInTenUnderUSD: 0.3, worstUSD: null },
    ],
  },
}

/** Read a document built above with the facts above. */
function read(options) {
  return parseLanding(document(options), LANDING_FILE, FACTS)
}

/** The message the loader refuses a document written here with; a case that is accepted fails
 * here, in the case that was watching for the refusal, rather than one case later. */
function refusalOf(text) {
  try {
    parseLanding(text, LANDING_FILE, FACTS)
  } catch (error) {
    return error.message
  }
  assert.fail(`the loader accepted a document it has to refuse, so the rule this case watches was not applied at all:\n${text}`)
}

/** The message the loader refuses a document built above with. */
function refusal(options) {
  return refusalOf(document(options))
}

/** `change` for one variant: the keys to replace on the variant at `route`. */
function only(route, changed) {
  return (variant) => (variant.path === route ? changed : {})
}

// ---- the shipped file ---------------------------------------------------------------------

test('the shipped file carries the spec\'s four campaigns, each at its own path', () => {
  const landing = loadLanding()

  assert.deepEqual(
    landing.variants.map((v) => v.path),
    SPEC.map((v) => v.path),
    'website/data/landing.yaml does not carry the spec\'s four campaigns, in the order spec §6 writes them: the four pages are built from this file, so a path that moved is a page that moved (G2.1)',
  )
  for (const [at, variant] of landing.variants.entries()) {
    assert.equal(variant.name, SPEC[at].name, `the variant at ${variant.path} is named ${JSON.stringify(variant.name)}, and the path names it: the message name is the variant's identity, in the page's own hooks and in the pre-fill text (G2.1)`)
    assert.equal(variant.headline, SPEC[at].headline, `the ${variant.name} variant's headline is not the sentence spec §6 writes for it: the campaign copy is the spec's, read from website/data/landing.yaml rather than typed into a page`)
    assert.ok(variant.supportingLine.length > 0, `the ${variant.name} variant carries no supporting line, and the hero prints the headline with one (spec §6)`)
    assert.ok(/^[a-z][a-z-]*$/.test(variant.tone), `the ${variant.name} variant's tone is ${JSON.stringify(variant.tone)}, and the tone is a word the page's look keys off (spec §6 gives /crew "lighter tone")`)
  }
})

test('every proof number in the shipped file is the sentinel, because no measurement backs a figure yet', () => {
  const landing = loadLanding()

  for (const variant of landing.variants) {
    assert.deepEqual(
      variant.proofNumber,
      { value: 'unmeasured', source: 'unmeasured' },
      `the ${variant.name} variant carries the proof number ${JSON.stringify(variant.proofNumber)}: the band prints one figure as a measurement, and until the owner's aggregation exists no variant publishes one — a percentage saved, a per-PR cost and an hourly rate are claims, and a figure arrives beside the entry of website/data/typical-costs.yaml that backs it (F6, D96, D97)`,
    )
  }
})

test('the file carries no partner logo and no media, because none exists yet', () => {
  const landing = loadLanding()

  assert.deepEqual(
    landing.partnerLogos,
    [],
    'website/data/landing.yaml carries a partner logo: a logo needs the partner\'s written permission, which is the owner\'s, and the social-proof section is hidden while the list is empty (F11, F12, D405, R3)',
  )
  for (const variant of landing.variants) {
    assert.equal(variant.video, null, `the ${variant.name} variant names a video: the owner produces the campaign videos, and the pages build without them until one exists (F14, G2.5)`)
    assert.equal(variant.image, null, `the ${variant.name} variant names an image: the /crew cat imagery is the owner's file, and the page builds without it (F14, G2.5)`)
  }
})

test('the booking link and the WhatsApp link are built from the file\'s own public identifiers', () => {
  const landing = loadLanding()

  assert.equal(landing.calendlyURL, 'https://calendly.com/dimi-fugaro/demo', 'the file\'s calendly_url is not the owner\'s booking link, which is the one input the pages link (R3)')
  assert.match(landing.whatsappNumber, /^\d+$/, 'the file\'s whatsapp_number is not the wa.me form: digits alone, written as a string so a leading zero would survive (R3)')
  for (const variant of landing.variants) {
    assert.equal(
      variant.whatsappURL,
      `https://wa.me/${landing.whatsappNumber}?text=${encodeURIComponent(whatsappText(variant.name))}`,
      `the ${variant.name} variant's WhatsApp link is not built from the number and that variant's own pre-fill message: the link is derived, never pasted into the file, so a changed number or message moves every page that renders it (G2.1)`,
    )
  }
  assert.doesNotMatch(
    landingText(),
    /https:\/\/wa\.me\//,
    'website/data/landing.yaml carries a wa.me link: the file writes the number and the message, and the loader derives the link from them, which is what keeps the number in one place (G2.1)',
  )
})

test('the pre-fill message names the variant it was opened from, and nobody else', () => {
  const landing = loadLanding()

  for (const variant of landing.variants) {
    assert.equal(
      variant.whatsappText,
      whatsappText(variant.name),
      `the ${variant.name} variant's pre-fill message is not the spec's own pattern with that variant's name in it (spec §6): "Hi Dimi, I saw Fugaro on the <variant> page." — the person it names is the owner's own first name, and no partner is named anywhere in the file (F12)`,
    )
  }
})

// ---- the claims guard ---------------------------------------------------------------------

test('a figure whose source is an entry of the measured costs is accepted', () => {
  const landing = read({ change: only('/save', { proofNumber: { value: '"90% lower AI coding spend"', source: 'task' } }) })

  assert.deepEqual(
    landing.variants[0].proofNumber,
    { value: '90% lower AI coding spend', source: 'task' },
    'a proof number naming the "task" row of website/data/typical-costs.yaml was not read: that row is the sample the figure is drawn from, and its own `runs` (396) is at or above D97\'s minimum',
  )
  assert.equal(landing.variants[1].proofNumber.value, 'unmeasured', 'a variant that carries the sentinel was given another variant\'s figure')
})

test('a figure whose source names no entry of the measured costs is refused', () => {
  const message = refusal({ change: only('/save', { proofNumber: { value: '"90% lower AI coding spend"', source: 'made_up' } }) })

  assert.match(message, /made_up/, `a proof number whose source names no entry of website/data/typical-costs.yaml was refused without naming the source: a build error is read by whoever just edited the file, and the source is what they have to correct (F6) — the message was ${JSON.stringify(message)}`)
  assert.match(message, /task/, `the refusal does not name the entries the measured costs do carry, so the editor is not told what a figure may be drawn from — the message was ${JSON.stringify(message)}`)
})

test('a figure drawn from a row below min_runs is refused', () => {
  const message = refusal({ change: only('/save', { proofNumber: { value: '"90% lower AI coding spend"', source: 'thin' } }) })

  assert.match(message, /minimum/, `a proof number drawn from a row with 5 runs was accepted: D97's minimum is the threshold of every figure the site publishes, and it reaches the loader from website/data/site.yaml through the same loadTypicalCosts() the caps-and-costs guide reads (F6) — the message was ${JSON.stringify(message)}`)
  assert.match(message, /5/, `the refusal does not say the sample the row carries, so a figure below the minimum cannot be corrected from it — the message was ${JSON.stringify(message)}`)
})

test('the sentinel and a source move together, in both directions', () => {
  const sentinelWithSource = refusal({ change: only('/save', { proofNumber: { value: 'unmeasured', source: 'task' } }) })
  assert.match(sentinelWithSource, /source/, `a variant carrying the sentinel value with a named source was accepted: "unmeasured" is a variant with no figure, and a source beside it reads as a measurement — the message was ${JSON.stringify(sentinelWithSource)}`)

  const sourceWithoutValue = refusal({ change: only('/save', { proofNumber: { value: '"90% lower AI coding spend"', source: 'unmeasured' } }) })
  assert.match(sourceWithoutValue, /source/, `a figure with the sentinel in place of its source was accepted: a published figure is never typed into the file without the entry that backs it (F6) — the message was ${JSON.stringify(sourceWithoutValue)}`)
})

test('a proof number that is not the sentinel and carries no figure is refused', () => {
  const message = refusal({ change: only('/save', { proofNumber: { value: 'lower AI coding spend', source: 'task' } }) })

  assert.match(message, /figure/, `a proof number reading "lower AI coding spend" was accepted: a proof number is a figure, and a variant with words and no figure carries the sentinel "unmeasured" instead — the message was ${JSON.stringify(message)}`)
})

// ---- the media rule -----------------------------------------------------------------------

test('a self-hosted media path is accepted and an external URL is refused', () => {
  const landing = read({ change: only('/crew', { video: '/videos/crew.mp4', image: '/images/crew-cat.png' }) })

  assert.equal(landing.variants[3].video, '/videos/crew.mp4', 'a self-hosted video path was not read: the variant renders a file the site serves, and the owner supplies it when it exists (F14, D107)')
  assert.equal(landing.variants[3].image, '/images/crew-cat.png', 'a self-hosted image path was not read: the /crew imagery is a file under website/, never an external host (F20, D107)')

  for (const url of ['https://www.youtube.com/watch?v=x', 'http://vimeo.com/1', '//cdn.example.com/a.mp4']) {
    const message = refusal({ change: only('/crew', { video: url }) })
    assert.match(message, /self-hosted/, `the video ${url} was accepted: campaign media is a self-hosted path the site serves, never an external URL — an embedded player would be a fourth third party the consent work never admitted (F20, D107) — the message was ${JSON.stringify(message)}`)
  }

  const escaping = refusal({ change: only('/crew', { video: '/../../etc/passwd' }) })
  assert.match(escaping, /self-hosted/, `the video "/../../etc/passwd" was accepted: a same-origin path under website/ is what a variant's media may name — the message was ${JSON.stringify(escaping)}`)
})

// ---- the hero's screen ---------------------------------------------------------------------

test('a variant screenshots the console\'s own capture, named, and a name no row carries is refused', () => {
  const landing = read({ change: only('/save', { screenshot: 'console-spend.webp' }) })

  assert.equal(
    landing.variants[0].screenshot,
    FACTS.screens[0],
    'a variant naming a row of website/data/console-screens.yaml was not read as that row: the hero\'s picture is the console screen the variant names, and the loader hands the page the data file\'s own row — its `src`, its alt text, its caption and its pixel size — so the page writes none of the four itself (the owner\'s instruction of 2026-10-30, D190)',
  )
  assert.equal(landing.variants[1].screenshot, null, 'a variant naming no screenshot was given another variant\'s screen: a variant with no screen renders the campaign\'s own drawing in the hero instead, which is what the null is for (F14, G2.5)')

  // The refusal: a file name the console's own data file carries no row for. This is the case the
  // instruction asks for first — the hero's picture is one of the four captures the site already
  // serves, never a new file a campaign brings with it.
  const unknown = refusal({ change: only('/save', { screenshot: 'console-savings.webp' }) })
  assert.match(unknown, /console-savings\.webp/, `a screenshot naming a file that website/data/console-screens.yaml carries no row for was accepted: the hero renders the row's own alt text and caption, and a name with no row is a picture nothing describes (the owner's instruction of 2026-10-30) — the message was ${JSON.stringify(unknown)}`)
  assert.match(unknown, /console-spend\.webp/, `the refusal does not name the screens the console\'s data file does carry, so the editor is not told what a variant may name — the message was ${JSON.stringify(unknown)}`)

  // A path, a URL or a traversal is not a name: the site path is composed from the file's name,
  // so a value that carries one is a second origin or a second spelling of the same file.
  for (const [value, why] of [
    ['/screenshots/console-spend.webp', 'a path'],
    ['https://example.com/console-spend.webp', 'a URL'],
    ['../screenshots/console-spend.webp', 'a traversal'],
  ]) {
    const message = refusal({ change: only('/save', { screenshot: value }) })
    assert.match(message, /NAME/, `a screenshot written as ${why} (${JSON.stringify(value)}) was accepted: the value is the file\'s name under website/public/screenshots/, and the site path is composed from the row it names (D190) — the message was ${JSON.stringify(message)}`)
  }
})

test('every variant in the shipped file shows the console screen its claim is proved by', () => {
  const landing = loadLanding()
  const rows = loadConsoleScreens().screens
  // The screen per variant, as the instruction of 2026-10-30 fixes them: the Spend screen where
  // the claim is what a run is priced at, the run detail where it is the review policy's ledger,
  // verification and findings, and the Live screen where the same console runs many at once.
  const expected = {
    save: 'console-spend.webp',
    control: 'console-run.webp',
    scale: 'console-live.webp',
    crew: 'console-live.webp',
  }

  for (const variant of landing.variants) {
    assert.equal(
      variant.screenshot?.file,
      expected[variant.name],
      `the ${variant.name} variant shows ${JSON.stringify(variant.screenshot?.file)} in its hero, and its claim is proved by ${JSON.stringify(expected[variant.name])}: the hero's picture is the screen the variant's own promise is about (the owner's instruction of 2026-10-30)`,
    )
    const row = rows.find((one) => one.file === variant.screenshot.file)
    assert.ok(row, `the ${variant.name} variant names ${variant.screenshot.file}, which no row of website/data/console-screens.yaml carries: the loader reads that file, so this case is the second half of the same rule`)
    assert.deepEqual(variant.screenshot, row, `the ${variant.name} variant's screen is not the row the console's data file carries: the hero renders the row's own alt text, caption and pixels, so the site's pages describe one picture once rather than describing it twice (D190)`)
  }

  assert.doesNotMatch(
    landingText(),
    /alt:|caption:|width:|height:/,
    'website/data/landing.yaml carries a screen\'s alt text, caption or pixel size: the console\'s own data file is where a screen is described, and the campaign file names it (D190)',
  )
})

// ---- the file's own shape -----------------------------------------------------------------

test('a path that is not one of the four message names is refused', () => {
  // The second variant is moved onto the first one's path *and* renamed with it, so the case is
  // about the duplicate route and not about the pre-fill message (which has its own case).
  const duplicate = refusal({ change: only('/control', { path: '/save', whatsapp: whatsappText('save') }) })
  assert.match(duplicate, /\/save/, `two variants carrying the path /save were accepted: a path is the route the page is built at, and one route is one page — the message was ${JSON.stringify(duplicate)}`)

  const prefixed = refusal({ change: only('/save', { path: '/landing/save' }) })
  assert.match(prefixed, /landing/, `the path "/landing/save" was accepted: the paths are the four message names, with no /landing prefix (spec §6) — the message was ${JSON.stringify(prefixed)}`)

  const unknown = refusal({ change: only('/save', { path: '/savings' }) })
  assert.match(unknown, /savings/, `the path "/savings" was accepted: the four campaigns are the spec's own four names, and a fifth path is a page no campaign sends anyone to — the message was ${JSON.stringify(unknown)}`)
})

test('a variant the spec does not campaign is refused as well as a missing one', () => {
  const missing = refusal({ variants: DEFAULT_VARIANTS.slice(0, 3) })
  assert.match(missing, /crew/, `a document carrying three of the four campaigns was accepted: the four pages are one campaign each, and the third path missing is what this refusal has to name — the message was ${JSON.stringify(missing)}`)

  const fifth = refusal({ append: '  - path: /landing/extra\n    headline: "x"\n    supporting_line: "y"\n    tone: extra\n    proof_number:\n      value: unmeasured\n      source: unmeasured\n    whatsapp_text: "Hi Dimi, I saw Fugaro on the extra page."' })
  assert.match(fifth, /extra/, `a fifth variant was accepted: the four pages are the spec's four campaigns, and the loader refuses a path outside them — the message was ${JSON.stringify(fifth)}`)
})

test('an unknown key is refused, at every level of the document', () => {
  const top = refusal({ append: 'campaign_id: 7' })
  assert.match(top, /campaign_id/, `an unknown top-level key was accepted: a fact nobody reads is a fact nothing pins, and the loader validates exactly the keys the pages render (G2.1) — the message was ${JSON.stringify(top)}`)

  const variant = refusal({ change: only('/save', { append: 'gallery: []' }) })
  assert.match(variant, /gallery/, `an unknown variant key was accepted: the loader refuses a key no page renders — the message was ${JSON.stringify(variant)}`)

  const proof = refusal({ change: only('/save', { proofNumber: { value: 'unmeasured', source: 'unmeasured', extra: 'x' } }) })
  assert.match(proof, /extra/, `an unknown proof_number key was accepted: the guard reads exactly the value and the source the file writes — the message was ${JSON.stringify(proof)}`)
})

test('a blank headline or supporting line is refused, never rendered as a hole', () => {
  const headline = refusal({ change: only('/save', { headline: '' }) })
  assert.match(headline, /headline/, `a variant with a blank headline was accepted: the hero prints the headline, and an empty one is a hero with nothing where the campaign's own copy goes — the message was ${JSON.stringify(headline)}`)

  const spaces = refusal({ change: only('/save', { headline: '   ' }) })
  assert.match(spaces, /headline/, `a headline of spaces was accepted: a value that is blank once trimmed is an empty headline with a different spelling — the message was ${JSON.stringify(spaces)}`)

  const supporting = refusal({ change: only('/save', { supportingLine: '' }) })
  assert.match(supporting, /supporting_line/, `a variant with a blank supporting line was accepted: the hero prints the headline with one line under it, and the file is the only place that line is written — the message was ${JSON.stringify(supporting)}`)
})

test('a WhatsApp message that does not name its own variant is refused', () => {
  const other = refusal({ change: only('/save', { whatsapp: whatsappText('scale') }) })
  assert.match(other, /save|scale/, `a pre-fill message naming another variant was accepted: the message names the page the visitor came from, which is the whole of why it is per variant (spec §6) — the message was ${JSON.stringify(other)}`)

  const unnamed = refusal({ change: only('/save', { whatsapp: 'Hi Dimi, I saw Fugaro.' }) })
  assert.match(unnamed, /save/, `a pre-fill message naming no variant was accepted: the loader reads the message against the spec's own pattern — the message was ${JSON.stringify(unnamed)}`)
})

test('the booking link and the number are validated where the file writes them', () => {
  const query = refusal({ calendly: 'https://calendly.com/dimi-fugaro/demo?utm_source=x' })
  assert.match(query, /calendly/, `a booking link carrying a query was accepted: the file writes the link itself, and the UTMs are G2.4's to add at the page — the message was ${JSON.stringify(query)}`)

  const host = refusal({ calendly: 'https://example.com/dimi' })
  assert.match(host, /calendly/, `a booking link on another host was accepted: the demo link is Calendly's own origin, which G2.3 has to admit to the CSP and G2.2 has to render — the message was ${JSON.stringify(host)}`)

  const plain = refusal({ calendly: 'http://calendly.com/dimi-fugaro/demo' })
  assert.match(plain, /https/, `a booking link over http was accepted: the page links an https origin — the message was ${JSON.stringify(plain)}`)

  const number = refusal({ number: '"+1 437 784 2426"' })
  assert.match(number, /digits/, `a number with spaces and a plus in it was accepted: the file carries the wa.me form, digits alone — the message was ${JSON.stringify(number)}`)

  const numeric = refusal({ number: '14377842426' })
  assert.match(numeric, /digits|string/, `an unquoted number was accepted: the value is a string of digits, so its YAML kind is not what carries it and a leading zero would survive a refresh — the message was ${JSON.stringify(numeric)}`)
})

test('a partner logo is a name and a self-hosted image, and a broken entry is refused', () => {
  // The name is plainly fake, as the plan's own conventions require: the file names no partner
  // until the owner has that partner's written permission (F12, R3).
  const landing = read({ logos: [{ name: 'TESTPARTNER', image: '/images/partners/testpartner.svg' }] })

  assert.deepEqual(
    landing.partnerLogos,
    [{ name: 'TESTPARTNER', image: '/images/partners/testpartner.svg' }],
    'a well-formed partner logo was not read: the list is what the social-proof section renders once a partner has given written permission (F11, F12, D405)',
  )

  const remote = refusal({ logos: [{ name: 'TESTPARTNER', image: 'https://example.com/logo.svg' }] })
  assert.match(remote, /self-hosted/, `a logo on another host was accepted: a partner's logo is self-hosted like the rest of the site's media (F20, D107) — the message was ${JSON.stringify(remote)}`)

  const unnamed = refusal({ logos: [{ image: '/images/partners/x.svg' }] })
  assert.match(unnamed, /name/, `a logo with no name was accepted: an entry that renders a picture nobody is named beside is not social proof — the message was ${JSON.stringify(unnamed)}`)

  const blank = refusal({ logos: [{ name: '""', image: '/images/partners/x.svg' }] })
  assert.match(blank, /named beside/, `a logo with a blank name was accepted: the section prints the partner's own name beside the logo, so an empty one is a picture nobody is named beside — the message was ${JSON.stringify(blank)}`)

  const spaces = refusal({ logos: [{ name: '"   "', image: '/images/partners/x.svg' }] })
  assert.match(spaces, /named beside/, `a logo whose name is spaces was accepted: a value that is blank once trimmed is a blank name with a different spelling — the message was ${JSON.stringify(spaces)}`)
})

// ---- the reader, and the file the loader opens --------------------------------------------

test('a trailing "# …" is a comment, not part of the value', () => {
  // The reader is `content.ts`'s own, and in it a `#` after whitespace starts a comment. That is
  // the whole point of importing it rather than writing a second one: a headline that swallowed
  // an editor's note would be printed with it on the page, and a quoted value followed by a note
  // would be refused with a message about a quote that closes perfectly well.
  const noted = document()
    .replace('    headline: "Same PRs, a tenth of the price."', '    headline: "Same PRs, a tenth of the price." # the spec §6 line')
    .replace('    tone: savings', "    tone: savings # the audience's register")
    .replace('      value: unmeasured\n      source: unmeasured', '      value: unmeasured # no figure published yet\n      source: unmeasured')
  const landing = parseLanding(noted, LANDING_FILE, FACTS)

  assert.equal(landing.variants[0].headline, SPEC[0].headline, `a comment after a quoted headline was read as part of it: the value is ${JSON.stringify(landing.variants[0].headline)}, and the page prints every character of it`)
  assert.equal(landing.variants[0].tone, 'savings', `a comment after a plain scalar was read as part of it: the tone came back as ${JSON.stringify(landing.variants[0].tone)}, and the page keys a class name off it`)
  assert.deepEqual(landing.variants[0].proofNumber, { value: 'unmeasured', source: 'unmeasured' }, 'a comment after the sentinel was read as part of it, so the variant that publishes no figure no longer reads as the sentinel (F6)')
})

test('a construct the site\'s reader does not understand is refused, never guessed at', () => {
  // An alias, an anchor, a tag and a flow mapping: `content.ts`'s reader refuses these four with
  // one message naming all of them (`… is a YAML alias, anchor, tag or flow mapping …`), so it
  // does not say which one it decided a value was. These cases pin what the reader does tell the
  // editor — that the value is one of the four, taken for text would render as `*spec_headline`,
  // `&spec_headline`, `!str …` or `{a: 1}`, and at which line it is — rather than a distinction the
  // message does not make.
  const headlineLine = '    headline: "Same PRs, a tenth of the price."'
  const CONSTRUCTS = ['*spec_headline', '&spec_headline', '!str spec_headline', '{a: 1}']

  for (const value of CONSTRUCTS) {
    const message = refusalOf(document().replace(headlineLine, `    headline: ${value}`))

    assert.match(message, /alias, anchor, tag or flow mapping/, `the value ${JSON.stringify(value)} was not refused as a construct the reader does not understand: a value it does not resolve is an error rather than a string that renders wrongly on the page — the message was ${JSON.stringify(message)}`)
    assert.match(message, /:7:/, `the refusal of ${JSON.stringify(value)} names no line, so the editor is not told where it is — the message was ${JSON.stringify(message)}`)
  }
})

test('a missing file, an empty file and a version this reader does not know are refused', (t) => {
  const root = mkdtempSync(path.join(tmpdir(), 'fugaro-landing-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))

  assert.throws(
    () => readLandingFile(root),
    /is missing/,
    'a website directory with no website/data/landing.yaml was read: the loader refuses a missing file rather than returning a document with no campaigns in it, which is four pages with no copy',
  )

  mkdirSync(path.join(root, 'data'), { recursive: true })
  writeFileSync(path.join(root, 'data', 'landing.yaml'), '')
  assert.throws(
    () => readLandingFile(root),
    /is empty/,
    'an empty website/data/landing.yaml was read: an empty file is four empty pages, and the build fails rather than rendering one',
  )

  // And the other half of the same state: a file carrying a comment and nothing else is not
  // empty, so it reaches the reader, which refuses it as a file with no document in it rather
  // than reading it as a document with no keys.
  const commentOnly = refusalOf('# the four campaigns, not written yet\n')
  assert.match(commentOnly, /carries no document/, `a landing.yaml carrying nothing but a comment was read: the message was ${JSON.stringify(commentOnly)}`)

  const version = refusalOf(document().replace('version: 1', 'version: 2'))
  assert.match(version, /version/, `a document written in a version this reader does not know was accepted: the reader knows version 1, and it says so rather than reading the newer file as if it were the one it understands — the message was ${JSON.stringify(version)}`)
})
