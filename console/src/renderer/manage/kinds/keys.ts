// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The API keys kind: the secrets this project has stored, one Manage row each
// (the console-improvements plan's task MO-K; spec section 5's Models & keys
// group as D2903 and D2905 amended it; the descriptor is `manage/types.ts` and
// the prose `docs/console-improvements/manage-kinds.md`).
//
// **Names and status, and never a value.** `secrets ls --json` (contract
// section 6.6) is the inventory: a **bare JSON array** of entries carrying a
// logical name, the scope its value is resolved from, the labels and version
// counts of the Secret Manager container, and -- on a repository's read -- the
// `missing` flag for a name the repository's jobs mount and nothing resolves.
// The command "has no access call at all", so no value is in the document to
// begin with; this kind's own part of the rule is that it reads none of the
// members that could carry one, and that it draws no control a person could type
// a value into (design rule 2, D2905). A key is added, rotated and withdrawn at
// the operator's own terminal, which is what the two Copy-commands below are.
//
// **The document is a bare array, and this kind reads it as one.** The
// console's live reader treats a line that is not a JSON object as a notice
// (`decodeFeedLine`, contract section 10.13 rule 1), which is why the Projects
// tab's secrets panel draws its unavailable state through the live source today
// -- so the entries below are read from the parsed array for the reason that
// panel gives (`screens/projects.ts`'s header), and a document that is not an
// array is a read this kind has no rows for rather than a crash.
//
// **Two commands, and they are the console's own words.** D2903 keeps every
// write that carries a secret out of the console and gives the operator the
// exact command instead: `fugaro secrets set NAME` stores a value the CLI reads
// from the operator's own terminal -- a hidden prompt, or stdin -- and running
// it again under the same name stores a new version, which is the rotation; and
// `fugaro secrets rm NAME` withdraws a container and its config entry. Both are
// built by `command.ts`'s `buildCopyCommand` from words this file writes, so
// neither can carry a document's string into a shell, and the placeholder both
// spell is the CLI's own `NAME` metavariable (`secrets set NAME`,
// `secrets rm NAME`) -- a descriptor's actions belong to the kind rather than to
// a row (MG0), so the row beside the Copy is what names the key.

import { UNAVAILABLE_MARK } from '../../../shared/availability'
import { buildCopyCommand, commandWord } from '../command'
import { defineManageKind } from '../types'
import type { ManageAction, ManageColumn, ManageKind, ManageRow } from '../types'

/**
 * MISSING_WORD and STORED_WORD are the console's two words for a row's status,
 * and the document's own `missing` flag decides between them: it is written,
 * `true` only, for a name a repository's jobs mount that no container of the
 * repository or the project resolves (`secrets ls`'s MISSING column, the CLI's
 * "yes"). Every other row is a container the inventory found, so `stored` is a
 * fact about the row rather than a claim about a value -- which is the one thing
 * this kind may not make (contract section 6.6: the document carries no value,
 * and the two words here are drawn from the flag alone).
 */
const MISSING_WORD = 'missing'
const STORED_WORD = 'stored'

/**
 * KEYS_COLUMNS is the table's three columns. The key's name and its scope are
 * the two text members the CLI's own table prints (NAME and SCOPE), both
 * `label`, which is the class the Projects tab's secrets panel draws the same
 * two values under; the status is the console's word for the document's flag.
 */
const KEYS_COLUMNS: readonly ManageColumn[] = [
  { key: 'name', title: 'Key', class: 'label' },
  { key: 'scope', title: 'Scope', class: 'label' },
  { key: 'status', title: 'Status', class: 'label' },
]

/**
 * kindCommand builds one of this kind's own copy-commands from the words the
 * file writes. The builder answers a refusal as an ordinary outcome, because a
 * *value* from a document may be one the console will not copy -- and these two
 * commands carry no value at all: every word here is this file's, and the one
 * placeholder is the CLI's own metavariable. So a refusal is a bug in this file
 * rather than a row drawn without its Copy, and it is thrown at the moment the
 * kind is defined -- the same answer `defineManageKind` gives a group the shell
 * has no place for -- instead of leaving a section with a missing action and no
 * sentence saying why.
 */
function kindCommand(words: readonly string[]): ManageAction {
  const answer = buildCopyCommand(words.map((word) => commandWord(word)))
  if (!answer.ok) throw new Error(`the keys kind's own command ${words.join(' ')}: ${answer.reason}`)
  return answer.action
}

/** KEY_ACTIONS is the kind's whole action list: store the value (again, to
 * rotate) and withdraw it. Both are `copy-command` and neither is a write the
 * console performs, which is D2903's line for a secret. */
const KEY_ACTIONS: readonly ManageAction[] = [
  kindCommand(['fugaro', 'secrets', 'set', 'NAME']),
  kindCommand(['fugaro', 'secrets', 'rm', 'NAME']),
]

/** jsonObject is a value that is a JSON object, or null. It is the reading the
 * Projects tab's builders give a document's member; it is spelled here rather
 * than imported because those are private to a screen and a kind draws no
 * screen's helper into itself. */
function jsonObject(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** jsonText is a value that is a JSON string, or null. */
function jsonText(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/** keysRow is one entry of the inventory as one table row. An entry whose name
 * is not a string is **skipped**: the name is the row's identity and the first
 * thing drawn on it, so an entry without one is not a row this kind can key --
 * and a row keyed by a guess would collide with the next. Nothing else of the
 * entry is read: `id`, `created`, `latest` and `labels` reach no cell, which is
 * the surest way to keep a value off a screen (D2905). */
function keysRow(entry: unknown): ManageRow | null {
  const object = jsonObject(entry)
  if (object === null) return null
  const name = jsonText(object['name'])
  if (name === null || name === '') return null
  return {
    key: name,
    cells: {
      name,
      // The scope is where the value is resolved from, and the contract leaves
      // it out of a row that has none: `n/a` is a value the document does not
      // carry, never an empty cell a reader would take for a blank.
      scope: jsonText(object['scope']) ?? UNAVAILABLE_MARK,
      status: object['missing'] === true ? MISSING_WORD : STORED_WORD,
    },
  }
}

/** KEYS_KIND is the Models & keys group's second kind. */
export const KEYS_KIND: ManageKind = defineManageKind({
  id: 'keys',
  group: 'Models & keys',
  title: 'API keys',
  source: 'secrets.inventory',
  columns: KEYS_COLUMNS,
  decode: (doc) => {
    if (!Array.isArray(doc)) return []
    const rows: ManageRow[] = []
    for (const entry of doc) {
      const row = keysRow(entry)
      if (row !== null) rows.push(row)
    }
    return rows
  },
  actions: KEY_ACTIONS,
})
