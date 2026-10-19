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
//      that order, at the same level — the second is a part of the page and not a section of the
//      first, which is the shape that would tuck the store reviewer's half inside the site's
//      (`partFailures`).
//   2. **The app's reporting, and the page and the package agreeing about it.** The app reports
//      crashes through Firebase Crashlytics and anonymous usage through Firebase Analytics, to a
//      Firebase project the Fugaro project operates (the owner's ruling of 2026-10-19), and this
//      rule holds the page and the
//      repository to each other in both directions: a telemetry package the app's dependency list
//      declares and the page does not name is refused, and the page naming a product that neither
//      the telemetry plan nor the dependency carries is refused unless it says which version of
//      the app carries the reports (`telemetryFailures`). The sentences a reader is owed are
//      listed rather than trusted — what is sent, what is never sent, the switch's default and
//      where the switch is, Google as the holder, and Google's own policy linked rather than
//      restated. Everything else that collects is still refused, by name, over the whole of
//      `mobile/`: the page names two reporters, so a third one would be a flow nobody was told
//      about (`collectingFailures`). The names are listed rather than asked for by pattern,
//      because a pattern that tried to recognise an analytics SDK would be a pattern that misses
//      the next one. The section a reader deletes an app to find is held too, and held on its
//      own: "Deleting your data" has to say that the reports are the exception — the Fugaro
//      project, not the reader, holds them, and deleting the app does not take them out — and
//      how the reporting is stopped, because the page's other sections made that section's old
//      first sentence ("everything the software holds lives in your own accounts") false
//      (`DELETION_CLAIMS`, read from its heading to the next one).
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
//      recorded below, so that the exception is written down rather than inferred. The selector
//      carries `.go`, because the CLI and the server are mostly Go: a scan that reads the tree's
//      scripts and manifests but not its Go source reads everything except the software this
//      page is about, and it reads enough files to look busy while doing it. The case below goes
//      through the selector rather than around it for that reason, and the case above counts the
//      Go sources it selected.
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

/** Every file under the absolute directory `dir` whose NAME matches `re`, with its text. `base` is
 * what a file's `rel` is relative to — the repository, or a tree a case below built. */
function sourcesUnder(dir, re, base = REPO) {
  if (!existsSync(dir)) return []
  const out = []
  const walkDir = (at) => {
    for (const entry of readdirSync(at, { withFileTypes: true })) {
      const full = path.join(at, entry.name)
      if (entry.isDirectory()) {
        if (entry.name === 'node_modules' || entry.name === '.git') continue
        walkDir(full)
      } else if (re.test(entry.name)) {
        out.push({ rel: path.relative(base, full), text: readFileSync(full, 'utf8') })
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
  const main = mainOf(html)
  const headings = [...walk(main)].filter((el) => el.name === 'h2')
  const found = headings.map((el) => textOf(el).replace(/\s+/g, ' ').trim())
  const out = []
  if (found.join(' | ') !== PAGE_PARTS.join(' | ')) {
    out.push(
      `the privacy page's sections are ${JSON.stringify(found)}: the page carries ${JSON.stringify(PAGE_PARTS)} as its two parts, in that order, because the App Store and Play listings link this page as the software's privacy policy as well as the site's`,
    )
  }
  // And the two parts are SIBLINGS, not one inside the other. The reader above walks the
  // heading tree flat, so a "The software" heading nested inside "The website" — an `h2` in a
  // wrapper, an `h3` in the same part's markup, a section of the site's part — reads as the
  // two parts in order and is the one shape that makes the software's statement a subsection
  // of the site's: a store reviewer's part is a part of the page, and the sentence that says
  // what the phone sends has to be able to stand on its own.
  if (headings.length === 2 && headings[0].parent !== headings[1].parent) {
    out.push(
      `the privacy page's two parts are not siblings: "The software" sits ${depthOf(headings[1])} levels deep and "The website" ${depthOf(headings[0])}, so one part is nested inside the other, or the two are in different containers — the part headings are the page's two h2 elements at the same level, one after the other`,
    )
  }
  return out
}

/** How deep an element sits under the document, counting element levels only. */
function depthOf(el) {
  let depth = 0
  for (let node = el.parent; node; node = node.parent) depth += 1
  return depth
}

// ---- 2. the app's reporting, and the page and the package agreeing about it ---------------
//
// The app reports crashes and usage through Firebase Crashlytics and Firebase Analytics, to a
// Firebase project the Fugaro project operates (the owner's ruling of 2026-10-19, which names
// no decision number: the ruling is the authority here, not a number, and the repository's own
// allocation records D193 onward as free — docs/coordinator/README.md — so a number cited for
// it would be a number nobody gave). The
// page says so, and this section holds the page and the repository to each other in BOTH
// directions: a package in the app's dependency list that the page does not name is a page
// that stopped disclosing something the app does, and a page that names one of these products
// when neither the plan nor the dependency carries it is a page claiming something the
// repository does not hold.
//
// The two packages, each with the product name a reader is owed. They are named rather than
// matched by pattern, because a pattern that recognised "a Firebase analytics package" would
// be a pattern that misses the next one — which is exactly what the refused list below is for.
export const APP_TELEMETRY = Object.freeze([
  { pkg: '@react-native-firebase/crashlytics', product: 'Firebase Crashlytics' },
  { pkg: '@react-native-firebase/analytics', product: 'Firebase Analytics' },
])

// The plan that records the work, as the sibling PR that owns it will land it. While it is not
// in the tree, the page may only describe the reports as belonging to a version that carries
// them — the sentence `EARLIER_BUILDS` below — which is the honest half of a statement about
// an app the reader may not have. When the plan lands nothing here changes: the plan becomes
// the thing direction B accepts, and the sentence stays true either way.
export const TELEMETRY_PLAN = 'docs/plans/2026-10-19-mobile-telemetry.md'

/** The sentence that says which version of the app this paragraph is about. */
const EARLIER_BUILDS = /\b(?:earlier|previous|older) builds?\b[^.]*\bnothing\b/i

/** What the page's reporting paragraph must say, and what makes each sentence true. */
export const TELEMETRY_CLAIMS = Object.freeze([
  { page: 'crash', why: 'Crashlytics reports crashes, and the page may not round that to "usage"' },
  { page: 'logs, diagnostics and app performance data', why: "the allow-list the owner's ruling of 2026-10-19 settled: a crash report is the crash's logs, its diagnostics and its performance data" },
  { page: 'which screens were opened', why: 'the usage Analytics collects is the screen set, and nothing about what a reader did there' },
  { page: 'app-instance identifier', why: 'the identifier Firebase generates for the app instance is what the stores call "Device or other IDs", which the same ruling enumerated' },
  { page: 'Never sent', why: "the ruling's ban list, which the page states as what never leaves the device" },
  { page: 'message', why: 'no message content is ever sent, and it is the first thing a reader of this page wants to know' },
  { page: 'credentials and your keys', why: "D111's two items are never sent anywhere" },
  { page: 'mailbox', why: 'nothing from the reader\'s own mailbox reaches the Fugaro project' },
  { page: 'no advertising identifier', why: 'google_analytics_adid_collection_enabled is false, and the scan below holds the tree to it' },
  { page: 'off until you turn it on', why: "the ruling's opt-in: collection is off until the reader turns it on, which is the default the page must state" },
  { page: "the app's settings", why: 'the switch is in the app, and the page has to say where' },
  { page: 'Google holds', why: "the data is held by Google as Firebase's processor for the Fugaro project, which is what the page says rather than restating Google's policy" },
])

/** The heading of the section a reader looks at after deleting the app. */
export const DELETION_SECTION = 'Deleting your data'

/**
 * What that section owes the reader once the page discloses the reports.
 *
 * This is the pair of sentences the page's own contradiction forced: "Everything the software
 * holds lives in your own accounts … removing the installation removes it" was true of a
 * project with no reporting, and it is false the moment the app sends crash reports to a
 * project the Fugaro project operates — those are held by Google, for that project, and
 * deleting the app does not take them out. Nothing above held the section, so the page could
 * say both things at once; these four are what stop it.
 */
export const DELETION_CLAIMS = Object.freeze([
  { page: 'the Fugaro project', why: 'the section has to name the project that holds the reports, or "your own accounts" reads as the whole of the answer' },
  { page: 'does not remove', why: 'the reports are the one thing deleting the app leaves behind, and a reader who deletes the app is owed that sentence' },
  { page: 'stops the sending', why: 'the section says how the reporting is stopped — the app\'s own switch — rather than leaving deleting the app as the only thing a reader can do' },
  { page: 'issue tracker', why: 'a report already sent is not the reader\'s to delete, so the section names the route by which they ask' },
])

/**
 * The text under the `h3` whose heading is `title`, up to the next `h3`, or null when the page
 * carries no such heading. Document order over the elements under `main`, because the section's
 * own markup is the page's business: what the reader meets between the two headings is the
 * section, whatever sits between them.
 */
function sectionTextOf(main, title) {
  const nodes = [...walk(main)]
  const at = nodes.findIndex((el) => el.name === 'h3' && textOf(el).replace(/\s+/g, ' ').trim() === title)
  if (at < 0) return null
  const next = nodes.findIndex((el, i) => i > at && el.name === 'h3')
  return nodes
    .slice(at + 1, next < 0 ? undefined : next)
    .map(textOf)
    .join(' ')
    .replace(/\s+/g, ' ')
}

/** The package names an app's dependency list declares, as its `dependencies` and `devDependencies`. */
export function dependencyNames(packageJSON) {
  const parsed = JSON.parse(packageJSON)
  return [...Object.keys(parsed.dependencies ?? {}), ...Object.keys(parsed.devDependencies ?? {})]
}

/**
 * Every way the page and the repository disagree about the app's reporting, or [].
 *
 * `planExists` is whether the telemetry plan is in the tree, and `packageJSON` is
 * `mobile/package.json` (the app's own dependency list — the lockfile is not read here,
 * because the lockfile is derived from this file and a lockfile entry nobody declared is a
 * different defect, held by the app's own tests).
 */
export function telemetryFailures({ page, packageJSON, planExists }) {
  const out = []
  const deps = dependencyNames(packageJSON)
  const named = APP_TELEMETRY.filter((one) => page.includes(one.product))

  // Direction A: the app carries it, so the page names it. A dependency the app declares and
  // the page stays silent about is the disclosure that went stale.
  for (const { pkg, product } of APP_TELEMETRY) {
    if (deps.includes(pkg) && !page.includes(product)) {
      out.push(
        `mobile/package.json declares ${pkg} and the privacy page does not name ${product}: the page is the privacy policy the stores link, so a package the app carries and the page does not name is a collection the reader was not told about`,
      )
    }
  }

  // Direction B: the page names it, so the repository backs it. The plan is the record; the
  // dependency is the app's own evidence; and while neither is in the tree the page may only
  // say this about a version of the app that carries the reports, which is the sentence the
  // brief asks for and what the fixture below removes to make this direction red.
  for (const { pkg, product } of named) {
    if (planExists || deps.includes(pkg)) continue
    if (EARLIER_BUILDS.test(page)) continue
    out.push(
      `the privacy page names ${product} and the repository does not hold it: neither ${TELEMETRY_PLAN} nor ${pkg} in mobile/package.json is here, so the page claims a flow nothing in this tree records — land the plan or add the dependency, or say on the page which version of the app carries these reports`,
    )
  }

  // And the sentences the reader is owed, once the page says the app reports at all.
  if (named.length > 0) {
    for (const claim of TELEMETRY_CLAIMS) {
      if (!page.includes(claim.page)) {
        out.push(`the privacy page does not say ${JSON.stringify(claim.page)} about the app's reporting: ${claim.why}`)
      }
    }
    if (!EARLIER_BUILDS.test(page)) {
      out.push(
        `the privacy page does not say which version of the app carries these reports: the reports are not in the app a reader may be holding, so the paragraph has to say so rather than read as a description of the build in their pocket`,
      )
    }
    if (!page.includes(FIREBASE_PRIVACY_HREF)) {
      out.push(
        `the privacy page does not link Google's own Firebase privacy policy (${FIREBASE_PRIVACY}): the page names Google as the holder, and a page that names a holder without pointing at the holder's own statement is a page restating a policy it does not own`,
      )
    }

    // And the deletion section, which the reporting makes true in a different way: the reports
    // are the one thing deleting the app does not take with it, and a page that says both that
    // and "everything lives in your own accounts" is a page contradicting itself. The section
    // is read on its own — from its heading to the next one — because a sentence anywhere else
    // on the page is not the answer a reader deletes an app to find.
    const deletion = sectionTextOf(mainOf(page), DELETION_SECTION)
    if (deletion === null) {
      out.push(
        `the privacy page has no ${JSON.stringify(DELETION_SECTION)} section: the page discloses reports the Fugaro project holds, so it owes the reader the one section that says what deleting the app does and does not take with it`,
      )
    } else {
      for (const claim of DELETION_CLAIMS) {
        if (!deletion.includes(claim.page)) {
          out.push(
            `the privacy page's ${JSON.stringify(DELETION_SECTION)} section does not say ${JSON.stringify(claim.page)}: ${claim.why} — the section is read from its heading to the next one, so a sentence elsewhere on the page does not answer it`,
          )
        }
      }
    }
  }
  return out
}

/** Google's own Firebase privacy page, linked rather than restated. */
export const FIREBASE_PRIVACY = 'https://firebase.google.com/support/privacy'

/** The href the page has to carry for it, as the built HTML writes it. */
const FIREBASE_PRIVACY_HREF = `href="${FIREBASE_PRIVACY}"`

// The SDKs whose presence would make the page's disclosure false, each named by the package a
// bundler would resolve it from: everything that collects and is NOT the two products the page
// discloses. A crash reporter is on the list for the same reason Crashlytics is not — the
// page names its reporter, and a second one would be a second flow nobody was told about. So
// is every advertising SDK, whatever else it collects.
export const COLLECTING_SDKS = Object.freeze([
  'firebase-analytics',
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

/** The extensions the reach scan below reads: the software's own source in every language the
 * tree carries, Go first, because the CLI and the server ARE Go — a selector without `.go` would
 * leave "and to nothing the Fugaro project operates" unenforced for the whole of `cmd/` and
 * `internal/` while still scanning enough files to look busy. */
const REACH_BEARING = /\.(?:go|js|jsx|ts|tsx|mjs|cjs|json|sh)$/

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
        `${rel} names ${name}: the privacy page discloses two reporters — Firebase Crashlytics and Firebase Analytics — and says the app carries no advertising identifier, so ${what} is a collection the page does not account for — either take it out of the app or say on the page what it collects`,
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

/** The roots of the production sources the reach scan reads, as the repository's own directories. */
export const SOFTWARE_ROOTS = Object.freeze(['cmd', 'internal', 'console/src', 'mobile/src', 'shared'].map((dir) => path.join(REPO, dir)))

/** The production sources the reach scan reads: the tool, the console and the phone, in every
 * language the tree carries. `roots` and `base` are the repository's unless a case below points
 * the selector at a tree it built — the selector is half of what this rule asserts (a source it
 * does not select is a source that can name the host unseen), so the case goes through it rather
 * than around it. */
export function softwareSources(roots = SOFTWARE_ROOTS, base = REPO) {
  return roots
    .flatMap((dir) => sourcesUnder(dir, REACH_BEARING, base))
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

test('the app reports what the page says it reports, and neither side has moved without the other', () => {
  const files = mobileFiles()
  assert.ok(files.length > 5, `only ${files.length} files under mobile/ matched the scan, so this check is looking at a fraction of the app`)
  const failures = collectingFailures(files)
  assert.deepEqual(failures, [], `the mobile tree carries something that collects:\n  ${failures.join('\n  ')}`)

  const telemetry = telemetryFailures({
    page: privacyPage().html,
    packageJSON: repoFile('mobile/package.json'),
    planExists: existsSync(path.join(REPO, TELEMETRY_PLAN)),
  })
  assert.deepEqual(telemetry, [], telemetry.join('\n'))

  // The plan is not in this tree today (the sibling PR that writes it has not landed), so the
  // direction above is satisfied by the page's own sentence about which version carries the
  // reports. This case says so rather than leaving it implied: the sentence is what makes the
  // disclosure honest while the work is a plan, and the day the plan lands the file is what the
  // same function reads instead.
  if (!existsSync(path.join(REPO, TELEMETRY_PLAN))) {
    assert.ok(
      EARLIER_BUILDS.test(privacyPage().html),
      `the privacy page names the app's reporters and ${TELEMETRY_PLAN} is not in this tree: the paragraph has to say which version of the app carries the reports, because nothing in the repository holds them yet`,
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
  const go = files.filter((file) => file.rel.endsWith('.go'))
  assert.ok(
    go.length > 300,
    `only ${go.length} of the ${files.length} sources scanned are Go, so the reach scan is not reading the CLI and the server — the software this page is about is mostly Go, and a selector without \`.go\` leaves "and to nothing the Fugaro project operates" unenforced for all of it`,
  )
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

test('a page that lost a part, nested one inside the other, or put them side by side in two containers, is refused', () => {
  const one = SHELL('<h1>Privacy</h1><h2>The website</h2><p>w</p>')
  assert.match(partFailures(one).join('\n'), /carries \["The website","The software"\] as its two parts/)
  // The same words as a section INSIDE the first part: the reviewer's part is a part of the page.
  const nested = SHELL('<h1>Privacy</h1><h2>The website</h2><h3>The software</h3>')
  assert.match(partFailures(nested).join('\n'), /as its two parts/)
  assert.deepEqual(partFailures(SHELL('<h1>Privacy</h1><h2>The website</h2><h2>The software</h2>')), [])
})

test('a second part nested under the first is refused, headings or not, and the flat reading is what missed it', () => {
  // The headings in order, the right words, and one of them inside the other's markup: the
  // reader that walked the heading tree flat read this as two parts and said nothing, which is
  // the defect this case exists for.
  const wrapped = SHELL('<h1>Privacy</h1><h2>The website</h2><div class="part"><h2>The software</h2></div>')
  const failures = partFailures(wrapped)
  assert.equal(failures.length, 1, `a nested "The software" heading was not refused: ${failures.join('\n')}`)
  assert.match(failures[0], /two parts are not siblings/)
  assert.match(failures[0], /levels deep/)

  // And the control, which is the shape the page is built in: the two parts as siblings.
  assert.deepEqual(partFailures(SHELL('<h1>Privacy</h1><h2>The website</h2><p>w</p><h2>The software</h2><p>s</p>')), [])

  // Each part in a container of its own is the same defect read the other way: the parts are
  // level, but not one after the other in the page's own flow, so the reader cannot slice the
  // site's part without slicing into the software one.
  const sections = SHELL('<h1>Privacy</h1><section><h2>The website</h2></section><section><h2>The software</h2></section>')
  assert.match(partFailures(sections).join('\n'), /two parts are not siblings/)
})

test('an analytics, crash-reporting or advertising SDK named anywhere under mobile/ is refused', () => {
  for (const text of [
    `import analytics from 'firebase-analytics';\n`,
    `{ "dependencies": { "react-native-appsflyer": "^6.0.0" } }\n`,
    `import advertisingId from 'react-native-idfa';\n`,
    `const gaid = await nativeAds.getAdvertisingId();\n`,
    `import crashlytics from '@sentry/react-native';\n`,
  ]) {
    const failures = collectingFailures([{ rel: 'mobile/fixture.js', text }])
    assert.equal(failures.length, 1, failures.join('\n'))
    assert.match(failures[0], /mobile\/fixture\.js names /)
    assert.match(failures[0], /the page does not account for/)
  }
  // The control: a file that names none of them is not refused, or the scan would be one that
  // fails everything and this case would prove nothing.
  assert.deepEqual(collectingFailures([{ rel: 'mobile/fixture.js', text: `import * as SecureStore from 'expo-secure-store';\n` }]), [])
})

test("the app's own reporters are admitted by the scan, and the page is what admits them", () => {
  // The two packages the app is to carry are NOT findings — the page discloses them — which is
  // the difference between a scan that refuses everything and a scan that refuses what the page
  // does not account for. Named here so the list cannot quietly grow them back.
  for (const pkg of APP_TELEMETRY.map((one) => one.pkg)) {
    assert.ok(!COLLECTING_SDKS.includes(pkg), `${pkg} is refused by the SDK scan and disclosed by the page: the page names it as the app's reporter, so the scan must not also refuse it — one of the two is wrong`)
  }
  assert.deepEqual(
    collectingFailures([{ rel: 'mobile/package.json', text: `{ "dependencies": { "${APP_TELEMETRY[0].pkg}": "^1.0.0" } }` }]),
    [],
    'the disclosed crash reporter is refused by the mobile scan, so the page names a package the gate forbids',
  )
})

test("the page naming a reporter the repository does not hold, or the app holding one the page does not name, is refused", () => {
  const page = privacyPage().html
  const packageJSON = repoFile('mobile/package.json')
  const base = { page, packageJSON, planExists: false }

  // Direction A: the app declares it, the page stays silent. The package is planted in the
  // app's own dependency list, which is where a later task would add it.
  const declared = JSON.parse(packageJSON)
  declared.dependencies = { ...(declared.dependencies ?? {}), [APP_TELEMETRY[1].pkg]: '^22.0.0' }
  const silent = page.replace(/Firebase Analytics/g, 'the usage count')
  assert.match(
    telemetryFailures({ page: silent, packageJSON: JSON.stringify(declared), planExists: false }).join('\n'),
    new RegExp(`mobile/package.json declares ${APP_TELEMETRY[1].pkg.replace(/[/@.]/g, '\\$&')} and the privacy page does not name Firebase Analytics`),
    `a dependency the app declares and the page does not name was not refused: ${telemetryFailures({ page: silent, packageJSON: JSON.stringify(declared), planExists: false }).join('\n')}`,
  )
  // The control: the page's own naming, with the dependency declared and the plan in the tree,
  // is the state the two directions agree on, and it is not refused.
  assert.deepEqual(
    telemetryFailures({ page, packageJSON: JSON.stringify(declared), planExists: true }),
    [],
    'the page and the repository name the same reporter and the gate still refuses one of them',
  )

  // Direction B: the page names a reporter nothing in the tree holds, and it does not say which
  // version carries it. This is the half the brief asks for, and the sentence the fixture strips
  // is what the page uses instead while the plan is still a plan.
  const noVersion = page.replace(/Earlier builds of the app send nothing at all\./, 'The app sends these reports.')
  const unbacked = telemetryFailures({ page: noVersion, packageJSON, planExists: false })
  assert.equal(unbacked.length, 3, unbacked.join('\n'))
  for (const product of APP_TELEMETRY.map((one) => one.product)) {
    assert.ok(
      unbacked.some((line) => line.includes(`names ${product} and the repository does not hold it`)),
      `${product} is named on the page and neither the plan nor the app's dependency list records it, and the gate said nothing: ${unbacked.join('\n')}`,
    )
  }
  assert.ok(
    unbacked.some((line) => line.includes(TELEMETRY_PLAN)),
    `the refusal does not name ${TELEMETRY_PLAN}, so a later task is not told what to land`,
  )
  assert.ok(
    unbacked.some((line) => /does not say which version/.test(line)),
    'a page naming a reporter nothing holds, and saying nothing about which version carries it, is accepted',
  )

  // And the same page with the plan in the tree: direction B is satisfied by the plan's existence,
  // so the two "the repository does not hold it" refusals are gone.
  assert.deepEqual(
    telemetryFailures({ page, packageJSON, planExists: true }).filter((line) => /does not hold it/.test(line)),
    [],
    'the plan the page names is in the tree and the gate still refuses the page for naming a reporter nothing holds',
  )
  // The version sentence is not direction B's: it is the owner's own requirement (the reports are
  // not in the app a reader may hold), so it stays required with the plan in the tree — which is
  // what this fixture shows, the only line left being the sentence the fixture stripped.
  assert.deepEqual(
    telemetryFailures({ page: noVersion, packageJSON, planExists: true }),
    [
      'the privacy page does not say which version of the app carries these reports: the reports are not in the app a reader may be holding, so the paragraph has to say so rather than read as a description of the build in their pocket',
    ],
    'the sentence about which version carries the reports is direction B\'s to waive, and it is not: it is the owner\'s requirement in both states',
  )

  // The claims the reader is owed, one planted omission at a time: the ban list, the default,
  // and the link to Google's own policy.
  for (const [what, gone] of [
    ['the ban list', page.replaceAll('Never sent:', 'Also sent:')],
    ['the default', page.replaceAll('off until you turn it on', 'on unless you turn it off')],
    ['Google as the holder', page.replaceAll('Google holds', 'It is held')],
  ]) {
    const failures = telemetryFailures({ ...base, page: gone })
    assert.ok(failures.length >= 1, `a page that lost ${what} was not refused`)
  }
  const unlinked = telemetryFailures({ ...base, page: page.replace(FIREBASE_PRIVACY, 'https://example.net/policy') })
  assert.match(unlinked.join('\n'), /does not link Google's own Firebase privacy policy/)

  // And the deletion section, which the reporting makes false in its old form. Each claim is
  // planted absent on its own, and the section is read on its own: a page that says
  // "everything lives in your own accounts" while the paragraph above it sends reports to a
  // project the Fugaro project operates is the contradiction this rule exists for.
  for (const [, planted] of DELETION_CLAIMS.map((claim) => [claim, page.replaceAll(claim.page, 'something else')])) {
    const failures = telemetryFailures({ ...base, page: planted })
    assert.ok(
      failures.some((line) => line.includes(`"${DELETION_SECTION}" section does not say`)),
      `a page whose deletion section lost one of its sentences was not refused: ${failures.join('\n')}`,
    )
  }
  // The section gone, and the section emptied of the exception while keeping the words: the
  // old page's own sentence, which is what a reader deleting the app was told.
  const noSection = page.replace(/<h3[^>]*>Deleting your data<\/h3>/, '<h3>Something else</h3>')
  assert.notEqual(noSection, page, 'the fixture did not rename the deletion section, so this case proves nothing')
  assert.ok(
    telemetryFailures({ ...base, page: noSection }).some((line) => /has no "Deleting your data" section/.test(line)),
    'a page that lost the deletion section altogether is accepted, so a reader deleting the app has nothing to read',
  )
  const old = page.replace(
    /Everything else the software holds[\s\S]*?report already sent\./,
    'Everything the software holds lives in your own accounts, your own cloud project and your own devices. Removing the installation removes it.',
  )
  assert.notEqual(old, page, 'the fixture did not replace the deletion section, so this case proves nothing')
  const oldFailures = telemetryFailures({ ...base, page: old })
  assert.ok(
    oldFailures.some((line) => /does not say "the Fugaro project"/.test(line)) &&
      oldFailures.some((line) => /does not say "does not remove"/.test(line)),
    `the page whose deletion section contradicts the disclosure above it is accepted: ${oldFailures.join('\n')}`,
  )
  // The control: the shipped page passes all of it, so the rule is not one that fails everything.
  assert.deepEqual(telemetryFailures(base), [])
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

  // The selector is half of the rule, so the case goes through it rather than around it: a tree
  // with a production Go file and a Go test beside it, BOTH naming the host, is read by
  // softwareSources itself. The production file is selected and refused; the test is filtered out
  // — and a selector that lost `.go` would select neither, which is how this rule went unenforced
  // for the CLI and the server until it was caught.
  const tree = mkdtempSync(path.join(tmpdir(), 'privacy-reach-'))
  try {
    const root = path.join(tree, 'cmd')
    mkdirSync(root)
    writeFileSync(path.join(root, 'report.go'), `package cmd\n\nconst report = "https://${host}/v1/telemetry"\n`)
    writeFileSync(path.join(root, 'report_test.go'), `package cmd\n\nconst fixture = "https://${host}/v1/telemetry"\n`)
    const scanned = softwareSources([root], tree).map((file) => file.rel)
    assert.deepEqual(scanned, ['cmd/report.go'], `the reach scan selected ${JSON.stringify(scanned)}: it must select the Go source that names the host and filter out the Go test beside it`)
    const planted = reachFailures(softwareSources([root], tree), host)
    assert.equal(planted.length, 1, planted.join('\n'))
    assert.match(planted[0], /^cmd\/report\.go names /)
  } finally {
    rmSync(tree, { recursive: true, force: true })
  }

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
