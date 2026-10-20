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
//     width a 360 px phone cannot fit, or a `white-space: nowrap` that cannot break; the tall
//     viewBox is drawn at a phone's own width and its smallest label is still at least 12 px
//     once the column scales it; and exactly one of the two drawings is displayed at a time —
//     the wide one above the site's 48 rem breakpoint, the tall one below it — so the run is
//     drawn twice and read once.
//
// Standard library only; no network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
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
  for (const declaration of diagramDeclarations(css)) {
    if (!['width', 'min-width'].includes(declaration.property)) continue
    const px = lengthInPx(declaration.value, css)
    if (declaration.property === 'width' && /^\d+(\.\d+)?%$/.test(declaration.value.trim())) continue
    if (Number.isNaN(px)) {
      out.push(`\`${declaration.selector}\` is \`${declaration.property}: ${declaration.value}\`, which this check cannot turn into pixels, and a width it cannot read is one nothing checked (the owner's draft of 2026-10-19)`)
    } else if (px > PHONE_BOX) {
      out.push(`\`${declaration.selector}\` is \`${declaration.property}: ${declaration.value}\` — ${Math.round(px)} px, and a 360 px phone's content box is ${PHONE_BOX} px: a box wider than the phone is a page the phone scrolls sideways (the owner's draft of 2026-10-19)`)
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

  // The second viewBox is a second scale, at a phone's own width, with labels a reader can
  // read once the column has scaled it: the smallest label of the tall drawing, at the width
  // the drawing is shown at, is at least a readable 12 px.
  const tall = diagram.drawings.find((drawing) => drawing.tall)
  if (!tall) {
    out.push('the page carries no tall drawing, and a phone meets the run stacked rather than shrunk: the wide drawing\'s labels are a third of the size they are drawn at on a 360 px screen (the owner\'s draft of 2026-10-19)')
  } else {
    const box = tall.viewBox.trim().split(/\s+/).map(Number)
    if (Number.isFinite(box[2]) && box[2] > PHONE_WIDTH) {
      out.push(`the tall drawing is drawn ${box[2]} units wide and a phone's viewport is ${PHONE_WIDTH} px: the second viewBox is the run drawn at a phone's own width, so its labels scale by about one and stay readable (the owner's draft of 2026-10-19)`)
    }
    const smallest = Math.min(...tall.sizes)
    const scaled = (smallest * PHONE_BOX) / (Number.isFinite(box[2]) ? box[2] : PHONE_BOX)
    if (!(scaled >= 12)) {
      out.push(`the tall drawing's smallest label is ${smallest} units, which is ${scaled.toFixed(1)} px at a phone's content box: a label under 12 px is a label a reader on a phone cannot read, and the stacked drawing is there so that they can (the owner's draft of 2026-10-19)`)
    }
  }

  // Exactly one drawing is displayed at a time: the tall one below the site's breakpoint, and
  // the wide one above it. Above it the tall drawing is `display: none`, which is what takes it
  // out of the reading as well as off the page.
  const shown = (klass) => {
    const rules = diagramDeclarations(css).filter((declaration) => declaration.property === 'display' && new RegExp(`\\.${klass}(?![\\w-])`).test(declaration.selector))
    return {
      plain: rules.filter((rule) => !rule.conditions).map((rule) => rule.value),
      phone: rules.filter((rule) => /width\s*<=?\s*([\d.]+)rem/.test(rule.conditions) && Number(/width\s*<=?\s*([\d.]+)rem/.exec(rule.conditions)[1]) * 16 <= BREAKPOINT).map((rule) => rule.value),
    }
  }
  const tallShown = shown('run-tall')
  const wideShown = shown('run-wide')
  if (!tallShown.plain.includes('none')) {
    out.push(`the tall drawing is \`display: ${tallShown.plain.join(', ') || '(nothing declared)'}\` above the site's breakpoint, and only one of the two drawings is shown at a time: the run is drawn twice and read once (the owner's draft of 2026-10-19)`)
  }
  if (!tallShown.phone.includes('block')) {
    out.push(`the tall drawing is \`display: ${tallShown.phone.join(', ') || '(nothing declared)'}\` inside the media query below the site's breakpoint, and a phone is met with the stacked run rather than the shrunk wide one (the owner's draft of 2026-10-19)`)
  }
  if (!wideShown.phone.includes('none')) {
    out.push(`the wide drawing is \`display: ${wideShown.phone.join(', ') || '(nothing declared)'}\` inside the media query below the site's breakpoint, and a phone shows exactly one drawing (the owner's draft of 2026-10-19)`)
  }
  return out
}

/** True when the strings are not all the same. */
function different(values) {
  return new Set(values).size > 1
}

/** The width a 360 px phone's column has, with `main`'s own gutter out of it. */
const PHONE_BOX = 328

/** And the phone itself: the width the tall drawing is drawn at, and the width it scales into. */
const PHONE_WIDTH = 360

/** The site's breakpoint: `base.css` switches the chrome at 48 rem, and the drawings follow it. */
const BREAKPOINT = 48 * 16

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

  const cases = [
    ['a page with no diagram', page.replace(/<figure class="run-diagram"[\s\S]*?<\/figure>/, ''), /renders 0 run diagrams/],
    [
      'a diagram holding no drawing',
      page.replace(/<svg class="run-svg run-wide"[\s\S]*?<\/svg>/, '<span>a run, described</span>').replace(/<svg class="run-svg run-tall"[\s\S]*?<\/svg>/, ''),
      /holds no drawing/,
    ],
    [
      'a drawing with no name',
      page.replace(/class="run-svg run-wide" viewBox="[^"]*" role="img" aria-label="[^"]*"/, 'class="run-svg run-wide" viewBox="0 0 1200 617" role="img" aria-label=""'),
      /carries no `aria-label`|different accessible names/,
    ],
    ['a drawing that is not a picture', page.replace(/class="run-svg run-wide" viewBox="([^"]*)" role="img"/, 'class="run-svg run-wide" viewBox="$1" role="presentation"'), /is `role="presentation"`/],
    ['a drawing with no viewBox', page.replace(/class="run-svg run-wide" viewBox="0 0 1200 617"/, 'class="run-svg run-wide"'), /picture that scales carries the four numbers/],
    // The stages: a stage dropped from the drawing, one added, two swapped, and one the
    // repository does not name.
    [
      'a stage dropped from the drawing',
      page.replace(/<text class="run-stage" data-stage="verify"[^>]*>verify<\/text>/, ''),
      /draws the stages .*and the run's stages are/,
    ],
    [
      'a stage added to the drawing',
      page.replace(/<text class="run-stage" data-stage="writeback"/, '<text class="run-stage" data-stage="deploy" font-size="17" text-anchor="middle">deploy</text><text class="run-stage" data-stage="writeback"'),
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
    ['a tall drawing shown above the breakpoint', rewrite('run-tall', 'display:none', 'display:block'), /above the site's breakpoint/],
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
