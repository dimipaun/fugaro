# Outer-loop escalation: reconciliation

**Checked against** `main` at `d19e2b1cf` (fetched 2026-11-10), by symbol and path, not line. Where this page disagrees
with [spec.md](spec.md), this page wins. **Spec owner:** Dimi Paun. **Date:** 2026-11-10.

**Numbers this work holds:** decisions **D2030-D2069** (D2030-D2046 used), checks **540-549** (540-541 used). The highest
numbers found were D2019 on `main` and D1961 in open pull requests; Check 522 on `main` and 481 in open pull requests.

## Headline: spec concept, what the repo already has

| Spec concept | What the repo has | Class |
| --- | --- | --- |
| "The orchestrator restarts the PR" | The per-person local daemon of `docs/stateless-orchestrator` (`internal/orchestrator`: `state.go`, `tick.go`, `launch.go`, `notify.go`, `audit.go`). Its retry rule is bounded: one approval covers the first attempt and at most one automatic retry. | Gap (F1) |
| Rung 3 "merge as is, file follow-ups" | **BF2 / O14** (`docs/stateless-orchestrator`, D1244-D1247, answered 2026-11-08, design only, not built): merge as-is on positive evidence, a split for a run with nothing usable, a person's decision for a critical finding. | Exists (F2) |
| "Merge" at all | `docs/design/v1.md` non-goal "Auto-merge. Fugaro never merges"; D13 (`docs/rigor-modes`). `gitprov.Provider` has no merge call. D1245 is the only ruling that lets code merge. | Rule (F3) |
| Merge judge, a strong model | D1244/D1245 reject a model deciding a merge ("a prompt-injected run merges itself"). | Rule (F4) |
| "Tier" 0-4, "model tier" | `internal/tier` already means trivial/small/medium/large (`fugaro run --tier`, `governance.tiers`). | Rule (F5) |
| Rigor modes POC/Startup/Pro/Critical/Hotfix | Exist, `internal/rigor/mode.go`, closed set `Modes`, Pro is the default. Triage runs in Startup and Pro only (`config.TriageMode`). | Exists (F6) |
| Time and cost tracking per PR | The run chain: `docs/run-limit-carry-over` adds a chain budget a restart cannot reset, with `--max-usd/--max-tokens/--max-time` in the run records. Orchestrator state has `Attempts` and `DependsOn`, no wall-clock or cost ledger. | Exists / Gap (F7) |
| `models.strong`, `models.strongest`, `escalation.*` | Not found anywhere. Models are set per role (`agent.models.{coder,reviewer,background}`) and per craft (`agent.crafts.*`, `docs/model-crafts`). | Gap (F8) |
| Single-pass escalation recipe | Recipes (`internal/recipe`, catalog `default`, `solo`, `standard`, `premium`, `review-only`, ...) cannot say "one review pass, no fix, hand over". A review that asks for changes triggers a fix. A recipe names no model. | Gap (F9) |
| Rung 0 rebase and re-test | No rebase, conflict-resolution or update-branch step anywhere (`internal/gitops/checkpoint.go` only guards against an in-progress rebase). | Gap (F10) |
| Churn detection | `rigor.Split` has no cross-round memory. A finding has no identity across rounds (`docs/post-review-triage` F11; D1850 plans a key in the result record's `triage` block). D1246 plans "same file and class in two or more Reviews". | Gap (F11) |
| Follow-ups tagged with the source PR, important vs minor | `Task.FollowUpOf` and `Task.Debt` exist in `internal/task/task.go`; `fugaro followups RUN` is planned (T-TR10, not built). No importance field. | Exists / Gap (F12) |
| Merge notification, human sign-off | `internal/orchestrator/notify.go` has kinds `run-finished` and `decision-needed` and only a log sink; the owner push is a later task of `docs/stateless-orchestrator` (not built). D1247 raises a `waiting_on: person` decision. | Exists / Gap (F13) |
| Per-orchestrator daily report with a project roll-up | `docs/periodic-reviews`: one project-level writer (E4, "no per-orchestrator scheduler"), daily/weekly digests, 7 of 14 built. | Rule (F14) |
| "Blocks other tasks" | `orchestrator.WorkItem.DependsOn` (the dependency-driven launcher, O6, merged). A PR's "blocks" is the reverse edge. `fugaro plan` has `CriticalPathTasks` for plan documents only. | Exists (F15) |
| Strict config precedence, "thresholds can be overridden at the project level" | `internal/policy.Merge` is tighten-only; mode precedence is area > repo > project > default and **a repo may loosen the mode**. D1244 forbids the project layer from setting `orchestrator.triage_after`. | Rule (F16) |
| Review stop at 80% of time | `agent.review_stop_fraction` (default 0.8) and `rigor.BlocksAtStop` already make a PR ready at the stop with the rest as follow-ups: the in-run cousin of rung 3. It makes a PR ready; it does not merge. | Exists (F17) |

## Findings

**F1 (Gap). The outer loop is a bounded rule, not an open loop.** The daemon is a per-person local process holding state in the
RTDB `orchestrator/` node. Its stated retry is "the first attempt and at most one automatic retry"; a second failure needs a
person's new approval. The spec's "the orchestrator repeatedly resubmitting the same PR" is, today, mostly a person or a session
running `fugaro run --pr N`. Consequence: the ladder is the rule that replaces both the person's reflex and the one-retry rule,
and it lives in the daemon, which is O0-O4 and O6 merged and O8-O14, O16, O19 not (`docs/status.md`: 10/19).

**F2 (Exists). BF2/O14 is the same idea at a smaller scale.** D1244: deterministic code, never a model, fired by run age alone,
local key `orchestrator.triage_after` default 2h. D1245: merge as-is and file a follow-up from the open findings when the run is
terminal and ready, has commits, CI is green, the path set classifies `Routine` (`internal/orchestrator/sensitivity`), no finding
is critical and none is high in a security path. D1246: a run with no commits, no progress, or the same finding looping is
cancelled and split, never relaunched with the same spec. D1247: a critical finding or a high one in a security path stops and
raises `waiting_on: person`; each triage is appended to the audit log as `<run>:triage`; input it cannot read fails closed to
recommend-only. Reading a pull request's checks does not exist yet (`gitprov.PRInfo`); O14 adds it. **`orchestrator.triage_after` was never built**: only the docs, and `docs_stateless_orchestrator_test.go` pins its name, so D2032's replacement changes docs, not shipped config. Consequence: the ladder is
built **on** O14, not beside it; rung 3 and rung 4 are O14's two outcomes, and the plan amends D1244 and D1246 visibly (D2032,
D2036) rather than building a second rule.

**F3 (Rule). The spec has Fugaro merge; Fugaro never merges.** Rule: `docs/design/v1.md` non-goals, D13, `gitprov` has no merge
call. The one merge verb in code is `internal/mailbox/message.go` `Actions`, a person's confirmed command run as `gh pr merge`
under the operator's login by a consumer not built (`docs/mobile`, D169). D1245 is the owner's first ruling that code merges, and
on a narrow gate. Consequence: D2030 (answered): only the orchestrator daemon merges, only under D1245's gate; the rigor mode
chooses who is told and who must approve, never whether the gate applies. The spec's rows read accordingly (below).

**F4 (Rule). A model must not authorize a merge.** The judge's input is hostile, so an injected diff may talk the judge out of its veto; the deterministic gate is the only fence, and the judge adds a veto only on top of it. D1244/D1245 rejected "merge on a model's judgement of the diff". The
diff is attacker-influenced input (a hostile run, a hostile dependency). Consequence: D2031 (answered): the merge judge is a
model that can **only veto** and classify follow-ups; eligibility is decided by deterministic code before and after it.

**F5 (Rule). "Tier" collides with `internal/tier`.** `fugaro run --tier` and `governance.tiers` show trivial/small/medium/large
as a cost expectation. Two "tier" columns in the CLI and console would mislead. Consequence: D2033 (answered): the ladder steps
are **rungs R0-R4**; `strong` and `strongest` are **model grades**. The rest of this folder uses those words.

**F6 (Exists). Rigor modes match, but triage does not run in every mode.** `poc`, `startup`, `pro`, `critical`, `hotfix`; Hotfix
is off the axis (`Rank()` 0, blocks only security and wrong-fix findings). `agent.triage` is Startup and Pro only; its stage is
in shadow in T-TR7a. The spec's "interaction with triage" assumes it runs everywhere. Consequence: the ladder must not depend on
triage output; it reads the run record and the review verdicts, and uses triage's deferrals only where present.

**F7 (Exists/Gap). Cost and time come from the item's runs.** `docs/run-limit-carry-over` (L0-L3 merged) carries a chain budget "a restart cannot reset", but a chain exists **only when a launch names a limit** (D1041), so most items have none. The ladder therefore sums the item's own run records through its intents, and uses the chain's figure where one exists. Orchestrator `WorkItem` has `Attempts`, `Phase`, `RunID`,
`DependsOn` but no first-start time, no cumulative cost, no rung. Consequence: D2038 (resolution): cost and time per PR come from
the sum of the item's own run records (the chain where there is one); the ledger stores only the rung, its reason and the counters the chain cannot give (outer attempts, rung entry times).
`escalation.cost_ceiling` is a ceiling over that sum, never a second meter.

**F8 (Gap). No model grades.** `models.strong` would also read next to `agent.models`, a different thing (per-role). Consequence:
D2039 (resolution): the grades are `model_grades.strong` and `model_grades.strongest` in the project layer
(`schemas/project-layer.schema.json`, `defaults`) and are resolved by the runner to a model plus credential/pool exactly as a
craft's model is; roles point at a grade name. The spec's example names are kept only in `spec.md`. The default coder
"DeepSeek Flash" and the grade defaults "Sonnet 5.5" and "Opus 5.5" are **configuration examples** (`docs/multi-model.md`), not
built-in defaults; the plan ships no model default (a grade that is unset makes the rung refuse, D2039).

**F9 (Gap). A single-pass recipe needs a stop that is not a fix.** `premium` (`roles: {coder: reviewer}`) plus
`review: {max_rounds: 1}` gets close, but findings after the one review still launch a fix. Recipes name no model, so the grade
must arrive through the task or the config, not the recipe. Consequence: a new catalog recipe `escalate` with a stage flag that
ends the run at the review ("hand over"), a task field that names the grade (`model_grade`, closed set `strong|strongest`), and a
runner step that resolves it. Both touch agent configuration and prompts, so they are security-tier tasks.

**F10 (Gap). Rung 0 has no mechanism.** Consequence: D2035 (answered): a cloud run with a rebase-only recipe (rebase onto the
latest base, resolve conflicts, re-run the tests, push the branch); the daemon holds no checkout and no git credentials.

**F11 (Gap). Churn needs a finding identity.** The spec's rule compares findings across rounds. A finding has no id;
`runner.Finding` carries `severity`, `likelihood`, `impact`, `fix_cost`, `class`, `needs_owner`, `file`, `summary`. The record
now has `runstore.TriageEntry.Key` (a hash of file and summary, landed with #2215), but the triage stage fills it in **Startup and
Pro only** and is still in shadow. Consequence: the churn comparator reads that key and is **off in POC, Critical and
Hotfix** (it falls back to the normal path, never a guess); so a reworded finding does not read as "new minor", it also treats
two findings with the same file and class as the same (D1246's rule): a match on either is "same". D2036 (answered): the same finding repeating goes to
rung 2 once and to rung 4 (D1246's split) if rung 2 repeats it; new minor findings every round go straight to rung 3.
"Minor" is `rigor.Blocks`'s answer for the mode, not a new scale.

**F12 (Exists/Gap). Follow-ups mostly exist.** `Task.FollowUpOf` (run id plus finding keys) and `Task.Debt` are in
`internal/task/task.go`; D1849 launches a follow-up only after its parent PR is merged, found through the run's own branch; D1844
gives it the parent's mode. `fugaro followups` is planned. Importance ("important, not blocking" vs "minor") exists nowhere.
Consequence: D2040 (resolution): one new optional field on the task's follow-up block, `importance` (`important|minor`),
written from the merge judge's classification when the judge ran and from a deterministic rule when it did not (security or
data-loss class, or impact 5, is important); the launcher orders important ahead of new work. Adding a field is allowed; no
existing field changes meaning.

**F13 (Exists/Gap). Notification is half-built.** Only the log sink exists; the owner's push is not built (a `docs/stateless-orchestrator` task) and is per
person, not per project. Consequence: "notify the project owner" in Pro and Hotfix means a `decision-needed`-shaped notification
through the existing sink; the plan adds one kind (`merged-needs-review`), and the channel arrives with that task. Critical, and any
non-Routine path set in any mode, raises the D1247 `waiting_on: person` decision (D2037, answered).

**F14 (Rule). One project-level report writer.** `docs/periodic-reviews` E4 ruled one writer, no per-orchestrator scheduler.
Consequence: D2034 (answered): orchestrators write ledger records only; the daily digest gains an **Escalation** section (24 hours
and 7 days). It lists the spec's metrics that the records can answer: rung reached, time to merge by rung, rung 3 merges, their
follow-ups and how many closed, churn detections, longest open PRs, hours and cost past rung 1. **"Regressions traced to a
merge"** has no source in the records today; it is reported as "reverts of an escalation merge in the next 7 days" (a revert PR
naming the merge commit), which is a measurable proxy, and the owner is told that it is a proxy. Threshold suggestions are text
in the report; a person edits the local config (nothing applies one).

**F15 (Exists). "Blocks other tasks" is the reverse of `DependsOn`.** The orchestrator's items already carry `DependsOn`. A
PR blocks others when another **approved**, non-terminal item names its item (a session-class actor can add an unapproved item
with a `DependsOn`, so counting unapproved edges would let an agent halve a PR's thresholds and raise its model grade). Consequence: the critical-path flag is computed each tick from
state, never typed in; it halves the thresholds (`critical_path_factor` default 0.5) and raises the rung 2 grade to `strongest`.

**F16 (Rule). Loosening must not reach a repo file or the project policy.** Mode precedence lets a repo set a looser mode.
Thresholds and merge behaviour are authority, not preference. Consequence: D2032 (answered): the thresholds are **local-only**
keys of the orchestrator (the D1244 pattern: refused at load when the project layer or a repo sets them), a person edits them
through the shared editor (`internal/yamledit`), and the project layer may set only the **grades** (a model choice, validated
like a craft's). `escalation.cost_ceiling` is local too.

**F17 (Exists). The in-run stop is the cousin of rung 3.** At `agent.review_stop_fraction` the runner finalizes with the open
findings and Startup/POC accept "merge as-is, follow up the rest" (`rigor.BlocksAtStop`). Consequence: the ladder is the outer
backstop only; it does not change that stop, and a run that finalized at its stop and is ready is a rung 3 candidate at once.

**F18 (Gap). Per-mode table versus the real review caps.** `internal/rigor/rounds.go` `roundDefaults` give inner/senior rounds
POC 1/1, Startup 2/2, Pro 3/3, Critical 3/5, Hotfix 1/1. The spec's "normal runs before rung 2" (1, 2, 3, 3, 0) is a count of
whole **runs**, not rounds, and the run's default total is 3 hours (`config` `Total.Duration`, `workflows.*.timeouts.total`).
Consequence: the table is the plan's default; a "run" is one record in a chain; wall-clock is the chain's elapsed time.

**F19 (Rule). Anything that edits prompts, recipes, credentials or the agent's environment is security tier.** The judge prompt,
the `escalate` recipe, the grade resolution and the merge call all touch such paths; each is `opus + mandatory independent
review`, per `.claude/rules/credentials-service.md` and the owner's rule (security review is level 3 in-job).

**F20 (Rule). `fugaro update` is the whole upgrade, and it is safe while runs are in flight.** A new local config key with a
default needs no migration. The orchestrator state is not optional-friendly:
`schemas/orchestrator-state.schema.json` says `schema_version` is bumped whenever a field is added, the daemon's decoder refuses a
field it does not model, and a reader that finds a higher version must stop. So the ledger needs **`schema_version` 2 with a
`Migrate`**, which **the daemon runs itself at start-up** (`orchestrator_run.go`: "`fugaro update` no longer touches a node under orchestrator/"; `update` re-renders the unit and the daemon restarts into the migration), and the new block must be listed in `bookkeepingFields` (`state.go`), or `SpecDigest` (it
hashes every `WorkItem` field but those) would read each ledger write as a changed spec and revoke the approval. The deployed
RTDB rules do not pin the node's shape (`.write: false` for a token writer), so the state needs no rules change; **the digest's read of it does** (F27). A new recipe ships in the catalog and is reached by `update`. The release's operator section is "run
`fugaro update`". The plan pins this with a test that an installation at the previous state version converges.

**F21 (Rule). Follow-ups are filed, never approved.** D1226 (stateless-orchestrator): the rule never approves a work item, and
a task needs the owner's own go. The spec's "important follow-ups are prioritised above new work" therefore orders items that
are already approved and sorts the pending ones for the person; it never approves one. Consequence: D2040's `importance` field
sorts and notifies only.

**F22 (Rule/Gap). The stalled PR is a draft, and D1245 merges only a ready PR.** A run that hits its cap ends a draft;
readiness is the runner's and is not configurable (`docs/recipes.md`). Consequence: D2041 (answered, **not the recommendation**):
the daemon may mark a draft ready (`gitprov` `MarkReady`, E8) and then merge, when the judge does not veto and the D1245 gate
passes at the head SHA. This is a **new write authority for the daemon** and an amendment of D1245's "the PR is ready"
condition (T3). The gate is evaluated against the exact head SHA that is readied and merged, and the merge call carries that
SHA, so a push in between refuses it.

**F23 (Rule). The ladder launches runs nobody approved.** D1226: a cycle spawns only the recipe named in an approved item, and
`ApprovalAttempts = 2` (`internal/orchestrator/state.go`) is what one approval covers. R0, R2 and the judge are extra launches
with another recipe and a stronger model grade, each spending money. Consequence: D2042 (answered): the approval of an item
covers the ladder's runs, **one list**: R0 (at most two per item), R1 runs beyond the two an approval already covers, one R2, one `review-only` run per rebased head, one judge; the counts are the bound, and `escalation.cost_ceiling` (no default) bounds the cost where a person sets it; `ApprovalAttempts` counts normal
runs only. This amends D1226 visibly (T4).

**F24 (Wrong in an earlier draft). Provider calls.** `ApplyReady` only adds reviewers and labels on a PR already made ready; it
does not un-draft (`UpdatePR` with `Draft:false` does, and E8 wraps it), and neither it nor any checks read takes a head SHA. Consequence: E8 adds `MarkReady(number, expectedHeadSHA)`
(re-reads the head, refuses on a mismatch, then calls `UpdatePR`), `Merge(number, expectedHeadSHA)` and `Checks(number, sha)`.
A provider whose merge API has no expected-head precondition (Bitbucket's is unchecked, UNVERIFIED) makes the ladder **refuse
to merge** there and raise `waiting_on: person`. A merge never bypasses branch protection (no admin merge).

**F25 (Gap, the headline for the owner). The ladder can merge only `Routine` PRs, which are `docs/` and `website/`.** D1234's allowlist
(`internal/orchestrator/sensitivity/classify.go`, `routineRules`) holds those two trees, "added by a person deciding it". And
Fugaro's own CI runs only when dispatched by hand, so its pull requests report no checks and read as `CIUnknown`, never green.
Consequence: under D2030 and D2037, R3 auto-merges a docs-only PR in a repository that has checks, and **every code PR, and every
PR on Fugaro's own repository, ends R3 with `waiting_on: person` and the judge's verdict attached**. The ladder still ends the
20-40 hour stall (the person gets a ready, judged, rebased PR and one decision), but it does not by itself merge a stalled code
PR. Widening the allowlist or letting the ladder dispatch CI is the owner's call, not this plan's (O3).

**F26 (Gap). The merge credential.** The GitHub provider authenticates with an App installation token (`gitprov.TokenSource`: the
adapter's own mint with the App key, or the credentials service), never a person's login, and the daemon holds no provider today.
An App can sit on a ruleset's bypass list, so "no admin flag" is not "no bypass". Consequence: D2045 is amended: the daemon's
ready and merge calls use the same installation `TokenSource` as the runs, E8 refuses when the actor is on the base's bypass list
or the list cannot be read, and the App needs the permission to merge, which is an owner step (O4, Check 540 states it).

**F28 (Gap, blocks E8 and E10b). The merge credential has no route.** `github.ServiceTokens` needs the run's Firebase ID token
and the job's identity (`internal/gitprov/github/apptoken.go`), and the only person-class route, `internal/credsvc/githubread.go`,
"never mints a write". The daemon holds no `gitprov`. Any write mint the daemon can reach with the person's own Google credential
is equally reachable by a coding-agent session on the same machine, which would then hold a merge-capable token and skip the gate.
Consequence: open item O5 for the owner, and E8's `MarkReady` and `Merge` and all of E10b stay blocked until it is ruled: (a) a
new credentials-service route and caller class for a gated, audited merge mint, (b) the App key on the owner's machine, or (c)
the ladder never merges and always raises `waiting_on: person` (D2030's other option, so the plan's merge half would not be
built). The rest of the plan (R0-R2, the judge as an advisor, the ledger, importance, the digest) does not need it.

**F27 (Gap). The digest job cannot read the ledger.** The ledger sits in each person's `orchestrator/<id>/state` node
(`.read: "USER"`), and the reports job (periodic-reviews E4) is granted read on `runs/` and write on `reports/` only. Consequence:
E12 adds a read grant for the reports job's account on the ledger's escalation block only (a rules change in
`internal/budget/rules/rules.json.tmpl`, with its rules-version bump and its `update` step, a security-tier change), rather than
copying the ledger into the run records.

## Decisions

D2030-D2037, D2041 and D2042 are the owner's answers, given in this conversation on 2026-11-10; each was the recommendation unless said so. D2038-D2040 and D2043-D2046 are resolutions or defaults of the reconciliation, marked as such.

| D | Question | Recommendation and cost | Owner's answer |
| --- | --- | --- | --- |
| D2030 | Who may merge at the end of the ladder (F3)? | The orchestrator daemon only, under D1245's gate (CI green, `Routine` paths, no critical finding); the mode decides notify vs wait. Cost: non-Routine PRs always wait for a person. Alternatives: the ladder never merges (D13 intact, stall persists), or any mode merges without a gate. | **Orchestrator merges under D1245's gate**, 2026-11-10 (the recommendation) |
| D2031 | Role of the merge judge model (F4)? | Advisory: it may only veto and classify follow-ups; eligibility is deterministic code. Cost: it cannot rescue a PR the gate refuses. Alternatives: the model decides; no model. | **Advisory, can only block**, 2026-11-10 (the recommendation) |
| D2032 | Thresholds against `orchestrator.triage_after` (F2, F16)? | The per-mode table replaces `triage_after`, local-only, project layer cannot loosen. Cost: BF2's 2h first look becomes ~6h in Startup. Alternatives: keep 2h and layer rungs on top; project-layer thresholds. | **Ladder replaces `triage_after`**, 2026-11-10 (the recommendation). D1244 is amended by this. |
| D2033 | Names (F5)? | Rungs R0-R4; model grades. Alternatives: keep "Tier"; "levels" (collides with review levels). | **Rungs + model grades**, 2026-11-10 (the recommendation) |
| D2034 | Reports (F14)? | An Escalation section in the one project-level digest. Cost: no per-orchestrator view. Alternative: reopen E4 for per-orchestrator reports. | **Escalation section in the digest**, 2026-11-10 (the recommendation) |
| D2035 | Rung 0 mechanism (F10)? | A cloud run with a rebase-only recipe. Cost: minutes per attempt. Alternative: local git in the daemon (a new trust boundary). | **Cloud run with a rebase recipe**, 2026-11-10 (the recommendation) |
| D2036 | Repeated same finding (F11)? | Rung 2 once, then D1246's split; new-minor churn straight to rung 3. Cost: D1246's first reaction is amended. Alternative: D1246 as ruled. | **Spec order, split as fallback**, 2026-11-10 (the recommendation). D1246 is amended by this. |
| D2037 | Critical and non-Routine at the ceiling (F13)? | Reuse D1247's `waiting_on: person` with the judge's verdict attached. Cost: a Startup PR in a security path waits. Alternative: the mode alone decides (a credentials PR could merge itself). | **Reuse `waiting_on: person`**, 2026-11-10 (the recommendation) |
| D2041 | What may the ladder do with a stalled DRAFT whose gate passes and whose judge does not veto (F22)? | Hand it to a person (`waiting_on: person`); readiness stays the runner's alone. Cost: the most common stalled case still needs one human action. Alternatives: the daemon readies and merges; only a rung 2 run may ready it. | **Daemon marks it ready, then merges**, 2026-11-10 (**not the recommendation**; the owner wants the stalled case closed without a person). Consequence followed through in F22 and T3. |
| D2038 | Cost and time source (F7). | Resolution, one defensible answer: the run chain, not a new meter. | stated, no question |
| D2039 | Grade keys and defaults (F8). | Resolution: `model_grades.strong|strongest` in the project layer; no built-in model; an unset grade makes the rung refuse and ask. | stated, no question |
| D2040 | Follow-up importance (F12). | Resolution: one optional field `importance` on the follow-up block; a deterministic rule when no judge ran. | stated, no question |
| D2042 | What covers the ladder's extra launches (F23)? | The item's approval covers every run the ladder launches (R0 at most twice, R1 runs beyond the two an approval already covers, one R2, one `review-only` per rebased head, one judge); the counts are the bound, the cost ceiling an optional extra. Cost: one approval authorizes a costlier model for that item. Alternatives: each rung needs its own approval; a local switch `auto_launch`. | **Approval covers the ladder**, 2026-11-10 (the recommendation). D1226 is amended. |
| D2043 | May rung 0 merge a rebased head (review finding)? | Resolution, one defensible answer: **never directly.** A merge is eligible only at a head covered by a scored review verdict (with severities, so the gate's "no critical finding" is not fed by the judge), with the path set computed at that head. **Every** R0 head (the rebase run is an agent and can push anything) gets a `review-only` run at that head first; the judge then adds vetoes only. | stated, no question |
| D2044 | A judge that fails, times out or returns nothing (review finding)? | Resolution: an absent, failed, malformed or late verdict is a **veto** (blocked), never a merge. | stated, no question |
| D2045 | Merge credential and branch protection (review finding)? | Resolution: the daemon merges and readies through `gitprov` with the installation token its `TokenSource` mints (F26), never an admin or bypass merge; a base whose protection it cannot satisfy, or whose bypass list it cannot read, refuses and raises `waiting_on: person`. `README.md`, `SECURITY.md`, `docs/design/v1.md` and D13's pages say "Fugaro never merges" and are amended (E0, E14). | stated, no question |
| D2046 | Does `escalation.ready_drafts` ship off (T3)? | My own design call layered on D2041: **default off**, so the owner switches the new authority on once after Check 540. Cost: the stalled-draft case stays with a person until then. | **Recommended default, awaiting the owner's confirmation** (T3). |

D2038-D2040 and D2043-D2046 are resolutions or defaults the owner may overturn at the plan's review; each has one defensible reading and a named task.

## Rows of the spec, as read after this reconciliation

| Spec row | Reads as |
| --- | --- |
| "merge" in rung 0, 2, 3 | "Is merge-eligible under D1245's gate, and the daemon merges"; if the gate refuses, `waiting_on: person`. |
| POC and Startup: auto-merge | Auto-merge under the gate. A non-Routine path set waits for a person (D2037). |
| Pro: auto-merge + notify | Under the gate, plus one notification of kind `merged-needs-review`. |
| Critical: a human approves | Always `waiting_on: person`; nothing merges without the person. |
| Hotfix: auto-merge + notify | Under the gate, plus notification; Hotfix is not triaged (F6) and the judge's veto still applies. |
| Hard ceiling hours | Local keys, one set per mode; the critical-path factor halves them; the cost ceiling triggers the same step. |
| Rung 2 "senior reviewer plus optional security reviewer" | The existing in-job reviewers (L2 senior, L3 adversarial with the security-review craft); rung 2 asks for L3 when the path set is not `Routine`. |
| Dashboard | Out of this plan's tasks: the CLI read (`fugaro escalation`) and the digest section ship; a console screen is a follow-up plan (declined here, F14). |

## Open tensions and open items for the owner

1. **T1: D2032 amends D1244 and D2036 amends D1246.** Both were answered by the owner on 2026-11-08. The new answers are the
   owner's of 2026-11-10 and are recorded as amendments in both places (the plan edits those rows with a pointer); confirm that
   the amendment, not the older text, stands.
2. **T2: the merge judge is a model on a trust boundary the owner has twice ruled "no model".** D2031 limits it to a veto. A
   veto still lets a prompt-injected diff *block* its own merge, which is safe, but an injected diff may also *suppress* the judge's
   veto: the gate (Routine paths, green CI at the head, a scored review, no critical finding) is the only fence, and the judge is not
   one. Check 541 tests the gate with a non-Routine path set; Check 540 holds the judge's own behaviour. Confirm that a veto-only judge is acceptable on the stack's security tier.
3. **O1: "regressions traced to merges" is a proxy (F14).** Confirm "a revert in 7 days" is enough.
4. **O2: console dashboard** is out of this plan (a console plan of its own); confirm.
6. **O3 (F25):** the ladder merges only `Routine` (docs and website) PRs and, with no CI on Fugaro's own repository, none there; decide
   whether to widen D1234's allowlist or let the ladder dispatch CI. **O4 (F26):** the GitHub App needs the permission to merge and
   must not sit on a ruleset bypass list; an owner step. **O5 (F28):** rule the merge credential's route (a, b or c); E8 and E10b
   wait for it. A repository that requires reviews on its base refuses the App's merge, so such a repository always ends
   `waiting_on: person`.
5. **T3: D2041 against D1245 and `docs/recipes.md`.** D1245 merges a ready PR and readiness is "unchanged and not
   configurable". D2041 gives the daemon the right to ready a draft. The two are reconciled by the head-SHA binding and the
   unchanged gate (Routine paths, green CI, no critical finding, judge not vetoing), but D1245's text and the recipes page both
   say readiness is the runner's. State which wins: the plan implements D2041, amends both pages in E0, and keeps the readying
   behind a local switch `escalation.ready_drafts` (default **off**, so the owner turns authority on once, after Check 540).
   Confirm the switch default; without it the ladder is built and inert for drafts.
7. **T4: D2042 amends D1226** (a person's approval covers one recipe). Confirm the amended text: an approval covers the item's
   ladder runs under the cost ceiling, and nothing the ladder files is approved (F21).

## UNVERIFIED

- Which post-review-triage task makes the runner fill `TriageEntry.Key` (the field landed with #2215; the triage stage runs in
  Startup and Pro only); the churn comparator is planned against that field.
- That run-limit-carry-over's chain record exposes cumulative cost to the daemon without a new read path (L0-L3 merged; the
  reader was not opened).
- The exact `periodic-reviews` task that renders the digest (R7-R12 unbuilt): the Escalation section is planned as a section
  added to whichever task lands the daily digest; its id is not confirmed.
- Whether a per-run model grade can be injected into an existing run's `agent.models` without changing `internal/config`'s
  strict decode (read only `schemas/fugaro.schema.json` and `docs/model-crafts`, not the resolver code).
- Platform: GitHub's merge accepts an expected head SHA; Bitbucket's merge API was not checked for one (F24 refuses where it has
  none). The daemon's token and its right to merge under branch protection were not checked.
- The default models named in the spec (Sonnet 5.5, Opus 5.5, DeepSeek Flash): only `docs/multi-model.md` examples were seen.
