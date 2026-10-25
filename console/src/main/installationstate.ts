// What the console knows about one installation, and what the rail draws for
// it (owner's design ruling of 2026-10-25: the console never shows a picker
// screen; every discovered installation is a row in the installation rail).
//
// The rail draws **every** installation, readable or not, and an installation
// the console cannot open carries a warning mark beside its name. This module is
// the whole of that decision, and it is here -- in the main process, under plain
// Node -- rather than in the renderer, for the reason every other decision in
// this console is: a renderer that classified its own rows would be a second
// answer to one question, and the one a test cannot reach.
//
// **Four things make an installation unopenable, and they are the owner's
// four**: its config is refused by the CLI, the console's own store file is
// unreadable, the CLI itself is missing (so nothing can be driven), or its
// shared config is stale. The first three are *refusals* -- the console cannot
// open that installation at all -- and the fourth is a **warning**: the CLI read
// the installation and returned its fields, so the installation still opens,
// and the owner's ruling says so in as many words ("still open that installation
// if the reader returned its fields").
//
// So an issue is two facts and not one: `issue` is what the rail marks, and
// `readable` is whether a click opens the installation or its issue. They differ
// in exactly one case, `stale`, which is why they are two fields rather than a
// predicate over one.
//
// **A sentence, and a command.** The issue view opens with one plain sentence --
// what is wrong, and the exact command to run -- and the CLI's own text sits
// behind a disclosure. The sentence is composed here, from the CLI's own words
// where there are any (the refusal's path and reason, the resolution's own
// sentence) and from this console's own short advice otherwise. Nothing is
// paraphrased into a claim the console cannot support: every command below is
// one this repository documents, and where there is no command to run the
// sentence says what to edit instead.

/** InstallationIssue is what the rail marks beside one installation's name. */
export type InstallationIssue = 'none' | 'store' | 'config' | 'binary' | 'stale'

/**
 * InstallationState is one row's state: the mark, whether a click opens the
 * installation, and the two strings the issue view draws.
 *
 * `command` is `''` when there is no command to run -- a store file is the
 * operator's own to edit and no CLI command repairs it -- and the sentence then
 * carries the instruction on its own.
 */
export interface InstallationState {
  readonly issue: InstallationIssue
  readonly readable: boolean
  readonly sentence: string
  readonly command: string
  /** details is the CLI's own text for this installation, verbatim, for the
   * disclosure. Empty when the CLI printed no line about it. */
  readonly details: readonly string[]
}

/** NONE is the ordinary state: the installation opens and nothing is wrong. */
export const NO_ISSUE: InstallationState = Object.freeze({
  issue: 'none',
  readable: true,
  sentence: '',
  command: '',
  details: Object.freeze([]),
})

/** RowFacts is the part of a list row this module reads. It is deliberately the
 * *facts* and not the row type: the row carries the state this function
 * computes, and a type that took itself as input would be circular. */
export interface RowFacts {
  readonly project: string
  /** `listed` is false for a record the CLI did not enumerate at all. */
  readonly listed: boolean
  /** `configUnreadable` is the CLI's own signal: `gcp_project` is `""` exactly
   * when that installation's config could not be read. */
  readonly configUnreadable: boolean
}

/** ConsoleFacts is what the console knows beyond one row. */
export interface ConsoleFacts {
  /** storeRefusal is the store file's refusal, or `''`. */
  readonly storeRefusal: string
  readonly storePath: string
  /** cliRefusal is the resolution's own sentence when no `fugaro` was found at
   * any of rule 4's locations, or `''`. */
  readonly cliRefusal: string
  /** installCommand is the one command that installs the CLI. */
  readonly installCommand: string
  /** warnings is every line the CLI's list document carried. */
  readonly warnings: readonly string[]
}

/** MAX_DETAIL_LINES bounds one row's disclosure, so a document that repeats
 * itself cannot make one row's issue unbounded. */
export const MAX_DETAIL_LINES = 64

/**
 * classifyInstallation is the whole of the rail's mark for one row.
 *
 * The order is the order of the refusals' reach: a store the console cannot read
 * means it can open **nothing** (a choose writes that file, and the registry
 * refuses every write while it is unreadable), and a missing CLI means it can
 * drive **nothing**. Both are therefore marks on every row rather than on one,
 * which is what the owner's ruling asks for in listing them among the reasons an
 * installation "cannot be read".
 *
 * A row's own config is the third refusal, and it is the CLI's signal and not
 * this console's guess: the CLI lists an installation whose config it could not
 * read with its fields empty and a warning line saying why. The staleness is the
 * fourth mark and the only warning: the CLI read the installation and returned
 * its fields, and its own sentence says the shared copy it merged is older than
 * the reader refreshes it within.
 */
export function classifyInstallation(row: RowFacts, facts: ConsoleFacts): InstallationState {
  const details = detailsFor(row.project, facts.warnings)
  if (facts.storeRefusal !== '') {
    return Object.freeze({
      issue: 'store',
      readable: false,
      sentence:
        `the console cannot read ${facts.storePath}: ${facts.storeRefusal}. ` +
        `Fix or remove that file and reopen the console.`,
      command: '',
      details: Object.freeze(details),
    })
  }
  if (facts.cliRefusal !== '') {
    return Object.freeze({
      issue: 'binary',
      readable: false,
      sentence: `${facts.cliRefusal}. Install it with ${facts.installCommand}.`,
      command: facts.installCommand,
      details: Object.freeze(details),
    })
  }
  if (row.listed && row.configUnreadable) {
    return Object.freeze({
      issue: 'config',
      readable: false,
      sentence: configSentence(row.project, details),
      command: DOCTOR_COMMAND,
      details: Object.freeze(details),
    })
  }
  if (staleLineFor(row.project, facts.warnings) !== undefined) {
    return Object.freeze({
      issue: 'stale',
      readable: true,
      sentence:
        `${row.project}'s shared config is stale, so the console is reading the copy this machine has. ` +
        `Refresh it with ${DOCTOR_COMMAND}.`,
      command: DOCTOR_COMMAND,
      details: Object.freeze(details),
    })
  }
  return details.length === 0 ? NO_ISSUE : Object.freeze({ ...NO_ISSUE, details: Object.freeze(details) })
}

/**
 * DOCTOR_COMMAND is the one command the console offers for the two issues a
 * command can fix, and it is `fugaro doctor` because that is what runs the read
 * behind both of them: `doctor` is the command that calls `sharedFetch`
 * (`internal/cli/project.go`, `internal/cli/doctor.go`), so it is what refreshes
 * the cached shared config the staleness warning is about, and it is the check
 * that reports a project config this machine cannot read. The console offers no
 * command of its own invention: the CLI's own sentence for a refused config is
 * printed by `doctor` verbatim.
 */
export const DOCTOR_COMMAND = 'fugaro doctor'

/**
 * CONFIG_REFUSAL_MARKER is the CLI's own wording for a project config it could
 * not read -- `config_list.go` prints `"%s could not be read: %v"` with the path
 * first -- and it is matched so the console can lift the path out of the CLI's
 * line rather than guess at one: the document itself carries no config path, and
 * a path the console composed would be a path it did not obtain.
 */
const CONFIG_REFUSAL_MARKER = ' could not be read: '

/**
 * STALE_MARKER is the second half of the CLI's staleness sentence
 * (`config_list.go`), which is `"%s's shared config was last checked %s ago,
 * over the %s the reader refreshes it within"`. The line is recognised by the
 * installation's own name *and* this marker rather than by the marker alone: the
 * sentence is about one installation's shared copy, and a mark on the wrong row
 * would send an operator to the wrong project.
 */
const STALE_MARKER = ' the reader refreshes it within'

/** SHARED_STALE_LEAD is what the staleness line starts with, name included. */
function sharedStaleLead(project: string): string {
  return `${project}'s shared config was last checked `
}

/**
 * configSentence is the issue view's first line for a refused config: the path
 * and the reason are the CLI's own words (lifted out of its warning line when it
 * printed one), and the advice is this console's.
 *
 * The owner's example is this shape -- `edit <path>: <what is wrong> and run
 * <command> in each repository checkout` -- and the command here is `doctor`,
 * which re-reads the same file and prints the same refusal with its own fix
 * line. The console does not invent a fix per refusal: the one command that
 * names the file the CLI could not read is the CLI's own check.
 */
function configSentence(project: string, details: readonly string[]): string {
  const line = details.find((candidate) => candidate.includes(CONFIG_REFUSAL_MARKER))
  if (line === undefined) {
    return `the config for ${project} could not be read. Fix it and run ${DOCTOR_COMMAND} in each repository checkout.`
  }
  const cut = line.indexOf(CONFIG_REFUSAL_MARKER)
  const path = line.slice(0, cut)
  const reason = line.slice(cut + CONFIG_REFUSAL_MARKER.length)
  return `edit ${path}: ${reason}. Run ${DOCTOR_COMMAND} in each repository checkout.`
}

/**
 * detailsFor is the CLI's own text about one installation: the lines of its list
 * document that name this installation, and nothing else.
 *
 * A warning line names an installation one of two ways, and both are the CLI's
 * own formats rather than a search for the name anywhere in the line: the
 * staleness line starts with `<name>'s shared config was last checked `, and a
 * refused config's line carries `projects/<name>.yaml` in the path it names
 * first. Matching the bare name would be wrong in the direction that matters --
 * `aurora` is a substring of `aurora-2`, and a line about one installation drawn
 * under another's issue is a screen that lies.
 *
 * The bound is `MAX_DETAIL_LINES` and it is the visible kind: past it the
 * console appends one line saying how many it did not keep, because a disclosure
 * that silently shortens the CLI's output is the omission the list's own rule
 * forbids.
 */
export function detailsFor(project: string, warnings: readonly string[]): readonly string[] {
  const kept: string[] = []
  let dropped = 0
  for (const line of warnings) {
    if (!namesInstallation(line, project)) continue
    if (kept.length >= MAX_DETAIL_LINES) {
      dropped += 1
      continue
    }
    kept.push(line)
  }
  if (dropped > 0) kept.push(`${String(dropped)} more lines the console did not keep`)
  return Object.freeze(kept)
}

/** namesInstallation reports whether one of the CLI's warning lines is about
 * this installation. See `detailsFor` for the two shapes it recognises. */
export function namesInstallation(line: string, project: string): boolean {
  if (project === '') return false
  return line.includes(`projects/${project}.yaml`) || line.startsWith(sharedStaleLead(project))
}

/** staleLineFor is the CLI's staleness line for this installation, or
 * `undefined` when it printed none. */
export function staleLineFor(project: string, warnings: readonly string[]): string | undefined {
  const lead = sharedStaleLead(project)
  return warnings.find((line) => line.startsWith(lead) && line.includes(STALE_MARKER))
}

/** isStaleWarning is `staleLineFor` as a predicate, for a caller that has the
 * line and not the document. */
export function isStaleWarning(line: string, project: string): boolean {
  return line.startsWith(sharedStaleLead(project)) && line.includes(STALE_MARKER)
}

// ---------------------------------------------------------------------------
// The checkouts the console may offer
// ---------------------------------------------------------------------------

/**
 * isToolOwnedPath reports whether a directory is one the console must never
 * offer as an installation's checkout, whatever the CLI listed: a temporary
 * directory, or one a tool owns rather than the operator.
 *
 * The owner's report is the reason it exists -- the first screen offered
 * `/private/tmp/claude-501/.../scratchpad/wt-merge` as "a suggested checkout" --
 * and the rule is a property of the *path* and not of any one tool: a checkout
 * recorded under the OS's temporary directory is one the OS may delete, a
 * `/scratchpad/` segment is a scratch area by its own name, and a
 * `.claude/worktrees/` segment is an agent harness's working copy of a
 * repository rather than the operator's. None of the three is a directory an
 * operator keeps their work in, and offering one is how a console gets pointed
 * at a directory that will not be there tomorrow.
 *
 * `tempDirectory` is passed in rather than read here (`os.tmpdir()` is the
 * caller's) so this module stays a pure function of its arguments and a test can
 * name the temporary directory of the machine it is testing.
 *
 * The comparison is on path *segments* and not on prefixes: `/tmp` is refused
 * and `/tmpfoo` is not, and `/home/tmp/x` is not refused at all. A Windows
 * spelling is compared with both separators, because the paths the CLI lists on
 * the WSL route are Linux ones and a host checkout on Windows is not.
 */
export function isToolOwnedPath(path: string, tempDirectory: string): boolean {
  if (path === '') return false
  const normalized = path.replace(/\\/g, '/')
  const segments = normalized.split('/').filter((segment) => segment !== '')
  const roots = [tempDirectory, '/tmp', '/private/tmp', '/var/folders', '/private/var/folders']
    .filter((root) => root !== '')
    .map((root) => root.replace(/\\/g, '/').split('/').filter((segment) => segment !== ''))
  for (const root of roots) {
    if (root.length === 0 || root.length > segments.length) continue
    if (root.every((segment, index) => segments[index] === segment)) return true
  }
  // `/private/tmp` and `/tmp` are one directory on macOS, and `os.tmpdir()`
  // answers the first while a checkout recorded by a tool may spell the second:
  // the two literal roots above cover the spelling the environment does not.
  if (hasSegmentRun(segments, ['scratchpad'])) return true
  return hasSegmentRun(segments, ['.claude', 'worktrees'])
}

/** hasSegmentRun reports whether `run` appears as consecutive segments. */
function hasSegmentRun(segments: readonly string[], run: readonly string[]): boolean {
  for (let start = 0; start + run.length <= segments.length; start += 1) {
    if (run.every((segment, index) => segments[start + index] === segment)) return true
  }
  return false
}

/**
 * suggestedCheckouts is the directories the console may offer for one
 * installation: the ones the CLI listed, with the tool-owned and temporary paths
 * removed, and **the project's own recorded checkout first** when the console
 * holds one.
 *
 * The order is the owner's ruling ("prefer the project's own recorded
 * checkout"), and it is the whole of the preference: the recorded one is the
 * directory the operator actually chose, the CLI's list is what the machine
 * remembers, and a screen that offered them in the document's order would put a
 * directory the operator never picked first whenever the CLI's order differed.
 *
 * The recorded checkout is offered even when the CLI did not list it -- it is
 * the console's own record of the operator's choice, and hiding it would hide
 * the installation's working directory from the one screen that can change it --
 * but never when it is itself a tool-owned path, because the rule above is about
 * where an installation may be used from and not about who wrote it down.
 */
export function suggestedCheckouts(
  recorded: string | undefined,
  listed: readonly string[],
  tempDirectory: string,
): readonly string[] {
  const out: string[] = []
  if (recorded !== undefined && recorded !== '' && !isToolOwnedPath(recorded, tempDirectory)) out.push(recorded)
  for (const candidate of listed) {
    if (candidate === '' || isToolOwnedPath(candidate, tempDirectory)) continue
    if (out.includes(candidate)) continue
    out.push(candidate)
  }
  return Object.freeze(out)
}

/**
 * firstReadableIndex is the owner's open-first-readable rule: the index of the
 * first row whose installation the console can open, or `-1` when it can open
 * none.
 *
 * The rule is a **fact about the rows and not a choice among them**: the
 * console opens the first one it can, in the CLI's own order, and it does not
 * prefer the previously chosen installation over it. That is the owner's ruling
 * of 2026-10-25 in as many words -- "If the previously chosen installation is
 * unreadable the console simply opens the first readable one and the chosen one
 * keeps its place and warning icon in the rail" -- so the remembered choice is
 * never dropped from the store and never opened when it cannot be read.
 */
export function firstReadableIndex(states: readonly InstallationState[]): number {
  return states.findIndex((state) => state.readable)
}
