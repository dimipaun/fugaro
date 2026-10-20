# Website redesign — implementation plan

Status: **plan only; nothing of the redesign is implemented.** Owner: **@Dimi**. Date:
**2026-10-18**. Source: [spec.md](spec.md) — the brief, committed alone in this branch's
first commit. This plan executes
[reconciliation.md](reconciliation.md), whose findings it cites by number (`RF<n>`); the
first line of work on the site is [`../implementation-plan.md`](../implementation-plan.md)
(tasks `W0.1`–`W3.3`, decisions `D90`–`D109`), and **this plan inherits that plan's §3
constraints rather than restating them** — it adds only the ones that are new. The
"before" pictures are regenerated into [current/](current/), whose README has the command.

**The owner's ruling of 2026-10-18, as recorded for this line of work:**

> use as much JavaScript on the website as needed. The strict CSP stays (`script-src
> 'self'`, no remote origins), the origins gate stays (hash allow-list or file scripts);
> the 0-byte-JS rule and the byte budget numbers change deliberately and their tests
> change in the same plan.

## 1. What this plan is for

The brief's "Done when" line, turned into nine parts and three live checks:

| the brief's "done when" | where it is done |
| --- | --- |
| the gist is above the fold on every page | P4 (the copy, the word ceiling, the long pages' summaries), P5 (the hero and the ordering), Check 70b |
| no internal/meta text | P4, gated by `website/test/meta.mjs` |
| the accent matches the logo | P2, gated by `website/test/accent.mjs`; the owner's eye on the deployed site |
| the mobile header fits one row | P3, Check 70a |
| How It Works is visual | P8 |
| the homepage has the terminal animation | P7 |
| "good images or diagrams that show how the system works" (item 5) | P8 (the flow, also placed on the homepage) |
| every command block has a copy button | P6, gated by `website/test/copy.mjs`; Check 70c |
| final screenshots match the approved design | P9, and the owner's eyes (RF26, D190) |

## 2. What is new in this plan's constraints, on top of the first plan's §3

1. **This deliverable is documents.** No part below is in this branch: this branch
   carries `spec.md`, `reconciliation.md`, this plan, the README, `capture-screenshots.mjs`,
   `sample-logo-hue.py`, `current/README.md` and one `.gitignore` entry, and nothing else.
   Every part is a later pull request. **No screenshot is ever committed** (D190).
2. **Nothing under `.github/workflows/`.** No part needs one; if one ever did, the file
   is proposed under `docs/pending-workflows/` for the owner to copy.
3. **`firebase.json`'s policy stays as it is** (`script-src 'self'`, no nonce, no
   `unsafe-inline`, no `unsafe-eval`), and `website/test/origins.mjs`'s `INLINE_SCRIPTS`
   allow-list is not added to, unless D183 chooses the inline shape — in which case both
   grow by the same `'sha256-…'`, in one commit. The recommended shape — a first-party
   file — changes neither (RF15).
4. **One source of truth per fact.** A number a gate enforces lives in
   `website/README.md` and is read out of it (the pattern `budget.mjs` already uses).
   The word ceiling of P4 goes there too, beside the byte budgets.
5. **The site's gates stay browser-free** unless D187 decides otherwise: `npm run check`
   is hermetic and has no browser. A viewport claim is therefore either modelled
   (P4's word ceiling) or the owner's (Check 70).
6. **Every part is independently mergeable and independently revertible**: it changes
   one concern, it lands with its own tests, and reverting it leaves the site working.
   No part depends on a later part to be correct; where two parts touch the same file
   (`index.astro`: P5, P7 and P8; `base.css`: P3 and P5), they touch different blocks and
   the later one is written to apply on top.
7. **The first plan's §3.3, §3.6, §3.8, §3.9 and §7.1 all still hold**, including "a wave
   starts on the owner's go" and §15's approval gate. The order below is a recommendation
   about sequence, not permission to start.

## 3. The parts

Each part: what it changes, the gates it must leave green, how to verify it, and — where
a budget, a gate or a pinned number moves — **the test change, named**, in the same
commit. Every part's verification includes these two, from the repository root:

```sh
(cd website && npm run check)                                 # build + 179 tests today
GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'Docs|Lint|Website|CliRef' -count=1
```

### P1 — the status correction (RF1)

**What.** The status lines, in three documents, that say the site does not exist:
`../README.md:3` and `:5`, `../reconciliation.md:3`, `../implementation-plan.md:3`. Each
becomes a true sentence with the date it became true — the site exists, it is built,
`npm run check` passes, and the redesign is the line of work now open against it. This
directory's README already states the real status and is not edited.

**Nothing else.** No rewrite, no reflow, no "and while we are here".

**Verify.** `git show --stat HEAD` lists exactly those three files; `grep -n "No site code
exists" docs/website/*.md` prints nothing; and the sentence about the tests is checked by
running `(cd website && npm run check)` (179 pass on this base).

**Why first.** A document that says the site does not exist is the one error that makes
every later review of the redesign confusing, and it is the cheapest possible commit.

### P2 — the accent, sampled from the logo (RF16, RF18; D180, D181)

**What.** A fourth palette file, `website/src/styles/palette-logo.css`, with §5.2's ten
tokens — the same ten names every palette declares, so no new token: `--accent: #17A8E6`
(198.0°), `--accent-on-dark: #6FD8F5`, the ground moved to `#0A0E16`, the surfaces, border
and gradient stops with it, and `--warn` at `#F5A524` or `#F97316` as D181 says. It becomes
the palette in force by **one import** in `website/src/styles/tokens.css` and the
matching `PALETTE_IN_FORCE` line in `website/test/a11y.mjs`; `PALETTE_FILES` gains it.
`palette-split.css`, `palette-violet.css` and `palette-orange.css` stay as they are — the
record of D104's choice — and `a11y.mjs` keeps linting and contrast-checking all four.
`website/public/site.webmanifest`'s `theme_color`/`background_color` follow `--ground`
(`brand.mjs` asserts the equality, so the two move in one commit).

**New gate: `website/test/accent.mjs`.** The brief's "Done when: the accent colour
matches the logo" needs a gate that cannot be satisfied by a palette edit from memory. The
logo is a plain 8-bit, non-interlaced RGBA PNG, so the test **decodes it with the standard
library** (`node:zlib`'s `inflateSync` and the five PNG row filters — no dependency), runs
reconciliation §5.1's method over it (alpha ≥ 250, value ≥ 0.05, saturation ≥ 0.5,
circular mean), and asserts that the result is 198.4° ± 0.5° (the figure
`sample-logo-hue.py` prints, so a changed logo is noticed), that the palette in force's
`--accent` hue is within **5°** of it, and that `--warn` is at least 90° from it.
Mutation-proven: a fixture palette with `--accent: #8B5CF6` (258.3°) fails, and the test
carries that case.

**Where a gate moves.** `a11y.mjs`: `PALETTE_FILES` gains `palette-logo.css`,
`PALETTE_IN_FORCE` names it, and the failure message that cites "palette C" cites D180
instead. `PAIRS` and `BASE_PAIRS` do not change: §5.3 computes every pair they hold, and
all pass. `brand.mjs` does not change (it reads the imported palette's `--ground`).

**Verify.** The two commands of §3, with `accent.mjs`'s cases among the passes; then the
owner looks at the logo beside the accent (RU3).

**Independently mergeable because** it changes colour tokens and nothing structural, and
a revert is the import line and the `PALETTE_IN_FORCE` line.

### P3 — the two-level menu and the mobile drawer (RF3, RF4; D182) — *the drawer was retired on 2026-10-19; see the amendment at the end of this part*

**What.** `website/src/layouts/Base.astro`'s `NAV` becomes groups (D182's
recommendation: *Product*, *Reference*, *Project*, with Home as the wordmark link that
already exists and Privacy left in `FOOTER_NAV`), rendered as a two-level menu on a wide
viewport and as a drawer behind a single toggle on a narrow one. `FOOTER_NAV` still picks
its entries out of `NAV` (it throws if one is missing), so it is rewritten to search the
groups. `website/src/styles/base.css`'s `.site-nav` rules are rewritten for it, and the
comment at `base.css:117-118` — "at 360px the ten sections are three short lines" — is
replaced by the truth.

**No script.** The drawer and the dropdowns are `<details>`/`<summary>` plus CSS: they
work with JavaScript off, need no CSP decision and cost no bytes of the JavaScript budget.
What `<details>` does not do is close on Escape or on a click outside it; that is a few
lines of progressive enhancement in P6's script, so P3 merges first and is complete
without it.

**Where a gate moves — this is the part that edits a test, deliberately.**
`website/test/base-styles.mjs` asserts today that `.site-nav ul` is `display:flex`, and
that the section nav is a labelled landmark whose links all have an `href` and no
negative `tabindex`, with exactly one `aria-current="page"` when the page's route is in
the nav. Under the new structure: the nav is still a labelled landmark; its top-level
items are links or `<summary>` elements; every link in the menu has an `href` and no
negative `tabindex`; and **exactly one** `aria-current="page"` still exists on every page
whose route the menu lists. The `display:flex` regex follows the new selector. Each
rewritten assertion keeps a fixture that proves it can fail.

**New live check: 70a** (§4) — the mobile header on one row at 390 × 844, the drawer
openable and closable by keyboard, and the menu usable with JavaScript disabled.

**Verify.** The two commands of §3; then Check 70a by the owner.

**Independently mergeable because** it is the only part that touches the menu; P5 touches
`base.css`'s spacing, not `.site-nav`.

**Amended 2026-10-18 (owner's brief, item 1) — a group's submenu OPENS WHEN THE POINTER ARRIVES AT IT, on a device whose pointer hovers.** The brief this change was run from asks, as its first item, that the nav's submenus open on hover on desktop; the owner's acceptance line is *"hovering a top-level item on desktop opens its submenu immediately"*, with the submenu staying open when the pointer moves into it, **no** hover behaviour on touch, and the tap opening and closing as it always did. That is a third thing the menu's script does, beside the two closes the paragraph above names — and the paragraph is read with this amendment: **"No script" above is now "no script for the structure",** and the script's menu work is the two closes **and** the hover-open.

- **What the script does.** `website/public/scripts/site.js` gives each `.site-nav-group details` a `pointerenter` (open, when the event says the pointer is a mouse or a pen), a `pointerleave` (close **200 ms** later, so a pointer crossing the seam between the summary and the list, or brushing past, does not collapse it under the reader) and a `focusin` (open when the **keyboard's** focus arrives at the trigger — the second 2026-10-19 amendment below is what tells that focus from the one a click moves — which is what lets Tab reach the links inside a closed group), and it cancels the pending close when the pointer comes back. It also writes `aria-expanded` on the group's `<summary>` from a capture-phase `toggle` listener on `document` — the one event a browser fires when `open` changes, whichever way it changed: the pointer, a click, Enter or Space, a sibling in the group's exclusive set, or Escape. **Nothing writes the attribute before a toggle happens**, so a browser running no script has no second copy of the state to be wrong.
- **Why it is the script and not the stylesheet.** A closed `<details>` hides its own content in a way author CSS cannot portably override — the reason this part's own note gives for the panel being the toggle's sibling — so "open on hover" cannot be a `:hover` rule that reveals the list; it is the `open` attribute, and the attribute is the script's.
- **One question decides both halves, and it is the pointer's.** `base.css` scopes the menu's **every** `:hover` rule (the summaries', the links', the drawer toggle's) under `@media (hover: hover) and (pointer: fine)`, and the script asks `matchMedia` for the same query **before it binds anything**: a device that answers "no", or a browser with no `matchMedia` to ask, keeps the tap it has always had. **Never the viewport's width** — a tablet 1280 px wide has no hover to give, and a phone with a mouse attached has one. The CSS half also removes the sticky hover a touch device used to keep after a tap, which is a control marked as hovered that the reader never hovered.
- **Amended 2026-10-19 (review of the same brief, item 1): the question is the device's, and the event answers it too — and the answer is watched rather than sampled once.** The query above answers for the device's **primary** pointer, and a device can answer it "yes" and still have a finger on it: a touch laptop has a trackpad *and* a screen, and a tap there sends a `pointerenter` of its own before the click, so a script that took the arrival for a hover would open the group and let the tap's click close it again — a submenu no finger could open. So the events decide which pointer is arriving: a `pointerenter` or `pointerleave` whose `pointerType` is `touch` is ignored, a mouse and a pen are not, and a tap goes through the click the `<summary>` already has on every device. And the query is not asked once and kept: the script subscribes to its **`change`** and binds or unbinds the whole hover behaviour on each answer, so a keyboard docked to a tablet (a trackpad arrives), a mouse plugged into a phone, or a pointer taken away are all answered on the page the reader is already on, with no reload and with the click untouched in both directions.
- **Amended 2026-10-19 (second review of the same brief, item 1): the focus a click moves is the pointer's, and it opens nothing — and a close the browser makes hands the reader's focus back.** A browser moves the focus to the `<summary>` on the way to the click, so the `focusin` above — the keyboard's way into a closed group — was opening the group the press had landed on and the `<summary>`'s own activation was then toggling it straight back shut: on a touch laptop the first tap on each group did nothing (a second tap opened it), and a click on a closed group whose trigger did not already hold the focus did nothing either. The two focuses are told apart by what brought them: a `pointerdown` marks the next focus as the pointer's and a `keydown` marks it as the keyboard's, and only the keyboard's opens a group. The click stays the whole of a tap and still toggles, so a group opens on the first tap or the first press. And the close the browser makes by itself was stranding a keyboard reader: opening a group closes its siblings in the `name` set, which on a pointer's arrival happens with no key pressed and no focus moved by anyone, and the browser stops drawing the list the focus was standing in and drops that focus to `<body>`. The `toggle` listener is the only thing that hears that close, so the focus is remembered as it arrives (a `focusin` on `document`) and handed back to the trigger, as Escape and the pointer's own close already did.
- **What does not move.** The click is untouched (a `<summary>` still toggles its own group, so a click on a group the pointer has already opened closes it — the behaviour the control had before), the drawer and the one-row header are unchanged, Escape, the outside click and the focus handed back are unchanged, the menu still opens and closes and reaches every link with **JavaScript off**, there is no new origin, no CSP change, no inline script and no second file (D183), and the budgets move only by the script's own size.
- **The gate, and its proofs.** `website/test/nav-hover.mjs` is new: it runs the shipped script in a small stub DOM (no browser, F3) and holds the two query strings to each other, the open on arrival, the list staying open, the delay and its cancellation, the click on both a hovering and a touch pointer, the drawer not opening on hover, focus/Escape/Tab with the focus handed back, `aria-expanded` at every step, and the touch path doing nothing and scheduling no close. The 2026-10-19 half added three more cases to the same file: **a tap opening and closing a group on a device that answers the hover query "yes"** (and the mouse on that same machine still hovering), **a pen hovering like a mouse**, and **an answer that changes under a loaded page** binding the behaviour, unbinding it again, and leaving the click in place. The stub DOM grew the two things those need and no more: pointer events carrying their `pointerType`, and a `MediaQueryList` whose `change` a case can fire. The second 2026-10-19 round added two more cases: **a click on a closed group whose trigger did not already hold the focus** — it opens, because the press moves the focus and the click must still toggle it open — and **a group the browser closes because a sibling in the same `name` set opened**, which hands the reader's focus back to its trigger. The stub grew the three browser behaviours those need: the focus a pointer's press moves to the element it presses, the exclusive set a shared `name` makes, and the focus a closed `<details>` drops (a real browser's blur, before the `toggle` the page hears). **Each case was mutation-proved by hand** — twelve defects planted for the 2026-10-18 half, nine more for the 2026-10-19 half, and eight for this round, every one caught, three of the eight in the stub DOM itself — and `website/README.md`'s gate list names the new file. The list's own opening count moved with it, and is now held: the section says "**Seven** checks run over the built artifact" and `website/test/readme.mjs` counts the bullets under that sentence and compares them, so a gate added or removed without the sentence moving fails there (as does a gate named that is not a file, or one listed twice). The review's last two nits are the third 2026-10-19 round: that count, which this PR's seventh bullet had left at six, and the file's missing `the refusals, kept` section — the one the README's promise that **every** gate keeps fixtures exercising its refusals was false of. The gate's rules are now one function over the script source (`menuFailures`: the device that answers "no" getting no hover behaviour and scheduling no close, a tap on a device that answers "yes" opening the group, the close waiting a moment, `aria-expanded` saying what the group is, the focus a press moves opening nothing, the answer being watched rather than sampled, and a browser's own close handing the focus back) and one over the stylesheet (`hoverScopeFailures`: one pointer block, hover states under it, none outside it, none in a width query). A case plants each defect in the shipped script and in the built stylesheet and reads the named refusal back, and **each of those rules was mutation-proved the other way as well**: removed from the function, with the refusal it names watched failing. The fourth 2026-10-19 round is the review's own finding against that handback, and a real bug: the remembered focus was only ever replaced, never invalidated, so a press on the page's own text — which moves the focus to the body and fires no `focusin` anywhere — left it on a link inside the group, and the click's own close then handed the focus back to the trigger the reader was walking away from. The remedy is the invalidation, not a marker on the closes the file asks for: the `toggle` a `<details>` fires is a queued task, so a flag set around a synchronous `closeMenus` or hover close would be cleared before the handler ran. The press outside the group now forgets the focus, and a close that follows has nothing to give back; a press *inside* it keeps the memory, so a sibling's close still answers. The stub's press had to learn the same thing to be able to see it: `focus()` used to focus whatever it was given, so a press on a paragraph moved the focus there and fired a `focusin` a browser would not — the press now walks to the nearest focusable ancestor (a `tabindex`, a link with an `href`, a control) and, finding none, drops the focus to the body with only a `focusout`, exactly as Blink does. A case covers the whole of it — keyboard focus inside an open panel, then a click on the page's own text, asserting the group closed and the focus did not come back to the trigger — and an eighth `menuFailures` rule with an eighth planted defect (the invalidation removed) keeps it. Proved three ways: with the fix removed the new case fails; with the **old** stub *and* the fix removed every case passes, which is what the review's minor finding said and why the stub had to move; with the eighth rule removed the defect case goes green. The fifth 2026-10-19 round is the review's last finding: the pending hover-close (`CLOSE_AFTER_MS`, ~200 ms, scheduled on `pointerleave`) was never cancelled when the keyboard's focus entered the group, so a reader who tabbed into a submenu within that window had it closed under them. A group now has two readers and the pointer is only one of them: the close a pointer's departure owes is owed when nobody is left reading, so a keyboard reader standing in the group holds it open, the close lands when the focus goes, and a focus arriving inside the delay cancels it. The focus is read where it is asked for rather than remembered, because a focus can leave a group by way of something that cannot take it — a paragraph of the page's own text — and that fires no `focusin` anywhere; the one focus that is not a reader is the pointer's own on the trigger, which a tap or a click leaves there. The listener is on the document rather than the group, because the drawer's list is the trigger's sibling and a focus in it never reaches the group at all. The hover close can no longer land under a focus inside the group, so the handback it used to pay is gone: the closes that can still strand a focus are Escape's and the browser's, and those pay it. Two cases were added (a pointer leaving a group the keyboard is standing in does not close it, and closes a moment after the focus goes; a submenu the pointer has just left stays open when the keyboard arrives inside the delay) with a ninth and tenth `menuFailures` rule and two more planted defects, and each half of the fix was proved on its own: with the reader check removed the first case fails, with the cancellation removed the second does, and each new rule removed turns its own defect case green. The stub's document grew the `removeEventListener` the unbound listener needs. The sixth 2026-10-19 round is the review's own two findings against that fix. The keyboard's way into a group was bound inside the hover behaviour, so a device that answers the hover query no — a phone or a tablet with a keyboard attached, or a pointer taken away mid-session, which unbinds the hover behaviour — had a menu whose second level Tab could not enter at all, against the unconditional claim the script's header, `Base.astro`, `base.css`, `website/README.md` and this plan all make; the keyboard's half is now its own binding beside `expanded` (`keyboardGroups`), bound whatever the device answers, and only the pointer's listeners live behind the query. And the guard that keeps a close from reopening the group it handed the focus back to was cleared only by a `focusin` inside a group, so the stub's synchronous `toggle` — a browser queues it — left it set by the close with no `focusin` left to clear it, and a reader who took the focus out of the menu and back to the same trigger found the group still shut; the case that covered the guard interposed a focus on another group's trigger, which cleared the state, so the gate passed either way. The guard now expires with the focus it was written for: the document's own `focusin` clears it as soon as the focus arrives anywhere but the trigger it was written for, read on the way up so the group's own listener still has the focus the guard was written for. Two cases were added (the keyboard opening a group on a device with no hovering pointer, and the guard lasting only for the focus the close handed back) with an eleventh and twelfth `menuFailures` rule and two more planted defects, each proved by hand: with the binding moved back inside the hover behaviour the non-hover case fails, with the clearing removed the guard case fails, and with each new rule removed its own defect case goes green.
- **Budgets (D192).** The script grew 20,406 → 26,573 bytes on 2026-10-18 and 26,573 → 30,222 → 33,934 → 35,002 → 37,700 → 39,896 bytes on 2026-10-19, and the README's three figures are re-derived in each commit that ships it. **Where the figures live is `website/README.md`'s budget list, and `website/test/budget.mjs` is the authority for them**: its header carries the derivation (that build plus 25 %) and its `derivationFailures` case reads the README's numbers back and refuses a figure that is not three times the tight derivation of a build the artifact can be measured against — so no number repeated in a plan is ever the one enforced. At this commit they are JavaScript per page **149,610**, page weight **1,001,316**, whole site **3,106,374**.
- **The runtime half is the owner's, and Check 70a grew a step for it.** Check 70a's step 6 is the brief's acceptance line: the pointer's arrival, the move into the list, the departure and the return, with an emulated touch device (or a phone) as the other half, and — added on 2026-10-19 — the device the query answers **yes** for and a finger still uses (a touch laptop, or a phone with a mouse attached): a tap there opens the group and a second tap closes it, and unplugging the pointer and plugging it back in re-decides without a reload; the keyboard step that used to be 6 is 7. The second 2026-10-19 round added the acceptance the first one's fix needed: **a click on a closed group whose trigger did not hold the focus opens it on that press**, on the mouse and as a tap. Four of that check's statements said what the menu did **before** this amendment, and each is corrected: the script's "only job in the menu" was the two closes (step 4), the wide groups were "closed" (step 5), a group was opened "by keyboard" (the keyboard step, which is 7 now — tabbing to the summary opens it before the key is pressed), and step 6's touch half and its pass/fail lists name the click that must open a group. Until that step runs, the rendered hover behaviour is **UNVERIFIED** in a browser, and the stub DOM is not a pointer.

**Amended 2026-10-19 (owner's brief, item 1) — THE THREE GROUP LABELS ARE THE PHONE'S MENU, and the drawer and its one toggle are retired.** The brief this change was run from asks, as its first item, that the three group labels sit in the header's row on a phone with no `Menu` button, each tapping open its own panel of links, and that they fit beside the wordmark at 375 px with the wordmark's **text** hidden below about 380 px and the mark kept. So the paragraph above is read with this amendment: **"rendered as a two-level menu on a wide viewport and as a drawer behind a single toggle on a narrow one" is now "the same two-level menu at every width"** — one `<details name="site-nav-group">` per group, the shared `name` making the browser keep the three exclusive, a panel across the header's own width on a phone and the same list as a dropdown under its label from 48 rem up. `.site-nav-drawer`, `.site-nav-toggle` and `.site-nav-panel` and the sibling combinator that showed the panel are gone, and `site.js`'s `holds()` loses the drawer branch it kept for the panel. **D182's group decision is untouched** (three groups of three, Home as the wordmark, Privacy in the footer); what changed is what a phone does with them.

- **The wordmark's breakpoint is 390 px, not the ruling's 380.** The row is measured rather than estimated: at 381 px the wordmark's text plus the three labels is a few pixels wider than the header's content box in the font the built site falls back to, and a row that does not fit is a row that scrolls sideways. The text is hidden the way a visually-hidden label is — absolutely positioned, one pixel, clipped — and **not** with `display: none` or `visibility: hidden`: the wordmark is the link home, and a link whose accessible name is thrown away is announced as nothing.
- **A label and every link in a panel are at least 44 px tall, at every width**, which is the target size the brief asks the menu to be measured against. The header grew 65 px → 77 px and a wide dropdown 114 px → 150 px, which is a change the brief's acceptance asks for.
- **The gate moves with the menu, and `nav-hover.mjs`'s phone case with it.** `base-styles.mjs`'s `navLayoutFailures` is rewritten for the four invariants of the menu that is (three labels in one row at both widths, the header one row with the list out of its flow, a phone's row fitting, an open that moves nothing) and `navFailures` for the markup's shape, with the toggle, the panel and the drawer refused **by name**; each rule keeps a case that plants the defect and reads the refusal back. `nav-hover.mjs`'s phone case asserts what replaced the toggle — the labels are drawn with nothing in front of them, a pointer that does not hover opens nothing, a tap opens the group — and `copy.mjs`'s stub DOM loses the panel and the drawer branch it carried. **Every case was mutation-proved by hand.**
- **Check 70a moves with it too.** The drawer step becomes the three labels: at 390 and at 375 the header is one row with the wordmark's mark and no text, each label opens its own panel by tap and by keyboard, Escape closes it, and the menu still reaches every link with **JavaScript off**; at 320 px the same three labels fit. The one-row claim is measured at each width rather than at 390 alone, because the labels and the links grew to 44 px.
- **Budgets (D192).** The removal is 278 bytes off the base stylesheet every page inlines and 181 off the script, so 459 off the page figure and 3,239 off the site's, re-derived in the commit that ships them. `website/README.md` carries the figures; this bullet does not, on purpose.

### P4 — cut the text, and gate it (RF5–RF8, RF23, RF24; D186, D188, D189)

**What.**

- `getting-started.astro:61` — the meta paragraph goes, and the page opens with the
  install commands (the facts of `website/data/install.yaml` were always there; only the
  explanation of where they come from was on the page).
- `docs.astro:231-235` and `:260-263` — both meta paragraphs go. The hub's new lead is
  the per-guide one-line `answers` the page already carries, gathered at the top (D189).
- `faq.astro:397`, `community.astro:224`, `caps-and-costs.astro:20-22` and
  `CapsCosts.astro:192-194` — the provenance sentences go, replaced by what the page is
  for.
- The long pages' summaries, **each rendered from a file** under D189's recommendation:
  `/security/` renders the document's own "In short" section as the page's summary, its own
  "Reporting a vulnerability" section, and a link to the policy (**this needs the owner's
  text for `SECURITY.md` first** — and the page it produces is not the byte-for-byte page
  Global Constraint 12 asks for; **the departure is recorded below**); `/changelog/` renders
  the product summary of every release above its own entry, read from
  `website/data/release-summaries.yaml` — the owner's changelog feedback of 2026-10-19 replaced
  D189's "first Highlights line of the newest releases" reading for this page, and the release
  files' own Highlights are one click away in a `<details>` block
  (`docs/website/redesign/README.md`, "The 2026-10-19 changelog brief").
- Every other page's copy is rewritten short and scannable to D188's numbers: headings
  keep their meaning, the paragraphs under them shrink. FAQ answers stay traceable to the
  document each one names (each answer's `source`, which the page already enforces).
- The homepage's `h1` — "Fleeting cloud workers for coding agents: one task, one
  container, one PR, then gone." — is README's tagline, pinned byte for byte by
  `home.mjs` (D102); P4 does not reword it. P5 fixes where it sits.

**Two new gates.**

- **`website/test/meta.mjs`** — D186's phrase list, refused on every built page, with a
  fixture page carrying one of the phrases so the test proves it fails. The rule in its
  header: *a page must not explain why its own text exists.*
- **The word ceiling, in `website/test/budget.mjs`** — two new lines in `website/README.md`
  beside the byte budgets, read by the same strict reader: **words in a page's lead** (the
  visible text of `main` between the `h1` and the first `h2`) and **words per page** (all
  visible text of `main`, the three reference pages exempt by route). D188's
  recommendation is 40 and 250. Each number has a case that proves one word over fails.

**Where a gate moves.** `website/README.md`'s budget section gains the two numbers and
their derivation (reconciliation §6's counts are the input); `budget.mjs` gains the reader
and the cases. `changelog-page.mjs` gains the summary block as rendered release-file text
and keeps refusing any sentence not in a release file. `docs-hub.mjs`, `faq-community.mjs`,
`caps-and-costs.mjs` and `security.mjs` are re-run unchanged; where one asserts on a
removed paragraph, that assertion is removed in the same commit and named in its message.
If D189 takes alternative (a) instead, Global Constraint 6 is amended in
`../implementation-plan.md` and `changelog-page.mjs` and the security pin each gain an
exception for a marked summary block, in this part.

**Departure: Global Constraint 12, and where the security page is held now** (the owner's
brief of 2026-10-19, D189; recorded 2026-10-19, as shipped on this branch, and amended by the
owner's brief of 2026-10-20 for the drawing the page now opens with). The first
plan's Global Constraint 12 says a security claim is never paraphrased, and gives it a
shape: **the security page is generated from `SECURITY.md`, byte for byte below the title,
with its headings unchanged so the document's own cross-references keep pointing the right
way** (`../implementation-plan.md:49`). §5 of this plan turned that into a gate,
`TestWebsiteSecurityPageIsByteIdenticalToSecurityMd`, and the reconciliation's constraint
table recorded that D189's recommendation kept it. The brief replaced that shape, and two
things about the constraint are not what this branch ships:

- **The page is not the document.** `/security/` is the brief's three things and nothing
  else: the document's own "In short" section as the page's summary, its own "Reporting a
  vulnerability" section, and a link to the policy. **The owner's brief of 2026-10-20 made it
  four**: the boundary diagram the page opens with — one inline SVG of the trust
  boundaries, its labels the document's own words, the same facts as a list under it and the
  document's two limit lines below that. The cross-references those two sections
  carry are rewritten — `#known-limits` becomes a link into `SECURITY.md` in the repository
  at that section, because the page does not render the section it names, while
  `#reporting-a-vulnerability` resolves to the page's **own** anchor, because the page does
  render that section, below the summary that points at it — and a reference
  whose fragment names no heading of the document is rendered as its own text rather than
  as a link, which would land the reader at the top of the file under a word that promises
  a section. So the page is not byte-identical to the document, and its headings are not
  the document's. **What replaces that half of the constraint is
  `website/test/security.mjs`**: it holds the page to the document bullet for bullet and
  word for word, holds each rewritten cross-reference to the section that answers it — the
  page's own anchor where the page renders that section, the repository URL for the rest —
  refuses a
  reference rendered as a link where the document has no heading for it, and refuses a page
  that is anything other than those four things. That file — not the Go test —
  is where a change to the page's shape fails.
- **The drawing changes what "never paraphrased" has to mean, and the gate carries the
  change** (the owner's brief of 2026-10-20). Every word of the page is still the document's —
  the drawing's labels are phrases of `SECURITY.md` and a phrase the document does not carry
  stops the build (D99) — but a picture **arranges** the words it is made of, and two of the
  document's sentences set beside each other can read as a claim the document makes only as a
  conditional. So the constraint's substance is kept and stated more exactly: no security claim
  on the page is a word the document does not use, and the arrangement is a decision written
  down in `security.astro` (its decision 3) because no test here reads a picture's arrangement —
  the one place it could mislead is the model-key card, whose conditional the page draws as the
  one sentence the document states it in.
- **The Go pin reads the generated copy, and always did.** The constraint's other half
  survives untouched and is what `TestWebsiteSecurityPageIsByteIdenticalToSecurityMd`
  actually enforces: it reads **`website/content/security.md`**, the file the content
  pipeline writes from `SECURITY.md` (`go run ./internal/clidoc`, W1.3), which is the
  document byte for byte below its generated banner — it never reads a page, and it never
  did. Reading the generated copy is the point (a test written against the generator agrees
  with whatever the generator does), and its own header records what reads what.

  So Global Constraint 12 holds of the copy and of the pipeline, and the brief of
  2026-10-19 moved its page-level half from "the page is the document" to "the page is the
  document's, and `website/test/security.mjs` holds it there".

**Verify.** The two commands of §3, with `meta.mjs`'s and the word-ceiling cases among
the passes; and the owner reads the rewritten pages (the P9 "after" set is the picture of
it).

**Independently mergeable because** it changes page bodies and tests; the menu, the
palette and the scripts are untouched. The `/security/` summary waits on the owner's text;
everything else in P4 does not.

**Amended 2026-10-19 (the owner's request of 2026-10-19) — THE PRIVACY STATEMENT IS THE PRIVACY POLICY THE APP STORES LINK, AND THE WORD CEILING IS NOW DERIVED FROM ITS OWN COUNT.** The request is one page: the App Store and Play listings need a single privacy policy URL, and `/privacy/` is it, so the page has to cover the software as well as the site. The paragraph above is read with this amendment: **D188's 250 is no longer the whole-page ceiling** — the privacy statement is the one page of the site that is not the site's pitch, and the number is derived from it rather than picked.

- **The page carries two named parts.** "The website" is what it always said, in substance unchanged; "The software" says what the command line tool, the desktop console and the phone app do with a reader's data. Every sentence in the second part is held to the thing that makes it true, and `website/test/privacy.mjs` (new, and the ninth gate in `website/README.md`'s list) is what holds it: **the two parts in order and as siblings at the same level** (a part nested inside the other is a page whose two scopes cannot be read, which `consent.mjs`'s own reader refuses too); **the app's reporting held in both directions** — the page names Firebase Crashlytics and Firebase Analytics, so the gate refuses that naming unless the repository holds the plan for it or `mobile/package.json` carries the two packages, and refuses either package declared while the page stays silent — with the paragraph's claims held by the gate rather than copied here — each is a sentence the gate looks for, and the owner's ruling of 2026-10-19 is what they say now: the versions that carry the reports send them, with no switch inside the app to turn them off, and the clause about the two other tools is read as its sentence and not as its phrase, because the phrase is true of the website too and a software-part sentence calling the website unmeasured would contradict the page's own first part — and the "earlier builds send nothing" sentence required until the reports exist; **no undisclosed reporter and no advertising identifier** — every executable file, manifest and lockfile under `mobile/` is scanned for the analytics, crash-reporting and advertising SDKs and for the platform's advertising-identifier APIs, with the app's own two reporters admitted by name so the scan and the page agree about what the app carries; `SECRET_ITEMS` exactly D111's `idToken` and `macKey` with the page naming them, secure storage, the Keychain and the Keystore, and the tuple's `firebaseProjectId` behind "your own Firebase project"; the update check read from `internal/updatecheck` (GitHub's release list for **this** repository, a 24-hour cache) and the two switches `internal/cli` and `internal/localcfg` define; no production source under `cmd/`, `internal/`, `console/src/`, `mobile/src/` or `shared/` naming the host this project operates; and the contact section as the issue tracker and `SECURITY.md`'s reporting route with no e-mail address on the page. Each rule is one function with its own case, and each has a second case that plants the defect it refuses.
- **The ceiling, and the half of it that is D188's.** The number is the page's own count on the gates' own build plus 25 %, rounded up (D192's headroom rule) — **and it is not written here**: `website/README.md` states the two figures and `budget.mjs`'s `wordsCeilingFailures` refuses a README that states anything else, in the commit that moves the page. The count has moved twice under this amendment (the software part, then the app's reporting, then the deletion section's carve-out), which is exactly why no figure belongs in a document that is not the one the gate reads. **The raise is that page's and nobody else's**: the same function refuses any other page over D188's 250, so raising the number for one page did not retire D188 for the site — which is the failure this half exists to prevent, and it would have been silent.
- **`consent.mjs`'s vendor rule reads the site's part.** The software part names the reader's own Firebase project by design, and a scan of the whole page read that as the site's measurement naming a vendor it does not have. The rule is scoped to the site's part and is otherwise unchanged, and a new case plants a vendor in the site part and watches it refused. **The scoping is by document order of the two part headings, and it refuses a page where they do not share a parent**: the first cut read the first heading's siblings and dropped the last, which reads a page whose "The software" sits inside "The website" as though the site's part were the whole of `main` minus one heading — a scope that is not one — so the reader now throws on that page and a case proves it red. The software part naming Google in the shipped page's own words is what that case also holds: naming Google or Firebase there is allowed, and each is still refused named as what the *site* loads.
- **There is no terms page.** The owner ruled it in the same request, so nothing links one: `privacy.mjs` refuses a built page that does, and a sitemap that lists one.
- **Check 70b's step 5 moves with it** (the ceiling is read from `website/README.md`, and the page the README derives it from is named there), and the checks the page's own facts wait on are the owner's: the phone app's read path and its store listings.
- **The app's reporting is written into the page, and the pin runs both ways** (the second round on the owner's request of 2026-10-19, the same day). The owner ruled that the phone app **will** carry Firebase Crashlytics and Firebase Analytics, reporting to a Firebase project the Fugaro project operates rather than the reader's own, and asked for the page to cover it now. So the software part's phone-app section states in plain words what is sent (crash logs, diagnostics and app performance data; which screens are opened; the app-instance identifier Firebase generates), what is never sent (message contents, anything from the installation or its mailbox, credentials and keys), the single purpose, the **opt-in default** ("off until you turn it on, in the app's settings", which is the telemetry design's own answer of the same day and is listed for the owner as a consent default rather than asserted as a ruling), Google as the holder under Google's own Firebase privacy policy — **linked, not restated**, which is why `firebase.google.com` joined `site.yaml`'s `allowed_hosts` — and the sentence that this describes the version that carries the reports and that earlier builds send nothing. The old pin ("the mobile package carries no analytics dependency") is replaced by the bidirectional one above, and the reach scan's SDK list admits the app's own two reporters by name rather than leaving them in a list that would have failed the page's own disclosure. **One correction fell out of it and is worth reading as a rule**: `origins.mjs`'s flag-off half read every attribute for an analytics name, including an `<a href>` — and a link is a reference the reader follows, not a request the page makes, so the page could not point at the holder's own policy in the flag-off artifact without failing the gate written to keep the artifact free of the analytics loader. The link is now excluded from that half by name, with a case that proves both directions (a link is a reference; a script, an image or an inline block naming one is still refused), and every external link is still resolved host by host by `linkcheck.mjs`.
- **The documents that described the old page say what ships.** `docs/mobile/design/push-notifications.md` §9 argued from the page's deleted "What this page does not cover" section and sent the software's statement to `/security/`, and its Q8 rejected putting that statement on `/privacy/`; both now state the shipped truth (the page carries "The software", a push disclosure would be a subsection of it, and `/security/` is not where the program's statement is sent), while the trust-model half of that design's recommendation stands. The W3.2 note above is read with this amendment too.
- **The reach scan reads Go, and the case goes through the selector.** The rule above is the one rule whose subject is the whole tree, and its first cut selected the tree's scripts, manifests and lockfiles but not its Go source — 230 files, of which zero were Go, so `cmd/` contributed nothing and `internal/` only its testdata: the sentence "and to nothing the Fugaro project operates" was unenforced for the CLI and the server while the scan read enough files to look busy. The selector carries `.go` now (the `_test.go` filter then keeps the recorded exception out, as its own assertion always claimed), the gate case counts the Go sources it selected so a selector that loses `.go` fails there, and the defect case builds a tree with a production Go file and a Go test beside it and reads them through `softwareSources` itself rather than around it — which is why the first cut's case could not see this.
- **The app's reporting is always on, and the app carries no switch** (the owner's ruling of 2026-10-19, a third round on the same request). The bullet above states the **opt-in default** the telemetry design answered with, and this ruling supersedes it: the app versions that carry Crashlytics and Analytics send them with no in-app consent switch — not opt-in and not opt-out. So the phone-app paragraph that said collection was "off until you turn it on, in the app's settings" now says the versions that carry the reports send them, that there is no switch inside the app to turn them off, and what a reader can do instead (uninstall the app, or do not install it), with the command line tool and the console named as carrying none of these reports — **the website is not in that clause and must not be**: part one of the same page discloses the site's own Google Analytics measurement under the consent it states, so a software-part sentence calling the website unmeasured would be the page contradicting its own first half, and no rule in `website/test/privacy.mjs` reads the flag that decides that half; and "Deleting your data" says that uninstalling the app stops the sending, where it said the switch did. `website/test/privacy.mjs`'s claims move with the page, each new sentence held by its own case, and the page's word ceiling and the site's byte figure are re-derived in `website/README.md` in the same commit (D192). **What is not argued on the page:** always-on analytics identifiers for a reader in the EU usually need prior consent, and that is the owner's legal review to make rather than a sentence the statement argues.
- **What does not move.** Every other page's ceiling (250), the lead's 40 words, the three exempt reference pages, and the page's own website half.

### P5 — the gist above the fold (RF5, RF23; D187)

**What.** The homepage hero is rebuilt so that the `h1`, the one-line promise and the
first command are inside a 390 × 844 first screen (today the `h1` alone ends at 893 px,
below the fold, because the header is 177 px and the hero is tall). Getting Started's
first screen becomes its first command. This is a layout change in
`website/src/pages/*.astro` and `base.css`'s spacing — not another rewrite of the copy
(P4).

**Where a gate moves.** None hermetically: no browser-free test can see a fold, which is
exactly D187's question. If the owner takes D187's alternative (a), this part adds the
browser-driven test to `website/test/` and the dev dependency to `website/package.json`,
and the browser-free posture becomes a recorded change rather than an accident.

**New live check: 70b** — the fold, at 390 × 844 and 1280 × 900, on the built artifact,
for every route.

**Verify.** The two commands of §3; the capture script's measurements
(`h1Bottom` < 844 at 390 for every route, from the JSON it writes); Check 70b by the
owner.

**Independently mergeable because** it moves blocks within pages, and neither P4's nor
P5's tests assert the other's layout. It reads best after P4.

**Amended 2026-10-19 (owner's feedback on the page this part is about) — GETTING STARTED IS
THREE BANDS, AND WHAT THE COMMAND DOES NOT DO IS ITS OWN SECTION.** The owner's reading of
the page, after P4 and P5 shipped: "above the fold: just `brew install dimipaun/tap/fugaro`
and Console links, then we need a section with what you need to do for the actual
installation (Firebase etc), rest are details, later." So `getting-started.astro` is now
`#install` (the quick path `install.yaml` carries first, with its copy control, beside the
console option, and one closing sentence), `#setup` ("Set up your installation": seven
one-line steps in `docs/gcp-setup.md`'s own order — its preconditions and its short path —
each naming the document that carries the detail), and `#details` ("Details": the other
install paths as P4 left them, and the platform list D94 settles). **Nothing P5 claims is
undone**: `#install` still opens with a command, the two options are still in that section
in that order, and the page's first command block is what Check 70b measures. The page
stays inside D188's 250-word ceiling, so the words the new list costs are paid for out of
the page's own copy rather than out of the ceiling — `install.yaml`'s archive, cosign and
plugin notes and names are shorter for it, and the notes the Go test pins (the Homebrew
"tested on macOS", the go install's three phrases) are untouched. `install.yaml`'s header
now says the first path is the quick path, because that is what puts it above the fold.
The bands are held by a gate rather than by this paragraph: `website/test/fold.mjs` gains a
fourth rule — Getting Started's sections are exactly `#install`, `#setup`, `#details` in
that order, `#setup` is an `ol` and every step in it names the document behind it — so the
two things the reader came for cannot drift back down a page that grows. The site's own
build is 1,654 bytes heavier (12 of them the Firebase step's own wording), re-derived in
`website/README.md` in the same commits (D192).

### P6 — a copy button on every command block (RF13, RF14, RF15; D183, D192)

**What.** One first-party script for the whole site — recommended at
`website/public/scripts/site.js` — and a copy control on every command the site prints:
the hero's `<pre>` blocks (`Hero.astro:73`), Getting Started's (`getting-started.astro:113`),
the docs hub's (rendered from the generated reference by `docs.astro`'s renderer, so the
control is added there and not in the markdown), and the usage lines on Caps and costs
(`CapsCosts.astro`'s `code.usage`, which are commands but not `<pre>`). Each control has
an accessible name, a keyboard path, a visible focus ring, a "copied" state announced
politely (`aria-live="polite"`) that clears itself, and **no broken control** when the
script does not run: it is rendered `hidden` and the script reveals it. The same file
carries P3's Escape/outside-click close for the menu.

Measured today: 23 `<pre>` blocks (home 3, Getting Started 10, Docs 10) and zero
controls (RF13).

**Where a gate moves — named, in this commit, per the owner's ruling.**

- `website/README.md:89-90` — "The site ships no script today" is no longer true, and the
  sentence about the allowance is rewritten. **The numbers are re-derived (D192):** the
  tight figures in `budget.mjs`'s header become this build's (JavaScript per page: the
  script's bytes, plus 25 %), and the README's three numbers become three times those. If
  D192 takes alternative (a), the numbers stay and only the prose moves.
- `website/test/budget.mjs:16-17` — "JavaScript per page: 0 bytes. The built site ships no
  script at all today" becomes the new derivation.
- `website/test/budget.mjs` — a case over the **built** site: every page that references
  `site.js` counts its bytes once, and the site's total JavaScript equals the file's size.
  (The fixture case that a file-fetched island is counted already exists — "an island over
  the JavaScript budget is refused, inline or as a file"; what the build has never had is
  a real script to count.)
- `website/test/origins.mjs` — **no change** under D183's recommendation: the script is a
  `'self'` file and `INLINE_SCRIPTS` keeps its single consent-banner entry. Under the
  inline alternative, one sha256 joins that Map and the same `'sha256-…'` joins
  `firebase.json`'s `script-src`, in this commit.
- **`website/test/copy.mjs`** (new) — every `<pre>` and every `code.usage` on every built
  page carries exactly one control with an accessible name, rendered `hidden`; with a
  fixture page missing one, so the test proves it fails.

**New live check: 70c** — on the built or deployed site, each kind of control copies its
command to a real clipboard, by mouse and by keyboard, and nothing is shown with
JavaScript off.

**Verify.** The two commands of §3; Check 70c by the owner; and D105's Lighthouse numbers
re-run by the owner (RU4), because this is the part that first moves them.

**Independently mergeable because** it adds one public file and one control per command
renderer, plus the budget's prose, numbers and cases.

### P7 — the homepage run animation (RF9–RF11; D184)

**What.** `website/src/components/Terminal.astro` grows from one typed command into the
shape of a run, in D184's recommended form: today's `fugaro run "…"` line, then the
numbered steps of `README.md`'s `## How it works` — Start, Sync, Loop, Push and open the
pull request — each line held at build time to the document it came from, exactly as
`ArchitectureDiagram.astro` holds its captions today (RF11), and one caption on the page
saying it is the documented sequence, not a recording. (Under D184's alternative (a), the
lines are a committed, dated fixture of a real run's output instead, and the build holds
the page to the fixture.)

**Still no script.** Multi-line typing is CSS: one reveal per line, staggered with
`animation-delay`, `steps(<characters of that line>, end)`, with the whole transcript in
the markup. `Terminal.astro` has no guard of its own today and relies on the layout's
global reduced-motion rule (`Base.astro:193`, RF17); that rule still neutralises every
line, so a reader asking for less motion reads the whole transcript, still.

**Where a gate moves.** `website/test/home.mjs` holds the current single command to
`README.md` and to `content/cli-reference.md`; it grows to hold **every** line of the
transcript to its source, and it keeps the case that fails when one line is not in the
document it cites. A CSS animation is not JavaScript; the page's weight grows by a few
hundred bytes of CSS, inside the budget D192 sets.

**Verify.** The two commands of §3; and the owner watches the homepage, then sets
`prefers-reduced-motion: reduce` and watches it again (the transcript must be complete
and still).

**Independently mergeable because** it touches one component and one test.

### P8 — How It Works, rebuilt as a flow; and the picture on the homepage (RF12, RF20, RF21; D185)

**What.** `website/src/pages/how-it-works.astro` and
`website/src/components/ArchitectureDiagram.astro`: under D185's recommendation, the
brief's four steps as a vertical flow — task submitted, ephemeral Cloud Run job, agent
runs, pull request — one short **quoted** caption each (reconciliation RF12 lists them;
the component's substring check, unchanged, holds each to its source), an arrow between
steps, and `cache` and `security boundary` as annotations on the job step. The picture is
first-party SVG/CSS — no image, no third-party asset, no player (RF20). The same component
is placed on the homepage below the hero, which is the brief's item 5 "good images or
diagrams that show how the system works" (RF21: the site has no picture of itself today).
Under D185's alternative (a), the five stages stay and only the drawing changes.

**Where a gate moves.** `website/test/home.mjs`'s "the spec's five stage names and no
sixth" becomes "the four steps, in order, and the two annotations", and its caption check
stays; `a11y.mjs` keeps the steps an ordered list a screen reader reads in order, the art
`aria-hidden`, and the diagram's reduced-motion path. Both changes keep a failing fixture.

**Verify.** The two commands of §3; the owner compares the page against the approved
design (P9).

**Independently mergeable because** it is the only part that touches the diagram; on the
homepage it adds one block below the hero, which P5 and P7 do not touch.

### P9 — the design record and the "after" set (RF26; D190)

**What.** `docs/website/redesign/design.md` — the numbers the approved design fixed
(type scale, spacing rhythm, the accent's roles, the menu groups, the flow's shape),
recorded so that a later page change has something to keep to. And the "after" set, taken
by [`capture-screenshots.mjs`](capture-screenshots.mjs) with `docs/website/redesign/after`
as its argument (the command is in [current/README.md](current/README.md)), **not
committed** (`after/*.jpg` is ignored): the owner compares it against the regenerated
"before" set and the approved design. That comparison is **the owner's, not an agent's** —
the model that takes these pictures cannot view one (RU1).

**Where a gate moves.** None. This part is one document; nothing in the build reads it.

**Verify.** `git show --stat HEAD` lists only `docs/website/redesign/design.md`; and,
after the capture, `ls docs/website/redesign/after | sed 's/-part.*//; s/-fullpage.*//' |
sort` lists the same 22 route-viewport names as the same command over `current/`.

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

**The register's numbering — proposed, not taken.** The register's highest written
entries are `69a` and `69b` (W2.2); `69c` is W2.3's and not written yet. Above that,
`../../mobile/README.md`'s allocation table holds checks `80`–`89` (mobile), `90`–`99`
(the credentials service) and `100`–`109` (the mobile functional spec), and records
`70`–`79` as "unused and deliberately not taken". No document on `main` or in an open pull
request writes `70` (checked 2026-10-18). This plan therefore **proposes** `70a`–`70c`,
inside the website work's "69 onward", for the owner to confirm with the D-block
(reconciliation §9); each part re-reads the register and the allocation table before it
writes its check, and if `70` has been taken by then, it stops and the owner allocates.

## 5. What moves, in one table

Explicit, because the owner's ruling says the tests move in the same plan as the rule
they enforce:

| rule or number | where it is written | what happens | which part |
| --- | --- | --- | --- |
| JavaScript per page: 100,000 B | `website/README.md:89-90` | **re-derived** from the build that ships the script (D192), and the sentence that says "the site ships no script today" is rewritten; under D192's alternative (a), only the sentence | P6 |
| page weight and whole site: 662,766 B / 1,454,826 B | `website/README.md:91-94` | re-derived in the same commit by the same rule (D192); later parts that add bytes re-derive in their own commit | P6, P7, P8 |
| "JavaScript per page: 0 bytes" derivation | `website/test/budget.mjs:16-17` | rewritten to the new derivation | P6 |
| the JS accounting over the real build | `website/test/budget.mjs` | **new case**: the built site's script counted once per page that references it | P6 |
| words in the lead, words per page | `website/README.md` (two new lines) + `website/test/budget.mjs` | new numbers (D188), new reader, new cases | P4 |
| `INLINE_SCRIPTS` (sha256 allow-list) | `website/test/origins.mjs` | **unchanged** under D183's recommendation (a file script); one entry if the inline shape is chosen | P6 |
| `script-src 'self'` | `firebase.json` | **unchanged** under D183's recommendation; under the inline shape, the same `'sha256-…'` as `INLINE_SCRIPTS`, in the same commit | P6 |
| the palette in force | `website/src/styles/tokens.css`'s import, `a11y.mjs`'s `PALETTE_IN_FORCE` and `PALETTE_FILES` | a fourth file, `palette-logo.css`, joins the list and becomes the one in force | P2 |
| manifest colours = `--ground` | `website/public/site.webmanifest` | follows `--ground`; `brand.mjs`'s equality assertion is unchanged | P2 |
| the accent versus the logo's hue | `website/test/accent.mjs` (new) | new gate; decodes the PNG with `node:zlib` | P2 |
| `.site-nav ul{display:flex}` and the one-`aria-current` rule | `website/test/base-styles.mjs` | rewritten for the two-level menu, with the page's current marker still exactly one | P3 |
| provenance phrases | `website/test/meta.mjs` (new) | new gate | P4 |
| "the page adds no sentence of its own" | `website/test/changelog-page.mjs` | **unchanged rule**; under D189 the summary block was release-file text it accepted, and since the owner's changelog feedback of 2026-10-19 the two sources are the release files and `website/data/release-summaries.yaml`, both of them files the page renders rather than prose it writes | P4 |
| the security page = `SECURITY.md` byte for byte | `TestWebsiteSecurityPageIsByteIdenticalToSecurityMd` (the generated copy) and `website/test/security.mjs` (the page) | **departed from, as shipped**: the Go pin still holds `website/content/security.md` to `SECURITY.md`, and the page is held by `website/test/security.mjs` (P4's departure note) | P4 |
| copy controls | `website/test/copy.mjs` (new) | new gate | P6 |
| reduced-motion path per animation | `website/test/a11y.mjs` | **unchanged**, and every new animation must satisfy it | P7, P8 |
| the homepage transcript | `website/test/home.mjs` | grows from one line to every line | P7 |
| the diagram's five stage names, captions and semantics | `website/test/home.mjs`, `a11y.mjs` | four steps and two annotations under D185; the quoted-caption check stays | P8 |

`website/package.json`'s check script is `node --test test/*.mjs`, so a new test file
under `website/test/` is picked up with no edit there. The Go pins (`internal/cli`,
`internal/clidoc`) are untouched by every part: no part changes a generated document, the
canonical host or the install facts, and the generated `website/content/security.md` stays
byte-identical to `SECURITY.md` (P4's summary is added to the file itself, by the owner).
The **page** the briefs cut to the document's own sections and the drawing above them is a
departure from Global Constraint 12's
byte-for-byte shape, recorded in P4's departure note; it is held by
`website/test/security.mjs`.

## 6. Order and the owner's go

The recommended order is P1, P2, P3, P4, P5, P6, P7, P8, P9, one pull request each. P1 is
free. P2 and P3 are independent of each other and of everything after them. P4 and P5
read best in that order (the words, then the fold); P4's `/security/` summary waits on the
owner's text for `SECURITY.md`. P6 completes P3's menu with Escape/outside-click close but
P3 is whole without it. P6 and P7 are independent of each other, P8 is independent of all
of them, and P9 closes the line.

**No part starts on its own.** The first plan's §7.1 holds: a wave starts on the owner's
go, and §15's approval gate holds for each. This plan's nine parts are nine such
decisions, and the order above is a recommendation about sequence rather than a schedule.

## 7. What this plan does not do

It does not design the site: the type scale, the spacing rhythm, the copy itself and the
menu's labels are the design step's (D190), constrained by the rules in RF11, RF12, RF20
and RF21. It does not add a dependency (unless D187 takes the browser-driven test), a
workflow, a hosting-policy change (unless D183 takes the inline shape) or a third-party
asset, and it commits no screenshot. It does not touch `belong`, EdgeWeb or EdgeServer, reading or writing
nothing outside this repository. And it does not deploy anything: D92's live checks and
D191's sequencing are the owner's.

## 8. Decision numbering

Decisions are allocated by the owner, block by block, and the allocation is recorded in
[`../mobile/README.md:20-42`](../mobile/README.md) — the one table that lists every line of
work's block. Reading it: `D1`–`D20` (console), `D21`–`D38` (console v2), `D39`–`D54` +
`D54b` (voice), `D55`–`D78` (managed projects), `D90`–`D109` (this site's first line of
work), `D110`–`D129` (mobile), `D130`–`D159` (the credentials service, `D155`–`D159`
reserved) and `D160`–`D179` (the mobile functional spec, `D174`–`D179` reserved) are taken,
and `D79`–`D89` is a deliberately unused gap ("an unused number is not a free number").
**The redesign therefore proposes `D180`–`D192`**, the first block above the highest
allocation, and the checks `70a`–`70c` (§4) — both for the owner's confirmation rather than
assumed, in [reconciliation.md](reconciliation.md) §9. A reserved number is never re-used
and never re-numbered: if the owner allocates differently, the citations here and in the
reconciliation follow the allocation rather than the other way round.

The thirteen decisions, recommendation first (the full table, with alternatives and costs,
is reconciliation §9):

1. **D180** — the accent: the logo's hue, `#17A8E6`, as a fourth palette file made the one in force.
2. **D181** — the warning colour: keep the role, move it to amber `#F5A524`.
3. **D182** — the menu: *Product*, *Reference*, *Project*, Home as the wordmark, Privacy stays in the footer, a drawer on mobile — **the drawer is retired (owner's brief, 2026-10-19, item 1): the three group labels are the phone's menu**, and the groups themselves are unchanged (the amendment at the end of P3).
4. **D183** — the JavaScript: one first-party file, no CSP or allow-list edit.
5. **D184** — the run animation: today's command plus README's numbered steps, labelled as the documented sequence (alternative: a dated capture of a real run).
6. **D185** — How It Works: the brief's four steps with quoted captions, cache and security boundary as annotations.
7. **D186** — "no internal text": a hermetic gate over an explicit phrase list.
8. **D187** — the fold: a word ceiling plus owner-run check 70b, no browser in the gates.
9. **D188** — the ceiling: 40 words in every page's lead, 250 per page except the three reference pages.
10. **D189** — the long pages' summaries: rendered from files (an "In short" section in `SECURITY.md`, the releases' own Highlights lines, the hub's `answers`), never written on the page.
11. **D190** — the design lives in the owner's tool; the repository keeps `design.md`, the scripts and no screenshots.
12. **D191** — sequencing: the redesign before the remaining `W2`/`W3` waves.
13. **D192** — the byte budgets: re-derived from the build that ships the script, by the README's own rule.
