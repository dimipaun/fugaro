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

As of 2026-11-19 02:00 UTC (night), read from `main` at `0db80aa40` (#3198), the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. Released: CLI 0.26.1 (a hotfix of 0.26.0, tagged `v0.26.1` at `52e1e1a21` and published 2026-11-18 17:47 UTC; its notes are on `main`, #3212), CLI 0.26.0 (tagged `v0.26.0` at `271063335`, published 2026-11-18 14:24 UTC), console 0.123.0 (Tangerine, tagged `console-v0.123.0`, published 2026-11-18 05:11 UTC), CLI 0.25.0 (Vole) and console 0.122.0 (Strawberry) before them. Console 0.124.0 (Ugli) has its whole scope merged and is not tagged: the tag awaits the owner's go. CLI 0.27.0 is the next release (scope ruled 2026-11-17; its gate is secrets-brokering, graph-estimates with the orchestrator, config-ownership with composite-commands, and troubleshoot-cloud, periodic-reviews and model-crafts). RE6 (to be renumbered Check 670, Checks 670 to 679, by the owner's ruling of 2026-11-18) and Check 192 (the `--after-flip` half of the B1.13 flip, #2166, merged 2026-11-17; its operator steps are items 9 and 10 of `docs/releases/v0.26.0.md`) are owner-run after the release and are not a gate; the renumber pull request is not merged yet, so the repository still reads Check 620 for RE6 and troubleshoot-cloud's T-TC10 (to become Check 680, Checks 680 to 689). Issue #1865 (vision-model) carries the 0.26.0 milestone: its code merged in 0.25.0 and only owner Checks 520 and 521 are not run. **Main is green as of its last dispatched run.** CI run 44432700611 on `f368165f7` succeeded (it followed the red-main fixes #3209 and #3219); two docs-only commits (#3198, #3199) have merged since without a dispatched run. The secrets-brokering S2 flip (B2.7, #3152) and S3 flip (B3.8, #3170) have merged; the S4 flip B4.9 is live (#3222) and B5.7 follows; Check 195 gates only B4.9's flip, Check 580 gates only shared-jobs-default M2, and composite AP7 launches after the flips (owner, 2026-11-18). There are 39 open pull requests, read from `gh pr list` at 01:56 UTC and from the Fugaro status line in each body (no `fugaro runs ls`, which is unreliable): 34 are drafts and 5 are not. **32 are live** (a run is going, its status line updated within the hour): #3137 (orchestrator B9, a continuation follow-up), #3211 (an upgrade guard: `fugaro update` never hides a refusal), #3213 (WU0), #3215 (B4.6), #3216 (R16), #3217 (T-TC9), #3218 (P6.3), #3220 (W2), #3222 (B4.9), and the 23 first-wave runs launched tonight under console-improvements (#3223 to #3231 and #3234 to #3236: LP2, SM0, RS1, MG0, P1, RB1, GF1, WH1, ST1, C-OPS, MD1 and MD2) and mobile-identity-pairing (#3232, #3233 and #3237 to #3245: T0, DA, C1, C2, A2, P0, A1, J1, A3, P6 and P5); those two batches were still being launched at the read, so the list grows. **5 are ready** (no run going, not a draft, outcome `ready`): #3206 (the rehearsal of this repository's own catalog build before the release tag, a release gate: it needs the owner's review), #3207 (rigor-modes LV1.1), #3210 (mobile P5.1d's resident launcher), #3214 (periodic-reviews R15) and #3221 (console-improvements LP1). **2 are stalled**: #3205 (images: the real `scan.sh` on a catalog-shaped image; tests failing on `2657952`, no run since 00:43 UTC) and #3208 (an upgrade guard: `fugaro update` checks the owner's local config up front; halted on the run cap, tests passed on `8a53f3e`; #3211 is a relaunch in the same family). The upgrade guards (#3208, #3211) and the images scan fix (#3205) belong to no plan. This sweep reads the 33 commits merged since the 14:50 UTC read (`0d2fe54fc`) against each plan's task list: secrets-brokering B2.7, B3.8 and B4.5, shared-jobs-default M7 and S10, orchestrator-graph-estimates B4, config-ownership W1 and A2, outer-loop-escalation E15, portal P6.2, troubleshoot-cloud T-TC8, periodic-reviews R9 and mobile P5.3.5, plus the plans of mobile-identity-pairing (#3198), console-improvements (#3199) and workspace-projects-users (#3195), and findings and fixes that are not plan tasks (#3180, #3188, #3196, #3200 to #3204, #3209, #3212, #3219). Rows that could not be re-derived are marked "about" or say so: forge-authorship-images, licence-key-v2 (not reconciled), subscriptions (the owner's census of 2026-11-17, not re-derived), review-levels-ab-jobmax, rigor-modes, secrets-brokering and mobile; credentials-service is Done by the owner's ruling.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-workspace-token | 10/12 | T1.4 and T2.7 (owner-run: Checks 180 and 181); T2.2 was carried by shared-jobs-default S1 and S4, both merged; T2.9 is closed as replaced by the service's rules, D1981, and is not counted in the 12 |
| composite-commands | 7/10 | AP7 (the audit of "now run X" texts: launches after the secrets-brokering flips, owner 2026-11-18), AP9 and AP10, all not started; depends on no-prompt-policy |
| forge-authorship-images | about 20/25 | the tasks still open are not re-derived (the plan's own State lines date from 2026-11-10): owner-run A5 (Check 230) and the unscheduled C8b/C8c stay out; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 11/12 | owner G2.5 |
| licence-key-v2 | about 20/26 | not reconciled against the plan in this sweep, so the count is about; K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); K4.2 merged as #2873, the rest of K0–K7 not started or open (K3.4 held on TN1); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| mobile | about 55/67 | P5.1d's launch half (the resident pass merged as #3115; the launcher is ready pull request #3210, not yet landed), P5.1f (held: it collides with P5.1d on `orchestrator_serve.go`) and P5.1g (approved 2026-11-14, not started), P5.2.6 to P5.2.10 (approved 2026-11-14, not started), P5.3.7 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| model-crafts | 25/26 | MC26 (stays owner-run) and the activation plan for the four known limits, all approved 2026-11-16; F27, D1660–D1671 and D1673 were all answered as recommended on 2026-11-17, and D1672 on 2026-11-15 (A, then B and C together in one release, then D), so no decision is open; owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| orchestrator-graph-estimates | 36/39 | 3 left, all approved 2026-11-16: B9 (draft #3137, live: a continuation follow-up is running on it), R5 (not started: it waited on G4, merged, and still waits on W1) and W1 (not started; both wait on B9); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); Checks 700–703 owner-run |
| periodic-reviews | 14/17 | R15 (ready pull request #3214), R16 (draft #3216, live) and R5 (owner-run); owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| portal | 33/42 | owner P0.1 to P0.6; P6.3 (draft #3218, live); not started: P5.2 and P9.4, which waited on P6.2 (merged) |
| repos-management | 6/7 | RE6 (owner-run: its Check is renumbered to Check 670 by the owner's ruling, and the renumber pull request is not merged yet); #2690 was closed 2026-11-17 as superseded by RE1 to RE3 (#2932, #2935, #2939); all approved 2026-11-15 and D2400–D2417 answered the same day |
| review-levels-ab-jobmax | about 30/35 | U4 is reported unreliable until the owner runs Check 211; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded and not counted in the 35, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; X1 waits on T3.1; the count is about because the remaining tasks of the slot are not all named here |
| shared-jobs-default | 29/32 | 3 left: M2, which waits for owner Check 580 (the script is merged, #3121); M4 and M5 not started (M5 consumes M2); owner Checks 191, 192, 580, 581 and 582 |
| subscriptions | about 36/50 | a census of 2026-11-17 found 34 of 41 agent tasks done before S7.1 and S7.3 merged the same day (#3046, #3048), which makes 36, with the 10 owner-run (none run) in the 50; S1.7 is superseded and not counted. Open agent tasks: S0.7 (the app's first release, mobile-v0.200.0), S1.9 (waits on the enforcement release) and S2.9 (the production public key, which waits on owner S3.8), and about one more the census did not name; owner-run: S1.10 (Check 178, deferred), S3.7, S3.8, S6.4, S7.4 and S8.1–S8.6 (counsel closed S8.4 by the owner's ruling of 2026-11-17, recorded by #3042); TN1 seat enforcement is not in 0.24.0 (owner). The count rests on that census and was not re-derived task by task |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| config-ownership | 17/22 | best-effort for 0.26.0 and not in its gate (owner, 2026-11-17); all tasks approved 2026-11-15; 5 left: W2 (draft #3220, live), W3 and K1 (not started; they waited on composite AP5, which has merged), I1 and D1 (not started); D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| console-improvements | 0/46 | 12 first-wave runs live (LP2 #3223, SM0 #3224, RS1 #3225, MG0 #3226, P1 #3227, RB1 #3228, GF1 #3229, WH1 #3230, ST1 #3231, C-OPS #3234, MD1 #3235, MD2 #3236) and LP1 ready (pull request #3221); the other 33 tasks not started; owner-run Checks 710 (re-record the Live and Graph goldens), 711 (read timings) and 712 (the wizard on a scratch project) |
| mobile-identity-pairing | 0/45 | plan merged as #3198; 11 first-wave runs live (T0 #3232, DA #3233, C1 #3237, C2 #3238, A2 #3239, P0 #3240, A1 #3241, J1 #3242, A3 #3243, P6 #3244, P5 #3245); the other tasks not started; owner-run M0 and Z0, and Checks 730 to 737 (T0 writes them) |
| rigor-modes | about 27/40 | T8.2 (owner-run: Check 127, written); the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), and LV1.1 is ready pull request #3207; the count is about because the overlay's tasks are not all counted |
| secrets-brokering | about 48/65 | B1.16; owner Check 192 (the --after-flip half; the flip itself merged as #2166 on 2026-11-17); the flip chain: B4.9 (draft #3222, live) and B5.7 (not started), approved to launch one at a time with the owner checks moved to after-flip runs (owner, 2026-11-18); B2.6, B2.8 and B2.9; B3.7, B3.9 and B3.10; B4.2 (owner-run), B4.6 (draft #3215, live) and B4.7, B4.8, B4.10 and B4.11 (owner Check 195 gates only B4.9's flip); B5.6, B5.8 and B5.9 (all approved 2026-11-10; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511); the count is the previous sweep's 45 plus B2.7 (#3152), B3.8 (#3170) and B4.5 (#3181) and is about because the plan's 65 does not reconcile with the 23 tasks named here |
| troubleshoot-cloud | 10/12 | 2 left, both approved 2026-11-16: T-TC9 (draft #3217, live); T-TC10 is owner-run (its Check is renumbered to Check 680 by the owner's ruling; the renumber pull request is not merged yet) |
| workspace-projects-users | 0/26 | WU0 (the threat model and its independent security review: draft #3213, live, run `d625`); every other task waits on WU0's findings and its own go |

## To do or parked

| Spec | State |
| --- | --- |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| review-against-task | plan only: 0/16 tasks merged, none approved (docs/review-against-task); owner-run Check 750 after the release |
| run-messages | plan only: 0/11 tasks merged, none approved (docs/run-messages); D2940–D2947 answered 2026-11-18 as recommended; O1 and O2 answered; owner-run Check 750 after the release |
| voice-module, collateral, backlog, plans | no plan or not a spec |

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
