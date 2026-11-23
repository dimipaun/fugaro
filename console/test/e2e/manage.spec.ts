// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Manage section on the real kinds (the console-improvements plan's task
// I-MG; spec section 5 as D2903 amended it; the descriptor's prose is
// `docs/console-improvements/manage-kinds.md`).
//
// **What this file is for.** `manage-shell.test.ts` proves the shell over
// fixture kinds, and `manage-people.test.ts`, `manage-code.test.ts`,
// `manage-models.test.ts` and `manage-budget.test.ts` each prove one file of
// kinds over its own document. Nothing until this file proves the two joined:
// the list `manage/kinds/index.ts` hands the shell, drawn in the Project tab's
// own host by `mountProjectsManage`. So the acceptance's own list is the cases
// below -- the groups and the kinds under them, each kind's table, the empty
// sentence, a Copy-command -- and the red step runs through the first of them: a
// kind left out of `index.ts` takes its group's row (or, where the group has
// other kinds, its own section) with it and the case fails naming what is
// missing.
//
// **The window is the real one.** The console is launched against the in-range
// CLI stub, exactly as `projects.spec.ts` launches it: the handshake is the real
// handshake, and the dashboard the entry draws beside this spec's mount is drawn
// from the stub's own answers. What is **not** the entry's yet is the Manage
// section. No task of this slice wires it into `src/renderer/entry.ts` -- the
// operations it reads and the bridge that reaches them belong to MW1/MW2, which
// consume this task's list (`docs/console-improvements/implementation-plan.md`,
// I-MG's Interfaces and MW1's) -- so this spec calls the door the tab exposes,
// `mountProjectsManage`, the way that wiring will, and answers the eleven reads
// itself.
//
// **Where the documents come from, and why two of them are views.** Nine of the
// eleven kinds decode the CLI's own document (`members.list` twice, and
// `accounts.inventory`, `accounts.pools`, `repos.ls`, `repos.status`,
// `models.list`, `secrets.inventory`, `budget.show`), and two decode a view their
// operation has already decoded: `image.catalog.ls` answers C-OPS's
// `ImageCatalogView` and `recipes.ls` its `RecipesView`, which is the shape
// `manage-code.test.ts` feeds those two kinds through a bridge. The documents
// below are written here in those two shapes, as `projects.spec.ts` writes its
// own corpus; the kinds' reading of a real CLI's answers is the unit suite's
// subject, and no answer here reaches a network or a CLI -- every one of them is
// an object in this file. `test/fixtures/` holds the watch feed alone, so there
// is no recording of these nine to read instead.
//
// **One kind names an operation this console does not admit yet.** The pools
// kind's `source` is `accounts.pools`, the read `fugaro accounts pool ls --json`
// answers (the model-credentials-routing plan's VS2), and no operation of that
// name is in `src/main/ipc.ts`'s allow-list: admitting one is a security-sensitive
// change of its own (`.claude/rules/console.md`) and belongs to the task that
// wires the Manage section's reads, not to the kind. This spec answers the name
// from its own corpus, exactly as it answers every other read, so what is under
// test here is the kind and the list -- which is what the acceptance asks for.
//
// **No golden.** Every kind that lands draws an `as of HH:MM` stamp from
// `Date.now()` (the shell's own, `section.ts`'s `renderStamp`), so the frame
// here is not the same pixels twice and there is nothing to record. The
// acceptance asks for no screenshot, and this spec takes none.
//
// **The bundle is the compiled renderer, loaded by the same hand-built registry
// the other screen specs use** (`projects.spec.ts`, `live.spec.ts`,
// `spend.spec.ts`): `dist/` is what the registry injects, so the list below has
// to name every module the compiled screen requires, and
// `modules-registry.spec.ts` is the guard that says so -- by module, when a
// renderer edit gains an import. The entry, the document and the place the
// script is served from belong to other tasks; drawing from outside the app is
// what keeps this file to its own three.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import {
  MANAGE_CLASS,
  MANAGE_COLUMN_ATTRIBUTE,
  MANAGE_COPY_CLASS,
  MANAGE_EMPTY_CLASS,
  MANAGE_EMPTY_SENTENCE,
  MANAGE_GROUP_ATTRIBUTE,
  MANAGE_GROUP_CLASS,
  MANAGE_KIND_ATTRIBUTE,
  MANAGE_KIND_CLASS,
  MANAGE_OPERATION_ATTRIBUTE,
  MANAGE_REFUSAL_CLASS,
  MANAGE_ROW_CLASS,
  MANAGE_TABLE_CLASS,
  MANAGE_VIEW_CLASS,
} from '../../src/renderer/manage/shell'
import { MANAGE_GROUPS } from '../../src/renderer/manage/types'
import { PROJECTS_MANAGE_CLASS } from '../../src/renderer/screens/projects'
import { BUSY_SLOT_CLASS } from '../../src/renderer/section'
import { CLI_VERSION_BUILT_AGAINST, classifyCliVersion } from '../../src/shared/version'
import type { JSONDoc } from '../../src/source/source'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

/** consoleDir is the console's own directory, from this file's location:
 * `test/e2e/x.spec.ts` is two levels below it. */
const consoleDir = join(__dirname, '..', '..')

/** MANAGE_MOUNT is the host **this spec** draws the tab's frame into. It is not
 * the entry's own mount: the entry draws its dashboard into the document beside
 * this one, and every reading below is taken inside this host, so the entry's
 * drawing can neither satisfy nor fail an assertion. */
const MANAGE_MOUNT = 'manage-mount'

/** SETTLE_MS is how long the page waits between a click and reading the tree
 * back. A `<details>` **queues** its `toggle` rather than firing it in the click
 * (`HTMLDetailsElement`'s own steps), and the read the shell starts from that
 * event lands on a microtask, so one turn of the task queue is what puts both
 * behind us. It is a wait for a queued task and not for a document: every read
 * here answers from an object in this file. */
const SETTLE_MS = 25

/** COMPATIBILITY is the console's own compatibility with the CLI it was built
 * against -- the answer `manage/kinds/index.ts`'s `KINDS` is built for, and the
 * one the Budget kind asks `hidesView` of. A `dev` CLI or an older one would
 * reduce it, and a reduced console withholds the Budget kind's figures and its
 * Spend link (`kinds/budget.ts`); the case at the foot of this file asserts that
 * this is not the compatibility under test rather than leaving a withheld table
 * to look like a broken one. */
const COMPATIBILITY = classifyCliVersion(CLI_VERSION_BUILT_AGAINST)

// ---------------------------------------------------------------------------
// The corpus: one answer per read, in the shape that read's kind decodes.
// ---------------------------------------------------------------------------

/**
 * MEMBERS is `members.list` (`fugaro user ls --json`, contract version 2) as
 * `kinds/people.ts` reads it: this machine's own operator and admin lists, and
 * the grants node's stored pairs. Three members fall out of the two lists and
 * one pair out of `grants`, which is what the two kinds' row counts below are.
 * The addresses are `@example.invalid`, so nothing here is a live mailbox and
 * nothing is a value the console would redact into a different string.
 */
const MEMBERS: JSONDoc = {
  version: 2,
  project: 'example-project',
  run_operators: ['user:operator@example.invalid', 'group:fugaro-operators@example.invalid'],
  admins: ['user:admin@example.invalid'],
  grants: [{ user: 'user:admin@example.invalid', role: 'admin', scope: 'project', known: [] }],
  effective: [],
  legacy: [],
}

/**
 * ACCOUNTS is `accounts.inventory` (`fugaro accounts ls --json`) with no name in
 * it. It is the empty case of the acceptance -- the shell's own sentence, and
 * the kind's Copy-command drawn beside it -- and it is written with the document
 * the CLI really answers an installation that has added none: the block, the
 * alias map and the credentials list are there and empty, so the kind's row list
 * is empty because the document has no accounts in it and not because a member
 * is missing.
 */
const ACCOUNTS: JSONDoc = {
  repo: 'github/example-owner/example-repo',
  accounts: [],
  complete: true,
  states: {},
  aliases: {},
  credentials: [],
}

/**
 * POOLS is `accounts.pools` (`fugaro accounts pool ls --json`, C2's
 * `routing.PoolListDoc`, `internal/routing/pools.go`): the project's shared pool
 * with two members and a person's own pool with one -- and the figures the
 * acceptance turns on, one member each: a counted member with headroom, an
 * exhausted member with its reset and no headroom at all (the figure the contract
 * refuses to guess, D3178 and U1), and a member the service counts nothing for.
 * The document carries no `owner`, which is what makes the Owner column the
 * pool's `principal`: a person's pool is that person's Google subject, and the
 * shared pool is the project.
 */
const POOLS: JSONDoc = {
  project: 'example-project',
  pools: [
    {
      name: 'launcher',
      rule: 'ordered',
      principal: { kind: 'project', id: 'example-project' },
      threshold: 0.9,
      members: [
        {
          name: 'fugaro-launcher-primary',
          state: 'available',
          next_reset: null,
          usage: { calls: 128, tokens: 4820000 },
          headroom: 0.42,
        },
        {
          name: 'fugaro-launcher-secondary',
          state: 'exhausted',
          next_reset: '2026-11-22T18:30:00Z',
          usage: { calls: 41, tokens: 1500000 },
          headroom: null,
        },
      ],
    },
    {
      name: 'ana',
      rule: '',
      principal: { kind: 'person', id: '104293871120293847' },
      threshold: null,
      members: [
        {
          name: 'ana-claude-max',
          state: 'backed_off',
          next_reset: '2026-11-22T13:45:00Z',
          usage: { calls: 3, tokens: 90000 },
          headroom: 0.8,
        },
      ],
    },
  ],
}

/** REPOS is `repos.ls` (`fugaro project repos ls --json`): a repository the
 * allow-list admits by a pattern and this machine holds, and one the allow-list
 * names by its own entry and this machine has never configured -- the two states
 * the document keeps in two halves. */
const REPOS: JSONDoc = {
  version: 1,
  project: 'example-project',
  repos: [
    {
      canonical: 'github/example-owner/example-repo',
      provider: 'github',
      base_branch: 'main',
      workflows: [{ name: 'implement', entry: 'base', source: 'workflow' }],
      image_entry: 'base',
      image_source: 'workflow',
      claude_accounts: ['default'],
      published: { admitted: true, pattern: 'github/example-owner/*' },
      local: { present: true, key: 'example-owner/example-repo' },
    },
    {
      canonical: 'github/example-owner/other-repo',
      provider: 'github',
      base_branch: 'main',
      workflows: [],
      image_entry: '',
      image_source: 'repo',
      claude_accounts: [],
      published: { admitted: false, pattern: '' },
      local: { present: false, key: '' },
    },
  ],
  allow: { entries: ['github/example-owner/*'], generation: '1', state: '' },
  seen: { entries: [], notes: [], state: '' },
  local: { repos: ['example-owner/example-repo'], state: '' },
}

/** STATUS is `repos.status --json`: one repository whose two checks were read.
 * The kind draws one row per (repository, check) pair -- that is what the
 * document is -- so two checks are two rows, each carrying its own fix. */
const STATUS: JSONDoc = {
  version: 1,
  project: 'example-project',
  repos: [
    {
      canonical: 'github/example-owner/example-repo',
      enrolled: true,
      state: '',
      rows: [
        { id: 'app-installation', ok: true, problem: 'the App is installed on the repository' },
        {
          id: 'image-map',
          ok: false,
          problem: 'no image map names this repository',
          fix: 'fugaro image build base',
          severity: 'warning',
        },
      ],
    },
  ],
}

/**
 * IMAGES is `image.catalog.ls` as its operation answers it: C-OPS's
 * `ImageCatalogView`, whose entries carry `isDefault` because the decoder
 * (`shared/new-ops.ts`'s `imageEntryAt`) has already read the document's
 * `default`. The kind decodes this view and not the CLI's file -- which is why
 * the Default column below is `yes` for the first entry -- and
 * `manage-code.test.ts` feeds it through a bridge the same way.
 */
const IMAGES: JSONDoc = {
  project: 'example-project',
  entries: [
    {
      name: 'base',
      tags: ['v1', 'latest'],
      isDefault: true,
      source: 'images/base',
      description: 'the pinned base image',
      digest: 'sha256:0f1e2d',
    },
    {
      name: 'node22-playwright',
      tags: ['v3'],
      isDefault: false,
      source: 'images/node22-playwright',
      description: 'Node 22 with a browser',
      digest: '',
    },
  ],
}

/** RECIPES is `recipes.ls` as its operation answers it: `RecipesView`, one
 * recipe of one layer, launchable and unshadowed. */
const RECIPES: JSONDoc = {
  recipes: [
    {
      name: 'quick',
      source: 'repo',
      description: 'one implement round and a review, for a small change',
      useWhen: 'the task is one file and the tests already cover it',
      steps: [{ kind: 'first_line', command: '', maxRounds: null, bounce: false, autofix: false }],
      launchable: true,
      shadowedBy: '',
      problem: '',
    },
  ],
  notes: [],
}

/** MODELS is `models.list` (`fugaro models ls --json`) as the kinds decode it:
 * one model a route serves and the catalog prices, and one no route serves with
 * no price at all -- the two the kind's own columns have to tell apart. */
const MODELS: JSONDoc = {
  version: 1,
  project: 'example-project',
  catalog: null,
  routes: [],
  models: [
    {
      id: 'deepseek-v4.1-flash',
      route: 'gateway',
      source: 'catalog',
      input_per_m: 0.14,
      output_per_m: 0.28,
      catalog: null,
      allowed_to: ['*'],
      refused: '',
      note: 'routing.claude.via: gateway',
    },
    {
      id: 'claude-sonnet-5',
      route: '',
      source: 'none',
      input_per_m: null,
      output_per_m: null,
      catalog: null,
      allowed_to: [],
      refused: 'no route serves it',
      note: '',
    },
  ],
  repositories: [],
}

/** KEYS is `secrets.inventory` (`fugaro secrets ls --json`), which is a **bare
 * array** and not an object: a key whose container resolves a value, and one the
 * CLI marks missing. Neither entry carries a value, and the kind reads the
 * metadata alone (D2905). */
const KEYS: readonly JSONDoc[] = [
  { name: 'fugaro-github-token', scope: 'repo', latest: '3' },
  { name: 'fugaro-slack-webhook', scope: 'project', missing: true },
]

/** BUDGET is `budget.show` (`fugaro budget show --json`): two repositories with
 * their caps, the money counted against them and their kill state -- the first
 * live, the second halted by a person, whose row's sentence the table draws in
 * its own column. */
const BUDGET: JSONDoc = {
  project: 'example-project',
  gcp_project: 'example-cloud',
  mode: 'enforce',
  day: '2026-11-20',
  max_reserve_usd: 5,
  global: {
    daily_cap_usd: 40,
    per_run_cap_usd: 8,
    counted_usd: 12.5,
    spent_usd: 4,
    notional_usd: 0,
    leased_usd: 0,
    headroom_usd: 27.5,
    kill: null,
  },
  defaults: { repo_daily_cap_usd: 10, repo_per_run_cap_usd: 2 },
  repos: [
    {
      slug: 'acme/app',
      repo: 'acme/app',
      daily_cap_usd: 20,
      daily_cap_source: 'repo',
      per_run_cap_usd: 8,
      per_run_cap_source: 'global',
      counted_usd: 12.5,
      spent_usd: 4,
      notional_usd: 0,
      leased_usd: 0,
      headroom_usd: 7.5,
      kill: null,
      running: [],
    },
    {
      slug: 'acme/website',
      repo: 'acme/website',
      daily_cap_usd: 10,
      daily_cap_source: 'default',
      per_run_cap_usd: 2,
      per_run_cap_source: 'default',
      counted_usd: 11,
      spent_usd: 11,
      notional_usd: 0,
      leased_usd: 0,
      headroom_usd: 0,
      kill: {
        by: 'dana@example.invalid',
        at: '2026-11-20T09:00:00Z',
        reason: 'the day’s spend reached the cap while a run was looping',
      },
      running: [],
    },
  ],
  running: [],
  warnings: [],
}

/** DOCUMENTS is every read's answer, by the operation the kind names as its
 * `source` -- the console's own operation names and not the CLI's commands,
 * which is what `ManageKind.source` holds. `members.list` answers two kinds:
 * the people and the stored pairs are one document read as two lists. */
const DOCUMENTS: Readonly<Record<string, unknown>> = Object.freeze({
  'members.list': MEMBERS,
  'accounts.inventory': ACCOUNTS,
  'accounts.pools': POOLS,
  'repos.ls': REPOS,
  'repos.status': STATUS,
  'image.catalog.ls': IMAGES,
  'recipes.ls': RECIPES,
  'models.list': MODELS,
  'secrets.inventory': KEYS,
  'budget.show': BUDGET,
})

// ---------------------------------------------------------------------------
// The injected bundle: the compiled renderer, a DOM-backed factory, the mount.
// ---------------------------------------------------------------------------

/**
 * MODULES is every compiled module the screen under test requires, computed from
 * `renderer/screens/projects.js`'s relative `require`s and closed under them:
 * the Project tab's own modules, the Manage section's shell and descriptor, the
 * ten files of kinds `index.ts` names, the Spend screen the Budget kind's rows
 * come from and `renderer/gating.js`, which the harness reads for the gate it
 * hands the section (`ManageDeps.can` is required, and a harness with no role
 * read hands the no-answer value that module builds). The list is written out by
 * hand because the registry below is hand-built, and `modules-registry.spec.ts`
 * reads the built modules and fails here -- by module -- when a renderer edit
 * adds an import this list does not carry.
 */
const MODULES: readonly string[] = Object.freeze([
  'renderer/app.js',
  'renderer/gating.js',
  'renderer/manage/command.js',
  'renderer/manage/kinds/budget.js',
  'renderer/manage/kinds/images.js',
  'renderer/manage/kinds/index.js',
  'renderer/manage/kinds/keys.js',
  'renderer/manage/kinds/models.js',
  'renderer/manage/kinds/people.js',
  'renderer/manage/kinds/pools.js',
  'renderer/manage/kinds/read.js',
  'renderer/manage/kinds/recipes.js',
  'renderer/manage/kinds/repos.js',
  'renderer/manage/shell.js',
  'renderer/manage/types.js',
  'renderer/screens/policynotes.js',
  'renderer/screens/projects.js',
  'renderer/screens/reading.js',
  'renderer/screens/spend.js',
  'renderer/section.js',
  'renderer/skeleton.js',
  // The streamed reads the Project tab and the Spend screen fall back through
  // (RS5), which both of them require.
  'renderer/stream.js',
  'renderer/writes.js',
  'shared/availability.js',
  'shared/notice.js',
  'shared/sanitize.js',
  'shared/schema.js',
  'shared/version.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/** LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that can resolve `./` and `../` specifiers and nothing else, so a
 * bundle that grew an import of a package or of a Node builtin fails here rather
 * than in a window with no Node in it. */
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
`

/** FACTORY is a DOM-backed element factory: one wrapper per element, so the
 * screen's own narrow interface is served by a real node, and `node` is the
 * harness's handle on the element (never visible to the screen).
 *
 * It is `projects.spec.ts`'s factory plus `replaceChildren`: a kind's body is
 * **replaced whole** when its read lands (`manage/shell.ts`'s `landed`), so the
 * wrapper has to hand the engine's own children -- the elements, not the
 * wrappers around them -- to `Element.replaceChildren`. */
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
      replaceChildren: function () {
        node.replaceChildren();
        for (var i = 0; i < arguments.length; i++) node.appendChild(arguments[i].node);
      },
      childNodes: function () {
        var out = [];
        for (var i = 0; i < node.children.length; i++) out.push(wrap(node.children[i]));
        return out;
      },
      addEventListener: function (type, listener) { node.addEventListener(type, listener); },
      get value() { return node.value; },
    };
    wrappers.set(node, wrapper);
    return wrapper;
  }
  return {
    create: function (tag) { return wrap(document.createElement(tag)); },
    wrap: wrap,
  };
}
`

/**
 * HARNESS is the expression the window evaluates: it loads the compiled screen,
 * draws the Project tab's frame into its own host, mounts the Manage section into
 * the frame's Manage host -- the tab's door, `mountProjectsManage`, handed the
 * real kinds (`KINDS`) for the compatibility above -- and installs a handle on
 * the mount for the cases below to drive and read.
 *
 * The three things it records are the three claims a case here can only make
 * from outside the drawing: `reads` (which operations the section asked for, in
 * which order, once each), `copied` (what reached the clipboard) and `opened`
 * (which views the caller was asked to show). The kind classes and attributes
 * come from `shell.ts` itself, so a renamed hook fails the spec rather than
 * being read as an absent element.
 */
const HARNESS = `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var MOUNT = ${JSON.stringify(MANAGE_MOUNT)};
var SECTION_CLASS = ${JSON.stringify(MANAGE_CLASS)};
var GROUP_CLASS = ${JSON.stringify(MANAGE_GROUP_CLASS)};
var GROUP_ATTRIBUTE = ${JSON.stringify(MANAGE_GROUP_ATTRIBUTE)};
var KIND_CLASS = ${JSON.stringify(MANAGE_KIND_CLASS)};
var KIND_ATTRIBUTE = ${JSON.stringify(MANAGE_KIND_ATTRIBUTE)};
var OPERATION_ATTRIBUTE = ${JSON.stringify(MANAGE_OPERATION_ATTRIBUTE)};
var COLUMN_ATTRIBUTE = ${JSON.stringify(MANAGE_COLUMN_ATTRIBUTE)};
var TABLE_CLASS = ${JSON.stringify(MANAGE_TABLE_CLASS)};
var ROW_CLASS = ${JSON.stringify(MANAGE_ROW_CLASS)};
var COPY_CLASS = ${JSON.stringify(MANAGE_COPY_CLASS)};
var VIEW_CLASS = ${JSON.stringify(MANAGE_VIEW_CLASS)};
var EMPTY_CLASS = ${JSON.stringify(MANAGE_EMPTY_CLASS)};
var REFUSAL_CLASS = ${JSON.stringify(MANAGE_REFUSAL_CLASS)};
var HOST_CLASS = ${JSON.stringify(PROJECTS_MANAGE_CLASS)};
var SLOT_CLASS = ${JSON.stringify(BUSY_SLOT_CLASS)};
var DOCUMENTS = ${JSON.stringify(DOCUMENTS)};
var COMPATIBILITY = ${JSON.stringify(COMPATIBILITY)};

var projects = load('/renderer/screens/projects.js');
var gating = load('/renderer/gating.js');
var factory = domFactory();
var frame = projects.renderProjectsFrame(factory, 'light');
var previous = document.getElementById(MOUNT);
if (previous) previous.remove();
var host = document.createElement('div');
host.id = MOUNT;
host.appendChild(frame.root.node);
document.body.appendChild(host);

var reads = [];
var copied = [];
var opened = [];
var section = projects.mountProjectsManage({
  factory: factory,
  theme: 'light',
  read: function (operation) { reads.push(operation); return Promise.resolve(DOCUMENTS[operation]); },
  copy: function (text) { copied.push(text); },
  openView: function (view) { opened.push(view); },
  // The harness reads no role, which is gating.ts's no-answer case: the gate
  // it hands the section allows every action, and it is a value rather than an
  // absent member (ManageDeps.can is required).
  can: gating.gateFrom(null).can,
}, COMPATIBILITY, frame.manage);

function all(selector, root) {
  var scope = root || document.getElementById(MOUNT);
  return Array.prototype.slice.call(scope.querySelectorAll(selector));
}
function one(selector, root) {
  return all(selector, root)[0];
}
function textOf(node) {
  return node === undefined ? null : node.textContent;
}

/** directSlots counts the busy slots that are **children of the host itself**:
 * a host with one would be a claim about a read of this file's, and the kinds'
 * own slots are one level further down. */
function directSlots(node) {
  var count = 0;
  for (var i = 0; i < node.children.length; i++) {
    if (node.children[i].classList.contains(SLOT_CLASS)) count++;
  }
  return count;
}

/** cellsOf is one row's cells, keyed by the column the cell belongs to. */
function cellsOf(row, kind) {
  var cells = {};
  all('td', row).forEach(function (cell) {
    cells[cell.getAttribute(COLUMN_ATTRIBUTE)] = cell.textContent;
  });
  return cells;
}

function summary() {
  var groups = all('details.' + GROUP_CLASS).map(function (details) {
    var kinds = all('section.' + KIND_CLASS, details).map(function (kind) {
      var rows = all('tbody tr.' + ROW_CLASS, kind);
      return {
        id: kind.getAttribute(KIND_ATTRIBUTE),
        operation: kind.getAttribute(OPERATION_ATTRIBUTE),
        title: textOf(one('h3', kind)),
        columns: all('thead th', kind).map(function (th) { return th.getAttribute(COLUMN_ATTRIBUTE); }),
        rows: rows.length,
        tables: all('table.' + TABLE_CLASS, kind).length,
        cells: rows.map(function (row) { return cellsOf(row, kind); }),
        empty: textOf(one('p.' + EMPTY_CLASS, kind)),
        refusal: textOf(one('p.' + REFUSAL_CLASS, kind)),
        copies: all('button.' + COPY_CLASS, kind).map(textOf),
        views: all('button.' + VIEW_CLASS, kind).map(textOf),
      };
    });
    return {
      name: details.getAttribute(GROUP_ATTRIBUTE),
      title: textOf(one('summary', details)),
      open: details.hasAttribute('open'),
      kinds: kinds,
    };
  });
  var manageHost = one('div.' + HOST_CLASS);
  return {
    mounted: manageHost === undefined ? false : one('section.' + SECTION_CLASS, manageHost) !== undefined,
    manageSlots: manageHost === undefined ? -1 : directSlots(manageHost),
    section: typeof section.read === 'function',
    groups: groups,
    reads: reads.slice(),
    copied: copied.slice(),
    opened: opened.slice(),
  };
}

window.__manage = {
  summary: summary,
  /** open clicks one group's own heading, which is the operator's act and the
   * only thing that starts that group's kinds (manage/shell.ts), and answers
   * the tree once the queued toggle event and the reads behind it have settled. */
  open: function (name) {
    var details = all('details.' + GROUP_CLASS).filter(function (each) {
      return each.getAttribute(GROUP_ATTRIBUTE) === name;
    })[0];
    if (details === undefined) throw new Error('the mount drew no group ' + name);
    one('summary', details).click();
    return new Promise(function (resolve) {
      setTimeout(function () { resolve(summary()); }, ${String(SETTLE_MS)});
    });
  },
  /** copyAt clicks one Copy-command and answers what that button's own label is
   * and what the section put on the clipboard. */
  copyAt: function (index) {
    var button = all('button.' + COPY_CLASS)[index];
    if (button === undefined) throw new Error('the mount drew no Copy-command ' + index);
    button.click();
    return { label: textOf(button), copied: copied.slice() };
  },
  /** viewAt is the same for a kind's open-view control. */
  viewAt: function (index) {
    var button = all('button.' + VIEW_CLASS)[index];
    if (button === undefined) throw new Error('the mount drew no view control ' + index);
    button.click();
    return { label: textOf(button), opened: opened.slice() };
  },
};
return true;
})()`

// ---------------------------------------------------------------------------
// The shape a case asserts on, and what the mount is expected to draw.
// ---------------------------------------------------------------------------

/** DrawnKind is one kind's section as the harness read it back. */
interface DrawnKind {
  readonly id: string | null
  readonly operation: string | null
  readonly title: string | null
  readonly columns: readonly (string | null)[]
  readonly rows: number
  readonly tables: number
  readonly cells: readonly Readonly<Record<string, string | null>>[]
  readonly empty: string | null
  readonly refusal: string | null
  readonly copies: readonly (string | null)[]
  readonly views: readonly (string | null)[]
}

/** DrawnGroup is one group and the kinds under it, in the order drawn. */
interface DrawnGroup {
  readonly name: string | null
  readonly title: string | null
  readonly open: boolean
  readonly kinds: readonly DrawnKind[]
}

/** Drawn is the whole mount: the tree, and the three things the harness
 * recorded while it was drawn. */
interface Drawn {
  readonly mounted: boolean
  readonly manageSlots: number
  readonly section: boolean
  readonly groups: readonly DrawnGroup[]
  readonly reads: readonly string[]
  readonly copied: readonly string[]
  readonly opened: readonly string[]
}

/** KindFacts is what one kind's section is expected to be: the descriptor's own
 * words (`id`, `title`, `source` and its columns, in order) and how many rows the
 * document above puts in its table. `rows` is the document's own count, so a
 * table that drew nothing, drew a row per column or drew one document's rows
 * twice fails here. `columns` is read off the drawn heading, so the one kind
 * whose document holds nothing -- which draws the empty sentence instead of a
 * table -- has none to list. */
interface KindFacts {
  readonly id: string
  readonly title: string
  readonly operation: string
  readonly columns: readonly string[]
  readonly rows: number
}

/** EXPECTED is the plan's list: the four groups in `MANAGE_GROUPS`' order, and
 * under each the kinds `manage/kinds/index.ts` names, with the columns and the
 * row count above. **It is the acceptance's first clause written out**, so the
 * red step (a kind left out of `index.ts`) is exactly the first case failing. */
const EXPECTED: readonly { readonly group: string; readonly kinds: readonly KindFacts[] }[] = [
  {
    group: 'People & access',
    kinds: [
      {
        id: 'members',
        title: 'Members',
        operation: 'members.list',
        columns: ['member', 'roles', 'status'],
        rows: 3,
      },
      {
        id: 'roles',
        title: 'Roles',
        operation: 'members.list',
        columns: ['member', 'role', 'scope', 'status'],
        rows: 1,
      },
      {
        // The one kind whose document holds nothing, so it draws the empty
        // sentence in place of a table and its heading is not there to read:
        // `columns` is what the drawn table's own heading holds, so the fact
        // here is none, and the descriptor's two columns are
        // `test/unit/manage-people.test.ts`'s.
        id: 'accounts',
        title: 'Claude accounts',
        operation: 'accounts.inventory',
        columns: [],
        rows: 0,
      },
      {
        // One row per (pool, member), which is what the document is: three
        // members across the two pools above.
        id: 'pools',
        title: 'Pools',
        operation: 'accounts.pools',
        columns: ['pool', 'owner', 'rule', 'member', 'state', 'nextReset', 'usage', 'headroom'],
        rows: 3,
      },
    ],
  },
  {
    group: 'Code',
    kinds: [
      {
        id: 'repos',
        title: 'Repositories',
        operation: 'repos.ls',
        columns: ['repo', 'provider', 'branch', 'image', 'workflows', 'accounts', 'published', 'local'],
        rows: 2,
      },
      {
        id: 'repos-status',
        title: 'Repository status',
        operation: 'repos.status',
        columns: ['repo', 'enrolled', 'check', 'result', 'problem', 'fix'],
        rows: 2,
      },
      {
        id: 'images',
        title: 'Image catalog',
        operation: 'image.catalog.ls',
        columns: ['entry', 'tags', 'default', 'source', 'description', 'digest'],
        rows: 2,
      },
      {
        id: 'recipes',
        title: 'Recipes',
        operation: 'recipes.ls',
        columns: ['name', 'layer', 'usable', 'steps', 'useWhen', 'description', 'shadowedBy', 'problem'],
        rows: 1,
      },
    ],
  },
  {
    group: 'Models & keys',
    kinds: [
      {
        id: 'models',
        title: 'Models',
        operation: 'models.list',
        columns: ['model', 'route', 'source', 'input', 'output', 'notes'],
        rows: 2,
      },
      {
        id: 'keys',
        title: 'API keys',
        operation: 'secrets.inventory',
        columns: ['name', 'scope', 'status'],
        rows: 2,
      },
    ],
  },
  {
    group: 'Budget',
    kinds: [
      {
        id: 'budget',
        title: 'Caps and kill state',
        operation: 'budget.show',
        columns: ['repository', 'daily', 'perRun', 'counted', 'left', 'kill'],
        rows: 2,
      },
    ],
  },
]

/** READ_ORDER is the operations the section asks for when every group is opened,
 * in the order it asks: each kind's own source, group by group, and
 * `members.list` twice because two kinds read that document. **It is derived
 * from `EXPECTED`** and not written again, so the two cannot disagree about what
 * the mount read. */
const READ_ORDER: readonly string[] = EXPECTED.flatMap((group) =>
  group.kinds.map((kind) => kind.operation),
)

/** kindIn is one kind's section anywhere in the mount, or a failure naming it. */
function kindIn(drawn: Drawn, id: string): DrawnKind {
  for (const group of drawn.groups) {
    const found = group.kinds.find((kind) => kind.id === id)
    if (found !== undefined) return found
  }
  throw new Error(`the mount drew no ${id} kind: it drew ${drawn.groups
    .flatMap((group) => group.kinds.map((kind) => kind.id))
    .join(', ')}`)
}

test.describe.configure({ mode: 'serial' })

let app: ElectronApplication
let page: Page
let cli: CliStub

test.beforeAll(async () => {
  // The app reads a `fugaro` before it opens a window: without an in-range one
  // it opens the refusal document, and the document this spec mounts into would
  // never be loaded. The stub is also the installation the entry's own dashboard
  // reads, which is why the section this spec draws is drawn beside a real one.
  cli = inRangeCliStub()
  cli.seedActiveInstallation()
  app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
  page = await app.firstWindow()
  await page.waitForLoadState('domcontentloaded')
})

test.afterAll(async () => {
  await app?.close()
  cli.remove()
})

/**
 * run evaluates one expression in the window's own world and answers what it
 * returned. It goes through the main process's `webContents.executeJavaScript`
 * -- the door `projects.spec.ts` uses -- because that is the world the harness
 * was installed in and the one the real CSP governs; the page is only waited on
 * for its document.
 */
async function run<T>(source: string): Promise<T> {
  const result = await app.evaluate(async ({ BrowserWindow }, code) => {
    const window = BrowserWindow.getAllWindows()[0]
    if (window === undefined) return null
    return (await window.webContents.executeJavaScript(code, true)) as unknown
  }, source)
  if (result === null) throw new Error('the console has no window to draw the Manage section in')
  return result as T
}

/** draw mounts a fresh section over the corpus above and answers the tree it
 * drew: nothing has been opened and nothing has been read yet. */
async function draw(): Promise<Drawn> {
  await run<boolean>(HARNESS)
  return run<Drawn>('window.__manage.summary()')
}

/** open opens one group the way an operator does and answers the tree after the
 * group's reads have landed. */
async function open(group: string): Promise<Drawn> {
  return run<Drawn>(`window.__manage.open(${JSON.stringify(group)})`)
}

test('draws the four groups in the plan’s order, collapsed, with every kind under its own group', async () => {
  const drawn = await draw()

  // The mount went into the host `projects.ts` draws for it, and that host is
  // the one of the tab's ten with **no busy slot of its own**: the section owns
  // a mark per kind, so a slot here would be a claim about a read this file
  // never started.
  expect(drawn.section).toBe(true)
  expect(drawn.mounted).toBe(true)
  expect(drawn.manageSlots).toBe(0)

  // **The four rows, in the spec's own order** (D2903, section 5): a group the
  // console has no kind in is not drawn at all, so a kind left out of
  // `kinds/index.ts` is a row missing here -- the red step.
  expect(drawn.groups.map((group) => group.name)).toEqual([...MANAGE_GROUPS])
  expect(drawn.groups.map((group) => group.title)).toEqual([...MANAGE_GROUPS])

  // Every group is folded when it paints, and every kind sits under the group its
  // descriptor names, in the order the list gives.
  expect(drawn.groups.map((group) => group.open)).toEqual([false, false, false, false])
  expect(drawn.groups.map((group) => group.kinds.map((kind) => kind.id))).toEqual(
    EXPECTED.map((group) => group.kinds.map((kind) => kind.id)),
  )

  // **And nothing has been read**: the disclosure is the gate, so a console that
  // read eleven documents the moment the tab was opened -- the thing the owner's
  // rule of 2026-10-31 forbids -- fails here rather than in a stopwatch.
  expect(drawn.reads).toEqual([])
})

test('opens each group and draws every kind’s own table, one read per kind', async () => {
  let drawn = await draw()
  for (const group of EXPECTED) drawn = await open(group.group)

  // One section per kind, each naming the operation it read and the heading it
  // drew, with its columns in the descriptor's order and one row per row its
  // document holds.
  expect(drawn.groups.map((group) => ({ name: group.name, kinds: group.kinds }))).toMatchObject(
    EXPECTED.map((group) => ({ name: group.group, kinds: group.kinds })),
  )
  for (const group of EXPECTED) {
    for (const kind of group.kinds) {
      const section = kindIn(drawn, kind.id)
      expect(section.refusal, `${kind.id} drew a refusal`).toBeNull()
      expect(section.tables, `${kind.id} drew a table`).toBe(kind.rows === 0 ? 0 : 1)
      expect(section.cells, `${kind.id}'s rows`).toHaveLength(kind.rows)
    }
  }

  // **The eleven reads, in the order the groups were opened**: each kind's own
  // source and no other, the two kinds of `members.list` reading that one
  // document, and no read repeated.
  expect(drawn.reads).toEqual(READ_ORDER)

  // Opening a group a second time starts nothing: the section keeps the groups
  // it has begun, so a fold and an unfold meet the rows already read rather
  // than a second round of children.
  const first = EXPECTED[0]?.group ?? ''
  drawn = await open(first)
  drawn = await open(first)
  expect(drawn.reads).toEqual(READ_ORDER)
  expect(kindIn(drawn, 'members').rows).toBe(3)
})

test('draws the kind whose document holds nothing as the console’s empty sentence', async () => {
  await draw()
  const drawn = await open('People & access')

  // `accounts.inventory` answered a document with no account in it. The kind has
  // nothing to draw and says so in the console's own words -- an empty table
  // would be a table with a heading and no reason to be empty -- and the
  // members beside it, read from the same group's other document, still draw
  // their rows: one kind's state is never another's.
  const accounts = kindIn(drawn, 'accounts')
  expect(accounts.rows).toBe(0)
  expect(accounts.tables).toBe(0)
  expect(accounts.empty).toBe(MANAGE_EMPTY_SENTENCE)
  expect(accounts.refusal).toBeNull()
  expect(kindIn(drawn, 'members').rows).toBe(3)

  // And the command that fills the list is drawn all the same: the Copy-command
  // is about the kind and not about the rows this read happened to answer with,
  // and an operator looking at an empty list is the one who wants it.
  expect(accounts.copies).toEqual(['fugaro accounts add NAME'])
})

test('puts the exact argv of every kind-level Copy-command on the clipboard', async () => {
  let drawn = await draw()
  for (const group of EXPECTED) drawn = await open(group.group)

  // **The whole list of Copy-commands the mount draws**, in the order the kinds
  // are drawn: the roles kind's grant (the only one of the three People kinds
  // with a command of its own), the accounts kind's add -- the empty list above
  // included -- the images and models kinds' own, which are the state both
  // writes' floors give every CLI this console can place (the constants are
  // unset, so the Add is not shown and the Copy-command is), and the keys
  // kind's two.
  //
  // A **row's** own command is the descriptor's `ManageRow.actions` (a pair's
  // `fugaro user revoke`, a repository's removal), and the shell draws the
  // kind's own `actions` only: nothing here carries a row action. When the shell
  // draws them, this list grows and this line moves with it.
  const labels = drawn.groups.flatMap((group) => group.kinds.flatMap((kind) => kind.copies))
  expect(labels).toEqual([
    'fugaro user grant MEMBER --role ROLE --scope SCOPE',
    'fugaro accounts add NAME',
    'fugaro image add',
    'fugaro models add',
    'fugaro secrets set NAME',
    'fugaro secrets rm NAME',
  ])

  // Only those five kinds offer one, and a kind with no command of its own
  // draws no control at all rather than a disabled one.
  expect(
    drawn.groups.flatMap((group) => group.kinds.filter((kind) => kind.copies.length > 0).map((kind) => kind.id)),
  ).toEqual(['roles', 'accounts', 'images', 'models', 'keys'])

  // The button's label **is** the command (`copyCommandText`, the one place the
  // argv is joined), and clicking it puts that exact text on the clipboard: the
  // paste is the line a person reads.
  const clicked = await run<{ readonly label: string | null; readonly copied: readonly string[] }>(
    'window.__manage.copyAt(0)',
  )
  expect(clicked.label).toBe(labels[0])
  expect(clicked.copied).toEqual([labels[0]])
})

test('opens the Spend view from the Budget kind’s one control, and reads its figures', async () => {
  // The kind is built for the compatibility this console has with its own floor,
  // which withholds no view: a reduced one draws no Spend link and no figures
  // (MO-B's own test), and a case that met one here would be reading a table
  // that withheld everything.
  expect(COMPATIBILITY.hidden).toEqual([])

  await draw()
  const drawn = await open('Budget')
  const budget = kindIn(drawn, 'budget')

  // Two repositories, one live and one halted, drawn from one document. The
  // repository is the document's own name, and the kill column is the word the
  // Live and Spend screens draw for the same fact -- `live` for a repository the
  // document carries no kill for, and the CLI's own sentence on the one it does.
  expect(budget.rows).toBe(2)
  expect(budget.cells[0]?.['repository']).toBe('acme/app')
  expect(budget.cells[0]?.['kill']).toBe('live')
  expect(budget.cells[1]?.['kill']).toContain('KILLED')

  // **One control, and it is the Spend report's door**: an open-view asks the
  // caller to show a view of the console's own rail, which is the entry's
  // business -- so this spec is the caller and reads what it was asked for.
  expect(budget.copies).toEqual([])
  expect(budget.views).toEqual(['Open the spend view'])
  const clicked = await run<{ readonly label: string | null; readonly opened: readonly string[] }>(
    'window.__manage.viewAt(0)',
  )
  expect(clicked.label).toBe('Open the spend view')
  expect(clicked.opened).toEqual(['spend'])
})
