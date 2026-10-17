// website/test/home.mjs — the hero, the terminal and the architecture diagram (website
// plan, Task W1.4).
//
// The two pages this task writes are the first pages the site has, and they are the pages
// where a marketing site is most likely to say something the product cannot back. So every
// check below is the mechanical form of one of the plan's rules, and none of them is a
// check on the component's own text:
//
//   1. **The tagline is README.md's line, byte for byte** (F48, D102). It is read here from
//      the README, not from the component, so the two cannot drift: the hero that types the
//      tagline in by hand is the defect this check removes.
//   2. **The hero's sentences and commands are the repository's.** Its lead sentence has to
//      be a sentence of README.md, and its install commands have to be the commands
//      `website/data/install.yaml` carries for the paths it renders — never typed into the
//      component (Global Constraint 6, W1.4 step 1).
//   3. **The terminal types a real invocation.** The command is README.md's own example and
//      `fugaro run` is a command the generated CLI reference documents (W0.4's file), and
//      the animation holds its last frame so a reader whose system asks for less motion gets
//      the finished command rather than a half-typed one (D105, W1.4 step 2).
//   4. **The diagram carries the spec's five stage names and no sixth**, and every caption
//      on it is a line of README.md's `## How it works` or of `docs/design/v1.md` — the two
//      names the README does not use are asserted to be labels only (W1.4 step 3).
//   5. **Neither page names a claim the product cannot back**: no console download, no
//      Windows/WSL support, no "~2 h", no ballpark ranges (F50, D102, W1.4 step 4). The
//      video slot is checked against `site.yaml`'s own flag rather than against today's
//      value, so W2.4 turns the video on without meeting a red test here (D107).
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file:
// the check is a statement about the artifact a reader receives, and every fact it holds a
// page to is read from the repository rather than from the page's own sources.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)
const DIST = path.join(SITE, 'dist')

/** The bytes of a file in the checkout, for a page to be held against. */
function repoFile(relative) {
  const file = path.join(REPO, relative)
  assert.ok(existsSync(file), `${relative} is missing: this check reads the repository, not the build`)
  return readFileSync(file, 'utf8')
}

/** A built page, by route: `''` is `/`, `'how-it-works'` is `/how-it-works/`. */
function builtPage(route) {
  const file = path.join(DIST, ...(route === '' ? [] : [route]), 'index.html')
  assert.ok(
    existsSync(file),
    `the build produced no ${route === '' ? '/' : `/${route}/`} page: ${path.relative(REPO, file)} is not there, and the built page is what this check reads (W1.4)`,
  )
  return readFileSync(file, 'utf8')
}

/**
 * The stylesheet a built page carries. Astro inlines a small stylesheet into the page and
 * links a larger one, so both are read: the check is about the CSS the page ships, and
 * which of the two shapes it arrived in is the bundler's business.
 */
function builtCSS(page) {
  const inline = [...page.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1])
  const linked = [...page.matchAll(/<link\b[^>]*>/g)]
    .map((tag) => /href="([^"]+)"/.exec(tag[0]))
    .filter(Boolean)
    .map((m) => path.join(DIST, m[1].replace(/^\//, '').split('?')[0]))
    .filter((file) => file.endsWith('.css') && existsSync(file))
    .map((file) => readFileSync(file, 'utf8'))
  return [...inline, ...linked].join('\n')
}

/** An element's text: its tags removed, and the entities Astro writes decoded. */
function text(html) {
  return html
    .replace(/<[^>]*>/g, '')
    .replace(/&quot;/g, '"')
    .replace(/&#39;|&apos;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ')
    .trim()
}

/** The text of the first element carrying `class="…"`, or null when there is none. */
function classText(html, className) {
  const m = new RegExp(`<([a-z0-9]+)[^>]*class="${className}"[^>]*>([\\s\\S]*?)</\\1>`).exec(html)
  return m ? text(m[2]) : null
}

/**
 * A string as a document writes it rather than as a page renders it: markdown emphasis and
 * code spans are markers and not text, and a document's lines wrap where a page's do not.
 * Applied to both sides of every comparison below, so a caption and its source are compared
 * as the reader reads them.
 */
function normalize(markdown) {
  return markdown.replace(/[`*]/g, '').replace(/\s+/g, ' ').trim()
}

/** README.md's tagline: the line under its `# Fugaro` title, emphasis stripped (F48). */
function readmeTagline() {
  const lines = repoFile('README.md').split('\n')
  const title = lines.findIndex((line) => line.trim() === '# Fugaro')
  assert.notEqual(title, -1, 'README.md carries no "# Fugaro" title, and the site\'s tagline is the line under it')
  const line = lines.slice(title + 1).find((candidate) => candidate.trim() !== '')
  assert.ok(line, 'README.md\'s "# Fugaro" title is the last thing in the file, and the tagline is the line under it')
  return line.trim().replace(/^\*\*(.+)\*\*$/, '$1').trim()
}

/** One `## <heading>` section of README.md, up to the next `## `. */
function readmeSection(heading) {
  const readme = repoFile('README.md')
  const start = readme.indexOf(`\n## ${heading}\n`)
  assert.notEqual(start, -1, `README.md carries no "## ${heading}" section, and this check holds a caption to it`)
  const rest = readme.slice(start + 1)
  const next = rest.indexOf('\n## ', 1)
  return next === -1 ? rest : rest.slice(0, next)
}

/**
 * One install path of `website/data/install.yaml`, as its own block of the file: from its
 * `- id:` line to the next one. A block rather than a parsed document, because this check
 * is about what the file carries and the site's own reader is what parses it (W1.3).
 */
function installPathBlock(id) {
  const yaml = repoFile('website/data/install.yaml')
  const start = yaml.indexOf(`\n  - id: ${id}\n`)
  assert.notEqual(start, -1, `website/data/install.yaml carries no install path with id ${id}`)
  const rest = yaml.slice(start + 1)
  const next = rest.indexOf('\n  - id: ')
  return next === -1 ? rest : rest.slice(0, next)
}

/** The commands an install path prints: its `commands:` block scalar bodies. */
function installPathCommands(id) {
  return [...installPathBlock(id).matchAll(/^ {8}(.*)$/gm)].map((m) => m[1].trim()).filter(Boolean)
}

/** The five stage names `docs/website/spec.md` §2 gives the architecture diagram. */
function specStageNames() {
  const line = repoFile('docs/website/spec.md')
    .split('\n')
    .find((candidate) => candidate.includes('architecture diagram'))
  assert.ok(line, 'docs/website/spec.md §2 no longer describes the architecture diagram, and its five stage names are what the diagram renders')
  return line
    .slice(line.lastIndexOf(':') + 1)
    .split('→')
    .map((name) => name.trim())
    .filter(Boolean)
}

test("the hero tagline is README.md's tagline line, byte for byte", () => {
  const tagline = readmeTagline()
  const hero = classText(builtPage(''), 'hero-tagline')

  assert.equal(hero, tagline, 'hero tagline differs from the tagline line under README.md\'s "# Fugaro" title')
})

test("the hero's lead sentence is a sentence of README.md", () => {
  // The tagline is the README's line; the sentence under it is what Fugaro is, and D102
  // requires every other sentence on the site to be traceable to a document. It is read
  // from the built page and held against the README, so the component carries one copy and
  // this check is what keeps it the README's copy.
  const lead = classText(builtPage(''), 'hero-lead')

  assert.ok(lead, 'the hero renders no lead sentence, and a reader who has read the tagline needs one sentence saying what Fugaro is')
  assert.ok(
    normalize(repoFile('README.md')).includes(normalize(lead)),
    `the hero's lead sentence ${JSON.stringify(lead)} is not a sentence of README.md: every sentence on the site is traceable to a document (D102, W1.4 step 1)`,
  )
})

test('the hero renders the install commands install.yaml carries, and types none of them', () => {
  const page = builtPage('')
  const rendered = [...page.matchAll(/<div[^>]*class="hero-install-path"[^>]*>([\s\S]*?)<\/div>/g)].map((m) => m[0])

  assert.equal(rendered.length, 2, `the hero renders ${rendered.length} install paths, and it shows the two commands the plan names (W1.4 step 1)`)

  for (const block of rendered) {
    const id = /data-path="([^"]*)"/.exec(block)?.[1]
    assert.ok(id, 'an install path in the hero names no id, so nothing says which path of install.yaml it is')

    const commands = [...block.matchAll(/<pre[^>]*class="hero-install-command"[^>]*>([\s\S]*?)<\/pre>/g)].map((m) => text(m[1]))
    assert.deepEqual(
      commands,
      installPathCommands(id),
      `the hero's ${id} commands are not the ones website/data/install.yaml carries for that path: a command typed into the component is a second copy of the install text, and the one that drifts (W0.1, W1.4 step 1)`,
    )
  }
})

test('the hero renders a player only when site.yaml says the video exists', () => {
  const page = builtPage('')
  const flag = /^\s*video:\s*(true|false)\s*$/m.exec(repoFile('website/data/site.yaml'))

  assert.ok(flag, 'website/data/site.yaml carries no features.video, and the hero renders its video slot from that flag (D107)')
  if (flag[1] === 'true') return

  // The slot is empty while the video does not exist: no player, no poster, no embed, and
  // nothing that looks like a play button (D107, F50). W2.4's own test owns the other half.
  for (const [what, pattern] of [
    ['a <video> element', /<video\b/],
    ['an <iframe> embed', /<iframe\b/],
    ['a poster image', /\bposter=/],
    ['a player script', /type="video\//],
  ]) {
    assert.ok(
      !pattern.test(page),
      `the hero renders ${what} while site.yaml's features.video is false: the slot is empty until the video exists, and an empty player is the first thing a reader clicks (D107, W1.4 step 1)`,
    )
  }
})

test('the terminal types an invocation the CLI reference documents, and README.md shows', () => {
  const page = builtPage('')
  const command = classText(page, 'terminal-command')
  const reference = repoFile('website/content/cli-reference.md')

  assert.ok(command, 'the hero renders no terminal, and the spec\'s hero is a terminal typing a fugaro command (W1.4 step 2)')
  assert.ok(
    normalize(repoFile('README.md')).includes(normalize(command)),
    `the terminal types ${JSON.stringify(command)}, which README.md does not print: the command is the README's own example rather than one written for the page (W1.4 step 2)`,
  )
  assert.ok(
    reference.includes('`fugaro run`'),
    'the generated CLI reference documents no `fugaro run`, and the terminal types an invocation the reference carries (W0.4, W1.4 step 2)',
  )
})

test('the terminal holds the finished command when motion is reduced', () => {
  const page = builtPage('')
  const css = builtCSS(page)
  const command = classText(page, 'terminal-command')

  // The end state is in the markup: the finished command is what a reader sees with the
  // animation neutralised, so nothing about it depends on the animation having run.
  assert.ok(command && page.includes(command), 'the terminal\'s command is not in the markup in full, so a reader whose animation never runs sees a half-typed one')

  const typing = /\.terminal-command[^{]*\{([^}]*)\}/.exec(css)
  assert.ok(typing, 'the terminal has no typing rule, and the command is meant to be typed (W1.4 step 2)')
  assert.match(
    typing[1],
    /animation:[^;}]*\b(forwards|both)\b/,
    'the typing animation does not hold its last frame: with animation-duration neutralised the page has to land on the finished command, not on an empty line (D105, W1.4 step 2)',
  )

  // The rule that neutralises it is the layout's, written once for every component (W1.3
  // step 3, D105): this page carries it, so the terminal's end state is the one above.
  assert.match(css, /@media\s*\(prefers-reduced-motion:\s*reduce\)/, 'the built page carries no reduced-motion rule, and the terminal\'s end state depends on it (D105)')
})

test("the diagram carries the spec's five stage names and no sixth", () => {
  const names = specStageNames()
  assert.equal(names.length, 5, `docs/website/spec.md names ${names.length} stages, and the diagram renders the plan's five`)

  const page = builtPage('how-it-works')
  const rendered = [...page.matchAll(/data-stage="([^"]*)"/g)].map((m) => m[1])

  assert.deepEqual(
    rendered,
    names,
    'the diagram does not carry the spec\'s five stage names, in the spec\'s order, and no sixth (W1.4 step 3)',
  )
})

test("every caption on the diagram is the repository's own words", () => {
  const page = builtPage('how-it-works')
  const sources = {
    'README.md': readmeSection('How it works'),
    'docs/design/v1.md': repoFile('docs/design/v1.md'),
  }

  const stages = [...page.matchAll(/<li[^>]*class="stage"[^>]*>[\s\S]*?<\/li>/g)].map((m) => m[0])
  assert.equal(stages.length, 5, `the diagram renders ${stages.length} stages, and the spec names five (W1.4 step 3)`)

  for (const stage of stages) {
    const name = /data-stage="([^"]*)"/.exec(stage)?.[1]
    const source = /data-source="([^"]*)"/.exec(stage)?.[1]
    const caption = classText(stage, 'stage-caption')

    assert.ok(caption, `the diagram's ${JSON.stringify(name)} stage carries no caption, and every stage has one sentence saying what happens there (W1.4 step 3)`)
    assert.ok(source in sources, `the ${JSON.stringify(name)} stage attributes its caption to ${JSON.stringify(source)}, and a caption is held to README.md's "## How it works" or to docs/design/v1.md (W1.4 step 3)`)
    assert.ok(
      normalize(sources[source]).includes(normalize(caption)),
      `the ${JSON.stringify(name)} caption ${JSON.stringify(caption)} does not appear in ${source}: every caption on the diagram is the repository's own words rather than the page's (W1.4 step 3)`,
    )
  }

  // The two stage names README.md does not use are the page's map to the README's steps and
  // not quotations: nothing on the page attributes them to the README (W1.4 step 3).
  const howItWorks = sources['README.md']
  for (const label of ['security boundary', 'PR or artifact collected back']) {
    assert.ok(
      !howItWorks.includes(label),
      `README.md's "## How it works" uses the name ${JSON.stringify(label)}, and the page says it does not: the two names the README does not use are labels rather than quotations (W1.4 step 3)`,
    )
  }
})

test('neither page names a claim the product cannot back', () => {
  // The four sentences the reconciliation holds back (F50, D102), each as the thing a
  // reader could check for: a link to an artifact that does not exist, a platform the
  // repository does not build, a duration nothing measures, and ranges nothing computes.
  const heldBack = [
    ['a console download link', /releases\/download\//],
    ['Windows support, and WSL with it', /\bWSL\b|\bWindows\b/],
    ['the "~2 h, not 30 min" sentence', /~\s*2\s*h\b|\b2\s*h\b/],
    ['ballpark ranges from run history', /ballpark/i],
  ]

  for (const route of ['', 'how-it-works']) {
    const page = builtPage(route)
    for (const [what, pattern] of heldBack) {
      assert.ok(
        !pattern.test(page),
        `the ${route === '' ? '/' : `/${route}/`} page names ${what}, which the product cannot back today: the site publishes the measurement or nothing (F50, D102, W1.4 step 4)`,
      )
    }
  }
})
