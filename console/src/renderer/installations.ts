// The picker's view (desktop-console plan Task 3.4b; design section 3.4 rules 4,
// 5 and 7, D13, D14; owner ruling C9, 2026-10-18; owner ruling of 2026-10-20).
//
// The renderer half of the picker is deliberately the thinnest thing in this
// task, and that is the design's rule 7 rather than a shortcut: **the document
// holds no path it can act on by itself, no URL, and no rule about what may be
// stored.** It draws what the main process answered and sends back a project
// name, a directory out of that project's own `checkouts`, or nothing at all.
// Every decision that matters -- is there a `fugaro` at all, is this CLI in
// range, is this checkout one the CLI listed, which installation is active -- is
// made in `src/main/`, where a test can hold it under plain Node and a renderer
// cannot reach it at all.
//
// **The add-an-installation form is gone** (owner's ruling of 2026-10-20): the
// console asks the operator for no binary and no config, so there is no project
// field, no "Choose binary…", no "Choose config…" and no Add. What is left is
//
//   1. the list the CLI printed, one row per installation, one click that opens
//      it, and one click that removes the console's own record of it;
//   2. rule 7's pairing, asked for **at the moment a screen needs it**: a row
//      offers the checkouts that installation was listed with, and setting one
//      is the only thing here that makes the console run a `doctor`;
//   3. the two onboarding screens (`src/main/onboarding.ts` holds their copy),
//      and one link, which this process does not name -- the `openExternal`
//      operation takes no arguments, so the URL is the main process's constant.
//
// `mountPicker` is the whole behaviour: it is handed the document and the bridge
// and returns a `refresh`, so every step below is a unit test's to drive.
// `test/unit/installations-view.test.ts` does exactly that, over a fake document
// built from the ids `src/renderer/installations.html` actually declares, because
// the console has no jsdom dependency and this view is small enough that it does
// not need one.
//
// Every string drawn here goes through `drawn`, the display layer's one door
// (design section 5.1 rule 7): this document shows the CLI's own refusals and its
// own warning lines, and stderr is a channel the console does not own.

import type { InstallationReply } from '../preload/installations'
import { sanitizeRefusal } from '../shared/sanitize'

/** OnboardingState is the union `src/main/onboarding.ts` answers with, mirrored
 * here the way the operation table is: the sandboxed renderer cannot import the
 * main process's module, and a shape it does not know is drawn by `viewOf` as no
 * screen rather than as half of one. */
export type OnboardingState =
  | { readonly kind: 'none' }
  | {
      readonly kind: 'install-cli'
      readonly reason: string
      readonly searched: readonly string[]
      readonly command: string
    }
  | { readonly kind: 'first-installation'; readonly command: string }

/** InstallationsListRow is one row as `fugaro:installations/list` answers it:
 * the merge of what the CLI listed and what this console has recorded. */
export interface InstallationsListRow {
  readonly project: string
  readonly gcpProject: string
  readonly region: string
  readonly configUnreadable: boolean
  readonly listed: boolean
  readonly saved: boolean
  readonly active: boolean
  readonly mode: string
  readonly checkout?: string
  readonly checkouts: readonly string[]
}

/** InstallationsListAnswer is the whole of what `list` answers. `refusal` is the
 * sentence the *file* was refused with, and it is drawn with `path` so the
 * operator can fix the file rather than guess which one it is. */
export interface InstallationsListAnswer {
  readonly onboard: OnboardingState
  readonly rows: readonly InstallationsListRow[]
  readonly warnings: readonly string[]
  readonly refusal?: string
  readonly path: string
}

/** InstallationView is what the document shows: the status line, the rows, the
 * CLI's own warning lines, and the onboarding screen (or none). */
export interface InstallationView {
  readonly status: string
  readonly rows: readonly InstallationsListRow[]
  readonly warnings: readonly string[]
  readonly onboard: OnboardingState
}

/** EMPTY_ONBOARD is `kind: 'none'`, spelled once. */
const NO_ONBOARD: OnboardingState = Object.freeze({ kind: 'none' })

/** NO_INSTALLATION_STATUS is the line the list carries when the CLI listed
 * nothing and the console has nothing recorded: the choice is not the operator's
 * to make yet, which is what the first-installation screen beside it says. */
export const NO_INSTALLATION_STATUS = 'No installations yet.'

/**
 * viewOf turns one reply into the view.
 *
 * A refusal from the main process is the status line verbatim -- it is already a
 * sentence written for a person -- and an unreadable file is drawn with the path
 * it names, because the operator has to be able to go and fix that file.
 *
 * An onboarding screen is drawn **instead of** a status line rather than beside
 * one: it is the whole of what the window has to say, and a second sentence over
 * it would be a second voice. The rows are still drawn under it, which is the
 * case that matters -- a console whose CLI would not list still has the
 * installations its own store holds, and hiding them would take away the one
 * installation the operator can still open.
 */
/**
 * drawnScreen is the onboarding screen a reply really carries: the two kinds
 * `src/main/onboarding.ts` draws, and no other.
 *
 * The union above is what the main process is *supposed* to send; this is the
 * check that the wired value is one of them, and it is what makes the comment
 * on `OnboardingState` true. `ONBOARDING_COPY` is a closed map over those two
 * kinds, so a `kind` this build has never heard of would reach
 * `onboardingNodes`' `copy.title` as `undefined` and throw -- and the throw
 * happens while the window is being drawn, so the rows and the CLI's own
 * warning lines go down with it. A screen this build is not written for is
 * drawn as **no screen** rather than as half of one.
 */
function drawnScreen(
  state: OnboardingState | undefined,
): Exclude<OnboardingState, { kind: 'none' }> | undefined {
  if (state?.kind === 'install-cli') return state
  if (state?.kind === 'first-installation') return state
  return undefined
}

export function viewOf(reply: InstallationReply): InstallationView {
  if (!reply.ok) {
    return { status: reply.reason, rows: [], warnings: [], onboard: NO_ONBOARD }
  }
  const answer = reply.data as InstallationsListAnswer
  const onboard = drawnScreen(answer.onboard) ?? NO_ONBOARD
  const rows = answer.rows ?? []
  const warnings = answer.warnings ?? []
  if (answer.refusal !== undefined) {
    return {
      status: `${answer.path} could not be read: ${answer.refusal}`,
      rows,
      warnings,
      onboard: NO_ONBOARD,
    }
  }
  if (onboard.kind !== 'none') return { status: '', rows, warnings, onboard }
  const active = rows.find((row) => row.active)
  if (active !== undefined) {
    return {
      status: `${active.project} is the installation this console is showing (${active.mode}).`,
      rows,
      warnings,
      onboard,
    }
  }
  return { status: NO_INSTALLATION_STATUS, rows, warnings, onboard }
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
export function drawn(raw: string, field: string): string {
  const result = sanitizeRefusal(raw, field)
  return result.ok ? result.text : UNDRAWABLE
}

/** rowText is one row's line: the installation, the GCP project it is in, the
 * region it runs in, and the state this console knows about it.
 *
 * A row whose config could not be read is drawn as **the CLI listed it** with the
 * console's own word for the empty fields -- `the config could not be read` -- and
 * never hidden: the CLI's own rule is that such an installation is still an
 * installation (it is a directory with a name), and the warning line beneath the
 * list says why. A row the CLI did not list at all says so too: it is a record
 * this console holds, not a fact of the machine, and the operator has to be able
 * to tell the two apart. */
export function rowText(row: InstallationsListRow): string {
  const project = drawn(row.project, 'the row’s project')
  if (row.listed && row.configUnreadable) return `${project} — the config could not be read`
  const parts: string[] = [project]
  if (row.gcpProject !== '') parts.push(drawn(row.gcpProject, 'the row’s GCP project'))
  if (row.region !== '') parts.push(drawn(row.region, 'the row’s region'))
  if (!row.listed) parts.push('in this console’s list only')
  if (row.checkout !== undefined) parts.push(`checkout ${drawn(row.checkout, 'the row’s checkout')}`)
  if (row.mode !== 'ok' && row.mode !== 'unknown') parts.push(`(${drawn(row.mode, 'the row’s mode')})`)
  if (row.active) parts.push('active')
  return parts.join(' · ')
}

/**
 * ONBOARDING_COPY is the two screens' fixed words, and it is the whole of what
 * this file says about them: the title, the one sentence of what the console is
 * missing, and the link button's label.
 *
 * The *command* is never here. It comes from the main process
 * (`src/main/onboarding.ts`), which is the one place the console spells
 * `brew install dimipaun/tap/fugaro` and `fugaro init` -- so a screen cannot
 * drift from the command the repository documents, and the day `docs/setup-wizard/`
 * renames `init` to `setup` there is one constant to change and a test that
 * pins it.
 */
export const ONBOARDING_COPY: Readonly<Record<'install-cli' | 'first-installation', {
  readonly title: string
  readonly body: string
  readonly link: string
}>> = Object.freeze({
  'install-cli': Object.freeze({
    title: 'Install the Fugaro CLI first',
    // Terse and true, and deliberately **silent about the cause**: three failures
    // of rule 4's resolution draw this screen -- nothing found anywhere, a
    // candidate that exists and does not answer `version`, and a `binaryPath` in
    // `installations.json` that is not absolute -- and only the first is "it found
    // none". The cause is never in this sentence: it is the resolution's own,
    // drawn beneath it as `state.reason`. The list of places is drawn from the
    // answer's own `searched`, not from this sentence either.
    body: 'The console drives the Fugaro CLI, and it has none it can use:',
    link: 'Open the getting-started guide',
  }),
  'first-installation': Object.freeze({
    title: 'Set up your first installation',
    body: 'The console found the Fugaro CLI, and it has no installation to show. Create one with:',
    link: 'Open the getting-started guide',
  }),
})

// ---------------------------------------------------------------------------
// The slice of the DOM this view uses
// ---------------------------------------------------------------------------
//
// A handful of methods and three properties, declared here rather than taken
// from `lib.dom`, for the same reason the preload declares its own `ipcRenderer`
// slice: the view is then drivable under plain Node, where there is no DOM at
// all, and the interfaces say exactly what the view is allowed to touch. A
// `textContent` assignment is not markup, `appendChild` takes an element and not
// a string, and there is no `style`, no `href` and no `src` -- this document
// opens no URL itself, and the one link it offers is a button whose click the
// main process answers.

/** PickerEvent is the slice of an event this view uses. */
export interface PickerEvent {
  preventDefault(): void
}

/** PickerElement is one node. `disabled` is optional because the view touches it
 * on the controls that have it, and the test's fake elements are not the only
 * thing that may satisfy this. */
export interface PickerElement {
  textContent: string | null
  disabled?: boolean
  addEventListener(type: string, listener: (event: PickerEvent) => void | Promise<void>): void
  setAttribute(name: string, value: string): void
  appendChild(child: PickerElement): unknown
  replaceChildren(...nodes: PickerElement[]): void
}

/** PickerDocument is the document, by id, plus `createElement` for the rows and
 * the onboarding screen. */
export interface PickerDocument {
  getElementById(id: string): PickerElement | null
  createElement(tag: string): PickerElement
}

/** PickerBridge is the view's whole capability: the picker's five operations,
 * and nothing else. It is an interface rather than the preload's own type so a
 * test can hand in a fake without pretending to be Electron.
 *
 * `checkout` takes a project and **optionally** a directory: absent means "stop
 * using the one that is there", which is how an operator gets back to an
 * installation with no checkout (rule 5's ordinary case).
 */
export interface PickerBridge {
  list(): Promise<InstallationReply>
  choose(args: Readonly<Record<string, string>>): Promise<InstallationReply>
  checkout(args: Readonly<Record<string, string>>): Promise<InstallationReply>
  remove(args: Readonly<Record<string, string>>): Promise<InstallationReply>
  openExternal(): Promise<InstallationReply>
}

/** PickerMount is the mounted view. `refresh` re-reads the list, which is what
 * every action ends with: the main process is the only thing that knows what is
 * stored, so the view never guesses what a choose or a remove did to it. */
export interface PickerMount {
  refresh(): Promise<void>
}

/**
 * mountPicker wires the picker document to the bridge and returns its
 * `refresh`. Nothing here decides anything: it draws what a reply says, sends
 * back what the operator clicked, and re-reads. The rules it does hold are the
 * ones rule 7 leaves it:
 *
 *   - **a path is never invented.** The only directory this view ever sends is
 *     one out of that row's own `checkouts` -- a list the CLI printed -- and the
 *     main process checks it against the document it last read;
 *   - **a sentence is never written here**: every refusal shown is a string the
 *     main process sent, drawn through `drawn` rather than composed;
 *   - **nothing is drawn as markup**: rows and warning lines are elements whose
 *     text is `rowText`'s or `drawn`'s output, and the only attribute set is a
 *     button's `type`;
 *   - **the link is a button, not an anchor**, and it sends no URL: `openExternal`
 *     takes no arguments, so the address the operating system opens is the main
 *     process's own constant (`GETTING_STARTED_URL`), checked against its host
 *     allow-list on the way out.
 */
export function mountPicker(document: PickerDocument, bridge: PickerBridge): PickerMount {
  const status = document.getElementById('picker-status')
  const list = document.getElementById('picker-list')
  const warnings = document.getElementById('picker-warnings')
  const onboarding = document.getElementById('picker-onboarding')

  const say = (sentence: string): void => {
    // Every sentence here is the main process's, and it is drawn through the
    // sanitizer like every other string this view writes: a refusal from the CLI
    // is stderr, and stderr is a channel the console does not own.
    if (status !== null) status.textContent = sentence === '' ? '' : drawn(sentence, 'the picker’s answer')
  }

  // busy is whether a request is in flight (review S6). While it is, every
  // control -- a row's and the link's -- is disabled, and a click that still
  // arrives is dropped: the main process serialises its mutations as well, and
  // this is what stops a second choose from being *sent* at all.
  let busy = false
  let controls: PickerElement[] = []
  const settle = (): void => {
    for (const control of controls) control.disabled = busy
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

  /** label is a `<code>` element holding one command, drawn exactly as the main
   * process spelled it. */
  const label = (tag: string, text: string, field: string): PickerElement => {
    const element = document.createElement(tag)
    element.textContent = drawn(text, field)
    return element
  }

  const button = (text: string, onClick: () => Promise<void>): PickerElement => {
    const element = document.createElement('button')
    element.setAttribute('type', 'button')
    element.textContent = text
    // The promise is **returned**, not dropped: the click's work (the send, the
    // re-read, the refusal's sentence) is what the next action waits for, and a
    // listener that fired and forgot would leave a test -- and a second click --
    // looking at a page mid-flight.
    element.addEventListener('click', () => act(onClick))
    controls.push(element)
    return element
  }

  /** onboardingNodes is one screen, as elements: the title, the one sentence of
   * what is missing, the resolution's own sentence for the absent-binary screen,
   * the one command, and the one link -- plus, for that screen, the places the
   * resolution looked, which is a fact the answer carries and this view does not
   * paraphrase.
   *
   * `state.reason` is drawn **as it arrived**: this screen is built for every
   * failure of rule 4's resolution, and only one of the three -- nothing found --
   * is described by the fixed words above. A candidate that exists and does not
   * answer `version`, and a `binaryPath` in `installations.json` that is not
   * absolute, are this screen too, and the sentence that says so is the one the
   * operator can act on. A screen that dropped it would tell an operator to
   * install a CLI they already have. */
  const onboardingNodes = (state: Exclude<OnboardingState, { kind: 'none' }>): PickerElement[] => {
    const copy = ONBOARDING_COPY[state.kind]
    const nodes: PickerElement[] = [
      label('h2', copy.title, 'the onboarding title'),
      label('p', copy.body, 'the onboarding sentence'),
    ]
    // The resolution's own sentence, and the places it looked, are the
    // absent-binary screen's alone: the other screen is drawn because a binary
    // **was** found, so its answer carries neither fact.
    if (state.kind === 'install-cli') {
      nodes.push(label('p', state.reason, 'the resolution’s own sentence'))
    }
    nodes.push(label('code', state.command, 'the onboarding command'))
    if (state.kind === 'install-cli') {
      nodes.push(label('p', `Looked in: ${state.searched.join(', ')}`, 'the places the console looked'))
    }
    // The link is a button and not an anchor: this document has no `href` and
    // no URL in it at all. The click is answered by the main process, whose one
    // external-link path is what holds the address and its allow-list.
    nodes.push(button(copy.link, async () => {
      const reply = await bridge.openExternal()
      if (!reply.ok) say(reply.reason)
    }))
    return nodes
  }

  const rowElement = (row: InstallationsListRow): PickerElement => {
    const item = document.createElement('li')
    item.textContent = rowText(row)
    const project = { project: row.project }
    item.appendChild(
      button(row.active ? 'Chosen' : 'Choose', async () => {
        const reply = await bridge.choose(project)
        await refresh()
        if (!reply.ok) say(reply.reason)
      }),
    )
    // Rule 7's pairing, asked for at the moment it is needed: the checkouts the
    // CLI listed for this installation, offered one button each. A row with none
    // offers none -- there is nothing to set, and the installation works without
    // a checkout (rule 5) -- and a row that already has one offers to drop it.
    const checkouts = row.checkouts ?? []
    for (const checkout of checkouts) {
      if (checkout === row.checkout) continue
      item.appendChild(
        button(`Use ${drawn(checkout, 'the row’s checkout')}`, async () => {
          const reply = await bridge.checkout({ ...project, checkout })
          await refresh()
          if (!reply.ok) say(reply.reason)
        }),
      )
    }
    if (row.checkout !== undefined) {
      item.appendChild(
        button('Stop using this checkout', async () => {
          const reply = await bridge.checkout(project)
          await refresh()
          if (!reply.ok) say(reply.reason)
        }),
      )
    }
    if (row.saved) {
      item.appendChild(
        button('Remove', async () => {
          const reply = await bridge.remove(project)
          await refresh()
          if (!reply.ok) say(reply.reason)
        }),
      )
    }
    return item
  }

  /** render draws one answer. The rows, the warning lines and the screen are
   * replaced wholesale rather than patched, so a row that is gone from the
   * answer is gone from the page -- and a warning line the CLI printed is on the
   * page for as long as the CLI prints it. */
  const render = (next: InstallationView): void => {
    say(next.status)
    controls = []
    if (list !== null) list.replaceChildren(...next.rows.map(rowElement))
    if (warnings !== null) {
      warnings.replaceChildren(...next.warnings.map((line) => label('li', line, 'the CLI’s warning')))
    }
    if (onboarding !== null) {
      onboarding.replaceChildren(...(next.onboard.kind === 'none' ? [] : onboardingNodes(next.onboard)))
    }
    settle()
  }

  const refresh = async (): Promise<void> => {
    render(viewOf(await bridge.list()))
  }

  /** act sends one mutation and re-reads, with every control disabled until both
   * are done. A refusal is shown **after** the re-read, so the operator sees the
   * sentence over the list as it actually is rather than over a list the failed
   * action did not change. */
  const act = (send: () => Promise<void>): Promise<void> => exclusively(send)

  return { refresh }
}

/**
 * The bootstrap is guarded on the document and the bridge being there, for the
 * reason `src/preload/index.ts` gives about its own guard: this file is imported
 * by a unit test under plain Node, where there is no `document` and no bridge,
 * and importing it must do nothing at all.
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
