// website/test/compatibility.mjs — the compatibility matrix's data file
// (docs/compatibility-matrix/implementation-plan.md, task CM1; the spec's rows as
// docs/compatibility-matrix/reconciliation.md F1–F32 amends them, with the owner's answers
// D1520–D1539 of 2026-11-07).
//
// `website/data/compatibility.yaml` is the one place a compatibility claim is written, and
// the page renders it verbatim (D1537). That makes this file the page's only honest gate
// until the page exists, and it holds four things:
//
//   1. **The closed sets.** A row's `status` is one of five and a row's `edition` is one of
//      two, and a file that grows a sixth status or a third edition fails here rather than
//      rendering something the page's legend has no mark for (D1520, D1522). The same case
//      reads the spec's own group list, in its order.
//   2. **Evidence.** Every `supported` and `built-not-enabled` row names a path that is in
//      this repository. Those are the two statuses that claim something exists today, so a
//      row of either with no path behind it — or with a path that has been moved or deleted —
//      is a claim the page cannot back (the plan's "Global constraints" table).
//   3. **The two wordings the owner answered.** The Claude OAuth row's note ends with D1539's
//      exact sentence and no other row names terms or permission, and no row's text carries a
//      phrase the reconciliation marked Wrong — the `never_say` list the file itself holds, so
//      the page-wide test reads that list rather than repeating it.
//   4. **The rows the spec claimed and the tree refuses.** No date and no price in a row's own
//      text (D1523: an unbuilt row promises no date), and no mobile Web row (F7/D1521: there is
//      no web build, so the row is a removal rather than a status).
//
// The reader below is this file's own and reads the data file **literally**, the way
// `test/mobile.mjs` reads `mobile-claims.yaml` and `test/caps-and-costs.mjs` reads
// `typical-costs.yaml`: the site's YAML reader lives in TypeScript in `src/content.ts`, which
// the pages build with and a test cannot import, and a gate that read the file through the
// very loader it is checking would not see a loader's own mistake. It refuses what it has not
// been taught rather than guessing at it, and every refusal names the file and the line.
//
// The "as of" stamp's own test — that `as_of.cli` and `as_of.console` name the newest release
// files — is CM3's, added to this file after this one landed; this file checks the stamp's
// shape and leaves the comparison to it.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO } from './lib/html.mjs'

/** The file every message below names. */
const DATA = 'website/data/compatibility.yaml'

/** The five statuses, and nothing else (D1520). */
const STATUSES = ['supported', 'coming-soon', 'built-not-enabled', 'roadmap', 'not-planned']

/** The two editions, and nothing else (D1522). */
const EDITIONS = ['core', 'subscription']

/** The groups whose spec table carried an Edition column, so whose rows carry an edition. */
const EDITION_GROUPS = ['local-environment', 'console', 'mobile', 'forges', 'identity-and-roles']

/** The spec's groups, in the spec's order (the plan's "Interfaces"). */
const GROUP_IDS = [
  'local-environment',
  'console',
  'mobile',
  'agent-harness',
  'model-access',
  'cloud',
  'backends',
  'forges',
  'trackers',
  'notifications',
  'identity-and-roles',
]

/** The row D1539 is about, by the name it carries in the file. */
const OAUTH_ROW = 'Claude OAuth token (bring your own credentials)'

/** D1539's sentence, exactly: the note must end with it, and no other row may name terms. */
const OAUTH_SENTENCE = "Use of a subscription is subject to the provider's own terms."

/** What a row's own text may not carry: a date (D1523) or a price (the plan's conventions). */
const DATE = /\b(?:19|20)\d{2}\b/
const PRICE = /\$|\bcents?\b/i

/** The keys each level of the file may carry; anything else is refused, not ignored. */
const KEYS = {
  root: ['version', 'as_of', 'never_say', 'groups'],
  group: ['id', 'title', 'note', 'rows'],
  row: ['name', 'status', 'edition', 'note', 'evidence'],
}

/** A file of this checkout, as text, by its path from the repository root. */
function repoFile(rel) {
  return readFileSync(path.join(REPO, rel), 'utf8')
}

/** Whether this checkout carries a path (a file or a directory), by its path from the root. */
function repoHas(rel) {
  return existsSync(path.join(REPO, rel))
}

/** A line's own comment, taken off outside quotes so a `#` in a note is text. */
function stripComment(raw) {
  let quote = null
  for (let at = 0; at < raw.length; at++) {
    const ch = raw[at]
    if (quote) {
      if (ch === quote) quote = null
      continue
    }
    if (ch === '"' || ch === "'") {
      quote = ch
      continue
    }
    if (ch === '#' && (at === 0 || /\s/.test(raw[at - 1]))) return raw.slice(0, at)
  }
  return raw
}

/**
 * The data file, read literally: a mapping of scalars and sequences, nested by indentation,
 * with quoted scalars. It understands exactly what the file uses and refuses the rest —
 * an unclosed quote, a `: ` inside a plain scalar, a duplicate key, a line indented further
 * than the key above it, a key with no value.
 */
function parse(source, file = DATA) {
  const lines = []
  for (const [at, raw] of source.split('\n').entries()) {
    const text = stripComment(raw).trimEnd()
    if (text.trim() === '') continue
    lines.push({ number: at + 1, indent: text.length - text.trimStart().length, text: text.trimStart() })
  }
  let i = 0
  const fail = (message, line) => {
    throw new Error(`${file}${line ? `:${line.number}` : ''}: ${message}`)
  }
  const scalar = (text, line) => {
    if (text.startsWith('"') || text.startsWith("'")) {
      if (text.length < 2 || !text.endsWith(text[0])) {
        fail(`the quoted scalar ${JSON.stringify(text)} does not close on this line`, line)
      }
      const inner = text.slice(1, -1)
      return text[0] === '"' ? inner.replace(/\\(["\\])/g, '$1') : inner.replaceAll("''", "'")
    }
    if (text === '') fail('an empty value', line)
    if (/:\s/.test(text)) {
      fail(`the value ${JSON.stringify(text)} carries ": ", which makes it a mapping rather than a value; quote it if it is meant as text`, line)
    }
    if (text === 'true') return true
    if (text === 'false') return false
    if (text === 'null' || text === '~') return null
    if (/^-?\d+$/.test(text)) return Number(text)
    return text
  }
  const value = (rest, indent, line) => {
    if (rest !== '') return scalar(rest, line)
    const next = lines[i]
    if (next && next.indent > indent) return block(next.indent)
    return fail(`"${line.text}" has no value`, line)
  }
  const map = (indent, seed) => {
    const out = {}
    const put = (key, rest, line) => {
      if (key in out) fail(`the key "${key}" is written twice`, line)
      out[key] = value(rest, indent, line)
    }
    if (seed) put(seed.key, seed.rest, seed.line)
    while (i < lines.length && lines[i].indent >= indent) {
      const line = lines[i]
      if (line.indent > indent) fail('this line is indented further than the key above it', line)
      if (line.text === '-' || line.text.startsWith('- ')) {
        fail('a sequence item where a "key: value" line was expected', line)
      }
      const m = /^([^:]+):(.*)$/.exec(line.text)
      if (!m) fail(`cannot read ${JSON.stringify(line.text)} as a "key: value" line`, line)
      i++
      put(m[1].trim(), m[2].trim(), line)
    }
    return out
  }
  const sequence = (indent) => {
    const out = []
    while (i < lines.length && lines[i].indent === indent) {
      const line = lines[i]
      if (line.text !== '-' && !line.text.startsWith('- ')) break
      const rest = line.text === '-' ? '' : line.text.slice(2).trim()
      i++
      const m = rest === '' ? null : /^([^:]+):(.*)$/.exec(rest)
      out.push(m ? map(indent + 2, { key: m[1].trim(), rest: m[2].trim(), line }) : value(rest, indent, line))
    }
    return out
  }
  const block = (indent) => {
    const line = lines[i]
    if (!line) fail('the document ends where a value was expected')
    return line.text === '-' || line.text.startsWith('- ') ? sequence(indent) : map(indent)
  }
  if (lines.length === 0) fail('the file carries no document')
  const doc = block(lines[0].indent)
  if (i < lines.length) fail('there is content after the document', lines[i])
  return doc
}

/** The rows of every group, in the file's order, each with the group it sits in. */
function rowsOf(doc) {
  return (doc.groups ?? []).flatMap((group) => (group.rows ?? []).map((row) => ({ group, row })))
}

/** What the row says on the page: its name and its note, which are the text a reader reads. */
function rowText({ row }) {
  return `${row.name ?? ''} ${row.note ?? ''}`
}

/** Every string a group or a row renders, with what it is, for the file-wide scans below. */
function renderedText(doc) {
  const out = []
  for (const group of doc.groups ?? []) {
    out.push({ what: `the group ${group.id}`, text: `${group.title ?? ''} ${group.note ?? ''}` })
    for (const row of group.rows ?? []) {
      out.push({ what: `the row ${row.name}`, text: `${row.name ?? ''} ${row.note ?? ''}` })
    }
  }
  return out
}

/** The keys of a mapping that are not the ones it may carry, named for a message. */
function unknownKeys(map, allowed, what) {
  return Object.keys(map ?? {}).filter((key) => !allowed.includes(key)).map((key) => `${what} carries the key "${key}"`)
}

/**
 * Every way the file departs from the rules above, as lines — the whole gate, so that a
 * plant below can be held to the one line it is about. `has` is the path check, which the
 * cases below replace with a fixture only when they mean to test it.
 */
export function problems(doc, has = repoHas) {
  const out = []
  if (doc.version !== 1) out.push(`${DATA} writes version ${JSON.stringify(doc.version)}; this file's format version is 1`)
  out.push(...unknownKeys(doc, KEYS.root, DATA))
  if (!Array.isArray(doc.never_say) || doc.never_say.length === 0) {
    out.push(`${DATA} carries no never_say list, so nothing holds the page to the phrases the reconciliation marked Wrong`)
  }
  for (const key of ['cli', 'console']) {
    const stamp = doc.as_of?.[key]
    if (typeof stamp !== 'string' || !/^v\d+\.\d+\.\d+$/.test(stamp)) {
      out.push(`${DATA} writes as_of.${key} as ${JSON.stringify(stamp)}; it is the version of the newest release file, as in v0.20.3`)
    }
  }
  const groups = doc.groups ?? []
  const ids = groups.map((group) => group.id)
  if (ids.join(',') !== GROUP_IDS.join(',')) {
    out.push(`${DATA} carries the groups ${JSON.stringify(ids)}; the spec's groups, in its order, are ${JSON.stringify(GROUP_IDS)}`)
  }
  for (const group of groups) {
    out.push(...unknownKeys(group, KEYS.group, `the group ${group.id}`))
    if (typeof group.title !== 'string' || group.title === '') out.push(`the group ${group.id} carries no title`)
    if (!Array.isArray(group.rows) || group.rows.length === 0) {
      out.push(`the group ${group.id} carries no row, and a group heading over nothing is a table the page renders empty`)
    }
    for (const row of group.rows ?? []) {
      const where = `${DATA}: the row ${JSON.stringify(row.name)} of the group ${group.id}`
      out.push(...unknownKeys(row, KEYS.row, where))
      if (typeof row.name !== 'string' || row.name === '') out.push(`${where} carries no name`)
      if (!STATUSES.includes(row.status)) {
        out.push(`${where} carries the status ${JSON.stringify(row.status)}; the five are ${JSON.stringify(STATUSES)} (D1520)`)
      }
      if (row.edition !== undefined && !EDITIONS.includes(row.edition)) {
        out.push(`${where} carries the edition ${JSON.stringify(row.edition)}; the two are ${JSON.stringify(EDITIONS)} (D1522)`)
      }
      if (EDITION_GROUPS.includes(group.id) && row.edition === undefined) {
        out.push(`${where} carries no edition, and the spec's ${group.id} table has an Edition column`)
      }
      if (!EDITION_GROUPS.includes(group.id) && row.edition !== undefined) {
        out.push(`${where} carries an edition, and the spec's ${group.id} table has no Edition column`)
      }
      if (typeof row.note !== 'string' || row.note === '') out.push(`${where} carries no note`)
      const needs = row.status === 'supported' || row.status === 'built-not-enabled'
      if (needs && (typeof row.evidence !== 'string' || row.evidence === '')) {
        out.push(`${where} is ${row.status} and names no evidence path: a row of either status claims something exists today, so it names the path in this repository that backs it`)
      }
      if (row.evidence !== undefined && !has(row.evidence)) {
        out.push(`${where} names the evidence path ${JSON.stringify(row.evidence)}, which is not in this repository`)
      }
    }
  }
  for (const { what, text } of renderedText(doc)) {
    for (const phrase of doc.never_say ?? []) {
      if (text.includes(phrase)) {
        out.push(`${what} says ${JSON.stringify(phrase)}, which the reconciliation marked Wrong and never_say refuses`)
      }
    }
    if (DATE.test(text)) out.push(`${what} carries a date, and an unbuilt row promises none (D1523)`)
    if (PRICE.test(text)) out.push(`${what} carries a price, and the page carries none`)
  }
  const rows = rowsOf(doc)
  if (rows.some(({ row }) => /^web$/i.test(row.name ?? ''))) {
    out.push(`${DATA} carries a Web row; there is no web build, so F7/D1521 removes it rather than giving it a status`)
  }
  const oauth = rows.find(({ row }) => row.name === OAUTH_ROW)
  if (!oauth) {
    out.push(`${DATA} carries no ${JSON.stringify(OAUTH_ROW)} row, so nothing carries D1539's sentence`)
  } else if (!String(oauth.row.note).endsWith(OAUTH_SENTENCE)) {
    out.push(`${DATA}: the ${JSON.stringify(OAUTH_ROW)} row's note does not end with ${JSON.stringify(OAUTH_SENTENCE)}, which is the owner's sentence exactly (D1539)`)
  }
  for (const { row } of rows) {
    if (row.name === OAUTH_ROW) continue
    if (/\bterms?\b/i.test(rowText({ row }))) {
      out.push(`${DATA}: the row ${JSON.stringify(row.name)} names terms, and the Claude OAuth row is the only row that may (D1539)`)
    }
    if (/\bpermission/i.test(rowText({ row }))) {
      out.push(`${DATA}: the row ${JSON.stringify(row.name)} names permission, and the Claude OAuth row is the only row that may (D1539)`)
    }
  }
  return out
}

const RAW = existsSync(path.join(REPO, DATA)) ? repoFile(DATA) : ''
const FILE = RAW === '' ? {} : parse(RAW)

/** The file with one edit, and the problems that edit leaves — the shape every plant uses. */
function planted(from, to) {
  assert.ok(
    RAW.includes(from),
    `this case's anchor ${JSON.stringify(from)} is not in ${DATA}, so the edit it makes proves nothing — fix the anchor, never the rule`,
  )
  return problems(parse(RAW.replace(from, to)))
}

/** That the plant left exactly the problem a message names, and not silence. */
function refused(problems_, needle, why) {
  assert.ok(
    problems_.some((problem) => problem.includes(needle)),
    `${why}\nThe problems this file found were:\n${problems_.map((problem) => `  - ${problem}`).join('\n') || '  (none)'}`,
  )
}

test('the data file is there, and every row of it passes every rule', () => {
  assert.ok(
    existsSync(path.join(REPO, DATA)),
    `${DATA} is missing: it is the compatibility page's only place for a claim (D1537), and the page cannot render without it`,
  )
  const found = problems(FILE)
  assert.deepEqual(
    found,
    [],
    `${DATA} departs from its own rules:\n${found.map((problem) => `  - ${problem}`).join('\n')}`,
  )
  assert.ok(rowsOf(FILE).length >= 40, `${DATA} carries ${rowsOf(FILE).length} rows, and the reconciliation's findings map to the spec's own rows`)
})

test('a status outside the five, and an edition outside the two, are refused', () => {
  refused(
    planted('- name: "macOS"\n        status: supported', '- name: "macOS"\n        status: probably'),
    'carries the status "probably"',
    'A row whose status is not one of the five must be refused: the page has a legend of five marks, and a sixth status renders as one of them or as nothing.',
  )
  refused(
    planted('- name: "macOS"\n        status: supported\n        edition: core', '- name: "macOS"\n        status: supported\n        edition: ce'),
    'carries the edition "ce"',
    'A row whose edition is not core or subscription must be refused: D1522 retired CE and EE, and the page says Core (MIT) and Subscription.',
  )
  refused(
    planted('      - name: "GitLab Cloud"\n        status: roadmap\n        edition: core', '      - name: "GitLab Cloud"\n        status: roadmap'),
    'carries no edition',
    'A row of a group whose spec table had an Edition column must carry one: the page renders that column for every row of the group.',
  )
})

test('a supported or built-not-enabled row with no evidence, or with a path that is gone, is refused', () => {
  refused(
    planted('        note: "amd64 and arm64."\n        evidence: ".goreleaser.yaml"', '        note: "amd64 and arm64."'),
    'names no evidence path',
    'A supported row with no evidence path must be refused: the plan holds the two present-tense statuses to a path in this repository.',
  )
  refused(
    planted('evidence: "internal/config/claude_accounts.go"', 'evidence: "internal/config/not-a-file.go"'),
    'which is not in this repository',
    'An evidence path that is not in the tree must be refused: a claim whose proof was moved or deleted is a claim the page cannot back.',
  )
})

test('a phrase the page may not be made of is refused, wherever it is written', () => {
  refused(
    planted('note: "OpenRouter or any Anthropic-compatible endpoint, for the coder and first-line reviewer."', 'note: "OpenRouter, for any model, for the coder."'),
    'says "any model"',
    'A note carrying a never_say phrase must be refused: those are the phrases the reconciliation marked Wrong (F12 here), and the list lives in the file so the page-wide test reads it rather than repeating it.',
  )
  refused(
    planted('    title: "Forges"', '    title: "Forges and Enterprise SSO"'),
    'says "Enterprise SSO"',
    'The scan covers a group title as well as a row: the page renders it, so a forbidden phrase there is the same claim (F29).',
  )
})

test("the Claude OAuth row carries D1539's sentence, and no other row names terms", () => {
  refused(
    planted(OAUTH_SENTENCE, 'Use of a subscription is your own business.'),
    'does not end with',
    "D1539's sentence must be the note's last words, exactly: it is the owner's own neutral line and counsel has not seen it.",
  )
  refused(
    planted('        note: "amd64 and arm64."', '        note: "amd64 and arm64, under the subscription terms."'),
    'names terms',
    'No row but the Claude OAuth one may name terms or permission: the page carries one legal sentence and not a second one nobody approved (D1539).',
  )
  refused(
    planted('        note: "amd64 and arm64."', '        note: "amd64 and arm64, with no special permission needed."'),
    'names permission',
    'A row that asks the reader for a permission is a second legal sentence, which D1539 refuses.',
  )
})

test('a date and a price in a row are refused', () => {
  refused(
    planted('        note: "The same Expo codebase as iOS."', '        note: "The same Expo codebase as iOS, planned for 2027."'),
    'carries a date',
    'A row must carry no date: an unbuilt row promises none (D1523), and a date on the page is a claim the repository cannot back.',
  )
  refused(
    planted('        note: "No plan for email."', '        note: "No plan for email; Slack costs $5 a month."'),
    'carries a price',
    'A row must carry no price: the page carries none, so no note may add one.',
  )
})

test('a Web row, and a group the spec does not have, are refused', () => {
  refused(
    planted('      - name: "Android"', '      - name: "Web"\n        status: coming-soon\n        edition: subscription\n        note: "From the same Expo codebase."\n      - name: "Android"'),
    'carries a Web row',
    'A Web row must be refused: there is no web build, so F7/D1521 removes the row rather than giving it a status.',
  )
  refused(
    planted('  - id: forges', '  - id: forgez'),
    'the spec\'s groups, in its order, are',
    "A group the spec does not have — or the spec's own groups in another order — must be refused: the page's headings are the spec's.",
  )
})

test('a key the file does not carry is refused rather than ignored', () => {
  refused(
    planted('        status: supported\n        edition: core', '        status: supported\n        confidence: high\n        edition: core'),
    'carries the key "confidence"',
    'A row key outside the five must be refused: a fact written under a name nothing reads is a fact the page silently drops.',
  )
})

test('the reader refuses a file it cannot read rather than guessing at it', () => {
  assert.throws(
    () => parse('version: 1\nas_of:\n'),
    /has no value/,
    'A key with no value must be refused: read as text it would render as an empty cell.',
  )
  assert.throws(
    () => parse('version: 1\nas_of:\n  cli: v0.20.3\n  cli: v0.20.2\n'),
    /written twice/,
    'A key written twice must be refused: the second value would win with nothing saying so.',
  )
  assert.throws(
    () => parse('version: 1\nas_of:\n  cli: "v0.20.3\n'),
    /does not close/,
    'A quoted scalar that does not close must be refused: the rest of the file would read as one value.',
  )
  assert.throws(
    () => parse('version: 1\nas_of:\n  cli: v0.20.3: the release\n'),
    /carries ": "/,
    'A plain scalar carrying ": " must be refused: it is a mapping, and read as text it renders as a sentence nobody wrote.',
  )
})
