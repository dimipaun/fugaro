# Website redesign — reconciliation

Status: **plan only; nothing of the redesign is implemented.** Owner: **@Dimi**. Date:
**2026-10-18**. Source: [spec.md](spec.md) — the brief, committed alone in this branch's
first commit, before anything that answers it. The plan this document produces:
[implementation-plan.md](implementation-plan.md). The "before" pictures are regenerated
by [capture-screenshots.mjs](capture-screenshots.mjs) into [current/](current/) and are
not committed; that directory's README has the command.

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
**179 tests out of 179 in 72.5 s**. The Go side (`internal/cli`, the site's pins) is run
separately, and §6 records its result beside the site's.

Three documents in this repository say the opposite, in the present tense:
`../README.md:3` and `:5` ("**Status: plan only; nothing is implemented**",
"**No site code exists**: there is no `website/` directory…"), `../reconciliation.md:3`
and `../implementation-plan.md:3`. They are wrong, and finding **RF1** is that.

Everything below is measured against that built site, not against the plan for it.

## 1. How to read this

Five labels, and each finding carries exactly one:

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
**`D180` onward**; §9 says why that number and not another.

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
is missing; 179 hermetic gate tests under `website/test/`; 25 Go pins — eighteen
`TestWebsite*` in `internal/cli` and seven `TestCliReference*` across `internal/cli` and
`internal/clidoc`. The redesign's job is not to establish any of this.

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

### RF7 — **Gap** — the same class on FAQ, Community and Caps and costs

`website/src/pages/faq.astro:397` — "Six answers, each one taken from a document in this
repository rather than written here". `website/src/pages/community.astro:224` — "This
page points at those documents rather than repeating them".
`website/src/pages/caps-and-costs.astro:20-22` — the page's lead ends "each rendered from
the repository's own output rather than described here"; and the first paragraph under
its `## The caps` heading, in `website/src/components/CapsCosts.astro:192-194`, is
"Every command below is rendered from `website/content/cli-reference.md`, the reference
generated from the command tree …". Both are the same class as RF5 and RF6.

### RF8 — **Rule** — no gate can see RF5–RF7, and the rule needs a definition first

**What the rule does not touch: the content pipeline's generated-file banner.** Every file
under `website/content/` opens with `<!-- generated by …; do not edit -->` and, where it
has one, `<!-- source: … -->` — the first plan's Global Constraint 6 ("Every generated file
names its source in a header comment"), parsed by `content.ts`'s `GENERATED_HEADER` and
`SOURCE_HEADER` (`content.ts:108-109`). The pipeline **drops** the banner from the body it
renders and keeps it as two facts, `generator` and `source`. The banner is never on a page,
so the brief's rule leaves it exactly as it is. What the rule removes is page prose that
re-renders those facts as an explanation — `docs.astro:259-262` prints `reference.generator`
inside RF6's epilogue. A **link** built from `source` (the security page's lead links
`SECURITY.md`) is a citation, not an explanation, and stays.



**No gate in `website/test/` has an opinion about a page's own provenance.** Searching
the seventeen test files for the sentences of RF5–RF7 ("Nothing here is typed", "rather
than written here") finds no assertion: those phrases appear in the tests only as
comments and as `readme.mjs:40`'s `GENERATED_HEADER` regex, which reads the
`<!-- generated by … -->` banner of a markdown **file** rather than a rendered page. The
gates check facts, links, origins, contrast, layout and budgets. So "Done when: no internal/meta text" needs a new test — and
that test needs a definition, because several pages legitimately name repository files:
`privacy.astro` describes the site's own posture, `security.astro:43` builds its contents
from `SECURITY.md`'s headings, and the docs hub cites the document each guide lives in.
The distinction the test can enforce is narrower and checkable: **a page must not tell a
reader why its own text exists.** §9 (D186) puts the precise rule to the owner; part **P4** of the
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
holds the generated copy of `SECURITY.md` to the document byte for byte — the page itself
is not that copy, and since the brief of 2026-10-19 `website/test/security.mjs` holds the
page to the document's own words instead (see the Global Constraint 12 row in §3).

A terminal transcript is the easiest place in the site to break that rule: a job
identifier, an elapsed time, a branch name, a run URL or a pull-request number shown in
the animation is a fact about a run that did not happen. The same holds for a fake
"spending" figure. So the run animation can only show **lines the repository already
writes** — `README.md`'s `## How it works` numbered steps are the obvious source, exactly
as the five diagram captions are today — or it must be labelled, on the page, as an
illustration rather than a recording. §9 (D184) is this decision.

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
rule. The brief's "agent runs" does have a source the diagram does not use today:
`README.md`'s step 3, "**Loop:** Claude Code implements, writes tests, runs the build and
tests through a recording wrapper, reviews and fixes …". The component's own check
(`ArchitectureDiagram.astro:108-117`) compares a caption as a **substring** of its source
with whitespace and `` ` ``/`*` removed, so a short caption such as "Push and open the pull
request" is admissible — which is how "one line per step at most" can hold for the quoted
captions (step 4's sentence, today's fifth caption, is 38 words). §9 (D185) is this
decision; the recommendation is the brief's four steps, each captioned by a quoted line,
with `cache` and `security boundary` kept on the page as annotations of the job step.
Keeping the five stages as they are would leave out the one step the brief names that the
diagram lacks.

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

Two things follow:

- **The loose budget admits a script as it stands, which is not the same as being
  right.** `website/README.md:89-90` sets **JavaScript per page: 100,000 bytes**,
  deliberately loose by the owner's ruling of 2026-10-17 ("about three times what the build
  of 2026-10-17 needs, and a later optimisation pass tightens them"), and says the
  allowance "leaves room for W1.12's consent banner and the logo work without a budget
  edit". A copy controller is a kilobyte or two against a hundred; the page-weight ceiling
  (662,766 B) and the site total (1,454,826 B; the whole of `dist/` was 695,920 B on
  2026-10-18, re-measured for this review) leave the same room. The owner's ruling of
  2026-10-18 says the numbers **change deliberately**, so whether they are re-derived from
  the build that ships the script or kept is **D192**, not a default.

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
to `INLINE_SCRIPTS`** and, as `'sha256-…'`, to `firebase.json`'s `script-src` value (the
policy is a JSON string with no comment, and an inline script without its hash there is
blocked by the browser; W3.2 does the same for the banner's digest). The plan recommends
the file — one
mechanism, no hash to maintain per page, and it keeps the CSP and the allow-list exactly
as they are — and §9 (D183) records it as the owner's call because it is the CSP's shape
that is being chosen.

### RF16 — **Rule** — the palette is pinned in four places at once

`website/src/styles/palette-split.css` is the palette in force (D104's option C): it is the
one file `website/src/styles/tokens.css` imports, and `website/test/a11y.mjs` asserts that
import against `PALETTE_IN_FORCE`. Its sibling files `palette-violet.css` and
`palette-orange.css` are the other two candidates, and `a11y.mjs`'s `PALETTE_FILES` lints
all three: "the three palettes are one vocabulary in three value sets" fails if one palette
declares a token the others do not, and `PAIRS` is computed for **every** palette file, not
only the one in force. `website/test/brand.mjs` holds
`website/public/site.webmanifest:6-7`'s `theme_color`/`background_color` equal to the
`--ground` of the imported palette (both `#0B0B12` today).

So a new palette **cannot** add a token (`--accent-ink`, a light-ground set) to one file
alone. It is either a fourth file with the same ten tokens, added to `PALETTE_FILES` and
made the palette in force by the one `tokens.css` import and the `PALETTE_IN_FORCE` line
(the test's own failure message calls that "this one line"), or new values written into
`palette-split.css`, which would then no longer be D104's option C. The plan takes the
first: it keeps the record of D104's choice and makes the revert one line. Besides the new
file, the change touches the manifest's two colours (because `--ground` moves in §5's
derivation) and nothing else; `brand/fugaro-logo.png` is the source of the hue rather than
a file to re-colour.

### RF17 — **Rule** — reduced motion is not optional, and it is asserted

`Base.astro:193` carries the global neutralisation (`@media (prefers-reduced-motion:
reduce)`); `a11y.mjs` asserts a reduced-motion path for every animation.
`ArchitectureDiagram.astro:270-271` also guards its scroll-driven animation in a feature
query and a preference query (`@supports (animation-timeline: view())` around
`prefers-reduced-motion: no-preference`); `Terminal.astro` has **no** guard of its own and
relies on the layout's global rule. The homepage run animation inherits the global rule:
under `reduce`, the page shows the finished transcript, still, in full.

### RF18 — **Wrong (confirmed by measurement)** — the accent does not match the logo

Sampled from `brand/fugaro-logo.png` (1024 × 1024, RGBA; 115,370 pixels with alpha ≥ 250,
of which 107,437 are not near-black) by the committed
[sample-logo-hue.py](sample-logo-hue.py): the circular mean hue is **198.4°** at
saturation ≥ 0.5, **199.5°** at ≥ 0.6, **201.4°** at ≥ 0.7 and **204.2°** at ≥ 0.8; the
largest 5° bins are **200–204 (26.8 %)**, 195–199 (19.6 %), 205–209 (15.3 %). The logo's
hue is a cyan-blue around **198–204°**.

The palette in force: `--accent: #8B5CF6` (hue **258.3°**), `--accent-on-dark: #C4B5FD`
(hue **252.5°**), `--warn: #F97316` (hue **24.6°**). **The accent is about 60° away from
the logo's own hue and the warning colour about 174° away from it.** The brief's
complaint is not a matter of taste; it is a measurement. §5 records the derived palette
and its contrast matrix.

### RF19 — **Exists** — the site has one, dark, appearance, and the brief does not ask for a second

The site has **one** appearance: every palette is a dark-ground palette and `base.css`
carries no `prefers-color-scheme` block. The brief says nothing about a light theme or
about contrast (an earlier draft of this document quoted a "light and dark" line that is
not in [spec.md](spec.md); it was wrong and is removed). The new accent is held to the
contrast pairs the site already asserts (§5.3), and no light theme is planned.

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

### RF23 — **Gap** — "above the fold" and "very few words" are, today, unwritten and unmeasured

No gate measures words or viewport position. `budget.mjs` measures bytes; `a11y.mjs`
measures structure; both read the **built HTML and CSS with no browser**, and the site's
gates are deliberately browser-free (`website/README.md:96-98`: "there is no CI on pull
requests and the tests here are hermetic, so a browser-driven audit is not one of
them"). So the brief's central promise — "the gist above the fold on every page" — cannot
be a hermetic gate in the shape the others have. It can be (a) a word ceiling per page,
enforced hermetically, plus (b) a fold check, which needs a browser and therefore is
either a new owner-run live check or the first browser-driven test in this repository.
§9 (D187) is this decision, and part **P4** of the plan writes the word ceiling either way.

Measured today, the word count of the whole visible text of each page is: home 89,
how-it-works 107, getting-started 431, caps-and-costs 487, console 200, **docs 18,282**,
**security 6,240**, **changelog 6,191**, faq 493, community 268, privacy 277.

### RF24 — **Gap** — three pages are effectively unbounded, and none opens with a summary of key points

The brief allows long reference pages but asks each to open "with a short summary of key
points, detail below". Measured heights: `/docs/` 79,707 px at 1280 and 134,171 px at
390, `/changelog/` 17,076 and 32,122, `/security/` 15,314 and 30,500 — the three pages a
`fullPage` screenshot cannot capture at all (Chromium hangs; that is how they were
found). Contents today:

- `/security/` opens with two links and a contents list built from `SECURITY.md`'s own
  headings (`security.astro`, the `data-security-toc` nav) — measured, the `h1` ends at
  468 px at 1280 and 825 px at 390. A list of headings is not "a short summary of key
  points", and the brief names this very page as its example, so `/security/` needs one.
  The page is held to `SECURITY.md` by the first plan's Global Constraint 12 ("Security
  claims are never paraphrased"), whose shape is a byte-for-byte page under
  `TestWebsiteSecurityPageIsByteIdenticalToSecurityMd`, so a summary written on the page
  would be the paraphrase that rule forbids. **As shipped** (2026-10-19), that Go pin
  reads the generated copy `website/content/security.md` and never a page, and the page
  itself — the brief's three things — is held to the document's own words by
  `website/test/security.mjs`; the constraint table in §3 records the departure.
- `/docs/` has a lead and a contents list, but its lead is the internal text of RF6 — so
  removing the meta paragraph removes the only summary the page has, and the part that
  removes it must put a real one in its place, drawn from the documents' own facts.
- `/changelog/` has **no** summary of any kind: measured, the page goes from the `h1`
  straight into `v0.8.1`'s first entry — by rule: `website/test/changelog-page.mjs` fails
  on any sentence that is not in a release file ("the page adds no sentence of its own").

All three summaries therefore collide with the first plan's Global Constraint 6 ("A page
never restates a document; it renders it or links it") and, for two of them, with a test
that enforces it. Where each summary's words come from is §9's **D189**.

### RF25 — **Exists** — the site's pins, which the redesign must not break

25 Go tests hold the site's facts — eighteen named `TestWebsite*` and seven named
`TestCliReference*` (`TestWebsiteIsOutsideTheGoModule`,
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
free of third-party assets. §9 (D190) asks the owner where the approved design lives and
what "match" is checked against, recommending: the design lives in the owner's design
tool, the repository keeps the two screenshot sets and a short `design.md` that records
the decisions the design fixed (type scale, spacing rhythm, accent usage), and the owner
does the final visual comparison.

## 3. Conflicts with the first line of work's decisions (`D90`–`D109`)

D104's hues are contradicted by the brief deliberately; the other rows change how a
decision is **expressed in the tree** or leave it untouched. Two of the first plan's
Global Constraints collide with the brief as well, and they are the last two rows.

| Decision | What it says | What the redesign does to it |
| --- | --- | --- |
| **D90** (a second artifact: the site) | The site lives in `website/`, its own build, own language, own schedule | Untouched. The redesign adds no artifact and no deployment |
| **D91** (Firebase Hosting, website-only project) | Unchanged | Untouched |
| **D93** (Firebase Analytics behind a consent banner) | The one admitted third party, consent-gated | **Touched, in the tree:** `origins.mjs`'s `INLINE_SCRIPTS` currently holds exactly one sha256 — the banner controller — and RF15's copy script must not become a second inline entry by accident. If the file shape is chosen, the allow-list is untouched; if the inline shape is chosen, this Map and `firebase.json`'s `script-src` value each grow by one `'sha256-…'`, in the same commit |
| **D94** (install facts rendered as written) | The install page's facts come from `website/data/install.yaml` | **Kept, but its expression moves:** RF5's meta paragraph is the page *saying* it renders facts; D94's rule is the page *rendering* them. Removing the paragraph keeps D94 and strengthens it |
| **D104** (palette C: violet for structure and links, orange for warnings and cost) | The palette in force | **Contradicted by the brief, deliberately.** The brief says the accent must be sampled from the logo, and the logo is 198–204° while D104's violet is 258.3°. D104's *structure* (accent for structure and links, a second colour for warnings and cost) survives; its *hues* do not. §5 gives the new values, §9 (D180, D181) puts them to the owner, and the palette files, the manifest and the a11y pairs move together (RF16) |
| **D105** (motion, accessibility, performance; "static-first with no client JavaScript except deliberate islands, with a JavaScript budget in kilobytes for the animated terminal and the architecture diagram") | Deliberate islands, with a byte budget | **This decision already anticipates the redesign.** The 0-byte derivation in `README.md` and `budget.mjs` went *further* than D105 and became the thing the brief pushes against. The loose ceiling in force (100,000 B/page) admits a copy script as it stands (RF14); whether the numbers are re-derived from the build that ships it, as the owner's ruling of 2026-10-18 says they change, is **D192** |
| **D106** (nothing third-party but D93's script) | Enforced by the origins gate | Untouched. Any new asset is first-party (RF20) |
| **D107** (a video slot, empty until filled) | `features.video: false` | Untouched: the brief asks for "images or diagrams", and a video is neither, so the slot stays empty |
| **D108** (four gates; the fourth is the owner's live checks) | Build, link check, drift tests, a11y/origins lint, plus live checks | **Extended, additively:** RF23's fold check and the "no internal text" gate are new gates, not a replacement. Whether the fold check is hermetic, browser-driven-in-repo or owner-run is D187 |
| **Global Constraint 6** of the first plan ("A page never restates a document; it renders it or links it") | Enforced by construction and by `changelog-page.mjs` | **Collides** with the brief's "each opens with a short summary of key points" on `/security/`, `/changelog/` and `/docs/` (RF24): a summary written on the page is a restatement. **D189** |
| **Global Constraint 12** ("Security claims are never paraphrased"; the page is `SECURITY.md` byte for byte below the title) | the generated copy by `TestWebsiteSecurityPageIsByteIdenticalToSecurityMd`; the page by `website/test/security.mjs` | **Collides** with a `/security/` summary written on the page; D189's recommendation keeps the constraint by putting the summary in `SECURITY.md` itself. **As shipped (2026-10-19) the second half of its shape departed**: the Go pin has always read the generated copy `website/content/security.md` rather than a page, and the page — now the brief's three things, with the document's cross-references resolved to the section that answers them (the page's own anchor where the page renders that section, a link into the repository otherwise) and a reference naming no heading rendered as its own text — is held by `website/test/security.mjs`. The constraint's substance is kept: no security claim on the page is a paraphrase, because every word of it is the document's. The departure is recorded in the plan's P4 note |
| **W1.4's five stage names** (the first spec's `local dispatch`, `cloud job`, `cache`, `security boundary`, `PR or artifact collected back`) | `home.mjs`: "the spec's five stage names and no sixth" | **Changed** if D185 takes the brief's four steps: the test's five-names assertion is rewritten in P8 |

## 4. The CSP, the origins gate, and what the owner's ruling permits

The ruling of 2026-10-18 is quoted in the plan's front matter, above its §1,
and applied here:

- `script-src 'self'` **stays**. No remote origin, no `unsafe-inline`, no `unsafe-eval`,
  nothing added to `firebase.json`'s policy.
- The origins gate **stays**: `origins.mjs` keeps refusing a script, style, font or form
  from any origin but the site's own, keeps refusing a remote inline script whose sha256
  is not in `INLINE_SCRIPTS`, and keeps `site.yaml`'s `allowed_hosts` as the only list of
  hosts for images and media.
- The **0-byte-JS rule and the byte budget numbers change deliberately, and their tests
  change in the same plan** — as directive, not permission: the plan's parts name every
  prose statement and every test that move with them (RF14), and D192 asks whether the
  numbers are re-derived from the build that ships the script (recommended) or left at
  today's loose ceilings, rather than letting a script land quietly under one.

Under D183's recommendation the plan asks for **no workflow change and no hosting-policy
change**: an external first-party script is admissible under the policy as it stands (the
inline alternative edits `firebase.json`'s `script-src`, RF15). Nothing under
`.github/workflows/` is touched by this line of work, and if a part ever needed a
workflow, the file would be proposed under `docs/pending-workflows/` for the owner to
copy rather than written in place.

## 5. The accent: the logo's hue, the derived palette, and its contrast

### 5.1 What was sampled, and how

`brand/fugaro-logo.png`, 1024 × 1024 RGBA, read pixel by pixel by
[sample-logo-hue.py](sample-logo-hue.py) (Pillow only, like `scripts/gen-brand-icons.py`;
run `python3 -I docs/website/redesign/sample-logo-hue.py brand/fugaro-logo.png` from the
repository root):

1. pixels with alpha below 250 and near-black pixels (HSV value below 0.05) are dropped —
   the logo's ground is not its colour; 107,437 pixels remain;
2. the remaining pixels are converted to HSV;
3. for each saturation threshold, the hue is averaged **as a circular mean**
   (sin/cos sum, which is the only correct average for an angle, and which stops a few
   pixels at 359° from dragging the mean to 180°). The script prints the plain mean and
   the saturation-weighted one; the plain mean is the figure this document uses.

| saturation ≥ | pixels | mean hue | weighted by saturation | mean saturation |
| --- | --- | --- | --- | --- |
| 0.5 | 83,028 | **198.4°** | 199.7° | 0.832 |
| 0.6 | 77,323 | **199.5°** | 200.5° | 0.853 |
| 0.7 | 68,005 | **201.4°** | 201.9° | 0.880 |
| 0.8 | 51,926 | **204.2°** | 204.4° | 0.918 |

The largest 5° bins over the pixels with saturation ≥ 0.7:

| bin | share |
| --- | --- |
| 200–204° | 26.78 % |
| 195–199° | 19.63 % |
| 205–209° | 15.31 % |
| 210–214° | 9.59 % |
| 190–194° | 8.05 % |
| 185–189° | 7.76 % |

(The bins move by a point or two with the HSV implementation, because many pixels sit
exactly on 200°; the means do not.) The logo's colour is a saturated cyan-blue centred on
**198–202°**, its mass sitting between 185° and 214°.

### 5.2 The derived palette

One accent hue (198°, the sampled mean), one derivation, and **exactly the ten tokens every
palette file declares** (RF16: the palettes are one vocabulary, so a new token in one file
alone fails `a11y.mjs`). It is written as a fourth palette file, `palette-logo.css`:

| token | value | hue | what it is |
| --- | --- | --- | --- |
| `--accent` | `#17A8E6` | 198.0° | the logo's own hue, electric: structure, links' underline, focus ring, filled controls |
| `--accent-on-dark` | `#6FD8F5` | 193.0° | the accent's light tint for text on dark grounds (body-size text and links) |
| `--warn` | `#F5A524` | 37.0° | warnings and cost — D104's second role kept (D181) |
| `--ground` | `#0A0E16` | 220° | the page ground, a very dark blue-black (22° bluer than the logo's mean) |
| `--surface` | `#121A26` | 216° | raised surfaces (cards, code blocks) |
| `--text` | `#E6EEF5` | 208° | body text |
| `--text-muted` | `#9FB2C4` | 209° | secondary text |
| `--border` | `#24303F` | 213° | decorative rules and card edges |
| `--gradient-from` | `#08131F` | 211° | the hero's first gradient stop |
| `--gradient-to` | `#0A2233` | 205° | the hero's second gradient stop |

The label on an accent-filled control stays `--ground` (the pair `PAIRS` already asserts,
7.16:1), so no `--accent-ink` token is needed. The total shift is deliberate: `--ground`
moves from `#0B0B12` (a neutral violet-black) to `#0A0E16`, a blue-black, so the whole
page reads as the logo's colour rather than as a violet site with a cyan picture on it.

### 5.3 WCAG contrast, computed

Ratios computed from the sRGB relative-luminance definition over the values above, for
**every pair `a11y.mjs`'s `PAIRS` asserts** (and so every pair of `BASE_PAIRS`, which is a
subset of it):

| pair | ratio | needs |
| --- | --- | --- |
| `--text` on `--ground` | **16.47** | 4.5 ✓ |
| `--text` on `--surface` | **14.91** | 4.5 ✓ |
| `--text` on `--gradient-from` | **15.95** | 4.5 ✓ |
| `--text` on `--gradient-to` | **13.89** | 4.5 ✓ |
| `--text-muted` on `--ground` | **8.87** | 4.5 ✓ |
| `--text-muted` on `--surface` | **8.02** | 4.5 ✓ |
| `--text-muted` on `--gradient-from` | **8.58** | 4.5 ✓ |
| `--text-muted` on `--gradient-to` | **7.48** | 4.5 ✓ |
| `--accent-on-dark` on `--ground` | **11.79** | 4.5 ✓ (body text, links) |
| `--accent-on-dark` on `--surface` | **10.67** | 4.5 ✓ |
| `--accent-on-dark` on `--gradient-from` | **11.41** | 4.5 ✓ |
| `--accent-on-dark` on `--gradient-to` | **9.94** | 4.5 ✓ |
| `--warn` on `--ground` | **9.46** | 4.5 ✓ |
| `--warn` on `--surface` | **8.56** | 4.5 ✓ |
| `--warn` on `--gradient-from` | **9.16** | 4.5 ✓ |
| `--warn` on `--gradient-to` | **7.98** | 4.5 ✓ |
| `--ground` on `--warn` | **9.46** | 4.5 ✓ (a filled warning chip's label) |
| `--ground` on `--accent` | **7.16** | 4.5 ✓ (a filled control's label, the focused skip link) |
| `--accent` on `--ground` | **7.16** | 3 ✓ (focus ring, UI, large text) |
| `--accent` on `--surface` | **6.48** | 3 ✓ |
| `--accent` on `--gradient-from` | **6.93** | 3 ✓ |
| `--accent` on `--gradient-to` | **6.03** | 3 ✓ |

Two figures outside the pair set, recorded so nobody rediscovers them: `--accent` on white
is **2.70:1**, so the accent is never text on a light ground (the site has none, RF19), and
`--border` on `--ground` is **1.44:1** (1.31 on `--surface`) — admissible as a
**decorative** rule, which is how `a11y.mjs`'s own comment treats a hairline; if a border
ever marks a control's extent it must be raised to ≥ 3:1.

For comparison, the palette in force: `--accent-on-dark #C4B5FD` on `--ground` 10.62,
`--accent #8B5CF6` on `--ground` 4.63 — the same 4.63 is `--ground` on `--accent`, the
pair `PAIRS` holds to 4.5 for a filled control's label, which it clears by a tenth of a
point — and `--warn #F97316` on `--ground` 7.00.

**Every pair the site asserts passes under the derived palette**, all of them with room.

## 6. What the current site measures

Taken on 2026-10-18 on this branch's base (`85758e1`), Node v22.22.0, Playwright 1.49.1
with its own Chromium, `prefers-reduced-motion: reduce`, device scale factor 1. The capture script writes the same measurements to JSON when it is
run (the image directory is its first argument and the JSON path its second); the images
are regenerated into [current/](current/) and not committed.

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
72.5 s**; `dist/` 695,920 bytes over every file.

**The Go side, same base, same day:** `GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc
-run 'Docs|Lint|Website|CliRef' -count=1` → `ok github.com/dimipaun/fugaro/internal/cli
2.638s`, `ok github.com/dimipaun/fugaro/internal/clidoc 0.029s` — the eighteen
`TestWebsite*` pins and the seven `TestCliReference*` pins (RF25) all pass, so every fact the
pages render is still held to the file it is derived from. `GOFLAGS=-p=2 go vet ./...`
prints nothing and `gofmt -l .` prints nothing. The whole repository suite
(`fugaro verify test`) passes on the same commit.

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
changes the visual viewport can and will differ; D187 asks whether the acceptance check
is that measurement or a device the owner holds.

## 8. What this document does not decide

It does not decide the design. It does not choose the type scale, the spacing rhythm, the
new page copy, the exact menu groups, or the shape of the How It Works picture beyond the
constraints in RF11, RF12 and RF20. It records what is true today, what the brief asks
for, what the repository's rules permit, and which of the remaining questions are the
owner's — §9.

## 9. Owner decisions

Numbering, read off the owner's allocation table before a number was written
([`../mobile/README.md:20-42`](../mobile/README.md), the one table in this repository that
lists every line of work's block): **`D1`–`D20`**, **`D21`–`D38`**, **`D39`–`D54` (plus
`D54b`)**, **`D55`–`D78`**, **`D90`–`D109`** (the website work's first line), **`D110`–
`D129`** (mobile), **`D130`–`D159`** (the credentials service, `D155`–`D159` reserved) and
**`D160`–`D179`** (the mobile functional spec, `D174`–`D179` reserved) are taken. The gap
**`D79`–`D89`** is deliberately unused — the first line of work left it alone under the
rule "an unused number is not a free number" (`../reconciliation.md` §0, quoting
`docs/plans/2026-10-15-desktop-console-v2.md`). No document on `main` or in an open pull
request writes `D180` or above (checked 2026-10-18). The redesign therefore proposes the
first block above the highest allocation: **`D180`–`D192`**, thirteen numbers for the
thirteen rows below, and asks the owner to confirm the block in the same breath as
answering the rows. The same goes for the live checks: the plan's **`70a`–`70c`** sit in
a range `../mobile/README.md` records as "unused and deliberately not taken", so they too
are proposed, not assumed (implementation plan §4). A reserved number is never re-used and
never re-numbered: if the owner allocates differently, the citations here and in
[implementation-plan.md](implementation-plan.md) §8 follow the allocation rather than the
other way round. Each row gives the recommendation first; the same list appears in the
pull request body, because a decision this branch makes silently would be the one kind of
change nobody can review.

| # | question | recommendation | why | alternatives (cost) |
| --- | --- | --- | --- | --- |
| **D180** | **The accent and the palette** (§5) | Take the logo's own hue: `--accent #17A8E6` (198°), `--accent-on-dark #6FD8F5`, the ground moved to `#0A0E16` — §5.2's ten tokens, as a fourth palette file `palette-logo.css` made the palette in force; `palette-split.css` stays as the record of D104's option C | The brief says to sample the logo, and 198–204° is what the logo is; every pair `a11y.mjs` asserts passes (§5.3) | (a) keep `--accent #8B5CF6` and use the logo's hue only for the logo: cheapest, but the brief's complaint stands; (b) take the deep end of the sample (≈ 203°, `#0E7FC4`): calmer, less electric than the brief asks; (c) write the new values into `palette-split.css` instead of a new file: one file fewer, and the record of D104's choice is lost |
| **D181** | **What happens to D104's warning colour** | Keep D104's *role* (a second colour for warnings and cost) and move it from `#F97316` to the amber `#F5A524` (37°) | Amber reads as "attention" without reading as "error", and it is the stronger text colour (9.46:1 on the new ground against 6.89:1). Both are near-complementary to the accent (161° and 174° away), so this is a judgement of tone, not of hue distance | (a) keep `#F97316` exactly: it still passes 6.89:1 on the new ground; (b) drop the second colour: loses the caps-and-costs page's "attention" signal |
| **D182** | **The navigation's shape** | Three top-level sections with dropdowns — *Product* (How it works, Getting started, Caps and costs), *Reference* (Docs, Console, Changelog), *Project* (Security, FAQ, Community) — with Home as the logo link and a drawer on mobile; Privacy stays a footer link as today | Today's `NAV` is Home plus nine sections (`Base.astro:106-117`); three groups of three fit one row; Privacy is footer-only by the first line of work's choice (`FOOTER_NAV`) and nothing in the brief moves it | (a) five groups: more top-level items than a 390 px row holds; (b) two groups: long dropdowns, less scannable; (c) also put Privacy under *Project*: one more menu item, a change the brief does not ask for |
| **D183** | **How the JavaScript is delivered** (RF15) | **A first-party file** (`website/public/scripts/site.js`): no inline script, no new `INLINE_SCRIPTS` entry, no CSP edit | The CSP and the allow-list stay exactly as they are; the file is counted by `budget.mjs` like any other asset and is cached across pages | (a) an inline script with its sha256 in `INLINE_SCRIPTS` **and** in `firebase.json`'s `script-src`: no extra request, a policy edit and a hash to re-derive on every edit; (b) both shapes: two mechanisms to keep honest |
| **D184** | **What the homepage run animation may show** (RF10, RF11) | Lines the repository already writes — `fugaro run "…"` (today's command) and then the numbered steps of `README.md`'s `## How it works` (Start, Sync, Loop, Push and open the pull request), each held to the file at build time, with one caption saying it is the documented sequence and not a recording | It cannot go stale or invent a fact: the lines are held to the README exactly as the diagram's captions are | (a) **a real run's own output**, captured once from a sandbox run, committed as a fixture with its date and rendered verbatim: the closest reading of the brief's "a real Fugaro run", at the cost of identifiers (job id, branch, PR number) that are true of one past run and must be re-captured when the CLI's output changes; (b) a scripted illustration with invented ids, labelled as one: more sizzle, facts that are not facts; (c) keep the single command: leaves item 5 half-answered |
| **D185** | **How It Works: the brief's four steps or the site's five stages** (RF12) | The brief's four steps — task submitted, ephemeral Cloud Run job, agent runs, pull request — each captioned by a short **quoted** line: the `fugaro run` invocation, "one execution per task, then gone", README step 3 ("Claude Code implements, writes tests, runs the build and tests"), "Push and open the pull request"; `cache` and `security boundary` stay as two annotations on the job step | It is the flow the brief draws, it adds the one step the diagram lacks (the agent), and every caption is still a substring of its source, which is what the component's check holds (RF12) | (a) keep the five stages as they are and only redraw them: no new caption, but "agent runs" stays missing; (b) six stages (the five plus the agent): complete, and longer than the brief's "one line per step" picture wants |
| **D186** | **The "no internal text" rule** (RF5–RF8) | A new hermetic gate that refuses an explicit list of provenance phrases on every built page — "rendered from", "Nothing here is typed", "as it is generated", "generated rather than written", "rather than written here", "rather than described here", "points at those documents rather than repeating them" — stated as **a page must not explain why its own text exists** | A phrase list is checkable and mutation-provable, and it does not misfire on a link to `SECURITY.md` or on the docs hub naming the document each guide lives in; the banner of RF8 is never on a page and is untouched | (a) a rule over "names any repository file": fails on the legitimate citations; (b) no gate, a review habit: the brief's "Done when" asks for none to remain, and nothing would hold it |
| **D187** | **How "above the fold" is checked** (RF23) | A hermetic **word ceiling** (D188) plus **one owner-run live check** for the fold itself (70b) | Hermetic gates cannot see a viewport and this site's gates are deliberately browser-free; the ceiling is mutation-provable and the fold is what the owner's eyes are for | (a) the repository's first browser-driven test (Playwright in `website/test/`): a real fold assertion, a new dev dependency and the end of the browser-free posture; (b) the ceiling only: "above the fold" stays unchecked |
| **D188** | **The word ceiling as numbers** | Two numbers in `website/README.md`: **the lead** (visible text between the `h1` and the first `h2`, or the page's first section) **≤ 40 words on every page**; **the whole page ≤ 250 words** on every page except the three reference pages (`/docs/`, `/security/`, `/changelog/`), which have no whole-page ceiling | The lead is what sits above the fold, so it is the number that models the brief's principle; today's non-reference pages run 89–493 words, so 250 forces the cut on Getting Started (431), Caps and costs (487) and FAQ (493) without squeezing a page to a slogan | (a) one whole-page number, 150, and 400 for the reference pages' summary: stricter, cuts FAQ's six answers to about 25 words each, and needs a marker for where a reference page's summary ends; (b) a per-page table: precise, one more table to keep |
| **D188 (amended 2026-10-19)** | **The whole-page ceiling, once the privacy statement became the store listings' privacy policy** (the owner's request of 2026-10-19) | The row above stands for every page **but one**: `/privacy/` is the App Store and Play listings' privacy policy, so it carries the software as well as the site, and its ceiling is **derived from its own count** — that page's words plus 25 % (D192's headroom), rounded up — the figures are `website/README.md`'s, and they moved three times on 2026-10-19 as the software part, the app's reporting and the deletion section's carve-out landed — while **every other page stays within the row's 250**. The derivation is `website/test/budget.mjs`'s `wordsCeilingFailures`, beside the byte figures' own | A ceiling raised for one page is a ceiling every page gains, and nothing would say so: the second half of the rule is what keeps D188's number true for the rest of the site, and the first half makes a sentence added to the statement re-derive the README in the commit that adds it | (a) keep 250 and cut the software part to fit: the store reviewer's half of the page becomes a summary of a page it cannot link to; (b) exempt the page as `/docs/` and the other two are exempt: no number at all, and the page a listing links would be the site's only unmeasured one |
| **D189** | **Where the long pages' summaries come from** (RF24; Global Constraints 6 and 12) | Each summary is rendered from a file, never written on the page: **`/security/`** — a short "In short" section added at the top of `SECURITY.md` itself (written or approved by the owner, as every `SECURITY.md` change is), which the page renders like the rest; **`/changelog/`** — the first Highlights line of the newest few releases, selected from the release files, so `changelog-page.mjs` keeps passing; **`/docs/`** — the hub's existing one-line `answers` per guide, gathered at the top | It meets the brief without breaking the rule that a page renders and never restates, and leaves the byte-identity pin and the changelog's "no sentence of its own" test as they are | (a) summaries written on each page, with Global Constraint 6 amended for a marked summary block and `changelog-page.mjs` and the security pin given an exception for it: freer copy, the first restated facts on the site; (b) the security contents list counts as the summary: no work, and the brief's own example stays unanswered |
| **D190** | **Where the approved design lives** (RF26) | In the owner's design tool; the repository keeps the spec, these documents, the capture script and a short `redesign/design.md` recording the numbers the design fixed (type scale, spacing, accent usage, the menu groups). Screenshots, before and after, are regenerated and never committed | A design project file is not a document; the numbers it fixes do belong in the tree, because every later page change has to keep them | (a) keep the design only in the tool: the "match" check has no record in the tree; (b) commit the design's exports and the screenshots: several MB of binaries per round in `docs/` (the "before" set alone was 7.8 MiB) |
| **D191** | **Sequencing against the first line of work** | Redesign first, then the remaining `W2`/`W3` waves (the live checks, the deploy, analytics behind the banner) | The redesign changes `Base.astro`, `base.css`, the palette and several pages — the files `W3` deploys; doing it first deploys the finished look once | (a) finish `W3` first: the site goes live in its current look and the redesign is a second deploy; (b) interleave: two lines editing the same files |
| **D192** | **The byte budgets once the site ships a script** (RF14; the owner's ruling of 2026-10-18) | Re-derive them in the commit that ships the script, by the README's own rule: the tight figure is that build plus 25 % (JavaScript per page: the script file's bytes plus 25 %), the README's loose figure three times that; the page-weight and whole-site figures are re-derived the same way. Every later part that adds bytes (P7's CSS, P8's SVG) re-derives in its own commit | The ruling says the 0-byte rule and the numbers change deliberately, with their tests; a 100,000-byte ceiling held over a 2 KB script is a budget that cannot catch a regression | (a) keep today's numbers (100,000 / 662,766 / 1,454,826): no edit, and the JavaScript ceiling stays about fifty times what the site ships; (b) tight figures with no × 3: strict, and every copy edit risks a budget edit |
