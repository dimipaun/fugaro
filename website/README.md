# The Fugaro website

The site build (website plan, Part 1, Task **W1.1**). This directory is a **second
artifact** that lives in this repository beside `console/`, with its own language, its own
build and its own schedule (D90): nothing under it is importable by the Go module, and the
Go module gains no dependency from it. The boundary is a test, not a convention —
`internal/cli/docs_website_boundary_test.go`.

**What is here today:** the shell and the first of the pages. The pinned toolchain, the
static Astro configuration, the check that runs the build and every test file, and this
document. `website/data/` and `website/content/` hold the facts Part 0 generates; the pages
that render them land in W1.3 (`src/layouts/`) through W1.12, and the first two are the hero
(`/`) and the architecture diagram (`/how-it-works/`). **Which routes the build produces is
`ls website/src/pages/`** — the page tasks add to that directory — so that this paragraph
does not need an edit each time one lands, and so that a reader who wants the list reads the
list rather than a copy of it that has drifted (Global Constraint 6).

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
the deploy workflow to run (that workflow is W2.1's: it is not written yet, it will be staged under
`docs/pending-workflows/` for the owner to copy, and no Fugaro run may write it into
`.github/workflows/`), and it is hermetic: it reads this
repository, builds, and asserts over the artifact — no cloud, no credential, no deployed
site. The live checks — the real domain, DNS, TLS and the deployed HTML — are the owner's,
and W2.2 and W2.3 write them as **checks 69a–69c** in `docs/gcp-live-checklist.md`, whose
highest written section is 58 today.

## Nothing loads from a third party

No font, script, stylesheet, image or embed reaches a page from an origin outside this
build: a font is **self-hosted in this artifact** (`website/public/fonts/`, when the styles
land in W1.2), and there is no Google Fonts, no CDN, no embed and no pixel — because every
third-party origin is a request from a reader's browser to a company this project has no
relationship with (D106). There is exactly **one exception, and the owner chose it**: the
analytics script (D93), loaded **only after the visitor consents**, with W1.12's banner as
the gate that decides whether anything is fetched and its own tests for no request before
consent, no cookie before consent, withdrawal and keyboard reachability. `readme.mjs`
refuses a third-party font or script host in the shell's own sources today, and W1.11
asserts the same over the built HTML's script, link, iframe and image origins.

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

Everything under `content/` is **generated, and none of it is edited by hand**, but the
directory holds two kinds of file and they say so in two ways:

- a **document** (`content/*.md`) carries a header on its first line naming the command
  that writes it, and is regenerated by a command run **from the repository root**;
- a **machine output** (`content/costs.json`) is the aggregation's `--json` verbatim, so it
  has no header to carry and no format to add one to — what makes it the aggregation's
  output is its shape, and it is absent until the owner runs the command.

`website/test/readme.mjs` asserts that this table and those files agree: a file whose
header names a different command, a `content/` file this table does not name, a document
without its header, or a JSON file that is not the aggregation's output all fail the site's
check until this document says the same thing.

| File | Source | Regenerate with |
|---|---|---|
| `content/cli-reference.md` | the command tree, `internal/cli.NewRootCmd` | `go run ./internal/clidoc` |
| `content/security.md` | `SECURITY.md`, byte for byte below the title | `go run ./internal/clidoc` |
| `content/costs.json` | `fugaro stats runs --project <installation> --json`, and it is the aggregation's output verbatim | owner-run: `fugaro stats runs --project <installation> --json > website/content/costs.json` |

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
  test/*.mjs                       # every one of them runs under `npm run check`
  dist/                            # the build output, not committed
```

`node_modules/`, `dist/` and `.astro/` are not committed: the first is what `npm ci`
installs from the committed lockfile, and the last two are build output.

The deploy comes later and it is **not a Fugaro run's to write**: W2.1 stages a workflow
under `docs/pending-workflows/` for the owner to copy in, and the owner runs it to deploy
to the Firebase project created for the website only (D91, D109) — a run may not change
`.github/workflows/`. `docs/pending-workflows/` holds other pending workflows today (`ci.yml`),
but no deploy workflow and no `firebase.json` exists yet: they are W2.1's, and this document describes them in advance
rather than claiming them.
