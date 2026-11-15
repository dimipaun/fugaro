// website/src/lib/feature-matrix-model.mjs — the feature matrix's contract: the mapping shape,
// the snapshot shape, the fenced block FEATURES.md carries and the leak check
// (docs/feature-matrix/implementation-plan.md, task C1; the same shapes, written for a reader,
// in docs/feature-matrix/contract.md).
//
// Four tasks are built against this file — T1 builds the snapshot, T2 writes the mapping, T3
// renders FEATURES.md and T4a renders the page — and three of them start before any of them has
// run, so the shapes are frozen here rather than by whatever the first caller found convenient.
// Both validators refuse what they were not taught, naming the field: a matrix built from a
// mapping that grew a status or a plan the page has no mark for is a page that ships a row it
// cannot explain, and the refusal is the last place that is still cheap to see.
//
// **The fixture lives here rather than in the stub.** `FIXTURE_FILE` and `FIXTURE_SPECS` are the
// snapshot T3's and T4a's tests render and the spec names their leak checks read. The stub is
// deleted by I1 while those tests stay, so a test that reached the fixture through the stub would
// go with it.
//
// **The leak check is what keeps a spec name, a task id and a date off both outputs** (the
// reconciliation's R1 and R8). It matches a spec name as a whole hyphenated token, so the
// single-word spec names in status.md (`mobile`, `website`, `portal`, `plans`, ...) are ordinary
// words and are not matched; the three status.md section names it carries are the three F13
// criterion 3 names, not "Done", because "done" is ordinary English and a check that refused it
// would refuse honest copy. The stamp carries no date (`as_of: vX.Y.Z`), so every date found is
// reported rather than exempted.
//
// Standard library only and no network, as the rest of `src/lib/`: this module is imported by the
// site's build and by `node --test` alike.

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')

/** The statuses a row may carry, and nothing else (D2443, three of them). */
export const STATUSES = ['available', 'coming-soon', 'planned']

/** The plans, in the order the page's three columns are drawn (D2440). */
export const PLANS = ['community', 'team', 'enterprise']

/** The snapshot T3's and T4a's tests render, and the stub returns. */
export const FIXTURE_FILE = path.join(SITE, 'test', 'fixtures', 'feature-matrix', 'snapshot.json')

/** The fixture's own spec names: what its leak checks look for, and the whole of what they may not find. */
export const FIXTURE_SPECS = [
  'mobile',
  'website',
  'bitbucket-credentials',
  'bitbucket-token-types',
  'in-job-review',
  'no-prompt-policy',
  'run-without-checkout',
  'secrets-brokering',
  'task-governance',
  'vision-model',
]

/** Both shapes carry this version; a field that must change meaning gets a new name, not a new number (CLAUDE.md). */
const VERSION = 1

/** `coming-soon` is the only hold status: a hold holds a feature back, and only back to Coming soon (D2444). */
const HOLD_STATUSES = ['coming-soon']

const VERSION_TEXT = /^v\d+\.\d+\.\d+$/
const SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/

const MAPPING_KEYS = { required: ['version', 'features'], optional: ['excluded', 'retired'] }
const FEATURE_KEYS = { required: ['id', 'name', 'explanation', 'plan', 'specs'], optional: ['hold'] }
const HOLD_KEYS = { required: ['status', 'reason'], optional: [] }
const EXCLUDED_KEYS = { required: ['spec', 'reason'], optional: [] }
const SNAPSHOT_KEYS = { required: ['version', 'as_of', 'rows'], optional: [] }
const ROW_KEYS = { required: ['id', 'name', 'explanation', 'status', 'plans'], optional: [] }
const PLANS_KEYS = { required: PLANS, optional: [] }

const SECTION_NAMES = ['Nearly there', 'Work in progress', 'Parked']

/** The fence R5's block is written with, and the only fenced block FEATURES.md carries. */
const FENCE = '```'

// A task id in this repository's form: one to three capitals, a number, an optional dotted part
// and an optional trailing letter (`C1`, `T4a`, `W1.3`, `D2440`, `GV1.6`). The lookarounds keep it
// a whole token, so a hyphenated spec name is not read as one.
const TASK_ID = /(?<![A-Za-z0-9])[A-Z]{1,3}[0-9]+(?:\.[0-9]+)*[a-z]?(?![A-Za-z0-9])/g

const ISO_DATE = /(?<![0-9-])[0-9]{4}-[0-9]{2}-[0-9]{2}(?![0-9-])/g
const SPOKEN_DATE = /(?<![A-Za-z])(?:January|February|March|April|May|June|July|August|September|October|November|December) [0-9]{4}(?![0-9])/g

/** The fixture's snapshot, parsed and held to the snapshot shape: what T3, T4a and the stub render. */
export function readFixtureSnapshot() {
  const snapshot = JSON.parse(readFileSync(FIXTURE_FILE, 'utf8'))
  return validateSnapshot(snapshot)
}

/**
 * The mapping's shape, checked whole. Returns the mapping, so a caller can write
 * `const mapping = validateMapping(parseYAML(source))` and read nothing else.
 */
export function validateMapping(mapping) {
  const subject = 'mapping'
  object(subject, '', mapping)
  shape(subject, '', mapping, MAPPING_KEYS)
  version(subject, mapping)

  list(subject, 'features', mapping.features).forEach((feature, i) => {
    const at = `features[${i}]`
    object(subject, at, feature)
    shape(subject, at, feature, FEATURE_KEYS)
    slug(subject, `${at}.id`, feature.id)
    text(subject, `${at}.name`, feature.name)
    text(subject, `${at}.explanation`, feature.explanation)
    closed(subject, `${at}.plan`, feature.plan, PLANS)
    list(subject, `${at}.specs`, feature.specs)
    if (feature.specs.length === 0) {
      refuse(subject, `${at}.specs`, 'expected at least one spec; a feature no spec contributes to has no status to derive')
    }
    feature.specs.forEach((spec, j) => text(subject, `${at}.specs[${j}]`, spec))
    if (feature.hold !== undefined) {
      const held = `${at}.hold`
      object(subject, held, feature.hold)
      shape(subject, held, feature.hold, HOLD_KEYS)
      closed(subject, `${held}.status`, feature.hold.status, HOLD_STATUSES)
      text(subject, `${held}.reason`, feature.hold.reason)
    }
  })

  if (mapping.excluded !== undefined) {
    list(subject, 'excluded', mapping.excluded).forEach((entry, i) => {
      const at = `excluded[${i}]`
      object(subject, at, entry)
      shape(subject, at, entry, EXCLUDED_KEYS)
      text(subject, `${at}.spec`, entry.spec)
      text(subject, `${at}.reason`, entry.reason)
    })
  }

  if (mapping.retired !== undefined) {
    list(subject, 'retired', mapping.retired).forEach((id, i) => slug(subject, `retired[${i}]`, id))
  }

  return mapping
}

/** The snapshot's shape, checked whole. Returns the snapshot. */
export function validateSnapshot(snapshot) {
  const subject = 'snapshot'
  object(subject, '', snapshot)
  shape(subject, '', snapshot, SNAPSHOT_KEYS)
  version(subject, snapshot)

  if (typeof snapshot.as_of !== 'string' || !VERSION_TEXT.test(snapshot.as_of)) {
    refuse(subject, 'as_of', `expected the release the matrix was read against, written vX.Y.Z; found ${describe(snapshot.as_of)}`)
  }

  list(subject, 'rows', snapshot.rows).forEach((row, i) => {
    const at = `rows[${i}]`
    object(subject, at, row)
    shape(subject, at, row, ROW_KEYS)
    slug(subject, `${at}.id`, row.id)
    text(subject, `${at}.name`, row.name)
    text(subject, `${at}.explanation`, row.explanation)
    closed(subject, `${at}.status`, row.status, STATUSES)

    const plans = object(subject, `${at}.plans`, row.plans)
    shape(subject, `${at}.plans`, plans, PLANS_KEYS)
    for (const plan of PLANS) {
      if (typeof plans[plan] !== 'boolean') {
        refuse(subject, `${at}.plans.${plan}`, `expected true or false, found ${describe(plans[plan])}`)
      }
    }

    const lowest = PLANS.findIndex((plan) => plans[plan])
    if (lowest === -1) {
      refuse(subject, `${at}.plans`, 'no plan is set; a row every plan omits is a row the page draws as three empty cells')
    }
    for (const plan of PLANS.slice(lowest)) {
      if (!plans[plan]) {
        refuse(subject, `${at}.plans`, `${plan} is not set while ${PLANS[lowest]} is; a row carries its lowest plan and every plan above it (D2440)`)
      }
    }
  })

  return snapshot
}

/** The fenced block FEATURES.md carries, under its table (R5): the one place the page's rows come from. */
export function snapshotBlock(snapshot) {
  validateSnapshot(snapshot)
  return [`${FENCE}json`, JSON.stringify(canonical(snapshot), null, 2), FENCE].join('\n')
}

/** The snapshot back out of a FEATURES.md, held to the snapshot shape. */
export function readSnapshotBlock(markdown) {
  if (typeof markdown !== 'string') {
    throw new TypeError(`readSnapshotBlock reads a markdown document, and was given ${describe(markdown)}`)
  }

  const lines = markdown.split('\n')
  const bodies = []
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].trim() !== `${FENCE}json`) continue
    const body = []
    let j = i + 1
    for (; j < lines.length && lines[j].trim() !== FENCE; j++) body.push(lines[j])
    if (j === lines.length) throw new Error(`the markdown carries a ${FENCE}json block that is never closed`)
    bodies.push(body.join('\n'))
    i = j
  }

  if (bodies.length === 0) throw new Error(`the markdown carries no ${FENCE}json block, so it holds no snapshot`)
  if (bodies.length > 1) {
    throw new Error(`the markdown carries ${bodies.length} ${FENCE}json blocks; the snapshot block is the only fenced block FEATURES.md has`)
  }

  let snapshot
  try {
    snapshot = JSON.parse(bodies[0])
  } catch (err) {
    throw new Error(`the snapshot block is not JSON: ${err.message}`)
  }
  return validateSnapshot(snapshot)
}

/**
 * Every leak in `text`: a hyphenated spec name out of `specNames` found as a whole token, one of
 * status.md's section names, a task id, or a date (R1, R8). An empty list is the pass.
 *
 * The text is read as given. A caller checking built HTML passes the page's own text, because
 * markup is not copy — an inline `<path d="M34 178">` is a coordinate, not a task id.
 */
export function leakProblems(text, specNames) {
  if (typeof text !== 'string') {
    throw new TypeError(`leakProblems reads the text to check, and was given ${describe(text)}`)
  }
  if (!Array.isArray(specNames)) {
    throw new TypeError(`leakProblems reads the spec names to look for, and was given ${describe(specNames)}`)
  }

  const problems = []
  const add = (what) => {
    if (!problems.includes(what)) problems.push(what)
  }

  for (const name of specNames) {
    if (typeof name !== 'string' || !name.includes('-')) continue
    for (const match of text.matchAll(whole(name))) add(`the spec name ${JSON.stringify(match[0])}`)
  }
  for (const name of SECTION_NAMES) {
    for (const match of text.matchAll(whole(name))) add(`the status page's section name ${JSON.stringify(match[0])}`)
  }
  for (const match of text.matchAll(TASK_ID)) add(`a task id, ${JSON.stringify(match[0])}`)
  for (const match of text.matchAll(ISO_DATE)) add(`a date, ${JSON.stringify(match[0])}`)
  for (const match of text.matchAll(SPOKEN_DATE)) add(`a date, ${JSON.stringify(match[0])}`)

  return problems
}

function canonical(snapshot) {
  return {
    version: snapshot.version,
    as_of: snapshot.as_of,
    rows: snapshot.rows.map((row) => ({
      id: row.id,
      name: row.name,
      explanation: row.explanation,
      status: row.status,
      plans: { community: row.plans.community, team: row.plans.team, enterprise: row.plans.enterprise },
    })),
  }
}

function whole(name) {
  return new RegExp(`(?<![\\w-])${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?![\\w-])`, 'gi')
}

function refuse(subject, field, message) {
  throw new Error(`the feature matrix ${subject}:${field ? ` ${field}:` : ''} ${message}`)
}

function describe(value) {
  if (value === null) return 'null'
  if (Array.isArray(value)) return 'a list'
  if (typeof value === 'object') return 'an object'
  return `${typeof value} ${JSON.stringify(value)}`
}

function object(subject, field, value) {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    refuse(subject, field, `expected an object, found ${describe(value)}`)
  }
  return value
}

function list(subject, field, value) {
  if (!Array.isArray(value)) refuse(subject, field, `expected a list, found ${describe(value)}`)
  return value
}

function text(subject, field, value) {
  if (typeof value !== 'string' || value.trim() === '') {
    refuse(subject, field, `expected a non-empty string, found ${describe(value)}`)
  }
  return value
}

function slug(subject, field, value) {
  if (typeof value !== 'string' || !SLUG.test(value)) {
    refuse(subject, field, `expected a slug of lowercase words joined by hyphens, found ${describe(value)}`)
  }
  return value
}

function closed(subject, field, value, allowed) {
  if (!allowed.includes(value)) {
    refuse(subject, field, `${JSON.stringify(value)} is not one of ${allowed.join(', ')}`)
  }
  return value
}

function version(subject, value) {
  if (value.version !== VERSION) {
    refuse(subject, 'version', `expected ${VERSION}, found ${describe(value.version)}`)
  }
}

function shape(subject, field, value, { required, optional }) {
  const allowed = [...required, ...optional]
  const unknown = Object.keys(value).filter((key) => !allowed.includes(key))
  if (unknown.length > 0) {
    refuse(subject, field, `unknown ${unknown.length === 1 ? 'key' : 'keys'} ${unknown.map((key) => JSON.stringify(key)).join(', ')}; the shape is ${allowed.join(', ')}`)
  }
  for (const key of required) {
    if (!(key in value)) refuse(subject, field ? `${field}.${key}` : key, 'is missing')
  }
}
