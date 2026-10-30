# Review levels, A/B for every slot, job max, crash reports, console graph and gauges

**Status: plan only; nothing is implemented; no task approved.** The reconciliation is a
**DRAFT**: its decisions D680–D697 are not answered yet, and no implementation plan is written.

**Spec owner:** Dimi Paun. **Spec received:** 2026-10-30. **Reconciled against** `origin/main` at
`24fe1de60` and the docs and file lists of the 11 PRs open on 2026-10-30.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec "Fugaro: Review Levels, A/B Testing, Job Max", as received, with one redaction (below) |
| [reconciliation.md](reconciliation.md) | DRAFT. Findings F1–F44 against the code, decisions D680–D697 (unanswered), open items, UNVERIFIED. **Where it disagrees with `spec.md`, it wins** |

## Changes to the spec as received

One redaction, no typo fixes. Section 6 named a real person as the inspiration for the live
dashboard. Repository docs name no real people, so `spec.md` replaces that name with "a
well-known cross-platform task manager author". Everything else is byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D680–D719** | this work's block: **D680–D697** written (unanswered); **D698–D719** reserved and unused. Taken above the highest number on `main` (D679, the top of forge-authorship-images' reserved block D600–D679; model-ab-testing holds D550–D579) and above every open PR's docs (the highest there is D500) |
| **Checks 250–269** | this work's block, all reserved and unused until the plan is written. Above forge-authorship-images' Checks 230–249 and model-ab-testing's 210–219 |
