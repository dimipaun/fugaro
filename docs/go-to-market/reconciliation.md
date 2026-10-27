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
is not backed (task G2.1).

**F7 (Wrong): `opus-4.5`.** The agenda's demo command (`fugaro estimate --pr N --model opus-4.5`, in the business agenda) names a model
the price table does not have. `internal/pricing/table.go` has `claude-opus-5-5` and `claude-opus-5`;
`internal/pricing/canonical.go` maps the canonical ID `claude-opus-5-5` to the route ID
`anthropic/claude-opus-5.5`. The command takes a canonical ID or a route ID the table knows and refuses
any other with the list of known IDs.

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
files; the pages render without them and the flag turns on with the first file.

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
| **D406** | Does the per-file `ee/` notice (D400) go in now, or with counsel's licence text? | With counsel's text (T1 option b): the notice then names a licence that exists. Cost: files stay unmarked until L4.1 | **Pending: the owner has not answered.** No task writes a notice; G5.1 records the note only |

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

**T1 (D400 against D302 and the licensing reconciliation; D406 asks).** Files in a repository whose `LICENSE` is
MIT cannot be marked "covered by the `ee/` licence" until that licence text exists (L4.1 waits on
counsel), and the console has already been released under MIT, which those copies keep. Choices: (a) mark
them now with a neutral notice that names `LICENSING.md` and says the licence of these files is
expected to change from a stated future version, with counsel's wording later; (b) wait for L4.1 and mark
them in the same change that moves them. Option (a) holds back no code but makes a promise the owner may
not be able to keep for contributions already accepted under MIT; option (b) holds back the notice until
counsel is done. **The owner rules.** The plan has no task that writes a notice (task G5.1 only adds the
"move later" note to `docs/licensing/`).

**T2 (D400 against the Team price).** If the console, mobile app and coordinator stay MIT and in the
public tree, a seat cannot be enforced by a licence key on them until they move. What a $39.99 seat buys
before the move is the owner's to rule and sits in `fugaro-business`; no task here depends on it.

**O1.** The business sections are committed locally in `fugaro-business` (`docs/`); pushing them is the owner's.

**O3.** G2.3's CSP additions must name exact hosts: `.claude/rules/website.md` forbids wildcards and `brand.mjs` enforces it, and some vendors document wildcard origins. If a vendor needs one, the owner decides whether to admit it or drop that vendor.

**O2.** F18: the CSP widens for every page, not the four. Choices: accept it (recommended: the origins are inert without the loader), or change the CSP test to allow a second, path-scoped header, which a browser intersects with the first and so cannot widen. The plan assumes the first; the owner confirms.

## UNVERIFIED

- **U1.** The exact point in the gateway's per-call path where usage is available for summing by model,
  which G1.2's first step finds.
- **U3 (settled).** The catalog has rows for DeepSeek, Qwen and Kimi models (`internal/pricing/catalog`);
  which exact ones the price table prices stays the catalog's.
- **U4 (settled).** `firebase.json` has no rewrites and `website/src/pages` has no `save`, `control`,
  `scale` or `crew` route.
- **U5.** No figure in the agenda was measured by this reconciliation (F6).
