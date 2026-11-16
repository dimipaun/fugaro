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

As of 2026-11-16 evening, read from `main`, the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. Released: CLI 0.25.0 (Vole, tagged and published 2026-11-15; its website promotion succeeded; the last CLI tag), console 0.122.0 (Strawberry). Console 0.123.0 (Tangerine) is scoped and not yet tagged. The 0.26.0 gate is triage-loud (merged #2829, #2849 and #2910), repos-management RE0 to RE5 (RE0 merged), composite-commands AP3 to AP6 (AP3 open, draft #2816) and config-ownership; nothing else is claimed done for it. Main is red: CI run 43924820725, on `e2ca125f2` (#2943), failed in five accounts-key tests; no pull request fixing it was found open when this was written, so the fix is in progress and unnamed here. The red-main fixes already merged are #2913, #2915, #2916 and #2958. There are 40 open pull requests (this one excepted): 32 drafts and 8 not drafts (#2166, #2931, #2939, #2941, #2947, #2954, #2956, #2960). Whether each is live, ready or stalled was not re-measured in this sweep, so a row names a pull request as open or a draft and no more, except where a follow-up run is known to have been launched (#2690, #2784, #2816, #2869). This sweep reads the 44 commits merged since the sweep of #2914.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-token-types | 18/19 | CK (Checks 590 and 591) owner-run |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 |
| feature-matrix | 10/12 | T6 (owner-run, never dispatched), then CR1 (counted in the 12, raised only if T6 raises it); D2440–D2449 answered; no check taken; no after-deploy step |
| forge-labels | 8/9 | T7 and owner Check 640, both waiting on the daemon's identity to the credentials service (E10b, draft #2928) |
| go-to-market | 9/12 | owner G2.5, and two more |
| licence-key-v2 | 19/26 | K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); the rest of K0–K7 not started or open (K3.4 held on TN1); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| model-crafts | 20/26 | MC19, MC21 (draft #2946), MC23 (draft #2957), MC24 (draft #2955), MC25, MC26 (stays owner-run) and the activation plan for the four known limits, all approved 2026-11-16 (D1660–D1671 and D1673 are still "Not answered", the plan is built on their recommendations; D1672 answered 2026-11-15: A, then B and C together in one release, then D); owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| outer-loop-escalation | 14/18 | E10b (draft #2928), E12 (draft #2942), E14, E15 (owner-run), not started unless a draft is named; D2046 ruled 2026-11-13: `escalation.ready_drafts` is on by default; owner Checks 540 to 542 |
| periodic-reviews | 12/14 | R9; owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| task-governance | 27/31 | 4 left, all in `ee/governance`; T7.3 is live (draft #2817) |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| binary-allow-rule | 7/11 | AR4 to AR7, not started; all approved 2026-11-15 |
| bitbucket-workspace-token | 8/13 | T1.4, T2.2, T2.4 and T2.7; T2.4 and its phase-3 half rewritten for the proxy (a shared Bitbucket job holds no token) and approved to launch 2026-11-16 |
| composite-commands | 2/10 | AP3 (draft #2816, a follow-up run launched); AP4 to AP10 not started, AP5 and AP6 waiting on AP3; depends on no-prompt-policy |
| config-ownership | 8/22 | all tasks approved 2026-11-15; 14 left: S1 (open PR #2851), A1 (draft #2852); N2 and N4 are free to start; W1, W2, W3 and K1 on composite AP5, A2 and K2 on composite AP6; M3 and M4 on drafts #2576 and #2538; then I1 and D1; D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| forge-authorship-images | 16/25 | 9 left, all agent tasks approved 2026-11-16 (owner-run A5 and the unscheduled C8b/C8c stay out); C5c follows the merged C5a and C5b; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| mobile | about 46/67 | P6.7 (draft #2927) and P6.13 (free now that P4.1 has merged), P4.2 (Checks 80 and 83 to 88 are not in the register); P5.1b (draft #2888), P5.1c to P5.1g (approved 2026-11-14, not started; P5.1f no longer waits on P4.1), P5.2.4 and P5.2.6 to P5.2.10 (approved 2026-11-14, not started), P5.3.3 (draft #2900), P5.3.5 and P5.3.7 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| portal | 23/42 | owner P0.1 to P0.6; P1.7 (draft #2936) and P4.2 (draft #2929) are open; not started: P2.4, P3.2, P3.6 (the renew entitlement gate), P4.3, P5.1, P5.2, P6.1 to P6.3, P9.3, P9.4 |
| repos-management | 1/7 | RE1 (draft #2932), RE2 (draft #2935), RE3 (open PR #2939) and the earlier brief's RE1 (draft #2690, a follow-up run launched); RE4 and RE5 not started; RE6 owner-run; all approved 2026-11-15 and D2400–D2417 answered the same day; RE3 adopts #2690 |
| review-job | 5/10 | RJ8 (draft #2671), RJ4 (draft #2784, a follow-up run launched); RJ5, RJ9, RJ10 not started and not approved; owner Checks 610 and 611 |
| review-levels-ab-jobmax | about 27/40 | A/B reviewer slot; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; B1.2 (stalled, draft #2771) and B1.4 (live, draft #2660) are open; X1 waits on T3.1 |
| rigor-command | 4/10 | R7 (the hotfix gate; stalled, draft #2538), W (`setup` asks the rigor mode; live, draft #2576); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |
| rigor-modes | about 24/40 | T7.1, T8.1, T8.3; the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open |
| secrets-brokering | 33/65 | B1.13 flip (held, open PR #2166, held on Check 191, which the owner is rerunning now that repo-level account admission and the model-free refusal attempts are on main), B1.15, B1.16; owner Checks 191 and 192; B2.4–B2.9, B3.3 and B3.5–B3.10, B4.2–B4.11, B5.3–B5.9 (all approved 2026-11-10, each with its own gate; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511) |
| shared-jobs-default | 14/32 | 18 left; S9 (draft #2869, a follow-up run launched) and B3 (draft #2940) are open, the rest not started; owner Checks 191, 192, 580, 581 and 582 |
| spend-accuracy | 10/23 | 13 left, all agent tasks approved 2026-11-16: SA1.2 (open PR #2947), SA1.3 (draft #2948) and SA5.0 (open PR #2956) are open, the rest not started; D2640–D2651 answered 2026-11-15 (D2643 not the recommendation; four amended after review); OR1 and Checks 650–651 owner-run |
| subscriptions | about 22/51 | the rest not started or open, all agent tasks approved 2026-11-16 (counsel- and entity-gated tasks and the 10 owner-run stay gated); TN1 seat enforcement is not in 0.24.0 (owner) |
| troubleshoot-cloud | 1/12 | 11 left, all agent tasks approved 2026-11-16; T-TC10 is owner-run |

## To do or parked

| Spec | State |
| --- | --- |
| orchestrator-graph-estimates | 0/39: plan only; no task approved; D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); Checks 700–703 owner-run |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans | no plan or not a spec |

## Done

| Spec | Left |
| --- | --- |
| account-aliases | owner Check 550, not run; D2090–D2102 answered as recommended |
| accounts-add | owner checks LC1 to LC4 |
| api-key-review | owner Check 330 (AK8); AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
| bitbucket-credentials | owner Check 149; 148's read half parked by the owner 2026-11-14 until the coordinator job is deployed (`fugaro update --firebase-project <id> --coordinator`) (Check 145 ran 2026-11-14: U3 shows the read token reaches past its workspace; the owner accepted the reach, D360 and D361 stand) (S2, D1961); T3.3's trust paths (S3 is otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| c39-review-identity | owner live check (Check 530; its script, merged as #2626, is on main and not yet run) |
| catalog-build-context | none |
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
| diagnose-agent | owner Checks 410 to 412 |
| ee-reorg | owner E3.4 (Checks 200 and 201, scripts on main, not yet run); E4.1 deferred |
| failure-classes | owner Check 350 |
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
| roles-scopes | owner live check that a viewer cannot launch or cancel (not yet run) |
| run-limit-carry-over | owner-run live check (named, not numbered: a chain that halts at its dollar cap, continued with `--continues`, reports a smaller remaining than it started with) |
| run-without-checkout | no live check covers a launch from outside a checkout |
| security-classification | none |
| settings-commands | none |
| setup-wizard | owner Checks 27 (script on main) and 111 |
| short-run-ids | owner SR13 (Check 440 as a script and Check 441 as the register's steps, on main, not yet run) |
| stateless-orchestrator | owner Checks 301 to 304 and 305 (O16b) |
| troubleshoot-issue | none |
| vision-model | owner Checks 520 and 521, not run |
| website | W3.3 explainer video (parked), owner Check 69c (not written yet; 69a and 69b passed 2026-11-15) |
| website-changelog | WC4 lands with the next release |
| windows-cli | superseded by console-windows |
| workspaces | none |
