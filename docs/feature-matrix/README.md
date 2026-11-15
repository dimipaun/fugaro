# Feature matrix

**Status: plan only; nothing is implemented; no task approved.** See [docs/status.md](../status.md)
for the current table once the merger's sweep adds this spec's row. Spec owner: Dimi Paun. Date:
2026-11-15 (checked against `origin/main` at `3e1b02570`).

A public, user-facing list of what Fugaro does, derived from `docs/status.md`. It is written to a
root `FEATURES.md` and shown at fugaro.dev/features/, refreshed by every CLI release.

| File | What it holds |
| --- | --- |
| [spec.md](spec.md) | The spec as received, byte for byte (no typo was fixed) |
| [reconciliation.md](reconciliation.md) | Assumptions A1–A5, findings F1–F14, decisions D2440–D2447 (answered 2026-11-15, all as recommended except D2440: three plan columns), resolutions R1–R5, open items O1–O2, UNVERIFIED U1–U2 |
| [implementation-plan.md](implementation-plan.md) | C1, T1–T6, I1 and the anticipated CR1: the contract, the builder, the mapping, the markdown, the page, the wiring, the release step and the owner's read |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, read by `fugaro plan graph` |

Numbers: decisions **D2440–D2459** taken (D2440–D2447 used). No check number is taken. The spec's
three open questions are answered: the plans are Community, Team and Enterprise, shown as three
columns (D2440); the mapping is a hand-kept file (D2441); and the page is `/features/` in the
Product menu (D2442).
