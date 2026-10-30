// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The version skew handshake (desktop-console plan Task 3.2; design section
// 7.1, D7, and the skew row of section 6.3).
//
// The plan's own tests for this task are the boundary table here and
// `test/e2e/skew.spec.ts`, which draws the banner, the reduced mode and the
// refusal in the console's real window under its real CSP. This file holds the
// same facts at the level that runs anywhere -- the placement, the parse, and
// what the banner draws into a fake element -- for the same reason
// `degraded.test.ts` exists beside `degraded.spec.ts`: the e2e suite needs a
// display, and a boundary that only a display can catch is a boundary that
// stops being checked on a machine without one.
//
// What is held here, in the plan's own words:
//
//   - **the range check's boundaries** -- the built minor, the previous one, one
//     below the floor, one above the ceiling -- each as a named case, so a
//     mutation to the comparison fails a test whose name says which side broke;
//   - **the human-line parse against a captured `fugaro version` output** -- the
//     real line, both of the forms a real build prints, which is why this task
//     settles the design's UNVERIFIED item 3: `version` has no `--json`
//     (`internal/cli/root.go`'s `newVersionCmd` declares no flags and prints one
//     `Fprintln` of the `Version` variable), so the console parses the human
//     line and this file pins that parse to what a binary actually prints;
//   - **the below-floor case draws no dashboard** -- the plan's red step, held
//     at the model here and at the window in `window.test.ts`, so that a console
//     that drew the spend figures anyway fails both;
//   - and, because this task draws a string a binary chose, the rule that the
//     string a *screen* draws is the parsed one and never the line as printed:
//     an unparseable line -- including one that carried a credential -- is
//     dropped where it was read, and there is nothing left in the model for a
//     screen to draw.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { parseVersionLine } from '../../src/main/spawn'
import {
  BUDGET_COMPUTE_CLI_FLOOR,
  CLI_HARD_FLOOR_VERSION,
  CLI_VERSION_BUILT_AGAINST,
  CONSOLE_CLI_UPGRADE_COMMAND,
  HIDDEN_VIEW_WORDS,
  LAUNCH_CLI_FLOOR,
  NOTICE_REASONS,
  REDUCED_VIEWS,
  SKEW_HEADLINES,
  SUPPORTED_CLI_MINORS,
  UNREADABLE_VERSION,
  classifyCliVersion,
  budgetComputeSupported,
  compareMinors,
  hidesView,
  launchSupported,
  parseCliVersion,
  renderVersionSkew,
  skewSentence,
  unreadableCompatibility,
  unreadableReason,
} from '../../src/shared/version'
import type { CliCompatibility, ReducedView, SkewElement } from '../../src/shared/version'
import { NOTICE_SHOW_DETAILS } from '../../src/shared/notice'
import { githubToken } from '../support/fakeSecrets'

// ---------------------------------------------------------------------------
// A captured `fugaro version`.
// ---------------------------------------------------------------------------

/**
 * CAPTURED_RELEASE_OUTPUT is what a released binary prints, captured from a
 * build with the release ldflags --
 * `-X github.com/dimipaun/fugaro/internal/cli.Version=0.8.1` -- which is what
 * `.goreleaser.yaml` passes (`{{ .Version }}`, and goreleaser's version has no
 * leading `v`), and which matches `docs/releases/v0.8.1`, the highest release at
 * the time of writing.
 *
 * It is a **captured** string and not a constructed one: the point of pinning
 * the parse is that the console agrees with the binary, and a value this file
 * made up would only prove that the parser agrees with itself. If `newVersionCmd`
 * ever changes what it prints, this test is where the console finds out.
 */
const CAPTURED_RELEASE_OUTPUT = '0.8.1\n'

/**
 * CAPTURED_0_12_1_OUTPUT and CAPTURED_0_13_0_OUTPUT are what the `v0.12.1` and
 * `v0.13.0` binaries print, captured on 2026-10-30 from builds of those tags
 * with the release ldflags: the two releases the owner found this console
 * reducing, which the set now names.
 */
const CAPTURED_0_12_1_OUTPUT = '0.12.1\n'
const CAPTURED_0_13_0_OUTPUT = '0.13.0\n'

/** CAPTURED_DEV_OUTPUT is what a binary built without those ldflags prints:
 * `var Version = "dev"` in `internal/cli/root.go`, unchanged by the build. This
 * is the line every developer running `go run ./cmd/fugaro version` sees, and it
 * is why `classifyCliVersion` has a case for it that is not "unreadable". */
const CAPTURED_DEV_OUTPUT = 'dev\n'

// ---------------------------------------------------------------------------
// The fake screen.
// ---------------------------------------------------------------------------

/** FakeScreen is the smallest thing a banner may draw into: the same fake
 * `live.test.ts` and `degraded.test.ts` hand their screens, with the same
 * refusal of every attribute that could make a window *do* something. */
class FakeScreen implements SkewElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set(['href', 'src', 'srcdoc', 'style', 'action'])

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a banner may not set the ${name} attribute`)
    }
    if (lowered === 'class') {
      this.classes.length = 0
      this.classes.push(...value.split(/\s+/).filter((one) => one !== ''))
    }
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: SkewElement): void {
    this.nodes.push(child as FakeScreen)
  }
}

const factory = { create: (tag: string): FakeScreen => new FakeScreen(tag) }

/** walk visits every node of a tree, the root first. */
function walk(node: SkewElement): readonly FakeScreen[] {
  const fake = node as FakeScreen
  const out: FakeScreen[] = [fake]
  for (const child of fake.nodes) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: SkewElement, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** one is the single node carrying a class, asserted to be single. */
function one(node: SkewElement, className: string): FakeScreen {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** drawn is the whole tree's text, in document order. */
function drawn(node: SkewElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

// ---------------------------------------------------------------------------
// The parse, against the captured line.
// ---------------------------------------------------------------------------

describe('parseCliVersion', () => {
  it('reads the captured output of a release build', () => {
    const parsed = parseCliVersion(CAPTURED_RELEASE_OUTPUT)
    expect(parsed).toEqual({
      ok: true,
      version: { kind: 'release', raw: '0.8.1', major: 0, minor: 8, patch: 1 },
    })
  })

  it('reads the captured output of a dev build, as its own kind', () => {
    expect(parseCliVersion(CAPTURED_DEV_OUTPUT)).toEqual({
      ok: true,
      version: { kind: 'dev', raw: 'dev' },
    })
  })

  it('reads a git-describe or tag-shaped line by its numeric core', () => {
    // `.goreleaser.yaml` strips the leading `v`, but a binary built by hand from
    // a tag (`go build -ldflags "-X ...Version=$(git describe --tags)"`) prints
    // one, and `0.8.1-3-gabc1234` is what `git describe` appends. Both are the
    // same release to the comparison and both are kept raw for the screen.
    for (const line of ['v0.8.1', '0.8.1-rc.1', '0.8.1+meta', 'v0.8.1-3-gabc1234']) {
      const parsed = parseCliVersion(line)
      expect(parsed.ok, line).toBe(true)
      if (!parsed.ok || parsed.version.kind !== 'release') throw new Error(line)
      expect(`${parsed.version.major}.${parsed.version.minor}.${parsed.version.patch}`, line).toBe('0.8.1')
      expect(parsed.version.raw, line).toBe(line)
    }
  })

  it('trims the newline a Fprintln always writes, and tolerates blank lines', () => {
    expect(parseCliVersion('0.8.1')).toEqual(parseCliVersion('0.8.1\n'))
    expect(parseCliVersion('\n0.8.1\n\n')).toEqual(parseCliVersion('0.8.1\n'))
  })

  it('refuses a line that is not a version rather than guessing at one', () => {
    const refused = [
      '',
      '\n\n',
      'usage: fugaro version',
      'fugaro version 0.8.1',
      'error: something went wrong',
      '0.8.1\n0.9.0\n',
      '9'.repeat(201),
    ]
    for (const text of refused) {
      expect(parseCliVersion(text).ok, JSON.stringify(text.slice(0, 40))).toBe(false)
    }
  })

  it('refuses a version component too long to be a number, and the handshake reads it as unreadable', () => {
    // `Number.parseInt` of a 30-digit component is a float that has lost its
    // digits (and `1e400` digits is Infinity), so a comparison against the
    // supported range would place a line it cannot actually read. Fail closed.
    const huge = '9'.repeat(30)
    for (const line of [`${huge}.8.1`, `0.${huge}.1`, `0.8.${huge}`, `v0.8.${huge}-rc.1`]) {
      expect(parseCliVersion(line).ok, line).toBe(false)
      expect(classifyCliVersion(line).skew, line).toBe('unreadable')
    }
    // Nine digits per component is still a version.
    expect(parseCliVersion('0.8.999999999').ok).toBe(true)
  })

  it('keeps the refused line out of the reason, because the reason is drawn', () => {
    // The reason a screen or a title attribute shows is the console's own
    // sentence. `src/main/spawn.ts`'s `parseVersionLine` makes the opposite
    // choice for its own message (`JSON.stringify(line)`), which is the
    // resolver's to make; here the string goes no further than this function.
    const parsed = parseCliVersion('fugaro 0.8.1: unexpected argument "0.8.1"')
    if (parsed.ok) throw new Error('expected a refusal')
    expect(parsed.reason).not.toContain('0.8.1')
    expect(parsed.reason).not.toContain('unexpected')
  })

  it('accepts exactly the lines the binary resolver accepts', () => {
    // Two doors onto one line: the resolver (`src/main/spawn.ts`) refuses to run
    // a binary whose version it cannot read, and the handshake places the line
    // it reads. A grammar that drifted between them would be a console that
    // resolves a binary and then cannot read it, so both are held to the same
    // table.
    const table = [
      CAPTURED_RELEASE_OUTPUT,
      CAPTURED_DEV_OUTPUT,
      'v0.8.1',
      '0.8.1-rc.1',
      '0.8.1+meta',
      '',
      '\n',
      'usage: fugaro version',
      '0.8.1\n0.9.0\n',
      '9'.repeat(201),
      'fugaro version 0.8.1',
    ]
    for (const text of table) {
      expect(parseCliVersion(text).ok, JSON.stringify(text.slice(0, 40))).toBe(parseVersionLine(text).ok)
    }
  })
})

// ---------------------------------------------------------------------------
// The range and the floor.
// ---------------------------------------------------------------------------

describe('the supported range', () => {
  it('holds the minors whose documents were read, the built one the newest', () => {
    const built = parseCliVersion(CLI_VERSION_BUILT_AGAINST)
    if (!built.ok || built.version.kind !== 'release') throw new Error(CLI_VERSION_BUILT_AGAINST)
    const previous = `${String(built.version.major)}.${String(built.version.minor - 1)}`
    const builtMinor = `${String(built.version.major)}.${String(built.version.minor)}`
    // The design's original pair, V2.5.1a's launch minor, and the two re-read on
    // 2026-10-30 (0.12 and 0.13, the built minor). A member dropped or a minor
    // added without its re-read fails here.
    expect(SUPPORTED_CLI_MINORS).toEqual(['0.7', '0.8', '0.11', previous, builtMinor])
    expect(CLI_VERSION_BUILT_AGAINST).toBe('0.13.0')
    // The built minor is the newest member: it is the line above which a CLI is
    // newer than this console, and a member above it would be a promise about
    // documents the console was not built against.
    for (const minor of SUPPORTED_CLI_MINORS) {
      expect(compareMinors(minor, builtMinor), minor).toBeLessThanOrEqual(0)
    }
    // The launch floor's minor is a member; the floor itself is a *patch*
    // inside it, so the comparison that matters is the one its own tests make.
    expect(SUPPORTED_CLI_MINORS).toContain(LAUNCH_CLI_FLOOR.split('.').slice(0, 2).join('.'))
  })

  it('answers ok for the newest released CLI, so a release cannot leave the console behind', () => {
    // The release checklist's guard (2026-10-30): the console read 0.12.x and
    // 0.13.0 in the reduced mode for weeks because nothing failed when the CLI
    // shipped past the set. `docs/release-streams.yaml`'s CLI `current` is the
    // last *released* CLI (`next` is only a name the next minor will take, so a
    // planned release is never read here). If this fails, the next console
    // release re-reads that minor's documents and adds it to
    // SUPPORTED_CLI_MINORS (src/shared/version.ts).
    const streams = readFileSync(join(__dirname, '..', '..', '..', 'docs', 'release-streams.yaml'), 'utf8')
    const cli = /- name: cli\n(?:[ \t]+[^\n]*\n)*?[ \t]+current: "([^"]+)"/.exec(streams)
    const released = cli?.[1]
    if (released === undefined) throw new Error('docs/release-streams.yaml has no CLI `current` line')
    expect(released, 'the CLI stream is at a release').toMatch(/^\d+\.\d+\.\d+$/)
    const answer = classifyCliVersion(`${released}\n`)
    expect(
      answer.mode,
      `the released CLI ${released} must answer ok: add its minor to SUPPORTED_CLI_MINORS once its documents are re-read`,
    ).toBe('ok')
    expect(answer.skew, `the released CLI ${released} is not one of the minors this console has read`).toBe('supported')
  })

  it('is a release version the console can read, like the floor below it', () => {
    for (const constant of [CLI_VERSION_BUILT_AGAINST, CLI_HARD_FLOOR_VERSION]) {
      const parsed = parseCliVersion(constant)
      expect(parsed.ok, constant).toBe(true)
      if (!parsed.ok) throw new Error(constant)
      expect(parsed.version.kind, constant).toBe('release')
    }
  })

  it('has a hard floor one minor below the oldest supported minor', () => {
    const oldest = SUPPORTED_CLI_MINORS[0]
    if (oldest === undefined) throw new Error('no supported minors')
    const [major, minor] = oldest.split('.').map((part) => Number.parseInt(part, 10))
    if (major === undefined || minor === undefined) throw new Error(oldest)
    expect(CLI_HARD_FLOOR_VERSION).toBe(`${String(major)}.${String(minor - 1)}.0`)
  })

  it('offers the CLI its own upgrade command, not one this console invented', () => {
    // `internal/updatecheck`'s `BrewCommand`, which is the fix line the CLI's own
    // version nag prints. A console that told the operator to run something else
    // would be a second source of truth about how fugaro is installed.
    expect(CONSOLE_CLI_UPGRADE_COMMAND).toBe('brew upgrade dimipaun/tap/fugaro')
  })
})

// ---------------------------------------------------------------------------
// The launch floor.
// ---------------------------------------------------------------------------

describe('launchSupported', () => {
  it('is the design’s floor: the release that ships --plan-task', () => {
    // `--plan-task`, `internal/cli/recipes_skew.go`'s planTaskSince = "0.11.2",
    // is the newest flag the argv carries, and rule 11's own sentence is that
    // the operation is offered only to an installation whose CLI is at least
    // that (docs/design/desktop-console.md §5.1 rule 11, "a CLI too old for a
    // flag of the row").
    expect(LAUNCH_CLI_FLOOR).toBe('0.11.2')
    // And the floor is a **patch** inside that minor, which is exactly what a
    // range check cannot answer: `0.11` as a range member puts `0.11.0` and
    // `0.11.1` on the offering side of a floor they are below.
    const floor = parseCliVersion(LAUNCH_CLI_FLOOR)
    if (!floor.ok || floor.version.kind !== 'release') throw new Error(LAUNCH_CLI_FLOOR)
    expect(floor.version.patch).toBeGreaterThan(0)
  })

  it('admits a version at or above the floor, patch level by patch level', () => {
    for (const line of ['0.11.2', 'v0.11.2', '0.11.3', '0.11.99', '0.12.0', '0.12.1', '1.0.0']) {
      expect(launchSupported(line), line).toBe(true)
    }
    // A build-metadata tail is the same release, and the same comparison.
    expect(launchSupported('0.11.2+build.7')).toBe(true)
    // And the two patches below it are refused, which is the whole reason this
    // is a patch comparison: 0.11.0 and 0.11.1 are inside the supported minor
    // "0.11" and carry no --plan-task.
    for (const line of ['0.11.1', '0.11.0', '0.10.9', '0.8.1', '0.6.0']) {
      expect(launchSupported(line), line).toBe(false)
    }
  })

  it('is a comparison of its own, which range membership cannot stand in for', () => {
    // The two ways the range would answer wrongly, and both are live: 0.11 is a
    // supported minor and its first two patches are below the floor, while the
    // floor's own minor is in the range for a reason the range cannot state --
    // "in range" is neither necessary nor sufficient for a launch.
    expect(SUPPORTED_CLI_MINORS).toContain('0.11')
    expect(classifyCliVersion('0.11.0').mode).toBe('ok')
    expect(launchSupported('0.11.0')).toBe(false)
    expect(classifyCliVersion('0.10.0').mode).toBe('reduced')
    expect(launchSupported('0.10.0')).toBe(false)
    // The other direction: a line above the floor that the handshake reduces --
    // a build tail is the same release to the floor, and not a plain release
    // number to the handshake -- is still a launch, and a banner is not a
    // refusal.
    expect(classifyCliVersion('0.12.1+build.7').mode).toBe('reduced')
    expect(launchSupported('0.12.1+build.7')).toBe(true)
  })

  it('refuses a line it cannot place, rather than assuming the newest', () => {
    // The reason this is not `parseCliVersion`'s answer alone: the prerelease
    // tail is deliberately not compared by that parser (0.11.2-rc.1 is read as
    // 0.11.2), and a `dev` build is unplaceable. Both gate a spend, so both are
    // refusals here.
    for (const line of ['0.11.2-rc.1', '0.11.2-rc', 'dev', 'devel', '', 'the binary did not answer']) {
      expect(launchSupported(line), JSON.stringify(line)).toBe(false)
    }
    // A minor that names no patch is read as its x.y.0, and the floor is a
    // patch: `0.12` is above it and `0.11` is below it.
    expect(launchSupported('0.12')).toBe(true)
    expect(launchSupported('0.11')).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// The compute floor.
// ---------------------------------------------------------------------------

describe('budgetComputeSupported', () => {
  it('is the release that ships `budget show --compute`', () => {
    // The flag landed on the CLI's main on 2026-10-29 and no released CLI
    // carries it: `v0.12.1` is the newest tag, and it answers `--compute` with
    // an unknown-flag error. 0.13.0 is the release that ships it.
    expect(BUDGET_COMPUTE_CLI_FLOOR).toBe('0.13.0')
    expect(budgetComputeSupported('0.12.1')).toBe(false)
  })

  it('admits a version at or above the floor, patch level by patch level', () => {
    for (const line of ['0.13.0', 'v0.13.0', '0.13.1', '0.13.99', '0.14.0', '1.0.0']) {
      expect(budgetComputeSupported(line), line).toBe(true)
    }
    expect(budgetComputeSupported('0.13.0+build.7')).toBe(true)
    for (const line of ['0.12.99', '0.12.1', '0.12.0', '0.11.2', '0.6.0']) {
      expect(budgetComputeSupported(line), line).toBe(false)
    }
  })

  it('refuses a line it cannot place, rather than asking an older CLI for the flag', () => {
    // The same rule `launchSupported` states: a prerelease is read as its
    // release by `parseCliVersion`, and a `dev` build is unplaceable, so both
    // are refusals here rather than an argv an older CLI rejects.
    for (const line of ['0.13.0-rc.1', '0.13.0-rc', 'dev', 'devel', '', 'the binary did not answer']) {
      expect(budgetComputeSupported(line), JSON.stringify(line)).toBe(false)
    }
    // A minor that names no patch is read as its x.y.0.
    expect(budgetComputeSupported('0.13')).toBe(true)
    expect(budgetComputeSupported('0.12')).toBe(false)
  })

  it('is a patch comparison of its own, which range membership cannot stand in for', () => {
    // The flag is opt-in on a command the console draws in both modes, so the
    // question is the floor's and never the range's: 0.12 is outside the
    // supported range and above the launch floor, and it is still below this
    // one.
    expect(classifyCliVersion('0.12.1').mode).toBe('ok')
    expect(launchSupported('0.12.1')).toBe(true)
    expect(budgetComputeSupported('0.12.1')).toBe(false)
    expect(budgetComputeSupported(LAUNCH_CLI_FLOOR)).toBe(false)
  })

  it('is asked of the floor itself, whose own minor the range reaches later', () => {
    // The minors this walks are the floor's, not `SUPPORTED_CLI_MINORS`: the
    // range is widened to the minor that carries the flag by the same change
    // that flips the Spend screen's read, so a case asserting over the range
    // would go red on that commit for a reason that is not this comparison's.
    // What the predicate promises is a patch comparison against the floor, and
    // every patch of every minor below it is a refusal however close.
    const [floorMajor = '0', floorMinorText = '0'] = BUDGET_COMPUTE_CLI_FLOOR.split('.')
    const floorMinor = Number(floorMinorText)
    expect(floorMinor).toBeGreaterThan(0)
    for (let minor = 0; minor < floorMinor; minor += 1) {
      for (const patch of [0, 1, 9]) {
        const line = `${floorMajor}.${String(minor)}.${String(patch)}`
        expect(budgetComputeSupported(line), line).toBe(false)
      }
    }
    // The other half is the range's, and it is the reason the predicate is not
    // enough on its own: `ok` is exact-minor membership, and the caps read is
    // withheld in every mode that is not `ok` (`REDUCED_VIEW_OPERATIONS.caps`,
    // `src/main/ipc.ts`) -- so a CLI at the floor is drawn in the reduced mode
    // with its caps read withheld while the floor's minor is outside the range,
    // and the flag being passable and the figure being askable land together
    // only when the range widens to that minor.
    const atFloor = classifyCliVersion(BUDGET_COMPUTE_CLI_FLOOR)
    const inRange = SUPPORTED_CLI_MINORS.includes(`${floorMajor}.${String(floorMinor)}`)
    expect(atFloor.mode).toBe(inRange ? 'ok' : 'reduced')
    expect(hidesView(atFloor, 'caps')).toBe(!inRange)
  })
})

// ---------------------------------------------------------------------------
// The placement, boundary by boundary.
// ---------------------------------------------------------------------------

/** placed is a one-line summary of a placement, for a table that names its
 * cases rather than repeating four expectations per row. */
function placed(text: string): string {
  const compatibility = classifyCliVersion(text)
  return `${compatibility.mode}/${compatibility.skew}/${compatibility.cliVersion ?? 'null'}`
}

describe('classifyCliVersion', () => {
  it('proceeds, and draws no banner, inside the range', () => {
    // The built minor -- the version the console was built and tested against --
    // including the patch levels around the captured one, because the patch
    // level is not an axis of the JSON contract.
    for (const line of [CAPTURED_RELEASE_OUTPUT, 'v0.8.1', '0.8.0', '0.8.99']) {
      const compatibility = classifyCliVersion(line)
      expect(placed(line), line).toMatch(/^ok\/supported\//)
      expect(compatibility.hidden, line).toEqual([])
      expect(hidesView(compatibility, 'spend'), line).toBe(false)
      expect(hidesView(compatibility, 'caps'), line).toBe(false)
      expect(hidesView(compatibility, 'kill'), line).toBe(false)
    }
    // The previous minor -- the other half of the range -- is supported too, and
    // this is the boundary the plan names: a range of one minor only would fail
    // here.
    for (const line of ['0.7.0', '0.7.9']) {
      expect(placed(line), line).toMatch(/^ok\/supported\//)
    }
  })

  it('reads the two releases the owner found reduced, as members', () => {
    for (const line of [CAPTURED_0_12_1_OUTPUT, CAPTURED_0_13_0_OUTPUT, '0.12.0', '0.13.9']) {
      const compatibility = classifyCliVersion(line)
      expect(placed(line), line).toMatch(/^ok\/supported\//)
      expect(compatibility.hidden, line).toEqual([])
      for (const view of REDUCED_VIEWS) expect(hidesView(compatibility, view), `${line} ${view}`).toBe(false)
    }
  })

  it('answers ok for every plain release above the newest member, however far above', () => {
    // The owner's ruling of 2026-10-30: a CLI newer than the console is read
    // with the fields the console knows, and a read that no longer decodes is
    // refused on its own screen. `0.130.0` is minor 130, not 0.13 with a
    // trailing zero, and is placed as such.
    for (const line of ['0.14.0', 'v0.14.0', '0.14.3', '0.99.0', '0.130.0', '1.0.0', '2.3.4']) {
      const compatibility = classifyCliVersion(line)
      expect(placed(line), line).toBe(`ok/newer/${line}`)
      expect(compatibility.hidden, line).toEqual([])
    }
  })

  it('reduces the gap below the newest member, as an older CLI', () => {
    // 0.9 and 0.10 were never re-read: older than the console, above the floor.
    for (const line of ['0.9.0', '0.10.0', '0.10.7', '0.6.5']) {
      const compatibility = classifyCliVersion(line)
      expect(placed(line), line).toMatch(/^reduced\/older\//)
      expect(compatibility.hidden, line).toEqual([...REDUCED_VIEWS])
    }
  })

  it('opens ok to a plain release number only: a hostile line does not ride the open end', () => {
    // Every line here parses (it is a version to `spawn.ts`'s resolver too) and
    // none is a number a release prints, so none may answer `ok` -- not above
    // the set, where the answer is open-ended, and not inside a member minor
    // either.
    const irregular = [
      '0.13.0-evil',
      '0.13.0+x',
      '0.13.0-rc.1',
      '0.14.0-rc.1',
      '0.14.0+build.7',
      '1.0.0-0',
      '00.13.0',
      '0.013.0',
      '0.14.00',
      '0.13',
      '0.14',
      '0.14.0.1',
      'v0.14.0-3-gabc1234',
    ]
    for (const line of irregular) {
      const compatibility = classifyCliVersion(line)
      expect(placed(line), line).toBe(`reduced/irregular/${line}`)
      expect(compatibility.hidden, line).toEqual([...REDUCED_VIEWS])
    }
    // Whitespace: the newline `Fprintln` writes and blank lines around it are
    // the same line (`parseCliVersion`), and whitespace inside it is not a
    // version at all.
    expect(placed(' 0.14.0\t\n')).toBe('ok/newer/0.14.0')
    for (const line of ['0. 14.0', '0.14 .0', '0.14.0 evil', 'O.14.0', '0.x.0', '\u00a00.14.0\u200b']) {
      expect(placed(line), JSON.stringify(line)).toMatch(/^refused\/unreadable\//)
    }
    // And below the floor a tail does not soften the refusal.
    expect(placed('0.5.0-rc.1')).toBe('refused/below-floor/0.5.0-rc.1')
  })

  it('reduces, rather than refuses, between the floor and the range', () => {
    // The degraded band: older than the range, at or above the floor. The floor
    // itself is inside this band -- refusing the version the floor names would
    // make the constant a lie about what it means.
    for (const line of [CLI_HARD_FLOOR_VERSION, '0.6.9']) {
      const compatibility = classifyCliVersion(line)
      expect(placed(line), line).toMatch(/^reduced\/older\//)
      expect(compatibility.hidden, line).toEqual([...REDUCED_VIEWS])
    }
  })

  it('refuses, one below the floor', () => {
    const compatibility = classifyCliVersion('0.5.0')
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.skew).toBe('below-floor')
    expect(compatibility.cliVersion).toBe('0.5.0')
    // A refused console withholds every view, and `src/main/window.ts` withholds
    // the window with them: the plan's red step is that the dashboard is not
    // drawn, and this is the model half of it.
    expect(compatibility.hidden).toEqual([...REDUCED_VIEWS])
    expect(hidesView(compatibility, 'spend')).toBe(true)
  })

  it('refuses every version below the floor, not just the next one down', () => {
    for (const line of ['0.5.9', '0.4.0', '0.0.1', '0.1.0']) {
      expect(placed(line), line).toMatch(/^refused\/below-floor\//)
    }
  })

  it('reduces an unplaceable dev build rather than refusing it', () => {
    // `dev` is the line a working copy prints. It is newer than any release by
    // construction and no comparison can place it, so it proceeds in the reduced
    // mode with the banner naming it -- refusing would block the one person
    // building the CLI and the console together, and trusting it silently would
    // draw figures nothing vouched for.
    const compatibility = classifyCliVersion(CAPTURED_DEV_OUTPUT)
    expect(compatibility.mode).toBe('reduced')
    expect(compatibility.skew).toBe('unplaceable')
    expect(compatibility.cliVersion).toBe('dev')
    expect(compatibility.hidden).toEqual([...REDUCED_VIEWS])
  })

  it('names dev as the one non-version line it accepts, and guesses at no other', () => {
    // The boundary the plan's open item 3 settles (`docs/plans/2026-10-15-desktop-console.md`,
    // answered by the owner 2026-10-22): a build without the release `-ldflags`
    // prints `dev` -- a non-semver string the console *recognises* and reduces --
    // while any other line that is not a version stays the refusal `refuses a
    // line it cannot read` asserts. Widening the acceptance to "any non-semver
    // word" is the mutation this holds back: `dev` is the CLI's own constant
    // (`internal/cli/root.go`: `var Version = "dev"`), and a console that
    // accepted a word it does not recognise would be placing a line it cannot
    // read, which is what D7 forbids.
    expect(placed('dev'), 'the CLI\'s own dev constant').toMatch(/^reduced\/unplaceable\//)
    for (const line of ['devel', 'snapshot', 'unknown', 'dev-0.11.0']) {
      expect(placed(line), line).toMatch(/^refused\/unreadable\//)
    }
  })

  it('refuses a line it cannot read, and carries no version', () => {
    for (const text of ['', 'usage: fugaro version', 'not a version']) {
      const compatibility = classifyCliVersion(text)
      expect(compatibility.mode, text).toBe('refused')
      expect(compatibility.skew, text).toBe('unreadable')
      // The unreadable line is *dropped*: there is no `null`-versus-text
      // decision left to a screen, because there is no text.
      expect(compatibility.cliVersion, text).toBeNull()
    }
  })

  it('refuses rather than falling through to "assume it is supported"', () => {
    // The mutation this guards: an unreadable line that reached the comparison
    // and was placed as though it were in range. Design section 3.4 rule 4
    // already refuses to *run* a binary whose version cannot be read; a console
    // that ran it and drew its figures would be the same mistake with a worse
    // consequence.
    const compatibility = classifyCliVersion(githubToken())
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.cliVersion).toBeNull()
    // And a credential a wrapper echoed is not carried anywhere: not into the
    // version, not into a reason, not into anything a screen could draw.
    expect(JSON.stringify(compatibility)).not.toContain(githubToken())
  })

  it('places a version by its minor alone, on both sides', () => {
    expect(compareMinors('0.8', '0.8')).toBe(0)
    expect(compareMinors('0.7', '0.8')).toBeLessThan(0)
    expect(compareMinors('0.9', '0.8')).toBeGreaterThan(0)
    expect(compareMinors('1.0', '0.9')).toBeGreaterThan(0)
    expect(compareMinors('0.9', '1.0')).toBeLessThan(0)
    // Every supported minor is >= the hard floor, so the set and the floor
    // cannot meet.
    for (const minor of SUPPORTED_CLI_MINORS) {
      expect(compareMinors(minor, CLI_HARD_FLOOR_VERSION), minor).toBeGreaterThanOrEqual(0)
    }
    // No member is above the minor the console was built against: that minor
    // is where "newer than this console" starts.
    const aboveBuilt = SUPPORTED_CLI_MINORS.filter((minor) => compareMinors(minor, CLI_VERSION_BUILT_AGAINST) > 0)
    expect(aboveBuilt).toEqual([])
  })
})

describe('unreadableCompatibility', () => {
  it('is a refusal that keeps the console\'s own reason for it', () => {
    const compatibility = unreadableCompatibility('the binary did not answer')
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.skew).toBe('unreadable')
    expect(compatibility.cliVersion).toBeNull()
    expect(unreadableReason(compatibility)).toBe('the binary did not answer')
  })

  it('has no reason to draw when the placement read a line', () => {
    expect(unreadableReason(classifyCliVersion(CAPTURED_RELEASE_OUTPUT))).toBeNull()
    expect(unreadableReason(classifyCliVersion(CAPTURED_DEV_OUTPUT))).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// What the banner draws.
// ---------------------------------------------------------------------------

describe('skewSentence', () => {
  it('names both versions and the range, in the console\'s own words', () => {
    // Every mode's sentence, including the refusal and the unreadable line: the
    // design's requirement is that the banner names both versions, and the one
    // way to keep that true for a mode someone adds later is to hold all of them
    // to it.
    const sentences = [
      ...['0.7.0', '0.9.0', '0.14.0', '0.14.0-rc.1', '0.5.0', CAPTURED_DEV_OUTPUT, CAPTURED_RELEASE_OUTPUT].map((line) =>
        skewSentence(classifyCliVersion(line)),
      ),
      skewSentence(unreadableCompatibility('the binary did not answer')),
    ]
    for (const sentence of sentences) {
      expect(sentence).toContain(CLI_VERSION_BUILT_AGAINST)
      expect(sentence).toContain(SUPPORTED_CLI_MINORS.join(', '))
      expect(sentence.length).toBeGreaterThan(40)
    }
    // The two modes that mean "upgrade something" name what to upgrade -- for an
    // old CLI the CLI, and for a new one the console (there is no command for
    // that: the console is not installed by brew).
    expect(skewSentence(classifyCliVersion('0.14.0'))).toContain('update the console')
    expect(skewSentence(classifyCliVersion('0.14.0'))).toContain('older than the fugaro it is driving')
    expect(skewSentence(classifyCliVersion('0.14.0'))).not.toContain(CONSOLE_CLI_UPGRADE_COMMAND)
    expect(skewSentence(classifyCliVersion('0.9.0'))).toContain(CONSOLE_CLI_UPGRADE_COMMAND)
    expect(skewSentence(classifyCliVersion('0.14.0-rc.1'))).toContain('not a plain release number')
    expect(skewSentence(classifyCliVersion('0.5.0'))).toContain(CONSOLE_CLI_UPGRADE_COMMAND)
    expect(skewSentence(classifyCliVersion(CLI_HARD_FLOOR_VERSION))).toContain(CONSOLE_CLI_UPGRADE_COMMAND)
    expect(skewSentence(classifyCliVersion('0.5.0'))).toContain(CLI_HARD_FLOOR_VERSION)
  })

  it('never composes the line the binary printed into the sentence', () => {
    // The sentence is the console's, so it is the same string whatever the
    // binary said. The version the binary reported is drawn in its own cell,
    // through `putText`, and nowhere else -- which is what keeps a string from
    // outside out of prose this console wrote.
    const sentence = skewSentence(classifyCliVersion('v0.8.1-3-gabc1234'))
    expect(sentence).not.toContain('gabc1234')
    expect(sentence).not.toContain('0.8.1-3')
  })

  it('has a headline for every mode, and a word for every withheld view', () => {
    expect(Object.keys(SKEW_HEADLINES).sort()).toEqual(['ok', 'reduced', 'refused'])
    for (const view of REDUCED_VIEWS) {
      expect(HIDDEN_VIEW_WORDS[view].length, view).toBeGreaterThan(0)
    }
    expect(Object.keys(HIDDEN_VIEW_WORDS).sort()).toEqual([...REDUCED_VIEWS].sort())
  })
})

describe('NOTICE_REASONS', () => {
  it('is one plain sentence per drawn skew, and nothing else in it', () => {
    // The card's reason is what a reader gets before they open anything, so it
    // is one sentence: exactly one full stop, no semicolon, and no version
    // number, range or command -- the technical text is the details below it.
    expect(Object.keys(NOTICE_REASONS).sort()).toEqual([
      'below-floor',
      'irregular',
      'older',
      'unplaceable',
      'unreadable',
    ])
    for (const [name, sentence] of Object.entries(NOTICE_REASONS)) {
      expect(sentence.endsWith('.'), `${name}: ${sentence}`).toBe(true)
      expect(sentence.slice(0, -1), name).not.toContain('.')
      expect(sentence, name).not.toContain(';')
      expect(sentence).not.toContain(CONSOLE_CLI_UPGRADE_COMMAND)
      expect(sentence).not.toContain(CLI_VERSION_BUILT_AGAINST)
      expect(sentence).not.toContain(SUPPORTED_CLI_MINORS.join(', '))
    }
  })
})

describe('renderVersionSkew', () => {
  it('draws the card: the title, the reason, both versions and what is withheld', () => {
    const compatibility = classifyCliVersion('0.9.0')
    const { root } = renderVersionSkew(factory, compatibility, 'light')
    expect(root.getAttribute('class')).toBe('notice console-skew')
    expect(root.getAttribute('data-mode')).toBe('reduced')
    expect(root.getAttribute('data-skew')).toBe('older')
    expect(root.getAttribute('data-theme')).toBe('light')
    expect(root.getAttribute('data-severity')).toBe('warning')
    // `console-skew` and not `live`: a refused console draws no dashboard, and
    // the absence of that root is how a spec says so.
    expect(all(root, 'live')).toHaveLength(0)
    const title = one(root, 'notice-title')
    expect(title.tag).toBe('h2')
    // The card's own id, and the root names itself by it: the id is the card's,
    // because a document may hold more than one card (`notice.test.ts`).
    expect(title.getAttribute('id')).not.toBeNull()
    expect(root.getAttribute('aria-labelledby')).toBe(title.getAttribute('id'))
    expect(title.textContent).toBe(SKEW_HEADLINES.reduced)
    expect(one(root, 'skew-reason').textContent).toBe(NOTICE_REASONS.older)
    // The technical text is in the facts, behind the disclosure.
    expect(one(root, 'skew-sentence').textContent).toBe(skewSentence(compatibility))
    // The console's own version and the CLI's reported one, each in its own
    // cell: the first is a constant of this console, the second is a string the
    // binary chose and goes through `versionText`.
    expect(one(root, 'skew-console-version').textContent).toBe(CLI_VERSION_BUILT_AGAINST)
    expect(one(root, 'skew-cli-version').textContent).toBe('0.9.0')
    const hidden = all(root, 'skew-hidden')
    expect(hidden.map((node) => node.getAttribute('data-hidden'))).toEqual([...REDUCED_VIEWS])
    expect(hidden.map((node) => node.textContent)).toEqual(REDUCED_VIEWS.map((view) => HIDDEN_VIEW_WORDS[view]))
    // A label and its value are two elements with a space between them. The
    // strip this card replaces drew them as spans in a row and read `reads0.9.0`
    // in a text run; a `dl` cannot.
    expect(drawn(one(root, 'notice-facts'))).toContain('this console reads ' + CLI_VERSION_BUILT_AGAINST)
    expect(drawn(one(root, 'notice-facts'))).toContain('the fugaro it is driving reports 0.9.0')
  })

  it('folds the details behind a real button, and the button says which way it goes', () => {
    const { root, toggle, details } = renderVersionSkew(factory, classifyCliVersion('0.9.0'), 'light')
    expect(toggle).not.toBeNull()
    expect(toggle?.tag).toBe('button')
    expect(toggle?.getAttribute('type')).toBe('button')
    expect(toggle?.getAttribute('aria-expanded')).toBe('false')
    expect(toggle?.getAttribute('aria-controls')).toBe(details.getAttribute('id'))
    expect(toggle?.textContent).toBe(NOTICE_SHOW_DETAILS)
    expect(details.getAttribute('hidden')).toBe('')
    // The dismissal is the reader's, so the card has one.
    expect(all(root, 'notice-dismiss')).toHaveLength(1)
  })

  it('draws the refusal screen, and it is not a dashboard', () => {
    const { root, toggle, dismiss, details } = renderVersionSkew(factory, classifyCliVersion('0.5.0'), 'dark')
    expect(root.getAttribute('data-mode')).toBe('refused')
    expect(one(root, 'notice-title').tag).toBe('h1')
    expect(one(root, 'notice-title').textContent).toBe(SKEW_HEADLINES.refused)
    expect(one(root, 'skew-reason').textContent).toBe(NOTICE_REASONS['below-floor'])
    expect(one(root, 'skew-cli-version').textContent).toBe('0.5.0')
    // The whole document: no `live` root, and nothing that *does* something --
    // the refusal screen has no control in it at all.
    expect(all(root, 'live')).toHaveLength(0)
    expect(walk(root).filter((node) => node.tag === 'a' || node.tag === 'button')).toHaveLength(0)
    expect(toggle).toBeNull()
    expect(dismiss).toBeNull()
    // Its details are open: the range and the version line are what the operator
    // acts on, and there is no second screen to open them from.
    expect(details.getAttribute('hidden')).toBeNull()
  })

  it('says the console could not read a version rather than drawing a blank', () => {
    const { root } = renderVersionSkew(factory, classifyCliVersion('usage: fugaro version'), 'light')
    expect(one(root, 'skew-cli-version').textContent).toBe(UNREADABLE_VERSION)
    expect(one(root, 'skew-reason').textContent).toBe(NOTICE_REASONS.unreadable)
    expect(drawn(root)).not.toContain('usage')
  })

  it('draws the reader\'s own reason as a fact, so there is something to act on', () => {
    // A compatibility built by `unreadableCompatibility` is the reader's answer
    // after a spawn that failed or a read that did not finish. "It could not
    // read a version" without the why is a refusal screen with no next step, so
    // the reason the reader kept is drawn -- sanitized, like every other text
    // this module writes.
    const reason = 'the fugaro binary did not answer within 5000ms; run `fugaro version` yourself'
    const compatibility = unreadableCompatibility(reason)
    const { root } = renderVersionSkew(factory, compatibility, 'light')
    expect(unreadableReason(compatibility)).toBe(reason)
    expect(one(root, 'skew-because').textContent).toBe(reason)
    // In its own fact, not inside the sentence: the sentence stays the console's.
    expect(one(root, 'skew-sentence').textContent).not.toContain('5000ms')
    expect(drawn(one(root, 'notice-facts'))).toContain('why ' + reason)
  })

  it('draws no reason fact when the placement read a line', () => {
    for (const line of [CAPTURED_RELEASE_OUTPUT, CAPTURED_DEV_OUTPUT, '0.9.0', '0.5.0', 'not a version']) {
      const { root } = renderVersionSkew(factory, classifyCliVersion(line), 'light')
      expect(all(root, 'skew-because'), line).toHaveLength(0)
    }
  })

  it('redacts a token a reader put in the reason, and still draws the reason', () => {
    // The reason is a string a program wrote, so it takes the free-text door:
    // a credential-shaped run inside it is redacted in place rather than taking
    // the whole sentence down. The operator keeps the failure ("the reader
    // answered with ..."), and the token is not on screen -- nor in an
    // attribute, since the redaction happens before the sink.
    const token = githubToken()
    const compatibility = unreadableCompatibility(`the reader answered with ${token}`)
    const { root } = renderVersionSkew(factory, compatibility, 'light')
    const reason = unreadableReason(compatibility)
    expect(reason).not.toBeNull()
    expect(reason ?? '').toContain('the reader answered with')
    expect(one(root, 'skew-because').textContent).toBe(reason)
    expect(drawn(root)).not.toContain(token)
    expect(JSON.stringify([...walk(root)].map((node) => [...node.attributes]))).not.toContain(token)
  })

  it('draws no withheld list when there is nothing withheld', () => {
    const { root } = renderVersionSkew(factory, classifyCliVersion(CAPTURED_RELEASE_OUTPUT), 'light')
    expect(root.getAttribute('data-mode')).toBe('ok')
    expect(all(root, 'skew-hidden')).toHaveLength(0)
    expect(one(root, 'skew-sentence').textContent).toBe(skewSentence(classifyCliVersion(CAPTURED_RELEASE_OUTPUT)))
  })

  it('draws only the attributes a card may carry, as a screen may', () => {
    // No `href`, no `src`, no `style`, nothing that starts with `on`: the
    // disclosure and the dismissal are real buttons with real ARIA, and that is
    // the whole of what the card adds to the strip it replaces.
    const allowed = /^(?:class|title|id|type|tabindex|hidden|data-[a-z0-9-]+|aria-[a-z0-9-]+)$/
    for (const line of [CAPTURED_RELEASE_OUTPUT, '0.9.0', '0.5.0', CAPTURED_DEV_OUTPUT, 'not a version']) {
      const { root } = renderVersionSkew(factory, classifyCliVersion(line), 'light')
      for (const node of walk(root)) {
        for (const name of node.attributes.keys()) {
          expect(allowed.test(name.toLowerCase()), `${line}: ${name}`).toBe(true)
        }
      }
    }
  })

  it('draws a version the CLI printed through the sanitizer, not around it', () => {
    // A version line is a string the CLI chose, so it goes through the same door
    // as every other one. The interesting case is not a plausible line -- the
    // parse refuses those -- but a line *this* parser accepted that the
    // sanitizer still has an opinion about: a build tag with a `-` and a long
    // hex-looking run is a version to the grammar and a possible token to the
    // token rules, and whatever the sanitizer decides is what is drawn.
    const long = `0.9.0-${'a1b2c3d4'.repeat(6)}`
    const compatibility: CliCompatibility = classifyCliVersion(long)
    expect(compatibility.cliVersion, 'the grammar accepts the line').toBe(long)
    const { root } = renderVersionSkew(factory, compatibility, 'light')
    const cell = one(root, 'skew-cli-version')
    const text = cell.textContent ?? ''
    const attribute = cell.getAttribute('title')
    // Either it is drawn as the sanitizer drew it, or the console's own
    // sentence is drawn instead -- and the raw line is never on screen in the
    // refused case.
    if (text === long) {
      expect(attribute).toBeNull()
    } else {
      expect(text).toBe(UNREADABLE_VERSION)
      expect(drawn(root)).not.toContain(long)
    }
  })

  it('draws no string the binary printed when it could not read one', () => {
    const token = githubToken()
    const compatibility = classifyCliVersion(`${token}\n`)
    const { root } = renderVersionSkew(factory, compatibility, 'light')
    expect(drawn(root)).not.toContain(token)
    expect(JSON.stringify([...walk(root)].map((node) => [...node.attributes]))).not.toContain(token)
  })
})

describe('hidesView', () => {
  it('withholds the spend figures in every mode that is not "ok"', () => {
    const reductions: readonly CliCompatibility[] = [
      classifyCliVersion('0.9.0'),
      classifyCliVersion(CLI_HARD_FLOOR_VERSION),
      classifyCliVersion(CAPTURED_DEV_OUTPUT),
      classifyCliVersion('0.5.0'),
      unreadableCompatibility('the binary did not answer'),
      // The other road to "unreadable": a version read that succeeded and
      // printed something that is not a version (`window.ts` hands it straight
      // to `classifyCliVersion`). It must withhold what the read failure does.
      classifyCliVersion(''),
      classifyCliVersion('usage: fugaro version'),
      classifyCliVersion('not a version'),
    ]
    for (const compatibility of reductions) {
      for (const view of REDUCED_VIEWS) {
        expect(hidesView(compatibility, view), `${compatibility.skew}/${view}`).toBe(true)
      }
    }
  })

  it('withholds nothing in the supported range', () => {
    const compatibility = classifyCliVersion(CAPTURED_RELEASE_OUTPUT)
    for (const view of REDUCED_VIEWS) expect(hidesView(compatibility, view)).toBe(false)
  })

  it('is a closed list of views, and names only views the console has', () => {
    const views: readonly ReducedView[] = ['spend', 'caps', 'kill']
    expect([...REDUCED_VIEWS]).toEqual(views)
  })
})
