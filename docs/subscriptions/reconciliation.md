# Subscriptions: reconciliation

Checked against `origin/main` at `252b5c46a`, 2026-10-27, which holds counsel's licence text
(`ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md`, #1128), the contributor licence agreement
(`docs/licensing/CLA.md`) and the Subscription Terms draft for counsel
(`docs/licensing/subscription-terms.md`: first draft #1131, **amended to the owner's answers by
#1146**, licensing task L4.5, done). Where a finding below describes the first draft, it says so;
T19 states what the amended draft settles and what it leaves. **Every decision below is answered by the owner
(2026-10-27); nothing is implemented, and no task is approved.** Where this file disagrees with
[spec.md](spec.md), this file wins. Nothing here is legal advice: statements about licence terms are
facts found in the repository or in a published text, stated for the owner and counsel to weigh.

**Business material stays private (D403, D430).** The repository is public. Prices, currency
amounts, discounts offered, the merchant of record's fee rates, the Enterprise floor and minimum,
refund and renewal figures beyond those the Terms draft already prints, and the entity's tax plan
are recorded **in the private business repository** only. Where an owner's answer carries such a
figure, the cell below records its structure and says "price: private" or "figures: private".

**Numbers this work holds:** decisions **D430–D459** and checks **170–179**, taken above the
go-to-market work's D400–D429 and Checks 160–169 (`docs/go-to-market/README.md`) and the Bitbucket
credentials work's D360–D389 and Checks 145–154, on `main` and in the docs of every open PR.
**D430–D448** are written and answered; **D449–D459** are reserved and unused. **Checks 170–179**
are named by the plan, owner-run and not run. Findings are
**F1–F47**, resolutions **R1–R6**, tensions **T1–T26** (T1–T9 from the first reading, T10–T26 from
the answers; **all ruled 2026-10-27**, see "Rulings"; **T27**, a consequence of the rulings, **ruled
2026-10-29**), the owner's answers to the Terms draft's
questions **B1–B17**, UNVERIFIED **U1–U12**. The rulings were recorded against `origin/main` at
`d6f97594c` (which adds #1140 and #1148 to `252b5c46a`).

## Headline: spec concept → what the repository already has

| Spec concept | What exists at `252b5c46a` |
| --- | --- |
| Paid components under the Functional Source License (FSL), MIT after two years | Counsel's business licence of 2026-10-27, one text in `ee/LICENSE`, `console/LICENSE.md` and `mobile/LICENSE.md`: production use needs a subscription for the correct number of user seats (1.1), development and testing are free (3.1), no time conversion, no competing-use clause (F1, F2). **Kept (D431)** |
| Core stays MIT; paid code in its own directory with its own licence | Exists: root `LICENSE` (MIT), `LICENSING.md`, `ee/` with its licence and README, `internal/eeboundary/boundary_test.go`, and the one allowed route `cmd/fugaro/ee_on.go` → `ee/register` (D303 as amended), which **does not exist yet**: `ee/` holds no code (F3) |
| Community: console and mobile app for a single user, free | Licence 1.1 needs a subscription for every production user (F4). **D432:** one free seat per organisation that has no subscription |
| Seat = launcher, operator or admin; viewers free | Licence: "one person who uses the Software"; the Software is not the CLI (F5). **D433:** anyone who launches jobs by any means, as a pricing rule |
| Viewer/operator/admin roles | None: no sign-in in the console, IAM lists that grant, a device allowlist with no identity (F6). **D446:** a separate system-wide design |
| Seats measurable from Firebase Auth and allowlists | Only an estimate is possible (F7). **D439** |
| Safety features free | Exists, MIT, in every 0.11.x and 0.12.0 release (F8) |
| Per-project coordinator: Team | MIT, released v0.11.0–v0.12.0, imported by the launch path and run by a scheduled job from an untagged image (F9, F31, F32). **D434:** future versions move to the business-licensed folder; **as ruled by T10**, the command and the library move |
| Central orchestrators, hosted MCP server: Team | Not on `main`; orchestrators on branch `central-orchestrator`, open PR #831 building P2 in core (F10, F11, F38). **D435:** born in the business-licensed folder |
| Audit and spend-history exports: Enterprise | `fugaro report`, `fugaro runs digest` (0.12.0) and the console's Spend screen share `internal/report`; MIT and released (F13, F33). **D444:** future versions of the spend reports move |
| Signed key, local check, no phone-home | D308 design, not built; no release date stamped (F16). **D438 as ruled:** one key per organisation, expiry only, hand-minted until the entity and the owner-operated service exist (O4, T13) |
| Perpetual fallback on lapse | Licence 11.1 ends production use at lapse (F15). **D436:** no fallback; read-only after the grace period in the console and the app |
| Price, billing, refunds, programs, entity, merchant of record | Brackets in the Terms draft; the answers' figures are private (D430, D443) |

## Findings

**F1 — Rule: the licence vehicle.** The spec ships paid features under the FSL. The tree holds
counsel's text of 2026-10-27 (`ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md`, identical
wording; #1128), adopted after D305 and D311. It gates **production use** on "a valid Fugaro
Enterprise Edition subscription for the correct number of user seats" (1.1), frees development and
testing (3.1), grants nothing else (5.1), ends the production right when the subscription ends
(11.1), lets the Agreement prevail (12.1) and is governed by Ontario law (14.1). *Consequence:*
D431.

**F2 — Wrong (in part): what the FSL does.** The FSL-1.1-MIT template (read 2026-10-27 from the
licence site's public repository, `FSL-1.1-MIT.template.md` at commit `6a886da`) grants use for any
"Permitted Purpose", which **expressly includes "your internal use and access"**, carries a patent
licence, has no clause against working around a key check, and converts to MIT irrevocably two
years after the software is made available. *Consequence:* under the FSL a customer's internal
production use of every paid feature is lawful without paying. D431 (closed: the owner kept
counsel's licence).

**F3 — Exists: the open-core structure.** `LICENSING.md` states the split; `ee/README.md` reserves
`ee/` for net-new organisation features ("not a place for anything that lives in the core today");
`.claude/rules/ee.md` repeats "Nothing moves out of the core into `ee/` (D302)";
`internal/eeboundary/boundary_test.go` (`TestCoreNeverImportsEE`, `TestCoreNeverEmbedsEE`,
`TestNoModuleRouteIntoEE`) forbids any core route into `ee/` except `cmd/fugaro/ee_on.go`
importing `ee/register` under `//go:build ee`. Neither file exists: `ee/` holds only `LICENSE` and
`README.md`. *Consequence:* every "business-licensed folder" answer (D434, D435, D444) reaches users
only through `ee/register` and extension points the core must first expose (S1).

**F4 — Wrong: "Console and mobile app, single user: Community (free)".** Clause 1.1 requires a
subscription for production use by anyone; only development and testing are free (3.1).
`LICENSING.md` puts the console under it from console 0.113.0 and the app from its first release.
No release under it exists: the latest console tag is `console-v0.112.0` (MIT), and there is no
`mobile-v*` tag (`git ls-remote --tags origin`). *Consequence:* D432; a free seat must be a no-fee
subscription under the Terms or a licence change (T13).

**F5 — Wrong: the seat definition.** The licence: "A user seat is one person who uses the
Software, as further described in the Subscription Terms" (1.1). The first Terms draft's §5.1 counted
a person who "run[s] or operate[s] any part of it in Production Use"; the amended §5.1 states D433's
rule ("Every person who launches Jobs for the Customer, by any means") as a pricing rule. The licence's
Software is the console, the app and `ee/` only, so a person who launches jobs with the MIT CLI
uses no licensed Software. *Consequence:* D433 makes the seat a pricing rule of the Terms (T14).

**F6 — Wrong: viewer/operator/admin roles exist.** None does:
- **The console** has no sign-in: it spawns the installed `fugaro` with the operator's ambient
  cloud identity (`docs/design/desktop-console.md` §1, §3.1) and treats every identity as a
  possible admin (§3, rule 6 of §3.4, D14).
- **The CLI** reads `terraform.launchers` and `terraform.operators` (`operatorMember`,
  `launcherMember` in `internal/cli/roles.go`) only to choose what a table shows; they grant
  nothing.
- **The Firebase module** has four IAM lists (`deploy/terraform/gcp/modules/firebase/variables.tf`:
  `launchers`, `operators`, `admins`, `budget_admins`); launchers and operators get
  `roles/firebasedatabase.viewer`, admins and budget admins `roles/firebasedatabase.admin`.
- **The mobile app** pairs one Firebase Auth uid per device through `fugaro pair`, which is
  approved and **not built** (no `pair` command in `internal/cli`; `docs/mobile-auth/README.md`).
There is no viewer anywhere, and every person on any list can launch. *Consequence:* D446; T16.

**F7 — Wrong: seats are measurable from Firebase Auth and the allowlists.** Firebase Auth is used
by the mobile app only, with custom tokens whose claims a launcher chooses (`userClaim` in
`internal/budget/rules/rules.go`); the device node holds no person identity; IAM lists accept
`group:` members; run requesters (`RequestedBy` in `internal/cli/run.go`, `byPerson` in
`internal/budget/rollup.go`) are self-declared (`roles.go` calls a recorded identity "untrusted").
*Consequence:* an advisory estimate only (D439), and under D433 it must count every launcher,
including agent launches recorded under a person's configured identity (T14).

**F8 — Exists: the safety features are free core.** Spend caps and the kill switch
(`internal/budget`, `internal/policy`, `newBudgetKillCmd` in `internal/cli/budget.go`), the
credentials service (`internal/creds`, `internal/credsvc`), rigor modes (`internal/rigor`) and
`fugaro watch` are MIT and in every release from `v0.11.0` to `v0.12.0`. *Consequence:* R4.

**F9 — Rule: the coordinator is MIT, released and on `main`.** `internal/coordinator/` and
`internal/cli/coordinator.go` are in tags `v0.11.0` through `v0.12.0`; `internal/cli/coordinator_view.go`
first ships in `v0.12.0`, whose notes add `fugaro coordinator status --from-store`
(`docs/releases/v0.12.0.md`). D302 (2026-10-24, the recommendation) kept the coordinator and the
central orchestrators in core MIT; D400 (2026-10-26, not the recommendation) asked to mark them as
covered by the business licence in place and to record a note to move them into `ee/` later
(`docs/go-to-market/reconciliation.md`); counsel's text (scoped to the three directories) does not
cover in-place files. D434 therefore carries out D400's later move for future versions. *Consequence:* D434; T10.

**F10 — Gap: shared central orchestrators are not built on `main`.** `docs/central-orchestrator/`
holds the spec and plan (P0–P9); open PR #831 builds P2 (`fugaro orchestrator add|rm|ls`) as core
code. *Consequence:* D435; T20.

**F11 — Gap: the hosted MCP server is not built.** The coordinator plan's P8 (the MCP front door)
is not started. *Consequence:* D435.

**F12 — Gap: Workspaces have no docs package and no code; the licence has no tiers.** Team versus
Enterprise is a matter of the key's feature list and the Order Form. *Consequence:* D438.

**F13 — Exists: spend-history and audit exports.** `fugaro report` exports spend `--json` and
`--csv` (`internal/cli/report.go`); the credentials service writes an audit line per issuance and
refusal (`internal/credsvc/audit.go`). Both MIT and released. *Consequence:* D444; T11.

**F14 — Exists: the Watch dashboard is core.** `fugaro watch` is MIT; the console is its graphical
form and is under the business licence from 0.113.0. *Consequence:* none beyond D432.

**F15 — Rule: lapse.** Licence 11.1 ends production use at lapse. The first Terms draft's §10.4
offered Option A (that default) and Option B (perpetual fallback). The amended §10.4 removes Option
B, grants a 30-day grace period and a read-only use of the console and the app in which a User
"cannot launch, cancel or change anything", and §17.4 carves the read-only use out of the
destruction duty. *Consequence:* D436 is drafted; T12 ruled that read-only keeps cancel and the kill
switch, which the one Terms revision (S0.4) writes into §10.4.

**F16 — Gap: the licence key.** D308: an Ed25519-signed claim set verified offline, key id for
rotation, grace, no revocation, no phone-home; the check in `ee/` behind `ee/register`. The design
note (licensing plan L3.1, `docs/licensing/licence-key.md`) is not written. Releases stamp
`internal/cli.Version` (`internal/cli/root.go`) and `internal/cli.Commit`
(`internal/cli/releaseinfo.go`) through `.goreleaser.yaml`'s `ldflags`, and no release date.
*Consequence:* D438; S2.

**F17 — Gap: a ledger.** An offline key and a true-up need a record of what was issued. Under
D440 the signing service holds it, with personal data (T13). *Consequence:* R5 as amended.

**F18 — Gap: self-serve minting needs a service holding the signing key.** Licensing R8 and
`.claude/rules/ee.md` keep minting OWNER-RUN with the private key off every machine an agent
reaches. *Consequence:* D440 (the owner chose a hosted service); T13; R5 amended.

**F19 — Exists: licence state cannot stop a run.** The run's images build `fugaro` with no build
tag (`images/base/Dockerfile`, `images/history/Dockerfile`, `images/credentials/Dockerfile`), so
they carry no `ee/` code and no key check. *Consequence:* R1, which F32 puts under strain.

**F20 — Gap: "read-only" is undefined per feature, and the console is read-only today.** No
feature defines a read-only mode. The console spawns fifteen operations
(`console/src/main/spawn.ts`: `live.watch`, `budget.show`, `spend.report`, `runs.list`,
`runs.stats`, `plan.status`, `run.diagnose`, `run.logs`, `run.logLink`, `secrets.inventory`,
`accounts.inventory`, `cli.version`, `environment.report`, `installation.list`,
`coordinator.status`), all reads: it has no launch, cancel or kill-switch action
(`docs/design/desktop-console.md` D2, D14). The app's cancel is a mailbox action addressed to
the `orchestrator` agent (`shared/mailbox-schema/vectors.json`). *Consequence:* for the console as
built, "read-only after lapse" can only hide or keep paid views (Spend, the coordinator's project
view, seats); console v2's planned launch would be the first action a lapse could gate. T12; S6.1.

**F21 — Rule: who the licensor is.** `LICENSE`, `ee/LICENSE`, `console/LICENSE.md`,
`mobile/LICENSE.md` and `LICENSES/LicenseRef-Fugaro-Enterprise.txt` each say `Copyright (c) 2026`
the owner personally; the licence names the Licensor only through the Terms (§1.1,
`[LICENSOR LEGAL NAME]`); the CLA's "Project Owner" is "the copyright holder named in the Project's
root `LICENSE` file" and anyone to whom that holder transfers the Project or its copyright.
*Consequence:* D441 with B9 (assign now); T18.

**F22 — Exists (amended draft): the merchant of record.** The first draft was a direct
Licensor–Customer agreement whose tax clause assumed the Licensor invoices; the amended draft adds
§1.4 ("Who sells") and a merchant-of-record path in §7.3. *Consequence:* D442 is drafted; the
merchant's buyer terms remain U3.

**F23 — Exists: no competing-use clause is needed.** The business licence grants nothing beyond
development, testing and subscribed production use (5.1), and the Terms §3.7 forbids a hosted
offering. *Consequence:* none.

**F24 — Exists (amended draft): true-up and audit.** The amended draft has §5.6 "True-up (Team)"
and §5.7 "Audit (Enterprise)", as D443 (b) and B12 say. *Consequence:* none.

**F25 — Rule: commercial material in a public repository.** D403 keeps it in the private business
repository. *Consequence:* D430; `spec.md` is an excerpt.

**F26 — Gap: the website.** No pricing page exists (`website/src/pages/`); the Community page's
`#licence` section calls `ee/` "the reserved `ee/` directory" (`community.astro`), pinned by
`website/test/faq-community.mjs`; the privacy page (`privacy.astro`) covers the site and, in its
part on the software, states that the software reaches nothing the project operates (F44).
*Consequence:* S7; T22.

**F27 — Gap: free programs and the business-only clause.** The licence frees development and
testing only, so free production use comes from the Agreement. Terms §1.3 sells to businesses and
organisations only. An open-source maintainer applying with a repository URL and an email may be
an individual. *Consequence:* D445; T15.

**F28 — Rule: supported versions.** `SECURITY.md` "Supported versions": "Only the latest 0.x
release is supported." The amended Terms §10.2: "The Licensor supports the current minor release
and the one before it" (B8). *Consequence:* T23.

**F29 — Exists: the CLA allows relicensing.** CLA §3.2 lets the Project Owner relicense
contributions under any terms. *Consequence:* no CLA change for D434/D444 moves.

**F30 — Open: the spec's own six questions** are D443 (price, minimum, startup discount), D431,
D432, D441, D442.

**F31 — Rule (new): the launch path imports the coordinator.** `internal/cli/run.go` declares each
launch to the coordinator through `coordinator.Reporter{Store: store}.Report` (advisory: a failure
never fails the launch), and `internal/cli/project_cmd.go` uses `coordinator.PathRoot` when it
writes the project rules. The core may not import `ee/` (F3). *Consequence:* moving
`internal/coordinator` into `ee/` would break the MIT launch path unless an extension point is
added. T10 ruled that the library moves too, so the launch declares through a hook `ee/register`
fills (S1.2) and the paths move to a small MIT package (S1.4).

**F32 — Rule (new): the coordinator's scheduled job runs the CLI from a job image.**
`deploy/terraform/gcp/modules/installation/coordinator.tf` runs `/usr/local/bin/fugaro coordinator
sweep` from `var.coordinator.image` (the coordinator plan's P5). The job images build `fugaro`
untagged (F19), and the coordinator job's image **is the history image**: "one CLI binary
serves both jobs" (`internal/infra/firebase.go`, `CoordinatorImageExists`). `images/base/Dockerfile`
copies `cmd`, `internal`, `images` and `deploy/terraform`, never `ee/`, so the image pipeline cannot
build an `ee`-tagged CLI today. *Consequence:* a coordinator in `ee/` needs an `ee`-tagged build
inside the customer's cloud, which would also put `ee/` code into the `budget history` job, or a
new image with its own mirror entry; R1 and the image builds allow neither today (T10).

**F33 — Rule (new): `internal/report` has four consumers.** `internal/cli/report.go` (`fugaro
report`), `internal/cli/runs_digest.go` and `internal/digest` (`fugaro runs digest`, 0.12.0, the
periodic reviews of D320–D326), the console's Spend screen (`console/src/renderer/screens/spend.ts`
draws from `report --json`), and `internal/cli/docs_desktop_console_test.go` (pins the
`report.JSONDoc` shape the console reads). `plugin/skills/working/SKILL.md` teaches `fugaro report
--by …` and `plugin/skills/working/reference/periodic-reviews.md` teaches the digest; the plugin's
tests check every command a skill names against the CLI. *Consequence:* A15's move reaches the
digest, the console and the plugin skill (T11).

**F34 — Rule (new): an `ee`-tagged release relabels every licence label.**
`scripts/notices_test.go` (`TestEETagNeedsTheLabels`) refuses a release that sets the `ee` tag
without every licence label (the plugin manifest, the Homebrew cask and the rest it names) naming
the enterprise licence. D303 as amended is one binary. *Consequence:* once a CLI command lives in
`ee/`, the released `fugaro` binary is mixed-licence; "the CLI is MIT", which D433 and D436 rest on,
holds for the source and for an untagged build, not for the released binary (T21).

**F35 — Gap (new): the key has no user identity to bind to.** The console has no sign-in (F6); the
app's uid is chosen by a launcher (F7); the CLI's identity is the cloud's. D438 asks for a key for
"every user", D440 issues it against an email and a company domain. *Consequence:* the key binds to
an organisation (a domain or an Order Form) and carries a seat count, not a person; "every user
has a key" means every console and app installation holds its organisation's key (T13).

**F36 — Rule (new): a PR edited the four licence files** (#1140 merged on 2026-10-27 as
`dcee3c963`; the sequencing below is history). #1140 ("the Subscription Terms URL
in the four licence files") changes `ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md`,
`LICENSES/LicenseRef-Fugaro-Enterprise.txt`, `LICENSING.md`, `ee/README.md`,
`docs/licensing/subscription-terms.md`, `docs/licensing/implementation-plan.md`,
`docs/licensing/reconciliation.md` and `internal/cli/docs_ee_reserved_test.go`; the copyright-line
commit of B9, the Terms revision run and the licence-document changes touch the same files. *Consequence:* sequencing (T18).

**F37 — Rule (new): post-lapse read-only is not a licence right by itself.** Licence 11.1 ends the
production right "under clause 1" at lapse, and a console used read-only against a production
installation is still production use. The amended Terms §10.4 now grants it expressly, with a note
asking counsel to confirm it fits the licence (counsel question 12). *Consequence:* counsel (S8.4).

**F38 — Rule (new): D302's two subjects are reversed in part.** D302 kept "console, mobile,
coordinator and central orchestrators" in core MIT; D311 later put the console and the app under
the business licence; D434 and D435 now move the coordinator's future versions and the orchestrators.
`ee/README.md` ("not a place for anything that lives in the core today") and `.claude/rules/ee.md`
("Nothing moves out of the core into `ee/` (D302)") state it; `LICENSING.md` reserves `ee/` for
"net-new organisation features".
*Consequence:* those three files change with S0.3, and D302 is amended (T10, T20).

**F39 — Gap (new): free keys expire.** D440 issues a three-month commercial evaluation key
(renewable) and a one-year open-source key. Under D436 an expired key drops the console and the app
to read-only after the grace period. *Consequence:* a free evaluator who does not renew is
read-only; the MIT CLI is unaffected. Stated, not a tension.

**F40 — Gap (new): the key's covered-versions date has no role without a fallback.** D438 keeps
"a covered-versions date" in the key, which in the first proposal carried the perpetual fallback;
D436 drops the fallback. *Consequence:* O4, **ruled 2026-10-27: the date is dropped** (expiry only).

**F41 — Gap (new): no path gets a key into the products.** Licensing L3.1 left key storage open;
there is no CLI key file, no console screen to enter one, no app storage. The console spawns
`report --json` and `coordinator status` (F20), which D434 and D444 would gate, so the key must
reach the spawned CLI; the console's spawn allow-list holds exact argv
(`.claude/rules/console.md`), and a key in argv is visible to every local process. *Consequence:*
S2.1 decides storage and hand-off (never argv); S2.7, S2.8 build entry.

**F42 — Gap (new): paid keys have no issuance path.** D440 covers free keys and the manual queue.
Nothing mints a key on a Team purchase through the merchant of record, reissues it at a monthly or
annual renewal, stops reissue on cancellation, or mints an Enterprise key. *Consequence:* the
signing service takes the merchant's purchase events (signature-verified, idempotent) and the admin
page mints Enterprise keys (S3.1, S3.2, Check 179).

**F43 — Gap (new): the no-key and upgrade states.** D438 gives every user a key. A console 0.112.0
user (MIT, no key) who upgrades to 0.113.0 has none. *Consequence:* S6.1 defines what a console or
app with no key shows, and whether development and testing use (licence 3.1) needs one (T12).

**F44 — Rule (new): the website and its privacy page forbid what D440 adds.** The site's CSP
carries `form-action 'self'` (stated in `website/src/pages/mobile.astro` and held by the site's
tests), so a form posting to another origin is blocked; `website/test/privacy.mjs` rule 5 ("The
project operates no host the software talks to") and the privacy page's "nothing the Fugaro
project operates" describe a project that runs no service. *Consequence:* the CSP, the privacy
copy and rule 5 change only with the owner's approval (`.claude/rules/website.md`); S3.3, S7.3.

**F45 — Rule (new): the CLI reference and the plugin read the untagged tree.**
`TestCliReferenceNamesEveryCommandAndFlag` (`internal/cli/docs_website_cliref_test.go`) checks
`website/content/cli-reference.md` against the untagged command tree, and the plugin's tests check
every command a skill names. After D434 and D444 the untagged tree has no `coordinator` or `report`.
*Consequence:* S1.1 decides which tree the reference and the skills describe.

**F46 — Gap (new): the digest inside a run.** The working skill schedules `fugaro runs digest`
through "a run launched to write the report" (`plugin/skills/working/SKILL.md`). The run images
are untagged (R1). *Consequence:* if T11 moves the digest, a run can no longer write it.

**F47 — Gap (new): the open-source check and the key's binding.** A repository URL and an email
prove no control of the repository, so anyone can claim a well-known public project; and a key
binds to an organisation (F35) that nothing in the product compares with the installation, so one
free key can be shared without limit, and "a subscribing organisation gets no free seat" cannot be
checked. With no revocation a leaked one-year key stays valid. *Consequence:* T13; proof of control
in S3.1; the limits stated in the key section below.

## Tensions from the first reading (T1–T9), as the answers leave them

| # | Tension | State after 2026-10-27 |
| --- | --- | --- |
| T1 | FSL against counsel's licence | **Closed by D431** (counsel's licence kept) |
| T2 | The spec's free tier against the licence's seat rule | **Narrowed by D432/D433**; what remains is T13 and T14 |
| T3 | The Terms' lapse rule against perpetual fallback | **Closed by D436** (no fallback); replaced by T12 |
| T4 | Two seat definitions | **Replaced by T14** (D433) |
| T5 | Which product is free | **Closed by D432** (the MIT core, plus one evaluation seat per non-subscribing organisation, plus open-source teams) |
| T6 | Team features already MIT | **Replaced by T10, T11, T20** |
| T7 | Seller, licensor, copyright holder | **Narrowed by D441 and B9**; sequencing is T18 |
| T8 | Commercial questions | **Closed by D443** (figures private); the startup discount and programs are in the amended Terms §4.3 |
| T9 | "Never block work" against a licence state | **Replaced by T12** |

## Resolutions (one defensible answer each)

- **R1** — Licence state never reaches the run path: the run images build `fugaro` with no `ee`
  tag, and the key check lives only at `ee/register` and in the console and app. A later task pins
  it with a test that the images' build commands carry no tag (S1.1 designs it; F32 is the strain).
  **Narrowed by ruling T10 (2026-10-27):** the run images (base, credentials) stay untagged; the
  coordinator's sweep job runs the key-gated build (S1.7, S1.9). **Amended by D524 (2026-10-29,
  `docs/ee-reorg/reconciliation.md`):** the base and history images are built with `-tags ee` and
  `COPY ee`, once the relabel has landed (E3.2, then E1.4); the credentials image stays untagged;
  no key is mounted.
- **R2** — No phone-home at runtime: validation is offline; no seat telemetry; the anonymous update
  check (`internal/updatecheck`) is unchanged. **Amended by D440:** the only Fugaro-hosted
  component is the issuing service, reached when a person asks for a key, never by the product.
- **R3** — Every version released under MIT stays MIT: the coordinator and the reports of v0.11.0
  through **v0.12.1**, and the console up to 0.112.0. D434 and D444 move **future** versions only;
  **extended by ee-reorg R1 (2026-10-29, `docs/ee-reorg/reconciliation.md`).**
- **R4** — The safety features stay in the MIT core (F8). The kill switch stays reachable from the
  MIT CLI at every licence state, and after ruling T12 also from the console and the app after the
  grace period.
- **R5** — **Amended by D440:** the record of issued keys lives in the signing service's store and
  in the private business repository, never in this repository; the signing key lives only in the
  service's key store, never on a machine an agent reaches, and no agent deploys or operates the
  service (licensing R8 as amended). **Ruling T13 (2026-10-27):** the service is owner-operated in a
  dedicated Fugaro GCP project; until it exists, the owner mints keys by hand through the KMS key
  (S2.10, S3.8).
- **R6** — Tiers do not change the licence text; they live in the key's `features` and the Order
  Forms.

## Decisions (answered 2026-10-27)

Each row: the question, the recommendation and its reason, the alternative and its cost, and the
owner's answer with its date, whether it is the recommendation, and the reason. Figures in the
answers are private (D430).

| # | Question | Recommendation and reason | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D430** | Where does this spec live? (F25) | **An excerpt here**; price points, billing, refunds, SLA hours, the contracting entity and the conflict-of-interest note in the private business repository. *Reason:* D403 | Publish verbatim: costs a public pricing plan before it is validated | **2026-10-27: the recommendation.** A short excerpt here; prices, billing, refunds, SLA hours, the contracting entity and the conflict-of-interest note in the private business repository. *Why (inferred; the owner gave none):* the repository is public and D403 keeps business material private. `spec.md` is that excerpt. **Amended by ruling T24 (2026-10-27):** the contract figures (support targets and hours, renewal, refund and reassignment periods) stay in the Terms; prices, discounts, fees and the Enterprise minimum stay private |
| **D431** | Licence for `ee/`, `console/`, `mobile/` (T1) | **Keep counsel's business licence; drop the FSL.** *Reason:* the FSL frees internal production use (F2) | FSL-1.1-MIT or closed binaries | **2026-10-27: the recommendation.** Keep counsel's licence; drop the FSL. *Why:* approved by counsel and not revisited |
| **D432** | Is the console and app free for one user? (T2, T5) | **A no-fee Community plan in the Terms, one seat per organisation**, counsel confirming clause 1.1 | Paid from seat one | **2026-10-27: partly not the recommendation.** One free seat per organisation, **only for an organisation with no subscription**, intended for proof of concept and evaluation; a subscribing organisation pays for every user and gets no free seat. Teams whose projects are entirely public under an OSI licence get Team free. Counsel confirms it fits the licence. *Why:* the owner replaced an earlier "free for any individual" answer that conflicted with the seat model. Follow-through: T13, T15 |
| **D433** | What is a seat? (T4) | **Keep the licence's words; read-only users excluded in the Terms**; every launcher, operator or admin through the Software is a seat | The spec's wording incl. CLI launchers: costs a seat the licence cannot require | **2026-10-27: not the recommendation.** A seat is **anyone who launches jobs, by any means** (console, mobile, CLI, agent), defined as a **pricing rule in the Subscription Terms**; read-only viewers are free once the viewer role exists (D446). *Why (owner's note):* the CLI is MIT, so this binds only subscribing organisations; counsel words it. Follow-through: T14, T21 |
| **D434** | The coordinator: Team or core? (T6) | **Core stays core**; only new organisation-only parts born in `ee/`; amends D400 | Move future versions into `ee/`: costs D302's "nothing moves out", an MIT fork point | **2026-10-27: not the recommendation.** **Future coordinator versions move to the business-licensed folder**; the released MIT version stays available as a fork point. Amends **D302**; carries out **D400**'s later move. *Why (owner's note):* the released MIT version remains the fork point. Follow-through: T10. **Amended by ruling T10 (2026-10-27):** the command **and the library** move, not only future parts of the command; the sweep job's image carries the key-gated build (amended by D524, 2026-10-29: the history image is built with the `ee` tag; no key is mounted) |
| **D435** | Central orchestrators and the hosted MCP server | **Born in `ee/`**, through `ee/register` | Core MIT as planned | **2026-10-27: the recommendation.** Both start in the business-licensed folder (paid). *Why (inferred; the owner gave none):* not on `main`, not released. Follow-through: T20 (open PR #831) |
| **D436** | Perpetual fallback after a lapse (T3) | **Yes, through Terms §10.4 Option B** | No fallback (licence 11.1) | **2026-10-27: not the recommendation.** **No perpetual fallback.** After the grace period (B17), every console and mobile user drops to **read-only**; paid features are off. The MIT CLI keeps launching and cancelling jobs. *Why (owner's note):* this reverses "launching is never gated" for the console and the app only, which the owner confirmed; counsel confirms. Follow-through: T12, F37. **Amended by ruling T12 (2026-10-27):** read-only keeps **cancel** and the kill switch; it refuses launch and config changes |
| **D437** | What a lapse does in the product (T9) | **Paid features off with a banner; launch, cancel and every core action never gated** | Read-only for every paid feature | **2026-10-27: not the recommendation; merged into D436.** *Why (owner's note):* "Merged into A7" |
| **D438** | The licence key (extends D308) | `tier`, `features`, `seats`, `expires`, `covers_until`; `ReleaseDate` in every build; seats warned, never enforced | D308 as is | **2026-10-27: not the recommendation.** A **signed offline key for every user, including free users**, issued automatically by a **hosted signing service** (D440). Contents: tier, features, seats, expiry, covered-versions date. *Why (owner's note):* the signing service holds the private key and is secured like the credentials service; validation stays offline, so an outage blocks only new keys. Follow-through: T13, F35, F40 (O4). **Amended by rulings O4, T26 and the key binding (2026-10-27):** one key **per organisation** with a seat count; **expiry only** (the covered-versions date is dropped); the next key issues 30 days before expiry |
| **D439** | How seats are counted in the product | **An advisory estimate**: launchers and operators lists plus run requesters over 90 days, beside the licensed seats; nothing stored or sent | Firebase Auth listing; none | **2026-10-27: the recommendation.** *Owner's note:* under D433 the estimate counts **all launchers**. Follow-through: T14 |
| **D440** | The trial and key issuance | **Owner-minted trial keys by hand** | A signing service; an unsigned self-started trial | **2026-10-27: not the recommendation.** A **self-serve key website**. Open source: a repository URL plus an email, checked automatically (every repository public under an OSI licence), a one-year key. Commercial free seat: a company domain plus a work email on that domain (no free-mail domains), a three-month key, renewable, **one per organisation enforced by email domain**. A **manual request form** for special cases (for example free-mail users and multi-seat trials) feeds a **queue that notifies the owner**, who approves and mints keys from an **admin page**. *Why:* resolves the session's conflict between hand-minted keys and a key for everyone (D438). Amends licensing R8 and this file's R5. Follow-through: T13. **Amended by ruling T13 (2026-10-27):** the service is owner-operated in a dedicated Fugaro GCP project and built after the entity exists; **until then keys are minted by hand** (this row's first recommendation, as an interim) |
| **D441** | Who sells and who is the licensor? (T7) | **A dedicated entity before the first paid sale**, holding the copyright, named as Licensor | Interim under the owner's existing company | **2026-10-27: the recommendation.** A new dedicated entity before the first paid sale, named as Licensor in the Terms; with B9, the copyright is **assigned to it now**. *Why (inferred; the owner gave none):* one owner of rights, contracts and the CLA's Project Owner. Follow-through: T18. **Amended by ruling T18 (2026-10-27):** the assignment happens only once the entity exists, as one owner-go commit; nothing before |
| **D442** | Merchant of record | **A merchant of record for Team checkout; Enterprise direct** | Sell directly everywhere | **2026-10-27: the recommendation.** **Paddle** for Team checkout as merchant of record, handling sales taxes on its sales; Enterprise signed and invoiced directly by the entity. Fee rates and the tax plan: private. *Why (inferred; the owner gave none):* no sales-tax registration abroad for self-serve sales |
| **D443** | Commercial terms (T8) | (a) figures private; (b) audit for Enterprise only, Team self-certified true-up; (c) annual discount, prorated additions, reductions at renewal; (d) no startup discount yet | Audit for every plan; a startup discount now | **2026-10-27: partly not the recommendation.** (a) Prices private; (b) formal audit for Enterprise only, Team self-certifies at renewal (true-up); (c) an annual discount (figure private), additions prorated, reductions at renewal; **(d) a startup discount is added** (B13), against the recommendation. *Why:* none given; the startup discount's terms are B13's |
| **D444** | Spend and audit exports for Enterprise | **Only net-new exports**; `fugaro report` and the credentials audit log stay MIT | Charge for existing exports | **2026-10-27: not the recommendation.** **Spend and cost reports (`fugaro report` and similar) move to the business-licensed folder for future versions**; the credentials service's audit log stays MIT. *Why:* none given. Follow-through: T11. **Amended by ruling T11 (2026-10-27):** `fugaro report` moves; **`fugaro runs digest` stays MIT** with its own copy of the period helpers |
| **D445** | Free programs | **No-fee Order Forms on application**, renewed yearly; academic by institutions only | A clause in the Terms | **2026-10-27: partly not the recommendation.** Open source **automatic** (D440's checked key). Academic institutions get Team free and nonprofits a discount (figure private), both by a short application renewed yearly. *Why (inferred; the owner gave none):* open source is checked automatically by D440. Follow-through: T15 |
| **D446** | Multi-user roles (F6) | **A separate design before any build**, on the existing lists plus a read-only list | Roles inside the console only | **2026-10-27: the recommendation, widened.** A separate design first; viewer, operator and admin roles **enforced system-wide, the cloud included**, not just in the console. It creates D433's free viewer. Follow-through: T16 |
| **D447** | How paid features switch on | **The key-gated single binary of D303** (`ee` build tag) | Honour only | **2026-10-27: the recommendation.** Lapse per D436. Follow-through: T21. **Ruling T21 (2026-10-27):** one binary with a mixed licence, relabelled |
| **D448** | Sequencing | **Settle D431–D433 and D436 before console 0.113.0 and the first mobile release** | Release first | **2026-10-27: the recommendation.** D431, D432, D433 and D436 are decided; **counsel signs off D432, D433 and D436 before console 0.113.0 and the first mobile release**. Follow-through: T17. **Ruling T17 (2026-10-27):** those releases ship honour-based, without key enforcement |

## The Terms draft's owner questions (B1–B17)

The owner's answers of 2026-10-27 to the seventeen owner questions of the first Terms draft
(#1131), numbered as there. The amended draft on `main` (#1146) applies them and keeps two open (the
Enterprise seat floor, the interest rate); the last column names the amended clause. Where the
amended draft prints a figure that D430 sends to the private repository, the cell says so (T24).

| # | Terms question | Owner's answer (non-price) | Recommended? | Amended clause |
| --- | --- | --- | --- | --- |
| B1 | Pricing model and tiers | Community (free), Team (self-serve, per seat), Enterprise (annual contract, invoiced) | Yes | §4.1 |
| B2 | Metric | Per seat, as D433 defines it | Yes | §5.1 |
| B3 | Price and currency | Two currencies, fixed prices in each; the Enterprise minimum. **Price: private** | n/a | §4.2, §7.1 (brackets) |
| B4 | Billing period | Team monthly or annual; Enterprise annual only; additions prorated from the day added; reductions at the next renewal | Yes | §5.4, §5.5 |
| B5 | Term and renewal | Monthly renews monthly, cancel any time, effective at period end; annual auto-renews for a year unless cancelled before a notice period; Enterprise per contract. **Figures: private here; the amended §6.2 prints the notice period (T24)** | Yes | §6.1–§6.3 |
| B6 | Free tier or trial | The three-month commercial key (D440) is the self-serve single-seat trial; multi-seat Team trials on request through the manual queue. *Owner's note:* resolves the conflict with D440 | **No (not the recommendation)** | §4.4 |
| B7 | After lapse | As D436 | **No (not the recommendation)** | §10.4, §17.4 |
| B8 | Support and SLA | Community: GitHub issues; Team: email; Enterprise: severity-based targets; no 24/7; targets, not guarantees; service credits only; supported: the current and the previous minor release. **Hours and targets: private here; the amended §10.1 prints them (T24)** | Yes | §10.1, §10.2 |
| B9 | Contracting entity | A new entity; the copyright assigned to it **now**, while its value is low; the accountant confirms the mechanics. **Ruling T18 (2026-10-27):** once the entity exists, one owner-go commit | Yes | §1.1, the four copyright lines |
| B10 | Negotiated agreements | Enterprise may sign its own agreement | Yes | §2 "Agreement", §23 |
| B11 | Refunds and warranty | Refund rules per plan (**figures: private here; the amended §7.4 prints the refund window, T24**); no warranty beyond "as is" | Yes | §7.4, §13.3 |
| B12 | Audit | Enterprise: a formal audit at most once a year on notice, at the Licensor's cost below a threshold; Team: annual self-certified true-up, no audit | Yes | §5.6, §5.7 |
| B13 | Free use and a startup discount | Free programs per D445; a startup discount for small companies for a limited period, expiring automatically. **Discount, period and eligibility figures: private** | **No (not the recommendation)** | §4.3 (brackets) |
| B14 | Seat rules | A seat may move to another person at most once per period, except on departure (**period: private here; the amended §5.3 prints it, T24**); no Team minimum; Enterprise has a floor (**amount: open, private**); pull-request reviewers who never use the software need no seat | Yes | §5.1, §5.3, §4.2 |
| B15 | Affiliates and publicity | Affiliates may buy under one Order Form; no use of a customer's name or logo without written consent | Yes | §3.4, §20 |
| B16 | Patent and telemetry | No patent licence (Option A); any telemetry opt-in and documented | Yes | §3.6, §9.3 |
| B17 | Grace period and interest | A **30-day grace period** after key expiry before D436 applies; late-payment interest at a modest fixed rate that **counsel sets** | Yes | §8.2, §10.4, §7.2 |

## Rulings on the tensions (2026-10-27)

The owner ruled every open tension on 2026-10-27. Each row gives:

- the ruling and its date;
- whether it is the recommendation;
- a one-line reason;
- where the plan follows it through (the re-sliced `implementation-plan.md`).

The tensions as first raised are kept below for the record. **None of T10–T27 remains open**; T27, a consequence of the rulings, was ruled on 2026-10-29. What remains is
the open items (O1–O3, O5) and counsel's review.

| # | Ruling (2026-10-27) | Recommendation? | Why (inferred: the owner gave rulings, not reasons) | Plan |
| --- | --- | --- | --- | --- |
| **T10** | The coordinator's **command and library** move to the business-licensed folder. **What stays:** the released MIT 0.11.x–0.12.0 stays the fork point. **What changes:** the sweep job's image carries the key-gated build (amended by D524, 2026-10-29: the history image is built with the `ee` tag; no key is mounted); the console's project view, `coordinator status --from-store`, the P5 sweep and the plans that assume the coordinator in core are re-planned. This amends D302, D400 and D434; the core reaches the code only through `ee/register` | **No (not the recommendation**, which was "only new parts") | The owner wants the whole coordinator in the paid line from its next version. The `internal/eeboundary` route the ruling names is **already allowed** (`cmd/fugaro/ee_on.go` → `ee/register`); no boundary-test edit | S1.1, S1.2, S1.3, S1.4, S1.8, S1.9, S1.10 (Check 178) |
| **T11** | `fugaro report` moves to the business-licensed folder for future versions; **`fugaro runs digest` stays MIT** with a small MIT copy of what it needs | **No (not the recommendation)** | The periodic reviews stay free; the organisation's spend report is paid | S1.1, S1.5 |
| **Governance** (D520, D521, D527, D530; 2026-10-29) | **Budgets, tiers, tripwires, checkpoints and stage timeouts move to `ee/`** with `fugaro report` and the coordinator (T10, T11), per the ee-reorg plan ([`../ee-reorg/implementation-plan.md`](../ee-reorg/implementation-plan.md)): the layout `ee/governance`, `ee/report`, `ee/coordinator` (with its CLI) and `ee/register`. The spend caps, the daily-cap warning, the kill switch and rigor's per-mode budgets stay MIT; core keeps the `governance:` wire, the halt plumbing the digest and `diagnose` read, and the launch-time skew gates. The key-minting service lives under `ee/keysvc/`, deferred with the key work and owner-run to deploy | Yes (each was the recommendation) | Governance must run where the job runs: F11 (ee-reorg); the caps are the free product's safety net (R4, F8) | E1.1–E1.3, E1.5, E2.3, E4.1 (ee-reorg) |
| **T12** | After the grace period the console and the app **read and cancel runs**, and the kill switch stays; they cannot launch or change config. The amended Terms §10.4 changes in the one revision after counsel | Yes | Never paywall safety | S0.4, S6.1–S6.3, Checks 176–177 |
| **T13** | The key service is **owner-operated in a dedicated Fugaro GCP project** (Cloud Run, a non-exportable KMS signing key, intake split from signing), **built after the entity exists**; until then keys are **minted by hand** (D440's first recommendation, as an interim) | Yes | One operator and one boundary; nothing hosted before there is a seller | S2.10, S3.1–S3.8 |
| **T14** | Unchanged: the seat stays a contract pricing rule, and the estimate counts every launcher | Yes | Ruled by D433 | S4.1 |
| **T15** | An **individual counts as an organisation of one** for the free seat and the public-OSI rule; counsel confirms business-only does not exclude them | Yes | Open-source maintainers are often individuals | S0.2, S0.4, S3.1, S7.1 |
| **T16** | The cloud keeps enforcing the launcher, operator and admin lists **in MIT core**. `ee/` adds the role model, its UX and the viewer built on those lists, with a small MIT core change for the viewer's cloud enforcement | Yes | Enforcement that protects a project stays free | S5.1 |
| **T17** | Console 0.113.0 and the first mobile release ship under the business licence **without key enforcement** once counsel signs off D432, D433 and D436 (honour-based); enforcement arrives with the key service in a later release | Yes | The licensed releases need not wait for the key service | S0.7, S8.4 |
| **T18** | The copyright moves **once the entity exists, as one owner-go commit**: the licence files, the root-licence hash test, the Terms' Licensor line and the website. The accountant confirms the mechanics; nothing happens before. #1140 merged on 2026-10-27 (`dcee3c963`). The CLA names its Project Owner through the root `LICENSE` (F21), so it needs no line of its own unless counsel adds one | Yes | One coherent change, at the moment it has a recipient | S8.2, S8.3 |
| **T19** | Answered through T12, T15, O4, the key-binding ruling and T26. The Terms are **not revised now** (counsel is reviewing them); one revision run follows counsel's feedback | Yes | Counsel reviews one text at a time | S0.4 |
| **T20** | **Close #831 as superseded**; re-plan the orchestrators in the business folder behind `ee/register` | Yes | D435 places them in `ee/` | S1.8 |
| **T21** | **One binary with a mixed licence, relabelled** (the key-gated single binary, the `ee` tag). The notices say MIT core plus business-licensed parts, and `fugaro` is no longer described as simply MIT | **No (not the recommendation**, which was two builds) | One download, one release pipeline | S1.6 |
| **T22** | The **Team price is published** on the website; Enterprise is "contact us". The figures never enter the repository: the pages are owner-run publishing | Yes | Self-serve buyers need a price | S7.2, S7.4 |
| **T23** | (Default.) `SECURITY.md`'s "latest release only" is aligned to the Terms' current and previous minor release, as a docs task | Default | One supported-version rule | S0.5 |
| **T24** | The **contract figures stay in the Terms** (support targets, renewal, refund and reassignment periods); prices, discounts, fees and the Enterprise minimum stay on the private Order Form | Yes | Buyers must see the contract terms; prices change | S0.4 (no change to those clauses) |
| **T25** | **Honour-based refunds**: the leakage (a refunded buyer keeps a key until expiry) is an **accepted risk** | **No (not the recommendation**, which was a short initial key) | Simplicity over a small leak | S0.4, S2.1 |
| **T26** | The **next key issues 30 days before expiry** (overlap), so an issuing outage must exceed 60 days to bite; the key format allows overlapping keys | Yes | Renewals survive a long outage | S2.1, S2.3, S3.2, Check 175 |
| **O4** | The key carries **expiry only**: the covered-versions date and the build `ReleaseDate` stamping are dropped, superseding that part of D438. A version field may be added later | Yes | No fallback, so no version window | S2.1; S2.2 withdrawn; Check 178 reused |
| **Key binding** (F35, F47) | **One key per organisation** with a seat count, not per user. The console shows seats used against it, advisory; the Terms' per-User wording (§8.1, §8.5) changes in the one revision | Yes | The product can bind only an organisation | S0.4, S2.1, S4.2 |

### A consequence the rulings create (ruled 2026-10-29)

**T27 — (T17 against T10, T11) Honour-based consoles and key-gated commands.** Console 0.113.0 and
the first app release ship honour-based, without key enforcement (T17). But the console spawns
`report --json` (Spend) and `coordinator status --from-store` (the project view), and after the
single-binary release (S1.6) those commands are key-gated (T10, T11, S1.1's gating model). An
honour-based console paired with a later CLI therefore loses Spend and the project view unless a key
is installed: enforcement in fact, before S6. Choices: (a) S1.6 waits for the enforcing console
and app release; (b) the paid CLI commands warn instead of refusing until enforcement ships;
(c) the console keeps working only with a key from S1.6 on, and its release notes say so. The same
question applies to the launch's declaration to the coordinator. **Ruled (b), 2026-10-29: the paid
commands warn, not refuse, until enforcement ships — and that warning is itself DEFERRED with the key
work** (the ee-reorg ruling D523, `docs/ee-reorg/reconciliation.md`): no licence notice or warning
anywhere until the verifier, the key-minting service and the launch are ready; for now everything is
available to all. No release before then carries a notice, a warning or a key check.

*On the date:* T27 arose as a consequence of the 2026-10-27 rulings, and the ee-reorg documents
group it with them as a standing ruling ([`../ee-reorg/spec.md`](../ee-reorg/spec.md)); the (b)
answer and its deferral are the owner's of 2026-10-29 (D523), the date this file records.

### The tensions as raised (ruled 2026-10-27, kept for the record)

**T10 — (D434) Future coordinator versions in the business-licensed folder.** Facts: the
coordinator is MIT and released in v0.11.0–v0.12.0, including `coordinator status --from-store`
(F9); the MIT launch path calls `coordinator.Reporter` and the project rules use
`coordinator.PathRoot` (F31); the P5 aggregation job runs `fugaro coordinator sweep` from an
untagged job image in the customer's cloud (F32); the console's project view spawns
`fugaro coordinator status --from-store --all --json` (`console/src/main/spawn.ts`, operation
`coordinator.status`); the core may
not import `ee/` and the only route is `cmd/fugaro/ee_on.go` → `ee/register` (F3); D302 says
nothing moves out, and `ee/README.md`, `.claude/rules/ee.md` and `LICENSING.md` repeat it (F38).
Questions: (1) **What moves**: the command and the sweep only (the store, intent and reporter API
stay in core so the launch keeps declaring), or the whole package with an extension point the core
calls through a registry `ee/register` fills? (2) **How a CLI command lives in `ee/`**: a command
registry in `internal/cli` that `ee/register` adds to; the released binary is then mixed-licence
(T21). (3) **The sweep job's image**: an `ee`-tagged image for that job alone (R1 narrowed to "run
images"), or the sweep stays core and only the paid surface moves. (4) **The released MIT
versions** stay MIT and a lawful fork point (R3); anyone may keep developing them. (5) **Plans that
assume the coordinator in core**: the coordinator plan's P6–P8 (`docs/coordinator/implementation-plan.md`),
the central orchestrator plan, and the console's project view (console 0.112.0).
Choices: (a) move the command surface and new parts, keep the library and the sweep in core;
(b) move everything, add extension points for the launch declaration and build an `ee` job image;
(c) narrow D434 back to new parts only. Each costs: (a) little is actually gated; (b) the largest
change and an `ee` binary in the customer's cloud; (c) reverses the owner's answer.

**T11 — (D444) Spend and cost reports in the business-licensed folder.** Facts: `fugaro report` is
MIT and released; `internal/report` also feeds `fugaro runs digest` (0.12.0, MIT, the periodic
reviews), the console's Spend screen and a pinned docs test (F33); a run launched to write the
digest runs an untagged CLI (F46); the CLI reference and the plugin tests read the untagged tree
(F45); the `working` skill teaches
both commands and the plugin tests check them against the CLI; the credentials audit log stays MIT
(the owner's note). Questions: does the digest move with the report (it is a cost report of one
repository)? Does `internal/report` (the library) move, or only the command? Which commands are
"similar" (`fugaro runs stats`, `fugaro budget show`, `report --by person`)? The spend caps are
safety and stay free (R4): is spend visibility safety too? The periodic-reviews practice and the
skill that teaches it would need a key for future versions. Choices: (a) move only `fugaro report`'s
organisation-wide views (by person, across repositories), keep per-repository spend and the digest
free; (b) move report and digest together; (c) keep both MIT (reverses the answer).

**T12 — (D436) No fallback; read-only after the grace period.** Facts: licence 11.1 ends the
production right at lapse, consistent with no fallback (F15); but **read-only use of the console
against a production installation is still production use**, so the read-only state needs an
express post-lapse grant in the Terms (F37), which the amended §10.4 and §17.4 now draft; the spec's
"never block work" and the first proposal's D437 are reversed for the console and the app; the MIT
CLI keeps launching and cancelling. **The console is read-only already** (F20): it launches,
cancels and kills nothing, so a lapse can only remove views. Open: which views survive (live
watch, logs, Spend, the coordinator's project view, seats); whether console v2's planned launch is
the first gated action; the app's **cancel**, which goes through the mailbox to the
`orchestrator` agent that D435 makes paid, so a lapsed app may lose cancel (the spec's "never
paywall safety", R4); what a console or app with **no key** shows, including a 0.112.0 user
upgrading (F43); what a free evaluator's expired key does (F39). Choices: (a) read-only keeps every
current view and the app's cancel; (b) read-only keeps the free core's views (watch, logs) and
hides the paid ones; (c) the console and the app stop entirely and the Terms say so. **The amended
Terms §10.4 already words read-only as "cannot launch, cancel or change anything"**, which would
take the app's cancel away (against R4); that wording is a draft for counsel, not the owner's
ruling on this tension, and the owner rules on it.

**T13 — (D438, D440) A hosted signing service and key website.** A new Fugaro-hosted component
with a new trust boundary: whoever reaches the signing key mints any key for any tier. Facts and
questions: (1) **Trust and compromise**: the private key in a managed key store (non-exportable),
the service identity the only signer, no agent identity with any access, a key id for rotation
(D308), and no revocation (D308), so short key lives are the only limit on a leaked key.
(2) **Abuse**: email verification needs outbound mail; free-mail exclusion needs a maintained list;
"one per organisation by email domain" is defeated by second domains and subdomains; the
open-source check reads public repositories through the code host's API but cannot see whether a
project has other, private repositories, so "all repositories public" rests on the applicant's word.
(3) **Personal data** collected at sign-up (emails, company domains, repository URLs): PIPEDA and
GDPR, a privacy notice, retention, access and deletion requests; the amended Terms §8.4 covers
sign-up data (counsel question 14); the merchant of record as a recipient of buyers' data is in no
notice yet. (4) **Uptime**: a promise for issuance only; validation is offline
(R2 amended). (5) **Hosting and operation**: where it runs and who operates it are unanswered; the
spec's "no Fugaro-hosted dependency" becomes "no hosted dependency at runtime". (6) **Identity**:
the console has no sign-in, so the key binds to an organisation and a seat count, not a person
(F35); an MIT-CLI-only user needs no key (the CLI is not the Software). (7) **Order**: the service
issues keys as the Licensor, so it needs the entity first (D441). (8) **The admin page**: a second
authenticated surface that can mint any key: its authentication, step-up, CSRF defence, escaping
of applicants' free text (a stored script there could mint), an audit log of its mints and
per-tier limits are unspecified. (9) **Paid keys**: purchase, renewal and cancellation events from
the merchant of record, verified and idempotent (F42). (10) **Proof of control** for the
open-source check and the unbound key (F47); the "second key for a domain" answer must not reveal
whether a domain holds one; verification tokens single-use and expiring. (11) **Where the code
lives**: in `ee/keyservice/` it is published Software with its checks visible; in the private
repository it is not; a security trade-off, not only a location. (12) **The website and privacy
page** forbid a project-run host today (F44). Amends R5, licensing R8, D308 and D440's first
recommendation. The owner rules on hosting, operator, retention and the admin page's
authentication before S3 builds.

**T14 — (D433, D439) A seat is anyone who launches, by any means.** The CLI is MIT, so the rule is
a contract pricing rule binding subscribing organisations, not a licence restriction; counsel
words it. The advisory estimate must count every launcher: the launchers and operators lists
(groups count as unknown), plus distinct run requesters over 90 days; agent launches are recorded
under the configured person (`RequestedBy`), and an agent with no configured identity is invisible
(F7). Until the viewer role exists (D446), nobody can be a free viewer in practice: anyone who can
open the console can launch.

**T15 — (D432, D445) The free seat and the programs.** Licence 1.1 needs "a valid subscription", so
the free seat is a **no-fee subscription** under the Terms (or a licence change); "organisation",
"subscribing organisation" and "a project entirely public under an OSI licence" need definitions;
Affiliates buying under one Order Form (B15) make "an organisation with no subscription" ambiguous
for a group of companies; Terms §1.3 is business-only, while an open-source maintainer may be an
individual (F27); licence 3.1 already frees development and testing, so the evaluation seat is
production use and must be described as such.

**T16 — (D446) Roles enforced system-wide.** Enforcement in the cloud lives in core MIT code
(Terraform modules under `deploy/terraform/gcp`, the RTDB rules in `internal/budget/rules`), while
the roles are a paid feature: either the cloud-side enforcement is MIT and only the console and app
surfaces are paid, or the enforcement itself is an `ee/` module. It also needs an identity the
console does not have (F6) and the unbuilt `fugaro pair`. D433's free viewer waits on it.

**T17 — (D448) Counsel before console 0.113.0 and the first mobile release.** Console 0.113.0 is
the first console under the business licence (`LICENSING.md`), and the mobile app's first store
release is the app's first. Both are **blocked on counsel's sign-off of D432, D433 and D436**, and
on S6 (the read-only state) if it must ship with them. And since every console and app user holds
a key (D438), a release that enforces one also waits until keys can be issued: the signing service
live (S3.7) and the production public key in the verifiers (S2.9). The owner chooses whether
0.113.0 ships before key enforcement (licence only) or with it.

**T18 — (D441, B9) Copyright to the new entity now.** One commit must change the copyright line in
`LICENSE` (which, through the CLA's definition, also changes the CLA's Project Owner),
`ee/LICENSE`, `console/LICENSE.md`, `mobile/LICENSE.md` and
`LICENSES/LicenseRef-Fugaro-Enterprise.txt`, after the assignment is signed (the licensing plan
allows this edit only on the owner's go, the licensing reconciliation's F27, fact 4). The Terms' Licensor placeholder (§1.1),
the website, the Paddle account and the signing service's issuer follow. Open PR #1140 edits the
same licence files (F36) and should land or close first. Counsel: whether the approved licence
must name the entity as Licensor. Accountant: the mechanics and the tax treatment of the assignment.

**T19 — The amended Terms against this reconciliation.** `main`'s #1146 (licensing L4.5, done)
already amends `docs/licensing/subscription-terms.md` to the owner's answers: the Licensor and the
merchant of record (§1.1, §1.4, §7.3), the free plans and discounts (§4.3, figures bracketed), the
trial (§4.4), the seat rule (§5.1), roles (§5.2), true-up and audit (§5.6, §5.7), the seat estimate
(§5.8), renewal (§6), refunds (§7.4), every user's key and its issuing (§8.1–§8.4), support and
supported versions (§10.1, §10.2), the lapse (§10.4, §17.4), no warranty (§13.3), and counsel
questions 12–18. What it leaves, or decides ahead of the owner, for counsel and a later revision
run (S0.2, S0.4); this PR does not edit the Terms:

| Clause on `main` | What remains | From |
| --- | --- | --- |
| §10.4 | Read-only "cannot launch, cancel or change anything": **revise (T12 ruled):** cancel and the kill switch stay | T12, R4 |
| §10.2, §8.1 | "up to the covered-versions date of its License Key": **revise (O4 ruled):** drop the date | O4, F40 |
| §8.5, §8.1 | "outside the User it was issued to": **revise (key binding ruled):** one key per organisation with a seat count | F35, F47 |
| §8.3 | "Paid Subscriptions: keys for the seats bought, after purchase": no renewal, seat-change or cancellation path | F42, T25, T26 |
| §10.1, §6.2, §7.4, §5.3 | Print the support targets and the renewal, refund and reassignment periods: **kept (T24 ruled)** | T24 |
| §1.3 | Business-only, while an open-source maintainer applying with a repository URL may be an individual | T15 |
| §8.4, §9.5 | The merchant of record as a recipient of buyers' personal data | T13 |
| §3.3 note | The released binary carrying `ee/` code under one key-gated build is not addressed | T21 |
| Counsel questions 12–18 | Cover O3; S0.2 adds T15, T21, T23 and the rows above | O3 |

**T20 — (D435) Orchestrators in the business-licensed folder against PR #831.** #831 builds P2 of
the central orchestrators (the access record, the `orchestrators` block, `fugaro orchestrator
add|rm|ls`) as core code. Choices: hold #831 and re-plan P2 against `ee/register`; or land the
access record and config block in core (the mechanics of one installation) and move only the
command surface; or close #831. The owner rules; this PR does not touch it.

**T21 — (D447, D433, D436) "The CLI is MIT" against one key-gated binary.** Once any CLI command
lives in `ee/` (D434, D444, D435), the released `fugaro` is built with the `ee` tag and every
licence label names the enterprise licence (F34). The source of the core stays MIT and an untagged
build stays pure MIT, but the binary users download carries business-licensed code. Whether running
its MIT commands in production is "use of the Software" is counsel's (the Terms' definition of
Software includes code "compiled into a released binary"). Choices: (a) one binary as D447 says,
with counsel confirming that MIT commands need no seat; (b) a second, core-only binary as the
default download (D303's first answer); (c) the paid CLI commands ship as a separate binary.

**T22 — The public pricing page against private numbers.** A public pricing page publishes the
prices; the website's source is in this public repository. Choices: the figures enter the
repository at publication (the owner's go, after which they are public anyway); the page links to
the merchant's checkout and states no figures; or the figures come from a file supplied at the
owner-run deploy and never committed.

**T23 — (B8) Supported versions.** `SECURITY.md` supports "only the latest 0.x release"; B8 supports
the current and the previous minor release for the paid tiers. Whether `SECURITY.md` changes for
the core too, or the Terms carry their own rule for the Software only. `SECURITY.md` is pinned by
docs tests. The plan carries it to counsel (S0.2) and to a `SECURITY.md` task on the owner's
ruling (S0.5).

**T24 — (D430) Figures the amended Terms print.** D430 sends SLA hours, refunds and billing figures
to the private repository; the amended Terms on `main` print the support targets and hours
(§10.1), the annual renewal's cancellation notice (§6.2), the refund window (§7.4) and the seat
reassignment period (§5.3). They will be public once the Terms are published, but D430 keeps them
out of this folder. Choices: the owner accepts them in the Terms draft (they are terms a buyer must
see); or the Terms bracket them as Order Form items until publication.

**T25 — Refunds against keys that cannot be revoked.** A Team annual buyer who takes the refund
(§7.4) keeps a key valid until it expires, because D308 has no revocation. Choices: paid keys are
short-lived and reissued while paid (which sharpens T26); or the limit is accepted and the Terms
say so.

**T26 — An issuing outage does reach paying customers.** D438's note says an outage blocks only new
keys, but every renewal and seat change is a new key; an outage longer than the 30-day grace drops
paying users to read-only. Choices: keys issued well ahead of expiry; a longer grace for paid keys;
or the owner accepts it and S3.1 states an issuing uptime target.

## Open items

- **O1** — The Enterprise floor (B14): no number yet; private.
- **O2** — The late-payment interest rate (B17): counsel's.
- **O3** — For counsel (from the owner's session, beside the Terms draft's own questions):
  whether D432's free seat and D436's read-only lapse fit the business licence; the wording of
  D433's seat as a pricing rule; personal data at key sign-up (PIPEDA, GDPR); whether the licence
  must name the new entity as Licensor; the interest rate; GST/HST on supplies to the merchant of
  record and on Enterprise deals invoiced directly to Canadian customers, and registration timing.
- **O4** — ~~What the key's covered-versions date does now that there is no fallback (F40).~~
  **Ruled 2026-10-27:** dropped; the key carries expiry only.
- **O5** — For the accountant: the mechanics and tax treatment of assigning the copyright now.

## A key per organisation (D438 as answered and ruled)

Extends D308; nothing here is built. The design note is licensing plan L3.1
(`docs/licensing/licence-key.md`), superseded by S2.1. *Earlier version: a key per user with a
`covers_until` date and a `ReleaseDate` stamped into every build; withdrawn by O4 and the
key-binding ruling on 2026-10-27.*

- **The key** is D308's signed claim set plus `tier` (`community`, `team`, `enterprise`),
  `features`, `seats`, the **organisation** it was issued to (a domain, an individual as an
  organisation of one, or an Order Form id), `issued`, `expires` and a key id. **Expiry only**: no
  covered-versions date and no release-date stamping (O4).
- **The check**, offline, at `ee/register` (CLI), in the console and in the app. Paid features are
  on while the signature verifies, `now <= expires + grace` (B17), and the key is within its tier's
  maximum lifetime. After that the console and the app enter the read-only state, which keeps
  reads, cancel and the kill switch (D436, T12). The MIT CLI's own commands never check a key.
- **Renewal** (T26): the next key issues 30 days before expiry. Two keys may be installed, and the
  verifier takes the latest valid one, so an issuing outage bites only after 60 days.
- **Seats** are compared, never enforced (D439): the console shows seats used against the key's
  seats.
- **Issuance:** by hand through the KMS key until the entity and the service exist (T13, S2.10);
  then by the owner-operated service in its dedicated project. The record of issued keys is in the
  service and the private business repository (R5).
- **Known limits**, accepted:
  - the source is visible, so a determined user can remove the check;
  - a clock set back extends `expires` (D308);
  - with no revocation, a leaked key works until it expires, and a refunded buyer keeps theirs
    (T25, accepted);
  - nothing compares the organisation with the installation, so a key can be shared (F47).

  The Terms are the enforcement.
- **Storage and hand-off** (F41): S2.1 decides where each product keeps the key, and how the
  console passes it to the CLI it spawns, never in argv.

## UNVERIFIED

Checked against `origin/main` at `252b5c46a` on 2026-10-27.

- **U1** — (closed with T1) What "the date we make the Software available" means in the FSL.
- **U2** — Whether a no-fee subscription and a Terms-level exclusion of read-only users satisfy
  licence clause 1.1. Counsel's (O3).
- **U3** — How the merchant of record's buyer terms interact with the Terms' Licensor–Customer
  structure. Not researched.
- **U4** — Whether consumer withdrawal rights reach business-only sales through a merchant of
  record. Counsel's.
- **U5** — ~~Whether the console's and the app's builds stamp any date today.~~ Moot after O4
  (2026-10-27): no build stamps a release date.
- **U6** — What IAM permission listing Firebase Auth users needs. Not read.
- **U7** — The central orchestrator branch and PR #831's full content: read only from its title and
  `docs/central-orchestrator/README.md` on `main`.
- **U8** — The owner's MCP and Workspaces specs have no docs package; known only from briefs.
- **U9** — Whether a managed key store in the owner's cloud signs with Ed25519 (D308's algorithm)
  and keeps the key non-exportable; to be confirmed by S3.1 against the provider's documentation.
- **U10** — The commit-history copyright facts of licensing F3 were not re-checked after
  2026-10-24.
- **U11** — Whether an email-domain check, a free-mail list and the code host's API are enough to
  enforce "one per organisation" and "all repositories public under an OSI licence" (T13). Not
  measured; Checks 171 and 172 measure them once built.
- **U12** — ~~Where the signing service is hosted and who operates it.~~ **Ruled 2026-10-27
  (T13):** owner-operated, in a dedicated Fugaro GCP project.
