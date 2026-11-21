// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Code group's recipes (the console-improvements plan's task MO-C; the
// descriptor is `../types.ts` and the prose is
// `docs/console-improvements/manage-kinds.md`).
//
// **A recipe's row is a claim about what a run will do**, and that is why the
// two members a person cannot read past are drawn as their own columns: a recipe
// that failed to parse is not launchable, and a recipe a higher layer shadows is
// not the one a launch picks. Both are the CLI's own words (`launchable` and
// `shadowed_by`, `internal/cli/recipes.go`), and the console draws them rather
// than deriving them, because a console that decided a recipe was fine from its
// shape would offer a launch the run then refuses.
//
// The document is C-OPS's decoded `RecipesView`, for `images.ts`'s reason: the
// wire document's `use_when`, `shadowed_by` and `max_rounds` are read once, by
// `new-ops.ts`, and this kind re-reads the view it answered. The classes are
// C-OPS's own (`NEW_OPERATION_TEXT_CLASSES`), which `manage-code.test.ts` holds.

import { boolAt, objectsAt, textAt, valuesAt } from './read'
import { defineManageKind } from '../types'
import type { JsonObject } from './read'
import type { ManageColumn, ManageKind, ManageRow } from '../types'

/** RECIPE_COLUMNS is the recipes' table: the name, which layer it came from,
 * whether a launch may pick it, what it does, when a routing skill should reach
 * for it, and the two members that explain a recipe nothing can launch. */
export const RECIPE_COLUMNS: readonly ManageColumn[] = Object.freeze([
  { key: 'name', title: 'Recipe', class: 'label' },
  { key: 'layer', title: 'Layer', class: 'label' },
  { key: 'usable', title: 'Usable', class: 'label' },
  { key: 'steps', title: 'Steps', class: 'label' },
  { key: 'useWhen', title: 'Use when', class: 'sentence' },
  { key: 'description', title: 'Description', class: 'sentence' },
  { key: 'shadowedBy', title: 'Shadowed by', class: 'label' },
  { key: 'problem', title: 'Problem', class: 'sentence' },
])

/** roundsAt is a step's `maxRounds`, or 0 for one the document did not state.
 * The view's member is `number | null`, and 0 is the natural absent: the CLI's
 * own rendering prints `(max_rounds: N)` only when N is above zero
 * (`stepsSummary`), so a zero and an absent member draw the same step. */
function roundsAt(step: JsonObject): number {
  const raw = step['maxRounds']
  return typeof raw === 'number' && Number.isInteger(raw) && raw > 0 ? raw : 0
}

/** stepsCell is a recipe's steps as one cell, in the CLI's own rendering
 * (`stepsSummary`, `internal/cli/recipes.go`): a check names the command it runs
 * and its autofix, a review or adversarial step its bounce and its rounds, and
 * every other kind its rounds alone. An empty list is the console's own word for
 * none, so a recipe with no steps is not a blank column. */
function stepsCell(steps: readonly unknown[]): string {
  const parts: string[] = []
  for (const raw of steps) {
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) continue
    const step = raw as JsonObject
    const kind = textAt(step, 'kind')
    if (kind === '') continue
    const rounds = roundsAt(step)
    if (kind === 'check') {
      parts.push(`check: ${textAt(step, 'command')}${boolAt(step, 'autofix') ? ', autofix' : ''}`)
      continue
    }
    let part = kind
    if ((kind === 'review' || kind === 'adversarial') && boolAt(step, 'bounce')) {
      part += ' (bounce: first_line)'
    }
    if (rounds > 0) part += ` (max_rounds: ${String(rounds)})`
    parts.push(part)
  }
  return parts.length === 0 ? 'none' : parts.join(', ')
}

/** recipesKind decodes `recipes ls --json` into one row per recipe. It has no
 * actions: a recipe is a file in one of three layers, and the commands that
 * write or publish one are `recipes new`, `recipes publish` and `recipes
 * validate`, none of which D2903's first pass admits to the console. */
export const recipesKind: ManageKind = defineManageKind({
  id: 'recipes',
  group: 'Code',
  title: 'Recipes',
  source: 'recipes.ls',
  columns: RECIPE_COLUMNS,
  actions: [],
  decode(doc: unknown): readonly ManageRow[] {
    if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) return []
    const document = doc as JsonObject
    const rows: ManageRow[] = []
    for (const recipe of objectsAt(document, 'recipes')) {
      const name = textAt(recipe, 'name')
      const layer = textAt(recipe, 'source')
      if (name === '') continue
      rows.push({
        // The CLI lists one name per layer, and a lower layer's row of a name a
        // higher layer shadows is a row of its own: the key is the pair, or two
        // layers' `quick` would be one row drawn twice.
        key: `${layer}/${name}`,
        cells: {
          name,
          layer,
          usable: boolAt(recipe, 'launchable') ? 'yes' : 'no',
          steps: stepsCell(valuesAt(recipe, 'steps')),
          useWhen: textAt(recipe, 'useWhen'),
          description: textAt(recipe, 'description'),
          shadowedBy: textAt(recipe, 'shadowedBy'),
          problem: textAt(recipe, 'problem'),
        },
      })
    }
    return rows
  },
})
