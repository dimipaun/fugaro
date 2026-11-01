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
(`website/public/fonts/`, which the layout preloads from), and there is no Google Fonts, no CDN — because every
third-party origin is a request from a reader's browser to a company this project has no
relationship with (D106). There are two exceptions, and the owner chose both: the
analytics script (D93) and, since G2.3, the three vendors of the four campaign pages
(D401) — each loaded **only after the visitor consents**, with W1.12's banner as the gate
that decides whether anything is fetched and its own tests for no request before
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

**G2.3 is the task that added the second question** (D401), on the four campaign pages only —
`/save`, `/control`, `/scale` and `/crew` — and the three loads it covers: Meta's pixel,
LinkedIn's insight tag and Calendly's booking widget.

- **A separate consent under its own key**, `fugaro:marketing-consent`, and not a second value
  under the analytics key: a visitor who answered the site's question is not asked again on
  every page they read, and one who answered the campaign's question has not answered the
  site's. The banner asks both on a campaign page, and one press answers the questions that
  were open on it.
- **The configuration is the banner's markup**, in `data-marketing-*` attributes: the page the
  loader was built for, the two vendor ids, the three origins and the booking link
  `data/landing.yaml` writes. `site.js` names no host, so a page that is not one of the four
  carries none of it and loads none of them, and a campaign page served from another path
  loads none of them either.
- **An empty id is a loader switched off** (R3), and this repository publishes no identifier
  the owner did not supply. Both ids are the owner's, supplied on 2026-10-31 — the LinkedIn
  Partner ID and the Meta pixel id — so all three loaders run on the four campaign pages behind
  the campaign consent, and a value cleared in `site.yaml` switches its loader off. `internal/cli`'s
  boundary test refuses either id coming back empty and pins the three origins exact, and
  `test/consent.mjs` reads the switched-off state off a page whose markup carries an empty id.
- **Each vendor is handed the page's path and no query** (F22): the site builds the address it
  reports from the origin and the path, and passes it to the pixel and the insight tag as
  `page_location` rather than leaving them to read the address in the window, so what this site
  hands them carries none of the campaign parameters a visitor arrived through. What a vendor's
  own script reads out of the browser once it is running is the vendor's, and not something this
  site can promise about — the guarantee is over the hand-off, which is also the half the plan
  asks a test to hold (a test over the loader's configuration). The parameters G2.4 passes on to
  Calendly are allow-listed and length-capped, and reach neither of these two.
- **Withdrawal removes what this site may remove.** The pixel is told to revoke, and every
  first-party cookie the two tags set is deleted from this host: Meta's `_fbp` and `_fbc`, and
  the six LinkedIn documents for its insight tag — `li_sugr`, `AnalyticsSyncHistory`,
  `UserMatchHistory`, `bcookie`, `lidc` and `li_gc` — which is the list `site.js` carries and
  the privacy statement prints. A script already fetched keeps running until the visitor
  reloads, and a cookie a vendor set on its own domain is the vendor's to remove, which the
  privacy statement says in those words.
- **The deployed policy gains each vendor's origin per directive** — `script-src` for the
  three scripts, `img-src` and `connect-src` for the pixel's and the insight tag's beacons,
  `style-src` for Calendly's stylesheet, and a new `frame-src` for the booking page — with
  nothing added to `script-src` and one wildcard, `https://*.licdn.com` in `img-src`, which
  the owner ruled in on 2026-10-31 for that directive alone. `test/origins.mjs` holds the exact
  per-directive list, `test/brand.mjs` the coarse one, and `test/consent.mjs` the cross-check
  that the gate loads from exactly the origins the policy admits. The Calendly booking link
  itself stays a plain `<a href>`: a visitor who declines the campaign consent keeps it.

**G2.4 is the task that added the funnel and the pass-through** — three key events to GA4 (and,
since the owner's ruling of 2026-10-31, the same three actions to the pixel under Meta's names),
and the campaign parameters a visitor arrived through, onto the booking address and nothing else.

- **Three events, and only after the analytics grant.** `install_click` and `whatsapp_click`
  are presses of the two secondary calls to action, found by the ids the markup gives them;
  `demo_booked` is the widget's own booking report, and it is taken only when the message *is*
  that report — the widget posts several others over the same channel, its frame's height and a
  viewed event type among them, so the origin and the sender alone would count a booking when
  the widget was drawn — AND it comes from the frame Calendly's script built inside this page's
  container AND from the origin the booking link names. Anything else is dropped unread —
  another origin, another window of the page, another of the widget's messages — and no field
  of the message is forwarded. None of the three carries a parameter, and none is sent before
  the visitor has granted analytics or after a withdrawal; the count is the funnel, and the
  campaign's own attribution reaches GA4 through the owner's Calendly-to-GA4 integration rather
  than through this site (F22).
- **The same three actions are Meta's events too, and only after the campaign grant** (the
  owner's ruling of 2026-10-31): `ViewContent` for a press of Install free, `Contact` for the
  chat link and `Lead` for a booking, sent from the same handlers — the same press, the same
  once-per-action binding, and the same origin and sender checks on the widget's message — so
  the two measurements cannot drift apart. The gate is the campaign question rather than the
  analytics one: a visitor who granted the campaign measurement and not the site's is sent the
  pixel's three and none of GA4's, and a campaign page whose pixel id is empty sends Meta
  nothing (`test/landing-events.mjs`).
- **The campaign parameters go onto the booking address and nowhere else.** The five `utm_*`
  names the agenda's links carry, each value decoded and encoded again — a `+` in the visitor's
  query read as the space a browser reads it as, and the `%20` this page writes read back the
  same way — a value longer than 100 characters dropped rather than truncated, and every other
  key dropped: into the embed's address, which is the owner's link from `data/landing.yaml` with
  that query on it. They are
  not sent to GA4, not put into the chat message a visitor reads, and not added to the plain
  `Book a demo` anchor a visitor who declines the campaign consent keeps. A visitor with no
  campaign parameter gets the owner's link exactly as the file writes it.
- **The chat link is the third call to action**, and its address is `data/landing.yaml`'s
  WhatsApp number with the variant's own pre-fill: the loader builds it, the section renders
  it, and `wa.me` is on `allowed_hosts` because the site links to it.

## The gates

Thirteen checks run over the **built** artifact (`dist/`) on every `npm run check` (W1.11), each
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
  own bytes apart — the larger in a checkout with no tags, which is what
  `website-preview.yml`'s default checkout is, and therefore what the deployed site is — and
  the smaller with the tags this repository's own clone carries, the changelog page's own
  "not yet released" labels being the whole of the difference. A figure
  derived from one of those two builds was refused by the other, which is what happened: the
  gate could not measure the build the README named and `website-preview` could not deploy. So
  the derivation is read over **the gates' own build of the site**: a copy of the repository in
  a temp directory, built with the site's installed Astro by `test/lib/build.mjs`'s `buildCopy`,
  which carries no `.git` at all and so gives every checkout the same three numbers. The site's
  own `dist/` is held to the ceilings below like any other artifact.

  **No page of the site depends on the checkout's git state any more** (the owner's brief of
  2026-10-19): which releases are published comes from `plugin/.claude-plugin/plugin.json`'s
  version rather than from a tag, so the two checkout shapes above are one build now — the same
  commit builds to **1,512,120 bytes in a checkout with no tags** and to **1,512,120 bytes with the
  tags this repository's own clone carries**. `test/budget.mjs` reads that pair **here and
  nowhere else** (a figure copied into the gate's own comments or another document is a second
  place to forget, and this pair was once copied in two, which is how a clone figure came to sit
  a round's bytes behind) and refuses it unless the two are one figure — and unless that one
  figure is the site's own, which is the byte count the whole-site budget above derives from:
  the paragraph's claim is that the two shapes are *this* build, so a pair that agrees with
  itself a round behind is the same drift by the other door. The derivation stays
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
    render it: the console page's downloads section (each artifact's checksum beside its link),
    Getting Started's option and the homepage's call to action. It
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
    by the group labels in the header's row (item 1; the labels are **two** now that the owner's
    feedback of 2026-10-21 dropped the Reference group — see the bullet on it below), and the
    console's link in the homepage's call to action (item 6), which renders from the console's
    flag.
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
    section showed the quick path above the fold with the console option beside it (the card
    round at the end of this list replaced that pair with a card per system), the page carries
    a "Set up your installation" list of its own, and the other install paths and the platform
    list moved into a Details section below them. The page weight does not move: the
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
    the headline, the `>` mark in front of it and the rules that draw it as a line a reader
    types — the homepage's inline stylesheet and its HTML. Nothing outside the homepage moved,
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
    `main`'s Getting Started rework into this branch): a step of "Set up your installation"
    rendered the command it names and the sentence after it with nothing between them, the
    link ending one source line and the sentence starting the next. The space is written
    into the line now, **1 byte**, and the site figure is 972,414 → **972,415**. The page
    weight and the script did not move. The clone figure moved by the same bytes as each of
    the security page's rounds.
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
    own gate did not hold. It moved the site figure alone, 981,544 → **982,707**, by **1,163
    bytes**: **37** of them are the rules that say where the two drawings change over and how
    wide the stacked one is drawn, and the other **1,126** are the `--repo` round's bytes,
    which that round left un-derived — the bullet below re-derives from the build and names
    this same move as the 1,126 plus the 37 (D192). The two drawings change over at **59.25
    rem** now rather than at the site's 48 rem chrome breakpoint: the wide drawing's smallest
    label is 16 units of a 1200-unit picture, so at a 768 px viewport the column scaled it to
    9.6 px, and a label a reader cannot read is the whole reason the stacked drawing exists. The
    stacked drawing carries a ceiling of its own scale — 22.5 rem, one unit to the pixel — so a
    tablet is shown the phone's picture rather than that picture stretched to the column, and
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
    room "The console's screenshots" below counts. The page weight and the script did not
    move: the heaviest page is the docs hub, and the console page is not it.
  - **The homepage became the hero, one three-tab slider and one Get started button** (the
    owner's approved draft "Home, draft 2 (slider)", 2026-10-19), and it moved the site figure
    alone, 975,034 → **968,916**, by **6,118 bytes**. All of it is on the homepage: the chat's
    conversation left the site with `AgentChat.astro` — its markup and the rules that drew it,
    which the page inlined — the flow's figure and its stylesheet went with the rest of the
    page's middle, and the two install cards became one button and one command. The slider's own
    rules, the `fugaro watch` capture's slot and the 44 px minimum the owner's brief measures
    every control against — which the hero's two links were a fraction of a pixel short of on
    their padding and their line height alone — came back onto the page, which is why the move is
    a net 6,118 bytes rather than the whole of what left. The page weight and the script did not
    move: the heaviest page is the docs hub, which renders none of this, and the site ships the
    same one script. The word ceiling is `privacy`'s own page, which the draft did not touch; the
    homepage's own words are well under it. The clone figure moved by the same bytes.
  - **Getting Started's install options became one card per system** (the owner's draft of
    2026-10-19, "Getting started, draft 2 (OS icons)"), and moved the site figure alone,
    975,034 → **975,713**, by **679 bytes** net: two `<article class="install-card">` blocks,
    each carrying its system's name, the note and the command the install file offers it, and
    one inline `<svg>` glyph of the page's own — a laptop for macOS, a terminal for Linux — with
    the rules that lay the cards out in a grid, box them and size the glyph, all of it inlined
    into the one page that renders the section; and against that, the two `data-option` wrappers
    and the option box's own rules, which the cards replace, and the console's heading, which
    moved a level down to sit inside the macOS card. The page weight and the script did not
    move: the heaviest page is the docs hub, which renders no install section, and nothing
    outside Getting Started's own page and stylesheet changed. The page's own words fell with
    the boxes they sat in, 248 → **247**, so the word ceiling did not move. The rules the
    cards are held to — no card for a system the file does not carry, one icon per card, and it
    draws something — are `test/os-cards.mjs`'s, and the count of gates in this README moved with
    it.
  - **The security page opens with the trust boundary drawn** (the owner's brief of 2026-10-20),
    and it moved the site figure alone, 975,034 → **980,342**, by **5,308 bytes**. The page is
    four things now and nothing else: the drawing comes first, above the document's *In short*
    section, and it is one inline `<svg>` — the source of the task and the people who can steer
    a run, the one container per run and what it holds, the pull request that leaves it, and the
    egress nothing stops — with a name on it for a screen reader, the document's own facts as a
    list under it and its two limit lines below that. **Every label of the drawing is a phrase of
    `SECURITY.md`**, and `website/test/security.mjs` reads each one back off the built page and
    refuses a label that is in neither the document's own *In short* section nor the words the
    region carries beside the picture — its heading, the facts under it and the two lines below
    them, and not the rest of the page — which is why the drawing says "one container per run"
    and "ends every run with a pull request" where the brief's own sketch said "your own cloud
    project" and "a draft pull request you merge": the policy makes neither of those claims. The
    model-key card draws the document's conditional as the one sentence it is — the budget
    gateway is not on by default, so the real model key sits in the agent's environment — rather
    than setting the gateway and the key's place side by side as two facts, which would be a
    claim the policy never makes. The drawing is shapes and text in the markup and loads
    nothing, so the page weight and the script did not move: the heaviest page is still the docs
    hub, and no file the site ships was added. The clone figure moved by the same bytes.
    **Its review round moved the same figure by the last 308 of those bytes**: the drawing now
    declares its own geometry in the markup — every card the inset its labels are drawn at
    (`data-pad`), every label the size it is drawn at (`font-size`) — so that `test/security.mjs`
    can measure each label against the box it is drawn in and refuse one that overruns its card,
    which is why the source card's phrase is broken after "launch a" and not after "launch".
    **The inset is read on both axes, and the review of 2026-10-20 is why**: the first round
    measured a label's width against the card's and nothing else, so two cards drew their last
    line *below* the inset they declared — the runner's `can read its /proc` ten units past it,
    the gateway's last line four — and the gate was green over a picture with two lines on a
    card's border. Both cards grew to the height the lines need (a three-line card is 96 units
    high on the drawing's rhythm of a title 26 below the top edge and lines 24 apart, a
    four-line one 120) and the drawing's `viewBox` with them, 688 → **744**; a baseline above a
    card's top inset or below its bottom one is refused now, and the two cases are planted in
    `test/security.mjs`'s refusals.
  - **The benefits section is headed "Why Fugaro?"** (the owner's ruling of 2026-10-20), and it
    moved the site figure alone, 988,253 → **988,259**, by **6 bytes**: the section's heading
    and the slider tab that opens its panel are one name, so the three characters "Why
    Fugaro?" is longer than "Benefits" (eleven against eight) are written twice, once in the
    `<h2>` and once in the tab. All of it is on the
    homepage, which is the one page that renders the section, so the page weight and the script
    did not move: the heaviest page is the docs hub, which carries no benefits, and no file the
    site ships was added. The id and the class the section already carried (`benefits-title`,
    `home-benefits`) did not move with the label — they are the element's name in the markup
    rather than a label a reader sees — so nothing the stylesheet or `aria-labelledby` reads
    changed. The page's own words move by two (the heading and the tab are each a word longer),
    which is well inside the ceiling `privacy`'s page sets and does not move it; the two gates
    that pin the string are `test/home.mjs` (the heading) and `test/slider.mjs` (the tab, which
    it holds equal to the heading of the panel the tab opens).
  - **The security drawing's vertical round grew the two cards the review of 2026-10-20 found**
    (above), and it moved the site figure alone, 988,259 → **988,260**, by **1 byte**: the
    gateway card's own height goes from a two-digit number to a three-digit one (`96` → `120`),
    which is the whole of what the page's bytes did with it. The drawing loads nothing, so the
    page weight and the script did not move and no file the site ships was added. **It is the
    same round that added the gate's vertical half**, so the bytes and the check that reads them
    arrive in one commit: a picture that grows by the card that overran is a picture only a
    gate that measures the inset on both axes can hold.
  - **The console's per-platform block** (2026-10-20, the owner's Linux ruling): Getting
    Started's console card renders the data file's optional `linux` block as the console page's
    Downloads section does — the block's own sentence, and the assets the block names drawn
    under it rather than in the list above — so the card no longer lists a Linux artifact under
    a sentence about the disk images. **The whole site figure above is re-derived only when asked (the owner's ruling), so no figure is claimed here:**
    the two rules that give that block its spacing, which are scoped to the component one page
    renders. Nothing else moved and no page gained a word — the block's markup is rendered only
    while the data file carries a block, and the file this checkout ships carries none — so the
    page weight and the script are where they were, and the count of files in `dist/` is 23.
  - **The console page's four fixture screenshots** (the owner's feedback of 2026-10-21:
    "console page: screenshots missing") moved the page weight and the site figure, with the
    script where it was. `website/public/screenshots/` carries four WebP captures of the
    console's own screens — Live, Run, Spend and Installations, 1920 x 1200 each, 306,982 bytes
    between them, with the `MANIFEST.json` the run that took them wrote beside them — and
    `data/console-screens.yaml` carries the four rows the slider renders them from, in place of
    the empty list the placeholder frame used to draw. **The console page is the heaviest page of
    the site while it renders them**, 92,530 → **399,732 bytes** (the four pictures, its own
    markup, the script and the icons it fetches), which is why the page weight moves with the
    site figure here: the page the ceiling is derived from is the console page now, and the docs
    hub — the page every earlier derivation in this list measured — is 292,469 bytes. The site
    figure moves 988,260 → **1,387,679**, which is **399,419 bytes**, and **309,378** of them are
    this round's: the four pictures' **306,982** bytes, the manifest's **2,176**, and **220** onto
    the console page's own markup (30,091 → 30,311 bytes — four `img`, `figure` and `figcaption`
    elements in place of the placeholder frame's slide, which the per-file comparison of the two
    builds shows is the whole of the rest: no other file in `dist/` moved a byte). **The other
    90,041 bytes are what earlier rounds left un-derived**, and this round pays them by
    re-deriving rather than adding them to a figure that had already stopped describing its
    build: the gate's own build of the branch this round starts from is **1,078,301 bytes across
    23 files**, while this list still said 988,260 — D192's exact derivation runs only under
    `FUGARO_DERIVE_BUDGETS=1`, which CI does not set, and the owner's ruling of 2026-10-20 is
    that these figures are re-derived when they ask rather than on every round, so a figure here
    can lag the build it names (the redesign's README records that ruling, and the drift it
    allows was 1,035,806 against the stated site figure in the round that wrote it down). Those
    90,041 bytes are bytes the site grew before this round and are absorbed here, the way the
    `--repo` round's 1,126 bytes were paid in the logo round above. The script is untouched in
    this round — the slider as it stood then shipped no JavaScript and needed none (the
    feedback-B round below is where that changed) — and the count of files in `dist/` goes 23 →
    **28**. The page's own words go
    211 → **303**, well inside D188's 500 and the derived ceiling: a row's `alt` is text a reader
    gets when the picture does not arrive, and the four of them are the whole of the move.
    `test/console-screens.mjs` holds the four rows to the files' own headers and to the
    manifest's `alt` texts, and the two figures above are re-derived in this commit, from the
    gate's own build.
  - **The console page's feedback-B round** (the owner's feedback of 2026-10-21: the slider's
    count, its own controls and an autoplay; the three download cards in one row; and the struck
    `shasum` command) re-derived **all three** figures, from the gate's own build. **The script's
    figure is this round's alone**, and this is the first JavaScript the slider has ever cost the
    site: `site.js` goes 39,888 → **49,561 bytes** (+9,673), which is the whole of the slider's
    half — the bar the markup emits `hidden` revealed and driven, the count and the current dot
    read from the track's own scroll position, the arrows and the dot presses with their wrap,
    and the six-second autoplay with its four stops — and none of it is needed to read the page
    (the fallback is the markup and it did not change: the four pictures are in the document and
    the four dots are links). The page weight and the site figure move 399,732 → **410,743** and
    1,387,679 → **1,411,489**, which is **11,011 bytes**, and **the per-file comparison of the two
    builds shows that is the whole of the move**: `dist/scripts/site.js` (+9,673) and
    `dist/console/index.html` (30,311 → **31,649**, +1,338) are the only two files in `dist/` that
    moved a byte. What the page's own 1,338 are is this round's three parts — the control bar and
    its rules, the slider's stylesheet, the cards' one-row rules and the `word-break` on the
    checksum line — less the command block the owner struck.
    **The other 12,799 bytes are what rounds merged from `main` left un-derived** — 12,746 of them
    already inside the build the screenshots round's figure was taken from, and 53 from the
    CLI rename round's strings reaching the reference page — and this round pays them by
    re-deriving rather than carrying them forward, the way the screenshots round paid the 90,041
    before it. The console page's words go 303 → **299**: the struck command was four of them, and
    the bar the script drives adds none (an arrow's name is an `aria-label`, which is not text a
    reader reads). No file was added or removed, so the count of files in `dist/` stays 28.
  - **The feedback-B review round** (the review of the round above, 2026-10-21) re-derived the
    script's figure and the two that follow it from it, and **the whole move is the script's own
    bytes**: `site.js` goes 49,561 → **50,714 bytes** (+1,153), the page weight 410,743 →
    **411,896** and the site figure 1,411,489 → **1,412,642**, both by the same 1,153. No page's
    markup moved at all — `dist/console/index.html` is **31,649 bytes** in both builds — and no
    file was added or removed, so the count of files in `dist/` stays 28 and the console page's
    words stay **299**. The 1,153 bytes are the three corrections the review asked for: the move
    the script asks the track for is **`behavior: 'instant'`** rather than `'auto'` — which
    CSSOM-View resolves to the track's computed `scroll-behavior: smooth`, so the move was
    animated, the count and the mark narrating slides the reader was passing through and a second
    Next press advancing from a position a `scroll` event had already put back; the **first
    reading is the track's**, so a page opened on a shared `#console-screen-3` link or a restored
    scroll position counts and marks the slide it is showing rather than the first; and the
    comments on both, which are the larger part of the 1,153. The tests grew with them —
    `test/lib/dom.mjs` now records what the script asks the track for and can open a page at a
    slide, which is one more plant (`behavior: 'auto'`, refused by name) and one more case (the
    page that arrives scrolled, with the count hardcoded to the first slide refused beside it) —
    and **the pair the site's two checkout shapes are stated as is now held to the site's own
    build figure**, because the pair was a round behind in this same review: `test/budget.mjs`
    read those two numbers against each other and against nothing else, so a pair that agreed
    with itself and disagreed with the site passed the case that exists to catch exactly it.

  - **The four-point star under "The newest tools and models, ready to use" is drawn symmetric**
    (the owner's feedback of 2026-10-21: "icon for 'The newest tools and models, ready to use' is
    asymmetrical"). The glyph is the same four points, the same four notches, the same 16-unit
    span and the same 2-unit `currentColor` stroke: the drawing gains the north-west notch it was
    missing — a `l5.8-2.2` where the outline ran straight from the west point to the north one,
    which is the whole of why a reader saw a star leaning — and moves onto the middle of its
    24×24 box, where every other glyph of the set sits. That is **8 bytes** onto the homepage's
    markup and so onto the site figure, with the page weight and the script where they were and no
    page's words moved, and the count of files in `dist/` stays **28**. `test/home.mjs` carries the
    rule the drawing is now held to — a glyph drawn as one closed outline of straight edges is
    symmetric about both of the lines through its own middle and that middle is the middle of the
    box — planted against the star as it shipped, so the fix and the check that would have caught
    it arrive together.
  - **Home is the menu's first item** (the owner's feedback of 2026-10-21: "I think we need a
    'Home' as the first menu"). The item is a link in the menu's own list, in front of the
    groups, on every page and at every width, and the phone block's two rules are what pay for a
    fourth item in the header's row — the gap between the wordmark and the menu goes to the
    phone's 4 px and the items stand with no gap between them — with the wordmark's text leaving
    that row from 432 px down rather than from 390 px down, which is the same arithmetic with one
    more item in it. **Both halves are inlined into every page**, so the move is uniform and large
    for a single link: **170 bytes onto each of the 11 pages** — 119 of stylesheet and 51 of
    markup — which is **1,870 bytes** onto the site figure and **170** onto the page weight (the
    homepage's own `aria-current="page"` moves from the wordmark to the new link, which is why its
    markup moves by the same 51 as every other page's). The script is untouched — the menu still
    opens and closes with JavaScript off, and Home is a link, so it needs none — and the count of
    files in `dist/` stays **28**. `test/base-styles.mjs` holds the item's position, its route and
    its name and refuses a Home item that holds a list, and holds the phone block's two rules —
    the cut the four-item row was measured into, kept now that the same day's later feedback left
    the row a group narrower (the bullet on it below), where the two rules are headroom rather
    than the whole of the payment.
  - **"Say it. Get pull requests." is a slider** (the owner's feedback of 2026-10-21: "can we
    auto-rotate between tabs every few seconds? Like a slider? Maybe a slider would work better
    than tabs?"). The same three slides and the same three panels, with the strip of tabs
    replaced by a row of three dots — each one a fragment link to the panel it opens, so the
    block still switches with no script — and a pair of step controls the markup emits `hidden`
    and the site's script reveals. The script gains the clock (six seconds a slide), the wrap,
    the hold while a pointer or the keyboard's focus is in the block, the stop while the page is
    not being shown and the stop for a reader who has asked their system for less motion, and it
    writes the slide on screen to the block's `data-active` for the stylesheet to draw. **The
    block is only on the homepage, so its own bytes land there and nowhere else**: **2,186 bytes**
    onto the homepage's HTML — **1,054** of stylesheet and **1,132** of markup — and **13,211**
    onto the script, which every page loads once, so the page weight moves by the script alone and
    the site figure by both. The count of files in `dist/` stays **28**, the count of pages
    **11**, and the homepage's own words go 468 → **461** — a dot is a circle with no text in it,
    and the three labels the tabs showed are now the dots' own names, which a screen reader reads
    and the page does not draw. **All three figures are re-derived here**, from the gates' own
    build, in the commit that moves those bytes (D192), and **this round's three parts are the
    whole of the move**: the script 50,714 → **63,925 bytes**, the page weight 411,896 →
    **425,277**, and the site figure 1,412,642 → **1,429,917** — the 8 of the star, the 1,870 of
    Home, the 2,186 of this block's own markup, and the 13,211 of the script it added.
    `test/slider.mjs` holds the block's shape, the two states of the stylesheet, the target size
    of a dot and of a step control, and the behaviour itself — the file the site ships, run
    against the built homepage in a stub DOM — with each case planted against the line of the
    script it reads.

  - **The homepage slider's review round** (the review of the round above, 2026-10-21) is five
    corrections to that one block, and **the whole move is the block's own**: the script's bytes
    and the stylesheet's, with no page's markup rewritten and no page's words moved. `site.js`
    goes 63,925 → **66,189 bytes** (+2,264); the page weight 425,277 → **427,596** and the site
    figure 1,429,917 → **1,432,910** — the script's 2,264 once each, plus **55 bytes of
    stylesheet onto every one of the 11 pages** (605 in all), so the console page's HTML goes
    31,819 → **31,874** and the homepage's 50,629 → **50,808**, its own extra 124 being the
    block's own stylesheet. No file was added or removed, so the count of files in `dist/` stays
    **28** and the count of pages **11**. The five are:
    - **The Home link's padding at the wide width.** The rule setting the link's inline padding
      stood *below* the wide block at the same specificity, so the wide block's 8 px was dead
      code and Home kept its phone 4 px at every width while the three labels beside it in the
      same row opened to 8 — the opposite of what the rule's own comment said. The 8 px is in a
      media block after the base rule now, and `test/base-styles.mjs` holds the link's inline
      padding to the labels' own at both widths **and** the order of the two declarations, so a
      wide rule written back above the base one is refused rather than counted as present.
    - **The pointer's hold and the keyboard's hold are two flags, not one.** A single `held`
      boolean meant `pointerleave` re-armed the clock while the focus was still inside the block,
      and `focusout` did the same under a resting pointer: a reader who clicked a dot and moved
      the pointer off the block lost the panel they chose after six seconds. They are `over` and
      `inside` now, each releasing its own hold and no other, and a focus that moves from one dot
      to the next never leaves the block at all. `test/slider.mjs` gained the mixed ordering —
      pointer away under a focus, focus away under a pointer — which the round it replaces tested
      each hold in isolation and could not see.
    - **The reduced-motion query is watched, not sampled.** `calm()` re-read the query on every
      `arm()` but never subscribed to `change`, so a reader who turned less motion on while the
      block sat idle got one more advance before it settled — where the console slider and the
      menu both watch their queries already. Each block subscribes once, on its first arm, and
      only where the page is being shown, which is what keeps a stub that cannot answer
      `document.hidden` from being asked anything. `test/slider.mjs`'s stub answers a query's
      listeners now, and the new case turns less motion on and off under a running clock.
    - **A `:has()` selector no longer stands in a list beside plain ones.** The no-script
      first-panel rule listed a `:target` selector beside a `:has()` selector, and a selector
      list is not forgiving: a browser too old to answer `:has()` drops the whole rule — and
      because the build merges rules that carry identical declarations, the three *scripted*
      panel rules had been merged into that same rule, so such a browser lost the working slider
      too and not only the no-script state. The `:has()` selectors are in
      `@supports selector(:has(*))` rules of their own; **a plain split in the source is not
      enough**, because the minifier merges the two halves straight back, which is why the
      at-rule boundary is the fix. The same defect stood in the rule that marks the current dot,
      found by the new gate rather than by the review. `test/slider.mjs` now refuses any rule
      whose selector list mixes the two.
    - **The design record's specificity.** `docs/website/redesign/README.md` claimed the
      `:target` selector and the script's own rule were both (1,2,0) and the earlier would win.
      They are (0,4,0) and (1,2,0): the script's rule wins on the id whatever the order, and the
      `:not([data-active])` scoping is load-bearing for the reason the record gave — without it a
      still-targeted panel and the script's panel would both be displayed — rather than because
      of the order. The record's clock bullet and its `still()` reading are rewritten with it.
    `test/base-styles.mjs`'s band gate reads the new `@supports` rule through one narrow
    exemption, for that query string alone: a capability gate is not a width band, and every
    other at-rule is refused there as before.

  - **The four zoom levels are stills under the run** (the owner's brief of 2026-10-21, the
    zoom-diagrams plan's Part 4, D237/D244/D245) re-derived both figures from the gate's own
    build: the site figure 1,437,824 → **1,448,644**, by **10,820 bytes**, and **the per-file
    comparison of the two builds shows that is the whole of the move** —
    `dist/how-it-works/index.html` goes 40,775 → **51,595 bytes** and no other file in `dist/`
    moved a byte, so the count of files stays **28** and the count of pages **11**. What the
    page's own 10,820 are: the four `<figure>`s and the `<figcaption>` naming each level, the
    four `<svg>` drawings those figures carry, the one zero-size `<svg>` holding the single
    `<defs>` of five `<symbol>`s — the job tile, the orchestrator, the developer, the console and
    the project — that every level draws each entity through, the four anchor markers of the
    level list, and the scoped stylesheet the page inlines, because Astro inlines a component's
    rules into the one page that renders it. **The page weight does not move, and that is the
    check worth keeping**: the heaviest page is the console page, at 427,596 bytes, which renders
    none of this, while the How It Works page is 129,515 → **140,335**. The script did not move
    either — `public/scripts/site.js` is the same 66,189 bytes — and that is the shape the brief
    asks for: the four levels are markup and text, all four are in the document with no script
    at all, and the pinned scroll-driven zoom between them is Part 5, not this round. Nothing is
    fetched for any of it. The page's own words go 332 → **492**, inside D188's 500 and inside
    the ceiling `privacy`'s page derives, so no word ceiling moved. `test/how-it-works.mjs` holds
    the four levels — one `<defs>`, every `<use>` resolving, every symbol used, no level drawing
    its own copy of an entity, the markers, and every label of a level held to the drawing it is
    drawn from in both directions — a row of `website/data/zoom-levels.yaml` then, and the owner's
    own board since 2026-10-22, when the boards replaced the data file — and `test/a11y.mjs` holds
    the markers and the captions; the
    drawings themselves are the owner's to read, and that is Check 112 in
    `docs/gcp-live-checklist.md`. **Twenty-one of those bytes are the spaces at the end of the
    drawing's own labels**, which `test/spacing.mjs` asked for and the first full run of the suite
    is what found: the gate reads a built page the way a browser does, so two `<text>` labels
    written on adjacent lines are two words a reader reads as one (`workflowone`, `goneruns`), and
    the space belongs inside the run that carries it — which is what the security drawing's own
    labels do.

  - **The homepage's "See it" panel is a slider of the console page's own pictures** (the owner's
    brief of 2026-10-21, `docs/website/zoom-diagrams/spec.md`, Part 1 of its implementation plan;
    D246, D250, D251, D252), and **no picture is added**: `ScreenshotSlider.astro` is the site's
    one slider now — it takes its heading, its `data-slot` and the id prefix of the slides it
    names, and keeps everything else, so the console page renders it exactly as before — and the
    homepage's slot renders it over four rows of `website/data/home-screens.yaml`: the console
    page's own `console-spend`, `console-live` and `console-run` captures first, `alt` and
    `caption` copied from `console-screens.yaml` row for row, and the `fugaro watch` capture the
    panel has always shown, last, with the alt text and the transcription beside it it has had
    since PR #622. The script's interior hooks became `[data-slider-track]`,
    `[data-slider-slide]` and `[data-slider-dot]` in the commit before this one — the classes
    stay, because they are what the stylesheet draws — and the driver reads a hook only when
    `closest('[data-slider]')` is the block it started from, which is what keeps the homepage's
    own slider's panels out of the shared slider's reading and the shared slider's slides out of
    the homepage block's. **The move is three files and nothing else**: `dist/scripts/site.js`
    66,189 → **70,016** (+3,827: the shared slider's own 2,293 bytes and the 1,534 of the review
    round below), `dist/index.html` 50,808 → **56,969** (+6,161, the slider's
    markup and the shared component's rules) and `dist/console/index.html` 31,874 → **32,029**
    (+155, the same rows' hooks), so the site figure moves 1,437,824 → **1,447,967** by exactly
    those 10,143 — no file is added or removed (`dist/` stays **28 files**, the site **11 pages**)
    and no other file moves a byte. **The page weight is re-derived from another page**: the
    homepage fetches the three console captures and the watch capture now, so it is the heaviest
    page of the site, 563,219 bytes, where the figure before this round was the console page's
    427,596 (the console page is 431,578). The homepage's words go 461 → **563** — the three
    leading slides' `alt` texts, which are the console page's own — and D188's number moves with
    them, 500 → **600** (the reading is in the words ceiling below and in
    `docs/website/redesign/README.md`). `test/home.mjs` holds the slot to the rows the data file
    names in the order it names them, the three shared rows to the console's own file, the Watch
    row's declared size to the design record, and the whole slider to the no-JS page; the
    console's own gate, `test/console-screens.mjs`, runs unchanged against it. **The review round
    on this pull request added the gates for the second slider**: `test/slider.mjs` now runs
    `site.js` against the built homepage and drives the shared slider inside the "See it" panel —
    its four rows counted, marked and stepped, and its clock refused while the panel is
    `display: none` — and holds every hook read in the file to `own()`, which is what keeps a
    block from being handed a nested slider's elements by document order.

  - **The menu lost its third group** (the owner's feedback of 2026-10-21: "let's drop the top
    reference menu. We'll move the submenu console under product, and the rest go under
    Project"). The Reference group's `<li>`, `<details>`, `<summary>` and its list wrapper are
    gone from `NAV_GROUPS`, and the three links it held were moved rather than rewritten:
    Console's item into *Product*'s list and Docs's and Changelog's into *Project*'s, so the
    menu is two groups — *Product* (four items) and *Project* (five) — in front of the Home item
    the same day's earlier feedback put first, and the footer still picks its entries out of the
    same groups. **Every page carries the same move, because the menu is inlined into all of
    them**: **110 bytes off each of the 11 pages' HTML** (the removed block's own elements and
    their attributes, the three links themselves being moved and unchanged), **8 bytes off the
    script** (the two comments in it that counted three groups), which is **118 off the page
    weight**
    (the console page, which inlines the stylesheet, fetches the script and carries the four
    screenshots, is the heaviest page either way) and **1,218 off the site figure** — the 1,210
    of the pages and the 8 of the script. No file was added or removed, so the count of files in
    `dist/` stays **28**, the count of pages **11**, and no page's words move: a menu label is
    chrome, and the words the ceiling counts are `main`'s. The three budget figures below are
    **left where they are**: the owner's ruling of 2026-10-20 is that they are re-derived when
    the owner asks rather than on every round, so this move is stated here and the figures stay
    the ones the round above derived. `test/base-styles.mjs`'s `navFailures` holds the two
    groups by name and by the routes each lists, and refuses a third, a renamed group and a
    route that has left its group, and `test/nav-hover.mjs` reads the two lists the menu
    draws; `base.css`'s phone cut is left at the width the four-item row was measured at.

  - **The phone app's page** (the owner's brief of 2026-10-21, the zoom-diagrams plan's Part 3;
    D239, D240, D241, D242) adds one page and moves nothing else: **the site figure moves by
    24,858 bytes** — the new `dist/mobile/index.html` (24,440) and **38 bytes onto each of the
    11 pages that existed**, which is the menu's one new item, inlined into every page — and no
    file is removed, so the count of files in `dist/` goes 28 → **29** and the count of pages
    11 → **12**. **The script did not move at all** — `dist/scripts/site.js` is 70,008 bytes in
    both builds — and that is the shape this page has: it is a badge and a list, with no
    behaviour and no picture. The page weight does not move either, and that is the check worth
    keeping: the heaviest page is still the homepage, which renders none of this, and the phone
    page's own weight is 116,999 bytes (HTML 24,440, of which 17,529 is the stylesheet Astro
    inlines into it, and the 92,559 it fetches — the script, the logo and the icons, which every
    page fetches). The page's words are 139 and its lead 26, inside D188's 600 and the 40-word
    lead, so no ceiling moved.
    What the page is, and the four answers that decide it: a **preview badge** over the headline
    (D240's third condition), **every bullet marked** shipped or planned, the two states rendered
    as two lists with their own headings and the mark repeated on each card (D240's second
    condition, D241's answer — *shipped* is status and spend, *planned* is the roster, switching
    by name, the wake word, QR pairing and the three roles), the **wake word's row saying where
    the listening and the recognition happen** (D242: on the phone, and the page names no speech
    service and no cloud, because the local/remote switch is design only and not built), and
    **no date anywhere** (D240's first condition) and **no waitlist** (D239: no form, no
    `mailto:`, no third party — the page asks a reader for nothing). The claims are
    `data/mobile-claims.yaml`'s rows, rendered one for one, and the page refuses to build with
    either state's list empty: a heading over nothing reads as an app with nothing left to
    build. **No screenshot is added** (D251: the owner's captures arrive as files, and no agent
    commits one), so `data/mobile-screens.yaml` ships with no row and the page renders **no
    slider at all** — no section, no frame and no picture-shaped hole, the console page's own
    empty-list case; the file is handed to the same shared component the console page and the
    homepage render, so a row is a slider with this page's own slot and id prefix and nothing in
    the page or the component moves. The menu gains **one item, Mobile, as *Product*'s fifth**,
    so the two groups stay two and no third group is added.
    `test/mobile.mjs` is the page's own gate, and it reads the **built** page: the claims against
    the data file's rows (with plants that move a planned row into the shipped list and a shipped
    row into the planned one), the badge (absent, reworded, or below the headline), a four-digit
    year and every month name in the page's own text, its title and its description (`in 2027`
    and `March` are the plan's two plants), the voice row and the speech services it may not
    name, the roster printed among the shipped claims **while `mobile/src/feed/paths.js` denies
    the phone the `agents` family** (the one reading pinned to a repository file, derived from
    that file at both ends, so a grant that names the family turns the reading off rather than
    leaving it stale), and the form, the `mailto:` and the third-party link D239 refuses. Its
    fixture builds are the other end of each branch — a row in the screenshots file renders the
    slider with `mobile-screen-` ids and the console's ids do not leak onto the page, and the two
    refusals the data file itself carries (a state that is neither of the two, a file with no
    planned row) stop the build naming the file. The page's words, its lead and its links are
    held by `budget.mjs`, `linkcheck.mjs` and `a11y.mjs` like any other page's, rather than by a
    second rule here. **The three budget figures below are left where they are**, by the same
    owner's ruling of 2026-10-20 as the round above, and the page's drawings are the owner's to
    read: that is **Check 113** in `docs/gcp-live-checklist.md`.

  - **The menu gained Why GCP, and the site gained its twelfth route** (the owner's brief of
    2026-10-21, `docs/website/zoom-diagrams/`, Part 2 of its implementation plan; D238, D243,
    D248, D249). The item is *Product*'s second, between How it works and Getting started, and
    the menu is inlined into every page, so it is **40 bytes onto each of the eleven pages that
    were already there**; the new route's page, `dist/why-gcp/index.html`, is **23,651 bytes**;
    and the FAQ page is **753 heavier** — the same 40, plus the 713 of the seventh answer it
    renders — so the build's whole-site size moves 1,463,812 → **1,488,616** (+24,804) and the
    artifact goes **28 → 29 files**, **11 → 12 pages**. **The script does not move a byte** (70,008
    before and after): the page is claims and their sources in markup, and what holds them is
    the suite rather than the browser. The page's claims live in `website/data/why-gcp.yaml` —
    one row per claim, each with the file in this repository that proves it and the day it was
    last read against that file, which is what lets a reader date a claim about what another
    cloud offers (D243) — and the one sentence the page carries about the other clouds is the
    sentence D249 requires it to carry, held by `test/wording.mjs` together with the phrases the
    page may not be made of. The FAQ's seventh answer is the reader's own question ("Do I need
    to be on GCP?"), the two answers whose detail lives on this page link it by name, and
    `test/faq-community.mjs` holds both pages: the FAQ's seven entries to their data file, and
    the Why GCP page to its rows, in the file's order, with the file each row names linked at
    the branch `ci.yml` pushes to. **The three budget figures below are left where they are**
    (the owner's ruling of 2026-10-20: they are re-derived when the owner asks, not every
    round), so this round is stated here and the figures stay the ones the round above derived.
    The whole-site derivation's own base is stale, and this round is where it is worth saying
    so: it names 1,447,967 bytes across 28 files, where this build is 1,488,616 across 29 —
    this round's 24,804 and the 15,845 the checkout was already over the stated figure before
    it. The ceilings are unaffected either way, being three times the derived value rather than
    the measurement.
    Measured against the base this round and the phone app's share, which are the same base
    (28 files, 11 pages): on the merged tree the two rounds together leave `dist/` at **30
    files** and **13 pages**, and *Product* holds **six** items — this round's *Why GCP*
    second, the phone app's *Mobile* last — so the bullet above's "fifth" is where that round
    put it, not where it sits now. Since the owner's approval of 2026-10-23, *Why GCP* is a *Project* item,
    after *Security*, and *Product* holds five.


  - **The four zoom levels are the owner's four approved boards, redrawn as the site's inline
    SVG** (the owner's brief of 2026-10-22, the zoom-diagrams plan's Part 4 redone; D237, D244,
    D245, D247, D248, D249). Part 4's four levels are **gone**: the owner's reading of them was
    that those diagrams were terrible, and the drawings are the four boards in
    `website/design-ref/zoom-levels/level1.dc.html` … `level4.dc.html` — 1280 × 720, drawn in
    Claude Design, committed by #869 — reproduced as the site's own SVG: the same boxes, the same
    labels **word for word**, the same arrows including the bent and the double-headed ones and
    their small labels, the same colours, and the boards' landscape aspect kept (`viewBox="0 0
    1280 720"` over the content width). **The move is one file**: `dist/how-it-works/index.html`
    51,560 → **73,899 bytes** (+22,339) and no other file in `dist/` moves a byte, so the count of
    files stays **29** and the count of pages **12**. The script did not move —
    `dist/scripts/site.js` is 70,008 bytes in both builds — and neither did the homepage's HTML
    (56,898 in both), which is why **the page weight does not move either**: the heaviest page is
    still the homepage, at 563,219 bytes, and it renders none of this. What the page's own 22,339
    are: the four boards redrawn as four `<svg viewBox="0 0 1280 720" role="img">` drawings, the
    one zero-size `<svg>` holding the single `<defs>` — **17 `<symbol>`s** (`zoom-job`,
    `zoom-orchestrator`, `zoom-developer` and `zoom-developer-planned`, `zoom-person` and
    `zoom-person-planned`, `zoom-card`, `zoom-card-accent`, `zoom-card-planned`, `zoom-laptop`,
    `zoom-phone`, `zoom-project`, `zoom-loop`, `zoom-draft-pr`, `zoom-firebase`, `zoom-cloud`,
    `zoom-box`) and **four `<marker>` arrow heads** — the boards' labels as real `<text>` in the
    site's font stack, the four `<figure>`s
    with the `<figcaption>` naming each level, the four anchor markers of the level list, and the
    scoped stylesheet the page inlines, because Astro inlines a component's rules into the one
    page that renders it.
    **The page's words go 492 → 868** — the boards' own labels, which are what a level says — and
    **D188's number moves with them, 600 → 900**: 900 is the smallest round ceiling above the
    page, and the next page down that D188 governs is the homepage at 563 words (Getting Started
    is 309; the privacy statement, at 1076, is the page the words-per-page ceiling itself is
    derived from, so D188's own number does not measure it). The
    levels' words are no longer a data file: `website/data/zoom-levels.yaml` is gone and the
    boards are the source of truth — `src/lib/zoom-boards.mjs` reads each board's visible strings
    at build time and `holdZoomBoards` refuses a build whose drawing and board disagree, **in
    both directions**, so a label a board carries and the drawing dropped fails the build the way
    a line the drawing wrote for itself does. Every entity is drawn **once**, through the shared
    `<symbol>`s and their `<use>` references (D244): the same shape for the same entity in all
    four levels, no level drawing its own copy, and no symbol left unused. `test/how-it-works.mjs`
    is the page's gate and reads the **built** page: the boards' strings word for word (extracted
    from the design-ref files, which are the source of truth), the `<use>` rule, **no two labels
    drawn over each other and none leaving the board** (the text boxes are computed from the
    written attributes), and **every arrow's endpoints on a card's edge or on the corner of its
    own chain** (the path data is parsed) — each with a plant that proves it refuses, and the
    arrows and the labels are the whole of what a reader can check without eyes, since the model
    that wrote this cannot see the boards. `test/a11y.mjs` holds the four levels as four named
    anchors a keyboard reaches in order (D245: `<a href="#level-N">` in their own `<nav>`) and
    each level as **one named picture** (`role="img"` with its own `<title>` and `<desc>`), and
    `test/spacing.mjs` reads the boards' labels as labels rather than as one word run together
    because the drawing's own text class is a block. The dashed outlines still mean **planned**
    and the legend saying so is still on the page, above the levels (D248/D249's wording rules
    hold: no "disposable" anywhere); the levels are stills in the no-JS and reduced-motion
    rendering, all four of them in the document; and the pinned scroll-driven zoom between them is
    Part 5 and not this round, so its selectors and ids — `.zoom`, `#level-1`…`#level-4`,
    `.zoom-svg`, `.zoom-caption`, `.zoom-nav` and `.zoom-marker` — are kept as they stand.
    **The site figure is re-derived here, and only it**: the pair moves 1,447,967 → **1,512,120**
    bytes, of which this round's own move is the 22,339 above and the remaining 41,814 is drift
    earlier rounds left un-derived (the phone app's page and the boards' own commit among them) —
    measured against the gates' own build rather than argued, and stated here so the figure a
    reader sees is the build the site has. The per-page and per-script figures are left where they
    are, by the owner's ruling of 2026-10-20, and they carry the same drift: the script's figure
    was stated as 70,016 where the build was 70,008 and the page weight as 563,219 where the
    homepage's is 563,140 — the script's own figure is re-derived in the bullet below, with the
    bytes G2.3 and G2.4 added to it. The drawings are the owner's to read, and that is
    **Check 112** in `docs/gcp-live-checklist.md`.

  - **JavaScript per page:** 360,327 bytes. Three times 120,109: the one script the site ships,
    `public/scripts/site.js` (96,087 bytes), plus 25 %. What the file carries is the copy
    control, the menu, the consent gate — the site's, and since G2.3 the campaign's own gate and
    the three loaders behind it — and **both of the site's sliders** — the console page's,
    which the feedback-B round above added, and the homepage's, which the feedback-A round above
    it added. **G2.4's key events and its pass-through are the last 7,141 bytes, and the script's
    figure is re-derived with them**: the build the derivation names has moved 88,946 → 96,087,
    and the ceiling with it, 333,549 → 360,327. The drift the rounds before it left in this
    figure — the bullet above records the build as 70,008 against the 70,016 it stated — ends
    here, in the commit that moved the bytes; the page weight keeps its own until a round
    re-derives it.
    The console's half is the feedback-B round's, and it was the first JavaScript the site had
    gained since the menu (the owner's feedback of 2026-10-21): the bar the markup emits
    `hidden` — a Previous arrow, the count and a Next arrow — is revealed and driven here, the
    count (`n / N`) and the dot the reader is on are written from the track's own scroll
    position (so a trackpad swipe reads the same as a press), an arrow or a dot press moves the
    track round its ends, and a slide advances every six seconds while nobody is reading —
    stopping for a pointer over the slider, a focus inside it, a tab in the background, and a
    visitor whose system asks for less motion (`prefers-reduced-motion: reduce`, read at load
    and watched after it). **The review round above is that half's last 1,153 bytes**: a move the
    browser may not animate (`behavior: 'instant'`, since `'auto'` is the track's own
    `scroll-behavior: smooth`) and a first reading taken from the track rather than from the
    first slide. `test/console-screens.mjs` runs the file in `test/lib/dom.mjs`'s stub browser
    to hold that half, and refuses a planted defect in each part of it. **The homepage's half is
    the feedback-A round's 13,211 bytes, and the last 1,534 of them are this pull request's own
    review fix**: the guard that keeps a track the page
    is not laying out out of the count (the "See it" panel is `display: none` until its dot is
    pressed, and the count and the dots were advancing inside it), the block's two step controls
    read through `own()` like every other hook, and the gates that run the shared slider as this
    page renders it. The 2,293 bytes before the review round are the hooks and the `own()` rule
    themselves — the three interior hooks are
    `data-slider-*` rather than the console page's class names, and the driver keeps a hook only
    when `closest('[data-slider]')` is the block it started from, which is what lets one file
    drive two blocks that carry the same hooks.
    `test/slider.mjs` runs the same file against the built homepage in a stub DOM of its own, to
    hold the clock, the wrap, the two holds and the mark on the dot, the two blocks' hooks read
    through `own()`, and **the shared slider as this page renders it** — the four rows it counts,
    marks and steps, and the beats it refuses while its panel is `display: none` — each case
    planted against
    the line of the script it reads. **The two halves share the four names they have to agree on
    and nothing else** — the block's attribute, the two step controls, the six seconds and the
    motion query are declared once, with the console's section, and each half drives its own kind
    of block: a scroll container whose position is read back, and a set of panels the block names.
    What the file carries of the menu, in
    the detail `test/nav-hover.mjs` holds it
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
  - **Page weight per page:** 2,112,072 bytes. Three times 704,024: the heaviest page, the
    homepage, at 563,219 bytes (its HTML, the four screenshots it renders, and the files
    it fetches: the script above, the logo and the icons), plus 25 %.
  - **Whole site:** 5,670,450 bytes. Three times 1,890,150 (every file in `dist/`, 1,512,120
    bytes across 29 files, plus 25 %) — the gates' own build of the site, which every checkout
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
    one enforced). On the homepage today the first `h2` is the slider's own line over its
    control, *Say it. Get pull requests.*, and the region is the hero's lead paragraph and
    its row of links: **16 words**. Two things between the two headings are taken out of the
    count, and both are named: the hero's illustration — `figure.hero-shot`, the terminal
    card the bold redesign draws beside the copy, which is a picture with a caption rather
    than the page's opening prose, and which `test/fold.mjs`'s rule 1 takes out of the lead
    region in the same way (the four campaign heroes carry the class on the console screen
    they show rather than on a drawn card, `CampaignHero.astro`, D411, and it is a picture
    there for the same reason) — and the block the first `h2` belongs to, which is how the
    slider's tab labels stayed out of the region when the block's heading moved above them.
    Before the bold redesign the same region held the request the hero printed under the
    headline, the promise, its three calls to action and the slider's three tab labels: 30
    words. A
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
  - **Words per page:** 1345 words. Every word a reader sees inside `main` — command blocks
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
    site's pitch; the number is that page's own count on the gates' own build — **1076
    words** — plus 25 %, rounded up, which is **1345**. The count moved six times since
    G2.3: the campaign section took the page 988 → 1030, the owner's ruling of 2026-10-29 on
    the banner's wording — one question, worded for everything the site may load, with the
    refusal's reach stated in the consent card — took it 1030 → 1144, G2.4's sentence
    naming the campaign pages' three key events took it 1144 → 1213, the owner's feedback
    of 2026-10-30, which cut the banner to two sentences and the statement's website part to
    what a reader has to be told, took it 1213 → 1069, naming LinkedIn, whose insight
    tag the owner switched on with the Partner ID of 2026-10-31, took it 1069 → 1073, and
    naming Meta, whose pixel the owner switched on with the pixel id of 2026-10-31, took it
    1073 → 1076.
    `budget.mjs`'s
    `wordsCeilingFailures` holds the derivation to the built page the way the same file
    holds the byte budgets to theirs, so a sentence added there re-derives this line in the
    commit that adds it. **The raise is that page's and nobody else's**: every other page of
    the site stays within D188's own number, and the same function holds them there, so this
    number is a ceiling one page reaches and not a licence the rest can grow into. (D188's
    number was itself set against the site as it was measured before the redesign —
    `docs/website/redesign/reconciliation.md` §6: faq 493 words, caps-and-costs 487,
    getting-started 431, privacy 277, community 268, every one of them over it — and the
    redesign brought every one of those pages but the privacy statement inside it.)
    **D188's number was 250, the owner's feedback of 2026-10-19 moved it to 300, the
    bold redesign of 2026-10-20 moved it to 500, the 2026-10-21 brief moved it to 600, and the
    2026-10-22 brief moved it to 900.**
    The 2026-10-19 brief asks the homepage for
    a benefits section, which is a fourth required
    block on the one page that already carried the three the briefs before it require — the
    promise, the chat (changes spec item 3) and the flow (item 4) — and the four together
    measure 294 words. The bold redesign then builds the owner's approved board
    (`design-ref/HomeV3.dc.html`), whose homepage is five blocks — the hero, the slider, the
    run band, the grid of what a run leaves behind, and the closing call to action with the
    install command — and the five together measure **468 words**. The 2026-10-21 brief
    (`docs/website/zoom-diagrams/spec.md`) then asks the "See it" panel for a slider of four
    slides — the console's own three screenshots first and the `fugaro watch` capture last
    (D251, D252) — and the three pictures that lead it are the console page's own, so they
    carry the console page's own `alt` texts: one picture has one account of what it shows,
    and an image's `alt` is text a reader reads. **The four slides are the whole of that
    move**, 461 → **563 words**, and the page's blocks therefore measure 563. The 2026-10-22
    brief then replaces the four zoom levels on How It Works with the owner's four approved
    boards (`design-ref/zoom-levels/level1.dc.html` … `level4.dc.html`), redrawn as the site's
    inline SVG: a board's labels are strings the drawing renders, and a string the drawing
    renders is text a reader reads, so the four levels' labels are words on that page the way
    the four slides' `alt` texts are words on the homepage. **The boards' labels are the whole
    of that move**, 492 → **868 words**, and that page is therefore the one D188's number is set
    against. D188's number is the smallest round ceiling above the page that carries every block
    its briefs and its boards require, and it is still a ceiling rather than a
    target: the next page down is the
    homepage at 563 words (Getting Started is 309), and a page that wants the extra room has to
    carry a brief's own block for it.
    Every raise is recorded as a reading in `docs/website/redesign/README.md`, the way every
    number the briefs do not spell out is.
- `test/fold.mjs` — the ordering the fold claim rests on (P5, RF5). Whether the promise and
  the first command fit a real 390 × 844 first screen is the owner's live check (70b) and the
  word ceilings' business, and no test here sees a viewport (D187). The ordering underneath it
  is a statement about the built pages, though, and it is this file's rules: the homepage's lead
  region holds the request the hero renders under the headline, the promise, the row of links
  under it and the links in that row — and the `h2` it stops at is the slider's own first panel
  title: the block the page puts directly under the promise is that heading's own block, sitting
  between the `h1` and the `h2` that opens it, so the gate takes those elements out of the region
  before reading it. The slider and the install block are the sections that open after the lead
  region;
  Getting Started's install section opens with its commands, with the
  sentence about them still there after the paths; and on every route that prints a command —
  three of the thirteen today, and the other ten are skipped rather than asserted about — the
  first `h2` precedes the first `pre`. Its fourth rule is the owner's feedback of 2026-10-19:
  Getting Started is three bands in the order a reader reads them — `#install` (the command
  and the console), `#setup` (what a real installation needs, an `ol` whose every step names
  the document behind it), `#details` (everything else) — so the two things a reader came for
  cannot drift back down a page that grows. The whole gate is one function over one `dist`, so its
  refusals are kept the way the other gates keep theirs: a case writes a synthetic `dist` with
  each defect planted beside a page that keeps every rule, and each rule was mutation-proved by
  removing it from the function and watching that case fail.
- `test/os-cards.mjs` — the install cards on Getting Started (the owner's draft of
  2026-10-19, "Getting Started, draft 2 (OS icons)"). Two rules about the shape no other gate
  can see: **no card names a system `data/install.yaml` does not carry** — the card's
  `data-platform` is held to the file's platform list, its heading to the file's own name for
  that system, and its path to the path the file offers that system *first*, so a card under a
  system the file has never heard of, or under the Windows entry it marks `untested` (D94), or
  carrying a route the file does not offer that system, is refused — and **every card is drawn
  with exactly one icon**, `aria-hidden`, out of the tab order, drawing a shape, referencing
  nothing, and distinct from every other card's. The empty drawing is the refusal no other gate
  can make: an `<svg>` with no shape in it is an element that is present, named, decorated and
  clean, and it renders as an empty box. The file the cards are held to is the site's own
  reader of it (`content.ts`'s `loadInstallFacts()`), not a second parser in the gate. Its
  refusals are kept with fixtures that plant one defect each beside a fixture page that keeps
  every rule, and with two builds of a copy of the repository for the states the page's own
  frontmatter refuses: a supported system with no path under it, and one this page has no glyph
  for.
- `test/meta.mjs` — the provenance-phrase gate (D186): a page must not explain why its own
  text exists, so D186's phrase list (`rendered from`, `generated rather than written`, …) is
  refused in the text a reader reads, on every built page. It is the one defect the other
  four cannot see — a sentence about the build is short, well-formed and honest — and it is
  the sentence the redesign removes. Naming a document is not provenance text: the docs hub
  names each guide's file, a page may link to `SECURITY.md`, and the changelog renders the
  release files' own lines, which is why D186 chose an explicit list over a rule about
  naming a repository file. It holds a second rule of the same kind, because a phrase list
  cannot hold it: **no page may show a generated file's banner as text.** The generated files
  under `content/` — `cli-reference.md`, `go run ./ee/clidoc`'s output, and `security.md`,
  `go run ./internal/clidoc`'s; `downloads.json` beside them is hand-kept and carries no
  banner — open with `<!-- generated by …; do not edit -->`, W1.3's pipeline reads the
  banner as a fact and drops it from the body it renders, and RF8 takes "the banner is never
  on a page" as a premise — which was false on the docs hub, where the rendered CLI
  reference carried it through the markdown compiler and the page's own `escapeRawHtml`
  showed it to a reader as `<!-- generated by go run ./ee/clidoc; do not edit -->` above the
  reference's title.
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
- `test/privacy.mjs` — the privacy statement's software part, and the eight rules that keep it
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
    says the software talks "to nothing the Fugaro project operates". Every exception is recorded
    in the test by name rather than inferred from a filter, and there are three: a Go test whose
    fixtures test the boundary rule itself, and two console files — `onboarding.ts`, which holds
    the console's own link to the project's website (a URL handed to the reader's own browser and
    never a request the console makes), and `window.ts`, which carries only the host in
    `ALLOWED_EXTERNAL_HOSTS` so that link may open — neither of which may carry a request call of
    its own;
  - **the contact route and no address** — the contact section links the issue tracker and
    `SECURITY.md`'s reporting route and nothing else, and the page carries no e-mail address, by
    the owner's ruling that there is no address to publish; and
  - **no terms page** — no built page links to `/terms`, and no sitemap the build produced names
    one, which is the owner's ruling of the same day written where the link would have gone; and
  - **the look** — the page wears the bold redesign out of the shapes `src/styles/base.css` draws
    and invents none: the headline is on the display scale with its word in `.gradient-text`, the
    two part headings are on the display scale, every subsection is one of the site's cards, the
    hero's glow is decoration a reader is not handed, and every look class the page names is one
    `src/styles/base.css` draws and none of them is drawn by a rule of the page's own. **The
    shared file is read, not the built page alone**: `base.css` is inlined into every page, so the
    page's own rules and the shared ones sit side by side in one `<style>` where nothing can tell
    them apart, and a rule that counted either as a shape would admit the very page this rule is
    for. A name with no rule behind it draws nothing; a rule of the page's own is a second look.
    What is held is the shape of the look and not its rendering: a gate cannot see a page, and
    the match to the owner's boards (`website/design-ref/`) is the owner's check at the preview.

  Each rule is one exported function with its own case, and each has a second case that plants
  the defect it refuses — a lost part, a part nested under the other, a dependency that collects,
  a reporter the page does not name, a page naming a reporter nothing in the tree holds, a third
  secret, an endpoint pointed elsewhere, a source naming the host, a third contact route, an
  address, a `/terms` link, a page with no headline, a headline off the display scale, a gradient
  that left the headline, a part heading off the scale, a subsection outside a card, a glow a
  reader is handed, a look class nothing draws, a look class the page draws itself — and watches
  the named refusal come back. The word ceiling's own derivation — the
  privacy statement's own count plus 25 %, the number `website/README.md` states, and every
  other page of the site still inside D188's 500 — is `budget.mjs`'s, beside the reader that
  measures it and the byte figures it is a sibling of.
- `test/stacking.mjs` — the header paints above the page's blocks (the owner's reading of
  2026-10-19: "How it works: menu hidden by the blocks on mobile"). `tokens.css` names the
  site's two layers — `--z-header` for the header's row and the panel a group hangs below it,
  `--z-chrome` for the reader's own chrome above the page — and this gate reads them out of the
  **built** stylesheets: the header is positioned and carries the header's layer, which is what
  makes it a stacking context; the skip link and the consent banner carry the chrome's layer
  and nothing else does; and **no other rule declares a `z-index` at all**, which is the only
  thing a content component would need to paint over an open menu. The defect it was written
  for is the diagram: the retired four-step flow's steps were `position: relative` — that is
  what their arrows were placed against, and the homepage's slider is the positioned block that
  stands where they stood — and a positioned box with no `z-index` paints with every other such
  box in tree order, where the header is the first and a block drawn after it covers the menu on
  the pages that carry a positioned block. (The owner's ruling of 2026-10-20 retires the flow
  and `ArchitectureDiagram.astro` with it; the diagram's own history stays in
  `test/stacking.mjs`'s note.) `base.css` gives `.site-header` the
  header's layer instead, in one rule rather than one per component, and this gate keeps it
  there. Its refusals are kept the way the others keep theirs: a fixture page carries the scale
  and the three layered rules, and a case plants each defect — a header with no layer, a header
  a layer cannot apply to, a content block with a layer of its own — and watches the named
  refusal come back.
- `test/caps-figures.mjs` — the caps page's figures survive a phone (the owner's feedback of
  2026-10-19: "these render terrible on mobile", read on the page's right-hand part, the cost
  figures), as the bold redesign of 2026-10-21 left them. The page's two tables — what a job
  costs and the aggregation's per-recipe ranges — were both read as tables at 360 px: the first
  inside an `overflow-x: auto` box, which is six nowrap columns scrolling sideways inside their
  own box, and the second with no box at all, whose minimum content width is what widened the
  document. The tables are gone: the figures are three job cards now, so the stacking rules, the
  `data-label` on every cell and the one rule that printed them went with them, and this gate
  was rewritten around the cards rather than kept — a gate whose refusals cannot fire reads like
  one. The defect each rule is for is unchanged: every figure is under the name it is for (a
  `<dt>` before its `<dd>`, a name that says something, a figure that prints something, and no
  two figures in one card sharing a name); a phone reads one column of cards, read out of the
  page's own `grid-template-columns` at 360 and at 390 px rather than assumed from a class name;
  the labelled row wraps rather than pushing its figure out of the card; nothing in force at
  those widths holds a figure on one line or in a scroll box; no box is wider than `main`'s own
  content box at 360 px (read out of the stylesheet, not typed); the rows are no smaller than the
  body, which is the size `base.css` would otherwise leave a table at; and the three cards are
  side by side above this page's own 60 rem breakpoint, read again at 960 and at 1280 px, so a
  block that was deleted or moved behind a wider query is refused rather than green on every
  phone width. Two rules were added when the bars arrived: **the bar's own sentence names the
  marks the bar draws, and only those** — the bar is a picture with no text a reader can see, so
  its `aria-label` is the whole of what a reader who cannot see it is told, and the review job
  has no middle half and no worst run, so its bar draws neither; and **the page's two hero
  actions are 44 px targets**, read by `test/lib/controls.mjs`, the rule the homepage's own
  controls are held by as well. Its refusals are kept the way the others keep theirs: every
  defect it is written for has a case that plants the one thing it names in the artifact's own
  bytes — a card without a name or a list of figures, a figure with no `<dt>` before it, two
  figures under one name, a card with no bar, a bar with no accessible name, a bar whose
  sentence drops a mark it draws or names one it does not, a hero action under the 44 px target
  or with no minimum at all or with one that holds only above a breakpoint, the grid left at
  three columns at a phone's width, a row that cannot wrap, the nowrap figures and the scroll
  box in force at the phone's width, a box wider than the phone's content box, the rows at a
  table's smaller type, and the wide layout deleted or moved — and each case watches the named
  refusal come back.

**Lighthouse is the owner-run check** (D105's targets: performance ≥ 95, accessibility 100,
best practices 100, SEO 100), run against the built artifact by hand: there is no CI on pull
requests and the tests here are hermetic, so a browser-driven audit is not one of them.

The site's other gate is the Go side, which holds every fact to the file it is derived
from:

```sh
GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc/... ./ee/clidoc -run 'Docs|Lint|Website|CliRef|CoreCommands' -count=1
```

**The `Website|CliRef` half is not decoration.** The plan's baseline for this suite is
`-run 'Docs|Lint'`, and that pattern selects **none** of the site's own pins: of the 53
tests it selects in `internal/cli`, none is named `TestWebsite*` or `TestCliRef*`, so a
run of it alone is green with every fact the site renders unexecuted. `Website` selects the
boundary test, the install pins and the security page; `CliRef` selects the reference's
pins — in `internal/cli`, in `internal/clidoc/cliref` where the renderer's own rules are
checked, and in `ee/clidoc` where the committed file is held to the tagged tree's output —
and `CoreCommands` selects the core's subset check of that file, which is named for the rule
it applies rather than for the file it reads. The names are the ones the docs-only guard in
`ci.yml` already uses for the same tests.
`website/test/readme.mjs` holds this command to that: every test function the site's Go
test files declare has to be selected by the pattern this section prints.

## What is generated, and what must not be edited

The generated files under `content/` are **never edited by hand**: `cli-reference.md`,
`security.md` and `subscription-terms.md` are generated documents and `costs.json` is the owner-run aggregation's output.
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
| `content/cli-reference.md` | the command tree the released binary builds, `internal/cli.NewRootCmd` | `go run ./ee/clidoc` |
| `content/security.md` | `SECURITY.md`, byte for byte below the title | `go run ./internal/clidoc` |
| `content/subscription-terms.md` | `docs/licensing/subscription-terms.md`, sections 1 to 24 with the drafting notes cut, under a draft status line | `go run ./internal/clidoc` |
| `content/costs.json` | `fugaro stats runs --project <installation> --json`, and it is the aggregation's output verbatim | owner-run: `fugaro stats runs --project <installation> --json > website/content/costs.json` |
| `content/downloads.json` | hand-kept, not generated: the console's facts and the release's own published names, with `{version}` where the version goes — the URLs are derived from them (below), and `available` follows `site.yaml`'s `features.console_downloads` | none: hand-kept |

Three things about those commands are worth stating rather than leaving to the table:

- **One generator per file.** `go run ./internal/clidoc` writes the security and
  subscription-terms pages from their sources, and `go run ./ee/clidoc` writes the CLI
  reference from the tagged command tree — the tree the released binary builds (design
  `docs/design/ee-boundary-moves.md` §9). Two generators, one file each, so neither can
  overwrite the other's output. Each compiles into the build cache and writes nothing into
  the tree besides its output.
- **The costs file is the owner's to produce, and it may be absent.** The aggregation reads
  one named installation's run records, is refused inside a coding agent's session, and
  prints percentiles per recipe with the window and the sample size behind each — never a
  repository, a run id, a branch, a task, a person or a commit (D96, D97). Until the owner
  runs it, the file is absent — and the caps-and-costs page renders nothing from it: the
  per-recipe ranges section that read it was removed on 2026-10-21 (the owner's ruling), and
  `src/content.ts` still validates the file, so the day one is published the build refuses a
  group below `min_runs` rather than publishing it. An empty figure is honest, an invented
  one is not.
- **`website/data/*.yaml` is hand-written, and pinned by tests rather than regenerated.**
  `install.yaml` is held to `README.md`, `docs/release.md` and `.goreleaser.yaml` by
  `internal/cli/docs_website_install_test.go`, and `site.yaml` to `go.mod`, the
  repository's decisions and the site's own host list by the boundary test. Change the
  fact in the repository, then the value here, in one commit. `site.yaml`'s
  `features.console_downloads` is held to the hand-kept `content/downloads.json`'s
  `available` by the build itself, which refuses the two disagreeing (below).
  `console-screens.yaml` is the third, and it is the owner's own list rather than a fact
  another file states: it ships empty, and the build renders whatever rows it carries (below).
  The four zoom levels' words are **no longer a data file**: since the 2026-10-22 brief the
  levels are the owner's four approved boards (`design-ref/zoom-levels/*.dc.html`) redrawn as
  the site's inline SVG, and the boards are the source of truth for the labels, so
  `website/src/lib/zoom-boards.mjs` reads a board's visible strings and `holdZoomBoards` stops
  the build, naming the file, when the drawing says a word the board does not;
  `test/how-it-works.mjs` reads the same boards the other way round — a line the drawing wrote
  for itself, and a string of a board the drawing dropped, are each refused by the name of the
  board. A data file was a second copy of a board's text, and a second copy drifts.
- **`website/data/typical-costs.yaml` is the owner's own measurement, and the page renders
  it or nothing.** It carries what a job costs on this project's own development runs — one
  entry per kind of run, each with the sample behind it, over a window whose last day is the
  day the figures were taken — and the caps-and-costs page leads with it (the owner's brief
  of 2026-10-19). `src/content.ts` reads it, validates it (a version, a window of at least
  `site.yaml`'s `min_runs` runs, an ascending middle half, no duplicate id) and refuses it by
  name, and `test/caps-and-costs.mjs` holds the built page to it **in both directions**: every
  field the file carries is rendered in the field it is for — the card's own line or its
  labelled row — and no figure on the page is one the file does not carry. So a refresh is an
  edit to this file and never to a page, and a field added to it with no place to land fails
  the build rather than going unrendered.
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
(`console-vX.Y.Z`) and from `site.yaml`'s `repository` — that module is the one reader of the
file, and each page renders what it returns. **The version is the console's own**, not the CLI's
tag the 0.9.0 release happened to share: the console releases on its own stream since 2026-10-23
(`docs/release.md`, "Console releases"), so `release.version` is `console/package.json`'s
`version`, bumped on `main` before the `console-vX.Y.Z` tag, and the release's notes are
`docs/releases/console/vX.Y.Z.md`.

Three places offer the console from those facts: the console page's own Downloads section
(W1.6), Getting Started's console option inside the macOS install card (the owner's changes spec
of 2026-10-18, item 6; the card placement is the owner's draft of 2026-10-19), and the
homepage's call to action (the owner's brief of 2026-10-19, item
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
(`FUGARO_GATES_DIST`), because rendering the console's block costs words and Getting Started
was at the ceiling D188 set when the console shipped — 250 words then, 300 since the owner's feedback
of 2026-10-19 moved it, 500 since the bold redesign of 2026-10-20 moved it again (above) — and the
owner's flip has to be the whole change and may not be the
change that breaks the ceiling. A data file that says the console has shipped while the flag says it
has not stops the build, naming both files: they are one fact.

**The flip, in the site's own data, is four values:** `site.yaml`'s
`features.console_downloads: true`, `downloads.json`'s `available: true`, `release.version`
bumped to the version the console's own release carries, and each entry's `sha256` set to the
value the release's own sums file records for its published name. The download list itself does
not change — it already carries the release's published names — so no URL is typed anywhere and
every button, label and URL follows from those.

**A later console release moves those values again, and it has happened twice:** on
2026-10-19 the console's own release moved to `console-v0.9.0` (the owner's ruling that
the site's console downloads follow the console's own release), so `release.version` and both
`sha256` values moved with it and `data/console-sha256sums.txt` was replaced by the published
`console-v0.9.0` asset, byte for byte — nothing else about the section changed: the same two
artifacts, the same signing and notarisation, and the same derived URLs. **And on 2026-10-24 it
moved again, to `console-v0.110.0`, which is the release the console's own stream published:**
`release.version`, the four `sha256` values and the sums file moved as before, and the Linux pair
came with them, so the section grew the block below. **On 2026-10-30 it moved to
`console-v0.115.0`**, the first release whose sums file also names the update feed,
`latest-mac.yml`: the feed is summed so the one cosign signature covers it, but it is
electron-updater's to fetch and not a download. The sums file is still copied byte for byte,
and the two checks that every name in it is offered (`TestWebsiteConsoleReleaseFactsAreComplete`
and `website/test/console.mjs`) pass over that one name.

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

**The section's look is the owner's feedback of 2026-10-21, and one thing came out of it.** The
cards the board draws — macOS, which the release ships for, Linux, whose unsigned pair it carries,
and Windows, which it does not — stand in **one row** on a desktop, equal in height, and the
`sha256` under a link wraps inside its line (`word-break`, in the mono face at the caption's
size) rather than running out of the card; a narrow screen is the only thing that wraps the row,
and `test/console.mjs` reads the built rules for all three of those, rule by rule, with plants.
**And the verification command is gone from the macOS card**: the section used to print
`shasum -a 256 Fugaro.Console-*` under the checksums and the owner struck it, so the `released`
block is the sentence a page renders and the source of it, and a key beyond those two stops the
build. The checksums themselves stay: they are the release's own values and
`data/console-sha256sums.txt` is still what they are held to.

**The Linux card is the one place the section prints steps rather than a checksum**, and that is
the block D10's ruling of 2026-10-20 asks for: an unsigned artifact carries no signature of its
own, so `downloads.json` may carry an optional `linux` block — the sentence a Linux reader is
owed (the build is not signed at all, and why), the ids of the two assets it links, the steps that
prove those bytes instead (the release's own `console-sha256sums.txt`, verified against its
Sigstore bundle, then the file's Linux lines checked against it), each written once with
`{version}`, and the `source` the sentence came from. The ids are held to the file's own
`downloads` entries, so a Linux link can appear only once the release's name, its checksum and the
release's own line for it are all in the data; `Downloads.astro` renders the steps as the site's
own command blocks, one copy control each (`src/lib/copy-control.mjs`), and Getting Started's
console option renders the same sentence and the same two rows without the steps, as it renders a
macOS link without its checksum. The block renders only while the data file carries it, so a
checkout without one draws the "Not offered" card the earlier state drew, and `test/console.mjs`
holds that state, the block's shape and its refusals, and the built page to it.

The console is built for macOS and for the Linux pair, and for nothing else
(`console/README.md`, `.github/workflows/console-release.yml`: one universal `.dmg`, one `.zip`,
one AppImage and one `.deb`), which is why the platform, and not an invented one, is the word each
button carries — and why Windows is the only card still drawn as "Not offered". Its release is a
**draft until the owner publishes it** (the workflow creates `console-vX.Y.Z` with `--draft`),
so the buttons land with the published release and not with the draft.

## The console's screenshots

The console page opens with a screenshot slider (the owner's feedback of 2026-10-19: *"Console:
we need a screenshot slider at the top"*), and the owner read the preview on 2026-10-21 and said
the screenshots were missing. The four are there now:
`public/screenshots/console-{live,run,spend,installations}.webp`, 1920 x 1200 each, served at
`/screenshots/`, with `public/screenshots/MANIFEST.json` beside them — the record the run that
took them wrote: the window they were captured in, each capture's pixel size and SHA-256, and the
`alt` text for each screen. **They are fixture captures**: a fake fugaro with invented projects,
taken from the real console build, and the owner asked for them. Nothing links to the manifest;
it is served because `public/` is served, and it is what `test/console-screens.mjs` holds the data
file's `alt` texts to.

`src/components/ScreenshotSlider.astro` renders the rows of `data/console-screens.yaml` and
nothing else, so adding, replacing or removing a screenshot is a data-file edit and a file. Two
things are worth knowing before editing the file. The four campaign pages' heroes are a second
consumer of those rows: each variant names one in `data/landing.yaml` and the loader refuses a
name no row carries, so removing a row a variant names stops the campaign build rather than
quietly shrinking a page (D411). And the homepage's "See it" panel and the four campaign pages'
own slideshow are the same component over `data/home-screens.yaml`, not over this file — its
console rows mirror these three files and `test/home.mjs` holds them equal row for row, so a
change here reaches the homepage and the campaign pages only through that file, whose rows the
campaign slider is held to slide for slide by `test/landing-pages.mjs` (D412):

1. **The file**, under `public/screenshots/`. The name is letters, digits, dots, dashes and
   underscores; the extension is one the slider serves — `png`, `jpg`, `jpeg`, `webp` or `avif`.
   An SVG is refused: it is a document that can carry script and references, and the site renders
   pictures. **The four files here are WebP, and the PNG the capture run wrote is not what is
   served**: see the conversion step below, which is a step of the round and not something the
   capture does.
2. **The conversion, which no run in this repository performs.** `npm run screenshots` — or
   `scripts/console-screenshots.sh` from the root — writes one PNG per screen into
   `console/screenshots/` at **2560 x 1600** (the capture window is 1280 x 800 CSS pixels at a
   device scale factor of 2; `console/README.md`, "What comes back"), and that directory is
   git-ignored and never committed. The four files this site serves are those captures at
   **1920 x 1200** in WebP: the same picture at the same 16:10, at half the linear size and a
   fraction of the bytes. Convert the capture before committing anything —
   `cwebp -resize 1920 0 -q 80 console/screenshots/console-live.png -o
   website/public/screenshots/console-live.webp` is one command that does it, and the quality is
   the round's call, since the bytes it lands on are the round's budget line — and commit **only
   the converted file**. The served file keeps the capture's own name with its own format's
   extension (`console-live.png` → `console-live.webp`), which is the link the gate holds: the
   row is matched to the manifest's capture **by that name**, so a picture served under a name no
   capture has is refused. Which tool produced the four files shipped here is recorded nowhere;
   that is half the reason this step is written down.
3. **The manifest, copied in with the file.** `public/screenshots/MANIFEST.json` is the capture
   run's own record of the **PNGs** — `tools/screenshots/manifest.ts` writes it beside the
   captures as one entry per picture with its pixel size, its SHA-256 and its `alt` text, plus
   what the run omitted — and it was copied into `public/` because the site's `alt` texts are its
   texts and not a second drafting of them. **None of the files its hashes describe is served by
   this site**: the WebP beside it is the converted form, and the manifest's names, sizes and
   hashes are the captures'. Nothing in a website round regenerates the manifest, so a round that
   adds, replaces or re-runs a capture copies the run's manifest in **in the same commit as the
   file and the row**, so that the record beside the pictures still describes the run that took
   them — a published manifest whose entries match nothing the site serves is the drift this step
   exists to prevent.
4. **The row**, in `data/console-screens.yaml`, in the order the slider shows them:

   ```yaml
   screens:
     - file: console-live.webp
       alt: The console's Live screen for a fictional acme-platform installation: ...
       caption: Live
       width: 1920
       height: 1200
   ```

   `file` is the file's **name** and not a path or a URL: the site path is derived from it at
   build time, so no file under `website/` carries a URL (Global Constraint 9). `alt` and
   `caption` are both required and neither may be empty: `alt` is what a reader gets when the
   picture does not arrive and what a screen reader reads instead of it, and the caption is the
   line under the picture — one word per screen is what the four rows carry. `width` and
   `height` are the file's own pixels, which is what lets the browser reserve the box before the
   file lands; `test/console-screens.mjs` reads each WebP's header and refuses a row that
   declares a box the file is not, and holds each `alt` to the manifest's text for that capture,
   verbatim. `src/content.ts`'s `loadConsoleScreens()` refuses a name that is a path or a URL, a
   name with no extension at all, a format the slider does not serve, an empty `alt` or caption,
   a size that is not a positive whole number, and any key no reader reads — at the document's
   root or in a row, which is what keeps a URL out of the data file's own `src` line as well as
   out of `file`; every refusal names the row it refused, and the build stops. **A list with no
   row in it is still legal**: it renders no slider section at all, which is the state the page
   was in before the four captures landed.

**What a screenshot costs, against the three numbers the budget above states:**

- **The whole site:** the file is served from `public/`, so its bytes are in `dist/` and count
  once against the site ceiling the gates above state; `test/budget.mjs` holds the new build to
  that ceiling and asks for the figure to be re-derived in the commit that adds the file.
- **The console page's weight:** the pictures a page renders are part of its weight, so **a page
  that carries the heaviest set of pictures can become the page the page-weight ceiling is
  derived from** — the console page is that page today, at 410,743 bytes with the four captures
  in it, where the docs hub (292,469) was every earlier derivation's subject. A round that adds
  pictures re-derives both figures, and this one did.
- **The page's words.** The console page stands at **299 words** against the ceiling above, and
  a row's words all count: the caption is text under the picture, and the `alt` is counted too,
  because it is the text a reader gets when the picture does not arrive (`test/budget.mjs`'s
  `visibleText` reads an `img`'s `alt`). The four rows' `alt` texts are 148 of those words, and a
  page that needs more than the ceiling needs it raised in the same commit that adds it — D188's
  own rule: the number moves where the need is, and a build that needs more fails until it moves.

The slider's own bytes move with the page's markup and not with a script: the component's
stylesheet is inlined into the console page whether or not the slider renders.

**The slider reads the page with no JavaScript at all, and the owner's second reading of it
(2026-10-21) is about the half a stylesheet cannot write.** The markup is the fallback and it did
not change for this: the track is a focusable, labelled scroll container with snap points, every
dot is an `<a href="#…">` into its own slide, the first picture is in the page and the other three
are `loading="lazy"`, so a reader who runs no script scrolls, taps a dot and reads every one of
the four. What the owner asked for and got is *"the slider should show how many screenshots there
are (dots or "1 / 4" counter), and I should be able to click to select them (right now only the
keyboard works: add clickable dots/thumbnails and previous/next buttons); also we should
auto-slide every few seconds (~6s; pause on hover/focus and when the tab is hidden; off for
prefers-reduced-motion)"* — and all of that is `public/scripts/site.js`:

- **The bar**, which the markup emits `hidden` and only while there is more than one slide: a
  Previous arrow, the count and a Next arrow, each arrow a `<button type="button">` with its name
  in an `aria-label` (the chevron is drawn with a border and a rotation, so the bar adds no
  drawing and no second file to the page). A reader with no script sees the pictures and the dots
  and no button that does nothing — the rule `test/copy.mjs` holds the copy control to, and the
  reason the bar is `hidden` in the markup rather than by a class.
- **The count and the current dot**, both written from the track's own scroll position: `n / N`
  into a polite live region, and `aria-current="true"` on the dot the reader is on (every other
  dot carrying `"false"`, so no attribute has to be taken off anything). The track is the source
  of truth, so a reader's own swipe moves the reading exactly as an arrow press does.
- **The presses**: an arrow moves one slide, wrapping at either end, and a dot press is taken over
  from its anchor and made the same move — one slide at a time, with the count and the mark
  following. The move is not animated, and the script asks for that in the only way a browser
  takes: `behavior: 'instant'` on the scroll, because `'auto'` is resolved by CSSOM-View to the
  box's own computed `scroll-behavior` — which the track declares `smooth` — and so *is* the
  animation. The count is the scroll position, and a move the browser animated would be a move
  during which the two disagree: the reader would be told a number and shown a mark for slides
  they are passing through, and a second press would advance from a position a `scroll` event had
  already put back.
- **The first reading**, taken from the track like every later one: a reader who arrives already
  scrolled — a shared `#console-screen-3` deep link, or a scroll position the browser restored —
  opens with the count and the mark of the slide they are on, rather than `1 / N` and the first
  dot until the first `scroll` event corrected them.
- **The autoplay**: one slide every six seconds, stopped while the pointer is over the slider,
  while the focus is inside it, while the tab is hidden, and never started at all for a visitor
  whose system answers `prefers-reduced-motion: reduce` — a setting read at load and watched
  after, because a visitor can turn it on without reloading.

`website/test/console-screens.mjs` holds the shipped state — the four rows against the files'
own headers and the manifest's `alt` texts, the page against the rows, and the loading of each
slide — and the states beside it: a two-row fixture build, a one-slide build (which renders no
dots and no bar at all), an empty data file (which renders no slider), and the refusals above. The
fixture builds write no image bytes at all, because a test may not commit a picture either: they
assert the `src` the loader derives from the row's file name, and the `alt`, the caption, the size
and the dot that each row produces. **The script's half is held by running the script**, in a stub
browser: `test/lib/dom.mjs` — the harness `test/copy.mjs` used to keep to itself, moved out of that
file so that the two gates that run `site.js` run it in the one stub — is driven through every move
above (the arrows, a dot press, a reader's own scroll, the pointer, the focus, the tab and the
motion preference), and then through twelve planted defects, one per part of the half: a bar nobody
reveals, a count never written, an autoplay of the wrong interval or none at all, each of the four
stops removed, an arrow that goes past the end instead of round it, a dot press the script does not
take over, a count that ignores the reader's own scrolling, and a move that leaves its landing to
the stylesheet's animation — each one refused by name. Two readings have a case of their own: the
page a reader arrives on already scrolled (`scroll:` in the stub, the deep link and the restored
position), which the same stub answers by opening the page at that slide, and the moves themselves,
whose `scrollTo` the stub records — `track.scrollLeft` cannot tell an instant landing from an
animated one, because it lands where it is sent either way, and what the script *asked* the track
for is the half that can.

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
  own request in the hero's prompt line is item 3's example of one, quoted from the ruling
  that asked for it (`src/components/Hero.astro`, through `src/lib/agent-request.mjs`, which
  is where the line is held to that document at build time).
- **The skill the reader's screen is described by**, `plugin/skills/working/SKILL.md` — the
  line about what the watch screen shows, under the command in the slider's "See it" panel.
  The two answers the chat's illustration spoke, and the launch status line it printed, left
  the site with `AgentChat.astro` when the owner's approved draft of 2026-10-19 ("Home, draft
  2 (slider)") folded the chat into the slider: the status line was the illustration's own
  label rather than a document's line, and the build refused it if it turned out to be a
  sentence of the skill its turn cited. Item 3's acceptance is that the
  agent's lines "show the real shape of the skills' behaviour", and every line the
  illustration *speaks* is a line of the document it cites, so the build reads those files and
  stops, naming the file, when a line is not in one of them. (The three `Pull request n of 3`
  labels were the illustration's own rather than a document's lines — a run that has not
  happened has no numbers to quote — and the caption labelled the figure an illustration
  rather than claiming its lines are quotations.) **This is why a test build's copy of the
  repository carries `plugin/skills/`**: `test/lib/build.mjs` and the five test files that
  stage their own copy skip only the parts of `plugin/` a build does not read
  (`READ_UNDER_PLUGIN`).
- **The four steps' captions and the run's detail** came from `README.md` and
  `docs/design/v1.md`, cited by the component that quoted each line: the four-step flow's
  captions were `src/components/ArchitectureDiagram.astro`'s, retired with the flow by the
  owner's ruling of 2026-10-20, and the run's detail is `src/components/RunDiagram.astro`'s,
  where every label is held to the document it cites.

## Layout

```
website/
  package.json, package-lock.json, astro.config.mjs, tsconfig.json
  data/install.yaml, site.yaml     # hand-written facts, pinned by Go tests
  data/release-summaries.yaml      # each release's product summary, pinned by test/changelog-page.mjs
  data/typical-costs.yaml          # the owner's own measurement of what a job costs, pinned by test/caps-and-costs.mjs
  data/console-screens.yaml        # the console page's slides: hand-written, one row per screenshot
  data/mobile-claims.yaml          # the phone app's page: one row per claim, with the state the page marks it with (D240, D241)
  data/mobile-screens.yaml         # the phone page's slides: the same rows as the console's, empty until the owner's captures land (D251)
  design-ref/zoom-levels/*.dc.html # the four owner-approved zoom boards, redrawn as the site's SVG: the source of the levels' words (2026-10-22)
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
change `.github/workflows/`. `docs/pending-workflows/` now holds two pending workflows,
`docs/pending-workflows/console-release-symbols.yml` — the console's release pipeline
with the crash reporter's symbol upload added after the build, which runs at release time
only rather than on a pull request, and whose step is a no-op until the owner adds its
`SENTRY_AUTH_TOKEN` secret to the `console-release` environment — and
`docs/pending-workflows/console-windows.yml` — the console's Windows
installer pack, which runs at release time only rather than on a pull request, and is copied in
only after the packer and the unit suite can pass on a Windows runner. It is not this
site's (the credentials service's image leg in `images.yml`, credentials-service plan task C1.14, was copied in on 2026-10-30, the shared package's test job, `shared-tests.yml`, was copied in on 2026-10-22, the
mobile app's release workflow, `mobile-release.yml`, and the console's own release
stream, `console-release-tag.yml`, were copied in on 2026-10-23, and all four are no longer
staged, and Dependabot's watch over the console's npm dependencies was copied over
`.github/dependabot.yml` the same day): the website's two manual
workflows, `website-preview.yml` and `website-promote.yml` (#566), were copied into `.github/workflows/` by the
owner, and no deploy workflow is staged. The only other workflow files left there are the desktop
console's `console-tests.yml` and `console-release.yml`, which the owner has copied in as well:
their staged copies stay by design, because the console's docs tests assert each one is
byte-identical to its copy under `.github/workflows/`, so neither is pending.
