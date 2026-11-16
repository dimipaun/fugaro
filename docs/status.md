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

As of 2026-11-16, read from `main`, the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. Released: CLI 0.25.0 (Vole, tagged and published 2026-11-15; its website promotion succeeded), console 0.122.0 (Strawberry). Console 0.123.0 (Tangerine) is scoped and not yet tagged. Main's CI was green on `5efa4faa8`; the runs on `3e0835368` (#2908) were red from the writers table's golden (fixed by #2911), and #2913 is the latest red-main fix; no run has been taken on the head at the count. There are 35 open pull requests (this one excepted): 28 drafts and 7 not drafts (#2166, #2851, #2877, #2878, #2900, #2904, #2905). Whether each is live, ready or stalled was not re-measured in this sweep, so a row names a pull request as open or a draft and no more. This sweep reads the 64 commits merged since the sweep of #2836.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-token-types | 18/19 | CK (Checks 590 and 591) owner-run |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 |
| feature-matrix | 10/12 | T6 (owner-run, never dispatched), then CR1 (counted in the 12, raised only if T6 raises it); D2440–D2449 answered; no check taken; no after-deploy step |
| go-to-market | 9/12 | owner G2.5, and two more |
| licence-key-v2 | 19/26 | K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); the rest of K0–K7 not started or open (K3.4 held on TN1); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| outer-loop-escalation | 13/18 | E10b, E12 to E14, E15 (owner-run), not started; D2046 ruled 2026-11-13: `escalation.ready_drafts` is on by default; owner Checks 540 to 542 |
| periodic-reviews | 11/14 | R9 and R10; owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| task-governance | 27/31 | 4 left, all in `ee/governance`; T7.3 is live (draft #2817) |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| binary-allow-rule | 6/11 | AR3 to AR7, not started; all approved 2026-11-15 |
| bitbucket-workspace-token | 8/13 | T1.4, T2.2, T2.4 and T2.7; T2.4 and its phase-3 half rewritten for the proxy (a shared Bitbucket job holds no token) and approved to launch 2026-11-16 |
| composite-commands | 2/10 | AP3 (live, draft #2816); AP4 to AP10 not started, AP5 and AP6 waiting on AP3; depends on no-prompt-policy |
| config-ownership | 5/22 | all tasks approved 2026-11-15; 17 left: R1 (open PR #2904), S1 (open PR #2851), M1 (draft #2854), A1 (draft #2852), N3 (draft #2845); N2 and N4 are free to start; W1, W2, W3 and K1 on composite AP5, A2 and K2 on composite AP6; M3 and M4 on drafts #2576 and #2538; then I1 and D1; D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| forge-authorship-images | 14/25 | 11 left, all agent tasks approved 2026-11-16 (owner-run A5 and the unscheduled C8b/C8c stay out); C5b and C5c follow the merged C5a; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| forge-labels | 5/9 | T3 (the credentials service's label route; draft #2841), T4, T6 and T7 (T7 and Check 640 wait on the daemon's identity to the credentials service (E10b)); owner Check 640 |
| mobile | about 46/67 | P6.7 and P6.13 (free now that P4.1 has merged), P4.2 (Checks 80 and 83 to 88 are not in the register); P5.1b (draft #2888), P5.1c to P5.1g (approved 2026-11-14, not started; P5.1f no longer waits on P4.1), P5.2.4 and P5.2.6 to P5.2.10 (approved 2026-11-14, not started), P5.3.3 (open PR #2900), P5.3.5 and P5.3.7 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| model-crafts | 15/26 | MC14 (open PR #2905), MC17 to MC26 (MC18 draft #2881, MC20 open PR #2877, MC22 open PR #2878), the activation plan for the four known limits, all approved 2026-11-16 (MC26 stays owner-run; D1660–D1671 and D1673 are still "Not answered", the plan is built on their recommendations; D1672 answered 2026-11-15: A, then B and C together in one release, then D); owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| portal | 21/42 | owner P0.1 to P0.6; not started: P1.7, P2.2 (draft #2859), P2.4, P3.2, P3.6 (the renew entitlement gate), P4.2, P4.3, P5.1, P5.2, P6.1 to P6.3, P7.1, P9.3, P9.4 |
| repos-management | 0/7 | RE0 (live, draft #2828); RE1 to RE5 not started; RE6 owner-run; all approved 2026-11-15 and D2400–D2417 answered the same day; RE3 adopts draft #2690 (live, the earlier brief's RE1) |
| review-job | 5/10 | RJ8 (live, draft #2671), RJ4 (live, draft #2784); RJ5, RJ9, RJ10 not started and not approved; owner Checks 610 and 611 |
| review-levels-ab-jobmax | about 27/40 | A/B reviewer slot; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; B1.2 (stalled, draft #2771) and B1.4 (live, draft #2660) are open; X1 waits on T3.1 |
| rigor-command | 4/10 | R7 (the hotfix gate; stalled, draft #2538), W (`setup` asks the rigor mode; live, draft #2576); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |
| rigor-modes | about 24/40 | T7.1, T8.1, T8.3; the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open |
| secrets-brokering | 31/65 | B1.13 flip (held, open PR #2166, held on Check 191, which the owner is rerunning now that repo-level account admission and the model-free refusal attempts are on main), B1.15, B1.16; owner Checks 191 and 192; B2.3 (draft #2907)–B2.9, B3.3 and B3.5–B3.10, B4.2–B4.11, B5.2 (draft #2874)–B5.9 (all approved 2026-11-10, each with its own gate; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511) |
| shared-jobs-default | 12/32 | 20 left; S9 (draft #2869), B5 (draft #2896) and B9 (draft #2857) are open, the rest not started; owner Checks 191, 192, 580, 581 and 582 |
| subscriptions | about 22/51 | the rest not started or open, all agent tasks approved 2026-11-16 (counsel- and entity-gated tasks and the 10 owner-run stay gated); TN1 seat enforcement is not in 0.24.0 (owner) |

## To do or parked

| Spec | State |
| --- | --- |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| spend-accuracy | 0/23: plan only, no task approved; D2640–D2651 answered 2026-11-15 (D2643 not the recommendation; four amended after review); OR1 and Checks 650–651 owner-run |
| troubleshoot-cloud | 0/12: plan only (spec, reconciliation and plan merged as #2723), no task approved; T-TC10 is owner-run |
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
