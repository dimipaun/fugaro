// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's own keeper of the licence key, and the hand-off that carries it
// to the child (subscriptions plan S2.7; the design is
// docs/licensing/licence-key.md, whose "Storage and hand-off (F41)" section is
// the table this file is written against).
//
// Four things are held here, and each is a rule of that design rather than an
// implementation detail:
//
//   - **a stored key survives a restart.** The console's row of F41's table is
//     "the console's own store on the machine, one per installation", so what a
//     paste installs is read back by the *next* console process -- the file,
//     the modes it is kept at, and the fact that a key is read out of it rather
//     than a directory being searched. A store that only remembered the key in
//     memory would pass every case about the screen and lose the key on quit.
//   - **a bad key is refused, and a refusal changes nothing.** The key is
//     verified *before* it is kept (`verifyLicenceKey`, S2.4), so a key with a
//     signature that does not verify, or an id this build does not hold, is
//     answered with the verifier's own sentence -- and the installation keeps
//     the key it had. A refusal that overwrote the store would lose a working
//     installation to a mistyped paste.
//   - **the key reaches the child in its environment and never in its argv.**
//     F41's sentence is the reason: "a key in argv is a key every local process
//     reads". So the hand-off is one `CHILD_ENV_KEYS` entry, and the argv the
//     plan spawns -- and the line the spawn log records, which is
//     `spawnedArgv(plan)` -- carries no trace of the key.
//   - **the console's own environment is not a source of the key.** The key is
//     handed over as a value the *store* read, never as one this process
//     inherited: a `FUGARO_LICENCE_KEY` in the console's environment is planted
//     below and must not reach the child. That is the half a merge with
//     `process.env` would silently undo, and it is the half an independent
//     review of this task is asked to look at.
//
// **What is deliberately not here.** The verifier's own cases are
// `test/unit/licence.test.ts`'s (S2.4). The read-only state a lapse puts the
// console in is S6.1's design and S6.2's build. And the *screen* -- the paste,
// the figures and the refusal it draws -- is held by the renderer's own tests,
// because what reaches a renderer is the facts and never the key: this file
// asserts that the only way out of the store is `keyFor`, which the child
// environment is built from.
//
// **No key material is in this file.** Every key is generated at run time and
// every signature is computed from it, exactly as in `licence.test.ts`.
//
// The red step this file was written for: every case fails before
// `src/main/licence-store.ts` exists -- and the argv case is the one the plan
// names, because a store that handed the key over in the argv would satisfy
// "it survives a restart" while breaking the design's first rule.

import { generateKeyPairSync, sign as edSign, type KeyObject } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'

import { afterEach, describe, expect, it } from 'vitest'

import {
  LICENCE_DIR_MODE,
  LICENCE_FILE_MODE,
  createLicenceStore,
  readLicenceFile,
  refusedLicenceStore,
} from '../../src/main/licence-store'
import { verifyLicenceKey, type LicenceClaims, type LicenceKeyring } from '../../src/main/licence'
import { LICENCE_KEY_VAR, planFor, spawnedArgv } from '../../src/main/spawn'

const DAY = 86_400

/** ISSUED is the moment every key in this file is minted at, and NOW is the
 * moment every read is taken at: fixed Unix seconds rather than clocks,
 * because a state is a fact about a key and a `now`, and a case that read the
 * clock would be a case that changes. */
const ISSUED = 1_800_000_000
const NOW = ISSUED + DAY

const PROJECT = 'acme'

/** PROJECT is the installation the store's key belongs to; a second name is
 * used where a case holds two installations apart. */
const OTHER_PROJECT = 'bravo'

/** Signer is one test key: an Ed25519 pair generated at run time, the keyring
 * that holds its public half, and the id its keys are spelled under. */
interface Signer {
  readonly id: string
  readonly privateKey: KeyObject
  readonly keyring: LicenceKeyring
}

/** signer mints one test keypair. The PEM is the shape a keyring entry has: the
 * public half in the clear, which is all a verifier may hold. */
function signer(id: string): Signer {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519')
  const pem = publicKey.export({ type: 'spki', format: 'pem' })
  return { id, privateKey, keyring: { [id]: pem } }
}

/** b64url is the spelling every part of a key uses: base64url, unpadded. */
function b64url(text: string): string {
  return Buffer.from(text, 'utf8').toString('base64url')
}

/** mint signs one claim set as it is spelled. `payloadText` is the text the key
 * both embeds and signs, so a vector whose payload is not a claim set is still
 * a key a verifier has to read and refuse (`tampered` spells the other one). */
function mint(s: Signer, claims: LicenceClaims, payloadText = JSON.stringify(claims)): string {
  const payload = Buffer.from(payloadText, 'utf8')
  return [
    b64url(s.id),
    payload.toString('base64url'),
    Buffer.from(edSign(null, payload, s.privateKey)).toString('base64url'),
  ].join('.')
}

/** tampered replaces a key's payload with text its signature does not cover:
 * the id and the signature stay the ones minted, and the claim set beside them
 * is another one -- the shape a key edited in place has. `mint` cannot spell it,
 * because it signs the payload it embeds. */
function tampered(key: string, payloadText: string): string {
  const parts = key.split('.')
  return [parts[0] ?? '', b64url(payloadText), parts[2] ?? ''].join('.')
}

/** claimsFor is a claim set of the shape the design fixes, with the fields a
 * case cares about overridden. */
function claimsFor(overrides: Partial<LicenceClaims> = {}): LicenceClaims {
  return {
    tier: 'team',
    features: ['report'],
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

const directories: string[] = []

/** storePath answers a path **the store creates**: the directory is one this
 * case names and does not make, so a case can hold the mode the store keeps it
 * at (`LICENCE_DIR_MODE`) rather than the mode `mkdtemp` gave the temp root. */
function storePath(): string {
  const root = mkdtempSync(join(tmpdir(), 'fugaro-licence-'))
  directories.push(root)
  return join(root, 'console', 'licence.json')
}

afterEach(() => {
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

describe('the console’s licence-key store', () => {
  it('keeps a key across a restart, in its own file at the modes the design fixes', () => {
    const path = storePath()
    const s = signer('acme-2026')
    const key = mint(s, claimsFor())

    const first = createLicenceStore({ path, keyring: s.keyring })
    const saved = first.save(PROJECT, key, at(NOW))
    expect(saved.ok).toBe(true)
    if (!saved.ok) return
    expect(saved.reply.state).toBe('valid')

    // The restart: a second store over the same path, with nothing carried over
    // but the file.
    const second = createLicenceStore({ path, keyring: s.keyring })
    const reply = second.read(PROJECT, at(NOW))
    expect(reply.state).toBe('valid')
    if (reply.state !== 'valid') return
    expect(reply.claims).toEqual(claimsFor())
    // The key itself, which is what a child is handed.
    expect(second.keyFor(PROJECT, at(NOW))).toBe(key)

    // The file is the console's own store and is kept like a credential: mode
    // 0600 in a directory of the console's own, mode 0700.
    expect(statSync(path).mode & 0o777).toBe(LICENCE_FILE_MODE)
    expect(statSync(dirname(path)).mode & 0o777).toBe(LICENCE_DIR_MODE)
    // The whole of it: the file holds the key, because the file *is* the store
    // F41's table gives the console. What the design keeps the key out of is a
    // log, an error message and an argv -- held below and in the hand-off's own
    // cases -- and a store that held nothing would be a console that forgot the
    // key on quit.
    expect(readFileSync(path, 'utf8')).toContain(key)
  })

  it('answers one installation’s key for that installation only', () => {
    const path = storePath()
    const s = signer('acme-2026')
    const store = createLicenceStore({ path, keyring: s.keyring })
    const acme = mint(s, claimsFor())
    const bravo = mint(s, claimsFor({ organisation: 'bravo.example', tier: 'community' }))

    expect(store.save(PROJECT, acme, at(NOW)).ok).toBe(true)
    expect(store.save(OTHER_PROJECT, bravo, at(NOW)).ok).toBe(true)

    expect(store.keyFor(PROJECT, at(NOW))).toBe(acme)
    expect(store.keyFor(OTHER_PROJECT, at(NOW))).toBe(bravo)
    // An installation nothing was pasted for is F43's no-key state and not an
    // error: the store holds none for it and says so with an empty answer.
    expect(store.keyFor('charlie', at(NOW))).toBeUndefined()
    expect(store.read('charlie', at(NOW)).state).toBe('none')
    // And the two survive together: the second paste did not replace the first.
    const restarted = createLicenceStore({ path, keyring: s.keyring })
    expect(restarted.keyFor(PROJECT, at(NOW))).toBe(acme)
    expect(restarted.keyFor(OTHER_PROJECT, at(NOW))).toBe(bravo)
  })

  it('refuses a key that does not verify, and keeps what the installation had', () => {
    const path = storePath()
    const good = signer('acme-2026')
    const foreign = signer('acme-2027')
    const store = createLicenceStore({ path, keyring: good.keyring })
    const key = mint(good, claimsFor())
    expect(store.save(PROJECT, key, at(NOW)).ok).toBe(true)

    // Three refusals the verifier already knows (S2.4): an id this build does
    // not hold, a payload the signature does not cover, and a payload that is
    // not a claim set at all.
    const refusals = [
      mint(foreign, claimsFor()),
      tampered(key, JSON.stringify(claimsFor({ seats: 400 }))),
      mint(good, claimsFor(), 'not a claim set'),
    ]
    for (const refused of refusals) {
      const answer = store.save(PROJECT, refused, at(NOW))
      expect(answer.ok).toBe(false)
      if (answer.ok) continue
      // The sentence is the verifier's own and names the key, never a claim
      // (S2.4's rule 2).
      expect(answer.reason).toBe(
        ((): string => {
          const read = verifyLicenceKey(refused, at(NOW), good.keyring)
          if (read.ok) throw new Error('this case expects a refusal')
          return read.reason
        })(),
      )
    }

    // Nothing changed: the key the installation had is still the one that
    // applies, and the refused text was not written anywhere.
    expect(store.keyFor(PROJECT, at(NOW))).toBe(key)
    expect(readFileSync(path, 'utf8')).not.toContain(refusals[0] ?? '')
    const restarted = createLicenceStore({ path, keyring: good.keyring })
    expect(restarted.keyFor(PROJECT, at(NOW))).toBe(key)
  })

  it('never hands over a key it cannot verify', () => {
    const path = storePath()
    const good = signer('acme-2026')
    const foreign = signer('acme-2027')
    // A file written by something other than this console: the store reads it
    // as it finds it, and a key nothing verifies is a store holding a key
    // nothing verifies rather than a fourth state (the design's "states and
    // minting").
    const bad = mint(foreign, claimsFor())
    // This case writes the file itself rather than through `save`, so it makes
    // the directory too: every other case here lets the store create it
    // (`storePath`'s note), and a `writeFileSync` into a directory that is not
    // there is an ENOENT rather than a store holding a foreign key.
    mkdirSync(dirname(path), { recursive: true, mode: LICENCE_DIR_MODE })
    writeFileSync(
      path,
      `${JSON.stringify({ version: 1, installations: [{ project: PROJECT, key: bad }] })}\n`,
      { flag: 'w', mode: LICENCE_FILE_MODE },
    )
    const store = createLicenceStore({ path, keyring: good.keyring })
    // A state a screen draws, and not an absent key: the installation has a key
    // and nothing verifies it, which is the thing the operator has to see.
    expect(store.read(PROJECT, at(NOW)).state).toBe('refused')
    // The hand-off is the half that matters: a child is handed a key or
    // nothing, and a refused key is nothing.
    expect(store.keyFor(PROJECT, at(NOW))).toBeUndefined()
  })

  it('hands the key to the child in its environment and never in its argv', () => {
    const s = signer('acme-2026')
    const key = mint(s, claimsFor())
    const plan = planFor(
      'budget.show',
      {},
      {
        target: { project: PROJECT },
        binaryPath: '/opt/homebrew/bin/fugaro',
        directory: '/tmp/not-a-real-checkout',
        appEnv: {},
        licenceKey: key,
      },
    )
    expect(plan.env[LICENCE_KEY_VAR]).toBe(key)

    // **Never in argv**, on the array the plan spawns and on the line the spawn
    // log records (`spawnedArgv` is what `runToCompletion` writes). A key in
    // either is a key every local process reads, which is F41's first sentence.
    for (const word of plan.argv) expect(word).not.toContain(key)
    for (const word of spawnedArgv(plan)) expect(word).not.toContain(key)
    expect(plan.argv.join(' ')).not.toContain(key)
  })

  it('does not hand over a key the console’s own environment carries', () => {
    const s = signer('acme-2026')
    const planted = mint(s, claimsFor())
    const plan = planFor(
      'budget.show',
      {},
      {
        target: { project: PROJECT },
        binaryPath: '/opt/homebrew/bin/fugaro',
        directory: '/tmp/not-a-real-checkout',
        // The app's own environment, saturated with the variable: the store is
        // the only source of a handed-over key, and `childEnvironment` copies
        // this environment for six named keys and nothing else.
        appEnv: { [LICENCE_KEY_VAR]: planted, HOME: '/Users/owner' },
      },
    )
    expect(plan.env[LICENCE_KEY_VAR]).toBeUndefined()
    for (const word of spawnedArgv(plan)) expect(word).not.toContain(planted)
  })

  it('reads a file of its own shape only', () => {
    // The closed schema, held on the reader rather than through a store: a file
    // with a key the console did not write is refused whole, so a field a later
    // version adds is a version bump rather than a silently ignored key.
    expect(readLicenceFile('{"version":1,"installations":[]}').ok).toBe(true)
    expect(readLicenceFile('{"version":2,"installations":[]}').ok).toBe(false)
    expect(readLicenceFile('{"version":1,"installations":[],"keys":[]}').ok).toBe(false)
    expect(readLicenceFile('{"version":1,"installations":[{"project":"acme"}]}').ok).toBe(false)
    expect(readLicenceFile('not json').ok).toBe(false)
  })

  it('starts with no store at all where the console may not keep one', () => {
    // The state `installationStorePath` refuses (review S7): the console starts,
    // and this store answers the refusal rather than throwing at startup.
    const store = refusedLicenceStore('the console refuses to keep its records there')
    expect(store.keyFor(PROJECT, at(NOW))).toBeUndefined()
    expect(store.read(PROJECT, at(NOW)).state).toBe('refused')
    const saved = store.save(PROJECT, 'not.a.key', at(NOW))
    expect(saved.ok).toBe(false)
  })
})
