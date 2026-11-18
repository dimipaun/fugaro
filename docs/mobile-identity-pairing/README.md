# The mobile app's identity, pairing and text interface

**Status: plan only; nothing is implemented; no task approved.** The four documents below are the owner's spec as received, what the repository actually holds and the owner's answers, and the plan that follows. **Owner: Dimi Paun. Date: 2026-11-18.** Read against `main` at `01fe41f30`; every citation is by path and symbol, never by line number.

The spec amends [../mobile-auth/spec.md](../mobile-auth/spec.md) and the mobile app spec in [../mobile/](../mobile/): a person-centric app with its own identity layer, workspace-bound assertions instead of a bearer QR, multi-device continuity, job controls, a text interface, a context gauge and a usage meter. [reconciliation.md](reconciliation.md) works through what the repository holds against it (no identity project exists, the "security service" does not exist, workspaces here have no users, Firebase refresh tokens are not what the spec describes) and records the owner's ten answers; [implementation-plan.md](implementation-plan.md) turns them into 38 tasks in seven modules.

## The documents

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim. Not edited; no typo was fixed. |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions, findings F1-F30, decisions D2780-D2798 with answers, open items, UNVERIFIED. Where it disagrees with `spec.md`, it wins. |
| [implementation-plan.md](implementation-plan.md) | Slices, tasks, levels, the critical path, the dependencies broken by contract and kept, the checks. Nothing in it is approved. |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads. |

## Numbers taken

Decisions **D2780-D2829** (D2780-D2789 asked, D2790-D2798 resolved, D2799-D2829 reserved); checks **730-739** (730-736 used). Findings are numbered `F1`.. per folder, as in the neighbouring folders.

## What waits on something outside this folder

The owner's own go on each task; **O1**, the credsvc grant, before P5; Checks 730-736, all owner-run; the P5.1 consumer for start and cancel to work end to end.
