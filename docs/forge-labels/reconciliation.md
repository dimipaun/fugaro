# Forge labels: the spec against the code

**Spec:** [spec.md](spec.md), "Fugaro Spec: Forge Integration via Labels", verbatim. **Owner: Dimi Paun (D2430). Checked against `origin/main` at `f70f520cc` on 2026-11-15.** Where this file disagrees with `spec.md`, this file wins.

Numbers taken: **D2430-D2449** (the highest in use is D2429, in open pull requests), **Checks 640-649** (the highest is 632, in open pull requests), findings **F1-F26**, assumptions **A1-A5**, resolutions **R1-R8**, open items **O1-O4**, UNVERIFIED **U1-U3**. **Revised after review rounds 1 to 4 (2026-11-15):** F17-F26 and D2438-D2440 are new, F5-F8, F10, F12, F17 and R2-R6 changed, D2436 is superseded by D2439 and D2438 is amended.

## Assumptions

- **A1.** "The stateless orchestrator" is `internal/orchestrator`, the daemon `fugaro orchestrator run` starts on the owner's machine, not a cloud service: it has a tick (`orchestrator.Tick`) and no inbound endpoint from a forge (it serves one local endpoint on its own machine, which is not a webhook receiver).
- **A2.** "Job" means a Fugaro run, and a job's state is the work item's `Phase` (`queued`, `running`, `review`, `done`, `failed`, `blocked`, `cancelled`) as the daemon folds it from the run record.
- **A3.** "A spec's task" means a work item a planning session created from a plan task; the spec name is the plan's folder under `docs/` (for `docs/plans/<x>.md`, `<x>` lowercased with other characters replaced by `-`), carried on the work item in the patch that creates it (D2438).
- **A4.** The labels are for pull requests Fugaro opened; a pull request a person opened never gets one (the daemon only knows pull requests behind its own run ids).
- **A5.** The feature is off wherever no orchestrator runs: nothing else writes these labels (D2432).

## What the spec's concepts already have

| Spec concept | What the repository has |
| --- | --- |
| Labels on a pull request | `git.pr.labels` (`internal/config`, scope `InProject \| InRepo`): static labels, applied by `gitprov.Provider.ApplyReady` when a draft becomes ready; a draft carries none (`docs/git-providers.md`). Bitbucket drops them with one warning (`bitbucket.Provider.warnLabels`) |
| "Forge interface, optional capability" | `gitprov.Provider` has no label method besides `ApplyReady`'s list. Optional capabilities are separate interfaces found by type assertion (`gitprov.MintExempt`, `MergeMinter` in `internal/gitprov/merge.go`) |
| The orchestrator's forge writes | None. Its only forge action, `TriageActor.Merge`, always refuses with `ErrTriageUncorroborated` (`internal/cli/orchestrator_triage.go`); `creds.ServiceMergeMint` and the service's merge route exist, but nothing in `internal/cli` constructs the client, and `SECURITY.md` says the route ships inert: the daemon account, the invoker grant and the variable naming the service are not carried yet (E10b, `docs/outer-loop-escalation`) |
| Job start, finish, death | `orchestrator.Tick` folds each run's report into `WorkItem.Phase` (`internal/orchestrator/state.go`); the seams (`Triage`, `Decide`) are fields that, left unset, make the cycle skip that step |
| Spec name | `launch.json`'s `PlanTask` (`internal/runstore/launch.go`) is display-only, written from the run's side, "never an authorization input", and the orchestrator's launcher (`internal/cli/orchestrator_launcher.go`) never sets it; `WorkItem` has no such field |

## Findings

- **F1 (Exists).** `git.pr.labels` and `ApplyReady` already label a ready pull request. The spec's labels are a second, dynamic mechanism and leave `git.pr.labels` alone. *Consequence:* no change to `PRSpec.Labels` or `ApplyReady`; the new capability is separate (T1).
- **F2 (Rule).** `docs/git-providers.md` says a draft carries no reviewers and no labels, and a run that ends as a draft notifies nobody. The spec labels a pull request "when it is created". *Consequence:* the status and spec labels are not written through `PRSpec`; the daemon writes them after it sees the pull request, so the draft rule stands (R1).
- **F3 (Wrong).** The spec says GitHub and GitLab support labels. The code has `github`, `bitbucket` and `fake` providers only; `fugaro exec` refuses "gitlab". *Consequence:* GitHub is the one real adapter (D2431).
- **F4 (Wrong).** The spec says the orchestrator "opens a PR for a spec". A run opens it (the runner's `EnsurePR`); the daemon learns the number from the run record. *Consequence:* spec labels are applied when the daemon first sees the pull request in a run record (R2).
- **F5 (Gap, security).** The daemon holds no forge write credential and no path to the credentials service today (F19). The merge mint is bound to a repository, a pull request and a 40-hex head and gated on the merge rules, so no label call can ride on it. A token the service hands out for labels is not label-only: GitHub has no permission for labels alone, and the App's run token asks for `contents: write`, `pull_requests: write`, `issues: read`, `metadata: read` (`internal/gitprov/github/apptoken.go`, `tokenPermissions`), so a label token would be `pull_requests: write` on the whole repository (approve, close, retitle any pull request). *Consequence:* D2436 ("reuse the merge mint's fence") is superseded by D2439: the service performs the label write itself and no token leaves it.
- **F6 (Gap).** `WorkItem` carries neither a pull request number nor a spec name, and the launcher records no plan task. *Consequence:* the number comes from the run record (`runstore`'s `pr` field), corroborated by the service (F17); the spec name is a new optional `WorkItem` field set in the `work_item_upsert` patch that creates the item (D2438; the daemon has no plan import, work items come only from patches); an item with none gets no spec label (R2).
- **F7 (Rule).** A spec name still reaches the host as a label name. *Consequence:* the spec label is built only from a name that passes a strict shape (lowercase letters, digits, `-`, at most 44 characters, so `spec:` plus the name is at most 49 of GitHub's 50), otherwise skipped with one log line (R3); the service re-checks it (F17).
- **F8 (Gap).** The spec says nothing about `queued`, `review`, `blocked` and `cancelled`; `review` is set only by a patch and an item can sit in it with no job running. *Consequence:* a mapping (R4).
- **F9 (Wrong).** "Prefer webhooks": the daemon has no inbound endpoint, and the only "webhook" in `internal` is a Bitbucket deny-list entry. *Consequence:* tick-driven (D2434).
- **F10 (Gap).** "Keeps the expected label state in memory and issues a call only when a label needs to change" is only true if the daemon knows what the host holds, and a restart empties its memory. *Consequence:* each tick reads the open pull requests' labels in bulk, one list call per repository with a labelled pull request (D2435), and writes only the difference; memory holds only what is worth not repeating (labels known to exist, a spec label already refused, a pull request already known closed and clean) (R5).
- **F11 (Gap).** `fugaro setup` "pre-creates" the labels, but `fugaro update` is the whole upgrade (CLAUDE.md, HARD RULE): an installation that is already set up must converge without another command, and the daemon starts after `update`. *Consequence:* the daemon ensures the labels exist, with their colours, before the first add (R6); setup does not pre-create.
- **F12 (Gap).** Merged and closed pull requests: the spec does not say what happens to `fugaro:running` on one whose job ended while it merged. *Consequence:* the reconciler removes both status labels from a merged or closed pull request (D2433). A bulk list of open pull requests cannot show one, so a pull request absent from it is read once by number until it is known closed and clean (R5).
- **F13 (Gap).** A plain `fugaro run` has no orchestrator watching it. *Consequence:* its pull request carries no status labels (D2432); the docs say so.
- **F14 (Rule).** The change touches the credentials service (`internal/credsvc`, `internal/creds`) and a forge write path, so `.claude/rules/credentials-service.md` loads, `SECURITY.md` and `website/content/security.md` are pinned documents, and the tasks that touch them are reviewed by the security reviewer.
- **F15 (Gap).** No success criterion is stated. *Consequence:* the plan's acceptance is: after one tick, a pull request's `fugaro:` labels equal the desired set for its item, and a second tick with nothing changed makes no write.
- **F17 (Gap, security).** The pull request number in the run record and any name a run wrote are written by the job, inside the container it shares with the agent; the triage code refuses to trust the record's repository for the same reason. A hostile run could name another pull request and have the daemon relabel it. *Consequence:* the repository comes from the daemon's own item; the daemon sends the run id from its own intents with every write and the service refuses a label write on a pull request whose head branch is not exactly `fugaro/<run id>` (`gitops.RunBranchPrefix` plus the run id) or whose author is not Fugaro's own identity, so a run can name another pull request but not get it relabelled; the service accepts only the two exact status names and spec names of the allowed shape (R5).
- **F18 (Gap, security).** The merge mint's fence does not refuse an agent's session: `SECURITY.md` and `credsvc/mergemint.go` say a coding agent's session on the operator's machine can present the daemon account's identity. What the label route keeps from that fence is the caller class, repository scope, rate limit and audit line; its own protection is that the service accepts only an allowed label on a corroborated pull request, which bounds a stolen identity to relabelling Fugaro's own pull requests. *Consequence:* `SECURITY.md` says so (T6).
- **F19 (Gap).** The daemon has no identity to the service: `OrchestratorAccounts` is set nowhere outside tests, the invoker grant is Terraform and not carried, and nothing mints the daemon's ID token for the service. *Consequence:* a prerequisite outside this plan, named in the plan; T7 and Check 640 cannot pass without it (O2).
- **F20 (Gap).** GitHub limits a label name to 50 characters and a description to 100 (the first draft had them reversed). The descriptions below are well within it. *Consequence:* R3's bound.
- **F21 (Gap).** Each run pushes to its own branch `fugaro/<run id>` and so opens its own pull request (the launcher sets no branch), so a retry is a new pull request, not a second job on one: a superseded failed run's pull request would keep `fugaro:failed` and never clear. *Consequence:* label state is keyed per run and its pull request (R8).
- **F22 (Gap).** The service's rate limit for the merge route is 10 an hour per repository; the daemon ticks about every 30 seconds. *Consequence:* the label route states its own limits, tested, sized for a tick that writes only on a difference and reads once per tick, with reads counted apart from writes.
- **F23 (Rule).** A syncer that "writes the difference" over the whole `fugaro:`/`spec:` set would remove a `spec:` label a person added, against the spec's own rule. *Consequence:* it adds only its own spec label, removes only `fugaro:running` and `fugaro:failed`, and matches names the way GitHub does, case-insensitively (R5).
- **F24 (Rule).** `website/content/security.md` is `SECURITY.md` regenerated (`go run ./internal/clidoc`), never edited by hand (`.claude/rules/credentials-service.md`).
- **F25 (Gap).** `RunReport` is only the run id, start, finish and outcome, and the fold reads only intents that have not reported; a finished or failed item still needs its pull request number to keep or clear its labels. *Consequence:* T7 adds a per-run reader of the run record's pull request number, cached per run id because the number is fixed once set.
- **F26 (Gap, security).** The route accepts a caller-chosen name and text; a stolen daemon identity could write arbitrary label text or remove a `spec:` label a person added. *Consequence:* the request carries only a name, the service applies D2437's colours and descriptions itself, and the service removes only `fugaro:running` and `fugaro:failed`.
- **F16 (Gap).** `retry`: the daemon allows one automatic retry after a first failure; the retry is a new run on a new pull request (F21), so the first pull request keeps `fugaro:failed` until it closes, and the second is `running`. *Consequence:* none beyond R8.

## Resolutions (one defensible answer)

- **R1.** The daemon writes all labels after the draft exists; `PRSpec.Labels` and `ApplyReady` stay as they are (F1, F2).
- **R2.** The spec label is `spec:<name>` for the item's `spec` field (D2438), added the first tick the daemon has a corroborated pull request number for the item; an item with no `spec` has no spec label (F4, F6).
- **R3.** A spec label is written only when the name passes `^[a-z0-9][a-z0-9-]{0,43}$` after checking that the name is ASCII and then lower-casing it (F7, F20; the Kelvin sign lowers to `k`); otherwise skipped, one log line (F7).
- **R4.** Desired status label by phase: `running` gives `fugaro:running`; `failed` gives `fugaro:failed` on the pull request of the run that failed, which keeps it until that pull request closes: the daemon launches no follow-up run on an existing pull request (the launcher sets no branch, F21), so in practice only a close clears it (D2433, R8); `queued`, `review`, `blocked`, `done` and `cancelled` give neither. A merged or closed pull request gets neither (F8, F12).
- **R5.** Each tick the syncer reads one bulk list of open pull requests per repository that has an item with a pull request (and, once, a pull request absent from it by number), compares it with the desired set, and writes only the difference: it adds its own `spec:` label and the wanted status label, and removes only the exact names `fugaro:running` and `fugaro:failed`, never another label; names match case-insensitively; it remembers which labels it has ensured and which spec labels the host refused (F10, F17, F23).
- **R6.** The service creates `fugaro:running`, `fugaro:failed` and a spec label, with D2437's colour and description, just before the first add (D2440, F11).
- **R7.** Label operations are a separate optional interface found by type assertion; a provider without it, or whose `Supported()` is false (Bitbucket), makes every operation a silent no-op: no warning each tick (F1).
- **R8.** Label state is per run and its pull request: a pull request is `running` while its own run is in flight, `failed` when its own run ended failed, and neither otherwise; a failed run's pull request stays tracked until it is known closed and clean (F21).

## Decisions

| # | Question | Recommendation | Alternative | Answer |
| --- | --- | --- | --- | --- |
| **D2430** | Spec owner (the spec names none) | Dimi Paun | The project owner, unnamed | **2026-11-15: the recommendation.** |
| **D2431** | GitLab (F3) | GitHub only now; the capability is optional, so GitLab joins with a provider | Reserve GitLab in the interface; or add a provider first | **2026-11-15: the recommendation.** Why: there is no GitLab provider to attach it to. |
| **D2432** | Runs outside the orchestrator (F13) | Orchestrator-launched tasks only | The runner labels its own PR; or the reconciler covers every run | **2026-11-15: the recommendation.** Why: one writer, as the spec says. |
| **D2433** | Clearing `fugaro:failed` | Only on the next job, or when the pull request merges or closes | Also on a new push | **2026-11-15: the recommendation.** Why: a push needs an event the daemon does not receive. |
| **D2434** | Webhooks (F9) | Tick-driven, none built | A webhook receiver | **2026-11-15: the recommendation.** |
| **D2435** | Reconciliation interval | Every tick, one bulk read per repository | Every 10 minutes | **2026-11-15: the recommendation.** Why: within the hourly limit for a handful of repositories; a stale label lives one tick. |
| **D2436** | The credential that writes labels (F5) | Reuse the merge mint's fence for a label-only call | A separate label credential | **2026-11-15: the recommendation. Superseded by D2439 after review round 1:** the premise was wrong in two ways (the daemon has no service path yet; a label-only token does not exist on GitHub). |
| **D2437** | Colours and descriptions | Blue, red, grey (below) | Leave to the owner | **2026-11-15: the recommendation.** |
| **D2438** | The spec name's source (F6): the launcher records no plan task | A new optional `WorkItem` field | Drop spec labels for now; or read the run's launch record | **2026-11-15: the recommendation; amended after review.** The first answer rested on "the daemon's own plan import", which does not exist (work items come only from patches a session or person writes). Re-confirmed by the owner on 2026-11-15 as: the field is a shape-checked `work_item_upsert` field set in the patch; a run still cannot forge it. |
| **D2439** | How labels are written (F5, F18) | The credentials service performs the label write itself, so no token leaves it | A sibling token mint with `pull_requests: write`; or wait for the merge mint's wiring | **2026-11-15: the recommendation.** Why: only here is "label-only" true, and the service can enforce the namespaces and corroborate the pull request. Supersedes D2436. |
| **D2440** | Who creates the labels (F11, R6): the spec says `fugaro setup` | The service, on first use | Setup and update pre-create them | **2026-11-15: the recommendation.** Why: `fugaro update` is the whole upgrade, and an installation already set up must converge with no second command. |

**Label table (D2437).**

| Label | Colour | Description |
| --- | --- | --- |
| `fugaro:running` | `1D76DB` | A Fugaro job is working on this pull request |
| `fugaro:failed` | `D73A4A` | The last Fugaro job on this pull request failed |
| `spec:<name>` | `6E7781` | Pull request for spec <name> |

## Open tensions

None.

## Open items

- **O1.** None (the earlier item, confirming D2436 as amended, is closed by the owner's answer D2439).
- **O2.** *Answered 2026-11-15: wait for E10b (the recommendation).* One owner builds the daemon's identity to the credentials service (F19), shared with the merge mint; T7's live path and Check 640 stay blocked until it lands, and this plan adds no task for it.
- **O3.** *Answered 2026-11-15: the recommendation.* The daemon sends the id of the run that opened the pull request, taken from its own intents, and the service keeps the exact head match `fugaro/<that run id>`.
- **O4.** *Answered 2026-11-15: later, as follow-ups (the recommendation).* Folding `ensure` into `add` and sampling the audit of the 30-second reads are not in this plan; the ASCII-before-lowering rule is already in T3.

## UNVERIFIED

- **U1.** Whether the dogfood installation's GitHub App holds `pull_requests: write` on its repositories (the code's own run token asks for it, so it probably does). The owner reads it on the App's settings page; Check 640 shows it.
- **U2.** Which permission set the service asks for when it mints its own token for a label write: `{pull_requests: write, metadata: read}` is the narrowest that works; T3 pins it by a test.
- **U3.** GitHub's behaviour for a label name containing `:` created by an App token (names and descriptions are within the documented 50 and 100 character limits); Check 640 settles it.
