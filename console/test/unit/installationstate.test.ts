// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The rail's classification, the checkouts the console may offer, and the
// open-first-readable rule (owner's design ruling of 2026-10-25).
//
// Every case below is one of the owner's own four reasons an installation
// "cannot be read", plus the one warning that is not a refusal, plus the two
// rules about *which* row the console opens and *which* directories it offers.
// The names are invented (`aurora`, `orbit`): the console's tests never name a
// real project.

import { describe, expect, it } from 'vitest'

import {
  MAX_DETAIL_LINES,
  NO_ISSUE,
  classifyInstallation,
  detailsFor,
  firstReadableIndex,
  isStaleWarning,
  isToolOwnedPath,
  namesInstallation,
  openProject,
  staleLineFor,
  suggestedCheckouts,
} from '../../src/main/installationstate'
import type { ConsoleFacts, InstallationState, RowFacts } from '../../src/main/installationstate'

const INSTALL_COMMAND = 'brew install dimipaun/tap/fugaro'

/** facts is a console with nothing wrong with it, with the caller's overrides. */
function facts(overrides: Partial<ConsoleFacts> = {}): ConsoleFacts {
  return {
    storeRefusal: '',
    storePath: '/home/operator/.config/fugaro-console/installations.json',
    cliRefusal: '',
    installCommand: INSTALL_COMMAND,
    warnings: [],
    ...overrides,
  }
}

/** row is one readable, listed installation. */
function row(overrides: Partial<RowFacts> = {}): RowFacts {
  return { project: 'aurora', listed: true, configUnreadable: false, ...overrides }
}

/** staleLine is the CLI's own staleness sentence for a project, spelled the way
 * `internal/cli/config_list.go` prints it. */
function staleLine(project: string): string {
  return `${project}'s shared config was last checked 83h2m41s ago, over the 24h0m0s the reader refreshes it within`
}

describe('classifyInstallation', () => {
  it('marks a readable installation as no issue at all', () => {
    expect(classifyInstallation(row(), facts())).toEqual(NO_ISSUE)
  })

  it('marks every row when the console’s own store file is refused', () => {
    // A refused store is not one row's problem: a choose writes that file, so
    // the console can open nothing at all while it is unreadable. The sentence
    // names the path and the refusal, and offers no command -- the file is the
    // operator's own.
    const state = classifyInstallation(row(), facts({ storeRefusal: 'the file is not JSON' }))
    expect(state.issue).toBe('store')
    expect(state.readable).toBe(false)
    expect(state.command).toBe('')
    expect(state.sentence).toContain('/home/operator/.config/fugaro-console/installations.json')
    expect(state.sentence).toContain('the file is not JSON')
  })

  it('marks every row when no CLI was found, with the resolution’s own sentence and the install command', () => {
    const state = classifyInstallation(
      row({ listed: false }),
      facts({ cliRefusal: 'no fugaro binary was found: looked at /opt/homebrew/bin, /usr/local/bin' }),
    )
    expect(state.issue).toBe('binary')
    expect(state.readable).toBe(false)
    expect(state.command).toBe(INSTALL_COMMAND)
    expect(state.sentence).toContain('no fugaro binary was found')
    expect(state.sentence).toContain(INSTALL_COMMAND)
  })

  it('marks a row whose config the CLI refused, and lifts the path out of the CLI’s own line', () => {
    const state = classifyInstallation(
      row({ configUnreadable: true }),
      facts({
        warnings: [
          '~/.config/fugaro/projects/aurora.yaml could not be read: field base_imagez not found in type config.Project',
        ],
      }),
    )
    expect(state.issue).toBe('config')
    expect(state.readable).toBe(false)
    expect(state.command).toBe('fugaro doctor --project aurora')
    expect(state.sentence).toContain('edit ~/.config/fugaro/projects/aurora.yaml:')
    expect(state.sentence).toContain('field base_imagez not found')
    expect(state.sentence).toContain('fugaro doctor --project aurora')
    expect(state.details).toHaveLength(1)
  })

  it('still names the file when the CLI printed no line for the refused config', () => {
    const state = classifyInstallation(row({ configUnreadable: true }), facts())
    expect(state.issue).toBe('config')
    expect(state.sentence).toContain('aurora')
    expect(state.sentence).toContain('fugaro doctor --project aurora')
  })

  it('classifies the shared-config staleness line as stale, and still opens the installation', () => {
    // The owner's ruling: "classify it as stale ... and still open that
    // installation if the reader returned its fields". The CLI read this
    // installation and answered with its fields; only the shared copy it merged
    // is older than the reader refreshes it within.
    const state = classifyInstallation(row(), facts({ warnings: [staleLine('aurora')] }))
    expect(state.issue).toBe('stale')
    expect(state.readable).toBe(true)
    expect(state.command).toBe('fugaro doctor --project aurora')
    expect(state.sentence).toContain('stale')
    expect(state.details).toEqual([staleLine('aurora')])
  })

  it('does not read another installation’s staleness line as this one’s', () => {
    // `aurora` is a prefix of `aurora-2`: a mark driven by the bare name would
    // put one installation's warning on another's row.
    const state = classifyInstallation(row({ project: 'aurora' }), facts({ warnings: [staleLine('aurora-2')] }))
    expect(state.issue).toBe('none')
    expect(state.details).toEqual([])
  })

  it('does not read another installation’s refused config as this one’s', () => {
    const state = classifyInstallation(
      row({ project: 'aurora', configUnreadable: true }),
      facts({ warnings: ['~/.config/fugaro/projects/aurora-2.yaml could not be read: nope'] }),
    )
    expect(state.details).toEqual([])
  })

  it('keeps a row’s own config refusal ahead of a staleness line', () => {
    const state = classifyInstallation(
      row({ configUnreadable: true }),
      facts({ warnings: [staleLine('aurora'), '~/.config/fugaro/projects/aurora.yaml could not be read: nope'] }),
    )
    expect(state.issue).toBe('config')
    expect(state.readable).toBe(false)
  })
})

describe('namesInstallation', () => {
  it('recognises both of the CLI’s two shapes and nothing else', () => {
    expect(namesInstallation('~/.config/fugaro/projects/orbit.yaml could not be read: x', 'orbit')).toBe(true)
    expect(namesInstallation(staleLine('orbit'), 'orbit')).toBe(true)
    expect(namesInstallation('the remembered checkouts could not be read, so no checkout is listed: x', 'orbit')).toBe(false)
    expect(namesInstallation('', 'orbit')).toBe(false)
    expect(namesInstallation('anything', '')).toBe(false)
  })

  it('matches a refused config path native Windows printed with backslashes', () => {
    const line = 'C:\\Users\\operator\\AppData\\Roaming\\fugaro\\projects\\orbit.yaml could not be read: x'
    expect(namesInstallation(line, 'orbit')).toBe(true)
    expect(namesInstallation(line, 'aurora')).toBe(false)
    expect(namesInstallation(line.replace('orbit.yaml', 'orbit-2.yaml'), 'orbit')).toBe(false)
  })

  it('reads the path the line starts with, not a path the reason mentions (review m2)', () => {
    // Another installation's refusal that mentions this one's file is that
    // other installation's line: drawn here, it would send the operator to edit
    // the wrong file.
    const other = '~/.config/fugaro/projects/orbit.yaml could not be read: it includes ~/.config/fugaro/projects/aurora.yaml'
    expect(namesInstallation(other, 'aurora')).toBe(false)
    expect(namesInstallation(other, 'orbit')).toBe(true)
    // A directory whose name merely ends in `projects` is not the projects one.
    expect(namesInstallation('/home/operator/myprojects/aurora.yaml could not be read: x', 'aurora')).toBe(false)
    expect(namesInstallation('projects/aurora.yaml could not be read: x', 'aurora')).toBe(true)
  })
})

describe('staleLineFor and isStaleWarning', () => {
  it('requires both the installation’s name and the CLI’s own marker', () => {
    const line = staleLine('orbit')
    expect(staleLineFor('orbit', [line])).toBe(line)
    expect(isStaleWarning(line, 'orbit')).toBe(true)
    // The name without the marker is not the staleness sentence.
    expect(isStaleWarning("orbit's shared config was merged from the bucket", 'orbit')).toBe(false)
    // The marker without this installation's name is not its line.
    expect(isStaleWarning(staleLine('aurora'), 'orbit')).toBe(false)
  })
})

describe('detailsFor', () => {
  it('keeps only the lines about this installation', () => {
    const lines = [staleLine('aurora'), 'the remembered checkouts could not be read: x', '~/.config/fugaro/projects/aurora.yaml could not be read: y']
    expect(detailsFor('aurora', lines)).toHaveLength(2)
  })

  it('names the lines it did not keep rather than shortening silently', () => {
    const lines = Array.from({ length: MAX_DETAIL_LINES + 3 }, () => staleLine('aurora'))
    const kept = detailsFor('aurora', lines)
    expect(kept).toHaveLength(MAX_DETAIL_LINES + 1)
    expect(kept[kept.length - 1]).toContain('3 more lines')
  })
})

describe('isToolOwnedPath', () => {
  const temp = '/var/folders/x1/T'

  it('refuses the owner’s own example: a scratchpad under a temporary directory', () => {
    expect(isToolOwnedPath('/private/tmp/claude-501/-work-repo/scratchpad/wt-merge', temp)).toBe(true)
  })

  it('refuses every temporary root the ruling names', () => {
    expect(isToolOwnedPath('/tmp/aurora', temp)).toBe(true)
    expect(isToolOwnedPath('/private/tmp/aurora', temp)).toBe(true)
    expect(isToolOwnedPath('/var/folders/x1/T/aurora', temp)).toBe(true)
    expect(isToolOwnedPath('/private/var/folders/x1/T/aurora', temp)).toBe(true)
    expect(isToolOwnedPath(`${temp}/aurora`, temp)).toBe(true)
  })

  it('refuses a scratchpad segment anywhere, and an agent harness’s worktree', () => {
    expect(isToolOwnedPath('/home/operator/scratchpad/aurora', temp)).toBe(true)
    expect(isToolOwnedPath('/home/operator/src/.claude/worktrees/wt-1', temp)).toBe(true)
  })

  it('keeps an ordinary checkout, and refuses only whole segments', () => {
    expect(isToolOwnedPath('/home/operator/src/aurora', temp)).toBe(false)
    expect(isToolOwnedPath('/home/operator/tmp/aurora', temp)).toBe(false)
    expect(isToolOwnedPath('/home/operator/scratchpad-old/aurora', temp)).toBe(false)
    expect(isToolOwnedPath('/home/operator/src/.claude/worktrees-old', temp)).toBe(false)
    expect(isToolOwnedPath('', temp)).toBe(false)
  })

  it('reads a Windows spelling with the same segments', () => {
    expect(isToolOwnedPath('C:\\Users\\operator\\scratchpad\\aurora', temp)).toBe(true)
    expect(isToolOwnedPath('C:\\Users\\operator\\src\\aurora', temp)).toBe(false)
  })

  it('normalises the path and compares without case before it decides (review m1)', () => {
    expect(isToolOwnedPath('/TMP/aurora', temp)).toBe(true)
    expect(isToolOwnedPath('/Private/Tmp/aurora', temp)).toBe(true)
    expect(isToolOwnedPath('/Users/operator/Scratchpad/aurora', temp)).toBe(true)
    expect(isToolOwnedPath('/home/operator/src/.Claude/Worktrees/wt-1', temp)).toBe(true)
    expect(isToolOwnedPath('/home/operator/../../tmp/aurora', temp)).toBe(true)
    expect(isToolOwnedPath('/home/operator/./src/../src/aurora', temp)).toBe(false)
    // A Windows temporary directory, spelled in another case than the OS answered it.
    const windowsTemp = 'C:\\Users\\operator\\AppData\\Local\\Temp'
    expect(isToolOwnedPath('c:\\users\\Operator\\appdata\\local\\temp\\aurora', windowsTemp)).toBe(true)
    expect(isToolOwnedPath('C:\\Users\\operator\\src\\..\\AppData\\Local\\Temp\\aurora', windowsTemp)).toBe(true)
  })
})

describe('suggestedCheckouts', () => {
  const temp = '/tmp'

  it('offers the project’s own recorded checkout first', () => {
    expect(suggestedCheckouts('/home/operator/src/aurora', ['/home/operator/other/aurora', '/home/operator/src/aurora'], temp)).toEqual([
      '/home/operator/src/aurora',
      '/home/operator/other/aurora',
    ])
  })

  it('never offers a temporary or tool-owned directory', () => {
    expect(
      suggestedCheckouts(undefined, ['/private/tmp/claude-501/scratchpad/wt-merge', '/home/operator/src/aurora'], temp),
    ).toEqual(['/home/operator/src/aurora'])
  })

  it('drops a recorded checkout that is itself tool-owned', () => {
    expect(suggestedCheckouts('/tmp/aurora', ['/home/operator/src/aurora'], temp)).toEqual(['/home/operator/src/aurora'])
  })

  it('keeps the recorded checkout even when the CLI no longer lists it', () => {
    expect(suggestedCheckouts('/home/operator/src/aurora', [], temp)).toEqual(['/home/operator/src/aurora'])
  })

  it('offers nothing when there is nothing to offer', () => {
    expect(suggestedCheckouts(undefined, [], temp)).toEqual([])
  })

  it('refuses a directory whose real path is tool-owned, as a symlink into one is (review m1)', () => {
    const real = (path: string): string => (path === '/home/operator/link' ? '/tmp/real' : path)
    expect(
      suggestedCheckouts('/home/operator/link', ['/home/operator/link', '/home/operator/src/aurora'], temp, undefined, real),
    ).toEqual(['/home/operator/src/aurora'])
  })
})

describe('firstReadableIndex', () => {
  const state = (issue: InstallationState['issue'], readable: boolean): InstallationState => ({
    issue,
    readable,
    sentence: '',
    command: '',
    details: [],
  })

  it('is the first row the console can open, in the rows’ own order', () => {
    // The owner's case: the previously chosen installation is unreadable and
    // another is readable. The console opens the readable one -- the first one
    // it can, not a preference among the readable ones.
    expect(firstReadableIndex([state('config', false), state('none', true), state('none', true)])).toBe(1)
  })

  it('counts a stale row as readable, because the CLI returned its fields', () => {
    expect(firstReadableIndex([state('config', false), state('stale', true)])).toBe(1)
  })

  it('is -1 when no row is readable, which is the state the issue view is drawn for', () => {
    expect(firstReadableIndex([state('config', false), state('store', false)])).toBe(-1)
    expect(firstReadableIndex([])).toBe(-1)
  })
})

describe('openProject', () => {
  const state = (issue: InstallationState['issue'], readable: boolean): InstallationState => ({
    issue,
    readable,
    sentence: '',
    command: '',
    details: [],
  })
  const row = (project: string, active: boolean, issue: InstallationState['issue'], readable: boolean) => ({
    project,
    active,
    state: state(issue, readable),
  })

  it('is the remembered choice when the CLI’s document says it can be read', () => {
    // The operator's choice, wherever it sits in the CLI's order: a console that
    // opened the first row of every launch would throw that choice away at each
    // start, and "switching is a click on the rail" would be worth one session.
    expect(openProject([row('aurora', false, 'none', true), row('orbit', true, 'none', true)])).toBe('orbit')
  })

  it('is the first readable row when the remembered one cannot be read', () => {
    expect(openProject([row('aurora', true, 'config', false), row('orbit', false, 'none', true)])).toBe('orbit')
  })

  it('is the first readable row when the console has chosen none', () => {
    expect(openProject([row('aurora', false, 'none', true), row('orbit', false, 'none', true)])).toBe('aurora')
  })

  it('counts a stale row as openable, which is the owner’s "still open that installation"', () => {
    expect(openProject([row('aurora', true, 'stale', true), row('orbit', false, 'none', true)])).toBe('aurora')
  })

  it('is the first row when nothing can be read, which is the row the issue view is drawn for', () => {
    expect(openProject([row('aurora', false, 'config', false), row('orbit', false, 'store', false)])).toBe('aurora')
  })

  it('is null when the machine has no installation at all', () => {
    expect(openProject([])).toBe(null)
  })
})
