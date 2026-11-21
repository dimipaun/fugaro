# The workspace's credentials service: finish deploying it

Owner: Dimi Paun. Date: 2026-11-19. This brief was written by the coordinator from the owner's
rulings of 2026-11-19 in [workspace credentials](../workspace-credentials/spec.md) (D2960–D2979); it
is the spec this folder reconciles against the code.

## What to build

Finish deploying the workspace's credentials service (D2968).

- The Cloud Run service in the workspace's project. None exists:
  `deploy/terraform/gcp/roots/workspace/main.tf` creates only an account, a role and a grant.
- Wire `credsvc.NewWorkspace`, `WorkspaceShared` and `NewWorkspaceMeter`, which have no caller
  outside tests today.
- A writer and a command for the `workspace_credentials` node: the member table and the shared
  allow-lists. None exists.
- The cap meter wired to a refusal.
- Which credential a run picks when the member and the workspace both hold one (V5 of the workspaces
  design): the member's own copy wins (D2973).
- One uniform cascade for every credential kind, Bitbucket and GitHub included: the project's copy
  first, the workspace's as the fallback, with the workspace's allow-list still deciding on the
  fallback (D2978).
- The service that holds a credential proxies its traffic and never hands the value to a member's
  service (D2979).
- An `all_members` key for a shared entry, with a version bump of the node and a guard for an older
  service, and only once the meter is wired (D2977).

## The owner's constraints

- One code path with the project's service (`internal/credsvc`). A project's service is the
  one-member case. No second implementation of a route.
- A numbered live load check at a realistic count of concurrent runs, with explicit instance and
  concurrency limits.
- The plan states what a member's run does when the workspace's service is down, and keeps the
  service warm as Check 511 does.
- Every project keeps its own service (the ruling of 2026-11-05), and a project with no credential of
  its own never wakes it.
- `fugaro update` is the whole upgrade, and it is safe with runs in flight.

## Out of scope

- A person's own tokens at the workspace (D2969 puts them after the threat model, WU0).
- Moving provider keys off job mounts (D2975): its own spec, behind Check 50.
