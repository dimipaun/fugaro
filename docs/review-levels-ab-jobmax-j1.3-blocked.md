# J1.3 — `jobmax.auto_launch` — is blocked: its two dependencies are not on `main`

**Nothing was implemented for this task.** J1.3's own `Depends on` line names **T1.1** and **T1.2**
("the same `localcfg.go`, `project_cmd.go` and contract"), and neither is merged: the owner-written
`settings` node T1.1 creates does not exist, and the `project show --json` document T1.2 extends has
no `settings` member. This note is the evidence. The task's instruction is to stop on an unmerged
dependency and say which, so this pull request carries the note and no code.

`docs/status.md` is untouched, as the plan's own rule has it: a task's pull request does not move its
row. The plan file is untouched for the same reason — this is not this task's line to rewrite.

## What J1.3's contract is made of

J1.3's **Interfaces** line (plan line 383) has three parts, and only the first is this task's own:

1. `jobmax: { auto_launch: false }` in the owner's project config, decoded strictly — a boolean,
   default `false`, any other key under `jobmax:` refused. This part needs nothing from T1.1/T1.2.
2. **It is published only by the owner, into the same owner-written `settings` node as the crash
   switch** (O9 as ruled), never through the launcher-writable shared configuration. The node, the
   publish path that writes it and the rules entry that lets only the owner write it are **T1.1's**
   (`internal/localcfg/localcfg.go`, `internal/cli/project_cmd.go`,
   `internal/budget/rules/rules.json.tmpl`).
3. **`fugaro project show --json` prints `settings.jobmax_auto_launch: true|false|null`**, `null`
   (unreadable) meaning off. The `settings` member of that document is **T1.2's** (the same
   `project_cmd.go`, `console/docs/json-contract.md`).

Parts 2 and 3 are the half J1.2's `/jobmax` skill reads to decide whether it may launch without
asking (plan lines 361–376), which is the whole point of the opt-in: a config key that nothing
publishes and nothing reads is not the permission D730 rules on. Building J1.3 without them would
mean inventing T1.1's node, rules entry and publish path and T1.2's contract member inside this
task's diff — those tasks' own deliverables, on those tasks' own files.

## Evidence, run on this branch's base `ab2735b4` (= `origin/main` at the tip)

### L1.4 — merged

`b957b57f`, *policy: the owner's review-level floor, owner-only and tighten-only (#1406)*:
`localcfg.Config.ReviewFloor` (`internal/localcfg/localcfg.go:634`) with its strict decoder, the
`ReviewFloorPolicy` bridge (`:2116`) and `TestReviewFloorDecodeStrict`
(`internal/localcfg/localcfg_test.go:1556`). An ancestor of the base.

### J1.1 — merged

`da056ce7`, *plugin: job max in the parallelism skill (#1413)*, the commit the plan's own `Done` line
for J1.1 (line 359) records. An ancestor of the base. J1.3 does not depend on it directly; J1.2 does.

### T1.1 — **not merged**

| The plan says T1.1 produces | state on `main` |
|---|---|
| `localcfg.Telemetry{CrashReports *bool}` | absent. `grep -rn "Telemetry" --include=*.go .` finds only `internal/cli/docs_mobile_telemetry_test.go`'s test names; nothing in `internal/localcfg/` |
| the `settings` node in the database rules | absent. `internal/budget/rules/rules.json.tmpl`'s top-level nodes are `fugaro`, `creds`, `coordinator`, `config`, `pairing`, `runs` and the rest of the budget tree; no `settings` entry |
| `crash_reports` anywhere in the tree | absent outside the plan and the reconciliation |
| `TestTelemetryCrashReportsDefaultTrue`, `TestTelemetryNotInShared`, `TestTelemetrySharedForbidden`, `TestPublishWritesSettings` | none exists |

`git log origin/main --oneline --grep="settings: the owner's crash-report switch"` — the plan's own
**Commit** line for T1.1 — prints nothing.

### T1.2 — **not merged**

| The plan says T1.2 produces | state on `main` |
|---|---|
| `settings: { crash_reports: true\|false\|null }` in `project show --json` | absent. `projectShowDoc` (`internal/cli/project_cmd.go:2872`) carries `project`, `gcp_project`, `mode`, `caps`, `policy`, `allow_repos`, `enrolled`, `running_now`, `running_not_enrolled`, `enrolled_not_running` — and no `settings` member |
| the same member in the contract | absent from `console/docs/json-contract.md` |
| `TestProjectShowSettings`, `TestProjectShowSettingsUnknownIsNull` | neither exists |

`git log origin/main --oneline --grep="project show: the installation's settings"` — T1.2's **Commit**
line — prints nothing.

## What unblocks this

**T1.1** (`settings: the owner's crash-report switch, owner-written and user-readable`) then **T1.2**
(`project show: the installation's settings`), both in slice T, both earlier in the plan's own wave
order (waves 1 and 2; J1.3 is wave 3, plan line 592). With the `settings` node published and read,
J1.3 is a small change on top of them: the `jobmax` block in `localcfg`, its `Shared()` denial beside
`Telemetry`'s, the owner's publish writing `jobmax_auto_launch` into the same node, and the
`settings.jobmax_auto_launch` member in `project show --json`, with the four tests its **Tests** line
names and the red step the plan sets (the unknown key `jobmax` refused).

## The one part that could stand alone, and why it should not

J1.3's **Red step** — "the unknown key `jobmax` is refused" — is satisfied by the `localcfg` decode
alone, which depends on nothing T1.1/T1.2 own. Landing only that would put a key in the owner's
project config that `fugaro project policy publish` silently does not publish and `project show
--json` silently does not print: an owner who set `jobmax.auto_launch: true` would get a config that
parses, a publish that drops it and a `/jobmax` skill that reads `null` and asks, with nothing
anywhere saying why. That is the failure the opt-in exists to prevent, so this run leaves the key
unwritten rather than half-wired.
