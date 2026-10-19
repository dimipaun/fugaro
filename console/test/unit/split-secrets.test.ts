// A secret split by an invisible character, a terminal sequence, or both, at
// every position, must not leave any door in a readable fragment (review rounds
// 2 and 3 of PR 540).
//
// The doors are the three readers of CLI text: the display layer's sentence
// and label classes (`sanitizeText`), its refusal door (`sanitizeRefusal`), and
// the recorder (`redactText`, which is `redactFreeText`). (The IPC argument
// check in src/main/ipc.ts refuses every one of these splitters outright --
// `hiddenCharacterReason` -- so no split value reaches a handler at all.)
//
// The secrets are realistic: mixed case, digits, the length a real issued
// token has (`realisticSecrets`, assembled at run time). The repeated-letter
// fixtures elsewhere are why an earlier fix passed while leaking: a fragment of
// `AKIA` + sixteen `G`s is a shape no entropy rule finds, so cutting one in two
// left nothing a test could see.
//
// The property: for every family, every split position, every splitter and
// every placement, no output holds any FRAGMENT_LENGTH-character window of the
// secret.

import { describe, expect, it } from 'vitest'

import { sanitizeRefusal, sanitizeText } from '../../src/shared/sanitize'
import { createRedactor } from '../../src/source/redact'
import { realisticSecrets } from '../support/fakeSecrets'

const FRAGMENT_LENGTH = 8

/** SPLITTERS are ten characters every reader strips and no terminal draws. */
const SPLITTERS: readonly (readonly [string, string])[] = [
  ['ZWSP', '​'],
  ['ZWNJ', '‌'],
  ['ZWJ', '‍'],
  ['word joiner', '⁠'],
  ['BOM', '﻿'],
  ['soft hyphen', '­'],
  ['LRM', '‎'],
  ['tag A', String.fromCodePoint(0xe0041)],
  ['VS16', '️'],
  ['CGJ', '͏'],
]

const ESC = '\u001b'

function leaks(output: string, secret: string): string | null {
  for (let at = 0; at + FRAGMENT_LENGTH <= secret.length; at += 1) {
    const window = secret.slice(at, at + FRAGMENT_LENGTH)
    if (output.includes(window)) return window
  }
  return null
}

function outputs(value: string): readonly (readonly [string, string])[] {
  const draw = (result: ReturnType<typeof sanitizeText>): string => (result.ok ? result.text : '')
  return [
    ['sentence', draw(sanitizeText(value, 'sentence'))],
    ['label', draw(sanitizeText(value, 'label'))],
    ['refusal', draw(sanitizeRefusal(value))],
    ['recorder', createRedactor().redactText(value)],
  ]
}

/** placements wrap the split secret the five ways the review named. */
function placements(split: string, other: string): readonly (readonly [string, string])[] {
  return [
    ['alone', `denied: ${split} was refused`],
    ['after a matched token', `token ${other} and ${split} was refused`],
    ['before a matched token', `${split} and then ${other} was refused`],
    ['inside a url', `see https://h.example/x/${split}/y now`],
    ['inside a path', `read /home/x/${split}/key failed`],
  ]
}

function check(value: string, secret: string, where: string, problems: string[]): void {
  for (const [door, output] of outputs(value)) {
    const fragment = leaks(output, secret)
    if (fragment !== null) problems.push(`${door} / ${where}: ${JSON.stringify(output)} holds ${fragment}`)
  }
}

describe('a realistic secret split at every position', () => {
  const secrets = realisticSecrets()
  const other = (secrets.find(([name]) => name === 'github')?.[1] ?? '').replace(/^gh/, 'gh')

  it.each(secrets)('%s: no door draws a fragment, for every position, splitter and placement', (name, secret) => {
    const problems: string[] = []
    const neighbour = name === 'github' ? (secrets.find(([n]) => n === 'npm')?.[1] ?? '') : other
    for (let position = 1; position < secret.length; position += 1) {
      for (const [splitterName, splitter] of SPLITTERS) {
        const split = `${secret.slice(0, position)}${splitter}${secret.slice(position)}`
        for (const [placement, value] of placements(split, neighbour)) {
          check(value, secret, `${placement}, ${splitterName} at ${String(position)}`, problems)
        }
      }
    }
    expect(problems.slice(0, 10), `${String(problems.length)} leaks`).toEqual([])
  }, 300_000)

  it('the review’s regression: a token cut in two by one zero-width space', () => {
    const github = secrets.find(([n]) => n === 'github')?.[1] ?? ''
    const problems: string[] = []
    check(`err ${github.slice(0, 21)}​${github.slice(21)}`, github, 'cut at 21', problems)
    expect(problems).toEqual([])
  })
})

describe('a key id that borrows a boundary and splits itself', () => {
  const key = realisticSecrets().find(([name]) => name === 'aws key id')?.[1] ?? ''

  it('is redacted in every door when split at two positions, with every pair of splitters', () => {
    const problems: string[] = []
    for (let position = 1; position < key.length; position += 1) {
      for (const [firstName, first] of SPLITTERS) {
        for (const [secondName, second] of SPLITTERS) {
          const value = `J${first}${key.slice(0, position)}${second}${key.slice(position)} was refused`
          check(value, key, `${firstName} then ${secondName} at ${String(position)}`, problems)
        }
      }
    }
    expect(problems.slice(0, 10), `${String(problems.length)} leaks`).toEqual([])
  }, 300_000)

  it('is redacted when split at three positions', () => {
    const problems: string[] = []
    for (let a = 1; a < key.length - 1; a += 1) {
      for (let b = a + 1; b < key.length; b += 1) {
        const value = `J​${key.slice(0, a)}⁠${key.slice(a, b)}­${key.slice(b)} x`
        check(value, key, `at ${String(a)} and ${String(b)}`, problems)
      }
    }
    expect(problems.slice(0, 10), `${String(problems.length)} leaks`).toEqual([])
  }, 300_000)
})

describe('a terminal sequence wrapped around a secret', () => {
  const secrets = realisticSecrets()

  it.each(secrets)('%s: an OSC 8 hyperlink or an SGR right before it lends no boundary a reader misses', (_name, secret) => {
    const problems: string[] = []
    const wrappers: readonly (readonly [string, string])[] = [
      ['OSC 8, ST', `J${ESC}]8;;https://e.example${ESC}\\${secret}${ESC}]8;;${ESC}\\ was refused`],
      ['OSC 8, BEL', `J${ESC}]8;;https://e.example\u0007${secret}${ESC}]8;;\u0007 was refused`],
      ['SGR', `J${ESC}[31m${secret}${ESC}[0m was refused`],
      ['SGR inside', `${secret.slice(0, 6)}${ESC}[1m${secret.slice(6)} was refused`],
      ['SGR and a splitter', `J${ESC}[31m${secret.slice(0, 9)}​${secret.slice(9)} was refused`],
    ]
    for (const [wrapper, value] of wrappers) check(value, secret, wrapper, problems)
    expect(problems).toEqual([])
  })
})

describe('the url class reads every reading', () => {
  const key = realisticSecrets().find(([name]) => name === 'aws key id')?.[1] ?? ''

  it('refuses a link whose key id only the form as written shows, as it refuses the plain one', () => {
    for (const value of [`https://h.example/J​${key}`, `https://h.example/${key}`]) {
      const result = sanitizeText(value, 'url')
      expect(result.ok, value).toBe(false)
    }
  })

  it('still draws an ordinary link', () => {
    const result = sanitizeText('https://github.com/dimipaun/fugaro/pull/540', 'url')
    expect(result.ok).toBe(true)
  })
})

describe('the split readings stay bounded', () => {
  // The same bound and the same timer as the escape-sequence suites: generous
  // because the machines these run on are shared. Measured on an idle-ish Mac
  // (review round 3 of PR 540): about 45 ms for the first, about 25-50 ms for
  // the rest, each door.
  const BOUND_MS = 250
  const fill = (unit: string): string => unit.repeat(Math.floor(65_536 / unit.length))
  const inputs: readonly (readonly [string, string])[] = [
    ['a zero-width space inside every word', fill('ab​cd ')],
    ['one 64 KiB word of zero-width spaces', fill('a​')],
    ['four places per word', fill('a​b​c​d​e ')],
    ['six places per word', fill('a​b​c​d​e​f​g ')],
    ['an SGR between every pair of letters', fill('\u001b[1mab')],
  ]
  function timed(run: () => unknown): number {
    const start = performance.now()
    run()
    return performance.now() - start
  }
  it.each(inputs)('%s', (_name, input) => {
    const redactor = createRedactor()
    // One untimed pass over a short slice, so the timing is the work and not
    // the first compilation of the rules in this worker.
    const warm = input.slice(0, 2_048)
    sanitizeText(warm, 'sentence')
    sanitizeRefusal(warm)
    redactor.redactText(warm)
    expect(timed(() => sanitizeText(input, 'sentence'))).toBeLessThan(BOUND_MS)
    expect(timed(() => sanitizeRefusal(input))).toBeLessThan(BOUND_MS)
    expect(timed(() => redactor.redactText(input))).toBeLessThan(BOUND_MS)
  })
})
