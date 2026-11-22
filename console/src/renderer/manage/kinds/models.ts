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
// **The Add action, and the floor it waits on.** `models add` is the widening the
// plan admits as a *write* of its own (MW1), and this kind is where the console
// offers it: a write action whose form collects the model, the route and the two
// rates, submits them to `models.add.plan`, and leaves the window that follows to
// main and to the confirmation document. **Below the floor the action is the
// Copy-command instead**, because a CLI whose `models add` has no `--no-apply` and
// no plan document cannot be driven this way at all: the console passes a word
// that CLI refuses -- or, worse, one it accepts and then runs the write's
// follow-up steps for. `modelsAddSupported` is the one answer to that question,
// and `src/main/modelsadd.ts` asks the same constant on its own side.
//
// The two words `models add`'s own flags are, and everything about the write
// beyond this file, belong to MW1. `models use` and `models rm` stay Copy-commands
// the console does not offer: `models use` rewrites a repository's reviewed
// `agent.models.<role>` in its working tree, and the command would have to name a
// repository and a role no model's row carries.

import { formatReading, readNumber } from '../../../shared/availability'
import { decodeModelsList } from '../../../shared/schema'
import type { ModelsListModel } from '../../../shared/schema'
import { MODELS_ADD_CLI_FLOOR, modelsAddSupported } from '../../../shared/version'
import { buildCopyCommand, commandWord } from '../command'
import { defineManageKind } from '../types'
import type {
  ManageAction,
  ManageColumn,
  ManageKind,
  ManageRow,
  WriteAction,
  WriteField,
} from '../types'

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

/** MODELS_ADD_LABEL is the models write's control, in the console's own words.
 * It says what the write does and not which command runs it. */
export const MODELS_ADD_LABEL = 'Add a model'

/** MODELS_ADD_FIELDS is the form's four boxes, keyed as `models.add.plan`'s
 * payload carries them and labelled as the CLI names the values. The two rates
 * are optional: a model the built-in catalog already prices needs neither, and
 * the CLI refuses a half-given pair with its own sentence -- which is the reason
 * this form does not pair them itself. */
export const MODELS_ADD_FIELDS: readonly WriteField[] = Object.freeze([
  { key: 'id', label: 'Model ID', required: true },
  { key: 'route', label: 'Route', required: true },
  { key: 'priceIn', label: 'Input $/M', required: false },
  { key: 'priceOut', label: 'Output $/M', required: false },
])

/** MODELS_ADD_BELOW_FLOOR is the sentence the Copy-command is drawn with when
 * this CLI cannot be shown the write. It is the console's own words and it says
 * two things the operator needs: why there is no button, and that the write's
 * own follow-up steps are saved rather than run.
 *
 * **It names no flag**, and `--no-apply` not least: a CLI below this floor has
 * no `--no-apply` either (the comment above the floor), so a sentence telling
 * the operator to type one would hand out a word their CLI refuses -- the same
 * hazard `NO_APPLY_CLI_FLOOR` exists to keep the console from handing a child
 * (`src/shared/version.ts`), and the shape AP8's test pins. What the sentence
 * names instead is the release that carries the word. */
export const MODELS_ADD_BELOW_FLOOR =
  'this console needs a newer fugaro to show this write; the release that carries it keeps the steps that publish the model saved for `fugaro apply` rather than running them'

/** MODELS_ADD_BELOW_FLOOR_ARGV is the command the below-floor Copy-command
 * carries. It is `fugaro models add` and **no values at all**: the model, the
 * route and the rates are the operator's to type, and a placeholder like
 * `<id>` is not a word `buildCopyCommand` admits -- nor one a shell would run. */
const MODELS_ADD_BELOW_FLOOR_ARGV = Object.freeze([
  commandWord('fugaro'),
  commandWord('models'),
  commandWord('add'),
])

/** MODELS_ADD_ACTION is the write the kind offers above the floor. */
const MODELS_ADD_ACTION: WriteAction = Object.freeze({
  kind: 'write',
  plan: 'models.add.plan',
  label: MODELS_ADD_LABEL,
  gate: 'manage-project',
  fields: MODELS_ADD_FIELDS,
})

/** modelsActionsFor is the kind's whole action list for one CLI: the write above
 * the floor, and the Copy-command below it. The copy is built through
 * `buildCopyCommand` like every other command this section copies, so a word it
 * refused leaves the kind offering nothing rather than a command nobody may
 * run. */
function modelsActionsFor(cliVersion: string | null, floor: string | null): readonly ManageAction[] {
  if (cliVersion !== null && modelsAddSupported(cliVersion, floor)) return [MODELS_ADD_ACTION]
  const copy = buildCopyCommand(MODELS_ADD_BELOW_FLOOR_ARGV, MODELS_ADD_BELOW_FLOOR)
  return copy.ok ? [copy.action] : []
}

/**
 * modelsKindFor is this kind for the installation the console is driving: the
 * one place the models write's floor is asked. `cliVersion` is the version line
 * the handshake read, or `null` when it read none -- and a console that could
 * not place the CLI is below every floor, which is the narrow reading the floor
 * exists for.
 *
 * `floor` is the constant by default and an argument for the reason
 * `modelsAddSupported`'s own is: the branch a release will take is testable
 * while the constant is still unset (`manage-writes.test.ts` drives both).
 */
export function modelsKindFor(
  cliVersion: string | null,
  floor: string | null = MODELS_ADD_CLI_FLOOR,
): ManageKind {
  return defineManageKind({
    id: 'models',
    group: 'Models & keys',
    title: 'Models',
    source: 'models.list',
    columns: MODELS_COLUMNS,
    decode: (doc) => {
      const decoded = decodeModelsList(doc)
      return decoded.ok ? decoded.value.models.map(modelsRow) : []
    },
    actions: modelsActionsFor(cliVersion, floor),
  })
}

/** MODELS_KIND is the kind for a console that read no version line at all: the
 * handshake answered nothing, so the console cannot place the CLI and every
 * floor refuses it. What such a kind offers is the Copy-command -- the state
 * today's constant gives every version too (`MODELS_ADD_CLI_FLOOR` is `null`). */
export const MODELS_KIND: ManageKind = modelsKindFor(null)
