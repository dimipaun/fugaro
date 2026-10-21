/**
 * website/src/lib/repo-quote.mjs — a page's line, held to the document it is quoted from
 * (Global Constraint 6; the redesign's build-time guards).
 *
 * The redesigned pages say what the repository says: a marketing line on the site is a line
 * of `README.md`, of `docs/gcp-setup.md` or of the design documents, quoted rather than
 * written, and a paraphrase is a claim nothing holds. Each component that renders such lines
 * declares them with the document they come from and calls `holdLabels` at build time: a line
 * that is not in the document it cites stops the build, naming the file, rather than reaching
 * a reader as the repository's own words.
 *
 * The comparison is `comparable`, and it is deliberately loose about three things and nothing
 * else: markdown's markers (`*` for emphasis, `` ` `` for code), the apostrophe a document
 * writes straight and the site sets as a glyph, and whitespace — a document wraps a sentence
 * across lines and a page renders it as one. Everything else is byte for byte, so a word
 * changed, a clause dropped or a sentence moved to another card is a failure.
 *
 * This is the same reading `RunDiagram.astro` makes of its own labels, which is why it lives
 * here: the how-it-works diagram and the homepage's run band quote four of the same lines, and
 * two readers of "is this line the document's" that could disagree is the drift the check
 * exists to stop.
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO_ROOT } from '../content.ts'

/**
 * A string with its markdown markers, its apostrophes and its whitespace removed: how a label
 * and the document it claims to come from are compared. A page may set a document's straight
 * apostrophe in the site's own type, and that is a glyph rather than a word.
 */
export function comparable(document) {
  return document.replace(/[`*]/g, '').replace(/[‘’]/g, "'").replace(/\s+/g, '')
}

/** A file of the repository, whole. */
export function wholeFile(file) {
  return readFileSync(path.join(REPO_ROOT, file), 'utf8')
}

/**
 * One section of a document — `## How it works` in the README, `### Run, watch, diagnose` in
 * `docs/gcp-setup.md` — up to the next heading of the same level or a shallower one, which is
 * where a reader would say that section ends. `null` when the document carries no such
 * heading, which the caller reports rather than this reader refusing.
 */
export function section(file, heading) {
  const text = wholeFile(file)
  const at = [...text.matchAll(/^(#+) (.+)$/gm)].find((match) => match[2].replace(/`/g, '') === heading)
  if (!at || at.index === undefined) return null
  const level = at[1].length
  const rest = text.slice(at.index)
  const next = [...rest.matchAll(/^(#+) /gm)].find((match) => match.index !== undefined && match.index > 0 && match[1].length <= level)
  return next && next.index !== undefined ? rest.slice(0, next.index) : rest
}

/**
 * Every label, held to the document it cites. `where` names the component, so the failure a
 * build prints says which file to open; `sources` maps a source name to its text, and a
 * source that is not in it is a failure of the component's own list rather than of the label.
 */
export function holdLabels(labels, sources, where) {
  for (const [name, label] of Object.entries(labels)) {
    const doc = sources[label.source]
    if (!doc) {
      throw new Error(
        `${where}: the ${name} line cites ${JSON.stringify(label.source)}, which the component's own source list does not carry. Either the citation is wrong or the document is missing from the list (Global Constraint 6).`,
      )
    }
    if (!comparable(doc).includes(comparable(label.text))) {
      throw new Error(
        `${where}: the ${name} line ${JSON.stringify(label.text)} is not a line of ${label.source}, and every line a page quotes is the repository's own words (Global Constraint 6). Either the line was rewritten or the document moved it.`,
      )
    }
  }
}
