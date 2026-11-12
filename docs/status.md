# Fugaro: status of every spec under docs/

As of 2026-11-12, read from `main`, the open pull requests and each plan's task list (not from the plans' own status lines, which are stale in about 30 folders). Counts come from commit titles and file checks; "about" marks the approximate ones. **Owner-run** means a live check or step that only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. CLI 0.23.0 (Tapir) is being cut as this page is written and is not tagged yet.

## In progress, close to done

| Spec | Merged | Left |
| --- | --- | --- |
| licensing | 20/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); L5.3 (the pure-MIT build, draft #2363); L3.1 is superseded; L0.1 closed (#2359), L4.4 (#2364) merged, Check 130 closed PASS (#2367) |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 (its script landed in #2183) |
| console-dependency-graph | 14/17 | GV3.1, GV3.2; GV4.1 merged (#2414) |
| bitbucket-credentials | 11/12 | owner Checks 149 and 145, and 148's read half (S2, D1961; its script landed with T2.6 and T2.11 in #2270); S3 superseded by the secrets-brokering proxy chain (2026-11-10), except T3.3's trust paths; Check 147 retired (D1960) and T2.6 unblocked (D1962); D1960–D1962 answered 2026-11-10 as recommended (its reconciliation-secrets-brokering.md) |
| task-governance | 25/31 | 6 left: T3.4 (docs), and the rest in `ee/governance`; T8.3 merged (#2209) |
| mobile | about 35/45 | P5.x, P7.2 (draft #2391), P7.3 (draft #2392), P6.8 (draft #2395), P7.4, P7.5; VS1.1 (draft #2397), VS1.2 and VS1.3 after it (D213 approved); owner P2.3 and P4.4; D124 done (fugaro.dev added to the host list) |
| troubleshoot-issue | 16/18 | T-IS9 (docs), T-IS12 (draft #2384); T-IS11 (#2340), T-IS6 (#2135), T-IS19 (#2115) and T-IS7 (#2192) merged |
| rigor-command | 3/10 | R6 (docs), R7 (the hotfix gate), W (`setup` asks the rigor mode); R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, so the spec is mostly superseded but not wholly |
| compute-backend | 4/7 | Task 10 (the enable of `backend: cloud-batch`, with the `doctor` gate; draft #2325) is open; Task 12 is owner-run (Check 57b, and Check 124 for `--provision-batch`) after it; Task 13 only if Task 14's answer needs it. Built: Tasks 8, 9, 11 and 14 (the opener, the reaper role and `fugaro reaper`, the seal's carrier refusal); `backend: cloud-batch` is still refused until Task 10 |

## In progress, a lot left

| Spec | Merged | Left |
| --- | --- | --- |
| stateless-orchestrator | 15/20 | O11 (draft #2386), O13, O14 (ruled: split in three slices); O16b (approved, waits on the MCP wiring; the front door's M12 merged, #2333), O19 (approved, W7 #2275 merged); O9 (#2193), O12 (#2290) and O16a (#2381) merged; D2020-D2022 answered as recommended 2026-11-10; owner Checks 301 to 304 (300 PASSED 2026-11-10); 305 reserved for O16b |
| secrets-brokering | 20/65 | B1.13 flip (draft #2166, held on Check 191), B1.15, B1.16; owner Checks 191 and 192; the later slices planned 2026-11-10 as B2.1–B2.9, B3.1–B3.10, B4.1–B4.11, B5.1–B5.9 (all approved 2026-11-10, each with its own gate; D2070–D2080 answered as recommended; owner Checks 193–197); B3.1 (in a run, #2431) and B4.1 (in a run, #2426) launched. Dynamic-delivery subfolder, 11/22: DS0.1, DS0.2, DS1.1, DS1.2, DS1.3, DS1.4 (#2250), DS1.6, DS2.0, DS2.1 (#2237), DS3.3 and DS3.5 (#2288) merged; DS2.3 (draft #2383) and DS3.1 (draft #2162) open; owner DS0.3 (Checks 510 and 511). Check 191's script fixes merged (#2308, #2309, #2353); one more is open (draft #2370) |
| vision-model | 4/9 | V4 (in a run, #2425), V6 (in a run, #2430), V3, V7, V8 not started; owner Checks 520 and 521 not run; V0 (#2304), V1 (#2305), V2 (#2336) and V5 (#2390) merged |
| subscriptions | about 21/51 | S3.4 (draft #2382) and the rest not started or open; 10 owner-run; TN1 seat enforcement is not in 0.24.0 (owner); S3.2 (#2283), S4.2 (#2341), S6.2 (#2279) and S6.3 (#2277) merged |
| workspaces | 9/10 | W9 (in a run, #2421); W3 (#1947), W6 (#2105) and W7 (#2275) merged |
| no-prompt-policy | 12/17 | T10 to T13; owner T14; T9 (#2232), T4 (#2084), T7 (#1984) and T15 merged, and `update` asks nothing (#2082) |
| image-setup-skill | 11/14 | IS2 (draft #2398), IS8b (draft #2171, waits on O3), IS13 not started; IS1 (#2170), IS12 (#2393), IS3, IS4, IS5, IS7, IS8, IS9, IS10, IS11 and IS11b merged |
| diagnose-agent | 9/10 | T-DG10 (draft #2376); T-DG6 (#2244), T-DG7 (#2138), T-DG8 (#2345) and T-DG9 (#2379) merged |
| periodic-reviews | 7/14 | R7 to R12; owner Checks 136 and 340 |
| cli-output-console-graph | 28/32 | CG13b (draft #2415) and CG13c (CG13 was split in three, #2313); owner CL12 and CG12; CL9 (#2315), CL11 (#2377), CG5 (#2312), CG13a (#2378), CL6 (#2208), CL10 (#2210), CG4 (#2207) and CG6 (#2205) merged |
| issues-resolved-on-update | 6/8 | T-RU7, T-RU8; T-RU4 (#2187), T-RU5 (#2251) and T-RU6 (#2216) merged |
| catalog-build-context | 3/5 | CB4 (draft #2374), CB5; CB2 merged (#2255) |
| post-review-triage | 11/14 | TR8 (in a run, #2429), TR12; owner TR13; TR3, TR5 and TR6 merged (#2236), BF1m (#1999), TR7a (#2256), TR7b (#2400), TR9 (#2380) and TR10 (#2246) merged |
| outer-loop-escalation | 7/18 | E2 to E4, E6, E10a, E10b, E11 to E14 left; owner Checks 540 to 542; E0 (#2334), E1 (#2301), E5 (#2319), E7 (#2321), E8 (#2387), E9 (#2317) and E16 (#2320) merged; D2046 answered: `escalation.ready_drafts` is off by default |
| model-crafts | 6/10 | MC6 (draft #2389, with AK9 and AK10), MC7, MC9; owner MC10; MC4 (#2252) and MC5 (#2274) merged |
| security-classification | 7/11 | SC3, SC5, SC10 (in a run, #2427), SC11; SC9 merged (#2296) |
| settings-commands | about 10/12 | S10, S11 (merged held, #2280: its read half S2 is now on `main`, #2328); S2 (#2328), S6 (#2247), S8 (#2282) and S9 (#2347) merged; S7 closed 2026-11-10 as a duplicate of config-management's G7 |
| rigor-modes | about 23/40 | T7.1, T7.2, T8.1, T8.3; the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open; T1.7 merged (#2343) |
| roles-scopes | 4/12 | RS4 (draft #2399), RS8 (draft #2401), RS5, RS6a to RS6c, RS7, RS10; RS5 (`fugaro user`) not started; RS3 merged (#1994) |
| review-levels-ab-jobmax | about 25/40 | A/B reviewer slot, `/jobmax` skill (J1.1 in a run, #2424), `auto_launch`; B1.1 merged (#2339) |
| forge-authorship-images | 11/25 (A1, A2a, A2b, A3, A4, B1, C3, C4, C7, E1, X1); C8's C8a built; E0 closed as superseded (roles-scopes) | 14; C1 is in a run (#2428); the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 9/12 | owner G2.5, and two more; the cost-estimate half (G1.1 to G1.4: #2329, #2349, #2352, #2411) and G3.1 (#2335) merged |
| model-ab-testing | 11/18 | A3.3 (draft #2403), A5.2 (draft #2396), A5.3, A5.4, A6.1; owner Checks 210 to 212; A3.0 (#2346), A3.1 (#2300), A3.2 (#2298), A4.1 (#2302), A4.2 (#2297), A4.3 (#2338), A4.5 (#2299) and A5.1 (#2303) merged |
| bitbucket-workspace-token | 7/13 | T1.4, T2.2, T2.4, T2.6 and T2.7 left; T2.9 closed as replaced by the secrets-brokering proxy chain (D1981, 2026-11-10); T2.11's step 5 now a dated amendment to the shipped 0.14.0 operator notes (D1984); T2.6's three corrections landed (D1983); D1980–D1984 answered 2026-11-10 as recommended (its reconciliation-secrets-brokering.md) |
| run-limit-carry-over | 6/10 | L6 (draft #2413), L7 onward waiting; L0 to L5 merged (L4 #2240, L5 #2404) |
| binary-allow-rule | 4/11 | NV0, NV1, NV2 and AR2 merged, AR0 (draft #2202) held on the update-ordering fix; AR1 and AR3 to AR7 wait behind it |
| plugin-user-scope | 5/10 | U3 (draft #2388) and the rest; U0, U1, U2 (#2235), U5 (#2355) and U8 (#2394) merged; `fugaro upgrade` is still on main |
| run-without-checkout | 4/8 | A4 (draft #2402), B1, B2 and C1 left; A0 to A3 merged (A3 #2233) |

## To do or parked

| Spec | State |
| --- | --- |
| composite-commands | 1/10; depends on no-prompt-policy |
| licence-key-v2 | plan merged (#2406), 3/26 (K0–K7): K1.1 (#2408), K1.5 (#2409) and K7.2 (#2410) merged; K1.3, K1.4, K2.1, K2.2 and K3.1 in runs (#2420, #2418, #2422, #2419, #2423); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation); TN1 seat enforcement is not in 0.24.0 (owner); supersedes L3.1 and the old key's claims; owner Checks 560–564 not run |
| account-aliases | 1/5 (AL0–AL4), all approved 2026-11-10; AL2 merged (#2372), AL0 (draft #2373); D2090–D2102 answered as recommended; owner Check 550 not run |
| mobile-auth | 2/7; parked |
| repo-image-removal | **PARKED** (2/11; parked by image-setup, D1900) |
| voice-module, collateral, backlog, plans | no plan or not a spec |

## Done

| Spec | Left |
| --- | --- |
| config-management | none (14/14; G3 #2190, G4 #2221, G5 #2264, G6 #2265, G7 #2351, G9 #2385, G10 #2281, G12 #2245, G13 #2266 merged) |
| contract-first-decoupling | none (7/7; CD5 merged, #2273) |
| compatibility-matrix | owner CM4 (the read of the copy); CM3 merged (#2194) |
| mcp-server | M9 parked (D936, the fence declined, no release schedules it) and M11 declined (2026-11-09); M4's store half (#2117), M5b (#2114) and M12 (#2333, the front door mounted, dormant) merged |
| api-key-review | owner Check 330 (AK8); AK6b (#2172) and AK7 (#2263) merged; AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
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
| coordinator | 7/9; P7 and P8 re-planned under mcp-server; the roadmap tasks P9a (#2326), P9b (#2348) and P9d (#2412) merged, P9f in a run (#2432) |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| how-to | not a spec |
| console-windows | owner Check 119; T-H4 merged (#2175). The first real Windows unit run failed 484 tests, its fix chain is not planned yet |
| short-run-ids | owner SR13; SR12 merged (#2168) |
| website-changelog | WC4 lands with the next release |
| central-orchestrator | superseded by stateless-orchestrator |
| windows-cli | 10/31; superseded by console-windows |
