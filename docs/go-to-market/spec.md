# Fugaro go-to-market agenda: the engineering sections

> **Excerpt.** The owner's go-to-market agenda of 2026-10-26 holds business, marketing and sales
> plans that stay outside this repository (D403). This file copies verbatim only the sections that
> ask for work in this repository: the open-core line, the landing pages, `fugaro estimate` and its
> metrics, the enterprise must-haves, and the engineering rows of the action table. Pricing, outbound,
> content and partner material, and the named design partners, are left out. Each block
> below is a contiguous run of the original lines, unedited; the headings and this note are the excerpt's own.
> The landing pages' section below is the plan's intent as the owner wrote it on 2026-10-26; what the
> four pages render today, and where that reading departs from this text, is `reconciliation.md`'s
> D409, D410, D411 and D412, which win over this file. D412 is the owner's second read of the four
> pages on 2026-10-30: it supersedes this section's §5 reading of the calls to action, which now
> carry a small mark each, and its one-screenshot-per-page reading, since each page shows the
> console's every screen below the hero.

## Licensing context and action items (engineering rows)

Licensing context: open core. The engine stays MIT; enterprise features (console, phone app, workspaces) live in an `ee/` folder under a more restrictive source-available licence, GitLab/PostHog style.

Owner: **Dimi** = needs a human (accounts, payments, phone, relationships); **Agent** = a coding or drafting agent can do it.

| Task | Owner | When |
| --- | --- | --- |
| Build four landing pages per the spec | Agent | Before 1.0 |
| GA4 key events, Meta Pixel, LinkedIn Insight Tag, cookie banner | Agent | Before 1.0 |
| `fugaro estimate` command and raw token storage | Agent | Before 1.0 |
| SECURITY doc | Agent drafts, Dimi approves | Before 1.0 |
| SSO, audit logs, contract and DPA | Agent (code), Dimi (legal) | Before first big deal |

## 3. Open-core line (without its revenue paragraph)

Rule: a feature that only matters at team or org scale goes in `ee/`. Central policy (review loops, models, rigor, caps) stays in MIT core. Exact licence for `ee/` still to choose.

`ee/` features:

- Electron console
- Mobile app
- Workspaces
- Per-project coordinator agent
- Shared central orchestrators in the cloud
- SSO and roles (viewer, operator, admin)
- Audit logs
- Workspace-wide spend reporting

## 6. Landing pages (the task spec; the WhatsApp number setup is the owner's and is left out)

## 6. Landing pages

Where every post and video sends people. One template, a variant per campaign audience, each with its own tracking link:

- **Save** (savings videos): "Same PRs, a tenth of the price."
- **Control** (CTOs on LinkedIn): "One review policy, enforced for every developer."
- **Scale** (fleet and overnight-shift videos): "Your best engineer, times twenty."
- **Crew** (cats, yacht, casino on Instagram): lighter tone, same calls to action.

### Landing pages — Fugaro task spec

Goal: one static template on Firebase Hosting rendering four campaign variants from a single config file.

Paths (short, message-based, no `/landing` prefix): `fugaro.dev/save`, `/control`, `/scale`, `/crew`. Campaign identity via UTM tags, not paths. Variants are `noindex` with canonical to the home page.

Look: same brand as fugaro.dev (logo, fonts, colours), stripped down — no navigation menu, minimal footer. `/crew` uses playful cat imagery on the same brand.

Sections, top to bottom:

1. Hero: variant headline, one supporting line, muted looping video.
2. Proof number in big type (e.g. "90% lower AI coding spend").
3. How it works: three icons — cheap models do the work, premium models review, caps keep spend in check.
4. Social proof: design-partner logos (when available), GitHub stars.
5. Calls to action (no email waitlist):
    - **Book a demo** (primary): Calendly embedded inline, no page change.
    - **Install free** (secondary): links to GitHub.
    - **Chat with the founder** on WhatsApp: `wa.me` link to the separate WhatsApp Business number, message pre-filled with the variant, e.g. "Hi Dimi, I saw Fugaro on the save page."
6. Footer: GitHub, docs, pricing.

Config per variant: path, headline, supporting line, video URL, proof number, tone, WhatsApp pre-fill text.

Tracking: GA4 via Firebase; key events `demo_booked` (Calendly booking), `install_click` and `whatsapp_click`; capture UTMs and pass them into Calendly; Meta Pixel and LinkedIn Insight Tag; cookie consent banner for European visitors.

Done when: all four paths deploy, render on mobile and desktop, all key events fire in GA4 DebugView, and a test booking carries its UTM source.

## 10. The demo (the new feature only)

New feature for the demo: `fugaro estimate` — reprices a PR or job's stored token counts on any model from the price table. (Confirm the exact model identifier.)

## 12. Enterprise readiness

## 12. Enterprise readiness

Confirmed must-haves before the first big deal:

- SECURITY doc: trust model and secret flow
- SSO via Google and Microsoft
- Audit logs
- Support promise, e.g. reply within one business day
- Standard contract and data processing agreement

SOC 2 deferred until a buyer asks. Licence enforcement: signed licence key for `ee/`, no heavy DRM.

## 13. Metrics

## 13. Metrics

Funnel metrics, plus per-job cost benchmarking: report what each job would have cost on other models (e.g. Sonnet, Opus) alongside actual cost — labelled as an estimate, since other models use different token counts and retries.

Implementation: store raw token counts per job (input, output, cache reads, cache writes), not dollar figures. Cost on any model, including future ones or after price changes, is computed from a price table. For other models, assume the same cache ratio and label the result an estimate.
