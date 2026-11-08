# Fugaro: show resolved issues on `fugaro update`

## Goal

When a user upgrades Fugaro from version X to version Y, tell them which of the project's submitted issues this upgrade fixes. That closes the loop from troubleshoot to issue to fix.

## Flow

1. `fugaro update` records the old version (X) and the new version (Y).
2. After a successful upgrade, load the project's issues with status `submitted` from shared storage.
3. For each one, check its GitHub issue. It counts as resolved by this upgrade if:
   - the issue is closed as completed, and
   - the PR or commit that closed it first shipped in a release after X and up to Y.
4. Print a short summary, for example: `This update fixes 2 of your project's issues: #0007 (short summary), #0012 (short summary).`
5. Mark those issues `resolved` in shared storage, recording the version that fixed them and the GitHub link.
6. For each fixed issue, show the original command that failed (recorded in the issue, redacted) as a suggestion only, for example: `#0007 is fixed. The command that failed was 'fugaro run ...'. You may want to try it again.` Never run it automatically, and note that it may change things, since commands can be mutating. If the problem shows up again later, it is caught as described under Statuses.

**Show who hit each issue.** Next to every fixed issue, list everyone who ran into it, using the reporter recorded in the issue plus anyone from merged duplicates, for example `#0007: hit by Jack` or `#0012: hit by Ana and Jack`. The person running the update is often an admin, not the people who hit the failure, so they need to know whom to tell. The retry suggestion is meant for those reporters.

## Mapping fixes to releases

- Preferred: use release notes or tags. Each release lists the issues it closes (from `Fixes #N` in merged PRs).
- Fallback: find the commit that closed the issue and check which release tags contain it.
- Issues closed as not planned or duplicate get their status updated quietly, without counting them as fixed.

## Access

- The Fugaro repo is public, so these checks use the GitHub API without `gh` or authentication.
- If GitHub can't be reached, skip quietly and retry on the next update or the next daily report.

## Statuses

- Add `resolved` to the issue statuses (`pending`, `submitted`, `dismissed`, `resolved`).
- Nothing special is needed for regressions. If the problem happens again, the user runs troubleshoot as usual, which only writes a local issue. When that issue is uploaded to the project, the normal duplicate check finds the matching resolved issue. If the user's version already includes the fix, the issue is flagged as a regression and linked to the original GitHub issue so it can be reopened.

## Daily report

- The daily report's issues section also lists issues resolved since the last report, with the version that fixes them, and nudges anyone still on an older version to upgrade.

## Acceptance

- Updating from X to Y lists exactly the submitted issues fixed in releases after X and up to Y.
- Resolved issues are marked in shared storage, so every team member sees them.
- Update still succeeds and stays quiet when GitHub is unreachable.
