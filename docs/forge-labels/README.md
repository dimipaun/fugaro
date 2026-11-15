# Forge labels

**Status: plan only; nothing is implemented; no task approved.** Owner: Dimi Paun. Date: 2026-11-15. Checked against `origin/main` at `f70f520cc`. The current state of every spec is on [docs/status.md](../status.md).

The spec: the stateless orchestrator puts `fugaro:running`, `fugaro:failed` and `spec:<name>` labels on the pull requests of the runs it launches, through an optional label capability on the forge interface, so the pull request list shows which spec a pull request belongs to and whether a job is working on it.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received, byte for byte (no typo was fixed). **Where [reconciliation.md](reconciliation.md) disagrees with it, the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | The spec against the code: assumptions A1-A5, findings F1-F16, resolutions R1-R7, decisions D2430-D2437 with the owner's answers, open items O1-O2, UNVERIFIED U1-U3 |
| [implementation-plan.md](implementation-plan.md) | Two slices, seven tasks T1-T7 and Check 640; levels, critical path, shared-files table. The contract-first pass ran and reported no gain, so it was undone. Nothing in it is approved |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads |

**Numbers taken:** D2430-D2449 (D2430-D2437 used), Checks 640-649 (640 used). The highest in use were D2429 and Check 632, both in open pull requests.

**Waits on something external:** U1 (the GitHub App's permission to write labels) is the owner's to read; Check 640 is owner-run.
