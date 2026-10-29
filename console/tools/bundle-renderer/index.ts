// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The renderer bundler: the compiled entry, and everything it requires, into one
// script the document loads (desktop-console plan Task 3.4a; design section 5.1
// rules 1, 3 and 5), and the theme's stylesheets into the one stylesheet the
// documents link (Task 6.2; design section 5.2).
//
// **Why there is a step here at all.** `src/renderer/index.html` has to load a
// script, and the two shapes a script can arrive in were both refused before
// this file existed:
//
//   - **the CSP allows no inline script** (`script-src 'self'`, design section
//     5.1 rule 3), so the drawing code cannot live in the document;
//   - **`<script type="module">` was rejected** (plan step 4, "the rejected
//     alternative"): native ES modules would need every relative import in
//     `src/shared/` and `src/renderer/` rewritten with an explicit extension and
//     a second `tsc` configuration, and the console's own pinning rule
//     (`test/unit/pinning.test.ts`) allows it no bundler dependency to do that
//     for it.
//
// So `tsc` stays the compiler and this is a **dependency-free step** that runs
// after it: it reads the compiled CommonJS modules and writes one file.
//
// **What it does, in order.** It walks the closure of `entry` by the modules'
// own `require` calls -- string-literal relative specifiers only, exactly as the
// e2e specs' hand-built `MODULES` registries already resolve them -- and wraps
// each module in a function inside one registry, then calls the entry. The
// result is one script with no `require` of its own beyond the registry's
// (`dist/bundle/console.js`), and the document that loads it gets exactly the
// drawing code and nothing else.
//
// **What it refuses, and why each refusal is a build failure rather than a
// warning.** A module in the closure that requires:
//
//   - **a bare specifier** (`electron`, a package name): the renderer runs in
//     the sandbox with no module loader, so this could only ever be a runtime
//     crash in a window with no Node in it. The preload's own rule is the same
//     one written the other way round -- `dist/preload/index.js` may require
//     `electron` and nothing else -- and this is the renderer's.
//   - **a Node built-in** (`node:fs`, `fs`): the same, and worse: a renderer
//     that reached a filesystem would be design section 5.1 rule 1 undone.
//   - **a computed specifier** (`require(name)`): the closure cannot be walked,
//     so the module cannot be vouched for. This is the shape that would let a
//     module load something nobody read.
//
// The check is a scan for `require(` in the **compiled** modules, which is why
// `tsconfig.build.json` emits them with `removeComments: true`: a comment
// mentioning a `require` (this file's own header names one, and so does the
// preload's) is not a require, and a scan that could not tell the difference
// would refuse a module for its prose.
//
// **What it also writes.** The served root is `dist/bundle/`, and a document
// that the handler serves must be in it: every `.html` file of
// `src/renderer/` is copied there **verbatim** (byte for byte, so the CSP
// `<meta>` copy `window.test.ts` compares with the header is the same file it
// always was). A file in that directory that this tool did not put there is a
// refusal -- the served root holds exactly `SERVED_FILES` (`src/main/protocol.ts`)
// and nothing compiled beside them -- and `test/unit/bundle.test.ts` holds the
// two lists equal.
//
// **The stylesheet step (Task 6.2; design section 5.2), and why it is here.**
// The console's style is the theme directory's whole contents -- every `.css`
// under `src/renderer/theme/`, named by `THEME_STYLESHEETS` below and by nothing
// else -- and the served root is a flat allow-list (`SERVED_FILES`), so the
// directory's files are emitted as **one** file, `dist/bundle/console.css`,
// served at `/console.css` and linked from each of the four documents. One file
// and not one per source file, for the reason the scripts are bundled. The step
// is in this tool because this tool already owns the output directory.
//
// It reads the theme directory and refuses, with the same `BundleRefusal`:
//
//   - **a `.css` file that is not on `THEME_STYLESHEETS`** -- the list is the
//     directory's whole contents, so a stylesheet nobody reviewed is a file the
//     console would serve and nothing vouches for. The name test is
//     case-insensitive, because the extension is: `X.CSS` is a stylesheet a
//     Windows checkout and a Linux one would disagree about, and the refusal
//     should not depend on which one made the bundle;
//   - **a listed file that is missing, or that is not a regular file** -- so a
//     rename fails the build rather than silently dropping a layer of the
//     cascade, and so the list cannot run ahead of the files it names. A
//     symlink is refused for the same reason a stray `.css` is: the list is the
//     directory's whole contents, and following a link would put bytes the
//     directory does not hold into the served stylesheet (review M2);
//   - **a backslash**, **an `@import`**, **a quoted string that names a URL**
//     and **a remote `url()`** -- the bundle is one file and the console is
//     offline (`connect-src 'none'`), so an import would either fail at runtime
//     or be a request the CSP forbids. The first of those four is what makes the
//     other three unbypassable: CSS reads `\` as the start of an escape, so
//     `@\69mport "./base.css";` is an `@import`, `u\72l(...)` is a `url(...)`
//     and `"\68ttp://…"` is a remote URL, and none of these scans unescapes
//     (review M1). The quoted-string scan is separate from the `url()` one
//     because `image-set()` and `-webkit-image-set()` take a bare string as a
//     URL, which is a remote load that never writes `url(`.
//
// **No CSS is parsed, minified, autoprefixed or rewritten.** The bytes on disk
// are the bytes served, which is what makes the contrast unit test
// (`test/unit/theme-tokens.test.ts`) meaningful: it reads the same literals the
// window paints. The only bytes added are comment banners -- one naming the
// build, one per source file.
//
// **`console.css` is owned by the build, through the `siblings` mechanism that
// already exists for the second bundle.** `bundleStylesheet` writes it and
// `consoleBundles` tells each `bundleRenderer` that it is theirs, so the served
// root's foreign-file refusal keeps holding for anything else -- and
// `test/unit/bundle.test.ts` holds `dist/bundle/` equal to `SERVED_FILES` in
// both directions.
//
// **No `eval`, no `new Function`, no code generation.** The bundle is the
// compiled modules, wrapped; nothing here builds code from data, and nothing it
// could build would run: the CSP has no `'unsafe-eval'`.

import { copyFileSync, lstatSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import type { Stats } from 'node:fs'
import { basename, dirname, join, relative, resolve, sep } from 'node:path'

/** BundleRefusal is the one error this tool throws: a closure it will not
 * bundle, or a served root it will not write into. Its message is the tool's own
 * sentence about what it found, and the file it names is a path in this
 * checkout, never anything a job wrote. */
export class BundleRefusal extends Error {
  override readonly name = 'BundleRefusal'
}

/** BundleOptions is what the step is given: the compiled entry to walk, and the
 * one file to write. Both are absolute paths -- `npm run build` builds them from
 * the console package, and the unit test builds them from its own. */
export interface BundleOptions {
  readonly entry: string
  readonly out: string
  /** siblings are the file names of the *other* bundles this build writes into
   * the same directory (Task 3.4b: the picker's `installations.js` beside the
   * dashboard's `console.js`). They are owned, so a second bundle step does not
   * refuse the first one's output as foreign; anything else still is. */
  readonly siblings?: readonly string[]
}

/** BundledModule is one module of the closure: the file it was read from, and
 * the registry key it is written under. */
export interface BundledModule {
  readonly file: string
  readonly key: string
  readonly code: string
}

/** REQUIRES_ANY_RE matches the start of every `require` call in a compiled
 * module, including a `module.require` one (`\b` holds after the dot). What
 * follows is read separately, because that is what decides whether the call is
 * one this tool can walk or a refusal. */
const REQUIRES_ANY_RE = /\brequire\s*\(/g

/** LITERAL_SPECIFIER_RE is a specifier that is a plain string literal: the only
 * shape this tool follows. An escape in the specifier (`require('.\x2fapp')`)
 * does not match, which is deliberate -- a specifier spelled that way is one a
 * reader cannot check against the registry either. */
const LITERAL_SPECIFIER_RE = /^\s*(["'])([^"'\\\n]*)\1\s*\)/

/** requireSpecifiers is every `require` call of a module: the specifier when the
 * call's first argument is a string literal, and `null` when it is anything else
 * (a computed one, a template, a concatenation). */
export function requireSpecifiers(code: string): readonly (string | null)[] {
  const specifiers: (string | null)[] = []
  for (const match of code.matchAll(REQUIRES_ANY_RE)) {
    const rest = code.slice((match.index ?? 0) + match[0].length)
    const literal = LITERAL_SPECIFIER_RE.exec(rest)
    specifiers.push(literal === null ? null : (literal[2] ?? ''))
  }
  return specifiers
}

/** isRelative reports whether a specifier is one this tool may follow: `./` or
 * `../` and nothing else. A bare name, a `node:` builtin, an absolute path and a
 * URL are all "not relative", and the caller's refusal says which it was. */
export function isRelative(specifier: string): boolean {
  return specifier === '.' || specifier === '..' || specifier.startsWith('./') || specifier.startsWith('../')
}

/** resolveSpecifier is a relative specifier against the module that wrote it, as
 * the bundle's own loader resolves it at runtime: `tsc` emits the extension, and
 * a specifier without one is completed with `.js` (which is what the e2e specs'
 * registries do too). The result is normalised, so `./../shared/version` and
 * `../shared/version` land on one file. */
export function resolveSpecifier(fromFile: string, specifier: string): string {
  const resolved = resolve(dirname(fromFile), specifier)
  return resolved.endsWith('.js') ? resolved : `${resolved}.js`
}

/**
 * moduleClosure is the walk: `entry` first, then every module a module of the
 * closure requires, once each. It answers in dependency order (a module comes
 * after everything it requires), which is what makes the written registry
 * readable -- the loader does not need the order, since it resolves lazily, but
 * a reader diffing two builds does.
 *
 * A module that is required twice is read once; a cycle is therefore walked
 * once, and the runtime handles it the way CommonJS does (the partially filled
 * `exports` of the module being defined), because the registry caches a module
 * before it runs its body.
 */
export function moduleClosure(entry: string): readonly BundledModule[] {
  const files = new Map<string, string>()
  const walk = (file: string, requiredBy: string): void => {
    if (files.has(file)) return
    let code: string
    try {
      code = readFileSync(file, 'utf8')
    } catch {
      throw new BundleRefusal(`${requiredBy} requires ${basename(file)}, which the build did not emit`)
    }
    files.set(file, code)
    for (const specifier of requireSpecifiers(code)) {
      if (specifier === null) {
        throw new BundleRefusal(
          `${relative(process.cwd(), file)} requires something that is not a string literal: the console's bundle is one file and a specifier it cannot read is one it cannot bundle`,
        )
      }
      if (!isRelative(specifier)) {
        throw new BundleRefusal(
          `${relative(process.cwd(), file)} requires ${JSON.stringify(specifier)}: the renderer may require its own modules and nothing else (no package, no electron, no Node built-in)`,
        )
      }
      walk(resolveSpecifier(file, specifier), file)
    }
  }
  walk(resolve(entry), basename(entry))

  // Dependency order: the modules this file requires first. `files` holds them
  // in discovery order, which is the entry first, so the sort is a reverse
  // topological pass over the requires this walk already validated.
  const ordered: BundledModule[] = []
  const placed = new Set<string>()
  const place = (file: string): void => {
    if (placed.has(file)) return
    placed.add(file)
    const code = files.get(file) ?? ''
    for (const specifier of requireSpecifiers(code)) {
      if (specifier !== null && isRelative(specifier)) place(resolveSpecifier(file, specifier))
    }
    ordered.push({ file, key: '', code })
  }
  place(resolve(entry))

  const root = commonRoot(ordered.map((module) => module.file))
  return ordered.map((module) => ({ ...module, key: `/${relative(root, module.file).split(sep).join('/')}` }))
}

/** commonRoot is the deepest directory that contains every module of the
 * closure: the registry's keys are the modules' paths relative to it, so for
 * this console they are `/renderer/entry.js`, `/shared/version.js` and so on --
 * the same shape the e2e specs' registries use, with `dist/` as the root. */
function commonRoot(files: readonly string[]): string {
  const directories = files.map((file) => dirname(resolve(file)).split(sep))
  let shared = directories[0] ?? []
  for (const directory of directories) {
    let at = 0
    while (at < shared.length && at < directory.length && shared[at] === directory[at]) at += 1
    shared = shared.slice(0, at)
  }
  return shared.join(sep) || sep
}

/** LOADER is the registry the modules run on. It is the e2e specs' loader
 * (`test/e2e/live.spec.ts` and its siblings) with the paths coming from the
 * registry's own keys rather than from a list a spec wrote: a specifier that is
 * not relative throws, which is the runtime half of the refusal above -- a
 * bundle that somehow held one fails in the window rather than loading
 * something.
 *
 * Everything is inside one function so that the bundle adds no global to the
 * document: the window gets the console's drawings and not a `registry`, a
 * `cache` or a `load`. */
const LOADER = `(function () {
'use strict';
var registry = Object.create(null);
var cache = Object.create(null);
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

/** bundleText is the whole bundle: the loader, one registry entry per module,
 * and the one call that runs the entry. */
export function bundleText(modules: readonly BundledModule[]): string {
  const entries = modules.map(
    (module) =>
      `registry[${JSON.stringify(module.key)}] = function (exports, require, module) {\n${module.code}\n};`,
  )
  const entryKey = modules[modules.length - 1]?.key ?? ''
  return `${LOADER}${entries.join('\n')}\nload(${JSON.stringify(entryKey)});\n})();\n`
}

/** documentsOf is the served documents of a build: every `.html` file beside the
 * compiled entry's *source*, which is `<entry>/../../src/renderer` for this
 * console's layout (`dist/renderer/entry.js` from `src/renderer/entry.ts`). They
 * are copied verbatim; the served root must hold them, and `SERVED_FILES` in
 * `src/main/protocol.ts` is the list the unit test holds this directory equal
 * to. */
export function documentsOf(entry: string): readonly string[] {
  const directory = join(dirname(resolve(entry)), '..', '..', 'src', 'renderer')
  let names: string[]
  try {
    names = readdirSync(directory)
  } catch {
    throw new BundleRefusal(`the served documents are not in ${directory}: a bundle without them is a window with no document`)
  }
  const documents = names.filter((name) => name.endsWith('.html')).sort()
  if (documents.length === 0) {
    throw new BundleRefusal(`${directory} holds no .html file: the served root would have no document in it`)
  }
  return documents.map((name) => join(directory, name))
}

/** ownedInRoot is the names *one* bundle claims in the served root: its own
 * file, the siblings it was told about, and the documents it copies there. The
 * union over a build's bundles is what the pre-write refusal below is given. */
function ownedInRoot(options: BundleOptions, documents: readonly string[]): ReadonlySet<string> {
  return new Set<string>([
    basename(resolve(options.out)),
    ...(options.siblings ?? []),
    ...documents.map((file) => basename(file)),
  ])
}

/**
 * refuseForeignFilesIn is the served root's **one** foreign-file refusal, and
 * it is a function rather than a paragraph inside `bundleRenderer` because it
 * runs twice: once in `bundleRenderer`, where it belongs to the step that owns
 * the directory, and once in `buildConsole`, **before the first write**, so a
 * refusal cannot leave a fresh artefact behind (review M3). One implementation
 * answers one question; the second call is that answer asked earlier.
 *
 * A directory that is not there yet holds nothing foreign, and is made.
 */
function refuseForeignFilesIn(directory: string, owned: ReadonlySet<string>): void {
  let existing: string[] = []
  try {
    existing = readdirSync(directory)
  } catch {
    mkdirSync(directory, { recursive: true })
  }
  const foreign = existing.filter((name) => !owned.has(name))
  if (foreign.length > 0) {
    throw new BundleRefusal(
      `${relative(process.cwd(), directory)} holds ${foreign.join(', ')}, which this build does not write: the served root holds the bundle and its documents and nothing else`,
    )
  }
}

/** isSymlink is whether `path` is a symbolic link. A path that is not there is
 * not one: the writer is about to create it, and a refusal for a name that
 * nothing holds would refuse its own output. */
function isSymlink(path: string): boolean {
  try {
    return lstatSync(path).isSymbolicLink()
  } catch {
    return false
  }
}

/**
 * refuseSymlinkedOutput refuses an output name that is a symbolic link (review
 * M4). Every file this tool writes is one it made itself -- `console.css`,
 * `console.js`, `installations.js`, the served documents -- and a link at one of
 * those names would put those bytes wherever the link points, which is outside
 * `dist/bundle/` and outside everything `SERVED_FILES`, the foreign-file
 * refusal and the packaged-file list are about.
 *
 * **It is half of the rule, and the directory is the other half**: a link at the
 * *directory* the output is written into does the same thing, and reaches the
 * write by a path this function never sees -- see `refuseSymlinkedDirectory`
 * below.
 */
function refuseSymlinkedOutput(path: string): void {
  if (isSymlink(path)) {
    throw new BundleRefusal(
      `${relative(process.cwd(), path)} is a symbolic link: this build writes that file itself, so following the link would put its bytes outside the served root`,
    )
  }
}

/**
 * refuseSymlinkedDirectory refuses an output *directory* that is a symbolic link
 * (review M4, second pass). `mkdirSync(directory, { recursive: true })` accepts
 * a symlinked directory without a word, so `dist/bundle -> ../somewhere` used to
 * build cleanly and leave the served stylesheet written outside the served root,
 * with `bundlerOutputs` and the served-root listing agreeing about a directory
 * neither of them had read.
 *
 * **What this does not check, and why.** The file and its directory are the last
 * two components of the path this build computes, and they are the two it reads;
 * a symlink *above* them (a `dist/` that is a link with a real `dist/bundle/`
 * under it) is still followed. A whole-chain check would resolve the path and
 * compare it with the literal one, and that refuses the platform's own trees: on
 * macOS `os.tmpdir()` is under `/var`, a symlink to `/private/var`, so it would
 * fail every planted test tree and every build made on a Mac. `dist/` is also in
 * `.gitignore` (this repository's and the console's), so unlike the M2 symlink
 * among the *sources*, nothing here arrives through a reviewed commit: this is
 * the write path refusing the one-step shape a tampered working tree would take.
 */
function refuseSymlinkedDirectory(directory: string): void {
  if (isSymlink(directory)) {
    throw new BundleRefusal(
      `${relative(process.cwd(), directory)} is a symbolic link: this build writes into that directory itself, so following the link would put the bundle outside the served root`,
    )
  }
}

/**
 * bundleRenderer is the step: walk the entry's closure, write the one script,
 * and copy the served documents beside it.
 *
 * The output directory is **owned** by this tool: a file in it that this run is
 * not about to write is a refusal rather than something to leave there. The
 * served root is what the protocol handler reads (`rendererRoot` in
 * `src/main/index.ts`), so a stale module beside the bundle would be a file the
 * console serves and nothing vouches for -- and `SERVED_FILES` would be a list
 * that is not the directory.
 */
export function bundleRenderer(options: BundleOptions): void {
  const out = resolve(options.out)
  const directory = dirname(out)
  const documents = documentsOf(options.entry)
  const owned = ownedInRoot(options, documents)

  refuseSymlinkedDirectory(directory)
  refuseForeignFilesIn(directory, owned)

  const modules = moduleClosure(options.entry)
  refuseSymlinkedOutput(out)
  writeFileSync(out, bundleText(modules))
  for (const document of documents) {
    const destination = join(directory, basename(document))
    refuseSymlinkedOutput(destination)
    copyFileSync(document, destination)
  }
  process.stdout.write(
    `wrote ${relative(process.cwd(), out)}: ${String(modules.length)} modules, ${String(documents.length)} documents\n`,
  )
}

/**
 * THEME_STYLESHEETS is the theme directory's **whole contents**, in the order
 * the cascade needs: the vocabulary, then the values, then the rules that read
 * them (design section 5.2).
 *
 * **The list grows with the files, and the refusal below is what forces each
 * task to land its entry beside the file rather than tidiness.** A listed file
 * that is missing is a build failure, so a task that named a file another task
 * has not created yet would turn `npm run build` red the moment it merged. The
 * list is therefore introduced naming the files the directory already holds --
 * Task 6.1 wrote `tokens.css` and `palette.css` with no list, and Task 6.2 (this
 * step) introduces the list that names them -- and each later task appends the
 * file it creates, in the commit that creates it: every intermediate state is a
 * real build of the files that exist.
 *
 * It is `readonly` and frozen, and it is the list the tests read rather than a
 * second copy of it: `test/unit/bundle.test.ts` checks `console.css` against
 * *this list*, so the case is true of the directory as it stands and stays true
 * as each task appends its file.
 */
export const THEME_STYLESHEETS: readonly string[] = Object.freeze([
  'tokens.css',
  'palette.css',
  // The console redesign's tokens (step 1): after the value set, so a screen the
  // redesign rebuilds reads its own names, and before the rules that read them.
  'flight-deck.css',
  // Task 6.3's: the reset, the document, the chrome and the focus rules. The
  // entry that landed with the file, in one commit, because the refusal above
  // fires in both directions -- a listed file that is missing and an unlisted
  // `.css` that is present are both a build failure.
  'base.css',
  // Task 6.4's: the per-screen rules. After this task the list is the theme
  // directory's whole contents and stays still, which is what the two refusals
  // above enforce from both sides.
  'screens.css',
])

/** StylesheetOptions is what the stylesheet step is given: the theme directory
 * it reads and the one file it writes. Both are absolute paths, like
 * `BundleOptions`, so the unit test can plant a tree of its own. */
export interface StylesheetOptions {
  readonly directory: string
  readonly out: string
}

/** ThemeStylesheet is one layer of the served stylesheet: the file it was read
 * from, and its bytes. */
export interface ThemeStylesheet {
  readonly name: string
  readonly css: string
}

/** STYLESHEET_BANNER is the one line every generated `console.css` opens with,
 * so a reader of the artefact knows it is not a file to edit. It ends with a
 * newline because the layers below follow it directly. */
const STYLESHEET_BANNER = '/* console.css — generated by tools/bundle-renderer; do not edit */\n'

/** AT_IMPORT_RE matches an `@import` rule, at-rule spelling only. Like the
 * `require` scan above, this is a **scan and not a parse**: a theme file whose
 * prose mentions `@import` is refused for it, which is the direction this tool
 * errs in -- the refusal names the file and the reason, and a comment that
 * spelled one is a comment to reword.
 *
 * The `i` flag is load-bearing and not decoration. At-rule names are ASCII
 * case-insensitive in CSS, so `@IMPORT url("./base.css");` is a valid spelling
 * of the same rule -- and it is the worst one to let through, because the
 * target is relative, so the remote-URL scan does not catch it either: the
 * build would succeed, the emitted `console.css` would carry an `@import` at a
 * path that is not in `dist/bundle/`, and the layer would 404 and drop out of
 * the cascade silently. `CSS_URL_RE` and `REMOTE_SCHEME_RE` below carry the
 * same flag for the same reason. */
const AT_IMPORT_RE = /@import\b/i

/** CSS_URL_RE matches every `url()` a stylesheet writes, in the three spellings
 * CSS allows -- quoted with either mark, and bare -- with the closing paren
 * included in the match, so a refusal can quote the call as it is written. */
const CSS_URL_RE = /url\(\s*(?:'([^']*)'|"([^"]*)"|([^)'"\s]*))\s*\)?/gi

/** REMOTE_SCHEME_RE is a URL scheme that leaves this machine. `data:` is not
 * one -- it is a byte string in the file rather than a request, and the CSP's
 * `img-src 'self' data:` allows it -- so it is excluded here on purpose.
 * `blob:` is excluded on the same reading (it names a byte string this document
 * made rather than a request), and the CSP allows it in no directive at all --
 * so a `url(blob:…)` in a theme file is admitted by this scan and blocked when
 * it is painted. That is the one silent failure this file knows about and does
 * not refuse: it is a `data:` URL's asymmetry, one directive wide, and the
 * README's stylesheet section states it (review N2). */
const REMOTE_SCHEME_RE = /^(?!(?:data|blob):)[a-z][a-z0-9+.-]*:/i

/** URL_STRIPPED_RE matches what the URL parser removes **anywhere** in a
 * target before it reads a scheme: ASCII tab and newline (tab, LF, CR). */
const URL_STRIPPED_RE = /[\t\n\r]/g

/** URL_EDGE_SPACE_RE matches the C0 controls and spaces the URL parser strips
 * at the two ends of a target. */
const URL_EDGE_SPACE_RE = /^[\u0000-\u0020]+|[\u0000-\u0020]+$/g

/** urlParserTarget is a target as the URL parser would read it: ASCII tab and
 * newline removed wherever they are, then the leading and trailing C0 controls
 * and spaces stripped (the WHATWG URL parser's "remove all ASCII tab or
 * newline" and "strip leading and trailing C0 control or space", in that order).
 *
 * **The two scans below test this and not the bytes as written**, because this
 * is what decides whether the target is a request off this origin. The parser
 * strips those characters *before* it reads a scheme, so `url(" https://…")` and
 * `image-set("\thttps://…" 1x)` are remote loads whose scheme is not at
 * character 0 -- and testing the raw bytes admitted both, which was an
 * undocumented way round the scan (review M1, left open by #701). A space and a
 * tab are ordinary characters inside a CSS string, so neither spelling is even
 * unusual.
 *
 * The refusal still quotes the target **as it is written** -- `match[0]` -- so
 * the message shows the reader the bytes to change. */
function urlParserTarget(target: string): string {
  return target.replace(URL_STRIPPED_RE, '').replace(URL_EDGE_SPACE_RE, '')
}

/** isRemoteTarget reports whether a target, as written, names a URL off this
 * origin: a scheme that leaves this machine, or the protocol-relative `//`. It
 * is the one target test both scans use, and it is why the `url()` scan and the
 * quoted-string scan agree about `data:`, `blob:` and a leading space. */
function isRemoteTarget(target: string): boolean {
  const parsed = urlParserTarget(target)
  return parsed.startsWith('//') || REMOTE_SCHEME_RE.test(parsed)
}

/** remoteUrlIn is the first remote `url()` of a stylesheet, as it is written,
 * or `null` when every one of them is a relative path on this origin. */
export function remoteUrlIn(css: string): string | null {
  for (const match of css.matchAll(CSS_URL_RE)) {
    const target = match[1] ?? match[2] ?? match[3] ?? ''
    if (isRemoteTarget(target)) return match[0].trim()
  }
  return null
}

/** BACKSLASH_RE matches a backslash anywhere in a theme source (review M1).
 *
 * CSS reads `\` as the start of an **escape**, and unescapes identifiers and
 * strings before anything else sees them: `@\69mport "./base.css";` is an
 * `@import`, `u\72l(...)` is a `url(...)`, and `"\68ttp://…"` is a remote URL.
 * Every scan below is a scan and not a parser -- that is the whole of its
 * design, and the reason a comment that mentions `@import` is refused -- so
 * none of them can see through an escape, and the escape is what is refused.
 *
 * The direction is the one this tool errs in everywhere: refusal. It costs a
 * theme file nothing, because an escaped spelling of an ordinary character says
 * nothing a literal one does not, and neither stylesheet in the tree holds a
 * backslash at all. A later theme file that wants a character -- an em dash in
 * `content`, say -- writes the character. */
const BACKSLASH_RE = /\\/

/** QUOTED_STRING_RE matches every quoted string of a stylesheet, in either
 * mark. The body excludes its **own** mark and allows the other one, because
 * CSS does: a single quote is an ordinary character inside a double-quoted
 * `<string>`, and `image-set("https://cdn.example/a'b.png" 1x)` is a valid URL
 * that a body excluding both marks could not see -- the exact silent remote load
 * this scan was added for (review M1, second pass).
 *
 * **The body is bounded by the newline, and the bound is load-bearing twice
 * over** (review M1, left open by #701: it is why the one spelling below that
 * carries a raw LF is `url()`'s and not this scan's). A raw newline is what ends
 * a CSS `<string>` -- the tokenizer calls the whole thing a `<bad-string-token>`
 * and the declaration is dropped before any URL is resolved -- and a body that
 * crossed one would pair an **unbalanced** quote with the next quote of its mark
 * wherever that is. Both theme files hold an apostrophe in their prose, so a `'`
 * opening a comment's aside pairs with the `'` of a remote URL further down, and
 * the scan then reads the prose instead of the URL.
 *
 * **That is measured, not argued**: with the body widened to `[\s\S]`,
 * `image-set('http://cdn.example/x.png' 1x)` and
 * `-webkit-image-set('//cdn.example/a"b.png' 2x)` planted after `tokens.css` are
 * both **admitted** -- the mutant is caught by the two cases in `bundle.test.ts`
 * that plant exactly those. So the widening buys one spelling the CSS tokenizer
 * drops anyway and pays for it with two that resolve, which is why it was tried,
 * observed and reverted rather than kept. The `url()` scan, whose quoted body is
 * not bounded by the newline, refuses the LF spelling; `bundle.test.ts`'s probe
 * states both halves. Every other byte the URL parser strips -- space, tab, CR,
 * FF -- is in this body already, and `urlParserTarget` strips all of them. */
const QUOTED_STRING_RE = /(["'])((?:(?!\1)[^\n])*)\1/g

/**
 * quotedRemoteIn is the first quoted string of a stylesheet that names a URL
 * off this origin, as it is written, or `null` (review M1).
 *
 * It exists because a quoted string is a URL in two places `CSS_URL_RE` does
 * not look: `image-set()` and `-webkit-image-set()` take a bare string as a
 * URL, so `image-set("https://cdn.example/x.png" 1x)` is a request neither the
 * `url()` scan nor the at-rule scan sees. The test is the `url()` scan's own --
 * a scheme that leaves this machine, or the protocol-relative `//` -- so
 * `data:` and `blob:` are read the same way in both places.
 *
 * The target test is **anchored at the start of the string** (`startsWith('//')`
 * or `REMOTE_SCHEME_RE`, which is `^`-anchored), and that is what keeps the cost
 * narrow: a string that merely *mentions* a URL is not a URL, so
 * `content: "see the design's notes, https://…"` passes. What is refused is a
 * string that *begins* as a URL and is not one -- `content: "Note: hello"` reads
 * as the scheme `Note:` -- and rewording the string is the fix. That is the whole
 * of the false-positive surface (review M1, second pass: the prose here named a
 * shape that does not reproduce).
 *
 * **"The start of the string" means the start as the URL parser reads it**, and
 * that refinement is the second pass's own (review M1, left open by #701): the
 * parser removes ASCII tab and newline wherever they are and strips the leading
 * and trailing C0 controls and spaces *before* it reads a scheme, so
 * `image-set(" https://…" 1x)` is a remote load with its scheme behind a byte the
 * old test required at character 0. Both scans now read the target through
 * `isRemoteTarget`, which is `urlParserTarget`'s answer, and the refusal still
 * quotes the string as it is written.
 *
 * It cannot be relaxed by skipping comments, either: a scan that struck out
 * comment syntax would hide an escape behind an opening comment marker written
 * *inside* a string, which is the bypass the backslash rule exists to close.
 * Neither stylesheet in the tree holds `://` at all.
 */
export function quotedRemoteIn(css: string): string | null {
  for (const match of css.matchAll(QUOTED_STRING_RE)) {
    if (isRemoteTarget(match[2] ?? '')) return match[0]
  }
  return null
}

/**
 * themeStylesheetSources reads the theme directory and answers its layers in
 * `THEME_STYLESHEETS` order, refusing the things the header names: an unlisted
 * `.css`, a listed file that is missing or is not a regular file, a backslash, an
 * `@import`, a quoted string that names a URL, and a remote `url()`.
 *
 * The directory read is what makes the list the directory's *whole contents* in
 * both directions: a listed file that is absent fails, and a `.css` that is
 * present and unlisted fails.
 */
export function themeStylesheetSources(directory: string): readonly ThemeStylesheet[] {
  let names: string[]
  try {
    names = readdirSync(directory)
  } catch {
    throw new BundleRefusal(
      `the theme's stylesheets are not in ${directory}: a build without them would serve a document with no style`,
    )
  }
  const listed = new Set(THEME_STYLESHEETS)
  // The extension test is case-insensitive, because the extension is: `X.CSS`
  // is a stylesheet, and a checkout that spells it that way must not be the one
  // where an unreviewed stylesheet is the file the list does not name (N3).
  const unlisted = names
    .filter((name) => name.toLowerCase().endsWith('.css') && !listed.has(name))
    .sort()
  if (unlisted.length > 0) {
    throw new BundleRefusal(
      `${directory} holds ${unlisted.join(', ')}, which THEME_STYLESHEETS does not list: the list is the theme directory's whole contents, so a stylesheet nobody reviewed cannot be served`,
    )
  }

  const sources: ThemeStylesheet[] = []
  for (const name of THEME_STYLESHEETS) {
    const file = join(directory, name)
    let stats: Stats | undefined
    try {
      stats = lstatSync(file)
    } catch {
      stats = undefined
    }
    if (stats === undefined) {
      throw new BundleRefusal(
        `THEME_STYLESHEETS names ${name}, which ${directory} does not hold: the list is the theme directory's whole contents and may not run ahead of it`,
      )
    }
    // `lstatSync` and not `statSync`: a symlink is refused as the thing it is
    // rather than followed to what it points at (review M2). The list is the
    // theme directory's whole contents, and a link is a name whose bytes are
    // somewhere else -- `palette.css -> ~/.config/…` or `-> ../../../somewhere`
    // would be read into `console.css`, served to every window and packaged
    // into the `.app`, and no test reading the sources would see it.
    if (!stats.isFile()) {
      throw new BundleRefusal(
        `${name} is not a regular file: the list is the theme directory's whole contents, and a symlink or a directory there would put bytes the directory does not hold into the served stylesheet`,
      )
    }
    let css: string
    try {
      css = readFileSync(file, 'utf8')
    } catch {
      throw new BundleRefusal(
        `${name} could not be read: the theme directory holds it and the build cannot read it, so that layer would be missing from the served stylesheet`,
      )
    }
    if (BACKSLASH_RE.test(css)) {
      throw new BundleRefusal(
        `${name} holds a backslash: CSS reads one as the start of an escape, so an @import, a url() or a remote string can be spelled in a way these scans do not unescape, and a theme source that needs none of those spellings needs no backslash at all`,
      )
    }
    if (AT_IMPORT_RE.test(css)) {
      throw new BundleRefusal(
        `${name} holds an @import: the bundle is one file and the console is offline (connect-src 'none'), so an import would either fail at runtime or be a request the CSP forbids`,
      )
    }
    // The `url()` scan first, deliberately: a quoted `url('http://…')` holds a
    // string this scan would also match, and the refusal a reader wants is the
    // one about the call that made the request rather than about the mark
    // around its argument. `image-set()` strings are what is left over, which
    // is exactly the shape the `url()` scan cannot see.
    const remote = remoteUrlIn(css)
    if (remote !== null) {
      throw new BundleRefusal(
        `${name} loads ${remote} from another origin: the console is offline (connect-src 'none'), so a stylesheet may name only paths on this origin`,
      )
    }
    const quoted = quotedRemoteIn(css)
    if (quoted !== null) {
      throw new BundleRefusal(
        `${name} holds ${quoted}, a quoted string that names a URL: image-set() and -webkit-image-set() take a bare string as a URL, and the console is offline (connect-src 'none'), so a stylesheet may name only paths on this origin`,
      )
    }
    sources.push({ name, css })
  }
  return sources
}

/** stylesheetText is the artefact: one banner line naming the build, then each
 * layer preceded by a comment banner carrying the file's name, so a reader can
 * see the seam. The layer's own bytes are written unchanged, with a newline
 * appended only when the file did not end in one -- so the seam is always a line
 * of its own and nothing else about the file moves. */
export function stylesheetText(sources: readonly ThemeStylesheet[]): string {
  const layers = sources.map((source) => {
    const body = source.css.endsWith('\n') ? source.css : `${source.css}\n`
    return `/* ${source.name} */\n${body}`
  })
  return `${STYLESHEET_BANNER}${layers.join('')}`
}

/**
 * bundleStylesheet is the step: read the theme directory, write the one
 * stylesheet the documents link.
 *
 * The output directory is **owned** by this build, and this function does not
 * re-check that: `refuseForeignFilesIn` is the one refusal for a foreign file in
 * it, `bundleRenderer` calls it, and `consoleBundles` tells the renderer that
 * `console.css` is one of this build's own (`siblings`), so the served root's
 * foreign-file refusal still holds for anything else. A second check here would
 * be a second answer to one question -- and the ordering question that check
 * raises is `buildConsole`'s, which asks it **before the first write** rather
 * than here, because a refusal that lands after `console.css` was written leaves
 * a fresh artefact in a directory the build then abandons (review M3).
 */
export function bundleStylesheet(options: StylesheetOptions): void {
  const out = resolve(options.out)
  const directory = dirname(out)
  const sources = themeStylesheetSources(options.directory)
  refuseSymlinkedDirectory(directory)
  mkdirSync(directory, { recursive: true })
  refuseSymlinkedOutput(out)
  writeFileSync(out, stylesheetText(sources))
  process.stdout.write(
    `wrote ${relative(process.cwd(), out)}: ${String(sources.length)} stylesheets\n`,
  )
}

/** consoleStylesheet is the console's own stylesheet step, from the package the
 * command runs in: `src/renderer/theme/` into `dist/bundle/console.css`. */
export function consoleStylesheet(consoleRoot: string): StylesheetOptions {
  return {
    directory: join(consoleRoot, 'src', 'renderer', 'theme'),
    out: join(consoleRoot, 'dist', 'bundle', 'console.css'),
  }
}

/** consoleBundlePaths is the console's own build output, from the package the
 * command runs in: `dist/renderer/entry.js` (what `tsc -p tsconfig.build.json`
 * emits from `src/renderer/entry.ts`) into `dist/bundle/console.js`. */
export function consoleBundlePaths(consoleRoot: string): { readonly entry: string; readonly out: string } {
  return {
    entry: join(consoleRoot, 'dist', 'renderer', 'entry.js'),
    out: join(consoleRoot, 'dist', 'bundle', 'console.js'),
  }
}

/** pickerBundlePaths is the second entry (Task 3.4b): the picker's document
 * script, `dist/renderer/installations.js`, into `dist/bundle/installations.js`
 * beside the dashboard's bundle. */
export function pickerBundlePaths(consoleRoot: string): { readonly entry: string; readonly out: string } {
  return {
    entry: join(consoleRoot, 'dist', 'renderer', 'installations.js'),
    out: join(consoleRoot, 'dist', 'bundle', 'installations.js'),
  }
}

/** confirmBundlePaths is the third entry (V2.5.1a part 2b-2a): the launch
 * confirmation window's document script, `dist/renderer/confirm.js`, into
 * `dist/bundle/confirm.js` beside the other two. It is a bundle of its own for
 * the reason its document is a document of its own: the window has its own
 * preload and its own two methods, and neither the dashboard's screens nor the
 * picker's list has any business in it. */
export function confirmBundlePaths(consoleRoot: string): { readonly entry: string; readonly out: string } {
  return {
    entry: join(consoleRoot, 'dist', 'renderer', 'confirm.js'),
    out: join(consoleRoot, 'dist', 'bundle', 'confirm.js'),
  }
}

/** WORKER_FILE is the one file in the served root that this build neither
 * compiles nor writes: elkjs's layout worker, copied byte for byte out of the
 * pinned package (GV2.2). It is a devDependency rather than a dependency
 * because nothing requires it at runtime -- the renderer reaches it through
 * `new Worker`, and the 8 MB package stays out of the asar because only this
 * one file is copied. Serving it verbatim is the point: it is minified by its
 * own build, so anything this build did to it would be a second, unreviewed
 * artefact under the console's CSP (`script-src 'self'`, unchanged). */
export const WORKER_FILE = 'elk-worker.min.js'

/** consoleWorker is the worker's source and destination: from the installed
 * `elkjs` package into the served root, where `SERVED_FILES` lists it. */
export function consoleWorker(consoleRoot: string): { readonly source: string; readonly out: string } {
  return {
    source: join(consoleRoot, 'node_modules', 'elkjs', 'lib', WORKER_FILE),
    out: join(consoleRoot, 'dist', 'bundle', WORKER_FILE),
  }
}

/** refuseMissingSource refuses the worker's source when it is not a file, in the
 * shape the rest of this tool refuses in: a sentence rather than `readFileSync`'s
 * bare `ENOENT`. The ordinary way this happens is a tree that has not run
 * `npm ci` (the worker is copied out of the installed `elkjs` package), and the
 * failure that matters is the *order*: without this refusal the build would stop
 * with the missing input **after** `console.css` had been written, which is the
 * half-a-bundle shape review M3 asked `buildConsole`'s pre-write pass to
 * prevent. `bundleWorker` asks it again itself, one step before the bytes. */
function refuseMissingSource(source: string): void {
  let stats: Stats | undefined
  try {
    stats = lstatSync(source)
  } catch {
    stats = undefined
  }
  if (stats === undefined || !stats.isFile()) {
    throw new BundleRefusal(
      `${source} is not a file: this build copies elkjs's worker out of the installed package, so run npm ci in console/ first`,
    )
  }
}

/** bundleWorker copies the worker verbatim, re-checking its source and its
 * destination as the other writers do, so a tree without the package or a link
 * planted between `buildConsole`'s refusal pass and this write is refused here
 * rather than followed. */
export function bundleWorker(worker: { readonly source: string; readonly out: string }): void {
  refuseMissingSource(worker.source)
  refuseSymlinkedOutput(worker.out)
  writeFileSync(worker.out, readFileSync(worker.source))
}

/** FONT_FILES are the vendored web fonts (owner's ruling of 2026-10-28), each a
 * family directory under `src/renderer/fonts/` and a file name. They are copied
 * byte for byte, flat, into the served root, where `SERVED_FILES` lists them and
 * `flight-deck.css`'s `@font-face` rules name them by the same flat path, so
 * `font-src 'self'` admits them with no directive added. Their licences and
 * digests are `src/renderer/fonts/README.md` and `THIRD_PARTY_NOTICES.md`. */
export const FONT_FILES: readonly { readonly family: string; readonly file: string }[] = Object.freeze([
  { family: 'instrument-sans', file: 'InstrumentSans-Regular.woff2' },
  { family: 'instrument-sans', file: 'InstrumentSans-Medium.woff2' },
  { family: 'instrument-sans', file: 'InstrumentSans-SemiBold.woff2' },
  { family: 'instrument-sans', file: 'InstrumentSans-Bold.woff2' },
  { family: 'ibm-plex-mono', file: 'IBMPlexMono-Regular.woff2' },
  { family: 'ibm-plex-mono', file: 'IBMPlexMono-Medium.woff2' },
  { family: 'ibm-plex-mono', file: 'IBMPlexMono-SemiBold.woff2' },
])

/** consoleFonts is each font's source and destination. */
export function consoleFonts(consoleRoot: string): readonly { readonly source: string; readonly out: string }[] {
  return FONT_FILES.map(({ family, file }) => ({
    source: join(consoleRoot, 'src', 'renderer', 'fonts', family, file),
    out: join(consoleRoot, 'dist', 'bundle', file),
  }))
}

/** bundleFont copies one font verbatim, refusing a missing source and a
 * symlinked destination as the worker's copy does. */
export function bundleFont(font: { readonly source: string; readonly out: string }): void {
  refuseMissingFont(font.source)
  refuseSymlinkedOutput(font.out)
  writeFileSync(font.out, readFileSync(font.source))
}

function refuseMissingFont(source: string): void {
  let stats: Stats | undefined
  try {
    stats = lstatSync(source)
  } catch {
    stats = undefined
  }
  if (stats === undefined || !stats.isFile()) {
    throw new BundleRefusal(`${source} is not a file: the vendored fonts live under src/renderer/fonts/`)
  }
}

/** consoleBundles is every bundle `npm run build` writes, each told the others'
 * names -- and the stylesheet's -- so the served root's foreign-file refusal
 * still holds for anything else. `console.css` is written by
 * `bundleStylesheet`, which runs before these; the `siblings` mechanism is what
 * makes it *owned* rather than foreign. */
export function consoleBundles(consoleRoot: string): readonly BundleOptions[] {
  const all = [consoleBundlePaths(consoleRoot), pickerBundlePaths(consoleRoot), confirmBundlePaths(consoleRoot)]
  const stylesheet = basename(consoleStylesheet(consoleRoot).out)
  return all.map((bundle) => ({
    ...bundle,
    siblings: [
      ...all.filter((other) => other !== bundle).map((other) => basename(other.out)),
      stylesheet,
      WORKER_FILE,
      ...FONT_FILES.map(({ file }) => file),
    ],
  }))
}

/**
 * buildConsole is the whole bundle step of `npm run build`, in its order, and it
 * is a function rather than four lines in the entry point below because the
 * **order** is a claim a test has to be able to make (review M3).
 *
 * The order is: **every refusal this build can make**, then the stylesheet, then
 * elkjs's worker (GV2.2), then the three renderer bundles. The refusals come
 * first because they are the ones that can stop the build, and a build that
 * stops must not have written anything: with
 * the stylesheet first, a foreign file in `dist/bundle/` was a build failure that
 * nonetheless left a fresh `console.css` behind. That was harmless -- a failed
 * build is not packaged -- but "a refusal must not leave half a bundle behind"
 * held one way round only, and the difference between the two orders is a line.
 *
 * **The pre-write phase asks all of them, and not only the two the served root
 * owns** (review M4, left open by #701). The pair the directory owns
 * (`refuseSymlinkedDirectory`, `refuseForeignFilesIn`) was here; beside them
 * are now `refuseMissingSource` for the worker the build copies and
 * `refuseSymlinkedOutput` for **every name this build is about to write** -- the stylesheet, each bundle, and each served document each bundle
 * copies -- and `moduleClosure` for each entry, whose walk is where a bare
 * specifier, a computed one and a module the build did not emit are refused. A
 * symlinked `dist/bundle/console.js`, a symlinked `dist/bundle/index.html` and a
 * module that required `electron` all used to fail one writer further in, after
 * `console.css` -- and in the last two cases `console.js` as well -- had already
 * been written. The writers ask these questions too (`bundleStylesheet` and
 * `bundleRenderer` each refuse their own output, and `bundleRenderer` re-checks
 * each destination as it copies), so this pass is that answer asked earlier
 * rather than a second rule, exactly as `refuseForeignFilesIn`'s first call is.
 *
 * It is **not a lock**, and the writers' own checks stay for that reason: a link
 * planted between this pass and a write is refused by the writer that reaches
 * it, one step before the bytes land. What the pass buys is that the ordinary
 * failure -- the tree as it was when the build started -- cannot leave a fresh
 * artefact behind.
 *
 * Everything else in the step is unchanged: the stylesheet's refusals about the
 * theme *sources* are `themeStylesheetSources`', asked by the first writer
 * before it writes a byte.
 */
export function buildConsole(consoleRoot: string): void {
  const bundles = consoleBundles(consoleRoot)
  const first = bundles[0]
  if (first === undefined) {
    throw new BundleRefusal(`${consoleRoot} has no bundle to write: the console's build writes three`)
  }
  const directory = dirname(resolve(first.out))
  const stylesheet = consoleStylesheet(consoleRoot)
  const worker = consoleWorker(consoleRoot)
  const fonts = consoleFonts(consoleRoot)
  const owned = new Set<string>([WORKER_FILE, ...FONT_FILES.map(({ file }) => file)])
  const outputs: string[] = [resolve(stylesheet.out), resolve(worker.out), ...fonts.map((font) => resolve(font.out))]
  for (const bundle of bundles) {
    const documents = documentsOf(bundle.entry)
    for (const name of ownedInRoot(bundle, documents)) owned.add(name)
    outputs.push(resolve(bundle.out))
    for (const document of documents) outputs.push(join(directory, basename(document)))
  }

  refuseSymlinkedDirectory(directory)
  refuseForeignFilesIn(directory, owned)
  refuseMissingSource(worker.source)
  for (const font of fonts) refuseMissingFont(font.source)
  for (const output of outputs) refuseSymlinkedOutput(output)
  for (const bundle of bundles) moduleClosure(bundle.entry)

  bundleStylesheet(stylesheet)
  bundleWorker(worker)
  for (const font of fonts) bundleFont(font)
  for (const bundle of bundles) bundleRenderer(bundle)
}

/** consoleRootHere is the package `npm run build` runs in. It is read from the
 * working directory and **checked**, rather than guessed from this module's own
 * location: `package.json`'s name is the one thing that says a directory is this
 * console, so a build started somewhere else refuses instead of writing a bundle
 * beside whatever it found. */
export function consoleRootHere(cwd: string): string {
  let pkg: { name?: unknown }
  try {
    pkg = JSON.parse(readFileSync(join(cwd, 'package.json'), 'utf8')) as { name?: unknown }
  } catch {
    throw new BundleRefusal(`${cwd} is not the console package: npm run build runs from console/`)
  }
  if (pkg.name !== 'fugaro-console') {
    throw new BundleRefusal(`${cwd} is not the console package: npm run build runs from console/`)
  }
  return cwd
}

/** isEntryPoint is true when this file was run rather than imported: `argv[1]`
 * is this module, compiled to `index.js` under `.bundle-renderer/` (or run from
 * source as `index.ts` by a loader). A test importing it -- vitest's entry is
 * `argv[1]` there -- bundles nothing. */
export function isEntryPoint(argv1: string | undefined): boolean {
  return argv1 !== undefined && /bundle-renderer[\\/]index\.(?:js|cjs|ts)$/.test(argv1)
}

if (isEntryPoint(process.argv[1])) {
  try {
    buildConsole(consoleRootHere(process.cwd()))
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : String(error)
    process.stderr.write(`${message}\n`)
    process.exitCode = 2
  }
}
