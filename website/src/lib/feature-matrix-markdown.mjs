// website/src/lib/feature-matrix-markdown.mjs — FEATURES.md, rendered from a snapshot
// (docs/feature-matrix/implementation-plan.md, task T3; the shapes it reads are written for a
// reader in docs/feature-matrix/contract.md).
//
// One function, `renderMarkdown`, turns a snapshot into the whole of FEATURES.md: a lead, the
// legend a reader needs to read the table, the `as_of` line, one table and the fenced block the
// site's features page reads (R5). It writes no claim of its own — every name, explanation and
// status in the table is a snapshot row's, and the block is the snapshot itself — so the file and
// the page cannot disagree about a row, which is the shape the compatibility matrix already has
// (F1).
//
// **What the file may not carry** (R1, R8): a spec name, a status.md section name, a task id, a
// date or a price. The words here are the site's own, and
// `website/test/feature-matrix-markdown.mjs` holds the whole rendered document to
// `leakProblems` with the mapping's spec names. The lead and the legend say nothing about what a
// plan includes: the Community column's footnote is the owner's wording, not this file's (O1),
// and it arrives — if at all — as a contract change after the owner's read (T6).
//
// **The document carries exactly one table and exactly one fenced block.** The legend is prose,
// because the block under the table is the only fenced block FEATURES.md may have and a reader
// checking the block by eye should not have to decide which fence is the snapshot's (R5); one
// table is also what the acceptance asks for, so a test reading "the table" is unambiguous.

import { PLANS, snapshotBlock, validateSnapshot } from './feature-matrix-model.mjs'

/**
 * The two sentences between the title and the legend: what Fugaro does, and what this file
 * answers. It claims nothing a row below it does not carry, and it names no plan.
 */
const LEAD =
  'Fugaro runs each task you hand it as its own throwaway job in your cloud project, and opens the pull request when it is done. This file is what it can do, and how far along each capability is.'

/**
 * The legend: the three statuses in the words the table prints, and how the mark under a plan's
 * name is read. It defines the words and the marks and stops there — what a plan includes is the
 * Subscription Terms' to say, and this file carries no sentence of its own about that (O1).
 */
const LEGEND = [
  '**Available** — works today. **Coming soon** — under way, or built and not shipped yet. **Planned** — not started.',
  '',
  'A ✓ under Community, Team or Enterprise means the feature is included there, and a dash means it is not.',
].join('\n')

/**
 * The line above the block. It is a comment rather than prose: a reader of the rendered file
 * meets the table, and the block is the machine's copy of what the table says.
 */
const BLOCK_NOTE =
  '<!-- The table above is rendered from the block below, which the features page on the website reads. The two cannot disagree about a row. -->'

/** The status, in the words a reader reads. The three are `STATUSES`, and a fourth cannot reach here. */
const STATUS_LABEL = {
  available: 'Available',
  'coming-soon': 'Coming soon',
  planned: 'Planned',
}

/** The plan, as the column heading over its marks (D2440). */
const PLAN_LABEL = {
  community: 'Community',
  team: 'Team',
  enterprise: 'Enterprise',
}

/** The mark for a plan that includes the feature, and for one that does not. */
const MARK = '✓'
const NO_MARK = '—'

/**
 * The whole of FEATURES.md, from the snapshot and nothing else. The snapshot is validated first,
 * so a row the page has no word or mark for is refused here rather than rendered as an empty
 * cell.
 */
export function renderMarkdown(snapshot) {
  validateSnapshot(snapshot)
  return (
    [
      '# Fugaro features',
      LEAD,
      LEGEND,
      `as_of: ${snapshot.as_of}`,
      table(snapshot),
      BLOCK_NOTE,
      snapshotBlock(snapshot),
    ].join('\n\n') + '\n'
  )
}

/** The one table: the row's own words, its status word, and a mark under each plan. */
function table(snapshot) {
  const head = ['Feature', 'What it does', 'Status', ...PLANS.map((plan) => PLAN_LABEL[plan])]
  const rows = snapshot.rows.map((row) => [
    row.name,
    row.explanation,
    STATUS_LABEL[row.status],
    ...PLANS.map((plan) => (row.plans[plan] ? MARK : NO_MARK)),
  ])
  const rule = head.map(() => '---')
  return [head, rule, ...rows].map((cells) => `| ${cells.map(cell).join(' | ')} |`).join('\n')
}

/**
 * A cell's text. A `|` would end the cell and a newline would end the row, so both are escaped
 * rather than left to break the table: the explanations are hand-written (the mapping is
 * hand-kept, D2441) and a table is not the place to discover a stray pipe.
 */
function cell(text) {
  return text.replace(/\|/g, '\\|').replace(/\s*\n\s*/g, ' ')
}
