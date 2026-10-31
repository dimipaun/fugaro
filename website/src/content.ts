// website/src/content.ts — the site's content pipeline (website plan, Task W1.3).
//
// One module reads every fact the site renders, and one rule decides what happens when
// one of them is not there: **the build fails, loudly, naming the file**. A page that
// renders an empty install block, or an ungenerated security page, is the defect this
// module exists to remove — an `undefined` that reaches a template is a page that ships
// with a hole in it, and nothing in the artifact says so.
//
// Where the facts come from, and why they are read here rather than typed into a page
// (Global Constraint 6, the README's "A page renders a fact; it never restates one"):
//
//   - website/data/site.yaml      the site's own facts — the canonical URL, the
//                                 repository, the feature flags, D97's minimum sample.
//                                 Hand-written, and pinned to the repository by the Go
//                                 boundary test (internal/cli/docs_website_boundary_test.go).
//   - website/data/install.yaml   the install facts (W0.1), pinned to README.md,
//                                 docs/release.md and .goreleaser.yaml by
//                                 internal/cli/docs_website_install_test.go.
//   - website/data/release-summaries.yaml
//                                 the end-user sentence per release (the owner's brief of
//                                 2026-10-19), which the changelog page prints above the
//                                 entry's Highlights: hand-written, one line per release,
//                                 and the page stops the build when a release file it
//                                 renders has no line here.
//   - website/content/*.md        generated documents (W0.4's CLI reference, W0.5's
//                                 security page), each saying on its first line that it
//                                 is generated and by what.
//   - website/content/costs.json  W0.3's aggregation output, published deliberately and
//                                 **absent until the owner runs it**: an empty figure is
//                                 honest, an invented one is not (D96, D97).
//   - website/data/typical-costs.yaml
//                                 what a job costs, as the owner measured it on this
//                                 project's own development runs (the owner's brief of
//                                 2026-10-19). Hand-written, refreshed by editing the file,
//                                 and held to the built page by website/test/caps-and-costs.mjs.
//   - README.md                   the tagline and the subline, which are the two lines
//                                 under its `# Fugaro` title and are read here rather
//                                 than copied into a component, so the site and the README
//                                 cannot drift (F48, D102; changes spec item 2).
//
// Three properties this module is written to hold:
//
//   * **Loud.** A missing file, an empty file, a file whose generated header is gone, a
//     YAML document the reader does not understand and a costs file that publishes a
//     group below D97's minimum all throw, and every message names the file. There is no
//     fallback value anywhere below: a default is how a build renders nothing and says
//     nothing.
//   * **Network-free.** Nothing here fetches, resolves a host or reads a clock: the site
//     is built from the files in this repository (Global Constraint 4).
//   * **Dependency-free.** The standard library and a deliberately small YAML reader —
//     no npm package is added for the site's benefit (Global Constraint 5), and the
//     reader refuses what it does not understand rather than guessing at it, so a data
//     file that grows a construct this reader has not been taught fails the build with
//     its own line number instead of parsing into something wrong.
//
// It is called from website/src/layouts/Base.astro, which every page of the site renders
// through — so the whole generated layer is checked on every build that renders a page.
// The site's first page is W1.4's, so today that check is exercised by
// website/test/layout.mjs, which builds the layout with a page of its own in a temp copy.

import { existsSync, readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { spawnSync } from 'node:child_process'

/**
 * The site's own directory: `website/`.
 *
 * Found rather than assumed, because this module is read from two different places and
 * neither of them is a stable one to count from. Run directly (`node --test`, a script),
 * it is `website/src/content.ts`. Bundled into a build, it is a chunk under
 * `website/dist/.prerender/chunks/` — Astro compiles the layout's frontmatter into the
 * output tree, so `import.meta.url` points into `dist/` and a path built from it is a path
 * into the artifact. Both are inside the site, so walking up to the directory that carries
 * `astro.config.mjs` finds the site in either case; `process.cwd()` — which is the site
 * directory for `npm run build`, and for the test harness that builds a copy — is the
 * fallback for a build that starts somewhere else entirely.
 */
function findSiteRoot(): string {
  let dir = path.dirname(fileURLToPath(import.meta.url))
  for (let depth = 0; depth < 8; depth++) {
    if (existsSync(path.join(dir, 'astro.config.mjs'))) return dir
    const parent = path.dirname(dir)
    if (parent === dir) break
    dir = parent
  }
  const cwd = process.cwd()
  if (existsSync(path.join(cwd, 'astro.config.mjs'))) return cwd
  throw new Error(
    `cannot find the site's root: no directory from ${fileURLToPath(import.meta.url)} up to the filesystem root, and not the working directory ${cwd}, carries astro.config.mjs. The site's facts are read from files beside that config, so a build that cannot find it cannot read them (W1.3 step 2).`,
  )
}

export const SITE_ROOT = findSiteRoot()
/** The repository the site is built from: the site directory's parent. */
export const REPO_ROOT = path.dirname(SITE_ROOT)

const DATA_DIR = path.join(SITE_ROOT, 'data')
const CONTENT_DIR = path.join(SITE_ROOT, 'content')
const PUBLIC_DIR = path.join(SITE_ROOT, 'public')

/**
 * The generated documents the site cannot render without: the CLI reference (W0.4) and
 * the security page's source (W0.5). The reference is written by `go run ./ee/clidoc` from
 * the tagged command tree and the security page by `go run ./internal/clidoc` from
 * SECURITY.md, both in this repository, and both are required here — a page that renders
 * one of them renders the generated file or the build stops.
 *
 * `content/costs.json` is deliberately **not** in this list: it is W0.3's aggregation
 * output, the owner runs the command that writes it, and until then there is nothing to
 * render — which is the honest state on day one (W1.5, D96). The per-recipe ranges section
 * that read it was removed on 2026-10-21 (the owner's ruling) while the file is absent, so
 * nothing on the site renders these figures today; the loader stays, and validates the file
 * the day one is published.
 */
const REQUIRED_DOCUMENTS = ['cli-reference.md', 'security.md']

// The first line of every generated file, and the optional line under it naming the file
// it was generated from: `<!-- generated by go run ./ee/clidoc; do not edit -->` on the
// CLI reference, `<!-- generated by go run ./internal/clidoc; do not edit -->` on the
// security and subscription-terms pages. The command is read out of the file rather than
// typed here, so the site's README and this module are held to the same string the file
// itself carries (website/test/readme.mjs).
const GENERATED_HEADER = /^<!--\s*generated by\s+(.+?);\s*do not edit\s*-->$/
const SOURCE_HEADER = /^<!--\s*source:\s*(.+?)\s*-->$/

/** Where a file lives, relative to the repository root: what every message below names. */
function repoRelative(file: string): string {
  return path.relative(REPO_ROOT, file)
}

/**
 * The bytes of a file the site renders, or a build error naming it. A file that is
 * missing and a file that is empty are the same failure here on purpose: an empty
 * generated document renders an empty page, which is the silent hole this pipeline
 * exists to prevent.
 */
function readBytes(file: string, what: string): string {
  if (!existsSync(file)) {
    throw new Error(
      `${repoRelative(file)} is missing: ${what}. The site's build fails rather than rendering an empty block (W1.3 step 2), and the file this build reads is the one named here.`,
    )
  }
  const text = readFileSync(file, 'utf8')
  if (text.trim() === '') {
    throw new Error(
      `${repoRelative(file)} is empty: ${what}. An empty file is an empty page, and the build fails rather than rendering one (W1.3 step 2).`,
    )
  }
  return text
}

// ---------------------------------------------------------------------------------------
// A small YAML reader, for the subset the site's data files use.
//
// The site's facts are YAML because a fact file a person edits should be readable and
// commentable; they are read by this reader because the site ships no npm dependency for
// its own benefit (Global Constraint 5) and because a data file is small, hand-written and
// versioned. The subset is the one website/data/*.yaml actually uses — nested maps,
// sequences of scalars and of maps, flow sequences, quoted scalars, block scalars (`|`,
// `|-`, `>`, `>-`) and comments — and everything outside it is an error with a line
// number, never a guess. A fact file that grows a construct this reader has not been
// taught fails the build loudly, which is the same rule as a missing file.
//
// `parseYAML` and the validators below are exported because every data file the site reads
// is read by them: a second reader would be a second dialect, and the two would disagree
// about a comment, a float or a duplicate key before anyone noticed. `src/lib/landing.mjs`
// is the one caller outside this module (go-to-market plan, Task G2.1).
// ---------------------------------------------------------------------------------------

type Scalar = string | number | boolean | null
type Value = Scalar | Value[] | { [key: string]: Value }

interface Line {
  /** The line's number in the file, for the messages below. */
  number: number
  /** How far the line is indented, in spaces. */
  indent: number
  /** The line with its surrounding whitespace removed. */
  text: string
  /** The line as written, indentation included: block scalars keep their own layout. */
  raw: string
  /** A blank line, or a comment: a line no document element starts on. */
  blank: boolean
}

function splitLines(raw: string): Line[] {
  return raw.split('\n').map((line, index) => {
    const text = line.trim()
    return {
      number: index + 1,
      indent: line.length - line.trimStart().length,
      text,
      raw: line,
      blank: text === '' || text.startsWith('#'),
    }
  })
}

// A mapping line: `key: value`, where the colon is followed by whitespace or ends the
// line. The second half of that rule is what keeps `https://fugaro.example` a scalar and
// `canonical_url: https://fugaro.example` a key.
const MAPPING_LINE = /^([^:#\s][^:#]*):(\s.*|)$/

// A block scalar's indicator: `|` or `>` with an optional chomping sign.
const BLOCK_SCALAR = /^[|>][-+]?$/

/**
 * A value line with its trailing `#` comment removed.
 *
 * The data files are YAML, and in YAML `- brew install dimipaun/tap/fugaro   # the cask`
 * is a command and a comment, not a command with a comment glued to the end of it. This
 * reader reads it the same way rather than adopting the comment into the fact: a fact that
 * silently carries somebody's note is a fact the site renders wrongly, and it is the same
 * class of defect as a missing file — except that nothing fails.
 *
 * Two of YAML's own rules are kept, and both matter here: a `#` starts a comment only at
 * the start of the text or after whitespace, so a URL's fragment (`…#anchor`) is not one,
 * and a `#` inside a quoted scalar is not one either, so a value may contain the character
 * by quoting it. Block scalars never come through here: their lines are content, and
 * install.yaml's verification commands carry a `# macOS` and a `# Linux` that are part of
 * the command a reader copies.
 */
function stripComment(text: string): string {
  // YAML's rule: a quote is a quote only where it OPENS a scalar. In a plain scalar an
  // apostrophe is text (`the cask's page`), so it protects nothing. A flow sequence opens
  // a scalar after its `[` and after each `,`, so a quote there opens one too.
  const flow = text.startsWith('[')
  let quote = ''
  let opens = true
  for (let at = 0; at < text.length; at++) {
    const ch = text[at]
    if (quote !== '') {
      if (quote === '"' && ch === '\\') at++
      else if (ch === quote && quote === "'" && text[at + 1] === "'") at++
      else if (ch === quote) {
        quote = ''
        opens = false
      }
      continue
    }
    if ((ch === '"' || ch === "'") && opens) {
      quote = ch
      continue
    }
    if (ch === '#' && (at === 0 || /\s/.test(text[at - 1]))) return text.slice(0, at).trimEnd()
    // Whether the next character may open a quoted scalar: at the start, and in a flow
    // sequence after `[` or `,` (spaces between do not change it).
    if (!/\s/.test(ch)) opens = flow && (ch === '[' || ch === ',')
  }
  return text
}

export function parseYAML(raw: string, file: string): Value {
  const lines = splitLines(raw)
  let i = 0

  const fail = (message: string, at?: Line): never => {
    const where = at ? `:${at.number}` : ''
    throw new Error(
      `${file}${where}: ${message}. The site's own YAML reader understands the subset the site's data files use — nested maps, sequences of scalars and of maps, flow sequences, quoted scalars, block scalars (| and >) and comments — and refuses anything else rather than guessing at it (W1.3 step 2).`,
    )
  }

  const skip = (): void => {
    while (i < lines.length && lines[i].blank) i++
  }

  const scalar = (text: string, at: Line): Scalar => {
    text = stripComment(text).trim()
    // Quoted scalars first: quoting is how a value carries a `#` or a `: ` that would
    // otherwise be a comment or a nested mapping. Everything below this is a plain scalar.
    if (text.startsWith('"') || text.startsWith("'")) {
      if (text.length < 2 || !text.endsWith(text[0])) {
        fail(`the quoted scalar ${JSON.stringify(text)} does not close on this line`, at)
      }
      const inner = text.slice(1, -1)
      return text[0] === '"' ? inner.replace(/\\(["\\])/g, '$1') : inner.replaceAll("''", "'")
    }
    if (text.startsWith('[')) {
      if (!text.endsWith(']')) fail(`the flow sequence ${JSON.stringify(text)} does not close on this line`, at)
      const inner = text.slice(1, -1).trim()
      return inner === '' ? [] : inner.split(',').map((part) => scalar(part.trim(), at))
    }
    // The constructs this reader has not been taught, refused by name rather than read as
    // the string they look like: an alias, an anchor and a tag are YAML's own references
    // and this reader resolves none of them, and a flow mapping is a shape it does not
    // parse. Each of them, taken for text, is a fact that renders as `*defaults` or
    // `{a: 1}` on a page — which is why the contract is "refuse", not "guess".
    if (/^[*&!{}]/.test(text)) {
      fail(`the value ${JSON.stringify(text)} is a YAML alias, anchor, tag or flow mapping, and this reader reads none of them`, at)
    }
    // A plain scalar cannot carry a nested mapping: `key: a: b` is a YAML error, and this
    // reader refuses it rather than reading the value as the string "a: b".
    if (/:\s/.test(text)) {
      fail(`the value ${JSON.stringify(text)} carries ": ", which makes it a mapping rather than a value; quote it if it is meant as text`, at)
    }
    if (text === 'true') return true
    if (text === 'false') return false
    if (text === 'null' || text === '~') return null
    if (/^-?\d+$/.test(text)) return Number(text)
    if (/^-?\d+\.\d+$/.test(text)) return Number(text)
    return text
  }

  // A block scalar: the lines under it that are indented further than its own line,
  // verbatim (`|`) or folded into paragraphs (`>`), with `-` stripping the final newline.
  // Blank lines inside the block are the block's, and a `#` inside one is content rather
  // than a comment: install.yaml's verification commands carry both.
  //
  // The caller has already stepped past the line that carried the indicator — that is the
  // convention everywhere below — so the body starts at the current line.
  const blockScalar = (indicator: string, parentIndent: number): string => {
    const folded = indicator.startsWith('>')
    const strip = indicator.endsWith('-')
    const kept: string[] = []
    let bodyIndent = -1
    while (i < lines.length) {
      const line = lines[i]
      if (line.raw.trim() === '') {
        kept.push('')
        i++
        continue
      }
      if (line.indent <= parentIndent) break
      if (bodyIndent === -1) bodyIndent = line.indent
      kept.push(line.raw.slice(bodyIndent))
      i++
    }
    while (kept.length > 0 && kept[kept.length - 1] === '') kept.pop()
    const text = folded
      ? kept
          .join('\n')
          .split(/\n\s*\n/)
          .map((paragraph) =>
            paragraph
              .split('\n')
              .map((line) => line.trim())
              .filter((line) => line !== '')
              .join(' '),
          )
          .join('\n')
      : kept.join('\n')
    return strip ? text : `${text}\n`
  }

  // The value on the right of a `key:` line: a scalar on the line itself, a block scalar,
  // or a nested block on the lines below it.
  const readValue = (rest: string, at: Line, parentIndent: number): Value => {
    // The comment goes before the shape is decided: `key: |- # a literal block` is a block
    // scalar with a comment on it, and `key: # only a comment` is a key with no value.
    rest = stripComment(rest).trim()
    if (rest === '') {
      skip()
      const next = lines[i]
      if (next && next.indent > parentIndent) return block()
      return fail(`"${at.text}" has no value`, at)
    }
    if (BLOCK_SCALAR.test(rest)) return blockScalar(rest, parentIndent)
    return scalar(rest, at)
  }

  const mapping = (indent: number, seed?: { key: string; rest: string; at: Line }): { [key: string]: Value } => {
    const map: { [key: string]: Value } = {}
    const put = (key: string, rest: string, at: Line): void => {
      if (key in map) fail(`the key "${key}" is written twice`, at)
      map[key] = readValue(rest, at, indent)
    }
    if (seed) put(seed.key, seed.rest, seed.at)
    while (true) {
      skip()
      const line = lines[i]
      if (!line || line.indent < indent) break
      if (line.indent > indent) fail('this line is indented further than the key above it', line)
      if (line.text === '-' || line.text.startsWith('- ')) {
        fail('a sequence item where a "key: value" line was expected', line)
      }
      const m = MAPPING_LINE.exec(line.text)
      if (!m) fail(`cannot read ${JSON.stringify(line.text)} as a "key: value" line`, line)
      const key = m[1].trim()
      const rest = m[2].trim()
      i++
      put(key, rest, line) // readValue, which strips the value's comment before it shapes it
    }
    return map
  }

  const sequence = (indent: number): Value[] => {
    const out: Value[] = []
    while (true) {
      skip()
      const line = lines[i]
      if (!line || line.indent < indent) break
      if (line.indent > indent) fail('this line is indented further than the sequence item above it', line)
      if (!(line.text === '-' || line.text.startsWith('- '))) break
      const rest = line.text === '-' ? '' : line.text.slice(2).trim()
      if (BLOCK_SCALAR.test(rest)) {
        i++
        out.push(blockScalar(rest, indent))
        continue
      }
      // An item that opens a map on its own line: `- id: homebrew`, with the item's
      // remaining keys on the lines below, indented past the dash.
      const m = rest === '' ? null : MAPPING_LINE.exec(rest)
      if (m) {
        i++
        out.push(mapping(indent + 2, { key: m[1].trim(), rest: m[2].trim(), at: line }))
        continue
      }
      i++
      out.push(readValue(rest, line, indent))
    }
    return out
  }

  const block = (): Value => {
    skip()
    const line = lines[i]
    if (!line) return fail('the document ends where a value was expected')
    return line.text === '-' || line.text.startsWith('- ') ? sequence(line.indent) : mapping(line.indent)
  }

  skip()
  if (i >= lines.length) fail('the file carries no document')
  const value = block()
  skip()
  if (i < lines.length) fail('there is content after the document', lines[i])
  return value
}

// ---------------------------------------------------------------------------------------
// The validators. Every one of them names the file and the key it refused, because a
// build error is read by whoever just edited the fact file.
// ---------------------------------------------------------------------------------------

// What a key that is not there reads as, in the messages below: "not written in the file"
// rather than the word `undefined`, because the reader of a build error is looking at the
// file, not at this module.
export function describe(value: Value): string {
  return value === undefined ? 'not written in the file' : JSON.stringify(value)
}

export function asMap(value: Value, file: string, what: string): { [key: string]: Value } {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as a map of keys to values, so nothing below it can be read (W1.3 step 2).`)
  }
  return value
}

export function asList(value: Value, file: string, what: string): Value[] {
  if (!Array.isArray(value)) {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as a list, so nothing below it can be read (W1.3 step 2).`)
  }
  return value
}

export function asString(value: Value, file: string, what: string): string {
  if (typeof value !== 'string') {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as a string — every fact the site renders is written down, and none of them has a default (W1.3 step 2).`)
  }
  return value
}

function asBool(value: Value, file: string, what: string): boolean {
  if (typeof value !== 'boolean') {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as true or false (W1.3 step 2).`)
  }
  return value
}

function asInt(value: Value, file: string, what: string): number {
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as a whole number (W1.3 step 2).`)
  }
  return value
}

function asNumber(value: Value, file: string, what: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as a number (W1.3 step 2).`)
  }
  return value
}

/**
 * A figure the page prints in dollars and cents: every figure of the owner's measurement is
 * one, and each is printed to the cent and never rounded (`money()` in the caps-and-costs
 * guide, and `website/data/typical-costs.yaml`'s own promise that a figure is never rounded
 * there). So a figure that is not a whole number of cents is refused here rather than rounded
 * on the page: `0.365` would print as `$0.36`, which is a figure neither the file nor the
 * measurement behind it carries. The tolerance is float noise on a decimal written by hand
 * (`0.1 + 0.2`), an order of magnitude of room for a third decimal.
 */
function asCents(value: Value, file: string, what: string): number {
  const figure = asNumber(value, file, what)
  if (Math.abs(figure * 100 - Math.round(figure * 100)) > 1e-6) {
    throw new Error(
      `${file}: ${what} is ${figure}, which is not a whole number of cents, and the page prints every figure it carries in dollars and cents without rounding it (W1.3 step 2).`,
    )
  }
  return figure
}

function asURL(value: Value, file: string, what: string): string {
  const text = asString(value, file, what)
  let url: URL
  try {
    url = new URL(text)
  } catch {
    throw new Error(`${file}: ${what} is ${JSON.stringify(text)}, which is not an absolute URL (W1.3 step 2).`)
  }
  return url.href
}

/** A key a data file may leave out, read as its own documented default. */
function optional(map: { [key: string]: Value }, key: string, fallback: Value): Value {
  return map[key] === undefined ? fallback : map[key]
}

function keysOf(map: { [key: string]: Value }): string[] {
  return Object.keys(map).sort()
}

export function refuseUnknownKeys(map: { [key: string]: Value }, allowed: string[], file: string): void {
  for (const key of keysOf(map)) {
    if (!allowed.includes(key)) {
      throw new Error(
        `${file}: the key "${key}" is not one the site's pages read. A fact nobody reads is a fact nothing pins — internal/cli/docs_website_boundary_test.go decodes this file with unknown fields refused — so a new key is added with the page that renders it (W1.3 step 2).`,
      )
    }
  }
}

// ---------------------------------------------------------------------------------------
// website/data/site.yaml — the site's own facts.
// ---------------------------------------------------------------------------------------

/** The analytics block: D93's answer is Firebase Analytics behind a consent banner. */
export interface AnalyticsFacts {
  /** False until W3.2 sets the measurement id and turns it on. */
  enabled: boolean
  /** The analytics property, empty while the flag is off. */
  measurementID: string
}

/**
 * The campaign measurement's configuration (G2.3, D401): the two vendor ids and the
 * one origin each of the three loaders fetches its script from. An empty id disables
 * its loader: this repository invents no identifier (R3), and both ids are the
 * owner's, supplied on 2026-10-31.
 */
export interface MarketingFacts {
  /** The Meta pixel's id, which the owner supplied on 2026-10-31. */
  metaPixelID: string
  /** The origin the pixel's own script is fetched from. */
  metaOrigin: string
  /** The LinkedIn partner id, which the owner supplied on 2026-10-31. */
  linkedinPartnerID: string
  /** The origin the insight tag's own script is fetched from. */
  linkedinOrigin: string
  /** The origin Calendly's widget script and stylesheet are fetched from. */
  calendlyOrigin: string
}

/** The features the site renders only when they exist (D95, D107). */
export interface FeatureFacts {
  consoleDownloads: boolean
  video: boolean
}

/** website/data/site.yaml, validated: every value a page reads comes from here. */
export interface SiteFacts {
  canonicalURL: string
  repository: string
  analytics: AnalyticsFacts
  marketing: MarketingFacts
  features: FeatureFacts
  minRuns: number
  allowedHosts: string[]
}

const SITE_FILE = 'website/data/site.yaml'
// The keys site.yaml may carry: the reader refuses any other, so a fact nobody reads is a
// failure rather than a value no page and no test would ever see. A second domain's key is
// not here and no longer exists in the file -- W2.2 wrote one and the owner ruled on
// 2026-10-17 that there is no second domain (the amendment under plan Task W2.2), so the
// key coming back is refused with the key list as well as by the Go boundary test.
const SITE_KEYS = ['allowed_hosts', 'analytics', 'canonical_url', 'features', 'marketing', 'min_runs', 'repository', 'version']

// The keys the campaign measurement's block may carry, and the ones that are an origin rather
// than an id: an origin is read as an absolute origin and refused anything else, and an id is
// read as a string — empty meaning "this loader does not load".
const MARKETING_KEYS = ['calendly_origin', 'linkedin_origin', 'linkedin_partner_id', 'meta_origin', 'meta_pixel_id']
const MARKETING_ORIGINS = ['calendly_origin', 'linkedin_origin', 'meta_origin']

export function loadSiteFacts(): SiteFacts {
  const file = SITE_FILE
  const doc = asMap(
    parseYAML(readBytes(path.join(DATA_DIR, 'site.yaml'), 'the site\'s own facts: the canonical URL, the repository, the feature flags and D97\'s minimum sample (W0.2)'), file),
    file,
    'the document',
  )
  refuseUnknownKeys(doc, SITE_KEYS, file)

  if (asInt(doc.version, file, 'version') !== 1) {
    throw new Error(`${file}: version is not 1, and this reader knows version 1 (W1.3 step 2).`)
  }

  // The canonical URL: the origin alone, because the canonical <link>, the sitemap, the
  // OpenGraph tag and every absolute link the site prints are built from it. The Go
  // boundary test refuses a reserved placeholder host here since W2.2 wrote the real
  // domain, so a build with the placeholder back in this line fails there rather than
  // shipping a canonical link to a name that cannot resolve.
  const written = asString(doc.canonical_url, file, 'canonical_url')
  const canonical = new URL(asURL(doc.canonical_url, file, 'canonical_url'))
  if (written !== canonical.origin) {
    throw new Error(
      `${file}: canonical_url is ${JSON.stringify(written)}, and the site builds every absolute link from the origin alone (${canonical.origin}) — the canonical <link>, the sitemap and the OpenGraph tag each append their own path, so a trailing slash, a path, a query or a scheme-less host here would be copied into all of them (W1.3 step 1).`,
    )
  }

  // Both halves of one fact: analytics that report nowhere, and a property id nothing
  // reports to, are broken states, so the flag and the id are read together and must
  // agree. The Go boundary test pins the same pair, and W3.2 moves both in one commit
  // (D93) — which is why this is an agreement rather than "both off".
  const analytics = asMap(doc.analytics, file, 'analytics')
  refuseUnknownKeys(analytics, ['enabled', 'measurement_id'], `${file} analytics`)
  const enabled = asBool(analytics.enabled, file, 'analytics.enabled')
  const measurementID = asString(analytics.measurement_id, file, 'analytics.measurement_id')
  if (enabled !== (measurementID.trim() !== '')) {
    throw new Error(
      `${file}: analytics is enabled=${enabled} with measurement_id=${JSON.stringify(measurementID)}, and the two move together — W3.2 sets the property id and turns the flag on in one commit, behind W1.12's consent gate (D93, W1.3 step 2).`,
    )
  }

  const features = asMap(doc.features, file, 'features')
  refuseUnknownKeys(features, ['console_downloads', 'video'], `${file} features`)

  const allowedHosts = asList(doc.allowed_hosts, file, 'allowed_hosts').map((host, index) =>
    asString(host, file, `allowed_hosts[${index}]`),
  )
  if (allowedHosts.length === 0) {
    throw new Error(`${file}: allowed_hosts is empty, and the site's link and origin checks admit exactly the hosts on it (W1.11).`)
  }
  for (const host of allowedHosts) {
    if (host !== host.toLowerCase() || /[/:@?#]/.test(host)) {
      throw new Error(
        `${file}: allowed_hosts carries ${JSON.stringify(host)}, and an entry is a bare lowercase host with no scheme, port or path — it is compared against a URL's host (W1.11).`,
      )
    }
  }

  // The campaign measurement (G2.3): two ids and the three origins the loaders fetch their
  // scripts from. Every origin is an origin alone — the banner's configuration carries it into
  // the loader, which builds each request by appending its own path, so a path, a query or a
  // trailing slash here would be copied into all three — and an id may be empty, which is how a
  // vendor the owner has not supplied is wired but not loaded.
  const marketing = asMap(doc.marketing, file, 'marketing')
  refuseUnknownKeys(marketing, MARKETING_KEYS, `${file} marketing`)
  const marketingOrigins: Record<string, string> = {}
  for (const key of MARKETING_ORIGINS) {
    const written = asString(marketing[key], file, `marketing.${key}`)
    const url = new URL(asURL(marketing[key], file, `marketing.${key}`))
    if (written !== url.origin || url.protocol !== 'https:') {
      throw new Error(
        `${file}: marketing.${key} is ${JSON.stringify(written)}, and it is an origin alone over https (${url.origin}): the loader builds each request by appending the vendor's own path to it, so a path, a query, a trailing slash or another scheme here would be copied into the request (G2.3, D401).`,
      )
    }
    marketingOrigins[key] = url.origin
  }

  const minRuns = asInt(doc.min_runs, file, 'min_runs')
  if (minRuns < 1) {
    throw new Error(`${file}: min_runs is ${minRuns}, and D97's minimum sample is a positive count (W0.3, internal/stats.MinRuns).`)
  }

  return {
    canonicalURL: canonical.origin,
    repository: asURL(doc.repository, file, 'repository'),
    analytics: { enabled, measurementID },
    marketing: {
      metaPixelID: asString(marketing.meta_pixel_id, file, 'marketing.meta_pixel_id'),
      metaOrigin: marketingOrigins.meta_origin,
      linkedinPartnerID: asString(marketing.linkedin_partner_id, file, 'marketing.linkedin_partner_id'),
      linkedinOrigin: marketingOrigins.linkedin_origin,
      calendlyOrigin: marketingOrigins.calendly_origin,
    },
    features: {
      consoleDownloads: asBool(features.console_downloads, file, 'features.console_downloads'),
      video: asBool(features.video, file, 'features.video'),
    },
    minRuns,
    allowedHosts,
  }
}

// ---------------------------------------------------------------------------------------
// website/data/install.yaml — the install facts (W0.1), which the hero and the
// getting-started page render command by command rather than typing them.
// ---------------------------------------------------------------------------------------

/** One platform the site describes, with the repository's own status for it. */
export interface PlatformFacts {
  id: string
  name: string
  goos: string
  /** `supported` or `untested`: the wording D94 settled, and never a support promise. */
  status: string
  note: string
}

/** One way to install, with the commands the site prints verbatim. */
export interface InstallPathFacts {
  id: string
  name: string
  /** The file in this repository the commands were taken from. */
  source: string
  platforms: string[]
  commands: string[]
  /** The path's own sentence, rendered as written. */
  note: string
  /** Development only: the site says so, and offers no release artifact (W0.1). */
  devOnly: boolean
  /** The Releases page, for the path that downloads an archive. Empty otherwise. */
  releasePage: string
  /** The Homebrew tap, for the path that installs through it. Empty otherwise. */
  tapRepository: string
  /** The archive's name template, as .goreleaser.yaml writes it. Empty otherwise. */
  nameTemplate: string
  /** The archive's extension, without the dot. Empty otherwise. */
  extension: string
}

/** website/data/install.yaml, validated. */
export interface InstallFacts {
  build: { os: string[]; arch: string[] }
  platforms: PlatformFacts[]
  paths: InstallPathFacts[]
}

const INSTALL_FILE = 'website/data/install.yaml'

export function loadInstallFacts(): InstallFacts {
  const file = INSTALL_FILE
  const doc = asMap(
    parseYAML(readBytes(path.join(DATA_DIR, 'install.yaml'), 'the one install source the site renders: the commands, the platforms and the archive names (W0.1)'), file),
    file,
    'the document',
  )
  refuseUnknownKeys(doc, ['build', 'paths', 'platforms', 'version'], file)

  if (asInt(doc.version, file, 'version') !== 1) {
    throw new Error(`${file}: version is not 1, and this reader knows version 1 (W1.3 step 2).`)
  }

  const build = asMap(doc.build, file, 'build')
  refuseUnknownKeys(build, ['arch', 'os'], `${file} build`)
  const strings = (value: Value, what: string): string[] =>
    asList(value, file, what).map((entry, index) => asString(entry, file, `${what}[${index}]`))

  const platforms = asList(doc.platforms, file, 'platforms').map((entry, index) => {
    const platform = asMap(entry, file, `platforms[${index}]`)
    refuseUnknownKeys(platform, ['goos', 'id', 'name', 'note', 'status'], `${file} platforms[${index}]`)
    return {
      id: asString(platform.id, file, `platforms[${index}].id`),
      name: asString(platform.name, file, `platforms[${index}].name`),
      goos: asString(platform.goos, file, `platforms[${index}].goos`),
      status: asString(platform.status, file, `platforms[${index}].status`),
      note: asString(optional(platform, 'note', ''), file, `platforms[${index}].note`),
    }
  })
  if (platforms.length === 0) {
    throw new Error(`${file}: platforms is empty, and the install section renders the platforms a reader might be on (W0.1).`)
  }

  // The facts every path carries, and the ones only some do. An optional fact is read as
  // the empty string rather than defaulted to a sentence: a page renders the note this
  // file writes or it renders none, and it never invents one (Global Constraint 6).
  const paths = asList(doc.paths, file, 'paths').map((entry, index) => {
    const installPath = asMap(entry, file, `paths[${index}]`)
    refuseUnknownKeys(installPath, ['commands', 'dev_only', 'extension', 'id', 'name', 'name_template', 'note', 'platforms', 'release_page', 'source', 'tap_repository'], `${file} paths[${index}]`)
    const what = `paths[${index}]`
    // A path a reader can follow: the commands they type, or the page they download from.
    // The release archive has the second and no command (W0.1), and a path with neither is
    // an install step that asks a reader to do nothing.
    const commands = strings(optional(installPath, 'commands', []), `${what}.commands`)
    const releasePage = asString(optional(installPath, 'release_page', ''), file, `${what}.release_page`)
    if (commands.length === 0 && releasePage === '') {
      throw new Error(`${file}: ${what} carries neither a command nor a release page, so there is nothing on the page for a reader to follow (W1.3 step 2).`)
    }
    return {
      id: asString(installPath.id, file, `${what}.id`),
      name: asString(installPath.name, file, `${what}.name`),
      source: asString(installPath.source, file, `${what}.source`),
      platforms: strings(installPath.platforms, `${what}.platforms`),
      commands,
      note: asString(optional(installPath, 'note', ''), file, `${what}.note`),
      devOnly: asBool(optional(installPath, 'dev_only', false), file, `${what}.dev_only`),
      releasePage,
      tapRepository: asString(optional(installPath, 'tap_repository', ''), file, `${what}.tap_repository`),
      nameTemplate: asString(optional(installPath, 'name_template', ''), file, `${what}.name_template`),
      extension: asString(optional(installPath, 'extension', ''), file, `${what}.extension`),
    }
  })

  return {
    build: { os: strings(build.os, 'build.os'), arch: strings(build.arch, 'build.arch') },
    platforms,
    paths,
  }
}

// ---------------------------------------------------------------------------------------
// website/data/release-summaries.yaml — the end-user summary of each release (the owner's
// brief of 2026-10-19).
//
// The changelog renders one summary under each release's heading and the release file's own
// Highlights below it. The summary is a product feature a reader understands rather than an
// operator step, so it is written for the site and lives beside the site's other facts; the
// release file stays the source of an entry's full text and is never edited for it.
// ---------------------------------------------------------------------------------------

/** website/data/release-summaries.yaml, validated: the release's tag, to its summary. */
export type ReleaseSummaries = { [tag: string]: string }

const SUMMARIES_FILE = 'website/data/release-summaries.yaml'

/** The tag a summary is keyed by, which is the release file's own name without its `.md`. */
const RELEASE_TAG = /^v\d+\.\d+\.\d+$/

export function loadReleaseSummaries(): ReleaseSummaries {
  const file = SUMMARIES_FILE
  const doc = asMap(
    parseYAML(
      readBytes(path.join(DATA_DIR, 'release-summaries.yaml'), 'the end-user summary the changelog renders under each release (the owner\'s brief of 2026-10-19)'),
      file,
    ),
    file,
    'the document',
  )
  refuseUnknownKeys(doc, ['summaries', 'version'], file)

  if (asInt(doc.version, file, 'version') !== 1) {
    throw new Error(`${file}: version is not 1, and this reader knows version 1 (W1.3 step 2).`)
  }

  const summaries = asMap(doc.summaries, file, 'summaries')
  const out: ReleaseSummaries = {}
  for (const [tag, value] of Object.entries(summaries)) {
    // A key that is not a release's tag is a summary no entry can render: the changelog looks
    // a release up by the tag (the filename's own), so a misspelt key is an orphan the reader
    // of this file has to see rather than a line that silently never appears.
    if (!RELEASE_TAG.test(tag)) {
      throw new Error(`${file}: ${tag} is not a release tag, and summaries is keyed by them: vX.Y.Z, as docs/releases/vX.Y.Z.md writes it.`)
    }
    const summary = asString(value, file, `summaries.${tag}`)
    if (summary.trim() === '') {
      throw new Error(`${file}: summaries.${tag} is empty, and the changelog renders it under the release's heading: an empty summary is a release with nothing said about it (the owner's brief of 2026-10-19).`)
    }
    out[tag] = summary
  }
  if (Object.keys(out).length === 0) {
    throw new Error(`${file}: summaries is empty, and the changelog renders one summary per release (the owner's brief of 2026-10-19).`)
  }
  return out
}

// ---------------------------------------------------------------------------------------
// website/data/typical-costs.yaml — what a job costs, as the owner measured it on this
// project's own development runs (the owner's brief of 2026-10-19).
//
// It is a hand-written fact file like `site.yaml` and `install.yaml`, and the page that
// renders it is the caps-and-costs guide: the figures are the ones the owner recorded, the
// page prints them and no others, and a refresh is an edit here rather than an edit to a
// page (Global Constraint 6, D96, D97). The sample size is printed beside every figure, and
// a row drawn from fewer runs than `site.yaml`'s `min_runs` is refused here rather than
// rendered: the site publishes no figure from a sample thinner than that, and the two
// cannot disagree (D97).
// ---------------------------------------------------------------------------------------

/** One kind of run, with the figures the owner measured for it. */
export interface TypicalCostJob {
  /** The row's name in a build error, and the built row's own hook. */
  id: string
  /** The row's label, as a reader reads it. */
  name: string
  /** The sample the row's figures were drawn from. */
  runs: number
  medianUSD: number
  /** The range the middle half of the runs fell in, or null when the file carries none. */
  middleHalfUSD: { low: number; high: number } | null
  nineInTenUnderUSD: number
  /** The most expensive single run of that kind, or null when the file carries none. */
  worstUSD: number | null
}

/** website/data/typical-costs.yaml, validated. */
export interface TypicalCostsFacts {
  /** The window the runs were taken from, and how many finished in it. `to` is the day the
   * figures were taken, so the page prints the age of its own numbers. */
  from: string
  to: string
  runs: number
  jobs: TypicalCostJob[]
  /** The per-run cap Fugaro's own installation sets today: the ceiling a looping run meets. Not
   * a CLI default, so it is a fact about the installation the runs were taken from and never a
   * claim about what `fugaro` ships. It is **not** the cap these figures were measured under —
   * see `measuredUnderCapUSD`. */
  perRunCapUSD: number
  /** The day the cap above took effect, as the file spells it (`YYYY-MM-DD`). The page prints
   * it beside the cap, so a reader can tell which cap was in force when the figures were
   * measured rather than taking today's cap for the one they ran under. */
  perRunCapSince: string
  /** The per-run cap the figures above were measured under, which is a second figure and not a
   * copy of the one above: the owner lowered the cap on the date above, so a page that printed
   * today's cap as the one these runs ran under would claim a measurement under a cap that did
   * not exist yet. The bars are drawn against this one, so every mark sits inside its track. */
  measuredUnderCapUSD: number
}

const TYPICAL_COSTS_FILE = 'website/data/typical-costs.yaml'

export function loadTypicalCosts(minRuns: number): TypicalCostsFacts {
  const file = TYPICAL_COSTS_FILE
  const doc = asMap(
    parseYAML(
      readBytes(
        path.join(DATA_DIR, 'typical-costs.yaml'),
        'the figures the caps-and-costs guide leads with, as the owner measured them on this project\'s own development runs (the owner\'s brief of 2026-10-19)',
      ),
      file,
    ),
    file,
    'the document',
  )
  refuseUnknownKeys(doc, ['jobs', 'measured_under_cap_usd', 'per_run_cap_since', 'per_run_cap_usd', 'version', 'window'], file)

  if (asInt(doc.version, file, 'version') !== 1) {
    throw new Error(`${file}: version is not 1, and this reader knows version 1 (W1.3 step 2).`)
  }

  const window = asMap(doc.window, file, 'window')
  refuseUnknownKeys(window, ['from', 'runs', 'to'], `${file} window`)
  const runs = asInt(window.runs, file, 'window.runs')
  if (runs < minRuns) {
    throw new Error(
      `${file}: window.runs is ${runs}, below the minimum of ${minRuns} the site publishes a figure from (website/data/site.yaml, D97). The page prints the window's own count beside its figures, so a window thinner than that is not one this site publishes.`,
    )
  }

  // A row's optional figures are read as null rather than defaulted to a number: a page
  // renders the dash the file's absence means, and it never invents a range (Global
  // Constraint 6). `middle_half_usd` is the one shape with two numbers in it, and a list of
  // anything but two is refused here rather than half-read.
  const jobs = asList(doc.jobs, file, 'jobs').map((entry, index) => {
    const job = asMap(entry, file, `jobs[${index}]`)
    refuseUnknownKeys(job, ['id', 'median_usd', 'middle_half_usd', 'name', 'nine_in_ten_under_usd', 'runs', 'worst_usd'], `${file} jobs[${index}]`)
    const what = `jobs[${index}]`
    const id = asString(job.id, file, `${what}.id`)
    const sample = asInt(job.runs, file, `${what}.runs`)
    if (sample < minRuns) {
      throw new Error(
        `${file}: the row "${id}" carries ${sample} runs, below the minimum of ${minRuns} the site publishes a figure from (D97). The page prints the sample size beside every figure, so a row drawn from fewer runs is not one this site publishes.`,
      )
    }

    let middleHalfUSD: { low: number; high: number } | null = null
    if (job.middle_half_usd !== undefined) {
      const half = asList(job.middle_half_usd, file, `${what}.middle_half_usd`).map((value, at) => asCents(value, file, `${what}.middle_half_usd[${at}]`))
      if (half.length !== 2) {
        throw new Error(
          `${file}: ${what}.middle_half_usd carries ${half.length} figures, and the range the middle half of the runs fell in is two: the lower and the upper bound, in that order (W1.3 step 2).`,
        )
      }
      if (half[0] > half[1]) {
        throw new Error(`${file}: ${what}.middle_half_usd is [${half[0]}, ${half[1]}], and the lower bound is above the upper one (W1.3 step 2).`)
      }
      middleHalfUSD = { low: half[0], high: half[1] }
    }

    return {
      id,
      name: asString(job.name, file, `${what}.name`),
      runs: sample,
      medianUSD: asCents(job.median_usd, file, `${what}.median_usd`),
      middleHalfUSD,
      nineInTenUnderUSD: asCents(job.nine_in_ten_under_usd, file, `${what}.nine_in_ten_under_usd`),
      worstUSD: job.worst_usd === undefined ? null : asCents(job.worst_usd, file, `${what}.worst_usd`),
    }
  })
  if (jobs.length === 0) {
    throw new Error(`${file}: jobs is empty, and the caps-and-costs guide leads with one row per kind of run (the owner's brief of 2026-10-19).`)
  }
  const ids = new Set<string>()
  for (const job of jobs) {
    if (ids.has(job.id)) {
      throw new Error(`${file}: two rows carry the id "${job.id}", and the id is what a build error and the built row's own hook name (W1.3 step 2).`)
    }
    ids.add(job.id)
  }

  const perRunCapUSD = asCents(doc.per_run_cap_usd, file, 'per_run_cap_usd')
  if (perRunCapUSD <= 0) {
    throw new Error(`${file}: per_run_cap_usd is ${perRunCapUSD}, and the cap the page prints as the ceiling a looping run meets is above zero (W1.3 step 2).`)
  }
  const measuredUnderCapUSD = asCents(doc.measured_under_cap_usd, file, 'measured_under_cap_usd')
  if (measuredUnderCapUSD <= 0) {
    throw new Error(`${file}: measured_under_cap_usd is ${measuredUnderCapUSD}, and the cap the page draws the bars of its figures against is above zero: a bar scaled by a cap of nothing is a bar whose every mark is at infinity (the owner's feedback of 2026-10-22).`)
  }

  return {
    from: asString(window.from, file, 'window.from'),
    to: asString(window.to, file, 'window.to'),
    runs,
    jobs,
    perRunCapUSD,
    perRunCapSince: asString(doc.per_run_cap_since, file, 'per_run_cap_since'),
    measuredUnderCapUSD,
  }
}

// ---------------------------------------------------------------------------------------
// website/data/{console,home}-screens.yaml — the sliders' slides, and the pictures in them
// (the console's: owner feedback 2026-10-19; the homepage's: the zoom work of 2026-10-21, D246).
//
// **One reader for two files, because the two files are the same document.** The console page's
// slider and the homepage's "See it" panel render the same component from rows of the same
// shape, so the validation is one function and each page names its file and gets its rows. A
// row's `file` is a NAME and its `dir` is the one of the two directories under `public/` that
// serves it — `screenshots` unless the row says otherwise, which is why every console row (and
// every homepage row but the watch capture) carries no `dir` at all.
// ---------------------------------------------------------------------------------------

/**
 * One slide: the file, the two texts a reader gets for it, and its own pixel dimensions.
 */
export interface Screen {
  /** The file's name under `public/<dir>/`, as the data file writes it. */
  file: string
  /** The site path the browser fetches: derived here from `file` and `dir`, written nowhere. */
  src: string
  /** What a reader gets when the picture does not arrive, and what a screen reader reads. */
  alt: string
  /** The line under the picture. */
  caption: string
  /** The file's own width in pixels, so the browser can reserve the box. */
  width: number
  /** The file's own height in pixels. */
  height: number
}

/** One of the two screenshots data files, validated. */
export interface ScreensFacts {
  screens: Screen[]
}

/** Where a screenshot's file lives under `public/`, and the site path it is served at. */
const SCREENS_DIR = 'screenshots'

/**
 * The directories under `public/` a row's `file` may name, and the default. The console's rows
 * are all captures of the console and live in `screenshots/`; the homepage's last slide is the
 * owner's `fugaro watch` capture, which has been at `public/images/fugaro-watch.png` since PR
 * #622 and is not one of the console's four. **A row names one of these two and never a path**:
 * the site path is composed here from the pair, so no data file carries a URL (Global
 * Constraint 9), and a `dir` that is neither is refused by name rather than fetched.
 */
const SCREEN_DIRS = ['images', 'screenshots']

const SCREENS_KEYS = ['screens', 'version']
const SCREEN_KEYS = ['alt', 'caption', 'dir', 'file', 'height', 'width']

/**
 * The formats the slider serves. A console screenshot is a raster image, and these are the
 * ones a browser renders without a second thought: PNG is what the site's own captures are
 * (`public/images/fugaro-watch.png`, `logo.png`), and the three after it are the same
 * picture in fewer bytes — which is the whole of what the budget has to say about a format,
 * since it counts raw bytes and does not care how they were encoded.
 *
 * SVG is deliberately not on the list: it is a document rather than a picture, it can carry
 * script and external references, and a slider of screenshots has no use for one.
 */
const SCREEN_FORMATS = ['avif', 'jpeg', 'jpg', 'png', 'webp']

/**
 * The console's screenshots, in the order the slider shows them.
 *
 * **The four rows this file ships are the owner's own pictures** (the owner's feedback of
 * 2026-10-21: "console page: screenshots missing"): the Live, Run, Spend and Installations
 * screens, captured from the console's build as fixture data and served from
 * `public/screenshots/`, with the `alt` text `public/screenshots/MANIFEST.json` records beside
 * them. **An empty list stays a legal document and not a failure**, which is the one place this
 * reader differs from the install and site readers beside it: no build can make a screenshot
 * (D190), so `screens: []` is read as the state it is — the console page renders no slider at
 * all — and `website/test/console-screens.mjs` is the file that reads both states.
 *
 * Everything else is refused by name: a `file` that is a path or a URL rather than a name
 * under `public/screenshots/` (the site path is derived from it, so a URL here would be a
 * second copy of a fact the build composes — Global Constraint 9), a format the slider does
 * not serve, an `alt` or a `caption` nobody could read, and a size that is not a positive
 * whole number of pixels.
 */
export function loadConsoleScreens(): ScreensFacts {
  return {
    screens: screensOf(
      'console-screens.yaml',
      "the console's screenshots: the files, their alt text, their captions and their sizes (the owner's feedback of 2026-10-19)",
    ),
  }
}

/**
 * The homepage's "See it" slides, in the order the panel's slider shows them (the owner's
 * feedback of 2026-10-21, and the brief's "replace the single Fugaro Watch screenshot with a
 * slider"; D246, D251, D252).
 *
 * **The rows are the console's own three screenshots and then the watch capture**, which is the
 * order the brief asks for — "lead with the desktop console and give it the most space … Fugaro
 * Watch moves to the end as a 'for the terminal crowd' slide" — and the three leading rows'
 * `alt` and `caption` are the `console-screens.yaml` rows' own, verbatim: one picture has one
 * `alt` text and one caption, and a second page that paraphrased them would be a second copy of
 * the sentence that drifts first (`website/test/home.mjs` holds the three to the console file).
 * The last row is `public/images/fugaro-watch.png`, which is the same file the panel has always
 * shown, and its caption is the brief's own example for that slide.
 */
export function loadHomeScreens(): ScreensFacts {
  return {
    screens: screensOf(
      'home-screens.yaml',
      "the homepage slider's slides: the console's screenshots and the `fugaro watch` capture, with their alt text, their captions and their sizes (the brief of 2026-10-21)",
    ),
  }
}

/**
 * The slides of the site's own console slider: the homepage's rows without the `fugaro watch`
 * capture, which is a terminal screen rather than one of the console's, is served from
 * `public/images/` rather than from `public/screenshots/`, and is the homepage hero's picture
 * rather than a slide.
 *
 * **One reader for the five pages that show these rows**, which is the owner's feedback of
 * 2026-10-30 (D412's finding 3): the homepage's "See it" panel and the four campaign pages' own
 * slider show one set of pictures — the same file, the same `alt`, the same caption, the same
 * order — so the set is named once here and a page that wants another one is a change in this
 * function rather than a second filter on a second page. The rule is the directory the row is served
 * from — `screenshots` for the console's captures, `images` for the terminal capture — so it is
 * the row's own shape being read and not a file name written down a second time.
 */
export function loadHomeSliderScreens(): Screen[] {
  return loadHomeScreens().screens.filter((screen) => screen.src.startsWith(`/${SCREENS_DIR}/`))
}

/**
 * The phone app's screenshots, in the order the mobile page's slider shows them (the
 * zoom-diagrams plan, Part 3 item 4; D251).
 *
 * **The list is empty, and that is the document rather than a failure.** The owner supplies
 * the phone captures as files later (D251) and no agent commits a screenshot (D190), so the
 * file ships with no row and the page renders no slider at all: a row added here is a slide,
 * and nothing in the page or the shared component moves for it.
 */
export function loadMobileScreens(): ScreensFacts {
  return {
    screens: screensOf(
      'mobile-screens.yaml',
      "the phone app's screenshots: the files, their alt text, their captions and their sizes (the owner's answer of 2026-10-21, D251)",
    ),
  }
}

/**
 * One screenshots data file, read and validated: the name under `website/data/` and the
 * sentence that says what the file is for, because that sentence is what an unreadable file's
 * error names. Every caller above is this function with a different name, which is what keeps
 * the pages' rows the same shape — and therefore what keeps one component's rendering of them
 * the same.
 */
function screensOf(name: string, why: string): Screen[] {
  const file = `website/data/${name}`
  const doc = asMap(parseYAML(readBytes(path.join(DATA_DIR, name), why), file), file, 'the document')
  refuseUnknownKeys(doc, SCREENS_KEYS, file)

  if (asInt(doc.version, file, 'version') !== 1) {
    throw new Error(`${file}: version is not 1, and this reader knows version 1 (W1.3 step 2).`)
  }

  return asList(doc.screens, file, 'screens').map((entry, index) => {
    const what = `screens[${index}]`
    const screen = asMap(entry, file, what)
    refuseUnknownKeys(screen, SCREEN_KEYS, `${file} ${what}`)

    // The directory the row's file is served from: one of the two under `public/`, and
    // `screenshots` for a row that names none — which is every console row, and the reason a
    // row's shape did not change when the homepage's file arrived.
    const dir = screen.dir === undefined ? SCREENS_DIR : asString(screen.dir, file, `${what}.dir`)
    if (!SCREEN_DIRS.includes(dir)) {
      throw new Error(
        `${file}: ${what}.dir is ${JSON.stringify(dir)}, and a picture this site serves lives in one of ${SCREEN_DIRS.map((one) => `public/${one}/`).join(' or ')}: the site path the browser fetches is composed from the pair, so a directory that is neither is a file no page can reach.`,
      )
    }

    const name = asString(screen.file, file, `${what}.file`)
    // A NAME and not a path: the site path is `/${SCREENS_DIR}/${name}` below, and a name
    // carrying a separator, a scheme or a `..` is a file the derivation would not reach, or
    // a URL written into a data file (Global Constraint 9).
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(name)) {
      throw new Error(
        `${file}: ${what}.file is ${JSON.stringify(name)}, and it is the file's NAME under website/public/${dir}/ — letters, digits, dots, dashes and underscores, with no directory, no scheme and no space. The site path the browser fetches is derived from the name, so a name that is a path or a URL is a fact this file is not allowed to carry (Global Constraint 9).`,
      )
    }
    // The dot is required before the format is read. With no dot at all `lastIndexOf` answers
    // -1 and `slice(0)` hands back the whole name, so a name that IS a served format — `png`,
    // `jpg`, `webp` — would pass a check that only asks whether the extension is one of them,
    // and the row would render a picture at `/<dir>/png`. There is no dot, so there is
    // no extension and no picture either.
    const dot = name.lastIndexOf('.')
    const format = dot === -1 ? '' : name.slice(dot + 1).toLowerCase()
    if (!SCREEN_FORMATS.includes(format)) {
      throw new Error(
        `${file}: ${what}.file is ${JSON.stringify(name)}, and the slider serves ${SCREEN_FORMATS.join(', ')} — the raster formats a browser renders as a picture. An SVG is a document that can carry script and references, and a name with no extension at all is not a picture either.`,
      )
    }

    const alt = asString(screen.alt, file, `${what}.alt`)
    const caption = asString(screen.caption, file, `${what}.caption`)
    if (alt.trim() === '') {
      throw new Error(
        `${file}: ${what}.alt is empty, and every slide carries alt text: it is what a reader gets when the picture does not arrive and what a screen reader reads instead of it, and a screenshot nobody can identify is a picture a reader cannot use.`,
      )
    }
    if (caption.trim() === '') {
      throw new Error(
        `${file}: ${what}.caption is empty, and every slide carries a caption: it is the line under the picture that says what a reader is looking at, and the owner asked for one on each slide.`,
      )
    }

    // Both dimensions, each named on its own: they are the two numbers the browser
    // reserves the picture's box with, so a zero or a negative one is a box that collapses
    // and a page that jumps when the file lands — and the message says which of the two to
    // fix.
    const width = asInt(screen.width, file, `${what}.width`)
    const height = asInt(screen.height, file, `${what}.height`)
    if (width < 1) {
      throw new Error(
        `${file}: ${what}.width is ${width}, and a screenshot's width is the file's own pixels: a positive whole number, which is half of what the browser reserves the box with so the page does not jump when the file lands.`,
      )
    }
    if (height < 1) {
      throw new Error(
        `${file}: ${what}.height is ${height}, and a screenshot's height is the file's own pixels: a positive whole number, which is half of what the browser reserves the box with so the page does not jump when the file lands.`,
      )
    }

    return { file: name, src: `/${dir}/${name}`, alt, caption, width, height }
  })
}

// ---------------------------------------------------------------------------------------
// website/data/mobile-claims.yaml — what the phone app does, and what it does not do yet.
// ---------------------------------------------------------------------------------------

/**
 * One claim about the phone app, and whether the tree backs it today.
 *
 * `shipped` and `planned` are the two states the page renders differently (D240's second
 * condition, D241's answer), and they are the whole of the vocabulary: a third state would be
 * a third way for a reader to be unsure which of the two a bullet is.
 */
export interface MobileClaim {
  /** The claim, as the page renders it. */
  text: string
  /** Whether the tree backs it today (`shipped`) or it is still to be built (`planned`). */
  state: 'shipped' | 'planned'
}

/** website/data/mobile-claims.yaml, validated. */
export interface MobileClaimsFacts {
  claims: MobileClaim[]
}

const MOBILE_CLAIMS_FILE = 'website/data/mobile-claims.yaml'

const MOBILE_CLAIMS_KEYS = ['claims', 'version']
const MOBILE_CLAIM_KEYS = ['state', 'text']

/** The two states a claim may carry, in the order the page renders them. */
export const MOBILE_CLAIM_STATES = ['shipped', 'planned']

/**
 * The phone app's claims, in the order the page renders them (the zoom-diagrams plan, Part 3;
 * D240, D241).
 *
 * **A row is a claim and its state, and the state is required.** The page is a "coming soon"
 * page, and D240 allows one only while every bullet is marked shipped or planned: a row
 * without a state would be a claim the reader cannot place, and a state this reader does not
 * know would be a bullet the page cannot mark at all — so both are refused here, by name,
 * rather than rendered as prose.
 *
 * **An empty list is refused**, which is the one place this reader differs from the
 * screenshots reader beside it. A file with no screenshot is a legal document (no build can
 * make a picture, D190); a file with no claim is a page whose whole job is the claims, and a
 * build that rendered one would be a badge and nothing behind it — the state the brief's
 * third item exists to avoid.
 */
export function loadMobileClaims(): MobileClaimsFacts {
  const file = MOBILE_CLAIMS_FILE
  const doc = asMap(
    parseYAML(readBytes(path.join(DATA_DIR, 'mobile-claims.yaml'), "the phone app's claims: what it does today and what is still to be built, one row each, with the state the page marks it with (the zoom-diagrams plan Part 3; D240, D241)"), file),
    file,
    'the document',
  )
  refuseUnknownKeys(doc, MOBILE_CLAIMS_KEYS, file)
  const claims = asList(doc.claims, file, 'claims').map((entry, index) => {
    const what = `claims[${index}]`
    const claim = asMap(entry, file, what)
    refuseUnknownKeys(claim, MOBILE_CLAIM_KEYS, `${file} ${what}`)

    const state = asString(claim.state, file, `${what}.state`)
    if (!MOBILE_CLAIM_STATES.includes(state)) {
      throw new Error(
        `${file}: ${what}.state is ${JSON.stringify(state)}, and a claim is either ${MOBILE_CLAIM_STATES.map((one) => `\`${one}\``).join(' or ')}: the page renders the two into two lists with their own headings, so a bullet is marked shipped or planned and a reader never has to guess which one it is reading (D240, D241).`,
      )
    }

    const text = asString(claim.text, file, `${what}.text`)
    if (text.trim() === '') {
      throw new Error(
        `${file}: ${what}.text is empty, and every row of this file is a claim: a bullet with no words in it is a mark with nothing behind it, and the page renders this string as the whole of what the row says.`,
      )
    }

    return { text, state: state as MobileClaim['state'] }
  })

  if (claims.length === 0) {
    throw new Error(
      `${file}: claims is empty, and this file is the page: the phone app's page is a "coming soon" badge and the claims under it, so a build with no claim in the file is a page that says nothing about what is coming (D240, D241). A page whose every claim has been dropped is a page to take down, not one to build empty.`,
    )
  }

  return { claims }
}

// website/data/why-gcp.yaml — the claims the "Why GCP" page renders.
// ---------------------------------------------------------------------------------------

/**
 * One claim of the "Why GCP" page: the sentence a reader reads, the document in this
 * repository that proves it, and the day the two were last read against each other.
 */
export interface WhyGcpClaim {
  /** The row's anchor on the page, and the name a build error uses. */
  id: string
  /** The sentence the page renders. */
  text: string
  /** The document the claim is traceable to, relative to the repository root. */
  source: string
  /** The day the claim was last read against that document, `YYYY-MM-DD`. */
  date: string
}

/** website/data/why-gcp.yaml, validated. */
export interface WhyGcpFacts {
  /** The brief's four numbered points, in the order the page's `<ol>` renders them. */
  points: WhyGcpClaim[]
  /** The closing claims — the AWS objection and the second cloud — in the order the page renders them. */
  closing: WhyGcpClaim[]
  /** The page's one-sentence framing of what no other cloud offers (D249). */
  framing: WhyGcpClaim
}

const WHY_GCP_FILE = 'website/data/why-gcp.yaml'

const WHY_GCP_KEYS = ['closing', 'framing', 'points', 'version']
const WHY_GCP_CLAIM_KEYS = ['date', 'id', 'source', 'text']

/**
 * One row of the file: a claim, its citation and its date, all three read and all three
 * non-empty. The three refusals are `faq.astro`'s `loadFaq`'s, for the same reasons — an
 * unknown key is a fact nothing renders, an empty field is a claim the page would print a
 * blank space for, and a `source` that is not on disk is a citation of nothing — and the date
 * is refused unless it is written as a date, because a claim about what another cloud does is
 * one whose age a reader has to be able to judge (ZF58).
 */
function asClaim(value: Value, file: string, what: string): WhyGcpClaim {
  const claim = asMap(value, file, what)
  refuseUnknownKeys(claim, WHY_GCP_CLAIM_KEYS, `${file} ${what}`)

  const id = asString(claim.id, file, `${what}.id`)
  // The row's anchor: the page renders it as the row's `id`, so an id that is not one, or one
  // written twice, is an element a reader cannot jump to or two elements sharing a name.
  if (!/^[a-z][a-z0-9-]*$/.test(id)) {
    throw new Error(
      `${file}: ${what}.id is ${JSON.stringify(id)}, and a row's id is its anchor on the page: lower-case letters, digits and dashes, which is what \`id="…"\` and a link to the row are made of.`,
    )
  }

  const text = asString(claim.text, file, `${what}.text`)
  if (text.trim() === '') {
    throw new Error(
      `${file}: ${what}.text is empty, and every row of this file is a claim the page renders: a row with no words is a blank space where a reader was meant to read the reason Fugaro runs on GCP.`,
    )
  }

  const source = asString(claim.source, file, `${what}.source`)
  if (source.trim() === '') {
    throw new Error(
      `${file}: ${what}.source is empty, and every claim this page renders cites the document that proves it (Global Constraint 6): a claim with no citation is one no guard can hold, and the build stops rather than printing it.`,
    )
  }
  if (!existsSync(path.join(REPO_ROOT, source))) {
    throw new Error(
      `${file}: ${what} names ${JSON.stringify(source)} as the file that proves it, and this repository does not have that file. Every claim is traceable to a document in the tree or it is not written (D102, D243), so the build fails rather than linking a source that is not there.`,
    )
  }

  const date = asString(claim.date, file, `${what}.date`)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) {
    throw new Error(
      `${file}: ${what}.date is ${JSON.stringify(date)}, and every row carries the day its claim was last read against its source, written YYYY-MM-DD: a claim about what another cloud does is one whose age a reader has to be able to judge (docs/website/README.md, ZF58), and a row whose date is written any other way is one no reader can date.`,
    )
  }

  return { id, text, source, date }
}

/** A list of claims, each read by `asClaim` and each named in a build error by its position. */
function asClaims(value: Value, file: string, key: string): WhyGcpClaim[] {
  return asList(value, file, key).map((entry, index) => asClaim(entry, file, `${key}[${index}]`))
}

/**
 * The claims the "Why GCP" page renders: the brief's four numbered points, the two closing
 * claims, and the framing sentence (D243; the brief of 2026-10-21 and the reconciliation's
 * section 4, findings ZF15-ZF22).
 *
 * **The file is the page's only source of these sentences.** Nothing the page renders about
 * GCP is typed into the page: a claim arrives with its citation and its date or it does not
 * arrive, which is what keeps the page's argument checkable against the tree rather than
 * being a paragraph somebody believed. The four points are held to four — the brief numbers
 * them, the page renders them as one `<ol>`, and a fifth row is a point the brief does not
 * make — and the ids are held unique, because each is rendered as an element's `id`.
 */
export function loadWhyGcp(): WhyGcpFacts {
  const file = WHY_GCP_FILE
  const doc = asMap(
    parseYAML(readBytes(path.join(DATA_DIR, 'why-gcp.yaml'), "the Why GCP page's claims: the brief's four points, the AWS objection and the page's framing sentence (the brief of 2026-10-21, D243 and D249)"), file),
    file,
    'the document',
  )
  refuseUnknownKeys(doc, WHY_GCP_KEYS, file)
  const points = asClaims(doc.points, file, 'points')
  if (points.length !== 4) {
    throw new Error(
      `${file}: carries ${points.length} points, and the page renders the brief's four — Firebase as the control plane, a Firebase project being a Google Cloud project, the project being the one place the installation lives, and what Fugaro's own project holds. A point added or removed here is a change to the argument the page makes, so it is made in the brief and in this file together.`,
    )
  }

  const closing = asClaims(doc.closing, file, 'closing')
  const framing = asClaim(doc.framing, file, 'framing')

  const seen = new Set<string>()
  for (const claim of [...points, ...closing, framing]) {
    if (seen.has(claim.id)) {
      throw new Error(
        `${file}: two rows share the id ${JSON.stringify(claim.id)}, and a row's id is rendered as its element's \`id\`: the page would carry \`id="${claim.id}"\` twice, and a link to either row would resolve to the first.`,
      )
    }
    seen.add(claim.id)
  }

  return { points, closing, framing }
}

// ---------------------------------------------------------------------------------------
// website/content/ — the generated documents, and the aggregation's published output.
// ---------------------------------------------------------------------------------------

/** One generated document, with the header it carries read out of the file itself. */
export interface GeneratedDocument {
  /** The file's name under content/, e.g. `security.md`. */
  name: string
  /** The document's path, relative to the repository root: what a message names. */
  path: string
  /** The command that generated it, as its own header names it. */
  generator: string
  /** The file it was generated from, from the header's `source:` line. */
  source: string
  /** The header line, verbatim. */
  header: string
  /** The document below its header. */
  body: string
}

export function loadGeneratedDocument(name: string): GeneratedDocument {
  const file = `website/content/${name}`
  const text = readBytes(path.join(CONTENT_DIR, name), `the generated document ${name} (W0.4 and W0.5 generate the site's documents: the reference with \`go run ./ee/clidoc\`, the security and subscription-terms pages with \`go run ./internal/clidoc\`)`)
  const lines = text.split('\n')

  const header = lines[0] ?? ''
  const m = GENERATED_HEADER.exec(header)
  if (!m) {
    throw new Error(
      `${file} does not say on its first line that it is generated: every file under content/ is generated, its first line names the command that regenerates it, and a file without that line is one somebody edited by hand — the copy that drifts first (W1.3 step 2). Its first line is ${JSON.stringify(header)}.`,
    )
  }

  // The `source:` line, when the generator wrote one, is the header's second line and is
  // part of the header rather than of the document: it is dropped here and kept as a fact.
  const sourceMatch = SOURCE_HEADER.exec(lines[1] ?? '')
  const body = lines
    .slice(sourceMatch ? 2 : 1)
    .join('\n')
    .trim()
  if (body === '') {
    throw new Error(`${file} carries its header and nothing below it: a generated document with no body renders an empty page, and the build fails rather than rendering one (W1.3 step 2).`)
  }

  return { name, path: file, generator: m[1].trim(), source: sourceMatch ? sourceMatch[1].trim() : m[1].trim(), header, body }
}

/** Every generated document under content/, keyed by file name. */
function loadGeneratedDocuments(): { [name: string]: GeneratedDocument } {
  if (!existsSync(CONTENT_DIR)) {
    throw new Error(
      'website/content/ is missing: it is where Part 0 writes the site\'s generated documents (W0.4\'s CLI reference and W0.5\'s security page), and the build fails rather than rendering pages without them (W1.3 step 2).',
    )
  }

  const names = readdirSync(CONTENT_DIR).filter((name) => name.endsWith('.md')).sort()
  const documents: { [name: string]: GeneratedDocument } = {}
  for (const name of names) documents[name] = loadGeneratedDocument(name)

  for (const name of REQUIRED_DOCUMENTS) {
    if (!(name in documents)) {
      throw new Error(
        `website/content/${name} is missing: the site renders it, so the build fails here rather than on the page that would have rendered nothing (W1.3 step 2).`,
      )
    }
  }
  return documents
}

/** One measure's published figures: min, median and p90, and never a mean (D97). */
export interface Percentiles {
  min: number
  median: number
  p90: number
}

/** One recipe's published figures, as W0.3's `--json` prints them. */
export interface CostsGroup {
  recipe: string
  runs: number
  computeNotEstimated: number
  durationS: Percentiles
  costUSD: Percentiles
}

/** website/content/costs.json, validated, or null while the owner has not run it. */
export interface CostsFacts {
  project: string
  since: string
  until: string
  minRuns: number
  groups: CostsGroup[]
  totals: { [key: string]: number }
  notAPromise: string
}

const COSTS_FILE = 'website/content/costs.json'

function loadCosts(minRuns: number): CostsFacts | null {
  const file = COSTS_FILE
  const absolute = path.join(CONTENT_DIR, 'costs.json')
  if (!existsSync(absolute)) {
    // The honest empty state rather than a failure: the aggregation is owner-run, and no
    // page renders these figures while it has not been run (W1.5, D96 — the ranges band
    // that read them was removed on 2026-10-21).
    return null
  }

  const raw = readBytes(absolute, 'the aggregation\'s published output, written by the owner-run `fugaro stats runs --project <installation> --json` (W0.3)')
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch (err) {
    throw new Error(`${file} is not JSON: ${(err as Error).message} (W1.3 step 2).`)
  }
  const doc = asMap(parsed as Value, file, 'the document')

  // Seconds and dollars are fractional (internal/stats.Percentiles carries float64), so
  // these are numbers and not counts. A percentile the aggregation did not have is a
  // missing figure, not a zero, so all three keys are required.
  const percentiles = (value: Value, what: string): Percentiles => {
    const map = asMap(value, file, what)
    return {
      min: asNumber(map.min, file, `${what}.min`),
      median: asNumber(map.median, file, `${what}.median`),
      p90: asNumber(map.p90, file, `${what}.p90`),
    }
  }

  const groups = asList(doc.recipes, file, 'recipes').map((entry, index) => {
    const group = asMap(entry, file, `recipes[${index}]`)
    const runs = asInt(group.runs, file, `recipes[${index}].runs`)
    const recipe = asString(group.recipe, file, `recipes[${index}].recipe`)
    if (runs < minRuns) {
      throw new Error(
        `${file}: the group "${recipe}" carries ${runs} runs, below the minimum of ${minRuns} the site publishes a figure from. The command suppresses a group below the threshold (W0.3, D97); a group that reached this file anyway is not published, and the site's own facts fix the threshold in website/data/site.yaml (W1.3 step 2).`,
      )
    }
    return {
      recipe,
      runs,
      computeNotEstimated: asInt(group.compute_not_estimated, file, `recipes[${index}].compute_not_estimated`),
      durationS: percentiles(group.duration_s, `recipes[${index}].duration_s`),
      costUSD: percentiles(group.cost_usd, `recipes[${index}].cost_usd`),
    }
  })

  const fileMinRuns = asInt(doc.min_runs, file, 'min_runs')
  if (fileMinRuns !== minRuns) {
    throw new Error(
      `${file}: min_runs is ${fileMinRuns} and website/data/site.yaml says ${minRuns}. The threshold is one number: the site publishes no figure drawn from fewer runs than the aggregation's own minimum, and the two cannot disagree (D97, W1.3 step 2).`,
    )
  }

  const totals = asMap(doc.totals, file, 'totals')
  const numbers: { [key: string]: number } = {}
  for (const key of keysOf(totals)) numbers[key] = asInt(totals[key], file, `totals.${key}`)

  return {
    project: asString(doc.project, file, 'project'),
    since: asString(doc.since, file, 'since'),
    until: asString(doc.until, file, 'until'),
    minRuns: fileMinRuns,
    groups,
    totals: numbers,
    notAPromise: asString(doc.not_a_promise, file, 'not_a_promise'),
  }
}

// ---------------------------------------------------------------------------------------
// The rest of what a page needs, each read from the one place it is written down.
// ---------------------------------------------------------------------------------------

/**
 * The two lines the site's promise is written in, read from the one place they are
 * written: **README.md's own opening**, under its `# Fugaro` title.
 *
 *   - the **headline** is the README's tagline line — the first non-empty line under the
 *     title, the bold one — and it is the hero's `h1` (F48, D102);
 *   - the **subline** is the line under it: one sentence saying what the product is, which
 *     is the hero's lead, the site's default page description and the `og:description` a
 *     shared link shows — the card included, since the head emits one `twitter:card` and no
 *     second description tag, so the card reads the OpenGraph one (the changes spec of
 *     2026-10-18, item 2; `website/test/home.mjs` holds both to the README's line and
 *     asserts there is no `twitter:description`).
 *
 * Both are read rather than copied into a component, because a promise typed into a
 * template is a second copy of the README's line and the one that drifts — and because
 * every other sentence the site prints has to be traceable to a document or a command,
 * starting with these. Both also have to be there: a README whose title is followed by one
 * line, or by none, stops the build naming the file rather than rendering a page with a
 * hole in its first screen.
 *
 * The same goes for the *shape* of the two lines, and it is checked rather than assumed:
 * the README is markdown and what a page renders is text, so a promise that is a line of
 * markdown *structure* — a heading, a list item, a quote, a table row, a rule or a fence,
 * an image or a raw tag — would print its own markers on the first screen. Either line
 * being one stops the build naming the file, which is the behaviour
 * `website/test/home.mjs` holds to a case (a README that puts its promise under the title
 * as `## Install` fails the build rather than rendering "## Install" under the headline).
 *
 * The markdown emphasis is not part of either sentence, so the `**` that makes the tagline
 * bold in the README is stripped: what a page renders is the text a reader reads. The
 * subline is the whole of the line: it is one sentence, and a second one on the same line
 * would be rendered whole, so the README's own line breaks are what fixes where it ends.
 */
function loadPromiseLines(): { tagline: string; subline: string } {
  const file = 'README.md'
  const lines = readBytes(
    path.join(REPO_ROOT, 'README.md'),
    'the tagline and the subline: the two lines under its `# Fugaro` title, which the site renders rather than restating (F48, D102; changes spec item 2)',
  ).split('\n')

  const title = lines.findIndex((line) => line.trim() === '# Fugaro')
  if (title === -1) {
    throw new Error(`${file}: no "# Fugaro" title, and the site's tagline and subline are the two lines under it (F48, D102, W1.3 step 1).`)
  }

  const unemphasized = (line: string) => line.trim().replace(/^\*\*(.+)\*\*$/, '$1').trim()
  // Markdown that organises a document rather than saying something. Both lines are rendered
  // as plain text, so one of these would print its own markers on the first screen.
  const STRUCTURE = /^(?:#{1,6}\s|[-*+]\s|\d+[.)]\s|>\s?|\||`{3,}|<|={3,}|-{3,}$)/
  const under = lines.slice(title + 1).filter((candidate) => candidate.trim() !== '')
  if (under.length < 2) {
    throw new Error(
      `${file}: the "# Fugaro" title is followed by ${under.length} non-empty line(s), and the site renders the two under it: the tagline (the hero's headline) and the subline (the promise under it, which is the site's page description). Add the missing line, or this build has no promise to render (changes spec item 2, F48).`,
    )
  }

  const tagline = unemphasized(under[0])
  const subline = unemphasized(under[1])
  if (tagline === '' || subline === '') {
    throw new Error(`${file}: one of the two lines under the "# Fugaro" title is empty, and the site renders both (F48, D102; changes spec item 2).`)
  }
  for (const [what, line] of [
    ['tagline', tagline],
    ['subline', subline],
  ] as const) {
    if (STRUCTURE.test(line)) {
      throw new Error(
        `${file}: the ${what} is ${JSON.stringify(line)}, which is markdown structure — a heading, a list item, a quote, a table row, a rule or fence, an image or a tag — rather than the sentence the site renders as plain text: the markers would be printed on the first screen, where the promise is. The two lines are the README's opening under its "# Fugaro" title: one line saying what the product is, and the sentence under it (changes spec item 2, F48, D102).`,
      )
    }
  }
  return { tagline, subline }
}

/** README.md's tagline line: the site's headline, the hero's `h1` (F48, D102). */
export function loadTagline(): string {
  return loadPromiseLines().tagline
}

/**
 * README.md's subline: the one sentence under the tagline, rendered as the hero's lead and
 * as the site's default page description (changes spec item 2). It is provider-neutral on
 * purpose — "cloud jobs", not a named cloud — because where a run happens is the
 * architecture's business and is explained further down the page.
 */
export function loadSubline(): string {
  return loadPromiseLines().subline
}

/**
 * The self-hosted fonts the artifact ships, read from `public/fonts/` — the directory
 * W1.1's README names — so the head preloads a font this build produces and nothing else
 * (D106). A font that is not in the artifact is not preloaded: a preload is a request a
 * reader's browser makes before it has parsed anything, and pointing one at a file that
 * does not exist is a request for nothing.
 */
export function loadFonts(): { href: string; type: string }[] {
  const dir = path.join(PUBLIC_DIR, 'fonts')
  if (!existsSync(dir)) return []
  const types: { [extension: string]: string } = {
    '.woff2': 'font/woff2',
    '.woff': 'font/woff',
    '.ttf': 'font/ttf',
    '.otf': 'font/otf',
  }
  return readdirSync(dir)
    .filter((name) => path.extname(name) in types)
    .sort()
    .map((name) => ({ href: `/fonts/${name}`, type: types[path.extname(name)] }))
}

/**
 * The sitemap's URL, or null while the build does not ship one.
 *
 * A `<link rel="sitemap">` is a crawler's hint and not a visible link, but it is still a
 * pointer at a file, and the site's rule about pointers is the one Global Constraint 9
 * states for downloads: a page names an artifact that exists. The sitemap arrives with
 * the integration that produces it — `@astrojs/sitemap`, declared in astro.config.mjs,
 * which no task adds yet (W2.2 set the canonical URL the sitemap would be built from and
 * added no integration) — so the link is emitted from the canonical origin the moment
 * that line is there, and not before.
 */
export function loadSitemapHref(canonicalURL: string): string | null {
  const config = path.join(SITE_ROOT, 'astro.config.mjs')
  if (!existsSync(config)) return null
  // The config is a module, so it is imported (in a child process, because this loader is
  // synchronous) and its declared integrations are read: a comment or a string that
  // merely names the package is not an integration, and must not make the head point at a
  // sitemap the build does not produce.
  const script = `import(${JSON.stringify(pathToFileURL(config).href)}).then((mod) => process.stdout.write(JSON.stringify((mod.default.integrations ?? []).flat().map((entry) => entry && entry.name))))`
  const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], { cwd: SITE_ROOT, encoding: 'utf8' })
  if (result.status !== 0) {
    throw new Error(`website/astro.config.mjs could not be loaded to read its integrations: ${result.stderr || result.error}`)
  }
  if (!(JSON.parse(result.stdout) as unknown[]).includes('@astrojs/sitemap')) return null
  return new URL('/sitemap-index.xml', canonicalURL).href
}

/** Everything a page renders from, read once per build. */
export interface Site {
  facts: SiteFacts
  install: InstallFacts
  /** README.md's tagline line: the site's headline, the hero's `h1`. */
  tagline: string
  /** README.md's subline: the hero's lead and the site's default page description. */
  subline: string
  /** The generated documents under content/, keyed by file name. */
  documents: { [name: string]: GeneratedDocument }
  /** What a job costs, as the owner measured it on this project's own development runs. */
  typicalCosts: TypicalCostsFacts
  /** The aggregation's published figures, or null while the owner has not run it. */
  costs: CostsFacts | null
  fonts: { href: string; type: string }[]
  sitemapHref: string | null
}

let loaded: Site | null = null

/**
 * The whole pipeline: the site's facts, the install facts, the generated documents, the
 * aggregation's figures, the tagline and the subline, the fonts and the sitemap — each read from its own
 * source, each validated, and any one of them missing or malformed a build error naming
 * the file.
 *
 * Every page renders through the base layout, and the base layout calls this, so the
 * whole generated layer is checked on every build that renders a page. The result is
 * memoized: a build of forty pages reads the same facts once.
 */
export function loadSite(): Site {
  if (loaded !== null) return loaded
  const facts = loadSiteFacts()
  loaded = {
    facts,
    install: loadInstallFacts(),
    tagline: loadTagline(),
    subline: loadSubline(),
    documents: loadGeneratedDocuments(),
    typicalCosts: loadTypicalCosts(facts.minRuns),
    costs: loadCosts(facts.minRuns),
    fonts: loadFonts(),
    sitemapHref: loadSitemapHref(facts.canonicalURL),
  }
  return loaded
}
