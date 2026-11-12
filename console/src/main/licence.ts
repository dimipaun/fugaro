// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's offline licence-key verifier (subscriptions plan S2.4; the
// design is docs/licensing/licence-key.md, the S2.1 note, and S2.3's Go
// verifier is this module's sibling).
//
// One Ed25519 signature over a JSON claim set, in three base64url parts, in
// this order:
//
//     <key id>.<payload>.<signature>
//
// Four rules of the design are what the code below is shaped by, and each is a
// test in `test/unit/licence.test.ts` rather than a reading of this comment:
//
//   1. **The key id leads, in the clear.** The verifier has to choose the public
//      key before it can verify anything, so an id the keyring does not hold is
//      refused *before either other part is decoded* -- which is what makes a
//      compiled-in public key rotate, and what refuses a staging key in a
//      production build rather than half-accepting one. The id is the one part
//      **nothing decodes**: it is looked up as the text the key carries, held to
//      the base64url alphabet with the shape, exactly as the CLI's and the app's
//      verifiers read it, because one minted key text has to verify in all three
//      (S2.6).
//   2. **The raw bytes before the claims.** The signature is verified over the
//      payload's bytes as spelled, and only then are they parsed. Re-wrapping,
//      re-ordering or re-encoding a claim set is a different byte string, so it
//      fails the signature instead of verifying as the same claims; and no claim
//      is read out of a payload whose signature did not verify, because a claim
//      from an unverified payload is attacker-chosen text. Every refusal here
//      therefore names the key and the part that failed, never a claim.
//   3. **Three states, and a bound.** Against `now`: `valid` to `expires`,
//      `grace` for the 30 days after it, `expired` past that. A key outside all
//      three -- a bad signature, an unknown id, a term longer than its tier
//      allows -- is refused rather than given a state. A key past its grace is
//      still *read*, because the lapse is a state the console draws (S6.1) and
//      not an error.
//   4. **Two keys may be installed, and the later `expires` applies** (T26):
//      the next key issues 30 days before the current one expires, so a renewal
//      landing early changes nothing for the user.
//
// **What is deliberately not here.** No store and no file: where the console
// keeps the key, and how it reaches the CLI it spawns, is S2.7's
// (`licence-store.ts`, and one new `CHILD_ENV_KEYS` entry that is
// security-sensitive and has its own review). No read-only state: what the
// console still allows after the grace is S6.1's design and S6.2's build. No
// production key: `PRODUCTION_KEYRING` below is where S2.9 puts the owner's
// published public half, once S3.8 has created it. And no vectors: one set of
// vectors for all three verifiers is S2.6's.
//
// Three more things are rules rather than omissions. The verifier **makes no
// network call**, which is a fact about this file's imports and is held by a
// scan (`LicenceModuleImportsNoNetwork`). It **reads no environment**, so a key
// can only arrive as an argument. And it **throws nothing**: every answer is a
// `LicenceRead`, so a caller in the main process has a sentence to draw instead
// of an exception to catch. `now` is an argument for the same reason the clock
// is nowhere in here -- a state is a fact about a key and a moment.
//
// **The v1 key is read beside this one.** `verifyLicenceKeyV1`, at the end of
// this file, verifies the licence-key-v2 format (`fgk_<payload>.<signature>`,
// decisions D2130 and D2146) against the same kind of keyring, and the two
// formats are read by two functions that share no rule -- only the part
// decoder, because a part is a part in either. The console keeps working with
// the old shape until K7.3 removes it, and a text of one shape is refused by the
// other's verifier rather than half-read by it.

import { createPublicKey, verify as edVerify, type KeyObject } from 'node:crypto'

import { LITERAL_PLACEHOLDER, sanitizeText } from '../shared/sanitize'

/** LicenceTier is the `tier` claim's closed list (the design's `tier` row). */
export type LicenceTier = 'community' | 'team' | 'enterprise'

/** LicenceState is what an installed key is, against a `now`. A key that is
 * outside all three is refused rather than given a fourth. */
export type LicenceState = 'valid' | 'grace' | 'expired'

/** LicenceClaims is the payload, claim for claim. `organisation` is what the
 * key was issued to -- a domain, an individual as an organisation of one, or an
 * Order Form id -- and not a person or a machine: nothing in the product
 * compares it with the installation. `seats` is compared and shown, never
 * enforced (D439). */
export interface LicenceClaims {
  readonly tier: LicenceTier
  readonly features: readonly string[]
  readonly seats: number
  readonly organisation: string
  readonly issued: number
  readonly expires: number
}

/** LicenceKeyring is one lookup table of key id to the public half, in PEM:
 * the key id names the environment its signing key belongs to, and a production
 * build holds production ids only. */
export type LicenceKeyring = Readonly<Record<string, string>>

/** PRODUCTION_KEYRING is the ring the shipped console verifies against. It is
 * empty today because the owner's signing keys do not exist yet: S3.8 creates
 * them, S2.9 puts the published production public half in here, and until then
 * nothing verifies in a real build -- which is the honest state of a release
 * that carries no key enforcement. */
export const PRODUCTION_KEYRING: LicenceKeyring = Object.freeze({})

/** GRACE_DAYS is the grace that follows `expires` before the paid side lapses
 * (B17, D436). */
export const GRACE_DAYS = 30

const DAY_SECONDS = 86_400

/** TIER_MAX_LIFETIME_DAYS is the longest term each tier's key may be minted
 * for: the tier's term plus the 30-day issue lead the renewal above needs,
 * because that is the longest key the design mints -- an annual renewal is
 * minted 30 days before the key it follows expires, so a ceiling of the bare
 * term would refuse the very renewal the overlap exists for. The maxima are the
 * S2.1 note's number and not the Terms': a tier can be granted a longer one
 * here, in a decision, without a change to the key format. */
export const TIER_MAX_LIFETIME_DAYS: Readonly<Record<LicenceTier, number>> = Object.freeze({
  community: 365 + GRACE_DAYS,
  team: 365 + GRACE_DAYS,
  enterprise: 365 + GRACE_DAYS,
})

/**
 * MAX_LICENCE_KEY_LENGTH is the longest a key may be, and it bounds one in both
 * places the console meets one: the surface's own `licenceKey` argument kind (a
 * paste a renderer sent, `src/main/ipc.ts`) and the store's reader (a key in
 * the file the console keeps, `src/main/licence-store.ts`). It is here, in the
 * module that owns what a licence key is, so the two are one number rather than
 * two that agree today.
 *
 * A key is three base64url parts -- an id, a JSON claim set of six fields, and
 * an Ed25519 signature of 64 bytes -- so a real one is a few hundred characters
 * and the bound is generous by an order of magnitude; what it is for is the
 * other end, where a value is refused **before** anything decodes it.
 */
export const MAX_LICENCE_KEY_LENGTH = 1024

/** LicenceRead is one key's answer: the claims and the state they put the
 * installation in, or the console's own sentence for refusing the key. */
export type LicenceRead =
  | { readonly ok: true; readonly claims: LicenceClaims; readonly state: LicenceState }
  | { readonly ok: false; readonly reason: string }

/** TIERS is the closed list the `tier` claim is checked against. */
const TIERS: readonly string[] = Object.freeze(['community', 'team', 'enterprise'])

/** isTier is that check as a narrowing, so a claim passing it needs no cast. */
function isTier(value: unknown): value is LicenceTier {
  return typeof value === 'string' && TIERS.includes(value)
}

/** BASE64URL is the spelling every part uses: the alphabet, unpadded. A part
 * with a character outside it -- a `+`, a `/`, a pad -- is not a part this
 * verifier reads. */
const BASE64URL = /^[A-Za-z0-9_-]+$/

/**
 * decodePart reads one base64url part, canonically or not at all.
 *
 * The re-encode is the rule and not a formality: a base64 decoder ignores the
 * bits a part's length leaves over, so two spellings can decode to the same
 * bytes. A verifier that took the second spelling would accept a signature the
 * minter never wrote, so a part whose canonical spelling is not the one that
 * arrived is refused.
 */
function decodePart(part: string): Uint8Array | null {
  if (!BASE64URL.test(part)) return null
  const bytes = Buffer.from(part, 'base64url')
  return bytes.toString('base64url') === part ? bytes : null
}

/** textOf decodes UTF-8, or nothing: a payload that is not text is not a claim
 * set this verifier can parse. */
function textOf(bytes: Uint8Array): string | null {
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return null
  }
}

/** unixSeconds is the shape the two time claims have: a whole number of
 * seconds, never negative. */
function unixSeconds(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

/**
 * claimsOf parses a payload whose signature has already verified.
 *
 * A claim the console does not know is **refused**, not ignored, and that is
 * the Go verifier's rule rather than a console choice: the three verifiers read
 * one key text and answer one state (S2.6's shared vectors carry a `covers_until`
 * case for it, which O4 dropped from the format). A build that ignored a seventh
 * name would license an installation on a key the CLI and the app refuse. A
 * claim it does know and cannot read is refused the same way, because a key
 * whose seats or organisation is a string is not a key any decision here can be
 * made about.
 */
function claimsOf(payload: string): LicenceClaims | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(payload)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null
  const claims = parsed as Record<string, unknown>

  // The six claims are the whole of the format. `Object.keys` lists an own
  // `__proto__` a payload carries, so it is refused like any other name.
  if (Object.keys(claims).length !== 6) return null

  const tier = claims['tier']
  if (!isTier(tier)) return null

  const features = claims['features']
  if (!Array.isArray(features)) return null
  const list: string[] = []
  for (const feature of features) {
    if (typeof feature !== 'string') return null
    list.push(feature)
  }

  const seats = claims['seats']
  if (typeof seats !== 'number' || !Number.isSafeInteger(seats) || seats < 0) return null

  const organisation = claims['organisation']
  if (typeof organisation !== 'string' || organisation === '') return null

  const issued = claims['issued']
  const expires = claims['expires']
  if (!unixSeconds(issued) || !unixSeconds(expires)) return null

  return { tier, features: list, seats, organisation, issued, expires }
}

/** nameOf is the key id as a sentence may draw it. The id is text an operator
 * pasted into a key, so it goes through the console's own label class -- which
 * strips what cannot be drawn and bounds what can -- and an id that class
 * refuses is drawn as the placeholder rather than raw. */
function nameOf(id: string): string {
  const drawn = sanitizeText(id, 'label', 'the key id')
  return drawn.ok && drawn.text !== '' ? drawn.text : LITERAL_PLACEHOLDER
}

/** refuse is every refusal that has a key to name. */
function refuse(id: string, what: string): LicenceRead {
  return { ok: false, reason: `licence key '${nameOf(id)}' ${what}` }
}

/** stateOf is the state table: `valid` to `expires`, `grace` for the 30 days
 * after it, `expired` past that. */
function stateOf(expires: number, nowSeconds: number): LicenceState {
  if (nowSeconds <= expires) return 'valid'
  if (nowSeconds <= expires + GRACE_DAYS * DAY_SECONDS) return 'grace'
  return 'expired'
}

/**
 * verifyLicenceKey reads one key against one keyring at one moment.
 *
 * The order below is the design's and not an implementation choice: the id is
 * looked up before the other two parts are decoded, the signature is verified
 * over the payload's raw bytes before any of them is parsed, and the tier's
 * lifetime bound is applied before the state is read, so a key outside the
 * three states is refused rather than given one.
 */
export function verifyLicenceKey(raw: string, now: Date, keyring: LicenceKeyring = PRODUCTION_KEYRING): LicenceRead {
  // A key arrives from a file's line or from a paste, and neither adds meaning
  // to the whitespace around it; whitespace inside one is still a part that is
  // not base64url.
  const parts = raw.trim().split('.')
  if (parts.length !== 3) return { ok: false, reason: 'a licence key is three base64url parts' }

  // The id leads, in the clear, and it is the text the key carries: it is
  // looked up, and named in a refusal, exactly as written and never decoded --
  // which is what the CLI's and the app's verifiers do, and what one minted key
  // text needs to be read by all three (S2.6). Its spelling is still held to
  // the base64url alphabet the design gives every part, so a text outside it is
  // a malformed key rather than an id no keyring happens to hold.
  const id = parts[0] ?? ''
  if (id === '' || !BASE64URL.test(id)) return { ok: false, reason: "a licence key's id is not spelled in base64url" }

  // `Object.hasOwn` and not a plain index: a keyring is a plain object, an id
  // is text an operator pasted, and `keyring['constructor']` is not a public
  // key this build holds.
  const publicKey = Object.hasOwn(keyring, id) ? keyring[id] : undefined
  if (publicKey === undefined) return refuse(id, 'is not one the console holds')

  const payload = decodePart(parts[1] ?? '')
  if (payload === null) return refuse(id, 'has a payload that does not decode')

  const signature = decodePart(parts[2] ?? '')
  if (signature === null) return refuse(id, 'has a signature that does not decode')
  if (signature.length !== 64) return refuse(id, 'has a signature that is not 64 bytes')

  // The ring is the build's own, so an entry that is not an Ed25519 public key
  // is a defect in this console rather than in the key -- and it is still a
  // refusal, because a throw here would be an exception in the main process.
  let key
  try {
    key = createPublicKey(publicKey)
  } catch {
    return refuse(id, 'is not one the console holds')
  }
  if (key.asymmetricKeyType !== 'ed25519') return refuse(id, 'is not one the console holds')

  if (!edVerify(null, payload, key, signature)) return refuse(id, 'has a signature that does not verify')

  const payloadText = textOf(payload)
  if (payloadText === null) return refuse(id, 'has a payload that does not decode')

  const claims = claimsOf(payloadText)
  if (claims === null) return refuse(id, 'has claims that are not a licence claim set')

  if (claims.expires - claims.issued > TIER_MAX_LIFETIME_DAYS[claims.tier] * DAY_SECONDS) {
    return refuse(id, 'claims a term longer than its tier allows')
  }

  return { ok: true, claims, state: stateOf(claims.expires, Math.floor(now.getTime() / 1000)) }
}

/**
 * latestLicence picks the key that applies out of the ones installed, or `null`
 * when none of them does.
 *
 * Two keys may be installed (T26), and the one with the later `expires` is the
 * one that applies: a renewal minted 30 days early has the later expiry, so an
 * issuing outage only bites once it lasts past 60 days. A key that is refused
 * is skipped rather than counted, and `null` covers both the empty store and a
 * store whose every key was refused -- neither is a fourth state here, because
 * what an installation with no usable key shows is S6.1's design.
 */
export function latestLicence(
  keys: readonly string[],
  now: Date,
  keyring: LicenceKeyring = PRODUCTION_KEYRING,
): LicenceRead | null {
  let latest: { claims: LicenceClaims; state: LicenceState } | null = null
  for (const key of keys) {
    const read = verifyLicenceKey(key, now, keyring)
    if (!read.ok) continue
    if (latest === null || read.claims.expires > latest.claims.expires) {
      latest = { claims: read.claims, state: read.state }
    }
  }
  return latest === null ? null : { ok: true, claims: latest.claims, state: latest.state }
}

// ---------------------------------------------------------------------------
// The v1 key: `fgk_<payload>.<signature>`
// ---------------------------------------------------------------------------
//
// The licence-key-v2 format (spec sections 3, 4 and 6; decision D2130), whose Go
// verifier is `ee/licence/keyv1.go` (K1.1) and whose vectors the two are read
// against are `ee/licence/testdata/vectors_v1.json` (K1.4).
//
// Three rules of that design shape the code below, and each is a case in
// `test/unit/licence.test.ts` rather than a reading of this comment:
//
//   1. **The id travels inside the payload.** The older format wrote it in the
//      clear in front of the payload, which is what let that verifier consult the
//      keyring before decoding anything. A v1 key has no such part: `kid` and `v`
//      are read out of the payload first -- they are what chooses the public key
//      and decides whether this build reads the key at all -- and the claim set
//      is decoded only after the signature over the payload's raw bytes has
//      verified. So every refusal is one fixed sentence and echoes **no** text
//      out of the payload, the id included: until the signature has verified,
//      every character of the payload is text its author chose.
//   2. **Unknown fields are ignored, and only here** (D2146). Every other wire
//      format this project writes decodes strictly; the spec's "unknown optional
//      fields are ignored" is a named exception for a *signed* key, so that a
//      field a later v1 mint adds has a safe default in a build that predates it.
//      A known field of the wrong type is still refused, and so is a payload that
//      is not one claim set.
//   3. **The workspace binds the key** (D2134 as amended by TN2): `workspace_id`
//      is required and compared with the Firebase project id the caller passes,
//      which is the workspace the key is read in.
//
// `check_interval` is validated and never used: whether to contact the licence
// server is the renewal job's (K4.1), and this verifier makes no network call at
// all.

/** V1_PREFIX is what makes a v1 key recognisable in a log, a secret scanner and
 * a support ticket, which is the whole reason the format carries it (spec
 * section 3.1). */
export const V1_PREFIX = 'fgk_'

/** V1_VERSION is the only policy version this verifier reads. A higher one is
 * refused with `V1_UPGRADE_MESSAGE` and is never read as anything else: it is
 * not a fall back to Free (spec section 4). */
export const V1_VERSION = 1

/** V1_UPGRADE_MESSAGE is the spec's refusal for a key of a version this build
 * does not read, word for word (section 4), so that the sentence a console
 * draws is the sentence the CLI and the licence site's pages state. */
export const V1_UPGRADE_MESSAGE =
  'This licence key requires a newer version of Fugaro. Upgrade Fugaro to use it.'

/** LicenceV1State is what a v1 key is against a `now`: inside its paid term,
 * inside the grace the key itself carries, or past both (spec section 6).
 * `active` is the v1 name for the older format's `valid` above; the other two
 * are the names both formats write, because the state a key is in is one name
 * shared by every reader of the key. */
export type LicenceV1State = 'active' | 'grace' | 'expired'

/** LicenceV1RenewalMode and LicenceV1Kind are the closed lists the two named
 * claims are checked against. What each mode makes an installation do is the
 * renewal job's (K4.1); this verifier places the name. */
export type LicenceV1RenewalMode = 'auto' | 'manual' | 'none'
export type LicenceV1Kind = 'paid' | 'trial' | 'comped'

/**
 * LicenceV1Refusal names why a v1 key was refused, in the words the Go
 * verifier's sentinels and this one share: the two read one key text, and a key
 * one refuses has to be refused by the other for the same reason. A caller that
 * has to say *why* draws this name; a caller drawing a sentence uses the read's
 * `reason`.
 */
export type LicenceV1Refusal =
  | 'malformed'
  | 'unknown-key-id'
  | 'signature'
  | 'claims'
  | 'lifetime'
  | 'newer-version'
  | 'workspace'
  | 'duration'

/** LicenceV1Warning is one warning window: the roles it reaches and the moment
 * it opens at, counted from the start of the key's grace. This verifier carries
 * it; what reads one is K3.5. */
export interface LicenceV1Warning {
  readonly roles: readonly string[]
  readonly from: string
}

/**
 * LicenceV1Claims is the claim set a v1 payload holds, claim for claim, in the
 * payload's own spelling -- unlike the older format's camel-case claims above,
 * because this claim set is the format and not a shape this console chose: the
 * vectors, the licence server and the Go verifier all spell these names, and a
 * translation layer here would be one more place a claim could change meaning.
 *
 * Every field is the whole of it: an unknown field is ignored (rule 2 above),
 * and a field a payload omits or nulls takes the value a Go decoder would have
 * left in it -- an empty string, 0, or `null` for `check_interval`, which is how
 * a key says it never checks. `organisation` is what the key was issued to and
 * nothing compares it with the installation it is read at; `seats` is compared
 * and shown, never enforced here (D2132 held with K3.4); `note` is free text for
 * admins only. `renewal_secret_hash` is a hash and never the secret (D2135).
 */
export interface LicenceV1Claims {
  readonly v: number
  readonly kid: string
  readonly key_id: string
  readonly workspace_id: string
  readonly organisation: string
  readonly tier: LicenceTier
  readonly seats: number
  readonly issued_at: string
  readonly paid_through: string
  readonly grace_days: number
  readonly renewal_mode: LicenceV1RenewalMode
  readonly renew_before_days: number
  readonly check_interval: string | null
  readonly renewal_secret_hash: string
  readonly warnings: readonly LicenceV1Warning[]
  readonly kind: LicenceV1Kind
  readonly note: string
}

/** LicenceV1Read is one v1 key's answer: the claims and the state they put the
 * key in, or the refusal's name and the sentence for it. A refusal carries no
 * claim set and no state, so a caller cannot read anything out of a key this
 * verifier refused. */
export type LicenceV1Read =
  | { readonly ok: true; readonly claims: LicenceV1Claims; readonly state: LicenceV1State }
  | { readonly ok: false; readonly refusal: LicenceV1Refusal; readonly reason: string }

/** LicenceV1Decoded is one payload's answer inside the verifier: the claim set
 * every check has passed, or the refusal the payload itself answered -- the ones
 * that need neither the keyring nor the clock. */
type LicenceV1Decoded = { readonly claims: LicenceV1Claims } | { readonly refusal: LicenceV1Refusal }

/** V1_RENEWAL_MODES and V1_KINDS are the closed lists behind the two types
 * above, as `TIERS` is behind `LicenceTier`. */
const V1_RENEWAL_MODES: readonly string[] = Object.freeze(['auto', 'manual', 'none'])
const V1_KINDS: readonly string[] = Object.freeze(['paid', 'trial', 'comped'])

function isV1RenewalMode(value: string): value is LicenceV1RenewalMode {
  return V1_RENEWAL_MODES.includes(value)
}

function isV1Kind(value: string): value is LicenceV1Kind {
  return V1_KINDS.includes(value)
}

/** DAY_MS and MAX_INTERVAL_DAYS are the two numbers the v1 checks do their
 * arithmetic in. The longest span a duration holds is the Go verifier's bound,
 * computed the same way and for the same reason: a `check_interval` or a
 * `grace_days` past it is refused or clamped rather than wrapped into a window
 * far shorter than the key states. */
const DAY_MS = 86_400_000
const MAX_INTERVAL_DAYS = Math.floor((2 ** 62) / (24 * 60 * 60 * 1e9))

/** ZERO_INSTANT_MS is Go's zero time, 0001-01-01T00:00:00Z: what its decoder
 * leaves in a `time.Time` field a payload omits or nulls. A key with no
 * `paid_through` is therefore a key long past its grace rather than a refusal,
 * and the two verifiers have to agree about which. */
const ZERO_INSTANT_MS = Date.parse('0001-01-01T00:00:00Z')

/**
 * V1_REFUSALS is each refusal's sentence, and every one is fixed: no v1 refusal
 * names a value out of the payload (rule 1 above). `newer-version` is the one
 * sentence the format pins, and it is the spec's, not this file's.
 */
const V1_REFUSALS: Readonly<Record<LicenceV1Refusal, string>> = Object.freeze({
  malformed: 'the licence key is not an fgk_ key of a payload and a signature',
  'unknown-key-id': 'the licence key names a signing key this console does not hold',
  signature: 'the licence key’s signature does not verify',
  claims: 'the licence key’s claims are not a claim set this console reads',
  lifetime: 'the licence key claims a term longer than its tier allows',
  'newer-version': V1_UPGRADE_MESSAGE,
  workspace: 'the licence key is bound to a different workspace',
  duration: 'the licence key’s check interval is not a duration this console reads',
})

/** refuseV1 is every v1 refusal: the name a caller compares and the sentence a
 * caller draws, kept together so the two cannot drift apart. */
function refuseV1(refusal: LicenceV1Refusal): LicenceV1Read {
  return { ok: false, refusal, reason: V1_REFUSALS[refusal] }
}

/**
 * verifyLicenceKeyV1 reads one v1 key against one keyring at one moment.
 *
 * The order is the design's and not an implementation choice: `kid` and `v` are
 * read out of the payload (nothing else can be, the id that names the keyring
 * entry being a claim inside it), the signature is verified over the payload's
 * raw bytes before any claim is read, the version is compared before the claim
 * set is decoded, and the claim set is held to the workspace and the tier's
 * ceiling before the state is read -- so a key outside the three states is
 * refused rather than given one.
 *
 * `firebaseProjectId` is the workspace's own Firebase project id, read by the
 * caller from the project's config in the same read as the key (TN2); an empty
 * one never accepts a key. The ring is last so that it may default while the
 * workspace may not: a key is bound to one workspace, and there is no such thing
 * as a default one.
 */
export function verifyLicenceKeyV1(
  raw: string,
  now: Date,
  firebaseProjectId: string,
  keyring: LicenceKeyring = PRODUCTION_KEYRING,
): LicenceV1Read {
  // A key arrives by paste as often as from a file, and neither adds meaning to
  // the whitespace around it; whitespace inside one is a part that is not
  // base64url.
  const trimmed = raw.trim()
  if (!trimmed.startsWith(V1_PREFIX)) return refuseV1('malformed')
  const parts = trimmed.slice(V1_PREFIX.length).split('.')
  if (parts.length !== 2) return refuseV1('malformed')
  const payload = decodePart(parts[0] ?? '')
  const signature = decodePart(parts[1] ?? '')
  if (payload === null || signature === null) return refuseV1('malformed')

  const text = textOf(payload)
  const head = peekV1(text)
  if (head === null) return refuseV1('malformed')

  // `Object.hasOwn`, for the older verifier's reason: an id is text out of an
  // unverified payload, and `keyring['constructor']` is not a public key.
  const entry = Object.hasOwn(keyring, head.kid) ? keyring[head.kid] : undefined
  const key = entry === undefined ? null : ed25519KeyOf(entry)
  if (key === null) return refuseV1('unknown-key-id')

  // A signature that is not 64 bytes is a signature that does not verify, which
  // is the answer the Go verifier gives it too; the length is checked here
  // because Node's verifier only answers for the length it expects.
  if (signature.length !== 64 || !edVerify(null, payload, key, signature)) return refuseV1('signature')

  // Past this line the payload is the one the ring signed, so what it says is a
  // claim rather than a guess. The version is read before the claim set is
  // decoded, so a key of a version this build does not know is refused with the
  // upgrade message whatever shape its payload has.
  if (head.v > V1_VERSION) return refuseV1('newer-version')
  if (head.v < V1_VERSION) return refuseV1('claims')

  const decoded = decodeClaimsV1(text ?? '', firebaseProjectId)
  if ('refusal' in decoded) return refuseV1(decoded.refusal)
  return { ok: true, claims: decoded.claims, state: stateV1Of(decoded.claims, now.getTime()) }
}

/** ed25519KeyOf is a keyring entry as an Ed25519 public key, or `null` for one
 * that is not. The ring is the build's own, so such an entry is a defect in this
 * console rather than in the key -- and it is still an answer, because a throw
 * here would be an exception in the main process. */
function ed25519KeyOf(pem: string): KeyObject | null {
  try {
    const key = createPublicKey(pem)
    return key.asymmetricKeyType === 'ed25519' ? key : null
  } catch {
    return null
  }
}

/**
 * peekV1 reads the two claims that have to be known before the signature can be
 * verified: the key id that names the keyring entry, and the policy version that
 * decides whether this build reads the key at all. Everything else waits until
 * the signature has verified.
 *
 * A payload that is not a JSON object, or that carries no id, names no keyring
 * entry and no claim that can be placed, so the text is refused as malformed --
 * and the refusal carries none of the payload. `v` is read as the integer the
 * format writes it as: a payload spelling it any other way names no version this
 * verifier can compare and is malformed rather than read as a version it might
 * be. That is still a refusal and never a fall back to Free; what it is not is
 * the upgrade message, which is reserved for a key that says which version it
 * is.
 *
 * **One divergence from the Go verifier, and it is deliberate.** Go's decoder
 * reads one JSON value and leaves whatever follows it to the claim decode, so a
 * payload of a claim set with junk after it is refused there as `claims`; this
 * reader parses the payload whole and refuses it here, as `malformed`. Both
 * refuse it, no key a minter writes has that shape, and no vector in the shared
 * file carries one.
 */
function peekV1(payload: string | null): { readonly kid: string; readonly v: number } | null {
  if (payload === null) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(payload)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null
  const head = parsed as Record<string, unknown>

  const kid = head['kid']
  if (typeof kid !== 'string' || kid === '') return null
  // A field the payload omits or nulls is the zero value Go's decoder leaves in
  // it, which is a version this build does not read rather than a malformed
  // text.
  const raw = head['v']
  const version = raw === undefined || raw === null ? 0 : raw
  if (typeof version !== 'number' || !Number.isSafeInteger(version)) return null
  return { kid, v: version }
}

/**
 * decodeClaimsV1 reads the claim set out of a payload whose signature has
 * already verified, and holds it to the format's terms: unknown fields are
 * ignored (D2146), a known field of the wrong type is refused, and the checks
 * run in the Go verifier's order -- the workspace it binds to, the three named
 * claims, the check interval, the tier's ceiling.
 *
 * That order is what decides which of two faults a key with both answers, and it
 * is why the claim set is constructed last: a claim set this function answers is
 * one every check has already passed.
 */
function decodeClaimsV1(payload: string, firebaseProjectId: string): LicenceV1Decoded {
  let parsed: unknown
  try {
    parsed = JSON.parse(payload)
  } catch {
    return { refusal: 'claims' }
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return { refusal: 'claims' }
  const claims = parsed as Record<string, unknown>

  const v = integerClaim(claims['v'])
  const kid = stringClaim(claims['kid'])
  const keyId = stringClaim(claims['key_id'])
  const workspaceId = stringClaim(claims['workspace_id'])
  const organisation = stringClaim(claims['organisation'])
  const tier = stringClaim(claims['tier'])
  const seats = integerClaim(claims['seats'])
  const issuedAt = stampClaim(claims['issued_at'])
  const paidThrough = stampClaim(claims['paid_through'])
  const graceDays = integerClaim(claims['grace_days'])
  const renewalMode = stringClaim(claims['renewal_mode'])
  const renewBeforeDays = integerClaim(claims['renew_before_days'])
  const checkInterval = intervalClaim(claims['check_interval'])
  const renewalSecretHash = stringClaim(claims['renewal_secret_hash'])
  const warnings = warningsClaim(claims['warnings'])
  const kind = stringClaim(claims['kind'])
  const note = stringClaim(claims['note'])

  // One condition rather than seventeen: every field of the claim set, so that a
  // claim added to this format is a field this line is missing and not a field
  // nobody reads.
  if (
    v === null ||
    kid === null ||
    keyId === null ||
    workspaceId === null ||
    organisation === null ||
    tier === null ||
    seats === null ||
    issuedAt === null ||
    paidThrough === null ||
    graceDays === null ||
    renewalMode === null ||
    renewBeforeDays === null ||
    checkInterval === undefined ||
    renewalSecretHash === null ||
    warnings === null ||
    kind === null ||
    note === null
  ) {
    return { refusal: 'claims' }
  }

  if (workspaceId === '') return { refusal: 'claims' }
  if (workspaceId !== firebaseProjectId) return { refusal: 'workspace' }
  if (!isTier(tier)) return { refusal: 'claims' }
  if (!isV1RenewalMode(renewalMode)) return { refusal: 'claims' }
  if (!isV1Kind(kind)) return { refusal: 'claims' }
  if (checkInterval !== null && !readableIntervalV1(checkInterval)) return { refusal: 'duration' }
  // The bound is the module's one table, measured from the key's own
  // `issued_at`. Its number is the same as the older format's for the same
  // reason -- the longest key either format mints is a renewal that lands on the
  // first day it may -- and it bounds one key's length from a date the minter
  // chooses, which is the accepted limit both formats state. A term that runs
  // backwards is left to the state it produces rather than refused for its
  // length.
  if (stampInstant(paidThrough) - stampInstant(issuedAt) > TIER_MAX_LIFETIME_DAYS[tier] * DAY_MS) {
    return { refusal: 'lifetime' }
  }

  return {
    claims: {
      v,
      kid,
      key_id: keyId,
      workspace_id: workspaceId,
      organisation,
      tier,
      seats,
      issued_at: issuedAt,
      paid_through: paidThrough,
      grace_days: graceDays,
      renewal_mode: renewalMode,
      renew_before_days: renewBeforeDays,
      check_interval: checkInterval,
      renewal_secret_hash: renewalSecretHash,
      warnings,
      kind,
      note,
    },
  }
}

/** stringClaim, integerClaim, stampClaim and intervalClaim read one claim the way
 * Go's decoder leaves it: a field the payload omits or nulls is the field's zero
 * value -- an empty string, 0, no stamp -- and a field of another type is a
 * decode error, which `null` says here. `intervalClaim` answers `undefined` for
 * that error because `null` is a value this claim has: it is how a key says it
 * never checks. */
function stringClaim(value: unknown): string | null {
  if (value === undefined || value === null) return ''
  return typeof value === 'string' ? value : null
}

function integerClaim(value: unknown): number | null {
  if (value === undefined || value === null) return 0
  // An integer past the range JavaScript holds exactly is refused rather than
  // read as another one: a claim is a number a decision is made about, and a
  // value this process cannot hold exactly is not the value the key states.
  return typeof value === 'number' && Number.isSafeInteger(value) ? value : null
}

/** RFC3339 is the stamp shape the format writes its two times in. It is checked
 * as well as parsed because `Date.parse` reads spellings the format does not
 * write -- a date with no time, a time with no zone -- and a key whose term is
 * read from a stamp the minter did not write is a key whose state is a guess. */
const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/

function stampClaim(value: unknown): string | null {
  if (value === undefined || value === null) return ''
  if (typeof value !== 'string' || !RFC3339.test(value) || Number.isNaN(Date.parse(value))) return null
  return value
}

function intervalClaim(value: unknown): string | null | undefined {
  if (value === undefined || value === null) return null
  return typeof value === 'string' ? value : undefined
}

/** stampInstant is a claim's stamp as an instant in milliseconds; a stamp the
 * payload omitted is Go's zero time, which is what `stampClaim` left there. */
function stampInstant(stamp: string): number {
  return stamp === '' ? ZERO_INSTANT_MS : Date.parse(stamp)
}

/** warningsClaim reads the warnings list, and rolesClaim one window's roles.
 * They are a carrier: what reads a window is K3.5, and what this verifier holds
 * is that the payload's shape is the one the format declares, so that a key it
 * answers is a key the CLI answers too. */
function warningsClaim(value: unknown): readonly LicenceV1Warning[] | null {
  if (value === undefined || value === null) return []
  if (!Array.isArray(value)) return null
  const warnings: LicenceV1Warning[] = []
  for (const item of value) {
    if (typeof item !== 'object' || item === null || Array.isArray(item)) return null
    const window = item as Record<string, unknown>
    const roles = rolesClaim(window['roles'])
    const from = stringClaim(window['from'])
    if (roles === null || from === null) return null
    warnings.push({ roles, from })
  }
  return warnings
}

function rolesClaim(value: unknown): readonly string[] | null {
  if (value === undefined || value === null) return []
  if (!Array.isArray(value)) return null
  const roles: string[] = []
  for (const role of value) {
    if (typeof role !== 'string') return null
    roles.push(role)
  }
  return roles
}

/**
 * readableIntervalV1 answers whether a `check_interval` is one of the two ISO
 * 8601 forms the format writes (F16): `P<n>D` and `P<n>W`, and nothing else.
 * `PT1H`, `P1M` and a duration spelled any other way are refused rather than
 * approximated, because an interval that means something other than what it says
 * is worse than a key that will not load. A zero-length interval is a duration
 * and not a refusal; `null` is how a key says it never checks at all.
 */
function readableIntervalV1(text: string): boolean {
  if (text.length < 3 || text[0] !== 'P') return false
  const days = decimalDays(text.slice(1, -1))
  if (days === null) return false
  if (text.endsWith('D')) return true
  // The week form holds a seventh of the bound, as it does in the Go verifier:
  // past it, the days multiplied by seven are a span no duration holds.
  if (text.endsWith('W')) return days <= Math.floor(MAX_INTERVAL_DAYS / 7)
  return false
}

/** decimalDays reads a run of ASCII digits as a number of days, refusing an
 * empty run and one past the longest span a duration holds -- applied digit by
 * digit, so the accumulation cannot pass the bound before the check that would
 * refuse it. */
function decimalDays(text: string): number | null {
  if (text === '') return null
  let days = 0
  for (let index = 0; index < text.length; index += 1) {
    const digit = text.charCodeAt(index) - 48
    if (digit < 0 || digit > 9) return null
    days = days * 10 + digit
    if (days > MAX_INTERVAL_DAYS) return null
  }
  return days
}

/** graceMsOf is a key's own grace, clamped as the Go verifier clamps it: past
 * the longest span a duration holds, the grace is that span and the key stays in
 * grace for as long as this verifier can say, rather than wrapping into a window
 * shorter than the key states. A negative `grace_days` is no grace at all, and
 * the grace is the key's own number rather than one compiled in here, which is
 * what lets a trial carry none and an air-gapped key carry thirty (D2133,
 * D2138). */
function graceMsOf(days: number): number {
  if (days <= 0) return 0
  return Math.min(days, MAX_INTERVAL_DAYS) * DAY_MS
}

/** stateV1Of places a key against the clock: inside its paid term, inside the
 * grace it carries, or past both (spec section 6). A key past its grace is still
 * *read*, because the lapse is a state the console draws and not an error. */
function stateV1Of(claims: LicenceV1Claims, nowMs: number): LicenceV1State {
  const paidThrough = stampInstant(claims.paid_through)
  if (nowMs <= paidThrough) return 'active'
  if (nowMs - paidThrough <= graceMsOf(claims.grace_days)) return 'grace'
  return 'expired'
}
