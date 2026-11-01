# The managed agent converge: the coding agent runs the owner-level commands behind a deterministic gate

**Status: a design, and nothing is built. Every decision below is a proposal with a recommendation, and the owner has answered none of them.** This folder is the answer to the owner's ruling of 2026-11-01 — *"The whole point of a managed project is that YOU (the coding agent) manage it on my behalf without all these prompts"* — read against the code on `main`: in a **managed** project the coding agent runs the cloud-changing commands itself, and what replaces the typed confirmation is a **deterministic gate over the command's own plan**, with no prompt anywhere.

**This is a design pull request, opened as a draft and not merged: the owner decides.** It writes four documents, no code, no Terraform, no schema, no `go.mod`.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The design in full: what makes a project managed, which commands the agent may run and which it never may, the gate's rules per command, the cost bound on billable builds, the audit record, how a session learns it is in a managed project, the interaction with `fugaro update` and the other chains, revocation, and the threat model. **Where it disagrees with [reconciliation.md](reconciliation.md), the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | Each claim of the spec against the code on `main` and in the open pull requests: findings **F1–F21**, what exists and what is missing, the owner decisions **D1011–D1026** (§3) with the recommendation and the cost of each alternative, the number block and how it was read (§4), and the residual the design refuses to hide (§5) |
| [implementation-plan.md](implementation-plan.md) | Ten tasks **M0–M9**, each with its files (at most twelve), its interface, its **red step**, its named tests and its SEC flag; the shared-files table (§4); the order and what forces it (§3); the recommended slot (§6) |
| `README.md` | This file: the status, the reading of the number block, what is being asked, and what a reader should not take from here |

## What is being asked

Today a coding agent's session applies **nothing**. `initflow.CanConfirm` returns false for every class when a coding agent's environment marker is present, `AgentRefusal` never offers `--yes`, and the marker check is repeated at every CLI path that writes to the cloud. Exactly **one** command lifts that refusal: `fugaro managed apply`, whose gate is a deterministic predicate over the saved Terraform plan (`internal/infra/tf/managed_cover.go`) and whose whole design is the parent of this one (`docs/design/managed-projects.md`, D55 and D76).

The owner's point is that this leaves the feature half-built. A managed project whose agent can apply an agent-authored root but cannot run `fugaro update`, publish a layer, refresh an image or change a setting is a project the owner still has to drive; the prompts the ruling is about are not the ones in `managed apply` — they are the ones on every other command. So this design replaces the agent-session refusal, for a named set of owner-level commands and **only** in a managed project, with a gate of the same kind: deterministic, over the command's own plan, no prompt, no flag that reaches past it.

**The gateway enforces the cost cap on this run.** Every figure Claude Code prints about this session's own spend is ignored: it misprices non-Anthropic models, here by roughly a factor of twenty-five, and the gateway is the only authority. The run stops for one reason only — the gateway refuses a call.

## The number block

The decision block is taken **above every `D<number>` on `main` and in every open pull request**, read on 2026-11-01 rather than inherited from a neighbour's line.

- **On `main`**, at the head this branch was cut from (`15f4d8ab`), the highest *written* decision is **D900** (`docs/rigor-command/reconciliation.md`), and `docs/rigor-command/README.md:117` names **D901** as the number the next task takes.
- **In the open pull requests**, re-checked by fetching each branch and reading it rather than by trusting a table: **#1472** (`docs/cloud-senior-review`) writes D880–D899; **#1481** (`docs/run-without-checkout`) writes D901–D919 and **#1485** (`docs/run-limit-carry-over`) writes D901–D920 — **the two overlap, and neither is this work's to fix**; **#1487** (`docs/mcp-server`) takes D920–D934; **#1488** (`docs/plugin-user-scope`) takes D940–D953; **#1490** (`docs/periodic-reviews`) takes D960–D979; **#1492** (`docs/workspaces`) takes D980–D995; **#1491** (`docs/binary-allow-rule`) takes **D981–D1010**. Every other open pull request is code, or a document that writes no new block.
- **The highest number claimed anywhere is therefore `D1010`**, and the next free block above it is **D1011–D1040**, the thirty-wide width `docs/ee-reorg` took for D520–D549 and `docs/secrets-brokering` for D490–D519 before it. [reconciliation.md](reconciliation.md) §4 gives the **branch** each block was read from rather than the pull-request number, because the branch is what was fetched and what anyone can re-fetch — this environment cannot map a head branch to a pull request without the GitHub API — and it carries the four commands that re-check the whole table.

**This work takes D1011–D1040: D1011–D1026 are written here and answered with a recommendation, and D1027–D1040 are reserved and unused.** A later task that needs a number re-checks this section's facts rather than its conclusion. A scan for four-digit numbers is a scan for `D1…`, and the token `UID1000` in `docs/plans/2026-09-30-m9a-gateway-and-project-identity.md` is a false positive `docs/models-commands/README.md` already records the shape of, not a decision.

## What this design does not do

- **It does not remove the typed confirmation outside a managed project.** A project missing either mark, carrying another value, or whose marks cannot be read keeps D55's refusal exactly, and the property test that holds it (`TestManagedZeroValueKeepsEveryRefusal`, `internal/managed/apply_test.go`) stays.
- **It does not make `fugaro managed flag` or `fugaro managed unflag` agent-runnable.** The two commands that write the mark are the owner's, always: the mark is what turns the refusal off, so a session that could write it could grant itself everything in this document.
- **It does not make the gate a boundary against an agent that goes around the program.** The agent shares the container, the user and the metadata server with the runner; `SECURITY.md` says so, `managed apply`'s own header says so, and §5 of [reconciliation.md](reconciliation.md) repeats it rather than softening it. What the gate stops is the accident and the injection, not the adversary.
- **It does not touch `belong`.** No task of the plan names it, and every live step of the design is owner-run on a sandbox installation.
- **It does not decide anything the parent design decided.** D75's deployer fence, D76's command, D77's state bucket, D59's inventory and D74's delete list are inherited as they are; [reconciliation.md](reconciliation.md) §2 says which of them the gate here reads and which it leaves alone.
- **It is not a licence, key or payment change**, it adds no licence notice to any file, and it writes none of the warning text another 0.15.0 task owns.
