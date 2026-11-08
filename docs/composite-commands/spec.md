# Fugaro Composite Management Commands Spec

## Summary

Fugaro's management commands (settings, image add, and other resource commands) must do everything needed for a change to actually take effect. By default they run the follow-up steps themselves, such as publishing the policy or running an update. The user should never be told to run a series of extra commands after making a change.

## Background

- Users never edit Fugaro's YAML configuration files (project, local, repo) by hand. Management commands sit on top of those files: they set, change and delete values, check the input, and know which file each value belongs in.
- This already works well, and it also helps the console, which uses the same commands instead of parsing and editing files itself.
- **The problem:** after adding an image, the user was told to run `fugaro image add ...`, then `fugaro policy publish`, then `fugaro update`. A typical user has no idea what a policy is, why it needs publishing, or why an update is needed. The steps feel like magic.
- **The principle:** if we'd have to tell the user what to run next, that knowledge belongs inside the command.

## Rules

### 1. Commands apply changes by default

- Every management command makes the change and then runs all the steps needed for it to take effect (for example policy publish, update).
- When it finishes, the change is live.

### 2. Each change declares what it affects

- Every setting and resource type declares what it affects: for example "needs policy publish", "needs update", or nothing.
- A command runs only the steps that the change actually requires, in the right order.
- The steps live in one central place, so all commands and the console behave the same way.

### 3. A flag to only save the change

- A flag such as `--no-apply` (or `--only`) saves the change without running the follow-up steps.
- Use it to make several changes in a row and apply them once.
- A single apply command (for example `fugaro apply`) then runs all the pending follow-up steps, skipping duplicates, so publish and update run only once.
- Fugaro keeps track of which follow-up steps are still pending, so nothing is forgotten.

### 4. Plain-language output

- After applying, the command prints one or two plain sentences, for example "Image added and now available to your jobs."
- Internal words like "policy publish" appear only in verbose or debug output.

### 5. Fits with the no-prompt policy

- Running follow-up steps never asks for confirmation. Any cost is checked against the budget cap as set out in the No-Prompt Policy Spec.

## Enforcement

- Audit all existing management commands (settings, image add, and the other resource commands) and add their follow-up steps.
- Agent guidance: if documentation or an agent would tell the user to "now run X", treat that as a bug and move X into the command.
- Tests: after each management command finishes without the flag, the change is live and there are no pending follow-up steps.

## Acceptance criteria

- `fugaro image add ...` alone makes the image usable, with no further commands needed.
- With `--no-apply`, several changes followed by one apply run each needed step only once.
- No user-facing message tells the user to run publish or update by hand.
