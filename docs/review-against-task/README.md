# Reviews check the pull request against its task

**Status: plan only; nothing is implemented; no task approved.** This folder is documents only: it adds no code, no schema and no workflow, and outside it, it adds one row to [docs/status.md](../status.md) (a spec's row moves when its plan is written) and one sentence to [bucket-iam](../bucket-iam/spec.md) §10 naming the residual the owner accepted (O3).

**Spec owner:** the owner, who agreed the spec in conversation on 2026-11-18 after finding that no review stage sees the task a pull request was written for. Every review stage is to check the change against its task by default; `--code-only` keeps today's diff-only review, and `--spec FILE` supplies a spec by hand.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec **verbatim**. No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1 to A5, findings F1 to F37, decisions D2930 to D2935 with the owner's dated answers (D2932 and D2933 amended after review), resolutions R1 to R16, open items O1 to O3 (all answered 2026-11-18), UNVERIFIED U1 to U3. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Three slices and 16 tasks with levels, the critical path, the shared-files table, the dependencies broken and kept, the estimate line and the owner-run Check 750. **Nothing in it is approved** |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads (schema 3) |

## Numbers taken

Decisions **D2930 to D2939** (D2930 to D2935 used), checks **750 to 759** (750 used). The highest decision on `main` at `0db80aa40` (this branch's base) was D2929 (the console-improvements block); run-messages, merged meanwhile, holds D2940–D2959 and Checks 740–744, so this folder takes D2930–D2939 and Checks 750–759. Findings, assumptions, resolutions and UNVERIFIED items are numbered from 1 inside this folder only. One capability, so no module map.

## What waits on something external

The owner's explicit go for every task (O1–O3 are answered: both amendments confirmed, the bucket residual accepted and documented in [bucket-iam](../bucket-iam/spec.md) §10); and Check 750, owner-run after the release that carries RA10 (not a gate).
