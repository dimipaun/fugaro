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

As of 2026-11-20 16:30 UTC, read from `main` at `b5f778175` (#3305), the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page. The sections run from the nearest to done to the furthest; the finished specs are at the end.

**Released:** CLI 0.26.1 (hotfix of 0.26.0, tagged `v0.26.1`, 2026-11-18) and CLI 0.26.0, console 0.123.0. CLI 0.27.0 is next (gate: secrets-brokering, graph-estimates with the orchestrator, config-ownership with composite-commands, troubleshoot-cloud, periodic-reviews and model-crafts). Console 0.124.0 has its whole scope merged and is not tagged; the tag awaits the owner's go. RE6 and Check 192 are owner-run after the release and are not a gate; RE6 and T-TC10 still read Check 620 until the renumber pull request (to Checks 670 and 680) merges.

**CI:** the last green dispatched run is 44770131421 on `745f6e835` (#3310), which is one docs-only commit behind the tip of main at this read (`b5f778175`, #3305, with no dispatched run yet); the run before it, 44767082300 on `252329076` (#3276), was red, and #3310 is what fixed it. The CI changes of #3301 (longest shards first, the live-script runners run when what they read changed) have merged.

**Open pull requests:** 12, read from `gh pr list` at 16:30 UTC (8 drafts, 4 not). A non-draft is **ready** unless a run is going on it; a draft with no run going is **stalled**. Live runs, read from `fugaro runs ls --state running` at 16:30 UTC (5 runs): #3311 (mobile-identity-pairing A2's follow-up, run `4501`), the follow-up of A3 (run `a3e6`, no pull request number read), #3309 (always-a-budget AB0, run `0dc0`), #3307 (AB14, run `9814`) and #3304 (keys-through-service KS17, run `1bb1`). No run was going on the rest, and what the last run of each ended with was not re-read (about): ready, if its last run ended `ready`: #3280 (workspace-service WS1), #3291 (workspace-projects-users WU3) and #3303 (keys-through-service C1); stalled (drafts): #3296 (WS11), #3306 (always-a-budget C1), #3247 (mobile-identity-pairing P2) and #3244 (P6). Docs only: #3308 (the spec, reconciliation and plan of ci-affected-tests, which has no row until it merges).

Merged since the last read (43 commits, read from `b5f778175`): workspace-service C1, C2, C3, WS3, WS6, WS15 and WS22; review-against-task RA1, RA2, RA3, RA5b, RA6b, RA8a and RA8b; run-messages RM1, RM2 and RM4; workspace-projects-users WU1, WU2 and WU22; runs-ls-speed RL2B, RL3 and RL5; config-ownership W3; secrets-brokering B4.6 and B4.9 (the S4 flip); mobile-identity-pairing A1, A2, A3 and P5; orchestrator-graph-estimates B9; periodic-reviews R16; rigor-modes LV1.1; console-improvements RS1; the spec, reconciliation and plan of keys-through-service (#3299) and of always-a-budget (#3302, with #3305's D3029 note); #3206 (the catalog-build rehearsal) and #3211 (the update's left-for-you text), which belong to no plan; and the fixes #3298 and #3310. Rows that could not be re-derived are marked "about" or say so: forge-authorship-images, licence-key-v2, subscriptions, review-levels-ab-jobmax, rigor-modes, secrets-brokering and mobile; credentials-service is Done by the owner's ruling.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-workspace-token | 10/12 | T1.4 and T2.7 (owner-run: Checks 180 and 181); T2.2 was carried by shared-jobs-default S1 and S4, both merged; T2.9 is closed as replaced by the service's rules, D1981, and is not counted in the 12 |
| composite-commands | 7/10 | AP7 (the audit of "now run X" texts: launches after the secrets-brokering flips, owner 2026-11-18), AP9 and AP10, all not started; depends on no-prompt-policy |
| config-ownership | 19/22 | best-effort for 0.26.0 and not in its gate (owner, 2026-11-17); all tasks approved 2026-11-15; 3 left, all not started: K1 (W3 has merged, so it is unblocked), I1 and D1; D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| forge-authorship-images | about 20/25 | the tasks still open are not re-derived (the plan's own State lines date from 2026-11-10): owner-run A5 (Check 230) and the unscheduled C8b/C8c stay out; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 11/12 | owner G2.5 |
| licence-key-v2 | about 20/26 | not reconciled against the plan in this sweep, so the count is about; K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); K4.2 merged as #2873, the rest of K0–K7 not started or open (K3.4 held on TN1); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| mobile | about 56/67 | P5.1f (held: it collides with P5.1d on `orchestrator_serve.go`) and P5.1g (approved 2026-11-14, not started), P5.2.6 to P5.2.10 (approved 2026-11-14, not started), P5.3.7 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| model-crafts | 25/26 | MC26 (stays owner-run) and the activation plan for the four known limits, all approved 2026-11-16; F27, D1660–D1671 and D1673 were all answered as recommended on 2026-11-17, and D1672 on 2026-11-15 (A, then B and C together in one release, then D), so no decision is open; owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| orchestrator-graph-estimates | 37/39 | 2 left, both approved 2026-11-16 and not started: R5 and W1 (both waited on B9, which has merged, so both are unblocked); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); Checks 700–703 owner-run |
| periodic-reviews | 16/17 | R5 (owner-run); owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| portal | 34/42 | owner P0.1 to P0.6; not started: P5.2 and P9.4, which waited on P6.2 (merged) |
| repos-management | 6/7 | RE6 (owner-run: its Check is renumbered to Check 670 by the owner's ruling, and the renumber pull request is not merged yet); #2690 was closed 2026-11-17 as superseded by RE1 to RE3 (#2932, #2935, #2939); all approved 2026-11-15 and D2400–D2417 answered the same day |
| review-against-task | 13/16 | 3 left, not started: RA9 (the image gate at launch, validate and update), RA7 (the recipes page and the release notes) and RA10 (the end-to-end proof), all waiting on edges; owner-run Check 750 after the release |
| review-levels-ab-jobmax | about 30/35 | U4 is reported unreliable until the owner runs Check 211; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded and not counted in the 35, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; X1 waits on T3.1; the count is about because the remaining tasks of the slot are not all named here |
| rigor-modes | about 28/40 | T8.2 (owner-run: Check 127, written); the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), and LV1.1 has merged (#3207), so LV1.2 and the tasks behind it are not started; the count is about because the overlay's tasks are not all counted |
| run-messages | 8/11 | RM7 (the documents) and RM9 (the `working` skill names `fugaro tell`) wait on edges, not started; RM8 is owner-run (Check 740, after the release); D2940–D2947 answered 2026-11-18 as recommended |
| runs-ls-speed | 6/8 | RL4 (optional, built only if the default listing is still slow, not started) and RL6 (owner-run Check 760); D2960–D2967 are taken as recommendations (docs/runs-ls-speed) |
| secrets-brokering | about 50/65 | B1.16; owner Check 192 (the --after-flip half; the flip itself merged as #2166 on 2026-11-17); the flip chain: B5.7 (not started; B4.9, the S4 flip, has merged), approved to launch one at a time with the owner checks moved to after-flip runs (owner, 2026-11-18); B2.6, B2.8 and B2.9; B3.7, B3.9 and B3.10; B4.2 (owner-run), B4.7, B4.8, B4.10 and B4.11 (owner Check 195 gates only B4.9's flip); B5.6, B5.8 and B5.9 (all approved 2026-11-10; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511); the count is the last sweep's 48 plus B4.6 and B4.9 (B2.7, B3.8 and B4.5 included) and is about because the plan's 65 does not reconcile with the 23 tasks named here |
| shared-jobs-default | 29/32 | 3 left: M2, which waits for owner Check 580 (the script is merged, #3121); M4 and M5 not started (M5 consumes M2); owner Checks 191, 192, 580, 581 and 582 |
| subscriptions | about 36/50 | a census of 2026-11-17 found 34 of 41 agent tasks done before S7.1 and S7.3 merged the same day (#3046, #3048), which makes 36, with the 10 owner-run (none run) in the 50; S1.7 is superseded and not counted. Open agent tasks: S0.7 (the app's first release, mobile-v0.200.0), S1.9 (waits on the enforcement release) and S2.9 (the production public key, which waits on owner S3.8), and about one more the census did not name; owner-run: S1.10 (Check 178, deferred), S3.7, S3.8, S6.4, S7.4 and S8.1–S8.6 (counsel closed S8.4 by the owner's ruling of 2026-11-17, recorded by #3042); TN1 seat enforcement is not in 0.24.0 (owner). The count rests on that census and was not re-derived task by task |
| troubleshoot-cloud | 11/12 | T-TC10 is owner-run (its Check is renumbered to Check 680 by the owner's ruling; the renumber pull request is not merged yet); approved 2026-11-16 |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| always-a-budget | 0/19 | all approved 2026-11-20 and launching by plan edges: C1 (draft #3306, stalled: no run going), AB0 (draft #3309, live: run `0dc0`) and AB14 (draft #3307, live: run `9814`); the other tasks not started or waiting on edges; budget mode off is removed (D3009, the owner's own; D3010 and D3011 in the keys plan); D3020–D3028 answered 2026-11-20 (D3020, D3022, D3023, D3025 and D3028 not the recommendation; D3026 the owner's own: no rule in terms of a repository's auth, which gets its own spec); AB7 waits on a location-preference spec, not written yet (D3029); one release, with no release cut from main between AB7 and I1; owner-run Checks 790 and 791; the keys plan's KS14 and KS15 wait on its I1 |
| console-improvements | 13/46 | all 33 other tasks not started or waiting on edges; all six slices chosen and all tasks approved by the owner; owner-run Checks 710 (re-record the Live and Graph goldens), 711 (read timings) and 712 (the wizard on a scratch project) |
| keys-through-service | 0/20 | all approved 2026-11-20 and launching by plan edges: C1 (ready #3303, no run going; its last outcome not re-read) and KS17 (draft #3304, live: run `1bb1`); the other tasks not started or waiting on edges; D3000–D3015 answered 2026-11-19 and 2026-11-20 (D3005 and D3013 not the recommendation, D3007 and D3009 the owner's own; D3009 removes budget mode off under its own spec, not written yet); two releases; release A waits on the owner's run of Check 50 (D3013); KS14 and KS15 wait on that spec's plan, KS16 on secrets-brokering B5.7; owner-run Checks 50, 780 and 781 |
| mobile-identity-pairing | 10/45 | live: the follow-ups of A2 (#3311, run `4501`) and A3 (run `a3e6`); open drafts, stalled: P2 #3247 and P6 #3244 (about: per-run state not re-read); the other tasks not started; owner-run M0 and Z0, and Checks 730 to 737 (T0 wrote them) |
| workspace-projects-users | 4/26 | all tasks approved; WU3 (ready #3291, no run going; its last outcome not re-read), the other 21 not started and launch by plan edges |
| workspace-service | 7/25 | all approved 2026-11-20 and launching by plan edges: WS1 (ready #3280, no run going; its last outcome not re-read) and WS11 (draft #3296, stalled: no run going); the other tasks not started or waiting on edges; D2980–D2992 answered 2026-11-19 (D2984 not the recommendation; D2987 amended after review); owner-run Checks 770–773 after the release |

## To do or parked

| Spec | State |
| --- | --- |
| batch-run-identity | plan only: 0/23 tasks merged; C1, BRS, BR0 and BR6 approved 2026-11-20, none launched (docs/batch-run-identity); D3060–D3070 answered 2026-11-20 (D3070 not the recommendation; D3064 supersedes D2075; D3062's second read is decided after Check 810); BR3, BR5 and BR12 wait on the owner's run of Check 810 and his answer on O1; BRS, BR1, BR7 and BR12 start after always-a-budget's AB9 if it has an open pull request; owner-run Checks 810 and 811 |
| ci-affected-tests | plan only: 0/12 tasks merged, all approved 2026-11-20 (docs/ci-affected-tests): D3040–D3043 answered 2026-11-20 as recommended; CS6 is copied into the workflows only by the owner's word and Check 800 is owner-run |
| location-preference | plan only: 0/18 tasks merged, none approved (docs/location-preference); the region stays the one setting and the place (US, EU) is derived from it; D3080–D3091 answered 2026-11-20, all as recommended, D3087 and D3088 amended after review and confirmed; D3092–D3097 answered the same day (D3097 not the recommendation: a fourth review round) (D3090 removes the Firestore location's own confirmation, reversing D12 of 2026-10-20 for it); always-a-budget's AB7 waits on its LP2, LP4 and LP8 (D3094); LP3, LP6, LP10 and LP14 wait on AB7; the key service is redeployed with LP15 (owner); owner-run Checks 820 and 821 |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans | no plan or not a spec |
| workspace-credentials | an addendum with no plan (docs/workspace-credentials): D2960–D2979 all answered 2026-11-19 (D2978: one project-then-workspace cascade for every credential kind; D2975 the owner's own answer: every model key through the credentials service, none mounted, which needs its own spec; D150's gate stays until Check 50 passes); it waits on the workspace service being deployed under its own plan (D2968) |

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
