# Website redesign — reconciliation

Status: **plan only; nothing of the redesign is implemented.** Owner: **@Dimi**. Date:
**2026-10-18**. Source: [spec.md](spec.md) — the owner's text, committed verbatim and
alone in this branch's first commit, before anything that answers it. The plan this
document produces: [implementation-plan.md](implementation-plan.md). The "before"
pictures: [current/](current/), with their own README.

This is the second line of work on the site. The first is
[`../reconciliation.md`](../reconciliation.md) (findings `F1`–`F63`, decisions
`D90`–`D109`, all answered 2026-10-16) and [`../implementation-plan.md`](../implementation-plan.md)
(tasks `W0.1`–`W3.3`). **This document cites those findings and decisions rather than
repeating them** — the site's own rule is that a page renders a fact and never
restates one, and the same holds for a document like this one.

## 0. The one thing to know before reading further

The site the brief describes is **built**. On this branch's base (`85758e1`),
`website/` holds an eleven-route Astro site whose hermetic gates are green:
`npm run build` produces 11 pages, and `npm run check` — the site's own gate, run for
this document on 2026-10-18 in a container with Node v22.22.0 and no browser — passes
**179 tests out of 179 in 72.5 s**. The Go side (`internal/cli`, the site's pins) is
run separately; §7 records both.

Three documents in this repository say the opposite, in the present tense:
`../README.md:3` and `:5` ("**Status: plan only; nothing is implemented**",
"**No site code exists**: there is no `website/` directory…"), `../reconciliation.md:3`
and `../implementation-plan.md:3`. They are wrong, and finding **RF1** is that.

Everything below is measured against that built site, not against the plan for it.

## 1. How to read this

Three labels, and each finding carries exactly one:

- **Wrong** — the tree or its documents state something that is not true today.
- **Exists** — the brief asks for something the site already does; the work is to keep
  it, not to build it.
- **Gap** — nothing does it. The brief's item, unanswered.
- **Rule** — a rule of this repository that constrains how the gap may be closed. A
  `Rule` is not an obstacle to argue with in a plan; it is the shape of the answer.
- **Open** — a question no reader can settle from the tree. `Open` findings become the
  numbered decisions in §9.

Findings are numbered **`RF<n>`** — "redesign finding" — because `F1`–`F63` belong to
the first line of work and a re-used number is a false citation. Decisions are numbered
**`D110` onward**; §9 says why that number and not another.

Every finding cites the file and the symbol it was read from. Every measurement in this
document was taken on 2026-10-18, on this branch's base, and the command that took it is
named where it matters. Nothing in this document is a description of an image: the
model that took the screenshots cannot view one, so §5's colours and contrast ratios are
numbers computed from pixel data and from CSS, and the visual comparison the brief's
process step asks for is left to the owner.

## 2. Findings

### RF1 — **Wrong** — three documents say the site does not exist

`../README.md:3` says "Status: plan only; nothing is implemented"; `:5` says "**No site
code exists**: there is no `website/` directory, no hosting configuration, no domain and
no web asset of any kind in this repository today (F1)". `../reconciliation.md:3` and
`../implementation-plan.md:3` carry the same status line. Meanwhile `website/` holds 11
routes, `firebase.json` holds the hosting and CSP configuration, `brand/` holds the logo
and the icons, and `website/data/site.yaml` names the canonical origin.

Consequence for this line of work: **the redesign's own README states the real status**,
and correcting the three first-line documents is part 1 of the plan — one commit, three
status lines, no other edit, so the correction cannot hide inside a redesign.

### RF2 — **Exists** — a site exists, it is honest, and it is gated

Eleven routes (`/`, `/how-it-works/`, `/getting-started/`, `/caps-and-costs/`,
`/console/`, `/docs/`, `/security/`, `/changelog/`, `/faq/`, `/community/`,
`/privacy/`), built by `website/src/pages/*.astro`; a content pipeline in
`website/src/content.ts` that fails the build naming the file when a generated document
is missing; 179 hermetic gate tests under `website/test/`; 22 Go pins under
`internal/cli` (`TestWebsite*`, `TestCliReference*`). The redesign's job is not to
establish any of this.

### RF3 — **Gap** — the navigation is ten flat items and wraps onto three rows at 390 px

`website/src/layouts/Base.astro`'s `NAV` is a flat list of ten `{href, label}` entries
rendered as one `<ul>`; `website/src/styles/base.css:117-122` carries the comment that is
the brief's own complaint — "a horizontal bar that wraps onto as many lines as the
viewport needs. It needs no script and no hidden menu: at 360px the ten sections are
three short lines."

Measured with Playwright on the built site (script: [capture-screenshots.mjs](capture-screenshots.mjs)):

| | header height | nav rows | nav links |
| --- | --- | --- | --- |
| 1280 × 900 | 101 px | 2 | 10 |
| 390 × 844 | **177 px** | **3** | 10 |

177 px is 21 % of a 390 × 844 first screen. There is no dropdown, no drawer, no
hamburger, and no `<details>`/`<summary>` anywhere in the markup. Spec item 3, confirmed
by measurement.

### RF4 — **Rule** — the nav has a keyboard contract, and it is asserted

`website/test/base-styles.mjs` asserts, per built page, that `.site-nav ul` exists and is
`display:flex`, that `a[aria-current=page]` has a rule, and that the section nav is a
labelled landmark of keyboard-reachable links with **exactly one** `aria-current="page"`.
`website/test/a11y.mjs` adds focus outlines, no positive `tabindex`, and the
`role="button"`-must-be-focusable rule. A two-level menu, a drawer, or a `<details>`
disclosure changes what those assertions mean: the part of the plan that changes the nav
changes `base-styles.mjs` in the same commit, and the current-page rule stays true for
exactly one item.

### RF5 — **Gap** — the top of Getting Started is internal text

`website/src/pages/getting-started.astro:61` is the first thing under the `h1`:

> Every command on this page is rendered from `website/data/install.yaml`, which
> `internal/cli/docs_website_install_test.go` holds to `README.md`, `docs/release.md`
> and `.goreleaser.yaml` … Nothing here is typed into this page.

It names three repository files and one Go test file to a visitor who has not installed
anything. Measured, the page's first screen at both viewports is that paragraph, and the
commands the brief wants "right at the top, up and running in 30 seconds" come after it.
Spec item 1 and the brief's core principle, in the same place.

### RF6 — **Gap** — the docs hub's lead and its epilogue are internal text

`website/src/pages/docs.astro:232-235` — "Every guide below is a document of this
repository, where it changes with the code that makes it true; this page says what each
one … document that is generated rather than written." `docs.astro:259-262` — "The
reference above is rendered as it is generated, by `<code>{reference.generator}</code>`;
the site's own README is where its build and its generated files are documented."

The second sentence is the site explaining its own build system to a reader who came for
the CLI reference. Spec item 1 names exactly this ("remove the intro paragraphs that say
the content is generated / not hand-typed").

### RF7 — **Gap** — the same class on FAQ and Community

`website/src/pages/faq.astro:397` — "Six answers, each one taken from a document in this
repository rather than written here". `website/src/pages/community.astro:224` — "This
page points at those documents rather than repeating them".

### RF8 — **Rule** — no gate can see RF5–RF7, and the rule needs a definition first

A search of `website/test/*.mjs` for the sentences of RF5–RF7, for "generated by", for
"not typed" and for "written here" finds **nothing**: the gates check facts, links,
origins, contrast, layout and budgets, and no gate has an opinion about a page talking
about its own provenance. So "Done when: no internal/meta text" needs a new test — and
that test needs a definition, because several pages legitimately name repository files:
`privacy.astro` describes the site's own posture, `security.astro:43` builds its contents
from `SECURITY.md`'s headings, and the docs hub cites the document each guide lives in.
The distinction the test can enforce is narrower and checkable: **a page must not tell a
reader why its own text exists.** §9 (D116) puts the precise rule to the owner; §4 of the
plan makes it a gate.

### RF9 — **Exists** — there is already a CSS-only terminal that types a real command

`website/src/components/Terminal.astro` renders the command in full and reveals it with
`animation: terminal-type 2.4s steps(var(--terminal-characters), end) both`, so the page
ships **zero** script and the animation is the reveal of text that is entirely present.
Its command is held at build time to `README.md` and to `content/cli-reference.md`, so
the animation cannot outlive the command it shows. Under `prefers-reduced-motion: reduce`
the layout's global rule (`Base.astro`) neutralises it and the command is simply there.
Spec item 5 is partly satisfied: the typewriter, the honesty and the reduced-motion path
exist.

### RF10 — **Gap** — but it is one command, not a run

The terminal shows `fugaro run "add a --verbose flag to the export command"` and then
stops. There is no dispatch line, no Cloud Run job starting, no build, no push, no pull
request — the four beats the brief names ("starting the task, the Cloud Run job spinning
up, the pull request at the end"). No other animation exists on the site.

### RF11 — **Rule** — an animation may not invent a fact

This repository's site-wide rule is that a page renders a fact and never restates one,
and it is enforced by construction: `ArchitectureDiagram.astro` throws, naming the file,
when a caption is not a line of the document it cites; `docs.astro` renders the generated
documents rather than quoting them; `TestWebsiteSecurityPageIsByteIdenticalToSecurityMd`
holds a page to a file byte for byte.

A terminal transcript is the easiest place in the site to break that rule: a job
identifier, an elapsed time, a branch name, a run URL or a pull-request number shown in
the animation is a fact about a run that did not happen. The same holds for a fake
"spending" figure. So the run animation can only show **lines the repository already
writes** — `README.md`'s `## How it works` numbered steps are the obvious source, exactly
as the five diagram captions are today — or it must be labelled, on the page, as an
illustration rather than a recording. §9 (D114) is this decision.

### RF12 — **Gap** — How It Works is a list of five, the brief asks for a flow of four

`website/src/components/ArchitectureDiagram.astro`'s `STAGES` has **five** entries
(`local dispatch`, `cloud job`, `cache`, `security boundary`, `PR or artifact collected
back`), each an `<h2>` and one quoted caption, over an `aria-hidden` SVG rail; the page
`website/src/pages/how-it-works.astro` is an `h1` and that figure — 107 words, 1,293 px
tall at 1280. The brief's flow is **four** steps: task submitted → ephemeral Cloud Run
job → agent runs → result / pull request.

The two do not have the same number of steps and are not the same steps: the site's
"cache" and its "security boundary" are not in the brief's four, and the brief's "agent
runs" is not one of the site's five. Collapsing five into four is therefore not a layout
change, it is a change to what the page claims, and every caption has to survive RF11's
rule. §9 (D115) is this decision; the plan's part is written for either answer, and the
recommendation is to keep the five quoted stages and to make the *picture* the brief
asks for out of them (a vertical flow with one line each), because the brief's four steps
are a summary of the README's steps rather than a different set.

### RF13 — **Gap** — no command block has a copy button

Measured over the built site: `[data-copy]`, `.copy-button` and `button.copy` match
**zero** elements on all 22 page/viewport pairs, while there are **23** `<pre>` blocks —
3 on the home page, 10 on Getting Started, 10 on the Docs hub. Spec item 5.

### RF14 — **Rule** — what a copy button costs, and what it does not

Measured: every page ships **0 bytes** of inline script today, and
`website/test/budget.mjs` counts a page's JavaScript as its executable inline scripts
**plus the script files and module preloads it fetches** (`measurePage`), its weight as
the HTML plus every first-party file the page fetches, and the site total as every file
in `dist/`.

Two things follow, and they are good news for this brief:

- **The budget does not have to move.** `website/README.md:89-90` sets **JavaScript per
  page: 100,000 bytes**, deliberately loose by the owner's ruling of 2026-10-17 —
  "three times what the build needs … a later optimisation pass tightens them" — and its
  own words say the allowance "leaves room for W1.12's consent banner and the logo work
  without a budget edit". A copy controller is a kilobyte or two against a hundred. The
  same holds for the page-weight ceiling (662,766 B; the heaviest page is the docs hub)
  and the site total (1,454,826 B; the whole of `dist/` was 695,920 B on 2026-10-18).
- **Two prose statements do have to change**, in the commit that ships the script,
  because they become false: `website/README.md:89` ("The site ships no script today")
  and `website/test/budget.mjs:16-17` (the tight derivation, "JavaScript per page: 0
  bytes. The built site ships no script at all today (no island, no inline script …)").
  Neither is read by a gate — `budgetFailures` reads the numbers, not the prose — which
  is exactly why they must be changed by hand in the same commit and why the plan makes
  it a step rather than a hope.

### RF15 — **Rule** — the strict CSP stays, and it decides the shape of the script

The owner's ruling of 2026-10-18: "use as much JavaScript on the website as needed. The
strict CSP stays (`script-src 'self'`, no remote origins), the origins gate stays (hash
allow-list or file scripts)". Read out of the tree: `firebase.json`'s policy is
`default-src 'self'; script-src 'self'` with no nonce and no `unsafe-inline` and no
`unsafe-eval`, and `website/test/origins.mjs`'s `INLINE_SCRIPTS` is a Map keyed by
`sha256-<base64>` whose single entry is the consent banner controller (D93), with
fixture cases proving it refuses an unlisted inline script and a remote origin.

So a script has exactly two admissible shapes: **a first-party file** (for example
`website/public/scripts/copy.js`, fetched from `'self'`, no CSP edit, no allow-list
entry, counted by `budget.mjs` under `js`), or **an inline script whose sha256 is added
to `INLINE_SCRIPTS`** and to the deployed policy. The plan recommends the file — one
mechanism, no hash to maintain per page, and it keeps the CSP and the allow-list exactly
as they are — and §9 (D113) records it as the owner's call because it is the CSP's shape
that is being chosen.

### RF16 — **Rule** — the palette is pinned in four places at once

`website/src/styles/palette-split.css` is the palette in force (D104's option C),
selected by `website/test/a11y.mjs`'s `PALETTE_IN_FORCE`; its sibling files
`palette-violet.css` and `palette-orange.css` are the other two, and `a11y.mjs`'s
`PALETTE_FILES` lints all three. `website/test/brand.mjs` holds
`website/public/site.webmanifest:6-7`'s `theme_color`/`background_color` equal to the
palette's `--ground` (both `#0B0B12` today), and `a11y.mjs`'s `PAIRS`/`BASE_PAIRS` check
every text/surface/gradient/accent/warn pair **in the palette in force** over the built
CSS. A new accent therefore touches: `palette-split.css`, the manifest's two colours (if
`--ground` moves with the accent, which it does in §5's derivation), the a11y pair list
(only if a pair that is not there today appears), and — where the brief's own words say
the "current electric highlight colour does not match the logo" — nothing else, because
`brand/fugaro-logo.png` is the source of the hue rather than a file to re-colour.

### RF17 — **Rule** — reduced motion is not optional, and it is asserted

`Base.astro` carries the global neutralisation; `a11y.mjs` asserts a reduced-motion path
for every animation; `Terminal.astro` and `ArchitectureDiagram.astro` each guard their
animation in a preference query *and* a feature query (`@supports (animation-timeline:
view())`). The homepage run animation inherits all of it: under `reduce`, the page shows
the finished transcript, still, in full.

### RF18 — **Wrong (confirmed by measurement)** — the accent does not match the logo

Sampled from `brand/fugaro-logo.png` (1024 × 1024, RGBA, 115,370 fully opaque pixels):
the saturation-weighted circular mean hue is **198.5°** at saturation ≥ 0.5, **199.6°**
at ≥ 0.6, **201.4°** at ≥ 0.7 and **204.2°** at ≥ 0.8; the modal 5° bins are **200–204
(23.7 %)**, 195–199 (17.7 %), 205–209 (13.3 %). The logo's hue is a cyan-blue around
**198–204°**.

The palette in force: `--accent: #8B5CF6` (hue **258.3°**), `--accent-on-dark: #C4B5FD`
(hue **252.5°**), `--warn: #F97316` (hue **24.6°**). **The accent is about 57° away from
the logo's own hue and the warning colour is about 180° away from it.** The brief's
complaint is not a matter of taste; it is a measurement. §5 records the derived palette
and its contrast matrix.

### RF19 — **Open** — there is no light theme, and the brief asks about light and dark

The brief: "check WCAG contrast of text on it, light and dark". The site has **one**
appearance: `palette-split.css` is a dark-ground palette and `base.css` carries no
`prefers-color-scheme` block. The brief's "light and dark" is therefore a question with
two readings — "the accent's contrast against a light background *and* against a dark
one" (a property of the colour, which §5 computes) or "the site gains a light theme"
(a second palette, a second set of pairs and a `prefers-color-scheme` branch, which is
work the brief does not otherwise ask for). §9 (D119) asks which, recommending the first
and recording the second as a later, separate line of work.

### RF20 — **Rule** — what an image may be, and where it may come from

`website/data/site.yaml`'s `allowed_hosts` is `[github.com, raw.githubusercontent.com,
fugaro.dev]` and `origins.mjs` admits an image or a media file only from those hosts;
`D106` admits no third-party script, font, stylesheet, embed or pixel except D93's
consent-gated one; `D108`'s fourth gate is the owner's live check. So "good images or
diagrams" can only be **first-party files in the artifact**: SVG, CSS, or an image the
repository itself holds. Nothing may be embedded from a design tool, a stock library or
a CDN — and `TestWebsiteNamesNoSecondDomain` and `TestWebsiteCanonicalHostAppearsOnlyInSiteYaml`
say the same for any URL a page carries.

### RF21 — **Gap** — the site has no picture of itself

The only `<img>` on the site is the header logo (`Base.astro`, `/logo.png`, `alt=""`,
drawn at half its pixel size), and the only drawing is the diagram's rail: an
`aria-hidden` SVG line with five circles. There is no product image, no screenshot, no
second diagram. `site.yaml` sets `features.video: false`, so no player may appear
(`Check 69a` greps the home page for one and requires silence).

### RF22 — **Rule** — this deliverable is documents, and the code is later

`../implementation-plan.md` §3.1 and this brief: this branch carries **documents only**.
Everything the plan describes is a later pull request, each starting on the owner's go
(§7.1) and each passing through §15's approval gate. This reconciliation and the plan
beside it therefore contain no site change, and the plan's parts are ordered so that
each one is independently mergeable and separately verifiable.

### RF23 — **Rule/Gap** — "above the fold" and "very few words" are, today, unwritten and unmeasured

No gate measures words or viewport position. `budget.mjs` measures bytes; `a11y.mjs`
measures structure; both read the **built HTML and CSS with no browser**, and the site's
gates are deliberately browser-free (`website/README.md:96-98`: "there is no CI on pull
requests and the tests here are hermetic, so a browser-driven audit is not one of
them"). So the brief's central promise — "the gist above the fold on every page" — cannot
be a hermetic gate in the shape the others have. It can be (a) a word ceiling per page,
enforced hermetically, plus (b) a fold check, which needs a browser and therefore is
either a new owner-run live check or the first browser-driven test in this repository.
§9 (D117) is this decision, and §5 of the plan writes the word ceiling either way.

Measured today, the word count of the whole visible text of each page is: home 89,
how-it-works 107, getting-started 431, caps-and-costs 487, console 200, **docs 18,282**,
**security 6,240**, **changelog 6,191**, faq 493, community 268, privacy 277.

### RF24 — **Gap** — three pages are effectively unbounded, and only two of them open with a summary

The brief allows long reference pages but asks each to open "with a short summary of key
points, detail below". Measured heights: `/docs/` 79,707 px at 1280 and 134,171 px at
390, `/changelog/` 17,076 and 32,122, `/security/` 15,314 and 30,500 — the three pages a
`fullPage` screenshot cannot capture at all (Chromium hangs; that is how they were
found). Contents today:

- `/security/` builds a contents list from `SECURITY.md`'s own headings
  (`security.astro:43`) — a summary of key points, in the sense the brief means; the
  page opens with the document's `h1` and that list (measured: the `h1` ends at 468 px at
  1280, 825 px at 390).
- `/docs/` has a lead and a contents list, but its lead is the internal text of RF6 — so
  removing the meta paragraph removes the only summary the page has, and the part that
  removes it must put a real one in its place, drawn from the documents' own facts.
- `/changelog/` has **no** summary of any kind: measured, the page goes from the `h1`
  straight into `v0.8.1`'s first entry.

### RF25 — **Exists** — the site's pins, which the redesign must not break

22 Go tests hold the site's facts (`TestWebsiteIsOutsideTheGoModule`,
`TestWebsiteCanonicalHostAppearsOnlyInSiteYaml`, `TestWebsiteNamesNoSecondDomain`,
`TestWebsiteInstallFactsMatchTheRepository`, `TestWebsiteSecurityPageIsByteIdenticalToSecurityMd`,
`TestCliReferenceNamesEveryCommandAndFlag`, and the rest), and the 179 hermetic tests
hold its structure. The redesign changes `Base.astro`, `base.css`, the palette, several
pages and their headings; every part of the plan names the gates it must keep green,
and the two that will move are named in the part that moves them (RF4, RF14, RF16).

### RF26 — **Open** — where the approved design lives

The brief's process is: screenshots → Claude Design → build → compare until they match.
The repository can hold the spec, this reconciliation, the plan, the "before" set and a
"after" set; it cannot hold a design tool's project file, and `D106`/RF20 keep the site
free of third-party assets. §9 (D120) asks the owner where the approved design lives and
what "match" is checked against, recommending: the design lives in the owner's design
tool, the repository keeps the two screenshot sets and a short `design.md` that records
the decisions the design fixed (type scale, spacing rhythm, accent usage), and the owner
does the final visual comparison.

## 3. Conflicts with the first line of work's decisions (`D90`–`D109`)

None of these is a contradiction of a decision's *substance*; three of them change how a
decision is **expressed in the tree**, and one of them is a fact the first line of work
did not know yet.

| Decision | What it says | What the redesign does to it |
| --- | --- | --- |
| **D90** (a second artifact: the site) | The site lives in `website/`, its own build, own language, own schedule | Untouched. The redesign adds no artifact and no deployment |
| **D91** (Firebase Hosting, website-only project) | Unchanged | Untouched |
| **D93** (Firebase Analytics behind a consent banner) | The one admitted third party, consent-gated | **Touched, in the tree:** `origins.mjs`'s `INLINE_SCRIPTS` currently holds exactly one sha256 — the banner controller — and RF15's copy script must not become a second inline entry by accident. If the file shape is chosen, the allow-list is untouched; if the inline shape is chosen, this Map and the deployed CSP comment grow by one entry, in the same commit |
| **D94** (install facts rendered as written) | The install page's facts come from `website/data/install.yaml` | **Kept, but its expression moves:** RF5's meta paragraph is the page *saying* it renders facts; D94's rule is the page *rendering* them. Removing the paragraph keeps D94 and strengthens it |
| **D104** (palette C: violet for structure and links, orange for warnings and cost) | The palette in force | **Contradicted by the brief, deliberately.** The brief says the accent must be sampled from the logo, and the logo is 198–204° while D104's violet is 258.3°. D104's *structure* (accent for structure and links, a second colour for warnings and cost) survives; its *hues* do not. §5 gives the new values, §9 (D110, D111) puts them to the owner, and the palette files, the manifest and the a11y pairs move together (RF16) |
| **D105** (motion, accessibility, performance; "static-first with no client JavaScript except deliberate islands, with a JavaScript budget in kilobytes for the animated terminal and the architecture diagram") | Deliberate islands, with a byte budget | **This decision already anticipates the redesign.** The 0-byte derivation in `README.md` and `budget.mjs` went *further* than D105 and became the thing the brief pushes against. The budget in force (100,000 B/page) needs no change (RF14); the two prose statements that say "no script today" are updated in the commit that ships one |
| **D106** (nothing third-party but D93's script) | Enforced by the origins gate | Untouched. Any new asset is first-party (RF20) |
| **D107** (a video slot, empty until filled) | `features.video: false` | Untouched: the brief asks for "images or diagrams", and a video is neither, so the slot stays empty |
| **D108** (four gates; the fourth is the owner's live checks) | Build, link check, drift tests, a11y/origins lint, plus live checks | **Extended, additively:** RF23's fold check and the "no internal text" gate are new gates, not a replacement. Whether the fold check is hermetic, browser-driven-in-repo or owner-run is D117 |

## 4. The CSP, the origins gate, and what the owner's ruling permits

The ruling of 2026-10-18 is quoted verbatim in §2 of the plan and applied here:

- `script-src 'self'` **stays**. No remote origin, no `unsafe-inline`, no `unsafe-eval`,
  nothing added to `firebase.json`'s policy.
- The origins gate **stays**: `origins.mjs` keeps refusing a script, style, font or form
  from any origin but the site's own, keeps refusing a remote inline script whose sha256
  is not in `INLINE_SCRIPTS`, and keeps `site.yaml`'s `allowed_hosts` as the only list of
  hosts for images and media.
- The **0-byte-JS rule and the byte budget numbers change deliberately, and their tests
  change in the same plan** — as directive, not permission: the plan's parts name every
  prose statement and every test that move with them (RF14), rather than letting a script
  land quietly under a loose ceiling.

The plan therefore asks for **no workflow change and no hosting-policy change**: an
external first-party script is admissible under the policy as it stands. Nothing under
`.github/workflows/` is touched by this line of work, and if a part ever needed a
workflow, the file would be proposed under `docs/pending-workflows/` for the owner to
copy rather than written in place.

## 5. The accent: the logo's hue, the derived palette, and its contrast

### 5.1 What was sampled, and how

`brand/fugaro-logo.png`, 1024 × 1024, read pixel by pixel:

1. fully transparent and near-black pixels (alpha < 250, value < 0.05) are dropped —
   the logo's ground is not its colour;
2. the remaining pixels are converted to HSV;
3. for each saturation threshold, the hue is averaged **as a circular mean**
   (sin/cos sum, which is the only correct average for an angle, and which stops a few
   pixels at 359° from dragging the mean to 180°).

The result, and the modal 5°-bins over pixels with saturation ≥ 0.7:

| saturation ≥ | pixels | mean hue | mean saturation |
| --- | --- | --- | --- |
| 0.5 | 82,803 | **198.5°** | 0.837 |
| 0.6 | 77,195 | **199.6°** | 0.858 |
| 0.7 | 68,128 | **201.4°** | 0.885 |
| 0.8 | 52,756 | **204.2°** | 0.923 |

| bin | share |
| --- | --- |
| 200–204° | 23.70 % |
| 195–199° | 17.65 % |
| 205–209° | 13.30 % |
| 185–189° | 9.98 % |
| 210–214° | 9.93 % |
| 190–194° | 9.62 % |

The logo's colour is a saturated cyan-blue centred on **198–202°**, its mass sitting
between 185° and 214°. The sampler was a throwaway program run from this branch's
scratch directory (Go, standard library, `image/png`); it is deliberately **not**
committed, because a `package main` under `docs/` would become a second package in this
module, and the algorithm above is the whole of it.

### 5.2 The derived palette

One accent hue (198°, the sampled mean), one derivation, and the same token names as
`palette-split.css` so that every page and every test that reads a token keeps working:

| token | value | hue | what it is |
| --- | --- | --- | --- |
| `--accent` | `#17A8E6` | 198.0° | the logo's own hue, electric: structure, links, focus ring, filled controls |
| `--accent-on-dark` | `#6FD8F5` | 193.0° | the accent's light tint for text on dark grounds (body-size text and links) |
| `--accent-ink` | `#06121A` | 204° | the label on an accent-filled control (button text on `--accent`) |
| `--accent-on-light` | `#0B6FA8` | 201.8° | the accent as text on a light ground, if the light reading of D119 is taken |
| `--warn` | `#F5A524` | 37.0° | warnings and cost — D104's second role kept, moved to a hue that sits beside cyan rather than against it |
| `--ground` | `#0A0E16` | 220° | the page ground, a very dark blue-black taken from the accent's hue family |
| `--surface` | `#121A26` | 216° | raised surfaces (cards, code blocks) |
| `--text` | `#E6EEF5` | 208° | body text |
| `--text-muted` | `#9FB2C4` | 209° | secondary text |
| `--border` | `#24303F` | 213° | decorative rules and card edges |
| `--gradient-from` | `#08131F` | 211° | the soft glow at the top of a hero |
| `--gradient-to` | `#0A2233` | 205° | the same glow at its other end |
| `--light-ground` | `#FFFFFF` | — | only if D119's second reading is taken |
| `--light-surface` | `#F1F5F9` | — | idem |
| `--light-text` | `#0B1220` | — | idem |

The total shift is deliberate: `--ground` moves from `#0B0B12` (a neutral violet-black)
to `#0A0E16` (a blue-black four degrees off the logo's family), so the whole page reads
as the logo's colour rather than as a violet site with a cyan picture on it.

### 5.3 WCAG contrast, computed

Ratios computed from the sRGB relative-luminance definition, over the values above:

| pair | ratio | needs |
| --- | --- | --- |
| `--text` on `--ground` | **16.47** | 4.5 ✓ |
| `--text` on `--surface` | **14.91** | 4.5 ✓ |
| `--text-muted` on `--ground` | **8.87** | 4.5 ✓ |
| `--text-muted` on `--surface` | **8.02** | 4.5 ✓ |
| `--accent-on-dark` on `--ground` | **11.79** | 4.5 ✓ (body text, links) |
| `--accent-on-dark` on `--surface` | **10.67** | 4.5 ✓ |
| `--accent-on-dark` on `--gradient-from` | **11.41** | 4.5 ✓ |
| `--accent-on-dark` on `--gradient-to` | **9.94** | 4.5 ✓ |
| `--text` on `--gradient-from` | **15.95** | 4.5 ✓ |
| `--text` on `--gradient-to` | **13.89** | 4.5 ✓ |
| `--accent` on `--ground` | **7.16** | 3 ✓ (focus ring, UI, large text) |
| `--accent` on `--surface` | **6.48** | 3 ✓ |
| `--accent-ink` on `--accent` | **7.01** | 4.5 ✓ (button label) |
| `--warn` on `--ground` | **9.46** | 4.5 ✓ |
| `--warn` on `--surface` | **8.56** | 4.5 ✓ |
| `--light-text` on `--light-ground` | **18.72** | 4.5 ✓ |
| `--light-text` on `--light-surface` | **17.09** | 4.5 ✓ |
| `--accent-on-light` on `--light-ground` | **5.45** | 4.5 ✓ |
| `--accent-on-light` on `--light-surface` | **4.97** | 4.5 ✓ |
| `--accent` on a white ground | **2.70** | ✗ — this is *why* a light ground needs `--accent-on-light` |
| `--border` on `--ground` | **1.44** | ✗ 3:1 — admissible as a **decorative** rule (a divider is not a control's boundary); if a border ever marks a control's extent, it must be raised to ≥ 3:1 |

For comparison, the palette in force: `--accent-on-dark #C4B5FD` on `--ground` 10.62,
`--accent #8B5CF6` on `--ground` 4.63 (it clears the 4.5 line the site's `BASE_PAIRS`
uses for body text by a tenth of a point, which is why the accent is only ever used as
text at large sizes and as a focus ring — the distinction D105's pairs encode),
`--warn #F97316` on `--ground` 7.00.

**Everything the site asserts today keeps passing under the derived palette**, and the
two pairs that are close to a boundary are called out above rather than left for the gate
to find.

## 6. What the current site measures

Taken on 2026-10-18 on this branch's base (`85758e1`), Node v22.22.0, Playwright 1.49.1
with its own Chromium, `prefers-reduced-motion: reduce`, device scale factor 1. The capture script writes the same measurements to JSON when it is
run (it takes the output path as its first argument); the images are in
[current/](current/).

| route | height @1280 | height @390 | words | `<pre>` | copy buttons | inline JS |
| --- | --- | --- | --- | --- | --- | --- |
| `/` | 1,072 | 1,952 | 89 | 3 | 0 | 0 |
| `/how-it-works/` | 1,293 | 1,449 | 107 | 0 | 0 | 0 |
| `/getting-started/` | 3,372 | 4,361 | 431 | 10 | 0 | 0 |
| `/caps-and-costs/` | 2,282 | 3,313 | 487 | 0 | 0 | 0 |
| `/console/` | 1,042 | 1,524 | 200 | 0 | 0 | 0 |
| `/docs/` | **79,707** | **134,171** | **18,282** | 10 | 0 | 0 |
| `/security/` | 15,314 | 30,500 | 6,240 | 0 | 0 | 0 |
| `/changelog/` | 17,076 | 32,122 | 6,191 | 0 | 0 | 0 |
| `/faq/` | 2,073 | 3,201 | 493 | 0 | 0 | 0 |
| `/community/` | 1,434 | 2,087 | 268 | 0 | 0 | 0 |
| `/privacy/` | 1,704 | 2,574 | 277 | 0 | 0 | 0 |

Header and fold, at 390 × 844 (a small phone) and 1280 × 900:

| | header | nav rows | `h1` bottom @1280 | `h1` bottom @390 |
| --- | --- | --- | --- | --- |
| every route | 101 px @1280 / **177 px @390** | 2 / **3** | 200 px (home 366, security 468, privacy 248) | 246 px (home **893**, security 825, faq/console 283, privacy 308) |

The homepage's `h1` — "Fleeting cloud workers for coding agents: one task, one
container, one PR, then gone." — ends at **893 px on a 390 × 844 screen**: the site's
own headline is one line below the fold on the phone it is being read on. Every other
page's `h1` is inside the first screen, and Getting Started's *first screen is the
internal paragraph of RF5* rather than its first command.

Also measured: no page scrolls horizontally at 390 (`scrollWidth` 390 on all eleven), so
the brief's "layout renders fine today; keep it so" is confirmed as a fact to preserve,
not a hope.

The build itself: `npm run build` → 11 pages; `npm run check` → **179 pass, 0 fail,
72.5 s**; `dist/` 695,920 bytes over every file. The Go suite is in §7.

## 7. What could not be verified, and by whom

**RU1 — the visual comparison.** No agent in this run can view an image. The screenshots
exist, their paths and sizes are recorded, and **whether the finished site matches the
approved design is the owner's step**. Nothing in this document claims to have looked.

**RU2 — the deployed site.** `D92`'s live checks (`69a`, `69b`, and `W2.3`'s `69c`) have
not been run: there is no deployment in this run, no cloud action of any kind, and none
is proposed. Everything here is measured against a local build of `website/dist`.

**RU3 — the accent on a real screen.** Contrast is computed, not seen. The palette in §5
is a derivation from sampled pixels; whether it *looks* like the logo is the owner's
judgement, and the two candidate decisions in §9 are written so that the hue can be
nudged without redoing anything else.

**RU4 — Lighthouse.** `D105`'s targets (performance ≥ 95, accessibility 100, best
practices 100, SEO 100) are an owner-run check by the repository's own statement
(`website/README.md:96-98`). They were not run here, and adding JavaScript moves them;
the plan's last part asks the owner to run them after the copy script and the homepage
animation land.

**RU5 — the mobile header on a real device.** The 177 px / three-row measurement is
Chromium at 390 × 844. A real phone with a different font scale or a browser UI that
changes the visual viewport can and will differ; D117 asks whether the acceptance check
is that measurement or a device the owner holds.

## 8. What this document does not decide

It does not decide the design. It does not choose the type scale, the spacing rhythm, the
new page copy, the exact menu groups, or the shape of the How It Works picture beyond the
constraints in RF11, RF12 and RF20. It records what is true today, what the brief asks
for, what the repository's rules permit, and which of the remaining questions are the
owner's — §9.

## 9. Owner decisions

Numbering: the first line of work used `D90`–`D109` and nothing in this repository writes
`D110` or above. These are therefore **`D110`–`D121`**, and this document is where they
are put; if any other document is found to have written one of these numbers, the owner
allocates and the plan follows (§8 of the plan). Each is written with the recommendation
first, in the shape `../reconciliation.md` §4 uses; the same list appears in the pull
request body, because a decision this branch makes silently would be the one kind of
change nobody can review.

| # | question | recommendation | why | alternatives (cost) |
| --- | --- | --- | --- | --- |
| **D110** | **The accent and the palette** (§5) | Take the logo's own hue: accent `#17A8E6` (198°), with `--accent-on-dark #6FD8F5`, `--accent-ink #06121A`, and the ground moved to `#0A0E16` — the token set in §5.2, which passes every pair the site asserts | The brief says to sample the logo, and 198–204° is what the logo is; the site's own `--accent-on-dark` at 11.79:1 keeps body text and links comfortable | (a) keep `--accent #8B5CF6` and use the logo's hue only for the logo: cheapest, but the brief's complaint stands; (b) take the deep end of the sample (204°, `#0E7FC4`-class) for a calmer site: less electric, closer to the brief's "high-energy" only in name |
| **D111** | **What happens to D104's warning colour** | Keep D104's *role* (a second colour for warnings and cost) and move it to `#F5A524` (37°), which reads as attention beside a cyan accent | Two hues 160° apart fight; D104's orange at 24.6° against a cyan accent is the loudest possible pair | (a) keep `#F97316` exactly: it still passes 6.89:1 on the new ground; (b) drop the second colour entirely: loses the caps-and-costs page's "attention" signal |
| **D112** | **The navigation's shape** | Three top-level sections with dropdowns — *Product* (How it works, Caps and costs, Getting started), *Reference* (Docs, Console, Changelog), *Project* (Security, FAQ, Community, Privacy) — with Home as the logo, and a drawer on mobile | Ten flat items is what wraps to three rows; three groups of three or four is what fits one; the split follows the fold of the site's own contents, and Privacy belongs with the project's documents rather than under nothing | (a) five groups: more top-level than a 390 px row can hold; (b) two groups: the dropdowns get long and the menu stops being scannable; (c) leave the items flat and only shrink the type: the brief asks for a two-level menu |
| **D113** | **How the JavaScript is delivered** (RF15) | **A first-party file** (`website/public/scripts/*.js`), no inline script, no new `INLINE_SCRIPTS` entry, no CSP edit | The CSP and the allow-list stay exactly as the owner's ruling keeps them; a file is counted by `budget.mjs` like any other asset, and it is cacheable across pages | (a) an inline script per page with a sha256 in `INLINE_SCRIPTS` and in the deployed policy: keeps the request count at zero, costs one hash per page and a policy edit; (b) both (inline for the fold-critical bits, file for the rest): two mechanisms to keep honest |
| **D114** | **What the homepage run animation may show** (RF10, RF11) | Lines the repository already writes — the numbered steps of `README.md`'s `## How it works`, typed in order, ending at "Push and open the pull request" — with the page stating once, in the caption, that it is the documented sequence and not a recording | It is the only version that cannot go stale or lie: the captions are held to the file at build time exactly as the diagram's are, and a reader sees the real shape of a run | (a) a scripted illustration with an invented job id and elapsed time, labelled as an illustration: more "sizzle", one page that shows facts that are not facts; (b) no animation beyond the existing single command: cheapest, leaves the brief's item 5 half-answered |
| **D115** | **How It Works: five stages or four steps** (RF12) | Keep the five quoted stages (they are the repository's own words, and `cache` and the security boundary are real stages) and rebuild the **picture** as a vertical flow with one line per stage | The brief asks for the flow's *shape* ("one line per step at most"); the five stages are what the repository actually claims, and collapsing them would make the page say something no document says | (a) four steps exactly as the brief lists them: a new claim, and each step's line has to come from somewhere; (b) five stages as four steps by merging `cache` into `cloud job`: one merge, one caption quoted from two halves of the README rather than one |
| **D116** | **The "no internal text" rule** (RF5–RF8) | A new hermetic gate that refuses a small, explicit list of provenance phrases — "rendered from `website/data/…`", "Nothing here is typed into this page", "as it is generated", "generated rather than written", "rather than written here", "points at those documents rather than repeating them" — on every built page, and the rule stated in the plan as: **a page must not explain why its own text exists** | A phrase list is checkable, mutation-provable, and cannot misfire on `privacy.astro` or on the docs hub's legitimate citations of the documents it links | (a) a rule over "names any repository file": would fail on pages that legitimately link `SECURITY.md`, `docs/` pages and `README.md`; (b) no gate, a review habit: the brief's "Done when" line asks for the gate |
| **D117** | **How "above the fold" is checked** (RF23) | A hermetic **word ceiling per page** (recommendation: 150 words of visible text for every page except the three reference pages, which get 400 for their summary and are allowed their detail below), plus **one owner-run live check** for the fold itself (the register's next free number, §5 of the plan) | Hermetic gates cannot see a viewport, and this repository has deliberately no browser in CI; the word ceiling catches the failure the brief cares about in a way that can be mutation-proven, and the fold is what the owner's eyes are for | (a) add the repository's first browser-driven test (Playwright in `website/test/`): a real fold assertion, at the cost of the site's browser-free gate posture and a new dependency; (b) word ceiling only: cheap, leaves "above the fold" as a promise nobody checks |
| **D118** | **The word ceiling as a number** | 150 / 400 as above | The pages that read well today are already inside it (89–493 words); the ones that need the brief's treatment are the docs hub (18,282), changelog (6,191) and security (6,240), and those are reference pages the brief explicitly allows to stay long | (a) 200/600: more room, less punch; (b) per-page numbers in a table: more precise, one more table to maintain |
| **D119** | **Is "light and dark" a light theme?** (RF19) | No: take the brief's words as "the accent's contrast against light *and* dark grounds", which §5.3 computes | The brief's own page list, its "accent colour" item and its mobile item are all about the dark site; a light theme is a second design, not a contrast check | (a) yes, a light theme: a second palette, a `prefers-color-scheme` branch and a second set of a11y pairs — a separate line of work, after this one |
| **D120** | **Where the approved design lives** (RF26) | In the owner's design tool; the repository keeps the spec, this reconciliation, the plan, the "before" set, an "after" set, and a short `redesign/design.md` recording the numbers the design fixed (type scale, spacing, accent usage, the menu groups) | The repository's rules keep third-party assets out of the site and a design project file is not a document; the numbers the design fixes do belong in the tree, because every later change to a page has to keep them | (a) keep the design only in the tool: the "match" check has no record in the tree; (b) commit the design's exported images: 8 MB of images already arrived with the "before" set, and more binaries in `docs/` is the wrong direction |
| **D121** | **Sequencing against the first line of work** | Redesign first, then the remaining `W2`/`W3` waves (the live checks, the deploy, analytics behind the banner) | The redesign changes `Base.astro`, `base.css`, the palette and several pages — the same files `W3` will deploy; doing it first means deploying the finished look once instead of twice | (a) finish `W3` first: the site goes live in its current look and the redesign follows as a second deploy; (b) interleave: two lines editing the same files |
