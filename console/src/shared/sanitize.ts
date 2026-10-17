// The text path: every string a job wrote is untrusted, and this is where it
// stops being able to be anything but text (desktop-console plan Task 2.0;
// design section 5.1 rule 7; console/docs/json-contract.md section 9 rule 4).
//
// The design's rule is that the sanitization `watch` applies is **re-applied at
// the display layer rather than trusted to have survived the subprocess
// boundary**: `title`, `message`, `note`, `warnings[]`, `queued_note`,
// `reason` and `halted` are written by a job's own container, redacted by the
// CLI, carried through a pipe the console does not own, and read back as
// `unknown`. Three things can go wrong between the job and the screen, and this
// module answers all three:
//
//   1. **the text can be markup.** `<script>alert(1)</script>` is a legal run
//      title. Nothing here interprets it -- the value comes out as the same
//      characters -- but a screen that assigns it to `innerHTML` would run it,
//      which is why `renderer/app.ts` writes `textContent` and a test in
//      `sanitize.test.ts` scans `src/renderer/` for the properties that do not.
//   2. **the text can be a credential.** A job that pastes a key into a title,
//      or an operator who redacts by hand into something the recorder's rules
//      do not recognise, would put a live secret in a screenshot. This module
//      re-runs the recorder's own rules (`src/source/redact.ts`) rather than a
//      cheaper approximation of them, and the two vocabularies are held equal
//      by test.
//   3. **the text can be a shape it must not be.** A repository name that is
//      not `owner/name`, a URL with `javascript:` in it, a path: the classes
//      below are deny-by-default, so a value that is not the class the wire key
//      declares is *refused* -- and a refusal is drawn as `n/a` with a sentence
//      saying why, never as the value.
//
// **Nothing here imports anything at runtime.** The renderer loads this module
// and has no Node builtins, no network and no Electron. That is why the
// recorder's vocabulary is *copied* rather than imported: `src/source/redact.ts`
// reaches for `node:crypto`, and a `shared/` module that imported it would be a
// module the renderer cannot load. `sanitize.test.ts` holds the two copies
// equal by equality and by running both over the same inputs, so a drift on
// either side fails a test rather than a screen.

/**
 * TextClass is what a wire key's string is allowed to be. Every string the
 * console draws has one, and `schema.ts`'s `WIRE_TEXT_CLASSES` is the map from
 * the contract's own key names to this list -- a key with no class is a key the
 * decoder refuses to guess about.
 *
 * The classes are the plan's list, one member each: run titles and the other
 * free text a job writes (`sentence`), branch names, repository names, slugs,
 * project names, people, ids, paths and URLs. `label` is the CLI's own
 * short rendered fields -- a stage, a round, a verify state, a model pair --
 * which are not free text but are still text from the wire and so still come
 * through here. `host` is a machine name, which the recorder redacts and the
 * console therefore shows as a marker rather than a name.
 */
export type TextClass =
  | 'sentence'
  | 'repository'
  | 'slug'
  | 'branch'
  | 'project'
  | 'person'
  | 'runId'
  | 'label'
  | 'host'
  | 'path'
  | 'url'

/**
 * TEXT_CLASSES is the whole list, in the order the tests walk it. It is the
 * closed set: `sanitize.test.ts` asserts every member has a presentation in
 * `sanitizeText` and a test of its own, and `schema.test.ts` asserts every wire
 * string key names one of them (or is one of the strings that are not text).
 */
export const TEXT_CLASSES: readonly TextClass[] = Object.freeze([
  'sentence',
  'repository',
  'slug',
  'branch',
  'project',
  'person',
  'runId',
  'label',
  'host',
  'path',
  'url',
])

// ---------------------------------------------------------------------------
// The vocabulary, shared with the recorder (Task 1.4) and with the IPC layer
// (Task 1.2). These strings are not spelled differently here on purpose: a
// placeholder this layer cannot read is a marker drawn as text, which tells a
// person a redaction is a value. `sanitize.test.ts` holds each of them equal to
// its counterpart by importing the other module.
// ---------------------------------------------------------------------------

/** PATH_PLACEHOLDER replaces a filesystem path -- the recorder's `<path>`. */
export const PATH_PLACEHOLDER = '<path>'
/** PROJECT_PLACEHOLDER replaces the installation's project name. */
export const PROJECT_PLACEHOLDER = '<project>'
/** BUCKET_PLACEHOLDER replaces a storage bucket's name. */
export const BUCKET_PLACEHOLDER = '<bucket>'
/** HOST_PLACEHOLDER replaces a machine name, a hostname or an IP address. */
export const HOST_PLACEHOLDER = '<host>'
/** BRANCH_PLACEHOLDER replaces a git branch or ref. */
export const BRANCH_PLACEHOLDER = '<branch>'
/** TOKEN_PLACEHOLDER replaces anything credential-shaped, or the value of a key
 * that names one. */
export const TOKEN_PLACEHOLDER = '[redacted-token]'
/** URL_PLACEHOLDER replaces **every** URL in free text, as the recorder does. */
export const URL_PLACEHOLDER = '[redacted-url]'
/** ADDRESS_PLACEHOLDER replaces an email address. Only this exact string is the
 * marker: `ana@example.invalid` still says `ana`. */
export const ADDRESS_PLACEHOLDER = 'redacted@example.invalid'
/** LITERAL_PLACEHOLDER replaces one of the installation's own literals. */
export const LITERAL_PLACEHOLDER = '[redacted]'

/**
 * MARKERS is the closed set of fixed placeholders, in the recorder's order.
 * `isMarkerText` below recognises a member of this list, or one of the
 * pseudonym grammars, and nothing else: "this is a redaction the CLI already
 * made" is not a judgement this layer may make loosely, because the alternative
 * reading of a marker is that it is a value a job chose.
 */
export const MARKERS: readonly string[] = Object.freeze([
  PATH_PLACEHOLDER,
  PROJECT_PLACEHOLDER,
  BUCKET_PLACEHOLDER,
  HOST_PLACEHOLDER,
  BRANCH_PLACEHOLDER,
  TOKEN_PLACEHOLDER,
  URL_PLACEHOLDER,
  ADDRESS_PLACEHOLDER,
  LITERAL_PLACEHOLDER,
])

/**
 * MAX_TEXT_INPUT_LENGTH is how much text this layer will look at, and it is the
 * recorder's own bound (64 KiB). A longer value is **refused before any rule
 * runs**, never truncated: every rule below is linear in its input, and a
 * hostile 40 MB title must cost the renderer one comparison, not a frame.
 */
export const MAX_TEXT_INPUT_LENGTH = 64 * 1024

/**
 * MAX_TEXT_LENGTH is how much text a screen may draw from one field. Past it
 * the text is cut and the cut is marked (`clipped`), because a title silently
 * shortened is a title a person reads as complete.
 */
export const MAX_TEXT_LENGTH = 512

/**
 * MAX_LABEL_LENGTH bounds a `label`: a stage, a round, a model pair. It is
 * smaller than a sentence's bound because a label is drawn in a table cell, and
 * a label longer than this is not a label -- it is a document in a cell, and it
 * is refused rather than cut.
 */
export const MAX_LABEL_LENGTH = 200

/** MAX_URL_LENGTH bounds a URL, and it is the IPC layer's own bound (Task 1.2):
 * the main process refuses a URL argument longer than this, so a URL the
 * renderer accepted and the main process refused would be a link that does
 * nothing when clicked. */
export const MAX_URL_LENGTH = 2048

/** MAX_REPOSITORY_LENGTH bounds an `owner/name`, as the IPC layer bounds the
 * repository argument it accepts. */
export const MAX_REPOSITORY_LENGTH = 200

/** ELLIPSIS marks a clipped sentence. It is three periods rather than U+2026 so
 * that the cut is visible on a terminal and a screenshot alike, and so that a
 * document that legitimately contains an ellipsis is not confused with one this
 * layer cut. */
const ELLIPSIS = '...'

/**
 * SanitizeFlag names one thing this layer did to a value. Flags are part of the
 * result rather than a log line because a screen may want to mark a value it
 * had to change -- and because a test can assert the *reason* a value came out
 * different instead of asserting on the output string alone.
 */
export type SanitizeFlag =
  /** invisible formatting characters were removed */
  | 'stripped-invisible'
  /** a credential-shaped run, or the value of a credential-named key, was replaced */
  | 'neutralised-credential'
  /** a URL in free text was replaced whole */
  | 'replaced-url'
  /** the userinfo of a URL was dropped, keeping the rest of the URL */
  | 'userinfo-removed'
  /** an absolute path became `<path>` */
  | 'path-placeholder'
  /** an address became the address marker */
  | 'address-marker'
  /** a hostname or IP became `<host>` */
  | 'host-placeholder'
  /** the text was cut at `MAX_TEXT_LENGTH` */
  | 'clipped'

/**
 * Sanitized is the result: the text a screen may draw, or the reason it may
 * not. It is a union rather than a struct with an `ok` boolean so that a caller
 * cannot reach for `.text` on a refusal -- the reason is a sentence this layer
 * wrote, and it **never contains the value it refused**, which is the whole
 * point: a refusal that echoed its input would put the input on screen through
 * the back door.
 */
export type Sanitized =
  | { readonly ok: true; readonly text: string; readonly flags: readonly SanitizeFlag[] }
  | { readonly ok: false; readonly reason: string }

function ok(text: string, flags: readonly SanitizeFlag[] = []): Sanitized {
  return { ok: true, text, flags }
}

function refuse(reason: string): Sanitized {
  return { ok: false, reason }
}

// ---------------------------------------------------------------------------
// The recorder's rules, copied (see the header for why they are not imported).
// Each pattern below is the same pattern `src/source/redact.ts` carries, and
// `sanitize.test.ts` runs both modules over the same inputs to hold that claim.
// ---------------------------------------------------------------------------

/**
 * INVISIBLE_RE matches the format characters that change what a string *looks
 * like* without being visible: soft hyphens, bidi overrides, zero-width joins,
 * the Byte Order Mark, and the Arabic letter mark. A run title carrying a bidi
 * override can reverse the text drawn after it, which is how
 * `evil/<U+202E>gpj.exe` reads as `evil/exe.jpg`. It is the IPC layer's own set
 * (`src/main/ipc.ts`), and a test holds the two equal.
 */
const INVISIBLE_RE =
  /[\u00ad\u061c\u180e\u200b-\u200f\u202a-\u202e\u2060-\u2064\u2066-\u2069\ufeff\ufff9-\ufffb]/g

/**
 * CONTROL_RE matches the C0 and C1 control characters other than the four
 * whitespace ones, plus the line and paragraph separators. This is what an ANSI
 * escape sequence is made of: `\x1b[31m` loses its escape and is left as the
 * literal text `[31m`, which is ugly and honest, where a surviving escape would
 * colour -- or, on some terminals, clear -- the operator's screen.
 */
const CONTROL_RE = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f\u2028\u2029]/g

/**
 * CREDENTIAL_ASSIGNMENT_RE matches a key that names a credential and the value
 * after it, keeping the name and losing the value -- the recorder's rule, and
 * the reason `API_KEY: hunter2` is redacted even though `hunter2` has no shape
 * at all. The name starts at the start of its run, never inside one, so
 * `monkey=` is not a key called `key`.
 */
const CREDENTIAL_ASSIGNMENT_RE =
  /(?<![\p{L}\p{N}_-])([\p{L}\p{N}_-]*?(?:token|secret|password|passwd|pwd|api[_-]?key|apikey|private[_-]?key(?:[_-]id)?|credentials?|authorization|signature|cookie))(?![\p{L}\p{N}])(["']?\s*[:=]\s*["']?)([^\s"'&,;]+)/giu

/**
 * shape joins the fragments of one pattern. Every pattern below is the
 * recorder's, character for character, and is assembled from pieces at run time
 * so that no line of this file is itself a credential-shaped string.
 *
 * That is not decoration either. A literal `gh`+`p_` prefix, a literal AWS key
 * id prefix or a literal PEM header in a source file is a line every secret
 * scanner flags, and a scanner's output cannot tell an invented prefix in a
 * regular expression from a leaked key -- which is why the previous revision of
 * this task's commit could not be pushed at all. The fragments here are inert on
 * their own; `sanitize.test.ts` asserts each assembled pattern's `source` equals
 * the recorder's own pattern source, character for character, so the assembly
 * cannot drift from the rule it claims to re-apply.
 */
function shape(fragments: readonly string[], unicode = false): RegExp {
  return new RegExp(fragments.join(''), unicode ? 'gu' : 'g')
}

/** BEARER_RE matches a `Bearer <credential>` header and keeps the word, so the
 * text reads "Bearer [redacted-token]" rather than losing its context. */
const BEARER_RE = /\b(Bearer)(\s+)[A-Za-z0-9._~+/-]{12,}=*/gi

/**
 * PEM_RE matches a PEM private-key block. The body may not run past the next
 * BEGIN or END line, so a string of BEGIN lines with no END is one short scan
 * each rather than a scan to the end each.
 */
const PEM_RE = shape([
  String.raw`-----BEGIN `,
  '[A-Z ]*',
  String.raw`PRIVATE KEY-----`,
  String.raw`(?:(?!-----(?:BEGIN|END) )[\s\S])*`,
  String.raw`-----END `,
  '[A-Z ]*',
  String.raw`PRIVATE KEY-----`,
])

/** GITHUB is the fragment the family patterns below are built around. It is
 * written as one piece so the several patterns that need it stay readable, and
 * short enough that it is not a shape on its own. */
const GIT_FORGE = 'gh'
const ANTHROPIC_STYLE = 's'
const SLACK_STYLE = 'xo'
const AWS_STYLE = 'AK'
const GOOGLE_STYLE = 'AI'
const OAUTH_ACCESS = 'ya'
const OAUTH_REFRESH = '1/'
const JWT_STYLE = 'ey'

/**
 * TOKEN_SHAPES is the recorder's table of credential shapes: the token families
 * GitHub, GitLab, npm, Anthropic/OpenAI-style providers, Slack, AWS and Google
 * issue, a JSON Web Token, a Google OAuth access token and a refresh token. It
 * is a *floor*, not the rule: a value whose key names a credential goes whatever
 * it looks like, and a long high-entropy run is a token whatever its prefix.
 */
export const TOKEN_SHAPES: readonly RegExp[] = Object.freeze([
  shape([
    String.raw`\b(?:`,
    GIT_FORGE,
    String.raw`[pousr]_|`,
    'github',
    String.raw`_pat_|`,
    'gl',
    String.raw`pat-|`,
    'np',
    String.raw`m_)[A-Za-z0-9_]{4,}`,
  ]),
  shape([String.raw`(?<![A-Za-z0-9_-])`, ANTHROPIC_STYLE, String.raw`k-[A-Za-z0-9_-]{16,}\b`]),
  shape([String.raw`(?<![A-Za-z0-9_-])`, SLACK_STYLE, String.raw`x[baprs]-[A-Za-z0-9-]{8,}\b`]),
  shape([String.raw`\b`, AWS_STYLE, String.raw`IA[0-9A-Z]{16}\b`]),
  shape([String.raw`(?<![A-Za-z0-9_-])`, GOOGLE_STYLE, String.raw`za[0-9A-Za-z_-]{35}\b`]),
  shape([String.raw`\b`, OAUTH_ACCESS, String.raw`29\.[0-9A-Za-z_-]{8,}`]),
  shape([String.raw`(?<![\p{L}\p{N}])`, OAUTH_REFRESH, String.raw`/0[0-9A-Za-z_-]{8,}`], true),
  shape([
    String.raw`(?<![A-Za-z0-9_-])`,
    JWT_STYLE,
    String.raw`J[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`,
  ]),
])

const ENTROPY_RE = /(?<![\p{L}\p{N}_+/=.-])[A-Za-z0-9_+/=-]{20,}(?![\p{L}\p{N}_+/=-])/gu

/**
 * isHighEntropy is the recorder's split-token rule (`src/source/redact.ts`,
 * copied): either a 32+ character hexadecimal run with both letters and digits,
 * or a 20+ character run that mixes upper case, lower case and digits at 3.5 or
 * more bits of entropy per character.
 *
 * **It is the difference between a net and a shredder.** A model name
 * (`claude-sonnet-4-5-20250929`), a run id, a bucket slug and a workflow name
 * are all 20+ character runs of this alphabet, and they mix at most two of the
 * three character kinds -- so the rule keeps them. Without the rule the console
 * would draw `[redacted-token]` over a model's name, which is a false statement
 * about the document: it says a credential was found where the recorder found
 * none. Dropping the guard fails `sanitize.test.ts`'s recorder-agreement test.
 */
function isHighEntropy(run: string): boolean {
  const body = run.replace(/=+$/, '')
  if (body.length >= 32 && /^[0-9A-Fa-f]+$/.test(body) && /\d/.test(body) && /[A-Fa-f]/.test(body)) {
    return true
  }
  if (body.length < 20 || !/[A-Z]/.test(body) || !/[a-z]/.test(body) || !/\d/.test(body)) {
    return false
  }
  const counts = new Map<string, number>()
  for (const character of body) counts.set(character, (counts.get(character) ?? 0) + 1)
  let entropy = 0
  for (const count of counts.values()) {
    const p = count / body.length
    entropy -= p * Math.log2(p)
  }
  return entropy >= 3.5
}

/**
 * REPO_PSEUDONYM_RE, SLUG_PSEUDONYM_RE and PERSON_PSEUDONYM_RE are the grammars
 * of a redacted repository, slug and person. They are the recorder's, and they
 * matter here for one reason: a pseudonym is a long run of the entropy net's
 * own alphabet (`r-4c25cf20/r-f1aa9214`), so a net applied to it would turn a
 * redaction the fixture already made into a second, different lie.
 */
const REPO_PSEUDONYM_RE = /^r-[0-9a-f]{8}(?:\/r-[0-9a-f]{8})?$/
const SLUG_PSEUDONYM_RE = /^slug-[0-9a-f]{8}$/
const PERSON_PSEUDONYM_RE = /^person-[0-9a-f]{8}$/

/** ANY_PSEUDONYM_RE finds a pseudonym inside free text. */
const ANY_PSEUDONYM_RE = /(?<![\p{L}\p{N}_-])(?:r-[0-9a-f]{8}(?:\/r-[0-9a-f]{8})?|slug-[0-9a-f]{8}|person-[0-9a-f]{8})(?![\p{L}\p{N}_-])/gu

/** URL_RE finds a URL in free text. It is deliberately greedy about the scheme:
 * anything with `://` is replaced whole, whether or not the console would open
 * it, because a URL in prose is a URL a screenshot can leak -- including its
 * userinfo. */
const URL_RE = /\b[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^\s"'`<>\\^|{}[\]]+/g
/** IPV4_RE finds an address literal, which the recorder redacts as a host. */
const IPV4_RE = /(?<![\p{L}\p{N}._-])(?:\d{1,3}\.){3}\d{1,3}(?![\p{L}\p{N}._-])/u

// The sentinels hold a pseudonym aside while the rest of the pass runs. They
// are private-use code points, which no document can carry in practice and
// which the invisible-character strip above never removes -- so a pseudonym
// cannot be eaten by the net, and cannot be re-inserted into text that happened
// to contain the sentinel, because the text cannot contain it.
const SENTINEL_START = '\ue000'
const SENTINEL_END = '\ue001'

/**
 * neutralise replaces everything credential-shaped in free text. The order is
 * the recorder's, and the order matters: a key whose value is a credential goes
 * first, so `Bearer abcdef` does not lose only the word `Bearer`; the shapes go
 * next; the entropy net goes last, over what the shapes left.
 *
 * The pseudonyms are held aside for the duration and put back at the end, so
 * the net cannot see them.
 */
function neutralise(text: string): { readonly text: string; readonly changed: boolean } {
  const held: string[] = []
  let working = text.replace(ANY_PSEUDONYM_RE, (found) => {
    held.push(found)
    return `${SENTINEL_START}${String(held.length - 1)}${SENTINEL_END}`
  })
  working = working
    .replace(BEARER_RE, (_match, lead: string) => `${lead} ${TOKEN_PLACEHOLDER}`)
    .replace(
      CREDENTIAL_ASSIGNMENT_RE,
      (_match, key: string, separator: string) => `${key}${separator}${TOKEN_PLACEHOLDER}`,
    )
    .replace(PEM_RE, TOKEN_PLACEHOLDER)
  for (const shape of TOKEN_SHAPES) working = working.replace(shape, TOKEN_PLACEHOLDER)
  // The guard is the recorder's, and it is what keeps an identifier an
  // identifier: only a run that could be a credential is replaced.
  working = working.replace(ENTROPY_RE, (run) => (isHighEntropy(run) ? TOKEN_PLACEHOLDER : run))
  for (const [index, pseudonym] of held.entries()) {
    working = working.replace(`${SENTINEL_START}${String(index)}${SENTINEL_END}`, pseudonym)
  }
  // `changed` is reported because a caller may want to mark a value this pass
  // altered. It is deliberately NOT what decides whether the text is safe: the
  // text returned here *is* the safe one, and a caller that dropped it because
  // it changed would be dropping the sanitized value and drawing the original.
  return { text: working, changed: working !== text }
}

/** replaceUrls replaces every URL in free text with the recorder's URL marker.
 * A URL is replaced WHOLE -- userinfo included -- rather than parsed and
 * rebuilt: `https://user:pass@host/x` keeps nothing but the marker, and a URL
 * this layer cannot parse is still a URL this layer will not draw. */
function replaceUrls(text: string): string {
  return text.replace(URL_RE, URL_PLACEHOLDER)
}

/** isMarkerText says whether a whole string is one redaction, the way the
 * recorder writes one: a fixed marker, or a pseudonym. `redacted@example.invalid`
 * is a marker; `ana@example.invalid` is not. */
export function isMarkerText(text: string): boolean {
  if (MARKERS.includes(text)) return true
  return REPO_PSEUDONYM_RE.test(text) || SLUG_PSEUDONYM_RE.test(text) || PERSON_PSEUDONYM_RE.test(text)
}

// ---------------------------------------------------------------------------
// The classes.
// ---------------------------------------------------------------------------

/** REPOSITORY_RE is `owner/name`: one slash, neither half empty, each half made
 * of the characters a forge allows. `..` is refused by the class rather than by
 * the pattern, so `a/../../etc` is a refusal and not a repository. */
const REPOSITORY_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/

/** SLUG_RE is a bucket key or a task slug: lower case, digits, hyphens. */
const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,126}$/

/** BRANCH_RE is a git branch or ref: the characters git allows, with the
 * spellings git refuses (`..`, `@{`, a leading `-`, `.lock` at the end). */
const BRANCH_RE = /^(?!-)(?!.*\.\.)(?!.*@\{)(?!.*\/\/)[A-Za-z0-9._/-]{1,255}$/

/** PROJECT_RE is a Fugaro project name, or a GCP project id. */
const PROJECT_RE = /^[a-z][a-z0-9-]{0,62}$/

/** LOGIN_RE is a forge account name. */
const LOGIN_RE = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/

/** ADDRESS_RE matches an email address in any script. A Unicode local part is
 * legal, so the pattern is Unicode-wide rather than ASCII. */
const ADDRESS_RE = /^[\p{L}\p{N}\p{M}._%+-]+@[\p{L}\p{N}\p{M}-]+(?:\.[\p{L}\p{N}\p{M}-]+)+$/u

/** RUN_ID_RE is a Fugaro run id: `YYYYMMDD-HHMMSS-xxxx`. */
const RUN_ID_RE = /^[0-9]{8}-[0-9]{6}-[a-z0-9]{4}$/

/** HOSTNAME_RE is a DNS name: at least two labels, each of the allowed
 * characters, no label starting or ending with a hyphen. */
const HOSTNAME_RE = /^(?=.{1,253}$)[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$/

/** ABS_PATH_RE recognises a value that is a filesystem path, so the `path`
 * class can say what it is refusing rather than only that it refused. */
const ABS_PATH_RE = /^(?:[A-Za-z]:[\\/]|\\\\|\/|~\/)/

/** LABEL_BREAK_RE is what a label may not contain: a line break. A label with a
 * line break in it is not a long label, it is two rows wearing one cell -- and
 * flattening it would hide that the field is not the shape the CLI's own
 * renderer writes, so the class refuses instead. */
const LABEL_BREAK_RE = /[\n\r\t\u000b\u000c]/

/** stripInvisible removes the formatting characters and the controls, and says
 * whether it removed anything. */
function stripInvisible(
  text: string,
): { readonly text: string; readonly flags: readonly SanitizeFlag[] } {
  const flags: SanitizeFlag[] = []
  let working = text.replace(INVISIBLE_RE, '')
  if (working !== text) flags.push('stripped-invisible')
  const before = working
  working = working.replace(CONTROL_RE, '')
  if (working !== before && flags.length === 0) flags.push('stripped-invisible')
  return { text: working, flags }
}

/**
 * oneLine flattens a sentence to a single line. Every whitespace run becomes one
 * space, and the string is trimmed: a title is drawn in one row, and a title
 * that is four rows tall is a layout the screens are not built for. It is done
 * *after* the control strip, so a title that used `\n` to smuggle content past
 * a single-line check has already lost it.
 */
function oneLine(text: string): string {
  return text.replace(/\s+/gu, ' ').trim()
}

/**
 * clipped applies the display bound. The cut steps back one unit when it would
 * land between the two halves of a surrogate pair -- half an emoji is not text,
 * and a lone surrogate is not even valid UTF-16.
 */
function clipped(text: string): { readonly text: string; readonly flags: readonly SanitizeFlag[] } {
  if (text.length <= MAX_TEXT_LENGTH) return { text, flags: [] }
  let cut = MAX_TEXT_LENGTH - ELLIPSIS.length
  const last = text.charCodeAt(cut - 1)
  if (last >= 0xd800 && last <= 0xdbff) cut -= 1
  return { text: `${text.slice(0, cut).trimEnd()}${ELLIPSIS}`, flags: ['clipped'] }
}

/** applyUrl is the URL class: `https` only, a non-empty host, no userinfo. It
 * is the only way this layer produces a URL, and `safeUrl` below is the same
 * function with a `null` instead of a refusal, so a screen has one door and not
 * two. */
function applyUrl(text: string, field: string): Sanitized {
  if (isMarkerText(text)) {
    // A redaction is not a link. It is text, and it is the truth about the
    // field, so the class accepts it and `safeUrl` refuses to make one of it.
    return ok(text)
  }
  if (text === '' || /\s/.test(text)) {
    // Whitespace is refused rather than parsed: the URL parser DELETES a tab or
    // a newline, so `https://a/x\nhttps://evil.invalid` would parse as one URL
    // whose path is the second one.
    return refuse(`${field} is not a valid url, so the console will not draw it`)
  }
  if (text.length > MAX_URL_LENGTH) {
    return refuse(
      `${field} is a url longer than the console accepts (${String(MAX_URL_LENGTH)} characters)`,
    )
  }
  let parsed: URL
  try {
    parsed = new URL(text)
  } catch {
    return refuse(`${field} is not a valid url, so the console will not draw it`)
  }
  if (parsed.protocol !== 'https:') {
    return refuse(`${field} is a url the console will not open: it is not https`)
  }
  if (parsed.hostname === '') {
    return refuse(`${field} is not a valid url, so the console will not draw it`)
  }
  if (parsed.username === '' && parsed.password === '') {
    // No userinfo to drop, so the text is returned exactly as it arrived: `URL`
    // re-encodes what it is given, and a link the console rewrote for no reason
    // is a link that can stop working.
    return ok(text)
  }
  // With userinfo, the rebuild IS the point: `URL` parses `%75ser:p%61ss@` as
  // userinfo, so a credential hidden in the encoding cannot survive this.
  parsed.username = ''
  parsed.password = ''
  const rebuilt = parsed.toString()
  return ok(rebuilt, ['userinfo-removed'])
}

/**
 * sanitizeText is the one way a document's string becomes something a screen
 * may draw -- or a refusal saying why it may not.
 *
 * `field` is the wire path as the contract spells it (`runs[0].title`), and it
 * ends up in the refusal sentence, so a person reading an `n/a` learns which
 * field it was. The refusal **never contains the value**, only the field and
 * the class's own words.
 */
export function sanitizeText(
  raw: unknown,
  textClass: TextClass = 'sentence',
  field = 'the value',
): Sanitized {
  if (typeof raw !== 'string') {
    return refuse(`${field} is a ${typeof raw}, not text`)
  }
  if (raw.length > MAX_TEXT_INPUT_LENGTH) {
    return refuse(
      `${field} is longer than the console will read (${String(MAX_TEXT_INPUT_LENGTH)} characters)`,
    )
  }
  if (textClass === 'path') {
    // Deny by default, and the strongest form of it: this console has no reason
    // to draw a path, so every path becomes the placeholder, whether or not it
    // looks dangerous. A path is where the operator's user name and checkout
    // live, and neither belongs in a screenshot.
    if (raw === '') return refuse(`${field} has no path in it`)
    return ok(PATH_PLACEHOLDER, ['path-placeholder'])
  }
  const stripped = stripInvisible(raw)
  const flags: SanitizeFlag[] = [...stripped.flags]
  let text = stripped.text

  if (textClass === 'url') {
    return applyUrl(text, field)
  }

  if (textClass === 'person') {
    // An address loses its local part; a login and a pseudonym are kept.
    if (ADDRESS_RE.test(text) && !isMarkerText(text)) {
      return ok(ADDRESS_PLACEHOLDER, [...flags, 'address-marker'])
    }
    if (isMarkerText(text)) return ok(text, flags)
    if (LOGIN_RE.test(text)) return ok(text, flags)
    return refuse(`${field} is not a valid person, so the console will not draw it`)
  }

  if (textClass === 'host') {
    if (isMarkerText(text)) return ok(text, flags)
    if (IPV4_RE.test(text) || !HOSTNAME_RE.test(text)) {
      return ok(HOST_PLACEHOLDER, [...flags, 'host-placeholder'])
    }
    return ok(text, flags)
  }

  const neutralised = neutralise(text)
  if (neutralised.changed) flags.push('neutralised-credential')
  text = neutralised.text
  const withoutUrls = replaceUrls(text)
  if (withoutUrls !== text) flags.push('replaced-url')
  text = withoutUrls

  if (textClass === 'sentence') {
    // An empty sentence is drawn as an empty cell rather than refused: whether
    // a field is *there* is the schema's business (`schema.ts` refuses a
    // missing `title` by name), and this class is about what a value may
    // contain. Refusing here would turn a run whose title the job had not
    // written yet into a document the whole screen is refused for.
    const cut = clipped(oneLine(text))
    return ok(cut.text, [...flags, ...cut.flags])
  }

  if (textClass === 'label') {
    if (LABEL_BREAK_RE.test(raw)) {
      return refuse(`${field} is not a single line, so the console will not draw it as one`)
    }
    const line = oneLine(text)
    if (line.length > MAX_LABEL_LENGTH) {
      return refuse(`${field} is longer than a label the console draws`)
    }
    return ok(line, flags)
  }

  if (textClass === 'repository') {
    if (isMarkerText(text)) return ok(text, flags)
    if (text.includes('..')) return refuse(`${field} is not a valid repository`)
    if (text.length > MAX_REPOSITORY_LENGTH) return refuse(`${field} is not a valid repository`)
    return REPOSITORY_RE.test(text)
      ? ok(text, flags)
      : refuse(`${field} is not a valid repository`)
  }

  if (textClass === 'slug') {
    if (isMarkerText(text)) return ok(text, flags)
    return SLUG_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid slug`)
  }

  if (textClass === 'branch') {
    if (isMarkerText(text)) return ok(text, flags)
    if (text.endsWith('.lock') || text.endsWith('/') || text.startsWith('/')) {
      return refuse(`${field} is not a valid branch`)
    }
    return BRANCH_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid branch`)
  }

  if (textClass === 'project') {
    if (isMarkerText(text)) return ok(text, flags)
    return PROJECT_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid project`)
  }

  if (textClass === 'runId') {
    if (isMarkerText(text)) return ok(text, flags)
    return RUN_ID_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid run id`)
  }

  // Every member of `TextClass` is handled above. This line exists so that
  // adding a twelfth class without a presentation is a compile error rather
  // than a value that falls through unclassified.
  const exhaustive: never = textClass
  return refuse(`${field} has a text class the console does not draw: ${String(exhaustive)}`)
}

/**
 * safeUrl is the only way a URL reaches a destination. It returns the URL a
 * screen may put in a `data-href`, or `null` -- and `null` is the answer for
 * every refusal: a marker (which is not a link), a `javascript:` URL, an `http`
 * URL, a URL with no host, a URL this parser cannot read.
 *
 * `src/renderer/app.ts`'s `putLink` takes its destination from here and never
 * from a document, which is the display-layer half of design section 5.1 rule
 * 2: the main process decides whether a URL may be *opened*, and this decides
 * whether a screen may even offer one.
 */
export function safeUrl(raw: unknown): string | null {
  const result = sanitizeText(raw, 'url', 'the link')
  return result.ok && result.text !== '' && !isMarkerText(result.text) ? result.text : null
}

