# Fugaro Diagnose Spec

## Summary

`fugaro diagnose <run-id>` runs a local agent over the full log of a failed run, the doctor information, and the Fugaro source code at the user's exact version. It tells the user whether the problem is in their configuration (and how to fix it) or a bug in Fugaro (with an issue report ready to file).

## Background

- Real failures usually come from one specific command, such as `fugaro update`, not from the general health of the setup. `fugaro doctor` alone often isn't enough.
- Right now, fixing a problem means copying the output into an agent that can see the Fugaro source. That works for experienced users but not for new ones. A broken setup can block a whole team.
- Fugaro is open source, so any agent can read the code. Diagnose turns that into a single command.

## Depends on: clean output and run logs

- Commands show clean, Brew-style status output by default: one line per step, with a check mark or an X. The full output (Terraform, gcloud, and so on) goes to a log file. *(In progress.)*
- Every run has its own **run ID** and its own log file.
- Secrets are removed when the log is written, so logs are safe to give to an agent or attach to an issue.
- When a step fails, the command prints one line, for example: `Something went wrong. Run: fugaro diagnose <run-id>`.

## Command

- `fugaro diagnose <run-id>`. With no run ID, it uses the most recent failed run.
- It runs **locally, never as a cloud job**, because the cloud setup may be exactly what's broken.
- What it collects: the run's log, `fugaro doctor` output, the Fugaro version, and the effective configuration with secrets removed.
- The agent gets a troubleshooting prompt and a troubleshooting guide kept in the repo and written for agents.
- What it outputs:
    - **Configuration problem:** the cause in plain language, plus the exact command to fix it.
    - **Fugaro bug:** a short explanation, plus an issue report ready to file (summary, steps, the log extract with secrets removed, the version).
    - **Fixed in a newer release:** tells the user to upgrade, naming the version.

## Consent

- Fugaro never runs the agent on its own when something fails. It only suggests the command.
- The user typing `fugaro diagnose` is the consent. There's no prompt (see the No-Prompt Policy Spec).

## Source code

- The source lives in a local Fugaro hidden directory, for example `~/.fugaro/source/`.
- **Default: a git tree.** A shallow clone of the public repo over HTTPS, checked out at the release tag matching the installed Fugaro version. No GitHub account is needed.
- When the version changes, fetch the new tag into the same tree and check it out.
- The agent may fetch newer tags and look at the history to see whether the bug has already been fixed.
- **Fallback: a tarball.** If git isn't installed, download the source tarball for the installed version's tag.
- Keep track of which version the downloaded tarball is. If it doesn't match the installed version, download the right tarball and replace the old one.

## Credentials and model

- Use the AI credentials Fugaro already has, in the same order of preference as jobs: subscription OAuth token by default, then an Anthropic API key, OpenRouter, or Vertex through gcloud.
- Read the credentials from Secret Manager using the user's local gcloud login. If that fails, fall back to credentials in the local environment.
- **Default model: Sonnet.** Diagnosing means reading real code and logs, and diagnose runs are rare, so the cost stays small.
- The model can be changed with the normal settings command.

## Acceptance criteria

- A failed `fugaro update` prints its run ID and the diagnose command.
- `fugaro diagnose` with no arguments picks the latest failed run and gives a diagnosis without any extra setup.
- It works with no GitHub account, and with no git installed (using the tarball).
- The source used always matches the installed Fugaro version.
- No secrets appear in logs, agent input, or issue reports.
