# Licensing (open core): reconciliation

Checked against `origin/main` at `e924481cd`, 2026-10-24. **Every decision D300–D310 was answered
by the owner on 2026-10-24**, ten as recommended and **D305 not the recommendation**; those answers
are the approval of this reconciliation. Answering a decision approves no task: the plan
([implementation-plan.md](implementation-plan.md)) needs its own go, task by task. Where this file
disagrees with [spec.md](spec.md), this file wins. Nothing here is legal advice: every statement
about copyright or licence terms is a fact found in the repository or in a published text, stated
for the owner and counsel to weigh.

**Amended 2026-10-24 by the owner's rulings on T1 and O2** (both recorded below, beside the
original answers): `ee/` ships **reserved**, with no licence file, at 0.11.0 (T1 settled as
choice (iii)); and the root `LICENSE` stays **pure MIT, unchanged**, with the scope statement in a
new root `LICENSING.md` (O2, **not the recommendation**, amending D301). **Also amended
2026-10-24, not the recommendation: D303** — ee code reaches users in the **same** released
binary, compiled in by a build tag the release build sets and enabled by the licence key; builds
without the tag are pure MIT (open tension T3).

**Numbers this work holds:** decisions **D300–D319** (the highest decision written under `docs/`
on `main` is **D271**, the rigor levels' LD11; open PRs reach **D260**; D272–D299 are left unused
so that sibling spec reconciliations started the same day cannot collide with this block) and
checks **130–134** (the highest check section written in the register on `main` is **113**;
**119** and **123** are claimed by the console-windows and console dependency graph plans, and
open PR #1022 takes **124**; **130** is free either way).
Findings are **F1–F27**; resolutions (one defensible answer) are **R1–R8**; decisions
**D300–D310** are used and **D311–D319** are reserved and unused; checks **131–134** are reserved
and unused.

## Headline: spec concept → what the repository already has

| Spec concept | What exists at `e924481cd` |
| --- | --- |
| "Everything outside `/ee` is MIT (the current license)" | Root `LICENSE` is the MIT text, `Copyright (c) 2026` the owner personally; GitHub's licence API reports `MIT` for the public repository. No `ee/` exists. No file carries an SPDX header |
| "the owner owns all the copyright" (spec, Contributions) | Not the whole tree: `.agents/skills/` (299 files, 46 skills) is vendored third-party MIT code (F2). Every other commit is the owner's, the owner's own bots, or Dependabot (F3) |
| Root `LICENSE` preamble | The website derives the licence identifier from `LICENSE`'s **first line** and a test pins it (F7). **Amended (O2):** `LICENSE` stays unchanged; the statement goes in a new `LICENSING.md` |
| README "Licensing" section | `README.md` ends with `## License` / `[MIT](LICENSE)` |
| `ee/LICENSE` modelled on the two named vendors' `ee/` licences | Nothing. No licensor entity is named anywhere; the only copyright holder written is the owner personally (F15) |
| Core extension points (hooks, interfaces, plugin registration) | Interfaces for dependency injection and one name-keyed opener map (`backend.Open`); no plugin registration (F10) |
| "Core must never import `/ee`" + CI check | Two import-boundary tests already exist to copy (F12); CI runs on pushes to `main` only, not on PRs |
| "If `/ee` is removed, the core still builds" | One Go module, one binary that carries CLI and runner (F9); console, mobile, shared and website are separate npm projects (F11) |
| License-key check | No offline signature verification and no embedded public key anywhere (F16) |
| CLA / DCO + bot | Nothing: no CLA, no DCO convention, no PR-time check of any kind (F13) |
| Later `/ee` candidates (console, mobile, central orchestrators, coordinator) | All already in the public MIT tree or in open PRs against it; the console is released (F4, F5) |
| What stays core | All present under MIT (F19) |

## Findings

**F1 — Exists: where "MIT" is stated today.** Five places, all of which must agree after the
change: `LICENSE` (first line `MIT License`); `README.md` `## License`; `plugin/.claude-plugin/plugin.json`
`"license": "MIT"`; `.goreleaser.yaml` `homebrew_casks[0].license: MIT` (the cask in the tap
repository); `website/src/pages/community.astro` (the Licence card, from `licenceIdentifier()`).
`.goreleaser.yaml` `archives[0].files` ships `LICENSE` and `README.md` beside the binary.
No `package.json` in the tree has a `license` field (all ten are `"private": true`);
`.claude-plugin/marketplace.json` has none. No source file carries an SPDX header.
*Consequence:* the change is a coordinated edit of these five, and the plugin manifest and the
Homebrew cask stay `MIT` only if the released artifact stays core-only (D303). **As amended (D303,
2026-10-24):** they stay `MIT` while the release build sets no `ee` tag, which is the case at
0.11.0; the release that first sets it changes all of them together (T3, plan slice L5).

**F2 — Wrong: "no outside contributions exist, so the owner owns all the copyright".**
`.agents/skills/` (299 files, 46 skills) was vendored from a third-party Go skills
project in #994 (`skills-lock.json` names the source per skill; each `SKILL.md` frontmatter says
`license: MIT` and names its author). The tree carries **no copy of that project's licence text
or copyright notice**, which the MIT terms ask to be included in copies. `censusVendoredRoots` in
`internal/cli/docs_live_init_census_test.go` already treats `.agents` as vendored.
*Consequence:* that code is not the owner's to relicense; the root licence statement must not
claim it, any Apache move (D300) leaves it MIT under its own author, and its notice should be
added (R1).

**F3 — Gap: the copyright facts in the history (listed neutrally).** On `main` (1,564 commits):
commit authors are the owner under **three email addresses on three different domains**
(476, 139 and 3 commits), `fugaro-dev[bot]` (549: Fugaro's own GitHub App, i.e. runs),
the runner identity `Fugaro <fugaro@users.noreply.invalid>` (389) and `dependabot[bot]` (8
mechanical version bumps; its 14 `Signed-off-by` trailers are the only sign-offs in the
history). PR authors ever: the owner's account, `app/fugaro-dev`, `app/dependabot`. Commit
trailers credit AI models as co-authors (about 8,900 `Co-authored-by` lines). No outside human
contributor appears. *Consequence, for the owner and counsel, not decided here:* (a) whether any
organisation behind the email domains has a claim on work committed under its address; (b)
how AI-assisted authorship bears on the copyright the enterprise licence rests on. Neither blocks
the 0.11.0 slice; both bear on D305.

**F4 — Rule (internal contradiction): "nothing gets moved out of the core" vs the later
candidates.** The candidates are already core: `console/` (released, F5), `mobile/`, the
coordinator (docs/coordinator plus open PRs #1021, #1024), the central orchestrators (branch
`central-orchestrator`, open PR #831). The repository is **public** and reported as MIT, so all
of that is already published under MIT; relicensing later versions cannot take back what is
published. *Consequence:* moving any of them into `ee/` is exactly the move the spec's first
principle forbids, and anyone may keep developing the MIT version. Only **net-new** organisation
features can be born in `ee/` (D302).

**F5 — Gap: released versions stay MIT, and one release stream carries no licence text.**
`v0.10.4`'s `fugaro_0.10.4_linux_amd64.tar.gz` contains `LICENSE` (MIT, checked by download),
`README.md` and `fugaro`. `console-v0.110.0` ships `.dmg`, `.zip`, `.AppImage`, `.deb`;
`console/package.json` has no `license` field and its `build.files` list does not include a licence
file, so the packed app most likely carries no Fugaro licence text (UNVERIFIED U4). No `mobile-v*`
tag exists. *Consequence:* every release up to the change is MIT forever; the README section and the
0.11.0 release notes should say so (R4), and the console's packing gap is part of D307.

**F6 — Gap: third-party notices in the release binary (pre-existing).** `go list -deps ./cmd/fugaro
./internal/...` reaches 70 non-main modules: 36 Apache-2.0, 19 MIT, 15 BSD (licence files read from
the module cache); `google.golang.org/grpc` and `gopkg.in/yaml.v3` carry `NOTICE` files. The archive
ships no third-party notices file. The console dependency graph's GD3 already commits to shipping
elkjs's EPL-2.0 text (docs/console-dependency-graph/reconciliation.md F14). *Consequence:* the
spec's "License files and notices" section is where this belongs (D307); it is independent of the
open-core split.

**F7 — Wrong (for the spec's preamble): a pinned fact reads `LICENSE`'s first line.**
`licenceIdentifier()` in `website/src/pages/community.astro` takes line 1 of `LICENSE`, strips a
trailing " License" and throws only if nothing is left; `website/test/faq-community.mjs` ("the
licence is named, and it is the identifier LICENSE carries") pins the page to it, which is D101's
answer (state the licence inline). The page states the licence in **two elements**: the **Licence
card** in the community grid (a link to `LICENSE` and the note "the {licence} licence, stated here
because it is a fact about the source rather than a claim about anyone") and the **`#licence`
section** further down ("Fugaro is released under the {licence} licence: `LICENSE` is the whole of
it, and it is the only text that governs what you may do with the code"). The `#licence`
section's sentence is the one that becomes false once anything outside `LICENSE` governs part of
the repository.
*Consequence:* a preamble **above** `MIT License` would not break the build: the whole preamble
line would be rendered as the "identifier" ("released under the <preamble> licence"), a wrong page
that the pinning test still passes, since it compares the page with the same line. The spec's shape
is not taken. **As amended (O2, 2026-10-24):** `LICENSE` is not touched at all, so
`licenceIdentifier()` keeps reading `MIT License`; the `#licence` section's sentence is rewritten
and both elements link `LICENSING.md` (R2).

**F8 — Gap: licence detectors.** GitHub currently reports `MIT`. Extra text in `LICENSE` can make
GitHub's detector and pkg.go.dev's report "other" (U1; F27 found one repository of that shape
reported `other`); pkg.go.dev hides documentation
for packages whose licence it cannot classify as redistributable, which `ee/` packages will be
(UNVERIFIED U2). *Consequence:* a post-merge look is an owner-run check (Check 130). **As amended
(O2):** with `LICENSE` unchanged, Check 130 verifies that GitHub still reports MIT and that
`LICENSING.md` renders.

**F9 — Gap: one Go module, one binary.** `go.mod` is `module github.com/dimipaun/fugaro`;
`cmd/fugaro/main.go` builds `cli.NewRootCmd()`, and that one binary is both the CLI and the
in-container runner (the header of `internal/creds/boundary_test.go` says so). With "core never
imports `/ee`", ee code can only reach a binary through (a) a separate `main` under `ee/` that wraps
`cli.NewRootCmd()`, (b) a build-tagged file in `cmd/fugaro` importing `ee` (which is core importing
ee), or (c) runtime plugins (nothing like that exists). A Go package under `ee/` in the root module
may import `internal/...` (same path prefix). If the one released binary links ee code, the
archive's `LICENSE`, the plugin manifest and the Homebrew cask's `license: MIT` stop being true,
and every user downloads commercial code. `go install …@vX` fetches the whole module zip, `ee/`
included, either way. *Consequence:* D303 (which binary carries ee, and from which module).
**As amended (D303, 2026-10-24):** option (b), confined to one assembly file
(`cmd/fugaro/ee_on.go`, `//go:build ee`), which is the single place allowed to import `ee/`; the
release build's tag makes the released binary mixed-licence from the first ee feature on (T3).

**F10 — Exists (partly): the extension points a workspace feature would plug into.**
- `backend.Open(ctx, name, openers)` in `internal/backend/registry.go` picks from a
  `map[string]backend.Opener` that core builds in `cloudOpeners` (`internal/cli/cloud.go`): the
  closest thing to a registry, but the map is assembled in core.
- Interfaces used for injection: `agent.Agent`, `backend.Backend`, `gitprov.Provider` /
  `TokenSource`, `credsvc` `Verifier` / `PolicyReader` / `KillReader` / `KeySource` /
  `BundleSource`, `budget` `Reader` / `KillDB`, `creds` `Store` / `DB`, `initflow.Stage`.
- `policy.Merge(ceiling, tighten...)` (`internal/policy/merge.go`): layers ceiling → default
  branch → branch, a later layer only tightens, caps are **per run**. A workspace-wide cap is an
  aggregate across projects, which no layer expresses.
- `internal/projectpolicy` (the published project layer) and `internal/localcfg` (local).
- Embedded assets a workspace would extend: Terraform via `deploy/terraform/terraform.go`
  (`//go:embed all:gcp`), database rules via `internal/budget/rules/rules.go`
  (`//go:embed rules.json.tmpl`).
- No registration API: no `Register…` function under `internal/`, and the only **non-test**
  `init()` in `internal/` or `cmd/` is the template-func one in `internal/cli/root.go` (test files
  have two more: `internal/cli/skills_test.go`, `internal/watch/render_test.go`).
*Consequence:* the spec's Task 3 is real design work that belongs to the workspaces plan, not to
the 0.11.0 slice (R7).

**F11 — Gap: the boundary across languages.** `console/`, `mobile/`, `shared/` and `website/` are
separate npm projects outside the Go module (`TestConsoleIsOutsideTheGoModule` in
`internal/cli/docs_console_boundary_test.go`). A workspace view in the console (the workspaces
spec's own open question) means the console build pulling sources from `ee/`, which is core
importing ee. Workspace Terraform and database rules hit the same embed question (F10). The spec is
silent. *Consequence:* D309 scopes the first boundary check to Go and says when the others are
added.

**F12 — Exists: the precedent for the import-boundary check.** `internal/creds/boundary_test.go`
walks the whole module, parses import lines with `go/parser`, and holds an exact allow-list;
`internal/cli/docs_console_boundary_test.go` has `TestGoModHasNoConsoleDependency` and
`TestNoGoFileEmbedsFromConsole`. A parsed scan sees files of every build tag, which `go list -deps`
does not. It runs in `go test ./...`, so in every run's loop and in CI on `main`, with no workflow
edit. CI is not a PR gate (`CONTRIBUTING.md`, "What CI runs", the owner's 2026-10-16 ruling), so
any "CI check" catches a violation after merge; a job that deletes `ee/` and builds is a workflow
change that only the owner can copy in (CLAUDE.md, "Never edit `.github/workflows/`").
*Consequence:* D309.

**F13 — Gap: contributions.** No CLA, no DCO, no sign-off convention; `CONTRIBUTING.md` invites
PRs with no licensing terms; `.github/pull_request_template.md` has no DCO line. As commonly
understood (the owner and counsel to confirm), a DCO certifies the right to submit under the
project's licence but grants no right to relicense, so the spec's "a CLA **or** a DCO … so the
copyright stays relicensable" holds only for a CLA. A CLA check is either a hosted GitHub App the
owner installs (no workflow file) or a `pull_request_target` workflow (staged under
`docs/pending-workflows/`); `fugaro-dev[bot]` and `dependabot[bot]` need allow-listing; it would be
the repository's first PR-time check. *Consequence:* D304.

**F14 — Open: Apache-2.0 for the core.** Facts: the owner can license future versions of
owner-authored code (subject to F3); `.agents/` stays MIT (F2); published versions stay MIT;
every linked Go dependency is Apache-2.0, MIT or BSD; EPL-2.0 elkjs is compatible either way.
The switch touches the five places in F1, adds the `NOTICE` convention, and Apache's `LICENSE`
does not start with a one-word identifier line, so `licenceIdentifier()` changes too.
*Consequence:* D300; the spec asks for it before the next release.

**F15 — Open: the enterprise licence text and its licensor.** The only copyright holder written
is the owner personally. A commercial licence needs a licensor (the owner or a company, which
then needs rights from the owner), a definition of production use, subscription terms, and the
usual clause on contributions to `ee/`. An agent can draft a starting point modelled on the two
licences the spec names, marked DRAFT; it is the owner's and counsel's text. *Consequence:* D305,
and D306's fallback if the text is not approved by the 0.11.0 tag.

**F16 — Gap: the licence-key check.** No offline signature verification and no embedded public key
exist in the tree (no `ed25519`/`ecdsa` verify outside tests). The standard library's
`crypto/ed25519` is enough (no new dependency). The public key is not a secret and can be
compiled into ee code; the private signing key is the owner's, never in the repository, and
minting a key is OWNER-RUN. Offline means no revocation, so keys carry an expiry. The ee source is
visible, so the check is a switch, and the licence is the enforcement. No phone-home matches the
existing posture (`internal/updatecheck` sends one anonymous GET; the README says the console has
no telemetry of its own). A key literal may trip the secret scan (`images/trivy-secret.yaml`;
UNVERIFIED U5). Three design rules the D308 note must carry (added after the round-1 review):
the signature is verified over the **raw key bytes before** any JSON is parsed; the key carries a
**key id** so the compiled-in public key can be rotated; and an offline expiry can be beaten by
setting the machine's clock back, which the design states as a known limit rather than defends
against. *Consequence:* D308; nothing in 0.11.0 needs it unless an ee feature ships.

**F17 — Gap: the name and logo.** `brand/` (logo, app icons) sits in the MIT tree; there is no
trademark statement anywhere. MIT is silent on trademarks; Apache-2.0 §6 excludes them.
*Consequence:* D310.

**F18 — Gap: the docs and pages that state the licence.** `README.md` (`## License`); the website
Community card (F7); historical docs that call the repository MIT
(`docs/website/reconciliation.md` D101, `docs/console-dependency-graph/reconciliation.md` F14,
`docs/voice-module/reconciliation.md`) stay true of the core and need no edit. No release-notes
convention covers a licence change; it is a Highlights line, with no `### For operators` step.
*Consequence:* R2, R4, R5.

**F19 — Exists: what the spec keeps in the core.** All present under MIT: CLI (`internal/cli`),
job execution (`internal/runner`, `internal/backend`), recipes (`internal/recipe`), rigor modes
(`docs/rigor-modes/`, open PRs #1017–#1019), the review loop (`internal/runner`), project-level
caps (`internal/policy`, `internal/budget`), the watch dashboard (`internal/watch`,
`internal/cli/watch.go`), the harness and backend abstractions (`agent.Agent`, `backend.Backend`),
setup skills (`plugin/skills`). *Consequence:* none; consistent.

**F20 — Rule: the watch dashboard is core, the desktop console a candidate.** The README calls the
console "a graphical `fugaro watch`". Putting it in `ee/` would charge for the graphical form of a
core feature. *Consequence:* folded into D302.

**F21 — Gap: 0.11.0 timing.** The central orchestrators are recommended for "0.11.0 or later"
(docs/central-orchestrator/reconciliation.md D17) and the coordinator's project record is planned
for 0.11.0 (docs/console-dependency-graph/README.md, GD1). Both are spec candidates, and the spec
says "decide on each one before release". *Consequence:* D302 must be answered before the 0.11.0
tag, or both ship as core (which F4 says they already are).

**F22 — Gap: workspaces have no docs package.** Nothing under `docs/` defines a workspace at this
commit; no "workspace" concept exists in code (every hit is a git checkout or a provider's console
workspace). The spec's Tasks 3 and 4 depend on a workspaces spec. *Consequence:* R7.

**F23 — Gap: one module or two.** `ee/` in the root module keeps versions in lockstep and lets ee
import `internal/...` directly; a nested module (`ee/go.mod`) keeps ee out of the core module zip
and makes "core builds without ee" structural, at the cost of `replace` directives or `ee/vX.Y.Z`
tags (cross-module `internal` imports by path prefix: UNVERIFIED U3). *Consequence:* a sub-choice of
D303.

**F24 — Rule: file headers vs "comments explain why".** The spec wants a header on every `ee/`
source file; CLAUDE.md forbids comments that restate. A licence header is a legal notice, not a
code comment. *Consequence:* R3.

**F25 — Gap: an empty `ee/` in 0.11.0.** With no ee feature ready, the slice that ships is the
structure: `ee/LICENSE`, `ee/README.md`, the root statement, the README section, the boundary test.
The boundary test passes vacuously but pins the rule before the first ee file lands.
*Consequence:* D306.

**F26 — Gap: a separate workspaces spec is already in the owner's hands.** The licensing spec's
first `ee/` feature is workspaces; that work's reconciliation will need the decisions D302, D303,
D308 and D309 as inputs. *Consequence:* answer those here first.

**F27 — Open (from D305's answer): the two published texts, read 2026-10-24 at 16:35 UTC over
HTTPS.** This section is the only place the two vendors the spec names are cited, by URL only;
elsewhere they are "vendor A" (the first source below) and "vendor B" (the second). Sources,
fetched with `curl`:
- vendor A: `https://raw.githubusercontent.com/PostHog/posthog/master/ee/LICENSE` (the file last
  changed in that repository's commit `7fb9150bf6c1`, 2026-06-16);
- vendor B: `https://gitlab.com/gitlab-org/gitlab/-/raw/master/ee/LICENSE` (last changed in commit
  `8672550c`, 2024-10-29);
- vendor A's root licence, `https://raw.githubusercontent.com/PostHog/posthog/master/LICENSE`,
  for its shape;
- GitHub's licence API for vendor A's repository (`gh api repos/<vendor A's repository>/license`),
  which answers `NOASSERTION` / `other`.

What the two `ee/LICENSE` texts say, in substance (both alike except where noted): production use
requires agreeing to the vendor's **Subscription Terms of Service at a named URL** and a valid
subscription **"for the correct number of user seats"**; anyone may modify the software and
publish patches, but the vendor keeps all right, title and interest in those modifications, which
may then be used only under a subscription; copying and modifying for **development and testing**
needs no subscription; otherwise copying, merging, publishing, distributing, sublicensing and
selling are forbidden; an MIT-style warranty disclaimer; third-party components stay under their
own licences. Vendor B's text adds a paragraph scoping itself to what is not in that vendor's
community edition and carving client-side assets out to MIT, and requires the full text in all
copies. Vendor A's root `LICENSE` puts a scope statement (`ee/` under `ee/LICENSE`, third-party
components under their own licences, the rest MIT) ahead of the MIT permission text: the shape D301
first answered, and the reason GitHub reads it as `other`, which is why O2 then moved the statement
into `LICENSING.md`.

Four facts follow, each for the owner and counsel to confirm:
1. **The fitting model is vendor A's text.** It names one licensor and nothing else; vendor B's
   extra paragraph is about that vendor's own two-edition distribution and has no counterpart
   here. (Vendor A's root `LICENSE` shape is not followed: O2 keeps `LICENSE` pure MIT.)
2. **Neither text states a permission to reuse it, and neither states an attribution
   requirement.** Each file sits inside its own `ee/` directory, which its own terms cover, and on
   a literal reading those terms forbid copying it. Whether the text of a licence can be reused
   anyway is a legal question this reconciliation does not answer.
3. **"As is, with only names filled in" is not enough on its own.** The text points at a
   Subscription Terms of Service document at a URL and prices by user seats. Fugaro has neither: the
   website has no terms page (`website/src/pages/` holds none) and no subscription or seat model
   exists anywhere in the repository. Filling those in is writing terms, not filling in names.
4. **The licensor line.** The adopted text's copyright line and every vendor name in it become the
   licensor. The only copyright holder written in the repository is the owner personally (root
   `LICENSE`), so the line reads `Copyright (c) 2026 <the owner's full name>` unless the owner and
   counsel name an entity, in which case the root `LICENSE` copyright line should be checked
   against it too. **After O2 that is the one counsel-dependent exception to "`LICENSE` is never
   edited"**: an edit of the copyright line alone, on the owner's explicit go, in its own commit
   (implementation plan, Conventions).

*Consequence:* open tension T1 below; until it is settled, D306's fallback ships `ee/` as reserved,
with no `ee/LICENSE`.

## Resolutions (one defensible answer each)

- **R1** — Add the vendored project's upstream licence text under `.agents/` (its own `LICENSE`
  with its author's copyright line) and name `.agents/` in the scope statement (`LICENSING.md`, as amended by O2) as third-party code
  under its own licence. (F2)
- **R2** — Keep D101's shape: the website Community page states the split inline (core MIT, `ee/`
  reserved) in **both** of its licence elements, the Licence card and the `#licence` section, and
  both link `LICENSING.md` beside `LICENSE` (as amended by O2); no new `/licensing` page. Rewrite
  the `#licence` section's "the only text that governs", keep `licenceIdentifier()` working (F7), run
  `npm run check` in `website/`.
- **R3** — Each `ee/` source file starts with a two-line legal notice:
  `SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise` and a pointer to `ee/LICENSE`, enforced
  by a test beside the boundary test; legal notices are outside the comment rule. (F24) **The
  rule the test holds (amended by T1's ruling, so a reserved `ee/` passes; made exact after the
  round-1 review):**
  - **Scope.** Every file under `ee/`, walked without skipping any directory (dot-directories,
    `_`-prefixed directories and `testdata/` included, unlike the creds walker), symlinks refused.
    Only the exact paths `ee/README.md` and `ee/LICENSE` are exempt; a `README.md` in a
    subdirectory is a source file.
  - **Comment syntax by type.** `//` for `.go`, `.ts`, `.tsx`, `.js`, `.mjs`, `.cjs`; `#` for
    `.sh`, `.py`, `.tf`, `.yaml`, `.yml`, `.toml`, `Dockerfile`; `<!-- -->` for `.md` and `.html`;
    `/* */` for `.css`. A file starting with `#!` carries the notice on lines 2 and 3.
  - **Formats with no comments** (`.json`, `go.sum`, images, any binary or unknown type) are
    refused under `ee/` unless a sibling `<name>.license` file carries the notice (the REUSE
    convention); the sibling is itself a source file.
  - **Licence presence.** `ee/LICENSE` must exist whenever at least one source file does.
  - An absent `ee/`, or one holding only `ee/README.md`, passes.
- **R4** — Every version released before the change stays MIT; the README's Licensing section,
  `LICENSING.md` and the 0.11.0 release notes say so in one sentence. (F5)
- **R5** — No `### For operators` entry: nothing runs after deploy. A Highlights line and a
  `website/data/release-summaries.yaml` line carry the change. (F18)
- **R6** — The ee import path is `github.com/dimipaun/fugaro/ee/...`; the boundary test forbids
  it, matched by path segment (`…/ee` or `…/ee/…`), in any non-`ee/` Go file of any build tag, and
  forbids every other route into `ee/`: a symlink, `//go:embed`, `//go:generate`, a cgo include, a
  `replace` directive, or a nested `go.mod`/`go.work` under `ee/`. **One exception, from D303's
  amendment:** the assembly file `cmd/fugaro/ee_on.go`, which must carry `//go:build ee` and may
  import exactly one package, the registration package `github.com/dimipaun/fugaro/ee/register`
  (blank import); no other file and no other core package may import `ee/`.
  (F12)
- **R7** — The spec's Tasks 3 (extension points) and 4 (workspaces) move to the workspaces plan;
  this work ships the licensing structure only. (F10, F22)
- **R8** — The private signing key for licence keys is never written into the repository, a doc,
  a test or a run; minting is OWNER-RUN. (F16)

## Decisions (answered 2026-10-24)

Each row keeps the question, the recommendation as it was put, the alternative and its cost, and
the owner's answer with a one-line why. `(R)` marks an answer that took the recommendation.

| # | Question | Recommendation and reason | Alternative and its cost | Owner's answer (2026-10-24) |
| --- | --- | --- | --- | --- |
| **D300** | Core licence from 0.11.0: MIT or Apache-2.0? | **Stay MIT.** The spec's first principle says "MIT forever"; with no outside contributor, Apache's patent grant buys little today; one licence across all versions | **Apache-2.0:** explicit patent grant and trademark exclusion; costs the five-place edit (F1), a `NOTICE`, a new website derivation (F14), two licences in the history | **Stay MIT (R).** *Why:* one licence for every version, and the principle as written |
| **D301** | Shape of the root `LICENSE` | **Keep line 1 `MIT License`; a short scope statement (`ee/` excluded, `.agents/` third-party) between it and the copyright line; MIT text unchanged; Check 130 looks at the detectors after merge** | Preamble above line 1: the website renders the preamble line as the licence's name, and its pinning test still passes (F7, corrected after the round-1 review). `LICENSE` untouched plus `LICENSING.md`: the root file keeps claiming MIT over `ee/` | ~~**As recommended (R).** *Why:* the website's derivation keeps working and the file states its own scope.~~ **Amended 2026-10-24 by the owner's O2 answer, not the recommendation:** `LICENSE` stays pure MIT, first line and text unchanged, so GitHub keeps showing MIT; the scope statement (`ee/` excluded and reserved, `.agents/` third-party under its own licence, a pointer to the trademark paragraph) goes in a new root `LICENSING.md`, which the README Licensing section and the website card link. *Why:* GitHub's MIT reading matters more to the owner than the file stating its own scope (F27). Consequence: open tension T2 |
| **D302** | Where the later candidates go | **Console, mobile, coordinator and central orchestrators stay core MIT; only net-new organisation features are born in `ee/`** | Move some into `ee/`: breaks "nothing moves out", leaves an MIT fork point, needs the cross-language boundary now | **As recommended (R).** *Why:* they are already published under MIT (F4), and the console is the graphical watch (F20) |
| **D303** | How ee code reaches users | **A separate `main` under `ee/` in the same module, built as a separate artifact; the released `fugaro` stays core-only** | One key-gated binary: mixed-licence archive. Build tag in `cmd/fugaro`: core imports ee. Nested module: version coupling | ~~**As recommended (R).** *Why:* the released CLI's `LICENSE`, plugin manifest and Homebrew cask stay true (F9).~~ **Amended by the owner 2026-10-24, not the recommendation:** one released binary, key-gated (the CE/EE model): ee code is compiled into the **same** `fugaro` by an `ee` build tag that the release build sets, and enabled at run time by the licence key; builds without the tag are pure MIT. Core packages never import `ee/`; the one exception is the tag-gated assembly file `cmd/fugaro/ee_on.go` (R6). At 0.11.0 `ee/` is empty, the release sets no tag and the archive stays pure MIT. *Why:* one binary for users; the label consequence is T3 |
| **D304** | Contributions | **A CLA through a hosted CLA app, Fugaro's own bots allow-listed, in place before the first outside PR is merged** | DCO only: no relicensing right (F13). DCO for core and no outside code to `ee/`: gives up relicensing core | **As recommended (R); the app install is OWNER-RUN.** *Why:* only a CLA keeps the copyright relicensable |
| **D305** | The enterprise licence text and its licensor | **An agent drafts a starting point modelled on the two named licences, marked DRAFT; the owner and counsel settle text and licensor** | **Adopt a published source-available licence as is**, with only names filled in: quicker, fewer custom terms, less fitted | **Not the recommendation: adopt a published text as is (in the style of either vendor the spec names), only names filled in; no bespoke licence.** *Why:* the owner prefers a known text to a new one. The current texts were read for this answer (F27): vendor A's text fits; open tension **T1** records what the answer meets; the owner and counsel still confirm the text and the licensor line. **Resolved 2026-10-24 by the owner's T1 ruling:** 0.11.0 ships `ee/` **reserved, with no licence file**; `ee/LICENSE` lands only when counsel's text is approved. D305 stays not the recommendation; T1 choice (iii) is how it resolves |
| **D306** | What ships with 0.11.0 | **The skeleton only:** `ee/LICENSE` and `ee/README.md`, the root statement, the README Licensing section, the Community card in D101's shape, the `.agents/` upstream notice, the Go boundary and header tests, `CONTRIBUTING.md` contribution terms, one release-notes line. **If the licence text is not approved by the tag, ship everything else and mark `ee/` as reserved** | Also the licence-key library now: unused code, a key format fixed before a feature needs it | **As recommended (R).** *Why:* the structure and the rule land before the first `ee/` file. **Applied 2026-10-24 (T1):** the fallback is the shipping state: `ee/` reserved, no `ee/LICENSE`; and the root statement is `LICENSING.md` (O2) |
| **D307** | Third-party notices in released artifacts | **Yes: a notices file in the CLI archive (goreleaser) and `LICENSE` plus notices in the packed console** | Leave as is: the pre-existing gap stays (F5, F6) | **Yes (R).** *Why:* the released binaries carry other people's notices |
| **D308** | The licence key, when the first ee feature ships | **Ed25519-signed key checked offline, expiry and grace, no revocation, no phone-home** | Online activation: the telemetry-based enforcement the spec rules out | **As recommended (R); design only, not built in 0.11.0.** *Why:* the spec's "keep it light" with no telemetry. **With D303 amended:** the check lives in `ee/` and gates at the registration point (`ee/register`): nothing in core knows about keys |
| **D309** | Scope of the boundary check | **Go only: the parsed-import test, the `ee/` header test, and a pending workflow that deletes `ee/` and builds the core, staged under `docs/pending-workflows/` with its README section; the owner copies it in** | Every language now: rules for four npm projects with nothing to check | **As recommended (R).** *Why:* the first `ee/` code will be Go; the others arrive with the first non-Go file |
| **D310** | A trademark statement | **One paragraph in the README: the licences grant no right to the Fugaro name or logo** | None | **As recommended (R).** *Why:* `brand/` sits in the MIT tree (F17) |

Checks: **130** (owner-run, after the skeleton merges): **as amended by O2**, that GitHub still
reports MIT for the repository and that `LICENSING.md` renders on GitHub. **131–134** reserved,
unused.

## Open tensions and open items for the owner

- **T1 — D305 ("adopt a published text as is") against what the published texts say (F27).**
  (a) Neither text grants a permission to reuse it, and on a literal reading each one's own terms
  cover the file; (b) both depend on a Subscription Terms of Service document and a seat count that
  Fugaro does not have, so "only names filled in" leaves those two references pointing at nothing.
  The choices: **(i)** counsel confirms vendor A's text can be reused and the owner supplies a terms
  document and a seat definition (this blocks `ee/LICENSE`, now task L4.1, on both); **(ii)** the owner
  picks a source-available licence that is published expressly for others to adopt (which texts
  qualify, and on what terms, is UNVERIFIED U8), at the cost of terms that differ from "production
  needs a subscription"; **(iii)** keep D306's fallback: 0.11.0 ships `ee/` as reserved with no
  licence file, and T1 is settled before the first `ee/` feature. **(iii) is what happens by
  default at the 0.11.0 tag** if neither (i) nor (ii) is settled; it holds nothing else back.
  **Ruled 2026-10-24: (iii).** 0.11.0 ships `ee/` reserved with no licence file until counsel
  settles the text; counsel (L0.1) is off the 0.11.0 critical path. (i) or (ii) remains open for
  counsel, and `ee/LICENSE` lands in its own later task (L4.1) once a text is approved.
- **T2 — The root `LICENSE` alone no longer says `ee/` is excluded (accepted by the owner,
  2026-10-24).** With O2's answer, `LICENSE` is the plain MIT text over "the Software", and the
  exclusion of `ee/` and `.agents/` lives in `LICENSING.md`, the README and `ee/README.md`. A reader
  who opens only `LICENSE` sees no exclusion. The owner accepted this in exchange for GitHub's MIT
  reading; it is recorded, not resolved, and counsel may revisit it with `ee/LICENSE` (O1).
- **T3 — D303 amended: from the first ee feature on, the released binary is mixed-licence.**
  (a) The spec's "core code must never import anything from `/ee`" now has one exception, the
  tag-gated assembly file `cmd/fugaro/ee_on.go`; "if `/ee` is removed, the core still builds"
  holds for builds without the tag. (b) The release that first sets the `ee` tag ships commercial
  code to every user of the archive, so in that same release the Homebrew cask `license`, the
  plugin manifest `license`, the README, `LICENSING.md` and the website licence elements must say
  the binary carries code under the enterprise licence; a test (plan L2.1, from 0.11.0) refuses a
  non-empty tag list without those labels, and the label change is its own slice (plan L5) with an
  OWNER-RUN counsel review. (c) A user who wants a pure-MIT binary builds from source without the
  tag; whether to also publish a pure-MIT artifact is open for that release. (d) The repository's
  `go vet -tags` loop in `ci.yml` does not vet `//go:build ee` files today; the workspaces plan adds
  the tag to it when the first such file lands.
- **O1 — Counsel (OWNER-RUN, task L0.1):** the `ee/LICENSE` text and licensor line (T1), the CLA
  text for the hosted app (D304), F3 (a) and (b), and whether D307's notices meet the dependencies'
  terms.
- **O2 — D301 and GitHub's detector.** F27: GitHub reports `other` for a repository whose root
  `LICENSE` carries a scope statement ahead of the MIT text. D301's statement sits after line 1 but
  is still extra text, so Check 130 will most likely show "other" rather than "MIT" (U1). The owner
  either accepts that (the file's own words state the licence) or takes D301's alternative
  (`LICENSE` untouched plus a separate statement). **Ruled 2026-10-24, not the recommendation:**
  D301's alternative: `LICENSE` untouched, the statement in a new root `LICENSING.md`. D301 is
  marked amended; T2 records what it gives up; the plan's L1.3, L1.5 and Check 130 changed.
- **O3 — The hosted CLA app** is an install the owner makes (task L0.2) and is not on the 0.11.0
  critical path: `CONTRIBUTING.md` says no outside pull request is merged before its author signs,
  which holds whether or not the app is installed yet.

## UNVERIFIED

- **U1** — **Moot after O2** (`LICENSE` is not changed). What stays unverified is only that GitHub
  keeps reporting MIT once `LICENSING.md` sits beside it; Check 130 confirms it.
- **U2** — How pkg.go.dev classifies the module root and `ee/` packages after the change. Not part of
  Check 130 after O2 (`ee/` holds no package at 0.11.0); looked at when the first `ee/` package lands.
- **U3** — That a nested `ee/go.mod` module may import the root module's `internal/...` packages
  with this toolchain (`go 1.27`).
- **U4** — That the packed console (`console-v0.110.0` `.dmg`/`.AppImage`/`.deb`) carries no Fugaro
  licence text; read from `console/package.json`'s `build.files`, not by unpacking an asset.
- **U5** — Whether an Ed25519 public-key literal trips the image secret scan
  (`images/trivy-secret.yaml`) or GitHub secret scanning.
- **U6** — Whether any mobile build is already in a store's testing track (no `mobile-v*` tag
  exists; store state is outside the repository).
- **U7** — **Resolved 2026-10-24** (F27): both texts read over HTTPS, URLs and commits cited.
- **U8** — Which source-available licences are published expressly for reuse, and their terms
  (T1 choice (ii)); not read.
