// website/test/console.mjs — the console page (website plan, Task W1.6).
//
// The spec asks for "Console downloads: per-OS download links (placeholder until the
// console ships)", and the console has shipped: `console/` holds the application, its
// packaging and the workflow that signed, notarised and stapled its macOS artifacts, the
// release `console-v0.8.7` carries them, `site.yaml`'s `features.console_downloads` is true
// and the data file's `available` is true with it. The page's Downloads section renders that
// release, and the placeholder state it renders instead is a state a copy of the repository
// is built into rather than the state this checkout ships (W3.1). Six things are checked
// here:
//
//   1. **The released section renders the release's own downloads.** One link per published
//      asset, held to the repository, the release path, the tag and the published name one
//      part at a time (`test/lib/console-downloads.mjs`), with the checksum the data file
//      carries beside each — and the data file's own sentence about the shipped console
//      rather than a copy of it written into the markup.
//   2. **The unreleased state carries no link, and it is a fixture.** With
//      `features.console_downloads` flipped back to false and the data file's `available`
//      with it, the built section contains no download URL and no element a reader can
//      follow — no `<a>`, no `<button>`. That state is not the checkout's own any more, so
//      the case builds it in a copy of the repository: the refusal stays alive without the
//      site claiming the console is unreleased (D95, Global Constraint 9).
//   3. **The page renders its data file and types nothing of its own.** Every sentence in
//      the built page's article appears in `website/content/downloads.json` — the
//      mechanical form of the rule this site is written to, "a page renders a fact; it
//      never restates one" (F37, Global Constraint 6).
//   4. **Every claim is traceable to a decision.** Each sentence in the data file names the
//      document it came from, that document is in this repository, and every decision id
//      the source names is a decision that document actually carries (W1.6 step 3) — so a
//      claim about the console cannot be written without provenance.
//   5. **The rule the Go test enforces over the tree is wired into the build.** A download
//      URL in the data file fails the build here, naming the file, in either state: the
//      URL is derived from the release's published names and is never written down
//      (Global Constraint 9).
//   6. **A checksum the release does not publish fails the build.** The data file's `sha256`
//      values are held to the release's own `console-sha256sums.txt` (copied verbatim into
//      `website/data/`), so a wrong value is a red build rather than a page printing a
//      number no reader's download can match — and the case plants a well-formed wrong one,
//      because the shape alone cannot tell the two apart.
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
import {
  CONSOLE_README,
  DOWNLOADS_JSON,
  RELEASE_WORKFLOW,
  SITE_YAML,
  SUMS_FILE,
  assertReleaseURL,
  downloadNeedle,
  downloadsData,
  publishedAssets,
  publishedDownloads,
  releaseSums,
  repoFile,
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
      // `plugin/skills` is the one part of `plugin/` a build reads: the hero's chat
      // quotes the skills (website/test/lib/build.mjs).
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
  const page = path.join(site, 'dist', 'console', 'index.html')
  return {
    status: result.status,
    output: `${result.stdout ?? ''}${result.stderr ?? ''}`,
    page: existsSync(page) ? readFileSync(page, 'utf8') : null,
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
 * The page's text as a reader sees it: Astro escapes the characters below, and a test that
 * compared escaped text with the data file would fail on an apostrophe rather than on a
 * claim.
 */
function decode(html) {
  return html
    .replace(/&#39;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&#8212;/g, '—')
    .replace(/&amp;/g, '&')
}

test('the console page renders the facts its data file carries', async (t) => {
  const built = buildWith(t, () => {})
  const page = decode(builtPage(built))
  const data = downloadsData()

  // Every sentence rendered on the page is the data file's own: the name, what the console
  // is, and each property with its title.
  assert.ok(page.includes(`<h1>${data.name}</h1>`), `the page's heading is not ${JSON.stringify(data.name)}, the name the data file carries`)
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
    ['dmg', 'zip'],
    `${RELEASE_WORKFLOW} no longer takes exactly one .dmg and one .zip (it names ${rule.extensions.map((extension) => `.${extension}`).join(', ')}), and the data file's assets are those two`,
  )
  assert.ok(
    rule.versioned,
    `${RELEASE_WORKFLOW} no longer refuses an asset that is not named for the version (\`case "$f" in *"-$VERSION-"*\`), and the data file's names carry the version`,
  )
  for (const entry of data.downloads) {
    const extension = entry.file.slice(entry.file.lastIndexOf('.') + 1)
    assert.ok(
      rule.extensions.includes(extension),
      `${DOWNLOADS_JSON}'s ${entry.id} names a .${extension} file, and the release uploads ${rule.extensions.map((value) => `.${value}`).join(' and ')}`,
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
  // release carries and the page does not offer has nowhere to get it.
  assert.deepEqual(
    [...sums.keys()].sort(),
    data.downloads.map((entry) => entry.file.split('{version}').join(release.version)).sort(),
    `${SUMS_FILE} and ${DOWNLOADS_JSON} name different sets of assets: the site offers the release's assets, and an asset the release carries and the page does not is a download a reader cannot reach`,
  )

  // And the version is one this repository has released: the console's release is cut from the
  // CLI's own `vX.Y.Z` tag, and from 0.4.0 on a tag cannot be cut without a non-empty
  // `docs/releases/vX.Y.Z.md` on `main` (docs/release.md; both tag workflows run
  // `scripts/release-policy.sh check-highlights`). A version with no release file is therefore
  // a version the CLI was not released at, and not one the console was built from.
  //
  // **What this cannot prove, and nothing offline can: that `console-vX.Y.Z` is a release that
  // exists on GitHub.** A git tag cannot be read without a fetch, and a shallow CI checkout has
  // none. What W3.1 added is the offline half of that claim, and the Go boundary test is where
  // it lives: the data file's release block has to name the version of a CLI release note that
  // is in the tree and that names the console's own tag, and every asset has to be an asset the
  // release's own sums file records (`TestWebsiteConsoleReleaseFactsAreComplete`). What remains
  // the owner's check is the release page itself: the tag exists, its assets are the ones the
  // site names, and the checksums the page prints are the ones the release published.
  const highlights = `docs/releases/v${release.version}.md`
  assert.ok(
    existsSync(path.join(REPO, highlights)),
    `${DOWNLOADS_JSON}'s release.version is ${release.version} and ${highlights} is not in this repository: the console's release is cut from the CLI's own tag, and that tag cannot be cut without its highlights file`,
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
  assert.equal(asKey.page, null, 'the build wrote a page carrying a link to an unreleased artifact')

  const asFile = buildWith(t, (copy) => {
    const file = path.join(copy.root, 'website', 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.downloads[0].file = url
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
  assert.notEqual(asFile.status, 0, `the build accepted a URL in the file field:\n${asFile.output}`)
  assert.match(asFile.output, /downloads\.json/, 'the failure does not name the data file the URL came from')
  assert.match(asFile.output, /\.file is "/, 'the failure does not name the field the URL was written into')
  assert.equal(asFile.page, null, 'the build wrote a page carrying a link to an unreleased artifact')
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
})

test('the page states nothing its data file does not carry', async (t) => {
  const built = buildWith(t, () => {})
  const page = decode(builtPage(built))
  const data = downloadsData()

  // Every sentence on the page — the article's text, less the short labels (the heading,
  // the section titles, the navigation's words) that are the page's own furniture — appears
  // in the data file. A page that paraphrased a claim, or wrote a new sentence of its own
  // about the console, fails here (F37, Global Constraint 6).
  const article = /<article>([\s\S]*?)<\/article>/.exec(page)
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
    data.released.verify,
    data.not_released.text,
    ...data.properties.map((property) => property.text),
    // Each asset's own checksum, which the section prints beside its link: a 64-character
    // line a reader reads, so it is a sentence this rule sees.
    ...data.downloads.map((download) => download.sha256),
  ]
  for (const sentence of rendered) {
    assert.ok(
      sources.includes(sentence),
      `the page renders ${JSON.stringify(sentence)}, which ${DOWNLOADS_JSON} does not carry: a page renders a fact; it never restates one`,
    )
  }
})
