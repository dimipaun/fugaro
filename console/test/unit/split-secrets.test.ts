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

import {
  MAX_SPLIT_SCAN,
  MAX_TEXT_INPUT_LENGTH,
  resetScanWork,
  sanitizeRefusal,
  sanitizeText,
  scanWorkDone,
} from '../../src/shared/sanitize'
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
  // The bound is the work, not the clock. A wall clock measures the machine,
  // not the code: the 250 ms these inputs were held to (about 45 ms on an
  // idle-ish Mac when the bound was chosen, review round 3 of PR 540) measured
  // 435 ms on a loaded macOS runner, which failed the 0.9.6 and 0.9.7 release
  // gates over a runner that was merely busy. `scanWorkDone` counts what the
  // code chose to do -- the characters `traceCredentials` runs the tracked
  // rules over, summed across every reading (`resolveReadings`) -- and it is
  // the same number on every machine and in every run.
  //
  // The bound is the resolver's own. `MAX_SPLIT_SCAN` (sanitize.ts) is what one
  // pass of split readings may cost, readings times characters, and the reading
  // of the prepared form itself -- which every value pays whatever its places
  // are -- is at most `MAX_TEXT_INPUT_LENGTH`. A run whose combinations would
  // cost more than the budget allows is masked whole rather than read
  // (`MAX_SPLIT_PLACES`, `MAX_SPLIT_SCAN`), so this work cannot grow with the
  // number of places in the value: that is what makes these five pathological
  // inputs cost a bounded amount rather than the super-linear amount a reading
  // per run -- instead of a reading per combination across the runs, the
  // side-by-side reading `traceSplits` does -- would cost. What is NOT counted
  // here is each door's own, linear pass over the text (its rule list,
  // `applyRules`), which is why the bound is stated in the resolver's own terms
  // rather than as a multiple of the input.
  //
  // Mutations (verified, run and restored), both in traceSplits:
  //
  //  - drop `count > budget` from the masking test, so a run is read in every
  //    combination whatever the budget: "four places per word" costs 838,784
  //    (2.6x the bound) and "six places per word" 3,295,424 (10x), and both
  //    fail;
  //  - make `readings` the sum of the runs' combinations rather than their
  //    maximum -- one reading per run, the quadratic shape, where the
  //    side-by-side reading is one per combination across all of them: on the
  //    same five inputs shortened to 8 KiB (a full 64 KiB run would take
  //    minutes) "four places per word" costs 81,837,756, 250x the bound, and
  //    fails.
  const WORK_BOUND = MAX_SPLIT_SCAN + MAX_TEXT_INPUT_LENGTH
  const fill = (unit: string): string => unit.repeat(Math.floor(65_536 / unit.length))
  const inputs: readonly (readonly [string, string])[] = [
    ['a zero-width space inside every word', fill('ab​cd ')],
    ['one 64 KiB word of zero-width spaces', fill('a​')],
    ['four places per word', fill('a​b​c​d​e ')],
    ['six places per word', fill('a​b​c​d​e​f​g ')],
    ['an SGR between every pair of letters', fill('\u001b[1mab')],
  ]
  /** work is what `run` cost, in the characters the readings scanned: the
   * resolver's counter, read after one call and reset before the next. */
  function work(run: () => unknown): number {
    resetScanWork()
    try {
      run()
    } catch {
      // A refusal is an answer, and the work it took is still the work.
    }
    return scanWorkDone()
  }
  it.each(inputs)('%s', (_name, input) => {
    const redactor = createRedactor()
    expect(work(() => sanitizeText(input, 'sentence')), 'sentence').toBeLessThanOrEqual(WORK_BOUND)
    expect(work(() => sanitizeRefusal(input)), 'refusal').toBeLessThanOrEqual(WORK_BOUND)
    expect(work(() => redactor.redactText(input)), 'recorder').toBeLessThanOrEqual(WORK_BOUND)
  })
})
