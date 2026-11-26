// website/src/lib/feature-matrix.mjs — the feature matrix's builder: docs/status.md and the
// hand-kept mapping in, one snapshot out (docs/feature-matrix/implementation-plan.md, task T1;
// the spec as docs/feature-matrix/reconciliation.md amends it, with the owner's answers
// D2443–D2445).
//
// **What it reads.** status.md's five tables, found by heading, and the Spec cell of every row of
// them, split on commas: a name in one of those cells is a spec, and a spec sits in one table.
// The state a row gives its spec comes from the table — Nearly There and Work in progress have
// started, UAT and Done are done (UAT is built and waits for the owner's acceptance) — and, in "To do or parked", from the State cell: a cell that says the
// owner parked the spec parks it (and a parked spec keeps its merged count, so it is read before
// the count is); a cell that opens with a merged count above zero means the spec has started; and
// a cell that says neither leaves it not started, because a row in that table with no count on it
// is a row with nothing merged. Everything else in a row — the Left column, the prose between the
// tables, the rule comment at the top of the page — is prose, and nothing here reads or judges it.
//
// **What it refuses, rather than reading as the nearest thing it could have meant.** F8: this is
// the page's first machine reader, so a table, a row or an edge it has not been taught stops it
// and names the line. Three disagreements between the page and the mapping stop it as well, and
// each lists *every* name it found rather than the first, because one run of the generator is one
// trip the notes PR pays for (F13 criterion 1, R2, R7):
//
//   1. a name in a status.md row that the mapping neither gives to a feature nor excludes;
//   2. a name the mapping carries that status.md no longer does, on either side of the mapping —
//      a feature's spec or an excluded entry;
//   3. a row the previous FEATURES.md had Available that this snapshot would take back or drop —
//      unless a `hold` was added to the row, or its id is listed under `retired`, which are the
//      two ways the mapping says the move is deliberate (D2445, R7).
//
// **What it never does.** It writes no part of status.md (R3); it is the only module in the site
// that reads the page, and `npm run check` reads no part of it, so a status sweep can never turn
// the website red (R2); and it derives no row's words — a name and an explanation are the
// mapping's, and the status is the one thing this module works out (D2441, F1). Ids, names and
// explanations reach FEATURES.md and the page as the mapping wrote them; the leak check that keeps
// a spec name out of them is T2's over the mapping and T3's and T4a's over their own output.

import { PLANS, readSnapshotBlock, validateMapping, validateSnapshot } from './feature-matrix-model.mjs'

/** The one document this module reads, and the path a refusal names (R3). */
const STATUS_FILE = 'docs/status.md'

/**
 * The five tables, in the page's own words, with the columns each carries and whether its rows
 * carry a spec that has started, a spec that is done, or the State cell this module reads.
 */
const SECTIONS = [
  { heading: 'UAT', columns: ['Spec', 'Merged', 'Left'], kind: 'done' },
  { heading: 'Nearly There (NT)', columns: ['Spec', 'Merged', 'Left'], kind: 'started' },
  { heading: 'Work in progress (WIP)', columns: ['Spec', 'Merged', 'Left'], kind: 'started' },
  { heading: 'To do or parked', columns: ['Spec', 'State'], kind: 'todo' },
  { heading: 'Done', columns: ['Spec', 'Left'], kind: 'done' },
]

/** A section heading, and the row of dashes a markdown table carries under its header. */
const HEADING = /^##\s+(.*?)\s*$/
const SEPARATOR = /^:?-{3,}:?$/

/**
 * The two things "To do or parked" says about a spec (rule 5): the owner's words for a spec they
 * parked, and the merged count a row opens with — `2/5 merged ...`, the shape rule 1 gives that
 * column, so a cell that says nothing about merged work carries no count at all.
 */
const PARKED = /^parked by the owner\b/
const MERGED = /^(?:about )?(\d+)\/(\d+)/

/**
 * The snapshot FEATURES.md is rendered from and the page reads, in the contract's shape.
 *
 * `statusMarkdown` is docs/status.md's text, `mapping` the parsed mapping, `asOf` the release
 * being stamped, and `previous` the snapshot of the FEATURES.md already on `main` — the object
 * `readSnapshotBlock` returns, or the file's text, which is read here so that the block's own
 * refusals stay in one place — or `null` for the first generation, which skips the regression
 * check (R7).
 */
export function buildSnapshot({ statusMarkdown, mapping, previous, asOf }) {
  const features = validateMapping(mapping).features
  const specs = readStatus(statusMarkdown)

  refuse(`the mapping and ${STATUS_FILE} disagree`, coverageProblems(specs, mapping))

  const rows = []
  for (const feature of features) {
    const status = statusOf(feature, specs)
    if (status === null) continue
    rows.push({
      id: feature.id,
      name: feature.name,
      explanation: feature.explanation,
      status,
      plans: plansOf(feature.plan),
    })
  }

  refuse('the snapshot would move rows the previous FEATURES.md had available', regressionProblems(previousOf(previous), rows, mapping, specs))

  // The shape's version is the contract's own: `validateSnapshot` refuses anything but 1, so a
  // second snapshot shape is a second contract and not a number to raise here (C1).
  return validateSnapshot({ version: 1, as_of: asOf, rows })
}

/** Every name status.md carries, with the state its row gives it and the line it sits on. */
function readStatus(markdown) {
  if (typeof markdown !== 'string') {
    throw new TypeError(`the feature matrix builder reads ${STATUS_FILE}'s text, and was given ${describe(markdown)}`)
  }

  const lines = markdown.split('\n')
  const sections = []
  let open = null
  for (let i = 0; i < lines.length; i++) {
    const heading = HEADING.exec(lines[i])
    if (heading !== null) {
      open = { heading: heading[1], line: i, body: [] }
      sections.push(open)
      continue
    }
    if (open !== null) open.body.push(i)
  }

  const found = new Map()
  for (const section of sections) {
    if (!SECTIONS.some((known) => known.heading === section.heading)) {
      throw new Error(
        `${where(section.line)}: ${JSON.stringify(section.heading)} is not one of the five sections the builder reads (${SECTIONS.map((known) => JSON.stringify(known.heading)).join(', ')}); a status page that gained or renamed a section is one it has not been taught, and it stops rather than guessing at it (F8)`,
      )
    }
    if (found.has(section.heading)) {
      throw new Error(`${where(section.line)}: ${JSON.stringify(section.heading)} is the second section with that heading; the builder reads the five tables by heading, and two of them cannot be told apart`)
    }
    found.set(section.heading, section)
  }

  for (const known of SECTIONS) {
    if (!found.has(known.heading)) {
      throw new Error(
        `${STATUS_FILE} carries no ${JSON.stringify(known.heading)} section: the builder reads the five tables by heading, and a table it cannot find is one it will not guess at (F8, R3)`,
      )
    }
  }

  const specs = new Map()
  for (const known of SECTIONS) readTable(known, found.get(known.heading), lines, specs)
  return specs
}

/** One table, held to the columns it declares, and its rows' names into `specs`. */
function readTable(known, section, lines, specs) {
  let header = -1
  let separator = -1
  const body = []
  let ended = false

  for (const i of section.body) {
    const line = lines[i]
    if (line.startsWith('|')) {
      if (ended) {
        throw new Error(`${where(i)}: a second table under ${JSON.stringify(known.heading)}; a section carries one table, and this module reads the five it knows`)
      }
      if (header === -1) header = i
      else if (separator === -1) separator = i
      else body.push(i)
      continue
    }
    if (separator !== -1 && line.trim() !== '') ended = true
  }

  if (header === -1) {
    throw new Error(`${where(section.line)}: the ${JSON.stringify(known.heading)} section carries no table; the five tables are how this module reads the page (F8)`)
  }

  const cells = rowOf(lines, header)
  if (!same(cells, known.columns)) {
    throw new Error(
      `${where(header)}: the ${JSON.stringify(known.heading)} table's header reads ${print(cells)}; the table this module reads is ${print(known.columns)}`,
    )
  }

  if (separator === -1) {
    throw new Error(`${where(header)}: the ${JSON.stringify(known.heading)} table's header is followed by no separator row; the row of dashes is what tells a header from a row of specs`)
  }

  const dashes = rowOf(lines, separator)
  if (dashes.length !== known.columns.length || !dashes.every((cell) => SEPARATOR.test(cell))) {
    throw new Error(`${where(separator)}: the line under the ${JSON.stringify(known.heading)} table's header reads ${print(dashes)}, which is not a separator row of ${known.columns.length}`)
  }

  for (const i of body) {
    const cells = rowOf(lines, i)
    if (cells.length !== known.columns.length) {
      throw new Error(
        `${where(i)}: the row reads ${cells.length} cell${cells.length === 1 ? '' : 's'} where the ${JSON.stringify(known.heading)} table carries ${known.columns.length} (${print(known.columns)})`,
      )
    }
    for (const name of namesOf(cells[0], i)) {
      const already = specs.get(name)
      if (already !== undefined) {
        throw new Error(`${where(i)}: ${JSON.stringify(name)} sits in the row at line ${already.line} as well; a spec sits in one table, and a name in two has two states to choose between`)
      }
      specs.set(name, { state: stateOf(known, cells, i), line: i + 1 })
    }
  }
}

/**
 * The state a row gives its spec: `done`, `started`, `todo` or `parked`. The three tables that
 * are not "To do or parked" say it in their heading, and that table says it in its State cell.
 */
function stateOf(known, cells, i) {
  if (known.kind === 'done') return 'done'
  if (known.kind === 'started') return 'started'

  const state = cells[1]
  if (state === '') {
    throw new Error(`${where(i)}: the row's State cell is empty; the cell is what says whether the owner parked the spec, and an empty one says nothing (status.md's rule 5)`)
  }
  if (PARKED.test(state)) return 'parked'

  const merged = MERGED.exec(state)
  return merged !== null && Number(merged[1]) > 0 ? 'started' : 'todo'
}

/**
 * The status D2443 gives a feature, or `null` for a feature left off the matrix.
 *
 * A parked spec counts for nothing beside a spec that is not parked (R6), and a feature whose
 * specs are all parked is left off — a parked spec is not a promise — which is also why the
 * omission is decided before the mapping's `hold` is read: a hold says a built feature has not
 * shipped (D2444), and parked specs say there is nothing built to hold back.
 */
function statusOf(feature, specs) {
  const live = feature.specs.filter((spec) => specs.get(spec).state !== 'parked')
  if (live.length === 0) return null
  if (feature.hold !== undefined) return 'coming-soon'
  if (live.every((spec) => specs.get(spec).state === 'done')) return 'available'
  if (live.every((spec) => specs.get(spec).state === 'todo')) return 'planned'
  return 'coming-soon'
}

/** The row's three plans: the lowest one the mapping names, and every plan above it (D2440). */
function plansOf(plan) {
  const lowest = PLANS.indexOf(plan)
  return Object.fromEntries(PLANS.map((name, i) => [name, i >= lowest]))
}

/**
 * What status.md and the mapping say about each other, name by name: a status.md name with no
 * mapping entry, and a mapping name status.md does not carry (F13 criterion 1, R2).
 */
function coverageProblems(specs, mapping) {
  const mapped = new Map()
  for (const feature of mapping.features) {
    for (const spec of feature.specs) {
      if (!mapped.has(spec)) mapped.set(spec, `the feature ${JSON.stringify(feature.id)}`)
    }
  }
  for (const entry of mapping.excluded ?? []) {
    if (!mapped.has(entry.spec)) mapped.set(entry.spec, 'the excluded list')
  }

  const problems = []

  const unmapped = [...specs].filter(([name]) => !mapped.has(name))
  if (unmapped.length > 0) {
    problems.push(
      `${STATUS_FILE} carries ${count(unmapped.length, 'name')} the mapping neither gives to a feature nor excludes, so nothing says whether ${unmapped.length === 1 ? 'it is' : 'they are'} a feature: ${unmapped.map(([name, spec]) => `${name} (line ${spec.line})`).join(', ')}`,
    )
  }

  const missing = [...mapped].filter(([name]) => !specs.has(name))
  if (missing.length > 0) {
    problems.push(
      `the mapping carries ${count(missing.length, 'name')} that ${STATUS_FILE} does not, so the row built from ${missing.length === 1 ? 'it' : 'them'} has no status to derive: ${missing.map(([name, place]) => `${name} (${place})`).join(', ')}`,
    )
  }

  return problems
}

/**
 * Every row the previous snapshot had Available that this one takes back or drops, unless the
 * mapping says the move was deliberate — a `hold` added to the row, or its id under `retired`
 * (D2445, R7). With no previous snapshot there is nothing to regress against, and the check is
 * skipped.
 */
function regressionProblems(previous, rows, mapping, specs) {
  if (previous === null) return []

  const retired = new Set(mapping.retired ?? [])
  const held = new Set(mapping.features.filter((feature) => feature.hold !== undefined).map((feature) => feature.id))
  const features = new Map(mapping.features.map((feature) => [feature.id, feature]))
  const now = new Map(rows.map((row) => [row.id, row]))

  const problems = []
  for (const row of previous.rows) {
    if (row.status !== 'available' || retired.has(row.id)) continue
    const feature = features.get(row.id)
    if (now.has(row.id)) {
      if (now.get(row.id).status === 'available' || held.has(row.id)) continue
      problems.push(
        `${row.id}: available in the previous snapshot and ${now.get(row.id).status} now, because ${causeOf(feature, specs)}; a row that was available never comes back as Coming soon or Planned, and a hold on the row is the one deliberate exception (D2445, R7)`,
      )
      continue
    }
    problems.push(
      `${row.id}: available in the previous snapshot, and this one leaves the row off because ${causeOf(feature, specs)}; a row that was available is retired in the mapping when it is dropped on purpose (R7)`,
    )
  }
  return problems
}

/** Why the row is not Available: the specs that are not Done, or that every one of them is parked. */
function causeOf(feature, specs) {
  if (feature === undefined) return 'the mapping carries no feature with that id'
  const live = feature.specs.filter((spec) => specs.get(spec).state !== 'parked')
  if (live.length === 0) return `every one of its specs is parked (${feature.specs.join(', ')})`
  const notDone = live.filter((spec) => specs.get(spec).state !== 'done')
  return `${notDone.join(', ')} ${notDone.length === 1 ? 'is' : 'are'} not Done`
}

/** The previous snapshot, read from the caller's object or from FEATURES.md's own text (R5, R7). */
function previousOf(previous) {
  if (previous === null) return null
  if (typeof previous === 'string') return readSnapshotBlock(previous)
  return validateSnapshot(previous)
}

function refuse(headline, problems) {
  if (problems.length === 0) return
  throw new Error([`${headline}:`, ...problems.map((problem) => `  - ${problem}`)].join('\n'))
}

/** One table row's cells. A row that does not open and close with a pipe is a line to stop at. */
function rowOf(lines, i) {
  const line = lines[i].trimEnd()
  if (!line.endsWith('|')) {
    throw new Error(`${where(i)}: the line opens a table row and does not close it with "|"; a row the builder cannot read stops it (F8)`)
  }
  return line.slice(1, -1).split('|').map((cell) => cell.trim())
}

/** The names in a Spec cell: one name each, split on commas, and never empty or two words. */
function namesOf(cell, i) {
  const names = cell.split(',').map((name) => name.trim())
  for (const name of names) {
    if (name === '') {
      throw new Error(`${where(i)}: the Spec cell carries an empty name; a row names at least one spec (status.md's rule 7)`)
    }
    if (/\s/.test(name)) {
      throw new Error(`${where(i)}: ${JSON.stringify(name)} carries a space, and a Spec cell holds one spec name per comma-separated part (F8)`)
    }
  }
  return names
}

function where(line) {
  return `${STATUS_FILE}:${line + 1}`
}

function count(n, noun) {
  return n === 1 ? `a ${noun}` : `${n} ${noun}s`
}

function same(cells, columns) {
  return cells.length === columns.length && cells.every((cell, i) => cell === columns[i])
}

function print(cells) {
  return `| ${cells.join(' | ')} |`
}

function describe(value) {
  if (value === null) return 'null'
  if (Array.isArray(value)) return 'a list'
  if (typeof value === 'object') return 'an object'
  return `${typeof value} ${JSON.stringify(value)}`
}
