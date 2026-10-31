# The Coordinator tab as an action queue, clickable pull requests, an on-demand job list, and a terminal

**Status: design approved 2026-10-31, every decision answered as recommended; the CLI tasks C0-C4 queue for 0.15.0 and the console tasks K1-K5 for 0.117.0; the INPUT BOX of the terminal waits for the coordinator front door (P7/P8) and its own independent security review; the read-only transcript pane ships first (D827).** The decisions are in
[reconciliation.md](reconciliation.md); nothing is built yet. The page this replaces is the Coordinator screen as it
stands on `main` (Bitbucket credentials T1.2, D364): one read, `fugaro coordinator status
--from-store --all --json`, and one panel per repository, drawn from the view the coordinator's
sweep last stored.

**The owner's feedback, 2026-10-31:** *"The page is useless as-is; would be nice to get a terminal
to talk to the agent; PR numbers should be clickable so I can open them or even better see their
description inline; the job list should be expandable, pageable, loadable on-demand."*

**The owner's rulings, 2026-10-31:**

1. **The page answers first "what needs me now".** A short **action queue** on top — pull requests
   waiting for review or merge, halted or blocked jobs, plan tasks ready to launch, budget alerts —
   each item with **one** button (open PR, retry, launch, …). The page reads; it performs **no new
   write** unless the write reuses the existing launch path (V2.5.1, the console's only approved
   write). Below the queue: fleet totals, per-plan progress, and the job list, loaded on demand.
   The page follows **the progressive-rendering rule** (each section renders when its own data
   arrives; independent reads run in parallel; nothing waits for all of them) and **the jobs rule**
   (jobs are never in the first paint; every job read is bounded by a `--since`; the read is
   asynchronous; what is shown is cached and stamped "as of HH:MM"). Both rules are being written
   into `.claude/rules/console.md` and `docs/design/desktop-console.md` by a concurrent console run;
   this design cites them by those names.
2. **The terminal is design first, no build.** [reconciliation.md](reconciliation.md) §5 is the
   design: what "the agent" is, how a typed line would reach it, who may use it, what it may do, the
   audit log and rate limits, the security model and the read-only alternative.
   [spec.md](spec.md) §2.8 is what the page shows of it. **An independent review of the whole
   terminal is mandatory before any of it is built** — it would be a second write path after the
   launch. The terminal's decisions are **D820–D828**, at the end of
   [reconciliation.md](reconciliation.md).
3. **Pull request numbers clickable.** The URL is **built** from the repository identity and the
   number by a strict builder with a host allow-list (`github.com`, `bitbucket.org`), and opened
   through the console's one external-link path (`openExternalValidated`,
   `console/src/main/window.ts`) — never from a URL field in JSON. The inline description says
   where its text comes from and is rendered as **text**.
4. **The job list: expandable, pageable, on demand.** `runs ls` has no `--limit` and no cursor
   today (`newLsCmd`, `internal/cli/runs.go`), and `coordinator status --from-store --all` returns
   about **724 KB** in one document. [spec.md](spec.md) §6 names the CLI-side flags the console
   would spawn, word for word.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The page as the owner asked for it: an ASCII mock of the whole page, and for each section the exact read behind it, its approximate size and its measured time. **Where it disagrees with [reconciliation.md](reconciliation.md), the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | Findings F1–F18 against the code on `main`, the decisions D810–D819 for the page and D820–D828 for the terminal (each with a recommendation and the cost of the alternative), and the open items O1–O3 |
| [implementation-plan.md](implementation-plan.md) | Tasks for the CLI (0.15.0) and the console (0.117.0), each with its interfaces, tests, command, dependencies and a red step first. The terminal's tasks exist only as "after the owner answers D820–D828" |

## How this was reconciled

Against `origin/main` at `6edb2b24` and the 18 pull requests open on 2026-10-31, read with `grep`,
offset reads and `git show`. Every claim in [reconciliation.md](reconciliation.md) names the file
and the symbol it was read from.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D810–D829** | this work's block: **D810–D819** for the page, **D820–D828** for the terminal, **D829** reserved and unused. Verified free as follows: on `main` the highest written number is **D795** and **D796–D799** are the reserved tail of the models-commands block (**D780–D799**); the highest number in any open pull request's docs is D772 (the console auto-update plan, #1377). The token `D800` appears exactly once in `docs/` — in `docs/models-commands/README.md`, whose prose records that open pull request #715 "names D800" — and that is a **false positive**: #715's diff carries `U+D800`, the lone UTF-16 surrogate its WTF-8 tests are about, not a decision. **D800–D809 are therefore left as a gap rather than taken under a number a document on `main` already asserts is another work's** |
| **Checks** | **none taken.** Nothing in this design is an operator-run check: every task in [implementation-plan.md](implementation-plan.md) is verified by a test command, and the one verification that would need a check number — the terminal's independent security review — cannot be written before the owner answers D820–D828. The block above the register's own top (Check 273; 274–289 reserved by review-levels-ab-jobmax and 290–299 declared unused by the console auto-update plan) stays free |

## What this design does not do

It changes no code, no schema, no allow-list row and no existing document. It proposes no new
dependency. It builds nothing. If the owner rejects the action queue, the clickable links or the
job-list flags, the files here are deleted rather than amended: they are a proposal, and
[implementation-plan.md](implementation-plan.md) is written so that each task can be dropped on its
own.
