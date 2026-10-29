// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The redactor: what a recording may carry into this repository (desktop-console
// plan Task 1.4, SECURITY-SENSITIVE; design section 9.1).
//
// **Where this file lives, and why** (a deviation from the plan's file list,
// which names `console/tools/record-fixture/redact.ts`): the plan puts the
// recorder in `tools/`, and the recorder is the *writer* of a fixture -- but the
// rules are also the *reader*'s, because `loadFixture` audits every document it
// loads with exactly these classes, and a fixture is only as safe as the rules
// that made it. `src/source/fixture.ts` therefore has to import this module, and
// `tsconfig.build.json` has `rootDir: "src"`: a file under `tools/` cannot be in
// the app's build. So the redactor lives in `src/source/`, next to the loader
// that depends on it, and the recorder imports it from there. The plan's own
// split -- recorder in `tools/`, format and loader in `src/` -- is otherwise
// exactly as written.
//
// A recording is made on a real installation, against a real project, from a
// real checkout -- so every document in it can carry the operator's world: their
// project name, their machine, their home directory, their repository names,
// their colleagues' addresses, their branch names, and whatever a job wrote into
// a title or a log line. **None of that belongs in a commit.** This module is
// what stands between a recording and `console/test/fixtures/`, and its contract
// is deliberately narrow and deliberately deny-by-default:
//
//   1. **Every string is treated as hostile until a rule has passed over it.**
//      The rules are two kinds: *field classes*, where a key names what the
//      value is (`repo`, `slug`, `branch`, `pr_url`, `requested_by`, any
//      `*_by`, ...) and the whole value is replaced, and *pattern classes*,
//      which apply to every string in the document whatever its key is -- a
//      URL, a token (by shape, percent- or base64-wrapped, as a `name=value`
//      assignment, or as a long high-entropy run), an address in any script, a
//      branch, an absolute path, a repository path, a GCP project or bucket
//      named in a sentence, an IP address, a hostname, or one of the
//      installation's own literals (case-folded, look-alike-folded, and with a
//      host's stem). A **reserved key's whole subtree** is its class, and a
//      person-class value is pseudonymised wherever *else* the document spells
//      it (a queued run's title is its requester). The rules read the
//      *prepared* form of every string -- NFKC, then invisible characters
//      stripped -- so a fullwidth `＠` is an `@` and a zero-width space does not
//      split an address; a percent-encoded run is decoded first. Every rule is
//      linear in its input, and a string over `MAX_REDACTABLE_STRING_LENGTH`
//      (or a line over `MAX_REDACTABLE_LINE_LENGTH`) is refused before any rule
//      runs. A document with a key nobody anticipated still
//      cannot carry an address out of the room. **The keys are strings too**,
//      and a key is the one string that cannot be replaced: renaming it would
//      change a shape the console reads, so a key carrying identity is a
//      *refusal* instead (`auditKey`).
//   2. **Redaction is a marker, not a deletion, and the markers are a closed
//      set** (`<path>`, `<project>`, `<bucket>`, `<host>`, `<branch>`,
//      `[redacted-token]`, `[redacted-url]`, `redacted@example.invalid`,
//      `[redacted]`). A marked value is unambiguous on sight, and a value that
//      is *already* marked is left alone, which is what makes the whole
//      function idempotent: `redact(redact(x)) == redact(x)`. Three classes keep
//      a per-value pseudonym instead of a fixed marker -- a repository
//      (`r-1a2b3c4d/r-5e6f7a8b`), a slug (`slug-1a2b3c4d`) and a person
//      (`person-1a2b3c4d`) -- because a fixture with several repositories is
//      only worth having if the rows stay distinguishable. **A pseudonym is an
//      HMAC-SHA-256 under a random key drawn when the redactor is made, once
//      per recording, and never written anywhere**: an earlier version used a
//      public salt, and a 32-bit cut of SHA-256 over a guessable name is a
//      lookup table away from the name. Now the same repository is the same
//      pseudonym within one recording and a different one in the next, and the
//      loader never needs the key: it checks the pseudonym's grammar.
//   3. **It fails closed, twice.** A line that is not a JSON object is refused
//      rather than passed through, and every document is *audited* after
//      redaction: if any class is still present the call throws. A bug in a
//      rule is therefore a failed recording, not a leaked fixture. `loadFixture`
//      runs the same audit on load -- every class but the literals, which a
//      loader is not told -- so a fixture edited by hand after it was written is
//      refused too.
//   4. **It redacts identity, not hostility, and it does not invent data.** A
//      `<script>` in a title stays a `<script>`: the design wants a hostile-title
//      *recording*, and making text safe to *render* is the display layer's job
//      (Task 2.0's `src/shared/sanitize.ts`), not the recorder's. Booleans,
//      `null`, an **empty string** and a number that is neither under a
//      reserved key nor one of the literals are passed through untouched:
//      `pr_url: ""` is a run with no pull request, not a URL to redact. A string
//      in which no rule finds anything is returned byte for byte as it came;
//      one in which a rule finds something comes back prepared (NFKC, invisible
//      characters gone) and redacted. A `__proto__` key is refused, because a
//      copy would drop it without a word.
//
// What it is not: it is not a sanitiser (rule 4), it is not a schema (it walks
// whatever shape it is given, and knows no field beyond the key names its rules
// are about), and it is not a substitute for the owner's own review of a
// recording.

import { createHmac, randomBytes } from 'node:crypto'

import { decodeFeedLine } from '../main/spawn'
import {
  ABS_PATH_SOURCE,
  invisibleOrSeparatorRe,
  resolveReadings,
  stripTerminalSequences,
} from '../shared/sanitize'

/**
 * REDACTOR_VERSION is the marker written into every fixture's header and checked
 * on load. **The version is the contract**: a fixture whose header names a
 * redactor this build does not know is refused by `loadFixture` rather than
 * replayed, because a fixture's safety is a property of the rules that made it.
 * Version 2 is the review's: Unicode addresses, branch/account/person field
 * classes, hosts, IPs and GCP names, folded literals, more token families,
 * whole paths, and keyed pseudonyms.
 */
export const REDACTOR_VERSION = 'fugaro-console-redact/2'

/** PATH_MARKER replaces a filesystem path. */
export const PATH_MARKER = '<path>'
/** PROJECT_MARKER replaces the project or installation name, and a GCP project
 * ID or number named in a sentence. */
export const PROJECT_MARKER = '<project>'
/** BUCKET_MARKER replaces a storage bucket's name. */
export const BUCKET_MARKER = '<bucket>'
/** HOST_MARKER replaces a machine's name, a hostname, or an IP address. */
export const HOST_MARKER = '<host>'
/** BRANCH_MARKER replaces a git branch or ref. */
export const BRANCH_MARKER = '<branch>'
/** TOKEN_MARKER replaces anything credential-shaped, or the value of a key that
 * names one -- **including a value that does not look like a token**. */
export const TOKEN_MARKER = '[redacted-token]'
/** URL_MARKER replaces **every** URL, not only one with userinfo. */
export const URL_MARKER = '[redacted-url]'
/** ADDRESS_MARKER replaces an email address. Only this exact string is the
 * marker: `ana@example.invalid` still says `ana`. */
export const ADDRESS_MARKER = 'redacted@example.invalid'
/** LITERAL_MARKER replaces one of the installation's own literals, and the
 * whole value of an opaque identifier field (a Cloud Run execution). */
export const LITERAL_MARKER = '[redacted]'

/** REPO_PSEUDONYM_RE is the grammar of a redacted repository. */
export const REPO_PSEUDONYM_RE = /^r-[0-9a-f]{8}(?:\/r-[0-9a-f]{8})?$/
/** SLUG_PSEUDONYM_RE is the grammar of a redacted slug. */
export const SLUG_PSEUDONYM_RE = /^slug-[0-9a-f]{8}$/
/** PERSON_PSEUDONYM_RE is the grammar of a redacted person. */
export const PERSON_PSEUDONYM_RE = /^person-[0-9a-f]{8}$/

/** MARKERS is the closed set of fixed markers. */
export const MARKERS: readonly string[] = Object.freeze([
  PATH_MARKER,
  PROJECT_MARKER,
  BUCKET_MARKER,
  HOST_MARKER,
  BRANCH_MARKER,
  TOKEN_MARKER,
  URL_MARKER,
  ADDRESS_MARKER,
  LITERAL_MARKER,
])

/** RedactionRefusal is thrown when a line cannot be redacted, or when the audit
 * still finds something after redacting. */
export class RedactionRefusal extends Error {
  override readonly name = 'RedactionRefusal'
}

/**
 * MAX_REDACTABLE_STRING_LENGTH bounds one string (a value or a key) the redactor
 * and the audit will look at, and MAX_REDACTABLE_LINE_LENGTH one feed line. A
 * watch document is a few kilobytes and its longest string a title or a log
 * line; a string past the bound is **refused before any rule runs**, never
 * truncated. Every rule below is linear in its input, and the bound is what
 * keeps a single hostile line from holding the recorder's event loop -- and its
 * `--duration` timer -- for more than a moment.
 */
export const MAX_REDACTABLE_STRING_LENGTH = 64 * 1024
export const MAX_REDACTABLE_LINE_LENGTH = 1024 * 1024

/** tooLong is the refusal for a string or a line past its bound. It names the
 * bound, never the text. */
function tooLong(what: string, bound: number): string {
  return `${what} is longer than the redactor accepts (${String(bound)} characters); refused rather than truncated`
}

/**
 * INVISIBLE_RE is what `prepare` strips before any rule runs: the shared set of
 * invisible characters (`src/shared/sanitize.ts`: every format character,
 * default-ignorable code point, bidi control, private-use code point and C0/C1
 * control but tab, line feed and carriage return) plus the line and paragraph
 * separators, which the display layer removes as well. None of them is visible,
 * so none of them may split an address or a literal into pieces no rule
 * recognises. It is imported, not copied: a copy here once lacked
 * U+FFF9..U+FFFB, and a key split by one was written into a fixture verbatim
 * while the screen joined it back.
 */
const INVISIBLE_RE = invisibleOrSeparatorRe()

/** prepare is the form every rule reads: NFKC first (so a fullwidth `＠` *is*
 * an `@` before the address rule looks), then every terminal escape sequence
 * and control string gone, whole (`stripTerminalSequences`, the display layer's
 * one linear scanner, imported rather than copied so the three readers of CLI
 * text cannot drift), then the invisible characters gone. A sequence that
 * swallowed printable bytes is read every way by `redactFreeText` through the
 * display layer's `resolveAmbiguousSequences`. */
function prepare(text: string): string {
  return stripTerminalSequences(text.normalize('NFKC')).replace(INVISIBLE_RE, '')
}

/**
 * CONFUSABLES folds the Cyrillic and Greek letters that are drawn exactly like a
 * Latin one. It is used where a *name* is matched -- the installation's
 * literals, a person named in the same document, the recorder's `belong`
 * refusal -- and every entry maps one UTF-16 code unit to one, so a match in the
 * folded text is at the same offsets in the text it was folded from.
 */
const CONFUSABLES: Readonly<Record<string, string>> = Object.freeze({
  // Cyrillic, lower and upper case.
  а: 'a', е: 'e', о: 'o', р: 'p', с: 'c', у: 'y', х: 'x', і: 'i', ј: 'j', ѕ: 's', һ: 'h', ԁ: 'd', ԛ: 'q',
  ԝ: 'w', ӏ: 'l', к: 'k', А: 'A', В: 'B', Е: 'E', К: 'K', М: 'M', Н: 'H', О: 'O', Р: 'P', С: 'C', Т: 'T',
  Х: 'X', І: 'I', Ј: 'J', Ѕ: 'S', У: 'Y',
  // Greek, lower and upper case.
  α: 'a', ο: 'o', ρ: 'p', ν: 'v', ι: 'i', κ: 'k', υ: 'u', χ: 'x', Α: 'A', Β: 'B', Ε: 'E', Ζ: 'Z', Η: 'H',
  Ι: 'I', Κ: 'K', Μ: 'M', Ν: 'N', Ο: 'O', Ρ: 'P', Τ: 'T', Υ: 'Y', Χ: 'X',
  // Latin letters that are another letter's double.
  ı: 'i', ɡ: 'g', ɑ: 'a',
})
const CONFUSABLE_RE = new RegExp(`[${Object.keys(CONFUSABLES).join('')}]`, 'gu')

/** foldConfusables replaces each look-alike letter with the Latin letter it is
 * drawn as. The result has the same length as its input. */
export function foldConfusables(text: string): string {
  return text.replace(CONFUSABLE_RE, (ch) => CONFUSABLES[ch] ?? ch)
}

/** nameForm is how a name is compared: prepared, then folded. */
function nameForm(text: string): string {
  return foldConfusables(prepare(text))
}

/** replaceFolded replaces every match of `re` in the *folded* text at the same
 * offsets in `text` itself, so a literal spelled in Cyrillic look-alikes goes
 * while the rest of the string keeps its own letters. */
function replaceFolded(text: string, re: RegExp, replacement: string): string {
  const folded = foldConfusables(text)
  re.lastIndex = 0
  let out = ''
  let last = 0
  let found = false
  for (const match of folded.matchAll(re)) {
    found = true
    out += text.slice(last, match.index) + replacement
    last = match.index + match[0].length
  }
  return found ? out + text.slice(last) : text
}

/** PseudonymPrefix names the three pseudonym classes. */
export type PseudonymPrefix = 'r' | 'slug' | 'person'

/**
 * Redactor is the redactor for one recording: the functions the recorder and the
 * loader use. **It holds no data a caller can see**: the installation's literals
 * and the pseudonym key live in a closure, so `JSON.stringify` or `inspect` of a
 * redactor prints neither.
 */
export interface Redactor {
  /** redactDocument returns a redacted copy of a whole document. */
  redactDocument(document: unknown, where?: string): unknown
  /** redactText returns redacted free text (a notice line, a log message).
   * The person pass uses every person the documents this redactor redacted
   * so far named: a notice has no fields of its own to find a person by. */
  redactText(text: string): string
  /** redactFeedLine parses one line of a feed and returns the redacted line. It
   * throws on a line that is not a JSON object rather than passing it through. */
  redactFeedLine(line: string): string
  /** redactRepository is this recording's pseudonym for a repository. */
  redactRepository(value: string): string
  /** pseudonym is this recording's pseudonym for one value. */
  pseudonym(prefix: PseudonymPrefix, value: string): string
  /** problems reports every class still present in a value. */
  problems(value: unknown, where?: string): readonly string[]
}

/** RedactorOptions are the knobs a caller may set. `pseudonymKey` exists for
 * tests and for the synthetic fixture, whose bytes must be reproducible; a
 * recording never passes it, so its key is random and dies with the process. */
export interface RedactorOptions {
  readonly pseudonymKey?: string | Uint8Array
}

/**
 * createRedactor builds the redactor for one recording. `literals` are the
 * strings the operator's installation is known by -- the project name, the GCP
 * project ID and number, the bucket, the hostname, the user name, the home
 * directory, the checkout, the config path, the binary's path -- and each is
 * removed wherever it appears, in any case, including inside a title.
 */
export function createRedactor(literals: readonly string[] = [], options: RedactorOptions = {}): Redactor {
  const key = options.pseudonymKey === undefined ? randomBytes(32) : Buffer.from(options.pseudonymKey)
  const ctx: Ctx = {
    literals: literalPatterns(literals),
    persons: null,
    // The key is held by this closure only: it is not a property of the
    // redactor, so it is neither enumerable nor serialisable.
    pseudonym: (prefix, value) =>
      `${prefix}-${createHmac('sha256', key).update(`${prefix}\u0000${value}`).digest('hex').slice(0, 8)}`,
  }
  // The recording's people: every person any document redacted so far named,
  // with the pseudonym that document gave them. A notice is free text with no
  // fields, so this table is the only way the redactor can know a name in one
  // (review round 2 of PR 478: a notice used to see no people at all). A
  // document still collects its own people afresh -- this table only grows,
  // and only after a document has been redacted and audited.
  const seen: PersonTable = { words: new Map(), names: new Map(), maxWords: 0 }
  const remember = (document: unknown, where: string): void => {
    collectPersons(document, undefined, null, ctx, seen, { expanded: 0, distinct: 0, where }, 0)
  }
  const withSeen = (): Ctx => ({ ...ctx, persons: seen })
  return {
    redactDocument: (document, where) => {
      const redacted = redactDocument(document, ctx, where)
      remember(document, where ?? 'a document')
      return redacted
    },
    redactText: (text) => redactFreeText(text, withSeen()),
    redactFeedLine: (line) => {
      const out = redactFeedLine(line, ctx)
      const decoded = decodeFeedLine(line)
      if (decoded.kind === 'document') remember(decoded.document, 'a feed document')
      return out
    },
    redactRepository: (value) => redactRepository(value, ctx),
    pseudonym: (prefix, value) => ctx.pseudonym(prefix, value),
    problems: (value, where) => auditProblems(value, ctx.literals, seen, where),
  }
}

/** Ctx is what every rule below may reach for. `persons` are the person-class
 * values of the document being redacted (null outside one), each with the
 * pseudonym its own field got. */
interface Ctx {
  readonly literals: readonly LiteralRule[]
  readonly persons: PersonTable | null
  readonly pseudonym: (prefix: PseudonymPrefix, value: string) => string
}

// ---------------------------------------------------------------------------
// The field classes.
// ---------------------------------------------------------------------------

type FieldClass =
  | 'repository'
  | 'slug'
  | 'project'
  | 'bucket'
  | 'path'
  | 'host'
  | 'url'
  | 'token'
  | 'person'
  | 'branch'
  | 'opaque'
  | 'digest'
  | 'by'

/**
 * FIELD_CLASSES names the keys whose *whole value* is one class: a closed table
 * a reviewer can read. A key not on this table still goes through every pattern
 * class, and a key on it is *stricter*: its value must come out as that class's
 * marker, and the audit refuses the document if it does not. Keys are matched
 * lower-cased; `classFor` adds the suffix rules (`*_by`, `*_branch`, `*_url`,
 * `*_email`, `*_token`, `*_secret`, `*_password`).
 *
 * **Free text is not on this table** -- `title`, `message`, `halted`, `reason`,
 * `queued_note`, `outcome`, `detail` go through the pattern classes, which keep
 * the words and take the identity out. What *is* on it is every field whose
 * value is an identifier with no shape a pattern could find: a branch
 * (`acme-merger-codename`), an account (`zora-work`), a person (`Zora Kell`),
 * an execution name. Those are what `runs ls` rows carry in `watch --json`'s
 * degraded mode (`runview.Row`), and a pattern class can only miss them.
 */
const FIELD_CLASSES: Readonly<Record<string, FieldClass>> = Object.freeze({
  // A repository, in `owner/name` form.
  repo: 'repository',
  repository: 'repository',
  // The bucket-path slug.
  slug: 'slug',
  // The project / installation name, and the GCP project it reads.
  project: 'project',
  gcp_project: 'project',
  project_id: 'project',
  project_number: 'project',
  bucket: 'bucket',
  bucket_name: 'bucket',
  // Where things are.
  path: 'path',
  dir: 'path',
  cwd: 'path',
  checkout: 'path',
  workdir: 'path',
  binary: 'path',
  config: 'path',
  // The machine.
  host: 'host',
  hostname: 'host',
  machine: 'host',
  instance: 'host',
  ip: 'host',
  // A link.
  url: 'url',
  link: 'url',
  href: 'url',
  // Anything that names a credential.
  token: 'token',
  secret: 'token',
  password: 'token',
  passwd: 'token',
  private_key: 'token',
  private_key_id: 'token',
  authorization: 'token',
  bearer: 'token',
  api_key: 'token',
  apikey: 'token',
  credential: 'token',
  credentials: 'token',
  cookie: 'token',
  // A person or an account: the requester of a queued run, an author, an
  // account holder, the named Claude OAuth account a run used.
  requester: 'person',
  author: 'person',
  author_id: 'person',
  email: 'person',
  user: 'person',
  username: 'person',
  user_name: 'person',
  login: 'person',
  owner: 'person',
  actor: 'person',
  operator: 'person',
  committer: 'person',
  assignee: 'person',
  reviewer: 'person',
  account: 'person',
  account_id: 'person',
  oauth_account: 'person',
  // A git branch or ref, whatever it is called.
  branch: 'branch',
  ref: 'branch',
  head_ref: 'branch',
  base_ref: 'branch',
  // An identifier with no shape and no use in a replay: a Cloud Run execution.
  execution: 'opaque',
  // A digest or a commit: kept when it is hex, because it is not identity and
  // the high-entropy rule would otherwise take every one of them.
  sha: 'digest',
  sha256: 'digest',
  config_sha256: 'digest',
  head_sha: 'digest',
  pushed_head: 'digest',
  commit: 'digest',
})

/** REPORT_DIMENSIONS are the values `report --json`'s `by` may hold
 * (`internal/report`): there, `by` is the break-down dimension, not a person. */
const REPORT_DIMENSIONS: ReadonlySet<string> = new Set(['day', 'week', 'month', 'year', 'repo', 'model', 'person', 'mode'])

/** normaliseKey reads a key the way the table is written: camelCase and
 * PascalCase split into words (`githubToken` is `github_token`, `XApiKey` is
 * `x_api_key`), hyphens, dots and spaces as underscores, lower case. */
function normaliseKey(rawKey: string): string {
  return rawKey
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1_$2')
    .replace(/[-.\s]+/g, '_')
    .toLowerCase()
}

/** CREDENTIAL_KEY_RE is the suffix rule for a key that names a credential. It
 * ends at the word: `input_tokens` and `max_tokens` are counts, not tokens. */
const CREDENTIAL_KEY_RE =
  /(?:^|_)(?:token|secret|password|passwd|api_?key|private_key(?:_id)?|session_?id|session_token|credentials?|cookie|authorization|bearer)$/

/** classFor is the field class of one key in its object. */
function classFor(rawKey: string, parentKey: string | null): FieldClass | undefined {
  const key = normaliseKey(rawKey)
  if (key === 'by') return parentKey !== null && normaliseKey(parentKey) === 'kill' ? 'person' : 'by'
  // An own property only: `constructor` and `toString` are keys a document may
  // hold, and the table's prototype is not a class.
  if (Object.hasOwn(FIELD_CLASSES, key)) return FIELD_CLASSES[key]
  if (key.endsWith('_project_number') || key.endsWith('_project_id') || key.endsWith('_project')) return 'project'
  if (key.endsWith('_by') || key.endsWith('_email') || key.endsWith('_user')) return 'person'
  if (key.endsWith('_branch')) return 'branch'
  if (key.endsWith('_url')) return 'url'
  if (CREDENTIAL_KEY_RE.test(key)) return 'token'
  return undefined
}

/** subtreeClass is the class a reserved key passes to everything under it: an
 * object under `branch` is a branch all the way down, and so is an object under
 * `requested_by` a person. A digest key passes nothing (a commit object's
 * message is free text), and a `by` object is a person, not a dimension. */
function subtreeClass(cls: FieldClass | undefined): FieldClass | undefined {
  if (cls === undefined || cls === 'digest') return undefined
  return cls === 'by' ? 'person' : cls
}

// ---------------------------------------------------------------------------
// The pattern classes.
// ---------------------------------------------------------------------------

// **Every pattern below runs in time linear in its input.** A pattern that can
// fail after scanning a run must be anchored to the *start* of that run (a
// lookbehind that refuses to start inside it), or every position in a long run
// becomes a fresh scan of the rest of it: at 96b81997 three rules had no such
// anchor and 100 KB of `a` took half a minute. `redact-final.test.ts` holds each
// shape that was quadratic to a megabyte and to a 4x-input/4x-time ratio.

/** URL_RE matches every URL. It runs first, so the userinfo, the host and the
 * path inside a URL never get a chance to be what is replaced. The scheme starts
 * at the start of its run, never inside one. */
const URL_RE = /(?<![a-zA-Z0-9+.-])[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^\s"'`<>]+/g

/**
 * TOKEN_RES are the credential shapes worth naming: GitHub's token families (and
 * a short-prefixed piece of one, which is what half of a split token is),
 * OpenAI/Anthropic-style keys, Slack's, GitLab's, npm's, AWS's access-key id,
 * Google's API key, access token and refresh token, a JSON Web Token, a bearer
 * header and a PEM private key block. They are a *floor*: the value of a key
 * named `secret` is replaced whatever it looks like, an assignment
 * (`access_token=...`) loses its value, wrapped candidates are decoded first,
 * and a long high-entropy run is a token whatever its prefix.
 *
 * The first pattern is assembled from fragments rather than written as one
 * literal, and it is character-for-character the expression it was written as
 * before. A scanner reads a bare forge prefix the same way whether or not a
 * token follows it, so a file that spells one out is a file that gets flagged
 * forever -- and a report full of flagged non-secrets is a report nobody reads.
 * The rule here is about the *shape*, so the shape is built at load time.
 */
const TOKEN_RES: readonly RegExp[] = Object.freeze([
  new RegExp(['\\b(?:gh[pousr]_|', 'github', '_pat_|glpat-|npm_)[A-Za-z0-9_]{4,}'].join(''), 'g'),
  /(?<![A-Za-z0-9_-])sk-[A-Za-z0-9_-]{16,}\b/g,
  /(?<![A-Za-z0-9_-])xox[baprs]-[A-Za-z0-9-]{8,}\b/g,
  /\bAKIA[0-9A-Z]{16}\b/g,
  /(?<![A-Za-z0-9_-])AIza[0-9A-Za-z_-]{35}\b/g,
  /\bya29\.[0-9A-Za-z_-]{8,}/g,
  /(?<![\p{L}\p{N}])1\/\/0[0-9A-Za-z_-]{8,}/gu,
  /(?<![A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/g,
  // A JWT with a short, empty or missing signature: its header and payload are
  // the readable part, so two `eyJ` segments are a token whatever follows.
  /(?<![A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}(?:\.[A-Za-z0-9_-]*)?/g,
  /\bBearer\s+[A-Za-z0-9._~+/-]{12,}=*/gi,
  // The body may not run past the next BEGIN or END line, so a string of BEGIN
  // lines with no END is one short scan each, not a scan to the end each.
  /-----BEGIN [A-Z ]*PRIVATE KEY-----(?:(?!-----(?:BEGIN|END) )[\s\S])*-----END [A-Z ]*PRIVATE KEY-----/g,
])

/** ASSIGNMENT_RE matches a secret-named key and its value in free text --
 * `access_token=...`, `"private_key_id": "..."`, `password: ...` -- and the
 * replacement keeps the name and loses the value. The name starts at the start
 * of its run (`_` and `-` included), never inside one. */
const ASSIGNMENT_RE =
  /(?<![\p{L}\p{N}_-])([\p{L}\p{N}_-]*?(?:token|secret|password|passwd|pwd|api[_-]?key|apikey|private[_-]?key(?:[_-]id)?|credentials?|authorization|signature|cookie))(?![\p{L}\p{N}])(["']?\s*[:=]\s*["']?)([^\s"'&,;]+)/giu

/** PERCENT_RUN_RE is a run of the characters a percent-encoded value is made
 * of: one character class, scanned once. A run with an escape in it
 * (`PERCENT_ESCAPE_RE`) is decoded before any rule looks at it. */
const PERCENT_RUN_RE = /[A-Za-z0-9._~+=%-]+/g
const PERCENT_ESCAPE_RE = /%[0-9A-Fa-f]{2}/

/** percentDecode decodes every valid escape in a run, leniently (an invalid
 * escape stays as written), and again while escapes remain -- up to three
 * times, so `%2540` is an `@`. The run is ASCII, so each other character is
 * its own byte. */
function percentDecode(run: string): string {
  let text = run
  for (let round = 0; round < 3 && PERCENT_ESCAPE_RE.test(text); round += 1) {
    const bytes: number[] = []
    for (let i = 0; i < text.length; i += 1) {
      const hex = text.slice(i + 1, i + 3)
      if (text[i] === '%' && /^[0-9A-Fa-f]{2}$/.test(hex)) {
        bytes.push(Number.parseInt(hex, 16))
        i += 2
      } else {
        // A later round may hold what an earlier one decoded into UTF-8.
        const point = text.codePointAt(i) ?? 0
        const ch = String.fromCodePoint(point)
        for (const byte of Buffer.from(ch, 'utf8')) bytes.push(byte)
        i += ch.length - 1
      }
    }
    text = Buffer.from(bytes).toString('utf8')
  }
  return text
}
/** WRAPPED_BASE64_RE is a run that could be base64 (standard or URL-safe). */
const WRAPPED_BASE64_RE = /(?<![A-Za-z0-9+/_-])[A-Za-z0-9+/_-]{16,}={0,2}(?![A-Za-z0-9+/_=-])/g

/** ENTROPY_RE is a candidate for the high-entropy rule: a long run of the
 * characters tokens are made of. */
const ENTROPY_RE = /(?<![\p{L}\p{N}_+/=.-])[A-Za-z0-9_+/=-]{20,}(?![\p{L}\p{N}_+/=-])/gu

/** ADDRESS_RE matches an email address **in any script**: a Unicode local part
 * and a Unicode (IDN) domain. Only the exact marker is left alone. */
const ADDRESS_RE = /(?<![\p{L}\p{N}\p{M}._%+-])[\p{L}\p{N}\p{M}._%+-]+@[\p{L}\p{N}\p{M}-]+(?:\.[\p{L}\p{N}\p{M}-]+)+/gu

/** BRANCH_RE matches a git branch or ref by its conventional prefix, and any
 * `refs/heads/...`. A branch with no conventional prefix is found by its field
 * (`branch`, `base_branch`, ...), not by a pattern. */
const BRANCH_RE =
  /\b(?:refs\/(?:heads|tags|remotes)\/[^\s"'`<>,;]+|(?:feature|feat|fix|bugfix|hotfix|chore|release|dependabot|renovate|topic|wip)\/[A-Za-z0-9._/-]+)/g

/**
 * ABS_PATH_RE matches an absolute POSIX path: a run rooted at `/` (or `~/`) with
 * at least two segments. A segment is anything but a delimiter -- whitespace,
 * a quote, a bracket, `<>|;,:*?` -- so a non-ASCII segment (`/Users/zorá/...`)
 * is consumed whole. **A space is part of a segment when more path follows it**:
 * `/Users/Zora Kell/secret-plan/x` is one path, because `Kell` is followed by
 * `/`; a sentence after a path (`/etc/hosts for details`) is not, because no
 * slash follows its words. Two slashes is what keeps a sentence from being
 * eaten: `and/or` is not rooted. A single-segment path (`/opt`) in free text is
 * not redacted; the structured `path` key is.
 */
// The pattern itself is defined once, in `src/shared/sanitize.ts`
// (`ABS_PATH_SOURCE`), and built here from that same string: segments join
// across any whitespace, a line break included (review round 2 of PR 494 --
// this copy had stayed on `[ \t]` after the display layer's moved to `\s`, so
// `/home\nzora/.ssh` passed the recorder's audit).
const ABS_PATH_RE = new RegExp(ABS_PATH_SOURCE, 'gu')

/** WIN_PATH_RE matches a Windows path and a UNC path. */
const WIN_PATH_RE = /(?:\b[A-Za-z]:\\[^\s"'`,;:)\]}]*|\\\\[^\s"'`,;:)\]}]+)/g

/**
 * REPO_TOKEN_RE matches a repository path anywhere a string carries one -- `in
 * dimipaun/fugaro#412` in a title, `github.com/acme/secret-repo` in a log line.
 * Every segment must be at least four characters, so the prose pairs `and/or`,
 * `he/she`, `w/o`, `24/7` are left as written. **The false positive this keeps,
 * said out loud:** two ordinary words of four characters or more
 * (`input/output`) are pseudonymised as though they were a repository: that
 * costs a recorded title's wording, and the other mistake costs a private
 * repository's name in a committed file.
 */
const REPO_TOKEN_RE =
  /(?<![\w./~-])(?:(?:[A-Za-z0-9-]+\.)+[A-Za-z0-9-]+\/)?[A-Za-z0-9][A-Za-z0-9._-]{3,}\/[A-Za-z0-9][A-Za-z0-9._-]{3,}(?![\w/~-])/g

/** GCP_CONTEXT_RE matches a GCP project or bucket *named in a sentence*:
 * `on project acme-devel-zora`, `projects/123456789012`, `bucket fugaro-runs-x`.
 * A project ID has no shape of its own (6-30 lower-case letters, digits and
 * hyphens), so the word in front of it is the evidence; an ID with neither a
 * hyphen nor a digit (`project settings`) is prose and is left. */
const GCP_CONTEXT_RE = /\b(projects?|buckets?)(\s*[:=/]\s*|\s+)(["'`]?)([a-z0-9][a-z0-9._-]{3,61}[a-z0-9])/giu
/** RUNS_BUCKET_RE is the runs bucket by its own name (`fugaro-runs-<gcp>`). */
const RUNS_BUCKET_RE = /\bfugaro-runs-[a-z0-9][a-z0-9._-]*/giu

/** IPV4_RE and IPV6_RE match a network address, with a port when there is one. */
const IPV4_RE =
  /(?<![\p{N}.])(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(?:\.(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}(?::\d{1,5})?(?![\p{N}]|\.\d)/gu
const IPV6_RE =
  /(?<![\p{L}\p{N}:])(?:(?:[0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}|(?:[0-9A-Fa-f]{1,4}:){1,7}:(?:[0-9A-Fa-f]{1,4}(?::[0-9A-Fa-f]{1,4}){0,6})?|::(?:[0-9A-Fa-f]{1,4}(?::[0-9A-Fa-f]{1,4}){0,6}))(?![\p{L}\p{N}:])/gu

/**
 * HOST_RE matches a dotted name; `isHostname` decides whether it is a host. A
 * name is a host when its last label is a known top-level or private suffix
 * (`com`, `io`, `local`, `internal`, ...), or when it has three labels or more
 * and its last label is not a file extension. That leaves `redact.ts`,
 * `README.md`, `config.json`, `node.js` and `e.g.` alone, and takes
 * `acme-devel-zora-default-rtdb.firebaseio.com` and `zoras-laptop-pro.local`
 * whole.
 */
const HOST_RE =
  /(?<![\p{L}\p{N}\p{M}_.@-])(?:[\p{L}\p{N}](?:[\p{L}\p{N}\p{M}-]{0,61}[\p{L}\p{N}\p{M}])?\.)+\p{L}[\p{L}\p{M}]{1,62}(?![\p{L}\p{N}\p{M}_-]|\.[\p{L}\p{N}])/gu
const HOST_SUFFIXES: ReadonlySet<string> = new Set(
  (
    'com net org io dev app ai co cloud run page site online tech xyz info biz me us uk de fr eu ca au jp cn in nl ' +
    'se ch es it ru br pl be at dk no fi ie nz sg hk kr tw za mx ar edu gov mil int local localdomain internal ' +
    'lan home corp intranet localhost goog google gle'
  ).split(' '),
)
const FILE_EXTENSIONS: ReadonlySet<string> = new Set(
  (
    'ts tsx js jsx mjs cjs json ndjson md mdx txt log yaml yml toml ini cfg conf go py rb rs java kt c h cc cpp ' +
    'hpp cs swift sh bash zsh fish ps html htm css scss svg png jpg jpeg gif webp ico pdf csv tsv xml lock sum mod ' +
    'tf tfvars hcl sql db zip gz tgz tar bz xz dmg pkg deb rpm exe dll so dylib wasm map env pem key crt cer'
  ).split(' '),
)

function isHostname(match: string): boolean {
  const labels = match.toLowerCase().split('.')
  const last = labels[labels.length - 1] ?? ''
  if (HOST_SUFFIXES.has(last)) return true
  return labels.length >= 3 && !FILE_EXTENSIONS.has(last)
}

/** LiteralRule is one of the installation's own strings, as a pattern. */
interface LiteralRule {
  readonly value: string
  readonly re: RegExp
}

/**
 * literalPatterns turns the installation's literals into patterns that match
 * **in any case, in NFKC, on a boundary**, and adds each host-shaped literal's
 * stem: `Zoras-Laptop-Pro.local` is also `Zoras-Laptop-Pro`, because that is
 * how a shell prompt and half the CLI's messages spell it.
 *
 * A literal that begins with a letter or a digit is matched only where it is not
 * glued to another letter or digit (`ann` is left inside `announcement`). A
 * literal that begins with anything else -- `/Users/ana`, `~/src` -- is matched
 * wherever it stands, because **its own first character is the boundary**. The
 * trailing side is the mirror of the same rule.
 */
function literalPatterns(literals: readonly string[]): readonly LiteralRule[] {
  const values: string[] = []
  const add = (value: string): void => {
    const folded = nameForm(value)
    if (folded.trim() === '' || values.some((known) => known.toLowerCase() === folded.toLowerCase())) return
    values.push(folded)
  }
  for (const value of literals) {
    add(value)
    const folded = nameForm(value)
    // A host's stem: the first label of a dotted name with no path in it.
    if (!folded.includes('/') && !folded.includes('\\') && /^[\p{L}\p{N}][\p{L}\p{N}-]*\./u.test(folded)) {
      const stem = folded.slice(0, folded.indexOf('.'))
      if (stem.length >= 3) add(stem)
    }
  }
  return byLength(values.map(nameRule))
}

/** nameRule is one name (already in `nameForm`) as a pattern: **in any case, on
 * a boundary**. A name that begins with a letter or a digit is matched only
 * where it is not glued to another letter or digit; one that begins with
 * anything else is its own boundary. Callers sort longest first. */
function nameRule(value: string): LiteralRule {
  const escaped = value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const before = /^[\p{L}\p{N}]/u.test(value) ? '(?<![\\p{L}\\p{N}])' : ''
  const after = /[\p{L}\p{N}]$/u.test(value) ? '(?![\\p{L}\\p{N}])' : ''
  return { value, re: new RegExp(`${before}${escaped}${after}`, 'giu') }
}

/** byLength sorts names longest first, so a name that contains another is
 * replaced whole. */
function byLength<T extends { readonly value: string }>(rules: T[]): T[] {
  return rules.sort((a, b) => b.value.length - a.value.length)
}

/**
 * PersonTable is the person-class values of the document being redacted, **as
 * a hash table of word sequences**, not as one pattern per person: a document
 * may name thousands of people (one per queued run), and a pattern per person
 * over every string costs persons x text. A name is its words -- the runs of
 * letters, digits and marks of its prepared, confusable-folded form, lower-cased
 * -- so `Ana Smith`, `ana  smith`, `Ana-Smith`, `ana_smith` and `ANA\tSMITH` are
 * one name, and a name of two words or more is also entered last word first
 * (`Smith, Ana`). `findPersons` scans a string's words once and looks each run
 * of up to `maxWords` of them up: linear in the string, whatever the number of
 * people.
 */
interface PersonTable {
  /** words maps each word any person is spelled with to a small id. */
  readonly words: Map<string, number>
  /** names maps a name's word ids, joined by commas, to its replacement. */
  readonly names: Map<string, string>
  maxWords: number
}

/** MAX_DISTINCT_PERSONS bounds the persons one document may name. A watch
 * document names one requester per queued run -- a handful -- and a document
 * past the bound is refused, not redacted in part. */
export const MAX_DISTINCT_PERSONS = 4096
/** MAX_PERSON_WORDS bounds the words in one person's name. */
export const MAX_PERSON_WORDS = 16

/** WORD_RE is a word, as a person's name and the text it is looked for in are
 * split into words. */
const WORD_RE = /[\p{L}\p{N}\p{M}]+/gu

/** PersonMatch is one person found in a string, as offsets. */
interface PersonMatch {
  readonly start: number
  readonly end: number
  readonly replacement: string
}

/** findPersons finds every person in `text` (prepared), longest first at each
 * position, left to right, never overlapping. One scan of the words, and at
 * most `maxWords` lookups per word. Offsets are `text`'s own, because folding
 * maps one code unit to one. */
function findPersons(text: string, table: PersonTable | null, firstOnly = false): PersonMatch[] {
  const found: PersonMatch[] = []
  if (table === null || table.names.size === 0) return found
  const folded = foldConfusables(text)
  const starts: number[] = []
  const ends: number[] = []
  const ids: number[] = []
  WORD_RE.lastIndex = 0
  for (const match of folded.matchAll(WORD_RE)) {
    starts.push(match.index)
    ends.push(match.index + match[0].length)
    ids.push(table.words.get(match[0].toLowerCase()) ?? -1)
  }
  for (let i = 0; i < ids.length; ) {
    let best = -1
    let replacement = ''
    let key = ''
    for (let j = i; j < ids.length && j - i < table.maxWords && (ids[j] ?? -1) >= 0; j += 1) {
      key = j === i ? String(ids[j]) : `${key},${String(ids[j])}`
      const hit = table.names.get(key)
      if (hit !== undefined) {
        best = j
        replacement = hit
      }
    }
    if (best < 0) {
      i += 1
      continue
    }
    found.push({ start: starts[i] ?? 0, end: ends[best] ?? 0, replacement })
    if (firstOnly) return found
    i = best + 1
  }
  return found
}

/** replacePersons replaces every person `findPersons` finds. */
function replacePersons(text: string, table: PersonTable | null): string {
  const found = findPersons(text, table)
  if (found.length === 0) return text
  let out = ''
  let last = 0
  for (const match of found) {
    out += text.slice(last, match.start) + match.replacement
    last = match.end
  }
  return out + text.slice(last)
}

/** hits is `RegExp.test` for a shared, global pattern, re-anchored first. */
function hits(re: RegExp, text: string): boolean {
  re.lastIndex = 0
  return re.test(text)
}

// ---------------------------------------------------------------------------
// The rules, in order.
// ---------------------------------------------------------------------------

/** Rule is one pattern class: what it is called in a refusal, and how it
 * rewrites a string. A rule's `apply` leaves a string with nothing of its class
 * in it unchanged -- which is what the audit tests, rule by rule. */
interface Rule {
  readonly what: string
  apply(text: string, ctx: Ctx, nested: boolean): string
}

/** containsSecret answers whether decoded text is credential-shaped. */
function containsSecret(text: string): boolean {
  if (TOKEN_RES.some((re) => hits(re, text))) return true
  ASSIGNMENT_RE.lastIndex = 0
  for (const match of text.matchAll(ASSIGNMENT_RE)) {
    if (!isMarker(match[3] ?? '')) return true
  }
  return false
}

/** decodeBase64 returns printable text a base64 run decodes to, or null. */
function decodeBase64(run: string): string | null {
  const standard = run.replace(/-/g, '+').replace(/_/g, '/')
  const decoded = Buffer.from(standard, 'base64').toString('utf8')
  if (decoded.length < 8) return null
  const printable = decoded.replace(/[^\x20-\x7e]/g, '').length
  return printable / decoded.length >= 0.9 ? decoded : null
}

/** isHighEntropy is the split-token rule: a 20+ character run that mixes upper
 * case, lower case and digits with at least 3.5 bits of entropy per character,
 * or a 32+ character hex run with letters and digits (a key id, a digest
 * outside a digest field). Run ids, model names and slugs mix at most two of
 * the three, which is what keeps them. */
function isHighEntropy(run: string): boolean {
  const body = run.replace(/=+$/, '')
  if (body.length >= 32 && /^[0-9A-Fa-f]+$/.test(body) && /\d/.test(body) && /[A-Fa-f]/.test(body)) return true
  if (body.length < 20 || !/[A-Z]/.test(body) || !/[a-z]/.test(body) || !/\d/.test(body)) return false
  const counts = new Map<string, number>()
  for (const ch of body) counts.set(ch, (counts.get(ch) ?? 0) + 1)
  let entropy = 0
  for (const count of counts.values()) {
    const p = count / body.length
    entropy -= p * Math.log2(p)
  }
  return entropy >= 3.5
}

const RULES: readonly Rule[] = Object.freeze([
  { what: 'a URL', apply: (text) => text.replace(URL_RE, URL_MARKER) },
  {
    what: 'something credential-shaped',
    apply: (text, ctx, nested) => {
      // A percent-encoded run is decoded first. A credential in it takes the
      // whole run; anything else a rule finds in the decoded text -- an
      // address, a URL, a literal -- replaces the run with the decoded text,
      // redacted, so `u=ana%40acme.com` loses its local part too.
      let out = text.replace(PERCENT_RUN_RE, (run) => {
        if (!PERCENT_ESCAPE_RE.test(run)) return run
        const decoded = percentDecode(run)
        if (containsSecret(decoded) || isHighEntropy(decoded)) return TOKEN_MARKER
        if (nested) return run
        const redacted = applyRules(prepare(decoded), ctx, true)
        return redacted === prepare(decoded) ? run : redacted
      })
      out = out.replace(WRAPPED_BASE64_RE, (run) => {
        const decoded = decodeBase64(run)
        return decoded !== null && containsSecret(decoded) ? TOKEN_MARKER : run
      })
      for (const re of TOKEN_RES) out = out.replace(re, TOKEN_MARKER)
      out = out.replace(ASSIGNMENT_RE, (whole, name: string, sep: string, value: string) =>
        isMarker(value) ? whole : `${name}${sep}${TOKEN_MARKER}`,
      )
      return out
    },
  },
  {
    what: 'an address',
    apply: (text) => text.replace(ADDRESS_RE, (match) => (isAddressMarker(match) ? match : ADDRESS_MARKER)),
  },
  { what: 'a branch name', apply: (text) => text.replace(BRANCH_RE, BRANCH_MARKER) },
  {
    // A high-entropy run with two or more slashes that does not start at one
    // would be split by the path rule's unrooted alternative, leaving its first
    // fragment in the fixture and nothing for the net (or `problems()`) to see
    // (review round 2 of PR 494). It goes whole, before the path rule.
    what: 'something credential-shaped',
    apply: (text) =>
      text.replace(ENTROPY_RE, (run) =>
        !run.startsWith('/') && (run.match(/\//g) ?? []).length >= 2 && isHighEntropy(run) ? TOKEN_MARKER : run,
      ),
  },
  {
    what: 'a filesystem path',
    apply: (text) => text.replace(ABS_PATH_RE, PATH_MARKER).replace(WIN_PATH_RE, PATH_MARKER),
  },
  {
    what: 'a repository path',
    apply: (text, ctx) =>
      text.replace(REPO_TOKEN_RE, (match) => (REPO_PSEUDONYM_RE.test(match) ? match : redactRepository(match, ctx))),
  },
  {
    what: 'a GCP project or bucket',
    apply: (text) =>
      text
        .replace(GCP_CONTEXT_RE, (whole, word: string, sep: string, quote: string, id: string) => {
          if (!/[-\d]/.test(id)) return whole
          const marker = word.toLowerCase().startsWith('bucket') ? BUCKET_MARKER : PROJECT_MARKER
          return `${word}${sep}${quote}${marker}`
        })
        .replace(RUNS_BUCKET_RE, BUCKET_MARKER),
  },
  {
    what: 'a network address',
    apply: (text) => text.replace(IPV4_RE, HOST_MARKER).replace(IPV6_RE, HOST_MARKER),
  },
  { what: 'a hostname', apply: (text) => text.replace(HOST_RE, (match) => (isHostname(match) ? HOST_MARKER : match)) },
  {
    what: 'something credential-shaped',
    apply: (text) => text.replace(ENTROPY_RE, (run) => (isHighEntropy(run) ? TOKEN_MARKER : run)),
  },
])

// ---------------------------------------------------------------------------
// The redaction itself.
// ---------------------------------------------------------------------------

/** isMarker answers whether a value is already one of the closed marker set (or
 * a pseudonym), which is the idempotency test at the value level. */
export function isMarker(value: string): boolean {
  return (
    MARKERS.includes(value) ||
    REPO_PSEUDONYM_RE.test(value) ||
    SLUG_PSEUDONYM_RE.test(value) ||
    PERSON_PSEUDONYM_RE.test(value)
  )
}

/** redactRepository replaces a repository with this recording's pseudonym,
 * keeping the `owner/name` shape and keeping two repositories two. */
function redactRepository(value: string, ctx: Ctx): string {
  if (REPO_PSEUDONYM_RE.test(value)) return value
  const parts = value.split('/')
  const owner = parts[0] ?? ''
  const name = parts[1] ?? ''
  if (parts.length === 2 && owner !== '' && name !== '') {
    return `${ctx.pseudonym('r', owner)}/${ctx.pseudonym('r', name)}`
  }
  return ctx.pseudonym('r', value)
}

/** redactPerson replaces a person: anything holding an address becomes the
 * address marker, and anything else a pseudonym, so two requesters stay two. */
function redactPerson(value: string, ctx: Ctx): string {
  if (isMarker(value) || value === PLACEHOLDER) return value
  const form = prepare(value)
  if (value.includes('@') || form.includes('@')) return ADDRESS_MARKER
  return ctx.pseudonym('person', form)
}

/**
 * applyPass runs every pattern class, then the document's people, then the
 * installation's literals. **People and literals run last, after every pattern
 * rule**: a name is a word, and a word replaced first can switch a pattern off
 * -- a requester called `ya29` would turn every Google access token into a
 * pseudonym and hide the token, `local` would leave `zoras-laptop.` behind,
 * `42` would
 * split an IP address. Run last, a name can only replace what the patterns
 * left. People and literals are matched on the confusable-folded text.
 */
function applyPass(text: string, ctx: Ctx, nested: boolean, persons: boolean): string {
  let out = text
  for (const rule of RULES) out = rule.apply(out, ctx, nested)
  if (persons) out = replacePersons(out, ctx.persons)
  for (const rule of ctx.literals) out = replaceFolded(out, rule.re, LITERAL_MARKER)
  return out
}

/** MAX_PASSES bounds `applyRules`. A replacement can make a shape a rule finds
 * (a pseudonym before a `/`), so the passes repeat until nothing changes; two
 * suffice for every input known, and a string still changing after the bound is
 * refused by the document's fixed-point check. */
const MAX_PASSES = 3

/** applyRules runs `applyPass` to a fixed point, at most `MAX_PASSES` times. */
function applyRules(text: string, ctx: Ctx, nested = false, persons = true): string {
  let out = text
  for (let pass = 0; pass < MAX_PASSES; pass += 1) {
    const next = applyPass(out, ctx, nested, persons)
    if (next === out) return out
    out = next
  }
  return out
}

/**
 * redactFreeText runs every pattern class over one string, in the order that
 * keeps a rule from eating another rule's input: URLs (they contain everything
 * else), tokens, addresses, branches, paths, repository paths, GCP names,
 * network addresses, hosts, high-entropy runs, and last the document's people
 * and the installation's own literals. `persons` is false for a contract field
 * (`CONTRACT_KEYS`), whose value the console reads as an enumeration.
 *
 * **The rules read the prepared form** -- NFKC, invisible characters stripped --
 * so a fullwidth `＠` is an `@` and a zero-width space does not split an
 * address. A string in which no rule finds anything comes back byte for byte as
 * it came (a hostile title keeps its bidi override); one in which a rule finds
 * something comes back prepared and redacted. The form as written is tried as
 * well, for the rare match that only it shows. The one exception to "byte for
 * byte" is a string carrying a terminal escape sequence or control string: it
 * always comes back without them (review round 3 of PR 494), because a control
 * string can hide a token from every rule -- `ESC P` + a token, unterminated,
 * is no token to a rule and all of one to whoever reads the fixture -- and a
 * fixture is a file a person reads, not a terminal.
 *
 * **The readings are combined first, by the display layer's own step**
 * (`resolveReadings`, review round 2 of PR 540): the credential rules run over
 * the form as written (invisible characters in place) as well as the prepared
 * form, every time, and a credential either finds is masked. They used to be
 * read only when the prepared form matched nothing, so a path or an address
 * elsewhere in the value let a token split from its neighbour by a zero-width
 * character through whole.
 *
 * **A sequence that swallowed printable bytes is read every way** (review
 * round 4 of PR 494): `resolveAmbiguousSequences`, the display layer's rule,
 * masks every character of the terminal's reading that the credential rules
 * matched in any reading -- the bytes dropped, the final byte kept, the body
 * kept, per sequence -- and the rules then run over that masked form. A value
 * whose readings would cost more than the resolver's bounds is written as the
 * token marker whole: fail closed.
 *
 * The form as written is read with its sequences already gone, so a marker the
 * rules write into it is never re-read by the scanner: `ESC` + a marker would
 * otherwise lose its `[` and its first letter to a CSI (`edacted-token]`).
 */
function redactFreeText(text: string, ctx: Ctx, persons = true): string {
  if (text === '' || isMarker(text)) return text
  const ambiguity = resolveReadings(text)
  if (ambiguity === null) return TOKEN_MARKER
  if (ambiguity?.masked === true) return applyRules(ambiguity.text, ctx, false, persons)
  const prepared = prepare(text)
  const out = applyRules(prepared, ctx, false, persons)
  if (out !== prepared) return out
  if (prepared === text) return text
  const written = stripTerminalSequences(text)
  const fromText = applyRules(written, ctx, false, persons)
  if (written === text) return fromText
  return fromText === written ? prepared : prepare(fromText)
}

/** isAddressMarker answers whether an ADDRESS_RE match is the marker itself. */
function isAddressMarker(value: string): boolean {
  return value.toLowerCase() === ADDRESS_MARKER
}

/** PROTO_REFUSAL is why a `__proto__` key is refused: a plain object copy drops
 * it without a word, and a dropped key is a document the console never saw. */
const PROTO_REFUSAL = 'a "__proto__" key, which a copy of the document would silently drop'

/** entriesOf is `Object.entries`, refusing a `__proto__` key. */
function entriesOf(value: object): [string, unknown][] {
  const entries = Object.entries(value as Record<string, unknown>)
  if (entries.some(([key]) => key === '__proto__')) {
    throw new RedactionRefusal(`a document with ${PROTO_REFUSAL} is refused`)
  }
  return entries
}

/** checkLength refuses a string past the bound before any rule sees it. */
function checkLength(value: string, what: string): void {
  if (value.length > MAX_REDACTABLE_STRING_LENGTH) {
    throw new RedactionRefusal(tooLong(what, MAX_REDACTABLE_STRING_LENGTH))
  }
}

/** MAX_DEPTH bounds how deeply a document may nest. A watch document is four
 * levels deep; a hostile one ten thousand deep would otherwise end the walk in a
 * raw `RangeError` instead of a refusal. */
export const MAX_DEPTH = 256

/** tooDeep is the refusal for a document past `MAX_DEPTH`. */
function tooDeep(what: string): string {
  return `${what} nested deeper than ${String(MAX_DEPTH)} levels is refused`
}

/** checkDepth refuses a value past `MAX_DEPTH`. */
function checkDepth(depth: number): void {
  if (depth > MAX_DEPTH) throw new RedactionRefusal(tooDeep('a document'))
}

/**
 * MAX_EXPANSION bounds what NFKC may make of a string: a string whose
 * compatibility form is more than twice its length (plus a little, so one
 * ligature or ellipsis is fine) is refused, and so is a document whose strings
 * together grow by more than `MAX_DOCUMENT_EXPANSION`. One code point can be
 * eighteen in NFKC (U+FDFA), and every rule reads the prepared form: without
 * the bound a 1 MiB line is 18 MiB of rule input.
 */
const MAX_EXPANSION_SLACK = 32
const MAX_DOCUMENT_EXPANSION = 64 * 1024

/** expansionOf is how many characters NFKC adds to `value`, or a refusal. */
function expansionOf(value: string): number {
  const grown = value.normalize('NFKC').length
  if (grown > 2 * value.length + MAX_EXPANSION_SLACK) {
    throw new RedactionRefusal(expansionRefusal())
  }
  return Math.max(0, grown - value.length)
}

function expansionRefusal(): string {
  return 'a string whose compatibility form (NFKC) is more than twice its length is refused'
}

/** CONTRACT_KEYS are the fields whose values the console reads as an
 * enumeration or an identifier the CLI made (`stage`, `status`, a run id,
 * `auth`): the pattern classes still run over them, but **the person pass never
 * rewrites them**, so a requester called `queued` or `api` cannot turn a run's
 * stage or its auth into a pseudonym. */
const CONTRACT_KEYS: ReadonlySet<string> = new Set([
  'run', 'run_id', 'id', 'stage', 'status', 'auth', 'health', 'deadline', 'verify', 'outcome', 'mode', 'state',
])

/** CONTRACT_WORDS are the values those fields hold, and the words a run is
 * described with: a person value that is one of them is a placeholder or a
 * keyword, not a name, and is not looked for elsewhere. */
const CONTRACT_WORDS: ReadonlySet<string> = new Set([
  'run', 'runs', 'queued', 'running', 'coding', 'implement', 'review', 'verify', 'done', 'succeeded', 'failed',
  'halted', 'cancelled', 'canceled', 'infra_error', 'ready', 'none', 'ok', 'silent', 'lost', 'near', 'over',
  'live', 'stale', 'api', 'oauth', 'observe', 'enforce', 'pass', 'fail', 'tests',
])

/** isContractKey answers whether a key is one of `CONTRACT_KEYS`. */
function isContractKey(key: string | null): boolean {
  return key !== null && CONTRACT_KEYS.has(normaliseKey(key))
}

/** PLACEHOLDER is the CLI's "nothing here" (`-`): kept as written in a slug or
 * a person field, because it names nobody. */
const PLACEHOLDER = '-'

/** redactValue redacts one value. `cls` is the class its key (or a reserved
 * ancestor's) gives it; `key` is the key it sits under, which an array passes
 * to its items so that `kill[0].by` reads its parent as `kill`. */
function redactValue(value: unknown, cls: FieldClass | undefined, key: string | null, ctx: Ctx, depth = 0): unknown {
  checkDepth(depth)
  if (typeof value === 'string') return redactString(value, cls, ctx, isContractKey(key))
  if (typeof value === 'number') return redactNumber(value, cls, ctx)
  if (Array.isArray(value)) {
    const items = value.map((item) => redactValue(item, cls, key, ctx, depth + 1))
    if (cls !== undefined) return items
    const split = joinedCredentialElements(items)
    return split.size === 0 ? items : items.map((item, index) => (split.has(index) ? TOKEN_MARKER : item))
  }
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [childKey, child] of entriesOf(value)) {
      checkLength(childKey, 'a key')
      out[childKey] = redactValue(child, subtreeClass(cls) ?? classFor(childKey, key), childKey, ctx, depth + 1)
    }
    return out
  }
  return value
}

/** HEX_RE is what a digest field may hold and keep. */
const HEX_RE = /^[0-9a-f]{7,64}$/i

/** redactString applies the field class when there is one, and the pattern
 * classes otherwise. An empty string is never a class's value. */
function redactString(value: string, cls: FieldClass | undefined, ctx: Ctx, contract = false): string {
  checkLength(value, 'a string')
  if (value !== '' && cls !== undefined) {
    switch (cls) {
      case 'repository':
        return redactRepository(value, ctx)
      case 'slug':
        return SLUG_PSEUDONYM_RE.test(value) || value === PLACEHOLDER ? value : ctx.pseudonym('slug', value)
      case 'project':
        return PROJECT_MARKER
      case 'bucket':
        return BUCKET_MARKER
      case 'host':
        return HOST_MARKER
      case 'path':
        return PATH_MARKER
      case 'url':
        return URL_MARKER
      case 'token':
        return TOKEN_MARKER
      case 'person':
        return redactPerson(value, ctx)
      case 'branch':
        return BRANCH_MARKER
      case 'opaque':
        return LITERAL_MARKER
      case 'by':
        return REPORT_DIMENSIONS.has(value) ? value : redactPerson(value, ctx)
      case 'digest':
        if (HEX_RE.test(value)) return value
        break
    }
  }
  return redactFreeText(value, ctx, !contract)
}

/**
 * redactNumber redacts a number the two ways a number can carry identity: under
 * a reserved key (`project_number: 123456789012` is the project, and `token:
 * 12345` a credential), where it becomes that class's marker -- a string, since
 * the class's marker *is* the contract -- and as one of the installation's own
 * literals wherever it sits. Every other number is a count and is kept.
 */
function redactNumber(value: number, cls: FieldClass | undefined, ctx: Ctx): unknown {
  if (cls !== undefined && cls !== 'digest') return redactString(String(value), cls, ctx)
  return numberCarriesLiteral(value, ctx.literals) ? LITERAL_MARKER : value
}

function numberCarriesLiteral(value: number, literals: readonly LiteralRule[]): boolean {
  const text = String(value)
  return literals.some((rule) => hits(rule.re, text))
}

/**
 * joinedCredentialElements finds a credential split across adjacent string
 * elements of an array (`["gh", "p_", "abcd", "EFGH..."]`): each run of adjacent
 * strings is joined and the credential shapes and assignments are matched in
 * the join, and every element a match overlaps is returned. The high-entropy
 * rule is not asked: two ordinary words glued together are not a token.
 */
function joinedCredentialElements(items: readonly unknown[]): Set<number> {
  const marked = new Set<number>()
  let first = 0
  while (first < items.length) {
    if (typeof items[first] !== 'string') {
      first += 1
      continue
    }
    const starts: number[] = []
    let joined = ''
    let end = first
    for (let item = items[end]; typeof item === 'string'; item = items[end]) {
      starts.push(joined.length)
      joined += prepare(item)
      end += 1
    }
    if (end - first >= 2) {
      for (const [from, to] of credentialRanges(joined)) {
        // The element holding `from`, then every element up to `to`.
        let index = upperBound(starts, from) - 1
        for (index = Math.max(index, 0); index < starts.length && (starts[index] ?? Infinity) < to; index += 1) {
          if (items[first + index] !== '') marked.add(first + index)
        }
      }
    }
    first = end
  }
  return marked
}

/** upperBound is the number of `sorted` entries at or below `value`. */
function upperBound(sorted: readonly number[], value: number): number {
  let low = 0
  let high = sorted.length
  while (low < high) {
    const middle = (low + high) >> 1
    if ((sorted[middle] ?? 0) <= value) low = middle + 1
    else high = middle
  }
  return low
}

/** credentialRanges is every credential shape and every unredacted assignment in
 * a string, as offsets. */
function credentialRanges(text: string): [number, number][] {
  const ranges: [number, number][] = []
  for (const re of TOKEN_RES) {
    re.lastIndex = 0
    for (const match of text.matchAll(re)) ranges.push([match.index, match.index + match[0].length])
  }
  ASSIGNMENT_RE.lastIndex = 0
  for (const match of text.matchAll(ASSIGNMENT_RE)) {
    if (!isMarker(match[3] ?? '')) ranges.push([match.index, match.index + match[0].length])
  }
  return ranges
}

/** MARKER_WORDS are the words the markers and pseudonyms are spelled with: a
 * person whose every word is one of them (`person`, `redacted`, `redacted-token`)
 * would otherwise match the markers the redaction itself wrote. */
const MARKER_WORDS: ReadonlySet<string> = new Set([
  'person', 'slug', 'redacted', 'example', 'invalid', 'path', 'project', 'bucket', 'host', 'branch', 'token', 'url',
])

/** CollectState is what one walk of `collectPersons` counts. */
interface CollectState {
  expanded: number
  distinct: number
  readonly where: string
}

/**
 * collectPersons gathers every person-class value in a document -- a requester,
 * an author, a `kill.by`, anything under `requested_by` -- before anything is
 * redacted. **A person is identity wherever else the document spells them**:
 * the CLI titles a queued run with no workflow by its requester
 * (`internal/watch/queued.go`), and `requested_by` is the config's free-form
 * `user:`, so a pseudonym in one field and the name in the next is a leak.
 * A value with fewer than two letters or digits (`-`, the CLI's placeholder) is
 * not a name and is not collected, and nor is a contract keyword
 * (`CONTRACT_WORDS`). The same walk holds the document to `MAX_DEPTH` and to
 * its NFKC budget, so a hostile document is refused before any rule runs.
 */
function collectPersons(
  value: unknown,
  cls: FieldClass | undefined,
  key: string | null,
  ctx: Ctx,
  table: PersonTable,
  state: CollectState,
  depth: number,
): void {
  checkDepth(depth)
  if (typeof value === 'string') {
    if (value.length > MAX_REDACTABLE_STRING_LENGTH) return
    budget(value, state)
    if (cls === 'person' || (cls === 'by' && !REPORT_DIMENSIONS.has(value))) addPerson(value, ctx, table, state)
    return
  }
  if (Array.isArray(value)) {
    for (const item of value) collectPersons(item, cls, key, ctx, table, state, depth + 1)
    return
  }
  if (value !== null && typeof value === 'object') {
    for (const [childKey, child] of Object.entries(value as Record<string, unknown>)) {
      if (childKey.length <= MAX_REDACTABLE_STRING_LENGTH) budget(childKey, state)
      collectPersons(child, subtreeClass(cls) ?? classFor(childKey, key), childKey, ctx, table, state, depth + 1)
    }
  }
}

/** budget charges one string's NFKC growth to the document. */
function budget(value: string, state: CollectState): void {
  state.expanded += expansionOf(value)
  if (state.expanded > MAX_DOCUMENT_EXPANSION) throw new RedactionRefusal(`${state.where}: ${expansionRefusal()} (summed over the document)`)
}

/** addPerson enters one person in the table: their words, and their words last
 * word first. */
function addPerson(value: string, ctx: Ctx, table: PersonTable, state: CollectState): void {
  if (value === '' || value === PLACEHOLDER || isMarker(value)) return
  const form = nameForm(value)
  const lowered = form.toLowerCase()
  if ((form.match(/[\p{L}\p{N}]/gu) ?? []).length < 2 || CONTRACT_WORDS.has(lowered)) return
  const words = lowered.match(WORD_RE) ?? []
  if (words.length === 0 || words.every((word) => MARKER_WORDS.has(word))) return
  if (words.length > MAX_PERSON_WORDS) {
    throw new RedactionRefusal(
      `${state.where} names a person in more than ${String(MAX_PERSON_WORDS)} words, which the redactor does not look for elsewhere; refused`,
    )
  }
  const ids = words.map((word) => {
    let id = table.words.get(word)
    if (id === undefined) {
      id = table.words.size
      table.words.set(word, id)
    }
    return id
  })
  const key = ids.join(',')
  if (table.names.has(key)) return
  state.distinct += 1
  if (state.distinct > MAX_DISTINCT_PERSONS) {
    throw new RedactionRefusal(
      `${state.where} names more than ${String(MAX_DISTINCT_PERSONS)} distinct persons, more than the redactor accepts in one document; refused`,
    )
  }
  const replacement = redactPerson(value, ctx)
  table.names.set(key, replacement)
  if (ids.length >= 2) {
    const reversed = [ids[ids.length - 1], ...ids.slice(0, -1)].join(',')
    if (!table.names.has(reversed)) table.names.set(reversed, replacement)
  }
  table.maxWords = Math.max(table.maxWords, ids.length)
}

/** redactDocument redacts a whole document and then audits it: first the
 * people it names, then every value, then the audit with both in hand, and last
 * **the fixed-point check**: redacting the output again must change nothing.
 * A rule that a later replacement switched off, or a shape a replacement made,
 * is a refusal, not a fixture. */
function redactDocument(document: unknown, ctx: Ctx, where = 'a document'): unknown {
  const table: PersonTable = { words: new Map(), names: new Map(), maxWords: 0 }
  collectPersons(document, undefined, null, ctx, table, { expanded: 0, distinct: 0, where }, 0)
  const inner: Ctx = { ...ctx, persons: table }
  const redacted = redactValue(document, undefined, null, inner)
  const problems = auditProblems(redacted, ctx.literals, table, where)
  if (problems.length > 0) {
    throw new RedactionRefusal(`${where} is still carrying a redaction class after redaction: ${problems.join('; ')}`)
  }
  if (JSON.stringify(redactValue(redacted, undefined, null, inner)) !== JSON.stringify(redacted)) {
    throw new RedactionRefusal(`${where} is not a fixed point of the redaction: redacting it again changes it`)
  }
  return redacted
}

/** redactFeedLine parses one line of a feed and returns the redacted line. A
 * refusal says how long the line was, never what it said. */
function redactFeedLine(line: string, ctx: Ctx): string {
  if (line.length > MAX_REDACTABLE_LINE_LENGTH) {
    throw new RedactionRefusal(tooLong('a feed line', MAX_REDACTABLE_LINE_LENGTH))
  }
  const decoded = decodeFeedLine(line)
  if (decoded.kind !== 'document') {
    throw new RedactionRefusal(
      `a feed line that is not a JSON object cannot be redacted (${String(line.length)} characters, not echoed)`,
    )
  }
  return JSON.stringify(redactDocument(decoded.document, ctx, 'a feed document'))
}

// ---------------------------------------------------------------------------
// The audit.
// ---------------------------------------------------------------------------

/** AUDIT_CTX is the context the audit applies rules in. Its pseudonym is a
 * constant: the audit only asks whether a rule *would change* a string, and the
 * repository rule leaves an existing pseudonym alone whatever the key. */
const AUDIT_CTX: Ctx = { literals: [], persons: null, pseudonym: (prefix) => `${prefix}-00000000` }

/** SHOWN_KEY_RE is a key a refusal may print in the path it names: a plain
 * snake_case name, as every key the CLI writes is. Any other key -- and any key
 * that itself carries a class -- is printed as its position (`<key 3>`), so a
 * refusal on the terminal never spells the identity it refused. */
const SHOWN_KEY_RE = /^[a-z][a-z0-9_]{0,39}$/

/**
 * auditProblems reports every redaction class still present in a value, with the
 * path it was found at. `redactDocument` calls it on its own output, and
 * `loadFixture` on every document it loads. A refusal names the class and the
 * path, **never the value**.
 *
 * The audit is stricter than "does redaction find anything": a **reserved key**
 * -- and everything under it -- must carry its class's marker, so
 * `{"project": "fugaro"}` and `{"branch": {"name": "dm/q4"}}` are problems even
 * though no pattern class would match.
 */
function auditProblems(
  value: unknown,
  literals: readonly LiteralRule[],
  persons: PersonTable | null,
  where = 'a document',
  trail = '',
  cls: FieldClass | undefined = undefined,
  key: string | null = null,
  depth = 0,
): string[] {
  const here = at(where, trail)
  const problems: string[] = []
  if (depth > MAX_DEPTH) return [`${here} is ${tooDeep('a value')}`]
  if (typeof value === 'string') {
    if (value.length > MAX_REDACTABLE_STRING_LENGTH) {
      return [`${here} holds ${tooLong('a string', MAX_REDACTABLE_STRING_LENGTH)}`]
    }
    if (value.normalize('NFKC').length > 2 * value.length + MAX_EXPANSION_SLACK) {
      return [`${here} holds ${expansionRefusal()}`]
    }
    if (cls !== undefined && !matchesClass(value, cls)) {
      problems.push(`${here} holds a value (${String(value.length)} characters) that is not this class's marker`)
    }
    // A hex digest under a digest key is not identity, and the entropy rule
    // is not asked about it.
    if (cls === 'digest' && HEX_RE.test(value)) return problems
    problems.push(...auditNames(value, literals, isContractKey(key) ? null : persons, here, 'still carries'))
    problems.push(...auditString(value, here))
    return dedupe(problems)
  }
  if (typeof value === 'number') {
    if (cls !== undefined && cls !== 'digest') problems.push(`${here} holds a number where this class's marker belongs`)
    if (numberCarriesLiteral(value, literals)) problems.push(`${here} still carries one of the installation's own literals`)
    return problems
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      problems.push(...auditProblems(item, literals, persons, where, `${trail}[${String(index)}]`, cls, key, depth + 1))
    })
    if (cls === undefined && joinedCredentialElements(value).size > 0) {
      problems.push(`${here} holds adjacent strings that join into something credential-shaped`)
    }
    return dedupe(problems)
  }
  if (value !== null && typeof value === 'object') {
    Object.entries(value as Record<string, unknown>).forEach(([childKey, child], index) => {
      if (childKey === '__proto__') {
        problems.push(`${here} has ${PROTO_REFUSAL}`)
        return
      }
      if (childKey.length > MAX_REDACTABLE_STRING_LENGTH) {
        problems.push(`${here} has ${tooLong('a key', MAX_REDACTABLE_STRING_LENGTH)}`)
        return
      }
      // **The key is a string too, and it is the one string redaction cannot
      // replace**, so a key that carries identity is a refusal.
      const keyProblems = auditKey(childKey, literals, persons, here)
      problems.push(...keyProblems)
      const shown = keyProblems.length === 0 && SHOWN_KEY_RE.test(childKey) ? childKey : `<key ${String(index + 1)}>`
      const childClass = subtreeClass(cls) ?? classFor(childKey, key)
      problems.push(
        ...auditProblems(child, literals, persons, where, `${trail}.${shown}`, childClass, childKey, depth + 1),
      )
    })
    return dedupe(problems)
  }
  return problems
}

/** auditNames reports a literal, or a person the document names elsewhere, in a
 * string -- as written, and in its prepared, folded form. */
function auditNames(
  value: string,
  literals: readonly LiteralRule[],
  persons: PersonTable | null,
  here: string,
  verb: string,
): string[] {
  const problems: string[] = []
  const form = nameForm(value)
  if (literals.some((rule) => hits(rule.re, value) || hits(rule.re, form))) {
    problems.push(`${here} ${verb} one of the installation's own literals`)
  }
  if (findPersons(form, persons, true).length > 0) {
    problems.push(`${here} ${verb} a person the document names in a person field`)
  }
  return problems
}

/** auditKey reports whether a *key* carries identity. */
function auditKey(
  key: string,
  literals: readonly LiteralRule[],
  persons: PersonTable | null,
  here: string,
): string[] {
  const problems = auditNames(key, literals, persons, `${here} is a key that`, 'carries')
  problems.push(...auditString(key, here, true))
  return dedupe(problems)
}

/** auditString reports the pattern classes a single string still carries, as
 * written and prepared: a class is present when its rule would change the
 * string. */
function auditString(value: string, here: string, asKey = false): string[] {
  if (value === '' || isMarker(value)) return []
  const say = (what: string): string => (asKey ? `${here} is a key that carries ${what}` : `${here} still holds ${what}`)
  const problems: string[] = []
  const forms = [value]
  const prepared = prepare(value)
  if (prepared !== value) forms.push(prepared)
  for (const rule of RULES) {
    if (forms.some((form) => rule.apply(form, AUDIT_CTX, false) !== form)) problems.push(say(rule.what))
  }
  // The redactor never writes one (`redactFreeText`), and a control string is
  // where a token hides from every rule above.
  if (stripTerminalSequences(value) !== value) problems.push(say('a terminal escape sequence'))
  return dedupe(problems)
}

/** matchesClass answers whether a reserved key's value carries its class's
 * marker (or its pseudonym's grammar). An empty value passes. */
function matchesClass(value: string, cls: FieldClass): boolean {
  if (value === '') return true
  switch (cls) {
    case 'repository':
      return REPO_PSEUDONYM_RE.test(value)
    case 'slug':
      return SLUG_PSEUDONYM_RE.test(value) || value === PLACEHOLDER
    case 'person':
      return PERSON_PSEUDONYM_RE.test(value) || isAddressMarker(value) || value === PLACEHOLDER
    case 'by':
      return (
        REPORT_DIMENSIONS.has(value) || PERSON_PSEUDONYM_RE.test(value) || isAddressMarker(value) || value === PLACEHOLDER
      )
    case 'project':
      return value === PROJECT_MARKER
    case 'bucket':
      return value === BUCKET_MARKER
    case 'host':
      return value === HOST_MARKER
    case 'path':
      return value === PATH_MARKER
    case 'url':
      return value === URL_MARKER
    case 'token':
      return value === TOKEN_MARKER
    case 'branch':
      return value === BRANCH_MARKER
    case 'opaque':
      return value === LITERAL_MARKER
    case 'digest':
      return true
  }
}

/** at renders the place a problem was found. */
function at(where: string, trail: string): string {
  const path = trail.replace(/^\./, '')
  return `${where}${path === '' ? '' : ` at ${path}`}`
}

function dedupe(values: readonly string[]): string[] {
  return [...new Set(values)]
}

/** auditRedacted checks a value the way a loader can: with no literals in hand.
 * Every other class -- addresses, hosts, branch fields, tokens, paths -- needs
 * no list, and is checked. */
export function auditRedacted(value: unknown, where = 'a fixture record'): readonly string[] {
  return auditProblems(value, [], null, where)
}

/** assertRedacted throws when the audit finds anything, naming what. */
export function assertRedacted(value: unknown, where = 'a fixture record'): void {
  const problems = auditRedacted(value, where)
  if (problems.length > 0) {
    throw new RedactionRefusal(`${where} is not redacted: ${problems.join('; ')}`)
  }
}
