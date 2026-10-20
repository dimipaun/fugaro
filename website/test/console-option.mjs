// website/test/console-option.mjs — the console, offered on Getting Started (the owner's
// changes spec of 2026-10-18, item 6).
//
// Getting Started presents installation as two things: the CLI — the install paths
// `install.yaml` carries, each command with its copy control — and the desktop console, whose
// downloads a reader can reach without leaving the page. The acceptance is exactly that: "from
// Getting Started a user can install either the CLI or the console without leaving the page" —
// and, while `features.console_downloads` is false, that the console offers nothing to download
// at all. This checkout ships the flag TRUE (the console's `console-v0.9.0` release turned it
// on), so the unreleased state is one the cases below build a copy of the repository into
// rather than the state `npm run check` builds.
//
// **Where the console's block sits is the owner's draft of 2026-10-19.** The install section
// opens with one card per system `install.yaml` supports, and the console is offered inside the
// card of the system its own data file names on its assets — macOS — rather than beside the
// cards: a reader on Linux is not offered a build for a machine they are not on, and the console
// is findable from the page a reader installs the CLI from. The cards themselves (one per
// supported system, each with the path the file offers it first, each with one icon) are
// `os-cards.mjs`'s; this file is the console's own rules, read inside the card it sits in.
//
// **The homepage's call to action is the other place the flag decides** (the owner's brief of
// 2026-10-19, item 6: the console is findable from the home page's call to action; the
// 2026-10-18 spec's own item 6 is the Getting Started option above, which is not this). The
// hero's row of links gains the way to `/console/` when the flag is true and carries nothing
// about the console when it is false. Both halves are the case below: the flag-on half read
// off the build `npm run check` makes, the flag-off half off the unreleased copy.
//
// Six things are checked here:
//
//   1. **The console sits in the card of the system it ships for, and the CLI is still the
//      CLI.** The install section's cards carry the console's block exactly once, in the card
//      whose system is the one the console's own data file names on its assets, and the CLI's
//      commands are still on the page: a command, in a copy block with a named control. P5's
//      fold claim rests on the section opening with that command, and `fold.mjs` is the gate
//      that holds the ordering; the check here is that moving the console into a card did not
//      take the command away.
//   2. **The unreleased console offers nothing.** No `releases/download/` URL anywhere on the
//      page, and inside the console's block exactly one link — the way to `/console/` — and no
//      button. This is the rule the Go boundary test enforces over the whole tree while the
//      flag is false (Global Constraint 9), read off the page a reader gets — and the page is
//      the flag-off one this case builds, because that is the state the rule is about.
//   3. **The released console offers the release's own assets.** With `site.yaml`'s flag and
//      the data file's `available` both true — the state this checkout ships, so the build is
//      the copy with nothing mutated — the block renders one link per published asset of
//      `console-vX.Y.Z`, at the URL derived from the repository, the tag and the name the
//      release serves — the same URLs the console page's section renders, from the same
//      reader — and the cards' commands are untouched. That last part is the point of deriving
//      the URLs: the flip is the whole change, so it must not move anything else.
//   4. **The homepage offers the console from its call to action only while the flag is on**
//      (the owner's brief of 2026-10-19, item 6). With the flag true — the build `npm run
//      check` makes — the hero's row of links carries one more link, the way to `/console/`,
//      labelled the way Getting Started's block labels the console; with the flag false —
//      the unreleased copy — the row is the two links a reader met before the release and
//      says nothing about the console at all. The two links are the same, in the same order,
//      in both states.
//   5. **The unreleased build passes the gates that read the artifact.** The flag adds the
//      option's own markup and words to a page that sits close to D188's 250-word ceiling, so
//      the artifact gates are run over the flag-off build rather than assumed: the state the
//      site was in before the console's release is held to the same ceilings, and a change
//      that fits only one of the two states is a change that breaks the other.
//   6. **A flag and an `available` that disagree stop the build.** Whichever way round they
//      disagree — the data file claiming the console shipped under a flag that says it has
//      not, or a flag that has promised a release under a data file that still says there is
//      none — the build refuses the pair, naming both files: the two are one fact, and the
//      site may not render one state while the other says the opposite.
//
// The page is read through the same HTML reader the other gates use (`lib/html.mjs`), and the
// console's facts through `lib/console-downloads.mjs`, so this file carries no second spelling
// of a URL. The builds of the unreleased state — and of the flag/`available` disagreement —
// happen in a copy of the repository in a temp directory (`lib/build.mjs`, the shape
// `console.mjs` and `origins.mjs` use) and are network-free.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { DIST, SITE, find, parse, textOf, walk } from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'
import {
  SITE_YAML,
  assertReleaseURL,
  downloadNeedle,
  downloadsData,
  publishedDownloads,
  repoFile,
} from './lib/console-downloads.mjs'

/**
 * The heading of the console's block, which is the one place the page names the console: the
 * component writes it as its own label rather than reading it from the data file, and it is how
 * these cases find the block inside the card it sits in. `h4`, because the block is a section of
 * a card whose `h3` is the system's name (the owner's draft of 2026-10-19).
 */
const CONSOLE_HEADING = 'Desktop console'

/** Getting Started as the gates read it: the page the last `npm run build` wrote. */
function gettingStarted(dist = DIST) {
  return readFileSync(path.join(dist, 'getting-started', 'index.html'), 'utf8')
}

/**
 * The install cards inside `#install`, in document order. Read from the section rather than
 * from the page, because "a reader can install from this page" is a claim about the section the
 * page's own heading opens, and because the console's block sits in one of these.
 */
function installCards(page) {
  const main = find(parse(page), 'main')
  assert.ok(main, 'the built page carries no `main` landmark')
  const section = [...walk(main)].find((el) => el.attrs.get('id') === 'install')
  assert.ok(section, 'Getting Started carries no `#install` section, and installing is what the page is for')
  return [...walk(section)].filter(
    (el) => el.name === 'article' && (el.attrs.get('class') ?? '').split(/\s+/).includes('install-card'),
  )
}

/**
 * The console's block, as the card it sits in and the nodes it renders: its heading — the one
 * thing on the page that names the console, and the block's first element — and everything the
 * component writes under it. The card puts the console last, so the block is the heading and
 * the nodes after it, read out of the card rather than looked up by a class of its own: the
 * component carries no wrapper element, and the built page is the markup the card renders.
 *
 * `null` when no card carries the heading, which is the failure the cases below report rather
 * than an error here.
 */
function consoleBlock(page) {
  const card = installCards(page).find((el) =>
    [...walk(el)].some((node) => node.name === 'h4' && textOf(node).trim() === CONSOLE_HEADING),
  )
  if (card === undefined) return null
  const nodes = [...walk(card)]
  const at = nodes.findIndex((node) => node.name === 'h4' && textOf(node).trim() === CONSOLE_HEADING)
  return { card, nodes: nodes.slice(at) }
}

/** The console's block, with the failure naming what the cards do carry. */
function consoleBlockOrFail(page) {
  const block = consoleBlock(page)
  assert.ok(
    block,
    `no install card on the page carries a "${CONSOLE_HEADING}" heading, so the console is not offered where a reader installs from: the owner's draft of 2026-10-19 puts the console's block in the card of the system it ships for, and the cards the section draws are ${installCards(page).map((el) => JSON.stringify(el.attrs.get('data-platform'))).join(', ') || 'none'}`,
  )
  return block
}

/** Every link in a set of nodes, in document order. */
function linksOf(nodes) {
  return nodes.filter((node) => node.name === 'a')
}

/** Every command in a card, as a reader copies it. */
function commandsWithin(el) {
  return [...walk(el)].filter((node) => node.name === 'pre').map((node) => textOf(node))
}

/** Whether an element sits inside a copy block, which is what makes its command copyable. */
function inCopyBlock(el) {
  for (let node = el.parent; node; node = node.parent) {
    if (node.attrs?.has('data-copy')) return true
  }
  return false
}

/**
 * A copy of the repository with the console UNRELEASED — `site.yaml`'s flag and the data
 * file's `available` both false, which is the state the site was in until W3.1 flipped both
 * in one commit — built, with everything else left as the checkout has it.
 *
 * It is the fixture rather than the shipped state now: the checkout says the console has
 * shipped, so the state whose refusals this file keeps has to be built rather than read
 * (`test/lib/build.mjs`, the mechanism `console.mjs` and `origins.mjs` use for the same
 * reason).
 */
function unreleasedBuild(t) {
  return buildCopy(t, ({ site }) => {
    const yaml = path.join(site, 'data', 'site.yaml')
    writeFileSync(yaml, readFileSync(yaml, 'utf8').replace(/^(\s*console_downloads:\s*)true\s*$/m, '$1false'))
    const file = path.join(site, 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.available = false
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
}

test('Getting Started offers the console in the card of the system it ships for, and the CLI beside it', () => {
  // The console's block is inside the card of the system its own data file names on the assets
  // it publishes — macOS — and there is one such card: a reader on another system is not offered
  // a build for a machine they are not on, and the page does not offer the console twice (the
  // owner's draft of 2026-10-19). `os-cards.mjs` owns the cards themselves; what is read here is
  // where the console's block sits among them.
  const page = gettingStarted()
  const cards = installCards(page)
  assert.ok(cards.length >= 2, `the install section draws ${cards.length} card(s), and the console's block sits in one of the systems the page supports`)

  const platform = [...new Set(downloadsData().downloads.map((download) => download.platform))]
  assert.equal(platform.length, 1, `website/content/downloads.json names ${platform.length} platform(s) on its downloads, and the console ships for one system`)

  const block = consoleBlockOrFail(page)
  const named = [...walk(block.card)].find((el) => el.name === 'h3')
  assert.equal(
    textOf(named).trim(),
    platform[0],
    `the console's block sits in the card for ${JSON.stringify(textOf(named).trim())} and the console's own data file publishes it for ${JSON.stringify(platform[0])}: the block belongs in the card of the system the console ships for (the owner's draft of 2026-10-19)`,
  )
  assert.equal(
    installCards(page).filter((el) => [...walk(el)].some((node) => node.name === 'h4' && textOf(node).trim() === CONSOLE_HEADING)).length,
    1,
    'the console is offered in more than one card, and the page offers it once, with the system it ships for',
  )

  // The CLI is still the CLI: at least one command, each in a copy block with a control a
  // reader can name. `copy.mjs` owns the full control contract; what is checked here is that
  // moving the console into a card did not leave the console as the only thing on the page
  // (the acceptance is that BOTH are installable from here).
  const commands = cards.flatMap((card) => commandsWithin(card))
  assert.ok(commands.length > 0, 'no install card carries a command, so the page no longer installs the CLI')
  for (const card of cards) {
    for (const pre of [...walk(card)].filter((el) => el.name === 'pre')) {
      assert.ok(
        inCopyBlock(pre),
        `a card prints a command with no copy block around it (${JSON.stringify(textOf(pre).slice(0, 40))}), and every install command on this page is the existing path with its copy button`,
      )
    }
  }
})

test('the console option offers nothing to download while the console is unreleased', (t) => {
  // The state the site was in until W3.1, and the state the option has to keep rendering
  // honestly if the flag is ever turned back off. The checkout ships the RELEASED pair, so
  // this case builds the unreleased one in a copy of the repository: `site.yaml`'s flag and
  // the data file's `available` are both flipped back, which is the pair W3.1 flipped
  // together. The Go boundary test scans the whole of `website/` for a download URL while
  // the flag is false, and the page a reader gets in that state is what this case reads.
  assert.match(
    repoFile(SITE_YAML),
    /^\s*console_downloads:\s*true\s*$/m,
    `${SITE_YAML} does not have features.console_downloads true, and this case's fixture is the unreleased state it flips back to`,
  )

  const built = unreleasedBuild(t)
  assert.equal(built.status, 0, `the build of the unreleased site failed:\n${built.output}`)

  const page = gettingStarted(built.dist)
  const needle = downloadNeedle()
  assert.ok(
    !page.includes(needle),
    'Getting Started links releases/download/ while features.console_downloads is false',
  )

  // And inside the card's console block: the data file's own unreleased sentence, one link —
  // the way to the page that says what the console is — and no button at all.
  const block = consoleBlockOrFail(page)
  const note = downloadsData().not_released.short
  assert.ok(
    block.nodes.some((node) => textOf(node).includes(note)),
    `the console's block does not render ${JSON.stringify(note)}, the sentence website/content/downloads.json carries for the unreleased console`,
  )

  const links = linksOf(block.nodes)
  assert.equal(
    links.length,
    1,
    `the console's block carries ${links.length} link(s) while the console is unreleased, and the only one it may carry is the way to the console page (${links.map((el) => el.attrs.get('href')).join(', ')})`,
  )
  assert.equal(links[0].attrs.get('href'), '/console/', 'the console block\'s one link is not the way to the console page')
  assert.equal(textOf(links[0]).trim(), 'Learn more', 'the console block\'s link to the console page is not the "Learn more" the spec asks for')
  assert.ok(
    !block.nodes.some((el) => el.name === 'button'),
    'the console block carries a button while the console is unreleased',
  )
})

test('with the console released, the card\'s block renders the release\'s own assets and moves nothing else', (t) => {
  // The state this checkout ships, read from the build `npm run check` makes: the flag and
  // the data file's `available` are both true, which is the whole of the owner's change.
  const page = gettingStarted()
  const { nodes } = consoleBlockOrFail(page)
  const expected = publishedDownloads()
  assert.ok(expected.length >= 2, `the data file names ${expected.length} asset(s), which is too few to prove the block renders the release`)

  const links = linksOf(nodes)
  assert.equal(
    links.length,
    expected.length + 1,
    `the console's block carries ${links.length} link(s) with ${expected.length} asset(s) published and the way to the console page (${links.map((el) => el.attrs.get('href')).join(', ')})`,
  )
  assert.deepEqual(
    links.map((el) => textOf(el).trim()),
    [...expected.map((download) => download.label), 'Learn more'],
    'the console\'s block does not label each download with the platform and the asset\'s extension, in the order the release lists them, with the way to the console page last',
  )
  // Each download's URL is held to the release's own facts — the repository, the release path,
  // the tag and the published name — rather than compared with a URL composed in the test: see
  // `assertReleaseURL`. (`publishedDownloads` carries no URL for exactly that reason.)
  for (const [index, download] of expected.entries()) {
    assertReleaseURL(links[index].attrs.get('href'), download.name)
  }
  assert.equal(links[links.length - 1].attrs.get('href'), '/console/', 'the console block\'s last link is not the way to the console page')

  // The unreleased sentence is gone, and the way to the console page is not: "what is this
  // thing" is a question the released state answers no better than the unreleased one.
  assert.ok(
    !nodes.some((node) => textOf(node).includes(downloadsData().not_released.short)),
    'the console block still says the console is unreleased with the console released',
  )

  // One reader, two pages: the console page's own section renders each asset at the URL of the
  // release, held to the same facts as the block's links above.
  const consolePage = readFileSync(path.join(DIST, 'console', 'index.html'), 'utf8')
  const section = /<section[^>]*id="downloads"[\s\S]*?<\/section>/.exec(consolePage)
  assert.ok(section, 'the console page carries no downloads section')
  const consoleHrefs = [...section[0].matchAll(/<a[^>]*href="([^"]+)"[^>]*>/g)].map((match) => match[1])
  assert.deepEqual(
    consoleHrefs,
    // The option's links, less the way to the console page: those two were just held to the
    // release's own facts, so this compares the console page against facts rather than against
    // a second derivation of them.
    links.slice(0, expected.length).map((el) => el.attrs.get('href')),
    'the console page and Getting Started\'s console block render different URLs for the same assets, and they are one reader of one data file',
  )

  // And the flip is the whole change: the cards' commands are what they are in the unreleased
  // state, which this file builds for the comparison because the checkout ships the released
  // one. The cards are the install paths a reader came for, and releasing the console may not
  // move them.
  const unreleased = unreleasedBuild(t)
  assert.equal(unreleased.status, 0, `the build of the unreleased site failed:\n${unreleased.output}`)
  const commands = (html) => installCards(html).flatMap((card) => commandsWithin(card))
  assert.deepEqual(
    commands(page),
    commands(gettingStarted(unreleased.dist)),
    'the install commands changed when the console was released: the owner\'s change is the flag and a version bump, and it may not move the CLI',
  )
})

test("the homepage's call to action offers the console only while the flag is on, and moves nothing else", (t) => {
  // Item 6 of the 2026-10-19 brief: the console is findable from the home page's call to
  // action, and the link is rendered from the same flag the rest of the console's offering is.
  // The flag-on half is read off the build `npm run check` makes, which is the state this
  // checkout ships; the flag-off half off the unreleased copy, because a reader of an
  // unreleased console has nothing to click and the hero may not offer a page with nothing to
  // download on it — "findable" is item 6's word for the released state.
  assert.match(
    repoFile(SITE_YAML),
    /^\s*console_downloads:\s*true\s*$/m,
    `${SITE_YAML} does not have features.console_downloads true, and this case reads the flag-on half off the build \`npm run check\` makes`,
  )

  /** The hero's row of links, as [label, href] pairs, plus the row's own text. */
  const actions = (dist) => {
    const page = readFileSync(path.join(dist, 'index.html'), 'utf8')
    const hero = [...walk(parse(page))].find(
      (el) => el.name === 'section' && (el.attrs.get('class') ?? '').split(/\s+/).includes('hero'),
    )
    assert.ok(hero, 'the homepage carries no hero section, and the call to action is in the hero (W1.4)')
    const row = [...walk(hero)].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('hero-actions'))
    assert.ok(row, 'the hero carries no action row, and the call to action is what a reader does next (W1.4)')
    return { links: linksOf([...walk(row)]).map((el) => [textOf(el).trim(), el.attrs.get('href')]), text: textOf(row) }
  }

  const released = actions(DIST)
  assert.deepEqual(
    released.links,
    [
      ['Get started', '/getting-started/'],
      ['How it works', '/how-it-works/'],
      // The console's own name, as Getting Started's option calls it, and the page that says
      // what the console is — not a download URL: the URL a reader downloads from is derived
      // from the release in `src/lib/console-downloads.mjs`, and the hero names the way there
      // (Global Constraint 9).
      ['Desktop console', '/console/'],
    ],
    "the homepage's call to action does not offer the console with features.console_downloads true: item 6 of the owner's brief of 2026-10-19 asks that the console be findable from it, and the flag is what renders the link",
  )

  const built = unreleasedBuild(t)
  assert.equal(built.status, 0, `the build of the unreleased site failed:\n${built.output}`)
  const unreleased = actions(built.dist)

  // The flag-off row is the two links and nothing else: no console link, no console sentence,
  // no placeholder where the link would go. The unreleased state is structural.
  assert.deepEqual(
    unreleased.links,
    [
      ['Get started', '/getting-started/'],
      ['How it works', '/how-it-works/'],
    ],
    "the hero's call to action is not exactly the two links it carries while features.console_downloads is false: the console's link arrives with the flag, and until then the row says nothing about the console (the owner's brief of 2026-10-19, item 6)",
  )
  assert.ok(
    !/console/i.test(unreleased.text),
    "the hero's call to action mentions the console while features.console_downloads is false, and the unreleased state is structural: nothing about the console renders before its release (item 6)",
  )

  // And the flip moves nothing else about the row: the two links a reader met before the
  // console shipped are the two links they meet after it, in the same order.
  assert.deepEqual(
    released.links.slice(0, 2),
    unreleased.links,
    "the homepage's existing call to action changed when the console was released: the owner's change is the flag and a version bump, and it may not move what was already there",
  )
})

test('the unreleased site still passes the gates that read the artifact', (t) => {
  // Every gate that reads the built artifact is run over the UNRELEASED build rather than
  // assumed to hold. It was the other way round until W3.1 flipped the flag: the case ran the
  // gates over the flag-on build, because the flag-on build was not the one `npm run check`
  // made. It is now — the checkout ships the released pair, and `npm run check` runs these
  // very gates over it — so the state whose gates nothing else checks is the one the fixture
  // builds: the flag-off console option, which is a shorter page with the data file's
  // unreleased sentence on it and no download at all.
  //
  // The gates are run in one nested `node --test` with `FUGARO_GATES_DIST` pointed at the
  // copy, which is the mechanism they document for reading a different build. `console.mjs`
  // is NOT among them: it holds the checkout's own `site.yaml` to `true`, and this build is
  // the one where the flag is off. `NODE_TEST_CONTEXT` is stripped, because a nested
  // `node --test` that inherits this process's test context refuses to run the files it is
  // given and exits zero — the trap the pass-count check below closes.
  const built = unreleasedBuild(t)
  assert.equal(built.status, 0, `the build of the unreleased site failed:\n${built.output}`)

  // `os-cards` is among them because the console's block is a card's, not a section beside the
  // cards: the cards themselves — one per supported system, each with the path the file offers
  // it first and one icon — are what the unreleased page has to keep too, and the card the
  // console sits in is a card either way.
  const gates = ['a11y', 'budget', 'copy', 'fold', 'linkcheck', 'meta', 'origins', 'os-cards']
  const env = { ...process.env, FUGARO_GATES_DIST: built.dist }
  delete env.NODE_TEST_CONTEXT
  const run = spawnSync(
    process.execPath,
    ['--test', ...gates.map((name) => path.join(SITE, 'test', `${name}.mjs`))],
    { env, encoding: 'utf8' },
  )
  const output = `${run.stdout ?? ''}${run.stderr ?? ''}`
  assert.equal(
    run.status,
    0,
    `a gate that reads the artifact refuses the site the console's unreleased state produces, so the flag is not the whole of the two states:\n${output}`,
  )

  // And the run really ran: the cases above are the artifact gates' own, and a child that
  // skipped its files would report nothing while exiting zero. (`#` is escaped in captured
  // TAP output, hence the unescaping before both reads below.)
  const counts = /# pass (\d+)/.exec(output.replace(/\\#/g, '#'))
  assert.ok(counts, `the nested run reported no pass count, so nothing above says the gates read the unreleased build:\n${output}`)
  assert.ok(
    Number(counts[1]) >= 70,
    `the nested run reported ${counts[1]} passing cases, and the gates that read the artifact declare far more than that: a run that skipped the files it was given exits zero`,
  )
  assert.match(
    output.replace(/\\#/g, '#'),
    /ok \d+ - every page is within the word ceilings the README states/,
    'the budget gate did not run its word-ceiling case over the unreleased build, so nothing above says the flag-off page fits the ceiling',
  )
})

test('a data file and a flag that disagree about the console stop the build', (t) => {
  // The pair is one fact (W3.1's flag and W1.6's data file), and the build refuses the two
  // disagreeing whichever way round they disagree. The dangerous direction is the one this
  // case used to plant: `available` true while `site.yaml` is false renders a download URL on
  // a site the repository's own rule still refuses one. The other direction is the state this
  // checkout ships mutated the other way — the flag says the console has shipped and the data
  // file says it has not — which would render the unreleased sentence under a flag that has
  // already promised a release. The mutation is read off the checkout's own flag rather than
  // typed, so the case keeps testing a disagreement after any future flip.
  const released = /^\s*console_downloads:\s*true\s*$/m.test(repoFile(SITE_YAML))
  const built = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.available = !released
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })

  assert.notEqual(
    built.status,
    0,
    `the build rendered a console state ${SITE_YAML}'s features.console_downloads disagrees with:\n${built.output}`,
  )
  assert.match(built.output, /downloads\.json/, 'the failure does not name the data file')
  assert.match(built.output, /site\.yaml/, 'the failure does not name the site facts the flag lives in')
  assert.ok(
    !existsSync(path.join(built.dist, 'getting-started', 'index.html')),
    'the build wrote Getting Started with a download link on it',
  )
})
