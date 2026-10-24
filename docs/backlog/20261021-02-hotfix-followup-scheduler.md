---
id: 20261021-02
title: Nothing reacts to a merge, so a Hotfix follow-up is run by hand
mode: none
area: repo
status: open
filed-by: dimipaun
filed-in: hand
date: 2026-10-21
---

## What was left open

A Hotfix run records and prints a ready-to-paste follow-up task file and
**builds no scheduler**: no event source reacts to the merge that ends the
hotfix, so the follow-up that revisits the fix at the area's normal mode runs
only when a person runs it.

## Why

A trigger that reacts to a merge is new infrastructure with its own
credentials and its own failure mode — a scheduler that stops is invisible —
and it should not ride in on the first slice of a mode. The requirement the
spec states is satisfied in substance by a task file that exists the moment
the run ends and is trivial to run; what is missing is only the trigger
(D15).

## What done looks like

An event source that reacts to a merge — or to a ready Hotfix pull request
that has sat unmerged past a bound, which is O4's open question — starts the
follow-up at the area's normal mode. The follow-up's record names the original
Hotfix, so a regression trace points at it, and the follow-up runs at the
area's normal mode and can never be lowered (D15, D16).

## Links

- [reconciliation](../rigor-modes/reconciliation.md) D15, O4.
- [implementation plan](../rigor-modes/implementation-plan.md) T5.5, the task
  file half that ships without it.
