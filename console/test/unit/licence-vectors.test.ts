// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// One set of vectors for all three verifiers (subscriptions plan S2.6; the
// design is docs/licensing/licence-key.md, and S2.4 is the verifier this file
// reads them with).
//
// `ee/licence/testdata/vectors.json` is the plan's one set: the claim sets,
// states and refusals the Go verifier (S2.3), this console's (S2.4) and the
// app's (S2.5) are all read against, so a key that one of them accepts and
// another refuses is a defect no one of their own suites can see. This file
// reads that file **as it is written** -- the key texts verbatim, the ids as
// the text they are, the ring as the file spells it -- and asserts the
// console's answer for every vector and every installed-key pair. The app's
// sibling (`mobile/src/licence/vectors.test.js`) does the same, and the Go
// verifier's `TestVectorsRoundTrip` is the third reader.
//
// **What "the same state" means for a refusal.** The file carries a `refused`
// name for a key a verifier must turn away, drawn from the five the three
// verifiers share (malformed, unknown-key-id, signature, claims, lifetime),
// and a `state` for one it must read (valid, grace, expired). The console
// answers a refusal with a sentence rather than a code (S2.4's contract: every
// answer is a `LicenceRead`, so the main process has something to draw), so
// this file holds each sentence to the one name it means -- and holds the file
// to carrying all eight, so a vector set that quietly lost its negative half
// cannot be green here.
//
// **The ring is the file's, spelled the way the console holds it.** The file
// carries each public half as the 32 raw bytes of an Ed25519 key in unpadded
// base64url, which is what the app's verifier takes; the console's ring holds
// a PEM, the shape S2.9 hand-edits. The id is not translated: the key text the
// file carries is read verbatim, because one minted key text has to verify in
// the CLI, the console and the app alike.
//
// **No key material is in this file or the file it reads.** The vectors carry
// public halves and signed key texts and no private half anywhere: every key
// in them was minted by a throwaway keypair generated in the process that
// wrote the file.

import { createPublicKey } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { latestLicence, verifyLicenceKey, type LicenceKeyring, type LicenceRead } from '../../src/main/licence'

/** Vector is one accepted or refused key in the file. */
interface Vector {
  readonly name: string
  readonly key: string
  readonly state?: string
  readonly claims?: Record<string, unknown>
  readonly refused?: string
}

/** InstalledCase is one set of keys installed at once, and the key that must apply. */
interface InstalledCase {
  readonly name: string
  readonly keys: Readonly<Record<string, string>>
  readonly applies: string
  readonly state: string
}

interface VectorsFile {
  readonly now: number
  readonly keyring: Readonly<Record<string, string>>
  readonly vectors: readonly Vector[]
  readonly installed: readonly InstalledCase[]
}

const FILE: VectorsFile = JSON.parse(
  readFileSync(fileURLToPath(new URL('../../../ee/licence/testdata/vectors.json', import.meta.url)), 'utf8'),
) as VectorsFile

/** NOW is the clock every vector in the file is read at. */
const NOW = new Date(FILE.now * 1000)

/** ED25519_SPKI_PREFIX is the twelve bytes every Ed25519 public key's DER
 * spelling starts with, so a raw 32-byte key -- the spelling the vectors file
 * carries -- becomes the PEM the console's ring holds. */
const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex')

/** RING is the file's own ring, each public half re-spelled as the console
 * holds it: the id exactly as the file writes it, and no other. That is what
 * makes the two keyring vectors the file carries read as refusals for the id
 * alone. */
const RING: LicenceKeyring = Object.fromEntries(
  Object.entries(FILE.keyring).map(([id, raw]) => [
    id,
    createPublicKey({
      key: Buffer.concat([ED25519_SPKI_PREFIX, Buffer.from(raw, 'base64url')]),
      format: 'der',
      type: 'spki',
    })
      .export({ type: 'spki', format: 'pem' })
      .toString(),
  ]),
)

/** REFUSALS maps each of the five shared refusal names to the console sentence
 * that means it. The order matters only where a later pattern would also match
 * an earlier sentence; each entry here matches exactly the sentences whose
 * meaning its name is. */
const REFUSALS: readonly { readonly name: string; readonly sentence: RegExp }[] = [
  { name: 'unknown-key-id', sentence: /is not one the console holds/ },
  { name: 'lifetime', sentence: /a term longer than its tier allows/ },
  { name: 'signature', sentence: /signature that does not verify|signature that is not 64 bytes/ },
  { name: 'claims', sentence: /claims that are not a licence claim set/ },
  { name: 'malformed', sentence: /three base64url parts|not spelled in base64url|does not decode/ },
]

/** refusalNames is the five names the file may spell a refusal with. */
const REFUSAL_NAMES = REFUSALS.map((refusal) => refusal.name)

/** STATES is the three states an accepted key may be in. */
const STATES = ['valid', 'grace', 'expired'] as const

/** refusalOf reads one refusal's shared name out of the console's sentence. */
function refusalOf(read: LicenceRead): string {
  if (read.ok) throw new Error('the key verified and this case expects a refusal')
  const named = REFUSALS.filter((refusal) => refusal.sentence.test(read.reason)).map((refusal) => refusal.name)
  if (named.length !== 1) {
    throw new Error(`the sentence ${JSON.stringify(read.reason)} is not exactly one of the five shared refusals: ${named.join(', ')}`)
  }
  return named[0]!
}

describe('the shared licence vectors', () => {
  it('the file is the one set of vectors S2.6 names, and carries both halves', () => {
    expect(Object.keys(FILE.keyring)).toHaveLength(1)
    expect(FILE.vectors.length).toBeGreaterThanOrEqual(10)
    expect(FILE.installed.length).toBeGreaterThanOrEqual(2)
  })

  for (const vector of FILE.vectors) {
    it(`${vector.name}: the console reads it as the file says`, () => {
      if (vector.refused !== undefined) {
        expect(REFUSAL_NAMES, `the vector names the refusal ${JSON.stringify(vector.refused)}`).toContain(vector.refused)
        expect(refusalOf(verifyLicenceKey(vector.key, NOW, RING))).toBe(vector.refused)
        return
      }

      expect(vector.claims, 'an accepted vector carries no claim set, so nothing holds the payload to the terms it states').toBeDefined()
      const read = verifyLicenceKey(vector.key, NOW, RING)
      expect(read.ok, `${vector.name} is refused as ${read.ok ? '' : read.reason}`).toBe(true)
      if (!read.ok) return
      expect(read.state).toBe(vector.state)
      expect(read.claims).toEqual(vector.claims)
    })
  }

  for (const installed of FILE.installed) {
    it(`${installed.name}: the console picks the key the file says applies`, () => {
      const applies = verifyLicenceKey(installed.keys[installed.applies]!, NOW, RING)
      expect(applies.ok, `the key this case says applies is refused: ${applies.ok ? '' : applies.reason}`).toBe(true)

      // The pair is only interesting while both keys are good, so a case whose
      // other key were refused would prove nothing about the choice.
      for (const [name, key] of Object.entries(installed.keys)) {
        if (name === installed.applies) continue
        expect(verifyLicenceKey(key, NOW, RING).ok, `the ${name} key of the pair is refused too`).toBe(true)
      }

      const keys = Object.values(installed.keys)
      expect(latestLicence(keys, NOW, RING)).toEqual(applies)

      // A map has no order, so the file's order is not the order the store
      // reads: the pair the other way round is the same answer.
      expect(latestLicence([installed.keys[installed.applies]!, ...keys.filter((key) => key !== installed.keys[installed.applies])], NOW, RING)).toEqual(applies)

      const read = latestLicence(keys, NOW, RING)
      expect(read?.ok && read.state).toBe(installed.state)
    })
  }

  it('the file carries every refusal and every state the three verifiers share', () => {
    const seen = new Set(FILE.vectors.map((vector) => vector.refused ?? vector.state))
    for (const name of [...REFUSAL_NAMES, ...STATES]) {
      expect(seen, `no vector in the file is ${name}`).toContain(name)
    }
  })
})
