// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The two onboarding screens' facts, held to the repository's own copies of them
// (owner's ruling of 2026-10-20; brief requirement 4; design section 3.4 rule 7).
//
// There are exactly two screens for a first run that has nothing to draw, and
// each offers **one** fact the operator acts on:
//
//   1. no `fugaro` binary: the install command, `FUGARO_INSTALL_COMMAND`;
//   2. a binary and no installation: the setup command, `FUGARO_SETUP_COMMAND`.
//
// Both are strings this repository already states somewhere else, and that is
// the whole risk: a console constant that drifts from the command the project
// documents sends an operator to a command that does not exist. So each is
// pinned here to the file it is the *fourth* copy of:
//
//   - the install command to `website/data/install.yaml` -- the one install
//     source the site renders, whose own Go test
//     (`internal/cli/docs_website_install_test.go`) pins every command in it to
//     the file its `source:` names -- and to the `README.md` line that source
//     names;
//   - the setup command to `README.md`'s getting-started steps and to the
//     cobra command itself, `internal/cli/setup.go`'s `setupDoor` (`name:
//     "setup"`) -- which is the thing that would have to change for the string
//     to be wrong.
//
// The link is pinned the same way: the URL is the page the website serves,
// `website/src/pages/getting-started.astro`, so the two cannot drift apart.
//
// **The mutation each test catches**: change one character of a constant, or
// change the file it points at, and the test goes red by name. The reverse --
// a `website/data/install.yaml` that renamed the cask -- fails the same way,
// which is the point: the console cannot quietly disagree with the site.

import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { FUGARO_INSTALL_COMMAND, FUGARO_SETUP_COMMAND, GETTING_STARTED_URL, NO_ONBOARDING, firstInstallationState, installCliState } from '../../src/main/onboarding'
import { FUGARO_INSTALL_COMMAND as SOURCE_INSTALL_COMMAND } from '../../src/source/cli'

const REPO_ROOT = fileURLToPath(new URL('../../..', import.meta.url))

/** read is one file of the repository, read as text rather than parsed: these
 * pins are about the bytes a reader of that file sees. */
function read(relative: string): string {
  return readFileSync(new URL(`../../../${relative}`, import.meta.url), 'utf8')
}

describe('the install command', () => {
  it('is the command the website’s install source carries, byte for byte', () => {
    // `website/data/install.yaml` is the site's own source for the Homebrew
    // cask's command, and its Go test pins it to README.md in turn -- so pinning
    // to it here pins to the whole chain without this test re-implementing it.
    const install = read('website/data/install.yaml')
    const commands = install
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.startsWith('brew install '))
    expect(commands).toContain(FUGARO_INSTALL_COMMAND)
    // The non-vacuity half: the file really does carry a cask install command,
    // so the assertion above is not passing on an empty list.
    expect(commands.length).toBeGreaterThan(0)
  })

  it('is the command README.md’s getting-started steps carry', () => {
    expect(read('README.md')).toContain(FUGARO_INSTALL_COMMAND)
  })

  it('is one string in the console, not two that agree today', () => {
    // The screen's copy comes from `src/main/onboarding.ts`, which re-exports the
    // constant `src/source/cli.ts` has carried since Task 4.4. Two spellings of
    // one command is exactly the drift this file exists to catch, so the
    // re-export is asserted rather than assumed.
    expect(FUGARO_INSTALL_COMMAND).toBe(SOURCE_INSTALL_COMMAND)
    expect(FUGARO_INSTALL_COMMAND).toBe('brew install dimipaun/tap/fugaro')
  })
})

describe('the setup command', () => {
  it('is the command README.md tells the operator to run', () => {
    // The step, not merely the token: `fugaro setup` appears many times in
    // README.md, and the *first* one is the getting-started step that sets a
    // machine up. The sentence around it is asserted too, so a README that
    // renamed the command in that step would fail here by line.
    const readme = read('README.md')
    const step = readme.split('\n').find((line) => line.includes(FUGARO_SETUP_COMMAND))
    expect(step, `README.md must carry ${FUGARO_SETUP_COMMAND}`).toBeDefined()
    expect(step).toContain('1. Run')
  })

  it('is the command the CLI itself declares', () => {
    // `setupDoor`'s `name: "setup"` (`internal/cli/setup.go`) is what makes
    // `fugaro setup` the command: the string in the console is derived from the CLI's own name
    // for it, and this is the assertion that keeps the two together -- the
    // command is *built from the value the CLI declares*, so a rename in that
    // file is a red step here rather than a note beside a string nobody compares.
    const source = read('internal/cli/setup.go')
    const door = source.slice(source.indexOf('var setupDoor = convergeDoor{'))
    const declared = /name:\s*"([^"\s]+)"/.exec(door)?.[1]
    expect(declared, 'internal/cli/setup.go must declare setupDoor with name: "<name>"').toBe('setup')
    expect(FUGARO_SETUP_COMMAND).toBe(`fugaro ${declared ?? ''}`)
    // A later rename is the one change that must fail here, and it fails
    // *because of the derivation above*: renaming `setupDoor` in
    // `internal/cli/setup.go` makes `declared` disagree with the console's
    // constant, so the equalities above go red by name.
  })

  it('points at the plan that renames it, in one place', () => {
    // The brief asks for the text to live in ONE constant with a comment
    // pointing at `docs/setup-wizard/`: when the rename lands, one line changes.
    const module = readFileSync(new URL('../../src/main/onboarding.ts', import.meta.url), 'utf8')
    expect(module).toContain('docs/setup-wizard/')
    // Comments come out first: this module discusses the command at length on
    // purpose, and the claim is about the *code* -- one constant and no second
    // spelling of it.
    const code = module.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
    expect(code.match(/fugaro setup/g) ?? []).toHaveLength(1)
  })
})

describe('the getting-started link', () => {
  it('is the page the website serves, and the one host the console opens', () => {
    // `website/src/pages/getting-started.astro` is served by Astro at
    // `/getting-started/`: the path in the URL is the file's own name, and the
    // host is the site's. Nothing here fetches the page -- the console embeds no
    // part of the website (design section 5.1 rule 3) -- it only names where the
    // operator is sent.
    expect(GETTING_STARTED_URL).toBe('https://fugaro.dev/getting-started/')
    const page = read('website/src/pages/getting-started.astro')
    expect(page).toContain('getting-started.astro')
    expect(new URL(GETTING_STARTED_URL).pathname).toBe('/getting-started/')
    // ...and the repository's own site facts name the host, so a site that moved
    // to another domain fails here rather than sending operators to a dead link.
    // `website/data/site.yaml` is where the site writes its canonical URL down
    // (`website/astro.config.mjs` deliberately holds none), and `allowed_hosts`
    // is its own list of the hosts it is allowed to link to -- the site's
    // spelling of the same rule `src/main/window.ts` holds the console to.
    const site = read('website/data/site.yaml')
    expect(site).toContain(`canonical_url: https://${new URL(GETTING_STARTED_URL).host}`)
    expect(site).toContain(`  - ${new URL(GETTING_STARTED_URL).host}`)
  })
})

describe('the two states', () => {
  it('carries the resolution’s own sentence and its own list, frozen', () => {
    const state = installCliState({ reason: 'no fugaro binary was found: looked at /usr/local/bin/fugaro', searched: ['/usr/local/bin/fugaro'] })
    expect(state).toEqual({
      kind: 'install-cli',
      reason: 'no fugaro binary was found: looked at /usr/local/bin/fugaro',
      searched: ['/usr/local/bin/fugaro'],
      command: FUGARO_INSTALL_COMMAND,
    })
    // A value the screen may hold: nothing here is a getter over state that a
    // later read could change under it, and the array is a copy rather than the
    // caller's own.
    expect(Object.isFrozen(state)).toBe(true)
    const searched = ['/tmp/one']
    expect(installCliState({ reason: 'x', searched })).not.toBe(searched)
  })

  it('is a small closed union, and `none` is shared', () => {
    // The picker draws a list, or it draws one of these two screens -- there is
    // no third, and a state that grew a `kind` would fail here.
    expect(NO_ONBOARDING).toEqual({ kind: 'none' })
    expect(Object.isFrozen(NO_ONBOARDING)).toBe(true)
    expect(firstInstallationState()).toEqual({ kind: 'first-installation', command: FUGARO_SETUP_COMMAND })
    // The two screens' commands are different commands: a copy-paste that gave
    // the second screen the install command would leave an operator with a CLI
    // and no installation, told to install the CLI again.
    expect(FUGARO_SETUP_COMMAND).not.toBe(FUGARO_INSTALL_COMMAND)
    // The path arithmetic this file's `read()` uses, checked as the property it
    // is reaching for: `../../..` from this spec must land on the repository
    // root, so every `read('website/…')` above reads the file it names. (It used
    // to assert that the checkout's path contains the substring "repo", which is
    // true of a local `/work/repo/…` and false of GitHub's
    // `/home/runner/work/fugaro/fugaro/console` -- a fact about this machine.)
    expect(existsSync(join(REPO_ROOT, 'go.mod')), 'the path arithmetic must reach the repository root').toBe(true)
    expect(existsSync(join(REPO_ROOT, 'console', 'package.json'))).toBe(true)
  })
})
