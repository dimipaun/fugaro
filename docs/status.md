<!--
How this page is written

1. The **Merged** column holds only the count, tasks done out of tasks in the plan (`20/23`); "about" marks an approximate count (`about 35/45`). Nothing else goes in that cell.
2. Every explanation of what remains goes in **Left**.
3. **Left** lists only the tasks still to do and the state of each (in a run, a draft PR #N, waiting on X, owner-run). Never finished work: merged tasks are counted in Merged and named nowhere else. A ruling on a still-open task may stay as a short clause on it.
4. Merged/total of 0.7 or more sits in "Nearly There (NT)", below 0.7 in "Work in progress (WIP)"; for an "about n/m" row, n/m decides.
5. A spec in the works (a task of it with a live run or an open pull request, draft or not, or a merged task) is never in "To do or parked", which holds only specs with nothing merged, no live run and no open pull request, plus the specs the owner parked by name (a parked spec keeps its merged count and is not worked until the owner unparks it).
6. A pull request is in one of four states, and the four words are the same ones everyone uses: **live** (a run is going on it, which is what rule 3's "in a run" means), **ready** (no run is going, it is not a draft, and the last run that opened or followed it ended with the outcome `ready`, a review job's own outcome `reviewed` changing nothing), **stalled** (it had a run, no run is going and it is not ready — the outcome `draft`, a `[DRAFT]`-titled ordinary pull request on a host with no drafts included, or a ready one a person turned back into a draft) and **orphan** (no run ever produced or follows it). Landing is an action, not a fifth state: to **land** a pull request is to merge it as-is, with the findings it still carries recorded as follow-up tasks, rather than running another fix round (the `landing` skill).
7. Every table sorts its specs alphabetically by name, ascending, case-insensitive; a row moved between tables takes its place there.
-->

# Fugaro: status of every spec under docs/

As of 2026-11-23 (evening, US Eastern), read from `main` at `ace449124` (#3748), the pull requests merged since the previous read (`bbcf21d08`, #3650) and each plan's task list (about 30 folders' own status lines are stale). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links here. The sections run from the nearest to done to the furthest, the finished specs last. **The rows of the specs named under "Merged since the last read" were re-derived on 2026-11-23 from the merged pull requests' titles and each plan's task ids; every other row stands as of 2026-11-22 or earlier and was not re-read**, so the live-run paragraphs below are not a read of the run list.

**Released:** CLI 0.28.2 (tagged `v0.28.2`, hotfix of 0.28.1: the per-run execution reads and the 429 retry; published 2026-11-23 22:00 UTC), CLI 0.28.1 (`v0.28.1`, published 2026-11-23 18:06 UTC), CLI 0.28.0 "Yak" (`v0.28.0` on the release commit `5975447c1`, #3683; published 2026-11-23 04:54 UTC; its two gates, #3534 managed-flag-ergonomics T0 and #3677 `repos add`, merged first), CLI 0.27.0 "Xerus" (`v0.27.0` on `1d268d850`, #3539, 2026-11-21; published 2026-11-22 04:08 UTC), console 0.125.0 "Watermelon" (`console-v0.125.0`, 2026-11-21; published 2026-11-22 00:29 UTC), CLI 0.26.1 (hotfix of 0.26.0, `v0.26.1`, 2026-11-18), CLI 0.26.0 and console 0.124.0 "Ugli" (`console-v0.124.0`, 2026-11-20). Every "after the 0.27.0 tag" and "after the 0.28.0 tag" gate below is open; each installation still needs the new fugaro (`brew upgrade`) and `fugaro update` before a check that depends on it can pass. The owner ruled on 2026-11-21 that 0.27.0 was cut without the pre-tag image rehearsal, which stays owed: `fugaro-sandbox` is an empty project and needs its billing link, `fugaro setup`, a local config and a sandbox repository before `scripts/release-rehearsal.sh run` can run. Composite-commands AP7 (#3517) and AP9 (#3578) shipped in 0.28.0; AP10 has not merged. RE6's Check 670 is owner-run after the release and is not a gate; the plans of RE6 and T-TC10 read Checks 670 and 680, and both scripts are on main (#3524, #3531).

**CI:** not re-read. The last dispatched `ci` runs on record (2026-11-22): 45075705623 passed (`ci (full)`) on `9cda24af1` (#3670); 45073200751 passed on `2548f8146` (#3669). No `ci` run on a later commit was read, so the tip's state is unknown.

**Open pull requests:** 52, from `gh pr list` at this sweep (48 drafts; the four that are not: #3638, #3653, #3656 and #3711). Live runs were not re-read; the rows name each open pull request by number. Running now, by the owner's account: console-fixes RI5, IR4 and ID5; model-credentials-routing RT3, PL1, PL7, CS1, CS11, GV1 and PR4; workspace-projects-users WU10, WU16 and WU17; launcher-does-not-mint LM6; and an image-build guard task.

Merged since the last read (67 commits on `main` after `bbcf21d08`, #3650, among them the previous refresh #3678): the releases 0.28.0 (#3682, #3683), 0.28.1 (#3704) and 0.28.2 (#3728); managed-flag-ergonomics T0 (#3534); the `repos add` fix 0.28.0 waited on (#3677); model-credentials-routing RT2 (#3606), CS4 (#3613), VS2 (#3617) and PR5 (#3712, with its follow-up #3740); workspace-credentials WC4 (#3709), WC5 (#3661), WC9 (#3652) and WC10 (#3718, F21; the red main of #3709 and #3661 together, #3747); workspace-service WS19 (#3715); workspace-projects-users I1 (#3585, with its census fix #3707); console-fixes RI4 (#3619), IR3 (#3623), IR6 (#3628), ID2 (#3624), CG7b (#3660), ID6 (#3724) and CG1c (#3725); console-improvements MWR (#3655) and PW1 (#3732); bitbucket-workspace-token T2.7 (#3676: Check 181's script); batch-run-identity BR13 (#3664: Check 831); review-against-task's Check 750 script (#3675); required-version-stamp, new: its spec, reconciliation and plan (#3710), T1 (#3734) and T2c (#3722); repo-coupling-audit's checks and the report's live columns (#3684 to #3701, #3703: the rulings on B1 to B19 and the lift order); forge-labels' two plan fixes (#3671, #3673); and, outside any plan, every run has a title (#3702, #3705, #3706, #3748: a follow-up whose host cannot be read is named `Follow-up of PR #N`, a launch with no title is refused), `fugaro setup` adopting a GCP project that holds nothing of Fugaro's (#3611), `fugaro update` keeping a baked repository's per-repository jobs (#3699), the quota retry and the leaner `runs ls` read that 0.28.2 carries (#3721, #3726), the review brief's remote-call rule (#3727), the release script's second-patch rule (#3731), five leftovers of the claim route and the log-bucket refusal (#3595) and the fixes that kept main building (#3680, #3681, #3685, #3697, #3698, #3706, #3707, #3736). Rows whose count is still approximate are marked "about" and say why: subscriptions, review-levels-ab-jobmax and rigor-modes; credentials-service is Done by the owner's ruling.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-workspace-token | 11/12 | T1.4 (owner-run: Checks 180 and 181, neither run; both scripts are on main); no agent task left; T2.9 closed as replaced by the service's rules (D1981), not counted in the 12 |
| ci-affected-tests | 9/12 | CS6b (launchable: the workflows' copy-in), CS8 (behind CS6b) and CS9 (owner-run Check 800, runnable); no pull request open; all approved 2026-11-20 (docs/ci-affected-tests); D3040–D3043 answered 2026-11-20, all as recommended |
| composite-commands | 9/10 | AP10 (writes the scripts of Checks 390 to 392, which the owner then runs; draft #3637, open); all approved 2026-11-07; no-prompt-policy is Done and blocks nothing here |
| console-improvements | 43/46 | draft PR: I-OPS #3638 (the stub bridge is gone, the operations match the contract); waiting: SW3b, SW3c; all six slices chosen and all tasks approved; owner-run Checks 710 (re-record the Live and Graph goldens), 711 (read timings) and 712 (the wizard on a scratch project) |
| forge-authorship-images | 20/24 | owner-run A5 (Check 230, text only, not run); B3 (GitLab) parked by D610 until a named need; C8b and C8c unscheduled (D650, 2026-11-16); B2 closed by the owner 2026-11-21 as superseded by the orchestrator's E8, not counted in the 24; no agent task launchable; owner Checks 232, 234 and 235 text only, not run; the plan's State lines date from 2026-11-10; the image-setup overlap is C6 and C8's C8b/C8c — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 11/12 | owner G2.5 (the inputs, the preview deploy and Checks 160 and 161) and owner Check 162; no agent task left |
| licence-key-v2 | 21/24 | owner-run K0.1 (counsel and the privacy text: the packet is drafted for the owner to send), K0.2 (the production signing keys, the same step as subscriptions S3.8 and portal P0.4: the KMS key rings and one enabled ED25519 key each exist in `fugaro-prod` and `fugaro-staging` since 2026-11-22, so the Terraform half is done and the services half of P0.4 is not applied) and K5.2 (the Paddle account); K2.6 and K5.1 superseded by the portal, K6.1 dropped 2026-11-16, none of the three counted in the 24; owner Checks 560–564 not run; D2130–D2146 answered 2026-11-11 except D2145 (default); D2134 (amended by TN2) and D2144 not the recommendation |
| licensing | 23/24 | owner-run L0.2 (confirm the CLA bot's allow-list); the next Terms version is a redline with counsel; the folder's README still says the Terms page is unpublished |
| location-preference | 14/18 | LP10 and LP14 (launchable by their plan edges; no pull request open), LP11 (the documents) and LP13 (the two live-check scripts), not re-read; all approved 2026-11-20 (docs/location-preference); the region stays the one setting and the place (US, EU) is derived from it; D3080–D3091 answered 2026-11-20, all as recommended, D3087 and D3088 amended after review and confirmed; D3092–D3097 answered the same day (D3097 not the recommendation: a fourth review round); D3090 removes the Firestore location's own confirmation, reversing D12 of 2026-10-20 for it; the owner redeploys the key service with LP15; owner-run Checks 820 and 821 |
| mobile | 62/67 | no pull request open: P5.2.7 waits on its row being restated for the relay; P5.2.10 launchable, not started; the owner answered the push design's Q1, Q3 to Q8 and Q10 on 2026-11-21 (the plan carries each answer); P5.4 unscheduled; owner P2.3 and P4.4. Not counted in the 67: the voice-session plan (6/26), which has no row here and waits on D207 and the approval of VS2 |
| model-crafts | 25/26 | MC26 (the scripts of Checks 628 to 632, draft #3436, open; running them is the owner's); no decision open; owner Checks 420 to 422 (a script each; 420 and 421 ran 2026-11-15 with the premise not met, which an installation updated to 0.27.0 now meets; 422 not run) |
| periodic-reviews | 16/17 | R5 (owner-run); owner Check 136; owner Checks 341, 342 and 343 written, not run |
| portal | 36/42 | owner P0.1 to P0.6, in the order P0.1 and P0.4, then P0.2, then P0.5 and P0.6 (P0.4 is the signing-keys step subscriptions S2.9 waits on: the key rings and the key exist in both projects since 2026-11-22, so its Terraform half is done and its services half is not applied; the staging Hosting site is `fugaro-portal-staging`, which the owner created and #3663 reads; the scripts of Checks 570 to 575 are on main, #3432); no agent task left |
| repos-management | 7/7 | the owner runs Check 670 after `fugaro update` on 0.27.0 (tagged); D2400–D2417 answered 2026-11-15; no agent task left |
| review-levels-ab-jobmax | about 28/34 | T3.1 (draft #3635, open: the app's crash reports follow the project's published switch), V1.3 and X1 (the re-plan against the orchestrator and the config-ownership registry; documents only, nothing in it approved, it waits on the owner's review, and no task in it launches before that review); owner T3.2 (the store privacy forms, after T3.1) and T4 (Terms §7.3, with counsel); J1.2, J1.3 and T1.1 to T1.3 closed as superseded, not counted in the 34; U4 unreliable until the owner runs Check 211; the count is about because L5.1 and T0.1 have no commit under their own ids and are counted on content |
| rigor-modes | about 42/48 | the 2026-10-20 plan: T7.3 and T8.2 (owner-run: this repository's own rigor block, and Check 127, written) and T0.3 (not verified); T2.4, T4.7, T5.3, T5.4, T5.5 and T6.3 closed 2026-11-18 as superseded by the Hotfix rulings, not counted. The overlay: LV5.1 and LV5.2 wait on their text being restated without T5.3 to T5.5; LV7.1 is draft #3654, open; the count is 31 of the plan's 34 live tasks plus 11 of the overlay's 14 |
| run-messages | 10/11 | RM8 (owner-run: Check 740; 0.27.0 is tagged, so it is runnable once the installation has fugaro 0.27.0 and `fugaro update`); no agent task left; D2940–D2947 answered 2026-11-18, all as recommended |
| runs-ls-speed | 7/8 | RL4 (built only if Check 760 reads `runs ls --since 7d` over 8 seconds, D2966): Check 760 passed (owner-run, 2026-11-22), so by the plan RL4 is not needed unless the read says otherwise (the result itself was not read here), yet its brief was launched on 2026-11-22 and its draft #3672 is open; D2960–D2967 taken as recommendations (docs/runs-ls-speed) |
| secrets-brokering | 51/65 | no code task left in the plan: the 14 open are 9 owner check runs and 5 record commits waiting on the owner's pasted output. 0.27.0 is tagged (2026-11-21), so every check is runnable once the installation has fugaro 0.27.0 and `fugaro update`: B4.2 (Check 195), B5.6 (Check 197), B2.6 and B2.8 (Check 193), B3.7 and B3.9 (Check 194), B4.8 and B4.10 (Check 196) and B5.8 (Check 197's --after-flip half); records B1.16, B2.9, B3.10, B4.11 and B5.9 follow their checks; every script is on main and none of Checks 193 to 197 has run; the dynamic-delivery subfolder (22/22) is not in the 65 and has nothing left |
| shared-jobs-default | 29/32 | 3 left: M2 waits on owner Check 580 (the owner reruns it with a binary that carries #3441, which the 0.27.0 tag does, so after `fugaro update` on 0.27.0); M4 and M5 wait on M2 (M5 writes Check 582's script); owner Checks 580, 581 and 582 |
| subscriptions | about 39/50 | 2 agent tasks open: S2.9 (the production public key in the three verifiers; launched 2026-11-22, run 2ac8, draft #3674; its edge S3.8's Terraform half is done, the key rings and key exist) and S8.3 (not launchable: the copyright commit moving the licence files to the entity waits on owner S8.2); S0.7 (what remains of it is the app's first release, the owner's); S1.7 and S1.9 closed (superseded by ee-reorg E1.4; the key-secret mount deferred by D524 and replaced by D2137); owner-run: S1.10 (Check 178, deferred, and its premise of a mounted key no longer holds), S3.7 and S3.8 (now the portal's P0.4 to P0.6 and licence-key-v2's K0.2), S6.4, S7.4, S8.1, S8.2, S8.5 and S8.6; the count is about because the 50 predates the closures |
| workspace-projects-users | 22/26 | all tasks approved; WU10, WU16 and WU17 in a run (WU17 writes the scripts of Checks 720 and 721); WU21 waits on WU10 |
| workspace-service | 24/25 | WS21 launchable by its plan edges, no pull request open, not re-read; all approved 2026-11-20; D2980–D2992 answered 2026-11-19 (D2984 not the recommendation; D2987 amended after review); owner-run Checks 770–773 (0.27.0 is tagged, so they run once the installation has fugaro 0.27.0 and `fugaro update`) |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| always-a-budget | 10/19 | all approved 2026-11-20, launching by plan edges; open drafts: AB4 #3583 and AB5 #3452; the rest not started or waiting on edges; budget mode off is removed (D3009, the owner's own; D3010 and D3011 in the keys plan); D3020–D3028 answered 2026-11-20 (D3020, D3022, D3023, D3025 and D3028 not the recommendation; D3026 the owner's own: no rule in terms of a repository's auth, which gets its own spec); one release, with no release cut from main between AB7 and I1; owner-run Checks 790 and 791; the keys plan's KS14 and KS15 wait on its I1 |
| batch-run-identity | 10/27 | BR4's claim half is draft #3563, open; BR2 is draft #3506, open; BR7 is draft #3629 (doctor: a Batch installation is ready once the claim can be made), open, held by the owner until BR1, BR16 and BR18 merge (2026-11-23); BR1 launched 2026-11-21 (state not re-read); C1, BRS, BR0 and BR6 approved 2026-11-20; D3100–D3112 answered 2026-11-20 (D3110 and D3111 not the recommendation, D3110 amended after review; D3104 supersedes D2075; D3102's second read is decided after Check 830); BR3, BR5 and BR12 wait on the owner's run of Check 830 and his answer on O1; BR7 and BR12 start after always-a-budget's AB9 if it has an open pull request; owner-run Checks 830 and 831 |
| console-fixes | 23/37 | in a run: RI5, IR4 and ID5; open drafts: RI2 #3626, IR1 #3630 and ID3 #3658; CG4 (#3574) held on an owner decision (D3209); planned 2026-11-21: D3200-D3214 answered, D3215-D3224 resolutions (D3223, D3224, the amended D3215, D3217, D3218, D3220, tension T1 and the reindex IAM question wait on the owner, O3, O4, O6, O7); owner Checks 870 and 871 after the build |
| keys-through-service | 9/20 | all approved 2026-11-20, launching by plan edges; 11 not started or waiting on edges (KS6, KS9 and KS10 stopped by the owner 2026-11-20: credentials-belong-to-models replaces them); D3000–D3015 answered 2026-11-19 and 2026-11-20 (D3005 and D3013 not the recommendation, D3007 and D3009 the owner's own; D3009 removes budget mode off under its own spec, not written yet); two releases; release A waits on the owner's run of Check 50 (D3013); KS14 and KS15 wait on that spec's plan, KS16 on secrets-brokering B5.7; owner-run Checks 50, 780 and 781 |
| launcher-does-not-mint | 17/26 | nine left: LM13 is draft #3634, open; LM6 in a run; LM14 to LM18, LM21 and I1 have no pull request; approved 2026-11-20 (docs/launcher-does-not-mint); the credentials service issues a run's identity as a one-time ticket the run claims, and `fugaro update` takes the signing grant off launchers, operators and the resident orchestrator once the service answers; D3120–D3137 answered 2026-11-20, all as recommended, D3129, D3130 and D3136 amended after review and answered again, D3125 amended by D3136 (D3121 amends the rule on the credentials account's grants; D3129 holds everything in batch-run-identity that touches its key or its C1 until LM19; D3130 enforces the launch scope at the issuer; D3131 accepts that executions queued and not started halt at the update); the nine left were recorded as waiting on always-a-budget's I1 and keys-through-service's release A (D3128), not re-read; owner-run Checks 840 and 841 |
| managed-flag-ergonomics | 4/12 | T7a is draft #3665 (every command that writes to a project refuses an unmanaged one, D2217) and a follow-ups draft #3590 is open; the rest not started or waiting on edges; D2211-D2219 and D3160 answered (only managed projects: every writing command stops on an unmanaged one); docs/managed-flag-ergonomics |
| mobile-identity-pairing | 12/45 | tasks not started; owner-run M0 and Z0, and Checks 730 to 737 (T0 wrote them) |
| model-credentials-routing | 21/86 | in a run: RT3, PL1, PL7, CS1, CS11, GV1 and PR4; open drafts: CS9 #3618, MG1 #3614, MG3 #3612 and PR8 #3656; all tasks approved 2026-11-21, launching by plan edges (docs/model-credentials-routing); supersedes credentials-belong-to-models and carries every not-merged task of it |
| required-version-stamp | 2/12 | ten (T2a, T2b, T3 to T8, K1 and K2) not started or waiting on edges, no pull request open, not re-read; all tasks approved 2026-11-23, launching by plan edges (docs/required-version-stamp); lift L1 of the repo-coupling audit, with L8 waiting on it; D3260–D3271 answered 2026-11-23 (D3261 not the recommendation); owner-run Check 900 after the build |
| workspace-credentials | 8/18 | the owner's answers to WC0's review are in the plan (2026-11-22: a claim deadline for a workspace slip, and a grant bound to the members the person launches for, which reopens D3145), so every personal task, WC2 to WC14 and I1, is launchable by its plan edges; open: WC2 #3653 (not a draft); all tasks approved 2026-11-21 (docs/workspace-credentials); plans what workspace-service and keys-through-service left: sharing a provider key at the workspace (WC1) and a person's own Claude token at the workspace (WC0, WC2–WC14, I1); D2960–D2979 answered 2026-11-19 (D2975 the owner's own answer) and D3140–D3148 answered 2026-11-21, all as recommended (D3141: a personal token follows the route, so a project's own model credential shadows it); WC13 waits on workspace-service WS21; owner-run Checks 850 and 851 |

## To do or parked

| Spec | State |
| --- | --- |
| repo-image-removal | parked by the owner: 2/11 merged; R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release, not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans, design | no plan or not a spec (plans holds v1's milestone plans M0 to M6, design holds v1 and update-in-flight) |

## Done

| Spec | Left |
| --- | --- |
| account-aliases | owner Check 550, not run; D2090–D2102 answered as recommended |
| accounts-add | owner checks LC1 to LC4 |
| adopt-firebase-root | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| agent-env | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| api-key-review | owner Check 330 (AK8); AK9 and AK10 folded into model-crafts MC6 (F26, D1638) |
| artifact-runs | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| base-image | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| binary-allow-rule | none (all approved 2026-11-15) |
| bitbucket-credentials | owner Check 149; 148's read half parked by the owner 2026-11-14 until the coordinator job is deployed (`fugaro update --firebase-project <id> --coordinator`) (Check 145 ran 2026-11-14: U3 shows the read token reaches past its workspace; the owner accepted the reach, D360 and D361 stand) (S2, D1961); T3.3's trust paths (S3 otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| bitbucket-token-types | owner Checks 590 and 591 (scripts on main), not run |
| bucket-iam | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| budget-token-channel | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| c39-review-identity | owner live check (Check 530; its script is on main and not yet run) |
| catalog-build-context | issue #1738 (milestone 0.26.0): owner Check 292 runs on the sandbox installation only |
| central-orchestrator | superseded by stateless-orchestrator |
| checkpoint-pushes | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| cli-output-console-graph | owner Checks 310 (with its `--scratch` mode), 311, 313 and 314 (all scripts on main), not yet run |
| cloud-senior-review | CSR17 and CSR18 moved to api-key-review |
| compatibility-matrix | owner CM4 (the read of the copy) |
| compute-backend | declared operational by the owner 2026-11-15; owner Checks 57b (its stranded-job submission fixed) and 124, not yet run (Task 13 was dropped) |
| config-management | none |
| config-ownership | none (22/22 merged); D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| config-transitions | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| console-coordinator-v2 | owner Check C0 (profiling) |
| console-dependency-graph | owner Check 123 (the graph in the packed, signed macOS app, GV2.6), not yet run |
| console-managed-screens | none (console 0.121.0) |
| console-settings-edit | none (console 0.120.0) |
| console-styling | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| console-windows | owner Check 119 |
| contract-first-decoupling | none |
| coordinator | P7 and P8 re-planned under mcp-server |
| credentials-belong-to-models | superseded by model-credentials-routing (2026-11-21): 5/38 merged; every not-merged task is withdrawn and carried by the new plan, and the owner halted any launch from this one |
| credentials-service | none, Done by the owner's ruling and not re-counted here (no agent task left; the owner-run Checks 91, 92, 94, 95, 96 and 99 are the owner's to run or record (Check 90 ran 2026-10-21), and Check 96 holds C2.3's flip, which waits on the deployed service minting a token) |
| desktop-console | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| desktop-console-v2 | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| diagnose-agent | owner Checks 410 to 412 |
| ee-reorg | owner E3.4 (Checks 200 and 201, scripts on main, not yet run); E4.1 deferred |
| failure-classes | owner Check 350 |
| feature-matrix | CR1 is not raised, so it is not counted; D2440–D2449 answered; no check taken; no after-deploy step |
| forge-labels | owner Check 640 passed on 2026-11-22 (U1 and U3 answered YES; #3649 is closed) — nothing of the plan is left |
| generic-tool | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| headroom-compression | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| how-to | not a spec |
| image-env | owner Check 291 (script on main, not yet run) |
| image-refresh | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| image-setup-skill | owner Checks 500 and 501 |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| issue-mine | owner Checks 470 to 472: all ran 2026-11-14 and none passed in full (470 in part, its issue-record read and two human steps unmeasured; 471 failed its cache-cleared row; 472 passed on an empty store, not at size), so re-runs are owner-run |
| issues-resolved-on-update | none |
| key-signing-service | superseded by licence-key-v2 and portal (2026-11-21) |
| lapse-read-only | superseded by licence-key-v2 and desktop-console rule 13 (2026-11-21) |
| layered-config | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| m10-multi-model | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| m11-setup-and-skills | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| m9-budget-and-dashboard | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| managed-agent-converge | none |
| managed-deployer-fence | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| managed-projects | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| mcp-server | M9 parked (D936, the fence declined, no release schedules it); M11 declined (2026-11-09) |
| mixed-credentials | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| mobile-auth | owner live checks, numbered Checks 620 to 627 on main (draft #2700's 601 to 608 was closed); P5's D231 gap is a follow-up awaiting the owner's decision — revoking the last device empties the node, so the next publish re-seeds the revoked entry |
| model-ab-testing | owner Checks 210 to 212 (written on main, not run; drafts #2634 and #2637 closed as superseded) |
| models-commands | none |
| named-oauth-tokens | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| no-prompt-policy | owner Checks 380 to 382 (scripts on main), not yet run |
| orchestrator-graph-estimates | none (39/39 merged); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); owner-run Checks 700–703 |
| outer-loop-escalation | owner Checks 540 to 542 (written as scripts) |
| plugin-user-scope | E1, the release itself (rides the next CLI release; no operator step) |
| post-review-triage | owner TR13 (Checks 490 to 492; scripts on main, not yet run) |
| project-centric | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| project-image-catalog | none |
| project-named-oauth-tokens | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| project-secrets | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| project-workflow-secrets | none |
| provider-price-catalog | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| recipes | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| registry-remote-repository | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| repo-coupling-audit | none of the plan (7/7 merged); D3237's rulings on B1 to B19, D3238's lift waves (amended) and D3239's rescope of L5 recorded 2026-11-23; the lift specs L1, L5 and L8 (wave 1) are next, none written, nothing of the lift implemented |
| retire-image-mirror | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| review-against-task | owner Check 750 after the release (its script is on main) |
| review-job | owner Checks 610 and 611 (scripts on main, #3150 and #3154), not run |
| rigor-command | none (R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, which win) |
| roles | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| roles-scopes | owner live check that a viewer cannot launch or cancel (not yet run) |
| run-limit-carry-over | owner-run live check (named, not numbered: a chain that halts at its dollar cap, continued with `--continues`, reports a smaller remaining than it started with) |
| run-without-checkout | no live check covers a launch from outside a checkout |
| security-classification | none |
| settings-commands | none |
| setup-wizard | owner Checks 27 (script on main) and 111 |
| shared-config | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| short-run-ids | owner SR13 (Check 440 as a script and Check 441 as the register's steps, on main, not yet run) |
| spend-accuracy | OR1 and Checks 650–651 (script written, #2971) owner-run; D2640–D2651 answered 2026-11-15 (D2643 not the recommendation; four amended after review) |
| stateless-orchestrator | owner Checks 301 to 305 (O16b; 301 deferred); the orchestrator is installed under launchd and replaces /jobmax, and its state is stale (goal not set) |
| task-governance | owner Checks 137 and 138 (scripts on main, #3087), not run |
| troubleshoot-cloud | owner Check 680 (not run); approved 2026-11-16 |
| troubleshoot-issue | none |
| upgrade | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| vision-model | owner Checks 520 and 521, not run; issue #1865 carries the 0.26.0 milestone |
| voice | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| watch-queued | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| website | W3.3 explainer video (parked), owner Check 69c (not written yet; 69a and 69b passed 2026-11-15) |
| website-changelog | WC4 lands with the next release |
| windows-cli | superseded by console-windows |
| workspaces | none |
