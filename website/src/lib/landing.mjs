// website/src/lib/landing.mjs — the four landing variants, and the guard that refuses a
// published figure nothing backs (go-to-market plan, Task G2.1; reconciliation F6, F12, F14,
// F20, F22).
//
// `website/data/landing.yaml` is the only place a campaign variant's copy is written: its path,
// headline, supporting line, tone, proof number and WhatsApp pre-fill message. G2.2 builds the
// four pages from what this module returns and G2.3 puts the marketing consent around them; both
// read this file rather than typing a variant into a component.
//
// It is a plain module (`consent-key.mjs`, `changelog.mjs`), so the test that holds the file to
// its rules imports it directly under `node --test`. It reads the site's own facts through
// `content.ts` — `loadSite()`, which carries `site.yaml`'s `min_runs` and the entries of
// `typical-costs.yaml` read with it — so the threshold of a published figure stays one number
// (D96, D97) and no second copy of it exists here.
//
// Three rules, and every one of them is a refusal rather than a default:
//
//   1. **A proof number is backed or it is the sentinel.** A variant's figure must name an entry
//      of the owner's own measurement of this project's runs, with a sample at or above D97's
//      minimum, or be `unmeasured`. A figure typed in from the agenda is the defect this guard
//      exists for (F6).
//   2. **Media is self-hosted.** A variant's video and image are paths the site serves, never an
//      absolute URL: an external player would be a fourth third party the consent work never
//      admitted (F20, D107).
//   3. **The four variants are the spec's four campaigns**, at the spec's own paths, and the
//      pre-fill message names the variant its page is (spec §6). A fifth path is a page no
//      campaign sends anyone to.
//
// Nothing here fetches, resolves a host or reads a clock, and no npm package is added for the
// site's benefit: the YAML reader below is the site's own, as `content.ts`'s is (Global
// Constraint 5). It understands the subset `website/data/landing.yaml` uses — nested maps,
// sequences of maps, flow sequences, quoted scalars and comments — and refuses everything else
// with a line number, never a guess.

import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'

import { loadSite, SITE_ROOT } from '../content.ts'

/** Where the file lives, relative to the repository root: what every message below names. */
export const LANDING_FILE = 'website/data/landing.yaml'

// The four campaigns of `docs/go-to-market/spec.md` §6, in the order that section writes them.
// Each is a path and a name at once — the paths are "short, message-based, no `/landing` prefix"
// — so this list is the whole of which pages exist, and a document that carries a fifth path or
// drops one is refused rather than rendered.
const CAMPAIGNS = ['save', 'control', 'scale', 'crew']

// The sentinel: a variant whose figure is not published yet. It is spelled once, and the loader
// refuses it half-applied — a sentinel value with a source, or a figure with a sentinel source.
const SENTINEL = 'unmeasured'

// `website/data/typical-costs.yaml`, named in the messages a proof number's refusal prints:
// the file a figure has to be drawn from, which `loadSite()` has already read and validated.
const TYPICAL_COSTS = 'website/data/typical-costs.yaml'

// The pre-fill message the spec writes, with the variant's own name in it (spec §6: "Hi Dimi, I
// saw Fugaro on the save page."). The name is the only thing that varies, and it is what makes
// the visitor's message say which campaign brought them in. It also keeps the file's promise
// that the one person it names is the owner's own first name (F12).
const WHATSAPP_TEXT = /^Hi Dimi, I saw Fugaro on the (save|control|scale|crew) page\.$/

// The keys each level of the document may carry. A key nobody reads is a failure here rather
// than a value no page and no test would ever see, exactly as `content.ts` reads site.yaml.
const DOCUMENT_KEYS = ['calendly_url', 'partner_logos', 'variants', 'version', 'whatsapp_number']
const VARIANT_KEYS = ['headline', 'image', 'path', 'proof_number', 'supporting_line', 'tone', 'video', 'whatsapp_text']
const PROOF_KEYS = ['source', 'value']
const LOGO_KEYS = ['image', 'name']

/** A tone, a path's name and a logo's name: words the page's look and the site's routes key off,
 * so none of them may carry punctuation, a slash or a space that would become a selector or a
 * second route segment. */
const WORD = /^[a-z][a-z0-9-]*$/

// ---------------------------------------------------------------------------------------
// A small YAML reader, for the subset this file uses.
//
// The site's facts are YAML because a fact file a person edits should be readable and
// commentable, and this reader is the site's own for the same reason `content.ts`'s is: no npm
// package is added for the site's benefit. The subset is nested maps, sequences of maps, flow
// sequences, quoted scalars, plain scalars and comments; a construct outside it — a block
// scalar, an anchor, a tag, a flow mapping — is an error naming its line, never a value that
// parses into something else.
// ---------------------------------------------------------------------------------------

/**
 * The file's lines, each with the number a refusal names and the indentation the shapes below
 * are read from.
 *
 * @param {string} raw
 * @returns {{ number: number, indent: number, text: string, blank: boolean }[]}
 */
function splitLines(raw) {
  return raw.split('\n').map((line, index) => {
    const text = line.trim()
    return { number: index + 1, indent: line.length - line.trimStart().length, text, blank: text === '' || text.startsWith('#') }
  })
}

// A mapping line: `key: value`, where the colon is followed by whitespace or ends the line. The
// second half of the rule is what keeps a URL's own `//` out of the key, and a quoted value's
// `: ` (which this file's supporting lines carry) out of it too.
const MAPPING_LINE = /^([^:#\s][^:#]*):(\s.*|)$/

// A block scalar's indicator. The site's own reader understands `|` and `>`; this file uses
// neither — every string here is one line — so one is refused rather than half-read.
const BLOCK_SCALAR = /^[|>][-+]?\d*$/

/**
 * The document a file's bytes carry.
 *
 * @param {string} raw
 * @param {string} file
 * @returns {*}
 */
function parseYAML(raw, file) {
  const lines = splitLines(raw)
  let i = 0

  /** @param {string} message @param {{ number: number } | undefined} [at] */
  const fail = (message, at) => {
    throw new Error(
      `${file}${at === undefined ? '' : `:${at.number}`}: ${message}. The landing file's reader understands the subset website/data/landing.yaml uses — nested maps, sequences of maps, flow sequences, quoted scalars and comments — and refuses anything else rather than guessing at it (W1.3 step 2).`,
    )
  }

  const skip = () => {
    while (i < lines.length && lines[i].blank) i++
  }

  /** @param {string} text @param {{ number: number }} at */
  const scalar = (text, at) => {
    // Quoted scalars first: quoting is how a value carries a `#`, a `: ` or a number's shape
    // that would otherwise be a comment, a nested mapping or an integer.
    if (text.startsWith('"') || text.startsWith("'")) {
      if (text.length < 2 || !text.endsWith(text[0])) fail(`the quoted scalar ${JSON.stringify(text)} does not close on this line`, at)
      const inner = text.slice(1, -1)
      return text[0] === '"' ? inner.replace(/\\(["\\])/g, '$1') : inner.replaceAll("''", "'")
    }
    if (text.startsWith('[')) {
      if (!text.endsWith(']')) fail(`the flow sequence ${JSON.stringify(text)} does not close on this line`, at)
      const inner = text.slice(1, -1).trim()
      return inner === '' ? [] : inner.split(',').map((part) => scalar(part.trim(), at))
    }
    // The constructs this reader has not been taught, refused by name rather than read as the
    // string they look like: an alias, an anchor, a tag and a flow mapping are YAML's own
    // references and shapes, and taken for text each of them is a fact that renders as `*x` or
    // `{a: 1}` on a page (the contract is "refuse", not "guess").
    if (/^[*&!{}]/.test(text)) fail(`the value ${JSON.stringify(text)} is a YAML alias, anchor, tag or flow mapping, and this reader reads none of them`, at)
    if (BLOCK_SCALAR.test(text)) fail(`the value ${JSON.stringify(text)} is a block scalar, and this file writes every string on one line`, at)
    // A plain scalar cannot carry a nested mapping: `key: a: b` is a YAML error, and this reader
    // refuses it rather than reading the value as the string "a: b".
    if (/:\s/.test(text)) fail(`the value ${JSON.stringify(text)} carries ": ", which makes it a mapping rather than a value; quote it if it is meant as text`, at)
    if (text === 'true') return true
    if (text === 'false') return false
    if (text === 'null' || text === '~') return null
    if (/^-?\d+$/.test(text)) return Number(text)
    return text
  }

  // The value written under a key: the block of lines below it, which is a sequence of items or
  // a map. The caller has stepped past the key's line, so the first element's line is read here.
  /** @param {number} parentIndent */
  const block = (parentIndent) => {
    skip()
    const line = lines[i]
    if (line === undefined || line.indent <= parentIndent) fail('the key above carries no value: what it reads as is written under it, indented further', line)
    if (line.text === '-' || line.text.startsWith('- ')) return sequence(line.indent)
    const entry = MAPPING_LINE.exec(line.text)
    if (entry === null) fail(`the line ${JSON.stringify(line.text)} is neither a "key: value" pair nor a sequence item`, line)
    i++
    return mapping(line.indent, { key: entry[1].trim(), rest: entry[2].trim(), at: line })
  }

  /** @param {number} indent @param {{ key: string, rest: string, at: { number: number } }} first */
  const mapping = (indent, first) => {
    const out = {}
    let entry = first
    for (;;) {
      if (Object.hasOwn(out, entry.key)) fail(`the key "${entry.key}" is written twice`, entry.at)
      if (entry.rest === '') out[entry.key] = block(indent)
      else out[entry.key] = scalar(entry.rest, entry.at)
      skip()
      const line = lines[i]
      if (line === undefined || line.indent < indent) return out
      if (line.indent > indent) fail('this line is indented further than the key above it, so what it belongs to cannot be read', line)
      const next = MAPPING_LINE.exec(line.text)
      if (next === null) fail(`the line ${JSON.stringify(line.text)} is not a "key: value" pair, which is what a map is read from`, line)
      i++
      entry = { key: next[1].trim(), rest: next[2].trim(), at: line }
    }
  }

  /** @param {number} indent */
  const sequence = (indent) => {
    const out = []
    for (;;) {
      skip()
      const line = lines[i]
      if (line === undefined || line.indent !== indent || !(line.text === '-' || line.text.startsWith('- '))) return out
      const rest = line.text === '-' ? '' : line.text.slice(2).trim()
      // An item that opens a map on its own line: `- path: /save`, with the item's remaining
      // keys on the lines below, indented past the dash.
      const entry = rest === '' ? null : MAPPING_LINE.exec(rest)
      if (entry !== null) {
        i++
        out.push(mapping(indent + 2, { key: entry[1].trim(), rest: entry[2].trim(), at: line }))
        continue
      }
      if (BLOCK_SCALAR.test(rest)) fail(`the sequence item ${JSON.stringify(rest)} is a block scalar, and this file writes every string on one line`, line)
      i++
      out.push(scalar(rest, line))
    }
  }

  skip()
  if (i >= lines.length) fail('the file carries no document')
  const document = block(-1)
  skip()
  if (i < lines.length) fail('there is content after the document', lines[i])
  return document
}

// ---------------------------------------------------------------------------------------
// The validators. Every one names the file and the key it refused, because a build error is
// read by whoever just edited the data file.
// ---------------------------------------------------------------------------------------

// What a key that is not there reads as, in the messages below: "not written in the file"
// rather than the word `undefined`, because the reader of a build error is looking at the file.
/** @param {*} value */
function describe(value) {
  return value === undefined ? 'not written in the file' : JSON.stringify(value)
}

/** @param {*} value @param {string} file @param {string} what */
function asMap(value, file, what) {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as a map of keys to values, so nothing below it can be read (G2.1).`)
  }
  return value
}

/** @param {*} value @param {string} file @param {string} what */
function asList(value, file, what) {
  if (!Array.isArray(value)) {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as a list, so nothing below it can be read (G2.1).`)
  }
  return value
}

/** @param {*} value @param {string} file @param {string} what */
function asString(value, file, what) {
  if (typeof value !== 'string') {
    throw new Error(`${file}: ${what} is ${describe(value)}, and it is read as a string — every fact a page renders is written down as text, and a number like the WhatsApp number is quoted so its shape is not what carries it (G2.1).`)
  }
  return value
}

/** @param {{ [key: string]: * }} map @param {string[]} allowed @param {string} file @param {string} what */
function refuseUnknownKeys(map, allowed, file, what) {
  for (const key of Object.keys(map).sort()) {
    if (!allowed.includes(key)) {
      throw new Error(
        `${file}: the key "${key}" is not one of ${what}, which are ${allowed.join(', ')}. A fact nobody reads is a fact nothing pins, so a new key is added with the page that renders it (G2.1).`,
      )
    }
  }
}

/**
 * A media path the site serves: `/videos/save.mp4`, the file under `website/public/`. An
 * absolute URL is what this refuses — an external player or image host would be a third party
 * the marketing consent never admitted (F20, D107) — and so is a path that climbs out of the
 * site's own directory. The file itself may be absent: the owner produces the campaign media,
 * and the pages build without it (F14, G2.5).
 *
 * @param {*} value @param {string} file @param {string} what
 */
function selfHosted(value, file, what) {
  const text = asString(value, file, what)
  if (!/^\/(?!\/)[A-Za-z0-9._~\-/]*$/.test(text) || text.split('/').includes('..')) {
    throw new Error(
      `${file}: ${what} is ${JSON.stringify(text)}, and a variant's media is a self-hosted path under website/public/ — a file this site serves, never an absolute URL. An external player or image host is a fourth third party the marketing consent (G2.3, D401) never admitted, and the page builds without the file until the owner produces it (F14, F20, D107).`,
    )
  }
  return text
}

/**
 * The WhatsApp link, built from the number and the variant's own pre-fill message: the file
 * writes both and the link is derived, so the number lives in one place and a pasted `wa.me`
 * URL (which would carry the number again) cannot drift from it.
 *
 * @param {string} number @param {string} text
 */
function whatsappLink(number, text) {
  return `https://wa.me/${number}?text=${encodeURIComponent(text)}`
}

// ---------------------------------------------------------------------------------------
// The claims guard, the media rule and the campaign list.
// ---------------------------------------------------------------------------------------

/**
 * A variant's proof number: the figure the band prints in big type, with the entry of the
 * owner's measurement that backs it, or the sentinel.
 *
 * This is the guard of F6: a published figure is never typed into a page, so a value that is not
 * the sentinel must name an entry of `website/data/typical-costs.yaml` — read through the
 * `loadTypicalCosts(minRuns)` that `loadSite()` used, so the minimum sample is site.yaml's one
 * number and not a second copy — and that entry's own sample must be at or above it.
 *
 * @param {*} value @param {string} file @param {string} what
 * @param {{ facts: { minRuns: number }, typicalCosts: { jobs: { id: string, runs: number }[] } }} site
 */
function proofNumber(value, file, what, site) {
  const proof = asMap(value, file, what)
  refuseUnknownKeys(proof, PROOF_KEYS, file, `${what}'s keys`)
  const figure = asString(proof.value, file, `${what}.value`)
  const source = asString(proof.source, file, `${what}.source`)

  if (figure === SENTINEL) {
    if (source !== SENTINEL) {
      throw new Error(
        `${file}: ${what}.value is "${SENTINEL}" and its source is "${source}". The two move together: a variant with no figure carries the sentinel in both, and a source beside the sentinel reads as a figure that was measured (F6).`,
      )
    }
    return { value: figure, source }
  }

  if (!/\d/.test(figure)) {
    throw new Error(
      `${file}: ${what}.value is ${JSON.stringify(figure)}, and a proof number is a figure — the one the band prints in big type. A variant whose proof is a sentence rather than a number carries the sentinel "${SENTINEL}" instead (F6).`,
    )
  }
  if (source === SENTINEL) {
    throw new Error(
      `${file}: ${what} carries the figure ${JSON.stringify(figure)} with "${SENTINEL}" in place of its source. A published figure is never typed into a page: it comes from the variant config, whose source names the entry of ${TYPICAL_COSTS} it was drawn from, and the loader refuses a figure nothing backs (F6).`,
    )
  }

  const ids = site.typicalCosts.jobs.map((job) => job.id)
  const row = site.typicalCosts.jobs.find((job) => job.id === source)
  if (row === undefined) {
    throw new Error(
      `${file}: ${what}.source names "${source}", which ${TYPICAL_COSTS} does not carry: its entries are ${ids.join(', ')}. A figure is drawn from a measurement this repository holds, and the entry's own id is how it is named here (F6, D96).`,
    )
  }
  if (row.runs < site.facts.minRuns) {
    throw new Error(
      `${file}: ${what}.source names "${source}", whose sample is ${row.runs} runs, below the minimum of ${site.facts.minRuns} this site publishes a figure from (website/data/site.yaml, D97). The threshold reaches this file through the same loadTypicalCosts() the caps-and-costs guide reads, so it is one number and not a second copy (F6).`,
    )
  }
  return { value: figure, source }
}

/**
 * One variant of the four, validated.
 *
 * @param {*} value @param {number} index @param {string} file
 * @param {{ facts: { minRuns: number }, typicalCosts: { jobs: { id: string, runs: number }[] } }} site
 */
function variant(value, index, file, site) {
  const what = `variants[${index}]`
  const entry = asMap(value, file, what)
  refuseUnknownKeys(entry, VARIANT_KEYS, file, `${what}'s keys`)

  const route = asString(entry.path, file, `${what}.path`)
  const name = route.replace(/^\//, '')
  if (!/^\/[a-z][a-z0-9-]*$/.test(route) || !CAMPAIGNS.includes(name)) {
    throw new Error(
      `${file}: ${what}.path is ${JSON.stringify(route)}, and the paths are the four message names the spec fixes — /save, /control, /scale and /crew, short and with no /landing prefix (spec §6). A page is built at a path in this list and at no other, so a fifth path is a page no campaign sends anyone to.`,
    )
  }

  const tone = asString(entry.tone, file, `${what}.tone`)
  if (!WORD.test(tone)) {
    throw new Error(`${file}: ${what}.tone is ${JSON.stringify(tone)}, and a tone is a lowercase word the page's look keys off a class name (spec §6 gives /crew "lighter tone").`)
  }

  const text = asString(entry.whatsapp_text, file, `${what}.whatsapp_text`)
  const named = WHATSAPP_TEXT.exec(text)
  if (named === null || named[1] !== name) {
    throw new Error(
      `${file}: ${what}.whatsapp_text is ${JSON.stringify(text)}, and the pre-fill message names the page the visitor came from: "Hi Dimi, I saw Fugaro on the ${name} page." for this variant (spec §6). It is the whole of why the message is per variant, and the one person it may name is the owner's own first name (F12).`,
    )
  }

  return {
    name,
    path: route,
    headline: asString(entry.headline, file, `${what}.headline`),
    supportingLine: asString(entry.supporting_line, file, `${what}.supporting_line`),
    tone,
    proofNumber: proofNumber(entry.proof_number, file, `${what}.proof_number`, site),
    whatsappText: text,
    video: entry.video === undefined ? null : selfHosted(entry.video, file, `${what}.video`),
    image: entry.image === undefined ? null : selfHosted(entry.image, file, `${what}.image`),
  }
}

/**
 * One design partner's logo, each with the partner's written permission — which is why the
 * shipped list is empty and the social-proof section stays hidden (F11, F12, D405).
 *
 * @param {*} value @param {number} index @param {string} file
 */
function partnerLogo(value, index, file) {
  const what = `partner_logos[${index}]`
  const entry = asMap(value, file, what)
  refuseUnknownKeys(entry, LOGO_KEYS, file, `${what}'s keys`)
  return { name: asString(entry.name, file, `${what}.name`), image: selfHosted(entry.image, file, `${what}.image`) }
}

/**
 * The booking link: the owner's Calendly page, over https, on Calendly's own host, with no query
 * of its own — the UTMs a page may pass through are added at the page (F22, G2.4), and G2.3
 * admits this host to the CSP while G2.2 renders it as a plain link for a visitor who declined
 * the marketing consent.
 *
 * @param {*} value @param {string} file
 */
function bookingLink(value, file) {
  const text = asString(value, file, 'calendly_url')
  let url
  try {
    url = new URL(text)
  } catch {
    throw new Error(`${file}: calendly_url is ${JSON.stringify(text)}, which is not an absolute URL: the Book a demo link is an https URL on Calendly's own host (R3).`)
  }
  if (url.protocol !== 'https:') {
    throw new Error(`${file}: calendly_url is ${text}, and the page links an https URL: a booking link over http is one a browser refuses to open from an https page, and the demo link is the campaign's primary call to action (R3).`)
  }
  if (url.host !== 'calendly.com') {
    throw new Error(`${file}: calendly_url is on ${url.host}, and the demo link is Calendly's own: calendly.com, which G2.3 admits to the CSP and G2.2 renders (R3, D401).`)
  }
  if (url.search !== '' || url.hash !== '') {
    throw new Error(`${file}: calendly_url is ${text}, which carries a query or a fragment of its own. The file writes the link alone: the page adds only the allow-listed UTM values a visitor arrived with, encoded and capped, and it adds none of them to the plain-link fallback a visitor who declined marketing consent gets (F22, G2.4).`)
  }
  return text
}

/**
 * The WhatsApp number in the `wa.me` form: digits alone, the country code first, no plus, no
 * spaces and no punctuation. Quoted in the file, so a number whose leading digit is a zero
 * survives a refresh (R3).
 *
 * @param {*} value @param {string} file
 */
function waNumber(value, file) {
  const text = asString(value, file, 'whatsapp_number')
  if (!/^\d+$/.test(text)) {
    throw new Error(`${file}: whatsapp_number is ${JSON.stringify(text)}, and the wa.me form is digits alone — the country code and the number, with no plus, no spaces and no punctuation: https://wa.me/<digits> (R3).`)
  }
  return text
}

// ---------------------------------------------------------------------------------------
// The loader.
// ---------------------------------------------------------------------------------------

/**
 * One campaign variant, as `website/data/landing.yaml` writes it and a page renders it.
 *
 * @typedef {object} LandingVariant
 * @property {string} name The variant's name: the path without its slash, which is also the name
 *   the pre-fill message carries and the name a page and its events use (`/crew` is `crew`).
 * @property {string} path The route the page is built at.
 * @property {string} headline The hero's own line, from the spec.
 * @property {string} supportingLine The one line under the headline.
 * @property {string} tone The variant's voice, which the page's look keys off.
 * @property {{ value: string, source: string }} proofNumber The figure the band prints with the
 *   entry of the measured costs that backs it, or the sentinel in both.
 * @property {string} whatsappText The pre-filled message, naming this variant's page.
 * @property {string} whatsappURL The `wa.me` link, built from the file's number and that message.
 * @property {string | null} video A self-hosted video path, or null while none exists.
 * @property {string | null} image A self-hosted image path, or null.
 */

/**
 * `website/data/landing.yaml`, validated, plus the two identifiers every page links.
 *
 * @typedef {object} LandingFacts
 * @property {string} calendlyURL The owner's Calendly link, without a query of its own.
 * @property {string} whatsappNumber The WhatsApp Business number in the `wa.me` form.
 * @property {{ name: string, image: string }[]} partnerLogos The partners whose logos the
 *   social-proof section may render. Empty until one exists, which hides the section.
 * @property {LandingVariant[]} variants The four campaigns.
 */

/**
 * The four variants, read from a document already in memory. `site` is a `loadSite()` result —
 * the site's own `min_runs` and the measured costs read with it — which is what a proof number
 * is held against; the test passes its own facts through the same seam.
 *
 * @param {string} raw @param {string} file
 * @param {{ facts: { minRuns: number }, typicalCosts: { jobs: { id: string, runs: number }[] } }} site
 * @returns {LandingFacts}
 */
export function parseLanding(raw, file, site) {
  const doc = asMap(parseYAML(raw, file), file, 'the document')
  refuseUnknownKeys(doc, DOCUMENT_KEYS, file, "the landing file's keys")

  if (doc.version !== 1) {
    throw new Error(`${file}: version is ${describe(doc.version)}, and this reader knows version 1 (G2.1).`)
  }

  const calendlyURL = bookingLink(doc.calendly_url, file)
  const whatsappNumber = waNumber(doc.whatsapp_number, file)
  const partnerLogos = asList(doc.partner_logos, file, 'partner_logos').map((entry, index) => partnerLogo(entry, index, file))
  const variants = asList(doc.variants, file, 'variants').map((entry, index) => variant(entry, index, file, site))

  // The four pages are the four campaigns, each once: a path missing is a campaign with no page
  // to send anyone to, and a path twice is two pages built at one route.
  const paths = variants.map((one) => one.path)
  const twice = paths.find((route, at) => paths.indexOf(route) !== at)
  if (twice !== undefined) {
    throw new Error(`${file}: two variants carry the path ${JSON.stringify(twice)}, and a path is the route a page is built at — one route is one page, and the second variant is the one to remove (G2.1).`)
  }
  const missing = CAMPAIGNS.filter((name) => !paths.includes(`/${name}`))
  if (missing.length > 0) {
    const routes = missing.map((name) => `/${name}`).join(', ')
    throw new Error(
      `${file}: the document carries ${paths.length} of the four campaign variants, and ${routes} ${missing.length === 1 ? 'is' : 'are'} missing. Every campaign the ads send anyone to is a page, and this file is what says which four exist (spec §6, G2.1).`,
    )
  }

  return {
    calendlyURL,
    whatsappNumber,
    partnerLogos,
    variants: variants.map((one) => ({ ...one, whatsappURL: whatsappLink(whatsappNumber, one.whatsappText) })),
  }
}

/** @type {LandingFacts | null} */
let loaded = null

/**
 * The four campaign variants, read from `website/data/landing.yaml` and held against the site's
 * own facts — `loadSite()` carries `site.yaml`'s `min_runs` and the entries of
 * `typical-costs.yaml` read with it, so a published figure's threshold is one number.
 *
 * A file that is missing, empty, or written in a shape this reader does not know throws, and
 * every message names the file: there is no fallback value anywhere below, because a default is
 * how a page renders nothing and says nothing. The result is memoized, as `loadSite()`'s is: the
 * build renders four pages from it and reads the file once.
 *
 * @returns {LandingFacts}
 */
export function loadLanding() {
  if (loaded !== null) return loaded
  const file = LANDING_FILE
  const full = path.join(SITE_ROOT, 'data', 'landing.yaml')
  if (!existsSync(full)) {
    throw new Error(`${file} is missing: the four campaign variants every landing page is built from (spec §6). The site's build fails rather than rendering a page with no copy on it (G2.1).`)
  }
  const raw = readFileSync(full, 'utf8')
  if (raw.trim() === '') {
    throw new Error(`${file} is empty: the four campaign variants every landing page is built from (spec §6). An empty file is four empty pages, and the build fails rather than rendering one (G2.1).`)
  }
  loaded = parseLanding(raw, file, loadSite())
  return loaded
}
