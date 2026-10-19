// website/test/layout.mjs — the base layout and the content pipeline (website plan, W1.3).
//
// Three things are checked here, and the first is the one the task exists for:
//
//   1. **The build fails, loudly, when a generated file is not there.** The defect W1.3
//      removes is a build that succeeds while a page renders an empty block — an
//      ungenerated security page, an install section with no commands — because nothing
//      in the artifact says the fact was missing. So the tests below remove a file, strip
//      a file's generated header, break a data file, and publish a figure drawn from too
//      few runs, and each time the build has to stop and name the file.
//   2. **The head's facts come from `site.yaml` and from nowhere else.** The canonical URL
//      is a read of that file (change it and the built page follows), it appears in no
//      source file as a literal, and the sitemap and font hints point only at files the
//      build actually produces.
//   3. **The analytics tag is gated twice.** It is absent while `site.yaml`'s flag is
//      false, and when the flag is on the layout emits no fetch of its own: the origin
//      reaches the page only as the consent banner's configuration, and the file that reads
//      it is the site's one script (D93, D106, W3.2). What that file does with the
//      configuration — fetch nothing before a decision, one script after a grant — is
//      `consent.mjs`'s subject, and it runs the file to see it.
//
// How it builds: a test needs to *break* a file and build again, so it builds a copy of
// the repository in a temp directory rather than the checkout itself — the plan's own
// words for the first case ("a test that deletes it in a temp copy"). The copy is the
// repository minus everything a site build cannot read (`internal/`, `console/`, the
// Go test data), with the installed `node_modules` symlinked rather than copied, and it
// mirrors the checkout's shape — a `website/` directory with a `README.md` beside it —
// because the layout reads both. The page that renders the layout is the copy's own
// `src/pages/probe.astro`: the site's real pages are W1.4-W1.10's, and this test needs a
// page it can rely on and mutate. Every build is network-free: `npm ci` is not run here,
// and the only executable it starts is the site's own installed Astro.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { READ_UNDER_PLUGIN, linkNodeModules } from './lib/build.mjs'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)

// What a site build cannot read, and therefore what the copies below leave out: the Go
// module's own tree and test data, the console application, and the installed and built
// directories (the last two are symlinked and rebuilt instead).
const SKIP = new Set(['.git', 'internal', 'cmd', 'console', 'schemas', 'deploy', 'testdata'])
const SKIP_UNDER_WEBSITE = new Set(['node_modules', 'dist', '.astro'])

// The probe page: a page that renders the layout, and through it the whole content
// pipeline. It renders the security document's own heading, so a build that renders an
// empty block is visible in the output rather than only in the exit code, and the first
// install path's command and note, so a test can hold a fact's *text* — which is where a
// reader that guesses instead of refusing shows up — and not only its presence.
const PROBE = `---
import Base from '../layouts/Base.astro'
import { loadSite } from '../content.ts'

const site = loadSite()
const security = site.documents['security.md']
const heading = security.body.split('\\n')[0]
const homebrew = site.install.paths[0]
---

<Base title="The probe page">
  <h1>The probe page</h1>
  <section id="generated" data-source={security.source}>
    <h2>{heading}</h2>
  </section>
  <section id="install" data-command={homebrew.commands[0]} data-note={homebrew.note}></section>
</Base>
`

/** A copy of the repository in a temp directory, ready to be mutated and built. */
function copyOfTheRepository() {
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-layout-'))
  const root = path.join(dir, 'repo')
  cpSync(REPO, root, {
    recursive: true,
    filter: (src) => {
      const name = path.basename(src)
      const parent = path.basename(path.dirname(src))
      if (path.dirname(src) === REPO && SKIP.has(name)) return false
      // `plugin/skills` is the one part of `plugin/` a build reads: the hero's chat
      // quotes the skills (website/test/lib/build.mjs).
      if (parent === 'plugin' && !READ_UNDER_PLUGIN.has(name)) return false
      if (parent === 'website' && SKIP_UNDER_WEBSITE.has(name)) return false
      return true
    },
  })
  // Its own node_modules, every package linked but the build caches (.vite, .astro) its
  // own: a symlink to the whole tree shared vite's cache between parallel builds, which is
  // the ENOTEMPTY flake (website/test/lib/build.mjs).
  linkNodeModules(path.join(root, 'website', 'node_modules'))
  mkdirSync(path.join(root, 'website', 'src', 'pages'), { recursive: true })
  writeFileSync(path.join(root, 'website', 'src', 'pages', 'probe.astro'), PROBE)
  return { dir, root, site: path.join(root, 'website') }
}

/** Build the copy, and hand back what the build said and what it produced. */
function build(root) {
  const site = path.join(root, 'website')
  const result = spawnSync(process.execPath, [path.join(site, 'node_modules', 'astro', 'bin', 'astro.mjs'), 'build'], {
    cwd: site,
    encoding: 'utf8',
    env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' },
  })
  const output = `${result.stdout ?? ''}${result.stderr ?? ''}`
  const page = path.join(site, 'dist', 'probe', 'index.html')
  return {
    status: result.status,
    output,
    page: existsSync(page) ? readFileSync(page, 'utf8') : null,
  }
}

/** Build a fresh copy, mutate one file, build again, and clean up after the test. */
function buildWith(t, mutate) {
  const copy = copyOfTheRepository()
  t.after(() => rmSync(copy.dir, { recursive: true, force: true }))
  mutate(copy)
  return build(copy.root)
}

/** The bytes of a file in the checkout, for a test to hold the build's output against. */
function repoFile(relative) {
  return readFileSync(path.join(REPO, relative), 'utf8')
}

/** The built page, for a test that expects the build to have succeeded. */
function builtPage(built) {
  assert.equal(built.status, 0, `the build failed:\n${built.output}`)
  assert.ok(built.page, 'the build produced no page')
  return built.page
}

test('the build renders a page that carries the generated document it names', async (t) => {
  const built = buildWith(t, () => {})

  const page = builtPage(built)

  // The heading the page renders is the security document's own, read from the checkout
  // rather than from the build: the pipeline renders the generated file, and this is the
  // generated file.
  const security = repoFile('website/content/security.md')
  const heading = security.split('\n').slice(2).join('\n').trim().split('\n')[0]
  assert.ok(page.includes(`<h2>${heading}</h2>`), `the page does not render the security document's heading ${JSON.stringify(heading)}`)
  assert.match(page, /id="generated" data-source="SECURITY.md"/)
})

test('the build fails when a generated file is removed', async (t) => {
  const built = buildWith(t, (copy) => rmSync(path.join(copy.site, 'content', 'security.md')))

  assert.notEqual(built.status, 0, `the build succeeded with website/content/security.md removed, and rendered a page without it:\n${built.output}`)
  assert.match(built.output, /security\.md/, 'the failure does not name the file that is missing')
  assert.equal(built.page, null, 'the build wrote a page even though the file it renders is gone')
})

test('the build fails when a generated file loses its header', async (t) => {
  // The header is what makes a generated file generated: a file without it is one
  // somebody edited by hand, and the build refuses it rather than rendering it.
  const built = buildWith(t, (copy) => {
    const file = path.join(copy.site, 'content', 'security.md')
    writeFileSync(file, readFileSync(file, 'utf8').split('\n').slice(1).join('\n'))
  })

  assert.notEqual(built.status, 0, `the build succeeded with an ungenerated security.md:\n${built.output}`)
  assert.match(built.output, /security\.md/, 'the failure does not name the file without a header')
  assert.equal(built.page, null, 'the build wrote a page from a file with no generated header')
})

test('the pipeline reads an inline comment as a comment, not as part of the fact', async (t) => {
  // The data files are YAML, and in YAML `- brew install x   # the cask` is a command and
  // a comment. A reader that adopts the comment into the fact renders somebody's note to
  // a reader of the site — and does it silently, which is the failure mode this whole
  // pipeline exists to remove. Both halves are checked here: an inline value and a
  // sequence item, against the same two facts the page renders.
  const built = buildWith(t, (copy) => {
    const file = path.join(copy.site, 'data', 'install.yaml')
    const before = readFileSync(file, 'utf8')
    const after = before
      .replace('      - |-\n        brew install dimipaun/tap/fugaro\n', '      - brew install dimipaun/tap/fugaro   # the cask\n')
      .replace('    note: The Homebrew cask (tested on macOS).', '    note: The Homebrew cask (tested on macOS). # tested on macOS 15')
    assert.notEqual(after, before, 'the fixture did not change install.yaml, so this test would pass on anything')
    writeFileSync(file, after)
  })

  const page = builtPage(built)
  assert.match(page, /data-command="brew install dimipaun\/tap\/fugaro"/, 'the command did not survive the comment, or the comment was adopted into it')
  assert.match(page, /data-note="The Homebrew cask \(tested on macOS\)\."/, 'the note did not survive the comment, or the comment was adopted into it')
  assert.ok(!page.includes('the cask'), 'the comment was adopted into a fact the page renders')
  assert.ok(!page.includes('tested on macOS 15'), 'the comment was adopted into a fact the page renders')
})

/** Build with one replacement in a data file, and fail the test if the fixture did not apply. */
function buildWithData(t, name, from, to) {
  return buildWith(t, (copy) => {
    const file = path.join(copy.site, 'data', name)
    const before = readFileSync(file, 'utf8')
    const after = before.replace(from, to)
    assert.notEqual(after, before, `the fixture did not change ${name}, so this test would pass on anything`)
    writeFileSync(file, after)
  })
}

const HOMEBREW_NOTE = '    note: The Homebrew cask (tested on macOS).'

/** The data-note of the probe page, with HTML's own escaping of a quote undone. */
function renderedNote(page) {
  const m = /data-note="([^"]*)"/.exec(page)
  assert.ok(m, 'the page carries no data-note')
  return m[1].replaceAll('&#39;', "'").replaceAll('&#x27;', "'").replaceAll('&quot;', '"')
}

test('an apostrophe in a plain scalar does not hide the comment after it', async (t) => {
  // Only a quote that OPENS a scalar is a quote in YAML. In `The cask's page ...` the
  // apostrophe is text, so the ` # ...` after it is still a comment.
  const built = buildWithData(t, 'install.yaml', HOMEBREW_NOTE, "    note: The cask's page is the tap   # not part of the note")

  assert.equal(renderedNote(builtPage(built)), "The cask's page is the tap")
})

test('a # inside a quoted scalar is content, and a quote can be escaped inside one', async (t) => {
  const double = buildWithData(t, 'install.yaml', HOMEBREW_NOTE, '    note: "say \\"hi\\" # kept"   # dropped')
  assert.equal(renderedNote(builtPage(double)), 'say "hi" # kept')

  const single = buildWithData(t, 'install.yaml', HOMEBREW_NOTE, "    note: 'it''s # kept'   # dropped")
  assert.equal(renderedNote(builtPage(single)), "it's # kept")
})

test('the build refuses a misspelled key in every nested map the loader reads', async (t) => {
  const cases = [
    ['site.yaml', '  enabled: true\n', '  enabled: true\n  enabeld: true\n', 'enabeld', 'analytics'],
    ['site.yaml', '  video: false\n', '  video: false\n  vidoe: true\n', 'vidoe', 'features'],
    ['install.yaml', '  arch: [amd64, arm64]\n', '  arch: [amd64, arm64]\n  archs: []\n', 'archs', 'build'],
    ['install.yaml', '    status: supported\n', '    status: supported\n    stauts: untested\n', 'stauts', 'platforms[]'],
    ['install.yaml', '    tap_repository: dimipaun/homebrew-tap\n', '    tap_repository: dimipaun/homebrew-tap\n    devonly: true\n', 'devonly', 'paths[]'],
  ]
  for (const [name, from, to, key, map] of cases) {
    const built = buildWithData(t, name, from, to)
    assert.notEqual(built.status, 0, `the build accepted the misspelled key "${key}" in ${map}, and used its default:\n${built.output}`)
    assert.match(built.output, new RegExp(`the key "${key}" is not one the site's pages read`), `the failure for ${map} does not name the key`)
  }
})

test('the build refuses a data file that uses a construct the reader has not been taught', async (t) => {
  // The reader's contract is "refuse what you do not understand rather than guessing at
  // it". An alias is the clearest case: taken for text, `- *cask` becomes a command a
  // reader would copy and a shell could not run, and nothing on the page would look wrong.
  const built = buildWith(t, (copy) => {
    const file = path.join(copy.site, 'data', 'install.yaml')
    const before = readFileSync(file, 'utf8')
    const after = before.replace('      - |-\n        brew install dimipaun/tap/fugaro\n', '      - *cask\n')
    assert.notEqual(after, before, 'the fixture did not change install.yaml, so this test would pass on anything')
    writeFileSync(file, after)
  })

  assert.notEqual(built.status, 0, `the build accepted a YAML alias as a command:\n${built.output}`)
  assert.match(built.output, /install\.yaml/, 'the failure does not name the data file it refused')
  assert.match(built.output, /alias, anchor, tag or flow mapping/, 'the failure does not say what the reader could not read')
})

test('the build fails when a data file is missing', async (t) => {
  const built = buildWith(t, (copy) => rmSync(path.join(copy.site, 'data', 'site.yaml')))

  assert.notEqual(built.status, 0, `the build succeeded with website/data/site.yaml removed:\n${built.output}`)
  assert.match(built.output, /site\.yaml/, 'the failure does not name the data file that is missing')
})

test('the build fails when a data file is malformed', async (t) => {
  const built = buildWith(t, (copy) => {
    const file = path.join(copy.site, 'data', 'site.yaml')
    writeFileSync(file, readFileSync(file, 'utf8').replace('min_runs: 20', 'min_runs: "twenty"'))
  })

  assert.notEqual(built.status, 0, `the build succeeded with a malformed min_runs:\n${built.output}`)
  assert.match(built.output, /site\.yaml/, 'the failure does not name the data file that is malformed')
  assert.match(built.output, /min_runs/, 'the failure does not name the key that is malformed')
})

test('the build refuses a published figure drawn from too few runs', async (t) => {
  // D97: no figure is published from fewer runs than the aggregation's minimum. The
  // command suppresses a thin group, and this is the fence behind it — a costs file that
  // reached the site anyway is not rendered.
  const thin = JSON.stringify({
    project: 'sandbox',
    since: '2026-01-01',
    until: '2026-02-01',
    min_runs: 20,
    recipes: [{ recipe: 'standard', runs: 4, compute_not_estimated: 0, duration_s: { min: 1, median: 2, p90: 3 }, cost_usd: { min: 0.1, median: 0.2, p90: 0.3 } }],
    totals: { runs: 4, recipes: 1 },
    not_a_promise: 'Figures are measurements, not a promise.',
  })
  const built = buildWith(t, (copy) => writeFileSync(path.join(copy.site, 'content', 'costs.json'), thin))

  assert.notEqual(built.status, 0, `the build published a group of 4 runs, below the minimum of 20:\n${built.output}`)
  assert.match(built.output, /costs\.json/, 'the failure does not name the file the figures came from')
  assert.match(built.output, /standard/, 'the failure does not name the group that is too thin')
})

test('the build accepts published figures above the threshold', async (t) => {
  // The same file, above the minimum: the rule is a threshold and not a refusal to
  // publish anything (W1.5 renders this file when the owner has run the aggregation).
  const figures = JSON.stringify({
    project: 'sandbox',
    since: '2026-01-01',
    until: '2026-02-01',
    min_runs: 20,
    recipes: [{ recipe: 'standard', runs: 40, compute_not_estimated: 2, duration_s: { min: 60, median: 120.5, p90: 240 }, cost_usd: { min: 0.1, median: 0.2, p90: 0.3 } }],
    totals: { runs: 40, recipes: 1 },
    not_a_promise: 'Figures are measurements, not a promise.',
  })
  const built = buildWith(t, (copy) => writeFileSync(path.join(copy.site, 'content', 'costs.json'), figures))

  assert.equal(built.status, 0, `the build refused figures above the threshold:\n${built.output}`)
})

test('the canonical URL is a read of site.yaml and is written nowhere else', async (t) => {
  const canonical = /^canonical_url:\s*(\S+)$/m.exec(repoFile('website/data/site.yaml'))[1]

  const page = builtPage(buildWith(t, () => {}))
  assert.match(page, new RegExp(`<link rel="canonical" href="${canonical}/probe/"`), 'the canonical URL is not the origin site.yaml names, with the page\'s own path')

  // Change the fact and the built page follows it: the URL is read, not typed.
  const moved = builtPage(
    buildWith(t, (copy) => {
      const file = path.join(copy.site, 'data', 'site.yaml')
      writeFileSync(file, readFileSync(file, 'utf8').replace(/^canonical_url:.*$/m, 'canonical_url: https://elsewhere.example'))
    }),
  )
  assert.match(moved, /<link rel="canonical" href="https:\/\/elsewhere\.example\/probe\/"/, 'the canonical URL did not follow site.yaml')

  // And it is written down in exactly one place: the layout's own source carries the
  // origin of neither the site nor the repository, so W2.2's domain change is that one
  // line and a rebuild rather than a search through the templates.
  const layout = repoFile('website/src/layouts/Base.astro')
  const origin = new URL(canonical).host
  const repository = new URL(/^repository:\s*(\S+)$/m.exec(repoFile('website/data/site.yaml'))[1]).host
  for (const host of [origin, repository]) {
    assert.ok(!layout.includes(host), `the layout hard-codes ${host}, which is a fact of site.yaml`)
  }

  // The one absolute URL the layout is allowed to carry is the analytics origin, which is
  // the single third-party exception the owner decided on (D93, D106) and is admitted by
  // W1.11's origins check rather than by site.yaml's host list. Everything else it prints
  // is composed from a fact.
  const urls = new Set(layout.match(/https?:\/\/[^\s"'`)<]+/g) ?? [])
  assert.deepEqual([...urls], ['https://www.googletagmanager.com'], 'the layout carries an absolute URL that is not the analytics origin')
})

test('the analytics tag is absent while the flag is off', async (t) => {
  const built = buildWith(t, (copy) => {
    const file = path.join(copy.site, 'data', 'site.yaml')
    const before = readFileSync(file, 'utf8')
    const after = before.replace('  enabled: true', '  enabled: false').replace(/  measurement_id: ".*"/, '  measurement_id: ""')
    assert.notEqual(after, before, 'the fixture did not clear the analytics flag, so this test would pass on anything')
    writeFileSync(file, after)
  })
  const page = builtPage(built)
  assert.ok(!page.includes('googletagmanager'), 'the built page names the analytics origin while the flag is off')
  assert.ok(!page.includes('dataLayer'), 'the built page names the analytics layer while the flag is off')
  assert.ok(!page.includes('data-analytics-origin'), 'the built page carries the gate’s configuration while the flag is off')
})

test('with the flag on, the layout emits no fetch of its own: the origin is the banner’s configuration and the reader is the site’s one file', async (t) => {
  // The shipped build IS the flag-on state since W3.2, so this reads the copy as it stands.
  const built = buildWith(t, () => {})
  const page = builtPage(built)

  // What the head asks the browser to fetch, before a single script runs: nothing from
  // Google. No `script[src]` naming the origin, no preconnect, no dns-prefetch, no preload.
  const head = page.slice(page.indexOf('<head'), page.indexOf('</head>'))
  assert.ok(!/googletagmanager/.test(head), 'the layout fetches the analytics origin from the head, before any consent can be given')
  assert.ok(!/<script(?![^>]*\bsrc=)[^>]*>/i.test(head), 'the layout emits an inline script in the head: the site ships one script file and no inline block (D183, W3.2)')

  // The origin reaches the page exactly once, as the consent banner's own configuration
  // attribute, and the file that reads it is the site's one script.
  const carriers = [...page.matchAll(/<[^>]*data-analytics-origin="([^"]*)"[^>]*>/g)]
  assert.equal(carriers.length, 1, `the built page carries ${carriers.length} analytics configurations; the layout writes one, on the consent banner`)
  assert.equal(carriers[0][1], 'https://www.googletagmanager.com', 'the layout names an origin other than the one D106 admits')
  assert.match(carriers[0][0], /id="consent-banner"/, 'the layout puts the analytics configuration somewhere other than the consent banner the visitor answers')
  assert.match(page, /<script src="\/scripts\/site\.js" defer><\/script>/, 'the layout does not load the site’s one script, which is what reads the configuration above')
})

test('the reduced-motion base is in the built page', async (t) => {
  const built = buildWith(t, () => {})
  const page = builtPage(built)
  assert.match(page, /@media \(prefers-reduced-motion:\s*reduce\)/, 'the built page carries no reduced-motion rule')

  // The rule neutralises the durations of everything, which is what makes it a base
  // rather than a list: a component written later cannot forget it (D105, step 3).
  const rule = /@media \(prefers-reduced-motion:\s*reduce\)\{(.*?)\}\}/s.exec(page)
  assert.ok(rule, 'the reduced-motion rule has no body')
  assert.match(rule[1], /animation-duration:1ms!important/)
  assert.match(rule[1], /transition-duration:1ms!important/)
  assert.match(rule[1], /animation-iteration-count:1!important/)
})

test('the head points at the files the build ships, and at nothing else', async (t) => {
  const built = buildWith(t, () => {})
  const page = builtPage(built)

  // No sitemap integration is installed, so no sitemap is produced, so no page points at
  // one: a hint that 404s is the download-link defect in a smaller size (Global
  // Constraint 9).
  assert.ok(!page.includes('rel="sitemap"'), 'the page points at a sitemap the build does not produce')

  // A font the build ships is preloaded, and one it does not ship is not.
  const withFont = buildWith(t, (copy) => {
    mkdirSync(path.join(copy.site, 'public', 'fonts'), { recursive: true })
    writeFileSync(path.join(copy.site, 'public', 'fonts', 'fugaro.woff2'), 'not really a font')
  })
  assert.equal(withFont.status, 0, `the build failed with a font in public/fonts:\n${withFont.output}`)
  assert.match(builtPage(withFont), /<link rel="preload" as="font" type="font\/woff2" href="\/fonts\/fugaro\.woff2" crossorigin/)
})

test('a comment that mentions the sitemap package does not make the head point at a sitemap', async (t) => {
  // The config is a module: only an integration it declares produces a sitemap, and a
  // comment (or a string) that names the package is not one.
  const built = buildWith(t, (copy) => {
    const file = path.join(copy.site, 'astro.config.mjs')
    writeFileSync(file, `// @astrojs/sitemap is not installed, and a comment saying its name is not an integration.\n${readFileSync(file, 'utf8')}`)
  })

  assert.ok(!builtPage(built).includes('rel="sitemap"'), 'the page points at a sitemap the build does not produce')
})

test("the page description is README.md's own subline", async (t) => {
  const built = buildWith(t, () => {})
  const page = builtPage(built)

  // The **second** line under README.md's `# Fugaro` title — the sentence under the
  // headline — read here from the README rather than from the build: the site's promise is
  // the README's, and it is not a second copy of it (F48, D102). The changes spec of
  // 2026-10-18 (item 2) puts the subline in the description and not the headline, which is
  // four words with no verb in them; this test is the named test change that moved with it.
  // The README's markdown emphasis is not part of the sentence.
  const readme = repoFile('README.md').split('\n')
  const title = readme.findIndex((line) => line.trim() === '# Fugaro')
  const subline = readme
    .slice(title + 1)
    .filter((line) => line.trim() !== '')[1]
    .trim()
    .replace(/^\*\*(.+)\*\*$/, '$1')
    .trim()

  assert.ok(
    page.includes(`<meta name="description" content="${subline}"`),
    `the page's description is not the README's subline ${JSON.stringify(subline)}: the description is the sentence under the headline, and a page that describes itself with the headline tells a search engine four words (changes spec item 2)`,
  )
})
