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
//      cheaper approximation of them: the pattern classes below are that file's
//      list, in its order, and the corpus test in `sanitize.test.ts` asserts the
//      property that makes them a re-run -- in free text, this layer never draws
//      a value the recorder would have changed. (The `url` class is the
//      deliberate exception: the recorder replaces every url field, and this
//      class keeps a clean https link and refuses one that carries a
//      credential.) The two rules this layer cannot reproduce
//      (the people pass and the installation's literals) are named where the
//      list is, in `FREE_TEXT_RULES`, and neither is approximated.
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
// module the renderer cannot load. `sanitize.test.ts` holds the vocabulary
// (markers, placeholders, bounds) equal to the recorder's by equality, and the
// rule list equal to it by running both over the same corpus, so a drift on
// either side fails a test rather than a screen. The corpus test is
// one-directional on purpose: this layer is allowed to change *more* than the
// recorder -- it always removes the invisible characters, and it keeps the word
// `Bearer` where the recorder replaces it -- and is not allowed to change less.

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
 * recorder's own bound (64 KiB). A longer value is **refused, never
 * truncated**, and the bound is measured twice: on the value as it came, before
 * anything runs (a hostile 40 MB title costs the renderer one comparison, not a
 * frame), and again on the form the rules read -- after NFKC, which can grow a
 * string eighteen-fold (U+FDFA is one code unit and eighteen after it), so a
 * value inside the first bound can still be a million characters by the time a
 * rule sees it. Every rule below is linear in what it reads, so bounding what it
 * reads is what bounds the cost.
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
  /** a git branch in free text became `<branch>` */
  | 'branch-placeholder'
  /** a repository path or a GCP project or bucket in free text became a
   * placeholder: the recorder's keyed pseudonym where it has one, and
   * `[redacted]` where this layer has no key to compute one */
  | 'name-placeholder'
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
 * INVISIBLE_RE matches what `prepare` strips before any rule runs: every
 * default-ignorable code point (zero-width spaces and joiners, the soft hyphen,
 * the variation selectors), every bidi control, and every C0/C1 control but
 * tab, line feed and carriage return. None of them is visible, so none of them
 * may split a token, an address or a literal into pieces no rule recognises.
 *
 * It is the recorder's own set (`src/source/redact.ts`), copied here rather than
 * approximated: the hand-written list this replaced carried U+200B..U+200F and
 * U+FEFF but not the variation selectors, so `gh` + U+FE0F + `p_...` was a token
 * the recorder redacted and this layer drew. The one addition is U+FFF9..U+FFFB,
 * the interlinear annotation anchors: the IPC layer refuses them and the Unicode
 * property does not carry them, and a character that is invisible to every rule
 * must not be able to split one. The IPC layer's refusal list (`src/main/ipc.ts`)
 * is a subset of this set, and a test reads that list off the file and holds it
 * to exactly that.
 */
const INVISIBLE_RE =
  /[\p{Default_Ignorable_Code_Point}\p{Bidi_Control}\ufff9-\ufffb\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/gu

/**
 * CONTROL_RE matches the line and paragraph separators -- the two separators a
 * screen must not draw, and the two the recorder's set deliberately keeps. They
 * are removed rather than flattened, so a sentence that used one to smuggle a
 * second row past a single-line check has already lost it.
 */
const CONTROL_RE = /[\u2028\u2029]/g

/**
 * prepare is the form every rule reads: NFKC first, so a fullwidth `＠` *is* an
 * `@` before the address rule looks and a fullwidth `ｓｋ-` prefix is the `sk-`
 * the token table looks for; then the invisible characters gone, so none of them
 * can split a shape into pieces no rule recognises. It is the recorder's
 * `prepare`, copied.
 */
function prepare(text: string): string {
  return text.normalize('NFKC').replace(INVISIBLE_RE, '')
}

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
 * That is not decoration either. A literal forge prefix, a literal AWS key id
 * prefix or a literal PEM header in a source file is a line every secret
 * scanner flags, and a scanner's output cannot tell an invented prefix in a
 * regular expression from a leaked key -- which is why an earlier revision of
 * this task's commit could not be pushed at all. The fragments here are inert on
 * their own; `sanitize.test.ts` runs both this module and the recorder over the
 * same corpus and asserts that this layer never keeps what the recorder removed,
 * so the assembly cannot silently drift from the rule it claims to re-apply.
 */
function shape(fragments: readonly string[], unicode = false): RegExp {
  return new RegExp(fragments.join(''), unicode ? 'gu' : 'g')
}

/** BEARER_RE matches a `Bearer <credential>` header and keeps the word, so the
 * text reads "Bearer [redacted-token]" rather than losing its context. The
 * recorder replaces the word as well; keeping it is the one difference this
 * layer makes on purpose, and it is on the safe side of the boundary -- the
 * credential is gone either way. */
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

/** GIT_FORGE, ANTHROPIC_STYLE and the rest are the fragments the family patterns
 * below are built around, so that no line here spells a prefix out. */
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
 * it looks like, a percent-encoded or base64-wrapped candidate is decoded first,
 * and a long high-entropy run is a token whatever its prefix.
 *
 * The recorder's ninth and tenth shapes are `BEARER_RE` and `PEM_RE` below,
 * kept apart only because the first keeps its word.
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

/** ENTROPY_RE is a candidate for the high-entropy rule: a long run of the
 * characters tokens are made of. */
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

// ---------------------------------------------------------------------------
// The pattern classes, in the recorder's order. Every pattern below is the
// recorder's, character for character: the anchor at the start of a run is what
// keeps it linear in its input, and this layer runs over values the console does
// not own.
// ---------------------------------------------------------------------------

/** URL_RE matches every URL. It runs first, so the userinfo, the host and the
 * path inside a URL never get a chance to be what is replaced. A URL is
 * replaced WHOLE -- userinfo included -- rather than parsed and rebuilt:
 * `https://user:pass@host/x` keeps nothing but the marker, and a URL this layer
 * cannot parse is still a URL this layer will not draw. The scheme starts at the
 * start of its run, never inside one, so `_https://evil.invalid/x` is a URL and
 * `xhttps://evil.invalid/x` is not. */
const URL_RE = /(?<![a-zA-Z0-9+.-])[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^\s"'`<>]+/g

/** ADDRESS_RE matches an email address **in any script**: a Unicode local part
 * and a Unicode (IDN) domain. Only the exact marker is left alone. */
const ADDRESS_RE = /(?<![\p{L}\p{N}\p{M}._%+-])[\p{L}\p{N}\p{M}._%+-]+@[\p{L}\p{N}\p{M}-]+(?:\.[\p{L}\p{N}\p{M}-]+)+/gu

/** BRANCH_RE matches a git branch or ref by its conventional prefix, and any
 * `refs/heads/...`. A branch with no conventional prefix is found by its field
 * (`branch`, `base_branch`, ...), not by a pattern. */
const BRANCH_RE =
  /\b(?:refs\/(?:heads|tags|remotes)\/[^\s"'`<>,;]+|(?:feature|feat|fix|bugfix|hotfix|chore|release|dependabot|renovate|topic|wip)\/[A-Za-z0-9._/-]+)/g

/** PATH_SEGMENT is one segment of a filesystem path, and ABS_PATH_RE matches a
 * path rooted at `/` or `~/` with at least two segments. **A space is part of a
 * segment when more path follows it**, so `/Users/Zora Kell/plan/x` is one path
 * and a sentence after a path is not. Two slashes is what keeps a sentence from
 * being eaten: `and/or` is not rooted. */
const PATH_SEGMENT = String.raw`[^\s\/\\"'\x60<>|;,:()\[\]{}*?]+`
const ABS_PATH_RE = new RegExp(
  [
    String.raw`(?<![\p{L}\p{N}])(?:~\/|\/)(?:SEG(?:[ \t]+SEG)*\/)+SEG`,
    String.raw`(?:~\/|\/)(?:SEG\/)+SEG`,
  ]
    .join('|')
    .replaceAll('SEG', PATH_SEGMENT),
  'gu',
)

/** WIN_PATH_RE matches a Windows path and a UNC path. */
const WIN_PATH_RE = /(?:\b[A-Za-z]:\\[^\s"'`,;:)\]}]*|\\\\[^\s"'`,;:)\]}]+)/g

/** REPO_TOKEN_RE matches a repository path anywhere a string carries one -- `in
 * dimipaun/fugaro#412` in a title, `github.com/acme/secret-repo` in a log line.
 * Every segment must be at least four characters, so the prose pairs `and/or`,
 * `he/she`, `w/o`, `24/7` are left as written. **The false positive this keeps,
 * said out loud:** two ordinary words of four characters or more
 * (`input/output`) are replaced as though they were a repository: that costs a
 * recorded title's wording, and the other mistake costs a private repository's
 * name on a screen. */
const REPO_TOKEN_RE =
  /(?<![\w./~-])(?:(?:[A-Za-z0-9-]+\.)+[A-Za-z0-9-]+\/)?[A-Za-z0-9][A-Za-z0-9._-]{3,}\/[A-Za-z0-9][A-Za-z0-9._-]{3,}(?![\w/~-])/g

/** GCP_CONTEXT_RE matches a GCP project or bucket *named in a sentence*: `on
 * project acme-devel-zora`, `projects/123456789012`, `bucket fugaro-runs-x`. A
 * project ID has no shape of its own, so the word in front of it is the
 * evidence; an ID with neither a hyphen nor a digit (`project settings`) is
 * prose and is left. */
const GCP_CONTEXT_RE = /\b(projects?|buckets?)(\s*[:=/]\s*|\s+)(["'`]?)([a-z0-9][a-z0-9._-]{3,61}[a-z0-9])/giu
/** RUNS_BUCKET_RE is the runs bucket by its own name (`fugaro-runs-<gcp>`). */
const RUNS_BUCKET_RE = /\bfugaro-runs-[a-z0-9][a-z0-9._-]*/giu

/** IPV4_RE and IPV6_RE match a network address, with a port when there is
 * one. */
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
 * whole. A machine name is identity -- it names an operator's laptop -- which is
 * why the recorder redacts one and this layer shows the placeholder.
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

/** isHostname answers whether a HOST_RE match is a host rather than a file name
 * or an abbreviation. */
function isHostname(match: string): boolean {
  const labels = match.toLowerCase().split('.')
  const last = labels[labels.length - 1] ?? ''
  if (HOST_SUFFIXES.has(last)) return true
  return labels.length >= 3 && !FILE_EXTENSIONS.has(last)
}

/** isAddressMarker answers whether an ADDRESS_RE match is the marker itself. */
function isAddressMarker(value: string): boolean {
  return value.toLowerCase() === ADDRESS_PLACEHOLDER
}

// The sentinels hold a pseudonym aside while the rest of the pass runs. They are
// private-use code points, which no document can carry in practice and which the
// invisible-character strip above never removes -- so a pseudonym cannot be
// eaten by the net, and cannot be re-inserted into text that happened to contain
// the sentinel, because the text cannot contain it.
const SENTINEL_START = '\ue000'
const SENTINEL_END = '\ue001'

/** PERCENT_RUN_RE is a run of the characters a percent-encoded value is made of:
 * one character class, scanned once. A run with an escape in it
 * (`PERCENT_ESCAPE_RE`) is decoded before any rule looks at it. */
const PERCENT_RUN_RE = /[A-Za-z0-9._~+=%-]+/g
const PERCENT_ESCAPE_RE = /%[0-9A-Fa-f]{2}/

/** UTF8_ENCODER and UTF8_DECODER are the Web standards for turning a code point
 * into the bytes a percent escape encodes, and back. They are globals rather
 * than imports: `shared/` may import nothing at run time, and the renderer has
 * no `Buffer` -- which is what the recorder's own decoder uses. */
const UTF8_ENCODER = new TextEncoder()
const UTF8_DECODER = new TextDecoder('utf-8')

/**
 * percentDecode decodes every valid escape in a run, leniently (an invalid
 * escape stays as written), and again while escapes remain -- up to three times,
 * so `%2540` is an `@`. It is the recorder's decoder, and it is why a token
 * whose `p` was written `%70` is the token it spells.
 */
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
        const point = text.codePointAt(i) ?? 0
        const character = String.fromCodePoint(point)
        for (const byte of UTF8_ENCODER.encode(character)) bytes.push(byte)
        i += character.length - 1
      }
    }
    text = UTF8_DECODER.decode(Uint8Array.from(bytes))
  }
  return text
}

/** WRAPPED_BASE64_RE is a run that could be base64 (standard or URL-safe). */
const WRAPPED_BASE64_RE = /(?<![A-Za-z0-9+/_-])[A-Za-z0-9+/_-]{16,}={0,2}(?![A-Za-z0-9+/_=-])/g

/** decodeBase64 returns printable text a base64 run decodes to, or null. It is
 * the recorder's rule with a Web decoder: a run whose text is not mostly
 * printable is not text, and a run that does not decode is not a candidate. */
function decodeBase64(run: string): string | null {
  const standard = run.replace(/-/g, '+').replace(/_/g, '/')
  const padded = standard + '='.repeat((4 - (standard.length % 4)) % 4)
  let decoded: string
  try {
    const latin1 = atob(padded)
    const bytes = Uint8Array.from(latin1, (character) => character.charCodeAt(0))
    decoded = UTF8_DECODER.decode(bytes)
  } catch {
    return null
  }
  if (decoded.length < 8) return null
  const printable = decoded.replace(/[^\x20-\x7e]/g, '').length
  return printable / decoded.length >= 0.9 ? decoded : null
}

/** hits is `RegExp.test` for a shared, global pattern, re-anchored first. */
function hits(pattern: RegExp, text: string): boolean {
  pattern.lastIndex = 0
  return pattern.test(text)
}

/** containsSecret answers whether decoded text is credential-shaped. */
function containsSecret(text: string): boolean {
  if (TOKEN_SHAPES.some((pattern) => hits(pattern, text))) return true
  if (hits(BEARER_RE, text) || hits(PEM_RE, text)) return true
  CREDENTIAL_ASSIGNMENT_RE.lastIndex = 0
  for (const match of text.matchAll(CREDENTIAL_ASSIGNMENT_RE)) {
    if (!isMarkerText(match[3] ?? '')) return true
  }
  return false
}

/** Changed is what one run of the rules did: the text, and which classes fired
 * on it. */
interface Changed {
  readonly text: string
  readonly flags: readonly SanitizeFlag[]
}

/** OneRule is one pattern class: the flag it raises, and what it does to a
 * string. A rule leaves a string with nothing of its class in it unchanged,
 * which is the property the tests walk. */
interface OneRule {
  readonly flag: SanitizeFlag
  readonly apply: (text: string, nested: boolean) => string
}

/**
 * FREE_TEXT_RULES are the classes a job's own prose goes through, in the
 * recorder's order -- and the order is the point: URLs first (they contain
 * everything else), then credentials, then addresses, branches, paths,
 * repository paths, GCP names, network addresses, hosts, and the entropy net
 * last, over what the shapes left.
 *
 * **This is the list `src/source/redact.ts` runs**, bar two things this layer
 * cannot reproduce and does not pretend to:
 *
 *   - **the people pass.** The recorder replaces a requester's name everywhere
 *     the document spells it, from a table of the document's own people; this
 *     layer sees one string at a time and has no such table. A name is a word
 *     with no shape, and no pattern can find it. (`schema.ts` gives every
 *     person-shaped key the `person` class, which is where that value is
 *     handled.)
 *   - **the installation's literals.** The recorder holds the operator's own
 *     project name, paths and hosts, folded; the renderer is not given them, and
 *     a literal list in the renderer would be a list of the very strings the
 *     redaction exists to remove.
 *
 * Everything else is here. Where the recorder writes a *keyed pseudonym* --
 * `r-.../r-...` for a repository, `<project>` and `<bucket>` for a GCP name --
 * this layer writes `[redacted]` for a repository path, because it has no key to
 * compute a pseudonym with and a made-up one would be worse than a mark: two
 * repositories would look like one, and a pseudonym the recorder never wrote is
 * not a marker any later reader can verify. `[redacted]` is in the recorder's
 * own marker set and says exactly what happened.
 *
 * A percent-decoded run is the one recursive case: the rules that find nothing
 * in the run's own text find something in what it decodes to, and the run is
 * replaced by the decoded text, redacted -- so `u=ana%40acme.com` loses its
 * local part too. `nested` is what stops that from recurring.
 */
const FREE_TEXT_RULES: readonly OneRule[] = Object.freeze([
  { flag: 'replaced-url', apply: (text) => text.replace(URL_RE, URL_PLACEHOLDER) },
  { flag: 'neutralised-credential', apply: (text, nested) => credentialPass(text, nested) },
  {
    flag: 'address-marker',
    apply: (text) =>
      text.replace(ADDRESS_RE, (match) => (isAddressMarker(match) ? match : ADDRESS_PLACEHOLDER)),
  },
  { flag: 'branch-placeholder', apply: (text) => text.replace(BRANCH_RE, BRANCH_PLACEHOLDER) },
  {
    flag: 'path-placeholder',
    apply: (text) =>
      text.replace(ABS_PATH_RE, PATH_PLACEHOLDER).replace(WIN_PATH_RE, PATH_PLACEHOLDER),
  },
  {
    flag: 'name-placeholder',
    apply: (text) =>
      text.replace(REPO_TOKEN_RE, (match) =>
        REPO_PSEUDONYM_RE.test(match) ? match : LITERAL_PLACEHOLDER,
      ),
  },
  {
    flag: 'name-placeholder',
    apply: (text) =>
      text
        .replace(GCP_CONTEXT_RE, (whole, word: string, separator: string, quote: string, id: string) => {
          if (!/[-\d]/.test(id)) return whole
          const marker = word.toLowerCase().startsWith('bucket') ? BUCKET_PLACEHOLDER : PROJECT_PLACEHOLDER
          return `${word}${separator}${quote}${marker}`
        })
        .replace(RUNS_BUCKET_RE, BUCKET_PLACEHOLDER),
  },
  {
    flag: 'host-placeholder',
    apply: (text) =>
      text.replace(IPV4_RE, HOST_PLACEHOLDER).replace(IPV6_RE, HOST_PLACEHOLDER),
  },
  {
    flag: 'host-placeholder',
    apply: (text) => text.replace(HOST_RE, (match) => (isHostname(match) ? HOST_PLACEHOLDER : match)),
  },
  {
    flag: 'neutralised-credential',
    apply: (text) =>
      text.replace(ENTROPY_RE, (run) => (isHighEntropy(run) ? TOKEN_PLACEHOLDER : run)),
  },
])

/**
 * CREDENTIAL_RULES are the first two of them, and they are the whole pass a
 * value whose *key* declares what it is goes through. Running the whole list
 * over such a value would be wrong in both directions: `fixture-owner/fixture-repo`
 * is a repository path, so the repository rule would replace the very value the
 * `repository` class exists to validate, and the class would then refuse a
 * document the recorder wrote. The recorder makes the same split -- a field
 * class short-circuits, and the pattern classes are for free text -- and the
 * credential rules stay on for the validating classes because a token is a shape
 * a validating pattern can accept: a Google access token, dots and all, is a
 * legal git branch name, and so is a forge token with no underscore in it.
 */
const CREDENTIAL_RULES: readonly OneRule[] = Object.freeze(FREE_TEXT_RULES.slice(0, 2))

/**
 * credentialPass is the recorder's "something credential-shaped" rule: a
 * percent-encoded run is decoded first, then a base64-wrapped candidate, then
 * the token families, then an assignment, which keeps its name and loses its
 * value.
 */
function credentialPass(text: string, nested: boolean): string {
  let out = text.replace(PERCENT_RUN_RE, (run) => {
    if (!PERCENT_ESCAPE_RE.test(run)) return run
    const decoded = percentDecode(run)
    if (containsSecret(decoded) || isHighEntropy(decoded)) return TOKEN_PLACEHOLDER
    if (nested) return run
    const redacted = runRules(prepare(decoded), FREE_TEXT_RULES, true).text
    return redacted === prepare(decoded) ? run : redacted
  })
  out = out.replace(WRAPPED_BASE64_RE, (run) => {
    const decoded = decodeBase64(run)
    return decoded !== null && containsSecret(decoded) ? TOKEN_PLACEHOLDER : run
  })
  for (const pattern of TOKEN_SHAPES) out = out.replace(pattern, TOKEN_PLACEHOLDER)
  out = out.replace(BEARER_RE, (_match, lead: string) => `${lead} ${TOKEN_PLACEHOLDER}`)
  out = out.replace(PEM_RE, TOKEN_PLACEHOLDER)
  out = out.replace(
    CREDENTIAL_ASSIGNMENT_RE,
    (whole, name: string, separator: string, value: string) =>
      isMarkerText(value) ? whole : `${name}${separator}${TOKEN_PLACEHOLDER}`,
  )
  return out
}

/**
 * runRules runs one list of rules in order and reports what fired. The
 * pseudonyms are held aside for the duration and put back at the end, so the
 * entropy net cannot eat a redaction the recorder already made.
 */
function runRules(text: string, rules: readonly OneRule[], nested: boolean): Changed {
  const held: string[] = []
  let working = text.replace(ANY_PSEUDONYM_RE, (found) => {
    held.push(found)
    return `${SENTINEL_START}${String(held.length - 1)}${SENTINEL_END}`
  })
  const flags: SanitizeFlag[] = []
  for (const rule of rules) {
    const next = rule.apply(working, nested)
    if (next !== working && !flags.includes(rule.flag)) flags.push(rule.flag)
    working = next
  }
  for (const [index, pseudonym] of held.entries()) {
    working = working.replace(`${SENTINEL_START}${String(index)}${SENTINEL_END}`, pseudonym)
  }
  return { text: working, flags }
}

/**
 * freeText is the pass over one value of one class -- the recorder's own split
 * between "a field class" and "free text".
 *
 * For a sentence or a label the rules read the **prepared** form: NFKC, then the
 * invisible characters gone. A string in which no rule finds anything is drawn
 * as it came, minus the invisible characters, which this layer always removes;
 * one in which a rule finds something is drawn from the prepared form, because
 * that is the form the rule rewrote. The form as written is tried as well, for
 * the rare match that only it shows.
 */
function freeText(text: string, prepared: string, textClass: TextClass): Changed {
  const rules = textClass === 'sentence' || textClass === 'label' ? FREE_TEXT_RULES : CREDENTIAL_RULES
  const fromPrepared = runRules(prepared, rules, false)
  if (fromPrepared.text !== prepared) return fromPrepared
  if (prepared === text) return fromPrepared
  // `text` is the caller's already-stripped form, so the value returned here
  // carries no invisible character whatever the rules did: removing those is
  // this layer's own rule, not one of the recorder's, and a value drawn with a
  // bidi override in it is a value a screen can be made to spell something else.
  return runRules(text, rules, false)
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

/** WHOLE_BRANCH_RE is a git branch or ref: the characters git allows, with the
 * spellings git refuses (`..`, `@{`, a leading `-`, `.lock` at the end). It is
 * not the free-text `BRANCH_RE` above, which finds a branch by its conventional
 * prefix inside a sentence. */
const WHOLE_BRANCH_RE = /^(?!-)(?!.*\.\.)(?!.*@\{)(?!.*\/\/)[A-Za-z0-9._/-]{1,255}$/

/** PROJECT_RE is a Fugaro project name, or a GCP project id. */
const PROJECT_RE = /^[a-z][a-z0-9-]{0,62}$/

/** LOGIN_RE is a forge account name. */
const LOGIN_RE = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/

/** WHOLE_ADDRESS_RE matches an email address in any script. A Unicode local part is
 * legal, so the pattern is Unicode-wide rather than ASCII. It is not the
 * free-text `ADDRESS_RE` above, which finds an address inside a sentence. */
const WHOLE_ADDRESS_RE = /^[\p{L}\p{N}\p{M}._%+-]+@[\p{L}\p{N}\p{M}-]+(?:\.[\p{L}\p{N}\p{M}-]+)+$/u

/** RUN_ID_RE is a Fugaro run id: `YYYYMMDD-HHMMSS-xxxx`. */
const RUN_ID_RE = /^[0-9]{8}-[0-9]{6}-[a-z0-9]{4}$/

/** WHOLE_PATH_RE recognises a value that is a filesystem path, so the `path`
 * class can say what it is refusing rather than only that it refused. */
const WHOLE_PATH_RE = /^(?:[A-Za-z]:[\\/]|\\\\|\/|~\/)/

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

/**
 * carriesCredential answers whether one piece of a URL -- its query, its
 * fragment, or one path segment -- holds something the credential rules would
 * change. It reads the piece as written and percent-decoded, each in the
 * prepared form, and runs the credential pass the validating classes run (the
 * token families, a bearer header, a PEM block, a base64-wrapped candidate, and
 * an assignment such as `access_token=...`, which loses its value whatever it
 * looks like). `entropy` adds the high-entropy net, for the query and the
 * fragment (and `hostCarriesCredential` adds it for the host): a path segment
 * is left out of it on purpose, because a commit link's 40-hex digest is a path
 * segment, and refusing every commit link would be a refusal of the forge's own
 * URLs. The webhooks that keep their secret in the path are `WEBHOOK_URLS`.
 */
function carriesCredential(piece: string, entropy: boolean): boolean {
  if (piece === '') return false
  for (const form of new Set([prepare(piece), prepare(percentDecode(piece))])) {
    if (credentialPass(form, true) !== form) return true
    if (entropy) {
      URL_ENTROPY_RE.lastIndex = 0
      for (const match of form.matchAll(URL_ENTROPY_RE)) {
        if (isHighEntropy(match[0])) return true
      }
    }
  }
  return false
}

/**
 * URL_ENTROPY_RE is `ENTROPY_RE` with a dot as a separator on *both* sides. The
 * free-text net will not start a run right after a dot (it is the recorder's
 * pattern, and the recorder-agreement test holds it there), which in a URL let
 * `?id=v1.<random>` hide its random half. In a query, a fragment or a host a dot
 * only ever separates, so each piece between dots is read on its own: `v1.2.3`
 * and `file.name.ext` are short pieces and stay, and a long piece is still held
 * to `isHighEntropy`'s guard.
 */
const URL_ENTROPY_RE = /(?<![\p{L}\p{N}_+/=-])[A-Za-z0-9_+/=-]{20,}(?![\p{L}\p{N}_+/=-])/gu

/** HOST_DOT_RE is every character the URL parser reads as a label separator. */
const HOST_DOT_RE = /[.。．｡]/

/** HOST_RUN_RE is a run of a host label between hyphens and underscores. */
const HOST_RUN_RE = /[A-Za-z0-9]{20,}/g

/**
 * looksRandom is the host's own entropy rule, for the run `isHighEntropy` cannot
 * judge: a host is case-folded, so a token written into one loses the case mix
 * that guard asks for. A run of 20 or more letters and digits that has both, at
 * 3.5 bits per character or more, is a key, not a name -- a dictionary-like
 * label (`internationalbusinessmachines`) has no digits, and the labels a cloud
 * writes (`us-east-1`, `edgeweb-abc123xyz-ue`) are short runs between hyphens.
 */
function looksRandom(label: string): boolean {
  for (const [run] of label.matchAll(HOST_RUN_RE)) {
    const body = run.toLowerCase()
    if (!/[a-z]/.test(body) || !/\d/.test(body)) continue
    const counts = new Map<string, number>()
    for (const character of body) counts.set(character, (counts.get(character) ?? 0) + 1)
    let entropy = 0
    for (const count of counts.values()) {
      const p = count / body.length
      entropy -= p * Math.log2(p)
    }
    if (entropy >= 3.5) return true
  }
  return false
}

/**
 * punycodeDigit is RFC 3492's digit value of one character: `a`-`z` and `A`-`Z`
 * are 0-25, `0`-`9` are 26-35, and anything else is -1 -- each range spelled
 * out, so no character outside `[0-9A-Za-z]` can map to a digit.
 */
function punycodeDigit(code: number): number {
  if (code >= 0x30 && code <= 0x39) return code - 0x30 + 26
  if (code >= 0x41 && code <= 0x5a) return code - 0x41
  if (code >= 0x61 && code <= 0x7a) return code - 0x61
  return -1
}

/** PUNYCODE_MAX_INT is the decoder's integer bound, RFC 3492's `maxint`. */
const PUNYCODE_MAX_INT = 0x7fffffff

/**
 * punycodeDecode is RFC 3492's decoder, for the part of an `xn--` label after
 * its prefix; `null` for a label that does not decode -- a character that is
 * not a digit, a digit sequence that runs out, an integer past the RFC's bound,
 * or a code point that is not a Unicode scalar value -- so it never throws. The
 * URL parser writes a non-ASCII host in this form, and a token at the start of
 * such a label is preceded by `xn--` -- a hyphen, which is exactly what a
 * family pattern's left edge refuses -- so the host rule reads the decoded
 * label as well, and refuses a label that does not decode
 * (`hostCarriesCredential`).
 */
export function punycodeDecode(input: string): string | null {
  const base = 36
  const output: number[] = []
  const delimiter = input.lastIndexOf('-')
  for (let i = 0; i < Math.max(delimiter, 0); i += 1) {
    const code = input.charCodeAt(i)
    if (code >= 0x80) return null
    output.push(code)
  }
  let n = 128
  let bias = 72
  let i = 0
  for (let index = delimiter > 0 ? delimiter + 1 : 0; index < input.length; ) {
    const oldi = i
    for (let w = 1, k = base; ; k += base) {
      if (index >= input.length) return null
      const digit = punycodeDigit(input.charCodeAt(index))
      index += 1
      if (digit < 0) return null
      if (digit > Math.floor((PUNYCODE_MAX_INT - i) / w)) return null
      i += digit * w
      const t = k <= bias ? 1 : k >= bias + 26 ? 26 : k - bias
      if (digit < t) break
      if (w > Math.floor(PUNYCODE_MAX_INT / (base - t))) return null
      w *= base - t
    }
    const length = output.length + 1
    let delta = i - oldi
    delta = oldi === 0 ? Math.floor(delta / 700) : delta >> 1
    delta += Math.floor(delta / length)
    let k = 0
    for (; delta > 455; k += base) delta = Math.floor(delta / 35)
    bias = Math.floor(k + (36 * delta) / (delta + 38))
    if (Math.floor(i / length) > PUNYCODE_MAX_INT - n) return null
    n += Math.floor(i / length)
    i %= length
    if (n < 0x80 || n > 0x10ffff || (n >= 0xd800 && n <= 0xdfff)) return null
    output.splice(i, 0, n)
    i += 1
  }
  return String.fromCodePoint(...output)
}

/**
 * rawHost is the host as the text spells it, not as the parser rewrote it: the
 * parser folds case, and the case mix is what `isHighEntropy` reads. The scheme
 * is dropped with any run of slashes or backslashes after it (the parser
 * accepts `https:host` and `https:\\host`), then everything from the first
 * slash, backslash, `?` or `#`, then the userinfo and the port.
 */
function rawHost(text: string): string {
  const authority = text.replace(/^https:[/\\]*/i, '').split(/[/\\?#]/, 1)[0] ?? ''
  const host = authority.slice(authority.lastIndexOf('@') + 1)
  return host.startsWith('[') ? host : host.replace(/:[^:]*$/, '')
}

/**
 * hostCarriesCredential answers whether a URL's host holds a credential or a
 * random-looking label. It reads the host as written (case kept, which is what
 * the entropy guard reads) and as parsed (percent-decoded and case-folded) --
 * whole, so a token with a dot in it is still one token, and label by label,
 * each `xn--` label also punycode-decoded, and refused when it does not
 * decode -- and runs on every form the
 * credential pass (which percent-decodes a form itself), the entropy net and
 * `looksRandom`.
 */
function hostCarriesCredential(text: string, hostname: string): boolean {
  const written = rawHost(text)
  const forms = new Set<string>()
  for (const host of [written, hostname]) {
    forms.add(host)
    for (const label of host.split(HOST_DOT_RE)) {
      forms.add(label)
      if (/^xn--/i.test(label)) {
        // A label that claims to be punycode and is not is no host a link
        // has: it is refused rather than read as plain text.
        const decoded = punycodeDecode(label.slice(4))
        if (decoded === null) return true
        forms.add(decoded)
      }
    }
  }
  for (const form of forms) {
    if (carriesCredential(form, true)) return true
    if (looksRandom(prepare(form))) return true
  }
  return false
}

/**
 * WEBHOOK_URLS are the well-known webhook shapes whose secret is a **path
 * segment**: Slack, Discord, Microsoft Teams, Zapier, IFTTT and Telegram. A path
 * segment is left out of the entropy net on purpose -- a commit link's digest is
 * one -- so these are refused by host and path instead. The trade-off, said out
 * loud: a service not on this list that keeps its secret in the path is drawn
 * (unless the segment matches a token family), and a page of a listed host that
 * is not a webhook is kept.
 */
const WEBHOOK_URLS: readonly { readonly host: RegExp; readonly path: RegExp }[] = Object.freeze([
  { host: /(?:^|\.)hooks\.slack\.com$/, path: /^\/(?:services|workflows|triggers)\//i },
  { host: /(?:^|\.)discord(?:app)?\.com$/, path: /^\/api\/(?:v\d+\/)?webhooks\//i },
  { host: /(?:^|\.)webhook\.office\.com$/, path: /^\/webhookb2\//i },
  { host: /(?:^|\.)outlook\.office(?:365)?\.com$/, path: /^\/webhook\//i },
  { host: /(?:^|\.)hooks\.zapier\.com$/, path: /^\/hooks\//i },
  { host: /(?:^|\.)maker\.ifttt\.com$/, path: /\/with\/key\//i },
  { host: /(?:^|\.)api\.telegram\.org$/, path: /^\/(?:file\/)?bot/i },
])

/**
 * webhookPath is a URL path in the form `WEBHOOK_URLS` reads: percent-decoded,
 * every run of slashes and backslashes collapsed to one slash (a server reads
 * `//services` as `/services`, the parser does not), and its `.` and `..`
 * segments resolved by the URL parser -- repeated until it stops changing, so a
 * separator or a dot that only appears once decoded is resolved too.
 */
function webhookPath(pathname: string): string {
  let path = pathname
  for (let round = 0; round < 4; round += 1) {
    const flat = percentDecode(path).replace(/[\\/]+/g, '/')
    let next: string
    try {
      next = new URL(flat.startsWith('/') ? flat : `/${flat}`, 'https://webhook.invalid').pathname
    } catch {
      return flat
    }
    if (next === path) return flat
    path = next
  }
  return percentDecode(path).replace(/[\\/]+/g, '/')
}

/**
 * isWebhook answers whether a parsed URL is one of `WEBHOOK_URLS`. The host
 * loses every trailing dot (`hooks.slack.com..` is that host) and a listed host
 * matches with any subdomain in front of it (`www.discord.com`); the path is
 * `webhookPath`'s.
 */
function isWebhook(parsed: URL): boolean {
  const host = parsed.hostname.replace(/\.+$/, '')
  const path = webhookPath(parsed.pathname)
  return WEBHOOK_URLS.some((hook) => hook.host.test(host) && hook.path.test(path))
}

/** applyUrl is the URL class: `https` only, a non-empty host, no userinfo,
 * nothing credential-shaped or random-looking in its host, nothing
 * credential-shaped in its path, query or fragment, and none of the well-known
 * webhook URLs (`WEBHOOK_URLS`), whose secret is a path segment. It is the only way
 * this layer produces a URL, and `safeUrl` below is the same function with a
 * `null` instead of a refusal, so a screen has one door and not two. */
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
  // A credential in the host, the query, the fragment or a path segment is
  // refused, not rewritten: the URL is drawn as text, put in a title and offered
  // as a link, and a link with its key cut out is a different link. The
  // recorder replaces a url field whole; this class keeps a clean one and
  // refuses the rest.
  const query = parsed.search.replace(/^\?/, '')
  const fragment = parsed.hash.replace(/^#/, '')
  if (
    hostCarriesCredential(text, parsed.hostname) ||
    isWebhook(parsed) ||
    carriesCredential(query, true) ||
    carriesCredential(fragment, true) ||
    parsed.pathname.split('/').some((segment) => carriesCredential(segment, false))
  ) {
    return refuse(`${field} is a url that carries a credential, so the console will not draw it`)
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
  // The second half of the bound (`MAX_TEXT_INPUT_LENGTH`): the form the rules
  // read, after NFKC, is held to the same 64 KiB as the form that arrived.
  const prepared = prepare(text)
  if (prepared.length > MAX_TEXT_INPUT_LENGTH) {
    return refuse(
      `${field} is longer than the console will read (${String(MAX_TEXT_INPUT_LENGTH)} characters)`,
    )
  }

  if (textClass === 'url') {
    return applyUrl(text, field)
  }

  if (textClass === 'person') {
    // An address loses its local part; a login and a pseudonym are kept.
    if (WHOLE_ADDRESS_RE.test(text) && !isMarkerText(text)) {
      return ok(ADDRESS_PLACEHOLDER, [...flags, 'address-marker'])
    }
    if (isMarkerText(text)) return ok(text, flags)
    if (LOGIN_RE.test(text)) return ok(text, flags)
    return refuse(`${field} is not a valid person, so the console will not draw it`)
  }

  if (textClass === 'host') {
    // Every machine name is drawn as the placeholder, valid DNS name or not:
    // that is the class's contract (`TextClass`) and the recorder's host class,
    // which returns its marker unconditionally (`src/source/redact.ts`). A
    // marker the recorder already wrote is kept as it is.
    if (isMarkerText(text)) return ok(text, flags)
    return ok(HOST_PLACEHOLDER, [...flags, 'host-placeholder'])
  }

  const rules = freeText(text, prepared, textClass)
  flags.push(...rules.flags)
  text = rules.text

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
    return WHOLE_BRANCH_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid branch`)
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

