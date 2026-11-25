// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The settings edit window's view (the settings-edit plan's C2, D1108, D1109,
// D1113, D1116).
//
// `settingsView` is what the window is shown, built here from three things and
// nothing else: the dry run's document (`settings set|unset --dry-run --json`),
// the read's own row (`settings --json`), and the argv the confirmed edit
// spawns. It is a pure function in `src/main/`, with no Electron in it, so the
// whole of the window's content is asserted here under plain Node -- the way
// `confirm-view.test.ts` asserts the launch window's.
//
// Each test names the mutation it catches:
//
//   - a view that **decided the shape for itself** fails "a widening is two
//     answers": the shape is the dry run's own `widening` member, so a console
//     that re-derived it from the value would draw a one-click Confirm over a
//     write that raises a limit;
//   - a view that **made Confirm answerable without the tick** fails the same
//     case's `settingsConfirmAnswerable` assertions;
//   - a view that **showed the sentence always, or never** fails "exactly when
//     the layer written is not the layer that wins", which asserts both
//     directions;
//   - a view that **coerced a wrongly-typed member** fails the shape case: a
//     `widening` spelled `"true"` would make a widening a one-click edit, and an
//     `old` that is a number would draw a value the CLI never printed;
//   - a view that **spread the read's row into itself** fails "carries no member
//     the read's row could smuggle a sensitive value through": the row is the
//     drawer's, and a sensitive one is never drawn at all (D1111), so nothing
//     of it may reach the window through a member the contract does not name;
//   - a view that **showed a different argv from the one built** fails "shows
//     the argv the builder produced";
//   - a view that **invented the file a `repo`-layer edit writes, or read it
//     from anywhere but the argv** fails "names the file a `repo`-layer edit
//     writes" (RC80, L12): the target line the window draws is the argv's own
//     `--repo` word, so the window and the spawn cannot name two files.

import { describe, expect, it } from 'vitest'

import {
  SETTINGS_ENTRY_ABOVE_MAX,
  SETTINGS_ENTRY_BELOW_MIN,
  SETTINGS_ENTRY_CAVEAT,
  SETTINGS_ENTRY_DURATION,
  SETTINGS_ENTRY_READONLY,
  SETTINGS_ENTRY_SELECT,
  SETTINGS_ENTRY_TOGGLE,
  SETTINGS_ENTRY_WHOLE,
  SETTINGS_SHADOWED_SENTENCE,
  SETTINGS_TICK_LABEL,
  SETTINGS_VIEW_DOC_VERSION,
  SETTINGS_VIEW_SHAPE_BECAUSE,
  settingsConfirmAnswerable,
  settingsEntryError,
  settingsEntryView,
  settingsView,
} from '../../src/main/settingsview'
import type {
  SettingsEntryControl,
  SettingsEntryMeta,
  SettingsEntryRow,
  SettingsView,
  SettingsViewInputs,
  SettingsViewRow,
} from '../../src/main/settingsview'

/** DRY is `settings set --dry-run --json` (contract §6.20), invented: a
 * narrowing write of one scalar into the project layer. */
const DRY = {
  version: 1,
  key: 'budget.per_run_usd',
  layer: 'project',
  path: 'layer.yaml',
  changed: true,
  widening: false,
  why: '',
  old: '8',
  new: '12',
  refusal: '',
}

/** ROW is the read's own row for that key, as `settings --json` answers it. */
const ROW: SettingsViewRow = {
  key: 'budget.per_run_usd',
  value: '8',
  wins: 'project',
  targetLayer: 'project',
}

/** ARGV is the command line the confirmed edit spawns, field by field. It is
 * the caller's -- `argvFor`'s answer for the write (C3) -- and the view shows
 * it rather than a summary of it. */
const ARGV: readonly string[] = Object.freeze([
  'fugaro',
  'settings',
  'set',
  'budget.per_run_usd',
  '--layer',
  'project',
  '--',
  '12',
  '--project',
  'example-project',
])

/** VIEW_MEMBERS is the whole of what the window is shown, and it is asserted as
 * a set: a member added without a decision here is a member nobody chose. */
const VIEW_MEMBERS: readonly string[] = Object.freeze([
  'key',
  'oldValue',
  'newValue',
  'layer',
  'target',
  'wins',
  'sentence',
  'widening',
  'why',
  'argv',
  'defaultButton',
  'tick',
  'confirmNeedsTick',
])

/** REPO_ARGV is the same write aimed at a **repository's** `fugaro.yaml`
 * (RC80, L12): `--layer repo` names the document's kind and `--repo
 * <directory>` names which checkout it is -- the one word of this argv that is
 * the installation's own directory rather than a fact of the edit. */
const REPO_ARGV: readonly string[] = Object.freeze([
  'fugaro',
  'settings',
  'set',
  'budget.per_run_usd',
  '--layer',
  'repo',
  '--repo',
  '/Users/owner/src/checkout',
  '--',
  '12',
  '--project',
  'example-project',
])

/** inputs is a complete set of view inputs: the dry run and the row above. */
function inputs(overrides: Partial<SettingsViewInputs> = {}): SettingsViewInputs {
  return { dryRun: DRY, row: ROW, argv: ARGV, because: null, ...overrides }
}

/** viewOf builds the view and asserts it is one: every case below is about a
 * window that exists, except the three that assert a refusal. */
function viewOf(overrides: Partial<SettingsViewInputs> = {}): SettingsView {
  const read = settingsView(inputs(overrides))
  expect(read.ok, read.ok ? '' : read.because).toBe(true)
  if (!read.ok) throw new Error(read.because)
  return read.view
}

/** dryWith is the document above with **one** member replaced. */
function dryWith(member: string, value: unknown): unknown {
  return { ...DRY, [member]: value }
}

describe('the settings edit view', () => {
  it('carries the six fields, the shape and the argv, and nothing else', () => {
    const view = viewOf()
    expect(view.key).toBe('budget.per_run_usd')
    expect(view.oldValue).toBe('8')
    expect(view.newValue).toBe('12')
    expect(view.layer).toBe('project')
    expect(view.target).toBe('')
    expect(view.wins).toBe('project')
    expect(view.sentence).toBe('')
    expect(view.widening).toBe(false)
    expect(view.why).toBe('')
    expect(view.argv).toEqual(ARGV)
    expect(Object.keys(view).sort()).toEqual([...VIEW_MEMBERS].sort())
  })

  it('shows the dry run’s old value, and the read’s value when that layer holds none', () => {
    // D1108: the old value is the dry run's `old`, and the read's `value` when
    // the key is unset in the layer being written -- the dry run has no old
    // value to print there, and an empty cell would read as "the old value is
    // the empty string", which is a different fact from "nothing is set here".
    expect(viewOf().oldValue).toBe('8')
    expect(viewOf({ dryRun: dryWith('old', ''), row: { ...ROW, value: '8' } }).oldValue).toBe('8')
    expect(viewOf({ dryRun: dryWith('old', '9'), row: { ...ROW, value: '8' } }).oldValue).toBe('9')
    // A row the read carried no value for, and a dry run with no old value: the
    // window says nothing rather than inventing one.
    expect(viewOf({ dryRun: dryWith('old', ''), row: { ...ROW, value: null } }).oldValue).toBe('')
  })

  it('draws the sentence exactly when the layer written is not the layer that wins', () => {
    // D1113: the edit is written and no run reads differently. It is a
    // comparison of two fields the CLI printed -- the layer the write lands in
    // and the layer that decides the key today -- and it is present in one
    // direction and **absent** in the other, so a view that always drew it, or
    // never, fails one half of this case.
    expect(viewOf().sentence).toBe('')
    const shadowed = viewOf({ row: { ...ROW, wins: 'local', targetLayer: 'project' } })
    expect(shadowed.sentence).toBe(SETTINGS_SHADOWED_SENTENCE)
    // The sentence is not a refusal: the edit proceeds and the argv is drawn.
    expect(shadowed.argv).toEqual(ARGV)
  })

  it('names the file a `repo`-layer edit writes, read out of the argv it is about to spawn', () => {
    // RC80 (L12): a `--layer repo` argv says *a checkout's `fugaro.yaml`* and
    // not *which checkout*, and the window the operator confirmed showed no name
    // for the file -- so the window draws one, read out of the argv's own
    // `--repo` word rather than from a second value that could disagree with the
    // write (D1105).
    const repo = { argv: REPO_ARGV, row: { ...ROW, targetLayer: 'repo' } }
    expect(viewOf(repo).target).toBe('writes fugaro.yaml in /Users/owner/src/checkout')
    // Every other layer names no repository, so the line is absent -- and the
    // word is absent from the argv too, which is why one place decides both.
    expect(viewOf().target).toBe('')
    expect(ARGV).not.toContain('--repo')
    // The scan stops at the terminator: everything behind `--` is the setting's
    // own value, so a value spelled `--repo` is a value and not this flag.
    expect(
      viewOf({ argv: ['fugaro', 'settings', 'set', 'k', '--layer', 'repo', '--', '--repo', '/tmp/elsewhere'] }).target,
    ).toBe('')
    // A `repo`-layer plan whose argv carries no `--repo` at all -- which
    // `argvFor` refuses before an argv exists -- invents no name here either.
    expect(
      viewOf({ argv: ['fugaro', 'settings', 'set', 'k', '--layer', 'repo'], row: { ...ROW, targetLayer: 'repo' } })
        .target,
    ).toBe('')
  })

  it('shapes a widening as two answers: Cancel is the default and Confirm waits for the tick', () => {
    // D1109: the shape is the dry run's own `widening` member and never the
    // console's reading of the value, so a rule change in S5 changes the dialog
    // with no console edit.
    const view = viewOf({
      dryRun: { ...DRY, widening: true, why: 'this raises the per-run limit' },
    })
    expect(view.widening).toBe(true)
    expect(view.why).toBe('this raises the per-run limit')
    expect(view.defaultButton).toBe('cancel')
    expect(view.tick).toBe(SETTINGS_TICK_LABEL)
    expect(view.confirmNeedsTick).toBe(true)
    expect(settingsConfirmAnswerable(view, false)).toBe(false)
    expect(settingsConfirmAnswerable(view, true)).toBe(true)
  })

  it('shapes a narrowing as one click: Confirm is the default and there is no tick', () => {
    const view = viewOf()
    expect(view.widening).toBe(false)
    expect(view.why).toBe('')
    expect(view.defaultButton).toBe('confirm')
    expect(view.tick).toBe('')
    expect(view.confirmNeedsTick).toBe(false)
    expect(settingsConfirmAnswerable(view, false)).toBe(true)
  })

  it('shows the argv the builder produced, field by field', () => {
    const view = viewOf()
    expect(view.argv).toEqual(ARGV)
    expect(view.argv).not.toBe(ARGV)
    expect(Object.isFrozen(view.argv)).toBe(true)
  })

  it('carries no member the read’s row could smuggle a sensitive value through', () => {
    // D1111: a sensitive row is never drawn, so it never reaches a window --
    // and the window's own member list is the second half of that, because a
    // view that spread the row into itself would carry whatever the row
    // carried, including a member the drawing dropped the row for.
    const row = {
      ...ROW,
      scope: 'project',
      state: 'ok',
      sensitive: true,
    } as unknown as SettingsViewRow
    const view = viewOf({ row })
    expect(Object.keys(view).sort()).toEqual([...VIEW_MEMBERS].sort())
    expect(Object.keys(view)).not.toContain('sensitive')
    expect(JSON.stringify(view)).not.toContain('sensitive')
  })

  it('refuses a document of another version', () => {
    const read = settingsView(inputs({ dryRun: { ...DRY, version: 2 } }))
    expect(read.ok).toBe(false)
    if (read.ok) throw new Error('a document of version 2 was read')
    expect(read.because).toContain(`version 2 and this console reads version ${SETTINGS_VIEW_DOC_VERSION}`)
  })

  it('refuses a member of a type the contract does not give it rather than coercing it', () => {
    // The owner's rule of 2026-10-30: unknown keys are ignored and types are
    // strict. A `widening` spelled `"true"` would shape a widening as a
    // one-click edit, an `old` that is a number would draw a value the CLI
    // never printed, and a `changed` that is `"false"` would be read as true --
    // each a claim the CLI never made, so the window refuses the document
    // rather than opening on a coerced one.
    const wrong: readonly (readonly [string, unknown])[] = [
      ['key', 7],
      ['layer', 3],
      ['path', null],
      ['changed', 'false'],
      ['widening', 'true'],
      ['why', 1],
      ['old', 8],
      ['new', {}],
      ['refusal', []],
    ]
    for (const [member, value] of wrong) {
      const read = settingsView(inputs({ dryRun: dryWith(member, value) }))
      expect(read.ok, `${member}: ${JSON.stringify(value)} opened a window`).toBe(false)
      if (read.ok) continue
      expect(read.because).toBe(SETTINGS_VIEW_SHAPE_BECAUSE)
    }
  })

  it('refuses rather than opening on a document that never arrived', () => {
    const read = settingsView(inputs({ dryRun: null, because: 'the CLI refused the dry run' }))
    expect(read.ok).toBe(false)
    if (read.ok) throw new Error('a window opened on no document')
    expect(read.because).toBe('the CLI refused the dry run')
  })
})

/** ENTRY_ROW is the read's own row for one key, with the members the entry form
 * needs beside C2's four: the write's own answer (`writable`), the command the
 * read names for a key this path does not write (`owner`), the eight metadata
 * members SM-E emits, and the built-in default R6 draws. */
const ENTRY_ROW: SettingsEntryRow = {
  key: 'budget.per_run_usd',
  value: '8',
  wins: 'project',
  targetLayer: 'project',
  writable: true,
  owner: 'settings set',
  meta: {
    label: 'Per-run cap',
    description: 'What one run may spend.',
    group: 'Budget',
    type: 'int',
    enum: [],
    min: '0',
    max: '25',
    format: '',
  },
  default: { value: '5', unset: '' },
}

/** ENTRY_MEMBERS is the whole of the entry view, and the list is exact: a
 * member added to it is a member the window draws, and one the test below has
 * to be told about. */
const ENTRY_MEMBERS: readonly string[] = Object.freeze([
  'kind',
  'project',
  'key',
  'label',
  'description',
  'current',
  'defaultValue',
  'defaultUnset',
  'caveat',
  'control',
  'options',
  'min',
  'max',
  'format',
  'type',
  'command',
])

/**
 * The entry form (SU1, D2902): the first half of the one modal, where the
 * operator types a value before any dry run exists. What is asserted here is
 * the pair that decides whether OK is drawn enabled -- the control the key's
 * metadata calls for, and `settingsEntryError`'s answer for what has been typed
 * -- because the window's own rule is "OK is disabled while that answer is a
 * sentence", and a form that drew the control but validated nothing would write
 * whatever was typed.
 *
 * The client is a **pre-check and not the authority** (R5): the dry run is what
 * decides at OK, so each shape here is the permissive one the CLI's own reader
 * accepts rather than a second copy of its rules.
 */
describe('the settings entry form', () => {
  it('draws the label, the description and the raw key, and starts at the value a run reads now', () => {
    const view = settingsEntryView({ project: 'sandbox', row: ENTRY_ROW })
    expect(view.kind).toBe('settings-entry')
    expect(view.project).toBe('sandbox')
    expect(view.key).toBe('budget.per_run_usd')
    expect(view.label).toBe('Per-run cap')
    expect(view.description).toBe('What one run may spend.')
    expect(view.current).toBe('8')
  })

  it('shows the built-in default and says a lower layer may take over (R6)', () => {
    const view = settingsEntryView({ project: 'sandbox', row: ENTRY_ROW })
    expect(view.defaultValue).toBe('5')
    expect(view.defaultUnset).toBe('')
    expect(view.caveat).toBe(SETTINGS_ENTRY_CAVEAT)

    const sentence = settingsEntryView({
      project: 'sandbox',
      row: { ...ENTRY_ROW, default: { value: '', unset: 'unset = the built-in list price' } },
    })
    expect(sentence.defaultValue).toBe('')
    expect(sentence.defaultUnset).toBe('unset = the built-in list price')
  })

  it('falls back to the raw key and a text control for a key with no metadata', () => {
    const bare = settingsEntryView({
      project: 'sandbox',
      row: {
        ...ENTRY_ROW,
        key: 'some.future_key',
        meta: { label: '', description: '', group: '', type: '', enum: [], min: '', max: '', format: '' },
      },
    })
    expect(bare.label).toBe('some.future_key')
    expect(bare.control).toBe('text')
    expect(bare.options).toEqual([])
    expect(bare.min).toBe('')
    expect(bare.max).toBe('')
    expect(bare.format).toBe('')
  })

  it('draws the control the key’s type calls for, and a text control for a type it does not know', () => {
    const of = (meta: Partial<SettingsEntryMeta>): SettingsEntryControl | null => {
      const view = settingsEntryView({ project: 'sandbox', row: { ...ENTRY_ROW, meta: { ...ENTRY_ROW.meta, ...meta } } })
      return view.control
    }
    expect(of({ type: 'bool' })).toBe('toggle')
    expect(of({ type: 'enum', enum: ['failover', 'spread'] })).toBe('select')
    expect(of({ type: 'enum' })).toBe('text')
    expect(of({ type: 'int' })).toBe('number')
    expect(of({ type: 'float' })).toBe('number')
    expect(of({ type: 'string' })).toBe('text')
    expect(of({ type: 'duration', format: 'duration' })).toBe('text')
    // A type a newer CLI grew: the window draws the fallback rather than
    // refusing the read whole, which is SM-E's own rule for an absent member.
    expect(of({ type: 'ip' })).toBe('text')

    const options = settingsEntryView({
      project: 'sandbox',
      row: { ...ENTRY_ROW, meta: { ...ENTRY_ROW.meta, type: 'enum', enum: ['failover', 'spread'] } },
    })
    expect(options.options).toEqual(['failover', 'spread'])
  })

  it('leaves OK disabled for a number outside min..max', () => {
    const view = settingsEntryView({ project: 'sandbox', row: ENTRY_ROW })
    expect(view.control).toBe('number')
    expect(view.min).toBe('0')
    expect(view.max).toBe('25')
    expect(settingsEntryError(view, '26')).toBe(`${SETTINGS_ENTRY_ABOVE_MAX}${view.max})`)
    expect(settingsEntryError(view, '-1')).toBe(`${SETTINGS_ENTRY_BELOW_MIN}${view.min})`)
    expect(settingsEntryError(view, '25')).toBeNull()
    expect(settingsEntryError(view, '0')).toBeNull()
    expect(settingsEntryError(view, '0.5')).toBe(SETTINGS_ENTRY_WHOLE)
    expect(settingsEntryError(view, '2 x')).toBe(SETTINGS_ENTRY_WHOLE)
    expect(settingsEntryError(view, '')).toBe(SETTINGS_ENTRY_WHOLE)
  })

  it('bounds a duration in the value’s own spelling', () => {
    const view = settingsEntryView({
      project: 'sandbox',
      row: {
        ...ENTRY_ROW,
        meta: { ...ENTRY_ROW.meta, type: 'duration', format: 'duration', min: '90m', max: '14d' },
      },
    })
    expect(view.control).toBe('text')
    expect(settingsEntryError(view, '90m')).toBeNull()
    expect(settingsEntryError(view, '1h30m')).toBeNull()
    expect(settingsEntryError(view, '14d')).toBeNull()
    expect(settingsEntryError(view, '15d')).toBe(`${SETTINGS_ENTRY_ABOVE_MAX}${view.max})`)
    expect(settingsEntryError(view, '5m')).toBe(`${SETTINGS_ENTRY_BELOW_MIN}${view.min})`)
    expect(settingsEntryError(view, 'soon')).toBe(SETTINGS_ENTRY_DURATION)
  })

  it('refuses a value the metadata’s format does not accept', () => {
    const formats: readonly (readonly [string, string, string])[] = [
      ['repo', 'acme/widgets', 'widgets'],
      ['email', 'someone@example.com', 'someone@'],
      ['url', 'https://example.com/acme/widgets.git', 'http://example.com/x'],
      ['memory', '16Gi', '16GB'],
      ['branch', 'feature/one', '-feature'],
    ]
    for (const [format, good, bad] of formats) {
      const view = settingsEntryView({
        project: 'sandbox',
        row: { ...ENTRY_ROW, meta: { ...ENTRY_ROW.meta, type: 'string', format } },
      })
      expect(view.control).toBe('text')
      expect(settingsEntryError(view, good), `${format}: ${good}`).toBeNull()
      expect(settingsEntryError(view, bad), `${format}: ${bad}`).not.toBeNull()
    }
    const free = settingsEntryView({
      project: 'sandbox',
      row: { ...ENTRY_ROW, meta: { ...ENTRY_ROW.meta, type: 'string', min: '', max: '', format: '' } },
    })
    expect(settingsEntryError(free, 'anything at all')).toBeNull()
  })

  it('refuses a value outside a closed set, and accepts only the members the CLI lists', () => {
    const view = settingsEntryView({
      project: 'sandbox',
      row: { ...ENTRY_ROW, meta: { ...ENTRY_ROW.meta, type: 'enum', enum: ['narrow', 'wide'] } },
    })
    expect(view.control).toBe('select')
    expect(settingsEntryError(view, 'wide')).toBeNull()
    expect(settingsEntryError(view, 'wider')).toBe(SETTINGS_ENTRY_SELECT)
  })

  it('takes a toggle as on or off and nothing else', () => {
    const view = settingsEntryView({
      project: 'sandbox',
      row: { ...ENTRY_ROW, value: 'true', meta: { ...ENTRY_ROW.meta, type: 'bool' } },
    })
    expect(view.control).toBe('toggle')
    expect(settingsEntryError(view, 'true')).toBeNull()
    expect(settingsEntryError(view, 'false')).toBeNull()
    expect(settingsEntryError(view, 'yes')).toBe(SETTINGS_ENTRY_TOGGLE)
  })

  it('takes a reset whatever the control would say, because a reset writes no value at all', () => {
    const view = settingsEntryView({ project: 'sandbox', row: ENTRY_ROW })
    expect(settingsEntryError(view, null)).toBeNull()
  })

  it('shows a key this console does not write read-only, with the command that does', () => {
    const held = settingsEntryView({
      project: 'sandbox',
      row: { ...ENTRY_ROW, writable: false, owner: 'fugaro budget set --per-run 25' },
    })
    expect(held.control).toBeNull()
    expect(held.command).toBe('fugaro budget set --per-run 25')
    expect(settingsEntryError(held, '12')).toBe(SETTINGS_ENTRY_READONLY)
    expect(settingsEntryError(held, null)).toBeNull()

    // The same shape for a row the write cannot target at all: a writable key
    // with no layer to write is a key no edit of this window reaches.
    const nowhere = settingsEntryView({ project: 'sandbox', row: { ...ENTRY_ROW, targetLayer: '' } })
    expect(nowhere.control).toBeNull()
  })

  it('carries exactly the members the window draws, and nothing of the read’s own row', () => {
    const view = settingsEntryView({ project: 'sandbox', row: ENTRY_ROW })
    expect(Object.keys(view).sort()).toEqual([...ENTRY_MEMBERS].sort())
    expect(Object.keys(view)).not.toContain('writable')
    expect(Object.keys(view)).not.toContain('owner')
    expect(Object.keys(view)).not.toContain('targetLayer')
  })
})
