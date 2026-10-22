/**
 * website/src/lib/zoom-boards.mjs — the four boards, and the words a level draws (the brief of
 * 2026-10-22).
 *
 * **Where the words live now.** The four zoom levels are redrawn from the owner's approved
 * design boards — `website/design-ref/zoom-levels/level1.dc.html` … `level4.dc.html`, drawn in
 * Claude Design at 1280x720 — and the boards are the source of truth for the geometry, the
 * labels, the colours and the explanatory captions. So the words are no longer rows of a data
 * file of the site's own (`website/data/zoom-levels.yaml`, which this replaces): they are the
 * strings the boards print, and this reader takes them from the board files themselves.
 *
 * **Why that is a stronger reading than the data file was.** A data file is a second copy of a
 * board's text, and a second copy drifts: the defect the file existed to prevent was a label
 * the picture invented, and the file could only catch that by being the picture's source rather
 * than by checking it. Here the board is the source. `boardStrings` reads every visible string
 * out of a board — the tags become line breaks, the `<style>`, `<script>` and `<title>` blocks
 * are dropped, the entities are decoded — and `holdZoomBoards` holds every line the component
 * draws to the board it was drawn from, at build time, the way `holdLabels` holds a page's
 * marketing lines to the documents they quote (Global Constraint 6). A label the component
 * wrote for itself, or a word it changed, stops the build naming the file.
 *
 * **The reading is deliberately loose about whitespace and nothing else.** A board wraps a
 * card's text into the lines the card can hold and the drawing wraps it again at its own
 * column, so the comparison is `comparable` — the same reading the repository's documents are
 * held with, which removes whitespace and markdown's markers. Everything else is byte for byte:
 * `Ship` is not `ship`, and a dropped word is a failure.
 *
 * `website/test/how-it-works.mjs` reads the same function the other way round, which is the
 * half this cannot check: every string of a board is a `<text>` of its level, so a label the
 * drawing dropped is a failure there rather than a silence here.
 */
import { comparable, wholeFile } from './repo-quote.mjs'

/** The boards, one per level, in the order the levels are drawn (D245). */
export const BOARDS = [
  { id: 'level-1', file: 'website/design-ref/zoom-levels/level1.dc.html', name: 'Inside one job' },
  { id: 'level-2', file: 'website/design-ref/zoom-levels/level2.dc.html', name: 'One orchestrator, many jobs' },
  { id: 'level-3', file: 'website/design-ref/zoom-levels/level3.dc.html', name: "The developer's view" },
  { id: 'level-4', file: 'website/design-ref/zoom-levels/level4.dc.html', name: 'The whole project' },
]

/** The named entities a board writes in its markup, as the character a reader sees. */
const ENTITIES = { '&amp;': '&', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&#39;': "'", '&apos;': "'", '&nbsp;': ' ' }

/**
 * Every visible string of one board, in the order the board writes it: the text nodes, with the
 * markup around them turned into line breaks so that a `<b>` title and the line under it are
 * two strings rather than one, and with the three blocks that carry no visible text — the
 * `<title>` of the document, the `<style>` the board is drawn with and the `<script>` its
 * preview needs — dropped before the tags are.
 *
 * A board is HTML, so the reading is a reading of HTML: the tags it writes carry no `>` inside
 * an attribute, which is what makes the strip safe, and the entities it writes are the seven
 * below.
 */
export function boardStrings(file) {
  const text = wholeFile(file)
    .replace(/<style[\s\S]*?<\/style>/gi, ' ')
    .replace(/<script[\s\S]*?<\/script>/gi, ' ')
    .replace(/<title[\s\S]*?<\/title>/gi, ' ')
    .replace(/<[^>]*>/g, '\n')
    .replace(/&[a-zA-Z#0-9]+;/g, (entity) => ENTITIES[entity.toLowerCase()] ?? entity)
  return text
    .split('\n')
    .map((line) => line.replace(/\s+/g, ' ').trim())
    .filter((line) => line !== '')
}

/** The board a level is drawn from, by the level's own id. */
export const boardOf = (id) => BOARDS.find((board) => board.id === id) ?? null

/**
 * Every line the component draws, held to the board it was drawn from. `levels` is
 * `[{ id, words }]` — the strings one level draws, in the order it draws them — and a line that
 * is not a string of its board stops the build, naming the file and the line, rather than
 * reaching a reader as the board's own words.
 */
export function holdZoomBoards(levels, where = 'ZoomLevels.astro') {
  for (const level of levels) {
    const board = boardOf(level.id)
    if (board === null) {
      throw new Error(
        `${where}: the level ${JSON.stringify(level.id)} is drawn and no board of ${BOARDS.map((entry) => entry.file).join(', ')} is it. A level of the drawing is one of the owner's four boards.`,
      )
    }
    const words = comparable(boardStrings(board.file).join('\n'))
    for (const word of level.words) {
      if (!words.includes(comparable(word))) {
        throw new Error(
          `${where}: the level ${JSON.stringify(level.id)} draws ${JSON.stringify(word)}, which is not a string of ${board.file}. Every word of a level is the board's own, so that the drawing and the approved board cannot drift (the brief of 2026-10-22). Either the line was rewritten or the board moved it.`,
        )
      }
    }
  }
}
