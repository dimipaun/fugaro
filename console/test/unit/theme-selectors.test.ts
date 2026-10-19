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
// `NOT_A_LICENCE` keeps out the two `data-*` attributes the design names
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

import { readFileSync } from 'node:fs'
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
const SCANNED: readonly string[] = Object.freeze(['base.css', 'screens.css'])

/** RAIL_HOOKS is the rail's twelve classes, which §2.1 names and §4.1 draws as
 * thirteen elements. They are listed here because the count is the claim: the
 * stylesheet selects these twelve and no thirteenth, and a case below says so. */
const RAIL_HOOKS: readonly string[] = Object.freeze([
  'console-rail',
  'console-rail-head',
  'console-rail-title',
  'console-rail-collapse',
  'console-rail-list',
  'console-rail-row',
  'console-rail-name',
  'console-rail-project',
  'console-rail-state',
  'console-rail-spend',
  'console-rail-add',
  'console-rail-hint',
])

/** ELEMENT_PREFIX is the element names the design's tables qualify a class with
 * (`div.console-rail-head`, `header.degraded-header`). Stripping one is what
 * turns a token into the class a rule would select; a token with any other
 * prefix is not a hook. */
const ELEMENT_PREFIX = /^(?:a|aside|button|code|div|dl|figure|form|h1|h2|h3|header|input|label|li|nav|ol|p|pre|section|span|table|tbody|td|th|tr|ul)\./

/** HOOK is what a token has to look like to be a hook rather than a word of
 * prose. A class hook carries one of the prefixes §2's tables and prose are
 * built from -- a screen's own (`live-`), the chrome's (`console-`), the
 * installation line's (`installation-`) or a screen root spelled bare -- and an
 * attribute hook is a `data-*` or an `aria-*`. Everything else the section
 * mentions (`class`, `title`, `role`, `putReading`, `UNAVAILABLE_MARK`,
 * `path:line`) names no hook a stylesheet could select. */
const HOOK = /^(?:(?:live|spend|projects|run|degraded|console|unavailable|installation)(?:-[a-z0-9-]+)?|(?:data|aria)-[a-z0-9-]+)$/

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
const CLASS_HOOK = /^(?:live|spend|projects|run|degraded|console|unavailable|installation)-[a-z0-9-]+$/

/** SUFFIX is one `-suffix`, or a run of them joined by `/` as §2.7's
 * `-word`/`-at`/`-age` is written. */
const SUFFIX = /^-[a-z0-9-]+(?:\/-[a-z0-9-]+)*$/

/** NOT_A_LICENCE is the two attributes §2 names **in order to refuse them**.
 * §2's licence paragraph is explicit -- "`data-hidden` (every view the mode
 * withholds) and `data-skew` (the compatibility handshake), both stamped by
 * `console/src/shared/version.ts` ... they are emitted, no rule needs them, and a
 * rule that suddenly wants one adds it to this table first" -- so reading §2
 * whole must not quietly license them. They are subtracted here by name, which
 * is what keeps §2's sentence true of the scan. */
const NOT_A_LICENCE: readonly string[] = Object.freeze(['data-hidden', 'data-skew'])

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
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, '')
  return {
    classes: [...new Set([...stripped.matchAll(/\.([a-z][a-z0-9-]*)/g)].map((match) => match[1] ?? ''))].sort(),
    attributes: [...new Set([...stripped.matchAll(/\[([a-z][a-z0-9-]*)/g)].map((match) => match[1] ?? ''))].sort(),
  }
}

describe('the theme’s selectors against the design’s §2 inventory', () => {
  it('reads the inventory out of the design, and it names the rail’s twelve hooks', () => {
    // The non-vacuity guard for every case below: an extraction that found
    // nothing, or that read the wrong section, would pass a scan against an
    // empty list. The rail's twelve are the hooks this task adds, and §2.1 is
    // where they are listed.
    const hooks = inventoryHooks()
    expect(hooks.length, 'the §2 inventory read empty').toBeGreaterThan(20)
    for (const hook of RAIL_HOOKS) expect(hooks, `§2.1 does not name ${hook}`).toContain(hook)
    // And the two `data-*` hooks this task's rules select: a rule that wants one
    // adds it to the table **first** (design §2's licence rule).
    expect(hooks).toContain('data-collapsed')
    expect(hooks).toContain('data-state')
  })

  it('reads §2 whole, so the screens whose hooks are prose are licensed and the two refused hooks are not', () => {
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
    // And the widening is not a loosening: the two attributes §2 names in order
    // to refuse them are still refused, because a rule that wants one adds it to
    // §2 first (design §2's licence paragraph).
    expect(hooks, 'the scan licensed the two hooks §2 refuses by name').not.toContain('data-hidden')
    expect(hooks).not.toContain('data-skew')

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
    // `data-hidden` and `data-skew` as the two examples) -- so this case is what
    // makes a rule that suddenly wants one add it to the table first.
    const hooks = inventoryHooks()
    const unknown: string[] = []
    for (const name of SCANNED) {
      for (const attribute of selectorsIn(readTheme(name)).attributes) {
        if (!hooks.includes(attribute)) unknown.push(`${name}: [${attribute}]`)
      }
    }
    expect(unknown, 'a rule selects an attribute the design’s §2 does not name').toEqual([])
  })

  it('selects the rail’s twelve hooks and no thirteenth', () => {
    // "Twelve hooks on thirteen elements" (design §2.1 and §4.1): the foot row
    // and the button inside it share `console-rail-add`. A thirteenth
    // `console-rail-*` class in the stylesheet would be a hook either the entry
    // does not draw or §2.1 does not list, and both are the same defect.
    const rail = selectorsIn(readTheme('base.css')).classes.filter((name) => name.startsWith('console-rail'))
    expect(rail).toEqual([...RAIL_HOOKS].sort())
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
