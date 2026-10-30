# Fugaro: Review Levels, A/B Testing, Job Max

All six sections are independent and can be implemented as parallel jobs. Section 2 amends the existing A/B testing plan, which has not been built yet.

## 1. Recipes and review levels

**Problem:** the dogfooding recipe uses DeepSeek for both the inner-loop code review and the senior reviewer. Paying the same model to review its own work twice is wasteful and catches nothing new.

**Change:** replace the single "senior reviewer" with three numbered review levels.

- **Coding agent**: does the implementation.
- **Review level 1**: inner loop. Usually the coding agent's own model or another cheap model.
- **Review level 2**: senior review. Must use a different model from level 1, possibly at a higher effort level.
- **Review level 3**: optional adversarial or security review, used only some of the time.

**Per-job settings from the orchestrator, for each level:**

- Model and effort, or `none` to skip the level.
- Optional focus instructions, e.g. "watch the migration logic" or "check auth boundaries".

**Guardrail:** reject level 2, or at least warn, if it uses the same model as level 1.

**Defaults by rigor level (suggested):** POC uses level 1 only. Startup and Pro use levels 1 and 2. Critical uses all three. The orchestrator can override any of these per job.

**Iteration caps:**

- Each level has its own maximum number of passes. This is configurable at the project level, and there is always a default, so every loop has a limit.
- Defaults: 3 for the inner loop (level 1), 2 for each outer level (2 and 3). That means one bounce back, and on the second pass the work either passes or the job reports back.

**Bounce-back flow:**

- When level 2 or 3 finds issues, the work goes back to the inner coding loop, with level 1 review as normal.
- The fix then goes straight back to the level that raised the issue. Levels in between are not repeated. Example: a level 3 bounce goes to the inner loop with level 1, then back to level 3, skipping level 2.
- This keeps the worst-case cost bounded and still catches sloppy fixes.

## 2. A/B testing

**Current state:** the A/B testing spec exists and has been reconciled with the coding agent, but it has not been implemented. As planned, it only covers the coding agent. This section amends that plan.

**Change:** support A/B testing for all four slots: coding agent and review levels 1, 2 and 3.

**Rules:**

- **Change one thing per job:** an experimental job changes only one slot, so any difference in results can be traced to that change.
- **Measure reviewers differently:**
    - Coding agent: quality is roughly whether the work passes review, plus cost.
    - Reviewers: measure how many real issues they catch. Use the next level up to check this, by recording issues a higher level found that the tested reviewer missed. Also track false alarms and cost.
- Reports should show quality and cost trade-offs per slot and per model and effort setting.

## 3. Job max

**Concept:** "Job maxing" is the smart version of token maxing. The goal isn't to spend tokens. It's to get as much completed, reviewed work done in parallel as possible. Cloud jobs can all run at once, so the only real limit is natural dependencies between tasks.

**Definition for the distributed skills:** maximising completed, reviewed jobs in parallel, not tokens spent. The only things that force jobs to run one after another are real dependencies between them.

**Deliverables:**

- Define "job max" clearly in the core skill that ships with Fugaro, so every user and agent can use the term.
- Add a `/jobmax` command: it breaks the backlog or plan into parallel jobs and launches as many as the caps allow.

**Planning for parallel work:** the implementation-planning skill should make the dependency graph as wide and shallow as possible.

- **Contracts first:** a short first job defines interfaces, schemas and API shapes, then everything else fans out against them at once.
- **Stubs and fakes:** each side builds against a fake version of the other, so neither waits.
- **Schedule from the graph:** the plan produces an explicit dependency graph, and `/jobmax` launches each job as soon as its dependencies are done.

**Open item:** check whether the earlier plan to make the skills favour parallel chunks was ever built.

## 4. Telemetry and crash reports

**Decision:** crash reports are on by default, and users can turn them off in the project config. Nothing else is sent home.

**Integrations:**

- **Mobile app:** Firebase Crashlytics. To make it optional, turn off automatic collection in the app config, then call `setCrashlyticsCollectionEnabled` based on the project setting.
- **Electron console and CLI:** Sentry, since Crashlytics doesn't support desktop or CLI. Turn off breadcrumbs, local variables and default PII capture, and strip usernames from file paths, so reports contain only stack traces.

**Config:** a project-level flag, e.g. `telemetry.crashReports: true | false`, defaulting to `true`.

**Agreement:** the subscription agreement currently makes crash reports mandatory. Reword it to say crash reports are sent by default, contain only stack traces with no PII, and can be turned off in the project config.

**Open item:** get a quick legal check that opt-out is fine for EU users, given that reports contain no PII.

## 5. Console dependency graph layout

**Goal:** the dependency graph fits the available width and scrolls only vertically. Scrolling both ways makes it hard to read and use.

**Changes (ELK.js):**

- Set `elk.direction` to `DOWN`, so dependency levels stack top to bottom and the graph grows tall rather than wide.
- After layout, render the SVG with a `viewBox` from the layout's width and height, `width: 100%` and `height: auto`, so it scales to the panel width. Allow vertical scrolling only.
- Try `elk.layered.wrapping.strategy` with `elk.aspectRatio` set from the panel's shape, to fold long chains.

**Wide levels:** when many parallel jobs sit in one level, scaling to fit the width makes nodes tiny. Set a minimum node size. If fitting the width would go below it, fall back to horizontal scrolling for that graph, or group the level's jobs into a collapsible cluster.

## 6. Console live gauges and graphs

**Goal:** a live, dense dashboard look in the Electron console, inspired by a well-known cross-platform task manager author's cross-platform task manager.

**Elements:**

- **Spend rate gauge:** a speedometer in $/hour, with a red zone approaching the project's spend cap.
- **Sparklines:** small live trend lines for spend and running jobs over the last hour.
- **Cap bars:** horizontal bars showing how much of each cap is used, such as spend, concurrent jobs and iterations.

**Library:** ECharts, which has gauges and live-updating charts built in, so little custom drawing is needed.
