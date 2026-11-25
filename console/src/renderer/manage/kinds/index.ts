// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Manage section's kinds: the eleven lists of the four groups, in the order
// the Project tab draws them (the console-improvements plan's task I-MG; the
// descriptor is `../types.ts` and its prose
// `docs/console-improvements/manage-kinds.md`).
//
// **This file is the list and nothing else.** Every kind is defined in the file
// of its own subject -- `people.ts`, `pools.ts`, `repos.ts`, `images.ts`,
// `recipes.ts`, `models.ts`, `keys.ts`, `budget.ts` -- and this file names them, which is the
// one place their order is written: the shell sorts the groups by
// `MANAGE_GROUPS` and draws each group's kinds in the order it was handed them,
// so the order below is the order a reader of the Project tab sees. A list that
// left a kind out would be that kind missing from the tab with no sentence
// saying why, which is what `manage.spec.ts` opens each group to catch.
//
// **Why the list is a function.** `KINDS` is the plan's own word for it, and the
// Budget kind cannot be a constant: its reduced-mode gate (`hidesView`) is asked
// where the kind is made, and the answer belongs to the installation this
// console is driving (`budget.ts`'s `budgetKindFor`, MO-B). So the compatibility
// is taken here, once, and every kind a caller draws is already the answering
// one -- which is also why the shell asks no gate of its own.
//
// The eleven are the console-improvements plan's own grouping plus the pools
// screen the model-credentials-routing plan adds (VS2): People & access' four --
// MO-P's three and the pools -- the Code group's four, the two repository reads,
// the image catalog and the recipes (MO-C), then Models & keys' two (MO-K) and
// Budget's one (MO-B).

import type { CliCompatibility } from '../../../shared/version'
import type { ManageKind } from '../types'
import { budgetKindFor } from './budget'
import { imagesKindFor } from './images'
import { KEYS_KIND } from './keys'
import { modelsKindFor } from './models'
import { PEOPLE_KINDS } from './people'
import { poolsKind } from './pools'
import { recipesKind } from './recipes'
import { reposKind, reposStatusKind } from './repos'

/**
 * KINDS is the Manage section's own list for the installation this console is
 * driving: every kind, in the order the Project tab draws them. The caller is
 * the tab (`screens/projects.ts`'s `mountProjectsManage`), and the kinds are
 * what `mountManage` draws the four groups over.
 */
export function KINDS(compatibility: CliCompatibility): readonly ManageKind[] {
  // The order is `MANAGE_GROUPS`' and the shell re-sorts by group rather than
  // trusting it: a list written in another order would still group correctly,
  // and this is the order the plan gives the kinds within a group -- the two
  // repository reads before the catalog, the catalog before the recipes.
  //
  // Three of the kinds are **built for this installation** rather than declared
  // once: the Budget kind's reduced-mode gate and the two writes' floors (the
  // image catalog's and the models'), each asked of the version line this
  // console read. A kind that asked no floor would offer a write to a CLI that
  // cannot be driven the way it drives it, which is the failure the constants
  // exist for.
  return [
    ...PEOPLE_KINDS,
    poolsKind,
    reposKind,
    reposStatusKind,
    imagesKindFor(compatibility.cliVersion),
    recipesKind,
    modelsKindFor(compatibility.cliVersion),
    KEYS_KIND,
    budgetKindFor(compatibility),
  ]
}
