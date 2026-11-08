# Fugaro: short run IDs everywhere, console live tab matches watch

## Goal

Show runs everywhere by the last four characters of the run name, as `fugaro watch` already does. Rework the console's live tab to follow the design of `fugaro watch`.

## 1. Short ID rule

- Run names look like `<long-name>-xxxx`. The **short ID** is the final four characters.
- Show only the short ID in all places runs are shown: the CLI (all commands), the console, daily reports, issue files, and anything agents write (PR descriptions, comments, task notes, summaries).
- Show the full ID only in a details view, in logs and machine-readable output (`--json`), or when it is explicitly copied.
- Add the rule to the agents' system and task instructions: "Refer to runs and tasks only by their short ID (last 4 characters). Never print the full ID."

## 2. Short IDs as input

- Every command that takes a run or task ID accepts the short ID, the full ID, or any unique ending of the full ID.
- If a short ID matches more than one run, list the matches, each with the minimum suffix that tells them apart, and ask the user to choose.
- Resolve against active runs first, then recent ones.

## 3. Collisions in displays

- If two runs shown together share the same four characters, show both with as many extra characters as it takes to tell them apart (for example `x3f9a` vs `b3f9a`).

## 4. Console live tab

- Rework the console's live tab to follow the design of `fugaro watch`: same columns, short IDs, ordering and status presentation.
- Treat `fugaro watch` as the reference design. When watch changes, the live tab follows.

## Acceptance

- No full run IDs in the CLI, console list views, reports or agent-written text, except in details views and `--json`.
- Commands work when given a short ID, and an ambiguous one prompts the user to choose.
- The console's live tab matches `fugaro watch` in content and layout.
