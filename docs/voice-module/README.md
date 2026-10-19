# The voice module

*Status: **reconciliation only; nothing is implemented.** · Owner: Dimi Paun · 2026-10-19.*

The owner's voice module spec of 2026-10-18, committed verbatim, and its reconciliation against the code this repository already has, the voice work already decided, and the pull requests in flight.

**No code, no Terraform, no workflow and no plan lives here.** The spec's open questions are answered by the owner first; that answer is the gate, and what follows it is a separate document.

## Contents

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, **verbatim** — the text between the `=== SPEC TEXT ===` markers, copied unchanged. Committed alone in its own commit (`eea279f1`). |
| [reconciliation.md](reconciliation.md) | What the spec asks for, read against `origin/main` and the open pull requests. Findings, resolutions, and the questions that need the owner. |

## Status of the spec

The spec was written **without seeing this repository**, and it overlaps heavily with work that already exists or is in flight. It is not a new workstream: it is a **revision of [voice-spec.md](../design/voice-spec.md)** — the owner's own voice spec of 2026-10-15 — which this repository committed verbatim the same way and reconciled in [voice.md](../design/voice.md) as **F1–F38** and **decisions D39–D54b, all answered by the owner on 2026-10-16**.

Two newer documents decide most of what the new spec reopens, and both are cited rather than repeated here:

- **[docs/mobile/](../mobile/)** — the phone app: decisions **D110–D129**, the functional spec v0.1 reconciled as **G1–G22 / T1–T6 / O1–O7 / D160–D173**, and a plan whose Phases 0 and 1 are merged on `main` as the pure-logic packages in `shared/`.
- **[docs/credentials-service/](../credentials-service/)** — the "per-project security service" the spec's *Keys and security* section names: **D130–D154**, answered 2026-10-17, and **plan only; nothing implemented**.

And one in-flight design is the same decision seen from the module's side: **[docs/mobile/design/cloud-stt-tts.md](../mobile/design/cloud-stt-tts.md)** (P5.3, ⊂ PR #633), which carries the owner's **seven points** for the local/remote switch approved on 2026-10-19 — default local, a non-dismissible indicator, a named provider allow-list with terms recorded, no remote audio during a confirmation or on a destructive path, no credential in the app binary, fail-closed to local, and a measurement before either side is chosen.

## How this work was checked

| | |
|---|---|
| **Baseline** | `origin/main` at `82ba600d984a420c595cf06417af6d781c18535a` — "console: frame the golden screenshots on the screen, not the window (#619)", 2026-10-18 |
| **Date of the check** | 2026-10-19 |
| **Branches read** | PR #584, #590, #592, #602, #604, #633, #634 — fetched read-only, never modified |
| **Rules** | every finding cites a path and a symbol, never a bare line number; provider claims are checked against the providers' own public documentation and anything unverifiable is marked **UNVERIFIED** and never asserted |

The checks this module touches are registered in the [live check register](../gcp-live-checklist.md) — 64, 65, 66, 85, 86 and the proposed 102 and 110 — and **none of them is run or claimed here**.

## Typographical corrections

**None.** The spec as received contains no typographical error that needed fixing, so the text between the markers in [spec.md](spec.md) is byte-for-byte the owner's.

## Read in this order

1. [spec.md](spec.md) — the owner's text, unchanged.
2. [reconciliation.md](reconciliation.md) §1 — how this spec and P5.3's seven points are one decision.
3. [reconciliation.md](reconciliation.md) §2 — the table: what the spec asks for, and what the tree already has.
4. [reconciliation.md](reconciliation.md) §5 — **Open for the owner**: Q1–Q13, each with the options, a recommendation and what it changes. Q1 is the gate.
5. [reconciliation.md](reconciliation.md) §7 — what is **UNVERIFIED**, so nothing here is mistaken for a checked fact.
