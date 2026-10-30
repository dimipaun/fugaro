# Review levels, A/B for every slot, job max, crash reports, console graph and gauges

**Status: plan only; nothing is implemented; no task approved.** The owner answered D720–D737 on
2026-10-30, every one as recommended except **D730** (fully automatic `/jobmax` launches, not the
recommendation). The answers approve no task. Still open: tension **OT1** (`/jobmax` guardrails), the
crash-report processor's name (**O4**; the console tasks stay draft until then), how a level meets a recipe without its step (**O7**, gates L3.1), where a teammate's console and phone read the owner's crash-report switch (**O9**, gates slice T), Hotfix's default levels (**O10**), and counsel's review
of Terms §7.3 and the EU opt-out question (**O1**, **O2**).

**Spec owner:** Dimi Paun. **Spec received:** 2026-10-30. **Reconciled against** `origin/main` at
`24fe1de60` and the 11 PRs open on 2026-10-30, re-checked at `8301c23c8` against the 12 then open.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec "Fugaro: Review Levels, A/B Testing, Job Max", as received, with one redaction (below) |
| [reconciliation.md](reconciliation.md) | Findings F1–F44 against the code, decisions D720–D737 (answered 2026-10-30), open tension OT1, open items O1–O10, UNVERIFIED U1–U8. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices L, B, J, T, G, V and task X1; levels 0–6; the critical path; the shared-files table; Checks 270–273. **Nothing in it is approved** |

## Changes to the spec as received

One redaction, no typo fixes. Section 6 named a real person as the inspiration for the live
dashboard. Repository docs name no real people, so `spec.md` replaces that name with "a
well-known cross-platform task manager author". This is the literal substitution the owner asked for, so the sentence now reads "inspired by a well-known cross-platform task manager author's cross-platform task manager", with the repetition kept as given. Everything else is byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D720–D759** | this work's block: **D720–D737** written and answered 2026-10-30; **D738–D759** reserved and unused. Taken above the highest number on `main` (D679, the top of forge-authorship-images' reserved block D600–D679; model-ab-testing holds D550–D579), above D680–D719, which the roles design run (`docs/design/roles.md`, run 20261030-112626-0ae3) reserved first, and above every open PR's docs |
| **Checks 270–289** | this work's block: **270–273** proposed by the plan (270–272 written by X1, 273 by X2; run by the owner; NOT RUN); **274–289** reserved and unused. Above forge-authorship-images' Checks 230–249, model-ab-testing's 210–219 and the roles design run's 250–259 |
