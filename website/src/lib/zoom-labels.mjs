/**
 * website/src/lib/zoom-labels.mjs — the four zoom levels' sources, held to the documents they
 * quote (Global Constraint 6; the brief of 2026-10-21, `docs/website/zoom-diagrams/spec.md`).
 *
 * **Where the words live.** The levels' labels are rows of `website/data/zoom-levels.yaml`, not
 * constants of this file: the plan asks for the drawing's words as data so that the picture and
 * its text cannot drift, and a label the component renders that is not a row of the file (or a
 * row nothing renders) is refused by `website/test/how-it-works.mjs`. What this file carries is
 * the other half — the **documents** those rows cite, read once, and the guard that holds each
 * row to its document at build time.
 *
 * **Why a name and not a path in the row.** A row says `source: README.md#How it works`, which
 * is the same naming `website/src/lib/run-labels.mjs` uses, and this file is the one place that
 * name is turned into text: a source the map does not carry is a failure of the row's citation
 * rather than of the document, and `holdLabels` says so by name.
 *
 * **The labels are the brief's, corrected to what the runner does** (reconciliation §3). The
 * brief's Level 1 says the cheap loop is a cheap coder against a cheap reviewer and that the
 * senior reviews once; the tree says the first-line reviewer is the coder's own model in a
 * fresh session (`docs/recipes.md` §1), that the senior review is a loop bounded by
 * `agent.review_rounds` (default 2), and that it does not wait on the cheap loop. The rows are
 * the tree's sentences, so the drawing says what the run does.
 */
import { holdLabels, section, wholeFile } from './repo-quote.mjs'

export const README = 'README.md'
export const README_HOW = 'README.md#How it works'
export const README_SAFETY = 'README.md#Safety and cost controls'
export const README_START = 'README.md#Getting started'
export const README_WHY = 'README.md#Why Fugaro'
export const RECIPES = 'docs/recipes.md'
export const SPEC = 'docs/SPEC.md'
export const ZOOM_SPEC = 'docs/website/zoom-diagrams/spec.md'

/**
 * Every document a row of `website/data/zoom-levels.yaml` may cite, by the name the row uses.
 * The README's sections are read as sections rather than as the whole file, so that a row
 * cannot quote a sentence from a part of the README the level is not about and still be said
 * to come from `#How it works`.
 */
export const SOURCES = {
  [README]: wholeFile(README),
  [README_HOW]: section(README, 'How it works'),
  [README_SAFETY]: section(README, 'Safety and cost controls'),
  [README_START]: section(README, 'Getting started'),
  [README_WHY]: section(README, 'Why Fugaro'),
  [RECIPES]: wholeFile(RECIPES),
  [SPEC]: wholeFile(SPEC),
  [ZOOM_SPEC]: wholeFile(ZOOM_SPEC),
}

/**
 * The five entities a level may draw, in the order the brief names them (D244): the job tile,
 * the orchestrator, the developer, the console and the project. One `<symbol>` of the
 * component's `<defs>` per entity, and a level draws an entity **only** through `<use>` — so
 * this list is also the check that a row's `entity` is a shape the drawing has.
 */
export const ENTITIES = ['job', 'orchestrator', 'developer', 'console', 'project']

/**
 * Every label of a level, as `holdLabels` wants them: a name for the failure to print, the
 * text, and the document the text is a line of. The name is the row's own address —
 * `level-2 job[1] label[0]` — so a failure says which row of the data file to open rather
 * than which of thirty-odd labels it was.
 *
 * @param {{ id: string, name: { text: string, source: string }, caption: { text: string, source: string } | null, parts: { entity: string, labels: { text: string, source: string }[] }[] }[]} levels
 * @returns {Record<string, { text: string, source: string }>}
 */
export function labelsOf(levels) {
  const labels = {}
  for (const level of levels) {
    labels[`${level.id} name`] = level.name
    if (level.caption !== null) labels[`${level.id} caption`] = level.caption
    level.parts.forEach((part, partIndex) => {
      part.labels.forEach((label, labelIndex) => {
        labels[`${level.id} part[${partIndex}] ${part.entity} label[${labelIndex}]`] = label
      })
    })
  }
  return labels
}

/**
 * The four levels' words, held to the documents they cite. Called by `ZoomLevels.astro` at
 * build time: a row that is not a line of the document it names stops the build, naming the
 * row, rather than reaching a reader as the repository's own words.
 *
 * @param {Parameters<typeof labelsOf>[0]} levels
 */
export function holdZoomLabels(levels) {
  holdLabels(labelsOf(levels), SOURCES, 'ZoomLevels.astro')
}
