# Config ownership: one write path, semantic commands where a field has meaning

Spec owner: Dimi (repository owner). Spec received 2026-11-15.

**Status: plan only; nothing is implemented; no task approved.** Eight decisions are answered
(D2601-D2607, D2609, D2610, 2026-11-15); D2608, D2611, D2612, D2613 and D2614 are open with a
recommendation, and tensions T3 and T5 are open. Tasks that wait on one say so.

This folder is not `docs/config-management/`, which is the 2026-11-03 package ("no configuration file
edited by hand", G0-G13). This spec is its successor in subject; `reconciliation.md` section 6 says what of
it and of `docs/composite-commands/` it must respect.

| File | Role |
| --- | --- |
| `spec.md` | The spec as received, byte for byte (no typos fixed) |
| `audit.md` | The field-by-field audit the spec rests on (542 lines), a copy of the owner's scratchpad file, taken at `main` 3e1b02570. Data, not authority: each claim cited in the reconciliation was re-checked at `eddaa041b`. Its companions (`registry.tsv`, `writestatus.tsv`, `gen.py`, the settings dump) are not committed |
| `reconciliation.md` | Assumptions, findings F1-F18, decisions D2601-D2614 with the owner's answers, tensions, the composite-commands cross-reference, the capability map (seven modules), open items, UNVERIFIED |
| `implementation-plan.md` | Twenty tasks in five slices and five levels, the contract-first pass, the shared-files table, sequencing against open pull requests and composite-commands, the coverage table. Nothing in it is approved |
| `implementation-plan.graph.json` | The plan's tasks as `fugaro plan graph` reads them |

Number blocks taken: decisions **D2600-D2639** (highest in use on `main` and in open pull requests'
documents when taken: D2551). Findings are numbered within `reconciliation.md`. No checks reserved.
