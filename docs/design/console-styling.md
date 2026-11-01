# The console's style: the theme, the layout, and the wiring

**Status: design only; APPROVED by the owner on 2026-10-19, and Phase 6 is being built to
it.** *The status as first written, kept:* **Status: design only; nothing is implemented.**
Proposed 2026-10-19, at the owner's request ("a console styling task is needed … draft a
styling design before anyone builds it"). Owner: **@Dimi**. Every claim below was read on
`origin/main` at `82ba600` and names the file and the symbol it was read from. Nothing here
starts until the owner approves it: *no `.css` file, no Go, no TypeScript, no change to
`console/`, and no change to `SERVED_FILES` follows this document on its own* — which is
why this document is the design and the build plan it produces,
[§7](#7-the-build-plan-phase-6), is the work. **The owner approved both on 2026-10-19**, so
that go is given: §7's tasks are **APPROVED by the owner on 2026-10-19**, **Task 6.1** (the
token layer) is the first of them, and
[the desktop-console plan](../plans/2026-10-15-desktop-console.md) carries the same approval
in its phase heading, its Phase-summary row and its PR rows. **One of the six questions §8
put to the owner is answered in the ruling itself and is binding on the tasks below**:
**Q1 is option (b)** — one value set, dark; the light set does not ship, and §3.3's table is
kept as the record of it.

Related: [desktop-console.md](desktop-console.md) (§5.1 the hard rules, §6.1 the six
transfers from tmog.org, §6.2 the four screens, §6.3 how it degrades),
[desktop-console-v2.md](desktop-console-v2.md),
[the console's plan](../plans/2026-10-15-desktop-console.md), [console/README.md](../../console/README.md),
[console/docs/json-contract.md](../../console/docs/json-contract.md),
[the website's tokens](../../website/src/styles/tokens.css),
[the website redesign's reconciliation](../website/redesign/reconciliation.md) §5.

---

## 1. The problem, and the evidence

### 1.1 The console ships no stylesheet at all

This is not "the styling is unfinished". There is no stylesheet, and no mechanism that
could carry one:

| Claim | Read from |
|---|---|
| `console/src/renderer/index.html` is script-only: its `<head>` holds one `<script src="./console.js" defer>`, beside `<meta charset="utf-8">`, the CSP `<meta http-equiv>` copy and `<title>`, and holds **no stylesheet at all** — there is no `<link rel="stylesheet">` and no `<style>` element. | the file, 61 lines, read whole |
| The body of `index.html` is **empty on purpose**, and `console/test/unit/csp-document.test.ts` holds it empty (`expect(bodyMarkup(served('index.html'))).toBe('')`). | the file; the test |
| `console/src/renderer/installations.html` and `console/src/renderer/refusal.html` carry the same CSP `<meta>` and no stylesheet either; `refusal.html` is script-free as well (`expect(scriptElements(served('refusal.html'))).toEqual([])`). | the two documents; `csp-document.test.ts` |
| There is **no `.css` file anywhere under `console/`**: `find console -name '*.css'` returns nothing. | the tree |
| `SERVED_FILES` in `console/src/main/protocol.ts` lists five paths — `/console.js`, `/index.html`, `/installations.html`, `/installations.js`, `/refusal.html` — and `console/test/unit/bundle.test.ts` holds that list equal to the contents of `dist/bundle/` in both directions: `expect(SERVED_FILES.length, …).toBe(5)` and `expect(builtListing('bundle')).toEqual(servedNames())`. | `protocol.ts`; the test |
| `console/tools/bundle-renderer/index.ts` copies **`.html` only** (`names.filter((name) => name.endsWith('.html')).sort()`) and writes the two scripts. Nothing in it reads, resolves, concatenates or emits CSS. | the tool |
| `mediaTypeFor` in `protocol.ts` already maps `'.css': 'text/css; charset=utf-8'`, so the handler could serve a stylesheet the day one exists — the type is the only piece that is ready. | `protocol.ts` |

The screens are the other half of the same fact. Every one of them emits **class hooks and
`data-*` readings and nothing that paints**: in all four — and in the degraded screens with
them — a class is written at exactly one line, `element.setAttribute('class',
classes.join(' '))`, inside that file's **`make`** helper (`live.ts` 804, `spend.ts` 1317,
`projects.ts` 879, `run.ts` 708), and the `cell` helper beside it only calls `make`; each of
the four sets `data-theme` on its root and nothing else theme-shaped. The headers of those
files say so in their own words — `console/src/renderer/screens/live.ts`: "no stylesheet
and no colour"; `console/src/renderer/app.ts`'s `ElementLike` is deliberately narrower than
`HTMLElement` with "no style and no event handler", and `entry.ts`'s header repeats it:
"the element it wraps exposes a text slot and attributes — no markup parsing, no
navigating attribute, no style and no event handler". Nothing anywhere under
`console/src/renderer/` sets a `style` attribute, because the CSP admits no inline style
anyway (below).

### 1.2 The CSP is the reason, and it is not the obstacle

`CONTENT_SECURITY_POLICY` in `console/src/main/protocol.ts` is design §5.1 rule 3,
directive by directive:

```
default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:;
font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none';
frame-ancestors 'none'; object-src 'none'
```

**`style-src 'self'` is already the directive a stylesheet needs, and it is not being
used.** `'self'` permits a stylesheet served from `app://console` — the console's own
bundle — and the policy has no `'unsafe-inline'`, so a `<style>` element and a `style="…"`
attribute are both refused by the browser whatever the code does. The `<meta
http-equiv>` copy in each of the four documents is byte-identical to the header:
`console/test/unit/csp-document.test.ts` derives the document list from `SERVED_FILES`, so it
holds **all four** copies equal to the header and to each other, and
`console/test/unit/window.test.ts` holds the same for the two documents it reads
(`index.html` and `refusal.html`) alongside the assertion that the policy contains neither
`unsafe-inline`, `unsafe-eval` nor `*`.

So this design **adds nothing to the CSP and removes nothing from it**. A stylesheet is
the one thing `style-src 'self'` was written for and the console has never had.

### 1.3 Why this matters now: the golden frames are pictures of unstyled markup

The four screens take golden screenshots — `live.spec.ts`, `spend.spec.ts`,
`projects.spec.ts` and `run.spec.ts` each call `toHaveScreenshot` twice, once per theme —
so the suite names **eight baselines per platform** (`{light,dark}-<platform>.png`, and
`playwright.config.ts` sets `snapshotPathTemplate: '{testDir}/__screenshots__/{arg}-{platform}{ext}'`
with `updateSnapshots: 'none'`, so a missing baseline fails rather than being written).
On this branch **none of them is committed** (`console/test/e2e/__screenshots__` does not
exist) and the golden tests fail on purpose until an owner records them; the darwin set is
the subject of the draft PR #629.

A golden frame is a picture of the mount with no stylesheet in it. The two frames of one
screen are therefore **byte-identical pictures of the same unstyled markup**: dark and
light differ only in a `data-theme` attribute no rule reads. The baselines that PR #629
records are, and can only be, a record of the unstyled tree — which is exactly the artefact
§6.1 of the design exists to avoid ("density as the default, not a setting", "~24 px rows
with tabular figures … right-aligned numerics"). Recording them before the stylesheet lands
buys nothing and costs an owner-run recording pass; §7 orders the work so the re-recording
is one pass, once ([§6.1](#61-the-golden-frames)).

### 1.4 What is decided, and what is not

The design already decided the *shape* of the console's look, and this document does not
reopen it:

- **§6.1 rule 2** — density as the default: a compact row (~24 px), tabular figures,
  right-aligned numerics, no whitespace padding between related figures;
- **§6.1 rule 3** — "live graphics that behave like instruments … a cap bar that changes
  colour only at a defined threshold (tmog.org's amber/green discipline), never
  decoratively";
- **§6.1 rule 5** — "**Themes as a phosphor set, 'Instrument, not ornament'** … a light, a
  dark, and a small set of low-saturation palettes, **with all colour choices from a single
  token file** — which is also what makes the screens readable for colour-blind users";
- **§6.1 rule 1** — every reading carries one of the five availability states, so a
  missing value is `n/a` and never `0`.

What was never decided, and is what this document decides or puts to the owner:

1. the token file's contents — names and values, **as shipped the dark set alone** (the
   owner's Q1 ruling of 2026-10-19, [§8](#8-open-for-the-owner)); *as first written,
   "names, values, light and dark", and §3.3 is kept as the record of the light set that
   ruling deferred;*
2. how a theme is *chosen* (today `CONSOLE_THEME` in `entry.ts` is the literal `'dark'`,
   and the file's own header says "no task's Files name the code that would read the
   operating system's preference");
3. the layout, the focus rules and the forced-colors behaviour;
4. **the wiring: the stylesheet does not exist, is not bundled, is not served, and no
   document links one.** At the ref these claims were read, the plan's File Structure tree
   named `src/renderer/theme/tokens.css` with **no task behind it** — a file the tree
   promised and the tasks never created. §7's Phase 6 is what changes that:
   `docs/plans/2026-10-15-desktop-console.md:120` (the tree as this change leaves it — the
   same line is `:118` at the ref declared above, alone and unannotated) now carries all four
   `src/renderer/theme/*.css` lines, each annotated with the task that writes it, and
   Task 6.1's **Scope** names `tokens.css` and `palette.css`;
5. the tests that keep all of it honest.

---

## 2. Screen inventory

Every class hook and every `data-*` attribute below was read from the code. The inventory
is the contract the stylesheet is written against: **a rule may only select a hook this
table names**, and a task that adds a screen, a state or a reading extends this table in
the same PR.

**The tables are the hooks a rule needs, not every attribute the screens emit.** Two things
follow, and both are the same rule read twice. A hook that is emitted and not listed here is
**not** a licence to write a rule against it: Task 6.4's inventory test fails a selector for
a hook §2 does not name, and §2 is the table it fails against. And a hook a rule genuinely
wants — one the screens emit and this table has dropped — is added **here first**, in the PR
that wants it, so the test's list and the stylesheet's selectors move together. The
`data-*` attributes the screens emit beyond these are real — reading the renderer's
`setAttribute` calls against this table leaves a short list, and the entries above are
deliberately the subset a rule is written against rather than a second copy of the code.
**`data-hidden`** (every view the mode withholds) and **`data-skew`** (the compatibility
handshake), both stamped by `console/src/shared/version.ts`, are the two the inventory test
refuses by name; the graph screen's eight unlisted attributes are §2.11's own, and six of
those the test refuses by name too. They are the examples the rule holds for: they are
emitted, no rule needs them, and a rule that suddenly wants one adds it to this table first.
The test's licence is by attribute **name**, not by name and screen, so the two of §2.11's
eight that are hooks here as well — `data-key` for the spend screen (§2.3, §2.4) and
`data-view` for the navigation and the people panel (§2.1, §2.5) — stay licensed, and §2.11
says so.

Four facts hold across every screen and are stated once:

- **A number is never bare.** Every figure goes through `putReading` (`app.ts`), which
  calls `formatReading` (`console/src/shared/availability.ts`), so the marks are fixed console-wide:
  `~$` prefix, `est.` prefix, `n/a` (`UNAVAILABLE_MARK`), `—` (`GAP_MARK`), and a
  `≥` (`LOWER_BOUND_MARK`) in front of a sum that is a lower bound. An element carrying a
  reading with no value also carries its **reason in `title`** — the console's own
  sentence, never the value it refused.
- **A refused string is `n/a` with a `title`.** `putText` draws the console's own `n/a`
  for a value the sanitizer refused, `putLink` draws the reason instead of a URL, and the
  URL goes to **`data-href`** and never to the anchor's navigating attribute.
- **The theme travels as an attribute.** Each screen's root is
  `<section class="<screen>" data-theme="light|dark">`; nothing else about the drawing
  depends on the theme today.
- **A panel whose data has not arrived draws the shape of what is coming** (the owner,
  2026-10-31: "when changing tabs in the Console we should show a skeleton while we load the
  data, no?"). A tab switch draws one `div.console-skeleton` per section the tab is made of
  — a `div.console-skeleton-block` per thing that is coming, each carrying `data-shape`
  (`line`, `chip`, `row`, `card` or `band`) and the `data-section` its own wrapper names —
  and a section whose own read is in flight draws one too, in place of the content it does
  not have yet. The whole tree is `aria-hidden`, because a block is a shape and not a
  reading, and what a reader who cannot see it is told is the panel's own sentence in
  `span.console-reading-status` — visually hidden beside the blocks, with the fifteen-second
  `p.console-busy-still` under them. The shapes are `console/src/renderer/skeleton.ts`'s
  (`SKELETON_SHAPES`, one list per tab), the placeholder that draws them is
  `screens/reading.ts`'s and the per-section one is `renderer/section.ts`'s, so the hooks
  above are the console's rather than one screen's — and the heights the blocks hold are
  `--skeleton-*` steps of `flight-deck.css`, so the section does not move when the read
  lands.

### 2.1 The dashboard's chrome — `src/renderer/entry.ts`

**The header switcher replaced the installation rail** (owner's ruling of 2026-10-28, console
redesign step 1). The entry draws the body's two children, in this order, all from
`domSurface`'s mount: `header.console-header` and `main.console-page` — asserted by
`test/e2e/entry.spec.ts`:
`expect(shape.bodyChildren).toEqual(['console-header', 'console-page'])`
in an `ok` run. The header holds the wordmark, the installation switcher, the navigation, the
fleet gauge and Halt fleet; the switcher's menu holds the installation line and the rail's tree
(§4.1), and the page holds the panel. **A third child precedes these two whenever the mode is
not `ok`**: `startConsole` appends the version-skew notice to the same mount first (§2.8,
§2.14, §4.1), and in **refused** mode it is the whole document.

| Region | Element and hooks | What it holds |
|---|---|---|
| Header | `header.console-header` → `button.console-wordmark` (an `svg` and `span.console-wordmark-name`), `div.console-switcher`, `nav.console-views`, `div.console-gauge`, `button.console-halt` | the redesign's one header row on `--bg-header`, wrapping on a narrow window. The wordmark goes to Live |
| Installation switcher | `div.console-switcher` → `button.console-switcher-button` (`span.console-switcher-dot` with `data-state` and `role="img"`, `span.console-switcher-name`, `span.console-switcher-version`; `aria-haspopup`, `aria-expanded`, `aria-controls`), `div.console-switcher-menu` (`hidden` while folded) → `section.console-installation`, `aside.console-rail` | the installation the console reads, its CLI version and mode, and the status dot carrying the open row's rail word. The menu holds the installation line (the CLI's full path) and the rail's tree; Escape and a click outside fold it |
| Fleet gauge | `div.console-gauge` (`role="group"`) → `span.console-gauge-label`, `span.console-gauge-notional`, `svg.console-gauge-bar` (`rect.console-gauge-track`, `rect.console-gauge-fill`), `span.console-gauge-cap` | GLOBAL, today's notional spend, the counted dollars against the global daily cap as the fill's `width` attribute (no notional segment: notional counts toward no cap), and the cap. `n/a` until a read lands, and when the mode withholds caps |
| Halt fleet | `button.console-halt` with `aria-disabled="true"` | drawn as designed and not performed (`src/renderer/writes.ts`): no listener, no IPC, no spawn entry. The console's one approved write is launch-from-console |
| Installation rail | `aside.console-rail` → `div.console-rail-head` (`p.console-rail-title`), `div.console-rail-list` → one `button.console-rail-row` per installation it draws (`div.console-rail-nameline` → `span.console-rail-name` and, for an installation with an issue, `span.console-rail-warning` with `role="img"` and an `aria-label`; `span.console-rail-project`, `span.console-rail-state` with `data-state`, `span.console-rail-spend`), `p.console-rail-empty` (the line drawn, unhidden, when the rail has no row), `div.console-rail-add` → `button.console-rail-add`, `p.console-rail-hint`; the ARIA is `role="tablist"` on the list, `role="tab"` and `aria-selected` on its rows | the rail's tree, drawn inside the switcher's menu (§4.1): the header row, one tab per row, **one row per installation the CLI listed or this console holds, readable or not** (§4.1, owner's design ruling of 2026-10-25), the add row and the hint. **Fourteen hooks on fifteen elements** — `console-rail-add` names the foot row and its button both — and a rule selects no other. `data-state` on the state span is the word it already draws, repeated as the tone's hook |
| Installation line | `section.console-installation`, `data-installation`, `data-mode` | `data-installation="chosen"` with `p.installation-project`, `p.installation-binary`, `p.installation-version` and `data-mode`; `data-installation="none"` with `p.installation-none`; `data-installation="unreadable"` with `p.installation-unreadable`; and `p.installation-store-refusal` |
| Navigation | `nav.console-views` → one `button.console-view` per view | `type="button"`, at least 44 px tall (the redesign draws links; the renderer writes no `href`, so they stay buttons), `data-view="live\|graph\|spend\|projects\|coordinator"`, `aria-current="page"` on the view on screen and on no other, label as text |
| Page | `main.console-page` → `div.console-panel` | the page scrolls and the header does not; the panel is the page container (`max-width: var(--page-max)`, centred, padding 28 px 32 px 64 px) and holds the mounted screen's root, the installation issue view, or the onboarding view |
| Onboarding | `section.installation-onboarding` → `h1.installation-onboarding-title`, `p.installation-onboarding-body`, `p.installation-onboarding-reason` (the CLI resolution's own sentence, only when no CLI was found), `code.installation-onboarding-command` | drawn by `drawOnboarding` into the panel when the machine has no installation at all: the picker's own words for the two onboarding screens (`ONBOARDING_COPY`) and the command the main process names (`src/main/onboarding.ts`). Nothing is read |
| Installation issue | `section.installation-issue` → `h1.installation-issue-headline`, `p.installation-issue-sentence`, `code.installation-issue-command`, `details.installation-issue-details` (`summary.installation-issue-summary`, `pre.installation-issue-lines`), `div.installation-issue-actions` (`button.installation-issue-choose`, `button.installation-issue-remove`, `button.installation-issue-reveal`, `p.installation-issue-checkout-lead`, `select.installation-checkout-select`, `button.installation-issue-checkout`) | drawn by `drawIssue` into the panel when a rail row with an issue is clicked, or at boot when no installation can be read (§4.1): the installation's name, **one plain sentence first** with the exact command, the CLI's own text behind the `details` disclosure, and the row's actions. No banner. The `data-issue` it carries is the main process's classification and no rule selects it. **`select.installation-checkout-select` is the one hook here drawn in two places** (`renderCheckoutSelect`, `src/renderer/checkout.ts`): this row's actions and the Graph's no-checkout card, which offers the same list of the installation's re-validated, enrolled checkouts. It is one rule in `screens.css` and not two, and `button.installation-issue-checkout` beside it is the button that sends what the menu has selected |
| Reduced/refused notice | `section.notice` with `console-skew`, drawn by `renderVersionSkew` through `renderNotice` into the mount, **first of all** — before the header and the page: `data-severity`, `span.notice-mark`, `div.notice-body` → `h1\|h2.notice-title`, `p.notice-reason`, `button.notice-toggle`, `div.notice-details` → `dl.notice-facts` → `dt`/`dd`, and `button.notice-dismiss` when it can be put away | see §2.8 and §2.14 |
| Reduced-mode chip | `button.console-reduced-chip` (`data-severity="warning"`, `aria-describedby`) with `span.console-reduced-chip-reason` beside it, in the header **after** `div.console-switcher` and before `nav.console-views` | drawn in the reduced mode and no other: the mode's headline for a reader who reaches the chip, and what brings a dismissed notice back (§2.14) |

- **`VIEWS`** is `live`, `graph` and `coordinator` (never withheld), `spend` (view `spend`)
  and `projects` (view `caps`). A view the mode withholds is **not offered**: no link is
  created for it.
- **Run detail has no button.** `currentView` may be `'run'`, reached from a `live-run` row;
  when it is, no nav button carries `aria-current`, "which is the truth about the navigation:
  none of the offered views is the one on screen". The installation issue view is the same
  kind of view (`currentView` is `'issue'`), reached from a rail row in the switcher's menu.
- **The keyboard.** The nav's buttons are ordinary tab stops (the redesign's tabs, which
  replaced Task 6.5's nav tablist on 2026-10-28). The rail's list keeps Task 6.5's tablist:
  `role="tablist"` on `div.console-rail-list`, `role="tab"` and `aria-selected` on its rows,
  a roving tabindex and Enter to select (§4.3). The switcher's button carries
  `aria-expanded` and `aria-controls` for its menu.

### 2.2 The installation picker — `console/src/renderer/installations.html` + `console/src/renderer/installations.ts`

A **second document** in its own window (owner ruling C9). Its markup is hand-written
HTML, so it is the one screen whose hooks are **element ids, not classes**: the view sets
no `class` and no `data-*` anywhere, and its only attribute write is `type="button"` on a
row button.

| Region | Hook (id) | What it holds |
|---|---|---|
| Heading | (no id — `h1`) | "Installations" |
| Status line | `#picker-status` | one of the states below |
| Onboarding screen | `#picker-onboarding` | `h2`, one `p` of fixed words, the one command as a `code`, and one link `button` — plus, for the **absent-binary** screen only, the resolution's own sentence as a `p` and the places it looked as a `p` ("Looked in: …"); empty when there is nothing to onboard |
| List | `#picker-list` → `li` rows | `li` with the row text and that row's own buttons: "Choose"/"Chosen", then one `Use <directory>` per checkout the CLI listed **except the one in use**, then "Stop using this checkout" when one is in use, and "Remove" when the console holds a record for the installation. **Every checkout the read kept is drawn — there is no per-row drawing cap** — and what the read keeps is bounded: a row whose document lists more than `MAX_DISCOVERED_INSTALLATIONS` (256) checkouts makes the whole document a refusal naming that row, so the buttons are never a shortened list of what the CLI printed |
| Warnings | `#picker-warnings` → `li` lines | the CLI's own `warnings` lines, one each |

**The add form is gone** (the owner's ruling of 2026-10-20, `desktop-console.md`
D13): there is no `#picker-add`, no `#picker-project`, no pick button and no
submit, because the console asks the operator for no binary, no config and no
project. The list is what `fugaro config list --json` printed, and the two
onboarding screens are one of them; the form's id-scoped rules left
`screens.css` with the markup.

States, all in the status line: **error/refusal** (`reply.reason`, which is the
CLI's sentence when the CLI would not list), **file unreadable** (`"<path> could
not be read: <refusal>"`), **empty** ("No installations yet." — the list has no
rows at all, which is the state the first-installation screen is drawn for),
**nothing chosen** ("No installation is chosen. Choose one below." — the list has
rows and none of them is active: the ordinary first run of a console whose CLI
lists installations, and *not* the empty state, which would tell the operator
there is nothing while showing them what to open), **ready/chosen**
("`<project>` is the installation this console is showing (`<mode>`)."), and
**blank** while an onboarding screen is drawn — the screen is the whole of what
the window has to say, and a second sentence over it would be a second voice.
`console/test/unit/installations-view.test.ts` builds its fake document **from the ids the file really
declares** and asserts each id exists, so the ids are a pinned hook set — this design
styles them by id rather than adding classes. The row whose config the CLI could
not read is drawn **with the CLI's warning line underneath it**, never hidden.

### 2.3 Live — `console/src/renderer/screens/live.ts`

The default screen: one glance at what is spending money now. Regions in tree order:
header → total block → `div.live-repos` of `section.live-repo`, each with a meters row (a
cap bar and a burn sparkline), a kill banner and a `div.live-runs` holding a
`table.live-table` of `tr.live-run` → an optional
`div.live-orphans` → **`section.live-review`**, the review inbox (v2 plan Task V2.7.1,
design F26). **The header is the title row**: `header.live-header` is one wrapping row whose
cells share a baseline — `h1.live-project` (the project's name, at the heading size),
then the connection as `span.live-connection-dot` beside the cell that carries its word
(`span.live-connection`, with `span.live-connection-age` and an optional
`span.live-connection-reason` after it), then `span.live-mode`, then `span.live-day`, with
an optional `p.live-note`, and — when the caller keeps a feed reading — the `p.live-since`
"last read" line, with the `div.live-stale` banner after it once that feed is stale. **The
connection's dot is decorative and its word is the reading**: the dot is `aria-hidden` and
takes a tone per state (`--live` for the live words, amber for `stale`, the danger tone for
the lost ones, and the neutral `--decor` for a state this build does not know), while the
word beside it is always the state the CLI reported, so no state is carried by colour
alone (§3.5). **The stale banner and the last-read line are the header's last children, not
siblings after the repositories**: `header()` appends them itself, inside the `header` it
builds, and `renderLive` appends exactly five things to the
root — the header, the total, `div.live-repos`, the optional `div.live-orphans` and
`section.live-review`.
A rule that puts the banner below the repos list is a rule that selects it in a place it
never occupies. **The orphan box holds the same `table.live-table` as a repository panel
does**: `runTable` draws the table and no box around it, so each caller draws the box that
scrolls — `div.live-runs` inside a project, and `div.live-orphans` where there is no panel
at all — because the table has a `min-width` and a table with a floor and no scrolling
ancestor leaves its last columns off a narrow window.

**The total block's cap bar has two shapes, and `leased_usd` is what picks one.** A document
that carries the lease — every document `fugaro watch` writes since #1256 — draws three
segments: spent, leased and headroom, with `counted_usd` left out because it is the sum of
the split's first two. A recording made before that release carries no `leased_usd`, and the
bar then draws two: the one figure the document does hold (`counted_usd`) and headroom. The
Leased row of the legend stays in both shapes — unmeasured in the two-segment one — so the
absence is a reading rather than a missing row, and bar and legend are drawn from the one
list (`fleetAccounts`), so the legend cannot describe a segment the bar does not paint.

**The review inbox is drawn whether or not it has a row**, which is why its section is not
in the optional list above: "nothing is waiting for you" is an answer, and a section that
vanishes when it is empty is a section the operator has to remember to interpret. What it
holds is the feed's `ready[]` — the finished runs whose outcome is `ready`, which is the
CLI's own list (`internal/watch/filter.go`'s `ReadyForReview`, drawn as `Ready for your
review (N)` by `internal/watch/render.go`). **Its pull requests are text and never links**:
the number is drawn as text through `literal` and the URL through `putText` with the `url`
class, and **nothing in the section carries `data-href`** — the one attribute
`link.openExternal` acts on — because no document on this tree says which provider a run's
repository is on (v1 design §5.1 rule 2 and its gap 7; design F26). There is no `<a>` in the
section at all: not an inert one, and not one waiting for a provider.

**Its box is dashed, and its empty state is one sentence.** The panel is a dashed
`--control-border` outline rather than the solid hairline the other panels carry, because a
solid box the size of a figure would read as one more figure of the day, while a dashed one
reads as the place a thing arrives in; the mark above the heading
(`svg.live-review-icon`, a check in a box, `aria-hidden`) is decorative and the heading is
the whole of the reading. **The empty panel says the hedged sentence and not the design's
positive one**, and the reason is what the document does *not* carry: `ready[]` ships empty
both when nothing is waiting and when the runs bucket has not been read, and `queued_note`
does not separate the two — it is absent when the read succeeded **and** when the read has
not happened yet (`fugaro watch`'s first frame never waits for the bucket scan,
`internal/cli/watch_queued.go`'s `queuedSource`; `watch.View.QueuedNote` documents `""` as
"when it was, or when nobody asked"), so on those frames the panel holds a feed nobody has
read. The design's own sentence for an empty inbox — "Nothing waiting. Finished runs with a
pull request land here." — is therefore **not drawn**: it asserts an empty inbox, and the
signal that would earn it (a note for the not-yet-read state) is not in this document.
`LIVE_REVIEW_EMPTY`'s hedged "in this feed" is true of both.

| Group | Class hooks |
|---|---|
| Header | `live-header`, `live-project`, `live-mode`, `live-day`, `live-connection`, `live-connection-dot`, `live-connection-age`, `live-connection-reason`, `live-note` |
| Total | `live-total` (the panel root, with `live-fleet`), `live-counted`, `live-cap`, `live-percent`, `live-total-notional`, `live-total-percent` (the cap bar, an `svg`), `live-fleet-wide` (the wide cell), `live-fleet-cap`, `live-fleet-cap-label`, `live-fleet-hero`, `live-fleet-hero-note`, `live-fleet-bar`, `live-fleet-track`, `live-fleet-segment` (one `rect` per account the bar draws, keyed by `data-account`), `live-fleet-legend`, `live-fleet-key`, `live-fleet-swatch` (one mark per account the bar draws — a row whose account it does not draw has none), `live-fleet-key-word`, `live-fleet-unread` (a value the document does not carry), `live-fleet-counted`, `live-fleet-sep`, `live-fleet-full`, `live-fleet-warn-icon`, `live-fleet-full-word`, `live-fleet-stats`, `live-fleet-stat`, `live-fleet-stat-label`, `live-fleet-burn`, `live-fleet-burn-unit`, `live-fleet-active`, `live-fleet-lost`, `live-fleet-queued`, `live-fleet-waiting` |
| Repository | `live-repos`, `live-repo` (a `section` with an accessible name — "Project runs"), `live-repo-head`, `live-repo-label`, `live-repo-name`, `live-repo-figures`, `live-repo-figure`, `live-repo-figure-label`, `live-repo-sep`, `live-repo-counted`, `live-repo-cap`, `live-repo-notional`, `live-repo-percent`, `live-repo-meters`, `live-halt` (a `button`, drawn disabled), `live-burn-rate`, `live-burn`, `live-burn-bar`, `live-burn-unit`, `live-runs`, `live-orphans` |
| Run row | `live-table` (the `table` itself), `live-table-head` (one `th` per column), `live-run` (a `tr`), `live-run-id`, `live-run-title`, `live-run-mode`, `live-pipeline`, `live-pipeline-segments`, `live-pipeline-segment` (one `li` per segment, keyed by `data-mark`), `live-run-when`, `live-run-dot`, `live-run-word`, `live-run-stage`, `live-run-round`, `live-run-state`, `live-run-coder`, `live-run-models`, `live-run-auth`, `live-run-spend`, `live-run-age`, `live-run-status`, `live-run-chip` (the state's own word), `live-run-pip` (the lost chip's decorative mark), `live-run-flag` (one per flag **except** a flag whose name is the chip's own word — the health `lost` and the bucket `queued` — which joins the chip's `title` instead, §2.9), `live-run-contradiction`, `live-reap` (a `button`, drawn disabled and only on a lost run) |
| Condition classes on a row | `live-run-queued`, `live-run-stuck`, `live-run-lost`, `live-run-done` |
| Review inbox | `live-review`, `live-review-icon` (the decorative mark, an `svg` whose two shapes are `live-review-icon-box` and `live-review-icon-check`), `live-review-title` (the CLI's one line, `Ready for your review (N)`, drawn as **one text run** — `reviewSection` writes the words, the parentheses and the count together, so no rule can space them apart), `live-review-list`, `live-review-row`, `live-review-repo`, `live-review-number`, `live-review-url`, `live-review-run-title`, `live-review-age`, `live-review-run-id`, `live-review-empty`, `live-review-note`, `live-review-capped`, `live-review-dropped` (the count of rows the cap left out), `live-review-of`, `live-review-total` |
| Kill banner | `live-kill`, `live-kill-word`, `live-kill-by`, `live-kill-at`, `live-kill-reason` |
| Stale / recency | `live-stale`, `live-stale-word`, `live-stale-silence`, `live-stale-threshold`, `live-stale-at`, `live-since`, `live-since-word`, `live-since-at`, `live-since-age` |
| Bars and numbers | `live-bar`, `live-num` (every numeric cell) |

**The rigor chip is the one mark on a run row that carries no colour and no `data-*`.**
`span.live-run-mode` draws one word — `POC`, `Startup`, `Pro`, `Critical` or `Hotfix`, or
the record's own value when this build does not know it — and it is a chip by its border
and its size and by nothing else. The five modes have no colour of their own anywhere in
this tree: not in `fugaro runs ls`'s MODE column, not in `docs/rigor-modes`. A rule that
painted one mode differently from another would therefore be inventing a meaning, and §3.5
has no mark to pair it with — so the word is the whole of the mark, on this row and on the
two other screens that draw it (§2.5's `projects-job-mode`, §2.6's `run-rigor`). A run whose
record carries no rigor block draws **no chip at all**, which is a different cell from one
whose mode is the built-in default.

`data-*` emitted by this screen, with their value sets:

| Attribute | Values | Note |
|---|---|---|
| `data-theme` | `light` \| `dark` | on the `section.live` root |
| `data-received-at` | an ISO stamp, or `''` | the newest frame |
| `data-fill` | `12.5%` or `n/a` — one decimal, and **nothing else** | a repository cap bar's fill, as text. `bar()` writes it from the **same percent reading** it writes `data-state` from (`live.ts`), and its one call site passes a *percent* figure — `repo.percent` — which `schema.ts` reads through `readNumber`, whose only two states are `measured` and `unavailable`. **The fleet budget panel's cap bar is not this element**: it is the `svg` of §2.3's Total row, drawn from the cap's own shares, and it writes neither this attribute nor `data-state`. So this attribute is never `≥ 12.5%` (no `lowerBound` reaches a percent reading), never `~$12.5%` or `est. 12.5%` (no percent reading is marked), and never the em dash; **a rule keyed on a marked form here would match nothing** |
| `data-height` | `12.5%` or `—` — and **never `n/a`** | each sparkline bar, **except the empty one**. The heights are the bar's own shapes, not the reading's: `sparkline()` builds every one as `measured(share)` or, for a sample that is not plottable, `gap(...)` (`live.ts` 472-490), and `spark()` draws those bars and copies each height's state — so a bar the console cannot fill draws the em dash and never the `n/a` a `data-fill` may carry. **The one-bar empty sparkline is drawn outside that loop** (`live.ts` 900-910): a line with no sample appends a single `li.live-burn-bar` carrying `data-state="gap"` and **no `data-height` at all** (909), so a rule keyed on `[data-height]` selects every bar of a drawn window and misses the bar of an empty one — the case the empty-sparkline rule of §2.3's state list is about |
| `data-state` | **two vocabularies.** On a bar: the reading's availability, of which **three of the contract's five words reach this screen** — `measured`, `unavailable`, `gap`. Or, on the connection cell **and its dot** only, **the word that cell drew**: the CLI's connection state, or `n/a` when `putText` refused it | `bar()` writes it from a **percent** reading (`live.ts` 877-882), and its one call site passes one (`repo.percent`), whose only two states are `measured` and `unavailable`; **the fleet budget panel's cap bar carries no `data-state` at all** — it is an `svg` of `rect`s keyed by `data-account`, and the panel's own cap, headroom and counts carry the text and, where there is no number, the reason in `title`; `spark()`'s bars write `measured` or `gap` — `gap` for a sample that is not plottable and for the one bar of an empty sparkline (896-925). **`notional` and `estimated` are in the attribute's vocabulary and never on this screen**: Live's notional figures go through `numberCell`, which writes the text and the `~$` mark and no state, so a rule for `[data-state='notional']` matches nothing here (the attribute is Spend's, §2.4). `header()` sets it on `span.live-connection` from the cell's own `textContent`, deliberately — an attribute echoing the raw value would put back on screen exactly what the refusal kept off it — and on `span.live-connection-dot` from that same cell, because the dot is the word's decoration and must take the tone of the word that was actually drawn rather than of the state the document named. **A `[data-state='…']` rule for a state colour must therefore name the words it is about — `measured`, `unavailable` and `gap` on a bar, and the CLI's own connection words on the cell and the dot — and may not paint every `[data-state]` element**, which would paint a cell for a value that is not a state |
| `data-account` | `counted` \| `spent` \| `leased` \| `headroom` | which money account a shape on the fleet budget panel draws. The **account is the attribute and not part of the class name**: every `rect.live-fleet-segment` and every `span.live-fleet-swatch` carries one class in common, and a rule reads the account off this attribute — the shape every other tone on this tree uses. `fleetAccounts` names them, so the value is one of the four words above and never a word from the document. **A swatch is drawn only for an account the bar draws**: a mark keys a fill, so the Leased row of the two-segment fallback (the bar paints no lease there) has none, and a rule may not read a row's mark as an account the bar drew |
| `data-segments` | an integer as a string | how many pieces a sparkline broke into |
| `data-mark` | `past` \| `current` \| `future` \| `danger` | what a pipeline segment draws: one `li.live-pipeline-segment` per segment, and the mark is the attribute rather than part of the class name, the shape every other tone on this tree uses (the fleet bar's `data-account`, a row's `data-tone`). **The segments are decoration** — the list is `aria-hidden` and the run's stage, round and state are written as text under it (`div.live-run-when`) — so no rule may read a mark as the only place a state lives. A stage this build does not know draws **no `current` segment at all**: every segment is `future` and the stage's own word is still drawn |
| `data-tone` | `plain` \| `amber` \| `alarm` \| `queued` | row and flag severity |
| `data-queued` | `true` \| `false` | |
| `data-flag` | `silent` \| `near deadline` \| `over deadline` \| `stuck` \| `unknown health` \| `unknown deadline` | one `span.live-run-flag` per flag, **except a flag whose name is the state chip's own word** — the health `lost` and the bucket `queued` — which is not drawn as a span at all: the chip already says the word, so the flag's reason joins the chip's `title` and the row says its state once (§2.3) |
| `data-contradiction` | `true` (present only) | health says quiet longer than the run is old |
| `data-killed` | `true` \| `false` | **the repository block only** (`section.live-repo`, `true`/`false`), and `true` on the kill banner `div.live-kill`. **Not on the total block** (`section.live-total.live-fleet`): `total()` emits no `data-killed` and appends the banner instead, so a rule that colours the total block by this attribute selects nothing |
| `data-key` | a sanitized slug, or `''` | the repository |
| `data-fast` | `true` \| `false` | burn faster than the cap allows |
| `data-stale` | `true` (present only) | the stale banner |
| `data-stale-by-age` | `true` \| `false` | the connection cell |
| `data-silence` | the measured silence **as drawn** — always `<seconds> s` | the stale banner `div.live-stale` — the number the screen drew, for a rule that wants to key on it without recomputing it from the stamps. **Only the seconds form is reachable.** `staleBanner` writes `formatReading(feedReading(feed), 'seconds')` (`live.ts` 1071), and the banner is appended only when `model.feed.stale` (1039) — which is `seconds !== null && seconds > 45` (`src/source/cli.ts`), so the reading is always **measured** and the attribute always carries a whole number of seconds. The `n/a` and `—` forms `formatReading` can produce for the `seconds` unit belong to readings this call site cannot hold: `feedReading` (`live.ts` 640) returns only `unavailable` — for a console that has heard nothing, which is exactly the feed whose `stale` is false — or `measured`, and **never a `gap`** |

State variants and **how each is marked**: killed/halted (`div.live-kill` with
`data-killed="true"`, the word `KILLED by` then who, when, why); stale (`div.live-stale`
with `data-stale="true"` and the sentence "…(a feed is stale after 45 s), so the values
above are the last ones read and the graphs break where the console stopped observing");
gap (`data-state="gap"`, the mark `—`); silent (`data-flag="silent"`, `data-tone="amber"`);
lost (the state chip's **own word**, `data-tone="alarm"`, the pip `●` beside it and the flag's
reason in the chip's `title` — the flag is not drawn as a second span, so a rule that paints
`span.live-run-flag` for `lost` matches nothing); near/over deadline (`data-flag`, amber and
alarm); queued (the `live-run-queued` class, `data-queued="true"`, `data-tone="queued"`, and
the chip's own word, its reason in the chip's `title`);
stuck (`live-run-stuck`, `data-tone="alarm"`); notional (**the text mark `~$` alone** —
Live's `data-state` comes from a `percent` reading, which is `measured` or `unavailable`, or
from a sparkline bar's height, which is `measured` or `gap`, so this screen never writes
`data-state="notional"`; the attribute is Spend's, §2.4); unavailable
(`data-state="unavailable"`, the text `n/a`, the reason in
`title`); empty (a sparkline with no sample draws **one** bar whose reading is a `gap`).
**There is no loading state**: the screen is drawn from a model, and the entry draws
nothing until the handshake answers. **There is no cap-reached flag**: the cap is a
`percent` reading and the repository bar's `data-fill`, and no attribute beside them —
colouring it at a threshold is a rule the stylesheet writes, not a state the code emits.
The fleet budget panel's own word for a cap at its limit is a sentence rather than a
state: `p.live-fleet-full`, drawn when a measured headroom is 0.

Time constants the stylesheet's threshold rules must agree with:
`LIVE_STALE_AFTER_SECONDS = 45`, `LIVE_SILENT_AFTER_SECONDS = 60`,
`LIVE_LOST_AFTER_SECONDS = 3 * 60`, `LIVE_NEAR_DEADLINE_FRACTION = 0.8` (80 %),
`LIVE_BURN_WINDOW_SECONDS = 5 * 60`.

### 2.4 Spend — `console/src/renderer/screens/spend.ts`

Regions in tree order: header → `div.spend-controls` (the range picker, the
breakdown switch and the person knob) → `div.spend-totals` → `div.spend-chart` →
`table.spend-table` → `section.spend-people`
(the by-person panel, console L5b, drawn only while the person knob is on) → notes →
warnings → the caps panel.

| Group | Class hooks |
|---|---|
| Header | `spend-header`, `spend-project`, `spend-source`, `spend-since`, `spend-until`, `spend-weeks`, `spend-range-arrow`, `spend-degraded`, `spend-notice` |
| Controls | `spend-controls`, `spend-knob`, `spend-picker`, `spend-item`, `spend-window`, `spend-switch`, `spend-dim`, `spend-dim-unknown`, `spend-persons`, `spend-persons-word`, `spend-persons-state`, `spend-persons-note` |
| Totals | `spend-totals`, `spend-total`, `spend-totals-word`, `spend-total-model`, `spend-total-notional`, `spend-total-compute`, `spend-total-runs`, `spend-total-run-hours`, `spend-totals-note`, `spend-totals-partial`, `spend-totals-rule` |
| Chart | `spend-chart`, `spend-axis`, `spend-axis-word`, `spend-axis-top`, `spend-chart-svg`, `spend-gridline`, `spend-axis-line`, `spend-series`, `spend-bar`, `spend-gaps`, `spend-gap-column`, `spend-chart-keys`, `spend-chart-key`, `spend-chart-gap`, `spend-chart-na`, `spend-legend`, `spend-legend-key`, `spend-swatch`, `spend-legend-word`, `spend-legend-empty` |
| Table | `spend-table`, `spend-table-head`, `spend-table-row`, `spend-th`, `spend-table-body`, `spend-row`, `spend-key`, `spend-gap-cell`, `spend-model`, `spend-notional`, `spend-compute`, `spend-runs`, `spend-run-hours`, `spend-tokens-in`, `spend-tokens-out`, `spend-partial`, `spend-unreadable`, `spend-num` |
| Caps | `spend-caps`, `spend-caps-title`, `spend-caps-project`, `spend-caps-day`, `spend-caps-mode`, `spend-scope`, `spend-cap-limit`, `spend-cap-word`, `spend-cap-reserve`, `spend-cap-defaults`, `spend-cap-global`, `spend-cap-figure`, `spend-cap-counted`, `spend-cap-daily`, `spend-cap-per-run`, `spend-cap-notional`, `spend-cap-compute`, `spend-cap-headroom`, `spend-cap-repos`, `spend-cap-repo`, `spend-cap-repo-name`, `spend-cap-spent`, `spend-cap-bar`, `spend-cap-bar-svg`, `spend-cap-bar-track`, `spend-cap-bar-fill`, `spend-cap-at-cap`, `spend-kill`, `spend-kill-word`, `spend-kill-by`, `spend-kill-at`, `spend-kill-reason` |
| Notes | `spend-note`, `spend-warning` |
| By person | `spend-people`, `spend-people-title`, `spend-people-view`, `spend-people-you`, `spend-people-operator`, `spend-people-since`, `spend-people-until`, `spend-people-bound`, `spend-people-max-runs`, `spend-people-reason`, `spend-people-note`, `spend-people-rows`, `spend-people-totals`, `spend-people-unlaunched-word`, `spend-people-unlaunched`, `spend-person`, `spend-person-user`, `spend-person-total`, `spend-person-currency`, `spend-person-runs`, `spend-person-succeeded`, `spend-person-failed`, `spend-person-halted`, `spend-person-infra`, `spend-person-other`, `spend-person-no-record`, `spend-person-counted`, `spend-person-notional`, `spend-person-counted-runs`, `spend-person-average`, `spend-person-note` |

`data-*`: `data-theme`; `data-current`, `data-enabled`, `data-known` (on the
`ol.spend-switch` itself, beside the eight words), `data-dimension`
(`day|week|month|year|repo|model|person|mode`, or `unknown`), `data-day`,
`data-until` (`set` \| `cli`) on the controls — `data-current` and `aria-pressed` are on
the **`button.spend-window` and `button.spend-dim`** that were pressed, and
`data-enabled` on the `button.spend-persons` and on a `span.spend-dim` this console
may not use; `data-state`
(`measured|notional|estimated`), `data-height`, `data-account`
(`model|notional|compute`) on the **chart's bars** (`rect.spend-bar`, keyed by the
`g.spend-series` they hang in) and on the legend's `span.spend-swatch` — a drawn bar
only ever carries one of those three states, because a value the document does not
carry draws no bar at all (`unavailable` and `gap` are the states of a *value*, and a
`[data-state='unavailable']` or `[data-state='gap']` rule on `rect.spend-bar` selects
nothing; the `n/a` of such a value is a label at the bar's foot, `span.spend-chart-na`)
— a `tr.spend-row` and a `li.spend-chart-key` carry **none of those three**, so a
`[data-state]` or
`[data-height]` rule is a rule about the chart;
`data-partial`, `data-hollow`, `data-unreadable`, `data-key` on **both** the chart's
`li.spend-chart-key` and the table's `tr.spend-row` (`data-partial` also on
`div.spend-totals` and on the bars, `data-key` also on the bars and the gap columns and
on the caps panel's `li.spend-cap-repo`), and `data-gap` on the keys and the rows alone;
an unreadable row's money was never measured, so `data-unreadable`
(`true|false`) on the **rows and `div.spend-totals`** is the honesty state of a by-mode
row whose every run has no readable record (a record the build cannot read, or one not
written yet), so a rule about it selects the row (or the total) and never the
`span.spend-unreadable` inside it, which carries the marker text and no attribute;
`data-degraded` (`true`) on
`span.spend-degraded`; `data-available`, `data-scope-known`, `data-daily-scope`,
`data-per-run-scope` (a document word, or `unknown`) on the caps panel — `data-available` on
`section.spend-caps` itself, the two scope attributes on the **`span.spend-scope`** of the
figure whose cap the document names a scope for — and `data-at-cap` (`true|false`) on the
**`li.spend-cap-repo`** that owns the daily-cap bar, `true` when the document's own
`headroom_usd` is a measured value at or below zero, or its `counted_usd` has reached its
`daily_cap_usd`: `data-available` also
on **`section.spend-people`**, where `data-view` (`all|own|unknown`), `data-operator`
(`true|false`) and `data-truncated` (`true|false`) record what the CLI's own read resolved —
the view it scoped the rows to, whether the caller is an operator of the installation, and
whether the read stopped at `max_runs` — so a rule about a private or a truncated panel
selects the panel, `data-limit`
(`max_reserve`) on **`div.spend-cap-limit`** — the reserve line, drawn above the global
block; and `data-killed` (`true`) on the **kill banner `div.spend-kill`** — never on
`section.spend-caps` itself, but **inside it**: the banner is appended into
`div.spend-cap-global` or `li.spend-cap-repo`, so a `[data-killed]` rule scoped to the caps
panel does select it.

**The chart is grouped, never stacked, and billed and notional are never added.** Every
bar is drawn at its own account's share of the one axis top — the largest value any
account draws — so no bar height and no axis top is a sum of two accounts, and a key's
three bars are siblings inside `g.spend-series` rather than segments of one column. A
value the document does not carry draws no bar at all: its `n/a` is written at that bar's
own foot in `span.spend-chart-na`. A day the history does not hold draws one hatched
column (`rect.spend-gap-column`, labelled `no record` in `span.spend-chart-gap`) and the
table's own sentence across the money columns (`td.spend-gap-cell`, which carries a
`colspan`) — a gap is a gap and never a `$0.00`. The `svg` is `aria-hidden` geometry
(`SPEND_CHART_SIZE` and `preserveAspectRatio="none"`), so every figure it draws is also a
reading in the table, a label in `ol.spend-chart-keys` or a word in `ul.spend-legend`.

**The caps panel is figures under their own words, and the renderer adds nothing to them.**
No column header carries `counted`, `billed` or `headroom`, so every figure is drawn as the
word for what it is beside the number itself (`div.spend-cap-figure` → `span.spend-cap-word`
plus the reading), the three money accounts among them: `billed` is the document's
`spent_usd`, `notional` its `notional_usd` (the `~$` mark) and `compute` its `compute_usd`
(the `est.` mark, and drawn **only** when the document carries the key at all — an absent or
`null` one is `n/a`, never `$0.00`) — three figures, never a sum, in no figure, in no total
and in no bar. A repository's **daily-cap bar** draws the CLI's own `counted_usd` against its
`daily_cap_usd` (`counted / daily_cap_usd`, clamped to the bar's hundred-unit box and
stretched to the window by `preserveAspectRatio="none"`, `aria-hidden` geometry as the chart
is), with the counted dollars, the cap and the row's `headroom` written as text beside it.
That is the CLI's
enforcement figure and not a sum the console made: `counted_usd` is what the installation has
reserved and not released, a ceiling on spend, and what separates it from `spent_usd` is
`leased_usd` — which is why no bar and no figure here is `billed + compute`, and why the
renderer computes no sum of its own. A repository with no daily cap draws **no bar** and
writes its cap `n/a` with its reason, never a zero-length bar and never a `$0.00` — its
`headroom` is still drawn, because that is the figure the `at cap` word is measured from
(below).

States: killed (the kill banner, `data-killed="true"`); partial/hollow
(`data-partial="true"` / `data-hollow="true"` on the key, the row and the bar, plus the
`(partial)` marker text in `span.spend-partial` — the bar itself is drawn **hollow**, the
ground showing through it and its own account's edge left, so today's bar is never a final
day's bar at a glance and never a gap either); unreadable (`data-unreadable="true"` on a
`tr.spend-row` or on `div.spend-totals` plus the marker text `no readable record` in
`span.spend-unreadable`;
the money cells are `n/a`, never `$0.00`, and the row keeps its `runs` — a by-mode row is
unreadable exactly when no run behind it has a readable record (a record the build cannot
read, or one not written yet), and the totals line exactly when every row it sums is);
gap (a day with no document → `data-gap="true"` on the key and the row, one hatched
column and the reason in `title`); unavailable (caps absent: `data-available="false"` and
the sentence "…every cap here is n/a, not zero"); empty per account (a `gap` reading, so
the bar is not drawn and the key writes its `n/a`); degraded (`span.spend-degraded` with
`data-degraded="true"` and the word `degraded`);
person view withheld (`button.spend-persons` with `data-enabled="false"` and
`aria-pressed="false"`, the state text `off`,
and the note drawn as the **text** of `p.spend-persons-note` — not in a `title`; the same
sentence is in `title` on the switch's `person` item, which is the element disabled until the
knob is on);
unknown breakdown (`li.spend-dim-unknown` with `data-dimension="unknown"`, under an
`ol.spend-switch` whose own `data-known="false"` is what says the document used a word this
console does not know);
unknown cap scope (`data-scope-known="false"`, "a scope this console does not know");
at cap (`data-at-cap="true"` on the **`li.spend-cap-repo`** itself, and the word `at cap` in
`span.spend-cap-at-cap`, drawn beside the row's own `headroom` — the room the CLI finds left
under **whichever** cap binds, the repository's own or the project-wide one, whichever is
tighter — and beside the counted dollars and the cap when the repository has one of its own;
so a repository the global cap is holding back, or one whose cap nobody set at all, still
says it is at its limit, with no bar drawn for it. The word and, where there is a bar, the
fill go amber, and the word is what carries the fact, never the tone alone, §3.5);
by-person read refused or never made (`section.spend-people` with `data-available="false"`,
the whole panel muted, the sentence drawn as the **text** of `p.spend-people-reason` — the
CLI's own refusal, or the console's reason for having made no read); by-person panel private
or unattributed (`data-view="own"` or `"unknown"`, or `data-view="all"` with no identity the
read was resolved for, drawing the document's totals and **no row at all**); runs that
never started (a count in no row and no total, drawn after the totals as
`span.spend-people-unlaunched` beside the word `never started` in
`span.spend-people-unlaunched-word`).
**A section that is being read says so, and keeps what it had.** The owner's rule of
2026-10-31 — "we need to render parts of screen as soon as we get that data, not wait for
it all before we show anything" — gives this screen two sections, each with a read of its
own (`budget show` for the caps, the report for everything else), and a read that has not
answered draws a **busy mark** at the top of its own section instead of blanking it: the
section carries `data-busy="true"` and `aria-busy="true"` and fades every child but its
slot, and the mark itself is `div.console-busy` — `span.console-busy-spinner`, the static
word `span.console-busy-still-mark` a reader with the reduced-motion preference sees
**instead of** the spinning element, and the label `span.console-busy-label` naming what is
being read. The section's own first child is `div.console-busy-slot`, which is where the
mark goes and where a cached view's stamp is drawn, and a read that outlives fifteen
seconds adds `p.console-busy-still` to it. A view that came out of the session cache is
drawn at once, stamped `p.console-stamp` — `span.console-stamp-at` carrying the words
`as of HH:MM` on the console's own clock, and the `button.console-stamp-refresh` that reads
it again beside them, in the line but not inside the element that says *when*, because a
node's `textContent` is its whole subtree's and a hook on the line would answer
`as of 09:05Refresh`. The mark and the stamp are drawn by
`console/src/renderer/section.ts`, the helper every tab adopts as it is rebuilt around
sections, so the hooks above are the console's rather than this screen's.
**There is no halted state on this screen.** The one
cap-reached marker it draws is the caps panel's own word `at cap` (above), beside the
figures it was measured from; nothing else on the screen carries one, and a rule may not
read the tone alone as the state.

Formatting: money is `usd` (2 decimals, grouping, sign before `$`), shares are `percent`
(1 decimal, used only for `data-height`), time is `hours` (2 decimals + ` h`), counts are
`count` (0 decimals). **Billed is drawn plain, notional under `formatReading`'s `~` mark and
compute under its `est.` mark, and the totals line says in words that the first two are
different kinds of money and are never added together** (the "Today is partial." half is
drawn only when the document says the range includes a partial day). Exported:
`SPEND_DEFAULT_CONTROLS = {since:'7d', until:null,
by:'day', persons:false}` — the screen opens on the recent week by day (the owner's choice,
console 0.118.0), while `report`'s own `--since` default stays 30 days —
`SPEND_PICKER_WINDOWS = ['7d','30d','90d','12w']`,
`SPEND_DIMENSIONS` (eight), `SPEND_ACCOUNTS = ['model','notional','compute']`,
`PARTIAL_MARKER = '(partial)'`, `UNREADABLE_MARKER = 'no readable record'`. The table's gap
row carries the module's own `TABLE_GAP_SENTENCE` — the gap row's own sentence — which is not
exported: it is the sentence a row draws, not a vocabulary anything reads.

### 2.5 Project — `console/src/renderer/screens/projects.ts`

Seven panels, each `section.projects-panel` carrying `data-panel`, `data-state` and
(where there is one) `data-operation`: `caps`, `kill`, `repositories`, `accounts`,
`secrets`, `policy`, `enrollment`. **The panel's state is a word as well as a frame**:
`span.projects-state` draws `read`, `not read`, or the design's word for a card this CLI
cannot read at all — `locked` — so no rule has to carry a state on a tone alone (§3.5).

**The jobs panel is gone, and so is the second read behind it** (the owner's report of
2026-10-31: that list "is slow and serves no purpose there"). `runs ls --json` is about
248 KB and six seconds for a day of runs, and this screen started it before the caps —
the whole of the panel's content — could be drawn, which is what made the tab about
twenty seconds with nothing on screen. The screen makes **one** read, `budget show
--json`; the jobs table, the `projects-jobs-*` hooks beside it, `data-operation="runs.list"`
and the two repository claims the run list carried (the enrolled-and-seen split and the
"seen, not enrolled" card) all leave the vocabulary with it. The repositories panel draws
the cap document's own `repos` and says nothing about which of them have run: a repository
enrolled with no run is not the same fact as a repository that ran with no cap row.

**The screen's shell is `section.projects > div.projects-body > div.console-busy-slot`**,
and the entry mounts it in the frame the tab is opened in. The mark, the skeleton and the
`aria-busy` on `.projects-body` are `src/renderer/section.ts`'s (§2.11, and the console
rule in `.claude/rules/console.md`): the section is dimmed while the read is out, never
blanked, and says "still reading" after a quarter of a minute. A screen this session has
already read is drawn at once, stamped `as of HH:MM` with a Refresh control.

| Group | Class hooks |
|---|---|
| Header | `projects-header`, `projects-project`, `projects-mode`, `projects-day`, `projects-title`, `projects-state`, `projects-source`, `projects-count`, `projects-number`, `projects-because`, `projects-panel`, `projects-warning` |
| Notes | two hooks, one per panel that carries a note: `projects-note` on the **secrets** panel's sentence (the one call of `note()`), and `projects-accounts-note` on the accounts panel's sentence — the **only** class on it, since the `'note'` in that call is the wire field `textCell` writes and not a class, so a rule written against `p.note` matches nothing. **No single hook reaches both**, and the second is **unreachable today** — see below. The jobs panel's own note went with the panel |
| Caps | `projects-caps`, `projects-reserve`, `projects-reserve-word`, `projects-reserve-value`, `projects-cap-table`, `projects-cap-head`, `projects-cap`, `projects-cap-scope`, `projects-cap-cell`, `projects-cap-value`, `projects-cap-source` |
| Kill | `projects-kills`, `projects-kill-list`, `projects-kill-row`, `projects-kill-scope`, `projects-kill-repo`, `projects-kill-state`, `projects-kill-action`, `projects-kill`, `projects-kill-word`, `projects-kill-by`, `projects-kill-at`, `projects-kill-reason` |
| Repositories | `projects-repos`, `projects-repo-card`, `projects-repo-card-title`, `projects-repo-list`, `projects-repo-enrolled`, `projects-repo-card-none` |
| Accounts | `projects-accounts`, `projects-accounts-repo`, `projects-account-list`, `projects-account`, `projects-account-none`, `projects-accounts-note` |
| Secrets | `projects-secrets`, `projects-secret-list`, `projects-secret`, `projects-secret-name`, `projects-secret-scope`, `projects-secret-versions`, `projects-secret-latest`, `projects-secret-label`, `projects-secret-label-key`, `projects-secret-label-value`, `projects-secret-allowed`, `projects-secret-bound`, `projects-secret-bound-word`, `projects-secrets-managed` |

`data-*`: `data-theme`; `data-panel`; `data-state` — **the panel states are
`read`, `unavailable`, `not-built`**; `data-operation`
(`budget.show|accounts.inventory|secrets.inventory`, absent on `policy` and
`enrollment`); `data-scope` (`global|defaults|repo`, on a caps row and on a kill row);
`data-slug` (the same two rows); `data-killed` (`true`|`false` on a kill row, and `true` on
the banner a killed row carries); `data-kind` (`enrolled` alone, on a repository card —
the run list that carried the other two went with the jobs panel).

The `not-built` state is the design's own honesty rule in the DOM: `policy` and
`enrollment` carry `data-state="not-built"` and a `projects-because` sentence ("not
readable with this CLI: …"). An `unavailable` panel carries the CLI's sentence or "no
document arrived for …". A `read` panel carries a count and a body; **a panel in any other
state has no body at all**, so a rule that painted an empty body would be painting nothing.
The two `not-built` panels are the design's locked cards: a dashed frame, the state word
`locked` beside the title, and no body, no list and no count behind it.

**The caps are one table and every cell in it is labelled.** `table.projects-cap-table`
carries a `th.projects-cap-head` per column, and each row — the project (`global`), the
repository defaults (`defaults`) and one per repository — opens with
`th.projects-cap-scope` naming what the caps are set for, before the two
`td.projects-cap-cell`s. Each of those carries its figure as money
(`span.projects-cap-value`) **with the cap's source word** (`span.projects-cap-source`, from
`CAP_SOURCES`) beside it, because a bare column of numbers names nothing: a rule that
right-aligned three `$` figures would align a cap the CLI derived from the project's
defaults with one an operator typed on the repository. A source this build does not know is
drawn as `unknown` and not guessed at. Above the table, `p.projects-reserve` carries the
largest single lease the project has out (`span.projects-reserve-word` names it,
`span.projects-reserve-value` is the money).

**The switches are rows whose state is a word.** `ul.projects-kill-list` holds one
`li.projects-kill-row` for the project and one per repository, each with
`data-killed="true"` or `"false"` and a `span.projects-kill-state` that draws `KILLED` or
`live`. A killed row carries the badge banner, `div.projects-kill` — itself
`data-killed="true"`, with `span.projects-kill-word` (`KILLED by`) and the who, when and why
only when the document has them, and nothing invented when it does not. The row's own action
is drawn through `disabledWrite` (§5.1): `Kill` on a live row, `Revive` on a killed one.

**The repositories are one card**, `article.projects-repo-card`, drawn from the cap
document's own `repos`: `data-kind="enrolled"` and a title saying so, then one
`li.projects-repo-enrolled` per repository. It was three cards, one per state of a document
that also listed runs — a repository with a cap row and a run (`both`), one with a cap row
and no run yet (`enrolled`), and one that ran with no cap row (`seen`, with the only action
any card drew, `Add cap`) — and the two that needed a run went with the run list. A known
but empty set says `none` in `p.projects-repo-card-none` rather than drawing an empty card
or nothing at all. The panel's reason for an unread set stays `projects-because`
(`repositoriesBecause`), so a set no document reported is never drawn as an empty one.

The accounts and secrets panels draw their rows as lists (`ul.projects-account-list`,
`ul.projects-secret-list`). **Neither read is wired in this build**, so the two panels are
drawn with their own "not read" sentence and no list at all — and the secrets panel's
`p.projects-secrets-managed` sentence ("All managed by fugaro.") waits for a document whose
every entry says so.

**`projects-accounts-note` is a hook no document this CLI produces can reach.** `fugaro
accounts ls --json` writes `note` with `omitempty` and answers `complete: false` whenever
that note is non-empty (`internal/cli/accounts.go`), and the screen builds the accounts
panel as an *unavailable* panel in exactly that case: the body that draws the hook — and
`projects-accounts-note` is drawn nowhere else — exists only for a panel that **read** a
document, and the note itself is drawn as the panel's `projects-because` sentence. So the
hook is emitted by the code and reachable by no feed today; it becomes reachable the day
`complete: true` and a non-empty `note` can arrive together, which is why it stays in the
vocabulary rather than being deleted.

Readings: money (`usd`) for the reserve and the caps; counts for the panel counts and the
secret versions. **No reading is a time on this screen** — every date is text, and no cell
of this screen carries a rigor mode: the run detail owns that answer (§2.6), and the jobs
row that used to repeat it went with the run list.

### 2.6 Run detail — `console/src/renderer/screens/run.ts`

Reached from a `live-run` row. The header, its status strip and its halt banner sit above
the two columns: the main column (stages → spend by stage → tolerated by the mode →
diagnosis → logs → replay) beside the aside (ledger → pull request), with the warnings
after both. Two steps drew it — 3a the header, at a glance at the run, the timeline and
the log window, 3b the ledger, the pull request and the replay scrubber.

| Group | Class hooks |
|---|---|
| Breadcrumb | `run-crumb`, `run-crumb-live`, `run-crumb-sep`, `run-crumb-repo`, `run-crumb-run` |
| Header | `run-header`, `run-head`, `run-id`, `run-retry`, `run-meta`, `run-meta-field`, `run-meta-label`, `run-meta-value`, `run-meta-absent`, `run-repo`, `run-workflow`, `run-recipe`, `run-branch`, `run-account`, `run-requested-by`, `run-started-by`, `run-rigor`, `run-rigor-word`, `run-rigor-mode`, `run-rigor-from-word`, `run-rigor-from`, `run-rigor-area`, `run-rigor-area-name`, `run-rigor-area-mode` |
| Status strip | `run-strip`, `run-strip-cell`, `run-strip-label`, `run-strip-value`, `run-status`, `run-outcome`, `run-stage`, `run-flags`, `run-settled`, `run-flags-state`, `run-stale`, `run-follow-up` |
| Halt banner | `run-halt`, `run-halt-word`, `run-halt-reason`, `run-halt-where`, `run-halt-scope-word`, `run-halt-scope`, `run-halt-sep`, `run-halt-at-word`, `run-halt-at`, `run-halt-detail` |
| Layout | `run-layout`, `run-main`, `run-aside` |
| Ledger | `run-ledger`, `run-ledger-hero`, `run-ledger-word`, `run-ledger-total-value`, `run-ledger-bar`, `run-ledger-rail`, `run-ledger-billed`, `run-ledger-estimate`, `run-ledger-legend`, `run-ledger-key`, `run-ledger-swatch`, `run-ledger-swatch-billed`, `run-ledger-swatch-estimate`, `run-ledger-key-word`, `run-ledger-model-value`, `run-ledger-compute-value`, `run-ledger-notional`, `run-ledger-notional-value`, `run-number`, `run-panel-title` |
| Reconciliation | `run-recon`, `run-recon-word`, `run-recon-line`, `run-recon-label`, `run-reported-usd`, `run-charge-usd`, `run-recon-diff`, `run-recon-diff-warn`, `run-recon-diff-value`, `run-reported-warning`, `run-pin-warning` |
| Spend splits | `run-split`, `run-split-word`, `run-split-caption`, `run-split-absent`, `run-route`, `run-route-name`, `run-route-usd`, `run-model`, `run-model-name`, `run-model-usd` |
| Spend by stage | `run-spend-stages`, `run-spend-stage-list`, `run-spend-stage`, `run-spend-stage-name`, `run-spend-stage-duration`, `run-spend-stage-calls`, `run-spend-stage-agents`, `run-spend-stage-usd`, `run-spend-stage-notional`, `run-spend-stages-more`, `run-spend-stage-more` |
| Tolerated by the mode | `run-tolerated`, `run-tolerated-total`, `run-tolerated-count`, `run-tolerated-list`, `run-tolerated-finding`, `run-tolerated-title`, `run-tolerated-label`, `run-tolerated-mode`, `run-tolerated-likelihood`, `run-tolerated-impact`, `run-tolerated-fix-cost`, `run-tolerated-class`, `run-tolerated-more`, `run-tolerated-dropped`, `run-tolerated-absent` |
| Stages | `run-stages`, `run-stage-list`, `run-stage`, `run-stage-icon`, `run-stage-dot`, `run-stage-check`, `run-stage-cross`, `run-stage-name`, `run-stage-body`, `run-stage-passed`, `run-stage-bar`, `run-stage-track`, `run-stage-fill`, `run-stage-fill-failed`, `run-stage-count`, `run-stage-tests`, `run-stage-tests-word`, `run-stage-failures`, `run-stage-failures-word`, `run-stage-meta`, `run-stage-duration`, `run-stage-spend`, `run-stage-deadline` |
| Pull request | `run-pr`, `run-pr-repo`, `run-pr-number`, `run-pr-sep`, `run-pr-url`, `run-pr-reason` |
| Logs | `run-logs`, `run-log-list`, `run-log-line`, `run-log-time`, `run-log-severity`, `run-log-stage`, `run-log-message`, `run-log-note`, `run-log-dropped`, `run-log-of`, `run-log-total`, `run-log-refused`, `run-log-refused-count`, `run-log-link`, `run-log-link-note`, `run-log-tail`, `run-log-tail-line` |
| Diagnosis | `run-diagnosis`, `run-failed`, `run-failed-word`, `run-failed-test`, `run-flaky`, `run-flaky-word`, `run-flaky-test`, `run-finding`, `run-finding-severity`, `run-finding-file`, `run-finding-summary`, `run-agent-message`, `run-agent-icon`, `run-agent-stroke`, `run-agent-said`, `run-agent-word`, `run-agent-text`, `run-report-path`, `run-report-word`, `run-report-value`, `run-comments-word`, `run-comments-path`, `run-comments-value`, `run-draft-note`, `run-follow-up-word`, `run-follow-up-pr`, `run-follow-up-comments-word`, `run-follow-up-comments`, `run-follow-up-session` |
| Replay | `run-replay`, `run-replay-head`, `run-replay-note`, `run-replay-count`, `run-replay-records-word`, `run-replay-sep`, `run-replay-bound`, `run-replay-track`, `run-replay-rail`, `run-replay-fill`, `run-replay-tick`, `run-replay-tick-live`, `run-replay-tick-past`, `run-replay-playhead`, `run-replay-scrubbed`, `run-replay-at`, `run-replay-axis`, `run-replay-from`, `run-replay-mid`, `run-replay-to`, `run-replay-no-span`, `run-replay-window-line`, `run-replay-window` |
| Warnings | `run-warning` |

`data-*`: `data-theme`, `data-tone` on the status strip's `span.run-outcome`
(`success`/`failure`, written by the screen and never by a reader of the row), and
`data-href` on a log link (written by `putLink`, never the navigating attribute).

States: terminal / still running / settled / stale draft / follow-up (header flags, as
text); halted (`div.run-halt`, the word `HALTED`); the outcome's tone (`span.run-outcome`
with `data-tone`: `success` for the three outcomes of a run that delivered what it
promised -- `ready`, `reviewed`, `artifacts` -- and `failure` for `draft`, which runstore's
own `StatusFailed` calls a draft PR; the word is drawn either way and the tone only ever
beside it, and `none` or a word a newer CLI resolved carries no tone at all); stage passed/failed (`run-stage-passed`
with `passed`/`failed`, and the stage's decorative icon, which is `path.run-stage-check`,
`path.run-stage-cross` or `rect.run-stage-dot` -- `aria-hidden` and never the only
channel); deadline ok/near/over (`run-stage-deadline`), or `n/a` when the
document has no deadline; a verify record's spend always `n/a` (no verify record carries
one: the run's spend by stage is the spend-by-stage panel's, or the cell names the missing
`stages` key when the document carries none); a stage's calls or sub-agents `n/a` with the
sentence "not counted for this stage" when the document left the pointer absent, and a
measured `0` when it counted the stage at zero -- while the same stage's two dollars are
`omitempty` floats and are drawn as the ledger's own split of that stage's figure: an absent
`model_usd`, or an absent `model_notional_usd` in the notional cell, is a measured `$0.00`
and never an `n/a`, and a subscription run's stage whose record carries no notional part at
all (a record written before C1.16) draws its whole figure in the billed cell as
`~$X` rather than as plain dollars -- while one that carries a part equal to its
whole figure, which is what a subscription run writes for a stage it settled without a
gateway, draws a measured `$0.00` there and the figure once, under the notional mark, in
the cell beside it; a notional part drawn as `~$X`; the ledger's bar drawn only when it can
be (`svg.run-ledger-bar` is absent when the total is not a measured figure above zero, when
the model line is not billed money or when the compute is neither measured nor estimated);
the reconciliation's difference drawn only when both figures were measured, in
`p.run-recon-diff-warn` only when it is not zero; an empty or absent spend split as the
console's `n/a` with its reason (`p.run-split-absent`); a pull
request with **no link at all** (`run.link` is typed `null`, and `PR_NO_LINK_REASON`
explains why); logs bounded ("lines not drawn: N of M") and a count of refused lines; replay
scrubbed / not scrubbed (`p.run-replay-scrubbed`), a window with fewer than two distinct
record times (`p.run-replay-no-span`), and a tick's tone beside its place
(`line.run-replay-tick-past` past the playhead, `line.run-replay-tick-live` otherwise --
the position is what carries the fact and the tone is the second channel).

Readings: money (`usd`) throughout the ledger (the hero, the two legend rows, the plan's
part and the reconciliation's three lines), the spend-by-stage rows, the spend splits; counts for tests, failures, dropped lines,
calls, sub-agents, tolerated findings and their score axes, follow-up PRs and comments,
replay records; **seconds** for a stage's duration and the replay window.

**The ledger is one billed total and everything else read against it.** `p.run-ledger-hero`
carries the caption `TOTAL CHARGED` and `total_usd` -- the dollars the table **billed**,
and the figure the console draws exactly as the document wrote it: nothing on this screen
adds anything to make a hero. `svg.run-ledger-bar` is two segments as shares **of that
total**, the billed model figure solid (`rect.run-ledger-billed`) and the compute an
outline (`rect.run-ledger-estimate`, dashes and a `non-scaling-stroke` hairline), and it is
drawn only when the total is measured and above zero, the model line is billed money and
the compute is measured or estimated; otherwise there is no bar and
`ul.run-ledger-legend` still carries both figures as text with `li.run-ledger-key` rows
(the swatch is `aria-hidden` and the word beside it is the reading), so no value is lost to
a drawing that could not be made. The model line is the **billed** part of the model figure
(`model_usd` less `model_notional_usd`), and the plan's part is drawn apart from it in
`p.run-ledger-notional` under "Paid by the plan, not in the total" with the notional mark
(`~$X`): it is **never** a segment of the bar and **never** inside the hero, because it is
money nobody was charged. It is drawn exactly once -- a record whose whole model figure is
the plan's (a subscription run, with the key or from before C1.16) draws that figure once,
on the model line, under the notional mark, and no second line appears.

**The reconciliation is the routed calls' two accounts, and the difference is a
difference.** `div.run-recon` draws `reported_usd` and `charge_usd` as the document wrote
them; both are `omitempty`, so an absent key is `n/a` and never `$0.00` (a run whose routed
calls no provider priced is not a run that cost nothing). The third line is
`charge_usd - reported_usd` **only when both were measured**, signed and drawn as money,
and `n/a` with the reason "no provider reported a cost for this run's routed calls"
otherwise; it carries `run-recon-diff-warn` exactly when it is not zero. The word is
**Difference** and not the design's "Over-charged": the console can measure that the two
accounts differ and cannot know which of them is wrong, so a word naming a cause would be a
claim it cannot support. `reported_warning` and `pin_warnings` are the CLI's own sentences
about this pair and are drawn verbatim under it -- the console draws what the run wrote and
never a conclusion of its own.

**The two splits are two scopes and are never summed.** `route_by` is the spend of the run's
**routed** calls, so a Claude call -- which has no route -- is not in it, and the panel's
`span.run-split-caption` says exactly that; `model_by` is the spend **by model**, and
carries an `unattributed` entry of its own. They are splits of different scopes, which is
why their figures do not add up to the hero, to each other, or to anything else, and why
neither is totalled, compared or divided here: `div.run-split` draws the document's entries
as they came, one `p.run-route` or `p.run-model` each, and an empty or absent split draws
`p.run-split-absent` -- the console's `n/a` with the reason -- rather than a list that reads
as "nothing was spent".

**The scrubber is the ring's own window, and it draws no control.** `svg.run-replay-track`
is the window's axis: `rect.run-replay-rail` is the 12 px track, one `line.run-replay-tick`
is drawn per **record** at its timestamp's share of `from`..`to` (clamped to the ends), the
`--live-fill` span runs from the window's start to the playhead, and
`line.run-replay-playhead` is drawn **only** when something was scrubbed to, at that
record's stamp. A ring with fewer than two distinct stamps has no span to place a tick on,
so it draws the track with no positions and one line saying so. The playhead's time label
sits in its own row under the track (`p.run-replay-scrubbed`, `span.run-replay-at` on the
amber fill) because a text node inside a `preserveAspectRatio="none"` drawing would be
stretched horizontally, and the three axis labels under it are the window's own
`from`, middle and `to` (`span.run-replay-mid` is the midpoint of the two ends, a scale
mark rather than a fact about the run). **The previous/next controls and "Show whole
window" are not drawn**: `entry.ts` passes no ring to this screen, so on the real screen the
ring is empty and a control over it would do nothing -- which is the one thing §5.1
forbids. The bound note (`REPLAY_BOUND_NOTE`) and the reason nothing is scrubbed
(`scrubbedBecause`) are drawn as text.

**The spend-by-stage and tolerated panels are the document's own two blocks, and neither is
drawn when the document carries nothing for it.** `section.run-spend-stages` draws the
top-level `stages` -- `runstore.StageTiming`, the run's own per-stage record -- one
`li.run-spend-stage` per stage, bounded at the CLI's own `diagnoseStages` (128), with every
entry it did not draw counted in `p.run-spend-stages-more`: the bound's overflow **and** an
entry that was not an object, so the rows and that count are the whole array. Its two dollar
cells are the ledger's split of the stage's one figure and are never a sum of it: the billed
part for `run-spend-stage-usd` (`modelReading`'s rule, §2.5 -- the stage's `model_usd` less
the part the record carries, so a part equal to the whole figure leaves a measured `$0.00`
beside the cell that names what paid it -- and, only for a record that carries no part at
all, that whole figure marked `~$X`), and the part the record wrote for
`run-spend-stage-notional`, which carries the console's notional mark so the plan's list
price is never read as money and is a measured `$0.00` when the record carries none. The
fallback belongs to the billed cell alone: applied to both, one stage's figure would be
drawn twice. `section.run-tolerated` draws the `tolerated` block: the `total` it
carries (never below the entries listed, and drawn beside them rather than replaced by their
count), then one `li.run-tolerated-finding` per finding, bounded at `runstore.MaxTolerated`
(100) with the entries it did not draw counted in `p.run-tolerated-more`. A document with no
`tolerated` block draws no panel, and `p.run-tolerated-absent` is one line naming the key
that is missing: the absence is not the claim that nothing was tolerated, because a run that
tolerated nothing, a record written before the field existed and a run whose record could not
be read all write no block. Neither panel sums, divides or derives a figure: every number on
them is one the document wrote.

**The Rigor row is the one place the whole block is drawn.** `div.run-rigor` is appended to
the header only when the record carries a block, and it draws four things in tree order:
the word `RIGOR`, the mode, the word `from` and the layer that decided it, then one
`span.run-rigor-area` per matched rule — each holding the rule's own name and the mode it
set. The modes and the layers have the console's own words (`POC` … `Hotfix`, `default`,
`the project layer`, `the repository`, `an area rule`, `the request`), and a value this
build does not know is the record's own text through `putText` with the `label` class, so a
mode a newer CLI resolved is drawn as the bounded, escaped text it is rather than as a word
this build would be inventing. The record's two stamps — the moment the block was written
(`at`) and the revision the configuration was read from — are **not drawn**: this row is
about what the run resolved, and `fugaro diagnose` prints both on its own lines.

### 2.7 Degraded: no Firebase — `console/src/renderer/screens/degraded.ts`

`section.degraded` with `data-kind="no-firebase"`, `data-theme`, `data-received-at`; the
mode line `span.degraded-mode` carries the literal `polling` **and**
`data-state="polling"`. Regions: `header.degraded-header` (`degraded-project`,
`degraded-mode-word` "run records only", `degraded-notice`, `degraded-warning`,
`degraded-document-warning`, `degraded-since` + `-word`/`-at`/`-age`) →
`div.degraded-caps` (`degraded-cap` with `data-state="unavailable"`, `data-kill-capable`
— always `false` on this shape — and `degraded-caps-word`) → `div.degraded-totals`
(`degraded-model-usd`, `degraded-model-notional-usd`, `degraded-compute-usd`,
`degraded-compute-unknown`, `degraded-compute-word`, `degraded-total-usd`,
`degraded-runs`) → `ol.degraded-runs` of `li.degraded-run` (`degraded-run-id`, `-repo`,
`-status`, `-stage`, `-outcome`, `-branch`, `-reason`, `-account`, `-created`, `-model`,
`-compute`, `-total`, `data-run`, `data-notional-model`).

Its presence is the design's §6.3 rule made visible: **caps read `n/a`, the kill
affordance is absent (`KILL_ABSENT_BECAUSE`), and a figure that is a lower bound says so**
(`COMPUTE_LOWER_BOUND_NOTE`).

### 2.8 The unavailable and refusal screens — `console/src/renderer/screens/degraded.ts` + `console/src/renderer/entry.ts`

`renderUnavailable` draws `section.console-unavailable` with `data-kind="no-binary"` or
`data-kind="refused"`, `data-theme`, `data-received-at=""`. Two bodies:

- **no binary** — `div.unavailable-body` holding `h1.unavailable-headline` ("no fugaro
  binary"), `p.unavailable-reason`, `div.unavailable-install` (itself holding
  `span.unavailable-install-lead` ("install it with") and
  **`code.unavailable-install-command`**), and a searched-places line —
  `div.unavailable-searched` holding `span.unavailable-searched-count` and
  `span.unavailable-searched-lead`. **The tags are the code's and they matter**: the
  install command is a `code` and not a `pre`, and the leads are `span`s and not `p`s, so
  a rule spelled `pre.unavailable-install-command` styles nothing at all. **There is no
  choose-the-binary affordance** (§6.3's row, as the owner's ruling of 2026-10-20 amended
  it): no `div.unavailable-picker`, no `span.unavailable-picker-lead`, no
  `data-picker="binary"` and no `span.unavailable-configured` — the console asks the
  operator for no path, and the control this bullet used to describe wired nothing.
- **refused** — `h1.unavailable-headline` ("the CLI refused this read"),
  `span.unavailable-operation` and `p.unavailable-sentence`, where the sentence is the
  CLI's own text through `sanitizeRefusal` or the console's
  `REFUSAL_UNREADABLE` ("the CLI refused this read, and its own sentence is longer than the
  console will draw").

The **version-skew notice** (`renderVersionSkew` in `shared/version.ts`, drawn through
`renderNotice` in `shared/notice.ts`) is the third: the whole screen when the console
refused, a card a screen is mounted under when it is reduced. It is `section.console-skew`
and it carries `data-theme`, `data-mode` and `data-skew`; it is drawn into the mount —
`document.body` — **before** the installation line, the nav and the panel, so it is the
document's first child and their sibling rather than a child of `div.console-panel`
(§4.1). It is a card in the flow and not a banner across the window (§2.14), and a reduced
one can be put away — in which case it stays where it is, the body's first child, and only
its `hidden` attribute changes.

`refusal.html` itself is a **document**, not a screed: `<main>` with an `<h1>` ("Fugaro
console") and one `<p>` ("This console will not draw a dashboard for the fugaro it
found."). It loads **no script at all**, so it is the one page in the console that cannot
carry `data-theme` from code — see [§3.6](#36-how-the-theme-is-chosen).

### 2.9 Every state variant, in one table

| State | Screen(s) | How it is marked in the DOM |
|---|---|---|
| killed / halted | killed: Live (the repo block, and the banner the total block appends), Spend (the caps panel's banner), Project (a kill row, and the banner that row carries). halted: Run (`div.run-halt`) | a banner element with `data-killed="true"` and the word `KILLED by` + who, when, why — or, on Run, `div.run-halt` with the word `HALTED` + who, when, why and **no attribute at all** (§2.6: that screen's `data-*` is `data-theme`, plus `data-href` on a log link). **The attribute is on the banner, on Live's repository block itself, and on Project's kill row** — `section.live-repo` carries `true`/`false` and the banner is appended into that same `section` (`live.ts`), and `li.projects-kill-row` carries `true`/`false` with the banner appended into the row, so `.live-repo[data-killed="true"]` and `li.projects-kill-row[data-killed="true"]` are rules this design allows; the two blocks that only *append* a banner, `section.live-total.live-fleet` and `div.spend-caps`, carry none (§2.3, §2.4) |
| stale (console clock) | Live | `data-stale="true"` banner + the threshold sentence |
| stale (document age) | Live | `data-stale-by-age="true"` on the connection cell |
| silent / near / over deadline | Live run rows | `span.live-run-flag` with `data-flag` and `data-tone` (`amber`/`alarm`) |
| lost / queued | Live run rows | the state chip `span.live-run-chip`'s **own word** (on a `live-run-lost` / `live-run-queued` row), the flag's reason in the chip's `title`, and — for `lost` — the pip `●`. The flag is **not** drawn as a `span.live-run-flag`: the chip already says the word, and the pipeline line above says it again, so the row says its state once |
| stuck | Live run rows | the `live-run-stuck` class + `data-queued` + `data-flag` |
| gap | Live, Spend | `data-state="gap"`, the mark `—`, a reason in `title` |
| unavailable | `data-state="unavailable"`: Live (**the repository's cap bar**, `bar()` — the fleet budget panel writes no state anywhere: its cap, headroom and counts carry the `n/a` and the reason in `title` instead), Spend (**the caps panel's own `data-available`, and no bar of its chart** — `barRect` writes `measured`, `notional` or `estimated` and nothing else), Project (**the panel state**, beside `read` and `not-built`), degraded (the cap line). **Not the sparkline's bars**: their state is the bar's own height, `measured` or `gap` (`spark()`, `live.ts` 896-925), so `[data-state='unavailable']` selects no bar of a burn window. `data-available="false"`: Spend's caps panel. The text `n/a` with a reason in `title`: **every** screen | **Run writes no `data-state` at all** — its numbers go through `putReading`, which sets the text and, for a value with no number, the `title` reason, and nothing else (§2.6), so a `[data-state]` colour rule selects nothing on that screen |
| notional / estimated | the text marks: Live, Spend, Project, Run, degraded. The attribute `data-state`: **Spend alone** | `~$` / `est.` are `formatReading`'s marks, so they appear wherever a notional or estimated reading is drawn, on all five screens (Live's repository and total notional figures, §2.3; the subscription model and the estimated compute on Project and Run, §2.5, §2.6; the degraded compute, §2.7). The **attribute** `data-state="notional"` / `"estimated"` is written by Spend alone, on `rect.spend-bar`, and it is the **account's** state, not the breakdown on the axis: `SPEND_ACCOUNTS` is the three accounts of one grouped chart, and each of the three reads its own column into its own state — `model_micros` → `measured`, `notional_micros` → `notional`, `compute_micros` → `estimated` (`microsReading`, `spend.ts` 870-886) — with the bar copying that reading's state into the attribute (`barRect`, `spend.ts` 2398). A value the document does not carry draws **no bar**, so it writes no state: its `n/a` is written at the bar's own foot instead. `data-dimension` is the switch's word for the x-axis (`day\|week\|month\|year\|repo\|model\|person\|mode`) and decides nothing here: the notional chart is notional whether the rows are days or repositories. Project's `data-state` is its panel state, Live's comes from a `percent` reading, Run writes none, and the degraded cap is always `unavailable` |
| partial / hollow | Spend | `data-partial="true"` / `data-hollow="true"` + the `(partial)` marker |
| unreadable | Spend | `data-unreadable="true"` on the row / `div.spend-totals` + the `no readable record` marker |
| degraded | Spend | `span.spend-degraded` with `data-degraded="true"` |
| polling (no Firebase) | degraded | `data-kind="no-firebase"` + `data-state="polling"` |
| no binary / refused | unavailable | `data-kind="no-binary"` / `"refused"` |
| skew (refused / reduced) | chrome | the version-skew notice `section.console-skew` (§2.14): `data-mode`, `data-skew`, the reason, the facts, and every withheld view as an `li[data-hidden]`. The nav is missing the withheld buttons, and a dismissed notice is `[hidden]` on that same root with `button.console-reduced-chip` left in the header saying so |
| the rail's row states (§4.1) | chrome (the installation rail) | `span.console-rail-state` with the **word** `running` / `silent` / `idle` beside its glyph `●` / `▲` / `○`. **Not a state the code emits today** — the rail is the canvas's addition, and what a row reads is §4.1's open item — so whichever task draws the row draws the word with it: the word is what makes the tone redundant, and the collapsed rail keeps it in the row's accessible name |
| not built | Project (`policy`, `enrollment`) | `data-state="not-built"` + a `projects-because` sentence |
| empty | picker (status line), Live (a `gap` bar, and `p.live-review-empty` with the review inbox's own sentence), Spend (a `gap` key and row, a caps note) | the console's own sentence, never an empty box. The inbox's sentence says "in this feed" on purpose: `ready[]` ships empty when the runs bucket could not be read as well as when nothing is waiting, and nothing in the document separates them — `queued_note` is absent both when the read succeeded and when it has not happened yet (§2.3) |
| cap reached | Live: **no attribute — not a state in the code.** The cap is a `percent` reading and the repository bar's `data-fill`; the fleet budget panel draws instead a sentence, `p.live-fleet-full` ("the cap is fully committed: new runs wait in the queue until a lease settles"), when a measured headroom is 0. Spend: `data-at-cap` (`true`\|`false`) on the caps panel's `li.spend-cap-repo`, beside the word `at cap` (§2.4) | on Live the stylesheet may colour a threshold; the code emits no flag — and a rule keyed on `p.live-fleet-full` may not read it as a state (§2.3). Spend does emit one, and it is the **row's**, not the bar's: a row whose headroom ran out under the project's cap carries it with no bar drawn |
| loading | **none — not a state in the code** | the entry draws nothing before the handshake answers |

### 2.10 Numbers, money and time — what the stylesheet must align

| Kind | Unit | Where | Format |
|---|---|---|---|
| **Money** | `usd` | Live (spend, counted, cap, notional, burn rate, sparkline title), Spend (points, peaks, rows, totals, every cap), Projects (reserve, caps, job model/compute/total), Run (ledger, stage spend, reported, charged, routes, models), degraded (all totals) | `$` + grouping + **2 decimals**, sign before the `$` |
| **Share** | `percent` | Live (cap percent, bar fills), Spend (`data-height`) | 1 decimal + `%` |
| **Duration / time** | `seconds` | Live (connection age, silence), Run (stage duration, replay window), degraded (feed age) | 0 decimals + ` s` |
| **A span, as a person says it** | `duration` | Live (**a run's age only**, the ELAPSED column of the runs table) | `21s`, `29m 21s`, `1h 02m` — the three scales, never a raw count |
| **Time (money-shaped)** | `hours` | Spend (run hours, totals) | 2 decimals + ` h` |
| **Count** | `count` | Live (**the review inbox only**: the ready rows it holds, how many it drew and how many the document carried), Spend (runs, tokens), Projects (counts, versions), Run (tests, lines, records), Coordinator (a repository's records, how many of them are in flight, its collisions, and what a bound left out), degraded (runs, searched) | 0 decimals + grouping |

**Live's money, shares and durations are what its *run* rows are made of**; the only `count`
readings on the screen are the review inbox's three (v2 plan Task V2.7.1), each a count of a
list the document carries and therefore `measured` — which is why the section can draw it
without a reason. The rest of the screen's numbers are `usd`, `percent`, `seconds` and the
run row's one `duration`, and
`data-segments` — the one other integer it puts on an element — is a bare attribute on the
sparkline list (`String(line.segments)`), not a reading drawn through `formatReading`, so it
carries no mark, no `title` and no state.

**Six screens draw a figure through a `numberCell`, and five of them have a number hook.**
Live, Spend, Projects, Run and the Coordinator each have their own — one per screen file, not
one in `app.ts` — and each prepends its own class: `live-num`, `spend-num`,
`projects-number`, `run-number`, `coordinator-count`. The coordinator's is the one whose
cell holds the console's own word for the count beside the figure, so the class the figure
is drawn in — and the one the stylesheet's alignment rule names, until step 7 of the
redesign moved the Coordinator's figure to a rule of its own in `flight-deck.css` — is
`coordinator-count-value` inside that cell. Each screen's hook (`coordinator-count-value`
on the Coordinator, the prepended class on the other four) is the single hook giving
**tabular figures** and right alignment, so §6.1 rule 2's "tabular figures, right-aligned
numerics" is satisfiable on those five screens.

**The degraded screen's `numberCell` (`degraded.ts`) prepends no class at all** — it is
`cell(parent, factory, tag, ...classes)` with no `-num` — so every figure on the degraded
and unavailable screens (the feed age, runs, the money totals, the per-row money, the
searched count) has **no tabular-figures and no right-align hook** — and the table just
above names the degraded screen as the source of a duration, a count and money like every
other kind. That is a gap in the code and
not a gap in the rule: **Task 6.4 closes it**, by adding `degraded-num` to that one helper
and writing the same `font-variant-numeric: tabular-nums` rule for it, which is why
`console/src/renderer/screens/degraded.ts` is in Task 6.4's Files — the one screen file the
styling work edits, and the reason it is a code change and not a CSS one.

Monospace is the mechanism on all six: `font-variant-numeric: tabular-nums` with a
fallback to `--font-mono`.

### 2.11 Graph — `console/src/renderer/screens/graph.ts`

The Graph draws one model twice: a **picture** (an `svg` whose geometry is the layout
worker's answer, written as SVG attributes because `style-src 'self'` admits no inline
style, §5.5) and a **list** that is the same model with every title whole. Regions in tree
order: header (the heading and the toolbar) → chips → the stage, which holds the canvas or the
list and, while a task is selected, the inspector beside it → the legend, under the canvas
alone.

**The screen answers a control in the frame it was pressed in** (0.117.0). The refresh, the
stream filter and the plan select are one section's controls, and
`src/renderer/section.ts` puts the busy mark and `aria-busy` on `section.graph` for as long
as the read it started is out — the canvas already drawn stays under the mark, dimmed and
never blanked — and says it is still reading after a quarter of a minute. The canvas is
drawn from the plan status alone and the run overlay decorates it when the run list lands,
so a screen that waited for both reads would be the all-or-nothing wait this rule forbids.

**The picture is read left to right** (console redesign, step 6a). The layout engine is
asked for `elk.direction: 'RIGHT'` and `elk.edgeRouting: 'ORTHOGONAL'`, so a task sits to
the **left** of the tasks that need it, the layers it computes are the columns a reader
reads across, and a dependency is a line that leaves one card's side and arrives at
another's. A card is **260 × 88** (`GRAPH_NODE_WIDTH`, `GRAPH_NODE_HEIGHT`), which is the
two constants the engine is told and the two **one stream's** layout cache is keyed by: the
key is a serialization of that stream's **structure**, so the box sizes are in it and a
card that changes size changes the key with it.

**One engine run per stream, and the runs are stacked as rows** (the owner's ruling of
2026-10-31). The engine's packing is two-dimensional: handed every stream at once, it
places one stream's plans among another's wherever they fit, and the band drawn around a
stream's cards then swallows another stream's cards. So each stream is laid out on its own
run, and the runs are placed below one another **in the model's order**, `GRAPH_ROW_GAP`
between two rows — the room two bands need to stay apart. A read that changes one stream's
structure lays that stream out again and reuses every other stream's positions, because
the key above is a stream's.

**A stream is a band, and the band is geometry the screen computes rather than a
container the engine is given** — a run is handed one stream's nodes and edges and no
containers. Each stream's band
is the rectangle around the cards that stream has **on the picture** — a shut chip's hidden
tasks are outside it — padded by `GRAPH_PAD` and grown upward by `GRAPH_BAND_HEAD` for the
band's own label, which is the stream's `Sanitized` name ("no stream" for the plans that
name none). Bands are drawn **first**, so every card and every release box sits on its own
stream's ground, and the picture's `viewBox` covers them. Because the rows are stacked, a
band **is** a row: it cannot reach into another stream's. Under `forced-colors: active` the
band's boundary is the one graph stroke drawn `GrayText`: both fills are `Canvas` there, and
a boundary in the reading tone would leave the row's ground drawn as a card the size of a
row (§4.4). An edge whose two ends land in
different rows is the one thing no run has routed, and `crossStreamRoute` draws it as an
orthogonal line through the gutter between the two rows — no document this console builds
has one today (a plan's `needs` name tasks of that plan, and a plan belongs to one stream:
the reconciliation's F4, and GV4.1 is the slice that would add them).

**The canvas's ground is a dotted grid, and it is decoration.** It is a CSS background on
`svg.graph-canvas` and not an element: nothing is read from it, no state is carried by it,
and a background needs no element to be drawn, sized or hidden from a reader.

**The toolbar, the inspector and the legend are step 6b** (console redesign), and all three
are **read-only**: no IPC, no preload and no spawn changed to draw them, and the two controls
the design's panel asks for reach the code through what the screen already had — `Open run`
is the door `showRun` the card's Enter already used, and `Pause task` is a write this release
cannot make, so it is drawn `disabledWrite` with the console's own sentence and no listener at
all.

**The stream filter is a segmented control and not a tablist** (step 6b's one shape change).
Its buttons are four controls of one screen and not four panels: nothing appears where a
pressed button was, so each segment carries `aria-pressed` and the group is one
`role="group"` with the one name the set shares (`streams`), rather than `role="tab"` and
`aria-selected`. **Everything else about it is unchanged**: `data-tab` still keys a stream
(`all`, `stream:<name>`), the roving `tabindex` still puts exactly the chosen segment in the
tab order, and §4.3's keys — Left and Right, Home and End, each activating what it moves to —
are the same keys, because a chosen stream *is* the graph that is drawn and there is no second
key to press.

**The chosen stream is removed from the model before the layout, and that is not the same as
hiding it.** `buildGraphModel` builds its columns from the chosen tab alone, so the engine is
asked for one stream's nodes and `GraphLayout` places the rows the model holds — a filter that
merely hid the other streams' cards would leave their rows in the picture as empty bands,
which is a graph with holes in it. The band under a row is drawn from the nodes placed **in
that row**, so no empty band is drawn either way, and `graph.test.ts` counts the placement
calls and the bands a filtered read draws.

**The inspector is text, and it is the same facts the card draws.** A selected task opens a
panel beside the canvas — the card's words again as chips, the stream it is in, its run, what
it waits on and what waits on it — so a reader who cannot see the canvas, or who never looks
at it, reads the whole of the selection. The two related-task lists are **buttons**: pressing
one selects that task's card, which is how the panel lets a reader walk the graph, and each
button draws the task's `Sanitized` title rather than spelling its id — an id is a key this
screen joins two records on and never spells (`graph/model.ts`), and it travels in `data-key`
for the entry. Escape closes the panel and puts the keyboard back on the card, and a task of a
shut finished release — which has no card on the canvas — leaves the keyboard on the panel
itself, which is why it carries `tabindex="-1"`. **Pause is `disabledWrite`**, the one shape
this console draws a write it does not perform in: a real button, `aria-disabled="true"`, the
sentence in `title` and `aria-label`, and nothing listening.

**The legend is under the canvas and it is a key, not a second drawing.** One line per state a
card can carry and one per kind of edge, each a swatch beside the word — the swatch is
`aria-hidden`, so a reader hears the word once, and it is drawn as **the state's own boundary**
(1 px neutral, 2 px for the four running words and for `halted`, 2 px dashed for `blocked`, and
the live tint inside for `critical`, whose second channel on the canvas is the glow behind the
card) and as **the line's own width** (2 px for a dependency, 3 px for the critical path). Nothing in it
is a figure the graph computed: it is the console's own vocabulary, drawn even while the
canvas has none of those states in it. It is drawn **only under the canvas** — the list spells
every word already and carries neither kind of line.

| Hook | Drawn as | Note |
|---|---|---|
| `section.graph` | the screen's root | carries `data-theme` like every other screen's |
| `graph-header`, `graph-title` | `header`, `h1` | the heading line, `--space-sm` below it (§3.8) |
| `graph-toolbar` | `div` | the screen's controls as one row: the stream filter and the three buttons beside it |
| `graph-filter`, `graph-tab` | `div` + `button` | the segmented stream filter: `role="group"` with the set's one name on the `div`, `aria-pressed` on each button, a roving `tabindex`, and §4.3's own keys — Left and Right, Home and End, each activating the segment it moves to |
| `graph-actions`, `graph-view`, `graph-refresh`, `graph-fit` | `div` + three `button`s | view switch, refresh, and fit — the last one is drawn only while the canvas is |
| `graph-not-shown` | `p` | a bound dropped plans, tasks or needs: the console's own sentence, in `--warn` |
| `graph-chips`, `graph-chip-group`, `graph-chip-stream` | `div`, `section`, `h2` | the finished releases, one group per stream |
| `graph-chip-list`, `graph-chip-item`, `graph-chip` | `ul`, `li`, `button` | one chip per finished release: `aria-expanded` is the fact the click flips |
| `graph-chip-name`, `graph-chip-mark`, `graph-chip-version` | `span`s inside the chip | the name, the `✓` mark and the version |
| `graph-chip-body`, `graph-chip-tasks`, `graph-chip-task`, `graph-chip-task-title` | the open chip's own panel | drawn **only** while `aria-expanded` is `true` |
| `graph-release-bar` | `p` (chip body) and `span` (list heading) | the "n of m done" bar, both places |
| `graph-canvas` | `svg` | `viewBox` carries the zoom; the element has no width or height of its own, and its dotted-grid ground is a CSS background on this element rather than an element of its own |
| `graph-band`, `graph-band-box` | `g` + `rect` | one band per stream that has a task on the picture, drawn **first** so it is the ground of its own row: the `g` carries `data-stream`, and the box is the row's wash |
| `graph-band-label` | `text` at the band's head | the stream's own `Sanitized` name ("no stream" for the plans that name none) |
| `graph-release`, `graph-release-box` | `g` + `rect` | one frame per release with a task on the picture |
| `graph-release-heading`, `graph-release-bar` | `text` inside the box | the heading line and the bar under it |
| `graph-release-stream`, `graph-release-name`, `graph-release-version` | `tspan`s inside that heading | the same three parts the chip draws |
| `graph-edge`, `graph-edge-path` | `g` + `path` | one edge per dependency; the `g` carries the three facts below |
| `graph-node`, `graph-node-box` | `g` + `rect` | one card per task; the `g` carries `data-state`, `data-selected` and `data-critical`, a `role` (`button` where Enter or Space opens the card's run, `group` where no key does anything) and an `aria-label` of the whole title and every word the card draws — the card's lines cut a title at three, and the name is where it is whole |
| `graph-node-title`, `graph-node-title-line` | `text` inside the card, and one `tspan` per line it was wrapped to | SVG has no wrap, so the lines are the screen's: a title is wrapped at `GRAPH_NODE_TITLE_LINE_CODE_POINTS` over at most `GRAPH_NODE_TITLE_LINES` lines, and only a title that still does not fit is cut (with an ellipsis, on a code-point boundary) — the whole title is still in the list |
| `graph-node-word`, `graph-node-pr`, `graph-node-run`, `graph-node-mark` | `text` inside the card | one element per state word, the pull request number, the run and the reading's mark |
| `graph-node-comet` | `rect` on the card's own border, `aria-hidden` | the light streak that travels a **running** card's border: GV2.4's motion, drawn only while the first word is one of the four a running task draws (`implementing`, `in review`, `fixing`, `queued`) |
| `graph-node-pulse` | `rect` behind the card, `aria-hidden` | the glow a **critical** card draws: a filled, blurred wash of the same `--accent` the critical path is drawn in, a few pixels larger than the card so what a reader sees is the spill around it and never the card's own boundary, which keeps the state's tone |
| `graph-stage` | `div` | the drawing and the inspector side by side: two grid columns, and the panel's column is zero wide while nothing is selected, so the canvas lays out as it did before the panel existed |
| `graph-inspector` | `aside`, `tabindex="-1"` | the selected task's panel, drawn **only** while a task is selected: `aria-label` names it, and it is focusable so the press that selects a task the canvas does not draw — one of a shut finished release — has somewhere to land |
| `graph-inspector-title`, `graph-inspector-words`, `graph-inspector-word` | `h2`, `div`, one `span` per word | the task's whole title, then the words its card draws one element each |
| `graph-inspector-facts` | `dl` | the panel's label/value grid: (`dt`, `dd`) per fact, in the order stream, run, depends on, unblocks |
| `graph-inspector-stream`, `graph-inspector-run` | `span` inside a `dd` | the stream's `Sanitized` label, and the run's suffix (monospaced, like every other id) or the console's own sentence when there is none to open |
| `graph-inspector-needs`, `graph-inspector-unblocks` | `ul` inside a `dd` | the two ends of every dependency, one list each |
| `graph-inspector-need`, `graph-inspector-unblock`, `graph-inspector-task-title` | `li` → `button` → `span` | one button per related task, drawing that task's `Sanitized` title with its layout key in `data-key`; pressing it selects that card |
| `graph-inspector-none` | `span` inside a `dd` | the console's own word for a task with no dependency at that end |
| `graph-inspector-actions`, `graph-inspector-open`, `graph-inspector-pause` | `div` + two `button`s | the panel's two controls: the run door (drawn only when the read found a run), and a `disabledWrite` Pause |
| `graph-legend`, `graph-legend-title`, `graph-legend-list`, `graph-legend-item` | `div`, `h2`, `ul`, `li` | the key under the canvas, one line per state and per kind of edge |
| `graph-legend-swatch`, `graph-legend-word` | `span`s inside a line | the state's own boundary (or the line's own width) as a swatch, `aria-hidden`, and the word beside it |
| `graph-list`, `graph-list-plan`, `graph-list-title` | the list's `div`, one `section` and `h2` per plan | `role="list"` on the `div` |
| `graph-list-release`, `graph-list-nodes`, `graph-row` | the list's per-release `h3`, `ul` and `li` | one row per task, with the whole title |
| `graph-row-title`, `graph-row-mark`, `graph-row-pr`, `graph-row-run` | `span`s inside a row | the same five facts the card carries |
| `graph-words`, `graph-word` | `span`s in the list | the words, one element each |
| `graph-empty` | `p` | no plan to draw, or no layout to draw: two different sentences, one hook |

**Attributes a rule selects**, and the one thing each means:

| Attribute | Values | On |
|---|---|---|
| `data-state` | the node's **first** state word (§3.9's list), or the word a legend line stands for | `g.graph-node`, `li.graph-row`, `span.graph-legend-swatch` |
| `data-selected` | `true` \| `false` | `g.graph-node`, `li.graph-row` — and, on `g.graph-edge`, whether either end is the selected one |
| `data-critical` | `true` \| `false` | `g.graph-edge`, and `g.graph-node` — a card's first word cannot say it, because a critical task that is running says `implementing` there |
| `data-cross` | `true` \| `false` | `g.graph-edge` |
| `aria-pressed` | `true` \| `false` | `button.graph-tab` (§2.1's hook, this screen's stream filter) |
| `aria-expanded` | `true` \| `false` | `button.graph-chip` |
| `data-edge` | `dependency` \| `critical` | `span.graph-legend-swatch` — the two kinds of line, and the only two |

**The attributes no rule needs**, and §2's rule read the other way: the graph also emits
eight. `data-key` (a card's, a row's and an inspector button's layout key), `data-run-id` (the
run a gesture would open), `data-tab` (a filter segment's own value), `data-chip` (a chip's
release key),
`data-view` (the view switch's own value), `data-stream` (the stream a chip group **and a
band** are drawn for), `data-release` (a box's key) and `data-word` (the word an element
draws, beside the text it draws). They are the screen's own bookkeeping — the entry reads
`data-key`, `data-run-id`, `data-tab` and `data-chip` back off the drawn tree to route a
click, and the other four name only what the screen itself knows (which drawing is showing,
which stream a group or a band belongs to, which release a box is, which word an element
draws) — and no stylesheet rule may select one without adding it above first. Six of the eight are refused by name in
the inventory test with `data-hidden` and `data-skew`; the other two, `data-key` and
`data-view`, are §2's hooks on other screens and stay licensed (§2's licence paragraph).

**How each state is marked** (§3.5: a word, and the tone second):

| State | The word | The second channel |
|---|---|---|
| implementing / in review / fixing / queued | on the card, and in the list | the card's boundary in `--live` at 2 px — the tone a task being worked on is, and the tone the comet travels and the critical path is drawn in |
| waiting | on the card, and in the list | the card's own neutral boundary (`--line-strong`, 1 px): nothing is happening, and nothing shouts |
| blocked | on the card, and in the list | the boundary in `--decor` **and dashed** — the one state a reader needs at a glance |
| halted | on the card, and in the list | the boundary in `--warn`: something stopped and a person has to look |
| ready | on the card, and in the list | the neutral boundary, as waiting |
| done | on the card, and in the list | the neutral boundary and the title in `--text-3`: no longer a reading anyone acts on |
| critical | a **second** word beside the first | the edges of the critical path, 3 px of `--live` against a plain edge's 2 px of `--decor`, and the card's own glow behind it in the same `--live` |

**A dependency is a line and not a state**: an edge is `--decor` (the tone a drawing is
made of) unless it is on the critical path, which is `--live`. **The edge widths are the
second channel and the only one a forced-colors reader keeps**: a cross-release edge is
1 px, a plain one 2 px, the critical path 3 px, and the edges of a selected task 3 px of
the reading tone — and the selection's tone is the one thing that mode takes away, so its
arm draws those edges **4 px of `Highlight`**, a pixel wider than the critical path: the
whole distinction survives `forced-colors: active`, where the dashes of a blocked card
survive with it (§4.4). The selected **card** is a width too, 3 px
against a running card's 2, and it keeps its state's tone: a selection that recoloured the
boundary would hide the state it is selecting. No rule paints `--ok` on this screen: §3.2
licenses that token to two readings, and a finished task is neither.

**This section's motion is §3.9's amendment, and GV2.4 landed it, in the spec's own three
shapes.** A light streak travels a running card's border, a glow pulses behind a critical
card, and a flow runs along a critical edge — and every state above is still the static
drawing each of them leaves behind: the graph writes its own
`@media (prefers-reduced-motion: reduce)` arm and its own `forced-colors: active` arm, and
the global freeze in `base.css` stays where it is. No other screen animates anything, and no
figure animates anywhere (§3.9).

### 2.12 Coordinator — `console/src/renderer/screens/coordinator.ts`

The Coordinator draws the project view: the action queue, the fleet's totals, and one panel
per repository the installation's config lists, each with the sweep's own marker, the three
counts beside the repository's name, the records with the runs behind them, the collisions,
and the pull requests whose host state no sweep could read. Regions in tree order: the
screen's source line → the console's sentence about the age the view does not carry →
`section.coordinator-queue` (the head line: the count of what is waiting, the word it counts
and, for a queue that holds nothing, the console's own sentence → one `coordinator-queue-part`
per read, each holding its items or the note saying that read was not made → the blind-spot
line) → `section.coordinator-totals` (the fleet's strip) → one `section.coordinator-repo` per
repository (the head row: the name and which view the node is → the marker → the counter strip
→ what a bound left out → the records → the collisions → the unreads) — or, for an
installation that lists no repository, the console's own empty sentence instead of the panels.
**The screen reads four command lines** (`fugaro coordinator status --from-store --all
--json`, `runs ls --json --since 24h`, `plan status --all --json` and `budget show --json`:
one per independent document, and `report` is not read on this page at all), and it draws
**one button per queue item** and nothing else: a control whose word is the console's own.

**The action queue and the fleet's totals are console 0.118.0's K1**, and they are the two
sections the screen gained above the panels: what is waiting for a person — a pull request
open and not a draft, a halted or failed run, a plan task `ready` to launch, a budget alert —
with **exactly one button each** (`Open run` opens the run; `Retry` and `Launch` open the
launch confirmation of §2.13 with the item's repository and plan task filled in, which is
rule 11's window and rule 11's argv and no second write; `Open Spend` navigates to the Spend
screen, and nothing here calls `budget kill` or `budget resume`), and the fleet's own figures
off the same three documents plus `budget show`. A pull request's number is **text** in K1 and
never a link: the renderer builds no destination out of wire text. The screen's first
paragraph above is 0.117.0's and stays — the source line and the console's age sentence are
mounted in the frame the tab is opened in — and what K1 changes is that the drawing is no
longer read-only, so the one transition in this block is the pair of properties a hover moves
on a queue button, beside the focus ring the shared blocks give every control.

**The panel, the strip, the banner and the row are console redesign step 7.** The markup this
step draws replaces the markup `screens.css`'s Coordinator block met, and that block is
deleted with it, so every rule for this screen now lives in one section of `flight-deck.css`:
the name and the source line are **one head row** rather than two stacked lines, the counts
are a **strip** of three labelled cells whose collisions cell is marked when the repository
has any, the sweep's marker is a **banner** rather than a line of prose, and a record is a
**row** whose own head carries the number, the title, the branch, the release, the
state-and-draft chip and the flight word, with the runs nested under it. Two hooks moved with
the drawing: `coordinator-count-value` left §2.10's shared alignment rule for a rule of its
own (the shape `run-number` already had after the Run step), and the runs behind a record are
cut at `COORDINATOR_LIST_CAP` like a collision's file list — a pull request a hundred runs
deep is not a row of a hundred lines, and the count left off is drawn rather than left
silent.

**What is already known is drawn before the read** (0.117.0, kept): the source line and the
console's age sentence are mounted in the frame the tab is opened in, and the body's own
section is filled when the document arrives — the summary first, the per-PR and per-run
detail after, in idle chunks or on the operator's expand. While the read is out the body
carries `src/renderer/section.ts`'s busy mark, its `aria-busy` and a skeleton of the same
shape, and a document this session has already read is drawn at once, stamped `as of
HH:MM` with a Refresh control. **The declared-by rule is 0.117.0's too, and it stays**: the
introducing word and the declaration itself are drawn only when the launch record wrote a
nameable one (`nameableText`), because the phrase is a claim about a person and the record
only ever says what it was told.

| Hook | Drawn as | Note |
|---|---|---|
| `section.coordinator` | the screen's root | carries `data-theme` like every other screen's |
| `coordinator-source` | `p` | the read's own command line, in the console's words |
| `coordinator-age`, `coordinator-empty` | `p` | the console's two sentences: the age the stored view does not carry, and an installation that lists no repository |
| `section.coordinator-queue`, `section.coordinator-totals` | the queue's section and the fleet strip's | the screen's two K1 sections, on the frame the redesign gives a panel (§2.12's `section.coordinator-repo` below) |
| `coordinator-queue-head` | `div` | the queue's own line: the count of what is waiting, the word it counts, the console's sentence for a queue that holds nothing, and the slot the section's busy mark is put in |
| `coordinator-queue-count` | `span` | the count the console counted off the items it drew — drawn only when every one of the queue's four reads has answered, and as nothing rather than a `0`; a queue whose reads are still out, or refused, draws no count and no empty-state sentence either |
| `coordinator-queue-word`, `coordinator-queue-empty` | `span`s | `items need you`, and the sentence a **whole** queue that holds nothing draws instead of a zero — a queue the console could not read draws neither, because a refusal in a part is not an empty part |
| `coordinator-queue-part` | `div` | one read's part of the queue: its items, the count the cap left off, or the note saying that read was not made. A host, and it holds no state: the section's mark says the part is still being read |
| `coordinator-queue-items` | `ul` | one part's items, each drawn only when the part holds one |
| `li.coordinator-queue-item` | `li` | one thing waiting for a person |
| `coordinator-queue-kind` | `span` | which of the four kinds the item is, in the console's own word (`PR`, `JOB`, `PLAN`, `BUDGET`) |
| `coordinator-queue-subject` | `span` | the item's own id — a pull request's number, a run's id, a task's id — and empty for a fleet-wide alert, which draws no not-available mark in its place |
| `coordinator-queue-scope`, `coordinator-queue-repo`, `coordinator-queue-title` | `span`s | where the subject sits (a branch, the plan a task belongs to, the plan task a run recorded), the repository, and the untrusted title whoever wrote the thing wrote, each drawn only when the document carries one |
| `coordinator-queue-why` | `span` | why it is waiting, in the console's own words: the rule the queue applied, and for a budget alert which of the three grounds it stands on |
| `button.coordinator-queue-button` | `button` | **the item's one control**: `Open run`, `Retry`, `Launch` or `Open Spend`. A button and never an anchor |
| `coordinator-queue-note` | `p` | the console's sentence where a read was not made at all: an installation that records no checkout, or the reduced mode's withheld caps |
| `coordinator-blind` | `p` | the line under the items: the console's own sentence and the two counts of what the page cannot reach |
| `coordinator-blind-word` | `span` | the introducing sentence of that line |
| `coordinator-blind-stale`, `coordinator-blind-unread` | the line's two cells | repositories the sweep has not reconciled since, and pull requests a sweep could not read — hook names only: the rule that lays the line out is written on `coordinator-blind`, on the cell `coordinator-count` and on the reading inside it |
| `coordinator-total-repos`, `coordinator-total-inflight`, `coordinator-total-collisions`, `coordinator-total-plans`, `coordinator-total-counted`, `coordinator-total-cap` | the fleet strip's six cells | which figure each cell is — hook names only, like the two blind cells above |
| `section.coordinator-repo` | the repository's panel | one per repository the config lists, on the frame the redesign gives a panel (§2.3's `section.live-fleet`, the same ground, hairline and radius) |
| `coordinator-repo-head` | `div` | the panel's first element: the name and the source line on one line, which folds rather than shrinking either |
| `h2.coordinator-repo-name` | `h2` | the repository's own name, read out of the local config, and the head row's first element |
| `coordinator-repo-state` | `p` | which view the node is: `the last stored view`, or the runs-only fallback |
| `coordinator-repo-stale` | `div` | the sweep's marker as an amber banner, drawn only when the document carries one |
| `coordinator-stale-word`, `coordinator-stale-since`, `coordinator-stale-reason` | `span`s inside it | the introducing word — a filled chip at the head of the line — the stamp and the reason, drawn exactly as stored |
| `coordinator-counts` | `div` | the counter strip: a repository's three counts, between two hairlines |
| `coordinator-count` | `span` | one count's cell, holding the console's word for the count beside the reading |
| `coordinator-count-word`, `coordinator-count-value` | `span`s inside the cell | the console's own word for the count, and the reading — which is the screen's number hook, the class the alignment rule of step 7's section in `flight-deck.css` names, because the figure is drawn in a span of its own and the word beside it is not a figure |
| `coordinator-count-alert` | the collisions cell | the strip's one state: the cell of a repository whose collisions are above zero, drawn as a tinted cell. Selected **without a tag**, because the class arrives through `numberCell`'s `...classes` parameter |
| `coordinator-omitted` | `p` | the bound that left records or collisions out, drawn only when the document says so |
| `coordinator-omitted-word` | `span` | the console's sentence introducing them |
| `coordinator-records`, `coordinator-collisions`, `coordinator-unreads` | `ul`s | a repository's three lists, each drawn only when it holds something |
| `li.coordinator-record` | `li` | one record: a pull request with the runs behind it, or the in-flight runs alone |
| `coordinator-record-head` | `div` | the record's head row, which carries every fact but the runs |
| `coordinator-record-label` | `span` | the console's own name for the record, and the head row's first element |
| `coordinator-record-title` | `span` | the untrusted title whoever opened the pull request wrote |
| `coordinator-record-branch`, `coordinator-record-release` | `span`s | the branch and the release, monospaced like every other key this screen draws |
| `coordinator-record-chip` | `span` | the host's word for the pull request with the console's own beside it: one chip, because the two are one fact |
| `coordinator-record-state`, `coordinator-record-draft` | `span`s inside the chip | the host's state (`open`, `merged`, …) and the console's `draft`, and neither is drawn when the document has none |
| `coordinator-record-flight`, `coordinator-record-noruns` | `span`s | the console's own words: `in flight` or `finished`, and `no run behind it` |
| `coordinator-runs` | `ul` | the runs behind the record, nested under the head row behind the hairline a nested panel is separated with (§3.8), drawn only when the record has a run, and cut at the console's cap |
| `li.coordinator-run` | `li` | one run |
| `coordinator-run-id` | `span` | the run's id |
| `coordinator-run-status`, `coordinator-run-outcome` | `span`s | the record's own words, each drawn only when it wrote one |
| `coordinator-run-declared-word`, `coordinator-run-by` | `span`s | the introducing word and the declaration itself, which is unverified and is never an identity |
| `coordinator-run-open`, `coordinator-run-short` | `span`s | `in flight`, and the store's own admission that the path set is short |
| `li.coordinator-collision` | `li` | one pair of records in flight on the same files or services |
| `coordinator-collision-a`, `coordinator-collision-b` | `span`s | the pair's two labels |
| `coordinator-collision-word` | `span` | the console's sentence between them |
| `coordinator-file`, `coordinator-service` | `span`s | one path and one service of the shared sets, each already cut to the console's cap |
| `coordinator-more` | a `span` in a collision's row, an `li` at the end of a record's runs | how many of a cut list were not drawn |
| `li.coordinator-unread` | `li` | one pull request a sweep could not read |
| `coordinator-unread-word` | `span` | the introducing words, `state unread` |
| `coordinator-unread-number` | `span` | the pull request's number |
| `coordinator-unread-reason`, `coordinator-unread-retry` | `span`s | the reason a sweep gave, and when it may retry |
| `coordinator-unread-carried` | `span` | the console's answer to whether the state shown is the last one a sweep did read |

**This screen emits no `data-*` of its own, so every rule for it is written against a
class.** It holds no control, no state variant and no selection — the one attribute it
stamps is the `data-theme` every screen's root carries — and a rule that wanted a `data-*`
here would be inventing the state it keyed on. Seven of the hooks above are selected **without
a tag**, in two groups. The first four are the screen's own `listCell` and `countCell`
parameters (`coordinator-file`, `coordinator-service` and `coordinator-more`) and
`coordinator-count-alert`, which the strip's call to `numberCell` passes the same way: the
scan that pairs a selector with the tag the renderer draws cannot read a class that arrives
through a parameter, and a bare class selector is the shape `degraded-num` already has. The
other three are K1's own (`coordinator-queue`, `coordinator-totals` and
`coordinator-queue-head`), and the reason is the same one step along: the screen draws its two
new sections and the queue's head through exported constants
(`COORDINATOR_QUEUE_CLASS` and its neighbours), which that scan reads as a parameter too, so
`section.coordinator-queue` would be refused as a tag nothing draws. The three class names are
this screen's own, no rule competes for them and the tag would buy no specificity — the same
ground `coordinator-count-alert` stands on.

**Fifteen hooks the stylesheet does not select**, and §2's rule read the other way:
`coordinator-count-records`, `coordinator-count-inflight`, `coordinator-count-collisions`,
`coordinator-omitted-records` and `coordinator-omitted-collisions` say *which* count each
cell is, and the rules that lay the counts out and colour the sentence that introduces them
are written on the cell, on the reading inside it and on the sentence rather than on the
five. K1's ten join them: `coordinator-queue-part` is the host one read's part of the queue is
filled into, `coordinator-blind-host` is the host the blind-spot line is drawn into, and
`coordinator-blind-stale`, `coordinator-blind-unread` and the fleet strip's six
`coordinator-total-*` hooks say *which* reading each cell is. They are the screen's own
bookkeeping, and a rule that wants one adds it to the table above first.

**Two readings take `--warn` and one takes `--danger`, and each carries its own word**: the
sweep's stale marker and a pull request whose state no sweep could read are `--warn`, whose
role cell is "attention" (§3.2), and a pair of records in flight on the same files or
services — a collision — is `--danger`, drawn both as the strip's marked cell and as the
callout around the row. A set the document's own bound left short is the third reading and
the quietest: it keeps the `--warn` tone §2.11's `graph-not-shown` draws the same fact in.
Every one of them is drawn beside the sentence that says what it is, so the tone is redundant
by construction (§3.5) and this screen adds no arm to the `forced-colors` block: no rule here
depends on colour alone. **No rule paints `--ok`**:
§3.2 licenses that token to two readings, and no repository, record or run on this screen is
either of them.

**The screen claims no age, and the stylesheet does not add one.** The stored view carries
no time by design, so the marker's stamp is drawn as the string the document holds and never
reformatted into a date: the console's own sentence about the time it does not have is the
`coordinator-age` paragraph, and it is drawn as prose.

### 2.13 The launch confirmation window — `console/src/renderer/confirm.html` + `console/src/renderer/confirm.ts`

The modal the main process opens before it spawns a run (`desktop-console.md`, §5.1 rule 11;
V2.5.1a). It shows one launch — the installation, the repository the
launch recorded and the one it converted, the ref, the title and the plan task, the run id,
the command line word by word, the size of the task as submitted and the **whole** task text —
and asks the operator for one of two answers: cancel, which is the default, or go ahead.

| Region | Hook | What it holds |
|---|---|---|
| The task | `console-confirm-task` | the `pre` holding the task text as submitted, drawn in full and bounded, so a long task scrolls inside the box rather than being shortened or pushing the two answers off the window |
| The field list | `console-confirm-fields` | the `dl` holding the `dt`/`dd` field rows, carrying the one rule that wraps a long value inside its `dd` rather than widening the window |
| The answers | `console-confirm-actions` | the row the view appends its two buttons to: the confirming one, and Cancel |

**Its hooks are classes and not the picker's ids**, although its document is hand-written
HTML like the picker's: the scan in `console/test/unit/installations-view.test.ts` reads this
stylesheet for id selectors and holds every one of them to the ids `installations.html`
declares, so a second document's ids in this file would read there as rules for a page that
is not served. The four containers the view looks up (`confirm-status`, `confirm-fields`,
`confirm-task`, `confirm-actions`) are the document's own declarations, and the ids and the
three hooks above are the whole of what the window carries: **no `data-*` of its own, and no
rule here keyed on a theme attribute**, since the window is one surface with one ground. The
field rows are the view's `dt`/`dd` pairs and carry **no hook of their own**: the `dl` that
holds them is `console-confirm-fields`, whose one rule wraps a long value inside its `dd`,
and apart from that they carry only what `base.css`'s reset gives every `dt` and `dd`. A
rule that wanted a hook on a row itself adds it here first.

---

### 2.14 Notices — `console/src/shared/notice.ts` + `console/src/renderer/entry.ts`

**A notice is a card in the flow, never full-bleed.** The version-skew state — the reduced
mode's statement, and the refusal screen — is drawn by one reusable card, `renderNotice`
(`console/src/shared/notice.ts`). That module imports no renderer module: its element and
factory are declared structurally, the same choice `shared/version.ts` makes, so
`src/shared` still depends on no screen, and the module attaches no listener — the entry
that mounts the card is the only place with `nodeOf`, and it is where the disclosure, the
dismissal and the chip are wired. **The two ids the card's ARIA names are its own** (a
per-card number, not a fixed pair): a document may hold more than one card, and two cards
sharing an id would each be named by the other's title and open the other's facts. **The
version-skew notice is the only caller so far**: a second caller is a second row in its own
section, and this is the table its hooks are added to.

| Hook | Element | What it is |
|---|---|---|
| `section.notice` | the card's root | `display: grid` in three columns — the mark, the body, the dismiss button — on `--surface` with a 1 px `--border` edge, `--radius-md` and a 3 px left edge in the severity's colour. The root carries the caller's own extra class beside `notice` (`console-skew`), `aria-labelledby` pointing at the title, and the `data-severity` below |
| `data-severity` | the card's root | `warning`\|`info`, and it is the **tone**: the left edge's colour and the mark's ink. §3.5 is why it is never the only carrier — the mark's shape differs, and a reader who cannot tell amber from blue reads the same card |
| `span.notice-mark` | the mark | decoration (`aria-hidden`), drawn entirely by the stylesheet: a `!` in a triangle for a warning, an `i` in a circle for information. No rule gives it text |
| `div.notice-body` | the card's column | the title, the reason and the disclosure |
| `h1.notice-title` / `h2.notice-title` | the title | the console's own headline (`SKEW_HEADLINES`), one line, `--font-size-base`, semibold, ellipsised. It is an `h1` for the refused notice — the whole document, and its one `h1` — and an `h2` for a card under the window's own heading. It carries `tabindex="-1"` so the chip can put the keyboard on it |
| `p.notice-reason` | the reason | **one plain sentence, in the console's voice** (`NOTICE_REASONS`): what is wrong and what to do about it, with no version number, range or command in it. The technical text is the facts below. The skew notice's paragraph carries `skew-reason` beside it |
| `dl.notice-facts` → `dt`/`dd` | the facts | a real definition list: two columns, `max-content 1fr`. A value is a run of text or a real `ul` inside its `dd` — the withheld views are `li.skew-hidden` with `data-hidden`, and the reset's `list-style: none` does not reach them |
| `dd.notice-version` | a version value | the mono face with tabular figures (§3.7): a version is compared character by character |
| `button.notice-toggle` | the disclosure | `type="button"`, `aria-expanded` and `aria-controls` naming the details region, and its label flips between "Show details" and "Hide details". The region is `hidden` while folded — the state is the attribute, not a class |
| `div.notice-details` | the disclosure region | the `dl.notice-facts`, and the element `aria-controls` names. `hidden` while folded |
| `button.notice-dismiss` | the dismissal | drawn only when the notice is `dismissible`; a visible `×` that is `aria-hidden` and an `aria-label` that says what it does |
| `button.console-reduced-chip` | the header's chip | the reduced mode's standing statement, in the header after `div.console-switcher` and before `nav.console-views`. `type="button"`, `data-severity="warning"` and the headline in `title` |
| `span.console-reduced-chip-reason` | the chip's reason | visually hidden and **not** `aria-hidden`: the chip's visible word is two words long, so the mode's headline ("fugaro version skew: reduced mode") is what a reader who only reaches the chip is owed. `aria-describedby` on the chip names it |
| `hidden` | any of the above | the theme's way of putting a thing away — a folded menu, a folded notice, a dismissed card. The reset maps it to `display: none !important` because it has to outrank the `display` the element's own rule sets |

- **A card, and the one exception.** The card is `margin: var(--space-sm) auto 0` and
  `max-width: var(--page-max)` — centred, inside the window's own edges, and never
  full-bleed. The **refused** notice is the exception and it is not a card at all: it is
  `section.notice:only-child`, the whole document, so it takes the room it is given
  (`flex: 1 1 auto`) and drops the card's margins.
- **The disclosure is one button and one region.** `aria-expanded` and the region's `hidden`
  move together, and the label says which state the button is in. The refused notice has no
  button at all: its details are drawn open (`disclosure: 'expanded'`), because the range and
  the version line are what the operator acts on and folding them behind a button on the only
  screen there is would hide the reason there is no dashboard.
- **The dismissal is the session's and nothing else.** It is the card root's `hidden`
  attribute and no storage, no IPC and no preference: a reload is a fresh window and draws
  the notice again. The card keeps its place in the document — it stays the body's **first
  child** (§4.1) — and clicking dismiss moves focus to the chip. **Escape does not
  dismiss**: the card is not a dialog and has no key handling of its own.
- **The chip is why dismissal is safe.** A reduced console withholds spend, caps and the
  kill controls (§2.9): a window whose nav has no such button and no word about why is
  exactly the state §4.4's first rule refuses. So the chip is drawn for as long as the mode
  is — not for as long as the notice is — and activating it un-hides the notice and moves
  focus to the card's title. In the `ok` mode there is no notice and no chip; in the refused
  mode there is no dashboard and so no chip either.
- **The tone is a shape first.** The mark's glyph and outline differ per severity
  (`!` in a triangle, `i` in a circle), which is §3.5's rule read where it matters most: the
  one card an operator may see before anything else they can act on.

## 3. The theme system

### 3.1 One file, one value set, one vocabulary

Following [the website's own split](../../website/src/styles/tokens.css) — "the palette is
a value set for a fixed vocabulary rather than a second copy of it" — the console gets:

| File | Holds | Contains no |
|---|---|---|
| `console/src/renderer/theme/tokens.css` | the **vocabulary**: spacing steps, radii, type sizes, leading, weights, motion durations, the focus-ring width and offset | colour |
| `console/src/renderer/theme/palette.css` | the **value set**: every colour token, each written once — **as shipped, the dark set alone** (the owner's Q1 ruling of 2026-10-19; §3.3 is the record of the light set, which no task owns) | sizes, spacing, motion |
| `console/src/renderer/theme/base.css` | reset, document, chrome, layout, responsive, focus, forced-colors | colour literals — it reads the tokens |
| `console/src/renderer/theme/screens.css` | the per-screen rules, in the order §2 lists the screens | colour literals |

**One value set ships, and it is the dark one** (the owner's Q1 ruling of 2026-10-19,
[§8](#8-open-for-the-owner)): `palette.css` holds the dark values of §3.2 and nothing else,
and §3.3 is kept as the record of the light set the ruling deferred. *The heading as first
written, kept:* `One file, two value sets, one vocabulary` — the file held a light and a
dark set and a `color-scheme` decided which one was used, the mechanism §3.6 records and
the ruling removed.

The plan's tree carries a `src/renderer/theme/*.css` line per stylesheet, each annotated with
the task that writes it (`docs/plans/2026-10-15-desktop-console.md:120` — the tree as this change
leaves it). This design fills in what each file holds: Task 6.1's **Scope** names
`tokens.css` and `palette.css`, and Tasks 6.3 and 6.4 own `base.css` and `screens.css`.

**The vocabulary** (a 4 px base, mirroring the website so a reader of one knows the other):

```css
:root {
  --space-3xs: 0.125rem; --space-2xs: 0.25rem; --space-xs: 0.5rem;
  --space-sm: 0.75rem;   --space-md: 1rem;     --space-lg: 1.5rem;
  --space-xl: 2rem;      --space-2xl: 3rem;

  --radius-sm: 3px; --radius-md: 6px; --radius-pill: 999px;
  --border-width: 1px; --rule-width: 1px;

  --font-sans: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;

  --font-size-xs: 0.6875rem;  /* 11px — a table's header, a caption      */
  --font-size-sm: 0.75rem;    /* 12px — the dense default: rows, labels  */
  --font-size-base: 0.8125rem;/* 13px — a screen's prose                 */
  --font-size-md: 0.9375rem;  /* 15px — a panel title                    */
  --font-size-lg: 1.125rem;   /* 18px — the screen's h1                  */

  --leading-tight: 1.2; --leading-normal: 1.45;
  --weight-normal: 400; --weight-medium: 500; --weight-semibold: 600;

  --row-height: 24px;         /* §6.1 rule 2, as a token                */
  --row-height-compact: 20px;

  --focus-ring-width: 2px; --focus-ring-offset: 2px;

  --duration-fast: 120ms; --ease-out: cubic-bezier(0.22, 1, 0.36, 1);
}
```

**The very first rule of the font stack is `system-ui`, and no web font is ever
fetched**: the CSP allows `font-src 'self'` and `connect-src 'none'`, and the console is
offline by construction, so a self-hosted face would be a file the bundle ships for no
reason. The website's own note — "the fonts are a system stack … so the page fetches no
font from anywhere" — holds here for a stronger reason.

### 3.2 The dark value set — the console's first screen

Derived from the website's palette in force ([`palette-logo.css`](../../website/src/styles/palette-logo.css),
the logo's sampled hue, [reconciliation §5](../website/redesign/reconciliation.md)). The
fourteen rows below are accounted for exactly, and which of them is new is a thing a reader
should be able to check rather than assume:

- **Seven reuse a value the website already ships, byte for byte**: `--ground` `#0A0E16`,
  `--surface` `#121A26`, `--text` `#E6EEF5`, `--text-muted` `#9FB2C4`, `--accent` `#17A8E6`,
  `--border` `#24303F`, and `--warn` `#F5A524`. The amber is the one that is easy to get
  wrong: it is the site's, not this design's invention, and it is reused precisely because
  "attention" means the same thing on both sides of the product.
- **One reuses a value under a different name**: the website's `--accent-on-dark` `#6FD8F5`
  is the console's `--accent-ink`. "on-dark" is the site's name and the console calls the
  tone by what it is — the accent as text; *as first written, "The console has two grounds,
  so 'on-dark' would name the wrong one of them", which was the light set's reason (the
  owner's Q1 ruling of 2026-10-19 leaves the one ground below, and the rename stands on the
  name alone).*
- **Six are invented here**, because the website has no equivalent: `--surface-alt`
  `#18222F` and `--border-strong` `#5B7491`, and the four state colours the console needs
  and the site does not — `--ok` `#4CC38A`, `--error` `#F2648C`, `--halted` `#C9A0FF`,
  `--stale` `#8A9AAB`.

Seven plus one plus six is the fourteen rows below. Everything the website already names
keeps **its exact value**, so the console and the site are one product; and of the six that
are new, the four state colours are the only ones a later change can move without touching
the site at all. One of them did move, and [§3.4](#34-contrast-computed) is where and why.

| Token | Value | Hue | Role |
|---|---|---|---|
| `--ground` | `#0A0E16` | 220° | the window's ground |
| `--surface` | `#121A26` | 216° | a panel, a card, the installation rail's ground (§4.1) |
| `--surface-alt` | `#18222F` | 214° | a table header, a zebra row, the rail's selected row |
| `--text` | `#E6EEF5` | 208° | body text |
| `--text-muted` | `#9FB2C4` | 209° | labels, secondary prose |
| `--accent` | `#17A8E6` | 198° | structure: rules, the focus ring, a filled control, the cap bar below its threshold |
| `--accent-ink` | `#6FD8F5` | 193° | the accent **as text** — links, an accent heading |
| `--border` | `#24303F` | 213° | a **decorative** hairline |
| `--border-strong` | `#5B7491` | 212° | a border that marks a **control's extent** |
| `--ok` | `#4CC38A` | 151° | measured and inside the cap; the rail's `running` row (§4.1) |
| `--warn` | `#F5A524` | 37° | attention: near deadline, silent, a cap approaching |
| `--error` | `#F2648C` | 343° | failed, lost, an infra error |
| `--halted` | `#C9A0FF` | 266° | killed — a **deliberate stop**, not a failure |
| `--stale` | `#8A9AAB` | 211° | the last-known values after the feed stopped |

### 3.3 The light value set — the record of the set the ruling deferred

**This section is the record of the light value set, and no task owns it** (the owner's Q1
ruling of 2026-10-19: option (b), dark only — [§8](#8-open-for-the-owner)). Nothing below
ships; it is kept because a later ruling for a light theme should be a change of values
against it rather than a redesign.

*As designed, when the console was to ship both sets:* **the light value set — the same
hue, darkened for a light ground.** The website has no light ground ("the site has one, dark, appearance, and the brief does
not ask for a second", RF19), so this is the first light set in the product and it is
derived from the same sampled hue: the accent moves from 198° to **199.6°** and is darkened
until it clears 4.5:1 as text and 3:1 as a boundary on **each of the three light surfaces**,
not on "a light ground" in general, because the three do not have the same luminance.

**The figure the accent step answers, on the right background.** `#17A8E6` on **white** is
**2.70:1** — the figure [reconciliation §5.3](../website/redesign/reconciliation.md)
records, and it records it for white exactly: the site has no light ground, so white is the
only light surface the accent was ever measured against, and the note it adds — "the accent
is never text on a light ground" — is a note about the site. On **this set's own ground**
`#F2F6FA` the same value is **2.49:1**, and on `--surface-alt` `#E7EEF5` it is **2.31:1**.
2.49 is therefore the number this design has to answer for, and 2.70 is cited here only as
the white figure it is. None of the three clears any text requirement, which is why light
needs its own accent step rather than the same value:

- **`--accent` `#0B6E9E`** is the light accent: **5.17:1** on `--ground`, **5.62:1** on
  `--surface`, **4.80:1** on `--surface-alt`. It clears 4.5:1 as text on all three and 3:1
  as a boundary, so **on a light ground the accent may be a rule, a focus ring, a filled
  control's background and text**: unlike the dark set's, it does not *depend* on a second
  tone to be legible as text.
- **`--accent-ink` `#085A82`** is nevertheless the tone for accent text inside a hovered or
  tinted control: **6.92 / 7.51 / 6.42**. It exists for the same reason the pair exists on
  the dark set — a fill and a text tone carry different contrast requirements — and for one
  more: `--accent` on `--surface-alt` clears 4.5:1 by 0.30, and a hovered row is exactly
  where the background stops being the value §3.4 measured. The rows of that table are
  computed on the surface, not on the hover. The roles are not mirrored between the sets:
  on the dark set the ink is the *lighter* tone, on the light set the *darker* one.
  *The owner's Q1 ruling of 2026-10-19 left this set unshipped — this paragraph is the
  record of it, and no task reads it.*

| Token | Value | Hue | Role |
|---|---|---|---|
| `--ground` | `#F2F6FA` | 210° | the window's ground (a blue-tinted paper, not white) |
| `--surface` | `#FFFFFF` | — | a panel, the installation rail's ground (§4.1) |
| `--surface-alt` | `#E7EEF5` | 210° | a table header, a zebra row, the rail's selected row |
| `--text` | `#0B1219` | 210° | body text |
| `--text-muted` | `#516274` | 211° | labels, secondary prose |
| `--accent` | `#0B6E9E` | 200° | structure, a filled control |
| `--accent-ink` | `#085A82` | 200° | the accent as text |
| `--border` | `#D5DFE9` | 210° | a decorative hairline |
| `--border-strong` | `#6F8497` | 209° | a control's extent |
| `--ok` | `#0F7A4D` | 155° | |
| `--warn` | `#8A5600` | 37° | |
| `--error` | `#B01F45` | 344° | |
| `--halted` | `#7A3E9D` | 278° | |
| `--stale` | `#5C6B7A` | 210° | |

### 3.4 Contrast, computed

**The dark numbers below are the ones that ship; every light column, and every phrase
below that speaks of "the light set", "both sets" or "either set", is the arithmetic of the
deferred set that §3.3 records** (the owner's Q1 ruling of 2026-10-19: one value set, dark
— [§8](#8-open-for-the-owner)). They are kept because a later ruling for a light theme
should be measured against them rather than computed again. Every ratio was computed from the sRGB relative-luminance definition
(`(L1+0.05)/(L2+0.05)`) over the literal values in §3.2 and §3.3, by the same arithmetic
`website/test/a11y.mjs` uses. **Requirements: 4.5:1 for body-size text, 3:1 for large text
and for any boundary that marks a control's extent.**

**Dark**

| Pair | `--ground` | `--surface` | `--surface-alt` | Needs |
|---|---|---|---|---|
| `--text` | **16.47** | **14.91** | **13.69** | 4.5 ✓ |
| `--text-muted` | **8.87** | **8.02** | **7.37** | 4.5 ✓ |
| `--accent-ink` | **11.79** | **10.67** | **9.79** | 4.5 ✓ |
| `--accent` | **7.16** | **6.48** | **5.94** | 4.5 ✓ (and 3 ✓ as a UI boundary) |
| `--border-strong` | **4.00** | **3.62** | **3.33** | 3 ✓ |
| `--ok` | **8.72** | **7.89** | **7.24** | 4.5 ✓ |
| `--warn` | **9.46** | **8.56** | **7.86** | 4.5 ✓ |
| `--error` | **6.42** | **5.81** | **5.33** | 4.5 ✓ |
| `--halted` | **9.16** | **8.29** | **7.61** | 4.5 ✓ |
| `--stale` | **6.71** | **6.07** | **5.57** | 4.5 ✓ |
| `--border` (decorative) | 1.44 | 1.31 | 1.20 | **not admissible as a control boundary** — 1 px hairlines only |

**Light**

| Pair | `--ground` | `--surface` | `--surface-alt` | Needs |
|---|---|---|---|---|
| `--text` | **17.35** | **18.84** | **16.10** | 4.5 ✓ |
| `--text-muted` | **5.77** | **6.27** | **5.36** | 4.5 ✓ |
| `--accent-ink` | **6.92** | **7.51** | **6.42** | 4.5 ✓ |
| `--accent` | **5.17** | **5.62** | **4.80** | 4.5 ✓ (and 3 ✓ as a UI boundary) |
| `--border-strong` | **3.57** | **3.87** | **3.31** | 3 ✓ |
| `--ok` | **4.94** | **5.37** | **4.59** | 4.5 ✓ |
| `--warn` | **5.67** | **6.16** | **5.26** | 4.5 ✓ |
| `--error` | **6.18** | **6.71** | **5.74** | 4.5 ✓ |
| `--halted` | **6.44** | **6.99** | **5.98** | 4.5 ✓ |
| `--stale` | **5.04** | **5.47** | **4.67** | 4.5 ✓ |
| `--border` (decorative) | 1.24 | 1.35 | 1.15 | 1 px hairlines only |

**A label on a filled control** (a filled button, a state chip, a kill banner's fill)
carries `--ink-on-fill`, which is **`--ground` on the dark set that ships** and `--surface`
on the light set §3.3 records — light-on-dark and dark-on-light, as the two sets would have
been inverses:

| Fill | Dark: `--ground` on it | Light: `--surface` on it | Needs |
|---|---|---|---|
| `--accent` | **7.16** | **5.62** | 4.5 ✓ |
| `--ok` | **8.72** | **5.37** | 4.5 ✓ |
| `--warn` | **9.46** | **6.16** | 4.5 ✓ |
| `--error` | **6.42** | **6.71** | 4.5 ✓ |
| `--halted` | **9.16** | **6.99** | 4.5 ✓ |
| `--stale` | **6.71** | **5.47** | 4.5 ✓ |

The website's `--warn`/`--accent` pair is preserved: `--warn` is 37° and `--accent` 198°,
**161° apart**, "so the two can never be mistaken for each other".

**State separation, computed.** Hue is the second channel that matters here, and the
distance between two states in it is computed below rather than asserted — the earlier
revision of this section asserted a minimum, and the assertion was false in both sets (the
dark set's `--warn`/`--error` pair was **37.0°** apart, its `--halted`/`--stale` pair
**55.0°**, and the light set's `--warn`/`--error` pair **39.7°**). What this design holds
instead is a rule and a number (the light set's figures are the deferred set's, which the
owner's Q1 ruling of 2026-10-19 left unshipped — §3.3 is its record — and the dark set's
are the ones that ship):

> **Any two of the five state colours are at least 50° apart in hue, _or_ at least 3:1
> apart in luminance contrast.** The first is what the five present colours meet; the
> second is the fallback for a future pair that cannot clear 50° — the amber-to-red arc is
> the crowded one — and it is the same 3:1 [§4.4](#44-forced-colors-and-high-contrast)
> leans on. **A pair that clears the first need not clear the second**, and none of the
> ten below does: the luminance column is there to carry a pair that falls back, not to be
> a second constraint on a pair that does not. In fact no pair of the five present colours
> comes within a mile of it — the widest luminance gap in either set is **1.47** (dark,
> `--warn`/`--error`) and **1.30** (light) — because every state colour has to clear 4.5:1
> as text on all three surfaces, which holds all five in one narrow luminance band. The
> fallback is reachable only by a pair at both extremes of what that band allows: one at
> the 4.5:1 floor, the other near white. It is stated for the token that has to leave the
> band, not for these five.

50° is chosen as the point where two hues stop reading as "the same colour, shaded"; it is
satisfiable without moving `--warn`, which is reused verbatim from the website, and it
leaves the primary defence where [§3.5](#35-never-colour-alone) puts it: the word and the
icon every state carries. This table is the secondary defence — the thing that holds when a
reader has the glyph and the fill and no label, or reads the two fills side by side in a
chart legend.

**One pair failed that rule when it was first computed, and the token moved rather than the
rule.** `--error` was `#FF6B6B` at **0.0°**, 37.0° from `--warn`'s 37.0° in the dark set,
and `#C0272D` at **357.6°**, 39.7° from `--warn`'s 37.4° in the light set — inside the 50°
minimum in both, and at a luminance contrast of **1.36** and **1.05**, so neither channel
separated them and no amount of marker text would have made the two *fills* tell apart.
`--error` is now **`#F2648C` (343.1°) in the dark set** and **`#B01F45` (344.3°)** in the
light set, **53.9°** and **53.1°** from `--warn`, and every pair below clears 50°. The hue
stays in the red family — a crimson rather than a fire-engine red — which is what `--error`
means: failed, lost, an infrastructure error, distinct from the deliberate stop that is
`--halted`. Nothing else in either set moved.

The two sets did **not** share their hues closely enough for one table to serve both: the
light set's `--halted` is 278° where the dark set's is 266°, 12° away, and its `--ok` is
3.5° away, so both sets are tabulated here — **the dark table is the one that ships, and
the light table is the deferred set's (§3.3, the owner's Q1 ruling of 2026-10-19).**

**Dark: the ten pairs of state colours**

| Pair | Hue apart | Luminance contrast | Separated by |
|---|---|---|---|
| `--ok` / `--warn` | 114.2° | 1.09 | hue |
| `--ok` / `--error` | 168.2° | 1.36 | hue |
| `--ok` / `--halted` | 114.6° | 1.05 | hue |
| `--ok` / `--stale` | 59.6° | 1.30 | hue |
| `--warn` / `--error` | 53.9° | 1.47 | hue |
| `--warn` / `--halted` | 131.1° | 1.03 | hue |
| `--warn` / `--stale` | 173.9° | 1.41 | hue |
| `--error` / `--halted` | 77.2° | 1.43 | hue |
| `--error` / `--stale` | 132.2° | 1.04 | hue |
| `--halted` / `--stale` | 55.0° | 1.37 | hue |

**Light: the ten pairs of state colours**

| Pair | Hue apart | Luminance contrast | Separated by |
|---|---|---|---|
| `--ok` / `--warn` | 117.4° | 1.15 | hue |
| `--ok` / `--error` | 170.5° | 1.25 | hue |
| `--ok` / `--halted` | 123.1° | 1.30 | hue |
| `--ok` / `--stale` | 55.2° | 1.02 | hue |
| `--warn` / `--error` | 53.1° | 1.09 | hue |
| `--warn` / `--halted` | 119.5° | 1.14 | hue |
| `--warn` / `--stale` | 172.6° | 1.13 | hue |
| `--error` / `--halted` | 66.4° | 1.04 | hue |
| `--error` / `--stale` | 134.3° | 1.23 | hue |
| `--halted` / `--stale` | 67.9° | 1.28 | hue |

Circular hue distance is `min(|Δh|, 360°−|Δh|)` over the HSL hue of the literal values in
§3.2 and §3.3, and the luminance column is the same `(L1+0.05)/(L2+0.05)` as every other
table in this section, taken **between the two states** rather than against a surface. The
reading a reviewer should check rather than take on trust: **every pair clears 50°, and the
luminance column never does the work** — no two state fills are told apart by lightness in
either set, and that is deliberate, because lightness is the channel a colour-blind operator
and a forced-colors mode keep, and a state pair that leaned on it would collapse to the same
grey the moment either took it away. The states are told apart by hue, and by the word
[§3.5](#35-never-colour-alone) puts beside each one.

### 3.5 Never colour alone

Design §6.1 rule 5 already requires the screens to be readable for colour-blind users, and
WCAG 1.4.1 requires it regardless. **Every state the stylesheet colours also carries a mark
the code already emits**, and the stylesheet must never be the only carrier of a fact:

| State | The non-colour mark, already in the DOM |
|---|---|
| the rail's three row states (§4.1) | the **word** `running` / `silent` / `idle` beside its glyph `●` / `▲` / `○`, both in `span.console-rail-state`; the glyph alone is what the collapsed rail draws, and the row's accessible name then carries the word, so the tone is never the only carrier in either state |
| killed / halted | the banner's **words**: `KILLED by <who> <when> "<reason>"` / `HALTED` |
| stale | the banner's sentence, naming the 45 s threshold, plus `last read <at>` |
| silent / near / over deadline | the flag's **text** (`silent`, `near deadline`, …) in `span.live-run-flag` |
| lost / queued | the state chip's **word** (`lost`, `queued`) in `span.live-run-chip`, with the flag's reason in its `title` — the word is the mark, and the chip's tone is never the only carrier |
| stuck | the flag text plus the all-caps row treatment |
| gap | the mark `—` in the cell and a `title` |
| unavailable | the mark `n/a` and a `title` |
| notional / estimated | the `~$` / `est.` prefix **in the number itself** |
| partial | the `(partial)` marker text |
| degraded / polling | the words `degraded` and `polling` |
| cap near / over | the **number**: the percent reading and the repository bar's `data-fill`, and, on the fleet budget panel, the cap, the legend's figures and the full-cap banner's own sentence. On Spend, the caps panel's **word** `at cap` in `span.spend-cap-at-cap`, drawn with the figures it was measured from — the row's `headroom`, and the `counted` dollars and the `daily cap` where the repository has a cap of its own — beside the bar and never instead of them |
| not-built | the panel's own sentence |
| a run's rigor mode | the mode's own **word** — `POC`, `Startup`, `Pro`, `Critical`, `Hotfix` — in the chip on a Live row (§2.3), the MODE cell on Projects (§2.5) and the Rigor row on the run detail (§2.6). **This mark carries no colour at all**, and the row is here to say so: the five modes have no colour of their own in this tree, so the word is the whole of the mark and a rule must not paint the modes apart |

The rules this makes binding on the stylesheet: (1) **a colour never replaces a word**;
(2) a state colour is used on a **fill, a rule or a badge**, and the badge keeps its text;
(3) the one state that was a colour-only reading — a cap bar past its threshold — is
always drawn beside the numbers it is made of and, on Spend's caps panel, beside its own
word: the percent reading on Live, and the row's `headroom`, its counted dollars, its cap and
the word `at cap` on Spend, so the colour is redundant by construction;
(4) `--ok` is used for *measured and inside the cap* **and for the rail's `running` row** —
the installation the console is reading now, whose word is drawn beside its dot (§4.1), so
the tone is still a state and not decoration — and never decoratively, per §6.1 rule 3
("never decoratively"). *The rail's `running` is the second reading `--ok` carries, and it
was added to this rule and to §3.2's role cell in the same edit that added the rail: a state
word drawn in a tone whose stated role it does not have is a rule an implementer cannot
justify against this document.*

**This list is the primary defence; the separation table in [§3.4](#34-contrast-computed)
is the secondary one.** The rule is not that the states are far apart in hue — an earlier
revision of §3.4 said so, and the arithmetic did not support it, which is a reminder that a
spacing claim is only worth the number printed beside it. The rule is that **no state is
ever told apart by colour alone**: every state above carries a word, a mark or a number the
code already emits, so the design survives a reader who sees no hue difference at all. The
hue spacing is what keeps two *unlabelled* fills from colliding when they are compared
side by side — a chart legend, a filled button row — and it is stated as a computed minimum
there because that is the only way to state it honestly.

### 3.6 How the theme is chosen

**The owner's Q1 ruling of 2026-10-19 supersedes the mechanism below, and what ships is one
declaration.** The console ships one value set, dark: `:root { color-scheme: dark; }` in
`palette.css`, with the design's `:root, [data-theme]` pair kept around it so a later light
set would be a change of values rather than a restructuring. **There is no switch and no
script**: `light-dark()`, `prefers-color-scheme`, the OS-driven `light dark` pair and a
`[data-theme='light']` block appear nowhere in the theme directory, and
`console/test/unit/theme-tokens.test.ts` (Task 6.1) fails by name on each of the four, in
either letter case and either quote style. The three sources below, the code block and the
`light-dark()` paragraph are **the record of option (a)**, which the owner did not take; a
task that wants a light theme again needs a new ruling and the case above changed with it.
*As designed, before the ruling:*

Three sources, in this order, and **one switch**:

1. **The operating system, by default.** No script is involved: `:root` carries
   `color-scheme: light dark`, which is the browser's own "follow the OS" value, and
   `prefers-color-scheme` resolves against it.
2. **An explicit `data-theme` attribute**, which the screens *already emit* on their own
   roots. The attribute is the single switch: `[data-theme='light'] { color-scheme: light }`
   and `[data-theme='dark'] { color-scheme: dark }`.
3. **The owner's override**, if the owner wants one ([Q2](#8-open-for-the-owner)), which
   *writes that same attribute* (or, equivalently, `nativeTheme.themeSource` in the main
   process). Nothing in the CSS changes for it.
   *These three sources are the two-set design the owner's Q1 ruling of 2026-10-19 removed;
   they are kept as the record of it, and the ruled console reads no source at all.*

The palette is written **once per token** with `light-dark()`, which reads the *used*
`color-scheme` of the element it is declared on — *the two-set design the owner's Q1 ruling
of 2026-10-19 removed, kept as the record of it:*

```css
/* The two-set form, removed by the owner's Q1 ruling of 2026-10-19. */
:root { color-scheme: light dark; }          /* the OS decides, until told otherwise   */
[data-theme='light'] { color-scheme: light; } /* a screen, a mount, the document root   */
[data-theme='dark']  { color-scheme: dark; }

/* The two-set form, removed by the owner's Q1 ruling of 2026-10-19. */
:root, [data-theme] {                          /* declared on both, so a mount can switch */
  --ground: light-dark(#F2F6FA, #0A0E16);
  --surface: light-dark(#FFFFFF, #121A26);
  /* … every token of §3.2 and §3.3, once … */
}
```

The `:root, [data-theme]` pair is load-bearing and not decoration: a custom property is
**computed where it is declared**, so a token declared only on `:root` would inherit an
already-resolved colour into a mount that switched, and the light golden frame would be a
picture of the dark theme. Declaring the set on `[data-theme]` too makes the switch work at
any depth. `light-dark()` needs Chromium 123; the console pins Electron 45.0.0
(`console/package.json`), which is far past it — and the fallback, if the owner prefers no
modern function, is two explicit blocks selected by attribute, with the token test
asserting both blocks name exactly the same vocabulary (see [§9](#9-alternatives-considered)).
**The owner's Q1 ruling of 2026-10-19 removed all of this**: `palette.css` sets
`color-scheme: dark` outright, the pair is kept so a later light set would be a change of
values rather than a restructuring, and the token test fails by name on each spelling above.

**What each document gets, then:**

| Document | Script | How it is themed |
|---|---|---|
| `index.html` (dashboard) | `console.js` | the entry puts `data-theme` on the mount it draws the chrome into, so the nav, the installation line and the panel all switch together; each screen additionally stamps its own root (as it does today) |
| `installations.html` (picker) | `installations.js` | the picker's script stamps `document.documentElement` (or its `<main>`); its CSS is id-scoped |
| `refusal.html` | **none** | `:root`'s `color-scheme` alone — **nothing decides anything, and no code runs.** As shipped under the Q1 ruling that is `dark` outright; the pre-ruling form of this row said `light dark` and was the strongest argument for making the OS the default rather than a stored preference, since the one document the console shows when nothing else works must be themed without a script |

**Why the entry and not the screens decide.** Today the entry names one theme for every
drawing (`CONSOLE_THEME`, and `renderLive(…, theme)` and its three siblings take it as an
argument). That stays: the *argument* is what a golden frame records, and a screen never
reads the environment. **Under the Q1 ruling `CONSOLE_THEME` keeps the value it has today**
— the literal `'dark'`, which is the one set that ships — and no `matchMedia` read, no
`change` subscription and no `nativeTheme` query comes with it. *What the two-set design
would have changed in [Task 6.5](#75-task-65--the-theme-switch-the-window-background-and-the-keyboard):
`CONSOLE_THEME` becomes the answer of a small `themeChoice()` that reads
`matchMedia('(prefers-color-scheme: dark)')` and subscribes to its `change` event — a
`matchMedia` read is neither a network call nor a style write, so the CSP is untouched and
the `ElementLike` interface is untouched (a media query is not a DOM write).

### 3.7 Typography

| Role | Size | Weight | Family |
|---|---|---|---|
| Screen `h1` (`live-project`, `spend-project`, `projects-project`) | `--font-size-lg` (18px) | 600 | sans |
| Panel / section title (`projects-title`, `run-panel-title`, `spend-account-title`) | `--font-size-md` (15px) | 600 | sans |
| Table header (`projects-repos-label`, `run-log-time`, a column caption) | `--font-size-xs` (11px), uppercase, letter-spaced | 500 | sans |
| Row text, labels, controls | `--font-size-sm` (12px) — the dense default | 400 | sans |
| Screen prose (`live-note`, `spend-note`, `spend-warning`, `projects-because`, `run-warning`, and the two Projects note hooks — `projects-note`, `projects-accounts-note`, §2.5) | `--font-size-base` (13px) | 400 | sans |
| **Every number, id, slug, path, timestamp and log line** | `--font-size-sm` | 400 | `--font-mono` with `font-variant-numeric: tabular-nums` |

Money is always tabular and right-aligned, in mono, with the `$` inside the same cell —
§6.1 rule 2's "no whitespace padding between related figures". A run id, a repository slug,
a secret name and a log line are mono too, because their value is that they can be compared
character by character; prose never is.

### 3.8 Spacing and density

`--row-height: 24px` is the design's own number (§6.1 rule 2) and is a token, so a rule
that wants a different row height reads a token or an explicit multiple of this one rather
than a new number — and the two directions are not the same token: **a shorter row says
`--row-height-compact`** (`20px`, §3.1: four pixels shorter, for a caption row or a dense
control), while **a taller row says an explicit multiple** (`calc(2 * var(--row-height))`
for a row that carries two lines), because no taller token exists and the compact one is
shorter, not taller. The density rules the stylesheet keeps:

- a table row is `--row-height` (24 px) with `--space-2xs` vertical padding, no horizontal
  padding between related figures, and one hairline between rows;
- a panel is separated from the next by `--space-lg`; a panel's own padding is
  `--space-sm`;
- a screen's `h1` block has `--space-md` below it and nothing above;
- **figures that are read together sit together**: a cap's counted figure, its cap and its
  percent are one cell group, never three columns with gaps.

### 3.9 Motion

Motion is available and almost unused: `--duration-fast` (120 ms) with `--ease-out` for a
hover/focus transition and for a nav button's state change, and **nothing else**. The one
rule that matters:

```css
*, *::before, *::after { animation-duration: 0.01ms !important; animation-iteration-count: 1 !important;
                         transition-duration: 0.01ms !important; scroll-behavior: auto !important; }
```

inside `@media (prefers-reduced-motion: reduce)`. This is written **once**, globally, in
`base.css`, the way the website's `Base.astro` writes its own reduce rule once
([reconciliation RF17](../website/redesign/reconciliation.md)) "so a new component cannot
forget it". The console animates no chart, no sparkline and no number — a figure that
animated would be a figure whose last frame is a lie about when the value was read.

**Amendment, 2026-10-24: one screen animates, and this section carries its states and their static styles.** The owner's console dependency graph spec of 2026-10-22 asks for a moving graph — a light streak circling a running node, a glow behind the critical ones, a flow along the critical path's edges — and the owner's answer GD4 of that day, confirmed by the reconciliation's ruling (b) ([reconciliation](../console-dependency-graph/reconciliation.md)), amends this section **for that screen alone**: the graph screen (GV2.3, GV2.4 in [the plan](../console-dependency-graph/implementation-plan.md)) may animate, and everything above stands unchanged for every other screen. The exception is bounded by four rules, and a reader should be able to check a build against them:

- **The animation belongs to the graph and to nothing else.** Three keyframes, no more — the one exception the later amendment below adds is the busy mark's single turn: a **comet** on a node that is running, a **pulse** on a critical node, and a **flow** along an edge of the critical path. No other screen gains one, and **no figure animates anywhere** — which is the paragraph above, kept: a number whose last frame is stale is a number that lies. The test that matters here is a negative one: no animation outside the graph screen.
- **Every animated state carries its word.** §3.5's rule, unchanged and load-bearing on this screen: the states are `implementing`, `in review`, `fixing`, `queued`, `waiting`, `halted`, `stuck`, `blocked`, `ready`, `done` and `critical`, each drawn as text on the node with its colour second. A reader who cannot tell two colours apart reads the same graph, and a screenshot read in greyscale is still a graph.
- **Reduced motion gets a static style per state, not the global freeze.** The rule above *freezes* an animation rather than restyling it: `animation-duration: 0.01ms` with one iteration leaves a comet wherever it stopped, which is a picture of something that is not happening, and the same freeze is the whole of what `base.css` does (F36 in the reconciliation is where that gap was found). So the graph screen writes **one explicit static style per animated state** under its own `@media (prefers-reduced-motion: reduce)` block in `console/src/renderer/theme/screens.css`, each keeping its word above it, and the global freeze stays exactly where it is.
- **Forced colours draw no animation at all** (§4.4): under `forced-colors: active` every state is its mark and its border, the same drawing the reduced-motion arm leaves behind.

**Amendment, 2026-10-31: the busy mark's one turn, and nothing else moves.** The owner's rule of 2026-10-31 — "we need to render parts of screen as soon as we get that data, not wait for it all before we show anything" — needs the other half of the same complaint: a control change used to look like nothing had happened, and a section whose read has not answered has to say so **in the frame the click happened in**. A mark that says "reading" without moving is a mark a reader takes for a stopped console, so the busy mark (`console/src/renderer/section.ts`, drawn into a section's own slot) may draw **one** spinning element, `span.console-busy-spinner` in `flight-deck.css`, turned by **one** keyframe, `console-busy-turn`, whose only property is `transform`. The count above therefore becomes four. It is not a figure and not a state of a screen: it carries no number and no word about the data, it is drawn only while a read is in flight, and it is the only motion the console has outside the graph screen. Its two arms follow the rules above rather than restating them: under `prefers-reduced-motion: reduce` the element is **not drawn at all** (`display: none`, with the animation stopped on that same selector) and the static word `span.console-busy-still-mark` is shown in its place, because the global freeze would leave a ring wherever its last frame put it — a picture of half a circle rather than of reading; under `forced-colors: active` it declares no animation at all.

**Amendment, 2026-11-01: the skeleton's one shimmer.** The owner, 2026-10-31: "when changing tabs in the Console we should show a skeleton while we load the data, no?" A skeleton of still blocks is a panel that may as well be a picture of a panel, so a block that is waiting on a read may carry **one** shimmer: `div.console-skeleton-block` in `flight-deck.css`, swept by **one** keyframe, `console-skeleton-shimmer`, whose only property is `background-position` over a gradient of two steps of the vocabulary. The count above therefore becomes five. It is bounded exactly as the turn before it is: one element and one keyframe; it carries no figure, no word and no state (the whole skeleton tree is `aria-hidden`, §2's fourth fact); it is drawn only while a read is in flight; and it is not a state of a screen. Its two arms are §3.9's rules rather than exceptions to them: under `prefers-reduced-motion: reduce` the blocks are **static** — the animation is stopped on the selector that started it and the sweep is taken off, because the global freeze would otherwise leave the gradient wherever the sweep stopped, which is a picture of a half-loaded block — and under `forced-colors: active` they declare **no animation at all** and are drawn as the system's own outline (§4.4).

**What this amendment does not change.** The 120 ms `--duration-fast` hover and focus transitions are still the only motion on every other screen, the reduce rule is still written once in `base.css` and still applies to everything this screen does not restate, and the graph screen still takes its colours and its spacing from the shared token layer — which is why this is an amendment to the motion rule and to nothing else in this design.

---

## 4. Layout

### 4.1 The window's structure

`CONSOLE_WINDOW_SIZE` is `{ width: 1180, height: 780 }` CSS pixels
(`src/main/window.ts`) and the window sets **no background colour at all** — there is no
`backgroundColor` key in `consoleWindowOptions`. That is one wiring detail this design
proposes to change, and it is *not* a CSS change: with no `backgroundColor`, a theme's
window flashes the platform's default (usually white) before the document paints.

**Under the owner's Q1 ruling of 2026-10-19 the window paints one ground, and it is
`#0A0E16`.** One value set ships, so `--ground` has one value, a single literal is right on
every machine, and **no `nativeTheme` read and no `'updated'` listener comes with it** — the
proposal that needed them was the two-set one. The token test therefore asserts that one
literal against the shipped `--ground` (Task 6.5), not the pair below. *Which ground the
window paints, as the two-set design decided it and kept as the record of option (a):* it
said "equal to `--ground`", and with `--ground` carrying **two** values, `#0A0E16` and
`#F2F6FA`, and §3.6 making the OS the default, a single pinned literal would have been wrong
on one of the two machines — pinning the dark ground alone being the worse of the two errors,
because on a light-OS machine it would introduce the very flash §4.1 exists to remove,
**dark** where the document is about to paint light.

So the two-set design had the window paint **the ground of the theme it is about to show**,
with the main process reading which that is from `nativeTheme`:

- `nativeTheme.shouldUseDarkColors` → `#0A0E16` (the dark `--ground`), otherwise
  `#F2F6FA` (the light one);
- `nativeTheme.on('updated', …)` re-sets `backgroundColor`, so an OS appearance change
  while the window is open does not leave a stale ground behind the next paint;
- the sources in [§3.6](#36-how-the-theme-is-chosen) are the same order here, because they
  are the same fact: `nativeTheme.themeSource` is what an owner override sets (source 3),
  `shouldUseDarkColors` is its answer, and the renderer's `matchMedia` read (Task 6.5) is
  asking the same question of the same preference. The `data-theme` attribute is still the
  single switch for the CSS.
  *These three bullets are the two-set design's, and the owner's Q1 ruling of 2026-10-19
  removed them: none of `nativeTheme`, `shouldUseDarkColors` or `themeSource` is read.*

`nativeTheme` is core Electron and needs no dependency and no CSP change. **As shipped under
the ruling none of it is read**: the main process cannot read the CSS, so a background
literal is a duplicate of `--ground` however many sets there are, and with one set there is
one duplicate to keep honest — the plan's Task 6.5 carries it, and the token test asserts the
one equality. *The two-set form of the same problem:* **both** literals are duplicates of
the two `--ground` values, and the token test asserts **both** equalities —
the dark literal against `--ground` in the dark set and the light literal against `--ground`
in the light set — rather than the ambiguous "equal to `--ground`" the first revision
wrote.

**The header switcher replaced the rail (owner's ruling of 2026-10-28).** The console
redesign's shell is one header row and a page; its installation switcher in the header
replaces the canvas's left vertical rail of 2026-10-19, and its menu holds what the rail held.
The entry appends up to **three** children to `document.body`, which is the mount
`startConsole` is handed:

```
┌──────────────────────────────────────────────────────────────────────────┐
│ section.console-skew (section.notice)              reduced mode only     │
│   and button.console-reduced-chip, in the header below                    │
├──────────────────────────────────────────────────────────────────────────┤
│ header.console-header   wordmark · switcher · nav · gauge · Halt fleet   │
├──────────────────────────────────────────────────────────────────────────┤
│ main.console-page                                                        │
│   div.console-panel   the page container, max-width --page-max           │
│     section.live | .graph | .spend | .projects | .coordinator | .run     │
│     | .degraded | .console-unavailable                                   │
└──────────────────────────────────────────────────────────────────────────┘
```

- **The version-skew notice is not in the panel.** `startConsole` appends it to the mount it
  was handed, so the notice is the body's **first child**, appended before the header and the
  page. It is `section.console-skew` — a `section.notice` card (§2.14) — and it carries
  `data-theme`, `data-mode`, `data-skew` and `data-severity` (`shared/version.ts`). A
  dismissed notice keeps that place and takes `hidden`; the header's
  `button.console-reduced-chip` is what says the mode is still reduced (§2.14). In
  **refused** mode it is the whole document, because `bootDashboard` returns straight after
  it and **the other two children are never appended**.
- **The header is chrome and never scrolls**; the page scrolls. It wraps on a narrow window.
- **`nav.console-views` is five tabs** (`button.console-view`, at least 44 px tall; the
  redesign draws links, and the renderer writes no `href`), the
  current one marked with `aria-current="page"` and a 2 px inset `--live` underline. Because
  `aria-current` is the marker, the style rule is `[aria-current='page']` and not a class.
- **A screen's own root is the panel's only child** and carries the screen's `data-theme`,
  so a rule may be written `section.live { … }` or `.live { … }` but never
  `div.console-panel .live` — the panel is chrome and the screen is content.
- The installation line's three states (`chosen`, `none`, `unreadable`) are styled as
  three states, at the top of the switcher's menu, with the CLI's full path under the name.

**The installation rail — now the switcher's menu.** The canvas the owner approved on
2026-10-19 drew a **left vertical rail** (216 px wide, 56 px collapsed); the owner's ruling of
2026-10-28 replaced it with the redesign's header switcher, so the rail is no longer a column
and has no collapse control. What it held is unchanged and is drawn inside
`div.console-switcher-menu`, under the installation line: the rail itself is
`aside.console-rail`, and its tree is **fifteen classed elements**. Those fifteen elements carry
**fourteen classes**: the foot row and its button share `console-rail-add` (a row and the
control inside it are one region, and the class names the region), so a stylesheet selects
fourteen distinct hooks and [§2.1](#21-the-dashboards-chrome--srcrendererentryts) lists the same
fourteen. **The count is stated here and in §2.1 because it is what a reader checks the tree
against**, and the tree below is the list:

```
aside.console-rail                        the rail's tree, inside the switcher's menu
├─ div.console-rail-head                  the header row
│   └─ p.console-rail-title               the word "Installations"
├─ div.console-rail-list  role="tablist"  the installation rows, one tab each
│   └─ button.console-rail-row  role="tab"
│       ├─ div.console-rail-nameline      the name, and the warning mark beside it
│       │   ├─ span.console-rail-name     the installation's name
│       │   └─ span.console-rail-warning  "⚠", role="img", only when it has an issue
│       ├─ span.console-rail-project      its GCP project, in small text
│       ├─ span.console-rail-state        the state glyph **and** the state word
│       └─ span.console-rail-spend        the spend
├─ p.console-rail-empty                   "No installations yet", only when there is no row
├─ div.console-rail-add                   the foot row, with a top divider above it
│   └─ button.console-rail-add            "+ Add installation"
└─ p.console-rail-hint                    "Switch with the shortcut keys 1 2 3"
```

- **The switcher's button** draws a status dot, the installation's name and `CLI <version> ·
  <mode>`. The dot carries the open row's state word as `data-state` and as its accessible
  name, so the menu's state is visible while it is folded.
- **The three state words, each beside its glyph.** The glyph is drawn **and** the word is
  drawn — never one instead of the other ([§3.5](#35-never-colour-alone)):

| Word | Glyph | Tone | What it says |
|---|---|---|---|
| `running` | `●` | `--ok` | the console is reading this installation now |
| `silent` | `▲` | `--warn` | its last read was refused, or its feed has gone quiet |
| `idle` | `○` | `--text-muted` | it is stored, and nothing is being read from it |

- **The selected row** carries `aria-selected="true"` **and** a 3 px `--live` left border;
  the attribute is the machine-readable mark and the border is the drawn one, so the
  selection is not a colour alone either.
- **The console never shows a picker screen** (owner's design ruling of 2026-10-25). Every
  installation is listed in the rail, readable or not, the first readable one is open in the
  main area, and switching is a click on the rail. "+ Add installation" opens the **existing**
  picker window of §2.2 (`installation.open`), which is left for exactly one job: adding an
  installation by checkout path. The console never opens that window by itself.
- **What the rail draws.** The rail draws **one row per installation the CLI listed or this
  console holds, readable or not**: `installation.list` on the dashboard's surface is
  `fugaro config list --json` (`src/main/picker.ts` answers it, the same dispatcher the picker
  window uses), joined with the console's own store. **A row carries four fields and no one
  read answers all four**, so this document names the read behind each rather than implying
  one. The **name and the project** are the list row's `project`: the CLI's installations
  carry **no display name**, so the row's name span draws that label and the small-text
  project span draws the same one. The **state word is the rail's own mark and not a field of
  any read**: the open row says `running`, or `silent` when its read was refused or its feed
  has gone quiet, and every other row is `idle`. The **spend is the console's own budget
  read** — `budget.show` on the dashboard's channel, whose counted and spent dollars §2.4's
  caps panel already draws — for the open row only, and **no field is added to it for the
  rail**.
- **The warning mark, and the issue view.** An installation the console cannot read — its
  config is refused, the saved store is unreadable, the CLI is missing, or its shared config
  is stale — carries `span.console-rail-warning` beside its name: the `⚠` glyph in `--warn`,
  with `role="img"` and the issue's own sentence as its accessible name, so the fact is never
  the tone alone ([§3.5](#35-never-colour-alone)). The main process classifies each row
  (`src/main/installationstate.ts`); a stale row is still readable and still opens. Clicking a
  row with an issue opens that issue **in the main area** (`section.installation-issue`, §2.1):
  **one plain sentence first** — what is wrong and the exact command, `fugaro doctor --project
  <name>` where a command fixes it — the CLI's full text behind a `details` disclosure, and the
  actions the old Installations screen offered (use this installation when it is readable,
  remove from this console, choose a checkout, open the checkout folder). **No banner.** When
  no installation can be read, the console opens on the first row's issue and makes no read.
  The remembered choice is never dropped from the saved store: when it cannot be read the
  console opens the first readable row for this run only, and the remembered row keeps its
  place and its mark. **Removing the open installation rebinds at once**: the open rule
  picks again, a readable next row gets its own handshake and the window reopens on it, an
  unreadable one opens its issue, and with no row left the main area is the onboarding.
- **No installation at all.** The rail draws no row and `p.console-rail-empty` in their
  place, with "+ Add installation" still below it, and the main area is the **onboarding
  view** (`section.installation-onboarding`, §2.1): "Set up your first installation" with
  `fugaro setup`, or "Install the Fugaro CLI first" with the install command, as the list
  answer's `onboard` says. Nothing is read, so no Live refusal is drawn.
- **The console stays READ ONLY.** The rail's writes are the console's own: a click on a
  readable row with no issue switches the console to it (`installation.choose`, which writes
  `installations.json` and reopens the window), and the issue view can remove the console's
  record of an installation or pair it with a checkout. **No renderer names a path it made
  up**: a checkout is accepted only from the directories the rail offers for that installation
  (the ones the CLI listed, plus the console's own recorded checkout), with temporary and
  tool-owned paths (and symlinks into them) refused in the main process as well as left off the
  screen, and the folder the file manager opens is computed from the console's own record.
  Nothing on the rail writes to the cloud or to the CLI's configuration. **Halt fleet**, in
  the header, is drawn as the redesign draws it and performs nothing: `aria-disabled`, the
  title "Not available in this release", and no listener, IPC channel or spawn entry behind it
  (`src/renderer/writes.ts`; owner's ruling of 2026-10-28).
- **The keyboard** — the detail is [§4.3](#43-keyboard-and-focus): the rail's list is **a
  tablist in its own right**, `div.console-rail-list` with `role="tablist"` and one
  `role="tab"` row each; it uses a **roving `tabindex`** (the selected row is `0`, the rest
  `-1`), Arrow Up and Arrow Down move between the rows, **Home** and **End** go to the first
  and the last, and **Enter selects** — the arrow keys move focus and the key that chooses is
  Enter, which is the pattern's manual-activation form; **selecting a row is the click**: it
  switches the console to that installation, or opens its issue, and folds the menu. The
  **shortcut keys** select by position whether the menu is open or folded: **Command-1** to
  **Command-3** on macOS and **Control-1** to **Control-3** elsewhere address the first three
  rows **the rail draws**, which is the hint's own sentence. Escape folds the menu and returns
  the focus to the switcher's button.

**Two things the canvas did not decide, and what this design does with each.**

1. **No alert badge.** The canvas draws the state word and its glyph in **every** row, so a
   `silent` installation — the rail's own attention state — raises no separate badge when it
   is not the selected row, and neither would a fourth word if the rail ever gains one (a
   `halted` installation, say): a badge would be a second mark for a fact already written in
   the row, and a count of them would be a number nobody asked the console for. The warning
   mark (owner's ruling of 2026-10-25) is that shape: a mark on the row beside its name, not a
   badge and not a count.
2. **The shortcut keys and the platform.** They are Command + digit on macOS and Control +
   digit elsewhere, as the bullet above states. macOS reserves neither form inside an
   application window, and **the console has no other keyboard shortcut to collide with**:
   the entry has no keydown handling at all today (§2.1), so these are the console's first
   keyboard shortcuts and there is no binding for them to collide with. A later shortcut
   that wants one of these takes it from here rather than overriding it quietly.

### 4.2 Narrow windows

There is no responsive layer today and the window is fixed at 1180 × 780, but the
operator can resize one, and the content is tables. Three breakpoints, in `base.css` and
`screens.css`, written in `em` against **the root size** — the document sets no root
`font-size`, so the root is the browser's 16 px default and each `em` figure below is that
default's multiple: 1000 ÷ 16 = **62.5em**, 780 ÷ 16 = **48.75em**, 560 ÷ 16 = **35em**.
`em` rather than `px` because a media query in `em` is measured against the *root* size, so
an operator who has raised the default text size gets the narrow layout at the same
*measure of text* rather than at the same pixel width — which is the point of the layout
and is lost the moment a query is written in `px`:

| Width | What changes |
|---|---|
| ≤ 62.5em (1000 px at the 16 px default) | the widest tables (the Log and Jobs tables, `spend-rows`) drop their least load-bearing columns; a repository block's burn sparkline narrows before a figure does |
| ≤ 48.75em (780 px) | the nav wraps to two rows, the installation line drops to project · mode, and any three-column figure group becomes two |
| ≤ 35em (560 px) | the ledger, the caps panel and the panels' source lines stack; **no figure is ever dropped or truncated** — what is dropped is a column, and a dropped column is one whose value is repeated in a `title` |

**The three figures are the window's, and since the header switcher replaced the rail
(2026-10-28) the content column is the whole window**, less the page container's padding. The
rail was a fixed 216 px column, so each layer used to fire with the column 216 px narrower than
the number it names; the switcher's menu overlays the page instead of taking a column from it.
The header wraps by itself; below 35em the page's side padding drops to 16 px.

**The rule that outranks the breakpoints: a figure is never hidden to make a layout fit.**
A dense console that hides a cap on a narrow window is the console lying about spend,
which §6.1 rule 1 forbids more strongly than any layout does.

### 4.3 Keyboard and focus

- **The focus ring** is one rule and it is a token, as on the website:
  `:focus-visible { outline: var(--focus-ring-width) solid var(--accent); outline-offset: var(--focus-ring-offset) }`.
  The accent clears 3:1 against **the ground that ships** (§3.4's dark column; under the
  owner's Q1 ruling of 2026-10-19 there is one), and it did so against both grounds of both
  sets — which is exactly the requirement for a focus indicator.
- **`:focus-visible` and never `:focus`** — a mouse click must not leave a ring on a nav
  button, and a keyboard user must never lose one.
- **The rail's list is the console's roving tabindex.** Task 6.5 gave the nav and the rail's
  list the WAI-ARIA tablist behaviour; since the redesign's header (2026-10-28) the nav is
  five buttons, ordinary tab stops whose Enter is their click, and the rail's list keeps the
  tablist: `role="tablist"` over `div.console-rail-list` and its `role="tab"` rows, one tab
  stop for the whole list rather than one per installation, the arrows move focus and
  **Enter** selects. **It is a renderer change and not a style change** — no attribute it
  writes is a style, so the CSP and `ElementLike` are untouched.
- **The focus order is the DOM order.** A tab through the chrome goes: the wordmark, the
  switcher's button — and, while its menu is open, the menu's rows (one stop) and its add
  row — then the five nav buttons, the gauge's group, Halt fleet, and the page's own controls
  in the order the screen draws them. The shortcut keys exist so that `Ctrl`/`Cmd` + digit
  reaches the first three rows (§4.1) without opening the menu or walking the tab order.
- **A skipped-by-default DOM stays skipped**: the console has no skip link and does not
  need one (there is no repeated block of links before the content), and it must not gain
  a link that would need the allow-list's `openExternal` to do nothing.
- The picker is ordinary HTML (a list of rows, each with `button`s, and the onboarding
  screen's one link `button`), so it is keyboard-operable already; it needs only the ring
  and a visible disabled state, which `installations.ts` sets with `disabled` on every
  control while a request is in flight. There is no `label[for]` and no text input in this
  document any more (the ruling of 2026-10-20).

### 4.4 Forced colors and high contrast

Windows High Contrast and `forced-colors: active` discard author colours. Two rules:

1. **Every state keeps its word** ([§3.5](#35-never-colour-alone)), so nothing becomes
   unreadable when the colours go away. This is the whole reason that section exists.
2. One `@media (forced-colors: active)` block per stylesheet, each in the task that writes
   the rules it defends: `base.css` carries the chrome's — let the system paint its
   backgrounds by setting `background: Canvas` / `color: CanvasText` on the chrome, map the
   current view **and the rail's selected row** to `Highlight`/`HighlightText`, and re-enable
   the border the system collapses — the rail's accent left border is exactly such a border. `screens.css` carries the rows' and badges' — `Canvas`/`CanvasText` on the row
   surfaces, `LinkText`/`GrayText` where a fill was carrying a meaning, and
   `border: 1px solid CanvasText` on a panel, because a hairlines-only table becomes
   structureless without it. Neither block names a colour literal; both read the tokens.

---

## 5. Wiring (SECURITY-SENSITIVE)

This section is the part of the design that touches the **CSP, the protocol handler and
the build**, so it is flagged `SECURITY-SENSITIVE, independent opus review` in the plan
([Task 6.2](#72-task-62--the-wiring--security-sensitive)) and is not merged on a
self-review. Nothing here loosens a policy.

### 5.1 The stylesheet files

The theme directory's whole contents, under `console/src/renderer/theme/` (§3.1), emitted as
**one** served file, `dist/bundle/console.css`, served at `/console.css`. One file and not one
per source file, for the same reason the scripts are bundled: the served root is a flat
allow-list (`SERVED_FILES`), and the handler's refusals are pinned on paths that name **files
under one root**. A second stylesheet would be a second entry in the allow-list for no gain;
a request per theme file to a `app://` handler whose only job is to read a file is that many
times the work for one page paint.

### 5.2 How the bundler emits it

`console/tools/bundle-renderer/index.ts` gains a **stylesheet step**. The bundler itself has
no allow-list to copy, and saying otherwise would send the task looking for a mechanism that
is not there: its shape is an **owned set computed at run time** from what the run writes —
`basename(out)`, the `siblings` (the other bundles) and the served documents it emits — with
a `BundleRefusal` for any file in the output directory that is not in it (`bundleRenderer`,
`index.ts` 291-310). `console.css` needs no new kind of mechanism for that reason: it joins
the owned set, which is what the last bullet below says. The **ordered, frozen** lists in
this area are two, and neither is in the bundler — `SERVED_FILES` (`src/main/protocol.ts`
113), the served root's own list, and the **unit** suite's build helper — `OUTPUTS`
(`test/support/built.ts` 62), the file list the helper's own build produces and the unit
tests read — and the list this step does need is a list of *theme
files* rather than of outputs, so it is stated here rather than borrowed:

```ts
/** THEME_STYLESHEETS is the theme directory's whole contents, in the order the
 * cascade needs: the vocabulary, then the values, then the rules that read them. */
export const THEME_STYLESHEETS: readonly string[] = Object.freeze([
  'tokens.css', 'palette.css',
])
```

**The list grows with the files, one entry per task, and that is forced by the refusal
below rather than chosen for tidiness.** The list is the theme directory's *whole contents*
and the bundler refuses a listed file that is missing, so a task that landed the final
whole-directory list before the last files existed would turn `npm run build` red — the
wiring task cannot list a stylesheet no task has written yet. The list is therefore
introduced by the task that lands the bundler step itself ([Task 6.2](#72-task-62--the-wiring--security-sensitive)),
naming the files the directory already holds — Task 6.1 writes `tokens.css` and `palette.css`
with no list to hold them — and each later task appends its own file in the same commit that
creates it:

| Task | Creates | `THEME_STYLESHEETS` after it |
|---|---|---|
| 6.1 | `tokens.css`, `palette.css` | *(no list yet — nothing reads the directory at this stage; Task 6.2 is the row below)* |
| 6.2 | the bundler step itself | `tokens.css`, `palette.css` |
| 6.3 | `base.css` | `tokens.css`, `palette.css`, `base.css` |
| 6.4 | `screens.css` | `tokens.css`, `palette.css`, `base.css`, `screens.css` |

**Every intermediate state is green**, and each one is a real build: at 6.2 the artefact is
the files that exist at that point, concatenated in list order, and the served root grows a
stylesheet whose only content is the custom properties and `color-scheme` — a file that
paints nothing yet but is served, linked and asserted. What no state has is a listed file
that is absent, or a `.css` in the theme directory that is not listed: the first is the
bundler's own refusal, the second is the same refusal from the other side, and each task
moves both sides together.

- it reads `src/renderer/theme/` and **refuses a `.css` file that is not on the list**
  (`BundleRefusal`, exactly as the served root refuses a foreign file), so a stylesheet
  nobody reviewed cannot be served;
- it refuses a **missing** file on the list, and one that is **not a regular file**, so a
  rename fails the build rather than silently dropping a layer of the cascade — and,
  equally, so a list cannot run ahead of the files it names. The second half is a symlink's:
  a link at a listed name would put bytes the theme directory does not hold into the served
  stylesheet, which is the same failure the stray-`.css` refusal is about from the other
  side;
- it refuses a **backslash**, an **`@import`**, a **quoted string that begins as a URL** and
  a **remote `url()`** in any theme file — the bundle is one file and the console is offline
  (`connect-src 'none'`), so an import would either fail at runtime or be a network call the
  CSP forbids. The backslash is what makes the other three hold: CSS unescapes identifiers
  and strings before anything else sees them, so `@\69mport "./base.css";` is an `@import`
  and `u\72l(...)` is a `url(...)`, and no scan here unescapes. The quoted-string scan is
  the one that catches a URL `image-set()` takes as a **bare string**, which never writes
  `url(`. *(These two bullets and the symlink half of the one above are the review of #694's
  follow-up, 2026-10-19; `console/README.md`'s Task 6.2 section writes out what the step
  refuses and the mutants each refusal is proved against.)*
- it writes through nothing that is a **symbolic link** — neither a listed theme file, nor
  an output file, nor the directory it writes outputs into (`mkdirSync` accepts a symlinked
  directory in silence, so the directory is checked as well as the file);
- it writes `dist/bundle/console.css` = each file's bytes in list order, each preceded by a
  `/* <name> */` banner so a reader of the artefact can see the seam, and the whole file
  preceded by one line naming the build (`/* console.css — generated by
  tools/bundle-renderer; do not edit */`);
- `console.css` joins the **owned** set of the output directory (the `siblings` mechanism
  already exists for exactly this), so the served root's foreign-file refusal keeps
  holding;
- **no CSS is parsed, minified, autoprefixed or rewritten.** The bytes on disk are the
  bytes served, which is what makes the contrast unit test ([§6.3](#63-contrast-as-unit-tests))
  meaningful: it reads the same literals the window paints.

### 5.3 The one new entry in `SERVED_FILES`

```ts
export const SERVED_FILES: readonly string[] = Object.freeze([
  '/console.css',        // <- the one new entry
  '/console.js',
  '/index.html',
  '/installations.html',
  '/installations.js',
  '/refusal.html',
])
```

`console/test/unit/bundle.test.ts` changes with it, in the three places it counts or compares:
the non-vacuity count on the list itself, `expect(SERVED_FILES.length, …).toBe(6)`; the count
on the packaged-files list the test derives from it, `expect(required.length, …).toBe(8)` —
`required` is the two preload paths plus `SERVED_FILES`, so it grows with the list; and the
comparison that holds the two sides equal in both directions —
`expect(builtListing('bundle')).toEqual(servedNames())`. The two sides
are **different sources, which is what makes the comparison worth making**: `builtListing`
(`console/test/support/built.ts`) reads `dist/bundle/` off disk, and `servedNames()`
(`bundle.test.ts`) maps `SERVED_FILES` to bare file names. Adding `/console.css` changes the
**listed** side and the comparison fails until the build actually writes the file into
`dist/bundle/` — so the test is what makes a packaged app without its stylesheet fail rather
than ship. **The test's non-vacuity assertion is the load-bearing half**: a name dropped from
the list, or a file written into `dist/bundle/` that nothing vouches for, still fails.

### 5.4 The `<link>` elements

One `<link rel="stylesheet" href="./console.css" />` in the `<head>` of **each** of
`console/src/renderer/index.html`, `console/src/renderer/installations.html` and `console/src/renderer/refusal.html`, beside the existing CSP `<meta>`:

- it is a **relative path on this origin** (`./console.css`), like `./console.js`: no
  query, no host, no scheme, so it is resolved by the bundler as a literal and by the
  handler as a path under the root;
- it carries **no `media`, no `onload`, no integrity attribute and no `crossorigin`** — the
  file is local and same-origin, and `integrity` on an `app://` response is a hash nobody
  maintains;
- the refusal document **is included**, and that is the point: the one page a console below
  the floor shows must not be the unstyled one.

**Amended 2026-10-28: `console/src/renderer/confirm.html` is the fourth document and links
the same file the same way** (the launch confirmation window, §2.13, V2.5.1a). Nothing else
about this section moves with it: the stylesheet is still one file, still served by the same
handler under the same directive, and the document the window loads is the one page in the
bundle whose rules are classes rather than the picker's ids.

### 5.5 The CSP is unchanged, proved

**Nothing in this document edits `CONTENT_SECURITY_POLICY`, and the stylesheet is admitted
by the directive that is already there.** Read the string in
`console/src/main/protocol.ts`: `style-src 'self'` is its third directive. A `<link
rel="stylesheet">` to `app://console/console.css` is a stylesheet **from this origin**, so
`'self'` admits it; `default-src 'none'` is not consulted for it; `font-src 'self'` is not
consulted (no web font is loaded); `connect-src 'none'` is not consulted (a `<link>` is not
a fetch the CSP's `connect-src` governs, and no script fetches the CSS).

The evidence a reviewer should require, and which the tests provide:

- `CONTENT_SECURITY_POLICY` is byte-identical before and after, and so is the `<meta>`
  copy in each of the four documents — held for **all four** by
  `console/test/unit/csp-document.test.ts`, whose document list is derived from
  `SERVED_FILES`, and for the two it reads by `console/test/unit/window.test.ts`, whose
  `expect(CONTENT_SECURITY_POLICY).toBe(DESIGN_CSP)` pins the policy itself;
- `APP_SCHEME_PRIVILEGES` still has **no `bypassCSP`**, asserted rather than merely unset;
- the served CSS is served by the same `serveAppRequest`, with
  `content-type: text/css; charset=utf-8` from `mediaTypeFor`, `nosniff`, and the CSP
  header on the response.

**No inline style attribute and no CSP loosening is introduced, and a test fails if either
appears** — three of them, in fact: `csp-document.test.ts` already refuses a `<style`
element and a `style` attribute in any served document; [Task 6.2](#72-task-62--the-wiring--security-sensitive)
extends `console/test/unit/screen-literals.test.ts`'s scan so that a `.style` property write, a
`setAttribute('style', …)` or a `style` entry in its attribute vocabulary is an offence in
any file under `src/renderer/`; and the CSP assertions above refuse `'unsafe-inline'` (and
`'unsafe-eval'`, `*`, and a loosened `style-src`).

### 5.6 The unit tests that change, and what they pin

| File | Change |
|---|---|
| `console/test/unit/bundle.test.ts` | `SERVED_FILES.length` 5 → **6**, and `required.length` (the preload paths plus the list) 7 → **8**; a new case that `dist/bundle/console.css` is the concatenation of `THEME_STYLESHEETS` **read from the list**, in list order — not of hard-coded names, so the case is already true at 6.2 of the files that exist then and stays true as 6.3 and 6.4 append theirs; a case that the theme directory holds no `.css` outside the list |
| `console/test/unit/csp-document.test.ts` | a case that **every** document's `<link>` elements are exactly one, `rel="stylesheet"`, `href="./console.css"`, and carry no `media`/`onload`/`integrity`; the existing "no `<style>`, no `style` attribute" cases stay and now cover the stylesheet's existence too |
| `console/test/unit/window.test.ts` | unchanged in substance: the CSP/no-`bypassCSP` assertions already cover the CSS response, and the served-document cases still compare byte-identical HTML — its `<link>` is part of that HTML, so the file the window loads is still the file the test read |
| `console/test/unit/build-files.test.ts` | `bundlerOutputs` gains `'dist/bundle/console.css'`, so `build.files` in `console/package.json` **must** gain it (and `console/README.md`'s list must name it) — the packaged app would otherwise ship without its stylesheet, which is the exact failure this test exists to catch |
| `console/test/unit/theme-tokens.test.ts` (new) | the contrast and vocabulary cases of [§6.3](#63-contrast-as-unit-tests) |
| `console/test/unit/screen-literals.test.ts` | the `style` scan of §5.5 |

---

## 6. Tests

### 6.1 The golden frames

**The eight baselines are re-recorded after the stylesheet lands, and not before.**
Recording them first would produce a picture of unstyled markup that the next PR
invalidates, and — worse — it would let a reviewer believe the screens had been looked at.

- The frames are the same eight the tests already name: `live`, `spend`, `projects` and
  `run`, each `light` and `dark`, each per platform (`playwright.config.ts`'s
  `snapshotPathTemplate`), and the recording command is unchanged:
  `npx playwright test live.spec.ts --update-snapshots` (and its three siblings), run by
  **the owner, once per platform**, since `updateSnapshots: 'none'` makes a missing
  baseline fail rather than be written.
- **PR #629's darwin set is superseded, not kept.** Its baselines are pictures of the
  unstyled tree; the task that lands the CSS re-records them, and the PR body says so.
- `degraded.spec.ts` takes no frame, on purpose, and this design does not add one: its
  states are read as text assertions and a frame of a screen with no dashboard on it
  would be a picture of an absence.

### 6.2 The computed-background assertion — so identical frames can never be recorded again

**A per-spec assertion that the computed background colour differs between the schemes**,
in all four golden specs, next to the existing theme read. **Under the owner's Q1 ruling of
2026-10-19 there is one value set, so "differs between the schemes" is the wrong sentence**:
the two runs stamp different `data-theme` values and must paint the **same** ground, and the
assertion becomes that both runs compute the shipped `--ground` — the same guard, since a
tree with no stylesheet still fails it by reading `rgba(0, 0, 0, 0)` twice. The code below is
the two-set form, kept as the record of option (a):

```ts
// The two-set form, removed by the owner's Q1 ruling of 2026-10-19.
const paintedBackground = (el: Element): string =>
  getComputedStyle(el).backgroundColor

// for each theme, after the screen is drawn (the two-set form, removed by the
// owner's Q1 ruling of 2026-10-19):
expect(paintedBackground(root), `${theme}: the screen paints no background`)
  .not.toBe('rgba(0, 0, 0, 0)')
const [light, dark] = [/* the two runs' values */]
expect(light, 'light and dark paint the same background: the stylesheet is not being applied')
  .not.toBe(dark)
```

This is the test the current suite is missing. Every one of the four specs already reads
`data-theme` off the screen root (`scope.querySelector('.live')?.getAttribute('data-theme')`),
which passes today with no stylesheet at all. The new assertion fails **the moment a
stylesheet stops being served, stops being linked, or stops painting** — under the ruling
there is no switch left to stop, and the first two are what remains — which is exactly the
regression that could otherwise be recorded as a fresh golden and never noticed.

**This assertion lands in [Task 6.6](#76-task-66--the-golden-frames-re-recorded), with the
recording it guards — but what it waits for is the stylesheet, not Task 6.5's theme
choice.** It reads a *computed* background off a screen root, so it can pass only once a
rule paints one, and the rules that paint a screen root are the stylesheet's: `palette.css`
gives each theme its `--ground` and its `color-scheme`, and `base.css` and `screens.css`
spend them. Task 6.5 is a different subject — as designed it changed `CONSOLE_THEME` in
`entry.ts` from the literal `'dark'` to the media query's answer, which is the **app's** boot
path and the window's own `backgroundColor`; under the ruling it keeps the literal and what
is left of the task is the background and the keyboard.

The four golden specs never read it. Each draws its screen directly with the theme it
names — `mount(theLastFrame(), 'light')`, `renderSpend(factory, model, 'light')` and their
two siblings — and every screen stamps the theme it was handed on its own root
(`root.setAttribute('data-theme', theme)`; `live.ts` 1108-1109 and the same line in the
other three), so **the light run's root already says `data-theme='light'` today** and the
two runs already disagree about the attribute. They are byte-identical pictures for the
reason §1.3 gives, and not because the theme never varies: no stylesheet is served or
linked, so no rule reads `data-theme` and every computed background is `rgba(0, 0, 0, 0)`.
An earlier revision of this section said the light run still stamped `'dark'` and that the
assertion "would fail in Task 6.3 or 6.4"; both were wrong, and the hard dependency they
justified — on Task 6.5 — was wrong with them. The assertion belongs with the recording it
guards because 6.6 is where the goldens are re-made, and it could not land before the rules
it reads: the phase's own ordering, not the theme switch, is what puts it last. **The owner's
Q1 ruling of 2026-10-19 does not change any of this** — one value set ships, so the two runs
must paint the same ground, and the sentence above about the two runs disagreeing about the
attribute is a fact about the specs rather than a design this document prescribes.

### 6.3 Contrast as unit tests

`console/test/unit/theme-tokens.test.ts`, in the shape of `website/test/a11y.mjs` (which this file
should reuse the arithmetic of rather than re-derive):

- it **reads `theme/palette.css` for its colour numbers** — the literals the window paints,
  not a copy of them. **Three of the cases described below are not in the file that shipped
  with Task 6.1** (the file Task 6.1 wrote carries eight cases, and this list is the design
  the file is measured against), each for one of two reasons: the **two-set vocabulary case**
  is *replaced* — with one value set it would compare a set with itself, so the shipped case
  asserts the vocabulary is declared **exactly** (§3.1's tokens, and no name outside them)
  and that neither layer carries what the other owns —
  while the other **two read a file other than `palette.css`, and each lands in the task that
  writes what it reads** (the `--border` scan, which reads `base.css` and `screens.css`, in
  Task 6.4; the window background, which reads the main process's window module, in Task 6.5).
  Task 6.1 writes this file with the palette cases alone; the two arrive where their subject
  matter does, and the replacement is described with its case below;
- one case computes **every pair of §3.4** and asserts the ratio its role requires: 4.5:1
  for `--text`, `--text-muted`, `--accent-ink` and all five state colours on `--ground`,
  `--surface` and `--surface-alt`; 3:1 for `--accent` and `--border-strong` on each; 4.5:1
  for `--ink-on-fill` on every fill;
- a case asserts the **vocabulary** is complete and identical in both sets — the token
  names of one set are exactly the token names of the other, and every token of §3.1–§3.3
  is present — so a token added to light and not to dark fails by name. **Under the owner's
  Q1 ruling there is one set, so this case cannot be written as designed and Task 6.1
  replaces it with the case the file carries**: the vocabulary is declared **exactly** —
  every token of §3.1's table is present, *and* no name outside it is, so a step added to
  `tokens.css` on its own fails by name — neither layer carries what the other owns (no
  colour in `tokens.css`, no step in `palette.css`), and each of those two directions is a
  case a mutation fails by name;
- a case asserts `--border` is **only** used decoratively: no rule in `base.css` or
  `screens.css` uses `--border` where the property is `outline` or where the border marks
  a control (a scan of the CSS text, in the spirit of `screen-literals.test.ts`). **This
  one case is Task 6.4's and not Task 6.1's**: it is the first of the file's two cases that
  read something other than `palette.css`, and the files it reads are created by Tasks
  6.3 and 6.4 — see [§7.4](#74-task-64--the-screens). Task 6.1 writes the file and its
  palette cases; Task 6.4 adds this case at the point where both stylesheets exist;
- a case asserts **the main process's window background equals the ground the window is
  about to paint** ([§4.1](#41-the-windows-structure)) — under the Q1 ruling **one**
  literal, `#0A0E16`, against the shipped `--ground`; *as the two-set design wrote it,*
  **both** literals, the dark one against the dark `--ground` and the light one against the
  light `--ground`, rather than the ambiguous "equal to `--ground`" a `--ground` with two
  values cannot support. **This
  case is [Task 6.5](#75-task-65--the-theme-switch-the-window-background-and-the-keyboard)'s
  and not Task 6.1's**, for the plainest of reasons: the two literals are a change to
  `console/src/main/window.ts`, and Task 6.5 is the task that makes it — `consoleWindowOptions`
  gains no `backgroundColor` before then, so a case asserting one in Task 6.1 would be a
  case about a key that does not exist. It is the second of the file's two cases that read
  something other than `palette.css`, and like the `--border` scan it lands in the task that
  puts that file's content there;
- **a red step, run for real and committed as a case**: the arithmetic is exercised against
  a deliberately failing fixture, in the shape `a11y.mjs` keeps but with a pair this file
  names, and the case asserts it reports
  **`--text-muted on --surface is 3.1:1, needs 4.5:1`** — "so the refusal cannot rot into a
  comment about a check that no longer refuses anything". Two details are load-bearing and
  both were wrong in the first revision of this section: the **precision is one decimal**,
  because `a11y.mjs` formats `ratio.toFixed(1)` and its own committed fixture prints
  `3.1:1` (`website/test/a11y.mjs`), so a `3.10:1` string could not come from this
  discipline at all; and **the fixture pair is named**, because the console's real
  `--text-muted on --surface` is 8.02:1 dark and 6.27:1 light, so a `3.1:1` reading
  presupposes a pair deliberately overridden for the case — the precedent's own fixture is
  `--text-muted` `#646464` on `--surface` `#14141F`, and Task 6.1's case names its pair the
  same way rather than pretending a real one produced the figure.

### 6.4 A forced-colors smoke

One e2e case per screen, with Playwright's `emulateMedia({ forcedColors: 'active' })` (and
`colorScheme` pinned), asserting the three things §4.4 promises: the state words are still
in the DOM (`KILLED by`, `n/a`, `—`, `(partial)`, `queued`), the current view is still
marked, and the row structure still has a visible boundary (`border-bottom-width` is not
`0px` under forced colors, or the system's `CanvasText` is used). It is a **smoke, not a
golden**: forced-colors rendering is the platform's and its pixels are not ours to pin.

**It lands in halves, each with the rules it tests**, because a case written for a
stylesheet that does not exist yet is a case that fails: the chrome's half — the current
view still marked, the chrome's boundary still visible, **and the rail's rows with them**,
since §4.4's rule 1 is what the rail's three state words are for and a `silent` row's word
and glyph have to survive the same screen — is [Task 6.3](#73-task-63--the-layout)'s,
in `test/e2e/entry.spec.ts`, which is the spec that already drives the dashboard the
chrome is drawn into; the per-screen half — the state words and the row boundary, in each
screen's own spec — is [Task 6.4](#74-task-64--the-screens)'s. This is also why §4.4's
second rule splits the way it does: the chrome's `@media (forced-colors: active)` block is
in `base.css` and the rows' and badges' is in `screens.css`.

### 6.5 The docs test

`internal/cli/docs_console_styling_test.go` (new), in the shape of
`docs_budget_token_channel_test.go`, with thirteen tests:

1. a **presence test** over this document's eleven section headings and its load-bearing
   strings — the status line, `SERVED_FILES`, "No inline style attribute", `Never colour
   alone`, `KILLED by`, `~$`, `(partial)`, `tabular-nums`, `prefers-color-scheme`,
   `light-dark(`, `forced-colors`, "is already the directive a stylesheet needs",
   `mediaTypeFor`, `text/css; charset=utf-8`, `console.css`, `adoptedStyleSheets`,
   `Task 6.2`, "No D-number is allocated" — and over §2's vocabulary rows, failing when a
   cell that is nothing but a comma-separated list of code spans repeats one of its hooks:
   a hook list is the set a rule may select, and a repeat cannot be told from a typo (the
   Replay row listed `run-replay-count` at both ends, while the code emits eleven distinct
   `run-replay-*` classes);
2. a **citation test** that `os.Stat`s the paths this document cites **outside the console
   directory** — `website/src/styles/{tokens,palette-logo}.css`, `website/test/a11y.mjs`,
   `docs/design/desktop-console{,-v2}.md`, `docs/plans/2026-10-15-desktop-console.md`,
   `docs/website/redesign/reconciliation.md` — so a citation that moves is a failing test
   rather than a dead link; and that follows each `path:line` citation to that line and
   fails when the line does not name what the sentence around it says it does, because a
   line number is the citation a reader follows by hand — the two citations into the plan
   pointed at the tree line above the one this revision's added rows moved;
3. an **anchor test** that every in-page link in this document — the `#fragment` form —
   resolves to a heading in it, using GitHub's slug rule — lowercase, drop everything that
   is not a
   letter, a digit, a space, a hyphen or an underscore, spaces to hyphens — so an em dash
   leaves **two** hyphens behind (`— the wiring — SECURITY-SENSITIVE` slugs to
   `…the-wiring--security-sensitive`), and that no two headings slug to the same name. A
   citation test stats a file and cannot see a dead fragment, and three of this document's
   anchors were dead when the test was written — two of them for exactly that missing
   hyphen, one left over from an earlier revision of §7.5 that named a heading since
   rewritten;
4. a **plan test** that [Phase 6](#7-the-build-plan-phase-6) exists, that its
   six task headings are there, that the `theme/*.css` files are in the plan's tree,
   and that the phase names all six of the [§8](#8-open-for-the-owner) questions. **It also
   carries the two-set pin**: it reads both this document and the plan for the phrases that
   assume two value sets — `light and dark`, `light dark`, `light-dark(`,
   `prefers-color-scheme`, `color-scheme: light`, `both value sets`, `two value sets`,
   `two grounds`, `two --ground`, `two literals`, `two schemes`, `two sets`, `both sets`,
   `in each set`, `the light set`, `matchMedia`, `themeChoice`, `data-theme='light'` and
   `data-theme="light"` — and fails on one whose **own paragraph** is not marked with the
   ruling (`2026-10-19`, `Q1`, "as first written", "as designed", "as the two-set", "the
   record of", "record of option", "deferred", "would have", "did not take", "the ruling",
   "as shipped", "removed", "supersedes" and "kept"), so the plan's claim that every such
   place is marked is a claim a run checks rather than a claim a reader trusts. The mark is
   paragraph-local on purpose: an exemption that ran from a marked paragraph to the end of
   its section was one a sentence could hide behind, since every paragraph of §3.6, §4.1 and
   §8 sits under a ruling sentence somewhere above it. The paragraph is lower-cased and its
   whitespace is collapsed before the match, so a phrase a rewrap splits across two lines,
   or one whose capitalisation a sentence changes (`matchMedia` for `matchmedia`), is the
   same phrase to the test as it is to a reader;

**Items 5 to 9 — five of the thirteen — read §3.4's numbers rather than its prose**, which is the
half the review of the first revision asked for. They recompute WCAG 2.1's relative luminance
(`(L1+0.05)/(L2+0.05)`) and circular hue distance
(`min(|Δh|, 360°−|Δh|)`) from the literal values in §3.2 and §3.3, in four lines of Go each,
deliberately not shared with the console's own token test: that one is TypeScript under
`console/`, and the boundary is the same one the paragraph below explains.

5. a **contrast test** that every ratio §3.4 prints is the ratio the literals compute, to
   two decimals; that each token clears the requirement its role carries — 4.5:1 for the
   text tones and the five state colours on all three surfaces, 3:1 for `--accent` and
   `--border-strong`, 4.5:1 for `--ink-on-fill` on every fill; and that `--border` is
   **still below** 3:1, because a hairline that cleared it would be a boundary this design
   should have named as one;
6. a **state-separation test** that the ten pairs of state colours are the ten
   the literals make — **both tables below are read, because §3.4 keeps both: the dark
   table's ten are the set that ships, and the light table's ten are the record of the set
   the owner's Q1 ruling of 2026-10-19 deferred (§3.3), asserted as that record rather than
   as a set anything paints** — that every one clears §3.4's minimum, and that each row says which
   arm of §3.4's disjunction it took — 50° of hue, **or** 3:1 of luminance contrast where
   hue cannot separate a pair — so a pair that missed both fails the test and a row that
   tabulated a fallback pair as if hue had separated it fails too. It does **not** demand
   3:1 of a pair that already clears 50°, which is a rule this document does not state and
   none of the ten present pairs meets;
7. a **provenance test** that reads [`palette-logo.css`](../../website/src/styles/palette-logo.css)
   and checks §3.2's accounting against it: the seven values reused byte for byte, the one
   reused under another name (`--accent-on-dark` → `--accent-ink`), the six that are new
   and therefore absent from the website's palette, and the fourteen the three add up to;
8. an **accent-on-a-light-ground test** that `#17A8E6` is 2.70:1 on white and 2.49:1 on
   this set's own `--ground`, and that the light set's accent clears 4.5:1 on all three
   surfaces — the finding that the right figure was in the wrong place, made runnable;
9. a **separation-arms test** of the two arms of the rule in 6 on their own, over synthetic
   hue gaps and contrasts rather than over this document's colours — and the reason it is
   synthetic is that **this document's colours cannot reach the fallback at all**: every
   state colour has to clear 4.5:1 as text on all three surfaces, which holds the five in
   one narrow luminance band (the widest luminance gap between any two of them is 1.47), so
   no pair of the five present colours is 3:1 apart in luminance and **no pair in either of
   6's tables takes the second arm**. Item 6 therefore accepts the fallback wherever the
   literals justify it but never meets one, and no edit to this document alone can exercise
   that branch; the synthetic table here is what keeps the branch itself tested rather than
   dead code. It also pins both thresholds as inclusive, which is how §3.4 writes them ("at
   least 50°", "at least 3:1").
10. a **phase-ordering test**, the tenth, the one case of the thirteen that is about the two
   documents' agreement on the phase's order rather than about either alone — it reads
   [§7](#7-the-build-plan-phase-6) and the plan together: it cuts the plan's
   Phase 6 at its six task headings and §7 at its seven, and fails when a task's `**Files**`
   or `**Tests**` clause names a **later** task — the shape both of this revision's ordering
   errors took — when [§5.2](#52-how-the-bundler-emits-it)'s `THEME_STYLESHEETS` literal
   names a file no task has created yet at the point the list lands, when Tasks 6.3 or 6.4 omit
   their own file or their `THEME_STYLESHEETS` entry from `**Files**`, when Task 6.2 names
   either later file, when Task 6.1 claims `backgroundColor` or Task 6.5 does not, and when
   [§6.3](#63-contrast-as-unit-tests) stops saying the window-background case is Task 6.5's.
   And it counts itself: §6.5's own opening line says how many tests this file holds, and the
   case fails when that word and the file's `TestDocsConsoleStyling*` declarations disagree —
   the defect this revision found in the other direction, when the tenth test landed and the
   line stayed at nine. Each of its cases is mutation-proven, and the eight mutations are
   named in [§7.7](#77-the-red-steps). It reads the plan as text for the same boundary reason
   the rest of this file does.
11. a **counts test** over this section and §7.7, which is what keeps the numbers above from
   rotting: it numbers the ordered list here and requires it to run 1 to N with no gap and N
   to be the count this section's opening line prints in words; it reads the group sentence
   ("Items 5 to 9 — five of the thirteen") against the list it names, in every number the
   sentence prints;
   it counts the rows of §7.7's red-step table and the clauses of its two mutation lists
   against the words above them; and it counts this document's `## ` headings against the
   number the presence case names. Every count is taken from the thing counted — list items,
   table rows, semicolon-separated clauses — rather than from the prose beside it, because a
   test that agreed with the prose would agree with any prose. The revision that added it is
   the evidence: this section introduced its §3.4 group as "five that read §3.4's numbers"
   over a list numbered 5 to 10, so a reader counting the items after the sentence counted
   six.
12. a **rail test** over §4.1's rail block, the twelfth: it requires the block to say the rail
   is a **left vertical rail**, to name `aside.console-rail`, **both its widths** — the
   216 px expanded one and the 56 px collapsed one, each its own required string, so dropping
   either fails by name — `div.console-rail-list` as the tablist, the hint "Switch with the
   shortcut keys 1 2 3", **Command-1** and **Control-1**, the words READ ONLY and "+ Add
   installation", `role="tablist"`, a **roving `tabindex`**, Enter selects, and **Home** and
   **End**, each asserted separately because the section names both keys and a case that
   required only "Home" would pass over a block that had dropped End; it reads the three
   state words off the table the block draws them in, **one row each**, so `running`'s row
   carries its dot and `--ok`, `silent`'s its triangle and `--warn`, and `idle`'s its open
   circle and `--text-muted` — a glyph moved to the wrong word, or a word given a tone §3.2
   does not have, fails by name; it counts the tree's classed elements and its distinct
   classes and requires §4.1's sentence and §2.1's row to state the same two numbers, so the
   count cannot rot silently; and it requires the block to say the rail draws the rows the
   dashboard can read rather than a row per stored installation. It is the case that keeps
   the canvas's one structural addition from being remembered rather than written down.
13. a **rail-ownership test** over the plan, the thirteenth: it reads Task 6.3's `**Scope**`
   and `**Files**` and Task 6.5's `**Scope**` and requires each to name what that task owns —
   the rail's structure, and the entry that draws its elements, in 6.3; the rail's shortcut
   keys in 6.5 — because a design that describes a region and a plan whose tasks do not name
   it is a region nobody builds. It reads the plan as text for the same boundary reason the
   rest of this file does.

**The citation test stats a file; these five read the file**, which is the difference
between a citation that exists and a citation that is true. Neither substitutes for the
console's own token test in [§6.3](#63-contrast-as-unit-tests), which is the one that will
run against `palette.css` once it exists; these read the design's own tables, and the point
of both is that no number in this document is a number nobody computed.

**The citations to the console's own files are deliberately not stat'ed, and the reason is
a boundary rather than a preference.** `TestConsoleIsOutsideTheGoModule` in
`internal/cli/docs_console_boundary_test.go` is plan Task 0.3's boundary, and its
allow-list of Go files permitted to mention a path under `console/` is a **closed**
exception — the console contract's own docs tests, whose size the boundary test pins —
its entries asserted one by one. A further entry would be a deliberate widening of the
boundary that this styling work has no case for and no mandate to make (the owner accepted
C1 as recommended: the test **is** the guarantee that the Go module never reaches into
`console/`). So this test reads the
console-side citations only as text, and this document names them for the reader the design
is written for. **The first draft of the test failed the boundary test**, which is how the
rule was found rather than assumed.

Its red step is this document's absence, and the mutations each test is proved against are
named with the change ([§7.7](#77-the-red-steps)).

---

## 7. The build plan (Phase 6)

Added to [the plan](../plans/2026-10-15-desktop-console.md) as a new **Phase 6** in the
file's own numbering, in the five places that file carries a phase: one section
`## Phase 6 — …`; **six** `### Task 6.n:` headings; **one** row in the Phase summary table;
**five** rows in the PR groups table (`J` to `J5`, one row per PR *group* — the phase is six
tasks across five groups, since `J3` is the two PRs of Tasks 6.3 and 6.4); and
**one line per `theme/*.css` file** in the File Structure tree. **These six tasks are
`APPROVED by the owner on 2026-10-19`**, and so is this document: the owner's approval is the
go, **Q1** is answered by it (option (b), dark only) while **Q2 to Q6 stand as §8 writes
them**, and the plan's phase heading, summary row and PR rows say the same. *The heading as first written,
kept:* `The build plan (Phase 6, UNAPPROVED)`, whose text read "**Nothing here is approved**:
the plan's OWNER APPROVAL GATE stands, and these tasks carry `UNAPPROVED` until the owner
answers §8" — that was the state on 2026-10-19, before the go. Task 6.2's heading
carries `SECURITY-SENSITIVE, independent opus review`, and Global Constraints rule 9's list
gains `6.2` in the same edit — `TestPlanRule9ListsEveryFlaggedHeading` fails otherwise,
which is the mutation this change is proved against.

**Order, and why it is this one.** 6.1 (the tokens) is first because everything reads it.
6.2 (the wiring) is second because until it lands **nothing on screen changes at all** —
the CSS files are inert without a `<link>`. 6.3 and 6.4 (layout, then the screens) are
then ordinary CSS PRs that a reviewer can read as a stylesheet. **Two tasks touch
`console/src/renderer/entry.ts`, and they are the two ends of the chrome**: 6.3 appends the
rail's elements (§4.1) and 6.5 (the switch and the keyboard) is the one that adds the
attributes and the keys — the rail is drawn by 6.3 and takes the keyboard in 6.5, so neither
PR carries a stylesheet written against an element nothing draws, and neither carries an
element with no keys. 6.6 (the frames) is last and owner-run.

### 7.1 Task 6.1 — the tokens

- **Scope:** `console/src/renderer/theme/tokens.css` (the vocabulary of §3.1),
  `theme/palette.css` (the dark value set of §3.2 — §3.3 is the record of the set the
  owner's Q1 ruling of 2026-10-19 deferred, and no task writes it), and `console/test/unit/theme-tokens.test.ts`
  (the contrast and vocabulary cases of §6.3).
- **Files:** the three above; the plan's tree; `console/README.md`'s theme note.
- **Tests:** the red step is real: the token test is written first, and the failing pair it
  is proved against is a palette edited to break one ratio — the test reports the ratio by
  name and the mutation is reverted.
- **Verification:** `cd console && npm run test:unit` (the new file and the whole unit
  suite green).
- **Reviewer tier:** ordinary. It touches no CSP, no protocol and no document.
- **Not mergeable alone into a visible change:** it is inert until 6.2. That is fine and
  stated: a token file that nothing links is a file, and the PR says so.

### 7.2 Task 6.2 — the wiring — SECURITY-SENSITIVE

- **Scope:** the bundler's stylesheet step (§5.2) — `THEME_STYLESHEETS` **with the files
  that exist at this point** (`tokens.css`, `palette.css`, both from Task 6.1), the
  refusals, the `dist/bundle/console.css` artefact and its ownership; `SERVED_FILES`
  (§5.3); the `<link>` elements (§5.4 — three when this task landed, amended to four by
  V2.5.1a on 2026-10-28); `build.files` and the README's list (§5.6);
  and the tests that pin all of it — `bundle.test.ts`, `csp-document.test.ts`,
  `build-files.test.ts`, and the `style` scan extension to `screen-literals.test.ts` (§5.5).
  **The whole-directory list is not this task's**, and cannot be: the bundler refuses a
  listed file that is missing, so Tasks 6.3 and 6.4 append their own file, each in the commit
  that creates it (§5.2's table). What this task lands is a list that is true of the
  directory on the day it merges, and a build whose artefact is the files in it.
- **Files:** `console/tools/bundle-renderer/index.ts`, `console/src/main/protocol.ts`,
  the three `.html` documents, `console/package.json`, `console/README.md`,
  `console/test/unit/{bundle,csp-document,build-files,screen-literals}.test.ts`.
- **Tests, and the mutations each is proved against:** drop `/console.css` from
  `SERVED_FILES` → `bundle.test.ts` fails; add a `.css` to `theme/` that is not on
  `THEME_STYLESHEETS` → the bundler refuses; add `'unsafe-inline'` to `style-src` →
  `window.test.ts` and `csp-document.test.ts` fail; put `style="…"` in a served document →
  `csp-document.test.ts` fails; write `el.style.color = …` in `entry.ts` →
  `screen-literals.test.ts` fails; remove `dist/bundle/console.css` from `build.files` →
  `build-files.test.ts` fails.
- **Verification:** `cd console && npm run test:unit && npm run build`, then
  `npm run test:e2e -- entry` for the live CSP assertion, and `GOFLAGS=-p=2 go test
  ./internal/cli -run 'Docs|Lint' -count=1` (this task edits no Go, but the docs suite is
  the run this repository reaches a doc change by).
- **Reviewer tier:** **SECURITY-SENSITIVE, independent opus review** — it edits the CSP's
  tests, the protocol handler's allow-list and the served documents.
- **This is the task a reviewer should attack**: can the served root grow a file nothing
  vouches for, can `style-src` be widened, can a document gain a second stylesheet or an
  inline style.

### 7.3 Task 6.3 — the layout

- **Scope:** `theme/base.css` — the reset, the document as the two-column grid of §4.1 and
  **the installation rail** and its rules: the rail's fixed 216 px expanded width and its
  56 px collapsed width, its header row and
  its collapse control, its rows with their name, project, state glyph and word and spend
  spans, its selected row's 3 px accent border, its foot row and its hint — the three bands
  of §4.1, the nav's tab-list styling, the focus ring, the responsive layers of §4.2, and the
  **chrome's** `@media (forced-colors: active)` block of §4.4. **The rail's elements are
  drawn by `entry.ts`, and this task is what draws them**, because the rules that select
  them are this task's and a hook nothing emits is not a hook — the same rule that puts
  `degraded-num` in Task 6.4's Files. The rail's **keys** are not here: they are Task 6.5's,
  which is already the task that gives the nav its own.
- **Files:** `theme/base.css`; `src/renderer/entry.ts` (the rail's elements);
  `THEME_STYLESHEETS` in the bundler, which gains
  `'base.css'` in this commit — the list is the theme directory's whole contents and the
  bundler refuses a listed file that is missing, so the file and its entry move together
  (§5.2); `README.md`; `test/e2e/entry.spec.ts` (the forced-colors smoke's chrome half, and
  the body-children assertion the rail moves).
- **Tests:** the forced-colors smoke's chrome half (§6.4) — the current view still marked,
  the chrome's boundary still visible and the rail's rows with them, all under
  `emulateMedia({ forcedColors: 'active' })` — written first, which is this task's red step;
  a case that the rail draws **one row per installation the dashboard can read** — the
  active installation's, from `installation.current` — with its state word and its glyph,
  **and draws no row for an installation it cannot read**, since the dashboard's surface has
  no list read and this task adds none (§4.1, §11); and **the §2-inventory selector scan over
  `base.css`**, which is this task's and
  not §7.4's alone: the rail's hooks are the chrome's and §2.1 lists them, so the list the
  scan checks against is the inventory's tables rather than the screens' class literals,
  which cannot see a rule written against a hook the entry draws.
- **Verification:** `cd console && npm run test:unit && FUGARO_CONSOLE_GOLDENS=skip npm run
  test:e2e -- entry`; the
  e2e specs that read the chrome's shape (`entry.spec.ts`'s
  `['console-rail', 'console-installation', 'console-views', 'console-panel']`) still pass. The four golden
  tests are skipped in this task and in the two after it (`FUGARO_CONSOLE_GOLDENS=skip`,
  which the staged console-tests workflow already sets): a layout change is a picture the
  baselines do not have, and re-recording them before the last stylesheet lands is what
  [Task 6.6](#76-task-66--the-golden-frames-re-recorded) exists to stop.
- **Reviewer tier:** ordinary — but it is one of the two tasks that touch the entry
  (§7.5 adds the attributes and the keys and this one draws the rail's elements), so it is
  reviewed against §5.5's "no style, no loosening" rule explicitly: the rail's elements are
  created with classes and attributes, and the styling is `base.css`'s.

### 7.4 Task 6.4 — the screens

- **Scope:** `theme/screens.css` — the rules for Live, Spend, Projects, Run, the degraded
  and unavailable screens and the picker (§2), including the tabular-figure/right-align
  rule, the density rules of §3.8 and the state colours of §3.5; **and one line of code**,
  adding `degraded-num` to the degraded screen's `numberCell`, because that helper prepends
  no class today and the fifth number hook therefore does not exist for a stylesheet to
  select ([§2.10](#210-numbers-money-and-time--what-the-stylesheet-must-align)).
- **Files:** `theme/screens.css`; `THEME_STYLESHEETS` in the bundler, which gains
  `'screens.css'` in this commit (§5.2) — after this task the list is the whole directory
  and stays still; `src/renderer/screens/degraded.ts` (that one line);
  `test/e2e/{live,spend,projects,run,degraded}.spec.ts` (the smoke's per-screen half);
  `README.md`; `test/unit/theme-tokens.test.ts` (the `--border`-is-decorative case of §6.3,
  which belongs here rather than to Task 6.1 — it is the one case that reads `base.css` and
  `screens.css`, and neither exists until this task).
- **Tests:** the forced-colors smoke per screen (§6.4) — every state word still in the DOM
  and the row boundary still visible; a case that
  `screens.css` selects only hooks the [§2](#2-screen-inventory) inventory names — a scan
  of the stylesheet's selectors checked against a list derived from the inventory's tables,
  the screens' own class literals **and the chrome's hooks of §2.1**, since the rail's
  **twelve** hooks — §2.1's twelve, on its thirteen elements — are the entry's and not a
  screen's, so a rule that styles a class nothing emits fails; the
  `--border`-is-decorative scan.
- **Verification:** `cd console && npm run test:unit && FUGARO_CONSOLE_GOLDENS=skip npm run
  test:e2e`, with the goldens skipped as in Task 6.3.
- **Reviewer tier:** ordinary.

### 7.5 Task 6.5 — the theme switch, the window background and the keyboard

**The owner's Q1 ruling of 2026-10-19 removes the switch this task is named for** (§3.6,
[§8](#8-open-for-the-owner)). One value set ships, dark, so there is nothing to switch
between: the scope below is the two-set design this task was written from, and the heading
is kept as the record of what it was scheduled to do. What is left of the task is **the
window background and the keyboard**.

- **Scope:** `CONSOLE_THEME` stays the literal `'dark'` it is today — no `themeChoice()`,
  no `matchMedia` read, no `change` subscription and no `nativeTheme` query comes with it
  (§3.6); `consoleWindowOptions` gains `backgroundColor`, **one literal**, `#0A0E16`,
  against the one `--ground` (§4.1); the nav's tablist behaviour **and the rail's** (§4.3) —
  the rail's list gets the same roving `tabindex`, Arrow/Home/End between its rows and
  **Enter to select**, together with the **shortcut keys** that select by position (§4.1).
  The rail's keys are new to this task and belong here rather than in Task 6.3: the rail is
  the canvas's region and this is the task that gives the console its keyboard.
  *The scope as designed, before the ruling:* `CONSOLE_THEME` becomes the answer of a
  `themeChoice()` that reads `matchMedia('(prefers-color-scheme: dark)')` and subscribes to
  `change`; the entry stamps `data-theme` on the mount it draws the chrome into; the
  picker's script stamps its document; `consoleWindowOptions` gains **both background
  literals**, read from `nativeTheme.shouldUseDarkColors` and re-set on `nativeTheme`'s
  `updated` event.
- **Files:** `console/src/renderer/entry.ts` (the nav's tablist behaviour and its
  roving `tabindex`, §4.3), `console/src/main/window.ts` (the one background literal,
  §4.1), and `console/test/unit/{entry-wiring,theme-tokens}.test.ts` (the roving-tabindex
  case and the window-background case) — the same four files the plan's Task 6.5 names
  under its own **Files**, which is the list a pin compares with this one file for file.
  *As designed:* the same list with `console/src/renderer/installations.ts` (the picker's
  script, which the ruling leaves alone) and `console/test/unit/window.test.ts` — the same
  pair the plan's Task 6.5 keeps as the same record.
- **Tests, mutation-proven:** the window's background literal drifts from `--ground`
  (`#0A0E16` edited to `#121A26`) → the token test fails by name — **one literal, one
  direction, which is the ruled scope**; a roving-tabindex regression → the entry wiring
  test fails. *The two-set form of the background case:* `CONSOLE_THEME` no longer follows
  the media query → the unit test fails; either literal drifts from **its own** `--ground`
  → the token test fails, one assertion per literal, since "equal to `--ground`" was
  ambiguous with two grounds. **That assertion is this task's and cannot be an earlier
  one's** (§6.3): the literal arrives in `src/main/window.ts` here, so this is the first
  task at which there is anything to compare it against.
- **Note on the CSP and `ElementLike`:** this task writes **attributes only** — `role`,
  `aria-*` and `tabindex` on the nav. **`ElementLike` needs no widening, no style is
  written, and the CSP is not touched.** *The two-set design also read `matchMedia` and
  stamped `data-theme`; neither is a DOM style write or a network call, which is why that
  form would have left `ElementLike` and the CSP alone too.*
- **Verification:** `cd console && npm run test:unit && FUGARO_CONSOLE_GOLDENS=skip npm run
  test:e2e` — skipped here too, since this task changes what the window paints behind the
  first frame.
- **Reviewer tier:** ordinary — but it is one of the two tasks that touch the entry
  ([§7.3](#73-task-63--the-layout) appends the rail's elements and this one adds the
  attributes and the keys), so it is reviewed against §5.5's "no style, no loosening" rule
  explicitly.

### 7.6 Task 6.6 — the golden frames, re-recorded

- **Scope:** the eight baselines per platform and the per-spec computed-background
  assertion (§6.2) — the assertion this task owns as the guard on the recording it
  re-makes, and not because the theme *choice* arrives here: the four golden specs draw
  their screens with the theme they name and never read `CONSOLE_THEME`, so what the
  assertion needs is the stylesheet Tasks 6.1–6.4 write, and Task 6.5 changes no picture
  these specs take (§6.2). The forced-colors smoke is **not** here: it
  landed with the rules it tests, its chrome half in Task 6.3 and its per-screen half in
  Task 6.4.
- **Files:** `console/test/e2e/{live,spend,projects,run}.spec.ts`; the committed
  `console/test/e2e/__screenshots__/**` PNGs.
- **Owner-run:** the **recording** is owner-run ("recorded by an owner, once on each
  platform the comparison runs on", as the specs already say). The **assertions** are
  agent-written and fail until the baselines are committed — which is the suite's existing,
  deliberate red state.
- **Verification:** `cd console && npm run test:e2e` after the owner records; before that,
  `FUGARO_CONSOLE_GOLDENS=skip` for everything else.
- **Reviewer tier:** ordinary for the assertions; the recording is the owner's.
- **What it supersedes:** the draft PR #629's darwin baselines, which are pictures of the
  unstyled tree.

### 7.7 The red steps

Every task above lands test-first, and each red step was named with it. The four that
matter most, and the mutation each is proved against:

| Red step | Mutation that must make it fail |
|---|---|
| `theme-tokens.test.ts` (6.1) | a palette value edited to break one ratio — the test names the pair and the ratio |
| `bundle.test.ts` / `csp-document.test.ts` (6.2) | `/console.css` dropped from `SERVED_FILES`; a `<link>`'s `href` changed; `style-src` widened by one token |
| the forced-colors smoke (6.3's chrome half, 6.4's per screen) | the `@media (forced-colors: active)` block deleted from the stylesheet under it — the boundary collapses and the case fails |
| the golden specs' `paintedBackground` assertion (6.6) | the `<link>` removed from `index.html` — nothing paints the ground and both runs read the compositor's `rgba(0, 0, 0, 0)`, so the assertion fails. *Under the two-set design the same mutation failed because the two schemes then painted the same colour, which one set cannot show:* |

**And the five this document's own docs test was proved against, each run for real and
reverted** ([§6.5](#65-the-docs-test)): the status line replaced with `Status: draft.`; a
cited path removed from the text, and another moved on disk; an in-page anchor left as the
broken slug it was written as; Phase 6's heading stripped of `UNAPPROVED`, one of its six
question markers dropped, and one of its task headings renamed; and the rail's two glyphs
swapped between `running` and `silent` in §4.1's table, or its **216 px** expanded width — or
its **56 px** collapsed one — dropped from the block beside it, which the rail test catches
on the row the glyph moved to and by name, once per width — as does **§7's ordering sentence
put back to a wording that makes 6.5 the sole owner of the entry**, which the same case
refuses wherever either document carries that claim — the guard is a pattern over the two
documents rather than one fixed sentence, so it was run against §7's sentence, §7.5's
original phrasing and the plan's `J4` row, three wordings of the same claim, and the third is
the one this round found still standing.
The seventh is the plan's
own: `6.2` removed from Global Constraints rule 9's list, which
`TestPlanRule9ListsEveryFlaggedHeading` catches. **And the ordering test (§6.5's tenth)
carries eight of its own**, one per case, planted and reverted: the whole-directory
`THEME_STYLESHEETS` literal restored; `base.css` put back into Task 6.2's `**Files**`; the
window-background case put back into Task 6.1's `**Tests**`; a later task named in Task
6.3's `**Tests**`; `THEME_STYLESHEETS` removed from Task 6.3's `**Files**`; §6.3's
"This case is [Task 6.5]'s and not Task 6.1's" removed; §6.5's opening line put back to
"nine tests" over a file that declares ten; and `entry.ts` dropped from §7.5's `**Files**`
while the plan's Task 6.5 keeps it, which is the two documents' lists disagreeing about the
task that still owns the nav's keyboard behaviour. The two-set pin beside them is proved the
same way, twice: an unmarked two-set sentence planted in a section that states the ruling
somewhere above it, and a phrase a rewrap splits across two lines. **The sentence in §6.5
that describes that pin is proved as well** — a phrase dropped from its enumeration of them
fails the case that reads the sentence against the pin's own list, which is how a reader is
kept from writing a weaker scan than the one that ships — and so is the plan's list beside
§7.5's, file for file: a file moved from one document's ruled list to the other's fails by
name.

---

## 8. Open for the owner

Six questions this design does not answer for the owner, **one of which the owner has since
answered**: Q1 was ruled on 2026-10-19 (option (b), dark only) and the answer is written into
the question below, while Q2 to Q6 stand open. **No D-number is allocated** — they are the
console plan's `C`-series style only in shape, and a number is the owner's to give.

**Q1 — Does the console ship a light theme at all, and is the OS the default?**
- **ANSWERED by the owner on 2026-10-19: option (b), dark only** — matching today's
  `CONSOLE_THEME`, with the light set deferred to §3.3's table and no OS default added.
  `palette.css` sets `color-scheme: dark`; the four spellings a light theme would need
  (`light dark`, `light-dark()`, `prefers-color-scheme`, a `[data-theme='light']` block)
  fail `console/test/unit/theme-tokens.test.ts` by name. **This is the one question the
  approval of 2026-10-19 answered**, and §3.6, §4.1, §6.2 and §6.3 and the plan's Tasks 6.1,
  6.5 and 6.6 each carry what (b) changes in them. **Q2 to Q6 below stand open.**
- *Options:* (a) light and dark both ship, the OS decides, an override is offered
  (**recommended**); (b) dark only, matching today's `CONSOLE_THEME`, with light deferred;
  (c) light and dark, the OS decides, **no override at all**.
- *Recommendation:* (a). *Why:* the design already decided a light set exists (§6.1 rule 5)
  and the refusal page — the one document with no script — can only be themed by the OS
  (§3.6), so if there is no light theme the console ships a dark product and a white
  refusal page. (c) is defensible and cheaper; it costs the operator nothing but choice.

**Q2 — Where does the owner's theme override live?**
- *Options:* (a) **no override in v1** — the OS is the whole answer, and the app writes
  nothing new (**recommended for v1**); (b) a new bounded `settings.json` in `userData`,
  which **amends §5.1 rule 10** ("the app writes the spawn log, when asked, and this
  installations file, and nothing else") and needs the owner's ruling the way C8 did;
  (c) a key inside the existing `installations.json`, which is schema-closed and would
  need a version bump; (d) `nativeTheme.themeSource` in the main process, which themes all
  four documents including the refusal page.
- *Recommendation:* (a) for v1, with (d) named as the natural home if the owner later wants
  one — (d) needs no new setting to *decide the colour* but still needs one to *remember*
  the choice. *Why:* v1 writes two files and rule 10 says so in the owner's own words;
  adding a third is a security-relevant change (a new file, a new parse, a new bound) that
  a styling pass should not make silently.

**Q3 — Does the console ship the "phosphor set" the design's §6.1 rule 5 names?**
- *Options:* (a) light and dark only (**recommended**); (b) light, dark and the low-saturation
  variants (green, amber, blue, mono).
- *Recommendation:* (a). *Why:* rule 5's "a small set of low-saturation palettes" was written
  when the theme was a preference; the token layer makes each variant additive (one more
  `.css` and one more entry on `THEME_STYLESHEETS`), so deferring costs nothing and shipping
  six palettes costs six contrast sets to review and six more goldens per screen.
  *Unanswered, like Q2 and Q4 to Q6: the owner's Q1 ruling of 2026-10-19 answered Q1 alone,
  and the options above keep the wording they were put to the owner in.*

**Q4 — Are PR #629's darwin baselines superseded, or kept until the CSS lands?**
- *Options:* (a) superseded — the CSS lands, then one recording pass (**recommended**);
  (b) #629's baselines merge first and are re-recorded again later.
- *Recommendation:* (a). *Why:* they are pictures of the unstyled tree ([§1.3](#13-why-this-matters-now-the-golden-frames-are-pictures-of-unstyled-markup));
  merging them buys a recording pass that the next PR invalidates, and their presence would
  make the suite green on a console that has never been styled.

**Q5 — Is the forced-colors smoke a gate or a report?**
- *Options:* (a) a gate — the case fails the build (**recommended**); (b) a report — it runs
  and prints, and does not fail.
- *Recommendation:* (a), because the three things it asserts (the state words survive, the
  current view is still marked, the rows still have a boundary) are the accessibility
  contract §3.5 and §4.4 state, and a report nobody reads is not a contract.

**Q6 — Does the styling work gate the console's first release?**
- *Options:* (a) no — it lands after 0.9.0, as an ordinary follow-up (**recommended**);
  (b) yes, the console's MVP release waits for Phase 6.
- *Recommendation:* (a). *Why:* the design's MVP list ("What ships with the MVP") does not
  name a stylesheet, and a release that waits on a design review of colours is a release
  held by its least load-bearing part. (b) is the owner's call and costs one release.

---

## 9. Alternatives considered

### 9.1 A CSS-in-JS runtime — **refused by the CSP**

A runtime that injects a `<style>` element or writes `style="…"` needs
`style-src 'unsafe-inline'`; constructing the sheet through the CSSOM
(`sheet.insertRule`, `CSSStyleSheet.replaceSync`) is not blocked by CSP but **the text would
have to arrive in the script**, since `connect-src 'none'` refuses every fetch. The cost is
the same either way and it is not the CSP: the styling stops being a file a reviewer can
read, the CSP's `style-src 'self'` stops meaning anything, and the console's own rule — no
markup, no style, through the narrow `ElementLike` — is undone in one dependency. **Refused,
and the reason is reviewability rather than bytes.** A framework runtime is also the
dependency the console's own pinning rule exists to keep out.

### 9.2 Constructed stylesheets, `adoptedStyleSheets` — admissible, but it costs a fetch

Does `style-src 'self'` permit a constructed stylesheet built **from a served file**?

- **Yes for the file, no for the way it would be read.** A constructable `CSSStyleSheet`
  populated by `replaceSync(text)` is not subject to `style-src` at all (CSP has no hook on
  CSSOM insertion), so a served file *could* be adopted without touching `style-src` — **but
  the renderer cannot read that file**: `connect-src 'none'` refuses `fetch()`, and an
  `<link>`'s `.sheet` is only reachable through the same `<link>` this design already
  proposes. So an adopted sheet would have to be either (a) a `<link>` plus adoption, which
  is strictly more machinery than the `<link>` alone, or (b) CSS text embedded inside
  `console.js`, which is free under the CSP and **moves the stylesheet into the script** —
  no separate served file, no `text/css` response, nothing for `SERVED_FILES` to vouch for,
  and a reviewer reading TypeScript template literals for a `:focus-visible` rule.
- **Verdict: refused in favour of the `<link>`.** Adoption is a real option the day the
  console needs to swap a theme without a document reload; it is not needed to paint a
  screen, and it would spend the bundle's reviewability to buy nothing.

### 9.3 The unstyled-but-laid-out stopgap

Ship only `base.css` — a reset, the three bands, the density, the focus ring — and no
palette, so the console uses the platform's own colours. It is genuinely attractive: it is
one file, it is guaranteed to respect the OS, forced-colors and high contrast for free, and
it makes the golden frames meaningful (layout regressions) without deciding a colour.
**Refused as the plan, kept as an ingredient:** the console's first rule (§6.1 rule 1) is
that a reading never lies, and the state colours are how `measured`, `notional`, `estimated`,
`unavailable` and `gap` become distinguishable at a glance — a platform-coloured console
draws five states in one colour and asks the operator to read five pieces of text. 6.3's
`base.css` **is** that stopgap, with `palette.css` already in place beside it.

### 9.4 Recommended

**The `<link>` to one bundled `console.css`, the theme directory's whole contents,
`style-src 'self'` untouched** — §5 in full. It is the smallest change that paints the screens, it keeps the
CSP exactly as the design wrote it, it keeps every byte the window paints readable as a
file, and it is the shape the website already uses for its own tokens.

---

## 10. Design brief for the Claude Design canvas

A controller is building a Claude Design canvas in parallel with this document. **This
section is the brief it should be drawn from, and the two must agree**: the screens, the
regions, the states and the palette below are the same ones §2 and §3 specify, and a
canvas that invents a screen, a region, a state or a colour is a canvas that disagrees with
the code.

**The canvas has since been drawn and approved.** The owner approved it on 2026-10-19: the
four screens — Live, Spend, Projects and Run detail — in the one value set that ships, the
dark one, with the boundary screens of frame 5 drawn in the same set. Its **one structural
addition to this design is the installation rail** ([§4.1](#41-the-windows-structure)),
which this brief did not ask for and the owner accepted: the region's structure, its tokens,
its state words, its keys, the collapsed state and the read-only rule it keeps are specified
in §4.1, and a canvas drawn again from this brief draws the rail with the rest of the frame.

**The tone, in one line:** *dense, calm, operator-grade — an instrument, not a marketing
page.* The console is read at a glance, many times an hour, by the person who owns the
bill. Nothing is decorative, nothing is hidden, and no figure is ever dressed up.

**What to draw (five frames, in the one value set that ships, the dark one):** *As first
written, "five frames, each in light and dark" — the light frames are not drawn, since the
owner's Q1 ruling of 2026-10-19 (option (b), dark only) leaves §3.3 as the record of a set
no task owns.*

1. **Live** — the default. The three bands (§4.1) with the installation line, the tab-like
   nav and the panel. Regions in order: the header (`h1.live-project`, mode, day,
   connection); the total block (counted, cap, percent, the cap bar, the notional figure);
   two or three repository blocks, each with its cap bar, its 5-minute burn sparkline, its
   kill banner (draw one **without** a kill and one **with** `KILLED by <who> <when>
   "<reason>"`); under each, four run rows at 24 px with the columns §2.3 lists. Draw one
   row `silent` (amber flag word), one `lost` (alarm flag word), one `queued` (its own
   treatment), and one run whose spend reads `~$`.
2. **Spend** — the header with the range picker and the breakdown switch; three chart
   blocks (`billed model spend`, `~$ subscription list price`, `compute
   (estimated)` — draw them as **separate**, never summed, per the design's own rule);
   the rows table with money, counts and run hours; the totals; and the caps panel with the
   global cap, the defaults and one per-repository override, each naming the **binding
   scope**. Draw one hollow `(partial)` day.
3. **Projects** — eight panels, each with its title, its source line and its state. Draw
   three `read`, one `unavailable` (with its sentence), one `not-built` ("not readable with
   this CLI…"), and the secrets panel with **names, scopes and version counts only** —
   never a value.
4. **Run detail** — the header with its flags, the ledger, the stages table with one stage
   `over deadline`, the pull request shown **as text with no link**, the bounded log table,
   the diagnosis block, and the replay line.
5. **The boundary screens** — the degraded (no-Firebase) screen with caps reading `n/a` and
   the kill affordance absent; the no-binary screen with its install command and the places
   it searched; and the refusal screen (`<h1>Fugaro console</h1>` and one sentence). The last of these is the
   **no-script** case and must look complete anyway.

**The palette to draw in:** §3.2 — the dark set, the one that ships; §3.3 is the record of
the deferred light set and a canvas is not drawn from it (the owner's Q1 ruling of
2026-10-19) — with the semantic roles of
[§2.9](#29-every-state-variant-in-one-table). `--ok` is for measured-and-inside-cap and for
the installation rail's `running` row (§4.1);
`--warn` for attention; `--error` for failed/lost; `--halted` for **killed**, a deliberate
stop and not a failure — it must not read as an error; `--stale` for last-known values.
**Never colour alone**: every state badge carries its word (§3.5).

**What not to draw:** no marketing hero, no illustration, no gradient, no drop shadow, no
rounded card with a title floating in whitespace, no accent used decoratively, no empty box
where a state is missing, no green/red that is not also a word, no sparkline or chart
animation. A screen with nothing to measure draws that fact and one sentence — it does not
draw a zero.

---

## 11. What this document does not decide

- **It does not change the CSP** (§5.5), the protocol handler's refusals, the preload, the
  IPC allow-list or the spawn contract.
- **It does not add a read to the dashboard's bridge, and the rail is drawn without one.**
  The installation rail the canvas draws needs a bounded, schema-checked list read on the
  dashboard's surface to fill a row per stored installation ([§4.1](#41-the-windows-structure));
  the dashboard has none today — `installation.current` is its one installations read, it
  takes no argument and names no id, and the picker's list is on the picker's own channels —
  and this document **does not add it**. No Phase 6 task owns `src/preload/` or the IPC
  allow-list, and the phase draws the rows the dashboard can read. **A multi-row rail is a
  surface decision for the owner, and it is not taken here.**
- **It allocates no D-number and no C-number** (§8); the six questions are the owner's and
  are unnumbered on purpose.
- **It does not approve Phase 6** — that was the first revision's line, and the owner's
  approval of 2026-10-19 is the act that did. *As first written:* the tasks are `UNAPPROVED`
  until the owner answers [§8](#8-open-for-the-owner) and gives the go.
- **It does not decide the home for a theme override** (Q2) — that is a §5.1-rule-10
  amendment and an owner's ruling, not a styling choice.
- **It does not re-record the golden frames.** That is Task 6.6 and it is owner-run.
- **It does not touch `SECURITY.md`, any workflow, `docs/gcp-live-checklist.md` or any
  check number.** No check number is taken by this work: nothing here is a live step.
- **It does not name a font it does not have.** The stacks are `system-ui` and
  `ui-monospace`, no face is downloaded, and the CSP's `font-src 'self'` stays unused.
