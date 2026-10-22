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
//     side shows the wide drawing to the phone it cannot be read on. Both drawings have to be
//     on the page, and each is held to its own half of the changeover: the stacked drawing must
//     be off where the queries are silent — above that width the wide one is the one shown — and
//     the wide drawing must not be, since a wide drawing turned off there leaves a desktop no
//     run at all, the stacked one being `display: none` above the changeover. Only one of the
//     two is displayed at a time, so a page that lost one of them, or that turned the wide one
//     off outside the changeover, is a page whose reader is shown no run. A number typed in here
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
//   - **Every shape of the drawing is painted, from the palette, and every label is legible
//     where it is drawn** (`runPaintFailures` below). The drawing shipped as a series of black
//     rectangles — the owner's reading of the preview, 2026-10-21 — because its shapes are
//     markup the component generates and hands to `set:html`, which Astro's scope attribute
//     never reaches, so every scoped `.run-card[cid]`-shaped paint rule was dead and SVG's own
//     default fill (black) is what a reader saw. A gate that asked whether a rule with that
//     class exists passed over all of it. This reading resolves each shape's paint from the
//     rules that can reach it, refuses a shape left on SVG's default or painted with a colour
//     literal rather than a palette token, and computes the contrast of each label against the
//     surface it is drawn on.
//
// Standard library only; no network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { DIST, REPO, builtPages, cssDeclarations, cssRules, findAll, parse, textOf, walk } from './lib/html.mjs'
import { ancestorsOf, classesOf, ruleReaches } from './lib/selectors.mjs'
import { contrastRatio, paletteTokensInForce, parseColour } from './lib/colour.mjs'
import { BOARDS, boardStrings } from '../src/lib/zoom-boards.mjs'

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

/**
 * The inline-start padding a class's rules leave in force, as the value the cascade leaves:
 * `padding`, `padding-left` and `padding-inline-start` are one answer written three ways, so
 * they are read in the stylesheet's own order and the last one wins. That order is the whole
 * point of reading them together — the built `.run-also` declares `padding: 0` and then the
 * logical longhand that gives the list its room back, and a reading that stopped at the
 * shorthand would see no room at all. `null` when nothing declares one, which is the browser's
 * own `0`; a value this check cannot turn into pixels is left for the caller to refuse.
 */
function inlineStartPadding(css, klass) {
  let found = null
  for (const rule of cssRules(css)) {
    if (!new RegExp(`\\.${klass}(?![\\w-])`).test(rule.selector ?? '')) continue
    for (const [property, value] of cssDeclarations(rule.body ?? '')) {
      const parts = value.trim().split(/\s+/)
      if (property === 'padding') found = parts.length === 1 ? parts[0] : parts[1]
      else if (property === 'padding-left' || property === 'padding-inline-start') found = parts[0]
    }
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

  // And that list is a bulleted list, one bullet to an item (the owner's feedback of
  // 2026-10-21). The owner read the five facts as one run-on paragraph, because the panel drew
  // the rows the board draws and every one of them was set with `list-style: none` — a list whose
  // items carry no marker is a list a reader meets as prose.
  //
  // What a reader sees is the **marker the browser generates**, so this reads the two
  // declarations that decide it — `list-style` and `list-style-type` — **together and in the
  // stylesheet's own order**, because the shorthand and the longhand are one answer in the
  // cascade rather than two: `list-style: none inside` and `list-style: outside none` both leave
  // the browser nothing to draw, and a `list-style-type: none` written after a shorthand takes
  // the marker away again. So the reading walks the rules for the class and keeps the **type** in
  // force: the shorthand's own half about the marker (its two position words, `inside` and
  // `outside`, and a `url(...)` image say nothing about it) replaces the type, `none` being the
  // one word in it that is about the marker and taking it away, and a shorthand naming no type
  // leaves the initial `disc` in place — which is the shape this page is built in, `list-style:
  // disc` being what the minifier writes as `list-style: outside`. `declaredFor` above reads one
  // property's last declaration, which is one answer out of the two and the bug this reading was
  // written for.
  //
  // A list drawn with no marker at all is the other half, and it is not a declaration: a marker
  // is generated for a **list item**, so the rows have to still be the list's items where they
  // are laid out. The board this panel came from draws those rows as flex rows, and a flex row is
  // not a list item — `display: flex` on the item blockifies it and the marker goes with it,
  // whatever the list's own `list-style` reads — so both the list and its items are read for a
  // `display` that takes the marker away.
  //
  // `base.css` gives every marker on the site its colour (`:where(li)::marker`), so the marker's
  // tone is the site's and not this page's, and it is not a declaration to read here. Its place
  // is a declaration, and the room that place needs is read below.
  const POSITION_WORDS = new Set(['inside', 'outside'])
  let markerType = null
  let markerPosition = null
  const markerReadings = []
  for (const rule of cssRules(css)) {
    if (!new RegExp(`\\.run-also(?![\\w-])`).test(rule.selector ?? '')) continue
    for (const [property, value] of cssDeclarations(rule.body ?? '')) {
      if (property !== 'list-style' && property !== 'list-style-type' && property !== 'list-style-position') continue
      markerReadings.push(`${property}: ${value}`)
      if (property === 'list-style-type') {
        markerType = value.trim()
        continue
      }
      const words = value.trim().split(/\s+/)
      if (property === 'list-style-position') {
        markerPosition = words[0]
        continue
      }
      const place = words.find((word) => POSITION_WORDS.has(word.toLowerCase()))
      if (place) markerPosition = place.toLowerCase()
      const about = words.filter((word) => !POSITION_WORDS.has(word.toLowerCase()) && !/^url\(/i.test(word))
      markerType = about.includes('none') ? 'none' : (about[0] ?? 'disc')
    }
  }
  if (markerType === null) {
    out.push('the list of the facts beside the picture declares no `list-style` and no `list-style-type`, and the page draws no bullet of its own: the items of that list carry a bullet point each (the owner\'s feedback of 2026-10-21), so the marker is the stylesheet\'s to name and a list nothing declares one for falls back to the browser\'s own, which is a marker the page has not chosen')
  } else if (markerType === 'none') {
    out.push(`the list of the facts beside the picture is drawn with no marker on its items (${markerReadings.join(', ')}, which leaves \`list-style-type: none\` in force), and the owner's feedback of 2026-10-21 asks for a bullet point on each of its items: a list with no marker is the run-on paragraph the owner read — the rows the board draws, with nothing in front of them`)
  }
  const ITEM_DISPLAY = /^(flex|inline-flex|grid|inline-grid)$/
  const listDisplay = declaredFor(css, 'run-also', 'display')
  const itemDisplay = declaredFor(css, 'run-also-item', 'display')
  if (listDisplay !== undefined && ITEM_DISPLAY.test(listDisplay.value.trim())) {
    out.push(`the list of the facts beside the picture is \`display: ${listDisplay.value.trim()}\`, and the items of that list carry a bullet point each (the owner's feedback of 2026-10-21): a flex or grid container lays its children out as its own boxes, and a child that is not laid out as a list item is a child the browser draws no marker for, which is the run-on paragraph the owner read whatever the list's own \`list-style\` says`)
  } else if (itemDisplay !== undefined && itemDisplay.value.trim() !== 'list-item') {
    out.push(`the rows of the list beside the picture are \`display: ${itemDisplay.value.trim()}\`, and the items of that list carry a bullet point each (the owner's feedback of 2026-10-21): a marker is generated for a list item, so a row laid out as a block, a flex row or a grid cell is a row the browser draws no marker for — the board draws these rows as rows, and the marker is what a reader sees`)
  }

  // A marker the stylesheet names is not yet a marker a reader sees, and that is the whole of the
  // owner's second reading of this section (their feedback of 2026-10-22: "can we add a bullet
  // point in front of each point in this section?" — asked of a page whose list already declared
  // `list-style: disc`, with every gate above passing).
  //
  // An outside marker is drawn **in the list's own inline padding**, not in the row's: the
  // browser's stylesheet gives every list 40 px of it for exactly that reason, and a list whose
  // own inset is `0` has its markers drawn left of its own padding box — outside the box this
  // panel's `overflow: hidden` clips at, which is the clip its rounded corners need. What the
  // owner read settles the question the declarations above cannot answer: the rows of this list
  // hold 32 px of start padding each and the markers were still not there to read.
  //
  // So the room is read where it is drawn — the list's own inline-start padding, with `--space-md`
  // (the inset `base.css` gives every list on the site) as the floor, because a bullet needs the
  // room a bullet needs and a page that holds less than the site's own inset for its markers is a
  // page whose markers sit under its own text. `list-style-position: inside` is the one shape that
  // needs none of this: a marker drawn inside the row's content box is one the clip cannot reach,
  // so that list is let through.
  if ((markerPosition ?? 'outside') !== 'inside') {
    const room = inlineStartPadding(css, 'run-also')
    const px = room === null ? 0 : lengthInPx(room, css)
    const listInset = lengthInPx('var(--space-md)', css)
    const clips = ['overflow', 'overflow-x', 'overflow-y']
      .map((property) => declaredFor(css, 'run-also', property))
      .filter((declaration) => declaration !== undefined)
      .map((declaration) => declaration.value.trim())
      .filter((value) => value !== 'visible')
    const clipping = clips.length === 0
      ? 'and a marker drawn outside the list is a marker drawn outside the panel it belongs to'
      : `where the panel's \`overflow: ${clips.join(' ')}\` takes it away`
    if (Number.isNaN(px)) {
      out.push(`the list of the facts beside the picture declares \`padding-inline-start: ${room}\`, which this check cannot turn into pixels, and the room a list holds for its markers is the room this reading is about (the owner's feedback of 2026-10-22): a length nothing here can measure is a room nothing has checked, so it is refused rather than read as enough`)
    } else if (px < listInset) {
      out.push(`the list of the facts beside the picture holds ${px} px of inline-start padding for the markers of its items (${room === null ? 'nothing declared, which is the browser\'s own 0' : `\`${room}\``}), and the owner's feedback of 2026-10-22 asks for a bullet point in front of each of them: a marker drawn outside its item is drawn in the list's own inline padding, so a list holding less than the site's own inset for it draws its bullets outside its padding box, ${clipping} — the rows were left as the run-on paragraph the owner read`)
    }
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
  // The wide drawing's half of the same reading. `wideShown.plain` was collected and never
  // looked at, so the drawing turned off **where no query asks** passed: the query's own
  // `display: none` written onto the base rule as well leaves a desktop with no drawing at all,
  // since the stacked one is `display: none` above the changeover, and the two `switched`
  // readings below went on agreeing with themselves. The read is `includes('none')` and not
  // "the page declares an unconditional display", because the wide `<svg>` carries `.run-svg`
  // and no display rule of its own: what is refused is a rule that takes it off where the
  // queries are silent, and a page that declares nothing there is a page that shows it.
  if (wideShown.plain.includes('none')) {
    out.push(`the wide drawing is \`display: ${wideShown.plain.join(', ')}\` outside the media query the two drawings change over in, and the wide drawing is the one shown where that query does not hold — the stacked drawing is \`display: none\` above that width —: a drawing turned off where no query asks for it leaves the desktop with no run at all, both drawings off at once (the owner's draft of 2026-10-19)`)
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
  // And the wide drawing is *there*, which is the other half nothing read. The stacked drawing
  // is held to existing below; the wide one was held to nothing, so a page built with the wide
  // `<svg>` dropped still passed every reading in this file — the stages, the labels, the loop's
  // arrow and the list under the picture are read out of whichever drawings the page carries,
  // and every one of them is true of the stacked drawing alone. Above the changeover that
  // drawing is `display: none`, so what such a page draws for a desktop is nothing.
  if (!wide) {
    out.push(`the page carries no wide drawing, and the two drawings are one picture at two scales with only one of them shown at a time: above the width they change over at the stacked drawing is \`display: none\`, so a page that lost the wide one draws no run for the reader a desktop is (the owner's draft of 2026-10-19)`)
  }
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

// ---- the drawing's paint: what a reader actually sees -------------------------------------
//
// **The defect this reading exists for (2026-10-21).** The owner read the preview and said the
// diagram under "The run, end to end" was "a series of black rectangles". It was: the shapes of
// the drawing are markup this component generates and hands to `set:html`, and Astro appends the
// component's `data-astro-cid-…` to **every** compound of a scoped selector — so the built
// stylesheet declared `.run-card[data-astro-cid-…]` while every `<rect>` the drawing writes
// carries no scope attribute. All eleven paint rules were dead. Nothing else was wrong: the
// markup was right, the labels were right, the geometry was right, and every gate in this file
// passed, because every one of them reads the *drawing* and none read the *paint*. What a reader
// saw instead of the palette was SVG's own defaults — `fill: black`, `stroke: none` — which is
// black cards, black labels, and arrows that are stroke alone and so were not drawn at all.
//
// So this reading resolves each shape's paint the way a browser would: the last declaration of a
// rule that can reach the element, or the element's own presentation attribute when no rule
// declares the property, or SVG's initial value when neither does. It refuses a shape left on
// that initial value, a shape whose paint is not a colour the palette holds, a `var()` written
// into a presentation attribute (which is not a colour there — `var()` is valid in CSS only, and
// an invalid presentation attribute is dropped, leaving the shape black), and a label whose
// colour and the surface it is drawn on do not clear the ratio the site holds body text to.
//
// A rule that can reach the element is `lib/selectors.mjs`'s reading, the same one the changelog
// gate makes of its code chips: a rule whose selector names a scope the element does not carry
// is not in force, however its class reads. That is what makes this gate fail on the defect as
// it shipped rather than pass on it.

/** The SVG shapes a drawing paints, and the properties a colour may arrive on. A `<text>` is
 *  filled and a `<line>` has no fill at all, so each of those is painted by its one property;
 *  a `<rect>`, a `<circle>` and a `<path>` are painted by either — the cards and the arrowheads
 *  by their fill, the loop's own line by its stroke, with `fill: none` declared over it. */
const PAINT_PROPERTIES = { text: ['fill'], line: ['stroke'], rect: ['fill', 'stroke'], path: ['fill', 'stroke'], circle: ['fill', 'stroke'] }

/** What a drawing holds besides the shapes it paints: the two `<svg>` roots, the groups the
 *  geometry is nested in, the words a screen reader reads, and — for the zoom levels — the one
 *  `<defs>` that holds the shared `<symbol>`s and the `<use>` references that draw them. A
 *  `<symbol>` needs no paint of its own (it is never rendered where it stands) and a `<use>` is
 *  painted by the shapes of the `<symbol>` it names, which this reading reads where they are. A
 *  `<marker>` is the same kind of thing as a `<defs>`: it is never rendered where it stands, and
 *  the `<path>` inside it — which is what an arrowhead is — is read where that stands.
 *  An element of any other name is refused rather than skipped — a `<polygon>`, an `<ellipse>`
 *  or an `<image>` added to a drawing is a shape this reading does not know how to resolve the
 *  paint of, and a shape it cannot read is exactly where an unpainted one would hide (ZF42). */
const DRAWING_CONTAINERS = new Set(['svg', 'g', 'title', 'desc', 'defs', 'symbol', 'use', 'marker'])

/** WCAG 2.1 AA for body text: the ratio every label of the drawing is held to. The labels are
 *  12 px and up (the floor `runDiagramFailures` holds the drawings to), which is not "large
 *  text", so the ratio is the body one and not the 3:1 UI one. */
const LABEL_CONTRAST = 4.5

/** The palette token a paint value names, or `null` when it names no bare token. */
const tokenOf = (value) => /^var\(\s*(--[a-zA-Z0-9-]+)\s*\)$/.exec(value.trim())?.[1] ?? null

/** An opaque hex colour as `[r, g, b]`, or `null` for anything else (`none`, a colour function,
 *  a named colour). `parseColour` asserts rather than returning, so the guard is the reader. */
const rgbOf = (value) => (/^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test((value ?? '').trim()) ? parseColour(value, 'a colour of the drawing') : null)

/**
 * The paint an element ends up with for one property, as `{ value, from }` — the last
 * declaration of a rule that can reach the element (the page's rules for one shape are one class
 * deep and do not compete on specificity, so the cascade order is the file's order), or the
 * element's own presentation attribute when no rule declares the property — and `null` when
 * nothing does, which is SVG's initial value: black for `fill`, none for `stroke`, and not a
 * colour this page chose.
 *
 * A rule inside a conditional block is not read: the drawing's own rules sit in none, and a
 * shape painted only under some condition is a shape whose paint this reading cannot state,
 * which the refusal below reports rather than passes.
 */
function paintInForce(css, el, property) {
  let found = null
  for (const rule of cssRules(css)) {
    const selector = (rule.selector ?? '').trim()
    if (selector.startsWith('@') || (rule.conditions ?? []).length > 0) continue
    if (!ruleReaches(selector, el)) continue
    for (const [name, value] of cssDeclarations(rule.body ?? '')) {
      if (name === property) found = { value: value.trim(), from: `\`${selector}\``, source: 'rule' }
    }
  }
  if (found !== null) return found
  const attribute = el.attrs.get(property)
  return attribute === undefined
    ? null
    : { value: attribute.trim(), from: `its own \`${property}="${attribute}"\` attribute`, source: 'attribute' }
}

/**
 * What a declared paint value is worth: the palette token it names and its colour, `null` when it
 * is `none` or is not a colour the palette in force holds.
 *
 * Two writings are colours of the palette: the token (`var(--surface)`, which is how the page's
 * stylesheet paints) and the token's own literal (`#121a26`, which is how a value written straight
 * into the markup would read). Both are resolved through the palette, so that a colour the
 * drawing paints is one the contrast reading below can compute a pair from, and the palette is
 * still the one place a colour is decided.
 */
function paintColour(reading, palette, values) {
  if (reading === null || reading.value === 'none') return null
  const token = tokenOf(reading.value)
  if (token !== null) {
    const value = palette.get(token.replace(/^--/, ''))
    if (value === undefined) return null
    return { token, value, rgb: parseColour(value, `the palette's \`${token}\``) }
  }
  const rgb = rgbOf(reading.value)
  if (rgb === null || !values.has(reading.value.toLowerCase())) return null
  return { token: null, value: reading.value, rgb }
}

/**
 * The same reading, refused where it cannot paint: a `var()` is a colour in a stylesheet and not
 * in a presentation attribute, so a shape carrying `fill="var(--surface)"` is a shape painted
 * nothing at all. Both the refusal below and the contrast reading go through this, so that the
 * two agree on what the drawing is painted with.
 */
function painted(reading, palette, values) {
  if (reading === null || reading === undefined) return null
  if (reading.source === 'attribute' && tokenOf(reading.value) !== null) return null
  return paintColour(reading, palette, values)
}

/** The `<text>` elements of a drawing, as the geometry `textBox` measures them at. */
function drawnText(el) {
  return {
    text: textOf(el).trim(),
    x: Number(el.attrs.get('x')),
    y: Number(el.attrs.get('y')),
    size: Number(el.attrs.get('font-size')),
    anchor: el.attrs.get('text-anchor') ?? 'start',
  }
}

/**
 * The surface a label of the drawing is drawn on: the stage pill the label sits in, or — for
 * every other label — the card the box is drawn on. The pill is read by the drawing's own
 * geometry, the way `stagePills` reads which stage a word belongs to, so a label's ground is a
 * fact about where the drawing put it rather than about the class it carries.
 *
 * `{ colour, why }`: the surface's colour when this reading can state one, and otherwise `why`,
 * the line the failure carries. A label whose ground cannot be found is refused rather than
 * passed over — a ratio nothing can compute is a contrast check that is not running — while a
 * ground whose own paint is refused is left to the refusal above, where the rect is read.
 */
function labelGround(text, css, palette, values) {
  const box = ancestorsOf(text).find((el) => el.name === 'g' && (has(el, 'run-box') || has(el, 'run-band-box')))
  if (box === undefined) {
    return {
      colour: null,
      why: 'it is drawn outside any box of the drawing (a `g.run-box` or `g.run-band-box`), so this reading cannot say what surface it sits on: the ground is read from the box the drawing puts the label in, and a label whose ground no check can state is a label the contrast reading passes over without a word',
    }
  }
  const rects = [...walk(box)].filter((el) => el.name === 'rect')
  const card = rects.find((el) => has(el, 'run-card') || has(el, 'run-band'))
  const drawn = drawnText(text)
  const pill = rects
    .filter((el) => has(el, 'run-stage-pill'))
    .find((el) =>
      inPill(
        textBox(drawn),
        drawn,
        { x0: Number(el.attrs.get('x')), x1: Number(el.attrs.get('x')) + Number(el.attrs.get('width')), y0: Number(el.attrs.get('y')), y1: Number(el.attrs.get('y')) + Number(el.attrs.get('height')) },
      ),
    )
  if (pill === undefined && card === undefined) {
    return {
      colour: null,
      why: 'the box it is drawn in carries neither a card (`run-card`/`run-band`) nor a stage pill it sits in, so there is no surface under it and no pair to compute a ratio from: every label of this drawing is drawn on the card of its box or in a pill, and one that is drawn on neither is a label nothing holds',
    }
  }
  return { colour: painted(paintInForce(css, pill ?? card, 'fill'), palette, values), why: null }
}

/** How a label's colour is named in a failure: its token when it has one, its literal when it
 *  is written as the palette's own value. */
const named = (colour) => (colour.token === null ? colour.value ?? '' : `\`${colour.token}\``)

/**
 * Every way the built page's run drawing is left unpainted or unreadable, as the lines a reader
 * of the failure gets: a shape no rule reaches (the defect of 2026-10-21), a shape painted with
 * something that is not a colour the palette holds — a literal that is not one of its values, a
 * colour function, or a `var()` written into a presentation attribute, where it is invalid — and
 * a label whose colour against the surface it is drawn on is under the ratio the site's own text
 * is held to.
 *
 * Pure, over the page's markup and its stylesheet, so the built page is checked with it and the
 * refusals below are proved with it.
 */
/**
 * Every way one shape of a drawing is left unpainted or painted with something the palette does
 * not hold, as the lines a reader of the failure gets. One reading serves every drawing of the
 * site — the run's picture and the four zoom levels — so that two drawings cannot be held to two
 * different rules about what a colour is.
 *
 * A shape kind the reading has not been taught is refused by name: a drawing that grows an
 * element is a drawing whose paint no check holds until the reading knows how that element takes
 * its colour (ZF42, the gap the defect of 2026-10-21 came through).
 */
function paintRefusals(shape, css, palette, values) {
  const lines = []
  const properties = PAINT_PROPERTIES[shape.name]
  if (properties === undefined) {
    if (!DRAWING_CONTAINERS.has(shape.name)) {
      lines.push(
        `it is a \`<${shape.name}>\`, and this reading knows how a drawing's \`<rect>\`, \`<circle>\`, \`<path>\`, \`<line>\` and \`<text>\` are painted and nothing else. A shape it cannot read is a shape whose paint no check holds, which is the gap the defect of 2026-10-21 came through: teach this reading the element and how it takes its colour`,
      )
    }
    return lines
  }
  const readings = properties.map((property) => ({ property, ...(paintInForce(css, shape, property) ?? { value: null, from: null }) }))
  const declared = readings.filter((reading) => reading.value !== null)

  for (const reading of declared) {
    // `var()` is CSS's own syntax and an SVG presentation attribute is not CSS: an invalid
    // attribute is dropped, and the shape falls back to SVG's initial value — black for
    // `fill`, none for `stroke`. This is the trap a fix written in the markup falls into,
    // and it is refused whatever token it names, because the token is not read there at all.
    if (reading.source === 'attribute' && tokenOf(reading.value) !== null) {
      lines.push(
        `its \`${reading.property}\` is ${reading.value} (from ${reading.from}), and \`var()\` is not a colour in an SVG presentation attribute: it is CSS's own syntax, an invalid attribute is dropped, and the shape falls back to SVG's initial value, which for \`fill\` is black and for \`stroke\` is none. The colour belongs in a class in the page's stylesheet, where the token resolves`,
      )
      continue
    }
    // A colour of the palette, and nothing at all, are the two values there is nothing to
    // say about: the first is the shape painted the way the site paints, and the second is
    // one property of a shape another property may still paint.
    if (painted(reading, palette, values) !== null || reading.value === 'none') continue
    // A token the palette does not hold is a colour nothing in force decides, and the one
    // reading that could say what the shape is painted would be a second palette.
    const token = tokenOf(reading.value)
    if (token !== null) {
      lines.push(
        `its \`${reading.property}\` is ${reading.value} (from ${reading.from}), and the palette in force declares no \`${token}\` (${[...palette.keys()].map((name) => `--${name}`).join(', ')}): a shape painted with a token that is not there resolves to nothing, and SVG's initial value takes over — a black fill, no stroke`,
      )
      continue
    }
    // A colour the palette does not hold is still a colour: the shape is visible, and the
    // refusal is about where the value came from rather than about a reader seeing nothing.
    if (rgbOf(reading.value) !== null) {
      lines.push(
        `its \`${reading.property}\` is ${reading.value} (from ${reading.from}), which is not one of the palette's own colours (${[...new Set(palette.values())].join(', ')}): every colour the site paints comes from the palette in force, so that it is one value to change and one pair the contrast check can read`,
      )
      continue
    }
    lines.push(
      `its \`${reading.property}\` is ${reading.value} (from ${reading.from}), which this reading cannot turn into a colour: a colour function, a named colour or a \`currentColor\` is a value no contrast check can compute, and the palette in force holds opaque hex literals`,
    )
  }

  const isPainted = declared.some((reading) => painted(reading, palette, values) !== null)
  if (!isPainted && declared.length === 0) {
    lines.push(
      `no rule of the page's stylesheet can reach it and it declares no \`fill\` or \`stroke\` of its own: SVG's initial values are a black fill and no stroke at all, so the shape is painted black — the drawing's cards and its labels are rectangles of black — or, where it is stroke alone, not drawn at all. The shapes of this drawing arrive through \`set:html\`, which Astro's scope attribute never reaches: a scoped \`.run-…[data-astro-cid-…]\` rule cannot match them, and the paint rules need \`:global()\` around the shape's own compound (the owner's reading of the preview, 2026-10-21)`,
    )
  } else if (!isPainted && declared.every((reading) => reading.value === 'none')) {
    // Every paint the shape declares is `none`, so it is drawn as nothing — the other half
    // of the defect, and the half a reader sees as an absent arrow rather than a black card.
    // `none` is the one declared value no refusal above speaks for, so this is where it is
    // said; a shape whose paint is a colour this reading refuses has that line already, and
    // this one would only repeat it.
    lines.push(
      `it declares ${declared.map((reading) => `\`${reading.property}: ${reading.value}\``).join(' and ')} and no colour at all, so it is drawn as nothing: a stroke-only shape is not drawn without a stroke and a card is not drawn without a fill, which is the same defect as the black rectangles read the other way round — a rule that paints the shape is what has to declare the colour (the owner's reading of the preview, 2026-10-21)`,
    )
  }
  return lines
}

export function runPaintFailures(html, css, palette = paletteTokensInForce()) {
  const out = []
  const values = new Set([...palette.values()].map((value) => value.toLowerCase()))
  const figures = findAll(parse(html), (el) => el.name === 'figure' && classes(el).includes('run-diagram'))
  if (figures.length === 0) {
    return ['the page renders no run diagram, and this reading is about the shapes of one: the site\'s picture of a run is the drawing on How It Works (the owner\'s draft of 2026-10-19)']
  }

  for (const figure of figures) {
    for (const svg of walk(figure)) {
      if (svg.name !== 'svg') continue
      const where = `the ${has(svg, 'run-wide') ? 'wide' : has(svg, 'run-tall') ? 'tall' : '(unclassed)'} drawing`
      // The shapes are read class by class: one line per shape would be a hundred lines of the
      // same sentence, and a reader of the failure wants the rule to fix.
      const groups = new Map()
      const note = (el, line) => {
        const key = `<${el.name} class="${classes(el).join(' ')}">`
        if (!groups.has(key)) groups.set(key, { count: 0, lines: new Set() })
        const group = groups.get(key)
        group.count += 1
        group.lines.add(line)
      }

      for (const shape of walk(svg)) {
        for (const line of paintRefusals(shape, css, palette, values)) note(shape, line)
      }
      // And the labels: what each one is filled with, and what it is drawn on.
      for (const text of [...walk(svg)].filter((el) => el.name === 'text')) {
        const fill = painted(paintInForce(css, text, 'fill'), palette, values)
        if (fill === null) continue // its paint is refused above, where every shape is read
        const ground = labelGround(text, css, palette, values)
        if (ground.colour === null) {
          // The surface's own paint is refused above, with the rect, and the label's line would
          // repeat it; a ground this reading cannot find at all is its own refusal.
          if (ground.why !== null) note(text, ground.why)
          continue
        }
        const ratio = contrastRatio(fill.rgb, ground.colour.rgb)
        if (ratio >= LABEL_CONTRAST) continue
        note(
          text,
          `it is drawn in ${named(fill)} on ${named(ground.colour)}, which is ${ratio.toFixed(1)}:1 — under the ${LABEL_CONTRAST}:1 the site's own text is held to (WCAG 2.1 AA, \`website/test/a11y.mjs\`), and a label a reader cannot read is a label the drawing does not say`,
        )
      }

      for (const [key, group] of groups) {
        out.push(`${where} draws ${group.count} ${key} ${group.count === 1 ? 'shape' : 'shapes'}, and ${[...group.lines].join('; ')}`)
      }
    }
  }
  return out
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
    ['how-it-works/index.html'],
    'the run diagram is not on How It Works and only there: the owner\'s draft of 2026-10-19 puts it on that page, and the owner\'s ruling of 2026-10-20 retires the homepage\'s own four-step flow, so the site\'s one picture of a run is this one (website/test/home.mjs reads the homepage\'s slider, the block in the flow\'s place)',
  )
  for (const page of diagrammed) {
    const failures = runDiagramFailures(page.html, pageCss(page.html))
    assert.deepEqual(failures, [], `${page.rel}: the run diagram is not the one the owner's draft of 2026-10-19 asks for:\n  ${failures.join('\n  ')}`)
  }
})

test('every shape of the run drawing is painted from the palette, and every label is legible where it is drawn', () => {
  // The reading the owner's own finding of 2026-10-21 asks for: the drawing was a series of
  // black rectangles, and every gate in this file passed over it, because they read the drawing
  // and none of them read the paint.
  const page = builtPages().find((entry) => entry.rel === 'how-it-works/index.html')
  assert.ok(page, 'the built site has no How It Works page, and the run\'s drawing is on it')
  const failures = runPaintFailures(page.html, pageCss(page.html))
  assert.deepEqual(failures, [], `${page.rel}: the run drawing is not painted the way the site paints:\n  ${failures.join('\n  ')}`)
})

test('every paint refusal fires on the defect it names', (t) => {
  // The reading's own proof, and the defect of 2026-10-21 is its first case: the page as it is
  // built passes, and each fixture that breaks one thing about the paint makes exactly that
  // thing's line appear.
  const page = readFileSync(path.join(DIST, 'how-it-works', 'index.html'), 'utf8')
  const css = pageCss(page)
  const verdict = (stylesheet, html = page) => runPaintFailures(html, stylesheet)
  assert.deepEqual(verdict(css), [], 'the built page does not pass the reading its own fixtures are drawn from')

  // **The defect as it shipped.** Astro appends the component's scope attribute to every
  // compound of a scoped selector, so `.run-card` was built as `.run-card[data-astro-cid-…]` —
  // and the shapes, which arrive through `set:html`, carry no scope attribute for that to
  // match. This plant rebuilds exactly that shape out of the page's own stylesheet, which is
  // why the fix is read here and not in the source: the source's `.run-diagram :global(.run-card)`
  // and the dead `.run-card[data-astro-cid-…]` are one edit apart.
  const scope = /\.run-diagram\[(data-astro-cid-\w+)\]/.exec(css)?.[1]
  assert.ok(scope, 'the page\'s stylesheet carries no rule scoped to the drawing\'s own component, and the plant below rebuilds the dead shape from one')
  const rescoped = css.replace(new RegExp(`\\.run-diagram\\[${scope}\\] \\.run-([\\w-]+)`, 'g'), '.run-$1[' + scope + ']')
  assert.notEqual(rescoped, css, 'the plant did not change the page\'s stylesheet, so the case below proves nothing about the scoped shape')

  // The same defect one declaration at a time. Each plant rewrites the page's *own* rule for a
  // class — found by the class and the property, not by a selector typed in here, so a fix that
  // renames a rule cannot leave a fixture pointing at nothing.
  const rule = (klass) => new RegExp(`(\\.run-diagram\\[${scope}\\] \\.${klass}(?![\\w-])[^{}]*\\{[^{}]*?\\b(?:fill|stroke)\\s*:\\s*)([^;}]+)`)
  const repaint = (klass, value) => {
    const changed = css.replace(rule(klass), `$1${value}`)
    assert.notEqual(changed, css, `the page's stylesheet no longer paints \`.${klass}\` with a declaration this fixture can rewrite`)
    return changed
  }
  // And the same colour written where it cannot be one: a `var()` is CSS's own syntax, an SVG
  // presentation attribute is not CSS, and a shape whose only paint is that attribute falls
  // back to SVG's initial value. The rule that paints the card is taken out so that the
  // attribute is the paint in force, which is what a fix written in the markup would leave.
  const noFillRule = css.replace(new RegExp(`(\\.run-diagram\\[${scope}\\] \\.run-card\\{[^{}]*?)fill:[^;]+;`), '$1')
  assert.notEqual(noFillRule, css, 'the page\'s stylesheet no longer paints `.run-card` with a `fill` this fixture can take out')
  const attributeVar = page.replace(/<rect class="run-card"/, '<rect fill="var(--surface)" class="run-card"')
  // And a shape of a kind this reading has not been taught: the drawing is rect, path, line and
  // text today, and a `<polygon>` added to it tomorrow would otherwise be the one shape no check
  // holds — which is how the drawing shipped unpainted in the first place.
  const unknownShape = page.replace(/<line /, '<polygon points="0,0 4,0 2,4" ')
  assert.notEqual(unknownShape, page, 'the drawing no longer holds a `<line>` this fixture can rebuild as a shape the reading does not know')
  // And the other half of the same defect: a shape whose every declared paint is `none` is drawn
  // as nothing, and the arrows are stroke alone — `stroke: none` over them is an arrow no reader
  // sees while every rule that paints it still reads in the stylesheet as one that is there.
  const nothing = repaint('run-arrow-line', 'none')
  // And a label whose ground this reading cannot find: the box the drawing puts it in, and the
  // card inside that box. A contrast check with nothing to compute against is not a check, so
  // both are refused rather than passed over.
  const unboxed = page.replace('<g class="run-box" data-box="1">', '<g data-box="1">')
  assert.notEqual(unboxed, page, 'the drawing no longer opens with a box this fixture can take the class off')
  const uncarded = page.replace(/<rect class="run-card" data-pad="18" x="16\.0"/, '<rect class="run-plate" data-pad="18" x="16.0"')
  assert.notEqual(uncarded, page, 'the drawing no longer draws a card in its first box, and this fixture takes the class off one')

  const cases = [
    ['the scoped paint rules the drawing shipped with (2026-10-21)', { html: page, css: rescoped }, /no rule of the page's stylesheet can reach it/],
    ['a card painted with a literal the palette does not hold', { html: page, css: repaint('run-card', '#000000') }, /which is not one of the palette's own colours/],
    ['a card painted through a var() in its presentation attribute', { html: attributeVar, css: noFillRule }, /`var\(\)` is not a colour in an SVG presentation attribute/],
    ['a shape whose every declared paint is none', { html: page, css: nothing }, /and no colour at all, so it is drawn as nothing/],
    ['a label painted on the surface it is drawn on', { html: page, css: repaint('run-small', 'var(--border)') }, /under the 4\.5:1 the site's own text is held to/],
    ['a label drawn in a colour no contrast check can compute', { html: page, css: repaint('run-name', 'currentColor') }, /this reading cannot turn into a colour/],
    ['a label the drawing puts in no box at all', { html: unboxed, css }, /drawn outside any box of the drawing/],
    ['a label in a box that carries no surface', { html: uncarded, css }, /carries neither a card .* nor a stage pill/],
    ['a shape of a kind the reading has not been taught', { html: unknownShape, css }, /knows how a drawing's .* are painted and nothing else/],
  ]
  for (const [what, fixture, wanted] of cases) {
    const failures = verdict(fixture.css, fixture.html)
    assert.ok(
      failures.some((line) => wanted.test(line)),
      `${what}: the reading passed over it — ${wanted} is not among the lines it gave (${failures.length === 0 ? 'it gave none' : failures.join('; ')})`,
    )
  }
  // And the page as it is built is still the page that passes, so the plants above are the only
  // thing that changed.
  assert.deepEqual(verdict(css), [], 'the reading refuses the built page after its own fixtures ran')
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
      'a diagram that lost the wide drawing',
      // The wide drawing alone taken out, the stacked one left: above the width the two change
      // over at the stacked drawing is `display: none`, so the reader a desktop is gets no run.
      // Only one drawing is displayed at a time, which is why "one drawing is still there" is
      // not the same page as "the run is drawn at every width".
      page.replace(/<svg class="run-svg run-wide"[\s\S]*?<\/svg>/, ''),
      /carries no wide drawing/,
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
  // `[data-astro-cid-…]`, so the rule is found rather than spelled out here. A rule's selector is
  // read to its end rather than assumed to be the class alone: the paint rules of this drawing are
  // `.run-diagram[cid] .run-card`, and a reader that wanted the class to sit next to the brace
  // would find none of them.
  const ruleText = (klass) => {
    const match = new RegExp(`(?:^|[},])\\s*([^{}]*\\.${klass}(?![\\w-])[^{}]*\\{([^}]*)\\})`).exec(css)
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
    // The list of facts beside the picture, drawn with no marker on its items: the run-on
    // paragraph the owner read (their feedback of 2026-10-21), and the defect every gate in
    // this file passed over while the list was the page's own `<ul>` with `list-style: none`.
    [
      'a list of facts with no bullet on its items',
      // The declaration is **added** to the page's own rule rather than swapped into it: the
      // build's CSS minifier rewrites the shorthand it was written as (`list-style: disc` is
      // built as `list-style: outside`, since `disc` is `list-style-type`'s initial value), so
      // a fixture that typed the source's own value in would stop landing the moment the
      // minifier changed its mind. The added declaration is the last one in the rule's body,
      // which is the one in force.
      declare('run-also', 'list-style:none'),
      /asks for a bullet point on each of its items/,
    ],
    // The same defect written the long way: the marker is taken away by the longhand rather than
    // by the shorthand, which is the pair the reading above has to hold together — a shorthand
    // that names a marker and a `list-style-type: none` after it leave the browser nothing to
    // draw, and a reading that stopped at the shorthand would call that list bulleted.
    [
      'a list of facts whose marker the longhand takes away',
      declare('run-also', 'list-style-type:none'),
      /leaves `list-style-type: none` in force/,
    ],
    // And the same defect again, with the marker named on the list and the rows laid out as
    // something that is not a list item: the board draws these rows as rows, and a row the
    // browser blockifies is a row with no marker, whatever the list's own `list-style` reads.
    [
      'rows of the list laid out as flex rows',
      declare('run-also-item', 'display:flex'),
      /rows of the list beside the picture are `display: flex`/,
    ],
    [
      'a list of facts laid out as a flex row itself',
      declare('run-also', 'display:flex'),
      /the list of the facts beside the picture is `display: flex`/,
    ],
    // And the marker named, on a list that holds no room for it: the defect the owner read twice
    // (their feedback of 2026-10-22) — every gate above passes over it, because the list does
    // declare a marker and its rows are still list items. This is the page as it was built before
    // this fix, and the room is taken back the way the built stylesheet writes it: the logical
    // longhand added after the `padding: 0` that is already there.
    [
      'a list of facts with no room for its bullets',
      declare('run-also', 'padding-inline-start:0'),
      /holds 0 px of inline-start padding for the markers of its items/,
    ],
    // The same defect in the other direction: room held, and not the room a bullet needs. The
    // floor is the site's own list inset, so a list that holds less than the site's lists do is
    // refused rather than measured against a number this check invented.
    [
      'a list of facts holding less room than the site\'s own lists',
      declare('run-also', 'padding-inline-start:0.25rem'),
      /holds 4 px of inline-start padding for the markers of its items/,
    ],
    // And a room nothing can measure: a length this check cannot turn into pixels is a room no
    // check has read, which is refused rather than read as enough.
    [
      'a list of facts whose room cannot be measured',
      declare('run-also', 'padding-inline-start:1em'),
      /cannot turn into pixels/,
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
    // And the wide drawing off where no query asks for it: the query's own `display: none`
    // written onto the base rule as well, which is the changeover undone in the one direction
    // the tall rule cannot see — the stacked drawing is still turned off above that width, so
    // the two rules together leave a desktop reader with no drawing at all. The rule is written
    // in front of the query rather than found and rewritten, because the only `.run-wide` rule
    // the page carries is inside it.
    [
      'a wide drawing turned off at every width',
      replaceOnce('@media (width<=59.25rem){', '.run-wide{display:none}@media (width<=59.25rem){'),
      /the wide drawing is `display: none`/,
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

  // The one shape a list needs no room of its own for: a marker drawn *inside* the row's content
  // box is a marker the panel's clip cannot reach, so a list that takes that shape is let through
  // — a reading that refused it would be a reading refusing a shape the page is free to take, and
  // the room below is read because an outside marker is drawn outside its item.
  assert.deepEqual(
    verdict(page, declare('run-also', 'list-style-position:inside')).filter((line) => /inline-start padding|cannot turn into pixels/.test(line)),
    [],
    'a list whose marker is drawn inside its items holds no inline-start padding for one, and this reading refuses it anyway: the room is read because an outside marker is drawn outside the row, and a marker drawn inside the row is one the panel\'s clip cannot reach',
  )

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

// ---- the four levels (Part 4; ZF36–ZF43, D237, D244, D245) --------------------------------

/**
 * The zoom section of a built page, as everything the four levels are read through: the one
 * `<defs>`, the `<symbol>`s it holds, the four `<figure>`s with the `<use>` references and the
 * words each one draws, and the markers of the level `<nav>`.
 *
 * The levels are read from the page rather than from the component, because the defect this
 * whole reading exists for is a level that drew an entity itself: a test that asked the
 * component what it draws would be asking the thing under suspicion.
 */
function zoomLevelsOf(html) {
  const sections = findAll(parse(html), (el) => el.name === 'section' && has(el, 'zoom'))
  const section = sections[0] ?? null
  const zoom = { sections: sections.length, section, defs: [], symbols: [], levels: [], markers: [] }
  if (section === null) return zoom

  zoom.defs = findAll(section, (el) => el.name === 'defs')
  for (const symbol of findAll(section, (el) => el.name === 'symbol')) {
    const id = symbol.attrs.get('id') ?? ''
    zoom.symbols.push({
      id,
      // A `<symbol>` is a shape of the one `<defs>` when the `<defs>` is one of its ancestors:
      // a symbol written beside a level is a shape no level draws through `<use>`.
      inDefs: zoom.defs.some((defs) => ancestorsOf(symbol).includes(defs)),
      node: symbol,
    })
  }

  const nav = findAll(section, (el) => el.name === 'nav')[0]
  if (nav !== undefined) {
    for (const marker of findAll(nav, (el) => el.name === 'a')) {
      zoom.markers.push({
        href: marker.attrs.get('href') ?? '',
        name: (marker.attrs.get('aria-label') ?? '').trim(),
        text: textOf(marker).trim(),
        node: marker,
      })
    }
  }

  for (const figure of findAll(section, (el) => el.name === 'figure' && has(el, 'zoom-figure'))) {
    const svg = [...walk(figure)].find((el) => el.name === 'svg')
    const inside = svg === undefined ? [] : [...walk(svg)]
    const paths = inside.filter((el) => el.name === 'path')
    zoom.levels.push({
      id: figure.attrs.get('id') ?? '',
      svg,
      kinds: inside.map((el) => el.name),
      uses: inside.filter((el) => el.name === 'use').map((el) => ({ href: el.attrs.get('href') ?? '', node: el })),
      texts: inside.filter((el) => el.name === 'text').map((el) => textOf(el).trim()),
      // The shapes a level draws loose — the boxes it sits its cards in, and the arrows between
      // them. A `<path>` of a level is a join and not a shape: it is the arrow between two
      // cards, it carries the class below, and the reading refuses one that does not.
      boxes: inside.filter((el) => el.name === 'use').map((el) => {
        const [x, y, w, h] = ['x', 'y', 'width', 'height'].map((name) => Number(el.attrs.get(name)))
        return { href: el.attrs.get('href') ?? '', x, y, w, h }
      }),
      arrows: paths.filter((el) => has(el, 'zoom-arrow')).map((el) => el.attrs.get('d') ?? ''),
      ownPaths: paths.filter((el) => !has(el, 'zoom-arrow')).length,
      texts_: inside.filter((el) => el.name === 'text'),
      // The level's name, read from the span the `<figcaption>` writes, and the board's own
      // title and subtitle, which the drawing carries as `<title>` and `<desc>`.
      name: findAll(figure, (el) => has(el, 'zoom-level-name')).map((el) => textOf(el).trim()).join(' ').trim(),
      title: inside.filter((el) => el.name === 'title').map((el) => textOf(el).trim()).join(' ').trim(),
      desc: inside.filter((el) => el.name === 'desc').map((el) => textOf(el).trim()).join(' ').trim(),
    })
  }
  return zoom
}

/** The kinds a level may hold: the reference that draws an entity, the words that name it, the
 *  plain joins between two parts, and the containers they sit in. A join is a `<line>` or a
 *  `<path>` — the boards bend four of their arrows and draw one of them double-headed, so a
 *  join is a path — and a `<path>` is allowed only when it carries the join's own class: a
 *  shape drawn as a path is a copy of an entity's geometry like any other, which is the defect
 *  the class is there to name. */
const ZOOM_REFERENCE = new Set(['use'])
const ZOOM_WORDS = new Set(['text'])
const ZOOM_JOINS = new Set(['line', 'path'])
const ZOOM_CONTAINERS = new Set(['svg', 'g', 'title', 'desc'])

/** The kinds that ARE an entity's geometry: a level holding one of these has drawn a copy of a
 *  shape the one `<defs>` exists to hold (D244). */
const ZOOM_GEOMETRY = new Set(['rect', 'circle', 'ellipse', 'path', 'polygon', 'polyline', 'marker', 'symbol', 'image', 'foreignObject'])

/**
 * Every way the four levels break the same-shape rule (D244), as the lines a reader of the
 * failure gets: a `<use>` that resolves to nothing, a `<symbol>` no level draws, and a level
 * that holds a shape of its own.
 *
 * **Why this is the rule and not a nicety.** The four levels are one sequence because a job is
 * the same tile at every level; that is what makes the zoom legible, and no gate can see an
 * image. `<use>` turns "the same shape" into a text assertion, and this is the assertion.
 */
export function zoomShapeFailures(html) {
  const out = []
  const zoom = zoomLevelsOf(html)
  if (zoom.sections === 0) {
    return ['the page renders no zoom section, and the four levels are a section on How It Works under "The run, end to end" (D237)']
  }
  if (zoom.sections > 1) {
    out.push(`the page renders ${zoom.sections} zoom sections, and the four levels are one section under "The run, end to end" (D237)`)
  }
  if (zoom.defs.length !== 1) {
    out.push(
      `the zoom section holds ${zoom.defs.length} <defs> blocks, and the four levels share exactly one: one <symbol> per entity, each drawn once, and every level drawing it through <use href="#…"> (D244)`,
    )
  }
  const ids = zoom.symbols.map((symbol) => symbol.id)
  if (ids.some((id) => id === '')) out.push('a <symbol> of the zoom drawing carries no id, and an entity with no id is an entity no level can draw through <use> (D244)')
  if (new Set(ids).size !== ids.length) {
    out.push(`the zoom drawing holds two <symbol>s with the same id (${ids.join(', ')}): a second <use> for one id draws whichever the browser resolves first, which is a shape that changes with the markup order`)
  }
  for (const symbol of zoom.symbols) {
    if (!symbol.inDefs) {
      out.push(
        `the <symbol> ${JSON.stringify(symbol.id)} is not inside the zoom section's one <defs>, and the entities of the four levels are one <defs> of <symbol>s (D244): a shape written anywhere else is a second place the geometry lives`,
      )
    }
  }

  const drawn = new Set()
  for (const level of zoom.levels) {
    const where = `the level ${JSON.stringify(level.id)}`
    for (const use of level.uses) {
      const id = use.href.startsWith('#') ? use.href.slice(1) : ''
      if (id === '' || !ids.includes(id)) {
        out.push(
          `${where} draws \`<use href="${use.href}">\`, and the zoom section's one <defs> holds no such <symbol>: a reference that resolves to nothing draws nothing at all, and the level would be a caption with an empty picture (D244)`,
        )
        continue
      }
      drawn.add(id)
    }
    if (level.ownPaths > 0) {
      out.push(
        `${where} holds ${level.ownPaths} \`<path>\` of its own that is not one of its arrows, and a level draws an entity only through \`<use href="#…">\` (D244): a shape written into the level as a path is a second copy of an entity's geometry, and the first edit to one copy is the drift the four levels exist not to have`,
      )
    }
    for (const kind of [...new Set(level.kinds)].sort()) {
      if (ZOOM_REFERENCE.has(kind) || ZOOM_WORDS.has(kind) || ZOOM_JOINS.has(kind) || ZOOM_CONTAINERS.has(kind)) continue
      if (ZOOM_GEOMETRY.has(kind)) {
        out.push(
          `${where} holds a \`<${kind}>\` of its own, and a level draws an entity only through \`<use href="#…">\` (D244): a shape written into the level is a second copy of an entity's geometry, and the first edit to one copy is the drift the four levels exist not to have`,
        )
        continue
      }
      out.push(
        `${where} holds a \`<${kind}>\`, and this reading knows a level's \`<use>\`, \`<text>\`, its arrows and its own groups and nothing else: an element it cannot read is an element whose geometry nothing holds, and the way this drawing grows is a level that draws its own shapes again (D244, ZF41)`,
      )
    }
  }
  for (const symbol of zoom.symbols) {
    if (symbol.id !== '' && !drawn.has(symbol.id)) {
      out.push(
        `the one <defs> holds the <symbol> ${JSON.stringify(symbol.id)} and no level draws it: an entity no level uses is a shape nothing in the drawing holds, and it drifts from the levels that were meant to share it (D244)`,
      )
    }
  }
  return out
}

/**
 * The surface a level's label is drawn on: the `background` the page's stylesheet gives the
 * `<svg>` the label is inside, or the nearest ancestor that declares one. A label's ground is
 * read from the page's own rules the way the paint is, so the pair the contrast reading computes
 * is a pair the site decided.
 */
function zoomGround(text, css, palette, values, symbols = []) {
  for (const el of [text, ...ancestorsOf(text)]) {
    for (const rule of cssRules(css)) {
      const selector = (rule.selector ?? '').trim()
      if (selector.startsWith('@') || (rule.conditions ?? []).length > 0) continue
      if (!ruleReaches(selector, el)) continue
      for (const [property, value] of cssDeclarations(rule.body ?? '')) {
        if (property !== 'background' && property !== 'background-color') continue
        const colour = painted({ value: value.trim(), from: `\`${selector}\``, source: 'rule' }, palette, values)
        if (colour !== null) return { colour, why: null }
      }
    }
    // And the card a label is written in: a level draws its cards as `<use>` references, so a
    // label inside one is drawn on the fill of the `<symbol>` that reference names rather than
    // on the page under the drawing. The first reference of the group that resolves to a
    // `<rect>` with a fill is the card; a reference to the person drawn in a developer card has
    // no rect at all and is passed over.
    for (const use of [...walk(el)].filter((node) => node.name === 'use')) {
      const id = (use.attrs.get('href') ?? '').replace(/^#/, '')
      const symbol = symbols.find((entry) => entry.id === id)
      if (symbol === undefined) continue
      const shape = [...walk(symbol.node)].find((node) => node.name === 'rect')
      if (shape === undefined) continue
      const colour = painted(paintInForce(css, shape, 'fill'), palette, values)
      if (colour !== null) return { colour, why: null }
    }
  }
  return {
    colour: null,
    why: 'no rule of the page states a surface under it: a level\'s labels are drawn on the panel its `<svg>` carries (`background` in the page\'s stylesheet), and a label whose ground no rule names is a label no contrast pair can be computed from — which is a contrast check that is not running',
  }
}

/**
 * The board colours the drawing's own stylesheet names: every `--zoom-*` custom property the
 * page declares outside a conditional block, by the name the shape's `var()` uses. They are the
 * boards' colours rather than the palette's, which is why they are read here rather than in
 * `website/src/styles/palette-logo.css`: the drawing paints with them, so the reading that
 * resolves a shape's paint has to know them, and a colour no rule of the page states is refused
 * below as a literal nothing decides.
 */
function boardTokens(css) {
  const out = new Map()
  for (const rule of cssRules(css)) {
    const selector = (rule.selector ?? '').trim()
    if (selector.startsWith('@') || (rule.conditions ?? []).length > 0) continue
    for (const [name, value] of cssDeclarations(rule.body ?? '')) {
      if (!name.startsWith('--zoom-')) continue
      out.set(name.replace(/^--/, ''), value.trim())
    }
  }
  return out
}

/**
 * Every way the four levels' paint is not the site's, as the lines a reader of the failure gets:
 * a shape no rule reaches, a shape painted with something the palette does not hold, a `var()` in
 * a presentation attribute (where it is not a colour at all), and a label whose colour against
 * the surface it is drawn on is under the ratio the site's text is held to.
 *
 * The shapes of the one `<defs>` are read where they stand, which is what a `<use>` draws: a
 * `<symbol>` whose shapes are unpainted is four empty levels, and reading the reference instead
 * of the shape would find a `<use>` that is painted by nothing and say nothing about it.
 */
export function zoomPaintFailures(html, css, palette = paletteTokensInForce()) {
  const out = []
  // The boards' own colours are declared on the section as `--zoom-*`, and they are read here
  // the way the palette is: a colour a drawing paints is a value the page's stylesheet states,
  // and a value it does not state is a literal nothing decides. The palette is the site's
  // colours and the board's are the drawing's, so both are read and a shape may be painted with
  // either — a card with `var(--zoom-card)` and a label with `var(--text)` are both the page's.
  const board = boardTokens(css)
  const all = new Map([...palette, ...board])
  const values = new Set([...all.values()].map((value) => value.toLowerCase()))
  const zoom = zoomLevelsOf(html)
  if (zoom.section === null) {
    return ['the page renders no zoom section, and the four levels are the section on How It Works under "The run, end to end" (D237)']
  }

  // One line per kind of shape rather than one per shape: a level draws the same tile several
  // times, and a reader of the failure wants the rule to fix.
  const groups = new Map()
  const note = (el, line) => {
    const key = `<${el.name} class="${classes(el).join(' ')}">`
    if (!groups.has(key)) groups.set(key, { count: 0, lines: new Set() })
    const group = groups.get(key)
    group.count += 1
    group.lines.add(line)
  }

  for (const svg of findAll(zoom.section, (el) => el.name === 'svg')) {
    for (const shape of walk(svg)) {
      for (const line of paintRefusals(shape, css, all, values)) note(shape, line)
    }
    for (const text of [...walk(svg)].filter((el) => el.name === 'text')) {
      const fill = painted(paintInForce(css, text, 'fill'), all, values)
      if (fill === null) continue // its paint is refused above, where every shape is read
      const ground = zoomGround(text, css, all, values, zoom.symbols)
      if (ground.colour === null) {
        note(text, ground.why)
        continue
      }
      const ratio = contrastRatio(fill.rgb, ground.colour.rgb)
      if (ratio >= LABEL_CONTRAST) continue
      note(
        text,
        `it is drawn in ${named(fill)} on ${named(ground.colour)}, which is ${ratio.toFixed(1)}:1 — under the ${LABEL_CONTRAST}:1 the site's own text is held to (WCAG 2.1 AA, \`website/test/a11y.mjs\`), and a label a reader cannot read is a label the level does not say`,
      )
    }
  }

  for (const [key, group] of groups) {
    out.push(`the zoom drawing draws ${group.count} ${key} ${group.count === 1 ? 'shape' : 'shapes'}, and ${[...group.lines].join('; ')}`)
  }
  return out
}

/**
 * The strings one board prints, read out of the design-ref file the level was drawn from — the
 * same reader the component's build-time check uses (`src/lib/zoom-boards.mjs`), so that this
 * reading and the page cannot be looking at two different boards. The boards are the source of
 * truth for the four levels (the brief of 2026-10-22), and the file is read here rather than
 * taken from the page for the reason a test reads a document rather than the page's copy of it:
 * a reading that took its strings from the page could not see a string the page dropped.
 */
const boardWords = (id) => boardStrings(BOARDS.find((board) => board.id === id).file)

/**
 * Every way the four levels' words are not the boards', as the lines a reader of the failure
 * gets, read in both directions: a string a board prints that its level draws nowhere, and a
 * line a level draws that is no string of its board.
 *
 * **Why both directions.** The plan asks for the labels as the boards' own so that the drawing
 * and the approved boards cannot drift. A test that read only the page would hold the page's
 * lines to nothing and miss a caption the drawing dropped; a test that read only the boards would
 * miss a line the component wrote for itself. The two halves are the same reading of the same
 * files, and the level's own `<title>` is checked against its board's name as well, so a level
 * drawn from another board's strings is a failure rather than a silent swap.
 *
 * A drawn line is held to its board's whole text rather than to the board's line it came from,
 * because the board's own line breaks are the card's business and not a fact about the words: the
 * containment is the loose reading, and the other direction is what catches a word changed.
 */
export function zoomBoardFailures(html) {
  const out = []
  const zoom = zoomLevelsOf(html)
  if (zoom.section === null) {
    return ['the page renders no zoom section, and the four levels are the section on How It Works under "The run, end to end" (D237)']
  }
  for (const board of BOARDS) {
    if (!zoom.levels.some((level) => level.id === board.id)) {
      out.push(`the page draws no level ${JSON.stringify(board.id)}, and every board of ${board.file.replace(/level\d\.dc\.html$/, 'level*.dc.html')} is a level of the drawing (D245)`)
    }
  }
  for (const level of zoom.levels) {
    const board = BOARDS.find((entry) => entry.id === level.id)
    const where = `the level ${JSON.stringify(level.id)}`
    if (board === undefined) {
      out.push(`${where} is drawn and no board of ${BOARDS.map((entry) => entry.file).join(', ')} is it: a level of the drawing is one of the owner's four boards (D245)`)
      continue
    }
    const words = boardWords(level.id)
    const printed = comparable(words.join('\n'))
    for (const text of level.texts) {
      if (!printed.includes(comparable(text))) {
        out.push(
          `${where} draws ${JSON.stringify(text)}, which is not a string of ${board.file}: every word of a level is the board's own, so that the drawing and the approved board cannot drift (the brief of 2026-10-22). Either the line was rewritten or the board moved it.`,
        )
      }
    }
    const drawn = comparable(level.texts.join('\n'))
    for (const word of words) {
      if (!drawn.includes(comparable(word))) {
        out.push(
          `${board.file} prints ${JSON.stringify(word)} and ${where} draws no such text: the boards are the source of truth for the four levels, so a string of a board that the drawing dropped is a failure here rather than a silence (the brief of 2026-10-22).`,
        )
      }
    }
    if (comparable(level.title) !== comparable(board.name)) {
      out.push(
        `${where} is titled ${JSON.stringify(level.title)} and its board is ${JSON.stringify(board.name)}: a level's title is its board's name (D245), and a level drawn from another board's strings is the drift this reading exists to catch.`,
      )
    }
    if (comparable(level.desc) === '') {
      out.push(`${where} carries no <desc>, and a drawing that says what it is to a reader who cannot see it is what the levels are (Part 4)`)
    }
  }
  return out
}

/**
 * The box a `<text>` of the drawing occupies, from the attributes the drawing wrote: the width is
 * the component's own per-character estimate, the height is the em box around the baseline. The
 * drawing lays every label out with the same estimate, so this measures the box the browser will
 * paint rather than a box this reading invented — which is what makes an overlap here a defect
 * the owner would see.
 */
const ZOOM_CHAR = 0.55
const textBoxOf = (el) => {
  const num = (name, fallback) => {
    const value = el.attrs.get(name)
    return value === undefined ? fallback : Number(value)
  }
  const size = num('font-size', 16)
  const x = num('x', 0)
  const y = num('y', 0)
  const anchor = el.attrs.get('text-anchor') ?? 'start'
  const width = textOf(el).length * ZOOM_CHAR * size
  const left = anchor === 'middle' ? x - width / 2 : anchor === 'end' ? x - width : x
  return { left, right: left + width, top: y - size * 0.8, bottom: y + size * 0.2 }
}

/** The drawing's own board, as the viewBox the levels are drawn in: 1280x720 (the brief of 2026-10-22). */
const ZOOM_W = 1280
const ZOOM_H = 720

/**
 * Every way a label of the four levels is drawn over another or outside its board: the two things
 * a text measurement cannot be trusted to have got right, and the two things the owner would see
 * in the preview. The boxes are the ones `textBoxOf` computes from the drawing's own attributes,
 * so a label the drawing moved is measured where it was moved to.
 *
 * Two boxes are held to overlap only when they share more than half a pixel in both directions,
 * which is the tolerance the rounded attributes deserve; a box that leaves `0 0 1280 720` at all
 * is a failure, because the viewBox is the board's own frame and a label outside it is cropped.
 */
export function zoomLabelOverlapFailures(html) {
  const out = []
  const zoom = zoomLevelsOf(html)
  for (const level of zoom.levels) {
    const boxes = level.texts_.map((el) => ({ el, ...textBoxOf(el) }))
    for (const box of boxes) {
      const text = JSON.stringify(textOf(box.el))
      if (box.left < 0 || box.top < 0 || box.right > ZOOM_W || box.bottom > ZOOM_H) {
        out.push(
          `the level ${JSON.stringify(level.id)} draws ${text} at (${box.left.toFixed(1)}, ${box.top.toFixed(1)})–(${box.right.toFixed(1)}, ${box.bottom.toFixed(1)}), which leaves the board 0 0 ${ZOOM_W} ${ZOOM_H}: a label outside the viewBox is a label the owner cannot read.`,
        )
      }
    }
    for (let i = 0; i < boxes.length; i += 1) {
      for (let j = i + 1; j < boxes.length; j += 1) {
        const a = boxes[i]
        const b = boxes[j]
        const across = Math.min(a.right, b.right) - Math.max(a.left, b.left)
        const down = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top)
        if (across > 0.5 && down > 0.5) {
          out.push(
            `the level ${JSON.stringify(level.id)} draws ${JSON.stringify(textOf(a.el))} over ${JSON.stringify(textOf(b.el))} — their boxes share ${across.toFixed(1)}x${down.toFixed(1)} units, and no label of a level is drawn over another (the brief of 2026-10-22).`,
          )
        }
      }
    }
  }
  return out
}

/** The points an arrow's path data names: the drawing writes its arrows as `M x y H|V n`, one
 *  straight run from a card to a card, which is what makes an endpoint checkable at all. */
function arrowPoints(d) {
  const match = /^M\s*(-?[\d.]+)[ ,]+(-?[\d.]+)\s*([HV])\s*(-?[\d.]+)$/.exec(d.trim())
  if (match === null) return null
  const [, x, y, axis, end] = match
  const start = { x: Number(x), y: Number(y) }
  return { start, stop: axis === 'H' ? { x: Number(end), y: start.y } : { x: start.x, y: Number(end) } }
}

/** Whether a point is within `ZOOM_EDGE` of the edge of one of the level's boxes. */
const ZOOM_EDGE = 2
function touchesBox(point, boxes) {
  return boxes.some((box) => {
    const inside = point.x >= box.x - ZOOM_EDGE && point.x <= box.x + box.w + ZOOM_EDGE
    const beside = point.y >= box.y - ZOOM_EDGE && point.y <= box.y + box.h + ZOOM_EDGE
    const onEdgeX = Math.abs(point.x - box.x) <= ZOOM_EDGE || Math.abs(point.x - (box.x + box.w)) <= ZOOM_EDGE
    const onEdgeY = Math.abs(point.y - box.y) <= ZOOM_EDGE || Math.abs(point.y - (box.y + box.h)) <= ZOOM_EDGE
    return inside && beside && (onEdgeX || onEdgeY)
  })
}

/**
 * Every way an arrow of the four levels starts or ends in mid-air: an endpoint more than two
 * units from the edge of a card, and more than two units from the corner of another arrow of its
 * own chain. An arrow that points at nothing is the defect this reading is for — the owner sees a
 * line that stops short of the box it explains, and no test of the words can see it.
 *
 * The boxes are the `<use>` elements the level draws, which is where every card of the drawing is
 * (D244): a level draws its entities through the shared symbols and nothing else, so an arrow's
 * endpoints are held to the same shapes a reader sees.
 */
export function zoomArrowFailures(html) {
  const out = []
  const zoom = zoomLevelsOf(html)
  for (const level of zoom.levels) {
    const arrows = level.arrows.map((d) => ({ d, points: arrowPoints(d) }))
    for (const arrow of arrows) {
      if (arrow.points === null) {
        out.push(
          `the level ${JSON.stringify(level.id)} draws the arrow ${JSON.stringify(arrow.d)}, which is not a straight run from a point (\`M x y H n\` or \`M x y V n\`): an arrow whose path this reading cannot parse is an arrow whose ends nobody has checked.`,
        )
        continue
      }
      for (const [end, point] of [['start', arrow.points.start], ['end', arrow.points.stop]]) {
        if (touchesBox(point, level.boxes)) continue
        const corner = arrows.some(
          (other) =>
            other !== arrow &&
            other.points !== null &&
            [other.points.start, other.points.stop].some(
              (at) => Math.abs(at.x - point.x) <= ZOOM_EDGE && Math.abs(at.y - point.y) <= ZOOM_EDGE,
            ),
        )
        if (corner) continue
        out.push(
          `the level ${JSON.stringify(level.id)} draws the arrow ${JSON.stringify(arrow.d)} whose ${end} at (${point.x}, ${point.y}) is more than ${ZOOM_EDGE} units from every card of the level and from every corner of its own chain: an arrow that touches nothing points at nothing (the brief of 2026-10-22).`,
        )
      }
    }
  }
  return out
}

/**
 * Every way the four levels are not four stills a reader with no script is given, as the lines a
 * reader of the failure gets: a `<script>` in the section, a level whose drawing or caption is
 * hidden, a level that draws no words, and the levels not in the boards' order.
 *
 * The built page is the whole of what this reads, which is the point: the site's one script is
 * `website/public/scripts/site.js` and nothing in this section needs it, so the four levels are
 * in the HTML a browser receives before any script runs.
 */
export function zoomStillFailures(html, levels = null) {
  const out = []
  const zoom = zoomLevelsOf(html)
  if (zoom.section === null) {
    return ['the page renders no zoom section, and the four levels are the section on How It Works under "The run, end to end" (D237)']
  }
  if (zoom.section !== null && findAll(zoom.section, (el) => el.name === 'script').length > 0) {
    out.push('the zoom section carries a <script>, and the four levels are stills: the pinned, scroll-driven zoom between them is Part 5, and a section that needs a script to be read is a section a reader with no script is not given (D245, ZF39)')
  }
  const hidden = (el) => el.attrs.get('hidden') !== undefined || el.attrs.get('aria-hidden') === 'true'
  for (const level of zoom.levels) {
    const where = `the level ${JSON.stringify(level.id)}`
    if (level.svg === undefined) {
      out.push(`${where} holds no <svg>, and every level of the four is a drawing in the page rather than a picture the browser fetches (D244)`)
      continue
    }
    if (hidden(level.svg)) out.push(`${where}'s drawing is hidden, and the four levels are all visible with no script (Part 4)`)
    if (level.texts.length === 0) out.push(`${where} draws no words, and a level says what it is in the repository's own words beside its shapes (Part 4)`)
  }
  if (levels !== null && zoom.levels.map((level) => level.id).join(' | ') !== levels.join(' | ')) {
    out.push(
      `the page draws the levels ${JSON.stringify(zoom.levels.map((level) => level.id))}, and the boards write them ${JSON.stringify(levels)}: the levels are in order — inside a job, the jobs fanned out, the developer's view, the whole project (D245)`,
    )
  }
  return out
}

// ---- the phone's reading of the four levels ----------------------------------------------

/** The width the brief of 2026-10-22 names for the phone reading, and the size the smallest
 *  label a phone keeps must still be drawn at there. */
const ZOOM_PHONE_WIDTH = 390
const ZOOM_PHONE_MIN = 10

/**
 * Every way the four levels are not read on a phone, as the lines a reader of the failure gets:
 * the figure with no floor under its width, a figure that does not scroll the floor it declares,
 * a label the phone keeps that the floor draws under the brief's 10 px, a label the phone drops
 * that the floor could have drawn, and a group of small print that is not hidden where the phone
 * reads the page.
 *
 * **Why the floor is the whole of the phone reading.** A board is 1280 units wide and its labels
 * carry the board's own sizes, so a drawing scaled to a 390 px column sets the boards' 14 px
 * labels at 4.3 px, which is not text. The drawing therefore keeps a floor (`min-width`) and
 * scrolls sideways inside its own box rather than shrinking past it, and the labels the floor
 * cannot draw at 10 px — the boards' 11, 12 and 13 px small print — are drawn inside
 * `<g class="zoom-fine">`, which the phone's own query hides. All three are read here against the
 * floor the page's stylesheet declares: the floor, the box that scrolls it and the small print can
 * only move together, so raise the floor and a hidden label is refused as one the phone could read,
 * lower it and a kept label is refused as one it cannot, and take the scrolling away and the
 * drawing a reader was meant to scroll is refused for leaving the column instead.
 */
export function zoomPhoneFailures(html, css) {
  const out = []
  const zoom = zoomLevelsOf(html)
  if (zoom.section === null) {
    return ['the page renders no zoom section, and the four levels are the section on How It Works under "The run, end to end" (D237)']
  }

  // The floor the phone gets: the `min-width` the stylesheet puts under the drawing, in a query
  // that holds at the brief's own width.
  let floor = null
  for (const rule of cssRules(css)) {
    const selector = rule.selector ?? ''
    if (!/\.zoom-svg/.test(selector)) continue
    for (const [property, value] of cssDeclarations(rule.body ?? '')) {
      if (property !== 'min-width') continue
      const px = lengthInPx(value, css)
      if (Number.isNaN(px)) {
        out.push(
          `\`${selector}\` is \`min-width: ${value}\`, which this reading cannot turn into pixels: the floor under the drawing is the width every label of the four levels is measured against on a phone, so a floor nothing can measure is a floor nothing has checked (the brief of 2026-10-22)`,
        )
        continue
      }
      if (!appliesAt(rule.conditions, ZOOM_PHONE_WIDTH)) {
        out.push(
          `\`${selector}\` keeps its \`min-width: ${value}\` in a query that does not hold at ${ZOOM_PHONE_WIDTH} px (${(rule.conditions ?? []).join(' ') || 'no query'}): a floor the phone does not get is a 1280-unit board scaled to the phone's column, which draws the boards' own labels at a third of their size (the brief of 2026-10-22)`,
        )
        continue
      }
      if (floor === null || px > floor) floor = px
    }
  }
  if (floor === null) {
    out.push(
      `the page's stylesheet puts no \`min-width\` under \`.zoom-svg\` at ${ZOOM_PHONE_WIDTH} px, and a 1280-unit board scaled to a phone's column draws the boards' own labels at a third of their size: the drawing keeps a floor and scrolls sideways inside its own box rather than shrinking below what a reader can read (the brief of 2026-10-22)`,
    )
    return out
  }

  // And the group the labels under that floor are drawn in is hidden there.
  let hidden = false
  for (const rule of cssRules(css)) {
    if (!/\.zoom-fine/.test(rule.selector ?? '')) continue
    if (!appliesAt(rule.conditions, ZOOM_PHONE_WIDTH)) continue
    for (const [property, value] of cssDeclarations(rule.body ?? '')) {
      if (property === 'display' && value.trim() === 'none') hidden = true
      if (property === 'visibility' && value.trim() === 'hidden') hidden = true
    }
  }
  if (!hidden) {
    out.push(
      `\`.zoom-fine\` is not hidden at ${ZOOM_PHONE_WIDTH} px, and the labels the ${floor} px floor cannot draw at ${ZOOM_PHONE_MIN} px are drawn inside it: the phone either hides them or draws them at a size no reader reads (the brief of 2026-10-22)`,
    )
  }

  // And the box the floor overflows into scrolls: the floor is wider than the phone's column, so
  // the drawing leaves the column and the figure is the box that has to carry it. A floor with
  // nothing scrolling it is a floor a reader never sees the far side of — the drawing is clipped
  // or it pushes the page sideways, and both are the phone reading failing at the other end.
  let scrolls = false
  for (const rule of cssRules(css)) {
    if (!/\.zoom-figure/.test(rule.selector ?? '')) continue
    if (!appliesAt(rule.conditions, ZOOM_PHONE_WIDTH)) continue
    for (const [property, value] of cssDeclarations(rule.body ?? '')) {
      const declared = value.trim()
      if (property === 'overflow-x' && (declared === 'auto' || declared === 'scroll')) scrolls = true
      if (property === 'overflow' && (declared === 'auto' || declared === 'scroll')) scrolls = true
    }
  }
  if (!scrolls) {
    out.push(
      `\`figure.zoom-figure\` does not scroll at ${ZOOM_PHONE_WIDTH} px, and the drawing it holds keeps a ${floor} px floor: the floor is wider than the phone's column, so the figure is the box that scrolls it — without one the drawing is clipped or the page itself moves sideways (the brief of 2026-10-22)`,
    )
  }

  for (const level of zoom.levels) {
    const where = `the level ${JSON.stringify(level.id)}`
    // The reader lowercases an attribute's name, as a browser does (`viewBox` is written
    // `viewbox` in what it hands back).
    const box = (level.svg?.attrs.get('viewbox') ?? '').trim().split(/\s+/).map(Number)
    const units = box.length === 4 && Number.isFinite(box[2]) && box[2] > 0 ? box[2] : null
    if (units === null) {
      out.push(
        `${where} declares \`viewBox="${level.svg?.attrs.get('viewbox') ?? '(none)'}"\`, and the width of a board is what its labels are scaled by: a drawing with no readable width is a drawing whose labels are measured against nothing (the brief of 2026-10-22)`,
      )
      continue
    }
    for (const text of level.texts_) {
      const label = JSON.stringify(textOf(text).trim())
      const size = Number(text.attrs.get('font-size'))
      if (!Number.isFinite(size)) {
        out.push(
          `${where} draws ${label} with no \`font-size\` this reading can read, and the size of a label is the whole of what the phone's floor is measured against (the brief of 2026-10-22)`,
        )
        continue
      }
      const rendered = (size * floor) / units
      const small = ancestorsOf(text).some((el) => has(el, 'zoom-fine'))
      if (small && rendered >= ZOOM_PHONE_MIN) {
        out.push(
          `${where} draws ${label} inside the group the phone hides, and at the ${floor} px floor a label the board sets at ${size} px renders at ${rendered.toFixed(1)} px: a label the phone could read is a label the phone keeps (the brief of 2026-10-22)`,
        )
        continue
      }
      if (!small && rendered < ZOOM_PHONE_MIN) {
        out.push(
          `${where} draws ${label} at ${size} px, and the ${floor} px floor the phone gets renders it at ${rendered.toFixed(1)} px: a label under the brief's ${ZOOM_PHONE_MIN} px is drawn inside \`<g class="zoom-fine">\`, which the phone's query hides, or the floor is raised to the width the label needs (the brief of 2026-10-22)`,
        )
      }
    }
  }
  return out
}

// ---- the gate ----------------------------------------------------------------------------

/** The built How It Works page, which is where the four levels are. */
function howItWorks() {
  const page = builtPages().find((entry) => entry.rel === 'how-it-works/index.html')
  assert.ok(page, 'the built site has no How It Works page, and the four zoom levels are a section of it (D237)')
  return page
}

test('the four levels draw one set of shapes, every one of them used, and no level draws its own', () => {
  const page = howItWorks()
  const failures = zoomShapeFailures(page.html)
  assert.deepEqual(failures, [], `${page.rel}: the four levels do not share one set of shapes (D244):\n  ${failures.join('\n  ')}`)

  // And the drawing is four levels of the entities the boards draw, which is what the section
  // is: a reading that found no levels at all would pass every rule above without a word.
  const zoom = zoomLevelsOf(page.html)
  assert.equal(zoom.levels.length, 4, `the zoom section draws ${zoom.levels.length} levels, and the brief asks for four: inside a job, one orchestrator with many jobs, the developer's view, the whole project (D237)`)
  assert.deepEqual(
    zoom.levels.map((level) => level.id),
    ['level-1', 'level-2', 'level-3', 'level-4'],
    'the levels are not the four anchors the brief names, in order: `#level-1` … `#level-4` (D245)',
  )
  assert.equal(zoom.symbols.length, 17, `the one <defs> holds ${zoom.symbols.length} <symbol>s, and the boards draw seventeen entities: the job tile, the orchestrator, the developer, the person, the console, the laptop, the phone, the Firebase card, the project, the draft PR, the cloud, the loop, the box, the four cards and the planned developer (D244)`)
  assert.deepEqual(
    zoom.symbols.map((symbol) => symbol.id).sort(),
    [
      'zoom-box',
      'zoom-card',
      'zoom-card-accent',
      'zoom-card-planned',
      'zoom-cloud',
      'zoom-developer',
      'zoom-developer-planned',
      'zoom-draft-pr',
      'zoom-firebase',
      'zoom-job',
      'zoom-laptop',
      'zoom-loop',
      'zoom-orchestrator',
      'zoom-person',
      'zoom-person-planned',
      'zoom-phone',
      'zoom-project',
    ],
    'the entities of the one <defs> are not the ones the boards draw (D244)',
  )
  // The four arrow heads are the one <defs>'s markers rather than symbols: an arrow head is a
  // shape a `<path>` draws through `marker-end`, so the same-shape rule reaches it as a marker.
  const markers = findAll(zoom.defs[0], (el) => el.name === 'marker')
  assert.deepEqual(
    markers.map((marker) => marker.attrs.get('id')).sort(),
    ['zoom-head-accent', 'zoom-head-green', 'zoom-head-grey', 'zoom-head-planned'],
    'the one <defs> does not hold the four arrow heads the boards draw their arrows with (D244)',
  )
})

test('the same-shape rule refuses a level that draws its own copy, a symbol nothing uses and a use that resolves to nothing', () => {
  // The rule's own proof, one plant per refusal. Each plant is the built page with exactly one
  // thing broken, so a failure that names the wrong thing is a failure a reader cannot act on.
  const page = howItWorks().html
  assert.deepEqual(zoomShapeFailures(page), [], 'the built page does not pass the reading its own plants are drawn from')

  // 1. A level that draws the tile itself where it should draw `<use href="#zoom-job">`: the
  //    defect D244 exists for, because the copy is what drifts from the other three levels.
  const tile = /<use class="zoom-entity" href="#zoom-job"[^>]*\/>/
  assert.ok(tile.test(page), 'the built page draws the job tile with no `<use href="#zoom-job">`, and the plant below replaces one')
  const copied = page.replace(tile, '<rect class="zoom-tile" x="24" y="24" width="150" height="108" />')
  const copyFailures = zoomShapeFailures(copied)
  assert.equal(copyFailures.length, 1, `a level that draws its own copy of the tile is refused ${copyFailures.length} times: ${copyFailures.join(' | ')}`)
  assert.match(copyFailures[0], /holds a `<rect>` of its own/, 'the refusal does not name the shape the level drew itself')
  assert.match(copyFailures[0], /D244/, 'the refusal does not name the rule the level broke')

  // 2. A `<symbol>` no level uses: a shape in the one `<defs>` that nothing in the drawing
  //    holds, which is the half of the rule that keeps the defs honest.
  const orchestrator = page.replaceAll('href="#zoom-orchestrator"', 'href="#zoom-project"')
  assert.notEqual(orchestrator, page, 'the built page draws no `<use href="#zoom-orchestrator">`, and the plant below repoints one')
  const unused = zoomShapeFailures(orchestrator)
  assert.equal(unused.length, 1, `a symbol no level uses is refused ${unused.length} times: ${unused.join(' | ')}`)
  assert.match(unused[0], /holds the <symbol> "zoom-orchestrator" and no level draws it/, 'the refusal does not name the symbol nothing draws')

  // 3. A `<use>` pointing at an id that does not exist: a reference that resolves to nothing
  //    draws nothing, and every gate over the shapes would pass while the level was empty.
  const dangling = page.replace('<use class="zoom-entity" href="#zoom-job"', '<use class="zoom-entity" href="#zoom-job-tile"')
  const danglingFailures = zoomShapeFailures(dangling)
  assert.equal(danglingFailures.length, 1, `a use that resolves to nothing is refused ${danglingFailures.length} times: ${danglingFailures.join(' | ')}`)
  assert.match(danglingFailures[0], /`<use href="#zoom-job-tile">`/, 'the refusal does not name the reference that resolves to nothing')
  assert.match(danglingFailures[0], /holds no such <symbol>/, 'the refusal does not say what the reference was looking for')
})

test('every shape of the four levels is painted from the palette, and every label is legible where it is drawn', () => {
  const page = howItWorks()
  const failures = zoomPaintFailures(page.html, pageCss(page.html))
  assert.deepEqual(failures, [], `${page.rel}: the zoom drawing is not painted the way the site paints:\n  ${failures.join('\n  ')}`)
})

test('the paint reading refuses an untaught element kind in the zoom drawing', () => {
  // ZF42's rule, on the drawing it was written for: a level that grows an element this reading
  // has not been taught is a level whose paint no check holds, and the failure names the element
  // to teach rather than passing it over.
  const page = howItWorks().html
  const css = pageCss(page)
  const tile = /<use class="zoom-entity" href="#zoom-job"[^>]*\/>/
  const planted = page.replace(tile, '<ellipse class="zoom-tile" cx="100" cy="80" rx="70" ry="50" />')
  assert.notEqual(planted, page, 'the built page draws no `<use href="#zoom-job">`, and the plant below replaces one')
  const failures = zoomPaintFailures(planted, css)
  assert.equal(failures.length, 1, `an untaught element kind is refused ${failures.length} times: ${failures.join(' | ')}`)
  assert.match(failures[0], /<ellipse>/, 'the refusal does not name the element the reading has not been taught')
  assert.match(failures[0], /teach this reading the element/, 'the refusal does not say what to do about it')
})

test('the levels\' words are the boards\', and every string of a board is drawn', () => {
  const page = howItWorks()
  const failures = zoomBoardFailures(page.html)
  assert.deepEqual(failures, [], `${page.rel}: the four levels are not the owner's boards:\n  ${failures.join('\n  ')}`)
  assert.deepEqual(
    zoomLevelsOf(page.html).levels.map((level) => level.id),
    BOARDS.map((board) => board.id),
    'the levels the page draws are not the four boards, in order (D245)',
  )
  // And the reading has something to read: every board prints strings, and the levels draw more
  // than one of them — a board with no visible text would pass the two directions in silence.
  for (const board of BOARDS) {
    assert.ok(
      boardWords(board.id).length > 0,
      `${board.file} prints no visible string, and the boards are the source of truth for the four levels' words (the brief of 2026-10-22)`,
    )
  }
  const drawn = zoomLevelsOf(page.html).levels.reduce((count, level) => count + level.texts.length, 0)
  assert.ok(drawn >= 4 * BOARDS.length, `the four levels draw ${drawn} lines between them, and a level of a board draws that board's labels (the brief of 2026-10-22)`)
})

test('a line the drawing wrote for itself is refused, and so is a string of a board the drawing dropped', () => {
  const page = howItWorks().html
  assert.deepEqual(zoomBoardFailures(page), [], 'the built page does not pass the reading its own plants are drawn from')

  // 1. A line the drawing wrote for itself: the picture and the board have drifted, which is
  //    exactly what the boards being the source of truth is for.
  const invented = page.replace(
    '</svg><figcaption class="zoom-caption"',
    '<text class="zoom-word zoom-ink" x="64" y="712" font-size="14">an invented line of the picture</text></svg><figcaption class="zoom-caption"',
  )
  assert.notEqual(invented, page, 'the built page draws no level `<svg>` before a `<figcaption>`, and the plant below adds a line to the first one')
  const inventedFailures = zoomBoardFailures(invented)
  assert.equal(inventedFailures.length, 1, `a line that is not a string of the board is refused ${inventedFailures.length} times: ${inventedFailures.join(' | ')}`)
  assert.match(inventedFailures[0], /an invented line of the picture/, 'the refusal does not quote the line the page invented')
  assert.match(inventedFailures[0], /level1\.dc\.html/, 'the refusal does not name the board the line is not a string of')

  // 2. A string of a board that nothing draws: a fact the board states and the picture dropped.
  const drawn = '<text class="zoom-word zoom-ink-accent" x="640" y="197" font-size="13" text-anchor="middle">one bounce back</text>'
  assert.ok(page.includes(drawn), 'the built page draws no `<text>one bounce back</text>`, and the plant below drops it')
  const droppedFailures = zoomBoardFailures(page.replace(drawn, ''))
  assert.equal(droppedFailures.length, 1, `a board string nothing draws is refused ${droppedFailures.length} times: ${droppedFailures.join(' | ')}`)
  assert.match(droppedFailures[0], /one bounce back/, 'the refusal does not quote the string the drawing dropped')
  assert.match(droppedFailures[0], /level1\.dc\.html prints/, 'the refusal does not name the board the string is from')

  // 3. A level drawn from another board's strings: the title is the board's own name, so a level
  //    that swapped boards is refused rather than read as a level whose words happen to match.
  const swapped = page.replace('<title id="level-1-title">Inside one job</title>', '<title id="level-1-title">The whole project</title>')
  assert.notEqual(swapped, page, 'the built page carries no `<title id="level-1-title">Inside one job</title>`, and the plant below swaps it')
  const swappedFailures = zoomBoardFailures(swapped)
  assert.equal(swappedFailures.length, 1, `a level titled as another board is refused ${swappedFailures.length} times: ${swappedFailures.join(' | ')}`)
  assert.match(swappedFailures[0], /is titled "The whole project" and its board is "Inside one job"/, 'the refusal does not name the title and the board it should have been')
})

test('no label of a level is drawn over another, and none leaves the board', () => {
  const page = howItWorks()
  const failures = zoomLabelOverlapFailures(page.html)
  assert.deepEqual(failures, [], `${page.rel}: a label of the four levels is drawn over another or outside its board:\n  ${failures.join('\n  ')}`)

  // The reading's own proof, one plant per refusal: a label moved onto the line under it, and a
  // label pushed off the right of the board.
  const moved = page.html.replace(
    'x="208" y="394" font-size="16" text-anchor="middle" font-weight="700">Checks<',
    'x="208" y="414" font-size="16" text-anchor="middle" font-weight="700">Checks<',
  )
  assert.notEqual(moved, page.html, 'the built page draws no `<text>Checks</text>` at (208, 394), and the plant below moves it')
  const movedFailures = zoomLabelOverlapFailures(moved)
  assert.equal(movedFailures.length, 1, `a label drawn over another is refused ${movedFailures.length} times: ${movedFailures.join(' | ')}`)
  assert.match(movedFailures[0], /draws "Checks" over "compile, tests"/, 'the refusal does not name the two labels that overlap')

  const off = page.html.replace(
    'x="1072" y="374" font-size="16" text-anchor="middle" font-weight="700">Draft PR<',
    'x="1300" y="374" font-size="16" text-anchor="middle" font-weight="700">Draft PR<',
  )
  assert.notEqual(off, page.html, 'the built page draws no `<text>Draft PR</text>` at (1072, 374), and the plant below pushes it out')
  const offFailures = zoomLabelOverlapFailures(off)
  assert.equal(offFailures.length, 1, `a label outside the board is refused ${offFailures.length} times: ${offFailures.join(' | ')}`)
  assert.match(offFailures[0], /draws "Draft PR" at .*which leaves the board 0 0 1280 720/, 'the refusal does not say the label left the board')
})

test('every arrow of the four levels starts and ends on a card, or on the corner of its own chain', () => {
  const page = howItWorks()
  const failures = zoomArrowFailures(page.html)
  assert.deepEqual(failures, [], `${page.rel}: an arrow of the four levels points at nothing:\n  ${failures.join('\n  ')}`)
  const arrows = zoomLevelsOf(page.html).levels.reduce((count, level) => count + level.arrows.length, 0)
  assert.equal(arrows, 33, `the four levels draw ${arrows} arrows, and the boards draw thirty-three (the brief of 2026-10-22)`)

  // The reading's own proof, one plant per refusal: an arrow stopped short of the card it points
  // at, and an arrow whose path this reading cannot parse.
  const short = page.html.replace('d="M872 400 H984"', 'd="M872 400 H960"')
  assert.notEqual(short, page.html, 'the built page draws no arrow `M872 400 H984`, and the plant below stops it short')
  const shortFailures = zoomArrowFailures(short)
  assert.equal(shortFailures.length, 1, `an arrow that stops in mid-air is refused ${shortFailures.length} times: ${shortFailures.join(' | ')}`)
  assert.match(shortFailures[0], /whose end at \(960, 400\) is more than 2 units from every card/, 'the refusal does not name the endpoint that touches nothing')

  const bent = page.html.replace('d="M872 400 H984"', 'd="M872 400 L984 400"')
  assert.notEqual(bent, page.html, 'the built page draws no arrow `M872 400 H984`, and the plant below bends it')
  const bentFailures = zoomArrowFailures(bent)
  assert.equal(bentFailures.length, 1, `an arrow this reading cannot parse is refused ${bentFailures.length} times: ${bentFailures.join(' | ')}`)
  assert.match(bentFailures[0], /is not a straight run from a point/, 'the refusal does not say the path was not read')
})

test('every label the phone keeps is drawn at 10 px or more, and the small print is hidden there', () => {
  const page = howItWorks()
  const css = pageCss(page.html)
  const failures = zoomPhoneFailures(page.html, css)
  assert.deepEqual(failures, [], `${page.rel}: the four levels are not read on a phone the way the brief asks:\n  ${failures.join('\n  ')}`)

  // The reading's own proof, one plant per half. 1. A board's small print drawn outside the
  // group the phone hides: the group loses its class and its 13 px label is drawn at 9.3 px.
  const loose = page.html.replace('<g class="zoom-fine">', '<g>')
  assert.notEqual(loose, page.html, 'the built page draws no `<g class="zoom-fine">`, and the plant below unwraps one')
  const looseFailures = zoomPhoneFailures(loose, css)
  assert.ok(looseFailures.length > 0, 'small print drawn outside the group the phone hides is not refused, and the group is the whole of what keeps the phone above 10 px')
  const bounce = looseFailures.find((line) => /"one bounce back"/.test(line))
  assert.ok(bounce, `the refusal does not name the label the phone would draw under 10 px: ${looseFailures.join(' | ')}`)
  assert.match(bounce, /at 13 px/, 'the refusal does not say the size the board sets the label at')
  assert.match(bounce, /renders it at 9\.3 px/, 'the refusal does not say the size the floor draws the label at')

  // 2. The floor lowered under what the labels need: the same labels are then drawn at 7 px, and
  //    the refusal names the floor the stylesheet declares rather than the size the board sets.
  const low = css.replace('min-width:57.5rem', 'min-width:40rem')
  assert.notEqual(low, css, 'the built stylesheet declares no `min-width: 57.5rem` under `.zoom-svg`, and the plant below lowers the floor')
  const lowFailures = zoomPhoneFailures(page.html, low)
  assert.ok(lowFailures.length > 0, 'a floor under what the labels need is not refused, and the floor is what the phone reading is measured against')
  assert.match(lowFailures[0], /the 640 px floor/, 'the refusal does not name the floor the stylesheet declares')

  // 3. And the other direction: a label the floor could draw, hidden from the phone anyway — a
  //    word of a board the phone loses for nothing.
  const needless = page.html.replace(
    '<text class="zoom-word zoom-ink-accent" x="640" y="197" font-size="13" text-anchor="middle">one bounce back</text>',
    '<text class="zoom-word zoom-ink-accent" x="640" y="197" font-size="14" text-anchor="middle">one bounce back</text>',
  )
  assert.notEqual(needless, page.html, 'the built page draws no 13 px `<text>one bounce back</text>`, and the plant below raises it to 14 px')
  const needlessFailures = zoomPhoneFailures(needless, css)
  assert.equal(needlessFailures.length, 1, `a label hidden though the phone could read it is refused ${needlessFailures.length} times: ${needlessFailures.join(' | ')}`)
  assert.match(needlessFailures[0], /a label the phone could read is a label the phone keeps/, 'the refusal does not say the label was hidden for nothing')

  // 4. And the box the floor needs: with the figure's scrolling taken away the drawing still keeps
  //    its 920 units and nothing carries them, so the refusal names the box rather than a label.
  const fixed = css.replace('overflow-x:auto', 'overflow-x:visible')
  assert.notEqual(fixed, css, 'the built stylesheet declares no `overflow-x: auto` on the figure, and the plant below takes the scrolling away')
  const fixedFailures = zoomPhoneFailures(page.html, fixed)
  assert.equal(fixedFailures.length, 1, `a figure that does not scroll the floor it declares is refused ${fixedFailures.length} times: ${fixedFailures.join(' | ')}`)
  assert.match(fixedFailures[0], /does not scroll at 390 px/, 'the refusal does not say the figure does not scroll')
  assert.match(fixedFailures[0], /keeps a 920 px floor/, 'the refusal does not name the floor the drawing keeps')
})

test('all four levels are in the built HTML, in order, with no script', () => {
  const page = howItWorks()
  const levels = BOARDS.map((board) => board.id)
  const failures = zoomStillFailures(page.html, levels)
  assert.deepEqual(failures, [], `${page.rel}: the four levels are not four stills in the page:\n  ${failures.join('\n  ')}`)

  // The reading's own proof: a section that needs a script, and a level whose drawing is hidden,
  // are each refused by name — the two ways a still stops being one.
  const scripted = page.html.replace('<section class="zoom"', '<section class="zoom" data-zoom-scripted><script>window.zoom = 1</script>')
  assert.match(zoomStillFailures(scripted, levels).join(' | '), /carries a <script>/, 'the reading passes a section that carries a script, and a still is what needs no script')
  const hidden = page.html.replace('<svg class="zoom-svg"', '<svg class="zoom-svg" hidden')
  assert.match(zoomStillFailures(hidden, levels).join(' | '), /drawing is hidden/, 'the reading passes a hidden drawing, and a level a reader cannot see is not a level')
  // And the order: the levels are a sequence, so a page that drew them out of order is refused.
  const reversed = levels.slice().reverse()
  assert.match(zoomStillFailures(page.html, reversed).join(' | '), /the levels are in order/, 'the reading passes the levels in any order, and the four are one sequence')
})
