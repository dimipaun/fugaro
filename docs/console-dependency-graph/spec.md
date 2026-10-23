# Fugaro Console: the dependency graph view (owner's spec, 2026-10-22 night)

(The console part of the owner's "Release Names, Watch Fixes and Console Dependency Graph" spec, with its "Independent Release Streams" addendum. Where the addendum and the spec disagree, the addendum wins. Verbatim.)

## 3. Console: dependency graph view

### Structure

- Full task dependency graph, grouped into **boxes per planned release** (Badger, Badger one, Camel...). Use a layout engine that supports nested groups, e.g. ELK (elkjs).
- **Finished releases** collapse by default into small chips along the top (e.g. "Badger ✓") and expand only when clicked.
- Each release box header has a progress bar (e.g. "7 of 12 done").

### Nodes

- Each node shows the task name, the PR number if any, and the job's last four characters if it has a job.
- **Running:** a light streak circles the node border (comet effect). Its colour shows the state: blue = implementing, purple = review, amber = fixing (extend for other known states).
- **Critical:** a pulsing red glow behind the node.
- **Finished:** solid green, slightly faded.
- **Blocked** (waiting on a dependency): dashed outline.
- Clicking a node opens its job details or PR.

### Edges

- **Critical path:** a distinct colour, a thicker line, and an animated flow along the line.
- **Cross-release dependencies:** faint, thin grey lines by default. They highlight when a task is selected or being dragged to another release, which helps when moving tasks between releases.

### Navigation

- Scroll / pinch zoom, plus a "fit to screen" button.


---

# Addendum: Independent Release Streams (owner, 2026-10-22; where it and the spec disagree, the addendum wins)

## 1. Three independent release streams
Fugaro ships as three products with their own release cycles, no longer tied together: **CLI** (core Fugaro), **Console** (Electron desktop app), **Mobile** (phone app). Each stream has its own version numbers and its own release names. A release in one stream does not imply a release in the others.

## 2. Release names per stream
Each stream has its own naming theme, so the name alone tells you the stream. Rules from the spec still apply within each stream: one name per minor, alphabetical order, patches spoken as "<name> <patch>" (e.g. "Mango two").

| Stream  | Theme              | Sequence (start)                                   |
|---------|--------------------|----------------------------------------------------|
| CLI     | Animals (no birds) | Badger, Camel, Dolphin, Gecko, Hippo, Koala, Moose |
| Console | Fruits             | Cherry, Mango, Melon, ...                          |
| Mobile  | Birds              | Falcon, Pelican, Robin, ...                        |

Changes to the original spec: **Falcon moves from CLI to Mobile** (the CLI list contains no birds); CLI 0.10 is still **Badger**; Console: avoid Apple and Lemon (brand / negative connotations); insects were considered for Mobile and rejected (they read as "bugs"); the starting version for Console and Mobile is set when their first release is cut; their first minor takes the first name in the list.

## 3. Dependency graph: streams
Changes to section 3 of the spec: one tab per stream (CLI, Console, Mobile), each with that stream's release boxes and tasks as in the spec; an **All** tab shows the three streams side by side as vertical columns, each with its own release boxes; cross-stream dependencies (e.g. a Mobile task waiting on a CLI release) are faint thin grey lines, highlighted when a task is selected or dragged; in a single-stream tab a task with a dependency in another stream shows a small marker naming that stream and release (e.g. "needs CLI · Camel"); release box headers include the stream (e.g. "Console · Mango"); collapsed finished-release chips at the top are grouped by stream.
