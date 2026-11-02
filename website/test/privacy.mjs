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
// is the sentence failing. Eight rules, one function each, each with a case below that plants a
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
//      listed rather than trusted — what is sent, what is never sent, the app versions that
//      carry the reports having no switch to turn them off, and what a reader can do instead,
//      Google as the holder, and Google's own policy linked rather than restated. One of them
//      is read as its SENTENCE rather than as its phrase: the clause naming the two other
//      tools, because that phrase is true of the website too — so the sentence saying the
//      website carries none of the reports, the page contradicting its own first half, is
//      refused rather than passing on the phrase. Everything
//      else that collects is still refused, by name, over the whole of `mobile/`: the page
//      names two reporters, so a third one would be a flow nobody was told
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
//      rather than reaching anywhere — and the files in the tree that name it today are recorded
//      below, one by one, so that every exception is written down rather than inferred: the Go
//      test, and the console's two files — `onboarding.ts`, which holds the project's own link,
//      and `window.ts`, which carries only the host in `ALLOWED_EXTERNAL_HOSTS` so that link may
//      open — neither a service the software reports to (the note below `HOST_NAMING_CONSOLE` gives the reason and the ruling,
//      and the case that plants a third file proves the rule still holds for every other source).
//      The selector carries `.go`, because the CLI and the server are mostly Go: a scan that reads
//      the tree's scripts and manifests but not its Go source reads everything except the software
//      this page is about, and it reads enough files to look busy while doing it. The case below
//      goes through the selector rather than around it for that reason, and the case above counts
//      the Go sources it selected.
//   6. **The contact route, and no address.** The page's contact section points at the issue
//      tracker and at `SECURITY.md`'s reporting route, and the page carries no e-mail address
//      anywhere: the owner's ruling of 2026-10-19 is that there is no address to publish, so a
//      page that grew one would be a page contradicting a decision rather than a page with a
//      typo (`contactFailures`).
//   7. **There is no terms page.** The owner ruled it: no built page links to one, and no
//      sitemap the build produced names one (`termsFailures`). This is the page a `/terms` page
//      would have been linked from, which is why the rule is here rather than in `linkcheck.mjs`.
//   8. **The look.** The owner approved the bold redesign on 2026-10-20 (`website/design-ref/`)
//      and the look is written once, in `src/styles/base.css`'s section 2c. This page wears it —
//      the headline on the display scale with its word in the gradient, the two part headings on
//      the display scale, every subsection in one of the site's cards — and the classes it names
//      are shapes `src/styles/base.css` draws and none of them is a rule of the page's own, so a
//      page that grew a look of its own is refused (`lookFailures`, `shapeFailures`). What is
//      held is the shape of the look, not its
//      rendering: a gate cannot see a page, and the match to the owner's boards is the owner's
//      check at the preview.
//
// It reads the built artifact and the repository's sources, standard library only, no network
// (Global Constraint 4). The built half comes from `dist/`; `FUGARO_GATES_DIST` points it
// elsewhere, which is how each refusal below was mutation-proved.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, REPO, builtPages, cssRules, find, findAll, parse, siteFacts, textOf, walk } from './lib/html.mjs'

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

/**
 * What the page's reporting paragraph must say, and what makes each sentence true.
 *
 * `page` is the phrase the paragraph has to carry, and the phrase is enough for every claim
 * but one. The clause about the reports the OTHER tools carry is the exception, and it carries
 * two extra fields — `names`, the tools the clause has to name, and `notName`, the subject it
 * must not name — because its phrase is true of a sentence whose subject is wrong: "The website
 * and its pages carry none of these reports." carries the phrase, and a software-part clause
 * calling the website unmeasured is the page contradicting its own first half. So that claim is
 * held to its SENTENCE, read by `sentenceWith`, and not to its phrase.
 */
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
  { page: 'no switch inside the app', why: "the owner's ruling of 2026-10-19: the versions that carry these reports send them whether or not a reader wants them sent, so the page has to say there is no switch rather than leave the reader looking for one" },
  { page: 'uninstalling the app stops them', why: "uninstalling is the one thing that stops the reports, so the page owes a reader that sentence and not a switch the app does not have" },
  { page: 'not installing it avoids them', why: 'a reader who has not installed the app is owed the other half: the reports arrive with the installation, and not installing the version is what keeps them away' },
  { page: 'carry none of these reports', why: "the reports are the app's alone: the two other tools the software part covers carry none of them, and the page says so rather than leaving a reader of that part to guess. The clause names those two and not the website, whose own measurement is part one's subject — a software-part sentence calling the website unmeasured would contradict the half above it, and the flag that decides that half is not this gate's to read", names: ['command line tool', 'console'], notName: 'website' },
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
  { page: 'Uninstalling the app stops the sending', why: 'the section says how the reporting is stopped, and since the owner\'s ruling of 2026-10-19 the only thing that stops it is uninstalling the app — the version that carries the reports has no switch to turn them off' },
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

/**
 * The sentence of `page` that carries `needle`, with its whitespace collapsed, or null when the
 * page carries no such needle.
 *
 * A sentence ends at `.`, `!` or `?` followed by a space, which is how the page's prose writes
 * them. That is enough for the one claim read this way: the clause about the reports the other
 * tools carry is a single sentence of a single paragraph, and it is the sentence, not the
 * phrase inside it, that has to be about the right subject.
 */
function sentenceWith(page, needle) {
  const flat = page.replace(/\s+/g, ' ')
  const at = flat.indexOf(needle)
  if (at < 0) return null
  const marks = ['. ', '! ', '? ']
  const before = marks.map((mark) => flat.lastIndexOf(mark, at)).filter((i) => i >= 0)
  const after = marks.map((mark) => flat.indexOf(mark, at + needle.length)).filter((i) => i >= 0)
  return flat.slice(before.length ? Math.max(...before) + 2 : 0, after.length ? Math.min(...after) + 1 : flat.length)
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
        continue
      }
      // A claim that carries a subject is read as its sentence rather than as its phrase, and
      // the clause about the other tools is the one that does: its phrase is true of a sentence
      // about the website as well as of the sentence about the command line tool and the
      // console, and only one of those two sentences is a software-part statement. So the tools
      // the clause has to name are looked for in that sentence, and the subject it must not
      // name is refused there — a page that swapped the subject kept the phrase and lost the
      // meaning, which is exactly what the case below plants.
      const sentence = sentenceWith(page, claim.page)
      for (const name of claim.names ?? []) {
        if (!sentence.includes(name)) {
          out.push(
            `the privacy page's clause about the other tools does not name the ${name}: the clause says which of the software's tools carry none of these reports, and the software part is about the command line tool, the console and the phone app — a clause naming neither of the two tools is a clause about nothing`,
          )
        }
      }
      if (claim.notName && sentence.includes(claim.notName)) {
        out.push(
          `the privacy page's clause about the other tools names the ${claim.notName}: part one of this page is the website, and it discloses the site's own Google Analytics measurement under the consent that part states — a software-part clause calling the website unmeasured is the page contradicting its own first half, and the flag that decides that half is not this gate's to read`,
        )
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

/** The names that keep a selected file out of the scan: a test spells the host in a fixture to
 * test a rule rather than reaching anywhere. It is the first filter `softwareSources` applies, and
 * the case below reads it, so an allow-list entry cannot name a file the scan would never read. */
const NOT_PRODUCTION = /(?:_test\.go|\.test\.(?:js|ts|jsx|tsx|mjs))$/

/** The ways a source reaches a service, as the tokens it would carry to do it. Nothing on
 * `HOST_NAMING_CONSOLE` may carry one: those files are on the allow-list because they hold the
 * project's own link and make no request, and a request grown inside one of them would be exactly
 * what the rule is for while the host it names sits in a file the scan no longer reads. */
const REACH_CALLS = Object.freeze(['fetch(', '.request(', 'XMLHttpRequest', 'WebSocket', 'EventSource', 'sendBeacon'])

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

// The one test file in the tree that names the site's canonical host and is NOT a finding: a Go
// test whose fixtures spell the host to test the website-boundary rule itself. It is a test, so
// the scan skips every `_test.go`; this constant is here so the exception is written down rather
// than inferred from the filter, and so a production file that starts naming the host is not
// mistaken for it. The two production files that name it are `HOST_NAMING_CONSOLE`, below.
export const HOST_NAMING_TEST = 'internal/cli/docs_website_boundary_test.go'

// The two production files in the tree that name the site's canonical host and are NOT a finding,
// and the reason the rule above does not reach them: the console's own link to the project's
// public website, at the host `site.yaml`'s `canonical_url` states and the owner ruled canonical
// on 2026-10-17 (`docs/website/implementation-plan.md` Task W2.2, D92), which the owner's ruling
// of 2026-10-20 put on the console's onboarding screens.
//
// This rule exists for the page's sentence "They talk to your own Google Cloud project, ... and to
// nothing the Fugaro project operates. Nothing is reported anywhere": a source that names the
// host has found a service to *report to*. These two do not report anywhere. `window.ts` carries
// that host in the `openExternal` allow-list and `onboarding.ts` carries the getting-started URL
// (`GETTING_STARTED_URL`), and the console opens that URL by handing it to the *user's own
// browser* through the OS — the console itself makes no request, and what the reader then loads is
// the project's own website, which the page's part one describes rather than a host the software
// reaches. The sentence's subject is the software's own traffic, and a link the reader chooses to
// follow is not that; a console that could not link to its own site would be a page the onboarding
// ruling could not be written about.
//
// They are named one by one, with their full paths, so the exemption is exactly these two files
// and nothing else: the rule stays in force for every other source, and the case below plants a
// third file under the same directory and watches it refused. What is exempt is the link and not
// the file: the scan cannot see the host inside an allow-listed file, so the gate also reads both
// for the ways a source reaches a service (`REACH_CALLS`) and refuses one, which is the only place
// this rule could be defeated from.
export const HOST_NAMING_CONSOLE = Object.freeze(['console/src/main/onboarding.ts', 'console/src/main/window.ts'])

/** The design the console's reporting sentence comes from. `website/test/console.mjs` holds the
 * downloads page's property to the same section, so the two pages state one fact between them
 * rather than two readings of it. */
export const CONSOLE_DESIGN = 'docs/design/desktop-console.md'

/** What the design's section 7.4 says about the console's crash reporting, and therefore what
 * the privacy page says too. The design is the source and the page repeats it: the console is
 * the second artifact that can send anything, and its flow is not the phone's — the report goes
 * to Sentry rather than to a Firebase project the Fugaro project operates, and it leaves only
 * after the person turns the switch on (the owner's ruling of 2026-10-31, design section 7.4).
 *
 * The scrubbing sentence is held here in the design's own words, because the page's promise to a
 * reader is that sentence and not a summary of it: a page that rounds "no message, no path, no
 * user name, no environment and no host name" to "scrubbed" is a page promising less than the
 * code does. */
export const CONSOLE_REPORTING = Object.freeze({
  both: Object.freeze([
    'Sentry, hosted in the European Union',
    'no message, no path, no user name, no environment and no host name',
    // A frame is the file and the line, and both statements say so; what a reader would
    // otherwise be left to guess is that the function the code was in travels too.
    'its function name',
  ]),
  /** The design's own wording for the rule, in a list of its own because the page states the rule
   * and not this sentence: the page hands the reader the two facts that make reporting opt-in —
   * the switch ships off, and it decides the next launch — and a page that had to carry
   * "crash reporting only ever by the user's own opt-in" verbatim would be the design quoted
   * rather than a statement written for the person reading it. */
  design: Object.freeze(["crash reporting only ever by the user's own opt-in"]),
  page: Object.freeze([
    'Send Crash Reports',
    'Sentry, hosted in the European Union',
    'off until you tick it',
    'the next launch',
    'no message, no path, no user name, no environment and no host name',
  ]),
})

/** Every way the privacy page and the design disagree about the console's reporting, or [].
 *
 * `page` is the page's text and `design` the design document's, both as strings. The negative
 * half is the endpoint: the DSN is one public constant the app carries
 * (`console/src/main/crash-dsn.ts`), and it belongs in that file and in no statement — a page
 * that grew an ingest URL would be a page holding a value it has no reason to hold, and a
 * reader has nothing to do with one. */
export function consoleReportingFailures(page, design) {
  const out = []
  // A sentence the build wraps is still one sentence: both sides are read with their whitespace
  // collapsed, so a reflowed paragraph cannot fail a phrase that is on the page.
  page = page.replace(/\s+/g, ' ')
  design = design.replace(/\s+/g, ' ')
  for (const phrase of CONSOLE_REPORTING.both) {
    if (!design.includes(phrase)) {
      out.push(
        `the design's section 7.4 no longer says ${JSON.stringify(phrase)}: the privacy page repeats that section's sentence about the console's crash reporting, so a fact that leaves the design leaves the page stating something nothing in this repository holds`,
      )
    }
    if (!page.includes(phrase)) {
      out.push(
        `the privacy page does not say ${JSON.stringify(phrase)} about the console's crash reporting: the page states what the design's section 7.4 states, and a page that drops a fact of it discloses less than the console does`,
      )
    }
  }
  for (const phrase of CONSOLE_REPORTING.design) {
    if (!design.includes(phrase)) {
      out.push(
        `the design's section 7.4 no longer says ${JSON.stringify(phrase)}: that sentence is the rule the console's switch is built to, and the page is held to the two facts that carry it`,
      )
    }
  }
  for (const phrase of CONSOLE_REPORTING.page) {
    if (!page.includes(phrase)) {
      out.push(
        `the privacy page does not say ${JSON.stringify(phrase)} about the console's crash reporting: the switch is the menu item, it ships off, and it decides the next launch — a reader who is told the console can report crashes and not how to stop them is owed the sentence`,
      )
    }
  }
  if (/ingest[.-]/.test(page) || page.includes('sentry.io')) {
    out.push(
      'the privacy page carries an ingest endpoint: the DSN is one embedded public constant in console/src/main/crash-dsn.ts and the switch is the only gate, so the statement names the reporter and the region and never the endpoint the events are sent to',
    )
  }
  return out
}

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
    .filter((file) => !NOT_PRODUCTION.test(file.rel))
    .filter((file) => !HOST_NAMING_CONSOLE.includes(file.rel))
}

/** Every way an allow-listed file (`HOST_NAMING_CONSOLE`) carries a call that reaches a
 * service, or []. `files` is `[{ rel, text }]`; a file not on the allow-list is the reach scan's
 * business and not this one's. */
export function allowListedReachFailures(files) {
  const out = []
  for (const { rel, text } of files) {
    if (!HOST_NAMING_CONSOLE.includes(rel)) continue
    for (const call of REACH_CALLS) {
      if (text.includes(call)) {
        out.push(
          `${rel} carries \`${call}\` and is on the allow-list: the exemption is for a file that holds the project's own link and makes no request, and the host inside it is not scanned — so a call that reaches a service here is what the page's "to nothing the Fugaro project operates" is about, in the one place the scan cannot look`,
        )
      }
    }
  }
  return out
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

/**
 * A literal string, as a pattern that matches it and nothing else. The repository below is
 * interpolated into two patterns, and a repository URL is a string with `.` in it: unescaped,
 * `github.com` would match `githubXcom` and a link to somebody else's issue tracker would be
 * read as the owner's route, so the one link the section is allowed to carry is not the one
 * it has to carry.
 */
function escaped(text) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

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
  const repo = escaped(repository)
  const wanted = [new RegExp(`^${repo}/issues/?$`), new RegExp(`^${repo}/blob/main/SECURITY\\.md#.+$`)]
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

// ---- 8. the look ---------------------------------------------------------------------------
//
// The owner approved the bold redesign on 2026-10-20 (`website/design-ref/`, which carries the
// boards) and the look itself is written once, in `src/styles/base.css`'s section 2c: the
// display scale, the gradient, the card and the hero's glow. Every other page wears it, and the
// privacy statement is the page a store listing links — the one page a redesign could quietly
// leave behind, because it is a statement rather than a pitch and no board was drawn for it.
//
// **Two things are held, and they are different things.** The first is the markup: the page's
// headline is on the display scale and the word the boards put the accent on is in the
// gradient, its two part headings are on the display scale too, every subsection under them is
// one of the site's own cards, and the glow behind the headline is decoration a reader is not
// handed. The second is that the names it wears are SHAPES THE SITE DRAWS: every look class on
// the page is one `src/styles/base.css` defines, and the page draws none of them itself. The
// two halves are read from two places — the shared file, and what the build inlined into the
// page — because `base.css` is inlined into every page, so the page's own scoped rules and the
// shared ones end up side by side in one `<style>` where nothing can tell them apart. A class
// with no rule behind it is a name rather than a look, and a rule of the page's own is a second
// look: both are the shape of a page that grew its own.
//
// **What is not held is the rendering.** A gate cannot see a page, and the match to the owner's
// boards is the owner's check at the preview; this section holds the shape of the look and says
// so rather than pretending a string comparison is a design review.

/** The file the look is written in, from the repository's root: the shared layer's section 2c,
 *  and the only place a shape this page may name is defined. */
export const SHARED_LOOK = 'website/src/styles/base.css'

/** The display scale `base.css` defines: the headline steps the boards set. */
export const DISPLAY = Object.freeze(['display-xl', 'display-lg', 'display-md'])

/** The shapes the bold look is built from, by the class each is named. */
export const LOOK_SHAPES = Object.freeze([...DISPLAY, 'gradient-text', 'card', 'hero-glow'])

/** The classes on an element, as a list. */
const classListOf = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)

/**
 * The classes on an element that name the look: the shapes above, and any `display-*` at all —
 * a step nobody defined is exactly the defect the shape rule below is for, so the reader may
 * not filter it out before that rule sees it.
 */
function lookClassesOf(el) {
  return classListOf(el).filter((name) => LOOK_SHAPES.includes(name) || name.startsWith('display-'))
}

/** Whether `node` is `ancestor` or sits inside it. */
function within(node, ancestor) {
  for (let n = node; n; n = n.parent) if (n === ancestor) return true
  return false
}

/** The nearest ancestor of `el` that is a card, or null. */
function cardAround(el) {
  for (let n = el.parent; n; n = n.parent) if (classListOf(n).includes('card')) return n
  return null
}

/** The element's text as one line, for a failure line about it. */
const textOn = (el) => JSON.stringify(textOf(el).replace(/\s+/g, ' ').trim())

/** Every way the page's markup departs from the look, or []. */
export function lookFailures(html) {
  const main = mainOf(html)
  const els = [...walk(main)]
  const out = []

  const h1 = els.find((el) => el.name === 'h1')
  if (!h1) {
    out.push(
      `the privacy page has no h1: the page opens on its headline, and it is the headline the look is measured on (the bold redesign, website/design-ref/)`,
    )
  } else {
    if (!classListOf(h1).some((name) => DISPLAY.includes(name))) {
      out.push(
        `the privacy page's headline ${textOn(h1)} is not on the display scale: the bold look's headline is one of ${JSON.stringify(DISPLAY)} (base.css), the oversized, tightly tracked, heaviest-weight step the boards set — a headline left on the default scale is a page that did not get the redesign`,
      )
    }
    if (!els.some((el) => classListOf(el).includes('gradient-text') && textOf(el).trim() !== '' && within(el, h1))) {
      out.push(
        `the privacy page's headline carries no gradient: the emphasis the boards draw is the word in base.css's .gradient-text, inside the h1, and a headline of one flat tone is the look without its accent (the bold redesign, website/design-ref/)`,
      )
    }
  }

  for (const h2 of els.filter((el) => el.name === 'h2')) {
    if (classListOf(h2).some((name) => DISPLAY.includes(name))) continue
    out.push(
      `the privacy page's part heading ${textOn(h2)} is not on the display scale: a part of the statement is a section headline the boards set at display-lg (base.css), so the two parts read as the page's own division rather than as bold body text`,
    )
  }

  for (const h3 of els.filter((el) => el.name === 'h3')) {
    if (cardAround(h3)) continue
    out.push(
      `the privacy page's subsection heading ${textOn(h3)} stands on the page rather than in a card: every subsection of the statement is one of the site's own panels (base.css's .card), which is what makes the page's hierarchy a heading over its box — a run of headings over loose prose is the old look (website/design-ref/)`,
    )
  }

  for (const glow of els.filter((el) => classListOf(el).includes('hero-glow'))) {
    if (glow.attrs.get('aria-hidden') !== 'true') {
      out.push(
        `the privacy page's hero glow is not aria-hidden: base.css's .hero-glow paints no text, so it is decoration and a reader with a screen reader is owed nothing from it`,
      )
    }
    if (textOf(glow).trim() !== '') {
      out.push(
        `the privacy page's hero glow carries text (${textOn(glow)}): the layer behind the headline is decoration, and a word inside it is a word on the page that nothing styles`,
      )
    }
  }

  return out
}

/** The page's own stylesheets: what the build inlined, which is `base.css` plus the page's own
 *  scoped rules, and so what a browser reads when it draws the page. */
export function stylesheetsOf(html) {
  return findAll(parse(html), 'style')
    .map((el) => el.raw ?? '')
    .join('\n')
}

/** The class names a stylesheet draws: every class its selectors name. */
function drawnIn(css) {
  const drawn = new Set()
  for (const rule of cssRules(css)) {
    for (const m of rule.selector.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) drawn.add(m[1])
  }
  return drawn
}

/**
 * Every look class the page names that the look does not draw, or [].
 *
 * **`shared` is the file the look is written in** — `src/styles/base.css` — and `own` is what
 * the build inlined into this page, which is that file plus the page's own scoped rules. The
 * two are read apart on purpose. Reading the built page alone would count a class as drawn by
 * ANY rule in it, the page's own included, and a page that wrote its own `.display-xxl` — a
 * second headline scale, which is the exact defect this rule exists for — would pass: the
 * page's rules and the shared ones are inlined side by side in the same `<style>`, and no
 * reader of the artifact alone can tell one from the other. So a class is a shape of the look
 * when the shared file draws it, and a class the page draws ITSELF is refused as a second look
 * rather than admitted as a shape.
 */
export function shapeFailures(html, shared, own = '') {
  const named = new Set()
  for (const el of walk(parse(html))) for (const name of lookClassesOf(el)) named.add(name)
  const sharedDrawn = drawnIn(shared)
  const ownDrawn = drawnIn(own)
  return [...named]
    .filter((name) => !sharedDrawn.has(name))
    .sort()
    .map((name) =>
      ownDrawn.has(name)
        ? `the privacy page draws .${name} itself: the look is written once, in src/styles/base.css, and a headline scale or a card of the page's own is a second look rather than the site's — which is why this rule reads the shared file and not only the built page, where the two are inlined side by side (the bold redesign, website/design-ref/)`
        : `the privacy page wears .${name}, which nothing draws: the look is written once, in src/styles/base.css, and a page names the shapes that file defines — a class with no rule behind it draws nothing (the bold redesign, website/design-ref/)`,
    )
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

  // What the selector reads before the allow-list takes its two files out. An entry that the scan
  // would never read anyway exempts nothing, and the `every` below would not say so: it is true of
  // every path outside `SOFTWARE_ROOTS` — `console/package.json` names the host today and is not
  // read — and of every test. So the check is the selector's own reading and not a prefix test.
  const selected = new Set(
    SOFTWARE_ROOTS.flatMap((dir) => sourcesUnder(dir, REACH_BEARING, REPO))
      .filter((file) => !NOT_PRODUCTION.test(file.rel))
      .map((file) => file.rel),
  )
  for (const rel of HOST_NAMING_CONSOLE) {
    assert.ok(existsSync(path.join(REPO, rel)), `the recorded exception ${rel} is gone: delete it from HOST_NAMING_CONSOLE and its note in website/test/privacy.mjs, so the allow-list names only files that exist`)
    assert.ok(
      selected.has(rel),
      `${rel} is on the allow-list and the reach scan would never read it: an entry exempts a file the rule already skips, so the exemption hides nothing and the note beside it reads as a live exception (the entries are the production sources the selector reads and nothing else)`,
    )
    assert.ok(repoFile(rel).includes(FACTS.canonicalHost), `${rel} no longer names ${FACTS.canonicalHost}: it is on the allow-list for the console's own link to the project's website, and a file that has stopped naming the host is an entry that hides nothing`)
    assert.ok(
      files.every((file) => file.rel !== rel),
      `${rel} is one of the two recorded exceptions and was scanned as an ordinary production source: the console's link to the project's own website is not a host the software reports to, so the scan must skip it`,
    )
  }
  // The exemption is the file's own link and not a licence to reach: the scan cannot see the
  // host inside an allow-listed file, so a request grown there would be invisible to the rule
  // this file exists for. The two are read for the ways a source reaches a service.
  const reaching = allowListedReachFailures(HOST_NAMING_CONSOLE.map((rel) => ({ rel, text: repoFile(rel) })))
  assert.deepEqual(reaching, [], reaching.join('\n'))
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

test("the privacy page wears the site's bold look, out of the shapes the site draws", () => {
  const page = privacyPage()
  const failures = lookFailures(page.html)
  assert.deepEqual(failures, [], `the privacy page is not wearing the look:\n  ${failures.join('\n  ')}`)

  // The shape rule reads two things: the file the look is written in, and what the build
  // inlined into this page. Both are worth their answer only if they carry the look — a shared
  // file that had lost `base.css`'s section, or a page the build inlined nothing into, would
  // make the rule silent rather than strict.
  const shared = repoFile(SHARED_LOOK)
  assert.match(
    shared,
    /\.card\s*[,{]/,
    `${SHARED_LOOK} carries no \`.card\` rule: the shape rule reads the file the look is written in, and a shared stylesheet without it is a rule that can say nothing about any shape`,
  )
  const inlined = stylesheetsOf(page.html)
  assert.match(
    inlined,
    /\.card\s*[,{]/,
    'the built privacy page carries no `.card` rule: `base.css` is inlined into every built page, so a page with none inlined is a page whose shapes did not reach the artifact',
  )
  const shapes = shapeFailures(page.html, shared, inlined)
  assert.deepEqual(shapes, [], shapes.join('\n'))
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
  // The app declares the reporters since P7.1a, which satisfies direction B on its own, so the
  // fixture removes them from the dependency list to plant the state this half refuses.
  const undeclared = JSON.parse(packageJSON)
  for (const { pkg } of APP_TELEMETRY) delete undeclared.dependencies?.[pkg]
  const noVersion = page.replace(/Earlier builds of the app send nothing at all\./, 'The app sends these reports.')
  const unbacked = telemetryFailures({ page: noVersion, packageJSON: JSON.stringify(undeclared), planExists: false })
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

  // The claims the reader is owed, one planted omission at a time: the ban list, the absence of
  // a switch in the versions that carry the reports, what a reader can do instead, what a reader
  // who has not installed the app gets, the two other tools carrying none of the reports, and
  // the link to Google's own policy. Each case asserts the refusal names the claim it planted
  // absent, and not merely that something failed: a case that only counts failures proves a
  // sentence is refused by *a* rule, which is not the same as the rule written to hold it.
  for (const [what, gone, claim] of [
    ['the ban list', page.replaceAll('Never sent:', 'Also sent:'), 'Never sent'],
    ['the absence of a switch', page.replaceAll('no switch inside the app', 'a switch in the settings'), 'no switch inside the app'],
    ['what a reader can do instead', page.replaceAll('uninstalling the app stops them', 'the app stops them'), 'uninstalling the app stops them'],
    ['what not installing the app avoids', page.replaceAll('not installing it avoids them', 'installing it is what avoids them'), 'not installing it avoids them'],
    ['the two other tools carrying none of the reports', page.replaceAll('carry none of these reports', 'send nothing of their own'), 'carry none of these reports'],
    ['Google as the holder', page.replaceAll('Google holds', 'It is held'), 'Google holds'],
  ]) {
    const failures = telemetryFailures({ ...base, page: gone })
    assert.ok(
      failures.some((line) => line.includes(`does not say ${JSON.stringify(claim)}`)),
      `a page that lost ${what} was not refused for that claim: ${failures.join('\n')}`,
    )
  }
  // And the clause about the other tools, held to its SENTENCE and not to its phrase. The case
  // above plants the phrase absent; this one keeps the phrase and swaps the subject for the
  // website, which is the defect the always-on round fixed and which the phrase alone let back
  // in: the sentence reads "The website and its pages carry none of these reports." and every
  // check that only asked whether the phrase was on the page stayed green.
  const websiteSubject = page.replace(
    'The command line tool and the console are unaffected: they carry none of these reports.',
    'The website and its pages carry none of these reports.',
  )
  assert.notEqual(websiteSubject, page, 'the fixture did not replace the clause, so this case proves nothing')
  const websiteFailures = telemetryFailures({ ...base, page: websiteSubject })
  for (const tool of ['command line tool', 'console']) {
    assert.ok(
      websiteFailures.some((line) => line.includes(`does not name the ${tool}`)),
      `a clause that stopped naming the ${tool} was not refused: ${websiteFailures.join('\n')}`,
    )
  }
  assert.ok(
    websiteFailures.some((line) => /clause about the other tools names the website/.test(line)),
    `a software-part clause calling the website unmeasured was accepted, which is the page contradicting its own first half: ${websiteFailures.join('\n')}`,
  )

  // The two halves on their own, so neither is the other's to prove: a clause that names the
  // website BESIDE the two tools keeps both tool names and is still refused for its subject,
  // and a clause that names the two tools and no website at all is not refused for anything.
  const withWebsite = page.replace(
    'The command line tool and the console are unaffected: they carry none of these reports.',
    'The website, the command line tool and the console are unaffected: they carry none of these reports.',
  )
  assert.notEqual(withWebsite, page, 'the fixture did not add the website to the clause, so this case proves nothing')
  const withWebsiteFailures = telemetryFailures({ ...base, page: withWebsite })
  assert.deepEqual(
    withWebsiteFailures.filter((line) => /does not name the/.test(line)),
    [],
    `a clause naming the command line tool and the console was refused for not naming them: ${withWebsiteFailures.join('\n')}`,
  )
  assert.ok(
    withWebsiteFailures.some((line) => /clause about the other tools names the website/.test(line)),
    `a clause that names the website beside the two tools was accepted: ${withWebsiteFailures.join('\n')}`,
  )
  assert.deepEqual(
    telemetryFailures({ ...base, page }).filter((line) => /clause about the other tools/.test(line)),
    [],
    'the shipped clause names the two tools and not the website, and the rule refuses it anyway',
  )

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
  // The section replaced with the old page's own sentences, the ones this rule was written
  // for. The fixture reads to the end of the paragraph rather than to a sentence inside it,
  // so the shipped wording can move without the fixture going silently inert — the failure
  // below says so when it does not match.
  const old = page.replace(
    /Everything else the software holds[\s\S]*?<\/p>/,
    'Everything the software holds lives in your own accounts, your own cloud project and your own devices. Removing the installation removes it.</p>',
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

test('the console’s two recorded exceptions are exactly that, and a third file that names the host is still refused', () => {
  const host = FACTS.canonicalHost

  // The allow-list is two full paths and not a directory, a pattern or a suffix: a new file that
  // names the host, even one beside a recorded exception under `console/src/main/`, is selected
  // and refused. This is the case that keeps HOST_NAMING_CONSOLE from becoming a hole.
  const tree = mkdtempSync(path.join(tmpdir(), 'privacy-reach-console-'))
  try {
    const root = path.join(tree, 'console', 'src', 'main')
    mkdirSync(root, { recursive: true })
    writeFileSync(path.join(root, 'telemetry.ts'), `export const REPORT_URL = 'https://${host}/v1/report'\n`)
    const scanned = softwareSources([root], tree).map((file) => file.rel)
    assert.deepEqual(
      scanned,
      ['console/src/main/telemetry.ts'],
      `the reach scan selected ${JSON.stringify(scanned)}: a console file that is not one of the two recorded exceptions must be selected, so that naming the host there is a finding`,
    )
    const planted = reachFailures(softwareSources([root], tree), host)
    assert.equal(planted.length, 1, planted.join('\n'))
    assert.match(planted[0], /^console\/src\/main\/telemetry\.ts names /)
    assert.match(planted[0], /nothing the Fugaro project operates/)
  } finally {
    rmSync(tree, { recursive: true, force: true })
  }

  // And in the real tree the exemption is those two files rather than the console: the console's
  // other sources are still read, and none of them names the host.
  const consoleSources = softwareSources().filter((file) => file.rel.startsWith('console/src/'))
  assert.ok(
    consoleSources.length > 5,
    `only ${consoleSources.length} console sources were scanned: skipping the two recorded exceptions must not skip the console`,
  )
  assert.deepEqual(
    consoleSources.filter((file) => file.text.includes(host)).map((file) => file.rel),
    [],
    `a console source other than the two recorded exceptions names ${host}: the allow-list is those two files, and nothing about the directory they sit in`,
  )
})

test('a call that reaches a service inside an allow-listed console file is refused', () => {
  const host = FACTS.canonicalHost
  const link = `export const GETTING_STARTED_URL = 'https://${host}/getting-started/'\n`
  // The link alone is what the allow-list is for, and it passes.
  assert.deepEqual(allowListedReachFailures([{ rel: HOST_NAMING_CONSOLE[0], text: link }]), [])
  // Each way of reaching a service, grown inside either allow-listed file, is refused by name.
  for (const rel of HOST_NAMING_CONSOLE) {
    for (const call of REACH_CALLS) {
      const text = `${link}export function report(body) { return ${call === 'fetch(' ? `fetch('https://${host}/v1/report', { method: 'POST', body })` : `${call} /* https://${host}/v1/report */`} }\n`
      const failures = allowListedReachFailures([{ rel, text }])
      assert.equal(failures.length, 1, `${rel} carrying \`${call}\` was not refused: ${JSON.stringify(failures)}`)
      assert.ok(failures[0].startsWith(`${rel} carries \`${call}\``), failures[0])
    }
  }
  // The same call in a file off the allow-list is the reach scan's to refuse, not this check's.
  assert.deepEqual(allowListedReachFailures([{ rel: 'console/src/main/telemetry.ts', text: `fetch('https://${host}/')` }]), [])
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

test('the contact route is matched literally, not as a pattern', () => {
  // The repository is interpolated into two patterns and it is a URL, so it carries `.`:
  // read as a pattern, `github.com` matches `githubXcom` and a link to a stranger's issue
  // tracker is taken for the owner's route. A slug with a dot in it is a slug a repository
  // can have, which is why the string is escaped rather than assumed to be pattern-free.
  const repo = 'https://github.com/fugaro/fugaro.v2'
  const good = `<h2>Contact</h2><p>Ask in the <a href="${repo}/issues">issue tracker</a>, or report through <a href="${repo}/blob/main/SECURITY.md#reporting-a-vulnerability">SECURITY.md</a>.</p>`
  assert.deepEqual(contactFailures(SHELL(`<h1>Privacy</h1>${good}`), repo), [])

  const decoy = `${repo.replace(/\.v2$/, 'Xv2')}/issues`
  const failures = contactFailures(SHELL(`<h1>Privacy</h1>${good.replace('</p>', `<a href="${decoy}">help</a></p>`)}`), repo)
  assert.equal(failures.length, 1, failures.join('\n'))
  assert.match(failures[0], /links https:\/\/github\.com\/fugaro\/fugaroXv2\/issues: the owner's ruling/)
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

test('a headline off the display scale, or a gradient that left the headline, is refused', () => {
  // The look in miniature: a page whose headline is plain text, one whose gradient sits on the
  // lead instead of on the headline, and the page the site builds, which is neither.
  const parts = '<h2 class="display-lg">The website</h2><div class="card"><h3>a</h3></div><h2 class="display-lg">The software</h2><div class="card"><h3>b</h3></div>'

  const plain = SHELL(`<h1>Privacy</h1>${parts}`)
  assert.match(lookFailures(plain).join('\n'), /headline "Privacy" is not on the display scale/)

  const gilded = SHELL(`<h1 class="display-xl">Privacy</h1>${parts}`)
  assert.match(lookFailures(gilded).join('\n'), /headline carries no gradient/)

  // The gradient on the page and not on the headline: the accent is the headline's, so a
  // `.gradient-text` somewhere else in `main` is not the look.
  const elsewhere = SHELL(`<h1 class="display-xl">Privacy</h1><p class="gradient-text">the lead</p>${parts}`)
  assert.match(lookFailures(elsewhere).join('\n'), /headline carries no gradient/)

  const good = SHELL(`<h1 class="display-xl"><span class="gradient-text">Privacy</span></h1>${parts}`)
  assert.deepEqual(lookFailures(good), [])

  // And a page with no headline at all: the look is measured on the `h1`, so a page that lost
  // one is refused before either rule above can say anything about a scale or a gradient.
  const headless = SHELL(`<p>Privacy</p>${parts}`)
  assert.match(lookFailures(headless).join('\n'), /has no h1/)
})

test('a part heading off the display scale, a subsection outside a card, and a glow a reader is handed are refused', () => {
  const head = '<h1 class="display-xl"><span class="gradient-text">Privacy</span></h1>'
  const good = SHELL(`${head}<h2 class="display-lg">The website</h2><div class="card"><h3>a</h3></div><h2 class="display-lg">The software</h2><div class="card"><h3>b</h3></div>`)
  assert.deepEqual(lookFailures(good), [])

  const flat = SHELL(`${head}<h2>The website</h2><div class="card"><h3>a</h3></div><h2 class="display-lg">The software</h2><div class="card"><h3>b</h3></div>`)
  assert.match(lookFailures(flat).join('\n'), /part heading "The website" is not on the display scale/)

  // An `h3` under the part heading and outside any card: the heading over loose prose the
  // redesign replaced.
  const loose = SHELL(`${head}<h2 class="display-lg">The website</h2><h3>a</h3><p>prose</p><h2 class="display-lg">The software</h2><div class="card"><h3>b</h3></div>`)
  assert.match(lookFailures(loose).join('\n'), /subsection heading "a" stands on the page rather than in a card/)

  const announced = SHELL(`${head}<div class="hero-glow">grid</div><h2 class="display-lg">The website</h2><div class="card"><h3>a</h3></div><h2 class="display-lg">The software</h2><div class="card"><h3>b</h3></div>`)
  const glow = lookFailures(announced).join('\n')
  assert.match(glow, /hero glow is not aria-hidden/)
  assert.match(glow, /hero glow carries text/)

  const hidden = SHELL(`${head}<div class="hero-glow" aria-hidden="true"></div><h2 class="display-lg">The website</h2><div class="card"><h3>a</h3></div><h2 class="display-lg">The software</h2><div class="card"><h3>b</h3></div>`)
  assert.deepEqual(lookFailures(hidden), [])
})

test('a look class nothing draws is refused, and a class the page draws itself is refused as a second look', () => {
  // The shared file and the page are written here rather than taken from the artifact, so the
  // premise of this case is its own: a page that grew a `display-xxl` rule fails it whichever
  // way the site's own stylesheets move, and it fails with the named refusal rather than a
  // count. `good` is the page the site builds, in miniature.
  const shared =
    '.display-xl{font-size:clamp(1rem, 7.2vw, 6.5rem)}.display-lg{font-size:clamp(1rem, 4.4vw, 4.5rem)}' +
    '.gradient-text{background-clip:text}.card{padding:2rem}.hero-glow{position:absolute}'
  const good = '<h1 class="display-xl"><span class="gradient-text">t</span></h1><div class="card"><h3>a</h3></div><div class="hero-glow" aria-hidden="true"></div>'
  assert.deepEqual(shapeFailures(SHELL(good), shared, shared), [])

  // A step nobody defined: the class is a name, and a name draws nothing.
  const undrawn = shapeFailures(SHELL('<h1 class="display-xxl">t</h1>'), shared, shared)
  assert.equal(undrawn.length, 1, undrawn.join('\n'))
  assert.match(undrawn[0], /wears \.display-xxl, which nothing draws/)

  // And the page that answers that refusal by writing the rule itself: the class is drawn, and
  // by the page rather than by the look. This is the defect the shared file is read for — the
  // page's own rules and the shared ones are inlined side by side, so a reader of the built
  // page alone would count this rule as a shape and pass it.
  const invented = shapeFailures(SHELL('<h1 class="display-xxl">t</h1>'), shared, `${shared}\n.display-xxl{font-size:9rem}`)
  assert.equal(invented.length, 1, invented.join('\n'))
  assert.match(invented[0], /draws \.display-xxl itself/)

  // The other half of the same reading: with no shared file behind it, every shape the page
  // names is refused, which is what makes the gate's two control assertions worth their answer.
  const nothing = shapeFailures(SHELL('<h1 class="display-xl">t</h1>'), '', '')
  assert.equal(nothing.length, 1, nothing.join('\n'))
  assert.match(nothing[0], /wears \.display-xl, which nothing draws/)
})

test("the console's crash reporting is the design's sentence, the switch and all", () => {
  // The console is the second artifact in this page that can send anything, and the one that
  // leaves it to the person: the switch ships off, it decides the next launch, and the report
  // is scrubbed to the exception's type and the app's own frames. The design's section 7.4 is
  // the sentence, and this holds the page to saying the same thing rather than a summary of it.
  const design = readFileSync(path.join(REPO, CONSOLE_DESIGN), 'utf8')
  assert.deepEqual(consoleReportingFailures(privacyPage().html, design), [])

  // And the red direction, phrase by phrase: dropping the switch's own name, the region, the
  // scrubbing or the fact that it is off until the person ticks it is refused, and refused for
  // the page rather than for the design. Every occurrence goes, because the page states some of
  // these facts twice — "the next launch" is in the sentence that turns reporting on and in the
  // sentence that says unticking stops it — and a case that removed one of the two would be
  // asking about a page that still carries the fact.
  const page = privacyPage().html
  for (const phrase of [...CONSOLE_REPORTING.page, ...CONSOLE_REPORTING.both]) {
    const without = consoleReportingFailures(page.split(phrase).join(''), design)
    assert.ok(
      without.some((line) => line.includes(JSON.stringify(phrase)) && line.includes('privacy page')),
      `a page that dropped ${JSON.stringify(phrase)} was not refused: the console's reporting sentence is only as good as every fact of it`,
    )
  }

  // The other side of the split: the design's own wording for the rule is refused when it leaves
  // the design, which is where it is decided — the page is not asked for that sentence.
  for (const phrase of CONSOLE_REPORTING.design) {
    const without = consoleReportingFailures(page, design.replace(phrase, ''))
    assert.ok(
      without.some((line) => line.includes(JSON.stringify(phrase)) && line.includes('design')),
      `a design that dropped ${JSON.stringify(phrase)} was not refused: the switch's opt-in rule is the design's to state`,
    )
  }

  // A page that grew the endpoint the events are sent to is refused as well: the DSN is one
  // embedded public constant in `console/src/main/crash-dsn.ts`, and the statement names the
  // reporter and the region and never the value.
  const withEndpoint = consoleReportingFailures(`${page} <p>It posts each report to https://o000000.ingest.example.invalid/api/1/envelope/</p>`, design)
  assert.ok(
    withEndpoint.some((line) => line.includes('ingest endpoint')),
    'a privacy page carrying an ingest endpoint was not refused: the constant lives in one file of the console and the page states facts, not the value',
  )
})
