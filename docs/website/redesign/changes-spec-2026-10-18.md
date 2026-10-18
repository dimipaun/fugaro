# Fugaro website changes spec — 2026-10-18

Owner: **@Dimi**. Date: **2026-10-18**. Status: **agreed** — the owner and this line of work
agreed it on 2026-10-18, and the rulings that go with it are recorded in
[`README.md`](README.md), under "Owner rulings (2026-10-18), the changes spec". The text below
is the part of the spec this line of work implements, and it is committed here as it was
agreed: nothing in this directory edits it, and the items are reproduced whole rather than
summarised.

This is a **second** brief about the site, and it is not [`spec.md`](spec.md). That brief
asked for the site to be rebuilt short and visual, and the parts of it that were built are in
[`implementation-plan.md`](implementation-plan.md) (`P1`–`P9`); this one changes four of the
things that rebuild decided — the headline, the copy's vocabulary, the homepage's example and
the diagram — and where the two disagree this brief is later and wins. Findings in
[`reconciliation.md`](reconciliation.md) that the new text supersedes are named below where
they matter, and the rulings that carry them are in [`README.md`](README.md).

## What this run takes, and what it leaves

Items **2**, **3** and **4** are this line of work's next pull request, and each is
reproduced below in full: the problem it answers, what it asks for, and the acceptance it is
judged by. Item 5's `fugaro watch` screenshot is **made separately**, from a recorded fixture
through the real renderer (owner's ruling, 2026-10-18), so item 3 leaves a marked slot for it
and this run commits no image at all. The spec's remaining items are other runs' business.
Nothing in this document is implemented by being written here: the commits that implement it,
with their tests, are this branch's own — the document is committed first and alone, the way
both briefs were.

## Item 2 — new headline and subline

**Problem.** The current tagline ("fleeting workers in the cloud for coding agents") is
abstract.

**Headline:** `Lambdas for coding agents`. **Subline:** `Ephemeral cloud jobs that turn tasks
into pull requests.`

Use "jobs" instead of "workers" consistently across the site. Keep the subline
provider-neutral ("cloud jobs"); mention GCP / Cloud Run further down where the architecture
is explained.

**Apply to:** the home hero, `<title>`, meta description, Open Graph / Twitter card, and
`README.md`'s tagline (website and README only in this run; leave `plugin/skills/*`,
`internal/cli` help and `website/content/cli-reference.md` alone, they are generated from or
pinned to the CLI and change in 0.9.0).

**Acceptance:** the old "fleeting workers" wording no longer appears anywhere on the SITE or
its metadata or README (grep `website/` and `README.md`; the historical docs under `docs/`
stay as they are).

## Item 3 — homepage example: show the real workflow

**Problem.** The current example is a terminal where the user types `fugaro run <task>`; in
practice the coding agent invokes Fugaro through the skills and the user rarely runs it by
hand, except commands like `fugaro watch` or `fugaro upgrade`.

**What it asks for.** Replace the terminal example with a short AGENT CHAT illustration:
(1) the user asks their agent something like "Fan out these three bug fixes." (2) the agent
responds and kicks off Fugaro jobs through the skill (shown as a subtle tool-call / status
line, not a command the user typed); (3) a few minutes later the agent reports back with pull
request links. Add a small secondary snippet showing `fugaro watch` as the way to keep an eye
on running jobs (with its copy button). Remove `fugaro run` from the hero example (it stays in
the docs as an advanced/manual option). Leave a clearly marked slot right after the chat for
the `fugaro watch` screenshot that a separate task adds (an `<img>` is NOT part of this run;
do not add any binary file).

**Acceptance:** a first-time visitor understands Fugaro works THROUGH their agent, and the
only command they are likely to type is `fugaro watch`. The chat must be real markup (text,
not an image), accessible (a labelled figure, a readable order), and every command block keeps
its copy button. Use no invented stats and no fake product claims: the agent's lines show the
real shape of the skills' behaviour (read `plugin/skills/parallelism` and `working` for what
the agent really says and does).

## Item 4 — one bold, high-contrast diagram, no duplication

**Problem.** The home page has a simplified "how it works" section that duplicates the How it
Works page, and the visual (faint boxes and arrows plus a block of text) is hard to see and
dull.

**What it asks for.** Create ONE bold diagram, four steps: `1 Your agent`, `2 Fugaro`,
`3 Ephemeral cloud job`, `4 Pull request`. Optional animation of a task moving through the
steps (respect `prefers-reduced-motion`; if you animate, it must be CSS only).

**HOME:** shows the diagram with minimal copy (one short line per step) and the duplicate
text-heavy "how it works" section is REMOVED. **HOW IT WORKS page:** opens with the SAME
diagram (one shared component, not two copies), then adds the detailed explanation underneath,
so it reads as a zoom-in.

Dark only (see the ruling above) but built from theme tokens; legible on mobile (stack
vertically on narrow screens, header still one row at 390 px).

**Acceptance:** the diagram is the most visually prominent element in its section.
