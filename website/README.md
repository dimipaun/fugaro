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

Eight checks run over the **built** artifact (`dist/`) on every `npm run check` (W1.11), each
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
  depends on the git state of the checkout the gates run in. The changelog renders each release
  file's **"not yet released"** state wherever `git tag --list` finds no tag
  (`src/lib/changelog.mjs`), so the same commit builds to **980,920 bytes in a checkout with no
  tags** — which is what `website-preview.yml`'s default checkout is, and therefore what the
  deployed site is — and to **979,405 bytes with the tags this repository's own clone carries**,
  the changelog page's 1,515 bytes being the whole of the difference. A figure derived from one
  of those two builds is refused by the other, which is what happened: the gate could not
  measure the build the README named and `website-preview` could not deploy. So the derivation
  is read over **the gates' own build of the site**: a copy of the repository in a temp
  directory, built with the site's installed Astro by `test/lib/build.mjs`'s `buildCopy`, which
  carries no `.git` at all and so gives every checkout the same three numbers. The site's own
  `dist/` — whichever of the two shapes it takes — is held to the ceilings below like any other
  artifact, and being the *smaller* of the two builds it passes.

  **What each cause really moves**, measured on that build rather than argued:

  - the **1,515 bytes** between the two shapes of the site build are the changelog page's own
    "not yet released" labels, which a checkout with no tags prints for every release file;
  - flipping **`features.console_downloads`** — the flag the console's release turned on, and the
    flag `test/console-option.mjs` flips back off to prove the unreleased state is still safe —
    moves the **site figure up by 1,572 bytes**, from 979,348 with the flag off to 980,920 with it on:
    the console option's own markup on the three pages that render it — the console page's
    downloads section (each artifact's checksum beside its link, and the verification command)
    1,100, Getting Started's option 386 and the homepage's call to action 86. It moves
    the **page weight not at all**: the heaviest page is the docs hub, which renders no install
    option, no download and no hero. The copy the gates build of the unreleased state is
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
  six corrections in `main`'s, the redesign's fix rounds in this branch's. The last three rounds
  are where this build's three numbers come from, and both are measured over **two builds**
  rather than argued:

  - **This branch's own fix rounds**, each re-derived in the commit that moved it: the round
    after the merge moved the chat above the diagram, drew its turns as alternating bubbles,
    took three of the four cyan edges off the flow's cards and shortened the footer's link
    label; the round after that took the install section's own top padding off, because the
    flow above it already carries the gap between the two blocks — 54 bytes off the site
    figure, with the page weight where it was, the heaviest page being the docs hub, which
    renders neither block — and the round after *that* moved both figures up by the two
    declarations that make a command wrap (`white-space: pre-wrap; overflow-wrap: anywhere`)
    moving onto `base.css`'s `.command-block > pre`, which every page inlines: 440 bytes onto
    the site figure and 44 onto the page weight. That last round is also why **the page and
    the site move with the script**: a script every page loads is a page's weight as well as
    its JavaScript.
  - **The `fugaro watch` screenshot landed on the homepage** (`main`'s #655) and moved the
    site figure alone. The owner's capture and its transcription
    (`public/images/fugaro-watch.png`, 130,533 bytes, and `fugaro-watch.txt`, 3,363) are served
    by `public/` and are in every build's `dist/`, and the derivation the figure named before
    them predated the assets: those 134,195 bytes of build, plus the 299 bytes of homepage
    markup that put the picture in its slot, are the whole of the move — tight 1,056,379 to
    1,224,123 and stated 3,169,137 to 3,672,369. The page weight does not move with it, and
    that is the check worth keeping: the picture lands on the homepage, and the homepage is not
    the heaviest page — the docs hub is, at 267,095 bytes either side of that change — while
    the homepage's own weight went from 94,007 to 224,839 bytes, the screenshot being 130,533
    of it. The JavaScript is untouched.
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
  - **The console's release turned the flag on** (W3.1), and moved the site figure alone,
    976,349 → **977,921**, by the **1,572 bytes** the list above names: the console page's
    downloads section, Getting Started's console option and the hero's link to the console.
    The page weight and the script did not move: the heaviest page is the docs hub, which
    renders none of the three.
  - **The homepage's prompt line** (the owner's feedback of 2026-10-19) moved the site
    figure alone, 977,921 → **978,623**, by **702 bytes**: the request the hero renders under
    the headline, the `>` mark in front of it and the two rules that draw it as the chat's own
    bubble — the homepage's inline stylesheet and its HTML. Nothing outside the homepage moved,
    because the three files that changed all render on that one page, so the page weight and
    the script stand where they stood.
  - **The benefits** (the same feedback) moved the site figure alone again,
    978,623 → **980,771**, by **2,148 bytes**: the section's four names and four lines, its
    heading and its grid, and the scoped stylesheet rules that draw it — all of it on the
    homepage, which is the one page that renders it. The page weight and the script did not
    move: the heaviest page is the docs hub, and it carries no benefits. The words the section
    costs the homepage are what moved the word ceiling above, in this same commit.
  - **The pull request's caption** (the same feedback) moved the site figure alone again,
    980,771 → **980,837**, by **66 bytes**: the caption's own line and the `data-source` it
    cites with it, `docs/git-providers.md#Checkpoint pushes (git.pr.checkpoints)` in place of
    `README.md#How it works`, on the two pages that render the flow — the homepage and How It
    Works. The page weight and the script did not move: the heaviest page is the docs hub,
    which renders no flow. The
    caption is two words shorter than the one it replaces, which the homepage's word count
    below carries.
  - **The benefits' block rule** (the review of round 6) moved the site figure alone,
    980,837 → **980,920**, by **83 bytes**: the section's gutter and the room below it are now
    declared in the component that renders the section, where they were declared on the page and
    reached nothing — Astro scopes a page's selector to the elements the page renders, so the
    section, rendered by `Benefits.astro`, never carried the page's scope. The page's rule now
    names the two blocks the page renders and the section's rule writes the three declarations
    out again, which is the whole of the 83 bytes. The page weight and the script did not move:
    the heaviest page is the docs hub, which renders none of this, and it is one page's inline
    stylesheet either way.

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
  - **Page weight per page:** 1,000,974 bytes. Three times 333,658: the heaviest page, the
    docs hub, at 266,926 bytes (its HTML — the whole generated CLI reference — plus the
    files it fetches: the script above, the logo and the icons), plus 25 %.
  - **Whole site:** 3,678,450 bytes. Three times 1,226,150 (every file in `dist/`, 980,920
    bytes across 23 files, plus 25 %) — the gates' own build of the site, which is the build
    with no tags, 1,515 bytes above the one a full clone makes (above).

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
    the rendered document's own title, and the page's lead line comes before it) and so is
    `/changelog/` (whose summary is its first section, under the heading that names it).
    The ceiling is a ceiling, not a floor — a page with no lead region is a page D188's
    definition does not describe, and this gate says nothing about how much a page's first
    section should say.
  - **Words per page:** 300 words. Every word a reader sees inside `main` — command blocks
    and code spans included, since the ceiling is a claim about the page rather than about
    its prose — counted by `budget.mjs`'s own reader, which reads a page the way
    `test/lib/html.mjs`'s `textOf` does (a script or style body is not text, an image's
    `alt` is) and adds the one rule `textOf` does not carry: a subtree carrying `hidden` is
    not text either. No built page hides anything inside `main` today — the layout's own
    `hidden` elements, the skip link and the consent banner, sit outside it — so that rule
    is what stops a page that starts hiding a control from being counted for words nobody
    sees. `/docs/`, `/security/`
    and `/changelog/` are exempt, which is D188's own list: each renders a document of this
    repository rather than a page of the site, and the brief allows those to stay long.
    The ceiling is set against the site as it was measured before the redesign
    (`docs/website/redesign/reconciliation.md` §6: faq 493 words, caps-and-costs 487,
    getting-started 431, privacy 277, community 268 — every one of them over it).
    **D188's number was 250, and the owner's feedback of 2026-10-19 is what moved it.**
    That brief asks the homepage for a Benefits section, which is a fourth required block on
    the one page that already carried the three the briefs before it require — the promise,
    the chat (changes spec item 3) and the flow (item 4) — and the four together measure 294
    words. The number here is therefore the smallest round ceiling above the page that
    carries every block its briefs require, and it is still a ceiling rather than a target:
    the highest of the pages that gained nothing from that brief is Getting Started at 250
    words, and a page that wants the extra 50 has to carry a brief's own block for them.
    The raise is recorded as a reading in `docs/website/redesign/README.md`, the way every
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
  first `h2` precedes the first `pre`. The whole gate is one function over one `dist`, so its
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
what it returns. Nothing is written to disk, so a Highlights file that lands on `main`
before its tag exists leaves no stale copy behind — and no date is taken from a tag,
because a shallow checkout has no tags and the newest entry a reader sees has none yet
(D100). `website/test/changelog.mjs` holds that.

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
  (`src/components/ArchitectureDiagram.astro`, `src/components/RunDetail.astro`).

## Layout

```
website/
  package.json, package-lock.json, astro.config.mjs, tsconfig.json
  data/install.yaml, site.yaml     # hand-written facts, pinned by Go tests
  data/console-sha256sums.txt      # the console release's own sums file, copied verbatim
  content/                         # generated, do not edit (see the table above)
  src/lib/changelog.mjs            # reads docs/releases/ at build time
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
