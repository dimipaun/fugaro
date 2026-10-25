// The rigor block, read once for the three screens that draw a run's mode
// (`console/src/shared/rigor.ts`; owner request 2026-10-25, the rigor-mode
// screens task).
//
// The block is written by the job into the run record and reaches the console
// as untrusted text, so this file holds the four decisions the reader makes,
// and holds them as values rather than as prose:
//
//   1. **a mode this build knows is drawn as this build's own word** -- the
//      five of `RIGOR_MODE_WORDS`, which are the owner's own names for the dial
//      (`docs/rigor-modes/spec.md`) and the words `runs ls`'s MODE column
//      writes in its own cell. The table below is that table, value for value,
//      because two renderers that spell one mode two ways are two answers to
//      "what was this run's rigor";
//   2. **anything else is drawn as bounded text, or as nothing at all** -- a
//      mode a newer CLI resolved is kept and put through the sanitizer rather
//      than hidden (the direction a screen has to fail in), and a block that
//      carries no mode or no layer is no block, which is what a record from
//      before the field answers too. The second half is `readRigor`'s three
//      `null`s, which the screens draw the same way: **"no record of this run's
//      rigor"**, never "it resolved nothing";
//   3. **the vocabulary is not reachable through the prototype** -- the raw
//      value is a job-written string and `constructor` is a key every object
//      literal has, so a lookup that walked the chain would draw a *function*
//      where a mode goes;
//   4. **the join fails closed** -- `rigorIndex` keys a `runs ls --json`
//      document by run id for the Live screen, and a row it cannot key or read
//      is simply absent, so a run the index does not know is drawn with no chip
//      at all rather than with another run's mode.
//
// The screen tests (`projects.test.ts`, `live.test.ts`, `run.test.ts`) hold
// what each screen draws with these answers; this file holds the answers.

import { describe, expect, it } from 'vitest'

import {
  RIGOR_ABSENT,
  RIGOR_LAYER_WORDS,
  RIGOR_MAX_AREAS,
  RIGOR_MODE_WORDS,
  readRigor,
  rigorIndex,
  rigorLayerWord,
  rigorModeWord,
} from '../../src/shared/rigor'
import { MAX_LABEL_LENGTH, TOKEN_PLACEHOLDER, sanitizeText } from '../../src/shared/sanitize'
import { githubToken } from '../support/fakeSecrets'

/** drawn is a raw mode as a screen draws it: the reader's `null` is the signal
 * to hand the *value* to `putText` with the `label` class (`app.ts`), and this
 * is that call, so the assertions below are about the text that would land in
 * the document. */
function drawn(value: string): string {
  const result = sanitizeText(value, 'label', 'runs[].rigor.mode')
  return result.ok ? result.text : `refused: ${result.reason}`
}

describe('rigorModeWord', () => {
  it('draws each of the five modes as the word `runs ls` writes', () => {
    // `internal/cli/runs.go`'s `modeCell` and `docs/rigor-modes/spec.md`: the
    // record's own value is the lowercase enum and the screen's word is the
    // owner's name for it, so `poc` is never drawn as `poc`.
    const cases: readonly (readonly [string, string])[] = [
      ['poc', 'POC'],
      ['startup', 'Startup'],
      ['pro', 'Pro'],
      ['critical', 'Critical'],
      ['hotfix', 'Hotfix'],
    ]
    for (const [mode, word] of cases) {
      expect(rigorModeWord(mode), mode).toBe(word)
    }
  })

  it('answers null for every mode this build does not know', () => {
    // The vocabulary is closed at the five and a screen that got a word for
    // anything else would be inventing a mode: a case variant, the wire value's
    // own spelling with a space around it, the resolver's later answer
    // (`reached`, which `runstore.RigorRecord.Bounded` refuses and which is
    // deliberately not in the table), a mode a newer CLI may add, and the
    // empty string. `null` is not a refusal -- it is the instruction to draw
    // the raw value through the text path.
    const unknown = [
      '',
      'PRO',
      'PoC',
      ' poc',
      'poc ',
      'standard',
      'reached',
      'none',
      'pro;critical',
      '\u0000poc',
      'a'.repeat(MAX_LABEL_LENGTH + 1),
    ]
    for (const mode of unknown) {
      expect(rigorModeWord(mode), JSON.stringify(mode)).toBeNull()
    }
  })

  it('cannot be reached through the prototype chain', () => {
    // A job-written string is the key, and every object literal has these: a
    // lookup that fell through to `Object.prototype` would answer a function,
    // and a screen would then draw a function where a mode goes.
    for (const key of ['constructor', 'toString', 'hasOwnProperty', '__proto__', 'valueOf']) {
      expect(rigorModeWord(key), key).toBeNull()
      expect(rigorLayerWord(key), key).toBeNull()
    }
    expect(Object.keys(RIGOR_MODE_WORDS)).toHaveLength(5)
    expect(Object.keys(RIGOR_LAYER_WORDS)).toHaveLength(5)
  })
})

describe('rigorLayerWord', () => {
  it('names the layer that set the mode in the console\'s own words', () => {
    // `internal/rigor`'s `Layer` (docs/rigor-modes D24): the run's own request,
    // an area rule the paths matched, the repository's `rigor.default`, the
    // project layer's, and Fugaro's own default.
    const cases: readonly (readonly [string, string])[] = [
      ['default', 'default'],
      ['project', 'the project layer'],
      ['repo', 'the repository'],
      ['area', 'an area rule'],
      ['request', 'the request'],
    ]
    for (const [from, word] of cases) {
      expect(rigorLayerWord(from), from).toBe(word)
    }
  })

  it('answers null for `reached`, which is never what a record says', () => {
    // `reached` is the resolver's answer to a later moment asking again (D22);
    // it is not written by a runner and `Bounded` refuses a block that names
    // it, so a record carrying it is a block this build does not understand and
    // is drawn as the raw text it is rather than given an invented word.
    expect(rigorLayerWord('reached')).toBeNull()
    expect(RIGOR_LAYER_WORDS['reached']).toBeUndefined()
    for (const from of ['', 'DEFAULT', 'defaults', 'project_layer', 'repo;area']) {
      expect(rigorLayerWord(from), from).toBeNull()
    }
  })
})

describe('readRigor', () => {
  it('reads the block\'s mode, layer and areas and nothing else', () => {
    // The record carries two more keys -- `at`, the moment the block was
    // written, and `revision`, the git ref the configuration was read from --
    // and no screen draws either, so the view does not carry them: a reader
    // that carried fields nobody checks is a reader a later change can start
    // trusting by accident.
    const block = {
      mode: 'critical',
      from: 'area',
      areas: [
        { name: 'payments', mode: 'critical', at: '2026-10-25T09:00:00Z' },
        { name: 'billing', mode: 'pro', at: '2026-10-25T09:00:00Z' },
      ],
      at: '2026-10-25T09:00:00Z',
      revision: 'origin/main',
    }
    expect(readRigor(block)).toEqual({
      mode: 'critical',
      from: 'area',
      areas: [
        { name: 'payments', mode: 'critical' },
        { name: 'billing', mode: 'pro' },
      ],
    })
    expect(Object.keys(readRigor(block) ?? {})).toEqual(['mode', 'from', 'areas'])
  })

  it('answers null for a value that is not a block', () => {
    // A record from before the field, a run that died before bootstrap, and a
    // document a job wrote something else into: one answer, because the screens
    // draw all of them the same way. An **array** is not a block either -- it
    // is an object to `typeof`, and reading `mode` off one would be reading a
    // key that is not there.
    for (const raw of [null, undefined, '', 'pro', 0, 1, true, false, [], ['pro'], [], () => null]) {
      expect(readRigor(raw), JSON.stringify(raw) ?? 'undefined').toBeNull()
    }
  })

  it('answers null when the block names no mode, or no layer', () => {
    // The two keys a decision is made of are not `omitempty` in the record, so
    // a block missing either is a block this reader will not show: an absent
    // mode is not a mode, and drawing one would answer "what was this run's
    // rigor" with a blank. A value of the wrong type is the same state -- the
    // record is JSON, and anything else arrived from something that is not the
    // runner.
    const half: readonly unknown[] = [
      { from: 'repo' },
      { mode: 'pro' },
      { mode: '', from: 'repo' },
      { mode: 'pro', from: '' },
      { mode: null, from: 'repo' },
      { mode: 'pro', from: null },
      { mode: 1, from: 'repo' },
      { mode: 'pro', from: ['repo'] },
      { mode: { word: 'pro' }, from: 'repo' },
    ]
    for (const block of half) {
      expect(readRigor(block), JSON.stringify(block)).toBeNull()
    }
    // `areas` is not one of the two: a block that resolved a mode and matched
    // no rule is a real block, and it draws with no area chips.
    expect(readRigor({ mode: 'pro', from: 'repo', areas: [] })).toEqual({ mode: 'pro', from: 'repo', areas: [] })
  })

  it('keeps a mode and a layer this build does not know, rather than hiding them', () => {
    // The direction this has to fail in: a newer CLI may resolve a mode this
    // console has never heard of, and a reader that answered `null` would hide
    // the run's rigor entirely. The values are kept as the raw strings they are
    // and drawn through the text path -- see the next block.
    expect(readRigor({ mode: 'standard', from: 'reached' })).toEqual({
      mode: 'standard',
      from: 'reached',
      areas: [],
    })
  })

  it('drops an area rule it cannot name, and one that sets no mode', () => {
    // `runstore.RigorRecord.Bounded` does the same with both, so the console and
    // the CLI drop the same entries: a rule with no name is not a rule a reader
    // can name, and one with no mode set nothing. A blank name is the same as a
    // missing one.
    const block = {
      mode: 'pro',
      from: 'area',
      areas: [
        { name: 'payments', mode: 'critical' },
        { name: '', mode: 'critical' },
        { name: 'billing' },
        { mode: 'pro' },
        { name: 'billing', mode: '' },
        { name: 'billing', mode: null },
        { name: null, mode: 'pro' },
        { name: 1, mode: 'pro' },
        'payments',
        null,
        ['payments', 'critical'],
      ],
    }
    expect(readRigor(block)).toEqual({
      mode: 'pro',
      from: 'area',
      areas: [{ name: 'payments', mode: 'critical' }],
    })
  })

  it('caps the area list at the number the CLI bounds it to', () => {
    // `rigor.MaxAreas`: the CLI drops the rest when it bounds a block, and a
    // console that drew an unbounded list would be drawing a list no reader of
    // the record could have checked. The cap is applied here as well as there,
    // because a console that trusted a bound it did not apply would be reading
    // a document rather than a decision.
    const many = Array.from({ length: RIGOR_MAX_AREAS + 25 }, (_, index) => ({
      name: `area-${String(index)}`,
      mode: 'pro',
    }))
    const read = readRigor({ mode: 'pro', from: 'area', areas: many })
    expect(read?.areas).toHaveLength(RIGOR_MAX_AREAS)
    expect(read?.areas[0]).toEqual({ name: 'area-0', mode: 'pro' })
    expect(read?.areas[RIGOR_MAX_AREAS - 1]).toEqual({ name: `area-${String(RIGOR_MAX_AREAS - 1)}`, mode: 'pro' })
  })

  it('draws an unknown mode and layer as bounded text, never as a word', () => {
    // The text path the screens use for the reader's `null`: `putText` with the
    // `label` class and the record's own field. A value the sanitizer will not
    // have is drawn as the console's `n/a` with the reason, never as the value.
    expect(rigorModeWord('standard')).toBeNull()
    expect(drawn('standard')).toBe('standard')
    expect(drawn('pro\u0000critical')).toBe('procritical')
    expect(drawn('pro‮critical')).toBe('procritical')
    expect(drawn('pro\ncritical')).toContain('refused:')
    expect(drawn('a'.repeat(MAX_LABEL_LENGTH + 1))).toContain('refused:')
  })

  it('masks a mode shaped like a credential, and never draws it', () => {
    // A mode is job-written text like any other (SECURITY.md D4), so a value
    // that is shaped like a token is masked in place by the free-text rules.
    const token = githubToken()
    expect(rigorModeWord(token)).toBeNull()
    expect(drawn(token)).toBe(TOKEN_PLACEHOLDER)
    expect(drawn(token)).not.toContain(token)
  })
})

describe('RIGOR_ABSENT', () => {
  it('is the dash `runs ls` draws, and is not a mode', () => {
    // The compact cell for "no record of this run's rigor" -- `runview.Row`'s
    // nil block, and the same dash `internal/cli/runs.go` writes. A run that
    // resolved Fugaro's own default carries a block saying `pro`, so the dash
    // and the default are two different cells and the dash is never a word the
    // vocabulary answers for.
    expect(RIGOR_ABSENT).toBe('-')
    expect(rigorModeWord(RIGOR_ABSENT)).toBeNull()
    expect(Object.values(RIGOR_MODE_WORDS)).not.toContain(RIGOR_ABSENT)
  })
})

describe('rigorIndex', () => {
  const pro = { mode: 'pro', from: 'repo', areas: [] }
  const critical = { mode: 'critical', from: 'area', areas: [{ name: 'payments', mode: 'critical' }] }

  it('keys each run\'s block by its id', () => {
    const index = rigorIndex({
      project: 'demo',
      runs: [
        { run: 'run-1', rigor: pro },
        { run: 'run-2', rigor: critical },
        { run: 'run-3' },
      ],
      warnings: [],
    })
    expect([...index.keys()]).toEqual(['run-1', 'run-2'])
    expect(index.get('run-1')).toEqual({ mode: 'pro', from: 'repo', areas: [] })
    expect(index.get('run-2')).toEqual({
      mode: 'critical',
      from: 'area',
      areas: [{ name: 'payments', mode: 'critical' }],
    })
    expect(index.get('run-3')).toBeUndefined()
  })

  it('answers an empty index for anything that is not a run list', () => {
    // The Live screen reads this on the poll's clock, so every shape it can
    // arrive in has to be a value rather than a throw: a refusal, a document
    // that is not an object, a list that is not there, and a `runs` that is not
    // an array.
    for (const document of [null, undefined, '', 'pro', 0, true, [], {}, { runs: null }, { runs: {} }, { runs: 'run-1' }]) {
      expect(rigorIndex(document).size, JSON.stringify(document) ?? 'undefined').toBe(0)
    }
  })

  it('reads the older `run_id` spelling where the CLI wrote it, preferring `run`', () => {
    // The same pair `projects.ts` reads in the same order: the record's id is
    // `run`, `run_id` is the spelling an older document carries, and a row that
    // carries both is keyed by the one the model uses.
    const index = rigorIndex({
      runs: [
        { run_id: 'run-1', rigor: pro },
        { run: 'run-2', run_id: 'run-other', rigor: critical },
      ],
    })
    expect([...index.keys()]).toEqual(['run-1', 'run-2'])
    expect(index.get('run-2')?.mode).toBe('critical')
    expect(index.has('run-other')).toBe(false)
  })

  it('leaves out a row it cannot key or cannot read', () => {
    // A run the index does not know is drawn with no chip at all, never with a
    // mode taken from another run: a row with no id, an empty id, an id that is
    // not text, a row that is not an object, and a block the reader will not
    // show are all simply absent.
    const index = rigorIndex({
      runs: [
        null,
        'run-1',
        [],
        {},
        { rigor: pro },
        { run: '', rigor: pro },
        { run: 1, rigor: pro },
        { run: 'run-2' },
        { run: 'run-3', rigor: null },
        { run: 'run-4', rigor: { mode: 'pro' } },
        { run: 'run-5', rigor: { mode: 'pro', from: 'repo' } },
      ],
    })
    expect([...index.keys()]).toEqual(['run-5'])
  })

  it('keeps the first block a repeated id carries', () => {
    // Two rows for one run is a document a reader should not see, and the
    // answer has to be one of them rather than a last-writer-wins merge: the
    // first is the one the list put first, which is the newest run's.
    const index = rigorIndex({
      runs: [
        { run: 'run-1', rigor: pro },
        { run: 'run-1', rigor: critical },
      ],
    })
    expect(index.size).toBe(1)
    expect(index.get('run-1')?.mode).toBe('pro')
  })

  it('is a map a screen can read without it changing under it', () => {
    // The Live screen holds this between polls, so it is read-only as a type
    // and as a value: the map the reader answers with is its own, and the
    // document it read is not consulted again.
    const document = { runs: [{ run: 'run-1', rigor: { mode: 'pro', from: 'repo', areas: [] } }] }
    const index = rigorIndex(document)
    expect(index.get('run-1')).toEqual({ mode: 'pro', from: 'repo', areas: [] })
    ;(document.runs[0] as { rigor: { mode: string } }).rigor.mode = 'hotfix'
    expect(index.get('run-1')?.mode).toBe('pro')
  })
})
