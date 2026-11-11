# Account aliases: reconciliation

**Spec:** [spec.md](spec.md) (the owner's one-line ruling, quoted verbatim there, made concrete).
**Checked against** `origin/main` at `d51ada6d5` and the pull requests open on **2026-11-10**.
**Where this file disagrees with `spec.md`, this file wins.** Nothing is implemented; no task is
approved; **every decision below is OPEN** and waits for the owner.

**Numbers:** this work holds **D2070–D2089** (D2070–D2082 written, D2083–D2089 reserved) and
**Checks 550–554** (550 written, 551–554 reserved). The highest decision on `main` is D2019; the
open pull request #2285 (`docs/outer-loop-escalation`) reserves D2030–D2069 and Checks 540–549; no
open pull request's diff names a decision in D2070–D2089 or a check in 550–559.

## 1. What the repo already has

| Ruling concept | What exists on `origin/main` |
| --- | --- |
| "a stage's `claude_account`" | `stages.<stage>.claude_account` in the local config (`localcfg.Config.ClaudeAccountStages`, `internal/localcfg/localcfg.go:193`), the project default `default_claude_account` (`:187`) and the dormant switch `claude_account_auto` (`:198`); mirrored to the node as `default_claude_account` and `claude_account_stages` (`internal/projectpolicy/policy.go:218`, `:244`) |
| One place a reference is admitted | `(*localcfg.Config).choiceRefProblem` (`internal/localcfg/localcfg.go:651`): a choice key names an account of `claude_accounts` or `default`, never an axis non-account value |
| The account a launch resolves | `claudeAccountFor` (`internal/cli/run.go:2338`), rungs 1–5 of [docs/accounts-add/spec.md](../accounts-add/spec.md) §11.2, ending in `claudeAccountAllowed` (`run.go:2894`) |
| The resolved account a run carries | `task.Spec.OAuthToken` (`internal/task/task.go:194`), which names the job variant that mounts the account's token |
| What the record names | `runstore.Record.OAuthAccount` (`internal/runstore/runstore.go:1228`) and the per-stage `StageAccounts` (`:1246`, D876) |
| "One command" precedent | `fugaro accounts add`'s converge: `accountsAddRunStages` and `accountsAddStages` (`internal/cli/accounts_add_converge.go:267`, `:346`) run the policy, installation and repository stages of `fugaro update` |
| The one write path | `applySettingsEdit` (`internal/cli/settings_write.go:106`), with the coding-agent-session refusal |
| A named indirection that already exists | pools (`config.Pool`, `internal/config/craft.go:204`; `fugaro accounts pool`, `internal/cli/accounts_pool.go`) |

## 2. Findings

**F1 — Gap: under `oauth` a stage key is never read at a launch.** Every caller of `claudeAccountFor`
passes `stage == ""` (`internal/cli/run.go:527`, `:1925`, `:2241`, `:3623`; `internal/cli/followup.go:626`,
`:715`), and rung 2 is walked only when a stage is named (`run.go:2431`). Under `oauth` a run holds one
account — the job's (`internal/runner/accountchoice.go:237`) — so `stages.<stage>.claude_account` is
validated and allow-listed (`run.go:2364`) but serves no stage. It **does** serve the `api-key` mix:
`mixedRunAccount` (`run.go:2524`) takes the first mixed stage's key, and the runner reads the node's
`claude_account_stages` for `review`, `adversarial` and `triage` (`internal/runner/agentenv.go:148`).
*Consequence:* an alias on a stage key changes runs only where a stage key changes runs today — the
`api-key` mix — and on `default_claude_account` (rung 4, `run.go:2494`) it changes every `oauth` run.
That is why D2071 recommends admitting the alias on the default too; without it the ruling would bite
only mixed projects.

**F2 — Exists: the launch already resolves once and the run already holds what it resolved.** The
account a run holds is `spec.OAuthToken`, set from `claudeAccountFor`'s answer (`run.go:2252`), and it
names the job variant that mounts that one token (`internal/infra/spec.go:1074`, `:1084`). The runner
memoizes the policy node at bootstrap (`policyRead`, `internal/runner/projectpolicy.go:45`) and reads
the mixed stages once (`resolveMixedStages`, `agentenv.go:132`). *Consequence:* "a run in flight never
re-resolves" holds by construction once the alias is resolved inside `claudeAccountFor`; no runner
reads an alias.

**F3 — Gap: the one in-flight window.** A mixed `api-key` run launched before a repoint whose
bootstrap reads the node after the publish that carries it gets the **new** account from
`p.ClaudeAccountStages` (`agentenv.go:149`) while its job mounts the **old** token; `stageAgentEnv`
(`agentenv.go:266`) then refuses the stage for a token the job does not hold. The run fails closed, not
on the wrong account, but it is a run in flight that a repoint broke, which the ruling forbids. The same
window exists today for a hand edit of the stage key; aliases make it a routine act. *Consequence:*
D2074.

**F4 — Rule: an alias must never reach the node by its own name.** The node's account-choice members
are read by every job image in the field and are validated there with `config.ValidClaudeAccountRef`
(`internal/projectpolicy/policy.go:1281`); an image built before this release would read an alias as
an account with no job and no token. `config.AccountStagesAReleasedImageKnows`
(`internal/config/stage_accounts.go:96`) exists for exactly this skew. *Consequence:* the publish
writes the **resolved** account (`configAccountValues`, `internal/cli/init_policy_publish.go:248`), the
node's format and `projectpolicy.Version` (`internal/projectpolicy/version.go:79`, 4) do not move, and
`fugaro update` has nothing to migrate (HARD RULE satisfied by construction; pinned by a converge test
in AL0).

**F5 — Rule: the choice keys are local-only, and so is the alias.** `sharedForbidden`
(`internal/cli/sharedcfg.go:277`) bars `default_claude_account`, `stages` and `claude_account_auto` from
the runs bucket's shared file, and `localcfg.Config.Shared` (`internal/localcfg/shared.go:68`) drops
them. *Consequence:* `claude_account_aliases` joins both lists (D2070). A launcher on another machine
reads no alias, exactly as it reads no `default_claude_account` today.

**F6 — Rule (security): resolution must come before the allow-list.** `claudeAccountAllowed` and
`claudeAccountMembership` (`run.go:2894`, `:2962`) are asked about a name; if they were asked about the
alias they would find no `claude_accounts` entry and refuse, and a reader that skipped them for an alias
would be a way around `allow_data_to`. *Consequence:* every reader resolves first and checks the
resolved account; AL1's negative test is an alias whose target the repository is not admitted to.

**F7 — Rule (security): the policy merge never sees an alias.** `internal/policy`'s merge reads
layers built from `fugaro.yaml`, the project layer and the node; the craft access a later layer may
select is limited to names the project defines (`selectCraftAccess`, `internal/policy/craft.go:271`;
`craftAccessAvailable`, `:297`). An alias lives in none of those layers, so the tighten-only merge is
untouched and no repository can name one. *Consequence:* `agent.oauth_token` and craft access may not
name an alias in this plan (D2071, D2080).

**F8 — Rule: one namespace.** A craft's access names a credential or a pool without saying which, and
`ValidatePools` refuses a pool and a credential of one name (`internal/config/craft.go:359`);
`credentialNameProblem` (`:271`) holds the reserved names. A choice key likewise names an account
without saying what it is. *Consequence:* an alias may not carry the name of any account, credential
or pool (D2072).

**F9 — Gap: the job set follows the resolved account under `api-key`.** `WorkflowAccounts`
(`internal/infra/spec.go:1119`) builds one job per allowed account under `oauth` — so every target an
alias may take already has its job — but under `api-key` only the default plus `mixedStageAccounts`
(`:1990`, `:2044`), which reads the stage keys and the default. A repoint of a mixed project's alias
therefore adds the new target's variant and drops the old one from the set. Dropping a variant is the
repository stage's "needs care" row of [update-in-flight](../design/update-in-flight.md) (a job delete
takes its executions). *Consequence:* D2075.

**F10 — Exists: the publish already treats a changed account member as a widening.**
`policyAccountWidening` (`internal/cli/init_policy_publish.go:531`) reports a member the publish sets to
another account. *Consequence:* a repoint's publish is reported the same way; the alias command adds no
second confirmation (D2077), and the no-prompt policy (D1540) means no typed name.

**F11 — Gap: `accounts rm` does not know about aliases.** `newAccountsRmCmd`
(`internal/cli/accounts_rm.go:72`) refuses while a profile lists the account. A removed target would
leave an alias pointing at nothing and every key naming it refused at the next launch. *Consequence:*
D2081.

**F12 — Gap: the follow-up inherits the resolved name.** `followup.go:715` passes the previous task's
`OAuthToken` as the flag, which is the resolved account. *Consequence:* after a repoint, an open pull
request's follow-ups stay on the old account unless `--token` names the alias (D2078).

**F13 — Exists: the read surfaces.** `fugaro accounts ls --json` (`accountsListDoc`,
`internal/cli/accounts.go:65`) is spawned by the console (`console/src/main/spawn.ts:988`); an additive
member is admitted by the "a field never changes meaning" rule. The settings registry carries one row
per choice key (`internal/settings/registry.go:770`, `:771`, `:991`), and `settings set` writes them
(`internal/cli/settings_set.go:118`). *Consequence:* D2079 and AL3's registry row.

**F14 — Exists (and keeps): the auto walk is accounts-only.** `stageAccountCandidates`
(`internal/runner/accountchoice.go:301`) walks the node's `accounts`, which never hold an alias (F4).
*Consequence:* no change to the walk; D2080 records it.

**F15 — Gap: model-crafts is mid-migration over the same keys.** MC4 (#2252, open) derives craft keys
from the stage keys and would read an alias name in a stage key as an account (`deriveCraftCredentials`
in its `internal/cli/init_craft_migrate.go`). MC5 (#2274, draft) edits `internal/config/stage_accounts.go`,
`internal/localcfg/localcfg.go`, `internal/cli/init_policy_publish.go`, `internal/cli/settings_set.go`
and `internal/settings/registry.go`. *Consequence:* D2082 (ordering) and the plan's serialization.

**F16 — Exists: cascade is out of reach.** Under `cascade` the credentials service re-reads the node
per call (update-in-flight, the policy row), but a stage account is refused under `cascade`
(`config.CheckStageAccountMix`, `internal/config/stage_accounts.go:192`) and rung 4 never applies there
(`run.go:2404`). *Consequence:* no alias reaches a cascade run; nothing to design.

## 3. Decisions D2070–D2081 (all OPEN)

| # | Question | Recommendation (reason) | Alternatives and their cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D2070** | Where does an alias live? | **The owner's local project config, block `claude_account_aliases`, resolved before anything is published** (F4, F5): no node format change, no policy version, no `update` migration, no image skew. | (b) A node member resolved by the runner: a version bump with a migration in `update`, a skew gate for every image in the field, and a run that resolves at bootstrap — the window F3 describes, widened. (c) The committed `fugaro.yaml`: refused by the boundary of [accounts-add §7](../accounts-add/spec.md); a repository would choose the owner's credential. | OPEN |
| **D2071** | Which keys may name an alias? | **`stages.<stage>.claude_account`, `default_claude_account` and `--token`** (F1): the stage key alone serves only `api-key` mixes today, and one rule for every choice key is simpler to state and test. | (b) Only stage keys, the ruling's literal words: smaller, but an `oauth` project gets nothing from it. (c) Also `agent.oauth_token` and profile lists: a repository file depending on an owner-local name, and an alias deciding which jobs exist (F7). | OPEN |
| **D2072** | Name rule and namespace. | **The account name rule plus the reserved names, in one namespace with accounts, credentials and pools** (F8). | (b) A sigil (`@reviewer`): readable at a glance, but every validator, the registry and both docs learn a second syntax. (c) A separate namespace: a choice key's value would be ambiguous. | OPEN |
| **D2073** | What may an alias target? | **One defined `claude_accounts` account or `default`; no chains, no pools, no non-account axis values.** | (b) Chains: a cycle check and a read that has to print a path. (c) Pools: resolved in the run (MC5), which is the re-resolution the ruling forbids. | OPEN |
| **D2074** | How is the in-flight window of F3 closed? | **The runner serves a mixed run's stages with the account its launch resolved (`spec.OAuthToken`) when the node now names another, held to the node's admission (`accounts.<name>.allow_repos` admits the repository) and logged.** No task field: the account a mixed run holds is already the launch's (F2), and the job mounts only that token, so this admits nothing a `--token` could not. | (b) An additive task field carrying the resolved per-stage map: a skew gate (`checkImageSince`, `internal/cli/recipes_skew.go:1018`) and a format to keep for no extra safety. (c) Leave it: a repoint can still fail a launched, not-yet-bootstrapped run closed. | OPEN |
| **D2075** | Under `api-key`, what happens to the old target's job on a repoint? | **The repoint adds the new target's variant and never deletes the old one; it stays until a retirement the operator asks for, which already names executions in flight** (F9). | (b) Let the repository stage drop it: the "needs care" row of update-in-flight, on a routine command. (c) Refuse aliases on `api-key` mixes in the first slice: no job-set change at all, but the ruling's own case (a stage key) is the mix's. | OPEN |
| **D2076** | What is the one command? | **`fugaro accounts alias set ALIAS ACCOUNT` and `accounts alias rm ALIAS`**, converging through `accountsAddRunStages` so the node and the job set are current when it returns; the registry row's owner is the `accounts` family. | (b) `fugaro settings set claude_account_aliases.ALIAS ACCOUNT`: no new command, but no converge — the node and jobs wait for `fugaro update`, two commands. (c) Both: two doors to one key. | OPEN |
| **D2077** | Who may repoint? | **The owner at their own terminal: refused in a coding agent's session (as `accounts add` is), no typed confirmation (D1540); the publish's existing widening line reports the change (F10).** A managed project's agent goes through the managed gate exactly as a write of `default_claude_account` does. | (b) Allow it in an agent session because the target is already admitted: an agent could move the project's spend to another subscription. | OPEN |
| **D2078** | Does a follow-up follow a repoint? | **No: it keeps the account its previous run resolved, as today (F12); `--token ALIAS` moves it.** No format change. | (b) Record the alias in the task (additive, skew-gated) and re-resolve it on a follow-up: what an owner who repointed because an account ran out usually wants, at the cost of a task field and its gate. | OPEN |
| **D2079** | What do the reads show? | **`accounts ls` (text and an additive `aliases` member in `--json`), the settings rows, and the launch line `ALIAS -> ACCOUNT`; the run record names the resolved account only.** | (b) An additive record field naming the alias per stage: `diagnose` could say which alias a run used, at the cost of a field the console's redactor must classify (as D876's later task notes for `stage_accounts`). | OPEN |
| **D2080** | Failover, pools and crafts. | **The auto walk is unchanged (F14); a pool may not list an alias; craft access may name an alias only in a later task held behind MC5** (F7, F15). | (b) Make an alias a one-member pool: no new concept, but a pool is chosen in the run. (c) Design the craft half now: written against keys MC5 is still moving. | OPEN |
| **D2081** | `accounts rm` of an alias's target. | **Refused, naming `accounts alias set` (or `alias rm`)** (F11). | (b) Remove the alias with it: every key naming the alias then refuses at the next launch, discovered late. | OPEN |

D2082 is the ordering question below; D2083–D2089 are reserved and unused.

## 4. Open tensions and open items for the owner

1. **D2082 — ordering against model-crafts (OPEN).** Recommendation: **build aliases on today's keys
   after #2252 (MC4) and #2274 (MC5) merge**, and teach MC4's migration to resolve an alias before it
   derives a craft key (AL1 carries that one function if MC4 has merged; otherwise it is a note to
   #2252). Alternative: wait until crafts replace the stage keys and define the alias only as a craft
   access name — cost: the ruling waits for MC5–MC9.
2. **D2071 vs the ruling's words.** The ruling names the stage key; the recommendation widens it to the
   project default and `--token` because of F1. If the owner keeps the literal reading, AL1 drops two
   rungs and the spec's §4 table loses two rows; nothing else moves.
3. **D2074 changes who decides a mixed stage's account at run time** from the node alone to the launch's
   resolution held to the node's admission. It is a security-tree change (`internal/runner`) and is
   planned at opus with an in-run level 3 review.
4. **Independent review has not run on this package** (the author is a subagent that does not spawn
   reviewers).

## 5. UNVERIFIED

- Whether a Cloud Run task retry of an execution re-bootstraps and so re-reads the node: if it does, a
  retry after a repoint is F3's window again, and D2074 covers it; not read from the platform here.
- What the repository stage does today to a job variant that leaves `WorkflowAccounts` without
  `--retire` (state removal or a destroy behind `deletion_protection`): AL1's
  `TestRepointKeepsTheOldTargetVariant` is written to fail first and settle it.
- That no plugin skill names `default_claude_account` or the stage keys in a way the alias changes: AL4
  greps `plugin/skills` before editing.
