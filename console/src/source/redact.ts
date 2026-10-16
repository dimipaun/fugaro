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
//      installation's own literals (case-folded, and with a host's stem).
//      Every string is matched as written **and** in its NFKC form, so a
//      fullwidth `＠` is an `@`. A document with a key nobody anticipated still
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
//      (Task 2.0's `src/shared/sanitize.ts`), not the recorder's. Numbers,
//      booleans, `null` and an **empty string** are passed through untouched:
//      `pr_url: ""` is a run with no pull request, not a URL to redact. A string
//      in which no rule finds anything is returned byte for byte as it came,
//      NFKC or not; one in which a rule finds something comes back in NFKC.
//
// What it is not: it is not a sanitiser (rule 4), it is not a schema (it walks
// whatever shape it is given, and knows no field beyond the key names its rules
// are about), and it is not a substitute for the owner's own review of a
// recording.

import { createHmac, randomBytes } from 'node:crypto'

import { decodeFeedLine } from '../main/spawn'

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

/** PseudonymPrefix names the three pseudonym classes. */
export type PseudonymPrefix = 'r' | 'slug' | 'person'

/**
 * Redactor is the redactor for one recording: the installation's own literals,
 * a pseudonym key nobody can read back, and the functions the recorder and the
 * loader use.
 */
export interface Redactor {
  readonly literals: readonly string[]
  /** redactDocument returns a redacted copy of a whole document. */
  redactDocument(document: unknown, where?: string): unknown
  /** redactText returns redacted free text (a notice line, a log message). */
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
    // The key is held by this closure only: it is not a property of the
    // redactor, so it is neither enumerable nor serialisable.
    pseudonym: (prefix, value) =>
      `${prefix}-${createHmac('sha256', key).update(`${prefix}\u0000${value}`).digest('hex').slice(0, 8)}`,
  }
  return {
    literals: Object.freeze([...literals]),
    redactDocument: (document, where) => redactDocument(document, ctx, where),
    redactText: (text) => redactFreeText(text, ctx),
    redactFeedLine: (line) => redactFeedLine(line, ctx),
    redactRepository: (value) => redactRepository(value, ctx),
    pseudonym: (prefix, value) => ctx.pseudonym(prefix, value),
    problems: (value, where) => auditProblems(value, ctx.literals, where),
  }
}

/** Ctx is what every rule below may reach for. */
interface Ctx {
  readonly literals: readonly LiteralRule[]
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
 * (`acme-merger-codename`), an account (`dimi-work`), a person (`Dimi Paun`),
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
const REPORT_DIMENSIONS: ReadonlySet<string> = new Set(['day', 'week', 'month', 'year', 'repo', 'model', 'person'])

/** classFor is the field class of one key in its object. */
function classFor(rawKey: string, parentKey: string | null): FieldClass | undefined {
  const key = rawKey.toLowerCase()
  if (key === 'by') return parentKey === 'kill' ? 'person' : 'by'
  const fixed = FIELD_CLASSES[key]
  if (fixed !== undefined) return fixed
  if (key.endsWith('_by') || key.endsWith('_email') || key.endsWith('_user')) return 'person'
  if (key.endsWith('_branch')) return 'branch'
  if (key.endsWith('_url')) return 'url'
  if (/(?:^|_)(?:token|secret|password|passwd)$/.test(key)) return 'token'
  return undefined
}

// ---------------------------------------------------------------------------
// The pattern classes.
// ---------------------------------------------------------------------------

/** URL_RE matches every URL. It runs first, so the userinfo, the host and the
 * path inside a URL never get a chance to be what is replaced. */
const URL_RE = /\b[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^\s"'`<>]+/g

/**
 * TOKEN_RES are the credential shapes worth naming: GitHub's token families (and
 * a short-prefixed piece of one, which is what half of a split token is),
 * OpenAI/Anthropic-style keys, Slack's, GitLab's, npm's, AWS's access-key id,
 * Google's API key, access token and refresh token, a JSON Web Token, a bearer
 * header and a PEM private key block. They are a *floor*: the value of a key
 * named `secret` is replaced whatever it looks like, an assignment
 * (`access_token=...`) loses its value, wrapped candidates are decoded first,
 * and a long high-entropy run is a token whatever its prefix.
 */
const TOKEN_RES: readonly RegExp[] = Object.freeze([
  /\b(?:gh[pousr]_|github_pat_|glpat-|npm_)[A-Za-z0-9_]{4,}/g,
  /\bsk-[A-Za-z0-9_-]{16,}\b/g,
  /\bxox[baprs]-[A-Za-z0-9-]{8,}\b/g,
  /\bAKIA[0-9A-Z]{16}\b/g,
  /\bAIza[0-9A-Za-z_-]{35}\b/g,
  /\bya29\.[0-9A-Za-z_-]{8,}/g,
  /(?<![\p{L}\p{N}])1\/\/0[0-9A-Za-z_-]{8,}/gu,
  /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/g,
  /\bBearer\s+[A-Za-z0-9._~+/-]{12,}=*/gi,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g,
])

/** ASSIGNMENT_RE matches a secret-named key and its value in free text --
 * `access_token=...`, `"private_key_id": "..."`, `password: ...` -- and the
 * replacement keeps the name and loses the value. */
const ASSIGNMENT_RE =
  /(?<![\p{L}\p{N}])([\p{L}\p{N}_-]*?(?:token|secret|password|passwd|pwd|api[_-]?key|apikey|private[_-]?key(?:[_-]id)?|credentials?|authorization|signature|cookie))(?![\p{L}\p{N}])(["']?\s*[:=]\s*["']?)([^\s"'&,;]+)/giu

/** WRAPPED_PERCENT_RE is a run with at least one percent-escape in it: a
 * candidate to decode before the token shapes are tried. */
const WRAPPED_PERCENT_RE = /[A-Za-z0-9._~+=-]*(?:%[0-9A-Fa-f]{2}[A-Za-z0-9._~+=-]*)+/g
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
 * a quote, a bracket, `<>|;,:*?` -- so a non-ASCII segment (`/Users/dimí/...`)
 * is consumed whole. **A space is part of a segment when more path follows it**:
 * `/Users/Dimi Paun/secret-plan/x` is one path, because `Paun` is followed by
 * `/`; a sentence after a path (`/etc/hosts for details`) is not, because no
 * slash follows its words. Two slashes is what keeps a sentence from being
 * eaten: `and/or` is not rooted. A single-segment path (`/opt`) in free text is
 * not redacted; the structured `path` key is.
 */
const PATH_SEGMENT = String.raw`[^\s\/\\"'\x60<>|;,:()\[\]{}*?]+`
const ABS_PATH_RE = new RegExp(
  [
    // Rooted where a path can start: a segment may hold spaces.
    String.raw`(?<![\p{L}\p{N}])(?:~\/|\/)(?:SEG(?:[ \t]+SEG)*\/)+SEG`,
    // Rooted after a word character (`Data/Users/...`): no spaces, so the
    // prose `i/o and input/output` is not one path.
    String.raw`(?:~\/|\/)(?:SEG\/)+SEG`,
  ]
    .join('|')
    .replaceAll('SEG', PATH_SEGMENT),
  'gu',
)

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
 * `on project edge-devel-dimi`, `projects/123456789012`, `bucket fugaro-runs-x`.
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
 * `edge-devel-dimi-default-rtdb.firebaseio.com` and `dimis-macbook-pro.local`
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
 * stem: `Dimis-MacBook-Pro.local` is also `Dimis-MacBook-Pro`, because that is
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
    const folded = value.normalize('NFKC')
    if (folded.trim() === '' || values.some((known) => known.toLowerCase() === folded.toLowerCase())) return
    values.push(folded)
  }
  for (const value of literals) {
    add(value)
    const folded = value.normalize('NFKC')
    // A host's stem: the first label of a dotted name with no path in it.
    if (!folded.includes('/') && !folded.includes('\\') && /^[\p{L}\p{N}][\p{L}\p{N}-]*\./u.test(folded)) {
      const stem = folded.slice(0, folded.indexOf('.'))
      if (stem.length >= 3) add(stem)
    }
  }
  // Longest first, so a literal that contains another is replaced whole.
  values.sort((a, b) => b.length - a.length)
  return values.map((value) => {
    const escaped = value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const before = /^[\p{L}\p{N}]/u.test(value) ? '(?<![\\p{L}\\p{N}])' : ''
    const after = /[\p{L}\p{N}]$/u.test(value) ? '(?![\\p{L}\\p{N}])' : ''
    return { value, re: new RegExp(`${before}${escaped}${after}`, 'giu') }
  })
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
  apply(text: string, ctx: Ctx): string
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
    apply: (text) => {
      let out = text.replace(WRAPPED_PERCENT_RE, (run) => {
        let decoded: string
        try {
          decoded = decodeURIComponent(run)
        } catch {
          return run
        }
        return containsSecret(decoded) || isHighEntropy(decoded) ? TOKEN_MARKER : run
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
  if (isMarker(value)) return value
  if (value.includes('@') || value.normalize('NFKC').includes('@')) return ADDRESS_MARKER
  return ctx.pseudonym('person', value.normalize('NFKC'))
}

/** applyRules runs every pattern class, then the installation's literals. */
function applyRules(text: string, ctx: Ctx): string {
  let out = text
  for (const rule of RULES) out = rule.apply(out, ctx)
  for (const rule of ctx.literals) out = out.replace(rule.re, LITERAL_MARKER)
  return out
}

/**
 * redactFreeText runs every pattern class over one string, in the order that
 * keeps a rule from eating another rule's input: URLs first (they contain
 * everything else), then tokens, addresses, branches, paths, repository paths,
 * GCP names, network addresses, hosts, high-entropy runs, and last the
 * installation's own literals.
 *
 * **It runs twice: as written, and in NFKC.** A string a rule finds nothing in,
 * either way, comes back byte for byte; one whose compatibility form still
 * carries a class (a fullwidth address) comes back redacted, in NFKC.
 */
function redactFreeText(text: string, ctx: Ctx): string {
  if (text === '' || isMarker(text)) return text
  const once = applyRules(text, ctx)
  const folded = once.normalize('NFKC')
  if (folded === once) return once
  const twice = applyRules(folded, ctx)
  return twice === folded ? once : twice
}

/** isAddressMarker answers whether an ADDRESS_RE match is the marker itself. */
function isAddressMarker(value: string): boolean {
  return value.toLowerCase() === ADDRESS_MARKER
}

/** redactValue redacts one value with its key and its parent's key in hand. */
function redactValue(value: unknown, key: string | null, parentKey: string | null, ctx: Ctx): unknown {
  if (typeof value === 'string') return redactString(value, key, parentKey, ctx)
  if (Array.isArray(value)) return value.map((item) => redactValue(item, key, parentKey, ctx))
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [childKey, child] of Object.entries(value as Record<string, unknown>)) {
      out[childKey] = redactValue(child, childKey, key, ctx)
    }
    return out
  }
  return value
}

/** HEX_RE is what a digest field may hold and keep. */
const HEX_RE = /^[0-9a-f]{7,64}$/i

/** redactString applies the field class for a key when there is one, and the
 * pattern classes otherwise. An empty string is never a class's value. */
function redactString(value: string, key: string | null, parentKey: string | null, ctx: Ctx): string {
  if (value !== '' && key !== null) {
    switch (classFor(key, parentKey)) {
      case 'repository':
        return redactRepository(value, ctx)
      case 'slug':
        return SLUG_PSEUDONYM_RE.test(value) ? value : ctx.pseudonym('slug', value)
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
      case undefined:
        break
    }
  }
  return redactFreeText(value, ctx)
}

/** redactDocument redacts a whole document and then audits it. */
function redactDocument(document: unknown, ctx: Ctx, where = 'a document'): unknown {
  const redacted = redactValue(document, null, null, ctx)
  const problems = auditProblems(redacted, ctx.literals, where)
  if (problems.length > 0) {
    throw new RedactionRefusal(`${where} is still carrying a redaction class after redaction: ${problems.join('; ')}`)
  }
  return redacted
}

/** redactFeedLine parses one line of a feed and returns the redacted line. */
function redactFeedLine(line: string, ctx: Ctx): string {
  const decoded = decodeFeedLine(line)
  if (decoded.kind !== 'document') {
    throw new RedactionRefusal(
      `a feed line that is not a JSON object cannot be redacted: ${JSON.stringify(line.slice(0, 200))}`,
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
const AUDIT_CTX: Ctx = { literals: [], pseudonym: (prefix) => `${prefix}-00000000` }

/**
 * auditProblems reports every redaction class still present in a value, with the
 * path it was found at. `redactDocument` calls it on its own output, and
 * `loadFixture` on every document it loads.
 *
 * The audit is stricter than "does redaction find anything": a **reserved key**
 * must carry its class's marker, so `{"project": "fugaro"}` and
 * `{"branch": "dm/q4"}` are problems even though no pattern class would match.
 */
function auditProblems(value: unknown, literals: readonly LiteralRule[], where = 'a document', trail = ''): string[] {
  const problems: string[] = []
  if (typeof value === 'string') {
    problems.push(...auditLiterals(value, literals, `${at(where, trail)} still carries one of the installation's own literals`))
    problems.push(...auditString(value, where, trail))
    return dedupe(problems)
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      problems.push(...auditProblems(item, literals, where, `${trail}[${String(index)}]`))
    })
    return dedupe(problems)
  }
  if (value !== null && typeof value === 'object') {
    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      // **The key is a string too, and it is the one string redaction cannot
      // replace**, so a key that carries identity is a refusal.
      problems.push(...auditKey(key, literals, where, trail))
      const cls = classFor(key, parentKeyOf(trail))
      const childTrail = `${trail}.${key}`
      if (typeof child === 'string' && cls !== undefined && !matchesClass(child, cls)) {
        problems.push(
          `${at(where, childTrail)} holds ${JSON.stringify(child.slice(0, 80))}, which is not this class's marker`,
        )
      }
      // A hex digest under a digest key is not identity, and the entropy rule
      // is not asked about it.
      if (cls === 'digest' && typeof child === 'string' && HEX_RE.test(child)) continue
      problems.push(...auditProblems(child, literals, where, childTrail))
    }
    return dedupe(problems)
  }
  return problems
}

/** auditLiterals reports a literal in a string, as written or in NFKC. */
function auditLiterals(value: string, literals: readonly LiteralRule[], say: string): string[] {
  const folded = value.normalize('NFKC')
  for (const rule of literals) {
    if (hits(rule.re, value) || hits(rule.re, folded)) return [say]
  }
  return []
}

/** auditKey reports whether a *key* carries identity. */
function auditKey(key: string, literals: readonly LiteralRule[], where: string, trail: string): string[] {
  const problems = auditLiterals(key, literals, `${at(where, trail)} is a key that carries one of the installation's own literals`)
  problems.push(...auditString(key, where, trail, true))
  return dedupe(problems)
}

/** auditString reports the pattern classes a single string still carries, as
 * written and in NFKC: a class is present when its rule would change the
 * string. */
function auditString(value: string, where: string, trail: string, asKey = false): string[] {
  if (value === '' || isMarker(value)) return []
  const say = (what: string): string =>
    asKey ? `${at(where, trail)} is a key that carries ${what}` : `${at(where, trail)} still holds ${what}`
  const problems: string[] = []
  const forms = [value]
  const folded = value.normalize('NFKC')
  if (folded !== value) forms.push(folded)
  for (const rule of RULES) {
    if (forms.some((form) => rule.apply(form, AUDIT_CTX) !== form)) problems.push(say(rule.what))
  }
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
      return SLUG_PSEUDONYM_RE.test(value)
    case 'person':
      return PERSON_PSEUDONYM_RE.test(value) || isAddressMarker(value)
    case 'by':
      return REPORT_DIMENSIONS.has(value) || PERSON_PSEUDONYM_RE.test(value) || isAddressMarker(value)
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

/** parentKeyOf returns the last key of a trail, array indices stripped, so
 * `kill[0].by` has the parent `kill`. */
function parentKeyOf(trail: string): string | null {
  const trimmed = trail.replace(/(?:\[\d+\])+$/, '')
  const index = trimmed.lastIndexOf('.')
  return index < 0 ? null : trimmed.slice(index + 1)
}

function dedupe(values: readonly string[]): string[] {
  return [...new Set(values)]
}

/** auditRedacted checks a value the way a loader can: with no literals in hand.
 * Every other class -- addresses, hosts, branch fields, tokens, paths -- needs
 * no list, and is checked. */
export function auditRedacted(value: unknown, where = 'a fixture record'): readonly string[] {
  return auditProblems(value, [], where)
}

/** assertRedacted throws when the audit finds anything, naming what. */
export function assertRedacted(value: unknown, where = 'a fixture record'): void {
  const problems = auditRedacted(value, where)
  if (problems.length > 0) {
    throw new RedactionRefusal(`${where} is not redacted: ${problems.join('; ')}`)
  }
}
