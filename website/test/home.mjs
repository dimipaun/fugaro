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
//      homepage — the two shapes it came back in), and the slot for the screenshot, which is
//      asserted to be marked and to hold
//      **the owner's real capture** (owner spec of 2026-10-18, item 5; PR #622) — the slot's
//      own hook, a non-empty `alt`, the capture's declared dimensions and a same-origin path,
//      so the page carries the wordmark and the working terminal and nothing else.
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
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { cssDeclarations, cssRules, findAll, filesUnder, parse, textOf, walk } from './lib/html.mjs'
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
 * tab label `slider.mjs` pairs with it), so the two halves of the rename cannot drift apart.
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
 *     2026-10-20, and the same string the slider's tab carries;
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
        `${where}: its icon is not marked out of the tab order (focusable="false"), which is the other half of the decoration contract this site already writes on a drawing — \`a11y.mjs\` holds the slider's step icons to the same pair — and an icon is decoration on both (the owner's feedback of 2026-10-19)`,
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
function benefitsPage(items, { title = BENEFITS_TITLE, source = BENEFITS_SOURCE, sections = 1 } = {}) {
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
  // and a picture moved into the "Why Fugaro?" panel is a picture under a heading about something
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
    ['HomeSlider.astro'],
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

test("the run is How It Works' own picture of one, and the homepage carries the slider instead", () => {
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
  // tabs, three panels, all of them in the HTML a reader receives, each tab a link to the panel
  // it shows. A homepage whose tabs pointed at nothing would be a homepage whose blocks a reader
  // without JavaScript cannot reach — which is what the flow's own gate said about the figure
  // when it asked a page to render exactly one of them.
  const tabs = findAll(home, (el) => el.name === 'a' && classes(el).includes('tab')).map((el) => el.attrs.get('href') ?? '')
  const panels = findAll(home, (el) => classes(el).includes('panel')).map((el) => el.attrs.get('id') ?? '')
  assert.equal(
    tabs.length,
    3,
    `the homepage's slider offers ${tabs.length} tabs, and the draft's first panel is the three steps of using Fugaro beside a Benefits tab and a See it tab (the owner's approved draft of 2026-10-19; changes spec 2026-10-18, item 4)`,
  )
  assert.equal(
    panels.length,
    3,
    `the homepage's slider carries ${panels.length} panels, and every tab it offers is a link to a panel: a tab that points at nothing shows nothing (the owner's approved draft of 2026-10-19)`,
  )
  assert.deepEqual(
    tabs,
    panels.map((id) => `#${id}`),
    `the homepage's tabs point at ${JSON.stringify(tabs)} and its panels are ${JSON.stringify(panels.map((id) => `#${id}`))}: each tab is the link to the panel in its place, so the block is reachable without the script (the owner's approved draft of 2026-10-19)`,
  )

  // **And the picture is item 4's four steps, drawn**: the drawing's boxes, its labels, the run
  // in words under it, and the section the words are the detail of.
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
      assert.ok(
        !flat.includes(`${property}:`),
        `the ${where} page declares \`${property}\`, and the page is not revealed by scrolling: the flow's scroll-driven animation is retired with the flow (the owner's ruling of 2026-10-20; W1.4 step 3, D105)`,
      )
    }
    assert.ok(
      !/@keyframes/.test(flat),
      `the ${where} page declares a \`@keyframes\` animation, and neither thing that replaced the flow animates: the slider's three panels are the fragment's and the drawing is a still picture (the owner's ruling of 2026-10-20; W1.4 step 3, D105)`,
    )
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
    pageMotionFailures(`${guard}.step-card{animation:reveal 2s} .step-card:hover{transition:none}`).length === 0,
    'a neutralised animation and a disabled transition are both refused by the motion check, and neither is motion a reader is given',
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
  ['home-run-link', "the run band's link to How it works"],
  ['home-install-button', 'the button in the closing call to action'],
]

/**
 * Every way a homepage control misses the target size the owner's brief of 2026-10-19
 * measures it against, as the lines a reader of a failure gets. **Every control the page asks
 * a reader to tap is at least 44 px tall**, and the page's own controls are the hero's two
 * links in the action row, the run band's link to the page that draws the run, and the one
 * button in the closing call to action (`HomeRunBand.astro`, `HomeInstall.astro`); the
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
      /the button in the closing call to action is no declared target/,
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
