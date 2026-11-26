// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The setup wizard's confirmation window content (the console-improvements plan,
// task SW3a: D2904, O6/F27; design section 5.1 rules 11 and 12).
//
// **What this module is.** The window the operator confirms a setup run in is
// `confirm.ts`'s, one implementation for every question the console asks
// (D1115, D2902): this is what that window is **shown** for a first setup. It is
// a pure function with no Electron, no clock and no child in it, the way
// `settingsview.ts`'s and `launch.ts`'s view builders are, so the whole of what
// the operator reads is testable under plain Node.
//
// **What it carries is decided in `wizardrun.ts`, and this module only spells
// it.** The answers **as main parsed them** (never as the renderer sent them),
// the SHA-256 of the exact bytes the child is given on its stdin, the two
// command lines field by field, and the four sentences below. Nothing here
// computes a value, compares anything or decides anything: a second spelling of
// these fields would be a second thing to keep in step with the flow that holds
// the bytes.
//
// **The four sentences are the honest account of what a yes does**, and they are
// constants of this file rather than of the renderer because the console's
// sentences are the main process's (a screen draws what it was handed):
//
//   - `setup --json` confirms no step (`unconfirmedWhy`, `internal/cli/init.go`;
//     F27), so apply runs **only** what the CLI applies without a confirmation;
//   - every step that needs a person -- creating the GCP project, linking its
//     billing account, a Terraform review in a managed project, every secret
//     value -- arrives from the child as a `left_for_you` event with its own
//     line to run in the owner's terminal;
//   - this window's yes is therefore the operator's **go to run the CLI**, and
//     never a consent the CLI accepts (O6: no consent mechanism is planned, and
//     a coding agent's own command line must not be able to pass one);
//   - the Terraform plan is recomputed at apply, so what apply does can differ
//     from the plan the operator has just read.
//
// The window says all four **before** it asks, which is the whole reason they
// are drawn in it rather than in a screen behind it.

import type { SetupRigor } from '../shared/new-ops'

/**
 * SETUP_CONFIRM_PREFIX is how a setup window's subject identity is spelled, the
 * way `settingsConfirmID` spells a settings edit's: the flow that opens one owns
 * the identity, and `confirmSubjectID` (`confirm.ts`) is the one reader. The
 * project is in it rather than a counter because the answers name the
 * installation the run stands up, which is the fact an operator watching two
 * windows would tell them apart by.
 */
export const SETUP_CONFIRM_PREFIX = 'setup:'

/** setupConfirmID is one setup window's identity: the project its answers name. */
export function setupConfirmID(project: string): string {
  return SETUP_CONFIRM_PREFIX + project
}

/** SETUP_NOTE_NO_CONFIRMATION is what a yes may run, exactly: `--json` confirms
 * no step, so the child applies only the steps the CLI applies without a
 * confirmation and hands every other one back. */
export const SETUP_NOTE_NO_CONFIRMATION =
  'fugaro setup --json confirms no step: this runs only the steps the CLI applies without a confirmation.'

/** SETUP_NOTE_LEFT_FOR_YOU is the other half of the same fact: everything that
 * needs a person comes back as a line, and the window names them so that a run
 * which ends "leaving steps for you" is not read as a failure. */
export const SETUP_NOTE_LEFT_FOR_YOU =
  'creating the GCP project, linking its billing account, a Terraform review in a managed project and every secret value need a person: each one comes back as a line to run in your own terminal.'

/** SETUP_NOTE_GO is what this window's yes is and is not (O6). */
export const SETUP_NOTE_GO =
  'this yes is your go to run the CLI; it is not a consent the CLI accepts.'

/** SETUP_NOTE_REPLAN is the recomputation, said before it happens: `--plan-only`
 * read one plan, and apply takes the Terraform plan again. */
export const SETUP_NOTE_REPLAN =
  'the Terraform plan is taken again at apply, so what apply does can differ from the plan above.'

/** SETUP_NOTES is the four of them, in the order the window draws them: what a
 * yes runs, what it leaves, what the yes is, and what is recomputed. */
export const SETUP_NOTES: readonly string[] = Object.freeze([
  SETUP_NOTE_NO_CONFIRMATION,
  SETUP_NOTE_LEFT_FOR_YOU,
  SETUP_NOTE_GO,
  SETUP_NOTE_REPLAN,
])

/** SETUP_BAKE_YES, SETUP_BAKE_NO and SETUP_BAKE_DEFAULT are the three words the
 * bake answer is drawn as. The wire carries a boolean and the window draws a
 * word: a row saying `true` is a row an operator has to translate.
 *
 * The third word is the one that matters. A switch nobody answered is not a
 * "no": `bake_repos` absent means **yes** to every reader of the field (only a
 * written `false` turns the bake off -- `internal/imagebake`'s plan,
 * `internal/cli/init.go`'s `carryBakeAnswer`), so the window has to say what
 * happens and not what was left unsaid. Drawing it as `no` would tell an
 * operator their repositories stay out of the image while the run bakes them in. */
export const SETUP_BAKE_YES = 'yes'
export const SETUP_BAKE_NO = 'no'
export const SETUP_BAKE_DEFAULT = 'not answered: the CLI bakes'

/**
 * SetupView is the whole of what the setup window is shown, and it is a **copy**
 * rather than the held answers: the flow holds the answers' bytes and their
 * digest, the child's working directory and the spawn plan, and a window is the
 * one place in this app that is not the main process. Everything here is a
 * string or an array of strings, and the whole view is deep-frozen, so what the
 * window reads cannot be changed under it between the paint and the answer --
 * which matters most for the digest: a window that could rewrite it would be a
 * window whose yes is checked against nothing.
 *
 * The answers are the ones main **parsed and validated** (`wizardrun.ts`), and
 * not the object the renderer sent: a row drawn from the renderer's own
 * spelling would be a window that shows what was asked for rather than what will
 * happen, and the whole point of this window is that the operator reads the
 * second. This window carries five of the six answers main parses: the budget
 * alert (D2210) is asked, parsed and sent, and has no row here yet, so what it
 * is set to is drawn in the form and in the digest rather than in the window.
 * `planArgv` and `applyArgv` are the two commands field by field, program word
 * included, so the window shows what is spawned and not a summary of it (rule
 * 11's own promise).
 */
export interface SetupView {
  /** the union's discriminant, for `ConfirmView`'s reason: the window is one
   * implementation for every question and the document it serves has to know
   * which one it is drawing without guessing from the fields it can see. */
  readonly kind: 'setup'
  readonly project: string
  readonly region: string
  readonly name: string
  readonly rigor: SetupRigor
  readonly bake: string
  /** the SHA-256 of the exact bytes the child is given on its stdin, in
   * lowercase hex. It is the *plan's* digest: apply re-checks the held bytes
   * against it, so a buffer that changed under the window is refused instead of
   * run. */
  readonly digest: string
  readonly planArgv: readonly string[]
  readonly applyArgv: readonly string[]
  readonly notes: readonly string[]
}

/** SetupViewFields is what `setupView` is built from: the answer rows it draws
 * -- five of the six main parsed (above) -- the digest of the held bytes, and
 * the two command lines. It is an argument object rather than eight positional
 * parameters because a mis-ordered pair of strings here is a window that names
 * the wrong project with no way to see it. */
export interface SetupViewFields {
  readonly project: string
  readonly region: string
  readonly name: string
  readonly rigor: SetupRigor
  /** the bake switch in its **three** states: `true` and `false` are the
   * operator's own answer, and `undefined` is a switch nobody answered, which
   * the CLI reads as yes (`SETUP_BAKE_DEFAULT`). */
  readonly bakeRepos: boolean | undefined
  readonly digest: string
  readonly planArgv: readonly string[]
  readonly applyArgv: readonly string[]
}

/** setupView is the one conversion from the parsed answers and the held bytes to
 * what the window is shown, and it is a pure function of its argument for
 * `confirmView`'s reason: a second spelling of these fields would be a second
 * thing to keep in step. */
export function setupView(fields: SetupViewFields): SetupView {
  return Object.freeze({
    kind: 'setup',
    project: fields.project,
    region: fields.region,
    name: fields.name,
    rigor: fields.rigor,
    bake:
      fields.bakeRepos === undefined
        ? SETUP_BAKE_DEFAULT
        : fields.bakeRepos
          ? SETUP_BAKE_YES
          : SETUP_BAKE_NO,
    digest: fields.digest,
    planArgv: Object.freeze([...fields.planArgv]),
    applyArgv: Object.freeze([...fields.applyArgv]),
    notes: SETUP_NOTES,
  })
}
