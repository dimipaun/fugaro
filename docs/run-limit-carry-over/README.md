# Per-launch limits and a chain budget a restart cannot reset

**Status: design only; the owner has answered every decision (all as recommended) and no code follows from
this folder yet.** This document set answers the owner's 2026-10-31 brief: the orchestrator must be able to
pass **explicit new limits** when it launches a job, so the overall budget is never exceeded, and a job
that stopped on its limits (time, tokens or USD) and is restarted must **not** start those limits from
zero. The owner's answers of **2026-11-01** are recorded in
[reconciliation.md](reconciliation.md) §3 and carried through the spec: the **budget database's ledger** is
the source of a chain's money and token figures, the chain's **membership and limit live in the run records**
themselves — which the design says in as many words are a convention and not a bound the platform enforces —
a **chain limit exists only when it is set explicitly** at the root launch, and a **review job joins the PR's
chain**.

**Asked by:** the owner, 2026-10-31. **Answered by:** the owner, 2026-11-01.
**Reconciled against** `origin/main` at `385510e3` and the open pull requests of 2026-11-01.

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
surface**, and a **chain budget** whose remaining amount is `limit − what the chain already used`, with the
money and token figures read by the **launcher** from the **budget database's per-run ledger** — the numbers
the rules bind a run and a launcher to — and never from the caller. The chain's own membership and limit ride
the run records, and the design states what that does and does not bound (spec §4).

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The limits, the chain, the ledger, the halted-run writeback case, the CLI and JSON surface, the rigor and cloud-senior-review interactions, and the threat model |
| [reconciliation.md](reconciliation.md) | Each claim against the code on `main`: what exists, what is missing, what conflicts (findings **F1–F22**), and the owner decisions **D1041–D1061**, every one answered. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks **L0–L9** (at most 12 files each) with named tests, red steps, SEC flags and gates, the shared-files table, the owner-run live check, and the release slot |

**How a `:line` in this folder reads.** A citation names a file and a symbol, and the number is the
symbol's **declaration** line — for a claim about a symbol's documentation it is the first line of the
comment directly above it, and a range is the whole block the claim is about. Every number was read off
`origin/main` at `385510e3`; a line drifts the first time anyone edits the file above it, so the symbol, not
the number, is what a reader should search for. Every claim also states the fact in words, so a moved line
never carries the claim alone.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D1041–D1061** | this work's block: **21 decisions, every one answered by the owner on 2026-11-01, all as recommended.** The draft held **D901–D920**; those numbers are now taken by open pull requests (#1481 holds D901–D914, #1487 D920–D934, #1488 D940–D959, #1490 D960–D979, #1491/#1492 D980–D1010, #1496 D1011–D1040), so the block moves to the first free one, **D1041**, plus **D1061** for the raise of an exhausted chain, which the owner's second answer settles in the run record. On the tree this branch carries, the highest decision written outside this folder is **D900** (`docs/rigor-command/README.md`, whose own line says a later work takes D901). **That sentence is a reading of a moving tree, not a fact about `main`:** the open designs hold `D860–D879` (`docs/accounts-add`), `D880–D899` (`docs/cloud-senior-review`), `D901–D914` (#1481), `D920–D934` (#1487), `D940–D959` (#1488), `D960–D979` (#1490), `D980–D1010` (#1491/#1492) and `D1011–D1040` (#1496), so **whichever of them merges first moves it** — any of them can change what is written outside this folder and where the first free block starts. Every number here is the **launcher's 2026-11-01 reading**, so **a merger must re-check this line against the open pull requests and the merged tree and renumber again if it has moved** |
| **Checks** | none taken. The one live check this design needs is **named and not numbered** ([implementation-plan.md](implementation-plan.md) §"Live checks"), because the register's highest written section is **Check 190** and **Check 125** is claimed by the open `cloud-senior-review` design (#1472): the task that writes the check takes the number free at **its** merge, under the register's rule that whichever of two open claims merges second renumbers itself (`docs/gcp-live-checklist.md`) |

## At merge, and before implementation

**At merge**, three things, none optional, and a merger who checks only the first has not finished:

1. **Re-check the D-block.** `D1041–D1061` is the launcher's 2026-11-01 reading of the open pull requests;
   re-read them and the register against the tree being merged, and renumber to the first free block if it
   has moved (every file here that names a number).
2. **Take the live check's number** ([implementation-plan.md](implementation-plan.md) §"Live checks"): the
   task that writes it takes the number free at **its** merge.
3. **Re-read the `:line` citations** against the tree being merged, under the convention above.

**Before implementation, nothing is blocking.** The one item that was — **U4**, whether a chain's ledger read
may cross runs — is **settled** ([reconciliation.md](reconciliation.md) §5): `runs/$slug/$run` is readable on
`RUN` alone and `RUN` is the run's own identity, so a run's token cannot read a sibling's ledger; the read is
the **launcher's**, which already holds `roles/firebasedatabase.viewer`
(`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`), and the runner re-reads only its own ledger and the
chain's records (spec §3, §4; plan L1). What remains before code is the ordinary gate this folder has always
had: nothing here is approved to build.

## What this folder is not

- **Not code.** It is a design. No Go, no TypeScript, no Terraform, no rules change, no
  `.github/workflows` change, no live action.
- **Not a loosening.** Nothing here raises a ceiling a launcher or the agent can reach. Every per-launch
  limit tightens, the chain budget only ever lowers what a restart may spend, and an exhausted chain is raised
  by rewriting the root launch's stored task with the reason recorded — a path the design states plainly is a
  convention rather than a platform-enforced operator-only one (spec §4, D1061).
- **Not a second budget, and not a hard bound either.** The gateway's per-run cap, the lease, the ledger and
  the daily counters stay exactly where they are: this work **reads** the ledger the rules already keep and
  adds no node, no rule and no field to it. What it does *not* claim is enforcement: a chain's membership and
  limit are run records, so only the per-run and daily caps are hard bounds (spec §4, §9).
- **Not a licence to weaken a refusal.** Where this design refuses (a loosening value, a `--used-*` below what
  the engine read), it refuses; nothing here turns a refusal into a warning.
