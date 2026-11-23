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
  Firestore's mark is create-once. R2 of the audit stands. *Consequence:* no rules or Terraform change.
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
  `credentials-copy`.
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
  never changes meaning"). *Consequence:* the contract task publishes `schemas/required-cli.schema.json`.
- **F17 (Gap).** `doctor --offline` is "the session-start check the skills run". It answers the update
  class from this machine with no cloud call (`internal/cli/doctor.go`). Skipping `cli-below-required`
  there hides the fence from the skill. The stamp cache is a file on this machine. *Consequence:* R9.
- **F18 (Gap).** CI pins. Release notes tell operators to upgrade "every CLI and CI pin"
  (`docs/releases/README.md`, `docs/releases/v0.6.0.md`). With D3260, every admin update raises the
  stamp, so a CI job that runs an older pinned CLI with a selected project is refused until the pin
  moves, and there is no override. A CI job with no local config selects no project and is not
  fenced. *Consequence:* the release notes say so (I1). Which CI jobs select a project is U4.
- **F19 (Exists).** `fugaro --version` and a leading `-v` are answered before any pre-run
  (`NormalizeVersionArgs`, `internal/cli/root.go`). On a subcommand, `-v` is `--verbose`, so the
  spec's "`--version`/`-v`" means the leading form only. *Consequence:* none.

## 4. Decisions

The owner answered the spec's open questions Q1–Q7 on 2026-11-23. Each was asked on its own, with
the recommendation first.

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D3260 | (Q1) What does `update` write: its own release, or a per-release floor compiled into the binary? | Its own release: a hand-kept floor would go stale, and a missed bump means no fence | **2026-11-23, as recommended.** Every member upgrades after every admin update (and every CI pin, F18) |
| D3261 | (Q2) CLIs released before L1 cannot be fenced by the stamp. How does L8 handle them? | L8 ships at least one release after L1, and L8's new project keys bump `projectpolicy.Version`, the one fence every older CLI obeys (`CLISkew`) | **2026-11-23, not the recommendation: accept the gap.** CLIs released before L1 stay unfenced; the runner and the cap clamp still decide what a run does. Followed through: L1 bumps no `projectpolicy.Version` for this purpose, and no task here does; L8's own spec decides its release timing. The gap covers a pre-L1 *admin* too: a pre-L1 `update` does not know the stamp, does not refuse, and can re-apply its own images over a newer installation, where only the existing fences (`DB.Plan`'s rules marker, the policy node's `projectpolicy.VersionError`) still refuse |
| D3262 | (Q3) Is there ever a way to lower the stamp? | None for now; add one when a yank happens | **2026-11-23, as recommended** |
| D3263 | (Q4) How long may a cached stamp be trusted without a read? | 15 minutes | **2026-11-23, as recommended** |
| D3264 | (Q5) Do read failures fail open? | Yes, except a cached value above the binary and a malformed stamp, which refuse | **2026-11-23, as recommended** |
| D3265 | (Q6) Are console read paths exempt? | Exempt, with one stderr line | **2026-11-23, as recommended.** R5 keeps `apply`'s write arm out of the exemption |
| D3266 | (Q7) Does an `update` below the stamp refuse, or run and not lower it? | Refuse before its first write | **2026-11-23, as recommended.** R8 places it |
| D3267 | In a coding agent's session on a managed project, does `update` raise the stamp (F12)? | **Yes: `version-stamp` joins `gatedStages`.** Its document is the binary's own `Version`, the same kind of document as the `rules` stage's, which the gate already opens: "a document the binary carries, so the plan IS the generation". The policy node moves in that session; if the stamp did not, members would stay unfenced until the owner's own terminal ran `update`. Because gating is linear (F2), a stop at this stage would also block every later stage in the session: `workspace`, `credentials-route`, `layer`, `checkout`, `unit` and `compute` | The `credentials-route` model: the stage refuses in a session (`AgentError`), and the owner's terminal raises it. Its cost: every release's agent-session `update` ends with a step left for the owner, every later stage stays blocked behind it until then, and members stay unfenced in between. Both options write nothing a run holds | **2026-11-23, as recommended: gated like `rules`.** The stamp is a document the binary carries, so an agent's `update` raises it with the policy node it already moves, and no later stage is left blocked behind it |

## 5. Resolutions (one defensible answer, stated rather than asked)

- **R1. The slot.** `version-stamp` comes right after `credentials-copy` in `initflow`'s `order`,
  with `needs: Policy, Rules` recorded (F2, F3). The earlier stages through the credentials copy must
  be done or skipped before it runs. Every later stage is a member-visible document or this machine's
  own files, and none of them is what "this installation is at release R" depends on. L8's migration
  comes after it (O1).
- **R2. The confirmation.** The stage is `SelfConfirming` and confirms through `confirmStageWrite`,
  as `credentials-route` does. It asks nothing at the owner's terminal (D1540). It is not added to
  `runCovered` (F4). The refusal matrix gets its row: `self: true`, `covered: false`, `noYes: false`.
- **R3. The command's own project.** The pre-run check resolves the selected project from the
  command's own `--config`, `--project` and `--gcp-project` flags, which cobra has parsed by then, and
  from the environment. It never remembers a checkout (`resolveCheckout`, as now). It does not depend
  on `updateCheckDisabled` (A5).
- **R4. The shared-config fallback.** Where the pre-run finds no local project, `selectProject`'s
  shared-config branch runs the same check once it has the shared config. One function, two call
  sites (F6).
- **R5. Console read paths.** The exemption is `consoleReadPaths` minus the write arm of `apply`:
  `fugaro apply --status` passes with the stderr line, and a bare `fugaro apply` is fenced (F7). A test
  lists every `consoleReadPaths` entry that has a writing arm.
- **R6. Exemption by command path.** Every exemption is keyed by command path in one table. That
  table holds every hidden in-cloud entry point: the services, the Cloud Build steps, `image
  selftest` and `exec`. `OnPlatform` stays as a second gate. The tree-walk test (acceptance 2) fails
  on any command that is neither checked nor in the table (F8).
- **R7. The daemon.** `fugaro orchestrator run` is exempt from the root refusal. Instead, the daemon
  reads the stamp at `start` and at every tick (through the cache, so at most one read per 15
  minutes). When the stamp is above its binary, it takes the `updateDue` path: it prints why, then
  `holdForUpdate` under a unit, or exits under `--once` or with no `--binary` (F9).
- **R8. Where `update` refuses.** The check is at the converge door, shared by `update`, `update
  --repo` and `setup`. It comes before the image catalog guard, `syncAccess`, `rewriteLocalBudgetMode`
  and the first stage (F11), and reads the stamp fresh, not from the cache. It also runs under
  `--plan-only`, because a plan from a binary below the installation is the downgrade the refusal
  stops, and refusing costs nothing. It does not run under `--check`, which makes no cloud call
  (F10). A `dev` or pre-release binary skips it (spec, "Dev builds").
- **R9. Doctor offline.** `cli-below-required` answers from the stamp cache under `--offline` when
  there is one. That is this machine's file, so there is no cloud call. Without a cache it is skipped
  with a reason (F17).
- **R10. The node's shape is a contract.** The path, the type, strict decoding, the schema, the
  comparison and the refusal sentence are one small package, `internal/versionstamp`, with the schema
  beside it (F14, F16). Every reader and the writer consume it. The `X.Y.Z` stored has no `v` (A3).

## 6. Open tensions and open items

No open tension.

- **O1.** L8's spec places its migration stage after `version-stamp` in `initflow`'s `order`, and
  depends on `initflow.VersionStamp` (A6). With D3261, L8 still waits on L1 (D3238): the stamp fences
  every CLI from L1's release on, and the CLIs before it stay with the runner and the cap clamp.
- **O2.** Closed 2026-11-23: the owner answered D3267 (gated, as recommended).
- **O3.** `docs/status.md` gets this spec's row when the plan merges; this pull request does not
  touch it.

## 7. UNVERIFIED

- **U1.** Whether a cold-cache read of the stamp, including ADC's token refresh, fits in the 2-second
  timeout on a real machine. Settled by Check 900, run by the owner.
- **U2.** Whether a principal holding only `roles/firebasedatabase.viewer` reads
  `config/policy/required_cli` over REST under the rules' `".read": false` root. The CLI's other IAM
  reads of `config/` suggest it does, but Check 892 found no member on `fugaro`. Settled by Check 900
  run as a member, on a project that has one.
- **U3.** Whether the console marks a read failed when the CLI prints one line on stderr and its JSON
  on stdout. The console does not change here (spec), so this is for the console's owner: a unit test
  in `console/test/unit` over `spawn.ts` settles it.
- **U4.** Which CI pipelines run a pinned `fugaro` with a selected project (F18). Only the owner knows
  their CI.
