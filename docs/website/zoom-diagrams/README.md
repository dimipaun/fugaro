# Website zoom diagrams

**Status: plan answered; Part 1 and Part 4 in progress.** Owner: **@Dimi**. Spec date:
**2026-10-21** — and **every decision the work raised, `D237`–`D252`, was answered by the owner
on 2026-10-21** ("The owner's answers (2026-10-21)" below, and the last column of the
reconciliation's §12).
This directory is the documentation of a **third line of work on the site**, after the site
itself ([`../README.md`](../README.md), findings `F1`–`F63`, decisions `D90`–`D109`) and the
redesign ([`../redesign/README.md`](../redesign/README.md), findings `RF1`–`RF26`, decisions
`D180`–`D192`). It is a directory rather than a pair of files under `docs/design/` and
`docs/plans/` for the same reason the other two are: the site is its own build, in its own
language, on its own deploy.

**No code, no schema, no config and no page has changed in this line of work's own branch.**
Nothing under `website/` is touched by it: the parts in progress are the plan's **Part 1** and
**Part 4**, each in a pull request of its own, and Parts 2, 3 and 5 are written and waiting on
the owner's go. The four documents below are the whole of it, and every claim in them was read
out of the tree at `3021f4b3` on 2026-10-21, with one exception the answers recorded: `#842`
has merged into this branch's history since (`3c5cc354`), so the slider the answers name is
the merged component and not the one the readings were taken from.

## The four documents, in reading order

| Document | What it is |
|---|---|
| [spec.md](spec.md) | **The owner's text, verbatim** — committed alone in this branch's first commit, byte for byte, before anything that answers it. It is the source document for the other three, and it is not edited when they disagree with it. |
| [reconciliation.md](reconciliation.md) | What the brief asks for, checked against `3021f4b3`. **Fifty-nine numbered findings** classified **Wrong / Partly / Exists / Gap / Rule / Open** — six labels, one per finding, and the headings use no seventh — each citing a path and a symbol; six claim tables (§3 the job's loop, §4 "Why GCP", §5 the mobile page, §6 the waitlist, §7 the pinned zoom section, §8 the "See it" slider); **five UNVERIFIED items** (`ZU1`–`ZU5`); and **sixteen decision rows `D237`–`D252`** under "Open for the owner — answered 2026-10-21", each with the recommendation first and **the owner's answer** in its last column. |
| [implementation-plan.md](implementation-plan.md) | The plan that executes the reconciliation: **five parts**, each independently mergeable, each with its files, its hermetic and mutation-proven tests, a command an agent can run, and the owner's visual check. Two live checks: **112**, taken by the owner's answer to D247 and written and not run, and **113**, still proposed. |
| this file | The index, the status line above, the answer list below, and the typo record. |

## The owner's answers (2026-10-21)

**Every one of the sixteen decisions was answered by the owner on 2026-10-21, and no answer
went against its recommendation.** The rows keep the text they were written with — the
recommendation, the why and the alternatives — and the choice stands beside them in the
reconciliation's §12, whose last column is the record. The short form, one line each, and
none of them written "(default)":

- **D237** the four zoom levels are a section on `/how-it-works/`, under "The run, end to end".
- **D238** "Why GCP" joins the **Product** menu after "How it works" — and the owner's later brief (PR #845) drops the Reference group, so the menu it lands in is Product (How it works, Getting started, Caps and costs, Console) and Project (Docs, Security, FAQ, Community, Changelog); the "three groups of three" sentence moves with it.
- **D239** **no waitlist** — the badge and the "coming soon" copy only; the privacy page, the CSP and Check 69b are untouched.
- **D240** a "coming soon" page is allowed, with no date, every bullet marked shipped or planned, and a preview badge.
- **D241** the bullets are marked per the tree: **shipped** — status and spend; **planned** — the roster, switching by voice, QR pairing, the three roles, the wake word.
- **D242** the voice bullet is **on-device only**.
- **D243** the "Why GCP" facts live in `website/data/why-gcp.yaml`, one row per claim (`text`, `source`, `date`), held by a test.
- **D244** the same shapes are shared SVG `<symbol>`s, drawn once and used by all four levels through `<use>`.
- **D245** the level markers are anchor links in a `<nav>` of their own.
- **D246** the shared slider hooks are `data-slider-track` / `data-slider-slide` / `data-slider-dot`, with `[data-slider]` as the door.
- **D247** the zoom work takes owner check **112**, written and not run.
- **D248** the page **must not say "disposable"**.
- **D249** the framing rule is a banned list plus a required sentence, each with a planted defect.
- **D250** the shared slider lands **after #842, which has merged** (`3c5cc354`).
- **D251** the slides are the **images the Console page already uses** — its four committed screenshots — with phone screenshots later, supplied by the owner as files.
- **D252** the Fugaro Watch slide **stays last**, with its alt text, caption and text fallback.

## The short version

The brief is a good brief and **the site can carry most of it — but three of its four asks are
pages that do not exist, and its product claims are mostly not true as written.** The site is
an eleven-route Astro build with fourteen hermetic gates; there is no "Why GCP" page, no page
about the phone app and no diagram of the four levels, and the homepage's "See it" panel holds
one `<img>` where the brief wants a slider.

The corrections the reconciliation makes, in one line each:

- **Level 1's loop is close to the runner and wrong in three of its six lines.** The "cheap
  reviewer" is the coder's own model (`internal/config/config.go:208` `StageRole`, `:226`
  `ModelFor`); the senior review is a
  **loop** of up to `agent.review_rounds` rounds, default 2, not "one final review"
  (`internal/runner/runner.go:1764`); and a draft PR flips to ready on a senior `ship` **and**
  a verified passing test on the final commit (`internal/runner/outcome.go:31`). The
  deterministic gate and the bounce are **recipe-selected**, not properties of a job.
- **"GCP projects are free and disposable" is rejected by the repository's own design.** A
  managed project is **not** disposable — the Terraform state bucket, the spend history, the
  kill-switch state and the application data live in it, and the Firestore location can never
  be changed (`docs/design/managed-projects.md:18,211`). And "it never runs Terraform" is
  false: Terraform **is** Fugaro's provisioning model, and D58 permits adopting an existing
  project (`docs/design/managed-projects.md:174`).
- **Managed projects are gated off today** (`internal/cli/recipes_skew.go:152`
  `managedDeployerShipped = false`), so "Fugaro gets its own isolated, Fugaro-managed project"
  is a roadmap sentence, not a present-tense one.
- **Firebase is the strongest part of the argument, and the brief overstates it.** All three
  services are used, but they are the **optional** budget/dashboard/history control plane —
  "the budget is off by default" (`README.md:99`) — and Firebase Auth is the identity of a run
  and the phone, not of the console or the CLI. The repository's own sentence is better than
  the brief's: **"Firebase stays GCP-bound regardless of compute backend"**
  (`docs/backends.md:11`).
- **"We started out wanting a pluggable cloud" is the opposite of the repository's position.**
  A second cloud is **out of scope for 1.0 and wanted from contributors**
  (`README.md:188-190,204`; `docs/backends.md:3`).
- **The mobile page's six bullets describe an app that is voiceless and unpaired.** "Live agent
  roster" is **contradicted** — the phone's read grant denies `agents/**`
  (`mobile/src/feed/paths.js:44`); the wake word is a transcript prefix test with no acoustic
  engine and no speech engine shipped (`docs/voice-module/reconciliation.md:539-545`); project
  and agent switching by name, QR pairing and the three roles are phase 6 and the unbuilt
  `docs/mobile-auth/` folder (`internal/cli/project_cmd.go:394`).
- **A waitlist collects personal data on a static site, and four gates stand in the way.**
  The site has no `<form>` and no `mailto:` today; the privacy page says it "has no account,
  form or comment box" (`website/src/pages/privacy.astro:162`); the CSP is `form-action 'self'`
  and the origins gate refuses **any** non-first-party form action even for a host on the
  allow-list (`website/test/origins.mjs:319-321,626`); and the one script may not `fetch`
  (`website/test/copy.mjs:331,333`). **The reconciliation lists the four doors and does not
  choose** (D239).
- **The pinned zoom section is a CSP, one-script, reduced-motion and no-JS problem before it
  is a design problem.** The highest-risk item is `ZU1`: the site's universal reduced-motion
  rule sets `animation-duration: 1ms !important` on `*` (`website/src/layouts/Base.astro:236-246`),
  and what that does to a CSS scroll timeline is unverified — which is why Part 5 begins with
  a spike that may cancel the part, and why the brief's "still" half ships first.
- **"A job is always the same tile" is the one thing no gate can see.** The model that writes
  the drawing cannot view an image (D190), so the constraint becomes a testable **SVG
  `<symbol>` rule** (D244): one `<defs>`, every level drawing entities only through `<use>`.
- **The homepage slider exists in half, and three assertions pin it to one image.**
  `website/test/home.mjs:1477-1484` requires the homepage's `<img>` set to be exactly
  `['/logo.png', '/images/fugaro-watch.png']`. The brief's slider breaks that on purpose, so
  Part 1 **replaces** those assertions with stronger ones rather than deleting them.
- **Screenshots are never committed by an agent** (D190; `website/README.md:1547`). The
  slides arrive from the owner as files plus a manifest-shaped record and a data row each —
  the process the console's four already went through.

## The typo record

**No typo needed fixing, and the spec is committed byte for byte.** The brief was read against
this branch's own copy of it line by line for spelling, punctuation, the em dashes, the `↔`
of Level 1's "cheap coder ↔ cheap reviewer" and the `→` of Level 3's "Orchestrator uses Fugaro
→ single arrow to the cloud", and none of the four are wrong as written. Nothing was
normalised: the straight quotation marks in `"a thousand feet"`, `"how do I interact?"`,
`"Why GCP"`, `"Coming soon"`, `"Fugaro"`, `"Architecture"`, `"for the terminal crowd"`,
`"Desktop console"`, `"Terminal dashboard"` and `"no real alternative"` are the owner's, and
they stay as the owner wrote them. **If a later round changes a character of [spec.md](spec.md),
it is an edit to the owner's document and must be recorded here.**

## What this line of work may not do

The two earlier plans' constraints hold, and this one adds none of its own beyond them. The
short form, with the finding that states each in full:

1. No inline script, no new script file, no nonce, no `on*=` handler (ZF36).
2. No new Astro integration, no dependency, no sitemap (ZF57).
3. No image committed by an agent (ZF50).
4. Nothing under `.github/workflows/` (ZF31).
5. No page added without a `NAV_GROUPS` entry, and no group count changed without
   `website/test/base-styles.mjs` moving with it (ZF53, ZF54; D238's answer amends the count
   itself, and PR #845's dropped Reference group lands with that gate).
6. No date promised for the phone app (ZF30, D240) — the answer of 2026-10-21 keeps the page
   and keeps the rule.
7. No sentence the tree cannot back — which is the whole of §4 and §5 of the reconciliation
   (ZF15–ZF27, ZF59).

## See also

- [`../README.md`](../README.md) — the site's own first line of work, and the "everything the
  site would say that the product cannot back today is held back" rule (`:25`) that D240 was
  asked about, and answered on 2026-10-21 under three conditions (no date, every bullet marked,
  a preview badge).
- [`../redesign/README.md`](../redesign/README.md) — D182 (the nav), D183 (one script), D186
  (the provenance gate), D188 (the word ceilings), D190 (screenshots) and D192 (the byte
  budgets), all of which this line of work inherits.
- [`../../backends.md`](../../backends.md) — the compute seam, the control-plane seam, and
  "Firebase stays GCP-bound regardless of compute backend".
- [`../../design/managed-projects.md`](../../design/managed-projects.md) — why "disposable" is
  the one word the "Why GCP" page may not print.
- [`../../mobile/README.md`](../../mobile/README.md) and
  [`../../mobile-auth/README.md`](../../mobile-auth/README.md) — what the phone app is today,
  and the sixteen unanswered decisions `D221`–`D236` the mobile page's claims rest on.
- [`../../voice-module/reconciliation.md`](../../voice-module/reconciliation.md) — the wake
  word's ruling: a transcript prefix test, no acoustic engine.
- [`../../gcp-live-checklist.md`](../../gcp-live-checklist.md) — the check register, whose
  highest written number is 111 and whose rule is that a reservation is not a fact about the
  tree (`:195`).
