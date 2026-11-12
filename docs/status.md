# Fugaro: status of every spec under docs/

As of 2026-11-12, read from `main`, the open pull requests and each plan's task list (not from the plans' own status lines, which are stale in about 30 folders). Counts come from commit titles and file checks; "about" marks the approximate ones. **Owner-run** means a live check or step that only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. CLI 0.23.0 (Tapir) is being cut as this page is written and is not tagged yet.

## How this page is written

1. The **Merged** column holds only the count, tasks done out of tasks in the plan, such as `20/23`; "about" stays only where the count is approximate, written `about 35/45`. Nothing else goes in that cell.
2. Every explanation of what remains goes in the **Left** column.
3. **Left** lists only the tasks still to do and the state of each (in a run, a draft PR #N, waiting on X, owner-run). It never lists finished work; merged tasks are counted in Merged and named nowhere else. A ruling about a task that is still open may stay as a short clause on that task.
4. A spec with merged/total of 0.7 or more sits in "In progress, close to done"; below 0.7 it sits in "In progress, a lot left". For an "about n/m" row, n/m decides.
5. A spec that is in the works (a task of it has a live run or an open pull request, draft or not, or it has merged a task) is never in "To do or parked", which holds only specs with nothing merged, no live run and no open pull request.

## In progress, close to done

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-credentials | 11/12 | owner Checks 149 and 145, and 148's read half (S2, D1961); T3.3's trust paths (S3 is otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 |
| cli-output-console-graph | 29/32 | CG13c (draft #2459; CG13 was split in three, #2313); owner CL12 and CG12 |
| troubleshoot-issue | 17/18 | T-IS9 (docs) |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c) |
| settings-commands | about 10/12 | S10, S11 (held, #2280) |
| console-dependency-graph | 14/17 | GV3.1 (in a run, #2474), GV3.2 |
| task-governance | 25/31 | 6 left: T3.4 (docs), and the rest in `ee/governance` |
| post-review-triage | 12/14 | TR12 (in a run, #2473); owner TR13 |
| image-setup-skill | 13/14 | IS13 (draft #2466) |
| mobile | about 39/45 | P5.x, P7.4, P7.5; VS1.2 (draft #2455) and VS1.3 after it (D213 approved); owner P2.3 and P4.4 |
| stateless-orchestrator | 16/20 | O13, O14 (ruled: split in three slices; O14c in a run); O16b (approved, waits on the MCP wiring), O19 (approved); owner Checks 301 to 304; 305 reserved for O16b |
| issues-resolved-on-update | 6/8 | T-RU7, T-RU8 (draft #2462) |
| go-to-market | 9/12 | owner G2.5, and two more |
| no-prompt-policy | 12/17 | T10 (draft #2461), T11 to T13; owner T14 |
| catalog-build-context | 4/5 | CB5 |
| security-classification | 8/11 | SC3, SC5, SC11 (in a run, #2469) |
| model-ab-testing | 13/18 | A5.4 (draft #2460), A6.1 (in a run, #2471); owner Checks 210 to 212 |
| compute-backend | 5/7 | Task 12 is owner-run (Check 57b, and Check 124 for `--provision-batch`); Task 13 only if Task 14's answer needs it |
| model-crafts | 8/10 | MC9 (in a run, #2468); owner MC10 |
| run-limit-carry-over | 7/10 | L7 (in a run, #2478), L8, L9 |
| run-without-checkout | 6/8 | B2 and C1 |

## In progress, a lot left

| Spec | Merged | Left |
| --- | --- | --- |
| review-levels-ab-jobmax | about 26/40 | A/B reviewer slot, `auto_launch` |
| rigor-modes | about 24/40 | T7.1, T8.1, T8.3; the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open |
| plugin-user-scope | 6/10 | U4, U6, U7, E1 |
| periodic-reviews | 7/14 | R7 to R12; owner Checks 136 and 340 |
| forge-authorship-images | 12/25 | 13 left; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| vision-model | 6/9 | V7 (draft #2451), V3 and V8 not started; owner Checks 520 and 521 not run |
| bitbucket-workspace-token | 7/13 | T1.4, T2.2, T2.4, T2.6 and T2.7 |
| binary-allow-rule | 5/11 | AR1 and AR3 to AR7, not started |
| outer-loop-escalation | 8/18 | E3 (in a run, #2476), E4 (in a run, #2475), E6, E10a, E10b, E11 to E14; owner Checks 540 to 542 |
| roles-scopes | 6/12 | RS5 (`fugaro user`, draft #2465), RS6a to RS6c, RS7, RS10 |
| subscriptions | about 22/51 | the rest not started or open; 10 owner-run; TN1 seat enforcement is not in 0.24.0 (owner) |
| secrets-brokering | 22/65 | B1.13 flip (draft #2166, held on Check 191), B1.15, B1.16; owner Checks 191 and 192; B2.1–B2.9, B3.2–B3.10, B4.2–B4.11, B5.1–B5.9 (all approved 2026-11-10, each with its own gate; owner Checks 193–197); dynamic-delivery subfolder: DS1.5 (draft #2453), DS2.4 (in a run, #2470), DS4.1 (in a run, #2479), DS4.2 (in a run, #2472), owner DS0.3 (Checks 510 and 511) |
| rigor-command | 3/10 | R6 (docs), R7 (the hotfix gate), W (`setup` asks the rigor mode); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |
| mobile-auth | 2/7 | parked |
| repo-image-removal | 2/11 | **PARKED** by image-setup (D1900) |
| account-aliases | 2/5 | AL1, AL3 and AL4, all approved 2026-11-10; D2090–D2102 answered as recommended; owner Check 550 not run |
| licence-key-v2 | 8/26 | K0–K7 left: K2.3 (draft #2457) and K2.5 (draft #2458); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation); TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| composite-commands | 1/10 | depends on no-prompt-policy |

## To do or parked

| Spec | State |
| --- | --- |
| voice-module, collateral, backlog, plans | no plan or not a spec |

## Done

| Spec | Left |
| --- | --- |
| config-management | none |
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
| coordinator | P7 and P8 re-planned under mcp-server; P9c in a run (#2477) |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| how-to | not a spec |
| console-windows | owner Check 119 |
| short-run-ids | owner SR13 |
| website-changelog | WC4 lands with the next release |
| central-orchestrator | superseded by stateless-orchestrator |
| windows-cli | superseded by console-windows |
