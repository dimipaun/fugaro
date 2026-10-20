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
//      console does not understand is a screen that lies.
//   2. **Nothing in the document is a path the console will run.** A row's
//      `checkouts` are offered back to the CLI verbatim when the operator picks
//      one (the CLI's own `--dir`/cwd), never resolved, joined or guessed at
//      here, and the console holds only the ones this document listed.
//   3. **A warning line is shown, never dropped.** The CLI lists an installation
//      whose config could not be read with its fields empty *and* a warning that
//      says why (`config_list.go`'s `Warnings`), and the "never a silent
//      omission" rule is the CLI's own — this module keeps every line and bounds
//      only its length.

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
    installations.push(
      Object.freeze({ name, gcpProject, region, checkouts: checkoutsOf(entry) }),
    )
  }
  const warned = value['warnings']
  const warnings: string[] = []
  if (Array.isArray(warned)) {
    for (const line of warned.slice(0, MAX_WARNING_LINES)) {
      // A warning that is not a string is not a sentence this console can show;
      // it is dropped here rather than drawn as `[object Object]`. What is *not*
      // dropped is a warning the CLI printed.
      if (typeof line !== 'string' || line === '') continue
      warnings.push(line.length > MAX_DOCUMENT_STRING ? `${line.slice(0, MAX_DOCUMENT_STRING)}…` : line)
    }
  }
  return { ok: true, document: Object.freeze({ installations: Object.freeze(installations), warnings: Object.freeze(warnings) }) }
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
 * The field is a later addition (the setup-wizard work), so its shape is
 * tolerated rather than assumed: entries that are not non-empty strings are
 * dropped, and the *count* is bounded by `MAX_DISCOVERED_INSTALLATIONS`. What
 * this refuses to do is guess: an entry is kept verbatim and is never a path the
 * console went and looked at.
 */
function checkoutsOf(entry: Record<string, unknown>): readonly string[] {
  const value = entry['checkouts']
  if (!Array.isArray(value)) return Object.freeze([])
  const checkouts: string[] = []
  for (const candidate of value.slice(0, MAX_DISCOVERED_INSTALLATIONS)) {
    if (typeof candidate !== 'string') continue
    if (candidate === '' || candidate.length > MAX_DOCUMENT_STRING) continue
    // eslint-disable-next-line no-control-regex
    if (/[\u0000-\u001f\u007f]/.test(candidate)) continue
    checkouts.push(candidate)
  }
  return Object.freeze(checkouts)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
