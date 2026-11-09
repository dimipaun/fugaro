# Fugaro: contract-first task decoupling

## Problem

When planning breaks work into tasks, dependencies between those tasks are taken as given, so dependent tasks wait for each other one after another. Many of these dependencies could be broken by agreeing up front on how the pieces interact, which would let more tasks run in parallel.

## Solution

Add a **dependency-breaking pass** to planning. For each dependency edge, the planner asks: "Can this be broken by defining a contract first?" If it can, it inserts a small contract task. Once that task merges, both sides build in parallel.

## Flow

1. The planner builds the task graph as it does today.
2. **Decoupling pass**: for each dependency edge A → B (B needs A), decide whether B only needs A's *interface*, not A's implementation.
3. If B only needs the interface, create a **contract task** C that is small and fast. It defines:
   - interfaces, types and function signatures
   - API shapes and schemas, events and messages, and DB schema changes where relevant
   - stub or mock implementations that behave plausibly
   - a short contract doc in the repo explaining how the pieces interact
4. The graph changes from A → B to C → A and C → B, so A and B run in parallel after C merges.
5. Add an **integration task** that depends on both A and B. It removes the stubs, wires up the real implementation, and runs end-to-end checks.
6. Leave dependencies that truly need the implementation (for example, B needs A's real behaviour or data) as they are.

## Contract tasks

- Keep them small, ideally one short patch.
- Review contract tasks at least at the parent's rigor level. Contracts are cheap to get right early and costly to change late.
- Commit contracts to the repo so every task's agent can read them.

## Contract changes

- If a task discovers the contract is wrong or incomplete, it must not quietly work around it.
- Instead it raises a **contract change request**: a small task that amends the contract. The planner tells the other tasks built on that contract, and they rebase onto the change.
- Record every change request so planners can learn which contracts tend to be wrong.

## Rigor level

- **POC / Startup**: decouple aggressively. Stubs and rough contracts are fine.
- **Pro**: decouple where the contract is clear. Contracts get proper review.
- **Critical**: decouple only with fully specified contracts, and require strong integration tests.
- **Hotfix**: usually skip this pass, since the work is typically one task.

## Planner output

- Show which dependencies were broken and which were kept, with a one-line reason for each.
- Report the estimated gain in parallelism (for example, critical path length before and after).

## Ship as a built-in skill

This behaviour ships with Fugaro as a built-in planning skill, so every user gets it by default, not just our own setup.

- Package the decoupling pass, contract-task template and contract-change-request flow as a bundled planner skill that is on by default.
- Keep it generic: no Belong-specific or repo-specific assumptions. Project-specific tuning goes in project or repo config.
- Users can turn it off or tune it per project, area, repo or task, following the existing rigor-level precedence.
- General principle: things we learn while dogfooding Fugaro become built-in skills or recipes where they apply to everyone. Anything specific to our own projects stays in our project settings. The post-review triage personas should follow the same rule.

## Acceptance

- For a plan with interface-only dependencies, the planner inserts contract tasks and the dependent tasks run in parallel.
- An integration task exists for every broken dependency, and no stubs remain once it merges.
- Contract change requests work end to end, including notifying the tasks that depend on the contract.
