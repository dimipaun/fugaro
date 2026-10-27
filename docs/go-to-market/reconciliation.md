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
| `fugaro estimate` | Not there. The pieces are: `internal/pricing` (price table, catalog), `priceTableFor` in `internal/cli/prices.go`, `agent.Usage` per model in `internal/agent/stream.go`, `runstore.Cost` |
| Raw token counts per job | Not stored. `runstore.Cost` holds dollars (`ModelBy`, `RouteBy`); open PR #1115 adds only cache read/write totals |
| SSO, audit logs | Nothing in code |
| Hotfix mode (people series) | `docs/rigor-modes/` names Hotfix |

## Findings

**F1 (Rule): the `ee/` list contradicts D302.** The spec lists the console, mobile app, coordinator and
central orchestrators as `ee/`; `docs/licensing/reconciliation.md` D302 (answered 2026-10-24) keeps them
MIT core because the console is already released under MIT. The owner's ruling on this spec is D400.

**F2 (Exists): the licensing skeleton.** `ee/` is reserved with no licence file (T1), `LICENSE` stays pure
MIT, the scope statement is `LICENSING.md`, and the release sets an `ee` build tag (D303). `ee/LICENSE`
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

**F6 (Rule): published figures.** The headline figures in the landing copy ("90% lower", "a tenth of the
price", "$4.20", "$3 an hour") are claims. `docs/website/reconciliation.md` D96-D97 allow published
figures only from an owner-run aggregation of at least `internal/stats.MinRuns` runs, with no
repository, run, branch, task or person named, and a "not a promise" line. A competitor comparison needs
a source and a date. The pages carry a figure from the variant config only once that aggregation exists;
until then the config holds the sentinel `unmeasured` and the page refuses to build with a figure that
is not backed (task L2.3).

**F7 (Wrong): `opus-4.5`.** The demo command `fugaro estimate --pr 1255 --model opus-4.5` names a model
the price table does not have. `internal/pricing/table.go` has `claude-opus-5-5` and `claude-opus-5`;
`internal/pricing/canonical.go` maps the canonical ID `claude-opus-5-5` to the route ID
`anthropic/claude-opus-5.5`. The command takes a canonical ID or a route ID the table knows and refuses
any other with the list of known IDs.

**F8 (Gap): raw tokens are not on the record.** `agent.Usage` (input, output, cache read, cache creation)
is parsed per model from the agent stream, and the gateway sees each call's usage, but result.json keeps
dollars. #1115 (open) adds cache read and write token totals to `Cost`; input and output tokens per model
are not stored anywhere in the run record. `fugaro estimate` needs per-model token sums, so the record
must gain them (a `result.schema.json` change, additive, optional). Runs recorded before that have no
tokens, and `estimate` refuses them by name rather than guess (pre-1.0: no backfill).

**F9 (Gap): what an estimate means.** Another model uses other token counts and retries, and the price
catalog goes stale after 30 days (`runstore.Cost.CatalogStale`). The command labels every figure an
estimate, assumes the run's own cache ratio, prints the catalog's date, and says so when the catalog is
stale.

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

**F16 (Gap): estimating a PR.** `runstore` records `PR *PRRef` on the run, so `--pr N` can find the
run. Whether a lookup by PR number across runs exists is UNVERIFIED (U2).

## Decisions

| # | Question | Recommendation and cost | Owner's answer |
| --- | --- | --- | --- |
| **D400** | The agenda puts the console, mobile app, coordinator and orchestrators in `ee/` and sells them as Team. D302 keeps them MIT. Which wins? | Keep D302; Team is rebuilt around net-new features. Cost: the Team line cannot launch as written | **2026-10-26: NOT the recommendation.** Keep the code where it is for now because PRs are in flight; mark each of those areas, in its own files, as covered by the `ee/` licence; record a note to move them into `ee/` later. *Why:* avoid moving code under open PRs. Consequence and tensions: T1, T2 |
| **D401** | How do the landing pages load Meta Pixel, LinkedIn Insight Tag and Calendly? | Consent-gated, campaign pages only. Cost: wider CSP and privacy text, a test per origin | **2026-10-26: as recommended.** *Why:* the pages exist for paid traffic and the consent banner already exists. Amends D106 and F5 for those origins on those four pages only |
| **D402** | Does this plan own SSO and audit logs? | Track only; they get their own spec-to-pr from the workspaces spec. Cost: the first-big-deal date depends on a plan not yet written | **2026-10-26: as recommended.** *Why:* the workspaces spec owns the identity design |
| **D403** | Where do the engineering and business sections live? | Split: engineering in `docs/go-to-market/`, business in a private place. Cost: `spec.md` is an excerpt, not verbatim | **2026-10-26: as recommended.** The owner is creating a private repository, `fugaro-business`, for the marketing, sales and business material. This PR holds none of it and no plan task writes to it |
| **D404** | What does the plan gate on, given "1.0"? | A named launch gate, "launch-ready", on whatever release the owner cuts. Cost: no fixed date | **2026-10-26: as recommended.** The owner picks the version number at the cut |
| **D405** | GitHub stars on the pages? | Owner-run refresh file. Cost: can go stale | **2026-10-26: NOT the recommendation.** Omit stars until the number is worth showing. *Why:* no data file to keep fresh. The social-proof section ships with design-partner logos only, hidden until one exists |

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

**T1 (D400 against D302 and the licensing reconciliation).** Files in a repository whose `LICENSE` is
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

**O1.** The `fugaro-business` repository does not exist yet; moving the full agenda there is the owner's.

## UNVERIFIED

- **U1.** Whether `internal/gateway` already holds per-call input and output token counts at the point the
  run record is written (it holds usage for pricing; the record's own fields were not read in full).
- **U2.** Whether a lookup of a run by PR number exists in `internal/cli` (`--pr` on `fugaro run` is for
  follow-ups).
- **U3.** Which non-Claude models (the agenda names DeepSeek, Qwen and Kimi) have a price row in the
  embedded catalog today; `docs/design/provider-price-catalog.md` and `internal/pricing/catalog` decide it.
- **U4.** That every route in `website/src/pages` is deployed under `fugaro.dev` with no collision for
  `/save`, `/control`, `/scale` and `/crew`; `firebase.json` rewrites were not read in full.
- **U5.** No figure in the agenda was measured by this reconciliation (F6).
