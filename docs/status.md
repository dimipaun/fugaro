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
| workspaces | 9/10 | W9 (in a run, #2421) |
| diagnose-agent | 9/10 | T-DG10 (draft #2376) |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 |
| cli-output-console-graph | 28/32 | CG13b (draft #2415) and CG13c (CG13 was split in three, #2313); owner CL12 and CG12 |
| troubleshoot-issue | 16/18 | T-IS9 (docs), T-IS12 (draft #2384) |
| licensing | 20/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); L5.3 (the pure-MIT build, draft #2363) |
| settings-commands | about 10/12 | S10, S11 (held, #2280) |
| console-dependency-graph | 14/17 | GV3.1, GV3.2 |
| task-governance | 25/31 | 6 left: T3.4 (docs), and the rest in `ee/governance` |
| post-review-triage | 11/14 | TR8 (in a run, #2429), TR12; owner TR13 |
| image-setup-skill | 11/14 | IS2 (draft #2398), IS8b (draft #2171, waits on O3), IS13 not started |
| mobile | about 35/45 | P5.x, P7.2 (draft #2391), P7.3 (draft #2392), P6.8 (draft #2395), P7.4, P7.5; VS1.1 (draft #2397), VS1.2 and VS1.3 after it (D213 approved); owner P2.3 and P4.4 |
| stateless-orchestrator | 15/20 | O11 (draft #2386), O13, O14 (ruled: split in three slices); O16b (approved, waits on the MCP wiring), O19 (approved); owner Checks 301 to 304; 305 reserved for O16b |
| issues-resolved-on-update | 6/8 | T-RU7, T-RU8 |
| go-to-market | 9/12 | owner G2.5, and two more |
| no-prompt-policy | 12/17 | T10 to T13; owner T14 |

## In progress, a lot left

| Spec | Merged | Left |
| --- | --- | --- |
| catalog-build-context | 3/5 | CB4 (draft #2374), CB5 |
| security-classification | 7/11 | SC3, SC5, SC10 (in a run, #2427), SC11 |
| review-levels-ab-jobmax | about 25/40 | A/B reviewer slot, `/jobmax` skill (J1.1 in a run, #2424), `auto_launch` |
| model-ab-testing | 11/18 | A3.3 (draft #2403), A5.2 (draft #2396), A5.3, A5.4, A6.1; owner Checks 210 to 212 |
| rigor-modes | about 23/40 | T7.1, T7.2, T8.1, T8.3; the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open |
| compute-backend | 4/7 | Task 10 (the enable of `backend: cloud-batch`, with the `doctor` gate; draft #2325); Task 12 is owner-run (Check 57b, and Check 124 for `--provision-batch`) after it; Task 13 only if Task 14's answer needs it; `backend: cloud-batch` is still refused until Task 10 |
| model-crafts | 6/10 | MC6 (draft #2389, with AK9 and AK10), MC7, MC9; owner MC10 |
| run-limit-carry-over | 6/10 | L6 (draft #2413), L7 onward waiting |
| plugin-user-scope | 5/10 | U3 (draft #2388) and the rest |
| run-without-checkout | 4/8 | A4 (draft #2402), B1, B2 and C1 |
| periodic-reviews | 7/14 | R7 to R12; owner Checks 136 and 340 |
| forge-authorship-images | 11/25 | 14 left; C1 is in a run (#2428); the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| vision-model | 4/9 | V4 (in a run, #2425), V6 (in a run, #2430), V3, V7, V8 not started; owner Checks 520 and 521 not run |
| bitbucket-workspace-token | 7/13 | T1.4, T2.2, T2.4, T2.6 and T2.7 |
| binary-allow-rule | 4/11 | AR0 (draft #2202) held on the update-ordering fix; AR1 and AR3 to AR7 wait behind it |
| outer-loop-escalation | 7/18 | E2 to E4, E6, E10a, E10b, E11 to E14; owner Checks 540 to 542 |
| roles-scopes | 4/12 | RS4 (draft #2399), RS8 (draft #2401), RS5 (`fugaro user`, not started), RS6a to RS6c, RS7, RS10 |
| subscriptions | about 21/51 | S3.4 (draft #2382) and the rest not started or open; 10 owner-run; TN1 seat enforcement is not in 0.24.0 (owner) |
| secrets-brokering | 20/65 | B1.13 flip (draft #2166, held on Check 191), B1.15, B1.16; owner Checks 191 and 192; one more Check 191 script fix (draft #2370); B2.1–B2.9, B3.1–B3.10, B4.1–B4.11, B5.1–B5.9 (all approved 2026-11-10, each with its own gate; owner Checks 193–197), B3.1 (in a run, #2431) and B4.1 (in a run, #2426); dynamic-delivery subfolder: DS2.3 (draft #2383), DS3.1 (draft #2162), owner DS0.3 (Checks 510 and 511) |
| rigor-command | 3/10 | R6 (docs), R7 (the hotfix gate), W (`setup` asks the rigor mode); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |
| mobile-auth | 2/7 | parked |
| repo-image-removal | 2/11 | **PARKED** by image-setup (D1900) |
| account-aliases | 1/5 | AL0 (draft #2373) and the rest of AL0–AL4, all approved 2026-11-10; D2090–D2102 answered as recommended; owner Check 550 not run |
| licence-key-v2 | 3/26 | K0–K7 left: K1.3, K1.4, K2.1, K2.2 and K3.1 in runs (#2420, #2418, #2422, #2419, #2423); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation); TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
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
| compatibility-matrix | owner CM4 (the read of the copy) |
| mcp-server | M9 parked (D936, the fence declined, no release schedules it); M11 declined (2026-11-09) |
| api-key-review | owner Check 330 (AK8); AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
| console-managed-screens | none (console 0.121.0) |
| console-settings-edit | none (console 0.120.0) |
| console-coordinator-v2 | owner Check C0 (profiling) |
| managed-agent-converge | none; `managed apply`'s audit record is open (draft #2354) |
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
| coordinator | P7 and P8 re-planned under mcp-server; P9f in a run (#2432) |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| how-to | not a spec |
| console-windows | owner Check 119. The first real Windows unit run failed 484 tests, its fix chain is not planned yet |
| short-run-ids | owner SR13 |
| website-changelog | WC4 lands with the next release |
| central-orchestrator | superseded by stateless-orchestrator |
| windows-cli | superseded by console-windows |
