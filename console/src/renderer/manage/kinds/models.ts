// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Models kind: the models a launch of this project may name, one Manage row
// each (the console-improvements plan's task MO-K; spec section 5's Models & keys
// group as D2903 amended it; the descriptor is `manage/types.ts` and the prose
// `docs/console-improvements/manage-kinds.md`).
//
// **The document is the console's own read, decoded by the console's own
// reader.** `source` is `models.list` (`models ls --json`, contract section
// 6.18) and `decode` hands the answer to `decodeModelsList` -- the same door the
// Projects tab's models panel goes through (`entry.ts`'s `loadModels`) -- rather
// than reading the keys here a second time. A document this build does not read
// (an unknown `version`, a member of the wrong shape) is that reader's refusal,
// and a section with no rows of its own to draw is this kind's answer to it:
// nothing. The section's own sentence about a read that did not arrive belongs to
// the shell, which is the only part that knows a read was made at all.
//
// **The columns are the CLI's own table.** `models ls`' text form prints MODEL,
// ROUTE, SOURCE, INPUT, OUTPUT and NOTES (`models_ls.go`'s `writeModelsLs`), and
// the six here are those: a table an operator can hold against the command they
// would have run is the whole of what a thin front end owes.
//
// **No write, and this is the models kind's own omission and not an oversight.**
// `models add` is a widening the plan admits as a *write* of its own (MW1), and
// `models use` and `models rm` are destructive writes D2903 keeps out of the
// console (`models use` rewrites a repository's reviewed `agent.models.<role>`
// in its working tree) -- so this kind offers no action at all: a `copy-command`
// for `models use` would have to name a repository and a role the model's own
// row does not carry, which is a command the operator repairs rather than runs,
// and the plan's writes arrive with the tasks that admit them.

import { formatReading, readNumber } from '../../../shared/availability'
import { decodeModelsList } from '../../../shared/schema'
import type { ModelsListModel } from '../../../shared/schema'
import { defineManageKind } from '../types'
import type { ManageColumn, ManageKind, ManageRow } from '../types'

/**
 * NO_ROUTE_WORD is what a model no route of this project serves draws in the
 * Route column. It is the **console's own word** and not the document's: the
 * contract leaves `route` empty for such a row (a launch refuses it), and an
 * empty cell reads as a figure nobody drew where this is a fact about the row.
 * The CLI's own text form prints the same two words (`models ls`'s `no route`),
 * so the table and the command agree.
 */
const NO_ROUTE_WORD = 'no route'

/**
 * MODELS_COLUMNS is the table's six columns, each with the class every cell of
 * it passes under in the shell. The four text columns are the classes the
 * console's wire table already gives these keys (`WIRE_TEXT_CLASSES`,
 * `src/shared/schema.ts`): an id and a route name are `label`, which is how the
 * Projects tab's models panel draws the same two values, and NOTES is a
 * `sentence` -- the document's own spread or its reason for holding no price.
 *
 * The two price columns carry **text this kind formatted**, and they are
 * `label` for that reason: a cell is a string, and the only door a document's
 * number comes through is `readNumber` joined to `formatReading`
 * (`src/shared/availability.ts`), whose answer for a price the document does not
 * carry is `n/a` -- never `$0.00`, which would draw an unknown model as a free
 * one.
 */
const MODELS_COLUMNS: readonly ManageColumn[] = [
  { key: 'model', title: 'Model', class: 'label' },
  { key: 'route', title: 'Route', class: 'label' },
  { key: 'source', title: 'Source', class: 'label' },
  { key: 'input', title: 'Input $/M', class: 'label' },
  { key: 'output', title: 'Output $/M', class: 'label' },
  { key: 'notes', title: 'Notes', class: 'sentence' },
]

/** modelsRow is one model of the document as one table row: the id and route
 * the CLI read, where its price comes from, the two prices, and the note the row
 * carries -- which is the document's `refused` when it has one, exactly as the
 * text form prefers it, because "why this row has no price" is what the reader
 * of the row needs and the spread is what the reader of a priced row needs. */
function modelsRow(model: ModelsListModel): ManageRow {
  return {
    key: model.id,
    cells: {
      model: model.id,
      route: model.route === '' ? NO_ROUTE_WORD : model.route,
      source: model.source,
      input: formatReading(readNumber(model.inputPerM, 'models[].input_per_m'), 'usd'),
      output: formatReading(readNumber(model.outputPerM, 'models[].output_per_m'), 'usd'),
      notes: model.refused === '' ? model.note : model.refused,
    },
  }
}

/** MODELS_KIND is the Models & keys group's first kind. */
export const MODELS_KIND: ManageKind = defineManageKind({
  id: 'models',
  group: 'Models & keys',
  title: 'Models',
  source: 'models.list',
  columns: MODELS_COLUMNS,
  decode: (doc) => {
    const decoded = decodeModelsList(doc)
    return decoded.ok ? decoded.value.models.map(modelsRow) : []
  },
  actions: [],
})
