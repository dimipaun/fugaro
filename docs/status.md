# Fugaro: status of every spec under docs/

As of 2026-11-10, read from `main`, the open pull requests and each plan's task list (not from the plans' own status lines, which are stale in about 30 folders). Counts come from commit titles and file checks; "about" marks the approximate ones. **Owner-run** means a live check or step that only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end.

## In progress, close to done

| Spec | Merged | Left |
| --- | --- | --- |
| licensing | 17/23 | owner-run only |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 |
| console-dependency-graph | 13/17 | GV3.1, GV3.2, GV4.1 |
| bitbucket-credentials | 9/12 | T2.6, T2.11; owner Checks 149 and 145, and 148's read half (S2, D1961); S3 superseded by the secrets-brokering proxy chain (2026-11-10), except T3.3's trust paths; Check 147 retired (D1960) and T2.6 unblocked (D1962); D1960–D1962 answered 2026-11-10 as recommended (its reconciliation-secrets-brokering.md) |
| mcp-server | 8/12 | M4 store (draft #2117), M5b (draft #2114, a follow-up in a run); M9 parked (D936, the fence declined): no release schedules it |
| contract-first-decoupling | 4/7 | CD4, CD5, CD7 |
| task-governance | 24/31 | 7 left: T3.4 (docs), and the rest in `ee/governance` |
| mobile | about 35/45 | P5.x, P7.2 to P7.5; owner P2.3 and P4.4 |
| troubleshoot-issue | 12/18 | IS6 (draft #2135), IS19 (draft #2115), T-IS7, T-IS9, T-IS11, T-IS12 |
| compatibility-matrix | 2/4 | CM3, CM4; CM2 (the page) merged |
| rigor-command | 3/10 | R6 (docs), R7 (the hotfix gate), W (`setup` asks the rigor mode); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |

## In progress, a lot left

| Spec | Merged | Left |
| --- | --- | --- |
| stateless-orchestrator | 12/20 | O9 (draft #2193), O12 (draft #2290), O11, O13, O14; O16a (approved, launchable after #1994, dormant), O16b (approved, waits on the MCP wiring), O19 (approved, waits on W7 #2275); D2020-D2022 answered as recommended 2026-11-10; owner Checks 301 to 304 (300 PASSED 2026-11-10); 305 reserved for O16b |
| api-key-review | 6/9 | AK6b (draft #2172), AK7; owner Check 330. AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) and are no longer counted here |
| secrets-brokering | 18/30 | B1.13 flip (draft #2166, held on Check 191), B1.15, B1.16, B2.1 to B5.1; owner Checks 191 and 192. Dynamic-delivery subfolder: DS0.1, DS1.3 and DS2.0 merged, DS2.1 in a run |
| vision-model | plan only, 0/9 built (V0–V8); not started, nothing approved; owner Checks 520 and 521 not run |
| subscriptions | about 17/51 | about 22 tasks; 10 owner-run |
| workspaces | 6/10 | W3 and W6 (drafts), W7, W9 |
| no-prompt-policy | 9/17 | T4 and T7 (drafts), T9 (draft #2232) to T13; owner T14; T15 merged, and `update` asks nothing (#2082) |
| image-setup-skill | 5/14 | IS10 merged; IS1 (draft #2170), IS8b and others in draft PRs; IS2, IS12, IS13 not started |
| diagnose-agent | 5/10 | T-DG7 (draft #2138, a follow-up in a run), T-DG6, T-DG8 to T-DG10 |
| periodic-reviews | 7/14 | R7 to R12; owner Checks 136 and 340 |
| config-management | 5/14 | G3 to G7, G9 rest, G10, G12, G13 |
| cli-output-console-graph | 19/30 | CL6, CL9 to CL11, CG4 to CG6, CG13 |
| issues-resolved-on-update | 3/8 | T-RU4 to T-RU8 |
| catalog-build-context | 2/5 | CB2, CB4, CB5 |
| post-review-triage | 6/14 | TR3, TR6 (in a run), TR7 to TR10, TR12; TR5 merged, BF1m (#1999) merged |
| outer-loop-escalation | 0/18 | plan only, no task approved (E0 to E16 with E10a and E10b); owner Checks 540 to 542 |
| model-crafts | 4/10 | MC4 to MC7, MC9; owner MC10 |
| security-classification | 6/11 | SC3, SC5, SC9 to SC11 |
| settings-commands | about 6/12 | S6, S8 to S11; S7 closed 2026-11-10 as a duplicate of config-management's G7 |
| rigor-modes | about 22/40 | T7.1, T7.2, T8.1, T8.3; overlay plan LV1 to LV7 essentially unstarted |
| roles-scopes | 3/12 | RS3 (draft #1994), RS4, RS5, RS6a to RS6c, RS7, RS8, RS10; RS5 (`fugaro user`) not started |
| review-levels-ab-jobmax | about 24/40 | A/B reviewer slot, `/jobmax` skill, `auto_launch` |
| forge-authorship-images | 9/25 (A1, A2a, A2b, A3, A4, C3, C4, C7, E1); C8's C8a built; E0 closed as superseded (roles-scopes) | 15; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 4/12 | the cost-estimate half (`fugaro estimate`) |
| model-ab-testing | 3/18 | the sampler and the pair launch |
| bitbucket-workspace-token | 7/13 | T1.4, T2.2, T2.4, T2.6 and T2.7 left; T2.9 closed as replaced by the secrets-brokering proxy chain (D1981, 2026-11-10); T2.11's step 5 now a dated amendment to the shipped 0.14.0 operator notes (D1984); T2.6's three corrections landed (D1983); D1980–D1984 answered 2026-11-10 as recommended (its reconciliation-secrets-brokering.md) |
| run-limit-carry-over | 4/10 | L0 to L3 merged, L4 in a run, L5 onward waiting |
| binary-allow-rule | 2/11 | NV1 and NV2 merged, AR0 (draft #2202) held on the update-ordering fix; AR1 and AR3 to AR5 wait behind it |
| plugin-user-scope | 1/10 | U1 merged, U2 in a run; `fugaro upgrade` is still on main |
| run-without-checkout | 3/8 | A0 to A2 merged, A3 (draft #2233) in a run, A4 behind it |

## To do or parked

| Spec | State |
| --- | --- |
| composite-commands | 1/10; depends on no-prompt-policy |
| account-aliases | plan only, 0/5 (AL0–AL4), all approved 2026-11-10 and sequenced after #2274, #2247 and #2252; D2090–D2102 answered as recommended; owner Check 550 not run |
| mobile-auth | 2/7; parked |
| repo-image-removal | **PARKED** (2/11; parked by image-setup, D1900) |
| voice-module, collateral, backlog, plans | no plan or not a spec |

## Done

| Spec | Left |
| --- | --- |
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
| coordinator | 7/9; P7 and P8 re-planned under mcp-server |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| how-to | not a spec |
| console-windows | owner Check 119; T-H4 merged (#2175). The first real Windows unit run failed 484 tests, its fix chain is not planned yet |
| short-run-ids | owner SR13; SR12 merged (#2168) |
| website-changelog | WC4 lands with the next release |
| central-orchestrator | superseded by stateless-orchestrator |
| windows-cli | 10/31; superseded by console-windows |
