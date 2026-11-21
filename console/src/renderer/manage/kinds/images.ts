// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Code group's image catalog (the console-improvements plan's task MO-C; the
// descriptor is `../types.ts` and the prose is
// `docs/console-improvements/manage-kinds.md`).
//
// **The document here is already decoded.** `image.catalog.ls` is one of the nine
// operations C-OPS wrote (`src/shared/new-ops.ts`), and its answer is the typed
// `ImageCatalogView`: the wire document's `default` and its snake_case members
// are read there, strictly, against the contract's own decoder. So this kind's
// `decode` is handed that view and re-reads it through `./read`'s defensive
// readers -- `unknown` in, per the descriptor -- for one reason: the shell hands
// a decode whatever the read answered, and a kind that trusted its own argument
// would throw on a value a later reader passed it. What it never does is decode
// the wire document a second time: a second reading of `default` and `use_when`
// is the drift `new-ops.ts` exists to prevent.
//
// The classes of this table are C-OPS's own (`NEW_OPERATION_TEXT_CLASSES`): the
// catalog's `name`, `tags`, `source` and `digest` are `label`, its `description`
// is `sentence`, and a kind that chose its own would be a second answer to a
// question the contract's decoder already answered. `manage-code.test.ts` holds
// each of them equal to that map.

import { boolAt, objectsAt, stringsAt, textAt } from './read'
import { defineManageKind } from '../types'
import type { JsonObject } from './read'
import type { ManageColumn, ManageKind, ManageRow } from '../types'

/** IMAGE_COLUMNS is the catalog's table: what an entry is called, the tags a
 * repository may name it by, whether it is what a job naming nothing runs, what
 * it is built from and the digest the last build recorded. */
export const IMAGE_COLUMNS: readonly ManageColumn[] = Object.freeze([
  { key: 'entry', title: 'Entry', class: 'label' },
  { key: 'tags', title: 'Tags', class: 'label' },
  { key: 'default', title: 'Default', class: 'label' },
  { key: 'source', title: 'Built from', class: 'label' },
  { key: 'description', title: 'Description', class: 'sentence' },
  { key: 'digest', title: 'Digest', class: 'label' },
])

/** imagesKind decodes `image ls --json` into one row per catalog entry. It has
 * no actions: the one write this subject admits is `image add`, and D2903's
 * first pass gives it to MW2 as a console window with its own review (the
 * descriptor's `ManageAction`). `image rm` and `image build` stay what the CLI
 * is for. */
export const imagesKind: ManageKind = defineManageKind({
  id: 'images',
  group: 'Code',
  title: 'Image catalog',
  source: 'image.catalog.ls',
  columns: IMAGE_COLUMNS,
  actions: [],
  decode(doc: unknown): readonly ManageRow[] {
    if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) return []
    const document = doc as JsonObject
    const rows: ManageRow[] = []
    for (const entry of objectsAt(document, 'entries')) {
      const name = textAt(entry, 'name')
      if (name === '') continue
      rows.push({
        key: name,
        cells: {
          entry: name,
          tags: stringsAt(entry, 'tags').join(', '),
          default: boolAt(entry, 'isDefault') ? 'yes' : 'no',
          source: textAt(entry, 'source'),
          description: textAt(entry, 'description'),
          digest: textAt(entry, 'digest'),
        },
      })
    }
    return rows
  },
})
