# Config ownership: one write path, semantic commands where a field has meaning

Spec owner: Dimi (repository owner). Spec received 2026-11-15.

**Status: plan only; nothing is implemented yet; the owner approved all tasks on 2026-11-15 (#2811 merged).** The approval itself did not answer D2611, D2613, D2614, D2616, T3 or T5; the owner answered them afterwards on 2026-11-15, all as recommended, so every decision D2601-D2617 and the tensions T3 and T5 are settled. A task still waits only on an open pull request (#2782, #2753, #2732, #2498, #2576, #2538), a composite decision (AP5, AP6) or an earlier task; each task names which. D2616 added task S1, the `secrets` writers.

This folder is not `docs/config-management/`, which is the 2026-11-03 package ("no configuration file
edited by hand", G0-G13). This spec is its successor in subject; `reconciliation.md` section 6 says what of
it and of `docs/composite-commands/` it must respect.

| File | Role |
| --- | --- |
| `spec.md` | The spec as received, byte for byte (no typos fixed) |
| `audit.md` | The field-by-field audit the spec rests on (542 lines), a copy of the owner's scratchpad file, taken at `main` 3e1b02570. Data, not authority: each claim cited in the reconciliation was re-checked at `eddaa041b`. Its companions (`registry.tsv`, `writestatus.tsv`, `gen.py`, the settings dump) are not committed |
| `reconciliation.md` | Assumptions, findings F1-F24, decisions D2601-D2617 with the owner's answers, tensions, the composite-commands cross-reference, the capability map (seven modules), open items, UNVERIFIED |
| `implementation-plan.md` | Twenty-two tasks in five slices and five levels, the contract-first pass, the shared-files table, sequencing against open pull requests and composite-commands, the coverage table. The owner approved every task on 2026-11-15; tasks waiting on a pull request, a composite decision or an earlier task stay held |
| `implementation-plan.graph.json` | The plan's tasks as `fugaro plan graph` reads them |

Number blocks taken: decisions **D2600-D2639** (highest in use on `main` and in open pull requests'
documents when taken: D2551). Findings are numbered within `reconciliation.md`. No checks reserved.
