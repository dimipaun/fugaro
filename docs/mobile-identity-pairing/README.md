# The mobile app's identity, pairing and text interface

**Status: plan only; nothing is implemented; no task approved.** The four documents below are the owner's spec as received, what the repository actually holds and the owner's answers, and the plan that follows. **Owner: Dimi Paun. Date: 2026-11-18.** Read against `main` at `01fe41f30`; every citation is by path and symbol, never by line number.

The spec amends [../mobile-auth/spec.md](../mobile-auth/spec.md) and the mobile app spec in [../mobile/](../mobile/): a person-centric app with its own identity layer, workspace-bound assertions instead of a bearer QR, multi-device continuity, job controls, a text interface, a context gauge and a usage meter. [reconciliation.md](reconciliation.md) works through what the repository holds against it (no identity project exists, the "security service" does not exist, workspaces here have no users, Firebase refresh tokens are not what the spec describes) and records the owner's ten answers (D2782, credsvc, was **amended after review**: its endpoints are served by a second public Cloud Run service, which the owner confirmed and reconfirmed, O8); [implementation-plan.md](implementation-plan.md) turns them into 45 tasks and two anticipated contract changes in seven modules.

## The documents

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim. Not edited; no typo was fixed. |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions, findings F1-F41, decisions D2780-D2799 with answers, open items, UNVERIFIED. Where it disagrees with `spec.md`, it wins. |
| [implementation-plan.md](implementation-plan.md) | Slices, tasks, levels, the critical path, the dependencies broken by contract and kept, the checks. Nothing in it is approved. |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads. |
| [app-spike-howto.md](app-spike-howto.md) | The owner's runbook for task M0's throwaway spike ([`mobile/spike/`](../../mobile/spike/)): the consoles, the EAS environment, the build, and how the readings get into `app-spike-DRAFT.md`. Nothing here is run by the repository. |

## Numbers taken

Decisions **D2780-D2799** (D2780-D2789 asked, D2790-D2799 resolved); D2800-D2829 are not taken, because #3195 (merged) uses D2800-D2826 and D2829; checks **730-739** (730-737 used). Findings are numbered `F1`.. per folder, as in the neighbouring folders.

## What waits on something outside this folder

The owner's own go on each task; every open item O1-O18 was answered 2026-11-18 (see [reconciliation.md](reconciliation.md)), two against the recommendation: O6, a per-message device signature (tasks J5b and J5c), and O7, pinned keys plus a root-signed key set (tasks A4 and P8); #3195 merged during the reviews and the re-check O5 asked for is O14 (the roster is the one source of membership and role); Checks 730-737, all owner-run; the P5.1 consumer for start and cancel to work end to end.
