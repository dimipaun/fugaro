# The Fugaro website

The site build (website plan, Part 1, Task **W1.1**). This directory is a **second
artifact** that lives in this repository beside `console/`, with its own language, its own
build and its own schedule (D90): nothing under it is importable by the Go module, and the
Go module gains no dependency from it. The boundary is a test, not a convention —
`internal/cli/docs_website_boundary_test.go`.

**What is here today:** the shell. The pinned toolchain, the static Astro configuration,
the check that runs the build and every test file, and this document. `website/data/` and
`website/content/` already hold the facts Part 0 generates; the pages that render them land
in W1.3 (`src/layouts/`) through W1.12. **The build therefore produces no page yet**, which
is why `npm run build` is green today and why the first page's own test arrives with the
first page.

## Toolchain

**Node 22 LTS**, pinned in `package.json`'s `engines`: `>=22.22.0 <23`. The floor is
Astro's own requirement rather than a preference, and the ceiling is the major line this
site is built and checked on — the engines range is the supported toolchain, and it is the
line `ci.yml` installs. `npm run check` passes on **Node 22.22.0**, which is what the whole
of the suite — the build and every `test/*.mjs` — was last run on, and it is the version this
document's figures were measured under. An earlier revision of this paragraph recorded a run
on Node 26.x instead, with the engines check disabled: that is not the supported toolchain,
and nothing here is measured on it any more.

The dependency tree is pinned exactly: `package.json` names `astro` at one version, and
`package-lock.json` is **committed**, so `npm ci` installs the tree this site was checked
with and nothing else. Every package in the lockfile resolves from `registry.npmjs.org`.
The shell ships **no integration** — no adapter, no image service, no font pipeline — and a
task that needs one adds it there, with the byte budget that task carries (F56, D106).

## Install, build, check

```sh
npm ci        # install the pinned tree (this is the only step that needs the network)
npm run build # build the site into dist/
npm run check # the site's gate: the build, then every test/*.mjs
```

**The build needs no network.** `npm ci` fetches the pinned tree once; after that nothing
here fetches anything: there is no integration, no remote font, no remote image, and the
build script sets `ASTRO_TELEMETRY_DISABLED=1` so that Astro's own anonymous usage report
is off too. `npm run check` is the same command a developer runs and the one W2.1 stages
the deploy workflows to run (`website-preview.yml` and `website-promote.yml`, staged under
`docs/pending-workflows/` for the owner to copy in: no Fugaro run may write them into
`.github/workflows/`), and it is hermetic: it reads this
repository, builds, and asserts over the artifact — no cloud, no credential, no deployed
site. The live checks — the real domain, DNS, TLS and the deployed HTML — are the owner's,
and W2.2 and W2.3 write them as **checks 69a–69c** in `docs/gcp-live-checklist.md`: 69a (the
site is live and honest) and 69b (nothing phones home) are written by W2.2, and 69c (the
published ranges are real) is W2.3's, in the level after it.

## Nothing loads from a third party

No font, script, stylesheet, image or embed reaches a page from an origin outside this
build: the pages use the system font stack `src/styles/tokens.css` names, no font file
ships today, and a font added later is **self-hosted in this artifact**
(`website/public/fonts/`, which the layout preloads from), and there is no Google Fonts, no CDN, no embed and no pixel — because every
third-party origin is a request from a reader's browser to a company this project has no
relationship with (D106). There is exactly **one exception, and the owner chose it**: the
analytics script (D93), loaded **only after the visitor consents**, with W1.12's banner as
the gate that decides whether anything is fetched and its own tests for no request before
consent, no cookie before consent, withdrawal and keyboard reachability. `readme.mjs`
refuses a third-party font or script host in the shell's own sources today, and W1.11
asserts the same over the built HTML's script, link, iframe and image origins.

**W3.2 is the task that switched it on** (owner's ruling of 2026-10-18): Google Analytics 4,
measurement id `G-DZ4F35D3EH`, which is a public identifier rather than a secret and lives in
`data/site.yaml` and nowhere else — the layout and the test both read it from there, and a
planted second copy fails. The id, the audit trail and the choices behind them:

- **The property is a fact of `site.yaml`**, beside the flag that emits it: `analytics.enabled`
  decides whether the banner and its configuration are rendered at all, and clearing the flag
  leaves an artifact byte-for-byte free of the origin. When the flag is on, the banner carries
  the id and the one admitted origin as `data-analytics-id` and `data-analytics-origin`.
- **The gate is code in this site's one script file**, `public/scripts/site.js` — not an inline
  block — because D183 admits one script and because a first-party file is admitted by
  `script-src 'self'` while an inline block would need a hash in the deployed policy. The file
  reads the configuration out of the banner's markup, so it names no URL at all: with the flag
  off it finds no banner and does nothing.
- **Nothing is requested from Google before a decision.** A stored `granted` (under
  `fugaro:analytics-consent`, the key the banner's markup carries) loads the script once; a
  refusal, a dismissal, an unreadable answer and silence all load nothing. `test/consent.mjs`
  runs the shipped file in a stub browser and watches the request that does or does not arrive.
- **The privacy defaults are pinned in the one `config` call**: one page view per page and
  nothing else — no event, no user id — with `allow_google_signals` and
  `allow_ad_personalization_signals` both false. GA4 anonymises the IP address by default;
  there is no setting to add for it. `test/consent.mjs` reads `dataLayer` and holds each of
  those to what was built.
- **Withdrawing deletes the cookies the tag set** (owner's ruling of 2026-10-18): stopping the
  sends was only half of a withdrawal — Google's tag left first-party cookies on the site's
  origin, and a visitor who has said no should not keep being identified by them. So declining
  after accepting, from the banner the privacy page always offers, deletes `_ga`,
  `_ga_<container id>` (that name derived from the measurement id above rather than written
  down), `_gid`, `_gat` and each `_gat_*` cookie the browser is holding — on the page's host,
  and, only while one is still there, on the registrable parent domain the tag may have used —
  with `Path=/` and an expiry in the past, and with nothing else in the browser touched. No
  deletion, and no cookie read at all, runs before consent was ever given. `test/consent.mjs`
  runs the shipped file against a cookie jar holding those cookies and reads what is left.
- **The deployed policy is widened by exactly what Google documents** for Google Analytics 4
  without any Ads features
  ([tag-platform/security/guides/csp](https://developers.google.com/tag-platform/security/guides/csp),
  read 2026-10-18): `script-src` gains `https://www.googletagmanager.com`, `img-src` and
  `connect-src` gain that origin and `https://*.google-analytics.com`, and `connect-src` gains
  `https://*.google.com`. No `'unsafe-inline'`, no `'unsafe-eval'`, no nonce and no hash —
  the site ships no inline script. `test/origins.mjs` holds the policy to that exact token
  list, and refuses any token it does not carry.

## The gates

Twelve checks run over the **built** artifact (`dist/`) on every `npm run check` (W1.11), each
with fixtures that keep its refusals running, and none of them needs the network. The count is
a fact about the list under it, and `website/test/readme.mjs` holds it to that list: a gate
added or removed without this sentence moving fails there:

- `test/linkcheck.mjs` — every internal link and asset resolves to a built file, every
  `#anchor` to an id in its target, every external link is `https:` on `site.yaml`'s
  `allowed_hosts`, and every GitHub link into this repository names a path and heading that
  exist.
- `test/origins.mjs` — nothing loads from a host that is not this site (images and embeds
  may also come from `allowed_hosts`), inline scripts are on a hash allow-list — empty since
  W3.2, because the site ships no inline script at all — and the one third-party origin D93
  admits appears only as the consent banner's configuration. It also reads the shipped
  `public/scripts/site.js` for the gate's contract (no URL, no second analytics vendor, the
  consent decision compared with `granted`) and `firebase.json`'s Content-Security-Policy
  against the token list Google's documentation states, refusing an extra origin, an
  `'unsafe-inline'`, an `'unsafe-eval'`, a nonce, a hash or a wildcard.
- `test/a11y.mjs` — after W1.2's contrast cases: lang, one `h1`, no heading jumps,
  landmarks, a skip link, names and labels, keyboard reachability, focus outlines, a
  `prefers-reduced-motion` path for every animation, and the shipped tokens equal to the
  palette in force.
- `test/budget.mjs` — the byte budget, whose numbers are these (raw bytes). **They are
  deliberately loose, by the owner's ruling of 2026-10-17: size is not a concern yet, so
  each is three times what the build of 2026-10-19 needs, and a later optimisation pass
  tightens them.** The gate itself is not loose: a page or a site over the number fails, and
  the test keeps a case that proves it. The derivation (that build plus 25 %, D192) is in the
  test's header, and a task that moves the bytes re-derives the numbers here, in the commit
  that moves them; the gate refuses a derivation that names a build other than the one it can
  measure, so the figures cannot drift down as pages grow.

  **The build the three numbers are derived from is the build the gates make themselves**, and
  that is the correction of 2026-10-19 (this section's figures are re-derived below it). It is
  *not* `website/dist`, which is what the derivation named until then: the site's own build
  depended on the git state of the checkout the gates run in. The changelog rendered each
  release file's **"not yet released"** state wherever `git tag --list` found no tag
  (`src/lib/changelog.mjs`), so the same commit built to two site figures a changelog page's
  own bytes apart — the larger in a checkout with no
  tags** — which is what `website-preview.yml`'s default checkout is, and therefore what the
  deployed site is — and the smaller with the tags this repository's own clone carries, the
  changelog page's own "not yet released" labels being the whole of the difference. A figure
  derived from one of those two builds was refused by the other, which is what happened: the
  gate could not measure the build the README named and `website-preview` could not deploy. So
  the derivation is read over **the gates' own build of the site**: a copy of the repository in
  a temp directory, built with the site's installed Astro by `test/lib/build.mjs`'s `buildCopy`,
  which carries no `.git` at all and so gives every checkout the same three numbers. The site's
  own `dist/` is held to the ceilings below like any other artifact.

  **No page of the site depends on the checkout's git state any more** (the owner's brief of
  2026-10-19): which releases are published comes from `plugin/.claude-plugin/plugin.json`'s
  version rather than from a tag, so the two checkout shapes above are one build now — the same
  commit builds to **987,954 bytes in a checkout with no tags** and to **987,954 bytes with the
  tags this repository's own clone carries**. `test/budget.mjs` reads that pair **here and
  nowhere else** (a figure copied into the gate's own comments or another document is a second
  place to forget, and this pair was once copied in two, which is how a clone figure came to sit
  a round's bytes behind) and refuses it unless the two are one figure. The derivation stays
  where it is all the same — a copy of the repository built by the gates is the artifact the
  gates can measure in every environment, `dist/` or no `dist/`.

  **What each cause really moves**, measured on that build rather than argued — and stated as a
  rule rather than as a figure, because a figure here is a figure that has to be re-derived every
  time the build moves: the numbers the causes produce are the ones above and in this
  paragraph, which `test/budget.mjs` reads and holds to the build it makes itself, and the
  unreleased build's own size is measured by `test/console-option.mjs`:

  - flipping **`features.console_downloads`** — the flag the console's release turned on, and the
    flag `test/console-option.mjs` flips back off to prove the unreleased state is still safe —
    moves the **site figure up** by the console option's own markup on the three pages that
    render it: the console page's downloads section (each artifact's checksum beside its link,
    and the verification command), Getting Started's option and the homepage's call to action. It
    moves the **page weight not at all**: the heaviest page is the docs hub, which renders no
    install option, no download and no hero. The copy the gates build of the unreleased state is
    therefore the smaller of the two, and that is the direction a reader of this list needs:
    the flag's round is the one that added the bytes;
  - the script and the pages that follow are the figures' own subject, and each moved them in
    the commit that shipped it (D192).

  **The build of 2026-10-19 is the merged one, and this is one account of it.** The redesign
  branch merged `main` into it, so the artifact carries both halves at once — `main`'s site,
  which is the one script the site ships and the behaviour it holds, and this branch's
  redesigned pages on top of it, with the wording change of the changes spec's item 2 — and
  neither half's own account of the figures is the whole story any more. What carries those
  numbers is, in one list:

  - `main`'s script: the copy control of P6 and D183; the menu's Escape/outside-click close
    of P3; W3.2's consent banner and analytics gate, which moved out of the markup and into
    this one file, with the cookie deletion the owner's ruling of 2026-10-18 added to a
    withdrawal; and, from the same ruling's brief, its item 1 — a group's submenu opens when
    the pointer arrives at it on a device whose pointer hovers — together with the
    corrections of 2026-10-19 to that behaviour: the event decides which pointer arrived and
    the device's answer is watched rather than sampled once; the focus a click or a tap moves
    opens nothing, while a close the browser makes hands the reader's focus back; a close the
    reader asks for from outside the menu hands nothing back; the pointer's close waits while
    a keyboard reader stands in the group; and the keyboard's way into a group is bound
    whatever the device answers.
  - this branch's pages: the homepage's order (the promise and its calls to action, the chat,
    the diagram, the install paths — changes spec item 4), the run animation (P7), the How It
    Works flow (P8) and the long-page cuts (P4), with the changes spec's items 2 and 3 over
    all of it.
  - `main`'s picture: the owner's `fugaro watch` capture and its transcription, placed on the
    homepage by #655.
  - this branch's two changes from the owner's brief of 2026-10-19: the menu's drawer replaced
    by the three group labels in the header's row (item 1), and the console's link in the
    homepage's call to action (item 6), which renders from the console's flag.
  - the console's release (W3.1): `features.console_downloads` and the data file's
    `available` turned on together, which puts the console page's downloads, Getting Started's
    console option and the hero's link to the console into this build.

  Each half moved these figures in its own commits and re-derived them there — the script's
  six corrections in `main`'s, the redesign's fix rounds in this branch's. The last rounds are
  where this build's three numbers come from, and each was measured over two builds rather than
  argued; the arithmetic itself is the gate's, in `test/budget.mjs`:

  - **This branch's own fix rounds**, each re-derived in the commit that moved it: the round
    after the merge moved the chat above the diagram, drew its turns as alternating bubbles,
    took three of the four cyan edges off the flow's cards and shortened the footer's link
    label; the round after that took the install section's own top padding off, because the
    flow above it already carries the gap between the two blocks, which moved the site figure
    alone, with the page weight where it was, the heaviest page being the docs hub, which
    renders neither block; and the round after *that* moved both figures up by the two
    declarations that make a command wrap (`white-space: pre-wrap; overflow-wrap: anywhere`)
    moving onto `base.css`'s `.command-block > pre`, which every page inlines. That last round
    is also why **the page and the site move with the script**: a script every page loads is a
    page's weight as well as its JavaScript.
  - **The `fugaro watch` screenshot landed on the homepage** (`main`'s #655) and moved the
    site figure alone. The owner's capture and its transcription
    (`public/images/fugaro-watch.png` and `fugaro-watch.txt`) are served by `public/` and are in
    every build's `dist/`, and the derivation the figure named before them predated the assets:
    those assets, plus the homepage markup that put the picture in its slot, are the whole of
    the move. The page weight does not move with it, and that is the check worth keeping: the
    picture lands on the homepage, and the homepage is not the heaviest page — the docs hub is.
    The JavaScript is untouched.
  - **This branch's menu (item 1), and the review round after it**, measured against that
    build file by file. Taking the drawer out — the `Menu` toggle, the panel it opened and the
    sibling combinator that showed it — leaves each of the eleven pages **278 bytes lighter**,
    the base stylesheet every one of them inlines being that much shorter, and takes **181
    bytes off the script**. The review round then put the script's own comments back in step
    with the menu, three of them still describing the retired drawer, which is **290 bytes onto
    the script** — comments are part of the file the browser fetches. So the script is 39,779 →
    **39,888** (the two rounds together: −181 and +290) and the site figure 979,298 →
    976,349, the eleven pages' 3,058 and the script's 109. The page weight moves with the
    script and the stylesheet both, 267,095 → **266,926**: the docs hub inlines the stylesheet
    (−278) and fetches the script (+109). Nothing else in the artifact moved in that round —
    the console's link was behind a flag that build had off.
  - **The console's release turned the flag on** (W3.1), and moved the site figure alone: the
    console page's downloads section, Getting Started's console option and the hero's link to
    the console. The page weight and the script did not move: the heaviest page is the docs hub,
    which renders none of the three. The bytes are the ones the three figures below are derived
    from, and `test/budget.mjs` measures them; the rule is what this list carries.

  - **Getting Started's three bands** (the owner's feedback of 2026-10-19) moved the site
    figure alone, 977,921 → **979,575**, by the **1,654 bytes** the rework costs: the install
    section shows the quick path above the fold with the console option beside it, the page
    carries a "Set up your installation" list of its own, and the other install paths and the
    platform list moved into a Details section below them. The page weight does not move: the
    heaviest page is still the docs hub, which renders none of it, and the page that grew is
    Getting Started's, at 25,871 bytes.

  - **The 0.9.0 integration branch merged into `main`** (#697) and moved the page weight and
    the site figure, with the script where it was: it regenerated
    `website/content/cli-reference.md` for the 0.9.0 CLI and extended `content/security.md`.
    The docs hub renders the whole CLI reference, so the heaviest page went 266,926 →
    **280,604** (+13,678), and the site figure 979,575 → **1,019,827** (+40,252: the docs hub
    and the security page). Getting Started's rework above was derived on a branch cut before
    that merge, which is why the figures it wrote named a build `main` no longer makes; this
    re-derivation is the commit that moves them. The clone figure moved by the same bytes, the
    changelog page between the two shapes being unchanged.

  - **The console's screenshot slider** (the owner's feedback of 2026-10-19) moved the site
    figure alone, 1,019,827 → **1,020,942**, by its **1,115 bytes**: the component's
    stylesheet, which Astro inlines into the console page. It is inlined whether or not the
    slider renders, because the page imports the component — so the bytes are there while
    the data file is empty and while it is not, and the figure does not move again when the
    owner adds a screenshot (a picture's own bytes are served from `public/` and are the one
    thing that will move it then). The page weight and the script did not move: the console
    page is not the heaviest page, and the slider ships no script at all. The clone figure
    moved by the same bytes.

  - **The run under the picture became one vertical timeline** (the owner's feedback of
    2026-10-19), and moved the site figure alone, 1,020,942 → **1,021,991**, by **1,049
    bytes**. The five steps were a `repeat(auto-fit, minmax(18rem, 1fr))` grid of cards — three beside two on a desktop,
    two-by-two on a tablet — and the owner read the section and said it "does not look good".
    They are now one column at every width, each step a numbered marker on a rail joined by a
    one-pixel line, with the step's name and its line beside it: the page that renders them
    grows by the rail, the connector, the rule that stops it at the last step and the markup
    that places them, and the other ten pages, which render no run, are unmoved. The page weight
    and the script did not move either: the heaviest page is still the docs hub. The shape was
    held by `test/base-styles.mjs`'s `runTimelineFailures` — the layout in force at 360, 768 and
    1280, the connector's own extent and width, and every width or `nowrap` in the section
    against the phone's content box. The timeline those cases were written for is gone: the run
    under the picture is a diagram now, and `test/how-it-works.mjs`'s `runDiagramFailures` holds
    its shape instead, every line of the drawing measured against the box it is drawn in (the
    entry below). The clone figure moved by the same bytes.

  - **The caps-and-costs rework** (the owner's brief of 2026-10-19) moved the site figure
    alone, 1,021,991 → **1,023,326**, by **1,335 bytes**, on one page: the table of what a job
    costs and its provenance note, rendered from `website/data/typical-costs.yaml`, against the
    per-command scope sentences the page no longer carries and the `<details>` the commands now
    sit behind — and then the review of that rework, which added the scroll box the table's
    nowrap columns need on a phone and the page's own rule for its lead. The page weight and the
    script did not move: the heaviest page is the docs hub, and the caps page is not it. The
    clone figure moved by the same bytes.

  - **The changelog's own round** (the owner's brief of 2026-10-19) moved the site figure
    alone, 1,023,326 → **1,028,085**, by **4,759 bytes** net, and its causes are the page's own:
    each release now carries its product summary line and wraps the release file's own
    Highlights in a `<details>` block, so a summary and a notes label per release arrive, and
    the page no longer prints a "not yet released" label for every release in the checkout with
    no tags (which releases are published is the plugin's version now) or the "newest releases"
    block the summaries replaced. The page weight and the script did not move: the changelog
    page is not the heaviest page, and nothing outside it changed. It is also the round that
    made the clone figure the no-tags figure (above).

  - **The homepage's prompt line** (the owner's feedback of 2026-10-19) moved the site
    figure alone, 1,028,085 → **1,028,787**, by **702 bytes**: the request the hero renders under
    the headline, the `>` mark in front of it and the two rules that draw it as the chat's own
    bubble — the homepage's inline stylesheet and its HTML. Nothing outside the homepage moved,
    because the three files that changed all render on that one page, so the page weight and
    the script stand where they stood.
  - **The benefits** (the same feedback) moved the site figure alone again,
    1,028,787 → **1,030,935**, by **2,148 bytes**: the section's four names and four lines, its
    heading and its grid, and the scoped stylesheet rules that draw it — all of it on the
    homepage, which is the one page that renders it. The page weight and the script did not
    move: the heaviest page is the docs hub, and it carries no benefits. The words the section
    costs the homepage are what moved the word ceiling above, in the same commit.
  - **The pull request's caption** (the same feedback) moved the site figure alone again,
    1,030,935 → **1,031,001**, by **66 bytes**: the caption's own line and the `data-source` it
    cites with it, `docs/git-providers.md#Checkpoint pushes (git.pr.checkpoints)` in place of
    `README.md#How it works`, on the two pages that render the flow — the homepage and How It
    Works. The page weight and the script did not move: the heaviest page is the docs hub,
    which renders no flow. The caption is two words shorter than the one it replaces, which the
    homepage's word count below carries. (Two pages rendered the flow then; the round that put
    the run's own diagram on How It Works — the owner's draft of 2026-10-19, below — leaves it
    on the homepage, so the caption reaches one page now.)
  - **The benefits' block rule** (the review of round 6) moved the site figure alone,
    1,031,001 → **1,031,084**, by **83 bytes**: the section's gutter and the room below it are now
    declared in the component that renders the section, where they were declared on the page and
    reached nothing — Astro scopes a page's selector to the elements the page renders, so the
    section, rendered by `Benefits.astro`, never carried the page's scope. The page's rule now
    names the two blocks the page renders and the section's rule writes the three declarations
    out again, which is the whole of the 83 bytes. The page weight and the script did not move:
    the heaviest page is the docs hub, which renders none of this, and it is one page's inline
    stylesheet either way. The clone figure moved by the same bytes as each of these rounds.

  - **The privacy statement gained its software part** (the owner's request of 2026-10-19),
    and moved the site figure alone, 1,031,084 → **1,033,358**, by the **2,274 bytes** of that
    page's own markup, and the words ceiling below with it, 250 → **622**. The page is the one
    the App Store and Play listings link as a privacy policy, so it now carries two named
    parts — "The website", which is what it always said, and "The software", which covers the
    CLI, the console and the phone app — and the words the second part needs are what re-derive
    the ceiling. The page weight and the script did not move: the heaviest page is still the
    docs hub, and `public/scripts/site.js` is untouched.
  - **The phone app's reporting was written into that part** (the owner's request of the same
    day), and moved the site figure again, 1,033,358 → **1,034,706**, by the **1,348 bytes** those
    paragraphs add, with the words ceiling re-derived in the same commit, 622 → **842**: the
    page names Firebase Crashlytics and Firebase Analytics, states what is collected and what
    is never sent, the opt-in default and the way back, holds Google as the processor for the
    Fugaro project under Google's own policy (linked, not restated), and says that earlier
    builds send nothing. The page weight, the script and the number of files did not move.
  - **The deletion section was carved out from under it** (the same round, on review), and the
    page moved again, 1,034,706 → **1,035,063**, by the **357 bytes** the carve-out adds: the
    section that said "everything the software holds lives in your own accounts … removing the
    installation removes it" was contradicted by the reports above it, so it now names the
    reports as the exception, says nothing about them is the reader's to delete, says how the
    sending is stopped and where to ask about a report already sent — and the words ceiling was
    re-derived with it, 842 → **919**. The page weight, the script and the number of files did
    not move. The clone figure moved by the same bytes as each of these rounds.

  - **The security page was cut to three things** (the owner's brief of 2026-10-19), and it is
    the round this build's site figure comes from. The page rendered `SECURITY.md` byte for
    byte; it now renders three things and nothing else, and all three are the document's own:
    the policy's *In short* section as the page's summary, its *Reporting a vulnerability*
    section, and a link to the policy — which `website/test/security.mjs` holds it to, bullet
    for bullet and word for word. The summary is rendered and not written on the page, which is
    what D189 asks of every long page and what the first cut of this page did not do:
    re-deriving it from `SECURITY.md`'s own section moved the page to **18,183 bytes**, which
    is **62,524 bytes** below the 80,707 it weighed on `main` once the 0.9.0 merge had extended
    the policy, so the site figure falls 1,035,063 → **972,539**. The bullets after it name the rest of this build's figure. The
    page weight and the script did not move: the page renders neither the script's behaviour nor
    the docs hub, which is still the heaviest page.
  - **The new spacing gate found a row glued together** (`test/spacing.mjs`, added by the same
    review round), and the fix is the last 2 bytes of this build's site figure. The console
    page's download row puts each artifact's published name in a link and its SHA-256 in a
    `<code>` beside it, and the two are inline elements on two source lines — so the whitespace
    text node between them is what Astro's compression drops, and the row read "macOS
    .dmga434f3…". The space is written into the component now, one byte for each of the two
    assets, and the site figure is 972,539 → **972,541**. The gate reads every built page and
    takes a class the page's own stylesheet lays out as a block — the docs hub's `.path` — and a
    child of a container it lays out as flex or grid, which blockifies its children — the
    homepage's `.hero-actions` row — as a
    boundary a reader sees, so the seams the design makes on purpose are not refused; `test/spacing.mjs`
    measures that count over the artifact, and this list does not keep it.
  - **A cross-reference the document has no heading for renders as its own text** (the same
    review round). Every cross-reference in `SECURITY.md`'s "In short" is rewritten to a link
    into the document at the section it names, and one of them — `[Money](#money)` — names a
    bold lead in a bullet rather than a heading. Rendered as a link it read like every other
    cross-reference on the page and landed at the top of the file, a section away from what it
    named, so the page now renders that one as plain text: **71 bytes off the security page**,
    18,183 → **18,112**, and off the site figure, 972,541 → **972,470**. The page weight and the
    script did not move: the page changes no file every page loads, and the docs hub is still the
    heaviest page.
  - **A cross-reference into a section the page renders itself links the page** (round 5 of the
    same review). `SECURITY.md`'s summary points at "Reporting a vulnerability", and `/security/`
    renders that very section under that very id — but the page resolved every fragment against
    the document, so the one reference whose section is on the page sent the reader to GitHub for
    it, and the page's own `reporting-a-vulnerability` anchor was linked from nowhere. The page
    resolves a fragment it renders to its own anchor now: **56 bytes off the security page**,
    18,112 → **18,056**, and off the site figure, 972,470 → **972,414** (the document URL is 82
    bytes, the page's own anchor 26). The page weight and the script did not move.
  - **The spacing gate found a second row glued together once `main` came in** (the merge of
    `main`'s Getting Started rework into this branch): the "Set up your installation" step
    rendered "fugaro initin your own terminal.", the link ending one source line and the
    sentence starting the next. The space is written into the line now, **1 byte**, and the
    site figure is 972,414 → **972,415**. The page weight and the script did not move. The
    clone figure moved by the same bytes as each of the security page's rounds.
  - **The header was put on a layer of its own** (the owner's reading of 2026-10-19: "How it
    works: menu hidden by the blocks on mobile"), and moved **every page by the same 77
    bytes**: 972,415 → **973,262** at the site figure and 280,604 → **280,681** at the page
    weight. The whole of the move is the stylesheet every one of the eleven pages inlines — the
    scale's two tokens, the header's `z-index`, and the skip link's and the banner's moved onto
    the scale's names — and the script is untouched, because the fix is CSS and the script was
    not what covered the menu. That the eleven pages move by the same bytes is the check worth
    keeping: this round is the chrome, and the chrome is on every page. The clone figure moved by
    the same bytes.

  - **The console's own release moved to `console-v0.9.0`** (the owner's ruling of 2026-10-19,
    below): `docs/releases/v0.9.0.md` names the console's own release now, and that line is part
    of the changelog entry the page renders — the site figure is 973,262 → **973,481** (219
    bytes), and the two checkout shapes move together as one figure. The page weight and the
    script did not move: the heaviest page is the docs hub, which renders no changelog entry.
  - **The app's reporting is always on, and the app has no switch** (the owner's ruling of
    2026-10-19), and it moved the page once more. The phone app's paragraph
    said collection was off until the reader turned it on in the app's settings, and the deletion
    section said turning that switch off stopped the sending. The ruling leaves no switch: the
    versions that carry the reports send them, and the page says so — no switch inside the app to
    turn them off, uninstall the app or do not install it, and the command line tool and the
    console unaffected because they carry none of these reports — and the deletion section now
    says uninstalling the app stops the sending. That clause names the two tools and not the
    website on purpose: the site's own measurement is the page's first part, under the consent
    that part states, so a software-part sentence calling the website unmeasured would be the
    page contradicting itself, and the flag that decides that part is not this list's to read.
    That is **138 bytes onto the page's own markup**, 973,481 → **973,619**, and the
    words ceiling re-derived with it, 919 → **949** (the page's own 759 words plus 25 %, below).
    The page weight, the script and the number of files did not move: the heaviest page is still
    the docs hub, which renders none of this, and the clone figure moved by the same bytes.
  - **The benefits' points got their icons and a step up the type scale** (the owner's feedback
    of 2026-10-19: "make them bigger, bolder, use an icon for each point"), and it moved the
    site figure alone, 973,619 → **975,034**, by **1,415 bytes**: one inline `<svg>` per point —
    four glyphs, drawn with `stroke="currentColor"` and the presentation attributes that go with
    it — and the rules that size and place them, and the section's heading, its point names and
    their one line each a step up the type scale. All of it is on the homepage, which is the one
    page that renders the section, so the page weight and the script did not move: the heaviest
    page is the docs hub, and it carries no benefits. Nothing was fetched for any of it — the
    four glyphs are shapes in the markup, and `test/home.mjs` reads each one back off the built
    page and refuses an icon that references a file or an origin. The words are the ones the
    section already carried, so the word ceiling did not move.
  - **The CLI's `--repo` gained its bare form, and the two figures it moved were left
    un-derived** (the round of 2026-10-19; this round repairs it). `fugaro budget set --repo`
    with no value means the checkout's own repository now, and the command tree's usage strings
    changed with it — `--repo R` → `--repo [owner/name]` on `budget set`, `budget kill` and
    `budget resume`. The site renders those strings in two places, and nothing is fetched for
    either: **1,060 bytes onto the docs hub**, whose page carries the whole generated CLI
    reference, and **66 bytes onto caps-and-costs**, which quotes the three usages under its
    commands — **1,126 bytes onto the site figure**. Neither was re-derived in that round, and
    the round after it (How It Works, below) added its own 6,510 bytes to the figure stated in
    this list rather than re-deriving from the build — 975,034 + 6,510 is the 981,544 it wrote,
    while the build was 1,126 bytes larger — so the shortfall survived it and is paid in
    the logo round at the end of this list. Both figures are read off the gate's own build now,
    which is the rule (D192).
  - **How It Works is one diagram of the run, and the run in words under it** (the owner
    approved the draft "How it works, draft 2 (diagram)" on 2026-10-19 with "build it", and
    item P8's step-by-step timeline came out). It moved the site figure alone, 975,034 →
    **981,544**, by **6,510 bytes**: one inline `<svg>` per width — the run read left to right,
    and the same run read top to bottom below the site's 48 rem breakpoint, both drawn from
    the same labels in `RunDiagram.astro` — the box, pill, arrow and band rules that paint
    them from the palette's tokens, and the two lists of the repository's own lines under the
    picture. Both drawings are markup in the page and nothing is fetched for either; the page
    weight and the script did not move, because the heaviest page is the docs hub, which
    renders none of this, and `public/scripts/site.js` is untouched — the page loads the one
    script every page loads (the JavaScript budget below), and the drawing is markup rather
    than script. The words are the diagram's own labels and the page's remaining
    facts, so the word ceiling did not move either.
  - **The diagram's changeover, its ceiling and its arrow** (the review of the round above).
    The review read the drawing the way a reader meets it, and found three things the round's
    own gate did not hold. It moved the site figure alone, 981,544 → **982,707**, by **37
    bytes** — the rules that say where the two drawings change over and how wide the stacked
    one is drawn. The two drawings change over at **59.25 rem** now rather than at the site's
    48 rem chrome breakpoint: the wide drawing's smallest label is 16 units of a 1200-unit
    picture, so at a 768 px viewport the column scaled it to 9.6 px, and a label a reader
    cannot read is the whole reason the stacked drawing exists. The stacked drawing carries a
    ceiling of its own scale — 22.5 rem, one unit to the pixel — so a tablet is shown the
    phone's picture rather than that picture stretched to the column, and
    `website/test/how-it-works.mjs` holds that ceiling, the changeover and the floor at the
    column the page's own stylesheet gives each drawing, and reads which *side* of that width
    each rule holds on rather than only the width: `(width<=59.25rem)` and `(width>=59.25rem)`
    name the same width and show the drawings to opposite readers, so a changeover read from
    the wrong side — the stacked drawing on the desktop, the shrunk wide one on the phone the
    floor exists for — is refused too. The same file holds the arrow itself
    now: the line and the head that lands it, drawn at all, joining `review` and `implement`
    rather than two other stages, and the head at the end of the line that lands on
    `implement`, pointing the way the line runs — the head is a triangle and its own three
    corners say which way round it was drawn, because a mirrored head is the same triangle over
    the same two x's and its box cannot tell the two apart. Before, the gate refused only an
    arrow drawn across a stage pill, so deleting the arrow from both drawings left it green,
    and the head the component drew was mirrored: it sat on the right pill pointing back up
    its own line, at the stage the run came from. The page weight and the script did not move:
    the rules are scoped to the component, which one page renders, and the script is
    untouched.
  - **And the round re-derived the three figures from the build rather than from its own
    delta**, because the base the numbers carried was stale. `website/content/cli-reference.md`
    was regenerated in the round that landed `fugaro budget set --repo` and the figures were
    not re-derived with it (D192's rule), so the build the README named was 1,060 bytes under
    the docs hub it calls the heaviest page and 1,126 bytes under the site figure's build. The
    page figure's build moves 280,681 → **281,741** — the regenerated reference, which the
    docs hub renders and which this round's own change does not touch — and the site figure's
    981,544 → **982,707**, the 1,126 bytes plus this round's 37. Both figures are what
    `website/test/budget.mjs` measures in the gates' own build, which is the build the numbers
    are derived from (above); the script's figure did not move, so it was not re-derived.
  - **The logo's source became the owner's 2048 px one, and every derived icon was regenerated**
    (2026-10-20). The owner supplied `fugaro-logo-large.png`, 2048 × 2048, 8-bit RGBA on
    transparency: the same artwork with twice the real pixels, so the 1024 px app icon — and the
    console's byte copy of it — is no longer an upscale of a smaller source (`brand/README.md`
    and `console/README.md` carry the account, and `test/accent.mjs` re-pins the facts about the
    file: the canvas, the counts and the circular mean hue, which moved 198.4° → **199.0°**).
    Every file `scripts/gen-brand-icons.py` writes moved with it, and the two byte figures moved
    in opposite directions. **The whole site went 982,707 → 972,362, by −10,345 bytes:** the icons
    the site serves are **10,345 bytes lighter** in total — the three Android icons and the
    apple-touch icon are most of it, the three favicons are 89 bytes heavier, and the header's
    `logo.png` is 17 lighter. **The page weight went 281,741 → 281,813, by +72
    bytes:** the docs hub fetches the three favicons (89 bytes heavier) and the header logo (17
    lighter), so its share of the move is +72, on top of the 1,060 the CLI reference had
    already added to it. The script did not move — no page's JavaScript changed, and
    `public/scripts/site.js` is the same 39,888 bytes — and neither did a word of any page: no
    markup and no stylesheet changed, so the two word ceilings did not move either, and the count
    of files in `dist/` is 23, as it was.
  - **The 0.9.5 line's CLI reference** (the first sync of `main` into `integration-0.9.5`; its
    figures are that line's build, before the second sync below): the
    generated `content/cli-reference.md` gains the 0.9.5 commands — `fugaro backends ls`,
    `fugaro project sync` and `fugaro project repos` — and that moved the page weight and the
    site figure by the same **7,187 bytes**, 280,681 → **287,868** at the docs hub (the
    heaviest page, which renders the whole reference) and 975,034 → **982,221** at the site.
    The script and the number of files did not move, and the clone figure moved by the same
    bytes.
  - **The 0.9.5 release notes** (`docs/releases/v0.9.5.md` and its line in
    `data/release-summaries.yaml`): the changelog page gains the 0.9.5 entry, its summary and
    its collapsed Highlights, and that moved the site figure by **7,976 bytes**, 982,221 →
    **990,197**. The changelog page is not the heaviest page, so the page weight did not move,
    and neither did the script or the number of files; the clone figure moved by the same bytes.
  - **The second sync of `main` into `integration-0.9.5`, re-derived from the gate's own build**
    rather than from the deltas above, which were each measured on one line before the two
    met. The merged build carries `main`'s rounds (the `--repo` usages, How It Works and the
    regenerated icons) and the 0.9.5 line's (the 0.9.5 CLI reference, and the 0.9.5 release
    notes on the changelog page, with the price catalog's line and its known limit), and it
    measures **289,000 bytes** at the docs hub, still the heaviest page (281,813 on `main`, so
    **+7,187**, the 0.9.5 reference), and **987,954 bytes** for the whole site (972,362 on
    `main`, so **+15,592**). The script and the number of files did not move, and the
    clone figure moved by the same bytes.
  - **The caps page's figures became labelled rows on a phone** (the owner's feedback of
    2026-10-19: "these render terrible on mobile"), and moved the site figure alone,
    973,619 → **974,846**, by **1,227 bytes**, on one page: below the site's 48 rem breakpoint
    each row of both figures tables is a block, each cell a line the stylesheet labels with its
    column's own header through `content: attr(data-label)`, and the wide layout's own fit —
    the nowrap figures and the box that scrolled around them — moved inside a `min-width:
    48rem` block, with the stacked figures set at `--font-size-base` rather than `base.css`'s
    smaller table size. The page weight and the script did not move: the heaviest page is the
    docs hub, which renders neither table, and the caps page's own growth is nowhere near it.
    The clone figure moved by the same bytes. The shape is held by `test/caps-figures.mjs`
    (above), whose reading run against the old component fails on every defect it is written for:
    the cells unlabelled, the five stacking declarations missing, the head row left in the flow,
    no label rule reaching the page's cells, the wide layout's nowrap figures and scroll box in
    force at the phone's width, and the figures at the table's smaller type.
  - **The console page was cut to its essentials** (the owner's feedback of 2026-10-19: "way
    too much text, nobody cares"), and moved the site figure alone, 974,846 → **974,047**, by
    **799 bytes off** one page. What a reader gets now is one sentence on what the console is,
    the three properties with one line each, and the downloads: the page's facts come from
    `content/downloads.json` and the four sentences there are shorter, and the page's own
    markup is unchanged — the shorter sentences are the whole of the move. The numbers did not
    move, and that is the check worth keeping: the two macOS artifacts, their published names,
    their SHA-256 values and the tag they are derived from are the same values, and the page
    still says in the data file's own words that Windows and Linux builds do not exist yet.
    The page's words went 239 → **133** against the ceiling the budget states, which is the
    room "Adding a console screenshot" below counts. The page weight and the script did not
    move: the heaviest page is the docs hub, and the console page is not it.
  - **JavaScript per page:** 149,580 bytes. Three times 49,860: the one script the site ships,
    `public/scripts/site.js` (39,888 bytes), plus 25 %. What the file carries is the copy
    control, the menu and the consent gate above, in the detail `test/nav-hover.mjs` holds it
    to: the hover behaviour is bound only where `matchMedia` says the device's pointer hovers
    and only for a pointer that says it is a mouse or a pen — a tap sends a pointer of its
    own, and a finger never hovers, so a touch screen keeps the tap it has always had — and
    the answer is watched rather than sampled once, so a pointer plugged in or taken away
    after the page has loaded is answered too. The keyboard's focus opens a group so Tab
    reaches the links inside a closed one; the focus a click or a tap moves opens nothing,
    because the click that brought it is the `<summary>`'s own toggle and would close what the
    focus had opened; and a group the browser closes because its sibling in the `name` set
    opened hands the reader's focus back to its trigger — while a close the reader asks for
    from outside the menu hands nothing back, because a press on the page's own text moves the
    focus to the body without a `focusin` to hear, and the trigger of the menu just dismissed
    is not where the reader is. A group has two readers, and the pointer is only one of them:
    the close a pointer's departure owes is owed when nobody is left reading, so a keyboard
    reader standing in the group holds it open and the close lands when the focus goes — and a
    focus that arrives inside the `CLOSE_AFTER_MS` the close waits cancels it. That keyboard
    half is bound whatever the device answers, because the keyboard is the device's and not
    the pointer's: a phone or a tablet with a keyboard attached answers the hover query no,
    and the menu still has a second level Tab has to be able to enter. Every page loads the
    file once, so it is the whole of a page's JavaScript, and the test keeps a case that says
    so over the built artifact.
  - **Page weight per page:** 1,083,750 bytes. Three times 361,250: the heaviest page, the
    docs hub, at 289,000 bytes (its HTML — the whole generated CLI reference — plus the
    files it fetches: the script above, the logo and the icons), plus 25 %.
  - **Whole site:** 3,704,829 bytes. Three times 1,234,943 (every file in `dist/`, 987,954
    bytes across 23 files, plus 25 %) — the gates' own build of the site, which every checkout
    now builds alike, tags or no tags (above).

  Two of the numbers are **not** bytes, and they are the redesign's: the brief's core
  principle is "the gist above the fold on every page with very few words", which no byte
  budget can hold (D187, D188). The same file reads them out of this list, and the same
  rule applies — a task that needs more raises the number here, in the commit that needs
  it:
  - **Words in a page's lead:** 40 words. The lead is the visible text of `main` between
    the page's `h1` and its first `h2` — the paragraph a page opens with before its first
    section — and it is a lead and not an introduction: 40 words at most, on every page,
    the three reference pages included, because a lead is the page's own whichever page it
    is. **The region between the two headings is the whole of the definition, wherever each
    heading sits** (P5; P4's reviewer read the homepage's lead two ways, and the two
    readings are the same number only while the blocks between the `h1` and the first `h2`
    are inside the `h1`'s own container — on the homepage as it was, with its install
    commands and its chat between the hero and the flow's first step heading, they were 24
    words from that container and 134 from the plan's region, and the stricter one is the
    one enforced). On the homepage today the first `h2` is the chat's own *Through your
    agent* — the chat is the first block under the promise — and the region is the request
    the hero renders under the headline, the promise and its two calls to action: 23 words. A
    page that carries blocks between its `h1` and
    its first `h2` is a page whose lead those blocks are part of, so the fix for a long lead
    is a shorter lead or an earlier first `h2`, never a reader that looks somewhere else. A page
    with no `h2` at all has no such region, and its lead is the paragraphs that follow its
    `h1`. A page whose `h1` is followed straight by its first `h2` has no lead region
    either, and 0 words is inside the ceiling: `/security/` is shaped that way (its `h1` is
    the page's own title and its first `h2` is the model summary's, with nothing between them)
    and so is
    `/changelog/` (whose summary is its first section, under the heading that names it).
    The ceiling is a ceiling, not a floor — a page with no lead region is a page D188's
    definition does not describe, and this gate says nothing about how much a page's first
    section should say.
  - **Words per page:** 949 words. Every word a reader sees inside `main` — command blocks
    and code spans included, since the ceiling is a claim about the page rather than about
    its prose — counted by `budget.mjs`'s own reader, which reads a page the way
    `test/lib/html.mjs`'s `textOf` does (a script or style body is not text, an image's
    `alt` is) and adds the one rule `textOf` does not carry: a subtree carrying `hidden` is
    not text either. No built page hides anything inside `main` today — the layout's own
    `hidden` elements, the skip link and the consent banner, sit outside it — so that rule
    is what stops a page that starts hiding a control from being counted for words nobody
    sees. `/docs/`, `/security/`
    and `/changelog/` are exempt, which is D188's own list: `/docs/` and `/changelog/` render a
    document of this repository, and `/security/` renders two sections of the policy document
    and links to the rest of it, so the three carry text the repository wrote rather
    than a page of the site, and the brief allows those to stay long. The exemption is the
    owner's and this change did not touch it: the page is much shorter than the document it
    used to render whole and is still well over this ceiling, so it stays on the list — and
    `test/security.mjs` holds it to a ceiling of its own. How many words the page carries is
    `budget.mjs`'s own reader's to say, over the built artifact; this list does not keep the
    count, and the figure it kept here once did not match what the reader measures.
    **The number is no longer D188's own, and it is derived rather than picked.** The
    privacy statement is the page the App Store and Play listings link as a privacy policy,
    so it carries the software as well as the site and is the one page that is not the
    site's pitch; the number is that page's own count on the gates' own build — **759
    words** — plus 25 %, rounded up, which is **949**. `budget.mjs`'s
    `wordsCeilingFailures` holds the derivation to the built page the way the same file
    holds the byte budgets to theirs, so a sentence added there re-derives this line in the
    commit that adds it. **The raise is that page's and nobody else's**: every other page of
    the site stays within D188's own number, and the same function holds them there, so this
    number is a ceiling one page reaches and not a licence the rest can grow into. (D188's
    number was itself set against the site as it was measured before the redesign —
    `docs/website/redesign/reconciliation.md` §6: faq 493 words, caps-and-costs 487,
    getting-started 431, privacy 277, community 268, every one of them over it — and the
    redesign brought every one of those pages but the privacy statement inside it.)
    **D188's number was 250, and the owner's feedback of 2026-10-19 is what moved it to
    300.** That brief asks the homepage for a Benefits section, which is a fourth required
    block on the one page that already carried the three the briefs before it require — the
    promise, the chat (changes spec item 3) and the flow (item 4) — and the four together
    measure 294 words. D188's number is therefore the smallest round ceiling above the page
    that carries every block its briefs require, and it is still a ceiling rather than a
    target: the highest of the pages that gained nothing from that brief is Getting Started at
    250 words, and a page that wants the extra 50 has to carry a brief's own block for them.
    That raise is recorded as a reading in `docs/website/redesign/README.md`, the way every
    number the briefs do not spell out is.
- `test/fold.mjs` — the ordering the fold claim rests on (P5, RF5). Whether the promise and
  the first command fit a real 390 × 844 first screen is the owner's live check (70b) and the
  word ceilings' business, and no test here sees a viewport (D187). The ordering underneath it
  is a statement about the built pages, though, and it is this file's rules: the homepage's lead
  region holds four elements — the promise, the row of links under it, and the two links in that
  row — and the `h2` it stops at is the chat's own *Through your agent*: the illustration the
  page puts directly under the promise is that heading's own block, sitting between the `h1` and
  the `h2` that opens it, so the gate takes those elements out of the region before reading it.
  The chat, the diagram and the install block are the sections that open after the lead region;
  Getting Started's install section opens with its commands, with the
  sentence about them still there after the paths; and on every route that prints a command —
  three of the eleven today, and the other eight are skipped rather than asserted about — the
  first `h2` precedes the first `pre`. Its fourth rule is the owner's feedback of 2026-10-19:
  Getting Started is three bands in the order a reader reads them — `#install` (the command
  and the console), `#setup` (what a real installation needs, an `ol` whose every step names
  the document behind it), `#details` (everything else) — so the two things a reader came for
  cannot drift back down a page that grows. The whole gate is one function over one `dist`, so its
  refusals are kept the way the other gates keep theirs: a case writes a synthetic `dist` with
  each defect planted beside a page that keeps every rule, and each rule was mutation-proved by
  removing it from the function and watching that case fail.
- `test/meta.mjs` — the provenance-phrase gate (D186): a page must not explain why its own
  text exists, so D186's phrase list (`rendered from`, `generated rather than written`, …) is
  refused in the text a reader reads, on every built page. It is the one defect the other
  four cannot see — a sentence about the build is short, well-formed and honest — and it is
  the sentence the redesign removes. Naming a document is not provenance text: the docs hub
  names each guide's file, a page may link to `SECURITY.md`, and the changelog renders the
  release files' own lines, which is why D186 chose an explicit list over a rule about
  naming a repository file. It holds a second rule of the same kind, because a phrase list
  cannot hold it: **no page may show a generated file's banner as text.** The generated files
  under `content/` — `cli-reference.md` and `security.md`, both `go run ./internal/clidoc`'s
  output; `downloads.json` beside them is hand-kept and carries no banner — open with
  `<!-- generated by …; do not edit -->`, W1.3's pipeline reads the banner as a fact and
  drops it from the body it renders, and RF8 takes "the banner is never on a page"
  as a premise — which was false on the docs hub, where the rendered CLI reference carried it
  through the markdown compiler and the page's own `escapeRawHtml` showed it to a reader as
  `<!-- generated by go run ./internal/clidoc; do not edit -->` above the reference's title.
  The gate refuses the escaped banner and ignores a comment node, which is the difference
  between a line a reader reads and a note in the markup.
- `test/wording.mjs` — the retired word (changes spec of 2026-10-18, item 2): `worker` and
  `workers` are refused in three places — every built page's text and head, the site's own
  sources (the four directories `siteSources()` walks: `src/`, `data/`, `public/` and the
  generated documents under `content/`), and `README.md`. A run's unit of work is a **job** in
  the product's own vocabulary, so the word is retired rather than replaced one sentence at a
  time, and the gate is the reason no page can quietly reintroduce the old tagline. Three
  pages are exempt — `/docs/`, `/security/` and `/changelog/`, D188's own list — because each
  renders a document of this repository rather than a page of the site. The list is written
  once, in `test/lib/exempt.mjs`: this gate skips those pages, `budget.mjs` exempts them from
  the whole-page word ceiling, and `wording.mjs` holds the set equal to the pages D188's row in
  `docs/website/redesign/reconciliation.md` names, so the exemption cannot widen without
  failing a case. **The generated CLI reference still carries the old wording**, and that is
  the item's ruling rather than a miss: the tagline change reaches the CLI's help, the
  generated reference and the three plugin skills that carry it (`routing`, `working` and
  `parallelism`) in 0.9.0, not here.
- `test/spacing.mjs` — the site's words are separated by spaces (round 3 of the security page
  review). Astro's HTML compression drops a text node that is nothing but whitespace, so an
  inline element written at the start of a source line sits flush against the word before it and
  a reader gets `back andfugaro secrets` where the page meant `back and` + `fugaro secrets` —
  a defect the source does not show and a reader cannot report without the word being quoted
  back. The gate reads every built page the way a browser does, through the shared
  `runTogether()`/`verbatimText()` in `test/lib/html.mjs`: each direct text child is its own run,
  nothing is invented between two neighbours, and a run ending in a letter directly against one
  starting with a letter is one word, in either case (`back andfugaro secrets` and `see
  thePrivacy` are the same defect). What counts as a boundary a reader sees is read from the
  artifact rather than listed by name: a block element, any class the page's own inlined
  stylesheet lays out with a block-level `display` (the docs hub's `.path` under a guide's title
  does today), and any element inside a container that stylesheet lays out as flex or grid,
  which blockifies its children (the homepage's `.hero-actions` row does today) — because without
  them the gate would refuse the seams the design makes on purpose, and a gate that cries wolf is
  a gate somebody turns off. How many
  seams the design makes and how many the rule refuses are `test/spacing.mjs`'s to measure over
  the artifact; this list does not keep the count. It
  found one real defect when it landed: the console's download row put each artifact's published
  name and its SHA-256 in two inline siblings on two source lines, so the row read
  "macOS .dmga434f3…"; the space is in `src/components/Downloads.astro` now. The refusal is
  mutation-proved both ways, and the last cases prove each boundary is read from the page: a
  seam the stylesheet lays apart is not refused, and the same seam is refused the moment the
  stylesheet lays it out inline, for the block class and for the flex row alike.
- `test/nav-hover.mjs` — the menu's hover behaviour (the owner's brief of 2026-10-18, item 1).
  A `<details>` hides its own closed content in a way author CSS cannot portably override, so
  opening a group on hover is the script's work and not the stylesheet's: the gate runs the
  shipped `public/scripts/site.js` in a small stub DOM (no browser, F3), on a pointer that
  hovers and on one that does not, and holds both halves of the pair — the CSS's every
  `.site-nav` `:hover` rule inside `@media (hover: hover) and (pointer: fine)`, and the
  script's `matchMedia` asking the same query before it binds anything and again whenever its
  answer changes. It opens a group by moving a mouse onto it and by moving a pen onto it, keeps
  it open while the pointer is inside the submenu, closes it one delay later when the pointer
  leaves and cancels that close when the pointer returns, still opens and closes by the click
  the menu was built on, leaves the touch path with no hover behaviour at all — a finger's tap
  opens and closes a group even on a device that answers the query "yes", where the tap's own
  pointer would otherwise be taken for a hover — binds and unbinds the behaviour when the
  device's answer changes under a loaded page, opens on the keyboard's focus and closes on
  Escape with the focus given back, reads `aria-expanded` at every step, opens a closed group on
  the first click even though the press moves the focus to the trigger (a group opened by that
  focus would be closed again by the click that brought it), and hands a keyboard reader's focus
  back when the browser closes the group it was standing in because a sibling in the same `name`
  set opened — while a click on the page's own text, which closes the menu the reader is walking
  away from, hands nothing back: the press moves the focus to the body without a `focusin` to
  hear, and the trigger of the menu just dismissed is not where the reader is. A group has two
  readers, and the pointer is only one of them: the close a pointer's departure owes is owed when
  nobody is left reading, so a keyboard reader standing in the group holds it open and the close
  lands a moment after the focus goes — and a focus arriving inside that moment cancels the close
  the pointer asked for. The keyboard's way into a group is bound whatever the device answers,
  because the keyboard is the device's and not the pointer's: a phone or a tablet with a keyboard
  attached answers the query "no" and still has a second level Tab can enter. And the guard that
  keeps a close from reopening the group it handed the focus back to lasts for that focus and no
  longer: a reader who takes the focus out of the menu altogether and comes back to the same
  trigger opens the group again, as arriving at it the first time did. Each case was
  mutation-proved by hand: the defect was planted in the script or the stylesheet, the case was
  watched failing, and the defect was removed. The gate's rules are also
  one function over one script source (`menuFailures`) and one over one stylesheet
  (`hoverScopeFailures`), so its refusals are kept the way the other gates keep theirs: a case
  plants each defect in the shipped script — a pointer arrival that opens whatever arrived, an
  answer sampled once at load and never again, a close that waits no time, a control that never
  says what it is, a focus a press moved that opens the group, a browser's close nobody answers, a
  device that answers the query "no" getting the behaviour anyway, a close the reader asked
  for answering with the focus, a pointer whose close lands under a keyboard reader standing in
  the group, a close the reader arriving inside the delay does not cancel, a keyboard's way into
  a group bound with the pointer's hover so that a device with no hovering pointer has a menu Tab
  cannot enter, and a guard against reopening that outlives the focus it was written for — and in
  the stylesheet, and watches the named refusal come back.
  Each of those rules was then mutation-proved the other way: removed from the function, with the
  refusal it names watched failing.
- `test/privacy.mjs` — the privacy statement's software part, and the seven rules that keep it
  honest (the owner's request of 2026-10-19; the app's reporting is always on by the owner's
  ruling of the same day, with no switch inside the app). The page is the one the App Store and Play
  listings link as a privacy policy, so it now says what the CLI, the console and the phone app
  do as well as what the site does, and this file holds each of those sentences to the thing
  that makes it true rather than to a copy of the sentence:
  - **the two parts** — "The website" and "The software" as the page's two `h2`s, in that order,
    because the store reviewer's part is a part of the page and not a subsection of the site's —
    **and as siblings at the same level**, since a part nested inside the other is a page whose
    two scopes cannot be read, which `consent.mjs`'s own site-part reader refuses for the same
    reason;
  - **the app's reporting, held in both directions** — the page names Firebase Crashlytics and
    Firebase Analytics, so the file refuses that naming unless the repository holds the plan for
    it (`docs/plans/2026-10-19-mobile-telemetry.md`) or `mobile/package.json` already carries the
    two packages, and it refuses either package being declared while the page stays silent. A
    page that names a reporter and says nothing about which version of the app carries it is
    refused while the reports are still only planned, which is why the paragraph ends on
    "Earlier builds of the app send nothing at all." — the sentence a later task replaces with
    the plan's existence. The paragraphs' claims are each a sentence the file looks for: what is
    collected, the ban list, the app's versions carrying the reports and their having no switch
    to turn them off, what a reader can do instead — uninstall the app or not install it — the
    command line tool and the console carrying none of these reports (the clause names those two
    and not the website, whose own measurement is the part above it and is the flag's to state),
    Google as the holder, and the link to Google's own Firebase policy rather than a restatement of it — and
    so is the section a reader deletes an app to find. **"Deleting your data" is read on its own**
    (from its heading to the next one) and has to say that the reports are the exception: the
    Fugaro project, not the reader, holds them, deleting the app does not take them out,
    uninstalling stops the sending, and the issue tracker is where to ask about a report already sent.
    Without that, the page said both "everything the software holds lives in your own accounts"
    and that the app reports to a project the Fugaro project operates, and nothing held the two
    apart;
  - **no advertising identifier, and no undisclosed reporter** — every executable file, manifest
    and lockfile under `mobile/` is scanned for the analytics, crash-reporting and advertising
    SDKs and for the platform's advertising-identifier APIs, all of them named in the test. The
    app's own two reporters are admitted by name rather than left in the scan's list, so the page
    and the scan agree about what the app carries; a task that added a third fails the sentence
    rather than letting it quietly become false;
  - **the phone's two credentials and where they live** — `SECRET_ITEMS` in
    `mobile/src/accounts/secret.js` must be D111's `idToken` and `macKey` and no more, the page
    must name those two, secure storage, the Keychain and the Keystore, and the tuple field
    `firebaseProjectId` is what makes "your own Firebase project" true;
  - **the update check** — read from `internal/updatecheck` (GitHub's public "latest release"
    endpoint for THIS repository, a 24-hour cache) and from the two switches `internal/cli` and
    `internal/localcfg` define, so a page that stops naming a switch, a check pointed at another
    repository or a shorter cache each fails;
  - **the host the project operates** — no production source under `cmd/`, `internal/`,
    `console/src/`, `mobile/src/` or `shared/` may name the site's canonical host, since the page
    says the software talks "to nothing the Fugaro project operates" (the one file that names it
    is a Go test whose fixtures test the boundary rule itself, recorded in the test so the
    exception stays visible);
  - **the contact route and no address** — the contact section links the issue tracker and
    `SECURITY.md`'s reporting route and nothing else, and the page carries no e-mail address, by
    the owner's ruling that there is no address to publish; and
  - **no terms page** — no built page links to `/terms`, and no sitemap the build produced names
    one, which is the owner's ruling of the same day written where the link would have gone.

  Each rule is one exported function with its own case, and each has a second case that plants
  the defect it refuses — a lost part, a part nested under the other, a dependency that collects,
  a reporter the page does not name, a page naming a reporter nothing in the tree holds, a third
  secret, an endpoint pointed elsewhere, a source naming the host, a third contact route, an
  address, a `/terms` link — and watches the named refusal come back. The word ceiling's own derivation — the
  privacy statement's own count plus 25 %, the number `website/README.md` states, and every
  other page of the site still inside D188's 250 — is `budget.mjs`'s, beside the reader that
  measures it and the byte figures it is a sibling of.
- `test/stacking.mjs` — the header paints above the page's blocks (the owner's reading of
  2026-10-19: "How it works: menu hidden by the blocks on mobile"). `tokens.css` names the
  site's two layers — `--z-header` for the header's row and the panel a group hangs below it,
  `--z-chrome` for the reader's own chrome above the page — and this gate reads them out of the
  **built** stylesheets: the header is positioned and carries the header's layer, which is what
  makes it a stacking context; the skip link and the consent banner carry the chrome's layer
  and nothing else does; and **no other rule declares a `z-index` at all**, which is the only
  thing a content component would need to paint over an open menu. The defect it was written
  for is the diagram: `ArchitectureDiagram.astro`'s steps are `position: relative` — that is
  what their arrows are placed against — and a positioned box with no `z-index` paints with
  every other such box in tree order, where the header is the first and a block drawn after it
  covers the menu on the two pages that carry the drawing. `base.css` gives `.site-header` the
  header's layer instead, in one rule rather than one per component, and this gate keeps it
  there. Its refusals are kept the way the others keep theirs: a fixture page carries the scale
  and the three layered rules, and a case plants each defect — a header with no layer, a header
  a layer cannot apply to, a content block with a layer of its own — and watches the named
  refusal come back.
- `test/caps-figures.mjs` — the caps page's figures survive a phone (the owner's feedback of
  2026-10-19: "these render terrible on mobile", read on the page's right-hand part, the cost
  figures). The page's two tables — what a job costs and the aggregation's per-recipe ranges —
  were both read as tables at 360 px: the first inside an `overflow-x: auto` box, which is six
  nowrap columns scrolling sideways inside their own box, and the second with no box at all,
  whose minimum content width is what widened the document. Below the site's 48 rem breakpoint
  each row is now a block, each cell a line labelled with its column's own header text through
  `data-label`, and the label is printed by one rule (`content: attr(data-label)`) rather than
  written into every cell; the wide table's own fit — the nowrap figures and the scroll box
  around them — is scoped to `min-width: 48rem`, and the stacked figures are set at
  `--font-size-base`, because `base.css` sets every `table` at the table's smaller type. The
  gate reads the built page's own markup and stylesheet: every cell's `data-label` is its
  column's head text in its column's order, each of the five elements a stacked table is made of
  (the table, its body, its rows and both kinds of cell) is separately `display: block` at 360 and
  at 390 px with the head out of the flow, the rule that prints the labels is matched against the
  page's own cells — a `content: attr(data-label)` on a selector that reaches no cell prints
  nothing, and reads exactly like the rule that works — nothing in force at those widths holds a
  figure on one line or in a scroll box, no box is wider than `main`'s own content box at 360 px
  (read out of the stylesheet, not typed), the figures are no smaller than the body, and the two
  declarations the wide layout is made of are read again at 768 and at 1280 px, so a block that
  was deleted or moved behind a wider query is refused rather than green on every phone width.
  It reads the checkout's build and a copy built with a fixture `costs.json`, because the
  per-recipe table is the one this checkout cannot render (D96); the fixture case also plants a
  defect only that table's cells can show, so the second table is read rather than assumed. Its
  refusals are kept the way the others keep theirs: the reading run against `main`'s own
  `CapsCosts.astro` fails on every defect it is written for — every cell unlabelled, the five
  stacking declarations missing, the head row left in the flow, no rule printing a label, the
  nowrap figures and the scroll box in force at the phone's width, and the figures at the table's
  smaller type — and every refusal has a case that plants the one thing it names in the
  artifact's own bytes.

**Lighthouse is the owner-run check** (D105's targets: performance ≥ 95, accessibility 100,
best practices 100, SEO 100), run against the built artifact by hand: there is no CI on pull
requests and the tests here are hermetic, so a browser-driven audit is not one of them.

The site's other gate is the Go side, which holds every fact to the file it is derived
from:

```sh
GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'Docs|Lint|Website|CliRef' -count=1
```

**The `Website|CliRef` half is not decoration.** The plan's baseline for this suite is
`-run 'Docs|Lint'`, and that pattern selects **none** of the site's own pins: of the 53
tests it selects in `internal/cli`, none is named `TestWebsite*` or `TestCliRef*`, so a
run of it alone is green with every fact the site renders unexecuted. `Website` selects the
boundary test, the install pins and the security page; `CliRef` selects the reference's
pins, in `internal/cli` and in `internal/clidoc` where the generator's own tests live. The
names are the ones the docs-only guard in `ci.yml` already uses for the same tests.
`website/test/readme.mjs` holds this command to that: every test function the site's Go
test files declare has to be selected by the pattern this section prints.

## What is generated, and what must not be edited

The generated files under `content/` are **never edited by hand**: `cli-reference.md` and
`security.md` are generated documents and `costs.json` is the owner-run aggregation's output.
`content/downloads.json` is the one **hand-kept** file, marked `none: hand-kept` in the table,
and it is edited by hand. The directory holds three kinds of file and they say so in three ways:

- a **document** (`content/*.md`) carries a header on its first line naming the command
  that writes it, and is regenerated by a command run **from the repository root**;
- a **machine output** (`content/costs.json`) is the aggregation's `--json` verbatim, so it
  has no header to carry and no format to add one to — what makes it the aggregation's
  output is its shape, and it is absent until the owner runs the command.
- a **hand-kept** file (`content/downloads.json`) is not generated and has no command; the
  table's Regenerate cell says `none: hand-kept`, and `website/test/readme.mjs` lists it by
  name.

`website/test/readme.mjs` asserts that this table and those files agree: a file whose
header names a different command, a `content/` file this table does not name, a document
without its header, or a JSON file that is not the aggregation's output all fail the site's
check until this document says the same thing.

| File | Source | Regenerate with |
|---|---|---|
| `content/cli-reference.md` | the command tree, `internal/cli.NewRootCmd` | `go run ./internal/clidoc` |
| `content/security.md` | `SECURITY.md`, byte for byte below the title | `go run ./internal/clidoc` |
| `content/costs.json` | `fugaro stats runs --project <installation> --json`, and it is the aggregation's output verbatim | owner-run: `fugaro stats runs --project <installation> --json > website/content/costs.json` |
| `content/downloads.json` | hand-kept, not generated: the console's facts and the release's own published names, with `{version}` where the version goes — the URLs are derived from them (below), and `available` follows `site.yaml`'s `features.console_downloads` | none: hand-kept |

Three things about those commands are worth stating rather than leaving to the table:

- **One generator, two files.** `go run ./internal/clidoc` writes both generated documents
  from their sources; it compiles into the build cache and writes nothing into the tree
  besides its output.
- **The costs file is the owner's to produce, and it may be absent.** The aggregation reads
  one named installation's run records, is refused inside a coding agent's session, and
  prints percentiles per recipe with the window and the sample size behind each — never a
  repository, a run id, a branch, a task, a person or a commit (D96, D97). Until the owner
  runs it, the file is absent and the caps-and-costs page renders its "not enough runs yet"
  state: an empty figure is honest, an invented one is not.
- **`website/data/*.yaml` is hand-written, and pinned by tests rather than regenerated.**
  `install.yaml` is held to `README.md`, `docs/release.md` and `.goreleaser.yaml` by
  `internal/cli/docs_website_install_test.go`, and `site.yaml` to `go.mod`, the
  repository's decisions and the site's own host list by the boundary test. Change the
  fact in the repository, then the value here, in one commit. `site.yaml`'s
  `features.console_downloads` is held to the hand-kept `content/downloads.json`'s
  `available` by the build itself, which refuses the two disagreeing (below).
  `console-screens.yaml` is the third, and it is the owner's own list rather than a fact
  another file states: it ships empty, and the build renders whatever rows it carries (below).
- **`website/data/typical-costs.yaml` is the owner's own measurement, and the page renders
  it or nothing.** It carries what a job costs on this project's own development runs — one
  entry per kind of run, each with the sample behind it, over a window whose last day is the
  day the figures were taken — and the caps-and-costs page leads with it (the owner's brief
  of 2026-10-19). `src/content.ts` reads it, validates it (a version, a window of at least
  `site.yaml`'s `min_runs` runs, an ascending middle half, no duplicate id) and refuses it by
  name, and `test/caps-and-costs.mjs` holds the built page to it **in both directions**: every
  field the file carries is rendered in its own column, and no figure on the page is one the
  file does not carry. So a refresh is an edit to this file and never to a page, and a field
  added to it with no column to land in fails the build rather than going unrendered.
- **`website/data/console-sha256sums.txt` is the release's, copied rather than written.** It is
  the `console-sha256sums.txt` asset of the console's `console-vX.Y.Z` release, byte for byte —
  one `<64 hex>  <published name>` line per artifact, the shape `sha256sum` writes and
  `shasum -a 256 -c` reads. It is what the `sha256` values in `content/downloads.json` are held
  to: a checksum is a fact about bytes, so no build can compute it and a well-formed value is no
  evidence of anything. The file arrives with the console's release and the version bump beside
  it (below).

## The changelog is built, never committed

There is no changelog page file and no changelog JSON in this tree: `src/lib/changelog.mjs`
reads `docs/releases/vX.Y.Z.md` **during the build** and the changelog page (W1.9) renders
what it returns, with each release's product summary read from `data/release-summaries.yaml`
through the content pipeline beside it. Nothing is written to disk, so a file that lands on
`main` before its release leaves no stale copy behind — and no date is taken from a tag,
because a shallow checkout has no tags and the newest entry a reader sees has none yet
(D100).

**Which releases are published is a file in this repository, not a property of the
checkout** (the owner's brief of 2026-10-19: *"Changelog: why do I see not yet released?"*).
A release file is published when its version is at or below the version in
`plugin/.claude-plugin/plugin.json`. The bump lands on `main` before the tag does, so on
`main` the manifest names the newest released version and everything up to it reads as
released; a release file written ahead of its bump carries the "not yet released" marker, and
it is the only thing on the page that says so. `git tag --list` was the rule until this
change, and the deploy build checks out without tags, which printed the marker under every
release.

**The release files stay the operator's document.** Their Highlights bullets are written for
whoever runs Fugaro, so they are rendered as they are, inside a `<details>` block that opens
with no script (D183); the summary above them is the site's own line for that release, and a
release file with no entry in `data/release-summaries.yaml` stops the build naming the file
rather than rendering an entry with nothing under its heading. `website/test/changelog.mjs`
holds the generator to the files, and `website/test/changelog-page.mjs` holds the page to
both sources: every release file has a summary entry and none is empty, the page renders the
entry a release's own summary carries, and the page writes no sentence that is neither a
release file's nor a summary's.

## The console's downloads are derived, never written down

`content/downloads.json` is hand-kept and carries the console's facts, and the one rule about
it is the flag: `site.yaml`'s `features.console_downloads` is what the repository's own
boundary test reads (`internal/cli/docs_website_boundary_test.go`), and while it is false **no
file under `website/` may carry a download URL** (Global Constraint 9, finding 25). The data
file therefore carries the release's *published names* rather than a URL: `release.tag_prefix`,
`release.version`, and one `file` per asset with `{version}` where the version goes.
`src/lib/console-downloads.mjs` composes each URL at build time from those, from the tag
(`console-vX.Y.Z`, cut from the CLI's own `vX.Y.Z` tag) and from `site.yaml`'s `repository` —
that module is the one reader of the file, and each page renders what it returns.

Three places offer the console from those facts: the console page's own Downloads section
(W1.6), Getting Started's console option beside the CLI (the owner's changes spec of
2026-10-18, item 6), and the homepage's call to action (the owner's brief of 2026-10-19, item
6: the hero action row carries a `Desktop console` link to `/console/`, and nothing about the
console at all, rendered from `features.console_downloads`). In all three, the unreleased state
is **structural rather than editorial**: the download list is rendered from `available`'s
branch and nothing else, and the hero's link is rendered from the flag, so an unreleased
console has no link to follow even though its assets' names are already in the data file — and
the option's only link is the way to `/console/`, which is where the hero's link goes too.
`website/test/console.mjs` holds the console page to that, `website/test/home.mjs` holds the
homepage's call to action to the flag-off half (the row is exactly the two links it carried
before, and mentions the console nowhere), and `website/test/console-option.mjs` holds Getting
Started to it in **both** states: the shipped one, read off the built page, and the released
one, read off a build of a copy with the flag flipped. That second case also runs the budget gate over the flag-on build
(`FUGARO_GATES_DIST`), because the two-option page costs words and Getting Started was at the
ceiling D188 set when the console shipped — 250 words then, 300 since the owner's feedback
of 2026-10-19 moved it (above) — and the owner's flip has to be the whole change and may not be the
change that breaks the ceiling. A data file that says the console has shipped while the flag says it
has not stops the build, naming both files: they are one fact.

**The flip, in the site's own data, is four values:** `site.yaml`'s
`features.console_downloads: true`, `downloads.json`'s `available: true`, `release.version`
bumped to the CLI tag the console was built from, and each entry's `sha256` set to the value the
release's own sums file records for its published name. The download list itself does not change —
it already carries the release's published names — so no URL is typed anywhere and every button,
label and URL follows from those.

**A later console release moves two of those four again, and it has happened once:** on
2026-10-19 the console's own release moved to `console-v0.9.0` (the owner's ruling that
the site's console downloads follow the console's own release), so `release.version` and both
`sha256` values moved with it and `data/console-sha256sums.txt` was replaced by the published
`console-v0.9.0` asset, byte for byte. Nothing else about the section changed: the same two
artifacts, the same signing and notarisation, and the same derived URLs.

**The tag's prefix is checked in the build, not only in the gates.** `src/lib/console-downloads.mjs`
reads `CONSOLE_TAG="<prefix>$VERSION"` out of `.github/workflows/console-release.yml` and refuses a
`release.tag_prefix` the workflow does not compose (`website/test/console.mjs` plants the
disagreement and requires the refusal). A prefix of the right *shape* that no run composes is a tag
no release carries, and every button would 404 — the one failure the checksums, the documented names
and `console/README.md` all agree with a build that made it.

**It was not a one-line change, and four checks were written to say so** — each keeping the
unreleased state true, and each refusing the flip until the commit that made it extended it. That
commit is the one that turned the links on (2026-10-19), and it extended all four rather than
retiring any of them, so each refusal is still alive in the state the site now ships:

- `internal/cli/docs_website_boundary_test.go`'s `TestWebsiteLinksNoConsoleWhileTheFlagIsOff` used
  to fail on a true flag (`t.Fatalf`, *"this test has not been extended"*). It now scans in **both**
  states: while the flag is off no file under `website/` may carry a download URL, and while it is
  on the URLs the pages render are held by `TestWebsiteConsoleReleaseFactsAreComplete`, which
  checks the tag prefix against the workflow's own `CONSOLE_TAG="` line, the version against a
  release note that exists, each published name against `console/README.md` and against the
  release's sums file, and each checksum against that file's value. The URL's *reachability* is
  the one thing nothing offline can check, and it stays the owner's live check (69a).
- The same file's `TestWebsiteFactsMatchTheRepository` used to refuse a true flag; it now holds
  `site.yaml`'s `features.console_downloads` and `downloads.json`'s `available` to each other,
  which is the agreement the build refuses too.
- `website/test/console.mjs` guards its unreleased-state case with an assertion that `site.yaml`
  says `false`. The case is *about* the unreleased console, so it keeps its refusals alive on a
  **fixture**: it builds a copy whose flag and `available` are flipped back off and holds that
  build to the unreleased page, while the shipped-state cases read the real build.
- `website/test/console-option.mjs` does the same for Getting Started's console option, and its
  flag-disagreement case now flips whichever of the two facts the checkout does not state, so the
  test is about the disagreement rather than about a direction.

One more thing the flip did **not** have to touch, and this is the point of deriving the URLs:
nothing under `website/src/` or `website/content/` changed beyond those values, and no test needed
a new expectation, because the released-state cases hold the built page to the
release's own documentation (`console/README.md`) and to the workflow that cuts its tag rather
than to a URL the test composed the way the build does. What the commit *did* add under `data/`
is the release's sums file, because a checksum is the one fact the release's names cannot imply.

The console is built for macOS and nothing else (`console/README.md`,
`.github/workflows/console-release.yml`: one universal `.dmg` and one `.zip`), which is why the
platform, and not an invented second one, is the word each button carries. Its release is a
**draft until the owner publishes it** (the workflow creates `console-vX.Y.Z` with `--draft`),
so the buttons land with the published release and not with the draft.

## Adding a console screenshot

The console page opens with a screenshot slider (the owner's feedback of 2026-10-19: *"Console:
we need a screenshot slider at the top"*). It is **data-driven and ships empty**:
`src/components/ScreenshotSlider.astro` renders the rows of `data/console-screens.yaml` and
nothing else, and that file's list is empty, so the page a reader gets today carries no slider,
no heading and no placeholder — the page as it was before the component existed. Nothing in the
build can make a screenshot: the owner takes them from the released console, on their own Mac
(D190), and **no screenshot is committed**.

Adding the first one is two edits:

1. **The file**, under `public/images/console/`. Create that directory with the first file:
   git carries no empty directory, and a placeholder in it would be a file the site serves and
   the budget counts. The name is letters, digits, dots, dashes and underscores; the extension
   is one the slider serves — `png`, `jpg`, `jpeg`, `webp` or `avif`. A capture from macOS's own
   `screencapture` is a PNG and is served as it comes; a WebP or an AVIF of the same picture is
   fewer bytes for the same pixels, which is the whole of the choice. An SVG is refused: it is a
   document that can carry script and references, and the site renders pictures.
2. **The row**, in `data/console-screens.yaml`, in the order the slider shows them:

   ```yaml
   screens:
     - file: runs-list.png
       alt: A list of runs with their stage, age and spend.
       caption: Every run, with what it is doing and what it has cost.
       width: 1440
       height: 900
   ```

   `file` is the file's **name** and not a path or a URL: the site path is derived from it at
   build time, so no file under `website/` carries a URL (Global Constraint 9). `alt` and
   `caption` are both required and neither may be empty: `alt` is what a reader gets when the
   picture does not arrive and what a screen reader reads instead of it, and the caption is the
   line under the picture. `width` and `height` are the file's own pixels, which is what lets
   the browser reserve the box before the file lands. `src/content.ts`'s `loadConsoleScreens()`
   refuses a name that is a path or a URL, a name with no extension at all, a format the slider
   does not serve, an empty `alt` or caption, a size that is not a positive whole number, and
   any key no reader reads — at the document's root or in a row, which is what keeps a URL out
   of the data file's own `src` line as well as out of `file`; every refusal names the row it
   refused, and the build stops.

**What a screenshot costs, against the three numbers the budget above states:**

- **The whole site:** the file is served from `public/`, so its bytes are in `dist/` and count
  once against the site ceiling the gates above state — several times the size of the build it
  is derived from, so a screenshot's size is a thing to keep an eye on rather than the thing
  that decides; `test/budget.mjs` holds the new build to that ceiling and asks for the figure to
  be re-derived in the commit that adds the file.
- **The console page's weight:** that page is a fraction of the page ceiling above, so it has
  room for the pictures it renders, and it is not the page the ceiling is derived from (the
  docs hub is).
- **The page's words.** The console page stands at **133 of the 949 words** the ceiling above
  allows it, and a row's words all count: the caption is text under the picture, and the `alt`
  is counted too, because it is the text a reader gets when the picture does not
  arrive (`test/budget.mjs`'s `visibleText` reads an `img`'s `alt`). The slider's own heading
  takes one more. The page stood at 239 words against D188's 250 until the owner's cut of
  2026-10-19 took its prose to the essentials, and the room that cut bought is the room the
  screenshots land in: a page that needs more than the ceiling needs it raised in the same
  commit that adds it — D188's own rule: the number moves where the need is, and a build that
  needs more fails until it moves.

The slider's own bytes do **not** move when a screenshot is added: the component's stylesheet is
inlined into the console page whether or not the slider renders, which is the 1,115 bytes the
site figure's last round names. What moves the site figure then is the picture's own bytes.

`website/test/console-screens.mjs` holds both states: the empty one this checkout ships — no
element marked `data-slot="console-screens"`, no track, no dots and no `<img>` in the page's own
article — and a two-slide one, built from a copy of the repository whose data file carries two
fixture rows. The fixture build writes no image bytes at all, because a test may not commit a
picture either: it asserts the `src` the loader derives from the row's file name, and the `alt`,
the caption, the size and the dot that each row produces.

## The rule this directory is written to

**A page renders a fact; it never restates one.**

`data/` and `content/` hold the facts, `src/` renders them, and no page writes a command,
a number or a claim of its own. The install block comes from `install.yaml`, the security
page from `content/security.md`, the CLI reference from `content/cli-reference.md`, the
costs from `content/costs.json`, the changelog from `docs/releases/`, and the site-wide URL
and the feature flags from `site.yaml` — each one rendered, each one held to its source by
a test, in this repository, in the commit that changes it. A page that paraphrases a
document is the copy that drifts first, which is the failure this whole arrangement exists
to prevent (F37, Global Constraint 6).

**The sources are not all inside this directory.** The arrangement above is a rule about
provenance rather than about where a file lives (the changes spec of 2026-10-18, item 3):
five of the documents the site renders sit elsewhere in the repository, and the build reads
them there.

- **The changes spec**, `docs/website/redesign/changes-spec-2026-10-18.md` — the reader's
  own request in the hero's chat is item 3's example of one, quoted from the ruling that
  asked for the chat (`src/components/AgentChat.astro`).
- **The two skills the agent reads**, `plugin/skills/parallelism/SKILL.md` and
  `plugin/skills/working/SKILL.md` — the agent's two answers in that chat, and the line about
  what the watch screen shows. The launch status line is *not* one of them: it is the
  illustration's own label, and the build refuses it if it turns out to be a sentence of the
  skill its turn cites (`AgentChat.astro`). Item 3's acceptance is that the
  agent's lines "show the real shape of the skills' behaviour", and every line a turn of the
  illustration *speaks* is a line of the document it cites, so the build reads those files and
  stops, naming the file, when a line is not in one of them. (The three `Pull request n of 3`
  labels are the illustration's own rather than a document's lines — a run that has not
  happened has no numbers to quote — and the caption labels the figure an illustration rather
  than claiming its lines are quotations.) **This is why a test build's copy of the
  repository carries `plugin/skills/`**: `test/lib/build.mjs` and the five test files that
  stage their own copy skip only the parts of `plugin/` a build does not read
  (`READ_UNDER_PLUGIN`).
- **The four steps' captions and the run's detail** come from `README.md` and
  `docs/design/v1.md`, cited by the component that quotes each line
  (`src/components/ArchitectureDiagram.astro`, `src/components/RunDiagram.astro`).

## Layout

```
website/
  package.json, package-lock.json, astro.config.mjs, tsconfig.json
  data/install.yaml, site.yaml     # hand-written facts, pinned by Go tests
  data/release-summaries.yaml      # each release's product summary, pinned by test/changelog-page.mjs
  data/typical-costs.yaml          # the owner's own measurement of what a job costs, pinned by test/caps-and-costs.mjs
  data/console-screens.yaml        # the console page's slides: hand-written, ships empty
  data/console-sha256sums.txt      # the console release's own sums file, copied verbatim
  content/                         # generated, do not edit (see the table above)
  src/lib/changelog.mjs            # reads docs/releases/ and the plugin's version at build time
  src/layouts/, src/components/, src/pages/, src/styles/   # W1.2-W1.12
  public/                          # icons, logo.png, site.webmanifest (scripts/gen-brand-icons.py)
  test/*.mjs                       # every one of them runs under `npm run check`
  dist/                            # the build output, not committed
```

`node_modules/`, `dist/` and `.astro/` are not committed: the first is what `npm ci`
installs from the committed lockfile, and the last two are build output.

## Brand assets

Every icon and the header logo are derived from one file, `brand/fugaro-logo.png` at the
repository root, by `scripts/gen-brand-icons.py` (see `brand/README.md`). The outputs are
committed under `public/` — the favicons, the apple-touch icon, the Android icons including
a maskable one, `logo.png` and `site.webmanifest` — and the base layout links them. Nothing
is fetched from another origin, and `test/brand.mjs` holds the sizes, the opacity of the
home-screen icons, the manifest's colours (the palette's `--ground`), the header logo's
40 KB budget and its empty alt.

## Deploying

`firebase.json` at the repository root is the hosting configuration: it serves
`website/dist` with clean URLs, a Content-Security-Policy, `nosniff`, a referrer policy, a
one-year immutable cache for the hashed `/_astro/` files, a day for icons and fonts, and five
minutes for HTML. It names no project and there is no `.firebaserc`.

The policy admits this origin and the three Google Analytics origins W3.2 widened it by, and
nothing else: `script-src 'self' https://www.googletagmanager.com` (inline styles are still
allowed, because Astro inlines small stylesheets), `img-src` and `connect-src` with
`https://*.google-analytics.com`, `connect-src` with `https://*.google.com`. Those are the
origins Google documents for GA4 without Ads features, read 2026-10-18 from
[tag-platform/security/guides/csp](https://developers.google.com/tag-platform/security/guides/csp),
and `test/origins.mjs` holds the served policy to that list token by token — an extra origin
is a failure there, in the same commit that would add it. `test/brand.mjs` is the coarser
check beside it: the served policy names those three origins and no others. (An earlier
revision of that page listed `https://*.analytics.google.com`; the current one does not, and
the contract follows the page rather than the memory of it. `https://*.google.com` is in
`connect-src` because the documentation requires it, and without it the collect requests a
consenting visitor's browser makes would be blocked.) It landed ahead of W2.1 with the
owner's deploy script, and W2.1 extends it (the staged workflow).

### The canonical domain

`data/site.yaml`'s `canonical_url` is the one origin the canonical `<link>`, the sitemap, the
OpenGraph tag and every absolute link on the site are built from, and it is the real domain
the owner registered and connected Firebase Hosting to (owner ruling 2026-10-17; the site's
own project is `fugaro-prod`, D91). Its host is named in `data/site.yaml` and nowhere else
under `website/`, and it is on `allowed_hosts` — so pointing the site at a domain is that one
line, the host list's own entry and a rebuild, and no page can go on serving a domain the
site stopped being. `TestWebsiteCanonicalHostAppearsOnlyInSiteYaml` in
`internal/cli/docs_website_boundary_test.go` scans every file under `website/` for the host
(and the control beside it, `TestWebsiteHostScanSeesAPlantedCopy`, plants one in a page to
prove the scan sees it), which is why this page does not print the domain.

**There is no second domain, and no page or document here may name one.** W2.2 wrote a
`secondary_domain` for a domain the owner acquired alongside the canonical one; the owner
ruled on 2026-10-17 that no such domain is theirs, so both the value and every step that
attached it are gone — the ruling is recorded in the amendment under Task W2.2 of
`docs/website/implementation-plan.md`.
`TestWebsiteNamesNoSecondDomain`, with its own control, scans every file under `website/` for
the name W2.2 wrote and refuses it coming back, so a later task cannot reintroduce it by
copying an older revision of a page. **Registering a domain, attaching it to the site's
Firebase project and writing its DNS records are the owner's steps** — no agent registers a
domain, touches DNS or changes that project — and the live result is what check 69a in
`docs/gcp-live-checklist.md` records.

`scripts/deploy-website.sh` deploys from the owner's machine with the Firebase CLI, using
whatever `firebase login` the owner already did — it never reads, prints or takes a token
or a key:

```sh
scripts/deploy-website.sh --dry-run                 # build + npm run check, print the deploy command
scripts/deploy-website.sh                           # deploy live to fugaro-prod
scripts/deploy-website.sh --channel preview-1       # deploy to a preview channel instead
scripts/deploy-website.sh --project <id> --dry-run  # another website-only project
```

The project defaults to `fugaro-prod`, the Firebase project the owner created for the
website only (D91); `--project` overrides it, and it is never an installation's project.
The script checks that `node`, `npm` and `firebase` are installed, runs `npm ci` when
`node_modules/` is missing, runs `npm run check`, refuses an empty `dist/`, and then runs
`firebase deploy --only hosting --project <id>` (or
`firebase hosting:channel:deploy <name> --project <id>`). `scripts/deploy-website_test.sh`
tests it with the tools stubbed on PATH, and `go test ./scripts/` runs that test.

The deploy workflow is still W2.1's and **not a Fugaro run's to write**: W2.1 stages a
workflow under `docs/pending-workflows/` for the owner to copy in, and the owner runs it to
deploy to the Firebase project created for the website only (D91, D109) — a run may not
change `.github/workflows/`. `docs/pending-workflows/` now holds one pending workflow,
`docs/pending-workflows/shared-tests.yml` — the shared package's test job (mobile plan Task
P4.3), which is not this site's: the website's two manual
workflows, `website-preview.yml` and `website-promote.yml` (#566), were copied into `.github/workflows/` by the
owner, and no deploy workflow is staged. The only other workflow files left there are the desktop
console's `console-tests.yml` and `console-release.yml`, which the owner has copied in as well:
their staged copies stay by design, because the console's docs tests assert each one is
byte-identical to its copy under `.github/workflows/`, so neither is pending.
