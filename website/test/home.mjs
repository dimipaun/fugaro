// website/test/home.mjs — the hero, the chat, the flow and the run detail (website plan,
// Task W1.4; website changes spec of 2026-10-18, items 2, 3 and 4).
//
// The two pages this task writes are the first pages the site has, and they are the pages
// where a marketing site is most likely to say something the product cannot back. So every
// check below is the mechanical form of one of the plan's rules, and none of them is a
// check on the component's own text:
//
//   1. **The tagline is README.md's line, byte for byte** (F48, D102). It is read here from
//      the README, not from the component, so the two cannot drift: the hero that types the
//      tagline in by hand is the defect this check removes.
//   2. **The hero's sentences and commands are the repository's.** Its lead sentence has to
//      be a sentence of README.md, and its install commands have to be the commands
//      `website/data/install.yaml` carries for the paths it renders — never typed into the
//      component (Global Constraint 6, W1.4 step 1).
//   3. **The chat is the workflow, and every line its turns speak is a document's own**
//      (changes spec item 3). The request is the spec's example and the agent's two lines are
//      the `parallelism` and `working` skills' own sentences — read from those files rather
//      than imagined, since a chat is where a page invents what a product says. Two things on
//      the figure are not quotations, and they are held as what they are instead: the three
//      `Pull request n of 3` labels, the illustration's own labels for work a run would
//      report, and the status line under the agent's answer, the illustration's own label for
//      the launch (`Fugaro: launching 3 jobs`), which no skill prints — the launch is what the
//      agent did, and the line the reader is shown is the picture's, not a sentence quoted
//      from `plugin/skills/parallelism/SKILL.md`. That is why the caption may not call the
//      figure's lines quotations, and why the case below refuses a status line that *is* a
//      sentence of the document its turn cites. The hero offers one command to
//      copy besides the install paths, `fugaro watch`, and the old `fugaro run` example is
//      asserted gone; the slot for the screenshot a separate task adds is asserted to be
//      marked and empty, and the page to carry no picture but its wordmark.
//   4. **The flow carries the changes spec's four steps, in order**, one short line each,
//      from one component on both pages: the names, and the caption of each step, are read
//      out of the spec's own item 4 and of the section of README.md the step cites, in that
//      section's order — all four captions are lines of the README, so a source alone would
//      let them be shuffled among the steps. `ArchitectureDiagram.astro` supersedes D185's
//      step names; the detail D185's two annotations held (the caches, the trust boundary) is
//      under `RunDetail.astro` on How It Works, whose five steps and note are held to
//      README.md's `## How it works` and to `docs/design/v1.md` the same way, and whose
//      absence from the homepage is a case of its own (changes spec item 4).
//   5. **Neither page names a claim the product cannot back**: no console download, no
//      Windows/WSL support, no "~2 h", no ballpark ranges (F50, D102, W1.4 step 4). The
//      video slot is checked against `site.yaml`'s own flag rather than against today's
//      value, so W2.4 turns the video on without meeting a red test here (D107).
//   6. **Every link the pages carry points at a route the plan's tasks write** — the layout's
//      nav and the hero's call to action included, since a link to a route outside that list
//      is a 404 in the artifact (Global Constraint 9). Whether each page *exists* yet is
//      W1.11's link check, which runs once the page tasks have landed.
//   7. **Every `var(--token)` the pages ship has a declaration behind it**, because an
//      undefined custom property makes its declaration invalid at computed-value time rather
//      than falling back: a token name the design layer does not declare is a style that
//      silently does not apply, which is how the hero came to name a `--weight-bold` (W1.2).
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file:
// the check is a statement about the artifact a reader receives, and every fact it holds a
// page to is read from the repository rather than from the page's own sources. One case in
// the promise section is the exception, and it is the one that has to be: whether a README
// of the wrong *shape* stops the build cannot be asked of an artifact that was built from
// the right one, so it builds a mutated copy of the repository (`test/lib/build.mjs`) and
// reads the build's refusal rather than a page.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { findAll, parse, textOf, walk } from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)
const DIST = path.join(SITE, 'dist')

/** The bytes of a file in the checkout, for a page to be held against. */
function repoFile(relative) {
  const file = path.join(REPO, relative)
  assert.ok(existsSync(file), `${relative} is missing: this check reads the repository, not the build`)
  return readFileSync(file, 'utf8')
}

/** A built page, by route: `''` is `/`, `'how-it-works'` is `/how-it-works/`. */
function builtPage(route) {
  const file = path.join(DIST, ...(route === '' ? [] : [route]), 'index.html')
  assert.ok(
    existsSync(file),
    `the build produced no ${route === '' ? '/' : `/${route}/`} page: ${path.relative(REPO, file)} is not there, and the built page is what this check reads (W1.4)`,
  )
  return readFileSync(file, 'utf8')
}

/**
 * The stylesheet a built page carries. Astro inlines a small stylesheet into the page and
 * links a larger one, so both are read: the check is about the CSS the page ships, and
 * which of the two shapes it arrived in is the bundler's business.
 */
function builtCSS(page) {
  const inline = [...page.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1])
  const linked = [...page.matchAll(/<link\b[^>]*>/g)]
    .map((tag) => /href="([^"]+)"/.exec(tag[0]))
    .filter(Boolean)
    .map((m) => path.join(DIST, m[1].replace(/^\//, '').split('?')[0]))
    .filter((file) => file.endsWith('.css') && existsSync(file))
    .map((file) => readFileSync(file, 'utf8'))
  return [...inline, ...linked].join('\n')
}

/** An element's text: its tags removed, and the entities Astro writes decoded. */
function decode(html) {
  return html
    .replace(/&quot;/g, '"')
    .replace(/&#39;|&apos;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
}

function text(html) {
  return decode(html.replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim()
}

/**
 * A node's text as a reader receives it: the decoration the page marks `aria-hidden` left
 * out (the flow's step numbers are drawing, not words), entities decoded,
 * runs of whitespace one space. This is what the page says, which is what the checks below
 * hold to the repository.
 */
function readableText(node) {
  if (node.attrs && node.attrs.get('aria-hidden') === 'true') return ''
  if (node.text !== undefined) return node.text
  if (node.raw !== undefined) return ''
  return (node.children ?? []).map(readableText).join('')
}

/** The text of the first element carrying `class="…"`, or null when there is none. */
function classText(html, className) {
  const m = new RegExp(`<([a-z0-9]+)[^>]*class="${className}"[^>]*>([\\s\\S]*?)</\\1>`).exec(html)
  return m ? text(m[2]) : null
}

/**
 * A string as a document writes it rather than as a page renders it: markdown emphasis and
 * code spans are markers and not text, and where a document breaks a line or indents is the
 * document's layout rather than its words. Applied to both sides of every comparison below,
 * so a caption and the document it comes from are compared word for word with the layout
 * taken out of them — which is what README.md's `## How it works` needs, since a third of
 * that section is an ASCII diagram whose columns and padding are drawing rather than
 * writing, and a page that quotes a line of it should not have to quote the diagram's
 * whitespace along with it.
 */
function normalize(markdown) {
  return markdown.replace(/[`*]/g, '').replace(/\s+/g, '')
}

/** README.md's tagline: the line under its `# Fugaro` title, emphasis stripped (F48). */
function readmeTagline() {
  return promiseLines()[0]
}

/**
 * README.md's subline: the line under its tagline, emphasis stripped (changes spec item 2).
 * It is the promise the hero leads with and the sentence the head describes the site with,
 * and this check reads it from the README for the same reason the tagline's is read there:
 * the two lines are the README's, and neither is a second copy typed into a page.
 */
function readmeSubline() {
  return promiseLines()[1]
}

/** The two non-empty lines under README.md's `# Fugaro` title: the tagline, then the subline. */
function promiseLines() {
  const lines = repoFile('README.md').split('\n')
  const title = lines.findIndex((line) => line.trim() === '# Fugaro')
  assert.notEqual(title, -1, 'README.md carries no "# Fugaro" title, and the site\'s promise is the two lines under it')
  const under = lines.slice(title + 1).filter((candidate) => candidate.trim() !== '').slice(0, 2)
  assert.equal(under.length, 2, 'README.md\'s "# Fugaro" title is followed by fewer than two non-empty lines, and the site renders the promise the two of them state (changes spec item 2)')
  return under.map((line) => line.trim().replace(/^\*\*(.+)\*\*$/, '$1').trim())
}

/** One `## <heading>` section of README.md, up to the next `## `. */
function readmeSection(heading) {
  const readme = repoFile('README.md')
  const start = readme.indexOf(`\n## ${heading}\n`)
  assert.notEqual(start, -1, `README.md carries no "## ${heading}" section, and this check holds a caption to it`)
  const rest = readme.slice(start + 1)
  const next = rest.indexOf('\n## ', 1)
  return next === -1 ? rest : rest.slice(0, next)
}

/**
 * One install path of `website/data/install.yaml`, as its own block of the file: from its
 * `- id:` line to the next one. A block rather than a parsed document, because this check
 * is about what the file carries and the site's own reader is what parses it (W1.3).
 */
function installPathBlock(id) {
  const yaml = repoFile('website/data/install.yaml')
  const start = yaml.indexOf(`\n  - id: ${id}\n`)
  assert.notEqual(start, -1, `website/data/install.yaml carries no install path with id ${id}`)
  const rest = yaml.slice(start + 1)
  const next = rest.indexOf('\n  - id: ')
  return next === -1 ? rest : rest.slice(0, next)
}

/** The commands an install path prints: its `commands:` block scalar bodies. */
function installPathCommands(id) {
  return [...installPathBlock(id).matchAll(/^ {8}(.*)$/gm)].map((m) => m[1].trim()).filter(Boolean)
}

/**
 * The routes the site's pages live at, from the plan: the page files its tasks' `**Files:**`
 * lines name. Pages are lowercase and components are capitalised in this repository, so a
 * lowercase `<name>.astro` in a Files line is a page — and this is the only list of the
 * site's pages that exists while the page tasks are still being written.
 */
function plannedRoutes() {
  const routes = new Set()
  for (const line of repoFile('docs/website/implementation-plan.md').split('\n')) {
    if (!line.startsWith('**Files:**')) continue
    for (const m of line.matchAll(/([a-z0-9-]+)\.astro/g)) {
      routes.add(m[1] === 'index' ? '/' : `/${m[1]}/`)
    }
  }
  return routes
}

/** The internal links a built page carries: its hrefs that are paths on this site. */
function internalLinks(page) {
  return [...page.matchAll(/<a\b[^>]*href="([^"]+)"/g)]
    .map((m) => m[1])
    .filter((href) => href.startsWith('/'))
}

/**
 * The four steps the changes spec's item 4 names, read out of the ruling itself — the
 * spec's own `1 …`, `2 …`, `3 …`, `4 …` list, in the order it writes them.
 *
 * This replaces P8's reader of D185's row in `reconciliation.md`, and the replacement is
 * the point: the changes spec of 2026-10-18 is the later ruling, and item 4 names the four
 * steps as `Your agent`, `Fugaro`, `Ephemeral cloud job`, `Pull request` where D185 named
 * the first brief's four (`task submitted`, `ephemeral Cloud Run job`, `agent runs`, `pull
 * request`). The row is still the decision the page renders, so it is still what this check
 * holds the page to: a page that changed its steps and a spec that changed its mind cannot
 * drift apart silently, and a reworded item stops this check with the spec's own words in
 * the message rather than passing quietly.
 */
function item4() {
  const spec = repoFile('docs/website/redesign/changes-spec-2026-10-18.md')
  const at = spec.indexOf('four steps:')
  assert.notEqual(
    at,
    -1,
    'docs/website/redesign/changes-spec-2026-10-18.md carries no "four steps:", and the four steps the flow renders are the ones item 4 names (changes spec 2026-10-18, item 4)',
  )
  // The spec wraps the list across two lines, so the names are read from the paragraph
  // rather than from one line of it, and the four numbered code spans after the colon are
  // the list: `1 Your agent`, `2 Fugaro`, `3 Ephemeral cloud job`, `4 Pull request`.
  const named = [...spec.slice(at, at + 400).matchAll(/`(\d)\s+([^`]+)`/g)].map((m) => ({ number: Number(m[1]), name: m[2].trim() }))
  assert.deepEqual(
    named.map((step) => step.number),
    [1, 2, 3, 4],
    `the changes spec's item 4 no longer names four numbered steps this check can read: it reads ${JSON.stringify(named)}`,
  )
  return { steps: named.map((step) => step.name) }
}

/**
 * The sections a caption may be quoted from, by the name a step cites. A section rather
 * than the whole file: all four captions are lines of README.md, so held to the file alone
 * they could be shuffled among the steps and each would still be a quotation. Inside one
 * section the captions must also come in the order the section writes them, which is what
 * keeps the two steps that share `## How it works` from swapping.
 */
function captionSources() {
  return {
    'README.md#Getting started': readmeSection('Getting started'),
    'README.md#Why Fugaro': readmeSection('Why Fugaro'),
    'README.md#How it works': readmeSection('How it works'),
    'docs/design/v1.md': repoFile('docs/design/v1.md'),
  }
}

/** A built page's flow, as the elements it renders: the steps in order, with their arrows. */
function flowOf(html) {
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/)
  const doc = parse(html)
  const flows = findAll(doc, (el) => el.name === 'ol' && classes(el).includes('flow-steps'))
  const out = { flows: flows.length, steps: [], arrows: 0 }
  for (const flow of flows) {
    for (const el of walk(flow)) {
      if (el.name === 'svg' && classes(el).includes('step-arrow')) out.arrows += 1
      if (el.name !== 'li' || !classes(el).includes('flow-step')) continue
      const inner = (selector) => {
        const node = [...walk(el)].find((child) => classes(child).includes(selector))
        return node ? textOf(node).replace(/\s+/g, ' ').trim() : ''
      }
      out.steps.push({
        name: el.attrs.get('data-step') ?? '',
        source: el.attrs.get('data-source') ?? '',
        // The heading and the caption are the step as a reader sees it; `data-step` is this
        // check's own hook, and the two are held together below rather than one trusted for
        // the other.
        heading: inner('step-name'),
        caption: inner('step-caption'),
        // A list inside a step is a sub-list of it — the annotations D185 kept on the job
        // step, which item 4's one-line-each picture does not carry.
        nested: [...walk(el)].filter((child) => child.name === 'li').length,
      })
    }
  }
  return out
}

/** A step's slug as a heading: `data-step` is lowercase, a heading is not. */
function capitalize(name) {
  return name ? name[0].toUpperCase() + name.slice(1) : ''
}

/**
 * Every way a page's flow falls short of the changes spec's item 4, as the lines a reader
 * sees. Pure, over one page's HTML, so the built pages are checked with it and the refusals
 * below are proved with it: a fixture that breaks one rule has to make exactly that line
 * appear.
 *
 * The ruling's part is the four names in order. The captions are held to the *section* each
 * step cites, in that section's order — `reached` is a cursor per section that only moves
 * forward, so of the two steps quoting `## How it works`, the job's line comes first in the
 * README and has to come first on the page. Held to their files alone the four captions are
 * all lines of the README and can be shuffled among the steps with every one of them still a
 * quotation, while the page then says Fugaro is where the pull request opens.
 */
function flowFailures(html, ruling, sources) {
  const out = []
  const flow = flowOf(html)
  if (flow.flows !== 1) return [`the page renders ${flow.flows} flows, and it renders one: the flow is the picture of the run (P8, spec item 4)`]

  const names = flow.steps.map((step) => step.name)
  const wanted = ruling.steps.map((name) => name.toLowerCase())
  if (names.join(' | ') !== wanted.join(' | ')) {
    out.push(
      `the flow's steps are ${JSON.stringify(names)}, and the changes spec's item 4 names ${JSON.stringify(ruling.steps)}, in that order: a step added, dropped or reordered is a claim about how a run works (changes spec 2026-10-18, item 4)`,
    )
  }

  const reached = new Map()
  for (const [index, step] of flow.steps.entries()) {
    const where = `the ${JSON.stringify(step.name)} step`
    if (!step.caption) out.push(`${where} carries no caption, and every step has one short line saying what happens there (changes spec 2026-10-18, item 4)`)
    if (!(step.source in sources)) {
      out.push(`${where} attributes its caption to ${JSON.stringify(step.source)}, and a caption is held to a section of README.md (changes spec 2026-10-18, item 4)`)
    } else {
      const line = normalize(step.caption)
      const from = reached.get(step.source) ?? 0
      const at = normalize(sources[step.source]).indexOf(line, from)
      if (at === -1) {
        out.push(
          `${where}'s caption ${JSON.stringify(step.caption)} does not appear in ${step.source} at or after the step before it: every caption is the repository's own words, in the order their document writes them (changes spec 2026-10-18, item 4; Global Constraint 6)`,
        )
      } else {
        reached.set(step.source, at + line.length)
      }
    }
    // The name a reader sees, which is the heading: `data-step` is this check's own hook, and
    // a heading that stopped matching the spec would leave the visible flow saying something
    // the ruling does not.
    const named = ruling.steps[index] ?? ''
    if (step.heading !== named) {
      out.push(
        `${where}'s heading reads ${JSON.stringify(step.heading)}, and the changes spec's item 4 names it ${JSON.stringify(named)}: the heading is the name a reader sees (changes spec 2026-10-18, item 4)`,
      )
    }
    // One short line per step, and no sub-list under one of them: item 4 asks for the picture
    // with the minimum of copy, and the two annotations D185 hung on the job step are detail
    // that belongs in the How It Works page's explanation rather than in the picture.
    if (step.nested > 0) {
      out.push(
        `${where} carries ${step.nested} nested list item(s), and every step is one short line: the picture carries the four steps and nothing under them (changes spec 2026-10-18, item 4)`,
      )
    }
  }

  if (flow.arrows !== flow.steps.length - 1) {
    out.push(
      `the flow carries ${flow.arrows} arrows between ${flow.steps.length} steps, and it carries one between each pair: the spec's picture is a flow, and the arrows are what draw it (P8, spec item 4)`,
    )
  }
  return out
}

/**
 * A page carrying nothing but a flow, for the refusals below. The heading is the step's name
 * as the component writes it — capitalised from `data-step`, which is how the two are held
 * together — so the fixture page passes the same checks the built pages do; `heading` and
 * `nested` are the overrides the "a reader sees something else" cases break.
 */
function flowPage(steps) {
  const body = steps
    .map((step, index) => {
      const nested = step.nested ? `<ul class="step-annotations">${step.nested}</ul>` : ''
      const arrow =
        index < steps.length - 1 && step.arrow !== false
          ? '<svg class="step-arrow" aria-hidden="true" focusable="false"><path class="arrow-line" d="M12 1 V 25"></path></svg>'
          : ''
      return `<li class="flow-step" data-step="${step.name}" data-source="${step.source}"><div class="step-card"><div class="step-head"><span class="step-number" aria-hidden="true">${index + 1}</span><h2 class="step-name">${step.heading ?? capitalize(step.name)}</h2></div><p class="step-caption">${step.caption}</p>${nested}</div>${arrow}</li>`
    })
    .join('')
  return `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>t</h1><figure class="flow"><ol class="flow-steps">${body}</ol></figure></main></body></html>`
}

/**
 * The four steps as the page renders them, for the fixture cases: a copy of the markup, with
 * the repository's own lines in the sections the component cites and in the component's order.
 */
function fixtureSteps() {
  return [
    { name: 'your agent', source: 'README.md#Getting started', caption: "the plugin's working skill launches, watches and diagnoses runs" },
    { name: 'fugaro', source: 'README.md#Why Fugaro', caption: 'it uses your Google Cloud project and your model credentials' },
    { name: 'ephemeral cloud job', source: 'README.md#How it works', caption: 'one execution per task, then gone' },
    { name: 'pull request', source: 'README.md#How it works', caption: 'each commit is pushed within about a minute while the agent works' },
  ]
}

/** The section the detail quotes, by the name each of its steps cites. */
const HOW_IT_WORKS_SOURCE = 'README.md#How it works'

/**
 * The documents the detail may quote from, by the name each line cites: README.md's own run,
 * and the design document's one sentence about where the boundary of a run is.
 */
function runDetailSources() {
  return {
    [HOW_IT_WORKS_SOURCE]: readmeSection('How it works'),
    'docs/design/v1.md': repoFile('docs/design/v1.md'),
  }
}

/**
 * README.md's `## How it works` numbered steps, as the section writes them: the bold words
 * that name a step. The names carry punctuation the heading does not (`**Loop:**`,
 * `**Write caches back and exit.**`), which is the section's layout rather than a second name,
 * so it is stripped here.
 */
function howItWorksSteps() {
  return [...readmeSection('How it works').matchAll(/^\s*(\d+)\.\s+\*\*([^*]+)\*\*/gm)].map((match) => ({
    number: Number(match[1]),
    name: match[2].trim().replace(/[:.]$/, ''),
  }))
}

/** A name as the component writes it into `data-step`. */
function slug(name) {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
}

/** The How It Works page's detail, as the elements it renders: its title, its steps and its note. */
function runDetailOf(html) {
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/)
  const doc = parse(html)
  const sections = findAll(doc, (el) => el.name === 'section' && classes(el).includes('run-detail'))
  const out = { sections: sections.length, title: '', steps: [], note: '' }
  for (const section of sections) {
    const inside = (root, selector) => {
      const node = [...walk(root)].find((child) => classes(child).includes(selector))
      return node ? textOf(node).replace(/\s+/g, ' ').trim() : ''
    }
    out.title = inside(section, 'run-detail-title')
    out.note = inside(section, 'run-note')
    for (const el of walk(section)) {
      if (el.name !== 'li' || !classes(el).includes('run-step')) continue
      // The heading carries the step's number as decoration beside its name, and the name is
      // what the section writes: the number is dropped here for the same reason the component
      // hides it from a screen reader.
      const heading = inside(el, 'run-step-name')
      out.steps.push({
        name: el.attrs.get('data-step') ?? '',
        source: el.attrs.get('data-source') ?? '',
        heading: heading.replace(/^\d+\s*/, ''),
        line: inside(el, 'run-step-line'),
      })
    }
  }
  return out
}

/**
 * Every way the detail falls short of README.md's own run, as the lines a reader of this file
 * sees. Pure, over the detail and the section, so the built page is checked with it and the
 * refusals are proved with it.
 *
 * `cursor` walks forward through the section: a step's heading and its line have to appear in
 * the section at or after the step before them, which is what keeps two neighbouring steps
 * from swapping their lines — each is still a line of the section, and the page then restores
 * the caches after the agent has run.
 */
function runDetailFailures(detail, steps, sources) {
  const out = []
  if (detail.sections !== 1) {
    return [`the page renders ${detail.sections} detail sections, and it renders one: the detail is the zoom-in under the picture (changes spec 2026-10-18, item 4)`]
  }
  if (detail.steps.length !== steps.length) {
    out.push(
      `the detail renders ${detail.steps.length} steps, and README.md's "## How it works" numbers ${steps.length}: the detail is the run that section describes, and a step missing is a run described as shorter than it is (Global Constraint 6)`,
    )
  }

  const section = normalize(sources[HOW_IT_WORKS_SOURCE] ?? '')
  let cursor = 0
  for (const [index, step] of detail.steps.entries()) {
    const named = steps[index]?.name ?? ''
    const where = `the detail's step ${index + 1}`
    if (step.heading !== named) {
      out.push(
        `${where} is headed ${JSON.stringify(step.heading)}, and README.md names step ${index + 1} ${JSON.stringify(named)}: the headings are the section's own names for its steps (Global Constraint 6, D102)`,
      )
    }
    if (step.source !== HOW_IT_WORKS_SOURCE) {
      out.push(`${where} attributes its lines to ${JSON.stringify(step.source)}, and the detail quotes README.md's "## How it works" (Global Constraint 6)`)
    }
    if (slug(step.heading) !== step.name) {
      out.push(
        `${where} carries data-step ${JSON.stringify(step.name)} beside the heading ${JSON.stringify(step.heading)}, and the two name the same step: the attribute is this check's own hook, and a hook that drifted from the heading is a check reading a page nobody sees`,
      )
    }
    // Both are searched from where the step before this one ended, and the cursor moves to
    // the end of the *line*: the section writes `**Sync** to the exact target commit…`, so a
    // line begins inside the name that heads it, and a cursor advanced past the name would
    // sit past the start of the line that belongs to it.
    let end = cursor
    for (const [what, line] of [
      ['heading', step.heading],
      ['line', step.line],
    ]) {
      const quoted = normalize(line)
      const at = quoted ? section.indexOf(quoted, cursor) : -1
      if (at === -1) {
        out.push(
          `${where}'s ${what} ${JSON.stringify(line)} is not a line of README.md's "## How it works" at or after the step before it: the detail is the section's own words, in the section's own order (changes spec 2026-10-18, item 4; Global Constraint 6)`,
        )
      } else {
        end = Math.max(end, at + quoted.length)
      }
    }
    cursor = end
  }

  if (!detail.note) {
    out.push('the detail carries no note, and the note is the one fact under the steps: where the boundary of a run is (changes spec 2026-10-18, item 4)')
  } else if (!normalize(sources['docs/design/v1.md'] ?? '').includes(normalize(detail.note))) {
    out.push(
      `the detail's note ${JSON.stringify(detail.note)} is not a line of docs/design/v1.md: the note is quoted from the design document rather than written for the page (Global Constraint 6)`,
    )
  }
  return out
}

/** The line the detail carries under its steps: the design document's sentence. */
function detailNote() {
  return 'The trust boundary is the container, not the process.'
}

/**
 * The detail's steps for the fixture cases: README.md's own five, each with the line the
 * component quotes from it.
 */
function fixtureDetailSteps() {
  const lines = {
    Start: "from the image of the repository's workflow (a named build-and-test setup in its fugaro.yaml)",
    Sync: 'Sync to the exact target commit and restore the dependency caches.',
    Loop: 'Claude Code implements, writes tests, runs the build and tests through a recording wrapper, reviews and fixes',
    'Push and open the pull request': 'a draft opens at the first push (marked not verified), and it is marked ready only when the run passes',
    'Write caches back and exit': 'The run record and the PR remain; the container does not.',
  }
  return howItWorksSteps().map((step) => ({ name: step.name, line: lines[step.name] }))
}

/** A page carrying nothing but a detail section, for the refusals above. */
function runDetailPage(steps, note) {
  const body = steps
    .map(
      (step, index) =>
        `<li class="run-step" data-step="${slug(step.heading ?? step.name)}" data-source="${HOW_IT_WORKS_SOURCE}"><h3 class="run-step-name"><span class="run-step-number" aria-hidden="true">${index + 1}</span>${step.heading ?? step.name}</h3><p class="run-step-line">${step.line}</p></li>`,
    )
    .join('')
  return `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>t</h1><section class="run-detail" aria-labelledby="run-detail-title"><h2 class="run-detail-title" id="run-detail-title">The run, step by step</h2><ol class="run-steps">${body}</ol><p class="run-note">${note}</p></section></main></body></html>`
}

test("the hero headline is README.md's tagline line, byte for byte", () => {
  const tagline = readmeTagline()
  const hero = classText(builtPage(''), 'hero-tagline')

  assert.equal(hero, tagline, 'hero headline differs from the tagline line under README.md\'s "# Fugaro" title')
})

test("the hero's subline is README.md's subline, byte for byte", () => {
  // The headline is the README's first line and the promise under it is the README's
  // second, and D102 requires every other sentence on the site to be traceable to a
  // document. Both are read from the built page and held against the README, so the
  // component carries no copy of its own and this check is what keeps it the README's
  // (changes spec item 2, W1.4 step 1).
  const subline = readmeSubline()
  const lead = classText(builtPage(''), 'hero-lead')

  assert.ok(lead, 'the hero renders no lead sentence, and a reader who has read the headline needs one sentence saying what Fugaro is')
  assert.equal(lead, subline, "the hero's lead is not README.md's subline, the sentence under the headline")
  assert.ok(
    normalize(repoFile('README.md')).includes(normalize(lead)),
    `the hero's lead sentence ${JSON.stringify(lead)} is not a sentence of README.md: every sentence on the site is traceable to a document (D102, W1.4 step 1)`,
  )
})

test('a README whose promise is markdown structure stops the build, naming the file', (t) => {
  // `website/src/content.ts` reads the two lines **positionally** — the first two non-empty
  // lines under the title — so a README that reorganises its opening is the way the read is
  // handed a line that is not the promise at all. What it may not do is render it: a README
  // that opens with its headline and then a section heading would put `## Install` under the
  // hero's headline, on the site's first screen. It stops the build naming the file instead,
  // and this case is the check on that: a read whose failure mode nobody has exercised is a
  // read whose failure mode nobody knows (changes spec item 2, F48, D102).
  const built = buildCopy(t, ({ root }) => {
    const file = path.join(root, 'README.md')
    const lines = readFileSync(file, 'utf8').split('\n')
    const title = lines.findIndex((line) => line.trim() === '# Fugaro')
    assert.notEqual(title, -1, 'the copy\'s README.md carries no "# Fugaro" title')
    const under = []
    for (let i = title + 1; i < lines.length && under.length < 2; i++) if (lines[i].trim() !== '') under.push(i)
    assert.equal(under.length, 2, 'the copy\'s README.md carries fewer than two non-empty lines under its title')
    // The tagline stays; the line under it becomes structural markdown.
    lines[under[1]] = '## Install'
    writeFileSync(file, lines.join('\n'))
  })

  assert.notEqual(built.status, 0, 'a README whose subline is a markdown heading was built: the site would print "## Install" under the hero\'s headline')
  assert.match(built.output, /README\.md/, `the build that stopped names no file, and a reader has to be told which one to fix:\n${built.output}`)
  assert.match(built.output, /markdown structure/, `the build stopped for a reason other than the shape of the two promise lines:\n${built.output}`)
})

test('the homepage names the promise in its title, its description and its cards', () => {
  // Item 2's list of places the new promise goes: the home hero (the two tests above), the
  // `<title>`, the meta description, and the OpenGraph and Twitter cards. All four are read
  // out of the built page and held to the README's two lines rather than to what the page
  // says they are, so a page that renamed itself is a failure and not a second opinion.
  const page = builtPage('')
  const tagline = readmeTagline()
  const subline = readmeSubline()
  const content = (pattern, what) => {
    const m = pattern.exec(page)
    assert.ok(m, `the homepage carries no ${what}, and the head is where a search engine and a card read the promise (changes spec item 2)`)
    return m[1]
  }

  assert.equal(content(/<title>([^<]*)<\/title>/, '`<title>`'), `Fugaro — ${tagline}`)
  assert.equal(content(/<meta name="description" content="([^"]*)"/, 'meta description'), subline)
  assert.equal(content(/<meta property="og:title" content="([^"]*)"/, '`og:title`'), `Fugaro — ${tagline}`)
  assert.equal(content(/<meta property="og:description" content="([^"]*)"/, '`og:description`'), subline)
  assert.equal(content(/<meta name="twitter:card" content="([^"]*)"/, 'Twitter card'), 'summary')
  // The card's description is the OG description: Astro's layout emits one `twitter:card`
  // and no second description tag, and `layout.mjs` owns the head's tag list.
  assert.equal((page.match(/<meta name="twitter:description"/g) ?? []).length, 0)
})

test('the homepage renders the install commands install.yaml carries, and types none of them', () => {
  const page = builtPage('')
  // Read as a document rather than sliced with a regex: each path's block holds a copy
  // control as well as its commands (P6), and a non-greedy `<div>…</div>` would stop at the
  // control's own closing tag and check half a block. The paths are the homepage's own section
  // since the changes spec of 2026-10-18 (item 4): they used to be in the hero, and the block
  // is `HomeInstall.astro`'s now.
  const blocks = [...walk(parse(page))].filter((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('home-install-path'))

  assert.equal(blocks.length, 2, `the homepage renders ${blocks.length} install paths, and it shows the two commands the plan names (W1.4 step 1)`)

  // And they are not in the hero: item 4 puts the promise and its one call to action above the
  // picture and the commands below it, so a command back in the hero is the shape the review
  // of this branch asked to have changed.
  const hero = findAll(parse(page), (el) => el.name === 'section' && (el.attrs.get('class') ?? '').split(/\s+/).includes('hero'))[0]
  assert.ok(hero, 'the homepage has no hero section, and the promise and its call to action are the hero (P5, D102)')
  assert.equal(
    [...walk(hero)].filter((el) => el.name === 'pre').length,
    0,
    'the hero prints a command, and item 4 gives the hero the promise and its call to action: the commands are the install section below the picture (changes spec 2026-10-18, item 4)',
  )

  for (const block of blocks) {
    const id = block.attrs.get('data-path')
    assert.ok(id, 'an install path on the homepage names no id, so nothing says which path of install.yaml it is')

    const commands = [...walk(block)]
      .filter((el) => el.name === 'pre')
      .map((el) => textOf(el).trim())
    assert.deepEqual(
      commands,
      installPathCommands(id),
      `the homepage's ${id} commands are not the ones website/data/install.yaml carries for that path: a command typed into the component is a second copy of the install text, and the one that drifts (W0.1, W1.4 step 1)`,
    )
  }
})

test('the hero renders a player only when site.yaml says the video exists', () => {
  const page = builtPage('')
  const flag = /^\s*video:\s*(true|false)\s*$/m.exec(repoFile('website/data/site.yaml'))

  assert.ok(flag, 'website/data/site.yaml carries no features.video, and the hero renders its video slot from that flag (D107)')
  if (flag[1] === 'true') return

  // The slot is empty while the video does not exist: no player, no poster, no embed, and
  // nothing that looks like a play button (D107, F50). W2.4's own test owns the other half.
  for (const [what, pattern] of [
    ['a <video> element', /<video\b/],
    ['an <iframe> embed', /<iframe\b/],
    ['a poster image', /\bposter=/],
    ['a player script', /type="video\//],
  ]) {
    assert.ok(
      !pattern.test(page),
      `the hero renders ${what} while site.yaml's features.video is false: the slot is empty until the video exists, and an empty player is the first thing a reader clicks (D107, W1.4 step 1)`,
    )
  }
})

test('the hero offers the console from its call to action only while the flag is on', () => {
  // Item 6 asks that the console be findable from the home page's call to action, and
  // `site.yaml`'s `features.console_downloads` is what decides whether it is offered at all.
  // This is the flag-off half — the state the site ships in, and the state `npm run check`
  // builds — and the flag-on half is `console-option.mjs`'s, over a copy with the flag
  // flipped, because the day the console ships is not a day this build can see.
  const flag = /^\s*console_downloads:\s*(true|false)\s*$/m.exec(repoFile('website/data/site.yaml'))
  assert.ok(
    flag,
    'website/data/site.yaml carries no features.console_downloads, and the hero offers the console from that flag (item 6)',
  )
  if (flag[1] === 'true') return

  const hero = findAll(parse(builtPage('')), (el) => el.name === 'section' && (el.attrs.get('class') ?? '').split(/\s+/).includes('hero'))[0]
  assert.ok(hero, 'the homepage has no hero section, and the promise and its call to action are the hero (P5)')

  const actions = [...walk(hero)].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('hero-actions'))
  assert.ok(actions, 'the hero carries no action row, and the call to action is what a reader does next (W1.4)')

  // The row is the two links and nothing else: no console link, no console sentence, no
  // placeholder where the link would go. A reader of an unreleased console has nothing to
  // click, and the hero must not offer a page that has nothing to download on it either —
  // "findable" is item 6's word for the released state.
  assert.deepEqual(
    [...walk(actions)].filter((el) => el.name === 'a').map((el) => [textOf(el).trim(), el.attrs.get('href')]),
    [
      ['Get started', '/getting-started/'],
      ['How it works', '/how-it-works/'],
    ],
    'the hero\'s call to action is not exactly the two links it carries while features.console_downloads is false: the console\'s link arrives with the flag, and until then the row says nothing about the console (changes spec 2026-10-18, item 6)',
  )
  assert.ok(
    !/console/i.test(textOf(actions)),
    'the hero\'s call to action mentions the console while features.console_downloads is false, and the unreleased state is structural: nothing about the console renders before its release (item 6)',
  )
})

// ---- the chat (changes spec of 2026-10-18, item 3) ---------------------------------------

/**
 * The documents a line of the chat may be quoted from, by the name the turn cites: the spec,
 * whose item 3 gives the request, and the two skills, which are what the agent does with one.
 */
function chatSources() {
  return {
    'docs/website/redesign/changes-spec-2026-10-18.md': repoFile('docs/website/redesign/changes-spec-2026-10-18.md'),
    'plugin/skills/parallelism/SKILL.md': repoFile('plugin/skills/parallelism/SKILL.md'),
    'plugin/skills/working/SKILL.md': repoFile('plugin/skills/working/SKILL.md'),
  }
}

const CHAT_CLASSES = (el) => (el.attrs.get('class') ?? '').split(/\s+/)

/**
 * The chat a built page renders: its turns in order, each with the lines it carries — the
 * reader's request, the agent's answer and the status line under it, and the results it
 * reports back with. One array, so the rules below read the chat the way a reader does.
 *
 * `line` is what a turn *says* and is held to the document it cites; `status` is the
 * illustration's own label for what the agent did (changes spec of 2026-10-18, item 3: the
 * launch is "shown as a subtle tool-call / status line, not a command the user typed"), and
 * the rule below refuses it as a quotation the same way it refuses the result labels.
 */
function chat(page) {
  return [...walk(parse(page))]
    .filter((el) => el.name === 'li' && CHAT_CLASSES(el).includes('chat-turn'))
    .map((el) => {
      const inside = (selector) => {
        const node = [...walk(el)].find((child) => CHAT_CLASSES(child).includes(selector))
        return node ? decode(readableText(node)).replace(/\s+/g, ' ').trim() : ''
      }
      return {
        turn: el.attrs.get('data-turn') ?? '',
        source: el.attrs.get('data-source') ?? '',
        who: inside('turn-who'),
        line: inside('turn-line'),
        status: inside('turn-status'),
        results: [...walk(el)]
          .filter((child) => CHAT_CLASSES(child).includes('turn-result'))
          .map((child) => decode(readableText(child)).replace(/\s+/g, ' ').trim()),
      }
    })
}

/**
 * Every failure of the chat's rule, as the lines a reader of this file sees, in two halves:
 *
 *   - **every line a turn *speaks* — its `line` — is a line of the document that turn cites**,
 *     and the lines of a document come in the order that document writes them. The chat is an
 *     illustration drawn from the repository rather than a recording of a session, so a line a
 *     skill does not write — or one that moved ahead of the line before it — is the page
 *     putting words in the agent's mouth that the agent's own skill does not have (changes
 *     spec item 3, Global Constraint 6);
 *   - **the figure's own labels are not quotations.** `status` (the agent's launch line) and
 *     `results` (the `Pull request n of 3` labels) are the illustration's, and the caption
 *     says so: a run that has not happened has no pull request numbers, and a launch line a
 *     reader is meant to read as a status is not a sentence either skill prints. So neither
 *     may be a line of the document its turn cites, which is what would make them quotations
 *     while the caption denied it.
 *
 * Written as a function over a chat so a fixture can prove it refuses.
 */
export function chatFailures(turns, sources) {
  const out = []
  const reached = new Map() // document -> how far into it the line before this one reached
  for (const [index, turn] of turns.entries()) {
    const where = `turn ${index + 1} of the chat, ${JSON.stringify(turn.line)}`
    if (!turn.line) {
      out.push(`${where}, has no text: the chat is the illustration, and a turn with nothing in it is a bubble a reader cannot read`)
      continue
    }
    const document = sources[turn.source]
    if (!document) {
      out.push(`${where}, cites ${JSON.stringify(turn.source)}, which is not a document this check reads: every line says which document it is quoted from`)
      continue
    }
    const line = turn.line
    if (line) {
      const quoted = normalize(line)
      const at = normalize(document).indexOf(quoted, reached.get(turn.source) ?? 0)
      if (at === -1) {
        out.push(
          `the chat's line ${JSON.stringify(line)} does not appear in ${turn.source} at or after the line before it: every line a turn of the chat speaks is the document's own words, in the order it writes them (changes spec 2026-10-18, item 3; Global Constraint 6)`,
        )
      } else {
        reached.set(turn.source, at + quoted.length)
      }
    }
    for (const [what, label] of [
      ['status line', turn.status],
      ...(turn.results ?? []).map((result) => ['result label', result]),
    ]) {
      if (!label) continue
      if (normalize(document).includes(normalize(label))) {
        out.push(
          `the chat's ${what} ${JSON.stringify(label)} is a line of ${turn.source}, and the figure's own labels are not quotations: the caption says the picture is an illustration, which is what makes it honest about a launch line and three pull request numbers that no skill has printed (changes spec 2026-10-18, item 3; D184)`,
        )
      }
    }
  }
  return out
}

test('the homepage chat is the workflow, and every line its turns speak is the document it cites', () => {
  const turns = chat(builtPage(''))

  // Item 3's illustration, turn for turn: the request, the agent's answer with the launch it
  // runs, and the report a few minutes later. A page that grew a fourth turn, or lost one, is
  // a page this check would otherwise go on accepting.
  assert.equal(turns.length, 3, `the homepage chat renders ${turns.length} turns, and item 3's illustration is three (changes spec 2026-10-18, item 3)`)
  assert.deepEqual(
    turns.map((turn) => turn.turn),
    ['you', 'agent', 'agent'],
    `the chat's turns are ${JSON.stringify(turns.map((turn) => turn.turn))}, and the reader's request comes first, with the agent answering it (changes spec 2026-10-18, item 3)`,
  )
  assert.ok(turns[0].who, 'the chat does not say who is speaking, and a reader who cannot tell the two voices apart has not been shown the workflow (changes spec 2026-10-18, item 3)')
  assert.ok(turns[1].status, "the agent's answer carries no status line, and item 3 asks for the launch to show as a subtle tool-call / status line rather than as a command the reader typed (changes spec 2026-10-18, item 3)")
  assert.equal(
    turns[2].results.length,
    3,
    `the agent reports back with ${turns[2].results.length} results, and item 3's illustration fans three pieces of work out (changes spec 2026-10-18, item 3)`,
  )

  const failures = chatFailures(turns, chatSources())
  assert.deepEqual(failures, [], `the lines the homepage chat's turns speak are not the documents' own words (changes spec 2026-10-18, item 3):\n  ${failures.join('\n  ')}`)
})

test('the chat rule refuses a line the document it cites does not write', () => {
  const sources = chatSources()
  const turns = chat(builtPage(''))
  assert.deepEqual(chatFailures(turns, sources), [], 'the rule refuses the page it is written for, so every refusal below is the one thing it breaks')

  // A line with a word added to it: not a line of the skill, and the defect this rule exists
  // for — a chat that paraphrases what the agent says rather than quoting its skill.
  const paraphrased = turns.map((turn, index) => (index === 1 ? { ...turn, line: 'I will run each fix in its own job.' } : turn))
  assert.match(chatFailures(paraphrased, sources).join('\n'), /does not appear in plugin\/skills\/parallelism\/SKILL\.md/)

  // A line from the wrong document: the words are the repository's, and they are not this
  // turn's skill. The chat says which document each turn answers from, so this fails.
  const elsewhere = turns.map((turn, index) =>
    index === 1 ? { ...turn, source: 'plugin/skills/working/SKILL.md' } : turn,
  )
  assert.match(chatFailures(elsewhere, sources).join('\n'), /does not appear in plugin\/skills\/working\/SKILL\.md/)

  // The other half of the rule, and the half the launch line is about: the figure's own
  // labels may not be quotations. A status line that turned out to be a sentence of the
  // skill its turn cites would be a second thing the caption said was an illustration while
  // the page quoted it — and it is the shape a future edit would take, since the obvious way
  // to write a launch line is to copy the invocation the skill documents.
  const quotedStatus = turns.map((turn, index) =>
    index === 1 ? { ...turn, status: 'fugaro run --batch <name> --run-id <id> --task-file -' } : turn,
  )
  assert.match(
    chatFailures(quotedStatus, sources).join('\n'),
    /status line .* is a line of plugin\/skills\/parallelism\/SKILL\.md/,
    'a status line that is a sentence of the skill its turn cites was not refused, and the caption says the figure\'s labels are the illustration\'s own (changes spec 2026-10-18, item 3; D184)',
  )

  // And the same for a result label, the class of text the rule was written for.
  const quotedResult = turns.map((turn, index) => (index === 2 ? { ...turn, results: [...turn.results, 'It always ends with a pull request'] } : turn))
  assert.match(
    chatFailures(quotedResult, sources).join('\n'),
    /result label .* is a line of plugin\/skills\/working\/SKILL\.md/,
    'a result label that is a line of the skill its turn cites was not refused (changes spec 2026-10-18, item 3; D184)',
  )

  // A turn citing a document nothing reads, and a turn with no text at all.
  assert.match(chatFailures([{ ...turns[0], source: 'website/content/cli-reference.md' }], sources).join('\n'), /not a document this check reads/)
  assert.match(chatFailures([{ ...turns[0], line: '' }], sources).join('\n'), /has no text/)
})

test('the homepage launches through the agent, and the one command it offers the reader is fugaro watch', () => {
  // Item 3's acceptance, read off the built page: a first-time reader has to come away with
  // "my agent runs this", and the only command the page offers to copy besides the install
  // paths is `fugaro watch`. The old hero example — a `fugaro run` line the reader was meant
  // to type — is the thing this case is written to notice coming back.
  //
  // **The commands are read off the whole homepage rather than off a `.hero` section.** The
  // changes spec of 2026-10-18 (item 4, and the review of this branch) takes the chat and the
  // install paths out of the hero and puts them under the picture, so the hero holds the
  // promise and its call to action and nothing else; what a reader is offered to copy is a
  // fact about the page. A case that read a `.hero` section would pass on a page that offered
  // a `fugaro run` line anywhere else in it.
  const page = parse(builtPage(''))
  const commands = [...walk(page)].filter((el) => el.name === 'pre').map((el) => decode(readableText(el)).trim())
  assert.ok(
    commands.includes('fugaro watch'),
    `the homepage's command blocks are ${JSON.stringify(commands)}, and \`fugaro watch\` is the one command the reader is asked for (changes spec 2026-10-18, item 3)`,
  )
  assert.ok(
    !commands.some((command) => /^fugaro run\b/.test(command)),
    `the homepage offers ${JSON.stringify(commands)} as commands the reader can copy, and a \`fugaro run\` line is the manual path the documentation keeps rather than what the reader types (changes spec 2026-10-18, item 3)`,
  )
  assert.ok(
    !text(builtPage('')).includes('add a --verbose flag to the export command'),
    'the homepage still carries the old hero example, the `fugaro run "add a --verbose flag to the export command"` transcript (changes spec 2026-10-18, item 3: "Remove `fugaro run` from the hero example")',
  )

  // The launch is the agent's, drawn as a subdued line: a paragraph with no copy control of
  // its own — what the reader copies here is `fugaro watch`, not the launch.
  const status = findAll(page, (el) => CHAT_CLASSES(el).includes('turn-status'))[0]
  assert.ok(status, "the homepage's chat carries no status line, and item 3 asks for the launch to show as a subtle tool-call / status line (changes spec 2026-10-18, item 3)")
  assert.equal(status.name, 'p', `the status line is a <${status.name}>, and a command block is what a reader is offered to copy: the agent's launch is a line of the illustration (changes spec 2026-10-18, item 3)`)
  assert.equal(
    [...walk(status)].filter((el) => el.attrs.has('data-copy-button') || el.attrs.has('data-copy')).length,
    0,
    'the status line carries a copy control, and the reader did not type this line: the homepage offers exactly one command to copy (changes spec 2026-10-18, item 3)',
  )
  // And what it says is a status rather than a command: no `fugaro run` invocation, no flags,
  // and one short line. The exact words are the illustration's own (item 3 gives
  // "Fugaro: launching 3 jobs" as an example rather than as a caption to match), so this case
  // holds the shape and not the sentence.
  const said = decode(readableText(status)).replace(/\s+/g, ' ').trim()
  assert.doesNotMatch(
    said,
    /\bfugaro\s+run\b|--[a-z]/,
    `the status line ${JSON.stringify(said)} is a command, and the launch is what the agent did rather than something the reader types (changes spec 2026-10-18, item 3)`,
  )
  assert.ok(
    said.length > 0 && said.length <= 40,
    `the status line is ${said.length} characters (${JSON.stringify(said)}), and it is one short line under the agent's answer rather than a sentence: the launch is a status (changes spec 2026-10-18, item 3)`,
  )
})

test('the chat says on the page that it is an illustration, and claims no more than that', () => {
  // D184's rule for the transcript this replaces, kept for the chat: the page says what the
  // figure is rather than leaving a reader to take an illustration for a session that
  // happened. And it claims nothing about where the lines come from, because the figure is
  // not all quotations: the request is item 3's own example of one, and the three results are
  // the illustration's labels. The caption this case was written for said "every line is the
  // repository's own words, from the skills the agent reads", which is false about both of
  // those, and the words themselves are refused by `website/test/wording.mjs` — on the built
  // page and in the site's sources, so the sentence cannot come back through a component.
  const page = builtPage('')
  const caption = classText(page, 'chat-caption')

  assert.ok(caption, 'the homepage chat carries no caption, and item 3 admits the illustration only as a labelled one rather than as a recording (changes spec 2026-10-18, item 3; D184)')
  assert.match(caption, /illustration/i, `the chat's caption ${JSON.stringify(caption)} does not say the chat is an illustration (changes spec 2026-10-18, item 3)`)
  assert.match(caption, /not a recording/i, `the chat's caption ${JSON.stringify(caption)} does not say the lines are not a recording of a session (D184)`)
  assert.doesNotMatch(
    caption,
    /own words|quotation|quoted|verbatim/i,
    `the chat's caption ${JSON.stringify(caption)} claims the figure's lines are quotations, and two of the things a reader reads in it are not: the request is item 3's own example, and the three results are labels for work a run would report. The caption says what the picture is and where the agent's behaviour comes from; it does not vouch for where every line came from (changes spec 2026-10-18, item 3; Global Constraint 6)`,
  )
  assert.match(
    caption,
    /skills/i,
    `the chat's caption ${JSON.stringify(caption)} does not say where the agent's behaviour comes from, and the one thing about the figure a reader can check is that the agent's lines are its skills' (changes spec 2026-10-18, item 3)`,
  )

  // And it sits **directly under the conversation** (review round 3, item 3a). It used to be
  // the figure's last child with the watch block — the heading "Watch it run", the slot, the
  // command bar and the skill's note — between it and the turns, so a reader met the caption
  // after the heading of a different thing. It is the figure's last child still (that is the
  // content model: a `figcaption` comes first or last), and the watch block is the figure's
  // sibling now, so "last child" and "under the conversation" are the same place.
  const parsed = parse(page)
  const figure = findAll(parsed, (el) => el.name === 'figure' && CHAT_CLASSES(el).includes('chat'))[0]
  assert.ok(figure, 'the homepage renders no <figure class="chat">, and the caption is the figure\'s own name (changes spec 2026-10-18, item 3)')
  const named = [...walk(figure)].find((el) => CHAT_CLASSES(el).includes('chat-caption'))
  const children = (figure.children ?? []).filter((child) => child.name !== undefined)
  assert.equal(
    children[children.length - 1],
    named,
    "the chat's caption is not the figure's last child, and a `figcaption` is a figure's first or last child: this is the shape the HTML content model allows, and it is kept because the caption has to be reachable under the conversation (changes spec 2026-10-18, item 3a)",
  )
  const order = [...walk(parsed)]
  const heading = order.find((el) => CHAT_CLASSES(el).includes('chat-watch-title'))
  assert.ok(
    heading === undefined || order.indexOf(named) < order.indexOf(heading),
    "the chat's caption comes after the `fugaro watch` heading, and the review of round 3 found it there: the caption is what the conversation above it is, so it belongs at the foot of the conversation rather than under the heading of the block below it (changes spec 2026-10-18, item 3a)",
  )

  // And the fact the caption may not claim, read off the figure itself: the results are not
  // lines of the document the turn that reports them cites. They are the illustration's own
  // labels — a run that has not happened has no pull request numbers — so a caption that
  // called them quotations would be false, and a future edit that made them quotations of a
  // document would make this case fail until the caption said so.
  const report = chat(page).at(-1)
  assert.ok(report, 'the homepage chat renders no turns, so the results the caption is about cannot be read')
  const document = chatSources()[report.source]
  assert.ok(document, `the chat's last turn cites ${JSON.stringify(report.source)}, which is not a document this check reads`)
  for (const result of report.results) {
    assert.ok(
      !normalize(document).includes(normalize(result)),
      `the chat's result ${JSON.stringify(result)} is a line of ${report.source}, and the caption's reading of the figure is that the results are the illustration's own labels rather than quotations (changes spec 2026-10-18, item 3; D184)`,
    )
  }

  // And the same for the status line, the other thing on the figure that is not a quotation:
  // it is the illustration's label for the launch, so it may not be a sentence of the skill
  // its turn cites either. `chatFailures` refuses both; this case reads the built page for
  // the same thing, which is what makes the caption's reading true of the page a reader gets.
  const launch = chat(page).find((turn) => turn.status)
  assert.ok(launch, 'the homepage chat renders no status line, and the launch is the line the caption does not claim as a quotation')
  const launchDocument = chatSources()[launch.source]
  assert.ok(launchDocument, `the chat's launching turn cites ${JSON.stringify(launch.source)}, which is not a document this check reads`)
  assert.ok(
    !normalize(launchDocument).includes(normalize(launch.status)),
    `the chat's status line ${JSON.stringify(launch.status)} is a line of ${launch.source}, and the caption's reading of the figure is that the launch line is the illustration's own rather than a quotation (changes spec 2026-10-18, item 3; D184)`,
  )
})

test('the design record quotes the caption the site ships', () => {
  // The record tells a reader what the chat's caption says, and it quotes the sentence to do
  // it. A quote rots the moment the caption is rewritten, and this one has outlived a caption
  // already: review round 5 cut the caption to one sentence, and the record went on quoting
  // the long one — a sentence that existed nowhere on the site, and nothing said so, because
  // the caption's own case above reads the built page and never the record. The record is a
  // description of this branch's artifact (P9), so what it quotes of the artifact is held to
  // the artifact here, in the file that already holds the caption to its shape.
  const caption = classText(builtPage(''), 'chat-caption')
  assert.ok(caption, 'the homepage chat carries no caption, so there is nothing for the design record to quote (changes spec 2026-10-18, item 3)')
  const record = repoFile('docs/website/redesign/README.md')
  assert.ok(
    normalize(record).includes(normalize(caption)),
    `docs/website/redesign/README.md does not quote the caption the built page ships. The page says ${JSON.stringify(caption)}; the record's reading of the caption quotes a sentence that is not on the site. The record describes the site this branch builds, so the sentence it quotes moves with the caption (P9, changes spec 2026-10-18, item 3)`,
  )
})

test('the watch snippet is the command the reader keeps, and it copies itself', () => {
  const page = builtPage('')
  const command = classText(page, 'chat-watch-command')

  assert.equal(command, 'fugaro watch', `the hero's watch snippet reads ${JSON.stringify(command)}, and the one command the reader is asked for is \`fugaro watch\` (changes spec 2026-10-18, item 3)`)
  assert.ok(
    repoFile('README.md').includes(command),
    `the snippet offers ${JSON.stringify(command)}, which README.md does not print: the command is the documentation's rather than one written for the page (Global Constraint 9)`,
  )

  // And it is a command block like every other one on the site: one control, on the command
  // (P6, D183). The skill's own line about what the screen shows goes under it, quoted.
  const block = [...walk(parse(page))].find((el) => CHAT_CLASSES(el).includes('chat-watch'))
  assert.ok(block?.attrs.has('data-copy'), "the watch snippet is in no [data-copy] block, and it is a command a reader copies like any other (P6)")
  assert.equal(
    [...walk(block)].filter((el) => el.attrs.has('data-copy-button')).length,
    1,
    "the watch snippet does not carry exactly one copy control (P6, D183)",
  )
  const note = classText(page, 'chat-watch-note')
  assert.ok(
    normalize(repoFile('plugin/skills/working/SKILL.md')).includes(normalize(note)),
    `the note under the snippet, ${JSON.stringify(note)}, is not a line of plugin/skills/working/SKILL.md: the page quotes the skill rather than describing what the screen does (Global Constraint 6)`,
  )
})

test('the slot for the watch screenshot is marked, empty, and adds no image', () => {
  // Item 3 asks for a clearly marked slot right after the chat, and for the image itself to
  // be a separate task's: so the slot is here, it is empty, and this page adds no picture of
  // a screen the run does not have (Global Constraint 9, D107's rule for the hero's video).
  const page = parse(builtPage(''))
  const slot = findAll(page, (el) => el.attrs.get('data-slot') === 'watch-screenshot')[0]
  assert.ok(slot, 'the homepage carries no [data-slot="watch-screenshot"], and item 3 asks for a clearly marked slot for the `fugaro watch` screenshot (changes spec 2026-10-18, item 3)')
  assert.deepEqual(
    [...walk(slot)].map((el) => el.name),
    [],
    'the slot for the watch screenshot carries elements of its own: it is the place a later task puts the image, and until then it holds nothing (changes spec 2026-10-18, item 3)',
  )
  assert.equal(textOf(slot).trim(), '', `the slot carries the text ${JSON.stringify(textOf(slot).trim())}, and a slot is not a caption (changes spec 2026-10-18, item 3)`)
  for (const attribute of ['src', 'srcset', 'href', 'style', 'aria-label', 'role']) {
    assert.ok(!slot.attrs.has(attribute), `the slot carries ${attribute}, and the hook a task and this check read is \`data-slot\` alone (changes spec 2026-10-18, item 3)`)
  }

  // "right after the chat": the slot opens the block under the conversation, directly beneath
  // the heading of the thing the screenshot shows — the `fugaro watch` screen — so it is where
  // item 3 puts it. It used to sit inside the chat's figure, between the turns and the watch
  // bar; the review of round 3 moved the caption to the foot of the conversation (item 3a),
  // and a `figcaption` is a figure's first or last child, so the watch block — heading, slot,
  // bar, note — is the figure's sibling and carries the slot with it.
  const heading = findAll(page, (el) => CHAT_CLASSES(el).includes('chat-watch-title'))[0]
  assert.ok(heading, 'the homepage carries no "Watch it run" heading, and the screenshot slot and the command below it belong to a named block (changes spec 2026-10-18, item 3)')
  const siblings = (heading.parent.children ?? []).filter((child) => child.name !== undefined)
  assert.equal(
    siblings[siblings.indexOf(heading) + 1],
    slot,
    'the slot for the watch screenshot is not the element after the `fugaro watch` heading, and item 3 puts it right after the chat, with the command bar below it (changes spec 2026-10-18, item 3)',
  )
  const figure = findAll(page, (el) => el.name === 'figure' && CHAT_CLASSES(el).includes('chat'))[0]
  assert.ok(figure, 'the homepage renders no <figure class="chat">, and the conversation and its caption are the figure (changes spec 2026-10-18, item 3)')
  for (let parent = slot.parent; parent; parent = parent.parent) {
    assert.notEqual(
      parent,
      figure,
      "the watch screenshot's slot is inside the chat's figure, and the caption under the conversation is the figure's last child: a slot after it is markup the HTML content model rejects, and the slot belongs to the `fugaro watch` block the screenshot is of (changes spec 2026-10-18, item 3a)",
    )
  }

  // And no image came with it: the wordmark is the homepage's only picture, so a screenshot
  // committed by mistake — the one thing this run must not add — is this failure.
  const pictures = [...walk(page)]
    .filter((el) => ['img', 'picture', 'video', 'source'].includes(el.name))
    .map((el) => el.attrs.get('src') ?? el.attrs.get('srcset') ?? '')
  assert.deepEqual(
    pictures,
    ['/logo.png'],
    `the homepage renders the pictures ${JSON.stringify(pictures)}, and this run adds none: the wordmark is the page's only image, and the screenshot is a separate task's (changes spec 2026-10-18, item 3)`,
  )
})

test("the flow carries the changes spec's four steps, as one component on both pages", () => {
  const ruling = item4()
  assert.equal(ruling.steps.length, 4, `the changes spec's item 4 names ${ruling.steps.length} steps, and the picture draws four (changes spec 2026-10-18, item 4)`)

  // The homepage's flow sits under the hero, which is where P8 puts it: the picture of a run
  // above the tagline would be the first thing a reader meets, before anything has said what
  // Fugaro is. Held as "outside the hero and after it" rather than as an offset in the page's
  // text: a flow rendered inside the hero — under the chat, say — starts after the hero's
  // opening tag too, and is the same defect.
  const home = parse(builtPage(''))
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/)
  const hero = findAll(home, (el) => el.name === 'section' && classes(el).includes('hero'))[0]
  const figure = findAll(home, (el) => el.name === 'figure' && classes(el).includes('flow'))[0]
  assert.ok(hero, 'the homepage has no hero section, and the flow is placed below it (P8)')
  assert.ok(figure, 'the homepage renders no <figure class="flow">, and the flow is the picture item 4 asks for (P8, spec item 4)')
  let insideHero = false
  for (let parent = figure.parent; parent; parent = parent.parent) if (parent === hero) insideHero = true
  assert.ok(!insideHero, 'the homepage renders its flow inside the hero section: the picture of the run goes below the hero, not within it (P8)')
  const order = [...walk(home)]
  assert.ok(order.indexOf(hero) < order.indexOf(figure), 'the homepage renders its flow above the hero: the picture of the run goes below the hero (P8)')

  // Both pages, because the flow is the homepage's picture of the run as much as the How It
  // Works page's opening: item 4 asks for the SAME diagram on both, from one shared component.
  for (const route of ['', 'how-it-works']) {
    const failures = flowFailures(builtPage(route), ruling, captionSources())
    assert.deepEqual(
      failures,
      [],
      `the ${route === '' ? '/' : `/${route}/`} page's flow is not the one the changes spec's item 4 describes:\n  ${failures.join('\n  ')}`,
    )
  }

  // "one shared component, not two copies" (changes spec item 4), held to the artifact rather
  // than to the source: the two pages render the same figure, character for character, so a
  // page that grew a second copy of the picture — a copy that can drift from the first — is
  // this failure rather than two pictures that quietly disagree.
  const figureMarkup = (route) => {
    const page = builtPage(route)
    const start = page.indexOf('<figure class="flow"')
    assert.notEqual(start, -1, `the ${route === '' ? '/' : `/${route}/`} page renders no <figure class="flow">, and the picture is on both pages (changes spec 2026-10-18, item 4)`)
    return page.slice(start, page.indexOf('</figure>', start))
  }
  assert.equal(
    figureMarkup('how-it-works'),
    figureMarkup(''),
    'the homepage and How It Works render different flow markup: item 4 asks for the same diagram on both pages, from one component rather than two copies (changes spec 2026-10-18, item 4)',
  )

  // And the copy that told the run again on the homepage is gone (item 4: "the duplicate
  // text-heavy 'how it works' section is REMOVED"): the homepage carries the picture, and the
  // detail belongs to the page that opens with it. A section that came back would be a second
  // description of the run to keep in step with the first.
  assert.equal(
    runDetailOf(builtPage('')).sections,
    0,
    'the homepage renders a run-detail section: item 4 removes the duplicate text-heavy "how it works" section from the homepage and leaves the detail to the How It Works page (changes spec 2026-10-18, item 4)',
  )
  assert.equal(
    runDetailOf(builtPage('how-it-works')).sections,
    1,
    'the How It Works page renders no run-detail section, and item 4 puts the detailed explanation under the picture there (changes spec 2026-10-18, item 4)',
  )
})

test('the flow refusals fire on the defect each one names', () => {
  // The cases below are the check's own proof: the fixture page passes, and each fixture
  // that breaks one rule of the flow makes exactly that rule's line appear. Without them a
  // check that read no steps at all would pass every page.
  const ruling = item4()
  const sources = captionSources()
  const failing = (steps) => flowFailures(flowPage(steps), ruling, sources)

  assert.deepEqual(failing(fixtureSteps()), [], 'the fixture page does not pass the check it is the control for')

  const dropped = fixtureSteps().slice(0, -1)
  assert.ok(
    failing(dropped).some((line) => /steps are .*and the changes spec's item 4 names/.test(line)),
    'a step removed from the flow passes the check: the four steps are what item 4 draws, and a fifth or a missing one has to fail',
  )

  const reordered = [...fixtureSteps()]
  ;[reordered[2], reordered[3]] = [reordered[3], reordered[2]]
  assert.ok(
    failing(reordered).some((line) => /steps are .*and the changes spec's item 4 names/.test(line)),
    "two steps swapped pass the check: the order is the run's order, and a flow that drew the pull request before the job describes a run that cannot happen",
  )

  const paraphrased = fixtureSteps().map((step, i) => (i === 2 ? { ...step, caption: 'The agent works in the container.' } : step))
  assert.ok(
    failing(paraphrased).some((line) => /does not appear in README\.md#How it works/.test(line)),
    'a caption written for the page passes the check: every caption is a line of the section it cites (Global Constraint 6, RF11)',
  )

  const elsewhere = fixtureSteps().map((step, i) => (i === 0 ? { ...step, source: 'website/content/cli-reference.md' } : step))
  assert.ok(
    failing(elsewhere).some((line) => /held to a section of README\.md/.test(line)),
    'a caption attributed to a document the flow may not quote passes the check: every caption comes from a section of the README',
  )

  // Both captions of the two steps that share a section are lines of that section, so a check
  // that held each one to its section and no further would let them swap among the steps: the
  // page would then say the run opens a pull request that is done, then gone.
  const swapped = fixtureSteps().map((step, i) =>
    i === 2 ? { ...step, caption: fixtureSteps()[3].caption } : i === 3 ? { ...step, caption: fixtureSteps()[2].caption } : step,
  )
  assert.ok(
    failing(swapped).some((line) => /at or after the step before it/.test(line)),
    'the job step\'s caption and the pull request step\'s swapped pass the check: each is still a line of README.md\'s "## How it works", and the order is what keeps each on the step it describes',
  )

  // The heading a reader sees, which is not the `data-step` this check reads: a page whose
  // headings said something else entirely would otherwise pass on its attributes alone.
  const retitled = fixtureSteps().map((step, i) => (i === 2 ? { ...step, heading: 'A container does the work' } : step))
  assert.ok(
    failing(retitled).some((line) => /heading reads .*and the changes spec's item 4 names it/.test(line)),
    "a step heading replaced with a sentence of the page's own passes the check: the heading is the name a reader reads, and item 4 names it",
  )

  // Item 4's picture is four steps with one short line each, and D185's two annotations under
  // the job step are detail that moved into the How It Works page's explanation.
  const annotated = fixtureSteps().map((step, i) =>
    i === 2 ? { ...step, nested: '<li class="annotation"><span class="annotation-name">cache</span></li>' } : step,
  )
  assert.ok(
    failing(annotated).some((line) => /carries 1 nested list item\(s\)/.test(line)),
    'a sub-list under a step passes the check: the picture carries the four steps and nothing under them (changes spec 2026-10-18, item 4)',
  )

  const arrowless = fixtureSteps().map((step, i) => (i === 1 ? { ...step, arrow: false } : step))
  assert.ok(
    failing(arrowless).some((line) => /carries 2 arrows between 4 steps/.test(line)),
    'a step with no arrow after it passes the check: the picture is a flow, and the arrows are what make it one',
  )

  // And the reader itself: a page that renders no flow is the failure a check reading
  // "no steps" would otherwise report as success.
  assert.deepEqual(
    flowFailures('<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>t</h1></main></body></html>', ruling, sources),
    ['the page renders 0 flows, and it renders one: the flow is the picture of the run (P8, spec item 4)'],
  )
})

test('the flow moves on scroll only for a reader who allows motion', () => {
  // Both pages, because the flow is on both and each page ships the component's CSS.
  const css = ['', 'how-it-works'].map((route) => builtCSS(builtPage(route))).join('\n')

  // The animation is a scroll-driven one on the steps, and the reduced-motion path is the
  // flow itself: a reader whose system asks for less motion gets every step in place,
  // because the animation that brings a step in is inside a guard that never opens for
  // them. That is why the guard is checked here rather than a fill mode (W1.4 step 3, D105).
  assert.match(
    css,
    /animation-timeline:\s*view\(\)/,
    'the flow has no scroll-driven animation, and W1.4 step 3 asks for one that reveals the steps as the reader reaches them',
  )
  assert.match(
    css.replace(/\s+/g, ''),
    /@supports\(animation-timeline:view\(\)\)/,
    'the scroll-driven animation is not behind a feature query: a browser that cannot run it has to render the flow as it stands rather than render nothing',
  )
  assert.match(
    css.replace(/\s+/g, ''),
    /@media\(prefers-reduced-motion:no-preference\)/,
    'the scroll-driven animation is not behind a preference query, so a reader who asked for less motion would get a flow that fades in as they scroll it (D105, W1.4 step 3)',
  )
})

test("every caption in the flow is a line of the section its step cites, in that section's order", () => {
  // The same rule `flowFailures` applies, said here one page at a time and with the page's
  // name in the message, because this is the acceptance a reader can check: the four steps say
  // what the repository says, in the order the repository says it. Two of the four quote
  // README.md's `## How it works`, and the cursor is what keeps them in it.
  const ruling = item4()
  const sources = captionSources()

  for (const route of ['', 'how-it-works']) {
    const where = route === '' ? '/' : `/${route}/`
    const flow = flowOf(builtPage(route))

    assert.equal(
      flow.steps.length,
      4,
      `the ${where} page's flow renders ${flow.steps.length} steps, and the changes spec's item 4 names four (changes spec 2026-10-18, item 4)`,
    )
    assert.deepEqual(
      flow.steps.map((step) => step.heading),
      ruling.steps,
      `the ${where} page's flow is headed ${JSON.stringify(flow.steps.map((step) => step.heading))}, and item 4 names ${JSON.stringify(ruling.steps)}: the headings are the names a reader reads (changes spec 2026-10-18, item 4)`,
    )

    const reached = new Map()
    for (const step of flow.steps) {
      const section = sources[step.source] ?? ''
      assert.ok(
        step.source in sources,
        `the ${where} page's ${JSON.stringify(step.name)} step attributes its caption to ${JSON.stringify(step.source)}, and a caption is held to a section of README.md (changes spec 2026-10-18, item 4)`,
      )
      const line = normalize(step.caption)
      const from = reached.get(step.source) ?? 0
      const at = normalize(section).indexOf(line, from)
      assert.notEqual(
        at,
        -1,
        `the ${where} page's ${JSON.stringify(step.name)} caption ${JSON.stringify(step.caption)} does not appear in ${step.source} at or after the step before it: every caption in the flow is the repository's own words in the order their section writes them, rather than the page's (changes spec 2026-10-18, item 4; Global Constraint 6)`,
      )
      reached.set(step.source, at + line.length)
    }
  }
})

test("the How It Works detail is README.md's own steps, in its own order, under the picture", () => {
  // Item 4's zoom-in: the same picture the homepage renders, and under it the detail README.md
  // already writes. Read out of the built page and held to the section's own numbered steps
  // rather than to a copy of them here, so a page that reworded a step is a failure rather
  // than a second description of a run (Global Constraint 6, D102).
  const detail = runDetailOf(builtPage('how-it-works'))
  assert.equal(detail.sections, 1, 'the How It Works page renders no run-detail section, and item 4 puts the detailed explanation under the picture (changes spec 2026-10-18, item 4)')
  assert.ok(detail.title, 'the detail carries no heading, and a section that opens without one leaves the steps hanging under the picture (W1.11)')

  const failures = runDetailFailures(detail, howItWorksSteps(), runDetailSources())
  assert.deepEqual(failures, [], `the How It Works detail is not README.md's own run (changes spec 2026-10-18, item 4):\n  ${failures.join('\n  ')}`)
})

test('the detail refusals fire on the defect each one names', () => {
  // The detail's own proof, as the flow's: the fixture passes, and each fixture that breaks
  // one rule of the detail makes exactly that rule's line appear.
  const steps = fixtureDetailSteps()
  const sources = runDetailSources()
  const failing = (detailSteps, note) => runDetailFailures(runDetailOf(runDetailPage(detailSteps, note ?? detailNote())), steps, sources)

  assert.deepEqual(failing(steps), [], 'the fixture detail does not pass the check it is the control for')

  const dropped = steps.slice(0, -1)
  assert.ok(
    failing(dropped).some((line) => /renders 4 steps, and README\.md's "## How it works" numbers 5/.test(line)),
    'a step removed from the detail passes the check: the detail is the section\'s five steps, and one missing is a run described as shorter than it is',
  )

  const paraphrased = steps.map((step, i) => (i === 1 ? { ...step, line: 'Synchronise the repository.' } : step))
  assert.ok(
    failing(paraphrased).some((line) => /is not a line of README\.md's "## How it works"/.test(line)),
    'a step line written for the page passes the check: every line is the step\'s own words (Global Constraint 6, D102)',
  )

  // Both lines are lines of the section, so a check that held each one to the section and no
  // further would let two neighbouring steps swap their lines: the page would then restore the
  // caches after the agent had run.
  const swapped = steps.map((step, i) => (i === 1 ? { ...step, line: steps[2].line } : i === 2 ? { ...step, line: steps[1].line } : step))
  assert.ok(
    failing(swapped).some((line) => /at or after the step before it/.test(line)),
    "two steps' lines swapped pass the check: each is still a line of the section, and the order is what keeps each on the step it describes",
  )

  const renamed = steps.map((step, i) => (i === 3 ? { ...step, heading: 'The pull request' } : step))
  assert.ok(
    failing(renamed).some((line) => /is headed .*and README\.md names step 4/.test(line)),
    'a step heading written for the page passes the check: the headings are the section\'s own names for its steps',
  )

  const elsewhere = steps.map((step, i) => (i === 0 ? { ...step, heading: 'Setup' } : step))
  assert.ok(
    failing(elsewhere).some((line) => /is not a line of README\.md's "## How it works"/.test(line)),
    'a heading the section does not write passes the check: a step named for the page is not a step of the section',
  )

  const invented = 'The container is the trust boundary.'
  assert.ok(
    failing(steps, invented).some((line) => /is not a line of docs\/design\/v1\.md/.test(line)),
    'a note the design document does not write passes the check: the note is the one fact of the picture the steps do not state, and it is quoted like every other line',
  )

  assert.equal(
    runDetailOf(runDetailPage([], detailNote())).sections,
    1,
    'the fixture page renders no detail section, so every refusal above is about a page that has one',
  )
  assert.deepEqual(
    runDetailFailures({ sections: 0, title: '', steps: [], note: '' }, steps, sources),
    ['the page renders 0 detail sections, and it renders one: the detail is the zoom-in under the picture (changes spec 2026-10-18, item 4)'],
  )
})

test("every link on the two pages points at a route the plan's tasks write", () => {
  const routes = plannedRoutes()

  // A check that read no routes would pass every link, so the parse is asserted on the two
  // routes this task writes: if the plan's Files lines stop naming pages, this says so
  // instead of going quiet.
  assert.ok(
    routes.has('/') && routes.has('/how-it-works/'),
    `docs/website/implementation-plan.md's Files lines no longer name the site's pages: this check read ${routes.size} routes out of it, and it holds every link to that list`,
  )

  for (const route of ['', 'how-it-works']) {
    const page = builtPage(route)
    const where = route === '' ? '/' : `/${route}/`
    for (const href of internalLinks(page)) {
      assert.ok(
        routes.has(href),
        `the ${where} page links ${href}, which is not a route the plan's tasks write (the layout's nav and the hero's call to action included, because every link on these pages is checked here): a route outside that list is a 404 in the artifact, and it is Global Constraint 9's defect one page over from the download link it names (W1.4)`,
      )
    }
  }
})

test('the tokens the pages name are the ones the design layer declares', () => {
  // An undefined custom property makes its declaration invalid at computed-value time: the
  // style does not apply, nothing logs, and the page renders as if the rule were not written.
  // So the pages' CSS is read for every token name it asks for and every name it declares,
  // and the difference has to be empty. This is the check the hero needed: it named a
  // `--weight-bold` and a `--leading-relaxed` that W1.2's layer does not carry, and both
  // silently did nothing.
  const pages = ['', 'how-it-works'].map(builtPage)
  const css = pages.map(builtCSS).join('\n')
  const named = new Set([...css.matchAll(/var\((--[a-z0-9-]+)/g)].map((m) => m[1]))

  assert.ok(
    named.size > 0,
    "the pages' styles name no token at all, and every value they paint is a token of the design layer (W1.2, W1.4)",
  )

  if (!existsSync(path.join(SITE, 'src/styles/tokens.css'))) {
    // W1.2's layer is not in this tree — this branch is stacked under it, and W1.2 is merged
    // on main — so the names cannot be checked against it here. What can be checked is that
    // the layout is written to bundle the layer the moment it arrives: the failure this
    // guards is a site that ships every page unstyled while every check stays green, which
    // is what an `import.meta.glob` resolving to nothing looks like from outside. The check
    // below arms itself in any tree that carries the layer, and it was run in one.
    assert.match(
      repoFile('website/src/layouts/Base.astro'),
      /import\.meta\.glob\(\s*'\.\.\/styles\/tokens\.css'\s*,\s*\{\s*eager:\s*true\s*\}\s*\)/,
      'website/src/styles/tokens.css is not in this tree and the layout does not bundle it either, so the pages would ship unstyled with nothing to notice it (W1.2, W1.4)',
    )
    return
  }

  // The declarations the artifact carries, from its stylesheets and from a custom property a
  // page sets on an element inline. Nothing sets one today (the hero's terminal did, and item
  // 3 of the changes spec replaced it with the chat), so the pages contribute nothing to this
  // set — and the reading stays exactly the one that would catch a name a component asks for
  // and nothing declares.
  const declarations = new Set(
    [...`${css}\n${pages.join('\n')}`.matchAll(/(--[a-z0-9-]+)\s*:/g)].map((m) => m[1]),
  )
  const undeclared = [...named].filter((name) => !declarations.has(name))

  assert.deepEqual(
    undeclared,
    [],
    `the pages ask for ${undeclared.join(', ')}, and nothing in the artifact declares it: the declaration is invalid at computed-value time, so the style silently does not apply — a token name the design layer does not have is exactly this defect (W1.2, W1.4)`,
  )
})

test('neither page names a claim the product cannot back', () => {
  // The four sentences the reconciliation holds back (F50, D102), each as the thing a
  // reader could check for: a link to an artifact that does not exist, a platform the
  // repository does not build, a duration nothing measures, and ranges nothing computes.
  const heldBack = [
    ['a console download link', /releases\/download\//],
    ['Windows support, and WSL with it', /\bWSL\b|\bWindows\b/],
    ['the "~2 h, not 30 min" sentence', /~\s*2\s*h\b|\b2\s*h\b/],
    ['ballpark ranges from run history', /ballpark/i],
  ]

  for (const route of ['', 'how-it-works']) {
    const page = builtPage(route)
    for (const [what, pattern] of heldBack) {
      assert.ok(
        !pattern.test(page),
        `the ${route === '' ? '/' : `/${route}/`} page names ${what}, which the product cannot back today: the site publishes the measurement or nothing (F50, D102, W1.4 step 4)`,
      )
    }
  }
})
