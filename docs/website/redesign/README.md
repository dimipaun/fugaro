# Website redesign

**Status: the plan is implemented but for its last part, and items 2, 3 and 4 of
[the changes spec](changes-spec-2026-10-18.md) are implemented in this branch's pull
request.** Eight of the plan's nine parts are merged on `main` — `P1` and `P2` (#568), `P4`
(#572), `P3`, `P6` and `P7` (#575), `P8` (#574) and `P5` (#603) — and **`P9`**, the design
record and the "after" set, is the one still to do. This branch's pull request is later than
all of them, and it changes what they built — the hero's headline and its example, and the
four-step flow — which the readings under "Implementing items 2, 3 and 4" below record.

The site itself **is** built: `website/` holds an eleven-route Astro site whose hermetic
gates pass (300 tests, `npm run check`, on Node 22.22.0, measured at `08d57ed4`). The three documents of the
first line of work said the opposite, in the present tense — [`../README.md`](../README.md)
`:3` and `:5`, [`../reconciliation.md`](../reconciliation.md) `:3`,
[`../implementation-plan.md`](../implementation-plan.md) `:3` — which is reconciliation
finding **RF1**, and `P1` corrected exactly those lines: each of them now states that the
site is built, so the finding is closed and this branch is measured against the site, not
against a plan for it.

Owner: **@Dimi**. Date: **2026-10-18**. Source: the brief as given to this line of work,
committed alone in this branch's first commit.

## The documents

| file | what it is |
| --- | --- |
| [spec.md](spec.md) | **The brief, as given to this line of work.** Nothing in this directory edits it: it is the thing the rest answers. It is a one-paragraph condensation of the owner's longer text of 2026-10-18, with the same six items, process and "Done when" list; where the longer text says more (item 2: the current colour "looks good but does not match the logo"; item 4: the page "is currently blocks of text that do not explain much"), nothing here contradicts it |
| [changes-spec-2026-10-18.md](changes-spec-2026-10-18.md) | **The second brief, agreed 2026-10-18.** Where [spec.md](spec.md) asked for the site to be rebuilt short and visual, this one changes four of the things that rebuild decided: the headline and the copy's vocabulary (item 2), the homepage's example (item 3, an agent chat rather than a terminal the user types into) and the diagram (item 4, one bold four-step flow, no duplicated "how it works" text). **It is later, and where the two disagree it wins.** Items 2, 3 and 4 are the part this line of work implements — one pull request — and item 5's `fugaro watch` screenshot is made separately (see the rulings below); the document is committed as it was agreed, and no part of it is implemented by being written down |
| [reconciliation.md](reconciliation.md) | What the brief asks for, set against what the built site does — findings `RF1`–`RF26`, each **Wrong**, **Exists**, **Gap**, **Rule** or **Open**; the conflicts with the first line of work's decisions (`D90`–`D109`, notably D104's palette and D105's budget) and Global Constraints 6 and 12, with the CSP and the origins gate, and with the content pipeline's generated-file banner (RF8); the logo's sampled hue, the derived palette and its WCAG contrast over every asserted pair; the measurements of the current site; and decisions `D180`–`D192` |
| [implementation-plan.md](implementation-plan.md) | Nine ordered, independently mergeable parts (`P1`–`P9`), each with a runnable verification and the **named** test change wherever a budget, a gate or a pinned number moves; plus the live checks (`70a`–`70c`) the brief's "Done when" lines need |
| [current/](current/) | The **"before"** set's README: every route at 1280 and 390, with the measurements. **The images are not committed** (they are 7.8 MiB and regenerable); see below |
| [capture-screenshots.mjs](capture-screenshots.mjs) | The script that takes the screenshots and the measurements. It installs nothing into `website/` and `website/package.json` is untouched by it |
| [sample-logo-hue.py](sample-logo-hue.py) | The script that samples the logo's hue (reconciliation §5.1); Pillow only, prints numbers |

## The screenshots: regenerate them, they are not in the tree

The "before" set was taken on 2026-10-18 and removed from the branch before merge;
`docs/website/redesign/current/*.jpg` and `docs/website/redesign/after/*.jpg` are in the
root `.gitignore`. To make the "before" set again, from the repository root, on the commit
whose site you want to capture:

```sh
(cd website && npm ci && npm run build)
npm install --prefix /tmp/fugaro-pw playwright@1.49.1
/tmp/fugaro-pw/node_modules/.bin/playwright install chromium
PLAYWRIGHT_PREFIX=/tmp/fugaro-pw node docs/website/redesign/capture-screenshots.mjs docs/website/redesign/current
```

It writes 22 JPEGs (eleven routes at 1280 × 900 and 390 × 844) into `current/` and the
measurements to `/tmp/website-screenshots.json` (a second argument changes that path). The
"after" set is the same command with `docs/website/redesign/after` as the argument.

## The decisions

Decisions are allocated by the owner in blocks, and the allocation is recorded in
[`../mobile/README.md`](../mobile/README.md): `D90`–`D109` is the site's first line of work,
`D110`–`D129` mobile, `D130`–`D159` the credentials service, `D160`–`D179` the mobile
functional spec. This line of work's thirteen rows are therefore **`D180`–`D192`**, the first
block above the highest allocation — proposed for the owner's confirmation, listed with
their recommendations in [reconciliation.md](reconciliation.md) §9 and repeated in the pull
request body, because a decision taken silently in this branch would be the one kind of
change nobody can review.

## What this line of work may not do

It is **documents only**; the parts the plan describes are later pull requests, each
starting on the owner's go. Nothing under `.github/workflows/` is created or edited (a
workflow that were needed would be proposed under `docs/pending-workflows/` for the owner
to copy). No cloud action of any kind, no secret read or written, and `belong`, EdgeWeb
and EdgeServer are never touched. The strict CSP (`script-src 'self'`, no remote origins)
and the origins gate stay; the JavaScript budget and the 0-byte-JS prose change only
deliberately, with their tests, as the owner's ruling of 2026-10-18 requires.

## Owner rulings (2026-10-18)

The owner accepted every recommendation of D180 to D192, as written in `reconciliation.md`:

- **D180** the accent is the logo's hue, `#17A8E6`, as a fourth palette file; **D181** the warning colour moves to amber `#F5A524`.
- **D182** the menu is three groups of three (Product, Reference, Project), a drawer on mobile — **amended on 2026-10-19 by the owner's brief (item 1): the three group labels ARE the menu on a phone, and the drawer and its one toggle are retired** (see "The 2026-10-19 brief" below). What D182 decided about the *groups* stands: three groups of three, Home as the wordmark, Privacy in the footer.
- **D183** the JavaScript is one first-party file, `website/public/scripts/site.js`; no inline script, no CSP edit.
- **D184** the homepage animation shows the README's documented sequence, held to the file at build time, captioned as the documented sequence.
- **D185** How It Works uses the brief's four steps, each captioned by a quoted line.
- **D186** a build gate refuses the listed provenance phrases; **D188** the lead is at most 40 words on every page and the whole page at most 250 words (not Docs, Security, Changelog) — amended 2026-10-19: the privacy statement's own ceiling is derived from its own count, and every other page stays at 250 — amended 2026-10-21: the number is 500, the bold redesign's homepage being five blocks and 468 words (the reading below).
- **D187** the fold is checked by the word ceiling plus one owner-run live check (70b).
- **D189** long-page summaries are rendered from files; **D190** the design lives in the owner's tool, the repository keeps `design.md`, screenshots are regenerated and never committed; **D191** the redesign goes first; **D192** the byte budgets are re-derived in the commit that ships the script.

## Owner rulings (2026-10-18), the changes spec

Four rulings the owner made on [`changes-spec-2026-10-18.md`](changes-spec-2026-10-18.md)
(items 2, 3 and 4 of which this line of work implements). They are rulings about that
spec, not new decisions with numbers of their own; the numbered blocks in
[`../mobile/README.md`](../mobile/README.md) are the owner's to allocate, and none is taken
here.

- **Dark only, for now.** There is no light theme in this run. What the spec's "dark only
  (see the ruling above) but built from theme tokens" asks for is the other half: the
  diagram — and everything the changes touch — is built from the palette's tokens rather
  than from literal colours, so a light palette is one change (a palette file and the
  `PALETTE_IN_FORCE` line P2 established) and not a rewrite of the components. The spec's
  "light and dark" is **deferred**, not dropped.
- **The `fugaro watch` screenshot is made separately** (item 5). It comes from a recorded
  fixture rendered through the real renderer, in its own task; this run leaves the slot for
  it in the markup and commits **no image at all** — no `<img>`, no PNG, no JPEG. The rule
  that no screenshot is ever committed (D190) is unchanged.
- **The tagline change reaches the CLI, and the plugin skills, only in 0.9.0.** What
  changes in this run is the website and `README.md` (item 2's own words: "website and
  README only in this run"). The wording stays, untouched, where it is generated from or
  pinned to the CLI: `internal/cli`'s root command help (`Fleeting cloud workers for coding
  agents`), the generated `website/content/cli-reference.md` that renders it, and the three
  plugin skills that carry it (`plugin/skills/routing`, `plugin/skills/working` — its
  `reference/` files as well as its `SKILL.md` — and `plugin/skills/parallelism`). The two
  release surfaces that carry it as well — `.goreleaser.yaml`'s cask description and
  `.claude-plugin/marketplace.json`'s plugin description — are likewise not this run's:
  they are the CLI's release metadata, not the site's, and the same 0.9.0 change moves
  them. The acceptance grep therefore still matches `website/content/cli-reference.md`, and
  that one match is the ruling, not an oversight: the file is generated by
  `go run ./internal/clidoc` from the CLI's own help, and editing it by hand is the
  forbidden change (the generated-file rule). The list above is held to the tree rather than
  trusted: `website/test/wording.mjs` reads each skill's own `SKILL.md` and fails unless the
  skills carrying the word are the three named here, so a skill that gains or loses it — in
  0.9.0, or by accident — moves this sentence in the same commit. **The same test names the
  one file of the site's tree the word stays in while it waits for 0.9.0** —
  `FILES_THAT_KEEP_IT = ['content/cli-reference.md']`, dated 2026-10-19 — and holds that list
  to `website/content` as well: the generated documents are swept with the rest of the tree
  rather than left outside it, the one exception is a named path with the reason beside it,
  and the case fails in both directions, so a second generated document that starts carrying
  the word is refused and the exception cannot outlive the 0.9.0 change that removes the need
  for it.
- **The command this work renders is `fugaro upgrade`, because that is the command.** There
  is no `fugaro update`: the command that pins the plugin and moves the job images onto a
  release's base image is `fugaro upgrade` (`README.md` `## Upgrading`), which is the name
  the changes spec already uses ("except commands like `fugaro watch` or `fugaro upgrade`")
  and the one the pages this run touches render (`website/data/install.yaml`'s
  `fugaro upgrade --local`, `website/src/pages/getting-started.astro`). No wording was
  changed to arrive at it.

## Implementing items 2, 3 and 4: what those three items settle

Items 2, 3 and 4 of the changes spec are implemented in this branch's pull request. **Every
reading the spec does not spell out is recorded here**, one bullet each, so that a later
reader of the site does not have to guess them from the markup — and the list is not
counted, because each of the three fix rounds has added bullets to it and a number written
here goes stale the way the byte budgets did before D192.

- **Item 4's four steps are the spec's, and they supersede D185's names.** D185 ruled that
  How It Works uses "the brief's four steps, each captioned by a quoted line"; item 4 of the
  changes spec is the later brief and names them itself (`1 Your agent`, `2 Fugaro`,
  `3 Ephemeral cloud job`, `4 Pull request`), so the flow is drawn from those names, one
  line each — and `website/test/home.mjs` reads them out of the spec's own item 4 rather
  than out of a copy in the component, so the picture and the ruling cannot drift. **That
  bullet is item 4 as it stands on the homepage, and item 4's second half is amended by the
  owner's draft of 2026-10-19**: the HOW IT WORKS page opens with the run's own diagram rather
  than with this flow, and the amendment is recorded in "The owner's 2026-10-19 approval of the
  run's diagram" at the end of this document.
- **The two facts D185 put in the captions move to the detail.** Item 4 asks for "one short
  line each" in one bold picture, so neither the caches nor the trust boundary is a caption:
  the caches stay in the README's own step 2, and "The trust boundary is the container, not
  the process." (`docs/design/v1.md` §6.1) is the note under the How It Works detail. Both
  facts are still on the site; neither is inside the four boxes.
- **The chat's pull requests are text, not links.** Item 3 asks the agent to report back
  with pull request links, and a run that has not happened has no pull request to link to:
  a URL written for the page is the invented fact this line of work may not publish (Global
  Constraints 6 and 9). The three results are named as the batch's own pieces (`Pull
  request 1 of 3`), and the sentence above them is the `working` skill's own line about what
  a run ends with. Every other line a turn *speaks* is quoted the same way — the request from
  item 3 and the agent's two answers from `plugin/skills/parallelism/SKILL.md` and
  `plugin/skills/working/SKILL.md` — and the build holds each line to its document, in that
  document's order. A copy of the repository staged for a test build therefore carries
  `plugin/skills/`. **The launch is not one of those lines, and this reading used to say it
  was**: it claimed "the launch from the invocation the `parallelism` skill documents, with
  its own `<name>` and `<id>` placeholders", and what the page ships is the illustration's own
  label, `Fugaro: launching 3 jobs` — a status line no skill prints, which is why the caption
  may not call the figure's lines quotations and why `website/test/home.mjs` refuses a status
  line that *is* a sentence of the document its turn cites. (The caption is neither: it is the
  page's own sentence, and the next reading is what it may say.)
- **The chat's caption says what the figure is, not where every line came from.** The caption
  claimed "every line is the repository's own words, from the skills the agent reads", and
  that is false about two of the things the figure prints: the request is **item 3's own
  example** of one (a line of the spec, not of a skill), and the three `Pull request n of 3`
  labels are the illustration's own, so the last review's finding asked for the wording to be
  corrected. It reads "An illustration, not a recording of a session: the agent's lines are the
  skills Fugaro installs, and the labels are the picture's." — the figure's own label, and the
  one sentence in it that is neither a quotation nor a result label. (Review round 5 cut the
  caption to this one sentence, which is why the request and "the shape of what your agent does
  and reports" are gone from it — the quote above is the sentence the site ships.) The rule above
  is therefore stated of the lines a turn *speaks*, which is the part of the figure a document
  vouches for. `website/test/home.mjs` holds the caption to the shape it has to keep rather
  than to its wording — the word "illustration", the words "not a recording" and the word
  "skills", and no claim that the figure's lines are quotations (the words it refuses are
  "own words", "quotation", "quoted" and "verbatim") — and the same case checks that the three
  results are not lines of the document the turn cites, and compares this record's own quote of
  the caption against the caption the built page ships, so the sentence quoted here cannot
  outlive the sentence on the page. `website/test/wording.mjs` refuses the phrases the false
  sentence was made of — on every built page and in the site's own sources, with a case that
  plants the caption back and proves the refusal, so the claim cannot return through a rewrite.
- **Item 4's row was too narrow for the names it prints, and this round's answer to that was
  superseded.** The four cards share the row, so a card is a quarter of the column less the
  arrows between them: at 48 rem — the breakpoint the chrome switches at — a card's content
  box is about 100 px, which is narrower than the word `Ephemeral` in the spec's own third
  step name, and the name was held on one line beside a `flex: none` number, so the flow could
  not render its own labels at any width below about 79 rem. **This round answered with a row
  at 64 rem and a wrapping name, and the third fix round replaced both halves of that** (the
  reading below): the row starts at **72 rem**, which is also where the page stops growing, so
  it is drawn exactly where a card's content box is at its widest — **246 px** against the
  longest name's estimated 225, which a headless Chromium over the built site draws 222.7 px
  wide — and `Ephemeral cloud job` is on one line there, like the three names
  beside it; the number still sits above the name inside the row, and below 72 rem the four
  steps are a two-by-two, where a card is half the column and the name is free to wrap. The
  arithmetic is not a reading of a screenshot: `website/test/base-styles.mjs` computes the
  card's content box from the built stylesheet — the page's column, the four cards, the three
  gaps and the card's own padding and border, every number read rather than typed — and
  refuses a row where the longest name does not fit one line (with a margin on top of
  `textWidth`'s per-character estimate), and one where even the longest word does not fit the
  card, so a name that outgrows its card or a row that starts earlier fails there naming the
  width. (That arithmetic reads the name's box as **234 px** where the page's own box is 246:
  it counts both `main`'s gutter and the wrapper's, and the `main:has(> .hero)` shortcut drops
  `main`'s on this page — the narrower of the two readings, which is the safe direction for a
  rule that asks whether a name fits, and the reason the number here is the page's and not the
  check's.) Whether the picture then *looks* right at 1280 px is the owner's check (70a–70c), as
  D190 requires, and this line of work cannot see it.
- **The arrows point the way the run travels, and the fix round is where the sign was set.**
  The arrow is drawn pointing down (the shaft is `M12 1 V 25`), a positive angle in CSS turns
  clockwise, so the row's `rotate(90deg)` pointed every arrow back at the step before it — the
  four steps drawn in reverse by the one mark that says which way the run goes. The turn is
  `rotate(-90deg)` now, and `website/test/base-styles.mjs` reads the drawn direction out of
  the markup and the rotation out of the built stylesheet at the breakpoint, at the page's
  maximum width and below the breakpoint, where the arrow has to point down the column. A
  rotation the check cannot read is refused rather than assumed.
- **The byte budgets are re-derived over the merged build (D192).** Every item here changes
  what the artifact weighs — the terminal's transcript goes, the chat comes, `site.js` loses
  bytes, the flow's stylesheet changes — and then `main` came back into the branch, with
  the menu's hover-open and the six corrections that day and the next made to it, which moved
  the same script again. So the figures were derived once, over the build the merge produces,
  which is the only account of them that is true of the site that ships. **The three figures
  themselves are written in `website/README.md`** ("The gates"), each with the rule it is
  derived by, and `website/test/budget.mjs` reads them from there and holds each to the build
  it makes itself; **this document states none of its own**, and that is the correction of
  2026-10-19 — a figure copied into a document here drifted the last time the build moved,
  because nothing reads it there. What follows is the **record of the rounds that moved the
  figures**, with each round's own deltas, re-derived in the commit that shipped the bytes
  (D192), rather than a statement of what the site weighs now. The fix
  round after the merge re-derived all three over the build that branch ships — the chat moved
  above the diagram, its turns are drawn as alternating bubbles, three of the flow's four cyan
  card edges became the site's separator and the footer's link label is the repository's name
  rather than its URL — and the round after *that* re-derived the site figure once more, again
  lower: the install section's own top padding came off, because the flow above it
  already carries the gap between the two blocks (the fifth round below). The page weight did
  not move with it — the heaviest page is the docs hub, and that page renders neither the flow
  nor the install section — and the JavaScript is untouched. And the round that followed the
  fifth moved both figures the other way, by moving the two declarations that make a command
  wrap onto `base.css`'s `.command-block > pre`, which every page inlines: the copy control
  shares the command's row now, so the rule belongs to every command block on the site rather
  than to the homepage's install class alone (the fifth round below). It is the
  sixth re-derivation on this branch's side — the second fix round moved the homepage's order,
  the flow's treatment, the chat's launch line, the install command's wrapping and the chat's
  turns out of the measure's cap; the third's four commits moved the site figure and the page
  figure with it; and the fourth's fix for the two-by-two's wrap arrow moved it again on the two
  pages that draw the flow — and each was re-derived in the commit that shipped the bytes. **And
  this round is the screenshot landing**, which is the sixth: the site figure alone moves,
  because PR #622's two `public/images/` assets (the capture and its transcription) were already
  in every build of the site and unplaced, and the `<img>` that puts the picture on the homepage
  adds its markup. The page weight does not move — the picture is on the homepage, and the
  ceiling is derived from the docs hub, which renders neither the chat nor the screenshot — and
  nor does the JavaScript. **And the last round is this branch's own two changes from the
  owner's brief of 2026-10-19**, measured against that build file by file: the menu's drawer out
  (item 1) leaves each of the eleven pages lighter, the base stylesheet every one of them inlines
  being that much shorter, and takes bytes off the script; the review round that followed put the
  script's own comments back in step with the menu, which is bytes back onto it. The page weight
  moves with both: the docs hub inlines the stylesheet and fetches the script. The console's link
  of item 6 rendered nothing in that round, its flag being off. **And the round after it is the
  console's release** (W3.1), which turned `features.console_downloads` on and moved the site
  figure alone: the console page's downloads section — each
  artifact's published name as a link, the release's own SHA-256 beside it, and the command
  that checks a download against the release's sums file — Getting Started's console option
  and the hero's link to the console. The page weight and the script stay where they were: the
  heaviest page is the docs hub, which renders none of the three. **And a later round is the
  console's screenshot slider** (the owner's feedback of 2026-10-19: a slider at the top of the
  console page, data-driven from `website/data/console-screens.yaml` and shipping empty until
  the owner takes the pictures), which moved the site figure alone once more, by the
  component's stylesheet: Astro inlines it into the console page that imports it, whether or
  not the slider renders, so the bytes are in the build while the list is empty and while it is
  not. The page weight and the script stay where they were: the console page is not the
  heaviest page, and the slider ships no script at all. (**The list is no longer empty**: the
  owner's feedback of 2026-10-21 — "console page: screenshots missing" — is the four fixture
  captures under `website/public/screenshots/` and the four rows that render them, which moved
  both figures and made the console page the page the page-weight ceiling is derived from. The
  round is in `website/README.md`'s list, with the numbers.)
  **And the round after *that* is the run's own timeline** (the owner's feedback of
  2026-10-19): the site figure alone moves once more, by the rail, the connector, the rule that
  stops it at the last step and the markup that places them, on the one page that renders a
  run — the page weight and the script stay where they were, the heaviest page being the docs
  hub, which renders no step.
  **And the round after *that* is the caps-and-costs rework** (the owner's brief of
  2026-10-19, the page's own item): the site figure alone moves, on that one page — the table of
  what a job costs and its provenance note, rendered from `website/data/typical-costs.yaml`,
  against the per-command scope sentences the page no longer carries and the disclosure the
  commands sit behind — and then the review of that rework, the scroll box the table's nowrap
  columns need on a phone and the page's own rule for its lead. The page weight and the script
  stay where they were, the heaviest page being the docs hub.
  **And the round after it is the 2026-10-19 changelog brief** (its own section below), which moved the site figure alone:
  every release now renders its product summary and wraps the release file's own Highlights in
  a `<details>` block — a summary and a notes label per release onto `/changelog/` — while the
  "not yet released" labels the tag rule drew in the gates' build and the retired "newest
  releases" block are gone. The page weight and the script stay where they were: the changelog
  page is not the heaviest page, and nothing outside it changed.
  **The rounds after it are the homepage's** (the owner's feedback of 2026-10-19), each of which
  moved the site figure alone. **The prompt line**: the request the hero renders under the
  headline, the `>` mark in front of it and the two rules that draw it as the chat's own bubble —
  the three files it changed all render on that one page, so the page weight and the script stay
  where they were. **The benefits**: the section, its four names and four lines and the rules
  that draw the grid, all of them on the homepage; the page weight and the script stay where
  they were, and the words the section costs that page are what moved D188's word ceiling from
  250 to 300, in the same commit (the reading below). **The pull request's caption**: the
  caption's line and the `data-source` it cites with it —
  `docs/git-providers.md#Checkpoint pushes (git.pr.checkpoints)` where the step used to cite
  `README.md#How it works` — on the two pages that render the flow, the homepage and How It
  Works; the page weight and the script stay where they were, the docs hub rendering no flow.
  The caption is two words shorter than the one it replaced, so the homepage measures 294 words
  where the benefits left it at 296 (the reading below). **The review of round 6**: the
  benefits' gutter and the room below it are declared in `Benefits.astro` now, the file that
  renders the section, where the page's rule named the class and never reached the element —
  Astro scopes a page's selector to the elements the page renders, and the section is rendered
  by a component. The page weight and the script stay where they were.
  **And a later round is the header's layer**, from the owner's reading of 2026-10-19 ("How it
  works: menu hidden by the blocks on mobile"): the header and the panel a group hangs below it
  were positioned boxes with no `z-index`, so a block drawn after the header and taken out of
  the flow — the diagram's steps, which `position: relative` is what their arrows are placed
  against, and the run's timeline steps, positioned for their connector — painted over an open
  menu in tree order. `tokens.css` names the scale (`--z-header`, `--z-chrome`) and `base.css`
  puts `.site-header` on the header's layer, which makes it a stacking context. Every one of the
  eleven pages grows by the same bytes — the stylesheet they all inline — so the site figure and
  the page weight both move, the docs hub inlining the same stylesheet and being the heaviest
  page. The script is untouched.
  **And a later round is the run's own diagram** (the owner's approval of the draft *"How it
  works, draft 2 (diagram)"* on 2026-10-19, *"build it"*, which took the timeline above out of
  the page): the site figure alone moves, on the one page that renders a run — one inline
  `<svg>` per width, the box, pill, arrow and band rules that paint them from the palette's
  tokens, and the two lists of the repository's own lines under the picture — and then the
  review of that round moves it once more by the two rules that say where the drawings change
  over and how wide the stacked one is drawn. The page weight and the script stay where they
  were: the heaviest page is the docs hub, which renders none of it, and the drawing is markup
  rather than script. **That round also re-derived all three figures from the gates' own build
  rather than from its own delta**, because the base they carried was stale: the generated CLI
  reference had been regenerated a round earlier without re-deriving the numbers, and the
  correction is recorded in `website/README.md` with the round.
  **The figures themselves are the ones `website/README.md` writes, and they are written
  there and nowhere else**: `website/test/budget.mjs` reads them from there and holds each to
  the build it can measure, and a derivation that names another build fails, which is what
  stops the page and the site figures drifting down as pages grow (they had — page weight and
  whole site still named the build of 2026-10-18 after four parts added bytes to it).
  **Which build that is, is the 2026-10-19 correction**: the figures are derived over the
  gates' own build of the site — a copy of the repository in a temp directory, built with the
  site's Astro by `test/lib/build.mjs`'s `buildCopy`, which carries no `.git` — and not over
  `website/dist`, which is the checkout's own build. The reason was that one page of the site
  read the checkout's git state: the changelog rendered each release file's "not yet released"
  state where `git tag --list` found no tag (`src/lib/changelog.mjs`), so the same commit built
  to two site figures a changelog page's own bytes apart — the larger in a checkout with no
  tags, `website-preview.yml`'s default checkout and so the deployed site, and the smaller with
  the tags this repository's clone carries. A figure derived from one was refused by the other,
  which is what happened: the gate could not measure the build `main`'s README named, and
  `website-preview` could not deploy. **That dependence is gone** (the changelog brief below:
  which releases are published is the plugin's own version, not a tag), so `website/README.md`
  writes the two shapes as one figure and `website/test/budget.mjs` refuses the pair if they
  part again; the derivation stays where it is all the same — a copy built by the gates is the
  build they can measure in every environment. A derivation is likewise not held to
  whatever artifact a run was pointed at: `FUGARO_GATES_DIST` is how `console-option.mjs` runs
  the artifact gates over a copy with the console's flag flipped off — the state the checkout
  built until the console's release turned it on — and that copy is a different build rather
  than another copy of this one: turning the flag on added the console option's markup to the
  pages that render it (the console page's downloads section, Getting Started's option and the
  homepage's call to action), so the copy that turns it back off is the smaller build, with the
  page weight where it was (2026-10-19).
- **The hero offers one command to copy, and the screenshot's place is empty.** Item 3's
  acceptance is that the only command a reader is likely to type is `fugaro watch`, so the
  `fugaro run` transcript the homepage carried (`Terminal.astro`, D184) is removed from it —
  `fugaro run` stays in the documentation as the manual path — and the snippet that keeps
  `fugaro watch` is a command block like every other one on the site, with its own copy
  control and the skill's own line about what the screen shows under it. The agent's launch
  is a quiet line inside a turn rather than a command block, because the reader did not type
  it. Item 5's screenshot is a separate task: this pull request leaves
  `[data-slot="watch-screenshot"]` right after the chat and commits **no image**, and
  `website/test/home.mjs` held that slot to being empty and the homepage to its one picture
  (the wordmark) — **the state this file describes of that round**; the image landed in the
  slot later, with the case rewritten to hold it there (the section on the screenshot below). **The watch bar is a command block like every other one on the site, and the
  control sits beside the command rather than above it** — which the third fix round made the
  rule for the whole site rather than this block's exception: `base.css` lays every
  `[data-copy]` block out as one reversed row (`flex-direction: row-reverse`, the command
  taking the width the control leaves), the control comes first in the shared markup
  (`src/lib/copy-control.mjs`), and the watch bar's own row rule is gone (item 3c, below).
  `website/test/base-styles.mjs` reads every `[data-copy]` block's own rules and refuses one
  whose control is laid out first or wrapped above the command.
- **The second fix round is where the homepage became the page item 4 asks for.** The first
  fix round was about claims (the caption, the arrows, the watch bar's control); the second
  is the one an independent review of the built site asked for, and it changed the look, not
  the prose: the homepage's order is the headline, its subline and the two calls to action,
  then the flow **directly under them as the page's centrepiece** (24 px semibold step names,
  one 17 px line each, a 2 px accent border under a 6 px accent top edge, 2 rem bold numbers,
  3 px arrows), then the chat, then the `fugaro watch` block with item 5's empty slot, then
  the install paths last and quieter. The flow's cards are a quarter of the page's column
  because its list opted out of `base.css`'s measure in the first round; **the chat's turns
  are an `<ol>` too, so the same cap drew its three turns two across with a hole beside the
  third** — found by measuring the built page in a browser rather than by reading it, and
  fixed the same way (`max-width: none`), with `website/test/base-styles.mjs`'s new
  `chatRowFailures` reading the smallest column and the gap out of the grid rule and the cap
  out of the cascade. (That gate and the two-column chat are both gone again: the third fix
  round made the chat one conversation card at the measure's own width, and the fourth round's
  `chatConversationFailures` read that card's stylesheet — and the card, the chat and that gate
  all left the site with the owner's draft of 2026-10-19, which is the reading at the foot of
  this file. The history is kept here because the round that found the hole was the round that
  measured the page rather than read it.) The `go install` command, which the same review found clipped at
  `...@late`, wraps now (`white-space: pre-wrap`, `overflow-wrap: anywhere`) on both install
  cards, and `installCommandFailures` refuses a command block that clips instead. The agent's
  launch is the status line `Fugaro: launching 3 jobs` rather than a command block, which is
  what item 3 asks for and what `website/test/home.mjs` holds it to. The measurements this
  round was decided on are numbers from a headless Chromium over the built site (1104 px of
  row, three 360 px chat cards, no document overflow at 1280 or 390 on either page); whether
  the page then *looks* right is the owner's check, as D190 requires, and no screenshot is
  committed.
- **Item 2's wording still reaches the plugin skills, and that is deferred.** The review's
  last finding is the phrase "fleeting cloud worker" left in `plugin/skills/working/SKILL.md`
  and its description. It is the same 0.9.0 change the ruling above describes: the tagline
  reaches the CLI, the generated CLI reference and the three plugin skills in 0.9.0 and not
  in this run, and `website/test/wording.mjs` holds the tree to exactly that list. Nothing
  was changed here, and the finding is recorded as deferred rather than done.
- **The third fix round is the second review's, and each of its five findings is a rule.**
  The review built the site and looked at it, found the second round's two blockers fixed
  (the cards equal height at 270 px, the flow inside the first 900 px) and asked for these:
  - **the copy control has one placement, and it is beside the command** (item 3c). The
    install cards put it above the code block and the watch bar beside it, so the same
    component read two ways on one page. `base.css` now lays every `[data-copy]` block out as
    one row — `flex-direction: row-reverse` with the command taking the width the control
    leaves (`flex: 1 1 auto; min-width: 0`) — and the watch bar's own row rule is gone.
    `website/test/base-styles.mjs`'s `copyBlocks`/`sideBySideCopyBlocks` read every block's
    own rules and refuse one whose control is laid out first or wrapped above the command.
  - **the chat is one conversation card, and its caption is under it** (item 3a and the
    caption's place). Three cards with dead space and a `You` line floating mid-card became
    one card of three turns: a fixed 7 rem speaker column from 35 rem up (a `max-content`
    column resolves per row and aligns nothing), one column below it, the agent's turn marked
    by the accent's own left border. The caption moved out from under "Watch it run" to
    directly under the conversation, and the whole watch block — heading, item 5's slot, the
    command bar, the note — is now a sibling of the figure rather than inside it, so the
    caption can be the figure's last child. What read that markup was `website/test/home.mjs`,
    not the stylesheet gate: it read the caption as the figure's last child, the caption's
    words (`chat-caption`, an illustration and not a recording), and the slot as the element
    after the watch heading. `chatConversationFailures` read the stylesheet — the one-column
    turn list, the card, the measure's cap, the speaker column and the two speakers' sides —
    and nothing about the caption. The chat is not on the page any more (the draft of
    2026-10-19, below), so neither is that case; the slot the picture is in is the slider's
    now, and `website/test/home.mjs` reads it there.
  - **the empty slot takes no page** (item 3b). `[data-slot="watch-screenshot"]` with no rule
    of its own was a box of blank page between the conversation and the command bar, which is
    the ~140 px the review measured. `.chat-shot:empty { display: none }` takes it out until
    PR #622's image lands — and it is kept now that the image is in the slot, because it is the
    rule that keeps a placeholder from coming back — and `chatSlotFailures` refused a slot rule
    that gives it height, padding, a border or a background, and held the rule that sizes the
    image (`.chat-shot > img`) to scaling it: a width that is a percentage of the column and a
    height that follows the file's ratio. The slot moved into the slider's "See it" panel with
    the draft of 2026-10-19 and its class moved with it (`.watch-shot`); the same rules, read
    by `website/test/base-styles.mjs`'s `watchSlotFailures` under the new name, are what hold
    the picture to the column it sits in.
  - **How It Works has one left edge, and room under its title** (item 3d). The detail section
    carried `var(--space-md)` of inline padding of its own on top of `main`'s gutter, so "The
    run, step by step" and the five steps were 24 px right of the `h1` and of the flow between
    them; and `base.css`'s 12 px heading margin left the flow almost against the title. The
    padding is gone from `.run-detail`, and the room under the title is the page's own rule
    (`.page-title { margin-bottom: var(--space-xl) }` on `how-it-works.astro`, not on the
    shared component, which the homepage renders early on purpose). The gate reads the inset
    back out of the cascade and refuses a page whose top-level blocks start at two left edges.
  - **the flow is a stack, a two-by-two and a row, each in its own band of width** (item 3e).
    The 768 px band has been a two-by-two since the flow's own work, and nothing held it
    there: `flowLayoutFailures` reads the blocks that start the row and the two-by-two, so a
    stylesheet with no two-by-two at all — four cards stacked, the picture the review counted
    at over 400 px — passed every check. `flowLayoutAt(css, width)` now reads the shape in
    force at a width out of the cascade, and `flowBandFailures` reads the six widths that are
    the bands' own edges and the two widths the reviews measured: 559 and 560 (the review's
    "about 560 px", which is 35 rem, read exactly), 768, 1151 and 1152 (71.98 rem), and 1280.
    A layout rule inside a query with no viewport width in it is refused too, because that is
    a band at every width. Each of the five cases plants its defect and proves the refusal.
  - **the row's cards are one height, and each name is on one line** — the two blockers the
    review of this branch measured **at 1280 px**, where the four-across row is the layout in
    force and the cards the paragraph above counts at 270 px are the 768 px two-by-two's.
    `.flow-step` set `align-items: center`
    inside the row, so a step's cross axis — its card — took the card's own content height and
    the fourth card ended about 30 px above the third's: the row now stretches its steps, each
    step stretches its card, and the card's content is packed from its top. And the row starts
    at **72 rem** rather than the 64 rem the round before it chose — where the page stops
    growing, and where `Ephemeral cloud job`, which wrapped onto two lines at 64 rem while the
    three names beside it fitted on one, has a 246 px box against its estimated 225 (the
    reading above says where the check's own 234 px comes from, and why the page's box is the
    one to quote). That is
    the correction the reading above points at, and below 72 rem the four steps are the
    two-by-two, where the name is free to wrap again. `flowLayoutFailures` reads the card's
    content box out of the built stylesheet, holds the longest name to one line at the row's
    width and the longest word to the card, and refuses a row whose items are aligned to
    anything but `stretch`; every one of those refusals has a case that plants the defect and
    proves it fires.
  Nothing in this round was verified visually: this run cannot see an image, the rounds before
  it are what a browser measured, and whether the page now *looks* right is the owner's check
  (70a–70c), as D190 requires.
- **The fourth round is a code review's, and it found the two-by-two's wrap arrow pointing at
  the wrong step.** The band was `1 2` over `3 4` with step 2's arrow drawn below step 2 — and
  in that grid the cell below step 2 is step *4*'s, so the arrow landed on step 4: the picture
  read `1 -> 2 -> 4`, step 3 had no arrow pointing into it and step 4 had two. **Nothing
  caught it because the gate read the angle an arrow is turned to and never the box it is
  drawn in.** "Down" is the right angle for the step the arrow was meant to point at, and the
  arrow was drawing it at the wrong cell: an angle alone cannot tell two cells apart. The fix
  is the reviewer's own second option — the bottom row is `4 3`, so step 3 sits under step 2
  (where the down arrow lands on it) and step 4 sits beside step 3, which is why step 3's
  arrow turns back along the row (`rotate(90deg)`, the mirror of the `-90deg` the rightward
  arrows use) and points at step 4. The DOM order is untouched: it is the run's order, and it
  is what a screen reader reads — only the drawing is a snake. The gate now reads both halves
  of the question. `flowCells(css, shape, width)` reads the grid's own
  `grid-template-columns` and each step's `grid-area`/`grid-row`/`grid-column` (and auto-places
  the steps the stylesheet does not place, the way a browser does), so "the next step" is the
  cell it actually occupies in that shape rather than a rule in the test; `towards` names the
  side of a card that cell is on; and the arrow's own box — its `top`, its `left` and the sign
  of its margin — has to leave from that side *as well as* point that way. A placement in a
  shape the reader cannot evaluate (`span`, a named line, half a placement) and a box it
  cannot place against the card are refused rather than guessed. Four cases plant their
  defects: the grid left in its own row-major order, which is the defect itself; a wrap arrow
  drawn on the far side of its step; an arrow centred over its own card; and a placement
  written with a `span`. The whole-site budget moved with the fix, re-derived in the same
  commit (D192), which is where the figure is. This round was a reading of the stylesheet and of
  the gate, not a look at
  the page — the same limit as the round above.
- **The fifth round is the second visual review's, and it is the one that reordered the page.**
  The reviewer built the site and looked at it at three widths and found the homepage "not an
  agent chat": a reader met the four-step diagram before the conversation item 3 asks the
  homepage to show. So **the chat is the first block under the promise and the flow is the
  second** — the reverse of the order the second round gave the page, and the second round's
  own decision is why that is a change and not a bug: item 4's acceptance is that the diagram
  is the most prominent thing **in its section**, and a section directly under the chat, drawn
  at the page's full column, is that. Item 4 orders the diagram against the promise; it says
  nothing about the chat. The turns are **alternating bubbles**: one body per turn, tinted
  `--ground` against the card with a `--border` outline and a radius, the reader's at the right
  edge and the agent's at the left, with the agent's name in `--accent-on-dark` — the accent's
  left rail the third round put on the agent's row is gone, because a rail and a bubble on
  opposite sides drew two marks for one thing. `chatConversationFailures` refused a turn whose
  body has no side of its own (a grid item's `normal` is `stretch`, which draws a band rather
  than a message) and refused both speakers on one side; the third round's refusal of a card on
  `.chat-turn` itself stood, and a bubble is not that: those were three cards across the page,
  each stretched to the row's tallest, and this is one content-sized box in a row of the one
  card. (The chat, its bubbles, its caption and `chatConversationFailures` all left the
  homepage with the owner's draft of 2026-10-19 — the reading at the foot of this file — so the
  flow's cards and the footer's label are the parts of this round still on the site.)
  **The flow's cards lost three of their four cyan edges** — the sides and the bottom are
  `--border` at one pixel and the top rail is the accent at 4 px — which is the review's
  "calmer borders": what makes the picture prominent is the section, the card's size, the
  surface and the numbers, not an outline on every edge. **The footer's link label is the
  repository's own name** (`owner/name`, derived from `site.yaml`'s URL) rather than that URL,
  which ran the width of the footer's first column. **The chat's caption is one sentence**, two
  lines at 1280 and 768 and three at 375 where it was three and five: it still says the figure
  is an illustration, still says it is not a recording, still says where the agent's lines come
  from, and still claims no more than that (`home.mjs` and `wording.mjs` hold it to all four).
  The code review's four findings went with it: the two-by-two's header comment now describes
  the `4 3` bottom row it draws, `budget.mjs`'s lead reading is written from the built page,
  and `docs/gcp-live-checklist.md`'s copy-control failure example names the mechanism `site.js`
  has (`sourceOf` over the `[data-copy]` block's own `pre`/`code`) rather than the
  `[data-copy-source]` mark it no longer has. The comments that described the old order went
  with the order: `index.astro`, `Hero.astro`, `fold.mjs` — whose rule 1 is read off the first
  `h2`, now the chat's own title, and whose fixture is that shape — and `budget.mjs`'s lead
  reading. (Those three components called this the fourth round while the rest of the tree and
  this record call it the fifth; they say the fifth now, and the round-4 citations that stay
  are the wrap-arrow readings, which are that round's.)
  **The review of this round's own changes added three findings, all of them the record
  disagreeing with the artifact.** The reading of item 4's row above — "the row starts at
  64 rem, and a step name wraps" — was the round-2 finding's first answer, superseded by the
  third fix round without the record saying so: the row starts at **72 rem** and the longest
  name is on one line there, and that reading now says so and names the round that changed it.
  The same reading claimed the launch was "the invocation the `parallelism` skill documents,
  with its own `<name>` and `<id>` placeholders"; what the page prints is the illustration's
  own label, `Fugaro: launching 3 jobs`, and the launch is one of the two things on the figure
  that are **not** quotations (beside the three `Pull request n of 3` labels) — which is what
  `home.mjs`'s own header says now too, since it made the same claim and its cases refuse a
  status line that is a sentence of the document its turn cites. And the second review's "big
  gap before Install" was still there: the figures' wrappers end with `--space-3xl` of their
  own and the install section opened with `--space-lg` on top of it, so the band between the
  flow's last card and the "Install" heading was **144 px** of blank page — twice the rhythm
  every other pair of blocks on the page keeps. Each block here carries the room below itself
  (which is what `index.astro`'s own comment says), so the install section carries no top
  padding now and the gap is **112 px**, the same as the chat-to-flow gap and the page's tail.
  `website/test/base-styles.mjs`'s `homeRhythmFailures` reads the homepage's top-level blocks
  and their paddings out of the built page and refuses any adjacent pair that both pad the gap
  between them, with two cases planting the doubled padding — one on the install section and
  one on the page's own rule for the two figures, which pads two pairs at once and is refused
  twice — and a case asserting the page as it ships is not refused at all.
  The measurements this round was decided on are
  from a headless Chromium over the built site at 1280, 768 and 375: the four cards of the row
  are one height (218 px) with every name on one line, every top-level block starts where the
  one above it ends and the room between any two of them is the block above's own padding —
  112 px of it, and never two paddings added together: before the fix above, the flow-to-install
  gap was that 112 px plus the install section's own 32, which is the 144 px band the review
  measured. **The follow-up that closed this round's three findings re-measured the built site
  the same way and read the same page**: at each of the three widths the room under the hero is
  32 px and the room under each figure is 112 px — at 1280 those 112 px are the distance from
  the flow's last card (bottom 1324 px) to the "Install" heading (top 1436 px), and 768 and 375
  read the same 112 px between the same two edges — and the flow is a row of four at 1280 with
  the four cards one height (217.56 px, every name on one line), a two-by-two at 768 (190.38 px
  cards) and a stack at 375 (178.56 px). Reading three widths is also what the gate does now
  rather than one, and the gap is why: a padding that doubled a gap *below* some width would be
  invisible to a check taken at the desktop width, so `homeRhythmFailures` reads the page at
  1280, 768 and 375, and a case plants a phone-only double padding and proves the check refuses
  it at 375 and nowhere else. The watch slot is `display: none` while it is empty (the image has since
  landed in it, with the same rule kept under it), and nothing
  overflows horizontally, the
  reader's bubble is 288 px at the right of the card
  and the agent's 463–557 px at the left. Below 35 rem the bubbles fill the column — the
  reader's own line is nearly a phone's width — so the alternation is a thing a reader sees from
  a tablet up. The whole site moved with all of it — the chat's markup, the flow's edges, the
  footer's label, the install section's padding — and the site figure is re-derived over this
  build in the commit that moved it (D192, above); the page weight did not move, because the
  heaviest page renders neither the flow nor the install section. Whether the page then *looks*
  right is the owner's check (70a–70c, D190), and
  no screenshot is committed here: this run cannot see one either.

## The `fugaro watch` screenshot (owner spec of 2026-10-18, item 5)

`website/public/images/fugaro-watch.png` (2918 x 882 px, a 2x retina capture, so 1459 x 441 logical) is a
real screenshot of `fugaro watch` running on the owner's dev installation, taken by the owner on
2026-10-18. It is not a fixture and not generated. It shows the owner's real run titles, spend, run IDs
and pull request links of `dimipaun/fugaro`; the owner decided to use it as is. D190 ("screenshots are
regenerated and never committed") is waived by the owner for this one image, as it was for the first
version. `website/public/images/fugaro-watch.txt` is a hand transcription of what the image shows, one
line per terminal line, for alt text and as a text fallback (column spacing is approximate).

The only processing is a lossless-to-near-lossless size reduction: the PNG was quantised to 48 colours
without dithering and stripped of metadata (ImageMagick), which is what took it down to the size
the page ships. The text stays sharp and the green and yellow words keep their colours. No row or
text was cropped or edited.

Size: 130,533 bytes, inside the enforced byte budgets: the three ceilings and the rule each is
derived by are written in `website/README.md`, and `website/test/budget.mjs` is the case that
reads them from there and holds them to the build it makes itself — no figure of them is copied
here, for the reason the byte-budget bullet above gives. Placing the two assets and the `<img>`
moved the site figure and nothing else (above).

**It is on the homepage now, in the slider's "See it" panel** (`website/src/components/HomeSlider.astro`;
the owner's draft of 2026-10-19, the reading at the foot of this file): the panel's `div.watch-shot` is
the slot `[data-slot="watch-screenshot"]`, and the panel reads heading, picture, command — the block that
was empty until the asset existed is the picture the owner took. It was `AgentChat.astro`'s slot until
that draft folded the chat into the slider's first panel and deleted the component, and the picture moved
with the slot. The markup declares the capture's logical size (1459 x 441, the 2x file's own ratio) so the
box is reserved before the file arrives, `loading="lazy"` because the picture is behind the third tab and
nothing on the first screen waits on it, and the `alt` is the gist of `fugaro-watch.txt` — the
transcription ships as the text a reader gets when the picture does not. `website/test/home.mjs` holds all
four (in the slot, a non-empty `alt`, the declared dimensions, and no external URL) and holds the slot to
its place in the panel, and `website/test/base-styles.mjs`'s `watchSlotFailures` holds the sizing that
makes it scale — a width that is a percentage of the panel it sits in and a height that follows the file's
own ratio, refused if either becomes a pixel count (the two planted cases). The rendered page was measured
in a headless Chromium over the built site at 1280, 768 and 375 while the picture sat in the chat's block:
`scrollWidth` was the viewport's width at all three (no horizontal scroll, the picture being the page's own
and nothing else overflowing), and the picture laid out at 750 x 228, 722 x 220 and 329 x 101 px — the
column's width and the capture's ratio at each. That reading is the chat's block and not this one: the
panel is `display: none` until its tab is chosen, so the picture's width inside it is a fact of a page with
a tab chosen, and what holds it is the pair of declarations `watchSlotFailures` reads. Whether the rendered
page matches the owner's design is the owner's check (70a-70c, D190), as no gate here can see an image: at
the phone's column the picture is about a quarter of the width of the screen it captures, so its small
print is not readable there and the transcription beside it is what a reader reads instead.

## The 2026-10-19 brief: the phone's menu and the console in the call to action

The owner's brief of **2026-10-19** asks for two changes to the built site — its item 1 and
its item 6 — and they are implemented in this branch's pull request, one commit each. Every
reading the brief does not spell out is recorded here, the way items 2, 3 and 4's readings
are recorded above, so that a later reader of the site does not have to guess them from the
markup. **The list is not counted**: each round adds bullets to it, and a number written here
goes stale the way the byte budgets did before D192.

- **The three group labels ARE the menu on a phone (item 1), and D182's drawer is retired.**
  Below the width where the nav collapsed, the whole menu sat behind one `Menu` toggle: the
  three groups were two taps away, and the first tap told a reader nothing about what was in
  them. The toggle, the panel it opened and the sibling combinator that showed it are gone.
  The three group labels stand in the header's row at **every** width, each one a
  `<details>`/`<summary>` that opens its own list of links — a panel across the header's own
  width on a phone, and the same list as a dropdown under its label from 48 rem up. The
  `name` attribute is shared by the three, so the browser keeps the set exclusive: opening a
  group closes its siblings, which is the one-menu-at-a-time behaviour the toggle used to
  provide by hand. **D182's group decision is untouched** — three groups of three, Home as
  the wordmark, Privacy in the footer — and what changed is only what a phone *does* with
  them. `site.js` loses the branch it kept for the panel (`holds()` no longer looks for a
  sibling), and with it the only reason the script had to know where the panel was.
- **The wordmark's text leaves the header row below 390 px, and the mark stays (item 1).**
  The owner's ruling is "hide the wordmark TEXT below about 380px (keep the logo mark)", and
  the number is **390 px** because the row is measured rather than estimated: at 381 px the
  wordmark's text plus the three labels is a few pixels wider than the header's content box
  in the font the built site actually falls back to, and the row that does not fit is a row
  that scrolls sideways. The text is hidden the way a visually-hidden label is — absolutely
  positioned, one pixel, clipped — and **not** with `display: none` or `visibility: hidden`:
  the wordmark is the link home, and a link whose accessible name is thrown away is a link a
  screen reader announces as nothing. `website/test/base-styles.mjs` refuses a rule that
  removes the text any other way, and refuses one that takes the mark out with it.
- **A label and every link in a panel are at least 44 px tall, at every width.** The brief
  asks the menu to be measured against a 44 px tap target, and the labels and the wide
  dropdown's links were both shorter than that, so the rule is on the two of them and on
  every width rather than on the phone alone. **The header grew with it, 65 px → 77 px**, and
  a wide dropdown 114 px → 150 px: that is a change the brief's acceptance asks for and not a
  side effect nobody noticed, and it is why the row's fit is measured again at every width
  rather than assumed from the widths the header used to need.
- **The console is the call to action's third link, and the flag is what renders it (item
  6).** The homepage's hero action row carries a quiet link to `/console/`, labelled `Desktop
  console` — the name Getting Started's console option already gives it — and it renders
  **only** while `site.yaml`'s `features.console_downloads` is true. Nothing about the
  console renders while it is false: no link, no sentence, no placeholder. That is item 6's
  "findable" read the way the rest of the console's offering is read (structural rather than
  editorial): the way *to* the console is what the hero names, and the URL a reader downloads
  from stays derived from the release in one reader
  (`src/lib/console-downloads.mjs`, Global Constraint 9) — a download URL typed into the hero
  would be the second spelling of a URL that boundary test exists to refuse. The link points
  at `/console/` rather than at a release asset because the console page is where the
  downloads are *and* where the console is explained, and a reader who does not know what the
  console is needs the second thing first.
- **What the two changes cost, and what they do not.** The menu's removal takes bytes off the
  base stylesheet every page inlines and off `site.js`, so both the page figure and the site
  figure moved with it, and each is re-derived in the commit that ships them (D192, above).
  **The console's link moves no byte figure and no word ceiling**: while
  the flag is false it renders nothing, so the build the figures are derived from is the same
  build; and the flag-on state it produces is held by the budget gate run over that build
  (`console-option.mjs`'s `FUGARO_GATES_DIST` run), which is where the homepage's two extra
  words meet D188's ceilings. No new origin, no CSP change, no inline script and no second
  script file (D183): the menu's script work shrank.
- **The review round's re-derivation: 290 bytes back onto the script.** Three comments in
  `site.js` still described the drawer as though it existed — the retired toggle, the second
  level a group's list sat at, and the sibling combinator that showed the panel — and the
  review of this branch is right that comments are the specification here, so they were
  rewritten to describe the three `<details>` groups. Comments ship: the file the browser
  fetches is **290 bytes larger**, and all three figures moved with it, over the same gates'
  build — the script, the docs hub and the site, each by the two rounds' movements together (the
  figures the merge brought in were `main`'s, and these are this branch's build's; the numbers
  are `website/README.md`'s and `website/test/budget.mjs`'s, not this document's). Nothing
  else in the artifact moved: the console's link is behind a flag this build has off, and no
  stylesheet changed.
- **The gates, and their proofs.** `website/test/base-styles.mjs`'s two nav gates are
  rewritten for the menu that is (`navLayoutFailures`: the three labels in one row at both
  widths, the header one row with the list out of its flow, a phone's row fitting, and an
  open that moves nothing; `navFailures`: the markup's own shape, with the toggle, the panel
  and the drawer refused by name), and each rule keeps a case that plants the defect and reads
  the refusal back. `website/test/nav-hover.mjs`'s phone case is rewritten to the behaviour
  that replaced the toggle — the labels are drawn with nothing in front of them, a pointer
  that does not hover opens nothing, and a tap opens the group — and `website/test/copy.mjs`'s
  stub DOM loses the panel and the drawer branch it carried for the old markup.
  `website/test/home.mjs` holds the flag-off half of item 6 — the hero's row is exactly the
  two links it carried before, and it mentions the console nowhere — and
  `website/test/console-option.mjs` holds the flag-on half over its flipped copy: the row
  gains `Desktop console` → `/console/` and the two links a reader met before the release are
  the two they meet after it. **Every one was mutation-proved by hand**: the flag gate removed
  from the hero, the console link deleted, a `display: none` on the wordmark's text, a label
  with no `min-height`, a panel left in front of the labels, and a group's list hidden by a
  rule, each planted, seen to fail the case that names it, and removed.
- **The measurements, and the check that is still the owner's.** The built site was rendered
  in a headless Chromium at **1280, 768, 375 and 320 px**: the header is one row at each, 77
  px tall, the three labels and every link in a panel are 44 px tall, nothing scrolls
  sideways, every group opens by tap and by keyboard, and at 375 px the three labels are 65,
  83.5 and 59.3 px wide beside the 47 px mark. That is a measurement of the built artifact,
  not a look at it: **whether the page then looks right is the owner's check (70a–70c, D190)**,
  and no screenshot is committed here — this run cannot see one either.

## The owner's 2026-10-19 feedback on How It Works: the run is one vertical timeline

**Superseded**: the owner's next reading of that page approved the draft *"How it works, draft
2 (diagram)"* with *"build it"*, and the timeline below came out of the page — `RunDetail.astro`
and the gate that read its shape went with it. The section is kept as the round that decided
it; the shape that ships is the one in "The owner's 2026-10-19 approval of the run's diagram"
at the end of this document.

- **"It is a bit better, but not quite there. The run, step by step section does not look
  good."** The section had the right content and the wrong shape. `RunDetail.astro`'s five
  steps were a `repeat(auto-fit, minmax(18rem, 1fr))` grid: at 1280 px the five read three
  blocks beside two with a hole where a sixth would go, at 768 px two-by-two, and no mark
  joined one step to the next — five short paragraphs, which is a list of facts rather than a
  run. Item 4 asked for the detail of the picture above it; a grid of boxes under a picture of
  a sequence shows the reader the sequence nowhere.
- **The run is now one column at every width, with the steps joined.** Each step is a list
  item of two grid columns: a numbered marker in a fixed rail on the left, and the step's name
  and its one quoted line in the column beside it. The line between two steps is the step's
  own `::before` — `top` at the marker's height, `bottom` at the step's edge — so the segment a
  reader sees between two markers is exactly the step's own bottom padding, and the last step
  draws none, because `:not(:last-child)` is what turns the connector on. There is no
  breakpoint: the timeline is the same shape at 360 px as at 1280, which is what a sequence
  reads as. The marker is the accent's outline and the connector is `--border`, so the section
  stays quieter than the picture it details.
- **Every step is still README.md's own words, in the section's own order** (Global Constraint
  6, D102): the build-time quotation check is untouched, and the only line reworded is step 3's,
  which now runs to the end of its sentence so that the review and fix rounds the run repeats
  are on the page — still a contiguous substring of the section, still searched forward from
  the step before it. `test/home.mjs`'s built-page check reads the same five headings and lines
  out of the page, and it no longer strips a leading number back off a heading: the number is
  the heading's sibling now, and the fixture case for a heading that swallowed it proves the
  refusal fires.
- **The shape is a gate, and each claim has a fixture.** `test/base-styles.mjs` gains
  `runTimelineFailures`, which reads the built stylesheet for three things: one column in force
  at **360, 768 and 1280 px** — every `repeat(…)` is refused, counted or not, because a definite
  `repeat(5, 1fr)` hides its tracks in parentheses a top-level split cannot see into and so read
  as one column; a connector on `.run-step`'s `::before` that exists and lands on the rail —
  `content`, `position: absolute` on the line, `position: relative` on the step it is measured
  from, a `left` that resolves to half the marker's own width, a `background` that is declared
  and is a token, a width no wider than `--border-width`, a condition on the step not being the
  last, and both a `top` and a `bottom`; and no `width`, `min-width` or `white-space: nowrap` in
  the section wider than a phone's content box — **328 px**, derived as 360 minus `main`'s own
  inline padding at that width, read from `base.css` rather than assumed. Sixteen fixtures plant
  each defect and read the refusal back — including the unreadable-width case, which is refused
  by two readers of one declaration and is therefore asserted line by line rather than as a set —
  and the check was proved red by hand against the old stylesheet, which it refuses four times
  over.
- **The heading sits in the run's own rhythm.** `.run-detail-title`'s margin under it moved with
  the section, `--space-md` (24 px) → `--space-lg` (32 px) — the gap that separates two steps —
  so the title, the five markers and the note read down one column at one spacing, where the
  wider gap used to open a row of boxes. It is written down in the component rather than gated:
  a gap between 24 and 32 px is a preference, and every claim `runTimelineFailures` reads is a
  defect a reader would see.
- **The widths were read, not looked at.** The built HTML and CSS were examined at 360, 768
  and 1280 px for the causes of horizontal overflow — a fixed width, a `nowrap`, an absolutely
  positioned element outside the rail — and there are none: the grid's track is
  `minmax(0, 1fr)`, the rail is 32 px, and the name and the line both carry
  `overflow-wrap: anywhere`. That is arithmetic over the artifact. **Whether it now looks good
  is the owner's check (70a–70c, D190)**, and no screenshot is committed — this run cannot see
  one either.
- **The byte budget is re-derived in the commit that moves it (D192).** The site figure alone
  moved: the rail, the connector, the rule that
  stops it at the last step and the markup that places them, on the one page that renders a
  run. The page weight and the script did not
  move: the heaviest page is still the docs hub, which renders no step. The three numbers are
  `website/README.md`'s, re-derived there in this round's commit, and `website/test/budget.mjs`
  holds each to the gates' own build — this document states the rule and not the bytes.

## The 2026-10-19 home-page feedback: the prompt line, the benefits, the caption

The owner's **feedback of 2026-10-19** is a second message the same day, and it is not the
brief above: "Home page: sucks. We need a terser way of saying that you use it transparently
through your agent. Maybe just the `> Fan out these three bug fixes.` Also, we need a section
with Benefits or somesuch as we have in the README, no?" Plus one correction the message
names in passing — the diagram's fourth caption claimed more than the repository says. Every
reading it does not spell out is recorded here, as the brief's readings are above. The
feedback's own item numbers are not used anywhere in this tree: the brief of the same day has
an item 1 (the phone's menu) and an item 6 (the console's link), and citing the home page's
work by a number from the other message is how two documents get confused for one. The three
changes are cited as the feedback's own.

- **The prompt line is the request, and it is one string.** The hero renders
  `> Fan out these three bug fixes.` directly under the headline, drawn as the chat's own
  bubble, and the chat's first turn speaks the same sentence. Both read
  `src/lib/agent-request.mjs`, which holds the line to the changes spec of 2026-10-18, item 3
  (the spec's own example of a request) at build time. Two literals would be two lines to keep
  in step, and the day they drifted the page would show the reader asking for one thing and
  being answered about another; `website/test/home.mjs` holds the two rendered lines to each
  other and to the document from outside the build. **Nothing under the line explains it**: the
  brief asks for the request "as the centrepiece", and the explanation of what it means is the
  figure below, which shows the sentence being answered.
- **The benefits are the README's four reasons, and the page may not drop one.**
  `src/components/Benefits.astro` renders `README.md`'s `## Why Fugaro` — the four names it
  gives its reasons, with the numbers off, and for each one a sentence out of the paragraph
  that reason is argued in. Each line is held to **its own reason's paragraph** rather than to
  the section, so a line that moved from the reason above it to the one below stops the build
  instead of selling the reader the wrong sentence under the wrong name; and the build stops
  if the page renders fewer reasons than the README argues, so a claim the README makes cannot
  quietly leave the page. `website/test/home.mjs` makes both checks against the built page.
  The four are the claims this repository's own documents carry: the container is in the
  reader's project (`README.md#How it works`, `docs/design/v1.md`), one job execution per task
  (the same), the project layer holds the policy (`docs/project-layer.md`), and the base image
  and the per-role model are the tools' own (`docs/base-image.md`, `docs/multi-model.md`).
  No fifth reason was added and none was cut.
- **D188's word ceiling moves from 250 to 300, and that is the reading rather than a
  loosening.** The benefits are a fourth block the owner's feedback requires on the one page
  that already carried the three the briefs before it require — the promise, the chat (changes
  spec item 3) and the flow (item 4) — and the four together measured **296 words** when the
  section landed, so the homepage was over the number D188 set. `website/README.md` states the
  rule for exactly this:
  "a task that needs more raises the number here, in the commit that needs it". The number is
  therefore the smallest round ceiling above the page that carries every block its briefs
  require, and it stays a ceiling: the highest page that gained nothing from this feedback is
  Getting Started at 250 words, and a page that wants the extra fifty has to carry a block its
  own brief requires for them. The lead ceiling is untouched: the homepage's lead is 23 words
  (the request, the promise and the two links) against 40, and it grew by the request line the
  feedback puts there. The caption below takes two words back off that page — it measures **294**
  words with everything the feedback asks for in place, which is the number `website/README.md`
  derives the ceiling from.
- **D188's ceiling moves again, from 300 to 500, for the bold redesign's homepage.** The owner's
  board `design-ref/HomeV3.dc.html` (approved 2026-10-20) draws the homepage as five blocks: the
  hero, the slider, the run as a band of four steps, the grid of what a run leaves behind, and
  the closing call to action with the install command. The page that carried three blocks carries
  five, and the five measure **468 words**, so the page was over 300. The rule is the same one as
  above and it is the same kind of reading: the number is the smallest round ceiling above the
  page that carries every block its briefs and its boards require — still a ceiling, and the
  highest page that gained nothing from the board is How It Works at 279 words. The lead ceiling
  is untouched and the homepage's lead **fell** from 23 words to **16**: the board's hero prints
  the request, the promise and the two links, and the illustration the board adds below them is a
  `figure.hero-shot` the lead reading excludes whole, the way it already excludes the block the
  first `h2` belongs to. Both changes are read off the built page by `website/test/budget.mjs`,
  which is where the numbers above come from.
- **The byte figures are not re-derived, and that is the owner's ruling and not an omission.**
  The owner ruled on 2026-10-20 that the three figures `website/README.md` states are re-derived
  **only when the owner asks** — `website/test/budget.mjs` runs D192's exact derivation only with
  `FUGARO_DERIVE_BUDGETS=1`, so a round that moves a few hundred bytes no longer reds the
  preview — while the ceilings the pages are measured against hold on every run. They hold here:
  the gates' own build (the copy `test/lib/build.mjs` builds, which is the build the README's
  figures name) is **1,035,806 bytes** against the stated **3,705,975**, and it is the docs hub,
  not the homepage, that is still the heaviest page. The three figures are left as they stand
  until the owner asks for the re-derivation; the drift is recorded in the pull request body
  rather than silently absorbed.
- **The site's green is its own value, and not the console's `--ok`.** The bold look's success
  tone was first written as the board's own green, `#4CC38A` — which is also the console's
  `--ok`, and `docs/design/console-styling.md` §3.2 records that colour as one of six the
  console **invents** because "the website has no equivalent". `internal/cli`'s
  `TestDocsConsoleStylingTokenProvenance` holds that sentence true by refusing those six literals
  anywhere in `website/src/styles/palette-logo.css`, the palette in force, so the first value was
  a red Go test rather than a judgement call. The palette's green is **`#43C08A`**: the same hue
  family, the same contrast on the ground, the panel and the surface (read by
  `website/test/a11y.mjs` like every other designed pair), and not the console's literal. The
  other three palettes keep the console's value; the reading is about the palette the site
  actually imports, which is also the only one the Go test reads.
- **What this round could not verify, again.** The visual match to the owner's board is the
  owner's check at the preview: this run cannot open an image, no browser was driven, and no
  screenshot is committed. What the gates read is the artifact, the stylesheet and the numbers
  above — the homepage's five blocks and their rhythm (`base-styles.mjs`), the lead and the words
  (`budget.mjs`), the controls' 44 px minimum (`home.mjs`), the contrast of every designed pair
  (`a11y.mjs`), and the slider's panels (`slider.mjs`).
- **The diagram's fourth caption said more than the repository says.** It read "each commit is
  pushed within about a minute while the agent works", which is not what a reader of the picture
  takes it to mean: what opens the pull request is the run's **first checkpoint push** — within
  about a minute of the run's first commit — and what a reader gets then is a **draft, marked
  not verified**, while the run goes on pushing checkpoints as it works
  (`docs/git-providers.md#Early draft PRs (git.pr.early_draft)` and
  `#Checkpoint pushes (git.pr.checkpoints)`; `docs/gcp-setup.md` says the same). The caption is
  now **"the first checkpoint opens the draft PR, marked not verified"**, cited to
  `docs/git-providers.md#Checkpoint pushes (git.pr.checkpoints)`, and the step cites a section
  of a document other than README.md for the first time — which is why the flow's captions are
  held per-section rather than to README.md as one file, and why `home.mjs`'s ordering case is
  now built out of two of README.md's own `## How it works` lines: each step of the shipped flow
  cites a section of its own, and the rule that a section's lines come in the section's order
  has to keep holding for the day two steps share one again. **What the caption cannot carry
  is the rest of the fact** — that the push comes within about a minute of the run's first
  commit, which is the section above it in the same document and `docs/gcp-setup.md`'s
  sentence, and that the run goes on pushing checkpoints as it works, which is another sentence
  of the section the caption cites. A caption is one contiguous quotation of a repository
  document, and one stitched from two sentences' halves is a sentence the repository never
  wrote; the flow's step is the pull request, so the caption says what opens and what state it
  opens in, and the rest is the section it cites. It is recorded here rather than left to the
  reader of the picture.
- **The measurements, and the check that is still the owner's.** Nothing here was rendered: no
  browser was driven for this round, and no screenshot is committed — this run cannot see one.
  What the gates read is the artifact and the stylesheet: the homepage's words and its lead's
  against the ceilings (`budget.mjs`), the section as a named landmark with its heading and its
  `h3`s read out of the built page (`a11y.mjs`), and the homepage's bytes re-derived from the
  gates' own build (D192). **Its vertical padding is read out of the built CSS at the three
  widths the rhythm gate uses, so the block pads the gap above it once and not twice
  (`base-styles.mjs`)** — and that is true of the shipped artifact as of the fix below, which is
  also what the review of round 6 found wrong with the reading this paragraph carried: it gave
  the section's heading an ordinal in the page's outline and cited a padding the artifact did
  not have, and neither of those is a thing to write down here, because both are things the
  gates read off the built page. The fix is where the rule lives: the rule that pads the section
  is the one `Benefits.astro` declares, the file that renders it, where the page's own
  `.home-benefits` rule named the class and carried the page's scope, so it never matched the
  element at all (Astro scopes a page's rule to the elements the page renders). `base-styles.mjs`
  reads a selector against the `data-astro-cid-…` the element carries and refuses a block the
  stylesheet names and no rule reaches, so a dead rule cannot be read as a block's padding
  again. **Whether the page then looks right is the owner's check (70a–70c, D190)** — the
  two-column grid, the rule above each benefit and the prompt line's bubble are the parts of
  this round a gate can only say is present.

## The 2026-10-19 benefits feedback: an icon each, and a step up the type scale

A third comment the same day, on the design canvas rather than on a page: **"make them bigger,
bolder, use an icon for each point"**, under the four benefits. Every reading it does not spell
out is recorded here, as the two sections above record theirs. It changes the section's
presentation and **not one word of it**: the four names and the four lines are still
`README.md`'s `## Why Fugaro` reasons, held to their own reason's paragraph by the component's
build-time guard and by `website/test/home.mjs` from outside the build.

- **One glyph per point, inline, and decorative.** Each benefit carries one `<svg>` written in
  `src/components/Benefits.astro`: a shield for *Safe by construction*, arrows leaving a centre
  for *Scale out, and walk away*, three people for *Team control in one place*, a spark for *The
  newest tools and models, ready to use*. There is no icon font, no image file, no third-party
  anything and no JavaScript: the four are shapes in the markup, so the page fetches nothing for
  its own drawing and stays readable with the script off (Global Constraints 4 and 8). The glyph
  is `aria-hidden="true"` with `focusable="false"` — the name beside it is the point, and a
  screen reader handed the picture as well would read the point twice. It is drawn with
  `stroke="currentColor"` under a rule that sets `color: var(--accent)`, and `--accent` on
  `--ground` is a pair `a11y.mjs` already computes at 3:1 for all four palettes; its box is
  `--space-xl` (48 px), a step of the spacing scale rather than a number of the component's own.
- **Both the name and its line are larger, and the section's heading moved with them.** The
  type scale has one step between the body size and the size above it, and the feedback asks for
  the name *and* the line to be larger, so both are `--font-size-lg`; they are told apart by the
  weight and the colour the block already used, which is also why "bolder" is the size step
  rather than a weight — the scale stops at `--weight-semibold`, the weight the name already
  carried. The section's own `h2` is one step above them (`--font-size-xl`), so the block reads
  as a section with four points under it instead of as four headings and a label.
- **The gate is the icons' own, and it reads the built page.** `website/test/home.mjs` reads
  every `<svg>` under each benefit and refuses a point with none or with several, an icon that
  draws nothing (`<svg>` with no shape in it, which is a picture nobody sees and the one defect
  of this set no other gate can catch), an icon that is not marked as decoration or out of the
  tab order (`aria-hidden="true"`, `focusable="false"` — the pair `a11y.mjs` already holds the
  flow's arrows to), an icon that references anything (`<use href>`, an `<image>`, an `href` on
  the `<svg>` itself, an inline `style`) and four points drawn with one picture between them.
  Each refusal was proved by planting the defect in the component, rebuilding and watching the
  built-page case fail — not only by its fixture. The glyphs live in the component as drawings
  rather than as a template branch per name, so a name whose entry holds no shape stops the
  build instead of shipping an empty box.
- **The measurements, and the check that is still the owner's.** The figure re-derived in the
  commit is the site's, from the gates' own build (`website/README.md`); the page weight, the
  script and the word ceilings did not move, because the section is the homepage's and not one
  word of it changed. Nothing was rendered: no browser was driven and no screenshot is committed, and
  this run cannot see one. **Whether the four points now look right is the owner's check
  (70a–70c, D190)** — a gate can say the icons are present, decorated, unreferenced and four
  distinct drawings, and it cannot say the section looks good.

## The 2026-10-19 changelog brief: the released state, and the summaries

The same day's owner feedback about `/changelog/`: *"Changelog: why do I see not yet released?
Also, the summaries under each release should be more high level, product features that end
users understand."* Two changes follow from it, and every reading the feedback does not spell
out is recorded here.

- **A release is published when the plugin's version has reached it.** The page decided that
  from `git tag --list`, and the deploy build checks out without tags — so the deployed page
  printed the "not yet released" marker under **every** release. The rule is now a file in this
  repository: a release file is published when its version is at or below the version in
  `plugin/.claude-plugin/plugin.json`. The version bump lands on `main` before the tag does, so
  on `main` the manifest names the newest released version and everything up to it reads as
  released, while a release file written ahead of its bump keeps the marker — which is what the
  marker is for. The generator no longer reads the checkout's git state at all, so a shallow
  clone, a full clone and the deploy build render the same page; that is also what took the
  git-state builds out of the byte figures (above).
- **Each release opens with a product summary, and the release file's Highlights are one click
  away.** The summaries used to be the release files' own first Highlights bullet — the
  operator-facing line, written for whoever runs Fugaro. The summary is now written for a
  reader who uses Fugaro: one to three sentences naming what the release gives them, covering
  every file under `docs/releases/`, from what that file and its release say and nothing else.
  **The release files are not edited** — they stay the operator's document — and their
  Highlights are rendered as they are inside a `<details>` block labelled `Highlights`, which
  opens with no script (D183).
- **The summaries are a file the site renders, not copy on the page.** D189 put `/changelog/`'s
  summary in the release files themselves, so that the page went on rendering facts and nothing
  else; the brief asks for words that are not the release files' — a product summary of a
  release whose file is written for its operator — and **Global Constraint 6** ("a page never
  restates a document; it renders it or links it") is what that has to be read against. So the
  summary is `website/data/release-summaries.yaml`: one line per release, keyed by its tag,
  read and validated through the content pipeline (`src/content.ts`'s `loadReleaseSummaries`,
  W1.3) and rendered by `src/components/Changelog.astro`. The page still writes no prose of its
  own — what it renders is two files' text — and a release file with no entry in the data file
  **stops the build, naming the file**, rather than rendering an entry with nothing under its
  heading. `/changelog/` is exempt from D188's whole-page word ceiling (it renders a document of
  this repository), so the summaries add no ceiling to move; the byte figures moved with them,
  and are re-derived in the commit that ships them (D192).
- **The "newest releases" block goes, and that is a reading of the brief rather than a loss.**
  D189's `/changelog/` summary was "the first Highlights line of the newest few releases" above
  the list, which is a gist of the newest releases and not of each one. The brief asks for a
  summary under **each** release, which is what the block was approximating, so keeping both
  would put the same words on the page twice. The list is unchanged: every release file, newest
  first, each linked to its GitHub release, no date (D100).
- **What the gates hold.** `website/test/changelog.mjs` reads the published state from the
  plugin's version over fixture directories that are not git repositories at all — a tag cannot
  be the reason for any answer it asserts — and refuses a manifest it cannot read as JSON, a
  version that is not `x.y.z`, and a version naming no release file.
  `website/test/changelog-page.mjs` holds the page to both sources: one summary per release in
  the list's own order, the text the data file carries for that tag, no empty entry and no entry
  outliving its release file, no internal id or repository path in a summary's words, every
  release file's lines still rendered (the notes are collapsed, not dropped), and the marker
  printed for exactly the release files above the version the copy's manifest carries. Both
  builds run in a copy of the repository with no `.git`, which is the environment that used to
  make every release read as unpublished.
- **What is still the owner's check.** Whether the summaries read as product news and whether
  `/changelog/` looks right is the owner's own reading of the page (70a–70c, D190): this run
  cannot see a rendered page, and no screenshot is committed here.

## The owner's 2026-10-19 approval of the run's diagram: the timeline comes out

The owner approved the draft *"How it works, draft 2 (diagram)"* on 2026-10-19 with *"build
it"*, and the round that built it is the one `website/README.md` records as "How It Works is
one diagram of the run, and the run in words under it". Its sections here are the one above
(the timeline) and **changes spec item 4**, which is amended by it rather than rewritten there
(the spec is committed as it was agreed, and no part of it is implemented by being written
down).

- **The timeline above is superseded, and item 4's "SAME diagram" is amended for one page.**
  Item 4 asks for the homepage's four-step flow and for the HOW IT WORKS page to "open with the
  SAME diagram (one shared component, not two copies)". That page opens with the run's own
  diagram now: one drawing of the run — the draft's `[fix ─► review(2)]` loop and all — with
  the same facts in words under it, drawn by `RunDiagram.astro`. The homepage kept
  `ArchitectureDiagram.astro` and its four quoted captions, and the two components are two
  drawings of two things rather than two copies of one; the owner's ruling of 2026-10-20 retires
  that component and the four-step flow with it, so this branch has one drawing of a run and the
  homepage's block where the flow stood is the slider. The reading is the owner's and it is
  the whole of the amendment: **item 4's "one shared component" holds for the picture the two
  pages used to share, and the page that answers "what happens between asking for the work and
  getting the pull request" answers it with the run**, which is a picture the homepage's flow
  is not (item 4's acceptance — a picture, not a paragraph — is untouched).
- **The timeline section above is a record, not the page's shape.** The vertical timeline, its
  rail and its connector are gone from the artifact, `RunDetail.astro` with them, and
  `test/base-styles.mjs`'s `runTimelineFailures` went with the shape it read (that file's own
  note says so, and what is left of the run's layout there is the room its blocks take). The
  section above stays because the rounds are kept as they were decided — read it with this one
  beside it, and the page it describes is not the page that ships.
- **Every line of the drawing is quoted from the repository, and the drawing is markup.** Both
  drawings are inline `<svg>` rendered from one set of labels, each label carrying the document
  it is quoted from (`data-source`), and nothing is fetched: no picture file, no font, no
  origin, no script (D183, D190). The four steps the retired flow drew cited their captions the
  same way, and the run's diagram cites the redesign's own changes spec,
  `docs/design/v1.md` and the plugin's skills.
- **The shape is a gate, and each claim has a fixture.** `website/test/how-it-works.mjs` reads
  the built page and refuses: a figure that is not one diagram, a drawing without an
  accessible name or a `viewBox`, a stage the repository does not name, a label that is not a
  line of the document it cites, a line of the drawing wider than the box it is drawn in, a
  drawing scaled into a box narrower than a phone's column — and, since the review round below,
  the loop's own arrow. Its fixtures plant each defect in the built page and read the refusal
  back, and the page as it is built passes its own gate.
- **The review round: the changeover, the ceiling and the arrow.** Three findings came out of
  the review of that round, and all three were the gate's rather than the page's rendering:
  **the arrow was unheld** — deleting both loop paths, or drawing the arrow between two other
  stages, left the gate green, so the gate now reads the line and the head that lands it,
  which two stages its two ends join (paired with the pills by the drawing's own geometry) and
  where the head sits; **the wide drawing's floor held only at the widths the page happened to
  be read at** — its smallest label is 16 units of a 1200-unit picture and the drawings changed
  over at the site's 48 rem chrome breakpoint, so at a 768 px viewport that label came out at
  9.6 px, and the changeover is 59.25 rem now, the width at which the label is exactly the 12
  px floor in the page's own 900 px column; and **no ceiling held the stacked drawing**, which
  a tablet would have been shown stretched to the column, so `.run-tall` carries a
  `max-width` of its own scale — 22.5 rem, one unit to the pixel — and the gate refuses a
  ceiling that is absent, unreadable or larger than the viewBox it is drawn at. The box every
  readability number is taken at is read out of the page's own stylesheet (`main`'s inline
  padding and the page's ceiling) rather than typed into the gate, so a gutter that moves moves
  the arithmetic with it. **The byte budget is re-derived in the commit that moves it (D192)**:
  the site figure alone moved, by the stylesheet rules of that round, and the numbers are
  `website/README.md`'s (that round also re-derived the three figures from the gates' own
  build, because the base they carried was stale — `website/content/cli-reference.md` had been
  regenerated a round earlier without re-deriving them, which is recorded there).
- **And the review of that review round: the side of the query, and the way the head points.**
  Two of the three readings the round above added held less than they said. **The changeover was
  held by its width alone** — `(width<=59.25rem)` and `(width>=59.25rem)` name the same width
  and show the drawings to opposite readers, so a stylesheet that turned `.run-tall` on above
  59.25 rem and `.run-wide` off there satisfied every reading while stacking the drawing on a
  desktop and showing the shrunk wide one to the phone the floor exists for; the gate reads
  which side each switched rule holds on now, and refuses one read from above. **The head was
  held where it sat, not which way it pointed** — and the head the component drew was mirrored,
  its point at the base's place, so it landed on `implement` pointing back up its own line at
  the stage the run came from; a mirrored head is the same triangle over the same two x's, so
  only the corners can say which way round it was drawn, and the gate reads them (the point has
  to lead the base along the direction the line arrives in) and refuses a head that is not a
  triangle of three corners. The component's own header comment had still said the drawings
  change over "below 48 rem — the width the site's chrome switches at", which the round above
  made false. Neither fix moved a byte: the head is the same numbers written in another order
  and the side of a query is a reading, so the figures in `website/README.md` are the ones the
  build already measured.
- **What is still the owner's check.** The drawing was built to the draft the owner approved,
  and whether the page then looks right is the owner's reading of it (70a–70c, D190): this run
  cannot see a rendered page, no screenshot is committed here, and the gate says what the
  drawing contains and how large its labels come out rather than what it looks like.

## The 2026-10-19 home draft: one slider with three tabs, and one Get started button

The owner approved a **second canvas draft for the homepage** the same day as the brief and the
feedback above — **"Home, draft 2 (slider)"**, 2026-10-19 — and it is the authority the page and
its gates cite by name (`website/src/pages/index.astro`, `HomeSlider.astro`, `HomeInstall.astro`,
`how-it-works.astro`, and `website/test/{home,slider,fold,budget,a11y,wording}.mjs`). The canvas
is not a file of this repository and no agent opened it: what it asked for is recorded here, from
the draft's own text, and every reading it does not spell out is a decision of this branch's,
in the commit that made it.

- **The page is three blocks, and the page renders exactly those three.** `index.astro` is
  `Hero.astro`, then `HomeSlider.astro`, then `HomeInstall.astro`. The hero is the headline
  (README.md's tagline), the prompt line the feedback of the same day asked for
  (`> Fan out these three bug fixes.`, the string `src/lib/agent-request.mjs` holds to the
  changes spec), the one-line promise (README.md's subline) and the two links under them. The
  install block is the page's **last** section, which is where the draft puts it: the commands
  are no longer under the promise.
- **One slider with three tabs, and all three panels in the HTML.** `HomeSlider.astro` renders
  `.home-slider`, a row of three fragment links — "Easy to use", "Why Fugaro?", "See it" — and the
  three panels they open. *Easy to use* is the draft's own three lines (you tell your agent,
  Fugaro runs it in your cloud, you get a draft pull request), each with one inline SVG and the
  repository's sentence about it under it, held to README.md's sections at build time and read
  again by `website/test/slider.mjs`'s `stepFailures`. *Why Fugaro?* is `Benefits.astro`
  unchanged, rendered into the panel (`inPanel`), because the README's four reasons and their
  icons were already the thing this panel is for; the middle tab and the section's own heading
  are that one string since the owner's ruling of 2026-10-20, which read "Benefits" as the
  block's shape rather than as the question its four points answer (the reading at the foot of
  this file). *See it* is the owner's real `fugaro watch`
  capture above the command, and under the command the panel's caption: `fugaro watch` is the
  live screen of spend, repositories and running agents. — `plugin/skills/working/SKILL.md`'s own
  line, which `home.mjs` reads back to that file. The picture and its transcription are the
  section above's subject.
- **The tabs are fragment links and not radio inputs, and the site's own privacy statement is
  why.** The draft allows either. A radio group would put three `input` elements on the
  homepage, and the site makes two claims that would then be false: the privacy statement's
  "It has no account, form or comment box.", and `website/test/consent.mjs`, which refuses a
  `form`, an `input`, a `textarea` or a `select` on **every** built page. So the tabs are
  `<a href="#home-panel-…">`, one rule shows each panel, and the first panel is shown while no
  fragment is chosen: the page needs no JavaScript to be read or used, every panel's text is in
  the HTML a reader receives, and the tabs are keyboard operable as links with the focus ring
  the rest of the site's links carry. What that costs is recorded in the component: a radio
  group is announced as three choices and its arrow keys move between them, and a row of
  fragment links is announced as three links. `website/test/slider.mjs` holds the shape —
  three tabs, three panels, the panel the fragment names, no script that switches them.
- **The install block is one button, and the command under it is read from the data file.**
  `HomeInstall.astro` renders one large link to `/getting-started/` with the brew command under
  it, from `website/data/install.yaml` through `HOME_PATH` — the same reader the two install
  cards used, so the command is still never typed twice and the two cards are gone.
- **Every control the page asks a reader to tap is at least 44 px tall**, which is the size the
  owner's brief of 2026-10-19 measures every control against. The tabs and the install button
  already were; the hero's two links were a fraction of a pixel short of it on their padding and
  their line height alone, so they declare `min-height: 44px` and `website/test/home.mjs`'s
  `controlTargetFailures` reads the rule — including the case where the minimum sits inside a
  media query that is not in force, which is a minimum a phone does not get.
- **What left the page.** `AgentChat.astro` is deleted rather than hidden: a component no page
  renders is a component whose build-time guards stop running, and every line the site still
  publishes is still held to the document it quotes. `ArchitectureDiagram.astro` is off the
  homepage because the draft's page is those three blocks and the picture of the run is not one
  of them — the slider's first panel is the flow the reader is shown — and it drew
  `/how-it-works/` for a while; the owner's ruling of 2026-10-20 then retires the component and
  the flow with it, so the site's picture of a run is `RunDiagram.astro`'s and it is on
  `/how-it-works/` alone. The checks that were about the
  chat were replaced by checks about what replaced it (`website/test/slider.mjs` is new;
  `home.mjs`, `fold.mjs`, `budget.mjs`, `wording.mjs` and `base-styles.mjs` were re-pointed in
  the same commit), not dropped.
- **What the gates hold, and what they cannot.** The page's order, the three tabs and the three
  panels, the panel each fragment shows, the absence of a script, the 44 px targets — read at
  360 px and at 1280 px, so a minimum that holds at one of the two and not the other is refused —
  and the steps' provenance are `website/test/slider.mjs`'s five rules, with twenty-two planted
  cases under them; the hero's links against `features.console_downloads`, the prompt line's
  source, the watch block and the capture's slot are `home.mjs`'s; the lead's word count is
  `budget.mjs`'s and the byte figures are `website/README.md`'s, re-derived from the gate's own
  build in the commit that moved them (D192). **Whether the page looks like the owner's draft
  is the owner's check** (70a–70c, P9, D190): no gate here can see an image, and no screenshot
  is committed.

## The owner's ruling of 2026-10-20: the benefits section is headed "Why Fugaro?"

The owner's ruling of 2026-10-20 renames the homepage's "Benefits" heading to exactly `Why
Fugaro?` — capital W and F, question mark — and the slider's middle slide with it. Nothing else
about the section moves.

- **What the ruling settles.** The section renders README.md's `## Why Fugaro`: the four names
  that document gives its reasons, and for each one a sentence of the paragraph it argues it
  in. The block was headed "Benefits", which named the *shape* of the list rather than what the
  list is for, and "Benefits" is not a word README.md uses for it. The heading is now the
  question the four points answer, which is the reading the section's own provenance already
  carried: `data-source="README.md#Why Fugaro"` has cited that heading since the section
  landed.
- **What changed, and what did not.** `src/components/Benefits.astro`'s `<h2>` reads "Why
  Fugaro?", and `src/components/HomeSlider.astro`'s `SLIDES` middle entry carries the same
  string as its label. (The entry was `TABS` when the ruling was written; the owner's feedback
  of 2026-10-21, item 3, turned the tabs into the slider, and the list is `SLIDES` now. The
  string it carries is where it was.) The component's and the file's names, the section's `id`
  (`benefits-title`), its `class` (`home-benefits`), the panel's `id`
  (`home-panel-benefits`) and the `aria-labelledby` that ties the heading to the section are
  all where they were: they are the element's name in the markup, which the stylesheet and the
  accessibility tree read, and none of them is a label a reader sees. **The four points are
  not touched** — neither the README's own names nor the sentences drawn from its paragraphs —
  because they are the answer the new heading asks for, and a rename is not a licence to
  reword them.
- **The two gates that pin the string, and the ones that do not.** `website/test/home.mjs`
  reads the built page's `benefits-title` and refuses anything but the exact string, so the
  question mark is held as well as the words: its case plants the owner's own phrase without
  the question mark, and a second plants the label the section carried before the ruling.
  `website/test/slider.mjs` already held each panel's own `h2` equal to the label of the dot
  that opens it — a reader who arrives on a panel reads the label of the slide it is — so the
  dot's name and the heading are one name by construction, and a rename that reached only one
  of the two stops the build. The other gates named in the brief pin no string of this
  section: `a11y.mjs` reads the section as a named landmark and its `h3`s as headings under
  one `h2` and never the words; `copy.mjs` reads no heading of the homepage; and
  `stacking.mjs` reads the blocks' order and their rule, not their text. Nothing there moved.
- **The figures, re-derived rather than argued.** The site figure moved by the **6 bytes** the
  longer string costs in the built page — "Why Fugaro?" is three characters longer than
  "Benefits" and the string is written twice, once in the `<h2>` and once in the dot's own name — so
  `website/README.md`'s byte budget is re-derived in the same commit, 988,253 → **988,259**
  bytes across the same 23 files, with the whole-site ceiling following it, 3,705,951 →
  **3,705,972**. One more byte lands on the same branch after it, from the second item of the
  review of 2026-10-20 — the security page's boundary drawing grows the two cards whose last
  line was drawn below the inset they declare, and the gateway card's height goes from two
  digits to three (`96` → `120`) — so **the branch's own figures are 988,260 bytes across the
  same 23 files and a 3,705,975-byte whole-site ceiling**, and those are the pair
  `website/README.md` states. The page's own word count moves by two (the heading and the tab
  are each a word longer, which is what the shipped tab label costs the lead region too, 29 →
  **30** words), and **neither word ceiling moves**: the ceiling of 949 words is `privacy`'s own
  page plus a quarter, and the homepage stays well inside D188's 300. No file was added, no
  stylesheet rule was touched, and the one script is the same 39,888 bytes.
- **The measurement, and the check that is still the owner's.** Nothing here was rendered: no
  browser was driven for this round, no screenshot is committed, and this run cannot see one.
  What was read is the built artifact — the two strings out of `dist/index.html`, the site
  figure and the page's words and lead from the gates' own measurement of it, and
  `(cd website && npm run check)` green over all fourteen gates. **Whether the renamed
  heading looks right in the section's own type step is the owner's check** (70a–70c, P9,
  D190): the ruling names a string, and no gate here can see a page.

## The owner's 2026-10-21 feedback on the FAQ: the empty comparison section comes out

The owner's feedback on the preview of 2026-10-21 is one line — "FAQ page: remove How Fugaro
compares section, it's empty" — and this round is that one line. The section is removed from
the FAQ page and from the file that fed it; nothing else on the page moved.

- **What the section was, and why it was empty.** `website/src/pages/faq.astro` rendered a
  section headed "How Fugaro compares" from `website/data/faq.yaml`'s `comparison:` list. The
  list carried no row and never had one, and D103's provenance rule (F51–F52) is exactly what
  kept it that way: a row about another tool is a claim this repository cannot verify — it has
  no network and records no sourced, dated fact about any other agent runner — so a row
  without a `source:` and a `date:` stopped the build. The honest empty state was written for
  day one and the owner has now answered it: a heading over a panel that says nothing is a
  promise a reader meets on the page, and the answer is to take the section out rather than to
  keep a place set for rows that may never come.
- **What came out, and where.** `faq.astro`: the section and its panel, its empty state, the
  `data-comparison-rows` count and its heading; the validator that read the list (`rows`,
  `ComparisonRow` and `comparison` in `TOP_LEVEL_KEYS`); and the seven stylesheet rule blocks
  the section and its table were drawn with — `.comparison`, `.comparison-title`,
  `.comparison-panel`, `.comparison-empty`, `.comparison table`, `.comparison caption` and
  `.comparison th, .comparison td`. `website/data/faq.yaml`: the `comparison: []` key and
  the comment block that documented the row format, both replaced by a note that records the
  removal and its date. **Nothing else in the page moved** — the headline, the lede, the six
  answers, their sources and the two `more_*` links are the strings and the files they were.
- **The anchor, the navigation and the sitemap.** There was no table of contents on the page
  and no sitemap entry to remove: `src/layouts/Base.astro`'s menu links `/faq/` and no anchor
  of it, and the page's own outline was its bands. Nothing in the tree linked
  `/faq/#comparison`, and nothing links it now — `website/test/linkcheck.mjs`'s second rule is
  what holds that either way, since a `#fragment` that resolves to no `id` on the target page
  is refused wherever it is written.
- **The two gates that pin the removal.** `website/test/faq-community.mjs` reads the built
  page for every leftover of the section — the band, the heading "How Fugaro compares", and the
  word `comparison` itself, which is what every shape of the section was written in: its anchor
  and class, its panel, its empty state, its row count and all seven of its rule blocks. The
  word rather than a list of them, because a list is what a first cut of this check carried:
  it named the three shapes a reader sees and left five of the seven rule blocks free to come
  back unnoticed. Each shape has a plant a later edit would write, and the stylesheet's plant
  is the five selectors that list missed. The file half is a build case: a `comparison:` key written into
  `faq.yaml` again stops the build naming the key, because the page reads `version` and
  `questions` and refuses every other one. That is what keeps the removed section's data from
  returning as a fact nothing renders (W1.10 step 2), and it is the rule D103's reader used to
  be; the provenance cases themselves went out with the rows they were written for.
- **The figures, and the owner's request that they stay put.** The removal takes the section's
  markup and its rules out of the page: the built FAQ is **1,583 bytes** lighter (23,719 →
  **22,136** bytes), and the whole build is lighter by the same 1,583 bytes, the file count
  unmoved at 23. The page's word count drops from 241 to **211** — the heading and the
  empty-state sentence — and its lead is the nine words it was. **No figure in
  `website/README.md` is re-derived**: the owner's ruling of 2026-10-20 re-derives the byte
  budgets only when asked, and the round moves the whole-site total *down*, so every ceiling
  holds with the numbers further inside them than they were — which is what the budget gate
  measures on every run, whatever the README states.
- **The measurement, and the check that is still the owner's.** Nothing here was rendered: no
  browser was driven for this round and this run cannot see a picture. What was read is the
  built artifact — the page's own HTML for the section's leftovers and its bytes and words
  from the gates' own measurement — with `(cd website && npm run check)` green over all of the
  gates. **Whether the page looks right without the section is the owner's check at the
  preview** (70a–70c, P9, D190).

## The owner's 2026-10-21 feedback, items 1 and 2: the spark's drawing, and Home in the menu

The owner's feedback of **2026-10-21** is three readings of the live site (fugaro.dev) in one
message: the icon under the homepage's fourth benefit, a `Home` item at the front of the menu,
and the homepage's tab strip, which becomes a slider (the section below). This section is items
1 and 2. As every round here, the readings the feedback does not spell out are recorded, so a
later reader does not have to guess them from the markup.

- **The spark is drawn symmetric (item 1: "icon for 'The newest tools and models, ready to
  use' is asymmetrical").** `Benefits.astro` draws the four benefit glyphs as inline SVG in one
  24 × 24 box with a 2-unit `currentColor` stroke, and the fourth — the four-point star that
  means the newest tools and models — was drawn as one closed outline of straight edges with
  the north-west notch left out: `M12 3l2.2 5.8L20 11l-5.8 2.2L12 19l-2.2-5.8L4 11Z` runs from
  the west point straight to the north one, so six of its seven corners had a corner opposite
  them and the seventh had none, and the mark was drawn about y = 11 in a box whose middle is
  y = 12. Both are what a reader sees as the star leaning. The drawing is now
  `M12 4l2.2 5.8L20 12l-5.8 2.2L12 20l-2.2-5.8L4 12l5.8-2.2Z`: the same four points, the same
  four notches, the same 16-unit span, the same stroke, and the mark centred on (12, 12) like
  every other glyph of the set. **The meaning is untouched** — a spark, the newest tools and
  models, ready to use — and so are the set's size and stroke.
- **The rule that would have caught it, and its two plants.** `website/test/home.mjs`'s
  `benefitFailures` now holds a glyph that is *drawn as one closed outline of straight edges*
  to being symmetric about both of the lines through its own middle, and to that middle being
  the middle of the box. The rule is written over the drawing and not over the star by name —
  `outlineOf` reads the path's own commands — so it is a fact about the shape a glyph is rather
  than a description of today's markup, and a glyph drawn with curves or in several strokes (the
  shield, the arrows, the team) is not a mark it has anything to say about. Two defects are
  planted beside the fixture section: **the star as it shipped** (refused twice for the missing
  corner, at (9.8, 8.8), and once for the middle at (12, 11)) and **a symmetric star a unit off
  the box's middle** (refused for the middle alone). Both were also proved by hand against the
  real page: with the old drawing in `Benefits.astro` and the artifact rebuilt, the case that
  reads the built homepage fails naming the corner; with the fix in place it passes.
- **Home is the menu's first item (item 2: "I think we need a 'Home' as the first menu").**
  D182 took Home out of the menu and left the wordmark as the one link to `/`; the item is back
  as a plain `<li class="site-nav-home"><a href="/">Home</a></li>` in front of the three groups,
  in the same list, at every width — the "first menu" is the first item of the one menu the site
  has, and the phone shows that menu rather than a collapsed version of it (the 2026-10-19
  amendment). **The wordmark stays the way home it has always been**, so `/` is listed twice and
  the two links say the same thing; what moves is the current-page mark, which is now the menu's
  Home link on the home page and never the wordmark, because a page carries exactly one
  `aria-current="page"` (RF4) and the mark belongs on the link a reader looks for in a menu.
  Home is a link and not a group: home is one page and has nothing under it, so a disclosure in
  front of it would be a control that opens nothing, and it needs no script at all.
- **The phone row carries four items, and the fourth one moves the breakpoint and the row's
  spacing.** The 2026-10-19 round measured the row rather than estimating it: at 381 px the
  wordmark's text plus the three labels was a few pixels wider than the header's content box in
  the font the built site falls back to, so the text left the row below **390 px**. The Home
  link and its gap are about 45 px at that size, and the same arithmetic puts the cut at
  **432 px (27 rem)**, rounded up from the 426 px the three-item figure implies. Below the cut
  the row's own spacing is what the fourth item is paid for: the 8 px between the wordmark and
  the menu becomes 4, and the items stand with no gap between them — their own 4 px padding
  either side is what separates them, which is the 8 px a phone's labels have always been drawn
  with. That is 16 px of the 45 px back, and it is the part of the row that is neither the mark
  (the 2026-10-19 ruling keeps it) nor the labels (their 44 px height is the brief's tap
  target).
- **The gates, and what the live check has to settle.** `website/test/base-styles.mjs`'s
  `navFailures` holds the item's position (read off the list's own children, because "first" is
  the order a reader meets them in), its route and its name, refuses a Home item that holds a
  list or a disclosure, and refuses an item that is neither Home nor a group; `navLayoutFailures`
  holds the phone block's two rules, because a phone block that leaves the row's spacing standing
  is the three-item row's spacing with a fourth item in it. Each rule keeps a plant: a menu with
  no Home item, one with Home last, a Home link pointing elsewhere, a first item named something
  else, a Home item holding a list, a fifth item that is neither, and the two spacing rules
  removed. **Whether the row fits a 320 px phone's content box is a rendered measurement no
  hermetic test can take**: it is the owner's check 70a, exactly as the three-item row's fit was,
  and the arithmetic above says the row needs about 12 px more than a 320 px content box has on
  the label widths the 2026-10-19 round measured in a browser (65, 83.5 and 59.3 px at 375 px).
  If that check shows a sideways scroll, the levers are named in the code: the mark, the labels'
  own 4 px padding, and the breakpoint.
- **The bytes, re-derived in the commits that moved them.** The spark's drawing is 8 bytes onto
  the homepage's markup and so onto the site figure, and it was re-derived in its own commit —
  which also paid 12,799 bytes the stated figure had drifted by, the `cli-reference` rewrites of
  the CLI rounds (#789, #832) landing after the screenshots round re-derived (the running list in
  `website/README.md` records both, and D192's exact derivation runs only under
  `FUGARO_DERIVE_BUDGETS=1`, which CI does not set). The Home item is markup **and** stylesheet
  inlined into every page, so its move is uniform and large for one link: 170 bytes onto each of
  the 11 pages (119 of stylesheet, 51 of markup — the homepage's own current-page mark moves from
  the wordmark to the new link, which is why its markup moves by the same 51), 1,870 onto the
  site figure and 170 onto the page weight, all re-derived in its own commit. The script is
  untouched by both, and the count of files in `dist/` stays 28.
- **The measurement, and the check that is still the owner's.** Nothing here was rendered: no
  browser was driven for this round and this run cannot see a picture. What was read is the built
  artifact and the stylesheet, through the gates above, with `(cd website && npm run check)`
  green over all 415 cases. **Whether the star now reads as symmetric and whether the phone's
  header row fits at 320 px are the owner's check at the preview** (70a–70c, P9, D190).

## The owner's 2026-10-21 feedback, item 3: the tabs become a slider

The third reading of the owner's feedback of 2026-10-21 is the homepage's middle block:
"**'Say it. Get pull requests.': can we auto-rotate between tabs every few seconds? Like a
slider? Maybe a slider would work better than tabs?**" The 2026-10-19 draft approved the block as
a strip of three tabs; this section is what changed, why, and what the gates now read instead.
The draft's own section above still describes the shape the block was approved in — the three
slides, the three panels, the fragment links and the no-script switching are all still exactly
what it says — and the strip of tabs is the one part of it that is gone.

- **The controls are a row of dots and two step controls.** `HomeSlider.astro` renders
  `.slider-controls` — a previous button, a list of three dots, a next button — above the
  panels. Each dot is `<a class="slider-dot" href="#home-panel-…">` with an empty body and its
  own `aria-label` ("Slide 2 of 3: Why Fugaro?"): the circle a reader taps is a drawing, so the
  name a screen reader reads is the whole of what the dot says, and it says both the slide and
  the reader's place in the three. **The dots are fragment links and not buttons**, which is the
  2026-10-19 section's own decision kept: the block switches with no JavaScript at all, every
  panel's text is in the HTML a reader receives, and the no-script state of the block is a
  stylesheet rule rather than a script. **The two step controls are buttons**, because they step
  a block rather than going anywhere, and the markup emits them `hidden` — a control rendered
  visible by the markup is a button that does nothing for a reader whose browser runs no script
  (D183's "no broken control"), so the site's one script is what reveals them.
- **The script drives the block, and the stylesheet stands the no-script state down while it
  does.** `public/scripts/site.js` gains a `slider()` section: it finds the block by
  `[data-slider]`, writes the name of the slide on screen to the block's `data-active`, and the
  stylesheet's `.home-slider[data-active='easy'] #home-panel-easy`-style rules are what show the
  panel it belongs to. The no-script rules — the `:target` one and the one that shows the first
  panel while nothing is targeted — are scoped `.home-slider:not([data-active])`, and that
  scoping is load-bearing rather than tidiness: a reader who arrived on `#home-panel-see` and
  then let the block advance would otherwise have **two** panels shown at once, because the
  `:target` selector and the script's own rule are both (1,2,0) and the earlier one would win.
  The script also marks the dot of the slide on screen with `aria-current="true"`, which the
  stylesheet draws in the accent — the mark a reader has instead of a text label.
- **The clock, and the four things that stop it.** The block advances every six seconds
  (`ADVANCE_MS = 6000`) and wraps from the third slide back to the first. It is held while the
  pointer is over the block or the keyboard's focus is inside it (a reader who is reading must
  not have the slide move under them), it does not run while the page is not being shown at all
  (`document.hidden`), and it does not run for a reader whose system asks for less motion
  (`prefers-reduced-motion: reduce`) — for that reader the dots, the two controls and the arrow
  keys all still work, because what they asked for is that nothing move **on its own**. A click
  on a dot is `preventDefault`ed: with the script running the block's state is its own, and an
  address that kept the fragment would send the reader back to that slide on a reload. The script
  never writes to the address. Arrow keys work on the dots and on the two controls, and every
  other key is left to the browser, because Tab has to reach the next control.
- **The stub DOMs the whole file runs in, and why the clock is behind a short circuit.**
  `website/test/copy.mjs`, `website/test/consent.mjs` and `website/test/nav-hover.mjs` each run
  the real `site.js` against the built homepage, and none of them is a browser: they have no
  `document.hidden` and two of them have no `matchMedia`. So `still()` reads
  `held || !shown() || calm()`, and `shown()` is `document.hidden === false` — a document that
  cannot answer is a document no clock is started in, which is both the correct reading of a
  browser that does not answer and the reason those three gates keep their timer and query
  assertions unchanged. `website/test/consent.mjs`'s two selector maps did have to learn the
  block: that stub refuses a selector it does not read rather than matching nothing, so the five
  `data-slider` attributes are written into it, and the file's own comment says why.
- **What the gates read now.** `website/test/slider.mjs` is rewritten around the slider, and its
  six rules are: the three slides and the three dots paired (each dot a fragment link to the
  panel it opens, named after it, announced with its place in the three, and the panel headed
  with the same label); the stylesheet read in **both** states, one selector at a time so a rule
  that marks one dot for another dot's panel is refused rather than counted as a pair present;
  the block held to carrying no script and the script to naming no id and no class of the block
  (the two files are joined by the `data-` attributes alone); the 44 px targets, the wrapping
  rows, the focus ring and the 360 px content box; **the behaviour, run** — the file the site
  ships, against the built homepage, in a fourth stub DOM written for this gate, with seven
  cases (the first slide and both controls revealed; the clock stepping and wrapping; the hold
  under the pointer and under the keyboard's focus; a page nobody is shown; less motion; the
  dots, the controls and the arrow keys, and the address left alone; a reader arriving on a
  fragment) and each one planted against the line of the script it reads, with the plant
  asserted to parse; and the steps' provenance, unchanged. `website/test/home.mjs`'s gate over
  the block reads dots where it read tabs, `fold.mjs`'s fixture and `stacking.mjs`'s class list
  follow the new markup, and `website/test/budget.mjs`'s ceilings are re-derived in the commit
  that moved the bytes.
- **The bytes.** The block is only on the homepage, so its own bytes land there and nowhere
  else: **2,186 onto the homepage's HTML** — 1,054 of stylesheet and 1,132 of markup — and
  **10,912 onto the script**, which every page loads once. The page weight (the console page,
  still the heaviest) moves by the script alone, 399,902 → **410,814**; the site figure by both,
  1,402,356 → **1,415,454**; and all three of the README's byte budgets are re-derived in the
  commit that ships the script (D192). The count of files in `dist/` stays **28** and the count
  of pages **11**, and the homepage's own words go 468 → **461**, because a dot is a circle with
  no text in it: the three labels the tabs showed are now the dots' names, which a screen reader
  reads and the page does not draw.
- **The live check, and the one line of it that had to change with the page.**
  `docs/gcp-live-checklist.md`'s check 70b reads the block, so its Method now reads `SLIDES` out
  of the component and the clock out of the script, and its step 2 reads dots rather than tabs.
  **The step also tells the owner to put the pointer over the block before reading a panel**:
  the block moves on by itself six seconds after the page lands, and a slide that is being read
  stays where it is only while the pointer or the focus is inside it — a live check that took
  longer than six seconds to read a panel would otherwise be reading a different panel than the
  one it chose.
- **The measurement, and the check that is still the owner's.** Nothing here was rendered: no
  browser was driven for this round and this run cannot see a picture. What was read is the built
  artifact, the stylesheet and the script, through the gates above, with `(cd website && npm run
  check)` green over all 423 cases. **Whether the slider reads as a slider — the look of the
  dots, the feel of the six seconds, whether the motion is too fast or too slow — is the owner's
  check** (70a–70c, P9, D190), and the number to move first if it is wrong is `ADVANCE_MS` in
  `public/scripts/site.js`.
