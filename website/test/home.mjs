// website/test/home.mjs — the hero, the terminal and the flow (website
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
//   4. **The flow carries D185's four steps, in order, with the two annotations on the job
//      step**, and every caption on it — a step's and an annotation's — is a line of
//      README.md or of `docs/design/v1.md`; the names the README does not use are asserted
//      to be labels rather than quotations. The four steps and the two annotations are read
//      out of the ruling's own row in `docs/website/redesign/reconciliation.md`, so the page
//      is held to the decision rather than to a copy of it (W1.4 step 3, P8, RF12/D185).
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
import { findAll, parse, textOf, walk } from './lib/html.mjs'

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

/**
 * D185's four steps and its two annotations, read out of the ruling itself. The row is the
 * decision the flow renders, so it is the row this check holds the page to: a page that
 * changed its steps and a ruling that changed its mind cannot drift apart silently, and a
 * reworded row stops this check with the row in the message rather than passing quietly.
 */
function d185() {
  const line = repoFile('docs/website/redesign/reconciliation.md')
    .split('\n')
    .find((candidate) => candidate.startsWith('| **D185**'))
  assert.ok(
    line,
    'docs/website/redesign/reconciliation.md carries no D185 row, and the four steps the flow renders are the ones that ruling names (P8)',
  )
  // The row's own words, capital and all: its first two cells name the alternative it did
  // not take, and the four steps are the ones between the em dashes of the decision.
  const steps = /The brief's four steps — (.+?) —/.exec(line)
  const annotations = /`([^`]+)` and `([^`]+)` stay as two annotations on the job step/.exec(line)
  assert.ok(
    steps && annotations,
    `the D185 row no longer reads the way this check reads it (the four steps between two em dashes, then the two annotations on the job step): ${JSON.stringify(line.slice(0, 160))}`,
  )
  return {
    steps: steps[1].split(',').map((name) => name.trim()),
    annotations: [annotations[1], annotations[2]],
  }
}

/** The documents a caption may be quoted from, by the path a step cites. */
function captionSources() {
  return {
    'README.md': repoFile('README.md'),
    'docs/design/v1.md': repoFile('docs/design/v1.md'),
  }
}

/** A built page's flow, as the elements it renders: the steps in order, with their arrows. */
function flowOf(html) {
  const classes = (el) => (el.attrs.get('class') ?? '').split(/\s+/)
  const doc = parse(html)
  const flows = findAll(doc, (el) => el.name === 'ol' && classes(el).includes('flow-steps'))
  const out = { flows: flows.length, steps: [], arrows: 0 }
  for (const flow of flows) {
    for (const el of walk(flow)) {
      if (el.name === 'svg' && classes(el).includes('step-arrow')) out.arrows += 1
      if (el.name !== 'li' || !classes(el).includes('flow-step')) continue
      const caption = [...walk(el)].find((child) => classes(child).includes('step-caption'))
      out.steps.push({
        name: el.attrs.get('data-step') ?? '',
        source: el.attrs.get('data-source') ?? '',
        caption: caption ? textOf(caption).replace(/\s+/g, ' ').trim() : '',
        annotations: [...walk(el)]
          .filter((child) => child.name === 'li' && classes(child).includes('annotation'))
          .map((child) => ({
            name: child.attrs.get('data-annotation') ?? '',
            source: child.attrs.get('data-source') ?? '',
            caption: (() => {
              const text = [...walk(child)].find((x) => classes(x).includes('annotation-caption'))
              return text ? textOf(text).replace(/\s+/g, ' ').trim() : ''
            })(),
          })),
      })
    }
  }
  return out
}

/**
 * Every way a page's flow falls short of D185, as the lines a reader sees. Pure, over one
 * page's HTML, so the built pages are checked with it and the refusals below are proved with
 * it: a fixture that breaks one rule has to make exactly that line appear.
 */
function flowFailures(html, ruling, sources) {
  const out = []
  const flow = flowOf(html)
  if (flow.flows !== 1) return [`the page renders ${flow.flows} flows, and it renders one: the flow is the picture of the run (P8, D185)`]

  const names = flow.steps.map((step) => step.name)
  if (names.join(' | ') !== ruling.steps.join(' | ')) {
    out.push(
      `the flow's steps are ${JSON.stringify(names)}, and D185 names ${JSON.stringify(ruling.steps)}, in that order: a step added, dropped or reordered is a claim about how a run works (P8)`,
    )
  }

  for (const step of flow.steps) {
    if (!(step.source in sources)) {
      out.push(`the ${JSON.stringify(step.name)} step attributes its caption to ${JSON.stringify(step.source)}, and a caption is held to README.md or to docs/design/v1.md (P8)`)
    } else if (!normalize(sources[step.source]).includes(normalize(step.caption))) {
      out.push(`the ${JSON.stringify(step.name)} caption ${JSON.stringify(step.caption)} does not appear in ${step.source}: every caption is the repository's own words rather than the page's (P8, D185)`)
    }
    if (!step.caption) out.push(`the ${JSON.stringify(step.name)} step carries no caption, and every step has one short line saying what happens there (P8, D185)`)
  }

  const annotated = flow.steps.filter((step) => step.annotations.length > 0)
  const job = flow.steps[1]
  const where = annotated.map((step) => step.name)
  if (where.join(' | ') !== (job ? [job.name].join(' | ') : '')) {
    out.push(
      `the flow annotates ${JSON.stringify(where)}, and D185 puts both annotations on the job step, ${JSON.stringify(job?.name ?? '')}: the cache and the security boundary are things the container does, not steps of their own (P8, D185)`,
    )
  }
  for (const annotation of annotated.flatMap((step) => step.annotations)) {
    if (!(annotation.source in sources)) {
      out.push(`the ${JSON.stringify(annotation.name)} annotation attributes its caption to ${JSON.stringify(annotation.source)}, and a caption is held to README.md or to docs/design/v1.md (P8)`)
    } else if (!normalize(sources[annotation.source]).includes(normalize(annotation.caption))) {
      out.push(`the ${JSON.stringify(annotation.name)} caption ${JSON.stringify(annotation.caption)} does not appear in ${annotation.source}: an annotation is a label with a quoted line, and the line is the repository's (P8, D185)`)
    }
  }
  const annotationNames = annotated.flatMap((step) => step.annotations.map((annotation) => annotation.name))
  if (annotationNames.join(' | ') !== ruling.annotations.join(' | ')) {
    out.push(`the flow's annotations are ${JSON.stringify(annotationNames)}, and D185 names ${JSON.stringify(ruling.annotations)}, in that order (P8)`)
  }

  if (flow.arrows !== flow.steps.length - 1) {
    out.push(
      `the flow carries ${flow.arrows} arrows between ${flow.steps.length} steps, and it carries one between each pair: the brief's picture is a flow, and the arrows are what draw it (P8, spec item 4)`,
    )
  }
  return out
}

/** A page carrying nothing but a flow, for the refusals below. */
function flowPage(steps) {
  const body = steps
    .map((step, index) => {
      const annotations = (step.annotations ?? [])
        .map(
          (annotation) =>
            `<li class="annotation" data-annotation="${annotation.name}" data-source="${annotation.source}"><span class="annotation-name">${annotation.name}</span><span class="annotation-caption">${annotation.caption}</span></li>`,
        )
        .join('')
      const arrow =
        index < steps.length - 1 && step.arrow !== false
          ? '<svg class="step-arrow" aria-hidden="true" focusable="false"><path class="arrow-line" d="M12 1 V 25"></path></svg>'
          : ''
      return `<li class="flow-step" data-step="${step.name}" data-source="${step.source}"><div class="step-card"><div class="step-head"><span class="step-number" aria-hidden="true">${index + 1}</span><h2 class="step-name">${step.name}</h2></div><p class="step-caption">${step.caption}</p><ul class="step-annotations">${annotations}</ul></div>${arrow}</li>`
    })
    .join('')
  return `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>t</h1><figure class="flow"><ol class="flow-steps">${body}</ol></figure></main></body></html>`
}

/** The four steps as the page renders them, for the fixture cases: a copy of the markup. */
function fixtureSteps() {
  return [
    { name: 'task submitted', source: 'README.md', caption: 'fugaro run "add a --verbose flag to the export command"' },
    {
      name: 'ephemeral Cloud Run job',
      source: 'README.md',
      caption: 'one execution per task, then gone',
      annotations: [
        { name: 'cache', source: 'README.md', caption: 'Sync to the exact target commit and restore the dependency caches.' },
        { name: 'security boundary', source: 'docs/design/v1.md', caption: 'The trust boundary is the container, not the process.' },
      ],
    },
    { name: 'agent runs', source: 'README.md', caption: 'Claude Code implements, writes tests, runs the build and tests' },
    { name: 'pull request', source: 'README.md', caption: 'Push and open the pull request' },
  ]
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
  assert.ok(
    command && text(page).includes(command),
    'the terminal\'s command is not in the markup in full, so a reader whose animation never runs sees a half-typed one',
  )

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

test("the flow carries D185's four steps, in order, with the two annotations on the job step", () => {
  const ruling = d185()
  assert.equal(ruling.steps.length, 4, `D185's row names ${ruling.steps.length} steps, and the brief draws four (spec item 4, P8)`)
  assert.equal(ruling.annotations.length, 2, `D185's row names ${ruling.annotations.length} annotations, and the ruling keeps two (P8)`)

  // The homepage's flow sits under the hero, which is where D185 puts it: the picture of a
  // run above the tagline would be the first thing a reader meets, before anything has said
  // what Fugaro is.
  const home = builtPage('')
  const hero = home.indexOf('<section class="hero"')
  const flow = home.indexOf('<figure class="flow"')
  assert.ok(
    hero !== -1 && flow > hero,
    `the homepage renders its flow ${flow === -1 ? 'nowhere' : 'above the hero'}: D185 places the picture of the run below the hero (P8)`,
  )

  // Both pages, because the same flow is placed on the homepage as the brief's item 5 "good
  // images or diagrams that show how the system works" (RF21, D185): a check that read only
  // How It Works would not notice the homepage losing the picture.
  for (const route of ['', 'how-it-works']) {
    const failures = flowFailures(builtPage(route), ruling, captionSources())
    assert.deepEqual(
      failures,
      [],
      `the ${route === '' ? '/' : `/${route}/`} page's flow is not the one D185 describes:\n  ${failures.join('\n  ')}`,
    )
  }
})

test('the flow refusals fire on the defect each one names', () => {
  // The cases below are the check's own proof: the fixture page passes, and each fixture
  // that breaks one rule of the flow makes exactly that rule's line appear. Without them a
  // check that read no steps at all would pass every page.
  const ruling = d185()
  const sources = captionSources()
  const fixture = (steps) => flowPage(steps)
  const failing = (steps) => flowFailures(fixture(steps), ruling, sources)

  assert.deepEqual(failing(fixtureSteps()), [], 'the fixture page does not pass the check it is the control for')

  const dropped = fixtureSteps().slice(0, -1)
  assert.ok(
    failing(dropped).some((line) => /steps are .*and D185 names/.test(line)),
    'a step removed from the flow passes the check: the four steps are what the brief draws, and a fifth or a missing one has to fail',
  )

  const reordered = [...fixtureSteps()]
  ;[reordered[2], reordered[3]] = [reordered[3], reordered[2]]
  assert.ok(
    failing(reordered).some((line) => /steps are .*and D185 names/.test(line)),
    'two steps swapped pass the check: the order is the run\'s order, and a flow that drew the pull request before the agent describes a run that cannot happen',
  )

  const paraphrased = fixtureSteps().map((step, i) => (i === 2 ? { ...step, caption: 'The agent works in the container.' } : step))
  assert.ok(
    failing(paraphrased).some((line) => /does not appear in README.md/.test(line)),
    'a caption written for the page passes the check: every caption is a line of the document it cites (Global Constraint 6, RF11)',
  )

  const misquoted = fixtureSteps().map((step, i) =>
    i === 1 ? { ...step, annotations: [{ ...step.annotations[0], source: 'docs/design/v1.md' }] } : step,
  )
  assert.ok(
    failing(misquoted).some((line) => /"cache" caption .* does not appear in docs\/design\/v1.md/.test(line)),
    'an annotation whose caption is not in the document it cites passes the check: the annotation\'s line is quoted like every other caption',
  )

  const moved = fixtureSteps().map((step, i) =>
    i === 1 ? { ...step, annotations: [] } : i === 2 ? { ...step, annotations: fixtureSteps()[1].annotations } : step,
  )
  assert.ok(
    failing(moved).some((line) => /annotates .*and D185 puts both annotations on the job step/.test(line)),
    'the annotations moved off the job step pass the check: D185 keeps them on the step they describe',
  )

  const renamed = fixtureSteps().map((step, i) => (i === 1 ? { ...step, annotations: [{ ...step.annotations[0], name: 'deps' }, step.annotations[1]] } : step))
  assert.ok(
    failing(renamed).some((line) => /annotations are .*and D185 names/.test(line)),
    'an annotation renamed passes the check: the two names are the ruling\'s, not the page\'s',
  )

  const arrowless = fixtureSteps().map((step, i) => (i === 1 ? { ...step, arrow: false } : step))
  assert.ok(
    failing(arrowless).some((line) => /carries 2 arrows between 4 steps/.test(line)),
    'a step with no arrow after it passes the check: the brief asks for a visual flow, and the arrows are what make it one',
  )

  // And the reader itself: a page that renders no flow is the failure a check reading
  // "no steps" would otherwise report as success.
  assert.deepEqual(
    flowFailures('<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>t</h1></main></body></html>', ruling, sources),
    ['the page renders 0 flows, and it renders one: the flow is the picture of the run (P8, D185)'],
  )
})

test('the flow moves on scroll only for a reader who allows motion', () => {
  // Both pages, because the flow is on both and each page ships the component's CSS.
  const css = ['', 'how-it-works'].map((route) => builtCSS(builtPage(route))).join('\n')

  // The animation is a scroll-driven one on the steps, and the reduced-motion path is the
  // flow itself: a reader whose system asks for less motion gets every step in place,
  // because the animation that brings a step in is inside a guard that never opens for
  // them. That is why the guard is checked here rather than a fill mode (W1.4 step 3, D105).
  assert.match(
    css,
    /animation-timeline:\s*view\(\)/,
    'the flow has no scroll-driven animation, and W1.4 step 3 asks for one that reveals the steps as the reader reaches them',
  )
  assert.match(
    css.replace(/\s+/g, ''),
    /@supports\(animation-timeline:view\(\)\)/,
    'the scroll-driven animation is not behind a feature query: a browser that cannot run it has to render the flow as it stands rather than render nothing',
  )
  assert.match(
    css.replace(/\s+/g, ''),
    /@media\(prefers-reduced-motion:no-preference\)/,
    'the scroll-driven animation is not behind a preference query, so a reader who asked for less motion would get a flow that fades in as they scroll it (D105, W1.4 step 3)',
  )
})

test("every caption in the flow is the repository's own words", () => {
  const ruling = d185()
  const sources = captionSources()

  for (const route of ['', 'how-it-works']) {
    const page = builtPage(route)
    const where = route === '' ? '/' : `/${route}/`
    const flow = flowOf(page)

    assert.equal(flow.steps.length, 4, `the ${where} page's flow renders ${flow.steps.length} steps, and D185 names four (P8)`)

    for (const step of flow.steps) {
      assert.ok(step.caption, `the ${where} page's ${JSON.stringify(step.name)} step carries no caption, and every step has one short line saying what happens there (P8, D185)`)
      assert.ok(step.source in sources, `the ${where} page's ${JSON.stringify(step.name)} step attributes its caption to ${JSON.stringify(step.source)}, and a caption is held to README.md or to docs/design/v1.md (P8)`)
      assert.ok(
        normalize(sources[step.source]).includes(normalize(step.caption)),
        `the ${where} page's ${JSON.stringify(step.name)} caption ${JSON.stringify(step.caption)} does not appear in ${step.source}: every caption in the flow is the repository's own words rather than the page's (W1.4 step 3, P8, D185)`,
      )
    }

    for (const annotation of flow.steps.flatMap((step) => step.annotations)) {
      assert.ok(
        annotation.caption && normalize(sources[annotation.source] ?? '').includes(normalize(annotation.caption)),
        `the ${where} page's ${JSON.stringify(annotation.name)} annotation caption ${JSON.stringify(annotation.caption)} does not appear in ${annotation.source}: an annotation is a label and a quoted line, and the line is the repository's (P8, D185)`,
      )
    }
  }

  // One name the README does not use is the page's label for a fact the design states in
  // other words, and it stays a label: nothing on the page presents it as a line of the
  // README's "## How it works" (P8, D185).
  const howItWorks = readmeSection('How it works')
  for (const label of ruling.annotations) {
    // A word of its own rather than a substring: the README's step 2 restores "the
    // dependency caches", and a page that wrote `cache` as though the section said it would
    // be quoting a word the section does not have.
    const asWord = new RegExp(`(^|[^a-z])${label}([^a-z]|$)`)
    assert.ok(
      !asWord.test(howItWorks),
      `README.md's "## How it works" writes ${JSON.stringify(label)} as a word of its own, and the page keeps it as a label over a quoted caption: the names the README does not use are labels rather than quotations (P8, D185)`,
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
