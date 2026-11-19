<!--
How this page is written

1. The **Merged** column holds only the count, tasks done out of tasks in the plan, such as `20/23`; "about" stays only where the count is approximate, written `about 35/45`. Nothing else goes in that cell.
2. Every explanation of what remains goes in the **Left** column.
3. **Left** lists only the tasks still to do and the state of each (in a run, a draft PR #N, waiting on X, owner-run). It never lists finished work; merged tasks are counted in Merged and named nowhere else. A ruling about a task that is still open may stay as a short clause on that task.
4. A spec with merged/total of 0.7 or more sits in "Nearly There (NT)"; below 0.7 it sits in "Work in progress (WIP)". For an "about n/m" row, n/m decides.
5. A spec that is in the works (a task of it has a live run or an open pull request, draft or not, or it has merged a task) is never in "To do or parked", which holds only specs with nothing merged, no live run and no open pull request, and the specs the owner has parked by name (a parked spec keeps its merged count in its row and is not worked until the owner unparks it).
6. A pull request is in one of four states, and the four words are the same ones everyone uses: **live** (a run is going on it, which is what rule 3's "in a run" means), **ready** (no run is going, it is not a draft, and the last run that opened or followed it ended with the outcome `ready`, a review job's own outcome `reviewed` changing nothing), **stalled** (it had a run, no run is going and it is not ready — the outcome `draft`, a `[DRAFT]`-titled ordinary pull request on a host with no drafts included, or a ready one a person turned back into a draft) and **orphan** (no run ever produced or follows it). Landing is an action, not a fifth state: to **land** a pull request is to merge it as-is, with the findings it still carries recorded as follow-up tasks, rather than running another fix round (the `landing` skill).
7. In every table the specs are sorted alphabetically by name, in ascending order (case-insensitive); a row moved between tables is put in its place there.
-->

# Fugaro: status of every spec under docs/

As of 2026-11-18 14:50 UTC (afternoon), read from `main` at `0d2fe54fc` (#3150), the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. Released: CLI 0.26.0 (tagged `v0.26.0` at `271063335`, #3189, and published 2026-11-18 14:24 UTC; its gate was fully merged), console 0.123.0 (Tangerine, tagged `console-v0.123.0`, published 2026-11-18 05:11 UTC), CLI 0.25.0 (Vole) and console 0.122.0 (Strawberry) before them. Console 0.124.0 (Ugli) has its whole scope merged and is not tagged: the tag awaits the owner's go. CLI 0.27.0 is the next release (scope ruled 2026-11-17; its gate is secrets-brokering, graph-estimates with the orchestrator, config-ownership with composite-commands, and troubleshoot-cloud, periodic-reviews and model-crafts). RE6 (to be renumbered Check 670, Checks 670 to 679, by the owner's ruling of 2026-11-18) and Check 192 (the `--after-flip` half of the B1.13 flip, #2166, merged 2026-11-17; its operator steps are items 9 and 10 of `docs/releases/v0.26.0.md`) are owner-run after the release and are not a gate; the renumber pull request is not merged yet, so the repository still reads Check 620 for RE6 and troubleshoot-cloud's T-TC10 (to become Check 680, Checks 680 to 689). Issue #1865 (vision-model) carries the 0.26.0 milestone: its code merged in 0.25.0 and only owner Checks 520 and 521 are not run. **Main is green as of its last dispatched run.** CI run 44320104577 on `271063335` (the v0.26.0 release commit) succeeded; one commit (#3150) has merged since without a dispatched run. The 403 (`Failed to authenticate`) that ended a night's runs between 04:14 and 04:36 UTC was fixed by the owner, so those notes are gone: where the same task was relaunched and merged under another pull request, the old draft is a stale duplicate (named below), and no run has touched the others since. GitHub reported 13 open Dependabot vulnerabilities, 5 of them high, in the previous read; not re-read this sweep. The secrets-brokering flip chain B2.7, B3.8, B4.9 and B5.7 is approved to launch now, one at a time, with its owner checks moved to after-flip runs; B2.7 is the ready pull request #3152 (a continuation follow-up is running on it); Check 195 gates only B4.9's flip, Check 580 gates only shared-jobs-default M2, and composite AP7 launches after the flips (owner, 2026-11-18). There are 23 open pull requests, read from `gh pr list` at 14:45 UTC and from the Fugaro status line in each body (no `fugaro runs ls`, which is unreliable): 22 are drafts and #3152 is not a draft. **10 are live** (a run is going): #3147 (E15), #3152 (B2.7), #3166 (P6.2), all three on continuation follow-ups, and the plan tasks and fix rounds #3180 (the `--retry` refusal brief), #3181 (B4.5), #3182 (T-TC8), #3183 (M7), #3184 (B4), #3186 (S10) and #3188 (the senior-review fix round of the merged S6 fix, run `50da`). **13 are stalled**, none ready: #3137 (B9: its last status line records tests failing on `04c7927`, and a later commit at 04:26 UTC is not covered by it), #3139 (P5.3.5: tests failing on `b30fc7f` at its last status line), #3140 (R9: tests passed on `f7cf507`, two senior-review findings recorded), #3141 (A2: the agent made no commits), #3170 (B3.8: its run ended in the first stage, no work on the branch) and #3187 (config-ownership W1: tests passed on `bd69dbd`, the review stage failed because claude exited without a result), plus seven stale duplicates whose work is already merged under another pull request: #3148 (F19, merged as #3177), #3159 (the S6 fix, #3174), #3161 (`fugaro followups` finding its git credential, #3172), #3162 (portal P2.4 follow-up, #3175), #3163 (mobile P5.1d follow-up, #3176), #3164 (portal P9.3 follow-up, #3171) and #3165 (portal P6.1 follow-up, #3173). Orchestrator work: the orchestrator is installed under launchd and replaces `/jobmax`. This sweep reads the 28 commits merged since the status commit of 04:35 UTC (`312a3957a`) against each plan's task list: orchestrator-graph-estimates B2, E3, G4, R3 and S4, shared-jobs-default M6 and S7, config-ownership K2, review-levels-ab-jobmax B1.3, secrets-brokering B4.3 and B4.4, troubleshoot-cloud T-TC7, review-job RJ9 and RJ10, forge-labels CK, and the catalog-build-context Check 292 script (#3145), as well as the daemon's identity F19 (#3177, outside any count), the S6 fix (#3174), `fugaro followups` (#3172) and the follow-ups of portal and mobile (#3171, #3173, #3175, #3176), which are findings of landed pull requests and not plan tasks. Rows that could not be re-derived are marked "about" or say so: forge-authorship-images, licence-key-v2 (not reconciled), subscriptions (the owner's census of 2026-11-17, not re-derived), review-levels-ab-jobmax, rigor-modes, secrets-brokering and mobile; credentials-service is Done by the owner's ruling.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-workspace-token | 10/12 | T1.4 and T2.7 (owner-run: Checks 180 and 181); T2.2 was carried by shared-jobs-default S1 and S4, both merged; T2.9 is closed as replaced by the service's rules, D1981, and is not counted in the 12 |
| composite-commands | 7/10 | AP7 (the audit of "now run X" texts: launches after the secrets-brokering flips, owner 2026-11-18), AP9 and AP10, all not started; depends on no-prompt-policy |
| forge-authorship-images | about 20/25 | the tasks still open are not re-derived (the plan's own State lines date from 2026-11-10): owner-run A5 (Check 230) and the unscheduled C8b/C8c stay out; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 11/12 | owner G2.5 |
| licence-key-v2 | about 20/26 | not reconciled against the plan in this sweep, so the count is about; K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); K4.2 merged as #2873, the rest of K0–K7 not started or open (K3.4 held on TN1); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| mobile | about 54/67 | P5.1d's launch half (the resident pass merged as #3115; the launch itself is not delivered, a product and scope decision for the owner), P5.1f (held: it collides with P5.1d on `orchestrator_serve.go`) and P5.1g (approved 2026-11-14, not started), P5.2.6 to P5.2.10 (approved 2026-11-14, not started), P5.3.5 (draft #3139, stalled), P5.3.7 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| model-crafts | 25/26 | MC26 (stays owner-run) and the activation plan for the four known limits, all approved 2026-11-16; F27, D1660–D1671 and D1673 were all answered as recommended on 2026-11-17, and D1672 on 2026-11-15 (A, then B and C together in one release, then D), so no decision is open; owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| orchestrator-graph-estimates | 35/39 | 4 left, all approved 2026-11-16: B4 (draft #3184, live), B9 (draft #3137, stalled), R5 (not started: it waited on G4, merged, and still waits on W1) and W1 (not started); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); Checks 700–703 owner-run |
| outer-loop-escalation | 17/18 | E15 (the owner-run Checks 540, 541 and 542 written as scripts: draft #3147, live: a continuation follow-up is running on it); `escalation.ready_drafts` ships on by default (#3146, D2046) and the daemon already reads it (#3149 found it wired and changed nothing); owner Checks 540 to 542 |
| periodic-reviews | 13/17 | R9 (draft #3140, stalled with tests passing on `f7cf507`: the owner answered E6 on 2026-11-17, a designated repository plus a folder set by the owner in the project layer, public allowed, the launcher hands the bytes in), R15 and R16 (not started) and R5 (owner-run); owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| portal | 32/42 | owner P0.1 to P0.6; P6.2 (draft #3166, live: a continuation follow-up is running on it; its last status line left a level 2 finding in round 2); not started: P5.2, P6.3 and P9.4, which wait on P6.2 |
| repos-management | 6/7 | RE6 (owner-run: its Check is renumbered to Check 670 by the owner's ruling, and the renumber pull request is not merged yet); #2690 was closed 2026-11-17 as superseded by RE1 to RE3 (#2932, #2935, #2939); all approved 2026-11-15 and D2400–D2417 answered the same day |
| review-levels-ab-jobmax | about 30/35 | U4 is reported unreliable until the owner runs Check 211; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded and not counted in the 35, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; X1 waits on T3.1; the count is about because the remaining tasks of the slot are not all named here |
| shared-jobs-default | 27/32 | 5 left: M7 (draft #3183, live) and S10 (draft #3186, live); M2, which waits for owner Check 580 (the script is merged, #3121); M4 and M5 not started (M5 consumes M2); the senior-review fix round of the merged S6 fix is draft #3188, live; owner Checks 191, 192, 580, 581 and 582 |
| subscriptions | about 36/50 | a census of 2026-11-17 found 34 of 41 agent tasks done before S7.1 and S7.3 merged the same day (#3046, #3048), which makes 36, with the 10 owner-run (none run) in the 50; S1.7 is superseded and not counted. Open agent tasks: S0.7 (the app's first release, mobile-v0.200.0), S1.9 (waits on the enforcement release) and S2.9 (the production public key, which waits on owner S3.8), and about one more the census did not name; owner-run: S1.10 (Check 178, deferred), S3.7, S3.8, S6.4, S7.4 and S8.1–S8.6 (counsel closed S8.4 by the owner's ruling of 2026-11-17, recorded by #3042); TN1 seat enforcement is not in 0.24.0 (owner). The count rests on that census and was not re-derived task by task |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| config-ownership | 15/22 | best-effort for 0.26.0 and not in its gate (owner, 2026-11-17); all tasks approved 2026-11-15; 7 left: W1 (draft #3187, stalled: tests passed, the review stage failed), W2, W3 and K1 (not started; they waited on composite AP5, which has merged), A2 (draft #3141, stalled: the agent made no commits), I1 and D1 (not started); D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| rigor-modes | about 27/40 | T8.2 (owner-run: Check 127, written); the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open; the count is about because the overlay's tasks are not all counted |
| secrets-brokering | about 45/65 | B1.16; owner Check 192 (the --after-flip half; the flip itself merged as #2166 on 2026-11-17); the flip chain B2.7 (ready pull request #3152, with a continuation follow-up running), B3.8 (draft #3170, stalled), B4.9 and B5.7, approved to launch one at a time with the owner checks moved to after-flip runs (owner, 2026-11-18); B2.6, B2.8 and B2.9; B3.7, B3.9 and B3.10; B4.2 (owner-run), B4.5 (draft #3181, live) and B4.6–B4.11 (owner Check 195 gates only B4.9's flip); B5.6, B5.8 and B5.9 (all approved 2026-11-10; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511); the count is the previous sweep's 43 plus B4.3 (#3156) and B4.4 (#3168) and is about because the plan's 65 does not reconcile with the 23 tasks named here |
| troubleshoot-cloud | 9/12 | 3 left, all agent tasks approved 2026-11-16: T-TC8 (draft #3182, live), T-TC9 (not started); T-TC10 is owner-run (its Check is renumbered to Check 680 by the owner's ruling; the renumber pull request is not merged yet) |

## To do or parked

| Spec | State |
| --- | --- |
| console-improvements | plan only: 0/46 tasks merged, none approved (docs/console-improvements); owner-run Checks 710 (re-record the Live and Graph goldens), 711 (read timings) and 712 (the wizard on a scratch project) |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans | no plan or not a spec |
| workspace-projects-users | plan only, 0/26 merged; WU0 (the threat model and its independent security review) approved by the owner 2026-11-18 to launch once the plan is on `main`; every other task waits on WU0's findings and its own go |

## Done

| Spec | Left |
| --- | --- |
| account-aliases | owner Check 550, not run; D2090–D2102 answered as recommended |
| accounts-add | owner checks LC1 to LC4 |
| api-key-review | owner Check 330 (AK8); AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
| binary-allow-rule | none (AR7 merged as #3077 on 2026-11-17; all approved 2026-11-15) |
| bitbucket-credentials | owner Check 149; 148's read half parked by the owner 2026-11-14 until the coordinator job is deployed (`fugaro update --firebase-project <id> --coordinator`) (Check 145 ran 2026-11-14: U3 shows the read token reaches past its workspace; the owner accepted the reach, D360 and D361 stand) (S2, D1961); T3.3's trust paths (S3 is otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| bitbucket-token-types | owner Checks 590 and 591 (scripts on main), not run |
| c39-review-identity | owner live check (Check 530; its script, merged as #2626, is on main and not yet run) |
| catalog-build-context | issue #1738 (milestone 0.26.0): the gate flip is merged (#3080) and shipped in 0.26.0; the script that writes Check 292 is merged (#3145); owner Check 292 runs after it, on the sandbox installation only |
| central-orchestrator | superseded by stateless-orchestrator |
| cli-output-console-graph | owner Checks 310 (with its `--scratch` mode), 311, 313 and 314 (all scripts on main), not yet run |
| cloud-senior-review | CSR17 and CSR18 moved to api-key-review |
| compatibility-matrix | owner CM4 (the read of the copy) |
| compute-backend | declared operational by the owner 2026-11-15; owner Checks 57b (its stranded-job submission fixed) and 124, not yet run (Task 13 was dropped) |
| config-management | none |
| console-coordinator-v2 | owner Check C0 (profiling) |
| console-dependency-graph | owner Check 123 (the graph in the packed, signed macOS app, GV2.6), not yet run |
| console-managed-screens | none (console 0.121.0) |
| console-settings-edit | none (console 0.120.0) |
| console-windows | owner Check 119 |
| contract-first-decoupling | none |
| coordinator | P7 and P8 re-planned under mcp-server |
| credentials-service | none, Done by the owner's ruling and not re-counted here (no agent task is left; the owner-run Checks 91, 92, 94, 95, 96 and 99 are the owner's to run or record (Check 90 ran 2026-10-21), and Check 96 holds C2.3's flip, which waits on the deployed service minting a token) |
| diagnose-agent | owner Checks 410 to 412 |
| ee-reorg | owner E3.4 (Checks 200 and 201, scripts on main, not yet run); E4.1 deferred |
| failure-classes | owner Check 350 |
| feature-matrix | T6 is done (the owner read the preview on 2026-11-17, no footnote, O1 answered) and CR1 is not raised, so it is not counted; the owner's mapping corrections merged as #3047 (mapping only); D2440–D2449 answered; no check taken; no after-deploy step |
| forge-labels | owner Check 640 (its script is merged, #3155; not run) |
| how-to | not a spec |
| image-env | owner Check 291 (script on main, not yet run) |
| image-setup-skill | owner Checks 500 and 501 |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| issue-mine | owner Checks 470 to 472: all ran 2026-11-14 and none has passed in full (470 passed in part, its issue-record read and two human steps unmeasured; 471 failed its cache-cleared row; 472 passed on an empty store, not at size), so re-runs are owner-run |
| issues-resolved-on-update | none |
| managed-agent-converge | none |
| mcp-server | M9 parked (D936, the fence declined, no release schedules it); M11 declined (2026-11-09) |
| mobile-auth | owner live checks, numbered Checks 620 to 627 on main (draft #2700's 601 to 608 was closed); P5's D231 gap is a follow-up awaiting the owner's decision — revoking the last device empties the node, so the next publish re-seeds the revoked entry |
| model-ab-testing | owner Checks 210 to 212 (written on main, not run; drafts #2634 and #2637 were closed as superseded) |
| models-commands | none |
| no-prompt-policy | owner Checks 380 to 382 (scripts on main), not yet run; the T11 verification runs found nothing to fix (#2728, #2729) |
| plugin-user-scope | E1, the release itself (rides the next CLI release; no operator step) |
| post-review-triage | owner TR13 (Checks 490 to 492; the scripts are on main, not yet run) |
| project-image-catalog | none |
| project-workflow-secrets | none |
| review-job | owner Checks 610 and 611 (scripts on main, #3150 and #3154), not run |
| rigor-command | none (R2 to R5 were closed 2026-11-10 as duplicates of settings-commands and config-management, which win; every live task has merged) |
| roles-scopes | owner live check that a viewer cannot launch or cancel (not yet run) |
| run-limit-carry-over | owner-run live check (named, not numbered: a chain that halts at its dollar cap, continued with `--continues`, reports a smaller remaining than it started with) |
| run-without-checkout | no live check covers a launch from outside a checkout |
| security-classification | none |
| settings-commands | none |
| setup-wizard | owner Checks 27 (script on main) and 111 |
| short-run-ids | owner SR13 (Check 440 as a script and Check 441 as the register's steps, on main, not yet run) |
| spend-accuracy | OR1 and Checks 650–651 (script written, #2971) owner-run; D2640–D2651 answered 2026-11-15 (D2643 not the recommendation; four amended after review); SA1.4, SA2.3 and SA6.1 merged 2026-11-17 (#2969, #2974, #3053) |
| stateless-orchestrator | owner Checks 301 to 305 (O16b; 301 deferred); the orchestrator is installed under launchd and replaces /jobmax, and its state is stale (goal not set) |
| task-governance | owner Checks 137 and 138 (scripts on main, #3087), not run |
| troubleshoot-issue | none |
| vision-model | owner Checks 520 and 521, not run; issue #1865 carries the 0.26.0 milestone: its code merged in 0.25.0 and only these two checks remain |
| website | W3.3 explainer video (parked), owner Check 69c (not written yet; 69a and 69b passed 2026-11-15) |
| website-changelog | WC4 lands with the next release |
| windows-cli | superseded by console-windows |
| workspaces | none |
