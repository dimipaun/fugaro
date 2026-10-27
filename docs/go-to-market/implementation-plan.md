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
- **D401** the Meta Pixel, LinkedIn Insight Tag and Calendly embed load only after consent, only on
  `/save`, `/control`, `/scale` and `/crew`.
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
  step**; G1.4's notes say so.
- Numbers: decisions **D400-D429** and checks **160-169** are this work's block, taken above the highest
  decision written under `docs/` on `main` (D368) and in open PRs (D368), and the highest check (150).
  D400-D405 are written; D406-D429 and checks 163-169 are reserved and unused.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No request to a third-party origin before consent, and none off the four campaign pages | G2.3 origins test over the built HTML plus a consent-state test |
| A published figure is never typed into a page; it comes from the variant config and the config refuses an unbacked one | G2.1 |
| An estimate is labelled an estimate, names the catalog's date and refuses a run with no stored tokens | G1.3 |
| `estimate` accepts only IDs the price table knows (F7) | G1.3 |
| The new result fields are additive and optional, so older records still validate | G1.1 |
| Variants are `noindex` with a canonical to the home page | G2.2 |
| No real person, partner or organisation name in a page, fixture or doc (F12) | review focus on G2.1, G2.2 |

Review focus for the independent reviewers: consent ordering on the landing pages (an attacker view of
what loads before the banner is answered), the CSP widening being exactly the origins named, and the
gateway change in G1.2 reading usage without touching the budget path.

## Slices and tasks

**Slice G1: `fugaro estimate` and raw tokens.** Ships alone: a new command and optional fields.

### G1.1 Run record: tokens per model

- **Files:** `internal/runstore/cost.go`, `internal/runstore/cost_test.go`, `schemas/result.schema.json`
  (the `cost` object only).
- **Interfaces:** consumes `agent.Usage`'s four counts; produces `Cost.ModelTokens`, a map from model to
  `{input, output, cache_read, cache_write}` int64, `omitempty`.
- **Schema change:** additive optional property under `cost`, `additionalProperties: false` kept.
- **Tests:** `TestCostModelTokensRoundTrip`, `TestCostModelTokensOmittedWhenEmpty`, the schema test that
  validates a record with and without the field.
- **Command:** `go test ./internal/runstore/ -run 'TestCostModelTokens|Schema' -count=1`
- **Depends on:** open PR #1115 merged (both edit `cost.go` and the schema's `cost` object).
- **Commit:** `runstore: a run's cost keeps its tokens per model`
- **Red step:** the round-trip test fails to compile until the field exists.
- **Reviewer tier:** standard.

### G1.2 The runner fills the tokens

- **Files:** the one place #1115's Task 5 fills the cache totals (found by searching `CacheRead` and
  `pricing.Usage` in `internal/gateway` and `internal/runner`), and its test.
- **Interfaces:** consumes the gateway's per-call usage (U1); produces filled `Cost.ModelTokens`.
- **Tests:** `TestRunFillsModelTokens` over `internal/gcpfake` and the fake agent, with two models and a
  call whose usage is unparsed (counted in `UsageUnparsed`, contributing no tokens).
- **Command:** `go test ./internal/runner/ -run TestRunFillsModelTokens -count=1` (the runner package takes
  about 25 minutes in full: name the test).
- **Depends on:** G1.1.
- **Commit:** `runner: record each model's tokens on the run's cost`
- **Red step:** the test fails with an empty `ModelTokens`.
- **Reviewer tier:** `opus + mandatory independent review` (the gateway holds the model credential and
  enforces the budget).

### G1.3 `fugaro estimate`

- **Files:** `internal/cli/estimate.go` (new), `internal/cli/estimate_test.go` (new), `internal/cli/root.go`
  (one registration line).
- **Interfaces:** consumes `runstore` records, `priceTableFor`, `Cost.ModelTokens`; produces the command
  `fugaro estimate (--run ID | --pr N) --model ID [--json]`.
- **Tests:** `TestEstimateReprices`, `TestEstimateRefusesRunWithoutTokens`,
  `TestEstimateRefusesUnknownModel` (names the known IDs), `TestEstimateLabelsEstimate`,
  `TestEstimateStaleCatalog`, `TestEstimateByPR` (U2: if no lookup by PR exists the task adds only the
  smallest one and says so in its PR).
- **Command:** `go test ./internal/cli/ -run TestEstimate -count=1`
- **Depends on:** G1.1.
- **Commit:** `cli: fugaro estimate reprices a run on another model`
- **Red step:** each test fails with "unknown command".
- **Reviewer tier:** standard.

### G1.4 Docs and the plugin skill

- **Files:** `website/content/cli-reference.md`, `plugin/skills/working/SKILL.md`,
  `docs/releases/` (the next release's notes, Highlights line), the docs tests' pins if any move.
- **Tests:** `go test ./internal/cli/ -run Docs -count=1`, `go test ./plugin/... -count=1`, and
  `npm run check` in `website/`.
- **Command:** the three above.
- **Depends on:** G1.3.
- **Commit:** `docs: the estimate command, in the CLI reference and the working skill`
- **Red step:** the plugin test that checks every named command against the CLI fails once the skill
  names `estimate` before G1.3, and passes after.
- **Reviewer tier:** standard.

**Slice G2: the four landing pages.** Ships alone after the owner supplies the inputs (G2.5).

### G2.1 Variant data, its loader and the claims guard

- **Files:** `website/data/landing.yaml` (new), `website/src/content.ts` (the loader and its
  validation), `website/test/landing-data.test.mjs` (new).
- **Interfaces:** produces `loadLanding()`: per variant, path, headline, supporting line, video URL,
  proof number, tone, WhatsApp pre-fill text.
- **Schema/config change:** `landing.yaml` is new; paths are the four message names, unique, with no
  `/landing` prefix.
- **Tests:** unknown field is an error; duplicate path is an error; a proof number must carry a source key
  that names the owner-run aggregation (D96-D97) or the sentinel `unmeasured`, and the build refuses a
  non-sentinel figure with no source; the WhatsApp text names the variant.
- **Command:** `cd website && node --test test/landing-data.test.mjs`
- **Depends on:** none.
- **Commit:** `website: the four landing variants as one validated data file`
- **Red step:** the test fails because `loadLanding` is missing.
- **Reviewer tier:** standard.

### G2.2 The page template

- **Files:** `website/src/pages/[variant].astro` (new, or four thin pages if the router cannot do it
  without a collision with the existing routes: decide at the start by building), `website/src/components/`
  (new files for the sections), `website/test/landing-pages.test.mjs` (new).
- **Interfaces:** consumes `loadLanding()`; no navigation, a minimal footer.
- **Tests:** the four paths build; each is `noindex` with a canonical to the home page; the section order
  matches the spec; no email form; the page renders without the video file (F14); mobile viewport check
  as the site's existing responsive test does.
- **Command:** `cd website && npm run build && node --test test/landing-pages.test.mjs`
- **Depends on:** G2.1.
- **Commit:** `website: /save, /control, /scale and /crew from one template`
- **Red step:** the build test fails with four missing paths.
- **Reviewer tier:** standard.

### G2.3 The three third-party loads, behind consent

- **Files:** `website/public/scripts/site.js`, `firebase.json` (the CSP header only),
  `website/data/site.yaml`, `website/src/pages/privacy.astro`, `website/test/origins.test.mjs` (the
  existing origins test, extended), `internal/cli/docs_website_boundary_test.go` if it pins the CSP.
- **Interfaces:** consumes the consent gate; produces loaders for the pixel, the insight tag and the
  Calendly script that run only after consent and only when the path is one of the four.
- **Config change:** `site.yaml` gains the three identifiers (public) and the allowed hosts; the CSP
  widens by exactly the documented origins and gains a `frame-src`.
- **Tests:** no request to any of the three before consent; none after consent on a non-campaign page; consent
  withdrawal stops them; the privacy page names the three; the CSP lists exactly the new origins.
- **Command:** `cd website && node --test test/origins.test.mjs && npm run check` and
  `go test ./internal/cli/ -run Docs -count=1`
- **Depends on:** G2.2.
- **Commit:** `website: the pixel, the insight tag and Calendly, after consent on the campaign pages`
- **Red step:** the origins test fails because the pages carry no such loaders and the CSP lacks the hosts.
- **Reviewer tier:** `opus + mandatory independent review` (privacy and consent).

### G2.4 Key events and UTM pass-through

- **Files:** `website/public/scripts/site.js` (the same file as G2.3, a later level),
  `website/test/landing-events.test.mjs` (new).
- **Interfaces:** emits `demo_booked`, `install_click` and `whatsapp_click` to GA4 after consent; captures
  UTM parameters at load and passes them into the Calendly embed.
- **Tests:** each event fires once on its action and not before consent; UTMs reach the Calendly URL;
  absent UTMs leave it unchanged.
- **Command:** `cd website && node --test test/landing-events.test.mjs`
- **Depends on:** G2.3.
- **Commit:** `website: landing key events and UTM pass-through`
- **Red step:** the tests fail because the events are not emitted.
- **Reviewer tier:** standard.

### G2.5 Inputs, preview and checks: OWNER-RUN

- The owner supplies: the Calendly link, the WhatsApp Business number, the proof figure's aggregation, the
  video and the `/crew` imagery (each only when they exist; the pages build without them).
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
  coordinator and orchestrators are to move into `ee/` later, and T1 open).
- **Tests:** `go test ./internal/cli/ -run Docs -count=1`
- **Depends on:** none.
- **Commit:** `docs(licensing): the console, mobile app, coordinator and orchestrators move to ee/ later`
- **Red step:** none (documentation).
- **Reviewer tier:** standard.

**Check 162: OWNER-RUN, after G1.2 and G1.3 are released.** The owner runs `fugaro estimate --run <ID>
--model <the run's own model>` on one finished sandbox run; the figure equals the record's `model_usd` to
the cent. Checks 163-169 are reserved and unused.

## Parallel execution

Edges, from the "Depends on" lines: #1115 (external) → G1.1 → {G1.2, G1.3}; G1.3 → G1.4; G2.1 → G2.2 →
G2.3 → G2.4 → G2.5; G3.1, G5.1 and G2.1 have no dependency.

| Level | Tasks (run concurrently) |
| --- | --- |
| L0 | G1.1 (after #1115 merges), G2.1, G3.1, G5.1 |
| L1 | G1.2, G1.3, G2.2 |
| L2 | G1.4, G2.3 |
| L3 | G2.4 |
| L4 | G2.5 (owner-run), Check 162 once released |

**Critical path:** G2.1 → G2.2 → G2.3 → G2.4 → G2.5. It is sequential because each task builds on the
previous one's interface and G2.3 and G2.4 write the same file. The estimate path (#1115 → G1.1 → G1.3 →
G1.4) is shorter and independent of it.

### Shared files

| File | Writers | Levels |
| --- | --- | --- |
| `website/public/scripts/site.js` | G2.3, G2.4 | L2, L3 (never together) |
| `website/src/content.ts` | G2.1 | L0 |
| `website/test/origins.test.mjs` | G2.3 | L2 |
| `internal/runstore/cost.go`, `schemas/result.schema.json` | G1.1 (and #1115, merged first) | L0 |
| `internal/cli/root.go` | G1.3 | L1 |
| `website/content/cli-reference.md`, `plugin/skills/working/SKILL.md` | G1.4 | L2 |
| `docs/licensing/README.md` | G5.1 | L0 |

No level holds two writers of one file. A change to any task's Files re-checks this table.

## Not in this plan

SSO, audit logs, the contract and DPA (D402, the owner's legal part), the per-file `ee/` notice (T1), the
price and what a seat buys (T2), the X drafting agent, lead lists, outreach, the videos and every other
item of the agenda's action table (D403, R5).
