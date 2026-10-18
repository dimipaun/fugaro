// The served root, the bundle in it, and the bundler that wrote both
// (desktop-console plan Task 3.4a, steps 4 and 5; design section 5.1 rules 1, 3
// and 5).
//
// Three claims are held here, and each of them is a claim the CSP alone cannot
// make:
//
//   1. **The served root holds exactly `SERVED_FILES`** (`src/main/protocol.ts`)
//      -- the two documents and the one script, and nothing else. The protocol
//      handler will serve any file under `dist/bundle/` that a URL names, so the
//      *directory* is the allow-list, and this test is what holds the directory
//      to the list a reader can see.
//   2. **The bundle is the entry and every module it requires**, by the
//      registry's own keys, and it requires nothing else: no bare specifier, no
//      `electron`, no Node built-in, no computed one. A renderer that could
//      `require('electron')` would be a renderer with a main process inside it,
//      and the sandbox it runs in has no module loader at all -- so the failure
//      this prevents is a runtime crash in a window with no Node, which is why
//      it is a build failure instead (`tools/bundle-renderer/index.ts`).
//   3. **The bundle runs as the document runs it.** The unit suite is the only
//      place this can be checked without Electron: `test/e2e/entry.spec.ts`
//      drives the real window in CI, and the `node:vm` run below drives the same
//      file against a fake document whose markup-parsing slots and navigating
//      attributes **throw**. It is a second, cheaper witness of the same fact --
//      the entry reaches the bridge and the gate draws -- and it fails locally,
//      where the e2e run cannot.
//
// `test/support/built.ts` makes the build before any of this reads it: the
// workflow runs `npm run test:unit` on a fresh checkout, with no `dist/` at all.

import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createContext, runInContext } from 'node:vm'

import { describe, expect, it } from 'vitest'

import { SERVED_FILES } from '../../src/main/protocol'
import {
  BundleRefusal,
  bundleRenderer,
  consoleBundlePaths,
  consoleBundles,
  consoleRootHere,
  isEntryPoint,
  isRelative,
  moduleClosure,
  requireSpecifiers,
  resolveSpecifier,
} from '../../tools/bundle-renderer/index'
import { CONSOLE_ROOT, builtFile, builtListing, builtPath } from '../support/built'
import {
  FakeDocument,
  FakeNode,
  aReducedCompatibility,
  anOkCompatibility,
  everyNode,
  recordingTimers,
  textOf,
} from '../support/fakeDom'

/** ENTRY is the compiled entry the build walks, and OUT the file it writes. */
const PATHS = consoleBundlePaths(CONSOLE_ROOT)

/** servedNames is `SERVED_FILES` as file names, which is what a directory
 * listing is: `/console.js` is `console.js` in the root. */
function servedNames(): readonly string[] {
  return SERVED_FILES.map((path) => path.replace(/^\//, '')).sort()
}

describe('the served root npm run build leaves', () => {
  it('holds exactly the files SERVED_FILES names', () => {
    // The non-vacuity guard first: an empty list would make the comparison
    // below pass against an empty directory, and the list is what the handler's
    // root is supposed to be.
    expect(SERVED_FILES.length, 'SERVED_FILES names nothing: the comparison below would be vacuous').toBe(5)
    expect(builtListing('bundle')).toEqual(servedNames())
  })

  it('holds the documents the sources hold, byte for byte', () => {
    for (const name of ['index.html', 'installations.html', 'refusal.html']) {
      const source = readFileSync(join(CONSOLE_ROOT, 'src', 'renderer', name), 'utf8')
      // Byte for byte, and not merely equal after parsing: the CSP `<meta>`
      // copy in each document is compared with the header
      // (`test/unit/window.test.ts`), and a build that rewrote it would make
      // that comparison about a file nobody wrote.
      expect(builtFile(join('bundle', name)), `${name} was not copied verbatim`).toBe(source)
    }
  })

  it('holds a bundle the entry was compiled into', () => {
    expect(builtFile('renderer/entry.js').length, 'tsc emitted no entry for the bundler to walk').toBeGreaterThan(0)
    expect(builtFile('bundle/console.js').length, 'the bundle is empty').toBeGreaterThan(0)
  })

  it('holds the picker’s bundle, the second entry (Task 3.4b), with its own closure and nothing else', () => {
    const picker = builtFile('bundle/installations.js')
    const closure = moduleClosure(builtPath('renderer/installations.js'))
    expect(closure.length, 'the picker closure is the entry alone').toBeGreaterThan(1)
    for (const module of closure) {
      expect(picker, `${module.key} is not in the picker bundle`).toContain(
        `registry[${JSON.stringify(module.key)}] = function (exports, require, module) {`,
      )
    }
    expect(closure[closure.length - 1]?.key).toBe('/renderer/installations.js')
    const specifiers = requireSpecifiers(picker)
    for (const specifier of specifiers) expect(isRelative(specifier ?? '')).toBe(true)
    expect(picker).not.toMatch(/\beval\s*\(/)
    expect(picker).not.toMatch(/new\s+Function\s*\(/)
    // The build writes both, each told the other's name and nothing else.
    expect(consoleBundles(CONSOLE_ROOT).map((bundle) => [bundle.out, bundle.siblings])).toEqual([
      [join(CONSOLE_ROOT, 'dist', 'bundle', 'console.js'), ['installations.js']],
      [join(CONSOLE_ROOT, 'dist', 'bundle', 'installations.js'), ['console.js']],
    ])
  })
})

describe('the bundle', () => {
  const bundle = builtFile('bundle/console.js')
  const modules = moduleClosure(builtPath('renderer/entry.js'))

  it('is the entry and every module it requires', () => {
    // The closure is derived, never listed: a module the entry starts requiring
    // is in the bundle the day it is imported, and this test does not have to be
    // edited for it.
    expect(modules.length, 'the closure is the entry alone: this test would pass on a bundle that is one file').toBeGreaterThan(1)
    for (const module of modules) {
      expect(bundle, `${module.key} is not in the bundle`).toContain(
        `registry[${JSON.stringify(module.key)}] = function (exports, require, module) {`,
      )
    }
    const entryKey = modules[modules.length - 1]?.key
    expect(entryKey, 'the last module of the closure is not the entry').toBe('/renderer/entry.js')
    expect(bundle.trimEnd().endsWith('})();')).toBe(true)
  })

  it('requires its own modules and nothing else', () => {
    const specifiers = requireSpecifiers(bundle)
    expect(specifiers.length, 'the bundle holds no require at all: this test would pass by finding nothing').toBeGreaterThan(0)
    for (const specifier of specifiers) {
      expect(specifier, 'the bundle holds a require that is not a string literal').not.toBeNull()
      expect(
        isRelative(specifier ?? ''),
        `the bundle requires ${JSON.stringify(specifier)}: the renderer may require its own modules and nothing else (no package, no electron, no Node built-in)`,
      ).toBe(true)
    }
    // Named as well as covered by the loop above, because this is the one
    // specifier a reader of the preload's rule expects to see and must not:
    // `dist/preload/index.js` requires `electron` and the renderer does not.
    expect(specifiers).not.toContain('electron')
  })

  it('holds no eval and no new Function', () => {
    // Belt and braces with the CSP, which has no `'unsafe-eval'`: this one is
    // what fails at build time rather than in a window.
    expect(bundle).not.toMatch(/\beval\s*\(/)
    expect(bundle).not.toMatch(/new\s+Function\s*\(/)
  })

  it('is what the build script writes, after the compiler that emits its entry', () => {
    const pkg = JSON.parse(readFileSync(join(CONSOLE_ROOT, 'package.json'), 'utf8')) as {
      scripts?: Record<string, string>
    }
    const build = pkg.scripts?.['build'] ?? ''
    const compiles = build.indexOf('tsconfig.build.json')
    const bundles = build.indexOf('bundle-renderer')
    // The order is the point and not a style: the bundler reads
    // `dist/renderer/entry.js`, so a build that bundled first would bundle the
    // previous build's entry, or nothing.
    expect(compiles, `npm run build does not compile the entry: ${build}`).toBeGreaterThanOrEqual(0)
    expect(bundles, `npm run build does not run the bundler: ${build}`).toBeGreaterThan(compiles)
    expect(build).toContain('.bundle-renderer/index.js')
    // And the paths the script's own bundler is given are the paths this test
    // read the bundle from.
    expect(PATHS.out).toBe(join(CONSOLE_ROOT, 'dist', 'bundle', 'console.js'))
    expect(PATHS.entry).toBe(join(CONSOLE_ROOT, 'dist', 'renderer', 'entry.js'))
  })

  it('ships the built preload, the bundle and both documents -- once Task 4.1 lands', (context) => {
    // **The one thing this task cannot finish from here.** `build.files` is
    // electron-builder's list of what goes into the packaged app, and it arrives
    // with Task 4.1 (PR #540), which has not merged. Until it does, the compiled
    // preload and the served root are outside every list of packaged files: a
    // packaged console would come up with no script to run and no bridge to
    // read, which is the defect this task exists to close, reintroduced at the
    // packager. The plan's 3.4a note says so and this test is the half of it
    // that can be written now: the four entries are named, derived rather than
    // retyped (`SERVED_FILES` plus the preload path the window is given), and
    // asserted the moment the list exists. Whichever of #540 and this lands
    // second adds them.
    const pkg = JSON.parse(readFileSync(join(CONSOLE_ROOT, 'package.json'), 'utf8')) as {
      build?: { files?: readonly string[] }
    }
    const required = [
      'dist/preload/index.js',
      'dist/preload/installations.js',
      ...SERVED_FILES.map((path) => `dist/bundle${path}`),
    ]
    // Non-vacuity: an empty `SERVED_FILES` would make the loop below assert
    // nothing at all, and the loop is the whole claim.
    expect(required.length, 'SERVED_FILES names nothing: the check below would be vacuous').toBe(7)
    const files = pkg.build?.files
    if (files === undefined) {
      context.skip(
        'PR #540 (Task 4.1) has not merged, so package.json has no build.files yet; when it does, it must ' +
          `list ${required.join(', ')}`,
      )
      return
    }
    for (const entry of required) {
      expect(
        files,
        `${entry} is not in package.json's build.files: a packaged console would not ship it`,
      ).toContain(entry)
    }
  })
})

// ---------------------------------------------------------------------------
// The bundle, run the way the document runs it.
// ---------------------------------------------------------------------------
//
// The document and the clock are `test/support/fakeDom.ts`'s, shared with
// `entry-wiring.test.ts` so both suites drive the entry against one surface --
// this one through the built bundle, that one through the sources.

/**
 * runBundle runs the built bundle in a fresh realm, against a fresh document,
 * with the bridge it is given -- and answers the document, and how many times
 * the bridge was asked.
 *
 * Two things about this harness are load-bearing rather than incidental:
 *
 *   - **The bridge is built inside the realm**, by the bootstrap below, from a
 *     JSON string. `compatibilityFrom` refuses a value whose prototype is not
 *     the realm's own `Object.prototype`, so an answer this test built in its
 *     own realm would arrive as unreadable and *every* case below would draw the
 *     refusal -- a test that passed by refusing everything. A window gets its
 *     bridge from `contextBridge`, which copies the value across exactly this
 *     boundary, so the bootstrap is that copy.
 *   - **The realm is lent four host objects and no more** (`TextEncoder`,
 *     `TextDecoder`, `atob`, `btoa`). A window has all four -- `sanitize.ts`
 *     encodes to UTF-8 and decodes base64 -- and a `vm` realm has none, because
 *     they are host objects rather than language ones. Nothing else of Node's is
 *     offered, which is the point: the bundle here runs against as little of a
 *     platform as the sandboxed renderer has.
 */
async function runBundle(
  reply: Readonly<Record<string, unknown>> | undefined,
  installation: Readonly<Record<string, unknown>> = { ok: true, data: { chosen: false, sentence: 'no installation has been chosen' } },
): Promise<{ document: FakeDocument; asked: () => number; timers: ReturnType<typeof recordingTimers> }> {
  const document = new FakeDocument()
  const timers = recordingTimers()
  const sandbox: Record<string, unknown> = { document, TextEncoder, TextDecoder, atob, btoa, ...timers.globals }
  const context = createContext(sandbox)
  if (reply !== undefined) {
    sandbox['REPLY'] = JSON.stringify(reply)
    sandbox['INSTALLATION'] = JSON.stringify(installation)
    runInContext(
      'var asked = 0;\n' +
        'var fugaro = { getCompatibility: function () { asked += 1; return Promise.resolve(JSON.parse(REPLY)); },\n' +
        '  getInstallation: function () { return Promise.resolve(JSON.parse(INSTALLATION)); } };\n',
      context,
    )
  }
  runInContext(builtFile('bundle/console.js'), context)
  // The entry's first call is asynchronous (`startConsole` awaits the bridge),
  // so the drawing lands a microtask later. A macrotask is well past it.
  await new Promise((resolve) => setTimeout(resolve, 20))
  return { document, asked: () => (sandbox['asked'] as number | undefined) ?? 0, timers }
}

describe('the bundle in a document', () => {
  it('draws no figure for an in-range fugaro -- the installation state, the navigation and a refused read -- and asks before it decides', async () => {
    const { document, asked, timers } = await runBundle({ ok: true, data: anOkCompatibility() })
    // The non-vacuity guard: "drew no figure" is only an answer if the entry ran.
    // A bundle that threw on load, or one whose guard skipped it, draws nothing
    // too -- and this says which happened.
    expect(asked(), 'the entry never asked the bridge: nothing here was drawn or refused').toBe(1)
    // The three drawings of a console whose handshake is in range: D13's
    // no-installation state (Task 3.4b, step 4), the navigation, and the panel
    // the screens are drawn into (Task 3.4c).
    expect(document.body.children).toHaveLength(3)
    const root = document.body.children[0] as FakeNode
    expect(root.attributes.get('class')).toBe('console-installation')
    expect(root.attributes.get('data-installation')).toBe('none')
    expect(textOf(root)).toContain('no installation has been chosen')
    const nav = document.body.children[1] as FakeNode
    expect(nav.attributes.get('class')).toBe('console-views')
    expect(nav.children.map((button) => button.attributes.get('data-view'))).toEqual([
      'live',
      'spend',
      'projects',
    ])
    // And the panel holds the Live screen's own refusal rather than a dashboard
    // of zeros: this bridge carries the two handshake methods and **no read at
    // all**, so the read the screen opens with is one the console could not
    // make. `renderUnavailable` draws no figure, which is the whole point of
    // answering a read like this with a refusal.
    const panel = document.body.children[2] as FakeNode
    expect(panel.attributes.get('class')).toBe('console-panel')
    const drawn = panel.children[0] as FakeNode
    expect(drawn.attributes.get('data-kind')).toBe('refused')
    expect(drawn.attributes.get('class')).toBe('console-unavailable')
    // The poll is running: a live screen that never asked again would be a
    // screenshot, and the interval is what makes it a screen.
    expect(timers.live()).toBe(1)
  })

  it('names the file and why, when the store refused it (review S5)', async () => {
    const { document } = await runBundle(
      { ok: true, data: anOkCompatibility() },
      {
        ok: true,
        data: {
          chosen: false,
          sentence: 'no installation has been chosen',
          refusal: 'the file is not JSON',
          path: '/home/owner/.config/fugaro-console/installations.json',
        },
      },
    )
    const text = textOf(document.body.children[0] as FakeNode)
    expect(text).toContain('/home/owner/.config/fugaro-console/installations.json could not be read: the file is not JSON')
  })

  it('names the chosen installation’s project, binary, version and mode', async () => {
    const { document } = await runBundle(
      { ok: true, data: aReducedCompatibility() },
      {
        ok: true,
        data: { chosen: true, id: '0123456789abcdef', project: 'acme', binary: '/opt/fugaro/bin/fugaro', cliVersion: '9.9.9', mode: 'reduced' },
      },
    )
    // The banner first, then the installation.
    const section = document.body.children[1] as FakeNode
    expect(section.attributes.get('data-installation')).toBe('chosen')
    expect(section.attributes.get('data-mode')).toBe('reduced')
    const lines = section.children.map((child) => child.textContent)
    expect(lines).toEqual(['acme', '/opt/fugaro/bin/fugaro', '9.9.9 · reduced'])
  })

  it('draws the reduced banner, with the withheld views named', async () => {
    const { document } = await runBundle({ ok: true, data: aReducedCompatibility() })
    const root = document.body.children[0]
    expect(root, 'the reduced banner was not mounted').toBeDefined()
    expect(root?.attributes.get('class')).toBe('console-skew')
    expect(root?.attributes.get('data-mode')).toBe('reduced')
    expect(root?.attributes.get('data-skew')).toBe('newer')
    const hidden = everyNode(root as FakeNode)
      .map((node) => node.attributes.get('data-hidden'))
      .filter((view): view is string => view !== undefined)
    expect(hidden).toEqual(['spend', 'caps', 'kill'])
    // The banner names both versions, and the CLI's is drawn as itself.
    expect(textOf(root as FakeNode)).toContain('9.9.9')
  })

  it('draws the refusal screen when there is no bridge at all', async () => {
    // A document with no `window.fugaro` -- the preload did not run, or it
    // failed -- is a document that cannot ask the handshake's question, so the
    // answer it gets is the one that draws no figures.
    const { document } = await runBundle(undefined)
    const root = document.body.children[0]
    expect(root?.attributes.get('data-mode')).toBe('refused')
    expect(root?.attributes.get('data-skew')).toBe('unreadable')
    const hidden = everyNode(root as FakeNode).filter((node) => node.attributes.has('data-hidden'))
    expect(hidden.length, 'a refused console must withhold every view by name').toBe(3)
  })

  it('draws the refusal screen for an answer it cannot read', async () => {
    const { document } = await runBundle({ ok: true, data: { mode: 'ok' } })
    expect(document.body.children[0]?.attributes.get('data-mode')).toBe('refused')
  })
})

// ---------------------------------------------------------------------------
// The bundler's refusals.
// ---------------------------------------------------------------------------

/** planted is a throwaway console layout: the two served documents under
 * `src/renderer/`, and the compiled modules the test wrote under
 * `dist/renderer/`. It is the smallest tree `bundleRenderer` accepts, so a
 * refusal below is about the module that was planted and not about the tree. */
function planted(modules: Readonly<Record<string, string>>): string {
  const root = mkdtempSync(join(tmpdir(), 'console-bundle-'))
  mkdirSync(join(root, 'src', 'renderer'), { recursive: true })
  mkdirSync(join(root, 'dist', 'renderer'), { recursive: true })
  writeFileSync(join(root, 'src', 'renderer', 'index.html'), '<!doctype html>\n')
  writeFileSync(join(root, 'src', 'renderer', 'refusal.html'), '<!doctype html>\n')
  for (const [name, code] of Object.entries(modules)) {
    writeFileSync(join(root, 'dist', 'renderer', name), code)
  }
  return root
}

/** pathsOf is the entry and the output inside a planted tree. */
function pathsOf(root: string): { entry: string; out: string } {
  return { entry: join(root, 'dist', 'renderer', 'entry.js'), out: join(root, 'dist', 'bundle', 'console.js') }
}

/** withPlanted runs `body` on a planted tree and removes it afterwards. */
function withPlanted<T>(modules: Readonly<Record<string, string>>, body: (root: string) => T): T {
  const root = planted(modules)
  try {
    return body(root)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
}

describe('the bundler', () => {
  it('bundles a planted tree and copies its documents', () => {
    // The control for every refusal below: a tree like this one bundles, so a
    // refusal there is the planted module's doing and not the tool's.
    withPlanted(
      {
        'entry.js': `'use strict';\nrequire("./other");\n`,
        'other.js': `'use strict';\n`,
      },
      (root) => {
        bundleRenderer(pathsOf(root))
        const out = readFileSync(pathsOf(root).out, 'utf8')
        // The keys are the modules' paths relative to their deepest common
        // directory, which for this tree is `dist/renderer`.
        expect(out).toContain('registry["/entry.js"]')
        expect(out).toContain('registry["/other.js"]')
        expect(readFileSync(join(root, 'dist', 'bundle', 'index.html'), 'utf8')).toBe('<!doctype html>\n')
        expect(readFileSync(join(root, 'dist', 'bundle', 'refusal.html'), 'utf8')).toBe('<!doctype html>\n')
      },
    )
  })

  it('refuses a computed require', () => {
    withPlanted(
      {
        'entry.js': `'use strict';\nvar name = "./other";\nrequire(name);\n`,
        'other.js': `'use strict';\n`,
      },
      (root) => {
        expect(() => {
          bundleRenderer(pathsOf(root))
        }).toThrow(BundleRefusal)
        expect(() => {
          bundleRenderer(pathsOf(root))
        }).toThrow(/not a string literal/)
      },
    )
  })

  it('refuses a bare specifier', () => {
    withPlanted({ 'entry.js': `'use strict';\nrequire('electron');\n` }, (root) => {
      expect(() => {
        bundleRenderer(pathsOf(root))
      }).toThrow(/may require its own modules and nothing else/)
    })
    withPlanted({ 'entry.js': `'use strict';\nrequire('node:fs');\n` }, (root) => {
      expect(() => {
        bundleRenderer(pathsOf(root))
      }).toThrow(/may require its own modules and nothing else/)
    })
  })

  it('refuses a module the build did not emit', () => {
    withPlanted({ 'entry.js': `'use strict';\nrequire("./missing");\n` }, (root) => {
      expect(() => {
        bundleRenderer(pathsOf(root))
      }).toThrow(/the build did not emit/)
    })
  })

  it('refuses a file in the served root that it did not write', () => {
    // The served root is what the protocol handler reads, so a file beside the
    // bundle is a file the console serves and nothing vouches for.
    withPlanted({ 'entry.js': `'use strict';\n` }, (root) => {
      mkdirSync(join(root, 'dist', 'bundle'), { recursive: true })
      writeFileSync(join(root, 'dist', 'bundle', 'leftover.js'), 'stale\n')
      expect(() => {
        bundleRenderer(pathsOf(root))
      }).toThrow(/leftover\.js/)
    })
  })

  it('owns a sibling bundle it is told about, and still refuses anything else', () => {
    withPlanted({ 'entry.js': `'use strict';\n` }, (root) => {
      mkdirSync(join(root, 'dist', 'bundle'), { recursive: true })
      writeFileSync(join(root, 'dist', 'bundle', 'installations.js'), 'the other bundle\n')
      bundleRenderer({ ...pathsOf(root), siblings: ['installations.js'] })
      writeFileSync(join(root, 'dist', 'bundle', 'leftover.js'), 'stale\n')
      expect(() => {
        bundleRenderer({ ...pathsOf(root), siblings: ['installations.js'] })
      }).toThrow(/leftover\.js/)
    })
    withPlanted({ 'entry.js': `'use strict';\n` }, (root) => {
      mkdirSync(join(root, 'dist', 'bundle'), { recursive: true })
      writeFileSync(join(root, 'dist', 'bundle', 'installations.js'), 'the other bundle\n')
      expect(() => {
        bundleRenderer(pathsOf(root))
      }).toThrow(/installations\.js/)
    })
  })

  it('refuses a package that is not the console', () => {
    withPlanted({ 'entry.js': `'use strict';\n` }, (root) => {
      expect(() => consoleRootHere(root)).toThrow(BundleRefusal)
      expect(consoleRootHere(CONSOLE_ROOT)).toBe(CONSOLE_ROOT)
    })
  })

  it('reads specifiers and paths the way its refusals assume', () => {
    // The probes: the tests above are only about the bundler if these two
    // functions answer what they say they answer.
    expect(requireSpecifiers(`require('./app'); require("x");`)).toEqual(['./app', 'x'])
    expect(requireSpecifiers(`require(name); require('./' + name); require(\`./a\`);`)).toEqual([null, null, null])
    // A `require` written inside a comment is still matched, because this scan
    // is a scan and not a parse -- which is exactly why `tsconfig.build.json`
    // emits the renderer with `removeComments: true`: this tree's own prose
    // names `require('electron')`, and a scan that read that as a call would
    // refuse the preload for its header.
    expect(requireSpecifiers(`// require('./not-code')`)).toEqual(['./not-code'])
    expect(isRelative('./app')).toBe(true)
    expect(isRelative('../shared/version')).toBe(true)
    expect(isRelative('electron')).toBe(false)
    expect(isRelative('node:fs')).toBe(false)
    expect(isRelative('/etc/passwd')).toBe(false)
    expect(resolveSpecifier('/tmp/dist/renderer/entry.js', '../shared/version')).toBe('/tmp/dist/shared/version.js')
    expect(isEntryPoint('/work/repo/console/.bundle-renderer/index.js')).toBe(true)
    expect(isEntryPoint('/work/repo/console/node_modules/vitest/vitest.mjs')).toBe(false)
  })
})
