// website/test/home.mjs — the hero, the benefits, the slider and the run detail (website plan,
// Task W1.4; website changes spec of 2026-10-18, items 2, 3 and 4; the owner's approved draft
// "Home, draft 2 (slider)", 2026-10-19).
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
//   3. **The prompt line is the request, quoted from the document it cites** (changes spec item
//      3; the owner's feedback of 2026-10-19). The request is the spec's own example, read from
//      `src/lib/agent-request.mjs` and held back to the document the page names in
//      `data-source`, rather than imagined — the page shows the reader a line that document
//      writes. The chat the same item asked for left the site with `AgentChat.astro` when the
//      owner's approved draft of 2026-10-19 ("Home, draft 2 (slider)") folded it into the
//      slider's first panel, so what this file holds to the repository instead is the run the
//      reader is shown: the watch block, which is the one command the page offers to copy
//      besides the install command (`fugaro watch`, with the old `fugaro run` example asserted
//      gone), and the slot for the screenshot, which is asserted to be marked and to hold
//      **the owner's real capture** (owner spec of 2026-10-18, item 5; PR #622) — the slot's
//      own hook, a non-empty `alt`, the capture's declared dimensions and a same-origin path,
//      so the page carries the wordmark and the working terminal and nothing else.
//   4. **The flow carries the changes spec's four steps, in order**, one short line each,
//      from one component on both pages: the names, and the caption of each step, are read
//      out of the spec's own item 4 and of the section of a document of this repository the
//      step cites, in that section's order — three of the four captions are lines of the
//      README, so a source alone would let them be shuffled among the steps. `ArchitectureDiagram.astro` supersedes D185's
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

/**
 * One `## <heading>` section of a document of this repository, up to the next `## ` — the
 * same reader `ArchitectureDiagram.astro` uses at build time, so the check and the build
 * resolve a section the same way. A heading's code span is a marker rather than part of its
 * name, which is why the search reads the headings with their backticks off: the section the
 * pull request's caption cites is `## Checkpoint pushes (\`git.pr.checkpoints\`)` and the key
 * in it is part of the name.
 */
function section(document, heading) {
  const text = repoFile(document)
  const at = [...text.matchAll(/^## (.+)$/gm)].find((match) => match[1].replace(/`/g, '') === heading)
  assert.ok(at, `${document} carries no "## ${heading}" section, and this check holds a caption to it`)
  const rest = text.slice(at.index)
  const next = rest.indexOf('\n## ', 1)
  return next === -1 ? rest : rest.slice(0, next)
}

/** One `## <heading>` section of README.md, up to the next `## `. */
function readmeSection(heading) {
  return section('README.md', heading)
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

/** An install path's note, with a folded scalar's line breaks read as the spaces they are. */
function installPathNote(id) {
  const folded = /^ {4}note: >-\n((?: {6}.*\n?)+)/m.exec(installPathBlock(id))
  const plain = /^ {4}note: (\S.*)$/m.exec(installPathBlock(id))
  const note = folded ? folded[1].split('\n').map((line) => line.trim()).join(' ') : plain?.[1]
  assert.ok(note, `website/data/install.yaml's ${id} path carries no note, and the homepage prints the note under the command`)
  return note.trim()
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
 * The sections a caption may be quoted from, by the name a step cites. A section rather than
 * the whole file: three of the four captions are lines of README.md, so held to the file
 * alone they could be shuffled among the steps and each would still be a quotation. Inside
 * one section the captions must also come in the order the section writes them, which is what
 * keeps two steps quoting the same section from swapping.
 *
 * The fourth cites `docs/git-providers.md`'s section on checkpoint pushes (the owner's
 * feedback of 2026-10-19): the pull request's caption is about when the pull request opens,
 * which is that document's fact rather than the README's, whose own line about the push is a
 * sentence about the branch.
 */
function captionSources() {
  return {
    'README.md#Getting started': readmeSection('Getting started'),
    'README.md#Why Fugaro': readmeSection('Why Fugaro'),
    'README.md#How it works': readmeSection('How it works'),
    'docs/git-providers.md#Checkpoint pushes (git.pr.checkpoints)': section('docs/git-providers.md', 'Checkpoint pushes (git.pr.checkpoints)'),
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
 * forward, so two steps quoting one section can only quote it in the order it writes them.
 * Held to their files alone the captions are lines of the repository and can be shuffled
 * among the steps with every one of them still a quotation, while the page then says Fugaro
 * is where the pull request opens.
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
      out.push(`${where} attributes its caption to ${JSON.stringify(step.source)}, and a caption is held to a section of a document this repository carries (changes spec 2026-10-18, item 4)`)
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
    {
      name: 'pull request',
      source: 'docs/git-providers.md#Checkpoint pushes (git.pr.checkpoints)',
      caption: 'the first checkpoint opens the draft PR, marked not verified',
    },
  ]
}

/**
 * Two steps quoting one section, for the ordering case: each step of the flow cites a section
 * of its own since the pull request's caption moved to `docs/git-providers.md` (the owner's
 * feedback of 2026-10-19), and the rule that a section's lines come in the section's order is
 * what has to keep holding. The lines are README.md's `## How it works` own — the job's line
 * from its diagram, and step 4's clause about the draft — in the order the section writes
 * them, so the pair is a real pair of that section's lines and not a shape invented here.
 */
function sharedSectionSteps() {
  const steps = fixtureSteps()
  steps[3] = { ...steps[3], source: 'README.md#How it works', caption: 'a draft opens at the first push (marked not verified)' }
  return steps
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
      // The heading is the step's name and nothing else. The number is a sibling of it — a
      // column of the step's own grid, the rail the line is drawn down — and the section's name
      // is what the heading has to carry. This used to strip a leading `3 ` back off the
      // heading, because the old markup put the number inside it; the strip is gone with that
      // markup, so a heading that swallowed its number again reads as the heading it is rather
      // than as the step's name, and is refused below.
      out.steps.push({
        name: el.attrs.get('data-step') ?? '',
        source: el.attrs.get('data-source') ?? '',
        heading: inside(el, 'run-step-name'),
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

/**
 * A page carrying nothing but a detail section, for the refusals above. The markup is the one
 * `RunDetail.astro` renders: the number a sibling of the heading and the heading's own line in
 * the column beside it, so the fixture the refusals are proved on is the page they are about.
 */
function runDetailPage(steps, note) {
  const body = steps
    .map(
      (step, index) =>
        `<li class="run-step" data-step="${slug(step.heading ?? step.name)}" data-source="${HOW_IT_WORKS_SOURCE}"><span class="run-step-number" aria-hidden="true">${index + 1}</span><div class="run-step-body"><h3 class="run-step-name">${step.heading ?? step.name}</h3><p class="run-step-line">${step.line}</p></div></li>`,
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

/**
 * README.md's `## Why Fugaro` reasons, as the section writes them: each `**N. Name.**`
 * paragraph, as its name and the paragraph the reason is argued in. The paragraph comes back
 * with the name because a benefit's line is held to **its own reason** and not to the
 * section: the four are one section, so a line held to the section alone could be moved from
 * the reason above it to the one below and still be a quotation.
 */
function readmeReasons() {
  const out = []
  for (const block of readmeSection('Why Fugaro').split(/\n{2,}/)) {
    const match = /^\*\*\d+\.\s*(.+?)\.\*\*/.exec(block.trim())
    if (match) out.push({ name: match[1], paragraph: block })
  }
  return out
}

/** The document the benefits section is quoted from, as the page and the component cite it. */
const BENEFITS_SOURCE = 'README.md#Why Fugaro'

/**
 * The attributes of an element as one stable string, without the build's own scope attribute:
 * what a glyph is drawn with is its drawing, and Astro's `data-astro-cid-…` is the same on
 * every element of the section rather than part of any picture.
 */
function attrsOf(el) {
  return [...el.attrs.entries()]
    .filter(([name]) => !name.startsWith('data-astro-cid-'))
    .map(([name, value]) => `${name}=${JSON.stringify(value)}`)
    .sort()
    .join(' ')
}

/**
 * The shapes an `<svg>` can be drawn with. Everything else inside one — a `<title>`, a
 * `<defs>`, a `<desc>` — is not a picture: a glyph whose whole content is a name for itself
 * draws nothing, and that is the defect the empty-drawing refusal below is about.
 */
const SHAPE_ELEMENTS = new Set(['path', 'circle', 'ellipse', 'rect', 'line', 'polyline', 'polygon'])

/**
 * The drawing inside one `<svg>`: its shapes and their own attributes, and nothing above it.
 * `''` when the icon has no shape in it at all, whatever else it may hold — which is what the
 * refusals below read as "this icon draws nothing".
 */
function drawingOf(svg) {
  return [...walk(svg)]
    .filter((el) => SHAPE_ELEMENTS.has(el.name))
    .map((el) => `${el.name} ${attrsOf(el)}`.trim())
    .join(';')
}

/**
 * The ways an `<svg>` could pull something in from outside itself, which is what an inline
 * icon may never do: an attribute that names another document, or an element whose whole job
 * is to reference one. `style` is on the list for the same reason and not for a different
 * one: a glyph painted by an inline style is a colour the palette does not carry and the
 * deployed policy's `style-src` would have to admit.
 */
const REFERRING_ATTRS = new Set(['href', 'xlink:href', 'src', 'srcset', 'style', 'poster'])
const REFERRING_ELEMENTS = new Set(['image', 'use', 'script', 'foreignobject', 'iframe', 'object', 'embed'])

/**
 * What one of a benefit's icons points at, or '' when it points at nothing (the good case).
 * The `<svg>` itself is read beside its descendants, because an `<svg href="…">` is a
 * reference as much as a `<use href="…">` inside one is.
 */
function referenceOf(svg) {
  for (const el of [svg, ...walk(svg)]) {
    for (const [name, value] of el.attrs ?? []) {
      if (REFERRING_ATTRS.has(name)) return `<${el.name} ${name}=${JSON.stringify(value)}>`
    }
    if (REFERRING_ELEMENTS.has(el.name)) return `<${el.name}>`
  }
  return ''
}

/**
 * The icons a benefit renders, as the facts the refusals below are about: one entry per
 * `<svg>` in the item, carrying what it draws, whether it is marked as decoration, and what
 * it references. Every `<svg>` under the item is one of its icons — the reader does not go
 * looking for a class name, so a second picture added without the class is still counted.
 */
function iconsOf(item) {
  const out = []
  for (const el of walk(item)) {
    if (el.name !== 'svg') continue
    out.push({
      drawing: drawingOf(el),
      hidden: el.attrs.get('aria-hidden') === 'true',
      focusable: el.attrs.get('focusable') === 'false',
      reference: referenceOf(el),
    })
  }
  return out
}

/** A built page's benefits, as the elements it renders: the section's title and its items. */
function benefitsOf(html) {
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/)
  const doc = parse(html)
  const sections = findAll(doc, (el) => el.name === 'section' && classes(el).includes('home-benefits'))
  const out = { sections: sections.length, title: '', source: '', items: [] }
  for (const section of sections) {
    const inside = (root, selector) => {
      const node = [...walk(root)].find((child) => classes(child).includes(selector))
      return node ? decode(readableText(node)).replace(/\s+/g, ' ').trim() : ''
    }
    out.title = inside(section, 'benefits-title')
    out.source = section.attrs.get('data-source') ?? ''
    for (const item of walk(section)) {
      if (item.name !== 'li' || !classes(item).includes('benefit')) continue
      out.items.push({
        name: inside(item, 'benefit-name'),
        line: inside(item, 'benefit-line'),
        icons: iconsOf(item),
      })
    }
  }
  return out
}

/**
 * Every failure of the benefits' rule, as the lines a reader of this file sees:
 *
 *   - **the page renders the README's reasons, all of them and in the README's order** — one
 *     benefit per reason, named with the README's own name for it (the number off, which is
 *     the README's device for counting and not part of the name). A reason the page drops is a
 *     claim the README makes that the page does not, and the owner's feedback of 2026-10-19
 *     asks for the section the README has rather than a selection from it;
 *   - **every line is a sentence of the reason its own name names.** Held to the section alone
 *     the four lines could be shuffled among the four reasons and each would still be a
 *     quotation, while the page sold the reader the sentence about safety under the name about
 *     models. Held to the reason's own paragraph, a line that moved stops the build (Global
 *     Constraint 6, D102);
 *   - **every benefit carries exactly one icon, it draws something, it is decoration, and it
 *     fetches nothing.** The owner's feedback of 2026-10-19 asks each point for an icon; so the
 *     page may not render a benefit with none, with two, with a `<svg>` that has no shape in it
 *     and so renders as an empty box, with an icon a screen reader is handed as content beside
 *     the name that already says the point, with one that is not marked out of the tab order,
 *     or with one that references a file or an origin — an icon font, an `<image>`, a
 *     `<use href>`, an inline style. **The empty drawing is the refusal no other gate can
 *     make**: the element is present, named and clean, and the reader sees nothing, which is
 *     the defect this round is about. The four glyphs are also held to being four: four points
 *     drawn with one picture is one icon used four times, and the picture then says nothing
 *     about the point under it.
 *
 * Written as a function over a benefits section and the README's reasons so a fixture can
 * prove it refuses.
 */
export function benefitFailures(benefits, reasons) {
  const out = []
  if (benefits.sections !== 1) {
    return [`the page renders ${benefits.sections} benefits sections, and it renders one: the section is the README's four reasons and not a figure (the owner's feedback of 2026-10-19)`]
  }
  if (benefits.title !== 'Benefits') {
    out.push(
      `the benefits section is headed ${JSON.stringify(benefits.title)}, and the section a reader looks for is headed "Benefits": the heading is the site's own label for the block, the way "Install" is the install block's (the owner's feedback of 2026-10-19)`,
    )
  }
  if (benefits.source !== BENEFITS_SOURCE) {
    out.push(
      `the benefits section cites ${JSON.stringify(benefits.source)}, and its names and lines are README.md's "## Why Fugaro" (Global Constraint 6)`,
    )
  }
  if (benefits.items.length !== reasons.length) {
    out.push(
      `the page renders ${benefits.items.length} benefit(s) and README.md's "## Why Fugaro" argues ${reasons.length}: the section is the README's reasons, and one dropped is a claim the README makes that the page does not (the owner's feedback of 2026-10-19)`,
    )
  }

  for (const [index, item] of benefits.items.entries()) {
    const reason = reasons[index]
    const where = `benefit ${index + 1} of the benefits`
    if (reason === undefined) {
      out.push(`${where}, ${JSON.stringify(item.name)}, has no reason to be: README.md's section argues ${reasons.length} and the page renders more (the owner's feedback of 2026-10-19)`)
      continue
    }
    if (item.name !== reason.name) {
      out.push(
        `${where} is named ${JSON.stringify(item.name)}, and README.md names reason ${index + 1} ${JSON.stringify(reason.name)}: the page renders the README's reasons in the README's order, with the numbers off (the owner's feedback of 2026-10-19; Global Constraint 6)`,
      )
    }
    if (!normalize(reason.paragraph).includes(normalize(item.line))) {
      out.push(
        `${where} carries the line ${JSON.stringify(item.line)}, which is not in README.md's reason ${index + 1}: every line of the benefits is a sentence of the reason its name names rather than one written for the page (the owner's feedback of 2026-10-19; Global Constraint 6)`,
      )
    }
  }

  // The icons: one per benefit, decoration, and nothing fetched (the owner's feedback of
  // 2026-10-19). Read per benefit rather than as a count over the section, because "four
  // icons for four benefits" is also true of one benefit with four of them and three with
  // none.
  for (const [index, item] of benefits.items.entries()) {
    const icons = item.icons ?? []
    const where = `benefit ${index + 1} of the benefits`
    if (icons.length !== 1) {
      out.push(
        `${where} carries ${icons.length} icon(s) and every point of the benefits carries one: the owner's feedback of 2026-10-19 asks each point for an icon, and a benefit drawn with none or with several is a point the picture does not mark (the owner's feedback of 2026-10-19)`,
      )
      continue
    }
    const [icon] = icons
    // **The icon has to draw something.** An `<svg>` with no shape in it is a point rendered
    // with nothing where the owner's feedback of 2026-10-19 asks for a picture, and it is the
    // one defect of this set that no other check in this file or in `a11y.mjs` can see: the
    // element is present, named, decorated and clean, and the reader gets an empty box. The
    // component keeps its glyphs as drawings rather than as template branches for the same
    // reason, and this is what holds the artifact to it.
    if (icon.drawing === '') {
      out.push(
        `${where}: its icon draws nothing (an <svg> with no shape in it), and an icon is what marks the point — a glyph that renders as an empty box is a point with no picture at all, which is the defect the owner's feedback of 2026-10-19 asks this section not to have`,
      )
    }
    if (!icon.hidden) {
      out.push(
        `${where}: its icon is not marked as decoration (aria-hidden="true"), and the picture is the point's — a screen reader handed the glyph as well reads the name beside it twice (the owner's feedback of 2026-10-19)`,
      )
    }
    if (!icon.focusable) {
      out.push(
        `${where}: its icon is not marked out of the tab order (focusable="false"), which is the other half of the decoration contract this site already writes on a drawing — \`a11y.mjs\` holds the flow's arrows to the same pair — and an icon is decoration on both (the owner's feedback of 2026-10-19)`,
      )
    }
    if (icon.reference !== '') {
      out.push(
        `${where}: its icon references something outside itself (${icon.reference}), and an icon of this site is inline SVG drawn with the page's own tokens — an icon font, an image file, a <use href> or an inline style is another thing a reader's browser has to fetch, or a colour the palette does not carry (the owner's feedback of 2026-10-19; Global Constraints 4 and 8)`,
      )
    }
  }

  // Four points, four pictures: a glyph repeated under two points is one icon used twice, and
  // the drawing then says nothing about the point it sits under.
  const drawings = benefits.items.flatMap((item) => (item.icons ?? []).map((icon) => icon.drawing))
  if (new Set(drawings).size !== drawings.length) {
    const repeated = drawings.filter((drawing, i) => drawings.indexOf(drawing) !== i)
    out.push(
      `the benefits draw ${drawings.length} icon(s) with ${new Set(drawings).size} distinct picture(s), so ${repeated.length} of them repeat another point's glyph (${JSON.stringify(repeated[0])}): each point carries a glyph of its own (the owner's feedback of 2026-10-19)`,
    )
  }
  return out
}

/**
 * One icon, in the shape the section writes one: an inline `<svg>` with a glyph inside it.
 * `body` is what it draws — or references, in the fixtures that plant a defect — and `marks`
 * are the attributes beyond the drawing, so a fixture can leave `aria-hidden` off or hang an
 * `href` on the `<svg>` itself.
 */
function iconMarkup(body, marks = 'aria-hidden="true" focusable="false"') {
  return `<svg class="benefit-icon" viewBox="0 0 24 24" ${marks}>${body}</svg>`
}

/** Four distinct drawings, one per point, so the fixture section is the page the gate passes. */
const FIXTURE_GLYPHS = ['M1 1h2v2', 'M3 3h2v2', 'M5 5h2v2', 'M7 7h2v2']

/** A page carrying nothing but a benefits section, for the refusals above. */
function benefitsPage(items, { title = 'Benefits', source = BENEFITS_SOURCE, sections = 1 } = {}) {
  const body = items
    .map(
      (item, index) =>
        `<li class="benefit">${item.icon ?? iconMarkup(`<path d="${FIXTURE_GLYPHS[index % FIXTURE_GLYPHS.length]}"></path>`)}<h3 class="benefit-name">${item.name}</h3><p class="benefit-line">${item.line}</p></li>`,
    )
    .join('')
  const section = `<section class="home-benefits" aria-labelledby="benefits-title" data-source="${source}"><h2 class="benefits-title" id="benefits-title">${title}</h2><ul class="benefit-list">${body}</ul></section>`
  return `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>t</h1>${section.repeat(sections)}</main></body></html>`
}

test('the benefits are README.md\'s four reasons, each with a line of its own reason', () => {
  // The owner's feedback of 2026-10-19: "we need a section with Benefits or somesuch as we
  // have in the README, no?" So the section is the README's, read off the built page and held
  // to the repository here: four reasons, the README's names for them in the README's order,
  // and for each one a sentence out of the paragraph that reason is argued in. A section whose
  // whole job is to persuade is the one place on a site where a sentence of the page's own
  // would go unnoticed, which is why every line is read back against the document it came from
  // rather than trusted to the component's own guard (Global Constraint 6, D102).
  const reasons = readmeReasons()
  assert.ok(reasons.length > 0, 'README.md\'s "## Why Fugaro" carries no reason this check can read, so the benefits were held to nothing')
  assert.deepEqual(
    benefitFailures(benefitsOf(builtPage('')), reasons),
    [],
    'the homepage\'s benefits are not the README\'s reasons',
  )
})

test('the benefits refusals fire on a paraphrase, a shuffled line and a dropped reason', (t) => {
  // Three defects, each planted alone beside the section that keeps the rule, so the gate is a
  // gate rather than a description of today's markup: a line rewritten for the page, a line
  // that moved from the reason above it to the one below, and a reason the page leaves out.
  const reasons = readmeReasons()
  const good = reasons.map((reason) => ({ name: reason.name, line: firstSentence(reason.paragraph) }))
  assert.deepEqual(benefitFailures(benefitsOf(benefitsPage(good)), reasons), [], 'the fixture section is not the README\'s own, so the three refusals below prove nothing')

  const paraphrased = good.map((item, index) => (index === 0 ? { ...item, line: 'Nothing runs on your laptop at all.' } : item))
  assert.match(
    benefitFailures(benefitsOf(benefitsPage(paraphrased)), reasons).join('\n'),
    /is not in README\.md's reason 1/,
    'a benefit line written for the page is not refused',
  )

  const shuffled = good.map((item, index) => (index === 0 ? { ...item, line: good[1].line } : item))
  assert.match(
    benefitFailures(benefitsOf(benefitsPage(shuffled)), reasons).join('\n'),
    /is not in README\.md's reason 1/,
    'a line moved from the reason above it to the one below is not refused',
  )

  assert.match(
    benefitFailures(benefitsOf(benefitsPage(good.slice(0, -1))), reasons).join('\n'),
    /renders 3 benefit\(s\) and README\.md's "## Why Fugaro" argues 4/,
    'a dropped reason is not refused',
  )

  const renamed = good.map((item, index) => (index === 0 ? { ...item, name: 'Safe' } : item))
  assert.match(
    benefitFailures(benefitsOf(benefitsPage(renamed)), reasons).join('\n'),
    /is named "Safe", and README\.md names reason 1/,
    'a benefit renamed by the page is not refused',
  )

  assert.match(
    benefitFailures(benefitsOf(benefitsPage(good, { title: 'Why Fugaro' })), reasons).join('\n'),
    /is headed "Why Fugaro"/,
    'a section that lost its heading is not refused',
  )

  assert.match(
    benefitFailures(benefitsOf(benefitsPage(good, { source: 'README.md' })), reasons).join('\n'),
    /cites "README\.md", and its names and lines are README\.md's "## Why Fugaro"/,
    'a section that cites the whole README instead of the section is not refused',
  )
})

test('the benefits refusals fire on a missing icon, a second one, a named glyph and a repeated one', () => {
  // The owner's feedback of 2026-10-19: "use an icon for each point". Five defects, each
  // planted alone beside the section that keeps the rule, so the gate is a gate rather than a
  // description of today's markup: a point with no icon, a point with two, an icon a screen
  // reader is handed as content, one that references another file, and four points drawn with
  // one picture between them. The fixture passes with its four distinct glyphs, which is what
  // makes the five refusals below about the planted defect and not about the fixture.
  const reasons = readmeReasons()
  const good = reasons.map((reason) => ({ name: reason.name, line: firstSentence(reason.paragraph) }))
  assert.deepEqual(benefitFailures(benefitsOf(benefitsPage(good)), reasons), [], 'the fixture section is not the README\'s own, so the five refusals below prove nothing')

  const plant = (index, icon) => good.map((item, i) => (i === index ? { ...item, icon } : item))
  const refusal = (items) => benefitFailures(benefitsOf(benefitsPage(items)), reasons).join('\n')

  assert.match(
    refusal(plant(0, '')),
    /benefit 1 of the benefits carries 0 icon\(s\) and every point of the benefits carries one/,
    'a benefit drawn with no icon is not refused',
  )

  assert.match(
    refusal(plant(0, iconMarkup('<path d="M1 1h2v2"></path>') + iconMarkup('<path d="M9 9h2v2"></path>'))),
    /carries 2 icon\(s\)/,
    'a benefit drawn with two icons is not refused',
  )

  assert.match(
    refusal(plant(0, iconMarkup('<path d="M1 1h2v2"></path>', 'focusable="false"'))),
    /benefit 1 of the benefits: its icon is not marked as decoration/,
    'an icon a screen reader is handed as content beside the name is not refused',
  )

  assert.match(
    refusal(plant(0, iconMarkup('<path d="M1 1h2v2"></path>', 'aria-hidden="true"'))),
    /benefit 1 of the benefits: its icon is not marked out of the tab order/,
    'an icon that is nothing of the decoration contract but its aria-hidden is not refused',
  )

  // The empty icon: an `<svg>` that renders as an empty box. Three shapes, a `<title>`, a
  // `<defs>` and nothing drawn — every one of them a picture a reader never sees, and the one
  // defect of this set that leaves the element present, named and clean.
  for (const [what, body] of [
    ['no children at all', ''],
    ['a title and nothing else', '<title>shield</title>'],
    ['an empty group', '<g></g>'],
    ['definitions with no shape', '<defs><clipPath id="c"></clipPath></defs>'],
  ]) {
    assert.match(
      refusal(plant(0, iconMarkup(body))),
      /benefit 1 of the benefits: its icon draws nothing/,
      `an icon of ${what} is not refused: a glyph that renders as an empty box is a point with no picture`,
    )
  }

  // A reference from inside the `<svg>` and one on the `<svg>` itself: an icon font or an
  // image file arrives through the first, and the second is the shape that has no glyph in it
  // at all.
  assert.match(
    refusal(plant(0, iconMarkup('<use href="/icons.svg#shield"></use>'))),
    /references something outside itself \(<use href="\/icons\.svg#shield">\)/,
    'an icon that pulls a glyph in from another file is not refused',
  )
  assert.match(
    refusal(plant(0, iconMarkup('<path d="M1 1h2v2"></path>', 'aria-hidden="true" focusable="false" style="stroke:red"'))),
    /references something outside itself \(<svg style="stroke:red">\)/,
    'an icon painted by an inline style is not refused',
  )

  assert.match(
    refusal(good.map((item) => ({ ...item, icon: iconMarkup('<path d="M1 1h2v2"></path>') }))),
    /draw 4 icon\(s\) with 1 distinct picture\(s\)/,
    'four points drawn with one glyph between them are not refused',
  )
})

/** The first sentence of a paragraph, for the fixture above: markdown markers taken out. */
function firstSentence(paragraph) {
  const text = paragraph.replace(/^\*\*[^*]+\*\*\s*/, '').trim()
  const end = text.indexOf('. ')
  return end === -1 ? text : text.slice(0, end + 1)
}

/**
 * The hero's prompt line, as the two facts about it this file reads: what it says, with the
 * prompt mark the page hides from a screen reader taken out (a reader reads the request, not
 * the mark in front of it), and the document its `data-source` cites. `null` when the hero
 * renders none, which is the failure the case below reports rather than an error here.
 */
function heroPrompt(page) {
  const hero = findAll(parse(page), (el) => el.name === 'section' && (el.attrs.get('class') ?? '').split(/\s+/).includes('hero'))[0]
  assert.ok(hero, "the homepage has no hero section, and the prompt line is the hero's own (the owner's feedback of 2026-10-19)")
  const prompt = [...walk(hero)].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('hero-prompt'))
  return prompt === undefined
    ? null
    : { text: decode(readableText(prompt)).replace(/\s+/g, ' ').trim(), source: prompt.attrs.get('data-source') ?? '' }
}

test("the hero's prompt line is the request, quoted from the document it cites", () => {
  // The owner's feedback of 2026-10-19: the home page has to say, in as few words as
  // possible, that a reader drives Fugaro through the coding agent they already have, and the
  // fewest words are the request — "Maybe just the `> Fan out these three bug fixes.`". Two
  // facts are read off the built page, and the second is what makes the line a quotation
  // rather than copy: it is a line of the document its `data-source` cites (Global Constraint
  // 6, D102 — a page renders a fact and never restates one), and it is the string
  // `website/src/lib/agent-request.mjs` holds, which is where the line is held to that
  // document at build time. The hero renders the module's export rather than typing the
  // sentence, and this case is what holds the artifact to that: a hero that typed its own
  // version of the request would show the reader a line the module does not carry.
  //
  // The module had a second renderer until the owner's approved draft of 2026-10-19 ("Home,
  // draft 2 (slider)") folded the chat into the slider: the chat's first turn spoke the same
  // sentence, and the case held the two renderings to each other. The pin is on the string,
  // so it reads the module's export and not a second copy of the line on the page.
  const page = builtPage('')
  const prompt = heroPrompt(page)
  assert.ok(prompt, "the hero renders no prompt line, and the owner's feedback of 2026-10-19 puts the request under the headline as what the page is about")
  assert.ok(prompt.text, "the hero's prompt line is empty: the page's whole statement about how a reader drives Fugaro is that line (the owner's feedback of 2026-10-19)")
  assert.ok(
    prompt.source,
    "the hero's prompt line cites no document, and every line on the site says which document it is quoted from (Global Constraint 6)",
  )
  assert.ok(
    normalize(repoFile(prompt.source)).includes(normalize(prompt.text)),
    `the hero's prompt line ${JSON.stringify(prompt.text)} is not a line of ${prompt.source}: the line is the repository's own example of a request, not a sentence written for the page (changes spec 2026-10-18, item 3; Global Constraint 6)`,
  )

  // **The line the page renders is the module's own string**, and the citation is the
  // module's too. Read off the module's source rather than imported: the module imports
  // `../content.ts` to find the repository root, and the test runner has no TypeScript loader
  // — the same reading `base-styles.mjs` and `budget.mjs` do of the components they are about.
  const module = repoFile('website/src/lib/agent-request.mjs')
  const request = /export const REQUEST = '([^']*)'/.exec(module)
  const source = /export const REQUEST_SOURCE = '([^']*)'/.exec(module)
  assert.ok(
    request && source,
    'website/src/lib/agent-request.mjs no longer exports the request and the document it is quoted from, and the hero renders both (the owner\'s feedback of 2026-10-19; changes spec 2026-10-18, item 3)',
  )
  assert.equal(
    prompt.text,
    request[1],
    `the hero's prompt line reads ${JSON.stringify(prompt.text)} and src/lib/agent-request.mjs holds ${JSON.stringify(request[1])}: the hero renders the module's string rather than typing its own version of the request (the owner's feedback of 2026-10-19)`,
  )
  assert.equal(
    prompt.source,
    source[1],
    `the hero's prompt line cites ${JSON.stringify(prompt.source)} and src/lib/agent-request.mjs names ${JSON.stringify(source[1])}: one sentence is quoted from one document, and two citations would be two places to keep in step (Global Constraint 6)`,
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

test("the homepage offers one install command, install.yaml's own, and types none of it", () => {
  const page = builtPage('')
  // Read as a document rather than sliced with a regex: the block holds a copy control as well
  // as the command (P6), and a non-greedy `<div>…</div>` would stop at the control's own
  // closing tag and check half a block.
  //
  // **One path rather than the file's five.** The owner's approved draft of 2026-10-19 ("Home,
  // draft 2 (slider)") replaces the two install cards the page used to end with by one "Get
  // started" button and the command most readers run: a reader choosing between Homebrew and
  // `go install` before they have decided to install anything is the choice the draft takes
  // away, and `/getting-started/` still lays every path out with what each one sets up. The
  // path rendered here is `HomeInstall.astro`'s `HOME_PATH`, and this case reads the command
  // out of the file that carries it rather than out of the component.
  const section = findAll(parse(page), (el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('home-install'))[0]
  assert.ok(section, "the homepage carries no install block, and the page ends with the one command most readers run (the owner's approved draft of 2026-10-19)")
  assert.equal(
    section.attrs.get('data-source'),
    'website/data/install.yaml',
    `the homepage's install block cites ${JSON.stringify(section.attrs.get('data-source'))}, and the command and the note under it are that file's (W0.1, W1.4 step 1; Global Constraint 6)`,
  )

  const commands = [...walk(section)]
    .filter((el) => el.name === 'pre')
    .map((el) => textOf(el).trim())
  assert.deepEqual(
    commands,
    [installPathCommands('homebrew')[0]],
    `the homepage's install commands are ${JSON.stringify(commands)}, and website/data/install.yaml's quick path prints ${JSON.stringify(installPathCommands('homebrew')[0])} and nothing else: a command typed into the component is a second copy of the install text, and the one that drifts (W0.1, W1.4 step 1)`,
  )
  assert.equal(
    classText(page, 'home-install-note'),
    installPathNote('homebrew'),
    'the note under the homepage\'s install command is not the quick path\'s own note: the file carries what the path sets up, and the component renders it (W0.1, Global Constraint 6)',
  )

  // **And the one control is the button**, which is the draft's call to action: one link, to
  // the page where the paths are laid out. A second link here — the `go install` card's, or a
  // release page's — is the choice the draft takes off this page.
  const links = [...walk(section)].filter((el) => el.name === 'a').map((el) => [textOf(el).trim(), el.attrs.get('href')])
  assert.deepEqual(
    links,
    [['Get started', '/getting-started/']],
    `the homepage's install block carries the links ${JSON.stringify(links)}, and the draft gives it one: the button that leads to the page where every path is laid out (the owner's approved draft of 2026-10-19)`,
  )

  // And the command is not in the hero: item 4 puts the promise and its one call to action
  // above the picture and the commands below it, so a command back in the hero is the shape the
  // review of this branch asked to have changed.
  const hero = findAll(parse(page), (el) => el.name === 'section' && (el.attrs.get('class') ?? '').split(/\s+/).includes('hero'))[0]
  assert.ok(hero, 'the homepage has no hero section, and the promise and its call to action are the hero (P5, D102)')
  assert.equal(
    [...walk(hero)].filter((el) => el.name === 'pre').length,
    0,
    'the hero prints a command, and item 4 gives the hero the promise and its call to action: the command is the install block below the slider (changes spec 2026-10-18, item 4)',
  )
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
  // Item 6 of the owner's brief of 2026-10-19 asks that the console be findable from the home
  // page's call to action, and `site.yaml`'s `features.console_downloads` is what decides
  // whether it is offered at all. (The changes spec of 2026-10-18 has an item 6 too, and it is
  // Getting Started's console option rather than this — `console-option.mjs` holds that one.)
  // This is the flag-off half over the build `npm run check` makes, and it applies only while
  // the checkout's flag is false. The checkout now ships the flag TRUE (the console's
  // `console-v0.9.0` release), so this case steps aside and `console-option.mjs` holds both
  // halves: the flag-on row off this build and the flag-off row off an unreleased copy.
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
    'the hero\'s call to action is not exactly the two links it carries while features.console_downloads is false: the console\'s link arrives with the flag, and until then the row says nothing about the console (the owner\'s brief of 2026-10-19, item 6)',
  )
  assert.ok(
    !/console/i.test(textOf(actions)),
    'the hero\'s call to action mentions the console while features.console_downloads is false, and the unreleased state is structural: nothing about the console renders before its release (item 6)',
  )
})

// ---- the watch block, and the capture (changes spec of 2026-10-18, item 3) ---------------

// The homepage's one command block and the screenshot beside it. Both were the chat's: the
// owner's approved draft of 2026-10-19 ("Home, draft 2 (slider)") folds the chat into the
// slider's third panel, which shows the reader what a run looks like from the outside —
// `fugaro watch` on their own machine, and the real screen. The illustration of a
// conversation left the site with `AgentChat.astro`; what stayed is the pair a reader can
// act on, so the cases below read the panel the draft puts them in.

test('the watch block is the command the reader keeps, and it copies itself', () => {
  const page = builtPage('')
  const command = classText(page, 'watch-command')

  assert.equal(command, 'fugaro watch', `the "See it" panel's command reads ${JSON.stringify(command)}, and the one command the reader is asked for is \`fugaro watch\` (changes spec 2026-10-18, item 3)`)
  assert.ok(
    repoFile('README.md').includes(command),
    `the panel offers ${JSON.stringify(command)}, which README.md does not print: the command is the documentation's rather than one written for the page (Global Constraint 9)`,
  )

  // And it is a command block like every other one on the site: one control, on the command
  // (P6, D183). The skill's own line about what the screen shows goes under it, quoted.
  const block = [...walk(parse(page))].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('watch-block'))
  assert.ok(block?.attrs.has('data-copy'), 'the watch block is in no [data-copy] block, and it is a command a reader copies like any other (P6)')
  assert.equal(
    [...walk(block)].filter((el) => el.attrs.has('data-copy-button')).length,
    1,
    'the watch block does not carry exactly one copy control (P6, D183)',
  )
  const note = classText(page, 'watch-note')
  assert.ok(
    normalize(repoFile('plugin/skills/working/SKILL.md')).includes(normalize(note)),
    `the note under the command, ${JSON.stringify(note)}, is not a line of plugin/skills/working/SKILL.md: the page quotes the skill rather than describing what the screen does (Global Constraint 6)`,
  )
})

test('the watch screenshot is in the slot the "See it" panel marks, named, sized and served from here', () => {
  // The owner's spec of 2026-10-18 item 5 and PR #622 supply the picture, and it is the
  // owner's real capture of `fugaro watch` running on their own installation, published as it
  // was taken (D190 waived by the owner for this one image, `docs/website/redesign/README.md`).
  // So the slot holds the image, and what this case is about is that it holds THE image and
  // holds it so it can be read: a picture no reader can identify, or one that shifts the block
  // under it when the file lands, or one fetched from somebody else's host, are the three ways
  // this goes wrong.
  const page = parse(builtPage(''))
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/)
  const slot = findAll(page, (el) => el.attrs.get('data-slot') === 'watch-screenshot')[0]
  assert.ok(slot, 'the homepage carries no [data-slot="watch-screenshot"], and the capture of the `fugaro watch` screen needs a marked slot to be checked in (changes spec 2026-10-18, item 3)')
  for (const attribute of ['src', 'srcset', 'href', 'style', 'aria-label', 'role']) {
    assert.ok(!slot.attrs.has(attribute), `the slot carries ${attribute}, and the hook this check reads is \`data-slot\` alone (changes spec 2026-10-18, item 3)`)
  }

  // **It is in the third panel, and it opens it.** The owner's approved draft of 2026-10-19
  // puts the capture in the "See it" tab, and the panel reads: its heading, then the picture,
  // then the command the reader runs to see the same screen. Held as structure — the panel's
  // own children — rather than as a class name, because the panel is what a reader switches to,
  // and a picture moved into the "Benefits" panel is a picture under a heading about something
  // else.
  const panel = findAll(page, (el) => el.attrs.get('id') === 'home-panel-see')[0]
  assert.ok(panel, 'the homepage carries no #home-panel-see, and the capture is what the draft\'s "See it" tab shows (the owner\'s approved draft of 2026-10-19)')
  let inPanel = false
  for (let parent = slot.parent; parent; parent = parent.parent) if (parent === panel) inPanel = true
  assert.ok(inPanel, 'the watch screenshot is not in the "See it" panel, and the draft puts the capture in the tab a reader opens to see what a run looks like (the owner\'s approved draft of 2026-10-19)')
  const children = (panel.children ?? []).filter((child) => child.name !== undefined)
  const heading = children.find((child) => child.name === 'h2')
  assert.ok(heading, 'the "See it" panel carries no heading, and the picture and the command below it belong to a named block (the owner\'s approved draft of 2026-10-19)')
  assert.equal(
    children[children.indexOf(heading) + 1],
    slot,
    'the slot for the watch screenshot is not the element after the "See it" heading: the panel reads heading, picture, command (the owner\'s approved draft of 2026-10-19)',
  )
  assert.ok(
    !classes(slot).includes('watch-block') && findAll(page, (el) => classes(el).includes('watch-block'))[0],
    'the watch command block is gone from the page, and the panel offers the reader the command that shows them the screen the picture is of (changes spec of 2026-10-18, item 3)',
  )

  // **The picture is IN the slot**, exactly one of it: the slot is where the capture goes, and
  // an image beside the slot is a picture this page renders outside the place the page says it
  // renders it.
  const images = [...walk(slot)].filter((el) => el.name === 'img')
  assert.equal(
    images.length,
    1,
    `the watch screenshot's slot holds ${images.length} <img> element(s), and it is the one place the homepage shows the \`fugaro watch\` screen (owner spec 2026-10-18, item 5)`,
  )
  const image = images[0]

  // **The path is this site's own**: a root-relative path under `/images/`, which is the file
  // PR #622 committed, and not a URL — of any scheme, or a protocol-relative `//host/path`.
  // This is the origins gate's rule read from the page side: the picture a reader is shown is
  // served from the site that shows it (Global Constraint 9, D93).
  const src = image.attrs.get('src') ?? ''
  assert.equal(
    src,
    '/images/fugaro-watch.png',
    `the homepage's watch screenshot is ${JSON.stringify(src)}, and the asset PR #622 committed and this site serves is /images/fugaro-watch.png (owner spec 2026-10-18, item 5)`,
  )
  assert.ok(
    !/^[a-z][a-z0-9+.-]*:/i.test(src) && !src.startsWith('//') && !image.attrs.has('srcset'),
    `the watch screenshot is loaded as ${JSON.stringify(src)}${image.attrs.has('srcset') ? ' with a srcset' : ''}, and a page that fetches its own picture does not name another host for it: no scheme, no protocol-relative URL, no candidate list this check cannot follow (Global Constraint 9; \`test/origins.mjs\`)`,
  )

  // **The alt says what the picture shows**, because the picture is a terminal full of the
  // owner's own run titles and spend: a reader who cannot see it is owed an account of the
  // screen, and the transcription ships beside the image (`public/images/fugaro-watch.txt`) as
  // the text that account is taken from. The a11y gate refuses a missing `alt`; what this
  // refuses is an empty one — `alt=""` marks a reader's only account of a page's one
  // photograph as decoration — and a stub too short to be one. The floor is a floor and not the
  // wording: what the reader is told is the owner's, the same way the picture is.
  const alt = (image.attrs.get('alt') ?? '').trim()
  assert.ok(
    alt.length >= 40,
    `the watch screenshot's alt is ${JSON.stringify(alt)}, and the picture is the owner's real \`fugaro watch\` screen: a reader who cannot see it is owed what the screen shows, from the transcription that ships beside the image (owner spec 2026-10-18, item 5; \`public/images/fugaro-watch.txt\`)`,
  )

  // **And the dimensions are the capture's own**, so the browser reserves the box before the
  // file arrives and the command under it does not jump — which is the whole reason the
  // attributes are there. They are held to the capture `docs/website/redesign/README.md`
  // records rather than to themselves: a declared box of the wrong SHAPE — `1459 x 440`, one
  // pixel off the height, or the two numbers swapped — reserves the wrong box and scales the
  // picture into it, and that is this failure. The file the page names is the one the record
  // is about.
  const record = /`website\/public\/images\/fugaro-watch\.png` \((\d+) x (\d+) px, a 2x retina capture, so (\d+) x (\d+) logical\)/.exec(
    repoFile('docs/website/redesign/README.md'),
  )
  assert.ok(record, 'docs/website/redesign/README.md no longer records the capture\'s pixel size and its logical size, so the declared dimensions cannot be held to the file they are about (owner spec 2026-10-18, item 5)')
  const [, captureW, captureH, logicalW, logicalH] = record.map(Number)
  const width = Number(image.attrs.get('width'))
  const height = Number(image.attrs.get('height'))
  assert.ok(
    Number.isInteger(width) && width > 0 && Number.isInteger(height) && height > 0,
    `the watch screenshot declares ${JSON.stringify(image.attrs.get('width'))} x ${JSON.stringify(image.attrs.get('height'))}, and a pair of positive integers is what reserves the picture's box before the file arrives (owner spec 2026-10-18, item 5)`,
  )
  assert.equal(
    width / height,
    captureW / captureH,
    `the watch screenshot declares ${width} x ${height} and the capture is ${captureW} x ${captureH} (${logicalW} x ${logicalH} logical): the declared box has to be the file's own shape, or the browser reserves the wrong space and scales the picture into it (owner spec 2026-10-18, item 5)`,
  )
  assert.deepEqual(
    [width, height],
    [logicalW, logicalH],
    `the watch screenshot declares ${width} x ${height} and the capture is ${logicalW} x ${logicalH} at its logical size: a 2x capture is declared at the size it occupies on the page, which is what stops the box it reserves being twice the height of the one the CSS lays it out in (owner spec 2026-10-18, item 5)`,
  )

  // **The page's pictures are these two and no others**: the wordmark in the header and the
  // owner's screenshot. A homepage that grew a third `<img>` — an illustration, an icon, a
  // picture of a screen the run does not have — is what this last assertion is for (Global
  // Constraint 9; D107's rule for the hero's video). The slider's three step icons are inline
  // SVG rather than files, so they are not pictures in this reading and carry no `src`.
  const pictures = [...walk(page)]
    .filter((el) => ['img', 'picture', 'video', 'source'].includes(el.name))
    .map((el) => el.attrs.get('src') ?? el.attrs.get('srcset') ?? '')
  assert.deepEqual(
    pictures,
    ['/logo.png', '/images/fugaro-watch.png'],
    `the homepage renders the pictures ${JSON.stringify(pictures)}, and it renders two: the wordmark beside the site's name, and the owner's \`fugaro watch\` screenshot (owner spec 2026-10-18, item 5)`,
  )
})

test("the flow is How It Works' own picture of a run, and the homepage carries none of it", () => {
  const ruling = item4()
  assert.equal(ruling.steps.length, 4, `the changes spec's item 4 names ${ruling.steps.length} steps, and the picture draws four (changes spec 2026-10-18, item 4)`)

  // **One page carries the flow.** The owner's approved draft of 2026-10-19 ("Home, draft 2
  // (slider)") is the homepage's three blocks — the hero, the slider and the call to action —
  // and the picture of a run is not one of them. It is `/how-it-works/`'s opening, where the
  // four steps have the room to be drawn and where the detail under them is (item 4 keeps the
  // detail off the homepage). Both pages rendered the same figure until this draft; the
  // assertion below reads the homepage and refuses one, and `website/test/a11y.mjs` reads the
  // same fact off the artifact's own ordered lists.
  const home = parse(builtPage(''))
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/)
  assert.equal(
    findAll(home, (el) => el.name === 'figure' && classes(el).includes('flow')).length,
    0,
    `the homepage renders a <figure class="flow">, and the draft's homepage is the hero, the slider and the call to action: the picture of the run is /how-it-works/'s opening (the owner's approved draft of 2026-10-19; changes spec 2026-10-18, item 4)`,
  )
  const hero = findAll(home, (el) => el.name === 'section' && classes(el).includes('hero'))[0]
  assert.ok(hero, 'the homepage has no hero section, and the hero is the page\'s first block (P5, D102)')

  const failures = flowFailures(builtPage('how-it-works'), ruling, captionSources())
  assert.deepEqual(
    failures,
    [],
    `the /how-it-works/ page's flow is not the one the changes spec's item 4 describes:\n  ${failures.join('\n  ')}`,
  )

  // And the copy that told the run again on the homepage is gone (item 4: "the duplicate
  // text-heavy 'how it works' section is REMOVED"): the detail belongs to the page that opens
  // with the picture. A section that came back would be a second description of the run to
  // keep in step with the first.
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
    failing(elsewhere).some((line) => /held to a section of a document this repository carries/.test(line)),
    'a caption attributed to a document the flow may not quote passes the check: every caption comes from a section this check reads',
  )

  // Two steps of one section, each quoting a line of it: a check that held each caption to
  // its section and no further would let them swap, and the page would then say the run opens
  // a pull request that is done, then gone. The shipped flow cites four sections, so the pair
  // is built here out of README.md's own `## How it works` lines — the job's and step 4's —
  // and the rule is what has to keep holding for the day two steps share a section again.
  const pair = sharedSectionSteps()
  const swapped = pair.map((step, i) => (i === 2 ? { ...step, caption: pair[3].caption } : i === 3 ? { ...step, caption: pair[2].caption } : step))
  assert.deepEqual(failing(pair), [], 'the two steps quoting one section do not pass the check their own case is about')
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
  // The page that renders the flow, because the CSS is inlined per page: the homepage renders
  // no flow since the owner's approved draft of 2026-10-19, so its stylesheet carries none of
  // these rules and a case that read it would be reading the wrong page.
  const css = builtCSS(builtPage('how-it-works'))

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
  // what the repository says, in the order the repository says it. Three of the four quote a
  // section of README.md, and the pull request's caption quotes `docs/git-providers.md`'s
  // checkpoint pushes, which is the document that says when the pull request opens; a
  // section's own order is the cursor below, and it is what a second step quoting the same
  // section obeys.
  const ruling = item4()
  const sources = captionSources()

  // The page that carries the flow, and the only one that does (the owner's approved draft of
  // 2026-10-19 puts the homepage's picture of a run into `/how-it-works/`'s opening alone).
  for (const route of ['how-it-works']) {
    const where = `/${route}/`
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
        `the ${where} page's ${JSON.stringify(step.name)} step attributes its caption to ${JSON.stringify(step.source)}, and a caption is held to a section of a document this repository carries (changes spec 2026-10-18, item 4)`,
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

  // The number sits beside the heading rather than in it. The old markup carried it inside the
  // heading and `runDetailOf` stripped it back off, so a reader saw "3 Loop" and this file saw
  // "Loop"; the fixture below is that markup, and it is refused rather than stripped.
  const numbered = steps.map((step, i) => (i === 2 ? { ...step, heading: `${i + 1}${step.name}` } : step))
  assert.ok(
    failing(numbered).some((line) => /is headed .*and README\.md names step 3/.test(line)),
    'a step whose heading carries its own number passes the check: the number is decoration beside the heading, and a heading that swallowed it is not the name the section gives the step',
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

/** The homepage's own controls, and what each one is, as a failure names it. */
const CONTROL_TARGETS = [
  ['hero-cta', "the hero's filled call to action"],
  ['hero-cta-quiet', "the hero's quiet call to action"],
  ['home-install-button', 'the button under the slider'],
]

/**
 * Every way a homepage control misses the target size the owner's brief of 2026-10-19
 * measures it against, as the lines a reader of a failure gets. **Every control the page asks
 * a reader to tap is at least 44 px tall**, and the page's own controls are the hero's two
 * links in the action row and the one button under the slider (`HomeInstall.astro`); the
 * slider's three tabs are `test/slider.mjs`'s, which reads them at the widths the page is
 * read at, so this reading is about the controls outside the slider.
 *
 * A minimum rather than a measurement, and that is the point: the hero's filled link is its
 * own line height plus its padding, which comes to a fraction of a pixel short of the target
 * with nothing in the markup to say so — a defect no reading of the markup can see and no
 * rendered measurement in this repository can take, which is why the rule declares the minimum
 * and this reading looks for the declaration. A rule inside a media query does not count: the
 * minimum has to hold at every width, so the rule it is declared in is one with no condition
 * over it.
 */
export function controlTargetFailures(css, controls = CONTROL_TARGETS) {
  const tokens = new Map([...css.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;}]+)/g)].map((m) => [m[1], m[2].trim()]))
  const px = (value) => {
    const trimmed = (value ?? '').trim()
    const named = /^var\((--[a-z0-9-]+)\)$/.exec(trimmed)
    const resolved = named ? (tokens.get(named[1]) ?? '') : trimmed
    const m = /^([\d.]+)px$/.exec(resolved.trim())
    return m ? Number(m[1]) : null
  }
  // Whether a position in the stylesheet is inside a block: at the top level every rule is in
  // force at every width, and a rule inside a media query is not.
  const depthAt = (index) => {
    let depth = 0
    for (let i = 0; i < index; i++) {
      if (css[i] === '{') depth++
      else if (css[i] === '}') depth--
    }
    return depth
  }

  const out = []
  for (const [klass, what] of controls) {
    const heights = []
    for (const match of css.matchAll(new RegExp(`\\.${klass}(?![\\w-])[^{}]*\\{([^}]*)\\}`, 'g'))) {
      if (depthAt(match.index) !== 0) continue
      const declared = /min-height\s*:\s*([^;}]+)/.exec(match[1])
      const height = px(declared?.[1])
      if (height !== null) heights.push(height)
    }
    if (!heights.some((height) => height >= 44)) {
      out.push(
        `${what} is ${heights.length ? `a ${Math.max(...heights)} px target` : 'no declared target'}, and the owner's brief of 2026-10-19 measures every control the page asks a reader to tap against 44 px`,
      )
    }
  }
  return out
}

test('every control the homepage asks a reader to tap is at least 44 px tall', () => {
  assert.deepEqual(
    controlTargetFailures(builtCSS(builtPage(''))),
    [],
    'a control on the homepage is under the 44 px target the owner’s brief of 2026-10-19 measures every control against',
  )
})

test('a control under the target is refused, wherever its minimum is missing or out of force', (t) => {
  // Each plant is one of the ways the minimum stops holding: taken off the rule that declares
  // it, moved into a media query where it is in force at one width rather than at every one,
  // and declared under the target. All three are defects a reading of the markup cannot see,
  // which is why the reading is of the declaration.
  const css = builtCSS(builtPage(''))
  const ruleOf = (klass) => new RegExp(`\\.${klass}(?![\\w-])[^{}]*\\{[^}]*\\}`)
  const withoutMinimum = (klass) => css.replace(ruleOf(klass), (rule) => rule.replace(/min-height\s*:\s*[^;}]+;?/, ''))
  const cases = [
    ['the minimum taken off the hero’s filled link', withoutMinimum('hero-cta'), /the hero's filled call to action is no declared target/],
    ['the minimum taken off the hero’s quiet link', withoutMinimum('hero-cta-quiet'), /the hero's quiet call to action is no declared target/],
    [
      'the button’s minimum moved into a media query, where it holds at one width and not at every one',
      `${withoutMinimum('home-install-button')}@media (min-width:48rem){.home-install-button{min-height:56px}}`,
      /the button under the slider is no declared target/,
    ],
    [
      'a minimum declared under the target',
      css.replace(ruleOf('hero-cta'), (rule) => rule.replace('min-height:44px', 'min-height:20px')),
      /the hero's filled call to action is a 20 px target/,
    ],
  ]
  for (const [what, planted, expected] of cases) {
    assert.notEqual(planted, css, `${what} left the stylesheet as it is, so the plant is not a plant`)
    const failures = controlTargetFailures(planted)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused by the sentence that names it:\n  ${failures.join('\n  ') || '(nothing refused)'}`,
    )
  }
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
