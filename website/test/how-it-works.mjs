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
//     exactly one of the two drawings is displayed at a time, below one width the stylesheet
//     declares, so the run is drawn twice and read once. The width they change over at is read
//     out of the stylesheet and the box each drawing is scaled into out of the page's own
//     gutter and ceiling, because what the changeover buys is a label size: the wide drawing's
//     smallest label is 16 units of a 1200-unit picture, so the drawing is shown where its
//     column puts that label at 12 px or more, and the tall drawing — drawn at a phone's own
//     width, with a ceiling of its own scale — meets the phone's column instead. Which *side*
//     of that width each rule holds on is read too, since the same width read from the other
//     side shows the wide drawing to the phone it cannot be read on. A number typed in here
//     would keep agreeing with itself after the page's gutter moved.
//   - **The loop's arrow is drawn, and it joins the stages the run repeats.** The draft draws
//     `[fix ─► review(2)]`: the arrow leaves `review`, runs up beside the pills and lands back
//     on `implement`. So the pill the arrow's two ends land on is read from the stage words the
//     drawing itself pairs with its pills, the line has to reach those two pills, the head that
//     lands it has to be drawn at the end of the line that lands on `implement` — and with its
//     point ahead of its base along that line, since a head is where the arrow points and a
//     head turned round lands on the right pill pointing the wrong way — and no part of the
//     arrow may be drawn across a pill. An arrow deleted from the drawing, or redrawn between
//     two other stages, is a run the page does not describe.
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
 * The points of the polyline a `<path>` draws with (`M`, `H`, `V` and `L`), which is all the
 * loop's line and its head are made of. A command this does not know is a path this check
 * cannot read, and the caller refuses that rather than passing it — the head below is read
 * corner by corner, so a path whose shape cannot be turned into corners is not a head.
 */
function pathPoints(d) {
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
  return known && points.length > 0 ? points : null
}

/** The box those points are drawn in. */
function pathBox(d) {
  const points = pathPoints(d)
  if (points === null) return null
  const xs = points.map((point) => point[0])
  const ys = points.map((point) => point[1])
  return { x0: Math.min(...xs), x1: Math.max(...xs), y0: Math.min(...ys), y1: Math.max(...ys) }
}

/**
 * Which way an arrow's head points, read from the line it is drawn on and the head's own
 * corners. The direction is the one the line arrives in, taken from its last two points; the
 * two corners of a triangle that sit at the same place along that direction are the base the
 * head is drawn from, and the third is its point. The point has to be *ahead* of the base on
 * the way in: a head whose point trails its base is drawn backwards, pointing at the stage the
 * arrow came from rather than the one it lands on. The head's box cannot tell these apart —
 * the mirrored head is the same triangle over the same two x's — so the reading is the
 * corners, not the extent. `null` is a head this cannot read: fewer or more than three corners,
 * a line of no length, or corners that do not give it one base.
 */
function headLeadsLine(line, head) {
  if (line === null || head === null || line.length < 2 || head.length !== 3) return null
  const [from, to] = line.slice(-2)
  const length = Math.hypot(to[0] - from[0], to[1] - from[1])
  if (length === 0) return null
  const dx = (to[0] - from[0]) / length
  const dy = (to[1] - from[1]) / length
  const along = head.map(([x, y]) => x * dx + y * dy)
  const bases = [[0, 1], [0, 2], [1, 2]].filter(([i, j]) => Math.abs(along[i] - along[j]) < 0.5)
  if (bases.length !== 1) return null
  const [base] = bases
  const apex = [0, 1, 2].find((index) => !base.includes(index))
  return {
    line: { from, to },
    apex: head[apex],
    base: base.map((index) => head[index]),
    leads: along[apex] - (along[base[0]] + along[base[1]]) / 2 > 0.5,
  }
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
                points: pathPoints(el.attrs.get('d') ?? ''),
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

/**
 * Which side of the width a media query's condition is on: `'below'` for a rule that holds at
 * the widths *up to* the one it names (`max-width: 59.25rem`, built as `(width<=59.25rem)`),
 * `'above'` for one that holds from that width up (`min-width`), and `null` for a query that
 * names no width, or names two that disagree about their side. A width read from the wrong
 * side is the same width, so the width alone cannot say which drawing a reader gets: the
 * stacked run below 59.25 rem and the stacked run *above* it are two different pages.
 */
function querySide(conditions) {
  let side = null
  for (const [, minimum, maximum, kind] of (conditions ?? '').matchAll(
    /(min-)?(max-)?width\s*(:|<=|>=|<|>)\s*([\d.]+)(rem|px)/g,
  )) {
    const here = kind === '>=' || kind === '>' || (kind === ':' && minimum) ? 'above' : kind === '<=' || kind === '<' || (kind === ':' && maximum) ? 'below' : null
    if (here === null || (side !== null && side !== here)) return null
    side = here
  }
  return side
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
      // lands back on `implement` — the `fix` round `docs/design/v1.md` §4 draws. Four
      // readings, because the arrow is a claim about the run rather than decoration: it has
      // to be *drawn* (the line, and the head that lands it — an arrow deleted from the
      // drawing leaves a run with no fix round in it), it has to join `review` and
      // `implement` rather than two other stages, its head has to be where the arrow points
      // (the one end of the line that lands on `implement`, and turned the way the line runs),
      // and it has to be drawn beside the pills rather than across them (a line struck through
      // the two stages the round trip is between).
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
          // And which way it points, which is not where it sits: the head is a triangle, its
          // three corners say which way round it was drawn, and the two x's a mirrored head is
          // drawn over are the ones the correct head is drawn over too.
          const head = headLeadsLine(lines[0].points, heads[0].points)
          if (head === null) {
            out.push(`${where}'s ${box.name} draws the loop's arrowhead as a path this check cannot read as a triangle (${heads[0].points?.length ?? 0} corners), and an arrowhead is the three corners of a triangle — one point, on a base — or it is not an arrow that says which stage the round trip lands on (the owner's draft of 2026-10-19)`)
          } else if (!head.leads) {
            out.push(`${where}'s ${box.name} draws the loop's arrowhead backwards: its point is at x ${head.apex[0].toFixed(1)}, its base at x ${head.base.map(([x]) => x.toFixed(1)).join(' and ')}, and the line arrives at x ${head.line.to[0].toFixed(1)} from x ${head.line.from[0].toFixed(1)} — the point trails the base the way the line runs, so the head points back at the stage the run came from rather than at the one it lands on: an arrowhead is where the arrow points (the owner's draft of 2026-10-19)`)
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
  //
  // A rule's *width* is not enough to say what a reader at a viewport is shown: `(width<=59.25rem)`
  // and `(width>=59.25rem)` name the same width and show the drawings at opposite ends of it.
  // So each switched rule carries which side of its width it holds on, and the changeover the
  // two `some` checks below hold is the one that shows the stacked run to the narrow reader.
  const shown = (klass) => {
    const rules = diagramDeclarations(css).filter(
      (declaration) => declaration.property === 'display' && new RegExp(`\\.${klass}(?![\\w-])`).test(declaration.selector),
    )
    return {
      plain: rules.filter((rule) => !rule.conditions).map((rule) => rule.value),
      switched: rules
        .filter((rule) => rule.conditions)
        .map((rule) => ({ value: rule.value, at: queryWidth(rule.conditions), side: querySide(rule.conditions) })),
    }
  }
  const tallShown = shown('run-tall')
  const wideShown = shown('run-wide')
  const widths = [...new Set([...tallShown.switched, ...wideShown.switched].map((rule) => rule.at))]
  const switchAt = widths.length === 1 ? widths[0] : null
  const described = (rule) =>
    `\`display: ${rule.value}\` ${rule.at === null ? 'at a width this check cannot read' : `${rule.side === null ? 'on no side this check can read of' : `at or ${rule.side}`} ${rule.at} px`}`
  if (!tallShown.plain.includes('none')) {
    out.push(`the tall drawing is \`display: ${tallShown.plain.join(', ') || '(nothing declared)'}\` above the width the two drawings change over at, and only one of the two drawings is shown at a time: the run is drawn twice and read once (the owner's draft of 2026-10-19)`)
  }
  if (switchAt === null) {
    out.push(`the two drawings change over at ${widths.length === 0 ? 'no width this check can read' : `${widths.length} different widths (${widths.map((at) => (at === null ? 'one it cannot read' : `${at} px`)).join(', ')})`}: one drawing is shown at a time, so the page's stylesheet declares the single width that swaps them, and the whole reason that width is where it is is the label size below (the owner's draft of 2026-10-19)`)
  } else {
    if (!tallShown.switched.some((rule) => rule.value === 'block' && rule.at === switchAt && rule.side === 'below')) {
      out.push(`the tall drawing is ${tallShown.switched.map(described).join(', ') || 'not declared inside a media query'}, and a phone is met with the stacked run rather than the shrunk wide one: the query at ${switchAt} px is the one that shows it, read from below — a query naming the same width from above shows the stacked run to a desktop and the shrunk wide one to the phone it cannot be read at (the owner's draft of 2026-10-19)`)
    }
    if (!wideShown.switched.some((rule) => rule.value === 'none' && rule.at === switchAt && rule.side === 'below')) {
      out.push(`the wide drawing is ${wideShown.switched.map(described).join(', ') || 'not declared inside a media query'}, and the wide drawing is turned off below the same width the tall one is turned on below: two drawings on a page is the run drawn twice and read twice, and a query naming the same width from above turns the wide one off for the desktop and leaves both of them on the phone (the owner's draft of 2026-10-19)`)
    }
  }
  const switchColumn = switchAt === null ? null : columnAt(css, switchAt)
  if (phoneColumn === null) {
    out.push(`the column a ${PHONE_WIDTH} px phone gives the drawings cannot be read out of the page's own stylesheet: \`main\` declares no inline padding this check can turn into pixels, and the box a drawing is scaled into is what the phone's label size is taken at (the owner's draft of 2026-10-19)`)
  }
  if (switchAt !== null && switchColumn === null) {
    out.push(`the column the drawings change over at (${switchAt} px) cannot be read out of the page's own stylesheet — \`main\`'s inline padding or the page's ceiling is not declared in a form this check can turn into pixels — and that column is the narrowest one the wide drawing is shown in, which is where its label floor is taken (the owner's draft of 2026-10-19)`)
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

// ---- the opening and the close -----------------------------------------------------------

/**
 * The page's opening and its close, as the elements they render: the opening's pill, its
 * headline and the lines it quotes, the links a reader acts on, then the closing band's
 * heading, its line and its link. A block the page does not render is `null`; a list is empty.
 */
export function openingOf(html) {
  const doc = parse(html)
  const one = (name) => findAll(doc, (el) => has(el, name))[0] ?? null
  const inner = (el) => textOf(el).trim().replace(/\s+/g, ' ')
  const lines = (root) =>
    root === null
      ? []
      : [...walk(root)]
          .filter((el) => el.attrs.has('data-source'))
          .map((el) => ({ source: el.attrs.get('data-source'), text: inner(el) }))
  const links = (root) =>
    root === null
      ? []
      : [...walk(root)]
          .filter((el) => el.name === 'a' && el.attrs.get('href'))
          .map((el) => ({ href: el.attrs.get('href'), text: inner(el) }))
  const hero = one('how-hero')
  const close = one('how-close')
  const headline = hero === null ? null : ([...walk(hero)].find((el) => el.name === 'h1') ?? null)
  const pill = hero === null ? null : ([...walk(hero)].find((el) => has(el, 'pill')) ?? null)
  const heading = close === null ? null : ([...walk(close)].find((el) => el.name === 'h2') ?? null)
  return {
    hero: hero !== null,
    close: close !== null,
    pill: pill === null ? null : inner(pill),
    headline: headline === null ? null : inner(headline),
    lead: lines(hero),
    heroLinks: links(hero),
    heading: heading === null ? null : inner(heading),
    closeLines: lines(close),
    closeLinks: links(close),
  }
}

/**
 * What the page's opening and its close say that the repository does not. The board
 * (`website/design-ref/HowItWorksV3.dc.html`) draws the opening as the page's own name, the
 * headline the picture below is named with, one line of what a run is, and the two links a
 * reader acts on, and it closes the page on the same promise and the one link that acts on it.
 *
 * Every line of both blocks either is the repository's own — and is held to the document its
 * `data-source` names, exactly as the diagram's labels are — or is one of the board's own
 * marketing lines, which no document of this repository writes and which the pull request
 * lists for the owner to veto (the closing band's heading is the one of those this page
 * ships). A sentence written for the page and rendered without a source is the defect this
 * gate is for: it is a claim about how a run works that no document of this repository makes.
 */
export function openingFailures(html, docs = sources()) {
  const out = []
  const opening = openingOf(html)
  if (!opening.hero) {
    return ['the page renders no opening (a `section.how-hero`), and the board draws one: the page opens with its own name, the headline the picture is named with, one line of what a run is, and the two links a reader acts on (the owner\'s board website/design-ref/HowItWorksV3.dc.html)']
  }

  // The headline, held to the picture it names: the two are one sentence, so a reader is told
  // the same thing by the heading and by the drawing under it.
  const name = runDiagramOf(html).drawings[0]?.name ?? ''
  if (opening.headline === null) {
    out.push('the page\'s opening carries no `h1`, and the headline is the line the picture below it is named with: a reader who lands here is told what the page is about by its first heading (W1.11)')
  } else if (name === '') {
    out.push(`the page's headline reads ${JSON.stringify(opening.headline)} and the page renders no named drawing to hold it to, and the headline is the sentence the picture below it is named with (the owner's board HowItWorksV3)`)
  } else if (!comparable(name).startsWith(comparable(opening.headline))) {
    out.push(`the page's headline reads ${JSON.stringify(opening.headline)} and the drawing below it is named ${JSON.stringify(name)}, which does not open with it: the page's first line and its picture are one sentence (the owner's board HowItWorksV3)`)
  }

  // The pill: the page's own name, which is the name the site's menu gives this page.
  const menu = [...html.matchAll(/<a\b[^>]*aria-current="page"[^>]*>([\s\S]*?)<\/a>/g)].map((match) => match[1].replace(/<[^>]*>/g, '').replace(/\s+/g, ' ').trim())
  if (opening.pill === null) {
    out.push('the page\'s opening carries no pill, and the board opens it with the page\'s own name, which is the word the menu gives this page (the owner\'s board HowItWorksV3)')
  } else if (!menu.includes(opening.pill)) {
    out.push(`the page's pill reads ${JSON.stringify(opening.pill)} and the site's menu names this page ${JSON.stringify(menu)}, and the pill is the page's own name rather than a second name for it (the owner's board HowItWorksV3; src/layouts/Base.astro)`)
  }

  // Every quoted line of the opening and of the close, held to the document it names. The
  // opening and the close are sentences rather than the clauses the diagram's labels are, so
  // the page sets a label's first letter as a sentence's — that one letter may move, and
  // nothing else: `comparable` is otherwise case-sensitive, so a line rewritten rather than
  // re-capitalised is still refused.
  const asLine = (text) => {
    const line = comparable(text)
    return [line, line.charAt(0).toLowerCase() + line.slice(1), line.charAt(0).toUpperCase() + line.slice(1)]
  }
  const held = (line, where) => {
    const doc = docs[line.source]
    if (doc === undefined) {
      out.push(`${where} attributes its text to ${JSON.stringify(line.source)}, and a quoted line is quoted from a document of this repository (Global Constraint 6)`)
      return
    }
    if (!asLine(line.text).some((reading) => comparable(doc).includes(reading))) {
      out.push(`${where} reads ${JSON.stringify(line.text)}, which is not a line of ${line.source}: every line of this page is the repository's own words rather than a sentence written for the page (Global Constraint 6, D102)`)
    }
  }
  for (const line of opening.lead) held(line, 'a line of the page\'s opening')
  for (const line of opening.closeLines) held(line, 'the line of the page\'s closing band')
  if (opening.lead.length === 0) {
    out.push('the page\'s opening says what a run is in no quoted line at all, and the line under the headline is the diagram\'s own labels: the opening is the run in the repository\'s words (Global Constraint 6, D102)')
  }

  // And the other half of that: a block of text in either place that is quoted from nothing at
  // all. The board draws the opening as the page's own name (the pill), the headline, the two
  // links, and the run in the repository's words; the close is a heading, a quoted line and a
  // link. A paragraph in either block that quotes no document is a sentence written for the
  // page, which is the defect this gate exists for — it is how a claim about how a run works
  // gets onto the site with no document behind it.
  const prose = (root, own, where) => {
    for (const el of root === null ? [] : walk(root)) {
      if (!['p', 'li', 'blockquote', 'figcaption'].includes(el.name)) continue
      if (classes(el).some((name) => own.includes(name))) continue
      if (el.attrs.has('data-source') || [...walk(el)].some((child) => child.attrs.has('data-source'))) continue
      out.push(`${where} renders a block of text of its own (${JSON.stringify(textOf(el).trim().replace(/\s+/g, ' ').slice(0, 80))}) and quotes no document for it: every line of this page is the repository's own words, and a sentence written for the page is a claim no document of this repository makes (Global Constraint 6, D102)`)
    }
  }
  const blocks = parse(html)
  const block = (name) => findAll(blocks, (el) => has(el, name))[0] ?? null
  prose(block('how-hero'), ['pill', 'how-pill', 'how-actions'], 'the page\'s opening')
  prose(block('how-close'), [], 'the page\'s closing band')

  // The links a reader acts on: the board's two in the opening, and the one the close repeats.
  const wants = [
    { links: opening.heroLinks, href: '/getting-started/', where: 'the page\'s opening' },
    { links: opening.heroLinks, href: '/caps-and-costs/', where: 'the page\'s opening' },
    { links: opening.closeLinks, href: '/getting-started/', where: 'the page\'s closing band' },
  ]
  for (const want of wants) {
    if (!want.links.some((link) => link.href === want.href && link.text !== '')) {
      out.push(`${want.where} carries no link to \`${want.href}\`, and the board draws one: the opening is the way to install Fugaro and the page that says what a run costs, and the close repeats the first (the owner's board HowItWorksV3)`)
    }
  }

  // The close: the promise the page opened with, a heading of the board's own, and the link.
  if (!opening.close) {
    out.push('the page renders no closing band (a `section.how-close`), and the board draws one: the page closes on the promise it opened with and the one link that acts on it (the owner\'s board HowItWorksV3)')
  } else {
    if (opening.heading === null) {
      out.push('the page\'s closing band carries no heading, and the board closes the page on a heading of its own (the owner\'s board HowItWorksV3)')
    }
    if (opening.closeLines.length === 0) {
      out.push('the page\'s closing band carries no quoted line, and the line under its heading is the repository\'s own promise (Global Constraint 6, D102)')
    }
  }
  return out
}

// ---- the gate ----------------------------------------------------------------------------

test('How It Works is one diagram of the run, named for a screen reader and quoted from the repository', () => {
  const pages = builtPages()
  const diagrammed = pages.filter((page) => page.html.includes('run-diagram'))
  assert.deepEqual(
    diagrammed.map((page) => page.rel),
    [path.join('how-it-works', 'index.html')],
    'the run diagram is not on How It Works and only there: the owner\'s draft of 2026-10-19 puts it on that page, and the owner\'s ruling of 2026-10-20 retires the homepage\'s own four-step flow, so the site\'s one picture of a run is this one (website/test/home.mjs reads the homepage\'s slider, the block in the flow\'s place)',
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

  // The loop's arrowhead turned round: the same triangle over the same two x's with its point
  // and the base exchanged, which is the head drawn back along its own line. The head is
  // `M base (y-6) L point y L base (y+6) Z`, so the numbers are three corners and the first is
  // the base the third repeats; the swap writes the point's x where the base's was.
  const headCorners = (html) => {
    const head = /(<path class="run-loop-head" d=")([^"]*)"/.exec(html)
    assert.ok(head, 'the built page draws no loop arrowhead, and this fixture redraws it')
    const numbers = head[2].match(/-?\d+(?:\.\d+)?/g)
    assert.equal(numbers.length, 6, 'the loop arrowhead is three corners of a triangle, and this fixture redraws it corner by corner')
    return { head, numbers }
  }
  const mirrorHead = (html) => {
    const { head, numbers: [base, yTop, point, yMid, , yBottom] } = headCorners(html)
    return html.replace(head[0], `${head[1]}M ${point} ${yTop} L ${base} ${yMid} L ${point} ${yBottom} Z"`)
  }
  // And the point dropped, leaving the base: two corners in the box the head was drawn in, so
  // the position check still passes and the head is refused for what it is.
  const flattenHead = (html) => {
    const { head, numbers: [base, yTop, , , other, yBottom] } = headCorners(html)
    return html.replace(head[0], `${head[1]}M ${base} ${yTop} L ${other} ${yBottom}"`)
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
    // The head's *direction*, which is not its position: the triangle turned round over the
    // same two x's, which is the head the file drew before this check existed — the point at
    // the base's place and the base where the point belongs, so the arrow lands pointing back
    // the way it came. Nothing about the head's box changes, so the position check above cannot
    // see it.
    [
      'a loop arrowhead pointing back down its own line',
      mirrorHead(page),
      /draws the loop's arrowhead backwards/,
    ],
    // And a head that is not a triangle at all: the point dropped, leaving the base — the head
    // is what says which way the arrow points, so a path that is not three corners is refused
    // rather than measured.
    [
      'a loop arrowhead that is not a triangle',
      flattenHead(page),
      /cannot read as a triangle \(2 corners\)/,
    ],
    [
      'a loop line nothing can measure',
      // The line's `d` replaced by a curve: the drawing's own paths are polylines, and a path
      // this check cannot read is one whose ends are not held to the pills at all.
      page.replace(/(class="run-loop-line" d=")[^"]*"/, '$1C 0 0 4 4 8 8"'),
      /draws the loop with a path this check cannot read/,
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
      // The card's own markup: the item, its number and its name — the number the bold
      // redesign draws inside every card, read past rather than spelled out here.
      page.replace(/<li class="run-fact"[^>]*>[\s\S]*?<strong class="run-fact-name"[^>]*>Pull request<\/strong>[\s\S]*?<\/li>/, ''),
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
  /** A fragment of the built stylesheet replaced wherever it appears, when it appears at all. */
  const replaceEvery = (pattern, to) => {
    const found = [...css.matchAll(pattern)].length
    assert.ok(found > 0, `this fixture replaces ${pattern}, and the built stylesheet does not carry it`)
    return css.replace(pattern, to)
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
    // A ceiling under the phone's own column: the drawing is scaled down rather than up, and
    // the phone's label size is taken in that box.
    [
      'a tall drawing capped below the phone',
      rewrite('run-tall', 'max-width:22.5rem', 'max-width:14rem'),
      /tall drawing's smallest label is .* px in the .* px box a 360 px phone gives it/,
    ],
    // The other half of the changeover: the tall drawing turns on where the wide one is not
    // turned off, which is two drawings on one page.
    [
      'a wide drawing that is not turned off at the changeover',
      replaceOnce('{display:none}.run-tall', '{display:block}.run-tall'),
      /the wide drawing is `display: block`/,
    ],
    // The same width read from the other side: `(width>=59.25rem)` names the width the page
    // changes over at and changes over the wrong way — the stacked drawing is shown to the
    // desktop and the shrunk wide one to the phone the floor exists for. Both rules are still
    // at 948 px, so the width alone cannot see it.
    [
      'the drawings changing over the other way round',
      replaceOnce('(width<=59.25rem)', '(width>=59.25rem)'),
      /a phone is met with the stacked run|turned off below the same width/,
    ],
    // And the arithmetic itself: a stylesheet that does not say what `main`'s gutter is leaves
    // the box every readability number is taken at unmeasurable, which is refused rather than
    // defaulted — the check writes no box of its own.
    [
      'a page whose gutter cannot be read',
      replaceEvery(/main\{[^}]*\}/g, ''),
      /cannot be read out of the page's own stylesheet/,
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

test('How It Works opens on the run in the repository\'s words and closes on the same promise', () => {
  const page = readFileSync(path.join(DIST, 'how-it-works', 'index.html'), 'utf8')
  const failures = openingFailures(page)
  assert.deepEqual(
    failures,
    [],
    `the page's opening is not the one the owner's board draws:\n  ${failures.join('\n  ')}`,
  )
})

test('every opening refusal fires on the defect it names', (t) => {
  // The same proof the diagram's gate carries: the built page passes, and each fixture that
  // breaks one thing in the opening makes exactly that thing's line appear. Without these the
  // gate would pass on a page whose lead is a sentence written for it, which is the defect it
  // exists for (Global Constraint 6, D102).
  const page = readFileSync(path.join(DIST, 'how-it-works', 'index.html'), 'utf8')
  const docs = sources()
  const verdict = (html) => openingFailures(html, docs)
  assert.deepEqual(verdict(page), [], 'the built page does not pass the gate its own fixtures are drawn from')

  /** A fragment of the built page replaced, when the built page carries it. */
  const rewritten = (pattern, to) => {
    const found = typeof pattern === 'string' ? page.includes(pattern) : pattern.test(page)
    assert.ok(found, `this fixture rewrites ${pattern}, and the built page does not carry it`)
    return page.replace(pattern, to)
  }

  const cases = [
    ['a page with no opening', rewritten('class="how-hero"', 'class="how-hero-x"'), /renders no opening/],
    [
      'an opening with no headline',
      rewritten(/<h1 class="page-title display-xl"[\s\S]*?<\/h1>/, ''),
      /opening carries no `h1`/,
    ],
    // The headline and the picture are one sentence: the heading renamed is the page telling a
    // reader one thing and its own drawing telling them another.
    [
      'a headline the picture is not named with',
      rewritten('>How a task becomes <span class="gradient-text"', '>How a run becomes <span class="gradient-text"'),
      /headline reads .*which does not open with it/,
    ],
    [
      'an opening with no pill',
      rewritten(/<p class="pill how-pill"[\s\S]*?<\/p>/, ''),
      /opening carries no pill/,
    ],
    [
      'a pill that is not the page\'s own name',
      rewritten('>How it works</p>', '>How a run works</p>'),
      /pill reads .*and the site's menu names this page/,
    ],
    // The lead: a line of the diagram's labels paraphrased, and the same paragraph with every
    // source taken off it — a sentence written for the page and rendered as though it were the
    // repository's.
    [
      'a line of the opening that is not the document it names',
      rewritten('>One execution per task, then gone<', '>One execution per task, then deleted<'),
      /a line of the page's opening reads .*not a line of README.md#How it works/,
    ],
    [
      'an opening whose lines name no document',
      // Both refusals are true of it — the opening quotes nothing, and so it renders a block
      // of its own — and the case names both rather than one of them.
      page.replace(/(<section class="how-hero"[\s\S]*?)<\/section>/, (whole, body) => `${body.replace(/ data-source="[^"]*"/g, '')}</section>`),
      /opening says what a run is in no quoted line at all|opening renders a block of text of its own/,
    ],
    [
      'an opening with no way to install it',
      rewritten(/<a class="how-cta" href="\/getting-started\/"[^>]*>Get started<\/a><a class="how-cta-quiet"/, '<a class="how-cta-quiet"'),
      /opening carries no link to `\/getting-started\/`/,
    ],
    [
      'an opening with nothing about what a run costs',
      rewritten(/<a class="how-cta-quiet" href="\/caps-and-costs\/"[^>]*>Caps and costs<\/a>/, ''),
      /opening carries no link to `\/caps-and-costs\/`/,
    ],
    // The closing band taken off the page: both refusals are true of it — there is no band,
    // and so no link in it — and the case names both rather than one of them.
    // A sentence written for the page and quoted from nothing: the defect this gate exists
    // for, planted where a page this new would grow one.
    [
      'an opening that says something of its own',
      rewritten('<p class="how-actions"', '<p class="how-claim">Fugaro is fast, safe and cheap.</p><p class="how-actions"'),
      /opening renders a block of text of its own .*and quotes no document for it/,
    ],
    [
      'a closing band that says something of its own',
      rewritten('</p></div><a class="how-cta"', '</p><p class="how-claim">It costs nothing to try.</p></div><a class="how-cta"'),
      /closing band renders a block of text of its own .*and quotes no document for it/,
    ],
    [
      'a page with no closing band',
      rewritten('class="how-close"', 'class="how-close-x"'),
      /renders no closing band|closing band carries no link to `\/getting-started\/`/,
    ],
    [
      'a closing band with no heading',
      rewritten(/<h2 class="how-close-title[^"]*"[^>]*>[\s\S]*?<\/h2>/, ''),
      /closing band carries no heading/,
    ],
    [
      'a closing line that is not the document it names',
      rewritten('>In your own cloud project</p>', '>In our own cloud project</p>'),
      /the line of the page's closing band reads .*not a line of README.md#Why Fugaro/,
    ],
    [
      'a closing band with no way to act on it',
      rewritten(/(<section class="how-close"[\s\S]*?)<a class="how-cta"[^>]*>Get started<\/a>/, '$1'),
      /closing band carries no link to `\/getting-started\/`/,
    ],
  ]
  for (const [what, broken, expected] of cases) {
    const lines = verdict(broken)
    // Every line has to be the refusal the case names: a fixture that breaks one thing and a
    // gate that refuses it for another reason is a case that proves nothing.
    assert.deepEqual(
      [...new Set(lines.map((line) => (expected.test(line) ? 'fired' : line)))],
      ['fired'],
      `${what}: the refusal did not fire, and this is what it said instead:\n${lines.join('\n')}`,
    )
  }
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
