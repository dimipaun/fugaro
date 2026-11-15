# Fugaro Spec: Forge Integration via Labels

## Summary
Use forge labels on pull requests to link PRs to their spec and to show at a glance whether a Fugaro job is running on a PR or has failed. Labels are managed by the stateless orchestrator through the forge abstraction.

## Motivation
When browsing the PR list it is hard to tell which spec a PR belongs to and whether an agent job is currently working on it. Labels appear as coloured tags directly in the PR list and can be used as filters, making them the lightest way to surface this.

## Why labels, not projects
GitHub Projects are not shown in PR list rows (only in the sidebar and on the board) and live at user/org level, so one project per spec would create clutter. Labels are visible in the list and filterable. Projects may later be used for larger groupings such as releases or user-facing features.

## Labels
### Spec label
- Format: **spec:<spec-name>**.
- Applied when a PR is created for a spec's task.
- Filtering by this label lists all PRs for that spec.

### Job status labels
- **fugaro:running**: a Fugaro job is currently active on the PR.
- **fugaro:failed**: the most recent job on the PR failed.
- No success label: success is already signalled by the PR moving from draft to ready.
- Detailed phases (implementation, review, fix) remain in fugaro watch and are not mirrored to labels.

## Ownership: stateless orchestrator
The deterministic stateless orchestrator owns all label writes. It already knows when jobs start, finish, or die, and it removes this monitoring work (and its token cost) from the local agent.
- On job start: add fugaro:running, remove fugaro:failed.
- On job success: remove fugaro:running.
- On job failure: remove fugaro:running, add fugaro:failed.
- Reconciliation loop: periodically compare labels against actual job state and fix stale labels, e.g. fugaro:running left behind by a crashed job.

## Forge abstraction
- Add label operations (ensure label exists, add, remove, list on PR) to the forge interface as an optional capability.
- Forges that do not support PR labels report the capability as unsupported and all label operations become no-ops.
- GitHub and GitLab support labels. Bitbucket Cloud does not support PR labels and will skip this feature.

## Edge cases and API efficiency
- **State-aware, change-only writes**: the orchestrator keeps the expected label state per PR in memory and issues a forge call only when a label actually needs to change. No redundant add/remove calls.
- **Rate limits**: GitHub allows roughly 5,000 API requests per hour per token. Prefer webhooks for PR and label events over polling; when reconciling, fetch labels in bulk with one query per repo rather than one call per PR.
- **Stale fugaro:failed**: if a human pushes a fix, the failed label can linger. Clear it on the next relevant event (new push or new job), per the open question below.
- **Human-managed labels**: Fugaro only ever touches labels in the fugaro: and spec: namespaces and never removes labels added by people.
- **Permissions**: fugaro setup pre-creates fugaro:running and fugaro:failed (with colours and descriptions) while it has elevated access. Spec labels are created on demand when the orchestrator first opens a PR for a spec (GitHub auto-creates a missing label when it is added, given repo write access). Spec labels are best-effort: the spec name is already in the PR title, so if creation fails, log once and continue without them.

## Out of scope
- Mirroring fine-grained workflow phases to labels.
- Using GitHub Projects.

## Open questions
- Label colours and descriptions.
- Whether fugaro:failed is cleared automatically when a human pushes a fix, or only on the next Fugaro job.
- Reconciliation interval.
