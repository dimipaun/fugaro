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
// its rules imports it directly under `node --test`. Its YAML reader and its validators are
// `content.ts`'s own, imported rather than copied: one data file, one dialect, and one place
// where a comment, a float or a duplicate key is read. Nor does it read the site through
// `loadSite()`: the two facts a proof number needs — `site.yaml`'s `min_runs` and the entries of
// `typical-costs.yaml`, which `loadTypicalCosts(minRuns)` refuses below that number itself — come
// from `loadSiteFacts()` and `loadTypicalCosts()` directly, so the threshold of a published
// figure stays one number (D96, D97) and a build of this file reads no README, no generated
// document and no font.
//
// Four rules, and every one of them is a refusal rather than a default:
//
//   1. **A proof number is backed or it is the sentinel.** A variant's figure is `unmeasured`,
//      or it is accepted only beside the entry of the owner's own measurement of this project's
//      runs it was drawn from — and only when that entry's sample is at or above D97's minimum.
//      What the guard refuses is a figure with nothing behind it, which is what the campaign
//      copy of the agenda would otherwise supply (F6). It judges no figure's truth, wording or
//      roundness: a figure the measurement backs is read as the file wrote it. The copy's own
//      rhetoric — `/save`'s "a tenth of the price", which the spec writes as a headline — is
//      copy, and it is not a `proof_number`.
//   2. **Media is self-hosted.** A variant's video and image are paths the site serves, never an
//      absolute URL: an external player would be a fourth third party the consent work never
//      admitted (F20, D107).
//   3. **The four variants are the spec's four campaigns**, at the spec's own paths, and the
//      pre-fill message names the variant its page is (spec §6). A fifth path is a page no
//      campaign sends anyone to.
//   4. **A partner logo is a name beside a self-hosted picture, and the name is there.** The
//      social-proof section prints the partner's own name beside the logo: an entry whose `name`
//      is blank is a picture nobody is named beside, which is not social proof. The shipped list
//      is empty, because the file names no partner until the owner has that partner's written
//      permission (F11, F12, D405).
//
// Nothing here fetches, resolves a host or reads a clock, and no npm package is added for the
// site's benefit (Global Constraint 5).

import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'

import { asList, asMap, asString, describe, loadSiteFacts, loadTypicalCosts, parseYAML, refuseUnknownKeys, SITE_ROOT } from '../content.ts'

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
// the file a figure has to be drawn from.
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

/** A tone: the word the page's look keys off, so it carries no punctuation, a slash or a space
 * that would become a selector that matches nothing. A partner logo's `name` is not this: it is
 * the partner's own name, read as written. A variant's path is held to the same charset by the
 * route check in `variant()` below, which reads it as the path it is rather than as a word. */
const WORD = /^[a-z][a-z0-9-]*$/

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
 * One line of a variant's copy that the hero prints: a headline or the supporting line under it.
 * A blank string is refused rather than rendered, because a headline or a supporting line with
 * nothing in it is a hero with a hole where the campaign copy goes — and this file is the only
 * place that copy is written, so there is no other sentence to fall back to (G2.1).
 *
 * @param {*} value @param {string} file @param {string} what
 */
function copyLine(value, file, what) {
  const text = asString(value, file, what)
  if (text.trim() === '') {
    throw new Error(
      `${file}: ${what} is ${JSON.stringify(text)}, and the hero prints it as the campaign's own copy. A blank ${what} is a hero with a hole where the copy goes: this file is the only place a variant's headline and supporting line are written, so there is nothing to render in its place (G2.1).`,
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

/**
 * A variant's proof number: the figure the band prints in big type, with the entry of the
 * owner's measurement that backs it, or the sentinel.
 *
 * This is the guard of F6: a published figure is never typed into a page, so a value that is not
 * the sentinel must name an entry of `website/data/typical-costs.yaml` — the rows
 * `loadTypicalCosts(minRuns)` returned, so the minimum sample is site.yaml's one number and not a
 * second copy — and that entry's own sample must be at or above it.
 *
 * @param {*} value @param {string} file @param {string} what
 * @param {{ facts: { minRuns: number }, typicalCosts: { jobs: { id: string, name: string, runs: number }[] } }} site
 */
function proofNumber(value, file, what, site) {
  const proof = asMap(value, file, what)
  refuseUnknownKeys(proof, PROOF_KEYS, file)
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
      `${file}: ${what} carries the figure ${JSON.stringify(figure)} with "${SENTINEL}" in place of its source. A published figure is never typed into a page: its source names the entry of ${TYPICAL_COSTS} it was drawn from, and the loader refuses a figure nothing backs (F6).`,
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
 * @param {{ facts: { minRuns: number }, typicalCosts: { jobs: { id: string, name: string, runs: number }[] } }} site
 */
function variant(value, index, file, site) {
  const what = `variants[${index}]`
  const entry = asMap(value, file, what)
  refuseUnknownKeys(entry, VARIANT_KEYS, file)

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
    headline: copyLine(entry.headline, file, `${what}.headline`),
    supportingLine: copyLine(entry.supporting_line, file, `${what}.supporting_line`),
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
  refuseUnknownKeys(entry, LOGO_KEYS, file)
  const name = asString(entry.name, file, `${what}.name`)
  if (name.trim() === '') {
    throw new Error(
      `${file}: ${what}.name is ${JSON.stringify(name)}, and the social-proof section prints the partner's own name beside the logo. A blank name is a picture nobody is named beside, which is not social proof, and the file names a partner only once the owner has that partner's written permission (F11, F12, D405).`,
    )
  }
  return { name, image: selfHosted(entry.image, file, `${what}.image`) }
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
 * The four variants, read from a document already in memory. `site` is what a proof number is
 * held against — `site.yaml`'s own `min_runs` and the entries `loadTypicalCosts(minRuns)` returns
 * for `typical-costs.yaml` — so a test passes its own facts through the same seam
 * `loadLanding()` fills from the site's own files.
 *
 * @param {string} raw @param {string} file
 * @param {{ facts: { minRuns: number }, typicalCosts: { jobs: { id: string, name: string, runs: number }[] } }} site
 * @returns {LandingFacts}
 */
export function parseLanding(raw, file, site) {
  const doc = asMap(parseYAML(raw, file), file, 'the document')
  refuseUnknownKeys(doc, DOCUMENT_KEYS, file)

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

/**
 * The file's bytes, under the website directory `root` — `SITE_ROOT` in a build, a test's own
 * directory in a case. A file that is missing or empty throws rather than reading as a document
 * with no variants in it, and both messages name the file.
 *
 * @param {string} root
 * @returns {string}
 */
export function readLandingFile(root) {
  const file = LANDING_FILE
  const full = path.join(root, 'data', 'landing.yaml')
  if (!existsSync(full)) {
    throw new Error(`${file} is missing: the four campaign variants every landing page is built from (spec §6). The site's build fails rather than rendering a page with no copy on it (G2.1).`)
  }
  const raw = readFileSync(full, 'utf8')
  if (raw.trim() === '') {
    throw new Error(`${file} is empty: the four campaign variants every landing page is built from (spec §6). An empty file is four empty pages, and the build fails rather than rendering one (G2.1).`)
  }
  return raw
}

/** @type {LandingFacts | null} */
let loaded = null

/**
 * The four campaign variants, read from `website/data/landing.yaml` and held against the site's
 * own facts — `loadSiteFacts()` for `min_runs` and `loadTypicalCosts(min_runs)` for the entries a
 * figure may be drawn from, which are the same two the caps-and-costs guide reads.
 *
 * A file that is missing, empty, or written in a shape the reader does not know throws, and every
 * message names the file: there is no fallback value anywhere below, because a default is how a
 * page renders nothing and says nothing. The result is memoized: the build renders four pages
 * from it and reads the file once.
 *
 * @returns {LandingFacts}
 */
export function loadLanding() {
  if (loaded !== null) return loaded
  const facts = loadSiteFacts()
  const parsed = parseLanding(readLandingFile(SITE_ROOT), LANDING_FILE, { facts, typicalCosts: loadTypicalCosts(facts.minRuns) })
  loaded = { ...parsed, variants: parsed.variants.map((one) => ({ ...one, video: carried(one.video), image: carried(one.image) })) }
  return loaded
}

/**
 * A variant's media when the site also SERVES it: the path as the file wrote it, or null when
 * nothing is under `website/public/` at it.
 *
 * The document's own rule is that the media is a self-hosted path, and this is the other half of
 * it: the owner produces the campaign's video and pictures when they exist, and this repository
 * carries none of them today, so a variant that names a file nobody has produced renders a
 * player or a picture with nothing behind it — a broken element on the one page a visitor
 * reached from an ad. Reporting null instead is what makes the page complete without the file
 * (F14, G2.5), and it is read here rather than in a template so one page and one component
 * cannot disagree about what exists.
 *
 * `parseLanding()` still reads the document as written, which is what its own cases hold: what a
 * file NAMES and what the site HAS are two questions, and this is the second one.
 *
 * @param {string | null} media
 * @returns {string | null}
 */
function carried(media) {
  if (media === null) return null
  return existsSync(path.join(SITE_ROOT, 'public', media)) ? media : null
}
