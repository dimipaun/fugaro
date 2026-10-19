// The hand-scrub of a real `fugaro watch --json` document (owner decision
// 2026-10-19: "record from the real installation, scrub by hand").
//
// The three stall / kill / cap cases cannot be recorded without cloud writes and
// stay invented. THIS is for `normal`: the shape is the real CLI's (every key, its
// type, its order, which optional keys are present are all copied from the
// recording), and every VALUE that could identify the installation is replaced
// by an invented one. Nothing here is random: the same recording scrubs to the
// same bytes.
//
// Deny by default: a key this file does not know is a refusal, never a pass
// through, so a CLI that grows a field fails the scrub instead of leaking it.
//
// What is replaced, and by what:
//   project                     example-project  (then `<project>` via the redactor)
//   repo                        example-owner/example-repo, -2, -3 ... by first sight
//   slug                        github-com-example-owner-example-repo, -2 ...
//   run                         YYYYMMDD-HHMMSS-eeNN, the time being document.now minus the
//                               run's own age_seconds, so IDs agree with the clock
//   title                       "# Task: example task N" (the CLI's "# Task:" prefix is kept)
//   requested_by, kill.by       redacted@example.invalid
//   pr_url                      https://example.invalid/example-owner/example-repo/pull/N
//   workflow, recipe            example-workflow, example-recipe
//   every dollar amount         an invented fixed amount; totals are recomputed so the
//                               document's invariants hold (see `recompute`)
//   models                      claude-sonnet-4-5-20250929 (the real one was a `vendor/model` string,
//                               which the redactor reads as a repository)
// Kept as the CLI wrote them, ONLY when the value is in the fixed CLI vocabulary
// enumerated below (anything else is a refusal, never a pass-through): the enums
// and flags (mode, stage, round, health, deadline, auth, verify, status, outcome,
// connection.state), the numbers (ages, burn) and queued_note (a fixed sentence).
// The clock is SHIFTED by a constant offset (TIME_SHIFT_MS) so no real instant
// survives: now, day, kill.at and the run IDs all come from the shifted clock, and
// every age_seconds stays as it was, so they stay consistent.
//
// Nothing about a removed value is ever persisted, not even a hash: a bare hash of
// a low-entropy value (a small integer, an amount, a repo-style name) is recovered
// by dictionary. `removedValues` hands the removed values to the caller in memory
// only; the command line prints a COUNT.

import type { JSONDoc } from '../../src/source/source'

/** SCRUB_PROJECT is the invented project name; the redactor turns it into the marker. */
export const SCRUB_PROJECT = 'example-project'

/** INVENTED_SPEND is the invented per-run spend, in order, then repeating. */
export const INVENTED_SPEND: readonly number[] = Object.freeze([0.15, 0.4, 0.11, 0.03, 0.1, 0.07, 0.03, 0.04, 0.01])
/** INVENTED_CAP_USD is the invented daily cap, in place of the real one. */
export const INVENTED_CAP_USD = 50
/** TIME_SHIFT_MS moves the whole recorded clock; the real instant is recorded nowhere. */
export const TIME_SHIFT_MS = -((41 * 24 + 5) * 3600 + 17 * 60 + 33) * 1000

/** The fixed CLI vocabulary per field. A value outside it fails the scrub closed. */
export const VOCAB = {
  mode: ['enforce', 'observe', 'warn'],
  connectionState: ['live', 'stale', 'offline', 'refused', 'polling'],
  stage: ['queued', 'plan', 'provision', 'implement', 'fix', 'check', 'review', 'render', 'writeback'],
  auth: ['api-key', 'oauth', '-'],
  health: ['ok', 'silent', 'lost'],
  deadline: ['ok', 'near', 'over'],
  status: ['succeeded', 'failed', 'halted', 'cancelled', 'infra_error'],
  outcome: ['ready', 'draft', 'none', 'reviewed', 'artifacts'],
} as const
/** VERIFY_SHAPE is the CLI's verify wording ("-", or one `fugaro verify` line, truncated to 120 chars). */
export const VERIFY_SHAPE = /^(-|fugaro verify [a-z][a-z-]* #\d{1,4}: (passed|failed)( \(exit \d{1,3}, \d{1,6} tests?, \d{1,6} failures?(; warning: no JUnit reports found under \*\*\/junit\*\.xml, \*\*\/buil)?\)?)?)$/
const ROUND_SHAPE = /^(-|\d{1,2})$/
/** QUEUED_NOTE_SHAPE is the one queued_note sentence the CLI writes for a slow bucket. */
export const QUEUED_NOTE_SHAPE = /^queued runs unavailable: the runs bucket did not answer within \d{1,3}s$/
const KNOWN_TOP = [
  'project', 'mode', 'day', 'now', 'connection', 'total', 'repos', 'runs', 'finished', 'ready', 'queued_note',
]

type Obj = Record<string, unknown>

function isObj(value: unknown): value is Obj {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function refuse(path: string, what: string): never {
  throw new Error(`scrub: ${what} at ${path}`)
}

function round6(n: number): number {
  return Math.round(n * 1e6) / 1e6
}

type Rule = (v: unknown, path: string) => unknown

const num: Rule = (v, path) => (typeof v === 'number' && Number.isFinite(v) ? v : refuse(path, 'not a number'))
const bool: Rule = (v, path) => (typeof v === 'boolean' ? v : refuse(path, 'not a boolean'))
const oneOf = (allowed: readonly string[]): Rule => (v, path) =>
  typeof v === 'string' && allowed.includes(v) ? v : refuse(path, 'a value outside the CLI vocabulary')
const shaped = (re: RegExp): Rule => (v, path) =>
  typeof v === 'string' && re.test(v) ? v : refuse(path, 'a value outside the CLI vocabulary')
const invent = (value: unknown): Rule => (v, path) => (typeof v === 'string' ? value : refuse(path, 'not a string'))

/** applyRules copies a key only through its own rule; a key with no rule is refused. */
function applyRules(obj: Obj, path: string, rules: Readonly<Record<string, Rule>>): Obj {
  const out: Obj = {}
  for (const key of Object.keys(obj)) {
    const rule = rules[key]
    if (rule === undefined) refuse(`${path}.${key}`, 'an unknown key')
    out[key] = rule(obj[key], `${path}.${key}`)
  }
  return out
}

const BURN_RULES: Readonly<Record<string, Rule>> = { known: bool, usd_per_min: num, fast: bool }
const CONNECTION_RULES: Readonly<Record<string, Rule>> = {
  state: oneOf(VOCAB.connectionState),
  age_seconds: num,
  // `reason` is free text in the CLI: no sentence is known to be safe, so it is refused.
}

function shiftIso(iso: string, path: string): string {
  const t = Date.parse(iso)
  if (!Number.isFinite(t)) return refuse(path, 'not a timestamp')
  return new Date(t + TIME_SHIFT_MS).toISOString().slice(0, 19) + 'Z'
}

function runStamp(now: string, ageSeconds: number): string {
  const t = new Date(Date.parse(now) - Math.round(ageSeconds) * 1000)
  const iso = t.toISOString() // 2026-10-19T01:19:30.000Z
  return `${iso.slice(0, 10).replaceAll('-', '')}-${iso.slice(11, 19).replaceAll(':', '')}`
}

/** scrubDocument turns one real document into its invented twin. */
export function scrubDocument(raw: unknown): JSONDoc {
  if (!isObj(raw)) refuse('$', 'the document is not an object')
  for (const key of Object.keys(raw)) if (!KNOWN_TOP.includes(key)) refuse(`$.${key}`, 'an unknown key')

  const repoNames = new Map<string, number>()
  const repoIndex = (real: unknown, path: string): number => {
    if (typeof real !== 'string') return refuse(path, 'a repo that is not a string')
    let i = repoNames.get(real)
    if (i === undefined) {
      i = repoNames.size + 1
      repoNames.set(real, i)
    }
    return i
  }
  const inventedRepo = (i: number): string => (i === 1 ? 'example-owner/example-repo' : `example-owner/example-repo-${i}`)
  const inventedSlug = (i: number): string =>
    i === 1 ? 'github-com-example-owner-example-repo' : `github-com-example-owner-example-repo-${i}`

  const now = shiftIso(typeof raw['now'] === 'string' ? raw['now'] : refuse('$.now', 'no now'), '$.now')
  const out: Obj = {}
  for (const key of Object.keys(raw)) {
    const value = raw[key]
    const path = `$.${key}`
    switch (key) {
      case 'project':
        out[key] = SCRUB_PROJECT
        break
      case 'mode': out[key] = oneOf(VOCAB.mode)(value, path); break
      case 'now': out[key] = now; break
      case 'day':
        if (typeof value !== 'string') refuse(path, 'not a string')
        out[key] = now.slice(0, 10)
        break
      case 'connection':
        if (!isObj(value)) refuse(path, 'not an object')
        out[key] = applyRules(value, path, CONNECTION_RULES)
        break
      case 'queued_note': out[key] = shaped(QUEUED_NOTE_SHAPE)(value, path); break
      case 'total':
        out[key] = scrubTotalOrRepo(value, path, undefined)
        break
      case 'repos': {
        if (!Array.isArray(value)) refuse(path, 'repos is not an array')
        out[key] = value.map((repo, i) => {
          if (!isObj(repo)) return refuse(`${path}[${i}]`, 'a repo that is not an object')
          const n = repoIndex(repo['repo'], `${path}[${i}].repo`)
          return scrubTotalOrRepo(repo, `${path}[${i}]`, n)
        })
        break
      }
      case 'runs': {
        if (!Array.isArray(value)) refuse(path, 'runs is not an array')
        out[key] = value.map((run, i) => scrubRun(run, `${path}[${i}]`, i, now, repoIndex, inventedRepo, inventedSlug))
        break
      }
      case 'finished': case 'ready': {
        if (!Array.isArray(value)) refuse(path, `${key} is not an array`)
        out[key] = value.map((run, i) => scrubFinished(run, `${path}[${i}]`, i, now, repoIndex, inventedRepo, inventedSlug))
        break
      }
    }
  }
  return recompute(out)
}

function copyKeys(obj: Obj, path: string, allowed: readonly string[]): void {
  for (const key of Object.keys(obj)) if (!allowed.includes(key)) refuse(`${path}.${key}`, 'an unknown key')
}

function scrubKill(kill: unknown, path: string): unknown {
  if (kill === null) return null
  if (!isObj(kill)) return refuse(path, 'a kill that is neither null nor an object')
  copyKeys(kill, path, ['by', 'at', 'reason'])
  const out: Obj = {}
  for (const key of Object.keys(kill)) {
    if (key === 'by') out[key] = 'redacted@example.invalid'
    else if (key === 'at') out[key] = shiftIso(String(kill[key]), `${path}.at`)
    else out[key] = 'example reason'
  }
  return out
}

function scrubTotalOrRepo(value: unknown, path: string, repoN: number | undefined): Obj {
  if (!isObj(value)) return refuse(path, 'not an object')
  const inventedRepo = `example-owner/example-repo${repoN === 1 ? '' : `-${repoN}`}`
  return applyRules(value, path, {
    slug: invent(`github-com-example-owner-example-repo${repoN === 1 ? '' : `-${repoN}`}`),
    repo: invent(inventedRepo),
    counted_usd: num, spent_usd: num, notional_usd: num, percent: num, runs: num, run_hours: num, // recomputed below
    daily_cap_usd: (v, p) => (v === null ? null : typeof v === 'number' ? INVENTED_CAP_USD : refuse(p, 'not a number')),
    burn: (v, p) => (isObj(v) ? applyRules(v, p, BURN_RULES) : refuse(p, 'not an object')),
    kill: (v, p) => scrubKill(v, p),
  })
}

function scrubRun(
  run: unknown, path: string, i: number, now: string, repoIndex: (r: unknown, p: string) => number,
  inventedRepo: (n: number) => string, inventedSlug: (n: number) => string,
): Obj {
  if (!isObj(run)) return refuse(path, 'a run that is not an object')
  const n = repoIndex(run['repo'], `${path}.repo`)
  const age = run['age_seconds']
  return applyRules(run, path, {
    run: invent(`${runStamp(now, typeof age === 'number' ? age : 0)}-ee${String(i + 1).padStart(2, '0')}`),
    slug: invent(inventedSlug(n)),
    repo: invent(inventedRepo(n)),
    title: invent(`# Task: example task ${i + 1}`),
    stage: oneOf(VOCAB.stage),
    round: shaped(ROUND_SHAPE),
    verify: shaped(VERIFY_SHAPE),
    models: (v, p) => (v === '-' ? '-' : typeof v === 'string' ? 'claude-sonnet-4-5-20250929' : refuse(p, 'not a string')),
    recipe: invent('example-recipe'),
    auth: oneOf(VOCAB.auth),
    notional: bool,
    spent_usd: (v, p) => (v === null ? null : typeof v === 'number' ? INVENTED_SPEND[i % INVENTED_SPEND.length] : refuse(p, 'not a number')),
    age_seconds: num,
    health: oneOf(VOCAB.health),
    deadline: oneOf(VOCAB.deadline),
    halted: invent('example halt reason'),
    queued: bool,
    stuck: bool,
    workflow: invent('example-workflow'),
    requested_by: invent('redacted@example.invalid'),
  })
}

function scrubFinished(
  run: unknown, path: string, i: number, now: string, repoIndex: (r: unknown, p: string) => number,
  inventedRepo: (n: number) => string, inventedSlug: (n: number) => string,
): Obj {
  if (!isObj(run)) return refuse(path, 'a finished run that is not an object')
  const n = repoIndex(run['repo'], `${path}.repo`)
  const age = run['age_seconds']
  return applyRules(run, path, {
    run: invent(`${runStamp(now, typeof age === 'number' ? age : 0)}-ff${String(i + 1).padStart(2, '0')}`),
    slug: invent(inventedSlug(n)),
    repo: invent(inventedRepo(n)),
    title: invent(`# Task: example finished task ${i + 1}`),
    status: oneOf(VOCAB.status),
    outcome: oneOf(VOCAB.outcome),
    failed: bool,
    pr_url: invent(`https://example.invalid/example-owner/example-repo/pull/${100 + i}`),
    pr_number: () => 100 + i,
    age_seconds: num,
  })
}

/** recompute makes the invented amounts agree with each other the way the CLI's
 * do: a run's spend is a part of its repo's, a repo's of the total, `counted`
 * is spent plus notional, `percent` is counted over the cap, and `run_hours` and
 * `runs` come from the rows. The real recording's figures are not used. */
function recompute(doc: Obj): JSONDoc {
  const runs = (doc['runs'] as Obj[]) ?? []
  const repos = (doc['repos'] as Obj[]) ?? []
  const total = doc['total'] as Obj
  const perSlug = new Map<string, number>()
  let spentAll = 0
  let notionalAll = 0
  let hours = 0
  for (const run of runs) {
    const spent = typeof run['spent_usd'] === 'number' ? run['spent_usd'] : 0
    spentAll += run['notional'] === true ? 0 : spent
    notionalAll += run['notional'] === true ? spent : 0
    const slug = run['slug'] as string
    perSlug.set(slug, (perSlug.get(slug) ?? 0) + (run['notional'] === true ? 0 : spent))
    hours += typeof run['age_seconds'] === 'number' ? run['age_seconds'] / 3600 : 0
  }
  const fill = (block: Obj, spent: number, notional: number): void => {
    block['spent_usd'] = round6(spent)
    block['notional_usd'] = round6(notional)
    block['counted_usd'] = round6(spent)
    if (typeof block['daily_cap_usd'] === 'number') block['percent'] = round6((spent / block['daily_cap_usd']) * 100)
  }
  // A repo's spend is its runs' spend; the CLI counts only billed spend against the cap.
  const reposTotal = { spent: 0, notional: 0 }
  for (const repo of repos) {
    const spent = perSlug.get(repo['slug'] as string) ?? 0
    const notional = round6(
      runs.filter((r) => r['slug'] === repo['slug'] && r['notional'] === true).reduce((a, r) => a + (r['spent_usd'] as number), 0),
    )
    fill(repo, spent, notional)
    reposTotal.spent += spent
    reposTotal.notional += notional
  }
  fill(total, repos.length > 0 ? reposTotal.spent : spentAll, repos.length > 0 ? reposTotal.notional : notionalAll)
  total['runs'] = runs.length
  total['run_hours'] = hours
  return doc as JSONDoc
}

function leaves(value: unknown, into: Set<string>): void {
  if (typeof value === 'string') into.add(value)
  else if (typeof value === 'number') into.add(String(value))
  else if (Array.isArray(value)) for (const v of value) leaves(v, into)
  else if (isObj(value)) for (const v of Object.values(value)) leaves(v, into)
}

/** removedValues is every string and number leaf of the RAW document that does not
 * survive, as a leaf, into the scrubbed one: what the scan must never find. It is
 * returned in memory to the caller (a test, through an injected callback) and is
 * never written anywhere, and nothing derived from it (no hash) is either. */
export function removedValues(raw: unknown, scrubbed: unknown): string[] {
  const before = new Set<string>()
  const after = new Set<string>()
  leaves(raw, before)
  leaves(scrubbed, after)
  return [...before].filter((v) => !after.has(v) && v !== '')
}
