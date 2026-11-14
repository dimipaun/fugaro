<!--
How this page is written

1. The **Merged** column holds only the count, tasks done out of tasks in the plan, such as `20/23`; "about" stays only where the count is approximate, written `about 35/45`. Nothing else goes in that cell.
2. Every explanation of what remains goes in the **Left** column.
3. **Left** lists only the tasks still to do and the state of each (in a run, a draft PR #N, waiting on X, owner-run). It never lists finished work; merged tasks are counted in Merged and named nowhere else. A ruling about a task that is still open may stay as a short clause on that task.
4. A spec with merged/total of 0.7 or more sits in "In progress, close to done"; below 0.7 it sits in "In progress, a lot left". For an "about n/m" row, n/m decides.
5. A spec that is in the works (a task of it has a live run or an open pull request, draft or not, or it has merged a task) is never in "To do or parked", which holds only specs with nothing merged, no live run and no open pull request, and the specs the owner has parked by name (a parked spec keeps its merged count in its row and is not worked until the owner unparks it).
6. A pull request is in one of four states, and the four words are the same ones everyone uses: **live** (a run is going on it, which is what rule 3's "in a run" means), **ready** (no run is going, it is not a draft, and the last run that opened or followed it ended with the outcome `ready`, a review job's own outcome `reviewed` changing nothing), **stalled** (it had a run, no run is going and it is not ready — the outcome `draft`, a `[DRAFT]`-titled ordinary pull request on a host with no drafts included, or a ready one a person turned back into a draft) and **orphan** (no run ever produced or follows it). Landing is an action, not a fifth state: to **land** a pull request is to merge it as-is, with the findings it still carries recorded as follow-up tasks, rather than running another fix round (the `landing` skill).
-->

# Fugaro: status of every spec under docs/

As of 2026-11-13, read from `main`, the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. Released: CLI 0.23.1 (Tapir), console 0.122.0 (Strawberry). CLI 0.24.0 (Uakari) waits on Check 191 and the owner's go; CLI 0.25.0 (Vole) and console 0.123.0 (Tangerine) are scoped. Of the 24 open pull requests, 7 are live, 0 ready, 16 stalled and 1 orphan (#2535).

## In progress, close to done

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-credentials | 11/12 | owner Checks 149 and 145, and 148's read half (S2, D1961); T3.3's trust paths (S3 is otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 |
| cli-output-console-graph | 30/32 | owner CL12 and CG12 |
| vision-model | 7/9 | V3 (stalled, draft #2500), V8 not started; owner Checks 520 and 521 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c) |
| settings-commands | about 10/12 | S10, S11 (held, #2280) |
| console-dependency-graph | 15/17 | GV3.2 (stalled, draft #2501) |
| task-governance | 25/31 | 6 left: T3.4 (docs), and the rest in `ee/governance` |
| post-review-triage | 13/14 | owner TR13 |
| plugin-user-scope | 7/10 | U6, U7, E1 |
| mobile | about 41/45 | P5.x, P7.4, P7.5; VS1.3 (D213 approved, no run yet); owner P2.3 and P4.4 |
| stateless-orchestrator | 18/20 | O13, O19 (approved, blocked: a run the workspace refused is not distinguishable in the run record, so it waits on a W-side record change; U12 resolved NO 2026-11-13); owner Checks 301 to 304 and 305 (O16b) |
| issues-resolved-on-update | 6/8 | T-RU7, T-RU8 (stalled, draft #2462) |
| go-to-market | 9/12 | owner G2.5, and two more |
| no-prompt-policy | 13/17 | T11 to T13; owner T14 |
| model-ab-testing | 16/18 | owner Checks 210 to 212 |
| compute-backend | 5/7 | Task 12 is owner-run (Check 57b, and Check 124 for `--provision-batch`); Task 13 only if Task 14's answer needs it |
| model-crafts | 9/10 | owner MC10 |
| run-limit-carry-over | 8/10 | L8, L9 (stalled, draft #2537) |
| run-without-checkout | 6/8 | B2 and C1 |

## In progress, a lot left

| Spec | Merged | Left |
| --- | --- | --- |
| review-levels-ab-jobmax | about 26/40 | A/B reviewer slot, `auto_launch` |
| rigor-modes | about 24/40 | T7.1, T8.1, T8.3; the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open |
| periodic-reviews | 7/14 | R7 to R12; owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| forge-authorship-images | 12/25 | 13 left; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| shared-jobs-default | 0/32 | all 32 tasks: plan only (its docs pull request is an orphan), none approved; owner Checks 191, 192, 580, 581 and 582 |
| bitbucket-workspace-token | 7/13 | T1.4, T2.2, T2.4, T2.6 and T2.7 |
| binary-allow-rule | 5/11 | AR1 and AR3 to AR7, not started |
| outer-loop-escalation | 10/18 | E6 (stalled, draft #2498), E11 (stalled, draft #2497), E10a, E10b, E12 to E14; D2046 ruled 2026-11-13: `escalation.ready_drafts` is on by default; owner Checks 540 to 542 |
| roles-scopes | 7/12 | RS6a, RS6b, RS6c (each stalled after a follow-up run on the independent senior review: drafts #2485, #2484, #2486), RS7 (live, draft #2482), RS10 (waits on RS6a to RS6c and RS7) |
| subscriptions | about 22/51 | the rest not started or open; 10 owner-run; TN1 seat enforcement is not in 0.24.0 (owner) |
| portal | 8/42 | plan merged (#2513); owner P0.1 to P0.4 pending; the open owner decisions were ruled 2026-11-13, in three runs: the Entra directory pin (L3, in a run), the NAT alternative design note (in a run) and the doc rename (in a run); P3.6 (the renew entitlement gate) not started; the rest not started |
| review-job (no spec folder yet) | 3/5 | option C: one review-only job per provider and account so `fugaro review --pr N` works out of the box; C2 to C4 not launched, B-infra and the docs wait |
| secrets-brokering | 25/65 | B1.13 flip (stalled, draft #2166, held on Check 191: the cause is found, no Claude account is admitted for a per-repo Bitbucket job, so repo-level admission is in a run, #2550, and the Check's early failure in a run, #2549), B1.15, B1.16; owner Checks 191 and 192; B2.1–B2.9, B3.2–B3.10, B4.2–B4.11, B5.1–B5.9 (B5.1 live, #2545; all approved 2026-11-10, each with its own gate; owner Checks 193–197); dynamic-delivery subfolder: DS1.5 (stalled, draft #2453), owner DS0.3 (Checks 510 and 511) |
| rigor-command | 3/10 | R6 (docs), R7 (the hotfix gate; stalled, draft #2538), W (`setup` asks the rigor mode); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |
| mobile-auth | 4/7 | P3, P5, P6 (stalled, draft #2536); P2 was already in main and P4 merged; nothing blocks the rest |
| account-aliases | 2/5 | AL1 (live, draft #2544), AL3 and AL4, all approved 2026-11-10; D2090–D2102 answered as recommended; owner Check 550 not run |
| licence-key-v2 | 11/26 | K2.4 (stalled, draft #2522); K3.2 (stalled, draft #2528); K2.x renewal (workspace id, subscription id, paid-term gate, revoke the chain; orphan draft #2535); K2.6 and K2.7 approved, waiting on K2.4; the rest of K0–K7 not started or open; D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| composite-commands | 1/10 | depends on no-prompt-policy |

## To do or parked

| Spec | State |
| --- | --- |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans | no plan or not a spec |

## Done

| Spec | Left |
| --- | --- |
| config-management | none |
| troubleshoot-issue | none |
| catalog-build-context | none |
| security-classification | none |
| image-setup-skill | owner Checks 500 and 501 |
| contract-first-decoupling | none |
| workspaces | none |
| diagnose-agent | owner Checks 410 to 412 |
| compatibility-matrix | owner CM4 (the read of the copy) |
| mcp-server | M9 parked (D936, the fence declined, no release schedules it); M11 declined (2026-11-09) |
| api-key-review | owner Check 330 (AK8); AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
| console-managed-screens | none (console 0.121.0) |
| console-settings-edit | none (console 0.120.0) |
| console-coordinator-v2 | owner Check C0 (profiling) |
| managed-agent-converge | none |
| models-commands | none |
| project-image-catalog | none |
| project-workflow-secrets | none |
| accounts-add | owner checks LC1 to LC4 |
| c39-review-identity | owner live check |
| issue-mine | owner Checks 470 to 472 |
| image-env | owner Check 291 |
| setup-wizard | owner Checks 27 and 111 |
| ee-reorg | owner E3.4; E4.1 deferred |
| failure-classes | owner Check 350 |
| website | W3.3 explainer video (parked), owner Check 69 |
| cloud-senior-review | CSR1 to CSR16 done; CSR17 and CSR18 moved to api-key-review |
| coordinator | P7 and P8 re-planned under mcp-server |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| how-to | not a spec |
| console-windows | owner Check 119 |
| short-run-ids | owner SR13 |
| website-changelog | WC4 lands with the next release |
| central-orchestrator | superseded by stateless-orchestrator |
| windows-cli | superseded by console-windows |
