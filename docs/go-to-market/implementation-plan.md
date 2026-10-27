# Go-to-market (engineering sections): implementation plan

## Opening and status

This plan builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)**, which wins
where they differ. Base: `origin/main` at `e9dacf54b`, 2026-10-26.

**Status: plan only. Nothing is implemented, and NO TASK IS APPROVED.** Each task needs the owner's
explicit go; answering a decision approved none of them.

The owner's answers of 2026-10-26 that shape it:

- **D400** (not the recommendation): the console, mobile app, coordinator and orchestrators stay where
  they are, to be marked as covered by the `ee/` licence and moved later. Only the note (G5.1) is a task
  here; the notice itself waits on T1.
- **D401** the Meta Pixel, LinkedIn Insight Tag and Calendly embed load only after a separate marketing
  consent (F17), and only on `/save`, `/control`, `/scale` and `/crew` by the loader's path check; the CSP
  widens site-wide because it is one header (F18, O2).
- **D402** SSO and audit logs are gates this plan tracks and does not build.
- **D403** the business sections live in the owner's private `fugaro-business` repository; no task here
  writes to it.
- **D404** the plan gates on "launch-ready", on whatever release the owner cuts.
- **D405** (not the recommendation): no GitHub stars on the pages.

## Conventions every part follows

- `CLAUDE.md`: targeted tests only; gofmt, vet; no new dependency or framework; comments say why.
- `.claude/rules/website.md` for every `website/**` task. `.claude/rules/credentials-service.md` for G3.1,
  which reads `SECURITY.md` and `website/content/security.md`.
- Docs are tested: a task that changes a doc or the site runs `go test ./internal/cli/ -run Docs` and, for
  `website/`, `npm run check`.
- No secret-shaped literal in tests, docs or fixtures. The Meta, LinkedIn and GA identifiers are public,
  but tests use plainly fake values (`TESTPIXEL`, `TESTPARTNER`).
- No agent edits `.github/workflows/`. The website deploys through the existing owner-run path
  (`scripts/deploy-website.sh`, `docs/website/preview-promote.md`).
- No live cloud action by an agent; every live check below is OWNER-RUN.
- Hermetic tests: `internal/testutil`, `t.TempDir()`, no network, no home directory.
- Release notes: the CLI part (G1) adds a command and an additive result field and has **no operator
  step**. A release-notes file exists only for a version the owner has chosen (D404), so G1.4 stages the
  Highlights line in its PR body and writes `docs/releases/vX.Y.Z.md` (with its line in
  `website/data/release-summaries.yaml`) only at the cut.
- Numbers: decisions **D400-D429** and checks **160-169** are this work's block, taken above the highest
  decision written (D367) and reserved (up to D389 by the Bitbucket credentials plan) under `docs/` on
  `main` and in open PRs, and above the highest check reserved (154).
  D400-D405 are written; D406-D429 and checks 163-169 are reserved and unused.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No request to a third-party origin before the marketing consent, and none off the four campaign pages | G2.3 origins test over the built HTML plus consent-state and path tests |
| Declining marketing consent leaves a working Book a demo link | G2.2, G2.3 |
| A published figure is never typed into a page; it comes from the variant config, which refuses an unbacked one | G2.1 |
| A variant's media is a self-hosted file, never an external URL | G2.1 |
| The four pages are `noindex`, canonical to the home page, and absent from the sitemap | G2.2 |
| A booking event is accepted only from Calendly's origin; UTM and pre-fill values are allow-listed, length-capped and encoded | G2.4 |
| An estimate is labelled an estimate, names the catalog's date, refuses a run with no stored tokens and an ID the table does not know | G1.3 |
| The new result fields are additive and optional, so older records still validate | G1.1 |
| No real person, partner or organisation name in a page, fixture or doc (F12) | review focus on G2.1, G2.2 |

Review focus for the independent reviewers: consent ordering on the landing pages (what loads before the
banner is answered, and what a visitor who consented only to analytics gets), the CSP widening being
exactly the origins named, cookie removal on withdrawal, and the gateway change in G1.2 reading usage
without touching the budget path and keyed on the model the gateway priced, never on a string the
upstream response carries.

## Slices and tasks

**Slice G1: `fugaro estimate` and tokens per model.** Ships alone: a new command and optional fields.

### G1.1 Run record: tokens per model

- **Files:** `internal/runstore/cost.go`, `internal/runstore/cost_test.go`, `schemas/result.schema.json`
  (the `cost` object only), `schemas/schemas_test.go`.
- **Interfaces:** consumes `pricing.Usage`'s counts; produces `Cost.ModelTokens`, a map from model to
  `{input, output, cache_read, cache_write_5m, cache_write_1h}` int64, `omitempty`.
- **Schema change:** additive optional property under `cost`, `additionalProperties: false` kept.
- **Tests:** `TestCostModelTokensRoundTrip`, `TestCostModelTokensOmittedWhenEmpty`, and in `schemas` a
  case that validates a record with and without the field.
- **Command:** `go test ./internal/runstore/ ./schemas/ -run 'TestCostModelTokens|Result' -count=1`
- **Depends on:** open PRs #1115, #1107 and #1111 merged (all edit `cost.go` or the schema).
- **Commit:** `runstore: a run's cost keeps its tokens per model`
- **Red step:** the round-trip test fails to compile until the field exists.
- **Reviewer tier:** standard.

### G1.2 The runner fills the tokens, on both cost paths

- **Files:** `internal/runner/cost.go`, `internal/runner/runner.go` (and the one gateway file that exposes
  the per-model sum, found first: U1), with their tests.
- **Interfaces:** with the gateway on, sums the gateway's per-call `pricing.Usage` under the model the
  gateway priced; without it, copies `agent.Result.ModelUsage`. Produces filled `Cost.ModelTokens`.
- **Tests:** `TestRunFillsModelTokensGateway` (two models; a call whose usage is unparsed adds no tokens;
  a response naming another model adds no key), `TestRunFillsModelTokensSubscription` (`auth: oauth`).
- **Command:** `go test ./internal/runner/ -run 'TestRunFillsModelTokens' -count=1` (the package takes
  about 25 minutes in full: name the tests).
- **Depends on:** G1.1; #1107, #1110, #1111 and #1115 merged (all write `runner.go` or `cost.go`).
- **Commit:** `runner: record each model's tokens on the run's cost`
- **Red step:** both tests fail with an empty `ModelTokens`.
- **Reviewer tier:** `opus + mandatory independent review` (the gateway holds the model credential and
  enforces the budget).

### G1.3 `fugaro estimate`

- **Files:** `internal/cli/estimate.go` (new), `internal/cli/estimate_test.go` (new),
  `internal/cli/root.go` (one registration line), `website/content/cli-reference.md` (regenerated with
  `go run ./internal/clidoc`, never edited by hand).
- **Interfaces:** consumes run records, `priceTableFor`, `Cost.ModelTokens` and the `onPR` lookup;
  produces `fugaro estimate (--run ID | --pr N) --model ID [--json]`. With several runs on a PR it lists
  them and asks for `--run`.
- **Tests:** `TestEstimateReprices`, `TestEstimateRefusesRunWithoutTokens`,
  `TestEstimateRefusesUnknownModel` (names the known IDs), `TestEstimateLabelsEstimate`,
  `TestEstimateStaleCatalog`, `TestEstimateByPR`, `TestEstimateSeveralRunsOnAPR`, and the existing
  `TestCliReferenceIsCurrent` in `internal/clidoc`.
- **Command:** `go test ./internal/cli/ ./internal/clidoc/ -run 'TestEstimate|TestCliReference' -count=1`
- **Depends on:** G1.1.
- **Commit:** `cli: fugaro estimate reprices a run on another model`
- **Red step:** each estimate test fails with "unknown command"; the reference test fails until regenerated.
- **Reviewer tier:** standard.

### G1.4 Plugin skill, docs and the live-check row

- **Files:** `plugin/skills/working/SKILL.md`, `docs/gcp-live-checklist.md` (Check 162 and its register
  row), the docs tests' pins if any move.
- **Tests:** `go test ./internal/cli/ -run Docs -count=1`, `go test ./plugin/... -count=1`.
- **Command:** the two above.
- **Depends on:** G1.3.
- **Commit:** `docs: the estimate command in the working skill, and Check 162`
- **Red step:** the plugin test that checks every command a skill names against the CLI fails if the skill
  names `estimate` before G1.3 exists.
- **Reviewer tier:** standard.

**Slice G2: the four landing pages.** Ships alone after the owner supplies the inputs (G2.5).

### G2.1 Variant data, its loader and the claims guard

- **Files:** `website/data/landing.yaml` (new), `website/src/content.ts` (the loader and its
  validation), `website/test/landing-data.mjs` (new).
- **Interfaces:** produces `loadLanding()`: per variant, path, headline, supporting line, self-hosted video
  file (optional), proof number with its source, tone, WhatsApp pre-fill text; and a `partner_logos` list
  (empty).
- **Schema/config change:** `landing.yaml` is new; paths are the four message names, unique, no
  `/landing` prefix.
- **Tests:** unknown field is an error; duplicate path is an error; a proof number carries a source that
  names the owner-run aggregation (D96-D97) or the sentinel `unmeasured`, and a non-sentinel figure with
  no source fails the build; a video is a path under `website/`, an `http(s)` URL fails; the WhatsApp text
  names the variant.
- **Command:** `cd website && node --test test/landing-data.mjs`
- **Depends on:** none.
- **Commit:** `website: the four landing variants as one validated data file`
- **Red step:** the test fails because `loadLanding` is missing.
- **Reviewer tier:** standard.

### G2.2 The page template

- **Files:** `website/src/pages/[variant].astro` (new, or four thin pages if the router cannot do it
  without a collision with the existing routes: decided at the start by building), new files under
  `website/src/components/`, `website/src/content.ts` is **not** touched (G2.1 owns it), the sitemap
  exclusion in `website/astro.config.mjs`, `website/test/landing-pages.mjs` (new).
- **Interfaces:** consumes `loadLanding()`; no navigation, a minimal footer (GitHub and docs; no pricing
  link, since no pricing page exists and pricing is outside this repository).
- **Tests:** the four paths build; each is `noindex` with a canonical to the home page and absent from
  the sitemap; section order matches the spec; no email form; Book a demo is a plain link and the page is
  complete without the video file and without any logo (the social-proof section is hidden while
  `partner_logos` is empty); the mobile-viewport check the site's responsive tests use.
- **Command:** `cd website && npm run build && node --test test/landing-pages.mjs`
- **Depends on:** G2.1.
- **Commit:** `website: /save, /control, /scale and /crew from one template`
- **Red step:** the build test fails with four missing paths.
- **Reviewer tier:** standard.

### G2.3 Marketing consent and the three loads

- **Files:** `website/public/scripts/consent-early.js`, `website/public/scripts/site.js`, `firebase.json`
  (the CSP header only), `website/data/site.yaml`, `website/src/pages/privacy.astro`,
  `docs/website/reconciliation.md` (an amendment note on D106), `.claude/rules/website.md` (the CSP
  line), and the tests that pin them: `website/test/origins.mjs`, `brand.mjs`, `consent.mjs`,
  `layout.mjs`, `privacy.mjs`, plus `internal/cli/docs_website_boundary_test.go` if it pins the CSP.
- **Interfaces:** a separate marketing purpose in the consent state, with a consent version that asks
  existing visitors again; loaders for the pixel, the insight tag and the Calendly script that run only
  with that purpose granted and only on the four paths; withdrawal removes the trackers' cookies.
- **Config change:** `site.yaml` gains the three public identifiers and the origins (as the file's own
  rule says, load origins are admitted by the consent check, not `allowed_hosts`); the CSP widens by
  exactly the documented origins and gains a `frame-src`, site-wide (F18). **The owner approves the
  privacy wording and the rules-file line** before the task merges.
- **Tests:** no request to any of the three before the marketing purpose is granted, including after an
  analytics-only consent; none on a non-campaign page; withdrawal stops them and clears their cookies; the
  privacy page names the three; the CSP lists exactly the new origins; the existing CSP and single-header
  tests are amended to the new exact list.
- **Command:** `cd website && node --test test/origins.mjs test/brand.mjs test/consent.mjs test/layout.mjs test/privacy.mjs && npm run check`
  and `go test ./internal/cli/ -run Docs -count=1`
- **Depends on:** G2.2.
- **Commit:** `website: a marketing consent, and the pixel, the insight tag and Calendly behind it`
- **Red step:** the origins test fails because the pages carry no such loaders and the CSP lacks the hosts.
- **Reviewer tier:** `opus + mandatory independent review` (privacy and consent).

### G2.4 Key events, UTM pass-through and the live-check rows

- **Files:** `website/public/scripts/site.js` (the file G2.3 wrote, a later level),
  `website/test/landing-events.mjs` (new), `docs/gcp-live-checklist.md` (Checks 160 and 161 and their
  register rows).
- **Interfaces:** emits `demo_booked`, `install_click` and `whatsapp_click` to GA4 after analytics consent;
  accepts `demo_booked` only from a message whose `origin` is Calendly's; passes only allow-listed `utm_*`
  values, length-capped and encoded, into the Calendly URL and the `wa.me` text.
- **Tests:** each event fires once on its action and not before consent; a message from another origin
  does not fire `demo_booked`; UTMs reach the Calendly URL; hostile values (script text, an over-long
  value, a non-`utm_` key) are dropped or encoded; absent UTMs leave the URL unchanged.
- **Command:** `cd website && node --test test/landing-events.mjs`, and `go test ./internal/cli/ -run Docs -count=1`
- **Depends on:** G2.3.
- **Commit:** `website: landing key events and UTM pass-through`
- **Red step:** the tests fail because the events are not emitted.
- **Reviewer tier:** standard.

### G2.5 Inputs, preview and checks: OWNER-RUN

- The owner supplies: the Calendly link, the WhatsApp Business number, the proof figure's aggregation, the
  video and the `/crew` imagery (each only when they exist; the pages build without them), and any partner
  logo with the partner's written permission.
- The owner types `scripts/deploy-website.sh` from the owner's clone for a preview (dry-run first), then
  the promote workflow. No agent performs any step.
- **Check 160:** all key events fire in GA4 DebugView after consent and none before. **Check 161:** a test
  booking carries its UTM source into Calendly.
- **Depends on:** G2.4.

**Slice G3: the SECURITY check.**

### G3.1 Compare the security text with the secret flow

- **Files:** `docs/go-to-market/security-check.md` (new). No edit to `SECURITY.md` or
  `website/content/security.md` unless the owner approves the wording the note proposes.
- **Interfaces:** reads `SECURITY.md`, `website/content/security.md`, `docs/credentials-service/`.
- **Tests:** the docs tests still pass.
- **Command:** `go test ./internal/cli/ -run Docs -count=1`
- **Depends on:** none.
- **Commit:** `docs(go-to-market): the security text against the secret flow, with gaps listed`
- **Red step:** none (documentation); the review verifies each claim against the code.
- **Reviewer tier:** `opus + mandatory independent review` (credentials service rule file).

**Slice G5: licensing note.**

### G5.1 The "move later" note

- **Files:** `docs/licensing/README.md` (one paragraph: D400's note that the console, mobile app,
  coordinator and orchestrators are to move into `ee/` later, T1 open, and the four stale statements F19
  lists, to be amended with the notice).
- **Tests:** `go test ./internal/cli/ -run Docs -count=1`
- **Depends on:** none.
- **Commit:** `docs(licensing): the console, mobile app, coordinator and orchestrators move to ee/ later`
- **Red step:** none (documentation).
- **Reviewer tier:** standard.

**Check 162: OWNER-RUN, after G1.2 and G1.3 are released.** The owner runs `fugaro estimate --run <ID>
--model <the run's own model>` on one finished sandbox run with no long-context calls and a fresh catalog;
the figure is within a few percent of the record's `model_usd` (F9 lists why it is not exact) and the
owner judges it. Checks 163-169 are reserved and unused.

## Parallel execution

Edges, from the "Depends on" lines: open PRs #1115, #1107, #1110, #1111 (external) → G1.1 → {G1.2, G1.3};
G1.3 → G1.4; G2.1 → G2.2 → G2.3 → G2.4 → G2.5; G3.1, G5.1 and G2.1 have no dependency.

| Level | Tasks (run concurrently) |
| --- | --- |
| L0 | G1.1 (after the open PRs merge), G2.1, G3.1, G5.1 |
| L1 | G1.2, G1.3, G2.2 |
| L2 | G1.4, G2.3 |
| L3 | G2.4 |
| L4 | G2.5 (owner-run), Check 162 once released |

**Critical path:** G2.1 → G2.2 → G2.3 → G2.4 → G2.5. It is sequential because each task builds on the
previous one's interface and G2.3 and G2.4 write the same file. The estimate path (the open PRs → G1.1 →
G1.3 → G1.4) is independent of it.

### Shared files

| File | Writers | Levels |
| --- | --- | --- |
| `website/public/scripts/site.js` | G2.3, G2.4 | L2, L3 (never together) |
| `docs/gcp-live-checklist.md` | G1.4, G2.4 | L2, L3 |
| `website/src/content.ts` | G2.1 | L0 |
| `website/astro.config.mjs` | G2.2 | L1 |
| `website/test/origins.mjs`, `brand.mjs`, `consent.mjs`, `layout.mjs`, `privacy.mjs` | G2.3 | L2 |
| `internal/runstore/cost.go`, `schemas/result.schema.json` | G1.1 (after #1115, #1107, #1111) | L0 |
| `internal/runner/cost.go`, `internal/runner/runner.go` | G1.2 (after #1107, #1110, #1111, #1115) | L1 |
| `internal/cli/root.go`, `website/content/cli-reference.md` | G1.3 | L1 |
| `plugin/skills/working/SKILL.md` | G1.4 | L2 |
| `docs/licensing/README.md` | G5.1 | L0 |

No level holds two writers of one file. A change to any task's Files re-checks this table; an open PR that
writes a file named above is a dependency until it merges.

## Not in this plan

SSO, audit logs, the contract and DPA (D402, the owner's legal part), the per-file `ee/` notice and the
four stale statements (T1, F19), the price and what a seat buys (T2), the estimate shown beside each
job's cost (F21), the X drafting agent, lead lists, outreach, the videos and every other item of the
agenda's action table (D403, R5).
