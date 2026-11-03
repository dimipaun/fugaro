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
//     the argv the builder produced".

import { describe, expect, it } from 'vitest'

import {
  SETTINGS_SHADOWED_SENTENCE,
  SETTINGS_TICK_LABEL,
  SETTINGS_VIEW_DOC_VERSION,
  SETTINGS_VIEW_SHAPE_BECAUSE,
  settingsConfirmAnswerable,
  settingsView,
} from '../../src/main/settingsview'
import type { SettingsView, SettingsViewInputs, SettingsViewRow } from '../../src/main/settingsview'

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
  '--yes',
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
  'wins',
  'sentence',
  'widening',
  'why',
  'argv',
  'defaultButton',
  'tick',
  'confirmNeedsTick',
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
