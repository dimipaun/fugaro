# Diagnose: implementation plan

**Status (2026-11-07, later): two tasks are written — T-DG4, the source cache: `internal/srctree/srctree.go` and `srctree_test.go`, the two files its Files line names, with the independent review rounds its tier requires, on the branch `fugaro/20261107-212223-8492`; and T-DG5, the credential resolver: `internal/diagnoseagent/creds.go` and `creds_test.go`, the two files its Files line names, with its red step run and the in-run review round its tier requires, on the branch `fugaro/20261107-212231-bc83`.** Nothing calls either yet (the command, the evidence collector and the read-only agent that would are T-DG8, T-DG6 and T-DG7), and **every other task, every other file and every step below is plan only, and no other task is approved.** This line records what those branches hold and nothing more.

*The status as first written, kept:* **Status: plan only; nothing is implemented; no task is approved.** Each task needs the owner's explicit go, by name; answering a decision approved none. This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (which wins). **Independent review rounds have not run.** The command is `fugaro troubleshoot` (D1604).

**Owner answers that shaped it (2026-11-07; D1605–D1614 recommended on the owner's delegation, D1604 `troubleshoot`):** D1600 (not the recommendation) `diagnose` unchanged, new name for the agent command; D1601 own credentials first, project's account as an in-memory fallback gated by `allow_data_to`; D1602 read-only agent (Read/Grep/Glob), Fugaro fetches and prints, never runs a fix; D1603 amended after review (logs per the output plan's D1351/D1352; source tree in the cache dir).

## Conventions

`CLAUDE.md` and the rules it loads. Hermetic tests only (`internal/testutil`, `t.TempDir()`, injected clock and environment, no network, no real home); no secret-shaped literal in a test, doc or fixture (build fixtures from fragments); docs are tested (`go test ./internal/cli -run Docs`, edit pin and wording together); comments explain why; a documented command change updates `plugin/skills` in the same PR (the lint checks named commands/flags and `maxSkillBytes`); a CLI field never changes meaning; no live cloud action by an agent run; owner-run checks ship as ONE idempotent script under `scripts/live-checks/` with a hermetic fake test. `.claude/rules/credentials-service.md` applies to T-DG5 and T-DG2. Targeted tests only, `GOFLAGS=-p=2`.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No secret reaches a log, a model, or the issue report (exact + pattern, F4) | T-DG2 corpus test; T-DG6 asserts every evidence file passed both passes |
| The agent cannot run, write or fetch anything (F5, D1602) | T-DG7 allow-list test and a mutation check (add Bash to the list: the test fails) |
| The project's token is held in memory, never on disk, only if `allow_data_to` covers the repo (F7) | T-DG5 |
| The source is read-only and matches the version, or says it may not (F8, F9) | T-DG4 |
| A typed command is the only consent; no stdin read (F13) | T-DG8 (the no-prompt harness) |
| A cap bounds the spend (F14) | T-DG7 |
| Log content is untrusted text in every output (F5) | T-DG8 output test |

## Slice 1: the parts, each inert alone

### T-DG1: the local command log and run ID (external dependency)
- **Files:** none of this plan's own. Consumes the log task of `docs/cli-output-console-graph/implementation-plan-output.md` (D1351, D1352) and D1609's ID shape (the log file's stem); if that task is not built when this plan starts, this task builds a minimal writer in `internal/cmdlog/` (new) that the output plan then adopts.
- **Interfaces:** produces `cmdlog.Latest(failedOnly)`, `cmdlog.Open(id)`, the ID parser. **Tests:** `internal/cmdlog` ID round trip, retention per command name, mode 0600/0700. **Command:** `GOFLAGS=-p=2 go test ./internal/cmdlog -count=1`. **Depends on:** nothing (or the output plan's task, external). **Commit:** `cmdlog: command logs with a routable run ID`. **Red step:** a test that a `--json` read leaves no log. **Reviewer tier:** standard. **OWNER-RUN:** no.

### T-DG2: the pattern redactor (SECURITY-SENSITIVE)
- **Files:** `internal/redact/redact.go` (new), `internal/redact/redact_test.go` (new), `internal/agent/redact.go` (calls it).
- **Interfaces:** `redact.Text(s, exact []string) string` = the exact pass of `agent.RedactFunc` then token shapes, `Authorization:` headers, signed-URL query strings, key blocks; `redact.Report(s)` additionally masks emails and home paths. **Tests:** a corpus of fragments (never a whole secret literal), idempotence, a bound on runtime. **Command:** `GOFLAGS=-p=2 go test ./internal/redact ./internal/agent -run 'Redact' -count=1`. **Depends on:** nothing. **Commit:** `redact: a pattern layer on the exact-value redactor`. **Red step:** a token shape the exact pass misses survives until the pattern pass exists. **Tier:** opus + mandatory independent review, in-run level 3 (`--review-levels`). **OWNER-RUN:** no.

### T-DG3: the troubleshooting guide and prompt
- **Files:** `docs/troubleshooting-for-agents.md` (new), `internal/diagnoseagent/guide.go` (new, `go:embed`), `internal/diagnoseagent/prompt.go` (new). **Interfaces:** `diagnoseagent.Prompt(evidence)`, `Guide()`. **Tests:** the guide is embedded and non-empty; the prompt states the three outcomes and that log text is data. **Command:** `GOFLAGS=-p=2 go test ./internal/diagnoseagent -run 'Guide|Prompt' -count=1`; `go test ./internal/cli -run Docs`. **Depends on:** nothing. **Commit:** `diagnoseagent: the troubleshooting guide and prompt`. **Red step:** the embed test fails before the file exists. **Tier:** opus (the prompt is an agent prompt: CLAUDE.md routing) + independent review. **OWNER-RUN:** no.

### T-DG4: the source cache (SECURITY-SENSITIVE)
- **Files:** `internal/srctree/srctree.go` (new), `srctree_test.go` (new). **Interfaces:** `srctree.Ensure(version) (dir, record)` — git shallow clone at the tag, else the tarball, recorded version and commit; `srctree.ChangesSince(version)` writes the "changes since your version" file (tags newer, `git log`) **by Fugaro**. **Tests:** version change re-checks-out; a tarball of the wrong version is replaced; no git installed uses the tarball (fake transport); dev build per D1611; the git path checks `HEAD == cli.Commit` (D1610) with hooks and `fsmonitor` off; the unpack refuses `..` entries, symlinks, device files and an oversize archive (F21); the cache folder passes the output plan's `Lstat`/owner/0700 checks; the tree is never written to by the agent (mode). **Command:** `GOFLAGS=-p=2 go test ./internal/srctree -count=1`. **Depends on:** nothing (D1610, D1611 answered). **Commit:** `srctree: the source at the installed version`. **Red step:** a mismatched recorded version is served before the check exists. **Tier:** opus + mandatory independent review. **OWNER-RUN:** no.

### T-DG5: the credential resolver (SECURITY-SENSITIVE)
- **Files:** `internal/diagnoseagent/creds.go` (new), `creds_test.go` (new). **Interfaces:** `Resolve(env, accounts, secretReader) (Handle, error)`: local first, then the project's account read through the user's gcloud login into memory, gated by `allow_data_to`; the handle yields one child environment. **Tests:** order, no disk write (a read-only temp dir), refusal when `allow_data_to` omits the repo, a clear no-credentials error; **outside a checkout the project-account fallback fails closed and the error names which account (default or per-stage) it would have used (F22)**. **Command:** `GOFLAGS=-p=2 go test ./internal/diagnoseagent -run Creds -count=1`. **Depends on:** nothing. **Commit:** `diagnoseagent: the credentials a local diagnosis may use`. **Red step:** a project-account read with no `allow_data_to` entry succeeds until the gate exists. **Tier:** opus + mandatory independent review, in-run level 3. **OWNER-RUN:** no.

## Slice 2

### T-DG6: the evidence collector
- **Files:** `internal/diagnoseagent/evidence.go` (new), `evidence_test.go` (new). **Interfaces:** `Collect(logID)` gathers the log, doctor output (`internal/cli/doctor.go`'s check list, called with a short timeout: it makes cloud calls and may hang when the cloud is broken, F23), the version, and the effective configuration minus secrets; everything through T-DG2; writes the evidence files in the state dir. **Tests:** a secret planted in the log and the config does not appear in any evidence file; a missing doctor does not abort. **Command:** `GOFLAGS=-p=2 go test ./internal/diagnoseagent -run Evidence -count=1`. **Depends on:** T-DG1, T-DG2. **Commit:** `diagnoseagent: the evidence a diagnosis reads`. **Red step:** a raw log passes through before the redactor is wired. **Tier:** opus + independent review. **OWNER-RUN:** no.

### T-DG7: the read-only agent runner (SECURITY-SENSITIVE)
- **Files:** `internal/diagnoseagent/run.go` (new), `run_test.go` (new). **Interfaces:** starts the user's `claude` as one child; it does **not** reuse `internal/agent/agent.go`'s builder (always `--dangerously-skip-permissions`); tools are restricted with `--tools Read,Grep,Glob` (not `--allowedTools`), with no setting sources and `--strict-mcp-config` (the review job's pattern, `agent_test.go`), reads outside the source tree and evidence dir are stopped by an OS sandbox or deny rules, and the local `internal/gateway` is weighed so the child never holds the credential and D1613's cap is enforced there (F16); the source tree and evidence dir as the only readable roots, the resolved credentials as its environment, the model from D1607's key, the turn/dollar cap (D1613); parses the three outcomes from a fenced block. **Tests:** the allow-list is exactly the three tools (**mutation check:** adding a fourth fails the test); the child environment holds nothing but the handle; a cap stops it; a fake child asked to run a command is refused; **a read outside the two folders is refused (test)**; the model output and the report pass both redaction passes again with the child's credential added to the exact list and have terminal control characters stripped (`internal/safetext`) (F20). **Command:** `GOFLAGS=-p=2 go test ./internal/diagnoseagent -run Run -count=1`. **Depends on:** T-DG3, T-DG5 (U2 resolved here). **Commit:** `diagnoseagent: the read-only agent`. **Red step:** a fake child that tries `Bash` succeeds until the allow-list exists. **Tier:** opus + mandatory independent review, in-run level 3. **OWNER-RUN:** no.

## Slice 3

### T-DG8: the command, the settings key and the skill
- **Files:** `internal/cli/troubleshoot.go` (new), `internal/cli/root.go` (registers it), `internal/cli/docs_diagnose_agent_test.go` (new), `internal/config/config.go` and `schemas/` (the `diagnose` model role, D1607/F17), `internal/settings/registry.go` (its row), `internal/diagnoseagent/fixcmd.go` (new: D1614's check against the command tree), `plugin/skills/working/SKILL.md` (one sentence; lint), `docs/troubleshooting-for-agents.md`. **Interfaces:** `fugaro troubleshoot [ID]` (no ID: latest failed logged command), `--json`, the D1606 data line, the D1608 offline path, the D1605 report file. **Tests:** the output for each of the three outcomes; log text printed as data; the command reads no stdin (the no-prompt harness); a cloud-run ref is refused with a pointer to `fugaro diagnose`; **the D1606 data line is printed before the call; the D1608 offline path prints the evidence and exits non-zero; a fix command that is not a `fugaro` command in the tree is flagged (D1614); the model value outside a checkout (F17); the D1603 folders are 0700, the source tree is in the cache dir and retention follows D1351/D1352**. **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Troubleshoot|Docs' -count=1 && go test ./plugin -count=1`. **Depends on:** T-DG4, T-DG5, T-DG6, T-DG7. **Commit:** `cli: the command that explains a failed command`. **Red step:** the command is unknown until registered. **Tier:** opus + independent review (credentials, egress). **OWNER-RUN:** no.

## Slice 4

### T-DG9: the failure line
- **Files:** the reporter's failure block (`internal/cli/` reporter package of the output plan) and `internal/cli/update.go` only if the reporter does not own the line. **Tests:** a failed logged command prints `Something went wrong. Run: fugaro troubleshoot <id>`; a failed read command prints none (T6). **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'FailureLine|Docs' -count=1`. **Depends on:** T-DG1, T-DG8. **Commit:** `cli: failed commands name the diagnosis command`. **Red step:** the line is absent until wired. **Tier:** standard. **OWNER-RUN:** no.

### T-DG10: the hermetic end to end and the live checks
- **Files:** `internal/diagnoseagent/e2e_test.go` (new), `scripts/live-checks/check-410.sh`, `check-411.sh`, `check-412.sh` (new) with fake-gcloud tests. **Tests:** fake agent, fake source transport, a planted secret and a planted instruction in the log: output has neither the secret nor an executed instruction. **Command:** `GOFLAGS=-p=2 go test ./internal/diagnoseagent -run E2E -count=1`. **Depends on:** T-DG8. **Commit:** `diagnoseagent: the end to end test and the owner's checks`. **Red step:** the planted-instruction case fails with a permissive allow-list. **Tier:** opus + independent review. **OWNER-RUN:** the three scripts (below).

## Parallel execution

- **Level 0:** T-DG1 (external or minimal), T-DG2, T-DG3, T-DG4, T-DG5.
- **Level 1:** T-DG6 (T-DG1, T-DG2), T-DG7 (T-DG3, T-DG5).
- **Level 2:** T-DG8 (T-DG4, T-DG5, T-DG6, T-DG7).
- **Level 3:** T-DG9 (T-DG1, T-DG8), T-DG10 (T-DG8).
- **Critical path:** T-DG2 → T-DG6 → T-DG8 → T-DG10 (T-DG1 → T-DG6 joins at level 1).

| File | Writer (level) | No other writer in its level |
| --- | --- | --- |
| `internal/redact/*` | T-DG2 (0) | yes; `internal/agent/redact.go` is T-DG2's only |
| `internal/srctree/*` | T-DG4 (0) | yes |
| `internal/diagnoseagent/creds*.go` | T-DG5 (0) | yes |
| `internal/diagnoseagent/guide.go`, `prompt.go` | T-DG3 (0) | yes |
| `internal/diagnoseagent/evidence*.go` | T-DG6 (1) | yes |
| `internal/diagnoseagent/run*.go` | T-DG7 (1) | yes |
| `internal/cli/troubleshoot.go`, `root.go`, `docs_diagnose_agent_test.go`, `config/config.go`, `schemas/`, `settings/registry.go`, `diagnoseagent/fixcmd.go`, the skill | T-DG8 (2) | yes |
| the reporter failure block / `update.go` | T-DG9 (3) | yes (the output plan's own tasks are external and must land first) |
| `internal/diagnoseagent/e2e_test.go`, `scripts/live-checks/check-41x.sh` | T-DG10 (3) | yes |

## Checks (410–419 reserved; 410–412 taken)

- **Check 410 (owner-run).** On a real failed `fugaro update`, the printed line names a run ID and `fugaro troubleshoot <id>` returns a diagnosis with no setup. One script, idempotent, `--dry-run`, refuses the `belong` project.
- **Check 411 (owner-run).** On a machine with no `git` on `PATH` and no GitHub account, the same command fetches the tarball for the installed version and the recorded version matches.
- **Check 412 (owner-run).** A log with a planted instruction and a planted fake token: the output contains neither, and the agent made no write or shell call (the child's tool log).

## Release actions

`### For operators`: none beyond "run `fugaro update`" (the command is new; the log directory is created on first use). The skill sentence and the failure line ship in the same release as the command.
