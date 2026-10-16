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
//      value is (`repo`, `slug`, `pr_url`, `requested_by`, ...), and *pattern
//      classes*, which apply to every string in the document whatever its key
//      is -- an address, a token, a URL, an absolute path, a branch name, a
//      repository path, or one of the installation's own literals. A document
//      with a key nobody anticipated still cannot carry an address out of the
//      room. **The keys are strings too**, and a key is the one string that
//      cannot be replaced: renaming it would change a shape the console reads,
//      so a key carrying identity is a *refusal* instead (`auditKey`) -- an
//      address, a repository path, a checkout or the operator's project name in
//      the key position fails the recording rather than being written out.
//   2. **Redaction is a marker, not a deletion, and the markers are a closed
//      set** (`<path>`, `<project>`, `<host>`, `<branch>`, `[redacted-token]`,
//      `[redacted-url]`, `redacted@example.invalid`, `[redacted]`). A marked
//      value is unambiguous on sight; nothing in a fixture can be mistaken for
//      real data, and a value that is *already* marked is left alone, which is
//      what makes the whole function idempotent: `redact(redact(x)) ==
//      redact(x)`. Two classes keep a per-value pseudonym instead of a fixed
//      marker -- a repository (`r-1a2b3c4d/r-5e6f7a8b`) and a slug
//      (`slug-1a2b3c4d`) -- because a fixture with several repositories is only
//      worth having if the rows stay distinguishable. A pseudonym is a SHA-256
//      of the value with a fixed, public domain separator: it is one-way, so it
//      reveals nothing, and it is stable, so the same repository is the same
//      pseudonym in every fixture.
//   3. **It fails closed, twice.** A line that is not a JSON object is refused
//      rather than passed through, and every document is *audited* after
//      redaction: if any class is still present -- an address, a token, a URL, a
//      path, a branch, a repository path, a literal, or a reserved key whose
//      value is not a marker -- the call throws. A bug in a rule is therefore a
//      failed recording, not a leaked fixture. `loadFixture` runs the same audit
//      on load, so a fixture edited by hand after it was written is refused too.
//   4. **It redacts identity, not hostility, and it does not invent data.** A
//      `<script>` in a title stays a `<script>`: the design wants a hostile-title
//      *recording*, and making text safe to *render* is the display layer's job
//      (Task 2.0's `src/shared/sanitize.ts`), not the recorder's. Numbers,
//      booleans, `null` and an **empty string** are passed through untouched:
//      `pr_url: ""` is a run with no pull request, not a URL to redact, and a
//      fixture whose absent fields had been turned into markers would be a
//      fixture that lies about absence. The one bound this module keeps on text
//      is the CLI's own: `watch` has already clipped every database-derived
//      string before it emitted the document (`internal/watch/build.go`'s
//      `clean`, `maxText` = 200 runes), and the recorder lengthens nothing.
//
// What it is not: it is not a sanitiser (rule 4), it is not a schema (it walks
// whatever shape it is given, because a document is the CLI's and this module
// deliberately knows no field of it beyond the key names its rules are about),
// and it is not a substitute for the owner's own review of a recording.

import { createHash } from 'node:crypto'

import { decodeFeedLine } from '../main/spawn'

/**
 * REDACTOR_VERSION is the marker written into every fixture's header and checked
 * on load. **The version is the contract**: a fixture whose header names a
 * redactor this build does not know is refused by `loadFixture` rather than
 * replayed, because a fixture's safety is a property of the rules that made it
 * and those rules are not the ones in front of it. A change to any rule below,
 * to the marker set, or to the pseudonym derivation is a change to this string.
 */
export const REDACTOR_VERSION = 'fugaro-console-redact/1'

/**
 * PSEUDONYM_SALT is the domain separator the pseudonyms are derived with. It is
 * **not a key and must not be treated as one**: it is a fixed, public constant
 * whose only job is to keep this module's hashes from colliding with any other
 * digest in the repository. A pseudonym is safe because SHA-256 of a repository
 * name is not invertible, not because this string is secret.
 */
export const PSEUDONYM_SALT = 'fugaro-console-fixture'

/** PATH_MARKER replaces a filesystem path: a home directory, a checkout, a
 * temporary directory, a binary's location. */
export const PATH_MARKER = '<path>'
/** PROJECT_MARKER replaces the project or installation name. */
export const PROJECT_MARKER = '<project>'
/** HOST_MARKER replaces a machine's name. */
export const HOST_MARKER = '<host>'
/** BRANCH_MARKER replaces a git branch or ref. */
export const BRANCH_MARKER = '<branch>'
/** TOKEN_MARKER replaces anything credential-shaped, or the value of a key that
 * names one -- **including a value that does not look like a token**, because a
 * key called `secret` is reason enough. */
export const TOKEN_MARKER = '[redacted-token]'
/** URL_MARKER replaces **every** URL, not only one with userinfo: a URL carries
 * a host and a path, and both are installation-specific. It is strictly stronger
 * than the userinfo rule the plan names, and that case is tested by name. */
export const URL_MARKER = '[redacted-url]'
/** ADDRESS_MARKER replaces an email address. Its own domain is `.invalid`
 * (RFC 2606), which is also how the audit tells a marker from a real address. */
export const ADDRESS_MARKER = 'redacted@example.invalid'
/** LITERAL_MARKER replaces one of the installation's own literals -- the project
 * name the recorder was told, the machine's hostname, the operator's user name,
 * their home directory, the checkout, the config path, the binary's path. */
export const LITERAL_MARKER = '[redacted]'

/** REPO_PSEUDONYM_RE is the grammar of a redacted repository: one or two
 * pseudonym segments. A one-segment value is what a repository-shaped field
 * carries when it had no `owner/` half. */
export const REPO_PSEUDONYM_RE = /^r-[0-9a-f]{8}(?:\/r-[0-9a-f]{8})?$/
/** SLUG_PSEUDONYM_RE is the grammar of a redacted slug. */
export const SLUG_PSEUDONYM_RE = /^slug-[0-9a-f]{8}$/
/** PERSON_PSEUDONYM_RE is the grammar of a redacted person: a requester, an
 * author, whoever flipped a kill switch. A person's address is a marker instead;
 * a person's name is a pseudonym, so two requesters stay two. */
export const PERSON_PSEUDONYM_RE = /^person-[0-9a-f]{8}$/

/** MARKERS is the closed set of fixed markers, for the audit and for readers of
 * this file: every one of them is unmistakable on sight. */
export const MARKERS: readonly string[] = Object.freeze([
  PATH_MARKER,
  PROJECT_MARKER,
  HOST_MARKER,
  BRANCH_MARKER,
  TOKEN_MARKER,
  URL_MARKER,
  ADDRESS_MARKER,
  LITERAL_MARKER,
])

/** RedactionRefusal is thrown when a line cannot be redacted, or when the audit
 * still finds something after redacting. The recorder writes nothing and says
 * why; that is the whole of rule 3. */
export class RedactionRefusal extends Error {
  override readonly name = 'RedactionRefusal'
}

/**
 * Redactor is the redactor as a value: the installation's own literals, plus the
 * functions the recorder and the loader use. It is an object rather than a set
 * of free functions because the literal list is state -- the same document
 * redacted with two different literal lists is two different results, and a
 * fixture's header should say which one produced it.
 */
export interface Redactor {
  readonly literals: readonly string[]
  /** redactDocument returns a redacted copy of a whole document. */
  redactDocument(document: unknown, where?: string): unknown
  /** redactText returns redacted free text (a notice line, a log message). */
  redactText(text: string): string
  /** redactFeedLine parses one line of a feed and returns the redacted line as
   * the fixture writer serialises it. It throws on a line that is not a JSON
   * object rather than passing it through. */
  redactFeedLine(line: string): string
  /** problems reports every class still present in a value. Empty means clean;
   * `redactDocument` and `loadFixture` both throw when it is not. */
  problems(value: unknown, where?: string): readonly string[]
}

/**
 * createRedactor builds the redactor for one recording. `literals` are the
 * strings the operator's installation is known by -- the project name, the
 * hostname, the user name, the home directory, the checkout, the config path,
 * the binary's path -- and each is removed wherever it appears, in any field,
 * including inside a title. The list is the recorder's **deny-by-default** half:
 * the pattern classes catch what has a shape, and this catches the names that do
 * not.
 */
export function createRedactor(literals: readonly string[] = []): Redactor {
  const rules = literalPatterns(literals)
  return {
    literals: Object.freeze([...literals]),
    redactDocument: (document, where) => redactDocument(document, rules, where),
    redactText: (text) => redactFreeText(text, rules),
    redactFeedLine: (line) => redactFeedLine(line, rules),
    problems: (value, where) => auditProblems(value, rules, where),
  }
}

// ---------------------------------------------------------------------------
// The field classes.
// ---------------------------------------------------------------------------

/** FieldClass is one row of `FIELD_CLASSES`. */
type FieldClass = 'repository' | 'slug' | 'project' | 'path' | 'host' | 'url' | 'token' | 'person'

/**
 * FIELD_CLASSES names the keys whose *whole value* is one class, and it is a
 * closed table a reviewer can read: each row says what the key is and what
 * replaces it. A key not on this table still goes through every pattern class --
 * which is what makes the table an addition rather than the only defence -- and
 * a key on it is *stricter*: its value must come out as that class's marker, and
 * the audit refuses the document if it does not.
 *
 * The names are the documents' own (`internal/watch/json.go`, `runview.Row`,
 * `docs/json-contract.md` section 10.13): `repo`, `slug`, `pr_url`,
 * `requested_by`. **Free text is not on this table at all** -- `title`,
 * `message`, `halted`, `reason`, `queued_note`, `outcome` and every key nobody
 * has written yet go through the pattern classes, which is exactly what the
 * plan's red step asks for ("a recorder that passes a title through untouched"
 * must fail).
 *
 * The bare key `by` is deliberately absent: it is a person inside a `kill`
 * object (`JSONKill.by`) and a break-down dimension in `report --json`, so it is
 * decided by its parent key in `classFor` rather than by this table.
 */
const FIELD_CLASSES: Readonly<Record<string, FieldClass>> = Object.freeze({
  // A repository, in `owner/name` form (`JSONRun.repo`, `JSONFinished.repo`,
  // `JSONRepo.repo`, `runview.Row.repo`).
  repo: 'repository',
  repository: 'repository',
  // The bucket-path slug: a one-way hash of provider and repository
  // (`internal/task/task.go`'s `Slug`), and still an identifier of a private
  // repository, so it is pseudonymised too.
  slug: 'slug',
  // The project / installation name, and the GCP project it reads.
  project: 'project',
  gcp_project: 'project',
  project_id: 'project',
  // Where things are: a checkout, a config, a binary, a working directory.
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
  // A link: the pull request, the Cloud console, anything else.
  url: 'url',
  pr_url: 'url',
  link: 'url',
  href: 'url',
  // Anything that names a credential. The value does not have to look like a
  // token: a key called `secret` is reason enough to lose the value.
  token: 'token',
  access_token: 'token',
  refresh_token: 'token',
  secret: 'token',
  client_secret: 'token',
  password: 'token',
  passwd: 'token',
  private_key: 'token',
  authorization: 'token',
  bearer: 'token',
  api_key: 'token',
  apikey: 'token',
  credential: 'token',
  credentials: 'token',
  // A person: the requester of a queued run, an author, an account holder.
  requested_by: 'person',
  requester: 'person',
  author: 'person',
  email: 'person',
  username: 'person',
  owner_email: 'person',
})

/** classFor is the field class of one key in its object: the table, plus the
 * one key whose class depends on where it is. */
function classFor(key: string, parentKey: string | null): FieldClass | undefined {
  if (key === 'by' && parentKey === 'kill') return 'person'
  return FIELD_CLASSES[key]
}

// ---------------------------------------------------------------------------
// The pattern classes.
// ---------------------------------------------------------------------------

/** URL_RE matches every URL. Order matters: it runs first, so the userinfo, the
 * host and the path inside a URL never get a chance to be what is replaced. */
const URL_RE = /\b[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^\s"'`<>]+/g

/**
 * TOKEN_RES are the credential shapes worth naming: GitHub's four token families
 * and a personal access token, OpenAI/Anthropic-style keys, Slack's, AWS's
 * access-key id, Google's API key, a JSON Web Token, a bearer header and a PEM
 * private key block. They are a *floor*, not the defence: the value of a key
 * named `secret` is replaced whatever it looks like (`FIELD_CLASSES`), and the
 * recorder is told the installation's own literals.
 */
const TOKEN_RES: readonly RegExp[] = Object.freeze([
  /\bgh[pousr]_[A-Za-z0-9]{16,}\b/g,
  /\bgithub_pat_[A-Za-z0-9_]{16,}\b/g,
  /\bsk-[A-Za-z0-9_-]{16,}\b/g,
  /\bxox[baprs]-[A-Za-z0-9-]{8,}\b/g,
  /\bAKIA[0-9A-Z]{16}\b/g,
  /\bAIza[0-9A-Za-z_-]{35}\b/g,
  /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/g,
  /\bBearer\s+[A-Za-z0-9._~+/-]{12,}=*/gi,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g,
])

/** ADDRESS_RE matches an email address. A match whose domain is `.invalid` is
 * the marker itself (RFC 2606) and is left alone -- which is what keeps
 * `redactFreeText` idempotent and what the audit reads to tell a marker from a
 * real address. */
const ADDRESS_RE = /\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\b/g

/**
 * ABS_PATH_RE matches an absolute POSIX path: a run rooted at `/` (or at `~/`)
 * with at least two segments.
 *
 * **This rule is deliberately not a prefix whitelist, and an earlier version's
 * was a real hole.** That version listed the prefixes a person expects --
 * `Users|home|root|private|var|tmp|opt|srv|mnt|Volumes|Applications` -- and
 * required the match not to be preceded by a word character. Both halves missed
 * paths a recording really carries: `/usr/local/bin/<binary>`, `/etc/<...>/config.json`
 * and `/Library/...` were never matched whatever the context, and macOS's
 * firmlink form -- `/System/Volumes/Data/Users/<user>/src/<binary>`, which is
 * what a real `HOME` looks like on Catalina and later -- was missed twice over,
 * because `/System` is not on the list and the `/Volumes/...` inside it is
 * preceded by the `m` of `System`. A recording of the operator's own checkout
 * walked straight through, and the audit shared the pattern, so the audit
 * reported clean.
 *
 * What it matches now is any `/`-rooted run of path characters **with a second
 * slash in it**, plus the `~/` form. Two slashes is what keeps a sentence from
 * being eaten: a lone `/` in prose ("either/or", "24/7", "and/or") has no
 * leading `/` at all, and a relative path (`src/source/redact.ts`) is not
 * rooted, so neither is touched. The cost is that a single-segment absolute
 * path (`/opt`, `/etc`) in free text is not redacted -- a structured key carrying
 * one is (`FIELD_CLASSES`'s `path` class), and the trade is worth stating rather
 * than hiding.
 */
const ABS_PATH_RE = /(?:~\/|\/)[A-Za-z0-9._@+-]+(?:\/[A-Za-z0-9._@+-]+)+/g

/** WIN_PATH_RE matches a Windows path and a UNC path, so a recording made on one
 * machine is not safe only because the rules were written on another. */
const WIN_PATH_RE = /(?:\b[A-Za-z]:\\[^\s"'`,;:)\]}]*|\\\\[^\s"'`,;:)\]}]+)/g

/** BRANCH_RE matches a git branch or ref. A branch name is an identifier a
 * person chose, and one of the classes the plan names, so it is replaced
 * wherever it appears -- including inside a title. */
const BRANCH_RE =
  /\b(?:refs\/heads\/)?(?:feature|feat|fix|bugfix|hotfix|chore|release|dependabot|renovate|topic|wip)\/[A-Za-z0-9._\/-]+/g

/**
 * REPO_TOKEN_RE matches a repository path anywhere a string carries one -- `in
 * dimipaun/fugaro#412` in a title, `github.com/acme/secret-repo` in a log line --
 * which is the class the structured `repo` key alone would miss.
 *
 * **Two shapes, and both halves were wrong in an earlier version.** It matched a
 * bare `owner/name` only, with a lookbehind that refused a match after `.`: so
 * `see github.com/acme/secret-repo for details` came out unchanged *and audited
 * clean*, because the URL rule needs a scheme (`://`), the path rule needs a
 * leading `/`, and the lookbehind rejected the match after the host's dot. A
 * dotted host prefix is now part of the shape. And every segment must be at
 * least **four** characters, where the earlier version asked for two: two let
 * the prose pair `fix auth and/or retries` through and turned it into
 * `fix auth r-fbfed1d0/r-554072a2 retries` -- a repository-shaped token the
 * document never contained, which is the one thing rule 4 forbids. `and/or`,
 * `he/she`, `him/her`, `either/or`, `w/o`, `i/o`, `n/a`, `y/n` and `24/7` are
 * all shorter than four on one side and are now left as they were written.
 *
 * A match preceded by a word character, a slash, a dot or a tilde is not a match
 * (it is part of a longer path or a word), and one that is already a pseudonym
 * is left alone, which keeps the rule idempotent.
 *
 * **The host prefix is the second of two rules, not the only one**, and no test
 * isolates it: the pattern class above now matches *any* `/`-rooted run of
 * segments, where it once needed a whitelisted first segment, so
 * `github.com/acme/secret-repo` is reached by both. It is kept because the shape
 * is the repository class's business and a reader of this rule should see it
 * here; a test that claimed to pin it alone would be asserting the path class's
 * work.
 *
 * **The false positive this keeps, said out loud:** two ordinary words of four
 * characters or more -- `input/output`, `read/write`, `yes/no` is short but
 * `input/output` is not -- are pseudonymised as though they were a repository.
 * The two directions do not cost the same thing: this one costs a recorded
 * title's wording, and the other costs a private repository's name in a
 * committed file. Nothing in the text itself tells the two apart, and nothing
 * downstream parses a title, so the rule takes the first.
 */
const REPO_TOKEN_RE =
  /(?<![\w./~-])(?:(?:[A-Za-z0-9-]+\.)+[A-Za-z0-9-]+\/)?[A-Za-z0-9][A-Za-z0-9._-]{3,}\/[A-Za-z0-9][A-Za-z0-9._-]{3,}(?![\w/~-])/g

/** LiteralRule is one of the installation's own strings, as a pattern that only
 * matches it on a boundary. */
interface LiteralRule {
  readonly value: string
  readonly re: RegExp
}

/**
 * literalPatterns turns the installation's literals into boundary-anchored
 * patterns. The boundary matters at one end only, and which end depends on the
 * literal's own first and last character.
 *
 * A literal that begins with a letter or a digit is matched only where it is not
 * glued to another word character, because replacing a user name called `ann`
 * inside `announcement` would corrupt the recording rather than protect it. A
 * literal that begins with anything else -- `/Users/ana`, `~/src`, `github.com` --
 * is matched wherever it stands, because **its own first character is the
 * boundary**: a `/` is not a word character, so requiring a non-word character
 * before it is what let `Data/Users/ana/...` through, where the operator's real
 * home directory sits inside macOS's firmlink path. The trailing side is the
 * mirror of the same rule.
 *
 * An earlier version also refused a match preceded by `/` or `\` for literals
 * shorter than four characters. That exclusion is gone: a literal preceded by a
 * path separator is a **path segment**, which is exactly a place a user name
 * appears (`/Users/ana`), and over-redaction is the safe direction. What is kept
 * is the word boundary on both sides of an alphanumeric literal, so `ann` in
 * `announcement` is still left alone.
 */
function literalPatterns(literals: readonly string[]): readonly LiteralRule[] {
  const rules: LiteralRule[] = []
  for (const value of literals) {
    if (value === '') continue
    const escaped = value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const before = /^[A-Za-z0-9]/.test(value) ? '(?<![A-Za-z0-9])' : ''
    const after = /[A-Za-z0-9]$/.test(value) ? '(?![A-Za-z0-9])' : ''
    rules.push({ value, re: new RegExp(`${before}${escaped}${after}`, 'g') })
  }
  return rules
}

/** hits is `RegExp.test` for a shared, global pattern: it re-anchors the pattern
 * first, because `lastIndex` survives between calls on a `/g` regex and a
 * one-character-wide audit bug is the kind nobody finds by reading. */
function hits(re: RegExp, text: string): boolean {
  re.lastIndex = 0
  return re.test(text)
}

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

/** pseudonym derives a stable, one-way pseudonym for a value. */
export function pseudonym(prefix: 'r' | 'slug' | 'person', value: string): string {
  const digest = createHash('sha256').update(`${PSEUDONYM_SALT}\u0000${value}`).digest('hex')
  return `${prefix}-${digest.slice(0, 8)}`
}

/** redactRepository replaces a repository with its pseudonym, keeping the
 * `owner/name` shape the console's own argument schema depends on
 * (`src/main/ipc.ts`'s `repository` kind), and keeping two repositories two. */
export function redactRepository(value: string): string {
  if (REPO_PSEUDONYM_RE.test(value)) return value
  const parts = value.split('/')
  const owner = parts[0] ?? ''
  const name = parts[1] ?? ''
  if (parts.length === 2 && owner !== '' && name !== '') {
    return `${pseudonym('r', owner)}/${pseudonym('r', name)}`
  }
  return pseudonym('r', value)
}

/** redactPerson replaces a person: anything holding an address becomes the
 * address marker (the value is replaced whole, so a display name in front of an
 * address does not survive it), and anything else becomes a pseudonym, so two
 * requesters stay two. */
export function redactPerson(value: string): string {
  if (isMarker(value)) return value
  if (value.includes('@')) return ADDRESS_MARKER
  return pseudonym('person', value)
}

/**
 * redactFreeText runs every pattern class over one string, in the order that
 * keeps a rule from eating another rule's input: URLs first (they contain
 * everything else), then tokens, then addresses, then **branches**, then paths,
 * then repository paths, then the installation's own literals. A value that is
 * already a marker is returned untouched, which is what makes the function
 * idempotent.
 *
 * **Branches run before paths** because the path rule is the broad one: it
 * matches any `/`-rooted run of segments, so `refs/heads/fix/cap-rollover`
 * would come out as `refs<path>` -- redacted, but as the wrong class, and the
 * branch class is one the plan names. The reverse collision is bounded: a real
 * path that happens to contain a `feat/`, `fix/`, `chore/`-style segment comes
 * out as two markers (`<path><branch>`) rather than one, which over-redacts the
 * same characters and never leaves one behind.
 */
export function redactFreeText(text: string, literals: readonly LiteralRule[]): string {
  if (text === '' || isMarker(text)) return text
  let out = text.replace(URL_RE, URL_MARKER)
  for (const re of TOKEN_RES) out = out.replace(re, TOKEN_MARKER)
  out = out.replace(ADDRESS_RE, (match) => (isAddressMarker(match) ? match : ADDRESS_MARKER))
  out = out.replace(BRANCH_RE, BRANCH_MARKER)
  out = out.replace(ABS_PATH_RE, PATH_MARKER)
  out = out.replace(WIN_PATH_RE, PATH_MARKER)
  out = out.replace(REPO_TOKEN_RE, (match) => (REPO_PSEUDONYM_RE.test(match) ? match : redactRepository(match)))
  for (const rule of literals) out = out.replace(rule.re, LITERAL_MARKER)
  return out
}

/** isAddressMarker answers whether an ADDRESS_RE match is the marker itself. */
function isAddressMarker(value: string): boolean {
  return value.toLowerCase().endsWith(ADDRESS_MARKER.slice(ADDRESS_MARKER.indexOf('@')))
}

/** redactValue redacts one value with its key and its parent's key in hand. */
function redactValue(
  value: unknown,
  key: string | null,
  parentKey: string | null,
  literals: readonly LiteralRule[],
): unknown {
  if (typeof value === 'string') return redactString(value, key, parentKey, literals)
  if (Array.isArray(value)) return value.map((item) => redactValue(item, key, parentKey, literals))
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [childKey, child] of Object.entries(value as Record<string, unknown>)) {
      out[childKey] = redactValue(child, childKey, key, literals)
    }
    return out
  }
  return value
}

/** redactString applies the field class for a key when there is one, and the
 * pattern classes otherwise. An empty string is never a class's value: `""` is
 * how these documents say "absent", and a fixture must not claim a run had a
 * pull request whose URL is `[redacted-url]`. */
function redactString(
  value: string,
  key: string | null,
  parentKey: string | null,
  literals: readonly LiteralRule[],
): string {
  if (value !== '' && key !== null) {
    switch (classFor(key, parentKey)) {
      case 'repository':
        return redactRepository(value)
      case 'slug':
        return SLUG_PSEUDONYM_RE.test(value) ? value : pseudonym('slug', value)
      case 'project':
        return PROJECT_MARKER
      case 'host':
        return HOST_MARKER
      case 'path':
        return PATH_MARKER
      case 'url':
        return URL_MARKER
      case 'token':
        return TOKEN_MARKER
      case 'person':
        return redactPerson(value)
      case undefined:
        break
    }
  }
  return redactFreeText(value, literals)
}

/** redactDocument redacts a whole document and then audits it: what a rule
 * missed is a refusal, not a fixture (rule 3). */
function redactDocument(document: unknown, literals: readonly LiteralRule[], where = 'a document'): unknown {
  const redacted = redactValue(document, null, null, literals)
  const problems = auditProblems(redacted, literals, where)
  if (problems.length > 0) {
    throw new RedactionRefusal(
      `${where} is still carrying a redaction class after redaction: ${problems.join('; ')}`,
    )
  }
  return redacted
}

/** redactFeedLine parses one line of a feed and returns the redacted line. A
 * line that is not a JSON object is refused rather than passed through: the
 * recorder records a notice deliberately (`recordLine` in `index.ts`), and a line
 * this function cannot parse is one nobody decided about. */
function redactFeedLine(line: string, literals: readonly LiteralRule[]): string {
  const decoded = decodeFeedLine(line)
  if (decoded.kind !== 'document') {
    throw new RedactionRefusal(
      `a feed line that is not a JSON object cannot be redacted: ${JSON.stringify(line.slice(0, 200))}`,
    )
  }
  return JSON.stringify(redactDocument(decoded.document, literals, 'a feed document'))
}

// ---------------------------------------------------------------------------
// The audit.
// ---------------------------------------------------------------------------

/**
 * auditProblems reports every redaction class still present in a value, with the
 * path it was found at. It is the second half of rule 3 and it is not a
 * formality: `redactDocument` calls it on its own output, and `loadFixture`
 * calls it on every document it loads, so a fixture that was edited after it was
 * written -- or one written by a redactor with a bug -- is refused by name rather
 * than replayed into a screen.
 *
 * The audit is stricter than "does redaction find anything": a **reserved key**
 * must carry a marker, so `{"project": "<project>"}` is clean and
 * `{"project": "fugaro"}` is a problem even though no pattern class would have
 * matched `fugaro`. That is what makes the marker a closed set rather than a
 * convention.
 */
function auditProblems(
  value: unknown,
  literals: readonly LiteralRule[],
  where = 'a document',
  trail = '',
): string[] {
  const problems: string[] = []
  if (typeof value === 'string') {
    for (const rule of literals) {
      if (hits(rule.re, value)) {
        problems.push(`${at(where, trail)} still carries one of the installation's own literals`)
      }
    }
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
      // replace.** Renaming a key would change the document's shape -- `repo`,
      // `title`, `day` and `burn` are names the console reads -- so a key that
      // carries identity is not redacted, it is a *refusal*. Without this, an
      // address, a repository path or the operator's checkout could sit in the
      // key position of a document that redacts to itself and audits clean:
      // `{ "acme/secret-repo": { … } }`, `{ "/Users/ana/src/fugaro": 1 }`.
      problems.push(...auditKey(key, literals, where, trail))
      const cls = classFor(key, parentKeyOf(trail))
      if (typeof child === 'string' && cls !== undefined && !matchesClass(child, cls)) {
        problems.push(
          `${at(where, `${trail}.${key}`)} holds ${JSON.stringify(child.slice(0, 80))}, which is not this class's marker`,
        )
      }
      problems.push(...auditProblems(child, literals, where, `${trail}.${key}`))
    }
    return dedupe(problems)
  }
  return problems
}

/** auditKey reports whether a *key* carries identity. It is the same rules as a
 * value, plus the installation's own literals -- a key is the one string
 * redaction cannot rename, so a key that names the operator's project, checkout
 * or repository is a refusal rather than something to replace. */
function auditKey(key: string, literals: readonly LiteralRule[], where: string, trail: string): string[] {
  const problems: string[] = []
  for (const rule of literals) {
    if (hits(rule.re, key)) {
      problems.push(`${at(where, trail)} is a key that carries one of the installation's own literals`)
      break
    }
  }
  problems.push(...auditString(key, where, trail, true))
  return dedupe(problems)
}

/** auditString reports the pattern classes a single string still carries.
 * `asKey` changes only the wording: a key does not *hold* a class, it *is* one,
 * and a refusal that says which it was is a refusal a reader can act on. */
function auditString(value: string, where: string, trail: string, asKey = false): string[] {
  if (value === '' || isMarker(value)) return []
  const say = (what: string): string =>
    asKey ? `${at(where, trail)} is a key that carries ${what}` : `${at(where, trail)} still holds ${what}`
  const problems: string[] = []
  if (hits(URL_RE, value)) problems.push(say('a URL'))
  for (const re of TOKEN_RES) {
    if (hits(re, value)) {
      problems.push(say('something credential-shaped'))
      break
    }
  }
  for (const match of value.matchAll(ADDRESS_RE)) {
    if (!isAddressMarker(match[0])) problems.push(say('an address'))
  }
  if (hits(ABS_PATH_RE, value) || hits(WIN_PATH_RE, value)) problems.push(say('a filesystem path'))
  if (hits(BRANCH_RE, value)) problems.push(say('a branch name'))
  // A pseudonym is a repository *path* by grammar and the marker by intent, so
  // only the matches that are not already pseudonyms are a problem.
  for (const match of value.matchAll(REPO_TOKEN_RE)) {
    if (!REPO_PSEUDONYM_RE.test(match[0])) problems.push(say('a repository path'))
  }
  return dedupe(problems)
}

/** matchesClass answers whether a reserved key's value carries its class's
 * marker (or, for the two pseudonym classes, its pseudonym's grammar). An empty
 * value passes: `""` is absence, and absence is not identity. */
function matchesClass(value: string, cls: FieldClass): boolean {
  if (value === '') return true
  switch (cls) {
    case 'repository':
      return REPO_PSEUDONYM_RE.test(value)
    case 'slug':
      return SLUG_PSEUDONYM_RE.test(value)
    case 'person':
      return PERSON_PSEUDONYM_RE.test(value) || isAddressMarker(value)
    case 'project':
      return value === PROJECT_MARKER
    case 'host':
      return value === HOST_MARKER
    case 'path':
      return value === PATH_MARKER
    case 'url':
      return value === URL_MARKER
    case 'token':
      return value === TOKEN_MARKER
  }
}

/** at renders the place a problem was found, so the refusal names it. */
function at(where: string, trail: string): string {
  const path = trail.replace(/^\./, '')
  return `${where}${path === '' ? '' : ` at ${path}`}`
}

/** parentKeyOf returns the last key of a trail, which is what tells a `kill`
 * object's `by` from a report dimension's `by`. **Array indices are stripped
 * first**, and that is what makes the answer the same in both shapes of the
 * documents: `{ kill: { by: … } }` walks the object at trail `.kill`, and
 * `{ kill: [{ by: … }] }` walks the object at trail `.kill[0]`, so a version
 * that read the parent off the raw trail returned `kill[0]` -- a key no class
 * table holds -- and the person class went unchecked in the array form. The
 * value was still pseudonymised; the *audit* was the half that was blind. */
function parentKeyOf(trail: string): string | null {
  const trimmed = trail.replace(/(?:\[\d+\])+$/, '')
  const index = trimmed.lastIndexOf('.')
  return index < 0 ? null : trimmed.slice(index + 1)
}

function dedupe(values: readonly string[]): string[] {
  return [...new Set(values)]
}

/** auditRedacted checks a value the way a loader can: with no literals in hand,
 * because a loader is not told the installation's names -- the marker is what it
 * can check. */
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
