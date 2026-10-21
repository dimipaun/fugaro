// website/test/caps-and-costs.mjs — getting started, and the caps-and-costs guide
// (website plan, Task W1.5; reconciliation F22, F26, F27, F31, F33, F48, F50; D94,
// D96, D97; Global Constraints 4, 6, 8 and 11).
//
// The plan names four checks for the caps-and-costs page, and each of them is a
// mechanical form of a rule this work is held to:
//
//   1. **Every figure on the page is the one its own source carries.** This is
//      "publish the measurement, not the anecdote". The page has **one** source
//      since the owner's ruling of 2026-10-21: `website/data/typical-costs.yaml`,
//      the owner's own measurement of this project's development runs, which the
//      three job cards render. It may carry no figure that file did not produce —
//      which is why the spec's "~2 h" sentence never appears here (finding 33,
//      D97) — and the caps carry no figure at all: the reference's own usage lines
//      and one-line descriptions are the whole of what is rendered there.
//   2. **The data file is rendered exactly.** Every figure
//      `website/data/typical-costs.yaml` carries is on the page, in the field it is
//      for, and no figure on the page is one the file does not carry — so a refresh
//      of the figures is an edit to the file and never to a page, and a field added
//      to the file with no place to land fails here rather than going unrendered.
//   3. **The per-recipe ranges section stays removed** (the owner's ruling of
//      2026-10-21). It rendered `website/content/costs.json`, the owner-run
//      aggregation's output (W0.3), and that file is absent from this checkout, so
//      the band was an empty state on every page of the site. The removal is held
//      here rather than left to the next author: a page that grows the band back
//      has to do it deliberately, and `website/src/content.ts` still validates the
//      file the day one is published.
//   4. **The `fugaro budget` invocations match the CLI reference.** The caps
//      guide prints the command tree's own usage lines (W0.4's generated
//      reference) and carries no budget command of its own, so a flag can only
//      drift if the page stopped rendering the reference — which is what the
//      "no `fugaro budget` outside a rendered command" case catches.
//
// And two about getting started, whose wording W0.1's Go test pins to the
// repository: every command the page prints is one `website/data/install.yaml`
// carries, byte for byte, and the platform list is the file's own statuses —
// macOS and Linux supported, Windows untested and carrying its own note
// (the redesign's P4 cut the WSL sentence that used to be in it), never a
// platform a reader is offered (F20, D94).
//
// **Two things about this file are worth stating.**
//
// The costs file is **absent from this checkout**, and that is the honest state
// on day one: the aggregation reads one named installation's own run records,
// is refused inside a coding agent's session, and no run may read or publish an
// installation's data (D96, Global Constraint 10). Until the owner's ruling of
// 2026-10-21 the page rendered an empty state for it; the band is gone now, and
// what holds that is the case below rather than a fixture. The figures the page
// does render are `website/data/typical-costs.yaml`'s, a different source with a
// different life: hand-written, always present, and held to the case below.
//
// How it builds: like `layout.mjs`, a copy of the repository in a temp
// directory, with the installed `node_modules` symlinked rather than copied, so
// a test can remove a page's facts and build again. The pages under test are the
// real ones this task adds; every build is network-free and nothing here starts
// anything but the site's own Astro.
//
// **Red step** (the plan's own, recorded here as the plan requires — the exact
// line the case below printed before the page existed): with `caps-and-costs.astro`
// holding the spec's own sentence — `<p>Tasks with review often run ~2 h, not 30
// min.</p>` — and the case run against it,
// `node --test --test-name-pattern='renders exactly website/data/typical-costs.yaml'
// test/caps-and-costs.mjs` said
//
//   Expected: FAIL — the page states "Tasks with review often run ~2 h, not 30 min.",
//   which is not a figure website/data/typical-costs.yaml carries
//
// The sentence was removed rather than the case: the defect is a page that prints a
// figure no file behind it produced, which is finding 33 and the reason the plan's §5
// review row lists this page (F50, D97).
//
// **And the red steps for the figures themselves.** The case that holds the page to
// `website/data/typical-costs.yaml` was written, and then each defect it exists for
// was planted in `src/components/CapsCosts.astro` and the case run against it:
//
//   - `money()` rounded to the whole dollar (`$${Math.round(value)}`) — the case
//     failed with "the page does not state $10.00, which
//     website/data/typical-costs.yaml carries", the file's `10` printed as `$10`;
//   - one figure given a value of its own (`{'$1.00'}` for the nine-in-ten row) —
//     "card task: … | $1.00 | … (the file carries … | $1.27 | …)", two cards named;
//   - the middle-half row dropped (`{'—'}` in place of the range) — "card task:
//     … | — | … (the file carries … | $0.15–$0.68 | …)";
//   - the window line deleted — "the page does not state where its figures come
//     from, or over what window".
//
// Each was removed and the case passed again. A figure the page writes of its own is
// a figure no file behind it carries.
//
// **The bold redesign of 2026-10-21 rewrote this case's reader, and the rewrite was
// proved the same way.** The table became three job cards, so the case reads cards:
// `data-job` in the file's own order, the card's `<h3>` for the name, its runs line,
// its median and its `<dl>` of labelled rows. Planted in the new markup, each of the
// four defects above still fails the case with the card named, and so does the one
// the cards made possible — a `<dt>` and its `<dd>` swapped, which is a figure under
// the wrong name and is the defect the table's fixed column order used to make
// impossible.
//
// **The cap's sentence is the callout's now, and it says whose cap it is.** The
// redesign gave the per-run cap a card of its own ("Our per-run cap", `$10.00`)
// instead of a clause in the window line, because `--per-run` defaults to 0, which
// sets no cap, and an enforcing project with no per-run cap anywhere refuses every
// lease: the figure is Fugaro's installation's and not something `fugaro` ships — the
// same correction `data/typical-costs.yaml`'s header and `src/content.ts`'s field doc
// carry. The case holds the callout to the file's own figure. The refusals the
// figures' reader owes are `layout.mjs`'s subject rather than this file's: they are
// shapes of the data file, and that file is where they are planted. The page's word
// count is 264 of D188's 949 and its lead is 25 of the 40 the same decision allows.
//
// **The second review's finding, and the red step behind the change it made.** The page
// prints a figure to the cent with `toFixed(2)`, the loader accepted any finite number,
// and `money()` below rounded the file's figure the same way — so a file carrying a third
// decimal was rounded on the page and passed *both* directions of this case, against the
// file's own promise that a figure is never rounded there. Planted, with `median_usd: 0.36`
// written `0.365` in `website/data/typical-costs.yaml`:
//
//   $ node --test --test-name-pattern='renders exactly website/data/typical-costs.yaml' test/caps-and-costs.mjs
//   ✔ the page renders exactly website/data/typical-costs.yaml, in the field each figure is for
//   ℹ pass 1   ℹ fail 0
//
// — green, with the page printing `$0.36` for the file's `0.365` (`(0.365).toFixed(2)` is
// `"0.36"`). The fix is at the source rather than at the print: `src/content.ts`'s `asCents`
// refuses a figure that is not a whole number of cents, so `money()` in the page can only
// pad, and the `money()` here prints the digits the file itself carries instead of rounding
// them. With the same fixture planted the build refuses the row, which is what the change
// to `layout.mjs`'s refusal cases holds.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { READ_UNDER_PLUGIN, linkNodeModules } from './lib/build.mjs'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)

// What a site build cannot read, and therefore what the copies below leave out:
// the Go module's own tree and test data, the console application, and the
// installed and built directories (the last two are symlinked and rebuilt).
const SKIP = new Set(['.git', 'internal', 'cmd', 'console', 'schemas', 'deploy', 'testdata'])
const SKIP_UNDER_WEBSITE = new Set(['node_modules', 'dist', '.astro'])

/**
 * The figures a costs file holds, in W0.3's own `--json` shape: two groups, each
 * above D97's minimum of 20 runs, and the disclaimer the command publishes them
 * with. `internal/cli/stats.go`'s `statsDoc` is the shape this mirrors — the
 * contract is that one, and `website/src/content.ts` validates the file against
 * it before a page renders a figure from it.
 *
 * Nothing here is data about anybody's runs: it is a fixture, it exists for the
 * length of one build in a temp directory, and the site's own published file is
 * the owner's. The disclaimer carries "0.9" on purpose: the page has to render
 * it verbatim, so a number the JSON carries must pass the first case unchanged —
 * which is how the case tells "rendered" from "rounded".
 */
const FIGURES = {
  project: 'sandbox',
  since: '2026-01-01',
  until: '2026-02-01',
  min_runs: 20,
  recipes: [
    {
      recipe: 'standard',
      runs: 40,
      compute_not_estimated: 2,
      duration_s: { min: 60, median: 120.5, p90: 240 },
      cost_usd: { min: 0.1, median: 0.2, p90: 0.3 },
    },
    {
      recipe: 'review-heavy',
      runs: 24,
      compute_not_estimated: 0,
      duration_s: { min: 900, median: 3600, p90: 7200 },
      cost_usd: { min: 1.5, median: 3.25, p90: 7.5 },
    },
  ],
  totals: { runs: 64, recipes: 2, runs_below_min_runs: 3, runs_unknown_recipe: 1, runs_not_estimated: 2, runs_without_a_figure: 1 },
  not_a_promise:
    'A cap is a ceiling, not an estimate: in observe a run counts and logs what it spent, and in enforce it halts. ' +
    "These figures are percentiles over one window of one installation's own run records: what those runs did, not a promise about another run. " +
    "Every figure prints the number of runs behind it, and the p90 whenever 0.9 times (runs minus one) is a whole number.",
}

/** A copy of the repository in a temp directory, ready to be mutated and built. */
function copyOfTheRepository() {
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-caps-'))
  const root = path.join(dir, 'repo')
  cpSync(REPO, root, {
    recursive: true,
    filter: (src) => {
      const name = path.basename(src)
      const parent = path.basename(path.dirname(src))
      if (path.dirname(src) === REPO && SKIP.has(name)) return false
      // Which parts of `plugin/` a build reads, and why each one: READ_UNDER_PLUGIN's
      // own comment (website/test/lib/build.mjs).
      if (parent === 'plugin' && !READ_UNDER_PLUGIN.has(name)) return false
      if (parent === 'website' && SKIP_UNDER_WEBSITE.has(name)) return false
      return true
    },
  })
  linkNodeModules(path.join(root, 'website', 'node_modules'))
  return { dir, root, site: path.join(root, 'website') }
}

/** Build the copy, and hand back what the build said and the pages it produced. */
function build(copy) {
  const result = spawnSync(process.execPath, [path.join(copy.site, 'node_modules', 'astro', 'bin', 'astro.mjs'), 'build'], {
    cwd: copy.site,
    encoding: 'utf8',
    env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' },
  })
  return {
    status: result.status,
    output: `${result.stdout ?? ''}${result.stderr ?? ''}`,
    page(route) {
      const file = path.join(copy.site, 'dist', route.replace(/^\//, '').replace(/\/$/, ''), 'index.html')
      return existsSync(file) ? readFileSync(file, 'utf8') : null
    },
  }
}

/** Build a fresh copy, mutate one file, build again, and clean up after the test. */
function buildWith(t, mutate) {
  const copy = copyOfTheRepository()
  t.after(() => rmSync(copy.dir, { recursive: true, force: true }))
  mutate(copy)
  return build(copy)
}

/** Write a costs file into the copy, as the owner's own run of the command does. */
function publish(figures) {
  return (copy) => writeFileSync(path.join(copy.site, 'content', 'costs.json'), JSON.stringify(figures))
}

/** The built page, for a test that expects the build to have succeeded. */
function builtPage(built, route) {
  assert.equal(built.status, 0, `the build failed:\n${built.output}`)
  const page = built.page(route)
  assert.ok(page, `the build produced no page at ${route}`)
  return page
}

/** The bytes of a file in the checkout, for a test to hold the build against. */
function repoFile(relative) {
  return readFileSync(path.join(REPO, relative), 'utf8')
}

/** HTML entities a template writes, read back as the reader reads them. */
function decode(text) {
  return text
    .replace(/&#x([0-9a-fA-F]+);/g, (_, hex) => String.fromCodePoint(parseInt(hex, 16)))
    .replace(/&#(\d+);/g, (_, dec) => String.fromCodePoint(Number(dec)))
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&amp;/g, '&')
}

/** The visible text of a built page: no head, no script, no style, no tags. */
function textOf(html) {
  return decode(
    html
      .replace(/<head[\s\S]*?<\/head>/gi, ' ')
      .replace(/<script[\s\S]*?<\/script>/gi, ' ')
      .replace(/<style[\s\S]*?<\/style>/gi, ' ')
      .replace(/<[^>]+>/g, ' '),
  )
}

/** A string with its whitespace flattened, for comparisons across markup. */
function collapse(text) {
  return text.replace(/\s+/g, ' ').trim()
}

// A number as a reader sees one: `(?<![\w.])` keeps `p90`'s digits and the `9`
// of a `0.9` out of the set, so what is compared is what is written as a figure.
const NUMBER = /(?<![\w.])\d+(?:\.\d+)?/g

/** Every number written anywhere in a costs file, however it is spelled there. */
function numbersOf(figures) {
  const allowed = new Set(JSON.stringify(figures).match(NUMBER) ?? [])
  const walk = (value) => {
    if (typeof value === 'number') allowed.add(String(value))
    else if (Array.isArray(value)) value.forEach(walk)
    else if (value && typeof value === 'object') Object.values(value).forEach(walk)
  }
  walk(figures)
  return allowed
}

// A block of the page's own text as a reader reads it: the failure message for a
// figure the costs file does not carry is the sentence that stated it, and a
// block boundary is where a sentence ends on a page.
const BLOCK = /<\/?(?:p|li|ul|ol|h[1-6]|table|thead|tbody|tr|td|th|dd|dt|dl|section|article|header|footer|nav|main|div|pre|blockquote)\b[^>]*>/gi

/** Every block of a built page's own text, in the order a reader meets them. */
function blocksOf(html) {
  return html
    .split(BLOCK)
    .map((chunk) => textOf(chunk))
    .filter((text) => text.trim() !== '')
}

/** The numbers a string writes, as a reader reads them. */
function numbersIn(text) {
  return [...String(text).matchAll(NUMBER)].map((match) => match[0])
}

/** The built page split into the regions the page's own two bands fill. */
function splitPage(page) {
  const section = (id) => {
    const match = new RegExp(`<section id="${id}"[\\s\\S]*?</section>`).exec(page)
    assert.ok(match, `the page has no ${id} section`)
    return match[0]
  }
  const typical = section('typical-costs')
  const caps = section('caps')
  return {
    typical,
    caps,
    // Everything else: the hero — the pill, the headline, the lead, the two calls to action
    // and the two mode cards — which writes no figure at all.
    rest: page.replace(typical, ' ').replace(caps, ' '),
  }
}

/**
 * `website/data/typical-costs.yaml`, read the way the page's own reader reads it: the
 * window's two dates and its count, one entry per job with the fields the table renders, and
 * the per-run cap. The reader is deliberately literal — it is this gate's, and a field the
 * file renames is a field the case below stops finding.
 */
function readTypicalCosts() {
  const out = { window: {}, jobs: [], perRunCap: null }
  let job = null
  for (const line of repoFile('website/data/typical-costs.yaml').split('\n')) {
    if (line.trim() === '' || line.trim().startsWith('#')) continue
    let match
    if ((match = /^ {2}(from|to|runs): (\S+)$/.exec(line))) out.window[match[1]] = match[2]
    else if ((match = /^ {2}- id: (\S+)$/.exec(line))) out.jobs.push((job = { id: match[1] }))
    else if ((match = /^ {4}([a-z_]+): (.+)$/.exec(line))) job[match[1]] = match[2].trim()
    else if ((match = /^per_run_cap_usd: (\S+)$/.exec(line))) out.perRunCap = match[1]
  }
  assert.ok(out.jobs.length > 0, 'website/data/typical-costs.yaml carries no job for the page to render')
  assert.ok(out.window.from && out.window.to && out.window.runs, 'website/data/typical-costs.yaml carries no window for the page to print')
  assert.ok(out.perRunCap !== null, 'website/data/typical-costs.yaml carries no per-run cap for the page to print')
  return out
}

/**
 * A figure as the page prints it: the digits `website/data/typical-costs.yaml` itself carries,
 * padded to cents and never rounded. Padding is all the page does with them — the loader
 * refuses a figure that is not a whole number of cents (`asCents` in `src/content.ts`), so a
 * figure this function had to round would be one the page cannot print — and re-applying the
 * page's own rounding here would be the gate agreeing with the defect it exists for: a file
 * carrying `0.365` printed as `$0.36` passed both directions while this function rounded too.
 */
function money(value) {
  const [whole, fraction = ''] = String(value).trim().split('.')
  assert.ok(
    /^\d+$/.test(whole) && fraction.length <= 2,
    `website/data/typical-costs.yaml carries ${value}, which is not a whole number of cents for the page to print without rounding it`,
  )
  return `$${whole}.${fraction.padEnd(2, '0')}`
}

/** The middle half as the page prints it: a range, or a dash where the file carries none. */
function middleHalf(value) {
  if (value === undefined) return '—'
  const [low, high] = value
    .replace(/^\[|\]$/g, '')
    .split(',')
    .map((part) => part.trim())
  return `${money(low)}–${money(high)}`
}

/**
 * Every figure `website/data/typical-costs.yaml` carries, spelled the way the page prints it:
 * the window's two dates and its count, the per-run cap, and each job's sample, median,
 * middle half and worst. A figure the page prints that is not in this set is one the file does
 * not carry, which is the direction that makes a refresh an edit to the file and never to a
 * page (D96, D97).
 */
function figuresOf(file) {
  const allowed = new Set()
  const allow = (text) => numbersIn(text).forEach((number) => allowed.add(number))
  for (const value of [file.window.from, file.window.to, file.window.runs]) allow(value)
  allow(`${file.perRunCap} ${money(file.perRunCap)}`)
  for (const job of file.jobs) {
    allow(`${job.runs} ${money(job.median_usd)} ${money(job.nine_in_ten_under_usd)}`)
    if (job.middle_half_usd !== undefined) allow(middleHalf(job.middle_half_usd))
    if (job.worst_usd !== undefined) allow(money(job.worst_usd))
  }
  return allowed
}

/**
 * The job cards the figures band renders, each as its own bytes and the id it carries. The
 * split is on `data-job`, which only a job card carries: the card's own class list may change
 * without this reader losing a card, and a card the page stops rendering is a card this reader
 * stops seeing, which is the direction the case below reads.
 */
function jobCards(typical) {
  const parts = typical.split(/<li [^>]*\bdata-job="([^"]+)"/).slice(1)
  const out = []
  for (let i = 0; i < parts.length; i += 2) {
    const body = parts[i + 1]
    out.push({ id: parts[i], html: body.slice(0, body.indexOf('</li>')) })
  }
  return out
}

/**
 * The text of the one element of a card carrying a class, or null when the card has none. The
 * closing tag is read with its digits (`</h3>` is not `</[a-z]+>`): a reader that missed them
 * would take the next closing tag it could match instead, and read a card's name and its runs
 * line as one cell.
 */
function figureOf(card, klass) {
  const match = new RegExp(`<[a-z][a-z0-9]*[^>]*class="[^"]*\\b${klass}\\b[^"]*"[^>]*>([\\s\\S]*?)<\\/[a-z][a-z0-9]*>`).exec(card)
  return match === null ? null : collapse(textOf(match[1]))
}

/** The `<dt>`/`<dd>` pairs of a card's `<dl>`, in the order the card prints them. */
function rowsOf(card) {
  return [...card.matchAll(/<dt[^>]*>([\s\S]*?)<\/dt>\s*<dd[^>]*>([\s\S]*?)<\/dd>/g)].map((match) => [
    collapse(textOf(match[1])),
    collapse(textOf(match[2])),
  ])
}

test('the page renders exactly website/data/typical-costs.yaml, in the field each figure is for', async (t) => {
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')
  const { typical } = splitPage(page)
  const file = readTypicalCosts()
  const strays = []

  // One card per job the file holds, in the file's own order, and every figure the field it
  // is for: a figure the page rounds, drops or invents is a card that does not match, and so
  // is a job the file carries with no card to render it.
  const cards = jobCards(typical)
  assert.deepEqual(
    cards.map((card) => card.id),
    file.jobs.map((job) => job.id),
    "the page does not render the jobs website/data/typical-costs.yaml carries, in the file's own order",
  )
  cards.forEach((card, index) => {
    const job = file.jobs[index]
    // The card's name, its runs and its median, then its labelled rows: the label is read off
    // the card and held to the name the row is for, so a `<dt>` and its `<dd>` swapped is a
    // figure under the wrong name rather than a row this reader cannot see.
    const expected = [
      ['the name', job.name, figureOf(card.html, 'caps-job-name')],
      ['the runs', job.runs, figureOf(card.html, 'caps-job-runs-value')],
      ['the median', money(job.median_usd), figureOf(card.html, 'caps-job-median-value')],
      ['Middle half', middleHalf(job.middle_half_usd), null],
      ['9 in 10 under', money(job.nine_in_ten_under_usd), null],
      ['Worst', job.worst_usd === undefined ? '—' : money(job.worst_usd), null],
    ]
    const rows = rowsOf(card.html)
    const printed = [
      expected[0][2],
      expected[1][2],
      expected[2][2],
      ...expected.slice(3).map(([label, , ], row) => {
        const found = rows[row]
        if (found === undefined) return null
        return found[0] === label ? found[1] : `${found[0]} = ${found[1]}`
      }),
    ]
    if (printed.some((cell, at) => cell !== expected[at][1])) {
      strays.push(
        `card ${job.id}: ${printed.map((cell) => cell ?? '(nothing)').join(' | ')} (the file carries ${expected.map(([, wanted]) => wanted).join(' | ')})`,
      )
    }
  })

  // The band's window line states how old the figures are: the file's own two dates and the
  // number of runs behind them, each one a value the file carries rather than a sentence
  // written beside it.
  const window = blocksOf(typical)
    .map(collapse)
    .find((block) => block.startsWith("Fugaro's own runs"))
  assert.ok(window, 'the page does not state where its figures come from, or over what window')
  for (const value of [file.window.from, file.window.to, file.window.runs]) {
    assert.ok(window.includes(value), `the page's window line does not state ${value}, which website/data/typical-costs.yaml carries`)
  }

  // And the per-run cap, which the band states in the callout of its own — the figure is the
  // installation's, and the callout is where the page says whose it is.
  const cap = /<p[^>]*\bdata-cap\b[^>]*>([\s\S]*?)<\/p>/.exec(typical)
  assert.ok(cap, 'the page prints no per-run cap, and the file carries one')
  assert.ok(
    collapse(textOf(cap[1])).includes(money(file.perRunCap)),
    `the page's cap callout does not state ${money(file.perRunCap)}, which website/data/typical-costs.yaml carries`,
  )

  // And no figure in the band is one the file does not carry — the direction that makes a
  // refresh an edit to the file. The labels come off first: a `<dt>` names the statistic ("9 in
  // 10 under" is the name of a figure, not a figure), the same removal the table's head needed,
  // and the rows above are compared label by label in any case.
  const allowed = figuresOf(file)
  const withoutLabels = typical.replace(/<dt[^>]*>[\s\S]*?<\/dt>/g, ' ')
  for (const block of blocksOf(withoutLabels).map(collapse)) {
    if (numbersIn(block).some((number) => !allowed.has(number))) strays.push(block)
  }

  assert.deepEqual(
    [...new Set(strays)],
    [],
    `the page states ${[...new Set(strays)].map((s) => JSON.stringify(s)).join(', ')}, which is not what website/data/typical-costs.yaml carries (D97: the page publishes the measurement, and no figure of its own)`,
  )
})

test('the page writes no figure outside the band that holds its own file\'s', async (t) => {
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')
  const { caps, rest } = splitPage(page)
  const strays = []

  // Outside the figures band — the hero, and the caps, where the reference's usage lines and
  // its one-line descriptions are the whole of what is rendered — no figure is written at all:
  // a typed "Runs often cost 2 USD." in the lead would pass a check that allowed any number the
  // fixture holds anywhere. (The band belongs to the page's own file, and the case above holds
  // it to that.)
  for (const block of [...blocksOf(caps), ...blocksOf(rest)]) {
    if (numbersIn(block).length > 0) strays.push(collapse(block))
  }

  assert.deepEqual(
    [...new Set(strays)],
    [],
    `the page states ${[...new Set(strays)].map((s) => JSON.stringify(s)).join(', ')}, outside the band that holds website/data/typical-costs.yaml's own figures — the hero and the caps write no figure of their own (D97)`,
  )
})

test('the per-recipe ranges band is gone, and the aggregation\'s file is still read', async (t) => {
  // The owner's ruling of 2026-10-21: the band rendered `website/content/costs.json`, the
  // owner-run aggregation's output (W0.3), and that file is absent from this checkout — so the
  // band was an empty state on every page of the site, and a band that says nothing is worse
  // than a band that is not there. The removal is held here rather than left to the next
  // author: the case builds a page with a fixture file published and reads the page for the
  // band, so a page that grows it back has to do it deliberately and say so.
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')
  const text = collapse(textOf(page))

  assert.ok(!/<section[^>]*id="costs"/.test(page), 'the page carries the per-recipe ranges band again: it was removed until there is data (the owner\'s ruling of 2026-10-21)')
  assert.ok(!page.includes('data-recipe'), 'the page renders a per-recipe figure, and the band that rendered them was removed')
  // The aggregation's own disclaimer is a sentence of the band, so with the band gone no
  // sentence of it is on the page. Read sentence by sentence rather than as the whole
  // paragraph: the line is three sentences long and a page that rendered one of them would be
  // rendering the aggregation's own rule beside its own copy — the drift D97 exists against —
  // while a check for the whole string would call that page clean.
  for (const sentence of FIGURES.not_a_promise.split(/(?<=\.)\s+/)) {
    assert.ok(
      !text.includes(collapse(sentence)),
      `the page renders ${JSON.stringify(sentence)}, which is a sentence of the aggregation's own "not a promise" line: it belongs to the band that was removed`,
    )
  }
  for (const group of FIGURES.recipes) {
    assert.ok(
      !text.includes(group.recipe),
      `the page names the aggregation's recipe ${JSON.stringify(group.recipe)}, and no page publishes an aggregation figure while the band is removed`,
    )
  }

  // The file is still validated: a group below D96's minimum fails the build rather than being
  // published, which is the fence that stays whether or not a page renders the figures.
  const thin = buildWith(t, publish({ ...FIGURES, recipes: [{ ...FIGURES.recipes[0], recipe: 'rare', runs: 4 }] }))
  assert.notEqual(thin.status, 0, 'the build published a group of 4 runs, below the minimum of 20')
  assert.match(thin.output, /costs\.json/, 'the failure does not name the file the figures came from')

  // And with no aggregation file behind the page at all — this checkout's own state — the only
  // figures a reader reads are the data file's: the band's removal is not a licence for a
  // figure of the page's own. The labels come off first, as they do in the case above.
  const plain = buildWith(t, () => {})
  const plainPage = builtPage(plain, '/caps-and-costs/')
  const allowed = figuresOf(readTypicalCosts())
  const withoutLabels = plainPage.replace(/<dt[^>]*>[\s\S]*?<\/dt>/g, ' ')
  const strays = [...new Set(blocksOf(withoutLabels).map(collapse))].filter((block) =>
    numbersIn(block).some((number) => !allowed.has(number)),
  )
  assert.deepEqual(
    strays,
    [],
    `the page writes ${strays.map((s) => JSON.stringify(s)).join(', ')}, which no file behind this page carries: the only figures on it are website/data/typical-costs.yaml's`,
  )
})

test('the fugaro budget invocations match the CLI reference', async (t) => {
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')

  // The reference is W0.4's generated document, and its usage lines are the
  // commands' own `Use` strings: what the page prints is what `fugaro --help`
  // prints, so a flag cannot drift between the guide and the command tree it
  // describes (finding 26, W0.4).
  const reference = repoFile('website/content/cli-reference.md')
  const usages = [...reference.matchAll(/^Usage: `(fugaro budget[^`]*)`$/gm)].map((match) => match[1])
  assert.ok(usages.length > 0, 'the CLI reference carries no `fugaro budget` usage line to render')

  const printed = [...page.matchAll(/data-command="([^"]*)"/g)].map((match) => decode(match[1]))
  assert.ok(printed.length > 0, 'the caps page prints no budget command')
  for (const command of printed) {
    assert.ok(usages.includes(command), `the page prints ${JSON.stringify(command)}, which is not a usage line the CLI reference carries:\n  ${usages.join('\n  ')}`)
  }

  // The guide covers the caps, what is set now, and both halves of the kill
  // switch — a caps page that lost one of them would still pass the check above.
  const covered = ['fugaro budget set', 'fugaro budget show', 'fugaro budget kill', 'fugaro budget resume']
  for (const command of covered) {
    assert.ok(
      printed.some((line) => line.startsWith(command)),
      `the caps guide prints no invocation of ${JSON.stringify(command)}, so one of the caps it describes has no command beside it`,
    )
  }

  // And no budget command is written beside the rendered ones: every `fugaro
  // budget` string on the page is inside an element the reference filled, so the
  // page cannot carry a command of its own that the check above never sees.
  const withoutRendered = page.replace(/<code[^>]*data-command="[^"]*"[^>]*>[\s\S]*?<\/code>/g, ' ')
  assert.ok(!textOf(withoutRendered).includes('fugaro budget'), 'the caps page writes a `fugaro budget` command of its own instead of rendering the reference\'s')
})

test('the caps guide covers every budget subcommand the CLI reference carries', async (t) => {
  // The page says it prints the whole of the command's interface, and a claim of
  // coverage is the F50 class of defect when nothing backs it: a subcommand the
  // reference documents and the guide omits — `preview` and `prices` were exactly
  // that — leaves a reader with a command they cannot find and a page that says
  // there is nothing else. The reference is generated from the command tree, so
  // the tree is the list this page owes.
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')

  const reference = repoFile('website/content/cli-reference.md')
  const subcommands = [...reference.matchAll(/^## `(fugaro budget \S+)`$/gm)].map((match) => match[1])
  assert.ok(subcommands.length > 1, `the CLI reference carries ${subcommands.length} budget subcommand heading(s), which is too few to be the interface this page covers`)

  const rendered = [...page.matchAll(/data-guide="([^"]*)"/g)].map((match) => decode(match[1]))
  for (const path of subcommands) {
    assert.ok(
      rendered.includes(path),
      `the CLI reference carries \`${path}\` and the caps guide renders no row for it: the page says it prints the whole of the interface, so an omitted subcommand makes that sentence false`,
    )
  }
})

test('the aggregation has not been run, which is the premise the band\'s removal rests on', () => {
  // The aggregation is owner-run and no run of this repository may read an installation's
  // records (D96, Global Constraint 10), so `website/content/costs.json` is not in this
  // checkout — and that is the whole reason the per-recipe ranges band is gone (the owner's
  // ruling of 2026-10-21). The case holds the premise rather than the conclusion: the day the
  // file is published, the band is a decision to make again, with figures in hand and the
  // minimum-sample fence of the case above around them, instead of a page that quietly grows
  // an empty section back.
  const file = path.join(REPO, 'website', 'content', 'costs.json')
  assert.ok(
    !existsSync(file),
    'website/content/costs.json is committed: it is the owner-run aggregation\'s own output, and the ranges band removed on 2026-10-21 was removed because that file is absent — with the file published, whether the band comes back is the owner\'s decision and not this page\'s (W2.3 is the task that publishes the file with check 69c)',
  )
})

test('getting started renders the install commands from install.yaml, and no command of its own', async (t) => {
  // The commands are install.yaml's (W0.1), which the Go test pins to README.md
  // and docs/release.md byte for byte: the page renders them, so the copy most
  // likely to be read cannot drift from the copy the release process prints
  // (finding 21, F37, Global Constraint 6).
  const built = buildWith(t, () => {})
  const page = builtPage(built, '/getting-started/')

  const install = repoFile('website/data/install.yaml')
  const commands = [...install.slice(install.indexOf('\npaths:')).matchAll(/- \|-\n((?: {8}[^\n]*\n?)+)/g)].map((match) =>
    collapse(
      match[1]
        .split('\n')
        .map((line) => line.slice(8))
        .join('\n'),
    ),
  )
  assert.ok(commands.length >= 5, `install.yaml carries ${commands.length} commands, which is fewer than the install paths this page renders`)

  const printed = [...page.matchAll(/<pre[^>]*>([\s\S]*?)<\/pre>/g)].map((match) => collapse(decode(match[1].replace(/<[^>]+>/g, ''))))
  assert.deepEqual(printed, commands, 'the commands the page prints are not exactly install.yaml\'s, in its own order')
})

test('the setup steps print the plugin commands install.yaml carries, and no spelling of their own', async (t) => {
  // The `#setup` band's last two steps tell a reader to run `fugaro setup` and `/fugaro:setup`,
  // which are the plugin path's own commands in install.yaml — the file the page renders every
  // other command from. A step is one line of prose and the commands in it looked like prose,
  // so they were typed into the page until this case existed: a rename in install.yaml moved
  // #details' plugin path and left the steps above it telling a reader to run a command the
  // file no longer carries. The page renders them from the path now, and this is what holds
  // the two together — every `<code>` in the band, in order, is install.yaml's own text.
  const built = buildWith(t, () => {})
  const page = builtPage(built, '/getting-started/')

  const install = repoFile('website/data/install.yaml')
  const plugin = install.slice(install.indexOf('- id: plugin'))
  const commands = [...plugin.matchAll(/- \|-\n((?: {8}[^\n]*\n?)+)/g)].map((match) =>
    collapse(
      match[1]
        .split('\n')
        .map((line) => line.slice(8))
        .join('\n'),
    ),
  )
  assert.ok(commands.length >= 2, `install.yaml's plugin path carries ${commands.length} command(s), which is too few to be the two the setup steps name`)
  const [installCommand, agentCommand] = commands

  const band = /<section id="setup"[\s\S]*?<\/section>/.exec(page)
  assert.ok(band, 'Getting Started carries no `#setup` section, and the page is three bands (the owner\'s brief of 2026-10-19)')
  const printed = [...band[0].matchAll(/<code[^>]*>([\s\S]*?)<\/code>/g)].map((match) => collapse(decode(match[1].replace(/<[^>]+>/g, ''))))
  assert.deepEqual(
    printed,
    [installCommand, agentCommand, installCommand],
    `the setup steps print ${JSON.stringify(printed)} where install.yaml's plugin path carries ${JSON.stringify(commands)}: the steps render the file's own commands, so a command renamed there moves the steps with it, and a step that spells one itself is the fourth copy of the install instructions this page refuses (Global Constraint 6)`,
  )
})

test('getting started promises commands only where install.yaml carries them', async (t) => {
  // install.yaml is allowed to carry a path with no commands — the release archive,
  // whose facts are a Releases page and a name template rather than a command line —
  // and a page that printed a citation line above that path's empty list would be
  // telling a reader to read something that is not there. The line and the blocks are
  // the same fact, so the page renders both or neither.
  //
  // The citation line is the redesign's (P4): it used to read "Commands as printed in
  // <file>." and now reads "From <file> · <platforms>", with the per-path platform line
  // folded into it. What this case holds is the invariant, not the wording: one marker,
  // rendered exactly where a command block is.
  const built = buildWith(t, () => {})
  const page = builtPage(built, '/getting-started/')

  // The opening tag is read as `<article …>` rather than as `<article data-path="…">`: the
  // cards the owner's draft of 2026-10-19 put in `#install` carry their class and their
  // platform before the path, and a regex pinned to the old attribute order would stop seeing
  // the two paths those cards hold — one of which is the no-commands shape this case is about.
  const articles = [...page.matchAll(/<article[^>]*data-path="([^"]+)"[\s\S]*?<\/article>/g)].map((match) => ({
    id: decode(match[1]),
    block: match[0],
  }))
  assert.ok(articles.length > 0, 'the getting-started page renders no install path at all')

  let withCommands = 0
  for (const article of articles) {
    const hasCommands = /<pre[^>]*>/.test(article.block)
    const promises = /<p class="source"[^>]*>/.test(article.block)
    if (hasCommands) withCommands++
    assert.equal(
      promises,
      hasCommands,
      `install path \`${article.id}\` ${promises ? 'cites the file its commands come from' : 'cites no file'} and renders ${hasCommands ? 'command blocks' : 'none'}: the line and the blocks are one fact, and a path with no commands cites nothing`,
    )
  }

  // Both shapes are in the file, or this check is only looking at one of them.
  assert.ok(withCommands > 0, 'no install path carries a command, so install.yaml is not the file this page renders')
  assert.ok(withCommands < articles.length, 'every install path carries commands, so the no-commands shape install.yaml allows is not exercised')
})

test('getting started offers the platforms install.yaml marks, and states Windows as untested', async (t) => {
  // D94's answer: macOS and Linux are supported, Windows is untested and carries
  // its own note — a note and never a platform a reader is offered, because this
  // repository builds no Windows binary (finding 20).
  const built = buildWith(t, () => {})
  const page = builtPage(built, '/getting-started/')

  const install = repoFile('website/data/install.yaml')
  const platforms = [...install.matchAll(/^ {2}- id: (\S+)\n {4}name: (.+)\n {4}goos: \S+\n {4}status: (\S+)/gm)].map((match) => ({
    id: match[1],
    name: match[2].trim(),
    status: match[3],
  }))
  assert.ok(platforms.length > 0, 'install.yaml carries no platform to render')

  const text = collapse(textOf(page))
  for (const platform of platforms) {
    assert.match(
      page,
      new RegExp(`data-platform="${platform.id}" data-status="${platform.status}"`),
      `the page does not render ${platform.name} with the status install.yaml writes (${platform.status})`,
    )
  }

  const windows = platforms.find((platform) => platform.id === 'windows')
  assert.equal(windows?.status, 'untested', 'install.yaml no longer marks Windows untested, and this page states what the file states (D94)')

  // A path names the goos its archive is built for, and the page prints the name a reader
  // reads for it: `darwin` is `macOS` here, and an operating system's internal name is not
  // what a reader is offered. The redesign (P4) folded each path's platform list into its
  // citation line, so the names are read off the whole page rather than off a
  // `Platforms: …` line of their own.
  assert.ok(
    !/\b(darwin|goos)\b/.test(text),
    'the page prints a path\'s platform list as the goos names install.yaml uses internally rather than the platform names its own list writes',
  )
  assert.match(text, /macOS, Linux/, 'the archive path does not render its platforms as the names install.yaml writes for them')

  // The untested platform's own note is rendered, verbatim: the reason Windows is a note
  // rather than a support promise, which is the whole of what the note says.
  const note = install.slice(install.indexOf('- id: windows')).match(/note: >-\n((?: {6}[^\n]*\n?)+)/)[1]
  assert.ok(text.includes(collapse(note)), `the page does not render Windows' own note, which install.yaml writes as:\n${collapse(note)}`)
})
