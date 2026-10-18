// The picker's view (desktop-console plan Task 3.4b, step 5; design section 3.4
// rules 5 and 7, D13; owner ruling C9).
//
// The view is what the operator actually drives, and this is the only test that
// runs it. It runs under **plain Node with no DOM library**: `mountPicker`
// declares the four methods and three properties it uses, so the document below
// is a fake that implements exactly those, and the console gains no jsdom
// dependency for a window of eight controls.
//
// The fake is built from the ids `src/renderer/installations.html` **really
// declares**, read from the file rather than listed here, so the seam between
// the document and the view is held: a `#picker-pick-checkout` the view looked
// up and the document did not carry would be `null` here too, the mount would
// wire nothing, and the assertion that every control the view needs exists
// would be what fails.
//
// Each test names the mutation it catches:
//
//   - a view that sent a **path** instead of the token it was issued fails
//     "sends the token it was issued, and never a path";
//   - a view that let the form navigate fails "handles the submit in the page";
//   - a view that kept a token after an add fails "spends a choice exactly
//     once" -- which is the plan's red step for the single-use rule, seen from
//     the renderer's side of it;
//   - a view that wrote its own sentence over a refusal fails "shows the main
//     process's sentence verbatim";
//   - a view that acted on a row without re-reading fails "re-reads the list
//     after every action";
//   - a view that drew a row with `innerHTML` fails "draws a row as an element
//     whose text is `rowText`", whose fake has no `innerHTML` at all.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { UNDRAWABLE, mountPicker, rowText, viewOf } from '../../src/renderer/installations'
import type {
  InstallationListRow,
  InstallationView,
  PickerBridge,
  PickerDocument,
  PickerElement,
  PickerEvent,
  PickKind,
} from '../../src/renderer/installations'
import type { InstallationReply } from '../../src/preload/installations'
import { TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
// The shape is assembled at run time, never written down: no line of this
// repository carries a secret-shaped string, including this one (Task 2.0).
import { inert } from '../support/fakeSecrets'

// ---------------------------------------------------------------------------
// A document, faked from the real one
// ---------------------------------------------------------------------------

/** FakeElement is one node: text, the three properties the view touches, a
 * child list, and the listeners a test can fire. It has no `innerHTML`, no
 * `style`, no `href` and no `src` -- a view that reached for one would not
 * compile against `PickerElement`, and the fake is the second half of that. */
class FakeElement implements PickerElement {
  textContent: string | null = null
  value?: string
  disabled?: boolean
  readonly attributes = new Map<string, string>()
  readonly children: FakeElement[] = []
  private readonly listeners = new Map<string, ((event: PickerEvent) => void | Promise<void>)[]>()

  constructor(readonly tag: string) {}

  addEventListener(type: string, listener: (event: PickerEvent) => void | Promise<void>): void {
    const held = this.listeners.get(type) ?? []
    held.push(listener)
    this.listeners.set(type, held)
  }

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value)
  }

  appendChild(child: PickerElement): unknown {
    this.children.push(child as FakeElement)
    return child
  }

  replaceChildren(...nodes: PickerElement[]): void {
    this.children.length = 0
    for (const node of nodes) this.children.push(node as FakeElement)
  }

  /** fire runs one event, awaited: every handler in the view is async, and a
   * test that did not await would assert on a page mid-flight. */
  async fire(type: string): Promise<{ readonly prevented: boolean }> {
    const event = { prevented: false, preventDefault(): void { event.prevented = true } }
    for (const listener of this.listeners.get(type) ?? []) await listener(event)
    return event
  }

  /** byText finds a descendant button by its label. */
  button(label: string): FakeElement {
    const found = this.children.find((child) => child.tag === 'button' && child.textContent === label)
    if (found === undefined) throw new Error(`no button labelled ${label} under <${this.tag}>`)
    return found
  }
}

class FakeDocument implements PickerDocument {
  readonly byId = new Map<string, FakeElement>()
  createElement(tag: string): PickerElement {
    return new FakeElement(tag)
  }
  getElementById(id: string): PickerElement | null {
    return this.byId.get(id) ?? null
  }
}

const documentPath = fileURLToPath(new URL('../../src/renderer/installations.html', import.meta.url))
const html = readFileSync(documentPath, 'utf8')

/** fakeDocument builds the document's elements from the ids the file declares. */
function fakeDocument(): FakeDocument {
  const document = new FakeDocument()
  for (const match of html.matchAll(/id="([^"]+)"/g)) {
    const id = match[1] ?? ''
    document.byId.set(id, new FakeElement('div'))
  }
  return document
}

/** IDS are the controls the view looks up by name. The first test asserts every
 * one of them is in the document, which is what keeps this fake honest. */
const IDS = [
  'picker-status',
  'picker-list',
  'picker-add',
  'picker-project',
  'picker-add-submit',
  'picker-pick-binary',
  'picker-pick-config',
  'picker-pick-checkout',
] as const

// ---------------------------------------------------------------------------
// A bridge, faked on the wire
// ---------------------------------------------------------------------------

/** TOKENS are what the main process issues -- 32 hex characters, the shape
 * `PICK_TOKEN_PATTERN` in `src/main/installations.ts` accepts -- and each kind
 * gets its own so a test can tell which one was sent where. */
const TOKENS: Readonly<Record<PickKind, string>> = {
  binary: 'b1'.repeat(16),
  config: 'c2'.repeat(16),
  checkout: 'd3'.repeat(16),
}

interface Recording {
  readonly add: Readonly<Record<string, string>>[]
  readonly choose: Readonly<Record<string, string>>[]
  readonly remove: Readonly<Record<string, string>>[]
  readonly calls: string[]
}

/** fakeBridge is the picker's whole surface, recording what it was asked. */
function fakeBridge(options: {
  readonly rows?: readonly InstallationListRow[]
  readonly refusal?: string
  readonly picks?: Partial<Record<PickKind, InstallationReply>>
  readonly answer?: Partial<Record<'list' | 'add' | 'choose' | 'remove', InstallationReply>>
} = {}): { readonly bridge: PickerBridge; readonly recording: Recording } {
  const recording: Recording = { add: [], choose: [], remove: [], calls: [] }
  const listAnswer = (): InstallationReply =>
    options.answer?.list ??
    ({
      ok: true,
      data: {
        rows: options.rows ?? [],
        ...(options.refusal === undefined ? {} : { refusal: options.refusal }),
        path: '/home/owner/.config/fugaro-console/installations.json',
      },
    } as InstallationReply)
  const pick = (kind: PickKind): Promise<InstallationReply> => {
    // Named rather than inline: `ok: true` has to keep its literal type for the
    // reply union to accept it, and an inline object inside `Promise.resolve`
    // is inferred without the contextual type in some positions.
    const issued: InstallationReply = { ok: true, data: { token: TOKENS[kind] } }
    return Promise.resolve(options.picks?.[kind] ?? issued)
  }
  const bridge: PickerBridge = {
    list: async () => {
      recording.calls.push('list')
      return listAnswer()
    },
    pickBinary: () => pick('binary'),
    pickConfig: () => pick('config'),
    pickCheckout: () => pick('checkout'),
    add: async (args) => {
      recording.calls.push('add')
      recording.add.push(args)
      return options.answer?.add ?? { ok: true, data: { rows: [] } }
    },
    choose: async (args) => {
      recording.calls.push('choose')
      recording.choose.push(args)
      return options.answer?.choose ?? { ok: true, data: { rows: [], active: { chosen: false, sentence: 'x' } } }
    },
    remove: async (args) => {
      recording.calls.push('remove')
      recording.remove.push(args)
      return options.answer?.remove ?? { ok: true, data: { rows: [], active: { chosen: false, sentence: 'x' } } }
    },
  }
  return { bridge, recording }
}

function row(overrides: Partial<InstallationListRow> = {}): InstallationListRow {
  return { id: '0123456789abcdef', project: 'fugaro', binary: '/usr/local/bin/fugaro', active: false, mode: 'ok', ...overrides }
}

/** mount builds the view over a fresh fake document, and returns the pieces a
 * test needs. Every test starts here. */
async function mount(options: Parameters<typeof fakeBridge>[0] = {}) {
  const document = fakeDocument()
  const { bridge, recording } = fakeBridge(options)
  const view = mountPicker(document, bridge)
  await view.refresh()
  return { document, recording, view, element: (id: string): FakeElement => document.byId.get(id) as FakeElement }
}

// ---------------------------------------------------------------------------

describe('the picker’s document and its view', () => {
  it('declares every control the view looks up, and the view finds them', () => {
    // Non-vacuity, and the seam: the fake document is built from the file, so
    // this asserts the file carries the ids `mountPicker` needs. Without it, a
    // renamed id would turn every test below into a test of a page that wired
    // nothing at all.
    const document = fakeDocument()
    for (const id of IDS) expect(document.getElementById(id), `installations.html must declare #${id}`).not.toBeNull()
    expect(html).toContain('<script src="./installations.js" defer>')
  })

  it('draws the status the main process answered with', async () => {
    const empty = await mount({ rows: [] })
    expect(empty.element('picker-status').textContent).toBe('No installations yet. Add one below.')
    expect(empty.element('picker-add-submit').disabled).toBe(false)

    const active = await mount({ rows: [row({ active: true, mode: 'reduced' })] })
    expect(active.element('picker-status').textContent).toBe('fugaro is the installation this console is showing (reduced).')

    const noneChosen = await mount({ rows: [row()] })
    expect(noneChosen.element('picker-status').textContent).toBe('No installation is chosen. Choose one below.')
  })

  it('draws a row as an element whose text is `rowText`, with Choose and Remove', async () => {
    const listed = row({ config: '/home/owner/fugaro.yaml', checkout: '/home/owner/src/fugaro' })
    const { element } = await mount({ rows: [listed] })
    const list = element('picker-list')
    expect(list.children).toHaveLength(1)
    const item = list.children[0] as FakeElement
    // The row's text is `rowText`'s output, byte for byte: nothing the view adds
    // or hides. A view that built the line itself would drift from the model the
    // unit tests already pin.
    expect(item.textContent).toBe(rowText(listed))
    expect(item.children.map((child) => child.tag)).toEqual(['button', 'button'])
    expect(item.button('Choose').attributes.get('type')).toBe('button')
    expect(item.button('Remove').textContent).toBe('Remove')
    // The active row cannot be chosen again: it already is.
    const chosen = await mount({ rows: [row({ active: true })] })
    expect((chosen.element('picker-list').children[0] as FakeElement).button('Chosen').disabled).toBe(true)
  })

  it('re-reads the list after every action', async () => {
    const { element, recording } = await mount({ rows: [row()] })
    const item = element('picker-list').children[0] as FakeElement
    await item.button('Choose').fire('click')
    expect(recording.choose).toEqual([{ id: '0123456789abcdef' }])
    await item.button('Remove').fire('click')
    expect(recording.remove).toEqual([{ id: '0123456789abcdef' }])
    // One read to mount, then one after each of the two actions: a view that
    // patched its own list would show an installation the store had refused.
    expect(recording.calls).toEqual(['list', 'choose', 'list', 'remove', 'list'])
  })

  it('disables every control while a request is in flight, and sends no second choose (review S6)', async () => {
    const document = fakeDocument()
    const { bridge, recording } = fakeBridge({ rows: [row(), row({ id: 'fedcba9876543210', project: 'other' })] })
    let release: () => void = () => undefined
    const original = bridge.choose.bind(bridge)
    const held: PickerBridge = {
      ...bridge,
      choose: async (args) => {
        await new Promise<void>((resolve) => {
          release = resolve
        })
        return original(args)
      },
    }
    const view = mountPicker(document, held)
    await view.refresh()
    const list = document.byId.get('picker-list') as FakeElement
    const [first, second] = list.children as FakeElement[]
    const inFlight = first?.button('Choose').fire('click')
    await Promise.resolve()
    // Every control is disabled while the first choose is out.
    for (const id of ['picker-add-submit', 'picker-pick-binary', 'picker-pick-config', 'picker-pick-checkout']) {
      expect((document.byId.get(id) as FakeElement).disabled, id).toBe(true)
    }
    expect(second?.button('Choose').disabled).toBe(true)
    expect(second?.button('Remove').disabled).toBe(true)
    // A click that arrives anyway is dropped, not queued.
    await second?.button('Choose').fire('click')
    release()
    await inFlight
    expect(recording.choose).toEqual([{ id: '0123456789abcdef' }])
    // And the controls come back once the reply and the re-read are in.
    expect((document.byId.get('picker-add-submit') as FakeElement).disabled).toBe(false)
    const redrawn = (document.byId.get('picker-list') as FakeElement).children as FakeElement[]
    expect(redrawn[1]?.button('Choose').disabled).toBe(false)
  })

  it('sends the token it was issued, and never a path', async () => {
    const { element, recording } = await mount({ rows: [] })
    await element('picker-pick-binary').fire('click')
    // The button says a choice was made without naming it: the path never
    // crossed into this process, so there is nothing here to name.
    expect(element('picker-pick-binary').textContent).toBe('Binary chosen')
    element('picker-project').value = 'acme'
    const event = await element('picker-add').fire('submit')
    expect(recording.add).toEqual([{ project: 'acme', binary: TOKENS.binary }])
    // Mutation: a view that sent the path it was shown -- or any path at all --
    // fails here. There is no `/` in the payload, and the binary is exactly the
    // token this process was handed.
    for (const value of Object.values(recording.add[0] ?? {})) expect(value).not.toContain('/')
    expect(recording.add[0]?.['binary']).toBe(TOKENS.binary)
    expect(event.prevented).toBe(true)
  })

  it('handles the submit in the page, and one with nothing filled in sends nothing invented', async () => {
    const { element, recording } = await mount({ rows: [] })
    const event = await element('picker-add').fire('submit')
    expect(event.prevented).toBe(true)
    // No project and no token: the view sends neither key rather than an empty
    // string, so the sentence the operator gets is the main process's own
    // "project is required" rather than one this view made up about a value it
    // sent empty.
    expect(recording.add).toEqual([{}])
  })

  it('spends a choice exactly once', async () => {
    const { element, recording } = await mount({ rows: [] })
    await element('picker-pick-binary').fire('click')
    element('picker-project').value = 'acme'
    await element('picker-add').fire('submit')
    expect(recording.add[0]).toEqual({ project: 'acme', binary: TOKENS.binary })
    // The token is single-use on the main process's side, and the view cannot
    // tell whether its payload got as far as the token check -- so it drops
    // every token it holds. A view that kept one would send a spent token, and
    // the sentence it got back would be about the token, not the installation.
    expect(element('picker-pick-binary').textContent).toBe('Choose binary…')
    element('picker-project').value = 'acme'
    await element('picker-add').fire('submit')
    expect(recording.add[1]).toEqual({ project: 'acme' })
  })

  it('shows the main process’s sentence verbatim, and disables the form when the file cannot be read', async () => {
    const refused = await mount({
      answer: { list: { ok: false, reason: 'installations.json could not be read: version 2 is not 1' } },
    })
    expect(refused.element('picker-status').textContent).toBe('installations.json could not be read: version 2 is not 1')
    // An add is refused while the file cannot be read, so the form is refused
    // too: a view that left it enabled would offer an action whose only outcome
    // is another refusal.
    expect(refused.element('picker-add-submit').disabled).toBe(true)
    expect(refused.element('picker-pick-binary').disabled).toBe(true)

    const unreadable = await mount({ rows: [], refusal: 'an unknown key' })
    expect(unreadable.element('picker-status').textContent).toBe(
      '/home/owner/.config/fugaro-console/installations.json could not be read: an unknown key',
    )
    expect(unreadable.element('picker-add-submit').disabled).toBe(true)

    // A refusal from a *mutation* is shown over the list as it actually is: the
    // re-read happens first, so the sentence is not drawn over a list the failed
    // action did not change.
    const refusedChoose = await mount({
      rows: [row()],
      answer: { choose: { ok: false, reason: 'this console cannot drive the CLI at /usr/local/bin/fugaro' } },
    })
    const item = refusedChoose.element('picker-list').children[0] as FakeElement
    await item.button('Choose').fire('click')
    expect(refusedChoose.element('picker-status').textContent).toBe('this console cannot drive the CLI at /usr/local/bin/fugaro')
    expect(refusedChoose.recording.calls).toEqual(['list', 'choose', 'list'])
  })

  it('re-applies the display sanitizer to every string it draws', async () => {
    // Design section 5.1 rule 7: the console re-applies the sanitization at the
    // display layer rather than trusting the transport. The transport here is
    // the main process, and behind it a CLI whose stderr the picker draws -- so
    // a token-shaped run in a refusal must not reach the document, and neither
    // must the invisible characters a value can be made to misspell with.
    const invisible = '‮'
    const refused = await mount({
      answer: { list: { ok: false, reason: `could not read the binary at ${inert('gh', 'p_')}${'a'.repeat(36)}` } },
    })
    expect(refused.element('picker-status').textContent).toBe(`could not read the binary at ${TOKEN_PLACEHOLDER}`)

    const listed = await mount({ rows: [row({ binary: `/usr/local/bin/${invisible}fugaro` })] })
    const item = listed.element('picker-list').children[0] as FakeElement
    expect(item.textContent).toBe('fugaro — /usr/local/bin/fugaro')
    expect(item.textContent ?? '').not.toContain(invisible)

    // ...and a string the sanitizer refuses is drawn as the console's own
    // sentence about the class, never as the string itself. An over-long
    // refusal is the simplest string it refuses.
    const undrawable = await mount({ answer: { list: { ok: false, reason: 'x'.repeat(70_000) } } })
    expect(undrawable.element('picker-status').textContent).toBe(UNDRAWABLE)
  })

  it('answers a pick that was cancelled or reshaped without inventing a token', async () => {
    const cancelled = await mount({ picks: { binary: { ok: false, reason: 'nothing was chosen' } } })
    await cancelled.element('picker-pick-binary').fire('click')
    expect(cancelled.element('picker-status').textContent).toBe('nothing was chosen')
    expect(cancelled.element('picker-pick-binary').textContent).toBe('Choose binary…')

    // A success with no token is a shape this view does not know. It says so
    // rather than sending an empty string, which the validator would refuse with
    // a sentence about the wrong thing.
    const reshaped = await mount({ picks: { config: { ok: true, data: {} } } })
    await reshaped.element('picker-pick-config').fire('click')
    expect(reshaped.element('picker-status').textContent).toBe('the console answered without a choice')
    reshaped.element('picker-project').value = 'acme'
    await reshaped.element('picker-pick-binary').fire('click')
    await reshaped.element('picker-add').fire('submit')
    expect(reshaped.recording.add[0]).toEqual({ project: 'acme', binary: TOKENS.binary })
  })
})

describe('the view as a function of one reply', () => {
  it('is the whole of the two states, and the third for a file that was refused', () => {
    const empty: InstallationView = viewOf({ ok: true, data: { rows: [], path: '/p' } })
    expect(empty).toEqual({ status: 'No installations yet. Add one below.', rows: [], canAdd: true })
    const chosen: InstallationView = viewOf({ ok: true, data: { rows: [row({ active: true })], path: '/p' } })
    expect(chosen.canAdd).toBe(true)
    expect(chosen.rows).toHaveLength(1)
    const refused: InstallationView = viewOf({ ok: true, data: { rows: [], refusal: 'why', path: '/p' } })
    expect(refused).toEqual({ status: '/p could not be read: why', rows: [], canAdd: false })
    // A reply that is not a success at all is a sentence and nothing else.
    expect(viewOf({ ok: false, reason: 'nope' })).toEqual({ status: 'nope', rows: [], canAdd: false })
  })

  it('marks a row’s mode and its checkout, and no version', () => {
    // The row never carries a CLI version: it is re-read by the handshake on
    // every launch, and a stored one could only go stale. A view that drew one
    // would be drawing a fact the store refuses to keep.
    expect(rowText(row({ mode: 'reduced', checkout: '/src/fugaro' }))).toBe(
      'fugaro — /usr/local/bin/fugaro · checkout /src/fugaro · (reduced)',
    )
    expect(rowText(row({ active: true }))).toBe('fugaro — /usr/local/bin/fugaro · active')
  })
})
