# Config ownership: one write path, semantic commands where a field has meaning

Spec owner: Dimi (repository owner). Spec received 2026-11-15.

**Status: plan only; nothing is implemented; no task approved. Phase 1 only: the reconciliation is a
draft with every decision open; the implementation plan is not written.**

This folder is not `docs/config-management/`, which is the 2026-11-03 package ("no configuration file
edited by hand", G0-G13). This spec is its successor in subject; `reconciliation.md` section 6 says what
of it and of `docs/composite-commands/` it must respect.

| File | Role |
| --- | --- |
| `spec.md` | The spec as received, byte for byte (no typos fixed) |
| `audit.md` | The field-by-field audit the spec rests on (542 lines), a copy of the owner's scratchpad file, taken at `main` 3e1b02570. Data, not authority: each claim cited in the reconciliation was re-checked at `eddaa041b`. Its companions (`registry.tsv`, `writestatus.tsv`, `gen.py`, the settings dump) are not committed |
| `reconciliation.md` | Draft: assumptions, findings F1-F18, decisions D2601-D2612 (answers empty), tensions, the composite-commands cross-reference, the capability map, UNVERIFIED |

Number blocks taken: decisions **D2600-D2639** (highest in use on `main` and in open pull requests'
documents: D2551). Findings are numbered within `reconciliation.md`. No checks reserved yet.
