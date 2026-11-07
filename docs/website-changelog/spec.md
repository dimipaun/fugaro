# Fugaro Website Change Log Spec

## Summary

The website's change log page shows only the **last three releases**. They're pulled automatically from GitHub, and a link leads to the full releases page on GitHub. Nobody edits the page by hand when a release goes out.

## Why

- The current page lists every release and has to be edited for each one. That work keeps growing.
- The full history already lives on GitHub, where developers expect to find it.
- Three recent releases show that the project is active, and they cost nothing extra once the page is automated.

## Requirements

- When the site builds, fetch the latest three releases from the GitHub Releases API (title, date, summary or notes, link).
- Show them in the site's existing change log style, newest first.
- Under the list, add a "See all releases on GitHub" link to the repo's releases page.
- Publishing a GitHub release triggers a site rebuild (or the site rebuilds on a schedule), so the page stays current.
- If the API call fails during a build, keep the last content that was built, or show only the GitHub link.
- Remove the manual per-release editing step from the release process.

## Acceptance criteria

- Publishing a new GitHub release updates the website's change log with no manual edits.
- The page shows exactly the three newest releases plus the GitHub link.
