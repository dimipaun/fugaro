// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Models and API keys kinds (the console-improvements plan's task MO-K): the
// two lists of the Models & keys group, their columns, the documents they decode
// and the two commands the keys kind copies.
//
// **What this file is for.** Two facts the plan's acceptance names. The first is
// that the console reads what the CLI already answers and invents nothing: the
// models kind draws the six columns `models ls`' text form prints, a price the
// document does not carry is `n/a` and never `$0.00`, and a document this build's
// reader does not know is no rows rather than a guess. The second is D2905: the
// keys kind is names and status, no value anywhere on it, and the only thing it
// offers is the two commands to copy -- `fugaro secrets set NAME` (which stores a
// new version each time, and so is the rotation) and `fugaro secrets rm NAME`.
// The status is the console's word for the container's metadata, and `stored` is
// the CLI's own presence rule: a container is not a value, so one that resolves
// to no enabled version draws `no value`.
// The last block is the red step: a scan of the kind's own file that fails the
// moment an input control is added to it.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { TEXT_CLASSES } from '../../src/shared/sanitize'
import { copyCommandText } from '../../src/renderer/manage/command'
import { KEYS_KIND } from '../../src/renderer/manage/kinds/keys'
import { MODELS_KIND } from '../../src/renderer/manage/kinds/models'
import { MANAGE_GROUPS } from '../../src/renderer/manage/types'
import type { CopyCommandAction, ManageKind, ManageRow } from '../../src/renderer/manage/types'

/** MODELS_DOC is `models ls --json` (contract section 6.18): the two states the
 * table has to tell apart. `deepseek-v4.1-flash` is priced from the catalog and
 * served by a route; `claude-sonnet-5` is served by nothing -- `route` is `''`,
 * which the kind draws in the console's own words -- and holds no price at all,
 * with the CLI's own reason in `refused`. Its two prices are `null`, which is
 * unknown and never zero. */
const MODELS_DOC: unknown = {
  version: 1,
  project: 'example-project',
  catalog: null,
  routes: [],
  models: [
    {
      id: 'deepseek-v4.1-flash',
      route: 'gateway',
      source: 'catalog',
      input_per_m: 0.14,
      output_per_m: 0.28,
      catalog: null,
      allowed_to: ['*'],
      refused: '',
      note: 'routing.claude.via: gateway',
    },
    {
      id: 'claude-sonnet-5',
      route: '',
      source: 'none',
      input_per_m: null,
      output_per_m: null,
      catalog: null,
      allowed_to: [],
      refused: 'no route serves it',
      note: '',
    },
  ],
  repositories: [],
}

/** KEYS_DOC is `secrets ls --json`'s body (contract section 6.6): the bare
 * array, with the three states the table has to tell apart -- a key with a value
 * to resolve, a required name the repository's jobs mount and nothing resolves,
 * and the empty container `init` leaves behind before a value is stored. The
 * entries carry every member that is **not** a value but could be mistaken for
 * something to draw -- the container's `id`, when it was created, its version
 * count and newest enabled version, its labels -- and the tests below assert
 * that none of them reaches a cell. */
const KEYS_DOC: unknown = [
  {
    name: 'fugaro-github-token',
    scope: 'repo',
    allowed: ['example-owner/example-repo'],
    id: 'projects/example-cloud/secrets/fugaro-github-token',
    created: '2026-09-01T09:00:00Z',
    labels: { managed_by: 'fugaro' },
    versions: 4,
    latest: '3',
  },
  { name: 'fugaro-slack-webhook', scope: 'project', allowed: [], versions: 0, missing: true },
  {
    name: 'fugaro-optional-token',
    scope: 'repo',
    allowed: [],
    id: 'projects/example-cloud/secrets/fugaro-optional-token',
    created: '2026-09-02T09:00:00Z',
    labels: { managed_by: 'fugaro' },
    versions: 0,
    latest: '',
  },
]

/** KIND_SOURCE is the keys kind's own file: the red step is about what is *in*
 * the kind, and the descriptor it exports is a description rather than a
 * drawing, so the file's text is where a control would have to appear. */
const KIND_SOURCE = join(__dirname, '..', '..', 'src', 'renderer', 'manage', 'kinds', 'keys.ts')

/** FORM_WORDS is the vocabulary an input control is built from: the way to make
 * an element, and the elements a person types a value into. */
const FORM_WORDS = /\b(createElement|input|textarea|form|button|fieldset)\b/

/** formOffences reports the code lines of a module that name that vocabulary.
 * The line's comment is dropped first, so the file may say in prose what it must
 * not do -- a scan a comment could trip is a scan whose next author deletes the
 * comment. */
function formOffences(source: string): readonly string[] {
  return source
    .split('\n')
    .map((line) => {
      const at = line.indexOf('//')
      return at === -1 ? line : line.slice(0, at)
    })
    .filter((line) => FORM_WORDS.test(line))
}

/** rows decodes one document through one kind, so a case reads the answer rather
 * than the call. */
function rows(kind: ManageKind, doc: unknown): readonly ManageRow[] {
  return kind.decode(doc)
}

/** cells is every string one document's rows draw, columns in the order the kind
 * declared them, joined with a separator no value holds: it is what a case
 * asserts on when the question is what can reach the screen rather than which
 * row carried it. */
function cells(kind: ManageKind, doc: unknown): string {
  const keys = kind.columns.map((column) => column.key)
  return rows(kind, doc)
    .flatMap((row) => keys.map((key) => row.cells[key] ?? ''))
    .join('\u0000')
}

/** copyActions is the kind's copy-commands, refusing when an action is not one:
 * a case that expected a command and got something else says so, rather than
 * reading `argv` off a union. */
function copyActions(kind: ManageKind): readonly CopyCommandAction[] {
  return kind.actions.map((action) => {
    if (action.kind !== 'copy-command') throw new Error(`the kind offers a ${action.kind} action`)
    return action
  })
}

describe('the two kinds of the Models & keys group', () => {
  it('are the group’s two, in the descriptor’s own terms', () => {
    expect(MANAGE_GROUPS[2]).toBe('Models & keys')
    expect(MODELS_KIND.group).toBe('Models & keys')
    expect(KEYS_KIND.group).toBe('Models & keys')
    expect(MODELS_KIND.id).toBe('models')
    expect(KEYS_KIND.id).toBe('keys')
    expect(MODELS_KIND.title).toBe('Models')
    expect(KEYS_KIND.title).toBe('API keys')
    // The operations the shell reads their answers from: the Project tab's own
    // two reads, so a kind names no operation the allow-list does not carry.
    expect(MODELS_KIND.source).toBe('models.list')
    expect(KEYS_KIND.source).toBe('secrets.inventory')
  })

  it('declares a class the console sanitizes under for every column', () => {
    for (const kind of [MODELS_KIND, KEYS_KIND]) {
      expect(kind.columns.length).toBeGreaterThan(0)
      for (const column of kind.columns) {
        expect(TEXT_CLASSES, `${kind.id}'s ${column.key}`).toContain(column.class)
        expect(column.title, `${kind.id}'s ${column.key}`).not.toBe('')
      }
    }
  })

  it('fills a cell for every column it declared, and no other', () => {
    const cases: readonly (readonly [ManageKind, unknown])[] = [
      [MODELS_KIND, MODELS_DOC],
      [KEYS_KIND, KEYS_DOC],
    ]
    for (const [kind, doc] of cases) {
      const keys = kind.columns.map((column) => column.key).sort()
      expect(rows(kind, doc).length, `${kind.id} drew no row`).toBeGreaterThan(0)
      for (const row of rows(kind, doc)) {
        expect(Object.keys(row.cells).sort(), `${kind.id}'s ${row.key}`).toEqual(keys)
        for (const key of keys) expect(typeof row.cells[key], `${kind.id}'s ${row.key}.${key}`).toBe('string')
      }
    }
  })
})

describe('the models kind', () => {
  it('draws the columns the CLI’s own table prints', () => {
    expect(MODELS_KIND.columns.map((column) => column.title)).toEqual([
      'Model',
      'Route',
      'Source',
      'Input $/M',
      'Output $/M',
      'Notes',
    ])
  })

  it('draws each model with its route, where its price comes from and its note', () => {
    const [priced] = rows(MODELS_KIND, MODELS_DOC)
    expect(priced?.key).toBe('deepseek-v4.1-flash')
    expect(priced?.cells).toEqual({
      model: 'deepseek-v4.1-flash',
      route: 'gateway',
      source: 'catalog',
      input: '$0.14',
      output: '$0.28',
      notes: 'routing.claude.via: gateway',
    })
  })

  it('draws the console’s own words for a model no route serves, and its refusal', () => {
    const [, unpriced] = rows(MODELS_KIND, MODELS_DOC)
    expect(unpriced?.key).toBe('claude-sonnet-5')
    expect(unpriced?.cells.route).toBe('no route')
    // The reason the row holds no price is what a reader of the row needs, and
    // it is drawn from the document's own member rather than paraphrased.
    expect(unpriced?.cells.notes).toBe('no route serves it')
  })

  it('reads a price the document does not carry as n/a and never as $0.00', () => {
    const [, unpriced] = rows(MODELS_KIND, MODELS_DOC)
    expect(unpriced?.cells.input).toBe(UNAVAILABLE_MARK)
    expect(unpriced?.cells.output).toBe(UNAVAILABLE_MARK)
    const drawn = cells(MODELS_KIND, MODELS_DOC)
    expect(drawn).not.toContain('$0.00')
    expect(drawn).toContain(UNAVAILABLE_MARK)
  })

  it('answers no rows for a document this build’s reader does not know', () => {
    // A version this build does not read, and documents that are not a models
    // document at all: neither is a list to guess a row from.
    expect(rows(MODELS_KIND, { ...(MODELS_DOC as Record<string, unknown>), version: 99 })).toEqual([])
    expect(rows(MODELS_KIND, null)).toEqual([])
    expect(rows(MODELS_KIND, { routes: [] })).toEqual([])
  })

  it('offers the write’s Copy-command when the floor is unset, and no models add of its own', () => {
    // `MODELS_ADD_CLI_FLOOR` is `null` today, so every version is below it and
    // the kind offers the command instead of the write. The write itself is
    // `manage-writes.test.ts`'s case, where the floor is handed in.
    expect(copyActions(MODELS_KIND).map((action) => copyCommandText(action))).toEqual([
      'fugaro models add',
    ])
  })
})

describe('the keys kind', () => {
  it('draws the name, the scope and the status, and nothing else', () => {
    expect(KEYS_KIND.columns.map((column) => column.title)).toEqual(['Key', 'Scope', 'Status'])
    expect(rows(KEYS_KIND, KEYS_DOC).map((row) => row.cells)).toEqual([
      { name: 'fugaro-github-token', scope: 'repo', status: 'stored' },
      { name: 'fugaro-slack-webhook', scope: 'project', status: 'missing' },
      { name: 'fugaro-optional-token', scope: 'repo', status: 'no value' },
    ])
  })

  it('keys each row by the CLI’s own name of the key', () => {
    expect(rows(KEYS_KIND, KEYS_DOC).map((row) => row.key)).toEqual([
      'fugaro-github-token',
      'fugaro-slack-webhook',
      'fugaro-optional-token',
    ])
  })

  it('reads the document’s own missing flag as the status, and nothing else', () => {
    expect(rows(KEYS_KIND, KEYS_DOC)[1]?.cells.status).toBe('missing')
    // `missing` is written `true` and never anything else; a document that
    // spells it some other way is not the CLI's flag, and the row falls to the
    // container's own metadata (`latest` here, so a stored key).
    expect(rows(KEYS_KIND, [{ name: 'x', missing: 'yes', latest: '1' }])[0]?.cells.status).toBe('stored')
    expect(rows(KEYS_KIND, [{ name: 'x', latest: '1' }])[0]?.cells.status).toBe('stored')
    // And a row the CLI did mark is missing whatever else it carries.
    expect(rows(KEYS_KIND, [{ name: 'x', missing: true, latest: '1' }])[0]?.cells.status).toBe('missing')
  })

  it('draws no value for a container that resolves to nothing, and stored only for one that does', () => {
    // The CLI's own presence rule (`markMissingRequired`): a name counts as
    // present when the row carries a latest enabled version, so a container is
    // not a value. `{versions: 0}` is the empty container `init` leaves behind
    // before a value is stored (there is no `latest` at all), and `latest: ''`
    // is one whose versions are all disabled.
    const status = (entry: unknown): unknown => rows(KEYS_KIND, [entry])[0]?.cells.status
    expect(status({ name: 'x', scope: 'repo', versions: 0 })).toBe('no value')
    expect(status({ name: 'x', scope: 'repo', versions: 0, latest: '' })).toBe('no value')
    expect(status({ name: 'x', scope: 'repo', versions: 2, latest: '' })).toBe('no value')
    expect(status({ name: 'x', scope: 'repo', versions: 2, latest: '2' })).toBe('stored')
    // A document with no metadata of its own is a row the console cannot call
    // stored: nothing says a value resolves for it.
    expect(status({ name: 'x', scope: 'repo' })).toBe('no value')
  })

  it('never draws a member of the entry that is not a name, a scope or the flag', () => {
    const drawn = cells(KEYS_KIND, KEYS_DOC)
    for (const member of [
      'projects/example-cloud/secrets/fugaro-github-token',
      '2026-09-01T09:00:00Z',
      'managed_by',
      'example-owner/example-repo',
    ]) {
      expect(drawn, `${member} reached a cell`).not.toContain(member)
    }
    // The entry's two numbers are the version count and the newest enabled
    // version, and the status is a word rather than either of them -- `latest`
    // is read to choose that word and is never drawn, so `4` and `3` both
    // appear nowhere on the rows.
    expect(drawn).not.toContain('3')
    expect(drawn).not.toContain('4')
  })

  it('answers no rows for a document that is not the array', () => {
    expect(rows(KEYS_KIND, { secrets: [] })).toEqual([])
    expect(rows(KEYS_KIND, null)).toEqual([])
  })

  it('skips an entry with no name, and keeps the ones beside it', () => {
    const drawn = rows(KEYS_KIND, [null, { scope: 'repo' }, { name: '' }, { name: 'kept', scope: 'repo' }])
    expect(drawn.map((row) => row.key)).toEqual(['kept'])
  })

  it('draws n/a for a scope the document does not carry, never a blank cell', () => {
    expect(rows(KEYS_KIND, [{ name: 'x' }])[0]?.cells.scope).toBe(UNAVAILABLE_MARK)
  })
})

describe('the keys kind offers commands to copy, and never a form', () => {
  it('copies `secrets set` and `secrets rm`, in the CLI’s own spelling', () => {
    const drawn = copyActions(KEYS_KIND)
    expect(drawn.map((action) => action.argv)).toEqual([
      ['fugaro', 'secrets', 'set', 'NAME'],
      ['fugaro', 'secrets', 'rm', 'NAME'],
    ])
    // The text the clipboard gets: one word, one shell word, and no quoting.
    // `set` is the command that stores a version, so running it again under the
    // same name is the rotation, and there is no third action to offer.
    expect(drawn.map((action) => copyCommandText(action))).toEqual([
      'fugaro secrets set NAME',
      'fugaro secrets rm NAME',
    ])
  })

  it('offers nothing the console performs itself', () => {
    expect(KEYS_KIND.actions.length).toBeGreaterThan(0)
    for (const action of KEYS_KIND.actions) expect(action.kind).toBe('copy-command')
  })

  it('carries no input control, and the scan sees one when it is planted', () => {
    const source = readFileSync(KIND_SOURCE, 'utf8')
    expect(source, 'the kind file was read').toContain('KEYS_KIND')
    expect(formOffences(source)).toEqual([])
    // The mutation, so a scan that read nothing cannot pass: every way an input
    // control would arrive in the kind is reported.
    expect(formOffences(`${source}\nconst field = document.createElement('input')`)).toHaveLength(1)
    expect(formOffences(`${source}\nconst area = document.createElement('textarea')`)).toHaveLength(1)
    expect(formOffences(`${source}\nconst tag = '<form>'`)).toHaveLength(1)
    expect(formOffences(`${source}\nconst submit = document.createElement('button')`)).toHaveLength(1)
    // And a comment may name them: the file's own prose is not an offence.
    expect(formOffences('// no input, no textarea, no form and no button')).toEqual([])
  })
})
