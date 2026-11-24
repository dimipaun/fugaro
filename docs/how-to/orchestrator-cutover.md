# Cutting over to the orchestrator daemon

The daemon takes over in one step, with no shadow week (D1235): from the day it
runs, it is what launches queued work, marks finished runs and nudges the stuck
ones. The step is `fugaro update` — the whole upgrade, and the only command this
cutover needs (D2729).

## Run the update (**per installation**)

```bash
fugaro update
```

At your terminal it installs the login unit that runs the project's
orchestrator daemon on this machine if there is none, re-renders an installed
one for the binary this release ships, migrates the project's published policy
in place and rebuilds the job images (D2700). Nothing else is a step: there is
no dump to import, no script to stop by hand, and nothing to keep on disk for a
window of days. A run in flight when the update runs keeps its records, its
ledger and its counters, and the daemon adopts it: an adopted run is folded and
counted like any other (D2702).

Every machine runs the update, and one of them holds the project's daemon lease
and drives the runs while the others stand by and take it over when it is free.
`fugaro orchestrator status` says what this machine has: whether that login unit
is installed, the file it is written at, and whether the file is what this
release would write. `fugaro orchestrator uninstall` removes it, which is how
one machine leaves the rotation without touching the project.

## The switch

Whether a daemon launches anything at all is the project's own answer, in
`orchestrator_enabled`, a top-level key of its published policy: true for a
project that never states it, and a `false` that reaches every machine's daemon
(D2701, D2739). Read the answer that stands with
`fugaro settings orchestrator_enabled --json`.

While it is false the daemons launch nothing and go on folding the runs already
going, so a project that wants the work driven by hand sets the key and lets the
runs in flight finish. The key is the node's own answer to keep: `fugaro project
policy publish` carries the policy and keeps this key as the node has it, so a
machine whose config predates it cannot turn a project's daemon back on.

## What a session does (**per repository**)

A session in a checkout whose project has a live daemon hands the work over
rather than launching it: one item for one piece of work, or a plan imported
whole with `fugaro orchestrator import --plan docs/<plan>`, and it approves
neither — a person's word is what the daemon launches on. With the switch off,
or with nobody holding the project's lease, sessions drive the runs as before.

Whether a daemon is live is the project's answer and not this machine's: the
daemon serves the project and one machine holds its lease, so a machine whose
unit is not installed is out of the rotation rather than a project without a
daemon. `fugaro orchestrator ls` is where a session reads it — the project's
holder and the time of its last beat, and a beat inside the lease's ninety
seconds is a daemon driving the project on whichever machine that is.
