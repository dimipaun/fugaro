# Fugaro Rigor Modes Spec

Oct 20, 2026 · @Dimi

## Problem

Dogfooding Fugaro works, but every patch is gated at maximum rigor: every finding is fixed, every patch heavily checked. That is slow and expensive in time and money, and does not match how real teams operate. Rigor should be a dial, set to fit the situation.

## The modes

Modes sit on one axis: how much cost we accept to reduce risk.

1. **POC** — Happy path only. Damn the torpedoes; just make it work.
2. **Startup** — Good foundation. Happy path plus common error paths. Rare, expensive corner cases go to the backlog.
3. **Pro** — Established-company quality. Aim for bug-free patches, but balance cost against likelihood; extremely rare, high-effort issues may stay unfixed.
4. **Critical** — Maximum care about everything.
5. **Hotfix** (off-axis) — Production is on fire. Time is the dominant constraint (see below).

## Findings policy per mode

Modes differ mainly in what happens to reviewer findings, not just how hard the reviewer looks:

| Mode | Findings handling |
| --- | --- |
| POC | Ignore (except catastrophic) |
| Startup | Fix common; log rare/expensive to backlog |
| Pro | Weigh fix cost vs. likelihood × severity |
| Critical | Every finding blocks |
| Hotfix | Only findings that make the fix wrong or unsafe block |

## Finding scoring

The reviewer scores each finding on three axes:

- **Likelihood** — how often it will occur
- **Severity** — how bad it is when it does (crash, data loss, security, cosmetic)
- **Fix cost** — effort to address

Each mode is a set of thresholds over these scores. Severity acts as an override: rare but catastrophic findings (data loss, security) block even in Startup mode.

## Scoping

- A project has a default mode.
- Paths/areas can override it (e.g. payments or ledger code locked to Critical while admin UI runs in Startup).
- A task can request a mode explicitly, but cannot go below a path's locked floor.

## Hotfix mode

Time is precious; every check must justify its delay.

- **Minimal diff** — smallest change that stops the bleeding.
- **Gating proportional to fix confidence:**
  - Trivially, obviously correct fix (e.g. missing null check flooding logs with NPEs) → ship immediately, no pre-deploy tests.
  - Less certain fix → fastest checks only (compile, targeted test).
- **Full test suite runs after deploy**, asynchronously, to catch regressions without blocking the fix.
- **Automatic follow-up task** to revisit the fix properly in the area's normal mode.

## Open questions

- Exact scoring scale (e.g. 1–5 per axis) and per-mode thresholds.
- How the reviewer calibrates "common" vs "rare" consistently.
- Where mode config lives (project Git repo alongside recipes?).
- Who may invoke Hotfix, and how fix confidence is assessed?
