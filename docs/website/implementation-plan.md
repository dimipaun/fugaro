# The Fugaro website: implementation plan

Status: **plan only; nothing is implemented.** Owner: **@Dimi**. Date: **2026-10-16**. This plan executes [reconciliation.md](reconciliation.md), whose findings it cites by number (`F<n>`); the owner's own text is [spec.md](spec.md). Directory index: [README.md](README.md).

Related: [docs/plans/2026-10-16-managed-projects.md](../plans/2026-10-16-managed-projects.md) (the shape this plan follows, and the sibling that holds D55–D78 and checks 67–68), [docs/design/desktop-console.md](../design/desktop-console.md) (D2, D4, D10, D11 — read, not reopened), [docs/release.md](../release.md), [docs/pending-workflows/README.md](../pending-workflows/README.md), [docs/gcp-live-checklist.md](../gcp-live-checklist.md), [CONTRIBUTING.md](../../CONTRIBUTING.md).

## 0. What this plan is, and the one thing it does in an unusual order

This is a plan for a second artifact that does not exist yet, in a repository whose own rules make three of its steps the owner's: the site's **deploy** is a `.github/workflows/*` change (F4), its **domain** is a registration (F15), and its **hosting, palette, tagline and aggregation data policy** are decisions (D90–D109). So the plan is ordered by what is **provable without a deployed site** and by what is **independent of the owner's answers**, and the parts are cut so that everything before Part 2 is mergeable on its own merits — the drift tests and the aggregation command are worth having even if the site is never built, and the site's pages are worth having even if the host is never chosen.

**The unusual order is deliberate: the facts come before the site.** Every page the spec asks for that is *about* the product is a rendering of something that already exists in this repository — the install instructions, `SECURITY.md`, `docs/releases/vX.Y.Z.md`, the command tree, the run records. Part 0 builds the machinery that turns each of those into a file, and a test that fails when the file and its source disagree. Part 1 builds a site that renders those files and nothing else. A site built the other way round — pages first, facts pasted in — is the failure mode F21 describes four times over: the copy most likely to be read is the copy least likely to be updated.

**Two things this plan does not do.** It does not answer any decision row: §13 lists them, and the gate at §15 holds the whole plan behind the owner's answers. And it does not touch a single file under `.github/workflows/` — the deploy workflow is **staged** under `docs/pending-workflows/` for the owner to copy, which is the mechanism the repository already uses (F4, F62).

## 1. Part summary

| Part | What it ships | Tasks | Independently mergeable? | Reviewer tier |
|---|---|---|---|---|
| **Part 0 — The facts** | The generated content layer: install facts, site facts, the CLI reference, the security page's source, the changelog's source, and `fugaro stats runs` — the owner-run, read-only aggregation. No site code. | W0.1–W0.6 | Yes, each on its own; the whole part merges without Part 1 | **opus** (W0.3 is SECURITY-SENSITIVE), sonnet for the rest |
| **Part 1 — The site** | The Astro app under `website/`: toolchain, tokens and palettes, layout, the generated-content pipeline, and the ten sections of the spec that the repository can back. Plus its hermetic gates: build, link/anchor check, drift tests, accessibility and origin lint. | W1.1–W1.11 | Yes, per PR group; the shell (W1.1–W1.3) must land before the pages | **opus** for the boundary and origin tests, sonnet for the pages |
| **Part 2 — Hosting, deploy, live checks** | The staged deploy workflow, the owner's copy, the canonical URL and redirects, the register's check 69, and the video slot's empty state. | W2.1–W2.4 | Yes, after W1.1 | **opus** (the workflow is staged, not written, and its content is a security boundary) |
| **Part 3 — After the console ships** | The console download links become real; the explainer video is embedded; analytics is switched on if D93 says so. **Unscheduled — it waits on the console's first release and on the video.** | W3.1–W3.3 | Yes | **opus** (a public link to a signed artifact) |

## 2. Check numbers this plan takes (69 and up)

**This plan takes 69 and up, written as 69a–69c**, in [docs/gcp-live-checklist.md](../gcp-live-checklist.md). The register's highest written `## Check N` section on `origin/main` is **58**; 59 (59a/59b), 60, 61, 62–63, 64–66 and 67–68 are reserved by the console, budget-token-channel, project-centric, console-v2, voice and managed-projects lines respectively, each written down where it was allocated. **69 is the next free number, and the sub-letters are the register's own convention** for one line of work's several live steps (43a–43f, 59a/59b, 67a/67b):

| Check | What it is | Who runs it | Written by |
|---|---|---|---|
| **69a** | **The site is live and honest.** The canonical URL resolves over HTTPS, the redirect from the secondary domain lands on it, the deployed HTML is the artifact the build produced, and the page a stranger lands on names no unshipped feature (no console download link, no video player, no platform the build does not produce). | Owner | W2.2 |
| **69b** | **Nothing phones home.** Loading the live page with the network panel open shows **no request to an origin outside the allowed-host list** — no font, no script, no embed, no pixel — and, if D93 chose analytics, exactly the one endpoint the decision names and nothing else. | Owner | W2.2 |
| **69c** | **The published ranges are real.** The caps-and-costs page's figures are re-derived from the aggregation command over the same window and match, the sample sizes are printed, and the "not a promise" line is present. | Owner | W2.3 |

**The "67 onward" collision, and the reading this plan applies.** The sentence all three documents quote hands the register **"managed projects 67 onward"**, and the managed-projects plan reads that as an open-ended block (`docs/plans/2026-10-16-managed-projects.md` §2, step 5: *"a block is held for the work it was given"*). Read that way, 69 is inside another line of work's block and is not free — so this plan applies the **bounded** reading and records it here rather than letting the number speak for itself: "onward" is spent on the numbers a line of work actually takes and writes (managed projects took 67 **and** 68), a clause that reached every number above 67 would leave no later line of work able to take one at all, and **if the owner reads the clause as open-ended, or any document on `origin/main` writes 69, then 69 is managed projects' and the owner allocates** — W2.2 re-reads the register *before* it writes anything and stops rather than renumbering (U6), and §15 carries the question as a gate item. **A reserved number is never reused or renumbered** either way, and as in every precedent **this plan does not write the row or the section**: W2.2 owns both, so one writer owns the number and a rebase cannot produce two.

## 3. Global Constraints

1. **DOCS-ONLY until the owner answers §13.** The commit that adds this plan and the reconciliation touches **only** `docs/website/*`. No task in this plan starts before the gate at §15.
2. **Nothing under `.github/workflows/` is ever created, edited or deleted by a run** (F4). A workflow change is authored under `docs/pending-workflows/` and copied in by the owner, and the staged file is removed in the same PR that copies it (the procedure `docs/pending-workflows/README.md` describes).
3. **`GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` must pass** after every task, and `go test ./...` must pass after any task that adds or changes Go code (F10).
4. **Tests are hermetic**: no real cloud, no network, no clock, no credential (F58). Every gate in this plan runs from the built artifact and the files in the tree; the live checks are the owner's, numbered 69 and up.
5. **No new third-party dependency in the Go module for the site's benefit.** The generators of Part 0 use the standard library and what the module already imports; the CLI reference walks `NewRootCmd()` rather than adding `cobra/doc` if the walk is a hundred lines, and if `cobra/doc` is added it is added with the version already in `go.mod`.
6. **One source of truth per fact, and a test that holds it** (F37). A page never restates a document; it renders it or links it. Every generated file names its source in a header comment.
7. **The site's own text is checked against `plugin/readme_test.go`'s retired-name walker**: files under `docs/` are read by `TestReadmeNamesNoRetiredSkill`, so no file this plan adds may name a retired skill (F7).
8. **Nothing on the page loads from a third party** (F55, D106): fonts self-hosted, no CDN, no embed outside the allowed-host list, and the promise is enforced by a test over the built HTML's origins.
9. **The site never links an unsigned or unreleased artifact** (F25): the downloads section renders from a data flag, and a test refuses to emit a link while the flag is false.
10. **The aggregation reads one named installation, read-only, and emits no identifiers** (F32, D96): no repository name, run ID, branch, PR URL, task text, person or commit appears in its output, **`belong` and its repositories are named by no output, no check, no published figure and no test fixture this plan produces** — every red step and every test fixture uses a synthetic name, and the documents that record the rule name it only in order to state it — and `belong` is never read by an agent; the default target is the sandbox installation.
11. **No number is published from a sample smaller than the minimum** (D97), and every published range carries its window, its sample size and the "not a promise" line.
12. **Security claims are never paraphrased** (F41, D99): the security page is generated from `SECURITY.md`, byte for byte below the title, with its headings unchanged so the document's own cross-references keep pointing the right way.
13. **The release train is not touched** (F5, F61, D109): no `website` check is added to `ci.yml` or `scripts/release-gate.sh`, and no CLI tag depends on the site.
14. **Add new commits; never amend or rebase** (this run's own rule, and the same discipline the sibling plans carry).
15. **Every task's red step is real**: the test named in `Red step` must fail before the task's code exists, and the task records the exact `Expected: FAIL` line it saw.

## 4. Review focus

| Risk | Where it bites | Who reviews |
|---|---|---|
| A generated file drifts from its source and the test is written against the generator rather than against the source | Part 0, all of it | opus — the test must read the **source** (the README, the goreleaser config, the struct, the directory listing), not the generator's own output |
| The aggregation leaks an identifier — a repository slug in a group key, a run ID in a "sample" field, a task title in an example | W0.3 | **opus, SECURITY-SENSITIVE** — the test is a golden-output test over the command's `--json`, asserting the **set of keys** and that no value matches any identifier shape |
| A public page makes a claim the product cannot back (the four of F50) | W1.4, W1.5, W1.6, W1.10, W3.x | opus |
| A third-party origin reaches the built HTML through a font, an icon set or an embed | W1.11 | opus |
| A staged workflow that deploys more than it says, or holds a credential it should not | W2.1 | **opus** — the staged file is read line by line against D91, D109 and F14 |
| The site's build depends on the network (a Google font, an npm postinstall that fetches) | W1.1 | opus |

## 5. File structure

**What exists today:** nothing under `website/`; nothing under `docs/website/` except the four documents this plan's PR adds; no CLI reference; no generator of any kind; `internal/cli/docs_*.go` as the only document-pinning tests.

**What this plan creates:**

```
docs/website/                     # this PR — the four documents, and nothing else
docs/pending-workflows/
  website.yml                     # W2.1 — staged, for the owner to copy
website/                          # Part 1 — the artifact
  package.json, package-lock.json, astro.config.mjs, tsconfig.json
  README.md                       # how to build and check it, and what is generated
  data/
    install.yaml                  # W0.1 — the one install source
    site.yaml                     # W0.2 — canonical URL, analytics, feature flags
  content/
    cli-reference.md              # W0.4 — generated, do not edit
    security.md                   # W0.5 — generated from SECURITY.md, do not edit
    changelog.json                # W0.6 — generated from docs/releases/, do not edit
    costs.json                    # W0.3 — the aggregation's output, published deliberately
  src/
    layouts/Base.astro
    components/{Hero,Terminal,ArchitectureDiagram,CapsCosts,Downloads,Changelog}.astro
    pages/{index,how-it-works,getting-started,caps-and-costs,console,docs,security,changelog,faq,community}.astro
    styles/tokens.css, palette-violet.css, palette-orange.css, palette-split.css
  test/
    linkcheck.mjs                 # W1.11 — links and anchors over the built HTML
    a11y.mjs                      # W1.11 — contrast, reduced-motion, keyboard, origins
internal/clidoc/                  # W0.4 — the generator, in the Go module
  cliref.go, cliref_test.go
internal/cli/
  docs_website_boundary_test.go   # W0.2 — the console's boundary test, for the site
  docs_website_install_test.go    # W0.1 — install facts against the repository
  docs_website_cliref_test.go     # W0.4 — the reference against NewRootCmd
  docs_website_security_test.go   # W0.5 — the page against SECURITY.md
  docs_website_changelog_test.go  # W0.6 — the changelog against docs/releases/
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
| **P7** | W1.11 | The gates: link and anchor check, accessibility and origin lint, the JavaScript budget. | **opus** |
| **P8** | W2.1, W2.2, W2.3 | Hosting: the staged workflow, the owner's copy, the canonical URL and redirects, and the register's check 69. | **opus** |
| **P9** | W2.4 | The video slot's empty state. | sonnet |
| **P10** | W3.1, W3.2, W3.3 | After the console ships. **Unscheduled.** | **opus** |

## 7. Parallel execution

**Edges, read off each task's `Depends on` line, and every one of them is a task that genuinely waits.** W0.1 has none. W0.3 has none (its own lane; it writes only inside the Go module). W0.2 ← W0.1. **W0.4, W0.5 and W0.6 have none either** — they generate into `website/content/` and `internal/clidoc/`, which no other task writes, and W0.2's boundary test is a rule over the tree rather than a gate in front of them. W1.1 ← {W0.2, W0.4, W0.5, W0.6} (the build reads `content/`). W1.1 → {W1.2, W1.3}. W1.3 → {W1.4, W1.5, W1.6, W1.7, W1.8, W1.9, W1.10}, with W1.5 ← W0.3 (the figures), W1.8 ← W0.5 and W1.9 ← W0.6. {W1.2, W1.4…W1.10} → W1.11. W1.11 → W2.1 → W2.2. W0.3 → W2.3, and W2.2 → W2.3 (the check number). W1.3 → W2.4, which also follows W1.4 (below). W3.1, W3.2 and W3.3 are gated on the owner's own events rather than on a task.

**The file conflicts that order tasks rather than dependencies.** `website/src/components/Hero.astro` is written by W1.4 and later by W2.4 (the video slot lives in the hero), so W2.4 follows W1.4 even though it depends only on W1.3. `website/data/site.yaml` has **five writers, each of them after the previous one has merged**: W0.2 creates it, W2.2 sets the canonical URL, W2.4 sets `features.video`, W3.2 sets `analytics` and W3.3 sets the video's URL — and no two of them share a level. `docs/gcp-live-checklist.md` has **two writers in two levels**: W2.2 writes the summary-table rows for 69a/69b and the `## Check 69a`/`## Check 69b` sections, and **W2.3 writes `## Check 69c` with its row** in a later level (**L8**, with W2.4's L7 between them), which is also the answer to "who owns check 69" — W2.2 owns 69a and 69b, W2.3 owns 69c, and no level ever has two writers of the file. Everything else that looks shared runs one way only: `site.yaml` is **read** by W1.3, W1.6, W1.9 and W2.4; `tokens.css` is W1.2's and every page reads it; `website/content/*` is written by Part 0 and read by Part 1 — that direction is the point of the order, and it is why Part 0 is parallel with Part 1's *shell* and serial with Part 1's *content*.

**Levels.** A level's tasks can run concurrently — **no two tasks in a level write the same file** — and a level starts when the level before it has merged.

| Level | Tasks | PR | Reviewer | Files touched |
|---|---|---|---|---|
| **L0** | W0.1, **W0.3**, W0.4, W0.5, W0.6 | P1, P2, P3 | sonnet, **opus** (W0.3) | `website/data/install.yaml`; `internal/cli/docs_website_install_test.go`; `internal/stats/*`, `internal/cli/stats*.go`; `internal/clidoc/*`; `website/content/{cli-reference,security}.md`, `website/content/changelog.json`; `internal/cli/docs_website_{cliref,security,changelog}_test.go` |
| **L1** | W0.2 | P1 | sonnet | `website/data/site.yaml`, `internal/cli/docs_website_boundary_test.go` |
| **L2** | W1.1 | P4 | sonnet | `website/{package.json,package-lock.json,astro.config.mjs,tsconfig.json,README.md,.gitignore}` |
| **L3** | W1.2, W1.3 | P4 | sonnet | `website/src/styles/*`, `website/src/layouts/Base.astro`, `website/src/content.ts` |
| **L4** | W1.4, W1.5, W1.6, W1.7, W1.8, W1.9, W1.10 | P5, P6 | sonnet | `website/src/components/*`, `website/src/pages/*`, `website/data/faq.yaml`, `website/content/{costs,downloads}.json` |
| **L5** | W1.11 | P7 | **opus** | `website/test/{linkcheck,a11y,origins}.mjs`, `website/package.json`'s check script |
| **L6** | W2.1, W2.2 | P8 | **opus** | `docs/pending-workflows/website.yml`, `docs/pending-workflows/README.md`; `website/data/site.yaml`, `docs/gcp-live-checklist.md` |
| **L7** | W2.4 | P9 | sonnet | `website/src/components/Hero.astro`, `website/data/site.yaml`, `website/test/video.mjs` |
| **L8** | W2.3 | P8 | **opus** | `website/content/costs.json`, `website/src/pages/caps-and-costs.astro`, `docs/gcp-live-checklist.md` |
| **L9** | W3.1 | P10 | **opus** | `website/content/downloads.json`, `website/src/pages/console.astro`, `internal/cli/docs_website_boundary_test.go` |
| **L10** | W3.2 | P10 | **opus** | `website/data/site.yaml`, `website/src/layouts/Base.astro`, `website/test/origins.mjs` |
| **L11** | W3.3 | P10 | **opus** | `website/data/site.yaml`, `website/src/components/Hero.astro`, `website/content/` (the poster) |

**Part 3's three levels are not a schedule.** Each of L9, L10 and L11 waits on an owner event (the console's first release, D93's answer, the video), so they start in whatever order those arrive; the only pair that *cannot* run together is W3.2 and W3.3, because both write `site.yaml`.

**The critical path** is the longest chain, and its tail is the owner's, not the agent's:

> **W0.1 → W0.2 → W1.1 → W1.3 → W1.4 → W1.11 → W2.1 → W2.2 → check 69a/69b**
> and off that chain, the owner-gated rest: **W2.2 → W2.3 → check 69c**, **W2.2 → (the console's first release) → W3.1**, and W3.2/W3.3 after the owner's own answers.

**Eight agent tasks on the chain and three owner-run points at its end** (the live checks 69a/69b, the console's first release and the video). **Everything else is a side branch and can land in any order**: W0.3 (the aggregation) is the largest single piece of work in the plan and is on no path to the site at all — it blocks only W2.3, the published figures, which is the one page that can wait. W0.4, W0.5 and W0.6 are three independent generators, and a reader benefits from each the day it merges. W1.5 through W1.10 are six independent pages behind one shell.

**What the parallelism buys, stated honestly.** The plan's wall-clock is not the sum of its tasks: L0 is five agents, L4 is seven, and the only true serialization is the shell (W1.1 → W1.3) and the gates (W1.11, W2.1). The reason to cut it this way is not speed but **independence**: every level's PR is mergeable alone, and what the owner's unanswered decisions block is bounded and known — §7.1 states it row by row.

### 7.1 What can start, and under which answers

**One statement, and §13 and §15 point here rather than restating it.** The owner's direction on this work (2026-10-16) is to **start as soon as we can**; that is a direction and not an approval — no row in the reconciliation's §4 is answered — so each wave needs the owner's go on the decisions it depends on. Concretely:

- **Where the site lives decides Part 0's paths, and nothing else.** Every Part 0 task except W0.3 writes under `website/`, and that location is D90's question, so **D90's answer sets the output path** — as the recommendation (*this repository, under `website/`*) it is the paths §5 lists, and elsewhere it is the same tasks under a different prefix.
- **The realistic first wave, all at once: W0.1, W0.2, W0.4, W0.5 and W0.6.** They write six files that share nothing, they depend on nothing (W0.2 on W0.1 alone), and each is worth merging on its own — the install facts and the rule that pins them, the site's facts and the boundary test, the CLI reference, the rendered `SECURITY.md`, the changelog's source.
- **W0.3 joins that wave as soon as D96 is answered** (or the owner accepts Global Constraint 10's strict reading): it writes only inside the Go module, so it needs no path from D90 and no other decision, and it is the one task in the plan that a decision row can hold up on its own.
- **Part 1 needs no answer beyond the defaults.** The palette (D104) ships as three files with the split one in force, the tagline (D102) is `README.md`'s own line, and analytics (D93) is off — each a one-line change later. It does need **all** of Part 0's content tasks (W0.2, W0.4, W0.5, W0.6), because the build reads `content/`.
- **Part 2 needs D91 and D92** — a host and a domain for the deploy step and the canonical URL — and cannot start before them. **Part 3 needs the owner's own events**, not a decision about the site.
- **Passing the gate at §15 is the go for a wave, not a blanket approval**, and it does not answer a row: §15 lists what the owner has to have decided for each wave to be startable.

## 8. Part 0 — The facts the site renders

### Task W0.1: The install facts, and the test that holds them to the repository

**Files:** `website/data/install.yaml` (new), `internal/cli/docs_website_install_test.go` (new).

**Steps.**
1. Write `website/data/install.yaml` with one entry per install path, each carrying a `source:` field naming the file its text came from, and **no command that is not in `README.md` or `docs/release.md`**. The five entries are what the repository really ships, not what a site would prefer to ship:
   - **Homebrew** — `brew install dimipaun/tap/fugaro`, the cask `README.md` marks *"(tested on macOS)"*, resolving through `dimipaun/homebrew-tap`; `source: README.md`.
   - **The release archive** — the sentence `README.md` actually prints: download an archive from the **Releases page** (`https://github.com/dimipaun/fugaro/releases`), *darwin and linux, amd64 and arm64*, and verify it with cosign. The site **links the Releases page**; it does not build a download URL, because `releases/download/` is exactly the string W0.2's test refuses and the archive's URL carries a version the site does not know. The name template — `fugaro_<version>_<os>_<arch>.tar.gz` — is recorded for the reader, pinned to `.goreleaser.yaml`; `source: README.md`.
   - **cosign verification** — the block `docs/release.md` `## Verifying a release` prints, verbatim: `V=X.Y.Z`; `gh release download v$V -R dimipaun/fugaro -p checksums.txt -p checksums.txt.sigstore.json -p "fugaro_${V}_$(uname -s | tr A-Z a-z)_*"`; `cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json --certificate-identity "https://github.com/dimipaun/fugaro/.github/workflows/release.yml@refs/tags/v$V" --certificate-oidc-issuer https://token.actions.githubusercontent.com`; and the `shasum -a 256 -c -` / `sha256sum -c -` check against `checksums.txt`; `source: docs/release.md`.
   - **`go install`** — `go install github.com/dimipaun/fugaro/cmd/fugaro@latest`, carrying `README.md`'s own note that it **is for development only** (it builds a `dev` version with no release images and no plugin pin); `source: README.md`.
   - **The plugin** — the route `README.md`'s `## Getting started` prints and no other: `fugaro init` wires the Fugaro plugin into the checkout's `.claude/settings.json`, `/fugaro:setup` writes `fugaro.yaml`, a teammate who opens and trusts the folder gets the plugin installed, and when the `/fugaro:` skills are not listed the repair is `fugaro upgrade --local` (which installs it through the `claude` CLI). **The marketplace commands** (`/plugin marketplace add dimipaun/fugaro`, `/plugin install fugaro@fugaro`, `claude plugin install fugaro@fugaro --scope project`) are what a person runs inside Claude Code and are documented in `docs/design/upgrade.md` and `docs/design/m11-setup-and-skills.md` — **not** in `README.md` — so the YAML leaves them out rather than widening the test's source list to internal design documents.
2. Write the test so that it reads **the repository**, not the YAML's own prose: the two files it reads are the two the product's own install text lives in. Every command string in the YAML must appear verbatim in `README.md`'s `## Getting started` **or** in `docs/release.md` — **a command neither file carries fails the test, which is what keeps an invented instruction (a `curl | tar` one-liner nobody ships, a marketplace command a reader cannot run) off the site**; the OS/arch matrix must equal `.goreleaser.yaml`'s `builds[0].goos` × `builds[0].goarch`; the archive name template must equal `archives[0].name_template`; the Homebrew line must equal the `homebrew_casks[0].repository` owner and the cask name; and the `go install` entry must carry the "development only" marker that `README.md` carries.
3. Add the negative half: the YAML must not name a platform that `.goreleaser.yaml` does not build (**this is F20's gate** — a future Windows build is the only way this test ever changes its answer), must not carry a version number anywhere (the site renders `latest`, or the release's own tag), and must contain no `releases/download/` URL — the archive path is the Releases page, which is also the rule W0.2 enforces for the console.
4. Run the red step, then the test, then the docs suite.

**Tests:** `TestWebsiteInstallFactsMatchTheRepository`, `TestWebsiteInstallFactsNameNoUnbuiltPlatform`, `TestWebsiteInstallFactsCarryNoVersion`. All three read `website/data/install.yaml` plus `README.md`, `docs/release.md`, `.goreleaser.yaml`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Website|Docs|Lint' -count=1`

**Depends on:** nothing. **Commit:** `feat(website): one install source, pinned to the README, the release guide and goreleaser`

**Red step.** Write the three tests first, with `website/data/install.yaml` absent. `Expected: FAIL` — `open website/data/install.yaml: no such file or directory`.

### Task W0.2: The site's facts, and the console's boundary test for the site

**Files:** `website/data/site.yaml` (new), `internal/cli/docs_website_boundary_test.go` (new).

**Steps.**
1. Write `website/data/site.yaml` with the values the pages read and nobody hard-codes: `canonical_url` (a placeholder, per D92), `secondary_domain`, `analytics` (an object whose `enabled` is `false` until D93 is answered), `features: {console_downloads: false, video: false}` (D95, D107), `min_runs` (D97's N), and `allowed_hosts` (the origin list W1.11 enforces, seeded from the links the site actually makes: `github.com`, `raw.githubusercontent.com` and the site's own canonical origin).
2. Write `docs_website_boundary_test.go` asserting the three properties `TestConsoleIsOutsideTheGoModule` asserts for the console: no file under `website/` is imported by the Go module, `go.mod` names no dependency that exists only for the site, and no Go file embeds a file from `website/` — so the site stays a separate artifact whose only coupling to the module is a test.
3. Add the flag test: while `features.console_downloads` is `false`, no file under `website/` may contain a `github.com/dimipaun/fugaro/releases/download/` URL — the mechanical form of "never link an unsigned or unreleased console" (F25, Global Constraint 9).
4. Run the red step, then the suite.

**Tests:** `TestWebsiteIsOutsideTheGoModule`, `TestGoModHasNoWebsiteDependency`, `TestNoGoFileEmbedsFromWebsite`, `TestWebsiteLinksNoConsoleWhileTheFlagIsOff`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Website|Docs|Lint' -count=1`

**Depends on:** W0.1 (same PR group; the boundary test's file walk assumes `website/data/` exists). **Commit:** `feat(website): the site's facts, the boundary test, and the console-link rule`

**Red step.** Write the four tests first, with `website/` absent. `Expected: FAIL` — `open website/data/site.yaml: no such file or directory`.

### Task W0.3: `fugaro stats runs` — the owner-run aggregation — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/stats/stats.go` (new), `internal/stats/stats_test.go` (new), `internal/cli/stats.go` (new), `internal/cli/stats_test.go` (new).

**Steps.**
1. In `internal/stats`, implement the statistics over `[]runstore.Record` (or over the narrower view the CLI passes in): **percentiles** (min, median, p90) of wall-clock duration and of `Cost.TotalUSD`, grouped by **recipe**, over a window; the **minimum-sample rule** (`MinRuns`, default 20) which suppresses a group rather than publishing it; and the count of runs whose compute cost is an **estimate** rather than a measurement (`Cost.ComputeEstimated`/`ComputeSource`), which the caller reports rather than folds in. No mean is published alone: the struct has no field for one. **A record whose recipe is not one the tree ships (`internal/recipe`) is dropped rather than grouped**, so the output never carries a group key no reader can look up.
2. In `internal/cli`, add `newStatsCmd()` with one subcommand, `runs`: `--project` (**required, with no default** — the command refuses to run until exactly one installation is named, the sandbox included, and there is **no `--all`**), `--since` (default 90d, the runs lifecycle's own age), `--min-runs`, `--json`. It reads the run records **read-only** through the existing store client, and it **refuses to run when it detects a coding-agent session** (the marker the repository's other owner-only paths use).
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

### Task W0.6: The changelog's source, generated from `docs/releases/`

**Files:** `website/content/changelog.json` (generated), `internal/cli/docs_website_changelog_test.go` (new), generator `internal/clidoc/changelog.go`.

**Steps.**
1. Generate a JSON array from `docs/releases/vX.Y.Z.md`: `{tag, version, released, highlights: [lines], for_operators: [lines]}` — the **tag and the version from the filename**, the bullets and the optional `### For operators` block as written, and nothing invented. **There is no `date` field, and that is the design rather than an omission.** The release files carry neither a title nor a date (`docs/releases/README.md`: "Plain Markdown, no title", and no date appears in any of the eleven files), the file's own first line has none, the site's build is network-free (W1.1) so it cannot ask the releases API, and a date taken from a git tag is a date that **does not exist for the newest entry** — `docs/releases/README.md` requires the Highlights file to be committed to `main` **before** `scripts/release.sh` runs, which is the first entry a reader sees. So the page prints the tag and links the entry to its GitHub release, where the date is GitHub's, and the repository holds no second copy of it.
2. **`released` is the one field that reads the checkout's tags, and no test pins it.** It is `true` when `git tag --list vX.Y.Z` finds the tag in the checkout the generator ran in and `false` otherwise; a **shallow CI checkout has no tags at all**, so every entry there is `false`, which is the page's **"not yet released"** state — the honest rendering for a file that is on `main` before its tag. **No test reads a tag and no test reads a commit date**, so this generator cannot hold a release or fail in CI for a reason that depends on clone depth or on the tag's existence.
3. Write the tests, none of which reads a tag: the set of entries equals the set of `v*.md` files in `docs/releases/` (so a release cannot land without the entry following, and an entry cannot name a file that is gone); the entries are ordered newest first **by version parsed from the filename**, not from tags; every `highlights` and `for_operators` line appears in its file and no entry carries a sentence its file does not (the same no-invention rule as W0.5); and the committed file equals a fresh generation **except for `released`** — the one field that depends on the checkout, compared for shape only (a boolean) — so the test catches a stale or edited entry and still passes in a shallow clone. **`docs/releases/README.md` is not a release** and is skipped by name, not by pattern.
4. Add the drift test the site's page will lean on: `README.md` still carries no changelog section, so the page remains the only index of those files (F44) — and if that ever changes, this test is where the duplicate is noticed.
5. Run the red step, then the suite.

**Tests:** `TestWebsiteChangelogCoversEveryReleaseFile`, `TestWebsiteChangelogListsNoReleaseThatIsNotAFile`, `TestWebsiteChangelogAddsNoSentenceOfItsOwn`, `TestWebsiteChangelogIsCurrent`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Website|Docs|Lint' -count=1`

**Depends on:** nothing. **Commit:** `feat(website): generate the changelog from docs/releases, with a test that the sets match`

**Red step.** Write the set-equality test first, with the JSON absent. `Expected: FAIL` — `changelog.json lists 0 releases, docs/releases/ holds 11`.

## 9. Part 1 — The site

### Task W1.1: The site's shell, toolchain and build

**Files:** `website/package.json`, `website/package-lock.json`, `website/astro.config.mjs`, `website/tsconfig.json`, `website/README.md`, `website/.gitignore` (new).

**Steps.**
1. Scaffold the Astro app with **`output: 'static'`** and **no integrations beyond what an island needs** (the spec's "static-first; React islands only where interactivity needs it" read as a constraint — F56). Pin the Node version in `package.json`'s `engines` and in the README, and commit the lockfile.
2. Make the build **network-free after install**: no Google Fonts, no remote images, no integration that fetches at build time, and fonts self-hosted under `website/public/fonts/` (D106, Global Constraint 8). The README says which command installs and which builds, and that the build must succeed with the network off.
3. Write the site's own README: what is generated and must not be edited (`content/*`), the regeneration commands from Part 0, the check commands, and the rule that a page renders a fact rather than restating it.
4. Verify the build produces the pages and that `npm ci && npm run build` is reproducible from a clean checkout, then commit.

**Tests:** the build itself (it fails on a missing generated file, which is W1.3's pipeline), plus a README test asserting the regeneration commands it names are the ones Part 0 added.

**Command:** `cd website && npm ci && npm run build` (the site's own gate; the Go gates stay `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`)

**Depends on:** W0.2, W0.4, W0.5, W0.6 (the build reads `content/`). **Commit:** `feat(website): the Astro shell, a pinned toolchain, and a build that needs no network`

**Red step.** Build with `content/` absent and with the content pipeline not yet written. `Expected: FAIL` — the page renders an empty install block instead of failing (the defect W1.3 exists to remove), and the README names no regeneration command.

### Task W1.2: Tokens and three palettes

**Files:** `website/src/styles/tokens.css`, `palette-violet.css`, `palette-orange.css`, `palette-split.css` (new).

**Steps.**
1. Write `tokens.css` as the semantic layer (`--ground`, `--surface`, `--text`, `--text-muted`, `--accent`, `--accent-on-dark`, `--border`, `--warn`, plus spacing, radius and type scale) with **no colour literal in it**: every colour comes from the active palette file.
2. Write the three palettes of D104 as the three candidate files, each meeting WCAG AA against the dark ground, each with the hero's gradient stops named as tokens so the contrast test can check text over them (U5).
3. Write the contrast check into `website/test/a11y.mjs` (W1.11 owns the file; this task owns its first case): every text/background pair in the token set meets AA, and the gradient stops are pairs too.
4. The palette in force is selected by **one line** in `site.yaml` (or one import in `tokens.css`), so the owner's answer to D104 is a one-line change and nothing else.

**Tests:** `a11y.mjs`'s contrast case over every token pair and every palette; a test that no palette file is missing a token the others define (so a new token cannot be added to one palette only).

**Command:** `cd website && npm run check`

**Depends on:** W1.1. **Commit:** `feat(website): design tokens and three palettes, each passing contrast in a test`

**Red step.** Run the contrast check against a deliberately failing pair. `Expected: FAIL` — `--text-muted on --surface is 3.1:1, needs 4.5:1`.

### Task W1.3: The layout and the generated-content pipeline

**Files:** `website/src/layouts/Base.astro`, `website/src/content.ts` (new).

**Steps.**
1. Write the base layout: `<head>` with the canonical URL **from `site.yaml`** (never hard-coded), the self-hosted font preload, the sitemap and OpenGraph tags, and the analytics script **only when `site.yaml`'s `analytics.enabled` is true** and **only from an allowed origin** (D93, D106).
2. Write the content pipeline: a single module that loads `content/*` and **throws** when a file is missing or its generated-file header is absent — so a build cannot silently render an empty install block, an empty changelog or an ungenerated security page.
3. Add the reduced-motion base: a global `@media (prefers-reduced-motion: reduce)` rule that neutralises the animation utility classes the components use, written here rather than in each component, so a new component cannot forget it (D105).
4. Assert the pipeline's failure mode in the build (a missing `content/` file fails the build with the file's name in the message).

**Tests:** the build fails when a `content/` file is removed (a test that deletes it in a temp copy); a test that the canonical URL appears only as a `site.yaml` read; a test that the analytics tag is absent while the flag is false.

**Command:** `cd website && npm run check`

**Depends on:** W1.1. **Commit:** `feat(website): the base layout, a content pipeline that fails loudly, and the reduced-motion base`

**Red step.** Remove `website/content/changelog.json` and build. `Expected: FAIL` — the build succeeds and renders an empty changelog (the defect this task removes).

### Task W1.4: The hero and the architecture diagram

**Files:** `website/src/components/Hero.astro`, `Terminal.astro`, `ArchitectureDiagram.astro`, `website/src/pages/index.astro`, `how-it-works.astro` (new).

**Steps.**
1. The hero: the **tagline exactly as `README.md` writes it** (F48 — a test asserts the string is byte-identical to the line under `README.md`'s `# Fugaro` title, which is the README's own tagline line, so the site and the README cannot drift), one sentence of what Fugaro is, the two install commands rendered **from `install.yaml`** (never typed into the component), and the video slot (W2.4) empty.
2. The animated terminal: a typing animation of a real `fugaro` invocation **taken from the CLI reference** (W0.4's file), in CSS/SVG or a small first-party island, with a byte budget (W1.11 enforces it), and a reduced-motion path that renders the finished frame.
3. The architecture diagram: **the spec's five stage names** — local dispatch, cloud job, cache, security boundary, PR or artifact collected back (`spec.md` §2) — as an SVG, with each stage's one-sentence caption **taken from the repository rather than written here**: `README.md`'s `## How it works` five steps ("Start", "Sync", "Loop", "Push and open the pull request", "Write caches back and exit") and its ASCII diagram are the sources, and the two names the README does not use ("security boundary", "PR or artifact collected back") are the page's map to those steps rather than quotations. Animated on scroll, with a reduced-motion path.
4. Nothing on either page names an unshipped feature (F50): a test greps the built HTML for the four held-back claims (console download, WSL support, "~2 h", "ballpark ranges") and fails.

**Tests:** tagline byte-identity against the tagline line under `README.md`'s `# Fugaro` title; the diagram carries the spec's five names and no sixth; every caption on the diagram appears in `README.md`'s `## How it works` or in `docs/design/v1.md` (the two names the README does not use are asserted to be labels only, with no claim attributed to the README); the install commands in the hero equal `install.yaml`'s; the built HTML names none of the four held-back claims; the terminal's animation has a reduced-motion end state.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the hero, the terminal and the architecture diagram, from the repository's own words`

**Red step.** Write the tagline test against a paraphrased tagline. `Expected: FAIL` — `hero tagline differs from the tagline line under README.md's "# Fugaro" title`.

### Task W1.5: Getting started, and the caps-and-costs guide

**Files:** `website/src/pages/getting-started.astro`, `caps-and-costs.astro`, `website/src/components/CapsCosts.astro` (new), `website/content/costs.json` (published, from W0.3).

**Steps.**
1. Getting started renders `install.yaml` per platform, with the **plugin path** as a first-class step (F22), the cosign verification inline, and the platform list showing **macOS and Linux supported, Windows untested** with the WSL note D94 settles — the wording pinned by W0.1's test.
2. The caps guide explains what the repository actually has: the project-global caps, the default repo caps, the per-repo caps, the per-run cap and max-reserve, the `observe`/`enforce` modes, the kill switches — each with the `fugaro budget` command that sets it (from W0.4's reference, so the flags cannot drift) — and what a cap *is* (a ceiling; `enforce` halts).
3. The costs section renders `content/costs.json` (W0.3's output): percentiles per recipe, each figure with its window and sample size, groups below `min_runs` absent rather than approximated, and D97's **"not a promise"** line present as a rendered element (a test asserts it).
4. The page carries **no figure the JSON does not carry** — a test that every number in the built page appears in `costs.json`, which is the mechanical form of "publish the measurement, not the anecdote" and the reason the "~2 h" sentence never appears here.

**Tests:** every number on the page appears in `costs.json`; the "not a promise" line is present; a group below `min_runs` is absent from the page; the `fugaro budget` invocations match the CLI reference.

**Command:** `cd website && npm run check`

**Depends on:** W1.3, W0.3 (for `costs.json`; the page renders a "not enough runs yet" state while the file is absent, which is the honest state on day one). **Commit:** `feat(website): getting started, and a caps-and-costs guide that only prints measurements`

**Red step.** Write the "every number appears in costs.json" test with a hard-coded "~2 h" in the page. `Expected: FAIL` — `page states "~2 h", which costs.json does not carry`.

### Task W1.6: Console downloads, as a placeholder with no link

**Files:** `website/src/pages/console.astro`, `website/src/components/Downloads.astro`, `website/content/downloads.json` (new, `available: false`).

**Steps.**
1. Render the section from `downloads.json`: while `available` is false, the page says what the console is (from `docs/design/desktop-console.md` D2/D4/D11 in the site's own words, since that document is internal), that it is **not released yet**, and what it will be — and renders **no link and no button**.
2. Wire the rule W0.2's test enforces: no `releases/download/` URL anywhere under `website/` while the flag is off, and when it is on, every URL must point at a release asset of a tag that exists (the test W3.1 extends).
3. State the console's own guarantees that are decided and user-facing: it is **strictly read-only** (D2), it has no telemetry (F17), and its artifact publishes from the CLI's own tag (D11) — each sentence traceable to a decision, with the site not inventing a distribution.

**Tests:** no download URL in the built HTML while the flag is false; no button or link element in the downloads section; the page names the console's read-only and no-telemetry properties.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the console section, as a placeholder that renders no link`

**Red step.** Write the "no download URL" test with a hard-coded placeholder link. `Expected: FAIL` — `console page links releases/download/ while features.console_downloads is false`.

### Task W1.7: The docs hub

**Files:** `website/src/pages/docs.astro` (new).

**Steps.**
1. Build the hub: `README.md`'s `## Getting started` (the quick start), `docs/gcp-setup.md`, `docs/recipes.md`, `docs/project-layer.md`, `docs/multi-model.md`, `docs/git-providers.md`, and the generated CLI reference — each entry one line saying what the document answers, linking to it on GitHub **by URL built from `site.yaml`'s repository**, never by a copied path that can rot.
2. Link, never copy (F37, D98): a test asserts the page contains **no paragraph of more than one sentence that appears verbatim in any document under `docs/`** — the mechanical form of "this is a hub".
3. The CLI reference is rendered from `content/cli-reference.md` (W0.4), so a new command appears here the day it lands.

**Tests:** every link target exists in the repository (the file-level half of W1.11's link check, run against the GitHub URLs); no copied prose; the CLI reference's command count equals the generated file's.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the docs hub, linking the guides and rendering the generated CLI reference`

**Red step.** Write the no-copied-prose test with a pasted paragraph from `docs/recipes.md`. `Expected: FAIL` — `docs page repeats a sentence from docs/recipes.md`.

### Task W1.8: The security page

**Files:** `website/src/pages/security.astro` (new).

**Steps.**
1. Render `content/security.md` (W0.5) inside the site's typography, with the document's headings as anchors and a table of contents generated from them — and with **no editing**: the page adds a lead sentence and nothing else, and W0.5's no-invention test covers the file rather than the page, so the page's lead sentence is asserted to be a link line rather than a claim.
2. Keep every anchor the repository's cross-references name (F40), asserted by re-running W0.5's anchor test over the **built page** as well as the file — the anchors survive the renderer or the build fails.
3. Add the reporting pointer: how to report a vulnerability, taken from the document's own `## Reporting a vulnerability` section, with no paraphrase.

**Tests:** the built page's heading set equals `SECURITY.md`'s; every anchor the repository references exists in the built HTML; the page's non-document text is the lead line only.

**Command:** `cd website && npm run check`

**Depends on:** W1.3, W0.5. **Commit:** `feat(website): the security page, rendered from SECURITY.md and nothing else`

**Red step.** Write the heading-set test against a page whose headings were "improved". `Expected: FAIL` — `built page heading "What we protect" is not in SECURITY.md`.

### Task W1.9: The changelog page

**Files:** `website/src/pages/changelog.astro`, `website/src/components/Changelog.astro` (new).

**Steps.**
1. Render `content/changelog.json` (W0.6): one entry per release, newest first, with the tag, the bullets and the `### For operators` block where the file has one — **no date** (the file carries none and the repository holds none; the entry links to its GitHub release, which is where the date is stated), and an entry whose `released` is false rendered as **not yet released** rather than as a release that exists.
2. Render **nothing of its own**: no summary, no "what's new" prose, no editorial. A test asserts every sentence on the page appears in `docs/releases/` (the page-level form of W0.6's file-level test).
3. The entry count equals the JSON's, and the newest entry's tag equals the newest file's — the visible half of the drift test.

**Tests:** the page's release set equals `docs/releases/`'s; every sentence on the page is in a release file; the newest entry is the newest file.

**Command:** `cd website && npm run check`

**Depends on:** W1.3, W0.6. **Commit:** `feat(website): the changelog page, rendered from the release notes`

**Red step.** Write the set-equality test with the newest release missing from the page. `Expected: FAIL` — `page omits v0.8.0, docs/releases/ holds it`.

### Task W1.10: The FAQ and the community page

**Files:** `website/src/pages/faq.astro`, `community.astro` (new).

**Steps.**
1. The FAQ answers what the repository can prove, from the repository: what Fugaro is and is not (a fleet of ephemeral workers, not an IDE agent), where the code runs (the operator's own GCP project, one Cloud Run job execution per task), what a run costs and what stops it (the caps, with the guide's link), what happens to a failed run (a draft pull request; Fugaro never merges), what Fugaro can never change (the workflow files, with the reason), and what the security model does not promise (a link to the security page, **not** a summary).
2. **Every comparison row carries a source and a date or it is not written** (D103, F51–F52). The page's comparison section is rendered from a data file (`website/data/faq.yaml`) whose comparison entries **require** a `source:` and a `date:` field, enforced by a test — so a comparison cannot be added without provenance, and the section renders empty until the owner supplies rows.
3. The community page: the repository, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, the issue templates, the licence stated inline (**MIT**), and the CI description either accurate (`test`, `rules` and `terraform` on pushes to `main` and on demand; no CI on pull requests) or absent (F46, D101).
4. Nothing on either page restates the contributing guide's rules (F47): the same no-copied-prose test as W1.7, extended to `CONTRIBUTING.md` and `SECURITY.md`.

**Tests:** every comparison entry has a source and a date, and the section is empty without them; the CI description matches `ci.yml`'s `on:` block; no copied prose from `CONTRIBUTING.md`; the licence is named.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the FAQ with provenance, and a community page that links rather than restates`

**Red step.** Write the provenance test with a comparison row carrying no source. `Expected: FAIL` — `comparison row "Cursor" carries no source`.

### Task W1.11: The gates — links, anchors, accessibility, origins, budget

**Files:** `website/test/linkcheck.mjs`, `website/test/a11y.mjs` (W1.2 added its first case), `website/test/origins.mjs`, `website/package.json`'s check script (new).

**Steps.**
1. **Link and anchor check** over the built HTML: every internal link resolves to a built page or a `docs/` file that exists in the repository, every `#anchor` resolves to an `id` in the target, and every external link's host is on `site.yaml`'s `allowed_hosts` (F59).
2. **Origins check** over the built HTML: the `script`, `link[rel=stylesheet]`, `link[rel=preload]`, `img`, `iframe` and `font` origins are all first-party or on the allowed list — the mechanical form of "nothing phones home" (F55, D106, Global Constraint 8). A Google Fonts URL, a CDN icon set or an embedded player fails here.
3. **Accessibility check**: the contrast pairs (W1.2), a `prefers-reduced-motion` path for every animated component (asserted by checking that each animation class is covered by the global reduce rule), every interactive element keyboard-reachable and focus-visible, a landmark structure and one `h1` per page, and alt text on every image.
4. **Budget check**: the JavaScript shipped per page is under the stated kilobyte budget, and the total page weight is under its budget — the numbers written into `website/README.md` so the budget is a fact rather than an aspiration.
5. Wire all four into `npm run check` — the script runs **every `website/test/*.mjs`**, so a check a later task adds (W2.4's video slot, W3.2's analytics origin) is picked up without touching this task's files — and note in the README that **Lighthouse is the owner-run check** (D105's targets) because there is no CI on pull requests and the repository's tests are hermetic (F3, F58).

**Tests:** the four checks themselves, each with a deliberate failing fixture asserted in the same run (a broken anchor, a `fonts.googleapis.com` link, a `motion-safe`-only animation, an over-budget island).

**Command:** `cd website && npm run check`

**Depends on:** W1.2, W1.4–W1.10. **Commit:** `feat(website): the gates — links, anchors, origins, accessibility and the byte budget`

**Red step.** Run the origin check against a page carrying a Google Fonts link. `Expected: FAIL` — `fonts.googleapis.com is not on the allowed-host list`.

## 10. Part 2 — Hosting, deploy and the live checks

### Task W2.1: The deploy workflow, staged for the owner — SECURITY-SENSITIVE

**Files:** `docs/pending-workflows/website.yml` (new), `docs/pending-workflows/README.md` (updated).

**Steps.**
1. Write the workflow **for the owner to copy into `.github/workflows/`**, following the procedure `docs/pending-workflows/README.md` describes (including the note that the staged file is removed in the same PR that copies it). It builds the site from `website/`, runs the site's own checks, and deploys to the host D91 chooses — with the host's deploy step written for **whichever of D91's answers the owner picks**, marked so that exactly one branch is active and the others are commented out with the reason.
2. The credential rule (F14): the deploy token is a **GitHub Actions repository secret** named in the workflow and in the staged README, with a comment stating what it must never be — an installation's service account, a credential a Fugaro run holds, or a token with write access to anything but the site's own project. The workflow runs **no Fugaro run** and is not invoked by any Fugaro command. **It is written for a public repository, which is what this one is** (the module, the images and the release archives are all published): it declares an explicit **least-privilege `permissions:` block** — `contents: read` plus the host's deploy scope and nothing else, never `workflows: write` — the deploy job runs **only on `main`**, and the workflow is never triggered by `pull_request_target`, so a pull request from a fork (which gets no secrets at all) cannot reach the credential and cannot run a deploy step.
3. The trigger is the site's own: a path filter on `website/**` on pushes to `main`, plus `workflow_dispatch` — **no release trigger and no tag**, which is D109's decoupling made mechanical.
4. Update `docs/pending-workflows/README.md` — its staged-file list gains the site's workflow, and **its stale "One file is pending: `images.yml`" line is corrected in the same edit** (that file was copied in and removed in `6b57ba9`; the directory holds only the README today, F62) — and the copy procedure, and verify that the workflow's YAML parses (`actionlint` if present, otherwise a YAML parse test) — the file cannot be run, so parsing is the only hermetic check available.
5. **Nothing under `.github/workflows/` is touched by this task** (Global Constraint 2). The copy is the owner's step, recorded in the staged README's checklist.

**Tests:** the staged YAML parses; a test asserts no `.github/workflows/` file was added or modified by the task (a diff-scoped check, as the sibling plans use); the workflow names no credential other than through `secrets.`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` plus the YAML parse check

**Depends on:** W1.11. **Commit:** `feat(website): stage the deploy workflow for the owner to copy, with no credential a run can hold`

**Red step.** Write the "no credential outside `secrets.`" test against a workflow carrying an inline token. `Expected: FAIL` — `website.yml carries a literal token`.

### Task W2.2: The canonical URL, the redirects, and check 69a/69b in the register

**Files:** `website/data/site.yaml` (the canonical URL and the secondary domain), `docs/gcp-live-checklist.md` (the row and the section), the host's redirect configuration (wherever D91 puts it: a `_redirects` file for Cloudflare Pages, a CNAME plus a Pages setting for GitHub Pages, a `firebase.json` `redirects` block **only if the owner overrode D91 — and then only against a separate Firebase project created for the site, never the installation's**, because *"one Firebase project serves one installation"* and a public site deployed into an installation's project is the failure D91 rejects (F13).

**Steps.**
1. Set the canonical URL and the secondary domain in `site.yaml`, which every absolute link, the sitemap, the OpenGraph tag and the canonical `<link>` read (W1.3) — so the domain change is one line and a rebuild, and **the registration itself is not a task** (D92, F15).
2. Write the redirect from the secondary domain to the canonical one **in the host's own mechanism**, with the choice recorded in the site's README.
3. Re-read `docs/gcp-live-checklist.md` **on `origin/main` first** (U6): the highest written section, the summary table, and any reservation in `docs/plans/` or `docs/design/` that claims 69. **If 69 is taken, stop and take the owner's reallocation** — never renumber, never reuse (the register's own rule).
4. Write **check 69a** (the site is live and honest) and **check 69b** (nothing phones home) as rows in the register's summary table **and** as `## Check 69a` / `## Check 69b` sections, in the register's own format — owner-run, each step a command or an observation, each with its pass condition, and each naming what it would look like to fail.
5. Confirm that the two checks are the **only** thing in this plan that needs a live site, and that everything else ran hermetically (F58, F60).

**Tests:** the register's own docs test still passes; a test asserting the two new sections exist and are referenced from the summary table (the shape the register's existing entries use); the canonical URL appears in `site.yaml` only.

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

**Files:** `website/src/components/Hero.astro` (the slot), `website/data/site.yaml`'s `features.video`, `website/test/video.mjs` (new — W1.11's `npm run check` runs every `test/*.mjs`, so this file is picked up without editing W1.11's).

**Steps.**
1. Render the slot only when `features.video` is true; while it is false, the hero carries **no player, no iframe, no poster, no play button** — a test asserts all four are absent from the built HTML (F57, D107).
2. Write the fill-in path: when the owner produces the video, `site.yaml` names its URL, and the origins check (W1.11) requires its host to be on the allowed list — so an embedded third-party player cannot arrive without a decision.
3. Add the poster-image rule: the poster is a file in the artifact, and the player has no autoplay with sound (D107).

**Tests:** no `iframe`, `video` or play button in the built hero while the flag is false; with the flag true, the video's host is on the allowed list and the poster is a local file.

**Command:** `cd website && npm run check`

**Depends on:** W1.3. **Commit:** `feat(website): the video slot, empty until the console has something to show`

**Red step.** Write the no-iframe test with a placeholder embed. `Expected: FAIL` — `hero embeds youtube.com while features.video is false`.

## 11. Part 3 — After the console ships (unscheduled)

### Task W3.1: The console downloads become real

**Files:** `website/content/downloads.json` (`available: true` with per-OS assets), `website/src/pages/console.astro`, W0.2's link test (extended).

**Steps.** Fill `downloads.json` from the console's own release artifacts, with per-OS entries and their **signature and notarisation status** (F24, D95); extend W0.2's test so that when `available` is true every URL points at a release asset of a **tag that exists** and is a **signed** artifact (the rule `docs/release.md` `## Verifying a release` states for the CLI, applied to the console); keep the read-only and no-telemetry sentences. **Depends on the console's first release — an owner decision to schedule.**

**Tests:** every download URL resolves to an existing release tag; every asset is signed; the page still claims no more than D2/D4/D11 say.

**Command:** `cd website && npm run check` **Commit:** `feat(website): the console downloads, once the console has a signed release` **Red step.** Point a URL at a tag that does not exist. `Expected: FAIL` — `console download points at tag v9.9.9, which does not exist`.

### Task W3.2: Analytics, if D93 says yes

**Files:** `website/data/site.yaml` (`analytics`), `website/src/layouts/Base.astro`, `website/test/origins.mjs` (the allowed list).

**Steps.** Turn on exactly the tool D93 names, loaded from the origin D93 names (first-party or self-hosted by preference); add that origin to `allowed_hosts` **and nowhere else**; state in one sentence on the page what is measured; confirm no cookie and no consent banner is needed by the tool's own configuration, and record that in the site's README. **Depends on D93.**

**Tests:** exactly one analytics origin in the built HTML; no cookie-setting script; the disclosure sentence is present; with the flag off, no analytics request at all.

**Command:** `cd website && npm run check` **Commit:** `feat(website): the analytics decision, applied and disclosed` **Red step.** Build with the flag true and no allowed origin. `Expected: FAIL` — `analytics origin is not on the allowed-host list`.

### Task W3.3: The explainer video

**Files:** `website/data/site.yaml` (`features.video`, the URL), `website/src/components/Hero.astro`, `website/content/` (the poster).

**Steps.** Embed the owner's 30–45 s video from an origin on the allowed list or self-hosted, with a local poster, no autoplay with sound, a transcript link, and a reduced-motion path that shows the poster rather than an autoplaying animation. **Depends on the owner producing the video** (spec's own open item).

**Tests:** the host is allowed; the poster is a local file; no autoplay with sound; a transcript link exists.

**Command:** `cd website && npm run check` **Commit:** `feat(website): the explainer video, embedded from an allowed origin` **Red step.** Embed from a host not on the list. `Expected: FAIL` — `video host vimeo.com is not on the allowed-host list`.

## 12. Cross-task dependencies

| Task | Needs | Because |
|---|---|---|
| W0.2 | W0.1 | Same PR; the boundary walk assumes `website/data/` exists |
| W0.4, W0.5, W0.6 | nothing | They write `internal/clidoc/` and `website/content/`, which no other task writes; W0.2's boundary test is a rule over the tree, not a gate in front of them |
| W1.1 | W0.2, W0.4–W0.6 | The build reads `content/`; the pipeline (W1.3) fails on a missing file |
| W1.2, W1.3 | W1.1 | Tokens and layout need the toolchain |
| W1.4–W1.10 | W1.3 | Every page needs the layout and the content pipeline |
| W1.5 | W0.3 | The caps page renders `costs.json` |
| W1.8 | W0.5 | The security page renders the generated file |
| W1.9 | W0.6 | The changelog page renders the generated file |
| W1.11 | W1.2, W1.4–W1.10 | The gates check the built pages, and the contrast case needs the tokens |
| W2.1 | W1.11 | The workflow runs the site's own checks |
| W2.2 | W2.1 | The live checks follow the deploy |
| W2.3 | W0.3, W1.5, W2.2 | The figures need the command, the page and the check number |
| W2.4 | W1.3, W1.4 (a file, not a dependency) | The slot lives in `Hero.astro`, which W1.4 writes, and both it and W2.2 write `site.yaml` — so the two are ordered by their files rather than by their dependencies |
| W2.2, W2.4, W3.2, W3.3 | each other, in that order | All four write `website/data/site.yaml`; no two of them share a level (§7) |
| W3.1 | the console's first release | D95, F23 |
| W3.2 | D93's answer | The owner's decision |
| W3.3 | the owner's video | The spec's own open item |

## 13. Decisions this plan defers to the owner

**All twenty rows of the reconciliation's §4 are unanswered, and seven of them block work in this plan:**

| Decision | Blocks | If unanswered |
|---|---|---|
| **D90** where the site lives | **Parts 1, 2 and 3 entirely** | Part 0 lands and is useful on its own (the install facts, the CLI reference, the security render and the changelog are worth having for readers who never open a site); the site does not start. |
| **D91** hosting | W2.1, W2.2 | Part 1 lands and builds; nothing deploys. |
| **D92** domain | W2.2 (the canonical URL stays a placeholder) | The site builds with a placeholder canonical URL; check 69a cannot pass. |
| **D93** analytics | W3.2, and W1.3's script tag | The site ships with no analytics, which is the recommendation and a valid permanent answer. |
| **D96** the aggregation's data policy | W0.3's shape, W2.3 | Without it, W0.3 is written to the strictest reading of Global Constraint 10 and W2.3 does not run; the caps page shows the "not enough runs" state. |
| **D102** the tagline | W1.4's tagline test | The hero renders the line under `README.md`'s `# Fugaro` title, which is the tagline already. |
| **D104** the palette | W1.2's default | The split palette (violet structure, orange cost) is the default; the owner's answer is a one-line change. |

**Everything else in §4 is a finding with a stated resolution** (D94, D95, D97–D101, D103, D105–D109), and this plan implements the recommendation unless the owner overrides it. **What can start, and under which of these answers, is stated once in §7.1 and not restated here** — the short version is that the first wave (W0.1, W0.2, W0.4, W0.5, W0.6) needs only D90's answer for its output path, W0.3 needs only D96, Part 1 needs no answer beyond the defaults, and Parts 2 and 3 need D91, D92 and the owner's own events. The gate at §15 is the go for a wave rather than a blanket approval, because D90 alone changes every path in §5.

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
10. **The changelog page names a release before its tag exists** (W0.6). `docs/releases/README.md` requires the Highlights file to be merged to `main` **before** `scripts/release.sh` runs, so between that merge and the tag the newest entry is a release GitHub does not have yet: the entry renders as **not yet released**, and the flag is false wherever the generator ran in a checkout without the tag (a shallow CI clone always). The perpendicular fix — asking the releases API — is a network call the build deliberately does not make (W1.1), so the plan records the window rather than passing it off as a state.

## 15. OWNER APPROVAL GATE

**Nothing in this plan starts until the owner has:**

1. **Read [reconciliation.md](reconciliation.md) and answered §4's rows** — at minimum the seven OWNER-ONLY ones (**D90** where the site lives, **D91** hosting, **D92** domain, **D93** analytics, **D96** the aggregation's data policy, **D102** the tagline, **D104** the palette) — and confirmed or overridden the rest. The owner's direction (2026-10-16) was to **start as soon as we can**; that is a direction and not an approval of any row, so this list is what turns it into a go — **and it is a go per wave, not for the plan as a whole**: §7.1 states which decisions each wave needs, and no row below is a substitute for one.
2. **Approved the order**, which puts the generated facts and their drift tests (Part 0) and the aggregation command (W0.3) **before any site code**, and which is the recommendation rather than the only possibility.
3. **Accepted the four refusals** in the reconciliation's §5: no console download link until the console is signed and released, no "~2 h" sentence until the aggregation produces it, no average published as a range, and no Windows support claim beyond what `.goreleaser.yaml` builds.
4. **Accepted the two rules this plan cannot implement by itself**: that the deploy workflow is **staged** under `docs/pending-workflows/` for the owner to copy (no run may touch `.github/workflows/`), and that the site is **not** added to the release gate.
5. **Accepted that check 69 (as 69a–69c) is the owner's to run**, and that W2.2 writes it into the register when Part 2 lands rather than this plan writing it now.
6. **Settled the register's "67 onward" clause for this work** — either confirming the bounded reading this plan applies (the clause is spent on the numbers managed projects took and wrote, 67 and 68, so 69 is this work's first) or reading it as open-ended and allocating the site's checks a number of its own. **The plan's Part 2 does not start until this is answered**: W2.2 re-reads the register, and if 69 is taken or written anywhere, it stops rather than renumbering (§2, U6).
7. **Named the reviewer for the SECURITY-SENSITIVE task** (W0.3, the aggregation) — a task whose whole value is that its output carries no identifier, and whose failure mode is silent.
