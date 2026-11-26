# Required-version stamp: reconciliation

Checked against `origin/main` at `0dec0bb36` on 2026-11-23. The spec was checked at `b8fdb53e2`; every
claim below was re-checked at `0dec0bb36`. Paths and symbols are cited, never lines. **Where this
document disagrees with [spec.md](spec.md), it wins.**

The spec is lift L1 of the repo-coupling audit ([report §6 and §7](../repo-coupling-audit/report.md),
D3233, D3238). It is one capability (a stamp, its writer, its reader and the places that report it),
so it was reconciled without a capability map.

## 1. Assumptions

- A1. "A project operation" is any command that runs with a selected project, whatever it does. The
  spec's fenced-by-default rule (every command with a selected project, except a named exempt list)
  is the definition, not a list of writers.
- A2. "The selected project" is the project the command itself acts on: its own `--config`,
  `--project` and `--gcp-project`, then the checkout, `$FUGARO_PROJECT` and `$FUGARO_CONFIG`, as
  `selectProject` resolves them (F5, F6).
- A3. `Version` is compared as a plain `X.Y.Z`. The stamp stores it without a `v` prefix, though
  `updatecheck.Valid` accepts one.
- A4. The stamp is per installation: one RTDB, one node. A workspace does not carry one for its
  members.
- A5. `FUGARO_NO_UPDATE_CHECK=1` and a project config's `update_check: false` switch off the release
  nag only. They do not switch off the stamp, which has no override (spec: "no override flag").
- A6. "Before anything L8 migrates" means L8's migration is a later stage in `initflow`'s `order`.
  L8's own spec places it (O1).

## 2. The spec against the repository

| Spec concept | What the repository has |
| --- | --- |
| The only refusal of an older CLI | `projectpolicy.VersionError` and `CLISkew` (`internal/projectpolicy/version.go`), only on commands that read the policy node |
| Store beside `config/policy/shared` | `projectpolicy.PathShared` (`internal/projectpolicy/paths.go`); the rules template's root is `".read": false, ".write": false` and `config/policy` has no child that would match `required_cli` (`internal/budget/rules/rules.json.tmpl`) |
| CLI reads RTDB as the person | `newBudgetClient` (`internal/cli/cloud.go`), ADC with the budget scopes |
| Admin writes, everyone reads over IAM | `local.admins` get `roles/firebasedatabase.admin`, `local.readers` get `roles/firebasedatabase.viewer` (`deploy/terraform/gcp/modules/firebase/iam.tf`) |
| Firestore's `fugaro_version` cannot rise | `FirestoreMark.fields`, written with `firestore.MustNotExist()` (`internal/infra/firestore.go`) |
| Compare-and-set | `rtdb.Client.GetETag`, `rtdb.Client.PutIfMatch` (`internal/rtdb/client.go`) |
| The root hook | `newCoreRoot`'s `PersistentPreRun` (`internal/cli/root.go`); no other command defines one |
| Release parsing, brew command, cache directory | `updatecheck.Valid`, `updatecheck.Newer`, `updatecheck.BrewCommand`, `updatecheck.cachePath` over `localcfg.UserDir(..., localcfg.DirCache)` |
| Error path and exit code | `PrintFailure` (`internal/cli/safetext.go`), `ExitUserError` (`internal/cli/root.go`) |
| Stage order and doors | `initflow.order`, `runCovered` (`internal/initflow/stage.go`); `initStages`, `stagesNamed` (`internal/cli/init_stages.go`); `updateDoor` (`internal/cli/update.go`), `setupDoor` (`internal/cli/setup.go`) |
| The older-rules precedent | `infra.DB.Plan` (`internal/infra/firebasedb.go`) refuses a rules marker above `projectpolicy.RulesVersion` |
| Runs in flight | `announceRunsInFlight` (`internal/cli/init_stages.go`), `TestUpdateLeavesARunInFlightRunning` (`internal/cli/update_in_flight_test.go`), `docs/design/update-in-flight.md` |
| The daemon's hold | `orchestratorDaemon.updateDue`, `holdForUpdate` (`internal/cli/orchestrator_run.go`) |
| Console handshake | `classifyCliVersion` (`console/src/shared/version.ts`) |
| Doctor | `cliVersionCheck`, `--offline` (`internal/cli/doctor.go`) |
| Plugin | `plugin/skills/working/SKILL.md`; `plugin/skills/update/SKILL.md` never runs `brew` |

## 3. Findings

- **F1 (Exists).** Every store and IAM claim holds: `config/policy/required_cli` has no rules grant,
  so only IAM reaches it (admins write, readers read); `newBudgetClient` reads as the person;
  Firestore's mark is create-once. R2 of the audit stands. Two service accounts also hold
  `roles/firebasedatabase.admin` and so could write the node: the history job's account
  (`history_database`) and, where the coordinator is on, its writer (`coordinator_database`), both in
  `deploy/terraform/gcp/modules/firebase/iam.tf`. Neither one's code writes `/config`, so the stamp is
  no new exposure, and T7's hand-edit recovery covers a wrong value from any writer. The spec's "admins
  only" means the people with the role. *Consequence:* no rules or Terraform change.
- **F2 (Wrong, mechanism).** "`initflow.Run` does not run a stage whose needs failed." `needs` gates
  nothing; it only records why `order` is what it is. Gating is linear: a stage runs only when every
  registered stage before it is done or skipped, except the `pastStops` tolerance
  (`internal/initflow/stage.go`, the comment above `order`; `blocker` in `internal/initflow/loop.go`). *Consequence:*
  stronger than the spec says. Any earlier stage that fails, blocks or stops for the user leaves the
  stamp unchanged. So the slot decides which stages must have converged first (R1).
- **F3 (Rule).** The spec puts the stage after `Policy`. The slot right after `Policy` is taken by
  contract: `CredentialsService` needs `Policy` "and nothing else in the run is between them", and
  `CredentialsCopy` comes "immediately AFTER" that (`internal/initflow/stage.go`, both constants'
  comments, and the order's own comments). *Consequence:* R1 puts `version-stamp` right after
  `credentials-copy`, and ahead of `workspace-project`, the next slot, which is a Terraform grant in
  the workspace's own project.
- **F4 (Wrong).** "It joins `runCovered`." `runCovered` is the set of stages the plan-review screen
  confirms (`initEngine.todo`, `internal/cli/init_review.go`). These are mostly Terraform applies.
  `TestRefusalMatrixListsEveryStageTheCLIRegisters` (`internal/cli/setup_refusals_test.go`) pins
  that set. The database-write stages (`policy`, `workspace`, `credentials-route`) are not in it.
  They are `SelfConfirming` and confirm through `confirmStageWrite`, which asks nothing at the
  owner's terminal (D1540; `routePublication.confirm`, `internal/cli/update_route.go`). *Consequence:*
  R2.
- **F5 (Gap).** The root hook's project is not the command's project. `selectedProjectConfigReadOnly`
  resolves with an empty `cloudOptions{}` (`internal/cli/project.go`). So it ignores the command's
  own `--config`, `--project` and `--gcp-project` (`addCloudFlags`, `internal/cli/cloud.go`), and
  `fugaro run --project B` in a checkout of A would check A's stamp. The hook reaches it only through
  `updateCheckDisabled`, which skips the lookup under `FUGARO_NO_UPDATE_CHECK=1`. *Consequence:* R3.
- **F6 (Gap).** A member with no local project config is not fenced. `selectProject` alone falls back
  to the shared config published to the runs bucket (`cloudOptions.sharedOK`), and the cloud
  commands (`newCloudEnv` and `openBudgetDB` in `internal/cli/cloud.go`) go through it. The pre-run's
  read-only resolver never does, so for that member it finds no project and skips the check.
  `fugaro run` is among these commands. *Consequence:* R4.
- **F7 (Gap).** `consoleReadPaths` (`internal/cli/project.go`) is keyed by command path and holds
  `fugaro apply`. Its comment says the console's argv is `apply --status` but that the bare
  `fugaro apply`, which runs saved stages through `stagesNamed`, is covered by the same key. If every
  console read path is exempt, a write arm is exempt too. *Consequence:* R5.
- **F8 (Gap).** The exempt list misses in-cloud entry points that `backend.OnPlatform` does not claim.
  `OnPlatform` recognises Cloud Run jobs and Batch tasks (`DetectPlatform`'s codecs,
  `internal/backend/codec.go`). It does not recognise Cloud Run services or Cloud Build steps. The
  spec names `credentials serve`, `orchestrator serve`, `voice serve`, `intake serve` and `history`.
  It misses:
  - `coordinator serve` (`ee/coordinator/cli/coordinator_serve.go`);
  - the Cloud Build steps `image gate`, `image record` and `image git-credential`
    (`internal/cli/image_record.go`, `internal/cli/gitcred.go`);
  - `image selftest` (`internal/cli/image.go`), which runs inside a freshly built image.

  `updateCheckExcluded` keys by `cmd.Name()`, which the console's table already found too coarse.
  *Consequence:* R6.
- **F9 (Gap).** A root refusal of `fugaro orchestrator run` would loop. The login unit restarts the
  daemon after any exit (KeepAlive and Restart=always). `holdForUpdate` exists because an exit is "the
  same old binary started again … for ever" (`internal/cli/orchestrator_run.go`). Also, `updateDue`
  runs once at `start`, not at every tick as the spec says. *Consequence:* R7.
- **F10 (Wrong, minor).** `updateDoor` and `setupDoor` are in `internal/cli/update.go` and
  `internal/cli/setup.go`, not `init_stages.go`. `update`'s flag is `--plan-only`, not `--plan`
  (`internal/cli/init.go`). `--check` makes no cloud call at all, so it cannot read the stamp either.
  *Consequence:* R8 names the modes.
- **F11 (Rule).** `update` writes before its first stage. The image catalog guard can rewrite the
  local config. `rewriteLocalBudgetMode` rewrites it. `syncAccess` recomputes the access lists
  (`initEngine.converge`, `internal/cli/init_stages.go`). The precedent the spec cites, `DB.Plan`,
  refuses inside the Firebase stage, after the installation stage has already applied. *Consequence:*
  R8 puts `update`'s own check at the door, before all of these.
- **F12 (Wrong).** "In a coding agent's session it is skipped like every other cloud write." On a
  managed project, the gate opens some stages in a session and the loop applies them there:
  `gatedStages` (`internal/cli/update_managed.go`), `loop.agentStage`/`gatedStage`
  (`internal/initflow/loop.go`). Those stages are installation, Firebase, installation-2,
  repository, `rules`, `policy`, `credentials-service` and `workspace`. Fugaro serves only managed
  projects (D2217). So whether the stamp is one of them decides two things:
  - whether an agent's `fugaro update` raises it;
  - whether every later stage is blocked behind it in that session, because gating is linear (F2).

  *Consequence:* D3267 (answered: gated).
- **F13 (Rule, docs are tested).** `docs/design/update-in-flight.md` holds one row per step. Line 62
  is cited by line in `internal/cli/docs_security_test.go`, `docs_bitbucket_runbook_test.go` and
  `docs_project_wide_git_test.go`. *Consequence:* the stamp's row goes after the policy rows, below
  line 62, so the cited line does not move (T2b).
- **F14 (Exists).** The comparison and the words exist:
  - `updatecheck.Newer(stamp, Version)` is "stamp above this binary";
  - `updatecheck.Valid` rejects `dev` and pre-releases;
  - `updatecheck.BrewCommand` is the command `CLISkew` names;
  - `projectpolicy.MarkerError` is the malformed-marker precedent.

  *Consequence:* the new package wraps these and writes no semver code of its own.
- **F15 (Exists).** `fugaro project policy publish --delete` removes `config/policy/shared` and the
  `config/policy/repos` children it wrote, never `config/policy` itself (`internal/cli/project_cmd.go`,
  `projectpolicy.PathRepos`). No writer replaces `config` or `config/policy` whole. *Consequence:*
  acceptance 8 holds by construction; one test pins it (T2b).
- **F16 (Rule).** Every RTDB node other binaries read has a strict decoder and a JSON Schema in
  `schemas/`, with a test that holds the two together (`schemas/credential-route.schema.json` and
  `internal/creds/route_test.go`). This node is read by every later CLI release (CLAUDE.md: "a field
  never changes meaning"). *Consequence:* the stamp package's task (T1) publishes `schemas/required-cli.schema.json`.
- **F17 (Gap).** `doctor --offline` is "the session-start check the skills run". It answers the update
  class from this machine with no cloud call (`internal/cli/doctor.go`). Skipping `cli-below-required`
  there hides the fence from the skill. The stamp cache is a file on this machine. *Consequence:* R9.
- **F18 (Gap).** CI pins. Release notes tell operators to upgrade "every CLI and CI pin"
  (`docs/releases/README.md`, `docs/releases/v0.6.0.md`). With D3260, every admin update raises the
  stamp, so a CI job that runs an older pinned CLI with a selected project is refused until the pin
  moves, and there is no override. A CI job with no local config selects no project and is not
  fenced. *Consequence:* the release notes say so (T8). Which CI jobs select a project is U4.
- **F19 (Exists).** `fugaro --version` and a leading `-v` are answered before any pre-run
  (`NormalizeVersionArgs`, `internal/cli/root.go`). On a subcommand, `-v` is `--verbose`, so the
  spec's "`--version`/`-v`" means the leading form only. *Consequence:* none.

Findings added by the first review round, each checked against `0dec0bb36`:

- **F20 (Gap, amends D3267).** A run can get past a stage whose work was not done. A stage may
  answer `Skipped` with `Undone: true` (`initflow.Status.Undone`), and the loop goes on, because
  `Skipped` counts as converged for linear gating (F2). The policy stage does this when the node is
  held: `policyMigrateStage.Check` returns `Skipped` and `Undone` when `credentialsMoveHeld` holds
  the node back (`internal/cli/init_policy_migrate.go`). The credentials-service stage skips in a
  coding agent's session with `credentialsSessionHeld` (`internal/cli/init_stages.go`). So a gated
  session could raise the stamp while the policy node and the credentials service stay at the
  release behind. Only the policy stage answers `Skipped` with `Undone` before the slot (`git grep
  'Undone: *true' internal/cli`; the workspace stage's comes after it). Its three cases are: no
  database, a database that is not this installation's, and a node held by `credentialsMoveHeld`.
  The stamp stage's own adoption check (F22) covers the first two. *Consequence:* D3267 is amended.
  The stamp stage holds, with a named line, while `credentialsMoveHeld` holds the policy node below
  `projectpolicy.Version`, or while the credentials service's recorded image is not this release's.
  The second test is `servicesImageStale`, the one `credentialsResolveBeforeRaise` uses. These are the
  engine's own facts, so the loop needs no change. In a coding agent's session, `credentialsMoveHeld`
  answers "held" on every installation whose tfvars carry a credentials block. So in practice the
  owner's own `update` raises the stamp, and an agent session raises it only when nothing is held.
- **F21 (Gap).** A `gatedStages` entry alone admits nothing. Each gated database write judges its own
  document in a `gateStep` and writes an audit record before it writes: `rulesStage.gateStep`,
  `policyMigrateStage.gateStep` and `workspaceStage.gateStep` (`internal/cli/update_managed.go`).
  *Consequence:* the stamp stage gets `versionStampStage.gateStep`. Its document is the exact bytes
  `PutIfMatch` writes, under the ETag that was read. Its admitted rule names the binary's own
  `Version`. The admit, with its audit record, comes before the write (T2b).
- **F22 (Rule).** A stage that writes the budget database first asks whether that database is this
  installation's: `budgetDBUnadopted` (`internal/cli/init_rules_stage.go`). The rules stage skips
  with its sentence when it is not (D1017). *Consequence:* the stamp stage's `Check` does the same
  (T2a).
- **F23 (Gap, availability).** The stamp only rises, and nothing lowers it (D3262). That makes a bad
  value permanent: a build stamped with `-X` with a version that was never released, or a mistaken
  cached value. *Consequence:* D3268 bounds both ways in, and D3269 makes the release bound a live
  check.
- **F24 (Gap).** `update`'s `RunE` (`newInitCmdWith`, `internal/cli/init.go`) dispatches `--check`
  first. Then it runs `--repo`, or the default `runInit`. Inside `runInit`, `--print-vars`,
  `--config-only` and `--forget` each take their own path, and `--forget` rolls Terraform state back.
  A check at the converge alone would miss `--forget` and `--config-only`. *Consequence:* R8 moves the
  check to the top of `RunE`.
- **F25 (Gap).** `fugaro budget kill` stops spend, in all its forms. `--workspace`
  (`runBudgetKillWorkspace`, `internal/cli/budget_kill_workspace.go`) resolves its own project and
  refuses `--config`, `--project` and `--gcp-project`. Fencing a command whose whole purpose is to
  stop money is the wrong way round. *Consequence:* D3270.

## 4. Decisions

The owner answered the spec's open questions Q1–Q7 on 2026-11-23. Each was asked on its own, with
the recommendation first.

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D3260 | (Q1) What does `update` write: its own release, or a per-release floor compiled into the binary? | Its own release: a hand-kept floor would go stale, and a missed bump means no fence | **2026-11-23, as recommended.** Every member upgrades after every admin update (and every CI pin, F18) |
| D3261 | (Q2) CLIs released before L1 cannot be fenced by the stamp. How does L8 handle them? | L8 ships at least one release after L1, and L8's new project keys bump `projectpolicy.Version`, the one fence every older CLI obeys (`CLISkew`) | **2026-11-23, not the recommendation: accept the gap.** CLIs released before L1 stay unfenced; the runner and the cap clamp still decide what a run does. Followed through: L1 bumps no `projectpolicy.Version` for this purpose, and no task here does; L8's own spec decides its release timing. The gap covers a pre-L1 *admin* too: a pre-L1 `update` does not know the stamp, does not refuse, and can re-apply its own images over a newer installation, where only the existing fences (`DB.Plan`'s rules marker, the policy node's `projectpolicy.VersionError`) still refuse |
| D3262 | (Q3) Is there ever a way to lower the stamp? | None for now; add one when a yank happens | **2026-11-23, as recommended** |
| D3263 | (Q4) How long may a cached stamp be trusted without a read? | 15 minutes | **2026-11-23, as recommended** |
| D3264 | (Q5) Do read failures fail open? | Yes, except a cached value above the binary and a malformed stamp, which refuse | **2026-11-23, as recommended.** D3268 bounds the cached-value exception |
| D3265 | (Q6) Are console read paths exempt? | Exempt, with one stderr line | **2026-11-23, as recommended.** R5 keeps `apply`'s write arm out of the exemption |
| D3266 | (Q7) Does an `update` below the stamp refuse, or run and not lower it? | Refuse before its first write | **2026-11-23, as recommended.** R8 places it |
| D3267 | In a coding agent's session on a managed project, does `update` raise the stamp (F12)? | **Yes: `version-stamp` joins `gatedStages`.** Its document is the binary's own `Version`, the same kind of document as the `rules` stage's, which the gate already opens: "a document the binary carries, so the plan IS the generation". The policy node moves in that session; if the stamp did not, members would stay unfenced until the owner's own terminal ran `update`. Because gating is linear (F2), a stop at this stage would also block every later stage in the session: `workspace`, `credentials-route`, `layer`, `checkout`, `unit` and `compute` | The `credentials-route` model: the stage refuses in a session (`AgentError`), and the owner's terminal raises it. Its cost: every release's agent-session `update` ends with a step left for the owner, every later stage stays blocked behind it until then, and members stay unfenced in between. Both options write nothing a run holds | **2026-11-23, as recommended: gated like `rules`. Amended after review (F20, F21) and confirmed by the owner on 2026-11-23: "keep it, raise when converged".** The gate entry, the stage's `gateStep` and its audit record stay. The stage raises only once the installation has converged. It holds, with a named line, while `credentialsMoveHeld` holds the policy node, or while `servicesImageStale` says the credentials service's recorded image is not this release's. In a session, `credentialsMoveHeld` holds on every installation with a credentials block, so in practice the owner's own `update` raises the stamp; an agent session's raise happens only when nothing is held |
| D3268 | The stamp only rises (D3262). How is a bad value kept out (F23)? | **Bound it.** `update` writes only a published release: a `Version` that `updatecheck.Valid` accepts and that is no newer than the newest release tag `updatecheck` can see. It never writes a `dev` build or one stamped with `-X` for a release that does not exist; it leaves the stamp unchanged with a line instead. A cached value above the binary refuses on its own only while it is younger than 15 minutes. Past that, only a re-read that confirms it refuses, and a re-read that fails passes with the stderr line. D3262 stands: there is still no command that lowers the stamp. The last-resort recovery is a hand edit of the node in the Firebase console, documented in T7 | Leave it unbounded: a mistaken value stays until someone hand-edits the node, and every member is refused meanwhile | **2026-11-23, as recommended ("Bound it").** Owner's ruling on the availability risk the first review raised. D3269 replaces "no newer than the newest release tag" with a live check that the tag `v<Version>` is a published release |
| D3269 | How does `update` know its `Version` is a published release (D3268)? | **A live check:** the release tag `v<Version>` must exist as a published release that is neither a draft nor a pre-release, read from the same releases API `updatecheck` uses. The check ignores the update-check switches (`FUGARO_NO_UPDATE_CHECK`, `update_check: false`) and the update-check cache. When the lookup fails, the stamp stays unchanged and a line says why. An older-line hotfix (a 0.27.x published after 0.28.0) passes, because the question is about the tag, not about the newest release | Reuse the update-check cache and its "newest release" answer: no extra call, but it is switched off by the very switches a CI or an offline owner sets, and it wrongly refuses an older-line hotfix | **2026-11-23, as recommended** |
| D3270 | Is `fugaro budget kill` fenced (F25)? | **Exempt, in all its forms, `--workspace` included:** stopping spend must always work | Fenced like any writer: a member below the stamp could not stop a runaway run | **2026-11-23, as recommended** |
| D3271 | (O7) Does `update`'s door read get a longer timeout than every command's 2 seconds? | **No: keep 2 seconds and decide after Check 900.** There is no new parameter. Check 900 measures whether a cold read, token refresh included, fits (U1); if it does not, a follow-up raises the timeout. A failed read falls back to the cache rule | 10 seconds for `update` only, at the cost of a new parameter and up to 10 seconds of waiting before `update`'s first line when the database is unreachable; or 10 seconds everywhere, so every project command can stall | **2026-11-23, as recommended.** Closes O7. Check 900 ran 2026-11-26: the cold read fit (284 ms), so 2 seconds stands |

## 5. Resolutions (one defensible answer, stated rather than asked)

- **R1. The slot.** `version-stamp` comes right after `credentials-copy` in `initflow`'s `order`,
  with `needs: Policy, Rules` recorded (F2, F3). The earlier stages through the credentials copy must
  be done or skipped before it runs, and F20's hold covers the ones that skipped with their work
  undone. The stages after the slot are `workspace-project` (a Terraform grant in the workspace's own
  project), the workspace record, the credentials route, the layer, the checkout, the unit and
  compute. None of them is part of what a member's CLI must match for "this installation is at
  release R". L8's migration comes after the slot (O1).
- **R2. The confirmation.** The stage is `SelfConfirming` and confirms through `confirmStageWrite`,
  as `credentials-route` does. It asks nothing at the owner's terminal (D1540). It is not added to
  `runCovered` (F4). The refusal matrix gets its row: `self: true`, `covered: false`, `noYes: false`.
- **R3. The command's own project.** The pre-run check resolves the selected project from the
  command's own `--config`, `--project` and `--gcp-project` flags, which cobra has parsed by then, and
  from the environment. It never remembers a checkout (`resolveCheckout`, as now). It does not depend
  on `updateCheckDisabled` (A5).
- **R4. The shared-config fallback.** Where the pre-run finds no local project, `selectProject`'s
  shared-config branch runs the same check once it has the shared config. One function, two call
  sites (F6). `selectProject` has no `cmd`, so the pre-run records its verdict in the context:
  fenced, exempt, console read, or on a platform. The shared-config site checks only on an explicit
  "fenced" verdict, and it evaluates `backend.OnPlatform` itself. A context with no verdict, such as a
  test calling the resolver directly, is not checked. So `doctor` and `runs ls` on the shared config
  pass below the stamp, and the console read prints its stderr line.
- **R5. Console read paths.** The exemption is `consoleReadPaths` minus the write arm of `apply`:
  `fugaro apply --status` passes with the stderr line, and a bare `fugaro apply` is fenced (F7). A test
  lists every `consoleReadPaths` entry that has a writing arm. The table entry for `fugaro apply`
  carries a flag predicate: it is exempt only when `cmd.Flags().GetBool("status")` is true, since
  `--status` is a `BoolVar` in `internal/cli/apply.go`. So `apply --status=false` is fenced.
- **R6. Exemption by command path.** Every exemption is keyed by command path in one table. That
  table holds every hidden in-cloud entry point: the services, the Cloud Build steps, `image
  selftest` and `exec`. `OnPlatform` stays as a second gate. The tree-walk test (acceptance 2) fails
  on any command that is neither checked nor in the table (F8). An entry is a command path with an
  optional predicate over the parsed flags. Only `apply`'s entry has one (R5).
- **R7. The daemon.** `fugaro orchestrator run` is exempt from the root refusal. Instead, the daemon
  reads the stamp at `start` and at every tick (through the cache, so at most one read per 15
  minutes). When the stamp is above its binary, it takes the `updateDue` path: it prints why, then
  `holdForUpdate` under a unit. Under `--once` or with no `--binary`, it exits 0, because
  `holdForUpdate` returns nil there, as it does for `updateDue` (F9). When the stamp rises mid-run,
  the daemon stops claiming, releases its lease the way `abandon` does, and serves nothing more. It
  then holds under a unit, or exits 0.
- **R8. Where `update` refuses.** The check is at the top of the door's `RunE`, shared by `update`,
  `update --repo` and `setup` (`newInitCmdWith`). It sits after the `--check` arm and is skipped for
  `--print-vars`, the two modes that make no cloud call. So `--forget`, `--config-only`,
  `--publish-config` and the converge are all refused below the stamp (F24). It comes before the
  image catalog guard, `syncAccess`, `rewriteLocalBudgetMode` and the first stage (F11), and reads the
  stamp fresh, not from the cache. It also runs under
  `--plan-only`, because a plan from a binary below the installation is the downgrade the refusal
  stops, and refusing costs nothing. It does not run under `--check`, which makes no cloud call
  (F10) and is answered earlier in `RunE` anyway. A `dev` or pre-release binary skips it (spec, "Dev
  builds").
- **R9. Doctor offline.** `cli-below-required` answers from the stamp cache under `--offline` when
  there is one. That is this machine's file, so there is no cloud call. Without a cache it is skipped
  with a reason (F17).
- **R10. One package, built first.** The path, the type, strict decoding, the schema, the
  comparison, the refusal sentence and the cached, fail-open reader are one package,
  `internal/versionstamp`, with the schema beside it (F14, F16). It is built first, as one task with
  no stub. Every reader and the writer consume it. The `X.Y.Z` stored has no `v` (A3). The plan's
  contract-first pass breaks no edge here. A contract for the reader would be most of the reader, and
  the graph tool cannot hold the reader's side behind such a contract, because every other side waits
  on it.

## 6. Open tensions and open items

No open tension.

- **O1 (closed 2026-11-23, confirmed by the owner).** L8's spec places its migration stage after `version-stamp` in `initflow`'s `order`, and
  depends on `initflow.VersionStamp` (A6). With D3261, L8 still waits on L1 (D3238): the stamp fences
  every CLI from L1's release on, and the CLIs before it stay with the runner and the cap clamp.
  L8's release A is cut at least one release after L1's (L8's D3300).
- **O2.** Closed 2026-11-23: the owner answered D3267 (gated, as recommended).
- **O3.** `docs/status.md` gets this spec's row when the plan merges; this pull request does not
  touch it.
- **O4.** Closed 2026-11-23: the owner confirmed the amended D3267 ("keep it, raise when
  converged").
- **O5.** Which stamp a workspace-scoped write should check. `budget kill --workspace` is exempt
  (D3270). The other workspace writes (`workspace member`, `cap`, `share`, `unshare`, `add`, `rm`)
  run with a selected installation, so the fence checks that installation's stamp. They write the
  workspace's own database, whose project is an installation with a stamp of its own, and no reader
  of that stamp exists here. Whether a workspace write should also check the workspace's stamp is
  left for the workspace work's own spec. Nothing in this plan checks it.
- **O6.** U3 (whether the console treats one stderr line on its reads as a failure) is the console
  owner's. This plan changes no console code, so no task here tests it.
- **O7 (closed 2026-11-23 by D3271; the Check 900 reading of 2026-11-26 keeps it: the cold read fit
  in 284 ms, so 2 seconds stands).** Should `update`'s door read have a longer timeout than the 2 seconds every
  command's read has? T4's fresh read uses T1's timeout. When it fails, it falls back to the cache
  rule, so a cached value above the binary that is younger than 15 minutes still refuses. A longer
  door timeout would be a new parameter, so it is the owner's call.
- **O8.** A later tightening: the in-cloud exemptions (`credentials serve`, `image gate` and the
  rest, R6) are keyed by command path, so they also pass when typed on a laptop. They are hidden
  commands with no project write of their own. Gating them on a platform signal as well
  (`K_SERVICE` for a Cloud Run service, the Cloud Build environment for a build step) is left for
  later.

## 7. UNVERIFIED

- **U1.** Whether a cold-cache read of the stamp, including ADC's token refresh, fits in the 2-second
  timeout on a real machine. **Settled by Check 900 on 2026-11-26: PASS** — the owner's run on
  `fugaro` with 0.29.5 measured the cold read at 284 ms, well under `ReadTimeout`.
- **U2.** Whether a principal holding only `roles/firebasedatabase.viewer` reads
  `config/policy/required_cli` over REST under the rules' `".read": false` root. The CLI's other IAM
  reads of `config/` suggest it does, but Check 892 found no member on `fugaro`, and the owner's run
  of Check 900 on 2026-11-26 was run as the owner. Settled by Check 900 run as a member, on a
  project that has one.
- **U3.** Whether the console marks a read failed when the CLI prints one line on stderr and its JSON
  on stdout. The console does not change here (spec), so this is for the console's owner: a unit test
  in `console/test/unit` over `spawn.ts` settles it.
- **U4.** Which CI pipelines run a pinned `fugaro` with a selected project (F18). Only the owner knows
  their CI.
