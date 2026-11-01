# Per-launch limits and a chain budget a restart cannot reset

**Status: design only; the owner has answered every decision (all as recommended) and no code follows from
this folder yet.** This document set answers the owner's 2026-10-31 brief: the orchestrator must be able to
pass **explicit new limits** when it launches a job, so the overall budget is never exceeded, and a job
that stopped on its limits (time, tokens or USD) and is restarted must **not** start those limits from
zero. The owner's answers of **2026-11-01** are recorded in
[reconciliation.md](reconciliation.md) §3 and carried through the spec: the **budget database's ledger** is
the source of "used" and the run records are only the budget-off fallback, a **chain limit exists only when
it is set explicitly** at the root launch, and a **review job joins the PR's chain**.

**Asked by:** the owner, 2026-10-31. **Answered by:** the owner, 2026-11-01.
**Reconciled against** `origin/main` at `46e1b072` and the open pull requests of 2026-11-01.

## What it is

Today a run's ceilings are fixed before the launch and are per **run**, not per **chain**. Three facts
make that a hole the orchestrator cannot close from outside:

- The per-run dollar cap is not a launch argument at all: it is the **job's environment**
  (`FUGARO_MAX_RUN_USD`, `internal/runner/spend.go:23`), written when the job was created by
  `fugaro setup` and re-converged by `fugaro update`, and read by the runner at bootstrap. A launcher
  cannot pass a cap.
- The one limit a launch *can* pass, `--total-timeout`, is a **timeout** and may raise the run's own clock
  (`internal/cli/run.go:2350`); nothing a launch passes can tighten the dollar or token ceiling.
- Every limit is measured from **zero at each run**. A follow-up (`--pr N`) or a retry starts with the
  whole cap again, so a chain of restarts multiplies the spend with nothing in the engine counting it.

This work adds one thing to the engine and one thing to the chain: a **tighten-only per-launch limit
surface**, and a **chain budget** whose remaining amount is `limit − what the chain already used`, read by
the engine from the **budget database's per-run ledger** — the number the rules themselves enforce — and
never from the caller.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The limits, the chain, the ledger, the halted-run writeback case, the CLI and JSON surface, the rigor and cloud-senior-review interactions, and the threat model |
| [reconciliation.md](reconciliation.md) | Each claim against the code on `main`: what exists, what is missing, what conflicts (findings **F1–F22**), and the owner decisions **D1041–D1061**, every one answered. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks **L0–L9** (at most 12 files each) with named tests, red steps, SEC flags and gates, the shared-files table, the owner-run live check, and the release slot |

**How a `:line` in this folder reads.** A citation names a file and a symbol, and the number is the
symbol's **declaration** line — for a claim about a symbol's documentation it is the first line of the
comment directly above it, and a range is the whole block the claim is about. Every number was read off
`origin/main` at `46e1b072`; a line drifts the first time anyone edits the file above it, so the symbol, not
the number, is what a reader should search for. Every claim also states the fact in words, so a moved line
never carries the claim alone.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D1041–D1061** | this work's block: **21 decisions, every one answered by the owner on 2026-11-01, all as recommended.** The draft held **D901–D920**; those numbers are now taken by open pull requests (#1481 holds D901–D914, #1487 D920–D934, #1496 D1011–D1040), so the block moves to the first free one, **D1041**, plus **D1061** for the operator-only raise the owner's second answer asks for. On the tree this branch carries, the highest decision written outside this folder is **D900** (`docs/rigor-command/README.md`, whose own line says a later work takes D901). The open pull requests' numbers are the **launcher's 2026-11-01 reading**: this session has no GitHub credentials and cannot re-read them, so **a merger must re-check this line against the open pull requests and renumber again if it has moved** |
| **Checks** | none taken. The one live check this design needs is **named and not numbered** ([implementation-plan.md](implementation-plan.md) §"Live checks"), because the register's highest written section is **Check 190** and **Check 125** is claimed by the open `cloud-senior-review` design (#1472): the task that writes the check takes the number free at **its** merge, under the register's rule that whichever of two open claims merges second renumbers itself (`docs/gcp-live-checklist.md`) |

## At merge, and before implementation

**At merge**, three things, none optional, and a merger who checks only the first has not finished:

1. **Re-check the D-block.** `D1041–D1061` is the launcher's 2026-11-01 reading of the open pull requests,
   taken without GitHub credentials; re-read them and the register against the tree being merged, and
   renumber to the first free block if it has moved (every file here that names a number).
2. **Take the live check's number** ([implementation-plan.md](implementation-plan.md) §"Live checks"): the
   task that writes it takes the number free at **its** merge.
3. **Re-read the `:line` citations** against the tree being merged, under the convention above.

**Before implementation**, one open item, and it is blocking: **U4**
([reconciliation.md](reconciliation.md) §5). The rules' read on a run's ledger node is `RUN`
(`internal/budget/rules/rules.go:580`), the run's own identity, so a chain read that crosses runs needs
the platform's **admin read** rather than a run token — and whether a run token in fact reads a sibling is a
question this design states but cannot prove from a checkout. **L1's first step is to settle it**; until it
is settled, no code for the ledger read is written, because the answer decides who performs the read. The
figures, the tests and the rest of the design do not change either way.

## What this folder is not

- **Not code.** It is a design. No Go, no TypeScript, no Terraform, no rules change, no
  `.github/workflows` change, no live action.
- **Not a loosening.** Nothing here raises a ceiling a launcher or the agent can reach. Every per-launch
  limit tightens, the chain budget only ever lowers what a restart may spend, and the only way to raise an
  exhausted chain is the operator-only one of **D1061**.
- **Not a second budget.** The gateway's per-run cap, the lease, the ledger and the daily counters stay
  exactly where they are. This work **reads** the ledger the rules already keep; it adds no node, no rule
  and no field to it.
- **Not a licence to weaken a refusal.** Where this design refuses (a loosening value, an unreadable
  ledger), it refuses; nothing here turns a refusal into a warning.
