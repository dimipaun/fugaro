---
id: 20261021-01
title: A run cannot file a backlog item into a repository other than its own
mode: none
area: project
status: open
filed-by: dimipaun
filed-in: hand
date: 2026-10-21
---

## What was left open

A run's installation token reaches exactly one repository, and the provider
contract's `PRSpec` names none, so a run working in another repository cannot
file an item into the project's designated backlog repository (D29). What ships
first is a **scope** rule rather than a cross-repo write: the designated
repository holds the items filed **in** it — by the owner by hand, or by a run
in that repository — and a run elsewhere files into its own repository with
`area:` naming the area.

## Why

The narrowing is a mint parameter rather than a permission:
`internal/gitprov/github/apptoken.go`'s `tokenSource.Token` mints with
`{"repositories": []string{s.repo}, "permissions": perms}`, while
`apprepos.go` mints one with a permissions map and no `repositories` list. So
it is liftable — at the price of a second token, a second checkout, a second
push and a second pull request. That is a task of its own rather than a line
inside a filing command, and it should be decided on its merits (F43, D29).

## What done looks like

A run in a repository other than the designated one files its items there: a
second installation-token mint without the single-repository narrowing, a
second checkout, a second push and a pull request in the designated
repository, with `filed-in:` naming the run that filed them. Until then the
designated repository is a scope rule, and D29's value — which repository that
is — is still the owner's to name.

## Links

- [reconciliation](../rigor-modes/reconciliation.md) F43, D29.
- [implementation plan](../rigor-modes/implementation-plan.md) T4.5, whose
  **Depends on** line names the same value.
