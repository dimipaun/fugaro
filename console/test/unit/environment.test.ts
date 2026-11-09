// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The environment view says which side of the boundary the credentials are on
// (the console-on-Windows plan's T-G5; reconciliation F21, D5(a); design
// section 3.4 rule 7).
//
// **The claim, in one paragraph.** The console holds no credential and reads
// none: the child gets a fixed environment (`src/main/spawn.ts`'s
// `CHILD_ENV_KEYS`) and finds its identity the way a terminal does, in the home
// directory of the environment it runs in. On the WSL route that environment is
// the distribution's, and `gcloud auth application-default login` run there
// writes `~/.config/gcloud/application_default_credentials.json` **inside the
// distribution** while the same command on Windows writes
// `%APPDATA%\gcloud\application_default_credentials.json`
// ([docs/windows-cli/wsl.md](../../../docs/windows-cli/wsl.md), its second
// trap, F39). So a login on Windows does not sign the CLI in, `fugaro doctor`
// answers "not authenticated", and the operator -- who has just signed in and
// watched it work in a browser -- is owed the reason rather than an afternoon
// (F21). That is what the console's own sentence here is for.
//
// **What this file holds, and why it is a test rather than a comment.** The two
// halves the plan's red step names:
//
//   1. **with a WSL installation the environment view states that the CLI's
//      credentials are the distribution's** -- in the console's own words, a
//      constant of `src/renderer/screens/environment.ts` and not a document
//      string, drawn through that module's `literal` (`screen-literals.test.ts`
//      holds the constant to that shape), and **absent** for an installation on
//      this machine, whose three lines stay exactly as they were;
//   2. **the console reads no token in either case** -- the view is a pure
//      function of the installation answer, its own module opens no file and
//      names no credential variable, and a token planted in what the answer
//      carries is not drawn. The checker below is proven against the mutations
//      it exists for rather than trusted.
//
// The view driven here is the installation line the dashboard draws
// (`drawInstallation`, `src/renderer/entry.ts`): it is the console's statement
// of the environment it is driving -- the project, the binary, and the version
// and mode the handshake read -- and it is the only surface in this tree that
// receives the installation's `distro`, the field `ActiveInstallation` carries
// "exactly when the active installation is a WSL one" for this sentence
// (`src/main/installations.ts`, T-D1). The doctor document itself is drawn by
// the Project tab's credentials-service and policy panels, and neither of those
// sections is handed an installation: they draw the CLI's own words about its
// own state, which is a different question from which side of the boundary the
// CLI is on.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { drawInstallation } from '../../src/renderer/entry'
import {
  ENVIRONMENT_CREDENTIALS_HELD_BY_CONSOLE,
  ENVIRONMENT_CREDENTIALS_HELD_CLASS,
  ENVIRONMENT_CREDENTIALS_IN_DISTRO,
  ENVIRONMENT_CREDENTIALS_SIDE_CLASS,
  runsInsideADistribution,
} from '../../src/renderer/screens/environment'
import { sanitizeRefusal, sanitizeText } from '../../src/shared/sanitize'
import { githubToken } from '../support/fakeSecrets'

const ENVIRONMENT_MODULE = fileURLToPath(new URL('../../src/renderer/screens/environment.ts', import.meta.url))

// ---------------------------------------------------------------------------
// The fake screen.
// ---------------------------------------------------------------------------

/** FakeScreen is the smallest thing the installation line may draw into: the
 * same fake `degraded.test.ts` hands the degraded screens, with the same
 * refusal of every attribute that could make a window *do* something. */
class FakeScreen {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set([['h', 'ref'].join(''), 'src', 'srcdoc', 'style'])

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a screen may not set the ${name} attribute`)
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

  append(child: FakeScreen): void {
    this.nodes.push(child)
  }

  childNodes(): readonly FakeScreen[] {
    return this.nodes
  }
}

const factory = { create: (tag: string): FakeScreen => new FakeScreen(tag) }

/** walk visits every node of a tree, the root first. */
function walk(node: FakeScreen): readonly FakeScreen[] {
  const out: FakeScreen[] = [node]
  for (const child of node.childNodes()) out.push(...walk(child))
  return out
}

/** texts is one class's drawn text, in document order: what each node of the
 * class holds, and never the attribute beside it. */
function texts(root: FakeScreen, className: string): readonly (string | null)[] {
  return walk(root)
    .filter((node) => node.classes.includes(className))
    .map((node) => node.textContent)
}

/** drawn is the whole tree's text, in document order. */
function drawn(root: FakeScreen): string {
  return walk(root)
    .map((node) => node.textContent ?? '')
    .join(' ')
}

/** anInstallationReply wraps one `installation.current` data member in the
 * envelope every operation answers with (`ConsoleReply`, `src/main/ipc.ts`). */
function anInstallationReply(data: Record<string, unknown>): unknown {
  return { ok: true, data }
}

/** aWslInstallation is one chosen WSL installation: the answer T-D1 carries a
 * `distro` for, and nothing else about it is special. */
function aWslInstallation(overrides: Record<string, unknown> = {}): unknown {
  return anInstallationReply({
    chosen: true,
    id: '0123456789abcdef',
    project: 'acme',
    binary: '/opt/fugaro/bin/fugaro',
    checkout: '/home/owner/acme',
    distro: 'Ubuntu-24.04',
    cliVersion: '9.9.9',
    mode: 'reduced',
    ...overrides,
  })
}

// ---------------------------------------------------------------------------
// The view.
// ---------------------------------------------------------------------------

describe('the environment view says which side the credentials are on', () => {
  it('says the credentials are the distribution’s, for an installation whose CLI runs inside one', () => {
    const section = drawInstallation(factory, aWslInstallation()) as FakeScreen
    expect(section.getAttribute('data-installation')).toBe('chosen')
    // The installation line itself is unchanged: the sentences are drawn beside
    // the project, the binary and the version, never instead of them.
    expect(walk(section).slice(1, 4).map((node) => node.textContent)).toEqual([
      'acme',
      '/opt/fugaro/bin/fugaro',
      '9.9.9 · reduced',
    ])
    expect(texts(section, ENVIRONMENT_CREDENTIALS_SIDE_CLASS)).toEqual([ENVIRONMENT_CREDENTIALS_IN_DISTRO])
    expect(texts(section, ENVIRONMENT_CREDENTIALS_HELD_CLASS)).toEqual([ENVIRONMENT_CREDENTIALS_HELD_BY_CONSOLE])
  })

  it('draws the console’s own words, and they survive both doors unchanged', () => {
    // The sentence names the distribution and where to sign in, and it is the
    // console's: nothing in it is a token, a path or a context an operator would
    // lose to the paragraph classes -- which is what "the console's own words
    // rather than a document string" has to mean for a sentence drawn beside a
    // document's.
    expect(ENVIRONMENT_CREDENTIALS_IN_DISTRO).toContain('distribution’s own')
    expect(ENVIRONMENT_CREDENTIALS_IN_DISTRO).toContain('gcloud auth application-default login')
    const sentence = sanitizeText(ENVIRONMENT_CREDENTIALS_IN_DISTRO, 'sentence')
    expect(sentence.ok && sentence.text).toBe(ENVIRONMENT_CREDENTIALS_IN_DISTRO)
    const held = sanitizeRefusal(ENVIRONMENT_CREDENTIALS_HELD_BY_CONSOLE)
    expect(held.ok && held.text).toBe(ENVIRONMENT_CREDENTIALS_HELD_BY_CONSOLE)
  })

  it('says nothing about the credentials side for an installation on this machine', () => {
    const reply = anInstallationReply({
      chosen: true,
      id: '0123456789abcdef',
      project: 'acme',
      binary: '/opt/fugaro/bin/fugaro',
      cliVersion: '9.9.9',
      mode: 'reduced',
    })
    const section = drawInstallation(factory, reply) as FakeScreen
    expect(section.childNodes().map((node) => node.textContent)).toEqual([
      'acme',
      '/opt/fugaro/bin/fugaro',
      '9.9.9 · reduced',
    ])
    expect(texts(section, ENVIRONMENT_CREDENTIALS_SIDE_CLASS)).toEqual([])
    expect(texts(section, ENVIRONMENT_CREDENTIALS_HELD_CLASS)).toEqual([])
  })

  it('says nothing for the answers that name no CLI at all', () => {
    // `runsInsideADistribution` is the whole decision, so the cases that cannot
    // have a side to state are asserted on it directly: an answer the console
    // cannot read, a store that refused, no installation chosen, and a chosen
    // installation whose `distro` is absent, empty or not a string. The empty
    // string is the source's own omission (`src/main/installations.ts` drops an
    // empty `distro` when it builds this answer), and a non-string is a field
    // this build does not read -- neither is a statement about a distribution.
    const none = [
      null,
      'an installation',
      { ok: false, reason: 'the CLI could not be asked' },
      anInstallationReply({ chosen: false, sentence: 'no installation has been chosen' }),
      anInstallationReply({ chosen: true, project: 'acme', distro: '', cliVersion: null, mode: 'unknown' }),
      anInstallationReply({ chosen: true, project: 'acme', distro: 42, cliVersion: null, mode: 'unknown' }),
    ]
    for (const reply of none) expect(runsInsideADistribution(reply), JSON.stringify(reply)).toBe(false)
    expect(runsInsideADistribution(aWslInstallation())).toBe(true)
  })

  it('draws no token, for an installation on either side of the boundary', () => {
    const token = githubToken()
    const hostReply = anInstallationReply({
      chosen: true,
      project: 'acme',
      binary: token,
      cliVersion: '9.9.9',
      mode: 'reduced',
    })
    for (const reply of [aWslInstallation({ binary: token }), hostReply]) {
      const text = drawn(drawInstallation(factory, reply) as FakeScreen)
      expect(text).not.toContain(token)
      expect(text).toContain('redacted')
    }
  })
})

// ---------------------------------------------------------------------------
// The read that never happens.
// ---------------------------------------------------------------------------

/** codeOnly is a module's source with its comments removed: the scan below is
 * about what the code *does*, and this module discusses the distribution's
 * credentials file at length on purpose. */
function codeOnly(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/**
 * tokenReads returns, for every source it is given, the reasons it looks like a
 * read of a credential. It is deliberately two-part, the way
 * `configread.test.ts`'s checker is: **the environment view opens nothing at
 * all** -- it is a pure function of the installation answer, so any filesystem
 * or child-process access in it is the offence -- and **nothing in it names a
 * credential variable** (`GOOGLE_APPLICATION_CREDENTIALS` is the variable the
 * design's rule 5 forbids the console to set, and a token read out of the app's
 * own environment is the same defect from the other side).
 */
export function tokenReads(files: readonly { readonly path: string; readonly source: string }[]): readonly string[] {
  const found: string[] = []
  const opens = /\b(?:readFileSync|readFile|createReadStream|openSync|open|readdirSync|statSync|existsSync|loadFile|execSync|spawnSync)\s*\(/
  const credential = /(?:GOOGLE_APPLICATION_CREDENTIALS|CLOUDSDK_[A-Z_]*(?:TOKEN|CREDENTIALS)|application_default_credentials|access_token|refresh_token|process\.env)/
  for (const file of files) {
    const source = codeOnly(file.source)
    for (const line of source.split('\n')) {
      if (opens.test(line)) found.push(`${file.path}: opens something: ${line.trim()}`)
      if (credential.test(line)) found.push(`${file.path}: names a credential: ${line.trim()}`)
    }
  }
  return found
}

describe('the environment view reads no token, on either side of the boundary', () => {
  it('reads no credential in either case, and the checker catches the read it exists for (MUTATION)', () => {
    const source = readFileSync(ENVIRONMENT_MODULE, 'utf8')
    expect(tokenReads([{ path: 'environment.ts', source }])).toEqual([])
    // The mutations, held as fixtures rather than described: a read of the
    // distribution's own ADC file -- the shortcut F21 exists to forbid -- and a
    // token taken out of the app's environment. Both must be found.
    const planted = [
      `${source}\nfunction loadDistroCredentials() {\n  return readFileSync('/home/owner/.config/gcloud/application_default_credentials.json', 'utf8')\n}\n`,
      `${source}\nconst inherited = process.env['GOOGLE_APPLICATION_CREDENTIALS']\n`,
    ]
    for (const mutant of planted) {
      expect(tokenReads([{ path: 'environment.ts', source: mutant }]), mutant).not.toEqual([])
    }
  })

  it('draws only the two console constants, so nothing the answer carries reaches the credential lines', () => {
    // A token-shaped string planted in a member the answer carries and the view
    // does read (`distro`) reaches no line: the drawn text of the two credential
    // lines is the module's own constants, character for character.
    const planted = aWslInstallation({ distro: githubToken() })
    const section = drawInstallation(factory, planted) as FakeScreen
    expect(texts(section, ENVIRONMENT_CREDENTIALS_SIDE_CLASS)).toEqual([ENVIRONMENT_CREDENTIALS_IN_DISTRO])
    expect(texts(section, ENVIRONMENT_CREDENTIALS_HELD_CLASS)).toEqual([ENVIRONMENT_CREDENTIALS_HELD_BY_CONSOLE])
    expect(runsInsideADistribution(planted)).toBe(true)
  })
})
