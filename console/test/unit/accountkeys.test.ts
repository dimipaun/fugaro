// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The review account keys, as a section (the managed-screens plan's CM6; F16,
// F17, D1478, D1479).
//
// The ask was "the review account keys (`default_claude_account`,
// `claude_account_stages`: names only, never a token)", and the reconciliation
// answered where they come from: **`settings --json`**, the read the Project tab
// already makes (D1478), so this section adds no operation and no argv -- it is
// a second reader of a document that is already on the wire. The document
// carries the two keys as rows of its `settings` list under the registry's own
// spellings (`default_claude_account`, `stages.*.claude_account`, F16), and the
// section draws one row per key with the stage each belongs to.
//
// **The wildcard row carries an account this section does not draw.** The read
// resolves `stages.*.claude_account` to the first stage in sorted order and does
// not say which one it took (`valueAtPath`, `internal/cli/settings_read.go`), so
// the account in that row belongs to a stage the row does not name and the row
// states that instead (`ACCOUNT_KEYS_STAGE_WORD`). The case below pins both
// halves: the default row's account is drawn, and the wildcard row's account
// appears nowhere -- a document that named a stage (`stages.review.
// claude_account`) still draws that stage's account against it.
//
// **"Names only, never a token" is held by the read's rule, not by a promise**
// (D1479): a row the document marks `sensitive` is dropped whole -- no key, no
// value, no row -- so a key an operator makes secret-shaped leaves no trace on
// this screen. The case below is the one the task names, and the mutation it
// names is putting the dropped row back with an empty value in its cell.
//
// The fake screen is `projects.test.ts`'s, in this file's own words: the screens
// are siblings, each declares its own `ScreenElement`, and a section that
// reached for a navigating attribute, a resource, a style or a handler fails
// every case here rather than one.

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS } from '../../src/main/ipc'
import {
  ACCOUNT_KEYS_BECAUSE_CLASS,
  ACCOUNT_KEYS_CLASS,
  ACCOUNT_KEYS_KEY_CLASS,
  ACCOUNT_KEYS_OPERATION,
  ACCOUNT_KEYS_ROW_CLASS,
  ACCOUNT_KEYS_SOURCE,
  ACCOUNT_KEYS_STAGE_CLASS,
  ACCOUNT_KEYS_STAGE_WORD,
  ACCOUNT_KEYS_UNSET_WORD,
  ACCOUNT_KEYS_VALUE_CLASS,
  DEFAULT_ACCOUNT_KEY,
  STAGE_ACCOUNT_KEY,
  buildAccountKeys,
  renderAccountKeys,
} from '../../src/renderer/screens/accountkeys'
import type { ScreenElement, ScreenFactory } from '../../src/renderer/screens/accountkeys'
import { TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
import { githubToken } from '../support/fakeSecrets'

// ---------------------------------------------------------------------------
// The harness: a fake element that refuses what a section may not do.
// ---------------------------------------------------------------------------

/** FakeScreen is the element a section draws into. It records text, classes and
 * attributes, and it **throws on the attributes a screen must never set**: the
 * navigating attribute, a resource, a style and any handler. */
class FakeScreen implements ScreenElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set([
    ['h', 'ref'].join(''),
    'src',
    'srcdoc',
    'style',
    'formaction',
    'action',
  ])

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a console screen may not set the ${name} attribute`)
    }
    if (lowered === 'class') {
      this.classes.length = 0
      this.classes.push(...value.split(/\s+/).filter((one) => one !== ''))
    }
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: ScreenElement): void {
    this.nodes.push(child as FakeScreen)
  }

  childNodes(): readonly ScreenElement[] {
    return this.nodes
  }
}

const factory: ScreenFactory = { create: (tag) => new FakeScreen(tag) }

/** walk visits every node of a tree, the root first. */
function walk(node: ScreenElement): readonly FakeScreen[] {
  const fake = node as FakeScreen
  const out: FakeScreen[] = [fake]
  for (const child of fake.childNodes()) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: ScreenElement, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** one is the single node carrying a class, asserted to be single. */
function one(node: ScreenElement, className: string): FakeScreen {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** drawn is the whole tree's text, in document order: what a person reading the
 * screen would read if they read every cell. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** rowKeys is the key cell of every drawn row, in document order. */
function rowKeys(frame: ScreenElement): readonly (string | null)[] {
  return all(frame, ACCOUNT_KEYS_ROW_CLASS).map(
    (row) => all(row, ACCOUNT_KEYS_KEY_CLASS)[0]?.textContent ?? null,
  )
}

/** valueCells is the text of every row's value cell. */
function valueCells(frame: ScreenElement): readonly (string | null)[] {
  return all(frame, ACCOUNT_KEYS_VALUE_CLASS).map((cell) => cell.textContent)
}

/** stageCells is the text of every row's stage cell. */
function stageCells(frame: ScreenElement): readonly (string | null)[] {
  return all(frame, ACCOUNT_KEYS_STAGE_CLASS).map((cell) => cell.textContent)
}

// ---------------------------------------------------------------------------
// The documents.
// ---------------------------------------------------------------------------

/** settingRow is one row of the read's `settings` list: the members the read
 * prints for every key, and this section's two subjects only as far as they are
 * given. A row with no `value` member is a row the read carries no value for --
 * which is what a `sensitive` row is, and what the section's fallback row is. */
function settingRow(key: string, value?: string): Record<string, unknown> {
  const row: Record<string, unknown> = {
    key,
    scope: 'project',
    wins: value === undefined ? 'default' : 'local',
    state: 'ok',
    writable: false,
    owner: '',
    target_layer: 'local',
  }
  if (value !== undefined) row['value'] = value
  return row
}

/** totalSettings is the document `settings --json` prints as it is drawn by the
 * probe of the project this section was written against: a key beside the two
 * subjects, so a section that drew the whole table rather than its own two rows
 * fails the first case. */
function settingsDoc(rows: readonly Record<string, unknown>[]): Record<string, unknown> {
  return {
    version: 1,
    project: 'probe',
    generated_at: '2026-11-07T00:00:00Z',
    sources: [],
    settings: [
      settingRow('cap_seconds', '3600'),
      ...rows,
      settingRow('claude_account_auto', 'false'),
    ],
  }
}

/** drawn is the section drawn from one document. */
function section(raw: unknown, because: string | null = null): FakeScreen {
  const host = new FakeScreen('div')
  renderAccountKeys(factory, { raw, because }, host)
  const found = all(host, ACCOUNT_KEYS_CLASS)
  expect(found, `expected the section in .${ACCOUNT_KEYS_CLASS}`).toHaveLength(1)
  const root = found[0]
  if (root === undefined) throw new Error('no section')
  return root
}

describe('the review account keys, drawn from the read the tab already makes', () => {
  it('adds no operation: it draws the read the Project tab already spawns', () => {
    const operations = CONSOLE_OPERATIONS.map((operation) => operation.name)
    expect(operations).toContain(ACCOUNT_KEYS_OPERATION)
    const root = section(settingsDoc([settingRow(DEFAULT_ACCOUNT_KEY, 'team-b')]))
    expect(root.getAttribute('data-operation')).toBe(ACCOUNT_KEYS_OPERATION)
    expect(one(root, 'projects-account-keys-source').textContent).toBe(ACCOUNT_KEYS_SOURCE)
    expect(root.getAttribute('data-state')).toBe('read')
  })

  it('draws both keys, the default\'s account and no account against the wildcard', () => {
    const root = section(
      settingsDoc([
        settingRow(DEFAULT_ACCOUNT_KEY, 'team-b'),
        settingRow(STAGE_ACCOUNT_KEY, 'team-d'),
      ]),
    )
    // The two subjects in the order the section draws them: the project's
    // default first, the stage rows after it.
    expect(rowKeys(root)).toEqual([DEFAULT_ACCOUNT_KEY, STAGE_ACCOUNT_KEY])
    // The default row's account is the key's own value, so it is drawn. The
    // wildcard row's is the first stage account in sorted order -- a stage this
    // row does not name -- so the cell says what the read states instead, and
    // the account it carried is drawn nowhere: `* team-d` would say every stage
    // uses `team-d`.
    expect(valueCells(root)).toEqual(['team-b', ACCOUNT_KEYS_STAGE_WORD])
    expect(drawn(root)).not.toContain('team-d')
    // The default account belongs to no stage, so its row draws no stage cell.
    expect(stageCells(root)).toEqual(['*'])
    // Nothing else of the document is drawn: the read prints every key the
    // registry has, and this section's subject is two of them.
    const text = drawn(root)
    expect(text).not.toContain('cap_seconds')
    expect(text).not.toContain('3600')
    expect(text).not.toContain('claude_account_auto')
  })

  it('draws the stage each stage row belongs to', () => {
    const root = section(
      settingsDoc([settingRow('stages.review.claude_account', 'team-c')]),
    )
    // The default row is drawn either way -- this document carries no row for
    // it, so it reads unset -- and the stage row is drawn after it, with the
    // segment its key carries.
    expect(rowKeys(root)).toEqual([DEFAULT_ACCOUNT_KEY, 'stages.review.claude_account'])
    expect(stageCells(root)).toEqual(['review'])
    expect(valueCells(root)).toEqual([ACCOUNT_KEYS_UNSET_WORD, 'team-c'])
  })

  it('drops a sensitive row whole: no key, no value, no row', () => {
    const sensitive: Record<string, unknown> = {
      key: 'stages.adversarial.claude_account',
      scope: 'project',
      wins: 'local',
      state: 'ok',
      writable: false,
      owner: '',
      target_layer: 'local',
      sensitive: true,
    }
    const root = section(settingsDoc([sensitive, settingRow(DEFAULT_ACCOUNT_KEY, 'team-b')]))
    // The row is not blanked and not renamed: it is not drawn at all, and the
    // section draws the registry's own key as unset in its place -- which is
    // what it draws for a key the read carries no row for.
    expect(rowKeys(root)).toEqual([DEFAULT_ACCOUNT_KEY, STAGE_ACCOUNT_KEY])
    expect(drawn(root)).not.toContain('adversarial')
    expect(valueCells(root)).toEqual(['team-b', ACCOUNT_KEYS_UNSET_WORD])
  })

  it('draws a key the read does not carry as unset, and never as an empty cell', () => {
    const root = section(settingsDoc([]))
    expect(rowKeys(root)).toEqual([DEFAULT_ACCOUNT_KEY, STAGE_ACCOUNT_KEY])
    expect(valueCells(root)).toEqual([ACCOUNT_KEYS_UNSET_WORD, ACCOUNT_KEYS_UNSET_WORD])
    expect(valueCells(root)).not.toContain('')
  })

  it('draws the value the read spells as unset as the console\'s own word', () => {
    // The read answers a key no layer decides with the registry's sentence for
    // what unset means (`unset = the project's default account`), which is not
    // an account and not a value this section draws: the row says unset, and the
    // sentence reaches no cell.
    const root = section(
      settingsDoc([settingRow(STAGE_ACCOUNT_KEY, "unset = the project's default account")]),
    )
    expect(valueCells(root)).toEqual([ACCOUNT_KEYS_UNSET_WORD, ACCOUNT_KEYS_UNSET_WORD])
    expect(drawn(root)).not.toContain("the project's default account")
  })

  it('withholds the section for a row of a shape it does not read', () => {
    const root = section(settingsDoc([{ ...settingRow(DEFAULT_ACCOUNT_KEY), value: 42 }]))
    expect(root.getAttribute('data-state')).toBe('unavailable')
    expect(all(root, ACCOUNT_KEYS_ROW_CLASS)).toHaveLength(0)
    // The refusal names what the read carried, and it is a sentence of its own
    // and not an empty panel.
    expect(one(root, ACCOUNT_KEYS_BECAUSE_CLASS).textContent).toContain('does not read')
  })

  it('refuses on its own read rather than drawing a table it cannot vouch for', () => {
    const refused = section(null, 'settings.read: fugaro settings --json exited 1: no project config')
    expect(refused.getAttribute('data-state')).toBe('unavailable')
    expect(all(refused, ACCOUNT_KEYS_ROW_CLASS)).toHaveLength(0)
    expect(drawn(refused)).toContain('no project config')

    const skewed = section({ version: 2, settings: [] })
    expect(skewed.getAttribute('data-state')).toBe('unavailable')
    expect(drawn(skewed)).toContain('version 2')
  })

  it('draws the read\'s refusal as the CLI wrote it, not paraphrased', () => {
    // Design section 6.3: for a refusal the sentence *is* the remedy, so the
    // console shows it unedited. The refusal goes through `sanitizeRefusal`
    // (`drawnRefusal`), which keeps a path and a role as the CLI printed them --
    // the free-text `sentence` class draws the first `<path>` and the second
    // `[redacted]`, and the tab's settings panel draws this same `settings.read`
    // refusal unedited beside this section.
    const refused = section(
      null,
      'settings.read: fugaro settings --json exited 1: cannot read /home/zora/checkout/fugaro.yaml: the caller lacks roles/datastore.viewer',
    )
    const because = one(refused, ACCOUNT_KEYS_BECAUSE_CLASS).textContent ?? ''
    expect(because).toContain('/home/zora/checkout/fugaro.yaml')
    expect(because).toContain('roles/datastore.viewer')
    expect(because).not.toContain('<path>')
    expect(because).not.toContain('[redacted]')
  })

  it('still redacts a credential a child echoed into its refusal', () => {
    // The other half of the same door, and the reason the sentence is sanitized
    // rather than drawn as it arrived: a refusal is not this console's own text,
    // and a CLI that printed the token it failed with must not put it on a
    // screen. `sanitizeRefusal` is the credential rules and this layer's own two
    // (`drawnRefusal`), so this half holds while the remedy above survives.
    const token = githubToken()
    const refused = section(
      null,
      `settings.read: fugaro settings --json exited 1: permission denied for ${token}`,
    )
    const because = one(refused, ACCOUNT_KEYS_BECAUSE_CLASS).textContent ?? ''
    expect(because).not.toContain(token)
    expect(because).toBe(`settings.read: fugaro settings --json exited 1: permission denied for ${TOKEN_PLACEHOLDER}`)
  })

  it('draws no control on any row', () => {
    const root = section(
      settingsDoc([
        settingRow(DEFAULT_ACCOUNT_KEY, 'team-b'),
        settingRow('stages.review.claude_account', 'team-c'),
      ]),
    )
    const CONTROLS = ['button', 'a', 'input', 'select', 'textarea', 'form', 'label']
    for (const node of walk(root)) {
      expect(CONTROLS, `a section that shows a fact offers no control: ${node.tag}`).not.toContain(node.tag)
    }
    // The one key `settings set` could write is drawn without a write, which is
    // the owner's read-only rule for these screens (D1471).
    expect(walk(root).some((node) => node.getAttribute('data-setting-key') !== null)).toBe(false)
  })

  it('holds the model apart from the tree, so its rules are testable in plain Node', () => {
    const model = buildAccountKeys({
      raw: settingsDoc([settingRow(DEFAULT_ACCOUNT_KEY, 'team-b')]),
      because: null,
    })
    expect(model.state).toBe('read')
    expect(model.because).toBeNull()
    expect(model.rows).toEqual([
      { key: DEFAULT_ACCOUNT_KEY, stage: null, value: { kind: 'account', name: 'team-b' } },
      // The document carries no row for the wildcard key, so its cell is the
      // console's unset rather than the stage word -- the two are different
      // facts, and only a value the read resolved is the second one.
      { key: STAGE_ACCOUNT_KEY, stage: '*', value: { kind: 'unset' } },
    ])
  })
})
