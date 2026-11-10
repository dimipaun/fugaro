// website/test/lib/exempt.mjs — the pages D188 exempts from the word ceiling, and the narrower
// set the wording rules skip (redesign part P4; changes spec of 2026-10-18, item 2).
//
// Two gates look away from a page, and they do not look away from the same pages:
//
//   - **The whole-page word ceiling** (`test/budget.mjs`, D188) exempts five: `/docs/`,
//     `/security/`, `/changelog/` and `/legal/subscription-terms/`, which render a document of
//     this repository rather than a page of the site, and — since 2026-11-10 — `/compatibility/`,
//     whose tables print `website/data/compatibility.yaml`: the 46 rows of names, notes,
//     statuses and editions that file alone carries (D1537), so the words a reader reads there
//     are the matrix's own and not a page's prose. The task that adds the page is the
//     compatibility matrix's implementation plan (CM2), which names this file; the ceiling
//     itself is recorded in D188's row, which the set below is held equal to.
//   - **The wording rules** — the retired word and D249's banned framing phrases
//     (`test/wording.mjs`) — exempt only the first four. The reason is the one the ceiling gives
//     for them and not for the matrix: a page that renders a document of this repository is a
//     quotation of it, and a quotation is not the site's own copy (the changes spec's item 2
//     retires the word everywhere else). `/compatibility/` is not a quotation: the matrix is a
//     file this site owns and renders, the page writes its own chrome around it, and a retired
//     word or a banned claim on the built page is a defect whichever file the words came from.
//     So the matrix's page is exempt from the ceiling and held to the wording rules.
//
// **Why the sets are written here rather than read from the ruling.** `CEILING_PAGES` is
// D188's own list, and `website/test/wording.mjs` holds it equal to the pages the ruling's row
// in `docs/website/redesign/reconciliation.md` names and holds `DOCUMENT_PAGES` equal to that
// same list minus `CEILING_ONLY_PAGES`: a page added to either set, or taken out of the ruling,
// fails one of those two cases with both lists in the message. Reading the ruling instead would
// let an edit to a document widen what the gates skip, which is the one thing this file exists
// to stop — a ruling the owner changes is a ruling the gates are re-read against, not one they
// follow.

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO } from './html.mjs'

/** The document that records D188, and that the sets below are held equal to. */
export const RECONCILIATION = 'docs/website/redesign/reconciliation.md'

/**
 * The pages whose words the wording rules leave alone: they render a document of this
 * repository, so the sentence is that document's and not the site's own copy.
 */
export const DOCUMENT_PAGES = new Set([
  '/docs/',
  '/security/',
  '/changelog/',
  '/legal/subscription-terms/',
])

/**
 * The one page the word ceiling exempts and the wording rules do not: the compatibility
 * matrix's own page, which renders a file this site owns rather than a document of the
 * repository quoted whole (D1537; the compatibility matrix's plan, Task CM2).
 */
export const CEILING_ONLY_PAGES = ['/compatibility/']

/** Every page D188 exempts from the whole-page word ceiling (`test/budget.mjs`). */
export const CEILING_PAGES = new Set([...DOCUMENT_PAGES, ...CEILING_ONLY_PAGES])

/** The ruling itself, as written. */
export function readReconciliation() {
  return readFileSync(path.join(REPO, RECONCILIATION), 'utf8')
}

/**
 * The pages a ruling's row names, as the paths it writes in backticks — the second column of
 * `reconciliation.md`'s decisions table, which is where D188 spells its exemption out. A row
 * that is not there throws rather than returning nothing: a ruling that moved is one this
 * file has to be re-read against, and an empty list would read as "nothing is exempt".
 */
export function documentPagesNamedIn(ruling, decision = 'D188') {
  const row = ruling.split('\n').find((line) => line.startsWith(`| **${decision}**`))
  if (!row) {
    throw new Error(
      `${RECONCILIATION} carries no ${decision} row, and the pages the gates exempt are that ruling's own list (website/test/lib/exempt.mjs)`,
    )
  }
  return [...new Set([...row.matchAll(/`(\/[a-z-]+(?:\/[a-z-]+)*\/)`/g)].map((match) => match[1]))]
}
