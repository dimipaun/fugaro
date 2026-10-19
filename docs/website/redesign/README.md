# Website redesign

**Status: the plan is implemented but for its last part, and items 2, 3 and 4 of
[the changes spec](changes-spec-2026-10-18.md) are implemented in this branch's pull
request.** Eight of the plan's nine parts are merged on `main` — `P1` and `P2` (#568), `P4`
(#572), `P3`, `P6` and `P7` (#575), `P8` (#574) and `P5` (#603) — and **`P9`**, the design
record and the "after" set, is the one still to do. This branch's pull request is later than
all of them, and it changes what they built — the hero's headline and its example, and the
four-step flow — which the readings under "Implementing items 2, 3 and 4" below record.

The site itself **is** built: `website/` holds an eleven-route Astro site whose hermetic
gates pass (268 tests, `npm run check`, measured 2026-10-19). The three documents of the
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
- **D182** the menu is three groups of three (Product, Reference, Project), a drawer on mobile.
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
- **The spec's `fugaro update` means `fugaro upgrade`.** There is no `update` command; the
  command that pins the plugin and moves the job images onto a release's base image is
  `fugaro upgrade` (`README.md` `## Upgrading`). Where the spec writes `update`, this work
  writes `upgrade`.

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
  item 3, the agent's two answers from `plugin/skills/parallelism/SKILL.md` and
  `plugin/skills/working/SKILL.md`, and the launch from the invocation the `parallelism`
  skill documents, with its own `<name>` and `<id>` placeholders — and the build holds each
  line to its document, in that document's order. A copy of the repository staged for a test
  build therefore carries `plugin/skills/`. (The caption is neither: it is the page's own
  sentence, and the next reading is what it may say.)
- **The chat's caption says what the figure is, not where every line came from.** The caption
  claimed "every line is the repository's own words, from the skills the agent reads", and
  that is false about two of the things the figure prints: the request is **item 3's own
  example** of one (a line of the spec, not of a skill), and the three `Pull request n of 3`
  labels are the illustration's own, so the last review's finding asked for the wording to be
  corrected. It reads "An illustration of the workflow, not a recording of a session: what you
  type, and the shape of what your agent does and reports. The agent's behaviour comes from
  the skills Fugaro installs." — the figure's own label, and the one sentence in it that is
  neither a quotation nor a result label. The rule above is therefore stated of the lines a
  turn *speaks*, which is the part of the figure a document vouches for.
  `website/test/home.mjs` holds the caption to that reading (it says
  it is an illustration and not a recording, it says where the agent's behaviour comes from,
  it claims no quotation of the figure's lines, and the three results are checked not to be
  lines of the document the turn cites), and `website/test/wording.mjs` refuses the phrases
  the false sentence was made of — on every built page and in the site's own sources, with a
  case that plants the caption back and proves the refusal, so the claim cannot return through
  a rewrite.
- **Item 4's row starts at 64 rem, and a step name wraps.** The four cards share the row, so
  a card is a quarter of the column less the arrows between them: at 48 rem — the breakpoint
  the chrome switches at — a card's content box is about 100 px, which is narrower than the
  word `Ephemeral` in the spec's own third step name, and the name was held on one line
  beside a `flex: none` number, so the flow could not render its own labels at any width below
  about 79 rem. The row now starts at 64 rem, the name wraps (it is a heading, not a label)
  and the number sits above it inside the row. The arithmetic is not a reading of a
  screenshot: `website/test/base-styles.mjs` computes the card's content box from the built
  stylesheet and holds it against the longest word of the names the built page prints, at the
  breakpoint and at the page's maximum width, so a name that outgrows its card or a row that
  starts earlier fails there naming the width. Whether the picture then *looks* right at
  1280 px is the owner's check (70a–70c), as D190 requires, and this line of work cannot see
  it.
- **The arrows point the way the run travels, and the fix round is where the sign was set.**
  The arrow is drawn pointing down (the shaft is `M12 1 V 25`), a positive angle in CSS turns
  clockwise, so the row's `rotate(90deg)` pointed every arrow back at the step before it — the
  four steps drawn in reverse by the one mark that says which way the run goes. The turn is
  `rotate(-90deg)` now, and `website/test/base-styles.mjs` reads the drawn direction out of
  the markup and the rotation out of the built stylesheet at the breakpoint, at the page's
  maximum width and below the breakpoint, where the arrow has to point down the column. A
  rotation the check cannot read is refused rather than assumed.
- **The byte budgets are re-derived in this branch (D192).** Its items change what the
  artifact weighs — the terminal's transcript goes, the chat comes, `site.js` loses 117 bytes,
  the flow's stylesheet changes — so all three figures move with it, from the build of
  2026-10-19: **JavaScript per page 76,086** (three times 25,362; `public/scripts/site.js`
  20,289 bytes, unmoved since the second round), **page weight per page 927,834** (three times
  309,278; the docs hub 247,422 bytes) and **whole site 3,059,394** (three times 1,019,798;
  815,838 bytes across 21 files — re-derived again in the second fix round on this branch,
  which moved the homepage's order, the flow's treatment, the chat's launch line, the install
  command's wrapping and the chat's own turns out of the measure's cap, and a fourth time
  across the third fix round, whose four commits moved the site figure to 814,455, 815,116 and
  815,212 bytes and the page figure with it, each re-derived in the commit that shipped the
  bytes, and a fifth time by the fourth round, whose fix for the two-by-two's wrap arrow moved
  it 626 bytes on the two pages that draw the flow). The
  figures live in `website/README.md` and nowhere else, and
  `website/test/budget.mjs` reads them from there and holds each to the build it can measure:
  a derivation that names another build fails, which is what stops the page and the site
  figures drifting down as pages grow (they had — page weight and whole site still named the
  build of 2026-10-18 after four parts added bytes to it).
- **The hero offers one command to copy, and the screenshot's place is empty.** Item 3's
  acceptance is that the only command a reader is likely to type is `fugaro watch`, so the
  `fugaro run` transcript the homepage carried (`Terminal.astro`, D184) is removed from it —
  `fugaro run` stays in the documentation as the manual path — and the snippet that keeps
  `fugaro watch` is a command block like every other one on the site, with its own copy
  control and the skill's own line about what the screen shows under it. The agent's launch
  is a quiet line inside a turn rather than a command block, because the reader did not type
  it. Item 5's screenshot is a separate task: this pull request leaves
  `[data-slot="watch-screenshot"]` right after the chat and commits **no image**, and
  `website/test/home.mjs` holds that slot to being empty and the homepage to its one picture
  (the wordmark). **The watch bar is the site's one command block that puts its control beside
  its command rather than above it**, and the fix round gave it `flex-direction: row-reverse`
  to do it: the control comes first in the shared markup (`src/lib/copy-control.mjs`), so a
  plain row drew it at the left of the command it belongs to — the other way round from
  `base.css`'s rule and from every other block. `website/test/base-styles.mjs` reads every
  `[data-copy]` block's own rules and refuses one whose control is laid out first.
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
    one card of four turns: a fixed 7 rem speaker column from 35 rem up (a `max-content`
    column resolves per row and aligns nothing), one column below it, the agent's turn marked
    by the accent's own left border. The caption moved out from under "Watch it run" to
    directly under the conversation, and the whole watch block — heading, item 5's slot, the
    command bar, the note — is now a sibling of the figure rather than inside it, so the
    caption can be the figure's last child. `chatConversationFailures` reads the card, the
    speaker column and the caption's place; `website/test/home.mjs` reads the slot as the
    element after the watch heading.
  - **the empty slot takes no page** (item 3b). `[data-slot="watch-screenshot"]` with no rule
    of its own was a box of blank page between the conversation and the command bar, which is
    the ~140 px the review measured. `.chat-shot:empty { display: none }` takes it out until
    PR #622's image lands, and `chatSlotFailures` refuses a slot rule that gives it height,
    padding, a border or a background, and requires the rule that will size the image
    (`.chat-shot > img`) so that adding the image stays one line of markup.
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
  written with a `span`. The whole-site budget moved 626 bytes with the fix, re-derived in the
  same commit (D192). This round was a reading of the stylesheet and of the gate, not a look at
  the page — the same limit as the round above.
