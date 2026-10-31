# Go-to-market (engineering sections): reconciliation

Checked against `origin/main` at `e9dacf54b` on 2026-10-26. Spec: [spec.md](spec.md), an excerpt of the owner's
agenda (D403). **Where this file disagrees with `spec.md`, this file wins.**

## Headline: what the spec concept already has in the repo

| Spec concept | What the repo has |
| --- | --- |
| Open core, `ee/` for org features | `docs/licensing/`: `ee/` reserved, root `LICENSING.md`, `ee` build tag, license key (D300-D310). Console, mobile, coordinator and orchestrators were ruled core (D302); D400 amends that |
| Four landing pages, GA4, cookie banner | `website/` is a built Astro site (`website/src/pages`); GA4 loads only after consent (`website/data/site.yaml`, `firebase.json` CSP). No campaign pages, no pricing page |
| Meta Pixel, LinkedIn Insight Tag, Calendly embed | Nothing. `docs/website/reconciliation.md` D106 admits one third party (GA4 behind the consent gate); the CSP in `firebase.json` has no `frame-src` |
| SECURITY doc | `SECURITY.md` and `website/content/security.md` (a rule file, `credentials-service.md`, loads when either is touched) |
| `fugaro estimate` | Not there. The pieces are: `internal/pricing` (price table, catalog), `priceTableFor` in `internal/cli/prices.go`, `agent.Usage` per model in `internal/agent/agent.go`, `runstore.Cost` |
| Raw token counts per job | Not stored. `runstore.Cost` holds dollars (`ModelBy`, `RouteBy`); open PR #1115 adds only cache read/write totals |
| SSO, audit logs | Nothing in code |
| Hotfix mode (people series) | `docs/rigor-modes/` names Hotfix |

## Findings

**F1 (Rule): the `ee/` list contradicts D302.** The spec lists the console, mobile app, coordinator and
central orchestrators as `ee/`; `docs/licensing/reconciliation.md` D302 (answered 2026-10-24) keeps them
MIT core because the console is already released under MIT. The owner's ruling on this spec is D400.

**F2 (Exists): the licensing skeleton.** `ee/` is reserved with no licence file (the licensing reconciliation's T1), `LICENSE` stays pure
MIT, the scope statement is `LICENSING.md`, and D303 (amended) compiles `ee/` code into the one binary by an `ee` build tag, but at 0.11.0 `ee/` is empty and the release sets no tag (`.claude/rules/ee.md`). `ee/LICENSE`
waits on counsel (licensing plan task L4.1). The spec's "exact licence still to choose" is that task.

**F3 (Exists): the SECURITY document.** `SECURITY.md` and `website/content/security.md` already state the
trust model; the credentials-service rule file governs them. The agenda's task is a check of what exists
against "trust model and secret flow", not a new document.

**F4 (Exists): the site, its hosting and its consent gate.** The landing pages are new routes in an
existing build, not a new site. Deploys go through `scripts/deploy-website.sh` and the preview/promote
workflows (`docs/website/preview-promote.md`); an agent run never edits `.github/workflows/`.

**F5 (Rule): third-party loads.** Meta Pixel, the LinkedIn Insight Tag and an inline Calendly embed are
requests to origins D106 does not admit. The CSP allows scripts only from `'self'` and
`www.googletagmanager.com`, and has no `frame-src`. Resolved by D401.

**F6 (Rule): published figures.** The agenda's headline figures (a percentage saved, "a tenth of the price", a per-PR cost, an hourly rate) are claims. `docs/website/reconciliation.md` D96-D97 allow published
figures only from an owner-run aggregation of at least `internal/stats.MinRuns` runs, with no
repository, run, branch, task or person named, and a "not a promise" line. A competitor comparison needs
a source and a date. The pages carry a figure from the variant config only once that aggregation exists;
until then the config holds the sentinel `unmeasured` and the page refuses to build with a figure that
is not backed (task G2.1). What the page renders while the sentinel is there — nothing at all — is D409.

**F7 (Wrong, settled): `opus-4.5` is a nickname, not an ID.** The agenda's demo command
(`fugaro estimate --pr N --model opus-4.5`, in the business agenda) names a spelling the price table does
not have. The owner confirmed on 2026-10-26 that it means `claude-opus-5-5`, which `internal/pricing/table.go`
has (beside `claude-opus-5`), and `internal/pricing/canonical.go` maps it to the route ID
`anthropic/claude-opus-5.5`. The command takes a canonical ID or a route ID the table knows, so the demo
line is `fugaro estimate --pr N --repo owner/name --model claude-opus-5-5`; it refuses any other spelling
with the list of known IDs and adds no alias table (a nickname map would be a second place to keep in step
with the catalog).

**F8 (Gap): tokens per model are not on the run record.** `pricing.Usage` (input, output, cache read,
cache write 5m and 1h, web searches) is what the gateway prices; `agent.Usage` (`internal/agent/agent.go`)
is what the agent stream reports per model, with one cache-creation count and no 5m/1h split; `budget.ModelUse` (`internal/budget/model.go`) already carries a model's in, out, cache read and cache write for the day counter, and `internal/runner/budget_session.go` books all cache creation as 5m. `runstore.Cost` keeps dollars
only (`ModelBy`, `RouteBy`), and #1115 (open) adds only cache read/write totals. Two paths fill the cost:
with the gateway on, `internal/runner/cost.go` fills `ModelBy` from the gateway's ledger; without it (`auth: oauth` runs, and api-key or vertex runs whose budget mode is neither observe nor enforce: `GatewayOn` in `internal/runner/routing.go`) the only source is `agent.Result.ModelUsage`, and the record's `model_source` says `claude-code`. Both need
filling or `estimate` would refuse every subscription run. The record gains per-model tokens (additive,
optional; the 5m/1h split is kept on the gateway path and is not available on the subscription path, which records all cache creation as 5m, as the budget does). #1115's cache read/write totals are the sums of the per-model figures, and a test pins the equality. `runview.cost` rebuilds a run's cost and drops `ModelBy`, so `estimate` reads the stored `Record.Cost` directly. Runs recorded before that have none, and `estimate`
refuses them by name (pre-1.0: no backfill).

**F9 (Gap): what an estimate means.** Another model uses other token counts and retries, and the price
catalog goes stale after 30 days (`runstore.Cost.CatalogStale`). The command labels every figure an
estimate, assumes the run's own cache ratio, prints the catalog's date, and says so when the catalog is stale. It cannot reproduce a recorded `model_usd` exactly: long-context tiers apply per call, web searches are priced apart, and `model_usd` carries the `unattributed` residual and calls settled from reservations (`Unreconciled`, `UsageUnparsed`). Check 162 is judged with that in mind.

**F10 (Gap): SSO and audit logs do not exist.** The workspaces spec that covers SSO has not been
reconciled. Resolved by D402: this plan names them as gates and owns none of their tasks.

**F11 (Gap): GitHub stars.** A star count is a network call at build or view time. Resolved by D405.

**F12 (Rule): names.** The spec names design partners; the owner's rule is no real names in docs. The
excerpt omits them, and the plan says "a design partner". Logos on a page need the partner's written
permission, which is the owner's.

**F13 (Gap): "1.0 mid-November".** No 1.0 release or definition exists; the CLI is 0.11.x with 0.12.0 in
flight. Resolved by D404.

**F14 (Gap): campaign media.** `features.video` in `website/data/site.yaml` is false and a test pins it
against the media the site holds. The muted looping video and the `/crew` cat imagery are owner-produced
files; the pages render without them and the flag turns on with the first file. The hero's picture while a
variant names no file of its own is D411's: a screen of the console the variant names in
`website/data/landing.yaml`, with the campaign's own drawing as the fallback.

**F15 (Rule): doc and site tests.** Website content is pinned by `website/test/*.mjs` and
`internal/cli/docs_website_boundary_test.go`; any change to `site.yaml`, the CSP or the privacy page runs
`go test ./internal/cli/ -run Docs` and `npm run check` in `website/`.

**F16 (Exists): finding a run by PR.** `fugaro runs ls --pr N` is backed by `onPR` in
`internal/cli/runs.go`, over the run's `PR *PRRef`. A PR can have several runs (the first and its
follow-ups); `estimate --pr N` reuses that lookup and, when more than one run matches, lists them and asks
for `--run`, rather than sum or pick one.

**F17 (Gap): one consent covers analytics only.** (The marketing choice is asked only on the four campaign pages, by a campaign variant of the banner there; every other page, and every visitor who never opens one, is unchanged.) The consent state is one stored granted/denied value for
the analytics banner (`website/public/scripts/consent-early.js`, `site.js`), and withdrawal deletes only
GA4's cookies. Loading advertising trackers under it would treat an analytics consent as an advertising
one, under privacy text that does not name those vendors. D401 therefore needs a separate marketing
purpose, a consent version that asks existing visitors again, withdrawal that removes the trackers'
cookies, and the privacy text's owner-approved wording. A visitor who declines still needs the call to
action: Book a demo falls back to a plain link.

**F18 (Gap): D401's "four pages only" cannot be a CSP property.** `firebase.json` serves exactly one
Content-Security-Policy on every path, and `website/test/origins.mjs` pins that. The CSP therefore widens
site-wide by exactly the new origins, and "those pages only" holds in the loader's path check and its test
(open item O2).

**F19 (Gap): documents D400 and D401 leave stale.** `ee/README.md`, `LICENSING.md`, `.claude/rules/ee.md`
and the D302 row of `docs/licensing/reconciliation.md` say nothing moves out of the core; they are
amended with the notice (T1), not before. D106 in `docs/website/reconciliation.md` and the CSP line in
`.claude/rules/website.md` ("the analytics host, nothing else") are amended by task G2.3.

**F20 (Gap): campaign pages must not index, and media must be self-hosted.** The site builds no
sitemap (`website/astro.config.mjs` has no integrations; `content.ts` returns no sitemap URL), so the four
pages need only `noindex` and a canonical to the home page; adding a sitemap would add a dependency and is
not planned. `Base.astro` always renders the navigation and a self-canonical, so the pages need a campaign
mode of it (task G2.2). D107 requires media to be self-hosted or on an allowed host, so a variant's video
is a file under `website/`, never a YouTube or Vimeo URL, which would be a fourth third party D401 never
allowed.

**F22 (Gap): UTMs and GA4.** GA4 receives the page's origin and path only (`site.js`), and the privacy page says no query is reported. UTM values go to the Calendly URL and nowhere else (the Meta and LinkedIn tags send the page URL with its query by default, so G2.3 configures them to drop it and the privacy text says so); they are not put into the `wa.me` text (which would show them to the visitor and send campaign data to a vendor the privacy text does not name), and attribution in GA4 would be a privacy-text change that is the owner's.

**F21 (Gap): the benchmarking line of spec §13.** "Alongside actual cost" asks for the estimate shown next
to each job's cost; this plan builds only the on-demand command. Showing it in `fugaro diagnose` or the
console is left out.

## Decisions

| # | Question | Recommendation and cost | Owner's answer |
| --- | --- | --- | --- |
| **D400** | The agenda puts the console, mobile app, coordinator and orchestrators in `ee/` and sells them as Team. D302 keeps them MIT. Which wins? | Keep D302; Team is rebuilt around net-new features. Cost: the Team line cannot launch as written | **2026-10-26: NOT the recommendation.** Keep the code where it is for now because PRs are in flight; mark each of those areas, in its own files, as covered by the `ee/` licence; record a note to move them into `ee/` later. *Why:* avoid moving code under open PRs. Consequence and tensions: T1, T2 |
| **D401** | How do the landing pages load Meta Pixel, LinkedIn Insight Tag and Calendly? | Consent-gated, campaign pages only. Cost: wider CSP and privacy text, a test per origin | **2026-10-26: as recommended.** *Why:* the pages exist for paid traffic and the consent banner already exists. Amends D106 and F5 for those origins on those four pages only |
| **D402** | Does this plan own SSO and audit logs? | Track only; they get their own spec-to-pr from the workspaces spec. Cost: the first-big-deal date depends on a plan not yet written | **2026-10-26: as recommended.** *Why:* the workspaces spec owns the identity design |
| **D403** | Where do the engineering and business sections live? | Split: engineering in `docs/go-to-market/`, business in a private place. Cost: `spec.md` is an excerpt, not verbatim | **2026-10-26: as recommended.** The owner is creating a private repository, `fugaro-business`, for the marketing, sales and business material. This PR holds none of it and no plan task writes to it |
| **D404** | What does the plan gate on, given "1.0"? | A named launch gate, "launch-ready", on whatever release the owner cuts. Cost: no fixed date | **2026-10-26: as recommended.** The owner picks the version number at the cut |
| **D405** | GitHub stars on the pages? | Owner-run refresh file. Cost: can go stale | **2026-10-26: NOT the recommendation.** Omit stars until the number is worth showing. *Why:* no data file to keep fresh. The social-proof section ships with design-partner logos only, hidden until one exists |
| **D406** | Does the per-file `ee/` notice (D400) go in now, or with counsel's licence text? | With counsel's text (T1 option b): the notice then names a licence that exists. Cost: files stay unmarked until L4.1 | **2026-10-26: not the recommendation (neither option).** The owner says the question is moot: a new licence is about to land as `ee/LICENSE.md`, so the notice can name it. *Why:* counsel's text arrives shortly. No task here writes the notice; it follows the licence, in the licensing work |
| **D407** | T1: when do the console, mobile app, coordinator and orchestrator files get their `ee/` notice? | With `ee/LICENSE.md`, in the same change that lands it. Cost: unmarked until then; counsel confirms the owner can relicense contributions | **2026-10-26: as recommended.** *Why:* the notice then names a text that exists. Copies already released stay MIT. The notice is the licensing work's change, not a task here |
| **D408** | T2: what does a Team seat buy while the code stays in the public tree? | The `ee/` licence for the marked files, enforced by the licence key (D308), plus a support promise, from the version that carries the notice. Cost: depends on D407's notice landing before Team opens | **2026-10-26: as recommended.** *Why:* earlier MIT releases stay free; the seat is the commercial licence and support. The price and plan live in `fugaro-business` |
| **D409** | What does a campaign page render while a variant's figure is the sentinel `unmeasured`, and the proof band (spec §6, section 2) has no figure to publish? (F6) | The band renders with a "not measured yet" notice where the figure would be. Cost: the page explains an absence, and the band holds the place of a number nobody has | **2026-10-30: NOT the recommendation — the owner, reading the preview, asked for nothing at all.** The band is not rendered while the sentinel is there: no figure, no placeholder and no notice about one, and the page is complete without the section. `website/data/landing.yaml` and the loader keep the sentinel and its guard; what changed is what the page does with it. *Why:* a notice apologises for something the reader never saw, and an empty band holding the figure's place is the same apology drawn as a box. Consequence: spec §6's section 2 is absent from all four campaign pages until a figure a measurement backs is written into the file — the one section omission the owner asked for, beside D405's. The same reading of the preview asked for the hero's second half: each page's hero carries a drawing of the product's own promise — a task, the short-lived cloud job it starts and the pull request the job ends in — in the site's palette and the variant's tone, inline rather than fetched, because no variant names media yet (F14, G2.5). Superseded by D411 |
| **D410** | The owner read the four campaign pages on the live preview at 1440, 768 and 390 px on 2026-10-30 and asked for a design pass. Which of spec §6's descriptions does it supersede? | Restyle inside the site's own tokens, palette, typeface and brand — no new look, colour, dependency or origin. Cost: spec §6 sections 1, 3 and 5 describe the plan's intent rather than the page's rendering, and this file is where the two are reconciled | **2026-10-30: as recommended.** Five findings, all of them in the shared template and the campaign components and none of them in `website/data/landing.yaml`. **(1)** The first screen carried no action: the booking is now the hero's own 56 px link, under the supporting line and above the picture, and it is the same file link the closing band offers, so the page offers one booking in two places and a visitor who declines the marketing consent keeps a working way to book (D401, F17). **(2)** The hero's picture is the dominant thing in the first screen: flat filled shapes in the site's palette and the variant's tone, no frame, no gradient, glow or shadow at rest, inline SVG with a text alternative, and from the wide breakpoint the hero is a grid — the headline across the band, then the copy at 2fr beside the drawing at 3fr. **(3)** Every headline is at most two lines at 1440, 768 and 390 px: the headline spans the band and its size is capped, because a 48-character headline on two lines in the 358 px phone column is a ~21 px headline, and the campaign's own section type scales under the headline's slope so the page's `h1` is still the largest type on the page at every width. **(4)** The page is no longer thin: **section 3's "three icons" are gone** — a step is its heading and its line, in a full-width row of a `--panel` field at section sizes rather than in a third of a three-column row — and section 5 is the page's one full-width field of flat colour, the accent as the field and its words in the ground, with one filled booking control and two quiet text actions. **(5)** Motion: the hero's parts stagger in on the first frame the browser draws, the two lower bands ride `site.js`'s own `[data-reveal-block]` entrance at 16–32 px, the controls answer a hover, a press and a focus, and every transition and animation the campaign's own classes declare sits inside `prefers-reduced-motion: no-preference`. *Why:* the owner's own reading of the rendered pages, which is G2.5's check and the one thing the plan's tests cannot do. Consequence: spec §6's section 1 says "muted looping video" and the hero draws the product's promise instead while no variant names a video file (D409, F14; the picture itself is D411's); its section 3 says "three icons" and no step carries one; its section 5 says the booking is an inline embed, and the plain link is the booking's first form (D401) |
| **D411** | What is the campaign hero's picture, given the owner's instruction of 2026-10-30 after the design pass (D410's finding 2)? | A real screen of the console, named per variant in `website/data/landing.yaml` and shown in the frame the console page's own slider gives a screen; the campaign's own drawing stays as the hero's fallback for a variant that names none. Cost: the drawing D409 and D410 record is no longer what the four heroes show, and a variant's screen has to be a file the console page already owns | **2026-10-30: the owner's instruction, on the same preview read as D409 and D410.** Each variant names a `screenshot` — the file's NAME under `website/public/screenshots/`, which the loader holds to the rows of `website/data/console-screens.yaml` and refuses when no row carries it, naming the rows that exist — and the hero renders that capture large: the hero's wider column from the wide breakpoint (a 16:10 capture in a 3fr column of the band, about half of a 900 px first screen) and the full width of the page under the booking action on a phone, fetched with the page (`loading="eager"`, `fetchpriority="high"`) with the row's own `alt`, caption and pixel size reserving its box. No file is added, no copy is re-encoded and no origin is added: the hero fetches one of the four pictures `public/screenshots/` already holds (F14, D190). Which screen is the claim the variant makes: `/save` the Spend screen, whose subject is what a run is priced at; `/control` the run detail — the ledger, the verification and the findings a review policy's rounds end in; `/scale` and `/crew` the Live screen, where the same console runs many at once, and `/crew`'s own `image` takes the slot the day the owner supplies one. The campaign's own drawing is kept as the hero's **fallback**: it is what a hero shows while its variant names no screen, and a variant that names one renders the screen instead of it, so a hero carries one picture and never two. The screens are the console's own fixture captures of a fictional `acme-platform` installation, their `alt` texts name it, and no page presents one as a customer's. *Why:* the owner's instruction after reading the design pass on the same preview, which is G2.5's check and the one thing the plan's tests cannot do. Consequence: D410's finding 2 and D409's "the hero draws the product's promise instead while no variant names a video file" are superseded — the hero's picture is the console's screen while the variant names one, and the drawing is what it shows when it names none (F14, G2.5, D190) |
| **D412** | The owner read the four campaign pages on the live preview again on 2026-10-30, after the design pass (D410) and the hero's screens (D411), and asked for three things: the three closing actions made friendlier, the field behind them made less square, and the console's every screen shown rather than one. Does this work take all three, and where? | All three, and only inside the shared template, the campaign components, the campaign styles and the site's own tokens: the closing band's three actions each get a small inline mark with a lift and a nudge for a pointer (finding 1), the page's two fields of colour are cut at `--radius-xl` (finding 2), and the console's screens are shown on all four pages in the homepage's own slider, below the hero and above the closing call to action (finding 3). Cost: `website/data/landing.yaml`'s claims and figures are untouched, so the screens' subject is the campaigns' own promise rather than a figure, and the four pages carry three more pictures each while no screenshot is added, copied or re-encoded | **2026-10-30: the owner's instruction, on the same preview as D409-D411, and no question of this file's.** **(1)** The band behind the three closing actions is friendlier: each of the booking, the install and the chat carries its own small mark — a calendar, a download and a speech bubble — drawn inline in the site's own style, `aria-hidden` because the link's own words are the meaning, and a plate that lifts 2 px with the mark nudging 2 px the way its own action goes, for a pointer that hovers and only inside `prefers-reduced-motion: no-preference`, so a reader who asked their system for less is served the band still; the hero's own booking link is the same link and shares the booking's mark and lift. **(2)** The field is no longer square: the closing band and the steps' panel — the page's two fields of colour — are cut at `--radius-xl`, the largest radius step the site's token scale draws and the one `src/styles/tokens.css` says there is deliberately nothing above, so the two fields are one shape. The radius is the whole of the change to the box: both stay flat colour, with no gradient, no shadow and no hairline at rest. **(3)** Every campaign page shows the console's screens: below the hero and above the closing call to action, in a section with its own heading, the homepage's "See it" slider (`ScreenshotSlider.astro`) over the homepage's own rows (`website/data/home-screens.yaml` through `loadHomeSliderScreens()`), so the four pages show the same screens, in the same order, with the same `alt` text, captions and pixel sizes, with the same focusable track, dot anchors and `hidden` control bar, with a thumb-sized control on a phone and every slide in the document for a reader whose browser runs no script. The pictures are named, never added: the console's own captures under `website/public/screenshots/`, one eager (the first slide) and the rest lazy, with the CSP, the script (`public/scripts/site.js` needed no change) and the byte budget untouched. *Why:* the owner's reading of the rendered pages, which is G2.5's check and the one thing the plan's tests cannot do. Consequence: spec §6's section 5 says the calls to action are two links and a chat, and it now draws them with a mark each; spec §6's "one screenshot per page" reading is superseded — each page shows its hero's screen and, below the fold, every screen the homepage's panel shows (F14, D190, D246) |

**Resolutions (one defensible answer, not questions):**

- **R1.** `fugaro estimate` is a new CLI command in `internal/cli`, repricing a run's stored token counts
  from the price table (F7, F8, F9). It adds the tokens-per-model fields to the run record and
  `schemas/result.schema.json`.
- **R2.** The four pages are routes in the existing `website/` build rendered from one data file,
  `website/data/landing.yaml`, with `noindex` and a canonical to the home page, in the existing deploy path
  (F4, F14).
- **R3.** The WhatsApp number, Calendly link, partner logos and video files are inputs the owner supplies;
  none is a secret, and none is invented in the repository.
- **R4.** The SECURITY work is a verification task (F3), and the owner approves any wording change.
- **R5.** Everything in the agenda's action table owned by the owner, and every business section, is out
  of scope here (D403).

## Open tensions and open items for the owner

**T1 (closed 2026-10-26 by D407).** Files in a repository whose `LICENSE` is MIT could not be marked
"covered by the `ee/` licence" before that licence existed, and the console is already released under MIT,
which those copies keep. D406 said the licence is about to land as `ee/LICENSE.md`; D407 settles the
timing: the notice goes in with it.

**T2 (closed 2026-10-26 by D408).** While those areas stay in the public tree, what a Team seat buys:
D408 answers it. The price stays in `fugaro-business`.

**O1.** The business sections are committed locally in `fugaro-business` (`docs/`); pushing them is the owner's.

**O3 (narrowed).** `.claude/rules/website.md` forbids a wildcard in `script-src` (enforced by `test/brand.mjs`). The new vendors' script hosts are expected to be exact (the three vendors are Meta, LinkedIn and Calendly); a wildcard could arise only in `img-src`, `connect-src` or `frame-src`, which that rule does not cover. G2.3 pins exact hosts wherever the vendor allows it and raises to the owner any directive that needs a wildcard.

**O2 (answered 2026-10-26: accepted).** F18: the CSP widens for every page, not the four; the origins are inert without the loader, and "those pages only" holds in the loader's path check.

## UNVERIFIED

- **U1.** The exact point in the gateway's per-call path where usage is available for summing by model,
  which G1.2's first step finds.
- **U3 (settled).** The catalog has rows for DeepSeek, Qwen and Kimi models (`internal/pricing/catalog`);
  which exact ones the price table prices stays the catalog's.
- **U4 (settled).** `firebase.json` has no rewrites and `website/src/pages` has no `save`, `control`,
  `scale` or `crew` route.
- **U5.** No figure in the agenda was measured by this reconciliation (F6).
