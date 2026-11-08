# Fugaro: `fugaro issue mine` — what happened to my issues

## Goal

Let every team member see what happened to the issues they hit: whether each one is still local, pending, submitted, fixed or dismissed, and when its status changed.

## Command

- `fugaro issue mine` lists issues where the current user is a reporter. That includes issues they created and duplicates of theirs that were merged into someone else's issue.
- The command lives under `fugaro issue`. Don't call it "report", because that name is already used by the daily reports.
- `fugaro issue list --mine` works as an alias.

## What each row shows

- The issue's short number and a one-line summary.
- **Status:** `local` (not uploaded yet), `pending`, `submitted`, `resolved`, `dismissed` or `regression`.
- **Dates:** when the user hit it, when it was submitted, and when it was resolved.
- **For submitted issues:** the GitHub link and whether the issue is open or closed on GitHub.
- **For resolved issues:** the version that fixed it, and whether the user is already on that version. If they aren't, nudge them to run `fugaro update`.
- **For resolved issues the user is already covered for:** the command that failed (redacted), as a suggestion to try again, never run automatically.
- **Other reporters:** how many other people hit the same issue, for example "also hit by 2 others".

## Ordering and filters

- Show issues that need the user's attention first: fixed but not updated yet, then pending, then submitted, then resolved or dismissed.
- `--all` includes old resolved and dismissed issues, which are hidden by default after 30 days.
- `--json` gives machine-readable output.

## Data and refresh

- Read from the project's shared storage, plus any local issues that haven't been uploaded yet.
- Refresh GitHub status the same way `fugaro update` does, using the public API with no `gh` needed. If GitHub can't be reached, show the last known status and when it was last checked.
- The current user is identified the same way as the issue's reporter field.

## Acceptance

- A user sees their own issues, including ones merged into another reporter's issue.
- A resolved issue shows the fixing version and, if the user is behind, a prompt to update.
- The command works offline, showing local issues and the last known statuses.
