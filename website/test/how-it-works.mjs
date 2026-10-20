// website/test/how-it-works.mjs — How It Works is one diagram of a run, with arrows, and every
// label of it is a line of a document of this repository (the owner's draft "How it works,
// draft 2 (diagram)", approved on 2026-10-19 with "build it").
//
// **What the page is.** One inline-SVG picture — your agent, Fugaro, an ephemeral cloud job
// working through the run's stages, and the pull request that opens as a draft — and, under it,
// the same facts as a short text list for a reader who skips the figure, plus the page's
// remaining facts in a second list. No JavaScript draws any of it: the picture is markup and
// the words under it are markup, and nothing on the page measures or moves either.
//
// **What this gate holds, and why each half is here rather than left to the eye.**
//
//   - **The picture exists, and a screen reader is told what it is.** `/how-it-works/` renders
//     one `<figure class="run-diagram">` holding SVG, every drawing in it is `role="img"` with
//     a non-empty `aria-label`, and no other page carries the figure. A picture whose name went
//     missing is a picture a screen reader announces as nothing, and `a11y.mjs`'s general rule
//     (an `<svg role="img">` needs a name) is the reason this half is a name rather than the
//     whole of the check: what this file adds is that the name is the *run's*, and that the page
//     renders the figure at all.
//   - **The stages are the run's own.** The drawing's `<text class="run-stage">` elements, in
//     order, are `bootstrap`, `implement`, `verify`, `review`, `writeback`, and the loop drawn
//     back round is `fix`. The list is pinned here, and each name is held to the document that
//     names it as a stage — `docs/design/v1.md` §4's chain for four of them,
//     `plugin/skills/working/reference/logs.md`'s list of the stages a run reaches for
//     `verify` — so a stage renamed in either document fails here naming the file rather than
//     leaving a page that draws a stage the run does not have. A stage added, dropped,
//     renamed or reordered is refused: the order is the run's, and the picture is a claim
//     about what happens between asking for the work and getting the pull request.
//   - **Every other label is the repository's own words.** Each labelled group of the drawing
//     carries the document it is quoted from, and its text — the lines of the drawing joined
//     back into a sentence — has to be a line of that document, read out of the repository with
//     markdown markers, whitespace and the apostrophe's two glyphs normalized. This is
//     `RunDiagram.astro`'s own build-time check read at the artifact, where a page can be
//     looked at without the component that wrote it: a paraphrase that reached the built page
//     stops here even if someone relaxed the build. The list under the figure is held the same
//     way, because it is the same claim for a reader who skips the picture.
//   - **The two drawings are one picture.** The wide drawing and the tall one carry the same
//     labels, from the same documents: a phone and a desktop are told the same thing, and the
//     second viewBox is a second scale rather than a second version of the run.
//   - **The words under the figure carry the picture's facts**, in the picture's order, with
//     the box names the drawing gives them: a reader who skips the figure is told everything it
//     says, and a fact cannot be dropped from the list while the picture keeps it.
//   - **The drawing scales to a phone, and never scrolls sideways.** Every drawing carries a
//     `viewBox` and the stylesheet gives it `width: 100%` of the column and a `max-width` of
//     the same, so a drawing is scaled rather than clipped; no rule of the diagram's declares a
//     width a 360 px phone cannot fit, or a `white-space: nowrap` that cannot break; and
//     exactly one of the two drawings is displayed at a time, at one width the stylesheet
//     declares, so the run is drawn twice and read once. The width they change over at is read
//     out of the stylesheet and the box each drawing is scaled into out of the page's own
//     gutter and ceiling, because what the changeover buys is a label size: the wide drawing's
//     smallest label is 16 units of a 1200-unit picture, so the drawing is shown where its
//     column puts that label at 12 px or more, and the tall drawing — drawn at a phone's own
//     width, with a ceiling of its own scale — meets the phone's column instead. A number typed
//     in here would keep agreeing with itself after the page's gutter moved.
//   - **The loop's arrow is drawn, and it joins the stages the run repeats.** The draft draws
//     `[fix ─► review(2)]`: the arrow leaves `review`, runs up beside the pills and lands back
//     on `implement`. So the pill the arrow's two ends land on is read from the stage words the
//     drawing itself pairs with its pills, the line has to reach those two pills, the head that
//     lands it has to be drawn at the end of the line that lands on `implement`, and no part of
//     the arrow may be drawn across a pill. An arrow deleted from the drawing, or redrawn
//     between two other stages, is a run the page does not describe.
//
// Standard library only; no network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { DIST, REPO, builtPages, cssDeclarations, cssRules, findAll, parse, textOf, walk } from './lib/html.mjs'

// ---- the repository's own words ---------------------------------------------------------

/** The bytes of a file in the checkout. */
function repoFile(relative) {
  return readFileSync(path.join(REPO, relative), 'utf8')
}

/**
 * A label and a line of a document, compared the way `RunDiagram.astro` compares them: markdown
 * markers, whitespace and the apostrophe's two glyphs out of the way, so a page may set a
 * document's straight apostrophe in the site's own type without the label stopping being the
 * document's line.
 */
function comparable(text) {
  return text.replace(/[`*]/g, '').replace(/[‘’]/g, "'").replace(/\s+/g, '')
}

/** One section of a document, up to the next heading of the same level or a shallower one. */
function section(document, heading) {
  const text = repoFile(document)
  const at = [...text.matchAll(/^(#+) (.+)$/gm)].find((match) => match[2].replace(/`/g, '') === heading)
  assert.ok(at && at.index !== undefined, `${document} carries no "${heading}" section, and a label of the diagram is quoted from it`)
  const level = at[1].length
  const rest = text.slice(at.index)
  const next = [...rest.matchAll(/^(#+) /gm)].find((match) => match.index !== undefined && match.index > 0 && match[1].length <= level)
  return next && next.index !== undefined ? rest.slice(0, next.index) : rest
}

/**
 * The documents the page's labels cite, by the name each group carries, read the way a reader
 * of the repository would: README.md's own sections, `docs/gcp-setup.md`'s budget and checkpoint
 * sections, the `parallelism` skill, the design document and the changes spec.
 */
function sources() {
  return {
    'README.md#How it works': section('README.md', 'How it works'),
    'README.md#Why Fugaro': section('README.md', 'Why Fugaro'),
    'README.md#Safety and cost controls': section('README.md', 'Safety and cost controls'),
    'README.md#Getting started': section('README.md', 'Getting started'),
    'docs/gcp-setup.md#Turning the model budget on': section('docs/gcp-setup.md', 'Turning the model budget on'),
    'docs/gcp-setup.md#Run, watch, diagnose': section('docs/gcp-setup.md', 'Run, watch, diagnose'),
    'docs/design/v1.md': repoFile('docs/design/v1.md'),
    'plugin/skills/parallelism/SKILL.md': repoFile('plugin/skills/parallelism/SKILL.md'),
    'docs/website/redesign/changes-spec-2026-10-18.md': repoFile('docs/website/redesign/changes-spec-2026-10-18.md'),
  }
}

// ---- the run's stages --------------------------------------------------------------------

/**
 * The stages the picture draws, in the order it draws them, and the loop: `docs/design/v1.md`
 * §4's chain (`bootstrap ─► implement ─► review(1) ─► [fix ─► review(2)] … ─► finalize ─►
 * writeback`) is where the order comes from, `verify` is the stage
 * `plugin/skills/working/reference/logs.md` lists among the ones a run reaches, and `fix` is
 * the loop the chain draws back round into `review`.
 *
 * `finalize` is not drawn: the owner's draft of 2026-10-19 names the five stages above, and the
 * picture is the run as a reader meets it — the pushing and the PR's opening are what a run
 * leaves behind rather than a box of their own.
 */
const STAGES = ['bootstrap', 'implement', 'verify', 'review', 'writeback']
const LOOP = 'fix'

/**
 * The chain `docs/design/v1.md` draws the run with, as the line of that document, and the
 * stages it names, in the order it names them. The document's own drawing is the reading: a
 * chain that stopped being one line, or that stopped naming a stage, is this failure rather
 * than a stage the page may draw.
 */
function chain() {
  const line = repoFile('docs/design/v1.md').split('\n').find((text) => /^bootstrap\b/.test(text) && text.includes('─►'))
  assert.ok(
    line,
    'docs/design/v1.md no longer draws the run\'s stage chain on one line beginning "bootstrap ─►", and the diagram\'s stages are that chain\'s (docs/design/v1.md §4)',
  )
  // First occurrence only: the chain writes `review` twice — `review(1)` and, after the loop,
  // `review(2)` — and a stage is one stage however many rounds of it the chain draws.
  return { line, names: [...new Set([...line.matchAll(/[a-z_]+/g)].map((match) => match[0]))] }
}

/** The stages `plugin/skills/working/reference/logs.md` says a run reaches, as that document lists them. */
function loggedStages() {
  const logs = repoFile('plugin/skills/working/reference/logs.md')
  const line = logs.split('\n').find((text) => /stages reached, in order/.test(text))
  assert.ok(
    line,
    'plugin/skills/working/reference/logs.md no longer lists the stages a run reaches in order, and that list is where `verify` is a stage name rather than the wrapper\'s name (plugin/skills/working/reference/logs.md)',
  )
  return line
}

/** Whether a stage is one a document of this repository names as a stage. */
function namedStage(name) {
  const { line, names } = chain()
  if (names.includes(name)) return true
  return new RegExp(`\`${name}\``).test(loggedStages())
}

// ---- the page ----------------------------------------------------------------------------

/** A page's own stylesheet: its inline `<style>` blocks, and any first-party sheet it links. */
function pageCss(html, dist = DIST) {
  const inline = [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((match) => match[1])
  const linked = [...html.matchAll(/<link[^>]*rel="stylesheet"[^>]*href="(\/[^"]+)"/g)].map((match) =>
    readFileSync(path.join(dist, match[1]), 'utf8'),
  )
  return [...inline, ...linked].join('\n')
}

const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
const has = (el, name) => classes(el).includes(name)

/** The lines of a drawing's text node, joined back into the sentence the drawing writes. */
function labelText(group) {
  const rows = [...walk(group)].filter((el) => el.name === 'text').map((el) => textOf(el).trim())
  return rows.join(' ').replace(/\s+/g, ' ').trim()
}

/**
 * What a run of text takes at a size, and above what it takes in any font the page can be read
 * in. The drawing's labels are set in the reader's own sans (`--font-sans`), whose stack lands
 * on a wide face on more systems than not — DejaVu Sans on most Linux desktops is the widest
 * of them, and about a tenth wider per character than Arial or Inter. The component lays the
 * drawing out with the same estimate and refuses an over-wide line at build time; this is the
 * same measurement taken again over the built page, where the box a line was drawn in is the
 * `data-pad` the card declares rather than a number from the component's own source.
 */
const CHAR = 0.62
const textWidth = (text, size) => [...text].length * CHAR * size

/**
 * The box a `<path>` is drawn in, from its own `d`: the points of the polyline the diagram
 * draws with (`M`, `H`, `V` and `L`), which is all the loop and its head are made of. A
 * command this does not know is a path whose extent this check cannot read, and the caller
 * refuses that rather than passing it.
 */
function pathBox(d) {
  const points = []
  let x = 0
  let y = 0
  let known = true
  for (const [, command, rest] of d.matchAll(/([A-Za-z])([^A-Za-z]*)/g)) {
    const numbers = (rest.match(/-?\d+(?:\.\d+)?/g) ?? []).map(Number)
    if (command === 'M' || command === 'L') [x, y] = [numbers[0], numbers[1]]
    else if (command === 'H') x = numbers[0]
    else if (command === 'V') y = numbers[0]
    else if (command !== 'Z') known = false
    if (command !== 'Z') points.push([x, y])
  }
  if (!known || points.length === 0) return null
  const xs = points.map((point) => point[0])
  const ys = points.map((point) => point[1])
  return { x0: Math.min(...xs), x1: Math.max(...xs), y0: Math.min(...ys), y1: Math.max(...ys) }
}

/** The box a text node is drawn in: its own size, and the baseline it sits on. */
function textBox(text) {
  const size = Number(text.size)
  const width = textWidth(text.text, size)
  const x0 = text.anchor === 'middle' ? text.x - width / 2 : text.anchor === 'end' ? text.x - width : text.x
  return { x0, x1: x0 + width, y0: text.y - size * 0.8, y1: text.y + size * 0.25, width }
}

const overlaps = (a, b) => a.x0 < b.x1 - 0.5 && a.x1 > b.x0 + 0.5 && a.y0 < b.y1 - 0.5 && a.y1 > b.y0 + 0.5

/**
 * Whether a stage's own label is drawn inside a pill: the sides, and the line's own cap
 * height — a descender dipping a hair under the pill's edge is how the type sits in every
 * pill of the drawing, and what is read here is whether the word is drawn across the pill
 * rather than in it.
 */
const inPill = (drawn, text, pill) =>
  drawn.x0 >= pill.x0 - 0.5 && drawn.x1 <= pill.x1 + 0.5 && text.y - text.size * 0.8 >= pill.y0 - 1 && text.y <= pill.y1 + 0.5

/**
 * The pill each stage's own label is drawn in, paired by the drawing's own geometry — a stage
 * word inside a pill is that pill's stage. One predicate, read twice: by the check that every
 * stage word of the drawing has a pill of its own, and by the reading of which two pills the
 * loop's arrow joins, so the two cannot disagree about where a stage is drawn (which is what
 * makes the loop's ends a fact about the stages rather than about two numbers).
 */
function stagePills(box) {
  const pairs = []
  for (const text of box.texts) {
    if (!text.stage || !text.klass.includes('run-stage')) continue
    if (![text.x, text.y, text.size].every(Number.isFinite)) continue
    const drawn = textBox(text)
    const pill = box.pills.find((candidate) => inPill(drawn, text, candidate))
    if (pill) pairs.push({ stage: text.stage, pill })
  }
  return pairs
}

/**
 * A page's run diagram, as the elements it renders: the drawings, each drawing's labelled
 * groups and stage names, the picture's name, and the words under it. `figure` is 0 or 1.
 */
export function runDiagramOf(html) {
  const doc = parse(html)
  const figures = findAll(doc, (el) => el.name === 'figure' && has(el, 'run-diagram'))
  const out = { figures: figures.length, drawings: [], words: { facts: [], also: [] } }
  for (const figure of figures) {
    for (const svg of walk(figure)) {
      if (svg.name !== 'svg') continue
      const labels = []
      for (const group of walk(svg)) {
        if (group.name === 'g' && has(group, 'run-label')) {
          labels.push({ source: group.attrs.get('data-source') ?? '', text: labelText(group) })
        }
      }
      out.drawings.push({
        wide: has(svg, 'run-wide'),
        tall: has(svg, 'run-tall'),
        role: svg.attrs.get('role') ?? '',
        name: svg.attrs.get('aria-label') ?? '',
        // The reader lowercases an attribute's name, as a browser does (`viewBox` is written
        // in camel case in SVG's own grammar and matched case-insensitively in HTML).
        viewBox: svg.attrs.get('viewbox') ?? '',
        // The stage names the drawing writes into its pills, in the order the drawing puts
        // them, and the `fix` the loop's own label carries: the run's stages are the picture's
        // skeleton, and they are read out of the markup a reader's screen reader would read.
        stages: [...walk(svg)].filter((el) => has(el, 'run-stage')).map((el) => el.attrs.get('data-stage') ?? textOf(el).trim()),
        loop: [...walk(svg)].filter((el) => has(el, 'run-loop')).map((el) => textOf(el).trim()),
        labels,
        sizes: [...walk(svg)]
          .filter((el) => el.name === 'text')
          .map((el) => Number(el.attrs.get('font-size')))
          .filter((size) => Number.isFinite(size)),
        // Every box of the drawing — the four cards and the band — as the geometry a line is
        // drawn in: the card's own width, the inset its contents are drawn at, the text nodes
        // inside it, the stage pills, and the loop's own paths.
        boxes: [...findAll(svg, (el) => has(el, 'run-box') || has(el, 'run-band-box'))].map((box) => {
          const rect = [...walk(box)].find((el) => el.name === 'rect' && (has(el, 'run-card') || has(el, 'run-band')))
          const body = [...walk(box)].find((el) => el.name === 'g' && (has(el, 'run-box-body') || has(el, 'run-band-body')))
          if (!rect || !body) return { name: box.attrs.get('data-box') ?? 'the band', broken: true }
          return {
            name: box.attrs.get('data-box') ? `box ${box.attrs.get('data-box')}` : 'the band',
            broken: false,
            width: Number(rect.attrs.get('width')),
            pad: Number(rect.attrs.get('data-pad')),
            texts: [...walk(body)]
              .filter((el) => el.name === 'text')
              .map((el) => ({
                klass: classes(el),
                text: textOf(el).trim(),
                // The stage a stage's label names: the pill it sits in is read from this,
                // which is how the loop's arrow is held to the two stages it joins.
                stage: el.attrs.get('data-stage') ?? null,
                x: Number(el.attrs.get('x')),
                y: Number(el.attrs.get('y')),
                size: Number(el.attrs.get('font-size')),
                anchor: el.attrs.get('text-anchor') ?? 'start',
              })),
            pills: [...walk(body)]
              .filter((el) => el.name === 'rect' && has(el, 'run-stage-pill'))
              .map((el) => ({
                x0: Number(el.attrs.get('x')),
                x1: Number(el.attrs.get('x')) + Number(el.attrs.get('width')),
                y0: Number(el.attrs.get('y')),
                y1: Number(el.attrs.get('y')) + Number(el.attrs.get('height')),
              })),
            // The loop's own paths — the line, and the head that lands it — each with the
            // class it is drawn under, so the arrow the draft asks for can be held as the
            // arrow it is: it has to be drawn, and it has to join the two stages.
            loop: [...walk(body)]
              .filter((el) => el.name === 'path' && classes(el).some((name) => name.startsWith('run-loop')))
              .map((el) => ({
                klass: classes(el).find((name) => name.startsWith('run-loop')) ?? '',
                path: pathBox(el.attrs.get('d') ?? ''),
              })),
          }
        }),
      })
    }
  }
  const words = findAll(doc, (el) => el.name === 'section' && has(el, 'run-words'))[0]
  if (words) {
    for (const el of walk(words)) {
      if (el.name === 'li' && has(el, 'run-fact')) {
        out.words.facts.push({
          name: [...walk(el)].filter((child) => has(child, 'run-fact-name')).map((child) => textOf(child).trim())[0] ?? '',
          labels: [...walk(el)]
            .filter((child) => has(child, 'run-fact-text'))
            .map((child) => ({ source: child.attrs.get('data-source') ?? '', text: textOf(child).trim().replace(/\s+/g, ' ') })),
        })
      }
      if (el.name === 'li' && has(el, 'run-also-item')) {
        out.words.also.push({ source: el.attrs.get('data-source') ?? '', text: textOf(el).trim().replace(/\s+/g, ' ') })
      }
    }
  }
  return out
}

/** The diagram's rule for a class, as the built stylesheet declares it, last one winning. */
function declaredFor(css, klass, property) {
  let found
  for (const rule of cssRules(css)) {
    if (!new RegExp(`\\.${klass}(?![\\w-])`).test(rule.selector ?? '')) continue
    for (const [name, value] of cssDeclarations(rule.body ?? '')) if (name === property) found = { value, query: (rule.conditions ?? []).join(' ') }
  }
  return found
}

/** Every declaration of the diagram's classes, as `{ selector, conditions, property, value }`. */
function diagramDeclarations(css) {
  const out = []
  for (const rule of cssRules(css)) {
    const selector = rule.selector ?? ''
    if (!/\.run-[\w-]+/.test(selector)) continue
    for (const [property, value] of cssDeclarations(rule.body ?? '')) {
      out.push({ selector, conditions: (rule.conditions ?? []).join(' '), property, value })
    }
  }
  return out
}

/**
 * The width a media query's own condition names, in pixels — `(width<=47.99rem)` and
 * `(width>=48rem)` are what the built stylesheet writes, and the source forms it is built
 * from (`(max-width: 47.99rem)`) read the same — or `null` for a query that is about
 * something else (`prefers-reduced-motion`, a hover query). A query naming two widths is
 * read as the last of them, which is the one the rule's own reading turns on.
 */
function queryWidth(conditions) {
  let found = null
  for (const [, minimum, maximum, kind, value, unit] of (conditions ?? '').matchAll(
    /(min-)?(max-)?width\s*(:|<=|>=|<|>)\s*([\d.]+)(rem|px)/g,
  )) {
    const at = Number(value) * (unit === 'rem' ? 16 : 1)
    if (kind !== ':') found = at
    else if (maximum) found = at
    else if (minimum) found = at
    else found = null
  }
  return found
}

/** Whether a rule's own conditions hold at a viewport: the widths a query names, and nothing
 * else. A rule inside a query this check cannot read — a print sheet, a hover query, a
 * `supports` block — is not the rule a reader at this viewport gets. */
function appliesAt(conditions, viewport) {
  for (const condition of conditions ?? []) {
    const widths = [...condition.matchAll(/(min-)?(max-)?width\s*(:|<=|>=|<|>)\s*([\d.]+)(rem|px)/g)]
    if (widths.length === 0) return false
    for (const [, minimum, maximum, kind, value, unit] of widths) {
      const at = Number(value) * (unit === 'rem' ? 16 : 1)
      if (kind === '>=' || (kind === ':' && minimum)) {
        if (!(viewport >= at)) return false
      } else if (kind === '<=' || (kind === ':' && maximum)) {
        if (!(viewport <= at)) return false
      } else if (kind === '>') {
        if (!(viewport > at)) return false
      } else if (kind === '<') {
        if (!(viewport < at)) return false
      } else return false
    }
  }
  return true
}

/**
 * `main`'s inline padding at a viewport, read out of the page's own stylesheet: the gutter
 * the drawings are scaled inside. The page's own two rules are the reading — the base
 * `padding: var(--space-lg) var(--space-sm) var(--space-2xl)` and the wider viewport's
 * `padding: var(--space-xl) var(--space-md) var(--space-3xl)` — so a token that moves moves
 * every number below with it rather than leaving this check measuring a box the page is no
 * longer drawn in. `null` when no rule of the page declares a padding this check can turn
 * into pixels, which the callers refuse rather than guess at.
 */
function mainGutter(css, viewport) {
  let gutter = null
  for (const rule of cssRules(css)) {
    if ((rule.selector ?? '').trim() !== 'main') continue
    const padding = [...cssDeclarations(rule.body ?? '')].find(([property]) => property === 'padding')
    if (!padding) continue
    if (!appliesAt(rule.conditions ?? [], viewport)) continue
    // The shorthand's inline value: `padding: 0` is none, and a two- or three-value padding
    // writes it second.
    const parts = padding[1].trim().split(/\s+/)
    const px = lengthInPx(parts.length === 1 ? parts[0] : parts[1], css)
    if (Number.isNaN(px)) return null
    gutter = px
  }
  return gutter
}

/**
 * The width of the column `main` holds at a viewport: the page's `--page-max` ceiling, less
 * the gutter on both sides. This is the box a drawing is scaled into, and every readability
 * number below is taken at a viewport this way rather than from a width written here.
 * `null` when the stylesheet does not say.
 */
function columnAt(css, viewport) {
  const gutter = mainGutter(css, viewport)
  const pageMax = lengthInPx('var(--page-max)', css)
  if (gutter === null || Number.isNaN(pageMax)) return null
  return Math.min(viewport, pageMax) - 2 * gutter
}

/**
 * Every way a built page's diagram falls short of the picture the owner's draft of 2026-10-19
 * asks for, as the lines a reader of the failure gets. Pure, over the page's markup and its
 * stylesheet, so the built page is checked with it and the refusals below are proved with it.
 */
export function runDiagramFailures(html, css, docs = sources()) {
  const out = []
  const diagram = runDiagramOf(html)
  if (diagram.figures !== 1) {
    return [`the page renders ${diagram.figures} run diagrams, and it renders one: the page is one picture of a run, with the run in words under it (the owner's draft of 2026-10-19)`]
  }
  if (diagram.drawings.length === 0) {
    return ['the page\'s run diagram holds no drawing, and the picture is inline SVG in the page rather than a file it fetches (the owner\'s draft of 2026-10-19)']
  }

  // The name, on every drawing: a `<svg role="img">` with no name is art a screen reader
  // announces as nothing, and the two drawings are one picture, so they carry one name.
  for (const drawing of diagram.drawings) {
    const where = `the ${drawing.wide ? 'wide' : drawing.tall ? 'tall' : '(unclassed)'} drawing`
    if (drawing.role !== 'img') {
      out.push(`${where} is \`role="${drawing.role}"\`, and the drawing is a picture the page's text list stands for: a reader without it is given a name, which is what \`role="img"\` with an \`aria-label\` is for (W1.11)`)
    }
    if (!drawing.name.trim()) {
      out.push(`${where} carries no \`aria-label\`, and a picture with no name is a picture a screen reader announces as nothing (W1.11)`)
    }
  }
  if (diagram.drawings.length > 1 && new Set(diagram.drawings.map((d) => d.name)).size !== 1) {
    out.push('the page\'s drawings carry different accessible names, and they are one picture at two scales: a reader told a different run depending on the width is a reader told two things')
  }

  // The stages, in the order the drawing puts them, held to the documents that name them.
  for (const drawing of diagram.drawings) {
    const where = `the ${drawing.wide ? 'wide' : 'tall'} drawing`
    if (drawing.stages.join(' | ') !== STAGES.join(' | ')) {
      out.push(
        `${where} draws the stages ${JSON.stringify(drawing.stages)}, and the run's stages are ${JSON.stringify(STAGES)}, in that order (docs/design/v1.md §4; plugin/skills/working/reference/logs.md): a stage added, dropped, renamed or reordered is a claim about how a run works`,
      )
    }
    for (const stage of drawing.stages) {
      if (!namedStage(stage)) {
        out.push(`${where} draws a stage called ${JSON.stringify(stage)}, and no document of this repository names it as a stage of a run (docs/design/v1.md §4; plugin/skills/working/reference/logs.md)`)
      }
    }
    if (drawing.loop.join(' | ') !== LOOP) {
      out.push(`the ${where}'s loop is labelled ${JSON.stringify(drawing.loop)}, and the loop the run takes back round is ${JSON.stringify(LOOP)} (docs/design/v1.md §4)`)
    }
    if (!namedStage(LOOP)) {
      out.push(`the loop's own label ${JSON.stringify(LOOP)} is not a stage of the run's chain in docs/design/v1.md, and the arrow is that chain's own loop`)
    }
  }

  // The stages' own order in the chain the design document draws: `verify` is not in that
  // chain's boxes but is one of the stages a run reaches, so the four the chain names have to
  // keep the chain's order — a picture that ran `review` before `implement` is refused.
  const { names } = chain()
  const drawn = diagram.drawings[0]?.stages ?? []
  const inChain = drawn.filter((stage) => names.includes(stage))
  const chainOrder = [...names].filter((name) => inChain.includes(name))
  if (inChain.length > 1 && inChain.join(' | ') !== chainOrder.join(' | ')) {
    out.push(
      `the drawing runs ${JSON.stringify(inChain)} and docs/design/v1.md's chain runs ${JSON.stringify(chainOrder)}: the picture is the run that document describes, in that document's order (docs/design/v1.md §4)`,
    )
  }

  // Every other label, held to the document it cites. The text list under the figure is held
  // the same way, because it says the same thing for a reader who skips the picture.
  const held = (label, where) => {
    const doc = docs[label.source]
    if (doc === undefined) {
      out.push(`${where} attributes its text to ${JSON.stringify(label.source)}, and a label is quoted from a document of this repository (Global Constraint 6)`)
      return
    }
    if (!comparable(doc).includes(comparable(label.text))) {
      out.push(`${where} reads ${JSON.stringify(label.text)}, which is not a line of ${label.source}: every label on this page is the repository's own words rather than a sentence written for the page (Global Constraint 6, D102)`)
    }
  }
  for (const drawing of diagram.drawings) {
    const where = `the ${drawing.wide ? 'wide' : 'tall'} drawing`
    for (const label of drawing.labels) held(label, `${where}'s label`)
    if (drawing.labels.length === 0) {
      out.push(`${where} carries no labelled text, and the picture is its labels: a drawing whose words moved out of it says nothing (the owner's draft of 2026-10-19)`)
    }
  }
  for (const fact of diagram.words.facts) for (const label of fact.labels) held(label, `the ${JSON.stringify(fact.name)} fact`)
  for (const item of diagram.words.also) held(item, 'a line of the list under the picture')

  // One picture at two scales: the same labels, from the same documents, in the same order.
  if (diagram.drawings.length > 1) {
    const say = (drawing) => drawing.labels.map((label) => `${label.source}\u0000${comparable(label.text)}`).join('\u0001')
    if (different(diagram.drawings.map(say))) {
      out.push('the page\'s drawings carry different labels, and they are one picture at two scales: the phone and the desktop are told the same run, or the second viewBox is a second drawing')
    }
  }

  // The words under the figure carry the picture's facts, in the picture's order, with the box
  // names the drawing gives them: a reader who skips the figure is told everything it says.
  const bands = diagram.words.facts
  const drawn650 = diagram.drawings[0] ?? { labels: [] }
  const drawingLabels = drawn650.labels.map((label) => comparable(label.text)).join('|')
  const listLabels = bands.flatMap((fact) => fact.labels).map((label) => comparable(label.text)).join('|')
  if (listLabels !== drawingLabels) {
    out.push(
      `the list under the picture reads ${JSON.stringify(listLabels.split('|'))} where the picture's labels are ${JSON.stringify(drawingLabels.split('|'))}: the list is the same facts for a reader who skips the figure, in the picture's own order (the owner's draft of 2026-10-19)`,
    )
  }
  for (const fact of bands) {
    if (!fact.name.trim()) out.push('a fact in the list under the picture names no box, and the list is the picture in words: every fact says what it is about')
  }
  if (diagram.words.also.length === 0) {
    out.push('the page carries no second list, and the facts the picture does not carry — how a run starts, when the draft appears, what a run syncs, what outlives the container, where the boundary is — are what that list is for (the owner\'s draft of 2026-10-19)')
  }

  // The drawings scale to the column and never scroll sideways.
  for (const drawing of diagram.drawings) {
    const box = drawing.viewBox.trim().split(/\s+/).map(Number)
    if (box.length !== 4 || box.some((value) => !Number.isFinite(value))) {
      out.push(`a drawing declares \`viewBox="${drawing.viewBox}"\`, and a picture that scales carries the four numbers of its own coordinate system: without them an SVG is 300 by 150 px and a phone scrolls sideways for it (the owner's draft of 2026-10-19)`)
    }
  }
  const width = declaredFor(css, 'run-svg', 'width')
  const maxWidth = declaredFor(css, 'run-svg', 'max-width')
  if (!width || !/^100%$/.test(width.value.trim())) {
    out.push(`\`.run-svg\` is \`width: ${width?.value ?? '(nothing declared)'}\`, and the drawing is as wide as the column it is drawn in: \`width: 100%\` is what scales it, and a fixed width is a phone scrolling sideways (the owner's draft of 2026-10-19)`)
  }
  if (!maxWidth || !/^100%$/.test(maxWidth.value.trim())) {
    out.push(`\`.run-svg\` is \`max-width: ${maxWidth?.value ?? '(nothing declared)'}\`, and the drawing never grows past the column: \`max-width: 100%\` is the ceiling that keeps it there (the owner's draft of 2026-10-19)`)
  }
  // The column a 360 px phone gives the drawings — out of the page's own stylesheet, so that
  // a gutter that moves moves this with it — which is the box every width below is held to.
  const phoneColumn = columnAt(css, PHONE_WIDTH)
  for (const declaration of diagramDeclarations(css)) {
    if (!['width', 'min-width'].includes(declaration.property)) continue
    const px = lengthInPx(declaration.value, css)
    if (declaration.property === 'width' && /^\d+(\.\d+)?%$/.test(declaration.value.trim())) continue
    if (Number.isNaN(px)) {
      out.push(`\`${declaration.selector}\` is \`${declaration.property}: ${declaration.value}\`, which this check cannot turn into pixels, and a width it cannot read is one nothing checked (the owner's draft of 2026-10-19)`)
    } else if (phoneColumn !== null && px > phoneColumn) {
      out.push(`\`${declaration.selector}\` is \`${declaration.property}: ${declaration.value}\` — ${Math.round(px)} px, and a ${PHONE_WIDTH} px phone's column is ${Math.round(phoneColumn)} px: a box wider than the phone is a page the phone scrolls sideways (the owner's draft of 2026-10-19)`)
    }
    if (declaration.property === 'min-width' && declaration.value.trim() !== '0') {
      out.push(`\`${declaration.selector}\` is \`min-width: ${declaration.value}\`, and a floor under the width of a block of the run is a block a phone cannot fit (the owner's draft of 2026-10-19)`)
    }
  }
  for (const declaration of diagramDeclarations(css)) {
    if (declaration.property !== 'white-space') continue
    if (declaration.value.trim() === 'nowrap') {
      out.push(`\`${declaration.selector}\` sets \`white-space: nowrap\`, and a label that cannot break is a label that runs past the page on a phone (the owner's draft of 2026-10-19)`)
    }
  }

  // Every line of the drawing, measured against the box it is drawn in, and the loop's arrow
  // against the pills it passes: the drawing is laid out for a font wider than the one the
  // component writes it in, and a line that outgrows its card is a line over the card's border
  // and into the gap beside it — a browser's own reading of a label the draft has no room for.
  // The box is the card's own geometry: its width, and the inset `data-pad` its contents are
  // drawn at, both read off the built page.
  for (const drawing of diagram.drawings) {
    const where = `the ${drawing.wide ? 'wide' : 'tall'} drawing`
    for (const box of drawing.boxes) {
      if (box.broken) {
        out.push(`${where}'s ${box.name} is drawn without the card its contents sit on and the inset they are drawn at, and a line of the picture is held to the box it is drawn in (the owner's draft of 2026-10-19)`)
        continue
      }
      const left = box.pad
      const right = box.width - box.pad
      for (const text of box.texts) {
        if (![text.x, text.y, text.size].every(Number.isFinite)) {
          out.push(`${where}'s ${box.name} draws ${JSON.stringify(text.text)} without the position or the size it is drawn at, and a line this check cannot measure is a line nothing holds inside its box (the owner's draft of 2026-10-19)`)
          continue
        }
        const drawn = textBox(text)
        if (drawn.x0 < left - 0.5 || drawn.x1 > right + 0.5) {
          out.push(`${where}'s ${box.name} draws ${JSON.stringify(text.text)} from ${drawn.x0.toFixed(1)} to ${drawn.x1.toFixed(1)} units, and the box's content runs ${left} to ${right}: the drawing is laid out for a font the page's stack resolves to on a reader's system (DejaVu Sans and the other wide sans faces are wider per character than Arial or Inter), and a line that fits neither is a line over the card's border (the owner's draft of 2026-10-19)`)
        }
        // A stage's own label belongs to its pill: the pill is the stage, and a word drawn
        // wider than it is a word sitting on two pills at once.
        if (text.klass.includes('run-stage')) {
          const inside = box.pills.some((pill) => inPill(drawn, text, pill))
          if (!inside) {
            out.push(`${where}'s ${box.name} draws the stage ${JSON.stringify(text.text)} from ${drawn.x0.toFixed(1)} to ${drawn.x1.toFixed(1)} units, and no pill of the drawing holds it: a stage is its pill, and a word wider than the pill it sits in is a word over the pill beside it (the owner's draft of 2026-10-19)`)
          }
          continue
        }
        for (const pill of box.pills) {
          if (overlaps(drawn, pill)) {
            out.push(`${where}'s ${box.name} draws ${JSON.stringify(text.text)} over the stage pill at ${pill.x0.toFixed(1)} to ${pill.x1.toFixed(1)} units: the labels of a box are drawn in its content box and the stages have their own, so a word over a pill is a word the stage's own label is read with (the owner's draft of 2026-10-19)`)
          }
        }
      }
      // The loop the draft draws: an arrow that leaves `review`, runs up beside the stages and
      // lands back on `implement` — the `fix` round `docs/design/v1.md` §4 draws. Three
      // readings, because the arrow is a claim about the run rather than decoration: it has
      // to be *drawn* (the line, and the head that lands it — an arrow deleted from the
      // drawing leaves a run with no fix round in it), it has to join `review` and
      // `implement` rather than two other stages, and it has to be drawn beside the pills
      // rather than across them (a line struck through the two stages the round trip is
      // between).
      if (box.texts.some((text) => text.stage)) {
        const lines = box.loop.filter((path) => path.klass === 'run-loop-line')
        const heads = box.loop.filter((path) => path.klass === 'run-loop-head')
        if (lines.length !== 1 || heads.length !== 1) {
          out.push(`${where}'s ${box.name} draws the loop with ${lines.length} \`run-loop-line\` and ${heads.length} \`run-loop-head\` paths, and the fix round is one arrow: a line that leaves \`review\` and the head that lands it back on \`implement\` (docs/design/v1.md §4; the owner's draft of 2026-10-19)`)
        } else if (!lines[0].path || !heads[0].path) {
          out.push(`${where}'s ${box.name} draws the loop with a path this check cannot read, and an arrow nothing can measure is an arrow nothing holds beside the pills (the owner's draft of 2026-10-19)`)
        } else {
          const staged = stagePills(box)
          const review = staged.find((pair) => pair.stage === 'review')?.pill
          const implement = staged.find((pair) => pair.stage === 'implement')?.pill
          const from = lines[0].path
          const tip = heads[0].path
          const reaches = (y, pill) => pill !== undefined && y >= pill.y0 - 0.5 && y <= pill.y1 + 0.5
          const span = (pill) => (pill ? `${pill.y0.toFixed(1)} to ${pill.y1.toFixed(1)} units` : 'no pill this check can find')
          const joins =
            (reaches(from.y0, review) && reaches(from.y1, implement)) || (reaches(from.y1, review) && reaches(from.y0, implement))
          if (!joins) {
            out.push(`${where}'s ${box.name} draws the loop's arrow from y ${from.y0.toFixed(1)} to y ${from.y1.toFixed(1)}, and the round trip runs from the \`review\` pill (${span(review)}) back round to \`implement\`'s (${span(implement)}): the pills are where the run's stages are drawn, so an arrow between two other stages is a fix round the run does not take (docs/design/v1.md §4; the owner's draft of 2026-10-19)`)
          }
          const landed = (tip.y0 + tip.y1) / 2
          if (!reaches(landed, implement) || tip.x0 < from.x0 - 0.5 || tip.x1 > from.x1 + 0.5) {
            out.push(`${where}'s ${box.name} draws the arrow's head from x ${tip.x0.toFixed(1)} to x ${tip.x1.toFixed(1)} at y ${landed.toFixed(1)}, and the head of the round trip sits at the end of its line that lands on \`implement\` — the line runs x ${from.x0.toFixed(1)} to x ${from.x1.toFixed(1)}, y ${from.y0.toFixed(1)} to y ${from.y1.toFixed(1)} —: an arrowhead is where the arrow points, and a head drawn anywhere else points at something the round trip does not do (the owner's draft of 2026-10-19)`)
          }
        }
        for (const path of box.loop) {
          if (!path.path) continue
          for (const pill of box.pills) {
            if (overlaps(path.path, pill)) {
              out.push(`${where}'s ${box.name} draws the loop's arrow across ${path.path.x0.toFixed(1)} to ${path.path.x1.toFixed(1)} units and the stage pill at ${pill.x0.toFixed(1)} to ${pill.x1.toFixed(1)} is drawn over it: the loop is drawn beside the stages it sends the run back through, and an arrow through a pill reads as a line through that stage rather than the round trip (the owner's draft of 2026-10-19)`)
            }
          }
        }
      }
    }
  }

  // The two drawings are two scales of one picture, and the label a reader has to be able to
  // read is the smallest one — every number below is taken at a width the page's own
  // stylesheet declares rather than at one written here: the gutter out of `main`'s padding,
  // the ceiling out of `--page-max`, and the width the drawings change over at out of the
  // stylesheet's own query. A check that typed the box in would keep agreeing with itself
  // after the page's gutter moved.
  //
  //   - The **tall** drawing meets a phone: it is drawn at a phone's own width, so its
  //     smallest label scales by about one into a phone's column, and it carries a
  //     `max-width` of its own scale so that the tablet it is also shown on gets the phone's
  //     picture rather than that picture stretched.
  //   - The **wide** drawing meets the narrowest column it is shown in — the column at the
  //     width the drawings change over at, which is the whole reason that width is 59.25 rem
  //     and not the site's 48 rem chrome breakpoint: at a 768 px viewport the column scales
  //     the wide drawing's 16 unit labels to 9.6 px, a label a reader cannot read.
  const shown = (klass) => {
    const rules = diagramDeclarations(css).filter(
      (declaration) => declaration.property === 'display' && new RegExp(`\\.${klass}(?![\\w-])`).test(declaration.selector),
    )
    return {
      plain: rules.filter((rule) => !rule.conditions).map((rule) => rule.value),
      switched: rules
        .filter((rule) => rule.conditions)
        .map((rule) => ({ value: rule.value, at: queryWidth(rule.conditions), query: rule.conditions })),
    }
  }
  const tallShown = shown('run-tall')
  const wideShown = shown('run-wide')
  const widths = [...new Set([...tallShown.switched, ...wideShown.switched].map((rule) => rule.at))]
  const switchAt = widths.length === 1 ? widths[0] : null
  if (!tallShown.plain.includes('none')) {
    out.push(`the tall drawing is \`display: ${tallShown.plain.join(', ') || '(nothing declared)'}\` above the width the two drawings change over at, and only one of the two drawings is shown at a time: the run is drawn twice and read once (the owner's draft of 2026-10-19)`)
  }
  if (switchAt === null) {
    out.push(`the two drawings change over at ${widths.length === 0 ? 'no width this check can read' : `${widths.length} different widths (${widths.map((at) => (at === null ? 'one it cannot read' : `${at} px`)).join(', ')})`}: one drawing is shown at a time, so the page's stylesheet declares the single width that swaps them, and the whole reason that width is where it is is the label size below (the owner's draft of 2026-10-19)`)
  } else {
    if (!tallShown.switched.some((rule) => rule.value === 'block' && rule.at === switchAt)) {
      out.push(`the tall drawing is \`display: ${tallShown.switched.map((rule) => rule.value).join(', ') || '(nothing declared)'}\` inside a media query, and a phone is met with the stacked run rather than the shrunk wide one: the query at ${switchAt} px is the one that shows it (the owner's draft of 2026-10-19)`)
    }
    if (!wideShown.switched.some((rule) => rule.value === 'none' && rule.at === switchAt)) {
      out.push(`the wide drawing is \`display: ${wideShown.switched.map((rule) => rule.value).join(', ') || '(nothing declared)'}\` inside a media query, and the wide drawing is turned off at the same width the tall one is turned on at: two drawings on a page is the run drawn twice and read twice (the owner's draft of 2026-10-19)`)
    }
  }
  const switchColumn = switchAt === null ? null : columnAt(css, switchAt)
  if (switchColumn === null || phoneColumn === null) {
    out.push(`the column the drawings are scaled into cannot be read out of the page's own stylesheet (${switchColumn === null ? 'the width they change over at, ' : ''}${phoneColumn === null ? "`main`'s own inline padding or the page's ceiling" : ''}), and the box a drawing is drawn in is what every readability number here is taken at: a check that wrote the box down instead would keep measuring a page the reader no longer has (the owner's draft of 2026-10-19)`)
  }

  // The readable floor: the smallest label of each drawing, at the narrowest column it is
  // shown in, is at least 12 px.
  const floor = 12
  const scaledInto = (drawing, column) => {
    const box = drawing.viewBox.trim().split(/\s+/).map(Number)
    const smallest = Math.min(...drawing.sizes)
    if (!Number.isFinite(box[2]) || box[2] <= 0 || !Number.isFinite(smallest) || column === null) return null
    return { smallest, units: box[2], px: (smallest * column) / box[2] }
  }
  const wide = diagram.drawings.find((drawing) => drawing.wide)
  if (wide && switchAt !== null) {
    const read = scaledInto(wide, switchColumn)
    if (read && !(read.px >= floor)) {
      out.push(`the wide drawing's smallest label is ${read.smallest} units, which is ${read.px.toFixed(1)} px in the ${Math.round(switchColumn)} px column the page gives the drawing at ${switchAt} px — the narrowest viewport it is shown at: a label under ${floor} px is a label a reader cannot read, and the wide drawing is shown above the width its own labels fit at or it is shown to a reader who cannot read it (the owner's draft of 2026-10-19)`)
    }
  }
  const tall = diagram.drawings.find((drawing) => drawing.tall)
  if (!tall) {
    out.push('the page carries no tall drawing, and a phone meets the run stacked rather than shrunk: the wide drawing\'s labels are a third of the size they are drawn at on a 360 px screen (the owner\'s draft of 2026-10-19)')
  } else {
    const box = tall.viewBox.trim().split(/\s+/).map(Number)
    if (Number.isFinite(box[2]) && box[2] > PHONE_WIDTH) {
      out.push(`the tall drawing is drawn ${box[2]} units wide and a phone's viewport is ${PHONE_WIDTH} px: the second viewBox is the run drawn at a phone's own width, so its labels scale by about one and stay readable (the owner's draft of 2026-10-19)`)
    }
    // Its own scale, as the stylesheet declares it: the ceiling the stacked drawing is shown
    // under, so a tablet is shown the phone's picture rather than a stretched one. Unreadable
    // (or absent) is refused rather than defaulted: a ceiling nothing states is a ceiling a
    // tablet does not have.
    const cap = declaredFor(css, 'run-tall', 'max-width')
    const capPx = cap ? lengthInPx(cap.value, css) : NaN
    const fit = (column) => (column === null ? null : Number.isNaN(capPx) ? column : Math.min(column, capPx))
    if (Number.isNaN(capPx)) {
      out.push(`the tall drawing declares \`max-width: ${cap?.value ?? '(nothing)'}\`, which this check cannot turn into pixels${cap ? '' : ', and it declares no ceiling at all'}: the stacked drawing is the run at a phone's own scale, and a ceiling nothing states leaves the tablet it is also shown on with a picture stretched to the column (the owner's draft of 2026-10-19)`)
    }
    const read = scaledInto(tall, fit(phoneColumn))
    if (read && !(read.px >= floor)) {
      out.push(`the tall drawing's smallest label is ${read.smallest} units, which is ${read.px.toFixed(1)} px in the ${Math.round(fit(phoneColumn) ?? 0)} px box a ${PHONE_WIDTH} px phone gives it (the page's own column, capped by the drawing's own ceiling): a label under ${floor} px is a label a reader on a phone cannot read, and the stacked drawing is there so that they can (the owner's draft of 2026-10-19)`)
    }
    // And the ceiling itself: at the widest viewport the tall drawing is shown at — the width
    // the drawings change over at — it is not drawn larger than its own viewBox. A ceiling this
    // check cannot read is refused above rather than read as one that is not there.
    if (!Number.isNaN(capPx) && Number.isFinite(box[2]) && box[2] > 0 && switchAt !== null) {
      const widest = fit(switchColumn)
      if (widest !== null && widest > box[2] + 0.5) {
        out.push(`the tall drawing is shown ${Math.round(widest)} px wide at ${switchAt} px — the widest viewport it is shown at, the narrowest the wide one is — and it is drawn ${box[2]} units wide: the stacked drawing is the run at a phone's own scale, and a tablet is shown the phone's picture rather than that picture stretched (the owner's draft of 2026-10-19)`)
      }
    }
  }
  return out
}

/** True when the strings are not all the same. */
function different(values) {
  return new Set(values).size > 1
}

/**
 * The phone the drawings are held to: the width the tall drawing is drawn at, and the
 * viewport whose column every phone number below is taken at. It is the one number written
 * here rather than read out of the page — the box that column holds is `columnAt`'s, out of
 * `main`'s own padding, so a gutter that moves moves the box with it.
 */
const PHONE_WIDTH = 360

/** A length in pixels, for the tokens this page's stylesheet declares. */
function lengthInPx(value, css) {
  const text = value.trim()
  if (/^0(px|rem|em)?$/.test(text)) return 0
  const tokens = new Map()
  for (const match of css.matchAll(/--([a-z0-9-]+):\s*([\d.]+)(rem|px)\b/g)) tokens.set(match[1], Number(match[2]) * (match[3] === 'rem' ? 16 : 1))
  const variable = /^var\(--([a-z0-9-]+)\)$/.exec(text)
  if (variable) return tokens.get(variable[1]) ?? NaN
  const rem = /^([\d.]+)rem$/.exec(text)
  if (rem) return Number(rem[1]) * 16
  const px = /^([\d.]+)px$/.exec(text)
  if (px) return Number(px[1])
  return NaN
}

// ---- the gate ----------------------------------------------------------------------------

test('How It Works is one diagram of the run, named for a screen reader and quoted from the repository', () => {
  const pages = builtPages()
  const diagrammed = pages.filter((page) => page.html.includes('run-diagram'))
  assert.deepEqual(
    diagrammed.map((page) => page.rel),
    [path.join('how-it-works', 'index.html')],
    'the run diagram is not on How It Works and only there: the owner\'s draft of 2026-10-19 puts it on that page, and the homepage keeps its own four-step flow (website/test/home.mjs)',
  )
  for (const page of diagrammed) {
    const failures = runDiagramFailures(page.html, pageCss(page.html))
    assert.deepEqual(failures, [], `${page.rel}: the run diagram is not the one the owner's draft of 2026-10-19 asks for:\n  ${failures.join('\n  ')}`)
  }
})

test('every run-diagram refusal fires on the defect it names', (t) => {
  // The gate's own proof: the page as it is built passes, and each fixture that breaks one
  // thing in it makes exactly that thing's line appear. Without these cases a gate that read
  // no drawing at all would pass, which is the failure mode a page this new has.
  const page = readFileSync(path.join(DIST, 'how-it-works', 'index.html'), 'utf8')
  const css = pageCss(page)
  const docs = sources()
  const verdict = (html, stylesheet = css) => runDiagramFailures(html, stylesheet, docs)
  assert.deepEqual(verdict(page), [], 'the built page does not pass the gate its own fixtures are drawn from')

  // The pills of the wide drawing, exchanged whole: the first of the two named stages takes
  // the other's place and the other takes its.
  const swapPills = (html, one, other) => {
    const pill = (name) => new RegExp(`<text class="run-stage" data-stage="${name}"[^>]*>${name}</text>`)
    const first = html.match(pill(one))
    const second = html.match(pill(other))
    assert.ok(first && second, `the built page carries no \`${one}\` and \`${other}\` stage pills, and this fixture swaps them`)
    return html.replace(first[0], '\u0000').replace(second[0], first[0]).replace('\u0000', second[0])
  }

  // A stage pill copied: as the drawing draws it, at the next stage's own place and size,
  // renamed — a stage that was not there before and is drawn exactly as the others are.
  const cloneStage = (html, from, to) => {
    const pill = new RegExp(`<text class="run-stage" data-stage="${from}"[^>]*>${from}</text>`).exec(html)
    assert.ok(pill, `the built page draws no \`${from}\` stage pill, and this fixture copies it`)
    const copy = pill[0].replace(`data-stage="${from}"`, `data-stage="${to}"`).replace(`>${from}<`, `>${to}<`)
    return html.replace(pill[0], pill[0] + copy)
  }

  // A path of the loop moved sideways, whole: every x of its `d` by the same amount, so the
  // fixture moves the arrow rather than redrawing it. The loop's `d` is written as pairs of
  // numbers per command (`M x y H x V y H x`, and the head's `M x y L x y L x y Z`), so the
  // even ones are the x's.
  const shiftX = (html, klass, by) => {
    const path = new RegExp(`(<path class="${klass}" d=")([^"]*)"`).exec(html)
    assert.ok(path, `the built page draws no \`${klass}\` path, and this fixture moves it`)
    let index = 0
    const moved = path[2].replace(/-?\d+(?:\.\d+)?/g, (number) => (index++ % 2 === 0 ? (Number(number) + by).toFixed(1) : number))
    return html.replace(path[0], `${path[1]}${moved}"`)
  }

  // The loop redrawn between two other stages, in the lane it keeps: the arrow's two ends
  // moved up one stage row, so the round trip leaves `verify` and lands on `bootstrap` — a fix
  // round the run does not take. The row is the drawing's own: every pill is drawn six units
  // shorter than the row it sits in, which is the arithmetic the component draws its stages
  // with. Nothing else moves: the line stays in its lane, clear of the pills, and the head
  // stays on the end of the line that lands on `implement`, so the one thing this fixture
  // breaks is which two stages the arrow joins.
  const reroute = (html) => {
    const pill = /<rect class="run-stage-pill"[^>]*height="([\d.]+)"/.exec(html)
    assert.ok(pill, 'the built page draws no stage pill, and this fixture moves the loop a stage row at a time')
    const step = Number(pill[1]) + 6
    const line = /(class="run-loop-line" d="M ([\d.]+) ([\d.]+) H ([\d.]+) V ([\d.]+) H )([\d.]+)"/.exec(html)
    assert.ok(line, 'the built page draws no loop line in the shape this fixture redraws, and the fixture is written to that shape')
    return html.replace(line[0], `${line[1]}${line[2]} ${(Number(line[3]) - step).toFixed(1)} H ${line[4]} V ${(Number(line[5]) - step).toFixed(1)} H ${line[6]}"`)
  }

  const cases = [
    ['a page with no diagram', page.replace(/<figure class="run-diagram"[\s\S]*?<\/figure>/, ''), /renders 0 run diagrams/],
    [
      'a diagram holding no drawing',
      page.replace(/<svg class="run-svg run-wide"[\s\S]*?<\/svg>/, '<span>a run, described</span>').replace(/<svg class="run-svg run-tall"[\s\S]*?<\/svg>/, ''),
      /holds no drawing/,
    ],
    [
      'a drawing with no name',
      page.replace(/(class="run-svg run-wide" viewBox="[^"]*" role="img" aria-label=")[^"]*"/, '$1"'),
      /carries no `aria-label`|different accessible names/,
    ],
    ['a drawing that is not a picture', page.replace(/class="run-svg run-wide" viewBox="([^"]*)" role="img"/, 'class="run-svg run-wide" viewBox="$1" role="presentation"'), /is `role="presentation"`/],
    ['a drawing with no viewBox', page.replace(/class="run-svg run-wide" viewBox="[^"]*"/, 'class="run-svg run-wide"'), /picture that scales carries the four numbers/],
    // The stages: a stage dropped from the drawing, one added, two swapped, and one the
    // repository does not name.
    [
      'a stage dropped from the drawing',
      page.replace(/<text class="run-stage" data-stage="verify"[^>]*>verify<\/text>/, ''),
      /draws the stages .*and the run's stages are/,
    ],
    [
      'a stage added to the drawing',
      // A sixth stage drawn where a real one is, so the one thing this fixture breaks is the
      // list of stages rather than the geometry they are drawn with.
      cloneStage(page, 'implement', 'deploy'),
      /draws the stages .*and the run's stages are|names it as a stage of a run/,
    ],
    [
      'two stages swapped',
      // The two pills exchanged in the wide drawing, whole: a swap of the labels alone would
      // not be a swap, since a stage is read by the attribute the drawing gives it and the
      // `data-stage` hook is what the gate holds to the repository.
      swapPills(page, 'verify', 'review'),
      /draws the stages .*and the run's stages are/,
    ],
    ['a loop labelled something else', page.replace('class="run-text run-loop"', 'class="run-text run-loop-x"'), /loop is labelled \[\]/],
    // The geometry a browser shows rather than a preference: a line set larger than the box it
    // was laid out in — which is what a wider system font does to a drawing laid out for a
    // narrower one — and the loop drawn through the pills it is supposed to pass beside.
    [
      'a name drawn wider than its card',
      page.replace(/(class="run-name"[^>]*font-size=")[\d.]+"/, '$160"'),
      /draws "Your agent" from .* and the box's content runs/,
    ],
    [
      'a stage label wider than its pill',
      page.replace(/(class="run-stage" data-stage="verify"[^>]*font-size=")[\d.]+"/, '$160"'),
      /no pill of the drawing holds it|and the box's content runs/,
    ],
    [
      'a loop drawn through the stage pills',
      // The arrow left where it is drawn and moved back over the pills: the width the pills
      // took before the loop had a lane of its own, which is the defect a reader sees as a
      // line struck through `implement` and `review`. Two readings are true of it and both are
      // expected: the line is drawn over three pills, and the head it no longer reaches — the
      // fixture moves the line and leaves the arrowhead where it was drawn — no longer sits at
      // the end of the line that lands on `implement`.
      shiftX(page, 'run-loop-line', -40),
      /draws the loop's arrow across .* and the stage pill|draws the arrow's head from/,
    ],
    // The arrow itself, read the way the draft writes it (`[fix ─► review(2)]`): it is drawn,
    // at all, and it joins the two stages the round trip is between. Each fixture breaks one of
    // those and nothing else, so the refusal that fires is the one the fixture is about.
    [
      'a drawing with no loop at all',
      // The whole arrow gone from the wide drawing: the line and the head that lands it, which
      // is a run with no fix round in it however the pills are drawn.
      page.replace(/<path class="run-loop-line"[^>]*\/>/, '').replace(/<path class="run-loop-head"[^>]*\/>/, ''),
      /draws the loop with 0 `run-loop-line` and 0 `run-loop-head` paths/,
    ],
    [
      'a loop with no arrowhead',
      page.replace(/<path class="run-loop-head"[^>]*\/>/, ''),
      /draws the loop with 1 `run-loop-line` and 0 `run-loop-head` paths/,
    ],
    [
      'a loop that joins two other stages',
      reroute(page),
      /draws the loop's arrow from y .* and the round trip runs from the `review` pill/,
    ],
    [
      'an arrowhead somewhere else',
      shiftX(page, 'run-loop-head', 40),
      /draws the arrow's head from x .* and the head of the round trip sits at the end of its line/,
    ],
    // A label written for the page rather than quoted: the sentence a reader reads has to be
    // the document's own.
    [
      'a label the document does not write',
      // The page turned over to a sentence of its own: both drawings and the list under them,
      // so the one thing this fixture breaks is where the words came from.
      page
        .replaceAll('Fan out these three</text>', 'Fan out these two</text>')
        .replaceAll('Fan out these three bug fixes.', 'Fan out these two bug fixes.'),
      /is not a line of docs\/website\/redesign\/changes-spec-2026-10-18\.md/,
    ],
    [
      'a label attributed to a document that does not carry it',
      // The words left alone and the paper they are credited to changed: a label has to be the
      // document's own words and the document it names.
      page.replace(
        'class="run-fact-text" data-source="docs/website/redesign/changes-spec-2026-10-18.md"',
        'class="run-fact-text" data-source="docs/design/v1.md"',
      ),
      /is not a line of docs\/design\/v1\.md/,
    ],
    [
      'a label citing a file that is not there',
      page.replace('data-source="plugin/skills/parallelism/SKILL.md"', 'data-source="docs/parallelism.md"'),
      // The second line is the drawing's own: one drawing crediting a missing file no longer
      // matches the other, which is a true reading of the broken page.
      /is quoted from a document of this repository|carry different labels/,
    ],
    // The list under the picture: a fact dropped from it, and one reordered.
    [
      'a fact dropped from the list',
      page.replace(/<li class="run-fact"[^>]*><strong class="run-fact-name"[^>]*>Pull request<\/strong>[\s\S]*?<\/li>/, ''),
      /the list under the picture reads/,
    ],
    // A second drawing that says something else: the phone is told a different run.
    [
      'a second drawing that says something else',
      page.replace(/(class="run-svg run-tall"[\s\S]*?aria-label=")[^"]*"/, '$1A different run"'),
      /carry different labels|different accessible names/,
    ],
  ]
  // The page as it is built, with one of the diagram's own rules rewritten: the fixtures the
  // markup cannot reach are declarations, and Astro scopes a component's rule with its own
  // `[data-astro-cid-…]`, so the rule is found rather than spelled out here.
  const ruleText = (klass) => {
    const match = new RegExp(`(?:^|[},])\\s*(\\.${klass}(?:\\[[^\\]]*\\])?\\{([^}]*)\\})`).exec(css)
    assert.ok(match, `the built stylesheet carries no \`${klass}\` rule, and the cases below rewrite it`)
    return [match[1], match[2]]
  }
  const rewrite = (klass, from, to) => {
    const [text, body] = ruleText(klass)
    assert.ok(body.includes(from), `\`${klass}\` no longer declares ${JSON.stringify(from)}, and this fixture rewrites it`)
    return css.replace(text, text.replace(from, to))
  }
  /** A declaration added to a rule's own body: a rule found by name, and the page's own text. */
  const declare = (klass, declaration) => {
    const [text] = ruleText(klass)
    // The rule's body may or may not end in a `;`, so the new declaration is separated from it.
    return css.replace(text, text.replace('}', `;${declaration};}`))
  }
  /** A fragment of the built stylesheet replaced, when it is the only place that fragment is. */
  const replaceOnce = (from, to) => {
    assert.equal(css.split(from).length - 1, 1, `this fixture replaces ${JSON.stringify(from)}, and the built stylesheet carries it ${css.split(from).length - 1} times rather than once`)
    return css.replace(from, to)
  }
  const cssCases = [
    ['a drawing with a fixed width', rewrite('run-svg', 'width:100%', 'width:40rem'), /is `width: 40rem`|box wider than the phone/],
    ['a drawing with no width', rewrite('run-svg', 'width:100%;', ''), /`\.run-svg` is `width: \(nothing declared\)`/],
    ['a drawing with no ceiling', rewrite('run-svg', 'max-width:100%;', ''), /`max-width: \(nothing declared\)`/],
    ['a label that cannot break', declare('run-cap', 'white-space:nowrap'), /white-space: nowrap/],
    // The drawing a phone is shown, which is the one the media query turns on: the fragment is
    // the phone block's own tail, so the fixture rewrites the rule in the query rather than the
    // base rule above it.
    ['a tall drawing that is not the one a phone shows', replaceOnce('display:block}}', 'display:none}}'), /a phone is met with the stacked run|shows exactly one drawing/],
    ['a tall drawing shown above the changeover', rewrite('run-tall', 'display:none', 'display:block'), /above the width the two drawings change over at/],
    // The width the two drawings change over at, and the reason it is not the site's chrome
    // breakpoint: moved back to 48 rem, the wide drawing is shown from a 768 px viewport up,
    // where its own 16 unit labels come out at 9.8 px — the finding this check was written for.
    [
      'the drawings changing over at the site chrome breakpoint',
      replaceOnce('(width<=59.25rem)', '(width<=47.99rem)'),
      /wide drawing's smallest label is .* px in the .* px column the page gives the drawing/,
    ],
    // The stacked drawing's ceiling: a tablet is shown the phone's picture rather than that
    // picture stretched, and a ceiling nothing states is refused rather than read as none.
    [
      'a tall drawing stretched to the column',
      rewrite('run-tall', 'max-width:22.5rem', 'max-width:56.25rem'),
      /the tall drawing is shown .* px wide at .* and it is drawn .* units wide/,
    ],
    [
      'a tall drawing with no ceiling of its own',
      rewrite('run-tall', 'max-width:22.5rem;', ''),
      /declares `max-width: \(nothing\)`.*declares no ceiling at all/,
    ],
  ]
  for (const [what, stylesheet, expected] of [...cases, ...cssCases]) {
    const broken = typeof stylesheet === 'string' && stylesheet.startsWith('<!DOCTYPE')
    const lines = broken ? verdict(stylesheet) : verdict(page, stylesheet)
    // Every line has to be the refusal the case names: a fixture that breaks one rule and a
    // gate that refuses it for another reason is a case that proves nothing.
    assert.deepEqual(
      [...new Set(lines.map((line) => (expected.test(line) ? 'fired' : line)))],
      ['fired'],
      `${what}: the refusal did not fire, and this is what it said instead:\n${lines.join('\n')}`,
    )
  }

  // And the repository's own half: a stage name no document carries is refused as a stage of
  // the run, wherever the drawing got it from.
  assert.ok(
    !namedStage('deploy') && namedStage('verify') && namedStage('fix') && namedStage('writeback'),
    'the stage names the repository carries are not read as they are: `verify` and `fix` are stages of the run and `deploy` is not, and this is the reading the gate holds the drawing to',
  )
})

// ---- the pins, re-derived here -----------------------------------------------------------

test('the stages the page draws are the stages the repository names', () => {
  // The list `STAGES` is the pin, and this is what keeps it from being a list kept here and
  // nowhere else: the chain `docs/design/v1.md` §4 draws names four of the five, in the order
  // the picture draws them, and `plugin/skills/working/reference/logs.md` names the fifth.
  const { line, names } = chain()
  for (const stage of STAGES.filter((name) => name !== 'verify')) {
    assert.ok(names.includes(stage), `the run's stage ${JSON.stringify(stage)} is not in docs/design/v1.md's chain (${JSON.stringify(line)}), and the diagram draws it (docs/design/v1.md §4)`)
  }
  const drawnOrder = STAGES.filter((name) => names.includes(name))
  const chainOrder = [...names].filter((name) => drawnOrder.includes(name))
  assert.deepEqual(drawnOrder, chainOrder, 'the diagram draws the run\'s stages in an order the design document\'s chain does not: the picture is the run that document describes (docs/design/v1.md §4)')
  assert.ok(names.includes(LOOP), `the loop the diagram draws is not a stage of the chain docs/design/v1.md draws (${JSON.stringify(line)})`)
  assert.ok(new RegExp('`verify`').test(loggedStages()), 'plugin/skills/working/reference/logs.md no longer names `verify` among the stages a run reaches, and that list is where the page\'s fifth stage comes from')
})
