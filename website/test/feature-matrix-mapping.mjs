// website/test/feature-matrix-mapping.mjs — the feature matrix's mapping file
// (docs/feature-matrix/implementation-plan.md, task T2; the spec as
// docs/feature-matrix/reconciliation.md amends it, with the owner's answers D2440–D2445 of
// 2026-11-15).
//
// `docs/feature-matrix/mapping.yaml` is the one place a feature's name, grouping and plan are
// written, and the generator reads it to build FEATURES.md and the `/features/` page (D2441,
// F1). The page writes no claim of its own, so this file is the page's only honest gate until
// the page exists, and it holds four things:
//
//   1. **The shape.** The keys each level may carry, the closed plan set (D2440) and the one
//      hold status there is (D2444). A file that grows a fourth plan, a second hold status or
//      a key nothing reads fails here rather than rendering something the page has no column
//      or mark for.
//   2. **Ids and specs are unique.** An id never changes (R7), so a second row carrying one is
//      a row that cannot be told from the first; and a spec belongs to exactly one feature or
//      to the excluded list, never to two rows (F13 criterion 1).
//   3. **One or two sentences.** The page gives each feature a name and a short explanation,
//      and a longer one is a row the page's table cannot hold.
//   4. **No spec name leaks.** No id, name or explanation carries a hyphenated spec name as a
//      whole token (R1, R8). The spec names are read from this file's own `specs` and
//      `excluded` lists, which is where the matrix names every spec it knows; no part of
//      status.md is read here, so no status.md sweep can turn this file red (R2).
//
// The reader below is this file's own and reads the mapping **literally**, with the site's
// `parseYAML`, the way `test/compatibility.mjs` reads its data file: the closed sets are
// written here rather than imported from the snapshot contract, because a gate that read the
// file through the very loader it is checking would not see the loader's own mistake. It
// refuses what it has not been taught rather than guessing at it, and every refusal names the
// file, the row and the field.
//
// What this file does **not** check: that every name in every row of docs/status.md is mapped
// or excluded. That is the generator's own refusal when it writes, and it is proved once by the
// run that writes FEATURES.md (F11, F13 criterion 1) — a standing test here would read
// status.md, which R2 forbids.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO } from './lib/html.mjs'
import { parseYAML } from '../src/content.ts'

/** The file every message below names. */
const MAPPING = 'docs/feature-matrix/mapping.yaml'

/** The three plans, and nothing else; a row names the lowest one that includes it (D2440). */
const PLANS = ['community', 'team', 'enterprise']

/** The three statuses a snapshot row may carry (C1's closed set), and nothing else. */
const STATUSES = ['available', 'coming-soon', 'planned']

/** The one status a `hold` may carry: a held feature is Coming soon (D2444). */
const HOLD_STATUSES = ['coming-soon']

/** The keys each level of the file may carry; anything else is refused, not ignored. */
const KEYS = {
  root: ['version', 'features', 'excluded', 'retired'],
  feature: ['id', 'name', 'explanation', 'plan', 'specs', 'hold'],
  hold: ['status', 'reason'],
  excluded: ['spec', 'reason'],
}

/** An id, and a status.md name: lowercase words joined by single hyphens. */
const NAME_SHAPED = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/

/** The sentences of a text: what stands between its full stops, question marks and bangs. */
function sentences(text) {
  return text
    .split(/[.!?]+(?:\s+|$)/)
    .map((part) => part.trim())
    .filter((part) => part !== '')
}

/**
 * Whether `name` appears in `text` as a whole token. A hyphen is part of the token on both
 * sides, so a spec name inside a longer hyphenated word is not a leak of the spec name, and a
 * single-word spec name (`mobile`, `website`, `portal`) is an ordinary English word and is
 * never matched — which is why only the hyphenated names are searched for (R1, R8).
 */
function wholeToken(text, name) {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp(`(?<![\\w-])${escaped}(?![\\w-])`).test(text)
}

/** The keys of a mapping that are not the ones it may carry, named for a message. */
function unknownKeys(map, allowed, what) {
  return Object.keys(map ?? {})
    .filter((key) => !allowed.includes(key))
    .map((key) => `${what} carries the key "${key}"`)
}

/**
 * Every way the mapping departs from the rules above, as lines — the whole gate, so that a
 * plant below can be held to the one line it is about.
 */
function problems(doc) {
  const out = []
  if (doc.version !== 1) {
    out.push(`${MAPPING} writes version ${JSON.stringify(doc.version)}; this file's format version is 1`)
  }
  out.push(...unknownKeys(doc, KEYS.root, MAPPING))
  if (!Array.isArray(doc.retired)) {
    out.push(`${MAPPING} carries no retired list; R7 reads it to tell a row that was deliberately retired from one that went missing`)
  }
  const features = Array.isArray(doc.features) ? doc.features : []
  const excluded = Array.isArray(doc.excluded) ? doc.excluded : []
  if (features.length === 0) out.push(`${MAPPING} carries no feature, and a matrix with no row is a page with no table`)
  if (excluded.length === 0) {
    out.push(`${MAPPING} carries no excluded entry, and every status.md name that is not a feature's spec belongs here with its reason (F6)`)
  }

  /** Every status.md name the file claims: the features' specs and the excluded entries. */
  const specNames = []

  for (const [at, feature] of features.entries()) {
    if (feature === null || typeof feature !== 'object' || Array.isArray(feature)) {
      out.push(`${MAPPING}: the item at index ${at} of features is not a feature`)
      continue
    }
    const where = `${MAPPING}: the feature ${JSON.stringify(feature.id ?? `at index ${at}`)}`
    out.push(...unknownKeys(feature, KEYS.feature, where))
    if (typeof feature.id !== 'string' || !NAME_SHAPED.test(feature.id)) {
      out.push(`${where} carries the id ${JSON.stringify(feature.id)}; an id is a user-language slug, as in run-a-task (R8)`)
    }
    if (typeof feature.name !== 'string' || feature.name.trim() === '') {
      out.push(`${where} carries no name, and the name is what a reader of the page sees`)
    }
    if (typeof feature.explanation !== 'string' || feature.explanation.trim() === '') {
      out.push(`${where} carries no explanation`)
    } else if (sentences(feature.explanation).length > 2) {
      out.push(
        `${where} carries an explanation of ${sentences(feature.explanation).length} sentences; the page gives each feature one or two`,
      )
    }
    if (!PLANS.includes(feature.plan)) {
      out.push(`${where} carries the plan ${JSON.stringify(feature.plan)}; the three are ${JSON.stringify(PLANS)} (D2440)`)
    }
    if (!Array.isArray(feature.specs) || feature.specs.length === 0) {
      out.push(`${where} names no spec, and a feature's status is read from the specs it is built from (D2443)`)
    } else {
      for (const spec of feature.specs) {
        if (typeof spec !== 'string' || !NAME_SHAPED.test(spec)) {
          out.push(`${where} names the spec ${JSON.stringify(spec)}, which is not a status.md name`)
        } else {
          specNames.push(spec)
        }
      }
    }
    if (feature.hold !== undefined) {
      const hold = feature.hold
      out.push(...unknownKeys(hold, KEYS.hold, `${where}'s hold`))
      if (!HOLD_STATUSES.includes(hold?.status)) {
        out.push(
          `${where} carries the hold status ${JSON.stringify(hold?.status)}; the only hold status is ${JSON.stringify(HOLD_STATUSES[0])} (D2444)`,
        )
      }
      if (typeof hold?.reason !== 'string' || hold.reason.trim() === '') {
        out.push(`${where} carries a hold with no reason, and the reason is what tells the owner why a built feature is held back (D2444)`)
      }
    }
  }

  for (const [at, entry] of excluded.entries()) {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) {
      out.push(`${MAPPING}: the item at index ${at} of excluded is not an entry`)
      continue
    }
    const where = `${MAPPING}: the excluded entry ${JSON.stringify(entry.spec ?? `at index ${at}`)}`
    out.push(...unknownKeys(entry, KEYS.excluded, where))
    if (typeof entry.spec !== 'string' || !NAME_SHAPED.test(entry.spec)) {
      out.push(`${where} names the status.md name ${JSON.stringify(entry.spec)}, which is not a name status.md can carry`)
    } else {
      specNames.push(entry.spec)
    }
    if (typeof entry.reason !== 'string' || entry.reason.trim() === '') {
      out.push(`${where} carries no reason, and an excluded name with no reason is a name the matrix silently dropped`)
    }
  }

  const named = new Set()
  for (const name of specNames) {
    if (named.has(name)) {
      out.push(
        `${MAPPING} names the spec ${JSON.stringify(name)} twice; a spec is a feature's or is excluded, and the two lists never share one (F13 criterion 1)`,
      )
    }
    named.add(name)
  }

  const ids = features.map((feature) => feature?.id)
  for (const id of new Set(ids.filter((id, at) => ids.indexOf(id) !== at))) {
    out.push(`${MAPPING} writes the id ${JSON.stringify(id)} twice; an id never changes, so a second row with one cannot be told from the first (R7)`)
  }

  // The hyphenated names, read from the file itself: the single-word ones are ordinary words.
  const hyphenated = [...named].filter((name) => name.includes('-'))
  for (const feature of features) {
    if (feature === null || typeof feature !== 'object' || Array.isArray(feature)) continue
    for (const [what, text] of [
      ['id', feature.id],
      ['name', feature.name],
      ['explanation', feature.explanation],
    ]) {
      if (typeof text !== 'string') continue
      for (const name of hyphenated) {
        if (wholeToken(text, name)) {
          out.push(
            `${MAPPING}: the ${what} of the feature ${JSON.stringify(feature.id)} says ${JSON.stringify(name)}, which is a spec name; a spec name reaches neither the page nor FEATURES.md (R1, R8)`,
          )
        }
      }
    }
  }
  return out
}

const RAW = existsSync(path.join(REPO, MAPPING)) ? readFileSync(path.join(REPO, MAPPING), 'utf8') : ''
const FILE = RAW === '' ? {} : parseYAML(RAW, MAPPING)

/** The file with one edit, read back — the anchor check every plant needs, and only that. */
function planted(edit) {
  const copy = structuredClone(FILE)
  edit(copy)
  return problems(copy)
}

/** That the plant left exactly the problem a message names, and not silence. */
function refused(found, needle, why) {
  assert.ok(
    found.some((problem) => problem.includes(needle)),
    `${why}\nThe problems this file found were:\n${found.map((problem) => `  - ${problem}`).join('\n') || '  (none)'}`,
  )
}

/** The features and the excluded entries the file carries, as lists, whatever the file holds. */
const FEATURES = Array.isArray(FILE.features) ? FILE.features : []
const EXCLUDED = Array.isArray(FILE.excluded) ? FILE.excluded : []

/** Every status.md name the file claims: the features' specs and the excluded entries. */
function namedSpecs() {
  return [
    ...FEATURES.flatMap((feature) => (Array.isArray(feature?.specs) ? feature.specs : [])),
    ...EXCLUDED.map((entry) => entry?.spec),
  ].filter((name) => typeof name === 'string')
}

test('the mapping file is there, and every row of it passes every rule', () => {
  assert.ok(
    existsSync(path.join(REPO, MAPPING)),
    `${MAPPING} is missing: it is the one place a feature's name, grouping and plan are written (D2441), and the generator cannot build FEATURES.md or the page without it`,
  )
  const found = problems(FILE)
  assert.deepEqual(
    found,
    [],
    `${MAPPING} departs from its own rules:\n${found.map((problem) => `  - ${problem}`).join('\n')}`,
  )
  assert.ok(
    FEATURES.length >= 20,
    `${MAPPING} carries ${FEATURES.length} features, and the plan's estimate from status.md's own names is twenty to thirty rows (U1)`,
  )
  assert.ok(
    EXCLUDED.length >= 8,
    `${MAPPING} excludes ${EXCLUDED.length} names, and status.md carries several rows that are not specs (F6)`,
  )
  assert.ok(
    HOLD_STATUSES.every((status) => STATUSES.includes(status)),
    `a hold status outside the status set (${JSON.stringify(STATUSES)}) is a row the page has no mark for`,
  )
})

test('an id written twice is refused', () => {
  refused(
    planted((doc) => {
      doc.features[1].id = doc.features[0].id
    }),
    'twice',
    'Two features sharing an id must be refused: the id is in FEATURES.md\'s block and never changes (R7), so a reader cannot tell the two rows apart.',
  )
})

test('a spec named twice is refused', () => {
  refused(
    planted((doc) => {
      doc.features[1].specs.push(doc.features[0].specs[0])
    }),
    'twice',
    'A spec mapped to two features must be refused: a name in status.md is exactly one feature\'s spec, or an excluded entry (F13 criterion 1).',
  )
  refused(
    planted((doc) => {
      doc.excluded.push({ spec: doc.features[0].specs[0], reason: 'a second home for one name' })
    }),
    'twice',
    'A spec that is both a feature\'s and excluded must be refused: the two lists are the whole classification, and a name in both classifies nothing.',
  )
})

test('an explanation of more than two sentences is refused', () => {
  refused(
    planted((doc) => {
      doc.features[0].explanation = 'It runs a task. It opens a pull request. It reports what it did.'
    }),
    'sentences',
    'An explanation of three sentences must be refused: the page gives each feature a name and one or two sentences, and the third is a row its table cannot hold.',
  )
})

test('a hyphenated spec name in an id, a name or an explanation is refused', () => {
  const hyphenated = [...new Set(namedSpecs())].filter((name) => name.includes('-'))
  assert.ok(hyphenated.length > 0, `${MAPPING} names no hyphenated spec, so this case could not tell a leak from a clean row`)
  const name = hyphenated[0]
  refused(
    planted((doc) => {
      doc.features[0].explanation = `It does ${name} for you.`
    }),
    name,
    'A spec name in an explanation must be refused: neither the page nor FEATURES.md shows a spec name (R1, R8), and this is the only place that can be caught before the generator writes.',
  )
  refused(
    planted((doc) => {
      doc.features[0].name = `The ${name} feature`
    }),
    name,
    'A spec name in a feature\'s name must be refused for the same reason.',
  )
  refused(
    planted((doc) => {
      doc.features[0].id = name
    }),
    name,
    'A spec name as an id must be refused: a row\'s id is public, so it is a user-language slug and never a spec name (R8).',
  )
})

test('a plan and a hold status outside the closed sets are refused', () => {
  refused(
    planted((doc) => {
      doc.features[0].plan = 'free'
    }),
    'the plan "free"',
    'A plan outside the three must be refused: the page draws one column per plan, and a fourth value renders as a row with no column (D2440).',
  )
  const held = FEATURES.findIndex((feature) => feature?.hold !== undefined)
  assert.ok(held >= 0, `${MAPPING} holds no feature, so this case could not tell a refused hold status from a clean one (D2444)`)
  refused(
    planted((doc) => {
      doc.features[held].hold.status = 'planned'
    }),
    'the hold status "planned"',
    'A hold whose status is not Coming soon must be refused: a hold says a built feature is held back, which is Coming soon and nothing else (D2444).',
  )
})

test('a key the file does not carry is refused rather than ignored', () => {
  refused(
    planted((doc) => {
      doc.features[0].confidence = 'high'
    }),
    'carries the key "confidence"',
    'A feature key outside the six must be refused: a fact written under a name nothing reads is a fact the page silently drops.',
  )
  refused(
    planted((doc) => {
      doc.notes = 'a second page'
    }),
    'carries the key "notes"',
    'A root key outside the four must be refused, so a block nothing renders cannot be added in silence.',
  )
})

test('a feature with no spec, and an excluded name with no reason, are refused', () => {
  refused(
    planted((doc) => {
      doc.features[0].specs = []
    }),
    'names no spec',
    'A feature with no spec must be refused: its status is read from the specs it is built from, and a row with none has no status to read (D2443).',
  )
  refused(
    planted((doc) => {
      doc.excluded[0].reason = ''
    }),
    'carries no reason',
    'An excluded name with no reason must be refused: the exclusion is a decision, and a decision nobody wrote down is one nobody can review (F6).',
  )
})

test('the reader refuses a file it cannot read rather than guessing at it', () => {
  assert.throws(
    () => parseYAML('version: 1\nfeatures:\n  - id: "run-a-task\n', MAPPING),
    /does not close/,
    'A quoted scalar that does not close must be refused: the rest of the file would read as one value.',
  )
  assert.throws(
    () => parseYAML('version: 1\nfeatures:\n  - id: run-a-task: the runner\n', MAPPING),
    /carries ": "/,
    'A plain scalar carrying ": " must be refused: it is a mapping, and read as text it is a name nobody wrote.',
  )
  refused(
    problems(parseYAML('version: 1\nfeatures:\n', MAPPING)),
    'carries no feature',
    'A file whose features key carries nothing must be refused by this file\'s own gate, never read as an empty matrix.',
  )
  refused(
    problems(parseYAML('version: 1\nfeatures:\n  - id: run-a-task\n', MAPPING)),
    'carries no name',
    'A feature that is only an id must be refused: it renders as a row with no name and no explanation.',
  )
})
