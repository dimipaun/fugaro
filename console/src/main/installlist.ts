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
//      `gcp_project` and `region`, and a `warnings` that is present and is not an
//      array. Two tolerances are deliberate and are made where they are used: an
//      **absent** optional field reads as the empty value (a row with no
//      `gcp_project`, a document with no `warnings` — in both cases the CLI sent
//      nothing, so nothing is hidden), and `checkouts`, where what is tolerated
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
 */
export interface DiscoveredInstallation {
  readonly name: string
  readonly gcpProject: string
  readonly region: string
  readonly checkouts: readonly string[]
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
  for (const entry of listed) {
    if (!isRecord(entry)) return { ok: false, reason: 'the CLI listed an installation that is not an object' }
    const name = entry['name']
    if (typeof name !== 'string' || name === '' || name.length > MAX_DOCUMENT_STRING) {
      return { ok: false, reason: 'the CLI listed an installation without a usable name' }
    }
    const gcpProject = textField(entry, 'gcp_project')
    const region = textField(entry, 'region')
    if (gcpProject === null || region === null) {
      return { ok: false, reason: `the CLI listed ${JSON.stringify(name)} with a field that is not a string` }
    }
    const checkouts = checkoutsOf(entry)
    if (checkouts === null) {
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
    installations.push(
      Object.freeze({ name, gcpProject, region, checkouts }),
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

/**
 * checkoutsOf reads the optional `checkouts` array: the directories this
 * installation is used from, as the CLI listed them.
 *
 * Each entry is an object (`console/docs/json-contract.md` §6.8: `repository`,
 * `path`, `enrolled`, `valid`, `reason`), and **only an entry with `valid: true`
 * and an absolute `path` is kept** -- the contract's one exception to "nothing in
 * this document is a path the console acts on" is a row the CLI has just
 * re-validated, and a `valid: false` row (any `reason`, including
 * `not_representable`) is never used for anything. Entries of any other shape
 * are dropped, an absent key is an empty list (an older CLI), and the *count* is
 * bounded by `MAX_DISCOVERED_INSTALLATIONS`. What this refuses to do is guess: a
 * kept path is verbatim and is never a path the console went and looked at.
 *
 * **Past the bound it refuses the document, and does not return a short
 * array.** The picker draws one button per checkout this carries
 * (`docs/design/console-styling.md` §2.2), so a truncation here is a screen
 * showing fewer directories than the CLI printed with nothing said about the
 * ones that went -- the silent omission rule 3 forbids. The refusal is the
 * caller's, because only the caller knows which installation it is reading; the
 * `null` this returns for a document past the bound is the same shape
 * `textField` uses, and it is turned into a sentence naming the row.
 */
function checkoutsOf(entry: Record<string, unknown>): readonly string[] | null {
  const value = entry['checkouts']
  if (!Array.isArray(value)) return Object.freeze([])
  if (value.length > MAX_DISCOVERED_INSTALLATIONS) return null
  const checkouts: string[] = []
  for (const entry of value) {
    if (!isRecord(entry) || entry['valid'] !== true) continue
    const candidate = entry['path']
    if (typeof candidate !== 'string') continue
    if (candidate === '' || candidate.length > MAX_DOCUMENT_STRING || !candidate.startsWith('/')) continue
    // eslint-disable-next-line no-control-regex
    if (/[\u0000-\u001f\u007f]/.test(candidate)) continue
    checkouts.push(candidate)
  }
  return Object.freeze(checkouts)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
