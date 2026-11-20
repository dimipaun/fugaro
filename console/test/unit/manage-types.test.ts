// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Manage kind descriptor and the Copy-command builder (the
// console-improvements plan's task MG0): the four groups and their order, a kind
// whose group is not one of them, and every shape of word the builder refuses.
//
// **What this file is for.** The descriptor is what the shell and the five kind
// tasks compile against, so the parts worth pinning are the two it *refuses*:
// a kind in a group the shell has no place for, and a value that a shell would
// read as syntax. Both are refused rather than repaired (nothing is quoted,
// dropped or escaped), and neither reason may draw the value it refused.

import { describe, expect, it } from 'vitest'

import { buildCopyCommand, commandValue, commandWord, copyCommandText } from '../../src/renderer/manage/command'
import type { CommandPart, CopyCommandAnswer } from '../../src/renderer/manage/command'
import { MANAGE_GROUPS, defineManageKind } from '../../src/renderer/manage/types'
import type { CopyCommandAction, ManageGroup, ManageKind } from '../../src/renderer/manage/types'

/** action is the command a build admitted, or a failure naming the refusal -- so
 * a case that expected a command and got a refusal says so, rather than reading
 * `argv` off a union. */
function action(parts: readonly CommandPart[]): CopyCommandAction {
  const answer: CopyCommandAnswer = buildCopyCommand(parts)
  if (!answer.ok) throw new Error(`refused: ${answer.reason}`)
  return answer.action
}

/** reason is the builder's refusal sentence, or a failure when it admitted the
 * command. */
function reason(parts: readonly CommandPart[]): string {
  const answer: CopyCommandAnswer = buildCopyCommand(parts)
  if (answer.ok) throw new Error('the builder admitted a command it should have refused')
  return answer.reason
}

/** KIND is one kind of the shape a kind file writes: the members list, whose
 * columns are the two the People & access tasks fill and whose read is the
 * operation `MG0` names as `source`. Its `decode` is never called here -- this
 * file pins the descriptor, and the kind that decodes a document is its own
 * task's. */
const KIND: ManageKind = {
  id: 'members',
  group: 'People & access',
  title: 'Members',
  source: 'members.list',
  columns: [
    { key: 'member', title: 'Member', class: 'person' },
    { key: 'roles', title: 'Roles', class: 'label' },
  ],
  decode: () => [],
  actions: [],
}

describe('the groups a kind may sit in', () => {
  it('is the spec’s four, in the order the Project tab draws them', () => {
    expect(MANAGE_GROUPS).toEqual(['People & access', 'Code', 'Models & keys', 'Budget'])
  })

  it('admits a kind in every one of the four', () => {
    for (const group of MANAGE_GROUPS) {
      expect(defineManageKind({ ...KIND, group }).group).toBe(group)
    }
  })

  it('refuses a kind whose group the shell has no place for, naming it and the four', () => {
    expect(() => defineManageKind({ ...KIND, group: 'Nowhere' as ManageGroup })).toThrow(/Nowhere/)
    expect(() => defineManageKind({ ...KIND, group: '' as ManageGroup })).toThrow(/People & access/)
  })
})

describe('the Copy-command builder', () => {
  it('admits a value in every shape a Manage row copies', () => {
    const values = [
      'ana@example.invalid',
      'dimipaun/fugaro',
      'roles/datastore.viewer',
      'openrouter-main',
      '2026-11-18T10:00:00Z',
      'a..b',
      '1.2.3',
    ]
    for (const value of values) {
      expect(action([commandWord('fugaro'), commandValue(value)]).argv).toEqual(['fugaro', value])
    }
  })

  it('admits the console’s own flags as fixed words, and one value as a word of the argv', () => {
    const built = action([
      commandWord('fugaro'),
      commandWord('user'),
      commandWord('grant'),
      commandWord('--project=fugaro-dev'),
      commandValue('ana@example.invalid'),
      commandValue('roles/datastore.viewer'),
    ])
    expect(built.kind).toBe('copy-command')
    expect(built.argv).toEqual([
      'fugaro',
      'user',
      'grant',
      '--project=fugaro-dev',
      'ana@example.invalid',
      'roles/datastore.viewer',
    ])
  })

  it('writes the clipboard text as the argv joined with single spaces', () => {
    const built = action([
      commandWord('fugaro'),
      commandWord('user'),
      commandWord('grant'),
      commandWord('--project=fugaro-dev'),
      commandValue('ana@example.invalid'),
      commandValue('roles/datastore.viewer'),
    ])
    expect(copyCommandText(built)).toBe(
      'fugaro user grant --project=fugaro-dev ana@example.invalid roles/datastore.viewer',
    )
  })

  it('refuses a value a shell would read as syntax, and never the words around it', () => {
    const unsafe = [
      ['a semicolon', 'ana;whoami'],
      ['a command substitution', '$(whoami)'],
      ['a backtick', 'ana`whoami`'],
      ['a newline', 'ana\nbob'],
      ['a space', 'ana bob'],
      ['a leading dash', '-p'],
      ['a leading equals', '=x'],
      ['a leading at', '@x'],
      ['a leading tilde', '~x'],
      ['a parent segment', '../etc'],
      ['a parent segment inside the word', 'owner/../etc'],
      ['an empty value', ''],
    ] as const
    for (const [what, value] of unsafe) {
      const parts = [commandWord('fugaro'), commandWord('accounts'), commandWord('rm'), commandValue(value)]
      const refusal = reason(parts)
      expect(refusal, `${what} was refused`).not.toBe('')
      // The reason is drawn beside the row, so it may not carry the value it
      // refused -- a value the console will not put in a shell is one it will
      // not put in a sentence either.
      if (value !== '') expect(refusal, `${what} was not drawn in the reason`).not.toContain(value)
    }
  })

  it('refuses a fixed word built out of a document’s string', () => {
    expect(reason([commandWord('fugaro'), commandWord('repos add ../etc')])).not.toBe('')
    expect(reason([commandWord('fugaro'), commandWord('user;grant')])).not.toBe('')
    expect(reason([commandWord('fugaro'), commandWord('--repo=owner name')])).not.toBe('')
  })

  it('refuses an empty command', () => {
    expect(reason([])).not.toBe('')
  })
})
