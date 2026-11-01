# Dependency and distribution audit

Read-only. Nothing in this document changes a dependency, a licence or a build, and no test enforces
the classifications below except the two hermetic rules `internal/eeboundary/licence_test.go` states
(a copyleft identifier in either lockfile, and a dependency declaring no licence at all). What is
here is what somebody would otherwise have to derive by hand: what the console and the mobile app
depend on, what each dependency is licensed under, which declarations oblige something, and what is
left for counsel.

Written 2026-10-27, against `console/package-lock.json` and `mobile/package-lock.json` as they stand
at that date. Both are `lockfileVersion` 3.

**The console's half was revised 2026-10-31**, for the crash reporter's dependency (desktop-console
plan Task 2.1b, D733): `@sentry/electron` 8.2.0 and its tree, and with it the console's second
runtime dependency. **Its shipped set was revised again the same day**, by Task 2.1b's fix round A:
`@sentry/bundler-plugins` and everything only that subpath reaches are excluded by name in
`build.files`, because nothing in them loads at run time, so the tree this revision counted as
shipped is smaller than it says below. The licence table is still the whole lockfile, which is what
it was always about, and the console's counts are read off the lockfile of that date. The mobile app's counts are still the 2026-10-27 ones and have drifted since — nothing in this
revision looked at `mobile/`.

## Method

Every number below was read out of the two lockfiles on disk. Nothing was installed, no registry was
contacted, and no `node_modules` was read: a v3 lockfile carries each package's `license` field, and
that field is the declaration this audit classifies. The tables below are a tally of that field, read
off the two files and recorded as of 2026-10-27; no command prints them, and this section does not
claim one does. What the rule enforces, it enforces over the same two files read the same way:
`go test ./internal/eeboundary/ -run Licen -count=1 -v` prints, for each lockfile, every copyleft
identifier it found there and allowed by name.

The two things this method cannot see, stated rather than left implied:

- **A lockfile is a declaration, not a licence text.** A package whose `license` field says `MIT` and
  whose shipped bytes carry someone else's notice is not caught here. Nothing in either lockfile is
  suspected of that; it is a limit of the method.
- **A lockfile says who is installed, not what ships.** The `dev` flag means "no production
  dependency path needs this", and that is not the same question as "is this in the artifact". The
  console's lockfile marks 349 of its 414 packages `dev` — tooling, the test runners, the packer —
  and `elkjs` is one of those `dev` packages whose file nevertheless ends up in the asar. Where the
  distinction matters below, it is called out.

## The console, `console/`

414 packages, 10 of them direct, 8 in `devDependencies` and 2 in `dependencies`:

| Licence | Packages |
| --- | ---: |
| MIT | 295 |
| ISC | 37 |
| Apache-2.0 | 34 |
| MPL-2.0 | 12 |
| BlueOak-1.0.0 | 11 |
| BSD-3-Clause | 10 |
| BSD-2-Clause | 7 |
| `WTFPL OR ISC` | 1 |
| `WTFPL OR MIT` | 1 |
| WTFPL | 1 |
| `MIT OR CC0-1.0` | 1 |
| 0BSD | 1 |
| Python-2.0 | 1 |
| EPL-2.0 | 1 |
| FSL-1.1-Apache-2.0 | 1 |
| **no licence declared** | **0** |

What ships. `package.json`'s `build.files` packs an explicit list out of `dist/` plus `LICENSE.md`
and `THIRD_PARTY_NOTICES.md`; nothing else under `console/` reaches the package, and
`console/test/unit/build-files.test.ts` reads the requirement off the tree rather than restating it.
electron-builder also packs the production `node_modules` subtree, which is what the two runtime
dependencies below are for: the console's own code imports only `node:` builtins, `electron`,
`@electron/fuses`, `electron-updater` and `@sentry/electron`, and only the last two are declared
runtime dependencies, each reached from exactly one file under `src/main/`.
`@electron/fuses`'s import lives in `dist/main/fuses.js`, which `build.files` excludes by name. So
the asar holds the console's own compiled output, one copied third-party file
(`dist/bundle/elk-worker.min.js`), the two runtime dependencies with their trees, and the Electron
runtime that electron-builder puts around it.

**The two runtime dependencies, and why each is there.**

| Package | Version | Declared | Reached from | Why |
| --- | --- | --- | --- | --- |
| `electron-updater` | 6.8.10 | MIT | `src/main/updater-electron.ts` | The update client the console ships update checks and installs through (Task 4.4) |
| `@sentry/electron` | 8.2.0 | MIT | `src/main/crash-electron.ts` | The crash reporter the owner approved in D733, wired behind a per-user switch that is off by default (Task 2.1b) |

Neither is copyleft, and neither is in the table below. What both bring in is a tree of their own,
packed beside them and reproduced section by section in `console/THIRD_PARTY_NOTICES.md` — sixteen
packages for the update client and fourteen for the crash reporter, and **no section for
`has-flag` 4.0.0**: the name reaches the lockfile through the crash reporter's bundler subpath, and
the packer would hoist it (with `supports-color`) out of that excluded tree, because the update
client's own `debug` does an optional `require('supports-color')` that nothing declares and that
`debug` catches and survives. `build.files` refuses both by name, so neither is packed, neither is
shipped and neither is described: **`has-flag` is excluded, not shipped** (fix round B of
Task 2.1b, and `console/test/unit/build-files.test.ts` holds the exclusion). Every version in those sections is the one the lockfile pins, and since
Task 2.1b's fix round A the file is no longer read off a machine: `console/test/unit/notices.test.ts`
derives the shipped set from `package-lock.json` for each of the three targets the console is built
for — darwin arm64 and x64 in the universal bundle, and linux x64 — and compares it with the file's
sections in both directions, so a section cannot outlive the package it describes and a package that
ships cannot lose its section.

**And two of those packages carry other projects' code inside them.** `@sentry/server-utils` and
`@sentry/server-runtime-injection` — two of the crash reporter's fourteen — each ship a `vendored/`
directory under `build/cjs/` and `build/esm/` holding thirteen other projects' sources in all: the
`@apm-js-collab` transformer and its bundler plugins and tracing hooks, `astring`, `debug`,
`esquery`, `has-flag`, `meriyah`, `module-details-from-path`, `ms`, `semifies`, `source-map` and
`supports-color`. Those are not packages of the archive, so they have no section of their own: the
notices file's last section lists them with the versions and licences their packages' own
`build/THIRD-PARTY-LICENSES.txt` — which both package and pack — states, and reproduces their
licence texts. All open source; the one thing that has no text anywhere is
`@apm-js-collab/code-transformer-bundler-plugins`, whose shipped entry declares MIT and carries no
licence body, which the notices file records and which is with counsel. A third package,
`@sentry/node` itself, carries three vendored files under `build/*/integrations/fs/vendored/`,
and it attributes them a different way: the built files carry no banner and the package ships no
`THIRD-PARTY-LICENSES.txt`, but the `sourcesContent` of the `.js.map` files beside them opens with
the vendored source's own header, which names `@opentelemetry/instrumentation-fs@0.37.0`, the
OpenTelemetry Authors and Apache-2.0, vendored from `opentelemetry-js-contrib@15ef750`. The
notices file states that and points at the Apache-2.0 text it already reproduces.

**One licence in the console's tree is not open source, and it is not in the package.** `sentry`
0.45.0 — declared `FSL-1.1-Apache-2.0`, the Functional Source License 1.1 with a two-year conversion
to Apache-2.0 — arrives with `@sentry/bundler-plugins` 11.7.0, which `@sentry/node` depends on and
which the console neither calls nor configures: nothing in `console/src/` bundles anything with
Sentry's build plugins. It was installed *and packed* until Task 2.1b's fix round A, which excludes
that subpath's whole tree by name in `build.files` (nothing in the tree loads at run time, which the
round's probe shows), so no distributed artifact carries it and `THIRD_PARTY_NOTICES.md` no longer
reproduces its text. The fact stays recorded here because it is the one licence in the console's
lockfile that restricts what a licensee may do rather than only what they must reproduce:
`copyleftPattern` in `internal/eeboundary/licence_test.go` does not match it — FSL is not a copyleft
family — so the rule passes and this paragraph is what records it.

**Copyleft, and what each obliges.**

| Package | Version | Declared | In the package? |
| --- | --- | --- | --- |
| `elkjs` | 0.11.0 | EPL-2.0 | **Yes** — `dist/bundle/elk-worker.min.js` is copied verbatim by the bundler |
| `lightningcss` and its 11 platform binaries | 1.33.0 | MPL-2.0 | No — build-time CSS for the console's own stylesheet |

`elkjs` is the one that matters, it is already handled, and it is handled in the right place:
`console/THIRD_PARTY_NOTICES.md` names the file, the exact version, the tarball it came from, the
upstream repository, and carries the full EPL-2.0 text. EPL-2.0 §3.1 asks a distributor to make the
Program available as Source Code and to say how to get it, and §3.3 asks that the Program's notices
not be removed; the notices file is packed (`build.files` carries it) and names the source, which is
what those two clauses are for. Whether it is *enough* is counsel's call, not this audit's — the two
gaps it leaves are listed under "Left for counsel" below.

`lightningcss` (MPL-2.0, file-level copyleft) is a build tool: it minifies and transforms the
console's own `.css`, so its source is not in the artifact and MPL-2.0's obligations attach to
`lightningcss`'s own files, not to the stylesheet it produced. No notices entry is needed for it. The
package is not shipped and no file of it is copied.

**No package in the console's lockfile declares no licence.**

**Five moderate advisories at the lockfile level, and none of them in the shipped set.** `npm audit
--omit=dev` on 2026-10-31 answers five moderate, no high and no critical, over five entries and two
underlying advisories: `minimatch`'s globparts amplification (a denial of service) and `glob`'s
`AbortSignal` cancellation bypass (a post-timeout filesystem traversal, reported against `glob`
*through* `minimatch`). The other three entries are the propagation path rather than a second
problem — `@sentry/bundler-plugins`, which depends on them, and `@sentry/node` and
`@sentry/electron`, which depend on it. Neither advisory is in the artifact: `glob`, `minimatch` and
`@sentry/bundler-plugins` are all excluded by name in `build.files` (Task 2.1b's fix round A), so no
distributed file carries the code either advisory is about. The two entries that do ship,
`@sentry/node` and `@sentry/electron`, carry no advisory of their own — npm reports them because a
dependency below them does — and the console calls neither build-plugin subpath. This paragraph is
the read of that command on that date, not a standing claim: `npm audit` is a query against the
registry's advisory database, and a package that gains an advisory later arrives here as a new
paragraph rather than as a change to this one.

## The mobile app, `mobile/`

488 packages, 6 of them direct, all 6 in `dependencies`:

| Licence | Packages |
| --- | ---: |
| MIT | 420 |
| ISC | 28 |
| MPL-2.0 | 12 |
| BSD-3-Clause | 6 |
| BlueOak-1.0.0 | 6 |
| Apache-2.0 | 5 |
| `MIT OR CC0-1.0` | 2 |
| Unlicense | 2 |
| BSD-2-Clause | 2 |
| `MIT OR Apache-2.0` | 1 |
| `BSD-3-Clause OR GPL-2.0` | 1 |
| 0BSD | 1 |
| CC-BY-4.0 | 1 |
| Python-2.0 | 1 |
| **no licence declared** | **0** |

Direct dependencies, all MIT: `@react-native-async-storage/async-storage` 2.2.0, `expo` 58.0.16,
`expo-secure-store` 58.0.2, `expo-splash-screen` 58.0.10, `react` 19.3.0, `react-native` 0.88.0.

**Copyleft and attribution declarations, and who pulls each in.**

| Package | Version | Declared | Required by | Notes |
| --- | --- | --- | --- | --- |
| `lightningcss` and its 11 platform binaries | 1.33.0 | MPL-2.0 | `@expo/metro-config` | A build-time CSS transformer in the Metro pipeline |
| `node-forge` | 1.4.0 | `BSD-3-Clause OR GPL-2.0` | `@expo/cli`, `@expo/code-signing-certificates` | A choice of two licences; the audit reads the app as taking BSD-3-Clause, so no GPL obliges anything. `copyleftAllowed` in `internal/eeboundary/licence_test.go` names it with this reason, so the identifier is visible in the diff rather than hidden by a smarter parser |
| `caniuse-lite` | 1.0.30001816 | CC-BY-4.0 | `browserslist` | Browser-support data, not code. CC-BY-4.0 asks for attribution, so if its data reached the artifact it would want a notice — as a `browserslist` input it does not |
| `react-native`, `expo`, `react` and their transitive MIT/ISC/Apache-2.0 tree | — | permissive | the app | All MIT/ISC/Apache-2.0/BSD-family; nothing here obliges anything beyond keeping the notices |

The mobile lockfile marks all 488 packages production (`dev: false`), and that flag should not be read
as "all 488 ship". The packages above that are plainly build-time — `@expo/cli`, `@expo/metro-config`,
`browserslist` — are in the production graph because `expo` depends on them, and Metro bundles only
what the app's code imports. **Which of the 488 end up inside the shipped JS bundle is not something
a lockfile can answer**, and this audit does not claim to: it would take a Metro bundle and a read of
its module map. That is a gap, not a finding.

**No package in the mobile lockfile declares no licence.**

## `console/THIRD_PARTY_NOTICES.md`, and where it is stale

The file exists, it is packed (`build.files` carries it), and `LICENSING.md` links it. What it says
about `elkjs` is accurate today: version, file, path in the package, tarball URL, upstream repository,
and the licence text verbatim.

It is stale in three ways, none of them about `elkjs`:

1. **The artifact is an Electron app, and Electron's own notices are not in this file.** The
   packaging is an Electron one: `electron` 45.0.0 is pinned in `devDependencies` and in the
   `electronDownload.checksums` block, and electron-builder wraps the asar in Electron's own runtime —
   Chromium, Node and their third-party notices. The file's opening paragraph counts what the console
   ships *beside* that runtime and cannot count what the runtime itself carries, so a reader who
   stops there stops one layer short. (It said "one file that is not ours" until Task 4.4, which is
   also when this item stopped being a quotation from it and became a statement about the artifact.)
   Electron's own notice
   obligations are met by Electron's own distribution (its `LICENSE` and `LICENSES.chromium.html`
   travel with the runtime), but a reader of *this* file cannot tell that.
2. **The file carries no copyright or attribution line for `elkjs`.** The EPL-2.0 text copied into it
   is the licence boilerplate, whose Exhibit A is a blank form; EPL-2.0 §3.3 asks that the notices
   contained within the Program not be removed or altered. Whether a distributor is expected to
   reproduce the package's own copyright line, and which line that is, is a question for counsel
   rather than a conclusion here.
3. **It does not name `LICENSE.md` as the console's own licence.** The file sits beside the licence
   the console is under and says nothing about it; `LICENSING.md` and `console/package.json`'s
   `license` field carry that fact instead. Minor, and arguably correct — the file is about third
   parties — but a reader who opens it first learns nothing about the terms the rest of the package
   is under.

**The mobile app has no third-party notices file at all.** `mobile/` carries `LICENSE.md` and no
`THIRD_PARTY_NOTICES.md`, and the app ships expo, React Native, React and their tree. The mobile app
has never been released (it has no release tag and `app.json` says version `0.0.0`), so nothing is
out of compliance yet — but a store submission is the moment that changes, and the file would have to
be written before it. `eas.json` already has a `submit.internal` block naming the App Store
(`ascAppId`) and a Google Play `track` — the pipeline for that submission exists.

## App-store distribution, against the licence's clauses 1 and 4

`mobile/eas.json` builds with `distribution: "store"` and `buildType: "app-bundle"`, and its
`submit.internal` block targets the App Store and Google Play. The licence the app is under
(`mobile/LICENSE.md`, counsel's text of 2026-10-27) says two things a store distribution has to be
read against. **Both are questions for counsel. Nothing here is a conclusion, and nothing here
should be acted on without counsel's answer.**

Clause 1.1, production use:

> You may use the Software in production only if you have agreed to, and are in compliance with, the
> Agreement, and you hold a valid Fugaro Subscription Edition subscription for the correct number of
> user seats.

Clause 4.1, redistribution:

> You may redistribute the unmodified Software in source form, as part of the Fugaro repository or of
> a fork of it, provided that this licence text is retained and the Software is not modified.

Questions:

1. **Who publishes the app to the stores?** Clause 4.1 licenses redistribution in *source* form and
   clause 5 grants no other rights, so on the text as written only the Licensor (or someone with a
   written agreement) may publish a binary build. Does a store submission by the Licensor need
   anything further, and is the answer different for a subscriber publishing its own build?
2. **What does clause 1.1 mean for a downloaded binary?** Every downloader becomes a holder of the
   Software, and clause 1.1 conditions *production* use on a subscription. Is the licence asking the
   store to gate the download, the app to refuse production use without a seat, or merely recording
   that production use is licensed elsewhere? This changes what the app has to do, so it is worth an
   answer before the first submission rather than after.
3. **Do Apple's and Google's terms conflict with clause 1.1's seat counting?** Both stores'
   developer agreements require the developer to have the rights it distributes, and both have terms
   about what a licence to end users may say. Apple's Developer Program License Agreement and Google
   Play's Developer Distribution Agreement and Developer Program Policies are the two documents to
   read the clause against; this audit has not read them, and any section numbers counsel needs are
   counsel's to find.
4. **Is an unlisted or private distribution the right first step?** Google Play's `internal` track is
   what `eas.json` targets today, and Apple has equivalent routes (TestFlight and Custom Apps). If
   clause 1.1's or 4.1's answer is that public distribution does not fit, these are the shapes that
   do — a named set of testers rather than the public store listing.
5. **Does Apple's or Google's required end-user licence agreement, if either requires one, override
   or sit beside `mobile/LICENSE.md`?** Apple's standard EULA and Google's default terms both attach
   to a store download. Which of them governs a downloader's use, and how the business licence's
   clause 1 coexists with it, is a question with an answer only counsel can give.
6. **A first release is planned and none has been published.** `LICENSING.md` says no release of the
   mobile app has been published. Counsel's answer therefore lands before anything ships, which is
   the cheapest moment to get it.

## Left for counsel

Collected, so nothing here is buried in the prose above:

- **The licence's coverage sentence against the coordinator and orchestrator code.** The three
  directories cover `ee/`, the console and the mobile app; the coordinator and orchestrator code lives
  outside them and stays MIT until it moves. Already recorded in `docs/licensing/README.md`; restated
  here because it is the same question the audit above raises about what is inside and outside the
  three directories.
- **The `elkjs` attribution line**, and whether EPL-2.0 §3.3 wants a copyright line reproduced in
  `console/THIRD_PARTY_NOTICES.md` beside the licence text. Staleness 2 above.
- **The Electron runtime** and how the console's notices file should account for it. Staleness 1
  above.
- **Whether `mobile/` needs a `THIRD_PARTY_NOTICES.md`** before its first store submission, and what
  it has to carry for `expo`, React Native and React.
- **The six app-store questions above**, against the licence's clauses 1.1 and 4.1.
- **The Functional Source License in the console's dependency tree.** `sentry` 0.45.0
  (`FSL-1.1-Apache-2.0`), through `@sentry/bundler-plugins`, is source-available rather than open
  source: its terms restrict competing use until the licence converts to Apache-2.0 two years after
  each release. Nothing in `console/src/` calls it, and `internal/eeboundary/licence_test.go`'s
  copyleft rule does not match the identifier. Task 2.1b's fix round A took it out of what ships —
  `build.files` excludes the bundler subpath's whole tree, so no distributed artifact carries it —
  but it stays installed and in the lockfile as a dependency of `@sentry/node`, so whether a
  FSL-1.1 package may sit in the build at all, and whether the crash reporter's dependency is
  acceptable with it in the tree, is still counsel's call. Task 2.1b records it rather than deciding
  it.
- **Whether `node-forge`'s `(BSD-3-Clause OR GPL-2.0)` is read the way this audit reads it.** The
  audit takes BSD-3-Clause and records that in `copyleftAllowed`; if counsel reads the expression
  differently the allow-list entry is wrong and the dependency is a decision rather than a note.
- **The LicenceRef copy.** `LICENSES/LicenseRef-Fugaro-Subscription.txt` answers to the same identifier
  as `ee/LICENSE` and is compared to it byte for byte. What REUSE expects *around* that file (a
  README in `LICENSES/`, `REUSE.toml`, a check in CI) is not settled by this work, and no tool reads
  the directory today.

## What a machine enforces from all of this

`internal/eeboundary/licence_test.go`, hermetic (two files, no network, no install):

| Rule | What fails |
| --- | --- |
| `no GPL, AGPL or SSPL dependency` | a package in either lockfile whose declared licence carries a GPL, LGPL, AGPL or SSPL identifier, unless its name is in `copyleftAllowed` with a reason |
| `every dependency declares a licence` | a package in either lockfile with no `license` field |

`console/test/unit/lockfile.test.ts` and the console's `pinning.test.ts` enforce different, already
existing things about `console/package-lock.json` (pinned versions, the lockfile's own shape); they
say nothing about licences, and this audit does not change them.

`console/test/unit/notices.test.ts` does, from this revision on: it derives the set of packages the
packed console carries for each of its three targets out of the lockfile, `package.json`'s
`dependencies` and `build.files`'s exclusions, and holds `THIRD_PARTY_NOTICES.md` to it in both
directions — a package that ships with no section fails, and so does a section for a package that
does not ship. That is the machine half of this document's "a lockfile says who is installed, not
what ships" caveat: the audit still reasons about licences, and the test is what keeps the file in
step with the artifact.
