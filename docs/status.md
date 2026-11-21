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

As of 2026-11-21 02:45 UTC (2026-11-20 evening, US time), read from `main` at `4ce11172f` (#3427), the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page. The sections run from the nearest to done to the furthest; the finished specs are at the end.

**Released:** CLI 0.26.1 (hotfix of 0.26.0, tagged `v0.26.1`, 2026-11-18) and CLI 0.26.0; console 0.124.0 "Ugli" (tagged `console-v0.124.0`, 2026-11-20). CLI 0.27.0 is next (gate: secrets-brokering, graph-estimates with the orchestrator, config-ownership with composite-commands, troubleshoot-cloud, periodic-reviews and model-crafts; its notes are draft #3368). RE6 and Check 192 are owner-run after the release and are not a gate; RE6 and T-TC10 still read Check 620 until the renumber pull request (to Checks 670 and 680) merges.

**CI:** the last completed dispatched run on main is 44841014060, green, on `7fe08baf7` (#3399), 17 commits behind the tip at this read; the run before it, 44837763597 on main at 23:34 UTC, was red, and #3425 and #3427 are the commits that made main build again. The tip (`4ce11172f`) has no dispatched run yet.

**Open pull requests:** 43, read from `gh pr list` at 02:45 UTC (42 drafts, 1 not). Live runs, read from `fugaro runs ls --state running` at 02:47 UTC (27 runs, each on its own pull request): #3401 (launcher-does-not-mint LM0), #3402, #3403 and #3404 (batch-run-identity BR8, BR9, BR11), #3405 and #3407 (keys-through-service KS2, KS5), #3406 and #3410 (workspace-service WS20, WS8), #3408, #3409, #3411, #3412, #3413, #3414, #3415, #3416, #3417, #3419 and #3422 (workspace-projects-users WU5, WU6, WU12, WU4, WU13, WU8, WU23, WU15, WU7, WU11, WU24), #3418, #3421 and #3424 (ci-affected-tests CS2b, CS5, CS4), #3420, #3423 and #3426 (location-preference LP2, LP12, LP4), #3428 (orchestrator-graph-estimates R5) and #3429 (the mobile plan's P5.1b and P5.1c). No run was going on the other 16, and what the last run of each ended with was not re-read (about): **ready** if its last run ended `ready`: #3352 (workspace-service WS7, the one non-draft); **stalled** (drafts): #3244, #3247, #3296, #3306, #3307, #3316, #3335, #3339, #3349, #3361, #3364, #3380, #3381 and #3394. Docs only: #3368 (the 0.27.0 release notes). Numbers move while landing and launch runs go on: this is one read.

Merged since the last read (86 commits, read from `b5f778175`; 5 are specs' own documents, listed first): the specs, reconciliations and plans of ci-affected-tests, location-preference, credentials-belong-to-models, batch-run-identity and launcher-does-not-mint; workspace-service WS1, WS2, WS4, WS5, WS16 and WS17; workspace-projects-users C1, C2, WU3, WU9, WU18 and WU20; keys-through-service C1, KS1, KS3 and KS17; location-preference LP0, LP1, LP5a, LP5b, LP5c, LP7, LP8, LP9 and LP15; ci-affected-tests CS1, CS2a, CS3 and CS7; console-improvements GF2, MO-B, MO-C, MO-K, MO-SH, RB4, RS2, RS3, RS4, SM1, SM2, SM4, SM-E, ST2, SU1 and SU2a; rigor-modes LV1.2, LV2.1, LV2.2, LV4.1 and LV6.1; review-against-task RA9 and RA10; run-messages RM7 and RM9; config-ownership K1, I1 and D1 (the spec is complete); always-a-budget AB0; batch-run-identity BRS; launcher-does-not-mint C1; orchestrator-graph-estimates W1; secrets-brokering B4.7; mobile P5.1f and P5.3.7; portal P5.2; the release of console 0.124.0 (#3327) and its fixes of red main; and #3377, #3398, #3399, #3425 and #3427, which belong to no plan task. Rows that could not be re-derived are marked "about" or say so: forge-authorship-images, licence-key-v2, subscriptions, review-levels-ab-jobmax, rigor-modes, secrets-brokering and mobile; the WS4 task of workspace-service and the console-improvements task ids were read from each pull request's own text; credentials-service is Done by the owner's ruling.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-workspace-token | 10/12 | T1.4 and T2.7 (owner-run: Checks 180 and 181); T2.2 was carried by shared-jobs-default S1 and S4, both merged; T2.9 is closed as replaced by the service's rules, D1981, and is not counted in the 12 |
| composite-commands | 7/10 | AP7 (the audit of "now run X" texts: launches after the secrets-brokering flips, owner 2026-11-18), AP9 and AP10, all not started; depends on no-prompt-policy |
| forge-authorship-images | about 20/25 | the tasks still open are not re-derived (the plan's own State lines date from 2026-11-10): owner-run A5 (Check 230) and the unscheduled C8b/C8c stay out; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 11/12 | owner G2.5 |
| licence-key-v2 | about 20/26 | not reconciled against the plan in this sweep, so the count is about; K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); K4.2 merged as #2873, the rest of K0–K7 not started or open (K3.4 held on TN1); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| mobile | about 58/67 | P5.1b and P5.1c (the orchestrator block writer, live #3429), P5.1g (approved 2026-11-14, not started), P5.2.6 to P5.2.10 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| model-crafts | 25/26 | MC26 (stays owner-run) and the activation plan for the four known limits, all approved 2026-11-16; F27, D1660–D1671 and D1673 were all answered as recommended on 2026-11-17, and D1672 on 2026-11-15 (A, then B and C together in one release, then D), so no decision is open; owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| orchestrator-graph-estimates | 38/39 | 1 left, approved 2026-11-16: R5 (live #3428); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); Checks 700–703 owner-run |
| periodic-reviews | 16/17 | R5 (owner-run); owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| portal | 35/42 | owner P0.1 to P0.6; not started: P9.4 |
| repos-management | 6/7 | RE6 (owner-run: its Check is renumbered to Check 670 by the owner's ruling, and the renumber pull request is not merged yet); #2690 was closed 2026-11-17 as superseded by RE1 to RE3 (#2932, #2935, #2939); all approved 2026-11-15 and D2400–D2417 answered the same day |
| review-against-task | 15/16 | 1 left, not started: RA7 (the recipes page and the release notes), waiting on edges; owner-run Check 750 after the release |
| review-levels-ab-jobmax | about 30/35 | U4 is reported unreliable until the owner runs Check 211; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded and not counted in the 35, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; X1 waits on T3.1; the count is about because the remaining tasks of the slot are not all named here |
| rigor-modes | about 33/40 | T8.2 (owner-run: Check 127, written); the rest of the overlay plan LV3 to LV7 is unblocked in principle (LD2: the level wins): LV3.1 (draft #3394, stalled: no run going), the others not started; the count is about because the overlay's tasks are not all counted |
| run-messages | 10/11 | RM8 is owner-run (Check 740, after the release); D2940–D2947 answered 2026-11-18 as recommended |
| runs-ls-speed | 6/8 | RL4 (optional, built only if the default listing is still slow, not started) and RL6 (owner-run Check 760); D2960–D2967 are taken as recommendations (docs/runs-ls-speed) |
| secrets-brokering | about 51/65 | B1.16; owner Check 192 (the --after-flip half; the flip itself merged as #2166 on 2026-11-17); the flip chain: B5.7 (draft #3316, stalled: no run going; B4.9, the S4 flip, has merged), approved to launch one at a time with the owner checks moved to after-flip runs (owner, 2026-11-18); B2.6, B2.8 and B2.9; B3.7, B3.9 and B3.10; B4.2 (owner-run), B4.8, B4.10 and B4.11 (owner Check 195 gates only B4.9's flip); B5.6, B5.8 and B5.9 (all approved 2026-11-10; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511); the count is the last sweep's 48 plus B4.6, B4.7 and B4.9 (B2.7, B3.8 and B4.5 included) and is about because the plan's 65 does not reconcile with the 23 tasks named here |
| shared-jobs-default | 29/32 | 3 left: M2, which waits for owner Check 580 (the script is merged, #3121); M4 and M5 not started (M5 consumes M2); owner Checks 191, 192, 580, 581 and 582 |
| subscriptions | about 36/50 | a census of 2026-11-17 found 34 of 41 agent tasks done before S7.1 and S7.3 merged the same day (#3046, #3048), which makes 36, with the 10 owner-run (none run) in the 50; S1.7 is superseded and not counted. Open agent tasks: S0.7 (the app's first release, mobile-v0.200.0), S1.9 (waits on the enforcement release) and S2.9 (the production public key, which waits on owner S3.8), and about one more the census did not name; owner-run: S1.10 (Check 178, deferred), S3.7, S3.8, S6.4, S7.4 and S8.1–S8.6 (counsel closed S8.4 by the owner's ruling of 2026-11-17, recorded by #3042); TN1 seat enforcement is not in 0.24.0 (owner). The count rests on that census and was not re-derived task by task |
| troubleshoot-cloud | 11/12 | T-TC10 is owner-run (its Check is renumbered to Check 680 by the owner's ruling; the renumber pull request is not merged yet); approved 2026-11-16 |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| always-a-budget | 1/19 | all approved 2026-11-20 and launching by plan edges: C1 (draft #3306, stalled: no run going) and AB14 (draft #3307, stalled: no run going); the other tasks not started or waiting on edges; budget mode off is removed (D3009, the owner's own; D3010 and D3011 in the keys plan); D3020–D3028 answered 2026-11-20 (D3020, D3022, D3023, D3025 and D3028 not the recommendation; D3026 the owner's own: no rule in terms of a repository's auth, which gets its own spec); AB7 waits on a location-preference spec, not written yet (D3029); one release, with no release cut from main between AB7 and I1; owner-run Checks 790 and 791; the keys plan's KS14 and KS15 wait on its I1 |
| batch-run-identity | 1/27 | C1's contract (draft #3380, stalled: no run going) and BR0's Check 830 (draft #3381, stalled: no run going); live: BR8 #3402, BR9 #3403 and BR11 #3404; C1, BRS, BR0 and BR6 approved 2026-11-20; D3100–D3112 answered 2026-11-20 (D3110 and D3111 not the recommendation, D3110 amended after review; D3104 supersedes D2075; D3102's second read is decided after Check 830); BR3, BR5 and BR12 wait on the owner's run of Check 830 and his answer on O1; BR1, BR7 and BR12 start after always-a-budget's AB9 if it has an open pull request; owner-run Checks 830 and 831; **on hold by launcher-does-not-mint's D3129 (2026-11-20):** C1, BR6 and every task that touches the key wait for that plan's LM19; only BR0 and BR8 to BR11 may go |
| ci-affected-tests | 4/12 | live: CS2b #3418, CS4 #3424 and CS5 #3421; all approved 2026-11-20 (docs/ci-affected-tests): D3040–D3043 answered 2026-11-20 as recommended; CS6 is copied into the workflows only by the owner's word and Check 800 is owner-run |
| console-improvements | 29/46 | open and stalled (drafts, no run going): MO-P #3335, SRC #3339 and SM3 #3349; the other 14 not started or waiting on edges; all six slices chosen and all tasks approved by the owner; owner-run Checks 710 (re-record the Live and Graph goldens), 711 (read timings) and 712 (the wizard on a scratch project) |
| keys-through-service | 4/20 | all approved 2026-11-20 and launching by plan edges: KS2 (live #3405) and KS5 (live #3407), KS19 (draft #3361, stalled: no run going); the other tasks not started or waiting on edges; D3000–D3015 answered 2026-11-19 and 2026-11-20 (D3005 and D3013 not the recommendation, D3007 and D3009 the owner's own; D3009 removes budget mode off under its own spec, not written yet); two releases; release A waits on the owner's run of Check 50 (D3013); KS14 and KS15 wait on that spec's plan, KS16 on secrets-brokering B5.7; owner-run Checks 50, 780 and 781 |
| launcher-does-not-mint | 1/26 | LM0 (live #3401); approved 2026-11-20 (docs/launcher-does-not-mint); the credentials service issues a run's identity as a one-time ticket the run claims, and `fugaro update` takes the signing grant off launchers, operators and the resident orchestrator once the service answers; D3120–D3137 answered 2026-11-20, all as recommended, D3129, D3130 and D3136 amended after review and answered again, D3125 amended by D3136 (D3121 amends the rule on the credentials account's grants; D3129 holds everything in batch-run-identity that touches its key or its C1 until LM19; D3130 enforces the launch scope at the issuer; D3131 accepts that executions queued and not started halt at the update); the other tasks wait on always-a-budget's I1 and keys-through-service's release A (D3128); owner-run Checks 840 and 841 |
| location-preference | 9/18 | live: LP2 #3420, LP4 #3426 and LP12 #3423; all approved 2026-11-20 (docs/location-preference); the region stays the one setting and the place (US, EU) is derived from it; D3080–D3091 answered 2026-11-20, all as recommended, D3087 and D3088 amended after review and confirmed; D3092–D3097 answered the same day (D3097 not the recommendation: a fourth review round) (D3090 removes the Firestore location's own confirmation, reversing D12 of 2026-10-20 for it); always-a-budget's AB7 waits on its LP2 and LP4 (D3094); LP3, LP6, LP10 and LP14 wait on AB7; the key service is redeployed with LP15 (owner); owner-run Checks 820 and 821 |
| mobile-identity-pairing | 10/45 | open drafts, stalled (no run going): P2 #3247 and P6 #3244; the other tasks not started; owner-run M0 and Z0, and Checks 730 to 737 (T0 wrote them) |
| workspace-projects-users | 10/26 | all tasks approved; live: WU4 #3412, WU5 #3408, WU6 #3409, WU7 #3417, WU8 #3414, WU11 #3419, WU12 #3411, WU13 #3413, WU15 #3416, WU23 #3415 and WU24 #3422; the other 5 (WU10, WU16, WU17, WU21, I1) not started and launch by plan edges |
| workspace-service | 13/25 | all approved 2026-11-20 and launching by plan edges: live: WS8 #3410 and WS20 #3406; WS7 (ready #3352, no run going; its last outcome not re-read); stalled drafts: WS9 #3364 and WS11 #3296; the other 7 not started or waiting on edges; D2980–D2992 answered 2026-11-19 (D2984 not the recommendation; D2987 amended after review); owner-run Checks 770–773 after the release |

## To do or parked

| Spec | State |
| --- | --- |
| credentials-belong-to-models | plan only: 0/38 tasks merged, all approved 2026-11-20, launching by plan edges (docs/credentials-belong-to-models); D3060–D3073 answered 2026-11-20, all as recommended, D3061 amended by the owner and D3064 after review (`agent.auth` is retired: a craft's access names the credential, an unset one is the first declared credential that reaches the model, and Vertex only where named (D3061, amended by the owner); D3065 retires the consent switch and D3072 sets the version rule aside for `fugaro.yaml`); two releases; it starts after always-a-budget's I1, keys-through-service's KS5, KS12 and KS19 (D3071) and replaces keys-through-service KS6, KS9 and KS10; release 2 waits on release 1 and on keys-through-service KS13; owner-run Checks 810 and 811 |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans | no plan or not a spec |
| workspace-credentials | plan only: 0/17 tasks merged, no task approved (docs/workspace-credentials); it plans what workspace-service and keys-through-service left: sharing a provider key at the workspace (WC1) and a person's own Claude token at the workspace (WC0, WC2–WC13, I1), every personal task behind the owner's answers to the threat model's review (WC0); D2960–D2979 answered 2026-11-19 (D2975 the owner's own answer) and D3140–D3145 answered 2026-11-21, all as recommended (D3141: a personal token follows the route, so a project's own model credential shadows it); WC1 waits on workspace-service WS19 and WC5 on WS8; owner-run Checks 850 and 851 |

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
| config-ownership | none (22/22 merged: K1 as #3337, I1 as #3391, D1 as #3386); D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
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
| outer-loop-escalation | owner Checks 540 to 542 (written as scripts, E15 merged as #3147); `escalation.ready_drafts` ships on by default (#3146, D2046) and the daemon already reads it (#3149 found it wired and changed nothing) |
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
