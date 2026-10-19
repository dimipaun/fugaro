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
- **D186** a build gate refuses the listed provenance phrases; **D188** the lead is at most 40 words on every page and the whole page at most 250 words (not Docs, Security, Changelog).
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
  than out of a copy in the component, so the picture and the ruling cannot drift.
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
  heaviest page, and the slider ships no script at all.
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
  round made the chat one conversation card at the measure's own width, and the gate that
  reads it now is `chatConversationFailures` — see the reading below. The history is kept
  here because the round that found the hole was the round that measured the page rather
  than read it.) The `go install` command, which the same review found clipped at
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
    caption can be the figure's last child. What reads that markup is `website/test/home.mjs`,
    not the stylesheet gate: it reads the caption as the figure's last child, the caption's
    words (`chat-caption`, an illustration and not a recording), and the slot as the element
    after the watch heading. `chatConversationFailures` reads the stylesheet — the one-column
    turn list, the card, the measure's cap, the speaker column and the two speakers' sides —
    and nothing about the caption.
  - **the empty slot takes no page** (item 3b). `[data-slot="watch-screenshot"]` with no rule
    of its own was a box of blank page between the conversation and the command bar, which is
    the ~140 px the review measured. `.chat-shot:empty { display: none }` takes it out until
    PR #622's image lands — and it is kept now that the image is in the slot, because it is the
    rule that keeps a placeholder from coming back — and `chatSlotFailures` refuses a slot rule
    that gives it height, padding, a border or a background, and holds the rule that sizes the
    image (`.chat-shot > img`) to scaling it: a width that is a percentage of the column and a
    height that follows the file's ratio.
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
  opposite sides drew two marks for one thing. `chatConversationFailures` refuses a turn whose
  body has no side of its own (a grid item's `normal` is `stretch`, which draws a band rather
  than a message) and refuses both speakers on one side; the third round's refusal of a card on
  `.chat-turn` itself stands, and a bubble is not that: those were three cards across the page,
  each stretched to the row's tallest, and this is one content-sized box in a row of the one
  card. **The flow's cards lost three of their four cyan edges** — the sides and the bottom are
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

**It is on the homepage now** (`website/src/components/AgentChat.astro`): the slot
`[data-slot="watch-screenshot"]` under the chat holds it, so the block that was empty until the asset
existed is the picture the owner took. The markup declares the capture's logical size (1459 x 441, the
2x file's own ratio) so the box is reserved before the file arrives, `loading="lazy"` because the block
sits under the whole conversation and nothing above the fold waits on it, and the `alt` is the gist of
`fugaro-watch.txt` — the transcription ships as the text a reader gets when the picture does not.
`website/test/home.mjs` holds all four (in the slot, a non-empty `alt`, the declared dimensions, and no
external URL), and `website/test/base-styles.mjs`'s `chatSlotFailures` holds the sizing that makes it
scale — a width that is a percentage of the column it sits in and a height that follows the file's own
ratio, refused if either becomes a pixel count (the two planted cases). The rendered page was measured in
a headless Chromium over the built site at 1280, 768 and 375: `scrollWidth` is the viewport's width at all
three (no horizontal scroll, the picture being the page's own and nothing else overflowing), and the
screenshot lays out at 750 x 228, 722 x 220 and 329 x 101 px — the column's width and the capture's ratio
at each. Whether the rendered page matches the owner's design is the owner's check (70a-70c, D190), as no
gate here can see an image: at 375 px the picture is a quarter of the width of the screen it captures, so
its small print is not readable at that width and the transcription beside it is what a reader reads
instead.

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
