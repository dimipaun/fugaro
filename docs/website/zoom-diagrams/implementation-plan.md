# Website zoom diagrams — implementation plan

Status: **Part 1 is implemented** (`website`'s PR of 2026-10-21: one slider, two pages —
the shared component, `website/data/home-screens.yaml`, and the hooks); **parts 2–5 are
plan only.** Owner: **@Dimi**. Date: **2026-10-21**.
Source: [spec.md](spec.md), the brief. This plan executes
[reconciliation.md](reconciliation.md) — its findings `ZF1`–`ZF59`, its unverified items
`ZU1`–`ZU5` and its decisions `D237`–`D252`. Read that document first: this one cites its
numbers and does not restate its evidence.

**The owner's ruling this plan waits on, recorded once:** every decision in the
reconciliation's §12 is a recommendation. **A part whose decisions are unanswered does not
start**, and the parts below are written so that the answering order is also the merging
order. No part needs all sixteen.

## 1. What this plan is for

| The brief asks for | Where it is done |
|---|---|
| a homepage "See it" slider, console first, Watch last | **Part 1** |
| a "Why GCP" page, its FAQ entry and its nav slot | **Part 2** |
| a mobile "coming soon" page | **Part 3** |
| the four levels as stills, shapes consistent across them | **Part 4** |
| the pinned, scroll-driven zoom section with level markers and fallbacks | **Part 5** |

Parts 1, 2 and 3 are content on pages that exist or are new but ordinary. Parts 4 and 5 are
one feature in two halves, split so that the stills can ship, be looked at and be approved
before a single animation exists.

## 2. What is new in this plan's constraints, on top of the two earlier plans' §3

1. **No new script file, no inline script, no `on*=` handler, no nonce.** One CSP, one
   script, both asserted token by token (`website/test/origins.mjs:423-473`,
   `website/test/budget.mjs:456-499`). Anything that needs JavaScript is an addition to
   `website/public/scripts/site.js`.
2. **The JS budget is a ceiling that does not move by itself.** `website/test/budget.mjs`
   reads its three figures out of `website/README.md:771-812`; since the owner's ruling of
   2026-10-20 they are re-derived **only when the owner asks**
   (`docs/website/redesign/README.md:889-898`), while the ceilings hold on every run. A part
   that grows `site.js` says by how much in its own pull-request body.
3. **The word ceilings hold on every build.** Lead ≤ **40** words (`website/README.md:821-826`)
   and page ≤ **949** words (`:854-876`), measured by `website/test/budget.mjs`'s
   `wordFailures`.
4. **Motion is gated or neutralised, and the neutraliser is universal**
   (`website/src/layouts/Base.astro:236-246`; `website/test/a11y.mjs:592-624`). ZU1 is the
   open question about what that rule does to a scroll timeline, and Part 5 begins with it.
5. **No-JS is not a fallback, it is the base case.** The markup renders everything; the script
   reveals controls it emitted `hidden` (`website/src/components/ScreenshotSlider.astro:162-168`).
6. **A new page is a new route**, held to all fourteen gates on its first build, and it must
   be in `NAV_GROUPS` or the build throws (`website/src/layouts/Base.astro:163-167`).
7. **No image is committed by an agent** (D190; `website/README.md:1547`). Pictures arrive
   from the owner with a manifest-shaped record and a data row.
8. **`.github/workflows/` is never touched by a run.** Anything a deploy needs is staged under
   `docs/pending-workflows/` for the owner to copy.

## 3. The parts

### Part 1 — one slider, two pages (ZF44–ZF51; D246, D250, D251, D252)

**What.** The console page's slider becomes a shared component, and the homepage's "See it"
panel becomes a slider that renders the same rows.

1. **The hooks.** Rename `site.js`'s interior selectors — `SLIDER_TRACK = '.screens-track'`,
   `SLIDER_SLIDE = '.screen'`, `SLIDER_DOT = '.screen-dot'` (`site.js:507-514`) — to
   `[data-slider-track]`, `[data-slider-slide]`, `[data-slider-dot]`, keeping `[data-slider]`
   as the door and `[data-slider-bar]`/`[data-slider-prev]`/`[data-slider-next]`/`[data-slider-counter]`
   as they are. Both components carry the hooks; the console's classes stay for styling.
2. **The shared component.** `ScreenshotSlider.astro` gains the three things that make it
   reusable — a `heading` prop (it hard-codes `'Screenshots'` and `console-screens` as its
   `data-slot`, `:112-116`), an id prefix, and a `caption` on the section — and keeps
   everything else: the named focusable track, the `hidden` bar, the dot anchors, the 44 px
   controls, the first-slide-eager rule.
3. **The data file.** `website/data/home-screens.yaml`, the same shape as
   `console-screens.yaml` (`version`, `screens[]` of `file`/`alt`/`caption`/`width`/`height`),
   read by a `loadHomeScreens()` in `website/src/content.ts` beside `loadConsoleScreens()`
   (`:1011-1085`). Rows, in the brief's order: the console's own three screenshots for the
   leading slides, then the Watch slide last.
4. **The Watch slide.** `fugaro-watch.png` moves from the plain `<img>` in
   `HomeSlider.astro:298-302` to a row of the new file, keeping its alt text and its
   `.watch-note` caption. The `[data-slot="watch-screenshot"]` slot stays where it is, in the
   "See it" panel, and the slider renders into it.

**Files.** `website/public/scripts/site.js`; `website/src/components/ScreenshotSlider.astro`;
`website/src/components/HomeSlider.astro`; `website/src/components/` (a new
`HomeScreens.astro` if the panel's heading, id prefix and captions warrant one);
`website/src/content.ts`; `website/data/home-screens.yaml`;
`website/src/pages/console.astro` (the prop); `website/test/{slider,home,console-screens,budget,a11y}.mjs`;
`website/README.md` (the JS figure and the slider paragraph).

**Tests.** Hermetic, and each with a plant:

- `home.mjs`'s three pinned assertions (ZF47) are **replaced by stronger ones**, not deleted:
  the See-it slot holds exactly the rows `home-screens.yaml` names, in order, each with its
  own `src`, `alt`, `width`, `height` and caption; the page's `<img>` set is the rows plus
  `/logo.png`; the slot is still immediately after the panel's `<h2>` and still inside
  `#home-panel-see`. Plants: a row dropped, a row reordered, a caption swapped, the Watch row
  moved off the end.
- `console-screens.mjs`'s `sliderFailures` and `scriptFailures` run unchanged against the
  console page, proving the hook rename did not move the console's behaviour.
- `slider.mjs` still sees exactly three tabs and three panels, and `site.js` still names no
  home-slider id (its `:447-460` case).
- `budget.mjs`'s per-page JS figure is re-checked; the README's paragraph says what the bytes
  are for.
- A no-JS case: the built page with the script's effects removed shows all rows and the dot
  anchors resolve (`linkcheck.mjs` already proves the anchors; the case proves the rows are
  all present without script, as `console-screens.mjs:643` does for the console).

**Verify.** `cd website && npm run check`. The part is done when the fourteen gates pass on
the built artifact, and the pull-request body states the `site.js` byte delta and the page's
new weight.

**The owner's visual check.** That the See-it tab, opened on a phone and on a desktop, shows
the console first and the Watch last, that the captions read right, and that the Watch slide's
terminal text is legible where it is drawn (ZU4).

**Independently mergeable because** it changes one component, one data file and one script's
selector names; no route is added, no nav changes and no page text changes.

**Depends on** D250 (#842 must have merged, and `HomeSlider.astro` re-read), D246 (the hooks),
D251 (the owner's slides exist), D252 (the Watch slide stays).

### Part 2 — the "Why GCP" page, the FAQ entry and the nav (ZF15–ZF22, ZF52–ZF59; D238, D243, D248, D249)

**What.**

1. **The data file.** `website/data/why-gcp.yaml`: one row per claim, each with `text`,
   `source` (a repository path) and, where the claim is about an external fact, `date`. The
   rows are the four numbered points of the brief's "Why GCP" **as the tree supports them**
   (reconciliation §4), plus the AWS paragraph. The two claims the tree refuses — "free and
   disposable" and "never runs Terraform" — are **rewritten**, not dropped: a managed project
   is new and holds only Fugaro's resources (ZF16, ZF17).
2. **The page.** `website/src/pages/why-gcp.astro`, a reader for the file in the shape
   `faq.astro`'s `loadFaq` uses (`:257-306`: unknown key refuses, empty field refuses, a
   `source` that does not exist on disk refuses), the lead ≤ 40 words, and the four points as
   an `<ol>` with each point's source rendered as a link to the file at the branch `ci.yml`
   pushes to.
3. **The FAQ entry.** A seventh entry in `website/data/faq.yaml`, `id: do-i-need-gcp`,
   `question: "Do I need to be on GCP?"`, an answer that is **plain text** (ZF56) and
   `more_href: /why-gcp/`, `more_label: Why GCP`. The existing `where-it-runs` entry keeps its
   own answer and gains the same link, so the two do not say the same thing twice.
4. **The nav.** `{ href: '/why-gcp/', label: 'Why GCP' }` as the fourth Product item, after
   "How it works" (D238), and D182's sentence in `Base.astro:126-131` amended in the same
   commit — the layout comment and `base-styles.mjs:2636`'s failure message both say "three
   groups of three".
5. **The framing pin.** A `wording.mjs`-style pair (D249): a banned list and a required
   sentence, each with a planted defect.

**Files.** `website/src/pages/why-gcp.astro`; `website/data/why-gcp.yaml`;
`website/data/faq.yaml`; `website/src/layouts/Base.astro`; `website/test/wording.mjs`;
`website/test/faq-community.mjs`; `website/test/linkcheck.mjs` (only if a new allowed host is
needed — it should not be); `website/README.md` (the route count and the whole-site figure);
`website/test/budget.mjs` (its derivation comment names the pages); `docs/website/redesign/README.md`
and `docs/website/redesign/capture-screenshots.mjs` (the "eleven routes" prose ZF57 lists as
present tense).

**What this part does *not* edit, and why.** ZF57's list has two kinds of entry and only one
of them moves. The **dated** ones — `docs/website/README.md:3`,
`docs/website/reconciliation.md:3`, `docs/website/implementation-plan.md:3` and
`docs/website/redesign/reconciliation.md:19-21,649,653` — are sentences about the day the site
landed (2026-10-16/17/18) or about the tree at a named commit, and a twelfth route does not
falsify a sentence about that day or that commit. This line of work's own three documents are
the same case twice over: each is anchored to `3021f4b3`. The **present-tense** ones move with
the page, and they are in the Files list above. A round that edits a dated sentence to keep a
count current has changed a record of the past to say something that was not true of it.

**Tests.** Hermetic, mutation-proven:

- The page renders exactly the rows the file names, in order, each with a source link that
  resolves to a file that exists — the `faq-community.mjs:741-760` pattern, on a new page.
- The framing pin: the built page must contain the required sentence and must contain no
  banned phrase. Plants: the required sentence removed; `no real alternative` inserted;
  `the only cloud` inserted.
- The FAQ: seven entries, unique ids, the new entry's `more_href` resolving, and the page's
  `<article>` count and source links.
- The nav: `base-styles.mjs`'s group count (still 3), the one `aria-current="page"` rule on
  the new route, and `linkcheck.mjs` on the new page's links.
- `budget.mjs`: lead and word ceilings on the new page, and the README's whole-site figure
  re-stated (or the drift recorded, per constraint 2).

**Verify.** `cd website && npm run check`, and — because a new route touches the Go side's
docs pins — `GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'Docs|Lint|Website|CliRef' -count=1 -timeout 40m`.

**The owner's visual check.** That the page reads as the brief's argument and not as a
rebuttal of it; that the AWS paragraph lands; and that "one integrated project" is the
sentence the owner wants in place of "disposable" (D248).

**Independently mergeable because** it adds one route, one data file, one FAQ entry and one
nav item, and changes no component and no script.

**Depends on** D238 (the nav), D243 (the data file), D248 (the wording), D249 (the pin).

### Part 3 — the mobile app page (ZF23–ZF31; D239, D240, D241, D242)

**What.** `website/src/pages/mobile.astro`, a sister page to `/console/`, with:

1. **The badge.** A "Coming soon" element in the page's lead block, and **no date anywhere on
   the page** — held by a test that refuses a four-digit year and a month name in the page's
   text, with a plant (D240).
2. **The claims, each marked.** Six rows in `website/data/mobile-claims.yaml`, each with
   `text` and `state: shipped | planned`. What is shipped is status and spend (ZF26); what is
   planned is the roster, the wake word's engine, project/agent switching by name, QR pairing
   and the three roles (ZF23, ZF24, ZF25, ZF27). The page renders the two states differently
   and says which is which, so nothing on it is a false present tense.
3. **The voice sentence.** On-device, with no cloud speech service named (D242).
4. **The screenshots.** A slot in the shape Part 1's component uses, rendering **no** slide
   until `website/data/mobile-screens.yaml` has a row (ZF49; the console's empty-list case is
   the precedent, `console-screens.mjs:613-637`). The owner supplies the files (D251).
5. **The waitlist.** Per D239: if the answer is "no waitlist", the page carries the badge and
   the copy and nothing else, and the privacy page is untouched. If the answer is one of the
   other three doors, this task is a part of its own — it changes `firebase.json`, the origins
   gate's CSP contract, the privacy page and its `contactFailures`, and it amends Check 69b
   (ZU5) — and it does not ride in on this page's commit.
6. **The nav.** `{ href: '/mobile/', label: 'Mobile' }` in the **Reference** group, beside
   "Console", or in Product — the brief says "sister page to the console page", which reads
   as Reference.
7. **The live check** (D247): **113**, written and **not run** in this part — the owner opens
   the built page and the live page and confirms the badge is there, no release date is, and
   every claim is either shipped today or marked as planned. This check belongs to this part
   and not to Part 5, which may be cancelled by its own spike; a part that can ship alone
   carries its own check.

**Files.** `website/src/pages/mobile.astro`; `website/data/mobile-claims.yaml`;
`website/data/mobile-screens.yaml` (empty rows); `website/src/layouts/Base.astro`;
`website/test/mobile.mjs` (new); `website/README.md` (the route count and figures);
`docs/gcp-live-checklist.md` (Check 113).

**Tests.** Hermetic, mutation-proven:

- Every claim row renders, and a row whose `state` is `planned` is rendered inside the
  element the page reserves for planned claims — plants: a planned row rendered as shipped,
  a shipped row rendered as planned.
- No date: a four-digit year and each month name are refused in the page's visible text —
  plants: "in 2027", "March".
- The roster sentence is absent while `agents/**` is denied to the phone — a plant that
  inserts "live agent roster" into the data file, which must fail with a message naming
  `mobile/src/feed/paths.js` (ZF23). *This is the one test that pins a product fact to a
  repository file rather than to a document, and it is deliberate.*
- The page's word ceiling, lead ceiling and links, through the gates that already iterate
  every built page.

**Verify.** `cd website && npm run check` and the Go docs run above.

**The owner's visual check.** That the badge does not read as an apology, that the page looks
like the console page's sister, and that the planned/shipped marking is visible without
reading every line. The same three questions on the **live** page are Check 113.

**Independently mergeable because** it adds one route and two data files and changes no
component, no script and no existing page.

**Depends on** D240 (may the page exist), D241 (what it may say), D242 (the voice sentence),
D239 (the waitlist).

### Part 4 — the four levels as stills, with the same shapes (ZF36–ZF43; D237, D244)

**What.** One component, `website/src/components/ZoomLevels.astro`, rendering the four levels
as **inline SVG**, in order, all visible with no script.

1. **The symbols.** One `<defs>` block holding one `<symbol>` per entity — the job tile, the
   orchestrator, the developer, the console, the cloud, the project boundary — each drawn
   once, in the palette's own tokens, with a `viewBox` of its own. Every level draws entities
   **only** through `<use href="#…">` (D244).
2. **The levels.** Four `<figure>`s, each with a `<figcaption>` naming the level, and each
   level's labels written as data rows in `website/data/zoom-levels.yaml` so the drawing and
   its words cannot drift. The labels are the brief's, **corrected** to what the runner does
   (reconciliation §3): Level 1 says the review is the coder's own model and that the senior
   review is a loop; the caption says which recipe the drawing is (ZF7).
3. **The placement.** On `/how-it-works/`, below "The run, end to end" and its existing
   `RunDiagram.astro` (D237), inside a `<section>` with its own `<h2>`.
4. **The accessibility.** One `<nav>` of level anchors with accessible names (D245), a
   `<figcaption>` per level, and `role="img"` with an `aria-label` only where a level is a
   single picture; where a level is a list of labelled parts, the parts carry the text. The
   anchors are `#level-1` … `#level-4` (D245's ids, and Part 5 reuses them rather than adding
   a second set); each `<figure>` carries the matching `id`.

**Files.** `website/src/components/ZoomLevels.astro`; `website/data/zoom-levels.yaml`;
`website/src/pages/how-it-works.astro`; `website/test/how-it-works.mjs` (the paint gate's
extensions, `:1110`); `website/test/a11y.mjs`; `website/README.md` (figures).

**Tests.** Hermetic, mutation-proven:

- **The same-shape rule (D244).** Every `<use>` in every level resolves to a `<symbol>` in
  the one `<defs>`; every `<symbol>` is used by at least one level; and no level contains a
  shape that draws an entity's geometry itself. Plants: a level with its own `<rect>` where a
  tile should be a `<use>`; a symbol no level uses; a `<use>` pointing at an id that does not
  exist.
- **The paint gate** (`how-it-works.mjs:1110` `runPaintFailures`) extended to the new
  drawing's element kinds — a new kind refuses until the reading is taught it, which is the
  gate's own rule (ZF42).
- **The labels**: every label is a row of the data file, and every row renders — the
  `how-it-works.mjs:617-625` pattern (`runDiagramFailures`'s `held`), where a label attributed
  to a document that does not carry that line is refused by name.
- **The no-JS case**: all four levels' text is in the built HTML, in order, with the script
  never run.
- **The a11y gates**: one `<h1>` on the page still, named landmarks, no positive `tabindex`,
  contrast from the palette, and the markers keyboard-reachable in document order.

**Verify.** `cd website && npm run check`, plus the Go docs run.

**The owner's visual check.** That the four levels are legible at 360 px (ZU4); that the
shapes really do read as the same shapes across the four; and that the sequence tells the
story the brief wants without captions.

**Independently mergeable because** it adds one component, one data file and one section to a
page that exists. It is the brief's "still" half, complete on its own.

**Depends on** D237 (where it lives), D244 (the symbol rule), D245 (the markers — this part
builds them). It does **not** depend on Part 5.

### Part 5 — the pinned, scroll-driven section (ZU1; D245, D247)

**What.** Part 4's four stills become the brief's pinned sequence. **This part begins with a
spike, and the spike may cancel the part.**

1. **The spike (ZU1).** A throwaway page, not committed, that pins a container with
   `position: sticky` and drives the zoom with a CSS scroll-driven animation
   (`animation-timeline: view()`), under the site's own reduced-motion rule
   (`Base.astro:236-246`: `animation-duration: 1ms !important`, `animation-iteration-count: 1 !important`
   on `*`). The question is whether the animation is neutralised, frozen on its first frame,
   or unaffected. **Answer recorded in this folder's README, and if the answer is "frozen" or
   "broken", the part stops here and the brief's "animated" half is reported as not
   achievable under the site's own accessibility rule** — the stills of Part 4 are already
   shipped and are the fallback the brief itself names.
2. **The section**, if the spike passes: one sticky container, four level panels, each
   scaled and faded by the scroll timeline, all four present in the markup in order and all
   four fully readable when the timeline is not running (the no-JS case is Part 4's).
3. **The markers.** Part 4's `#level-1` … `#level-4` anchors (D245) **moved**, not rebuilt:
   the same `<nav>` and the same ids, placed beside the pinned container on a wide screen and
   above it on a phone. A second set of anchors would be a second set of ids, and the
   linkcheck gate resolves both to the same `<figure>`s.
4. **The reduced-motion path.** A `@media (prefers-reduced-motion: reduce)` rule on the
   section that shows the four levels as stills — the same markup, no timeline — so the
   answer to the spike is not load-bearing for the reader who asks for less motion.
5. **The live check** (D247): **112**, written and not run — the owner opens the built page
   and the live page, scrolls it, jumps by a marker, and sets the system's reduced-motion
   preference.

**Files.** `website/src/components/ZoomLevels.astro` (the pinned layout);
`website/src/styles/` (one new stylesheet, or the component's scoped styles); `website/test/how-it-works.mjs`;
`website/test/a11y.mjs` (the motion reading); `website/test/fold.mjs` (unchanged — the section
is below the lead, ZF38); `docs/gcp-live-checklist.md` (Check 112); `docs/website/zoom-diagrams/README.md`
(the spike's answer).

**Tests.** Hermetic, mutation-proven:

- The four panels are in the built HTML in order with the timeline absent — the no-JS case,
  re-run because this part changes the markup.
- Every `animation`/`transition` the section declares is either inside a
  `prefers-reduced-motion: no-preference` block or has a matching `reduce` rule —
  `a11y.mjs`'s `motionFailures` extended if the section needs a property it does not read
  today, with a plant for each new property.
- The markers: each resolves to an id, each has an accessible name, they are in document
  order, and there is exactly one per level — plants: a marker with no target, a marker
  without a name, two markers for one level.
- `budget.mjs`: the page's weight and words after the section lands.

**Verify.** `cd website && npm run check`, the Go docs run, and — the one thing no agent can
do — the owner's browser.

**The owner's visual check.** The whole brief's "Format" bullet: that each level rests as a
still, that the zoom between them is smooth, that the markers let a reader jump, and that the
section is legible on a phone.

**Independently mergeable because** it changes one component and one stylesheet and adds one
live check; Part 4's stills remain correct whether this part lands or not.

**Depends on** Part 4, the spike's answer, D245 (the markers), D247 (the check number).

## 4. The live checks

The register's rule is that a reservation is not a fact about the tree
(`docs/gcp-live-checklist.md:195`), and the highest number written today is **111**
(`:5781`). This plan proposes:

| # | What it checks | Who runs it |
|---|---|---|
| **112** | The pinned zoom section: the four levels rest as stills, the zoom between them is smooth, a marker jumps to its level, and the system's reduced-motion preference turns the section into stills | The owner, in a browser, against the built artifact and the live site. **No agent runs any step of it** |
| **113** | The mobile page promises no date: the live page carries the badge, no release date, and every claim it makes is either shipped today or marked as planned | The owner, in a browser |

Both are **proposed, not taken**. If the owner reallocates, the plan follows.

## 5. What moves, in one table

| Artifact | Part 1 | Part 2 | Part 3 | Part 4 | Part 5 |
|---|---|---|---|---|---|
| `site.js` | selector hooks, maybe bytes | — | — | — | — |
| `website/src/components/` | `ScreenshotSlider` shared, `HomeSlider` | — | — | `ZoomLevels` | `ZoomLevels` |
| `website/data/` | `home-screens.yaml` | `why-gcp.yaml`, `faq.yaml` | `mobile-claims.yaml`, `mobile-screens.yaml` | `zoom-levels.yaml` | — |
| `website/src/pages/` | — | `why-gcp.astro` | `mobile.astro` | `how-it-works.astro` | — |
| Routes | 11 | 12 | 13 | 13 | 13 |
| Nav groups | 3 × 3 | 3 × 4 | 3 × 4 or 5 | — | — |
| `website/README.md` figures | JS, page, site | site, route count | site, route count | page, site | page, site |
| `website/test/budget.mjs` derivation comment | — | the page list | — | — | — |
| `docs/website/redesign/` route prose | — | `README.md`, `capture-screenshots.mjs` | — | — | — |
| `docs/gcp-live-checklist.md` | — | — | Check 113 | — | Check 112 |
| Live checks | — | — | **113** | — | **112** |

## 6. Order and the owner's go

**The order is the answer order.** Part 1 waits on D250, which waits on a pull request this
checkout cannot see (ZF51); Part 2 waits on four decisions that are all copy and nav
questions; Part 3 waits on the waitlist question, which is the one with a privacy cost; Parts
4 and 5 wait on D237 and D244 and are otherwise the least blocked.

If the owner wants one thing first and nothing else answered: **Part 4** needs only D237 and
D244, ships the brief's "still" half, and is the prerequisite of Part 5.

**An answered decision is not an approved plan.** Each part starts on the owner's go for that
part, and no part waits on a decision another part needs.

## 7. What this plan does not do

- It does not write the pages' copy. It writes where the copy comes from and what holds it.
- It does not touch `.github/workflows/`, `docs/pending-workflows/` or any deploy step. The
  site's deploy is `preview-promote.md`'s, unchanged.
- It does not reopen D90–D109 or D180–D192. Where the brief conflicts with them, the
  reconciliation records the conflict and §12 of it asks.
- It does not add a dependency, an Astro integration, a sitemap or a new script file.
- It does not change the privacy page except under one of D239's non-recommended answers, and
  then in a part of its own.
- It does not promise a date for the phone app, and Part 3's tests refuse one.

## 8. Decision numbering

The plan's decisions are the reconciliation's `D237`–`D252` (§12 of that document). The
highest written decision under `docs/` is **D236**; the gap `D198`–`D200` is inside the
coordinator's note and `D200` may be held; the convention is to take the first block above
the highest allocation rather than to derive one (`docs/website/redesign/implementation-plan.md:556-569`).
**The allocation is the owner's**, and this line of work renumbers itself if the owner takes
the block.
