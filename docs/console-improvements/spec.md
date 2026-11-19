# Fugaro Console Improvements Spec

November 2026. Scope: the Electron console, which stays a thin front end over the Fugaro CLI. All logic and data access remain in the CLI; the console only calls CLI commands and renders their output.

## Goals

- Make the console genuinely useful day to day, so it is preferred over the terminal tools rather than the reverse.
- Fix readability and layout problems in the Live pane, task graph and settings.
- Expose the CLI's management (CRUD) commands through the UI.
- Offer first-run setup from the console.
- Make pane switching feel fast.

Out of scope for now: the Spend/Reports pane (works well, no changes), and a long-running CLI server mode (deferred, see Performance).

## 1. Live pane

The Live pane should be the graphical equivalent of Fugaro Watch. Watch is well designed: its economy of space produces condensed, easy-to-scan information. Today the Live pane is hard to read and is less useful than Watch.

Requirements:

- Model the information layout on Fugaro Watch: the same fields, grouping and ordering (running jobs, what each is doing, elapsed time, status), using the extra space for clarity rather than more clutter.
- Remove the growing row of blue squares. They appear to be progress or heartbeat indicators (grey text on light blue, looks like a percentage) appended as a new element on every update, never completing, which expands the pane indefinitely. Any progress indicator must update in place, one per job, and must end when the job ends.
- Ensure sufficient text contrast throughout; no grey-on-light-blue.
- Pane height must not grow from repeated updates; long lists scroll.

## 2. Task graph

The graph of tasks and dependencies is currently laid out wide and short, forcing two-dimensional scrolling.

Requirements:

- Lay out top to bottom (vertical flow), constrained to the pane width.
- Only vertical scrolling; it should read like a normal web page.
- If a level is too wide, wrap or stack nodes rather than overflow horizontally.

## 3. Editing a setting

Today clicking edit embeds an edit box inline in the page, which looks poor.

Requirements:

- Keep a small pen icon next to each setting.
- Clicking it opens a standard modal dialog with: human-readable setting name, a description of what it does, the current value, an input control appropriate to the type, and OK / Cancel.
- Input controls by type: dropdown for small enumerations, boolean toggle, numeric input with validation (type, range), text with format validation where applicable.
- Validate before OK is enabled; show clear inline errors.
- "Reset to default" option that removes the explicit value; display the default value that would then apply.
- Writes go through the corresponding CLI command, never by editing config files directly.

## 4. Project settings organisation

Today settings are a long flat list of obscure dotted config keys, with no search. Since the CLI exists so nobody edits config files directly, users should not need to know these key names.

Requirements:

- Give every setting a human-readable label and a short description. The raw key may be shown subtly (for example in the edit dialog) for advanced users.
- Organise settings into logical groups, displayed as an accordion: groups collapsed by default, expand the one of interest.
- Add a search box that filters by label, description and raw key, auto-expanding matching groups.
- The labels, descriptions and groups should be defined once, ideally in the CLI's settings metadata, so the CLI help and console stay consistent.

## 5. Management of system objects

The CLI has CRUD-style commands for core objects: projects, repos, users, models, tokens and others (for example repo add, remove, list, status). The console should expose equivalent functionality as a thin front end.

First pass (expect to iterate after use):

- One common pattern for every object type: a list view (table with key columns and status), an Add button, a pen icon for edit, and delete with confirmation.
- Add and edit reuse the modal dialog from section 3, so validation and look are consistent.
- Organise navigation semantically rather than as a flat list of object types. Initial grouping inside each project tab, under a Manage section:
  - People & access: users, roles, OAuth tokens
  - Code: repos, images, recipes
  - Models & keys: API keys, model choices
  - Budget: spending caps, alerts, with a link to the Spend report
- Projects themselves are managed from the cross-project overview.
- Optional enhancement: CLI commands describe their own fields (type, allowed values, defaults) in JSON so the console can generate forms rather than hand-code each one.

## 6. First-run setup wizard

If the console starts and no Fugaro project is initialised, present a wizard equivalent to the CLI `fugaro setup` flow.

Requirements:

- Ask the same few questions as `fugaro setup` (project name and so on), with the same defaults.
- The console must not drive an interactive terminal prompt. `fugaro setup` should accept all answers non-interactively (flags or a JSON answers file); the wizard collects answers and passes them.
- Final step streams setup progress live and shows errors clearly, since setup does real provisioning work.
- Reuse the same wizard behind an "Add project" button in the overview.

## 7. Performance

Pane switches are sluggish, often several seconds and sometimes 7 to 10 seconds. The console itself is not slow; the CLI queries RTDB and assembles a full JSON result before returning, and nothing is cached, so every click pays the full cost again. Process startup is not believed to be a factor (the Go binary starts quickly, as Fugaro Watch shows).

Phase 1 (now):

- Caching with stale-while-revalidate: a pane renders cached data immediately, refreshes in the background, then updates. After the first load, switching panes should be instant.
- Incremental output: CLI commands used by the console emit newline-delimited JSON (one record per line) so the console renders rows as they arrive rather than waiting for one large document.
- Where possible, reuse the data path Fugaro Watch already uses to stream from RTDB, rather than building a full snapshot first.

Phase 2 (deferred, only if Phase 1 is not good enough):

- A long-running CLI mode (for example `fugaro serve`) that the console talks to over stdio, keeping connections warm and pushing live RTDB updates.

## 8. Cross-cutting robustness

These apply to every pane and dialog.

- **Version compatibility:** on startup the console reads the CLI's reported version and warns clearly if they are incompatible, rather than failing in odd ways.
- **Cache invalidation after writes:** any edit, add or delete immediately refreshes the panes that show the affected data, bypassing the stale-while-revalidate cache.
- **Errors and empty states:** a failed or timed-out CLI call shows the actual error with a Retry button, never a blank pane or endless spinner. Empty data gets a clear message (for example "No jobs running").
- **Permissions:** actions the user's role (viewer, operator, admin) cannot perform are disabled with a tooltip explaining why, rather than failing after the click.
- **Destructive actions:** removing a repo, user or project states the consequences in the confirmation dialog; project deletion requires typing the project name to confirm.

## Acceptance criteria

- Live pane conveys at least what Fugaro Watch shows, readably, with no growing indicator rows.
- Graph needs only vertical scrolling at normal window widths.
- Every setting is editable via the modal dialog with validation and reset to default.
- Settings are grouped, labelled in plain language, and searchable.
- All CLI CRUD operations for the listed objects are available in the UI.
- A fresh install can be fully set up from the console.
- Revisiting a pane renders in well under a second; first loads show rows progressively.
