# CLI output cleanup and the console dependency graph: the documents

**Status: plan only; nothing is implemented; no task approved.**

**Spec owner: Dimi Paun. Date: 2026-11-06.** Checked against `origin/main` at `57f7d871f`.

The owner's spec, "Fugaro: CLI Output Cleanup and Console Dependency Graph", is two independent changes, reconciled against the repository and planned as two work streams (D1350).

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, **verbatim**, committed alone in its own first commit. |
| [reconciliation.md](reconciliation.md) | Findings **F1-F30** (Wrong / Exists / Gap / Rule / Open), the headline table, decisions **D1350-D1362** with the owner's answers of 2026-11-06, the tensions **T1-T6** (T4, T5 open), the smallest slices, the checks, UNVERIFIED. **Where it disagrees with `spec.md`, it wins.** |
| [implementation-plan-output.md](implementation-plan-output.md) | Part A: the reporter, the log sink, the flag, the converge family and the other commands. Tasks **CL1-CL12**, levels L0-L5, critical path, shared-files table. Nothing in it is approved. |
| [implementation-plan-graph.md](implementation-plan-graph.md) | Part B: `fugaro graph`, the snapshot stream, the console's scope dropdown; the orchestrator slice and the endpoint written but deferred. Tasks **CG1-CG13**, levels L0-L5, critical path, shared-files table. Nothing in it is approved. |

## Typos fixed in `spec.md`

None. The file is byte-identical to the text received (`cmp` against the Downloads file).

## Numbers this work holds

- Decisions **D1350-D1369**: D1350-D1362 written and answered, **D1363-D1369 reserved and unused**.
- Checks **310-314**: 310 and 311 are part A's, 312 and 313 part B's, 314 is the viewer-role read check (T2, owner-run, only when the orchestrator slice is scheduled); none is written into `docs/gcp-live-checklist.md`.
- Findings **F1-F30**; tasks **CL1-CL12** and **CG1-CG13** (this folder's own prefixes; `O0-O19` and `CSR` belong to other plans).
- Read above everything on `origin/main` and in the open pull requests: open #1772 (`docs/image-env`) takes D1310-D1329, which overlaps `docs/stateless-orchestrator`'s D1320-D1349. That overlap is not this work's to fix; it is why this block starts above D1349.

## Answers that were not the recommendation

**D1355:** `termenv` becomes a direct dependency (the owner approved it, so `CLAUDE.md`'s ask-first rule is satisfied). All the other answers took the recommendation.

## Waiting on the owner

Tensions **T1 to T6** (reconciliation section 3a) were all ruled on 2026-11-06, each the recommendation. Each task still needs the owner's explicit go. The graph plan's orchestrator slice waits on `docs/stateless-orchestrator/` (six of whose tasks are written, all merged on `main`, O0, O1, O3 and O4 on 2026-11-07 and O2 and O6 on 2026-11-08; nothing else there is), and its endpoint slice is conditional. When both plans run at once, `docs/design/v1.md` has two writers (CL11 and CG3): sequence them.

## Cross-plan shared files, in the order they are written

D1350 is "two streams, two plans, **no shared task**". The plans do share a few **files**, and these are sequenced here; when both streams run at once, an earlier writer merges before a later one starts.

| File | Writers, in order |
| --- | --- |
| `website/content/cli-reference.md` | CL4 (the persistent flag, regenerated for every command), then CG3s (the stub's entry), then CG3 (the `graph` command through `ee/clidoc`) |
| `internal/cli/docs_website_cliref_test.go` | CL4, then CG3s |
| `docs/design/v1.md` | CG3 (the command table row), then CL11 (the output contract) |
| `internal/cli/root.go`, `internal/cli/extend.go` | CL4 only; CG3s only |
| `docs/gcp-live-checklist.md` | the task that implements the first slice needing a check, per plan, one PR at a time |
