// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Role-gated write controls, in a real Electron window (console-improvements
// plan RB4; D2907, F22). The unit suite (`test/unit/gating.test.ts`) holds the
// table and the read; this is the same claim drawn under the real policy, by the
// shipped modules, into a document that the console's own CSP is enforcing.
//
// **What is real here and what is not.** Everything the console owns is real:
// the window, the CSP, the entry that booted it, `renderer/gating.js` and
// `renderer/writes.js` out of `dist/`, and the header's own Halt fleet control,
// which the running app drew -- this spec reads it rather than drawing one. What
// is stood in for is the **CLI**: no release carries `user ls --me`, so
// `WHOAMI_CLI_FLOOR` is `null` (`src/shared/version.ts`) and a real console asks
// nobody for a role; the allow-list row (AL) is not landed either, so the
// bridge has no `whoAmI` member to answer one. A spec that wanted a role out of a
// real binary would therefore be asserting a read no installation can perform
// today. Instead it hands the real `gateFrom`/`readGate` the bridge and the
// version line the app would hand them, with the floor argument
// (`readGate(ops, version, floor)`) that the release which ships `--me` will
// fill in -- so what is asserted below is the drawing for each role this console
// will meet, through the code that will draw it.
//
// **Two halves of RB4, and both are here.** The first is the header's own
// control, which must be *unchanged* when nothing answered: with no role read,
// `can` allows every action and `disabledWrite` draws `WRITE_UNAVAILABLE`, which
// is the sentence every write control has carried since the redesign. The second
// is a role that was read: a viewer's controls are disabled with a tooltip
// naming the role they need and the viewer's own, an operator's two run actions
// are left to this release's sentence and only the admin's are refused, and a
// project-level action is refused to an admin whose role is held elsewhere
// (`EffectiveAtProject`, F19).
//
// **Drawn, never raw.** As in `skew.spec.ts` and `live.spec.ts`, the drawing goes
// through `executeJavaScript`: the console's CSP refuses an inline `<script>`, and
// the module registry below exists only in the world `executeJavaScript` runs in.
// `page.evaluate` is used for reading the document. The body the window evaluates
// starts the role read -- which is a promise, on the bridge's own time -- and each
// case then waits for the drawing it started (`read`), rather than holding a
// promise across the process boundary.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { WRITE_UNAVAILABLE } from '../../src/renderer/writes'
import { WHOAMI_CLI_FLOOR } from '../../src/shared/version'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

// The console's own directory, from this file's location, as in the other specs:
// `test/e2e/x.spec.ts` is two levels below it.
const consoleDir = join(__dirname, '..', '..')

/** MODULES are the compiled modules this spec needs, in dependency order:
 * `version.js` for the floor the read is planned against, `gating.js` for the
 * table and the decision, `writes.js` for the control the decision is drawn
 * through, and `new-bridge.js` with the contract it reads a bridge through,
 * because the role reaches the gate the way it really does -- an envelope from
 * the bridge, decoded by `shared/new-ops.js`. The list is closed under
 * `require`; `modules-registry.spec.ts` is the guard. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/schema.js',
  'shared/sanitize.js',
  'shared/notice.js',
  'shared/version.js',
  'shared/new-ops.js',
  'renderer/new-bridge.js',
  'renderer/gating.js',
  'renderer/writes.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/** LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that resolves `./` and `../` specifiers and nothing else, so a
 * bundle that grew an import of `node:` or of a package fails here rather than in
 * a window with no Node in it. Unchanged from `skew.spec.ts` and
 * `live.spec.ts`, deliberately: this is the same window and the same constraint. */
const LOADER = `
var registry = {};
var cache = {};
function normalize(spec, from) {
  if (spec.charAt(0) !== '.') throw new Error('the renderer may not require ' + spec);
  var base = from.split('/').slice(0, -1);
  var parts = spec.split('/');
  for (var i = 0; i < parts.length; i++) {
    var part = parts[i];
    if (part === '' || part === '.') continue;
    if (part === '..') { base.pop(); continue; }
    base.push(part);
  }
  var resolved = base.join('/');
  return /\\.js$/.test(resolved) ? resolved : resolved + '.js';
}
function load(path) {
  if (cache[path]) return cache[path].exports;
  var definition = registry[path];
  if (!definition) throw new Error('no module ' + path);
  var module = { exports: {} };
  cache[path] = module;
  definition(module.exports, function (spec) { return load(normalize(spec, path)); }, module);
  return module.exports;
}
function mount(tree) {
  var previous = document.getElementById('console-mount');
  if (previous) previous.remove();
  var host = document.createElement('div');
  host.id = 'console-mount';
  host.appendChild(tree.node);
  document.body.appendChild(host);
}
`

/** FACTORY is a DOM-backed factory: one wrapper per element, so what the console
 * draws is served by a real node while staying exactly as narrow as the interface
 * the unit tests hand it. Unchanged from `skew.spec.ts`. */
const FACTORY = `
function domFactory() {
  var wrappers = new Map();
  function wrap(node) {
    var existing = wrappers.get(node);
    if (existing) return existing;
    var wrapper = {
      tag: node.tagName.toLowerCase(),
      node: node,
      get textContent() { return node.textContent; },
      set textContent(value) { node.textContent = value; },
      setAttribute: function (name, value) { node.setAttribute(name, value); },
      getAttribute: function (name) { return node.getAttribute(name); },
      removeAttribute: function (name) { node.removeAttribute(name); },
      append: function (child) { node.appendChild(child.node); },
      childNodes: function () {
        var out = [];
        for (var i = 0; i < node.children.length; i++) out.push(wrap(node.children[i]));
        return out;
      },
    };
    wrappers.set(node, wrapper);
    return wrapper;
  }
  return {
    create: function (tag) { return wrap(document.createElement(tag)); },
    createNS: function (tag) { return wrap(document.createElementNS('http://www.w3.org/2000/svg', tag)); },
    wrap: wrap,
  };
}
`

/** bundle wraps one drawing body in the harness the window evaluates. */
function bundle(body: string): string {
  return `(function () {\n${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}${body}\n})()`
}

/**
 * Control is one write control this console draws, named as RB4's table names
 * the thing it does: the control's own class and label as its screen draws them
 * (`entry.ts`'s Halt fleet, and the four in `screens/`), and the action the
 * table is keyed by.
 *
 * The labels are placed here as strings rather than imported: this spec draws
 * them through `disabledWrite` itself, because the screens do not pass a
 * decision to it yet -- MW1, MW2 and MWR are the tasks that do, from the gate
 * this one produces -- so importing four screen modules would put their own
 * drawings in the registry to assert something about a call they do not make.
 * The drawing below is the same call they will make, with the same strings.
 *
 * `Pause task` (`graph-inspector-pause`) is deliberately not here: which action
 * pausing a task names is that task's to decide, and a case that guessed it
 * would pin a role nobody chose.
 */
interface Control {
  readonly className: string
  readonly label: string
  readonly action: string
}

const CONTROLS: readonly Control[] = Object.freeze([
  // The run screen's re-run, and the Live screen's Reap: two run actions, both
  // an operator's (`ActionLaunch`, `ActionCancel`; D1587 for the cancel).
  Object.freeze({ className: 'run-retry', label: 'Retry run', action: 'launch' }),
  Object.freeze({ className: 'live-reap', label: 'Reap', action: 'cancel' }),
  // The Projects screen's kill switch for one repository (`ActionManageRepo`).
  Object.freeze({ className: 'projects-kill-action', label: 'Kill', action: 'manage-repo' }),
  // The Live screen's Halt project and the header's Halt fleet: project-level
  // writes (`ActionManageProject`), held only at project scope.
  Object.freeze({ className: 'live-halt', label: 'Halt project', action: 'manage-project' }),
  Object.freeze({ className: 'console-halt', label: 'Halt fleet', action: 'manage-project' }),
])

/** A_VERSION is the version line every read below is planned against, and
 * A_FLOOR the floor: the pair says "this CLI carries `--me`", which is the
 * question `readGate` asks before it asks anybody. The release's own floor is
 * `WHOAMI_CLI_FLOOR`, which is `null` today (asserted below), and this is how a
 * spec reads above it. */
const A_VERSION = '9.9.9'
const A_FLOOR = '0.0.1'

/** aRead is one `user ls --me --json` document, as the CLI's decoder reads it:
 * WH1's four members, which is what `shared/new-ops.js` admits. */
function aRead(role: string, scope: string | null): Record<string, unknown> {
  return { version: 1, role, scope, repos: ['acme/app'] }
}

/** theBridge is the body's own bridge: one member, answering whatever the case
 * is about, in the console's reply envelope. `newOpsFrom` reads it exactly as it
 * reads the preload's. */
const BRIDGE = `
function bridgeAnswering(answer) {
  return load('/renderer/new-bridge.js').newOpsFrom({
    whoAmI: function () { return Promise.resolve(answer); },
  });
}
function bridgeless() {
  return load('/renderer/new-bridge.js').newOpsFrom({});
}
`

/** drawRole reads the caller's role through the real `readGate` -- over a
 * bridge answering `reply`, or over a bridge with no such member at all when
 * `reply` is `null` -- and draws every control above from what it answered.
 *
 * The body is **synchronous** and returns before anything is drawn: it starts
 * the read (which is a promise, on the bridge's own time), removes whatever the
 * previous case mounted and answers `started`. The drawing lands in the frame
 * the read resolves in, and the case waits for it (`waitForSelector` below)
 * rather than holding a promise across the boundary -- so a case cannot pass by
 * reading the case before it, and the spec does not depend on
 * `executeJavaScript` awaiting a promise.
 *
 * The section carries the role and scope the gate answered as attributes: the
 * case asserts those against the document it drew, which is the same fact read
 * back out of the window rather than out of a returned promise.
 */
function drawRole(reply: string | null): string {
  const bridge = reply === null ? 'bridgeless()' : `bridgeAnswering(${reply})`
  return bundle(`
var version = load('/shared/version.js');
${BRIDGE}
var controls = ${JSON.stringify(CONTROLS)};
var factory = domFactory();
function drawAll(gate) {
  var section = factory.create('section');
  section.setAttribute('class', 'gating-probe');
  section.setAttribute('data-gate-role', gate.role === null ? '' : gate.role);
  section.setAttribute('data-gate-scope', gate.scope === null ? '' : gate.scope);
  for (var i = 0; i < controls.length; i++) {
    section.append(load('/renderer/writes.js').disabledWrite(factory, controls[i].className, controls[i].label, null, gate.can(controls[i].action)));
  }
  mount({ node: section.node });
}
var previous = document.getElementById('console-mount');
if (previous) previous.remove();
var ops = ${bridge};
load('/renderer/gating.js').readGate(ops, ${JSON.stringify(A_VERSION)}, ${JSON.stringify(A_FLOOR)}).then(drawAll);
return { started: true, floorUnset: version.WHOAMI_CLI_FLOOR === null };
`)
}

/** anEnvelope is the console's own accepted reply carrying one CLI document. */
function anEnvelope(document: Record<string, unknown>): string {
  return JSON.stringify({ ok: true, data: { document } })
}

/** aRefusal is the console's own refusal, which RB4 reads as no answer and not
 * as a role (`new-bridge.ts`'s `refused`). */
const A_REFUSAL = JSON.stringify({ ok: false, reason: 'the fugaro binary refused this read' })

/** HEADER_HALT_SELECTOR is the header's own Halt fleet control as the entry
 * draws it: the shell's `header` is a direct child of `body`, and the control is
 * one of its own children -- so the selector cannot reach the probe section this
 * spec mounts, which draws a control with the same class. */
const HEADER_HALT_SELECTOR = 'body > header > .console-halt'

/** Drawn is one control as the document holds it, before the class the case
 * looked it up by is put back on it (`Probe`). */
interface Drawn {
  readonly tag: string | null
  readonly ariaDisabled: string | null
  readonly title: string | null
  readonly ariaLabel: string | null
  readonly text: string | null
}

/** Probe is one control, named by the class this spec drew it with. */
interface Probe extends Drawn {
  readonly className: string
}

/** Mounted is one case's drawing as the document holds it. */
interface Mounted {
  /** drew is the number of controls the probe section holds. */
  readonly drew: number
  /** gateRole and gateScope are what the gate answered, read off the section
   * rather than returned across the boundary (`drawRole`): `null` for the empty
   * attribute, which is what the no-answer cases draw. */
  readonly gateRole: string | null
  readonly gateScope: string | null
  readonly probes: readonly Probe[]
  /** headerHalt is the app's own header control, which this spec looks up by
   * its class rather than drawing, so it carries no `className` of its own. */
  readonly headerHalt: Drawn | null
}

/** mounted reads every probe control back out of the mount, the role the gate
 * answered, and the header's own Halt fleet control out of the app's header --
 * by `HEADER_HALT_SELECTOR` and not a bare class lookup, because the probe
 * section draws a control with the same class inside `#console-mount`. */
function mounted(page: Page, classes: readonly string[]): Promise<Mounted> {
  return page.evaluate(
    ([wanted, headerSelector]: readonly [readonly string[], string]) => {
      const scope = document.getElementById('console-mount')
      const section = scope?.querySelector('.gating-probe') ?? null
      const one = (element: Element | null): Drawn => ({
        tag: element?.tagName.toLowerCase() ?? null,
        ariaDisabled: element?.getAttribute('aria-disabled') ?? null,
        title: element?.getAttribute('title') ?? null,
        ariaLabel: element?.getAttribute('aria-label') ?? null,
        text: element?.textContent ?? null,
      })
      const byClass = (className: string): Drawn =>
        one(section?.querySelector(`.${className}`) ?? null)
      const header = document.querySelector(headerSelector)
      const attribute = (name: string): string | null =>
        section?.getAttribute(name) === '' ? null : (section?.getAttribute(name) ?? null)
      return {
        drew: section === null ? 0 : section.querySelectorAll('button').length,
        gateRole: attribute('data-gate-role'),
        gateScope: attribute('data-gate-scope'),
        probes: wanted.map((className) => ({ ...byClass(className), className })),
        headerHalt: header === null ? null : one(header),
      }
    },
    [classes, HEADER_HALT_SELECTOR] as const,
  )
}

/** theReason is the tooltip `writes.ts` composes for a refused control: the
 * label, then the clause `gating.ts` answered. */
function theReason(label: string, clause: string): string {
  return `${label} ${clause}`
}

test.describe('the role-gated write controls', () => {
  // One window, one page, and the tests in order: each draws its own roles into
  // the same document, exactly as `skew.spec.ts` does. The timeout is the file's
  // own rather than the 30s default because the first case waits for the entry's
  // boot on a loaded machine (`launch.spec.ts` sets one for the same reason).
  test.describe.configure({ mode: 'serial', timeout: 60_000 })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation: the app under
    // test has to boot its real dashboard for the header's own control to exist.
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
    // The entry boots the dashboard on its own time -- a handshake, then the
    // installation's reads -- and the header's control is what the second case
    // reads, so this waits for the drawing rather than for the document. A
    // console that never draws it fails here with the selector, which is the
    // fact a reader wants: there was no header to read.
    await page.waitForSelector(HEADER_HALT_SELECTOR, { state: 'attached', timeout: 20_000 })
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  /** read puts one bridge answer through the real read in the window -- the
   * window's own error if the body threw -- waits for the drawing it started,
   * and reads it back. `role` and `scope` are what that read must have answered
   * (`null` for the cases where nothing did), taken from the drawn section, so a
   * case cannot pass by reading a role other than the one it is about, or by
   * reading the case before it.
   */
  async function read(
    role: string | null,
    scope: string | null,
    reply: string | null,
  ): Promise<Mounted> {
    const result = await app.evaluate(async ({ BrowserWindow }, body) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(body, true)) as Record<string, unknown>
    }, drawRole(reply))
    expect(result['error']).toBeUndefined()
    expect(result['started']).toBe(true)
    // The body removed the previous case's mount before it started the read, so
    // this waits for *this* case's drawing and never for a stale one.
    await page.waitForSelector('#console-mount .gating-probe > button', {
      state: 'attached',
      timeout: 15_000,
    })
    const drawn = await mounted(page, CONTROLS.map((control) => control.className))
    expect(drawn.gateRole).toBe(role)
    expect(drawn.gateScope).toBe(scope)
    expect(drawn.drew).toBe(CONTROLS.length)
    return drawn
  }

  test('the console carries no floor yet, which is why the release asks no CLI', async () => {
    // The premise of every case below, asserted rather than assumed: the floor
    // is unset until a release carries `user ls --me`, so `readGate` in the app
    // reads `gateFrom(null)` without asking anybody and the roles drawn below
    // have to be driven through the floor argument.
    expect(WHOAMI_CLI_FLOOR).toBeNull()
  })

  test('the header’s own Halt fleet is drawn as it always was, because nothing answered', async () => {
    // The real app, its real header, its real control. No role was read (the
    // floor is unset above), so `can` allowed the action and the control carries
    // this release's own sentence: RB4 changes nothing about a console that
    // cannot read a role, and a disabled control here is the redesign's refusal
    // and not the caller's.
    const { headerHalt, drew } = await mounted(page, [])
    expect(drew).toBe(0)
    expect(headerHalt).not.toBeNull()
    expect(headerHalt?.tag).toBe('button')
    expect(headerHalt?.ariaDisabled).toBe('true')
    expect(headerHalt?.title).toBe(WRITE_UNAVAILABLE)
    expect(headerHalt?.ariaLabel).toBe(`Halt fleet: ${WRITE_UNAVAILABLE.toLowerCase()}`)
    expect(headerHalt?.text).toBe('Halt fleet')
  })

  test('a viewer’s write controls are disabled, each naming the role it needs and the viewer’s own', async () => {
    // RB4's red step, in the window. Every control stays in the document --
    // `aria-disabled` and not `disabled`, so a keyboard reader meets it -- and
    // the tooltip is the sentence that says who may use it: the role the action
    // takes, at the scope it is held at, and the caller's own role and where it
    // reaches.
    const { probes: drawn } = await read('viewer', 'project', anEnvelope(aRead('viewer', 'project')))
    const at = (className: string): Probe => {
      const found = drawn.find((probe) => probe.className === className)
      if (found === undefined) throw new Error(`${className} was not drawn`)
      return found
    }
    for (const control of CONTROLS) {
      const probe = at(control.className)
      expect(probe.tag, control.className).toBe('button')
      expect(probe.ariaDisabled, control.className).toBe('true')
      expect(probe.text, control.className).toBe(control.label)
    }
    // One sentence per action, spelled out: a viewer may use nothing.
    expect(at('run-retry').title).toBe(
      theReason('Retry run', 'needs the operator role and your role is viewer at project scope'),
    )
    expect(at('live-reap').title).toBe(
      theReason('Reap', 'needs the operator role and your role is viewer at project scope'),
    )
    expect(at('projects-kill-action').title).toBe(
      theReason('Kill', 'needs the admin role and your role is viewer at project scope'),
    )
    expect(at('live-halt').title).toBe(
      theReason('Halt project', 'needs the admin role at project scope and your role is viewer at project scope'),
    )
    expect(at('console-halt').title).toBe(
      theReason('Halt fleet', 'needs the admin role at project scope and your role is viewer at project scope'),
    )
    // The accessible name carries the same sentence, because a tooltip is not
    // what a screen reader announces.
    expect(at('console-halt').ariaLabel).toBe(
      `Halt fleet: needs the admin role at project scope and your role is viewer at project scope`,
    )
  })

  test('an operator keeps the two run actions, and only the admin’s are refused', async () => {
    // A role read is not "everything is refused": an operator may launch and
    // cancel, so those two keep this release's sentence -- the console still
    // does not perform them -- and the two admin actions name the operator.
    const { probes: drawn } = await read('operator', 'project', anEnvelope(aRead('operator', 'project')))
    const titleOf = (className: string): string | null =>
      drawn.find((probe) => probe.className === className)?.title ?? null
    expect(titleOf('run-retry')).toBe(WRITE_UNAVAILABLE)
    expect(titleOf('live-reap')).toBe(WRITE_UNAVAILABLE)
    expect(titleOf('projects-kill-action')).toBe(
      theReason('Kill', 'needs the admin role and your role is operator at project scope'),
    )
    expect(titleOf('console-halt')).toBe(
      theReason('Halt fleet', 'needs the admin role at project scope and your role is operator at project scope'),
    )
  })

  test('a project admin’s controls are all drawn as before: the role is not what refuses them', async () => {
    // The other half of the same rule: a role that may use a control does not
    // make this build perform the write, so an admin reads the release's own
    // sentence and no role clause at all.
    const { probes: drawn } = await read('admin', 'project', anEnvelope(aRead('admin', 'project')))
    for (const probe of drawn) {
      expect(probe.title, probe.className).toBe(WRITE_UNAVAILABLE)
      expect(probe.ariaDisabled, probe.className).toBe('true')
    }
  })

  test('an admin whose role is held elsewhere is refused the project-level writes, and told where it reaches', async () => {
    // `EffectiveAtProject` (F19): an all-repos grant holds nothing at project
    // scope, so Halt fleet is not this caller's however high the role reads --
    // and the tooltip is what explains a refusal an admin would not expect.
    const { probes: drawn } = await read('admin', 'all', anEnvelope(aRead('admin', 'all')))
    const titleOf = (className: string): string | null =>
      drawn.find((probe) => probe.className === className)?.title ?? null
    expect(titleOf('projects-kill-action')).toBe(WRITE_UNAVAILABLE)
    expect(titleOf('run-retry')).toBe(WRITE_UNAVAILABLE)
    expect(titleOf('console-halt')).toBe(
      theReason('Halt fleet', 'needs the admin role at project scope and your role is admin at every repository'),
    )
  })

  test('a refusal is no answer: every control stays as it was, and the CLI’s own sentence is what a click shows', async () => {
    // The old CLI, the old preload, a bridge that cannot ask: RB4 reads every
    // one of them as no answer (`readGate`; `new-bridge.ts`'s three gaps), and no
    // answer is not the role `none`. Nothing is disabled by a role, and the
    // refusal the CLI gives still reaches the operator after the click -- which
    // is the whole of what this console can do until it can read a role.
    for (const [what, drawnCase] of [
      ['a refused reply', await read(null, null, A_REFUSAL)],
      ['a bridge with no whoAmI member', await read(null, null, null)],
    ] as const) {
      expect(drawnCase.probes, what).toHaveLength(CONTROLS.length)
      for (const probe of drawnCase.probes) {
        expect(probe.title, `${what}: ${probe.className}`).toBe(WRITE_UNAVAILABLE)
        expect(probe.ariaDisabled, `${what}: ${probe.className}`).toBe('true')
      }
    }
  })

  test('and the drawing has teeth: the same reader finds the header’s control the app drew', async () => {
    // The check on the check, as in `skew.spec.ts`: the assertions above are
    // readings of a mounted section, and a reader that found nothing would pass
    // them in silence. The app's own header carries a control of the same class,
    // drawn by the entry rather than by this spec, and it is found here.
    const { headerHalt } = await mounted(page, [])
    expect(headerHalt?.text).toBe('Halt fleet')
    expect(headerHalt?.title).toBe(WRITE_UNAVAILABLE)
  })
})
