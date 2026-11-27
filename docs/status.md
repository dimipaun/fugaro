<!--
How this page is written

1. The **Merged** column holds only the count, tasks done out of tasks in the plan (`20/23`); "about" marks an approximate count (`about 35/45`), and `not counted` marks a plan whose tasks were never counted. Nothing else goes in that cell.
2. Every explanation of what remains goes in **Left**.
3. **Left** lists only the tasks still to do and the state of each (in a run, a draft PR #N, waiting on X, owner-run). Never finished work: merged tasks are counted in Merged and named nowhere else. A ruling on a still-open task may stay as a short clause on it.
4. **UAT** holds every spec whose agent work is complete: no live run, no open pull request, and no task an agent can launch or that waits on one; what is left is the owner's alone (owner-run checks, owner steps, owner decisions), and Left lists only those. Its test is the plan's remaining tasks, never the percentage, and when it is unclear whether an agent task remains the spec stays out of UAT. A spec with an agent task left sits in "Nearly There (NT)" at merged/total of 0.7 or more and in "Work in progress (WIP)" below 0.7; for an "about n/m" row, n/m decides. "Done" holds only specs with nothing left at all, owner work included.
5. A spec in the works (a task of it with a live run or an open pull request, draft or not, or a merged task) is in UAT, NT or WIP and never in "To do or parked", which holds only specs with nothing merged, no live run and no open pull request, plus the specs the owner parked by name (a parked spec keeps its merged count and is not worked until the owner unparks it).
6. A pull request is in one of five states, and the five words are the same ones everyone uses: **live** (a run is going on it, which is what rule 3's "in a run" means), **paused** (a run suspended itself on it and the orchestrator will resume it at a named time: it needs nothing from a person, is not landed and takes no follow-up while the pause stands), **ready** (no run is going, it is not a draft, and the last run that opened or followed it ended with the outcome `ready`, a review job's own outcome `reviewed` changing nothing), **stalled** (it had a run, no run is going, no pause stands and it is not ready — the outcome `draft`, a `[DRAFT]`-titled ordinary pull request on a host with no drafts included, or a ready one a person turned back into a draft) and **orphan** (no run ever produced or follows it). Landing is an action, not a sixth state: to **land** a pull request is to merge it as-is, with the findings it still carries recorded as follow-up tasks, rather than running another fix round (the `landing` skill).
7. Every table (UAT, NT, WIP, To do or parked and Done) sorts its specs alphabetically by name, ascending, case-insensitive; a row moved between tables takes its place there.
-->

# Fugaro: status of every spec under docs/

As of 2026-11-26 (US Eastern), read from `main` at `d06af43a0` (#4168), the pull requests merged since the previous read (`9f4cfc947`, #4092) and each plan's task list (about 30 folders' own status lines are stale). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links here. The sections run from UAT (the agents' work is complete, the owner's remains) through the nearest to done to the furthest, the specs with nothing left last. **The rows of the specs named under "Merged since the last read" were re-derived on 2026-11-26 from the merged pull requests' titles and each plan's task ids; every other row stands as of 2026-11-26 or earlier and was not re-read.**

**Released:** console 0.126.0 "Yuzu" (`console-v0.126.0` on `0445e140c`; published 2026-11-25 09:02 UTC; its website step is done, #3971), console 0.127.0 "Zante" (tagged `console-v0.127.0` on `e2b1d647f`, #3972, 2026-11-25; its release workflow was building when this was read, and its website step follows), CLI 0.28.2 (tagged `v0.28.2`, hotfix of 0.28.1: the per-run execution reads and the 429 retry; published 2026-11-23 22:00 UTC), CLI 0.28.1 (`v0.28.1`, published 2026-11-23 18:06 UTC), CLI 0.28.0 "Yak" (`v0.28.0` on the release commit `5975447c1`, #3683; published 2026-11-23 04:54 UTC; its two gates, #3534 managed-flag-ergonomics T0 and #3677 `repos add`, merged first), CLI 0.27.0 "Xerus" (`v0.27.0` on `1d268d850`, #3539, 2026-11-21; published 2026-11-22 04:08 UTC), console 0.125.0 "Watermelon" (`console-v0.125.0`, 2026-11-21; published 2026-11-22 00:29 UTC), CLI 0.26.1 (hotfix of 0.26.0, `v0.26.1`, 2026-11-18), CLI 0.26.0 and console 0.124.0 "Ugli" (`console-v0.124.0`, 2026-11-20). Every "after the 0.27.0 tag" and "after the 0.28.0 tag" gate below is open; each installation still needs the new fugaro (`brew upgrade`) and `fugaro update` before a check that depends on it can pass. The owner ruled on 2026-11-21 that 0.27.0 was cut without the pre-tag image rehearsal, which stays owed: `fugaro-sandbox` is an empty project and needs its billing link, `fugaro setup`, a local config and a sandbox repository before `scripts/release-rehearsal.sh run` can run. Composite-commands AP7 (#3517) and AP9 (#3578) shipped in 0.28.0; AP10 merged after it (#3637). CLI 0.29.0 "Zander" and its hotfixes 0.29.1 to 0.29.4 are tagged (the pre-tag rehearsal stays owed, and its image build is refused in an agent's session: run `scripts/release-rehearsal.sh` in the owner's own terminal). **CLI 0.29.5** is tagged (`v0.29.5` on `046db8046`, 2026-11-26, a hotfix with no rehearsal) and verified: every workflow the tag started passed and the `history`, `credentials` and `base` images pull without a login. It carries the credentials-service invoker grant for every shared job (#4007, #4035), the lifted-keys stage with `update --ask` (#4022) and the CLI-reference regeneration (#4061); the owner runs `brew upgrade dimipaun/tap/fugaro` and `fugaro update`, then reruns Checks 580, 670 and 521, which failed on the missing grant. The owner released at the green tip over repo-override-rulings' release fence (2026-11-25), so half of release A ships in 0.29.0. The next CLI release is 0.30.0 "Zebra", which completes release A; release B is 0.31.0. RE6's Check 670 is owner-run after the release and is not a gate; the plans of RE6 and T-TC10 read Checks 670 and 680, and both scripts are on main (#3524, #3531).

**CI:** the run at this read's tip (`d06af43a0`, 45911985646) is queued, so main's tip has no full run of its own; the last completed run (`a067bd0fa`, 45909635215) failed and a fix is going, and the three before it passed (`b98a80348`, `a8b113ef1`, `bcf262e7a`). The reds of the night were fixed in turn (#4015, #4060, #4061, #4064–#4068, #4071, #4083, #4084, #4086–#4089); the flakes seen were `check292_test.sh` and a `fuses.test.ts` checksum test. **The owner's rulings of 2026-11-26:** Check 580's gate is lifted (M2 landed, per-repository jobs are retired and any fault is fixed forward); the Check 880 task stays deferred; release A ships with the Claude-through-a-provider refusal in place and Check 50 flips it; a security finding may be deferred to a follow-up on a landed pull request, but no release is cut before that follow-up merges (none is deferred now).

**Owner-run checks:** 96, 620 and 195 are recorded (2026-11-25/26; 195's `notoken` finding stands), as are 890 and 900 on 0.29.5 (#4166); 580 failed at `run` on the missing invoker grant and waits on 0.29.5, as do 670 and 521 (its wait loop's fix, reading an ended run's status, is merged, #4164); 830 failed on 2026-11-25 (`probe-ran`, `u1-handmade`) and its fixed script is on main (#4160, and 840's absence read, #4167), rerun is the owner's; 50 runs the morning of 2026-11-26.

**Open pull requests:** 19 from `gh pr list`, all drafts: image-map-by-provider S3 (#4182), D1a (#4181) and D1b (#4190); model-credentials-routing PL9 (#4191); repo-override-rulings S2 (#4189), D1 (#4185) and RT2 (#4183); image-secrets IS7 (#4187) and IS11 (#4192); forge-authorship-images C8c (#4188); launcher-does-not-mint LM14 (#4178); mobile-identity-pairing M3 (#4184); mobile P5.4.1 (#4180); update-from-anywhere U7a (#4179) and U9b (#4186); managed-flag-ergonomics T3 (#4045); and three without a plan task (the `check631.sh` prompt fix #4101, the doctor-warnings check #4110 and the update gate #4170).

Merged since the last read (60 commits on `main` after #4092): image-map-by-provider T0c, U1a, U1b, R1, R2a, R2b, R2c, R2d, R3 and S2; model-credentials-routing PL5, PL6, MG2, PR2a, PR2b, PR13 and CS15; repo-override-rulings CK3, RT1 and RT3; rigor-modes T0.3 and LV5.2; image-secrets IS6; update-from-anywhere U3b and U10; workspace-credentials WC12; launcher-does-not-mint LM21; keys-through-service KS20; forge-authorship-images C8b; mobile P5.2.7 and the split of P5.4 into nine tasks; mobile-identity-pairing M1; review-levels-ab-jobmax X1; vision-model's V7 narrowing (#4174); and, outside any plan, the `accounts switch` write refusal with the job retirement (#4109), the update's skipped-half notice (#4108), the unmanaged-project refusals of the image, workspace and publish commands (#3952), the doctor fixes (#4115, #4116), the lint-autofix hand-back (#4105), the shared job's policy routes (#4158), the live-check scripts and fixes (#4125, #4150, #4160, #4161, #4162, #4164, #4167, #4173) and the owner's records (#4166). Rows whose count is still approximate are marked "about" and say why: subscriptions, review-levels-ab-jobmax, rigor-modes, model-credentials-routing and mobile-identity-pairing; credentials-service is Done by the owner's ruling.

## UAT

| Spec | Merged | Left |
| --- | --- | --- |
| account-aliases | not counted | owner Check 550, not run; D2090–D2102 answered as recommended |
| accounts-add | not counted | owner checks LC1 to LC4 |
| always-a-budget | 19/19 | 19/19 merged (AB9 as the halt commit #3843, its test follow-up #3865); owner-run Checks 790 and 791; I1 (#4040) shipped after AB7, so it rides the next release (the plan's release fence was crossed in 0.29.0); budget mode off is removed (D3009, the owner's own); D3020–D3028 answered 2026-11-20 |
| api-key-review | not counted | owner Check 330 (AK8); AK9 and AK10 folded into model-crafts MC6 (F26, D1638) |
| bitbucket-credentials | not counted | owner Check 149; 148's read half parked by the owner 2026-11-14 until the coordinator job is deployed (`fugaro update --firebase-project <id> --coordinator`) (Check 145 ran 2026-11-14: U3 shows the read token reaches past its workspace; the owner accepted the reach, D360 and D361 stand) (S2, D1961); T3.3's trust paths (S3 otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| bitbucket-token-types | not counted | owner Checks 590 and 591 (scripts on main), not run |
| bitbucket-workspace-token | 11/12 | T1.4 (owner-run: Checks 180 and 181, neither run; both scripts are on main); no agent task left; T2.9 closed as replaced by the service's rules (D1981), not counted in the 12 |
| c39-review-identity | not counted | owner live check (Check 530; its script is on main and not yet run) |
| catalog-build-context | not counted | issue #1738 (milestone 0.26.0): owner Check 292 runs on the sandbox installation only |
| ci-affected-tests | 11/12 | CS9 (owner-run Check 800, runnable); no agent task left, no pull request open; all approved 2026-11-20 (docs/ci-affected-tests); D3040–D3043 answered 2026-11-20, all as recommended |
| cli-output-console-graph | not counted | owner Checks 310 (with its `--scratch` mode), 311, 313 and 314 (all scripts on main), not yet run |
| compatibility-matrix | not counted | owner CM4 (the read of the copy) |
| compute-backend | not counted | declared operational by the owner 2026-11-15; owner Checks 57b (its stranded-job submission fixed) and 124, not yet run (Task 13 was dropped) |
| console-coordinator-v2 | not counted | owner Check C0 (profiling) |
| console-dependency-graph | not counted | owner Check 123 (the graph in the packed, signed macOS app, GV2.6), not yet run |
| console-fixes | 34/37 | no agent task left; owner Checks 870 and 871 after the build; D3200–D3214 answered 2026-11-21, and of the resolutions D3215–D3224 the amended D3215, D3217, D3218, D3220, D3223, D3224, tension T1 and the reindex IAM question still wait on the owner (O3, O4, O6, O7) |
| console-improvements | 46/46 | no agent task left; owner-run Checks 710 (re-record the Live and Graph goldens), 711 (read timings) and 712 (the wizard on a scratch project); all six slices chosen and all tasks approved |
| console-windows | not counted | owner Check 119 |
| credentials-service | not counted | none, Done by the owner's ruling and not re-counted here (no agent task left; the owner-run Checks 91, 92, 94, 95, 96 and 99 are the owner's to run or record (Check 90 ran 2026-10-21), and Check 96 holds C2.3's flip, which waits on the deployed service minting a token) |
| diagnose-agent | not counted | owner Checks 410 to 412 |
| failure-classes | not counted | owner Check 350 |
| go-to-market | 11/12 | owner G2.5 (the inputs, the preview deploy and Checks 160 and 161) and owner Check 162; no agent task left |
| image-env | not counted | owner Check 291 (script on main, not yet run) |
| image-setup-skill | not counted | owner Checks 500 and 501 |
| issue-mine | not counted | owner Checks 470 to 472: all ran 2026-11-14 and none passed in full (470 in part, its issue-record read and two human steps unmeasured; 471 failed its cache-cleared row; 472 passed on an empty store, not at size), so re-runs are owner-run |
| licence-key-v2 | 22/24 | owner-run K0.1 (counsel and the privacy text: the packet is drafted for the owner to send), K0.2 (the production signing keys, the same step as subscriptions S3.8 and portal P0.4: the KMS key rings and one enabled ED25519 key each exist in `fugaro-prod` and `fugaro-staging` since 2026-11-22, so the Terraform half is done and the services half of P0.4 is not applied) and K5.2 (the Paddle account); K2.6 and K5.1 superseded by the portal, K6.1 dropped 2026-11-16, none of the three counted in the 24; owner Checks 560–564 not run; D2130–D2146 answered 2026-11-11 except D2145 (default); D2134 (amended by TN2) and D2144 not the recommendation |
| location-preference | 18/18 | none (18/18 merged); owner-run Checks 820 and 821; the owner redeploys the key service with LP15; D3080–D3097 answered 2026-11-20 |
| mobile-auth | not counted | owner live checks, numbered Checks 620 to 627 on main (draft #2700's 601 to 608 was closed); P5's D231 gap is a follow-up awaiting the owner's decision — revoking the last device empties the node, so the next publish re-seeds the revoked entry |
| model-ab-testing | not counted | owner Checks 210 to 212 (written on main, not run; drafts #2634 and #2637 closed as superseded) |
| model-crafts | not counted | owner Checks 628 to 632 (scripts on main) and 420 to 422 (420 and 421 ran 2026-11-15 with the premise not met, which an installation updated to 0.27.0 now meets; 422 not run) |
| no-prompt-policy | not counted | owner Checks 380 to 382 (scripts on main), not yet run |
| orchestrator-graph-estimates | 39/39 | none (39/39 merged); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); owner-run Checks 700–703 |
| periodic-reviews | 16/17 | R5 (owner-run); owner Check 136; owner Checks 341, 342 and 343 written, not run |
| portal | 36/42 | owner P0.1 to P0.6, in the order P0.1 and P0.4, then P0.2, then P0.5 and P0.6 (P0.4 is the signing-keys step subscriptions S2.9 waits on: the key rings and the key exist in both projects since 2026-11-22, so its Terraform half is done and its services half is not applied; the staging Hosting site is `fugaro-portal-staging`, which the owner created and #3663 reads; the scripts of Checks 570 to 575 are on main, #3432); no agent task left |
| post-review-triage | not counted | owner TR13 (Checks 490 to 492; scripts on main, not yet run) |
| repos-management | 7/7 | 7/7 merged; the owner runs Check 670 after `fugaro update` on 0.29.5; D2400–D2417 answered 2026-11-15; no agent task left |
| required-version-stamp | 12/12 | 12/12 merged; no agent task left; owner-run Check 900 after the build; lift L1 of the repo-coupling audit, with L8 waiting on it; D3260–D3271 answered 2026-11-23 (D3261 not the recommendation) |
| review-against-task | not counted | owner Check 750 after the release (its script is on main) |
| review-job | not counted | owner Checks 610 and 611 (scripts on main, #3150 and #3154), not run |
| review-levels-ab-jobmax | about 31/34 | no agent task left; owner T3.2 (the store privacy forms, after T3.1) and T4 (Terms §7.3, with counsel), and owner-run Checks 270 (a Critical run whose L3 rejects once), 271 (a scrubbed console crash report) and 272 (the app's crash reports before pairing), all written and not run — 272's paired half is a written follow-up waiting on mobile-identity-pairing M2 and M3; U4's miss rate stays `unreliable` until model-ab-testing's Check 211 runs; J1.2, J1.3 and T1.1 to T1.3 closed as superseded, not counted in the 34; the count is about because L5.1 and T0.1 have no commit under their own ids and are counted on content |
| roles-scopes | not counted | owner live check that a viewer cannot launch or cancel (not yet run) |
| run-limit-carry-over | not counted | owner-run live check (named, not numbered: a chain that halts at its dollar cap, continued with `--continues`, reports a smaller remaining than it started with) |
| run-messages | 10/11 | RM8 (owner-run: Check 740; 0.27.0 is tagged, so it is runnable once the installation has fugaro 0.27.0 and `fugaro update`); no agent task left; D2940–D2947 answered 2026-11-18, all as recommended |
| setup-wizard | not counted | owner Checks 27 (script on main) and 111 |
| shared-jobs-default | 32/32 | 32/32 merged; owner Checks 580, 581 and 582 only (the owner lifted the Check 580 gate on 2026-11-26: M2, #4059, merged first); no agent task left |
| short-run-ids | not counted | owner SR13 (Check 440 as a script and Check 441 as the register's steps, on main, not yet run) |
| spend-accuracy | not counted | OR1 and Checks 650–651 (script written, #2971) owner-run; D2640–D2651 answered 2026-11-15 (D2643 not the recommendation; four amended after review) |
| stateless-orchestrator | not counted | owner Checks 301 to 305 (O16b; 301 deferred); the orchestrator is installed under launchd and replaces /jobmax, and its state is stale (goal not set) |
| subscriptions | about 41/50 | no agent task open (S2.9, the production public key in the three verifiers, and S8.3, the copyright commit to the licensing entity, are merged); S0.7 (what remains of it is the app's first release, the owner's); S1.7 and S1.9 closed (superseded by ee-reorg E1.4; the key-secret mount deferred by D524 and replaced by D2137); owner-run: S1.10 (Check 178, deferred, and its premise of a mounted key no longer holds), S3.7 and S3.8 (now the portal's P0.4 to P0.6 and licence-key-v2's K0.2), S6.4, S7.4, S8.1, S8.2, S8.5 and S8.6; the count is about because the 50 predates the closures |
| task-governance | not counted | owner Checks 137 and 138 (scripts on main, #3087), not run |
| troubleshoot-cloud | not counted | owner Check 680 (not run); approved 2026-11-16 |
| vision-model | not counted | no agent task left; owner Checks 520 and 521, not run (both still measure what they were written to measure, and the ruling's own state is the reserved Check 522, an owner step); issue #1865 carries the 0.26.0 milestone; the owner ruled 2026-11-26 (D2017) that the slot serves only the runs that read an image, and that narrowing is merged (#4174) |
| workspace-credentials | 18/18 | no agent task left; owner-run Checks 850 and 851; all tasks approved 2026-11-21 (docs/workspace-credentials); the owner's answers to WC0's review are in the plan (2026-11-22); D2960–D2979 answered 2026-11-19 (D2975 the owner's own answer) and D3140–D3148 answered 2026-11-21, all as recommended (D3141: a personal token follows the route, so a project's own model credential shadows it) |
| workspace-projects-users | 26/26 | none (26/26 merged); owner-run Checks 720 and 721 (scripts on main) |
| workspace-service | 25/25 | none (25/25 merged); owner-run Checks 770–773 once the installation has fugaro 0.27.0 and `fugaro update`; D2980–D2992 answered (D2984 not the recommendation; D2987 amended after review) |

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| forge-authorship-images | 21/23 | C8c is a draft (#4188: the shared-image commands `image ls`, `image prune` and the image-count read; `checkout: shared` stays refused), unparked by the owner 2026-11-26 (D650 amended); owner-run A5 (Check 230, text only, not run); owner Checks 232, 234 and 235 text only, not run; B2 closed by the owner 2026-11-21 as superseded by the orchestrator's E8, not counted in the 23; the plan's State lines date from 2026-11-10; the image-setup overlap is C6 and C8's C8c — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| image-map-by-provider | 14/19 | S3 (#4182), D1a (#4181) and D1b (#4190) are drafts; U1c and X1 wait on edges (U1c after a later step retires the node's `image_map`); all tasks approved 2026-11-25 (docs/image-map-by-provider); owner-run Check 940 after the build |
| launcher-does-not-mint | 21/26 | LM14 is a draft (#4178: the update stage that moves the signing grant off the launchers); LM15 waits on LM14, LM16 on LM14 and LM21, LM17 on LM14 and LM21, and LM18 on LM14 and one released version; approved 2026-11-20 (docs/launcher-does-not-mint); the credentials service issues a run's identity as a one-time ticket the run claims, and `fugaro update` takes the signing grant off launchers, operators and the resident orchestrator once the service answers; D3120–D3137 answered 2026-11-20, all as recommended (D3129 holds everything in batch-run-identity that touches its key or its C1 until LM19; D3130 enforces the launch scope at the issuer; D3131 accepts that executions queued and not started halt at the update); owner-run Checks 840 and 841 |
| licensing | 23/24 | owner-run L0.2 (confirm the CLA bot's allow-list); the next Terms version is a redline with counsel; the folder's README still says the Terms page is unpublished |
| managed-flag-ergonomics | 9/12 | T3 is a draft (#4045, a follow-up run on the converge rigs); T2c and T5 not started or waiting on edges; D2211-D2219 and D3160 answered (only managed projects: every writing command stops on an unmanaged one); docs/managed-flag-ergonomics |
| mobile | 64/75 | no agent task left unscheduled: P5.4 was split into nine tasks, P5.4.1 to P5.4.9, and approved 2026-11-26, and P5.4.1 is a draft (#4180, the front agent's design of record) whose placement and credential are the owner's questions 1 and 2; the owner answered the push design's Q1, Q3 to Q8 and Q10 on 2026-11-21 (the plan carries each answer); owner P2.3 and P4.4. Not counted in the 75: the voice-session plan (6/26), which has no row here and waits on D207 and the approval of VS2 |
| repo-override-rulings | 35/50 | CK3, RT1 and RT3 merged since the last read; S2 (#4189), D1 (#4185) and RT2 (#4183) are drafts; the rest not started or waiting on edges (release A, then release B); CLI 0.29.0 was cut at the green tip over the plan's release fence by the owner's word (2026-11-25), so half of release A ships there: release A completes in 0.30.0 and release B is 0.31.0; D3237 and D3238 recorded 2026-11-23 (docs/repo-override-rulings) |
| rigor-modes | about 46/48 | the 2026-10-20 plan: T7.3 and T8.2 (owner-run: this repository's own rigor block, and Check 127, written); T2.4, T4.7, T5.3, T5.4, T5.5 and T6.3 closed 2026-11-18 as superseded by the Hotfix rulings, not counted. The overlay: LV5.1 and LV5.2 merged (LV5.1's harness fixes #4071 and #4084); the owner ruled 2026-11-26 that a lint-autofix run that skips implement still writes its hand-back (#4105); the count is 32 of the plan's 34 live tasks plus 14 of the overlay's 14 |
| secrets-brokering | 51/65 | no code task left in the plan: the 14 open are 9 owner check runs and 5 record commits waiting on the owner's pasted output. 0.27.0 is tagged (2026-11-21), so every check is runnable once the installation has fugaro 0.27.0 and `fugaro update`: B4.2 (Check 195), B5.6 (Check 197), B2.6 and B2.8 (Check 193), B3.7 and B3.9 (Check 194), B4.8 and B4.10 (Check 196) and B5.8 (Check 197's --after-flip half); records B1.16, B2.9, B3.10, B4.11 and B5.9 follow their checks; every script is on main and none of Checks 193 to 197 has run; the dynamic-delivery subfolder (22/22) is not in the 65 and has nothing left; the image-secrets subfolder (docs/secrets-brokering/image-secrets, #3787) is not in the 65 either: 13/15 merged (C1, I1, IS1 to IS5, IS8, IS9, IS10, IS12, IS13 and the flip, IS6), with IS7 (#4187) and IS11 (#4192) as drafts |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| batch-run-identity | 17/27 | BR1, BR16 and BR17 merged since the last read (BR2, BR4, BR6 and BR7 before it); C1, BRS, BR0 and BR6 approved 2026-11-20; D3100–D3112 answered 2026-11-20 (D3110 and D3111 not the recommendation, D3110 amended after review; D3104 supersedes D2075); BR3, BR5 and BR12 wait on Check 830 (run 2026-11-25: FAIL on `probe-ran` and `u1-handmade`; the link is the instance's own object, read with `compute.instances.get`, which cannot be scoped to Fugaro's VMs) and on the owner's choice under O1; owner-run Checks 830 and 831 |
| keys-through-service | 10/17 | all approved 2026-11-20; the Claude-through-a-provider refusal ships in release A (KS20 merged, and its doctor follow-up #4172); Check 50 flips it, owner-run; 7 more not started or waiting on edges (KS11, KS13 to KS16, KS18 and I1); KS6, KS9 and KS10 withdrawn from the plan (2026-11-20, replaced by model-credentials-routing), not counted in the 17; D3000–D3015 answered 2026-11-19 and 2026-11-20 (D3005 and D3013 not the recommendation, D3007 and D3009 the owner's own; D3009 removes budget mode off under its own spec, not written yet); two releases; KS14 and KS15 wait on that spec's plan, KS16 on secrets-brokering B5.7; owner-run Checks 50, 780 and 781 |
| mobile-identity-pairing | about 31/45 | M3 is a draft (#4184); the rest of the app half (M2, K3, J1, J5c, X3), P8, Z2 and Z3 (unblocked by Z0's result), I1, I2, D1 and UP1 not started or waiting on edges (the count is about: the plan's task ids matched 26 of 41 by title on 2026-11-26 and the earlier 30 of 45 was not re-read, and M1 merged since); owner-run M0 and Checks 730 to 737 (scripts on main) |
| model-credentials-routing | about 59/86 | PL5, PL6, MG2, PR13, CS15 and PR2 (as PR2a, #4152, and PR2b, #4176) merged since the last read (PL2: the owner ruled 2026-11-26 that `accounts add` writes a lone account, D3198); PL9 is a draft (#4191); the rest not started or waiting on edges; all tasks approved 2026-11-21, launching by plan edges (docs/model-credentials-routing); the Check 880 task is deferred by the owner (2026-11-26); supersedes credentials-belong-to-models and carries every not-merged task of it; the count is about because PL2b merged outside the plan's list (#3984) |
| update-from-anywhere | 14/22 | U3b and U10 merged since the last read; U7a (#4179) and U9b (#4186) are drafts; U9c, U9d, U11, U12, I1 and K1 wait on edges; spec, reconciliation and plan merged (#3852); D3340–D3352 answered 2026-11-23 (docs/update-from-anywhere) |

## To do or parked

| Spec | State |
| --- | --- |
| gitlab-support | parked by the owner: 0 merged; not built until a named need (a GitLab customer or repository), and then only after its own reconciliation of tokens and `Draft:` semantics (D610); no plan, no reconciliation written, nothing approved |
| repo-image-removal | parked by the owner: 2/11 merged; R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release, not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans, design | no plan or not a spec (plans holds v1's milestone plans M0 to M6, design holds v1 and update-in-flight) |

## Done

| Spec | Left |
| --- | --- |
| adopt-firebase-root | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| agent-env | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| artifact-runs | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| base-image | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| binary-allow-rule | none (all approved 2026-11-15) |
| bucket-iam | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| budget-token-channel | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| central-orchestrator | superseded by stateless-orchestrator |
| checkpoint-pushes | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| cloud-senior-review | CSR17 and CSR18 moved to api-key-review |
| composite-commands | owner Checks 390 to 392 (scripts on main; 390 failed on 2026-11-24 in a catalog build: a GitHub Dockerfile entry has no git credential, fix in run 7129); no-prompt-policy is Done and blocks nothing here |
| config-management | none |
| config-ownership | none (22/22 merged); D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| config-transitions | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| console-managed-screens | none (console 0.121.0) |
| console-settings-edit | none (console 0.120.0) |
| console-styling | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| contract-first-decoupling | none |
| coordinator | P7 and P8 re-planned under mcp-server |
| credentials-belong-to-models | superseded by model-credentials-routing (2026-11-21): 5/38 merged; every not-merged task is withdrawn and carried by the new plan, and the owner halted any launch from this one |
| desktop-console | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| desktop-console-v2 | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| ee-reorg | owner E3.4 (Checks 200 and 201, scripts on main, not yet run); E4.1 deferred |
| feature-matrix | CR1 is not raised, so it is not counted; D2440–D2449 answered; no check taken; no after-deploy step |
| forge-labels | owner Check 640 passed on 2026-11-22 (U1 and U3 answered YES; #3649 is closed) — nothing of the plan is left |
| generic-tool | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| headroom-compression | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| how-to | not a spec |
| image-refresh | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
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
| models-commands | none |
| named-oauth-tokens | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| outer-loop-escalation | built (E0 to E13 and E16 merged) but not working yet: the orchestrator daemon is not running (no loaded launchd job; its state was last written 2026-11-14) and the runner writes no `findings.json`, so every triage reads `findings_absent` and only recommends until the writer lands (run a8f5, draft #3970); owner Checks 540 to 542 not run: 540 waits on the writer, 542 is runnable once the App's merge permission is granted on a sandbox repository and prints which pieces of the daemon's identity are missing |
| plugin-user-scope | E1, the release itself (rides the next CLI release; no operator step) |
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
| rigor-command | none (R2 to R5 closed 2026-11-10 as duplicates of settings-commands and config-management, which win) |
| roles | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| run-without-checkout | no live check covers a launch from outside a checkout |
| runs-ls-speed | none (8/8 merged); Check 760 passed 2026-11-22; D2960–D2967 taken as recommendations |
| security-classification | none |
| settings-commands | none |
| shared-config | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| troubleshoot-issue | none |
| upgrade | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| voice | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| watch-queued | none recorded: a pre-layout spec, moved from docs/design and docs/plans on 2026-11-21, not re-counted |
| website | W3.3 explainer video (parked), owner Check 69c (not written yet; 69a and 69b passed 2026-11-15) |
| website-changelog | WC4 lands with the next release |
| windows-cli | superseded by console-windows |
| workspaces | none |
