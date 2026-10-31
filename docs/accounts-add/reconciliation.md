# `fugaro accounts add`: reconciliation against the code

Read against `origin/main` at `802ae65eb` — which now carries #1464 (the settings design, D840–D859),
#1468, #1471, #1463 and #1467 — and the pull requests open on 2026-10-31. The findings were first written against
`0a1f5694` and re-checked against the merge; the rows that moved say so. Each finding is
**Exists**, **Missing**, **Conflicts** or **Ungoverned**, with its consequence for [spec.md](spec.md).
Citations name a file and a symbol, never a line number: a line number is stale the moment anything above
it changes, and the pin this document serves is about the code's shape, not its layout.

## 1. What exists

**F1: Exists. The account model itself, end to end.** A named account is a name in the local config
(`localcfg.Config.ClaudeAccounts`), a project-wide secret `claude-oauth-token-<name>`
(`config.ClaudeAccountSecret`), a Cloud Run job per `(profile, provider, account)`
(`config.ProjectJobAccounts`, `config.ProjectJobTokenAccount`, `gcp.ProjectJobName`), an `accounts`
block in the published policy derived at publish (`projectpolicy.DeriveAccounts`, reached from
`buildProjectPolicy` in `internal/cli/project_cmd.go`), and a per-repository admission node
(`projectpolicy.RepoPolicy`). The subset rule and the reserved name are enforced by
`config.ValidateProfileClaudeAccounts` and `reservedClaudeAccountName`
(`internal/config/claude_accounts.go`). *Consequence:* `accounts add` writes **no new rule**. It is an
assembler: every check it makes is a call, and its own logic is the order of the calls.

**F2: Exists. The store, the prompt and the redaction floor.** `secretsSetProjectWide` decides
eligibility *before* any value is read (`projectWideAccepted`, `projectWideRefusal`, and
`projectWideHint` for the other intent); `storeSecret` reads and stores; `readSecret` picks the hidden
prompt or stdin; `readHidden` / `readHiddenLine` are the echo-free terminal read with Ctrl-C handling;
`validateSecret` is the empty / floor / 64 KiB / one-line rule; `minSecretBytes` is the redaction floor;
`clear` wipes the buffer; `agent.Redact` is applied to anything a server may have echoed. All in
`internal/cli/secrets.go`. *Consequence:* spec §2.2 steps 3 and 5 are calls, and the value's whole
lifetime is already written down.

**F3: Exists. The name grammar, and two reserved names with two different rules — which spec §2.4 had
conflated.** `config.ClaudeAccountNameRE` and `config.MaxClaudeAccountNameLen` are the shape.
`reservedClaudeAccountName` (`internal/config/claude_accounts.go`, `name == config.BitbucketSecret`)
refuses **`bitbucket-token` only**, and `ValidClaudeAccountRef` refuses it with
`reservedAccountNameMessage` — but `ValidClaudeAccountRef` **accepts `default`** (returns `""`, and only
here: it is the unsuffixed `claude-oauth-token` a profile is meant to be able to list), while
`ValidateClaudeAccounts` refuses `default` **as a definition**
(`"default" is reserved for the unnamed claude-oauth-token secret and may not be configured here`).
*Consequence:* `accounts add` **defines** an account, so it refuses `default` with
`ValidateClaudeAccounts`'s own sentence and `bitbucket-token` through `ValidClaudeAccountRef` — it
cannot reuse `ValidClaudeAccountRef` alone for the name check, because that function's answer for
`default` is the one case where a reference and a definition differ. The refusals of spec §2.4 are those
two calls, plus the pattern. (This row was wrong when the design was first written: it said the command
"adds the two reserved names' refusals", which would have refused `default` with the wrong sentence and
missed that `ValidClaudeAccountRef` already refuses `bitbucket-token`.)

**F4: Exists. The read side.** `fugaro accounts ls` (`internal/cli/accounts.go`) prints the names a
repository may choose among — `config.AllowedClaudeAccounts`'s own order — plus each account's
credential state from the Realtime Database, as `accountsListDoc`, with `Complete` and `Note` for the
cases where the answer is short. `accounts clear NAME` clears one mark under the rules in its own
`Long` (a `broken` mark is the operator's to clear; a `backed_off` one is a probe's; an unknown state is
nobody's). The states are `internal/creds`. *Consequence:* §2.2 step 8's read-back is a call, and
spec §5's "`clear` stays" is a decision about a door that already exists and already says no to the
wrong states.

**F5: Exists. The converge seam.** `initStages` (`internal/cli/init_stages.go`) is the one list both
front doors register: `Preflight`, `Installation`, `Firebase`, `Images`, `Installation` again,
`Secrets`, `Plugin`, the running checkout's `Repository`, `Rules`, the `Policy` stage and the checkout's
own steps — each an `initflow.Stage`. `newUpdateCmdWith` and `newSetupCmdWith` differ only in the
project stage's creation path. *Consequence:* spec §2.5's "invokes those stages in-process" is
reachable without a second pipeline: the command builds the same engine and runs the stages it names.

**F6: Exists. The job set and the per-secret IAM, on both roots.** `infra.ProjectJobs`
(`internal/infra/projectjobs.go`) computes one job and one service account per entry of
`config.ProjectJobAccounts`, with a `SecretEnv` naming the account's project-wide secret, and refuses a
profile that `projectpolicy.DeriveAccounts` could not describe. On the per-repository side,
`modules/repo/workflows.tf` merges `repo.project_secrets` into each workflow's secret map and
`modules/workflow/sa.tf` grants `roles/secretmanager.secretAccessor` **per secret ID it mounts**, never
project-wide. *Consequence:* an account named by `--repos` is served by the *installation* apply if a
profile admits it, and by its *own repository root* apply if a per-repository job selects it — which is
why spec §2.5 converges both, and why F7's checkout record matters.

**F7: Exists. The record of where this machine has each repository.** `localcfg.Checkouts`,
`localcfg.CheckoutsPath`, `localcfg.LoadCheckouts` (`internal/localcfg/checkouts.go`), and the
`agentAtRef` / `layerRoutes` reads the models reconciliation's F13 already lists. *Consequence:* a
`--repos` repository with a checkout here is reachable for its own repository root; one without is not,
and spec §2.5 says so instead of pretending otherwise.

**F8: Exists. The coding-agent marker.** `agentenv.Marker`, wrapped as `agentMarker` / `agentEnv`
(`internal/cli/initsecrets.go`), read by the stages that must not act in an agent's session.
*Consequence:* spec §2.4's first row is a call, not a convention.

**F9: Exists. The comment-blind write helpers.** `lineDiff`, `backupFile` (`<file>.bak-<timestamp>`,
mode 0600) and `writeFileAtomic` (`internal/cli/init.go`), and `setGCPProjectLine`
(`internal/cli/init_fugaroyaml.go`) as the one-line-edit precedent. *Consequence:* spec §2.6's diff and
atomic write are reused; what is *not* reusable is their callers' whole-struct `yaml.Marshal`
(`initRun.writeConfig`, `restoreRun.writeConfig`), which is F12's reason to exist.

## 2. What is missing, and what conflicts

**F10: Missing. `fugaro update` does not publish the policy node from the local config.** The `Policy`
stage on `main` **migrates** the node in place — `policyMigration` / `withDerivedMembers`
(`internal/cli/init_policy_migrate.go`), which adds the version this binary writes and the Bitbucket
members, and never re-derives `profiles` or `accounts`. The node's `profiles` and its derived `accounts`
block are written only by `buildProjectPolicy`, whose one non-test call site is
`internal/cli/project_cmd.go`'s publish. *Consequence:* **the publish half of spec §2.5 does not exist
today**, and without it `accounts add` leaves the owner a second command — exactly the HARD RULE
violation the brief was written about. This is dependency **E2**.

**F11: Missing. No command writes `claude_accounts` or `profiles`.** The local config's only writers
are the two converge doors, and both marshal the whole struct (`localcfg.Config.Marshal`). Nothing edits
one key. *Consequence:* `accounts add` is the first targeted writer of the project's local config, and
it is why F12 exists.

**F12: Exists (dependency landed). `internal/yamledit` is on `main` — PR **#1467**, merged as
`802ae65eb`.** Its own
package doc promises exactly what this work needs: `SetScalar`, `AddListItem`, `RemoveListItem`,
`UpsertSeqItemByKey`, `RemoveSeqItemByKey`, `Changed`, a result that re-parses, and refusals that never
echo document bytes. *Consequence:* spec §2.3 depends on it. Dependency **E1**.

**F13: Missing (dependency). `internal/cli/settings_write.go` is not on `main`.** It is the settings
design's S5 — whose **design** merged as #1464 on 2026-10-31 (D840–D859 are on `main`), while the
**code** has not: `internal/cli/settings_write.go` is absent from the merged tree, so S5 is specified
and unimplemented. Its own shared-files table says the five helpers — the diff, the confirmation with
the typed project name, the agent-session refusal, `backupFile`, `writeFileAtomic` — "exist once", and
that "a second copy is the thing their plans and this one both forbid". *Consequence:* spec §2.2 steps 4
and 7 call it, and this design waits for the settings implementation, not merely for its PR. Dependency
**E3**.

**F14: Exists, and it binds. The ownership gate.** #1460, merged as `7efc78e0`, gives the migration
`policyMigrationFor` / `budgetDBUnadopted`: a database in a Firebase project the installation never
adopted is skipped rather than written. The settings design's S3 notes that the publish half must reach
the node through the **same** call — one gate read, one ETag, one write path — with
`TestUpdatePolicyPublishSkipsTheDatabaseTheRulesStageSkips` as the pin. That test **does not exist on
`main`**: it arrives with E2 (#1464's S3, whose design merged while the code did not), which is why A2
cites it as the gate's pin
rather than as an existing check. *Consequence:* `accounts add`
must not become a second writer with its own weaker gate: spec §2.5's first bullet is a hard
requirement, not a preference.

**F15: Conflicts. The pty package is pinned as a test-only dependency, in two places this work does not
own alone.** `go.mod` carries `github.com/creack/pty v1.1.24 // tests only: the hidden-prompt test needs a
pseudo-terminal`, and `internal/cli/docs_console_boundary_test.go`'s `TestGoModHasNoConsoleDependency`
holds that exact line — comment included — in `consoleGoModRequireBaseline`. A production use of the
package makes the comment false, so both change together. Neither file is on the 2026-10-30 reserved
list, so an accounts task *may* edit them; the conflict is that the comment is a claim about the whole
tree's dependency posture and the pin is the console boundary's own test, so a unilateral edit is a
change to somebody else's rule dressed as a build fix. *Consequence:* spec §3's pty path has a
**cross-work dependency (E5)** with a named owner, or it does not use the package (D862's alternative,
or D863's Windows refusal). Note also that the module itself is **already required**: no version and no
`require` line is added, so the brief's "no new dependency" rule holds — only the comment's claim moves.

**F16: Missing. No login runner, and no production use of the `claude` lookup.** `readHiddenLine` is
the tree's only terminal-input helper and it reads *Fugaro's* terminal; nothing in the tree allocates a
pty outside tests (`internal/cli/secrets_pty_test.go`, `internal/e2e/update_test.go`).
`claudeplugin.Find` — the absolute-path, `exec.ErrDot`-refusing lookup spec §3.1 reuses — is production
code today, reached by `internal/cli/upgrade_plugin.go`, so the lookup is a call and the *runner* is
new. *Consequence:* spec §3 is the only genuinely new mechanism in this design, and it is the only part
of the command that executes another program.

**F17: Ungoverned. The live check has nowhere to go yet.** `docs/gcp-live-checklist.md` is named in the
2026-10-30 rulings as another work stream's file, and its Check 35 (**U2**, `claude setup-token` and
multi-account switching) is "written and **NOT RUN**". *Consequence:* spec §9 is written where this
design can write it, and the implementing task moves it into the checklist under the number free at
that merge (D870). A derivation: **LC1–LC3 are answerable today** by the owner in ten minutes and would
settle the guard's floor, its alphabet and its line rule before anyone writes the guard.

**F18: Conflicts. The doc half is not four files, and nothing would go red if the converge half were
dropped.** [docs/project-claude-accounts.md](../project-claude-accounts.md) is pinned by
`internal/cli/docs_project_named_oauth_tokens_test.go` (seven `TestDocsProjectClaudeAccounts*` tests,
including `…CommandsExist`, which resolves every `fugaro …` line on the page against the real command
tree) and by `internal/cli/docs_project_policy_reseed_test.go` (which cites the page's §5). The
`claude setup-token`-then-`fugaro secrets set` pair also appears in `internal/cli/doctor.go`,
`internal/cli/doctor_render.go`, `internal/cli/initsecrets.go`, `internal/infra/repo.go`, two plugin
skills and four goldens. And **no test in the tree asserts that adding an account leaves the owner
nothing to run** — the HARD RULE's own half. *Consequence:* spec §8 is the file list the implementing
task works from, and the plan's A2 red step is an end-to-end assertion that `fugaro run --token <name>`
works with no second command, because nothing else would notice its absence.

**F19: Conflicts, resolved in this work's favour: spec §8 is wrong about `internal/cli/root.go`.** The
row there says the design "registers the two subcommands" and names `root.go`, which the 2026-10-30
rulings reserve for other work streams. It does not: the `accounts` parent already exists and owns its
own registration — `newAccountsCmd` ends in `cmd.AddCommand(newAccountsLsCmd(), newAccountsClearCmd())`
(`internal/cli/accounts.go`) — so `add` and `rm` register inside that same file and `root.go` is never
opened. *Consequence:* a reserved file leaves this work's list entirely, and the implementation plan's
task A1 gains a one-line edit to a file it already owns instead of a negotiation. Recorded here because
this document wins where it disagrees with [spec.md](spec.md).

## 3. Dependencies, and the order

| E | What it is | What this design needs from it | Order |
| --- | --- | --- | --- |
| **E1** | `internal/yamledit` — **#1467, merged (`802ae65eb`)** | The comment-preserving edit of `claude_accounts.<name>.allow_data_to` and `profiles.<p>.claude_accounts` (spec §2.3). If its API differs from the promise above, the accounts task adapts to it; if it does not merge, this work **may not write its own splice** — the settings design's shared-files rule — so A1 waits | E1 before A1 |
| **E2** | Settings **S3** — `fugaro update` publishes the policy node from the local config — **design merged as #1464; S3 unimplemented** | The publish half of spec §2.5 (F10), reaching the node through `policyMigrationFor` (F14). A2's own red step (`TestAccountsAddLeavesNothingToRun`) asserts that the published node carries `accounts.<name>.allow_repos` and the job set carries the account's job, and neither is reachable until the `Policy` stage publishes from the local config (F10). Starting A2 earlier leaves only two ways to make that test green — weaken it or skip it | E2 **before A2 starts** |
| **E3** | Settings **S5** — `internal/cli/settings_write.go` — **design merged as #1464; S5 unimplemented** (the file is absent from the tree, F13) | The diff, the confirmation, the agent refusal, `backupFile`, `writeFileAtomic`, in one copy (F13) | E3 before A1 |
| **E4** | The `claude` lookup and the pty decision | `claudeplugin.Find` is already production code (F16), so only the runner is new; the pty package's promotion is E5 | E4 with A0 |
| **E5** | `go.mod`'s `creack/pty` comment and `internal/cli/docs_console_boundary_test.go`'s `consoleGoModRequireBaseline` (F15) | Whoever promotes the package updates the comment and the pin in the same PR. Neither file is an accounts task's. If the owner answers D862 with the paste alternative, E5 disappears | E5 with A0, or never |

**#1463 (`docs/rigor-command`, D830–D839) is *not* a dependency.** Its `Layer` stage publishes the
project layer, and this work writes no project-layer key: `claude_accounts` and `profiles` are
local-config-only by construction (spec §7). The only thing the two designs share is E1, the editor.

## 4. Decisions D860–D872

**None of these is answered.** Each row's recommendation comes first, and the alternative's cost is the
reason to read the row rather than take the recommendation. The plan in
[implementation-plan.md](implementation-plan.md) is written to the recommendation until the owner
answers.

| D | Question | Recommendation | The alternative, and its cost |
| --- | --- | --- | --- |
| **D860** | Where does the deploy half live? | **The command invokes the update door's own stages in-process** (`initStages`: `Installation`, `Policy`, the running checkout's `Repository`), behind one confirmation, through the code `fugaro update` uses | (a) The command writes the config and the secret and ends with `next: fugaro update`: that is the second command the HARD RULE forbids, and the owner's seven steps become three rather than one. (b) Fork `fugaro update` as a child: it re-runs the whole door — images, security rules, plugin, checkout upgrade — under its **own** confirmation, and this command's output becomes a transcript of another command rather than its own report |
| **D861** | What is `allow_data_to` by default? | **The checkout the command runs in**, resolved as `fugaro secrets set` resolves it; `--repos` names others, and with no checkout the flag is required | Require `--repos` always: every owner retypes their own repository, and the common case (one repository, one account) loses its default for no safety — the field is printed before anything is written either way |
| **D862** | How does `--claude` capture the token? | **A pty child with a line-at-a-time copier and the token-run guard** (spec §3.2), with the hidden prompt always offered as a fallback (spec §3.3) | (a) Give the child the owner's own terminal and ask for one paste afterwards: safest, but it makes `--claude` two owner acts and lets the terminal show the token, which is the thing the brief asks to avoid. (b) Give it the terminal and afterwards read Claude Code's own stored credential: needs a path and a format Anthropic does not promise, and reads a plaintext credential file Fugaro has no business reading. The recommendation's cost is real: it proxies another program's terminal output, and its correctness rests on the unverified U2 — which is what LC1–LC4 exist for |
| **D863** | `--claude` on Windows | **Refused**, with a sentence naming the paste path and Anthropic's own command; the rest of `accounts add` works unchanged | Add a Windows pty (ConPTY) dependency: a new dependency, new build tags, and its own review, for a flag whose fallback is one paste |
| **D864** | Which profile gains the account? | **The only profile when there is exactly one; `--profile` otherwise, refusing and listing them.** With zero profiles, declare and store, and say in one line that no project job serves it | Never touch `profiles`: the owner's hand edit stays, so the brief's step (c) is dropped and the command is not one command. Taking the *first* profile silently would be worse: it puts a credential on a job the owner did not choose |
| **D865** | How far does the converge reach for `--repos`? | **The installation root always; the running checkout's repository root always; each named repository whose checkout is recorded on this machine through the re-converge the models design's D787 asks `fugaro update` to grow.** A named repository with no checkout here is named in the output, with what is left | Converge only the checkout the command runs in: a `--repos` repository whose own per-repository jobs also mount the named secret keeps a stale repository root until the owner runs `fugaro update --repo .` there — a second command, in a corner the flag created |
| **D866** | What does `accounts rm` do to the secret? | **Delete it**, through the store's own `Delete` (`internal/backend/gcp/secrets.go`), after refusing while a profile still lists the account; Secret Manager's own grace period is the recovery, and the secret's ID is named in the confirmation | (a) Leave the secret: a live credential nobody may read is still a live credential, and "rm" that leaves it is a lie the next audit finds. (b) Remove the IAM through Terraform only: the installation apply already removes the job and its account, so the grant goes — but the value stays. The recommendation's cost: no command reaches `Secrets.Delete` today, so `rm` is its first caller |
| **D867** | Does `accounts clear` fold into re-running `add`? | **No. `add` clears the mark it should clear** (a replaced token cannot still be `broken`), **and `clear` stays** for a token replaced another way and for the states `add` must not touch (a `backed_off` mark is a probe's, an unknown state is nobody's) | Fold `clear` away: a teammate's machine, or an owner whose rotation ran through `fugaro secrets set`, loses its only door, and `accounts add` would have to grow the state rules `clear` already carries |
| **D868** | Which confirmation? | **The printed diff plus a `y`; a widening of `allow_data_to` asks for the account's name typed**, following the settings design's sensitive-direction convention (its SD9) | `--yes` for everything: the one step whose whole meaning is "more repositories may reach this credential" would be settable by a script, which is precisely the flag a script sets |
| **D869** | `--json`, and non-interactive use | **No `--json`.** `--token-stdin` is the scripted value path, `accounts ls --json` is the machine-readable read, and the command is refused in a coding agent's session | A `--json` report of the write: a machine-readable document for a write a person is supposed to confirm, with no reader on the console or mobile side (neither reads the local config) |
| **D870** | Where does the live check live? | **Written in spec §9 as LC1–LC5, and moved into `docs/gcp-live-checklist.md` by the task that implements `--claude`**, under the number free at that merge, as Check 35's own row asks | Take a number in this design: reserving a number in a document for a file it may not write is exactly the collision the checklist's own paragraph records between two open plans, and a reservation is not a fact about the tree |
| **D871** | The login's output holds more than one distinct token-shaped run — what does the capture store? | **Nothing: two or more distinct candidates is a refusal**, and the command falls back to the hidden prompt (§3.3), saying how many it saw. One candidate is the common case; the rule costs a paste only where the guard is genuinely unsure | "The last one wins": the login's output is a stream nobody has seen, so the last run can be a progress line, the child's echo of something the owner pasted, or a decoy from a hostile binary (§6(c)) — and it becomes the stored credential with no way to tell. The first-wins spelling has the same hole with the opposite bias. Either way the failure is silent until a run uses the token |
| **D872** | A line containing `://` carries runs of 40+ characters from the token alphabet (the PKCE `code_challenge` is 43, `state` is another) — are they candidates? | **Never candidates, and nothing on such a line is withheld.** The cost is the opposite error: a token printed *on* a `://` line is not captured, and the command falls back to the paste with nothing stored. **LC1 settles the premise**: if it shows the token printed inside a URL, the exemption narrows to the login's own authorize URL rather than every `://` line | Offering them to the capture (the first draft of §3.2) means the command can **store the `code_challenge` as the account's token**: a value that is not a credential, that the store accepts, and that fails only when a run tries to use it — and the failure surfaces as an auth error on someone's run, not as a refusal at the terminal |

**D873–D879 are reserved and unused.**
