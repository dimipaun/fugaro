# W2.4 — the website and the generated reference — was already on `main`

**Nothing was implemented for this task: its deliverable was already on `main` when the run
started.** This note is the evidence, and it is the pull request's only change. No website
file, no generated page, no test and no golden was touched.

## Where it landed

`26ea375c`, *website: the getting-started sequence is `fugaro setup`, and the CLI reference is
regenerated* — pull request **#850**, merged 2026-10-21, an ancestor of this branch's base
`e9dc9be2`. Its commit line is verbatim the one `### Task W2.4` names under **Commit**, and its
diff is the task.

## Why this note does not spell the retired verb

`internal/cli/docs_live_init_census_test.go`'s census walks `docs/` among its live roots and
matches the retired command's two spellings against every file it finds there. A file in this
directory that spelled the retired command would join the census and fail
`TestLiveInitCensusMatchesTheFrozenList`, which asserts that the walk finds exactly
`pendingRename`. So the retired verb is written here as **“the retired `init` verb”**, and the
grep below is stated rather than pasted.

## The task, point by point

| W2.4's file | state on `main` |
|---|---|
| `website/data/install.yaml` | the `plugin` path's three commands are `fugaro setup`, `/fugaro:setup` and `fugaro upgrade --local` (lines 107–119), and its `note:` says “fugaro setup wires them into
the checkout”. The retired verb is gone. |
| `website/src/pages/getting-started.astro` | the `#setup` `<ol>` is **three** steps — the cloud preconditions, `fugaro setup` in the reader's own terminal, `/fugaro:setup` in the agent — and both `doc('docs/gcp-setup.md', …)` links carry the fragment `the-short-path-fugaro-setup-then-fugarosetup` (lines 491 and 507). The commands are read from `install.yaml`'s `plugin` path (`installCommand` / `agentCommand`, lines 166–172) rather than typed into a step, and the
page's prose about passing flags is gone. |
| `website/src/components/Changelog.astro` | the `escapeHTML` comment no longer quotes a retired-command release line; it now names `fugaro image build --repo` and `--base`, and says a dated release record keeps the name it shipped with. |
| `website/test/caps-and-costs.mjs` | the setup-steps case reads the plugin path's two commands as `installCommand` / `agentCommand` and asserts the band prints them in `[installCommand, agentCommand, installCommand]` order. |
| `website/README.md` | the two round-log sentences that quoted the retired verb were reworded (the spacing-gate bullet and the byte-figure bullet). |
| `website/content/cli-reference.md` | regenerated: `go run ./internal/clidoc` rewrites it byte-identically today. |
| `website/content/security.md` | current: `go run ./internal/clidoc` prints “website/content/security.md is already current”. |
| `website/design-ref/GettingStartedV4.dc.html` | the owner-approved getting-started board was renamed with the page it draws. |

The census file records the same landing in its own words: **“W2.4 moved the list seven files
down, and it is the website's whole share”** — the six site files above plus
`plugin/readme_test.go`, which left because the sentence it pins left `README.md`'s Getting
started in the same commit.

## The evidence, run on this branch's base `e9dc9be2`

- **The word is gone from the website.** A `git grep` for the retired command over `HEAD --
  website/` prints **nothing**: zero files, zero hits. The same grep for the bare `init` +
  `--repo` form also prints nothing. `fugaro setup` appears in eight website files, 35 hits
  between them — `cli-reference.md` (15) and `security.md` (6) among them.
- **The census agrees.** `pendingRename` in `internal/cli/docs_live_init_census_test.go` lists
  **no** path under `website/`, and its own prose says why.
- **The generated reference is current.** `go run ./internal/clidoc` →
  `website/content/security.md is already current` and
  `wrote website/content/cli-reference.md from the command tree (78 commands)`, after which
  `git status --short` is **empty**: the generator's output is what the tree already holds.
- **W2.4's own tests pass**, all on `e9dc9be2`:
  - `GOFLAGS=-p=2 go test ./internal/clidoc -run CliRef -count=1` → `ok`
  - `GOFLAGS=-p=1 go test ./internal/cli -run 'TestWebsiteInstallFacts|TestCliReferenceNamesEveryCommandAndFlag|TestCliReferenceInventsNothing|TestWebsiteSecurityPage' -count=1` → `ok`
  - `GOFLAGS=-p=1 go test ./internal/cli -run 'TestCensusNamesInitReadsPastTheHead|TestCensusOutputAndExemptArePinned|TestCensusOutputCoversEveryIgnoredPathTheWalksReach|TestLiveInitCensusMatchesTheFrozenList|TestLiveInitCensusRootsAreTheOnlyOnesThatNameIt' -count=1` → `ok`
  - `GOFLAGS=-p=1 go test ./plugin/... -run 'Readme|Skill|Lint' -count=1` → `ok`
- The `docs/gcp-setup.md` heading the two page links resolve to exists and reads
  `## The short path: \`fugaro setup\`, then \`/fugaro:setup\`` (line 13).

## What was not verified here

- W2.4's **Verification** line ends in `(cd website && npm ci && npm test)`. This container has
  **no `npm` binary at all** (`npm: No such file or directory`), so the website's own node
  tests were not run in this run — including `website/test/caps-and-costs.mjs`, the case that
  resolves the two anchor fragments, and `website/test/docs-hub.mjs`. `website/package.json`
  carries no `test` script either; the site's own gate is `npm run check` (`astro build`, then
  `node --test test/*.mjs`). The Go-side pins that read the same files are green, above.
- No live cloud action, no network and no `belong`/EdgeWeb/EdgeServer interaction was needed or
  attempted.
