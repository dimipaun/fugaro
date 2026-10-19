// website/test/caps-and-costs.mjs — getting started, and the caps-and-costs guide
// (website plan, Task W1.5; reconciliation F22, F26, F27, F31, F33, F48, F50; D94,
// D96, D97; Global Constraints 4, 6, 8 and 11).
//
// The plan names four checks for the caps-and-costs page, and each of them is a
// mechanical form of a rule this work is held to:
//
//   1. **Every figure on the page is the one its own source carries.** This is
//      "publish the measurement, not the anecdote", and the page has two sources
//      since the owner's brief of 2026-10-19: `website/data/typical-costs.yaml`,
//      the owner's own measurement of this project's development runs, which the
//      table above the fold renders, and `website/content/costs.json`, the
//      owner-run aggregation's output (W0.3), which the per-recipe ranges render.
//      Neither half may carry a figure its own file did not produce — which is why
//      the spec's "~2 h" sentence never appears here (finding 33, D97) — and the
//      caps between them carry no figure at all: the reference's own usage lines
//      and one-line descriptions are the whole of what is rendered there.
//   2. **The data file is rendered exactly.** Every figure
//      `website/data/typical-costs.yaml` carries is on the page, in the column the
//      field is for, and no figure on the page is one the file does not carry — so
//      a refresh of the figures is an edit to the file and never to a page, and a
//      field added to the file with no column to land in fails here rather than
//      going unrendered.
//   3. **The "not a promise" line is present**, rendered from the aggregation's
//      own output rather than written beside it (D97).
//   4. **A group below `min_runs` is absent from the page.** The aggregation
//      suppresses a thin group and the content pipeline (W1.3) refuses a file
//      that carries one anyway, so the page renders exactly the groups the JSON
//      carries and no group it does not (D96's answer fixes the minimum at 20).
//   5. **The `fugaro budget` invocations match the CLI reference.** The caps
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
// installation's data (D96, Global Constraint 10). So the page renders its
// "not enough runs yet" state in the tree, and the figures below are a
// **fixture written into a temp copy** — never a claim about a real run, and
// never a file under `website/content/`. `website/content/costs.json` is
// published by the owner's own run of the command, and W2.3 is the task that
// publishes it with check 69c that the figures are real.
//
// How it builds: like `layout.mjs`, a copy of the repository in a temp
// directory, with the installed `node_modules` symlinked rather than copied, so
// a test can write a costs file (or remove a page's facts) and build again. The
// pages under test are the real ones this task adds; every build is
// network-free and nothing here starts anything but the site's own Astro.
//
// **Red step** (the plan's own, recorded here as the plan requires — the exact
// line this file's aggregation case printed before the page existed): with
// `caps-and-costs.astro` holding the spec's own sentence —
// `<p>Tasks with review often run ~2 h, not 30 min.</p>` — and that case run
// against it, `node --test --test-name-pattern='renders the costs.json field'
// test/caps-and-costs.mjs` said
//
//   Expected: FAIL — the page states "Tasks with review often run ~2 h, not 30 min.",
//   which is not the costs.json field it is rendered for
//
// (the case was called "every number on the page appears in costs.json" then, and
// the brief of 2026-10-19 gave the page a second source and the case its second
// half). The sentence was removed rather than the case: the defect is a page that
// prints a figure the aggregation did not produce, which is finding 33 and the
// reason the plan's §5 review row lists this page (F50, D97).
//
// **And the red steps for the second source** (the owner's brief of 2026-10-19,
// recorded here the way the plan asks for the first). The case that holds the table
// to `website/data/typical-costs.yaml` was written, and then each defect it exists
// for was planted in `src/components/CapsCosts.astro` and the case run against it:
//
//   - `money()` rounded to the whole dollar (`$${Math.round(value)}`) — the case
//     failed with "the page's note does not state $10.00, which
//     website/data/typical-costs.yaml carries", the file's `10` printed as `$10`;
//   - one cell given a figure of its own (`{'$1.00'}` for the nine-in-ten column) —
//     "row task: … | $1.00 | … (the file carries … | $1.27 | …)", two rows named;
//   - the middle-half column dropped (`{'—'}` in place of the range) — "row task:
//     … | — | … (the file carries … | $0.15–$0.68 | …)";
//   - the note paragraph deleted — "the page does not state where its figures come
//     from, or over what window".
//
// Each was removed and the case passed again. The same rule the aggregation's half
// is held to is the one these hold: a figure the page writes of its own is a figure
// no file behind it carries, and this page has two files.
//
// The two cases the second source changed were proved the same way: a typed "A run
// costs 2 USD." in the page's lead failed the aggregation case, whose first half now
// reads the whole page outside the aggregation's section rather than the costs
// section's own remainder; and a literal `$9.99` written into the note in place of
// the file's cap failed the absent-file case, which is the one that says a page with
// no `costs.json` behind it prints the data file's figures and nothing else.
//
// **The review of that rework moved three things here, and none of them is a rule of
// this file's.** The note says *whose* cap it prints ("our per-run cap is $10.00"):
// `--per-run` defaults to 0, which sets no cap, and an enforcing project with no
// per-run cap anywhere refuses every lease, so the figure is Fugaro's installation's
// and not something `fugaro` ships — the same correction `data/typical-costs.yaml`'s
// header and `src/content.ts`'s field doc now carry. The table sits in its own
// `overflow-x: auto` box, so six nowrap columns scroll rather than widening the
// document on a phone, and the lead the page writes is the `.lede` `community.astro`
// and `faq.astro` carry, which Astro's per-file scoping means this page must restate.
// The refusals the figures' reader owes are `layout.mjs`'s subject rather than this
// file's: they are shapes of the data file, and that file is where they are planted.
// The cases here were unchanged by all three, and the page's word count is 249 of
// D188's 250 — the note's own sentence was kept to the word the ceiling had left.

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
      // `plugin/skills` is the one part of `plugin/` a build reads: the hero's chat
      // quotes the skills (website/test/lib/build.mjs).
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

/** The built page split into the regions the page's own two sources fill. */
function splitPage(page) {
  const section = (id) => {
    const match = new RegExp(`<section id="${id}"[\\s\\S]*?</section>`).exec(page)
    assert.ok(match, `the page has no ${id} section`)
    return match[0]
  }
  const typical = section('typical-costs')
  const caps = section('caps')
  const costs = section('costs')
  return {
    typical,
    caps,
    costs,
    // Everything else: the page's own heading and its lead, which write no figure at all.
    rest: page.replace(typical, ' ').replace(caps, ' ').replace(costs, ' '),
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

/** A figure as the page prints it: dollars and cents, from the value the file carries. */
function money(value) {
  return `$${Number(value).toFixed(2)}`
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

test('the page renders exactly website/data/typical-costs.yaml, in the field each figure is for', async (t) => {
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')
  const { typical } = splitPage(page)
  const file = readTypicalCosts()
  const strays = []

  // One row per job the file holds, in the file's own order, and every cell the field its
  // column names: a figure the page rounds, drops or invents is a cell that does not match,
  // and so is a job the file carries with no row to render it.
  const rows = [...typical.matchAll(/<tr data-job="([^"]+)"[\s\S]*?<\/tr>/g)]
  assert.deepEqual(
    rows.map((row) => row[1]),
    file.jobs.map((job) => job.id),
    'the page does not render the jobs website/data/typical-costs.yaml carries, in the file\'s own order',
  )
  rows.forEach((row, index) => {
    const job = file.jobs[index]
    const cells = [...row[0].matchAll(/<t[dh][^>]*>([\s\S]*?)<\/t[dh]>/g)].map((match) => collapse(textOf(match[1])))
    const expected = [
      job.name,
      job.runs,
      money(job.median_usd),
      middleHalf(job.middle_half_usd),
      money(job.nine_in_ten_under_usd),
      job.worst_usd === undefined ? '—' : money(job.worst_usd),
    ]
    if (JSON.stringify(cells) !== JSON.stringify(expected)) {
      strays.push(`row ${job.id}: ${cells.join(' | ')} (the file carries ${expected.join(' | ')})`)
    }
  })

  // The note under the table states how old the figures are and what stops a run that
  // loops: the window's two dates, the number of runs behind them and the per-run cap, each
  // one a value the file carries rather than a sentence written beside it.
  const note = blocksOf(typical)
    .map(collapse)
    .find((block) => block.startsWith("Fugaro's own runs"))
  assert.ok(note, 'the page does not state where its figures come from, or over what window')
  for (const value of [file.window.from, file.window.to, file.window.runs, money(file.perRunCap)]) {
    assert.ok(note.includes(value), `the page's note does not state ${value}, which website/data/typical-costs.yaml carries`)
  }

  // And no figure in the region is one the file does not carry — the direction that makes a
  // refresh an edit to the file: the table's head is a label row rather than a figure ("9 in
  // 10 under" names the statistic), so it comes off before the figures are read, and the rows
  // above are compared cell by cell in any case.
  const allowed = figuresOf(file)
  const withoutHead = typical.replace(/<thead[\s\S]*?<\/thead>/g, ' ')
  for (const block of blocksOf(withoutHead).map(collapse)) {
    if (numbersIn(block).some((number) => !allowed.has(number))) strays.push(block)
  }

  assert.deepEqual(
    [...new Set(strays)],
    [],
    `the page states ${[...new Set(strays)].map((s) => JSON.stringify(s)).join(', ')}, which is not what website/data/typical-costs.yaml carries (D97: the page publishes the measurement, and no figure of its own)`,
  )
})

test('the aggregation\'s section renders the costs.json field each figure is for, and the page writes no figure of its own', async (t) => {
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')
  const { caps, costs, rest } = splitPage(page)
  const strays = []

  // Outside the aggregation's section — the page's own lead, and the caps, where the
  // reference's usage lines are the whole of what is rendered — no figure is written at all:
  // a typed "Runs often cost 2 USD." there would pass a check that allowed any number the
  // fixture holds anywhere. (The table above the fold belongs to the other source, and the
  // case above holds it to its own file.)
  for (const block of [...blocksOf(caps), ...blocksOf(rest)]) {
    if (numbersIn(block).length > 0) strays.push(collapse(block))
  }

  // The table: every cell is the specific field its column renders, compared exactly.
  const rows = [...costs.matchAll(/<tr data-recipe="([^"]+)"[\s\S]*?<\/tr>/g)]
  assert.equal(rows.length, FIGURES.recipes.length, 'the table does not carry one row per group')
  rows.forEach((row, index) => {
    const group = FIGURES.recipes[index]
    const cells = [...row[0].matchAll(/<t[dh][^>]*>([\s\S]*?)<\/t[dh]>/g)].map((m) => collapse(textOf(m[1])))
    const expected = [
      group.recipe,
      String(group.runs),
      `${group.duration_s.min} / ${group.duration_s.median} / ${group.duration_s.p90}`,
      `${group.cost_usd.min} / ${group.cost_usd.median} / ${group.cost_usd.p90}`,
      String(group.compute_not_estimated),
    ]
    if (JSON.stringify(cells) !== JSON.stringify(expected)) strays.push(`row ${group.recipe}: ${cells.join(' | ')} (expected ${expected.join(' | ')})`)
  })

  // Every other block of the section may carry only the numbers of the one field it
  // renders: the window, the minimum, one counter, or the aggregation's own disclaimer.
  const T = FIGURES.totals
  const counters = [
    [/^\d+ runs were below that threshold/, T.runs_below_min_runs],
    [/^\d+ runs carried no finished figure/, T.runs_without_a_figure],
    [/^\d+ runs named a recipe this binary does not ship/, T.runs_unknown_recipe],
    [/^\d+ runs carried no compute estimate/, T.runs_not_estimated],
  ]
  const fieldsFor = (text) => {
    if (text.startsWith('Measured over')) return [FIGURES.since, FIGURES.until, T.runs, T.recipes]
    if (text.startsWith('A group thinner')) return [FIGURES.min_runs]
    const counter = counters.find(([pattern]) => pattern.test(text))
    if (counter) return [counter[1]]
    if (text === collapse(FIGURES.not_a_promise)) return [FIGURES.not_a_promise]
    return []
  }
  const withoutRows = costs.replace(/<tr data-recipe="[\s\S]*?<\/tr>/g, ' ').replace(/<caption[\s\S]*?<\/caption>/g, ' ')
  for (const block of blocksOf(withoutRows).map(collapse)) {
    const allowed = fieldsFor(block).flatMap(numbersIn)
    if (numbersIn(block).some((n) => !allowed.includes(n))) strays.push(block)
  }

  assert.deepEqual(
    [...new Set(strays)],
    [],
    `the page states ${[...new Set(strays)].map((s) => JSON.stringify(s)).join(', ')}, which is not the costs.json field it is rendered for (D97: the page publishes the aggregation's figures, and no figure of its own)`,
  )
})

test('the costs section states the empty state when there is no group to render', async (t) => {
  // D96: "not enough runs yet" is the state of zero renderable groups, not only of an
  // absent file. A present file with no group would otherwise render an empty table.
  const none = buildWith(t, publish({ ...FIGURES, recipes: [] }))
  const page = builtPage(none, '/caps-and-costs/')
  assert.match(collapse(textOf(page)), /not enough runs/, 'a costs.json with recipes: [] does not render the empty state')
  assert.ok(!/<table\b/.test(splitPage(page).costs), 'a costs.json with no group renders an empty table')

  // Every group under the minimum is refused by the content pipeline: the build stops,
  // naming the file, rather than rendering a table of nothing.
  const thin = buildWith(t, publish({ ...FIGURES, recipes: [{ ...FIGURES.recipes[0], recipe: 'rare', runs: 4 }] }))
  assert.notEqual(thin.status, 0, 'a costs.json with only under-minimum groups was published')
  assert.match(thin.output, /costs\.json/)
})

test('the not-a-promise line is rendered with the figures', async (t) => {
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')

  // The disclaimer is the aggregation's own string (W0.3's `not_a_promise`), so
  // the page renders it rather than writing one beside it: a disclaimer written
  // here would be the second copy of the rule, and the one that drifts (D97).
  assert.ok(
    collapse(textOf(page)).includes(collapse(FIGURES.not_a_promise)),
    'the page does not render the aggregation\'s own "not a promise" line',
  )
})

test('a group below min_runs is never rendered', async (t) => {
  // D96's answer fixes the minimum at 20 runs, and a range drawn from fewer is
  // the anecdote this whole arrangement exists to keep off the page. Two fences
  // hold it: the aggregation suppresses the group, and a file that carries one
  // anyway fails the build rather than being published (W1.3's pipeline).
  const thin = {
    ...FIGURES,
    recipes: [...FIGURES.recipes, { recipe: 'rare', runs: 4, compute_not_estimated: 0, duration_s: { min: 1, median: 2, p90: 3 }, cost_usd: { min: 0.01, median: 0.02, p90: 0.03 } }],
  }
  const refused = buildWith(t, publish(thin))
  assert.notEqual(refused.status, 0, `the build published a group of 4 runs, below the minimum of 20:\n${refused.output}`)
  assert.match(refused.output, /costs\.json/, 'the failure does not name the file the figures came from')
  assert.match(refused.output, /rare/, 'the failure does not name the group that is too thin to publish')

  // And the page renders exactly the groups the JSON carries: no group appears
  // that the aggregation did not publish, and none of the published ones is
  // dropped.
  const built = buildWith(t, publish(FIGURES))
  const page = builtPage(built, '/caps-and-costs/')
  const rendered = [...page.matchAll(/data-recipe="([^"]+)"/g)].map((m) => m[1])
  assert.deepEqual(rendered, FIGURES.recipes.map((group) => group.recipe), 'the page does not render the groups the aggregation published, in its own order')
  assert.ok(!page.includes('rare'), 'the page names a group the aggregation did not publish')

  // The sample the figures are drawn from is printed, as D97 requires: the
  // threshold is the aggregation's own `min_runs`, not a number written here.
  assert.match(collapse(textOf(page)), new RegExp(`\\b${FIGURES.min_runs}\\b`), 'the page does not print the minimum sample it publishes a figure from')
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

test('the caps page states the empty state while the aggregation has not been run', async (t) => {
  // The costs file is absent from this checkout, which is the honest day-one
  // state: the aggregation is owner-run and no run may read an installation's
  // records (D96, Global Constraint 10). A page that rendered an aggregation
  // figure anyway, or that fell silent about it, would be the failure this
  // arrangement exists to prevent. The figures the page does render are the data
  // file's, which is a different source with a different life — hand-written,
  // always present, and held to the case above — so the check here is that the
  // absent file is the whole of what the aggregation's section says and that no
  // figure anywhere on the page is one the data file does not carry.
  const built = buildWith(t, () => {
    const file = path.join(REPO, 'website', 'content', 'costs.json')
    assert.ok(!existsSync(file), 'website/content/costs.json is committed: it is the owner-run aggregation\'s own output and W2.3 is the task that publishes it with check 69c')
  })
  const page = builtPage(built, '/caps-and-costs/')
  const text = collapse(textOf(page))
  const { costs } = splitPage(page)

  assert.match(text, /not enough runs/, 'the page does not say the aggregation has not been run')
  assert.deepEqual(
    numbersIn(textOf(costs)),
    [],
    'the page renders an aggregation figure with no costs file behind it',
  )

  // The table's head is a label row rather than a figure ("9 in 10 under" names the
  // statistic), so it comes off before the page's figures are read; everything else a reader
  // reads has to be a figure website/data/typical-costs.yaml carries.
  const allowed = figuresOf(readTypicalCosts())
  const withoutHeads = page.replace(/<thead[\s\S]*?<\/thead>/g, ' ')
  const strays = [...new Set(blocksOf(withoutHeads).map(collapse))].filter((block) =>
    numbersIn(block).some((number) => !allowed.has(number)),
  )
  assert.deepEqual(
    strays,
    [],
    `the page writes ${strays.map((s) => JSON.stringify(s)).join(', ')}, which no file behind this page carries: with no costs.json the only figures on it are website/data/typical-costs.yaml's`,
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

  const articles = [...page.matchAll(/<article data-path="([^"]+)"[\s\S]*?<\/article>/g)].map((match) => ({
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
