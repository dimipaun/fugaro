# Website redesign

**Status: plan only; nothing of the redesign is implemented.**

The site itself, however, **is** built: `website/` holds an eleven-route Astro site whose
hermetic gates pass (179 tests, `npm run check`, measured 2026-10-18). The three
documents of the first line of work — [`../README.md`](../README.md) `:3` and `:5`,
[`../reconciliation.md`](../reconciliation.md) `:3`,
[`../implementation-plan.md`](../implementation-plan.md) `:3` — still say the opposite,
in the present tense; correcting them is the first part of this line of work
(reconciliation finding **RF1**).

Owner: **@Dimi**. Date: **2026-10-18**. Source: the owner's text, committed verbatim and
alone in this branch's first commit.

## The documents

| file | what it is |
| --- | --- |
| [spec.md](spec.md) | **The owner's text, verbatim.** Nothing in this directory edits it, and nothing may: it is the thing the rest answers |
| [reconciliation.md](reconciliation.md) | What the brief asks for, set against what the built site does — findings `RF1`–`RF26`, each **Wrong**, **Exists**, **Gap**, **Rule** or **Open**; the conflicts with the first line of work's decisions (`D90`–`D109`, notably D104's palette and D105's budget), with the CSP and the origins gate, and with the content pipeline's generated-file banner; the logo's sampled hue, the derived palette and its WCAG contrast matrix; and the measurements of the current site |
| [implementation-plan.md](implementation-plan.md) | Nine ordered, independently mergeable parts (`P1`–`P9`), each with a runnable verification and the **named** test change wherever a budget, a gate or a pinned number moves; plus the live checks (`70a`–`70c`) the brief's "Done when" lines need |
| [current/](current/) | The **"before"** set: every route at 1280 and 390, taken with Playwright on 2026-10-18, with the measurements beside them. No agent has looked at an image in this set; the visual comparison is the owner's |
| [capture-screenshots.mjs](capture-screenshots.mjs) | The script that took it, committed so the set is re-runnable. It installs nothing into `website/` and `website/package.json` is untouched by it |

## The decisions

The twenty decisions of the first line of work are `D90`–`D109` and are all answered.
This line of work's twelve are **`D110`–`D121`**, listed with their recommendations in
[reconciliation.md](reconciliation.md) §9 and repeated in the pull request body — a
decision taken silently in this branch would be the one kind of change nobody can review.

## What this line of work may not do

It is **documents only**; the parts the plan describes are later pull requests, each
starting on the owner's go. Nothing under `.github/workflows/` is created or edited (a
workflow that were needed would be proposed under `docs/pending-workflows/` for the owner
to copy). No cloud action of any kind, no secret read or written, and `belong`, EdgeWeb
and EdgeServer are never touched. The strict CSP (`script-src 'self'`, no remote origins)
and the origins gate stay; the JavaScript budget and the 0-byte-JS prose change only
deliberately, with their tests, as the owner's ruling of 2026-10-18 requires.
