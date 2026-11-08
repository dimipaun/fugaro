// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's offline licence-key verifier (subscriptions plan S2.4; the
// design is docs/licensing/licence-key.md, the S2.1 note, and the Go verifier
// S2.3 is this module's sibling).
//
// S2.4's own words are "S2.3's cases in TypeScript, with Node's built-in
// Ed25519", so the cases below are S2.3's, one `it` each, and each is held here
// for the reason it is held there:
//
//   - **the id leads** (VerifyUnknownKeyID, VerifyRefusesBeforeParsing). A
//     verifier chooses the public key before it can verify anything, so an id
//     the keyring does not hold is refused *before either other part is
//     decoded* -- which is what the second case proves: the payload and the
//     signature are not merely wrong, they are not base64url at all, and the
//     sentence is still the id's.
//   - **the raw bytes before the claims** (VectorsRoundTrip, and the three
//     negative vectors). The signature is over the payload's bytes as spelled,
//     so a re-wrapped payload is a different byte string rather than the same
//     claims, and no claim is read out of a payload whose signature did not
//     verify -- the truncated-claims case is a *valid* signature over a payload
//     that is not a claim set, and the sentence names the part, never a claim.
//   - **the three states and their edges** (GraceIsThirtyDays,
//     ExpiredAfterGrace): `valid` to `expires`, `grace` for the 30 days after
//     it, `expired` past that. A key past its grace is still *read*, not
//     refused: the lapse is a state the console draws (S6.1), not an error.
//   - **the tier's maximum** (KeyBeyondTheTierLifetimeIsRefused), from the
//     key's own `issued`, with the renewal the overlap exists for admitted at
//     the ceiling.
//   - **the overlap** (OverlappingKeysPickTheLatest): two keys may be
//     installed, and the later `expires` is the one that applies.
//   - **the two keyrings** (StagingKeyIDIsRefused, and the production ring's
//     half): a key id names the environment its signing key belongs to, and a
//     production build holds production ids only.
//   - **no network** (LicenceModuleImportsNoNetwork), the console's half of
//     S2.3's `TestLicencePackageImportsNoNetwork`: a verifier that never phones
//     home is a fact about its imports, so the imports are read back out of the
//     source and held to an allow-list -- and the scan is shown a plant it must
//     catch, because a scan that cannot fail is not a scan.
//
// **Three of S2.3's cases are deliberately not here, and each is somebody
// else's.** `TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts` is the CLI
// store's rule (the CLI reads `FUGARO_LICENCE_KEY` once and unsets it); the
// console's half of the hand-off is S2.7's `CHILD_ENV_KEYS` entry, which is
// security-sensitive and has its own review. What this file holds instead is
// the shape that half rests on: the verifier reads **no environment at all**,
// so a key can only arrive as an argument. The production keyring's *contents*
// are S2.9's, once the owner has created the keys (S3.8) -- so what is held
// here is the rule that survives that task: no test id is in it, and every
// entry it does hold is an Ed25519 public key. And one set of vectors for all
// three verifiers is S2.6's.
//
// **No key material is in this file.** Every key below is generated at run
// time, and a signature is computed from it; the design admits a public key and
// a signature vector in test data, never a private half, and a private key
// literal here would be a secret-shaped string in a public repository
// (test/unit/secret-hygiene.test.ts walks the tree for exactly that).
//
// The red step this file was written for: `a re-spelled payload is a different
// byte string` fails before src/main/licence.ts exists.

import { createPublicKey, generateKeyPairSync, sign as edSign, type KeyObject } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  GRACE_DAYS,
  PRODUCTION_KEYRING,
  TIER_MAX_LIFETIME_DAYS,
  latestLicence,
  verifyLicenceKey,
  type LicenceClaims,
  type LicenceKeyring,
  type LicenceRead,
  type LicenceState,
  type LicenceTier,
} from '../../src/main/licence'

const DAY = 86_400

/** ISSUED is the moment every key in this file is minted at: a fixed Unix
 * second rather than a clock, because every state below is a fact about a key
 * and a `now`, and a test that read the clock would be a test that changes. */
const ISSUED = 1_800_000_000

/** TIERS is the closed list the `tier` claim may name. */
const TIERS: readonly LicenceTier[] = ['community', 'team', 'enterprise']

/** Signer is one test key: an Ed25519 pair generated at run time, the keyring
 * that holds its public half, and the id its keys are spelled under. */
interface Signer {
  readonly id: string
  readonly publicKey: KeyObject
  readonly privateKey: KeyObject
  readonly keyring: LicenceKeyring
}

/** signer mints one test keypair. The PEM is the shape a keyring entry has: the
 * public half in the clear, which is all a verifier may hold. */
function signer(id: string): Signer {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519')
  const pem = publicKey.export({ type: 'spki', format: 'pem' })
  return { id, publicKey, privateKey, keyring: { [id]: pem } }
}

/** keyringOf is one keyring from several signers, so a case can hold a key that
 * verifies and a key that does not. */
function keyringOf(...signers: readonly Signer[]): LicenceKeyring {
  const ring: Record<string, string> = {}
  for (const one of signers) Object.assign(ring, one.keyring)
  return ring
}

/** b64url is the spelling every part of a key uses: base64url, unpadded. */
function b64url(text: string): string {
  return Buffer.from(text, 'utf8').toString('base64url')
}

/** spell joins the three parts, in the order the design fixes: the key id
 * first, in the clear, then the payload, then the signature over the payload's
 * bytes. */
function spell(id: string, payload: Uint8Array, signature: Uint8Array): string {
  return [
    b64url(id),
    Buffer.from(payload).toString('base64url'),
    Buffer.from(signature).toString('base64url'),
  ].join('.')
}

/** mint signs one claim set as it is spelled: the payload's bytes, and a
 * signature over exactly those bytes. `spellId` and `payloadText` exist so a
 * negative vector can spell an id the signature does not belong to, or a
 * payload the signature does not cover. */
function mint(s: Signer, claims: LicenceClaims, spellId = s.id, payloadText = JSON.stringify(claims)): string {
  const payload = Buffer.from(payloadText, 'utf8')
  return spell(spellId, payload, edSign(null, payload, s.privateKey))
}

/** claimsFor is a claim set of the shape the design fixes, with the fields a
 * case cares about overridden. */
function claimsFor(overrides: Partial<LicenceClaims> = {}): LicenceClaims {
  return {
    tier: 'team',
    features: ['report', 'coordinator'],
    seats: 4,
    organisation: 'acme.example',
    issued: ISSUED,
    expires: ISSUED + 365 * DAY,
    ...overrides,
  }
}

/** at is a `now` from a Unix second. */
function at(seconds: number): Date {
  return new Date(seconds * 1000)
}

/** refusalOf narrows a read to its sentence, failing the case if it verified. */
function refusalOf(read: LicenceRead): string {
  if (read.ok) throw new Error('the key verified and this case expects a refusal')
  return read.reason
}

/** verified narrows a read -- or an empty store's `null` -- to what it holds,
 * failing the case if no key applied. */
function verified(read: LicenceRead | null): { readonly claims: LicenceClaims; readonly state: LicenceState } {
  if (read === null || !read.ok) throw new Error('no key verified and this case expects one')
  return read
}

/** stateAt is the state one key is in at a Unix second, failing the case if the
 * key was refused rather than read. */
function stateAt(key: string, seconds: number, keyring: LicenceKeyring): LicenceState {
  const read = verifyLicenceKey(key, at(seconds), keyring)
  if (!read.ok) throw new Error(read.reason)
  return read.state
}

describe('the licence key', () => {
  it('VectorsRoundTrip: a minted key reads back as the claims it was minted from', () => {
    const acme = signer('acme-2026')
    const claims = claimsFor()

    expect(verifyLicenceKey(mint(acme, claims), at(ISSUED + DAY), acme.keyring)).toEqual({
      ok: true,
      claims,
      state: 'valid',
    })
  })

  it('VectorsRoundTrip: every tier and a key with no features read back whole', () => {
    const acme = signer('acme-2026')
    for (const tier of TIERS) {
      const claims = claimsFor({ tier, features: [] })
      const read = verifyLicenceKey(mint(acme, claims), at(ISSUED), acme.keyring)
      expect(verified(read).claims).toEqual(claims)
    }
  })

  it('VerifyUnknownKeyID: an id the keyring does not hold is refused, and the sentence names it', () => {
    const acme = signer('acme-2026')
    const reason = refusalOf(verifyLicenceKey(mint(acme, claimsFor()), at(ISSUED), keyringOf()))

    expect(reason).toContain('acme-2026')
    expect(reason).toMatch(/is not one the console holds/)
  })

  it('VerifyRefusesBeforeParsing: an unknown id is refused before either other part is decoded', () => {
    const acme = signer('acme-2026')
    // The other two parts are not base64url at all, and not JSON either: if the
    // id were looked up after them, this sentence would be about a part.
    const reason = refusalOf(
      verifyLicenceKey(`${b64url('someone-else')}.!!!not-base64url!!!.????`, at(ISSUED), acme.keyring),
    )

    expect(reason).toContain('someone-else')
    expect(reason).not.toMatch(/decode|claims/)
  })

  it('VerifyUnknownKeyID: a key whose id does not decode is refused without a name', () => {
    // `!` is not in the base64url alphabet, so there is no id to look up and no
    // name to draw.
    const reason = refusalOf(verifyLicenceKey('!!!!.AAAA.BBBB', at(ISSUED), PRODUCTION_KEYRING))

    expect(reason).toMatch(/id does not decode/)
  })

  it('StagingKeyIDIsRefused: a key minted under a staging id is refused by the production verifier', () => {
    const staging = signer('staging-2026')
    const key = mint(staging, claimsFor())

    const reason = refusalOf(verifyLicenceKey(key, at(ISSUED), PRODUCTION_KEYRING))
    expect(reason).toContain('staging-2026')

    // The same key verifies under the keyring its id names, so the refusal is
    // the ring's and not the key's.
    expect(verified(verifyLicenceKey(key, at(ISSUED), staging.keyring)).state).toBe('valid')
  })

  it('TestKeyIDIsNotInTheProductionKeyring: the ring holds no test id, and every entry is an Ed25519 public key', () => {
    const test = signer('test-2026')

    expect(PRODUCTION_KEYRING[test.id]).toBeUndefined()
    // S2.9 adds the owner's published production key here; what must survive
    // that task is the shape of every entry.
    for (const [id, pem] of Object.entries(PRODUCTION_KEYRING)) {
      expect(createPublicKey(pem).asymmetricKeyType, `${id} is not an Ed25519 public key`).toBe('ed25519')
    }
  })
})

describe('the states', () => {
  const acme = signer('acme-2026')
  const EXPIRES = ISSUED + 365 * DAY
  const key = mint(acme, claimsFor({ expires: EXPIRES }))

  it('GraceIsThirtyDays: valid to expires, grace for 30 days after it, expired past that', () => {
    expect(stateAt(key, ISSUED, acme.keyring)).toBe('valid')
    expect(stateAt(key, EXPIRES, acme.keyring)).toBe('valid')
    expect(stateAt(key, EXPIRES + 1, acme.keyring)).toBe('grace')
    expect(stateAt(key, EXPIRES + GRACE_DAYS * DAY, acme.keyring)).toBe('grace')
    expect(stateAt(key, EXPIRES + GRACE_DAYS * DAY + 1, acme.keyring)).toBe('expired')
  })

  it('ExpiredAfterGrace: a key past its grace is read as expired, not refused', () => {
    const read = verifyLicenceKey(key, at(EXPIRES + (GRACE_DAYS + 1) * DAY), acme.keyring)

    expect(read).toEqual({ ok: true, claims: claimsFor({ expires: EXPIRES }), state: 'expired' })
  })

  it('GraceIsThirtyDays: the grace is the note’s number, and every tier’s maximum is the term plus it', () => {
    expect(GRACE_DAYS).toBe(30)
    for (const tier of TIERS) {
      expect(TIER_MAX_LIFETIME_DAYS[tier], `${tier}'s maximum`).toBe(365 + GRACE_DAYS)
    }
  })
})

describe('the tier’s maximum lifetime', () => {
  const acme = signer('acme-2026')

  it('KeyBeyondTheTierLifetimeIsRefused: the bound is the term plus the 30-day issue lead', () => {
    for (const tier of TIERS) {
      const max = TIER_MAX_LIFETIME_DAYS[tier]
      // The longest key the ruled issuance mints: an annual renewal, minted 30
      // days before the key it follows expires. It is at the ceiling, and it
      // must be admitted -- a bound of the bare term would refuse it.
      const renewal = mint(acme, claimsFor({ tier, expires: ISSUED + max * DAY }))
      expect(verifyLicenceKey(renewal, at(ISSUED), acme.keyring).ok, `${tier} at the ceiling`).toBe(true)

      const past = mint(acme, claimsFor({ tier, expires: ISSUED + max * DAY + 1 }))
      const reason = refusalOf(verifyLicenceKey(past, at(ISSUED), acme.keyring))
      expect(reason, `${tier} past the ceiling`).toMatch(/term longer than its tier allows/)
    }
  })

  it('KeyBeyondTheTierLifetimeIsRefused: the bound is measured from the key’s own issued', () => {
    // The hole the design lists rather than closes: `issued` is the minter's to
    // choose, so a key minted with a future `issued` passes the length check
    // and reads valid under the state table for as long as its minter wants.
    // Held here so that closing it would be a decision and not an accident.
    const max = TIER_MAX_LIFETIME_DAYS.team
    const future = mint(acme, claimsFor({ issued: ISSUED + 10 * 365 * DAY, expires: ISSUED + 10 * 365 * DAY + max * DAY }))

    expect(verified(verifyLicenceKey(future, at(ISSUED), acme.keyring)).state).toBe('valid')
  })
})

describe('two keys installed at once', () => {
  const first = signer('acme-2025')
  const renewal = signer('acme-2026')
  const ring = keyringOf(first, renewal)
  const EARLIER = ISSUED + 365 * DAY
  const LATER = ISSUED + 335 * DAY + 365 * DAY
  const older = mint(first, claimsFor({ issued: ISSUED, expires: EARLIER }))
  const newer = mint(renewal, claimsFor({ issued: ISSUED + 335 * DAY, expires: LATER }))

  it('OverlappingKeysPickTheLatest: the later expires is the key that applies, in any order', () => {
    const now = at(ISSUED + 340 * DAY) // inside both terms: the overlap the design mints for

    for (const store of [
      [older, newer],
      [newer, older],
    ]) {
      const read = verified(latestLicence(store, now, ring))
      expect(read.claims.expires).toBe(LATER)
      expect(read.state).toBe('valid')
    }
  })

  it('OverlappingKeysPickTheLatest: an expired key beside a valid one loses to the valid one', () => {
    const read = verified(latestLicence([newer, older], at(EARLIER + (GRACE_DAYS + 1) * DAY), ring))

    expect(read.claims.expires).toBe(LATER)
    expect(read.state).toBe('valid')
  })

  it('OverlappingKeysPickTheLatest: a key that is refused is skipped, not chosen', () => {
    // The id names the renewal, but the signature is the first signer's: the
    // entry is in the ring, and the key still does not verify.
    const foreign = mint(first, claimsFor({ expires: LATER }), renewal.id)

    expect(verified(latestLicence([foreign, older], at(ISSUED + DAY), ring)).claims.expires).toBe(EARLIER)
  })

  it('OverlappingKeysPickTheLatest: a store with no usable key is null, and a lapsed key is still read', () => {
    // The empty store, and a store whose every key is refused: both are the
    // store's business (S6.1's no-key state), not a fourth state here.
    expect(latestLicence([], at(ISSUED), ring)).toBeNull()
    expect(latestLicence(['not.a.key', older], at(ISSUED), keyringOf())).toBeNull()

    // Past the grace, the state is expired rather than nothing: the console
    // knows it has lapsed.
    const read = verified(latestLicence([older], at(EARLIER + (GRACE_DAYS + 1) * DAY), ring))
    expect(read.state).toBe('expired')
  })
})

describe('the negative vectors', () => {
  const acme = signer('acme-2026')

  it('a re-spelled payload is a different byte string, and the signature refuses it', () => {
    const claims = claimsFor()
    const canonical = JSON.stringify(claims)
    // The same claims, spelled in another order: a verifier that parsed first
    // and compared claims would accept it.
    const respelled = JSON.stringify({
      organisation: claims.organisation,
      seats: claims.seats,
      features: claims.features,
      tier: claims.tier,
      issued: claims.issued,
      expires: claims.expires,
    })
    expect(respelled).not.toBe(canonical)

    const signed = Buffer.from(canonical, 'utf8')
    const key = spell(acme.id, Buffer.from(respelled, 'utf8'), edSign(null, signed, acme.privateKey))

    expect(refusalOf(verifyLicenceKey(key, at(ISSUED), acme.keyring))).toMatch(/signature that does not verify/)
  })

  it('a non-canonical spelling of a part is refused, not decoded to the same bytes', () => {
    const claims = claimsFor()
    const payload = Buffer.from(JSON.stringify(claims), 'utf8')
    const signature = edSign(null, payload, acme.privateKey)
    const part = signature.toString('base64url')

    // The last character of a 64-byte signature carries four bits the decoder
    // ignores: flipping the lowest one decodes to the same bytes.
    const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'
    const last = alphabet.indexOf(part.slice(-1))
    const malleable = part.slice(0, -1) + alphabet[last ^ 1]
    expect(Buffer.from(malleable, 'base64url').equals(signature), 'the vector is not malleable').toBe(true)

    // Spelled by hand rather than through `spell`: re-encoding the bytes would
    // put the canonical spelling back and lose the vector.
    const canonical = spell(acme.id, payload, signature)
    const respelled = canonical.replace(/\.[^.]+$/, `.${malleable}`)
    expect(respelled).not.toBe(canonical)
    expect(refusalOf(verifyLicenceKey(respelled, at(ISSUED), acme.keyring))).toMatch(/signature that does not decode/)

    // And the padded spelling, which a lenient decoder accepts: also refused.
    const padded = canonical.replace(/\.[^.]+$/, `.${part}=`)
    expect(refusalOf(verifyLicenceKey(padded, at(ISSUED), acme.keyring))).toMatch(/signature that does not decode/)
  })

  it('a foreign key id is refused: the id names a key the signature does not belong to', () => {
    const other = signer('other-2026')
    const key = mint(acme, claimsFor(), other.id)

    const reason = refusalOf(verifyLicenceKey(key, at(ISSUED), keyringOf(acme, other)))
    expect(reason).toContain('other-2026')
    expect(reason).toMatch(/signature that does not verify/)
  })

  it('truncated claims are refused after the signature verifies, and no claim is drawn', () => {
    // A *valid* signature over a payload that is not a claim set: the sentence
    // must be about the part, because a claim from an unverified payload is
    // attacker-chosen text.
    const truncated = '{"tier":"team","features":["report'
    const key = spell(acme.id, Buffer.from(truncated, 'utf8'), edSign(null, Buffer.from(truncated, 'utf8'), acme.privateKey))

    const reason = refusalOf(verifyLicenceKey(key, at(ISSUED), acme.keyring))
    expect(reason).toMatch(/claims that are not a licence claim set/)
    expect(reason).not.toContain('report')
  })

  it('a payload that is JSON but not a claim set is refused: an array, a scalar, a missing or mistyped claim', () => {
    for (const payload of [
      '[1,2,3]',
      '"nope"',
      'null',
      '{"tier":"team"}',
      JSON.stringify({ ...claimsFor(), tier: 'premium' }),
      JSON.stringify({ ...claimsFor(), seats: '4' }),
      JSON.stringify({ ...claimsFor(), seats: -1 }),
      JSON.stringify({ ...claimsFor(), issued: 1.5 }),
      JSON.stringify({ ...claimsFor(), organisation: '' }),
      JSON.stringify({ ...claimsFor(), features: ['report', 7] }),
      // A refused payload whose text is hostile: the sentence names the part,
      // and never a value the spelt payload carried.
      JSON.stringify({ ...claimsFor(), seats: '4', organisation: '<script>alert(1)</script>' }),
    ]) {
      const bytes = Buffer.from(payload, 'utf8')
      const key = spell(acme.id, bytes, edSign(null, bytes, acme.privateKey))
      const reason = refusalOf(verifyLicenceKey(key, at(ISSUED), acme.keyring))

      expect(reason, payload).toMatch(/claims that are not a licence claim set/)
      expect(reason, payload).not.toContain('<script>')
    }
  })

  it('a signature that is not 64 bytes is refused', () => {
    const short = spell(acme.id, Buffer.from(JSON.stringify(claimsFor()), 'utf8'), Buffer.alloc(32, 7))

    expect(refusalOf(verifyLicenceKey(short, at(ISSUED), acme.keyring))).toMatch(/not 64 bytes/)
  })

  it('a key that is not three parts is refused, and one surrounded by whitespace is read', () => {
    for (const key of ['', 'a.b', 'a.b.c.d']) {
      expect(refusalOf(verifyLicenceKey(key, at(ISSUED), acme.keyring)), key).toMatch(/three base64url parts/)
    }

    // An empty part is a part that does not decode, named as the part it is.
    const empty = `${b64url(acme.id)}..${b64url('x')}`
    expect(refusalOf(verifyLicenceKey(empty, at(ISSUED), acme.keyring))).toMatch(/has a payload that does not decode/)

    // A key arrives from a file's line or a paste, and neither adds meaning to
    // the whitespace around it.
    const key = mint(acme, claimsFor())
    expect(verifyLicenceKey(`  ${key}\n`, at(ISSUED), acme.keyring).ok).toBe(true)
  })

  it('a hostile id is refused, and the sentence draws it sanitized and never raw', () => {
    const hostile = signer('prod\u001b[31mred')
    const reason = refusalOf(verifyLicenceKey(mint(hostile, claimsFor()), at(ISSUED), PRODUCTION_KEYRING))
    expect(reason).not.toContain('\u001b')

    // A newline in an id is not a label the console draws at all: the sentence
    // falls back to the placeholder rather than carrying a second line.
    const split = signer('prod\nkey')
    const other = refusalOf(verifyLicenceKey(mint(split, claimsFor()), at(ISSUED), PRODUCTION_KEYRING))
    expect(other).not.toContain('\n')
    expect(other.split('\n')).toHaveLength(1)
  })
})

describe('the module itself', () => {
  const source = readFileSync(fileURLToPath(new URL('../../src/main/licence.ts', import.meta.url)), 'utf8')

  /** importSpecifiers lists the module specifiers one source file names, so
   * "this verifier reaches no network" can be a scan and not a promise. */
  function importSpecifiers(text: string): string[] {
    const found: string[] = []
    for (const match of text.matchAll(/^\s*(?:import|export)[^'"]*from\s*'([^']+)'|^\s*import\s*'([^']+)'/gm)) {
      found.push(match[1] ?? match[2] ?? '')
    }
    return found
  }

  const NETWORK_MODULES = ['node:http', 'node:http2', 'node:https', 'node:net', 'node:tls', 'node:dns', 'node:dgram']

  it('LicenceModuleImportsNoNetwork: every module it reaches is node:crypto, and it reads no environment', () => {
    const specifiers = importSpecifiers(source)
    expect(specifiers.length, 'the scan found no imports: it no longer reads the file').toBeGreaterThan(0)

    expect(specifiers.filter((specifier) => !specifier.startsWith('.'))).toEqual(['node:crypto'])
    expect(specifiers.filter((specifier) => NETWORK_MODULES.includes(specifier))).toEqual([])
    // A verifier that read a key out of the environment would be the hand-off
    // S2.7 owns, in the wrong module.
    expect(source).not.toMatch(/process\.env/)
    expect(source).not.toMatch(/require\s*\(/)
  })

  it('LicenceModuleImportsNoNetwork: the scan catches a planted network import', () => {
    const plant = [
      "import { request } from 'node:https'",
      "import { connect } from 'node:net'",
      "import { readFileSync } from 'node:fs'",
    ].join('\n')

    expect(importSpecifiers(plant).filter((specifier) => NETWORK_MODULES.includes(specifier))).toEqual([
      'node:https',
      'node:net',
    ])
  })
})
