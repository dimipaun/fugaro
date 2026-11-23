// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Budget kind of the Manage section (the console-improvements plan's task
// MO-B): the caps and the kill state it draws per repository from `budget.show`,
// the Spend link it offers instead of any cap control (D2909), and the reduced
// mode, where it withholds each figure under the view that covers it instead of
// reading a shape this console cannot vouch for.
//
// **What this file is for.** The kind is the whole of what the shell may draw
// for the Budget group -- the descriptor is a kind's own list of columns, cells
// and actions, and the shell reads no document key of its own
// (`docs/console-improvements/manage-kinds.md`) -- so the cases here are the
// facts a reader of the screen depends on: which rows exist, what a cell says,
// that nothing on it writes a cap, that no alerts row appears however the
// document grows (D2909), and that a reduced console withholds rather than
// misreads.
//
// The document is built here rather than loaded from `test/fixtures/`: a fixture
// is a recorded read with a digest and a licence sibling (`fixture.test.ts`), and
// this file's subject is the kind's decoding, not the recording of a document.

import { describe, expect, it } from 'vitest'

import {
  BUDGET_CAPS_HIDDEN_WORD,
  BUDGET_KILL_HIDDEN_WORD,
  BUDGET_SPEND_HIDDEN_WORD,
  budgetKindFor,
} from '../../src/renderer/manage/kinds/budget'
import { LIVE_CAPS_HIDDEN_WORD } from '../../src/renderer/screens/live'
import { classifyCliVersion } from '../../src/shared/version'
import type { CliCompatibility, ReducedView } from '../../src/shared/version'

/** SUPPORTED is the handshake for a CLI inside this console's own range
 * (`SUPPORTED_CLI_MINORS`): every view is drawn. */
const SUPPORTED = classifyCliVersion('0.12.0')

/** REDUCED is the handshake for a CLI this console will not vouch for: 0.9 is
 * above the hard floor and outside `SUPPORTED_CLI_MINORS`, so the mode is the
 * reduced one and all three views are withheld -- the same line `ipc.test.ts`
 * and `startup.test.ts` build their reduced handshake from. */
const REDUCED = classifyCliVersion('0.9.0')

/** withHidden is the reduced answer with exactly the named views withheld. The
 * realistic answers withhold all three or none, and a subset is how a case here
 * asks the kind about one view's own question -- the same shape `ipc.test.ts`
 * builds to check the handshake's own reading. */
function withHidden(...views: readonly ReducedView[]): CliCompatibility {
  return { ...REDUCED, hidden: [...views] }
}

/** aBudget is a `budget show --json` document: two repositories, one under its
 * own cap and one under the project's default, each with the money counted
 * against it. The second is killed with all three facts a kill carries, which is
 * the shape `omitempty` makes optional and the shape a row's kill cell has to
 * draw. */
function aBudget(): Readonly<Record<string, unknown>> {
  return {
    project: 'acme',
    gcp_project: 'acme-dev',
    mode: 'enforce',
    day: '2026-11-20',
    max_reserve_usd: 12,
    global: {
      daily_cap_usd: 25,
      per_run_cap_usd: 5,
      counted_usd: 8.25,
      spent_usd: 6,
      notional_usd: 0,
      leased_usd: 2.25,
      headroom_usd: 16.75,
      kill: null,
    },
    defaults: { repo_daily_cap_usd: 25, repo_per_run_cap_usd: 5 },
    repos: [
      {
        slug: 'acme/app',
        repo: 'acme/app',
        daily_cap_usd: 20,
        daily_cap_source: 'repo',
        per_run_cap_usd: 4,
        per_run_cap_source: 'repo',
        counted_usd: 3.5,
        spent_usd: 1.25,
        notional_usd: 0,
        leased_usd: 2.25,
        headroom_usd: 16.5,
        kill: null,
        running: [],
      },
      {
        slug: 'acme/website',
        repo: 'acme/website',
        daily_cap_usd: 25,
        daily_cap_source: 'default',
        per_run_cap_usd: 5,
        per_run_cap_source: 'global',
        counted_usd: 4.75,
        spent_usd: 4.75,
        notional_usd: 0,
        leased_usd: 0,
        headroom_usd: 20.25,
        kill: {
          by: 'dana@example.invalid',
          at: '2026-11-20T09:00:00Z',
          reason: 'the day’s spend reached the cap while a run was looping',
        },
        running: [],
      },
    ],
    running: [],
    warnings: [],
  }
}

/** rowCells is one row's cells, or a failure naming the row a case expected and
 * did not get -- so a case about the second repository says which one it wanted
 * rather than reading `undefined`. */
function rowCells(
  compatibility: CliCompatibility,
  index: number,
  doc: unknown = aBudget(),
): Readonly<Record<string, string>> {
  const rows = budgetKindFor(compatibility).decode(doc)
  const row = rows[index]
  if (row === undefined) throw new Error(`the kind drew no row ${index} (it drew ${rows.length})`)
  return row.cells
}

/** repoAt is one of the fixture's repository blocks, or a failure naming the one
 * a case wanted -- so a case that changes a member of it never spreads
 * `undefined`. */
function repoAt(index: number): Readonly<Record<string, unknown>> {
  const repos = aBudget()['repos'] as readonly Readonly<Record<string, unknown>>[]
  const row = repos[index]
  if (row === undefined) throw new Error(`the fixture has no repository ${index}`)
  return row
}

/** withRepo is the document with its first repository block replaced, which is
 * how a case asks one question about a cap the fixture does not carry. */
function withRepo(row: Readonly<Record<string, unknown>>): Readonly<Record<string, unknown>> {
  const doc = aBudget()
  const repos = doc['repos'] as readonly Readonly<Record<string, unknown>>[]
  return { ...doc, repos: [row, ...repos.slice(1)] }
}

describe('the Budget kind', () => {
  it('is one kind in the Budget group, drawn from one read', () => {
    const kind = budgetKindFor(SUPPORTED)
    expect(kind.id).toBe('budget')
    expect(kind.group).toBe('Budget')
    expect(kind.source).toBe('budget.show')
    // The columns are the table's whole width, in the order the shell draws
    // them: the repository, its two caps, the money counted against them and the
    // kill state. Every column names the class its cells pass under, because a
    // cell the sanitizer cannot place is a cell the shell would have to guess
    // about -- `repository` for a name, `label` for the CLI's own short rendered
    // fields, and `sentence` for the kill cell, which carries the document's own
    // reason text beside its attestation.
    expect(kind.columns.map((column) => column.key)).toEqual([
      'repository',
      'daily',
      'perRun',
      'counted',
      'left',
      'kill',
    ])
    // The money column is the CLI's `counted_usd` and is headed with the CLI's
    // own word for it: `counted` is what the caps and the headroom beside it are
    // measured against, while `spent_usd` is the billed figure the caps panel
    // draws as `billed` -- so a column headed "spent" carrying the counted
    // figure would report every open lease as money charged.
    expect(kind.columns.map((column) => column.title)).toEqual([
      'Repository',
      'Daily cap',
      'Per-run cap',
      'Counted today',
      'Left today',
      'Kill switch',
    ])
    expect(kind.columns.map((column) => column.class)).toEqual([
      'repository',
      'label',
      'label',
      'label',
      'label',
      'sentence',
    ])
  })

  it('draws one row per repository: its caps with their scopes, the money, its kill state', () => {
    const rows = budgetKindFor(SUPPORTED).decode(aBudget())
    expect(rows.map((row) => row.key)).toEqual(['acme/app', 'acme/website'])

    // The first repository is the one where the two figures differ -- its
    // `counted_usd` is $3.50 against a `spent_usd` of $1.25, the $2.25 being a
    // lease no run has released -- so it is the row a case about which of the
    // two the column carries reads.
    expect(rows[0]?.cells).toEqual({
      repository: 'acme/app',
      daily: '$20.00 (repo)',
      perRun: '$4.00 (repo)',
      counted: '$3.50',
      left: '$16.50',
      kill: 'live',
    })

    // The second row's caps bind at other scopes -- a cap of $20 with no scope is
    // a figure nobody can act on, because the fix differs by scope -- and its
    // kill carries the three facts the document has, in the shape the Live
    // screen's banner and the Spend screen's cell draw.
    expect(rows[1]?.cells).toEqual({
      repository: 'acme/website',
      daily: '$25.00 (default)',
      perRun: '$5.00 (global)',
      counted: '$4.75',
      left: '$20.25',
      kill: 'KILLED by dana@example.invalid 2026-11-20T09:00:00Z the day’s spend reached the cap while a run was looping',
    })
  })

  it('draws the counted figure under the counted word, and not the billed one', () => {
    // `counted_usd` is the money the caps and the headroom beside it are
    // measured against, and `spent_usd` -- the billed figure, which the caps
    // panel draws as `billed` -- is a smaller number whenever a lease is open.
    // A column drawing the billed figure would leave a row whose money and
    // headroom cells did not add up to its cap, and one headed "spent" drawing
    // the counted figure would report every unreleased lease as money charged.
    const cells = rowCells(SUPPORTED, 0)
    expect(cells['counted']).toBe('$3.50')
    expect(JSON.stringify(cells)).not.toContain('1.25')
  })

  it('says a scope word this console does not know as unknown, and never the word itself', () => {
    const cells = rowCells(SUPPORTED, 0, withRepo({ ...repoAt(0), daily_cap_source: 'per-workspace' }))
    expect(cells['daily']).toBe('$20.00 (unknown)')
    // The CLI's word is a document's string and this build does not know it, so
    // it is drawn as the console's own word for the case and never as itself.
    expect(JSON.stringify(cells)).not.toContain('per-workspace')
  })

  it('draws a cap the document does not carry as n/a, never as a zero', () => {
    const cells = rowCells(SUPPORTED, 0, withRepo({ ...repoAt(0), daily_cap_usd: null, daily_cap_source: 'none' }))
    expect(cells['daily']).toBe('n/a (none)')
    // The scope is still drawn beside it: "no cap at this scope" is a fact the
    // operator can act on, and it is not the same as a cap of nothing.
    expect(cells['perRun']).toBe('$4.00 (repo)')
  })

  it('draws no alerts row, column or cell, whatever the document carries (D2909)', () => {
    // Alerts have no CLI object: nothing in `budget show` is an alert and no
    // command lists one. The owner declined them rather than have the console
    // invent a row, so a document that grew an `alerts` array is not read here at
    // all -- and the mutation this case catches is a kind that decoded one.
    const doc = { ...aBudget(), alerts: [{ repo: 'acme/app', kind: 'cap-reached', at: '2026-11-20T10:00:00Z' }] }
    const kind = budgetKindFor(SUPPORTED)
    expect(kind.columns.map((column) => column.title)).not.toContain('Alerts')
    expect(kind.columns.map((column) => column.key)).not.toContain('alerts')
    const rows = kind.decode(doc)
    for (const row of rows) {
      expect(Object.keys(row.cells)).toEqual(kind.columns.map((column) => column.key))
    }
    expect(JSON.stringify(rows)).not.toContain('cap-reached')
  })

  it('offers the Spend link and nothing that writes a cap', () => {
    // The descriptor is the whole control surface the shell draws, so "the cap
    // controls stay disabled" is exactly this list: the kind offers the Spend
    // report's own link (`entry.ts`'s `VIEWS` id `spend`) and no write action and
    // no Copy-command. A cap command names a scope and a figure the kind was
    // never handed, and a control the console draws and does not perform is
    // `writes.ts`'s `disabledWrite`, whose reason is `WRITE_UNAVAILABLE` -- this
    // kind neither draws one nor adds a second reason of its own.
    expect(budgetKindFor(SUPPORTED).actions).toEqual([{ kind: 'open-view', view: 'spend' }])
  })

  it('draws no rows at all when no document was read', () => {
    // The shell draws its own empty sentence for a kind with no rows; the kind's
    // half is that a missing document, a document with no repositories and a read
    // that answered some other shape are one answer rather than a row of
    // invented zeroes.
    expect(budgetKindFor(SUPPORTED).decode(null)).toEqual([])
    expect(budgetKindFor(SUPPORTED).decode({ repos: [] })).toEqual([])
    expect(budgetKindFor(SUPPORTED).decode('not a document')).toEqual([])
  })
})

describe('the Budget kind in the reduced mode', () => {
  it('withholds the cap figures and their scopes under the caps view', () => {
    const cells = rowCells(withHidden('caps'), 0)
    expect(cells['daily']).toBe(BUDGET_CAPS_HIDDEN_WORD)
    expect(cells['perRun']).toBe(BUDGET_CAPS_HIDDEN_WORD)
    // The figure and the scope are both gone: a withheld cap that left its scope
    // behind would still say which scope binds, read from a document this console
    // cannot vouch for.
    expect(JSON.stringify(cells)).not.toContain('20.00')
    // The other two views are not this one's question: the money and the kill
    // state are drawn, because the views that cover them are not withheld.
    expect(cells['counted']).toBe('$3.50')
    expect(cells['kill']).toBe('live')
  })

  it('withholds the money under the spend view, and the kill state under the kill view', () => {
    const money = rowCells(withHidden('spend'), 1)
    expect(money['counted']).toBe(BUDGET_SPEND_HIDDEN_WORD)
    expect(money['left']).toBe(BUDGET_SPEND_HIDDEN_WORD)
    expect(money['daily']).toBe('$25.00 (default)')
    expect(money['kill']).toContain('KILLED')

    const kill = rowCells(withHidden('kill'), 1)
    expect(kill['kill']).toBe(BUDGET_KILL_HIDDEN_WORD)
    expect(JSON.stringify(kill)).not.toContain('dana@example.invalid')
    expect(kill['counted']).toBe('$4.75')
  })

  it('withholds every figure of a fully reduced console, and the repository name never', () => {
    // The repository is not a figure: a row of nothing but withheld cells would
    // be a table nobody could read, and the name is the one cell no mode has a
    // reason to doubt.
    expect(rowCells(REDUCED, 0)).toEqual({
      repository: 'acme/app',
      daily: BUDGET_CAPS_HIDDEN_WORD,
      perRun: BUDGET_CAPS_HIDDEN_WORD,
      counted: BUDGET_SPEND_HIDDEN_WORD,
      left: BUDGET_SPEND_HIDDEN_WORD,
      kill: BUDGET_KILL_HIDDEN_WORD,
    })
  })

  it('withholds the Spend link with the spend view, which the rail does not offer either', () => {
    // A view this mode withholds is not offered at all (`entry.ts`), so a link to
    // it would be a door to nothing.
    expect(budgetKindFor(REDUCED).actions).toEqual([])
    expect(budgetKindFor(withHidden('spend')).actions).toEqual([])
    // A mode that withheld the caps alone still has its Spend tab, so the link
    // stays: the question is the tab's own view and not this table's.
    expect(budgetKindFor(withHidden('caps')).actions).toEqual([{ kind: 'open-view', view: 'spend' }])
  })

  it('says the caps with the Live screen’s own sentence, so one mode speaks with one voice', () => {
    // A second copy rather than an import -- the screens and the kinds are
    // siblings and none owns the others -- held equal here so the two cannot
    // drift: one reduced mode withholding the caps in two places under two
    // sentences would read as two modes.
    expect(BUDGET_CAPS_HIDDEN_WORD).toBe(LIVE_CAPS_HIDDEN_WORD)
  })
})
