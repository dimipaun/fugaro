# The Fugaro website: implementation plan

Status: **the site is built.** Owner: **@Dimi**. Date: **2026-10-16**, the day **all twenty decisions (D90–D109) were answered** — D91 and D93 against the reconciliation's recommendation, the other eighteen as recommended. **An answered decision is not an approved plan**: each wave still starts only on the owner's go (§7.1, §15). **Parts 0 and 1 of this plan landed on 2026-10-17** — `website/` builds eleven routes and `npm run check` passes — and the redesign ([redesign/](redesign/README.md)) is the line of work now open against the built site. This plan executes [reconciliation.md](reconciliation.md), whose findings it cites by number (`F<n>`); the owner's own text is [spec.md](spec.md). Directory index: [README.md](README.md).

Related: [docs/plans/2026-10-16-managed-projects.md](../plans/2026-10-16-managed-projects.md) (the shape this plan follows, and the sibling that holds D55–D78 and checks 67–68), [docs/design/desktop-console.md](../design/desktop-console.md) (D2, D4, D10, D11 — read, not reopened), [docs/release.md](../release.md), [docs/pending-workflows/README.md](../pending-workflows/README.md), [docs/gcp-live-checklist.md](../gcp-live-checklist.md), [CONTRIBUTING.md](../../CONTRIBUTING.md).

## 0. What this plan is, and the one thing it does in an unusual order

This is a plan for a second artifact that does not exist yet, in a repository whose own rules make three of its steps the owner's: the site's **deploy** is a `.github/workflows/*` change (F4), its **domain** is a registration (F15), and its **hosting, palette, tagline and aggregation data policy** are decisions (D90–D109). So the plan is ordered by what is **provable without a deployed site** and by what is **independent of the owner's answers**, and the parts are cut so that everything before Part 2 is mergeable on its own merits — the drift tests and the aggregation command are worth having even if the site is never built, and the site's pages are worth having even if the host is never chosen.

**The unusual order is deliberate: the facts come before the site.** Every page the spec asks for that is *about* the product is a rendering of something that already exists in this repository — the install instructions, `SECURITY.md`, `docs/releases/vX.Y.Z.md`, the command tree, the run records. Part 0 builds the machinery that turns each of those into a file, and a test that fails when the file and its source disagree. Part 1 builds a site that renders those files and nothing else. A site built the other way round — pages first, facts pasted in — is the failure mode F21 describes four times over: the copy most likely to be read is the copy least likely to be updated.

**Two things this plan does not do.** It does not answer any decision row — **the owner answered all twenty on 2026-10-16, and §13 records them**; what this plan still holds back is the **go**, because §7.1's rule (the gate at §15) starts a wave only when the owner gives one, and behind that alone. **Two of the answers change what this plan says it will build** — D91 (Firebase Hosting, in a separate Firebase project created for the website only, rather than Cloudflare Pages or GitHub Pages) and D93 (Firebase Analytics behind a consent banner, rather than none) — and both are carried into the tasks below rather than noted at the front. And it does not touch a single file under `.github/workflows/` — the deploy workflow is **staged** under `docs/pending-workflows/` for the owner to copy, which is the mechanism the repository already uses (F4, F62).

## 1. Part summary

| Part | What it ships | Tasks | Independently mergeable? | Reviewer tier |
|---|---|---|---|---|
| **Part 0 — The facts** | The generated content layer: install facts, site facts, the CLI reference, the security page's source, the changelog's source, and `fugaro stats runs` — the owner-run, read-only aggregation. No pages. | W0.1–W0.6 | Yes, each on its own; the whole part merges without Part 1 | **opus** (W0.3 is SECURITY-SENSITIVE), sonnet for the rest |
| **Part 1 — The site** | The Astro app under `website/`: toolchain, tokens and palettes, layout, the generated-content pipeline, and the ten sections of the spec that the repository can back. Plus its hermetic gates — build, link/anchor check, drift tests, accessibility and origin lint — and the consent banner that keeps analytics behind consent (D93, D106). | W1.1–W1.12 | Yes, per PR group; the shell (W1.1–W1.3) must land before the pages | **opus** for the boundary and origin tests, sonnet for the pages |
| **Part 2 — Hosting, deploy, live checks** | The staged deploy workflow and the `firebase.json` it deploys with — both aimed at the **separate Firebase project the owner creates for the website only** (D91) — the owner's copy, the canonical URL ~~and the secondary-domain redirect~~ (the owner owns no second domain — ruling of 2026-10-17, the amendment under W2.2), the register's check 69, and the video slot's empty state. | W2.1–W2.4 | Yes, after W1.1 | **opus** (the workflow is staged, not written, and its content is a security boundary) |
| **Part 3 — After the console ships** | The console download links become real; the explainer video is embedded; **Firebase Analytics is switched on behind the consent banner D93 chose**. **W3.2 has landed** (2026-10-18) **and W3.1 landed on 2026-10-19 against the published `console-v0.8.7` release; W3.3 is still unscheduled, waiting on the video.** W3.1's own edge was the console's first release, and W3.2 never waited on a decision (D93 was answered on 2026-10-16). Owner ruling 2026-10-17: W3.2 goes BEFORE W3.1 in the `site.yaml` order, because W3.1 cannot happen before a console release. | W3.1–W3.3 | Yes | **opus** (a public link to a signed artifact) |

## 2. Check numbers this plan takes (69 and up)

**This plan takes 69 and up, written as 69a–69c**, in [docs/gcp-live-checklist.md](../gcp-live-checklist.md). The register's highest written `## Check N` section on `origin/main` is **58**; 59 (59a/59b), 60, 61, 62–63, 64–66 and 67–68 are reserved by the console, budget-token-channel, project-centric, console-v2, voice and managed-projects lines respectively, each written down where it was allocated. **69 is the next free number, and the sub-letters are the register's own convention** for one line of work's several live steps (43a–43f, 59a/59b, 67a/67b):

| Check | What it is | Who runs it | Written by |
|---|---|---|---|
| **69a** | **The site is live and honest.** The canonical URL resolves over HTTPS, the deployed HTML is the artifact the build produced, and the page a stranger lands on names no unshipped feature (no console download link, no video player, no platform the build does not produce). There is no second domain to redirect from (owner ruling 2026-10-17, the amendment under W2.2). | Owner | W2.2 |
| **69b** | **Nothing phones home.** Loading the live page with the network panel open shows **no request to an origin outside the allowed-host list** — no font, no script, no embed, no pixel — and — **D93's answer does choose analytics** — nothing is requested from the analytics origin **until consent is given**, after which exactly that one origin and no other third party appears. | Owner | W2.2 |
| **69c** | **The published ranges are real.** The caps-and-costs page's figures are re-derived from the aggregation command over the same window and match, the sample sizes are printed, and the "not a promise" line is present. | Owner | W2.3 |

**The "67 onward" collision, and the reading this plan applies.** The sentence all three documents quote hands the register **"managed projects 67 onward"**, and the managed-projects plan reads that as an open-ended block (`docs/plans/2026-10-16-managed-projects.md` §2, step 5: *"a block is held for the work it was given"*). Read that way, 69 is inside another line of work's block and is not free — so this plan applies the **bounded** reading and records it here rather than letting the number speak for itself: "onward" is spent on the numbers a line of work actually takes and writes (managed projects took 67 **and** 68), a clause that reached every number above 67 would leave no later line of work able to take one at all, and **if the owner reads the clause as open-ended, or any document on `origin/main` writes 69, then 69 is managed projects' and the owner allocates** — W2.2 re-reads the register *before* it writes anything and stops rather than renumbering (U6), and §15 carries the question as a gate item. **A reserved number is never reused or renumbered** either way, and as in every precedent **this plan does not write the row or the section**: W2.2 owns both, so one writer owns the number and a rebase cannot produce two.

## 3. Global Constraints

1. **DOCS-ONLY until the owner gives a go.** The commit that adds this plan and the reconciliation touches **only** `docs/website/*`. **Every decision in §13 is answered (2026-10-16), so no task waits on an answer any more**; what still holds a task back is the **go** — no task starts before its wave may start under §7.1's rule (the gate at §15).
2. **Nothing under `.github/workflows/` is ever created, edited or deleted by a run** (F4). A workflow change is authored under `docs/pending-workflows/` and copied in by the owner, and the staged file is removed in the same PR that copies it (the procedure `docs/pending-workflows/README.md` describes).
3. **`GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` must pass** after every task, and `go test ./...` must pass after any task that adds or changes Go code (F10).
4. **Tests are hermetic**: no real cloud, no network, no clock, no credential (F58). Every gate in this plan runs from the built artifact and the files in the tree; the live checks are the owner's, numbered 69 and up.
5. **No new third-party dependency in the Go module for the site's benefit.** The generators of Part 0 use the standard library and what the module already imports; the CLI reference walks `NewRootCmd()` rather than adding `cobra/doc` if the walk is a hundred lines, and if `cobra/doc` is added it is added with the version already in `go.mod`.
6. **One source of truth per fact, and a test that holds it** (F37). A page never restates a document; it renders it or links it. Every generated file names its source in a header comment.
7. **The site's own text is checked against `plugin/readme_test.go`'s retired-name walker**: files under `docs/` are read by `TestReadmeNamesNoRetiredSkill`, so no file this plan adds may name a retired skill (F7).
8. **Nothing on the page loads from a third party, except the one script the owner chose in D93** (F55, D106): fonts self-hosted, no CDN, no embed outside the allowed-host list, and the promise is enforced by a test over the built HTML's origins — **with exactly one permitted exception, Google's Analytics script, loaded only after the visitor consents and with no other third party**. The origins test allows that script **and only that script, and only behind the consent gate** (W1.12); a second analytics origin, a font CDN or an embedded player fails it.
9. **The site never links an unsigned or unreleased artifact** (F25): the downloads section renders from a data flag, and a test refuses to emit a link while the flag is false.
10. **The aggregation reads one named installation, read-only, and emits no identifiers** (F32, D96): no repository name, run ID, branch, PR URL, task text, person or commit appears in its output, **`belong` and its repositories are named by no output, no check, no published figure and no test fixture this plan produces** — every red step and every test fixture uses a synthetic name, and the documents that record the rule name it only in order to state it — and `belong` is never read by an agent; **D96's answer names the default target as the sandbox/dogfood installation, `fugaro`, and says `belong` is never aggregated unless the owner says so**.
11. **No number is published from a sample smaller than the minimum** (D97, and D96's answer fixes the minimum at **20 runs**), and every published range carries its window, its sample size and the "not a promise" line.
12. **Security claims are never paraphrased** (F41, D99): the security page is generated from `SECURITY.md`, byte for byte below the title, with its headings unchanged so the document's own cross-references keep pointing the right way. **(Departed from, of the page, as shipped 2026-10-19: the owner's brief for `/security/` cut it to the document's "In short" section, its reporting section and a link, and the brief of 2026-10-20 put a boundary diagram of the trust boundaries above them, drawn in the document's own words, so the page is not the document byte for byte; the generated copy is, and `website/test/security.mjs` holds the page to the document's own words — every label of the drawing a phrase of the document — while the drawing's arrangement is the page's own decision, written down in `security.astro` and read by no gate. The redesign's P4 records it — `redesign/implementation-plan.md`, and the Global Constraint 12 row of `redesign/reconciliation.md`.)**
13. **The release train is not touched** (F5, F61, D109): no `website` check is added to `ci.yml` or `scripts/release-gate.sh`, and no CLI tag depends on the site.
14. **Add new commits; never amend or rebase** (this run's own rule, and the same discipline the sibling plans carry).
15. **Every task's red step is real**: the test named in `Red step` must fail before the task's code exists, and the task records the exact `Expected: FAIL` line it saw.

## 4. Review focus

| Risk | Where it bites | Who reviews |
|---|---|---|
| A generated file drifts from its source and the test is written against the generator rather than against the source | Part 0, all of it | opus — the test must read the **source** (the README, the goreleaser config, the struct, the directory listing), not the generator's own output |
| The aggregation leaks an identifier — a repository slug in a group key, a run ID in a "sample" field, a task title in an example | W0.3 | **opus, SECURITY-SENSITIVE** — the test is a golden-output test over the command's `--json`, asserting the **set of keys** and that no value matches any identifier shape |
| A public page makes a claim the product cannot back (the four of F50) | W1.4, W1.5, W1.6, W1.10, W3.x | opus |
| A third-party origin reaches the built HTML through a font, an icon set or an embed — **or the one allowed exception escapes its gate** (an analytics request before consent, a cookie before consent, consent that cannot be withdrawn, a banner a keyboard cannot reach) | W1.11, **W1.12** | opus |
| A staged workflow that deploys more than it says, or holds a credential it should not | W2.1 | **opus** — the staged file is read line by line against D91, D109 and F14 |
| The site's build depends on the network (a Google font, an npm postinstall that fetches) | W1.1 | opus |

## 5. File structure

**What exists today:** nothing under `website/`; nothing under `docs/website/` except the four documents this plan's PR adds and the W2.1 amendment's fifth, `preview-promote.md` (below); no CLI reference; no generator of any kind; `internal/cli/docs_*.go` as the only document-pinning tests.

**What this plan creates:**

```
docs/website/                     # the plan's PR — the four documents; W2.1's amendment of 2026-10-18 adds a fifth, preview-promote.md (the owner-only one-time setup the two staged workflows need)
docs/pending-workflows/
  website-preview.yml             # W2.1 — staged, for the owner to copy; the manual preview deploy (amendment of 2026-10-18)
  website-promote.yml             # W2.1 — staged, for the owner to copy; the owner-only promote to live (amendment of 2026-10-18)
firebase.json                     # W2.1 creates it, and it carries no redirects — a redirect rule cannot match on host, and there is no second domain (W2.2's amendments)
website/                          # Part 1 — the artifact
  package.json, package-lock.json, astro.config.mjs, tsconfig.json
  README.md                       # how to build and check it, and what is generated
  data/
    install.yaml                  # W0.1 — the one install source
    site.yaml                     # W0.2 — canonical URL, analytics, feature flags
  content/
    cli-reference.md              # W0.4 — generated, do not edit
    security.md                   # W0.5 — generated from SECURITY.md, do not edit
    costs.json                    # W0.3 — the aggregation's output, published deliberately
  src/
    lib/changelog.mjs             # W0.6 — builds the changelog from docs/releases/ at site build time; its output is not committed
    layouts/Base.astro
    components/{Hero,Terminal,ArchitectureDiagram,CapsCosts,Downloads,Changelog,ConsentBanner}.astro   # ConsentBanner is W1.12
    pages/{index,how-it-works,getting-started,caps-and-costs,console,docs,security,changelog,faq,community}.astro
    pages/privacy.astro           # W1.12 — what the site measures, when analytics loads, how consent is given and withdrawn
    styles/tokens.css, palette-violet.css, palette-orange.css, palette-split.css
  test/                           # `npm run check` (W1.1) runs every file here
    changelog.mjs                 # W0.6 — the changelog builds and covers every release file
    readme.mjs                    # W1.1; a11y.mjs W1.2 then W1.11; layout.mjs W1.3
    home.mjs, caps-and-costs.mjs, console.mjs, docs-hub.mjs, security.mjs, changelog-page.mjs, faq-community.mjs   # W1.4–W1.10, one each
    linkcheck.mjs, origins.mjs, budget.mjs   # W1.11; origins.mjs also W1.12 (the one allowed exception)
    consent.mjs                   # W1.12 — no analytics request before consent, withdrawal, no cookie, keyboard
    video.mjs                     # W2.4
internal/clidoc/                  # W0.4 — the generator, in the Go module
  cliref.go, cliref_test.go
internal/cli/
  docs_website_boundary_test.go   # W0.2 — the console's boundary test, for the site
  docs_website_install_test.go    # W0.1 — install facts against the repository
  docs_website_cliref_test.go     # W0.4 — the reference against NewRootCmd
  docs_website_security_test.go   # W0.5 — the page against SECURITY.md
  stats.go, stats_test.go         # W0.3 — the aggregation command
internal/stats/
  stats.go, stats_test.go         # W0.3 — percentiles, windows, the minimum-sample rule
```

## 6. PR groups

Each group is one pull request, each independently mergeable, each with its own verification.

| PR | Tasks | What it is | Reviewer |
|---|---|---|---|
| **P1** | W0.1, W0.2 | The site's facts and the boundary test. Mergeable alone: the facts are worth having before the site, and the boundary test is a rule the repository should hold either way. | sonnet |
| **P2** | W0.3 | `fugaro stats runs` — the owner-run aggregation, and its no-identifiers test. **SECURITY-SENSITIVE.** Mergeable alone: it is a CLI feature with a real use (`fugaro report` does not answer "how long does a task take"). | **opus** |
| **P3** | W0.4, W0.5, W0.6 | The three generators and their drift tests. Mergeable alone: a CLI reference and a rendered security document are useful to readers who never open the site. | sonnet |
| **P4** | W1.1, W1.2, W1.3 | The site's shell: toolchain, tokens and palettes, layout, the content pipeline that fails on a missing generated file, and the build gate. | sonnet |
| **P5** | W1.4, W1.5, W1.6 | The product pages: hero and how it works, getting started, caps and costs, console placeholder. | sonnet |
| **P6** | W1.7, W1.8, W1.9, W1.10 | The document pages: docs hub, security, changelog, FAQ and community. | sonnet |
| **P7** | W1.11, W1.12 | The gates: link and anchor check, accessibility and origin lint, the JavaScript budget — plus **the consent banner**, which is the gate that keeps the one allowed third-party script behind the visitor's consent (D93, D106). | **opus** |
| **P8** | W2.1, W2.2, W2.3 | Hosting: the staged workflow and its `firebase.json`, aimed at the **website-only Firebase project** (D91), the owner's copy, the canonical URL, and the register's check 69 (no secondary-domain redirect: the owner owns no second domain — ruling of 2026-10-17, W2.2's amendment). | **opus** |
| **P9** | W2.4 | The video slot's empty state. | sonnet |
| **P10** | W3.1, W3.2, W3.3 | W3.2 **landed** 2026-10-18 and W3.1 **landed** 2026-10-19 (the `console-v0.8.7` release); **W3.3 unscheduled**, after the video. | **opus** |

## 7. Parallel execution

**Edges, read off each task's `Depends on` line, and every one of them is a task that genuinely waits.** W0.1 has none. W0.3 has none (its own lane; it writes only inside the Go module). W0.2 ← W0.1. **W0.4, W0.5 and W0.6 have none either** — they write into `website/content/`, `website/src/lib/`, `website/test/changelog.mjs` and `internal/clidoc/`, which no other task writes, and W0.2's boundary test is a rule over the tree rather than a gate in front of them. W1.1 ← {W0.2, W0.4, W0.5} (the build reads `data/` and `content/`; the changelog is built at build time, so W1.1 does not wait for W0.6). W1.1 → {W1.2, W1.3}. W1.3 → {W1.4, W1.5, W1.6, W1.7, W1.8, W1.9, W1.10}, with W1.5 ← W0.3 (the figures), W1.8 ← W0.5 and W1.9 ← W0.6. {W1.2, W1.4…W1.10} → W1.11. **W1.11 → W1.12** (the consent banner extends the origins check W1.11 writes and gates the one script W1.11's allowed list admits). W1.11 → W2.1 → W2.2. W0.3 → W2.3, W1.5 → W2.3, and W2.2 → W2.3 (the check number). W1.3 → W2.4, and W2.4's `Depends on` line also names W1.4 and W2.2 for the two files it shares with them (below). **W1.12 → W3.2** (the analytics switch turns on behind the banner). W3.1 and W3.3 are gated on the owner's own events rather than on a task; **W3.2 is not — D93 was answered on 2026-10-16, so it waits on W1.12 and on the `site.yaml` order ahead of it (W2.4), and on nothing else. W3.1 follows W3.2 in file order (owner ruling 2026-10-17).**

**The file conflicts that order tasks rather than dependencies.** `website/src/layouts/Base.astro` is written by W1.3 (L3) and then by **W1.12** (L6 — the analytics loader, rewritten so nothing loads before consent), and **W3.2 no longer writes it**: turning analytics on is `site.yaml`'s id and flag plus the record in the site's README, and the loader itself is unchanged; `website/test/origins.mjs` is W1.11's (L5) and then **W1.12's** (L6, the one allowed exception); and `website/src/components/ConsentBanner.astro`, `website/src/pages/privacy.astro` and `website/test/consent.mjs` are **W1.12's**, with W3.2 extending `consent.mjs` in a later level. `website/src/components/Hero.astro` is written by W1.4 and later by W2.4 (the video slot lives in the hero), so W2.4 follows W1.4. `website/data/site.yaml` has **six writers, each of them after the previous one has merged**, in the order W0.2 → W2.2 → W2.4 → W3.2 → W3.1 → W3.3 *(owner ruling 2026-10-17; the order was W3.1 → W3.2 before, and W3.2 → W3.1 swaps only those two)*: W0.2 creates it, W2.2 sets the canonical URL, W2.4 sets `features.video`, W3.2 sets `analytics`, **W3.1 flips `features.console_downloads` (D95, reconciliation D95)** and W3.3 sets the video's URL — and no two of them share a level. **That puts W3.2 at L9 and W3.1 one level after it (L10), and W3.3 after W3.1 (L11); the earlier order made W3.2 follow W3.1 in file order, so analytics could not go on until the console's first release — the owner ruled that backwards, since W3.1 is the task that cannot happen before the console is released and W3.2 can.** `internal/cli/docs_website_boundary_test.go` is the one file **outside** `website/` with the same shape: W0.2 writes it (L1) and it pins `site.yaml`'s facts, so **W2.2 relaxes the canonical-URL pin and strikes the secondary-domain one with it (L7; the owner ruled on 2026-10-17 that no second domain is theirs), W2.4 the `features.video` pin (L8) and W3.1 the `console_downloads` pin (L10, where its row already names the file)** — one writer per level, each in the commit that changes the value its message names. It is deliberately *not* on W3.2's list: the analytics pin in it asserts that the flag and the measurement id agree with each other rather than that they are off, so W3.2 turns the measurement on by setting both and meets no red test and no second writer in its level. `docs/gcp-live-checklist.md` has **two writers in two levels**: W2.2 writes the summary-table rows for 69a/69b and the `## Check 69a`/`## Check 69b` sections in **L7**, and **W2.3 writes `## Check 69c` with its row** in **L8**, the level after W2.2's, which is also the answer to "who owns check 69" — W2.2 owns 69a and 69b, W2.3 owns 69c, and no level ever has two writers of the file. Everything else that looks shared runs one way only: `site.yaml` is **read** by W1.3, W1.6, W1.9 and W2.4; `tokens.css` is W1.2's and every page reads it; `website/test/a11y.mjs` is W1.2's first (L3) and W1.11's later (L5); `website/content/*` and `website/src/lib/changelog.mjs` are written by Part 0 and read by Part 1 — that direction is the point of the order, and it is why Part 0 is parallel with Part 1's *shell* and serial with Part 1's *content*.

**Levels.** A level's tasks can run concurrently — **no two tasks in a level write the same file** — and a level starts when the level before it has merged.

| Level | Tasks | PR | Reviewer | Files touched |
|---|---|---|---|---|
| **L0** | W0.1, **W0.3**, W0.4, W0.5, W0.6 | P1, P2, P3 | sonnet, **opus** (W0.3) | `website/data/install.yaml`; `internal/cli/docs_website_install_test.go`; `internal/stats/*`, `internal/cli/stats*.go`; `internal/clidoc/*`; `website/content/{cli-reference,security}.md`; `internal/cli/docs_website_{cliref,security}_test.go`; `website/src/lib/changelog.mjs`, `website/test/changelog.mjs` |
| **L1** | W0.2 | P1 | sonnet | `website/data/site.yaml`, `internal/cli/docs_website_boundary_test.go` |
| **L2** | W1.1 | P4 | sonnet | `website/{package.json,package-lock.json,astro.config.mjs,tsconfig.json,README.md,.gitignore}`, `website/test/readme.mjs` |
| **L3** | W1.2, W1.3 | P4 | sonnet | `website/src/styles/*`, `website/test/a11y.mjs`; `website/src/layouts/Base.astro`, `website/src/content.ts`, `website/test/layout.mjs` |
| **L4** | W1.4, W1.5, W1.6, W1.7, W1.8, W1.9, W1.10 | P5, P6 | sonnet | `website/src/components/*`, `website/src/pages/*`, `website/data/faq.yaml`, `website/content/{costs,downloads}.json`, and one test file each: `website/test/{home,caps-and-costs,console,docs-hub,security,changelog-page,faq-community}.mjs` |
| **L5** | W1.11 | P7 | **opus** | `website/test/{linkcheck,a11y,origins,budget}.mjs` |
| **L6** | **W1.12**, W2.1 | **P7**, P8 | **opus** | `website/src/components/ConsentBanner.astro`, `website/src/pages/privacy.astro`, `website/src/layouts/Base.astro`, `website/test/{consent,origins}.mjs`; `firebase.json`, `docs/pending-workflows/website.yml`, `docs/pending-workflows/README.md` |
| **L7** | W2.2 | P8 | **opus** | `website/data/site.yaml`, `internal/cli/docs_website_boundary_test.go` (the canonical-URL pin, and the pins that refuse a second domain), ~~`firebase.json` (the redirects)~~ (there are none: W2.2's amendments), `docs/gcp-live-checklist.md` |
| **L8** | W2.3, W2.4 | P8, P9 | **opus** (W2.3), sonnet (W2.4) | `website/content/costs.json`, `website/src/pages/caps-and-costs.astro`, `docs/gcp-live-checklist.md`; `website/src/components/Hero.astro`, `website/data/site.yaml`, `internal/cli/docs_website_boundary_test.go` (the `features.video` pin), `website/test/video.mjs` |
| **L9** | **W3.2** | P10 | **opus** | `website/data/site.yaml`, `website/README.md`, `website/test/consent.mjs` |
| **L10** | W3.1 | P10 | **opus** | `website/content/downloads.json`, `website/src/pages/console.astro`, `website/data/site.yaml` (`features.console_downloads`), `internal/cli/docs_website_boundary_test.go` |
| **L11** | W3.3 | P10 | **opus** | `website/data/site.yaml`, `website/src/components/Hero.astro`, `website/content/` (the poster) |

**How the levels were checked.** A small script parsed every task's `Depends on` and `Files` lines, added the file-order edges above (`site.yaml`: W0.2 → W2.2 → W2.4 → W3.2 → W3.1 → W3.3; `Hero.astro`: W1.4 → W2.4 → W3.3; `Base.astro`: W1.3 → W1.12; `origins.mjs`: W1.11 → W1.12; `consent.mjs`: W1.12 → W3.2; `firebase.json`: W2.1 → W2.2; `gcp-live-checklist.md`: W2.2 → W2.3; `a11y.mjs`: W1.2 → W1.11; `internal/cli/docs_website_boundary_test.go`: W0.2 → W2.2 → W2.4 → W3.1 (W3.2 does not write it), with W3.1's own row already naming the file), and placed each task at one more than the highest level of anything it follows. It confirmed that every task sits in exactly one level, that no two tasks in a level write one file, that every level is as early as its edges allow (**L0–L11** — W1.12 and W2.1 join L6, W3.2 joins L9, W3.1 joins L10, W3.3 L11), and that every arrow on the critical path below is a `Depends on` line. **W3.1 and W3.3 are placed by their owner events rather than by an edge; W3.2 is placed by its edges and by the `site.yaml` order behind W2.4 (owner ruling 2026-10-17: W3.2 before W3.1)**, which is why Part 3's levels are not a schedule (§ below).

**Part 3's levels are not a schedule.** W3.1 waits on the console's first release and W3.3 on the video — owner events — so they start in whatever order those arrive; **W3.2 no longer waits on an owner event (D93 was answered on 2026-10-16) and is placed at L9 by its edges and by the `site.yaml` order behind W2.4, ahead of W3.1 (owner ruling 2026-10-17).** The pairs that *cannot* run together are W3.1, W3.2 and W3.3, because all three write `site.yaml`, which is why each sits one level after the previous (W3.2 at L9, W3.1 at L10, W3.3 at L11). **Of those events, the console's first release has arrived: W3.1 landed on 2026-10-19 against `console-v0.8.7`, so L10 is spent and only W3.3 (L11) still waits on an owner event.**

**The critical path** is the longest chain, and its tail is the owner's, not the agent's:

> **W0.1 → W0.2 → W1.1 → W1.3 → W1.4 → W1.11 → W2.1 → W2.2 → W2.3 → check 69c**
> with **check 69a/69b** run by the owner after W2.2, and Part 3 (W3.2, W3.1, W3.3, in that file order) after it — W3.1 and W3.3 after the owner's own events (the console's first release and the video, which no task's `Depends on` line names, so they are drawn as no arrow) and **W3.2 after W1.12**, now that D93's answer is in and it waits on no event.

**Nine agent tasks on the chain (L0–L8), and the owner-run points beside and after it** (the live checks 69a/69b and 69c, the console's first release and the video). W2.4 is not on the chain: it shares L8 with W2.3 and ends there. **Everything else is a side branch and can land in any order**: W0.3 (the aggregation) is the largest single piece of work in the plan and is on no path to the site at all — it blocks only W2.3, the published figures, which is the one page that can wait. W0.4, W0.5 and W0.6 are three independent generators, and a reader benefits from each the day it merges. W1.5 through W1.10 are six independent pages behind one shell.

**What the parallelism buys, stated honestly.** The plan's wall-clock is not the sum of its tasks: L0 is five agents, L4 is seven, and the only true serialization is the shell (W1.1 → W1.3) and the gates (W1.11 → W1.12, and W2.1). The reason to cut it this way is not speed but **independence**: every level's PR is mergeable alone, and what the owner's **go** holds back is bounded and known — with every decision answered, §7.1 states it wave by wave, and the register's "67 onward" clause is the one item Part 2 still waits on beyond the go.

### 7.1 What can start, and under which answers

**One rule, and §3, §13, §15, the README and the reconciliation point here rather than restating it.** The owner answered **every decision (D90–D109) on 2026-10-16**, so **no wave waits on an answer any more**. What is left is the **go**, and the owner's direction of 2026-10-16 — to *start as soon as we can* — is a direction and not that go. So:

> **A wave may start as soon as the owner has given a go for that wave. No decision blocks anything any more, and nothing else blocks either** — no other row of §4 and no other item of §15.

The waves, and what each one now waits on:

- **Wave 1 — W0.1, W0.3, W0.4, W0.5 and W0.6 (level 0): LAUNCHED on the owner's go of 2026-10-17** ("go wave 1"; five parallel cloud runs, one draft pull request each). **W0.2 is level 1 and is NOT in wave 1: it depends on W0.1 and waits for it to merge, then needs the owner's go for its wave.** Every one of them writes under `website/`, and that location was D90's question — **D90 was answered as recommended (this repository, under a top-level `website/`), so the paths §5 lists stand as written and no task changes prefix.** They write files that share nothing, they depend on nothing (W0.2 on W0.1 alone), and each is worth merging on its own — the install facts and the rule that pins them, the site's facts and the boundary test, the CLI reference, the rendered `SECURITY.md`, the changelog's generator.
- **W0.3 is in wave 1 on the same go**: it writes only inside the Go module, and **D96 was answered as recommended** (the command, the named sandbox/dogfood installation, `belong` never aggregated unless the owner says so, a minimum of 20 runs) — so it needs no path from D90 and no second decision.
- **Part 1 — W1.1 to W1.12: nothing but the go, plus the Part 0 tasks their `Depends on` lines name** (W0.2, W0.4 and W0.5 for the build; W0.3, W0.5 and W0.6 for three of its pages). **D104 was answered as recommended — palette C, the split one, is the palette in force; D102 was answered as recommended — the tagline is `README.md`'s own line; D93 was answered against the recommendation — Firebase Analytics, so W1.12 ships the consent banner and the gate, and the script tag stays off until W3.2 turns it on.**
- **Part 2 — W2.1 to W2.4: nothing but the go — the decisions are answered — and the register's "67 onward" clause** (§2, §15 item 6), because W2.2 writes check 69. **D91's answer is Firebase Hosting in a separate Firebase project created for the website only**: W2.1's staged workflow and the `firebase.json` it writes target that project, the owner supplies its project id at deploy time through the workflow's variable, and no installation's project id appears anywhere. **D92's answer is the owner's own registration**: the site takes one configured canonical URL value, no agent registers a domain or touches DNS, and the custom-domain step is the owner's. **W2.3 also runs W0.3**, whose D96 answer is recorded above.
- **Part 3 — W3.1 to W3.3 (file order W3.2, W3.1, W3.3 by the owner's ruling of 2026-10-17): W3.2 landed on 2026-10-18 and W3.1 on 2026-10-19 against the `console-v0.8.7` release, which is the console's first — the owner's own event, and the one W3.1 waited on. W3.3 still waits on the video**, the other owner event. W3.2 waited only on W1.12, now that D93 is answered: it sets the measurement id, turns `analytics.enabled` on and records the disclosure, and the banner already gates the load.

## 8. Part 0 — The facts the site renders

### Task W0.1: The install facts, and the test that holds them to the repository

**Files:** `website/data/install.yaml` (new), `internal/cli/docs_website_install_test.go` (new).

**Steps.**
1. Write `website/data/install.yaml` with one entry per install path, each carrying a `source:` field naming the file its text came from, and **no command that is not in `README.md` or `docs/release.md`**. The five entries are what the repository really ships, not what a site would prefer to ship:
   - **Homebrew** — `brew install dimipaun/tap/fugaro`, the cask `README.md` marks *"(tested on macOS)"*, resolving through `dimipaun/homebrew-tap`; `source: README.md`.
   - **The release archive** — the sentence `README.md` actually prints: download an archive from the **Releases page** (`https://github.com/dimipaun/fugaro/releases`), *darwin and linux, amd64 and arm64*, and verify it with cosign. The site **links the Releases page**; it does not build a download URL, because `releases/download/` is exactly the string W0.2's test refuses and the archive's URL carries a version the site does not know. The name template — `fugaro_<version>_<os>_<arch>`, which is `archives[0].name_template` exactly; the `.tar.gz` a downloaded file ends in comes from that archive's `formats: [tar.gz]`, not from the template — is recorded for the reader, pinned to `.goreleaser.yaml`; `source: README.md`.
   - **cosign verification** — the block `docs/release.md` `## Verifying a release` prints, verbatim: `V=X.Y.Z`; `gh release download v$V -R dimipaun/fugaro -p checksums.txt -p checksums.txt.sigstore.json -p "fugaro_${V}_$(uname -s | tr A-Z a-z)_*"`; the four-line `cosign verify-blob checksums.txt \` … `--certificate-oidc-issuer https://token.actions.githubusercontent.com` command **as the block writes it, one flag per line with its backslash continuations**, so the verbatim match is against the block's own lines rather than a joined one-liner; and the `shasum -a 256 -c -` / `sha256sum -c -` check against `checksums.txt`; `source: docs/release.md`.
   - **`go install`** — `go install github.com/dimipaun/fugaro/cmd/fugaro@latest`, carrying `README.md`'s own note that it **is for development only** (it builds a `dev` version with no release images and no plugin pin); `source: README.md`.
   - **The plugin** — the route `README.md`'s `## Getting started` prints and no other: `fugaro init` wires the Fugaro plugin into the checkout's `.claude/settings.json`, `/fugaro:setup` writes `fugaro.yaml`, a teammate who opens and trusts the folder gets the plugin installed, and when the `/fugaro:` skills are not listed the repair is `fugaro upgrade --local` (which installs it through the `claude` CLI). **The marketplace commands** (`/plugin marketplace add dimipaun/fugaro`, `/plugin install fugaro@fugaro`, `claude plugin install fugaro@fugaro --scope project`) are what a person runs inside Claude Code and are documented in `docs/design/upgrade.md` and `docs/design/m11-setup-and-skills.md` — **not** in `README.md` — so the YAML leaves them out rather than widening the test's source list to internal design documents.
2. Write the test so that it reads **the repository**, not the YAML's own prose: the two files it reads are the two the product's own install text lives in. Every command string in the YAML must appear verbatim in `README.md`'s `## Getting started` **or** in `docs/release.md` — **a command neither file carries fails the test, which is what keeps an invented instruction (a `curl | tar` one-liner nobody ships, a marketplace command a reader cannot run) off the site**; the OS/arch matrix must equal `.goreleaser.yaml`'s `builds[0].goos` × `builds[0].goarch`; the archive name template must equal `archives[0].name_template` (which carries no extension) and the extension the YAML shows must equal `archives[0].formats`; the Homebrew line must equal the `homebrew_casks[0].repository` owner and the cask name; and the `go install` entry must carry the "development only" marker that `README.md` carries.
3. Add the negative half: the YAML must not name a platform that `.goreleaser.yaml` does not build (**this is F20's gate** — a future Windows build is the only way this test ever changes its answer), must not carry a version number anywhere (the site renders `latest`, or the release's own tag), and must contain no `releases/download/` URL — the archive path is the Releases page, which is also the rule W0.2 enforces for the console.
4. Run the red step, then the test, then the docs suite.

**Tests:** `TestWebsiteInstallFactsMatchTheRepository`, `TestWebsiteInstallFactsNameNoUnbuiltPlatform`, `TestWebsiteInstallFactsCarryNoVersion`. All three read `website/data/install.yaml` plus `README.md`, `docs/release.md`, `.goreleaser.yaml`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Website|Docs|Lint' -count=1`

**Depends on:** nothing. **Commit:** `feat(website): one install source, pinned to the README, the release guide and goreleaser`

**Red step.** Write the three tests first, with `website/data/install.yaml` absent. `Expected: FAIL` — `open website/data/install.yaml: no such file or directory`.

### Task W0.2: The site's facts, and the console's boundary test for the site

**Files:** `website/data/site.yaml` (new), `internal/cli/docs_website_boundary_test.go` (new).

**Steps.**
1. Write `website/data/site.yaml` with the values the pages read and nobody hard-codes: `canonical_url` (a placeholder, per D92), ~~`secondary_domain`~~ (struck: the owner does not own a second domain — ruling of 2026-10-17, the amendment under W2.2), `analytics` (an object whose `enabled` is `false`, and which **only W3.2 turns on** — D93 was answered on 2026-10-16 as Firebase Analytics behind the consent banner, so the flag is off until that task lands, and even then the load goes through W1.12's gate), `features: {console_downloads: false, video: false}` (D95, D107), `min_runs` (D97's N), and `allowed_hosts` (the origin list W1.11 enforces, seeded from the links the site actually makes: `github.com`, `raw.githubusercontent.com` and the site's own canonical origin).
2. Write `docs_website_boundary_test.go` asserting the three properties `TestConsoleIsOutsideTheGoModule` asserts for the console: no file under `website/` is imported by the Go module, `go.mod` names no dependency that exists only for the site, and no Go file embeds a file from `website/` — so the site stays a separate artifact whose only coupling to the module is a test.
3. Add the flag test: while `features.console_downloads` is `false`, no file under `website/` may contain a `github.com/dimipaun/fugaro/releases/download/` URL — the mechanical form of "never link an unsigned or unreleased console" (F25, Global Constraint 9).
4. Run the red step, then the suite.

**Tests:** `TestWebsiteIsOutsideTheGoModule`, `TestGoModHasNoWebsiteDependency`, `TestNoGoFileEmbedsFromWebsite`, `TestWebsiteLinksNoConsoleWhileTheFlagIsOff`. **Two more tests in the same file hold rules those four assume**: `TestWebsiteBoundaryWalkReadsEveryDirectoryOfTheSite` (a temp tree, the console boundary's `TestConsoleBoundaryWalkSkipsWhatTheGoToolSkips` applied to this walk, so a later tightening that steps out of `website/src/`, `website/dist/` or `website/node_modules` fails an assertion instead of quietly shrinking the console-link scan) and `TestWebsiteFactsMatchTheRepository`, which pins `site.yaml` to the sources it describes (`repository` to `go.mod`'s module path, `min_runs` to `internal/stats.MinRuns`, `analytics` to a flag and a measurement id that agree with each other, both `features` to `false`, ~~`secondary_domain` to empty~~ (the key itself is refused now: the owner does not own a second domain — ruling of 2026-10-17, the amendment under W2.2), `allowed_hosts` to bare lowercase hosts, and `canonical_url` to a reserved placeholder origin on that list). Later tasks relax the pins they own, each in its own level and each named in the message it meets: W2.2 the canonical URL ~~and the secondary domain~~, W2.4 `features.video`, W3.1 `features.console_downloads`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Website|Docs|Lint' -count=1`

**Depends on:** W0.1 (same PR group; the boundary test's file walk assumes `website/data/` exists). **Commit:** `feat(website): the site's facts, the boundary test, and the console-link rule`

**Red step.** Write the four tests first, with `website/` absent. `Expected: FAIL` — `open website/data/site.yaml: no such file or directory`.

### Task W0.3: `fugaro stats runs` — the owner-run aggregation — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/stats/stats.go` (new), `internal/stats/stats_test.go` (new), `internal/cli/stats.go` (new), `internal/cli/stats_test.go` (new).

**Steps.**
1. In `internal/stats`, implement the statistics over `[]runstore.Record` (or over the narrower view the CLI passes in): **percentiles** (min, median, p90) of wall-clock duration and of `Cost.TotalUSD`, grouped by **recipe**, over a window; the **minimum-sample rule** (`MinRuns`, default 20) which suppresses a group rather than publishing it; and the count of runs whose compute cost is an **estimate** rather than a measurement (`Cost.ComputeEstimated`/`ComputeSource`), which the caller reports rather than folds in. No mean is published alone: the struct has no field for one. **A record whose recipe is not one the tree ships (`internal/recipe`) is dropped rather than grouped**, so the output never carries a group key no reader can look up.
2. In `internal/cli`, add `newStatsCmd()` with one subcommand, `runs`: `--project` (**required, with no default** — the command refuses to run until exactly one installation is named, the sandbox included, and there is **no `--all`**), `--since` (default 90d, the runs lifecycle's own age), `--min-runs`, `--json`. It reads the run records **read-only** through the existing store client, and it **refuses to run when it detects a coding-agent session** (the marker the repository's other owner-only paths use). **D96's answer fixes what the plan may read from it: the site's figures come from the sandbox/dogfood installation — the `fugaro` installation — only, `belong` is never aggregated unless the owner says so, and the minimum is 20 runs**; the tests and every red step use synthetic names (Global Constraint 10).
3. Make the output shape the security property: group keys are **recipe names and the window**, every value is a number or a count, and there is no field for a repository slug, run ID, branch, PR URL, task text, person or commit. Print, as the command's own closing line, D97's disclaimer: what a cap is (`observe` counts, `enforce` halts), that these are percentiles over a window and not a promise, and the sample size behind every figure.
4. Write the tests: a **golden `--json` test** asserting the exact key set, and an **identifier test** asserting that no string value in the output matches a repository-slug shape, a run-ID shape, a branch name or a person — over a fixture whose records deliberately contain all of them, so the test fails if a future field leaks one. Plus a test that `--min-runs` suppresses a group below the threshold, and one that the command's help names no other installation.
5. Wire the subcommand into `NewRootCmd`, run the red step, then the full suite and `go vet`.

**Tests:** `TestStatsRunsJSONCarriesNoIdentifiers` (**the load-bearing one**), `TestStatsRunsOutputShapeIsExactlyTheAggregates`, `TestStatsRunsSuppressesSmallSamples`, `TestStatsRunsRequiresOneNamedProject`, `TestStatsRunsHelpNamesNoOtherInstallation`.

**Command:** `go test ./internal/stats ./internal/cli -run 'Stats|Website|Docs|Lint' -count=1 && go vet ./...`

**Depends on:** nothing (its own lane; it is the one task in Part 0 that no other task waits on). **Commit:** `feat(stats): fugaro stats runs — an owner-run, read-only aggregation that emits no identifiers`

**Red step.** Write the identifier test and the shape test first, against a stub that returns a group keyed by repository slug. `Expected: FAIL` — `stats output carries identifier "acme/example" in group key`.

### Task W0.4: The CLI reference, generated from the command tree

**Files:** `internal/clidoc/cliref.go` (new), `internal/clidoc/cliref_test.go` (new), `website/content/cli-reference.md` (generated), `internal/cli/docs_website_cliref_test.go` (new).

**Steps.**
1. Write the generator: walk `internal/cli`'s `NewRootCmd()` and emit one Markdown section per command (path, short description, long description, usage line, flags with their defaults and required-ness, and the subcommand list), **skipping hidden commands** (F38) and skipping the internal commands that carry the forbidden marker — **the hidden commands are `history`, `image git-credential`, `image selftest`, `image record`, `image gate` and `exec`** (`Hidden: true` in `internal/cli/history.go`, `gitcred.go` and `image.go` — where the hidden command is the **subcommand** `image selftest`, not `image` itself, which is documented and visible — and `image_record.go`, whose `image record` and `image gate` are two separate commands, and `exec.go`; **there is no top-level `git-credential` command and no `token` command in the tree**) — with the skip list **derived from the tree** (`Hidden` read off each command, subcommands included) rather than hand-written where the tree already records it.
2. Write the file with a header naming its source and the command that regenerates it: `<!-- generated by go run ./internal/clidoc; do not edit -->`.
3. Write the two tests: **every** non-hidden command and flag reachable from `NewRootCmd()` appears in the file (the shape `TestDocsDesktopConsoleContractNamesEveryWatchField` uses), and the file names **no** hidden command and **no** flag that does not exist (the shape `TestDocsDesktopConsoleContractNamesNoInventedField` uses). A third test runs the generator into a buffer and asserts it equals the committed file, so a struct change cannot land without the reference changing.
4. Run the red step, then the suite.

**Tests:** `TestCliReferenceNamesEveryCommandAndFlag`, `TestCliReferenceInventsNothing`, `TestCliReferenceIsCurrent`.

**Command:** `go test ./internal/clidoc ./internal/cli -run 'CliRef|Website|Docs|Lint' -count=1`

**Depends on:** nothing (it generates into `website/content/` and `internal/clidoc/`, which no other task writes; the boundary test W0.2 adds is a rule over the tree, not a gate in front of the generator). **Commit:** `feat(clidoc): generate the CLI reference from the command tree, pinned by a test`

**Red step.** Write the two naming tests first, with the generator returning an empty document. `Expected: FAIL` — `cli-reference.md does not name command "fugaro budget set"`.

### Task W0.5: The security page's source, generated from `SECURITY.md`

**Files:** `website/content/security.md` (generated), `internal/cli/docs_website_security_test.go` (new). The generator is a small Go program under `internal/clidoc` (`secdoc.go`) beside W0.4's.

**Steps.**
1. Generate the page from `SECURITY.md`: the title from the document's own `#` heading, then the body **byte for byte**, with the headings unchanged (F40) and a generated-file header naming the source and the regeneration command.
2. Write the tests: the page's body equals `SECURITY.md`'s body (a byte comparison, not a substring check); the page's heading set equals the file's heading set in order; and the anchors the repository's cross-references name are present (reusing the anchor extraction `docs_security_test.go` already does, so the two tests cannot disagree about what an anchor is).
3. Add the negative half: the page carries **no sentence that `SECURITY.md` does not carry** — a test that every paragraph of the page appears in the source file, which is the mechanical form of "never paraphrase a security claim" (F41, D99).
4. Run the red step, then the suite.

**Tests:** `TestWebsiteSecurityPageIsByteIdenticalToSecurityMd`, `TestWebsiteSecurityPageKeepsEveryHeading`, `TestWebsiteSecurityPageAddsNoSentenceOfItsOwn`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'SecurityMd|Website|Docs|Lint' -count=1`

**Depends on:** nothing (the generated page stands alone; W1.8 renders it). **Commit:** `feat(website): render SECURITY.md, byte for byte, with a test that refuses a paraphrase`

**Red step.** Write the byte-identity test first, with the page absent. `Expected: FAIL` — `open website/content/security.md: no such file or directory`.

### Task W0.6: The changelog's generator, run at site build time from `docs/releases/`

**Files:** `website/src/lib/changelog.mjs` (new), `website/test/changelog.mjs` (new). **Nothing generated is committed**, and no Go test is added: the release train is not touched (Global Constraint 13, D109).

**Steps.**
1. Write the generator as a standard-library Node module (`node:fs`, `node:path`, `node:child_process`; no npm dependency, so it runs before W1.1's toolchain exists) that the site's build calls: it reads `docs/releases/vX.Y.Z.md` and returns an array of `{tag, version, released, highlights: [lines], for_operators: [lines]}` — the **tag and the version from the filename**, the bullets and the optional `### For operators` block as written, and nothing invented. W1.9's page calls it during `npm run build`; **its output is never written into the tree**, so there is no committed copy to go stale when a Highlights file lands on `main` before `scripts/release.sh` runs, and nothing in `internal/cli` — which the CI `test` job and therefore the release gate run — reads it. **There is no `date` field, and that is the design rather than an omission.** The release files carry neither a title nor a date (`docs/releases/README.md`: "Plain Markdown, no title", and no date appears in any of the eleven files), the site's build is network-free (W1.1) so it cannot ask the releases API, and a date taken from a git tag does not exist for the newest entry — the Highlights file is committed to `main` **before** `scripts/release.sh` runs. So the page prints the tag and links the entry to its GitHub release, where the date is GitHub's.
2. **`released` is the one field that reads the checkout's tags, and no test pins it.** It is `true` when `git tag --list vX.Y.Z` finds the tag in the checkout the build ran in and `false` otherwise; a **shallow checkout has no tags at all**, so every entry there is `false`, which is the page's **"not yet released"** state — the honest rendering for a file that is on `main` before its tag. **No test reads a tag and no test reads a commit date.**
3. Write `website/test/changelog.mjs` (`node:test`), the build-time check, none of whose cases reads a tag: the generator runs without throwing over the tree's `docs/releases/`; the set of entries equals the set of `v*.md` files there (so every release file is covered, and no entry names a file that is gone); the entries are ordered newest first **by version parsed from the filename**; every `highlights` and `for_operators` line appears in its file and no entry carries a sentence its file does not (the same no-invention rule as W0.5); and `released` is a boolean, checked for shape only. **`docs/releases/README.md` is not a release** and is skipped by name, not by pattern. The file runs under `node --test` today and under `npm run check` once W1.1 adds it, which runs every `website/test/*.mjs`.
4. Add the case the site's page will lean on: `README.md` still carries no changelog section, so the page remains the only index of those files (F44) — and if that ever changes, this case is where the duplicate is noticed.
5. Run the red step, then the test and the docs suite.

**Amended 2026-10-18 (release-blocking fix on the 0.8.5 path) — a release file's body has TWO optional section headings, and the entry carries them as one list.** `docs/releases/v0.8.5.md` ships a `### Known limits` section, which the owner's release rule asks for, and step 1's shape knew one heading, so the second section's heading line leaked into the page as text and W1.9's gate refused it (`the page carries a sentence no release file does: "## Known limits"`, and the same with `###`). The generator now recognises `### For operators` and `### Known limits` — the two `###` headings `docs/releases/README.md` names, each optional, either without the other, in either order — and returns `{tag, version, released, highlights, sections}`: `highlights` is the lines before the first heading, and `sections` is an **ordered** list of `{title, lines}` in the file's own order, so the entry keeps whatever order the file writes. The component renders each section as an `<h3>` with its lines under it through the same `inline()` path the operator steps used. `for_operators` is gone with it (no other consumer). W0.6's test file changed in three places. `changelog builds from docs/releases` holds the entry's fields and each section's shape as well as the shape it held before; `changelog adds no sentence of its own` still compares the generator against the real `docs/releases/` corpus, with the split it expects rebuilt from both headings; and the file **gains** `the generator splits a file at both section headings, in the file's own order`, the one case that writes its own temp `docs/releases/` — every shape the format allows, both sections, each one alone, neither, and one that writes the two in the reverse order, which is what makes "the entry carries the file's order" an assertion rather than a property of the one file on `main` that has both. W1.9's page test grew the same way: its "renders every line" case compares one entry against one release file element by element (a dropped line, a reordered section and a leaked `###` each fail in the element that differs). The file's heading is `###` and not `##`: GoReleaser puts the file under its own `## Highlights`, so a `##` in the body is a second top-level heading beside it.

**Tests:** `website/test/changelog.mjs` — `changelog builds from docs/releases`, `changelog covers every release file`, `changelog lists no release that is not a file`, `changelog adds no sentence of its own`, `the generator splits a file at both section headings, in the file's own order`, `README carries no changelog section`.

**Amended 2026-10-19 (the owner's changelog feedback) — `released` reads the plugin's own version, and a test pins it.** Step 2's rule is replaced: `released` is `true` when the release file's version is at or below the version in `plugin/.claude-plugin/plugin.json`, and `false` otherwise. The tag rule was reported from the deployed site — *"Changelog: why do I see not yet released?"* — because the deploy build checks out without tags and so printed the marker under every release. The manifest's version is a file this repository carries and every checkout has, its bump lands on `main` before the tag, and the highest version below it is a release file that exists, so the rule has an answer everywhere. The module imports `node:fs` and `node:path` only — `node:child_process` is gone with the tag rule — and `buildChangelog({releasesDir, pluginFile})` takes both paths so a fixture can state its own. W0.6's test file gains `a release is published when its version is at or below the plugin's, whatever the checkout carries` (fixture directories that are not git repositories at all), `the plugin's own version names a release file, and that release is published`, and `a plugin manifest the changelog cannot read stops the build, naming the file` (a manifest that is not JSON, and a version that is not `x.y.z`).

**Command:** `node --test website/test/changelog.mjs && GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** nothing. **Commit:** `feat(website): build the changelog from docs/releases at site build time, with a check that every release file is covered`

**Red step.** Write the coverage case first, with the generator returning an empty array. `Expected: FAIL` — `changelog lists 0 releases, docs/releases/ holds 11`.

## 9. Part 1 — The site

### Task W1.1: The site's shell, toolchain and build

**Files:** `website/package.json` (with its `build` and `check` scripts), `website/package-lock.json`, `website/astro.config.mjs`, `website/tsconfig.json`, `website/README.md`, `website/.gitignore`, `website/test/readme.mjs` (new).

**Steps.**
1. Scaffold the Astro app with **`output: 'static'`** and **no integrations beyond what an island needs** (the spec's "static-first; React islands only where interactivity needs it" read as a constraint — F56). Pin the Node version in `package.json`'s `engines` and in the README, and commit the lockfile.
2. Make the build **network-free after install**: no Google Fonts, no remote images, no integration that fetches at build time, and fonts self-hosted under `website/public/fonts/` (D106, Global Constraint 8). The README says which command installs and which builds, and that the build must succeed with the network off.
3. Write the site's own README: what is generated and must not be edited (`content/*`), the regeneration commands from Part 0, the check commands, and the rule that a page renders a fact rather than restating it.
4. Add the **`check` script** to `package.json`: it builds the site and then runs **every `website/test/*.mjs`** under `node --test`, so W0.6's `changelog.mjs` is picked up at once and each later task's test file is picked up without touching this task's files.
5. Verify the build produces the pages and that `npm ci && npm run build` is reproducible from a clean checkout, then commit.

**Tests:** the build itself (it fails on a missing generated file, which is W1.3's pipeline), plus `website/test/readme.mjs`, asserting the regeneration commands the README names are the ones Part 0 added.

**Command:** `cd website && npm ci && npm run check` (the site's own gate; the Go gates stay `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`)

**Depends on:** W0.2, W0.4, W0.5 (the build reads `data/` and `content/`; the changelog is built at build time by W0.6's module, which W1.9 calls). **Commit:** `feat(website): the Astro shell, a pinned toolchain, and a build that needs no network`

**Red step.** Build with `content/` absent and with the content pipeline not yet written. `Expected: FAIL` — the page renders an empty install block instead of failing (the defect W1.3 exists to remove), and the README names no regeneration command.

### Task W1.2: Tokens and three palettes

**Files:** `website/src/styles/tokens.css`, `palette-violet.css`, `palette-orange.css`, `palette-split.css`, `website/test/a11y.mjs` (new; W1.11 extends it in L5).

**Steps.**
1. Write `tokens.css` as the semantic layer (`--ground`, `--surface`, `--text`, `--text-muted`, `--accent`, `--accent-on-dark`, `--border`, `--warn`, plus spacing, radius and type scale) with **no colour literal in it**: every colour comes from the active palette file.
2. Write the three palettes of D104 as the three candidate files, each meeting WCAG AA against the dark ground, each with the hero's gradient stops named as tokens so the contrast test can check text over them (U5).
3. Write the contrast check into `website/test/a11y.mjs` (this task creates the file with its first case; W1.11 extends it in L5): every text/background pair in the token set meets AA, and the gradient stops are pairs too.
4. The palette in force is selected by **one import in `tokens.css`** (not in `site.yaml`, which this task does not write), so changing the owner's mind is a one-line change and nothing else. **D104's answer of 2026-10-16 is palette C — violet for structure and links, orange for warnings and cost — so `palette-split.css` is the one imported, and the other two ship as the documented alternatives.**

**Tests:** in `website/test/a11y.mjs`: the contrast case over every token pair and every palette, and a case that no palette file is missing a token the others define (so a new token cannot be added to one palette only).

**Command:** `cd website && npm run check`

**Depends on:** W1.1. **Commit:** `feat(website): design tokens and three palettes, each passing contrast in a test`

**Red step.** Run the contrast check against a deliberately failing pair. `Expected: FAIL` — `--text-muted on --surface is 3.1:1, needs 4.5:1`.

### Task W1.3: The layout and the generated-content pipeline

**Files:** `website/src/layouts/Base.astro`, `website/src/content.ts`, `website/test/layout.mjs` (new).

**Steps.**
1. Write the base layout: `<head>` with the canonical URL **from `site.yaml`** (never hard-coded), the self-hosted font preload, the sitemap and OpenGraph tags, and the analytics script **only when `site.yaml`'s `analytics.enabled` is true**, **only from an allowed origin**, and **only after the visitor has consented** (D93, D106) — the loader is written so that the consent gate, not the flag alone, decides whether anything is fetched, and W1.12 owns that gate and its tests.
2. Write the content pipeline: a single module that loads `content/*` and **throws** when a file is missing or its generated-file header is absent — so a build cannot silently render an empty install block or an ungenerated security page.
3. Add the reduced-motion base: a global `@media (prefers-reduced-motion: reduce)` rule that neutralises the animation utility classes the components use, written here rather than in each component, so a new component cannot forget it (D105).
4. Assert the pipeline's failure mode in the build (a missing `content/` file fails the build with the file's name in the message).

**Tests:** in `website/test/layout.mjs`: the build fails when a `content/` file is removed (a test that deletes it in a temp copy); a test that the canonical URL appears only as a `site.yaml` read; a test that the analytics tag is absent while the flag is false.

**Command:** `cd website && npm run check`

**Depends on:** W1.1. **Commit:** `feat(website): the base layout, a content pipeline that fails loudly, and the reduced-motion base`

**Red step.** Remove `website/content/security.md` and build. `Expected: FAIL` — the build succeeds and renders an empty security page (the defect this task removes).

### Task W1.4: The hero and the architecture diagram

**Files:** `website/src/components/Hero.astro`, `Terminal.astro`, `ArchitectureDiagram.astro`, `website/src/pages/index.astro`, `how-it-works.astro`, `website/test/home.mjs` (new).

**Steps.**
1. The hero: the **tagline exactly as `README.md` writes it** (F48 — a test asserts the string is byte-identical to the line under `README.md`'s `# Fugaro` title, which is the README's own tagline line, so the site and the README cannot drift), one sentence of what Fugaro is, the two install commands rendered **from `install.yaml`** (never typed into the component), and the video slot (W2.4) empty.
2. The animated terminal: a typing animation of a real `fugaro` invocation **taken from the CLI reference** (W0.4's file), in CSS/SVG or a small first-party island, with a byte budget (W1.11 enforces it), and a reduced-motion path that renders the finished frame.
3. The architecture diagram: **the spec's five stage names** — local dispatch, cloud job, cache, security boundary, PR or artifact collected back (`spec.md` §2) — as an SVG, with each stage's one-sentence caption **taken from the repository rather than written here**: `README.md`'s `## How it works` five steps ("Start", "Sync", "Loop", "Push and open the pull request", "Write caches back and exit") and its ASCII diagram are the sources, and the two names the README does not use ("security boundary", "PR or artifact collected back") are the page's map to those steps rather than quotations. Animated on scroll, with a reduced-motion path.
4. Nothing on either page names an unshipped feature (F50): a test greps the built HTML for the four held-back claims (console download, WSL support, "~2 h", "ballpark ranges") and fails.

**Tests:** in `website/test/home.mjs`: tagline byte-identity against the tagline line under `README.md`'s `# Fugaro` title; the diagram carries the spec's five names and no sixth; every caption on the diagram appears in `README.md`'s `## How it works` or in `docs/design/v1.md` (the two names the README does not use are asserted to be labels only, with no claim attributed to the README); the install commands in the hero equal `install.yaml`'s; the built HTML names none of the four held-back claims; the terminal's animation has a reduced-motion end state.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the hero, the terminal and the architecture diagram, from the repository's own words`

**Red step.** Write the tagline test against a paraphrased tagline. `Expected: FAIL` — `hero tagline differs from the tagline line under README.md's "# Fugaro" title`.

### Task W1.5: Getting started, and the caps-and-costs guide

**Files:** `website/src/pages/getting-started.astro`, `caps-and-costs.astro`, `website/src/components/CapsCosts.astro`, `website/test/caps-and-costs.mjs` (new), `website/content/costs.json` (published, from W0.3).

**Steps.**
1. Getting started renders `install.yaml` per platform, with the **plugin path** as a first-class step (F22), the cosign verification inline, and the platform list showing **macOS and Linux supported, Windows untested** with the WSL note D94 settles — the wording pinned by W0.1's test.
2. The caps guide explains what the repository actually has: the project-global caps, the default repo caps, the per-repo caps, the per-run cap and max-reserve, the `observe`/`enforce` modes, the kill switches — each with the `fugaro budget` command that sets it (from W0.4's reference, so the flags cannot drift) — and what a cap *is* (a ceiling; `enforce` halts).
3. The costs section renders `content/costs.json` (W0.3's output): percentiles per recipe, each figure with its window and sample size, groups below `min_runs` absent rather than approximated, and D97's **"not a promise"** line present as a rendered element (a test asserts it).
4. The page carries **no figure the JSON does not carry** — a test that every number in the built page appears in `costs.json`, which is the mechanical form of "publish the measurement, not the anecdote" and the reason the "~2 h" sentence never appears here.

**Tests:** in `website/test/caps-and-costs.mjs`: every number on the page appears in `costs.json`; the "not a promise" line is present; a group below `min_runs` is absent from the page; the `fugaro budget` invocations match the CLI reference.

**Command:** `cd website && npm run check`

**Depends on:** W1.3, W0.3 (for `costs.json`; the page renders a "not enough runs yet" state while the file is absent, which is the honest state on day one). **Commit:** `feat(website): getting started, and a caps-and-costs guide that only prints measurements`

**Red step.** Write the "every number appears in costs.json" test with a hard-coded "~2 h" in the page. `Expected: FAIL` — `page states "~2 h", which costs.json does not carry`.

### Task W1.6: Console downloads, as a placeholder with no link

**Files:** `website/src/pages/console.astro`, `website/src/components/Downloads.astro`, `website/content/downloads.json` (new, `available: false`), `website/test/console.mjs` (new).

**Steps.**
1. Render the section from `downloads.json`: while `available` is false, the page says what the console is (from `docs/design/desktop-console.md` D2/D4/D11 in the site's own words, since that document is internal), that it is **not released yet**, and what it will be — and renders **no link and no button**.
2. Wire the rule W0.2's test enforces: no `releases/download/` URL anywhere under `website/` while the flag is off, and when it is on, every URL must point at a release asset of a tag that exists (the test W3.1 extends).
3. State the console's own guarantees that are decided and user-facing: it is **strictly read-only** (D2), it has no telemetry (F17), and its artifact publishes from the CLI's own tag (D11) — each sentence traceable to a decision, with the site not inventing a distribution.

**Tests:** in `website/test/console.mjs`: no download URL in the built HTML while the flag is false; no button or link element in the downloads section; the page names the console's read-only and no-telemetry properties.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the console section, as a placeholder that renders no link`

**Red step.** Write the "no download URL" test with a hard-coded placeholder link. `Expected: FAIL` — `console page links releases/download/ while features.console_downloads is false`.

### Task W1.7: The docs hub

**Files:** `website/src/pages/docs.astro`, `website/test/docs-hub.mjs` (new).

**Steps.**
1. Build the hub: `README.md`'s `## Getting started` (the quick start), `docs/gcp-setup.md`, `docs/recipes.md`, `docs/project-layer.md`, `docs/multi-model.md`, `docs/git-providers.md`, and the generated CLI reference — each entry one line saying what the document answers, linking to it on GitHub **by URL built from `site.yaml`'s repository**, never by a copied path that can rot.
2. Link, never copy (F37, D98): a test asserts the page contains **no paragraph of more than one sentence that appears verbatim in any document under `docs/`** — the mechanical form of "this is a hub".
3. The CLI reference is rendered from `content/cli-reference.md` (W0.4), so a new command appears here the day it lands.

**Tests:** in `website/test/docs-hub.mjs`: every link target exists in the repository (the file-level half of W1.11's link check, run against the GitHub URLs); no copied prose; the CLI reference's command count equals the generated file's.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the docs hub, linking the guides and rendering the generated CLI reference`

**Red step.** Write the no-copied-prose test with a pasted paragraph from `docs/recipes.md`. `Expected: FAIL` — `docs page repeats a sentence from docs/recipes.md`.

### Task W1.8: The security page

**Files:** `website/src/pages/security.astro`, `website/test/security.mjs` (new).

**Steps.**
1. Render `content/security.md` (W0.5) inside the site's typography, with the document's headings as anchors and a table of contents generated from them — and with **no editing**: the page adds a lead sentence and nothing else, and W0.5's no-invention test covers the file rather than the page, so the page's lead sentence is asserted to be a link line rather than a claim.
2. Keep every anchor the repository's cross-references name (F40), asserted by the same anchor rule as W0.5's test, applied in this task's own test file to the **built page** as well as the file — the anchors survive the renderer or the build fails.
3. Add the reporting pointer: how to report a vulnerability, taken from the document's own `## Reporting a vulnerability` section, with no paraphrase.

**Tests:** in `website/test/security.mjs`: the built page's heading set equals `SECURITY.md`'s; every anchor the repository references exists in the built HTML; the page's non-document text is the lead line only.

**Command:** `cd website && npm run check`

**Depends on:** W1.3, W0.5. **Commit:** `feat(website): the security page, rendered from SECURITY.md and nothing else`

**Red step.** Write the heading-set test against a page whose headings were "improved". `Expected: FAIL` — `built page heading "What we protect" is not in SECURITY.md`.

### Task W1.9: The changelog page

**Files:** `website/src/pages/changelog.astro`, `website/src/components/Changelog.astro`, `website/test/changelog-page.mjs` (new).

**Steps.**
1. Render the entries W0.6's `website/src/lib/changelog.mjs` builds at build time: one entry per release, newest first, with the tag, the bullets and the `### For operators` block where the file has one — **no date** (the file carries none and the repository holds none; the entry links to its GitHub release, which is where the date is stated), and an entry whose `released` is false rendered as **not yet released** rather than as a release that exists.
2. Render **nothing of its own**: no summary, no "what's new" prose, no editorial. A test asserts every sentence on the page appears in `docs/releases/` (the page-level form of W0.6's file-level test).
3. The entry count equals the generator's, and the newest entry's tag equals the newest file's — the visible half of the drift test.

**Tests:** in `website/test/changelog-page.mjs`: the page's release set equals `docs/releases/`'s; every sentence on the page is in a release file; the newest entry is the newest file.

**Command:** `cd website && npm run check`

**Depends on:** W1.3, W0.6. **Commit:** `feat(website): the changelog page, rendered from the release notes`

**Red step.** Write the set-equality test with the newest release missing from the page. `Expected: FAIL` — `page omits v0.8.0, docs/releases/ holds it`.

**Amended 2026-10-19 (the owner's changelog feedback) — each release opens with a product summary, and the release file's Highlights are collapsed below it.** Step 2's "render nothing of its own" stands and step 1's entry rendering changes: the summary under a release is no longer the release file's own first Highlights bullet but the release's line in a new data file, `website/data/release-summaries.yaml` (one `vX.Y.Z: one line` per file under `docs/releases/`, written for a reader who uses Fugaro), read and validated by `src/content.ts`'s `loadReleaseSummaries` and rendered by the component, with the file's own Highlights, sections and all, inside a `<details>` block labelled `Highlights`. `docs/releases/` is not edited: the file stays the operator's document. A release file with no entry in the data file stops the build, naming the file. The page's test gains one case per property that matters — every release file has an entry and none is empty, the summary the page renders for a tag is that entry's text, no entry outlives its release file, the words are the site's (no internal id and no repository path), and every release file's lines are still rendered, in the file's order — and the "not yet released" case becomes version-driven: the copy's manifest carries the second-newest release file's version, so exactly the newest file is marked in a checkout with no `.git` at all. The "newest releases" summary block the redesign added above the list is removed: the gist role it filled is now every entry's own summary.

### Task W1.10: The FAQ and the community page

**Files:** `website/src/pages/faq.astro`, `community.astro`, `website/data/faq.yaml`, `website/test/faq-community.mjs` (new).

**Steps.**
1. The FAQ answers what the repository can prove, from the repository: what Fugaro is and is not (a fleet of ephemeral workers, not an IDE agent), where the code runs (the operator's own GCP project, one Cloud Run job execution per task), what a run costs and what stops it (the caps, with the guide's link), what happens to a failed run (a draft pull request; Fugaro never merges), what Fugaro can never change (the workflow files, with the reason), and what the security model does not promise (a link to the security page, **not** a summary).
2. **Every comparison row carries a source and a date or it is not written** (D103, F51–F52). The page's comparison section is rendered from a data file (`website/data/faq.yaml`) whose comparison entries **require** a `source:` and a `date:` field, enforced by a test — so a comparison cannot be added without provenance, and the section renders empty until the owner supplies rows.
3. **D103's answer adds the owner to the loop: the FAQ, comparison section included, is owner-reviewed before it publishes** — so the task records the page as pending that review rather than publishing it as final. **Status: approved by the owner 2026-10-17** — the pending-review notice and the `review_pending` flag are removed, and the test pins their absence.
4. The community page: the repository, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, the issue templates, the licence stated inline (**MIT**), and the CI description either accurate (`test`, `rules` and `terraform` on pushes to `main` and on demand; no CI on pull requests) or absent (F46, D101).
5. Nothing on either page restates the contributing guide's rules (F47): the same no-copied-prose rule as W1.7's, written in this task's own test file and run over `CONTRIBUTING.md` and `SECURITY.md` as well.

**Tests:** in `website/test/faq-community.mjs`: every comparison entry has a source and a date, and the section is empty without them; the CI description matches `ci.yml`'s `on:` block; no copied prose from `CONTRIBUTING.md`; the licence is named.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the FAQ with provenance, and a community page that links rather than restates`

**Red step.** Write the provenance test with a comparison row carrying no source. `Expected: FAIL` — `comparison row "Cursor" carries no source`.

### Task W1.11: The gates — links, anchors, accessibility, origins, budget

**Files:** `website/test/linkcheck.mjs`, `website/test/a11y.mjs` (W1.2 created it with its first case), `website/test/origins.mjs`, `website/test/budget.mjs` (new). `package.json` is not touched: W1.1's `check` script already runs every `website/test/*.mjs`.

**Steps.**
1. **Link and anchor check** over the built HTML: every internal link resolves to a built page or a `docs/` file that exists in the repository, every `#anchor` resolves to an `id` in the target, and every external link's host is on `site.yaml`'s `allowed_hosts` (F59).
2. **Origins check** over the built HTML: the `script`, `link[rel=stylesheet]`, `link[rel=preload]`, `img`, `iframe` and `font` origins are all first-party or on the allowed list — the mechanical form of "nothing phones home" (F55, D106, Global Constraint 8). A Google Fonts URL, a CDN icon set or an embedded player fails here. **The list has exactly one third-party entry, and it is the one D93 chose: Google's Analytics origin, admitted only for the script W1.12 loads behind the consent gate** — so the check must not merely tolerate that host: any *other* third-party origin fails, and W1.12 asserts the admitted one is never loaded before consent.
3. **Accessibility check**: the contrast pairs (W1.2), a `prefers-reduced-motion` path for every animated component (asserted by checking that each animation class is covered by the global reduce rule), every interactive element keyboard-reachable and focus-visible, a landmark structure and one `h1` per page, and alt text on every image.
4. **Budget check**: the JavaScript shipped per page is under the stated kilobyte budget, and the total page weight is under its budget — the numbers written into `website/README.md` so the budget is a fact rather than an aspiration.
5. The four run under `npm run check` with no wiring — W1.1's script runs **every `website/test/*.mjs`**, so a check a later task adds (W2.4's video slot, W3.2's analytics origin) is picked up the same way — and note in the README that **Lighthouse is the owner-run check** (D105's targets) because there is no CI on pull requests and the repository's tests are hermetic (F3, F58).

**Tests:** the four checks themselves, each with a deliberate failing fixture asserted in the same run (a broken anchor, a `fonts.googleapis.com` link, a `motion-safe`-only animation, an over-budget island).

**Command:** `cd website && npm run check`

**Depends on:** W1.2, W1.4–W1.10. **Commit:** `feat(website): the gates — links, anchors, origins, accessibility and the byte budget`

**Red step.** Run the origin check against a page carrying a Google Fonts link. `Expected: FAIL` — `fonts.googleapis.com is not on the allowed-host list`.

### Task W1.12: The consent banner, and the gate that keeps analytics behind it

**Files:** `website/src/components/ConsentBanner.astro` (new), `website/src/pages/privacy.astro` (new), `website/src/layouts/Base.astro` (extended), `website/test/consent.mjs` (new), `website/test/origins.mjs` (extended — W1.11 wrote it in L5).

**Steps.**
1. Write the banner: consent and refusal are **equal choices** (no pre-ticked box and no "accept" that is easier to reach than "refuse"), it states in one sentence what is measured, it links the privacy page, and it is **accessible and keyboard-operable** — an accessible name, focus-visible controls, nothing reachable only by pointer, no colour-only signalling, and no focus trap.
2. Write `website/src/pages/privacy.astro`: what the site measures, that **analytics load only after consent**, how consent is given and withdrawn, and what the tool is — **Google Analytics, a third-party script that sets identifiers** — in other words the privacy statement F16 records the repository has never had, written from D93's answer rather than inherited.
3. Rewrite the analytics loader in `Base.astro` so that **nothing is fetched before consent**: while consent is absent or refused the script tag is **not emitted at all**, `analytics.enabled` decides only whether the banner is offered, and the stored choice is **withdrawable** — withdrawing it takes effect on the next page load and the script is never fetched again until consent is given again.
4. Extend `website/test/origins.mjs` so that the allowed list admits **exactly one third-party origin — Google's Analytics origin, and only for the script loaded behind the gate** (any second third party still fails; this is the D106 exception and it is one entry wide).
5. Add `website/test/consent.mjs` with the four cases D93's answer requires: **no analytics request before consent**, **consent is withdrawable**, **no cookie before consent**, and **the banner is accessible and keyboard-operable**. Because the load is gated rather than merely switched off, W3.2 can turn the flag on later without touching the gate — and this task waits on no decision any more, D93 having been answered on 2026-10-16.

**Tests:** in `website/test/consent.mjs`: the built page makes no request to the analytics origin before consent and sets no cookie; a refusal loads nothing; withdrawing consent stops future loads; the banner's controls are keyboard-reachable and named. In `website/test/origins.mjs`: one admitted third-party origin and no second one.

**Command:** `cd website && npm run check`

**Depends on:** W1.3 (the layout) and W1.11 (the origins check it extends). **Commit:** `feat(website): the consent banner, and an analytics gate that loads nothing before consent`

**Red step.** Write the "no request before consent" case against a page that emits the analytics tag unconditionally. `Expected: FAIL` — `page requests googletagmanager.com before consent`.

**Amended 2026-10-18 (owner ruling) — withdrawing consent also DELETES the cookies Google's tag set, so step 3's withdrawal and step 5's "no cookie" case each gain a second half.** The owner ruled on 2026-10-18, in the brief this change was run from, that stopping the sends is not the whole of a withdrawal: `gtag.js` left first-party cookies on the site's **own** origin (`_ga`, `_ga_<container id>`, `_gid`, `_gat` and the `_gat_*` names), and a visitor who had withdrawn kept being identified by them. What the ruling adds, and what it leaves alone:

- **Step 3 stands, and gains the deletion.** Declining after accepting — on `/privacy/`, where `Base.astro` is asked for `alwaysOffer` so the choice is always available again — deletes every cookie GA4 sets for this site, in the site's one first-party script that D183 admits, **after** Google's documented opt-out flag (`ga-disable-<id>`) is set and consent is updated to denied: `_ga`, `_ga_<container id>` (the name **derived** from the one measurement id the banner carries — `_ga_` plus the id without its `G-` — never written down), `_gid`, `_gat`, and each `_gat_*` name the browser is actually holding (matched as `_gat` exactly or `_gat_…`, so a `_gate` of the site's own is left alone). Each is deleted on the page's host with `Path=/` and an expiry in the past, and — **only while a copy is still visible** — on the parent domain the tag may have used (the host without its leftmost label; the production host `fugaro.dev` is an apex, so no parent attempt is ever made there). Nothing below the parent is tried: no wildcard, and no suffix guessed from a public-suffix list this file does not carry (a preview host such as `foo--bar.web.app` therefore yields the public suffix `web.app`, whose write the browser rejects and nothing else — the comment in the file says so rather than overstating the rule).
- **Step 5's case is now "no cookie the site set, before consent or after it".** `website/test/consent.mjs`'s stub `document.cookie` became a **jar keyed on name, domain and path** that models what a browser does with a `Set-Cookie` string — replacement, removal by an expiry in the past, the **default path** (the request's own directory) when the write names none — and the new cases run the shipped file against planted GA cookies: after a withdrawal none of the listed names remain and the site's own cookies survive, the derived `_ga_<id>` name follows the id constant the banner carries, the parent domain is reached where the host is not enough, every write carries `Path=/` beside the expiry (a delete that omitted the path would land on `/privacy` and remove nothing), nothing is read or written before consent was ever given, and a second withdrawal is idempotent. `website/test/copy.mjs`'s ban on `document.cookie` became the narrower rule it should have been: the file may touch cookies, and only to delete them — every write carrying the expired marker, and `Path=/`.
- **Step 2's page changed with it, inside D188's ceiling.** "Google's cookies stay until you clear them" became "deletes Google's cookies (`_ga` and the rest) from this browser", paid for by trading out the clause that pointed at Google's own policy beyond IP anonymisation; the flag-on page still fits the 250-word ceiling exactly, and `consent.mjs` holds the sentence to the script that makes it true.
- **Nothing else moved.** No new origin, no CSP change, no `'unsafe-inline'` and no nonce, no second script file (D183), and the flag still decides only whether the banner and its configuration are emitted. The byte budgets are re-derived in the commit that ships the script (D192), and no figure is frozen into this plan: **`website/README.md`'s budget list is where the three numbers live, and `website/test/budget.mjs` is the authority for them** — its header carries the derivation (that build plus 25 %) and its `derivationFailures` case reads the README's numbers back and refuses a figure that is not three times the tight derivation of a build the artifact can be measured against — so the commit that moves the script re-derives them there, and a number quoted anywhere else is stale by the next script change.
- **The runtime half is the owner's**, and Check 69b gained a step for it in the same pass: whether an expired write with `Path=/` really matches GA4's cookie **on the apex host** — where only the host-scope attempt runs — is a browser question no hermetic stub can answer. Until that step runs, the deletion is **UNVERIFIED** in a browser, and the build's jar is not the browser.

## 10. Part 2 — Hosting, deploy and the live checks

### Task W2.1: The deploy workflow, staged for the owner — SECURITY-SENSITIVE

**Files:** `docs/pending-workflows/website.yml` (new), `docs/pending-workflows/README.md` (updated), `firebase.json` (new).

**Steps.**
1. Write the workflow **for the owner to copy into `.github/workflows/`**, following the procedure `docs/pending-workflows/README.md` describes (including the note that the staged file is removed in the same PR that copies it). It builds the site from `website/`, runs the site's own checks, and deploys to **Firebase Hosting — D91's answer — in the separate Firebase project the owner created for the website only**. Its own comment says so: **this deploy targets the website's project and never an installation's** (`docs/gcp-setup.md` `## Adoption`: *"one Firebase project serves one installation"*), and never a project a Fugaro run can reach. **The project id is named only through the workflow's variable** (`vars.FIREBASE_PROJECT_ID`, or a variable of that shape), which the owner sets at deploy time; **no Firebase project id is committed anywhere**, because the site's project id is not public and the tree therefore carries no installation's and no site's — a clone names neither.
2. Write `firebase.json` at the repository root as the hosting configuration the deploy step reads: the site's build directory (`website/dist`), clean URLs, the cache and security headers, ~~and — W2.2's addition — the redirect block~~ (struck: W2.2 adds no redirects, and cannot — the amendments under W2.2). It carries **no project id, no site id and no `.firebaserc`**: the project comes from the workflow's variable at deploy time, so nothing under version control points at any Firebase project at all.
3. The credential rule (F14): the deploy credential is a **GitHub Actions repository secret scoped to the website's own Firebase project** and to nothing else, named in the workflow and in the staged README, with a comment stating what it must never be — **an installation's service account, a credential a Fugaro run holds**, or a credential with write access to anything but the site's project. **No run may hold it**: the deploy is an owner-run step, and the workflow runs **no Fugaro run** and is invoked by no Fugaro command. **It is written for a public repository, which is what this one is** (the module, the images and the release archives are all published): it declares an explicit **least-privilege `permissions:` block** — `contents: read` plus the host's deploy scope and nothing else, never `workflows: write` — the deploy job runs **only on `main`**, and the workflow is never triggered by `pull_request_target`, so a pull request from a fork (which gets no secrets at all) cannot reach the credential and cannot run a deploy step.
4. The trigger is the site's own: a path filter on `website/**` on pushes to `main`, plus `workflow_dispatch` — **no release trigger and no tag**, which is D109's decoupling made mechanical.
5. Update `docs/pending-workflows/README.md` — its staged-file list gains the site's workflow, and **its stale "One file is pending: `images.yml`" line is corrected in the same edit** (that file was copied in and removed in `6b57ba9`; the directory holds only the README today, F62) — and the copy procedure, and verify that the workflow's YAML parses (`actionlint` if present, otherwise a YAML parse test) — the file cannot be run, so parsing is the only hermetic check available.
6. **Nothing under `.github/workflows/` is touched by this task** (Global Constraint 2). The copy is the owner's step, recorded in the staged README's checklist.

**Tests:** the staged YAML parses; a test asserts no `.github/workflows/` file was added or modified by the task (a diff-scoped check, as the sibling plans use); the workflow names no credential other than through `secrets.`; **and a test asserts the tree carries no Firebase project id and no `.firebaserc`** — the project is named only through the workflow's variable, so nothing committed points at any Firebase project (D91).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` plus the YAML parse check

**Depends on:** W1.11. **Commit:** `feat(website): stage the deploy workflow for the owner to copy, with no credential a run can hold`

**Red step.** Write the "no credential outside `secrets.`" test against a workflow carrying an inline token. `Expected: FAIL` — `website.yml carries a literal token`.

**Amended 2026-10-18 (owner ruling) — the deploy is TWO manual workflows, a preview and a promote, and the credential is federated rather than a secret.** The owner approved this shape on 2026-10-18, and it supersedes three of the steps above:

- **Step 4's trigger is struck.** There is no path filter on `website/**` and no push trigger anywhere: **both workflows are `workflow_dispatch` only**. One (`website-preview.yml`) builds a ref the owner names and deploys it to a Firebase Hosting **preview channel** of the site's project, printing the preview URL in the job summary — and **a preview is public**, which the owner accepted with the shape. The other (`website-promote.yml`) **copies an existing preview channel's version onto the live channel** and is the owner's alone: the job carries `environment: website-live` whose only required reviewer is the owner, plus a guard on `github.actor`. So the live site moves only when the owner promotes bytes they have already seen, which is D109's decoupling taken one step further than step 4 had it (nothing at all runs on a merge).
- **Step 3's credential is struck and replaced.** The deploy credential is **not** a GitHub Actions secret: both workflows authenticate to Google through **Workload Identity Federation** (`google-github-actions/auth`, pinned by SHA), exchanging the run's own OIDC token for a short-lived access token for a deployer service account that holds only Firebase Hosting roles on the site's project. **Never a key file**, and `secrets.` appears nowhere in either file: the three values they read (`FIREBASE_PROJECT_ID`, `GCP_WORKLOAD_IDENTITY_PROVIDER`, `GCP_DEPLOYER_SERVICE_ACCOUNT`) are repository **variables**, which are identifiers rather than credentials. The rest of step 3 stands unchanged: least privilege declared in `permissions:` (`contents: read` and `id-token: write`, nothing else), no `pull_request_target`, no `.firebaserc`, the project named only through the variable, and neither workflow runs a Fugaro command or is invoked by one.
- **Step 1's staged name is struck.** The staged files are **`docs/pending-workflows/website-preview.yml`** and **`docs/pending-workflows/website-promote.yml`**, not `website.yml`, and the owner's one-time setup (the WIF pool and provider, the deployer service account and its roles, the `website-live` environment, the three repository variables) is written out for the owner in **`docs/website/preview-promote.md`** — commands to run, never run by an agent. `docs/pending-workflows/README.md` carries the copy-in steps for the pair, and the channel-name input is validated against **the same pattern `scripts/deploy-website.sh` validates `--channel` against** (`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`), which a Go test reads out of both files.

Steps 2, 5 and 6 stand: `firebase.json` as it exists, the staged README updated, and nothing under `.github/workflows/` touched. The tests step is `internal/cli/docs_website_deploy_test.go`: it parses each staged YAML and requires `workflow_dispatch` to be the only trigger, no `pull_request_target`, every `uses:` pinned to a full commit SHA with its version comment, explicit least-privilege `permissions:` on the workflow and on every job, the environment and actor guard on the promote job, the channel pattern above, the preview URL written to `$GITHUB_STEP_SUMMARY`, and no key-file credential — with a companion test that plants a weakening per rule and requires the check that names it to report it.

### Task W2.2: The canonical URL and check 69a/69b in the register

**Files:** `website/data/site.yaml` (the canonical URL ~~and the secondary domain~~), `internal/cli/docs_website_boundary_test.go` (the pins in `TestWebsiteFactsMatchTheRepository`, relaxed in the same commit — W0.2 wrote that file and this task is the one that replaces the placeholder origin and moves the canonical host in `allowed_hosts` with it), ~~`firebase.json` (the `redirects` block — W2.1 created the file, this task adds the redirect)~~ (see the amendments below: there is no `redirects` block, and there cannot be one — and there is no second domain to redirect from), `docs/gcp-live-checklist.md` (the rows and the sections). **The hosting is D91's answer — Firebase Hosting in the website's own project — ~~so the redirect's home is `firebase.json`'s `redirects` block~~, and it targets only that project: an installation's Firebase project is never a deploy target here** (F13, and `docs/gcp-setup.md` `## Adoption`: *"one Firebase project serves one installation"*).

**Steps.**
1. Set the canonical URL ~~and the secondary domain~~ in `site.yaml`, which every absolute link, the sitemap, the OpenGraph tag and the canonical `<link>` read (W1.3) — so the domain change is one line and a rebuild, and **the registration itself is not a task** (D92, F15). One URL, not two: the owner ruled on 2026-10-17 that no second domain is theirs (the second amendment below).
2. ~~Write the redirect from the secondary domain to the canonical one as a `firebase.json` `redirects` entry~~ (the redirect is a property of the attached domain and has no `firebase.json` entry — and, per the owner's ruling of 2026-10-17, **there is no second domain and so no redirect to write at all**: see the second amendment below). **The custom-domain wiring is the owner's, and D92's answer says so: the owner registers the domain** (`fugaro.dev`), **adds it to the website's Firebase project and writes the DNS records at the registrar — no agent registers a domain or touches DNS.** What this task writes is the placeholder-to-real change of the one configured canonical URL value; the owner-run half is what check 69a verifies.
3. Re-read `docs/gcp-live-checklist.md` **on `origin/main` first** (U6): the highest written section, the summary table, and any reservation in `docs/plans/` or `docs/design/` that claims 69. **If 69 is taken, stop and take the owner's reallocation** — never renumber, never reuse (the register's own rule).
4. Write **check 69a** (the site is live and honest) and **check 69b** (nothing phones home) as rows in the register's summary table **and** as `## Check 69a` / `## Check 69b` sections, in the register's own format — owner-run, each step a command or an observation, each with its pass condition, and each naming what it would look like to fail.
5. Confirm that the two checks are the **only** thing in this plan that needs a live site, and that everything else ran hermetically (F58, F60).

**Tests:** the register's own docs test still passes; a test asserting the two new sections exist and are referenced from the summary table (the shape the register's existing entries use); the canonical URL appears in `site.yaml` only.

**Amended 2026-10-17 (this task's run) — the redirect is not a `firebase.json` entry, and there is no way to write one.** Firebase Hosting matches a redirect rule against the request **path**, and firebase-tools' schema for a rule (`schema/firebase-config.json`, `definitions/HostingRedirects`) admits `destination`, exactly one of `source`/`glob`/`regex`, and an optional `type` — `additionalProperties: false`, so **no field exists that could condition a rule on the request's host**. A rule written to move one host's traffic to another Hosting site would therefore fire on the destination host as well: `https://fugaro.dev/faq` would be answered `301 https://fugaro.dev/faq`, and the browser would report a redirect loop — on the live site, from the next deploy. A host-to-host redirect is **a property of the attached domain** (the Firebase console's own redirect option, or the Hosting API's custom-domain redirect), which is the owner's step and is D92's already, and never a `firebase.json` rule. Step 2 above is read accordingly: `firebase.json` is left alone, and `TestWebsiteFirebaseRedirectsDoNotLoop` is the mechanical half, so a later task that reaches for the looping rule meets a red test instead of a loop.

**Amended again 2026-10-17 (owner ruling) — the owner does not own the second domain, so there is none.** **The owner ruled that `secondary_domain` in `website/data/site.yaml` names a domain that is not theirs**, so the value, every page and document that named it and every step that attached or redirected it are gone: **the site has one domain, `canonical_url`, and nothing redirects.** The finding above stands and is kept, because it is what explains the absence — a `firebase.json` `redirects` rule cannot be conditioned on the host, so the only way to serve a second domain without a loop is a domain-level redirect on a host this project controls, and there is no second host. It is what `TestWebsiteFirebaseRedirectsDoNotLoop` still guards, and it is the reason no redirect rule exists anywhere in this tree. `TestWebsiteNamesNoSecondDomain` (with its own planted-copy control) scans every file under `website/` for the struck host and refuses it coming back; the boundary test's facts pin refuses the `secondary_domain` key by name; and check 69a observes the canonical origin and no other host.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** W2.1. **Commit:** `docs(checklist): check 69a and 69b — the site is live and honest, and nothing phones home`

**Red step.** Write the register test before the sections exist. `Expected: FAIL` — `summary table names check 69a, no "## Check 69a" section`.

### Task W2.3: The published ranges, and check 69c

**Files:** `website/content/costs.json` (published from W0.3's output), `docs/gcp-live-checklist.md` (check 69c), `website/src/pages/caps-and-costs.astro` (the sample-size and window rendering, if W1.5 stubbed it).

**Steps.**
1. Run the aggregation (W0.3) as the owner would — **one named installation, the sandbox** — over the window the page states, and commit its output as `content/costs.json`. **The command is the owner's to run**: this task documents the invocation, the window and the minimum-sample rule, and the check verifies the result.
2. Refuse to publish a thin sample: if no recipe reaches `min_runs`, `costs.json` carries the counts and the page renders the "not enough runs yet" state rather than a range — and the check records that as a pass with a note, not a failure (U1).
3. Write **check 69c** (the published ranges are real): re-derive each figure from the same window, compare, and confirm the sample sizes and the "not a promise" line are on the page.
4. Record in the page's own text what the figures are drawn from — the window, the sample size per recipe, the installation type (a sandbox, named as a sandbox and not by its repositories) — and nothing else (Global Constraint 10).

**Tests:** every figure in `costs.json` carries a sample size and a window; no recipe below `min_runs` carries a range; the page's numbers equal the JSON's; no repository name appears anywhere in `costs.json` (W0.3's identifier test re-run over the committed file).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Stats|Website|Docs|Lint' -count=1`

**Depends on:** W0.3, W1.5, W2.2. **Commit:** `feat(website): publish the measured ranges, and check 69c that they are real`

**Red step.** Write the "no repository name in costs.json" test with a slug in a group key. `Expected: FAIL` — `costs.json carries identifier "example-repo"`.

### Task W2.4: The video slot's empty state

**Files:** `website/src/components/Hero.astro` (the slot), `website/data/site.yaml`'s `features.video`, `internal/cli/docs_website_boundary_test.go` (the `features.video` pin in `TestWebsiteFactsMatchTheRepository`, relaxed in the same commit — the flag may not be flipped while that test still refuses it, and its message names this task), `website/test/video.mjs` (new — W1.1's `npm run check` runs every `test/*.mjs`, so this file is picked up without editing anyone else's).

**Steps.**
1. Render the slot only when `features.video` is true; while it is false, the hero carries **no player, no iframe, no poster, no play button** — a test asserts all four are absent from the built HTML (F57, D107).
2. Write the fill-in path: when the owner produces the video, `site.yaml` names its URL, and the origins check (W1.11) requires its host to be on the allowed list — so an embedded third-party player cannot arrive without a decision.
3. Add the poster-image rule: the poster is a file in the artifact, and the player has no autoplay with sound (D107).

**Tests:** no `iframe`, `video` or play button in the built hero while the flag is false; with the flag true, the video's host is on the allowed list and the poster is a local file.

**Command:** `cd website && npm run check`

**Depends on:** W1.3, and — for the two files it shares — W1.4 (`Hero.astro`) and W2.2 (`site.yaml`), so it runs in L8 (§7). **Commit:** `feat(website): the video slot, empty until the console has something to show`

**Red step.** Write the no-iframe test with a placeholder embed. `Expected: FAIL` — `hero embeds youtube.com while features.video is false`.

## 11. Part 3 — After the console ships (unscheduled)

### Task W3.1: The console downloads become real

**Files:** `website/content/downloads.json` (`available: true` with per-OS assets), `website/src/pages/console.astro`, `website/data/site.yaml` (`features.console_downloads`, flipped to true), W0.2's link test (extended).

**Steps.** Fill `downloads.json` from the console's own release artifacts, with per-OS entries and their **signature and notarisation status** (F24, D95); extend W0.2's test so that when `available` is true every URL points at a release asset of a **tag that exists** and is a **signed** artifact (the rule `docs/release.md` `## Verifying a release` states for the CLI, applied to the console); keep the read-only and no-telemetry sentences. **Depends on the console's first release — an owner decision to schedule — and, in `site.yaml` file order, on W3.2 (owner ruling 2026-10-17: W3.2 goes before W3.1, and both write `website/data/site.yaml`, so they stay sequential).**

**Changed 2026-10-18 (the owner's changes spec, item 6).** `downloads.json` carries the release's own **published names** now — `release.tag_prefix`, `release.version` and one `file` per asset with `{version}` where the version goes — and `src/lib/console-downloads.mjs` derives every URL at build time from them, from the tag (`console-vX.Y.Z`) and from `site.yaml`'s `repository`, so **no file under `website/` carries a download URL** and the flag flip is the whole change (Global Constraint 9). The steps below therefore fill in a **version**, not a list of URLs: `site.yaml`'s `features.console_downloads: true`, `downloads.json`'s `available: true` and `release.version` bumped to the tag the release was cut from. The console is **macOS only** — one universal `.dmg` and one `.zip` (`console/README.md`, `.github/workflows/console-release.yml`) — and the buttons are labelled from the platform and the asset's own extension, so no distribution is invented. Three places offer the console from that one reader: this page's Downloads section, Getting Started's console option (item 6), and — **added on 2026-10-19 (the owner's brief, item 6)** — the homepage's call to action, whose hero action row carries a `Desktop console` link to `/console/` while the flag is true and renders nothing about the console while it is false; `website/test/console.mjs` and `website/test/console-option.mjs` hold the first two to it, the second over a build with the flag already flipped, and `website/test/home.mjs` holds the homepage's row to the flag-off half.

**Tests:** every download URL resolves to an existing release tag; every asset is signed; the page still claims no more than D2/D4/D11 say.

**Landed 2026-10-19, against the published `console-v0.8.7` release.** The three values above moved (`site.yaml`'s `features.console_downloads: true`, `downloads.json`'s `available: true` and `release.version: 0.8.7`) and **a fourth arrived**: the release's own `console-sha256sums.txt`, copied verbatim to `website/data/console-sha256sums.txt`, because a checksum is the one fact the release's names cannot imply — no build can compute it, so a well-formed value is no evidence of anything, and the release's own file is what `src/lib/console-downloads.mjs` and the boundary test hold every `sha256` to. W0.2's link test was extended rather than retired: while the flag is off it still refuses any `releases/download/` URL under `website/`, and while it is on `TestWebsiteConsoleReleaseFactsAreComplete` holds the tag prefix to the workflow's own `CONSOLE_TAG="` line, the version to a release note that exists, and each published name and checksum to the release's own documents. The one thing no offline check can make — that the URL answers — stays check 69a, which the owner runs. `console.mjs` and `console-option.mjs` keep their unreleased-state refusals alive on fixtures that flip the flag back off, and the byte budgets were re-derived in the same commit.

**Moved to `console-v0.9.0`, 2026-10-19 (the owner's ruling: the site's console downloads follow the console's own release).** The console version is the CLI tag the release was cut from, so the move is `docs/releases/v0.9.0.md` naming the console's own release, `downloads.json`'s `release.version` and both `sha256` values, and `website/data/console-sha256sums.txt` replaced by the published `console-v0.9.0` asset byte for byte. The site's three claims about *how* the console ships are unchanged and only their numbers moved: the assets are still one universal `.dmg` and one `-mac.zip`, still signed with a Developer ID certificate and notarised by Apple, still served with the dot GitHub rewrites the space to. **The tag-prefix check the earlier review asked for now lives in the build, not only in the gates:** `src/lib/console-downloads.mjs` reads `CONSOLE_TAG="<prefix>$VERSION"` out of `.github/workflows/console-release.yml` and refuses a `release.tag_prefix` the workflow does not compose, which is the one check that turns a right-shaped-but-wrong prefix into a red build rather than a 404 behind every button (`website/test/console.mjs` plants the disagreement and requires the refusal).

**Note added 2026-10-20 (the owner's Linux ruling).** The owner ruled that day: *"yes, Linux for 0.10.0, unsigned"* ([design D10](../design/desktop-console.md) and its §7.6), so `downloads.json` may now carry **one optional per-platform block** — `linux: { text, assets[], source }` — that the reader validates and `Downloads.astro` renders **only when it is there**; its `assets` are ids of the file's own `downloads` entries, so a per-platform link can only exist for an asset the file names and the release's sums file records, and its rows are drawn **under that block** rather than twice in the list above. **The shipped file carries no such block and the page still says macOS only**, because no release has published a Linux asset yet: the entry lands with the release that does, and the same change adds the two asset names and their checksums to `downloads.json` and their lines to `website/data/console-sha256sums.txt`. What the note does not do is soften this task's rule: the block's presence gates a *new platform's* links, never a link for an asset that does not exist. `website/test/console.mjs` holds all three states (absent, refused when it names an asset the file does not carry, and rendered with its own rows when it is there).

**Command:** `cd website && npm run check` **Commit:** `feat(website): the console downloads, once the console has a signed release` **Red step.** Point a URL at a tag that does not exist. `Expected: FAIL` — `console download points at tag v9.9.9, which does not exist`.

### Task W3.2: Analytics, switched on behind the consent gate

**Files:** `website/data/site.yaml` (`analytics`: the measurement id and the flag), `website/README.md` (the record), `website/test/consent.mjs` (extended to the enabled state).

**Steps.**
1. **D93 was answered on 2026-10-16 — Firebase Analytics, behind a consent banner — so this task asks nothing and decides nothing.** Set the Firebase Analytics measurement id in `site.yaml` (the value the owner supplies, public by nature), turn `analytics.enabled` on, and confirm the load still goes through **W1.12's gate unchanged**: nothing is fetched before consent, and the privacy page and the one-sentence disclosure stay true.
2. Record in the site's README that the analytics is **Firebase Analytics (Google Analytics)**, that it is **consent-gated and sets identifiers**, that a banner is normally required in the EU/UK — which is why the banner ships — and that clearing `analytics.enabled` (or the id) turns the whole thing off with no other change.
3. Extend `website/test/consent.mjs` to the **enabled** state: the analytics origin is requested after consent and only after it; withdrawing consent stops the load; the disclosure sentence is present; and with the flag off there is no banner and no request at all.
4. This task waits on **W1.12** and on the `site.yaml` order ahead of it (W2.4) — **not on an owner decision any more** — and **W3.1 now follows it** (owner ruling 2026-10-17), so analytics does not wait for the console's first release.

**Tests:** exactly one analytics origin in the built HTML and only after consent; no cookie before consent; the disclosure sentence present; with the flag off, no analytics request and no banner.

**Command:** `cd website && npm run check` **Commit:** `feat(website): Firebase Analytics, switched on behind the consent gate` **Red step.** Build with the flag true and the gate bypassed. `Expected: FAIL` — `page requests googletagmanager.com before consent`.

**Amended 2026-10-18 (owner ruling) — the tool is Google Analytics 4 with the id `G-DZ4F35D3EH`, the gate is a first-party file, and the CSP widens by exactly what Google documents and no more.** The owner ruled on 2026-10-18, in the brief this task was run from, on five points the steps above leave open, and **the ruling supersedes the wording of this task wherever the two differ** — the sequence D93 answered (measurement behind W1.12's banner), the flag, the one admitted origin and the load that goes through the gate are unchanged.

1. **Google Analytics 4, and the id is a `G-` measurement id.** D93's answer and this task's own prose call the tool *Firebase Analytics*; what ships is **Google Analytics 4**, reached through the same Google account, and the value the owner supplies is a GA4 measurement id (`G-DZ4F35D3EH`) that the loader passes to Google's own `gtag/js`. The record in `website/README.md`, the privacy page and the site.yaml comment name **Google Analytics 4**, and the id is written in **`website/data/site.yaml` and nowhere else** — `website/test/consent.mjs` walks `src/`, `data/`, `public/`, `test/`, `content/`, the top-level files and `firebase.json` and refuses the literal anywhere but `data/site.yaml`, so a second copy fails rather than drifts.

2. **No inline script, and therefore no `'unsafe-inline'` and no nonce.** The owner's redesign ruling (D183, one first-party script file) and the brief's own words ("if gtag needs an inline bootstrap, load a first-party file that sets dataLayer and gtag") put the whole bootstrap — `window.dataLayer`, the `gtag` shim, `gtag('js', …)` and the one `gtag('config', …)` — inside `website/public/scripts/site.js`, the single first-party file that already carries the banner controller and the copy control. **This is why W1.12's gate moved out of `Base.astro` and out of `ConsentBanner.astro`**: an inline block would have needed `'unsafe-inline'` or a nonce, and the ruling refuses both. The banner's markup carries the gate's configuration (`data-consent-key`, `data-analytics-id`, `data-analytics-origin`) and the file reads it, so **the gate names no URL of its own** — which is what keeps the strong property that **an artifact built with the flag off names no analytics origin anywhere**, and `website/test/origins.mjs` holds both halves (the flags-on artifact carries exactly one configuration and one `/scripts/site.js`, the flags-off artifact names the origin nowhere).

3. **The policy widens by exactly what Google documents for GA4 without Ads features, read from the source on 2026-10-18.** `https://developers.google.com/tag-platform/security/guides/csp` — the page the brief requires be cited — gives, for a site that uses GA4 and **not** the Advertising features: `script-src-elem https://www.googletagmanager.com`, `img-src https://www.googletagmanager.com https://*.google-analytics.com`, and `connect-src https://www.googletagmanager.com https://*.google-analytics.com https://*.google.com`. `firebase.json`'s `script-src` becomes `'self' https://www.googletagmanager.com` (the origin in `script-src` rather than `script-src-elem`: the fallback is equivalent and the site keeps one script directive), and `img-src` and `connect-src` gain their three documented origins each. **The list is not the one this task's brief expected**: the brief named `https://*.analytics.google.com`, which the page does not list, and did not name `https://*.google.com`, which it does; the contract follows the documentation, per the brief's own "verify against Google's current documentation and keep only what is needed". `website/test/origins.mjs` pins the whole policy as exact token lists with the citation and the transcription noted, and refuses any token outside it — another origin, `'unsafe-inline'`, `'unsafe-eval'`, a nonce, a hash or a wildcard in `script-src`. `website/test/brand.mjs` holds the coarser rule that the three hosts are the only origins anywhere in the policy.

4. **The privacy defaults are in the one `config` call, and the test reads them.** `gtag('config', id, {allow_google_signals: false, allow_ad_personalization_signals: false})` — no Google Signals, no ads personalisation, no advertising features, no user id, and **page_view only**: no `event`, `set`, `get` or `consent` call beside it. GA4 anonymises the IP by default and nothing here turns that off. `website/test/consent.mjs` asserts the exact call and refuses a second one, so re-enabling an advertising feature is a red test rather than a silent change; the privacy page names the same defaults and is held to them by the same file.

5. **The task's `Files` row above is too narrow.** In practice this task writes `website/data/site.yaml` and `website/README.md` as listed, and (because the gate became code in the one file) `website/public/scripts/site.js`, `website/src/layouts/Base.astro`, `website/src/components/ConsentBanner.astro`, `website/src/pages/privacy.astro`, `firebase.json`, and the gates that read those: `website/test/origins.mjs`, `consent.mjs`, `copy.mjs`, `layout.mjs`, `brand.mjs`, plus the byte budgets in `website/README.md` re-derived in the commit that ships the script (D192's rule: 3 × the build × 1.25, rounded up; the figures themselves live in `website/README.md`'s budget list and `website/test/budget.mjs` is the authority for them, which is why none is written down here — they move with the script). The flag-on privacy page is held to the word ceiling the way every other page is, and **the number is not written here**: `website/test/budget.mjs`'s `wordsCeilingFailures` is where the ceiling and its derivation live, and `website/README.md` states it. This note recorded a page at D188's 250 words with a 14-word lead, with the caveat's own short section ("What this page does not cover") brought back to carry the sentence about the host's request logs — an earlier draft of the page cut that sentence and pointed at `/security/` for it, which does not carry it, because the security page is the Fugaro program's posture and not the host's logs. **Two things have since moved, and the note says so rather than reading as today**: the owner's request of 2026-10-19 (PR #689) gave the page a second named part ("The software") because the stores link one privacy policy URL, and that request's text and the app's reporting took the page past 250 — so the ceiling is now derived from the page's own count plus 25 % (D192's headroom, applied by `budget.mjs`) instead of D188's 250, which still holds every other page; and the caveat is a list under "What this site does not do" rather than a section of its own, with the host's-logs sentence still the last item. The page's own sentences are held to the repository by `website/test/privacy.mjs`, and the parts' scoping by `website/test/consent.mjs`. Check 69b's step 2 in [docs/gcp-live-checklist.md](../gcp-live-checklist.md) is corrected in the same pass: its text said `analytics.enabled` is false until this task and that today's pass is "no analytics request appears at all", which this task makes false.

6. **A withdrawal deletes GA4's cookies, not only the sends** (owner ruling, 2026-10-18 — the amendment under W1.12 is the whole of it). Step 3 above is read accordingly: "withdrawing consent stops the load" is the first half, and the second is that the identifiers the tag wrote while the visitor had accepted are removed from the browser — `_ga`, `_ga_<container id>` derived from the id `site.yaml` carries, `_gid`, `_gat` and the `_gat_*` names — on the page's host, and on the parent domain where one is tried, with `Path=/` and an expiry in the past. The privacy page's sentence says so, `website/test/consent.mjs` runs the shipped file against a path-keyed cookie jar, and Check 69b's own step reads the cookies in a browser, which is where the part no stub can prove lives.

### Task W3.3: The explainer video

**Files:** `website/data/site.yaml` (`features.video`, the URL), `website/src/components/Hero.astro`, `website/content/` (the poster).

**Steps.** Embed the owner's 30–45 s video from an origin on the allowed list or self-hosted, with a local poster, no autoplay with sound, a transcript link, and a reduced-motion path that shows the poster rather than an autoplaying animation. **Depends on the owner producing the video** (spec's own open item).

**Tests:** the host is allowed; the poster is a local file; no autoplay with sound; a transcript link exists.

**Command:** `cd website && npm run check` **Commit:** `feat(website): the explainer video, embedded from an allowed origin` **Red step.** Embed from a host not on the list. `Expected: FAIL` — `video host vimeo.com is not on the allowed-host list`.

## 12. Cross-task dependencies

| Task | Needs | Because |
|---|---|---|
| W0.2 | W0.1 | Same PR; the boundary walk assumes `website/data/` exists |
| W0.4, W0.5, W0.6 | nothing | They write `internal/clidoc/`, `website/content/`, `website/src/lib/` and `website/test/changelog.mjs`, which no other task writes; W0.2's boundary test is a rule over the tree, not a gate in front of them |
| W1.1 | W0.2, W0.4, W0.5 | The build reads `data/` and `content/`; the pipeline (W1.3) fails on a missing file |
| W1.2, W1.3 | W1.1 | Tokens and layout need the toolchain |
| W1.4–W1.10 | W1.3 | Every page needs the layout and the content pipeline |
| W1.5 | W0.3 | The caps page renders `costs.json` |
| W1.8 | W0.5 | The security page renders the generated file |
| W1.9 | W0.6 | The changelog page calls the generator at build time |
| W1.11 | W1.2, W1.4–W1.10 | The gates check the built pages, and the contrast case needs the tokens |
| W1.12 | W1.3, W1.11 | The layout it extends, and the origins check whose allowed list widens by exactly one entry (D93, D106) |
| W2.1 | W1.11 | The workflow runs the site's own checks |
| W2.2 | W2.1 | The live checks follow the deploy |
| W2.3 | W0.3, W1.5, W2.2 | The figures need the command, the page and the check number |
| W2.4 | W1.3; W1.4 and W2.2 by file | The slot lives in `Hero.astro`, which W1.4 writes, and both it and W2.2 write `site.yaml` — so W2.4 follows both, in L8 |
| W2.2, W2.4, W3.2, W3.1, W3.3 | each other, in that order (W3.2 before W3.1: owner ruling 2026-10-17) | All five write `website/data/site.yaml`; no two of them share a level (§7). *The row listed four before: W3.1 was a writer too but was not in it.* |
| W3.1 | the console's first release, and W3.2 in `site.yaml` order | D95, F23 |
| W3.2 | W1.12, and the `site.yaml` order ahead of it (W2.4) | The analytics load goes through the consent gate; **D93 was answered on 2026-10-16, so nothing here waits on a decision** |
| W3.3 | the owner's video | The spec's own open item |

## 13. Decisions the owner has answered

**All twenty rows of the reconciliation's §4 were answered by the owner on 2026-10-16, seven of them the owner's alone.** §7.1's one rule therefore needs only the **go**; this table says what each answer governs here. **Two answers differ from the reconciliation's recommendation — D91 and D93 — and both are carried into the tasks; the other eighteen are the recommendation.**

| Decision | Answer of 2026-10-16 | What it governs here |
|---|---|---|
| **D90** where the site lives | **as recommended — this repository, under a top-level `website/`** | Every path under `website/` stands as §5 writes it: wave 1, Parts 1–3. |
| **D91** hosting | **against the recommendation — Firebase Hosting, in a SEPARATE Firebase project created for the website only** | W2.1 (the staged workflow and the `firebase.json` it writes), W2.2. The owner creates that project; the deploy credential is scoped to it and is neither an installation's nor a run's; **no run may hold the credential**; the deploy is an owner-run step from the staged workflow; the project id is supplied by the owner at deploy time through the workflow's variable and is never committed where it is not public; no installation's project id appears anywhere. |
| **D92** domain | **as recommended, and the second domain is struck** | W2.2: the site takes **one configured canonical URL value**; the owner registers the domain (`fugaro.dev`) and owns the custom-domain and DNS steps; **no agent registers a domain or touches DNS**; check 69a cannot pass before the owner does. The owner ruled on 2026-10-17 that they do not own the second domain the answer had conditioned on ("`fugaro.ai` redirected if acquired"), so there is no second value, no redirect and nothing to attach — the amendment under Task W2.2 records it. |
| **D93** analytics | **against the recommendation — Firebase Analytics WITH a consent banner** | W1.12 (the banner and the gate, now scheduled), W3.2 (the measurement id and the switch), W1.11 (the one allowed origin), D106 (the one exception). Analytics load **only after consent**; the consequences stand recorded: Google Analytics sets identifiers, a banner is normally required in the EU/UK, and the page loads a Google script. |
| **D94** the install text | as recommended | W0.1, W1.5: one install source pinned to `README.md`, `docs/release.md` and `.goreleaser.yaml`, Windows stated as untested. |
| **D95** the console section | as recommended | W1.6, W3.1: a flag-gated placeholder that renders nothing linkable while the flag is false. |
| **D96** the aggregation's data policy | as recommended | W0.3, W2.3: `fugaro stats runs --project <name>`, sandbox/dogfood (the `fugaro` installation) data only, `belong` never aggregated unless the owner says so, minimum **20 runs** per range. |
| **D97** what makes a range honest | as recommended | W0.3, W1.5, W2.3: no small-sample range, the window and sample size printed, the "not a promise" line, and no "~2 h" sentence. |
| **D98** the docs section | as recommended | W1.7, W0.4: link, do not copy, plus the generated CLI reference. |
| **D99** the security page | as recommended | W0.5, W1.8: generated from `SECURITY.md`, byte for byte below the title. |
| **D100** the changelog | as recommended | W0.6, W1.9: built from `docs/releases/` at site build time, with no test that reads a tag. |
| **D101** the community page | as recommended | W1.10: link, and do not restate; the CI description taken from `ci.yml` as it is today or omitted. |
| **D102** the tagline | as recommended | W1.4: `README.md`'s own tagline line, byte-identical, with every other sentence traceable. |
| **D103** the FAQ's comparison | as recommended | W1.10: every comparison row carries a source and a date or it is dropped, and **the owner reviews the FAQ before it publishes**. |
| **D104** the palette | **as recommended — palette C** | W1.2: violet for structure and links, orange for warnings and cost; `palette-split.css` is the one in force, the other two ship as alternatives. |
| **D105** motion, accessibility, performance | as recommended | W1.2, W1.3, W1.11: the contract as numbers a test enforces. |
| **D106** what the page may load | as recommended, **with the D93 exception** | W1.11, W1.12: the origins check admits **exactly one** third-party script — Google's consent-gated Analytics script — and no other font, CDN, embed or pixel. |
| **D107** the explainer video | as recommended | W2.4, W3.3: a slot that renders nothing until the owner has a video. |
| **D108** testing without a deployed site | as recommended | W0.1–W0.6, W1.1–W1.12: four gates, three hermetic and one the owner's; the live checks are 69 and up. |
| **D109** release placement | as recommended | W2.1: owner-run from the staged workflow, and added to nothing — no `website` check in the release gate. |

**Nothing in this plan waits on an unanswered row any more.** What can start, and under which answers, is §7.1's one rule — **a wave starts once the owner has given a go for it, and nothing else blocks**; the register's "67 onward" clause (§2, §15 item 6) remains a gate item for Part 2 only.

## 14. Open items, listed rather than fixed

1. **The register's 69 may be taken by the time Part 2 lands** (U6). W2.2 re-reads `origin/main` and stops rather than renumbering.
2. **The sandbox installation may hold too few runs for any range** (U1). The command's "not enough runs" answer is a valid outcome and the page renders it.
3. **Some sampled runs carry an estimated compute cost** (U2). The command reports the count; a range that would fold an estimate into a measurement is not published.
4. **`CONTRIBUTING.md`'s `## What CI runs` is stale** (F11, F46). It is a root file, outside this PR's `docs/`-only scope; it wants its own one-line PR, and the site's community page is written to be correct either way.
5. **`docs/release.md`, `README.md`, `.goreleaser.yaml`'s `release.header` and `docs/design/v1.md` §12 remain four copies of the install text.** W0.1 pins the site against them, which is a fence around the fifth copy rather than a fix of the first four; collapsing them into one file is a separate, larger change (D94's alternative).
6. **No `.claude/rules/` files and no `CLAUDE.md` exist in this repository** (F8). This plan's constraints live in §3 and in tests, which is what the repository uses instead.
7. **No `CODEOWNERS` file exists** (F63), so this work routes no reviews and updates no routing file; the reviewer tiers in §1 are this plan's own.
8. **The FAQ's comparison section is empty until the owner supplies sourced rows** (D103, F51). The section renders nothing rather than an unsourced claim.
9. **The three palettes' gradient contrast is unverified until W1.2 runs** (U5); the owner should choose from the palettes that have passed rather than from a description.
10. **The changelog page names a release before its release exists, and that is the state the marker is for** (W0.6). `docs/releases/README.md` requires the Highlights file to be merged to `main` **before** `scripts/release.sh` runs, so between that merge and the version bump the newest entry is a release GitHub does not have yet: the entry renders as **not yet released**. **Amended 2026-10-19 (the owner's changelog brief): the flag is a file, not a tag.** It is false for a release file whose version is above `plugin/.claude-plugin/plugin.json`'s, and the release process merges that bump to `main` (`scripts/release.sh`'s "release: vX.Y.Z" PR) before it pushes the tag — so the window is one release file wide in every checkout, shallow or not, instead of as wide as the checkout's tag list. The tag was never the fact the page needed: the deploy build checks out without tags, so the old rule printed the marker under **every** release, which is the owner's report (*"Changelog: why do I see not yet released?"*) and the reason W0.6's step 2 was replaced. The perpendicular fix — asking the releases API — remains a network call the build deliberately does not make (W1.1), so the plan records the window rather than passing it off as a state.
11. **The website's own Firebase project does not exist yet, and it is the owner's to create** (D91). Part 2's deploy cannot run before that: the owner creates the project for the website only, supplies its id at deploy time through the workflow's variable, and holds the credential, which no run may hold. **Nothing in the tree names a Firebase project** — no `.firebaserc`, no project id, no site id — and that is the design rather than an omission.
12. **The site carries analytics, and that is now a property of the product** (D93): Firebase Analytics behind a consent banner **sets identifiers**, a banner is **normally required in the EU/UK**, and the page **loads a Google script** — the one exception to "nothing third party" (D106), gated in W1.12 and admitted as a single allowed origin in W1.11. Recorded here so the trade-off is read rather than discovered, and so the privacy page (W1.12) states it.

## 15. OWNER APPROVAL GATE

**The gate is §7.1's rule, per wave, and nothing more:** a wave starts once the owner has **given a go for that wave**; with every decision answered, the gate carries no answer any more. The owner's direction (2026-10-16) was to **start as soon as we can**, and the answers were given the same day; a direction is not the go, and the go is what is still outstanding. The items below are what a go is given on; **none of them is a start condition unless §7.1 lists it**.

1. **Give the go, wave by wave** (§7.1). **Every row of §4 is answered — D90, D96, D91 and D92 among the ones the waves once listed, and the other OWNER-ONLY rows (D93 analytics, D102 the tagline, D104 the palette) with them** — so what a go settles now is the order and the timing, not the content. Two answers are **not** the reconciliation's recommendation (**D91** Firebase Hosting in a separate website-only project, **D93** Firebase Analytics behind a consent banner), and this plan is written to them.
2. **The order**, which puts the generated facts and their drift tests (Part 0) and the aggregation command (W0.3) **before any site code**, and which is the recommendation rather than the only possibility.
3. **The four refusals** in the reconciliation's §5: no console download link until the console is signed and released, no "~2 h" sentence until the aggregation produces it, no average published as a range, and no Windows support claim beyond what `.goreleaser.yaml` builds.
4. **The two rules this plan cannot implement by itself**: that the deploy workflow is **staged** under `docs/pending-workflows/` for the owner to copy (no run may touch `.github/workflows/`), and that the site is **not** added to the release gate.
5. **That check 69 (as 69a–69c) is the owner's to run**, and that W2.2 writes it into the register when Part 2 lands rather than this plan writing it now.
6. **The register's "67 onward" clause, settled for this work** — either confirming the bounded reading this plan applies (the clause is spent on the numbers managed projects took and wrote, 67 and 68, so 69 is this work's first) or reading it as open-ended and allocating the site's checks a number of its own. **This one is a start condition, for Part 2 only, and §7.1 lists it**: W2.2 re-reads the register, and if 69 is taken or written anywhere, it stops rather than renumbering (§2, U6).
7. **The one exception to "nothing third party"** — D93's consent-gated Google Analytics script, which D106 now carries in writing: **one script, behind a gate that loads nothing before consent, with the privacy page stating what is measured and how consent is given and withdrawn** (W1.12). A go is given on that shape, because it is the single place the project's "nothing phones home" posture is relaxed.
8. **The reviewer for the SECURITY-SENSITIVE task** (W0.3, the aggregation) — a task whose whole value is that its output carries no identifier, and whose failure mode is silent. **This is a merge condition, not a start condition**: W0.3 starts under §7.1's rule, and its PR does not merge until the owner has named its reviewer.
