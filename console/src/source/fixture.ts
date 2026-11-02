// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Recorded, redacted fixtures: the file format, its loader, and the replaying
// source (desktop-console plan Task 1.4; design section 9.1).
//
// The plan's premise for this file is that **the recorder is agent-written and
// the recordings are owner-run**. An agent may not run `fugaro watch` against a
// real project, may not read `~/.config/fugaro` and may not touch a credential,
// so the eight recordings the design asks for do not exist yet and this module
// exists so that they can be replayed the day the owner makes them. Everything
// here is therefore written to be *told* nothing: a fixture is a file, and this
// file is the only reader of that file.
//
// Three parts, in order:
//
//   1. **The format.** A fixture is NDJSON: one header line and one line per
//      record. The header is the fixture's paperwork -- the format version, the
//      redactor that made it (`REDACTOR_VERSION`), whether it is synthetic, which
//      of the design's cases it is, the project *as redacted*, how many records
//      follow, and a SHA-256 of the body. The records are what the console
//      replays: an arrival stamp and either a document or a notice, which is
//      exactly the pair `decodeFeedLine` produces from a child's stdout.
//   2. **The loader.** `loadFixture` verifies before it returns anything: the
//      header parses, the format version is one this build knows, **the
//      redaction marker names a redactor this build knows**, the body's digest
//      matches the header's, the record count matches the header's, every record
//      is a well-formed envelope with a non-decreasing timestamp, and **every
//      document and every notice passes the redaction audit**
//      (`assertRedacted`). A fixture that fails any of those is refused by name.
//      That is the plan's "the loader must refuse a fixture whose redaction
//      marker/hash does not verify", plus the audit, because a digest alone
//      proves only that the file is the one the recorder wrote -- it says nothing
//      about whether the recorder was the one in this repository.
//   3. **The source.** `FixtureSource` implements the whole `FugaroSource`
//      interface over one fixture, so every screen and every later test can be
//      driven from a recording with no child process, no network and no
//      installation. The live feed **replays the recording's own clock**: a gap
//      of ninety seconds between two records is a gap of ninety seconds in the
//      stream, the documents go through the same `FeedTracker` a real child's
//      frames go through (`src/main/spawn.ts`), and the notices arrive on the
//      same stream, in the same place, as they did when the recording was made.
//      A fixture that replayed its records back-to-back would answer the wrong
//      question: the eight cases are *about* time.
//
// What this file deliberately does not do: it does not spawn anything, it does
// not read the filesystem (the recorder reads the file and hands the text in, so
// a test never needs a real path), it does not decide which fixture to use, and
// it does not decode a document -- a fixture's documents are the CLI's, undecoded,
// exactly like a live one's.

import { createHash } from 'node:crypto'

import { FeedTracker } from '../main/spawn'
import type { Notice, Timers } from '../main/spawn'
import type { OperationName, ValidatedArguments } from '../main/ipc'
import { stringifyDeep } from '../shared/stringify'
import { MAX_REDACTABLE_LINE_LENGTH, REDACTOR_VERSION, RedactionRefusal, assertRedacted } from './redact'
import { SourceRefusal, refusalFrom } from './source'
import type {
  FeedEnd,
  FugaroSource,
  InstallationRef,
  JSONDoc,
  LiveFeed,
  Snapshot,
  TextSnapshot,
  Unsubscribe,
} from './source'

/**
 * FIXTURE_FORMAT_VERSION is the version of the *envelope* -- the header's shape
 * and the records' shape. It changes when this file's reader changes what it
 * accepts, and it is checked before anything else: a fixture written by a later
 * format is refused rather than half-read.
 */
export const FIXTURE_FORMAT_VERSION = 1

/** FIXTURE_HEADER_KEY is the header line's one key. A file whose first line has
 * no such key is not a fixture, and saying so is better than reporting a JSON
 * error about a document. */
export const FIXTURE_HEADER_KEY = 'fugaro_fixture'

/** FIXTURE_DIRECTORY is where fixtures live, relative to the console. The
 * recorder writes only here (its `--out` is checked against this). */
export const FIXTURE_DIRECTORY = 'test/fixtures'

/** FIXTURE_HEADER_NOTE is the sentence every synthetic fixture carries, so that
 * a person who opens the file -- not the loader, a person -- is told what it is
 * without having to know the header's fields. */
export const SYNTHETIC_NOTE =
  'synthetic: invented documents put through the same redaction path as a recording; never recorded from any installation or cloud'

/** FixtureRefusal is thrown when a fixture does not verify. Its message names
 * what failed, because the alternative -- a replay that quietly shows a
 * different recording's numbers -- is the failure this whole file prevents. */
export class FixtureRefusal extends Error {
  override readonly name = 'FixtureRefusal'
}

/** FixtureCaseName is every `case` a committed fixture may carry, and there are
 * three kinds of them, in this order: the eight cases design section 9.1 asks
 * the owner to record (the names are the design's own, so a missing recording is
 * a missing *name* and not a shrug), `synthetic` (Task 2.0's invented documents
 * through the same redaction path), and `degraded` (Task 3.1's project with no
 * budget backend, a state no recording of the sandbox can be in). The last two
 * are fixtures this tree writes; `FIXTURE_CASES` is the eight, and only the
 * eight. */
export type FixtureCaseName =
  | 'normal'
  | 'silent-then-lost'
  | 'kill-mid-feed'
  | 'repo-at-cap'
  | 'oauth-notional'
  | 'absent-field'
  | 'gap'
  | 'hostile-title'
  | 'synthetic'
  | 'degraded'

/**
 * FIXTURE_CASES is the eight recordings design section 9.1 asks for, in its
 * order. **Every one of them is owner-run and none of them exists yet**: the
 * plan forbids an agent from running the recorder against a real installation,
 * so this list is what the owner's work will be checked against and what the
 * README says is missing. `synthetic` and `degraded` are deliberately not on it:
 * both are fixtures this tree *writes* from invented documents, and a list that
 * mixed them with the recordings would leave a reader working out which name was
 * real.
 */
export const FIXTURE_CASES: readonly FixtureCaseName[] = Object.freeze([
  'normal',
  'silent-then-lost',
  'kill-mid-feed',
  'repo-at-cap',
  'oauth-notional',
  'absent-field',
  'gap',
  'hostile-title',
])

/** FixtureRecord is one line of a recording: what arrived, and when the console
 * received it. A record is a document or a notice and never both -- the two
 * shapes `decodeFeedLine` produces, which is what makes a recording and a live
 * stream the same thing to everything downstream. */
export type FixtureRecord =
  | { readonly at: string; readonly document: JSONDoc }
  | { readonly at: string; readonly notice: string }

/** FixtureMeta is what a fixture says about itself besides its records. */
export interface FixtureMeta {
  /** `case` is which `FixtureCaseName` this is: a name in `FIXTURE_CASES` for a
   * recording, `synthetic` for Task 2.0's invented documents, `degraded` for
   * Task 3.1's degraded-mode frames. It is typed `string` rather than
   * `FixtureCaseName` because a header is read before it is trusted (the
   * loader's audit is what names a bad field). */
  readonly case: string
  /** `project` is the project **as redacted**: the marker `<project>`, for a
   * recording and for a synthetic fixture alike -- the loader audits the header
   * with the same rules as a document, so a `project` that is not the marker is
   * a refusal whether or not an installation ever existed. */
  readonly project: string
  /** `synthetic` is true for a fixture no installation produced. A loader does
   * not treat it differently -- the audit is the same -- but a reader, and the
   * README, must be able to tell at a glance. */
  readonly synthetic: boolean
  /** `note` is the one sentence a person reads. */
  readonly note: string
  /** `cli` is the version of `fugaro` the recording was made against, when the
   * recorder could read it. */
  readonly cli?: string | undefined
  /** `recorded` is when the recording was made, RFC 3339 UTC. Absent for a
   * synthetic fixture: it was not made anywhere. */
  readonly recorded?: string | undefined
}

/** FixtureHeader is the header line as it is written and as it is read. */
export interface FixtureHeader extends FixtureMeta {
  readonly version: number
  /** `redactor` is `REDACTOR_VERSION`: which rules made this file. */
  readonly redactor: string
  readonly records: number
  /** `sha256` is the digest of the body: every byte after the header's newline. */
  readonly sha256: string
}

/** LoadedFixture is a verified fixture. Nothing outside this module builds one:
 * the only way to hold one is to have passed `loadFixture`. */
export interface LoadedFixture {
  readonly header: FixtureHeader
  readonly records: readonly FixtureRecord[]
  /** `where` is what the caller calls this fixture in a message -- a path, for
   * the recorder, or a name, for a test. */
  readonly where: string
}

/** stampOf is the console's arrival stamp: RFC 3339 UTC, whole seconds, the
 * same shape `src/main/spawn.ts` and `src/source/cli.ts` write. */
export function stampOf(date: Date): string {
  return date.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

/** STAMP_RE is the stamp shape a record's `at` must have (section 10.13: the
 * console's own clock, never the child's). */
const STAMP_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/

/** ms parses a stamp. A stamp that does not parse is `NaN`, and every caller has
 * already refused one. */
function ms(at: string): number {
  return Date.parse(at)
}

/**
 * writeFixture serialises a fixture: a header line, then one line per record,
 * then a final newline. It is the only writer, and `loadFixture` is the only
 * reader, so the format has exactly one definition.
 *
 * The digest is taken over the body -- the bytes after the header's newline --
 * which is why the header cannot be part of its own digest and why the record
 * count is in the header too: a truncated file changes the body, and a body
 * edited *and* re-digested by hand still has to survive the redaction audit
 * `loadFixture` runs.
 */
export function writeFixture(records: readonly FixtureRecord[], meta: FixtureMeta): string {
  const body = records.map((record) => stringifyDeep(canonicalRecord(record))).join('\n')
  const header: FixtureHeader = {
    version: FIXTURE_FORMAT_VERSION,
    redactor: REDACTOR_VERSION,
    case: meta.case,
    project: meta.project,
    synthetic: meta.synthetic,
    ...(meta.cli === undefined ? {} : { cli: meta.cli }),
    ...(meta.recorded === undefined ? {} : { recorded: meta.recorded }),
    records: records.length,
    sha256: digest(body),
    note: meta.note,
  }
  const head = JSON.stringify({ [FIXTURE_HEADER_KEY]: header })
  return body === '' ? `${head}\n` : `${head}\n${body}\n`
}

/** canonicalRecord puts a record's keys in the writer's order, so that a fixture
 * written once and re-serialised byte for byte is the same file -- the property
 * `fixture.test.ts` holds the committed fixture to. It writes what it is given
 * and refuses nothing: a record holding both a document and a notice is a
 * malformed *line*, and `loadFixture` is what says so. */
function canonicalRecord(record: FixtureRecord): Record<string, unknown> {
  const out: Record<string, unknown> = { at: record.at }
  if ('document' in record) out['document'] = record.document
  if ('notice' in record) out['notice'] = record.notice
  return out
}

function digest(body: string): string {
  return createHash('sha256').update(body, 'utf8').digest('hex')
}

/**
 * loadFixture verifies a fixture and returns it, or throws `FixtureRefusal`
 * naming what failed. The order of the checks is the order of the claims: the
 * file is a fixture at all, this build knows the format, this build knows **the
 * redactor that made it**, the bytes are the ones the header describes, the
 * header itself is redaction-clean, the records are well-formed and in order,
 * and nothing in them is unredacted.
 */
export function loadFixture(text: string, where = 'the fixture'): LoadedFixture {
  const newline = text.indexOf('\n')
  if (newline < 0) throw new FixtureRefusal(`${where} has no header line and no records`)
  const headText = text.slice(0, newline)
  const body = text.slice(newline + 1)
  const header = parseHeader(headText, where)

  if (header.version !== FIXTURE_FORMAT_VERSION) {
    throw new FixtureRefusal(
      `${where} is format version ${String(header.version)}; this build reads version ${String(FIXTURE_FORMAT_VERSION)}`,
    )
  }
  if (header.redactor !== REDACTOR_VERSION) {
    throw new FixtureRefusal(
      `${where} was redacted by ${JSON.stringify(header.redactor)}; this build knows ${JSON.stringify(REDACTOR_VERSION)} and will not replay a fixture it cannot vouch for`,
    )
  }
  const actual = digest(body.endsWith('\n') ? body.slice(0, -1) : body)
  if (actual !== header.sha256) {
    throw new FixtureRefusal(
      `${where} does not verify: the body's sha256 is ${actual}, and its header says ${header.sha256}`,
    )
  }

  // **The header is the one part of a fixture the digest does not cover**, so
  // every check above can pass while it carries identity: an edit to
  // `"project"` — a value `FixtureSource` hands a screen as the installation's
  // name — leaves the body's sha256 untouched. It is audited with the same
  // rules as a document, minus the installation's own literals (the loader is
  // told nothing about an installation, and a synthetic fixture's invented
  // project name must stay allowed).
  auditOrRefuse(header, `${where}'s header`)

  const lines = body.endsWith('\n') ? body.slice(0, -1).split('\n') : body.split('\n')
  const records: FixtureRecord[] = []
  if (!(lines.length === 1 && lines[0] === '')) {
    for (const [index, line] of lines.entries()) {
      records.push(parseRecord(line, where, index))
    }
  }
  if (records.length !== header.records) {
    throw new FixtureRefusal(
      `${where} has ${String(records.length)} records; its header says ${String(header.records)}`,
    )
  }
  checkFixtureOrder(records, where)
  return { header, records, where }
}

/** parseHeader reads the first line: an object with exactly one key, the header
 * key, holding an object. */
function parseHeader(text: string, where: string): FixtureHeader {
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new FixtureRefusal(`${where}'s first line is not JSON: ${JSON.stringify(text.slice(0, 120))}`)
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new FixtureRefusal(`${where}'s first line is not a JSON object`)
  }
  const raw = (parsed as Record<string, unknown>)[FIXTURE_HEADER_KEY]
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new FixtureRefusal(`${where}'s first line has no ${FIXTURE_HEADER_KEY} object`)
  }
  const header = raw as Record<string, unknown>
  const wrong = (field: string, want: string): never => {
    throw new FixtureRefusal(`${where}'s header field ${field} is ${JSON.stringify(header[field])}, want ${want}`)
  }
  if (typeof header['version'] !== 'number') wrong('version', 'a number')
  if (header['redactor'] !== undefined && typeof header['redactor'] !== 'string') wrong('redactor', 'a string')
  if (typeof header['case'] !== 'string') wrong('case', 'a string')
  if (typeof header['project'] !== 'string') wrong('project', 'a string')
  if (typeof header['synthetic'] !== 'boolean') wrong('synthetic', 'a boolean')
  if (typeof header['note'] !== 'string') wrong('note', 'a string')
  if (typeof header['records'] !== 'number') wrong('records', 'a number')
  if (typeof header['sha256'] !== 'string') wrong('sha256', 'a string')
  // The redactor field's absence is its own refusal, and a more specific one
  // than the version check's: a header without it names no rules at all.
  if (header['redactor'] === undefined) {
    throw new FixtureRefusal(`${where}'s header names no redactor, so nothing vouches for its contents`)
  }
  return header as unknown as FixtureHeader
}

/** parseRecord reads one body line: a stamp, and a document or a notice. */
function parseRecord(line: string, where: string, index: number): FixtureRecord {
  const at = `${where} record ${String(index + 1)}`
  // A hard cap before the audit runs: the redactor's line bound, doubled for the
  // record's own wrapper and for markers longer than what they replaced. The
  // audit then holds every string to the redactor's string bound.
  if (line.length > 2 * MAX_REDACTABLE_LINE_LENGTH) {
    throw new FixtureRefusal(`${at} is longer than a feed line can be`)
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(line)
  } catch {
    throw new FixtureRefusal(`${at} is not JSON: ${JSON.stringify(line.slice(0, 120))}`)
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new FixtureRefusal(`${at} is not a JSON object`)
  }
  const record = parsed as Record<string, unknown>
  const stamp = record['at']
  if (typeof stamp !== 'string' || !STAMP_RE.test(stamp) || Number.isNaN(ms(stamp))) {
    throw new FixtureRefusal(`${at} has no RFC 3339 UTC arrival stamp, and a recording is its timestamps`)
  }
  const hasDocument = record['document'] !== undefined
  const hasNotice = record['notice'] !== undefined
  if (hasDocument === hasNotice) {
    throw new FixtureRefusal(`${at} must hold exactly one of "document" and "notice"`)
  }
  if (hasNotice) {
    const notice = record['notice']
    if (typeof notice !== 'string') throw new FixtureRefusal(`${at}'s notice is not a string`)
    auditOrRefuse(notice, at)
    return { at: stamp, notice }
  }
  const document = record['document']
  if (document === null || typeof document !== 'object' || Array.isArray(document)) {
    throw new FixtureRefusal(`${at}'s document is not a JSON object`)
  }
  auditOrRefuse(document, at)
  return { at: stamp, document: document as JSONDoc }
}

/**
 * auditOrRefuse runs the redaction audit over one record and refuses as a
 * `FixtureRefusal`. The audit's own refusal already names the class and the path
 * it found it at (`the frame at 12:00:01Z still holds an address`), and that
 * sentence is kept verbatim -- what changes is the class, so that **a fixture
 * which does not verify is one refusal to catch**, whether it failed its digest,
 * its count, its order or its audit.
 */
function auditOrRefuse(value: unknown, where: string): void {
  try {
    assertRedacted(value, where)
  } catch (error) {
    if (error instanceof RedactionRefusal) throw new FixtureRefusal(error.message)
    throw error
  }
}

/**
 * checkFixtureOrder refuses records whose timestamps go backwards. A recording
 * is a series, and the replay's whole value is that a gap is a gap; a file whose
 * records were reordered by hand would replay as a series that never existed.
 */
function checkFixtureOrder(records: readonly FixtureRecord[], where: string): void {
  for (let i = 1; i < records.length; i += 1) {
    const previous = records[i - 1]
    const current = records[i]
    if (previous === undefined || current === undefined) continue
    if (ms(current.at) < ms(previous.at)) {
      throw new FixtureRefusal(
        `${where} record ${String(i + 1)} is stamped ${current.at}, before record ${String(i)} at ${previous.at}`,
      )
    }
  }
}

// ---------------------------------------------------------------------------
// The replaying source.
// ---------------------------------------------------------------------------

/** MAX_REPLAY_FEEDS is how many replays one fixture source keeps at once. It is
 * `src/source/cli.ts`'s `MAX_LIVE_FEEDS`, for the same reason (Task 1.2's
 * surface is `invoke`-only, so a renderer walking through repositories would
 * otherwise leave a feed per name), and `fixture.test.ts` asserts the two are
 * equal so that a later change to one cannot silently change the other. */
export const MAX_REPLAY_FEEDS = 4

/** FixtureSourceOptions is everything a replaying source is built from. */
export interface FixtureSourceOptions {
  readonly fixture: LoadedFixture
  /** `documents` answers the one-shot reads (`budget.show`, `runs.list`, ...)
   * by operation name. A fixture that holds no document for a read refuses it by
   * name rather than answering with the live feed's last document, which would
   * be a screen showing a different command's answer. */
  readonly documents?: Partial<Record<OperationName, JSONDoc>> | undefined
  /** `texts` answers the two reads whose answer is a sentence (`cli.version`,
   * `run.logLink`). */
  readonly texts?: Partial<Record<OperationName, string>> | undefined
  /** `timers` is the clock the replay is scheduled on. It defaults to Node's
   * timers; a test passes a fake one and drives the recorded gaps itself. */
  readonly timers?: Timers | undefined
  /** `firstFrameBudgetMs` is how long `current()` waits for the first document,
   * as in `src/source/cli.ts`. */
  readonly firstFrameBudgetMs?: number | undefined
}

/** DEFAULT_FIRST_FRAME_BUDGET_MS is `FIRST_FRAME_BUDGET_MS`'s value, repeated
 * here rather than imported so that `fixture.ts` does not depend on `cli.ts`
 * (the console's own boundary test forbids a cycle between sources, and a replay
 * has no child whose speed the number would describe). */
export const DEFAULT_FIRST_FRAME_BUDGET_MS = 15_000

/**
 * FixtureSource is the whole `FugaroSource` interface over one recording: no
 * child, no network, no installation, and a live feed that replays the
 * recording's own clock. It is what a test drives a screen from, and what the
 * design's manual pass uses when the owner's recordings land.
 */
export class FixtureSource implements FugaroSource {
  readonly installation: InstallationRef

  readonly #fixture: LoadedFixture
  readonly #documents: Partial<Record<OperationName, JSONDoc>>
  readonly #texts: Partial<Record<OperationName, string>>
  readonly #timers: Timers
  readonly #budget: number
  readonly #feeds = new Map<string, ReplayFeed>()

  constructor(options: FixtureSourceOptions) {
    const header = options.fixture.header
    this.#fixture = options.fixture
    this.#documents = options.documents ?? {}
    this.#texts = options.texts ?? {}
    this.#timers = options.timers ?? NODE_TIMERS
    this.#budget = options.firstFrameBudgetMs ?? DEFAULT_FIRST_FRAME_BUDGET_MS
    // The header's own fields are the installation, and the binary path is a
    // placeholder: a fixture names a recording, not an installation, and a screen
    // showing `/Users/<someone>/bin/fugaro` read out of a fixture would be
    // exactly the leak this task exists to prevent.
    this.installation = {
      binaryPath: '(fixture)',
      version: header.cli ?? header.case,
      project: header.project,
      source: 'configured',
    }
  }

  /** subscribeLive answers a live feed, idempotent for the same arguments while
   * one is live, exactly as `FugaroSource.subscribeLive` states and as
   * `CliSource` implements it -- one feed per distinct argument set, the oldest
   * dropped past `MAX_LIVE_FEEDS`. A feed that has ended is not live, so the
   * next call replays the recording from its first record. */
  subscribeLive(args: ValidatedArguments): LiveFeed {
    const key = argsKey(args)
    const live = this.#feeds.get(key)
    if (live !== undefined) {
      this.#feeds.delete(key)
      this.#feeds.set(key, live)
      return live
    }
    while (this.#feeds.size >= MAX_REPLAY_FEEDS) {
      const oldest = this.#feeds.entries().next().value
      if (oldest === undefined) break
      const [oldestKey, oldestFeed] = oldest
      this.#feeds.delete(oldestKey)
      void oldestFeed.stop()
    }
    const feed = new ReplayFeed(this.#fixture.records, {
      timers: this.#timers,
      firstFrameBudgetMs: this.#budget,
    })
    this.#feeds.set(key, feed)
    feed.onEnded(() => {
      if (this.#feeds.get(key) === feed) this.#feeds.delete(key)
    })
    return feed
  }

  version(): Promise<TextSnapshot> {
    return this.#text('cli.version')
  }

  runLogLink(args: ValidatedArguments): Promise<TextSnapshot> {
    return this.#text('run.logLink', args)
  }

  showBudget(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('budget.show', args)
  }

  runReport(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('spend.report', args)
  }

  listRuns(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('runs.list', args)
  }

  statsRuns(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('runs.stats', args)
  }

  planStatus(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('plan.status', args)
  }

  coordinatorStatus(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('coordinator.status', args)
  }

  coordinatorShow(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('coordinator.show', args)
  }

  diagnoseRun(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('run.diagnose', args)
  }

  readRunLogs(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('run.logs', args)
  }

  listSecrets(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('secrets.inventory', args)
  }

  listAccounts(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('accounts.inventory', args)
  }

  environmentReport(args: ValidatedArguments): Promise<Snapshot> {
    return this.#document('environment.report', args)
  }

  async close(): Promise<void> {
    const feeds = [...this.#feeds.values()]
    this.#feeds.clear()
    await Promise.all(feeds.map((feed) => feed.stop()))
  }

  #document(operation: OperationName, args: ValidatedArguments): Promise<Snapshot> {
    const document = this.#documents[operation]
    if (document === undefined) {
      return Promise.reject(
        new SourceRefusal(
          `${this.#fixture.where} holds no ${operation} document (${argsNote(args)}); a fixture answers only the reads it recorded`,
        ),
      )
    }
    // The stamp is the fixture's last record: a one-shot read has no clock of
    // its own, and inventing "now" in a replay would make a screenshot differ
    // from the recording it claims to be.
    const at = this.#fixture.records.at(-1)?.at ?? ''
    return Promise.resolve({
      document,
      receivedAt: at,
      sequence: 1,
      day: typeof document['day'] === 'string' ? document['day'] : null,
      rolled: false,
      notices: [],
    })
  }

  #text(operation: OperationName, args: ValidatedArguments = {}): Promise<TextSnapshot> {
    const text = this.#texts[operation]
    if (text === undefined) {
      return Promise.reject(
        new SourceRefusal(
          `${this.#fixture.where} holds no ${operation} text (${argsNote(args)}); a fixture answers only the reads it recorded`,
        ),
      )
    }
    return Promise.resolve({ text, receivedAt: this.#fixture.records.at(-1)?.at ?? '' })
  }
}

/** argsKey is a subscription's identity: the arguments, sorted, so two calls
 * that ask for the same thing are the same feed however they were spelled. */
function argsKey(args: ValidatedArguments): string {
  return Object.keys(args)
    .sort()
    .map((key) => `${key}=${JSON.stringify(args[key] ?? null)}`)
    .join('\u0000')
}

function argsNote(args: ValidatedArguments): string {
  const key = argsKey(args)
  return key === '' ? 'no arguments' : key.replaceAll('\u0000', ', ')
}

const NODE_TIMERS: Timers = {
  setTimeout: (handler, delay) => setTimeout(handler, delay),
  clearTimeout: (handle) => {
    clearTimeout(handle)
  },
}

/** ReplayFeedDeps is what one replay is scheduled with. */
interface ReplayFeedDeps {
  readonly timers: Timers
  readonly firstFrameBudgetMs: number
}

/**
 * ReplayFeed is `LiveFeed` over a recording. It is the mirror of `LiveStream`
 * (`src/source/cli.ts`) with the child taken out: the same frames through the
 * same `FeedTracker`, notices accumulated onto the next snapshot, the same
 * `current()` semantics with the same budget, and an end that reports how many
 * documents were delivered.
 *
 * Two things are its own:
 *
 *   - **the schedule is the recording's.** Each record is delivered at the
 *     offset its stamp had from the first record's, so the design's gap case
 *     arrives as a gap, and the silent-then-lost case arrives with the same
 *     ninety seconds of nothing in it that the operator watched.
 *   - **the end is the recording's end.** The design's recordings are of a
 *     bounded `watch`, and where a real feed ends because a child stopped or
 *     refused, a recording ends because it ran out: the reason is `stopped` and
 *     the sentence is `null`, because the recording does not hold the sentence a
 *     child that is not running would have printed.
 */
class ReplayFeed implements LiveFeed {
  #latest: Snapshot | null = null
  #notices: Notice[] = []
  #snapshots = 0
  #ended: FeedEnd | null = null
  readonly #timers: Timers
  readonly #budget: number
  readonly #handles: ReturnType<typeof setTimeout>[] = []
  readonly #change = new Set<(snapshot: Snapshot) => void>()
  readonly #noticeListeners = new Set<(notice: Notice) => void>()
  readonly #endedListeners = new Set<(end: FeedEnd) => void>()
  readonly #waiting = new Set<{
    readonly resolve: (snapshot: Snapshot) => void
    readonly reject: (error: Error) => void
  }>()
  readonly #tracker = new FeedTracker()

  constructor(records: readonly FixtureRecord[], deps: ReplayFeedDeps) {
    this.#timers = deps.timers
    this.#budget = deps.firstFrameBudgetMs
    const first = records[0]
    if (first === undefined) {
      // A fixture with no records is refused by the loader; a feed that is built
      // anyway must not leave `current()` hanging, so it ends at once.
      this.#end()
      return
    }
    const start = ms(first.at)
    for (const [index, record] of records.entries()) {
      const last = index === records.length - 1
      const delay = Math.max(ms(record.at) - start, 0)
      this.#schedule(delay, () => {
        this.#deliver(record)
        if (last) this.#end()
      })
    }
  }

  get latest(): Snapshot | null {
    return this.#latest
  }

  onChange(listener: (snapshot: Snapshot) => void): Unsubscribe {
    this.#change.add(listener)
    return () => {
      this.#change.delete(listener)
    }
  }

  onNotice(listener: (notice: Notice) => void): Unsubscribe {
    this.#noticeListeners.add(listener)
    return () => {
      this.#noticeListeners.delete(listener)
    }
  }

  onEnded(listener: (end: FeedEnd) => void): Unsubscribe {
    this.#endedListeners.add(listener)
    if (this.#ended !== null) listener(this.#ended)
    return () => {
      this.#endedListeners.delete(listener)
    }
  }

  current(): Promise<Snapshot> {
    if (this.#latest !== null) return Promise.resolve(this.#latest)
    if (this.#ended !== null) {
      return Promise.reject(refusalFrom('watch', 'the recording ended before its first document'))
    }
    return new Promise<Snapshot>((resolve, reject) => {
      let timer: ReturnType<typeof setTimeout> | null = null
      const waiter = {
        resolve: (snapshot: Snapshot): void => {
          if (timer !== null) this.#timers.clearTimeout(timer)
          resolve(snapshot)
        },
        reject: (error: Error): void => {
          if (timer !== null) this.#timers.clearTimeout(timer)
          reject(error)
        },
      }
      timer = this.#timers.setTimeout(() => {
        this.#waiting.delete(waiter)
        reject(
          refusalFrom('watch', `no document arrived within ${String(Math.round(this.#budget / 1000))}s`),
        )
      }, this.#budget)
      this.#waiting.add(waiter)
    })
  }

  /** stop ends the replay where it is: the pending records are dropped, exactly
   * as a real feed's child is killed rather than read to its end. */
  stop(): Promise<void> {
    if (this.#ended === null) {
      this.#cancel()
      this.#end()
    }
    return Promise.resolve()
  }

  #schedule(delay: number, handler: () => void): void {
    this.#handles.push(this.#timers.setTimeout(handler, delay))
  }

  #cancel(): void {
    for (const handle of this.#handles) this.#timers.clearTimeout(handle)
    this.#handles.length = 0
  }

  #deliver(record: FixtureRecord): void {
    if (this.#ended !== null) return
    if ('notice' in record) {
      const notice: Notice = { text: record.notice, receivedAt: record.at }
      this.#notices = [...this.#notices, notice]
      for (const listener of [...this.#noticeListeners]) listener(notice)
      return
    }
    const frame = this.#tracker.push(record.document, record.at)
    this.#snapshots += 1
    const snapshot: Snapshot = {
      document: frame.document as JSONDoc,
      receivedAt: frame.receivedAt,
      sequence: frame.sequence,
      day: frame.day,
      rolled: frame.rolled,
      notices: this.#notices,
    }
    this.#notices = []
    this.#latest = snapshot
    for (const listener of [...this.#change]) listener(snapshot)
    const waiting = [...this.#waiting]
    this.#waiting.clear()
    for (const waiter of waiting) waiter.resolve(snapshot)
  }

  #end(): void {
    if (this.#ended !== null) return
    this.#cancel()
    const finished: FeedEnd = {
      // The recording ran out. It is not a refusal: nothing refused, and the
      // sentence a screen would show is the recording's own last document.
      reason: 'stopped',
      sentence: null,
      snapshots: this.#snapshots,
    }
    this.#ended = finished
    const waiting = [...this.#waiting]
    this.#waiting.clear()
    for (const waiter of waiting) {
      waiter.reject(refusalFrom('watch', 'the recording ended before its first document'))
    }
    for (const listener of [...this.#endedListeners]) listener(finished)
  }
}
