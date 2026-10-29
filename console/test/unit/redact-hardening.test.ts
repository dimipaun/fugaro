// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The redactor against the inputs an independent security review beat it with
// (desktop-console plan Task 1.4, SECURITY-SENSITIVE). Every input below was,
// at b65fff5, run through `createRedactor([...]).redactDocument`, passed the
// audit, and would have been written into a committed fixture. Each test is
// named after the class it pins, and each one fails if that class's rule is
// removed or narrowed back to what it was.

import { createHash } from 'node:crypto'

import { describe, expect, it } from 'vitest'

import { FixtureRefusal, loadFixture, writeFixture } from '../../src/source/fixture'
import type { FixtureRecord } from '../../src/source/fixture'
import {
  ADDRESS_MARKER,
  BRANCH_MARKER,
  BUCKET_MARKER,
  HOST_MARKER,
  LITERAL_MARKER,
  PATH_MARKER,
  PERSON_PSEUDONYM_RE,
  PROJECT_MARKER,
  REPO_PSEUDONYM_RE,
  RedactionRefusal,
  TOKEN_MARKER,
  assertRedacted,
  createRedactor,
} from '../../src/source/redact'
import { SYNTHETIC_META } from './synthetic'
import { inert } from '../support/fakeSecrets'

/** red redacts one document and returns its JSON, which is what a fixture
 * would have carried: a test asserts on the text, because a leak is a leak
 * whatever key it sits under. */
function red(value: Record<string, unknown>, literals: readonly string[] = []): string {
  return JSON.stringify(createRedactor(literals).redactDocument(value))
}

/** b64 is base64 of a UTF-8 string. */
function b64(text: string): string {
  return Buffer.from(text, 'utf8').toString('base64')
}

describe('addresses, in any script', () => {
  it('redacts an address whose local part is not ASCII, in a value', () => {
    const out = red({ title: 'ping zorá@widgetco.example' })
    expect(out).not.toContain('widgetco')
    // The local part goes too, accent and all: a rule that matched only the
    // ASCII run before the accent would leave `zor` behind.
    expect(out).not.toContain('zor')
    expect(out).toContain(ADDRESS_MARKER)
  })

  it('redacts an address on an internationalised domain whole, not only its local part', () => {
    const out = red({ title: 'cc zora@wídgetco.example today' })
    expect(out).toBe(JSON.stringify({ title: `cc ${ADDRESS_MARKER} today` }))
  })

  it('redacts an address spelled in compatibility characters (NFKC)', () => {
    // Fullwidth letters and an @ that is only an @ after NFKC.
    const out = red({ title: 'ask ｚｏｒａ＠widgetco.example' })
    expect(out).not.toContain('widgetco')
  })

  it('refuses a key that is a non-ASCII address', () => {
    expect(() => red({ 'zorá@widgetco.example': 1 })).toThrow(/is a key that carries an address/)
    expect(() => {
      assertRedacted({ 'zora@wídgetco.example': 1 })
    }).toThrow(RedactionRefusal)
  })

  it('does not take a marker that only ends like one for the marker', () => {
    // `zora@example.invalid` is not the marker: it still says `zora`.
    expect(() => {
      assertRedacted({ title: 'from zora@example.invalid' })
    }).toThrow(/still holds an address/)
  })
})

describe('branch names, accounts and other shapeless identifiers, by field', () => {
  it('replaces every branch field whole, whatever the name looks like', () => {
    const out = createRedactor().redactDocument({
      runs: [
        { branch: 'acme-merger-codename', base_branch: 'release-2027-secret' },
        { branch: 'dm/q4', head_branch: 'x', ref: 'refs/heads/main' },
      ],
    }) as { runs: Record<string, string>[] }
    expect(out.runs[0]).toEqual({ branch: BRANCH_MARKER, base_branch: BRANCH_MARKER })
    expect(out.runs[1]).toEqual({ branch: BRANCH_MARKER, head_branch: BRANCH_MARKER, ref: BRANCH_MARKER })
    expect(() => {
      assertRedacted({ branch: 'dm/q4' })
    }).toThrow(/not this class's marker/)
  })

  it('replaces oauth_account whole, not only the part that happened to be a literal', () => {
    const out = createRedactor(['zora']).redactDocument({ oauth_account: 'zora-work' }) as Record<string, string>
    expect(out['oauth_account']).toMatch(PERSON_PSEUDONYM_RE)
    expect(() => {
      assertRedacted({ oauth_account: 'zora-work' })
    }).toThrow(/not this class's marker/)
  })

  it('replaces a Cloud Run execution name and a log URL whole', () => {
    const out = createRedactor().redactDocument({
      execution: 'fugaro-run-20261015-abcd',
      log_url: 'console.cloud.google.com/logs?project=acme-devel-zora',
    }) as Record<string, string>
    expect(out['execution']).toBe(LITERAL_MARKER)
    expect(JSON.stringify(out)).not.toContain('acme-devel-zora')
  })
})

describe('hosts, addresses on the network, and GCP names', () => {
  it('redacts a hostname whole', () => {
    const out = red({ reason: 'lookup acme-devel-zora-default-rtdb.firebaseio.com: no such host' })
    expect(out).toBe(JSON.stringify({ reason: `lookup ${HOST_MARKER}: no such host` }))
  })

  it('redacts an IP address and its port, v4 and v6', () => {
    expect(red({ reason: 'dial 10.42.7.13:443 refused' })).toBe(JSON.stringify({ reason: `dial ${HOST_MARKER} refused` }))
    expect(red({ reason: 'dial fe80::1ff:fe23:4567:890a refused' })).not.toContain('fe80')
  })

  it('redacts a GCP project ID, a bucket and a project number named in a sentence', () => {
    expect(red({ reason: 'permission denied on project acme-devel-zora' })).toBe(
      JSON.stringify({ reason: `permission denied on project ${PROJECT_MARKER}` }),
    )
    expect(red({ reason: 'bucket fugaro-runs-acme-devel-zora is gone' })).toBe(
      JSON.stringify({ reason: `bucket ${BUCKET_MARKER} is gone` }),
    )
    expect(red({ reason: 'see projects/123456789012/secrets' })).not.toContain('123456789012')
    expect(red({ reason: 'wrote to fugaro-runs-acme-devel-zora' })).not.toContain('acme-devel-zora')
    expect(red({ reason: 'as fugaro-runner@acme-devel-zora.iam.gserviceaccount.com' })).toBe(
      JSON.stringify({ reason: `as ${ADDRESS_MARKER}` }),
    )
  })

  it('leaves file names and versions alone', () => {
    const value = { title: 'fix redact.ts and README.md, bump to v0.8.1 and node 22.23.3, e.g. config.json' }
    expect(red(value)).toBe(JSON.stringify(value))
  })
})

describe("the installation's literals, however they are spelled", () => {
  const literals = ['fugaro', 'Zoras-Laptop-Pro.local', 'zora']

  it('matches a literal in any case', () => {
    const out = red({ title: 'FUGARO on zoras-laptop-pro.local by Zora' }, literals)
    expect(out.toLowerCase()).not.toContain('fugaro')
    expect(out.toLowerCase()).not.toContain('zora')
    expect(out.toLowerCase()).not.toContain('laptop')
  })

  it("matches a host literal's stem without its .local suffix", () => {
    const out = red({ title: 'ran on Zoras-Laptop-Pro' }, literals)
    expect(out).toBe(JSON.stringify({ title: `ran on ${LITERAL_MARKER}` }))
  })

  it('matches a literal spelled in compatibility characters', () => {
    expect(red({ title: 'by ｚｏｒａ' }, literals)).toBe(JSON.stringify({ title: `by ${LITERAL_MARKER}` }))
  })

  it('refuses a key that carries a literal in another case', () => {
    expect(() => red({ ZORA: 1 }, literals)).toThrow(/is a key that carries one of the installation's own literals/)
  })
})

/** GOOGLE_BODY is the body of a fixed Google access token, assembled from two
 * fragments so that no line here is one: the tests below wrap it, split it and
 * embed it, and every one of them wants the same string. */
const GOOGLE_BODY = inert('a0AfH6SMBx7Kq2pLm9TzW', 'v4Yb7Nc3Pd1Hs6Jf0Ga5Q')

describe('credentials the shape list did not know', () => {
  const ghp = inert('gh', 'p_', 'R8x2KqLm9TzWv4Yb7Nc3Pd1Hs6Jf0Ga5QeUi')

  it.each([
    ['a Google access token', inert('ya', '29.', GOOGLE_BODY)],
    ['a Google refresh token', inert('1/', '/0', 'gAbCdEfGhIjKlMnOpQrStUvWxYz0123456789')],
    ['a URL-encoded token', `ghp%5F${ghp.slice(4)}`],
    ['a base64-wrapped token', b64(ghp)],
    ['a base64-wrapped assignment', b64(`password=hunter2hunter2`)],
    ['an assignment in free text', 'access_token=abcdefghijklmnop12345'],
    ['a quoted assignment', '"private_key_id": "0123456789abcdef0123456789abcdef01234567"'],
    ['a key id', 'private_key_id: 0123456789abcdef0123456789abcdef01234567'],
    ['a long high-entropy run', 'zWv4Yb7Nc3Pd1Hs6Jf0Ga5QeUiR8x2'],
  ])('redacts %s', (_what, secret) => {
    const out = red({ message: `failed with ${secret} at noon` })
    expect(out).toContain(TOKEN_MARKER)
    expect(out).not.toContain(secret)
    expect(out).toContain('at noon')
    expect(() => {
      assertRedacted({ message: `failed with ${secret} at noon` })
    }).toThrow(/credential-shaped/)
  })

  it('keeps a long run whose entropy is below the threshold, and redacts one just above it', () => {
    // The threshold pinned from the other side: "it redacts identity, not
    // hostility ... it does not invent data". Each run below mixes upper case,
    // lower case and digits and is 20+ characters long, so only the entropy
    // decides. Built by concatenation, so no literal here is token-shaped.
    const repeated = 'RunA2026' + 'RunA2026' + 'RunA' // 2.77 bits per character
    const below = 'Ab1Cd2Ef3Gh' + 'Ab1Cd2Ef3Gh' // 3.46 bits per character
    const above = 'Ab1Cd2Ef3GhIj' + 'Ab1Cd2Ef3G' // 3.65 bits per character
    expect(red({ message: `build ${repeated} done` })).toBe(JSON.stringify({ message: `build ${repeated} done` }))
    expect(red({ message: `build ${below} done` })).toBe(JSON.stringify({ message: `build ${below} done` }))
    expect(red({ message: `build ${above} done` })).toBe(JSON.stringify({ message: `build ${TOKEN_MARKER} done` }))
  })

  it('redacts a token split across two fields', () => {
    const out = red({ head: ghp.slice(0, 14), tail: ghp.slice(14) })
    expect(out).not.toContain(ghp.slice(0, 14))
    expect(out).not.toContain(ghp.slice(14))
  })

  it('redacts a private_key_id under its own key', () => {
    expect(red({ private_key_id: 'abc' })).toBe(JSON.stringify({ private_key_id: TOKEN_MARKER }))
  })

  it('leaves run ids, model names, digests under digest keys and ordinary words alone', () => {
    const value = {
      run: '20261015-120000-aa01',
      models: 'claude-sonnet-4-5-20250929',
      title: 'the tokens: 1234 of internationalization',
      sha256: '9c5effc8f53aea4d0adda0635681f691304bfd3057ba2ecf08500650f87adbff',
      pushed_head: '027e90ab4d1c2e3f4a5b6c7d8e9f0a1b2c3d4e5f',
    }
    expect(red(value)).toBe(JSON.stringify(value))
  })
})

describe('pseudonyms', () => {
  it('are keyed per recording, so a name list cannot rebuild them', () => {
    // At b65fff5 these were SHA-256 of the name with a public salt
    // (`fugaro-console-fixture`): anyone with a list of candidate names could
    // recompute every pseudonym in a fixture. None of those may appear now.
    const unkeyed = (value: string): string =>
      createHash('sha256').update(`fugaro-console-fixture\u0000${value}`).digest('hex').slice(0, 8)
    const out = red({ repos: [{ repo: 'acmeowner/acmeweb' }, { repo: 'acmeinc/x' }] })
    for (const name of ['acmeowner', 'acmeweb', 'acmeinc', 'x']) expect(out).not.toContain(unkeyed(name))
    // Two recordings of the same repository do not share a pseudonym...
    const first = createRedactor().redactRepository('acmeowner/acmeweb')
    const second = createRedactor().redactRepository('acmeowner/acmeweb')
    expect(first).toMatch(REPO_PSEUDONYM_RE)
    expect(first).not.toBe(second)
    // ...and one recording keeps the same repository the same pseudonym.
    const one = createRedactor()
    expect(one.redactRepository('acmeowner/acmeweb')).toBe(one.redactRepository('acmeowner/acmeweb'))
  })

  it('never put the key in the redactor or in its output', () => {
    const redactor = createRedactor(['x'], { pseudonymKey: 'a-test-only-key-0123456789' })
    expect(JSON.stringify(redactor)).not.toContain('a-test-only-key')
    expect(Object.keys(redactor)).not.toContain('pseudonymKey')
    expect(JSON.stringify(redactor.redactDocument({ repo: 'a/b' }))).not.toContain('a-test-only-key')
  })
})

describe('people and paths', () => {
  it('applies the person rule to every by-like field', () => {
    const out = createRedactor().redactDocument({
      halt: { by: 'Zora Kell' },
      review: { reviewed_by: 'Zora Kell', user: 'zora', actor: 'zora' },
    }) as { halt: Record<string, string>; review: Record<string, string> }
    expect(out.halt['by']).toMatch(PERSON_PSEUDONYM_RE)
    expect(out.review['reviewed_by']).toMatch(PERSON_PSEUDONYM_RE)
    expect(out.review['user']).toMatch(PERSON_PSEUDONYM_RE)
    expect(out.review['actor']).toMatch(PERSON_PSEUDONYM_RE)
    expect(() => {
      assertRedacted({ halt: { by: 'Zora Kell' } })
    }).toThrow(/halt\.by/)
    // A report's break-down dimension is still a dimension.
    expect(createRedactor().redactDocument({ by: 'person', report: { by: 'model' } })).toEqual({
      by: 'person',
      report: { by: 'model' },
    })
  })

  it('consumes a path with a space or a non-ASCII character whole', () => {
    expect(red({ title: 'read /Users/Zora Kell/secret-plan/x now' })).toBe(
      JSON.stringify({ title: `read ${PATH_MARKER} now` }),
    )
    expect(red({ title: 'read /Users/zorá/secret-plan, then stop' })).toBe(
      JSON.stringify({ title: `read ${PATH_MARKER}, then stop` }),
    )
  })
})

describe('the loader, with no literals in hand', () => {
  const records = (document: Record<string, unknown>): FixtureRecord[] => [{ at: '2026-10-15T12:00:00Z', document }]

  it.each([
    ['a non-ASCII address', { title: 'ping zorá@widgetco.example' }],
    ['a branch field', { branch: 'acme-merger-codename' }],
    ['a hostname', { reason: 'lookup zoras-laptop-pro.local' }],
    ['a token', { message: inert('ya', '29.', GOOGLE_BODY) }],
    ['a GCP project in a sentence', { reason: 'denied on project acme-devel-zora' }],
  ])('refuses a hand-edited fixture carrying %s', (_what, document) => {
    expect(() => loadFixture(writeFixture(records(document), SYNTHETIC_META), 'edited.ndjson')).toThrow(FixtureRefusal)
  })
})

describe('every input above, redacted twice', () => {
  it('is idempotent and audits clean', () => {
    const hostile = {
      title: 'ping zorá@widgetco.example on Zoras-Laptop-Pro via 10.42.7.13:443',
      reason: 'permission denied on project acme-devel-zora, bucket fugaro-runs-acme-devel-zora',
      message: `access_token=abcdefghijklmnop12345 then ${inert('ya', '29.', GOOGLE_BODY)}`,
      note: 'read /Users/Zora Kell/secret-plan/x and lookup acme-devel-zora-default-rtdb.firebaseio.com',
      runs: [{ branch: 'dm/q4', base_branch: 'release-2027-secret', oauth_account: 'zora-work', halt: { by: 'Zora Kell' } }],
      ＦＵＧＡＲＯ: 1,
    }
    const redactor = createRedactor(['fugaro', 'Zoras-Laptop-Pro.local', 'zora'])
    expect(() => redactor.redactDocument(hostile)).toThrow(/is a key that carries/)
    const { ＦＵＧＡＲＯ: _key, ...rest } = hostile
    const once = redactor.redactDocument({ ...rest, value: 'ＦＵＧＡＲＯ' })
    expect(redactor.redactDocument(once)).toEqual(once)
    expect(redactor.problems(once)).toEqual([])
    expect(JSON.stringify(once).toLowerCase()).not.toMatch(/zora|fugaro|widgetco|acme-devel|secret|laptop|10\.42/)
  })
})
