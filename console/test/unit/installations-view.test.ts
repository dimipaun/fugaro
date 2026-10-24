// The picker's view (desktop-console plan Task 3.4b, step 5; design section 3.4
// rules 4, 5 and 7, D13; owner ruling C9; owner's ruling of 2026-10-20).
//
// The view is what the operator actually drives, and this is the only test that
// runs it. It runs under **plain Node with no DOM library**: `mountPicker`
// declares the methods and properties it uses, so the document below is a fake
// that implements exactly those, and the console gains no jsdom dependency for a
// window of a list and two screens.
//
// The fake is built from the ids `src/renderer/installations.html` **really
// declares**, read from the file rather than listed here, so the seam between the
// document and the view is held: a `#picker-warnings` the view looked up and the
// document did not carry would be `null` here too, and the assertion that every
// container the view needs exists would be what fails.
//
// Each test names the mutation it catches:
//
//   - a view that asked the operator for a **binary or a config** -- the form the
//     owner's ruling removed -- fails "the document declares no form", which
//     reads the file and asserts the controls are absent;
//   - a view that drew a row with `innerHTML` fails "draws a row as an element
//     whose text is `rowText`", whose fake has no `innerHTML` at all;
//   - a view that **hid** a row the CLI listed with a config it could not read
//     fails "draws a row the CLI listed with its warning";
//   - a view that sent a checkout the row did not offer fails "offers only the
//     checkouts the CLI listed";
//   - a view that opened a URL itself would not compile -- there is no `href` and
//     no anchor in the slice -- and the link test asserts the click sends **no
//     arguments at all**, which is what keeps the address in the main process;
//   - a view that wrote its own sentence over a refusal fails "shows the main
//     process's sentence verbatim";
//   - a view that acted on a row without re-reading fails "re-reads the list
//     after every action";
//   - a **stylesheet rule for an id this document does not declare** fails
//     "styles the containers this document declares, and no id it does not",
//     which is the case the removed form's `#picker-add` rules were caught by.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  NO_CHOICE_STATUS,
  NO_INSTALLATION_STATUS,
  ONBOARDING_COPY,
  UNDRAWABLE,
  mountPicker,
  rowText,
  viewOf,
} from '../../src/renderer/installations'
import type {
  InstallationsListRow,
  InstallationView,
  OnboardingState,
  PickerBridge,
  PickerDocument,
  PickerElement,
  PickerEvent,
} from '../../src/renderer/installations'
import type { InstallationReply } from '../../src/preload/installations'
import { TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
// The shape is assembled at run time, never written down: no line of this
// repository carries a secret-shaped string, including this one (Task 2.0).
import { inert } from '../support/fakeSecrets'

// ---------------------------------------------------------------------------
// A document, faked from the real one
// ---------------------------------------------------------------------------

/** FakeElement is one node: text, the properties the view touches, a child list,
 * and the listeners a test can fire. It has no `innerHTML`, no `style`, no
 * `href` and no `src` -- a view that reached for one would not compile against
 * `PickerElement`, and the fake is the second half of that. */
class FakeElement implements PickerElement {
  textContent: string | null = null
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

  /** button finds a direct child button by its label. */
  button(label: string): FakeElement {
    const found = this.children.find((child) => child.tag === 'button' && child.textContent === label)
    if (found === undefined) {
      throw new Error(`no button labelled ${JSON.stringify(label)} under <${this.tag}>; has ${JSON.stringify(this.children.map((child) => child.textContent))}`)
    }
    return found
  }

  /** labels are the text of every direct child, which is what the onboarding
   * screen's assertions read. */
  labels(): readonly (string | null)[] {
    return this.children.map((child) => child.textContent)
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

/** THEME is the stylesheet directory, and `screens.css` is the one file the
 * picker's rules live in (design section 5.4: every document of the bundle links
 * the one served stylesheet, and the screens' share of it is this file). */
const screensPath = fileURLToPath(new URL('../../src/renderer/theme/screens.css', import.meta.url))
const screens = readFileSync(screensPath, 'utf8')

/** fakeDocument builds the document's elements from the ids the file declares. */
function fakeDocument(): FakeDocument {
  const document = new FakeDocument()
  for (const match of html.matchAll(/id="([^"]+)"/g)) {
    const id = match[1] ?? ''
    document.byId.set(id, new FakeElement('div'))
  }
  return document
}

/** IDS are the containers the view looks up by name. The first test asserts every
 * one of them is in the document, which is what keeps this fake honest. */
const IDS = ['picker-status', 'picker-list', 'picker-warnings', 'picker-onboarding'] as const

/** FORM_CONTROLS are the ids and the labels of the add form the owner's ruling
 * removed. They are a value and not a grep in the test below, so the mutation --
 * putting the form back -- is what the test says it catches. */
const FORM_CONTROLS = [
  'picker-add',
  'picker-project',
  'picker-add-submit',
  'picker-pick-binary',
  'picker-pick-config',
  'picker-pick-checkout',
  'Choose binary…',
  'Choose config…',
  'Choose checkout…',
  'Project name',
] as const

// ---------------------------------------------------------------------------
// A bridge, faked on the wire
// ---------------------------------------------------------------------------

interface Recording {
  readonly choose: Readonly<Record<string, string>>[]
  readonly checkout: Readonly<Record<string, string>>[]
  readonly remove: Readonly<Record<string, string>>[]
  readonly reveal: Readonly<Record<string, string>>[]
  readonly calls: string[]
  readonly openExternal: unknown[][]
}

/** DEFAULT_PATH is the store path the fake answers with, so a refusal's sentence
 * names a file the test can recognise. */
const DEFAULT_PATH = '/home/owner/.config/fugaro-console/installations.json'

/** fakeBridge is the picker's whole surface, recording what it was asked. */
function fakeBridge(options: {
  readonly rows?: readonly InstallationsListRow[]
  readonly warnings?: readonly string[]
  readonly onboard?: OnboardingState
  readonly refusal?: string
  readonly answer?: Partial<
    Record<'list' | 'choose' | 'checkout' | 'remove' | 'reveal' | 'openExternal', InstallationReply>
  >
} = {}): { readonly bridge: PickerBridge; readonly recording: Recording } {
  const recording: Recording = {
    choose: [],
    checkout: [],
    remove: [],
    reveal: [],
    calls: [],
    openExternal: [],
  }
  const listAnswer = (): InstallationReply =>
    options.answer?.list ??
    ({
      ok: true,
      data: {
        onboard: options.onboard ?? { kind: 'none' },
        rows: options.rows ?? [],
        warnings: options.warnings ?? [],
        ...(options.refusal === undefined ? {} : { refusal: options.refusal }),
        path: DEFAULT_PATH,
        active: { chosen: false, sentence: 'no installation has been chosen' },
      },
    } as InstallationReply)
  const ok: InstallationReply = { ok: true, data: { rows: [], active: { chosen: false, sentence: 'x' } } }
  const bridge: PickerBridge = {
    list: async () => {
      recording.calls.push('list')
      return listAnswer()
    },
    choose: async (args) => {
      recording.calls.push('choose')
      recording.choose.push(args)
      return options.answer?.choose ?? ok
    },
    checkout: async (args) => {
      recording.calls.push('checkout')
      recording.checkout.push(args)
      return options.answer?.checkout ?? ok
    },
    remove: async (args) => {
      recording.calls.push('remove')
      recording.remove.push(args)
      return options.answer?.remove ?? ok
    },
    reveal: async (args) => {
      recording.calls.push('reveal')
      recording.reveal.push(args)
      return options.answer?.reveal ?? ok
    },
    // The operation takes no arguments, and this fake takes none either: a view
    // that tried to send a URL would not compile against `PickerBridge`.
    openExternal: async (...args: readonly unknown[]) => {
      recording.calls.push('openExternal')
      recording.openExternal.push([...args])
      return options.answer?.openExternal ?? { ok: true, data: { opened: 'https://fugaro.dev/getting-started/' } }
    },
  }
  return { bridge, recording }
}

/** row is one listed installation, with the fields a test does not name filled
 * in the way the CLI lists an ordinary one. */
function row(overrides: Partial<InstallationsListRow> = {}): InstallationsListRow {
  return {
    project: 'fugaro',
    gcpProject: 'fugaro-sandbox-482301',
    region: 'europe-west4',
    configUnreadable: false,
    listed: true,
    saved: true,
    active: false,
    mode: 'ok',
    checkouts: [],
    ...overrides,
  }
}

/** ONBOARD_CLI and ONBOARD_FIRST are the two screens as the main process answers
 * them -- the copy's own values, so a test that asserted a different title would
 * be asserting something the console does not say. */
const ONBOARD_CLI: OnboardingState = {
  kind: 'install-cli',
  reason: 'no fugaro binary was found: looked at /opt/homebrew/bin, /usr/local/bin',
  searched: ['/opt/homebrew/bin', '/usr/local/bin', '/Users/owner/.local/bin', '/usr/bin'],
  command: 'brew install dimipaun/tap/fugaro',
}
const ONBOARD_FIRST: OnboardingState = { kind: 'first-installation', command: 'fugaro setup' }

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
  it('declares every container the view looks up, and the view finds them', () => {
    // Non-vacuity, and the seam: the fake document is built from the file, so
    // this asserts the file carries the ids `mountPicker` needs. Without it, a
    // renamed id would turn every test below into a test of a page that wired
    // nothing at all.
    const document = fakeDocument()
    for (const id of IDS) expect(document.getElementById(id), `installations.html must declare #${id}`).not.toBeNull()
    expect(html).toContain('<script src="./installations.js" defer>')
  })

  it('the form is gone: the console asks the operator for no binary and no config', () => {
    // The owner's ruling of 2026-10-20, as a test: the manual add-an-installation
    // form is REMOVED. A console that put its controls back -- an input, a
    // "Choose binary…" button, a project field -- fails here.
    for (const control of FORM_CONTROLS) {
      expect(html, `installations.html must not carry ${JSON.stringify(control)}`).not.toContain(control)
    }
    expect(html).not.toContain('<form')
    expect(html).not.toContain('<input')
    // Non-vacuity: the assertions above are not "the string is absent from any
    // file" -- each one catches the control when it is planted.
    for (const control of FORM_CONTROLS) {
      expect(`<form id="${control}">`).toContain(control)
    }
    expect('<input id="x" />').toContain('<input')
  })

  it('styles the containers this document declares, and no id it does not', () => {
    // The two onboarding screens are screens in the approved visual language
    // (the owner's ruling of 2026-10-20), which means the stylesheet has to know
    // their hooks — and the removed form's id-scoped rules had to leave with the
    // form. Both halves are one scan, because both are the same drift:
    //
    //   - a rule selecting an id this document does not declare is a rule for a
    //     page that is not served (the form's `#picker-add`, `#picker-project`,
    //     `#picker-pick-*` and `#picker-add-submit` sat in `screens.css` after
    //     the markup was gone, styling nothing);
    //   - a container the view looks up with no rule at all is an unstyled
    //     screen — which is what the onboarding screen would have been, drawn in
    //     the browser's own defaults.
    //
    // Comments come out first: the stylesheet discusses these ids in its prose,
    // and a scan satisfied by a comment would be no scan at all.
    const code = screens.replace(/\/\*[\s\S]*?\*\//g, '')
    const declared = new Set([...html.matchAll(/id="([^"]+)"/g)].map((match) => match[1] ?? ''))
    const selected = new Set([...code.matchAll(/#([a-zA-Z][a-zA-Z0-9_-]*)/g)].map((match) => match[1] ?? ''))
    // Non-vacuity: the scan sees the hooks that are there, so the empty result
    // below is not a scan reading nothing.
    expect(selected.size).toBeGreaterThan(3)
    expect([...selected].filter((id) => !declared.has(id))).toEqual([])
    for (const id of IDS) expect(selected.has(id), `screens.css must style #${id}`).toBe(true)
    // And the mutation is real: the form's own rules fail the first half.
    const planted = '#picker-add { background: var(--surface); }'
    const plantedIds = [...planted.matchAll(/#([a-zA-Z][a-zA-Z0-9_-]*)/g)].map((match) => match[1] ?? '')
    expect(plantedIds.filter((id) => !declared.has(id))).toEqual(['picker-add'])
  })

  it('draws the status the main process answered with', async () => {
    // Review of this branch: a console whose CLI lists installations and which
    // has chosen none is the **ordinary first run** this PR exists to build, and
    // it is not the empty state. `NO_INSTALLATION_STATUS` ("No installations
    // yet.") belongs to the list with nothing in it -- the state the
    // first-installation screen is drawn for -- and drawing it over rows told the
    // operator there was nothing while showing them the installations they could
    // open.
    const listed = await mount({ rows: [row()] })
    expect(listed.element('picker-status').textContent).toBe(NO_CHOICE_STATUS)
    expect(NO_CHOICE_STATUS).not.toBe(NO_INSTALLATION_STATUS)

    const empty = await mount({ rows: [] })
    expect(empty.element('picker-status').textContent).toBe(NO_INSTALLATION_STATUS)

    const active = await mount({ rows: [row({ active: true, mode: 'reduced' })] })
    expect(active.element('picker-status').textContent).toBe('fugaro is the installation this console is showing (reduced).')
  })

  it('draws a row as an element whose text is `rowText`, with the row’s own buttons', async () => {
    const listed = row({ checkout: '/home/owner/src/fugaro' })
    const { element } = await mount({ rows: [listed] })
    const list = element('picker-list')
    expect(list.children).toHaveLength(1)
    const item = list.children[0] as FakeElement
    // The row's text is `rowText`'s output, byte for byte: nothing the view adds
    // or hides. A view that built the line itself would drift from the model the
    // unit tests already pin.
    expect(item.textContent).toBe(rowText(listed))
    expect(item.button('Choose').attributes.get('type')).toBe('button')
    expect(item.button('Remove').textContent).toBe('Remove')
  })

  it('draws a row the CLI listed with its warning, and never hides it', async () => {
    // The CLI's own rule: an installation whose config could not be read is still
    // listed, with its fields empty, and a warning line says why. A picker that
    // dropped the row would be a silent omission -- the one thing the CLI's
    // `config list` refuses to do.
    const listed = row({ project: 'acme', gcpProject: '', region: '', configUnreadable: true, saved: false })
    const warning = '/Users/owner/.config/fugaro/projects/acme.yaml could not be read: permission denied'
    const { element } = await mount({ rows: [listed], warnings: [warning] })
    const item = element('picker-list').children[0] as FakeElement
    expect(item.textContent).toBe('acme — the config could not be read')
    // The name is still there -- it is a fact of the directory -- and the row is
    // a row, not a note: it can be chosen like any other.
    expect(item.button('Choose')).toBeDefined()
    const warned = element('picker-warnings')
    expect(warned.children.map((child) => [child.tag, child.textContent])).toEqual([['li', warning]])
    // A record the CLI did not list is drawn too, and says so: it is this
    // console's memory of an installation, and the operator has to be able to
    // tell it apart from a fact of the machine.
    const stored = await mount({ rows: [row({ listed: false, checkouts: [] })] })
    expect((stored.element('picker-list').children[0] as FakeElement).textContent).toBe(
      'fugaro · fugaro-sandbox-482301 · europe-west4 · in this console’s list only',
    )
  })

  it('re-reads the list after every action', async () => {
    const { element, recording } = await mount({ rows: [row()] })
    const item = element('picker-list').children[0] as FakeElement
    await item.button('Choose').fire('click')
    expect(recording.choose).toEqual([{ project: 'fugaro' }])
    await item.button('Remove').fire('click')
    expect(recording.remove).toEqual([{ project: 'fugaro' }])
    // One read to mount, then one after each of the two actions: a view that
    // patched its own list would show an installation the store had refused.
    expect(recording.calls).toEqual(['list', 'choose', 'list', 'remove', 'list'])
  })

  it('offers only the checkouts the CLI listed, and one button to stop using one', async () => {
    // Rule 7's pairing, asked for at the moment a screen needs it: these are the
    // directories the document carried, and the main process checks the one that
    // comes back against the same list.
    const listed = row({ checkouts: ['/home/owner/src/fugaro', '/home/owner/src/fugaro-work'] })
    const { element, recording } = await mount({ rows: [listed] })
    const item = element('picker-list').children[0] as FakeElement
    await item.button('Use /home/owner/src/fugaro-work').fire('click')
    expect(recording.checkout).toEqual([{ project: 'fugaro', checkout: '/home/owner/src/fugaro-work' }])

    // A row already using one offers the *other* ones and a way back: an absent
    // `checkout` argument is how an operator clears it.
    const using = await mount({
      rows: [row({ checkouts: ['/home/owner/src/fugaro', '/home/owner/src/fugaro-work'], checkout: '/home/owner/src/fugaro' })],
    })
    const inUse = using.element('picker-list').children[0] as FakeElement
    const labels = inUse.children.map((child) => child.textContent)
    // ...and the reveal affordance, drawn on the row that has a checkout
    // (D3(a), T-D3). It is **drawn on the checkout and not on the spelling the
    // file manager will get**: the view names no path, so a WSL row offers the
    // same button a host row does.
    expect(labels).toEqual([
      'Choose',
      'Use /home/owner/src/fugaro-work',
      'Stop using this checkout',
      'Open the checkout folder',
      'Remove',
    ])
    await inUse.button('Stop using this checkout').fire('click')
    expect(using.recording.checkout).toEqual([{ project: 'fugaro' }])

    // A row the CLI listed with no checkouts offers none -- and the console
    // still works: an installation with no checkout is rule 5's ordinary case.
    const none = await mount({ rows: [row()] })
    expect((none.element('picker-list').children[0] as FakeElement).children.map((child) => child.textContent)).toEqual([
      'Choose',
      'Remove',
    ])
  })

  it('opens the row’s directory without ever being handed one (D3(a), T-D3)', async () => {
    // The half of the rule that keeps Windows spellings out of the view: the
    // button sends the **project** and nothing else -- no path, and so no UNC
    // spelling, which is the form a WSL checkout is opened by. What is opened is
    // the main process's own record for that project.
    const listed = row({ checkout: '/home/owner/src/fugaro' })
    const { element, recording } = await mount({ rows: [listed] })
    const item = element('picker-list').children[0] as FakeElement
    await item.button('Open the checkout folder').fire('click')
    expect(recording.reveal).toEqual([{ project: 'fugaro' }])
    // The main process's refusal is drawn, and only it: a row whose directory
    // could not be opened says so, rather than looking like a click that
    // worked.
    const refused = await mount({
      rows: [listed],
      answer: { reveal: { ok: false, reason: 'the console could not open the installation’s directory' } },
    })
    const refusedItem = refused.element('picker-list').children[0] as FakeElement
    await refusedItem.button('Open the checkout folder').fire('click')
    expect(refused.element('picker-status').textContent).toBe(
      'the console could not open the installation’s directory',
    )
    // ...and a row with no checkout (rule 5's ordinary case) offers no button at
    // all: there is nothing to open, and the view knows that without asking.
    const none = await mount({ rows: [row()] })
    expect(
      (none.element('picker-list').children[0] as FakeElement).children.map((child) => child.textContent),
    ).toEqual(['Choose', 'Remove'])
  })

  it('renders the absent-binary screen, with the resolution’s own sentence, the command and the places it looked', async () => {
    const { element } = await mount({ onboard: ONBOARD_CLI, rows: [] })
    const screen = element('picker-onboarding')
    const copy = ONBOARDING_COPY['install-cli']
    const state = ONBOARD_CLI as Extract<OnboardingState, { kind: 'install-cli' }>
    expect(screen.children.map((child) => [child.tag, child.textContent])).toEqual([
      ['h2', copy.title],
      ['p', copy.body],
      // The resolution's own sentence, drawn as it arrived. It is the one line
      // that names what actually went wrong, and it is **not** interchangeable
      // with the fixed sentence above: three different failures draw this screen
      // (see the case below), and only one of them is "nothing was found".
      ['p', state.reason],
      // The command is the main process's, byte for byte: this view spells none
      // of it.
      ['code', state.command],
      ['p', `Looked in: ${state.searched.join(', ')}`],
      ['button', copy.link],
    ])
    // The screen is the whole of what the window says: the status line is empty
    // over it rather than a second voice.
    expect(element('picker-status').textContent).toBe('')
  })

  it('draws the sentence that names the real problem, for a CLI that was found and refused', async () => {
    // `installCliState` is built for **every** failure of rule 4's resolution,
    // and two of the three are not "no binary was found": a candidate that exists
    // and exits non-zero or times out on `version`, and a `binaryPath` in
    // `installations.json` that is not absolute. A screen that drew only a fixed
    // "it found none" sentence would tell an operator to install a CLI they have
    // -- and the sentence that says what actually happened reaches this view in
    // `reason`, which is what this case holds.
    const refused: OnboardingState = {
      kind: 'install-cli',
      reason: 'the binary exited 7 for `version`: not a fugaro CLI',
      searched: ['/opt/homebrew/bin'],
      command: 'brew install dimipaun/tap/fugaro',
    }
    const { element } = await mount({ onboard: refused, rows: [] })
    const lines = (element('picker-onboarding').children as readonly FakeElement[]).map((child) => child.textContent)
    expect(lines, 'the sentence naming the real problem was dropped').toContain(
      'the binary exited 7 for `version`: not a fugaro CLI',
    )
    // ...and the fixed sentence does not claim a cause of its own, which is what
    // makes it true on all three failures. This is the mutation: a `body` that
    // said the CLI was not found sends this operator to install one they have.
    expect(ONBOARDING_COPY['install-cli'].body).not.toMatch(/found none|not found|no fugaro/)
  })

  it('renders the no-installation screen, with the one command', async () => {
    const { element } = await mount({ onboard: ONBOARD_FIRST, rows: [] })
    const screen = element('picker-onboarding')
    const copy = ONBOARDING_COPY['first-installation']
    expect(screen.children.map((child) => child.textContent)).toEqual([
      copy.title,
      copy.body,
      'fugaro setup',
      // No "looked in" line: this screen is drawn because a binary **was** found,
      // and the places it was found in are not what is missing.
      copy.link,
    ])
    // ...and it is drawn *instead of* the no-installations status line, not over
    // a screen that also says there are none.
    expect(element('picker-status').textContent).toBe('')
    expect(element('picker-list').children).toEqual([])
  })

  it('opens the one link through the surface, and sends no URL with it', async () => {
    // The link is a button and not an anchor, and the operation takes **no
    // arguments**: the address is `GETTING_STARTED_URL` in the main process, and
    // a renderer cannot name one. A view that sent a URL would not compile
    // against `PickerBridge`; this is the wire, which is the second half.
    for (const onboard of [ONBOARD_CLI, ONBOARD_FIRST]) {
      const { element, recording } = await mount({ onboard, rows: [] })
      const link = element('picker-onboarding').button(ONBOARDING_COPY[onboard.kind as 'install-cli'].link)
      expect(link.attributes.get('type')).toBe('button')
      await link.fire('click')
      expect(recording.openExternal).toEqual([[]])
      expect(recording.calls).toEqual(['list', 'openExternal'])
    }
    // A refusal from the main process is shown as the status line, verbatim.
    const refused = await mount({
      onboard: ONBOARD_CLI,
      rows: [],
      answer: { openExternal: { ok: false, reason: 'the host is not one the console opens in the browser' } },
    })
    await refused.element('picker-onboarding').button(ONBOARDING_COPY['install-cli'].link).fire('click')
    expect(refused.element('picker-status').textContent).toBe('the host is not one the console opens in the browser')
  })

  it('shows the main process’s sentence verbatim, over the list as it actually is', async () => {
    const refused = await mount({
      answer: { list: { ok: false, reason: 'installations.json could not be read: version 2 is not 1' } },
    })
    expect(refused.element('picker-status').textContent).toBe('installations.json could not be read: version 2 is not 1')

    const unreadable = await mount({ rows: [row()], refusal: 'an unknown key' })
    expect(unreadable.element('picker-status').textContent).toBe(`${DEFAULT_PATH} could not be read: an unknown key`)
    // The rows are still drawn: a refused store file does not take away an
    // installation the console can still open.
    expect(unreadable.element('picker-list').children).toHaveLength(1)

    // A refusal from a *mutation* is shown after the re-read, so the sentence is
    // not drawn over a list the failed action did not change.
    const refusedChoose = await mount({
      rows: [row()],
      answer: { choose: { ok: false, reason: 'this console cannot drive the CLI at /usr/local/bin/fugaro' } },
    })
    const item = refusedChoose.element('picker-list').children[0] as FakeElement
    await item.button('Choose').fire('click')
    expect(refusedChoose.element('picker-status').textContent).toBe('this console cannot drive the CLI at /usr/local/bin/fugaro')
    expect(refusedChoose.recording.calls).toEqual(['list', 'choose', 'list'])
  })

  it('disables every control while a request is in flight, and sends no second choose (review S6)', async () => {
    const document = fakeDocument()
    const { bridge, recording } = fakeBridge({ rows: [row(), row({ project: 'other' })] })
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
    // Every control is disabled while the first choose is out, the second row's
    // included.
    expect(second?.button('Choose').disabled).toBe(true)
    expect(second?.button('Remove').disabled).toBe(true)
    // A click that arrives anyway is dropped, not queued.
    await second?.button('Choose').fire('click')
    release()
    await inFlight
    expect(recording.choose).toEqual([{ project: 'fugaro' }])
    // And the controls come back once the reply and the re-read are in.
    const redrawn = (document.byId.get('picker-list') as FakeElement).children as FakeElement[]
    expect(redrawn[1]?.button('Choose').disabled).toBe(false)
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

    // A warning line is drawn through the same door: it carries a path the
    // operator needs, and it is stderr's text for all that.
    const warned = await mount({
      rows: [row()],
      warnings: [`${inert('gh', 'p_')}${'a'.repeat(36)} could not be read`],
    })
    expect(warned.element('picker-warnings').children[0]?.textContent).toBe(`${TOKEN_PLACEHOLDER} could not be read`)

    const listed = await mount({ rows: [row({ project: `fu${invisible}garo` })] })
    const item = listed.element('picker-list').children[0] as FakeElement
    expect(item.textContent).toBe('fugaro · fugaro-sandbox-482301 · europe-west4')
    expect(item.textContent ?? '').not.toContain(invisible)

    // ...and a string the sanitizer refuses is drawn as the console's own
    // sentence about the class, never as the string itself. An over-long
    // refusal is the simplest string it refuses.
    const undrawable = await mount({ answer: { list: { ok: false, reason: 'x'.repeat(70_000) } } })
    expect(undrawable.element('picker-status').textContent).toBe(UNDRAWABLE)
  })
})

describe('the view as a function of one reply', () => {
  const answer = (over: Record<string, unknown>): InstallationReply =>
    ({ ok: true, data: { onboard: { kind: 'none' }, rows: [], warnings: [], path: DEFAULT_PATH, ...over } } as InstallationReply)

  it('is the list, the two screens, and the refusal, and nothing else', () => {
    // Rows with nothing active: the ordinary first run, and its own sentence.
    const list: InstallationView = viewOf(answer({ rows: [row()] }))
    expect(list).toEqual({ status: NO_CHOICE_STATUS, rows: [row()], warnings: [], onboard: { kind: 'none' } })

    // No rows at all: the empty state, which is what "No installations yet."
    // means. The two are different lines because they are different situations.
    expect(viewOf(answer({}))).toEqual({
      status: NO_INSTALLATION_STATUS,
      rows: [],
      warnings: [],
      onboard: { kind: 'none' },
    })

    const first = viewOf(answer({ onboard: ONBOARD_FIRST }))
    expect(first.onboard).toEqual(ONBOARD_FIRST)
    // The screen is the whole of what the window says: no status line beside it.
    expect(first.status).toBe('')

    const cli = viewOf(answer({ onboard: ONBOARD_CLI }))
    expect(cli.onboard).toEqual(ONBOARD_CLI)

    // A refusal that is not a success at all is a sentence and nothing else.
    expect(viewOf({ ok: false, reason: 'nope' })).toEqual({
      status: 'nope',
      rows: [],
      warnings: [],
      onboard: { kind: 'none' },
    })

    // A reply a newer main process answered with a screen this view does not
    // know is drawn as no screen rather than as half of one -- and the rows and
    // the CLI's warning lines beside it are still drawn. A reply with **no**
    // `onboard` at all is the case above it (`options.onboard` defaults to
    // `none`); this is the one an unknown `kind` is, which used to reach
    // `ONBOARDING_COPY[state.kind].title` as `undefined.title` and throw.
    const unknown = viewOf(
      answer({
        rows: [row({ active: true })],
        warnings: ['the CLI said so'],
        onboard: { kind: 'future-screen', command: 'fugaro future' } as unknown as OnboardingState,
      }),
    )
    expect(unknown.onboard).toEqual({ kind: 'none' })
    expect(unknown.rows).toHaveLength(1)
    expect(unknown.warnings).toEqual(['the CLI said so'])
    // It is no screen, but it is not the first-installation screen either: the
    // rows are what the window says, so the status line is the active row's.
    expect(unknown.status).toBe('fugaro is the installation this console is showing (ok).')
  })

  it('draws no screen for a kind it does not know, and draws the rows anyway', async () => {
    // The same claim through the mount, which is where the throw actually
    // happened: `mountPicker`'s refresh calls `onboardingNodes` for any kind but
    // `none`, so an unknown one took the whole redraw down -- the rows included,
    // which is the one installation the operator can still open.
    const { element } = await mount({
      rows: [row({ active: true })],
      warnings: ['the CLI said so'],
      onboard: { kind: 'future-screen', command: 'fugaro future' } as unknown as OnboardingState,
    })
    expect(element('picker-onboarding').children).toEqual([])
    expect(element('picker-list').children).toHaveLength(1)
    expect(element('picker-warnings').children).toHaveLength(1)
  })

  it('marks a row’s state, and no version', () => {
    // The row never carries a CLI version: the handshake re-reads it on every
    // launch, and a stored one could only go stale. A view that drew one would be
    // drawing a fact the store refuses to keep.
    expect(rowText(row({ mode: 'reduced', checkout: '/src/fugaro' }))).toBe(
      'fugaro · fugaro-sandbox-482301 · europe-west4 · checkout /src/fugaro · (reduced)',
    )
    expect(rowText(row({ active: true }))).toBe('fugaro · fugaro-sandbox-482301 · europe-west4 · active')
    // `unknown` is not a state worth a word: nothing has read this installation's
    // CLI in this run, which is the ordinary case for a listed row.
    expect(rowText(row({ mode: 'unknown' }))).toBe('fugaro · fugaro-sandbox-482301 · europe-west4')
  })
})
