// The picker's view (desktop-console plan Task 3.4b; design section 3.4 rules 5
// and 7, D13, D14; owner ruling C9).
//
// The renderer half of the picker is deliberately the thinnest thing in this
// task, and that is the design's rule 7 rather than a shortcut: **the document
// holds no path, no id it can act on by itself, and no rule about what may be
// stored.** It draws what the main process answered, sends a project name and
// the tokens the main process issued, and nothing else. Every decision that
// matters -- is this binary executable, is this CLI in range, does this checkout
// belong to this project, is this token outstanding -- is made in
// `src/main/installations.ts`, where a test can hold it under plain Node and a
// renderer cannot reach it at all.
//
// `mountPicker` is the whole behaviour: it is handed the document and the
// bridge and returns a `refresh`, so every step below -- the list, the three
// pick buttons, the add form, a row's Choose and Remove -- is a unit test's to
// drive. `test/unit/installations-view.test.ts` does exactly that, over a fake
// document built from the ids `src/renderer/installations.html` actually
// declares, because the console has no jsdom dependency and this view is small
// enough that it does not need one.
//
// A token is the only thing that crosses back to the main process, and it is
// **single-use**: a redemption removes it, so the view drops each token it holds
// the moment the add that used it returns. The paths themselves never enter this
// process at all -- `list` answers rows, and a row's `binary` is the main
// process's account of a path it already holds, which is why `rowText` can draw
// it and a pick cannot.
//
// Every string drawn here goes through `drawn`, the display layer's one door
// (design section 5.1 rule 7): this document shows the CLI's own refusals, and
// stderr is a channel the console does not own.

import type { InstallationReply } from '../preload/installations'
import { sanitizeRefusal } from '../shared/sanitize'

/** InstallationListRow is one row as `fugaro:installations/list` answers it. */
export interface InstallationListRow {
  readonly id: string
  readonly project: string
  readonly binary: string
  readonly config?: string
  readonly checkout?: string
  readonly active: boolean
  readonly mode: string
}

/** InstallationListAnswer is the whole of what `list` answers. `refusal` is the
 * sentence the *file* was refused with, and it is drawn with `path` so the
 * operator can fix the file rather than guess which one it is. */
export interface InstallationListAnswer {
  readonly rows: readonly InstallationListRow[]
  readonly refusal?: string
  readonly path: string
}

/** InstallationView is what the document shows: the status line, the rows, and
 * whether the add form is usable. */
export interface InstallationView {
  readonly status: string
  readonly rows: readonly InstallationListRow[]
  readonly canAdd: boolean
}

/** viewOf turns one reply into the view. A refusal from the main process is the
 * status line verbatim -- it is already a sentence written for a person -- and
 * an unreadable file additionally disables the form, because an add is refused
 * while the file cannot be read. */
export function viewOf(reply: InstallationReply): InstallationView {
  if (!reply.ok) return { status: reply.reason, rows: [], canAdd: false }
  const answer = reply.data as InstallationListAnswer
  if (answer.refusal !== undefined) {
    return { status: `${answer.path} could not be read: ${answer.refusal}`, rows: answer.rows, canAdd: false }
  }
  if (answer.rows.length === 0) {
    return { status: 'No installations yet. Add one below.', rows: [], canAdd: true }
  }
  const active = answer.rows.find((row) => row.active)
  const status = active === undefined
    ? 'No installation is chosen. Choose one below.'
    : `${active.project} is the installation this console is showing (${active.mode}).`
  return { status, rows: answer.rows, canAdd: true }
}

/** UNDRAWABLE is what this view draws where the sanitizer refuses a string. It
 * is the console's own sentence about the *class* -- a value this console will
 * not draw is not a value with an explanation -- and it is never the string
 * that was refused. */
export const UNDRAWABLE = 'a line the console will not draw'

/**
 * drawn is the one door every string this view puts in the document goes
 * through, which is design section 5.1 rule 7: *"the console re-applies
 * `watch`'s sanitization at the display layer rather than trusting the transport
 * to have done it"*. The transport here is the main process, and behind it a CLI
 * whose stderr the picker draws -- `doctor`'s pairing refusal, verbatim.
 *
 * The pass is `sanitizeRefusal` and not `sanitizeText`, for the reason that
 * function's own doc gives: the `sentence` and `path` classes **replace** a path
 * with `<path>`, and a row exists so the operator can recognise the installation
 * they chose. `sanitizeRefusal` keeps a path, a project and a role as they were
 * written while redacting a credential-shaped run, dropping the invisible
 * characters a value can be made to misspell with, and folding the whole to one
 * bounded line. The operator's own paths are exactly the strings this window is
 * for, and a token-shaped run inside one is exactly what must not be drawn.
 */
function drawn(raw: string, field: string): string {
  const result = sanitizeRefusal(raw, field)
  return result.ok ? result.text : UNDRAWABLE
}

/** rowText is one row's line: the project, the binary and the state. It carries
 * no id and no `--config`: the id is the main process's handle for a row, and
 * the config path is a path, which the document draws only because the operator
 * chose it and must be able to recognise it -- through `drawn`, like every other
 * string here. */
export function rowText(row: InstallationListRow): string {
  const parts = [`${drawn(row.project, 'the row’s project')} — ${drawn(row.binary, 'the row’s binary')}`]
  if (row.config !== undefined) parts.push(`config ${drawn(row.config, 'the row’s config')}`)
  if (row.checkout !== undefined) parts.push(`checkout ${drawn(row.checkout, 'the row’s checkout')}`)
  if (row.mode !== 'ok') parts.push(`(${drawn(row.mode, 'the row’s mode')})`)
  if (row.active) parts.push('active')
  return parts.join(' · ')
}

// ---------------------------------------------------------------------------
// The slice of the DOM this view uses
// ---------------------------------------------------------------------------
//
// Four methods and three properties, declared here rather than taken from
// `lib.dom`, for the same reason the preload declares its own `ipcRenderer`
// slice: the view is then drivable under plain Node, where there is no DOM at
// all, and the interfaces say exactly what the view is allowed to touch. A
// `textContent` assignment is not markup, `appendChild` takes an element
// and not a string, and there is no `style`, no `href` and no `src`.

/** PickerEvent is the slice of an event this view uses: the form's submit is
 * handled in the page, so it must not navigate. */
export interface PickerEvent {
  preventDefault(): void
}

/** PickerElement is one node. `value` and `disabled` are optional because the
 * view touches them on the controls that have them, and the test's fake
 * elements are not the only thing that may satisfy this. */
export interface PickerElement {
  textContent: string | null
  value?: string
  disabled?: boolean
  addEventListener(type: string, listener: (event: PickerEvent) => void | Promise<void>): void
  setAttribute(name: string, value: string): void
  appendChild(child: PickerElement): unknown
  replaceChildren(...nodes: PickerElement[]): void
}

/** PickerDocument is the document, by id, plus `createElement` for the rows. */
export interface PickerDocument {
  getElementById(id: string): PickerElement | null
  createElement(tag: string): PickerElement
}

/** PickKind is the three dialogs, named as `src/main/installations.ts` names
 * them: one kind per argument of `add`. */
export type PickKind = 'binary' | 'config' | 'checkout'

/** PickerBridge is the view's whole capability. It is `InstallationBridge` as
 * the preload exposes it -- seven of its eight methods, because `current` is
 * the dashboard's read and the picker's status line is `list`'s -- and it is an
 * interface rather than the preload's own type so a test can hand in a fake
 * without pretending to be Electron. */
export interface PickerBridge {
  list(): Promise<InstallationReply>
  pickBinary(): Promise<InstallationReply>
  pickConfig(): Promise<InstallationReply>
  pickCheckout(): Promise<InstallationReply>
  add(args: Readonly<Record<string, string>>): Promise<InstallationReply>
  choose(args: Readonly<Record<string, string>>): Promise<InstallationReply>
  remove(args: Readonly<Record<string, string>>): Promise<InstallationReply>
}

/** PickerMount is the mounted view. `refresh` re-reads the list, which is what
 * every action ends with: the main process is the only thing that knows what is
 * stored, so the view never guesses what an add or a remove did to it. */
export interface PickerMount {
  refresh(): Promise<void>
}

/** PICK_LABELS is the three buttons' text, before and after a choice. A chosen
 * button says so, because the token it holds is the whole of what a successful
 * pick left behind: the view never learns the path, so it cannot draw one. */
const PICK_LABELS: Readonly<Record<PickKind, { readonly idle: string; readonly chosen: string }>> = {
  binary: { idle: 'Choose binary…', chosen: 'Binary chosen' },
  config: { idle: 'Choose config…', chosen: 'Config chosen' },
  checkout: { idle: 'Choose checkout…', chosen: 'Checkout chosen' },
}

/**
 * mountPicker wires the picker document to the bridge and returns its
 * `refresh`. Nothing here decides anything: it draws what a reply says, sends
 * what the operator entered, and re-reads. The four rules it does hold are the
 * ones rule 7 leaves it:
 *
 *   - **a path is never sent.** `add` carries a project name and the tokens the
 *     main process issued, and the view drops each token as soon as the add
 *     that used it returns -- a token is single-use, and a view that kept one
 *     would send a refused one the second time;
 *   - **a sentence is never written here.** Every refusal shown is a string the
 *     main process sent, and it is drawn through `drawn` rather than composed:
 *     the console does not paraphrase a refusal, and it does not trust one
 *     either;
 *   - **nothing is drawn as markup**: rows are elements whose text is
 *     `rowText`'s output, and the only attributes set are a button's `type`;
 *   - **a submit is handled in the page**, never as a navigation.
 */
export function mountPicker(document: PickerDocument, bridge: PickerBridge): PickerMount {
  const status = document.getElementById('picker-status')
  const list = document.getElementById('picker-list')
  const project = document.getElementById('picker-project')
  const submit = document.getElementById('picker-add-submit')
  const form = document.getElementById('picker-add')

  const pickers: Readonly<Record<PickKind, { readonly button: PickerElement | null; readonly send: () => Promise<InstallationReply> }>> = {
    binary: { button: document.getElementById('picker-pick-binary'), send: () => bridge.pickBinary() },
    config: { button: document.getElementById('picker-pick-config'), send: () => bridge.pickConfig() },
    checkout: { button: document.getElementById('picker-pick-checkout'), send: () => bridge.pickCheckout() },
  }

  /** tokens are the outstanding choices, and the only path-shaped things this
   * process ever holds are their names. */
  const tokens: Record<PickKind, string | undefined> = { binary: undefined, config: undefined, checkout: undefined }

  const say = (sentence: string): void => {
    // Every sentence here is the main process's, and it is drawn through the
    // sanitizer like every other string this view writes: a refusal from the CLI
    // is stderr, and stderr is a channel the console does not own.
    if (status !== null) status.textContent = drawn(sentence, 'the picker’s answer')
  }

  const controls = (): readonly (PickerElement | null)[] => [submit, pickers.binary.button, pickers.config.button, pickers.checkout.button]

  // busy is whether a request is in flight (review S6). While it is, every
  // control -- the form's and every row's -- is disabled, and a click that still
  // arrives is dropped: the main process serialises its mutations as well, and
  // this is what stops a second choose from being *sent* at all.
  let busy = false
  let view: InstallationView = { status: '', rows: [], canAdd: false }
  let rowButtons: { readonly button: PickerElement; readonly activeRow: boolean }[] = []
  const settle = (): void => {
    for (const control of controls()) {
      if (control !== null) control.disabled = busy || !view.canAdd
    }
    for (const { button, activeRow } of rowButtons) button.disabled = busy || activeRow
  }
  const exclusively = async (body: () => Promise<void>): Promise<void> => {
    if (busy) return
    busy = true
    settle()
    try {
      await body()
    } finally {
      busy = false
      settle()
    }
  }

  const rowElement = (row: InstallationListRow): PickerElement => {
    const item = document.createElement('li')
    item.textContent = rowText(row)
    const choose = document.createElement('button')
    choose.setAttribute('type', 'button')
    choose.textContent = row.active ? 'Chosen' : 'Choose'
    choose.addEventListener('click', () => act(() => bridge.choose({ id: row.id })))
    const remove = document.createElement('button')
    remove.setAttribute('type', 'button')
    remove.textContent = 'Remove'
    remove.addEventListener('click', () => act(() => bridge.remove({ id: row.id })))
    rowButtons.push({ button: choose, activeRow: row.active }, { button: remove, activeRow: false })
    item.appendChild(choose)
    item.appendChild(remove)
    return item
  }

  /** render draws one answer. The rows are replaced wholesale rather than
   * patched, so a row that is gone from the answer is gone from the page. */
  const render = (next: InstallationView): void => {
    view = next
    say(next.status)
    rowButtons = []
    if (list !== null) list.replaceChildren(...next.rows.map(rowElement))
    settle()
  }

  const refresh = async (): Promise<void> => {
    render(viewOf(await bridge.list()))
  }

  /** act sends one mutation and re-reads, with every control disabled until
   * both are done. A refusal is shown **after** the re-read, so the operator
   * sees the sentence over the list as it actually is rather than over a list
   * the failed action did not change. */
  const act = (send: () => Promise<InstallationReply>): Promise<void> =>
    exclusively(async () => {
      const reply = await send()
      await refresh()
      if (!reply.ok) say(reply.reason)
    })

  const clearTokens = (): void => {
    for (const kind of ['binary', 'config', 'checkout'] as const) {
      tokens[kind] = undefined
      const button = pickers[kind].button
      if (button !== null) button.textContent = PICK_LABELS[kind].idle
    }
  }
  // The buttons start from the view's own labels, not from whatever the
  // document's markup happened to say: a label is the one place the view
  // reports whether a pick is held, so it is the view that writes it.
  clearTokens()

  for (const kind of ['binary', 'config', 'checkout'] as const) {
    const picker = pickers[kind]
    const button = picker.button
    if (button === null) continue
    button.addEventListener('click', () =>
      exclusively(async () => {
        const reply = await picker.send()
        if (!reply.ok) {
          say(reply.reason)
          return
        }
        const token = (reply.data as { readonly token?: unknown }).token
        if (typeof token !== 'string' || token === '') {
          // The main process answers a cancel with `ok: false`, so a success
          // with no token is a shape this view does not know: it says so rather
          // than sending an empty string to `add`, which the validator would
          // refuse with a sentence about the wrong thing.
          say('the console answered without a choice')
          return
        }
        tokens[kind] = token
        button.textContent = PICK_LABELS[kind].chosen
        say('Chosen. Fill in the project name, then press Add.')
      }),
    )
  }

  form?.addEventListener('submit', (event) => {
    // The page is the whole application: a submit that navigated would leave
    // the console for a URL the CSP refuses, and would take the picker with it.
    event.preventDefault()
    return exclusively(async () => {
      const args: Record<string, string> = {}
      const named = project?.value ?? ''
      // A key is sent only when the operator filled it in: the main process's
      // own sentence for a missing one ("project is required") is better than a
      // refusal this view invented about a value it sent empty.
      if (named !== '') args['project'] = named
      for (const kind of ['binary', 'config', 'checkout'] as const) {
        const token = tokens[kind]
        if (token !== undefined) args[kind] = token
      }
      const reply = await bridge.add(args)
      // The tokens are dropped whether the add succeeded or not: this view
      // cannot tell **how far** the payload got (the main process redeems a
      // token where its validation reaches it), and a view that kept them would
      // sometimes send a spent one. Choosing again is one dialog.
      clearTokens()
      await refresh()
      if (!reply.ok) say(reply.reason)
      else if (project !== null) project.value = ''
    })
  })

  return { refresh }
}

/**
 * The bootstrap is guarded on the document and the bridge being there, for the
 * reason `src/preload/index.ts` gives about its own guard: this file is
 * imported by a unit test under plain Node, where there is no `document` and no
 * bridge, and importing it must do nothing at all.
 *
 * The cast is the same one the preload makes: this module declares the slice of
 * the DOM it uses, so it reads `globalThis` through its own interface rather
 * than through `lib.dom`'s.
 */
const scope = globalThis as { readonly document?: unknown; readonly fugaroInstallations?: unknown }

if (scope.document !== undefined && scope.fugaroInstallations !== undefined) {
  // The two assertions are where the declared slice meets the real page: this
  // module promises the view only ever touches what `PickerElement` names, and
  // the bridge is the preload's, exposed under the key it froze. `unknown` on
  // the scope rather than `PickerDocument`/`PickerBridge` is what keeps the cast
  // honest -- `lib.dom`'s `Document` is not this interface, and a cast that
  // claimed it was would be the compiler agreeing with a lie.
  void mountPicker(scope.document as PickerDocument, scope.fugaroInstallations as PickerBridge).refresh()
}
