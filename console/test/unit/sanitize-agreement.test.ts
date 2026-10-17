// The display layer (src/shared/sanitize.ts) and the recorder
// (src/source/redact.ts) are held to agree, and the CLI-stderr door
// (sanitizeRefusal) is held to redact every credential the text path redacts
// (review round 2 of PR 494). Every credential-shaped value here is built at run
// time by test/support/fakeSecrets.ts; none is written down.

import { describe, expect, it } from 'vitest'

import { TOKEN_PLACEHOLDER, invisibleRe, sanitizeRefusal, sanitizeText } from '../../src/shared/sanitize'
import { createRedactor } from '../../src/source/redact'
import * as fake from '../support/fakeSecrets'

/** fullwidth is the NFKC-equivalent fullwidth spelling of printable ASCII. */
function fullwidth(text: string): string {
  return [...text]
    .map((character) => {
      const code = character.charCodeAt(0)
      return code >= 0x21 && code <= 0x7e ? String.fromCharCode(code + 0xfee0) : character
    })
    .join('')
}

function drawn(result: ReturnType<typeof sanitizeText>): string {
  return result.ok ? result.text : `refused: ${result.reason}`
}

/** plainShapes is every credential shape as it is written, without the
 * spellings NFKC has to fold first. */
function plainShapes(): readonly string[] {
  return [
    fake.githubToken(),
    fake.githubPat(),
    fake.gitlabToken(),
    fake.npmToken(),
    fake.providerApiKey(),
    fake.slackToken(),
    fake.awsKeyId(),
    fake.googleApiKey(),
    fake.googleAccessToken(),
    fake.jwt(),
    fake.highEntropyRun(),
    fake.hexDigest(),
  ]
}

/** credentials is every credential shape the text path redacts, in both the
 * spelling it is written in and the fullwidth one only NFKC makes a token, plus
 * the two base64 spellings of one key. */
function credentials(): readonly string[] {
  const shapes = plainShapes()
  return [
    ...shapes,
    ...shapes.map(fullwidth),
    Buffer.from(fake.googleApiKey()).toString('base64'),
    `${Buffer.from(fake.googleApiKey()).toString('base64')}A`,
  ]
}

describe('the refusal door redacts what the text path redacts', () => {
  it('redacts a fullwidth token, which only NFKC makes a token', () => {
    const sentence = `permission denied for ${fullwidth(fake.providerApiKey())}`
    expect(drawn(sanitizeText(sentence, 'sentence'))).toBe(`permission denied for ${TOKEN_PLACEHOLDER}`)
    expect(drawn(sanitizeRefusal(sentence))).toBe(`permission denied for ${TOKEN_PLACEHOLDER}`)
  })

  it('runs the entropy net', () => {
    const sentence = `permission denied for ${fake.highEntropyRun()}`
    expect(drawn(sanitizeRefusal(sentence))).toBe(`permission denied for ${TOKEN_PLACEHOLDER}`)
  })

  it('agrees with the sentence class, value for value, over the credential corpus', () => {
    for (const token of credentials()) {
      const sentence = `permission denied for ${token} while reading`
      const text = drawn(sanitizeText(sentence, 'sentence'))
      expect(text, token).toContain(TOKEN_PLACEHOLDER)
      expect(drawn(sanitizeRefusal(sentence)), token).toBe(text)
    }
  })
})

describe('a base64 run whose length is 1 mod 4', () => {
  it('is decoded and redacted by both layers, the way Buffer.from reads it', () => {
    const b64 = Buffer.from(fake.googleApiKey()).toString('base64')
    expect(b64.length % 4).toBe(0)
    for (const suffix of ['A', 'AB', 'ABC', '']) {
      const text = `token ${b64}${suffix} end`
      expect(drawn(sanitizeText(text, 'sentence')), suffix).toBe(`token ${TOKEN_PLACEHOLDER} end`)
      expect(createRedactor().redactText(text), suffix).toBe(`token ${TOKEN_PLACEHOLDER} end`)
    }
  })
})

describe('a path split by a line break', () => {
  it('is a path to the recorder as it is to the display layer', () => {
    const redactor = createRedactor(['/home/zora'])
    const first = JSON.stringify(redactor.redactDocument({ message: 'checkout /home\nzora/.ssh here' }))
    expect(first).not.toMatch(/zora/i)
    const second = JSON.stringify(redactor.redactDocument({ message: 'in /Users/Zora\nKell/src/fugaro now' }))
    expect(second).not.toContain('Kell')
    expect(redactor.problems({ message: 'checkout /home\nzora/.ssh here' })).not.toEqual([])
    for (const text of ['checkout /home\nzora/.ssh here', 'in /Users/Zora\nKell/src/fugaro now']) {
      expect(drawn(sanitizeText(text, 'sentence'))).toBe(
        createRedactor().redactText(text).replace(/\s+/g, ' '),
      )
    }
  })
})

describe('a path-shaped secret', () => {
  it('is redacted whole, not split by the path rule', () => {
    const run = fake.highEntropyRun()
    const secret = `${run.slice(0, 13)}/${run.slice(13)}${run.slice(0, 4)}/${run.slice(4)}`
    const text = `deploy with ${secret} now`
    const recorded = JSON.stringify(createRedactor().redactDocument({ title: text }))
    expect(recorded).not.toContain(run.slice(0, 13))
    expect(recorded).toContain(TOKEN_PLACEHOLDER)
    expect(drawn(sanitizeText(text, 'sentence'))).toBe(`deploy with ${TOKEN_PLACEHOLDER} now`)
  })

  it('leaves an ordinary owner/name and a rooted path to their own rules', () => {
    expect(drawn(sanitizeText('see /home/someone/src/x for it', 'sentence'))).toBe('see <path> for it')
  })
})

describe('terminal escape sequences do not hide a token', () => {
  const ESC = String.fromCharCode(27)
  const BEL = String.fromCharCode(7)
  const jwt = (signature: string): string =>
    ['ey' + 'JhbGciOiJIUzI1NiJ9', 'ey' + 'JzdWIiOiIxMjM0NTY3ODkwIn0', signature].filter((part) => part !== '').join('.')

  const wrapped = (): readonly string[] => {
    const token = fake.githubToken()
    const key = fake.providerApiKey()
    return [
      `${ESC}[31m${token}${ESC}[0m`,
      `${ESC}[1;38;5;196m${key}${ESC}[0m`,
      `${ESC}]0;title${BEL}${token}`,
      `${ESC}]8;;x${ESC}\\${token}${ESC}]8;;${ESC}\\`,
      `${String.fromCharCode(0x9b)}31m${token}`,
      // a token split by a sequence in its middle
      `${token.slice(0, 8)}${ESC}[0m${token.slice(8)}`,
      jwt('abcDEF123_-xyz789').replace(/^/, `${ESC}[32m`),
    ]
  }

  it('is redacted by the sentence class, the refusal door and the recorder alike', () => {
    for (const value of wrapped()) {
      const sentence = `permission denied for ${value} while reading`
      const text = drawn(sanitizeText(sentence, 'sentence'))
      expect(text, JSON.stringify(value)).toBe(`permission denied for ${TOKEN_PLACEHOLDER} while reading`)
      expect(drawn(sanitizeRefusal(sentence)), JSON.stringify(value)).toBe(text)
      expect(createRedactor().redactText(sentence), JSON.stringify(value)).toBe(text)
    }
  })

  it('leaves no fragment of a sequence in a sentence that held no token', () => {
    const sentence = `${ESC}[31mpermission denied${ESC}[0m`
    expect(drawn(sanitizeRefusal(sentence))).toBe('permission denied')
    expect(drawn(sanitizeText(sentence, 'sentence'))).toBe('permission denied')
  })

  it('redacts a JWT whatever its signature, at the refusal door as in the sentence class', () => {
    for (const signature of ['', 'sig', 'abcDE', 'abcDEF123_-xyz789']) {
      const sentence = `rejected ${jwt(signature)} here`
      const text = drawn(sanitizeText(sentence, 'sentence'))
      expect(text, signature).toBe(`rejected ${TOKEN_PLACEHOLDER} here`)
      expect(drawn(sanitizeRefusal(sentence)), signature).toBe(text)
      expect(createRedactor().redactText(sentence), signature).toBe(text)
    }
  })
})

describe('an invisible character between a letter and a token', () => {
  // Each of these is a boundary to a token shape's lookbehind in the form as
  // written and nothing at all in the prepared form, so the token next to it is
  // one token to the recorder, which reads the form as written after the
  // prepared one (`redactFreeText`, src/source/redact.ts), and was no token to
  // the display layer, which read only the stripped form: it drew the key id
  // whole beside a fixture that had redacted it. A zero-width space, a bidi
  // mark, a word joiner, a combining grapheme joiner and a byte-order mark.
  const SPLITS = ['​', '‎', '⁠', '͏', '﻿']

  it('is redacted by the sentence class, the refusal door and the recorder alike', () => {
    for (const split of SPLITS) {
      const sentence = `J${split}${fake.awsKeyId()} was refused`
      const text = drawn(sanitizeText(sentence, 'sentence'))
      expect(text, JSON.stringify(split)).toBe(`J${TOKEN_PLACEHOLDER} was refused`)
      expect(drawn(sanitizeRefusal(sentence)), JSON.stringify(split)).toBe(text)
      // The recorder draws the redaction from the form as written, which still
      // holds the split character: it is this layer's own rule, not one of the
      // recorder's, that a drawn value carries no invisible character, so the
      // two agree once this layer's strip is applied to the recorder's answer.
      expect(createRedactor().redactText(sentence).replace(invisibleRe(), ''), JSON.stringify(split)).toBe(text)
    }
  })

  it('holds every token family, in the label class as in the sentence class', () => {
    for (const token of plainShapes()) {
      const value = `x​${token}`
      // The recorder's answer is the expectation: what this layer draws must be
      // what the fixture beside it holds, and neither may hold the token.
      const recorded = createRedactor().redactText(value).replace(invisibleRe(), '')
      expect(recorded, token).not.toContain(token)
      for (const textClass of ['sentence', 'label'] as const) {
        const text = drawn(sanitizeText(value, textClass))
        expect(text, `${textClass}: ${token}`).not.toContain(token)
        expect(text, `${textClass}: ${token}`).toBe(recorded)
      }
    }
  })
})
