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
//      gone by `runExampleFailures` over every command block and every word of the built
//      homepage — the two shapes it came back in), and the slides' slot, which is asserted to
//      be marked, to be where the panel's heading leads and to hold
//      **the rows of `website/data/home-screens.yaml`** (the brief of 2026-10-21: the single
//      capture becomes a slider; D246, D251, D252) — the console's own three screenshots in the
//      brief's order and the owner's real `fugaro watch` capture last, each with its own file,
//      `alt`, declared box and caption, with the page's whole `<img>` set being those rows plus
//      the wordmark. The capture's own facts are unmoved by that: a non-empty `alt`, the
//      dimensions `docs/website/redesign/README.md` records, and a path this site serves
//      (owner spec of 2026-10-18, item 5; PR #622).
//   4. **The run is How It Works' picture of it, and the homepage carries the slider instead**
//      (changes spec of 2026-10-18, item 4; the owner's ruling of 2026-10-20). The four-step
//      flow and its component, `ArchitectureDiagram.astro`, are retired, and what this file
//      held the flow to is held to what took its place: the drawing's four boxes are the
//      spec's item 4's four names in the ruling's order, every word on the figure — the labels
//      inside each drawing and the two lists of the run in words under it — is a line of the
//      section it cites, in that section's order, and the homepage's block is the slider, whose
//      three steps and three panels are read on the page. The run's stages and its caps are on
//      How It Works, where `test/how-it-works.mjs` holds them to the documents they cite.
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
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { cssDeclarations, cssRules, findAll, filesUnder, parse, textOf, walk } from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'
import { controlTargetFailures } from './lib/controls.mjs'
import { readScreenRows } from './lib/screens.mjs'

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
 * out (the slider's step numbers and its icons are drawing, not words), entities decoded,
 * runs of whitespace one space. This is what the page says, which is what the checks below
 * hold to the repository.
 */
function readableText(node) {
  if (node.attrs && node.attrs.get('aria-hidden') === 'true') return ''
  if (node.text !== undefined) return node.text
  if (node.raw !== undefined) return ''
  return (node.children ?? []).map(readableText).join('')
}

/**
 * The text of the first element whose class list carries `className`, or null when there is
 * none. The match is on the whole token rather than on the attribute's exact text, because a
 * page is free to give an element more than one class: the hero's headline is
 * `class="hero-tagline display-xl"` — its own width and `base.css`'s display step — and a
 * reader that wanted `class="hero-tagline"` exactly would find nothing there and pass the page
 * on an empty reading.
 */
function classText(html, className) {
  const m = new RegExp(`<([a-z0-9]+)[^>]*\\bclass="[^"]*\\b${className}\\b[^"]*"[^>]*>([\\s\\S]*?)</\\1>`).exec(html)
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
  // The typographic quotes are folded to their plain ones, the way `website/test/how-it-works.mjs`
  // folds them: the site's markdown pipeline sets a repository's `'` as `’` on the page, so a
  // label the page quotes from a document is the document's own line written in the type the
  // page sets it in, and a comparison that kept the two apart would refuse every quotation with
  // an apostrophe in it.
  return markdown.replace(/[`*]/g, '').replace(/[‘’]/g, "'").replace(/\s+/g, '')
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
 * reader the retired four-step flow's build-time caption guard used, kept here for the two
 * places this file resolves a section: the design record's account of the watch screenshot, and
 * `quotedSection` below, which holds the picture of the run's labels to theirs. A heading's code
 * span is a marker rather than part of its name, which is why the search reads the headings with
 * their backticks off: a section citing `## Checkpoint pushes (\`git.pr.checkpoints\`)` carries
 * the key in its name.
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
 * The routes the site's pages live at, from the plans: the page files their tasks' `**Files**`
 * lines name. Pages are lowercase and components are capitalised in this repository, so a
 * lowercase `<name>.astro` in a Files line is a page — and this is the only list of the
 * site's pages that exists while the page tasks are still being written.
 *
 * **Two plans write this site's pages, and each writes its line its own way.** The website
 * plan (`docs/website/implementation-plan.md`) is the site's first round and writes
 * `**Files:**`; this line of work's plan
 * (`docs/website/zoom-diagrams/implementation-plan.md`) adds routes to the same site — its
 * Part 2 writes `/why-gcp/` and its Part 3 writes `/mobile/` — and writes `**Files.**`. Both
 * spellings are read, because a route a plan writes is a route a page of this site lives at
 * whichever document writes it.
 *
 * **The name has to be the whole last segment of a backticked path**, and that is a fix to
 * this reader rather than a tightening of it: the loose form it was written with — any run of
 * lowercase letters before `.astro`, anywhere in the line — read
 * `ArchitectureDiagram.astro` as `/iagram/`, `Terminal.astro` as `/erminal/` and
 * `Downloads.astro` as `/ownloads/`, so the list it returned held routes no plan writes, which
 * is the one thing the case below exists to refuse. The website plan writes its own list both
 * ways (a full `website/src/pages/index.astro` and a bare `how-it-works.astro` beside it), so
 * the path in front of the name is optional and the backticks are not.
 */
function plannedRoutes() {
  const routes = new Set()
  const plans = ['docs/website/implementation-plan.md', 'docs/website/zoom-diagrams/implementation-plan.md']
  for (const plan of plans) {
    for (const line of repoFile(plan).split('\n')) {
      if (!/^\*\*Files[:.]\*\*/.test(line)) continue
      for (const m of line.matchAll(/`(?:[\w./-]+\/)?([a-z0-9-]+)\.astro`/g)) {
        routes.add(m[1] === 'index' ? '/' : `/${m[1]}/`)
      }
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
 * One section of a document of this repository, as a label of the picture is held to it: the
 * heading a name cites (`## <heading>`, or a `### <heading>` like `docs/gcp-setup.md`'s
 * `Run, watch, diagnose`, read at the level it is written and up to the next heading of that
 * level or higher), or the whole document when the name cites no section. A section rather than
 * the whole file, because several of the picture's labels are lines of README.md, and held to
 * the file alone they could be shuffled with every one of them still a quotation; inside one
 * section the labels must also come in the order the section writes them, which is what keeps
 * two labels quoting one section from swapping. `text` is null when the name cites no such
 * document or section — this returns rather than asserting, because a label attributed to a
 * section that is not there is one of the defects the check reports.
 *
 * This is the retired four-step flow's own reader, kept with its subject: the flow held each of
 * its captions to the section its `data-source` named, and the picture of the run that took the
 * flow's place is held to its labels the same way (the owner's ruling of 2026-10-20).
 */
function quotedSection(source) {
  const at = /^([^#]+)#(.+)$/.exec(source)
  const file = path.join(REPO, at ? at[1] : source)
  if (!existsSync(file)) return { text: null }
  const whole = readFileSync(file, 'utf8')
  if (!at) return { text: whole }
  const found = [...whole.matchAll(/^(#+) (.+)$/gm)].find((match) => match[2].replace(/`/g, '') === at[2])
  if (!found) return { text: null }
  const level = found[1].length
  const rest = whole.slice(found.index)
  const next = [...rest.matchAll(/^(#+) /gm)].find((match) => match.index > 0 && match[1].length <= level)
  return { text: next ? rest.slice(0, next.index) : rest }
}

/**
 * The picture of the run, as one built page carries it: the figure, the name of each box of each
 * drawing in the order the drawing prints them, the labels on each drawing, the list of words
 * under the figure, and — the retired flow's own reading, still read here because what a page
 * must *not* carry is as much of this check's subject as what it must — how many `ol.flow-steps`
 * the page renders.
 */
function pictureOf(html) {
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
  const doc = parse(html)
  const figures = findAll(doc, (el) => el.name === 'figure' && classes(el).includes('run-diagram'))
  const words = findAll(doc, (el) => el.name === 'section' && classes(el).includes('run-words'))
  const out = {
    figures: figures.length,
    flows: findAll(doc, (el) => el.name === 'ol' && classes(el).includes('flow-steps')).length,
    drawings: [],
    words: [],
    // The regions a citation is read in: each drawing, and each list under the figure. A cursor
    // per region rather than one per page, because the lists say the same run again — the second
    // list quotes lines of the sections the first one quoted, and the phone drawing repeats the
    // desktop drawing label for label — so one cursor across the page would refuse the second
    // telling of a fact for being the second.
    regions: [],
    details: words.length,
  }
  for (const figure of figures) {
    for (const svg of walk(figure)) {
      if (svg.name !== 'svg') continue
      out.drawings.push({
        wide: classes(svg).includes('run-wide'),
        // A box's name is its `run-name` text, joined: the drawing breaks a long name across
        // two `<text>` lines ("Ephemeral" then "cloud job"), and the name a reader reads is
        // both of them.
        names: findAll(svg, (el) => el.name === 'g' && classes(el).includes('run-box')).map((box) =>
          [...walk(box)]
            .filter((el) => el.name === 'text' && classes(el).includes('run-name'))
            .map((el) => textOf(el).trim())
            .join(' '),
        ),
        labels: [...walk(svg)]
          .filter((el) => el.name === 'g' && classes(el).includes('run-label'))
          .map((el) => ({ source: el.attrs.get('data-source') ?? '', text: textOf(el).trim() })),
      })
      const drawing = out.drawings[out.drawings.length - 1]
      out.regions.push({ where: `the ${drawing.wide ? 'wide' : 'tall'} drawing`, labels: drawing.labels })
    }
  }
  for (const block of words) {
    for (const list of findAll(block, (el) => (el.name === 'ol' || el.name === 'ul') && classes(el).some((name) => name.startsWith('run-')))) {
      const klass = classes(list)
      const labels = [...walk(list)]
        .filter((el) => classes(el).some((name) => name === 'run-fact-text' || name === 'run-also-item'))
        .map((el) => ({ source: el.attrs.get('data-source') ?? '', text: textOf(el).replace(/\s+/g, ' ').trim() }))
      out.words.push(...labels)
      out.regions.push({
        where: klass.includes('run-facts') ? 'the facts under the picture' : klass.includes('run-also') ? 'the second list under the picture' : `the ${klass.join(' ')} list`,
        labels,
      })
    }
  }
  return out
}

/**
 * Every way one built page's picture of a run falls short of what replaced the retired
 * four-step flow (the owner's ruling of 2026-10-20), as the lines a reader sees. Pure, over one
 * page's HTML, so the built pages are checked with it and the refusals below are proved with it.
 *
 * The two claims the retired gate made are made here of the picture that took its place. Its
 * first was that the page's steps are the changes spec's item 4's four names, in the ruling's
 * order — now the drawing's four boxes, which are the same steps drawn rather than listed. Its
 * second was that every caption is a line of the section its step cites, in that section's order
 * — now every word on the figure: the labels drawn inside each drawing, and the two lists of the
 * same run in words under it. A caption held to its file alone could be shuffled among the steps
 * with every one of them still a quotation, so the cursor is kept, and it is per region — one
 * drawing, and each list under the figure: the phone drawing carries the same labels as the
 * desktop one in the same order, and the second list quotes lines of the sections the first one
 * quoted, so a single cursor across the page would refuse the second telling of a fact for being
 * the second rather than for being wrong.
 *
 * And the flow itself: a page that renders `ol.flow-steps` again is rendering the component the
 * ruling deleted, which is why a page carrying one is refused here rather than passed for
 * having, along with it, a picture that is right.
 */
export function pictureFailures(html, ruling) {
  const out = []
  const picture = pictureOf(html)
  if (picture.flows > 0) {
    out.push(
      `the page renders ${picture.flows} \`ol.flow-steps\`, and the four-step flow is retired: the owner's ruling of 2026-10-20 deletes \`ArchitectureDiagram.astro\` and its flow, and the picture of a run is How It Works' drawing with the run in words under it`,
    )
  }
  if (picture.figures !== 1) {
    out.push(
      `the page renders ${picture.figures} pictures of a run, and it renders one: the run is one figure drawn at two scales, with the same run in words under it (the owner's draft of 2026-10-19; changes spec 2026-10-18, item 4)`,
    )
    return out
  }
  if (picture.details !== 1) {
    out.push(
      `the page renders ${picture.details} lists of the run in words, and it renders one: the picture is the drawing and the list under it, which is what a reader who skips the figure reads (the owner's draft of 2026-10-19; changes spec 2026-10-18, item 4)`,
    )
  }

  for (const drawing of picture.drawings) {
    const where = `the ${drawing.wide ? 'wide' : 'tall'} drawing`
    if (drawing.names.join(' | ') !== ruling.steps.join(' | ')) {
      out.push(
        `${where} draws the boxes ${JSON.stringify(drawing.names)}, and the changes spec's item 4 names ${JSON.stringify(ruling.steps)}, in that order: the four boxes are the run's four steps, and a step added, dropped, renamed or reordered is a claim about how a run works (changes spec 2026-10-18, item 4; the owner's ruling of 2026-10-20)`,
      )
    }
    if (drawing.labels.length === 0) {
      out.push(
        `${where} carries no labelled text, and the picture is its labels: a drawing whose words moved out of it says nothing (the owner's draft of 2026-10-19)`,
      )
    }
  }

  for (const region of picture.regions) {
    const reached = new Map()
    for (const label of region.labels) {
      const held = quotedSection(label.source)
      if (held.text === null) {
        out.push(
          `${region.where} attributes the label ${JSON.stringify(label.text)} to ${JSON.stringify(label.source)}, and a label is held to a section of a document this repository carries (Global Constraint 6; changes spec 2026-10-18, item 4)`,
        )
        continue
      }
      const line = normalize(label.text)
      const from = reached.get(label.source) ?? 0
      const at = normalize(held.text).indexOf(line, from)
      if (at === -1) {
        out.push(
          `${region.where}'s label ${JSON.stringify(label.text)} does not appear in ${label.source} at or after the label before it: every word of the picture is the repository's own words in the order their document writes them, and a paraphrase — or a line that moved above another — is the page inventing what the product says (Global Constraint 6, RF11; changes spec 2026-10-18, item 4)`,
        )
      } else {
        reached.set(label.source, at + line.length)
      }
    }
  }
  return out
}

const REDUCED_MOTION = /prefers-reduced-motion\s*:\s*reduce/i
const ALLOWS_MOTION = /prefers-reduced-motion\s*:\s*no-preference/i
/** A declaration that stops motion rather than starting it. */
const STOPS = /^(none|auto|0m?s|1ms)\b/
/**
 * What a reduce rule writes to take a kind of motion away, by the property it writes it in: a
 * duration, delay or scroll behaviour set to nothing, and an animation's iteration count set to
 * the one play it is given (the site's own guard writes `animation-iteration-count: 1`).
 */
const NEUTRALISERS = {
  'animation-duration': STOPS,
  'animation-delay': STOPS,
  'animation-iteration-count': /^(0|1)\b/,
  'transition-duration': STOPS,
  'transition-delay': STOPS,
  'scroll-behavior': /^auto\b/,
}
/** The scroll-driven reveal the retired flow brought its steps in with. */
const SCROLL_TIMELINE = ['animation-timeline', 'scroll-timeline', 'view-timeline']

/**
 * Every way one built page keeps motion for a reader who asked for less of it. The retired
 * flow's own gate (W1.4 step 3, D105) asked the page to bring its steps in on a scroll-driven
 * animation behind a `@supports` guard and a `prefers-reduced-motion: no-preference` query;
 * that animation is retired with the flow (the owner's ruling of 2026-10-20), and neither of the
 * two things that took its place moves at all — the slider's three panels are the fragment's and
 * the drawing is a still picture. So what is left to hold is the page's remaining motion, every
 * declaration of it: smooth scrolling, and the transitions on the copy control and the links.
 * Each one has to be covered by the page's own answer to a reader who asks for less — a
 * `prefers-reduced-motion: reduce` rule that sets it to nothing, either on the declaration's own
 * selector or on the universal `*, :before, :after` guard — and the scroll-driven reveal is
 * refused outright, because a page that reveals itself as a reader scrolls it is the flow's
 * mechanism back under another name.
 */
export function pageMotionFailures(css) {
  const out = []
  const rules = cssRules(css)
  const reduce = rules.filter((rule) => rule.conditions.some((condition) => REDUCED_MOTION.test(condition)) && rule.selector)
  const universal = reduce.filter((rule) => rule.selector.split(',').some((part) => part.trim() === '*'))
  const stops = (rs, property) => rs.some((rule) => cssDeclarations(rule.body).some(([p, v]) => p === property && NEUTRALISERS[property].test(v)))
  const covered = {
    animation: stops(universal, 'animation-duration') && stops(universal, 'animation-iteration-count'),
    transition: stops(universal, 'transition-duration'),
    'scroll-behavior': stops(universal, 'scroll-behavior'),
  }
  const neutralised = (selector, kind) =>
    reduce.some(
      (rule) =>
        rule.selector === selector &&
        cssDeclarations(rule.body).some(([p, v]) => (p === kind || p === `${kind}-name` || p === `${kind}-duration`) && (NEUTRALISERS[p] ?? STOPS).test(v)),
    )

  for (const rule of rules) {
    if (!rule.selector || rule.selector.startsWith('@')) continue
    const declared = cssDeclarations(rule.body)
    for (const [property, value] of declared) {
      if (SCROLL_TIMELINE.includes(property) && !/^none\b/.test(value.trim())) {
        out.push(
          `${rule.selector}${rule.conditions.length > 0 ? ` (inside ${rule.conditions.join(' and ')})` : ''} declares ${property}: ${value}, and the page is not revealed by scrolling: the flow's scroll-driven animation is retired with the flow (the owner's ruling of 2026-10-20), and the slider's panels are the fragment's while the drawing is a still picture (W1.4 step 3, D105)`,
        )
      }
    }
    // A rule inside one of the two motion queries is the page's own handling of motion: a
    // `reduce` rule *is* the answer, and a `no-preference` rule only ever runs for a reader who
    // allows motion.
    if (rule.conditions.some((condition) => REDUCED_MOTION.test(condition) || ALLOWS_MOTION.test(condition))) continue
    for (const [property, value] of declared) {
      let kind = null
      if ((property === 'animation' || property === 'animation-name') && !/^none\b/.test(value)) kind = 'animation'
      if ((property === 'transition' || property === 'transition-duration') && !STOPS.test(value)) kind = 'transition'
      if (property === 'scroll-behavior' && /smooth/.test(value)) kind = 'scroll-behavior'
      if (kind === null || covered[kind] || neutralised(rule.selector, kind)) continue
      out.push(
        `${rule.selector} declares ${property}: ${value}, and no prefers-reduced-motion: reduce rule neutralises it: motion is for a reader who allows it, and the reader who asks for less is answered by a rule that sets it to nothing (W1.4 step 3, D105)`,
      )
    }
  }
  return out
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
 * The benefits section's heading, exactly: the owner's ruling of 2026-10-20 names it "Why
 * Fugaro?" — capital W and F and the question mark — because the four points under it are the
 * reasons the README argues under that heading, and the section is the answer to the question
 * rather than a list of "benefits". Held as one string and read twice (the heading, and the
 * slide label `slider.mjs` pairs with it), so the two halves of the rename cannot drift apart.
 */
const BENEFITS_TITLE = 'Why Fugaro?'

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

/** The commands a straight-edged outline is drawn with, and the only ones read here. */
const OUTLINE_COMMANDS = 'MmLlHhVvZz'

/**
 * The corners of a drawing that is one closed outline of straight edges — a single `<path>`
 * drawn through points and closed with `Z`, with nothing else in the drawing — or `null` when
 * it is anything else: several shapes, several subpaths, a curve, or an outline left open.
 *
 * Only such a drawing is read as a mark whose corners are the whole of it, which is what the
 * symmetry rule below is about. The rest of the set is not read here and needs no exemption:
 * the shield and the team are drawn with curves, and the arrows are four separate strokes
 * that never close, so a drawing this function refuses to read is a drawing the rule has
 * nothing to say about rather than one it lets pass.
 */
function outlineOf(drawing) {
  const quoted = /^path d=("(?:[^"\\]|\\.)*")$/.exec(drawing)
  if (quoted === null) return null
  const d = JSON.parse(quoted[1])
  const points = []
  let at = null
  let moved = false
  let closed = false
  for (const [, letter, args] of d.matchAll(/([A-Za-z])([^A-Za-z]*)/g)) {
    if (!OUTLINE_COMMANDS.includes(letter)) return null
    const rest = args.replace(/[\s,]/g, '')
    const numbers = args.match(/-?\d*\.?\d+(?:e-?\d+)?/gi) ?? []
    if (rest.replace(/-?\d*\.?\d+(?:e-?\d+)?/gi, '') !== '') return null
    const values = numbers.map(Number)
    if (values.some((value) => !Number.isFinite(value))) return null
    const relative = letter === letter.toLowerCase()
    const move = (point) => {
      at = point
      points.push(point)
      return point
    }
    const step = (x, y) => move(relative ? [at[0] + x, at[1] + y] : [x, y])
    if (letter === 'Z' || letter === 'z') {
      if (values.length !== 0 || at === null) return null
      closed = true
      continue
    }
    if (letter === 'M' || letter === 'm') {
      // A second `M` opens a second subpath, which is a drawing of more than one outline.
      if (moved || values.length < 2 || values.length % 2 !== 0) return null
      moved = true
      // A path's first `m` is absolute whatever its case — there is no current corner for it
      // to be relative to.
      if (at === null) move([values[0], values[1]])
      else step(values[0], values[1])
      for (let i = 2; i < values.length; i += 2) step(values[i], values[i + 1])
      continue
    }
    if (!moved || at === null) return null
    if (letter === 'L' || letter === 'l') {
      if (values.length < 2 || values.length % 2 !== 0) return null
      for (let i = 0; i < values.length; i += 2) step(values[i], values[i + 1])
    } else if (letter === 'H' || letter === 'h') {
      if (values.length === 0) return null
      // A horizontal step keeps the corner's own y — the one `at` is carrying — and a
      // vertical one keeps its x, which is the whole of what those two commands mean.
      for (const value of values) move([relative ? at[0] + value : value, at[1]])
    } else {
      if (values.length === 0) return null
      for (const value of values) move([at[0], relative ? at[1] + value : value])
    }
  }
  if (!closed || !moved || points.length < 3) return null
  // `Z` closes back onto the first corner, so the outline is the points as they stand.
  return points
}

/**
 * What a mark drawn as one closed outline of straight edges has to be, as the lines a reader
 * of this file sees. The owner's feedback of 2026-10-21: "icon for 'The newest tools and
 * models, ready to use' is asymmetrical (fix the icon's drawing so it is visually
 * symmetric)". The glyph under that point is a four-point star, and a star is a symmetric
 * mark — its corners mirrored onto themselves about both of the lines through its middle, and
 * that middle the middle of the 24×24 box the icon set draws in, which is where every other
 * glyph of the set sits. The drawing that shipped had the north-west notch left as a straight
 * edge from the west point to the north one, so one corner of seven had nothing opposite it,
 * and the whole mark sat a unit above the middle of the box; a reader sees both as the mark
 * leaning, which is what the feedback is about.
 *
 * The rule is written over the drawing rather than over the star by name, so it is a fact
 * about the shape a glyph is and not a description of today's markup: it reads every glyph of
 * the set that is drawn as one closed outline, and today that is the star alone. An empty list
 * is a glyph that is symmetric about both lines and centred in the box, or one this rule has
 * nothing to say about.
 */
export function glyphSymmetryFailures(drawing, where) {
  const points = outlineOf(drawing)
  if (points === null) return []
  const round = (value) => Math.round(value * 1e4) / 1e4
  const xs = points.map(([x]) => x)
  const ys = points.map(([, y]) => y)
  const middle = [(Math.min(...xs) + Math.max(...xs)) / 2, (Math.min(...ys) + Math.max(...ys)) / 2]
  const corners = new Set(points.map(([x, y]) => `${round(x)},${round(y)}`))
  const out = []
  for (const [axis, mirrored] of [
    ['vertical', points.map(([x, y]) => [2 * middle[0] - x, y])],
    ['horizontal', points.map(([x, y]) => [x, 2 * middle[1] - y])],
  ]) {
    const absent = mirrored.filter(([x, y]) => !corners.has(`${round(x)},${round(y)}`))
    if (absent.length > 0) {
      out.push(
        `${where}: its glyph is drawn as one closed outline of straight edges and is not symmetric about the ${axis} line through its middle — ${absent.length} of its ${points.length} corners have no corner opposite them (${absent.map(([x, y]) => `(${round(x)}, ${round(y)})`).join(', ')}), so the mark reads as a shape drawn lopsided rather than as the mark the icon set draws: the owner's feedback of 2026-10-21 asks for the drawing to be visually symmetric, with its meaning, its stroke and the set's size kept as they are`,
      )
    }
  }
  if (round(middle[0]) !== 12 || round(middle[1]) !== 12) {
    out.push(
      `${where}: its glyph's middle is (${round(middle[0])}, ${round(middle[1])}) and the icon set draws every glyph about the middle of the 24×24 box, (12, 12) — a mark symmetric in itself and sitting off the middle of its box is the same lean the owner's feedback of 2026-10-21 is about, seen from the other side (the box is the component's, \`viewBox="0 0 24 24"\`)`,
    )
  }
  return out
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
 *   - **the section is headed "Why Fugaro?"**, the owner's own name for it since the ruling of
 *     2026-10-20, and the same string the slider's second slide carries;
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
 *   - **no benefit carries an icon** (the owner's ruling of 2026-10-23, the redesign's Step 3,
 *     lever 5, which retired the icon-topped points the feedback of 2026-10-19 had asked for).
 *     The glyph readings below (`glyphSymmetryFailures`) stay as the reading of a drawing, for
 *     any glyph the site draws again.
 *
 * Written as a function over a benefits section and the README's reasons so a fixture can
 * prove it refuses.
 */
export function benefitFailures(benefits, reasons) {
  const out = []
  if (benefits.sections !== 1) {
    return [`the page renders ${benefits.sections} benefits sections, and it renders one: the section is the README's four reasons and not a figure (the owner's feedback of 2026-10-19)`]
  }
  if (benefits.title !== BENEFITS_TITLE) {
    out.push(
      `the benefits section is headed ${JSON.stringify(benefits.title)}, and the section a reader looks for is headed ${JSON.stringify(BENEFITS_TITLE)}: the heading is the site's own label for the block, the way "Install" is the install block's, and it is the question the README's four reasons answer (the owner's ruling of 2026-10-20)`,
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
  // **No icons since the owner's ruling of 2026-10-23** (the redesign's Step 3, lever 5: the row
  // of identical icon cards becomes one composed section). The feedback of 2026-10-19 asked each
  // point for an icon; the audit of 2026-10-23 flagged the result as icon-topped cards, and the
  // points are now four headed paragraphs. A point drawn with an icon is the retired shape.
  for (const [index, item] of benefits.items.entries()) {
    const icons = item.icons ?? []
    if (icons.length > 0) {
      out.push(
        `benefit ${index + 1} of the benefits carries ${icons.length} icon(s), and the points carry none since the owner's ruling of 2026-10-23: the row of icon-topped points is the shape that ruling retired`,
      )
    }
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


/** A page carrying nothing but a benefits section, for the refusals above. */
function benefitsPage(items, { title = BENEFITS_TITLE, source = BENEFITS_SOURCE, sections = 1 } = {}) {
  const body = items
    .map(
      (item, index) =>
        `<li class="benefit">${item.icon ?? ''}<h3 class="benefit-name">${item.name}</h3><p class="benefit-line">${item.line}</p></li>`,
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

  // The heading is pinned to the character: the plant is the owner's own words without the
  // question mark, so a rename that read the ruling as a phrase rather than as the exact
  // string is refused.
  assert.match(
    benefitFailures(benefitsOf(benefitsPage(good, { title: 'Why Fugaro' })), reasons).join('\n'),
    /is headed "Why Fugaro"/,
    'a section headed with the old label, or with the new one minus its question mark, is not refused',
  )

  assert.match(
    benefitFailures(benefitsOf(benefitsPage(good, { title: 'Benefits' })), reasons).join('\n'),
    /is headed "Benefits"/,
    'the label this section carried before the owner\'s ruling of 2026-10-20 is not refused',
  )

  assert.match(
    benefitFailures(benefitsOf(benefitsPage(good, { source: 'README.md' })), reasons).join('\n'),
    /cites "README\.md", and its names and lines are README\.md's "## Why Fugaro"/,
    'a section that cites the whole README instead of the section is not refused',
  )
})

test('the benefits refusal fires on a point drawn with an icon', () => {
  // The owner's ruling of 2026-10-23 (the redesign's Step 3, lever 5) retired the icon on each
  // point. The fixture passes with no icon; a point with one, or with two, is refused.
  const reasons = readmeReasons()
  const good = reasons.map((reason) => ({ name: reason.name, line: firstSentence(reason.paragraph) }))
  assert.deepEqual(benefitFailures(benefitsOf(benefitsPage(good)), reasons), [], 'the fixture section is not the README\'s own, so the refusals below prove nothing')
  const plant = (index, icon) => good.map((item, i) => (i === index ? { ...item, icon } : item))
  const refusal = (items) => benefitFailures(benefitsOf(benefitsPage(items)), reasons).join('\n')
  assert.match(refusal(plant(0, iconMarkup('<path d="M1 1h2v2"></path>'))), /benefit 1 of the benefits carries 1 icon\(s\)/, 'a point drawn with an icon is not refused')
  assert.match(
    refusal(plant(2, iconMarkup('<path d="M1 1h2v2"></path>') + iconMarkup('<path d="M9 9h2v2"></path>'))),
    /benefit 3 of the benefits carries 2 icon\(s\)/,
    'a point drawn with two icons is not refused',
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
  assert.equal(content(/<meta name="twitter:card" content="([^"]*)"/, 'Twitter card'), 'summary_large_image')
  // The card carries a picture this site ships, by an absolute URL.
  assert.match(content(/<meta property="og:image" content="([^"]*)"/, '`og:image`'), /^https:\/\/[^/]+\/og-image\.png$/)
  assert.equal(content(/<meta property="og:image:width" content="([^"]*)"/, '`og:image:width`'), '1200')
  assert.equal(content(/<meta property="og:image:height" content="([^"]*)"/, '`og:image:height`'), '630')
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
  // the checkout's flag is false. The checkout now ships the flag TRUE (the console's own
  // release stream), so this case steps aside and `console-option.mjs` holds both
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
    [['Get started', '/getting-started/']],
    'the hero\'s call to action is not its one action (the owner\'s rulings of 2026-10-23, lever 2): the console is surfaced in the page\'s content, and `console-option.mjs` holds that row',
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

/** The old hero example, the transcript item 3 removes from the hero ("Remove `fugaro run`
 * from the hero example"): the launch line a reader was once meant to type, with the task the
 * spec's own example asks for. It is read as one string so that the gate and this comment
 * cannot drift from each other. */
const RETIRED_EXAMPLE = 'add a --verbose flag to the export command'

/**
 * The homepage's commands and its words against item 3's one rule: **the command the reader is
 * offered is `fugaro watch`, and the manual `fugaro run` line is not on the page**.
 *
 * The commands are the page's `<pre>` blocks and the words are the page's text, both read over
 * the whole homepage rather than over a section: the retired hero example came back as a
 * command block a reader could copy and as a transcript in prose, and a gate that read one
 * shape would let the other through. It is a fact about the page, which is what makes it
 * readable here — the case in `copy.mjs` that refuses the word `run` knows about commands the
 * page *offers* and not about a line the site publishes as prose.
 *
 * The two branches are the two ways the example returns, and each has a plant in the case
 * below: a `<pre>` whose command is a `fugaro run` invocation, and the transcript's own words
 * anywhere in the page's text. Neither can be seen by the page's byte budget — which only
 * tells a reader that *something* moved — so this is where the example is held gone.
 */
function runExampleFailures(html) {
  const out = []
  const commands = [...walk(parse(html))]
    .filter((el) => el.name === 'pre')
    .map((el) => text(readableText(el)))
  for (const command of commands) {
    if (/^fugaro run\b/.test(command)) {
      out.push(
        `the homepage offers ${JSON.stringify(command)} as a command the reader can copy, and a \`fugaro run\` line is the manual path the documentation keeps rather than what the reader types (changes spec 2026-10-18, item 3)`,
      )
    }
  }
  if (text(html).includes(RETIRED_EXAMPLE)) {
    out.push(
      `the homepage still carries the old hero example, the \`fugaro run "${RETIRED_EXAMPLE}"\` transcript (changes spec 2026-10-18, item 3: "Remove \`fugaro run\` from the hero example")`,
    )
  }
  return out
}

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

test('the homepage offers the reader fugaro watch and not the retired fugaro run example', () => {
  const page = builtPage('')
  const commands = [...walk(parse(page))].filter((el) => el.name === 'pre').map((el) => text(readableText(el)))
  assert.ok(
    commands.includes('fugaro watch'),
    `the homepage's command blocks are ${JSON.stringify(commands)}, and read off the whole page rather than off a section: what a reader is offered to copy is a fact about the page, and \`fugaro watch\` is the command item 3 asks for`,
  )
  const failures = runExampleFailures(page)
  assert.deepEqual(
    failures,
    [],
    `the homepage carries the retired \`fugaro run\` example, which item 3 removes from the hero:\n  ${failures.join('\n  ')}`,
  )

  // And each way it comes back is refused, one plant per branch. The first is the example as a
  // command block — what the retired hero carried, and what a page that copied the
  // documentation's manual path would ship; the second is the transcript as prose, which no
  // command-block reading sees and which is why the words are swept too.
  const asCommand = page.replace(
    '<pre class="watch-command"',
    `<pre class="planted">fugaro run --batch demo</pre>\n<pre class="watch-command"`,
  )
  assert.match(
    runExampleFailures(asCommand).join('\n'),
    /the homepage offers "fugaro run --batch demo" as a command the reader can copy/,
    'a `fugaro run` line put back among the page\'s command blocks is not refused, so the manual path is free to return as something the reader is asked to type',
  )
  const asProse = page.replace(
    /<\/main>/,
    `<p>Launch the run yourself with \`fugaro run "${RETIRED_EXAMPLE}"\`.</p></main>`,
  )
  assert.match(
    runExampleFailures(asProse).join('\n'),
    /the homepage still carries the old hero example, the `fugaro run "add a --verbose flag to the export command"` transcript/,
    'the retired hero example put back as prose is not refused, and a transcript is not a command block: the example would ship again with every other gate green',
  )
})

// ---- the "See it" slider: the panel's rows, and the pictures (the brief of 2026-10-21) -----
//
// The "See it" panel used to hold one `<img>` — the owner's `fugaro watch` capture of
// 2026-10-18 (spec item 5, PR #622) — and it holds a slider now (the brief of 2026-10-21:
// "replace the single Fugaro Watch screenshot with a slider"; D246, D251, D252): the console
// page's component, rendering `website/data/home-screens.yaml`, which is the console's own
// three screenshots first and the watch capture last. What the owner's screenshot item asked
// for is unchanged and is what these cases hold: the slot is marked and is where the panel's
// heading leads, the picture is this site's own file at its own declared size, and a reader who
// cannot see it is owed what it shows.
//
// **The three assertions that used to pin the page's pictures are replaced, not deleted.** The
// slot was held to exactly one `<img>`, the whole page to exactly `/logo.png` and
// `/images/fugaro-watch.png`, and the component that renders the slot to exactly one file
// (ZF47). Every one of those is now false by design, and the reading that takes their place is
// stronger: the slot holds exactly the rows the data file names, in order, each with its own
// `src`, `alt`, `width`, `height` and caption, the page's pictures are those rows plus the
// wordmark, and the rows the slider shows a reader with no script are the same four.

const HOME_SCREENS_YAML = 'website/data/home-screens.yaml'
const CONSOLE_SCREENS_YAML = 'website/data/console-screens.yaml'
const HOME_SLOT = 'watch-screenshot'
const HOME_SLIDER_SLOT = 'home-screens'
const WATCH_FILE = 'fugaro-watch.png'

/** The site path a row is served at, composed the way `src/content.ts` composes it. */
const srcOfRow = (row) => `/${row.dir}/${row.file}`

/**
 * Everything the "See it" panel's slider can be wrong about, as sentences a reader of a red run
 * can act on: the slot is where the panel's heading leads, the slider inside it renders exactly
 * the rows the data file names in the file's own order, every row's picture is the row's own
 * file at the row's own size with the row's own `alt` and caption, the dots are the slides'
 * order read twice and every one of them is a named link into its own slide, and the whole
 * page's pictures are those rows and the wordmark and nothing else.
 *
 * The reading is taken over the HTML the build wrote — the page as a reader whose browser runs
 * no script receives it — which is why it is a statement about the artifact rather than about
 * the script that reveals the control bar.
 */
function homeSliderFailures(page, allRows) {
  const out = []
  const doc = parse(page)
  // **Since the owner's rulings of 2026-10-23 (the redesign's Step 3)** the slot is in the "what
  // you get" section, the slider in it is the data file's console rows, and the Watch row (the
  // file's last) is the hero's picture rather than a slide: the page shows it once.
  const watchRow = allRows.find((row) => row.file === WATCH_FILE)
  const rows = allRows.filter((row) => row.file !== WATCH_FILE)

  const panel = findAll(doc, (el) => el.name === 'section' && (el.attrs.get('class') ?? '').split(/\s+/).includes('home-get'))[0]
  if (!panel) return ['the homepage carries no "what you get" section (`section.home-get`), and that section is where the slider is (the owner\'s rulings of 2026-10-23)']

  const slot = findAll(doc, (el) => el.attrs.get('data-slot') === HOME_SLOT)[0]
  if (!slot) return ['the homepage carries no [data-slot="watch-screenshot"], and the slot is where the slides go (changes spec 2026-10-18, item 3)']

  let insidePanel = false
  for (let parent = slot.parent; parent; parent = parent.parent) if (parent === panel) insidePanel = true
  if (!insidePanel) out.push('the slides\' slot is not in the "what you get" section, which is where the page shows what a run looks like (the owner\'s rulings of 2026-10-23)')

  if (findAll(slot, (el) => el.name === 'h2').length !== 1) {
    out.push('the slides\' slot carries no heading of its own, and the slides and the command beside them belong to a named block (the owner\'s rulings of 2026-10-23: a heading on every section)')
  }
  for (const attribute of ['src', 'srcset', 'href', 'style', 'aria-label', 'role']) {
    if (slot.attrs.has(attribute)) out.push(`the slot carries ${attribute}, and the hook this check reads is \`data-slot\` alone (changes spec 2026-10-18, item 3)`)
  }

  const slider = findAll(slot, (el) => el.attrs.get('data-slot') === HOME_SLIDER_SLOT)[0]
  if (!slider) {
    return [
      ...out,
      `the slot holds no [data-slot="${HOME_SLIDER_SLOT}"], and the slider is the panel's own component: the rows of ${HOME_SCREENS_YAML} are what a reader is shown (the brief of 2026-10-21)`,
    ]
  }
  if (!slider.attrs.has('data-slider')) {
    out.push(`the slider carries no \`data-slider\`, and that attribute is how the site's one script finds the block it drives: without it the arrows, the count and the mark on the current dot are bound to nothing`)
  }

  const inside = [...walk(slider)]
  const images = inside.filter((el) => el.name === 'img')
  const slides = inside.filter((el) => el.name === 'li' && el.attrs.has('data-slider-slide'))
  const captions = inside.filter((el) => el.name === 'figcaption')
  const dots = inside.filter((el) => el.name === 'a' && el.attrs.has('data-slider-dot'))

  if (images.length !== rows.length) out.push(`the slot renders ${images.length} <img> for ${rows.length} row(s) in ${HOME_SCREENS_YAML}: the slider is the rows, and a picture the data file does not name is a picture no case here can hold to anything`)
  if (slides.length !== rows.length) out.push(`the track carries ${slides.length} slide(s) for ${rows.length} row(s) in ${HOME_SCREENS_YAML}`)
  if (captions.length !== rows.length) out.push(`the slider renders ${captions.length} caption(s) for ${rows.length} row(s) in ${HOME_SCREENS_YAML}`)
  if (dots.length !== rows.length) out.push(`the slider carries ${dots.length} dot(s) for ${rows.length} row(s) in ${HOME_SCREENS_YAML}`)

  for (const [index, row] of rows.entries()) {
    const where = `the ${index + 1}th slide`
    const img = images[index]
    if (img) {
      if (img.attrs.get('src') !== srcOfRow(row)) out.push(`${where} loads ${JSON.stringify(img.attrs.get('src'))}, want the row's own file under /${row.dir}/`)
      if (img.attrs.get('alt') !== row.alt) out.push(`${where}'s alt is not the row's alt text, which is what a reader gets when the picture does not arrive and what a screen reader reads instead of it`)
      if (img.attrs.get('width') !== String(row.width) || img.attrs.get('height') !== String(row.height)) {
        out.push(`${where} declares ${JSON.stringify(img.attrs.get('width'))} x ${JSON.stringify(img.attrs.get('height'))} and the row carries ${row.width} x ${row.height}: the box the browser reserves is the file's own`)
      }
      const loading = index === 0 ? 'eager' : 'lazy'
      if (img.attrs.get('loading') !== loading) out.push(`${where} is loaded ${JSON.stringify(img.attrs.get('loading') ?? null)}, and the first slide is the one the frame shows while every other one arrives when a reader scrolls to it (${loading})`)
      if (img.attrs.get('decoding') !== 'async') out.push(`${where} declares decoding ${JSON.stringify(img.attrs.get('decoding') ?? null)}, and every slide decodes asynchronously so a 1920-wide WebP does not hold up the frame around it`)
    }
    const caption = captions[index]
    if (caption) {
      if (textOf(caption).trim() !== row.caption) out.push(`${where}'s caption is ${JSON.stringify(textOf(caption).trim())} and the row's is ${JSON.stringify(row.caption)}: the line under a picture is the row's own`)
      if (slides[index] && ![...walk(slides[index])].includes(caption)) out.push(`${where}'s caption is not inside the ${index + 1}th slide, so it reads as a caption about the picture before it`)
    }
    const slide = slides[index]
    const dot = dots[index]
    if (slide) {
      const id = slide.attrs.get('id')
      if (!id) out.push(`${where} carries no id, so no dot can reach it`)
      else if (dot && dot.attrs.get('href') !== `#${id}`) out.push(`${where}'s dot points at ${JSON.stringify(dot.attrs.get('href'))} and the slide is #${id}: a dot that points elsewhere sends a reader to the wrong picture`)
    }
    if (dot && (dot.attrs.get('aria-label') ?? '').trim() === '') out.push(`${where}'s dot has no accessible name, and it carries no text: a screen reader announces it as a link and nothing else`)
  }

  // **The page's pictures are these rows and the wordmark, and nothing else.** The old reading
  // was the same statement about a page that carried two files; the slider makes it four, and
  // the rule is what it was: a homepage that grew a picture the data file does not name — an
  // illustration, an icon, a screenshot of a screen the run does not have — is refused here
  // (Global Constraint 9).
  // The rows' pictures on the whole page: the Watch capture once, in the hero, before the
  // slides, and each console row once, as its slide, in the file's order. The page's other
  // pictures (the wordmark, the run's zoom diagrams, the Spend screen beside the cost) are its
  // own files and not rows of this data file; every picture is served from this site.
  const pictures = [...walk(doc)]
    .filter((el) => ['img', 'picture', 'video', 'source'].includes(el.name))
    .map((el) => el.attrs.get('src') ?? el.attrs.get('srcset') ?? '')
  // (The run section and the cost section show two of the console's screens beside their own
  // steps, so a console row's file may appear outside the slider; the slider's own order is held
  // slide by slide above.)
  const watchSrc = watchRow ? srcOfRow(watchRow) : null
  const watchAt = watchSrc ? pictures.filter((src) => src === watchSrc).length : 0
  if (pictures[0] !== '/logo.png' || (watchSrc && watchAt !== 1) || (watchSrc && pictures[1] !== watchSrc) || pictures.some((src) => !src.startsWith('/'))) {
    out.push(`the page renders the pictures ${JSON.stringify(pictures)}: the wordmark first, the Watch capture once and first after it (the hero's picture), and nothing from another origin (owner spec of 2026-10-18, item 5; the owner's rulings of 2026-10-23; Global Constraint 9)`)
  }
  return out
}

/** Build a copy with `website/data/home-screens.yaml` replaced, and hand back what it made. */
function buildWithHomeScreens(t, contents) {
  return buildCopy(t, ({ site }) => {
    writeFileSync(path.join(site, 'data', 'home-screens.yaml'), contents)
  })
}

/** The rows of a data file, written back as the file writes them. */
function screensYAML(rows) {
  const lines = ['version: 1', 'screens:']
  for (const row of rows) {
    lines.push(`  - file: ${row.file}`)
    lines.push(`    alt: ${JSON.stringify(row.alt)}`)
    lines.push(`    caption: ${JSON.stringify(row.caption)}`)
    if (row.dir && row.dir !== 'screenshots') lines.push(`    dir: ${row.dir}`)
    lines.push(`    width: ${row.width}`)
    lines.push(`    height: ${row.height}`)
  }
  return `${lines.join('\n')}\n`
}

test('the "See it" slot holds the rows the homepage\'s data file names, in order, Watch last', () => {
  // The strong reading ZF47's three assertions are replaced by. It is read over the built
  // homepage and against the data file, and what it holds is every fact each row carries: the
  // picture's own file, its `alt`, the box the browser reserves, and the caption under it.
  const rows = readScreenRows(HOME_SCREENS_YAML)
  assert.ok(rows.length >= 2, `${HOME_SCREENS_YAML} carries ${rows.length} row(s), and this case asserts nothing about a slider with fewer than two slides`)
  assert.equal(
    rows[rows.length - 1].file,
    WATCH_FILE,
    `the last row of ${HOME_SCREENS_YAML} is ${JSON.stringify(rows[rows.length - 1].file)}, and the brief puts the Watch capture there: "Fugaro Watch moves to the end as a 'for the terminal crowd' slide" (D252)`,
  )

  const page = builtPage('')
  const failures = homeSliderFailures(page, rows)
  assert.deepEqual(failures, [], `the "See it" panel's slider is not the rows the data file names:\n  ${failures.join('\n  ')}`)
})

test('the three console rows are the console page\'s own rows, alt and caption verbatim', () => {
  // One picture has one `alt` text and one caption. The homepage's first rows are the console
  // page's own screenshots, and a page that paraphrased them would be a second copy of the
  // sentence that drifts first (Global Constraint 6) — so the two files are held to each other,
  // row for row, on the keys a reader is owed: the file, the `alt` and the caption.
  const home = readScreenRows(HOME_SCREENS_YAML)
  const console_ = new Map(readScreenRows(CONSOLE_SCREENS_YAML).map((row) => [row.file, row]))
  const shared = home.filter((row) => console_.has(row.file))
  assert.equal(
    shared.length,
    home.length - 1,
    `the homepage's rows are ${JSON.stringify(home.map((row) => row.file))} and ${shared.length} of them are rows of ${CONSOLE_SCREENS_YAML}: the brief leads with the desktop console's own screenshots, and the Watch capture is the one slide that is not one of them (D251, D252)`,
  )
  for (const row of shared) {
    const source = console_.get(row.file)
    assert.equal(row.alt, source.alt, `${HOME_SCREENS_YAML}'s ${row.file} carries an alt text of its own, and the same picture is described by ${CONSOLE_SCREENS_YAML}: one picture has one account of what it shows (Global Constraint 6)`)
    assert.equal(row.caption, source.caption, `${HOME_SCREENS_YAML}'s ${row.file} carries a caption of its own, and the same picture carries ${CONSOLE_SCREENS_YAML}'s: the line under a picture is the row's own, and two rows for one picture are two lines that drift apart`)
    assert.equal(row.width, source.width, `${HOME_SCREENS_YAML}'s ${row.file} declares a width of its own, and the file is the console page's own: the box the browser reserves is the file's pixels, which are one number (W1.4)`)
    assert.equal(row.height, source.height, `${HOME_SCREENS_YAML}'s ${row.file} declares a height of its own, and the file is the console page's own: the box the browser reserves is the file's pixels, which are one number (W1.4)`)
  }
})

test('the page holds the Watch capture at the size the design record gives it', () => {
  // The owner's item 5, unmoved by the slider: the capture is the owner's real `fugaro watch`
  // screen, and the box the page declares for it is the one `docs/website/redesign/README.md`
  // records — the capture's own shape at its logical size, which is what stops the block under
  // it moving when the file lands. The row is read from the data file, the record from the
  // repository, and the two are held to each other.
  const row = readScreenRows(HOME_SCREENS_YAML).find((one) => one.file === WATCH_FILE)
  assert.ok(row, `${HOME_SCREENS_YAML} carries no ${WATCH_FILE} row, and the panel has shown that capture since PR #622 (owner spec 2026-10-18, item 5)`)
  assert.equal(row.dir, 'images', `the Watch row is served from ${JSON.stringify(row.dir)}, and the capture PR #622 committed is at public/images/fugaro-watch.png: the design record, the transcription beside it and the page all name that path (owner spec 2026-10-18, item 5)`)
  assert.ok((row.alt ?? '').trim().length >= 40, `the Watch row's alt is ${JSON.stringify(row.alt ?? '')}, and the picture is the owner's real \`fugaro watch\` screen: a reader who cannot see it is owed what the screen shows, from the transcription that ships beside the image (public/images/fugaro-watch.txt)`)

  const record = /`website\/public\/images\/fugaro-watch\.png` \((\d+) x (\d+) px, a 2x retina capture, so (\d+) x (\d+) logical\)/.exec(
    repoFile('docs/website/redesign/README.md'),
  )
  assert.ok(record, 'docs/website/redesign/README.md no longer records the capture\'s pixel size and its logical size, so the declared dimensions cannot be held to the file they are about (owner spec 2026-10-18, item 5)')
  const [, captureW, captureH, logicalW, logicalH] = record.map(Number)
  assert.equal(
    row.width / row.height,
    captureW / captureH,
    `the Watch row declares ${row.width} x ${row.height} and the capture is ${captureW} x ${captureH} (${logicalW} x ${logicalH} logical): the declared box has to be the file's own shape, or the browser reserves the wrong space and scales the picture into it (owner spec 2026-10-18, item 5)`,
  )
  assert.deepEqual(
    [row.width, row.height],
    [logicalW, logicalH],
    `the Watch row declares ${row.width} x ${row.height} and the capture is ${logicalW} x ${logicalH} at its logical size: a 2x capture is declared at the size it occupies on the page (owner spec 2026-10-18, item 5)`,
  )
})

test('every slide is in the HTML a reader receives, with no script run at all', () => {
  // **The no-JS case.** Nothing about this slider is built at runtime: the markup renders every
  // row — the picture with its `alt` and its declared box, the caption under it, and the dot
  // that is a link into its slide — and the script's half is the `hidden` control bar it
  // reveals. So a reader whose browser runs no script reaches all four slides by scrolling the
  // track or by following a dot, which is the site's rule (D183) and the reason the fallback is
  // a property of the markup rather than a promise about the script.
  // The slides are the console rows; the Watch row is the hero's picture (the owner's rulings of
  // 2026-10-23).
  const rows = readScreenRows(HOME_SCREENS_YAML).filter((row) => row.file !== WATCH_FILE)
  const doc = parse(builtPage(''))
  const slider = findAll(doc, (el) => el.attrs.get('data-slot') === HOME_SLIDER_SLOT)[0]
  assert.ok(slider, `the built page carries no [data-slot="${HOME_SLIDER_SLOT}"], and the slides are rendered by the console page's component rather than by a script (D183)`)

  const images = [...walk(slider)].filter((el) => el.name === 'img')
  assert.deepEqual(
    images.map((el) => el.attrs.get('src')),
    rows.map(srcOfRow),
    `the slider's pictures in the HTML a reader receives are ${JSON.stringify(images.map((el) => el.attrs.get('src')))}, and the data file names ${JSON.stringify(rows.map(srcOfRow))}: with no script the pictures are the whole of the slider, so every row has to be in the markup`,
  )
  assert.deepEqual(
    [...walk(slider)].filter((el) => el.name === 'figcaption').map((el) => textOf(el).trim()),
    rows.map((row) => row.caption),
    'the captions in the HTML a reader receives are not the data file\'s, in order: a caption written by the script is a caption a reader without one never sees',
  )

  // Every dot resolves, in the document the reader was sent: the fragment each one carries
  // names a slide that is in this page. `linkcheck.mjs` proves the same for every link on every
  // page; this is the slider's own half of it, stated where the slides are read.
  const ids = new Set([...walk(doc)].filter((el) => el.attrs.has('id')).map((el) => el.attrs.get('id')))
  for (const dot of [...walk(slider)].filter((el) => el.name === 'a' && el.attrs.has('data-slider-dot'))) {
    const href = dot.attrs.get('href') ?? ''
    assert.ok(
      href.startsWith('#') && ids.has(href.slice(1)),
      `a dot links to ${JSON.stringify(href)}, which names no element of the page: with no script the dot's own jump is the whole of what it does, and a fragment that resolves to nothing is a control that does nothing (D183)`,
    )
  }
})

test('a slider that is not the rows the data file names is refused, plant by plant', (t) => {
  // The reading above is only worth its lines if it disagrees with a page that is not the one
  // the data file describes. Three plants are edits to the built page's own HTML — the shape
  // every defect would reach a reader in — and two are builds of a mutated data file, which is
  // the half a page-shaped plant cannot prove: that the markup follows the file rather than
  // agreeing with it by coincidence.
  const rows = readScreenRows(HOME_SCREENS_YAML)
  const page = builtPage('')

  /** The offset of the `index`th slide's `<li>` in a built page. */
  function slideAt(html, index) {
    const at = [...html.matchAll(/<li class="screen[^"]*" data-slider-slide id="home-screen-\d+"/g)][index]
    assert.ok(at, `the built page carries no ${index + 1}th <li> slide, so this plant cannot be made`)
    return at.index
  }

  /** The page with the `index`th slide's caption replaced by `caption`. */
  const withCaption = (html, index, caption) => {
    const from = slideAt(html, index)
    const to = html.indexOf('</li>', from)
    return (
      html.slice(0, from) +
      html.slice(from, to).replace(/<figcaption[^>]*>[\s\S]*?<\/figcaption>/, `<figcaption class="screen-caption">${caption}</figcaption>`) +
      html.slice(to)
    )
  }

  const spent = page.replace(/\s*<li class="screen[^"]*" data-slider-slide id="home-screen-1"[\s\S]*?<\/li>/, '')

  const defects = [
    {
      what: 'a caption that is not the row\'s',
      html: withCaption(page, 0, 'A screenshot of something else entirely'),
      wants: /caption is/,
    },
    {
      what: 'a slide dropped out of the markup',
      html: spent,
      wants: /renders 2 <img> for 3 row\(s\)/,
    },
  ]
  for (const { what, html, wants } of defects) {
    assert.notEqual(html, page, `${what}: the planted edit changed nothing, so this fixture proves nothing`)
    const failures = homeSliderFailures(html, rows)
    assert.ok(failures.some((line) => wants.test(line)), `${what} was not refused (${wants}); the reading said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }

  // **A row dropped from the file** and **the Watch row moved off the end**: the page is built
  // again from the mutated file, and the reading is taken against the file as it ships — so a
  // page that rendered the same four slides whatever the file said fails both.
  // A build whose data file drops a console row renders one slide fewer, and the reading of the
  // shipped rows refuses it.
  const fewer = rows.filter((_, index) => index !== 1)
  const dropped = buildWithHomeScreens(t, screensYAML(fewer))
  const droppedPage = readFileSync(path.join(dropped.dist, 'index.html'), 'utf8')
  assert.equal(
    [...droppedPage.matchAll(/<li class="screen[^"]*" data-slider-slide/g)].length,
    fewer.filter((row) => row.file !== WATCH_FILE).length,
    `a build with ${fewer.length} row(s) in ${HOME_SCREENS_YAML} rendered a different number of slides: the slider is the console rows, and a slide the file does not name is a picture nothing on the site accounts for`,
  )
  assert.ok(
    homeSliderFailures(droppedPage, rows).some((line) => /renders 2 <img> for 3 row\(s\)/.test(line)),
    'the reading did not refuse the build whose data file dropped a console row',
  )

  // And the file's order is the order a reader meets the slides in: a build with the console
  // rows the other way round renders them the other way round, and the reading of the shipped
  // order refuses it.
  const consoleRows = rows.filter((row) => row.file !== WATCH_FILE)
  const reversed = [...consoleRows].reverse()
  const moved = buildWithHomeScreens(t, screensYAML([...reversed, ...rows.filter((row) => row.file === WATCH_FILE)]))
  const movedPage = readFileSync(path.join(moved.dist, 'index.html'), 'utf8')
  const movedSlider = findAll(parse(movedPage), (el) => el.attrs.get('data-slot') === HOME_SLIDER_SLOT)[0]
  assert.deepEqual(
    [...walk(movedSlider)].filter((el) => el.name === 'img').map((el) => el.attrs.get('src')),
    reversed.map(srcOfRow),
    `a build with the console rows reversed in ${HOME_SCREENS_YAML} renders its slides in another order: the file's order is the order a reader meets them in`,
  )
  assert.ok(
    homeSliderFailures(movedPage, rows).some((line) => /want the row's own file/.test(line)),
    'the reading did not refuse the build whose slides are not in the shipped file\'s order',
  )
})

test('the design record quotes the caption the site ships', () => {
  // The record tells a reader what the "See it" panel's caption says, and it quotes the
  // sentence to do it. A quote rots the moment the caption is rewritten, and this one has
  // outlived a caption already: it quoted the chat's, and the owner's approved draft of
  // 2026-10-19 ("Home, draft 2 (slider)") moved the picture and its caption into the slider's
  // third panel and deleted the component the record still pointed at. The record is a
  // description of this branch's artifact (P9, D102), so what it quotes of the artifact is
  // held to the artifact here, in the file that already holds the caption to its shape.
  const page = builtPage('')
  const caption = classText(page, 'watch-note')
  assert.ok(caption, 'the "See it" panel carries no caption under its command, so there is nothing for the design record to quote (the owner\'s approved draft of 2026-10-19)')
  const record = repoFile('docs/website/redesign/README.md')
  assert.ok(
    normalize(record).includes(normalize(caption)),
    `docs/website/redesign/README.md does not quote the caption the built page ships. The page says ${JSON.stringify(caption)}; the record's account of the caption quotes a sentence that is not on the site. The record describes the site this branch builds, so the sentence it quotes moves with the caption (P9, D102; Global Constraint 6)`,
  )

  // And where the record says the picture is, which is a pointer at the tree and rots the same
  // way a quote does: the record's section on the capture named
  // `website/src/components/AgentChat.astro` while that component rendered the slot, and the
  // draft of 2026-10-19 moved the slot into the slider's third panel and deleted the file. So
  // every component path the section points at has to be a component this tree has, and the one
  // that renders the slot has to be among them — read from the tree rather than from the record.
  const renders = filesUnder(path.join(SITE, 'src/components'))
    .filter((file) => readFileSync(path.join(SITE, 'src/components', file), 'utf8').includes('data-slot="watch-screenshot"'))
  assert.deepEqual(
    renders,
    ['HomeWhatYouGet.astro'],
    `the watch screenshot's slot is rendered by ${JSON.stringify(renders)}, and the record's account of where the picture is is read against that component (P9; the owner's approved draft of 2026-10-19)`,
  )
  const where = section('docs/website/redesign/README.md', 'The fugaro watch screenshot (owner spec of 2026-10-18, item 5)')
  const pointers = [...new Set([...where.matchAll(/website\/src\/components\/([\w.-]+\.astro)/g)].map((m) => m[1]))]
  assert.ok(
    pointers.length > 0,
    `docs/website/redesign/README.md's section on the capture names no component of website/src/components, and the record has to say where the picture is: the picture is in the slot ${renders[0]} renders (P9; the owner's approved draft of 2026-10-19)`,
  )
  for (const component of pointers) {
    assert.ok(
      existsSync(path.join(SITE, 'src/components', component)),
      `docs/website/redesign/README.md's section on the capture points at website/src/components/${component}, and this tree has no such component: the record describes the site this branch builds, so its pointers move with the tree (P9; the owner's approved draft of 2026-10-19)`,
    )
  }
  assert.ok(
    pointers.includes(renders[0]),
    `docs/website/redesign/README.md's section on the capture names ${JSON.stringify(pointers)} and not ${renders[0]}, the component that renders the watch screenshot's slot: the record's account of where the picture is is a pointer at the tree (P9; the owner's approved draft of 2026-10-19)`,
  )
})

// ---- the picture of the run: the gates the retired four-step flow left behind ----------------
//
// The owner's ruling of 2026-10-20 retires `ArchitectureDiagram.astro` and the four-step flow
// with it, and asks for the gates that read the flow to be replaced, one for one, by gates that
// read what the pages show now. The four tests below are the flow's four gates, kept as gates
// and moved onto the picture that took the flow's place on `/how-it-works/` — the drawing and
// the run in words under it — and onto the block that took its place on the homepage, the
// slider. Where the flow's gate read the flow's four steps, these read the drawing's four boxes;
// where it read the flow's captions, these read every word on the figure; where it read the
// flow's scroll-driven reveal, these read the motion the two pages actually keep.

test("the run is How It Works' own picture of one, and the homepage carries its one run section instead", () => {
  const ruling = item4()
  assert.equal(ruling.steps.length, 4, `the changes spec's item 4 names ${ruling.steps.length} steps, and the picture draws four (changes spec 2026-10-18, item 4)`)

  // **One page carries the picture.** The owner's approved draft of 2026-10-19 ("Home, draft 2
  // (slider)") is the homepage's three blocks — the hero, the slider and the call to action —
  // and the picture of a run is not one of them: it is `/how-it-works/`'s opening, where the
  // four steps have the room to be drawn and where the run in words under them is. The homepage
  // is where the retired flow's absence is read, because the flow is what stood in the slider's
  // place; `website/test/how-it-works.mjs` reads the same fact from the other side, refusing a
  // diagram on any page but that one.
  const home = parse(builtPage(''))
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
  assert.equal(
    findAll(home, (el) => el.name === 'figure' && classes(el).includes('run-diagram')).length,
    0,
    `the homepage renders a <figure class="run-diagram">, and the draft's homepage is the hero, the slider and the call to action: the picture of the run is /how-it-works/'s opening (the owner's approved draft of 2026-10-19; changes spec 2026-10-18, item 4)`,
  )
  assert.equal(
    pictureOf(builtPage('')).flows,
    0,
    'the homepage renders the retired four-step flow: the owner\'s ruling of 2026-10-20 deletes `ArchitectureDiagram.astro` and the flow with it, and the block that stands in its place is the slider (website/test/slider.mjs holds the slider\'s own shape)',
  )

  // And the slider is the block in its place, read as the retired gate read the picture: three
  // slides, three panels, all of them in the HTML a reader receives, each dot a link to the
  // panel it shows. A homepage whose dots pointed at nothing would be a homepage whose blocks a
  // reader without JavaScript cannot reach — which is what the flow's own gate said about the
  // figure when it asked a page to render exactly one of them. (The owner's feedback of
  // 2026-10-21, item 3, turned the strip of tabs into this slider; the slides, the dots and the
  // two step controls are read in full in `website/test/slider.mjs`, and this is the homepage's
  // own gate keeping the one reading the retired flow's gate left behind.)
  // **The slider is retired by the owner's rulings of 2026-10-23** (the redesign's Step 3, lever
  // 1: the run as ONE section instead of the three overlapping explainers). The homepage carries
  // no slider panels, and the run is one section with the run's four steps, each beside a real
  // picture (`test/a11y.mjs` holds its list): the drawing stays /how-it-works/'s own.
  assert.equal(
    findAll(home, (el) => classes(el).includes('panel') || classes(el).includes('slider-dot')).length,
    0,
    'the homepage renders the retired slider\'s panels or dots (the owner\'s rulings of 2026-10-23)',
  )
  const runs = findAll(home, (el) => el.name === 'section' && classes(el).includes('home-run'))
  assert.equal(runs.length, 1, `the homepage renders ${runs.length} run sections, and the run is ONE section (the owner's rulings of 2026-10-23)`)
  assert.ok(
    findAll(runs[0], (el) => el.name === 'a' && el.attrs.get('href') === '/how-it-works/').length === 1,
    'the homepage\'s run section does not link to /how-it-works/, where the run is drawn whole',
  )

  const drawn = pictureOf(builtPage('how-it-works'))
  assert.equal(
    drawn.figures,
    1,
    `the /how-it-works/ page renders ${drawn.figures} pictures of a run, and it renders one: the picture is the page's opening (the owner's approved draft of 2026-10-19)`,
  )
  const failures = pictureFailures(builtPage('how-it-works'), ruling)
  assert.deepEqual(
    failures,
    [],
    `the /how-it-works/ page's picture of a run is not the one the changes spec's item 4 describes:\n  ${failures.join('\n  ')}`,
  )

  // The run in words is the detail, and the detail is not on the homepage: item 4 removes the
  // duplicate text-heavy "how it works" section from the homepage and leaves the explanation to
  // the page that opens with the picture. A section that came back would be a second description
  // of the run to keep in step with the first. (This is the retired gate's count of
  // `section.run-detail`, moved onto the section that carries the same detail now: the
  // run-detail section went with the flow in PR #726, and the list under the picture is what
  // stands in its place.)
  assert.equal(
    pictureOf(builtPage('')).details,
    0,
    'the homepage renders the run in words: item 4 removes the duplicate text-heavy "how it works" section from the homepage and leaves the detail to the How It Works page, where it is the list under the picture (changes spec 2026-10-18, item 4)',
  )
  assert.equal(
    drawn.details,
    1,
    'the How It Works page renders no run in words under its picture, and item 4 puts the detail under the picture there: the drawing alone is a picture a reader who cannot see it is told nothing by (changes spec 2026-10-18, item 4)',
  )
})

test("the flow's scroll-driven reveal is retired with it, and every motion the pages keep answers a reader who asks for less", () => {
  // The retired gate (W1.4 step 3, D105) held the flow's reveal to two guards: a
  // `@supports (animation-timeline: view())` feature query and a
  // `@media (prefers-reduced-motion: no-preference)` preference query. The reveal is retired
  // with the flow, and the gate that read it is replaced by the rule the two readings below
  // state: the mechanism is refused outright, because neither thing that took the flow's place
  // animates — the slider's panels are the fragment's and the drawing is a still picture — and
  // the motion the pages do keep (smooth scrolling, and the transitions on the copy control and
  // the links) is covered on each page by its own answer to a reader who asks for less.
  for (const route of ['', 'how-it-works']) {
    const where = route === '' ? '/' : `/${route}/`
    const css = builtCSS(builtPage(route))
    assert.deepEqual(
      pageMotionFailures(css),
      [],
      `the ${where} page keeps motion a reader who asked for less would still be given:\n  ${pageMotionFailures(css).join('\n  ')}`,
    )
    const flat = css.replace(/\s+/g, '')
    for (const property of ['animation-timeline', 'scroll-timeline', 'view-timeline']) {
      // A feature query names the property too, and asks rather than declares.
      assert.ok(
        !flat.replace(/@supports[^{]*/g, '').includes(`${property}:`),
        `the ${where} page declares \`${property}\`, and the page is not revealed by scrolling: the flow's scroll-driven animation is retired with the flow (the owner's ruling of 2026-10-20; W1.4 step 3, D105). The run picture's step reveal plays once in view, on a timer, not on the scroll (the owner's feedback of 2026-10-24)`,
      )
    }
    // The homepage has a motion layer since the owner's rulings of 2026-10-23 (the redesign's
    // Step 3, lever 7: motion with reduced-motion fallbacks): the hero's capture opens once as the
    // page loads and the live card's rows arrive, so its keyframes are allowed, held by the
    // reduced-motion reading above. How It Works keeps its drawing still; its only keyframes are
    // the run picture's step reveal (owner-approved motion opportunities 2026-10-24), played once
    // when the picture comes into view.
    if (route !== '') {
      const named = [...flat.matchAll(/@keyframes([\w-]+)/g)].map((match) => match[1])
      assert.deepEqual(
        named.filter((name) => !name.startsWith('run-step-') && !name.startsWith('run-link-')),
        [],
        `the ${where} page declares a \`@keyframes\` animation other than the run picture's step reveal, and the drawing is a still picture (the owner's ruling of 2026-10-20; W1.4 step 3, D105)`,
      )
    }
  }
})

test("every word of the picture is a line of the section it cites, in that section's order", () => {
  // The same rule `pictureFailures` applies, said here one page at a time and with the page's
  // name in the message, because this is the acceptance a reader can check: the figure and the
  // run in words under it say what the repository says, in the order the repository says it.
  // The retired gate read the flow's four captions this way; the labels drawn inside the figure
  // and the list of the same run under it are what stands in their place, and the slider's own
  // three lines are held to their sections the same way by `website/test/slider.mjs`'s
  // `stepFailures`, which is the homepage's side of this acceptance.
  const where = '/how-it-works/'
  const picture = pictureOf(builtPage('how-it-works'))
  const regions = picture.regions.map((region) => ({ ...region, where: `${where}'s ${region.where}` }))
  assert.equal(
    regions.length,
    4,
    `the ${where} page's picture is read as ${regions.length} regions and the page carries four — the wide drawing, the tall drawing, the facts under them and the second list under those: a reading that found fewer would pass the page on labels it never read (the owner's draft of 2026-10-19)`,
  )

  for (const region of regions) {
    assert.ok(
      region.labels.length > 0,
      `nothing is labelled in ${region.where}, and the picture is its words: a region this check reads as empty is a region this check passes without reading (the owner's draft of 2026-10-19)`,
    )
    const reached = new Map()
    for (const label of region.labels) {
      const held = quotedSection(label.source)
      assert.notEqual(
        held.text,
        null,
        `${region.where} attributes the label ${JSON.stringify(label.text)} to ${JSON.stringify(label.source)}, and a label is held to a section of a document this repository carries (changes spec 2026-10-18, item 4)`,
      )
      const line = normalize(label.text)
      const from = reached.get(label.source) ?? 0
      const at = normalize(held.text).indexOf(line, from)
      assert.notEqual(
        at,
        -1,
        `${region.where}'s label ${JSON.stringify(label.text)} does not appear in ${label.source} at or after the label before it: every word of the picture is the repository's own words in the order their document writes them, rather than the page's — and a line that moved above another is the page describing a run the repository does not (changes spec 2026-10-18, item 4; Global Constraint 6, RF11)`,
      )
      reached.set(label.source, at + line.length)
    }
  }
})

test('the picture refusals fire on the defect each one names', () => {
  // The cases below are the check's own proof: the fixture page — the built page's own figure and
  // the built page's own run in words, side by side — passes, and each fixture that breaks one
  // rule makes exactly that rule's line appear. Without them a check that read no boxes and no
  // labels at all would pass every page, which is the failure mode a replacement gate this new
  // has (the owner's ruling of 2026-10-20).
  const ruling = item4()
  const page = builtPage('how-it-works')
  const figure = /<figure class="run-diagram"[\s\S]*?<\/figure>/.exec(page)
  const words = /<section class="run-words"[\s\S]*?<\/section>/.exec(page)
  assert.ok(figure && words, "the built /how-it-works/ page carries no `figure.run-diagram` and `section.run-words`, and the fixtures below are drawn from the page's own markup")
  const fixture = (body) => `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>t</h1>${body}</main></body></html>`
  const picturePage = (figureMarkup = figure[0], wordsMarkup = words[0]) => fixture(figureMarkup + wordsMarkup)
  const failing = (html) => pictureFailures(html, ruling)
  const control = picturePage()
  assert.deepEqual(failing(control), [], 'the fixture page does not pass the check it is the control for')

  // The four boxes, which are the ruling's four names: one renamed, two exchanged, one with no
  // name at all.
  const renamed = picturePage(figure[0].replace('>Your agent</text>', '>Your helper</text>'), words[0])
  assert.notEqual(renamed, control, 'the fixture did not rename a box: the page\'s own markup carries no `>Your agent</text>`')
  assert.ok(
    failing(renamed).some((line) => /draws the boxes .*and the changes spec's item 4 names/.test(line)),
    'a box renamed passes the check: the four boxes are the steps item 4 names, and a name the ruling does not carry is a claim about how a run works',
  )

  const tag = (name) => new RegExp(`<text class="run-name"[^>]*>${name}</text>`)
  const swap = (html, one, other) => {
    const first = tag(one).exec(html)
    const second = tag(other).exec(html)
    assert.ok(first && second, `the built page's figure names no \`${one}\` and \`${other}\` box, and this fixture exchanges them`)
    return html.replace(first[0], '\u0000').replace(second[0], first[0]).replace('\u0000', second[0])
  }
  assert.ok(
    failing(picturePage(swap(figure[0], 'Your agent', 'Fugaro'), words[0])).some((line) => /draws the boxes .*and the changes spec's item 4 names/.test(line)),
    "two boxes swapped pass the check: the order is the run's order, and a picture that drew Fugaro asking for the work describes a run that cannot happen",
  )
  assert.ok(
    failing(picturePage(figure[0].replace('>Your agent</text>', '></text>'), words[0])).some((line) => /draws the boxes \[\"\"/.test(line)),
    'a box left with no name passes the check: every box of the picture is named, and a box whose name is gone says nothing about the step it draws',
  )

  // The labels: one paraphrased, one attributed to a section that is not there, and two of one
  // section exchanged — each still the repository's words if the *file* were what bounded them.
  const paraphrased = picturePage(figure[0].replace('>Fan out these three</text>', '>Fan out these two</text>'), words[0])
  assert.notEqual(paraphrased, control, 'the fixture did not paraphrase a label: the page\'s own markup carries no `>Fan out these three</text>`')
  assert.ok(
    failing(paraphrased).some((line) => /does not appear in docs\/website\/redesign\/changes-spec-2026-10-18\.md at or after the label before it/.test(line)),
    'a paraphrased label passes the check: every word of the picture is the repository\'s own (Global Constraint 6, RF11)',
  )

  const elsewhere = picturePage(figure[0].replace('data-source="plugin/skills/parallelism/SKILL.md"', 'data-source="plugin/skills/parallelism/SKILL.md#Nope"'), words[0])
  assert.notEqual(elsewhere, control, 'the fixture did not misattribute a label: the page\'s own markup carries no `data-source="plugin/skills/parallelism/SKILL.md"`')
  assert.ok(
    failing(elsewhere).some((line) => /a label is held to a section of a document this repository carries/.test(line)),
    'a label attributed to a section that does not exist passes the check: a label is quoted from a section this check reads',
  )

  // Two labels of one section, each quoting a line of it, in the order the section writes them:
  // a check that held each label to its section and no further would let them swap, and the page
  // would then say the draft opens before the job that opens it. The cursor is what keeps each
  // label where the document put it, and the pair below is README.md's `## How it works` own two
  // lines, moved into each other's place in the list under the picture.
  const spanAt = (markup, text) => {
    const at = markup.indexOf(text)
    assert.notEqual(at, -1, `the built page's words list carries no ${JSON.stringify(text)}, and this fixture moves it`)
    const start = markup.lastIndexOf('<span', at)
    return markup.slice(start, markup.indexOf('</span>', at) + '</span>'.length)
  }
  const jobLine = spanAt(words[0], 'Cloud Run job per repository workflow')
  const draftLine = spanAt(words[0], 'a draft opens at the first push')
  const moved = words[0].replace(draftLine, '').replace(jobLine, draftLine + jobLine)
  const swapped = picturePage(figure[0], moved)
  assert.notEqual(swapped, control, 'the fixture did not move a line: the page\'s own words list carries no such spans')
  assert.ok(
    failing(swapped).some((line) => /at or after the label before it/.test(line)),
    "the draft's line and the job's line swapped pass the check: each is still a line of README.md's \"## How it works\", and the order is what keeps each on the step it describes",
  )

  // And the reader itself: a page that renders no picture is the failure a check reading "no
  // boxes" would otherwise report as success, and a page that renders the retired flow again is
  // rendering the component the ruling deleted.
  assert.ok(
    failing(picturePage('', words[0])).some((line) => /renders 0 pictures of a run/.test(line)),
    'a page with no figure passes the check: the page that carries the run is the page whose picture this check reads',
  )
  assert.ok(
    failing(picturePage(figure[0] + '<ol class="flow-steps"><li class="flow-step">a step</li></ol>', words[0])).some((line) => /renders 1 `ol\.flow-steps`/.test(line)),
    'a page that renders the retired flow again passes the check: the ruling deletes the component, and the flow is not a second description the pages may keep',
  )
  assert.ok(
    failing(picturePage(figure[0], '')).some((line) => /renders 0 lists of the run in words/.test(line)),
    'a page whose run in words is gone passes the check: the drawing alone is a picture a reader who cannot see it is told nothing by (changes spec 2026-10-18, item 4)',
  )

  // The motion gate: the page's own answer to a reader who asks for less, and the scroll-driven
  // reveal the flow brought its steps in with.
  const guard = '@media (prefers-reduced-motion:reduce){*,:before,:after{scroll-behavior:auto!important;transition-duration:1ms!important;transition-delay:0s!important;animation-duration:1ms!important;animation-iteration-count:1!important;animation-delay:0s!important}}'
  assert.deepEqual(pageMotionFailures('.copy-button{transition:color var(--duration-fast)}html{scroll-behavior:smooth}' + guard), [], 'a page whose motion is guarded does not pass the motion check its own cases are about')
  assert.ok(
    pageMotionFailures('.copy-button{transition:color var(--duration-fast)}').some((line) => /transition: color var\(--duration-fast\), and no prefers-reduced-motion: reduce rule neutralises it/.test(line)),
    'a transition with no reduce rule passes the motion check: motion is for a reader who allows it (W1.4 step 3, D105)',
  )
  assert.ok(
    pageMotionFailures('html{scroll-behavior:smooth}').some((line) => /scroll-behavior: smooth, and no prefers-reduced-motion: reduce rule neutralises it/.test(line)),
    'smooth scrolling with no reduce rule passes the motion check: the reader who asks for less is given a page that does not slide under them (W1.4 step 3, D105)',
  )
  assert.ok(
    pageMotionFailures(`.step-card{animation-timeline:view()}${guard}`).some((line) => /declares animation-timeline: view\(\), and the page is not revealed by scrolling/.test(line)),
    "the flow's scroll-driven reveal passes the motion check as long as it is guarded: the flow is retired and neither thing in its place animates, so the mechanism is refused rather than guarded (the owner's ruling of 2026-10-20)",
  )
  assert.ok(
    pageMotionFailures(`@supports (animation-timeline:view()){@media (prefers-reduced-motion:no-preference){.step-card{animation-timeline:view()}}}${guard}`).some((line) => /and the page is not revealed by scrolling/.test(line)),
    'a guarded scroll-driven rule passes, and the flow\'s reveal is still retired',
  )
  assert.ok(
    pageMotionFailures(`${guard}.step-card{animation:reveal 2s} .step-card:hover{transition:none}`).length === 0,
    'a neutralised animation and a disabled transition are both refused by the motion check, and neither is motion a reader is given',
  )
})


// ---- the two blocks the bold redesign adds to the homepage --------------------------------

/**
 * The homepage's two new blocks, as the lines they quote and the document each line cites: the
 * run band (`HomeRunBand.astro`) and the grid of what a run leaves behind
 * (`HomeWhatYouGet.astro`).
 *
 * **Both blocks are written the same way**, and this reads that way from the artifact: every
 * sentence under a name is a line of a document of this repository, carried in `data-source`,
 * while the blocks' own headings and card names are the owner's board rather than a document's
 * words. Each component holds its lines at build time through `src/lib/repo-quote.mjs`; this is
 * the same rule read off the built page, so a citation the markup dropped or pointed at the
 * wrong document is a red gate and not only a build error (Global Constraint 6).
 */
function quotedBlocksOf(html) {
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
  const doc = parse(html)
  const linked = (root, name) => [...walk(root)].filter((el) => classes(el).includes(name))
  const line = (el) => ({
    source: el.attrs.get('data-source') ?? '',
    text: decode(readableText(el)).replace(/\s+/g, ' ').trim(),
  })
  const out = { run: { sections: 0, lines: [] }, get: { sections: 0, lead: null, lines: [] } }
  for (const section of findAll(doc, (el) => el.name === 'section' && classes(el).includes('home-run'))) {
    out.run.sections += 1
    for (const one of linked(section, 'home-run-step-line')) out.run.lines.push(line(one))
  }
  for (const section of findAll(doc, (el) => el.name === 'section' && classes(el).includes('home-get'))) {
    out.get.sections += 1
    for (const one of linked(section, 'home-get-lead')) out.get.lead = line(one)
    for (const one of linked(section, 'home-get-line')) out.get.lines.push(line(one))
  }
  return out
}

/**
 * Every line of the two new blocks that is not a line of the document it cites, as the lines a
 * reader of a failure gets. **A line with no `data-source` is the first defect refused**: a
 * sentence under a name with nothing citing it is a sentence no gate holds, and it reads exactly
 * like a quotation.
 */
export function quotedBlockFailures(html) {
  const blocks = quotedBlocksOf(html)
  const out = []
  const check = (where, one) => {
    if (!one.source) {
      out.push(`${where} carries the line ${JSON.stringify(one.text)} and cites no document: every line of these two blocks is a line of this repository, and a line with no \`data-source\` is a line no gate can hold (Global Constraint 6)`)
      return
    }
    const held = quotedSection(one.source)
    if (held.text === null) {
      out.push(`${where} cites ${JSON.stringify(one.source)}, and this repository carries no such document or section: a citation naming nothing is a quotation nothing can check`)
      return
    }
    if (!normalize(held.text).includes(normalize(one.text))) {
      out.push(`${where} carries ${JSON.stringify(one.text)} and cites ${JSON.stringify(one.source)}, which does not write that line: a page renders a fact and never restates one (Global Constraint 6, D102)`)
    }
  }
  for (const one of blocks.run.lines) check('the run band', one)
  if (blocks.get.lead) check('the “what you get” grid', blocks.get.lead)
  for (const one of blocks.get.lines) check('the “what you get” grid', one)
  return out
}

test('the two blocks the bold redesign adds quote the repository, line by line', () => {
  // The acceptance a reader can check: the band's four lines and the grid's five, under cards
  // whose names are the board's, say what the repository says — and the grid's own lead line
  // with them. The count is asserted first, because a reading that found no line would pass the
  // page on lines it never read.
  const html = builtPage('')
  const blocks = quotedBlocksOf(html)
  assert.equal(
    blocks.run.sections,
    1,
    `the homepage renders ${blocks.run.sections} \`section.home-run\`, and the bold redesign's homepage carries one (the owner's board \`website/design-ref/HomeV3.dc.html\`, 2026-10-20)`,
  )
  assert.equal(
    blocks.run.lines.length,
    4,
    `the run band carries ${blocks.run.lines.length} step line(s), and the run is four steps: the four names \`RunDiagram.astro\` draws, each with the line a document writes for it`,
  )
  assert.equal(
    blocks.get.sections,
    1,
    `the homepage renders ${blocks.get.sections} \`section.home-get\`, and the bold redesign's homepage carries one (the owner's board \`website/design-ref/HomeV3.dc.html\`, 2026-10-20)`,
  )
  assert.ok(
    blocks.get.lead !== null,
    'the “what you get” grid carries no lead line, so the sentence the five cards are the detail of is not read here',
  )
  // Three since the owner's rulings of 2026-10-23 (the redesign's Step 3): the pull request, the
  // diff and the report, and the logs. The cost moved to the cost section, which quotes
  // README.md's caps lines under its own build guard, and where the agent runs to "Why Fugaro?".
  assert.equal(
    blocks.get.lines.length,
    3,
    `the “what you get” section carries ${blocks.get.lines.length} line(s), and it carries three: one per thing a run leaves behind`,
  )

  const failures = quotedBlockFailures(html)
  assert.deepEqual(
    failures,
    [],
    `a line of the bold redesign's two new blocks is not a line of the document it cites:\n  ${failures.join('\n  ')}`,
  )
})

test('the block reading refuses a line whose citation is dropped or moved, and only those', () => {
  // The defect the reading above exists for: the same markup with one card's `data-source` taken
  // off, and one band line's citation pointed at a document that does not write it. Both are
  // invisible to a reading of the text — the sentences are the repository's either way — so the
  // plants are what hold the reading to the citation rather than to the words.
  const html = builtPage('')
  const control = quotedBlockFailures(html)
  assert.deepEqual(control, [], 'the shipped page does not pass its own reading, so the plants below would prove nothing')

  const dropped = html.replace(
    '<p class="home-get-line" data-source="README.md#Safety and cost controls"',
    '<p class="home-get-line"',
  )
  assert.notEqual(dropped, html, 'the fixture did not drop a card’s citation: the page’s own markup carries none to drop')
  const moved = html.replace(
    '<p class="home-run-step-line" data-source="README.md#Why Fugaro"',
    '<p class="home-run-step-line" data-source="README.md#Getting started"',
  )
  assert.notEqual(moved, html, 'the fixture did not move a band line’s citation: the page’s own markup carries none to move')

  assert.ok(
    quotedBlockFailures(dropped).some((one) => /cites no document/.test(one)),
    'a card line with its `data-source` taken off was not refused, and a line with nothing citing it is the defect this reading exists for',
  )
  assert.ok(
    quotedBlockFailures(moved).some((one) => /which does not write that line/.test(one)),
    'a band line whose citation was moved to a section that does not write it was not refused',
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
        `the ${where} page links ${href}, which is not a route the plans' tasks write (the layout's nav and the hero's call to action included, because every link on these pages is checked here): a route outside that list is a 404 in the artifact, and it is Global Constraint 9's defect one page over from the download link it names (W1.4)`,
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

/**
 * The homepage's own controls, and what each one is, as a failure names it. **Every control the
 * page asks a reader to tap is at least 44 px tall**, and the page's own controls are the hero's
 * two links in the action row, the run band's link to the page that draws the run, and the one
 * button in the closing call to action (`HomeRunBand.astro`, `HomeInstall.astro`); the slider's
 * three dots and its two step controls are `test/slider.mjs`'s, which reads them at the widths
 * the page is read at, so this reading is about the controls outside the slider.
 */
// The hero has one action since the owner's rulings of 2026-10-23 (lever 2), and the sections
// below it carry their own links: the console and phone app links, and the cost section's button.
const CONTROL_TARGETS = [
  ['hero-cta', "the hero's filled call to action"],
  ['home-run-link', "the run band's link to How it works"],
  ['home-get-link', "the “what you get” section's links to the console and the phone app"],
  ['home-cost-button', "the cost section's button to caps and costs"],
  ['home-install-button', 'the button in the closing call to action'],
]

/**
 * Every way a homepage control misses the target size the owner's brief of 2026-10-19 measures
 * it against, as the lines a reader of a failure gets. **Every control the page asks a reader
 * to tap is at least 44 px tall**, and the page's own controls are the hero's two links in the
 * action row, the run band's link to the page that draws the run, and the one button in the
 * closing call to action (`HomeRunBand.astro`, `HomeInstall.astro`); the slider's three dots and
 * its two step controls are `test/slider.mjs`'s, which reads them at the widths the page is read
 * at, so this reading is about the controls outside the slider. The reading itself is
 * `test/lib/controls.mjs`'s —
 * one rule read in two places, since `test/caps-figures.mjs` runs it over the caps page's two
 * hero actions as well — and the list above is this page's.
 */
export { controlTargetFailures }

test('every control the homepage asks a reader to tap is at least 44 px tall', () => {
  assert.deepEqual(
    controlTargetFailures(builtCSS(builtPage('')), CONTROL_TARGETS),
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
    ['the minimum taken off the cost section’s button', withoutMinimum('home-cost-button'), /the cost section's button to caps and costs is no declared target/],
    [
      'the button’s minimum moved into a media query, where it holds at one width and not at every one',
      `${withoutMinimum('home-install-button')}@media (min-width:48rem){.home-install-button{min-height:56px}}`,
      /the button in the closing call to action is no declared target/,
    ],
    [
      'a minimum declared under the target',
      css.replace(ruleOf('hero-cta'), (rule) => rule.replace('min-height:52px', 'min-height:20px')),
      /the hero's filled call to action is a 20 px target/,
    ],
  ]
  for (const [what, planted, expected] of cases) {
    assert.notEqual(planted, css, `${what} left the stylesheet as it is, so the plant is not a plant`)
    const failures = controlTargetFailures(planted, CONTROL_TARGETS)
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

// ---- the latest releases the hero names (owner's request of 2026-10-22, PR #940, and of
// 2026-10-24 for the console) ------------------------------------------------------------

/** The newest `vX.Y.Z` in `dir` at or below `ceiling`, read here and not from the site's own
 *  modules, so the check and the page can disagree. */
function newestAtOrBelow(dir, ceiling) {
  const num = (v) => v.split('.').map(Number)
  const cmp = (a, b) => {
    for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] - b[i]
    return 0
  }
  const top = num(ceiling)
  const versions = readdirSync(path.join(REPO, dir))
    .map((name) => /^v(\d+\.\d+\.\d+)\.md$/.exec(name))
    .filter(Boolean)
    .map((m) => m[1])
    .filter((v) => cmp(num(v), top) <= 0)
    .sort((a, b) => cmp(num(b), num(a)))
  return versions[0] ?? null
}

test('the hero names the newest published CLI release and console release, each linked', () => {
  const cli = newestAtOrBelow('docs/releases', JSON.parse(repoFile('plugin/.claude-plugin/plugin.json')).version)
  const consoleVersion = newestAtOrBelow('docs/releases/console', JSON.parse(repoFile('console/package.json')).version)
  assert.ok(cli, 'no CLI release file is published in this checkout, so the line would be held to nothing')
  const html = readFileSync(path.join(SITE, 'dist', 'index.html'), 'utf8')
  const lines = findAll(parse(html), (el) => el.name === 'p' && (el.attrs.get('class') ?? '').split(/\s+/).includes('hero-release'))
  assert.equal(lines.length, 1, 'the hero carries no single latest-release line')
  const links = [...walk(lines[0])].filter((el) => el.name === 'a')
  assert.equal(textOf(links[0]).trim(), `v${cli}`, `the hero names ${textOf(links[0]).trim()} and the newest published CLI release is v${cli}`)
  assert.equal(links[0].attrs.get('href'), '/changelog/', 'the CLI release does not link the changelog')
  if (consoleVersion === null) {
    assert.equal(links.length, 1, 'the hero names a console release and none is published')
  } else {
    assert.equal(links.length, 2, 'the hero does not name the console release')
    assert.equal(textOf(links[1]).trim(), `Console ${consoleVersion}`, `the hero names ${textOf(links[1]).trim()} and the newest published console is ${consoleVersion}`)
    assert.match(links[1].attrs.get('href') ?? '', new RegExp(`/releases/tag/console-v${consoleVersion.replace(/\./g, '\\.')}$`), 'the console release does not link its own release page')
  }
  // Under the call to action (the owner's ruling of 2026-10-24), so the headline is the first
  // thing in the hero and the action comes before the note.
  const order = html.indexOf('class="hero-cta"') < html.indexOf('class="hero-release"')
  assert.ok(order, 'the latest-release line is not under the call to action')
})
