# Cutting over to the orchestrator daemon

The daemon replaces the sweep, the Monitor watcher and the queue scripts in one
step, with no shadow week (D1235): from the day you start it, it is what
launches queued work, marks finished runs and nudges the stuck ones. No step
here deletes the scripts — they stay on disk — and the rollback below is
reachable for **14 days**.

## 1. Import what the session tracked (**per installation**)

Once, and before the daemon starts, hand it the session's memory: everything
the interactive session holds, written out as one patch document, and then

```bash
fugaro orchestrator import -f session-dump.json
```

The dump is read by `fugaro orchestrator patch`'s own validator and bounded by
its own caps, because it is the one document in this loop a model wrote. It
lands as you, and no dump approves work: approving a work item, setting the goal
and closing a decision that waits on a person are your own word at a real
terminal, so a dump carrying one is refused whole, whether the decision was
already in the node or the dump raised it itself in the same document. Every
work item it declares arrives unapproved, and the daemon launches approved work
only, so approve what should run before you start it, with
`fugaro orchestrator patch -f approved.json`.

## 2. Stop the scripts launching (**per installation**)

Nothing but the daemon takes its launch claim — the claim is per run id, and the
scripts take none — so a script and the daemon launching the same work would
launch it twice. Stop whatever starts runs from `queue.txt`: the sweep, the
watcher's launcher and the queue scripts. Their watching may stay.

## 3. Start it and see one run through (**per installation**)

Test the budget's spend caps and the kill switch first, then

```bash
fugaro orchestrator status
fugaro orchestrator run
```

`fugaro setup` installed the login unit; on a machine that skipped it, or after
a repair, `fugaro orchestrator install` writes it. Confirm one finished run, one
dependency-driven launch and one notification (Check 302 of
[the live checklist](../gcp-live-checklist.md), run at your own installation).

## The rollback, by hand (**per installation**)

**No rollback command is built.** The scripts are still on disk, they stay there
for the 14 days, and inside that window the rollback is these three steps and
they are the whole of it:

1. Stop the daemon: `fugaro orchestrator uninstall`.
2. Re-arm the sweep, the Monitor watcher and the queue scripts.
3. See one launch through the scripts before removing anything.

That is the way back while the window lasts; nothing else was moved, and the
scripts are what the machine runs from again.

## After the 14 days (**per installation**)

Nothing removes the scripts for you. Once the fortnight has passed with no
rollback, delete them yourself; until then, keep their launching stopped.
