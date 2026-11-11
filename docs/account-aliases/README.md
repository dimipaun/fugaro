# Account aliases

**Status as of 2026-11-10: plan only, 0/5 merged; nothing is implemented; no task is approved; see [docs/status.md](../status.md) for the current table.** Spec owner: Dimi Paun. Date: 2026-11-10.

The owner's ruling, for CLI 0.25.0: *a stage's `claude_account` may name an alias, repointed by one
command; repointing affects NEW launches only, never runs in flight.* The design keeps the alias in
the owner's local config, resolves it inside the launch before the allow-list is asked, and publishes
only the resolved account, so the node, the task and the run record keep their formats and
`fugaro update` has nothing to migrate. One command, `fugaro accounts alias set ALIAS ACCOUNT`,
repoints it and converges the node and the job set; a mixed run launched before a repoint keeps the
account its launch resolved.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The ruling, quoted verbatim, made concrete: where an alias lives, its names and targets, which keys may name it, resolution, the command, the reads, the security reading |
| [reconciliation.md](reconciliation.md) | Findings F1–F16 against `d51ada6d5`, decisions D2070–D2082 (**all OPEN**), open items, UNVERIFIED. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks AL0–AL4, Check 550 (owner-run), levels, critical path, shared-files table, serialization against open pull requests. **Nothing in it is approved** |

**Numbers:** D2070–D2089 (D2070–D2082 written), Checks 550–554 (550 written). Neighbours:
[accounts-add](../accounts-add/README.md) (the choice keys, D873–D876),
[model-crafts](../model-crafts/README.md) (credentials, pools and crafts; MC4 #2252 and MC5 #2274 move the
same keys), [design/update-in-flight](../design/update-in-flight.md). **Independent review has not run.**
