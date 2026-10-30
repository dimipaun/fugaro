# Review levels, A/B for every slot, job max, crash reports, console graph and gauges

**Status: plan only; nothing is implemented; no task approved.** The reconciliation is a
**DRAFT**: its decisions D720–D737 are not answered yet, and no implementation plan is written.

**Spec owner:** Dimi Paun. **Spec received:** 2026-10-30. **Reconciled against** `origin/main` at
`24fe1de60` and the docs and file lists of the 11 PRs open on 2026-10-30.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec "Fugaro: Review Levels, A/B Testing, Job Max", as received, with one redaction (below) |
| [reconciliation.md](reconciliation.md) | DRAFT. Findings F1–F44 against the code, decisions D720–D737 (unanswered), open items, UNVERIFIED. **Where it disagrees with `spec.md`, it wins** |

## Changes to the spec as received

One redaction, no typo fixes. Section 6 named a real person as the inspiration for the live
dashboard. Repository docs name no real people, so `spec.md` replaces that name with "a
well-known cross-platform task manager author". Everything else is byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D720–D759** | this work's block: **D720–D737** written (unanswered); **D738–D759** reserved and unused. Taken above the highest number on `main` (D679, the top of forge-authorship-images' reserved block D600–D679; model-ab-testing holds D550–D579), above D680–D719, which the roles design run (`docs/design/roles.md`, run 20261030-112626-0ae3) reserved first, and above every open PR's docs |
| **Checks 270–289** | this work's block, all reserved and unused until the plan is written. Above forge-authorship-images' Checks 230–249, model-ab-testing's 210–219 and the roles design run's 250–259 |
