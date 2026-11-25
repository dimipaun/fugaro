// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The text path: every string the CLI wrote is untrusted, and this is where it
// stops being able to be anything but text (desktop-console plan Task 2.0;
// design section 5.1 rule 7; console/docs/json-contract.md section 9 rule 4).
//
// The design's rule is that the sanitization `watch` applies is **re-applied at
// the display layer** rather than trusted to have survived the subprocess
// boundary, and the plan's Review Focus for this phase is one question: *can a
// job-written string become code -- a markup assignment, an unescaped
// attribute, a URL built from a title?* This file answers it four ways:
//
//   1. **the hostile-title corpus** -- control characters, an ANSI escape
//      sequence, a script tag, a `javascript:` URL, a very long line -- through
//      `sanitizeText` and then through `app.ts`'s writers, into a fake element
//      every markup-parsing property of which THROWS. The assertion is not "the
//      text looks safe"; it is that the only write that happened was a text
//      write, so a path that stopped being a text node fails here rather than
//      in an operator's window.
//   2. **a test per class** -- run titles, branch names, repository names,
//      slugs, project names, people, run ids, labels, hosts, paths and URLs,
//      each with what it accepts, what it refuses and what it rewrites.
//      Deny-by-default is only real if every class says no to something.
//   3. **a scan of `console/src/renderer/`** for the markup-parsing properties
//      and for the numeric formatters `availability.ts` exists to replace. A
//      rule that lives only in a reviewer's head is a rule a later task drops,
//      so the scan is here, it is self-checked against a planted assignment,
//      and it needs the directory not to be empty.
//   4. **the recorder agreements** -- the markers, the bounds, the credential
//      patterns and the keep-or-drop decision on identifier-shaped text are all
//      held equal to Task 1.4's `src/source/redact.ts`, by running both modules
//      over the same inputs. The two cannot import each other (the redactor
//      reaches for `node:crypto`; `shared/` must import nothing at runtime), so
//      drift is caught here or not at all.
//
// **No line of this file contains a credential-shaped string**, and that is a
// rule rather than a style: a fake token committed to a public repository is
// refused by push protection, flagged by every scanner, and indistinguishable
// from a leaked one to a reader without the diff. Every secret-shaped input
// comes from `test/support/fakeSecrets.ts`, which assembles them at run time.

import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { MAX_URL_LENGTH as IPC_MAX_URL_LENGTH } from '../../src/main/ipc'
import {
  ADDRESS_MARKER,
  BRANCH_MARKER,
  BUCKET_MARKER,
  HOST_MARKER,
  LITERAL_MARKER,
  MARKERS,
  MAX_REDACTABLE_STRING_LENGTH,
  PATH_MARKER,
  PROJECT_MARKER,
  TOKEN_MARKER,
  URL_MARKER,
  createRedactor,
} from '../../src/source/redact'
import { UNAVAILABLE_MARK, measured } from '../../src/shared/availability'
import type { Reading } from '../../src/shared/availability'
import {
  ADDRESS_PLACEHOLDER,
  BRANCH_PLACEHOLDER,
  BUCKET_PLACEHOLDER,
  HOST_PLACEHOLDER,
  LITERAL_PLACEHOLDER,
  MARKERS as SHARED_MARKERS,
  MAX_IMAGE_REF_LENGTH,
  MAX_LABEL_LENGTH,
  MAX_REPO_PATTERN_LENGTH,
  MAX_REPOSITORY_LENGTH,
  MAX_TEXT_INPUT_LENGTH,
  MAX_TEXT_LENGTH,
  MAX_URL_LENGTH,
  PATH_PLACEHOLDER,
  PROJECT_PLACEHOLDER,
  TEXT_CLASSES,
  TOKEN_PLACEHOLDER,
  TOKEN_SHAPES,
  URL_PLACEHOLDER,
  isMarkerText,
  nameableText,
  punycodeDecode,
  safeUrl,
  sanitizeRefusal,
  sanitizeText,
} from '../../src/shared/sanitize'
import type { SanitizeFlag, TextClass } from '../../src/shared/sanitize'
import { COORDINATOR_PR_DOCUMENT_KEYS, wireTextClass } from '../../src/shared/schema'
import { DESTINATION_ATTRIBUTE, putLink, putReading, putText } from '../../src/renderer/app'
import type { ElementLike } from '../../src/renderer/app'
import * as fake from '../support/fakeSecrets'

/**
 * FakeElement is a DOM element that refuses to be anything but a text node plus
 * attributes. Every property and method that could turn a string into code --
 * the markup-parsing slots this interface could grow, the parsing methods, the
 * navigating attribute, and any event or resource attribute -- throws, so a
 * writer in `app.ts` that reached for one fails the test that wrote the text
 * rather than a security review months later.
 *
 * It is a structural stand-in for `ElementLike`, not a DOM implementation: the
 * unit suite runs under plain Node (there is no `document`), and the point is
 * the OBSERVATION -- `writes` records every mutation, so a test can assert that
 * a refusal wrote text and attributes and nothing else. `textContent` is an
 * accessor rather than a field for exactly that reason: a plain field would
 * record a write that never happened in the ledger, and `app.ts` assigns the
 * property rather than calling a method.
 */
class FakeElement implements ElementLike {
  readonly attributes = new Map<string, string>()
  readonly writes: string[] = []
  private stored: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set([
    // The navigating attribute and its resource siblings: a writer that set one
    // of these from a document would make the renderer navigate.
    ['h', 'ref'].join(''),
    'src',
    'srcdoc',
    'style',
    'formaction',
    'action',
  ])

  private assertAllowed(name: string): void {
    const lowered = name.toLowerCase()
    if (FakeElement.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a renderer may not set the ${name} attribute from a job's text`)
    }
  }

  get textContent(): string | null {
    return this.stored
  }

  set textContent(value: string | null) {
    this.stored = value
    this.writes.push('textContent')
  }

  setAttribute(name: string, value: string): void {
    this.assertAllowed(name)
    this.attributes.set(name, value)
    this.writes.push(`setAttribute(${name})`)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
    this.writes.push(`removeAttribute(${name})`)
  }
}

/** element is a fresh fake, typed so a writer cannot widen it. */
function element(): FakeElement {
  return new FakeElement()
}

/** text is `sanitizeText` for a value asserted to survive as text, so a test
 * that means "this is drawn as itself" says so in one line. */
function text(raw: unknown, textClass: TextClass = 'sentence', field = 'the value'): string {
  const result = sanitizeText(raw, textClass, field)
  if (!result.ok) throw new Error(`refused: ${result.reason}`)
  return result.text
}

/** refused is `sanitizeText` for a value expected to be refused, returning the
 * sentence so a test can assert what it says -- and assert what it does NOT
 * say. */
function refused(raw: unknown, textClass: TextClass, field = 'the value'): string {
  const result = sanitizeText(raw, textClass, field)
  if (result.ok) throw new Error(`expected a refusal, got ${JSON.stringify(result.text)}`)
  return result.reason
}

/** flags is the flag list of a value expected to be accepted. */
function flags(raw: unknown, textClass: TextClass, field = 'the value'): readonly SanitizeFlag[] {
  const result = sanitizeText(raw, textClass, field)
  if (!result.ok) throw new Error(`refused: ${result.reason}`)
  return result.flags
}

/** PRIVATE_KEY_LABEL is the tail of a PEM header, assembled here for the same
 * reason `fakeSecrets.ts` assembles the whole block: no line of this repository
 * carries the contiguous shape a scanner looks for. */
const PRIVATE_KEY_LABEL = ['PRIVATE', ' KEY-----'].join('')

/**
 * HOSTILE is the plan's own corpus of hostile titles, plus the two its Review
 * Focus names. Each is a string a job's container could have written into a
 * `title`, and each is legal JSON.
 */
const HOSTILE: readonly string[] = Object.freeze([
  'a title with a \u0000 nul and a \u0007 bell',
  'an escape: \u001b[31mred\u001b[0m and a screen clear: \u001b[2J',
  'a script tag: <script>alert(1)</script>',
  'an attribute break: " onmouseover="alert(1)',
  'a javascript URL: javascript:alert(document.cookie)',
  'a data URL: data:text/html,<script>alert(1)</script>',
  'a bidi override: evil/\u202egpj.exe',
  'a soft hyphen: pay\u00adpal',
  'a template payload: ${process.exit(1)} `id` $(id)',
  `a very long line: ${'x'.repeat(4096)}`,
])

describe('the hostile-title corpus', () => {
  it('renders as text, in a text node, and never as markup', () => {
    for (const hostile of HOSTILE) {
      const target = element()
      putText(target, hostile, 'sentence', 'runs[0].title')
      // The whole claim, in one line: the only write that happened was a text
      // write, and the fake's forbidden properties never fired. A writer that
      // reached for any of them would have thrown before this assertion.
      expect(target.writes, hostile).toEqual(['textContent'])
      expect(target.textContent, hostile).not.toBeNull()
      expect(target.textContent, hostile).not.toBe(UNAVAILABLE_MARK)
    }
  })

  it('strips what is not visible, and keeps the sentence', () => {
    // A control character or a format character is removed; the visible text
    // around it survives, because a title that vanished entirely would be a
    // worse lie than an ugly one.
    expect(text('a title with a \u0000 nul', 'sentence')).toBe('a title with a nul')
    expect(text('an escape: \u001b[31mred\u001b[0m', 'sentence')).toBe('an escape: red')
    expect(text('pay\u00adpal', 'sentence')).toBe('paypal')
    // The bidi override is gone, and the text it was hiding behind is drawn as
    // it came: `evil` and `gpj.exe` are two four-character segments around a
    // slash, which the recorder calls a repository path and this layer no longer
    // does (D3213). The override cannot reorder what is drawn, which is the
    // point of removing it; the pair is a pair of words.
    expect(text('evil/\u202egpj.exe', 'sentence')).toBe('evil/gpj.exe')
    expect(flags('pay\u00adpal', 'sentence')).toContain('stripped-invisible')
  })

  it('flattens to one line and clips a very long one, marking the cut', () => {
    expect(text('one\ntwo\r\nthree\tfour', 'sentence')).toBe('one two three four')
    const long = text('x'.repeat(4096), 'sentence')
    expect(long).toHaveLength(MAX_TEXT_LENGTH)
    expect(long.endsWith('...')).toBe(true)
    expect(flags('x'.repeat(4096), 'sentence')).toContain('clipped')
    // A value exactly at the bound is not marked as cut: a title that happens
    // to be 512 characters is not a title the console shortened.
    expect(flags('x'.repeat(MAX_TEXT_LENGTH), 'sentence')).not.toContain('clipped')
  })

  it('never splits a surrogate pair when it clips', () => {
    // A 512-character cut can land between the two halves of a surrogate pair,
    // and half an emoji is not text -- it is a lone surrogate, which is not even
    // valid UTF-16, and which some renderers draw as a replacement glyph.
    const cut = text('\u{1F600}'.repeat(400), 'sentence')
    expect(cut.endsWith('...')).toBe(true)
    const body = cut.slice(0, -3)
    for (let index = 0; index < body.length; index += 1) {
      const unit = body.charCodeAt(index)
      if (unit >= 0xd800 && unit <= 0xdbff) {
        const next = body.charCodeAt(index + 1)
        expect(next >= 0xdc00 && next <= 0xdfff).toBe(true)
        index += 1
      } else {
        expect(unit < 0xdc00 || unit > 0xdfff).toBe(true)
      }
    }
  })

  it('leaves markup characters alone, because they are text and the markers need them', () => {
    // Sanitizing here is not escaping, and it must not be: this layer's whole
    // claim is that the characters are harmless *because they are drawn as
    // text*. The placeholders the CLI itself writes contain angle brackets and
    // square brackets of their own, and a layer that escaped them would draw
    // `&lt;path&gt;` where a person should read `<path>`.
    expect(text('<script>alert(1)</script>', 'sentence')).toBe('<script>alert(1)</script>')
    expect(text('a < b && c > d', 'sentence')).toBe('a < b && c > d')
  })

  it('is idempotent, so the schema and the display layer can both apply it', () => {
    for (const hostile of HOSTILE) {
      const once = text(hostile, 'sentence')
      expect(text(once, 'sentence'), hostile).toBe(once)
    }
  })

  it('never echoes the refused text in its reason', () => {
    // The back door this test closes: a refusal whose message quoted the value
    // would put the value on screen as its own `title`, which is exactly what
    // the refusal existed to prevent.
    const hostile = 'javascript:alert(1)'
    const reason = refused(hostile, 'url', 'runs[0].url')
    expect(reason).not.toContain(hostile)
    expect(reason).not.toContain('javascript')
    expect(reason).toContain('runs[0].url')
  })
})

describe('the refusal door', () => {
  // `sanitizeRefusal` is the pass the CLI's own refusal sentence goes through
  // (review round 5 of PR 478): the credential rules and this layer's own, and
  // none of the pattern classes. Two screens draw that channel -- the
  // refused-read screen and the Projects panel -- and they used to draw it two
  // different ways.
  const REMEDY =
    "project sandbox-db's spend history refused the read: you need the Cloud Datastore Viewer role (roles/datastore.viewer) and the Service Usage Consumer role (roles/serviceusage.serviceUsageConsumer)"

  it('draws the CLI’s remedy character for character', () => {
    const drawn = sanitizeRefusal(REMEDY)
    expect(drawn.ok && drawn.text).toBe(REMEDY)
    expect(drawn.ok && drawn.flags).toEqual([])
  })

  it('draws the remedy the `sentence` class draws, because the rules that ate it are gone', () => {
    // Design section 6.3 requires the message verbatim, and until D3213 the
    // free-text rules would not have: the project became `<project>` and each
    // role name `[redacted]`, which is why this door had a rule set of its own.
    // The non-secret rules are gone, so the two agree on this sentence -- what
    // keeps the door separate now is the class's own business, not the rules.
    const through = sanitizeText(REMEDY, 'sentence', 'the CLI sentence')
    expect(through.ok && through.text).toBe(REMEDY)
  })

  it('still redacts a credential a child echoed into a refusal', () => {
    for (const token of [fake.githubToken(), fake.googleApiKey(), fake.slackToken()]) {
      const drawn = sanitizeRefusal(`permission denied for ${token}`)
      expect(drawn.ok && drawn.text, token).toBe(`permission denied for ${TOKEN_PLACEHOLDER}`)
    }
  })

  it('draws an address a child echoed into a refusal, as the sentence class does', () => {
    // A person is not a secret (D3213, 2026-11-21). The door redacted an address
    // until then, on the argument that a person and a service account are
    // identifiers the design redacts on every other channel -- and the operator
    // reading `permission denied for zora@acme.example` needs the address to
    // know whose permission was denied.
    for (const address of ['zora@acme.example', 'runner@proj.iam.gserviceaccount.com']) {
      const sentence = `permission denied for ${address}`
      const drawn = sanitizeText(sentence, 'sentence')
      expect(drawn.ok && drawn.text, address).toBe(sentence)
      const refusal = sanitizeRefusal(sentence)
      expect(refusal.ok && refusal.text, address).toBe(sentence)
      expect(refusal.ok && refusal.flags, address).not.toContain('address-marker')
    }
  })

  it('draws an ordinary path in a refusal as it is, and a credential inside one masked', () => {
    // The path rule that replaced every path is gone (D3213), and the entropy
    // net still declines a path run -- the guard the owner's report of
    // 2026-10-21 left behind, above `FREE_TEXT_RULES` -- so the operator's own
    // binary path is drawn. A token is a credential wherever it stands, path
    // segment or not, so the net still takes it: the guard is about the shape of
    // a path, never about what a path happens to contain.
    const binary = '/opt/homebrew/Cellar/fugaro/0.27.0/bin/fugaro'
    const drawn = sanitizeRefusal(`cannot run ${binary}: no such file`)
    expect(drawn.ok && drawn.text).toBe(`cannot run ${binary}: no such file`)
    const token = fake.highEntropyRun()
    const masked = sanitizeRefusal(`cannot read /home/x/${token}/key`)
    // The whole path run is what the net reads and replaces, not the one segment
    // inside it: a run of the net's alphabet is one candidate, slashes and all.
    expect(masked.ok && masked.text).toBe(`cannot read ${TOKEN_PLACEHOLDER}`)
  })

  it('strips the invisible characters and flattens the sentence, as every class does', () => {
    // The zero-width space goes (it is removed, not turned into a space) and the
    // line break is flattened: a sentence is drawn as one line.
    const drawn = sanitizeRefusal('no\u200bbackend\nfor this project')
    expect(drawn.ok && drawn.text).toBe('nobackend for this project')
    expect(drawn.ok && drawn.flags).toContain('stripped-invisible')
    // The private-use code points `runRules` holds pseudonyms with are taken out
    // of the value first, so a value cannot carry one into the hold.
    const sentinel = sanitizeRefusal('before \ue0000\ue001 after')
    expect(sentinel.ok && sentinel.text).toBe('before 0 after')
  })

  it('refuses what is not text, and what is longer than the console will read', () => {
    const long = 'a'.repeat(MAX_TEXT_INPUT_LENGTH + 1)
    const refused = sanitizeRefusal(long, 'the CLI sentence')
    expect(refused.ok).toBe(false)
    expect(refused.ok ? '' : refused.reason).toContain('the CLI sentence')
    expect(sanitizeRefusal(7).ok).toBe(false)
    // A sentence at the bound is drawn, clipped to the display bound.
    const at = sanitizeRefusal('a'.repeat(MAX_TEXT_INPUT_LENGTH))
    expect(at.ok).toBe(true)
    expect(at.ok && at.text.length).toBe(MAX_TEXT_LENGTH)
    expect(at.ok && at.flags).toContain('clipped')
  })
})

describe('the credential rules', () => {
  it('neutralises the token families the recorder knows', () => {
    // Every value here is assembled at run time by `test/support/fakeSecrets.ts`
    // -- see this file's header for why none of them can be written down.
    const tokens: readonly string[] = [
      fake.githubToken(),
      fake.githubPat(),
      fake.gitlabToken(),
      fake.npmToken(),
      fake.providerApiKey(),
      fake.slackToken(),
      fake.awsKeyId(),
      fake.googleApiKey(),
      fake.googleAccessToken(),
      fake.googleRefreshToken(),
      fake.jwt(),
    ]
    for (const token of tokens) {
      const line = text(`the job printed ${token} here`, 'sentence')
      expect(line, token).toContain(TOKEN_PLACEHOLDER)
      expect(line, token).not.toContain(token)
    }
  })

  it('neutralises the value of a key that names a credential, whatever it looks like', () => {
    // The recorder's rule: a *value* whose key names a credential goes, even
    // when it does not look like a token -- `hunter2` is a password.
    for (const line of [
      'Authorization: Bearer abcdefghijkl',
      'token=abcdef',
      'API_KEY: hunter2',
      'password = correct-horse',
      'client_secret: s3cr3t',
      'private_key_id: 7f3a',
    ]) {
      expect(text(line, 'sentence'), line).toContain(TOKEN_PLACEHOLDER)
    }
    // And the name survives, so the line still reads as a sentence about a
    // credential rather than as a sentence about nothing.
    expect(text('API_KEY: hunter2', 'sentence')).toContain('API_KEY')
    expect(text('API_KEY: hunter2', 'sentence')).not.toContain('hunter2')
  })

  it('neutralises a long opaque run, and keeps the recorder\u2019s pseudonyms', () => {
    // The entropy rule's two halves, and the failure mode of each. A 40-hex
    // digest with letters and digits is a candidate; a pseudonym pair is a long
    // run of the same alphabet and is NOT, because eating it would turn a
    // redaction the fixture already made into a different lie.
    expect(text(`key ${fake.hexDigest()}`, 'sentence')).toContain(TOKEN_PLACEHOLDER)
    expect(text(`key ${fake.highEntropyRun()}`, 'sentence')).toContain(TOKEN_PLACEHOLDER)
    const pseudonym = text('merge feat/x for r-4c25cf20/r-f1aa9214', 'sentence')
    expect(pseudonym).toContain('r-4c25cf20/r-f1aa9214')
    expect(pseudonym).not.toContain(TOKEN_PLACEHOLDER)
    expect(text('slug-6a7c7d06', 'sentence')).toBe('slug-6a7c7d06')
  })

  it('agrees with the recorder about what is an identifier and what is a secret', () => {
    // This layer re-applies the recorder's rules rather than trusting them to
    // have survived the subprocess boundary, so a rule that had drifted between
    // the two would be a rule the display applies and the recorder does not, or
    // the reverse -- and the entropy net is where the drift is easiest and
    // worst.
    //
    // The likeliest drift is exactly this one: a model name, a run id, a slug
    // and a workflow name are all long runs of the net's own alphabet, and they
    // mix at most two of the three character kinds the rule wants. A display
    // layer that dropped the recorder's `isHighEntropy` decision would draw
    // `[redacted-token]` over `claude-sonnet-4-5-20250929` -- a credential mark
    // on a value the recorder itself chose to keep, which is a false statement
    // about the document.
    //
    // The two modules cannot import each other (`src/source/redact.ts` reaches
    // for `node:crypto`, and `shared/` must import nothing at runtime), so the
    // agreement is held here, by running both over the same inputs.
    const recorder = createRedactor()
    const identifiers: readonly string[] = [
      'claude-sonnet-4-5-20250929',
      'claude-haiku-4-5-20251001',
      'claude-opus-4-1',
      'fugaro-cloud-run-batch',
      'console-screens-workflow',
      'redact-persons.test.ts',
      '20261015-120000-aa01',
      'run 20261015-120000-aa01 stopped',
      'v1.2.3-rc.4+build.20261015',
    ]
    const secrets: readonly string[] = [
      fake.highEntropyRun(),
      fake.hexDigest(),
      fake.githubToken(),
      fake.providerApiKey(),
    ]
    for (const sample of [...identifiers, ...secrets]) {
      const shown = text(sample, 'sentence')
      const redacted = recorder.redactText(sample)
      expect(
        shown.includes(sample),
        `${sample}: this layer ${shown.includes(sample) ? 'kept' : 'dropped'} it and the recorder ${redacted.includes(sample) ? 'kept' : 'dropped'} it`,
      ).toBe(redacted.includes(sample))
    }
    // Spelled out rather than left to the loop above, which a reader could take
    // for a tautology: the identifiers survive BOTH layers verbatim.
    for (const identifier of identifiers) {
      expect(text(identifier, 'sentence'), identifier).toBe(identifier)
      expect(recorder.redactText(identifier), identifier).toBe(identifier)
    }
    for (const secret of secrets) {
      expect(text(secret, 'sentence'), secret).toContain(TOKEN_PLACEHOLDER)
      expect(recorder.redactText(secret), secret).toContain(TOKEN_MARKER)
    }
    // The pseudonym grammars are the recorder's too, and they are why the net
    // does not eat a redaction: both layers keep one verbatim.
    for (const pseudonym of [
      'r-4c25cf20',
      'r-4c25cf20/r-f1aa9214',
      'slug-6a7c7d06',
      'person-2f9a4c1e',
    ]) {
      expect(text(pseudonym, 'sentence'), pseudonym).toBe(pseudonym)
    }
  })

  it('carries the recorder\u2019s credential families, one pattern each', () => {
    // The patterns in `sanitize.ts` are assembled from fragments at run time so
    // that this repository contains no credential-shaped string -- not even
    // inside a regular expression. Assembly can drift; the table's size and its
    // behaviour are what stop it.
    expect(TOKEN_SHAPES).toHaveLength(9)
    for (const pattern of TOKEN_SHAPES) {
      expect(pattern.flags).toContain('g')
      expect(pattern.source).not.toContain('undefined')
      // A pattern's own SOURCE is not a credential shape either: the fragments
      // are inert, and `fakeSecrets.ts` assembles the shape the same way. The
      // prefix is built here rather than written, for the same reason.
      expect(pattern.source).not.toMatch(new RegExp(['gh', 'p_'].join('')))
    }
  })

  it('replaces a URL in free text whole when it carries a credential, so its userinfo cannot survive', () => {
    const line = text('see https://user:pass@example.invalid/x?y=1 for the diff', 'sentence')
    expect(line).toBe(`see ${URL_MARKER} for the diff`)
    expect(line).not.toContain('user:pass')
    expect(text('see http://user:pass@example.invalid/x', 'sentence')).not.toContain('user:pass')
    // The rule is narrowed to the credential parts (D3213): a URL that carries
    // none is drawn, and its host, its query and its path come with it.
    expect(text('see https://example.invalid/x for the diff', 'sentence')).toBe(
      'see https://example.invalid/x for the diff',
    )
    expect(flags('see https://example.invalid/x', 'sentence')).not.toContain('replaced-url')
  })

  it('replaces a PEM block, and a bearer header, without losing the sentence', () => {
    const pem = text(`the key is ${fake.pemBlock()} and nothing else`, 'sentence')
    expect(pem).not.toContain(PRIVATE_KEY_LABEL)
    expect(pem).toContain(TOKEN_PLACEHOLDER)
    expect(pem.startsWith('the key is')).toBe(true)
    const bearer = text(`sent ${fake.bearerHeader()} to the API`, 'sentence')
    expect(bearer).toContain('Bearer')
    expect(bearer).toContain(TOKEN_PLACEHOLDER)
    expect(bearer).not.toContain(fake.googleAccessToken())
  })
})

describe('the free-text classes are the recorder\u2019s, class for class', () => {
  // The finding this block exists for: this layer claimed to re-apply the
  // recorder's rules while carrying a strict subset of them, so a token split by
  // a variation selector, a percent-encoded one, a fullwidth prefix, a URL after
  // a word character, an address, a path, a hostname and a URL containing a
  // pipe all reached the screen as written. The cases below are those inputs,
  // one by one, and each is an assertion about the *output* rather than about
  // the pattern that produced it.
  const VS16 = String.fromCharCode(0xfe0f)
  const FULLWIDTH_SK = String.fromCharCode(0xff53, 0xff4b) + '-'

  it('neutralises a token the recorder neutralises, however it was spelled', () => {
    const token = fake.githubToken()
    const cases: readonly (readonly [string, string])[] = [
      // Split by a variation selector: invisible, and not in the hand-written
      // set this layer used to carry.
      ['split by a variation selector', `${token.slice(0, 3)}${VS16}${token.slice(3)}`],
      // Percent-encoded: `%70` is a `p`, and the run is decoded before any rule
      // looks at it.
      ['percent-encoded', ['gh', '%70_', 'a'.repeat(36)].join('')],
      // Base64-wrapped: the decoded text is looked in, not the run.
      ['base64-wrapped', btoa(fake.googleAccessToken())],
      // Fullwidth: NFKC turns it into the prefix the table carries.
      ['written fullwidth', `${FULLWIDTH_SK}${'a'.repeat(24)}`],
    ]
    for (const [what, secret] of cases) {
      const line = text(`the job printed ${secret} here`, 'sentence')
      expect(line, what).toBe(`the job printed ${TOKEN_PLACEHOLDER} here`)
      expect(line, what).not.toContain(secret)
    }
  })

  it('draws an address, a path, a hostname and a repository path as they came', () => {
    // The finding this case list used to be about is the other way round now
    // (D3213, 2026-11-21): none of these is a secret, and the operator's own
    // tool draws what it was given -- whose address wrote the line, which path
    // the run touched, which host answered, which branch it was. The recorder
    // replaces every one of them; this layer may redact *less* than the recorder
    // and never more, which is the direction the agreement test holds.
    const cases: readonly (readonly [string, string])[] = [
      ['an address', 'mail ana@acme.invalid now'],
      ['a path', 'wrote /Users/ana/secret/plan.md today'],
      ['a windows path', 'wrote C:\\Users\\ana\\plan.md today'],
      ['a hostname', 'dial acme-devel-zora-default-rtdb.firebaseio.com now'],
      ['a private hostname', 'on zoras-laptop-pro.local now'],
      ['an address literal', 'dial 10.42.7.13:443 now'],
      ['a repository path', 'in dimipaun/fugaro#412 the fix landed'],
      ['a gcp project', 'on project acme-devel-zora today'],
      ['a runs bucket', 'wrote to fugaro-runs-acme-x9 today'],
      ['a branch', 'merge fix/console-text-path now'],
      ['a bare branch', 'merge feat/x for r-4c25cf20/r-f1aa9214'],
      ['a ref', 'pushed refs/heads/secret-codename now'],
    ]
    for (const [what, line] of cases) {
      expect(text(line, 'sentence'), what).toBe(line)
    }
  })

  it('still masks a credential spelled as one of the shapes it no longer replaces', () => {
    // One case per rule the change removes, and the answer for each is that the
    // shape is drawn while the credential inside it is not: what the layer gave
    // up is the *shape*, never the secret. Both values are built at run time by
    // `test/support/fakeSecrets.ts` -- no line of this file carries one.
    const token = fake.githubToken()
    const run = fake.highEntropyRun()
    const cases: readonly (readonly [string, string])[] = [
      ['an address', `mail ${token}@acme.invalid now`],
      ['a path', `wrote /home/ana/${token}/plan.md today`],
      ['a windows path', `wrote C:\\Users\\ana\\${run}\\plan.md today`],
      ['a hostname', `dial ${token}.example.invalid now`],
      ['an address literal', `dial 10.42.7.13:${token} now`],
      ['a repository path', `in dimipaun/${token} the fix landed`],
      ['a gcp project', `on project ${token} today`],
      ['a runs bucket', `wrote to ${run} today`],
      ['a branch', `merge fix/${run} now`],
      ['a ref', `pushed refs/heads/${token} now`],
    ]
    for (const [what, line] of cases) {
      const drawn = text(line, 'sentence')
      expect(drawn, what).toContain(TOKEN_PLACEHOLDER)
      expect(drawn, what).not.toContain(token)
      expect(drawn, what).not.toContain(run)
    }
  })

  it('replaces a URL that carries a credential, and draws one that does not', () => {
    // The three shapes a URL rule misses when it is not the recorder's: a scheme
    // after a word character, a URL whose path holds a `|`, and a scheme with no
    // dot in it. None of them carries a credential, so all three are drawn now
    // (D3213) -- and the fourth, whose userinfo is a password, is still replaced
    // whole, which is what the rule is kept for.
    expect(text('see _https://evil.invalid/x now', 'sentence')).toBe('see _https://evil.invalid/x now')
    expect(text('see https://evil.invalid/a|b end', 'sentence')).toBe('see https://evil.invalid/a|b end')
    expect(text('see git+ssh://evil.invalid/x now', 'sentence')).toBe('see git+ssh://evil.invalid/x now')
    expect(text('see http://user:pass@evil.invalid/x now', 'sentence')).toBe(`see ${URL_PLACEHOLDER} now`)
  })

  it('replaces a URL in free text when any of its parts carries a credential', () => {
    // The narrowed rule asks `applyUrl`'s own questions of the match (D3213):
    // userinfo, a host that reads as a key, a query or a fragment that carries
    // one, a well-known webhook's path, and a path segment that is a key. Each
    // secret is built at run time (`test/support/fakeSecrets.ts`).
    const token = fake.githubToken()
    const run = fake.highEntropyRun()
    const cases: readonly (readonly [string, string])[] = [
      ['userinfo holding a password', `https://user:${token}@example.invalid/x`],
      ['a token-named query key', `https://example.invalid/x?token=${token}`],
      ['a credential-named query key with a shapeless value', 'https://example.invalid/x?api_key=hunter2'],
      ['an access token in the fragment', `https://example.invalid/x#access_token=${token}`],
      ['a signed-url query key', `https://storage.googleapis.com/b/x?X-Goog-Signature=${run}`],
      ['a high-entropy path segment', `https://hooks.example/services/${token}`],
      ['a webhook path', `https://hooks.slack.com/services/${token}`],
      ['a token as a subdomain', `https://${token}.example.invalid/x`],
    ]
    for (const [what, url] of cases) {
      const drawn = text(`see ${url} now`, 'sentence')
      expect(drawn, what).toBe(`see ${URL_PLACEHOLDER} now`)
      expect(drawn, what).not.toContain(token)
    }
    // The same URL carrying nothing is drawn whole, host, query and path with
    // it: that is the difference this rule's narrowing makes, and the reason a
    // `?tab=files` link on a run screen reads as the link it is.
    expect(text('see https://example.invalid/x?page=2 now', 'sentence')).toBe(
      'see https://example.invalid/x?page=2 now',
    )
  })

  it('names the credential rule it applied, and nothing else', () => {
    // Every flag left in the free-text rule set is a credential flag (D3213):
    // the class flags a reader used to be able to look for -- `address-marker`,
    // `path-placeholder`, `host-placeholder`, `name-placeholder` -- went with
    // the rules that produced them, and a screen that drew one as "something was
    // hidden here" would be saying a credential was found where none was.
    expect(flags('mail ana@acme.invalid now', 'sentence')).toEqual([])
    expect(flags('wrote /Users/ana/plan.md today', 'sentence')).toEqual([])
    expect(flags('on zoras-laptop-pro.local now', 'sentence')).toEqual([])
    expect(flags('on project acme-devel-zora today', 'sentence')).toEqual([])
    expect(flags(`the job printed ${fake.githubToken()} here`, 'sentence')).toContain(
      'neutralised-credential',
    )
    expect(flags('see https://user:pass@evil.invalid/x now', 'sentence')).toContain('replaced-url')
  })

  it('keeps the recorder\u2019s identifiers, and the prose that only looks like one', () => {
    // Every one of these is a false positive the pattern rules are built to
    // avoid, and a layer that replaced one would be stating that a credential
    // was found where the recorder found none.
    for (const line of [
      'the model claude-sonnet-4-5-20250929 answered',
      'run 20261015-120000-aa01 stopped',
      'the console-screens-workflow says so',
      'the file redact-persons.test.ts is here',
      'release v1.2.3-rc.4+build.20261015 is out',
      'slug-6a7c7d06',
      'the pairs and/or, he/she and 24/7',
    ]) {
      expect(text(line, 'sentence'), line).toBe(line)
    }
  })

  it('never keeps a credential the recorder took, over a corpus of prose', () => {
    // The property, rather than a list of cases: if this layer drew a sample
    // verbatim, the recorder must not have found a credential in it. It is
    // one-directional on purpose (D3213, 2026-11-21): the recorder redacts
    // identity as well -- an address, a path, a host, a repository path, a
    // project, a bucket -- and this layer draws all of those, so what the two
    // still answer the same way is the credential. The marker counted here is
    // the credential one: the recorder's `[redacted-url]` is *not* a credential
    // signal, because it replaces every URL where this layer replaces only one
    // that carries a credential (the URL cases above are that rule's own test).
    const recorder = createRedactor()
    for (const sample of PROSE) {
      const shown = text(sample, 'sentence')
      if (shown !== sample) continue
      const redacted = recorder.redactText(sample)
      expect(
        redacted.includes(TOKEN_MARKER),
        `${sample}: this layer drew it as written and the recorder took a credential out of it`,
      ).toBe(false)
    }
    // And the other half of the same property, on the shapes that carry one: a
    // secret of each family this repository can build at run time, in a sentence
    // and in the recorder's hands, comes out of neither.
    for (const [what, secret] of fake.realisticSecrets()) {
      const sample = `the job wrote ${secret} to its log`
      const shown = text(sample, 'sentence')
      expect(shown, what).not.toContain(secret)
      expect(shown, what).toContain(TOKEN_PLACEHOLDER)
      expect(recorder.redactText(sample), what).not.toContain(secret)
    }
  })

  // The three layers' sets of invisible characters are one definition now
  // (`INVISIBLE_CLASS`); `invisible.test.ts` walks the whole Unicode range over
  // the recorder, this layer and the IPC check.

  it('leaves the validating classes their own values', () => {
    // The split the recorder makes too: a value whose key says what it is runs
    // the credential and URL rules and is then validated by its class, so a
    // repository path stays a repository path -- but a token spelled as one of
    // those values does not survive.
    expect(text('fixture-owner/fixture-repo', 'repository')).toBe('fixture-owner/fixture-repo')
    expect(text('feat/console-fixtures', 'branch')).toBe('feat/console-fixtures')
    const spelledAsABranch = ['feat/', 'ya', '29.', 'b'.repeat(40)].join('')
    expect(refused(spelledAsABranch, 'branch')).toContain('not a valid branch')
  })
})

/** PROSE is the corpus the credential property walks: the sentences a job
 * writes, plus one of each shape the pattern classes used to exist for. It is
 * not a list of expectations (those are above); it is the input set on which the
 * two layers' *credential* answers have to agree -- the identity shapes in it
 * are the ones this layer now draws and the recorder still replaces. */
const PROSE: readonly string[] = Object.freeze([
  'the run finished with no warnings',
  'a title with <script>alert(1)</script> in it',
  'the pairs and/or, he/she and 24/7',
  'the model claude-sonnet-4-5-20250929 answered',
  'run 20261015-120000-aa01 stopped',
  'see https://example.invalid/x for the diff',
  'see _https://evil.invalid/x now',
  'see https://evil.invalid/a|b end',
  'mail ana@acme.invalid now',
  'wrote /Users/ana/secret/plan.md today',
  'wrote C:\\Users\\ana\\plan.md today',
  'dial acme-devel-zora-default-rtdb.firebaseio.com now',
  'on zoras-laptop-pro.local now',
  'dial 10.42.7.13:443 now',
  'on project acme-devel-zora today',
  'wrote to fugaro-runs-acme-x9 today',
  'in dimipaun/fugaro#412 the fix landed',
  'merge fix/console-text-path now',
  'merge feat/x for r-4c25cf20/r-f1aa9214',
  'pushed refs/heads/secret-codename now',
  'slug-6a7c7d06',
  'Authorization: Bearer abcdefghijkl',
  'API_KEY: hunter2',
])

describe('the class rules', () => {
  it('names the classes the contract has fields for', () => {
    expect([...TEXT_CLASSES].sort()).toEqual(
      [
        'branch',
        'host',
        'imageRef',
        'label',
        'path',
        'person',
        'project',
        'repoPattern',
        'repository',
        'runId',
        'scope',
        'sentence',
        'slug',
        'url',
      ].sort(),
    )
  })

  it('keeps a repository, and refuses anything that is not one', () => {
    expect(text('fixture-owner/fixture-repo', 'repository')).toBe('fixture-owner/fixture-repo')
    expect(text('r-4c25cf20/r-f1aa9214', 'repository')).toBe('r-4c25cf20/r-f1aa9214')
    for (const bad of ['just-a-name', 'a/b/c', '/etc/passwd', 'a/../../etc', '', 'a/']) {
      expect(refused(bad, 'repository', 'repos[0].repo'), bad).toContain('repos[0].repo')
    }
    expect(refused('x'.repeat(MAX_REPOSITORY_LENGTH + 1) + '/y', 'repository')).toContain(
      'not a valid repository',
    )
  })

  it('keeps a policy allow-list pattern, and refuses anything that is not one', () => {
    // The four shapes a published policy's `allow_repos` carries
    // (`projectpolicy.MatchRepo`): the canonical repository the CLI writes,
    // the literal `*`, and the two wildcard forms the matcher documents. The
    // `repository` class refuses every one of them -- it is `owner/name`, one
    // slash -- which is why this class exists (CM7's review of 2026-11-07).
    expect(text('fixture-provider/fixture-owner/fixture-repo', 'repoPattern')).toBe(
      'fixture-provider/fixture-owner/fixture-repo',
    )
    expect(text('*', 'repoPattern')).toBe('*')
    expect(text('github/acme/*', 'repoPattern')).toBe('github/acme/*')
    expect(text('*/acme/*', 'repoPattern')).toBe('*/acme/*')
    // Three segments is the floor, not the count: a repository path nests, and
    // `ValidPattern` says so in the comment over its own segment loop ("a cap
    // of three refused an entry that does admit one"). The class read this
    // value as `n/a` until CM7's third review round.
    expect(text('bitbucket/acme/group/app', 'repoPattern')).toBe('bitbucket/acme/group/app')
    // A `..` is refused as a *segment*, which is how the CLI refuses it, and
    // not as a substring: `ValidPattern` accepts `github/a..b/app`.
    expect(text('github/a..b/app', 'repoPattern')).toBe('github/a..b/app')
    for (const bad of [
      // `repository`'s own shape is not one: a policy entry is
      // provider-qualified, and a bare `owner/name` in one is a value
      // `Canonical` never wrote.
      'fixture-owner/fixture-repo',
      'a/../../etc',
      'github/acme/',
      'a b/c/d',
      // Upper case is what `ValidPattern` refuses ("names are lower-case"), and
      // the canonical form `Canonical` writes is lower-cased.
      'GitHub/acme/app',
      // `task.CanonicalRepo` trims a trailing `.git`, so a pattern that ends in
      // one admits nothing.
      'github/acme/app.git',
      '',
    ]) {
      expect(refused(bad, 'repoPattern', 'policy.allow_repos[0]'), bad).toContain(
        'policy.allow_repos[0]',
      )
    }
    // The bound is the CLI's own `projectpolicy.MaxPatternLen`, not
    // `repository`'s: an entry of exactly that length is one `Decode` admits,
    // and the class refused it while the bound was 200.
    const longest = 'github/' + 'a'.repeat(MAX_REPO_PATTERN_LENGTH - 'github/'.length - 4) + '/app'
    expect(longest.length).toBe(MAX_REPO_PATTERN_LENGTH)
    expect(text(longest, 'repoPattern')).toBe(longest)
    expect(refused('x'.repeat(MAX_REPO_PATTERN_LENGTH + 1), 'repoPattern')).toContain(
      'not a valid repository pattern',
    )
  })

  it('keeps a stored grant scope, and refuses anything that is not one', () => {
    // The shapes `ParseScope` answers with (`internal/access`, and the canonical
    // spelling `Scope.String` writes): the two whole scopes, one pattern, a
    // nested path, and a comma-separated list.
    for (const good of [
      'project',
      'all-repos',
      'github/acme/*',
      'github/acme/app',
      'bitbucket/acme/group/app',
      'github/acme/app,github/acme/*',
    ]) {
      expect(text(good, 'scope'), good).toBe(good)
    }
    for (const bad of [
      // A bare `owner/name` is not one: a scope entry is provider-qualified
      // (`ValidPattern`), and `ParseScope` reads a repository of that shape as
      // an owner and a repository with no provider to match.
      'acme/app',
      // The lone star is `all-repos`' meaning, and `all-repos` is a project
      // admin's to grant: `ParseScope` refuses `*` so a grant cannot mint the
      // scope its holder does not hold.
      '*',
      // A wildcard provider is the allow-list's spelling and not a grant's:
      // `MatchRepo` admits `*/acme/*` for `allow_repos`, and a grant names the
      // repositories it covers.
      '*/acme/*',
      // A doubled star crosses a `/` to a reader and never does to the matcher.
      'github/**/app',
      // A whole scope is spelled alone: `ParseScope` refuses a reserved word
      // mixed with a repository list.
      'project,github/acme/app',
      // Upper case and a `.git` ending are what `ParseScope` folds and trims
      // before it stores an entry, so a *stored* scope carries neither.
      'GitHub/acme/app',
      'github/acme/app.git',
      // An empty entry, at either end of a list.
      'github/acme/app,',
      // A `.` or `..` segment covers nothing (`CanonicalRepo` refuses such a
      // repository), as it does for `repoPattern`.
      'github/../../etc',
      '',
    ]) {
      expect(refused(bad, 'scope', 'grants[0].scope'), bad).toContain('grants[0].scope')
    }
    // The bound is the CLI's own per-entry one: `ParseScope` canonicalises an
    // entry through `ValidPattern`, whose `MaxPatternLen` is the longest entry
    // the grants node's reader accepts.
    const longest = 'github/' + 'a'.repeat(MAX_REPO_PATTERN_LENGTH - 'github/'.length - 4) + '/app'
    expect(longest.length).toBe(MAX_REPO_PATTERN_LENGTH)
    expect(text(longest, 'scope')).toBe(longest)
    expect(refused(`github/acme/${'a'.repeat(MAX_REPO_PATTERN_LENGTH)}`, 'scope')).toContain(
      'not a valid scope',
    )
  })

  it('keeps a container image reference, and refuses anything that is not one', () => {
    // The shapes `catalogImageRE` accepts (`ValidateCatalog`): a bare name and
    // tag, a registry host, a host with a port, a path of several components,
    // and a digest.
    for (const good of [
      'node',
      'postgres:16',
      'ghcr.io/acme/agent:1.2',
      'registry.example.invalid:5000/acme/agent@sha256:' + 'a'.repeat(64),
    ]) {
      expect(text(good, 'imageRef'), good).toBe(good)
    }
    for (const bad of [
      'ghcr.io/acme/my image:1',
      ['ghcr.io/acme/agent:1', 'a'].join('\n'),
      'GHCR.IO/acme/agent',
      'ghcr.io/acme/agent:1.2@sha256:' + 'a'.repeat(63),
      '',
    ]) {
      expect(refused(bad, 'imageRef', 'policy.image_catalog.entries[0].source.image'), bad).toContain(
        'policy.image_catalog.entries[0].source.image',
      )
    }
    expect(refused('x'.repeat(MAX_IMAGE_REF_LENGTH + 1), 'imageRef')).toContain(
      'not a valid container image reference',
    )
  })

  it('keeps a slug, and refuses anything that is not one', () => {
    expect(text('slug-6a7c7d06', 'slug')).toBe('slug-6a7c7d06')
    expect(text('github-com-fixture-owner-fixture-repo', 'slug')).toBe(
      'github-com-fixture-owner-fixture-repo',
    )
    expect(text('<bucket>', 'slug')).toBe('<bucket>')
    for (const bad of ['Slug-6A7C7D06', 'a slug', 'a/slug', '-leading', '']) {
      expect(refused(bad, 'slug'), bad).toContain('not a valid slug')
    }
  })

  it('keeps a branch, and refuses anything that is not one', () => {
    expect(text('feat/console-fixtures', 'branch')).toBe('feat/console-fixtures')
    expect(text('<branch>', 'branch')).toBe('<branch>')
    expect(text('main', 'branch')).toBe('main')
    for (const bad of ['-leading-dash', 'a..b', 'a@{b}', 'a//b', 'a/', '/a']) {
      expect(refused(bad, 'branch'), bad).toContain('not a valid branch')
    }
  })

  it('redacts a branch-shaped credential to the recorder’s own marker', () => {
    // The branch grammar is `[A-Za-z0-9._/-]{1,255}`: upper case, lower case,
    // digits, dots, dashes and slashes -- the credential alphabet whole, and the
    // one validating class where a run no pattern can find was drawn as a name
    // (review of Task 4.1: a 20-character mixed-case run, and a 40-character hex
    // digest git would take as a ref). The token families are answered for by
    // the credential pass every validating class runs, which is why the first
    // loop is the token placeholder; the entropy rule's run is answered for in
    // the class, and the answer is `<branch>` -- the marker the *recorder*
    // writes for every value under a branch key, so this is agreement and not a
    // redaction the document does not contain. A refusal would be the other
    // choice, and the reason it is not taken is the last loop: the entropy rule
    // is a heuristic, and `feat/ABC-123-add-thing` is a real branch name it
    // calls a credential. Each value is assembled at run time
    // (`fakeSecrets.ts`), so no line of this repository holds the shape.
    const families: readonly string[] = [
      fake.awsKeyId(),
      fake.googleApiKey(),
      fake.gitlabToken(),
      fake.slackToken(),
      fake.googleAccessToken(),
    ]
    for (const value of families) expect(text(value, 'branch'), value).toBe(TOKEN_PLACEHOLDER)
    for (const value of [fake.highEntropyRun(), fake.hexDigest()]) {
      // This fails if the entropy check is deleted: both are legal branch names,
      // and both were drawn whole.
      expect(text(value, 'branch'), value).toBe(BRANCH_PLACEHOLDER)
      expect(flags(value, 'branch'), value).toContain('branch-placeholder')
    }
    // ...and a branch that is not high-entropy is drawn as the name it is, so
    // the class is not "redact everything long": these are the shapes the
    // console's own repositories use, including the mixed-case one that a
    // branch field is easiest to get wrong about.
    for (const good of ['feat/console-fixtures', 'fugaro/20261017-204831-b96d', 'feat/console-A1B2C3', 'main']) {
      expect(text(good, 'branch'), good).toBe(good)
    }
    // The heuristic's false positives are redacted rather than refused, which is
    // the difference between a cell the operator cannot read and a screen they
    // cannot use at all. Both of these are names a person would write and git
    // would accept; the rule calls them credentials because they mix case,
    // digits and length.
    for (const named of ['feat/ABC-123-add-thing', 'Fugaro/Entitlements-4.1']) {
      expect(text(named, 'branch'), named).toBe(BRANCH_PLACEHOLDER)
    }
  })

  it('keeps a project name, and refuses anything that is not one', () => {
    expect(text('fixture-sandbox', 'project')).toBe('fixture-sandbox')
    expect(text('<project>', 'project')).toBe('<project>')
    for (const bad of ['Fixture-Sandbox', 'a project', '1nope', 'a/b']) {
      expect(refused(bad, 'project'), bad).toContain('not a valid project')
    }
  })

  it('draws a person, an address included, and refuses only a credential', () => {
    // A person is who started the run (D3213, 2026-11-21): the operator's own
    // tool names them, so the login, the pseudonym the recorder minted and the
    // address all reach the screen as they came. What this class still refuses
    // is a value that is a secret and not a person, and it refuses it rather
    // than cutting it down -- half of a login is not a person (the case below).
    expect(text('fixture-operator', 'person')).toBe('fixture-operator')
    expect(text('person-2f9a4c1e', 'person')).toBe('person-2f9a4c1e')
    expect(text('fixture-operator@example.invalid', 'person')).toBe('fixture-operator@example.invalid')
    expect(flags('someone@example.invalid', 'person')).not.toContain('address-marker')
    expect(text(ADDRESS_MARKER, 'person')).toBe(ADDRESS_MARKER)
    // A marker the recorder wrote is kept: it is a value a reader recognises,
    // and refusing it would draw `n/a` over a document that is not broken.
    expect(text('Ana Kell', 'person')).toBe('Ana Kell')
    expect(text('a b@example.invalid', 'person')).toBe('a b@example.invalid')
    // The empty value is the schema's business, not this class's: whether a
    // field is *there* is `schema.ts`'s question, and a class that refused an
    // empty one would refuse the whole screen for a run whose `kill.by` the job
    // had not written yet.
    expect(text('', 'person')).toBe('')
  })

  it('refuses a credential that fits the login grammar, rather than drawing it', () => {
    // The login grammar is `[A-Za-z0-9-]{1,39}` and every one of these fits
    // inside it, which made the `person` class the single door a token could
    // come out whole: `TOKEN_SHAPES` redacts each of them in a title, a reason
    // and a stage, and the recorder pseudonymises the same value, so a live
    // document's `kill.by` or `requested_by` was the one place they survived --
    // and design section 5.1 rule 7 makes this layer the last line of defence
    // for live data. Each value is assembled at run time (`fakeSecrets.ts`), so
    // no line of this repository holds the shape.
    const shaped: readonly string[] = [
      fake.awsKeyId(),
      fake.googleApiKey(),
      fake.gitlabToken(),
      fake.slackToken(),
      fake.highEntropyRun(),
    ]
    for (const value of shaped) {
      // `refused` throws when the value is accepted at all, so this fails if the
      // credential check is deleted: each of these fits the login grammar
      // (`[A-Za-z0-9-]{1,39}`) and would otherwise be drawn whole.
      expect(refused(value, 'person')).toContain('shaped like a credential')
    }
    // ...and an ordinary login is still a login, so the class is not "refuse
    // everything long": the three people the design's own screens draw.
    for (const good of ['fixture-operator', 'zora-kell', 'person-2f9a4c1e', 'Ana']) {
      expect(text(good, 'person'), good).toBe(good)
    }
  })

  it('keeps a run id, and refuses anything that is not one', () => {
    expect(text('20261015-120000-aa01', 'runId')).toBe('20261015-120000-aa01')
    // A run whose id the recorder redacted is a marker, not a run id -- and the
    // class must accept it, because refusing it would draw `n/a` over the truth.
    expect(text(LITERAL_PLACEHOLDER, 'runId')).toBe(LITERAL_PLACEHOLDER)
    for (const bad of ['120000-20261015-aa01', '2026-10-15', '', 'a'.repeat(64)]) {
      expect(refused(bad, 'runId'), bad).toContain('not a valid run id')
    }
  })

  it('keeps a label on one line, and refuses one that is not', () => {
    // A label is the CLI's own short rendered field -- a stage, a round, a
    // verify state, a model pair -- and a label with a line break in it is not a
    // long label, it is two table rows wearing one cell. This is the class that
    // REFUSES rather than flattening, which is the difference between it and
    // `sentence`.
    expect(text('implement', 'label')).toBe('implement')
    expect(text('-', 'label')).toBe('-')
    expect(text('claude-sonnet-4-5-20250929 / claude-opus-4-1', 'label')).toBe(
      'claude-sonnet-4-5-20250929 / claude-opus-4-1',
    )
    expect(refused('two\nlines', 'label')).toContain('not a single line')
    expect(refused('x'.repeat(MAX_LABEL_LENGTH + 1), 'label')).toContain('longer than a label')
  })

  it('draws a host, dotted name, address literal and bare name alike', () => {
    // The recorder's host class returns its marker for every value it is given,
    // valid or not, and this layer used to follow it (review round 2 of PR 478).
    // A machine name is not a secret (D3213, 2026-11-21): which host answered is
    // what an operator reads a refusal for, so the value is drawn as it came.
    expect(text('<host>', 'host')).toBe('<host>')
    expect(text('fixture.invalid', 'host')).toBe('fixture.invalid')
    expect(text('build-07.corp.fixture.invalid', 'host')).toBe('build-07.corp.fixture.invalid')
    expect(flags('fixture.invalid', 'host')).not.toContain('host-placeholder')
    expect(text('192.0.2.10', 'host')).toBe('192.0.2.10')
    expect(text('10.0.0.1', 'host')).toBe('10.0.0.1')
    expect(text('localhost', 'host')).toBe('localhost')
    expect(text('zoras-laptop-pro.local', 'host')).toBe('zoras-laptop-pro.local')
    // A hostname that *is* a credential is still masked, by the rule that reads
    // the whole value rather than by this class: a key in a label is a key.
    expect(text(`${fake.githubToken()}.example.invalid`, 'host')).toBe(
      `${TOKEN_PLACEHOLDER}.example.invalid`,
    )
  })

  it('draws a path as it came, and still masks a credential inside one', () => {
    // A path is where the operator's user name lives, which is why it was drawn
    // as the placeholder until D3213 -- and a user name is not a secret. The
    // path of the file a finding names is most of what a finding says.
    for (const value of [
      '/Users/ana/src/fugaro',
      '/home/fugaro/.config/fugaro/fugaro.yaml',
      '~/checkout',
      'C:\\Users\\ana\\fugaro',
      '\\\\server\\share',
    ]) {
      expect(text(value, 'path'), value).toBe(value)
    }
    expect(flags('/etc/passwd', 'path')).not.toContain('path-placeholder')
    expect(text('', 'path')).toBe('')
    // The entropy net still declines a path run -- the guard the owner's report
    // of 2026-10-21 left behind -- so a long ordinary path is drawn, and a
    // credential inside one is not: this layer gave up the shape, never the
    // secret.
    const long = '/opt/homebrew/Cellar/fugaro/0.27.0/bin/fugaro'
    expect(text(long, 'path')).toBe(long)
    expect(text(`/home/ana/${fake.highEntropyRun()}/plan`, 'path')).toBe(TOKEN_PLACEHOLDER)
  })

  it('keeps an https URL, drops its userinfo, and refuses every other scheme', () => {
    expect(text('https://github.com/owner/name/pull/412', 'url')).toBe(
      'https://github.com/owner/name/pull/412',
    )
    const dropped = text('https://user:pass@github.com/owner/name/pull/412', 'url')
    expect(dropped).not.toContain('user:pass')
    expect(dropped.startsWith('https://github.com/owner/name/pull/412')).toBe(true)
    expect(flags('https://user:pass@github.com/x', 'url')).toContain('userinfo-removed')
    // A credential hidden in the encoding is userinfo too, and rebuilding the
    // URL from its parsed parts is what drops it.
    expect(text('https://%75ser:p%61ss@github.com/x', 'url')).not.toContain('%75ser')
    expect(text(URL_MARKER, 'url')).toBe(URL_MARKER)
    expect(text(PATH_PLACEHOLDER, 'url')).toBe(PATH_PLACEHOLDER)
    for (const bad of [
      'javascript:alert(1)',
      'data:text/html,<script>alert(1)</script>',
      'file:///etc/passwd',
      'http://github.com/x',
      'https://',
      'https://a/x\nhttps://evil.invalid',
      '',
    ]) {
      expect(refused(bad, 'url', 'finished[0].pr_url'), bad).toContain('the console will not')
    }
    expect(refused('javascript:alert(1)', 'url')).toContain('not https')
  })

  it('refuses a URL whose query, fragment or path carries a credential', () => {
    // Review round 3 of PR 478: the class kept an https URL exactly as it came,
    // so a token in its query or fragment went into the cell's text, its title
    // and its `data-href`, while the recorder replaces every url field whole.
    // Every value is assembled at run time (see this file's header).
    const base = 'https://github.com/o/r/pull/1'
    const token = fake.githubToken()
    const encoded = ['gh', '%70_', 'a'.repeat(36)].join('')
    for (const [what, url] of [
      ['a token-named query key', `${base}?access_token=${token}`],
      ['a credential-named key with a shapeless value', `${base}?api_key=hunter2`],
      ['a token in the fragment', `${base}#${token}`],
      ['an assignment in the fragment', `${base}#access_token=abcdef`],
      ['a percent-encoded token in the query', `${base}?x=${encoded}`],
      ['a high-entropy run in the fragment', `${base}#${fake.highEntropyRun()}`],
      ['a high-entropy run in the query', `${base}?state=${fake.highEntropyRun()}`],
      ['a token as a path segment', `https://github.com/o/r/pull/${token}`],
      ['a percent-encoded token as a path segment', `https://github.com/o/r/${encoded}`],
    ] as const) {
      const reason = refused(url, 'url', 'finished[0].pr_url')
      expect(reason, what).toContain('finished[0].pr_url')
      expect(reason, what).not.toContain(token)
      expect(safeUrl(url), what).toBeNull()
      const target = element()
      putLink(target, url, 'finished[0].pr_url')
      expect(target.getAttribute(DESTINATION_ATTRIBUTE), what).toBeNull()
      expect(target.textContent ?? '', what).not.toContain(token)
      expect(target.getAttribute('title') ?? '', what).not.toContain(token)
    }
    // The links a forge writes are kept as they came: a review anchor, a tab, a
    // commit (a hex digest in a path is a commit, not a key).
    for (const url of [
      `${base}#issuecomment-1234567`,
      `${base}/files?tab=files`,
      `https://github.com/o/r/commit/${fake.hexDigest()}`,
    ]) {
      expect(text(url, 'url'), url).toBe(url)
    }
  })

  /** refusedLink asserts the whole refusal: the class, `safeUrl`, and the cell
   * `putLink` draws, which must carry neither the link nor the secret. */
  function refusedLink(url: string, secret: string, what: string): void {
    const reason = refused(url, 'url', 'finished[0].pr_url')
    expect(reason, what).toContain('finished[0].pr_url')
    expect(reason, what).not.toContain(secret)
    expect(safeUrl(url), what).toBeNull()
    const target = element()
    putLink(target, url, 'finished[0].pr_url')
    expect(target.getAttribute(DESTINATION_ATTRIBUTE), what).toBeNull()
    expect(target.textContent ?? '', what).not.toContain(secret)
    expect(target.getAttribute('title') ?? '', what).not.toContain(secret)
  }

  it('refuses a URL whose host carries a credential or a random-looking label', () => {
    // Review round 4 of PR 478: the host was never read, so a token written as
    // a subdomain was accepted and drawn as the link's text. Every value is
    // assembled at run time (see this file's header).
    const token = fake.inert('gh', 'p_', 'abcdefgh1234')
    const mixed = fake.highEntropyRun()
    const lower = mixed.toLowerCase()
    // A label whose case mix is all of its entropy: high as written, low once
    // the parser folds it, so only the host as written shows it.
    const caseOnly = ['abcdef', '1', 'abcdef', '2']
      .map((part) => [...part].map((letter) => letter + letter.toUpperCase()).join(''))
      .join('')
    // A Slack-style token is only a token at the start of its run, so as an
    // `xn--` label it is invisible until the label is punycode-decoded.
    const slack = fake.slackToken()
    const encoded = new URL(`https://${slack}é.example.com/`).hostname
    expect(encoded.startsWith('xn--')).toBe(true)
    for (const [what, url, secret] of [
      ['a forge token as a subdomain', `https://${token}.evil.example.com/x`, token],
      ['a mixed-case high-entropy label', `https://${mixed}.example.com/x`, mixed],
      ['a lower-case random-looking label', `https://${lower}.example.com/x`, lower],
      ['a label whose entropy is its case', `https://${caseOnly}.example.com/x`, caseOnly],
      ['the same, behind backslashes the parser reads as slashes', `https:\\\\${caseOnly}.example.com/x`, caseOnly],
      // The family patterns are case-sensitive and a host is not: written in
      // capitals the token is only a token once the parser has folded it.
      ['a token written in capitals', `https://${token.toUpperCase()}.evil.example.com/x`, 'ABCDEFGH1234'],
      ['a high-entropy label behind userinfo and a port', `https://u@${mixed}.example.com:8443/x`, mixed],
      ['a token behind a percent-encoded host', `https://${token.replace('p', '%70')}.example.com/x`, 'abcdefgh1234'],
      ['a token split across a dot', `https://${fake.googleAccessToken()}.example.com/x`, 'iiiiiiii'],
      ['a token only the punycode-decoded label shows', `https://${encoded}/x`, encoded],
      ['a forge token as a subdomain, no slashes', `https:${token}.evil.example.com/x`, token],
    ] as const) {
      refusedLink(url, secret, what)
    }
    // Userinfo is dropped, not read: a password in it never reaches the screen,
    // so it is no reason to refuse the link.
    expect(text(`https://u:${mixed}@github.com/x`, 'url')).toBe('https://github.com/x')
    expect(text(`https://${mixed}@github.com/x`, 'url')).toBe('https://github.com/x')
    // The hosts a link ordinarily has are kept exactly as they came.
    for (const url of [
      'https://github.com/owner/name/pull/412',
      'https://bitbucket.org/owner/name/pull-requests/7',
      'https://console.cloud.google.com/run/detail/us-east5/service/logs?project=fixture-project',
      'https://us-east-1.console.aws.amazon.com/cloudwatch/home?region=us-east-1',
      'https://internationalbusinessmachinescorporation.example.com/x',
      'https://edgeweb-abc123xyz-ue.a.run.app/status',
      'https://my-company-production-environment.example.com/x',
      // A port is not part of the host: `secret:8443` is not an assignment.
      'https://secret:8443/x',
    ]) {
      expect(text(url, 'url'), url).toBe(url)
    }
  })

  it('refuses a high-entropy query or fragment value that follows a dot', () => {
    // Review round 4 of PR 478: the entropy net never starts a run right after
    // a dot, so `v1.<random>` hid the random half.
    const run = fake.highEntropyRun()
    for (const [what, url] of [
      ['a query value after a dot', `https://x.com/?id=v1.${run}`],
      ['a fragment value after a dot', `https://x.com/#v1.${run}`],
      ['a value between dots', `https://x.com/?id=a.${run}.b`],
    ] as const) {
      refusedLink(url, run, what)
    }
    for (const url of [
      'https://github.com/o/r/releases?tag=v1.2.3',
      'https://github.com/o/r/blob/main/x?file=file.name.ext',
      'https://github.com/o/r/pull/1#claude-sonnet-4-5-20250929.json',
      'https://github.com/o/r/pull/1?ref=release.2026-10-17.candidate',
    ]) {
      expect(text(url, 'url'), url).toBe(url)
    }
  })

  it('refuses the well-known webhook URLs, whose secret is a path segment', () => {
    // Review round 4 of PR 478: a path segment is left out of the entropy net on
    // purpose (a commit link's digest is one), so a webhook whose secret lives
    // in its path is refused by its shape instead.
    const id = '123456789012345678'
    const secret = fake.inert('Ab', 'cD', 'ef', 'GH', 'ij', 'KL', 'mn', 'OP')
    for (const [what, url] of [
      ['Slack', `https://hooks.slack.com/services/T0000/B0000/${secret}`],
      ['Slack, upper case and a trailing dot', `https://HOOKS.slack.com./services/T0000/B0000/${secret}`],
      ['Slack, a percent-encoded path', `https://hooks.slack.com/%73ervices/T0000/B0000/${secret}`],
      ['Slack workflows', `https://hooks.slack.com/workflows/T0000/A0000/1/${secret}`],
      ['Discord', `https://discord.com/api/webhooks/${id}/${secret}`],
      ['Discord, versioned', `https://discord.com/api/v10/webhooks/${id}/${secret}`],
      ['Discord, the old host', `https://discordapp.com/api/webhooks/${id}/${secret}`],
      ['Discord, canary', `https://canary.discord.com/api/webhooks/${id}/${secret}`],
      ['Microsoft Teams', `https://acme.webhook.office.com/webhookb2/${id}/IncomingWebhook/${secret}`],
      ['Zapier', `https://hooks.zapier.com/hooks/catch/123/${secret}/`],
      ['IFTTT', `https://maker.ifttt.com/trigger/event/with/key/${secret}`],
      ['Telegram', `https://api.telegram.org/bot${id}:${secret}/sendMessage`],
      ['Outlook', `https://outlook.office.com/webhook/${id}/IncomingWebhook/${secret}`],
      ['Outlook 365', `https://outlook.office365.com/webhook/${id}/IncomingWebhook/${secret}`],
    ] as const) {
      refusedLink(url, secret, what)
    }
    // The same hosts' other pages are links like any other.
    for (const url of [
      'https://discord.com/channels/1/2',
      'https://slack.com/app_redirect?channel=general',
      'https://github.com/o/r/settings/hooks/123',
    ]) {
      expect(text(url, 'url'), url).toBe(url)
    }
  })

  it('refuses a webhook URL behind doubled slashes, dot segments, encoded separators or a www host', () => {
    // Review round 5 of PR 478: the anchored path patterns read the parsed
    // path, which keeps `//`, and the host lost only one trailing dot. Every
    // value is assembled at run time (see this file's header).
    const id = '123456789012345678'
    const secret = fake.inert('Ab', 'cD', 'ef', 'GH', 'ij', 'KL', 'mn', 'OP')
    const bot = `${id}:${secret}`
    for (const [what, url] of [
      ['Slack, a doubled slash', `https://hooks.slack.com//services/T0000/B0000/${secret}`],
      ['Slack, slashes inside', `https://hooks.slack.com/services//T0000/B0000/${secret}`],
      ['Slack, two trailing dots', `https://hooks.slack.com../services/T0000/B0000/${secret}`],
      ['Slack, a dot segment', `https://hooks.slack.com/./services/T0000/B0000/${secret}`],
      ['Slack, a parent segment', `https://hooks.slack.com/x/../services/T0000/B0000/${secret}`],
      ['Slack, an encoded parent segment', `https://hooks.slack.com/x/%2e%2e/services/T0000/B0000/${secret}`],
      ['Slack, an encoded slash', `https://hooks.slack.com/%2Fservices/T0000/B0000/${secret}`],
      ['Slack, an encoded parent behind encoded slashes', `https://hooks.slack.com/x%2F..%2Fservices/T0000/B0000/${secret}`],
      ['Slack, an encoded backslash', `https://hooks.slack.com/%5Cservices/T0000/B0000/${secret}`],
      ['Discord, a doubled slash first', `https://discord.com//api/webhooks/${id}/${secret}`],
      ['Discord, a doubled slash inside', `https://discord.com/api//webhooks/${id}/${secret}`],
      ['Discord, versioned, a doubled slash', `https://discord.com/api/v10//webhooks/${id}/${secret}`],
      ['Discord, www', `https://www.discord.com/api/webhooks/${id}/${secret}`],
      ['Discord, the old host with www', `https://www.discordapp.com/api/webhooks/${id}/${secret}`],
      ['Telegram, a doubled slash', `https://api.telegram.org//bot${bot}/sendMessage`],
      ['Telegram, two trailing dots', `https://api.telegram.org../bot${bot}/sendMessage`],
      ['Zapier, a doubled slash', `https://hooks.zapier.com//hooks/catch/123/${secret}/`],
      ['Teams, a doubled slash', `https://acme.webhook.office.com//webhookb2/${id}/IncomingWebhook/${secret}`],
      ['Outlook, a doubled slash', `https://outlook.office.com//webhook/${id}/IncomingWebhook/${secret}`],
    ] as const) {
      refusedLink(url, secret, what)
    }
  })

  it('decodes punycode exactly as the URL parser encodes it, many code points to a label', () => {
    // Review round 5 of PR 478: every label here inserts several code points,
    // so a decoder whose bias adaptation is wrong puts them in the wrong place
    // or decodes the wrong ones.
    for (const label of ['bücher', 'münchen-straße', 'ñandú', 'δοκιμή', 'испытание', '例え', 'aé1üb2öc3', 'résumé-café']) {
      const encoded = new URL(`https://${label}.example/`).hostname.split('.')[0] ?? ''
      expect(encoded.startsWith('xn--'), label).toBe(true)
      expect(punycodeDecode(encoded.slice(4)), label).toBe(label.normalize('NFC').toLowerCase())
    }
  })

  it('refuses, and never throws on, an xn-- label that is not punycode', () => {
    // Review round 5 of PR 478: the digit mapping was a signed comparison, so
    // `:;<=>?@` became negative digits, and enough of them drove the code point
    // negative and `String.fromCodePoint` threw out of `sanitizeText`.
    for (const url of [
      `https://xn--a${';'.repeat(200)}b.com/`,
      `https://xn--${'='.repeat(200)}.com/`,
      `https://xn--a${'!'.repeat(5)}.com/`,
      `https://xn--${'zzzzzzzzzz'.repeat(5)}.com/`,
    ]) {
      expect(() => sanitizeText(url, 'url'), url).not.toThrow()
      expect(refused(url, 'url'), url).toContain('the console will not')
    }
    for (const input of [';;;', 'a-;', '====', '@', '[', '`', '{', '_', '99999999999999999999', '-é', '9999999999']) {
      expect(punycodeDecode(input), input).toBeNull()
    }
  })

  it('never lets an exception escape the url class, whatever the xn-- label holds', () => {
    // A fuzz loop over labels built from letters, digits and the ASCII
    // punctuation, with a fixed seed so a failure reproduces. sanitizeText
    // never throws: a value it will not draw comes back as a refusal.
    const alphabet = `abcxyzABCXYZ0189-!"#$&'()*+,;=:<>?@[]^_\`{|}~.%`
    let seed = 0x5eed
    const next = (limit: number): number => {
      seed = (seed * 1103515245 + 12345) & 0x7fffffff
      return seed % limit
    }
    for (let round = 0; round < 3000; round += 1) {
      let label = ''
      const length = 1 + next(250)
      // Runs of one character as well as single ones: a long run of a
      // punctuation digit is what drove the old decoder's code point negative.
      while (label.length < length) {
        label += (alphabet[next(alphabet.length)] ?? '').repeat(next(4) === 0 ? 1 + next(200) : 1)
      }
      for (const url of [`https://xn--${label}.com/`, `https://a.xn--${label}/x`, `https://xn--${label}`]) {
        let result: ReturnType<typeof sanitizeText> | undefined
        expect(() => {
          result = sanitizeText(url, 'url')
        }, url).not.toThrow()
        expect(result, url).toBeDefined()
      }
    }
  })

  it('refuses a value that is not a string at all, naming the field', () => {
    for (const value of [null, undefined, 42, true, {}, []]) {
      const reason = refused(value, 'sentence', 'runs[0].title')
      expect(reason).toContain('runs[0].title')
      expect(reason).toContain('not text')
    }
  })

  it('refuses a string past the input bound before any rule runs', () => {
    // Every rule in this module is linear, and this bound is what keeps a
    // hostile 40 MB title from costing the renderer a frame.
    expect(MAX_TEXT_INPUT_LENGTH).toBe(MAX_REDACTABLE_STRING_LENGTH)
    const long = 'a'.repeat(MAX_TEXT_INPUT_LENGTH + 1)
    expect(refused(long, 'sentence', 'runs[0].title')).toContain('longer than the console will read')
    // At the bound it is accepted, and clipped by the display bound instead.
    expect(text('a'.repeat(MAX_TEXT_INPUT_LENGTH), 'sentence')).toHaveLength(MAX_TEXT_LENGTH)
  })

  it('bounds the text again after NFKC, which can grow it eighteen-fold', () => {
    // Review round 3 of PR 478: the bound was measured before normalisation, so
    // 65 000 x U+FDFA (one code unit each, eighteen after NFKC) passed it and
    // every rule then ran over more than a million characters.
    const growth = String.fromCharCode(0xfdfa)
    expect(growth.normalize('NFKC').length).toBe(18)
    const grows = growth.repeat(4000)
    expect(grows.length).toBeLessThan(MAX_TEXT_INPUT_LENGTH)
    expect(grows.normalize('NFKC').length).toBeGreaterThan(MAX_TEXT_INPUT_LENGTH)
    for (const textClass of ['sentence', 'label', 'branch'] as const) {
      expect(refused(grows, textClass, 'runs[0].title'), textClass).toContain('longer than the console will read')
    }
    // A value that grows but stays inside the bound is still read.
    expect(text(growth.repeat(10), 'sentence')).toBe(growth.repeat(10))
  })
})

describe('safeUrl', () => {
  it('is the only way a link is built, and refuses everything else', () => {
    expect(safeUrl('https://github.com/owner/name/pull/412')).toBe(
      'https://github.com/owner/name/pull/412',
    )
    expect(safeUrl('https://user:pass@github.com/x')).toBe('https://github.com/x')
    for (const bad of [
      'javascript:alert(1)',
      'data:text/html,x',
      'file:///etc/passwd',
      'http://github.com/x',
      '',
      null,
      undefined,
      42,
      URL_MARKER,
      TOKEN_MARKER,
      PATH_MARKER,
      'slug-6a7c7d06',
    ]) {
      expect(safeUrl(bad), String(bad)).toBeNull()
    }
  })

  it('is what `putLink` uses, so a screen cannot go around it', () => {
    const target = element()
    putLink(target, 'javascript:alert(1)', 'finished[0].pr_url')
    expect(target.getAttribute(DESTINATION_ATTRIBUTE)).toBeNull()
    expect(target.textContent).toBe(UNAVAILABLE_MARK)
    expect(target.getAttribute('title')).toContain('not https')
    expect(target.writes).not.toContain(`setAttribute(${DESTINATION_ATTRIBUTE})`)
  })
})

describe('the markers this layer can read', () => {
  it('is the recorder\u2019s own list, marker for marker', () => {
    // A marker this layer could not read would be drawn as text, which tells a
    // person a redaction is a value.
    expect([...SHARED_MARKERS]).toEqual([...MARKERS])
    expect(PATH_PLACEHOLDER).toBe(PATH_MARKER)
    expect(PROJECT_PLACEHOLDER).toBe(PROJECT_MARKER)
    expect(BUCKET_PLACEHOLDER).toBe(BUCKET_MARKER)
    expect(HOST_PLACEHOLDER).toBe(HOST_MARKER)
    expect(BRANCH_PLACEHOLDER).toBe(BRANCH_MARKER)
    expect(TOKEN_PLACEHOLDER).toBe(TOKEN_MARKER)
    expect(URL_PLACEHOLDER).toBe(URL_MARKER)
    expect(ADDRESS_PLACEHOLDER).toBe(ADDRESS_MARKER)
    expect(LITERAL_PLACEHOLDER).toBe(LITERAL_MARKER)
  })

  it('bounds the same things the recorder and the IPC layer bound', () => {
    expect(MAX_URL_LENGTH).toBe(IPC_MAX_URL_LENGTH)
    expect(MAX_TEXT_INPUT_LENGTH).toBe(MAX_REDACTABLE_STRING_LENGTH)
    expect(MAX_REPOSITORY_LENGTH).toBeGreaterThan(0)
  })

  it('recognises a marker, and a pseudonym, and nothing else', () => {
    for (const marker of MARKERS) expect(isMarkerText(marker), marker).toBe(true)
    expect(isMarkerText('r-4c25cf20/r-f1aa9214')).toBe(true)
    expect(isMarkerText('slug-6a7c7d06')).toBe(true)
    expect(isMarkerText('person-2f9a4c1e')).toBe(true)
    // The near misses: a marker with a word around it is a sentence, and an
    // address that says `ana` still says `ana`.
    expect(isMarkerText('see <path> for it')).toBe(false)
    expect(isMarkerText('<path>x')).toBe(false)
    expect(isMarkerText('ana@example.invalid')).toBe(false)
    expect(isMarkerText('')).toBe(false)
    expect(isMarkerText('slug-6A7C7D06')).toBe(false)
  })

  it('names somebody only when the attribution has somebody to name', () => {
    // The owner's ruling of 2026-10-31: when there is no one to name, the whole
    // phrase goes. `nameableText` is the one rule behind "declared by ..." and
    // "KILLED by ...".
    expect(nameableText('ana@example.invalid')).toBe(true)
    expect(nameableText('Ana')).toBe(true)
    for (const nobody of ['', '   ', '[redacted]', 'redacted@example.invalid', 'r-4c25cf20', 'person-2f9a4c1e', '@example.invalid']) {
      expect(nameableText(nobody), nobody).toBe(false)
    }
    // And an address with nothing before its `@` is refused however much
    // whitespace it arrives with: `' @example.invalid'` names nobody either, and
    // a bare `startsWith('@')` would have drawn the sentence over it.
    expect(nameableText(' @example.invalid')).toBe(false)
    expect(nameableText('\t@example.invalid')).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// The scans. A rule that lives only in a reviewer's head is a rule a later task
// drops, so the two rules this module exists to hold are also read off the
// filesystem.
// ---------------------------------------------------------------------------

const SRC = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', '..', 'src')
const RENDERER_DIR = path.join(SRC, 'renderer')
const SHARED_DIR = path.join(SRC, 'shared')

/**
 * sourceFiles lists the `.ts` files under a directory **and its subdirectories**,
 * so a scan runs over what is actually there rather than over a list a task
 * forgot to update.
 *
 * Review round 5 of PR 478: this read one directory level, and
 * `src/renderer/` holds only `app.ts` -- so the three scans below never read the
 * five screens in `src/renderer/screens/`, while `app.ts`'s own header and this
 * file's comment claimed "no file under `src/renderer/`". The guard below only
 * required a non-empty list, which `app.ts` alone satisfied. It is
 * `ipc.test.ts`'s recursive walk now, and the guard names the directory the
 * screens live in so that moving them fails the test rather than the scan.
 */
function sourceFiles(directory: string): readonly string[] {
  const walk = (current: string): string[] => {
    const found: string[] = []
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const full = path.join(current, entry.name)
      if (entry.isDirectory()) found.push(...walk(full))
      else if (entry.name.endsWith('.ts')) found.push(full)
    }
    return found
  }
  return walk(directory).sort()
}

/**
 * FORBIDDEN_IN_RENDERER is the list the plan's lint-style test names: the
 * properties that parse a string as markup, the method that does, and the ways
 * to turn a string into code. The names are assembled here so that this file --
 * which is not under `src/renderer/` -- can name them, and so that the scan's
 * own list is not a plain spelling of the thing it scans for.
 */
const FORBIDDEN_IN_RENDERER: readonly string[] = Object.freeze([
  ['inner', 'HTML'].join(''),
  ['outer', 'HTML'].join(''),
  ['insertAdjacent', 'HTML'].join(''),
  ['document', '.write'].join(''),
  ['new ', 'Function('].join(''),
  ['ev', 'al('].join(''),
])

/** FORBIDDEN_FORMATTERS are the numeric formatters `availability.ts` exists to
 * replace: a screen that formats its own number has gone around the state. */
const FORBIDDEN_FORMATTERS: readonly string[] = Object.freeze([
  'toFixed(',
  'toLocaleString(',
  ['Intl', '.NumberFormat'].join(''),
])

/** NAVIGATING_ATTRIBUTE is the attribute a screen must never write, assembled
 * from two halves so that this file -- which is not under `src/renderer/` -- can
 * name it without the scan's own source being a spelling of the thing it looks
 * for. The URL belongs in `data-href` (`DESTINATION_ATTRIBUTE`), which the main
 * process turns into a destination it allows. */
const NAVIGATING_ATTRIBUTE = ['h', 'ref'].join('')

/** navigatingOffences reports the lines of a source that write that attribute:
 * the assignment, and the literal attribute name in a `setAttribute` call. A
 * comment may name it, and `data-href` is not it. */
function navigatingOffences(source: string): readonly string[] {
  return source
    .split('\n')
    .filter(
      (line) =>
        line.includes(`.${NAVIGATING_ATTRIBUTE} =`) ||
        line.includes(`setAttribute('${NAVIGATING_ATTRIBUTE}'`) ||
        line.includes(`setAttribute("${NAVIGATING_ATTRIBUTE}"`),
    )
}

/** offenders reports the lines of a source that contain a forbidden token. */
function offenders(source: string, forbidden: readonly string[]): readonly string[] {
  const found: string[] = []
  for (const line of source.split('\n')) {
    for (const token of forbidden) {
      if (line.includes(token)) found.push(`${token}: ${line.trim()}`)
    }
  }
  return found
}

describe('src/renderer/', () => {
  it('has files for these scans to look at', () => {
    // Without this, a scan over a directory that moved passes vacuously --
    // and it names the screens rather than merely asserting a non-empty list,
    // because `app.ts` alone satisfied that while the five files the scans are
    // really about went unread (review round 5 of PR 478).
    expect(sourceFiles(RENDERER_DIR)).toContain(path.join(RENDERER_DIR, 'app.ts'))
    expect(sourceFiles(RENDERER_DIR)).toContain(path.join(RENDERER_DIR, 'screens', 'live.ts'))
    expect(sourceFiles(RENDERER_DIR).length).toBeGreaterThanOrEqual(6)
    expect(sourceFiles(SHARED_DIR).length).toBeGreaterThan(0)
  })

  it('recognises a forbidden assignment when it sees one', () => {
    // The scan's own red step: the same function, run over a planted
    // assignment, must report it. A scan that cannot see its own mutation is
    // decoration.
    const planted = `element.${['inner', 'HTML'].join('')} = jobTitle`
    expect(offenders(planted, FORBIDDEN_IN_RENDERER)).toHaveLength(1)
    expect(offenders('element.textContent = jobTitle', FORBIDDEN_IN_RENDERER)).toEqual([])
    expect(offenders('const text = value.toFixed(2)', FORBIDDEN_FORMATTERS)).toHaveLength(1)
    expect(offenders('const text = value.toLocaleString()', FORBIDDEN_FORMATTERS)).toHaveLength(1)
    // The navigating attribute has a rule of its own, so it has a red step of
    // its own: the assignment, the literal attribute name -- and `data-href`,
    // which is where the URL actually goes and is not an offence.
    expect(navigatingOffences(`link.${NAVIGATING_ATTRIBUTE} = row.prUrl`)).toHaveLength(1)
    expect(navigatingOffences(`link.setAttribute('${NAVIGATING_ATTRIBUTE}', row.prUrl)`)).toHaveLength(1)
    expect(navigatingOffences(`link.setAttribute("${NAVIGATING_ATTRIBUTE}", row.prUrl)`)).toHaveLength(1)
    expect(navigatingOffences(`link.setAttribute('data-${NAVIGATING_ATTRIBUTE}', row.prUrl)`)).toEqual([])
    expect(navigatingOffences('// the URL goes in data-href, never in href')).toEqual([])
  })

  it('never interprets a string as markup', () => {
    for (const file of sourceFiles(RENDERER_DIR)) {
      const source = readFileSync(file, 'utf8')
      expect(offenders(source, FORBIDDEN_IN_RENDERER), file).toEqual([])
    }
  })

  it('never formats a bare number', () => {
    for (const file of sourceFiles(RENDERER_DIR)) {
      const source = readFileSync(file, 'utf8')
      expect(offenders(source, FORBIDDEN_FORMATTERS), file).toEqual([])
    }
  })

  it('never writes a destination into a navigating attribute', () => {
    // The forbidden spellings are the assignment and the literal attribute
    // name, not the word: a comment may explain which attribute is refused, and
    // `data-href` is where the URL actually goes.
    for (const file of sourceFiles(RENDERER_DIR)) {
      expect(navigatingOffences(readFileSync(file, 'utf8')), file).toEqual([])
    }
  })
})

describe('the shared modules', () => {
  it('import nothing at runtime, because the renderer has no Node', () => {
    // `src/source/redact.ts` reaches for `node:crypto` and `src/main/` reaches
    // for Electron, so a runtime import from either would be a module the
    // renderer cannot load. The vocabulary is shared by equality in this suite
    // instead, and this scan is what keeps that the only way to share it.
    for (const file of sourceFiles(SHARED_DIR)) {
      const source = readFileSync(file, 'utf8')
      const imports = source.match(/^\s*import\s[^\n]*from\s+['"][^'"]+['"]/gm) ?? []
      for (const statement of imports) {
        const specifier = /from\s+['"]([^'"]+)['"]/.exec(statement)?.[1] ?? ''
        expect(specifier.startsWith('.'), `${file}: ${statement.trim()}`).toBe(true)
        expect(specifier, `${file}: ${statement.trim()}`).not.toMatch(/node:|electron/)
      }
      // A `require` would be a runtime import the scan above cannot see.
      expect(source, file).not.toContain(['requ', 'ire('].join(''))
      expect(source, file).not.toContain('process.')
    }
  })

  it('is what app.ts\u2019s writers use, so a screen cannot go around them', () => {
    const source = readFileSync(path.join(RENDERER_DIR, 'app.ts'), 'utf8')
    expect(source).toContain('sanitizeText')
    expect(source).toContain('formatReading')
    expect(source).toContain('safeUrl')
  })
})

describe('the writers', () => {
  it('writes a measured reading as its number, with no title to explain', () => {
    const target = element()
    putReading(target, measured(12.5), 'usd')
    expect(target.textContent).toBe('$12.50')
    expect(target.getAttribute('title')).toBeNull()
    expect(target.writes).toEqual(['textContent'])
  })

  it('writes a reading with no number as n/a, with the reason as its title', () => {
    const target = element()
    const nothing: Reading = {
      state: 'unavailable',
      value: null,
      because: 'the document has no total.counted_usd',
      lowerBound: false,
    }
    putReading(target, nothing, 'usd')
    expect(target.textContent).toBe(UNAVAILABLE_MARK)
    expect(target.getAttribute('title')).toBe('the document has no total.counted_usd')
  })

  it('writes refused text as n/a, with the reason as its title', () => {
    const target = element()
    const hostile = 'not-an-owner-name'
    putText(target, hostile, 'repository', 'runs[0].repo')
    expect(target.textContent).toBe(UNAVAILABLE_MARK)
    const title = target.getAttribute('title') ?? ''
    expect(title).toContain('runs[0].repo')
    expect(title).not.toContain(hostile)
  })

  it('writes a link as text and a destination, never as a navigating attribute', () => {
    const target = element()
    putLink(target, 'https://github.com/owner/name/pull/412', 'finished[0].pr_url')
    expect(target.textContent).toBe('https://github.com/owner/name/pull/412')
    expect(target.getAttribute(DESTINATION_ATTRIBUTE)).toBe('https://github.com/owner/name/pull/412')
    expect(target.writes).toEqual([
      'textContent',
      'setAttribute(title)',
      `setAttribute(${DESTINATION_ATTRIBUTE})`,
    ])
  })

  it('renders a refused link as its reason, and links nothing', () => {
    const target = element()
    putLink(target, 'http://github.com/x', 'finished[0].pr_url')
    expect(target.textContent).toBe(UNAVAILABLE_MARK)
    expect(target.getAttribute('title')).toContain('not https')
    expect(target.getAttribute(DESTINATION_ATTRIBUTE)).toBeNull()
  })

  it('renders the recorder\u2019s own URL marker as text, and offers no link', () => {
    const target = element()
    putLink(target, URL_MARKER, 'finished[0].pr_url')
    expect(target.textContent).toBe(URL_MARKER)
    expect(target.getAttribute(DESTINATION_ATTRIBUTE)).toBeNull()
    expect(target.getAttribute('title')).toContain('redaction')
  })
})

describe('the coordinator\u2019s pull request document (coordinator v2 C3)', () => {
  // The nine keys of `coordinator show`'s document (`ee/coordinator/view.go`,
  // `PRDocument`, contract §6.15). Two of them are not strings; the seven that
  // are must each name a class in `schema.ts`: no console operation reads this
  // document since the Coordinator tab became the map (I-ORCH), and it is
  // classed anyway -- the console's class map covers every string key of every
  // document the contract defines, and the host's own words are the reason
  // `state` is classed here rather than left beside the enums in
  // `NON_TEXT_STRING_KEYS`, where a key means "the console compares or sorts
  // this, it never draws it".
  it('classes every string key of the document, the description included', () => {
    const strings = COORDINATOR_PR_DOCUMENT_KEYS.filter(
      (key) => key !== 'number' && key !== 'draft',
    )
    expect(strings).toHaveLength(7)
    for (const key of strings) expect(wireTextClass(key), key).toBeDefined()
    expect(wireTextClass('body')).toBe('sentence')
    expect(wireTextClass('title')).toBe('sentence')
    expect(wireTextClass('url')).toBe('url')
    expect(wireTextClass('base')).toBe('branch')
    expect(wireTextClass('branch')).toBe('branch')
    expect(wireTextClass('state')).toBe('label')
    // `author` is the host's own account id -- GitHub's numeric user id,
    // Bitbucket's `account_id` -- and **not** a login: the `person` class takes
    // a login and refuses anything else (`LOGIN_RE`), so Bitbucket's
    // `<digits>:<uuid>` would draw a refusal where a label is the honest answer.
    expect(wireTextClass('author')).toBe('label')
  })

  it('draws a hostile description as inert text, through the class the map gives it', () => {
    // A description is written by whoever opened the pull request, and it is the
    // one field that document adds to the stored view's record: a terminal escape, an OSC-8
    // hyperlink sequence, a bidi override and a line break, in one value. The
    // screen passes the class the map gives the key and nothing else, which is
    // the property this test holds.
    const hostile = [
      'a description the host holds: \u001b[31mred\u001b[0m and \u001b[2J',
      'a bidi override: evil/\u202egpj.exe',
      'an OSC-8 hyperlink: \u001b]8;;https://example.invalid/pull/1\u0007click here\u001b]8;;\u0007',
      'and a fourth line that is not a line',
    ].join('\n')
    const target = element()
    const textClass = wireTextClass('body')
    expect(textClass).toBe('sentence')
    putText(target, hostile, textClass, 'body')
    // The whole claim: the only write was a text write, so nothing here became
    // markup, an attribute or a link the renderer would follow.
    expect(target.writes).toEqual(['textContent'])
    expect(target.attributes.size).toBe(0)
    const drawn = target.textContent ?? ''
    expect(drawn).not.toBe(UNAVAILABLE_MARK)
    // The description is drawn, and it is one line of visible text: the escape
    // sequences and the bidi override are gone, the newlines are flattened, and
    // the sentence itself survives.
    expect(drawn).not.toMatch(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/)
    expect(drawn).not.toContain('\u202e')
    expect(drawn).toContain('and a fourth line that is not a line')
    expect(drawn).not.toContain('click here\u001b')
  })
})
