// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The version skew handshake (desktop-console plan Task 3.2; design section
// 7.1, D7, and the skew row of section 6.3).
//
// The console is a client of an interface it does not own: every figure it draws
// arrives as a `--json` document from a `fugaro` binary the operator installed,
// and that binary is on the operator's own release train. So the console
// carries **a set of CLI minors whose documents it has read**
// (`SUPPORTED_CLI_MINORS` below is the set itself, and is the one place the
// members are named), reads `fugaro version` at startup, and places what it read
// in these ways (design section 6.3, D7, and the owner's ruling of 2026-10-30):
//
//   * **inside the set, or any plain release above its newest member** -- it
//     proceeds, and draws nothing about versions at all. A CLI newer than this
//     console is the ordinary case of an older console: the CLI's compatibility
//     rule is that its JSON only grows (a field is added, never re-meant; a
//     field whose meaning changes gets a new name), so the decoders ignore a
//     key they do not know and draw what they do. A read that still fails to
//     decode -- a field removed or retyped -- is refused on its own screen with
//     the decoder's sentence (`shared/schema.ts`), not by this handshake;
//   * **below the set and at or above the floor, or a line that is not a plain
//     release number** -- it proceeds in a **reduced mode**: a banner naming
//     both versions, and the views whose figures it cannot vouch for are not
//     drawn (`hidesView` below);
//   * **below a hard floor** -- it refuses, because "a dashboard that
//     misreports spend because it read a shape it did not understand is worse
//     than one that says it cannot read it" (D7). `src/main/window.ts` is where
//     that refusal is real: no dashboard window is built at all.
//
// **`fugaro version` has no `--json`, and this is the fact that settles the
// design's UNVERIFIED item 3.** `newVersionCmd` (`internal/cli/root.go`) is
// `cobra.NoArgs` with no `Flags()` at all and prints one plain line -- the
// `Version` variable, i.e. the release tag of a built binary (`0.8.1`, captured
// from a build with `.goreleaser.yaml`'s `-X .../internal/cli.Version=0.8.1`),
// or the literal `dev` for a binary built without those ldflags. So the
// handshake parses the human line, and `version.test.ts` pins that parse
// against that real output -- the same line `src/main/spawn.ts`'s
// `parseVersionLine` already guards for the binary resolver, whose grammar this
// file's parser accepts byte for byte (a test asserts the two doors agree, so a
// cosmetic change to the line cannot break one without breaking the other).
//
// **Two things this file deliberately does not carry.** The raw unparseable line
// never enters the model: a string a binary printed can be anything -- a usage
// message, a stack trace, a token a wrapper echoed -- and a model that carried
// it would put it one writer away from a screen. A line the console cannot
// parse becomes `unreadable` with the console's own sentence and a `null`
// version, and the string is dropped where it was read. And the version strings
// that *do* enter the model are drawn through `sanitizeText`, the same door
// every other string from outside the console goes through (`versionText`
// below), because a version line is a string the CLI chose.
//
// **Why the drawing is here and not in a screen.** The skew state is the
// console's own -- it is a fact about the version comparison, not about any
// document -- and `renderer/screens/spend.ts` says so: "Task 3.2 owns the skew
// state and a screen that invented its own would be two". The banner a screen
// prepends to itself and the refusal screen a refused console draws instead of a
// dashboard are the two presentations of that one state, and they live beside
// the comparison that produces it.
//
// This is the one file in `shared/` that draws, and it is also imported by the
// **main** process (`src/main/window.ts` runs the handshake with it), so it
// depends on nothing in `renderer/`: the one writer below (`literal`) is this
// file's own rather than `renderer/app.ts`'s, the string a binary chose is
// sanitized by the `versionText` reader above it rather than by `putText`, and
// the element shape it draws into is declared here as the six DOM members it
// uses rather than imported from a screen. All of that is one rule: the renderer
// depends on `shared/`, and `shared/` does not depend on the renderer -- and the
// consequence for a reviewer is that this file's drawing is audited by
// `test/unit/screen-literals.test.ts`, which reads it from
// `src/shared/version.ts` with the same rules it holds the screens to.

import {
  renderNotice,
  type DrawnNotice,
  type NoticeDetail,
  type NoticeSpec,
} from './notice'
import { sanitizeText } from './sanitize'

/**
 * CLI_VERSION_BUILT_AGAINST is the `fugaro` version this console was **built
 * and tested against** (design section 7.1): the newest CLI release whose
 * documents the console's decoders were re-read against. It is `0.13.0`
 * because the console's struct pins (`schema.test.ts`, `spend.test.ts`,
 * `live.test.ts` and `report-dimensions.test.ts`, which read the Go sources by
 * path) pass against the `v0.13.0` tree unchanged, and no file those pins or
 * the console's other decoders read differs between `v0.13.0` and the tree this
 * console is built from. `.goreleaser.yaml` builds `Version` from the tag, so a
 * released binary prints exactly this line.
 *
 * It is a constant rather than an environment lookup on purpose: the console's
 * compatibility claim is a property of *this build* -- the shapes its decoder
 * was written against -- and a value read at runtime would make the claim about
 * the machine instead of about the code. The task that bumps it is the task
 * that re-reads the contract.
 */
export const CLI_VERSION_BUILT_AGAINST = '0.13.0'

/**
 * SUPPORTED_CLI_MINORS is the set of minors whose documents this console has
 * read, stated as minors rather than as versions because that is the
 * granularity of the promise -- the patch levels of a minor are not a
 * compatibility axis (the JSON contract is), and a console that refused `0.8.2`
 * because it was built against `0.8.1` would refuse every patch release.
 *
 * `0.7` and `0.8` are the design's original pair ("the minor it was built
 * against, plus the previous one", design section 7.1); `0.11` is V2.5.1a's
 * addition, the newest minor the launch half was written against (0.11.2 is the
 * first release to carry `--plan-task`); `0.12` and `0.13` were re-read on
 * 2026-10-30: the console's struct pins pass against both release trees, and
 * where `v0.12.1` writes less than `v0.13.0` (`leased_usd`, `compute_usd`, the
 * `digest` stage, the report's `--by mode`) the console already reads the key
 * as optional or the word as one an older CLI does not write. The gap (0.9,
 * 0.10) is honest: those releases are drawn in the reduced mode.
 *
 * **The set is not a ceiling** (the owner's ruling of 2026-10-30): a plain
 * release above its newest member answers `ok` as well (`classifyCliVersion`),
 * because the CLI's documents only grow and the decoders ignore a key they do
 * not know. The set's newest member is therefore the line between "a CLI older
 * than this console", which is reduced, and "a CLI this console reads"; and
 * `version.test.ts` holds that the newest released CLI minor
 * (`docs/release-streams.yaml`) answers `ok`, so a release cannot leave the
 * console behind without a red test.
 *
 * It is deliberately **not** `LAUNCH_CLI_FLOOR`'s minor: the set says which CLI
 * the console draws its full dashboard for, the floor says which CLI one
 * operation may be started on, and they are asked separately (`launchSupported`
 * and `classifyCliVersion`) because a launch needs one read, not a dashboard.
 */
export const SUPPORTED_CLI_MINORS: readonly string[] = Object.freeze(['0.7', '0.8', '0.11', '0.12', '0.13'])

/**
 * LAUNCH_CLI_FLOOR is the oldest CLI the launch operation may be offered to,
 * and it is the design's number: `--plan-task` is the newest flag the launch's
 * argv carries, its release is `internal/cli/recipes_skew.go`'s
 * **`planTaskSince = "0.11.2"`** (a title's `titlesSince` is `0.8.5`, and
 * `--run-id` and `--task-file` are older still), so the operation is offered
 * only to an installation whose CLI is at least that
 * (`docs/design/desktop-console.md` §5.1 rule 11, "a CLI too old for a flag of
 * the row").
 *
 * It is a **patch-level** floor, and it is deliberately not a member of
 * `SUPPORTED_CLI_MINORS`: that set is minor-granular and reads `0.11` as a
 * whole minor, which puts `0.11.0` and `0.11.1` inside the range and still
 * below this floor. Membership in the range says which CLI the console draws
 * its full dashboard for; this constant says which CLI one operation may be
 * started on, and the two questions are asked separately (`launchSupported` and
 * `classifyCliVersion`).
 *
 * **What the floor does not cover, and why it is not a reason to raise it.** A
 * CLI between this floor and the release that first carries `base_branch` in
 * `config list --json` (the read the launch needs: it runs from the app's own
 * private, empty directory, so the CLI's own merged order would fall back to
 * `main` unseen) still answers that command -- it simply has no `base_branch`
 * key at all. That is a state the operation draws for: `installlist.ts` keeps
 * an **absent** key apart from a key that is present and empty (§6.8: absent is
 * the document's word for "an older CLI"), and `recordedBaseBranch` refuses
 * such an installation with its own sentence, "this CLI does not report base
 * branches", rather than sending its operator to a setting they have already
 * set. Raising the floor to that release instead would refuse every launch on
 * today's CLI for a reason that is not the floor's -- which flag the row may
 * carry -- and would leave the design's own floor unanswered.
 */
export const LAUNCH_CLI_FLOOR = '0.11.2'

/**
 * launchSupported answers whether one version line may be launched with, by
 * comparing it against `LAUNCH_CLI_FLOOR` at the patch level (`meetsCliFloor`,
 * below, which states why the comparison refuses what it refuses).
 *
 * A line below the floor answers `false` however close: `0.11.1` is inside the
 * supported minor `0.11` and carries no `--plan-task`.
 *
 * It is deliberately not a membership test: `0.11` is a member of
 * `SUPPORTED_CLI_MINORS` while `0.11.0` and `0.11.1` are below this floor, and
 * the members `0.7` and `0.8` are below it as well.
 */
export function launchSupported(version: string): boolean {
  return meetsCliFloor(version, LAUNCH_CLI_FLOOR)
}

/**
 * BUDGET_COMPUTE_CLI_FLOOR is the oldest CLI the Spend screen's caps read may
 * ask for a repository's compute dollars, by passing `budget show --compute`.
 *
 * The flag is newer than the operation: `0.13.0` is the release that ships it
 * (`internal/cli/budget.go`'s `newBudgetShowCmd`, and `compute_usd` on each
 * `showRepo`); `v0.12.1` answers the flag with an unknown-flag error.
 *
 * It is a patch-level floor rather than a range membership for the reason
 * `LAUNCH_CLI_FLOOR` is: `0.12.1` passes that floor and is below this one, so
 * the question a caller asks before passing `--compute` is this constant's, and
 * neither `SUPPORTED_CLI_MINORS` nor `classifyCliVersion` answers it.
 *
 * It is asked only where the flag would be passed, and this console has exactly
 * one such place: the Spend screen's caps read. Nothing else asks, because
 * every other caller of `budget show` -- the rail and the Projects screen --
 * would pay for the runs-bucket listing the flag costs and draw none of it.
 */
export const BUDGET_COMPUTE_CLI_FLOOR = '0.13.0'

/**
 * budgetComputeSupported answers whether one version line may be asked for
 * `budget show --compute`, by comparing it against `BUDGET_COMPUTE_CLI_FLOOR`
 * at the patch level.
 *
 * **No caller asks it yet.** The caller is `showBudget({})` in
 * `src/renderer/entry.ts`, so no spawn passes `--compute` until that call is
 * flipped behind this predicate. The caps read is withheld in every
 * compatibility mode that is not `ok` (`REDUCED_VIEW_OPERATIONS.caps`,
 * `src/main/ipc.ts`), and a CLI at this floor now answers `ok`, so flipping the
 * caller is the one step left; the predicate is covered by `version.test.ts`
 * at, below and above the floor so that the run which flips it asks this rule
 * rather than writing its own comparison.
 */
export function budgetComputeSupported(version: string): boolean {
  return meetsCliFloor(version, BUDGET_COMPUTE_CLI_FLOOR)
}

/**
 * meetsCliFloor is the one patch-level comparison both floors above are asked
 * by, so a rule learned on one of them cannot be missing from the other.
 *
 * The order of the tests is the rule, and each refusal is on the safe side of a
 * spend. An unreadable line, a `dev` build and a prerelease are all `false`: the
 * first two cannot be placed at all, and `parseCliVersion` deliberately does not
 * compare a prerelease tail (`0.11.2-rc.1` is read as `0.11.2`), so accepting
 * one here would act on a release that has not happened. A line at or above the
 * floor -- including a build-metadata tail, which is the same release -- answers
 * `true`, and a line below it answers `false` however close.
 */
function meetsCliFloor(version: string, floorLine: string): boolean {
  const parsed = parseCliVersion(version)
  const floor = parseCliVersion(floorLine)
  if (!parsed.ok || parsed.version.kind !== 'release') return false
  if (!floor.ok || floor.version.kind !== 'release') return false
  const [core = ''] = parsed.version.raw.replace(/^v/, '').split('+')
  if (core.includes('-')) return false
  const { major, minor, patch } = parsed.version
  const { major: floorMajor, minor: floorMinor, patch: floorPatch } = floor.version
  if (major !== floorMajor) return major > floorMajor
  if (minor !== floorMinor) return minor > floorMinor
  return patch >= floorPatch
}

/**
 * CLI_HARD_FLOOR_VERSION is the version below which the console refuses (D7).
 * It is one minor below the oldest supported one, which is the smallest floor
 * the design's table can have: everything inside `SUPPORTED_CLI_MINORS` (and
 * every plain release above it) is supported, everything between the floor and
 * the set that is not a member is the degraded band, and everything below the
 * floor is the refusal. `version.test.ts` holds that
 * relationship -- a floor that drifted into the range, or a range that grew
 * past the floor, fails a named test.
 */
export const CLI_HARD_FLOOR_VERSION = '0.6.0'

/**
 * CONSOLE_CLI_UPGRADE_COMMAND is the fix line an *old* CLI's banner offers. It
 * is the CLI's own constant rather than one this file invented:
 * `internal/updatecheck/updatecheck.go`'s `BrewCommand`, which is the command
 * the CLI's own version nag prints ("fugaro %s is available (you have %s): %s").
 * The console prints it and runs nothing -- upgrading a binary is the
 * operator's act, not the app's, exactly as `FUGARO_INSTALL_COMMAND` is on the
 * absent-binary screen (`src/source/cli.ts`).
 */
export const CONSOLE_CLI_UPGRADE_COMMAND = 'brew upgrade dimipaun/tap/fugaro'

/**
 * ReducedView is a view whose figures a reduced-mode console does not draw.
 *
 * The list is closed, and it is the *spend* half of the console by design
 * rather than by taste: D7's reason for degrading at all is "a dashboard that
 * misreports spend because it read a shape it did not understand is worse than
 * one that says it cannot read it", so what a reduced mode withholds is every
 * figure that is money or a limit on money -- the Spend screen's series and
 * totals (`report --json`), the caps (`budget show --json`, and the cap bars the
 * Live screen draws from the same fields), and the kill/resume affordances,
 * which are cap state and are already absent whenever the caps are not readable
 * (design section 6.3). The run list, the stages, the ages and the deadlines are
 * fields `watch` has carried since long before this range and stay.
 */
export type ReducedView = 'spend' | 'caps' | 'kill'

/** REDUCED_VIEWS is the closed list, in the order the banner draws it. */
export const REDUCED_VIEWS: readonly ReducedView[] = Object.freeze(['spend', 'caps', 'kill'])

/**
 * HIDDEN_VIEW_WORDS is the console's own word for each withheld view: a screen
 * asks `hidesView` and draws this word, so there is one vocabulary for the list
 * rather than one per screen.
 */
export const HIDDEN_VIEW_WORDS: Readonly<Record<ReducedView, string>> = Object.freeze({
  spend: 'the spend screen: its series and totals',
  caps: 'the caps: every limit and its bars',
  kill: 'the kill and resume controls',
})

// ---------------------------------------------------------------------------
// The parse.
// ---------------------------------------------------------------------------

/**
 * CliVersion is a version line the console read and understood. `kind: 'dev'` is
 * the line a binary built without the release ldflags prints
 * (`internal/cli/root.go`: `var Version = "dev"`), and it is a *recognised* line
 * that no comparison can place -- which is a different fact from an unreadable
 * one, and `classifyCliVersion` treats it as such.
 */
export type CliVersion =
  | {
      readonly kind: 'release'
      readonly raw: string
      readonly major: number
      readonly minor: number
      readonly patch: number
    }
  | { readonly kind: 'dev'; readonly raw: string }

/** CliVersionParse is the parse's two outcomes: a version, or why not. */
export type CliVersionParse =
  | { readonly ok: true; readonly version: CliVersion }
  | { readonly ok: false; readonly reason: string }

/**
 * VERSION_LINE_RE is the grammar `src/main/spawn.ts`'s `parseVersionLine`
 * already accepts, and it is deliberately the same one: an optional `v`, a
 * dotted numeric core, and an optional prerelease/build tail. Anything else --
 * an empty line, a usage message, a stack trace -- is not a version, and the
 * console refuses to guess at one.
 */
/** MAX_VERSION_COMPONENT_DIGITS bounds each dotted component: nine digits is
 * always an exact integer, and no real release needs more. */
const MAX_VERSION_COMPONENT_DIGITS = 9

const VERSION_LINE_RE = /^v?\d+(?:\.\d+)*(?:[-+][0-9A-Za-z.-]+)?$/

/**
 * parseCliVersion reads `fugaro version`'s output: one line, trimmed, exactly as
 * the command writes it (`fmt.Fprintln`, so the line ends with a newline and a
 * blank-line-tolerant reader is the same reader). A second non-empty line is a
 * refusal, not a first line to take: a binary that answered with two lines
 * answered with something this console does not understand.
 *
 * The prerelease tail is **not** compared: `0.9.0-rc.1` is placed by
 * `major.minor.patch` like `0.9.0`, because the console orders no prereleases
 * and a comparison it invented would be a claim about the CLI's own scheme.
 * What it does not do is *drop* the tail: `raw` keeps the line as it was
 * printed, and that is what a screen draws.
 */
export function parseCliVersion(text: string): CliVersionParse {
  const lines = text.split('\n').filter((line) => line.trim() !== '')
  if (lines.length === 0) return { ok: false, reason: 'the binary printed no version line' }
  if (lines.length > 1) {
    return { ok: false, reason: 'the binary printed more than one line for its version' }
  }
  const raw = (lines[0] ?? '').trim()
  if (raw.length > 200) return { ok: false, reason: 'the version line is implausibly long' }
  if (raw === 'dev') return { ok: true, version: { kind: 'dev', raw } }
  if (!VERSION_LINE_RE.test(raw)) {
    // The line itself is **not** in the reason: it is a string the binary chose,
    // and the reason is drawn on a screen.
    return { ok: false, reason: 'the binary printed a line that is not a version' }
  }
  const numeric = raw.replace(/^v/, '').split(/[-+]/)[0] ?? ''
  const digits = numeric.split('.')
  // A component past MAX_VERSION_COMPONENT_DIGITS is not a number `parseInt`
  // can hold exactly, and a comparison against a rounded float would place a
  // line it cannot read. Fail closed: the line is unreadable.
  if (digits.some((part) => part.length > MAX_VERSION_COMPONENT_DIGITS)) {
    return { ok: false, reason: 'the binary printed a version number too long to read' }
  }
  const parts = digits.map((part) => Number.parseInt(part, 10))
  const [major, minor, patch] = parts
  if (major === undefined || minor === undefined) {
    return { ok: false, reason: 'the binary printed a version with no minor number' }
  }
  return {
    ok: true,
    version: { kind: 'release', raw, major, minor, patch: patch ?? 0 },
  }
}

// ---------------------------------------------------------------------------
// The comparison.
// ---------------------------------------------------------------------------

/**
 * CliSkewName is *why* the compatibility is what it is. Seven names, one per
 * way the console can place a version line, and each is a closed value so a
 * screen or a test can branch on it without matching prose.
 */
export type CliSkewName =
  /** a member of the supported set: the console proceeds and draws no banner */
  | 'supported'
  /** a plain release newer than the newest member: the console is the older
   * one, and proceeds exactly as for a member (the owner's ruling of 2026-10-30) */
  | 'newer'
  /** older than the newest member, not one, and at or above the floor: a
   * banner, and the spend views held back */
  | 'older'
  /** a line at or above the floor that is not a plain release number -- a
   * prerelease or build tail, a leading zero, a missing patch: the same */
  | 'irregular'
  /** below the floor: the console refuses to draw a dashboard */
  | 'below-floor'
  /** a line the console understands and cannot place (`dev`) */
  | 'unplaceable'
  /** a line the console could not read at all, or a read that failed */
  | 'unreadable'

/** CliCompatibilityMode is what the console does about it, which is the whole
 * point of the handshake: one of the three outcomes design section 6.3 lists. */
export type CliCompatibilityMode =
  /** proceed */
  | 'ok'
  /** proceed with a banner naming both versions, without the views withheld */
  | 'reduced'
  /** draw no dashboard */
  | 'refused'

/**
 * CliCompatibility is the handshake's outcome: the mode, why, both versions, and
 * the facts a screen needs to say any of it. It is a value rather than a
 * sentence so that `src/main/window.ts` can gate on `mode`, a screen can gate on
 * `hidesView`, and a test can assert the boundary without matching the prose.
 */
export interface CliCompatibility {
  readonly mode: CliCompatibilityMode
  readonly skew: CliSkewName
  /**
   * `cliVersion` is the version line **as the CLI printed it** (`0.8.1`,
   * `v0.8.1`, `dev`), or `null` when the console could not read one. It is
   * never the unparsed text: an unreadable line is dropped, not carried.
   */
  readonly cliVersion: string | null
  /** the version this console was built and tested against */
  readonly consoleVersion: string
  /** the supported range, as minors */
  readonly supported: readonly string[]
  /** the oldest version the console will read at all */
  readonly floor: string
  /** every view withheld in this mode, in `REDUCED_VIEWS`' order */
  readonly hidden: readonly ReducedView[]
  /**
   * `because` is present only when the console could not read a version line at
   * all, and it is the console's own sentence for why (a spawn that failed, a
   * read that failed). It never holds the child's output -- see
   * `unreadableCompatibility`.
   */
  readonly because?: string
}

/**
 * compareMinors orders two `major.minor` strings: negative when the first is
 * older, zero when they are the same minor, positive when it is newer. It is
 * written out rather than reached for through a dependency because the console
 * carries no semver library -- the console's own package.json dependencies are
 * a deliberate short list -- and because a `major.minor` pair is two integers,
 * not a version to be parsed.
 */
export function compareMinors(a: string, b: string): number {
  const left = a.split('.').map((part) => Number.parseInt(part, 10))
  const right = b.split('.').map((part) => Number.parseInt(part, 10))
  const leftMajor = left[0] ?? 0
  const rightMajor = right[0] ?? 0
  if (leftMajor !== rightMajor) return leftMajor < rightMajor ? -1 : 1
  const leftMinor = left[1] ?? 0
  const rightMinor = right[1] ?? 0
  if (leftMinor === rightMinor) return 0
  return leftMinor < rightMinor ? -1 : 1
}

/**
 * PLAIN_RELEASE_RE is the only shape a line may have to answer `ok`: an
 * optional `v` and exactly `major.minor.patch`, each a number with no leading
 * zero, and no tail of any kind. It is narrower than `VERSION_LINE_RE` on
 * purpose. A released binary prints exactly this (`.goreleaser.yaml`'s
 * `{{ .Version }}`), and an open-ended `ok` -- every release above the set --
 * must not also open to `0.14.0-evil`, `0.14.0+x` or `00.14.0`, which
 * `parseCliVersion` reads (it keeps the console's two version doors in step
 * with `spawn.ts`) but no release prints.
 */
const PLAIN_RELEASE_RE = /^v?(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)$/

/** minorOf is a version string's `major.minor`, which is the range's own unit. */
function minorOf(version: string): string {
  const parsed = parseCliVersion(version)
  if (!parsed.ok || parsed.version.kind !== 'release') {
    throw new Error(`the console's own version constant is not a release version: ${version}`)
  }
  return `${String(parsed.version.major)}.${String(parsed.version.minor)}`
}

/**
 * classifyCliVersion places a version line in the supported range. It is a pure
 * function of the string, the range and the floor -- no clock, no environment --
 * so every boundary is a test rather than an observation.
 *
 * The order of the tests is the rule. An unreadable line is refused before
 * anything is compared: a console that fell through to "treat it as supported"
 * would drive a binary it cannot identify, which design section 3.4 rule 4
 * already forbids for the *binary resolver* ("refuses to run a binary it cannot
 * identify") and which is worse here, because the consequence is a figure rather
 * than a message.
 */
export function classifyCliVersion(text: string): CliCompatibility {
  const parsed = parseCliVersion(text)
  if (!parsed.ok) return compatibility('refused', 'unreadable', null, [...REDUCED_VIEWS])
  if (parsed.version.kind === 'dev') {
    // A `dev` build is the operator's own working copy: newer than any release
    // by construction, and unplaceable by comparison. It proceeds in the reduced
    // mode rather than being refused, because refusing would block the one
    // person who is building the CLI and the console together -- and it is not
    // silently trusted either.
    return compatibility('reduced', 'unplaceable', parsed.version.raw, [...REDUCED_VIEWS])
  }
  const { raw } = parsed.version
  const floor = minorOf(CLI_HARD_FLOOR_VERSION)
  const inRange = minorOf(raw)
  // The comparison is on `major.minor` pairs, which is the range's own unit: the
  // patch level is not an axis of the JSON contract, and a console that placed
  // `0.8.2` differently from `0.8.1` would refuse every patch release.
  if (compareMinors(inRange, floor) < 0) {
    return compatibility('refused', 'below-floor', raw, [...REDUCED_VIEWS])
  }
  // Every `ok` below is open to a plain release only: the answer above the set
  // is open-ended, so the shape of the line is the whole of what keeps a
  // prerelease, a build tail or a zero-padded number from riding it.
  if (!PLAIN_RELEASE_RE.test(raw)) return compatibility('reduced', 'irregular', raw, [...REDUCED_VIEWS])
  // The set is a *set of minors*, not a comparison against one: the previous
  // minor is supported exactly as the built one is, and a console that reduced a
  // CLI it promised to support would be the set constant lying.
  if (SUPPORTED_CLI_MINORS.includes(inRange)) return compatibility('ok', 'supported', raw, [])
  if (compareMinors(inRange, newestSupportedMinor()) > 0) return compatibility('ok', 'newer', raw, [])
  return compatibility('reduced', 'older', raw, [...REDUCED_VIEWS])
}

/** newestSupportedMinor is the set's highest member, by `compareMinors` rather
 * than by position, so the order the set is written in is not a rule. */
function newestSupportedMinor(): string {
  return SUPPORTED_CLI_MINORS.reduce((newest, minor) => (compareMinors(minor, newest) > 0 ? minor : newest))
}

/**
 * unreadableCompatibility is the outcome when the console could not read a
 * version line at all -- a spawn that failed, or a read that failed. `because`
 * is the console's own sentence (the reasons `src/main/spawn.ts` produces are
 * the console's words, never the child's output); the version is `null` and the
 * mode is the refusal, because a console that cannot say which CLI it is driving
 * cannot vouch for a single figure on the dashboard.
 */
export function unreadableCompatibility(because: string): CliCompatibility {
  return { ...compatibility('refused', 'unreadable', null, [...REDUCED_VIEWS]), because }
}

/** compatibility builds the value, so every branch above has one shape. */
function compatibility(
  mode: CliCompatibilityMode,
  skew: CliSkewName,
  cliVersion: string | null,
  hidden: readonly ReducedView[],
): CliCompatibility {
  return {
    mode,
    skew,
    cliVersion,
    consoleVersion: CLI_VERSION_BUILT_AGAINST,
    supported: SUPPORTED_CLI_MINORS,
    floor: CLI_HARD_FLOOR_VERSION,
    hidden,
  }
}

/**
 * hidesView is the one question a screen asks of the handshake: may I draw this
 * view's figures? It is a function rather than a field per screen so that the
 * three withheld views are decided in one place, and it answers `true` for the
 * refusal as well -- a refused console draws no dashboard, so it draws none of
 * these either.
 */
export function hidesView(compatibility: CliCompatibility, view: ReducedView): boolean {
  return compatibility.hidden.includes(view)
}

// ---------------------------------------------------------------------------
// The two drawings.
// ---------------------------------------------------------------------------

/** SkewTheme is the theme the state is drawn for. It is the same two words as
 * `renderer/screens/live.ts`'s `LIVE_THEMES`, declared locally so this module
 * imports nothing from the renderer but the writers. */
export type SkewTheme = 'light' | 'dark'

/**
 * SkewElement is what this file draws into: the three DOM members
 * `renderer/app.ts`'s writers use, plus `tag` and `append`. It is declared
 * structurally rather than imported, so `shared/` depends on no screen --
 * `renderer/screens/degraded.ts` makes the mirror-image choice from the other
 * side -- and it is the same shape as `ScreenElement`, so a screen hands its own
 * elements to `renderVersionSkew` without a conversion.
 */
export interface SkewElement {
  textContent: string | null
  setAttribute(name: string, value: string): void
  getAttribute(name: string): string | null
  removeAttribute(name: string): void
  readonly tag: string
  append(child: SkewElement): void
}

/** SkewFactory is where elements come from. */
export interface SkewFactory {
  create(tag: string): SkewElement
}

/**
 * SKEW_HEADLINES is the console's own heading per mode. A reduced console is
 * still a console, so its heading says what is going on; a refused one says what
 * it refused to do.
 */
export const SKEW_HEADLINES: Readonly<Record<CliCompatibilityMode, string>> = Object.freeze({
  ok: 'the fugaro this console is driving is one it was built against',
  reduced: 'fugaro version skew: reduced mode',
  refused: 'this fugaro is not one this console will read',
})

/**
 * NOTICE_REASONS is the one plain sentence the card opens with, per drawn skew:
 * what is wrong and what the operator does about it, in the console's own words
 * and with no version number, range or jargon in it. The technical text -- the
 * range this console reads, the version the binary reported, the upgrade command
 * -- is what the card's details hold (`skewSentence` carries it), so the sentence
 * a reader sees first is a sentence, not a specification.
 *
 * The record is keyed by the skew **name** and not by the mode: the two modes are
 * drawn from five skews, and a name added to `CliSkewName` -- a sixth way a
 * version can be wrong -- is a compile error here until it has a sentence of its
 * own, which is the whole reason this is a typed record rather than a lookup.
 */
export const NOTICE_REASONS: Readonly<
  Record<Exclude<CliSkewName, 'supported' | 'newer'>, string>
> = Object.freeze({
  older:
    'This fugaro is older than the versions this console fully reads, so spend, caps and the kill controls are hidden until you upgrade fugaro.',
  irregular:
    'This fugaro reports a version that is not a plain release number, so spend, caps and the kill controls are hidden until you run a released fugaro.',
  unplaceable:
    'This is a development build of fugaro, so spend, caps and the kill controls are hidden because the console cannot vouch for them.',
  'below-floor': 'This fugaro is too old for this console to read, so upgrade fugaro to see the dashboard.',
  unreadable: 'The console could not tell which fugaro it is driving, so it shows no dashboard.',
})

/**
 * skewSentence is the banner's sentence, in the console's own words, and it is
 * deliberately **without the CLI's text**: the console's own constants are the
 * only versions it composes into a sentence, and the version the CLI reported is
 * drawn in its own cell through the one writer (`renderVersionSkew`). That way the
 * line a binary printed is never concatenated into prose this file wrote, and
 * the sentence is the same string whatever the binary said.
 */
/**
 * RANGE_PHRASE is the half-sentence every mode's sentence carries: what this
 * console was built against, and what it therefore reads. One fragment rather
 * than six hand-written copies, so no mode can be written without saying which
 * versions are in question -- which is the design's own requirement that the
 * banner name both versions.
 */
const RANGE_PHRASE = `this console was built against ${CLI_VERSION_BUILT_AGAINST} and reads ${SUPPORTED_CLI_MINORS.join(', ')} and every later release`

export function skewSentence(compatibility: CliCompatibility): string {
  switch (compatibility.skew) {
    case 'supported':
      return `this fugaro is one of the versions ${RANGE_PHRASE}`
    case 'newer':
      return (
        `this console is older than the fugaro it is driving: ${RANGE_PHRASE}. It shows what it can read: a ` +
        'field this fugaro added is ignored, and a screen whose read no longer decodes says so on that screen; ' +
        'update the console to see what this fugaro added'
      )
    case 'older':
      return (
        `the fugaro this console is driving is older than the versions ${RANGE_PHRASE}; upgrade it with ` +
        `${CONSOLE_CLI_UPGRADE_COMMAND}. It is running in a reduced mode and will not draw a figure it cannot ` +
        'vouch for'
      )
    case 'irregular':
      return (
        `the fugaro this console is driving reports a version that is not a plain release number, so it cannot ` +
        `be placed: ${RANGE_PHRASE}. It is running in a reduced mode and will not draw a figure it cannot ` +
        'vouch for; a released fugaro prints its version as major.minor.patch'
      )
    case 'below-floor':
      return (
        `the fugaro this console is driving is older than ${CLI_HARD_FLOOR_VERSION}, the oldest version this ` +
        `console will read: ${RANGE_PHRASE}. Upgrade it with ${CONSOLE_CLI_UPGRADE_COMMAND}. The console ` +
        'draws no dashboard at all, because a dashboard that misreports spend is worse than one that says it ' +
        'cannot read this fugaro'
      )
    case 'unplaceable':
      return (
        `the fugaro this console is driving reports a version it cannot place in the range ${RANGE_PHRASE}: ` +
        'a dev build, which is newer than any release by construction. It is running in a reduced mode and ' +
        'will not draw a figure it cannot vouch for'
      )
    case 'unreadable':
      return `the console could not read which fugaro it is driving, so it will not draw a dashboard it cannot vouch for: ${RANGE_PHRASE}`
  }
}

/**
 * UNREADABLE_VERSION is what a version cell draws when there is no version to
 * draw: the line was unreadable, or `sanitizeText` refused the one that was
 * read. It is the console's own sentence, and it is deliberately the same
 * sentence in both cases -- from a screen's side "the console could not read a
 * version" is one fact, and two spellings of it would be two things to keep in
 * step.
 */
export const UNREADABLE_VERSION = 'a version line the console could not read'

/**
 * versionText is the text a version cell draws, and it is the one door a string
 * the binary chose goes through: `sanitizeText` with the `'label'` class --
 * the same class `renderer/app.ts`'s `putText` uses, so the version line is held
 * to the same rules as every other label -- and the console's own sentence when
 * it is refused. It returns text rather than writing an element, so this file
 * has exactly one writer (`literal` below) and no second place a slot is
 * assigned.
 *
 * The refused case is not drawn as `n/a` with the reason in a tooltip, which is
 * what `putText` does: a version line the console will not draw is not a value
 * with an explanation, it is the same "no version" the unreadable line is, and
 * the sentence says so on screen where an operator will see it.
 */
function versionText(raw: string, field: string): string {
  const result = sanitizeText(raw, 'label', field)
  return result.ok ? result.text : UNREADABLE_VERSION
}

/**
 * versionCell is the text a version row draws: `null` (the unreadable case) is
 * the console's own sentence, because there is no value to draw and an `n/a`
 * with a title nobody can see would be less than the truth, and a version there
 * is goes through `versionText` -- it is a string a binary chose.
 */
function versionCell(version: string | null, field: string): string {
  return version === null ? UNREADABLE_VERSION : versionText(version, field)
}

/**
 * noticeReason is the one plain sentence the card opens with. The five drawn
 * skews each have their own (`NOTICE_REASONS`); the two that are not drawn -- a
 * supported and a newer CLI -- say the `ok` headline, because a console that
 * reads what it is driving has no reason to give.
 */
function noticeReason(compatibility: CliCompatibility): string {
  if (compatibility.skew === 'supported' || compatibility.skew === 'newer') {
    return SKEW_HEADLINES.ok
  }
  return NOTICE_REASONS[compatibility.skew]
}

/**
 * skewNoticeSpec is the handshake's state as a notice: the same card in both
 * modes, differing in what the reader needs from it. The reduced mode is a
 * console that still works, so its card folds the technical text away
 * (`'toggle'`) and can be dismissed; the refused mode is the whole document, so
 * its title is the document's one `h1` and its details are drawn open -- the
 * range and the version line are what the operator acts on, and folding them
 * behind a button on the only screen there is would hide the reason there is no
 * dashboard.
 *
 * The `console-skew` class is what the stylesheets and the specs reach the skew
 * notice by, and the per-row classes are the hooks the tests read: `skew-sentence`
 * holds the sentence, `skew-because` the reader's reason, and `data-hidden` marks
 * every view the mode withholds -- so a test asserts the withheld views by name
 * rather than by reading the sentence.
 */
function skewNoticeSpec(compatibility: CliCompatibility): NoticeSpec {
  const details: NoticeDetail[] = [
    { label: 'what happened', text: skewSentence(compatibility), classes: ['skew-sentence'] },
  ]
  // Why the console could not read a version, when its reader said: a reader's
  // reason is what `runVersionHandshake` kept from `probeVersion` (rule 4's
  // search, a spawn that failed, a deadline), and a screen that says only "it
  // could not read one" leaves the operator with nothing to act on. It reaches
  // the sink through `unreadableReason`, which is where `sanitizeText` runs, and
  // when the reader kept no reason at all the row is not created rather than
  // drawn empty, so there is no blank cell to misread.
  const because = unreadableReason(compatibility)
  if (because !== null) details.push({ label: 'why', text: because, classes: ['skew-because'] })
  details.push({
    label: 'this console reads',
    labelClasses: ['skew-console-label'],
    text: versionCell(compatibility.consoleVersion, 'the console version'),
    classes: ['notice-version', 'skew-console-version'],
  })
  details.push({
    label: 'the fugaro it is driving reports',
    labelClasses: ['skew-cli-label'],
    text: versionCell(compatibility.cliVersion, 'the CLI version'),
    classes: ['notice-version', 'skew-cli-version'],
  })
  if (compatibility.mode !== 'ok') {
    details.push({
      label: 'withheld',
      classes: ['skew-hidden-views'],
      list: compatibility.hidden.map((view) => ({
        text: HIDDEN_VIEW_WORDS[view],
        classes: ['skew-hidden'],
        attributes: { 'data-hidden': view },
      })),
    })
  }
  const refused = compatibility.mode === 'refused'
  return {
    severity: 'warning',
    dismissible: compatibility.mode === 'reduced',
    heading: refused ? 'h1' : 'h2',
    disclosure: refused ? 'expanded' : 'toggle',
    title: SKEW_HEADLINES[compatibility.mode],
    reason: noticeReason(compatibility),
    reasonClasses: ['skew-reason'],
    details,
    classes: ['console-skew'],
  }
}

/**
 * renderVersionSkew draws the handshake's state: the card a reduced console puts
 * at the top of the window, and the whole document a refused console draws
 * instead of a dashboard.
 *
 * It hands back the drawn notice rather than the root -- a caller that mounts it
 * (`renderer/app.ts`) needs the root, and the entry that wires the disclosure and
 * the dismissal needs the toggle, the dismiss button and the title they move
 * focus to.
 *
 * `data-mode` and `data-skew` are the machine-readable form of both and
 * `data-theme` is the theme; the root carries `console-skew` and **not** `live`:
 * a refused console draws no dashboard, and the absence of that root is how a
 * spec says so.
 */
export function renderVersionSkew(
  factory: SkewFactory,
  compatibility: CliCompatibility,
  theme: SkewTheme,
): DrawnNotice {
  const notice = renderNotice(factory, skewNoticeSpec(compatibility))
  const root = notice.root
  root.setAttribute('data-theme', theme)
  root.setAttribute('data-mode', compatibility.mode)
  root.setAttribute('data-skew', compatibility.skew)
  return notice
}

/**
 * unreadableReason is the console's own sentence for a compatibility that came
 * from a failed read, when one was kept, and `renderVersionSkew` draws it under
 * the banner's sentence (`p.skew-because`) -- the reader's reason is why the
 * operator sees a reduced or refused console at all, and "it could not read a
 * version" without it is a screen with nothing to act on.
 *
 * A caller that built the value with `classifyCliVersion` has none, and `null` is
 * that answer, so the caller creates no element.
 *
 * The class is `sentence`, which is the console's free-text door: a token shape
 * inside the reason is **redacted in place** by `sanitizeText` (a reader's
 * reason is a sentence a program wrote, and dropping the whole sentence because
 * part of it looked like a key would hide the failure it is describing). It is
 * sanitized here rather than at the call site so that the one door a reader's
 * sentence goes through is this function's.
 */
export function unreadableReason(compatibility: CliCompatibility): string | null {
  const because = compatibility.because
  if (typeof because !== 'string') return null
  const drawn = sanitizeText(because, 'sentence', 'the handshake reason')
  return drawn.ok ? drawn.text : null
}
