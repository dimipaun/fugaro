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

As of 2026-11-15, read from `main`, the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. Released: CLI 0.23.1 (Tapir), console 0.122.0 (Strawberry). CLI 0.24.0 (Uakari) waits on Check 191 and the owner's go; CLI 0.25.0 (Vole) and console 0.123.0 (Tangerine) are scoped. There are 47 open pull requests (this one excepted); the live, stalled and orphan breakdown was not recomputed for this sweep. Ten runs were live when this page was last counted: eight on a pull request and two (a docs run and a CLI run) that had not opened theirs yet.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3; the cascade's empty candidate list answered as `state_unavailable` (stalled, draft #2713) |
| go-to-market | 9/12 | owner G2.5, and two more |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c) |
| mobile | about 44/58 | P5.1 (split into tasks in drafts #2701 and #2703), P5.2 (design, draft #2687), P5.3.1 to P5.3.7 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| no-prompt-policy | 16/17 | T13 (draft #2707, being landed), the T11 follow-up (draft #2706, being landed); owner Checks 380 to 382 (T14 merged, scripts on main) |
| stateless-orchestrator | 18/20 | O13 (stalled, draft #2696), O19 (approved, blocked: a run the workspace refused is not distinguishable in the run record, so it waits on a W-side record change; U12 resolved NO 2026-11-13); owner Checks 301 to 304 and 305 (O16b) |
| task-governance | 26/31 | 5 left, all in `ee/governance` |
| vision-model | 8/9 | V8 not started; owner Checks 520 and 521 not run |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| account-aliases | 3/5 | AL3 (stalled, drafts #2608 and #2653), AL4, both approved 2026-11-10; D2090–D2102 answered as recommended; owner Check 550 not run |
| binary-allow-rule | 5/11 | AR1 and AR3 to AR7, not started |
| bitbucket-token-types | 5/19 | a follow-up on the merged C1 ended ready (draft #2612); T5 (stalled, draft #2588), T7 (stalled, draft #2593); T3, T4, T8 to T14, T16, I1 not started; CK (Checks 590 and 591) owner-run |
| bitbucket-workspace-token | 7/13 | T1.4, T2.2, T2.4, T2.6 and T2.7 |
| composite-commands | 1/10 | depends on no-prompt-policy |
| forge-authorship-images | 12/25 | 13 left; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| licence-key-v2 | 14/26 | K2.7 (stalled, draft #2600), K3.5 (stalled, #2607), K4.1 (stalled, drafts #2606 and #2650); K2.6 superseded by the portal; orphan draft #2535 is the earlier copy of the renewal work that #2564 merged; the rest of K0–K7 not started or open; D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| outer-loop-escalation | 11/18 | E6 (stalled, draft #2498), E10a (stalled, drafts #2609 and #2655), E10b, E12 to E14; D2046 ruled 2026-11-13: `escalation.ready_drafts` is on by default; owner Checks 540 to 542 |
| periodic-reviews | 7/14 | R7 to R12; owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| portal | 15/42 | owner P0.1 to P0.6; P2.1 (stalled, draft #2569), P2.3 (stalled, drafts #2603 and #2652); not started: P1.7, P2.2, P2.4, P3.1 to P3.3, P3.5, P3.6 (the renew entitlement gate), P4.1 to P4.3, P5.1, P5.2, P6.1 to P6.3, P7.1, P9.3, P9.4 |
| review-job | 4/10 | RJ8 (live, rework run ac0b, draft #2671); RJ2 approved (owner 2026-11-14); RJ4, RJ5 not started and not approved; owner Checks 610 and 611 |
| review-levels-ab-jobmax | about 26/40 | A/B reviewer slot, `auto_launch` |
| rigor-command | 3/10 | R6 (docs), R7 (the hotfix gate; stalled, draft #2538), W (`setup` asks the rigor mode; stalled, draft #2576); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |
| rigor-modes | about 24/40 | T7.1, T8.1, T8.3; the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open |
| secrets-brokering | 26/65 | B1.13 flip (stalled, draft #2166, held on Check 191, which the owner reruns now that repo-level account admission is on main), B1.15, B1.16; owner Checks 191 and 192; B2.1–B2.9, B3.2–B3.10, B4.2–B4.11, B5.1–B5.9 (B5.1 stalled, draft #2545; all approved 2026-11-10, each with its own gate; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511) |
| shared-jobs-default | 1/32 | 31 left: C2 (stalled, draft #2582), the rest not started; owner Checks 191, 192, 580, 581 and 582 |
| subscriptions | about 22/51 | the rest not started or open; 10 owner-run; TN1 seat enforcement is not in 0.24.0 (owner) |

## To do or parked

| Spec | State |
| --- | --- |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans | no plan or not a spec |

## Done

| Spec | Left |
| --- | --- |
| accounts-add | owner checks LC1 to LC4 |
| api-key-review | owner Check 330 (AK8); AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
| bitbucket-credentials | owner Check 149; 148's read half parked by the owner 2026-11-14 until the coordinator job is deployed (`fugaro update --firebase-project <id> --coordinator`) (Check 145 ran 2026-11-14: U3 shows the read token reaches past its workspace; the owner accepted the reach, D360 and D361 stand) (S2, D1961); T3.3's trust paths (S3 is otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| c39-review-identity | owner live check |
| catalog-build-context | none |
| central-orchestrator | superseded by stateless-orchestrator |
| cli-output-console-graph | owner Checks 310 (the script is on main; a scratch-mode change is in flight as run c310), 311 (the script is not on main yet; in flight as run ee98), 313 and 314 (scripts on main), not yet run; Check 312 passed 2026-11-13 |
| cloud-senior-review | CSR1 to CSR16 done; CSR17 and CSR18 moved to api-key-review |
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
| ee-reorg | owner E3.4; E4.1 deferred |
| failure-classes | owner Check 350 |
| how-to | not a spec |
| image-env | owner Check 291 |
| image-setup-skill | owner Checks 500 and 501 |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| issue-mine | owner Checks 470 to 472 |
| issues-resolved-on-update | none |
| managed-agent-converge | none |
| mcp-server | M9 parked (D936, the fence declined, no release schedules it); M11 declined (2026-11-09) |
| mobile-auth | owner live checks, numbered Checks 620 to 627 on main (draft #2700's 601 to 608 was closed); P5's D231 gap is a follow-up awaiting the owner's decision — revoking the last device empties the node, so the next publish re-seeds the revoked entry |
| model-ab-testing | owner Checks 210 to 212 (written on main, not run; drafts #2634 and #2637 were closed as superseded) |
| model-crafts | owner Checks 420 to 422 (written as one script each; not run) |
| models-commands | none |
| plugin-user-scope | E1, the release itself (rides the next CLI release; no operator step) |
| post-review-triage | owner TR13 (Checks 490 to 492; the scripts are in a run) |
| project-image-catalog | none |
| project-workflow-secrets | none |
| roles-scopes | owner live check that a viewer cannot launch or cancel (not yet run) |
| run-limit-carry-over | owner-run live check (named, not numbered: a chain that halts at its dollar cap, continued with `--continues`, reports a smaller remaining than it started with) |
| run-without-checkout | no live check covers a launch from outside a checkout |
| security-classification | none |
| settings-commands | none |
| setup-wizard | owner Checks 27 and 111 |
| short-run-ids | owner SR13 |
| troubleshoot-issue | none |
| website | W3.3 explainer video (parked), owner Check 69 |
| website-changelog | WC4 lands with the next release |
| windows-cli | superseded by console-windows |
| workspaces | none |
