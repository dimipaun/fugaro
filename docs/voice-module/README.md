# The voice module

*Status: **reconciliation only; nothing is implemented.** · Owner: Dimi Paun · 2026-10-19.*

*Questions: **all thirteen answered by the owner on 2026-10-19** — Q1 as the local/remote evaluation switch, both directions, under the seven points, and Q2–Q13 each taking the option [reconciliation.md](reconciliation.md) §5 recommended, with the answers recorded in its §5a — and **an answer is not approval of a plan or of any code**: nothing is built on them.*

The owner's voice module spec of 2026-10-18, committed verbatim, and its reconciliation against the code this repository already has, the voice work already decided, and the pull requests in flight.

**No code, no Terraform, no workflow and no plan lives here.** The spec's questions were answered by the owner on 2026-10-19, and an answer is not approval: what would follow one is a separate document, and this folder carries none of it.

## Contents

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, **verbatim** — the owner's text copied unchanged, and nothing else. Committed alone in its own commit (`eea279f1`). |
| [reconciliation.md](reconciliation.md) | What the spec asks for, read against `origin/main` and the open pull requests. Findings, resolutions, and the owner's questions with the answers of 2026-10-19. |

## Status of the spec

The spec was written **without seeing this repository**, and it overlaps heavily with work that already exists or is in flight. It is not a new workstream: it is a **revision of [voice-spec.md](../design/voice-spec.md)** — the owner's own voice spec of 2026-10-15 — which this repository committed verbatim the same way and reconciled in [voice.md](../design/voice.md) as **F1–F38** and **decisions D39–D54b, all answered by the owner on 2026-10-16**.

Two newer documents decide most of what the new spec reopens, and both are cited rather than repeated here:

- **[docs/mobile/](../mobile/)** — the phone app: decisions **D110–D129**, the functional spec v0.1 reconciled as **G1–G22 / T1–T6 / O1–O7 / D160–D173**, and a plan whose Phases 0 and 1 are merged on `main` as the pure-logic packages in `shared/`.
- **[docs/credentials-service/](../credentials-service/)** — the "per-project security service" the spec's *Keys and security* section names: **D130–D154**, answered 2026-10-17, and **plan only; nothing implemented**.

And one in-flight design is the same subject seen from the module's side: **[docs/mobile/design/voice-evaluation-switch.md](../mobile/design/voice-evaluation-switch.md)** (P5.3, ⊂ PR #633), which carries the owner's **seven points** for the local/remote switch approved on 2026-10-19 — default local, a persistent non-dismissible indicator, a named provider allow-list with terms recorded, no remote audio during a confirmation or on a destructive path, no credential in the app binary, fail-closed to local, and a bake-off through both engines. **The file was `cloud-stt-tts.md` in its first revision**; the revision of 2026-10-19 renamed it and added its **§1a**, which amends the mobile plan's Global Constraint 12 *for the evaluation switch only*, subject to the owner's acknowledgement — that document's own **Q0**, which gates the build.

## How this work was checked

| | |
|---|---|
| **Baseline** | `origin/main` at `82ba600d984a420c595cf06417af6d781c18535a` — "console: frame the golden screenshots on the screen, not the window (#619)", 2026-10-18. One cited path is **not** there: `console/scripts/notarize-and-staple.sh` was added by #626 after this commit, and finding #17 says so |
| **Date of the check** | 2026-10-19 |
| **Branches read** | PR #584, #590, #592, #602, #604, #633, #634 — fetched read-only, never modified |
| **Rules** | every finding cites a path and a symbol, never a bare line number; provider claims are checked against the providers' own public documentation and anything unverifiable is marked **UNVERIFIED** and never asserted |

The checks this module touches are allocated in the [live check register](../gcp-live-checklist.md): **64, 65 and 66** to the voice and console lines, and **85 and 86** to the mobile work's 80–89 block. The phone's Phase 6 **proposes 102** (the wake-word recognition rate) and **103** (a spoken cancel and merge on the sandbox repository, proposed in [functional-spec-reconciliation.md](../mobile/functional-spec-reconciliation.md)'s U4), and ⊂ PR #633 names **110** as the next free number for the remote half. **None of them is written, run or claimed here** — the full reading is finding #44.

## Typographical corrections

**None.** The spec as received contains no typographical error that needed fixing, so [spec.md](spec.md) is byte-for-byte the owner's.

The delimiter lines that framed the text in the request that carried it are that request's quoting convention, not part of the document, and are not reproduced in [spec.md](spec.md) — the file is the spec itself, with no wrapper. (Same for the earlier [voice-spec.md](../design/voice-spec.md), which was committed verbatim the same way.)

## Read in this order

1. [spec.md](spec.md) — the owner's text, unchanged.
2. [reconciliation.md](reconciliation.md) §1 — how this spec and the evaluation switch are one subject.
3. [reconciliation.md](reconciliation.md) §2 — the table: what the spec asks for, and what the tree already has.
4. [reconciliation.md](reconciliation.md) §5 — the owner's questions, Q1–Q13, each with its options, its recommendation, and the answer of 2026-10-19 marked in place.
5. [reconciliation.md](reconciliation.md) §5a — **Owner's answers**: Q1 in full (the evaluation switch and its seven points), the table of the Q2–Q13 answers, what Q1's answer changes in the spec's sections, and what it does not do.
6. [reconciliation.md](reconciliation.md) §7 — what is **UNVERIFIED**, so nothing here is mistaken for a checked fact.
