// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// `fugaro config list --json` as the console reads it (console/docs/json-contract.md
// §6.8, SECURITY-SENSITIVE: this is the document the installation picker draws).
//
// The read is the CLI's own enumeration of the installations this machine has
// (`internal/cli/config_list.go`, which closes the v1 design's gap 5 — "nothing
// enumerates installations", docs/design/desktop-console.md §4.3). The console
// opens no config file itself (§3.4 rule 7): it spawns the one local read that
// lists them and draws what it printed, so a document that is refused here is
// refused rather than partly believed.
//
// Three rules, and each is a test:
//
//   1. **The schema is closed on the fields the console uses and tolerant of the
//      rest.** A field outside the set below is ignored — the contract's own
//      rule (§2: a field the console does not know is ignored, which is what
//      makes a newer CLI's additions safe) — and a field *inside* it that arrives
//      as the wrong type is a refusal: an installation drawn from a document the
//      console does not understand is a screen that lies. That covers
//      `installations` itself (an array, never a map), each row's `name`,
//      `gcp_project`, `region` and `base_branch`, and a `warnings` that is
//      present and is not an array. Two tolerances are deliberate and are made where they are used: an
//      **absent** optional field reads as the empty value (a row with no
//      `gcp_project`, a document with no `warnings` — in both cases the CLI sent
//      nothing, so nothing is hidden) — and **`base_branch` is the one exception
//      to that rule**, because for it an absent key and an empty value are two
//      different facts (§6.8): absent is an older CLI that does not report base
//      branches, `''` is a config that records none, and the launch draws a
//      different sentence for each. It is read by `branchField` below rather than
//      by `textField` for exactly that reason — and `checkouts`, where what is tolerated
//      is an **element** and not the field: a `checkouts` that is not an array
//      carries no directory the operator could be offered, and an element that is
//      not a non-empty string is not a directory the CLI printed (its own field
//      is a `[]string`), so refusing every installation on the machine over
//      either would hide the rows this read exists to draw. What is **not**
//      tolerated is a length: a row past `MAX_DISCOVERED_INSTALLATIONS`
//      checkouts is a refusal, because the picker draws one button per entry
//      (`docs/design/console-styling.md` §2.2) and a shortened row is a screen
//      that lies by omission.
//   2. **Nothing in the document is a path the console will run.** A row's
//      `checkouts` are offered back to the CLI verbatim when the operator picks
//      one (the CLI's own `--dir`/cwd), never resolved, joined or guessed at
//      here, and the console holds only the ones this document listed.
//   3. **A warning line the CLI printed is shown, never dropped.** The CLI lists
//      an installation whose config could not be read with its fields empty *and*
//      a warning that says why (`config_list.go`'s `Warnings`), and the "never a
//      silent omission" rule is the CLI's own. Every line the CLI printed is
//      drawn: one longer than `MAX_DOCUMENT_STRING` is cut with an ellipsis
//      because the picker's rows are bounded, and the *number* of lines is
//      bounded by `MAX_WARNING_LINES` **visibly** — past that the console appends
//      one more line saying how many it did not keep, so a longer document is
//      bounded and not silently shortened. The one thing dropped without a line
//      of its own is an element that is not a non-empty string, which is not a
//      sentence this console can show and not a line the CLI printed either
//      (`Warnings` is a `[]string`).

/** MAX_DISCOVERED_INSTALLATIONS bounds what the console will hold from one
 * document. It is far above a real machine's count (the CLI enumerates the
 * project configs under one directory) and it is here so that a document cannot
 * make the picker's rows unbounded, exactly as `MAX_INSTALLATIONS` bounds the
 * store. */
export const MAX_DISCOVERED_INSTALLATIONS = 256

/** MAX_WARNING_LINES and MAX_DOCUMENT_STRING bound the two free-text parts: a
 * warning is a sentence a human listing prints, and a name is a project name. */
export const MAX_WARNING_LINES = 64
export const MAX_DOCUMENT_STRING = 4096

/**
 * DiscoveredInstallation is one installation the CLI listed: its project name,
 * its GCP project and its region.
 *
 * `gcpProject` and `region` are `''` when the CLI could not read that
 * installation's config — which is a state and not an absence: the entry is
 * still listed (its name is a fact of the directory) and one of the document's
 * warnings says why. The picker draws such a row with the console's own word for
 * it rather than an empty cell, and never hides it.
 *
 * `checkouts` is the optional per-installation array a newer CLI may add (the
 * setup-wizard work lists the checkouts an installation is used from). It is
 * **optional and tolerated absent**: an older CLI does not have the field at all
 * (§6.8's version-skew note), and the picker offers the entries it does have as
 * a way to give this installation a working directory. Nothing here reads,
 * resolves or validates a filesystem path.
 *
 * `baseBranch` and `repositories` are the recorded base branch, which is the
 * branch an installation's runs are cut from: the installation's own value, and
 * each repository's own with the installation's below it (design §5.1 rule 11 —
 * the window shows the branch the run is cut from, and a launch has no checkout
 * for the CLI's order to read it from). **`null` is a key the CLI did not print
 * at all** — an older CLI, which does not report base branches — and `''` is a
 * key that is there and records none. §6.8 keeps those two apart on purpose, and
 * so does this reader: reading an older CLI's silence as `''` would send an
 * operator to set a setting they have already set.
 */
export interface DiscoveredInstallation {
  readonly name: string
  readonly gcpProject: string
  readonly region: string
  /** the installation's own recorded base branch; `null` when the CLI does not
   * report base branches at all, `''` when its config records none */
  readonly baseBranch: string | null
  /** every checkout row's repository and its recorded base branch */
  readonly repositories: readonly DiscoveredRepository[]
  readonly checkouts: readonly string[]
  /**
   * `checkoutChoices` is the same rows as `checkouts`, **paired with the
   * repository each path was recorded under** and held to both of the CLI's
   * verdicts (`valid` *and* `enrolled`): it is what the dashboard's and the
   * Graph's checkout chooser draws its menu from.
   *
   * It is not `checkouts`, and the two differences are the point. `checkouts` is
   * the older list the picker window draws one `Use <dir>` button per entry from,
   * and it is kept exactly as it was. This one exists because a menu entry has to
   * say **which repository** a directory is a checkout of (owner's ask: "an
   * installation can have multiple repos, I should be able to pick one with a
   * dropdown"), and a row the CLI did not re-validate is not a directory the
   * console may offer at all (design §3.4 rule 7).
   */
  readonly checkoutChoices: readonly DiscoveredCheckout[]
}

/**
 * DiscoveredCheckout is one menu entry the choosers offer: the repository the CLI
 * recorded the path under, and the path itself.
 *
 * Both are the CLI's own strings and neither is resolved, joined or looked up
 * here (§6.8). The repository is what the entry is **labelled** with and the path
 * is what is **sent back** -- `installation.checkout` takes a directory the main
 * process then holds to this installation's own list, so the console never
 * invents one.
 */
export interface DiscoveredCheckout {
  readonly repository: string
  readonly path: string
}

/**
 * DiscoveredRepository is one repository an installation records, and the base
 * branch its runs are cut from: the CLI's `checkouts[]` row's `repository` and
 * `base_branch`, kept as the pair the launch needs (§6.8).
 *
 * It is read off **every** row the CLI printed, whatever that row's `valid` is:
 * which branch a repository's runs are cut from is a fact of the project config,
 * not of the directory a path named, and a row whose path has been deleted still
 * records it. `repository` is the CLI's own `<provider>/<owner>/<name>` and is
 * never resolved, joined or looked up here — the launch's own conversion is what
 * admits it or refuses it. `baseBranch` is `null` when the row carries no
 * `base_branch` key at all (an older CLI) and `''` when the key is there and the
 * config records none for that repository, in which case the installation's own
 * `baseBranch` is the layer below it. The two are different facts and a reader
 * must not merge them (§6.8).
 */
export interface DiscoveredRepository {
  readonly repository: string
  readonly baseBranch: string | null
}

/** InstallationsDocument is the whole read: the installations, in the CLI's own
 * order, and the lines it warned about. Both are seeded by the CLI ("an empty
 * array is a fact and a null is not", §1), so an empty document here means the
 * machine has none — which is the state the "set up your first installation"
 * screen is drawn for. */
export interface InstallationsDocument {
  readonly installations: readonly DiscoveredInstallation[]
  readonly warnings: readonly string[]
}

/** InstallationsParse is the parse's answer: the document, or the one sentence
 * saying why this text is not one. */
export type InstallationsParse =
  | { readonly ok: true; readonly document: InstallationsDocument }
  | { readonly ok: false; readonly reason: string }

/**
 * CLI_PROJECT_NAME is the CLI's own grammar for a project name
 * (`config.ProjectNameRE`, `internal/config/project.go`). A name outside it is
 * not one the CLI writes a config for, and the console builds commands from the
 * name (`fugaro doctor --project <name>` in the issue view), so a row whose name
 * is not this shape is not used at all (review m3).
 */
const CLI_PROJECT_NAME = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/

export function isCliProjectName(name: string): boolean {
  return CLI_PROJECT_NAME.test(name)
}

/**
 * parseInstallationsDocument reads the printed document. It is a *document*
 * parser and not a decoder for a payload: the text arrived from the CLI's stdout
 * through `runToCompletion`, and everything here is about refusing a shape the
 * console cannot draw rather than about recovering from it.
 */
export function parseInstallationsDocument(text: string): InstallationsParse {
  let value: unknown
  try {
    value = JSON.parse(text) as unknown
  } catch {
    return { ok: false, reason: 'the CLI’s installation list is not JSON' }
  }
  if (!isRecord(value)) return { ok: false, reason: 'the CLI’s installation list is not a JSON object' }
  const listed = value['installations']
  if (!Array.isArray(listed)) {
    return { ok: false, reason: 'the CLI’s installation list has no `installations` array' }
  }
  if (listed.length > MAX_DISCOVERED_INSTALLATIONS) {
    return { ok: false, reason: `the CLI listed more than ${String(MAX_DISCOVERED_INSTALLATIONS)} installations` }
  }
  const installations: DiscoveredInstallation[] = []
  const unnamed: string[] = []
  for (const entry of listed) {
    if (!isRecord(entry)) return { ok: false, reason: 'the CLI listed an installation that is not an object' }
    const name = entry['name']
    if (typeof name !== 'string' || name === '' || name.length > MAX_DOCUMENT_STRING) {
      return { ok: false, reason: 'the CLI listed an installation without a usable name' }
    }
    if (!isCliProjectName(name)) {
      // Named rather than dropped in silence (rule 3): the line says which row
      // the console left out and why.
      unnamed.push(`the CLI listed ${JSON.stringify(name)}, which is not a project name, so the console will not use it`)
      continue
    }
    const gcpProject = textField(entry, 'gcp_project')
    const region = textField(entry, 'region')
    const baseBranch = branchField(entry, 'base_branch')
    if (gcpProject === null || region === null || baseBranch.kind === 'unreadable') {
      return { ok: false, reason: `the CLI listed ${JSON.stringify(name)} with a field that is not a string` }
    }
    const rows = checkoutsOf(entry)
    if (rows.kind === 'too-many') {
      // Rule 3, one row down: the bound is kept and the omission is *named* --
      // here the count is a refusal rather than a shortened row, because a row
      // is what the picker draws its buttons from, and a row missing half its
      // directories is a screen that lies about the installation.
      return {
        ok: false,
        reason:
          `the CLI listed more than ${String(MAX_DISCOVERED_INSTALLATIONS)} checkouts for ` +
          `${JSON.stringify(name)}, which this console will not draw`,
      }
    }
    if (rows.kind === 'bad-branch') {
      // The base branch is one of the values the launch reads off this row and
      // a screen shows: a row carrying something that is not text is a document
      // this console does not understand, and reading it as "unrecorded" would
      // silently move the branch the window names.
      return {
        ok: false,
        reason: `the CLI listed a checkout of ${JSON.stringify(name)} with a \`base_branch\` that is not a string`,
      }
    }
    installations.push(
      Object.freeze({
        name,
        gcpProject,
        region,
        baseBranch: baseBranch.kind === 'absent' ? null : baseBranch.value,
        repositories: rows.repositories,
        checkouts: rows.paths,
        checkoutChoices: rows.choices,
      }),
    )
  }
  const warned = value['warnings']
  if (warned !== undefined && !Array.isArray(warned)) {
    // Rule 1: `warnings` is a field this console draws, so a value of another
    // type is a document it does not understand. Reading it as "no warnings"
    // would hide whatever the CLI put in it -- the one direction rule 3 forbids.
    return { ok: false, reason: 'the CLI’s installation list has `warnings` that are not an array' }
  }
  const warnings: string[] = []
  let dropped = 0
  if (Array.isArray(warned)) {
    for (const line of warned) {
      // A warning that is not a string is not a sentence this console can show;
      // it is dropped here rather than drawn as `[object Object]`. It is not a
      // warning the CLI *printed* either (`Warnings` is a `[]string`), so this
      // is not the omission rule 3 is about.
      if (typeof line !== 'string' || line === '') continue
      if (warnings.length >= MAX_WARNING_LINES) {
        dropped += 1
        continue
      }
      warnings.push(line.length > MAX_DOCUMENT_STRING ? `${line.slice(0, MAX_DOCUMENT_STRING)}…` : line)
    }
  }
  // The bound is kept and the omission is named, which is the whole of rule 3:
  // a document with more warning lines than the console draws says so, rather
  // than arriving on a screen shortened without a word.
  if (dropped > 0) warnings.push(droppedWarningLines(dropped))
  warnings.push(...unnamed)
  return { ok: true, document: Object.freeze({ installations: Object.freeze(installations), warnings: Object.freeze(warnings) }) }
}

/** droppedWarningLines is the one line the console adds in place of the warnings
 * past `MAX_WARNING_LINES`: it carries the count and nothing else -- no warning
 * text is paraphrased, and nothing the console did drop is described as if it
 * had been read. */
function droppedWarningLines(count: number): string {
  return `the CLI printed ${String(count)} more warning lines, which this console did not keep`
}

/** textField reads one string field of an entry, tolerating its absence (a
 * newer CLI need not carry it) and refusing a value of another type -- a number
 * where a project id belongs is a document this console does not understand --
 * which it reports as `null` rather than throwing: the caller turns that into
 * the refusal, so the field's name reaches the sentence a screen shows. */
function textField(entry: Record<string, unknown>, key: string): string | null {
  const value = entry[key]
  if (value === undefined || value === null) return ''
  if (typeof value !== 'string') return null
  return value.length > MAX_DOCUMENT_STRING ? value.slice(0, MAX_DOCUMENT_STRING) : value
}

/** BranchField is one `base_branch` key's three states, and they are three
 * different facts. `absent` is a key the CLI did not print at all — an older
 * CLI, which does not report base branches (§6.8) — `branch` with `""` is a key
 * that is there and records none, and `unreadable` is the key with a value of
 * another type, which the caller refuses the document for. `textField` cannot
 * be used for this one: it reads an absent key as `""`, which is the merge §6.8
 * forbids and which would tell an operator to set a setting they have already
 * set. */
type BranchField =
  | { readonly kind: 'absent' }
  | { readonly kind: 'branch'; readonly value: string }
  | { readonly kind: 'unreadable' }

function branchField(entry: Record<string, unknown>, key: string): BranchField {
  const value = entry[key]
  if (value === undefined) return { kind: 'absent' }
  if (typeof value !== 'string') return { kind: 'unreadable' }
  // A value past the document bound is `unreadable` rather than cut down to it:
  // this string is what the launch is cut from and what the confirmation window
  // shows, so a silently shortened one would name a branch the run does not use
  // -- the same lie `textField` avoids by only ever cutting a *warning* line,
  // which is display text and nothing else.
  if (value.length > MAX_DOCUMENT_STRING) return { kind: 'unreadable' }
  return { kind: 'branch', value }
}

/** CheckoutRows is what one installation's `checkouts` array yields: the two
 * lists the console keeps, or why the document is refused. The refusal is a
 * value rather than a sentence because only the caller knows which installation
 * it is reading and can name it in the sentence a screen shows. */
type CheckoutRows =
  | {
      readonly kind: 'rows'
      readonly paths: readonly string[]
      readonly repositories: readonly DiscoveredRepository[]
      readonly choices: readonly DiscoveredCheckout[]
    }
  /** more rows than the console will hold from one document */
  | { readonly kind: 'too-many' }
  /** a row whose recorded base branch is present and is not text */
  | { readonly kind: 'bad-branch' }

/**
 * checkoutsOf reads the optional `checkouts` array once, for both of the things
 * this console needs from it: the directories this installation is used from,
 * and the repository records the launch reads a recorded base branch from.
 *
 * Each entry is an object (`console/docs/json-contract.md` §6.8: `repository`,
 * `path`, `base_branch`, `enrolled`, `valid`, `reason`). **A path is kept only
 * from an entry with `valid: true`, an absolute `path` and no control
 * character** -- the contract's one exception to "nothing in this document is a
 * path the console acts on" is a row the CLI has just re-validated, and a
 * `valid: false` row (any `reason`, including `not_representable`) is never used
 * for anything. A **repository record** is kept from every entry that names one,
 * whatever its `valid` is, because the base branch is a fact of the project
 * config and not of the directory the path named. A **menu entry**
 * (`checkoutChoices`) is kept only from a row carrying `valid: true` *and*
 * `enrolled: true` and naming a repository: it is what the choosers draw, and
 * an entry that cannot say which repository a directory is a checkout of is not
 * one an operator can pick between. Entries of any other shape are
 * dropped, an absent key is an empty list (an older CLI), and the *count* is
 * bounded by `MAX_DISCOVERED_INSTALLATIONS`. What this refuses to do is guess: a
 * kept path is verbatim, and neither list is ever resolved, joined or looked at.
 *
 * **Past the bound it refuses the document, and does not return a short
 * array.** The picker draws one button per checkout this carries
 * (`docs/design/console-styling.md` §2.2), so a truncation here is a screen
 * showing fewer directories than the CLI printed with nothing said about the
 * ones that went -- the silent omission rule 3 forbids. The same goes for a
 * repository record: a launch that offered fewer repositories than the
 * installation records would be a launch the operator cannot make.
 *
 * The one field of a row that is *refused* rather than dropped is `base_branch`
 * (`branchField`'s rule for a field this console reads): the value is shown in
 * the launch window as the branch the run is cut from, so a row carrying
 * something that is not text is a document this console does not understand, and
 * reading it as "unrecorded" would move that branch in silence. The key's
 * *absence* is not that: it is kept as `null`, the one value that says this CLI
 * does not report base branches at all.
 */
function checkoutsOf(entry: Record<string, unknown>): CheckoutRows {
  const value = entry['checkouts']
  if (!Array.isArray(value)) {
    return { kind: 'rows', paths: Object.freeze([]), repositories: Object.freeze([]), choices: Object.freeze([]) }
  }
  if (value.length > MAX_DISCOVERED_INSTALLATIONS) return { kind: 'too-many' }
  const paths: string[] = []
  const repositories: DiscoveredRepository[] = []
  const choices: DiscoveredCheckout[] = []
  for (const row of value) {
    if (!isRecord(row)) continue
    const branch = branchField(row, 'base_branch')
    if (branch.kind === 'unreadable') return { kind: 'bad-branch' }
    const repository = row['repository']
    if (typeof repository === 'string' && repository !== '' && repository.length <= MAX_DOCUMENT_STRING) {
      repositories.push(Object.freeze({ repository, baseBranch: branch.kind === 'absent' ? null : branch.value }))
    }
    if (row['valid'] !== true) continue
    const candidate = row['path']
    if (typeof candidate !== 'string') continue
    if (candidate === '' || candidate.length > MAX_DOCUMENT_STRING || !candidate.startsWith('/')) continue
    // eslint-disable-next-line no-control-regex
    if (/[\u0000-\u001f\u007f]/.test(candidate)) continue
    paths.push(candidate)
    // The choosers' entry is the stricter one, and both halves of the CLI's
    // verdict are required: `valid` is the path's own and `enrolled` is the
    // project's, so a row that fails either is not a directory this console may
    // offer (design §3.4 rule 7, §6.8). `enrolled` is read here and not in
    // `paths`, which the picker window's own buttons are drawn from and which
    // this change leaves exactly as it was.
    if (row['enrolled'] !== true) continue
    if (typeof repository !== 'string' || repository === '' || repository.length > MAX_DOCUMENT_STRING) continue
    choices.push(Object.freeze({ repository, path: candidate }))
  }
  return {
    kind: 'rows',
    paths: Object.freeze(paths),
    repositories: Object.freeze(repositories),
    choices: Object.freeze(choices),
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
