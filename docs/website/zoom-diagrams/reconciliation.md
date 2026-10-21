# Website zoom diagrams — reconciliation

Status: **plan only; nothing is implemented.** Owner: **@Dimi**. Date: **2026-10-21**.
Source: [spec.md](spec.md) — the owner's brief, committed alone in this branch's first
commit, before anything that answers it. The plan this document produces:
[implementation-plan.md](implementation-plan.md).

This is the third line of work on the site. The first is
[`../reconciliation.md`](../reconciliation.md) (findings `F1`–`F63`, decisions
`D90`–`D109`, all answered 2026-10-16) with [`../implementation-plan.md`](../implementation-plan.md)
(tasks `W0.1`–`W3.3`). The second is [`../redesign/`](../redesign/README.md) (findings
`RF1`–`RF26`, decisions `D180`–`D192`, all answered 2026-10-18, with dated amendments since).
**This document cites those findings and decisions rather than repeating them**: the site's
own rule is that a page renders a fact and never restates one, and a document about the site
holds itself to it too.

## 0. The one thing to know before reading further

**The brief asks for four things, and three of them are pages the site does not have.** The
site is an **eleven-route** Astro build (`website/src/pages/*.astro`), gated by fourteen
hermetic checks run by `npm run check` (`website/package.json:12-13`). There is no "Why GCP"
page, no page about the phone app, and no diagram of the four levels. The fourth thing — the
homepage slider — exists in half: the homepage has a three-tab slider whose third panel
holds **one** `<img>` (`website/src/components/HomeSlider.astro:286-318`), and the console
page has a real, data-driven, scripted slider (`website/src/components/ScreenshotSlider.astro`,
shipped in #841).

**The second thing to know is that the brief's product claims are mostly not true as
written, and the site must not print them.** Level 1's loop is close to the runner but wrong
in three of its six lines (§3). "Why GCP" is right about Firebase and wrong about four
other things, one of which — *"GCP projects are free and disposable"* — the repository's own
managed-projects reconciliation explicitly rejects (`docs/design/managed-projects.md:18,211`).
The mobile page's six bullets describe an app that is, today, **voiceless and unpaired**
(`mobile/README.md:19-21`). None of this is an argument against the brief; it is the reason
this document exists before the pages do.

## 1. How to read this

Five labels, and each finding carries exactly one:

- **Wrong** — the tree or its documents state something that is not true today.
- **Exists** — the brief asks for something the site already does; the work is to keep it,
  not to build it.
- **Gap** — nothing does it. The brief's item, unanswered.
- **Rule** — a rule of this repository that constrains how the gap may be closed. A `Rule`
  is not an obstacle to argue with in a plan; it is the shape of the answer.
- **Open** — a question no reader can settle from the tree. `Open` findings become the
  numbered decisions in §12.

Findings are numbered **`ZF<n>`** — "zoom finding" — because `F1`–`F63` belong to the first
line of work and `RF1`–`RF26` to the redesign, and a re-used number is a false citation.
Things this document could not verify are **`ZU<n>`** (§10), as the redesign's are `RU<n>`.

Every finding cites the file and the symbol it was read from. Every measurement was taken on
2026-10-21 on this branch's base, `3021f4b3`, and the command that took it is named where it
matters. **Nothing in this document is a description of an image**: the model that wrote it
cannot view one, so the brief's visual asks are stated as constraints and left to the owner
(§10, D190, RU1).

## 2. Findings

### ZF1 — **Wrong** — "senior reviewer: one final review once the cheap loop passes"

`internal/runner/runner.go:1764` `seniorLoop` is a **loop**:
`for round := 1; round <= st.Rounds; round++`, where `st.Rounds` resolves to
`agent.review_rounds` (`internal/runner/plan.go:29` `planOf`), whose default is **2**
(`internal/config/defaults.go:37` `DefaultReviewRounds = 2`; range 1–10,
`internal/config/validate.go:124`). It is the final *stage*, and each round is a review
followed by a `fix` (`runner.go:1781`). "One final review" is false at the default settings.

It also does not wait for the cheap loop to pass. The cheap loop's verdict decides nothing:
`runner.go:1738-1752` runs the first-line loop and then `seniorLoop` regardless, and
`runner.go:1821-1828` says so outright — the first-line verdict "never decides readiness: it
is recorded with its tier, and the senior review that follows is told nothing of it".

### ZF2 — **Wrong** — "cheap coder ↔ cheap reviewer"

The cheap loop's review is a review by **the coder's own model**. `internal/config/config.go:208`
`StageRole` maps `review_first` to `RoleCoder`; only `review` is `RoleReviewer`
(`config.go:226` `ModelFor`). `docs/recipes.md:39` says the same in words: the `first_line`
step "is a review by the coder's model in a fresh session". There is no separate cheap reviewer identity to
draw as a second actor unless the brief means two *rounds*, which it does not.

### ZF3 — **Wrong** — "draft PR, flipped to ready only on senior pass"

`internal/runner/outcome.go:31` `Decide` returns ready only when **both** hold: a
clean-tree test record on the final commit passed (`latestVerifiedTest`, `outcome.go:35-40`)
**and** the last review's verdict is `"ship"` (`outcome.go:41-44`). Its own reasons name
both: `ReasonNoVerifiedTest` and `"tests failing on the final commit"`. `runner.go:2298`
then sets `spec.Draft = !ready`. So a senior pass alone does **not** flip a draft PR to
ready. The draft/flip pair itself is real and shipped (`internal/runner/prflow.go:200`
`openDraft`, `:224` `Draft: true`), gated by `git.pr.early_draft`, default **true**
(`internal/config/defaults.go:69-71`).

### ZF4 — **Wrong** — "deterministic checks first (compile, tests)" as a property of a job

The gate exists and runs first: `internal/runner/gate.go:204` `runGate`, called at
`runner.go:1641-1703` before the implement stage. But it runs only when the **recipe** has a
leading `check` step, which must come first if it is present
(`internal/recipe/recipe.go:910-917`). The `default` recipe has none
(`internal/recipe/catalog/default.yaml`: `first_line` then `review`); only `test-and-fix`
and `lint-fix` carry one. "Deterministic checks first" is therefore true of *some* recipes,
not of a job.

### ZF5 — **Wrong** — "on rejection: one bounce back into the cheap loop, then fail"

Bounces are `review_rounds − 1` (`docs/recipes.md:131`), so the default is exactly one — but
only for recipes that declare `bounce: first_line` on the review step
(`internal/recipe/recipe.go:96` `Step.Bounce`), which `standard` and `test-and-fix` do and
`default` does not. The last senior round never bounces: the loop returns before reaching the
bounce (`runner.go:1774` returns on `round == st.Rounds`; the bounce is `runner.go:1784`).
"Then fail" is
right in kind: the run ends not-ready with a draft outcome (`runner.go:2386-2395`), not an
infrastructure error.

### ZF6 — **Exists** — the container really is a Cloud Run **job**

`internal/infra/tf/cover.go:22-28` admits only `google_cloud_run_v2_job` and its IAM member;
`deploy/terraform/gcp/modules/project/job.tf:10` declares `resource "google_cloud_run_v2_job" "this"`;
there is **no** `google_cloud_run_v2_service` anywhere under `deploy/terraform/`. A run is one
**execution** (`internal/backend/codec.go:89-93` parses
`projects/<p>/locations/<r>/jobs/<j>/executions/<e>`). The one thing the brief's line cannot
say literally is "one": an installation holds a job resource per workflow plus a `history`
job (`deploy/terraform/gcp/modules/installation/history.tf:70`).

### ZF7 — **Rule** — the loop is a recipe's, so the drawing must not present it as the product's

Everything in ZF1–ZF5 is selected by `recipes:` and `agent:` keys, not fixed by the runner.
`docs/recipes.md` lists eight shipped recipes; `internal/recipe/recipe.go:44-46` gives three
modes (`implement`, `review`, `artifact`), and `mode: review` never changes a pull request's
draft state at all (`docs/recipes.md:44`). A diagram whose *caption* is a sequence is a
diagram that is wrong for six of the eight recipes. The four levels may show the shape; the
page must say which recipe it is drawing.

### ZF8 — **Wrong** — "central orchestrators", "coordinator (advisory)" and "security service" are not built

- Central orchestrators: `docs/central-orchestrator/README.md:3` — "Status: plan only; no
  code is implemented." No `internal/orchestrator` package exists.
- Coordinator: `docs/coordinator/README.md:3` — "approved to build on the branch
  `coordinator`, target 0.11.0; no code is implemented yet." Its decisions D193–D197 were
  answered on 2026-10-21, which is not the same thing as code.
- Security service: `docs/credentials-service/README.md:3` — "plan only; nothing is
  implemented". `SECURITY.md:107` names it as the *future* replacement for the OAuth token
  mount.

Level 4's "inside it" list is therefore a **roadmap** drawing, not a picture of the product.
It may be drawn; it may not be drawn as present tense without a label.

### ZF9 — **Exists** — budget caps and the kill switch are shipped

`internal/budget/kill.go:19-58` (`checkKills`/`watchKills`), `internal/budget/model.go:35`
`PathKillGlobal = "config/kill/global"` and `:52` `PathKillRepo`, enforced at
`internal/budget/session.go:523-526`; "an unreadable switch counts as ON"
(`internal/budget/kill.go:16-27`). `docs/releases/v0.9.5.md` ships `fugaro budget set|kill|resume`.

### ZF10 — **Exists** — live counters and history are shipped

Counters in the Realtime Database (`internal/rtdb/client.go:1-31`, `internal/budget/model.go:30-72`),
history in Firestore (`internal/firestore/client.go:1-3`, `internal/budget/rollover.go:48`
`SpendCollection = "spendDaily"`), surfaced by `fugaro watch` and `fugaro report`
(`internal/cli/report.go`, `internal/cli/history.go`).

### ZF11 — **Wrong** — "local orchestrator agent" is a name for the CLI

There is no resident local-orchestrator process. `docs/central-orchestrator/reconciliation.md:934-935`
(D15) records that the local orchestrator *is* the existing CLI — `fugaro run`, `fugaro watch`,
`fugaro diagnose`. Level 2's "Local orchestrator agent calls Fugaro" and Level 3's "Laptop runs
the console and the local orchestrator agent" draw a component that is a command-line program.

### ZF12 — **Exists** — the console is shipped and is its own release

`console/` is the desktop app; the newest console release is **`console-v0.9.5`**
(`docs/releases/v0.9.5.md`). The site already links it (`features.console_downloads` is true;
`website/data/site.yaml:23-24`).

### ZF13 — **Partly** — the phone app exists and is voiceless

`mobile/` is an Expo (React Native) app with real source (`mobile/src/{accounts,feed,mailbox,jobs,voice,demo}`),
P0–P3 merged, and `mobile/README.md:19-21`: it "ships **without an engine**: the first store
build is voiceless (the owner's ruling of 2026-10-19)". A first iOS submission succeeded on
2026-10-20 from the owner's terminal (`mobile/README.md:37-40`).

### ZF14 — **Wrong** — the brief's "0.10.0 / 0.11.0" frame does not exist yet

The newest release documented and tagged is **v0.9.5** (2026-10-20). `0.11.0` is the
coordinator line's **target** (`docs/coordinator/README.md:3`), not a shipped version, and
there is no 0.10.0 in `docs/releases/` or in the tags. A page that says "in 0.11.0" would be
promising a release number the repository has not committed to a date for.

## 3. The four levels against the runner

The brief's Level 1 lines, each checked against the code (the citations are in ZF1–ZF7):

| The brief says | Today | Why |
|---|---|---|
| one ephemeral Cloud Run job | **true in kind** | `google_cloud_run_v2_job`, one execution per run (ZF6) |
| deterministic checks first (compile, tests) | **recipe-selected** | only a recipe with a leading `check` step (ZF4) |
| cheap loop: cheap coder ↔ cheap reviewer, up to N rounds | **half** | the reviewer is the coder's own model; N is `agent.first_line_rounds`, default **1** (ZF2) |
| senior reviewer: one final review once the cheap loop passes | **false** | up to `agent.review_rounds` rounds, default **2**, and it does not wait on the cheap loop (ZF1) |
| on rejection: one bounce back into the cheap loop, then fail | **recipe-selected** | `bounce: first_line`, default one bounce (ZF5) |
| output: draft PR, flipped to ready only on senior pass | **half** | senior `ship` **and** a verified passing test on the final commit (ZF3) |

Level 2's "Fugaro spins up several jobs in parallel" is the CLI launching more than one run;
there is no fan-out component (ZF8, ZF11). Its "optional: budget check gate before launch" is
real in the sense that caps are checked as a run spends (`internal/budget/session.go`), and
the caps are **off by default** (`README.md:99`). Level 3 and Level 4's components are
ZF8/ZF11.

## 4. "Why GCP", claim by claim

The brief's four numbered points and its two closing paragraphs, against the tree:

### ZF15 — **Wrong** — "GCP projects are free and disposable"

"Free to create" is supportable and is in the repository's own words
(`docs/design/m9-budget-and-dashboard.md:828` — "A new project costs nothing by itself").
"Disposable" is **rejected by the repository's own reconciliation**:
`docs/design/managed-projects.md:18` — "A managed project is **not disposable** in the way
the spec means" — and `:211` lists why: the Terraform state bucket lives in that project,
so does the spend history and the kill-switch state in the Realtime Database, so does the
application data, and the Firestore database's **location is permanent and can never be
changed**. A page that prints "disposable" unqualified contradicts `docs/design/`.

### ZF16 — **Wrong** — "it never runs Terraform or cloud ops against existing infrastructure"

Fugaro's provisioning model **is** Terraform: `docs/gcp-setup.md:75` describes the typed-confirmation
applies, and `internal/infra/tf/cover.go` is the type allow-list for the resources those
applies may create. A **managed** project is one Fugaro created
(`docs/design/managed-projects-spec.md:27`), but even there Terraform **is** run — the
deployer applies the agent-authored root (`docs/design/managed-projects.md:191`;
`internal/cli/managed_apply.go:36-56`) — and D58 explicitly permits flagging an **existing**
project into managed mode (`docs/design/managed-projects.md:174`). The true, narrow sentence
is: *a managed project is a new project that holds only Fugaro's resources*.

### ZF17 — **Wrong** — "Fugaro gets its own isolated, Fugaro-managed project", in the present tense

Managed projects are **gated off**: `internal/cli/recipes_skew.go:152`
`const managedDeployerShipped = false`, and every command that would create one or act as
its deployer refuses before any cloud call (`internal/cli/managed_flag.go:178`,
`internal/cli/init.go:749,944`, `internal/cli/restore.go:192`,
`internal/cli/managed_apply.go:157`). `docs/design/managed-projects.md:3` says it outright.
`docs/releases/v0.9.5.md` lists managed projects among the disabled features.

### ZF18 — **Wrong** — "so there is no extra setup"

`fugaro init` provisions all of it: `docs/gcp-setup.md:9-10` (an installation gets APIs, a
runs bucket, custom roles, a scheduler and log isolation; a repository gets secret
containers, registries, job accounts, a Cloud Run job and a Scheduler job), and
`deploy/terraform/gcp/modules/firebase/apis.tf:8-17` enables `firebase`, `firebasedatabase`,
`identitytoolkit`, `securetoken`, `firestore` and `firebaserules` by name. The Realtime
Database is a created resource with `prevent_destroy`
(`deploy/terraform/gcp/modules/firebase/firebase.tf:20-33`). "Comes with it" is true of the
*same-project services*; "no extra setup" is false.

### ZF19 — **Partly** — "Firebase is non-negotiable" is too strong, and its three services are one optional plane

All three are used, and each is cited: Realtime Database
(`internal/rtdb/client.go:1-31`; counters `internal/budget/model.go:30-38`; the run registry —
the code's word for the "agent roster" — `internal/budget/model.go:74-77,327`; the kill switch
`internal/budget/model.go:35,52`), Firebase Auth as **a run's identity** and the phone's
(`internal/budget/authadmin.go:16-25`, `internal/budget/token/exchange.go:83,113`; the web API
key at `deploy/terraform/gcp/modules/firebase/firebase.tf:36-47`; mobile at
`docs/mobile-auth/spec.md:14,32`), and Firestore for history (`internal/firestore/client.go:1-3`).

But they are the **budget, dashboard and history control plane**, and it is optional:
`README.md:99` — "The budget is off by default" — and `docs/gcp-setup.md:464` documents a
degraded mode with no Firebase at all. A run needs none of the three. And Firebase Auth is
**not** the identity provider for the console (which holds no credential,
`docs/design/desktop-console.md:13,96`) nor for the CLI, which signs in with
`gcloud auth application-default login` (`internal/cli/adcpath.go:77`).

### ZF20 — **Partly** — "an object store could be swapped for S3"

Storage is behind an abstraction and the statement is directionally right: "Storage is not
part of the seam: every backend uses `gocloud.dev/blob`" (`docs/backends.md:7`;
`internal/backend/backend.go:4`). But the S3 driver is **not imported**:
`internal/blobx/blobx.go:28-30` imports `fileblob`, `gcsblob` and `memblob` only. S3 appears
in the repository as a *wanted* AWS component (`README.md:190`), not as a switch that exists.

The second half — "the others have no clean equivalent" — is **supported and is the strongest
sentence in the brief**: `docs/backends.md:27-34` names the four Firebase behaviours a
replacement would have to provide (atomic multi-path conditional updates, server-evaluated
rules tying counters together, event streams, a queryable document store), and says the
dependency is direct. `docs/backends.md:11` is quotable as-is: **"Firebase stays GCP-bound
regardless of compute backend."**

### ZF21 — **Wrong** — "we started out wanting a pluggable cloud… then it became clear"

The repository's position is not that a pluggable cloud was tried and dropped. It is that a
second cloud is **out of scope for 1.0 and wanted from contributors**:
`README.md:188-190` ("Other clouds: help wanted… We want to run Fugaro on **AWS**… and
**Azure**"), `README.md:204` (roadmap, unchecked), `docs/backends.md:3,21,42` (the seam, its
conformance suite and the live-checklist entry a second backend must add), and
`docs/design/setup-and-skills-spec-source.md:25` ("An AWS backend is out of scope [for 1.0]").
A page saying the pluggable cloud "became clear" to be wrong would be arguing against the
repository's own open invitation.

### ZF22 — **Rule** — the suggested framing contains a word the repository rejects

The brief's own suggested sentence — "no other cloud offers this as one integrated,
**disposable** project" — carries "disposable", which ZF15 shows the repository rejects. The
same sentence with "one integrated project" and no "disposable" is supportable. This is
finding ZF15's consequence and it is a copy rule, not a preference.

## 5. The mobile page, claim by claim

### ZF23 — **Wrong** — "live agent roster" is not readable by the phone

The phone's read grant is exactly four families — `config/caps`, `config/kill`, `spend` and
its own mailbox — and it **denies** `agents/**`: `mobile/src/feed/paths.js:44`
`DENIED_FAMILIES = Object.freeze(['agents', 'outcomes', 'runs', 'fugaro'])`. The heartbeat
advertises a list but the app surfaces only a **count**, and `mobile/src/jobs/heartbeat.js:84-86`
says the agent count "is deliberately **not** carried" because "no field of the list's table
names an agent". The "agent roster" the brief names is the
Realtime Database's `/agents/<slug>/<run>` registry (`internal/budget/model.go:74-77`), which
the *console* and `fugaro watch` read — the phone does not.

### ZF24 — **Partly** — the wake word exists as a transcript prefix test, and no engine is shipped

The routing is a shipped, merged pure function: `shared/command/index.js:129`
`WAKE_ALIASES = Object.freeze(['fugaro','figaro'])` and `:653` `routeUtterance`, reconciled as
D164 and stated at `docs/mobile/functional-spec-v0.1.md:59`. It is **not** an acoustic
detector: the owner's ruling of 2026-10-19 answered Q5 option (1), keeping the transcript
prefix test and **dropping** the spec's openWakeWord/Porcupine bullet
(`docs/voice-module/reconciliation.md:539-545,664`). The console's own voice spec lists the
wake word as a **non-goal for v1** (`docs/design/voice-spec.md:26`). And the app has no
speech engine, so "say Fugaro" works in no build today.

### ZF25 — **Partly** — switching projects and agents by name is Phase 6, unbuilt

Specified (`docs/mobile/functional-spec-v0.1.md:66-72,110`), decisions D163 and D172 answered
2026-10-17 (`docs/mobile/functional-spec-reconciliation.md:160,169`), phase 6 "APPROVED
2026-10-18 … and each still waits on the D160–D172 rows its line names"
(`docs/mobile/implementation-plan.md:46-49`). What exists is **account** switching across
installations (D114, `mobile/README.md:474`).

### ZF26 — **Exists, in half** — status and spend are built; the roster is not

The job list is built (P3.3) and reads status and spend straight from the Realtime Database
(`mobile/src/jobs/list.js`; `mobile/src/feed/nodes.js:121-122`; `mobile/src/feed/recompute.js`).
The roster half is ZF23.

### ZF27 — **Gap** — QR pairing and the three roles exist in no code

`fugaro pair` does not exist: `internal/cli/project_cmd.go:394` says "nothing mints a device
node yet ('fugaro pair' is plan task P6.6 and is not built)", and no QR generator is vendored
anywhere in `go.mod` or `internal/`. The roles are defined **only** in the owner's spec
(`docs/mobile-auth/spec.md` — Viewer / Operator / Admin as an allowlist capability table),
and D225 puts the role as a **field on the device node**, read in v1 by the CLI and the
console and **never by the rules** (`docs/mobile-auth/reconciliation.md:466-505`). The whole
`docs/mobile-auth/` folder is "plan only. No code is implemented… nothing here is approved"
(`docs/mobile-auth/README.md:3`), and its sixteen decisions D221–D236 are **recommendations
the owner has not answered** (`docs/mobile-auth/reconciliation.md:443-448`).

### ZF28 — **Rule** — what the voice ruling fixes, and what it forbids

D44 answered **local-only for v1** (`docs/plans/2026-10-16-voice.md:11`); D47 answered that
destructive voice commands are **not in v1** (same line). The in-app local/remote switch is
**design only and an evaluation, not an adoption**: `docs/mobile/design/voice-evaluation-switch.md:1-5,13-23`
— "Status: design only; nothing is implemented", default local, a persistent indicator, a
named provider allow-list, "**No remote audio or text while a confirmation is pending, and
never on any destructive-command path**", and fail-closed-to-local. A marketing page that
shows a phone talking to a cloud speech service would describe the thing the design
deliberately keeps behind a switch that is not built.

### ZF29 — **Gap** — there is no mobile page, and no "coming soon" anywhere

Eleven routes, none of them about the phone (`website/src/pages/`). The only mention of the
app on the live site is the privacy page's "The phone app" section
(`website/src/pages/privacy.astro:237-267`). A repo-wide search finds no "coming soon"
wording under `website/`, `docs/website/`, `docs/mobile/` or `README.md`.

### ZF30 — **Open** — a "coming soon" page argues with the site's own hold-back rule

`docs/website/README.md:25`: **"Everything the site would say that the product cannot back
today is held back"** — which is why there was no console download link while the console was
unshipped, and why there is no "~2 h" sentence. The mobile page would be the **first** place
the site advertises an unshipped artifact. It can be done honestly (a badge, no date, and
every bullet marked shipped or planned), but that is a decision, not a default. §12 D240.

### ZF31 — **Rule** — the mobile release path is staged, not applied

`docs/pending-workflows/mobile-release.yml:1-3` — "STAGED, NOT APPLIED". Nothing in this
line of work may edit `.github/workflows/`.

## 6. The waitlist: four doors, and what each costs

The brief asks for "an optional waitlist signup", which **collects personal data** (an email
address) on a site that is a static Astro build with no backend. Today the site has **no
`<form>` and no `mailto:` link anywhere** (a search of `website/src`, `website/public` and
`website/data` finds `<form>` and `mailto:` only inside tests).

### ZF32 — **Gap** — nothing on the site collects anything today, and that is asserted

`website/src/pages/privacy.astro:162` — "It has no account, form or comment box." That
sentence is on the page a reader is sent to from the consent banner, and it would become
false the moment a waitlist ships. `website/test/privacy.mjs:673-708` (`contactFailures`)
**refuses any e-mail address anywhere on the privacy page** and allows exactly two contact
routes: the repository's issue tracker and `SECURITY.md`. `website/test/consent.mjs:740-748`
requires every page to link `/privacy/`.

### ZF33 — **Rule** — the CSP and the origins gate refuse a third-party form

`firebase.json:10` carries `form-action 'self'`. `website/test/origins.mjs:423-436`
(`CSP_DIRECTIVES`) is the token-by-token contract, and `cspFailures` (`:444-473`) fails on any
added token, any missing token and any unknown directive. Stronger: `origins.mjs:319-321`
classifies a `<form action>` as **first-party**, and `refuse()` (`:137-163`) refuses **any**
non-canonical host for it **even when the host is on `allowed_hosts`**, with the message "a
script, stylesheet, font, preload or form may only come from this site (D106: no CDN)". The
plant is at `origins.mjs:626` ("a form posting elsewhere"). So a Buttondown/Mailchimp/Formspree
action needs `firebase.json`, `CSP_DIRECTIVES.form-action` **and** `refuse()`'s form case
changed, each argued for.

### ZF34 — **Rule** — the site's one script may not fetch

`website/test/copy.mjs:331,333` bans `fetch(`, `XMLHttpRequest`, `sendBeacon`, `WebSocket`,
`EventSource` and dynamic `import()` in `site.js`, and bans a `://` anywhere in the file. A
Firebase-function or Firestore-REST waitlist would have to delete that ban, widen
`connect-src` (`origins.mjs:432`) and add the project's origin to `firebase.json` — three
gates, all of which exist to keep "nothing phones home" (Check 69b) true.

### ZF35 — **Open** — the four doors, and this document does not choose

1. **A `mailto:` link.** No form, no third party, no CSP change. **Refused today** by
   `website/test/linkcheck.mjs:155-157` (only `https:` links; the plant at `:279,283` is
   "uses mailto:") and by `privacy.mjs`'s `contactFailures`. Cost: change two gates and the
   privacy page's "no account, form or comment box" sentence — and the reader's mail client
   becomes the collection point, which is honest but is not a waitlist.
2. **A form posting to an owner-controlled third party** (Buttondown, Mailchimp, Formspree,
   …). Cost: ZF33's three changes, plus the privacy page's GDPR duties — a named controller,
   a lawful basis, a retention statement and the processor named. The privacy page today has
   **none** of those: no controller identity, no address, no retention line, and the words
   GDPR and legal basis do not appear on it.
3. **A Firebase function or Firestore write.** Cost: ZF34's three changes **and** it puts the
   site's own request into the same project the product uses, which is the thing D91
   deliberately separated (the site has its own Firebase project, `docs/website/README.md:22`).
4. **No waitlist.** The page carries the badge and the "coming soon" copy and no signup. Cost:
   the brief's "optional" is declined, and the owner collects interest through the repository,
   which is already the site's only contact route.

The recommendation is in §12 (D239); the choice is the owner's, and this document does not
make it.

## 7. The pinned zoom section: what the constraints allow

### ZF36 — **Rule** — the CSP and the one-script rule fix the mechanism

`script-src 'self' https://www.googletagmanager.com`, no nonce, no hash, no `'unsafe-inline'`
(`firebase.json:10`; `origins.mjs:423-473`; `website/test/brand.mjs:186-188`). The site ships
**one** script file, `website/public/scripts/site.js` (50,714 bytes, 978 lines),
`website/test/budget.mjs:456-499` asserts `dist`'s JavaScript is exactly that file and that
every page's inline JS is 0, and `website/test/layout.mjs:426` refuses an inline script in the
head. **A scroll-driven section therefore has to be CSS-first, or an addition to `site.js`.**
The JS budget is read out of `website/README.md:771-812` and re-derived only when the owner
asks (D192 and the owner's ruling of 2026-10-20, `docs/website/redesign/README.md:889-898`).

### ZF37 — **Rule, with an unverified edge** — the reduced-motion neutraliser and scroll timelines

`website/src/layouts/Base.astro:236-246` writes one universal rule:

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 1ms !important;
    animation-delay: 0ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 1ms !important;
    transition-delay: 0ms !important;
    scroll-behavior: auto !important;
  }
}
```

`website/test/a11y.mjs:592-608` (`motionFailures`) requires every `animation`, `transition`
and `scroll-behavior: smooth` a page declares to be gated on `no-preference` **or**
neutralised by a matching `reduce` rule, and the universal rule satisfies it — which means a
CSS scroll-driven animation **passes the gate**. Whether it also *behaves* is a different
question: `animation-duration: 1ms !important` and `animation-iteration-count: 1 !important`
land on the exact properties a `animation-timeline: view()` animation uses, and the
interaction is **ZU1** below. This is the single highest-risk item in the brief and it is the
reason Part 5 of the plan starts with a spike rather than with markup.

### ZF38 — **Rule** — the homepage's lead region is closed to a diagram

`website/test/fold.mjs:158-172`: between a page's `<h1>` and its first `<h2>` only
`span.gradient-text`, `p.hero-prompt`, `span.hero-prompt-mark`, `p.hero-lead`, `p.hero-actions`,
`a.hero-cta` and `a.hero-cta-quiet` may appear, and the failure message says it in words:
"the diagram, the slider and the install block belong below it". A zoom section anywhere on
the homepage sits **below** the lead, or the build fails.

### ZF39 — **Rule** — no-JS: all four levels must be visible as stills

The site's pattern is markup-first and script-second: the console slider's track is a native
`scroll-snap-type: x mandatory` container, its dots are `<a href="#…">` anchors and its
control bar is emitted `hidden` (`website/src/components/ScreenshotSlider.astro:127-187`;
`website/test/console-screens.mjs:643`). The home slider's tabs are fragment links with CSS
`:target` (`website/src/components/HomeSlider.astro:235-245`), and `website/test/slider.mjs:205-381`
refuses a `<script>` inside it. A zoom section with no script must therefore render **four
stills, in order, all readable**, with the markers as anchors — which is also the brief's
"still" half and makes the animation a pure enhancement.

### ZF40 — **Rule** — accessibility, and what a new component inherits

`website/test/a11y.mjs` (`pageA11yFailures`, `:611-778`): one `<h1>`, one `main`, one
page-level `<header>` and `<footer>`, at least one **named** `<nav>`, a skip link first,
every `<img>` with `alt`, every `<svg role="img">` **named**, every control with an accessible
name, **no positive `tabindex`**, no focusable element inside `aria-hidden="true"`, no
removal of the focus outline except `:focus:not(:focus-visible)`, and contrast from the
palette's own tokens. `:926-945` already reads the homepage's slider as an **ordered list**
of three steps, and `website/test/base-styles.mjs` and `website/test/home.mjs` add the 44 px
control floor. Every new page inherits all of it on the first build.

### ZF41 — **Gap** — nothing enforces "the same shapes across levels"

The brief's constraint — "a job is always the same tile, the orchestrator always the same
shape" — is the one thing that makes a zoom sequence legible, and **no test can see it**
because the model that would write it cannot view an image (ZF43). The repository's nearest
precedent is `RunDiagram.astro:44-53`: "Two layouts of one picture, both generated from the
labels above, so the wide drawing and the tall one cannot say different things", held by
`website/test/how-it-works.mjs:36-38`. The testable version of the brief's rule is an
**SVG `<symbol>` rule**: one `<defs>` of entity symbols, every level drawing entities only
through `<use>`, and a hermetic test that refuses a level that draws its own copy. §12 D244.

### ZF42 — **Exists** — the drawing is painted from the palette, and that is a gate

`website/test/how-it-works.mjs:1110` `runPaintFailures` (commit `7a385463`, #816) reads the
paint of every `<rect>`, `<path>`, `<line>` and `<text>` of both run drawings out of the
built page and its stylesheet, and refuses a shape no rule can reach, a colour that is not
the palette's, a `var()` in an SVG presentation attribute, a label under 4.5:1, a shape whose
every paint is `none`, and **an element kind the reading has not been taught**. New diagram
work inherits this gate and must extend it rather than route around it.

### ZF43 — **Rule** — the agent that builds it cannot see it

D190 (`docs/website/redesign/README.md:85`) and RU1 (`docs/website/redesign/reconciliation.md:664-668`):
the design lives in the owner's tool, screenshots are regenerated and never committed, and
"whether the finished site matches the approved design is the owner's step". Every part of the
plan therefore ends in an owner's visual check, and every agent-side verification is a number
or a text assertion.

## 8. The homepage "See it" slider: what exists, what moves

### ZF44 — **Exists** — the console's slider is real, data-driven and gated

`website/src/components/ScreenshotSlider.astro` takes `screens` as its only prop, renders one
`<li class="screen">` per row with `<figure>`, `<img>` and `<figcaption>`, a focusable named
`<ul class="screens-track">`, a `hidden` control bar with two 44 px arrows and a
`role="status"` counter, and one `<a>` dot per slide. It is embedded on the console page at
`website/src/pages/console.astro:201`. Its four screenshots are
`website/public/screenshots/console-{live,run,spend,installations}.webp`, declared in
`website/data/console-screens.yaml` and recorded with their alt text and dimensions in
`website/public/screenshots/MANIFEST.json`. `website/test/console-screens.mjs` is the gate,
including the script's behaviour driven through `test/lib/dom.mjs`'s stub browser.

### ZF45 — **Exists** — the home slider is three tabs and the "See it" panel holds one image

`website/src/components/HomeSlider.astro:156-160` — `easy` / `benefits` / `see`, rendered as
three `<a class="tab">` fragment links with CSS `:target` panels. The "See it" panel
(`:286-318`) holds `div.watch-shot[data-slot="watch-screenshot"]` with
`/images/fugaro-watch.png` and its caption. The component's own header comment (`:44-50`)
records why it does **not** use `ScreenshotSlider.astro`: that component "renders
`website/data/console-screens.yaml`, it carries the console's own heading, ids and captions,
and it is a scroller rather than a tab panel".

### ZF46 — **Gap** — `site.js`'s slider is generic at the door and hard-coded inside

`website/public/scripts/site.js:956-957` binds **every** `[data-slider]` on the page, but the
interior selectors are the console's vocabulary: `SLIDER_TRACK = '.screens-track'`,
`SLIDER_SLIDE = '.screen'`, `SLIDER_DOT = '.screen-dot'` (`site.js:507-514`), and
`slider(section)` returns early without a `.screens-track` or with fewer than two slides
(`site.js:527-534`). A homepage slider can therefore reuse the script **only** by borrowing
the console's class names — and `website/test/slider.mjs:447-460` forbids `site.js` from
naming `home-slider`, `tab-bar`, `easy-step` or any id inside the home slider. The honest
answer is a rename to `data-slider-*` hooks. §12 D246.

### ZF47 — **Wrong** — two assertions pin the homepage's pictures to exactly two files

`website/test/home.mjs:1477-1484` asserts the whole homepage's `<img>` sources are exactly
`['/logo.png', '/images/fugaro-watch.png']`, with the comment "A homepage that grew a third
`<img>` … is what this last assertion is for". `:1407-1412` asserts the watch slot holds
**exactly one** `<img>`, and `:1510-1516` asserts exactly one component (`HomeSlider.astro`)
renders the slot. Three console slides in the "See it" panel break all three, by design. The
slider therefore changes `home.mjs` in the same commit, and the new assertion has to be at
least as strong (the slot holds exactly the rows the data file names, in order).

### ZF48 — **Rule** — the homepage's tab contract is pinned

`website/test/slider.mjs:205-381`: exactly one `.home-slider`, exactly **three** `a.tab` and
three `.panel`, labels exactly `['Easy to use', 'Why Fugaro?', 'See it']` in that order, each
tab's href matching the panel beside it, each panel's first `<h2>` equal to its tab label, no
`hidden`/`aria-hidden` on a panel, no `<script>` in the block, no `role="tab"`, no
`tabindex="-1"`, 44 px tabs at 360 and 1280. `website/test/a11y.mjs:926-945` reads the same
three steps as an ordered list. "Replace the single screenshot with a slider" happens
**inside** the third panel and must not become a fourth tab.

### ZF49 — **Gap** — no phone screenshot exists anywhere in the repository

The committed images are the site's icons, `logo.png`, `images/fugaro-watch.png`, the four
console WebPs and `brand/`'s three; `mobile/assets/` holds the Expo app icon and the adaptive
icon and nothing else. The brief's "add phone app screenshots once available" is therefore a
placeholder, not a task.

### ZF50 — **Rule** — agents do not commit screenshots; the owner supplies them

D190 and the root `.gitignore:24-27` (`/docs/website/redesign/current/*.jpg`,
`/docs/website/redesign/after/*.jpg`) are the redesign's half. The product half is
`website/README.md:1409-1562`: the console's four WebPs are **fixture captures** converted
from PNGs by an owner-run step (`cwebp -resize 1920 0 -q 80`), with `MANIFEST.json` copied in
in the same commit, and "a test may not commit a picture either" (`:1547`).
`fugaro-watch.png` was a **one-off owner waiver** (`docs/website/redesign/README.md:616-629`).
So the slider's slides arrive as the owner's files plus a data row each; the plan writes the
slot and the gate, never the pixels.

### ZF51 — **Open** — the brief's "#842" is not in this checkout

The brief says the home slider is "being reworked in PR #842 (feedback A)". This branch's
base is `3021f4b3` (#841) and `git log --all` carries no `#842`; the three-tab
`HomeSlider.astro` described above is the pre-#842 state. Part 1 of the plan is therefore
written to land **after** #842 and to re-read the component rather than assume its shape.
§12 D250.

## 9. Site structure: the page, the nav and the FAQ

### ZF52 — **Gap** — there is no "Why GCP" page and the nav has no room reserved

`website/src/layouts/Base.astro:132-157` `NAV_GROUPS` is three groups of three: Product
(How it works, Getting started, Caps and costs), Reference (Docs, Console, Changelog), Project
(Security, FAQ, Community). `/` is the wordmark and `/privacy/` is a footer link
(`Base.astro:173`). Eleven routes, nine of them in the menu.

### ZF53 — **Rule** — a route the menu does not list throws

`Base.astro:163-167` `navItem()` throws at build time: "Base.astro: something links {href},
which the menu's groups do not list". A new page is added to `NAV_GROUPS` in the same commit
as the footer or any other link to it.

### ZF54 — **Rule** — D182 pins three groups of three, and no test counts items

`website/test/base-styles.mjs:2635-2636` fails on any group count other than 3, with the
message "the nav is three groups of three (D182)"; the group `<details>` must share one
`name`, each group's `<ul>` must hold at least one `<a>`, and exactly one link carries
`aria-current="page"` and it must match the route (`:2639-2693`). **Items per group are not
asserted** — so a fourth Product item passes the gate while contradicting the sentence D182
and the layout comment both carry. That contradiction is the owner's to accept or refuse.
§12 D238.

### ZF55 — **Exists** — the FAQ already answers the GCP question, in the wrong place

`website/data/faq.yaml:52-57`, entry `where-it-runs`: *"In your own Google Cloud project, not
on a service of ours: each task is one Cloud Run job execution. AWS and Azure are not built."*
The brief's "Do I need to be on GCP?" is a seventh entry that links to the new page, and the
existing entry must not end up saying the same thing twice.

### ZF56 — **Rule** — an FAQ answer is plain text; links come from two fields only

`website/src/pages/faq.astro` parses the file with its own reader (`parseFaqYAML`, `:65-207`)
and validator (`loadFaq`, `:257-306`); the answer is a folded block scalar rendered literally
as `{entry.answer}` inside `<p class="question-answer">` (`:400`), with **no markdown and no
`set:html`**. A link in an answer is impossible: it comes from `source` (rendered as a
GitHub blob link, `:401-402`) or from `more_href` + `more_label` (`:403-408`), and
`more_href` must start with `/`. `website/test/faq-community.mjs:741-760` requires every
`<article>` to carry a source link at the branch `ci.yml` pushes to, and the file must name a
path that exists.

### ZF57 — **Rule** — a new page inherits every gate, and moves the whole-site figure

`linkcheck.mjs` (every link and anchor resolves, every external host is on
`site.yaml`'s `allowed_hosts`), `budget.mjs` (lead ≤ 40 words, page ≤ 949 words, page weight
and the whole-site figure), `a11y.mjs`, `meta.mjs`, `origins.mjs`, `consent.mjs` (every page
links `/privacy/`) and `base-styles.mjs` all iterate the built pages, so a twelfth route is
held to all of them on its first build. There is **no** sitemap (`astro.config.mjs`'s
`integrations: []`; `website/test/layout.mjs:458` refuses a `rel="sitemap"`), and **no test
asserts the route count** — "eleven routes" is prose in `docs/website/README.md:3`,
`docs/website/reconciliation.md:3`, `docs/website/implementation-plan.md:3` and
`website/README.md:248,254,433,436`, and it goes stale the moment a page lands. Three of the
four name 2026-10-17, the day the site landed, and a twelfth route does not falsify a sentence
about that day; `website/README.md`'s four are present tense and move with the page.

### ZF58 — **Rule** — a competitor claim needs a source and a date

`docs/website/README.md:25`: "no competitor comparison without a source and a date", echoed by
`docs/website/reconciliation.md:147` (finding 51) and `:246` (U4). **No test enforces it.**
The nearest machinery is `website/data/faq.yaml:93-103` and `faq.astro:226-233` — the FAQ's
`comparison:` key was deleted on 2026-10-21 and writing it back **stops the build**
(`website/test/faq-community.mjs:408-425`) — and `website/test/wording.mjs`'s
`RETIRED_CLAIMS` (`:128-137`), which refuses two specific unsourced lines.

### ZF59 — **Gap** — nothing holds the site to "what the product cannot back"

There is no gate that reads a page's claims against the tree. The two enforcement points are
`meta.mjs`'s `PHRASES` (`:41-52`, about provenance text, not about product claims) and
`wording.mjs`'s `RETIRED_CLAIMS`. The brief's "Why GCP" page is exactly the kind of page that
needs one: the framing rule the owner suggests ("never 'no real alternative'") is a
**banned-phrase rule with a required-phrase partner**, and it is cheap to write and cheap to
mutation-prove. §12 D249.

## 10. What could not be verified, and by whom

- **ZU1 — how a CSS scroll-driven animation behaves under the site's reduced-motion rule.**
  `Base.astro:236-246` sets `animation-duration: 1ms !important` and
  `animation-iteration-count: 1 !important` on `*` under `prefers-reduced-motion: reduce`.
  Whether an `animation-timeline: view()` animation is neutralised, frozen at its first frame,
  or unaffected (the timeline being progress-based rather than time-based) is a browser
  behaviour this repository cannot test: the gates are hermetic and drive a stub DOM, and the
  model writing this document cannot view a page. **Owner's browser, or a Playwright spike in
  the plan's Part 5.** The brief's whole "Format" bullet rests on the answer.
- **ZU2 — what PR #842 does to the home slider.** Not in this checkout (ZF51).
- **ZU3 — whether the owner's design tool has a board for the zoom sequence.** D190 puts the
  design there and the repository keeps only the numbers; nothing under
  `website/design-ref/` covers four levels. If a board exists, the plan's Parts 4 and 5 should
  read it first.
- **ZU4 — the rendered legibility of a four-level SVG on a 360 px screen.** Every gate the
  site has is text-based; the smallest type the palette's contrast rules allow is not the same
  question as whether a label is readable at a quarter of a phone's width — the redesign hit
  exactly this on `fugaro-watch.png` (`docs/website/redesign/README.md:657`). Owner's check.
- **ZU5 — what a waitlist would do to Check 69b.** "Nothing phones home" is a live, owner-run
  check (`docs/gcp-live-checklist.md`, Check 69b). Any waitlist option in §6 other than "no
  waitlist" changes what that check observes; the check's text would have to be amended, and
  this document cannot say by whom or when.

## 11. What this document does not decide

- It does not choose the waitlist's door (§6). It lists the four and their costs.
- It does not choose where the four levels live, or the nav's shape (§12 D237, D238).
- It does not write any copy. It says which sentences the tree supports and which it does not;
  the page's words are the plan's, and the owner's to approve.
- It does not re-open D90–D109 or D180–D192. Where the brief conflicts with them — the nav's
  "three groups of three" (ZF54), the hold-back rule (ZF30), D190's screenshot rule (ZF50) —
  the conflict is recorded and turned into a decision rather than resolved.
- It does not allocate the check numbers the plan proposes (D247); the register's rule is that
  a reservation is not a fact about the tree (`docs/gcp-live-checklist.md:195`).

## 12. Open for the owner

Each row is a question this document cannot settle, with the **recommendation first** and the
cost of the alternative. The owner is asked one row at a time, later, by the coordinator; none
of these is answered here, and none of them is written "(default)".

The numbers are **D237 onward**: the highest written decision under `docs/` is **D236**
(`docs/mobile-auth/`), the block `D198`–`D200` sits inside the coordinator's "a move into the
block must start at D198" note and `D200` may be held by the owner's notes
(`docs/coordinator/README.md:113`; `docs/mobile-auth/README.md:41`), and the convention is to
take the first block above the highest allocation rather than to derive one by grepping
(`docs/website/redesign/implementation-plan.md:556-569`). **The allocation is the owner's; this
document proposes D237–D252 and will renumber itself if the owner takes the block for
something else.**

| # | Question | Recommendation | Why | Alternatives (cost) |
|---|---|---|---|---|
| **D237** | Where do the four levels live? | A section on `/how-it-works/`, under "The run, end to end" | The homepage's lead region is closed to a diagram (ZF38); the homepage's picture of a run is deliberately How It Works' (ZF42, `home.mjs:414-443`); and How It Works is already the page whose subject is the run | A page of its own — a twelfth route, a fourth Product slot, and a page that says what the FAQ already says (ZF55); or the homepage — it moves the heaviest page's bytes and needs a new band under the slider |
| **D238** | Does the nav gain a fourth Product item? | Yes — "Why GCP" as the fourth Product item, placed **after** "How it works", and D182's "three groups of three" sentence amended in the same commit | The brief asks for it (spec, Site structure); no gate counts items (ZF54), so the only cost is the sentence, and leaving the sentence standing while shipping four is how a document starts lying | A fourth **group** ("Architecture") — three `<details>` become four, which `base-styles.mjs:2635-2636` refuses, so the gate changes too; or no nav entry, the page linked from the FAQ and Level 4 only — cheapest, but a page nobody can find |
| **D239** | Which waitlist door? | **No waitlist in this part**, and revisit after the app ships a voice engine | Every other door costs a privacy-page rewrite and at least one gate change (ZF32–ZF34), and the site's own rule is to hold back what the product cannot back (ZF30). The page loses nothing: the badge and the copy still say what is coming | A `mailto:` link — two gates and the privacy sentence change, and the reader's mail client is the collector; a third-party form — CSP, origins gate, plus GDPR duties the privacy page does not carry today; a Firebase function — also breaks "nothing phones home" (ZU5) and puts the site's request in the product's project, which D91 separated |
| **D240** | May the site carry a "coming soon" page at all? | Yes, under three conditions: no date is promised, every bullet is marked shipped or planned, and the page carries a badge that says it is a preview | `docs/website/README.md:25` holds back what the product cannot back; a labelled preview is not a claim about today, and the brief asks for the page | No page until the app ships — safest, and it costs the brief's whole third item; or an unlabelled page — it would make the site's own FAQ answer about "what exists" false |
| **D241** | What may the mobile page say the app does? | Only what the tree backs, per bullet: status and spend **are** readable (ZF26); the roster, project/agent switching by voice, QR pairing and the three roles are **planned** (ZF23, ZF25, ZF27); the wake word is a transcript prefix with no engine (ZF24) | ZF23 is a direct contradiction — the phone's grant denies `agents/**` — and the rest is phase 6, which is approved but unbuilt | Say it all as present tense — the page then states four falsehoods the repository documents against; say nothing but "coming soon" — honest and says nothing, which is the page's whole job |
| **D242** | Is the mobile page's voice bullet local-only? | Yes: describe voice as on-device, and say nothing about a cloud speech service | D44 (local-only for v1) and the evaluation switch's "never on any destructive-command path" (`docs/mobile/design/voice-evaluation-switch.md:13-23`) | Mention the local/remote switch — it is design only and not built, and a marketing page is the worst place to preview a switch the owner has not adopted |
| **D243** | Where does "Why GCP" get its facts? | A data file, `website/data/why-gcp.yaml`, with one row per claim carrying `text`, `source` and `date`, rendered by the page and held by a test — the pattern `website/data/faq.yaml`'s `source` field already uses | ZF59: no gate holds a claim to the tree today, and the four claims in §4 have four different verdicts; a row per claim is the smallest thing a test can read | Prose on the page — nothing holds it, and the next round cannot tell a supported sentence from an unsupported one |
| **D244** | How is "the same shapes across levels" made testable? | One `<defs>` of `<symbol>`s, one per entity (job tile, orchestrator, developer, console, project), shared by all four levels; every level draws entities **only** through `<use>`; a hermetic test refuses a level that draws its own copy of an entity, and refuses an entity no level uses | ZF41: the constraint is the thing that makes a zoom legible and no gate can see an image; `<use>` makes it a text assertion | Draw each level standalone — nothing holds the shapes together, and the first edit to one level silently breaks the sequence |
| **D245** | What are the level markers? | `<a href="#level-N">` anchors, each with an accessible name, in a `<nav>` of their own, mirroring the console slider's dots | Anchors need no script, the linkcheck gate resolves them, and the no-JS page keeps them working (ZF39) | `<button>`s — a no-JS reader gets dead controls; a scripted scroll-spy — new JavaScript for something CSS can do |
| **D246** | How does the homepage slider reuse `site.js`? | Rename the script's interior selectors to `data-slider-track` / `data-slider-slide` / `data-slider-dot` hooks, keep `[data-slider]` as the door, and let both components carry them | ZF46: the container scan is already generic and the interior is not; hooks let one component serve two pages without either borrowing the other's class names | A second slider implementation in `site.js` — the JS budget grows twice and two copies drift; borrowing `.screens-track` on the homepage — the console's names leak into a page that has no console, and `slider.mjs:447-460` already forbids the reverse |
| **D247** | Does the zoom work take a live check, and which number? | Yes — **112** and up (70d if the redesign's block is preferred), written and **not** run, as 69a–69c and 70a–70c are | The highest written check is **111** (`docs/gcp-live-checklist.md:5781`); a visual feature with no owner-run check is a feature nobody looks at on the live site (ZF43) | No check — the pinned section's behaviour under reduced motion (ZU1) is then never observed anywhere |
| **D248** | May the page print "disposable"? | No. Use "one integrated project", and where the word is wanted, define it: a managed project is **new** and holds only Fugaro's resources | ZF15: `docs/design/managed-projects.md:18,211` rejects "disposable" and lists what would be lost; the brief's own suggested framing uses the word, so this is the owner's to keep or drop | Keep "disposable" — the page then contradicts `docs/design/`, and the FAQ's "where does the code run" answer becomes the second place the site disagrees with itself |
| **D249** | How is the framing rule enforced? | A `wording.mjs`-style pair: a **banned** list (`no real alternative`, `no alternative`, `the only cloud`) and a **required** sentence the page must carry ("no other cloud offers this as one integrated project"), each with a planted defect | ZF58/ZF59: the rule is the owner's and no gate holds it; the repository's own `RETIRED_CLAIMS` is the precedent | Convention only — the next round re-introduces the phrase and nobody notices |
| **D250** | When does Part 1 (the shared slider) land? | After PR #842 merges, re-reading `HomeSlider.astro` first | ZF51: #842 is not in this checkout and it touches the same component; landing first means writing an assertion against markup that is about to change | Land now against the pre-#842 component — two rounds of `home.mjs` churn for one feature |
| **D251** | Who supplies the slides? | The owner, as files plus a `MANIFEST.json`-shaped record and a data row each, exactly as the console's four did (`website/README.md:1409-1562`) | ZF49, ZF50: no phone screenshot exists and no agent may commit one; the console's own process is the precedent and is already gated | An agent generates placeholder images — D190 and `website/README.md:1547` forbid it, and a placeholder that ships is worse than an empty slot |
| **D252** | What happens to the `fugaro watch` slide? | Keep it, last, as the brief says, with its existing alt text and caption, and keep `fugaro-watch.txt` as its text fallback | The brief asks for it; the alt text and the transcription are already gated (`home.mjs:1487-1533`), so the move is a reordering, not a rewrite | Drop it — the brief explicitly keeps it, and the transcription is the only accessible rendering of a terminal screen the site has |
