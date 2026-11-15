# Forge labels: the spec against the code

**Spec:** [spec.md](spec.md), "Fugaro Spec: Forge Integration via Labels", verbatim. **Owner: Dimi Paun (D2430). Checked against `origin/main` at `f70f520cc` on 2026-11-15.** Where this file disagrees with `spec.md`, this file wins.

Numbers taken: **D2430-D2449** (the highest in use is D2429, in open pull requests), **Checks 640-649** (the highest is 632, in open pull requests), findings **F1-F16**, assumptions **A1-A5**, resolutions **R1-R7**, open items **O1-O2**, UNVERIFIED **U1-U3**.

## Assumptions

- **A1.** "The stateless orchestrator" is `internal/orchestrator`, the daemon `fugaro orchestrator run` starts on the owner's machine, not a cloud service: it has a tick (`orchestrator.Tick`) and no inbound endpoint.
- **A2.** "Job" means a Fugaro run, and a job's state is the work item's `Phase` (`queued`, `running`, `review`, `done`, `failed`, `blocked`, `cancelled`) as the daemon folds it from the run record.
- **A3.** "A spec's task" means a plan task a run was launched for with `fugaro run --plan-task`; the spec name is the plan's folder name under `docs/`.
- **A4.** The labels are for pull requests Fugaro opened; a pull request a person opened never gets one (the daemon only knows pull requests behind its own run ids).
- **A5.** The feature is off wherever no orchestrator runs: nothing else writes these labels (D2432).

## What the spec's concepts already have

| Spec concept | What the repository has |
| --- | --- |
| Labels on a pull request | `git.pr.labels` (`internal/config`, scope `InProject \| InRepo`): static labels, applied by `gitprov.Provider.ApplyReady` when a draft becomes ready; a draft carries none (`docs/git-providers.md`). Bitbucket drops them with one warning (`bitbucket.Provider.warnLabels`) |
| "Forge interface, optional capability" | `gitprov.Provider` has no label method besides `ApplyReady`'s list. Optional capabilities are separate interfaces found by type assertion (`gitprov.MintExempt`, `MergeMinter` in `internal/gitprov/merge.go`) |
| The orchestrator's forge writes | None. Its only forge action, `TriageActor.Merge`, always refuses with `ErrTriageUncorroborated` (`internal/cli/orchestrator_triage.go`); its one forge credential path is `creds.ServiceMergeMint`, bound to one pull request at one head |
| Job start, finish, death | `orchestrator.Tick` folds each run's report into `WorkItem.Phase` (`internal/orchestrator/state.go`); the seams (`Triage`, `Decide`) are fields that, left unset, make the cycle skip that step |
| Spec name | `launch.json`'s `PlanTask` (`internal/runstore/launch.go`), display-only, shape-checked by `task.PlanTaskProblem`, "never an authorization input" |

## Findings

- **F1 (Exists).** `git.pr.labels` and `ApplyReady` already label a ready pull request. The spec's labels are a second, dynamic mechanism and leave `git.pr.labels` alone. *Consequence:* no change to `PRSpec.Labels` or `ApplyReady`; the new capability is separate (C1).
- **F2 (Rule).** `docs/git-providers.md` says a draft carries no reviewers and no labels, and a run that ends as a draft notifies nobody. The spec labels a pull request "when it is created". *Consequence:* the status and spec labels are not written through `PRSpec`; the daemon writes them after it sees the pull request, so the draft rule stands (R1).
- **F3 (Wrong).** The spec says GitHub and GitLab support labels. The code has `github`, `bitbucket` and `fake` providers only; `fugaro exec` refuses "gitlab". *Consequence:* GitHub is the one real adapter (D2431).
- **F4 (Wrong).** The spec says the orchestrator "opens a PR for a spec". A run opens it (the runner's `EnsurePR`); the daemon learns the number from the run record. *Consequence:* spec labels are applied when the daemon first sees the pull request in a run record (R2).
- **F5 (Gap, security).** The daemon holds no forge write credential and the one mint it has (`creds.ServiceMergeMint`, path `/v1/github/merge-token`) is bound to a repository, pull request number and a 40-hex head, and the service gates it on the merge rules. A label write is not a merge. *Consequence:* D2436's answer, "reuse the merge mint's fence", cannot mean the same mint; it is amended before the plan (below, D2436) to a sibling mint with the same fence.
- **F6 (Gap).** `WorkItem` carries neither a pull request number nor a spec name. *Consequence:* the number comes from the run record (`runstore`'s `pr` field), the spec name from `launch.json`'s `PlanTask`; an item with no plan task gets no spec label (R2).
- **F7 (Rule).** `PlanTask` is untrusted text written by the launcher, and a label name made from it is output to the host. *Consequence:* the spec label is built only from a name that passes a strict shape (lowercase letters, digits, `-`, bounded length), otherwise skipped with one log line (R3).
- **F8 (Gap).** The spec says nothing about `queued`, `review`, `blocked` and `cancelled`. *Consequence:* a mapping (R4).
- **F9 (Wrong).** "Prefer webhooks": the daemon has no inbound endpoint, and the only "webhook" in `internal` is a Bitbucket deny-list entry. *Consequence:* tick-driven (D2434).
- **F10 (Gap).** "Keeps the expected label state in memory and issues a call only when a label needs to change" is only true if the daemon knows what the host holds, and a restart empties its memory. *Consequence:* each tick reads the labels in bulk, one list call per repository with a labelled pull request (D2435), and writes only the difference; memory holds only what is worth not repeating (labels known to exist, a spec label already refused) (R5).
- **F11 (Gap).** `fugaro setup` "pre-creates" the labels, but `fugaro update` is the whole upgrade (CLAUDE.md, HARD RULE): an installation that is already set up must converge without another command, and the daemon starts after `update`. *Consequence:* the daemon ensures the labels exist, with their colours, before the first add (R6); setup does not pre-create.
- **F12 (Gap).** Merged and closed pull requests: the spec does not say what happens to `fugaro:running` on one whose job ended while it merged. *Consequence:* the reconciler removes both status labels from a merged or closed pull request (D2433).
- **F13 (Gap).** A plain `fugaro run` has no orchestrator watching it. *Consequence:* its pull request carries no status labels (D2432); the docs say so.
- **F14 (Rule).** The change touches the credentials service (`internal/credsvc`, `internal/creds`) and a forge write path, so `.claude/rules/credentials-service.md` loads, `SECURITY.md` and `website/content/security.md` are pinned documents, and the tasks that touch them are reviewed by the security reviewer.
- **F15 (Gap).** No success criterion is stated. *Consequence:* the plan's acceptance is: after one tick, a pull request's `fugaro:` labels equal the desired set for its item, and a second tick with nothing changed makes no write.
- **F16 (Gap).** `retry`: the daemon allows one automatic retry after a first failure, so the label sequence is running, failed, running. *Consequence:* none; the spec's rules already give it ("on job start: add running, remove failed").

## Resolutions (one defensible answer)

- **R1.** The daemon writes all labels after the draft exists; `PRSpec.Labels` and `ApplyReady` stay as they are (F1, F2).
- **R2.** The spec label is `spec:<folder>` for the plan folder of the item's `PlanTask`, added the first tick the daemon has a pull request number for the item; an item with no plan task has no spec label (F4, F6).
- **R3.** A spec label is written only when the name passes `^[a-z0-9][a-z0-9-]{0,48}$` after lower-casing; otherwise skipped, one log line (F7).
- **R4.** Desired status label by phase: `running` and `review` give `fugaro:running`; `failed` gives `fugaro:failed`; `queued`, `blocked`, `done` and `cancelled` give neither. A merged or closed pull request gets neither (F8, F12).
- **R5.** Each tick the syncer reads one bulk list per repository that has an item with a pull request, compares it with the desired set, and writes only the difference; it remembers which labels it has ensured and which spec labels the host refused, so neither is retried every tick (F10).
- **R6.** The daemon ensures `fugaro:running` and `fugaro:failed` exist (colour and description from D2437) before it first adds them, and ensures a spec label the same way (F11).
- **R7.** Label operations are a separate optional interface found by type assertion; a provider without it, or whose `Supported()` is false (Bitbucket), makes every operation a silent no-op: no warning each tick (F1).

## Decisions

| # | Question | Recommendation | Alternative | Answer |
| --- | --- | --- | --- | --- |
| **D2430** | Spec owner (the spec names none) | Dimi Paun | The project owner, unnamed | **2026-11-15: the recommendation.** |
| **D2431** | GitLab (F3) | GitHub only now; the capability is optional, so GitLab joins with a provider | Reserve GitLab in the interface; or add a provider first | **2026-11-15: the recommendation.** Why: there is no GitLab provider to attach it to. |
| **D2432** | Runs outside the orchestrator (F13) | Orchestrator-launched tasks only | The runner labels its own PR; or the reconciler covers every run | **2026-11-15: the recommendation.** Why: one writer, as the spec says. |
| **D2433** | Clearing `fugaro:failed` | Only on the next job, or when the pull request merges or closes | Also on a new push | **2026-11-15: the recommendation.** Why: a push needs an event the daemon does not receive. |
| **D2434** | Webhooks (F9) | Tick-driven, none built | A webhook receiver | **2026-11-15: the recommendation.** |
| **D2435** | Reconciliation interval | Every tick, one bulk read per repository | Every 10 minutes | **2026-11-15: the recommendation.** Why: within the hourly limit for a handful of repositories; a stale label lives one tick. |
| **D2436** | The credential that writes labels (F5) | Reuse the merge mint's fence for a label-only call | A separate label credential | **2026-11-15: the recommendation; amended before the plan.** Fact: `creds.ServiceMergeMint` mints for one pull request at one head and the service gates it on merge rules, so no label call can ride on it (F5). What changed: the same fence (the service's orchestrator-class identity, refused for a run's identity and in an agent's session, repository-scoped, rate-limited), as a sibling endpoint `/v1/github/label-token` and a sibling client `creds.ServiceLabelMint`; no new credential store. Kept: no second token to store or rotate. **Confirm in O1.** |
| **D2437** | Colours and descriptions | Blue, red, grey (below) | Leave to the owner | **2026-11-15: the recommendation.** |

**Label table (D2437).**

| Label | Colour | Description |
| --- | --- | --- |
| `fugaro:running` | `1D76DB` | A Fugaro job is working on this pull request |
| `fugaro:failed` | `D73A4A` | The last Fugaro job on this pull request failed |
| `spec:<name>` | `6E7781` | Pull request for spec <name> |

## Open tensions

None.

## Open items

- **O1.** Confirm D2436 as amended: a sibling label mint, not the merge mint itself.
- **O2.** Confirm that the daemon, not `fugaro setup` or `fugaro update`, creates the labels (R6), as the spec's "setup pre-creates" cannot converge an existing installation without a second command.

## UNVERIFIED

- **U1.** Whether the GitHub App's installation permissions already allow label writes on a pull request (label endpoints need Issues or Pull requests write). The owner can read it on the App's settings page; the merge path already needs pull request write, which probably covers it.
- **U2.** Whether the credentials service's token request can take the narrower permission set a label call needs, or reuses the merge mint's set. Settled by reading `internal/credsvc/mergemint.go` when T3 starts.
- **U3.** GitHub's limits on label creation by an App token on a repository the App is installed on (names with `:`, the 100-character limit on a name, the 50-character description limit). The names and descriptions above are within the documented limits; a live check (Check 640) settles it.
