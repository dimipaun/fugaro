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

import { createPublicKey, verify as edVerify } from 'node:crypto'

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
