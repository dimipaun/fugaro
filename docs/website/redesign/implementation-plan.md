# Website redesign — implementation plan

Status: **plan only; nothing of the redesign is implemented.** Owner: **@Dimi**. Date:
**2026-10-18**. Source: [spec.md](spec.md) — the owner's text, committed verbatim and
alone in this branch's first commit. This plan executes
[reconciliation.md](reconciliation.md), whose findings it cites by number (`RF<n>`); the
first line of work on the site is [`../implementation-plan.md`](../implementation-plan.md)
(tasks `W0.1`–`W3.3`, decisions `D90`–`D109`), and **this plan inherits that plan's §3
constraints rather than restating them** — it adds only the ones that are new. The
"before" pictures are in [current/](current/).

**The owner's ruling of 2026-10-18, verbatim:**

> use as much JavaScript on the website as needed. The strict CSP stays (`script-src
> 'self'`, no remote origins), the origins gate stays (hash allow-list or file scripts);
> the 0-byte-JS rule and the byte budget numbers change deliberately and their tests
> change in the same plan.

## 1. What this plan is for

The brief's "Done when" line, turned into nine parts and three live checks:

| the brief's "done when" | where it is done |
| --- | --- |
| the gist is above the fold on every page | P4 (the copy), P5 (the hero and the ordering), Check 70b |
| no internal/meta text | P4, gated by `website/test/meta.mjs` |
| the accent matches the logo | P2, gated by `website/test/accent.mjs`, Check 70c |
| the mobile header fits one row | P3, Check 70a |
| How It Works is visual | P8 |
| the homepage has the terminal animation | P7 |
| every command block has a copy button | P6, gated by `website/test/copy.mjs` |
| final screenshots match the approved design | P9, and the owner's eyes (RF26, D190) |

## 2. What is new in this plan's constraints, on top of the first plan's §3

1. **This deliverable is documents.** No part below is in this branch: this branch
   carries `spec.md`, `reconciliation.md`, this plan, `capture-screenshots.mjs` and
   `current/`, and nothing else. Every part is a later pull request.
2. **Nothing under `.github/workflows/`.** No part needs one; if one ever did, the file
   is proposed under `docs/pending-workflows/` for the owner to copy.
3. **`firebase.json`'s policy stays as it is** (`script-src 'self'`, no nonce, no
   `unsafe-inline`, no `unsafe-eval`), and `website/test/origins.mjs`'s `INLINE_SCRIPTS`
   allow-list is not added to unless D183 chooses the inline shape. The recommended
   shape — a first-party file — changes neither (RF15).
4. **One source of truth per fact.** A number a gate enforces lives in
   `website/README.md` and is read out of it (the pattern `budget.mjs` already uses).
   The word ceiling of P4 goes there too, beside the byte budgets.
5. **The site's gates stay browser-free** unless D187 decides otherwise: `npm run check`
   is hermetic and has no browser. A viewport claim is therefore either modelled
   (P4's word ceiling) or the owner's (Check 70).
6. **Every part is independently mergeable and independently revertible**: it changes
   one concern, it lands with its own tests, and reverting it leaves the site working.
   No part depends on a later part to be correct; where two parts touch the same file,
   they touch different lines and the later one is written to apply on top.
7. **The first plan's §3.3, §3.6, §3.8, §3.9 and §7.1 all still hold**, including "a wave
   starts on the owner's go" and §15's approval gate. The order below is a recommendation
   about sequence, not permission to start.

## 3. The parts

Each part: what it changes, the gates it must leave green, how to verify it, and — where
a budget, a gate or a pinned number moves — **the test change, named**, in the same
commit. Every verification runs from `website/` unless it says otherwise:

```sh
npm run check                                                 # build + 179 tests today
GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'Docs|Lint|Website|CliRef' -count=1
```

### P1 — the status correction (RF1)

**What.** Three lines, in three documents, that say the site does not exist:
`../README.md:3` and `:5`, `../reconciliation.md:3`, `../implementation-plan.md:3`. Each
becomes a true sentence with the date it became true — the site exists, it is built,
`npm run check` passes, and the redesign is the line of work now open against it. Also
`README.md` in this directory, which states the redesign's own status.

**Nothing else.** No rewrite, no reflow, no "and while we are here".

**Verify.** `git show --stat` is three files and a handful of lines; the sentence about
the tests can be checked by running `npm run check` (179 pass on this base).

**Why first.** A document that says the site does not exist is the one error that makes
every later review of the redesign confusing, and it is the cheapest possible commit.

### P2 — the accent, sampled from the logo (RF16, RF18, RF19; D180, D181, D189)

**What.** `website/src/styles/palette-split.css` takes the token values of
reconciliation §5.2 — `--accent: #17A8E6` (the logo's own hue, 198.0°),
`--accent-on-dark: #6FD8F5`, the new `--accent-ink`, the ground moved to `#0A0E16`, the
surfaces, borders and gradient ends with it, and `--warn` moved to `#F5A524` if D181
says so. `website/public/site.webmanifest`'s `theme_color`/`background_color` follow
`--ground` (`brand.mjs` asserts the equality, so the two move in one commit).
`website/src/styles/palette-violet.css` and `palette-orange.css` stay as they are: they
are the record of the choice the first line of work made, and `a11y.mjs` still lints all
three.

**New gate: `website/test/accent.mjs`.** The brief's "Done when: accent matches the
logo" needs a gate that cannot be satisfied by a palette edit from memory. Node has no
PNG decoder in this repository's dependency set (D106/§3.8: no new third-party
dependency for a lint), so the test carries the **sampled hue as a recorded fact** —
198.5°, saturation-weighted circular mean over `brand/fugaro-logo.png`'s opaque pixels at
saturation ≥ 0.5, taken 2026-10-18 by the method in reconciliation §5.1 — and asserts
that the palette in force's `--accent` is within **5°** of it, and that `--warn` is at
least 90° away from it. Mutation-proven: set `--accent` back to `#8B5CF6` (258.3°) and
the test fails; that case is the test's own fixture, so the gate proves it can fail.

**Where a gate moves.** `a11y.mjs` reads `--accent`/`--accent-on-dark` pairs out of the
palette in force, so no list edit is needed for the values themselves. It **is** edited
in one place: if the part ships an accent-filled control (a button or a badge carrying
`--accent-ink` on `--accent`), that pair joins `PAIRS`, with the ratio from
reconciliation §5.3 (7.01) asserted like the others.

**Verify.** `npm run check` (179 + the new accent tests, all green); then, by eye and
only by the owner, the logo beside the accent (RF19/RU3).

**Independently mergeable because** it changes colour tokens and nothing structural: the
nav, the copy and the pages are untouched, and a revert restores the violet palette
exactly.

### P3 — the two-level menu and the mobile drawer (RF3, RF4; D182)

**What.** `website/src/layouts/Base.astro`'s `NAV` becomes groups (recommendation in
D182: *Product*, *Reference*, *Project*, with Home as the logo link), rendered as a
two-level menu on a wide viewport and as a drawer behind a single toggle on a narrow one.
`website/src/styles/base.css`'s `.site-nav` rules are rewritten for it, and the comment
at `base.css:117-118` — "at 360px the ten sections are three short lines" — is replaced
by the truth.

**No script.** The drawer is `<details>`/`<summary>` plus CSS: it works with JavaScript
off, it needs no CSP decision, it costs no bytes of the JavaScript budget, and it is the
only version of the mobile fix that is testable without a browser. It also means P3 can
merge before P6 (which is where the first script arrives) without either part waiting.

**Where a gate moves — this is the part that edits a test, deliberately.**
`website/test/base-styles.mjs` asserts today that `.site-nav ul` is `display:flex` and
that the section nav is a labelled landmark of keyboard-reachable links with exactly one
`aria-current="page"`. Under the new structure those assertions become: the nav is a
labelled landmark; its top-level items are links or disclosure summaries; **exactly one**
`aria-current="page"` still exists on every page; every link in the menu is reachable by
keyboard (no positive `tabindex`, no `display:none` on the reachable path); and a
`<details>` that is closed does not hide the current page's own marker. The case that
proves the check can fail moves with them.

**New live check: 70a** (see §4) — the mobile header on one row, the drawer openable by
keyboard, and the menu readable with JavaScript disabled.

**Verify.** `npm run check`; then Check 70a by the owner at 390 px, and the desktop menu
by eye.

**Independently mergeable because** it is the only part that touches the nav: P4–P8
change page bodies and one component each, not `Base.astro`'s menu.

### P4 — cut the text, and gate it (RF5–RF8, RF23, RF24; D186, D187, D188)

**What.**

- `getting-started.astro:61` — the meta paragraph goes, and the page opens with the
  install commands and the platform list (the facts of `website/data/install.yaml` were
  always there; only the explanation of where they come from was on the page).
- `docs.astro:232-235` and `:259-262` — both meta paragraphs go. The hub then needs a
  **real** lead drawn from the documents' own facts (RF24): what a reader finds in each
  group of guides, one line each — not a description of the pipeline that renders them.
- `faq.astro:397` and `community.astro:224` — the "taken from a document in this
  repository rather than written here" and "points at those documents rather than
  repeating them" sentences go, replaced by what the page is for.
- `/changelog/` gains the short summary of key points the brief asks of every long page
  (RF24): a handful of lines at the top, each one a fact the release notes below already
  state, with the detail left where it is.
- Every page's copy is rewritten short and scannable after that: headings keep their
  meaning, the paragraphs under them shrink. The three reference pages (`/docs/`,
  `/security/`, `/changelog/`) keep their detail by the brief's own allowance.
- The homepage's `h1` — "Fleeting cloud workers for coding agents: one task, one
  container, one PR, then gone." — is measured ending at 893 px on a 390 × 844 screen
  (reconciliation §6). P5 fixes the fold; P4 fixes the words.

**Two new gates.**

- **`website/test/meta.mjs`** — the phrase list of D186, refused on every built page,
  with a fixture page that carries one of the phrases so the test proves it fails. The
  rule it states in its header: *a page must not explain why its own text exists.*
- **A word ceiling in `website/test/budget.mjs`** — the same test, the same
  `website/README.md`, one more number: **words of visible text per page**. The
  recommendation is D188's 150 for every page except the three reference pages, which get
  400 for what sits above their contents list. The ceiling is read out of
  `website/README.md` exactly as the three byte budgets are, so there is one source of
  truth, and the test carries the case that proves it fails.

**Where a gate moves.** `website/README.md`'s budget section gains the word ceiling and
its derivation (the measured word counts of reconciliation §6 are the derivation's
input); `budget.mjs` gains the reader and the case. The byte numbers do not move.

**Verify.** `npm run check`; and the owner reads the rewritten pages (P9's before/after
set is the picture of it).

**Independently mergeable because** it changes page bodies and two tests; the nav, the
palette, the components and the script are untouched.

### P5 — the gist above the fold (RF5, RF23; D187)

**What.** The homepage hero is rebuilt so that the `h1`, the one-line promise and the
first command are inside a 390 × 844 first screen (today the `h1` alone ends at 893 px,
below the fold, because the header is 177 px and the hero is tall). Getting Started's
first screen becomes its first command. This is a layout change in
`website/src/pages/*.astro` and `base.css`'s spacing tokens — not another rewrite of the
copy (P4).

**Where a gate moves.** None hermetically: no browser-free test can see a fold, which is
exactly D187's question. If the owner takes D187's second reading (a browser-driven test
in `website/test/`), this part adds it and the repository's browser-free posture is a
recorded decision rather than an accident.

**New live check: 70b** — the fold, at 390 × 844 and 1280 × 900, on the built artifact.

**Verify.** `npm run check`; Check 70b by the owner; the P9 "after" screenshots.

**Independently mergeable because** it moves blocks within pages P4 has already
rewritten, and neither part's tests assert the other's layout.

### P6 — a copy button on every command block (RF13, RF14, RF15; D183)

**What.** One first-party script — recommended at
`website/public/scripts/copy.js`, one file for the whole site — and one change to the
component that renders a command, so that every `<pre>` on the built site gets a control
with an accessible name, a keyboard path, a visible focus ring, a "copied" state that is
announced politely (`aria-live="polite"`) and that clears itself, and **no flash of
useless control** when the script does not run: the control is rendered hidden and the
script reveals it, so a reader with JavaScript off sees the command and no broken button.

Measured today: 23 `<pre>` blocks (home 3, Getting Started 10, Docs 10) and zero controls
(RF13).

**Where a gate moves — named, in this commit, per the owner's ruling.**

- `website/README.md:89-90` — "The site ships no script today" is no longer true, and the
  sentence that says the 100,000-byte allowance "leaves room for W1.12's consent banner
  and the logo work without a budget edit" is rewritten to say what it now holds. **The
  number stays 100,000 bytes** (RF14: it was set loose on purpose and this is what it was
  set loose for).
- `website/test/budget.mjs:16-17` — the header's tight derivation says "JavaScript per
  page: 0 bytes. The built site ships no script at all today"; it becomes the new
  derivation (one file of *n* bytes, counted per page), or it would be a comment that is
  false about the tree.
- `website/test/budget.mjs` gains the case it has never had: a fixture page that fetches
  a first-party script, proving `measurePage`'s `js = inlineJS + fileJS` accounting is
  real (today no page in the build has a script, so that half of the gate has no
  subject).
- `website/test/origins.mjs` — **no change**, if D183 takes the file shape: the script is
  a `'self'` file and `INLINE_SCRIPTS` keeps its single consent-banner entry. If D183
  takes the inline shape instead, this part adds one sha256 to that Map, keeps the case
  that proves an unlisted inline script is refused, and notes the same hash in the
  deployed policy's comment.
- **`website/test/copy.mjs`** (new) — every `<pre>` on every built page carries exactly
  one control, the control has an accessible name, and the page's script tags are
  first-party by construction (`origins.mjs` already refuses the rest).

**Verify.** `npm run check`; Check 70c by the owner (click a button on the deployed site
with the clipboard in hand); and the Lighthouse numbers of D105 re-run by the owner
(RU4), because this is the part that first moves them.

**Independently mergeable because** it is the first script the site ships and it touches
one component and one public file, plus the budget test's prose and one new case.

### P7 — the homepage run animation (RF9–RF11; D184)

**What.** `website/src/components/Terminal.astro` grows from one typed command into the
documented shape of a run: the dispatch line, the job starting, the sync of the target
commit, the push and the draft pull request — **the numbered steps of `README.md`'s
`## How it works`**, typed in order, each line held at build time to the document it came
from, exactly as `ArchitectureDiagram.astro` holds its captions today (RF11). And one
caption on the page saying what it is: the documented sequence, in the repository's own
words, not a recording.

**Still no script.** Multi-line typing is CSS: one reveal per line, staggered with
`animation-delay`, `steps(<characters of that line>, end)`, all inside the existing
`@supports`/`prefers-reduced-motion` guards, with the whole transcript present in the
markup so a reader with animations off reads it in full (the property Terminal.astro
already has and today's tests already assert).

**Where a gate moves.** `website/test/home.mjs` holds the current single command to
`README.md` and to `content/cli-reference.md`; it grows to hold **every** line of the
transcript to its source, and it keeps the case that fails when one line is not in the
document it cites. No byte budget moves: CSS animation is not JavaScript.

**Verify.** `npm run check`; and the owner watches the homepage and then sets
`prefers-reduced-motion: reduce` and watches it again (the transcript must be complete
and still).

**Independently mergeable because** it touches one component on one page and one test.

### P8 — How It Works, rebuilt as a flow (RF12, RF20, RF21; D185)

**What.** `website/src/pages/how-it-works.astro` and
`website/src/components/ArchitectureDiagram.astro`: the five quoted stages become a
vertical flow, one line each, with an arrow between them and the numbered rail replaced
by something a reader follows top to bottom. Every caption stays a line of the document
it cites (the component's own build-time check, unchanged), and the picture itself is
first-party SVG/CSS — no image, no third-party asset, no player (RF20, RF21). If D185
chooses the brief's four steps instead, this part merges `cache` into `cloud job` and
takes the merged caption from the same document rather than writing one.

**Where a gate moves.** `website/test/home.mjs` and `a11y.mjs` hold the diagram's
captions, its `<ol>` semantics and its `aria-hidden` art; they follow the new structure,
including the arrow being decorative and the stages remaining an ordered list a screen
reader reads in order.

**Verify.** `npm run check`; the owner compares the page against the approved design
(P9).

**Independently mergeable because** it is the only part that touches the diagram, and the
page keeps its `h1` and its route throughout.

### P9 — the design record and the "after" set (RF26; D190)

**What.** `docs/website/redesign/design.md` — the numbers the approved design fixed
(type scale, spacing rhythm, the accent's roles, the menu groups, the flow's shape),
recorded so that a later page change has something to keep to — plus
`docs/website/redesign/after/`, the same eleven routes at the same two viewports, taken
by [`capture-screenshots.mjs`](capture-screenshots.mjs) with its **output directory as the
argument** (`node capture-screenshots.mjs docs/website/redesign/after`): the script's
output directory is an argument and not a constant for exactly this reason, because the
"before" set is the baseline the "after" set is compared against and a script that wrote
both to one directory would overwrite the baseline on the second run. Everything else about
the run is unchanged, so the two sets are comparable picture for picture. And the owner's
comparison of the two sets: **that step is the owner's, not an agent's** — the model that
takes these pictures cannot view one (RU1).

**Where a gate moves.** None. This part is documents and images; nothing in the build
reads it.

**Verify.** `git status` shows only files under `docs/website/redesign/`; the file names
of `after/` match `current/`'s route for route and viewport for viewport.

**Independently mergeable because** nothing depends on it.

## 4. The live checks

Three of the brief's "done when" lines cannot be hermetic (§2.5): the mobile header on
one row, the gist above the fold, and the copy buttons working in a browser with a real
clipboard. They become **Check 70**, written as `70a`–`70c` in
[`docs/gcp-live-checklist.md`](../../gcp-live-checklist.md), each written by the part
that needs it — 70a by P3, 70b by P5, 70c by P6 — in the shape that register uses: a
guard block that reads its inputs out of the tree rather than remembering them, a step
list a person runs, an explicit **USER-RUN, NOT RUN** result line, and the rule that
**no agent runs any step of it**.

**The register's numbering.** The register's highest entry is `58`, with `69a`–`69c`
written by the first website plan (W2.2 and W2.3). `70` is the next free number and
nothing in this repository writes it. It is read out of the register before the check is
written, and if `70` has been taken by the time P3 lands, the owner allocates.

## 5. What moves, in one table

Explicit, because the owner's ruling says the tests move in the same plan as the rule
they enforce:

| rule or number | where it is written | what happens | which part |
| --- | --- | --- | --- |
| JavaScript per page: 100,000 B | `website/README.md:89` | **unchanged number**; the sentence that says "the site ships no script today" is rewritten | P6 |
| "JavaScript per page: 0 bytes" derivation | `website/test/budget.mjs:16-17` | rewritten to the new derivation | P6 |
| the JS accounting's own case | `website/test/budget.mjs` | **new case** (a fixture page that fetches a script) | P6 |
| words per page | `website/README.md` (new line) + `website/test/budget.mjs` | new number, new reader, new case | P4 |
| `INLINE_SCRIPTS` (sha256 allow-list) | `website/test/origins.mjs` | **unchanged** under D183's recommendation (a file script); one entry if the inline shape is chosen | P6 |
| `script-src 'self'` | `firebase.json` | **unchanged** — no edit, either way | — |
| manifest colours = `--ground` | `website/public/site.webmanifest` | follows `--ground`; `brand.mjs`'s equality assertion is unchanged | P2 |
| `.site-nav ul{display:flex}` and the one-`aria-current` rule | `website/test/base-styles.mjs` | rewritten for the two-level menu, with the page's current marker still exactly one | P3 |
| reduced-motion path per animation | `website/test/a11y.mjs` | **unchanged**, and every new animation must satisfy it | P7, P8 |
| the diagram's captions and semantics | `website/test/home.mjs` | follow the new flow; the quoted-caption check stays | P8 |
| the homepage transcript | `website/test/home.mjs` | grows from one line to every line | P7 |
| the accent versus the logo's sampled hue | `website/test/accent.mjs` (new) | new gate | P2 |
| provenance phrases | `website/test/meta.mjs` (new) | new gate | P4 |
| copy controls | `website/test/copy.mjs` (new) | new gate | P6 |

`website/package.json`'s check script is `node --test test/*.mjs`, so a new test file
under `website/test/` is picked up with no edit there. The Go pins (`internal/cli`) are
untouched by every part: no part changes a generated document, the canonical host, the
install facts or the security page's bytes.

## 6. Order and the owner's go

The recommended order is P1, P2, P3, P4, P5, P6, P7, P8, P9, one pull request each. P1 is
free. P2 and P3 are independent of each other and of everything after them. P4 and P5
read best in that order (the words, then the fold), P6 and P7 are independent of each
other, P8 is independent of all of them, and P9 closes the line.

**No part starts on its own.** The first plan's §7.1 holds: a wave starts on the owner's
go, and §15's approval gate holds for each. This plan's nine parts are nine such
decisions, and the order above is a recommendation about sequence rather than a schedule.

## 7. What this plan does not do

It does not design the site: the type scale, the spacing rhythm, the copy itself and the
menu's labels are the design step's (D190), constrained by the rules in RF11, RF12, RF20
and RF21. It does not add a dependency, a workflow, a hosting-policy change or a
third-party asset. It does not touch `belong`, EdgeWeb or EdgeServer, reading or writing
nothing outside this repository. And it does not deploy anything: D92's live checks and
D191's sequencing are the owner's.

## 8. Decision numbering

Decisions are allocated by the owner, block by block, and the allocation is recorded in
[`../mobile/README.md:20-42`](../mobile/README.md) — the one table that lists every line of
work's block. Reading it: `D1`–`D20` (console), `D21`–`D38` (console v2), `D39`–`D54` +
`D54b` (voice), `D55`–`D78` (managed projects), `D90`–`D109` (this site's first line of
work), `D110`–`D129` (mobile), `D130`–`D159` (the credentials service, `D155`–`D159`
reserved) and `D160`–`D179` (the mobile functional spec, `D174`–`D179` reserved) are taken,
and the one number between them, `D79`, is unused without being free. **The redesign therefore takes
`D180`–`D191`**, the first block above the highest allocation — proposed for the owner's
confirmation rather than assumed, in [reconciliation.md](reconciliation.md) §9. A reserved
number is never re-used and never re-numbered: if the owner allocates the block
differently, the citations here and in the reconciliation follow the allocation rather than
the other way round.
