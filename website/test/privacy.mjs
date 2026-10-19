// website/test/privacy.mjs — the privacy statement's software part, held to the repository
// (the owner's request of 2026-10-19).
//
// The App Store and Play listings need one privacy policy URL, and `/privacy/` is it: the page
// therefore carries what the site does with a visitor's browser and what the SOFTWARE does with
// a reader's data, and the second half is the half no gate held before this file. A statement
// about software nobody runs from this repository is a statement nothing can be measured
// against — and it is the page a store reviewer reads, so the cost of an invented sentence is a
// listing that has to be corrected rather than a page that reads badly.
//
// **Every sentence in "The software" is held to the thing that makes it true, never to a copy of
// itself.** That is the whole rule of this file: a sentence and a fact, so that the fact moving
// is the sentence failing. Seven rules, one function each, each with a case below that plants a
// defect and watches the named refusal come back:
//
//   1. **The two parts.** The page carries "The website" and "The software" as its two `h2`s, in
//      that order, and the second is not tucked inside the first: the store reviewer's part is a
//      part of the page (`partFailures`).
//   2. **No collecting SDK, and no advertising identifier.** Every executable file, manifest and
//      lockfile under `mobile/` is scanned for the analytics, crash-reporting and advertising
//      SDKs named below and for the platform advertising-identifier APIs — the page says the app
//      "carries no analytics SDK and no advertising identifier", and this is what would make that
//      false (`collectingFailures`). The names are listed rather than asked for by pattern,
//      because a pattern that tried to recognise an analytics SDK would be a pattern that misses
//      the next one.
//   3. **The phone's two credentials, and where they live.** `SECRET_ITEMS` in
//      `mobile/src/accounts/secret.js` is D111's list, and the page may describe exactly it: two
//      items, named as the page names them, in secure storage whose own sources say Keychain and
//      Keystore (`mobileFailures`). The account tuple's `firebaseProjectId` is the other half —
//      the project the app signs in to is the reader's own, typed in per installation, which is
//      what `mobile/src/accounts/account.js` says the field is.
//   4. **The update check.** `internal/updatecheck` states the endpoint and the 24-hour TTL the
//      page's "at most once a day" is a claim about, and `internal/cli/updatewarn.go` and
//      `internal/localcfg` define the two switches the page names. All three are read here, and
//      the repository the endpoint points at is held equal to `site.yaml`'s own — the page says
//      "this repository's public release list", and a tool checking somebody else's releases
//      would make that sentence false (`updateCheckFailures`).
//   5. **The project operates no host the software talks to.** Every production source under
//      `cmd/`, `internal/`, `console/src/`, `mobile/src/` and `shared/` is scanned for the
//      site's canonical host: the one host this project operates is the site's own, so a
//      software source that names it is a source that has found a service to report to, and the
//      page's "and to nothing the Fugaro project operates" is what fails (`reachFailures`).
//      Tests are not scanned — a Go test that spells the host in a fixture is testing a rule
//      rather than reaching anywhere — and the one file in the tree that names it today is
//      recorded below, so that the exception is written down rather than inferred.
//   6. **The contact route, and no address.** The page's contact section points at the issue
//      tracker and at `SECURITY.md`'s reporting route, and the page carries no e-mail address
//      anywhere: the owner's ruling of 2026-10-19 is that there is no address to publish, so a
//      page that grew one would be a page contradicting a decision rather than a page with a
//      typo (`contactFailures`).
//   7. **There is no terms page.** The owner ruled it: no built page links to one, and no
//      sitemap the build produced names one (`termsFailures`). This is the page a `/terms` page
//      would have been linked from, which is why the rule is here rather than in `linkcheck.mjs`.
//
// It reads the built artifact and the repository's sources, standard library only, no network
// (Global Constraint 4). The built half comes from `dist/`; `FUGARO_GATES_DIST` points it
// elsewhere, which is how each refusal below was mutation-proved.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, REPO, builtPages, find, parse, siteFacts, textOf, walk } from './lib/html.mjs'

// ---- the repository's files, by path -----------------------------------------------------

/** A file of the repository, read as text. A path that is not there throws rather than returning
 * nothing: every caller below holds a sentence of the page to a file, and a file that moved is a
 * sentence with nothing under it. */
function repoFile(rel) {
  return readFileSync(path.join(REPO, rel), 'utf8')
}

/** Every file under the absolute directory `dir` whose NAME matches `re`, with its text. */
function sourcesUnder(dir, re) {
  if (!existsSync(dir)) return []
  const out = []
  const walkDir = (at) => {
    for (const entry of readdirSync(at, { withFileTypes: true })) {
      const full = path.join(at, entry.name)
      if (entry.isDirectory()) {
        if (entry.name === 'node_modules' || entry.name === '.git') continue
        walkDir(full)
      } else if (re.test(entry.name)) {
        out.push({ rel: path.relative(REPO, full), text: readFileSync(full, 'utf8') })
      }
    }
  }
  walkDir(dir)
  return out
}

/** The site's repository, as `site.yaml` states it, without a trailing slash. */
function repositoryURL() {
  const m = /^repository:\s*"?(https:\/\/github\.com\/[^"\s#]+?)"?\s*(#.*)?$/m.exec(repoFile('website/data/site.yaml'))
  assert.ok(m, "site.yaml states no repository URL, so the page's contact links have nothing to be read against")
  return m[1].replace(/\/$/, '')
}

const REPOSITORY_URL = repositoryURL()
const REPOSITORY_SLUG = REPOSITORY_URL.replace(/^https:\/\/github\.com\//, '')

/** The built page this file is about, or a failure that says the build is missing. */
const PRIVACY_URL = '/privacy/'

function privacyPage(dist = DIST) {
  const page = builtPages(dist).find((p) => p.url === PRIVACY_URL)
  assert.ok(
    page,
    `the build carries no ${PRIVACY_URL} page: this file holds the privacy statement to the repository, and \`npm run check\` builds the artifact it reads first`,
  )
  return page
}

/** A built page's `main`, or a failure that says there is none to read. */
function mainOf(html) {
  const main = find(parse(html), 'main')
  assert.ok(main, 'the built privacy page has no `main`, so this reader is looking at nothing (a11y.mjs is the gate that refuses the landmark)')
  return main
}

// ---- 1. the two parts --------------------------------------------------------------------

/** The parts the page must carry, in order: a store reviewer reads "The software" as one. */
export const PAGE_PARTS = Object.freeze(['The website', 'The software'])

/** Every way the page's two named parts depart from that, or []. */
export function partFailures(html) {
  const found = [...walk(mainOf(html))]
    .filter((el) => el.name === 'h2')
    .map((el) => textOf(el).replace(/\s+/g, ' ').trim())
  if (found.join(' | ') === PAGE_PARTS.join(' | ')) return []
  return [
    `the privacy page's sections are ${JSON.stringify(found)}: the page carries ${JSON.stringify(PAGE_PARTS)} as its two parts, in that order, because the App Store and Play listings link this page as the software's privacy policy as well as the site's`,
  ]
}

// ---- 2. no collecting SDK, and no advertising identifier ---------------------------------

// The SDKs whose presence would make the page's "no analytics SDK" false, each named by the
// package a bundler would resolve it from. A crash reporter is on the list because it reports:
// the page states that nothing is reported anywhere, and an SDK that sends a stack trace on the
// next launch is an SDK that reports. So is every advertising SDK, whatever else it collects.
export const COLLECTING_SDKS = Object.freeze([
  'firebase-analytics',
  '@react-native-firebase/analytics',
  'expo-firebase-analytics',
  '@segment/analytics-react-native',
  '@segment/react-native',
  'react-native-google-analytics-bridge',
  'react-native-google-analytics',
  'mixpanel-react-native',
  '@mixpanel/react-native',
  'amplitude-react-native',
  '@amplitude/analytics-react-native',
  'react-native-appsflyer',
  'react-native-adjust',
  'react-native-branch',
  '@sentry/react-native',
  '@bugsnag/react-native',
  'react-native-google-mobile-ads',
  'react-native-admob',
  'react-native-fbsdk',
  'react-native-fbsdk-next',
  'react-native-idfa',
  'react-native-tracking-transparency',
])

// The platform's advertising-identifier APIs. The page's second sentence is about the IDENTIFIER
// rather than about an SDK: an app can read one without any of the SDKs above.
export const ADVERTISING_IDENTIFIERS = Object.freeze([
  'advertisingIdentifier',
  'getAdvertisingId',
  'AppTrackingTransparency',
  'TrackingTransparency',
  'AAID',
  'GAID',
  'IDFA',
])

/** The extensions a bundle, a manifest or a lockfile can carry a dependency in. */
const SDK_BEARING = /\.(?:js|jsx|ts|tsx|mjs|cjs|json|sh)$/

/** Every way a scanned file names something that collects, or []. `files` is `[{ rel, text }]`. */
export function collectingFailures(files) {
  const out = []
  for (const { rel, text } of files) {
    for (const name of [...COLLECTING_SDKS, ...ADVERTISING_IDENTIFIERS]) {
      if (!text.includes(name)) continue
      const what = COLLECTING_SDKS.includes(name)
        ? `${name} is an analytics, crash-reporting or advertising SDK`
        : `${name} reads the platform's advertising identifier`
      out.push(
        `${rel} names ${name}: the privacy page states that the phone app carries no analytics SDK and no advertising identifier, so ${what} and that sentence is false — either take it out of the app or say on the page what it collects`,
      )
    }
  }
  return out
}

/** The mobile tree, as the scan reads it. */
export function mobileFiles() {
  return sourcesUnder(path.join(REPO, 'mobile'), SDK_BEARING)
}

// ---- 3. the phone's credentials, and where they live -------------------------------------

/** D111's two items, as `mobile/src/accounts/secret.js` declares them. */
export function secretItems(secretSource) {
  const m = /export const SECRET_ITEMS = Object\.freeze\(\[([^\]]*)\]\)/.exec(secretSource)
  assert.ok(
    m,
    'mobile/src/accounts/secret.js declares no `SECRET_ITEMS = Object.freeze([...])`, so the list of what the phone may hold is not where this check reads it',
  )
  return [...m[1].matchAll(/'([^']+)'/g)].map((one) => one[1])
}

/** The account tuple's fields, as `mobile/src/accounts/account.js` declares them. */
export function accountFields(accountSource) {
  const m = /export const ACCOUNT_FIELDS = Object\.freeze\(\[([^\]]*)\]\)/.exec(accountSource)
  assert.ok(m, 'mobile/src/accounts/account.js declares no `ACCOUNT_FIELDS = Object.freeze([...])` for this check to read')
  return [...m[1].matchAll(/'([^']+)'/g)].map((one) => one[1])
}

/** What the page's phone paragraph must say, and what makes each true. */
export const PHONE_CLAIMS = Object.freeze([
  { page: 'Firebase ID token', why: "D111's first item, `idToken`, is a Firebase Auth ID token" },
  { page: 'pairing key', why: "D111's second item, `macKey`, is the per-pairing key" },
  { page: 'secure storage', why: 'both items live in platform secure storage and nowhere else (D111)' },
  { page: 'Keychain', why: 'the secure storage is the Keychain on iOS (`expo-secure-store.js`)' },
  { page: 'Keystore', why: 'the secure storage is the Keystore on Android (`expo-secure-store.js`)' },
  { page: 'Firebase project', why: 'the account tuple carries the owner-typed `firebaseProjectId` the app signs in to' },
])

/**
 * Every way the page's phone paragraph departs from the mobile tree, or []: the items are
 * D111's two and no more, the page names each of them and where they live, and the project the
 * app talks to is the field the account tuple holds.
 */
export function mobileFailures({ page, secretSource, accountSource, storageSource }) {
  const out = []
  const items = secretItems(secretSource)
  if (items.join(',') !== 'idToken,macKey') {
    out.push(
      `SECRET_ITEMS is ${JSON.stringify(items)}: D111 allows the phone exactly two items, an \`idToken\` and a \`macKey\`, and the page describes the two it holds — a third item is a credential the page does not account for`,
    )
  }
  for (const claim of PHONE_CLAIMS) {
    if (!page.includes(claim.page)) {
      out.push(`the privacy page does not say ${JSON.stringify(claim.page)} about the phone app: ${claim.why}, and the reader of this page is owed it`)
    }
  }
  for (const word of ['Keychain', 'Keystore']) {
    if (!storageSource.includes(word)) {
      out.push(
        `mobile/src/accounts/secure-store.js and expo-secure-store.js no longer name the ${word}: the page tells a reader the phone's credentials live in the ${word}, and that sentence is read off these files`,
      )
    }
  }
  const fields = accountFields(accountSource)
  if (!fields.includes('firebaseProjectId')) {
    out.push(
      `ACCOUNT_FIELDS is ${JSON.stringify(fields)}: the page says the app signs in to the reader's OWN Firebase project, and the tuple field that carries that project is \`firebaseProjectId\``,
    )
  }
  return out
}

// ---- 4. the update check ------------------------------------------------------------------

/** The facts the page's update-check paragraph is a claim about. */
export const UPDATE_CHECK = Object.freeze({
  host: 'api.github.com',
  endpointPath: '/releases/latest',
  ttlHours: 24,
  configKey: 'update_check',
})

/** The repository slug `internal/updatecheck` checks releases against. */
export function updateCheckRepo(source) {
  const m = /^const Repo = "([^"]+)"$/m.exec(source)
  assert.ok(m, 'internal/updatecheck/updatecheck.go declares no `const Repo = "owner/name"`, so the repository the tool asks about is not where this check reads it')
  return m[1]
}

/** The endpoint the check fetches, split around the repository constant it is built from. */
export function updateCheckEndpoint(source) {
  const m = /^const Endpoint = "([^"]*)" \+ Repo \+ "([^"]*)"$/m.exec(source)
  assert.ok(m, 'internal/updatecheck/updatecheck.go declares no `const Endpoint = "…" + Repo + "…"`, so the one address the tool asks is not where this check reads it')
  return { prefix: m[1], suffix: m[2] }
}

/** How many hours a cached check stands for. */
export function checkTTLHours(source) {
  const m = /^const CheckTTL = (\d+) \* time\.Hour$/m.exec(source)
  assert.ok(m, 'internal/updatecheck/updatecheck.go declares no `const CheckTTL = N * time.Hour`, so the page\'s "at most once a day" has nothing to be held to')
  return Number(m[1])
}

/** The environment switch `internal/cli` reads. */
export function noUpdateCheckEnv(source) {
  const m = /^const noUpdateCheckEnv = "([^"]+)"$/m.exec(source)
  assert.ok(m, 'internal/cli/updatewarn.go declares no `const noUpdateCheckEnv = "…"`, so the switch the page names is not where this check reads it')
  return m[1]
}

/**
 * Every way the page's update-check paragraph departs from the code it describes, or []: the one
 * address is GitHub's release list for THIS repository, a cached answer stands a day, and both
 * switches the page names are the ones the CLI and the local config define.
 */
export function updateCheckFailures({ page, updatecheckSource, updatewarnSource, localcfgSource, repositorySlug }) {
  const out = []
  const repo = updateCheckRepo(updatecheckSource)
  if (repo !== repositorySlug) {
    out.push(
      `the update check asks GitHub about ${repo} and site.yaml says this site's repository is ${repositorySlug}: the page says the tool asks "this repository's public release list", so a check pointed at another repository makes that sentence false`,
    )
  }
  const { prefix, suffix } = updateCheckEndpoint(updatecheckSource)
  if (!prefix.startsWith(`https://${UPDATE_CHECK.host}/`) || suffix !== UPDATE_CHECK.endpointPath) {
    out.push(
      `the update check's endpoint is ${JSON.stringify(prefix + '<repo>' + suffix)}: the page tells a reader it asks GitHub's public release list, and this is not that address`,
    )
  }
  const ttl = checkTTLHours(updatecheckSource)
  if (ttl !== UPDATE_CHECK.ttlHours) {
    out.push(
      `CheckTTL is ${ttl} hour(s) and the page says the check asks "at most once a day": a ${ttl}-hour cache is a different answer, so the page's sentence and this constant move together`,
    )
  }
  const env = noUpdateCheckEnv(updatewarnSource)
  if (!page.includes(`${env}=1`)) {
    out.push(
      `the privacy page does not name \`${env}=1\` as the way to turn the check off: that is the switch internal/cli/updatewarn.go defines, and the page tells a reader how to stop the one request it makes`,
    )
  }
  if (!localcfgSource.includes(`yaml:"${UPDATE_CHECK.configKey}`)) {
    out.push(
      `internal/localcfg declares no \`yaml:"${UPDATE_CHECK.configKey}"\` field: the page tells a reader that \`${UPDATE_CHECK.configKey}: false\` turns the check off, and this is the setting that does it`,
    )
  }
  if (!page.includes(`${UPDATE_CHECK.configKey}: false`)) {
    out.push(`the privacy page does not name \`${UPDATE_CHECK.configKey}: false\`: the second way to turn the check off, and the one a reader's own config file takes`)
  }
  return out
}

// ---- 5. the project operates no host the software talks to --------------------------------

// The one file in the tree that names the site's canonical host and is NOT a finding: a Go test
// whose fixtures spell the host to test the website-boundary rule itself. It is a test, so the
// scan skips every `_test.go`; this constant is here so the exception is written down rather
// than inferred from the filter, and so a production file that starts naming the host is not
// mistaken for it.
export const HOST_NAMING_TEST = 'internal/cli/docs_website_boundary_test.go'

/** The production sources the reach scan reads: the tool, the console and the phone. */
export function softwareSources() {
  const dirs = ['cmd', 'internal', 'console/src', 'mobile/src', 'shared']
  return dirs
    .flatMap((dir) => sourcesUnder(path.join(REPO, dir), SDK_BEARING))
    .filter((file) => !/(?:_test\.go|\.test\.(?:js|ts|jsx|tsx|mjs))$/.test(file.rel))
}

/** Every production source that names `host`, or []. */
export function reachFailures(files, host) {
  return files
    .filter((file) => file.text.includes(host))
    .map(
      (file) =>
        `${file.rel} names ${host}: that is the host this project operates, and the privacy page states that the CLI and the console talk "to nothing the Fugaro project operates" — a source that names it is a source that has found a service to report to, and the sentence moves with it`,
    )
}

// ---- 6. the contact route, and no address -------------------------------------------------

/** An e-mail address, as a page would carry one: a mailto: link, or the shape in prose. */
const ADDRESS_RE = /[\w.+-]+@[\w-]+\.[a-z]{2,}/i

/** Every way the page's contact section departs from the owner's ruling, or []. */
export function contactFailures(html, repository) {
  const out = []
  const main = mainOf(html)
  const heading = [...walk(main)].find((el) => /^h[23]$/.test(el.name) && /^contact$/i.test(textOf(el).trim()))
  assert.ok(
    heading,
    `the privacy page has no Contact section: the owner's ruling gives a reader two routes — the ${repository} issue tracker and SECURITY.md's private reporting route — and a page that stopped saying so leaves them nowhere to go`,
  )
  const siblings = heading.parent.children
  const section = []
  for (const sibling of siblings.slice(siblings.indexOf(heading) + 1)) {
    if (/^h[123]$/.test(sibling.name ?? '')) break
    section.push(sibling)
  }
  const hrefs = section.flatMap((node) => [...walk(node)]).filter((el) => el.name === 'a' && el.attrs.has('href')).map((el) => el.attrs.get('href'))
  const wanted = [new RegExp(`^${repository}/issues/?$`), new RegExp(`^${repository}/blob/main/SECURITY\\.md#.+$`)]
  for (const re of wanted) {
    if (!hrefs.some((href) => re.test(href))) {
      out.push(
        `the privacy page's contact section does not link ${re.source}: the owner's ruling of 2026-10-19 gives a reader the issue tracker and SECURITY.md's private reporting route, and nothing else`,
      )
    }
  }
  for (const href of hrefs) {
    if (wanted.some((re) => re.test(href))) continue
    out.push(
      `the privacy page's contact section links ${href}: the owner's ruling allows the issue tracker and SECURITY.md's reporting route, so a third route is a decision to ask the owner for rather than a line to add`,
    )
  }
  const found = (html.match(ADDRESS_RE) ?? [])[0]
  if (found) {
    out.push(
      `the privacy page carries the e-mail address ${found}: the owner's ruling of 2026-10-19 is that the repository's issue tracker and SECURITY.md are the routes and there is no address to publish — a published address is a mailbox somebody has to run`,
    )
  }
  return out
}

// ---- 7. the owner's ruling: there is no terms page ----------------------------------------

/** A built page's links that point at a `/terms` page. */
function termsHrefs(html) {
  const out = []
  for (const el of walk(parse(html))) {
    if (el.name !== 'a' || !el.attrs.has('href')) continue
    const href = el.attrs.get('href')
    if (/^\/?terms\/?(?:[?#].*)?$/.test(href) || /^https?:\/\/[^/]+\/terms\/?$/.test(href)) out.push(href)
  }
  return out
}

/**
 * Every way the artifact departs from "there is no terms page", or []: no built page links to
 * one, and no sitemap the build produced names one.
 */
export function termsFailures(dist) {
  const out = []
  for (const page of builtPages(dist)) {
    for (const href of termsHrefs(page.html)) {
      out.push(
        `${page.rel} links ${href}: the owner's ruling of 2026-10-19 is that this repository has no terms page — the privacy statement is the page, and a link to a page that is not built is a 404 for a reader and a listing that reads as unfinished`,
      )
    }
  }
  for (const sitemap of sourcesUnder(dist, /^sitemap.*\.xml$/)) {
    if (/\/terms\b/.test(sitemap.text)) {
      out.push(`${sitemap.rel} lists /terms: the owner's ruling is that there is no terms page, so nothing the build produces may point at one`)
    }
  }
  return out
}

// ---- the gate ----------------------------------------------------------------------------

const FACTS = siteFacts()

test('the privacy page carries the two named parts, in order', () => {
  const failures = partFailures(privacyPage().html)
  assert.deepEqual(failures, [], failures.join('\n'))
})

test('the phone app carries no analytics SDK and no advertising identifier, and the page says so about exactly the tree it has', () => {
  const files = mobileFiles()
  assert.ok(files.length > 5, `only ${files.length} files under mobile/ matched the scan, so this check is looking at a fraction of the app`)
  const failures = collectingFailures(files)
  assert.deepEqual(failures, [], `the mobile tree carries something that collects:\n  ${failures.join('\n  ')}`)
  const page = privacyPage().html
  for (const sentence of ['no analytics SDK', 'no advertising identifier']) {
    assert.ok(
      page.includes(sentence),
      `the privacy page does not say the phone app carries ${sentence}: the scan above is what holds that sentence to the tree, and a page that dropped it is a page with nothing to hold`,
    )
  }
})

test("the phone's credentials, and where they live, are D111's two and the page's six claims", () => {
  const failures = mobileFailures({
    page: privacyPage().html,
    secretSource: repoFile('mobile/src/accounts/secret.js'),
    accountSource: repoFile('mobile/src/accounts/account.js'),
    storageSource: repoFile('mobile/src/accounts/secure-store.js') + repoFile('mobile/src/accounts/expo-secure-store.js'),
  })
  assert.deepEqual(failures, [], failures.join('\n'))
})

test('the update check the page describes is the one internal/updatecheck and internal/cli implement', () => {
  const failures = updateCheckFailures({
    page: privacyPage().html,
    updatecheckSource: repoFile('internal/updatecheck/updatecheck.go'),
    updatewarnSource: repoFile('internal/cli/updatewarn.go'),
    localcfgSource: repoFile('internal/localcfg/localcfg.go'),
    repositorySlug: REPOSITORY_SLUG,
  })
  assert.deepEqual(failures, [], failures.join('\n'))
})

test('no production source names the host this project operates', () => {
  const files = softwareSources()
  assert.ok(files.length > 100, `only ${files.length} production sources were scanned, so this check is looking at a fraction of the software`)
  assert.ok(
    files.every((file) => file.rel !== HOST_NAMING_TEST),
    `${HOST_NAMING_TEST} is a test file and was scanned as a production source: the recorded exception is that a Go test may spell the host in a fixture, so the filter must skip it`,
  )
  assert.ok(existsSync(path.join(REPO, HOST_NAMING_TEST)), `the recorded exception ${HOST_NAMING_TEST} is gone: delete the constant and its note in website/test/privacy.mjs`)
  const failures = reachFailures(files, FACTS.canonicalHost)
  assert.deepEqual(failures, [], `a source of the software names the host the project operates:\n  ${failures.join('\n  ')}`)
})

test("the page's contact section is the owner's two routes, and the page carries no address", () => {
  const failures = contactFailures(privacyPage().html, REPOSITORY_URL)
  assert.deepEqual(failures, [], failures.join('\n'))
})

test('no page links to a terms page, and no sitemap lists one', () => {
  const failures = termsFailures(DIST)
  assert.deepEqual(failures, [], failures.join('\n'))
})

// ---- the refusals, kept ------------------------------------------------------------------

const SHELL = (body) => `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main>${body}</main></body></html>`

/** A temp dist of { rel: text }, removed when the case ends. */
function distOf(t, files) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-privacy-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  for (const [rel, body] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dist, rel)), { recursive: true })
    writeFileSync(path.join(dist, rel), body)
  }
  return dist
}

test('a page that lost a part, or nested one inside the other, is refused', () => {
  const one = SHELL('<h1>Privacy</h1><h2>The website</h2><p>w</p>')
  assert.match(partFailures(one).join('\n'), /carries \["The website","The software"\] as its two parts/)
  // The same words as a section INSIDE the first part: the reviewer's part is a part of the page.
  const nested = SHELL('<h1>Privacy</h1><h2>The website</h2><h3>The software</h3>')
  assert.match(partFailures(nested).join('\n'), /as its two parts/)
  assert.deepEqual(partFailures(SHELL('<h1>Privacy</h1><h2>The website</h2><h2>The software</h2>')), [])
})

test('an analytics, crash-reporting or advertising SDK named anywhere under mobile/ is refused', () => {
  for (const text of [
    `import analytics from '@react-native-firebase/analytics';\n`,
    `{ "dependencies": { "react-native-appsflyer": "^6.0.0" } }\n`,
    `import advertisingId from 'react-native-idfa';\n`,
    `const gaid = await nativeAds.getAdvertisingId();\n`,
  ]) {
    const failures = collectingFailures([{ rel: 'mobile/fixture.js', text }])
    assert.equal(failures.length, 1, failures.join('\n'))
    assert.match(failures[0], /mobile\/fixture\.js names /)
    assert.match(failures[0], /no analytics SDK and no advertising identifier/)
  }
  // The control: a file that names none of them is not refused, or the scan would be one that
  // fails everything and this case would prove nothing.
  assert.deepEqual(collectingFailures([{ rel: 'mobile/fixture.js', text: `import * as SecureStore from 'expo-secure-store';\n` }]), [])
})

test('a third credential in SECRET_ITEMS, or a page that stopped naming one, is refused', () => {
  const page = privacyPage().html
  const secretSource = repoFile('mobile/src/accounts/secret.js')
  const accountSource = repoFile('mobile/src/accounts/account.js')
  const storageSource = repoFile('mobile/src/accounts/secure-store.js') + repoFile('mobile/src/accounts/expo-secure-store.js')

  const third = mobileFailures({
    page,
    secretSource: secretSource.replace("Object.freeze(['idToken', 'macKey'])", "Object.freeze(['idToken', 'macKey', 'sessionCookie'])"),
    accountSource,
    storageSource,
  })
  assert.equal(third.length, 1, third.join('\n'))
  assert.match(third[0], /SECRET_ITEMS is \["idToken","macKey","sessionCookie"\]/)

  const quiet = mobileFailures({ page: page.replace(/Keystore/g, 'the platform store'), secretSource, accountSource, storageSource })
  assert.equal(quiet.length, 1, quiet.join('\n'))
  assert.match(quiet[0], /does not say "Keystore"/)
})

test('an update check pointed at another repository, or cached for less than a day, is refused', () => {
  const updatecheckSource = repoFile('internal/updatecheck/updatecheck.go')
  const base = {
    page: privacyPage().html,
    updatecheckSource,
    updatewarnSource: repoFile('internal/cli/updatewarn.go'),
    localcfgSource: repoFile('internal/localcfg/localcfg.go'),
    repositorySlug: REPOSITORY_SLUG,
  }

  const elsewhere = updateCheckFailures({ ...base, updatecheckSource: updatecheckSource.replace(/^const Repo = ".*"$/m, 'const Repo = "someone/else"') })
  assert.equal(elsewhere.length, 1, elsewhere.join('\n'))
  assert.match(elsewhere[0], /asks GitHub about someone\/else and site\.yaml says this site's repository is dimipaun\/fugaro/)

  const hourly = updateCheckFailures({ ...base, updatecheckSource: updatecheckSource.replace(/^const CheckTTL = 24 \* time\.Hour$/m, 'const CheckTTL = 1 * time.Hour') })
  assert.equal(hourly.length, 1, hourly.join('\n'))
  assert.match(hourly[0], /CheckTTL is 1 hour\(s\) and the page says the check asks "at most once a day"/)

  const silent = updateCheckFailures({ ...base, page: base.page.replace('FUGARO_NO_UPDATE_CHECK=1', 'a setting') })
  assert.equal(silent.length, 1, silent.join('\n'))
  assert.match(silent[0], /does not name `FUGARO_NO_UPDATE_CHECK=1`/)
})

test('a production source that names the host the project operates is refused, and the test that may is skipped', () => {
  const host = FACTS.canonicalHost
  const failures = reachFailures([{ rel: 'internal/cli/report.go', text: `const report = "https://${host}/v1/telemetry"` }], host)
  assert.equal(failures.length, 1, failures.join('\n'))
  assert.match(failures[0], new RegExp(`^internal/cli/report\\.go names ${host.replace(/\./g, '\\.')}`))
  assert.match(failures[0], /nothing the Fugaro project operates/)

  // The filter is what keeps the recorded exception out: the test file names the host and is not
  // a finding, and a production file beside it is.
  assert.ok(repoFile(HOST_NAMING_TEST).includes(host), `${HOST_NAMING_TEST} no longer names ${host}, so it is no longer the recorded exception this file's filter exists for`)
  const files = softwareSources()
  assert.ok(
    files.every((file) => file.rel !== HOST_NAMING_TEST),
    `${HOST_NAMING_TEST} is a test file and the scan above read it as a production source`,
  )
})

test('a contact section with a third route, or a page carrying an address, is refused', () => {
  const repo = REPOSITORY_URL
  const good = `<h2>Contact</h2><p>Ask in the <a href="${repo}/issues">issue tracker</a>, or report through <a href="${repo}/blob/main/SECURITY.md#reporting-a-vulnerability">SECURITY.md</a>.</p>`
  assert.deepEqual(contactFailures(SHELL(`<h1>Privacy</h1>${good}`), repo), [])

  const third = contactFailures(SHELL(`<h1>Privacy</h1>${good.replace('</p>', '<a href="https://support.example/">help</a></p>')}`), repo)
  assert.equal(third.length, 1, third.join('\n'))
  assert.match(third[0], /links https:\/\/support\.example\/: the owner's ruling allows/)

  const address = contactFailures(SHELL(`<h1>Privacy</h1>${good.replace('</p>', ' Or write to privacy@example.com.</p>')}`), repo)
  assert.equal(address.length, 1, address.join('\n'))
  assert.match(address[0], /carries the e-mail address privacy@example\.com/)

  const missing = contactFailures(SHELL('<h1>Privacy</h1><h2>Contact</h2><p>Ask us.</p>'), repo)
  assert.equal(missing.length, 2, missing.join('\n'))
})

test('a page that links a terms page, and a sitemap that lists one, are refused', (t) => {
  const failures = termsFailures(distOf(t, { 'index.html': SHELL('<h1>t</h1><a href="/terms/">Terms</a>'), 'terms/index.html': SHELL('<h1>Terms</h1>') }))
  assert.equal(failures.length, 1, failures.join('\n'))
  assert.match(failures[0], /^index\.html links \/terms\/: the owner's ruling/)

  const named = termsFailures(distOf(t, { 'index.html': SHELL('<h1>t</h1>'), 'sitemap-index.xml': '<urlset><url><loc>https://example.test/terms/</loc></url></urlset>' }))
  assert.equal(named.length, 1, named.join('\n'))
  assert.match(named[0], /sitemap-index\.xml lists \/terms/)

  assert.deepEqual(termsFailures(distOf(t, { 'index.html': SHELL('<h1>t</h1><a href="/privacy/">Privacy</a>') })), [])
})
