// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's two onboarding states (owner's ruling of 2026-10-20; design
// sections 3.4 rules 4 and 7, 6.3).
//
// The owner's ruling is one sentence: **the console asks the operator for
// nothing.** Not the binary, not the config. So a first run is not a form --
// it is a list of the installations the CLI finds on this machine, and, when
// there is nothing to list, one screen that says what to do about it.
//
// There are exactly two of those screens, and this module is the whole of their
// copy:
//
//   1. **no `fugaro` binary** at any of rule 4's locations: "install the CLI
//      first", with the one command that installs it on a Mac
//      (`FUGARO_INSTALL_COMMAND`, which `src/source/cli.ts` already carries --
//      the console's absent-binary screen has offered it since Task 4.4, and
//      `website/data/install.yaml` is the site's own source for it).
//   2. **a binary, and no installation**: "set up your first installation",
//      with the one command that does it (`FUGARO_SETUP_COMMAND`).
//
// Both screens offer the same link, and it is the *one* link this console may
// open: the website's getting-started page, through the external-link path
// `src/main/window.ts` owns (`openExternalValidated`, with its own host
// allow-list). The renderer never holds the URL and never names it: the
// operation that opens it takes **no arguments at all**, so a frame on the
// picker's document cannot ask the app to open a link of its choosing.
//
// The copy is terse and true, as the brief asks: one sentence of what the
// console needs, one command, one link. Nothing here is a promise about what the
// operator's machine has -- the command is *the* install command the project
// documents, and where it cannot be run the link is the way to the page that
// says so.

import { FUGARO_INSTALL_COMMAND } from '../source/cli'
import type { ActiveInstallation } from './installations'
import type { InstallationState } from './installationstate'

/**
 * GETTING_STARTED_URL is the one external link the console opens, and it is the
 * website's own getting-started page: `website/src/pages/getting-started.astro`,
 * which Astro serves at `/getting-started/`. A unit test reads that file, so the
 * URL and the page cannot drift apart.
 *
 * It is opened through `openExternalValidated(shell, ...)`, which checks the
 * scheme, refuses credentials and ports, and holds the host to
 * `ALLOWED_EXTERNAL_HOSTS` in `src/main/window.ts` -- where `fugaro.dev` is now
 * named. That list is the allow-list for the *whole* console: `will-navigate`
 * and `setWindowOpenHandler` still deny everything, so this URL is opened by the
 * OS browser and never in a window this app owns.
 */
export const GETTING_STARTED_URL = 'https://fugaro.dev/getting-started/'

/**
 * FUGARO_SETUP_COMMAND is the one command the "set up your first installation"
 * screen offers, and it is the **only** place the console spells it.
 *
 * `fugaro setup` is the command (`setupDoor`'s `name: "setup"` in
 * `internal/cli/setup.go`; README.md's getting-started steps are written in it).
 * The plan in `docs/setup-wizard/` renamed the command to `fugaro setup` and
 * retired its old spelling, and this constant is the one line that followed it;
 * a unit test pins it to README.md's first step and to the CLI's own
 * declaration, so a later rename cannot be missed.
 */
export const FUGARO_SETUP_COMMAND = 'fugaro setup'

export { FUGARO_INSTALL_COMMAND }

/**
 * OnboardingState is what a screen is drawn for, and it is a small closed union
 * on purpose: the picker draws a list, or it draws one of these two screens.
 *
 * The reason is carried verbatim where there is one: for the absent-binary state
 * it is the resolution's own sentence (`no fugaro binary was found: looked at
 * …`), which names every directory that was searched -- a fact, not a guess
 * about the operator's machine. `searched` is that list as a value, so a screen
 * can draw it without parsing the sentence.
 */
export type OnboardingState =
  | { readonly kind: 'none' }
  | {
      readonly kind: 'install-cli'
      readonly reason: string
      readonly searched: readonly string[]
      readonly command: string
    }
  | { readonly kind: 'first-installation'; readonly command: string }

/** NONE is the state every other screen has: there is something to list. */
export const NO_ONBOARDING: OnboardingState = Object.freeze({ kind: 'none' })

/** installCliState is screen 1: no binary was resolved, and the resolution's own
 * sentence says where it looked. */
export function installCliState(options: {
  readonly reason: string
  readonly searched: readonly string[]
}): OnboardingState {
  return Object.freeze({
    kind: 'install-cli',
    reason: options.reason,
    searched: Object.freeze([...options.searched]),
    command: FUGARO_INSTALL_COMMAND,
  })
}

/** firstInstallationState is screen 2: a binary answered, and the CLI listed no
 * installation at all. */
export function firstInstallationState(): OnboardingState {
  return Object.freeze({ kind: 'first-installation', command: FUGARO_SETUP_COMMAND })
}

// ---------------------------------------------------------------------------
// The list's own shapes
// ---------------------------------------------------------------------------

/**
 * InstallationsCheckoutChoice is one entry of the choosers' menu: the repository
 * the path is a checkout of, the absolute path itself, and how many further
 * directories this installation records for that same repository.
 *
 * `path` is the CLI's own string, verbatim, and it is what the screen sends back
 * through `installation.checkout` -- the main process holds it to this
 * installation's own validated list before anything is written, so a path that
 * did not come from this document is refused rather than used. `more` is the
 * count the label says out loud ("+2 more") and not a second list: the operator
 * picks one directory per repository, and a menu of five paths of one repository
 * would push the other repositories off the menu.
 */
export interface InstallationsCheckoutChoice {
  readonly repository: string
  readonly path: string
  readonly more: number
}

/**
 * InstallationsListRow is one row the picker draws, and it is the merge of two
 * facts: what the CLI listed (`listed`, with the GCP project and region it read)
 * and what this console has recorded about that project (`saved`, `checkout`,
 * `active`).
 *
 * `configUnreadable` is the CLI's own signal and not a guess: `gcp_project` is
 * `""` exactly when that installation's config could not be read
 * (`internal/cli/config_list.go`), and the document carries a warning line
 * saying why. Such a row is **never hidden** -- and `listed` is false for a
 * record the CLI did not list at all, which is the ordinary case for a store
 * written by an older console whose CLI cannot enumerate.
 *
 * **`state` is the rail's mark for this row** (owner's design ruling of
 * 2026-10-25): the issue it carries, whether a click opens it or its issue, the
 * one plain sentence the issue view draws, the command to run, and the CLI's own
 * lines for the disclosure. It is computed in `installationstate.ts` from the
 * facts here plus the console's own, so a screen draws a classification it did
 * not make.
 *
 * **`checkouts` is the list the console may offer**, which is not the CLI's list
 * verbatim: a temporary or tool-owned directory is dropped, and the project's
 * own recorded checkout comes first (`suggestedCheckouts`).
 */
export interface InstallationsListRow {
  readonly project: string
  readonly gcpProject: string
  readonly region: string
  readonly configUnreadable: boolean
  readonly listed: boolean
  readonly saved: boolean
  readonly active: boolean
  readonly mode: 'ok' | 'reduced' | 'refused' | 'unknown'
  readonly checkout?: string
  readonly checkouts: readonly string[]
  /**
   * **`checkoutChoices` is what the dashboard's and the Graph's checkout
   * chooser offers**, one entry per repository the installation records a usable
   * checkout of (owner's ask: "an installation can have multiple repos, I should
   * be able to pick one with a dropdown").
   *
   * It is not `checkouts`: that list is the picker window's one `Use <dir>`
   * button per entry, and it is unchanged. This one is built for a menu -- the
   * rows the CLI re-validated **and** enrolled, with the tool-owned and
   * temporary directories dropped (`offeredCheckouts`), collapsed to one entry
   * per repository with `more` counting the paths that share it -- and the two
   * lists therefore differ exactly where a menu would be wrong: an entry with no
   * repository to label it by, and a repository with several directories to
   * choose between.
   *
   * A stored-only row carries `[]`: the CLI listed nothing for it, so the
   * console has nothing it may offer.
   */
  readonly checkoutChoices: readonly InstallationsCheckoutChoice[]
  /**
   * **`repositories` is what this installation records as its own**, in the
   * CLI's order and each one once: the `<provider>/<owner>/<name>` identity,
   * verbatim, with no path, no base branch and no conversion attached.
   *
   * It is not `checkouts`, and the difference is the point for the one reader
   * that needs it: the Live view's launch control offers these as the
   * repositories a run may be launched in (V2.5.1b), and a launch names a
   * repository rather than a directory. `[]` is a fact -- an installation whose
   * config records none, or a row this console recorded and the CLI did not list
   * -- and the form draws a sentence for it rather than an empty control.
   */
  readonly repositories: readonly string[]
  readonly state: InstallationState
  /**
   * `open` is true for the one row the console is **open on** (`openProject`,
   * `installationstate.ts`): the row the main area is about, which the rail draws
   * as its selected row.
   *
   * It is not `active`. `active` is the operator's remembered choice, which the
   * store holds and this console never rewrites; `open` is this run's decision,
   * and the two differ in exactly the case the owner's ruling names -- the
   * remembered installation cannot be read, so the console opens the first one it
   * can while the chosen one keeps its place and its warning icon in the rail.
   */
  readonly open: boolean
}

/**
 * InstallationsListReply is the whole answer to the picker's `list`: the
 * onboarding state, the rows, the CLI's own warning lines, and the dashboard's
 * view of the active installation (so the picker can say which one the console
 * is reading from).
 */
export interface InstallationsListReply {
  readonly onboard: OnboardingState
  readonly rows: readonly InstallationsListRow[]
  readonly warnings: readonly string[]
  /** The store file's refusal, when `installations.json` could not be read: the
   * picker draws it naming the path, and no write is attempted. */
  readonly refusal?: string
  readonly path: string
  readonly active: ActiveInstallation
}
