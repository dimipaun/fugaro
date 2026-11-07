# Fugaro No-Prompt Policy Spec

## Summary

Fugaro never asks for interactive confirmation. Not from the owner at the terminal, and not from an agent. Spending is controlled by budget caps instead of prompts. Any confirmation prompt in Fugaro is a bug.

## Background

Fugaro commands such as `fugaro update` and `fugaro build-image` have kept asking for confirmation, for example making the user type the project name, even after hard rules were added. These prompts serve no purpose:

- Fugaro users aren't expected to understand the architecture or the security implications of what is being changed, so they can only approve blindly.
- A blind approval doesn't really move responsibility to the user. Fugaro is responsible for what it does.
- Prompts block non-interactive and agent-driven use. An agent can't answer a prompt, so the command just hangs.

## Managed projects

- Fugaro only supports **managed projects**. Every Firebase and GCP project Fugaro uses belongs to Fugaro to manage. It's Fugaro's scratch pad.
- No customer-critical data lives in a managed project. If one is badly damaged, the worst losses are things like reports and cost records.
- A **Fugaro project repo** (a meta repo) regularly saves the state of the managed project, so the history is kept and the project can be rebuilt from scratch. *(Status: designed, not yet implemented.)*
- Fugaro doesn't touch anything outside its managed project.

## Rules

### 1. No prompts, ever

- No command may wait for interactive input: no y/n questions, no typing the project name, no "are you sure".
- This applies the same way to commands typed by the owner in a terminal and to commands run by agents.
- Managing resources inside a managed project (IAM, Terraform changes, service setup, and so on) never needs confirmation.
- Calls to underlying tools must also be non-interactive, for example Terraform with auto-approve and gcloud with the quiet flag, so that no prompt comes through from a lower layer.

### 2. Budgets replace cost prompts

- Building an image is just another job that costs money. It uses the **same budget and cap as jobs**. There's no special handling or confirmation for image builds.
- Before an action that costs money, Fugaro estimates the cost and checks it against the cap.
- **Under the cap:** go ahead without asking.
- **Over the cap:** stop with a clear error. Don't prompt.

### 3. Over-cap error format

When an action is blocked by the cap, the error must give:

- the estimated cost of the action,
- the current cap and how much of it has been spent,
- the exact single command to raise the cap.

The command then exits with a non-zero status, so agents and scripts can tell that it was blocked.

### 4. Agents may run all commands

- Agents can run image builds, updates, and resource management, just as they can already launch jobs. Budget caps are the only spending limit.

### 5. Anything outside the managed project is a design decision

- Fugaro doesn't currently touch anything outside its managed project.
- If a future feature would touch outside resources (a customer repo, production, anything irreversible that belongs to the customer), it must be raised as an explicit design decision first. It must not be handled quietly by adding a confirmation prompt.

## Enforcement

- **Tests:** a test suite runs every CLI command with stdin closed or not attached to a terminal, and fails if any command waits for input.
- **Code review rule:** any new `input()`, confirm dialog, or interactive library call in Fugaro is rejected.
- **Agent instructions:** CLAUDE.md and the agent guidance state this policy and point to this spec as the authority. Coding agents must not add confirmation steps "for safety".
- **Cleanup:** audit the existing commands (update, build-image, Terraform wrappers, and so on) and remove all current prompts.

## Acceptance criteria

- Running any Fugaro command with stdin closed never hangs.
- Image builds under the cap run without stopping. Over the cap, they fail with the error format above.
- No confirmation prompt is left anywhere in the codebase.
