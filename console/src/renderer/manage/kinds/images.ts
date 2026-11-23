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

// **The Add action, and the floor it waits on.** `image add` is the one write
// this subject admits (MW2, D2903), and this kind is where the console offers it:
// a write action whose form collects the entry's name, its definition, its tags
// and its description, submits them to `image.add.plan`, and leaves the window
// that follows to main and to the confirmation document. **Below the floor the
// action is the Copy-command instead**, and the floor matters more here than
// anywhere else on this surface: below it `image add` has no `--no-apply`, so a
// console that spawned it would run the entry's own build -- the one step of this
// console's writes that bills by itself -- behind the operator's back.
// `imageAddSupported` is the one answer, and `src/main/imageadd.ts` asks the same
// constant on its own side.
//
// `image rm` and `image build` stay what the CLI is for: no row of this table
// offers them, and neither is a write this console performs.

import { IMAGE_ADD_CLI_FLOOR, imageAddSupported } from '../../../shared/version'
import { buildCopyCommand, commandWord } from '../command'
import { boolAt, objectsAt, stringsAt, textAt } from './read'
import { defineManageKind } from '../types'
import type { JsonObject } from './read'
import type {
  ManageAction,
  ManageColumn,
  ManageKind,
  ManageRow,
  WriteAction,
  WriteField,
} from '../types'

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

/** IMAGE_ADD_LABEL is the image write's control, in the console's own words: it
 * says what the write does and not which command runs it. */
export const IMAGE_ADD_LABEL = 'Add an image entry'

/** IMAGE_ADD_FIELDS is the form's four boxes, keyed as `image.add.plan`'s
 * payload carries them and labelled as the CLI names the values. The tags and the
 * description are optional -- an entry that covers no repository's needs lists
 * none, and `image ls` prints an entry with no description the same way. The
 * definition is `owner/name:path` and nothing else: `--inline` writes executable
 * content from a renderer's text and `--image` names an image that already
 * exists, and neither is a word this console may send. */
export const IMAGE_ADD_FIELDS: readonly WriteField[] = Object.freeze([
  { key: 'name', label: 'Entry name', required: true },
  { key: 'dockerfile', label: 'Built from (owner/name:path)', required: true },
  { key: 'tags', label: 'Tags', required: false },
  { key: 'description', label: 'Description', required: false },
])

/** IMAGE_ADD_BELOW_FLOOR is the sentence the Copy-command is drawn with when
 * this CLI cannot be shown the write. It is the console's own words, and the
 * second half is the fact that matters here: what keeps the entry's build off
 * this machine is the write's own saved-steps word, which the release that
 * shows the write carries. **It names no flag**, for the models kind's reason:
 * a CLI below this floor has no `--no-apply` either, so the sentence names the
 * release rather than a word that CLI would refuse. */
export const IMAGE_ADD_BELOW_FLOOR =
  'this console needs a newer fugaro to show this write; the release that carries it keeps the build it owes saved for `fugaro apply` rather than running it'

/** IMAGE_ADD_BELOW_FLOOR_ARGV is the command the below-floor Copy-command
 * carries: `fugaro image add` and no values at all, for the reason the models
 * kind's own says -- a placeholder is not a word this console copies. */
const IMAGE_ADD_BELOW_FLOOR_ARGV = Object.freeze([
  commandWord('fugaro'),
  commandWord('image'),
  commandWord('add'),
])

/** IMAGE_ADD_ACTION is the write the kind offers above the floor. It is gated as
 * a project-level write because the CLI's own `image add` is a project admin's
 * (`internal/cli/image_catalog.go`), which is the authority this console's
 * advisory gate copies. */
const IMAGE_ADD_ACTION: WriteAction = Object.freeze({
  kind: 'write',
  plan: 'image.add.plan',
  label: IMAGE_ADD_LABEL,
  gate: 'manage-project',
  fields: IMAGE_ADD_FIELDS,
})

/** imagesActionsFor is the kind's whole action list for one CLI: the write above
 * the floor, and the Copy-command below it. */
function imagesActionsFor(cliVersion: string | null, floor: string | null): readonly ManageAction[] {
  if (cliVersion !== null && imageAddSupported(cliVersion, floor)) return [IMAGE_ADD_ACTION]
  const copy = buildCopyCommand(IMAGE_ADD_BELOW_FLOOR_ARGV, IMAGE_ADD_BELOW_FLOOR)
  return copy.ok ? [copy.action] : []
}

/**
 * imagesKindFor decodes `image ls --json` into one row per catalog entry, for
 * the installation the console is driving: the one place the image write's floor
 * is asked. `cliVersion` is the version line the handshake read, or `null` when
 * it read none -- below every floor, for the reason the models kind's own says.
 *
 * `floor` is the constant by default and an argument for the reason
 * `imageAddSupported`'s own is: the branch a release will take is testable while
 * the constant is still unset (`manage-writes.test.ts` drives both).
 */
export function imagesKindFor(
  cliVersion: string | null,
  floor: string | null = IMAGE_ADD_CLI_FLOOR,
): ManageKind {
  return defineManageKind({
    id: 'images',
    group: 'Code',
    title: 'Image catalog',
    source: 'image.catalog.ls',
    columns: IMAGE_COLUMNS,
    actions: imagesActionsFor(cliVersion, floor),
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
}

/** imagesKind is the kind for a console that read no version line at all: the
 * floor refuses it, so what it offers is the Copy-command -- the state today's
 * constant gives every version too (`IMAGE_ADD_CLI_FLOOR` is `null`). */
export const imagesKind: ManageKind = imagesKindFor(null)
