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

As of 2026-11-21 20:55 UTC (15:55 US Eastern), read from `main` at `bb81953bf` (#3510), the open pull requests and each plan's task list (the plans' own status lines are stale in about 30 folders). "About" marks an approximate count; **owner-run** is a live check or step only the owner performs. Each folder's README links to this page. The sections run from the nearest to done to the furthest; the finished specs are at the end.

**Released:** CLI 0.26.1 (hotfix of 0.26.0, tagged `v0.26.1`, 2026-11-18) and CLI 0.26.0; console 0.124.0 "Ugli" (tagged `console-v0.124.0`, 2026-11-20). **Being cut at this read, no tag yet:** console 0.125.0 "Watermelon" (notes merged as #3465; the Windows dry run and `console-tests` are green on `4c0887c8f` and `bb81953bf`, and the tag waits on a green `ci` run on the commit it names) and CLI 0.27.0 "Xerus" (notes merged as #3464 with the price catalog regenerated on 2026-11-21; the owner ruled on 2026-11-21 that it is cut without the pre-tag image rehearsal, which stays owed: `fugaro-sandbox` exists as an empty project and needs its billing link, `fugaro setup`, a local config and a sandbox repository before `scripts/release-rehearsal.sh run` can run). The gate of 0.27.0 (secrets-brokering, graph-estimates with the orchestrator, config-ownership with composite-commands, troubleshoot-cloud, periodic-reviews and model-crafts) has merged. RE6 and Check 192 are owner-run after the release and are not a gate; RE6 and T-TC10 still read Check 620 until the renumber pull request (to Checks 670 and 680) merges.

**CI:** the last completed dispatched `ci` run on main is 44946149851, green, on `cfeed1b1c` (#3508); main was red four times between 02:45 and 15:00 UTC (the read map, the docs layout move, the CLI reference, and the pins that read the price catalog: #3466, #3468 to #3478, #3507 and #3508 made it build again). Two commits since touch console tests only (#3509 and #3510); `ci` run 44947741960 on `4c0887c8f` was still in progress at this read, and the tip `bb81953bf` has a green `console-tests` run (44948420326) and no `ci` run of its own yet.

**Open pull requests:** 39, read from `gh pr list` at 20:53 UTC (37 drafts, 2 not: #3420 and #3487). Live runs, read from `fugaro runs ls --state running` at 20:53 UTC (20 runs, each on its own pull request): #3504 (workspace-credentials WC1), #3501 (location-preference LP6), #3481 and #3483 (always-a-budget AB1b, AB7), #3488, #3489, #3491, #3492, #3493, #3495, #3497, #3498, #3500 and #3505 (launcher-does-not-mint LM10, LM22, LM23, LM24, LM11, LM7, LM4, LM8, LM3, LM2), #3490 and #3502 (console-improvements MW1, PL1), and the follow-up runs #3410 (workspace-service WS8), #3416 (workspace-projects-users WU15), #3247 (mobile-identity-pairing P2, relaunched on its branch) and #3436 (model-crafts MC26). Launched on 2026-11-21 and not in that read (finished, or not yet listed; the state of each was not re-read, so counts treat them as open): the follow-ups on #3420 (location-preference LP2) and #3452, workspace-credentials C1, C2 and WC0 (#3474), always-a-budget AB11, keys-through-service KS12, batch-run-identity BR1, BR2, BR4, BR14 and BR19, launcher-does-not-mint LM1 and LM5, console-improvements I-MG, credentials-belong-to-models C1 (#3460) and the mobile plan's P5.1g, P5.1h, P5.2.6 and P5.2.8. Numbers move while landing and launch runs go on: this is one read.

Merged since the last read (86 commits, read from `4ce11172f`; 2 are specs' own documents, listed first): the folding of the pre-layout specs into docs/<spec>/ (#3469) and the spec, reconciliation and plan of workspace-credentials (#3472); workspace-service WS7, WS9, WS11, WS12, WS13, WS14 and WS20; workspace-projects-users WU4, WU5, WU6, WU7, WU8, WU11, WU12, WU13, WU23 and WU24; keys-through-service KS2, KS4, KS5 and KS19; always-a-budget C1, AB1a, AB8a, AB10 and AB14; batch-run-identity C1, BR0, BR8, BR9, BR10 and BR11; launcher-does-not-mint LM0 and LM19 (LM19's document is written under batch-run-identity); ci-affected-tests CS2b, CS4, CS5 and CS7b; location-preference LP4 and LP12; console-improvements AL, MO-P, SM3, SRC and SW3b; credentials-belong-to-models CM1, CM3, CM22 and CM32; rigor-modes LV3.1 and LV4.2; orchestrator-graph-estimates R5 (the spec is complete); mobile P5.1b and P5.1c and mobile-identity-pairing P6; secrets-brokering B5.7 (the flip chain B2.7, B3.8 and B5.7 is done); licence-key-v2 K3.4; portal P9.4; the live-check scripts of Checks 740 (run-messages) and 760 (runs-ls-speed); the release notes of CLI 0.27.0 (#3464) and console 0.125.0 (#3465); the fix for `fugaro followups` (#3467), live only after the release and a `fugaro update`; and the fixes that made red main build again (#3458, #3466, #3468 to #3478, #3507 to #3510). Rows that could not be re-derived are marked "about" or say so: forge-authorship-images, licence-key-v2, subscriptions, review-levels-ab-jobmax, rigor-modes, secrets-brokering and mobile; the task ids of console-improvements were read from each pull request's own title; credentials-service is Done by the owner's ruling.

## Nearly There (NT)

| Spec | Merged | Left |
| --- | --- | --- |
| bitbucket-workspace-token | 10/12 | T1.4 and T2.7 (owner-run: Checks 180 and 181); T2.2 was carried by shared-jobs-default S1 and S4, both merged; T2.9 is closed as replaced by the service's rules, D1981, and is not counted in the 12 |
| composite-commands | 7/10 | AP7 (the audit of "now run X" texts: the secrets-brokering flips B2.7, B3.8 and B5.7 have all merged, so it is launchable with `--security`, not launched), AP9 and AP10, all not started; depends on no-prompt-policy |
| console-improvements | 34/46 | live: MW1 #3490 and PL1 #3502; I-MG launched 2026-11-21 (state not re-read); the other 9 not started or waiting on edges; all six slices chosen and all tasks approved by the owner; owner-run Checks 710 (re-record the Live and Graph goldens), 711 (read timings) and 712 (the wizard on a scratch project) |
| forge-authorship-images | about 20/25 | the tasks still open are not re-derived (the plan's own State lines date from 2026-11-10): owner-run A5 (Check 230) and the unscheduled C8b/C8c stay out; D650–D653 answered 2026-11-16 as recommended; the image-setup overlap is C6 and C8's C8b/C8c, not about 16 — [reconciliation-image-setup.md](forge-authorship-images/reconciliation-image-setup.md) |
| go-to-market | 11/12 | owner G2.5 |
| licence-key-v2 | about 21/26 | not reconciled against the plan in this sweep, so the count is about; K2.6 superseded by the portal; K6.1 dropped 2026-11-16 (the key is set only by `fugaro licence`, no wizard step, no-prompt policy); K4.2 merged as #2873, the rest of K0–K7 not started or open (K3.4 merged as #3447); D2130–D2146 answered 2026-11-11 except D2145 (default) (D2134 (amended by TN2) and D2144 not the recommendation), and all K* tasks approved 2026-11-13; TN1 seat enforcement is not in 0.24.0 (owner); owner Checks 560–564 not run |
| licensing | 21/23 | owner-run only: L0.2's allow-list and status, the Terms publication's L4.3, L5.1 T3(c); the next Terms version is a redline with counsel (merged as #2798) |
| mobile | about 60/67 | P5.1g, P5.1h, P5.2.6 and P5.2.8 (launched 2026-11-21, state not re-read), P5.2.7, P5.2.9 and P5.2.10 (approved 2026-11-14, not started), P5.4 (unscheduled); owner P2.3 and P4.4 |
| model-crafts | 25/26 | MC26 (stays owner-run; follow-up #3436 live: finish live checks 628 to 632) and the activation plan for the four known limits, all approved 2026-11-16; F27, D1660–D1671 and D1673 were all answered as recommended on 2026-11-17, and D1672 on 2026-11-15 (A, then B and C together in one release, then D), so no decision is open; owner Checks 420 to 422 (written as one script each; 420 and 421 ran 2026-11-15, premise not met and no run launched — both wait on the crafts, credentials and pools being published into the policy node, which the activation plan is to close; 422 not run) and the activation plan's own Checks |
| periodic-reviews | 16/17 | R5 (owner-run); owner Check 136; owner Check 340 ran a third time on 2026-11-13 and exited 1 (the reports job needs only slug-level listings); the revised Check is merged (#2524) and the owner runs it again |
| portal | 36/42 | owner P0.1 to P0.6; P9.4's live-check scripts (570 to 575) merged as #3432 |
| repos-management | 6/7 | RE6 (owner-run: its Check is renumbered to Check 670 by the owner's ruling, and the renumber pull request is not merged yet); #2690 was closed 2026-11-17 as superseded by RE1 to RE3 (#2932, #2935, #2939); all approved 2026-11-15 and D2400–D2417 answered the same day |
| review-against-task | 15/16 | 1 left, not started: RA7 (the recipes page and the release notes), waiting on edges; owner-run Check 750 after the release |
| review-levels-ab-jobmax | about 30/35 | U4 is reported unreliable until the owner runs Check 211; the orchestrator replaces `/jobmax` (2026-11-16): J1.2, J1.3 and T1.1 to T1.3 closed as superseded and not counted in the 35, T3.1 and V1.3 waiting on a re-plan against the orchestrator and the config-ownership registry; X1 waits on T3.1; the count is about because the remaining tasks of the slot are not all named here |
| rigor-modes | about 35/40 | T8.2 (owner-run: Check 127, written); the rest of the overlay plan LV3 to LV7 is unblocked in principle (LD2: the level wins): LV3.1 (#3394) and LV4.2 (#3332) merged, the others not started; the count is about because the overlay's tasks are not all counted |
| run-messages | 10/11 | RM8 is owner-run (Check 740, after the release; the script merged as #3433); D2940–D2947 answered 2026-11-18 as recommended |
| runs-ls-speed | 6/8 | RL4 (optional, built only if the default listing is still slow, not started) and RL6 (owner-run Check 760; the script merged as #3434); D2960–D2967 are taken as recommendations (docs/runs-ls-speed) |
| secrets-brokering | about 52/65 | B1.16; owner Check 192 (the --after-flip half; the flip itself merged as #2166 on 2026-11-17); the flip chain B2.7, B3.8, B4.9 and B5.7 is done (B5.7 merged as #3316), approved to launch one at a time with the owner checks moved to after-flip runs (owner, 2026-11-18); B2.6, B2.8 and B2.9; B3.7, B3.9 and B3.10; B4.2 (owner-run), B4.8, B4.10 and B4.11 (owner Check 195 gates only B4.9's flip); B5.6, B5.8 and B5.9 (all approved 2026-11-10; owner Checks 193–197); dynamic-delivery subfolder: owner DS0.3 (Checks 510 and 511); the count is the last sweep's 48 plus B4.6, B4.7, B4.9 and B5.7 (B2.7, B3.8 and B4.5 included) and is about because the plan's 65 does not reconcile with the 23 tasks named here |
| shared-jobs-default | 29/32 | 3 left: M2, which waits for owner Check 580 (the script is merged, #3121); M4 and M5 not started (M5 consumes M2); owner Checks 191, 192, 580, 581 and 582 |
| subscriptions | about 36/50 | a census of 2026-11-17 found 34 of 41 agent tasks done before S7.1 and S7.3 merged the same day (#3046, #3048), which makes 36, with the 10 owner-run (none run) in the 50; S1.7 is superseded and not counted. Open agent tasks: S0.7 (the app's first release, mobile-v0.200.0), S1.9 (waits on the enforcement release) and S2.9 (the production public key, which waits on owner S3.8), and about one more the census did not name; owner-run: S1.10 (Check 178, deferred), S3.7, S3.8, S6.4, S7.4 and S8.1–S8.6 (counsel closed S8.4 by the owner's ruling of 2026-11-17, recorded by #3042); TN1 seat enforcement is not in 0.24.0 (owner). The count rests on that census and was not re-derived task by task |
| troubleshoot-cloud | 11/12 | T-TC10 is owner-run (its Check is renumbered to Check 680 by the owner's ruling; the renumber pull request is not merged yet); approved 2026-11-16 |
| workspace-projects-users | 20/26 | all tasks approved; live: WU15 #3416 (follow-up: the state save's branch name never collides); the other 5 (WU10, WU16, WU17, WU21, I1) not started and launch by plan edges |
| workspace-service | 20/25 | all approved 2026-11-20 and launching by plan edges: live: WS8 follow-up #3410 (an upstream error answer settles at zero); the other 4 not started or waiting on edges; D2980–D2992 answered 2026-11-19 (D2984 not the recommendation; D2987 amended after review); owner-run Checks 770–773 after the release |

## Work in progress (WIP)

| Spec | Merged | Left |
| --- | --- | --- |
| always-a-budget | 6/19 | all approved 2026-11-20 and launching by plan edges: live: AB1b #3481 and AB7 #3483, AB11 launched 2026-11-21 (state not re-read); the other tasks not started or waiting on edges; budget mode off is removed (D3009, the owner's own; D3010 and D3011 in the keys plan); D3020–D3028 answered 2026-11-20 (D3020, D3022, D3023, D3025 and D3028 not the recommendation; D3026 the owner's own: no rule in terms of a repository's auth, which gets its own spec); AB7 waited on location-preference LP2 and LP4 (D3029, D3094); one release, with no release cut from main between AB7 and I1; owner-run Checks 790 and 791; the keys plan's KS14 and KS15 wait on its I1 |
| batch-run-identity | 7/27 | BR1, BR2, BR4, BR14 and BR19 launched 2026-11-21 once LM19 (#3444) merged (state not re-read); C1, BRS, BR0 and BR6 approved 2026-11-20; D3100–D3112 answered 2026-11-20 (D3110 and D3111 not the recommendation, D3110 amended after review; D3104 supersedes D2075; D3102's second read is decided after Check 830); BR3, BR5 and BR12 wait on the owner's run of Check 830 and his answer on O1; BR7 and BR12 start after always-a-budget's AB9 if it has an open pull request; owner-run Checks 830 and 831 |
| ci-affected-tests | 8/12 | left: CS6 (the workflows' copy-in, the owner's), CS6b and CS8 (behind CS6) and CS9 (owner-run Check 800); all approved 2026-11-20 (docs/ci-affected-tests): D3040–D3043 answered 2026-11-20 as recommended; CS6 is copied into the workflows only by the owner's word |
| credentials-belong-to-models | 4/38 | live: C1 (#3460); CM1 (#3459), CM3 (#3461), CM22 (#3463, Checks 810 and 811 as scripts) and CM32 (#3462) merged; all approved 2026-11-20, launching by plan edges (docs/credentials-belong-to-models); D3060–D3073 answered 2026-11-20, all as recommended, D3061 amended by the owner and D3064 after review (`agent.auth` is retired: a craft's access names the credential, an unset one is the first declared credential that reaches the model, and Vertex only where named (D3061, amended by the owner); D3065 retires the consent switch and D3072 sets the version rule aside for `fugaro.yaml`); two releases; it starts after always-a-budget's I1, keys-through-service's KS5, KS12 and KS19 (D3071) and replaces keys-through-service KS6, KS9 and KS10; release 2 waits on release 1 and on keys-through-service KS13; owner-run Checks 810 and 811 |
| keys-through-service | 8/20 | all approved 2026-11-20 and launching by plan edges: KS12 launched 2026-11-21 (state not re-read); the other 11 not started or waiting on edges (KS6, KS9 and KS10 are stopped, the owner 2026-11-20: credentials-belong-to-models replaces them); D3000–D3015 answered 2026-11-19 and 2026-11-20 (D3005 and D3013 not the recommendation, D3007 and D3009 the owner's own; D3009 removes budget mode off under its own spec, not written yet); two releases; release A waits on the owner's run of Check 50 (D3013); KS14 and KS15 wait on that spec's plan, KS16 on secrets-brokering B5.7; owner-run Checks 50, 780 and 781 |
| launcher-does-not-mint | 3/26 | live: LM2 #3505, LM3 #3500, LM4 #3497, LM7 #3495, LM8 #3498, LM10 #3488, LM11 #3493, LM22 #3489, LM23 #3491 and LM24 #3492; LM1 and LM5 launched 2026-11-21 (state not re-read); approved 2026-11-20 (docs/launcher-does-not-mint); the credentials service issues a run's identity as a one-time ticket the run claims, and `fugaro update` takes the signing grant off launchers, operators and the resident orchestrator once the service answers; D3120–D3137 answered 2026-11-20, all as recommended, D3129, D3130 and D3136 amended after review and answered again, D3125 amended by D3136 (D3121 amends the rule on the credentials account's grants; D3129 holds everything in batch-run-identity that touches its key or its C1 until LM19; D3130 enforces the launch scope at the issuer; D3131 accepts that executions queued and not started halt at the update); the other 11 wait on always-a-budget's I1 and keys-through-service's release A (D3128); owner-run Checks 840 and 841 |
| location-preference | 11/18 | live: LP6 #3501 and LP2's follow-up #3420 (the first run was cut short by an infrastructure error); all approved 2026-11-20 (docs/location-preference); the region stays the one setting and the place (US, EU) is derived from it; D3080–D3091 answered 2026-11-20, all as recommended, D3087 and D3088 amended after review and confirmed; D3092–D3097 answered the same day (D3097 not the recommendation: a fourth review round) (D3090 removes the Firestore location's own confirmation, reversing D12 of 2026-10-20 for it); always-a-budget's AB7 waits on its LP2 and LP4 (D3094); LP3, LP10 and LP14 wait on AB7 (live); the key service is redeployed with LP15 (owner); owner-run Checks 820 and 821 |
| mobile-identity-pairing | 11/45 | live: P2 #3247 (relaunched on its branch 2026-11-21, carrying its test file and defining `pairConsumerKeyPath`); the other tasks not started; owner-run M0 and Z0, and Checks 730 to 737 (T0 wrote them) |
| workspace-credentials | 0/18 | live: WC0 #3474 and WC1 #3504; C1 and C2 launched 2026-11-21 (state not re-read); all tasks approved 2026-11-21 (docs/workspace-credentials), WC2 to WC14 and I1 launch by plan edges (every personal task is behind the owner's answers to WC0's review); it plans what workspace-service and keys-through-service left: sharing a provider key at the workspace (WC1) and a person's own Claude token at the workspace (WC0, WC2–WC14, I1), D2960–D2979 answered 2026-11-19 (D2975 the owner's own answer) and D3140–D3148 answered 2026-11-21, all as recommended (D3141: a personal token follows the route, so a project's own model credential shadows it); WC1 waits on workspace-service WS19, WC5 on WS8, WC8 on WS18, WC13 on WS21, I1 on workspace-service's I1; owner-run Checks 850 and 851 |

## To do or parked

| Spec | State |
| --- | --- |
| key-signing-service | a design and threat model with no plan of its own (docs/key-signing-service, moved from docs/design on 2026-11-21); its own status line says the infrastructure and the issuing service are written and neither is applied; not re-derived against licence-key-v2 and portal |
| lapse-read-only | a design with no plan, not built (docs/lapse-read-only, moved from docs/design on 2026-11-21) |
| managed-flag-ergonomics | a design only, nothing built and no task approved (docs/managed-flag-ergonomics, moved from docs/design on 2026-11-21) |
| repo-image-removal | parked by the owner: 2/11 merged, R2 to R10 deferred by the image setup skill (D1900), not scheduled in any release and not cancelled; no run or pull request open |
| voice-module, collateral, backlog, plans, design | no plan or not a spec (plans holds v1's milestone plans M0 to M6, design holds v1 and update-in-flight) |

## Done

| Spec | Left |
| --- | --- |
| account-aliases | owner Check 550, not run; D2090–D2102 answered as recommended |
| accounts-add | owner checks LC1 to LC4 |
| adopt-firebase-root | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| agent-env | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| api-key-review | owner Check 330 (AK8); AK9 and AK10 are folded into model-crafts MC6 (F26, D1638) |
| artifact-runs | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| base-image | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| binary-allow-rule | none (AR7 merged as #3077 on 2026-11-17; all approved 2026-11-15) |
| bitbucket-credentials | owner Check 149; 148's read half parked by the owner 2026-11-14 until the coordinator job is deployed (`fugaro update --firebase-project <id> --coordinator`) (Check 145 ran 2026-11-14: U3 shows the read token reaches past its workspace; the owner accepted the reach, D360 and D361 stand) (S2, D1961); T3.3's trust paths (S3 is otherwise superseded by the secrets-brokering proxy chain, 2026-11-10) |
| bitbucket-token-types | owner Checks 590 and 591 (scripts on main), not run |
| bucket-iam | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| budget-token-channel | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| c39-review-identity | owner live check (Check 530; its script, merged as #2626, is on main and not yet run) |
| catalog-build-context | issue #1738 (milestone 0.26.0): the gate flip is merged (#3080) and shipped in 0.26.0; the script that writes Check 292 is merged (#3145); owner Check 292 runs after it, on the sandbox installation only |
| central-orchestrator | superseded by stateless-orchestrator |
| checkpoint-pushes | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| cli-output-console-graph | owner Checks 310 (with its `--scratch` mode), 311, 313 and 314 (all scripts on main), not yet run |
| cloud-senior-review | CSR17 and CSR18 moved to api-key-review |
| compatibility-matrix | owner CM4 (the read of the copy) |
| compute-backend | declared operational by the owner 2026-11-15; owner Checks 57b (its stranded-job submission fixed) and 124, not yet run (Task 13 was dropped) |
| config-management | none |
| config-ownership | none (22/22 merged: K1 as #3337, I1 as #3391, D1 as #3386); D2611, D2613, D2614, D2616 and T3, T5 answered 2026-11-15; no owner check; after deploy: `fugaro update` |
| config-transitions | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| console-coordinator-v2 | owner Check C0 (profiling) |
| console-dependency-graph | owner Check 123 (the graph in the packed, signed macOS app, GV2.6), not yet run |
| console-managed-screens | none (console 0.121.0) |
| console-settings-edit | none (console 0.120.0) |
| console-styling | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| console-windows | owner Check 119 |
| contract-first-decoupling | none |
| coordinator | P7 and P8 re-planned under mcp-server |
| credentials-service | none, Done by the owner's ruling and not re-counted here (no agent task is left; the owner-run Checks 91, 92, 94, 95, 96 and 99 are the owner's to run or record (Check 90 ran 2026-10-21), and Check 96 holds C2.3's flip, which waits on the deployed service minting a token) |
| desktop-console | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| desktop-console-v2 | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| diagnose-agent | owner Checks 410 to 412 |
| ee-reorg | owner E3.4 (Checks 200 and 201, scripts on main, not yet run); E4.1 deferred |
| failure-classes | owner Check 350 |
| feature-matrix | T6 is done (the owner read the preview on 2026-11-17, no footnote, O1 answered) and CR1 is not raised, so it is not counted; the owner's mapping corrections merged as #3047 (mapping only); D2440–D2449 answered; no check taken; no after-deploy step |
| forge-labels | owner Check 640 (its script is merged, #3155; not run) |
| generic-tool | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| headroom-compression | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| how-to | not a spec |
| image-env | owner Check 291 (script on main, not yet run) |
| image-refresh | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| image-setup-skill | owner Checks 500 and 501 |
| in-job-review | historical; delivered through cloud-senior-review, review-levels and api-key-review |
| issue-mine | owner Checks 470 to 472: all ran 2026-11-14 and none has passed in full (470 passed in part, its issue-record read and two human steps unmeasured; 471 failed its cache-cleared row; 472 passed on an empty store, not at size), so re-runs are owner-run |
| issues-resolved-on-update | none |
| layered-config | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| m10-multi-model | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| m11-setup-and-skills | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| m9-budget-and-dashboard | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| managed-agent-converge | none |
| managed-deployer-fence | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| managed-projects | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| mcp-server | M9 parked (D936, the fence declined, no release schedules it); M11 declined (2026-11-09) |
| mixed-credentials | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| mobile-auth | owner live checks, numbered Checks 620 to 627 on main (draft #2700's 601 to 608 was closed); P5's D231 gap is a follow-up awaiting the owner's decision — revoking the last device empties the node, so the next publish re-seeds the revoked entry |
| model-ab-testing | owner Checks 210 to 212 (written on main, not run; drafts #2634 and #2637 were closed as superseded) |
| models-commands | none |
| named-oauth-tokens | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| no-prompt-policy | owner Checks 380 to 382 (scripts on main), not yet run; the T11 verification runs found nothing to fix (#2728, #2729) |
| orchestrator-graph-estimates | none (39/39 merged: R5 as #3428); D2700–D2747 answered 2026-11-16 (D2720 not the recommendation; the decisions amended after review await the owner's confirmation, O4); Checks 700–703 owner-run |
| outer-loop-escalation | owner Checks 540 to 542 (written as scripts, E15 merged as #3147); `escalation.ready_drafts` ships on by default (#3146, D2046) and the daemon already reads it (#3149 found it wired and changed nothing) |
| plugin-user-scope | E1, the release itself (rides the next CLI release; no operator step) |
| post-review-triage | owner TR13 (Checks 490 to 492; the scripts are on main, not yet run) |
| project-centric | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| project-image-catalog | none |
| project-named-oauth-tokens | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| project-secrets | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| project-workflow-secrets | none |
| provider-price-catalog | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| recipes | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| registry-remote-repository | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| retire-image-mirror | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| review-job | owner Checks 610 and 611 (scripts on main, #3150 and #3154), not run |
| rigor-command | none (R2 to R5 were closed 2026-11-10 as duplicates of settings-commands and config-management, which win; every live task has merged) |
| roles | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| roles-scopes | owner live check that a viewer cannot launch or cancel (not yet run) |
| run-limit-carry-over | owner-run live check (named, not numbered: a chain that halts at its dollar cap, continued with `--continues`, reports a smaller remaining than it started with) |
| run-without-checkout | no live check covers a launch from outside a checkout |
| security-classification | none |
| settings-commands | none |
| setup-wizard | owner Checks 27 (script on main) and 111 |
| shared-config | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| short-run-ids | owner SR13 (Check 440 as a script and Check 441 as the register's steps, on main, not yet run) |
| spend-accuracy | OR1 and Checks 650–651 (script written, #2971) owner-run; D2640–D2651 answered 2026-11-15 (D2643 not the recommendation; four amended after review); SA1.4, SA2.3 and SA6.1 merged 2026-11-17 (#2969, #2974, #3053) |
| stateless-orchestrator | owner Checks 301 to 305 (O16b; 301 deferred); the orchestrator is installed under launchd and replaces /jobmax, and its state is stale (goal not set) |
| task-governance | owner Checks 137 and 138 (scripts on main, #3087), not run |
| troubleshoot-issue | none |
| upgrade | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| vision-model | owner Checks 520 and 521, not run; issue #1865 carries the 0.26.0 milestone: its code merged in 0.25.0 and only these two checks remain |
| voice | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| watch-queued | none recorded: a spec from before the folder layout, moved from docs/design and docs/plans on 2026-11-21 and not re-counted |
| website | W3.3 explainer video (parked), owner Check 69c (not written yet; 69a and 69b passed 2026-11-15) |
| website-changelog | WC4 lands with the next release |
| windows-cli | superseded by console-windows |
| workspaces | none |
