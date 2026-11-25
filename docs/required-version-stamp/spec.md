# Fugaro Required-Version Stamp Spec

Owner: Dimi Paun
Status: approved by the owner, 2026-11-23

Code claims are checked against `origin/main` at `b8fdb53e2`. They cite a path and a symbol, never a line.

## Purpose

After an admin runs `fugaro update`, a member on an older CLI can still write a repository's `fugaro.yaml` or the repo cap node, and nothing stops them. The only refusal of an older CLI today is the policy node's schema version (`projectpolicy.VersionError`, `CLISkew`). It fires only on commands that read the policy (repo-coupling audit F11, report §6).

The stamp is the fence. The project records the oldest CLI release that may act on it. A CLI below that release refuses project operations and names the upgrade. Lift L8 (migrate repo keys once, then reject them) needs this fence first (report §7).

## Guiding principles

- **The CLI only.** The console and the mobile app are not stamped (owner's spec). Nothing in a job obeys the stamp.
- **`fugaro update` is the whole upgrade.** Only `update` writes the stamp. A member needs nothing more than `brew upgrade` and `fugaro update`.
- **Fugaro asks for nothing.** The refusal is a printed sentence with the fix. There is no prompt and no override flag.
- **Safe with runs in flight.** The stamp changes nothing a run holds, and no run reads it.
- **A fence, not a boundary.** IAM is the boundary. The stamp stops honest drift. That is why it may fail open (see "Read failures").

## Scope

In scope: the node, its writer in `update`, the check in the CLI, the refusal, a doctor check, and one line in the plugin's `working` skill. Out of scope: the console's own version, the mobile app, the runner image, and fencing CLIs released before L1 (see Q2).

## What is stored, and where

- **Store: RTDB, at `config/policy/required_cli`, beside `config/policy/shared`** (reconciliation R2). The CLI already reads its config nodes from RTDB through `newBudgetClient` (`internal/cli/cloud.go`). Firestore holds rollups and the creation mark. Its `fugaro_version` field is written once, with `MustNotExist` (`internal/infra/firestore.go`, `FirestoreMark.fields`), so it cannot carry a value that rises.
- **Shape:** `{"schema": 1, "min": "0.29.0", "set_at": "<RFC 3339>"}`. `min` is a plain `X.Y.Z` (`updatecheck.Valid`). A CLI that reads a `schema` it does not know is behind by definition. It refuses with the upgrade sentence.
- **Who writes it:** admins only, over IAM. Admins hold `roles/firebasedatabase.admin`. Every person and viewer holds `roles/firebasedatabase.viewer` and can read it (`deploy/terraform/gcp/modules/firebase/iam.tf`, `local.admins`, `local.readers`). No rules change is needed. The template's root is `".read": false, ".write": false`, and no child rule grants this path (`internal/budget/rules/rules.json.tmpl`), so no client token reads it or writes it.

## When `update` raises it

- A new stage, `version-stamp`, in `internal/initflow`'s `order`, with `needs: Policy, Rules`. It runs in both doors (`updateDoor`, `setupDoor`; `internal/cli/init_stages.go`). It writes after the policy node is at this release. That is the moment the installation becomes this release, and it comes before anything L8 migrates.
- **Value:** the running binary's own `Version` (Q1). The write is a compare-and-set through `GetETag`/`PutIfMatch` (`internal/rtdb/client.go`), and it keeps the higher of the stored and the new value. **It never lowers the stamp.** No flag lowers it (Q3).
- **A failed or blocked earlier stage leaves the stamp unchanged.** `initflow.Run` does not run a stage whose needs failed. So members are never fenced off an installation that did not reach the release.
- The run's one confirmation covers it: it joins `runCovered`. It adds no prompt. In a coding agent's session it is skipped like every other cloud write. `--plan` and `--check` write nothing.
- Output is one brew-style line: `required CLI: 0.28.0 → 0.29.0 (members below it are refused until they upgrade)`. When the value is unchanged, the line says `current`.
- `fugaro apply` and `stagesNamed` subsets write it only when the stage is named.

## What the CLI checks, and when

- **Where:** the root hook becomes `PersistentPreRunE` (`internal/cli/root.go`, `newCoreRoot`). The report's other option was `cmd/fugaro/main.go`, but the root hook already has the command path and the selected project (`selectedProjectConfigReadOnly`, `internal/cli/project.go`). Its comment already forbids a subcommand hook that would replace it.
- **Which commands:** the check runs on every command that has a selected project, except an explicit exempt list. A new command is fenced by default. A command with no selected project (no local config, no RTDB URL) skips the check, because it cannot act on a project.
- **Exempt:**
  - `version`, and the `--version`/`-v` flag. Cobra answers the flag before any pre-run.
  - `help`, and the completion tree (`excludedCommand`, `inCompletionTree` in `internal/cli/updatewarn.go`).
  - `doctor`, which reports the stamp instead (below).
  - `update`, `setup` and `update-skills`.
  - The retired tombstone (`newRetiredInitCmd`, `internal/cli/retired.go`).
  - The console's read paths (`consoleReadPaths`). These pass and print one stderr line, never on stdout.
  - Every in-cloud process: `exec`, anything `backend.OnPlatform` claims, and the hidden service commands (`credentials serve`, `orchestrator serve`, `voice serve`, `intake serve`, `history`).
- **`update` checks for itself.** Before its first write, an `update` from a binary below the stamp refuses: "this installation was updated by fugaro X; upgrade first". The precedent is `internal/infra`'s `DB.Plan`, which already refuses to deploy older rules over a newer marker. Without this, an older admin's update would put older images and a re-rendered unit over a newer installation.
- **Cache:** one file per project's RTDB URL, under `localcfg.UserDir(..., DirCache)`, the place `updatecheck` keeps its cache (`internal/updatecheck/updatecheck.go`, `cachePath`). It stores the stamp value, not a verdict. Each command compares it with the running `Version`, so a fresh `brew upgrade` passes at once. A value no older than 15 minutes is used without a read (Q4). A cached value above this binary refuses without a read, because the stamp never falls.
- **Read failures fail open.** An unreachable database, a timeout (2 s), missing credentials or a denied read lets the command go on, with one stderr line that the stamp could not be read. The cached value still applies when there is one. The reasons:
  - The command that needs the cloud fails on its own anyway.
  - A local command offline should keep working.
  - Failing closed would turn every RTDB blip into a stop of all work, for a fence that is not a security boundary.
- **One case fails closed:** a stamp that is present but malformed (not `X.Y.Z`, or an unknown `schema`). No `update` writes one, so it is a hand edit or a newer release. The CLI refuses and names the node and `fugaro update`. The precedent is `projectpolicy.MarkerError` for a hand-edited rules marker.

## The refusal

One sentence on the error path every refusal takes (`PrintFailure`, `internal/cli/safetext.go`), exit code 1 (`ExitUserError`), and no prompt:

> this project requires fugaro 0.29.0 or newer (set by fugaro update on 2026-11-23) and this is 0.28.0, so nothing was done: upgrade with brew upgrade dimipaun/tap/fugaro, then run fugaro update

The brew command is `updatecheck.BrewCommand`, which is also the one in `CLISkew`. `--json` changes nothing: the error goes to stderr, as every refusal does.

## Runs in flight and jobs

- **The job's embedded CLI does not obey the stamp.** It is exempt through `exec`/`OnPlatform`. A run that started before the update keeps its image, records and writes (`docs/design/update-in-flight.md`, `TestUpdateLeavesARunInFlightRunning`). Image skew inside a job stays the job of `RunnerSkew` (`projectpolicy.SkewSentence`).
- **What is fenced is launching.** A member below the stamp cannot start a run, follow one up, cancel one or tell one anything. Runs already going are untouched.
- The stamp step reaches no run, so it adds no line to `announceRunsInFlight`.
- **A local orchestrator daemon** (`fugaro orchestrator run`) reads the stamp at every tick. When it is below the stamp, it holds the way `updateDue` already holds for a newer state node (`internal/cli/orchestrator_run.go`). It drives nothing and claims nothing until its binary moves on.

## Console and plugin

- **The console:** its handshake compares the CLI with the console's own floors (`classifyCliVersion`, `console/src/shared/version.ts`), not with the installation. That does not change. Its read paths stay exempt, so a dashboard keeps drawing. A write it spawns comes back with the refusal sentence, which the console shows as it shows any CLI error. The console's version is not enforced.
- **Doctor:** a new check, `cli-below-required` (warning, fix `brew upgrade …` then `fugaro update`), beside `cli-version` (`internal/cli/doctor.go`, `cliVersionCheck`). `--offline` skips it with a reason. Doctor is what the skill reads to notice that an update is due (CLAUDE.md).
- **The plugin:** the `working` skill names the refusal. The agent hands the user the two commands for their own terminal and never runs `brew` itself (the rule the `update` skill sets, `plugin/skills/update/SKILL.md`). The skill names only commands that exist, which `plugin/` tests already enforce.

## Mixed-CLI corner cases (audit §6)

| Case | Result |
| --- | --- |
| The admin upgraded; the member did not | The member is refused until `brew upgrade`. |
| The member is newer than the stamp | The member passes. Doctor's existing checks say an update is due. |
| Two admins on different CLIs | The newer admin's update raises the stamp. The older admin's update refuses before writing and never lowers it. |
| A CLI released before L1 | It never reads the stamp and is not fenced by it (Q2). |
| A member who upgraded to exactly the stamp | The member passes, because the stamp is a minimum. |
| A monorepo, or two projects | The stamp is per project (per RTDB), so the repository does not matter. |

## Dev builds

`Version` is `"dev"` for an unstamped build (`internal/cli/root.go`). A pre-release tag also fails `updatecheck.Valid`.
- **Such a CLI skips the check silently.** That is the stance `cliVersionCheck` already takes: it has nothing to compare.
- **Its `update` never writes the stamp.** Its line says the stamp was left unchanged because this is a development build.

## Acceptance criteria

1. An `update` from release R on an installation whose stamp is absent or below R leaves `min = R`. An `update` from an older release leaves it unchanged and refuses before its first write.
2. With the stamp above `Version`, `run`, `budget set`, `settings set` and a sample of every top-level command refuse with exit 1 and the sentence above. A test walks the whole command tree and fails on a command that is neither checked nor on the exempt list.
3. Every exempt command, `OnPlatform` and `exec` pass with the stamp above `Version`. Console read paths print the line on stderr and nothing on stdout.
4. An unreadable database passes with one stderr line. A cached value above `Version` refuses offline. A malformed stamp refuses and names the node.
5. A `dev` binary neither checks nor writes the stamp.
6. A failed `Policy` stage leaves the stamp unchanged.
7. `TestUpdateLeavesARunInFlightRunning` still passes, with a stamp raised mid-run.
8. `project policy publish --delete` leaves `required_cli` in place.
9. Doctor reports `cli-below-required`. The `working` skill names the refusal, and the `plugin/` tests pass.
10. All tests are hermetic, using `internal/gcpfake` and `t.TempDir()` for the cache.

## Non-goals

- No minimum console or mobile version.
- No stamp read inside jobs.
- No way to lower the stamp.
- No change to the policy or rules versions.

## Open questions for the owner

- **Q1. What does `update` write: its own release, or a per-release floor constant compiled into the binary?** Recommend its own release. Pre-1.0 has no backward compatibility, so any release may change a shape. A hand-kept floor constant is a fact nobody would keep right, and a missed bump means no fence. The cost is that every member upgrades after every admin update, which is one `brew upgrade`.
- **Q2. CLIs released before L1 cannot be fenced by the stamp. How does L8 handle them?** Recommend that L8 ship no earlier than one release after L1, and that L8's new project-level keys bump `projectpolicy.Version`. That bump is the one fence every older CLI already obeys at launch (`CLISkew`). L1 itself bumps nothing.
- **Q3. Is there ever an explicit way to lower the stamp (a yanked release)?** Recommend none for now. Pre-1.0 has no rollback path. Add one only when a yank happens.
- **Q4. How long may a cached stamp be trusted without a read?** Recommend 15 minutes. That is one RTDB GET per project per quarter hour. An old CLI keeps a 15-minute window after an admin's update, which L8's "reject at every reader" tolerates.
- **Q5. Do read failures fail open?** Recommend yes, except for a cached value above the binary and a malformed stamp, which refuse. The stamp is a fence, not a security boundary.
- **Q6. Should console read paths be fully exempt, or refused like the rest?** Recommend exempt with a stderr line (report §6). Refusing them would blank the dashboard that would show the person the problem.
- **Q7. Does an `update` from a binary below the stamp refuse, or run and just not lower it?** Recommend that it refuse before its first write. Otherwise an older admin re-applies older images and units over a newer installation.
