# `fugaro setup` and `fugaro update` — the documents

**Status: plan only; nothing is implemented; decisions open.** The owner's brief of 2026-10-20 asked for a design and a plan, not code: `fugaro init --name <project> --gcp-project <id> --create-project --link-billing <account> --firebase <id> --region <region>` is too complicated, two id flags for one project is one too many, the missing values should be asked with decent defaults, the billing accounts should be listed the way Firebase lists them, and `init` should not mean both "create" and "refresh". The owner answered the same day: **`fugaro setup`** is the interactive first-time wizard, **`fugaro update`** is the re-converge after a CLI upgrade, **`init` is removed** with no backward compatibility (only the owner's two installations exist, and they migrate by re-running), and **one project id** serves Google Cloud and Firebase by default, defaulting to `fugaro-<project name>`, with a separate Firebase project only as an advanced flag.

**D1–D12 in [reconciliation.md](reconciliation.md) §5 are open.** Each carries a recommendation and the alternative with its cost; **none is taken.** Answering a decision is not approving a task, and no code, Terraform, schema, skill or live action follows from these documents. The gate at the end of the implementation plan is **not passed**.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-20. **Base:** `main` at `82d366af`.

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's brief, **verbatim**, committed alone in its own first commit: the four sentences of 2026-10-20, then the answers the same day (the two commands; one project, one id). Nothing in it was changed. |
| [reconciliation.md](reconciliation.md) | What the tree says about the brief: corrections **C1–C3**, findings **F1–F24** against `main` (`82d366af`), each classified **Wrong / Exists / Gap / Rule** with a path and symbol and its consequence; the headline table (the wizard, the loop, the confirmations and the refusals already exist, so the work is shape rather than engine); **every place that names `fugaro init`**, by category and count; **decisions D1–D12**, each with a recommendation, the alternative and its cost; items O1–O5; the **UNVERIFIED** table. Where it disagrees with `spec.md`, it wins. |
| [implementation-plan.md](implementation-plan.md) | The plan: Slice 0 (the two pins and the checks' text; safe to start at once), Slice 1 (the two front doors, the tombstone and the wizard), Slice 2 (the rename), Slice 3 (the wizard's questions), Slice 4 (the billing picker), Slice 5 (`fugaro update`), Slice 6 (the owner-run checks), Slice 7 (later, unscheduled); each task with files, tests, a red step, a verification command, dependencies, reviewer tier and a commit message; the edges, the levels and the critical path. **No task is approved.** |

## The numbers this work holds

| Block | State |
|---|---|
| **D1–D12** | this work's decisions, all written and **all open** |
| **Check 27** | rewritten in place: same number, same **NOT RUN**; the command lines and flags are renamed and the wizard's sequence goes in front, while every probe the check already carries — the four Claude Code spikes, the mirror against the real registries, `doctor`/`update-skills`, the teammate's adopt run, `/fugaro:setup`, the hostile-clone gate and the prompt-injection probe — is **kept**, because no other check in the register names any of them |
| **Check 111** | new: the billing picker's five failure modes (API disabled, no permission, no TTY, `--json`, an agent session) |
| **110** | claimed by an open pull request; the register's rule is that whichever of two open claims merges second renumbers itself, so this plan takes **111** |

A reserved number is never reused and never renumbered. Check 27 keeps its
number because its subject moved rather than disappeared, and it stays **NOT
RUN** until the owner runs it.

## Related

- [docs/gcp-setup.md](../gcp-setup.md) — the guide that prints the old flags today (113 mentions) and is renamed with them.
- [docs/design/m11-setup-and-skills.md](../design/m11-setup-and-skills.md) and [docs/plans/2026-10-04-m11-setup-and-skills.md](../plans/2026-10-04-m11-setup-and-skills.md) — the converge loop this work builds on; its stage engines are reused unchanged.
- [docs/gcp-live-checklist.md](../gcp-live-checklist.md) — the register: Check 27, and Check 111 to add.
- [docs/credentials-service/](../credentials-service/) — the shape this folder follows, and the work that shares Checks 90–99.
- [SECURITY.md](../../SECURITY.md) — the claims the billing picker must not change: nothing is printed, stored or enabled silently.
