# Reviews check the pull request against its task

**Status: plan only; nothing is implemented; no task approved.** This folder is documents only: it adds no code, no schema and no workflow, and the one file it edits outside it is the row it adds to [docs/status.md](../status.md), which follows the rule that a spec's row moves when its plan is written.

**Spec owner:** the owner, who agreed the spec in conversation on 2026-11-18 after finding that no review stage sees the task a pull request was written for. Every review stage is to check the change against its task by default; `--code-only` keeps today's diff-only review, and `--spec FILE` supplies a spec by hand.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec **verbatim**. No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1 to A5, findings F1 to F27, decisions D2930 to D2934 with the owner's dated answers (D2932 and D2933 amended after review), resolutions R1 to R14, open items O1 and O2, UNVERIFIED U1 to U3. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Three slices and 14 tasks with levels, the critical path, the shared-files table, the dependencies broken and kept, the estimate line and the owner-run Check 740. **Nothing in it is approved** |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads (schema 3) |

## Numbers taken

Decisions **D2930 to D2949** (D2930 to D2934 used), checks **740 to 749** (740 used). The highest decision on `main` at `2d6fb0dca` was D2929 (the console-improvements block) and no open pull request's documents reserve a higher one; the highest check named was 737. Findings, assumptions, resolutions and UNVERIFIED items are numbered from 1 inside this folder only. One capability, so no module map.

## What waits on something external

The owner's explicit go for every task; the owner's confirmation of D2932 and D2933 as amended after review (O1, O2); and Check 740, owner-run after the release that carries RA10 (not a gate).
