# Website redesign

**Status: the plan is implemented but for its last part, and items 2, 3 and 4 of
[the changes spec](changes-spec-2026-10-18.md) are implemented in this branch's pull
request.** Eight of the plan's nine parts are merged on `main` — `P1` and `P2` (#568), `P4`
(#572), `P3`, `P6` and `P7` (#575), `P8` (#574) and `P5` (#603) — and **`P9`**, the design
record and the "after" set, is the one still to do. This branch's pull request is later than
all of them, and it changes what they built — the hero's headline and its example, and the
four-step flow — which the readings under "Implementing items 2, 3 and 4" below record.

The site itself **is** built: `website/` holds an eleven-route Astro site whose hermetic
gates pass (252 tests, `npm run check`, measured 2026-10-19). The three documents of the
first line of work — [`../README.md`](../README.md) `:3` and `:5`,
[`../reconciliation.md`](../reconciliation.md) `:3`,
[`../implementation-plan.md`](../implementation-plan.md) `:3` — still say the opposite,
in the present tense; correcting them is the first part of this line of work
(reconciliation finding **RF1**, and `P1` did it for the three lines it names).

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
- **The tagline change reaches the CLI, and the two plugin skills, only in 0.9.0.** What
  changes in this run is the website and `README.md` (item 2's own words: "website and
  README only in this run"). The wording stays, untouched, where it is generated from or
  pinned to the CLI: `internal/cli`'s root command help (`Fleeting cloud workers for coding
  agents`), the generated `website/content/cli-reference.md` that renders it, and the two
  plugin skills that carry it (`plugin/skills/routing`, `plugin/skills/working`). The two
  release surfaces that carry it as well — `.goreleaser.yaml`'s cask description and
  `.claude-plugin/marketplace.json`'s plugin description — are likewise not this run's:
  they are the CLI's release metadata, not the site's, and the same 0.9.0 change moves
  them. The acceptance grep therefore still matches `website/content/cli-reference.md`, and
  that one match is the ruling, not an oversight: the file is generated by
  `go run ./internal/clidoc` from the CLI's own help, and editing it by hand is the
  forbidden change (the generated-file rule).
- **The spec's `fugaro update` means `fugaro upgrade`.** There is no `update` command; the
  command that pins the plugin and moves the job images onto a release's base image is
  `fugaro upgrade` (`README.md` `## Upgrading`). Where the spec writes `update`, this work
  writes `upgrade`.

## Implementing items 2, 3 and 4: what those three items settle

Items 2, 3 and 4 of the changes spec are implemented in this branch's pull request. Four
readings the spec does not spell out are recorded here, so that a later reader of the site
does not have to guess them from the markup.

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
  a run ends with. Everything else the chat shows is quoted the same way — the request from
  item 3, the agent's two answers from `plugin/skills/parallelism/SKILL.md` and
  `plugin/skills/working/SKILL.md`, and the launch from the invocation the `parallelism`
  skill documents, with its own `<name>` and `<id>` placeholders — and the build holds each
  line to its document, in that document's order. A copy of the repository staged for a test
  build therefore carries `plugin/skills/`.
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
  (the wordmark).
