# Fugaro: status of every spec under docs/

As of 2026-11-11, read from `main`, the open pull requests and each plan's task list (not from the plans' own status lines, which are stale in about 30 folders). Counts come from commit titles and file checks; "about" marks the approximate ones. **Owner-run** means a live check or step that only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end.

## In progress, close to done

| Spec | Merged | Left |
| --- | --- | --- |
| licensing | 17/23 | owner-run only |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 |
| console-dependency-graph | 13/17 | GV3.1, GV3.2, GV4.1 |
| bitbucket-credentials | 11/12 | owner Checks 149 and 145, and 148's read half (S2, D1961; its script landed with T2.6 and T2.11 in #2270); S3 superseded by the secrets-brokering proxy chain (2026-11-10), except T3.3's trust paths; Check 147 retired (D1960) and T2.6 unblocked (D1962); D1960–D1962 answered 2026-11-10 as recommended (its reconciliation-secrets-brokering.md) |
| contract-first-decoupling | 6/7 | CD5 (in a run, #2273) |
| task-governance | 25/31 | 6 left: T3.4 (docs), and the rest in `ee/governance`; T8.3 merged (#2209) |
| mobile | about 35/45 | P5.x, P7.2 to P7.5; owner P2.3 and P4.4 |
| troubleshoot-issue | 15/18 | T-IS9 (docs), T-IS11, T-IS12; T-IS6 (#2135), T-IS19 (#2115) and T-IS7 (#2192) merged |
| rigor-command | 3/10 | R6 (docs), R7 (the hotfix gate), W (`setup` asks the rigor mode); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |
| compute-backend | 4/7 | Task 10 (the enable of `backend: cloud-batch`, with the `doctor` gate) is in a run; Task 12 is owner-run (Check 57b, and Check 124 for `--provision-batch`) after it; Task 13 only if Task 14's answer needs it. Built: Tasks 8, 9, 11 and 14 (the opener, the reaper role and `fugaro reaper`, the seal's carrier refusal); `backend: cloud-batch` is still refused until Task 10 |

## In progress, a lot left

| Spec | Merged | Left |
| --- | --- | --- |
| stateless-orchestrator | 12/20 | O9 (draft #2193), O12 (draft #2290), O11, O13, O14; O16a (approved, launchable after #1994, dormant), O16b (approved, waits on the MCP wiring), O19 (approved, waits on W7 #2275); D2020-D2022 answered as recommended 2026-11-10; owner Checks 301 to 304 (300 PASSED 2026-11-10); 305 reserved for O16b |
| secrets-brokering | 18/65 | B1.13 flip (draft #2166, held on Check 191), B1.15, B1.16; owner Checks 191 and 192; the later slices planned 2026-11-10 as B2.1–B2.9, B3.1–B3.10, B4.1–B4.11, B5.1–B5.9 (all approved 2026-11-10, each with its own gate; D2070–D2080 answered as recommended; owner Checks 193–197). Dynamic-delivery subfolder, 9/22: DS0.1, DS0.2, DS1.1, DS1.2, DS1.3, DS1.6, DS2.0, DS3.3 and DS3.5 (#2288) merged; DS1.4 (draft #2250), DS2.1 (#2237) and DS3.1 (#2162) open; owner DS0.3 (Checks 510 and 511). Check 191's script fixes are open (#2308 draft, #2309) |
| vision-model | 1/9 | V1 merged (#2305); V0 open (#2304); V2 to V8 not started; owner Checks 520 and 521 not run |
| subscriptions | about 18/51 | about 21 tasks, among them S6.3 (#2277) and S3.2 (draft #2283); 10 owner-run; S6.2 merged (#2279) |
| workspaces | 8/10 | W7 (#2275), W9; W3 (#1947) and W6 (#2105) merged |
| no-prompt-policy | 11/17 | T9 (draft #2232) to T13; owner T14; T4 (#2084), T7 (#1984) and T15 merged, and `update` asks nothing (#2082) |
| image-setup-skill | 9/14 | IS1 (draft #2170), IS8b (draft #2171, waits on O3); IS2, IS12, IS13 not started; IS3, IS4, IS5, IS7, IS8, IS9, IS10, IS11 and IS11b merged |
| diagnose-agent | 7/10 | T-DG8 to T-DG10; T-DG6 (#2244) and T-DG7 (#2138) merged |
| periodic-reviews | 7/14 | R7 to R12; owner Checks 136 and 340 |
| config-management | 8/14 | G6 (draft #2265), G7, G9 rest, G10 (draft #2281), G12 (draft #2245), G13 (draft #2266); G3 (#2190), G4 (#2221) and G5 (#2264) merged |
| cli-output-console-graph | 24/30 | CL9 and CG5 (in runs), CL11 (waits on CL9), CG13 (not approved); owner CL12 and CG12; CL6 (#2208), CL10 (#2210), CG4 (#2207) and CG6 (#2205) merged |
| issues-resolved-on-update | 5/8 | T-RU5 (draft #2251), T-RU7, T-RU8; T-RU4 (#2187) and T-RU6 (#2216) merged |
| catalog-build-context | 2/5 | CB2 (draft #2255), CB4, CB5 |
| post-review-triage | 7/14 | TR7a (draft #2256), TR7b, TR8, TR9, TR10 (#2246), TR12; owner TR13; TR3, TR5 and TR6 merged (#2236), BF1m (#1999) merged |
| outer-loop-escalation | 1/18 | E1 merged (#2301); E0, E2 to E14 (with E10a and E10b) and E16 left; owner Checks 540 to 542 |
| model-crafts | 4/10 | MC4 (#2252), MC5 (draft #2274; MC5a merged), MC6, MC7, MC9; owner MC10 |
| security-classification | 6/11 | SC3, SC5, SC9 (draft #2296), SC10, SC11 |
| settings-commands | about 6/12 | S6 (#2247), S8 (draft #2282), S9, S10, S11 (#2280, held: its read half is not on `main`); S7 closed 2026-11-10 as a duplicate of config-management's G7 |
| rigor-modes | about 22/40 | T7.1, T7.2, T8.1, T8.3; overlay plan LV1 to LV7 essentially unstarted |
| roles-scopes | 3/12 | RS3 (draft #1994, a follow-up in a run), RS4, RS5, RS6a to RS6c, RS7, RS8, RS10; RS5 (`fugaro user`) not started |
| review-levels-ab-jobmax | about 24/40 | A/B reviewer slot, `/jobmax` skill, `auto_launch` |
| forge-authorship-images | 9/25 (A1, A2a, A2b, A3, A4, C3, C4, C7, E1); C8's C8a built; E0 closed as superseded (roles-scopes) | 15; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 4/12 | the cost-estimate half (`fugaro estimate`) |
| model-ab-testing | 5/18 | A3.0, A3.2 (#2298), A3.3, A4.1 (#2302), A4.2 (#2297), A4.3, A5.1 (#2303), A5.2 to A5.4, A6.1; owner Checks 210 to 212; A3.1 (#2300) and A4.5 (#2299) merged |
| bitbucket-workspace-token | 7/13 | T1.4, T2.2, T2.4, T2.6 and T2.7 left; T2.9 closed as replaced by the secrets-brokering proxy chain (D1981, 2026-11-10); T2.11's step 5 now a dated amendment to the shipped 0.14.0 operator notes (D1984); T2.6's three corrections landed (D1983); D1980–D1984 answered 2026-11-10 as recommended (its reconciliation-secrets-brokering.md) |
| run-limit-carry-over | 4/10 | L0 to L3 merged, L4 (draft #2240), L5 onward waiting |
| binary-allow-rule | 4/11 | NV0, NV1, NV2 and AR2 merged, AR0 (draft #2202) held on the update-ordering fix; AR1 and AR3 to AR7 wait behind it |
| plugin-user-scope | 2/10 | U0 and U1 merged, U2 (draft #2235); `fugaro upgrade` is still on main |
| run-without-checkout | 4/8 | A0 to A3 merged (A3 #2233); A4, B1, B2 and C1 left |

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
| compatibility-matrix | owner CM4 (the read of the copy); CM3 merged (#2194) |
| mcp-server | M9 parked (D936, the fence declined, no release schedules it) and M11 declined (2026-11-09); M4's store half (#2117) and M5b (#2114) merged |
| api-key-review | owner Check 330 (AK8); AK6b (#2172) and AK7 (#2263) merged; AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
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
