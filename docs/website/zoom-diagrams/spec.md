# Fugaro Website — Diagram & Content Spec

Oct 21, 2026 · Dimi

## Concept

A zoom-out sequence: start inside the smallest unit and move up "a thousand feet" each step. Each level collapses the previous one into a single box, so readers feel the zoom without heavy captions. Keep shapes and colours consistent across all four levels (e.g. a job is always the same tile, the orchestrator always the same shape).

## Level 1 — Inside a cloud job

Purely zoomed in; no laptop, no orchestrator.

- Container boundary: one ephemeral Cloud Run job
- Deterministic checks first (compile, tests)
- Cheap loop: cheap coder ↔ cheap reviewer, up to N rounds
- Senior reviewer: one final review once the cheap loop passes
- On rejection: one bounce back into the cheap loop, then fail
- Output: draft PR, flipped to ready only on senior pass

## Level 2 — Orchestrator fans out jobs

A flow diagram.

- Local orchestrator agent calls Fugaro
- Fugaro spins up several jobs in parallel (each a Level 1 tile)
- Draft PRs flow back
- Optional: budget check gate before launch

## Level 3 — The developer's view

A map of where things live; "how do I interact?"

- Developer at the laptop, or remote via phone
- Laptop runs the console and the local orchestrator agent
- Developer talks to the orchestrator (keyboard, voice, phone)
- Orchestrator uses Fugaro → single arrow to the cloud (Level 2 collapsed)

## Level 4 — The whole project

- Shared Firebase/GCP project as the base everything sits on
- Inside it: coordinator (advisory), optional central orchestrators, security service, budget caps and kill switch, live counters and history
- Around the edges: multiple developers, each with laptop, console, local orchestrator; some on phones
- Local orchestrators report to the coordinator
- Doubles as the visual argument for GCP: everything lives in one project

## Presentation

- **Style:** match the current site — modern, elegant, powerful. Clean vector lines in the site palette, generous whitespace. Arrows and short technical labels are fine, but only a handful of elements per level.
- **Format:** both animated and still. A pinned, scroll-driven section where each level rests as a still image, then zooms out smoothly into the next. Side level markers let readers jump to any level. Build each level as SVG so transitions are mostly scale and fade.

## Homepage "See it" section

- Replace the single Fugaro Watch screenshot with a slider
- Lead with the desktop console and give it the most space (two or three slides: spending graphs, live tasks, review inbox)
- Fugaro Watch moves to the end as a "for the terminal crowd" slide
- Add phone app screenshots once available
- Short caption per slide (e.g. "Desktop console", "Terminal dashboard")

## Why GCP

We started out wanting a pluggable cloud (AWS, GCP, Azure). Then it became clear that Firebase is essential, and once Firebase is in, GCP follows naturally.

1. **Firebase is non-negotiable.** Fugaro needs the Realtime Database (live counters, agent roster, kill switch), Firebase Auth (identity provider), and Firestore (history). An object store could be swapped for S3, but the others have no clean equivalent.
2. **A Firebase project is a GCP project.** The compute (Cloud Run), Secret Manager and storage come with it, so there is no extra setup.
3. **One brain.** Secrets, IAM, billing and budgets all live in one place. A second cloud would split secrets and permissions across providers and make the whole thing a mess.
4. **Safety.** GCP projects are free and disposable. Fugaro gets its own isolated, Fugaro-managed project, so it never runs Terraform or cloud ops against existing infrastructure. That risk doesn't need taking.

**Answering the AWS objection:** the Fugaro project is a separate sandbox anyway, so it doesn't matter where your production runs. You're not moving anything to GCP, you're giving Fugaro its own walled garden.

**Suggested framing:** avoid saying "no real alternative". Say instead that no other cloud offers this as one integrated, disposable project.

## Site structure

- "Why GCP" page under the Product menu (or as an "Architecture" page), placed right after the zoom diagrams, since Level 4 already shows everything in one project
- FAQ entry "Do I need to be on GCP?" linking to it

## Mobile app page (coming soon)

- Sister page to the console page, with a "Coming soon" badge and an optional waitlist signup
- Lead: talk to your coding agents hands-free, no more copying and pasting specs
- Wake word: say "Fugaro" to talk to the app, otherwise speech goes to the active agent
- Switch between projects and agents by name
- Monitor and manage your agents on the go: live agent roster, status and spend, like Fugaro Watch and the console
- Pair with a quick QR scan, with viewer, operator and admin roles
