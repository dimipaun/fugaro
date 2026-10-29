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
**D300–D311** are used (D311 on 2026-10-26) and **D312–D319** are reserved and unused; **D531**
is the owner's ruling of 2026-10-29 (the coordinator leaves the Terms' Core), written at the
owner's own number rather than inside this block: it is above the highest number reserved on
`main` (the secrets-brokering work's D519), **D520–D530** are allocated nowhere and stay unused as
D272–D299 are, and this block's D312–D319 stay reserved; checks **131–134** are reserved
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
  - **Scope.** Every **tracked** file under `ee/` (`git ls-files`), skipping no directory
    (dot-directories, `_`-prefixed directories and `testdata/` included, unlike the creds walker),
    symlinks refused; a tracked file missing on disk is skipped (the core-without-ee workflow
    deletes `ee/` without committing).
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
  the tag to it when the first such file lands. (e) The three published images
  (`images/base/Dockerfile`, `images/history/Dockerfile`, `images/credentials/Dockerfile`) each
  build their own `fugaro` with `go build` and no tag: a second release build whose `fugaro` stays
  untagged, so a run's container never carries ee code unless the workspaces plan decides
  otherwise (an input for that plan).
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

## Amendment 2026-10-26 (D311)

**D311 — the owner's ruling of 2026-10-26, not a recommendation of this file.** The desktop
console, the mobile app and `ee/` carry one business licence, the same text in three files with
only the product's name changed: [`console/LICENSE.md`](../../console/LICENSE.md) ("Fugaro
Console"), [`mobile/LICENSE.md`](../../mobile/LICENSE.md) ("Fugaro Mobile") and
[`ee/LICENSE`](../../ee/LICENSE) ("Fugaro Enterprise Edition"). The ruling's parts:

- **The terms are the enterprise terms F27 reads**, adopted in substance from vendor B's
  `ee/LICENSE` and drafted in its structure, not copied: production use needs the Fugaro
  Subscription Terms (or another written agreement) and a subscription for the correct number of
  user seats; modifying and publishing patches is allowed, with the licensor keeping the rights in
  them; copying and modifying for development and testing needs no subscription; nothing else is
  granted; third-party components keep their own licences; an "as is" disclaimer.
- **Seats are users of the component**: one seat per person who uses it.
- **No client-side carve-out.** Vendor B's text puts client-side assets under MIT; this one does
  not. The console's renderer bundle and everything its windows load are under the business
  licence, and so is the mobile app's bundle.
- **From console 0.113.0.** Console releases up to and including 0.112.0 stay under the MIT
  licence they were released under, and so does every directory's source as published before the
  change (F4). No mobile release has been published (`docs/release-streams.yaml` holds the stream
  at `0.0.0` and no `mobile-v*` tag exists), so the app's licence applies from its first release.
  Nothing under `ee/` has ever been released.
- **Applied before counsel has read it.** Each file's first line is `DRAFT FOR COUNSEL REVIEW: not
  yet reviewed by counsel`, and the owner removes it when counsel has read the text. The
  Subscription Terms do not exist yet: each licence names them as `<URL TO BE PUBLISHED>`, and the
  owner publishes them or replaces that sentence before a component is distributed under it.

**What it changes here.** **D302 is reversed for the console and the mobile app**: they no longer
stay core MIT. F4's fact stands: what was published under MIT stays MIT, and anyone may keep using
and forking it. The console is the first component licensed outside the MIT core after the `ee/`
skeleton. **T1 and D305:** `ee/LICENSE` lands now as a draft instead of after counsel; task L4.1
is brought forward with that difference (its hash test against an approved text waits for
counsel), and the reserved state of 0.11.0 ends: `TestDocsEEReserved` became
`TestDocsEELicensed`, and the staged `core-without-ee.yml` skips the renamed test. **D309:** the
boundary tests under `internal/eeboundary/` are unchanged and stay green; no boundary check yet
covers `console/` or `mobile/`, which are separate npm projects outside the Go module (F11), and
`shared/` stays MIT although the mobile app imports it and the core's Go tests read it, so a
module moving between `mobile/` and `shared/` changes licence (an open item for the owner).
**D303 and T3** are unchanged: the released CLI binary carries no `ee/` code until a release sets
the `ee` tag.

**The contribution rule, changed 2026-10-26 by the owner.** The owner confirmed being the sole
copyright holder of `console/`, `mobile/`, `shared/` and `ee/` (no outside contributor in their
history, F3), including the code committed under each of the owner's three addresses; one address
is used from here on. `CONTRIBUTING.md` now says: the core takes contributions under MIT with the
signed CLA, as before; `console/`, `mobile/` and `ee/` take them from anybody who accepts that
directory's licence and has signed the same CLA, which must grant the relicensing rights for those
directories. **D305 consequence:** the sentence that `ee/` "takes no contributions while it is
reserved" (plan L1.4) is gone with the reservation's licence state. The CLA text is counsel's
(O1) and the bot install is the owner's (L0.2, O3), so an outside pull request to these
directories waits until the bot is live.

Besides F27's URLs, the two vendors are named once more, in this note only: the draft is adapted
in structure from GitLab's and PostHog's published `ee/LICENSE` texts, and whether that adaptation
is permitted is one of counsel's questions (T1 (a)).

## Amendment 2026-10-27 (the CLA text)

The CLA text that O1 and D304 leave to counsel is drafted in [CLA.md](CLA.md) on the owner's
request of 2026-10-26/27, awaiting counsel: it grants the Project Owner the relicensing rights the
contribution rule above requires, for the core and for `console/`, `mobile/` and `ee/`. Its
governing law and venue are Ontario, Canada (the owner's ruling of 2026-10-27); counsel confirms
the wording and its enforceability against contributors outside Canada. Plan task L0.1's CLA-text item is drafted;
L0.2, the owner's CLA Assistant install, is written out as a checklist in
[implementation-plan.md](implementation-plan.md#l02--install-the-hosted-cla-app-owner-run). The
`[CLA URL TO BE PUBLISHED]` placeholder was in the three licence files until the owner created the
Gist; the amendment of 2026-10-29 below fills it.

## Amendment 2026-10-28 (the CLA is final)

Counsel reviewed [CLA.md](CLA.md) and blessed it as-is, with no operative clause changed. The draft
banner and the `(draft)` marker on its version line are gone, so the file is the agreement as
counsel approved it. The `[CLA URL TO BE PUBLISHED]` placeholder stayed in the three licence files
until the owner created the Gist (filled on 2026-10-29, below), and the owner's CLA Assistant
install (L0.2) is unchanged; the note at the top of CLA.md tells the owner how to keep the Gist in
step with the file.

## Amendment 2026-10-29 (the CLA's URL is published)

The owner published the contributor licence agreement as a Gist (one Markdown file, `fugaro-cla.md`,
renamed from `gistfile1.txt` before publication) and gave the URL to be used exactly as given:
https://gist.githubusercontent.com/dimipaun/d5bb5db937f973b25be5778ce14ec8dd/raw/64918c7fc0f782327db82e7e790db53c3d47d09f/fugaro-cla.md.
That is a raw link pinned to one revision of the Gist, and the owner read the Gist's file as
byte-identical to the agreement block of [CLA.md](CLA.md), a trailing newline aside (U9: no test in
the tree can check a network resource). `[CLA URL TO BE PUBLISHED]` is
replaced by that URL in `ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md` and the byte copy in
`LICENSES/LicenseRef-Fugaro-Enterprise.txt` -- the placeholder token the only change, so the one
wording is what it was and `ee/LICENSE` and the `LICENSES/` copy stay byte for byte -- and in the pin of
`internal/cli/docs_ee_reserved_test.go`. Plan task L0.2's checklist item for it is ticked in
[implementation-plan.md](implementation-plan.md#l02--install-the-hosted-cla-app-owner-run).
**Because the URL names a revision, editing the Gist later does not change what the licence files
say**: a new revision needs a new URL written into the licence files, which is a change counsel
should see. The Subscription Terms page is still not published (L4.3), CLA Assistant is still to be
installed (the CONTRIBUTING gate), and `docs/ee-reorg/` is untouched: it is on another branch.

## Amendment 2026-10-28 (the CLA Assistant install)

The owner installed CLA Assistant on `dimipaun/fugaro`. What that closes is the half of L0.2 a
reader can see: the signing link is published in `CONTRIBUTING.md`'s `## Licensing of
contributions` section, and the app's badge is in the README's opening, so the requirement and the
place to sign are on the two pages a contributor reads first. L0.2's remaining steps — the Gist's
raw URL in the three licence files (L4.4), the allow-list, the status on pull requests and the test
pull request from a second account — are the owner's own session and are unchanged; the install
checklist is in [implementation-plan.md](implementation-plan.md#l02--install-the-hosted-cla-app-owner-run).

## Amendment 2026-10-27 (the Subscription Terms draft)

Counsel's licence text of 2026-10-27, in `ee/LICENSE`, `console/LICENSE.md` and
`mobile/LICENSE.md`, makes production use depend on the Fugaro Subscription Terms "available at
[URL TO BE PUBLISHED]" (or another written agreement with the Licensor) and a valid subscription
for the correct number of user seats, and leaves to those Terms the Licensor's identity, the
further description of a user seat, the ownership of subscribers' modifications (clause 2.1), any
patent licence (clause 10.1) and what may be kept after termination (clause 11.3).
[subscription-terms.md](subscription-terms.md) is a **first draft of those Terms for counsel to
review: not reviewed by counsel and not in force**, with every commercial term in brackets for the
owner, a list of the owner's commercial questions and a list of questions for counsel. Its defined
terms follow the licence's ("Agreement", "Licensor", "production", "user seat"); it treats the MIT
core as outside the Subscription; and it describes the licence key only as D308's design intent
(offline, signed, no phone-home), not as something built.

What it changes here: nothing is decided. The licence files are not edited; replacing their
placeholder URL is plan task L4.4, after counsel approves the text and the owner publishes it on
the website (L4.3). Its statements about telemetry were read from the tree on 2026-10-27 and two
are marked UNVERIFIED in the draft itself: whether the released console makes an update check (the
design allows one; no updater was found in its main-process code), and what a first mobile release
sends (no analytics or crash-reporting library is declared today). The contributor licence agreement
is drafted separately in [CLA.md](CLA.md) and the draft does not depend on its text.

## Amendment 2026-10-27 (the Subscription Terms follow the owner's answers)

The owner answered the draft's commercial questions in a session on 2026-10-27; the answers, with
the prices, currencies, discounts and the business plan, are kept in the owner's private business
repository and not here. [subscription-terms.md](subscription-terms.md) now follows them (plan task
L4.5). What changed in the draft:

- **Licensor and seller** (section 1): a new dedicated entity, incorporated before the first paid
  sale, with the copyright assigned to it; a merchant of record may sell self-serve purchases under
  its own buyer terms; Enterprise is invoiced directly.
- **Plans and free use** (section 4): Community, Team and Enterprise; one free seat per organisation
  without a subscription, for proof of concept and evaluation; Team free for entirely open-source
  teams (checked from the repository URLs) and academic institutions; discounts for nonprofits and
  startups by application, their amounts on the Order Form; the free seat's three-month key is the
  trial.
- **Seats** (section 5): anyone who launches jobs by any means, the command-line tool included, as
  a pricing rule between the parties; the command-line tool stays MIT; viewers free once that role
  exists (roles are a separate design); seat moves, prorated additions, reductions at renewal, a
  Team self-certified true-up and an Enterprise-only audit.
- **Term, fees, refunds, support** (sections 6, 7 and 10): renewal, refund and support rules as the
  owner decided; no warranty beyond "as is" (section 13.3); no patent licence (section 3.6); no
  use of a customer's name without consent (section 20).
- **Keys** (section 8): every user, free users included, holds a signed key that is still checked
  offline; keys are issued automatically by a hosted signing service the Licensor operates, with
  the sign-up data described; a 30-day grace period. This supersedes, for the Terms, the "minting is
  OWNER-RUN" line of L3.1, which the licence-key design note follows when it is written.
- **After a lapse** (section 10.4): no perpetual fallback; after the grace period the console and
  mobile app are read-only and paid features are off, while the MIT command-line tool keeps
  launching and cancelling jobs. This reverses, for the console and the mobile app, the earlier
  position that launching is never gated.

Still open: the Enterprise seat floor (the owner's) and the late-payment interest rate (counsel's).
The draft's counsel list gains the items the session added. The licence files, the CLA and
`LICENSING.md` are not edited, and the draft is still **not in force**.


## Amendment 2026-10-27 (the Subscription Terms URL)

The owner chose `https://fugaro.dev/legal/subscription-terms` as the Subscription Terms URL and had it written into the four licence files (L4.4). The page is published only after counsel approves the text (L4.3); until then the link does not resolve. The CLA's placeholder was unchanged by this amendment; it is filled by the amendment of 2026-10-29 above.

## Amendment 2026-10-29 (the coordinator leaves the Terms' Core)

The owner ruled (**D531**, 2026-10-29) that the coordinator moves under `ee/`, and that the
Subscription Terms must stop saying it is Core **now**, without waiting for counsel's next revision
of the text; the owner tells counsel, this being the owner's call and not a recommendation.

**The move is planned, not landed.** `ee/` still holds only `ee/LICENSE` and `ee/README.md`, so
under section 2's path definition ("the parts of the Fugaro repository outside `ee/`, `console/`
and `mobile/`") the coordinator is part of the Core today, and its released MIT versions stay MIT.
The edit is the owner's instruction that the Terms stop reading the other way now; the move itself
ships with the S1 tasks named below.

**The one sentence changed** is section 9.1 of [subscription-terms.md](subscription-terms.md), in
the list of what runs where:

> before: **…the optional credentials service and the coordinator are designed to run in the
> Customer's own cloud project (both are part of the Core today, not of the Software).**
>
> after: **…the optional credentials service and the coordinator are designed to run in the
> Customer's own cloud project (the credentials service is part of the Core today, not of the
> Software).**

Only the words that classified the coordinator as Core are gone: the coordinator keeps its place in
the sentence about where it runs, the credentials service keeps its classification (it is still
inside the three-directory boundary's complement), and no other wording in the draft changed. A
**Note for counsel** beside section 9.1 carries the dated note — *edited 2026-10-29 on the owner's
ruling D531 (counsel not yet consulted: tell counsel in the next revision round)* — and, being a
drafting note, it is cut from the published page. `website/content/subscription-terms.md` is
regenerated from the document with `go run ./internal/clidoc`, so the page and the draft stay in
step, and its text below the status line is the agreement alone.

**Not changed.** The four licence files are untouched (D531 edits Terms text, not the licence), as
are the decision rows that record the earlier answers (D302's "stay core MIT", D434 and T10 in
[subscriptions/reconciliation.md](../subscriptions/reconciliation.md)): they are what was decided
then. The documents that record the moves are tasks S0.3 and S0.4 of
[subscriptions/implementation-plan.md](../subscriptions/implementation-plan.md); the moves
themselves are S1.1 (the design), S1.4 (the coordinator's command and library), S1.5
(`fugaro report`) and S1.9 (the key-gated job image). Counsel's question
about the licence's own coverage sentence and the coordinator's released MIT versions (F38, T10)
is untouched by this edit.

## Amendment 2026-10-29 (counsel's restructured Subscription Terms)

Counsel returned the Subscription Terms restructured by plan, dated 2026-10-28 by the document
(its text carries no date or version of its own). [subscription-terms.md](subscription-terms.md)
now holds that text as written, in Parts A (General) to G (Legal terms for every plan), sections 1
to 34, with the earlier draft's drafting notes and its two question lists replaced by one list of
the points still open. `website/content/subscription-terms.md` is regenerated from it, and the page
renders the Part headings above the sections they group.

**Where the earlier sections went** (earlier draft of 2026-10-27, then counsel's numbering):

| Earlier draft | Counsel's text |
| --- | --- |
| 1 Parties and acceptance (1.4 Who sells) | 1 Parties and acceptance; who sells moves to 3.3, 11.5 and 13.1 |
| 2 Definitions | 2 Definitions |
| 3 What the Subscription grants | 4 What the Subscription grants (3.3 becomes 5.2 and 9.2; 3.6 becomes a patent licence, 4.5) |
| 4 Plans and Order Forms | 3 Plans, 10 Community and Free Plans, 11 Order Forms, 17 Discounts and trials |
| 5 User Seats | 5 User Seats; 11.3 and 11.4 seat changes; 16 True-up; 20 Audit |
| 6 Term and renewal | 14 Term and renewal (Team); 19 Enterprise commercial terms; 12.4 Price changes; 27.1 Duration |
| 7 Fees, payment and taxes | 12 Fees, payment and taxes; 15 Refunds |
| 8 License Keys | 6 License Keys and offline activation; 7.5(b) sign-up data |
| 9 Customer Data and privacy | 7 Customer Data and Privacy (9.3 Telemetry becomes 7.3 Crash reports and diagnostics) |
| 10 Support and updates | 8 Support and updates; 9 After the Subscription ends |
| 11 to 14 | 21 to 24 |
| 15 Indemnification (placeholder) | 25 Indemnification |
| 16 Confidentiality | 26 Confidentiality |
| 17 Suspension and termination | 12.6 Suspension; 27 Duration and termination |
| 18 to 23 | 28 to 33 |
| 24 Governing law and venue | 34 Governing law, venue and language |

**Held back from this repository.** Counsel's text states a late-payment interest rate (section
12.2) and a liability cap for the Community Plan, Free Plans and Beta Features (section 10.5).
Neither figure was published here before, so both stand as bracketed items until the owner
decides; every other figure in counsel's text was already in the earlier draft.

**Open**, as the list after the agreement records: counsel's comment on section 4.4
(Modifications); the provisions counsel's document highlights (5.1(i), 5.3 and 5.5); the owner's
rulings counsel's text does not carry (read-only use that can still cancel, T12; one key per
organisation; no covered-versions date, O4; the free seat per legal person, T15); crash reports
the Customer cannot turn off (section 7.3); publicity without consent (section 30.1); the
coordinator named as Core in section 7.1 (D531); and whether the licence files need a matching
revision now that the Terms no longer cite the licence's clauses. The licence files are not edited.

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
- **U9** — That the published Gist's file is byte-identical to the agreement block of
  [CLA.md](CLA.md), a trailing newline aside (the 2026-10-29 amendment). The owner read the two
  against each other when publishing; **nothing in the tree can check it**, because every test here
  is hermetic and reads no network. Re-read it if the Gist is ever re-published:
  `sed -n '/BEGIN AGREEMENT/,/END AGREEMENT/p' docs/licensing/CLA.md` against
  `curl -s https://gist.githubusercontent.com/dimipaun/d5bb5db937f973b25be5778ce14ec8dd/raw/64918c7fc0f782327db82e7e790db53c3d47d09f/fugaro-cla.md`.
  A mismatch would not reach a licensee through the licence files until a new revision's URL is
  written into them, which is the step counsel sees.
