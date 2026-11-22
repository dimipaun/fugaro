# Console fixes: reconciliation

Checked against `main` at `dfe066618` on 2026-11-21. Where this document and `spec.md` disagree, this document wins. Decisions D3200–D3229 and Checks 870–879 are this spec's block (README).

## Capability map (D3200, approved 2026-11-21)

The spec bundles three capabilities that ship and are tested apart. Every finding, decision and task below names one of these module ids.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `run-index` | A Firestore index of runs and a per-project summary document; its one writer (the history job); the backfill inside `fugaro update` and `fugaro runs reindex`; the Go query layer every reader calls | — |
| `indexed-reads` | `runs ls`, `plan status` and `graph` read through `run-index` with cursor pages; the console's Graph and runs reads get fast with no new argv, plus one new section for merge state | `run-index` (its interface; broken by contract C-INDEX in the plan) |
| `coordinator-graph` | Named orchestrators, many per project; cross-orchestrator dependencies and task claims; the Coordinator tab redrawn as a graph of active orchestrators | — |
| `identity-display` | The console draws identity and every other non-secret value as received; only secrets stay masked | — |

Build order: `run-index`, `coordinator-graph` and `identity-display` start together; `indexed-reads` starts once contract C-INDEX lands, without waiting for the index to be built.

## Assumptions

- A1. "The console graph" is the console's **Graph** tab (`console/src/renderer/screens/graph.ts`). Its first paint is `fugaro plan status --json --all`, not `fugaro graph` (F1).
- A2. "Today's data" means the project with 3,500+ pull requests, the owner's own installation. "100 times" means about 350,000 runs and pull requests in one project.
- A3. "Sub-second" means wall time from the console's spawn to the section's first paint, on a warm machine (credentials cached), including the CLI process start. A cold first start after login is not covered.
- A4. A "project" is a Fugaro project: one GCP project with one Firebase project and one Firestore `(default)` database. So the index needs no project key inside its documents.
- A5. "Remove redaction from the console" covers the desktop console only. The mobile app (`shared/digest/redact.js`, D118/D119) is out of scope: it is a port of the secrets-only redactor anyway.
- A6. "Orchestrator" in §2 means the daemon in `internal/orchestrator` (state at `orchestrator/<id>/state` in the RTDB). It does not mean a person's Claude session. The answer to D3206 makes several of them per project possible.
- A7. The spec's "Firestore" is a requirement, not an example. RTDB keeps what it holds today (budget, live set, orchestrator state, coordinator views).

## What the spec names, and what the repository already has

| Spec concept | Already in the repository |
| --- | --- |
| Console graph data source ("unclear") | Graph tab: `plan status --json --all`, then `runs ls --json --since 24h`, then `graph --json --source all` only as a fallback; all CLI spawns (`console/src/renderer/entry.ts` `readGraph`, `decorateRuns`, `readScope`) |
| The 90 s timeout | `READ_TIMEOUT_MS = 90_000` in `console/src/main/spawn.ts`, the deadline of every one-shot spawn |
| Firestore | Used already: a REST client (`internal/firestore/client.go`, single-range `Query`, `Patch`, `Get`), collections `spendDaily`, `meta` (`internal/budget/rollover.go`), `workspaceAudit` and the key-service ones. No run, task or graph data in it |
| Per-project summary / counts | RTDB day counters and Firestore `spendDaily`, read by `runs summary` (`internal/cli/runs_summary.go`); the coordinator's stored per-repo view (`ee/coordinator/state.go`); no run or task counts document |
| An index | runs-ls-speed's GCS pull-request index (`internal/runstore/prindex.go`, `PRIndexPath`, D2961–D2964); the local `runcache` (`internal/runcache`) |
| Backfill command | Only `fugaro update`'s `initEngine.backfillPRIndex` (`internal/cli/update_prindex.go`), 90 days, idempotent |
| Cursor pagination | `runs ls --limit/--after` (D818, `pageRows`), but the whole window is still read |
| Lazy-load details | Exists: logs and diagnose load only when a run is opened (`entry.ts` `showRun`); there is no diff read in the console |
| Coordinator tab | `console/src/renderer/screens/coordinator.ts` (console-coordinator-v2): action queue, totals, per-repository panels, job list, intents transcript; organised by repository, with no notion of an orchestrator |
| Overlap | Per-repository collisions of in-flight runs on actually changed files and services (`ee/coordinator/view.go` `collisions`, `Collision`) |
| Coordinator health | None stored; the sweep is clock-free by design (`ee/coordinator/sweep.go`). Orchestrator leases beat every 30 s (`internal/orchestrator/lease.go` `LeaseTTL`, `LeaseBeat`) |
| Graph rendering | elkjs 0.12.1 in a worker (`console/src/renderer/graph/layout-client.ts`), SVG in `screens/graph.ts` |
| PII scrubber | `console/src/shared/sanitize.ts` `sanitizeText`, at display time only |

## Findings

### run-index and indexed-reads

- **F1 (Wrong).** The spec's premise that the graph's data source is unclear: it is `fugaro plan status --json --all` (`internal/cli/plan_status.go` `runPlanStatus`). It walks up to 64 local `*.graph.json` files, then `loadRows` over `followUpLookback` (90 days, `internal/cli/followup.go`). That is a GCS delimiter listing plus a flat listing per repository, up to 3 GETs per run not cached (`readRunVia`), one backend executions list plus one `checkExecution` per run missing from it (`followExecution`). Then `planEstimateTable`: when the local estimate cache is older than 6 hours, 2 more GETs per run, up to `planEstimateRuns = 5000`, with no run cache. Then up to `planMaxLookups = 32` pull-request merge lookups, two `gh` processes each (`planMerges`). The cost is about 3N to 5N GETs for N runs in 90 days. Linear in history, so the 90 s spawn deadline (`READ_TIMEOUT_MS`) is reached as history grows.
- **F2 (Wrong).** `runs ls` is linear in the window, not in the page: `runLs` → `loadRows` reads every run in `--since` (default `lsDefaultWindow`, 7 days, `lsWorkers = 32`), and `--limit`/`--after` cut only the printed rows (the help says "the window is still read"). runs-ls-speed measured 15 s for 991 rows at `--since 7d` and over 250 s at `--since 90d`.
- **F3 (Gap).** `fugaro graph` (`ee/graph/cli/graph.go` `readGraph`) is RTDB-only when the coordinator has stored views. But a repository with no stored view falls back to `coordinatorRuns` (`ee/coordinator/cli/coordinator.go`): a GCS listing over 7 days plus about 3 GETs per run with no run cache. The coordinator sweep is off by default, so the fallback is the common case.
- **F4 (Exists, partly).** Lazy loading of logs is already the console's behaviour, and the console rule "jobs are never part of the first paint" (`.claude/rules/console.md`, rule 5) already keeps `runs ls` off the first paint. The slowness is in what each read costs, not in when it is made.
- **F5 (Exists).** The console already shares the CLI's data layer: every console read is a CLI spawn through the allow-lists (`console/src/main/spawn.ts`, `CONSOLE_OPERATIONS` in `ipc.ts`). The spec's "share the data layer" is met by making the CLI's reads indexed. The console never reads Firestore itself (D3216).
- **F6 (Rule).** The spec asks for a backfill *command*. The hard rule in `CLAUDE.md` says `fugaro update` is the whole upgrade: an installation must reach the indexed state with no other step. So the backfill runs inside `update` (as D2965 did for the PR index), and `fugaro runs reindex` exists only for a rebuild (D3215).
- **F7 (Rule).** "Safe while runs are in flight" (`docs/design/update-in-flight.md`). The backfill and every index write must leave a running run's records alone. The index is derived data written beside them and never rewrites a run object.
- **F8 (Gap).** Trust boundary. Launchers and job service accounts hold `roles/datastore.viewer` only. `roles/datastore.user` is database-wide (`deploy/terraform/gcp/modules/firebase/iam.tf`): a job given it could rewrite `spendDaily` and the key-service collections. The history job (`deploy/terraform/gcp/modules/installation/history.tf`, a sweep every 15 minutes) already holds `datastore.user` and read on the runs bucket. Settled by D3201: the history job is the index's only writer.
- **F9 (Gap).** Freshness. A 15-minute writer leaves a run that finished minutes ago out of the index. The RTDB already holds the live set (`agents/`) and `outcomes/<day>` (`internal/budget/model.go`). Settled by D3202: each read adds what changed since the index's own "folded through" stamp, so the overlay is bounded by about 15 minutes of activity, not by history.
- **F10 (Gap).** The Firestore client cannot run the queries a page needs. `internal/firestore/client.go` `Query` takes one field and one range: no `orderBy` on a second field, no equality filter beside it, no `limit` or `startAfter` cursor. Composite indexes need `google_firestore_index` resources in Terraform, and `datastore.user` cannot change indexes (comment in `iam.tf`). So the indexes are applied by `fugaro update`'s Terraform as the operator, never by the job.
- **F11 (Gap).** Merge state of pull requests is not something the history job can know: it has no forge credential. `plan status` asks `gh` (at most 32 lookups). That cost is bounded, but it sits on the Graph tab's first paint. Settled by D3217: merge state becomes its own console section, read after the canvas.
- **F12 (Exists, superseded).** runs-ls-speed's gated RL4 (a per-run summary object in GCS) answers the same need with a layout GCS cannot page in time order. D3204: RL4 is superseded; the GCS PR index stays until `--pr` reads the Firestore index, and then its writers stop.
- **F13 (Gap).** "Status counts updated incrementally" at 100x cannot be recomputed by a scan on every sweep. With one writer (D3201), the sweep applies each run's state change as a delta to the summary document, and a rebuild recomputes the counts from the index documents. A writer lease keeps a reindex and a sweep from overlapping (D3215).
- **F14 (Open → D3205).** The spec's acceptance needs a measurement, and hermetic tests cannot time a live read. Settled by D3205: tests pin a constant request count per page at 1x and 100x, plus owner Checks 870 and 871.
- **F15 (Gap).** The estimate table (`planEstimateTable`, up to 5,000 runs × 2 GETs) is history-linear too. The summary document carries the estimate table the sweep keeps, so `plan status` reads one document.

### coordinator-graph

- **F16 (Rule → D3206).** One orchestrator per project is the current design. `orchestratorID` is the project's name (orchestrator-graph-estimates F2), one lease per id (`internal/orchestrator/lease.go`), and the 0.26.0 notes say "one machine per project drives runs". The spec draws several orchestrators as nodes. The owner chose to allow many per project (D3206, not the recommendation), which reshapes the orchestrator and amends orchestrator-graph-estimates F2 and the plugin's orchestrator skill (`plugin/skills/orchestrator/SKILL.md`, "the id is the project name").
- **F17 (Rule → D3208).** A dependency never crosses nodes today (`internal/orchestrator/shard.go`). The validator refuses a `depends_on` naming another node's item, and `ee/graph/source_orchestrator.go` drops cross-node edges (graph contract §6.22). The spec's solid edge needs this reversed (D3208).
- **F18 (Gap).** No driver is stored. `Lease.holder` is a run id (`task.NewRunID`). `Owner`/`Agent` (`internal/orchestrator/hooks.go`) are used only by `ee/orchestrator/handoff.go` and never persisted. The node needs a `driver` (the principal whose credential holds the lease: a person's account or a service account) written with the lease (D3218).
- **F19 (Gap).** Two orchestrators in one project could take the same plan task or backlog item; nothing claims one today. Settled by D3209: a project-wide claim per plan task.
- **F20 (Gap).** A cycle across orchestrators cannot be seen by any one node's validator. Settled by D3220: the tick detects it and blocks the items with an escalation.
- **F21 (Exists).** Overlap exists as per-repository collisions on files the runs actually changed (`ee/coordinator/view.go` `collisions`). Its records carry no orchestrator id, but each orchestrator's `WorkItem.run_id` maps a run to its orchestrator, so the join needs no new field on the run (D3211, D3222).
- **F22 (Rule).** The spec contradicts console-coordinator-v2 (status Done, owner Check C0 left). Its tab claims no age (`COORDINATOR_NO_AGE_WORD`), shows history (the 24 h totals and the job list) and is organised by repository. D3210 removes those sections; D3212 adds a health line that states ages, amending v2's no-age rule for this tab.
- **F23 (Gap).** The coordinator sweep writes no time stamp (`ee/coordinator/sweep.go`). A health line needs a heartbeat document beside its view (D3212). The sweep is off by default (`google_cloud_scheduler_job "coordinator_sweep"`, `deploy/terraform/gcp/modules/installation/coordinator.tf`). With it off, the tab names the fix instead of drawing an empty overlap.
- **F24 (Gap).** The console reads no orchestrator data today: there is no spawn row for `orchestrator ls`. A new read is security-sensitive (`.claude/rules/console.md`): an independent review, pinning tests, and the §5.1 table in `docs/desktop-console/spec.md`.
- **F25 (Exists).** elkjs in a worker plus SVG (`console/src/renderer/graph/`) can lay out the coordinator graph. No new dependency is needed.

### identity-display

- **F26 (Exists; the source).** Identity masking happens only at display time, in `console/src/shared/sanitize.ts` `sanitizeText`, called for every drawn string through `console/src/renderer/app.ts` `putText`:
  - the `address-marker` rule in `FREE_TEXT_RULES` (any email becomes `ADDRESS_PLACEHOLDER = 'redacted@example.invalid'`);
  - the `person` branch, which masks a whole email and refuses anything not matching `LOGIN_RE` (a display name, a dotted local part, `user:`/`serviceAccount:` prefixes);
  - `REPO_TOKEN_RE` (`owner/name` becomes `[redacted]`), plus the branch, path, host, GCP-name and IP rules;
  - the `host` and `path` classes, which replace every value.

  The address rule is also in `REFUSAL_RULES`, used for CLI stderr.
- **F27 (Exists).** Nothing is redacted at rest. The run record keeps `launched_by` as given (`internal/cli/run.go`, `runstore.Launch.LaunchedBy`). The CLI's `--json` paths apply only `safetext` (control characters, length). Go's identity scrubber `redact.Report` (`internal/redact/redact.go`) is used only by issue reports and diagnose. So removing the display masks restores the values, with no data migration.
- **F28 (Wrong, partly).** The spec's "unavailable" is the console's `n/a` (`console/src/shared/availability.ts` `UNAVAILABLE_MARK`), and it is mostly not a data-loading bug. The main cause is the `person` class refusing a non-login value ("not a valid person"). The other causes:
  - records written before `launch.json` existed carry no launcher (`run.ts` `putMaybe`);
  - `owner/name` values in `label` text become `[redacted]`;
  - gated screens: D15 off (`spend.ts` `PERSON_OFF_NOTE`); the CLI's `you: ""` when the caller is unidentified (`internal/cli/runs_stats.go`, `PEOPLE_YOU_UNIDENTIFIED`); version floors and refused reads.

  `author` is decoded (`schema.ts` `coordinatorPullRequestAt`) but no renderer draws it.
- **F29 (Rule).** The masks are documented and pinned, not just coded:
  - `console/README.md` (the class table: "`person` | a login or a pseudonym"; "no address is drawn anywhere");
  - `docs/desktop-console/spec.md` §6.2 (`launched_by` drawn short through `label` because `person` refuses dotted locals) and §5.1 rule 7;
  - `console/src/shared/launcher.ts`, which cuts at the first `@`;
  - the tests `sanitize.test.ts`, `sanitize-agreement.test.ts` and `launcher.test.ts`;
  - the Go docs tests that pin `docs/desktop-console/spec.md`.

  No owner D-number orders identity masking on live screens; D15 is a default, not a mask (D3214).
- **F30 (Rule).** `.claude/rules/console.md` requires every untrusted string to go through a `TEXT_CLASSES` class. That stays: markup and control-character cleaning and the secret rules (`CREDENTIAL_RULES`, `credentialPass`, `TOKEN_SHAPES`, the entropy net, `maskCredentialBlocks`) remain. Only non-secret masks go (D3213). The rule's text changes to say so, and that rules-file edit takes the independent review the security paths take.
- **F31 (Exists, kept).** The fixture recorder (`console/src/source/redact.ts`, `createRedactor`, used only by `tools/record-fixture` and `tools/scrub-fixture`) redacts identity in **committed** fixtures. It is not on the live path and stays (D3219): a committed file is not an operator's screen.

## Decisions

Each answer was given by the owner on 2026-11-21 through `AskUserQuestion`.

| # | Module | Question | Recommendation | Answer |
| --- | --- | --- | --- | --- |
| D3200 | all | Capability map | Four modules as above | **Approved**, as recommended |
| D3201 | run-index | Who writes the Firestore index? | The history job's sweep: it already holds `datastore.user` and runs-bucket read; no new IAM (F8) | **History job sweep**, as recommended |
| D3202 | run-index | Freshness between sweeps | The index stores "folded through T"; each read adds the RTDB live set and the outcomes since T | **Index + RTDB overlay**, as recommended |
| D3203 | indexed-reads | `runs ls` default | Newest 50 by time plus every active run, whatever its age; `--since` stays as a filter; `--after` pages by cursor; totals from the summary document | **Newest 50 + all active**, as recommended |
| D3204 | indexed-reads | runs-ls-speed RL4 and the GCS PR index | RL4 superseded; `--pr` moves to Firestore; the PR-index writers stop in a later task, with the objects left in place; Check 760 runs once as the "before" reading | **As recommended** |
| D3205 | run-index, indexed-reads | Acceptance | Hermetic request-count tests at 1x and 100x, plus owner Check 870 (the real project) and Check 871 (a scratch project seeded with 100x synthetic runs by a script) | **As recommended** |
| D3206 | coordinator-graph | What is a node? | Every live driver (orchestrators plus launcher identities), with no orchestrator change | **Allow many orchestrators per project**: not the recommendation. Followed through: the orchestrator is reshaped (D3207–D3209, D3218, D3220), and orchestrator-graph-estimates F2 and the plugin's orchestrator skill are amended |
| D3207 | coordinator-graph | How is one orchestrator identified, and is there a cap? | One per driver (recommended first); then, after "named", a slug and a cap of 16 | **Named, any number; slug `^[a-z0-9][a-z0-9-]{0,39}$`; no cap**: not the recommendation (twice). The default orchestrator keeps the project's name. A read that lists orchestrators reads the active ones only, so its cost grows with the active count, not with history |
| D3208 | coordinator-graph | Cross-orchestrator dependencies | `depends_on` may name `<orchestrator>/<item>`; the waiting orchestrator reads the other's item phase each tick | **As recommended**; amends `internal/orchestrator/shard.go`'s rule and graph contract §6.22 |
| D3209 | coordinator-graph | Two orchestrators on one task | A project-wide claim per plan task or backlog item (RTDB transaction); others skip it and may depend on it | **As recommended** |
| D3210 | coordinator-graph | v2's sections | Graph plus drill-down only: totals, job list, transcript and action queue removed (jobs stay on the Live tab) | **As recommended** |
| D3211 | coordinator-graph | What overlap reads | The coordinator's per-repository collisions (actually changed files and services) mapped to orchestrators, plus two orchestrators on one pull request; with the sweep off, the tab says so and names the fix | **As recommended** |
| D3212 | coordinator-graph | Health line | The sweep writes a heartbeat document (last start, last success); the tab shows it plus each orchestrator's lease age | **Include it**, as recommended |
| D3213 | identity-display | Which masks go | Every non-secret mask: identity, repo, branch, path, host, project, bucket. Kept: credential, token, entropy, credentials inside URLs, markup and control characters | **All non-secret masks**, as recommended |
| D3214 | identity-display | D15's per-person breakdown default | On by default; amends D15 | **On by default**, as recommended |

### Resolutions (one defensible answer, stated rather than asked)

| # | Module | Resolution | Why |
| --- | --- | --- | --- |
| D3215 | run-index | Backfill and rebuild are one mode of the history job (`--reindex`). `fugaro update` starts it after its Terraform apply. `fugaro runs reindex` starts it again. Both cover all history, not a window. The job upserts by run id (idempotent) and recomputes the counts from the index. A writer lease in the RTDB keeps a sweep and a reindex from overlapping. Until the first reindex finishes, the summary document says `complete: false`, and every reader prints that the index is still building, never a silently short list | F6, F7, F13 |
| D3216 | indexed-reads | The console keeps spawning the CLI; it never reads Firestore directly | F5: the data layer is shared by construction, and no new credential path enters the console |
| D3217 | indexed-reads | Pull-request merge state leaves the Graph tab's first paint. `plan status` reads the index only by default, and `plan status --merges` (a new allow-listed argv) is its own console section, painted after the canvas | F11; the console rule that sections paint as their data arrives |
| D3218 | coordinator-graph | Orchestrator ids stay flat RTDB keys. The default orchestrator keeps the key `<project>`, so no state moves and runs in flight are untouched. A named one is `<project>~<name>`. The state gains `project` and `name`; the lease gains `driver`. All three fields are additive, written by `fugaro update`'s migration for the existing node | F16, F18; the hard rule on `update` |
| D3219 | identity-display | The fixture recorder keeps redacting identity in committed fixtures | F31 |
| D3220 | coordinator-graph | A cross-orchestrator cycle is detected at tick time; the items in it go `blocked` with an escalation that names the cycle | F20 |
| D3221 | coordinator-graph | The tab reads one document from a new command, `fugaro orchestrator map --json`: active nodes (name, driver, active tasks, running jobs, lease age), edges (dependency, overlap) and the coordinator heartbeat. It is one new spawn row | F24 |
| D3222 | coordinator-graph | Overlap edges join collisions to orchestrators through each orchestrator's `WorkItem.run_id`; nothing new is written on a run | F21 |

## Open tensions

None. D3207's "no cap" and the spec's sub-second rule meet without conflict: `orchestrator map` reads one shallow listing plus the active nodes, so its cost grows with live orchestrators, not with history. If a project ever runs enough live orchestrators to break a second, that is a later decision, not one this plan takes.

## Open items

- O1. Check 760 (runs-ls-speed, owner-run, the script is on main) is still unrun. D3204 keeps it as the "before" reading, so it should run before `indexed-reads` merges.
- O2. orchestrator-graph-estimates and the 0.26.0 release notes say one machine per project drives runs. After D3206 the next release's notes say what changed (CG-DOCS for the skill and the earlier plan; the release notes come with the release).

## UNVERIFIED

- U1. That a warm CLI process start (configuration, credentials, one Firestore query, one RTDB read in parallel) fits under 1 s. The project layer and policy reads alone took about 0.8 s in a `fugaro settings` call on 2026-11-21, and whether `runs ls` waits on them was not traced. Settled by I-INDEX's `TestFixedInvocationCost` (the requests made before the first index read) and by owner Check 870.
- U2. That the history job is deployed on the owner's installation (`var.enable_budget && history.deploy_job`). always-a-budget makes the budget unconditional, but whether `deploy_job` is set everywhere was not checked. The owner can settle it with `fugaro doctor`; `run-index` refuses with the fix when the job is absent.
- U3. Which fence applies to `fugaro runs reindex` in a coding agent's session (it starts a job execution). It should follow whatever `update`'s own job-starting steps follow; RI5 reads that fence and pins it.
- U4. Firestore read latency for an ordered composite query of 50 documents from the owner's location. Expected well under 300 ms, but not measured; Check 870 measures it.
- U5. Whether the coordinator sweep's collisions stay bounded at 100x (they compare pairs of in-flight records per repository). In-flight counts, not history, bound them; Check 871 does not cover the sweep.
