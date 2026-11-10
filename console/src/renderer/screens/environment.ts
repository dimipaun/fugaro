// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Which side of the boundary the CLI's credentials are on (the console-on-
// Windows plan's T-G5; reconciliation F21, D5(a); design section 3.4 rule 7).
//
// **The console holds no credential and reads no token, on either platform.**
// That is rule 7's second prohibition, it is a property of the spawn and of the
// installation store rather than of a screen -- the child gets the fixed
// environment of `CHILD_ENV_KEYS` (`src/main/spawn.ts`) and finds its identity
// the way a terminal does, in the home directory of the environment it runs in
// -- and this module is the *statement* of it rather than a second
// implementation: it opens no file, reads no variable and takes no input but the
// installation answer the header already has.
//
// **On the WSL route that home is the distribution's home, and the file there is
// a different file.** `gcloud auth application-default login` run inside WSL
// writes `~/.config/gcloud/application_default_credentials.json` *inside the
// distribution*; run on Windows it writes
// `%APPDATA%\gcloud\application_default_credentials.json`
// (`docs/windows-cli/wsl.md`, its second trap, F39). So **signing in on Windows
// does not sign the CLI in**: `fugaro doctor` answers "not authenticated", and
// an operator who has just watched a browser login succeed has no way to guess
// why. F21 asks for that reason in the console's own words rather than as
// doctor's bare "not authenticated", and this is it.
//
// **The sentences are the console's own words, and they are constants.** They
// are written through this module's `literal`, so they carry no document string
// by construction: the distribution's name, its home and the credentials file's
// path are all things a document or `wsl.exe` would have to supply, and a
// sentence built from any of them would not be the console's. The distribution
// the CLI runs in is named by the installation line beside these (and by the
// rail row), so repeating a name here would be a second spelling of a fact the
// operator is already looking at.
//
// **Drawn only when the installation is a WSL one, and drawn as nothing
// otherwise.** `installation.current` carries `distro` exactly when the active
// installation is a WSL one (`ActiveInstallation`, `src/main/installations.ts`,
// T-D1: the field is omitted for the empty value when the answer is built), so
// the absence of a non-empty `distro` is the whole of "this CLI is not inside a
// distribution". An answer the console cannot read, a store that refused and a
// machine with nothing chosen draw nothing here -- the same rule the
// installation line's own branches keep, because a sentence about a boundary
// nobody has crossed would be a sentence about nothing.
//
// **Where it is drawn, and why that is the environment view.** These lines hang
// under the installation line the header draws (`drawInstallation`,
// `src/renderer/entry.ts`): the console's statement of the environment it is
// driving -- the project, the binary, and the version and mode the handshake
// read. That line is the one surface in this tree that receives `distro`, and
// `ActiveInstallation`'s own comment says the field exists for exactly this
// sentence (T-D1, F21). The `doctor` document, which the design calls the
// environment view, is drawn by the Project tab's credentials-service and
// policy panels; neither is handed an installation, and neither could state
// which side of the boundary the CLI is on without being given one.

/** EnvironmentElement is what this module draws into, declared structurally the
 * way `shared/version.ts` declares its own: the entry's `SkewElement` and the
 * screens' `ScreenElement` are both the same shape, so an element crosses
 * between the three without a conversion and this module imports no screen. */
export interface EnvironmentElement {
  textContent: string | null
  setAttribute(name: string, value: string): void
  readonly tag: string
  append(child: EnvironmentElement): void
}

/** EnvironmentFactory is where the elements come from. */
export interface EnvironmentFactory {
  create(tag: string): EnvironmentElement
}

// ---------------------------------------------------------------------------
// The view's own vocabulary.
// ---------------------------------------------------------------------------

/** ENVIRONMENT_CREDENTIALS_SIDE_CLASS is the class of the line that says which
 * side of the boundary the CLI's credentials are on. It is an `installation-`
 * hook like its neighbours (`docs/design/console-styling.md` section 2.1, the
 * installation line's row), and no rule selects it yet: the line reads as one
 * more sentence of the installation block, and a rule that wants to tone it
 * joins that table first. */
export const ENVIRONMENT_CREDENTIALS_SIDE_CLASS = 'installation-credentials-side'

/** ENVIRONMENT_CREDENTIALS_HELD_CLASS is the class of the line that says what
 * the console itself holds -- nothing. The two are separate lines and separate
 * facts: the first is about the CLI's identity, the second about the console's,
 * and F21 needs both said (the console must not be read as the thing that is or
 * is not authenticated). */
export const ENVIRONMENT_CREDENTIALS_HELD_CLASS = 'installation-credentials-held'

/** ENVIRONMENT_CREDENTIALS_IN_DISTRO is the sentence T-G5 exists for, in the
 * console's own words: the credentials the CLI uses are **the distribution's
 * own**, and the login that produces them has to happen inside it. */
export const ENVIRONMENT_CREDENTIALS_IN_DISTRO =
  'the CLI runs inside the distribution, so the credentials it uses are the distribution’s own: sign in inside the distribution (`gcloud auth application-default login`), not on Windows, where the same command writes a different file'

/** ENVIRONMENT_CREDENTIALS_HELD_BY_CONSOLE is the other half: the console holds
 * no credential and reads none, so `doctor` -- and not this window -- is what
 * answers whether the CLI is signed in. It names the read rather than saying
 * "not authenticated" in the console's own voice, which is the mistake F21
 * names: a console that reported a state it did not read would be guessing
 * about an identity it never sees. */
export const ENVIRONMENT_CREDENTIALS_HELD_BY_CONSOLE =
  'this console holds no credential and reads none: whether the CLI is signed in is what `fugaro doctor` answers, and here it answers about the distribution, not about Windows'

// ---------------------------------------------------------------------------
// The decision, and the drawing.
// ---------------------------------------------------------------------------

/**
 * runsInsideADistribution answers whether one `installation.current` reply names
 * an installation whose CLI runs inside a WSL distribution -- which is the whole
 * decision this view makes about what to say.
 *
 * The reply is read here exactly as `drawInstallation` reads it -- the
 * `ConsoleReply` envelope, then the data object -- and the decision is one
 * field: a **non-empty `distro`**, which the main process puts on this answer
 * only for a WSL installation (T-D1). A value that is absent, empty or not a
 * string is "this CLI is not inside a distribution", which is the same answer
 * as a store that refused and is deliberately not an error: the view says
 * nothing rather than guessing a side.
 *
 * It answers a boolean and not the sentence, so that the two sentences reach
 * `literal` as the constants they are: a drawer that wrote whatever a helper
 * returned would be a drawer `screen-literals.test.ts` cannot tell from one
 * writing a document's string, and the guard's whole point is that the two are
 * different by construction.
 */
export function runsInsideADistribution(reply: unknown): boolean {
  const envelope = typeof reply === 'object' && reply !== null ? (reply as Record<string, unknown>) : null
  const data = envelope !== null && envelope['ok'] === true ? envelope['data'] : null
  const value =
    typeof data === 'object' && data !== null && !Array.isArray(data) ? (data as Record<string, unknown>) : null
  if (value === null || value['chosen'] !== true) return false
  const distro = value['distro']
  return typeof distro === 'string' && distro !== ''
}

/** literal writes the console's own vocabulary into an element. Both arguments
 * at every call site below are constants of this file: the sentences are the
 * console's, and `screen-literals.test.ts` holds them to it. */
function literal(element: EnvironmentElement, text: string): EnvironmentElement {
  element.textContent = text
  return element
}

/**
 * renderCredentialsSide draws the environment view's statement of which side of
 * the boundary the CLI and its credentials are on, and answers whether it drew
 * anything.
 *
 * It is called by the installation line's own drawing with the raw
 * `installation.current` reply, and it appends to that section: two `<p>`s,
 * each the console's own sentence and neither read from the answer. The
 * installation line's own three lines are drawn by its caller and are not
 * touched here -- this adds a statement and replaces none.
 */
export function renderCredentialsSide(
  parent: EnvironmentElement,
  factory: EnvironmentFactory,
  reply: unknown,
): boolean {
  if (!runsInsideADistribution(reply)) return false
  const side = factory.create('p')
  side.setAttribute('class', ENVIRONMENT_CREDENTIALS_SIDE_CLASS)
  parent.append(side)
  literal(side, ENVIRONMENT_CREDENTIALS_IN_DISTRO)
  const held = factory.create('p')
  held.setAttribute('class', ENVIRONMENT_CREDENTIALS_HELD_CLASS)
  parent.append(held)
  literal(held, ENVIRONMENT_CREDENTIALS_HELD_BY_CONSOLE)
  return true
}
