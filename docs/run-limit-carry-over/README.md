# Per-launch limits and a chain budget a restart cannot reset

**Status: design only; nothing is approved and no code follows from this folder.** The owner decides;
implementation is not approved. This document set answers the owner's 2026-10-31 brief: the orchestrator
must be able to pass **explicit new limits** when it launches a job, so the overall budget is never
exceeded, and a job that stopped on its limits (time, tokens or USD) and is restarted must **not** start
those limits from zero.

**Asked by:** the owner, 2026-10-31.
**Reconciled against** `origin/main` at `3d6ed04d` and the 15 pull requests open on 2026-11-01.

## What it is

Today a run's ceilings are fixed before the launch and are per **run**, not per **chain**. Three facts
make that a hole the orchestrator cannot close from outside:

- The per-run dollar cap is not a launch argument at all: it is the **job's environment**
  (`FUGARO_MAX_RUN_USD`, `internal/runner/spend.go:23`), written when the job was created by
  `fugaro setup` and re-converged by `fugaro update`, and read by the runner at bootstrap. A launcher cannot pass a cap.
- The one limit a launch *can* pass, `--total-timeout`, is a **timeout** and may raise the run's own clock
  (`internal/cli/run.go:2344`); nothing a launch passes can tighten the dollar or token ceiling.
- Every limit is measured from **zero at each run**. A follow-up (`--pr N`) or a retry starts with the
  whole cap again, so a chain of restarts multiplies the spend with nothing in the engine counting it.

This work adds one thing to the engine and one thing to the chain: a **tighten-only per-launch limit
surface**, and a **chain budget** whose remaining amount is `limit − what the chain already used`, read by
the engine from the run records and never from the caller.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The limits, the chain, the ledger, the halted-run writeback case, the CLI and JSON surface, the rigor and cloud-senior-review interactions, and the threat model |
| [reconciliation.md](reconciliation.md) | Each claim against the code on `main`: what exists, what is missing, what conflicts (findings **F1–F19**), and the owner decisions **D901–D920**. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks **L0–L8** (at most 12 files each) with named tests, red steps, SEC flags and gates, the shared-files table, the owner-run live check, and the release slot. **Nothing in it is approved** |

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D901–D920** | this work's block: **D901–D920 written and every one open** — the owner has answered none. Taken above every number on `main`, whose highest written is **D900** (the `rigor` design's own later decision, `docs/rigor-command/README.md`), and above every open pull request's numbers: the `cloud-senior-review` design (**#1472**, open) holds **D880–D899**, and no other open pull request names a decision above **D899**. So **D901 is the first free number**, re-checked on 2026-11-01 against `origin/main` at `3d6ed04d` and the open pull requests. Nothing beyond **D920** is reserved here |
| **Checks** | none taken. The one live check this design needs is **named and not numbered** ([implementation-plan.md](implementation-plan.md) §"Live checks"), because the register's highest written section is **Check 190** and **Check 125** is claimed by the open `cloud-senior-review` design (#1472): the task that writes the check takes the number free at **its** merge, under the register's rule that whichever of two open claims merges second renumbers itself (`docs/gcp-live-checklist.md`) |

## What this folder is not

- **Not code.** It is a design. No Go, no TypeScript, no Terraform, no rules change, no
  `.github/workflows` change, no live action.
- **Not approved.** Every decision below is a question with a recommendation, not a ruling. A task that
  finds a *new* decision reports it rather than picking an answer.
- **Not a loosening.** Nothing here raises a ceiling. Every per-launch limit tightens, the chain budget
  only ever lowers what a restart may spend, and the daily and project caps are untouched.
- **Not a second budget.** The gateway's per-run cap, the lease and the daily counters stay exactly where
  they are. This work adds a *chain* ledger the engine reads, and changes no rule the budget database
  enforces.
- **Not a licence to weaken a refusal.** Where this design refuses (a loosening value, an unreadable
  usage), it refuses; nothing here turns a refusal into a warning.
