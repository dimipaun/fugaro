// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Watch-versus-Live field table and its test (console-improvements plan
// LP1; reconciliation F1, D2901).
//
// F1 says what is missing: the Live pane already draws from `watch --json` and
// follows Watch's ordering, short IDs and state words (`live-parity.test.ts`),
// but there is **no table of Watch's fields against Live's**, so the spec's "at
// least what Watch shows" cannot be tested today. The table is
// `docs/console-improvements/live-watch-fields.md`; this file is what makes it
// a check rather than a document, in three directions:
//
//   - **the struct is the authority.** Every `json:"…"` member of `JSONDoc` and
//     of each of its row types -- read as text off `internal/watch/json.go`,
//     the way `schema.test.ts` reads the keys it decodes -- has a row in the
//     table. So a member the CLI adds and the fixture does not carry (it is
//     `omitempty`, or the fixture records one view) is still caught: it has no
//     row, and the failure names it. That is the direction F1 is about: the
//     next `watch --json` field must be *decided about*, never silently absent
//     from Live.
//   - **the recording is the second half.** Every member the parity fixture
//     (`test/fixtures/watch-parity.json`, recorded by
//     `internal/watch/console_parity_test.go` from watch's own renderers)
//     actually carries has a row too, walked at every depth the document nests.
//   - **a drawn row names an element the renderer really draws.** The Live
//     element in each row is a class read off `console/src/renderer/screens/live.ts`
//     -- not a hand-written claim -- and a row that draws nothing says
//     `not drawn` with its reason. A row that names a class live.ts does not
//     carry fails here, and so does a row that says `not drawn` and gives no
//     reason.
//
// A member with no row is a **gap the table names**, which becomes a task by
// amendment (the plan's own words): this file reports it, it never invents one.
//
// The check can fail, and the last test proves it in the only way a check of a
// check can be proven: it plants a member on a copy of the fixture's own
// document and asserts the walk names it.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/** WATCH_SOURCE is `internal/watch/json.go`, read from this file's own location
 * (the console tree sits one level inside the repository). It is the shortcut
 * the json tags travel: a struct member's own key, which is what the table's
 * field column names. */
const WATCH_SOURCE = readFileSync(
  fileURLToPath(new URL('../../../internal/watch/json.go', import.meta.url)),
  'utf8',
)

/** LIVE_SOURCE is `console/src/renderer/screens/live.ts`: the renderer half of
 * the table's element column, read so a row's claim that Live draws a member is
 * checkable. */
const LIVE_SOURCE = readFileSync(
  fileURLToPath(new URL('../../src/renderer/screens/live.ts', import.meta.url)),
  'utf8',
)

/** TABLE is the table itself: `docs/console-improvements/live-watch-fields.md`. */
const TABLE = readFileSync(
  fileURLToPath(new URL('../../../docs/console-improvements/live-watch-fields.md', import.meta.url)),
  'utf8',
)

/** FIXTURE is the recorded parity fixture, the `watch --json` frame the CLI
 * would print beside the plain frame for the same view. */
const FIXTURE = JSON.parse(
  readFileSync(fileURLToPath(new URL('../fixtures/watch-parity.json', import.meta.url)), 'utf8'),
) as { readonly document: Record<string, unknown> }

/**
 * STRUCTS is the document's whole shape, one entry per struct of
 * `internal/watch/json.go` and one per place a document puts it: `JSONBurn` and
 * `JSONKill` hang under both `total` and a repository, and `JSONFinished` is
 * both lists of finished runs. The `at` is the field column's own notation --
 * `repos[].burn`, spelled exactly as `live.ts` spells the wire path of a field
 * it draws.
 */
const STRUCTS: readonly { readonly type: string; readonly at: string }[] = [
  { type: 'JSONDoc', at: '' },
  { type: 'JSONConn', at: 'connection' },
  { type: 'JSONTotal', at: 'total' },
  { type: 'JSONBurn', at: 'total.burn' },
  { type: 'JSONBurn', at: 'repos[].burn' },
  { type: 'JSONKill', at: 'total.kill' },
  { type: 'JSONKill', at: 'repos[].kill' },
  { type: 'JSONRepo', at: 'repos[]' },
  { type: 'JSONRun', at: 'runs[]' },
  { type: 'JSONDrift', at: 'drift[]' },
  { type: 'JSONFinished', at: 'finished[]' },
  { type: 'JSONFinished', at: 'ready[]' },
]

/** tagsOf is every `json:"…"` key of one struct in `source`, in declaration
 * order. A `json:"-"` field writes no key. It throws rather than answering an
 * empty list: a pin that silently finds nothing would check nothing. */
function tagsOf(source: string, name: string): readonly string[] {
  const start = source.indexOf(`type ${name} struct {`)
  if (start < 0) throw new Error(`internal/watch/json.go has no \`type ${name} struct\``)
  const end = source.indexOf('\n}', start)
  if (end < 0) throw new Error(`the ${name} struct is not closed at column 0, so its tags cannot be read`)
  const keys: string[] = []
  for (const match of source.slice(start, end).matchAll(/`json:"([^"]*)"/g)) {
    const key = (match[1] ?? '').split(',')[0]
    if (key === undefined || key === '' || key === '-') continue
    keys.push(key)
  }
  if (keys.length === 0) throw new Error(`the ${name} struct carries no json tags to read`)
  return keys
}

/** pathsOfStructs is every field path the document's structs write: the `at` of
 * each place, joined to each of the struct's own keys. */
function pathsOfStructs(): readonly string[] {
  return STRUCTS.flatMap(({ type, at }) => tagsOf(WATCH_SOURCE, type).map((key) => (at === '' ? key : `${at}.${key}`)))
}

/** isPlainObject is whether a JSON value is an object this walk descends into.
 * `null` is a value the document ships (`kill`, `percent`) and not an object. */
function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** pathsInDocument is every member path a decoded document carries, at every
 * depth: an object's keys, and an array's elements under the array's own path
 * with `[]` on it -- the table's notation, where a row is about the member and
 * not about one element's index. An empty list still contributes its own path
 * (`finished`, `ready`), because the member is present and the table owes it a
 * row whether or not this recording filled it. */
function pathsInDocument(document: Record<string, unknown>): readonly string[] {
  const paths: string[] = []
  const walk = (node: Record<string, unknown>, at: string): void => {
    for (const [key, value] of Object.entries(node)) {
      const path = at === '' ? key : `${at}.${key}`
      paths.push(path)
      if (isPlainObject(value)) walk(value, path)
      else if (Array.isArray(value)) for (const item of value) if (isPlainObject(item)) walk(item, `${path}[]`)
    }
  }
  walk(document, '')
  return paths
}

/**
 * Row is one line of the table: the field it is about and its element cell.
 * A row's field is written in backticks, so prose above and below the table --
 * which names fields too -- is not read as one.
 */
interface Row {
  readonly field: string
  readonly element: string
}

/** NOT_DRAWN is how a row says Live draws nothing for a member; the reason
 * follows it, and a row that says it with no reason fails. */
const NOT_DRAWN = 'not drawn'

/** rows is the table's rows, in the order it lists them. A line is a row when
 * it is a table line whose first cell is a backticked field path. */
function rows(): readonly Row[] {
  const found: Row[] = []
  for (const line of TABLE.split('\n')) {
    if (!line.startsWith('|')) continue
    const cells = line.split('|').map((cell) => cell.trim())
    // A table line is `| field | element |`: split on `|` leaves an empty first
    // and last cell.
    const field = cells[1] ?? ''
    const element = cells.slice(2, -1).join(' | ')
    const name = /^`([^`]+)`$/.exec(field)?.[1]
    if (name === undefined || element === '') continue
    found.push({ field: name, element })
  }
  return found
}

/** tableFields is the field each row is about. */
function tableFields(): readonly string[] {
  return rows().map((row) => row.field)
}

/** uncovered is the member paths `fields` has no row for, in the order they
 * were walked: the naming the plan asks the check to produce. */
function uncovered(fields: readonly string[], paths: readonly string[]): readonly string[] {
  const known = new Set(fields)
  return paths.filter((path) => !known.has(path))
}

/** drawnBy is the class a row's element cell names, or null when the row says
 * Live draws nothing. A drawn cell opens with one backticked class and may
 * explain itself after it. */
function drawnBy(row: Row): string | null {
  if (row.element.startsWith(NOT_DRAWN)) return null
  const named = /^`([^`]+)`/.exec(row.element)?.[1]
  if (named === undefined) {
    throw new Error(`the row for \`${row.field}\` names no Live element and does not say \`${NOT_DRAWN}\`: ${row.element}`)
  }
  return named
}

describe('the Watch-versus-Live field table', () => {
  it('carries every member of the watch document’s structs', () => {
    const missing = uncovered(tableFields(), pathsOfStructs())
    expect(
      missing,
      'a field of internal/watch/json.go has no row in docs/console-improvements/live-watch-fields.md: ' +
        'give it the Live element that draws it, or `not drawn` and the reason',
    ).toEqual([])
  })

  it('carries every member the parity fixture ships', () => {
    const missing = uncovered(tableFields(), pathsInDocument(FIXTURE.document))
    expect(missing, 'the recorded fixture carries a field the table has no row for').toEqual([])
  })

  it('names a member the table has no row for (the check can fail)', () => {
    const planted: Record<string, unknown> = structuredClone(FIXTURE.document)
    const runs = planted.runs
    if (!Array.isArray(runs) || !isPlainObject(runs[0])) throw new Error('the fixture carries no first run to plant on')
    runs[0] = { ...runs[0], invented_member: 'a member the table does not know' }
    expect(uncovered(tableFields(), pathsInDocument(planted))).toEqual(['runs[].invented_member'])
  })

  it('names no field the watch document does not have', () => {
    const known = new Set(pathsOfStructs())
    const extra = tableFields().filter((field) => !known.has(field))
    expect(extra, 'the table has a row for a field internal/watch/json.go does not write').toEqual([])
  })

  it('draws each member or says why not, in an element live.ts draws', () => {
    const wrong: string[] = []
    for (const row of rows()) {
      const drawn = drawnBy(row)
      if (drawn === null) {
        if (!/^not drawn\s+—\s+\S/.test(row.element)) {
          wrong.push(`${row.field}: says "not drawn" with no reason`)
        }
        continue
      }
      // The class is read off the renderer as a quoted literal: a row that named
      // an element nobody draws would be a second hand-written claim, which is
      // what the table exists to replace.
      if (!LIVE_SOURCE.includes(`'${drawn}'`) && !LIVE_SOURCE.includes(`"${drawn}"`)) {
        wrong.push(`${row.field}: names ${drawn}, which live.ts does not draw`)
      }
    }
    expect(wrong).toEqual([])
  })
})
