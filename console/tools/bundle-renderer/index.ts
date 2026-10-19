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
// The console's style is four source files under `src/renderer/theme/`, and the
// served root is a flat allow-list (`SERVED_FILES`), so the four are emitted as
// **one** file, `dist/bundle/console.css`, served at `/console.css` and linked
// from each of the three documents. One file and not four, for the reason the
// scripts are bundled. The step is in this tool because this tool already owns
// the output directory.
//
// It reads the theme directory and refuses, with the same `BundleRefusal`:
//
//   - **a `.css` file that is not on `THEME_STYLESHEETS`** -- the list is the
//     directory's whole contents, so a stylesheet nobody reviewed is a file the
//     console would serve and nothing vouches for;
//   - **a listed file that is missing** -- so a rename fails the build rather
//     than silently dropping a layer of the cascade, and so the list cannot run
//     ahead of the files it names;
//   - **an `@import`** and **a remote `url()`** -- the bundle is one file and the
//     console is offline (`connect-src 'none'`), so an import would either fail
//     at runtime or be a network call the CSP forbids.
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

import { copyFileSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
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
  const owned = new Set<string>([
    basename(out),
    ...(options.siblings ?? []),
    ...documents.map((file) => basename(file)),
  ])

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

  const modules = moduleClosure(options.entry)
  writeFileSync(out, bundleText(modules))
  for (const document of documents) {
    copyFileSync(document, join(directory, basename(document)))
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
 * **The list grows with the files, one entry per task, and the refusal below is
 * what forces that rather than tidiness.** A listed file that is missing is a
 * build failure, so a task that named all four files before the last two exist
 * would turn `npm run build` red the moment it merged. The list is therefore
 * introduced by the task that lands the first two files (Task 6.2, this step)
 * and each later task appends its own in the commit that creates it:
 * `base.css` in Task 6.3 and `screens.css` in Task 6.4.
 *
 * It is `readonly` and frozen, and it is the list the tests read rather than a
 * second copy of it: `test/unit/bundle.test.ts` checks `console.css` against
 * *this list*, so the case is true here with two files in it and stays true when
 * 6.3 and 6.4 append theirs.
 */
export const THEME_STYLESHEETS: readonly string[] = Object.freeze(['tokens.css', 'palette.css'])

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
 * `img-src 'self' data:` allows it -- so it is excluded here on purpose. */
const REMOTE_SCHEME_RE = /^(?!(?:data|blob):)[a-z][a-z0-9+.-]*:/i

/** remoteUrlIn is the first remote `url()` of a stylesheet, as it is written,
 * or `null` when every one of them is a relative path on this origin. */
export function remoteUrlIn(css: string): string | null {
  for (const match of css.matchAll(CSS_URL_RE)) {
    const target = match[1] ?? match[2] ?? match[3] ?? ''
    if (target.startsWith('//') || REMOTE_SCHEME_RE.test(target)) return match[0].trim()
  }
  return null
}

/**
 * themeStylesheetSources reads the theme directory and answers its layers in
 * `THEME_STYLESHEETS` order, refusing the three things the header names.
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
  const unlisted = names.filter((name) => name.endsWith('.css') && !listed.has(name)).sort()
  if (unlisted.length > 0) {
    throw new BundleRefusal(
      `${directory} holds ${unlisted.join(', ')}, which THEME_STYLESHEETS does not list: the list is the theme directory's whole contents, so a stylesheet nobody reviewed cannot be served`,
    )
  }

  const sources: ThemeStylesheet[] = []
  for (const name of THEME_STYLESHEETS) {
    let css: string
    try {
      css = readFileSync(join(directory, name), 'utf8')
    } catch {
      throw new BundleRefusal(
        `THEME_STYLESHEETS names ${name}, which ${directory} does not hold: the list is the theme directory's whole contents and may not run ahead of it`,
      )
    }
    if (AT_IMPORT_RE.test(css)) {
      throw new BundleRefusal(
        `${name} holds an @import: the bundle is one file and the console is offline (connect-src 'none'), so an import would either fail at runtime or be a request the CSP forbids`,
      )
    }
    const remote = remoteUrlIn(css)
    if (remote !== null) {
      throw new BundleRefusal(
        `${name} loads ${remote} from another origin: the console is offline (connect-src 'none'), so a stylesheet may name only paths on this origin`,
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
 * re-check that: `bundleRenderer` is the one refusal for a foreign file in it,
 * and `consoleBundles` tells it that `console.css` is one of this build's own
 * (`siblings`), so the served root's foreign-file refusal still holds for
 * anything else. A second check here would be a second answer to one question.
 */
export function bundleStylesheet(options: StylesheetOptions): void {
  const out = resolve(options.out)
  const directory = dirname(out)
  const sources = themeStylesheetSources(options.directory)
  mkdirSync(directory, { recursive: true })
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

/** consoleBundles is every bundle `npm run build` writes, each told the others'
 * names -- and the stylesheet's -- so the served root's foreign-file refusal
 * still holds for anything else. `console.css` is written by
 * `bundleStylesheet`, which runs before these; the `siblings` mechanism is what
 * makes it *owned* rather than foreign. */
export function consoleBundles(consoleRoot: string): readonly BundleOptions[] {
  const all = [consoleBundlePaths(consoleRoot), pickerBundlePaths(consoleRoot)]
  const stylesheet = basename(consoleStylesheet(consoleRoot).out)
  return all.map((bundle) => ({
    ...bundle,
    siblings: [
      ...all.filter((other) => other !== bundle).map((other) => basename(other.out)),
      stylesheet,
    ],
  }))
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
    const consoleRoot = consoleRootHere(process.cwd())
    // The stylesheet first: its refusals are about the theme *sources*, and a
    // refusal there should not leave half a bundle behind in `dist/bundle/`.
    bundleStylesheet(consoleStylesheet(consoleRoot))
    for (const bundle of consoleBundles(consoleRoot)) bundleRenderer(bundle)
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : String(error)
    process.stderr.write(`${message}\n`)
    process.exitCode = 2
  }
}
