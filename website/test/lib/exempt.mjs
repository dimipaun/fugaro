// website/test/lib/exempt.mjs — D188's three reference pages, in one place (redesign part P4;
// changes spec of 2026-10-18, item 2).
//
// Two gates exempt the same pages, and for the same reason: `/docs/`, `/security/` and
// `/changelog/` render a document of this repository rather than a page of the site, so the
// words on them are that document's own and neither the whole-page word ceiling nor the
// retired-word rule is the site's to keep over them (D188, which exempts them from the ceiling;
// the changes spec's item 2 retires the word everywhere else).
//
// **Why the list is written once, here.** Two lists would be two rules, and the failure they
// drift into is quiet: a page added to one and not the other is a page the site exempts from
// one gate and holds to the other, with both gates green. So `test/wording.mjs` and
// `test/budget.mjs` both import this set — the one that skips these pages in the wording rule
// and the one that exempts them from the ceiling — and a page added here is exempt from both,
// in one commit, on purpose.
//
// **Why it is written out rather than read from the ruling.** The set is D188's own list and
// `website/test/wording.mjs` holds it equal to the pages the ruling's row in
// `docs/website/redesign/reconciliation.md` names: adding a page here, or taking one out of
// the ruling, fails that case with both lists in the message. Reading the ruling instead would
// let an edit to a document widen what the gates skip, which is the one thing this file exists
// to stop — a ruling the owner changes is a ruling the gates are re-read against, not one they
// follow.

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO } from './html.mjs'

/** The document that records D188, and that the set below is held equal to. */
export const RECONCILIATION = 'docs/website/redesign/reconciliation.md'

/** The pages D188 exempts: the three that render a document of this repository. */
export const DOCUMENT_PAGES = new Set(['/docs/', '/security/', '/changelog/'])

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
  return [...new Set([...row.matchAll(/`(\/[a-z-]+\/)`/g)].map((match) => match[1]))]
}
