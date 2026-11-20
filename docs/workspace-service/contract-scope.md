# The scope contract, C1

**Status: implemented (C1).** This document is the contract C1 fixes in code
(`internal/credsvc/scope.go`, `internal/credsvc/scope_stub.go`); the plan it
belongs to is [implementation-plan.md](implementation-plan.md) §3, and what it
builds is [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md),
which wins where the two disagree.

## What it is

This service has two deployments and one code path (spec.md §1). R1 is the seam:
**a route does not read the deployment's configuration to learn who is calling
it and what that caller may be served — it asks the per-call scope.** The scope
carries the verified caller and the readers chosen for that caller; `ServeHTTP`
resolves one per request, refuses a route the scope does not serve, and hands it
to the handler through the request's context, so no handler signature changes.

- `Scope` — the caller, the policy, kill, state, outcome, table, offering,
  forge-credential and meter readers, whether the scope serves a route (R14) and
  whether it accepts a slip (R18).
- `ScopeResolver` — the front door's one question, `Resolve(w, r) (Scope, bool)`.
  It owns the reads that come before any route is dispatched (the two identities
  and the pairing) and writes the refusal itself when one of them fails. `ok` is
  false for a call it refused.
- `Caller` — the verified run identity, the canonical repository the pairing
  resolved, the job account row it paired with and the parsed request.
- `NewWorkspaceResolver(WorkspaceScopeConfig) (ScopeResolver, error)` — the
  workspace resolver's constructor. The signature is fixed here because WS5
  builds the workspace's service from it and WS2 implements the scope behind it.
- `KeepWarmPath` — the keep-warm route's path (R14), spelled once: the route
  carries no secret, so that a member's run has something to ask while its model
  calls are refused (F33).

## The two sides

| | an installation's scope (WS1) | a workspace's scope (WS2) |
| --- | --- | --- |
| caller | the deployment's own verifier and pairing | `Workspace.Verify` over the owner's member table |
| routes | every route of the service | the model routes, a run's forge routes and the keep-warm route (R14) |
| policy | the installation's published node | the member's own node, read from the member's database |
| state | the installation's state store | the workspace's own database, and no write on a member's (R8) |
| offering | the bundle | the workspace's shared entries, intersected with the member's policy (`Workspace.Shared`, D1298, D2980) |
| slips | accepted (DS1) | refused (R18) |
| meter | none | the workspace's own cap (R15) |

An installation's scope is today's behaviour and nothing else, which is pinned by
`TestInstallationScopeIsTodaysBehaviour`: a call it serves is answered exactly as
it was before this contract existed.

## What a scope is not

A scope is not a credential and holds no value of one: every reader answers
names, records and entries, and a bundle's values are reached only by the code
that attaches one to a call. It is also not an admission: a scope that serves a
route that a caller's own node does not admit is a scope whose offering and
policy readers refuse the call, and never one that invents an empty answer.

## The stub

`StubScopeResolver` answers a scope that serves no route and whose every reader
refuses — the answer a front door test needs, and the answer a route switched to
the scope gets before either deployment implements one. It is the only
implementation C1 ships.
