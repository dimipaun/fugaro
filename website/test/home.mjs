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
//   3. **The terminal types a real invocation, and its transcript is the documented
//      sequence.** The command is README.md's own example and `fugaro run` is a command the
//      generated CLI reference documents (W0.4's file); the four lines after it are the
//      numbered steps of README.md's `## How it works`, each held to the section it cites
//      and to the order that section writes them in (P7, D184, RF11); and the animation
//      holds its last frame, so a reader whose system asks for less motion gets the whole
//      transcript rather than a half-typed line (D105, W1.4 step 2).
//   4. **The diagram carries the spec's five stage names and no sixth**, and every caption
//      on it is a line of README.md's `## How it works` or of `docs/design/v1.md` — the two
//      names the README does not use are asserted to be labels only (W1.4 step 3).
//   5. **Neither page names a claim the product cannot back**: no console download, no
//      Windows/WSL support, no "~2 h", no ballpark ranges (F50, D102, W1.4 step 4). The
//      video slot is checked against `site.yaml`'s own flag rather than against today's
//      value, so W2.4 turns the video on without meeting a red test here (D107).
//   6. **Every link the pages carry points at a route the plan's tasks write** — the layout's
//      nav and the hero's call to action included, since a link to a route outside that list
//      is a 404 in the artifact (Global Constraint 9). Whether each page *exists* yet is
//      W1.11's link check, which runs once the page tasks have landed.
//   7. **Every `var(--token)` the pages ship has a declaration behind it**, because an
//      undefined custom property makes its declaration invalid at computed-value time rather
//      than falling back: a token name the design layer does not declare is a style that
//      silently does not apply, which is how the hero came to name a `--weight-bold` (W1.2).
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file:
// the check is a statement about the artifact a reader receives, and every fact it holds a
// page to is read from the repository rather than from the page's own sources.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parse, textOf, walk } from './lib/html.mjs'

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
function decode(html) {
  return html
    .replace(/&quot;/g, '"')
    .replace(/&#39;|&apos;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
}

function text(html) {
  return decode(html.replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim()
}

/**
 * A node's text as a reader receives it: the decoration the page marks `aria-hidden` left
 * out (the terminal's `$` prompt and its caret are drawing, not words), entities decoded,
 * runs of whitespace one space. This is what the page says, which is what the checks below
 * hold to the repository.
 */
function readableText(node) {
  if (node.attrs && node.attrs.get('aria-hidden') === 'true') return ''
  if (node.text !== undefined) return node.text
  if (node.raw !== undefined) return ''
  return (node.children ?? []).map(readableText).join('')
}

/** The text of the first element carrying `class="…"`, or null when there is none. */
function classText(html, className) {
  const m = new RegExp(`<([a-z0-9]+)[^>]*class="${className}"[^>]*>([\\s\\S]*?)</\\1>`).exec(html)
  return m ? text(m[2]) : null
}

/**
 * A string as a document writes it rather than as a page renders it: markdown emphasis and
 * code spans are markers and not text, and where a document breaks a line or indents is the
 * document's layout rather than its words. Applied to both sides of every comparison below,
 * so a caption and the document it comes from are compared word for word with the layout
 * taken out of them — which is what README.md's `## How it works` needs, since a third of
 * that section is an ASCII diagram whose columns and padding are drawing rather than
 * writing, and a page that quotes a line of it should not have to quote the diagram's
 * whitespace along with it.
 */
function normalize(markdown) {
  return markdown.replace(/[`*]/g, '').replace(/\s+/g, '')
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

/**
 * The routes the site's pages live at, from the plan: the page files its tasks' `**Files:**`
 * lines name. Pages are lowercase and components are capitalised in this repository, so a
 * lowercase `<name>.astro` in a Files line is a page — and this is the only list of the
 * site's pages that exists while the page tasks are still being written.
 */
function plannedRoutes() {
  const routes = new Set()
  for (const line of repoFile('docs/website/implementation-plan.md').split('\n')) {
    if (!line.startsWith('**Files:**')) continue
    for (const m of line.matchAll(/([a-z0-9-]+)\.astro/g)) {
      routes.add(m[1] === 'index' ? '/' : `/${m[1]}/`)
    }
  }
  return routes
}

/** The internal links a built page carries: its hrefs that are paths on this site. */
function internalLinks(page) {
  return [...page.matchAll(/<a\b[^>]*href="([^"]+)"/g)]
    .map((m) => m[1])
    .filter((href) => href.startsWith('/'))
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
  // Read as a document rather than sliced with a regex: each path's block now holds a copy
  // control as well as its commands (P6), and a non-greedy `<div>…</div>` would stop at the
  // control's own closing tag and check half a block.
  const blocks = [...walk(parse(page))].filter((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('hero-install-path'))

  assert.equal(blocks.length, 2, `the hero renders ${blocks.length} install paths, and it shows the two commands the plan names (W1.4 step 1)`)

  for (const block of blocks) {
    const id = block.attrs.get('data-path')
    assert.ok(id, 'an install path in the hero names no id, so nothing says which path of install.yaml it is')

    const commands = [...walk(block)]
      .filter((el) => el.name === 'pre')
      .map((el) => textOf(el).trim())
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

// ---- the transcript (P7, D184) -----------------------------------------------------------

/**
 * The documents a transcript line may cite, by the name the line carries. The command is
 * README.md's example anywhere in the file; the four steps after it are the numbered steps
 * of its `## How it works`, so they cite that section and are held to it rather than to the
 * whole file.
 */
function transcriptSources() {
  return {
    'README.md': repoFile('README.md'),
    'README.md#How it works': readmeSection('How it works'),
  }
}

/** The transcript a built page renders: each line, the document it cites, and its timing. */
function transcript(page) {
  return [...walk(parse(page))]
    .filter((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('terminal-line'))
    .map((el) => {
      const style = el.attrs.get('style') ?? ''
      return {
        text: decode(readableText(el)).replace(/\s+/g, ' ').trim(),
        source: el.attrs.get('data-source') ?? '',
        characters: Number(/--terminal-characters:\s*(\d+)/.exec(style)?.[1] ?? NaN),
        delay: Number(/--terminal-delay:\s*([\d.]+)s/.exec(style)?.[1] ?? NaN),
        caret: [...walk(el)].some((child) => (child.attrs?.get('class') ?? '').split(/\s+/).includes('terminal-caret')),
      }
    })
}

/**
 * Every failure of the transcript's rule, as the lines a reader of this file sees: every
 * line is a line of the document it cites, and the lines of a document come in the order
 * that document writes them. The page's own caption calls the transcript the documented
 * sequence of a run, so a line the document does not write — or writes after a line that
 * follows it here — is the animation saying something the documentation does not (D184,
 * RF11). Written as a function over a transcript so a fixture can prove it refuses.
 */
export function transcriptFailures(lines, sources) {
  const out = []
  const reached = new Map() // document -> how far into it the line before this one reached
  for (const [index, line] of lines.entries()) {
    const where = `line ${index + 1} of the transcript, ${JSON.stringify(line.text)}`
    if (line.text === '') {
      out.push(`${where}, has no text: the whole transcript is in the markup, and a line that is somewhere else is one a reader without the animation never sees`)
      continue
    }
    const source = sources[line.source]
    if (!source) {
      out.push(`${where}, cites ${JSON.stringify(line.source)}, which is not a document this check reads: every line says which document it is quoted from`)
      continue
    }
    const at = normalize(source).indexOf(normalize(line.text), reached.get(line.source) ?? 0)
    if (at === -1) {
      out.push(
        `${where}, does not appear in ${line.source} at or after the line before it: the transcript is the documented sequence of a run rather than a run that happened, and every line of it is the repository's own words in the repository's own order (D184, RF11)`,
      )
      continue
    }
    reached.set(line.source, at + normalize(line.text).length)
  }
  return out
}

test("every line of the homepage transcript is the document it cites, in that document's order", () => {
  const lines = transcript(builtPage(''))

  // The transcript is the command and the README's four steps — Start, Sync, Loop, Push and
  // open the pull request (D184). A page that grew a sixth line, or lost one, is a page this
  // check would otherwise go on accepting.
  assert.equal(lines.length, 5, `the homepage terminal renders ${lines.length} lines, and the documented sequence is the command and the README's four steps (P7, D184)`)

  const failures = transcriptFailures(lines, transcriptSources())
  assert.deepEqual(failures, [], `the homepage transcript is not the documented sequence (P7, D184, RF11):\n  ${failures.join('\n  ')}`)
})

test('the transcript rule refuses a line the document it cites does not write', () => {
  const sources = transcriptSources()
  const lines = transcript(builtPage(''))
  assert.deepEqual(transcriptFailures(lines, sources), [], 'the rule refuses the page it is written for, so every refusal below is the one thing it breaks')

  // A step with a word added to it: not a line of the section, and the defect this rule
  // exists for — a transcript that is a paraphrase of the documentation rather than the
  // documentation's own words.
  const paraphrased = lines.map((line, index) =>
    index === 2 ? { ...line, text: `${line.text}, quickly` } : line,
  )
  assert.match(transcriptFailures(paraphrased, sources).join('\n'), /does not appear in README\.md#How it works/)

  // The README's words, but from outside the section the line cites. The claim a step line
  // makes is that it is a step of `## How it works`, so this fails even though the words are
  // in the file — which is why the section is what the line cites.
  const elsewhere = "Or ask your agent: the plugin's working skill launches, watches and diagnoses runs"
  assert.ok(normalize(sources['README.md']).includes(normalize(elsewhere)), 'the fixture quotes a sentence README.md carries, and it no longer does')
  assert.ok(!normalize(sources['README.md#How it works']).includes(normalize(elsewhere)), 'the fixture quotes a sentence outside "## How it works", and the README moved it inside')
  assert.match(
    transcriptFailures(lines.map((line, index) => (index === 1 ? { ...line, text: elsewhere } : line)), sources).join('\n'),
    /does not appear in README\.md#How it works/,
  )

  // The same lines, the fourth step put second: the words are all there, the order is not.
  const reordered = [lines[0], lines[3], lines[1], lines[2], lines[4]]
  assert.match(transcriptFailures(reordered, sources).join('\n'), /at or after the line before it/)

  // A line citing a document nothing reads, and a line with no text at all.
  assert.match(transcriptFailures([{ ...lines[0], source: 'website/content/cli-reference.md' }], sources).join('\n'), /not a document this check reads/)
  assert.match(transcriptFailures([{ ...lines[0], text: '' }], sources).join('\n'), /has no text/)
})

test('every line of the transcript is in the markup, revealed by its own count, one after another', () => {
  const page = builtPage('')
  const lines = transcript(page)
  assert.ok(lines.length > 1, 'the homepage terminal renders one line, and the transcript is a sequence (P7)')

  // The end state is in the markup: every line is whole in the page a reader receives, so
  // nothing about the transcript depends on the animation having run — with the animation
  // neutralised, the page is the whole transcript, still (D105, W1.4 step 2).
  for (const [index, line] of lines.entries()) {
    assert.ok(
      text(page).includes(line.text),
      `line ${index + 1} of the transcript, ${JSON.stringify(line.text)}, is not in the markup in full, so a reader whose animation never runs sees a half-typed line`,
    )
  }

  // The reveal: one step per character of the line, ending on the line's own width. The
  // count is written on the line and the rule reads it, so a count that does not cover the
  // line is a line that stops mid-word — and one that is far larger is a reveal that sits
  // still while the reader waits for it.
  const typing = /\.terminal-line[^{]*\{([^}]*)\}/.exec(builtCSS(page))
  assert.ok(typing, 'the transcript has no typing rule, and every line is meant to be typed (W1.4 step 2, P7)')
  assert.match(
    typing[1],
    /animation:[^;}]*\b(forwards|both)\b/,
    'the typing animation does not hold its last frame: with animation-duration neutralised the page has to land on the whole transcript, not on empty lines (D105, W1.4 step 2)',
  )
  assert.match(
    typing[1].replace(/\s+/g, ''),
    /steps\(var\(--terminal-characters\),end\)/,
    'the reveal is not stepped by the line\'s own character count, and a smooth reveal is a different animation from the one this page is written for (P7)',
  )

  for (const [index, line] of lines.entries()) {
    assert.ok(
      Number.isFinite(line.characters) && line.characters >= line.text.length,
      `line ${index + 1} of the transcript carries ${line.characters} characters for ${line.text.length} characters of text: the reveal is as wide as the line, and a count short of it cuts the line off`,
    )
    assert.ok(Number.isFinite(line.delay), `line ${index + 1} of the transcript carries no --terminal-delay, so every line arrives at once (P7)`)
  }
  assert.equal(lines[0].delay, 0, 'the first line of the transcript waits before it starts, and the command is what the reader came for (P7)')
  for (let i = 1; i < lines.length; i++) {
    assert.ok(lines[i].delay > lines[i - 1].delay, `line ${i + 1} of the transcript arrives with the line before it, and the lines are staggered so a reader can follow them (P7)`)
  }

  // The caret is in the last line and the last line's reveal is one column wider than its
  // text: that column is what the caret is drawn in, so a caret outside it — or a count
  // without it — is a caret that stays clipped until the page is reloaded.
  const last = lines[lines.length - 1]
  assert.ok(last.caret, 'the transcript\'s caret is not in its last line, so it sits at the end of a line that has not been typed yet (P7)')
  assert.ok(
    last.characters > last.text.length,
    'the last line\'s reveal is exactly its text wide, so the caret beside the text stays clipped (P7)',
  )

  // The rule that neutralises the animation is the layout's, written once for every
  // component (W1.3 step 3, D105): this page carries it, so the transcript's end state is
  // the one above.
  assert.match(builtCSS(page), /@media\s*\(prefers-reduced-motion:\s*reduce\)/, 'the built page carries no reduced-motion rule, and the transcript\'s end state depends on it (D105)')
})

test('the transcript says on the page that it is the documented sequence, not a recording', () => {
  // D184 admits the animation only in this form: the lines are the repository's own, and the
  // page says what they are. The caption is the whole of that claim, so it is checked as the
  // claim — a page that dropped it would be showing a transcript as if it were a run.
  const caption = classText(builtPage(''), 'terminal-caption')

  assert.ok(caption, 'the homepage terminal carries no caption, and D184 admits the run animation only as a labelled sequence rather than a recording (P7, RF11)')
  assert.match(caption, /documented sequence/i, `the terminal's caption ${JSON.stringify(caption)} does not say the lines are the documented sequence (D184)`)
  assert.match(caption, /not a recording/i, `the terminal's caption ${JSON.stringify(caption)} does not say the lines are not a recording of a run (D184)`)
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

test('the diagram moves on scroll only for a reader who allows motion', () => {
  const css = builtCSS(builtPage('how-it-works'))

  // The animation is a scroll-driven one on the stages, and the reduced-motion path is the
  // diagram itself: a reader whose system asks for less motion gets every stage in place,
  // because the animation that brings a stage in is inside a guard that never opens for
  // them. That is why the guard is checked here rather than a fill mode (W1.4 step 3, D105).
  assert.match(
    css,
    /animation-timeline:\s*view\(\)/,
    'the diagram has no scroll-driven animation, and W1.4 step 3 asks for one that reveals the stages as the reader reaches them',
  )
  assert.match(
    css.replace(/\s+/g, ''),
    /@supports\(animation-timeline:view\(\)\)/,
    'the scroll-driven animation is not behind a feature query: a browser that cannot run it has to render the diagram as it stands rather than render nothing',
  )
  assert.match(
    css.replace(/\s+/g, ''),
    /@media\(prefers-reduced-motion:no-preference\)/,
    'the scroll-driven animation is not behind a preference query, so a reader who asked for less motion would get a diagram that fades in as they scroll it (D105, W1.4 step 3)',
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

test("every link on the two pages points at a route the plan's tasks write", () => {
  const routes = plannedRoutes()

  // A check that read no routes would pass every link, so the parse is asserted on the two
  // routes this task writes: if the plan's Files lines stop naming pages, this says so
  // instead of going quiet.
  assert.ok(
    routes.has('/') && routes.has('/how-it-works/'),
    `docs/website/implementation-plan.md's Files lines no longer name the site's pages: this check read ${routes.size} routes out of it, and it holds every link to that list`,
  )

  for (const route of ['', 'how-it-works']) {
    const page = builtPage(route)
    const where = route === '' ? '/' : `/${route}/`
    for (const href of internalLinks(page)) {
      assert.ok(
        routes.has(href),
        `the ${where} page links ${href}, which is not a route the plan's tasks write (the layout's nav and the hero's call to action included, because every link on these pages is checked here): a route outside that list is a 404 in the artifact, and it is Global Constraint 9's defect one page over from the download link it names (W1.4)`,
      )
    }
  }
})

test('the tokens the pages name are the ones the design layer declares', () => {
  // An undefined custom property makes its declaration invalid at computed-value time: the
  // style does not apply, nothing logs, and the page renders as if the rule were not written.
  // So the pages' CSS is read for every token name it asks for and every name it declares,
  // and the difference has to be empty. This is the check the hero needed: it named a
  // `--weight-bold` and a `--leading-relaxed` that W1.2's layer does not carry, and both
  // silently did nothing.
  const pages = ['', 'how-it-works'].map(builtPage)
  const css = pages.map(builtCSS).join('\n')
  const named = new Set([...css.matchAll(/var\((--[a-z0-9-]+)/g)].map((m) => m[1]))

  assert.ok(
    named.size > 0,
    "the pages' styles name no token at all, and every value they paint is a token of the design layer (W1.2, W1.4)",
  )

  if (!existsSync(path.join(SITE, 'src/styles/tokens.css'))) {
    // W1.2's layer is not in this tree — this branch is stacked under it, and W1.2 is merged
    // on main — so the names cannot be checked against it here. What can be checked is that
    // the layout is written to bundle the layer the moment it arrives: the failure this
    // guards is a site that ships every page unstyled while every check stays green, which
    // is what an `import.meta.glob` resolving to nothing looks like from outside. The check
    // below arms itself in any tree that carries the layer, and it was run in one.
    assert.match(
      repoFile('website/src/layouts/Base.astro'),
      /import\.meta\.glob\(\s*'\.\.\/styles\/tokens\.css'\s*,\s*\{\s*eager:\s*true\s*\}\s*\)/,
      'website/src/styles/tokens.css is not in this tree and the layout does not bundle it either, so the pages would ship unstyled with nothing to notice it (W1.2, W1.4)',
    )
    return
  }

  // The declarations the artifact carries, from its stylesheets and from the properties a
  // component sets on an element (`--terminal-characters` is the terminal's, set inline).
  const declarations = new Set(
    [...`${css}\n${pages.join('\n')}`.matchAll(/(--[a-z0-9-]+)\s*:/g)].map((m) => m[1]),
  )
  const undeclared = [...named].filter((name) => !declarations.has(name))

  assert.deepEqual(
    undeclared,
    [],
    `the pages ask for ${undeclared.join(', ')}, and nothing in the artifact declares it: the declaration is invalid at computed-value time, so the style silently does not apply — a token name the design layer does not have is exactly this defect (W1.2, W1.4)`,
  )
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
