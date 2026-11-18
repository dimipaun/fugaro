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

As of 2026-11-18 04:35 UTC (night), read from `main` at `312a3957a` (#3167), the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page for its current state. The sections run from the nearest to done to the furthest; the finished specs are at the end. Released: CLI 0.25.0 (Vole, tagged and published 2026-11-15; the last CLI tag), console 0.122.0 (Strawberry). Console 0.123.0 (Tangerine) has its release commit on main (#3105) and is not tagged (no `console-v0.123.0` tag exists); console 0.124.0 (Ugli) is scoped and not tagged. CLI 0.26.0 is not yet cut, and every item of its gate is now merged: triage-loud (#2829, #2849 and #2910), repos-management RE4 (#2984) and RE5 (#3118), composite-commands AP3 (#2816) to AP6 (AP4 #3086, AP5 #3089, AP6 #3085) and the catalog-context gate flip of issue #1738 (#3080; its owner Check 292 runs after it, and the script that writes it is draft #3145). config-ownership is best-effort for 0.26.0 and not in the gate (owner, 2026-11-17). RE6 (renumbered to Check 670, Checks 670 to 679, by the owner's ruling of 2026-11-18) and Check 192 (the `--after-flip` half of the B1.13 flip, #2166, merged 2026-11-17; its operator steps are items 9 and 10 of `docs/releases/v0.26.0.md`) are owner-run after the release and are not a gate; the renumber pull request is not merged yet, so the repository still reads Check 620 for RE6 and troubleshoot-cloud's T-TC10 (to become Check 680, Checks 680 to 689). Issue #1865 (vision-model) carries the 0.26.0 milestone: its code merged in 0.25.0 and only owner Checks 520 and 521 are not run. **Main is green.** CI run 44234580786 on `f78d09de6` (#3158, which fixed the red that #3123 meeting #3112 caused) succeeded; three commits have merged since (#3093, #3125 and #3167) without a dispatched run, and #3167 fixed `npm run check` in `website/`, which was red on main because of the home page's watch sentence. GitHub reports 13 open Dependabot vulnerabilities, 5 of them high. The secrets-brokering flip chain B2.7, B3.8, B4.9 and B5.7 is approved to launch now, one at a time, with its owner checks moved to after-flip runs; Check 195 gates only B4.9's flip, Check 580 gates only shared-jobs-default M2, and composite AP7 launches after the flips (owner, 2026-11-18). There are 29 open pull requests (this one excepted), all of them drafts, read from `fugaro watch --once` at 04:30 UTC (its queued runs were unavailable, so a run still queued is not seen; `fugaro runs ls` timed out) and from the last `Fugaro run` comment on each: 4 are live (#3135, #3137, #3143 and #3145); the other 25 have no run going and are stalled, none ready. Of those 25, 19 ended `draft` between 04:14 and 04:24 UTC with the provider's 403 (`Failed to authenticate`; #3134, #3136, #3139, #3140, #3147, #3148, #3150, #3151, #3152, #3154, #3155, #3156, #3159, #3161, #3162, #3163, #3164, #3165 and #3166, which includes the runs launched tonight), and 6 ended `draft` for their own reasons: #3109 (the fix after review round 1 was not reviewed), #3126 (tests failing on the final commit), #3129 (review level 2 left changes), #3141 (the agent made no commits), #3142 (the adversarial stage's process exited without a result) and #3144 (halted on one owner decision). Four follow-up runs for the findings of landed pull requests are #3162 (portal P2.4), #3163 (mobile P5.1d), #3164 (portal P9.3) and #3165 (portal P6.1), and two fix runs are #3159 (shared-jobs-default S6) and #3161 (`fugaro followups` finding its git credential: the command needs the GitHub App ID at the owner's terminal and cannot run in an agent session); the landings #3125, #3123, #3112 and #3115 were landed as-is that way (batch jm250). Orchestrator work: the orchestrator is installed under launchd and replaces `/jobmax`. This sweep reads the 76 commits merged since the status commit of 2026-11-17 17:15 UTC (`a77929489`) against each plan's task list; group audits found rows that had already merged (composite AP4, AP5, AP6 and AP8, bitbucket-workspace-token T2.4 (#2882), orchestrator-graph-estimates R6, E4, B5 and B10, task-governance T7.4, T7.5 and T8.2, mobile P5.2.4 (#3054), rigor-modes T7.1 and T8.1). Rows that could not be re-derived are marked "about" or say so: forge-authorship-images, licence-key-v2 (not reconciled), subscriptions (the owner's census of 2026-11-17, not re-derived), review-levels-ab-jobmax, rigor-modes, secrets-brokering and mobile; credentials-service is Done by the owner's ruling.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-workspace-token | 10/12 | T1.4 and T2.7 (owner-run: Checks 180 and 181); T2.2 was carried by shared-jobs-default S1 and S4, both merged; T2.9 is closed as replaced by the service's rules, D1981, and is not counted in the 12 |
| composite-commands | 7/10 | AP7 (the audit of "now run X" texts: launches after the secrets-brokering flips, owner 2026-11-18), AP9 and AP10, all not started; depends on no-prompt-policy |
| forge-authorship-images | about 20/25 | the tasks still open are not re-derived (the plan's own State lines date from 2026-11-10): owner-run A5 (Check 230) and the unscheduled C8b/C8c stay out; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| forge-labels | 9/9 | CK (draft #3155, stalled: its run ended on the provider's 403) and owner Check 640, which runs after it; the daemon's identity to the credentials service (F19, a prerequisite outside this plan) is draft #3148, stalled |
| go-to-market | 11/12 | owner G2.5 |
| licence-key-v2 | about 20/26 | not reconciled against the plan in this sweep, so the count is about; K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); K4.2 merged as #2873, the rest of K0–K7 not started or open (K3.4 held on TN1); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| mobile | about 54/67 | P5.1d's launch half (the resident pass merged as #3115; the launch itself is not delivered, a product and scope decision for the owner; its follow-up is draft #3163, stalled), P5.1f (held: it collides with P5.1d on `orchestrator_serve.go`) and P5.1g (approved 2026-11-14, not started), P5.2.6 to P5.2.10 (approved 2026-11-14, not started), P5.3.5 (draft #3139, stalled), P5.3.7 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| model-crafts | 25/26 | MC26 (stays owner-run) and the activation plan for the four known limits, all approved 2026-11-16; F27, D1660–D1671 and D1673 were all answered as recommended on 2026-11-17, and D1672 on 2026-11-15 (A, then B and C together in one release, then D), so no decision is open; owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| orchestrator-graph-estimates | 30/39 | 9 left, all approved 2026-11-16: B2 (draft #3135) and B9 (draft #3137), live; R3 (draft #3142), G4 (draft #3129), E3 (draft #3134) and S4 (draft #3126), stalled; R5, B4 and W1 not started (R5 waits on G4 and W1); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); Checks 700–703 owner-run |
| outer-loop-escalation | 17/18 | E15 (the owner-run Checks 540, 541 and 542 written as scripts: draft #3147, stalled); `escalation.ready_drafts` ships on by default (#3146, D2046) and the daemon already reads it (#3149 found it wired and changed nothing); owner Checks 540 to 542 |
| periodic-reviews | 13/17 | R9 (draft #3140, stalled: the owner answered E6 on 2026-11-17, a designated repository plus a folder set by the owner in the project layer, public allowed, the launcher hands the bytes in), R15 and R16 (not started) and R5 (owner-run); owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| portal | 32/42 | owner P0.1 to P0.6; P6.2 (draft #3166, stalled: its run ee86 ended on the provider's 403 at 04:16 UTC); not started: P5.2, P6.3 and P9.4, which wait on P6.2; follow-up runs on P2.4, P6.1 and P9.3 are drafts #3162, #3165 and #3164, stalled |
| repos-management | 6/7 | RE6 (owner-run: its Check is renumbered to Check 670 by the owner's ruling, and the renumber pull request is not merged yet); #2690 was closed 2026-11-17 as superseded by RE1 to RE3 (#2932, #2935, #2939); all approved 2026-11-15 and D2400–D2417 answered the same day |
| review-job | 8/10 | RJ9 (draft #3150, stalled) and RJ10 (draft #3154, stalled), both approved 2026-11-18; owner Checks 610 and 611 |
| review-levels-ab-jobmax | about 29/35 | B1.3 (draft #3151, stalled: scores and reports the review slots); U4 is reported unreliable until the owner runs Check 211; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded and not counted in the 35, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; X1 waits on T3.1; the count is about because the remaining tasks of the slot are not all named here |
| shared-jobs-default | 25/32 | 7 left: M6 (draft #3144, stalled: its run halted on one owner decision), then M7 and S10, which launch when M6 merges; M2, which waits for owner Check 580 (the script is merged, #3121); M4 and M5 not started (M5 consumes M2); S7 (draft #3109, stalled); a fix run on S6 is draft #3159, stalled; owner Checks 191, 192, 580, 581 and 582 |
| subscriptions | about 36/50 | a census of 2026-11-17 found 34 of 41 agent tasks done before S7.1 and S7.3 merged the same day (#3046, #3048), which makes 36, with the 10 owner-run (none run) in the 50; S1.7 is superseded and not counted. Open agent tasks: S0.7 (the app's first release, mobile-v0.200.0), S1.9 (waits on the enforcement release) and S2.9 (the production public key, which waits on owner S3.8), and about one more the census did not name; owner-run: S1.10 (Check 178, deferred), S3.7, S3.8, S6.4, S7.4 and S8.1–S8.6 (counsel closed S8.4 by the owner's ruling of 2026-11-17, recorded by #3042); TN1 seat enforcement is not in 0.24.0 (owner). The count rests on that census and was not re-derived task by task |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| config-ownership | 14/22 | best-effort for 0.26.0 and not in its gate (owner, 2026-11-17); all tasks approved 2026-11-15; 8 left: W1, W2, W3 and K1 (not started; they waited on composite AP5, which has merged), A2 (draft #3141, stalled: the agent made no commits), K2 (draft #3136, stalled), I1 and D1 (not started); D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| rigor-modes | about 27/40 | T8.2 (owner-run: Check 127, written); the overlay plan LV1 to LV7 is unblocked in principle (LD2: the level wins), but LV1.1's type-naming and record-version rulings are still open; the count is about because the overlay's tasks are not all counted |
| secrets-brokering | about 43/65 | B1.16; owner Check 192 (the --after-flip half; the flip itself merged as #2166 on 2026-11-17); the flip chain B2.7 (draft #3152, stalled), B3.8, B4.9 and B5.7, approved to launch one at a time with the owner checks moved to after-flip runs (owner, 2026-11-18); B2.6, B2.8 and B2.9; B3.7, B3.9 and B3.10; B4.2 and B4.3 (draft #3156, stalled), B4.4–B4.11 (owner Check 195 gates only B4.9's flip); B5.6, B5.8 and B5.9 (all approved 2026-11-10; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511); the count is the previous sweep's 42 plus B3.6 (#3049) and is about because the plan's 65 does not reconcile with the 23 tasks named here |
| troubleshoot-cloud | 8/12 | 4 left, all agent tasks approved 2026-11-16: T-TC7 (draft #3143, live), T-TC8 and T-TC9 (not started); T-TC10 is owner-run (its Check is renumbered to Check 680 by the owner's ruling; the renumber pull request is not merged yet) |

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
| bitbucket-token-types | owner Checks 590 and 591 (scripts on main), not run |
| c39-review-identity | owner live check (Check 530; its script, merged as #2626, is on main and not yet run) |
| catalog-build-context | issue #1738 (milestone 0.26.0): the gate flip is merged (#3080), in the 0.26.0 gate; the script that writes Check 292 is draft #3145 (live); owner Check 292 runs after it, on the sandbox installation only |
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
| task-governance | owner Checks 137 and 138 (scripts on main, #3087), not run |
| troubleshoot-issue | none |
| vision-model | owner Checks 520 and 521, not run; issue #1865 carries the 0.26.0 milestone: its code merged in 0.25.0 and only these two checks remain |
| website | W3.3 explainer video (parked), owner Check 69c (not written yet; 69a and 69b passed 2026-11-15) |
| website-changelog | WC4 lands with the next release |
| windows-cli | superseded by console-windows |
| workspaces | none |
