# Review levels, A/B for every slot, job max, crash reports, console graph and gauges

**Re-planned 2026-11-21: T3.1, V1.3 and X1 are re-written against the orchestrator and the config-ownership registry, and X2 is closed — Check 273 was never measured and nothing is written for it. The re-plan is a document for the owner's review: nothing in it is approved and no task launches from it (see [implementation-plan.md](implementation-plan.md)).** **Ruling 2026-11-16: the orchestrator (`fugaro:orchestrator`) replaces `/jobmax`; J1.2, J1.3 and T1.1–T1.3 are closed as superseded (no `jobmax.auto_launch` is built); V1.2 is unchanged.** **Status as of 2026-11-10: in progress, about 24/40 merged; see [docs/status.md](../status.md) for the current table.** The owner answered D720–D737 on
2026-10-30, every one as recommended except **D730** (fully automatic `/jobmax` launches, not the
recommendation; ruled the same day with guardrails and a per-project opt-in, off by default). The answers approve no task. The open items O7, O9, O10 and O11 were ruled the same day (reconciliation §4). Still open: the crash-report processor's name (**O4**; the console tasks stay draft until then), counsel's review of Terms §7.3 and the EU opt-out question (**O1**, **O2**), and **O13** (raised by the re-plan's review, 2026-11-21: no command writes the policy's crash-report switch, so the owner's `false` for T3.1's `mobile_crash_reports` is a hand edit until a door is chosen; it ships with T3.1's `MobileCrashReportsForPublish` fill and its drift row, which are what carry the key to an installation that has published since P6).

**Spec owner:** Dimi Paun. **Spec received:** 2026-10-30. **Reconciled against** `origin/main` at
`24fe1de60` and the 11 PRs open on 2026-10-30, re-checked at `8301c23c8` against the 12 then open.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec "Fugaro: Review Levels, A/B Testing, Job Max", as received, with one redaction (below) |
| [reconciliation.md](reconciliation.md) | Findings F1–F44 against the code, decisions D720–D737 (answered 2026-10-30), the D730 ruling (OT1, resolved), open items O1–O13, UNVERIFIED U1–U8. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices L, B, J, T, G, V and task X1; levels 0–6; the critical path; the shared-files table; Checks 270–272 written, 273–289 reserved. **Nothing in it is approved, and the 2026-11-21 re-plan of T3.1, V1.3 and X1 (with X2's close) is a document for the owner's review** |

## Changes to the spec as received

One redaction, no typo fixes. Section 6 named a real person as the inspiration for the live
dashboard. Repository docs name no real people, so `spec.md` replaces that name with "a
well-known cross-platform task manager author". This is the literal substitution the owner asked for, so the sentence now reads "inspired by a well-known cross-platform task manager author's cross-platform task manager", with the repetition kept as given. Everything else is byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D720–D759** | this work's block: **D720–D737** written and answered 2026-10-30; **D738** written 2026-11-21 by the re-plan (T3.1's switch's carrier — the app reads the pairing-delivered configuration, **a re-decision put to the owner against O9's ruling of 2026-10-30, which had amended D734 to put the switches in an owner-written `settings` node nobody now builds**); **D739–D759** reserved and unused. Taken above the highest number on `main` (D679, the top of forge-authorship-images' reserved block D600–D679; model-ab-testing holds D550–D579), above D680–D719, which the roles design run (`docs/roles/spec.md`, run 20261030-112626-0ae3) reserved first, and above every open PR's docs |
| **Checks 270–289** | this work's block: **270–272** written by X1 (re-worded 2026-11-21 for 271 and 272; run by the owner; NOT RUN); **273** closed 2026-11-21 with nothing measured and nothing written — its subject, `/jobmax`, was retired with J1.2 on 2026-11-16, and X2 disappears with it; **273–289** reserved and unused, and they stay reserved and unused, because a number once reserved is never reused or renumbered. Above forge-authorship-images' Checks 230–249, model-ab-testing's 210–219 and the roles design run's 250–259 |

**Also in this folder** (moved here on 2026-11-21 from the top of `docs/`, the task reports):

- [j1.1-already-landed.md](j1.1-already-landed.md)
- [j1.3-blocked.md](j1.3-blocked.md)
- [x1-blocked.md](x1-blocked.md)
