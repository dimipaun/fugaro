# The Fugaro website

The site build (website plan, Part 1, Task **W1.1**). This directory is a **second
artifact** that lives in this repository beside `console/`, with its own language, its own
build and its own schedule (D90): nothing under it is importable by the Go module, and the
Go module gains no dependency from it. The boundary is a test, not a convention —
`internal/cli/docs_website_boundary_test.go`.

**What is here today:** the shell. The pinned toolchain, the static Astro configuration,
the check that runs the build and every test file, and this document. `website/data/` and
`website/content/` already hold the facts Part 0 generates; the pages that render them land
in W1.3 (`src/layouts/`) through W1.12. **The build therefore produces no page yet**, which
is why `npm run build` is green today and why the first page's own test arrives with the
first page.

## Toolchain

**Node 22 LTS**, pinned in `package.json`'s `engines`: `>=22.22.0 <23`. The floor is
Astro's own requirement rather than a preference, and the ceiling is the major line this
site is built and checked on — the engines range is the supported toolchain. What was
verified is narrower: `npm run check` passed on **Node 26.x** (the version the author of
this change runs, installed with `--engine-strict=false`), and the engines floor is the one
Astro states. The lockfile was not regenerated on Node 22 here, and no check was run on it.

The dependency tree is pinned exactly: `package.json` names `astro` at one version, and
`package-lock.json` is **committed**, so `npm ci` installs the tree this site was checked
with and nothing else. Every package in the lockfile resolves from `registry.npmjs.org`.
The shell ships **no integration** — no adapter, no image service, no font pipeline — and a
task that needs one adds it there, with the byte budget that task carries (F56, D106).

## Install, build, check

```sh
npm ci        # install the pinned tree (this is the only step that needs the network)
npm run build # build the site into dist/
npm run check # the site's gate: the build, then every test/*.mjs
```

**The build needs no network.** `npm ci` fetches the pinned tree once; after that nothing
here fetches anything: there is no integration, no remote font, no remote image, and the
build script sets `ASTRO_TELEMETRY_DISABLED=1` so that Astro's own anonymous usage report
is off too. `npm run check` is the same command a developer runs and the one W2.1 stages
the deploy workflows to run (`website-preview.yml` and `website-promote.yml`, staged under
`docs/pending-workflows/` for the owner to copy in: no Fugaro run may write them into
`.github/workflows/`), and it is hermetic: it reads this
repository, builds, and asserts over the artifact — no cloud, no credential, no deployed
site. The live checks — the real domain, DNS, TLS and the deployed HTML — are the owner's,
and W2.2 and W2.3 write them as **checks 69a–69c** in `docs/gcp-live-checklist.md`: 69a (the
site is live and honest) and 69b (nothing phones home) are written by W2.2, and 69c (the
published ranges are real) is W2.3's, in the level after it.

## Nothing loads from a third party

No font, script, stylesheet, image or embed reaches a page from an origin outside this
build: the pages use the system font stack `src/styles/tokens.css` names, no font file
ships today, and a font added later is **self-hosted in this artifact**
(`website/public/fonts/`, which the layout preloads from), and there is no Google Fonts, no CDN, no embed and no pixel — because every
third-party origin is a request from a reader's browser to a company this project has no
relationship with (D106). There is exactly **one exception, and the owner chose it**: the
analytics script (D93), loaded **only after the visitor consents**, with W1.12's banner as
the gate that decides whether anything is fetched and its own tests for no request before
consent, no cookie before consent, withdrawal and keyboard reachability. `readme.mjs`
refuses a third-party font or script host in the shell's own sources today, and W1.11
asserts the same over the built HTML's script, link, iframe and image origins.

**W3.2 is the task that switched it on** (owner's ruling of 2026-10-18): Google Analytics 4,
measurement id `G-DZ4F35D3EH`, which is a public identifier rather than a secret and lives in
`data/site.yaml` and nowhere else — the layout and the test both read it from there, and a
planted second copy fails. The id, the audit trail and the choices behind them:

- **The property is a fact of `site.yaml`**, beside the flag that emits it: `analytics.enabled`
  decides whether the banner and its configuration are rendered at all, and clearing the flag
  leaves an artifact byte-for-byte free of the origin. When the flag is on, the banner carries
  the id and the one admitted origin as `data-analytics-id` and `data-analytics-origin`.
- **The gate is code in this site's one script file**, `public/scripts/site.js` — not an inline
  block — because D183 admits one script and because a first-party file is admitted by
  `script-src 'self'` while an inline block would need a hash in the deployed policy. The file
  reads the configuration out of the banner's markup, so it names no URL at all: with the flag
  off it finds no banner and does nothing.
- **Nothing is requested from Google before a decision.** A stored `granted` (under
  `fugaro:analytics-consent`, the key the banner's markup carries) loads the script once; a
  refusal, a dismissal, an unreadable answer and silence all load nothing. `test/consent.mjs`
  runs the shipped file in a stub browser and watches the request that does or does not arrive.
- **The privacy defaults are pinned in the one `config` call**: one page view per page and
  nothing else — no event, no user id — with `allow_google_signals` and
  `allow_ad_personalization_signals` both false. GA4 anonymises the IP address by default;
  there is no setting to add for it. `test/consent.mjs` reads `dataLayer` and holds each of
  those to what was built.
- **Withdrawing deletes the cookies the tag set** (owner's ruling of 2026-10-18): stopping the
  sends was only half of a withdrawal — Google's tag left first-party cookies on the site's
  origin, and a visitor who has said no should not keep being identified by them. So declining
  after accepting, from the banner the privacy page always offers, deletes `_ga`,
  `_ga_<container id>` (that name derived from the measurement id above rather than written
  down), `_gid`, `_gat` and each `_gat_*` cookie the browser is holding — on the page's host,
  and, only while one is still there, on the registrable parent domain the tag may have used —
  with `Path=/` and an expiry in the past, and with nothing else in the browser touched. No
  deletion, and no cookie read at all, runs before consent was ever given. `test/consent.mjs`
  runs the shipped file against a cookie jar holding those cookies and reads what is left.
- **The deployed policy is widened by exactly what Google documents** for Google Analytics 4
  without any Ads features
  ([tag-platform/security/guides/csp](https://developers.google.com/tag-platform/security/guides/csp),
  read 2026-10-18): `script-src` gains `https://www.googletagmanager.com`, `img-src` and
  `connect-src` gain that origin and `https://*.google-analytics.com`, and `connect-src` gains
  `https://*.google.com`. No `'unsafe-inline'`, no `'unsafe-eval'`, no nonce and no hash —
  the site ships no inline script. `test/origins.mjs` holds the policy to that exact token
  list, and refuses any token it does not carry.

## The gates

Six checks run over the **built** artifact (`dist/`) on every `npm run check` (W1.11), each
with fixtures that keep its refusals running, and none of them needs the network:

- `test/linkcheck.mjs` — every internal link and asset resolves to a built file, every
  `#anchor` to an id in its target, every external link is `https:` on `site.yaml`'s
  `allowed_hosts`, and every GitHub link into this repository names a path and heading that
  exist.
- `test/origins.mjs` — nothing loads from a host that is not this site (images and embeds
  may also come from `allowed_hosts`), inline scripts are on a hash allow-list — empty since
  W3.2, because the site ships no inline script at all — and the one third-party origin D93
  admits appears only as the consent banner's configuration. It also reads the shipped
  `public/scripts/site.js` for the gate's contract (no URL, no second analytics vendor, the
  consent decision compared with `granted`) and `firebase.json`'s Content-Security-Policy
  against the token list Google's documentation states, refusing an extra origin, an
  `'unsafe-inline'`, an `'unsafe-eval'`, a nonce, a hash or a wildcard.
- `test/a11y.mjs` — after W1.2's contrast cases: lang, one `h1`, no heading jumps,
  landmarks, a skip link, names and labels, keyboard reachability, focus outlines, a
  `prefers-reduced-motion` path for every animation, and the shipped tokens equal to the
  palette in force.
- `test/budget.mjs` — the byte budget, whose numbers are these (raw bytes). **They are
  deliberately loose, by the owner's ruling of 2026-10-17: size is not a concern yet, so
  each is three times what the build of 2026-10-18 needs — the build that ships the site's one
  script and its menu (P6, P3) and the homepage's run animation (P7), with the How It Works
  flow (P8) merged in — and a later
  optimisation pass tightens them.** The gate itself is not loose: a page or a site over the
  number fails, and the test keeps a case that proves it. The derivation (that build plus
  25 %, D192) is in the test's header, and a task that moves the bytes re-derives the numbers
  here, in the commit that moves them:
  - **JavaScript per page:** 76,524 bytes. Three times 25,508: the one script the site ships,
    `public/scripts/site.js` (20,406 bytes — the copy control of P6 and D183, the menu's
    Escape/outside-click close of P3, and W3.2's consent banner and analytics gate, which
    moved out of the markup and into this one file, with the cookie deletion the owner's
    ruling of 2026-10-18 added to a withdrawal), plus 25 %. Every page loads it once, so
    it is the whole of a page's JavaScript, and the test keeps a case that says so over the
    built artifact.
  - **Page weight per page:** 853,602 bytes. Three times 284,534: the heaviest page, the
    docs hub, at 227,627 bytes (its HTML — the whole generated CLI reference — plus the
    files it fetches: the script above, the logo and the icons), plus 25 %.
  - **Whole site:** 2,941,224 bytes. Three times 980,408 (every file in `dist/`, 784,326
    bytes across 21 files, plus 25 %).

  Two of the numbers are **not** bytes, and they are the redesign's: the brief's core
  principle is "the gist above the fold on every page with very few words", which no byte
  budget can hold (D187, D188). The same file reads them out of this list, and the same
  rule applies — a task that needs more raises the number here, in the commit that needs
  it:
  - **Words in a page's lead:** 40 words. The lead is the visible text of `main` between
    the page's `h1` and its first `h2` — the paragraph a page opens with before its first
    section — and it is a lead and not an introduction: 40 words at most, on every page,
    the three reference pages included, because a lead is the page's own whichever page it
    is. **The region between the two headings is the whole of the definition, wherever each
    heading sits** (P5; P4's reviewer read the homepage's lead two ways, 24 words from the
    `h1`'s own container and 134 from the plan's region, and the stricter one is the one
    enforced). A page that carries blocks between its `h1` and its first `h2` is a page
    whose lead those blocks are part of — which is why the homepage opens its install
    section with the `h2` above the commands rather than putting the commands under the
    promise — so the fix for a long lead is a shorter lead or an earlier first `h2`, never
    a reader that looks somewhere else. A page
    with no `h2` at all has no such region, and its lead is the paragraphs that follow its
    `h1`. A page whose `h1` is followed straight by its first `h2` has no lead region
    either, and 0 words is inside the ceiling: `/security/` is shaped that way (its `h1` is
    the rendered document's own title, and the page's lead line comes before it) and so is
    `/changelog/` (whose summary is its first section, under the heading that names it).
    The ceiling is a ceiling, not a floor — a page with no lead region is a page D188's
    definition does not describe, and this gate says nothing about how much a page's first
    section should say.
  - **Words per page:** 250 words. Every word a reader sees inside `main` — command blocks
    and code spans included, since the ceiling is a claim about the page rather than about
    its prose — counted by `budget.mjs`'s own reader, which reads a page the way
    `test/lib/html.mjs`'s `textOf` does (a script or style body is not text, an image's
    `alt` is) and adds the one rule `textOf` does not carry: a subtree carrying `hidden` is
    not text either. No built page hides anything inside `main` today — the layout's own
    `hidden` elements, the skip link and the consent banner, sit outside it — so that rule
    is what stops a page that starts hiding a control from being counted for words nobody
    sees. `/docs/`, `/security/`
    and `/changelog/` are exempt, which is D188's own list: each renders a document of this
    repository rather than a page of the site, and the brief allows those to stay long.
    The ceiling is set against the site as it was measured before the redesign
    (`docs/website/redesign/reconciliation.md` §6: faq 493 words, caps-and-costs 487,
    getting-started 431, privacy 277, community 268 — every one of them over it).
- `test/fold.mjs` — the ordering the fold claim rests on (P5, RF5). Whether the promise and
  the first command fit a real 390 × 844 first screen is the owner's live check (70b) and the
  word ceilings' business, and no test here sees a viewport (D187). The ordering underneath it
  is a statement about the built pages, though, and it is this file's rules: the homepage's lead
  region holds one element and it is the promise, so the install block and the terminal are the
  section the heading opens; Getting Started's install section opens with its commands, with the
  sentence about them still there after the paths; and on every route that prints a command —
  three of the eleven today, and the other eight are skipped rather than asserted about — the
  first `h2` precedes the first `pre`. The whole gate is one function over one `dist`, so its
  refusals are kept the way the other gates keep theirs: a case writes a synthetic `dist` with
  each defect planted beside a page that keeps every rule, and each rule was mutation-proved by
  removing it from the function and watching that case fail.
- `test/meta.mjs` — the provenance-phrase gate (D186): a page must not explain why its own
  text exists, so D186's phrase list (`rendered from`, `generated rather than written`, …) is
  refused in the text a reader reads, on every built page. It is the one defect the other
  four cannot see — a sentence about the build is short, well-formed and honest — and it is
  the sentence the redesign removes. Naming a document is not provenance text: the docs hub
  names each guide's file, a page may link to `SECURITY.md`, and the changelog renders the
  release files' own lines, which is why D186 chose an explicit list over a rule about
  naming a repository file. It holds a second rule of the same kind, because a phrase list
  cannot hold it: **no page may show a generated file's banner as text.** The generated files
  under `content/` — `cli-reference.md` and `security.md`, both `go run ./internal/clidoc`'s
  output; `downloads.json` beside them is hand-kept and carries no banner — open with
  `<!-- generated by …; do not edit -->`, W1.3's pipeline reads the banner as a fact and
  drops it from the body it renders, and RF8 takes "the banner is never on a page"
  as a premise — which was false on the docs hub, where the rendered CLI reference carried it
  through the markdown compiler and the page's own `escapeRawHtml` showed it to a reader as
  `<!-- generated by go run ./internal/clidoc; do not edit -->` above the reference's title.
  The gate refuses the escaped banner and ignores a comment node, which is the difference
  between a line a reader reads and a note in the markup.

**Lighthouse is the owner-run check** (D105's targets: performance ≥ 95, accessibility 100,
best practices 100, SEO 100), run against the built artifact by hand: there is no CI on pull
requests and the tests here are hermetic, so a browser-driven audit is not one of them.

The site's other gate is the Go side, which holds every fact to the file it is derived
from:

```sh
GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'Docs|Lint|Website|CliRef' -count=1
```

**The `Website|CliRef` half is not decoration.** The plan's baseline for this suite is
`-run 'Docs|Lint'`, and that pattern selects **none** of the site's own pins: of the 53
tests it selects in `internal/cli`, none is named `TestWebsite*` or `TestCliRef*`, so a
run of it alone is green with every fact the site renders unexecuted. `Website` selects the
boundary test, the install pins and the security page; `CliRef` selects the reference's
pins, in `internal/cli` and in `internal/clidoc` where the generator's own tests live. The
names are the ones the docs-only guard in `ci.yml` already uses for the same tests.
`website/test/readme.mjs` holds this command to that: every test function the site's Go
test files declare has to be selected by the pattern this section prints.

## What is generated, and what must not be edited

The generated files under `content/` are **never edited by hand**: `cli-reference.md` and
`security.md` are generated documents and `costs.json` is the owner-run aggregation's output.
`content/downloads.json` is the one **hand-kept** file, marked `none: hand-kept` in the table,
and it is edited by hand. The directory holds three kinds of file and they say so in three ways:

- a **document** (`content/*.md`) carries a header on its first line naming the command
  that writes it, and is regenerated by a command run **from the repository root**;
- a **machine output** (`content/costs.json`) is the aggregation's `--json` verbatim, so it
  has no header to carry and no format to add one to — what makes it the aggregation's
  output is its shape, and it is absent until the owner runs the command.
- a **hand-kept** file (`content/downloads.json`) is not generated and has no command; the
  table's Regenerate cell says `none: hand-kept`, and `website/test/readme.mjs` lists it by
  name.

`website/test/readme.mjs` asserts that this table and those files agree: a file whose
header names a different command, a `content/` file this table does not name, a document
without its header, or a JSON file that is not the aggregation's output all fail the site's
check until this document says the same thing.

| File | Source | Regenerate with |
|---|---|---|
| `content/cli-reference.md` | the command tree, `internal/cli.NewRootCmd` | `go run ./internal/clidoc` |
| `content/security.md` | `SECURITY.md`, byte for byte below the title | `go run ./internal/clidoc` |
| `content/costs.json` | `fugaro stats runs --project <installation> --json`, and it is the aggregation's output verbatim | owner-run: `fugaro stats runs --project <installation> --json > website/content/costs.json` |
| `content/downloads.json` | hand-kept, not generated: W3.1 fills it from the console's release artifacts; a placeholder with `available: false` until then | none: hand-kept |

Three things about those commands are worth stating rather than leaving to the table:

- **One generator, two files.** `go run ./internal/clidoc` writes both generated documents
  from their sources; it compiles into the build cache and writes nothing into the tree
  besides its output.
- **The costs file is the owner's to produce, and it may be absent.** The aggregation reads
  one named installation's run records, is refused inside a coding agent's session, and
  prints percentiles per recipe with the window and the sample size behind each — never a
  repository, a run id, a branch, a task, a person or a commit (D96, D97). Until the owner
  runs it, the file is absent and the caps-and-costs page renders its "not enough runs yet"
  state: an empty figure is honest, an invented one is not.
- **`website/data/*.yaml` is hand-written, and pinned by tests rather than regenerated.**
  `install.yaml` is held to `README.md`, `docs/release.md` and `.goreleaser.yaml` by
  `internal/cli/docs_website_install_test.go`, and `site.yaml` to `go.mod`, the
  repository's decisions and the site's own host list by the boundary test. Change the
  fact in the repository, then the value here, in one commit.

## The changelog is built, never committed

There is no changelog page file and no changelog JSON in this tree: `src/lib/changelog.mjs`
reads `docs/releases/vX.Y.Z.md` **during the build** and the changelog page (W1.9) renders
what it returns. Nothing is written to disk, so a Highlights file that lands on `main`
before its tag exists leaves no stale copy behind — and no date is taken from a tag,
because a shallow checkout has no tags and the newest entry a reader sees has none yet
(D100). `website/test/changelog.mjs` holds that.

## The rule this directory is written to

**A page renders a fact; it never restates one.**

`data/` and `content/` hold the facts, `src/` renders them, and no page writes a command,
a number or a claim of its own. The install block comes from `install.yaml`, the security
page from `content/security.md`, the CLI reference from `content/cli-reference.md`, the
costs from `content/costs.json`, the changelog from `docs/releases/`, and the site-wide URL
and the feature flags from `site.yaml` — each one rendered, each one held to its source by
a test, in this repository, in the commit that changes it. A page that paraphrases a
document is the copy that drifts first, which is the failure this whole arrangement exists
to prevent (F37, Global Constraint 6).

## Layout

```
website/
  package.json, package-lock.json, astro.config.mjs, tsconfig.json
  data/install.yaml, site.yaml     # hand-written facts, pinned by Go tests
  content/                         # generated, do not edit (see the table above)
  src/lib/changelog.mjs            # reads docs/releases/ at build time
  src/layouts/, src/components/, src/pages/, src/styles/   # W1.2-W1.12
  public/                          # icons, logo.png, site.webmanifest (scripts/gen-brand-icons.py)
  test/*.mjs                       # every one of them runs under `npm run check`
  dist/                            # the build output, not committed
```

`node_modules/`, `dist/` and `.astro/` are not committed: the first is what `npm ci`
installs from the committed lockfile, and the last two are build output.

## Brand assets

Every icon and the header logo are derived from one file, `brand/fugaro-logo.png` at the
repository root, by `scripts/gen-brand-icons.py` (see `brand/README.md`). The outputs are
committed under `public/` — the favicons, the apple-touch icon, the Android icons including
a maskable one, `logo.png` and `site.webmanifest` — and the base layout links them. Nothing
is fetched from another origin, and `test/brand.mjs` holds the sizes, the opacity of the
home-screen icons, the manifest's colours (the palette's `--ground`), the header logo's
40 KB budget and its empty alt.

## Deploying

`firebase.json` at the repository root is the hosting configuration: it serves
`website/dist` with clean URLs, a Content-Security-Policy, `nosniff`, a referrer policy, a
one-year immutable cache for the hashed `/_astro/` files, a day for icons and fonts, and five
minutes for HTML. It names no project and there is no `.firebaserc`.

The policy admits this origin and the three Google Analytics origins W3.2 widened it by, and
nothing else: `script-src 'self' https://www.googletagmanager.com` (inline styles are still
allowed, because Astro inlines small stylesheets), `img-src` and `connect-src` with
`https://*.google-analytics.com`, `connect-src` with `https://*.google.com`. Those are the
origins Google documents for GA4 without Ads features, read 2026-10-18 from
[tag-platform/security/guides/csp](https://developers.google.com/tag-platform/security/guides/csp),
and `test/origins.mjs` holds the served policy to that list token by token — an extra origin
is a failure there, in the same commit that would add it. `test/brand.mjs` is the coarser
check beside it: the served policy names those three origins and no others. (An earlier
revision of that page listed `https://*.analytics.google.com`; the current one does not, and
the contract follows the page rather than the memory of it. `https://*.google.com` is in
`connect-src` because the documentation requires it, and without it the collect requests a
consenting visitor's browser makes would be blocked.) It landed ahead of W2.1 with the
owner's deploy script, and W2.1 extends it (the staged workflow).

### The canonical domain

`data/site.yaml`'s `canonical_url` is the one origin the canonical `<link>`, the sitemap, the
OpenGraph tag and every absolute link on the site are built from, and it is the real domain
the owner registered and connected Firebase Hosting to (owner ruling 2026-10-17; the site's
own project is `fugaro-prod`, D91). Its host is named in `data/site.yaml` and nowhere else
under `website/`, and it is on `allowed_hosts` — so pointing the site at a domain is that one
line, the host list's own entry and a rebuild, and no page can go on serving a domain the
site stopped being. `TestWebsiteCanonicalHostAppearsOnlyInSiteYaml` in
`internal/cli/docs_website_boundary_test.go` scans every file under `website/` for the host
(and the control beside it, `TestWebsiteHostScanSeesAPlantedCopy`, plants one in a page to
prove the scan sees it), which is why this page does not print the domain.

**There is no second domain, and no page or document here may name one.** W2.2 wrote a
`secondary_domain` for a domain the owner acquired alongside the canonical one; the owner
ruled on 2026-10-17 that no such domain is theirs, so both the value and every step that
attached it are gone — the ruling is recorded in the amendment under Task W2.2 of
`docs/website/implementation-plan.md`.
`TestWebsiteNamesNoSecondDomain`, with its own control, scans every file under `website/` for
the name W2.2 wrote and refuses it coming back, so a later task cannot reintroduce it by
copying an older revision of a page. **Registering a domain, attaching it to the site's
Firebase project and writing its DNS records are the owner's steps** — no agent registers a
domain, touches DNS or changes that project — and the live result is what check 69a in
`docs/gcp-live-checklist.md` records.

`scripts/deploy-website.sh` deploys from the owner's machine with the Firebase CLI, using
whatever `firebase login` the owner already did — it never reads, prints or takes a token
or a key:

```sh
scripts/deploy-website.sh --dry-run                 # build + npm run check, print the deploy command
scripts/deploy-website.sh                           # deploy live to fugaro-prod
scripts/deploy-website.sh --channel preview-1       # deploy to a preview channel instead
scripts/deploy-website.sh --project <id> --dry-run  # another website-only project
```

The project defaults to `fugaro-prod`, the Firebase project the owner created for the
website only (D91); `--project` overrides it, and it is never an installation's project.
The script checks that `node`, `npm` and `firebase` are installed, runs `npm ci` when
`node_modules/` is missing, runs `npm run check`, refuses an empty `dist/`, and then runs
`firebase deploy --only hosting --project <id>` (or
`firebase hosting:channel:deploy <name> --project <id>`). `scripts/deploy-website_test.sh`
tests it with the tools stubbed on PATH, and `go test ./scripts/` runs that test.

The deploy workflow is still W2.1's and **not a Fugaro run's to write**: W2.1 stages a
workflow under `docs/pending-workflows/` for the owner to copy in, and the owner runs it to
deploy to the Firebase project created for the website only (D91, D109) — a run may not
change `.github/workflows/`. `docs/pending-workflows/` now holds no pending workflow: the website's two manual
workflows, `website-preview.yml` and `website-promote.yml` (#566), were copied into `.github/workflows/` by the
owner, and no deploy workflow is staged. The only workflow files left there are the desktop
console's `console-tests.yml` and `console-release.yml`, which the owner has copied in as well:
their staged copies stay by design, because the console's docs tests assert each one is
byte-identical to its copy under `.github/workflows/`, so neither is pending.
