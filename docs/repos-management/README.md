# Repository management: full commands for a repository's enrolment in a project

**Status: a design. Nothing is approved and nothing is built.** The owner's order of 2026-11-14 — *"we need full management commands for repos: add, remove, ls, status (sort of a doctor)"* — is recorded verbatim in [spec.md](spec.md) §0, and every open question it leaves is a numbered decision in [reconciliation.md](reconciliation.md) §4, **D2400–D2417, each with a recommendation and the cost of each alternative, every one still unanswered.** The plan is [implementation-plan.md](implementation-plan.md): seven tasks **RE0–RE6**, one of which **adopts the draft another run left** (draft pull request #2690, head `2bcda85d`), one of which is **owner-run**, and none of which is approved.

**The design was written as four documents — no code, no Terraform, no schema, no `go.mod`.** [reconciliation.md](reconciliation.md) is the one that decides what is true; where it and [spec.md](spec.md) disagree, the reconciliation wins.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The order as received: the six hand steps that enrol a repository today, the `github-app-key` container created empty that nothing named until a launch refused, what each of the four verbs must do, what the work must not do, and the six questions asked of the owner. **Where it disagrees with [reconciliation.md](reconciliation.md), the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | The order against the code at `origin/main` = `5c170e5c` (2026-11-14): assumptions **A1–A8**, the "spec concept, what the repository already has" table (§2), findings **F1–F25** classed Wrong, Exists, Gap, Rule and Open (§3), the decisions **D2400–D2417** with their recommendations and the cost of every alternative (§4), the number block and how it was read (§5), the residual (§6) and **U1–U5** (§7) |
| [implementation-plan.md](implementation-plan.md) | Seven tasks **RE0–RE6**, each with its files, its interface, its **red step**, its named tests, its one command and its reviewer tier; the levels and what forces them (§2); the gates (§3); the shared-files table (§4); the checks the plan takes (§5); and what the plan does not own (§6) |
| `README.md` | This file: the status, what is being asked, the reading of the number block, and what a reader must not take from here |

## What is being asked

`fugaro project repos` has four subcommands today. `ls`, `add` and `rm` edit **one** of the six things that enrol a repository in a project — the published allow-list — and the other five are five other commands in an order that matters: the repository's own `fugaro.yaml`; the `repos:` entry in this machine's project config; the image map entry through `fugaro image map set`; `fugaro update`, which publishes the layer the allow-list lives in; and `fugaro update --repo <checkout>`, which converges the repository's own root — its job, its check job, its Scheduler job, and its job account's `run.invoker` grant on the credentials service. The owner got the order wrong several times on the GitHub sandbox `dimipaun/fugarosandbox`, and a `github-app-key` secret **container** created with no version in it made the whole enrolment look complete while planning **no job at all** — the readiness gate refuses to plan a job that mounts an unresolved secret, and nothing said so until a launch was refused.

The order asks for four verbs that cover that subject: **`add`** as the whole converge behind one confirmation, **`rm`** as its inverse with an explicit list of what it deliberately leaves, **`ls`** as one row per repository with a versioned `--json` document the console can read, and **`status`** as a doctor for a repository that exits nonzero when something blocks a launch. The reconciliation's own reading is that **almost every piece already exists** — the group, the gate, the ETag-guarded allow-list writer, the in-flight-run read, the local-config writer, most of `status`'s rows as existing `doctor` checks, and the whole repository root and retire flow as `fugaro update --repo`. **What is missing is the reach**, and the honest part of the design is saying which built piece each verb reuses and which it must not fork.

**What this design is not.** It is not approved, not built, and not a licence to run anything: three of its four writers touch the published policy, this machine's config and Terraform, and the one live check is the owner's own, on a throwaway installation.

## The number block

Read **on 2026-11-14** at `origin/main` = **`5c170e5c`**, rather than inherited from a neighbour's line ([reconciliation.md](reconciliation.md) §5 carries the commands that re-check it):

- **On `main`**, the highest written decision is **D2399** (`docs/review-job`, whose block its own README states as D2380–D2399), and that design's check block is **610–619**, of which **610 and 611** are used — `docs/status.md` names *"owner Checks 610 and 611"* as that work's.
- **In the open work streams**, every branch that writes a decision block was fetched and read: **none writes a decision above D2399 and none reserves a check in 620–629**. The draft this plan adopts claims nothing here — a task branch writes no decision block.
- **This work takes D2400–D2429** — **D2400–D2417 written here, every one pending the owner's answer, D2418–D2429 reserved** — and **Checks 620–629**, of which **620** is the plan's one owner-run check and **621–629 are reserved**.

A number reserved by a plan that has not merged is invisible to a grep, and a number reserved in a merged plan is invisible to a grep of the checklist — `docs/plans/2026-10-16-voice.md` §5.3 states both, and it is why §5's fourth command reads the designs' own files and not one file. A later task that needs a number re-checks that section's facts rather than this conclusion.

## What this design does not do

- **It does not make `add` or `rm` reachable from a coding agent's session.** Both take the agent refusal first, before any read, and neither is added to a gate's allowlist. The desktop console may call `ls --json` and `status --json` and nothing else.
- **It does not re-add `--yes`.** The no-prompt policy's T11 removed `--yes` and `--non-interactive` together (commit `fe0d59d5`, #2633); the draft this plan adopts had put `--yes` back, and D2403 is the correction. `--ask` is what the command line has.
- **It does not delete a secret, a secret version, an image, a run's history or a person's pull request**, and it ships no `--purge` (D2408). `rm`'s own output names what it leaves, twice.
- **It does not disturb a run in flight.** `rm` reads whether one is going, takes the strict confirmation when it is, and never rewrites a run's records, its ledger or its counters — the property `fugaro update` already holds and `TestUpdateLeavesARunInFlightRunning` already pins.
- **It does not fork `doctor`.** `status` reuses the checks that exist and adds four rows that do not; it adds no second reader of a fact `doctor` already computes, and it writes nothing on any path.
- **It does not change a repository's `fugaro.yaml`.** That file is the only place for repo-specific behaviour, and `add` reads it and never writes it.
- **It does not touch `docs/status.md`**, does not edit `.github/workflows/`, and adds no dependency.
- **It does not touch `belong`.** Every live step is owner-run on the sandbox installation.
- **It does not verify the Bitbucket half live.** Check 620 uses the GitHub sandbox; the Bitbucket rows reuse checks that are built and tested, but no owner-run check here reads a real workspace — that is a second check and a reserved number, if the owner wants it.
