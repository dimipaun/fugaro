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

As of 2026-11-17 17:15 UTC (evening), read from `main` at `a77929489` (#3058), the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. Released: CLI 0.25.0 (Vole, tagged and published 2026-11-15; its website promotion succeeded; the last CLI tag), console 0.122.0 (Strawberry). Console 0.123.0 (Tangerine) is scoped and not yet tagged. CLI 0.26.0 is not yet cut. Its gate is triage-loud (done: #2829, #2849 and #2910), repos-management RE4 (draft #2984, live) and RE5 (not started), composite-commands AP3 (merged as #2816 on 2026-11-17) to AP6 (AP4 draft #3086, AP5 draft #3089 and AP6 draft #3085, all live) and the catalog-context gate flip of issue #1738 (open PR #3080, not a draft; its owner Check 292 runs after it); nothing else is claimed done for it. config-ownership is best-effort for 0.26.0 and not in the gate (owner, 2026-11-17). RE6 with Check 620, and Check 192 (the `--after-flip` half of the B1.13 flip, #2166, merged 2026-11-17 after Check 191 passed every pre-flip row it can measure; its operator steps are items 9 and 10 of `docs/releases/v0.26.0.md`), are owner-run after the release and are not a gate. Issue #1865 (vision-model) carries the 0.26.0 milestone: its code merged in 0.25.0 and only owner Checks 520 and 521 are not run. **Main is red.** The last green run, 44102728113 on `43a72d338` (#3039), is before a collision of two orchestrator types (X4 and E10b) that broke main's build; #3095 fixed it. CI run 44132608812, dispatched on `074c835a8` (#3095), finished with a failure in six shards (cli-0, cli-1, cli-4, runner-0, scripts-live-0 and scripts-live-1): `TestDiagnoseSinceNamesARelease`, `TestEveryCommandRunsWithClosedStdin`, `TestCommandIsRegisteredOnce`, `TestLiveInitCensusMatchesTheFrozenList`, `TestSpendFieldsEndToEnd` and `TestLiveCheckScripts` (twice: Check 197's script needs `fugaro` on PATH where the test expects the gcloud stop, and a block has no `env-cleared` line); no pull request fixing them was found open when this was written. `TestInitPluginStageLeavesAPinItCannotRemoveAlone` and `TestInitPluginStageLeavesAnUnremovablePinForTheUser` also fail when run locally on `origin/main`, though they are not among that run's logged failures. Main has moved on by one commit (#3058, portal P3.2) since the commit that run covers. There are 36 open pull requests (this one excepted), read from `fugaro watch --once` at 17:10 UTC (the live ones; its queued runs were unavailable, so a run still queued is not seen) and from GitHub (draft or not): 21 are live (#2900, #2984, #3030, #3054, #3063, #3064, #3065, #3067, #3072, #3076, #3081, #3082, #3084, #3085, #3086, #3087, #3089, #3091, #3092, #3094 and #3096); 11 are not drafts with no run going (#2873, #3044, #3061, #3066, #3068, #3070, #3078, #3079, #3080, #3083 and #3088), which are ready only if their last run's outcome was `ready`, and this sweep did not re-read each last outcome (a run a blocking finding was left on would be stalled); and 4 are stalled drafts with no run going (#3038, #3049, #3090 and #3093). Two of the 36 carry no plan task that this sweep could name (#3080, which is issue #1738, and #3083, a governance budgets document); the others are named in their rows or are debt of a merged task (#3088, #3090, #3091, #3092, #3093 and #3094). Follow-up and debt runs live with no pull request of their own: AP3's debt (0385), B6's (867a), I1's senior review (f6bf), P5.1c's (07be), S7.3's (0238) and P3.2's (4556). Orchestrator work: the orchestrator is installed under launchd and replaces `/jobmax`. This sweep reads the 33 commits merged since the status commit of #3075 (2026-11-17 14:33 UTC) against each plan's task list; `#2690` was closed on 2026-11-17 as superseded by RE1 to RE3 (#2932, #2935, #2939). Rows that could not be re-derived are marked "about" or say so: licence-key-v2 (19/26 not reconciled), subscriptions (the owner's census of 2026-11-17, not re-derived), review-levels-ab-jobmax, rigor-modes and mobile; credentials-service is Done by the owner's ruling.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-token-types | 18/19 | CK (Checks 590 and 591: the two owner-run scripts are draft #3038, stalled; the owner runs them) |
| bitbucket-workspace-token | 9/12 | T1.4, T2.4 and T2.7 (T2.2 was carried by shared-jobs-default S1 and S4, both merged; T2.9 is closed as replaced by the service's rules, D1981, and is not counted in the 12); T2.4 and its phase-3 half rewritten for the proxy (a shared Bitbucket job holds no token) and approved to launch 2026-11-16 |
| forge-authorship-images | 18/25 | 7 left, all agent tasks approved 2026-11-16 (owner-run A5 and the unscheduled C8b/C8c stay out); D3 (open PR #3061, not a draft, no run going), the rest not started; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| forge-labels | 8/9 | T7 (draft #3096, live: run 0e11; its prerequisite E10b merged as #2928 on 2026-11-17) and owner Check 640, which runs after T7 |
| go-to-market | 11/12 | owner G2.5 |
| licence-key-v2 | about 19/26 | not reconciled against the plan in this sweep, so the count is about; K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); K4.2 is open PR #2873 (not a draft, no run going), the rest of K0–K7 not started or open (K3.4 held on TN1); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| mobile | about 52/67 | P5.1d, P5.1f and P5.1g (approved 2026-11-14, not started; P5.1f no longer waits on P4.1), P5.2.4 (draft #3054, live) and P5.2.6 to P5.2.10 (approved 2026-11-14, not started), P5.3.3 (draft #2900, live: a follow-up run on its three review findings), P5.3.5 and P5.3.7 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| model-crafts | 25/26 | MC26 (stays owner-run) and the activation plan for the four known limits, all approved 2026-11-16; F27, D1660–D1671 and D1673 were all answered as recommended on 2026-11-17, and D1672 on 2026-11-15 (A, then B and C together in one release, then D), so no decision is open; owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| outer-loop-escalation | 16/18 | E14 and E15 (owner-run), not started; D2046 ruled 2026-11-13: `escalation.ready_drafts` is on by default; owner Checks 540 to 542 |
| periodic-reviews | 12/17 | R9 (unblocked and launchable: the owner answered E6 on 2026-11-17, a designated repository plus a folder set by the owner in the project layer, public allowed, the launcher hands the bytes in, and its design has merged as #3039), the three tasks that design added, R14 (draft #3076, live), R15 and R16 (not started), and R5 (owner-run); owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| review-job | 7/10 | RJ5 (draft #3082, live); RJ9, RJ10 not started and not approved; owner Checks 610 and 611 |
| review-levels-ab-jobmax | about 29/35 | A/B reviewer slot; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded and not counted in the 35, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; X1 waits on T3.1; B1.2 and B1.4 merged 2026-11-17 (#2771, #2660); the count is about because the remaining tasks of the slot are not all named here |
| subscriptions | about 36/50 | a census of 2026-11-17 found 34 of 41 agent tasks done before S7.1 and S7.3 merged the same day (#3046, #3048), which makes 36, with the 10 owner-run (none run) in the 50; S1.7 is superseded and not counted. Open agent tasks: S0.7 (the app's first release, mobile-v0.200.0), S1.9 (waits on the enforcement release) and S2.9 (the production public key, which waits on owner S3.8), and about one more the census did not name; owner-run: S1.10 (Check 178, deferred), S3.7, S3.8, S6.4, S7.4 and S8.1–S8.6 (counsel closed S8.4 by the owner's ruling of 2026-11-17, recorded by #3042); TN1 seat enforcement is not in 0.24.0 (owner). The count rests on that census and was not re-derived task by task |
| task-governance | 28/31 | 3 left, all in `ee/governance`: T7.5 (Checks 137 and 138, draft #3087, live) and two more by the plan's own list (T7.4 and T8.2, not re-derived here); T7.3 merged 2026-11-17 (#2817) |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| composite-commands | 3/10 | AP4 (draft #3086, live), AP5 (draft #3089, live) and AP6 (draft #3085, live), all in the 0.26.0 gate; AP8 (draft #3084, live); AP7, AP9 and AP10 not started; depends on no-prompt-policy; AP3 merged 2026-11-17 (#2816), with a debt run on its apply door's guard (0385) |
| config-ownership | 12/22 | best-effort for 0.26.0 and not in its gate (owner, 2026-11-17); all tasks approved 2026-11-15; 10 left: N2 free to start; W1, W2, W3 and K1 on composite AP5, A2 and K2 on composite AP6; M4 (free now that M3, #2576 and #2538 have merged); then I1 and D1; D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| orchestrator-graph-estimates | 15/39 | 24 left, all approved 2026-11-16: R4 (draft #3063), G1 (draft #3064), E1 (draft #3065), G2 (draft #3067), S5 (draft #3081), all live; E2 (open PR #3066), B1 (open PR #3068), B7 (open PR #3070), B3 (open PR #3078) and S3 (open PR #3079), not drafts with no run going; the rest not started (R3, R5, R6, G4, E3, E4, S4, S6, B2, B4, B5, B9, B10, W1); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); Checks 700–703 owner-run |
| portal | 28/42 | owner P0.1 to P0.6; P5.1 (draft #3072, live); not started: P2.4, P5.2, P6.1 to P6.3, P9.3, P9.4; P4.3 and P3.2 merged 2026-11-17 (#3016, #3058), P4.3 with a debt PR (#3088, not a draft) |
| repos-management | 4/7 | RE4 (draft #2984, live: a follow-up run on the provider mismatch and `rm` keeping) and RE5 (not started), both in the 0.26.0 gate; RE6 owner-run after 0.26.0, with Check 620, not a gate; #2690 (the earlier brief's RE1) was closed 2026-11-17 as superseded by RE1 to RE3 (#2932, #2935, #2939); all approved 2026-11-15 and D2400–D2417 answered the same day |
| rigor-modes | about 25/40 | T7.1 (not started), T8.1 (the rigor live check, Check 127 written: open PR #3044, not a draft, no run going); the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open |
| secrets-brokering | 42/65 | B1.16; owner Check 192 (the --after-flip half; the flip itself merged as #2166 on 2026-11-17, after Check 191 passed its pre-flip rows, and its operator steps are release notes 9 and 10); B2.6–B2.9, B3.6 (Check 194 written, draft #3049, stalled), B3.7–B3.10, B4.2–B4.11 and B5.6–B5.9 (all approved 2026-11-10, each with its own gate; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511) |
| shared-jobs-default | 20/32 | 12 left, none of them with a run: S6, S7, S8, S10, N1 to N3, M2 and M4 to M7 not started; owner Checks 191, 192, 580, 581 and 582; B4, B6 and I1 merged 2026-11-17 (#3029, #3023, #3024), and draft #3093 (Check 581 reads the project of the checkout its build runs from) is open, stalled |
| troubleshoot-cloud | 7/12 | 5 left, all agent tasks approved 2026-11-16: T-TC5 (draft #3030, live: a follow-up run) is open, T-TC7 to T-TC9 not started; T-TC10 is owner-run; T-TC6 merged 2026-11-17 (#3056) |

## To do or parked

| Spec | State |
| --- | --- |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans | no plan or not a spec |

## Done

| Spec | Left |
| --- | --- |
| account-aliases | owner Check 550, not run; D2090–D2102 answered as recommended |
| accounts-add | owner checks LC1 to LC4 |
| api-key-review | owner Check 330 (AK8); AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
| binary-allow-rule | none (AR7 merged as #3077 on 2026-11-17; all approved 2026-11-15) |
| bitbucket-credentials | owner Check 149; 148's read half parked by the owner 2026-11-14 until the coordinator job is deployed (`fugaro update --firebase-project <id> --coordinator`) (Check 145 ran 2026-11-14: U3 shows the read token reaches past its workspace; the owner accepted the reach, D360 and D361 stand) (S2, D1961); T3.3's trust paths (S3 is otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| c39-review-identity | owner live check (Check 530; its script, merged as #2626, is on main and not yet run) |
| catalog-build-context | issue #1738 (milestone 0.26.0): the gate flip is open PR #3080 (not a draft), in the 0.26.0 gate; owner Check 292 runs after it, on the sandbox installation only |
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
| troubleshoot-issue | none |
| vision-model | owner Checks 520 and 521, not run; issue #1865 carries the 0.26.0 milestone: its code merged in 0.25.0 and only these two checks remain |
| website | W3.3 explainer video (parked), owner Check 69c (not written yet; 69a and 69b passed 2026-11-15) |
| website-changelog | WC4 lands with the next release |
| windows-cli | superseded by console-windows |
| workspaces | none |
