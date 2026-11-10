# Fugaro: status of every spec under docs/

As of 2026-11-10, read from `main`, the open pull requests and each plan's task list (not from the plans' own status lines, which are stale in about 30 folders). Counts come from commit titles and file checks; "about" marks the approximate ones. **Owner-run** means a live check or step that only the owner performs. Where a folder's own status line disagrees with its row here, the row is the current one.

## Done

| Spec | Left |
| --- | --- |
| console-managed-screens | none (console 0.121.0) |
| console-settings-edit | none (console 0.120.0) |
| console-coordinator-v2 | owner Check C0 (profiling) |
| managed-agent-converge | none |
| models-commands | none |
| project-image-catalog | none |
| project-workflow-secrets | none |
| accounts-add | owner checks LC1 to LC4 |
| c39-review-identity | owner live check |
| image-env | owner Check 291 |
| setup-wizard | owner Checks 27 and 111 |
| ee-reorg | owner E3.4; E4.1 deferred |
| failure-classes | owner Check 350 |
| website | W3.3 explainer video (parked), owner Check 69 |
| cloud-senior-review | CSR1 to CSR16 done; CSR17 and CSR18 moved to api-key-review |
| how-to | not a spec |

## In progress, close to done

| Spec | Merged | Left |
| --- | --- | --- |
| console-windows | 39/40 | T-H4 (draft #2175); owner Check 119 |
| short-run-ids | 12/14 | SR12 (draft #2168); owner SR13 |
| website-changelog | 3/4 | WC4 at the next release |
| licensing | 17/23 | owner-run only |
| credentials-service | about 32/36 | owner checks; Check 96 holds C2.3 |
| console-dependency-graph | 13/17 | GV3.1, GV3.2, GV4.1 |
| bitbucket-credentials | 9/12 | T2.6, T2.11; owner Checks 149 and 145 |

## In progress, a lot left

| Spec | Merged | Left |
| --- | --- | --- |
| stateless-orchestrator | 10/19 | O8 (draft #2064), O9 to O14, O16, O19; owner Checks 300 to 305 |
| api-key-review | 6/11 | AK6b (draft #2172), AK7, AK9, AK10; owner Check 330 |
| secrets-brokering | 18/30 | B1.13 flip (draft #2166, held on Check 191), B1.15, B1.16, B2.1 to B5.1; owner Checks 191 and 192. Dynamic-delivery subfolder: DS0.1 merged, five drafts |
| subscriptions | about 17/51 | about 22 tasks; 10 owner-run |
| workspaces | 6/10 | W3 and W6 (drafts), W7, W9 |
| no-prompt-policy | 8/17 | T4 and T7 (drafts), T9 to T13, T15; owner T14 |
| image-setup-skill | 4/14 | 7 in draft PRs; IS2, IS12, IS13 not started |
| mcp-server | 8/12 | M4 store (draft #2117), M5b (draft #2114); M9 deferred |
| diagnose-agent | 5/10 | T-DG7 (draft #2138), T-DG6, T-DG8 to T-DG10 |
| troubleshoot-issue | 12/18 | IS6 (draft #2135), IS19 (draft #2115), T-IS7, T-IS9, T-IS11, T-IS12 |
| periodic-reviews | 7/14 | R7 to R12; owner Checks 136 and 340 |
| config-management | 5/14 | G3 to G7, G9 rest, G10, G12, G13 |
| contract-first-decoupling | 4/7 | CD4, CD5, CD7 |
| issue-mine | 4/7 | T-IM3, T-IM6 (the command), T-IM7 |
| cli-output-console-graph | 19/30 | CL6, CL9 to CL11, CG4 to CG6, CG13 |
| issues-resolved-on-update | 3/8 | T-RU4 to T-RU8 |
| catalog-build-context | 2/5 | CB2, CB4, CB5 |
| post-review-triage | 5/14 | TR3, TR5 to TR10, TR12; waits on BF1m (#1999) |
| model-crafts | 4/10 | MC4 to MC7, MC9; owner MC10 |
| security-classification | 6/11 | SC3, SC5, SC9 to SC11 |
| settings-commands | about 6/12 | S6 to S11 |
| rigor-modes | about 22/40 | T7.1, T7.2, T8.1, T8.3; overlay plan LV1 to LV7 essentially unstarted |
| rigor-command | 3/10 | R6, R7, W; R2 to R5 closed 2026-11-10 as duplicates of the two winning plans |
| review-levels-ab-jobmax | about 24/40 | A/B reviewer slot, `/jobmax` skill, `auto_launch` |
| forge-authorship-images | about 9/25 | about 16, much overlapping image-setup |
| go-to-market | 4/12 | the cost-estimate half (`fugaro estimate`) |
| model-ab-testing | 3/18 | the sampler and the pair launch |
| mobile | about 35/45 | P5.x, P7.2 to P7.5; owner P2.3 and P4.4 |
| task-governance | 23/31 | 8 left, all in `ee/governance` |
| bitbucket-workspace-token | 7/13 | T2.2, T2.4, T2.6, T2.9, owner checks; the proxy chain probably makes T2.2 and T2.9 moot |
| compatibility-matrix | 1/4 | CM2 (page), CM3, CM4 |
| roles-scopes | 3/12 | RS3 to RS8, RS10; RS12 is later and not planned here |

## To do, parked or superseded

| Spec | State |
| --- | --- |
| run-limit-carry-over | 0/10; design only |
| binary-allow-rule | 0/11; its 0.20.0 slot passed |
| plugin-user-scope | 0/10; `fugaro upgrade` is still on main |
| run-without-checkout | 0/8; its releases shipped without it |
| composite-commands | 1/10; depends on no-prompt-policy |
| central-orchestrator | superseded by stateless-orchestrator |
| coordinator | 7/9; P7 and P8 re-planned under mcp-server |
| windows-cli | 10/31; superseded by console-windows |
| mobile-auth | 2/7; parked |
| repo-image-removal | 2/11; parked by image-setup (D1900) |
| in-job-review | historical; delivered through other folders |
| voice-module, backlog, plans | no plan or not a spec |

## Open points

1. Overlapping chains to settle: forge-authorship-images against image-setup, repo-image-removal (parked), and bitbucket-workspace-token against the secrets proxy chain.
2. Slots that passed with nothing shipped: binary-allow-rule, run-without-checkout, run-limit-carry-over, plugin-user-scope. Each needs a release slot or a decision to drop it.
3. Pull requests #2118 and #2131 are both S3.5 (the key service's infrastructure); one is a duplicate.
