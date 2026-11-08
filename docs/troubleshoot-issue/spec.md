# Fugaro: troubleshoot findings → `fugaro issue`

## Goal

Make `fugaro troubleshoot` output actionable: fix local problems on the spot, and save real Fugaro bugs as numbered issue files that can be filed on GitHub in a separate step.

## 1. Classify findings in `fugaro troubleshoot`

Each finding goes into one of three buckets:

- **Local**: wrong config, missing credentials, environment problems on the user's machine. Print fix steps right away. Never save it as an issue.
- **Fugaro**: an actual bug, or a case Fugaro can't handle automatically. Save it as an issue file.
- **Unclear**: save it as an issue file marked `unclear`. The user decides whether to submit it.

When troubleshoot finishes, print one line per saved issue, for example: `Saved issue #0007. Run 'fugaro issue submit 7' to file it on GitHub.` Never submit automatically.

## 2. Issue files: saved locally first, then shared with the project

Issues are project-wide, not tied to one machine. Troubleshoot runs on every team member's machine, but everyone on the project shares the same issues.

- **Save locally first.** Write the redacted issue to a local Fugaro issues folder straight away, so nothing is lost when the user is offline or the upload fails. Don't use the name "reports", which is already taken.
- **Then upload** it to the project's shared storage in GCS, next to the other project-level Fugaro data such as recipes.
- **Sync:** local issues that haven't been uploaded yet are retried on the next Fugaro command or by `fugaro issue sync`. Once an upload succeeds, the issue is marked as uploaded locally.
- **Numbering:** each issue gets a local ID right away. Once uploaded, it also gets a sequential project-wide number from shared storage (for example, #0007), and that is the number shown from then on.
- **Naming:** each file is named with its number plus the task ID, for example `0007-<task-id>.md`.
- **Metadata header:** number, task ID, reporter, created time, classification (`fugaro` or `unclear`), Fugaro version, OS, status (`pending`, `submitted` or `dismissed`), and the GitHub URL once submitted.
- **Body:** a summary, the diagnosis, steps to reproduce if known, and log excerpts that have been redacted.
- **Duplicates:** when uploading, check against existing project issues. If one matches, attach the new occurrence to it (reporter, time, logs) instead of creating another issue.
- Redaction always happens on the user's machine before anything is written or uploaded.

## 3. Redaction (required)

- Scrub secrets before writing the file: API keys, tokens, passwords, OAuth secrets, private keys, connection strings, and emails if possible.
- Keep log excerpts short and relevant, not full log dumps.
- `submit` shows a preview of exactly what will be posted and asks for confirmation.

## 4. `fugaro issue` command

- `fugaro issue list`: shows issues with their number, classification, status and summary.
- `fugaro issue show <n>`: prints an issue.
- `fugaro issue submit <n>`: claims the issue in shared storage (so two people can't file it twice), files it on GitHub with `gh issue create` against the Fugaro repo, then records the URL and sets the status to `submitted`.
- `fugaro issue submit` with no number: goes through all `pending` issues and asks before filing each one.
- `fugaro issue dismiss <n>`: marks an issue as not worth submitting.

## 4b. Who can submit

- Anyone on the team who has `gh` set up can submit any pending project issue. Fugaro is open source, so there's no reason to gate who can file.
- `fugaro issue list` shows every project issue from the whole team, not just your own.
- Setup (run by the admin) recommends installing `gh` and running `gh auth login`, but doesn't require it. Team members without `gh` still save and upload issues, and anyone who has `gh` can submit them.

## 5. When `gh` isn't available

- If `gh` isn't installed, detect the platform and print the install command:
  - macOS: `brew install gh`
  - Windows: `winget install GitHub.cli`
  - Linux: the apt or dnf command for the detected distro, or a link to the official install page if the distro is unknown
- If `gh` is installed but not logged in, tell the user to run `gh auth login`.
- In both cases, also print a GitHub new-issue link with the title and body already filled in. If the body is too long for a URL, shorten it and point to the local file.

## 6. Issues section in the daily report

Add a short issues section to the existing daily report on costs and job activity.

- **New:** issues created in the last 24 hours, with their classification and a one-line summary.
- **Pending:** issues not yet submitted, showing how long each has been waiting, oldest first.
- **Recurring:** issues sorted by how many times they've happened, including duplicates merged from other team members.
- **Next to each pending issue:** the exact command to submit it, for example `fugaro issue submit 7`.
- **Nudge:** the wording gets firmer as an issue ages or recurs more often. Exact thresholds can be set in config.
- **When nothing is pending:** the section shrinks to a single line, for example "No pending issues."

## Acceptance

- Local-only problems never create issue files.
- No secrets show up in saved issue files (add tests with sample secrets).
- Running submit with no number skips issues that are already submitted or dismissed.
- The fallback works when `gh` is missing or not logged in.
