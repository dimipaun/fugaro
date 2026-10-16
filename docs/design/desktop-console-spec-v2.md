# Fugaro Console — Feature Spec

Oct 15, 2026 · @Dimi

## Overview

An Electron desktop console for Fugaro: a richer, graphical Fugaro Watch. It is global to the desktop, not tied to a repo. Visual inspiration: the task manager at tmog.org.

## Project discovery and layout

- Reads the global Fugaro config in the home directory and lists every project the user has set up or has access to (including projects owned by others).
- Connects to each project's control plane (Firebase) using credentials already on the machine; projects added with the CLI appear automatically.
- Layout: left sidebar with a pinned **Overview** at the top, then one entry per project with a status dot (jobs running, blocked, needs attention).
- Overview: total spend across projects, all live jobs, anything waiting on the user.
- Per-project tabs: Live jobs, Dependency graph, Version roadmap, Spending.

## Dependency graph

- Renders the task dependency graph produced by the planner.
- Colour coding: done, running, ready (unblocked), blocked.
- Highlights bottleneck nodes, ranked by downstream work blocked, ideally weighted by critical path rather than raw count.
- Answers "what else can we start in parallel?" at a glance.
- Action: launch selected ready tasks, or "fan out everything ready".

## Command channel to orchestrators

- Console writes commands (e.g. start tasks 4 and 7) to a command queue in the realtime database; orchestrating agents listen via a hook or a Fugaro command polled between turns.
- Alternative path: console launches ready jobs directly via the Fugaro CLI; the agent sees them appear in the graph.
- Multiple orchestrators: typically one per thread of work (e.g. several Claude tabs, possibly on the same repo). Each registers a session with an ID, label, and project; the console shows one graph per session and addresses commands to the owning session.
- Tasks are claimed before starting, so two agents (or an agent and the user) can't start the same task twice.

## Overlap detection

- Sessions grouped by repo.
- Soft warnings, not locks: compare files changed by running/finished job branches and flag overlaps before merge.
- Planner adds an expected footprint per task, so likely collisions are flagged before launch.

## Version roadmap

- Each task has a target version (e.g. 0.7.0, 0.7.5, 0.8.0).
- Roadmap view: a column per release; drag tasks between versions.
- Dependency-aware: flags a task scheduled earlier than something it depends on, and offers to pull the dependency forward.
- Source of truth is the Fugaro project (stored with project state, e.g. in the project Git repo). No GitHub milestones or Jira sync.

## Additional features (all wanted)

- **Review inbox:** finished jobs with PR links, ready for review.
- **Desktop notifications:** job finished, failed, or needs input.
- **Per-job controls:** live logs, cancel, retry.
- **Spending cap alerts:** warn as a project approaches its cap.
- Spending graphs and live tasks (from the original console idea).

## Related: independence-maximizing planning skill

- New skill or upgrade to the planning skill: decompose work into as many independent tasks as possible to maximize horizontal fan-out through Fugaro.
- Where dependencies exist, cut them with small interfaces/contracts (stubs) both sides build against.
- Outputs the dependency graph and per-task expected footprint, feeding the console directly.
