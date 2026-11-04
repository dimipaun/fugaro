// website/test/console.mjs — the console page (website plan, Task W1.6).
//
// The spec asks for "Console downloads: per-OS download links (placeholder until the
// console ships)", and the console has shipped: `console/` holds the application, its
// packaging, and the workflow that signs, notarises and staples its macOS artifacts, the
// release `console-v0.110.0` carries them together with the unsigned Linux pair, `site.yaml`'s
// `features.console_downloads` is true
// and the data file's `available` is true with it. The page's Downloads section renders that
// release, and the placeholder state it renders instead is a state a copy of the repository
// is built into rather than the state this checkout ships (W3.1). Nine things are checked
// here:
//
//   1. **The released section renders the release's own downloads.** One link per published
//      asset, held to the repository, the release path, the tag and the **name the release
//      serves** one part at a time (`test/lib/console-downloads.mjs`), with the checksum the
//      data file carries beside each — and the data file's own sentence about the shipped
//      console rather than a copy of it written into the markup.
//   2. **The link carries the served name, not the name the build wrote.** GitHub rewrites a
//      space in an uploaded asset's name to a dot, so the release's own files spell
//      `Fugaro Console-<version>-universal.dmg` and the release serves, and the site must link
//      to, `Fugaro.Console-<version>-universal.dmg`: the case below refuses any href carrying
//      `%20`, which is the 404 every reader would have followed (`servedName` has the rule).
//   3. **The unreleased state carries no link, and it is a fixture.** With
//      `features.console_downloads` flipped back to false and the data file's `available`
//      with it, the built section contains no download URL and no element a reader can
//      follow — no `<a>`, no `<button>`. That state is not the checkout's own any more, so
//      the case builds it in a copy of the repository: the refusal stays alive without the
//      site claiming the console is unreleased (D95, Global Constraint 9).
//   4. **The page renders its data file and types nothing of its own.** Every sentence in
//      the built page's article appears in `website/content/downloads.json` — the
//      mechanical form of the rule this site is written to, "a page renders a fact; it
//      never restates one" (F37, Global Constraint 6).
//   5. **Every claim is traceable to a decision.** Each sentence in the data file names the
//      document it came from, that document is in this repository, and every decision id
//      the source names is a decision that document actually carries (W1.6 step 3) — so a
//      claim about the console cannot be written without provenance.
//   6. **The rule the Go test enforces over the tree is wired into the build.** A download
//      URL in the data file fails the build here, naming the file, in either state: the
//      URL is derived from the release's published names and is never written down
//      (Global Constraint 9).
//   7. **A checksum the release does not publish fails the build.** The data file's `sha256`
//      values are held to the release's own `console-sha256sums.txt` (copied verbatim into
//      `website/data/`), so a wrong value is a red build rather than a page printing a
//      number no reader's download can match — and the case plants a well-formed wrong one,
//      because the shape alone cannot tell the two apart.
//   8. **A platform block renders only when the data file carries one, and its links are the
//      file's own assets.** `downloads.json` carries an optional `linux` block — the owner's
//      ruling of 2026-10-20 put a Linux build in this repository, unsigned, and the console's
//      release now publishes it (D10) — and the section renders its sentence, its commands and
//      its assets *under* it, with nothing of it above; while it is not there, no built page
//      carries a word, a link or a command of it. A copy of the repository with the block taken
//      out is the fixture for that second state, so the refusal stays alive without the site
//      claiming the platform is missing. A block naming an asset the data file does not list
//      fails the build: a link can only appear for an asset the file names.
//   9. **Every download link the built site carries is an asset the data file names.** The
//      gate over the whole built site, and not only over this page: each `releases/download/`
//      href any page carries has to name one of the assets the release's own documents list,
//      held part by part (`assertReleaseURL`) — so a link added anywhere, for any platform,
//      fails until the asset is in the data file and in the release's own sums file.
//
// How it builds: the negative cases mutate a file and build again, so the builds happen in
// a copy of the repository in a temp directory rather than in this checkout (the shape
// `layout.mjs` uses for the same reason). The copy is the repository minus everything a
// site build cannot read, with the installed `node_modules` symlinked rather than copied,
// and every build is network-free: the only executable it starts is the site's own
// installed Astro.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { READ_UNDER_PLUGIN, linkNodeModules } from './lib/build.mjs'
import { controlTargetFailures } from './lib/controls.mjs'
import { builtPages, find, parse, textOf, walk } from './lib/html.mjs'
import {
  CONSOLE_README,
  DOWNLOADS_JSON,
  RELEASE_WORKFLOW,
  SITE_YAML,
  SUMS_FILE,
  assertReleaseURL,
  consoleTag,
  downloadNeedle,
  downloadsData,
  publishedAssets,
  publishedDownloads,
  releaseSums,
  renderedVerifyCommands,
  repoFile,
  repository,
  servedName,
  workflowAssetRule,
  workflowTagPrefix,
  workflowTagShape,
} from './lib/console-downloads.mjs'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)

// What a site build cannot read, and therefore what the copies below leave out: the Go
// module's own tree and test data, the console application, and the installed and built
// directories (the last two are symlinked and rebuilt instead).
const SKIP = new Set(['.git', 'internal', 'cmd', 'console', 'schemas', 'deploy', 'testdata'])
const SKIP_UNDER_WEBSITE = new Set(['node_modules', 'dist', '.astro'])

/**
 * One decision row of the console's design, as that document writes it: the row is the
 * decision, and a claim that cites `D2` is citing this line and not a remembered summary
 * of it. Every check below reads the row rather than a copy of it, so a decision that is
 * reworded fails a test instead of silently disagreeing with the page.
 */
function decisionRow(id) {
  const design = repoFile('docs/design/desktop-console.md')
  const row = new RegExp(`^\\|\\s*\\*\\*${id}\\*\\*\\s*\\|.*$`, 'm').exec(design)
  assert.ok(row, `docs/design/desktop-console.md carries no ${id} row, and the console's page cites it`)
  return row[0]
}

/** A copy of the repository in a temp directory, ready to be mutated and built. */
function copyOfTheRepository() {
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-console-'))
  const root = path.join(dir, 'repo')
  cpSync(REPO, root, {
    recursive: true,
    filter: (src) => {
      const name = path.basename(src)
      const parent = path.basename(path.dirname(src))
      if (path.dirname(src) === REPO && SKIP.has(name)) return false
      // Which parts of `plugin/` a build reads, and why each one: READ_UNDER_PLUGIN's
      // own comment (website/test/lib/build.mjs).
      if (parent === 'plugin' && !READ_UNDER_PLUGIN.has(name)) return false
      if (parent === 'website' && SKIP_UNDER_WEBSITE.has(name)) return false
      return true
    },
  })
  linkNodeModules(path.join(root, 'website', 'node_modules'))
  return { dir, root }
}

/** Build the copy, and hand back what the build said and what it produced. */
function build(root) {
  const site = path.join(root, 'website')
  const result = spawnSync(process.execPath, [path.join(site, 'node_modules', 'astro', 'bin', 'astro.mjs'), 'build'], {
    cwd: site,
    encoding: 'utf8',
    env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' },
  })
  const dist = path.join(site, 'dist')
  const page = path.join(dist, 'console', 'index.html')
  return {
    status: result.status,
    output: `${result.stdout ?? ''}${result.stderr ?? ''}`,
    page: existsSync(page) ? readFileSync(page, 'utf8') : null,
    // The whole built artifact, because one gate below is about every page rather than about
    // this one: a download link can be added anywhere, and only a scan of the site sees it.
    dist,
  }
}

/** Build a fresh copy, mutate one or more files, build again, and clean up after the test. */
function buildWith(t, mutate) {
  const copy = copyOfTheRepository()
  t.after(() => rmSync(copy.dir, { recursive: true, force: true }))
  mutate(copy)
  return build(copy.root)
}

/** The built page, for a test that expects the build to have succeeded. */
function builtPage(built) {
  assert.equal(built.status, 0, `the build failed:\n${built.output}`)
  assert.ok(built.page, 'the build produced no console page')
  return built.page
}

/** The built page's downloads section: the element the placeholder rule is about. */
function downloadsSection(page) {
  const m = /<section[^>]*id="downloads"[\s\S]*?<\/section>/.exec(page)
  assert.ok(m, 'the built page carries no downloads section')
  return m[0]
}

/**
 * The section's per-platform block (`<div class="downloads-linux">`), or null: what is inside
 * it, up to the list it ends with, and the element's own opening tag matched loosely because
 * Astro's scoping attribute (`data-astro-cid-…`) may or may not be on it.
 */
function platformBlock(section) {
  return /<div class="downloads-linux"[^>]*>([\s\S]*?)<\/ul>\s*<\/div>/.exec(section)?.[1] ?? null
}

/**
 * Whether an element carries a class, by token: `class="card download-card"` carries `card`
 * and not `download-card-…`. Matched by substring instead, `download-card` would also match
 * the platform heading's own `download-card-platform` and the cards would be read twice.
 */
function hasClass(el, name) {
  return (el.attrs.get('class') ?? '').split(/\s+/).includes(name)
}

/** The links one piece of markup carries, as `{ href, label }` in document order. */
function linksIn(html) {
  return [...html.matchAll(/<a[^>]*href="([^"]+)"[^>]*>([^<]*)<\/a>/g)].map((match) => ({
    href: match[1],
    label: match[2].trim(),
  }))
}

/**
 * Every href of a built page that names this repository's release-download path, with the page
 * it is on: the raw material of the gate over the whole built site. `downloadNeedle` is the
 * repository's own host and path, so a link to somebody else's release is not this gate's
 * business.
 */
function downloadLinksIn(pages) {
  const needle = downloadNeedle()
  const out = []
  for (const page of pages) {
    for (const [, href] of page.html.matchAll(/href="([^"]+)"/g)) {
      if (href.includes(needle)) out.push({ page: page.rel, href })
    }
  }
  return out
}

/**
 * Item 9's gate: **every** download link a built page carries names an asset the release's own
 * documents list, held to the release part by part (`assertReleaseURL`). It is the site-wide half
 * of the checks above, which each read one page or one section — a link added anywhere, on a page
 * nobody thought of, is the one no per-page case sees. The asset is the href's last segment
 * (percent-decoded, because a served name may carry a dot but an href is a URL), and it has to be
 * one of the names `publishedDownloads` derives from the release's own account of what it ships;
 * only then is the whole URL held to the repository, the release path, the tag and that name.
 *
 * Returns the links it checked, so the caller can assert the scan found something: a gate over an
 * empty set of pages passes without checking anything.
 */
function assertEveryDownloadLinkIsAListedAsset(pages, data = downloadsData()) {
  const known = publishedDownloads(data)
  const found = downloadLinksIn(pages)
  for (const link of found) {
    const segment = link.href.slice(link.href.lastIndexOf('/') + 1)
    let name = segment
    try {
      name = decodeURIComponent(segment)
    } catch {
      // Left as written: an undecodable segment is not a name the release serves, and the
      // assertion below is where that is said.
    }
    const download = known.find((entry) => entry.name === name)
    assert.ok(
      download,
      `${link.page} links ${link.href}, and ${DOWNLOADS_JSON} names no asset ${JSON.stringify(name)}: every download link the site carries is an asset the release publishes, and a link for one the data file does not carry is a button no check here can see`,
    )
    assertReleaseURL(link.href, download.name, data)
  }
  return found
}

/**
 * The page's TEXT as a reader sees it: Astro escapes the characters below, and a test that
 * compared escaped text with the data file would fail on an apostrophe rather than on a
 * claim.
 *
 * **Tags are left alone**, and that is what keeps the reader ahead of the page: an attribute
 * may legitimately carry an escaped `&quot;` of its own — the slider's screenshot `alt` texts
 * do, quoting the console's own status line — and decoding inside a tag would hand the strict
 * reader a document whose attribute value ends at the wrong quote. What a text comparison is
 * about is the text between the tags, so that is the half this decodes.
 */
function decode(html) {
  return html
    .split(/(<[^>]*>)/)
    .map((part) =>
      part.startsWith('<')
        ? part
        : part
            .replace(/&#39;/g, "'")
            .replace(/&quot;/g, '"')
            .replace(/&lt;/g, '<')
            .replace(/&gt;/g, '>')
            .replace(/&#8212;/g, '—')
            .replace(/&amp;/g, '&'),
    )
    .join('')
}

test('the console page renders the facts its data file carries', async (t) => {
  const built = buildWith(t, () => {})
  const page = decode(builtPage(built))
  const data = downloadsData()

  // Every sentence rendered on the page is the data file's own: the name, what the console
  // is, and each property with its title.
  //
  // The heading is read as a **document** and not as a string, because the redesign draws the
  // name in two tones: `website/src/pages/console.astro` splits it the way the home page's
  // headline is split, so the `h1` carries a class and a `<span class="gradient-text">` around
  // its last words. What has to hold is the thing the string comparison was really about — the
  // headline a reader reads is the data file's name, and the words in the gradient are the
  // name's own last two rather than a second copy of it with markup in the middle (D102).
  const doc = parse(page)
  const h1 = find(doc, 'h1')
  assert.ok(h1, 'the built console page carries no h1')
  assert.equal(
    textOf(h1).replace(/\s+/g, ' ').trim(),
    data.name,
    `the page's heading is not ${JSON.stringify(data.name)}, the name the data file carries`,
  )
  const gradient = [...walk(h1)].find((el) => hasClass(el, 'gradient-text'))
  assert.ok(gradient, 'the console page\'s headline carries no element of class "gradient-text", so the board\'s two-tone name is not drawn')
  assert.equal(
    textOf(gradient).trim(),
    data.name.split(/\s+/).filter(Boolean).slice(-2).join(' '),
    `the gradient carries ${JSON.stringify(textOf(gradient).trim())}, and the split is the name's own last two words: a headline whose halves are written down separately is a second copy of the name, and it is the copy that drifts`,
  )
  assert.ok(page.includes(data.what.text), `the page does not render what.text ${JSON.stringify(data.what.text)}`)
  for (const property of data.properties) {
    assert.ok(page.includes(property.text), `the page does not render the property "${property.id}"`)
    assert.ok(page.includes(property.title), `the page does not render the title of the property "${property.id}"`)
  }

  // And the section that offers the release renders the data file's own released sentence
  // rather than a copy of it written into the markup — and not the sentence the unreleased
  // state renders, which is now a state this checkout is not in.
  assert.ok(
    downloadsSection(page).includes(data.released.text),
    `the downloads section does not render released.text ${JSON.stringify(data.released.text)}`,
  )
  assert.ok(
    !page.includes(data.not_released.text),
    `the page still renders not_released.text ${JSON.stringify(data.not_released.text)} with the console released`,
  )

  // And the hero, in the state the checkout ships: the release's own tag and the platform its
  // first published asset is built for in the pill, and the action that offers that release.
  // Both are the data file's facts rather than this page's words — and the case below is the
  // other half of this gate, where the console is unreleased and a data file that still lists
  // its assets must draw neither. The pair is what makes the hero's release facts a reading of
  // the release rather than of the list.
  const hero = articleOf(page)
  assert.ok(
    hero.includes(consoleTag(data)),
    `the hero does not render the release tag ${JSON.stringify(consoleTag(data))} while the console has shipped`,
  )
  assert.ok(
    hero.includes(`Download for ${data.downloads[0].platform}`),
    `the hero does not offer "Download for ${data.downloads[0].platform}", the platform of the release's first published asset`,
  )
})

test('the downloads section carries no link and no download URL while the flag is false', async (t) => {
  // The state the console was in until W3.1, and the state the section has to keep rendering
  // honestly if the flag is ever turned back off: **built for macOS and not published**.
  // "Built" is not a claim taken on trust — the application and the workflow that signs,
  // notarises and staples its `.dmg` and `.zip` are in this repository — and the site is not
  // in that state any more, so this case BUILDS it: the checkout's own flag and `available`
  // are the released pair, and the copy below flips both back. That is what keeps the refusal
  // alive now that `npm run check` no longer builds the unreleased site (W3.1, D95).
  assert.ok(
    existsSync(path.join(REPO, 'console', 'package.json')),
    'console/ holds no application, so "the console is built" is not what this repository says',
  )
  assert.ok(
    existsSync(path.join(REPO, RELEASE_WORKFLOW)),
    `${RELEASE_WORKFLOW} does not exist, and the console's release workflow is what makes "built" a statement about this repository rather than a design`,
  )
  // The premise of the fixture: the checkout ships the RELEASED pair, so flipping both is
  // what makes the unreleased state, and a checkout that still said "unreleased" would make
  // this case assert nothing about the state it names.
  assert.match(
    repoFile(SITE_YAML),
    /^\s*console_downloads:\s*true\s*$/m,
    `${SITE_YAML} does not have features.console_downloads true, and this case's fixture is the unreleased state it flips back to`,
  )
  assert.equal(
    downloadsData().available,
    true,
    `${DOWNLOADS_JSON} does not say the console is available, and this case's fixture is the unreleased state it flips back to`,
  )

  const built = buildWith(t, (copy) => {
    const yaml = path.join(copy.root, 'website', 'data', 'site.yaml')
    writeFileSync(yaml, readFileSync(yaml, 'utf8').replace(/^(\s*console_downloads:\s*)true\s*$/m, '$1false'))
    const file = path.join(copy.root, 'website', 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.available = false
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
  const page = builtPage(built)

  const needle = downloadNeedle()
  assert.ok(
    !page.includes(needle),
    'console page links releases/download/ while features.console_downloads is false',
  )

  // And nothing a reader can follow at all: the placeholder is prose, and the two elements
  // that would make it actionable are absent from the section (D95).
  const section = downloadsSection(page)
  assert.ok(!/<a[\s>]/.test(section), 'the downloads section carries a link while the console is unreleased')
  assert.ok(!/<button[\s>]/.test(section), 'the downloads section carries a button while the console is unreleased')
  assert.ok(
    section.includes(downloadsData().not_released.text),
    'the downloads section does not render the data file\'s unreleased sentence while the flag is false',
  )

  // **And the hero says nothing about a release either.** It is the page's other half of the
  // same state: the pill names the release's tag and platform and the filled action offers the
  // download, and both are claims about a release this repository says has not shipped. The
  // fixture is the shape that makes the reading worth something — the data file still LISTS the
  // assets (a release's published names arrive before the release does, which is the state this
  // file was in while the console was being built), so a hero that read a platform off the list
  // without asking whether there is a release would draw the pill here. It draws the CLI link
  // and the lead instead, which are not claims about a release.
  const hero = articleOf(page)
  const data = downloadsData()
  assert.ok(
    data.downloads.length > 0,
    `${DOWNLOADS_JSON} lists no download, and this case's premise is a page whose data file lists the assets of a release that has not shipped`,
  )
  assert.ok(
    !hero.includes(consoleTag(data)),
    `the hero renders the release tag ${JSON.stringify(consoleTag(data))} while the console is unreleased: the pill is a claim about a release, and the data file naming an asset is not one`,
  )
  assert.ok(
    !/Download for/.test(hero),
    'the hero offers a download while the console is unreleased',
  )
  assert.ok(hero.includes('Install the CLI first'), 'the hero dropped its CLI link along with the download action')
  assert.ok(hero.includes(data.what.text), 'the hero dropped the data file\'s sentence about the console')
})

/** One built console page's `<article>`: the page's own content, without the layout around it. */
function articleOf(page) {
  const m = /<article[^>]*>([\s\S]*?)<\/article>/.exec(page)
  assert.ok(m, 'the built page carries no article')
  return m[1]
}

/**
 * The controls the console page asks a reader to tap, and what each one is: the classes the
 * redesign gave them. The home page's own case calls the same reading with its own controls
 * (`website/test/lib/controls.mjs`), so the owner's one rule is one reading — a control
 * cannot pass here on a declaration that page's reader would refuse.
 */
const CONSOLE_CONTROLS = [
  ['console-cta', "the hero's filled action"],
  ['console-cta-quiet', "the hero's quiet link"],
  ['download-link', 'a download link'],
  ['screen-dot', 'a dot in the screenshot slider'],
  ['screen-arrow', "an arrow in the screenshot slider's bar"],
]

/** The console page's own stylesheet, as the 44 px reading takes it: every `<style>` it ships. */
function builtStylesheet(built) {
  const css = [...walk(parse(decode(builtPage(built))))]
    .filter((el) => el.name === 'style')
    .map((el) => el.raw ?? '')
    .join('\n')
  assert.ok(
    css.includes('.console-cta'),
    'the built page ships no stylesheet of its own, so this case would be reading nothing',
  )
  return css
}

test('every control the console page asks a reader to tap is at least 44 px tall', async (t) => {
  const built = buildWith(t, () => {})
  const failures = controlTargetFailures(builtStylesheet(built), CONSOLE_CONTROLS)
  assert.deepEqual(
    failures,
    [],
    `a control on the console page is under the 44 px target the owner's brief of 2026-10-19 measures every control against:\n  ${failures.join('\n  ')}`,
  )
})

test('a console control under the target is refused, wherever its minimum is missing or out of force', async (t) => {
  // The passing case above proves what the page is, not what the reading refuses: a reading
  // that looked for the wrong property, or that stopped seeing a rule, would pass it and hold
  // nothing. Each plant below is one of the ways a minimum stops holding, and the case names
  // the sentence that has to fire — the same three shapes the home page's plant case uses
  // (`website/test/home.mjs`), because it is the same reading.
  const css = builtStylesheet(buildWith(t, () => {}))
  const ruleOf = (klass) => new RegExp(`\\.${klass}(?![\\w-])[^{}]*\\{[^}]*\\}`)
  const withoutMinimum = (klass) =>
    css.replace(ruleOf(klass), (rule) => rule.replace(/min-height\s*:\s*[^;}]+;?/, ''))
  const cases = [
    [
      "the minimum taken off the download link's rule",
      withoutMinimum('download-link'),
      /a download link is no declared target/,
    ],
    [
      'a slider dot declared under the target',
      css.replace(ruleOf('screen-dot'), (rule) => rule.replace('min-height:44px', 'min-height:20px')),
      /a dot in the screenshot slider is a 20 px target/,
    ],
    [
      "the hero's filled action's minimum moved into a media query, where it holds at one width and not at every one",
      `${withoutMinimum('console-cta')}@media (min-width:48rem){.console-cta{min-height:56px}}`,
      /the hero's filled action is no declared target/,
    ],
  ]
  for (const [what, planted, expected] of cases) {
    assert.notEqual(planted, css, `${what} left the stylesheet as it is, so the plant is not a plant`)
    const failures = controlTargetFailures(planted, CONSOLE_CONTROLS)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused by the sentence that names it:\n  ${failures.join('\n  ') || '(nothing refused)'}`,
    )
  }
})

// ---- the cards the owner read on 2026-10-21 --------------------------------------------
//
// Three things the owner asked of the downloads section, all of them about the stylesheet the
// cards are laid out by and none of them about the facts the section renders (those are held
// above):
//
//   1. the platform cards "should fit in ONE row on desktop (wrap only on narrow screens)";
//   2. "the cards should also keep equal heights";
//   3. and they should "not show the long checksum text breaking awkwardly (keep the
//      checksum, wrap it cleanly with word-break and a monospace smaller size)".
//
// None of the three is visible in the markup: a grid that wrapped because its columns ran out
// of room, a row of cards of three different heights and a 64-character hash breaking wherever
// the line happens to end are all the same HTML. So the reading is the built page's own
// stylesheet — the rules that decide the layout — and each case below is one of the three,
// with the plants that prove the reading refuses the state it names. **What the cards look like
// is still the owner's check** (70a-70c, P9): this is the rule, not the picture.

/**
 * The console page's stylesheet as scopes: the top level under `''`, and one entry per
 * `@media` block with the condition it is under. Brace-matched rather than regexed, because a
 * declaration's own text may hold braces (a `var()` fallback, a `minmax()`) and a media block
 * is where the one-row rule lives.
 */
function cssScopes(css) {
  const scopes = []
  let rest = ''
  let at = css.indexOf('@media')
  while (at !== -1) {
    rest += css.slice(0, at)
    const open = css.indexOf('{', at)
    let depth = 1
    let i = open + 1
    while (i < css.length && depth > 0) {
      if (css[i] === '{') depth++
      else if (css[i] === '}') depth--
      i++
    }
    scopes.push({ condition: css.slice(at + '@media'.length, open).trim(), body: css.slice(open + 1, i - 1) })
    css = css.slice(i)
    at = css.indexOf('@media')
  }
  scopes.push({ condition: '', body: rest + css })
  return scopes
}

/** A CSS text with every space taken out: the built stylesheet is minified, and the two
 * readings below look for declarations rather than for spacing. */
function tight(text) {
  return text.replace(/\s+/g, '')
}

/** Whether `klass`'s own rule in `scope` declares `declaration` (`grid-auto-flow:column`). */
function declares(scope, klass, declaration) {
  return new RegExp(`\\.${klass}(?![\\w-])[^{}]*\\{[^}]*${declaration}[^}]*\\}`).test(tight(scope))
}

/** Every way the cards' layout is not what the owner asked for on 2026-10-21. */
function cardLayoutFailures(css) {
  const out = []
  const scopes = cssScopes(css)
  const top = scopes.find((scope) => scope.condition === '').body
  // A minimum-width block, as the build's minifier writes it: `@media (min-width: 60rem)`
  // in the source, `@media (width>=60rem)` in the artifact. Both spellings are the same
  // query, and the reading takes either rather than holding the artifact to the source's.
  const wide = scopes.filter((scope) => /min-width\s*:|width\s*>=/.test(scope.condition))

  // 1. The base rule wraps, and the wide block is one row.
  if (!declares(top, 'download-cards', 'grid-template-columns:repeat\\(auto-fit,minmax\\(')) {
    out.push(
      "the download cards' rule declares no `repeat(auto-fit, minmax(…, 1fr))` grid, so a narrow viewport has no shape to wrap the cards in: the owner's feedback of 2026-10-21 is that they wrap on a narrow screen and stand in one row on a desktop",
    )
  }
  const oneRow = wide.filter(
    (scope) => declares(scope.body, 'download-cards', 'grid-auto-flow:column') && declares(scope.body, 'download-cards', 'grid-auto-columns:minmax\\(0,1fr\\)'),
  )
  if (oneRow.length === 0) {
    out.push(
      'no minimum-width block lays the download cards out as one row of equal columns (`grid-auto-flow: column` with `grid-auto-columns: minmax(0, 1fr)`): the owner\'s feedback of 2026-10-21 is that the platform cards "should fit in ONE row on desktop (wrap only on narrow screens)", and the base rule above is the wrap',
    )
  }
  if (wide.some((scope) => declares(scope.body, 'download-cards', 'grid-template-columns:repeat\\(auto-fit'))) {
    out.push(
      "a minimum-width block keeps the auto-fit grid the base rule declares, so a desktop wide enough for the cards' minimum columns is what decides whether they are one row: the one-row rule is this block's to make, not the window's",
    )
  }

  // 2. Equal heights: the grid stretches its items, so a row of cards is one height.
  if (!declares(top, 'download-cards', 'align-items:stretch')) {
    out.push(
      'the download cards\' grid does not stretch its items (`align-items: stretch`), so a row of cards is as tall as each card\'s own text: the owner\'s feedback of 2026-10-21 is that the cards "keep equal heights"',
    )
  }

  // 3. The checksum: kept, monospace, one step smaller than the running text, and broken
  //    anywhere rather than left to widen its card.
  const tokens = new Map([...css.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;}]+)/g)].map((m) => [m[1], m[2].trim()]))
  const px = (value) => {
    const named = /^var\((--[a-z0-9-]+)\)$/.exec((value ?? '').trim())
    const resolved = named ? (tokens.get(named[1]) ?? '') : (value ?? '')
    const m = /^([\d.]+)(rem|px)$/.exec(resolved.trim())
    return m ? Number(m[1]) * (m[2] === 'rem' ? 16 : 1) : null
  }
  const shaRule = /\.download-sha256(?![\w-])[^{}]*\{([^}]*)\}/.exec(tight(top))
  if (!shaRule) {
    out.push('the stylesheet carries no rule for a download\'s checksum (`.download-sha256`), so a 64-character hash is laid out by whatever rule it inherits')
    return out
  }
  const sha = shaRule[1]
  if (!/word-break:(break-all|anywhere)/.test(sha)) {
    out.push(
      "a download's checksum declares no `word-break`, so a 64-character hash with no space in it widens its card or runs out of it: the owner's feedback of 2026-10-21 is to keep the checksum and wrap it cleanly",
    )
  }
  if (!/font-family:var\(--font-mono\)/.test(sha)) {
    out.push("a download's checksum is not drawn in the monospace stack, and it is the one run of text on the page a reader compares character by character")
  }
  const size = px(/font-size:([^;}]+)/.exec(sha)?.[1])
  const body = px(tokens.get('--font-size-base'))
  if (size === null || body === null) {
    out.push("a download's checksum declares no readable font size, so its size is whatever it inherits")
  } else if (size >= body) {
    out.push(
      `a download's checksum is ${size} px against the ${body} px of the page's running text, and the owner's feedback of 2026-10-21 asks for a smaller monospace size: a hash at the running text's size is the awkward block of characters the cards were read for`,
    )
  }
  return out
}

test('the download cards are one row on a desktop, equal in height, and their checksums wrap', async (t) => {
  const failures = cardLayoutFailures(builtStylesheet(buildWith(t, () => {})))
  assert.deepEqual(
    failures,
    [],
    `the download cards are not laid out the way the owner read them on 2026-10-21:\n  ${failures.join('\n  ')}`,
  )
})

test('a card layout the owner did not ask for is refused, rule by rule', async (t) => {
  // The case above proves what the page is, not what the reading refuses. Each plant below is
  // one of the three things the owner asked for, taken back out of the stylesheet the way a
  // later round would take it out.
  const css = builtStylesheet(buildWith(t, () => {}))
  assert.match(css, /grid-auto-flow/, 'the built stylesheet declares no one-row rule at all, so the plants below would prove nothing')
  const cases = [
    [
      'the one-row block deleted, leaving the cards to wrap wherever the columns run out',
      css.replace(/@media[^{]*\{\s*\.download-cards[^{}]*\{[^}]*\}\s*\}/, ''),
      /no minimum-width block lays the download cards out as one row/,
    ],
    [
      // The plant edits the cards' own block rather than the file's first
      // `grid-auto-flow: column`: the page ships every component's rules as one stylesheet and
      // the console's screenshot track declares the same one-row property, so a plant that took
      // the first occurrence would edit the track's rule and the reading would rightly refuse
      // nothing.
      'the one-row block keeping the base rule\'s auto-fit grid',
      css.replace(
        /(@media[^{]*\{\s*\.download-cards[^{}]*\{[^}]*?)grid-template-columns:none/,
        '$1grid-template-columns:repeat(auto-fit,minmax(17rem,1fr))',
      ),
      /a minimum-width block keeps the auto-fit grid/,
    ],
    [
      'the cards allowed to be as tall as their own text',
      css.replace(/align-items:\s*stretch/, 'align-items: start'),
      /does not stretch its items/,
    ],
    [
      'a checksum that stops breaking inside itself',
      css.replace(/word-break:\s*(break-all|anywhere)/, 'overflow-wrap: normal'),
      /declares no `word-break`/,
    ],
    [
      'a checksum drawn in the running text\'s own size',
      css.replace(/\.download-sha256([^{}]*)\{([^}]*)\}/, (rule) => rule.replace(/font-size:[^;}]+/, 'font-size:var(--font-size-base)')),
      /against the \d+ px of the page's running text/,
    ],
    [
      'a checksum in the page\'s sans rather than in the monospace stack',
      css.replace(/\.download-sha256([^{}]*)\{([^}]*)\}/, (rule) => rule.replace(/font-family:[^;}]+/, 'font-family:var(--font-sans)')),
      /not drawn in the monospace stack/,
    ],
  ]
  for (const [what, planted, expected] of cases) {
    assert.notEqual(planted, css, `${what} left the stylesheet as it is, so the plant is not a plant`)
    const failures = cardLayoutFailures(planted)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused by the sentence that names it:\n  ${failures.join('\n  ') || '(nothing refused)'}`,
    )
  }
})

test("the page names the console's read-only and no-telemetry properties", async (t) => {
  const built = buildWith(t, () => {})
  const page = decode(builtPage(built))
  const data = downloadsData()

  // The two properties the console's own decisions fix and a reader is owed (D2, F17, D5):
  // it writes nothing, and it sends nothing.
  const ids = data.properties.map((property) => property.id)
  for (const id of ['read-only', 'no-telemetry']) {
    assert.ok(ids.includes(id), `the console's data file states no "${id}" property, and the page states both (W1.6 step 3)`)
  }

  assert.match(page, /read-only/i, 'the page does not say the console is read-only')
  assert.match(page, /no telemetry/i, 'the page does not say the console has no telemetry')
})

test('every console claim is traceable to the document it came from', () => {
  const data = downloadsData()

  // Every sentence the page renders carries a source, and the source is a document in this
  // repository that is about the console. A claim with no provenance is the failure W1.6
  // step 3 exists to prevent, and this is the mechanical form of it.
  const claims = [data.what, data.released, data.not_released, ...data.properties]
  assert.ok(claims.length >= 3, 'the console\'s data file carries too few claims to be the page\'s facts')

  const decisions = new Set()
  for (const claim of claims) {
    assert.ok(typeof claim.source === 'string' && claim.source.trim() !== '', `a claim with no source: ${JSON.stringify(claim.text)}`)

    const [file, ...named] = claim.source.trim().split(/\s+/)
    const absolute = path.join(REPO, file)
    assert.ok(file.startsWith('docs/'), `${JSON.stringify(claim.source)} does not name a document under docs/`)
    assert.ok(existsSync(absolute), `${JSON.stringify(claim.source)} names ${file}, which is not in this repository`)

    const text = readFileSync(absolute, 'utf8')
    assert.match(text, /console/i, `${file} is cited as the source of a console claim and does not mention the console`)

    // A decision id in a source has to be a decision that document actually carries: the
    // page cites D2, D5 and D11, and each of them is a row in the console's own design.
    for (const id of named) {
      assert.match(id, /^D\d+[a-z]?$/, `${JSON.stringify(claim.source)} names ${id}, which is not a decision id`)
      assert.ok(
        new RegExp(`\\|\\s*\\*\\*${id}\\*\\*\\s*\\|`).test(text),
        `${JSON.stringify(claim.source)} cites ${id}, and ${file} carries no ${id} row — a citation of a decision that does not exist is a claim with no source at all`,
      )
      decisions.add(id)
    }
  }

  for (const id of ['D2', 'D5', 'D11']) {
    assert.ok(
      decisions.has(id),
      `no console claim cites ${id}: the page states that the console is read-only (D2), that it has no telemetry (D5) and that it is its own artifact (D11), and each of those is one of those decisions`,
    )
  }

  // The not-released claim is the one whose source has to **state** it. `not_released.text` is
  // the console's own state, and the review of this branch found it citing `docs/release.md` —
  // a document that describes how the console's release is cut and never that there is none.
  // The lines about the console are read one by one, so a sentence elsewhere in the file (a
  // refusal message about something else, say) cannot vouch for the claim.
  const stateSource = data.not_released.source.trim().split(/\s+/)[0]
  const aboutTheConsole = repoFile(stateSource)
    .split('\n')
    .filter((line) => /console/i.test(line))
  assert.ok(
    aboutTheConsole.some((line) => /not released|has not been published|unpublished/i.test(line)),
    `${DOWNLOADS_JSON}'s not_released claim cites ${stateSource}, and none of its ${aboutTheConsole.length} line(s) about the console says the console is not released: the claim is the console's state, so the document cited for it has to state it`,
  )

  // The released claim is the same kind of claim about the same console, and its source has
  // to carry the two things the page now promises about the shipped artifact: that it is
  // signed and notarised, and that the CLI comes first because the CLI is the data plane.
  // A page that promises a notarised download, or a console that runs without the CLI, is a
  // page whose claim outran the document it cites.
  const releasedSource = data.released.source.trim().split(/\s+/)[0]
  const releasedIds = data.released.source.trim().split(/\s+/).slice(1)
  assert.ok(
    releasedIds.length > 0,
    `${DOWNLOADS_JSON}'s released claim cites ${JSON.stringify(data.released.source)} with no decision in it, and the claim's two promises are decisions: signing and notarisation (D4) and the CLI as the data plane (D1)`,
  )
  assert.ok(
    releasedIds.some((id) => /notaris/i.test(decisionRow(id))),
    `the released claim promises a signed and notarised artifact and cites ${releasedIds.join(', ')}, and none of those rows in docs/design/desktop-console.md says the console's artifact is notarised: a page that promises a notarised download has to cite the row that decides it`,
  )
  assert.ok(
    repoFile(releasedSource)
      .split('\n')
      .filter((line) => /console/i.test(line))
      .some((line) => /data plane/i.test(line)),
    `the released claim says the CLI is the data plane, and ${releasedSource} carries no line about the console that says so`,
  )
})

test("the console's guarantees are stated as the document that decides them states them", () => {
  // Traceability is not enough on its own: a sentence can cite a decision and still
  // misstate it. The two guarantees the page publishes are read out of the document that
  // decides them here, part by part — the defect this catches is a property that drops one
  // of a decision's parts and thereby promises more than the product does.
  const design = repoFile('docs/design/desktop-console.md')
  const data = downloadsData()

  const property = (id) => {
    const found = data.properties.find((entry) => entry.id === id)
    assert.ok(found, `the console's data file states no "${id}" property`)
    return found
  }

  // --- Strictly read-only (D2) ------------------------------------------------
  // D2 defers the three commands that write, and it names them: the console is read-only
  // because those commands stay in the CLI, not because the console is careful.
  const d2 = decisionRow('D2')
  assert.match(d2, /read-only/i, 'the design\'s D2 row no longer decides that the console is read-only; re-read D2 before restating it')
  const readOnly = property('read-only')
  assert.match(readOnly.text, /read-only/i, 'the read-only property does not say the console is read-only')
  for (const command of ['kill', 'resume', 'cancel']) {
    assert.ok(d2.includes(command), `the design's D2 row no longer names \`${command}\`, so the property that defers it can no longer be checked against it`)
    assert.ok(
      readOnly.text.includes(command),
      `the read-only property does not name \`${command}\`, which D2 defers to the CLI: a guarantee that names some of the commands that write and not the others is a narrower promise than the design makes`,
    )
  }

  // --- No telemetry (D5, and the design's section 7.4) ------------------------
  // Four promises in one sentence, and the page may not drop one of them. The update check
  // is the one that is easy to lose — an earlier revision of this property said the only
  // outbound requests were the CLI's own, which reads as "nothing else leaves the machine"
  // and is false: the console updates itself **on by default** (D5, section 7.3), so that
  // check is an outbound request of its own and has to be named.
  const promised = /No telemetry[^.]*\./.exec(design)
  assert.ok(promised, "the design's section 7.4 no longer carries the console's telemetry promise; re-read it before the page states one")
  assert.match(decisionRow('D5'), /On by default/, 'the design\'s D5 row no longer puts the update check on by default, which is why the check has to appear in the promise the page copies')

  const noTelemetry = property('no-telemetry')
  for (const part of ['telemetry', 'crash reporting', 'analytics', 'update check']) {
    assert.ok(promised[0].includes(part), `the design no longer promises "${part}" in section 7.4, and the page repeats that sentence's parts`)
    assert.ok(
      noTelemetry.text.includes(part),
      `the no-telemetry property does not name "${part}": the console's promise has four parts (telemetry, crash reporting, analytics and the update check's identifier) and a property that drops one promises more than the design does`,
    )
  }
  // The one flow there is, and it is the person's to turn on. Both sentences have to say so:
  // a property that repeats the promise and stops is a promise the design no longer makes,
  // because the console reports crashes to Sentry once the switch is on (the owner's ruling
  // of 2026-10-31, and the design's section 7.4). The switch and the host are the two facts a
  // reader acts on, so the design and the property are held to both rather than to the four
  // parts above alone.
  assert.match(
    promised[0],
    /opt[- ]in/i,
    `the design's section 7.4 promise does not say the crash reporting is an opt-in: ${JSON.stringify(promised[0])}`,
  )
  assert.match(
    noTelemetry.text,
    /opt[- ]in/i,
    `the no-telemetry property ${JSON.stringify(noTelemetry.text)} does not say the crash reporting is the person's own opt-in, and the design's sentence does`,
  )
  for (const [what, text] of [
    ["the design's section 7.4", design],
    ['the no-telemetry property', noTelemetry.text],
  ]) {
    for (const part of ['Sentry', 'European Union']) {
      assert.ok(
        text.includes(part),
        `${what} does not name ${part}: the crash reports go to Sentry, hosted in the EU, and a statement of the flow that withholds where it goes is not the design's sentence`,
      )
    }
  }

  // And where the property enumerates what does leave the machine, the update check has to
  // be listed among those things — the precise shape of the defect above. "Names the update
  // check somewhere" is not the rule and does not catch it: the defective sentence did name
  // the check, twice, and then enumerated the outbound requests as the CLI's own and
  // stopped. The rule is where the mention falls: if the property says "only outbound
  // requests", an update check has to be named *in that enumeration*, after it.
  const enumerated = noTelemetry.text.indexOf('only outbound requests')
  if (enumerated >= 0) {
    assert.ok(
      noTelemetry.text.indexOf('update check', enumerated) > enumerated,
      `the no-telemetry property ${JSON.stringify(noTelemetry.text)} names the CLI's requests as the only outbound ones without listing the update check among them: the console updates itself on by default (D5, section 7.3), that check fetches its own feed, and a property that stops at the CLI promises more than the design does`,
    )
  }

  // --- What the console is (D1, and the design's section 1) -------------------
  // One sentence of the design's own, and the page carries its parts.
  const isWhat = /It has no [^.]*\./.exec(design)
  assert.ok(isWhat, "the design's section 1 no longer says what the console holds; re-read it before the page repeats it")
  for (const part of ['no credential', 'no database client']) {
    assert.ok(isWhat[0].includes(part), `the design's section 1 no longer says the console has "${part}"`)
    assert.ok(
      data.what.text.includes(part),
      `what.text does not carry "${part}", and ${JSON.stringify(isWhat[0])} is where the page's sentence comes from`,
    )
  }
})

test('the release the download URLs are built from is one this repository has released', () => {
  const data = downloadsData()
  const { release } = data

  // The tag is the console's own, `console-vX.Y.Z`, and the PREFIX is the release workflow's,
  // read twice out of that file: the `CONSOLE_TAG="console-v$VERSION"` line that cuts the tag,
  // and the literal `console-vX.Y.Z` its header prints. Holding the prefix to those two is what
  // makes these assertions able to fail: the line this replaces compared the composed tag with
  // `console-v` and the version, both of which the data file had just supplied, so only a wrong
  // literal in the test itself could have failed it. The document the data file names as the
  // block's source is held to the same tag, because that is what it is cited for.
  assert.equal(
    release.tag_prefix,
    workflowTagPrefix(),
    `${DOWNLOADS_JSON}'s release.tag_prefix is not the prefix ${RELEASE_WORKFLOW} composes the console's tag with`,
  )
  assert.equal(
    `${release.tag_prefix}X.Y.Z`,
    workflowTagShape(),
    `${DOWNLOADS_JSON}'s release.tag_prefix is not the prefix ${RELEASE_WORKFLOW} prints the console's tag with`,
  )
  assert.match(
    repoFile(release.source.split(/\s+/)[0]),
    /console-vX\.Y\.Z/,
    `${release.source} no longer documents the console's own release tag, and ${DOWNLOADS_JSON}'s URLs are built from it`,
  )

  // The shapes the release's own job accepts, read out of that job rather than remembered:
  // it takes exactly one `.dmg` and one `.zip`, and its `case "$f" in *"-$VERSION-"*` guard
  // refuses any asset not named for the version. This comes **before** the names below,
  // because those are held to `console/README.md`'s documented ones: an asset in a shape the
  // release does not upload is otherwise reported as a drift between the data file and the
  // README rather than as what it is — a name no release will ever publish.
  const rule = workflowAssetRule()
  assert.deepEqual(
    rule.extensions,
    ['AppImage', 'deb', 'dmg', 'zip'],
    `${RELEASE_WORKFLOW} no longer takes one .dmg, one .zip, one .AppImage and one .deb (it names ${rule.extensions.map((extension) => `.${extension}`).join(', ')}), and the data file's assets are those four`,
  )
  assert.ok(
    rule.versioned,
    `${RELEASE_WORKFLOW} no longer refuses an asset that is not named for the version (\`case "$f" in *"-$VERSION-"*\`), and the data file's names carry the version`,
  )
  for (const entry of data.downloads) {
    const extension = entry.file.slice(entry.file.lastIndexOf('.') + 1)
    assert.ok(
      rule.extensions.includes(extension),
      `${DOWNLOADS_JSON}'s ${entry.id} names a .${extension} file, and the release uploads ${rule.extensions.map((value) => `.${value}`).join(', ')}`,
    )
    assert.ok(
      entry.file.includes('-{version}-'),
      `${DOWNLOADS_JSON}'s ${entry.id} is ${JSON.stringify(entry.file)}, which does not carry the version between dashes, and the release refuses an asset not named for the version`,
    )
  }

  // And the names are the release's own too: `console/README.md` prints what the packaging
  // step produces, and the data file's `file` entries are those names with `{version}` where
  // the version goes. A data file that drifted from the release puts a 404 behind every
  // button on the day the flag is flipped, and nothing in the build would notice.
  assert.deepEqual(
    data.downloads.map((entry) => entry.file.split('{version}').join(release.version)),
    publishedAssets(data),
    `${DOWNLOADS_JSON}'s asset names are not the names ${CONSOLE_README} documents for the release`,
  )

  // And each asset carries the SHA-256 of the bytes the release published: the sums file the
  // release publishes beside the two artifacts is `console-sha256sums.txt`, and the page
  // prints each value beside its link so a reader can check what arrived against what was
  // published. Two things are held here, and the second is the one that catches a wrong
  // value rather than a malformed one:
  //
  //   - the shape: 64 lowercase hex digits, one per asset, no two assets sharing one. A value
  //     that is not a checksum is one `shasum -a 256 -c` can never match, and a value copied
  //     from the other asset makes the page print a checksum the reader's own download fails
  //     against;
  //   - the value itself, against the release's own list of them: `SUMS_FILE` is the
  //     `console-sha256sums.txt` asset of the `console-vX.Y.Z` release, copied verbatim into
  //     `website/data/` when the site's console downloads are turned on, and every entry's
  //     checksum has to be what it records for the entry's published name. This is the check
  //     that makes a wrong checksum a red build: a well-formed value is not evidence of
  //     anything, and no build can compute the published one from anything in this tree. The
  //     reader refuses a disagreement by name (`src/lib/console-downloads.mjs`), and the case
  //     after this one plants one and requires that refusal.
  const sums = releaseSums()
  for (const entry of data.downloads) {
    assert.match(
      entry.sha256,
      /^[0-9a-f]{64}$/,
      `${DOWNLOADS_JSON}'s ${entry.id} carries ${JSON.stringify(entry.sha256)} as its sha256, and the checksum a reader checks a download against is 64 lowercase hex digits`,
    )
    const published = entry.file.split('{version}').join(release.version)
    assert.equal(
      sums.get(published),
      entry.sha256,
      `${DOWNLOADS_JSON}'s ${entry.id} carries ${JSON.stringify(entry.sha256)} and ${SUMS_FILE} records ${JSON.stringify(sums.get(published))} for ${JSON.stringify(published)}: the release's own sums file is what the page's checksum is checked against, and a value that disagrees with it fails for every reader who checks their download`,
    )
  }
  assert.equal(
    new Set(data.downloads.map((entry) => entry.sha256)).size,
    data.downloads.length,
    `${DOWNLOADS_JSON}'s assets do not each carry their own sha256 (${data.downloads.map((entry) => entry.sha256).join(', ')}): two assets with one checksum is one asset's checksum printed beside the other, and a reader who checked that download would be checking it against the wrong bytes`,
  )
  // And the release publishes nothing the site leaves out: a reader who wants an asset the
  // release carries and the page does not offer has nowhere to get it. The one exception is the
  // console's update feed, `latest-mac.yml`: the release job sums it into this file so the one
  // cosign signature covers it, but electron-updater fetches it and no reader downloads it.
  // Leaving its line in keeps the file byte for byte the signed one.
  assert.deepEqual(
    [...sums.keys()].filter((name) => name !== 'latest-mac.yml').sort(),
    data.downloads.map((entry) => entry.file.split('{version}').join(release.version)).sort(),
    `${SUMS_FILE} and ${DOWNLOADS_JSON} name different sets of assets: the site offers the release's assets, and an asset the release carries and the page does not is a download a reader cannot reach`,
  )

  // And the version is one this repository has released: since the console's own release stream
  // (docs/releases/console/README.md, the owner's addendum of 2026-10-22) the console has its own
  // tag, its own version line and its own notes, and the workflow refuses to build without
  // `docs/releases/console/vX.Y.Z.md` — it reads that file from the tagged commit and passes it as
  // the whole release body. So a version with no notes file is a version the console was not
  // released at, and one the site has no business offering a download for.
  //
  // **What this cannot prove, and nothing offline can: that `console-vX.Y.Z` is a release that
  // exists on GitHub.** A git tag cannot be read without a fetch, and a shallow CI checkout has
  // none. What W3.1 added is the offline half of that claim, and the Go boundary test is where
  // it lives: the data file's release block has to name the version of a console release whose
  // notes file is in the tree, and every asset has to be an asset the release's own sums file
  // records (`TestWebsiteConsoleReleaseFactsAreComplete`). What remains the owner's check is the
  // release page itself: the tag exists, its assets are the ones the site names, and the
  // checksums the page prints are the ones the release published.
  const notes = `docs/releases/console/v${release.version}.md`
  assert.ok(
    existsSync(path.join(REPO, notes)),
    `${DOWNLOADS_JSON}'s release.version is ${release.version} and ${notes} is not in this repository: the console's own release is cut with its notes file, and the workflow refuses to build without it`,
  )
})

test('the build refuses a download URL in the data file, in the state this checkout ships', async (t) => {
  // The rule W0.2's Go test enforces over the whole tree, wired into the build (W1.6 step
  // 2): a URL written into the data file stops the build here, naming the file. The URL is
  // synthetic, and the refusal does not depend on the flag: since the owner's item 6 the URLs
  // are DERIVED — the data file carries the release's published names and nothing else — so
  // this holds in the shipped state as it held in the unreleased one, and it is the state
  // this checkout is in that the build below mutates. Both shapes the URL can arrive in are
  // refused: as a `url` key of its own, and as a `file` that is a URL rather than a published
  // name. Either shape is a file that has stopped being the file the derivation reads.
  const url = 'https://downloads.example.invalid/console-macos.tar.gz'

  const asKey = buildWith(t, (copy) => {
    const file = path.join(copy.root, 'website', 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.downloads = [{ id: 'macos', platform: 'macOS', url }]
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
  assert.notEqual(asKey.status, 0, `the build accepted a download URL:\n${asKey.output}`)
  assert.match(asKey.output, /downloads\.json/, 'the failure does not name the data file the URL came from')
  assert.match(asKey.output, /carries the key "url"/, 'the failure does not say which key it refused')
  assert.match(asKey.output, /derived/, 'the failure does not say where a download URL comes from instead')
  assert.equal(asKey.page, null, 'the build wrote a page carrying a download URL the data file is not allowed to hold')

  const asFile = buildWith(t, (copy) => {
    const file = path.join(copy.root, 'website', 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.downloads[0].file = url
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
  assert.notEqual(asFile.status, 0, `the build accepted a URL in the file field:\n${asFile.output}`)
  assert.match(asFile.output, /downloads\.json/, 'the failure does not name the data file the URL came from')
  assert.match(asFile.output, /\.file is "/, 'the failure does not name the field the URL was written into')
  assert.equal(asFile.page, null, 'the build wrote a page whose download link came from a URL rather than from the release it names')
})

test("the build refuses a checksum that is not the release's own", async (t) => {
  // The checksum is the one value on the page a build cannot compute: it is a fact about bytes,
  // and the release's own `console-sha256sums.txt` is its only source. So the reader holds the
  // value against that file, and this is the case that proves the holding is real — the
  // mutation is a WELL-FORMED checksum (64 lowercase hex digits, a real one's shape) with one
  // character changed, which no shape check can tell from the right one. The build refuses it
  // by name, in both directions the release's file can disagree with the data file: a value
  // that is not the one it records, and an asset name it does not record at all.
  const entry = downloadsData().downloads[0]
  const wrong = `${entry.sha256.slice(0, -1)}${entry.sha256.endsWith('0') ? '1' : '0'}`
  assert.notEqual(wrong, entry.sha256, 'the mutation below is not a mutation: the planted checksum is the published one')

  const built = buildWith(t, (copy) => {
    const file = path.join(copy.root, 'website', 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.downloads[0].sha256 = wrong
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
  assert.notEqual(
    built.status,
    0,
    `the build rendered ${wrong} as the checksum of ${entry.id}, which is not the value the release published:\n${built.output}`,
  )
  assert.match(built.output, /downloads\.json/, 'the failure does not name the data file the checksum came from')
  assert.match(
    built.output,
    /console-sha256sums\.txt/,
    "the failure does not name the release's own sums file, and that file is what the value was checked against rather than the value's shape",
  )
  assert.equal(built.page, null, 'the build wrote a page printing a checksum the release does not publish')

  // The other half of the same fact: an asset name the release's file does not carry. The name
  // is the URL's last segment, so a name the release does not publish is a download that 404s,
  // and the release's sums file is where that is written down.
  const unpublished = buildWith(t, (copy) => {
    const file = path.join(copy.root, 'website', 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.downloads[0].file = 'Fugaro Console-{version}-universal.tar.gz'
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
  assert.notEqual(
    unpublished.status,
    0,
    `the build rendered a download for an asset the release does not publish:\n${unpublished.output}`,
  )
  assert.match(
    unpublished.output,
    /console-sha256sums\.txt/,
    "the failure does not name the release's own sums file, which is where the assets the release publishes are recorded",
  )
})

test('the build refuses a published name the data file spells out in full', async (t) => {
  // Every entry of `downloads` is the name the release publishes *for the version in the same
  // file*, so the file carries the literal `{version}` and the reader substitutes the version
  // it read from `release`. The defect the rule exists for is an entry frozen at one version:
  // it passes every other check in the reader — the checksums in this mutation still agree
  // with the release's own sums file, because the name below is the one that file records — so
  // nothing but the placeholder check tells it from a correct entry, and a file that carries
  // one renders last release's artifact under this release's tag at the next bump.
  const entry = downloadsData().downloads[0]
  const built = buildWith(t, (copy) => {
    const file = path.join(copy.root, 'website', 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.downloads[0].file = entry.file.split('{version}').join(data.release.version)
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })

  assert.notEqual(
    built.status,
    0,
    `the build accepted a published name written out in full rather than left as the file's placeholder:\n${built.output}`,
  )
  assert.match(built.output, /downloads\.json/, 'the failure does not name the data file the entry came from')
  assert.match(built.output, /downloads\[0\]\.file/, 'the failure does not name the entry of the file it refused')
  assert.match(
    built.output,
    /\{version\}/,
    'the failure does not name the placeholder the entry has to carry: a name frozen at one version is the thing the rule refuses',
  )
  assert.equal(built.page, null, 'the build wrote a page from a data file that names its assets by a version rather than by the release')
})

test('the build refuses a tag prefix the release workflow does not compose', async (t) => {
  // The one thing between the data file's names and the artifact a reader downloads is the
  // release's tag: every URL is
  // `<repository>/releases/download/<release.tag_prefix><release.version>/<name>`, and the
  // prefix is composed by the release workflow (`CONSOLE_TAG="<prefix>$VERSION"`). The reader
  // checks the prefix's SHAPE, and a shape is not the workflow's answer — so a prefix of the
  // right shape that no run composes passes every other check here (the checksums match, the
  // names are the release's, `console/README.md` documents them) and puts a 404 behind every
  // button. The mutation below is that prefix, planted in the workflow itself, which is what
  // makes the case a check of the build's reading rather than of a literal in this file.
  const planted = 'console-desktop-v'
  const built = buildWith(t, (copy) => {
    const file = path.join(copy.root, ...RELEASE_WORKFLOW.split('/'))
    const workflow = readFileSync(file, 'utf8')
    assert.ok(
      workflow.includes('CONSOLE_TAG="console-v$VERSION"'),
      `${RELEASE_WORKFLOW} no longer composes the console's tag as \`CONSOLE_TAG="console-v$VERSION"\`, so this case cannot plant the disagreement it is about`,
    )
    writeFileSync(file, workflow.replace('CONSOLE_TAG="console-v$VERSION"', `CONSOLE_TAG="${planted}$VERSION"`))
  })

  assert.notEqual(
    built.status,
    0,
    `the build accepted release.tag_prefix ${JSON.stringify('console-v')} while ${RELEASE_WORKFLOW} composed the tag with ${JSON.stringify(planted)}: the URL it renders names a tag no run creates, and every download link on the page is a 404 for the reader who follows it:\n${built.output}`,
  )
  assert.match(built.output, /downloads\.json/, 'the failure does not name the data file the prefix came from')
  assert.match(built.output, /console-release\.yml/, 'the failure does not name the workflow whose own line the prefix is checked against')
  assert.equal(built.page, null, 'the build wrote a page whose download links name a tag no run creates')
})

test('with the console released, the section renders the downloads the release publishes', async (t) => {
  // W3.1's state, which is the state this checkout ships: the flag in `site.yaml` and the
  // data file's own `available` are both true, and that pair was the WHOLE change. Nothing in
  // the page changed and no URL is written anywhere: the section's links are derived from the
  // release's published names, so the flip and a version bump move every button (the owner's
  // changes spec of 2026-10-18, item 6). The build below is the checkout's own — the copy is
  // the builder the negative cases use, with nothing mutated — because the unreleased state
  // is now the fixture rather than the shipped one.
  const built = buildWith(t, () => {})

  const page = decode(builtPage(built))
  const expected = publishedDownloads()
  assert.ok(expected.length >= 2, `the data file names ${expected.length} asset(s), which is too few to prove the section renders the release`)

  // The rendered links, in document order, held one at a time to the release's own facts: the
  // repository, the release path, the tag and the published name. `assertReleaseURL` is where
  // that happens, and it is deliberately not a URL composed here — see `test/lib`.
  const links = [...downloadsSection(page).matchAll(/<a[^>]*href="([^"]+)"[^>]*>([^<]*)<\/a>/g)].map((match) => ({
    href: match[1],
    label: match[2].trim(),
  }))
  assert.equal(
    links.length,
    expected.length,
    `the section renders ${links.length} download link(s) with ${expected.length} asset(s) published (${links.map((link) => link.href).join(', ')})`,
  )
  for (const [index, download] of expected.entries()) {
    assertReleaseURL(links[index].href, download.name)
    assert.equal(
      links[index].label,
      download.label,
      `the section offers ${JSON.stringify(links[index].label)} for ${JSON.stringify(download.name)}, and the release's own name for that asset is a ${download.platform} ${download.label.slice(download.platform.length + 1)}`,
    )
  }
  assert.ok(
    !page.includes(downloadsData().not_released.text),
    'the section still renders the not-released sentence with the console released',
  )

  // And the same rule over the whole built site rather than over this section (item 9): every
  // download link ANY built page carries is one of these assets, held to the release part by
  // part. The copy above is already built in the one state that carries links at all, so the
  // gate costs no second build — and the scan is asserted to have found them rather than
  // trusted, because a gate that scanned nothing would pass for the wrong reason.
  const siteLinks = assertEveryDownloadLinkIsAListedAsset(builtPages(built.dist))
  assert.ok(
    siteLinks.length >= expected.length,
    `the built site carries ${siteLinks.length} download link(s) and the release publishes ${expected.length} asset(s): every published asset is offered somewhere a reader can reach it, and a gate over an empty scan is a gate that holds nothing`,
  )
})

test('the site-wide download gate refuses a link the release does not carry', () => {
  // The gate the case above runs is worth exactly the failure it can produce, and no build here
  // can plant that failure: a link for an asset the data file does not name is not a state any
  // file in this repository is in — it is a page that pasted a URL, which is the one thing a
  // per-page case cannot see. So the gate is handed pages of its own, composed from the
  // repository's own facts plus the one thing that is wrong, and each refusal is the assertion.
  const data = downloadsData()
  const base = `${repository().replace(/\/+$/, '')}/releases/download`
  const page = (href) => [{ rel: 'planted/index.html', html: `<a href="${href}">Fugaro Console</a>` }]
  const asset = encodeURIComponent(publishedDownloads(data)[0].name)

  // An asset the release does not publish. The name is the only difference from a real link, so
  // what fails is the name rather than the origin or the tag.
  assert.throws(
    () => assertEveryDownloadLinkIsAListedAsset(page(`${base}/${consoleTag(data)}/${asset}.not-published`), data),
    /names no asset/,
    'the gate accepted a download link for an asset the data file does not name',
  )

  // The release's own asset on another release: the gate holds the URL to the release, not only
  // to the file name.
  assert.throws(
    () => assertEveryDownloadLinkIsAListedAsset(page(`${base}/${data.release.tag_prefix}9.9.9/${asset}`), data),
    /not an asset of the console's own release/,
    'the gate accepted a download link on a release the console has not cut',
  )

  // And it is not refusing everything: the release's own link — the state the case above hands
  // it — passes.
  assert.doesNotThrow(
    () => assertEveryDownloadLinkIsAListedAsset(page(`${base}/${consoleTag(data)}/${asset}`), data),
    "the gate refused a link to one of the release's own assets",
  )
})

test('the links carry the name the release serves, not the name the build wrote', async (t) => {
  // The bug this case exists for is the one no other check here could see, because every one of
  // them was anchored to a document in this repository: the release's own `console-sha256sums.txt`
  // and `console/README.md` both spell the macOS artifacts with a space, GitHub serves them with a
  // dot, and a build that pasted the release's spelling into the URL wrote
  // `.../Fugaro%20Console-<version>-universal.dmg` — a 404 for every reader, with the checksum
  // verification command failing beside it. So this case is about the SERVED name, and the two
  // halves of it are the same fact: the href's last segment is the served name, and no href
  // carries an encoded space at all.
  const built = buildWith(t, () => {})
  const page = decode(builtPage(built))

  const hrefs = [...downloadsSection(page).matchAll(/<a[^>]*href="([^"]+)"/g)].map((match) => match[1])
  assert.ok(hrefs.length >= 2, `the section renders ${hrefs.length} link(s), which is too few to prove the naming rule`)

  for (const [index, download] of publishedDownloads().entries()) {
    // `assertReleaseURL` holds the whole URL to the release's facts, the served name included;
    // these two assertions are the ones whose failure message names the bug rather than the
    // part, so a regression reads as what it is.
    assertReleaseURL(hrefs[index], download.name)
    assert.ok(
      hrefs[index].endsWith(`/${download.name}`),
      `the link ${hrefs[index]} does not end in ${JSON.stringify(download.name)}, the name the release serves; the release's own files spell that asset ${JSON.stringify(download.releaseName)}, and a link spelled that way is a 404`,
    )
    assert.equal(
      /%20/i.exec(hrefs[index]),
      null,
      `the link ${hrefs[index]} carries an encoded space: the release serves a space in an asset's name as a dot, so this href is a 404 for every reader who follows it`,
    )
  }

  // And the served name is a real transformation of the release's own, not a second name the
  // test invented: the two differ by exactly GitHub's rewrite of a space, and an asset whose
  // name carries no space — the Linux pair, which `console/package.json`'s
  // `build.linux.artifactName` writes with a dot already — is served as it is written. What
  // must not hold is that no name is rewritten at all: that would be a build which never
  // applied the rule, which is the 404 this case exists for.
  const rewritten = publishedDownloads().filter((download) => download.releaseName.includes(' '))
  assert.ok(
    rewritten.length > 0,
    `no asset the release publishes is named with a space (${publishedDownloads().map((download) => download.releaseName).join(', ')}), so this case would pass on a build that never applied the served-name rule at all`,
  )
  for (const download of publishedDownloads()) {
    assert.equal(
      servedName(download.releaseName),
      download.name,
      `${download.releaseName}'s served form is not ${JSON.stringify(download.name)}`,
    )
    assert.equal(
      download.name !== download.releaseName,
      download.releaseName.includes(' '),
      `${JSON.stringify(download.releaseName)} is served as ${JSON.stringify(download.name)}, and the only rewrite GitHub applies is a space to a dot: a name with no space in it is served as it is written`,
    )
  }
})

test('the page states nothing its data file does not carry', async (t) => {
  const built = buildWith(t, () => {})
  const page = decode(builtPage(built))
  const data = downloadsData()

  // Every sentence on the page — the article's text, less the short labels (the heading,
  // the section titles, the navigation's words) that are the page's own furniture — appears
  // in the data file. A page that paraphrased a claim, or wrote a new sentence of its own
  // about the console, fails here (F37, Global Constraint 6).
  // The opening tag is matched loosely: the page has a stylesheet of its own now, so Astro
  // scopes its markup with `data-astro-cid-…` and `<article>` is written `<article
  // data-astro-cid-…>` in the artifact (the same reason `downloadsSection` above is loose).
  const article = /<article[^>]*>([\s\S]*?)<\/article>/.exec(page)
  assert.ok(article, 'the built page carries no article')

  const rendered = article[1]
    .replace(/<[^>]+>/g, '\n')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length >= 40)

  assert.ok(rendered.length >= 5, `the page renders ${rendered.length} sentence(s), which is too few to prove anything`)

  const sources = [
    data.name,
    data.what.text,
    data.released.text,
    data.not_released.text,
    ...data.properties.map((property) => property.text),
    // Each asset's own checksum, which the section prints beside its link: a 64-character
    // line a reader reads, so it is a sentence this rule sees.
    ...data.downloads.map((download) => download.sha256),
    // And the platform block's own sentence, for the day the data file carries one: it is the
    // page's sentence as much as the released one is, and a page that paraphrased it would be
    // restating a fact — the rule this case is. Absent, it adds nothing to the list.
    ...(data.linux === undefined ? [] : [data.linux.text]),
    // And the verification commands that block renders, in the form the page carries them:
    // the version is in the data file once and the reader substitutes it, so a command a page
    // prints is a line of text the data file does not literally carry, and it is held here to
    // the same rule — the page renders a fact, and the fact is the file's own command.
    ...renderedVerifyCommands(data),
  ]
  for (const sentence of rendered) {
    assert.ok(
      sources.includes(sentence),
      `the page renders ${JSON.stringify(sentence)}, which ${DOWNLOADS_JSON} does not carry: a page renders a fact; it never restates one`,
    )
  }
})

// ---- The optional platform block (the owner's ruling of 2026-10-20; D10) ------------------
//
// The console's release publishes an unsigned Linux pair beside the macOS one — an AppImage and a
// `.deb` (D10; docs/release.md, "Verifying the console's release") — and the sentence both pages
// render about the console is the macOS one, because signing and notarisation are macOS's facts
// and Linux has neither. So `downloads.json` carries a `linux` block: the sentence a Linux reader
// is owed (what the build is, that it is unsigned, and what proves the bytes), the verification
// steps that are the whole of what an unsigned artifact can be checked with, and the ids of the
// assets it links. The section renders it only while it is there, and its links are held to the
// file's own `downloads` entries. The cases below are the states a checkout can be in: the block
// taken out (a fixture), the block naming something the file does not carry (refused), the block
// this checkout ships (rendered), and the block's own shape.

/** The copy's data file, parsed, and written back by `write` — the fixture's way in. */
function editData(copy, write) {
  const file = path.join(copy.root, 'website', 'content', 'downloads.json')
  const data = JSON.parse(readFileSync(file, 'utf8'))
  write(data)
  writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  return data
}

test('with no Linux block in the data file, no built page carries one', async (t) => {
  // The state the console's data file was in before its Linux artifacts were published, and the
  // state the section has to keep rendering honestly if the block is ever taken back out: the
  // packaging is in `console/`, the data file carries no Linux asset, so the page says what the
  // release it links says — macOS — and carries no other platform's word, link or command. That
  // is "renders only when present", and it is a fixture rather than the shipped state: this
  // checkout carries the block (the release publishes the pair), so the copy below takes it, the
  // two Linux entries and their sums lines back out.
  let fixtureData = null
  const built = buildWith(t, (copy) => {
    fixtureData = editData(copy, (data) => {
      assert.ok(
        data.linux,
        `${DOWNLOADS_JSON} carries no linux block, and this case's fixture is the state it is taken out of`,
      )
      const linuxIds = new Set(data.linux.assets)
      const removed = data.downloads
        .filter((entry) => linuxIds.has(entry.id))
        .map((entry) => entry.file.split('{version}').join(data.release.version))
      assert.ok(
        removed.length > 0,
        `${DOWNLOADS_JSON}'s linux block names no asset the file lists, so this fixture would take nothing out`,
      )
      delete data.linux
      data.downloads = data.downloads.filter((entry) => !linuxIds.has(entry.id))
      // The sentence that state carried: with no Linux asset in the file the section draws the
      // "Not offered" card the release carried then, and a card is only safe under a sentence
      // that names its platform (`Downloads.astro`'s own guard). So the fixture writes the
      // sentence the file carried before the Linux pair was published, and this case stays about
      // the block rather than about the copy.
      data.released.text =
        'The console ships for macOS only: one universal disk image and one archive, signed and notarised by Apple. Install the CLI first, because the CLI is the data plane. Windows and Linux builds do not exist yet.'
      // And the release's own sums file goes with the entries: a line it records that the data
      // file does not offer is a failure of its own, and this fixture is about the block.
      const sums = path.join(copy.root, 'website', 'data', 'console-sha256sums.txt')
      writeFileSync(
        sums,
        readFileSync(sums, 'utf8')
          .split('\n')
          .filter((line) => !removed.some((name) => line.endsWith(`  ${name}`)))
          .join('\n'),
      )
    })
  })
  const page = decode(builtPage(built))

  const section = downloadsSection(page)
  assert.equal(
    platformBlock(section),
    null,
    `the downloads section renders a Linux block while its data file carries none: it renders only while the data file has one (D10)`,
  )

  // **And no card names a platform the data file does not.** The redesign draws a card per
  // platform — the one the release ships for and the ones it does not — so the rule this case
  // has always held ("the page makes no claim about a platform no document of this repository
  // speaks about") is read off the cards the page actually draws rather than as a ban on a list
  // of words: whatever platform name a card carries has to be a word of the data file's own
  // sentences. The file is the fixture's, the one this page was built from: the shipped sentence
  // names Linux in the block this fixture takes out, so the two sentences below are the ones the
  // copy carries — the release this state links, which ships for macOS and for no other system.
  // It is the same rule from the other end, and it is the end that holds a platform nobody here
  // thought to write down.
  const cards = [...walk(parse(page))].filter((el) => hasClass(el, 'download-card-platform'))
  assert.ok(
    cards.length >= 3,
    `the section draws ${cards.length} platform card(s), and this case is about the three the fixture's state makes: the platform the release ships for and the two it does not`,
  )
  const claims = [fixtureData.released.text, fixtureData.not_released.text]
  for (const card of cards) {
    const platform = textOf(card).trim()
    assert.ok(
      claims.some((claim) => claim.includes(platform)),
      `the section draws a card for ${JSON.stringify(platform)} and no sentence of ${DOWNLOADS_JSON} names it: a card is a claim about a platform, and a claim no document of this repository makes is the one this case exists to refuse`,
    )
  }

  // And the page names no artifact format of a platform whose artifacts it does not have: an
  // AppImage and a `.deb` are what a Linux build is, and a page that named them while the data
  // file carries no Linux asset would be describing a download that is not there (D10).
  for (const word of ['AppImage', '.deb']) {
    assert.ok(
      !page.includes(word),
      `the page carries ${JSON.stringify(word)} and ${DOWNLOADS_JSON} says nothing about it: the page's sentence about the platform the release carries is the only one it makes`,
    )
  }
})

test('a Linux block naming an asset the data file does not carry fails the build', async (t) => {
  // **The gate.** A download link may only appear for an asset the data file names, and the only
  // place a link can be introduced is this block: its `assets` are ids of the file's own
  // `downloads` entries, each of which has already been held to the release's published name and
  // to the checksum the release's own sums file records. So the block below — a second
  // architecture's artifacts, which no release carries — is refused by the build rather than
  // rendered as two links that 404.
  const built = buildWith(t, (copy) => {
    editData(copy, (data) => {
      data.linux = {
        text: 'The console also builds for Linux on arm64: an AppImage and a .deb, and neither is code-signed.',
        assets: ['linux-arm64-appimage', 'linux-arm64-deb'],
        source: 'docs/design/desktop-console.md D10',
      }
    })
  })

  assert.notEqual(built.status, 0, `the build accepted a Linux block naming assets the data file does not carry:\n${built.output}`)
  assert.match(built.output, /downloads\.json/, 'the failure does not name the data file the block came from')
  assert.match(built.output, /linux\.assets\[0\]/, 'the failure does not name the entry of the block it refused')
  assert.match(built.output, /"linux-arm64-appimage"/, 'the failure does not name the id the block links')
  assert.match(
    built.output,
    /downloads lists no entry/,
    'the failure does not say where a block’s links have to come from: the ids of the assets the data file lists',
  )
  assert.equal(built.page, null, 'the build wrote a page linking an asset the data file does not carry')
})

test('the Linux block the data file carries is rendered with its own assets and its verification steps', async (t) => {
  // The state this checkout ships: the console's release publishes the unsigned Linux pair, and
  // `downloads.json` carries the block that says so — the sentence, the verification steps and
  // the two ids. So the section renders the block's own sentence and commands, links the block's
  // assets **under it**, prints their checksums, and leaves them out of the list above: one asset
  // of a release is one row, and a Linux artifact under the macOS sentence is one link under a
  // fact that does not describe it.
  const built = buildWith(t, () => {})
  const page = decode(builtPage(built))
  const data = downloadsData()
  const linux = data.linux
  assert.ok(
    linux,
    `${DOWNLOADS_JSON} carries no linux block, and this case is the state it is present in: the release publishes the AppImage and the .deb (D10)`,
  )
  assert.ok(
    Array.isArray(linux.verify) && linux.verify.length > 0,
    `${DOWNLOADS_JSON}'s linux block carries no verification step, and an unsigned build's bytes are proved by the release's own signed sums file and by nothing else (docs/release.md, "Verifying the console's release")`,
  )

  const section = downloadsSection(page)
  const block = platformBlock(section)
  assert.ok(block, 'the section renders no Linux block while the data file carries one')
  assert.ok(
    block.includes(linux.text),
    `the Linux block does not render the data file's own sentence ${JSON.stringify(linux.text)}`,
  )

  // The steps, in the file's order, with the release's version in them: they are what a reader of
  // an unsigned artifact does instead of checking a signature, which is the one thing the AppImage
  // and the `.deb` cannot be checked for.
  for (const command of renderedVerifyCommands(data)) {
    assert.ok(
      block.includes(command),
      `the Linux block does not render the verification step ${JSON.stringify(command)} that ${DOWNLOADS_JSON} carries: the command is the page's only claim about how these bytes are checked, and a page that dropped it would leave an unsigned download with nothing behind it`,
    )
  }

  // One link per asset the block names, in the file's order, held to the release's own facts: the
  // repository, the release path, the tag and the name the release serves — the same
  // `assertReleaseURL` the macOS rows are held to.
  const links = linksIn(block)
  const expected = publishedDownloads(data).filter((download) => linux.assets.includes(download.id))
  assert.equal(
    links.length,
    expected.length,
    `the block renders ${links.length} link(s) for the ${expected.length} asset(s) it names (${links.map((link) => link.href).join(', ')})`,
  )
  for (const [index, download] of expected.entries()) {
    assertReleaseURL(links[index].href, download.name, data)
    assert.equal(
      links[index].label,
      download.label,
      `the block offers ${JSON.stringify(links[index].label)} for ${JSON.stringify(download.name)}, and a label is the platform and the asset's own extension`,
    )
    const entry = data.downloads.find((candidate) => candidate.id === download.id)
    assert.ok(
      block.includes(entry.sha256),
      `the block does not print the checksum of ${download.name}: the value a reader checks their download against is the one fact the data file carries beside each link`,
    )
  }

  // And the block's assets are not in the list above it: one asset, one row. The list above
  // carries the release's macOS pair, which the block does not name.
  const blockStart = section.search(/<div class="downloads-linux"/)
  assert.ok(blockStart >= 0, 'the section carries no per-platform block to split on')
  const aboveLinks = linksIn(section.slice(0, blockStart))
  assert.equal(
    aboveLinks.length,
    data.downloads.length - expected.length,
    `the list above the block renders ${aboveLinks.length} link(s) and the data file carries ${data.downloads.length} asset(s), ${expected.length} of them the block's`,
  )
  for (const asset of expected) {
    const href = links.find((link) => link.href.endsWith(`/${asset.name}`))
    assert.ok(href, `the block renders no link to ${JSON.stringify(asset.name)}, which it names`)
    assert.equal(
      section.split(href.href).length - 1,
      1,
      `${href.href} appears more than once in the section: the block owns the assets it names`,
    )
  }

  // **Getting Started renders the console's downloads too**, from the same facts, through
  // `ConsoleOption.astro` — the card in the `#install` section. It is the consumer a block can be
  // added to and forgotten in: it renders every entry it was handed, so a Linux asset reached its
  // list under the macOS heading with no sentence behind it until the card learned the same rule.
  // So the assertion below is not about the section: it is about **every built page**, which is
  // the only shape that holds a page the block's author did not think of. A block-owned asset may
  // appear on a page only with the block's own sentence on it, only after that sentence, and only
  // once.
  const pages = builtPages(built.dist)
  const gettingStarted = pages.find((entry) => entry.rel.startsWith('getting-started/'))
  assert.ok(
    gettingStarted,
    `the build carries no Getting Started page, so the card that renders the console's downloads was not scanned (${pages.map((entry) => entry.rel).join(', ')})`,
  )
  assert.ok(
    decode(gettingStarted.html).includes(linux.text),
    `Getting Started does not render the block's own sentence while the data file carries one: the card is the second consumer of the same facts, and an asset of the block in its list is a link under a heading that does not describe it`,
  )
  for (const entry of pages) {
    const html = decode(entry.html)
    for (const asset of expected) {
      const occurrences = html.split(`/${asset.name}`).length - 1
      if (occurrences === 0) continue
      assert.equal(
        occurrences,
        1,
        `${entry.rel} links ${asset.name} ${occurrences} times: a block's asset is one row on a page, under the block that names it`,
      )
      const at = html.indexOf(`/${asset.name}`)
      const sentence = html.indexOf(linux.text)
      assert.ok(
        sentence >= 0,
        `${entry.rel} links ${asset.name} and carries none of the block's own sentence: an asset the block names is rendered with the sentence that says what it is, never under another platform's`,
      )
      assert.ok(
        at > sentence,
        `${entry.rel} links ${asset.name} above the block's own sentence: the block owns the assets it names, and a link before its sentence is one under the facts of whatever sentence is there`,
      )
    }
  }
})

test('a Linux block that is not a claim naming the file’s own assets fails the build', async (t) => {
  // The block's own shape, in the six states the review of it would look for: a key nothing
  // renders, no assets at all, a sentence with no source, one asset named twice, no
  // verification step, and a step that spells the release's version rather than leaving it to
  // the file's placeholder. Each is a build of its own — the reader refuses the first thing it finds and
  // no build can fail twice — and each is the same rule from a different side: the block is
  // the sentence a page renders, the assets it links, the steps that prove those bytes and
  // where the sentence came from, and nothing else.
  const plants = [
    {
      what: 'a key nothing renders',
      block: { text: 'Linux, unsigned.', assets: ['macos-dmg'], source: 'docs/design/desktop-console.md D10', platform: 'Linux' },
      names: [/linux carries the key "platform"/],
    },
    {
      what: 'no assets',
      block: { text: 'Linux, unsigned.', assets: [], source: 'docs/design/desktop-console.md D10' },
      names: [/linux\.assets is not a non-empty list/],
    },
    {
      what: 'a sentence with no source',
      block: { text: 'Linux, unsigned.', assets: ['macos-dmg'] },
      names: [/linux\.source is not written in the file/],
    },
    {
      what: 'one asset named twice',
      block: { text: 'Linux, unsigned.', assets: ['macos-dmg', 'macos-dmg'], source: 'docs/design/desktop-console.md D10' },
      names: [/linux\.assets names one asset twice/],
    },
    {
      what: 'no verification step',
      block: {
        text: 'Linux, unsigned.',
        assets: ['macos-dmg'],
        verify: [],
        source: 'docs/design/desktop-console.md D10',
      },
      names: [/linux\.verify is not a non-empty list/],
    },
    {
      what: 'a step that spells the release’s version',
      block: {
        text: 'Linux, unsigned.',
        assets: ['macos-dmg'],
        // The defect is the version written into the command rather than left to the file's
        // one placeholder: the plant spells the version this checkout's data file carries, so
        // what it plants is the stale step a later bump would leave behind.
        verify: [`gh release download ${consoleTag()} -R dimipaun/fugaro -p console-sha256sums.txt`],
        source: 'docs/design/desktop-console.md D10',
      },
      names: [/linux\.verify\[0\].*spells the release's version/],
    },
  ]

  for (const plant of plants) {
    const built = buildWith(t, (copy) => {
      editData(copy, (data) => {
        data.linux = plant.block
      })
    })
    assert.notEqual(built.status, 0, `the build accepted a Linux block with ${plant.what}:\n${built.output}`)
    assert.match(built.output, /downloads\.json/, `the failure for ${plant.what} does not name the data file`)
    for (const name of plant.names) {
      assert.match(built.output, name, `the failure for ${plant.what} does not say what it refused:\n${built.output}`)
    }
    assert.equal(built.page, null, `the build wrote a page from a Linux block with ${plant.what}`)
  }
})
