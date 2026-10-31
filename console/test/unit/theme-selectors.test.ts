// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The stylesheet's selectors against the design's §2 inventory (desktop-console
// plan Task 6.3; design sections 2, 2.1 and 5.5).
//
// The design's §2 opens with the licence every rule in this theme is written
// under: "**a rule may only select a hook this table names**", and a hook a rule
// genuinely wants is "added **here first**, in the PR that wants it, so the
// test's list and the stylesheet's selectors move together". This file is that
// test for the chrome, and it is the one case that can see the shape of rule
// Task 6.3 adds: a rule written against a class **the entry draws** rather than
// against a class a screen declares.
//
// **The list is read out of the design document, not written out here.** A
// second copy of §2 in this file would be a list that drifts from the one a
// reader checks, and the licence rule would then be enforced against the copy
// rather than against the design. So the document is read from the repository
// root (`docs/design/console-styling.md`), its §2 is taken whole, and every
// backticked token in it is read: `div.console-rail-head` names the class
// `console-rail-head`, `data-installation="chosen"` names the attribute
// `data-installation`, and a token that is neither (`console/src/renderer/
// entry.ts`, `renderVersionSkew`) names no hook and is skipped.
//
// **§2 whole, and not its tables alone -- Task 6.4 is why.** Task 6.3's scan
// read §2's table rows, which is where every hook of the four screens and of the
// chrome is listed; §2.7 and §2.8, the degraded screen and the unavailable and
// refusal screens, have **no tables at all** and name their classes in their
// prose, and §2.4's `data-*` list is a prose line under its table rather than a
// row of it. A scan narrowed back to the tables would therefore refuse the very
// rules Task 6.4 writes -- `section.degraded`, `div.unavailable-body`,
// `[data-panel]` -- and would do it by claiming the design does not name hooks
// it names two paragraphs further down. The reading is widened, not loosened:
// `HOOK` keeps only tokens shaped like a hook, so a prose word (`class`, `title`,
// `putReading`) is not a licence to select `.class` or `.title`, and
// `NOT_A_LICENCE` keeps out the `data-*` attributes the design names
// **in order to refuse them** ("A hook that is emitted and not listed here is
// not a licence to write a rule against it").
//
// **What is scanned is the selectors, not the file's text.** Comments are
// stripped first, so a hook named in this suite's own prose -- or in the
// stylesheet's long header -- is not a selector, and only what a rule selects
// is checked. Classes come from `.name` and attribute selectors from `[name`,
// which between them are every way a rule in these stylesheets names an element.
//
// **`screens.css` joins this scan in Task 6.4**, which is the task that writes
// it: the plan gives that task "a case that every selector in `screens.css`
// names a hook the design's §2 inventory lists ... the screens' own class
// literals **and the chrome's hooks of §2.1** (the rail's twelve, which Task 6.3
// draws and this scan must see)". The inventory read below is the shared half,
// and the per-file half is one entry in `SCANNED` -- which is why this file is
// named for the selectors rather than for `base.css`.

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { THEME_STYLESHEETS } from '../../tools/bundle-renderer'

/** CONSOLE_ROOT is the console package, which is where the theme is read from. */
const CONSOLE_ROOT = join(__dirname, '..', '..')

/** DESIGN_DOCUMENT is the design this stylesheet is written against, at the
 * repository root. It is the one file this suite reads from outside the console,
 * and it is read because §2's tables **are** the licence: a copy of them here
 * would be a copy, and the copy is what would be enforced. */
const DESIGN_DOCUMENT = join(CONSOLE_ROOT, '..', 'docs', 'design', 'console-styling.md')

/** INVENTORY_HEADING and NEXT_SECTION bound §2 in the design document. */
const INVENTORY_HEADING = '## 2. Screen inventory'
const NEXT_SECTION = '## 3.'

/** SCANNED is the stylesheets whose selectors are checked, and the task that
 * adds each: Task 6.3 wrote `base.css` and Task 6.4 appends `screens.css` here,
 * in the same commit that creates it -- a stylesheet the bundler serves and this
 * scan does not read is a stylesheet whose selectors nothing checks. */
const SCANNED: readonly string[] = Object.freeze(['flight-deck.css', 'base.css', 'screens.css'])

/** RAIL_HOOKS is the rail's fourteen classes, which §2.1 names and §4.1 draws as
 * fifteen elements (the name line, the warning mark and the empty-state line
 * are the owner's design ruling of 2026-10-25; the collapse control went with
 * the rail's column when the header switcher replaced it on 2026-10-28). They
 * are listed here because the count is the claim: the stylesheet selects these
 * fourteen and no fifteenth, and a case below says so. */
const RAIL_HOOKS: readonly string[] = Object.freeze([
  'console-rail',
  'console-rail-head',
  'console-rail-title',
  'console-rail-list',
  'console-rail-row',
  'console-rail-nameline',
  'console-rail-name',
  'console-rail-warning',
  'console-rail-project',
  'console-rail-state',
  'console-rail-spend',
  'console-rail-add',
  'console-rail-empty',
  'console-rail-hint',
])

/** ELEMENT_PREFIX is the element names the design's tables qualify a class with
 * (`div.console-rail-head`, `header.degraded-header`). Stripping one is what
 * turns a token into the class a rule would select; a token with any other
 * prefix is not a hook. */
const ELEMENT_PREFIX = /^(?:a|aside|button|code|dd|details|div|dl|figure|form|g|h1|h2|h3|header|input|label|li|main|nav|ol|p|path|pre|rect|section|span|summary|svg|table|tbody|td|text|th|tr|tspan|ul)\./

/** HOOK is what a token has to look like to be a hook rather than a word of
 * prose. A class hook carries one of the prefixes §2's tables and prose are
 * built from -- a screen's own (`live-`), the chrome's (`console-`), the
 * installation line's (`installation-`) or a screen root spelled bare -- and an
 * attribute hook is a `data-*` or an `aria-*`. Everything else the section
 * mentions (`class`, `title`, `role`, `putReading`, `UNAVAILABLE_MARK`,
 * `path:line`) names no hook a stylesheet could select. */
const HOOK = /^(?:(?:live|spend|projects|run|degraded|graph|coordinator|console|unavailable|installation|notice)(?:-[a-z0-9-]+)?|(?:data|aria)-[a-z0-9-]+|hidden)$/

/** CLASS_HOOK is the class half of `HOOK`, and `SUFFIX` is the shorthand §2.7
 * and §2.8 write their hook sets with: the degraded screen's section is two
 * paragraphs of prose rather than a table, and it names its run row
 * "(`degraded-run-id`, `-repo`, `-status`, ... `-total`, `data-run`, ...)" and
 * its feed age as "`degraded-since` + `-word`/`-at`/`-age`".
 *
 * A `-suffix` is a hook of the name it abbreviates, and reading §2 literally
 * without them would refuse to license `degraded-run-repo` while §2 names it two
 * characters further left -- the same defect the `HOOK`/table split avoids.
 *
 * **§2.14 adds `notice`, and `hidden` is a hook of its own.** A notice's card is
 * one family of hooks (`notice`, `notice-mark`, `notice-facts`, ...) and §2.14
 * is where they are listed; `hidden` is the attribute the theme puts a thing
 * away with, and the reset's `[hidden]` rule is the one rule that selects it.
 *
 * **§2.7 abbreviates two ways in one paragraph, so both are read.** The run row
 * is `degraded-run-id`, `-repo`, `-status`, ... — the suffix **replaces** the
 * last segment (`degraded-run-repo`) — while the feed age is `degraded-since` +
 * `-word`/`-at`/`-age`, where it is **appended** (`degraded-since-word`). Which
 * one a suffix is cannot be decided from the suffix, so the scan licenses where
 * the design points and expands both readings; the extra names (`degraded-word`,
 * `degraded-run-id-repo`) are hooks §2 does not intend and no screen emits, and a
 * licence that names one hook too many is harmless where one that names a hook
 * too few refuses a rule the design asked for. And the expansion is only where
 * it can be read at all: a suffix takes the class hook it **immediately**
 * follows, and any token that is neither is skipped -- so the `-num` of §2.10's
 * prose, which follows a `cell(...)` signature rather than a hook, licenses
 * nothing. */
const CLASS_HOOK = /^(?:live|spend|projects|run|degraded|graph|coordinator|console|unavailable|installation|notice)-[a-z0-9-]+$/

/** SUFFIX is one `-suffix`, or a run of them joined by `/` as §2.7's
 * `-word`/`-at`/`-age` is written. */
const SUFFIX = /^-[a-z0-9-]+(?:\/-[a-z0-9-]+)*$/

/** NOT_A_LICENCE is the attributes §2 names **in order to refuse them**: §2's
 * licence paragraph names `data-hidden` and `data-skew` explicitly ("they are
 * emitted, no rule needs them, and a rule that suddenly wants one adds it to
 * this table first"), and §2.11 names the graph's own bookkeeping the same way
 * ("no stylesheet rule may select one without adding it above first"). Reading
 * §2 whole must not quietly license either set, so they are subtracted here by
 * name -- which is what keeps both sentences true of the scan.
 *
 * **Six of §2.11's eight, and not the other two.** `data-key` and `data-view`
 * are listed in §2.11 because *the graph* emits them and no graph rule selects
 * them, but the same two names are §2's hooks elsewhere: §2.3's table and §2.4's
 * prose license `data-key` on a spend chart's points and rows and on the caps
 * panel's `li.spend-cap-repo` (`screens.css` selects it), and §2.1 licenses
 * `data-view` on `nav.console-views button.console-view` and §2.5 on
 * `section.spend-people`. The scan licenses by attribute **name**, not by name
 * and screen, so subtracting those two would refuse rules the design asked for;
 * §2.11's paragraph says which of its eight this list holds to. */
const NOT_A_LICENCE: readonly string[] = Object.freeze([
  'data-hidden',
  'data-skew',
  'data-run-id',
  'data-tab',
  'data-chip',
  'data-stream',
  'data-release',
  'data-word',
])

/** readDesign reads the design document, failing by name rather than with an
 * ENOENT from somewhere further along. */
function readDesign(): string {
  try {
    return readFileSync(DESIGN_DOCUMENT, 'utf8')
  } catch (error) {
    throw new Error(
      `the design this theme is written against cannot be read at ${DESIGN_DOCUMENT}: the ` +
        `selector scan's whole licence is §2 of that document (${String(error)})`,
    )
  }
}

/** readTheme reads one stylesheet of the theme directory. */
function readTheme(name: string): string {
  try {
    return readFileSync(join(CONSOLE_ROOT, 'src', 'renderer', 'theme', name), 'utf8')
  } catch (error) {
    throw new Error(`src/renderer/theme/${name} cannot be read (${String(error)})`)
  }
}

/** inventoryHooks is the hooks §2 names: every backticked token of the whole
 * section -- its tables and its prose, because §2.7 and §2.8 have no tables --
 * reduced to the class or the attribute it is, in name order. */
function inventoryHooks(): readonly string[] {
  const design = readDesign()
  const start = design.indexOf(INVENTORY_HEADING)
  const end = design.indexOf(NEXT_SECTION, start)
  if (start === -1 || end === -1) {
    throw new Error(`the design has no ${INVENTORY_HEADING} section: the scan would be vacuous`)
  }
  const section = design.slice(start, end)
  const hooks = new Set<string>()
  /** previous is the class hook the last token was, and the only thing a
   * following `-suffix` can be an abbreviation of. */
  let previous = ''
  for (const [, token] of section.matchAll(/`([^`\n]+)`/g)) {
    // `data-installation="chosen"` and `aria-current="true|false"` name the
    // attribute; the value it carries is a state, not a hook.
    const bare = (token ?? '').replace(/="[^"]*"$/, '')
    const qualified = ELEMENT_PREFIX.test(bare) ? bare.replace(ELEMENT_PREFIX, '') : bare
    if (CLASS_HOOK.test(qualified)) {
      previous = qualified
      if (!NOT_A_LICENCE.includes(qualified)) hooks.add(qualified)
      continue
    }
    if (SUFFIX.test(qualified) && previous !== '') {
      // Both readings §2 uses: the suffix replaces the hook's last segment, and
      // the suffix is appended to the hook whole.
      const replaced = previous.slice(0, previous.lastIndexOf('-') + 1)
      for (const suffix of qualified.split('/')) {
        for (const expanded of [replaced + suffix.slice(1), previous + suffix]) {
          if (HOOK.test(expanded) && !NOT_A_LICENCE.includes(expanded)) hooks.add(expanded)
        }
      }
      continue
    }
    previous = ''
    if (HOOK.test(qualified) && !NOT_A_LICENCE.includes(qualified)) hooks.add(qualified)
  }
  return [...hooks].sort()
}

/** selectorsIn is what one stylesheet's selectors name: the classes (`.name`)
 * and the attributes (`[name`), in name order, with the comments removed so the
 * file's own prose is not read as a selector. */
function selectorsIn(css: string): { readonly classes: readonly string[]; readonly attributes: readonly string[] } {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, '').replace(/url\([^)]*\)/g, 'url()')
  return {
    classes: [...new Set([...stripped.matchAll(/\.([a-z][a-z0-9-]*)/g)].map((match) => match[1] ?? ''))].sort(),
    attributes: [...new Set([...stripped.matchAll(/\[([a-z][a-z0-9-]*)/g)].map((match) => match[1] ?? ''))].sort(),
  }
}

/** RENDERER is the half of the console that puts elements in the DOM. The
 * cases below read it, not the design, because the design does not name the
 * tags: §2.8 says so itself -- "**The tags are the code's and they matter**: the
 * install command is a `code` and not a `pre`, and both leads are `span`s and
 * not `p`s, so a rule spelled `pre.unavailable-install-command` styles nothing
 * at all". */
const RENDERER: readonly string[] = Object.freeze([
  join(CONSOLE_ROOT, 'src', 'renderer'),
  // `src/shared/version.ts` draws one root of its own -- the version-skew
  // section -- and `base.css` selects it, so the half that draws is both
  // directories and not the renderer alone.
  join(CONSOLE_ROOT, 'src', 'shared'),
])

/** EMITTER is how a screen draws an element: `make` and `cell` and the two
 * value helpers each take the factory, then the tag, then the classes. */
const EMITTER = /\b(?:make|cell|makeNS|cellNS|drawn|drawnNS|textCell|numberCell)\(/g

/** CLASS_LITERAL is what a class argument looks like. A tag passed as a class
 * (`make(factory, 'li', 'li')`) is not one, and neither is a document value. */
const CLASS_LITERAL = /^[a-z][a-z0-9-]*$/

/** ELEMENT_QUALIFIED is a selector that names a tag as well as a class, which
 * is the only shape a wrong tag can break: `span.run-id` styles nothing if the
 * run's id is an `h1`. */
const ELEMENT_QUALIFIED = /(?:^|[\s,>+~({])([a-z][a-z0-9]*)\.([a-z][a-z0-9-]*)/g

/** sourceFiles is every TypeScript file under a directory, recursively. */
function sourceFiles(dir: string): readonly string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...sourceFiles(path))
    else if (entry.name.endsWith('.ts')) out.push(path)
  }
  return out
}

/** callArguments splits one call's arguments at its own top level, so a nested
 * call, array or object does not split it -- `cell(line, factory, 'span',
 * labelOf(entry), 'repo', 'projects-enrolled-repo')` is six arguments and not
 * eight. `open` is the index of the call's `(`. */
function callArguments(text: string, open: number): readonly string[] {
  const args: string[] = []
  let depth = 0
  let start = open + 1
  let quote: string | null = null
  for (let at = open; at < text.length; at += 1) {
    const character = text[at] ?? ''
    if (quote !== null) {
      if (character === '\\') at += 1
      else if (character === quote) quote = null
      continue
    }
    if (character === "'" || character === '"' || character === '`') quote = character
    else if (character === '(' || character === '[' || character === '{') depth += 1
    else if (character === ')' || character === ']' || character === '}') {
      depth -= 1
      if (depth === 0) {
        args.push(text.slice(start, at))
        return args
      }
    } else if (character === ',' && depth === 1) {
      args.push(text.slice(start, at))
      start = at + 1
    }
  }
  return args
}

/** literalsIn is every string literal an argument can be, or none: a plain
 * literal is itself, and an expression built only from literals and operators
 * (`kind === 'no-firebase' ? 'degraded' : 'console-unavailable'`, which is how
 * `renderDegraded` names its two roots) is all of them. An argument that reads a
 * variable names no tag or class this scan can know. */
function literalsIn(argument: string): readonly string[] {
  const literals = [...argument.matchAll(/'([a-z][a-z0-9-]*)'/g)].map((match) => match[1] ?? '')
  if (literals.length === 0) return []
  const bare = argument.replace(/'[^']*'/g, '')
  // `'degraded'` is itself; `kind === 'no-firebase' ? 'degraded' :
  // 'console-unavailable'` is both of its literals; and an argument that
  // computes a value (`labelOf(entry)`, `row.stage`) names neither.
  if (bare.includes('?') && bare.includes(':')) return literals
  return /[A-Za-z_]/.test(bare) ? [] : literals
}

/** enclosingHelper is the name of the function a `factory.create` call sits in,
 * for the helpers that stamp their own class from a parameter (`entry.ts`'s
 * `line()`), whose tag is only knowable from the call sites. */
function enclosingHelper(text: string, index: number): string | null {
  const openers = [...text.slice(0, index).matchAll(/(?:function\s+(\w+)|const\s+(\w+)\s*=\s*(?:\([^)]*\)|\w+)\s*(?::[^=]*)?=>)/g)]
  const last = openers[openers.length - 1]
  return last?.[1] ?? last?.[2] ?? null
}

/** classListsIn is the class lists one file builds before it draws: `const
 * classes = ['live-run']`, then `classes.push('live-run-queued')`, which is the
 * list a `make(factory, 'li', ...classes)` spreads. */
function classListsIn(text: string): ReadonlyMap<string, string> {
  const lists = new Map<string, string>()
  for (const match of text.matchAll(/const (\w+)(?:\s*:\s*[\w[\]<>| ]+)?\s*=\s*\[([^\]]*)\]/g)) {
    lists.set(match[1] ?? '', match[2] ?? '')
  }
  for (const match of text.matchAll(/(\w+)\.push\(([^)]*)\)/g)) {
    const name = match[1] ?? ''
    if (lists.has(name)) lists.set(name, `${lists.get(name) ?? ''},${match[2] ?? ''}`)
  }
  return lists
}

/** stringConstantsIn is the module's own named strings, because the chrome
 * stamps a constant rather than a literal (`RAIL_ROW_CLASS`). */
function stringConstantsIn(text: string): ReadonlyMap<string, string> {
  const constants = new Map<string, string>()
  for (const match of text.matchAll(/(?:export )?const (\w+)(?:\s*:\s*string)?\s*=\s*'([^']*)'/g)) {
    constants.set(match[1] ?? '', match[2] ?? '')
  }
  return constants
}

/** drawnPairs is every `tag.class` pair the renderer can put in the DOM: the
 * two ways a screen draws an element (an emitter call, whose classes are
 * literals or a spread list, and a raw `factory.create` with `setAttribute`). */
function drawnPairs(): ReadonlySet<string> {
  const pairs = new Set<string>()
  for (const dir of RENDERER) for (const file of sourceFiles(dir)) {
    const text = readFileSync(file, 'utf8')
    const lists = classListsIn(text)
    const constants = stringConstantsIn(text)
    for (const match of text.matchAll(EMITTER)) {
      const args = callArguments(text, match.index + (match[0]?.length ?? 0) - 1)
      const at = args.findIndex((argument) => argument.trim() === 'factory')
      if (at === -1) continue
      for (const tag of literalsIn(args[at + 1] ?? '')) {
        for (const argument of args.slice(at + 2)) {
          const trimmed = argument.trim()
          for (const className of literalsIn(trimmed)) if (CLASS_LITERAL.test(className)) pairs.add(`${tag}.${className}`)
          const spread = /^\.\.\.(\w+)$/.exec(trimmed)?.[1]
          if (spread === undefined) continue
          for (const one of (lists.get(spread) ?? '').matchAll(/'([a-z][a-z0-9-]*)'/g)) pairs.add(`${tag}.${one[1]}`)
        }
      }
    }
    // `factory.create('p')` followed by `setAttribute('class', …)`: the shape
    // `entry.ts` draws through. When the class is a parameter, the tag is paired
    // with what the helper's own call sites pass it.
    for (const match of text.matchAll(/factory\.create(?:NS)?\('([a-z][a-z0-9]*)'\)/g)) {
      const tag = match[1] ?? ''
      const after = text.slice(match.index, match.index + 1200)
      for (const stamp of after.matchAll(/setAttribute\('class',\s*(?:'([^']*)'|(\w+))\)/g)) {
        const literal = stamp[1] ?? constants.get(stamp[2] ?? '')
        if (literal !== undefined) {
          for (const name of literal.split(/\s+/).filter(Boolean)) pairs.add(`${tag}.${name}`)
          continue
        }
        const helper = enclosingHelper(text, match.index)
        if (helper === null) continue
        for (const call of text.matchAll(new RegExp(`\\b${helper}\\('([a-z][a-z0-9-]*)'`, 'g'))) {
          pairs.add(`${tag}.${call[1] ?? ''}`)
        }
      }
    }
    // `disabledWrite(factory, CLASS, label)` (`src/renderer/writes.ts`) draws a
    // `button` carrying the class it is handed: the one helper every write the
    // console draws and does not perform goes through.
    for (const match of text.matchAll(/disabledWrite\(\s*\w+,\s*(?:'([^']*)'|(\w+))/g)) {
      const literal = match[1] ?? constants.get(match[2] ?? '')
      if (literal !== undefined) pairs.add(`button.${literal}`)
    }
  }
  return pairs
}

/** Binding is one name a file binds to an element it drew: the name, what it
 * holds, and where in the file it was bound. */
interface Binding {
  readonly at: number
  readonly name: string
  readonly tag: string
  readonly classes: readonly string[]
  /** `create` is the raw `factory.create` shape, which is the one `sole` counts;
   * `createNS` is its SVG twin and `emitter` is a screen's own. */
  readonly shape: 'create' | 'createNS' | 'emitter'
}

/** elementBindings is every name a file binds to an element it drew, in source
 * order. It is the **one** reading of the two shapes the renderer draws
 * through, and it has three readers: `drawnAttributes` turns each binding into
 * an event, `sole` counts the raw ones, and `drawnOnce` counts both. */
function elementBindings(text: string): readonly Binding[] {
  const lists = classListsIn(text)
  const found: Binding[] = []
  // The optional wrapper is `literal(cell(element, factory, 'span',
  // 'degraded-mode'), 'polling')`, which binds the span the same way.
  for (const match of text.matchAll(/(?:const|let)\s+(\w+)\s*=\s*(?:\w+\(\s*)?(?:make|cell|makeNS|cellNS|drawn|drawnNS|textCell|numberCell)\(/g)) {
    const args = callArguments(text, match.index + (match[0]?.length ?? 0) - 1)
    const at = args.findIndex((argument) => argument.trim() === 'factory')
    if (at === -1) continue
    const tag = literalsIn(args[at + 1] ?? '')[0]
    if (tag === undefined) continue
    const classes: string[] = []
    for (const argument of args.slice(at + 2)) {
      const trimmed = argument.trim()
      for (const className of literalsIn(trimmed)) if (CLASS_LITERAL.test(className)) classes.push(className)
      const spread = /^\.\.\.(\w+)$/.exec(trimmed)?.[1]
      if (spread === undefined) continue
      for (const one of (lists.get(spread) ?? '').matchAll(/'([a-z][a-z0-9-]*)'/g)) classes.push(one[1] ?? '')
    }
    found.push({ at: match.index, name: match[1] ?? '', tag, classes, shape: 'emitter' })
  }
  for (const match of text.matchAll(/(?:const|let)\s+(\w+)\s*=\s*factory\.create\('([a-z][a-z0-9]*)'\)/g)) {
    found.push({ at: match.index, name: match[1] ?? '', tag: match[2] ?? '', classes: [], shape: 'create' })
  }
  for (const match of text.matchAll(/(?:const|let)\s+(\w+)\s*=\s*factory\.createNS\('([a-z][a-z0-9]*)'\)/g)) {
    found.push({ at: match.index, name: match[1] ?? '', tag: match[2] ?? '', classes: [], shape: 'createNS' })
  }
  return found.sort((left, right) => left.at - right.at)
}

/** drawnOnce is the element a file draws under one name **exactly once**,
 * whichever shape drew it. It answers what a frame's field holds: `spend.ts`
 * draws `const caps = cell(root, factory, 'section', 'spend-caps')` and calls no
 * other `caps` its own, so `frame.caps` is that section. */
function drawnOnce(bindings: readonly Binding[]): ReadonlyMap<string, { tag: string; classes: string[] }> {
  const counts = new Map<string, number>()
  const drawn = new Map<string, { tag: string; classes: string[] }>()
  for (const binding of bindings) {
    counts.set(binding.name, (counts.get(binding.name) ?? 0) + 1)
    drawn.set(binding.name, { tag: binding.tag, classes: [...binding.classes] })
  }
  return new Map([...drawn].filter(([name]) => counts.get(name) === 1))
}

/** fillerHosts is the element each function's parameter is filled with, keyed
 * `function\u0000parameter`: the shape the owner's rule of 2026-10-31 gives the
 * Spend screen, where a frame draws one element per read and the entry fills
 * that element when the read answers. The walk cannot run a call, so the
 * parameter is read from the call sites' own arguments, and an argument naming
 * a frame's part (`frame.caps`) from `drawnOnce`. A call whose argument resolves
 * to nothing (`entry.ts`'s `drawn.caps`, in a file that draws no `caps`) binds
 * nothing, so the answer is a union over the call sites rather than a decision
 * about any one of them -- and an argument the file cannot read is skipped
 * rather than guessed at. */
function fillerHosts(
  text: string,
  once: ReadonlyMap<string, { tag: string; classes: string[] }>,
): ReadonlyMap<string, { tag: string; classes: string[] }> {
  const hosts = new Map<string, { tag: string; classes: string[] }>()
  for (const match of text.matchAll(/(?:export\s+)?function\s+(\w+)\s*\(([^)]*)\)/g)) {
    const name = match[1] ?? ''
    const parameters = (match[2] ?? '').split(',').map((one) => /\w+/.exec(one)?.[0] ?? '')
    for (const call of text.matchAll(new RegExp(`\\b${name}\\(`, 'g'))) {
      const args = callArguments(text, (call.index ?? 0) + (call[0]?.length ?? 0) - 1)
      parameters.forEach((parameter, at) => {
        const part = /^\w+\.(\w+)$/.exec((args[at] ?? '').trim())?.[1]
        if (part === undefined) return
        const host = once.get(part)
        if (host !== undefined) hosts.set(`${name}\u0000${parameter}`, host)
      })
    }
  }
  return hosts
}

/** drawnAttributes is which attributes the console sets on which elements, keyed
 * `tag.class`. It is the second half of §7.4's "a rule that styles a hook
 * nothing emits": a rule may name a class the code draws and an attribute §2
 * lists, and still select nothing, because **the attribute is on a different
 * element** -- `span.spend-partial[data-partial='true']` is that shape, and the
 * design's own §2.4 puts `data-partial` on the chart's key, the row and the
 * totals line rather than on the marker span inside them.
 *
 * The reading is a walk in source order, because the code reuses its names: a
 * screen's `element` is a `section.spend-caps` in `fillSpendCaps()` and a
 * `div.spend-chart` in `chart()`, so each `const name = <emitter>` rebinds, a
 * `setAttribute('class', …)` on the name adds to its classes, and an attribute
 * write on the name is recorded against every class it then carries.
 *
 * **The second shape is a loop over a container the renderer filled with
 * elements it drew** (Task 6.5): the rail's `select` walks `rows` and the
 * navigation's `markButtons` walks `buttons`, and both write the marks on the
 * loop's own variable rather than on the name they were drawn under. Both loops
 * are also written *above* the loop that fills the container -- `select` is a
 * closure the drawing below it calls, and `markButtons` is called once the
 * buttons exist -- so the walk has not bound the element they walk when it
 * reaches them. The shape is therefore read from the container's own fill
 * (`rows.push(row)`, `buttons.set(entry.id, button)`), and the element is
 * resolved from the walk where the walk has it and from the file where the file
 * creates that name exactly once -- which is the honest limit of a scan that
 * cannot run the code: a name the file reuses is one only the order can read.
 *
 * **The third shape is a filler, and it is the owner's rule of 2026-10-31 in the
 * code's own terms.** Spend is drawn as a frame with named parts -- one element
 * per read, each with its own busy slot -- and the entry **fills** a part when
 * that part's read answers: `fillSpendCaps(factory, caps, element)` writes
 * `element.setAttribute('data-available', …)`, and `element` is not the
 * filler's to draw. It is the frame's, `renderSpendFrame`'s `caps`, drawn in
 * another function; the walk cannot run a call, so it resolves the parameter
 * from the arguments the call sites pass (`fillerHosts`), and an argument that
 * names a frame's part (`frame.caps`, `drawn.caps`) from the element the file
 * draws under that name exactly once (`drawnOnce`). A screen that adopts the
 * helper draws its parts the same way, so the shape is read rather than
 * excepted. */
function drawnAttributes(): ReadonlyMap<string, ReadonlySet<string>> {
  const attributes = new Map<string, Set<string>>()
  for (const dir of RENDERER) for (const file of sourceFiles(dir)) {
    const text = readFileSync(file, 'utf8')
    const lists = classListsIn(text)
    const constants = stringConstantsIn(text)
    const bindings = new Map<string, { tag: string; classes: string[] }>()
    const events: { readonly at: number; readonly apply: () => void }[] = []
    // holds is which element each container was filled with, and once is the one
    // `factory.create` binding of a name the file creates only once.
    const holds = new Map<string, string>()
    for (const match of text.matchAll(/(\w+)\.push\((\w+)\)/g)) holds.set(match[1] ?? '', match[2] ?? '')
    for (const match of text.matchAll(/(\w+)\.set\([^,()]*,\s*(\w+)\)/g)) holds.set(match[1] ?? '', match[2] ?? '')
    const created = new Map<string, { tag: string; classes: string[] }>()
    const times = new Map<string, number>()
    // `elementBindings` is read once and used twice: `times` and `created` are the
    // file's whole answer and are read before the walk runs (`sole` is a container
    // loop's fallback, and a loop written above the drawing it walks has to see a
    // name the file creates below it), and the same bindings become the walk's
    // events further down.
    const drawn = elementBindings(text)
    for (const binding of drawn) {
      if (binding.shape !== 'create') continue
      times.set(binding.name, (times.get(binding.name) ?? 0) + 1)
      created.set(binding.name, { tag: binding.tag, classes: [] })
    }
    for (const match of text.matchAll(/(\w+)\.setAttribute\('class',\s*(?:'([^']*)'|(\w+))\)/g)) {
      const shape = created.get(match[1] ?? '')
      if (shape === undefined) continue
      for (const one of (match[2] ?? constants.get(match[3] ?? '') ?? '').split(/\s+/).filter(Boolean)) {
        if (!shape.classes.includes(one)) shape.classes.push(one)
      }
    }
    const sole = (name: string): { tag: string; classes: string[] } | undefined =>
      times.get(name) === 1 ? created.get(name) : undefined
    const hosts = fillerHosts(text, drawnOnce(drawn))
    for (const binding of drawn) {
      events.push({
        at: binding.at,
        apply: () => bindings.set(binding.name, { tag: binding.tag, classes: [...binding.classes] }),
      })
    }
    for (const match of text.matchAll(/(\w+)\.setAttribute\('class',\s*(?:'([^']*)'|(\w+))\)/g)) {
      const name = match[1] ?? ''
      const stamped = (match[2] ?? constants.get(match[3] ?? '') ?? '').split(/\s+/).filter(Boolean)
      if (stamped.length === 0) continue
      events.push({
        at: match.index,
        apply: () => {
          const bound = bindings.get(name)
          if (bound === undefined) return
          for (const one of stamped) bound.classes.push(one)
        },
      })
    }
    // The loop over a filled container: `for (const each of rows)` and
    // `for (const [key, button] of buttons)` both bind their variable to the
    // element the container holds.
    for (const match of text.matchAll(/for\s*\(\s*const\s+(?:\[[^,\]]+,\s*)?(\w+)\s*\]?\s+of\s+(\w+)\s*\)/g)) {
      const name = match[1] ?? ''
      const element = holds.get(match[2] ?? '')
      if (element === undefined) continue
      events.push({
        at: match.index,
        apply: () => {
          const bound = bindings.get(element) ?? sole(element)
          if (bound === undefined) return
          bindings.set(name, { tag: bound.tag, classes: [...bound.classes] })
        },
      })
    }
    for (const match of text.matchAll(/(\w+)\.setAttribute\('([a-z][a-z0-9-]*)'/g)) {
      const name = match[1] ?? ''
      const attribute = match[2] ?? ''
      if (attribute === 'class') continue
      // A write on the enclosing function's own parameter is the filler's, and
      // the parameter's element is the one its call sites passed -- the section
      // the frame drew, not whatever the file last bound that name to.
      const enclosing = enclosingHelper(text, match.index)
      const host = enclosing === null ? undefined : hosts.get(`${enclosing}\u0000${name}`)
      events.push({
        at: match.index,
        apply: () => {
          const bound = host ?? bindings.get(name)
          if (bound === undefined) return
          for (const className of new Set(bound.classes)) {
            const key = `${bound.tag}.${className}`
            const carried = attributes.get(key) ?? new Set<string>()
            carried.add(attribute)
            attributes.set(key, carried)
          }
        },
      })
    }
    for (const event of events.sort((left, right) => left.at - right.at)) event.apply()
  }
  return attributes
}

/** AHEAD_OF_THE_CODE is the attribute claims that are deliberately written
 * before the code that emits them, keyed `tag.class[attribute]`.
 *
 * **Task 6.5 emptied it.** It held one, recorded in `base.css`'s header: the
 * selected rail row's `button.console-rail-row[aria-selected='true']` is the
 * design's own rule (§4.1: "the attribute is the machine-readable mark and the
 * border is the drawn one"), §2.1 handed `role` and `aria-selected` to Task 6.5
 * while the border was Task 6.3's, so for one task the rule was there before the
 * attribute was. The tablist behaviour arrived with Task 6.5 and the scan reads
 * it (the container-loop shape in `drawnAttributes`), so the exemption is gone
 * rather than stale: a **new** entry here would be a rule that styles nothing
 * the code emits, and the list is short enough to read. */
const AHEAD_OF_THE_CODE: readonly string[] = Object.freeze([])

/** QualifiedAttribute is one compound selector's claim that an element carrying
 * a class carries an attribute. */
interface QualifiedAttribute {
  readonly className: string
  /** The tag the selector names, or `null` when it names none. */
  readonly tag: string | null
  readonly attribute: string
  /** The compound as it was written, for the failure message. */
  readonly written: string
}

/** compoundSelectors splits one selector into its compounds, so a claim about a
 * descendant is not read as a claim about its ancestor: in
 * `aside.console-rail[data-collapsed='true'] span.console-rail-name`, the
 * attribute is the aside's and the class it is checked against is
 * `console-rail`. */
function compoundSelectors(selector: string): readonly string[] {
  const compounds: string[] = []
  let depth = 0
  let start = 0
  for (let at = 0; at < selector.length; at += 1) {
    const character = selector[at] ?? ''
    if (character === '[' || character === '(') depth += 1
    else if (character === ']' || character === ')') depth -= 1
    else if (depth === 0 && /[\s>+~]/.test(character)) {
      compounds.push(selector.slice(start, at))
      while (at + 1 < selector.length && /[\s>+~]/.test(selector[at + 1] ?? '')) at += 1
      start = at + 1
    }
  }
  compounds.push(selector.slice(start))
  return compounds.filter((one) => one !== '')
}

/** selectorList is every selector a stylesheet writes, comments stripped first
 * (so the file's own prose claims nothing) and each rule's list split at its
 * own commas. */
function selectorList(css: string): readonly string[] {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, '').replace(/url\([^)]*\)/g, 'url()')
  const selectors: string[] = []
  for (const rule of stripped.matchAll(/([^{}]+)\{/g)) {
    let depth = 0
    let start = 0
    const text = rule[1] ?? ''
    for (let at = 0; at < text.length; at += 1) {
      const character = text[at] ?? ''
      if (character === '[' || character === '(') depth += 1
      else if (character === ']' || character === ')') depth -= 1
      else if (character === ',' && depth === 0) {
        selectors.push(text.slice(start, at).trim())
        start = at + 1
      }
    }
    selectors.push(text.slice(start).trim())
  }
  return selectors.filter((one) => one !== '')
}

/** codedAttributes is every claim the stylesheets make about a class and an
 * attribute together. A pseudo-class that *contains* a selector (`:not`,
 * `:is`, `:where`, `:has`) is dropped first: `:not([data-state])` claims the
 * attribute is **absent**, which is the opposite claim. */
function codedAttributes(css: string): readonly QualifiedAttribute[] {
  const claims: QualifiedAttribute[] = []
  for (const selector of selectorList(css)) {
    for (const compound of compoundSelectors(selector)) {
      const bare = compound.replace(/:(?:not|is|where|has)\([^()]*\)/g, '')
      const className = /\.([a-z][a-z0-9-]*)/.exec(bare)?.[1]
      if (className === undefined) continue
      const tag = /^([a-z][a-z0-9]*)\./.exec(bare)?.[1] ?? null
      for (const attribute of bare.matchAll(/\[([a-z][a-z0-9-]*)/g)) {
        claims.push({ className, tag, attribute: attribute[1] ?? '', written: compound })
      }
    }
  }
  return claims
}

/** elementQualified is every `tag.class` pair the stylesheets select. */
function elementQualified(css: string): readonly string[] {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, '').replace(/url\([^)]*\)/g, 'url()')
  return [...new Set([...stripped.matchAll(ELEMENT_QUALIFIED)].map((match) => `${match[1] ?? ''}.${match[2] ?? ''}`))].sort()
}

describe('the theme’s selectors against the design’s §2 inventory', () => {
  it('reads the inventory out of the design, and it names the rail’s fourteen hooks', () => {
    // The non-vacuity guard for every case below: an extraction that found
    // nothing, or that read the wrong section, would pass a scan against an
    // empty list. The rail's twelve are the hooks this task adds, and §2.1 is
    // where they are listed.
    const hooks = inventoryHooks()
    expect(hooks.length, 'the §2 inventory read empty').toBeGreaterThan(20)
    for (const hook of RAIL_HOOKS) expect(hooks, `§2.1 does not name ${hook}`).toContain(hook)
    // And the two `data-*` hooks this task's rules select: a rule that wants one
    // adds it to the table **first** (design §2's licence rule).
    expect(hooks).toContain('data-state')
  })

  it('reads §2 whole, so the screens whose hooks are prose are licensed and the refused hooks are not', () => {
    // The case above is the count and the chrome's; this one is the reading
    // Task 6.4 widened, and each of the four hooks below is one a rule in
    // `screens.css` selects. Two of them are **prose-only**: §2.7 and §2.8 have
    // no tables, so a scan narrowed back to the tables would refuse
    // `section.degraded` and `div.unavailable-body` while §2 names both, and
    // §2.10 names `degraded-num` -- the hook Task 6.4 adds to the degraded
    // screen's `numberCell` -- in the sentence that hands it to this task.
    // `data-panel` is the fourth shape: §2.5's `data-*` list is a prose line
    // under its table rather than a row of it.
    const hooks = inventoryHooks()
    for (const hook of ['degraded', 'unavailable-body', 'degraded-num', 'data-panel', 'data-kind']) {
      expect(hooks, `§2 does not name ${hook}, and a rule selects it`).toContain(hook)
    }
    // And the widening is not a loosening: the attributes §2 names in order to
    // refuse them are still refused, because a rule that wants one adds it to §2
    // first (design §2's licence paragraph, and §2.11's for the graph's).
    expect(hooks, 'the scan licensed the two hooks §2 refuses by name').not.toContain('data-hidden')
    expect(hooks).not.toContain('data-skew')
    // §2.11 names eight attributes the graph emits and no rule needs, and the
    // six that are not §2 hooks elsewhere are refused here too: §2.11 says "no
    // stylesheet rule may select one without adding it above first", and a
    // reading of §2 whole would otherwise license all eight.
    for (const attribute of ['data-run-id', 'data-tab', 'data-chip', 'data-stream', 'data-release', 'data-word']) {
      expect(hooks, `the scan licensed ${attribute}, which §2.11 says no rule may select`).not.toContain(attribute)
    }
    // The other two of §2.11's eight **stay** licensed, and that is not an
    // oversight: the scan licenses by name, and both names are §2's hooks on
    // other screens -- a spend chart's and a caps row's `data-key` (§2.3, §2.4,
    // which `screens.css` selects) and the navigation's and the people panel's
    // `data-view` (§2.1, §2.5). Refusing them here would refuse those rules.
    expect(hooks, '§2 licenses data-key for the spend screen').toContain('data-key')
    expect(hooks, '§2 licenses data-view for the navigation').toContain('data-view')

    // The `-suffix` shorthand is expanded, and only where it can be read: the
    // degraded screen's run row and feed age are hooks §2 names in shorthand,
    // and §2.10's prose `-num` -- the one suffix in the section that follows a
    // signature rather than a hook -- licenses nothing.
    for (const hook of ['degraded-since-word', 'degraded-since-at', 'degraded-since-age', 'degraded-run-repo', 'degraded-run-created']) {
      expect(hooks, `§2 names ${hook} in shorthand and the expansion missed it`).toContain(hook)
    }
    expect(hooks, 'the scan expanded a suffix that follows no hook').not.toContain('data-segments-num')
  })

  it('selects no class §2 does not name, in every stylesheet this task writes', () => {
    const hooks = inventoryHooks()
    const unknown: string[] = []
    for (const name of SCANNED) {
      const { classes } = selectorsIn(readTheme(name))
      // The non-vacuity guard, per file: a file with no class selector would
      // pass this case by selecting nothing at all.
      expect(classes.length, `${name} selects no class at all`).toBeGreaterThan(0)
      for (const className of classes) {
        if (!hooks.includes(className)) unknown.push(`${name}: .${className}`)
      }
    }
    expect(unknown, 'a rule selects a hook the design’s §2 does not name').toEqual([])
  })

  it('selects no attribute §2 does not name', () => {
    // The same licence, for the `data-*` and `aria-*` hooks a rule selects
    // rather than for its classes. The attributes the screens emit beyond §2's
    // tables are real and are deliberately unlisted (design §2 names
    // `data-hidden` and `data-skew` as the two examples, and §2.11 the graph's
    // six) -- so this case is what makes a rule that suddenly wants one add it
    // to the table first.
    const hooks = inventoryHooks()
    const unknown: string[] = []
    for (const name of SCANNED) {
      for (const attribute of selectorsIn(readTheme(name)).attributes) {
        if (!hooks.includes(attribute)) unknown.push(`${name}: [${attribute}]`)
      }
    }
    expect(unknown, 'a rule selects an attribute the design’s §2 does not name').toEqual([])
  })

  it('selects the rail’s fourteen hooks and no fifteenth', () => {
    // "Fourteen hooks on fifteen elements" (design §2.1 and §4.1): the foot row
    // and the button inside it share `console-rail-add`. A fifteenth
    // `console-rail-*` class in the stylesheet would be a hook either the entry
    // does not draw or §2.1 does not list, and both are the same defect.
    const rail = selectorsIn(readTheme('base.css')).classes.filter((name) => name.startsWith('console-rail'))
    expect(rail).toEqual([...RAIL_HOOKS].sort())
  })

  it('qualifies every selector with the tag the renderer actually draws', () => {
    // The case above reads the classes a rule selects; this one reads the tags
    // it qualifies them with, because §2's licence is about hooks and says
    // nothing about the element in front of one -- and §2.8 is where the design
    // warns that the tags are the code's and a wrong one styles nothing at all.
    // `span.run-id` is the shape this catches: the run's id is an `h1`, so a
    // rule that spelled it `span` would be a rule no element ever matches, and
    // the selector scan above would still pass it, because `run-id` is a hook
    // §2 names.
    // `drawnPairs` reads the shapes the console draws through -- four emitter
    // helpers, the raw `create`/`setAttribute` pair with its three ways of
    // naming a class, and the two roots `degraded.ts` chooses between. A
    // **new** way of drawing an element is a shape this scan cannot read, and it
    // shows up here as a selector it cannot match rather than as a rule that
    // silently styles nothing; the fix is to teach the scan the shape.
    const drawn = drawnPairs()
    expect(drawn.size, 'the renderer read as drawing nothing').toBeGreaterThan(200)
    const unknown: string[] = []
    for (const name of SCANNED) {
      for (const pair of elementQualified(readTheme(name))) {
        if (!drawn.has(pair)) unknown.push(`${name}: ${pair}`)
      }
    }
    expect(unknown, 'a rule qualifies a hook with a tag nothing draws').toEqual([])
  })

  it('carries every attribute it qualifies a hook with on the element that has the class', () => {
    // The other half of §7.4's "a rule that styles a hook nothing emits", and
    // the one the class scan and the attribute scan between them cannot see: a
    // rule may name a class the code draws **and** an attribute §2 lists, and
    // still match nothing, because the attribute is on a different element.
    // `span.spend-partial[data-partial='true']` is that shape -- §2.4 puts
    // `data-partial` on the point, the row and the totals line, and the marker
    // span inside them carries the text `(partial)` and no attribute at all.
    const drawn = drawnAttributes()
    expect(drawn.size, 'the renderer read as setting no attribute').toBeGreaterThan(20)
    const unknown: string[] = []
    for (const name of SCANNED) {
      for (const claim of codedAttributes(readTheme(name))) {
        // A rule that names no tag (`[data-killed='true']`, a bare class) is a
        // claim about a class, so any element carrying it may be the one meant.
        const keys =
          claim.tag === null
            ? [...drawn.keys()].filter((key) => key.endsWith(`.${claim.className}`))
            : [`${claim.tag}.${claim.className}`]
        const key = `${claim.tag ?? ''}.${claim.className}[${claim.attribute}]`
        if (AHEAD_OF_THE_CODE.includes(key)) continue
        if (keys.some((key) => drawn.get(key)?.has(claim.attribute) === true)) continue
        unknown.push(`${name}: ${claim.written}`)
      }
    }
    expect(unknown, 'a rule qualifies a hook with an attribute the element does not carry').toEqual([])
  })

  it('scans the stylesheets the bundler serves, so a layer cannot be added and skipped', () => {
    // The guard on the guard: `SCANNED` is a list this file owns, and a task
    // that added a theme file without adding it here would leave that file's
    // selectors unchecked. Every stylesheet the bundler concatenates is either
    // scanned or is one of the two layers that hold no selector at all.
    const unscanned = THEME_STYLESHEETS.filter((name) => !SCANNED.includes(name))
    expect(unscanned).toEqual(['tokens.css', 'palette.css'])
  })
})
