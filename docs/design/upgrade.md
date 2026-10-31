# `fugaro upgrade` and `/fugaro:upgrade`

Status: design approved by the owner (2026-10-08). The details below were settled by reading the code, and each one is a decision the owner can veto in the plan ([plans/2026-10-08-upgrade.md](../plans/2026-10-08-upgrade.md)). Delivery: release **0.5.2**, together with `fugaro image refresh --yes` (PR #190). This work starts only after that PR has merged.

## Problem

After `brew upgrade dimipaun/tap/fugaro`, every checkout needs four separate actions, in two different tools:
- `fugaro image refresh --yes`, in the user's own terminal;
- `fugaro update-skills`;
- in Claude Code, `/plugin marketplace add dimipaun/fugaro`;
- in Claude Code, `/plugin install fugaro@fugaro` (or `/plugin marketplace update fugaro` when it is already installed).

Each of these sequences is repeated in the README, `docs/gcp-setup.md`, `docs/release.md`, the `/new-release` operator list, `fugaro doctor`'s fixes, the staleness warning, and the text of init's plugin stage. The owner wants one command, and a skill that knows how to do all of it.

## Behaviour

```
fugaro upgrade [PATH...] [--check | --local | --yes] [--allow-fork]
               [--repo R] [--workflow W]... [--image-source S] [--expect-digest KIND=sha256:H]...
               [--config F] [--project P] [--gcp-project G] [--region R]
```

The first line names the binary (`fugaro upgrade with fugaro 0.5.2`) and says the command never upgrades `fugaro` itself. **The upgrade sequence is `brew upgrade dimipaun/tap/fugaro && fugaro upgrade`.**

Each PATH (the default is `.`) is a checkout, and each is processed on its own. Two paths in the same checkout count once. Each checkout runs five steps in order and stops at the first step that fails:

1. **pin**: what `fugaro update-skills` does. The plugin's marketplace and enablement are merged into `.claude/settings.json`, pinned to this binary's tag. The diff is shown, and the command never commits. A fork's marketplace moves only with `--allow-fork`, and a file that is not valid JSON is never rewritten. One rule is new: a pin **newer** than this binary is never moved down. Instead, that checkout stops and the output names `brew upgrade`. This matters because `update-skills` itself does lower such a pin today, even though its own fix text says it never does.
2. **plugin**: the plugin is installed or updated in Claude Code through the `claude` CLI on `PATH`:
   - It reads `claude plugin marketplace list --json` and `claude plugin list --json`.
   - It adds the marketplace if it is missing, otherwise it updates it.
   - It installs the plugin at user scope if nothing applies to this checkout. Otherwise it updates each install that applies (user scope, or project or local scope recorded for this checkout), at that install's own scope.
   - When everything is current, it makes no change call.

   It then lists the installs again and compares the result with this binary. The output says that running Claude Code sessions must be restarted to apply it. Without `claude` on `PATH`, the step prints the slash commands to type in Claude Code instead.
3. **cloud**: `fugaro image refresh` for the checkout's repository. The refresh runs in the same process, through `runImageRefresh`; it is not a shell-out. `--yes` passes through, and without it the refresh is interactive as today.

   The step is **skipped**, with the reason shown, when:
   - there is no `fugaro.yaml`;
   - there is no local project config (a teammate's machine);
   - the repository is not onboarded;
   - the binary is a development build.
4. **rules**: the security rules generation a project policy is read under (update-converge UC3c). The generation this binary carries is deployed into the project's Firebase database by the same read, classification, plan and apply `fugaro update`'s rules stage uses — one owner of the question, so the two doors cannot converge into different states. The plan is printed and the project's name is typed (or `--yes`), as the policy step asks. It runs **before** the policy step because the policy write is gated on the deployed generation (`requirePolicyRules`): a rules step after it would deploy too late for the migration the same run had already refused, which is the state this step exists for, and the rule of 2026-10-30 that one `fugaro update` is the whole upgrade. It is skipped where there is no local project config, where the repository is not onboarded, where the config names no shared budget backend, where the database is one this installation may not write (`budgetDBUnadopted`, the one ownership question every writer into it asks), where the deployed marker is newer than this binary or hand edited, where the rules or their marker could not be read, in a coding agent's session, and under `--check` (see below).
5. **policy**: the project's published policy node, when an earlier `fugaro` wrote it in a version this binary does not read (update-converge UC2, `docs/policy-migration.md`). The node is migrated **in place** with `projectpolicy.Migrate`, with the members a publish derives from the local config (`bitbucket_token`, `bitbucket_api_roots`) patched in first, and written back under the ETag it was read at. One diff is printed, then the project's name is typed (or `--yes`), exactly as `fugaro project policy publish --delete` asks for a removal. It is after the rules step, which deploys the generation its write is gated on, and it is last: the cloud step's own verdicts — a real local-file error stops the checkout in every mode, `--check` included, and a checkout with nothing to refresh is skipped — are read first. It is skipped where there is no local project config, where the repository is not onboarded, where the config names no budget backend (there is no node to read), in a coding agent's session, and under `--check` (see below).

At the end, a summary gives one line per checkout, with each step's state (`current`, `done`, `stale`, `skipped`, `failed`, `not run`) and, for a checkout that stopped, the exact line to rerun. The exit code is the worst over all checkouts: 0 when everything finished or was current, 1 for a refusal, a failed local step or (with `--check`) anything stale, and 2 for a cloud failure. When a rerun finds everything current, it prints `nothing to do` and changes nothing.

**`--local`** runs pin and plugin only. **`--check`** writes nothing, runs no `claude` command and makes no cloud call. It reads three things:
- the settings file;
- Claude Code's `installed_plugins.json`;
- the local config's `base_images`.

It prints what each step would do and exits 1 if anything is stale, so CI can run it without credentials. It cannot see a build that failed after the base moved, because build records live in the cloud; the output says so, and the rules and policy steps say the same about the deployed rules generation and the published node: reading either needs credentials, so `--check` skips both and says it cannot tell what they are. Without `--yes`, `--local` or `--check`, the command needs a real terminal and refuses before any step runs.

**Inside a coding agent's session** (`CLAUDECODE` and the like), only the local steps run. The cloud, rules and policy steps are skipped and each prints the exact command to run in the user's own terminal, with and without `--yes`. The exit code is 0 when the local steps succeeded: the command did everything it may do there, and the skipped steps are named. `--check` reports the cloud state the same way everywhere, so CI and the skill still see `stale`. `fugaro upgrade` never even reads the cloud in that session, and the refusal inside `image refresh` stays as a backstop.

**`fugaro update` in that session is the other front door, and it stops sooner** (UC3c-1b, PR #1453). Its plugin stage runs before the checkout and is not local under the marker, so it returns **needs-you** with `fugaro update-skills` left for the user: a stale plugin pin is **not** moved there, and neither is anything the checkout stage would have done — the stage behind it waits. The run exits **1** with a `left_for_you` entry for the checkout on **every** run in a session, naming the two commands to run in the user's own terminal, `fugaro upgrade --yes <root>` and `fugaro upgrade <root>`. That exit is expected and not a failure: the run did everything a session may do, and one command in the operator's own terminal finishes it.

**The skill `/fugaro:upgrade`** works through the upgrade in this order:
1. It runs `fugaro version` and `fugaro upgrade --check`, then explains in plain words what is stale and why.
2. It runs `fugaro upgrade --local`, shows the diff to commit, and says a restart is needed.
3. It checks again with `fugaro upgrade --check`.
4. It hands the user one command for their own terminal, in a `user-runs` block: `fugaro upgrade --yes`. It says why: a coding agent's session cannot apply cloud changes, by design.

It never runs the cloud step or any `claude plugin` command itself, never edits settings by hand, never passes `--allow-fork`, and never asks for or handles a secret.

## `fugaro update` and a Firebase project that is not the installation's

`fugaro update` is the whole upgrade, and it works without a second command: the one exception is an installation whose `budget.firebase_project` names a project other than its own GCP project. `--firebase-project` takes **only the installation's own GCP project** (`checkFirebaseProject`, the owner's ruling of 2026-10-23: the budget backend goes in the project `fugaro setup` created for the installation and nowhere else), so a config recording another project is one no door adopts — and the config's `budget.firebase_project` alone never proves anything happened: a name in a config is a name someone typed, and a typo that agrees with itself (`budget.firebase_project: other` beside `other-default-rtdb`) leaves a pair that looks right.

So the run asks the **database**, not the config. Every stage that writes into the budget database — the security rules generation, and the policy migration after it, which reads the published node, takes the ETag its write is conditional on and writes the node back — asks **one** predicate (`budgetDBUnadopted`) before it reads or writes anything, in two halves and in this order. The first half is the config's own facts, which no read of the database can settle: no `budget.firebase_project` recorded at all, or a `budget.rtdb_url` that is not that Firebase project's own database (`rulesNoBudget`, `rulesForeignDatabase`). It is answered without opening the database, so a URL that is not the project's own is never contacted. The second half is the mark itself, `infra.DB.Check`, asked of **every** shape and not only of a config naming a separate project: an empty database passes that check whatever project its URL belongs to (which is how a config with a self-consistent typo would otherwise have marked and deployed into a database nobody adopted), a database another installation marked is not this one's, and one that holds data and no mark is not ours either. So a database cannot come out safe for the rules stage and unsafe for the policy migration: the rules stage being skipped is not a reason for the next writer to go on — `fugaro upgrade`'s rules step asks the same predicate (through the same `rulesState`) and the policy step behind it asks it again, so one door cannot come out writable where the other was refused. `fugaro update --firebase-project` refuses a config whose recorded project differs from its own (`checkFirebaseProject`) before any of this, which is what the way out below is for.

It is a **skip, not a refusal**: the installation's other stages (the images, the pin, the plugin) have nothing to do with that database, so `fugaro update` still does everything else, and the skip names the way out — drop the config's `budget.firebase_project` (and the `budget.rtdb_url` beside it, which is that project's database), then run:

```
fugaro update --firebase-project <gcp-project-id>
```

That door adopts the installation's own Firebase project, writes its mark into that project's database, and plain `fugaro update` converges there from then on — the adoption is not a step that repeats. The ordinary installation (`budget.firebase_project` equal to `gcp_project`) is untouched by any of this.

**Every writer into that database, and the gate it holds.** `fugaro update` is not the only command that writes there — the history job and the project commands do too — so the audit is the list, not the story of one of them. It was taken for this round, against the code at the commit that carries this text:

- the **`fugaro update` rules stage** (`init_rules_stage.go`, which applies the plan): `budgetDBUnadopted` — the config's own facts, then the mark of every shape. The `fugaro upgrade` rules step asks the same read through `rulesState`, so there is one classification and one gate.
- the **`fugaro update` policy stage** (`init_policy_migrate.go`, the node's `PutIfMatch`): `budgetDBUnadopted`, the same call. This is the gate F1's finding was about; before it, this stage held a weaker one than the rules stage.
- **`fugaro upgrade`'s rules step and policy step**: `rulesState` and the same `policyMigrationFor`, so both calls are the update door's own — and each asks the predicate rather than trusting the step before it: `fugaro upgrade`'s own rules step is skipped by the same answer, and the policy step behind it re-asks instead of reading that skip as a licence.
- the **history job**, both modes: `fugaro budget history --rollover` moves finished days out and then **deletes** their nodes, and the sweep moves them; both call `budgetDBUnadopted` (`history_rollover.go`, `history.go`), the rollover refusing with `nothing was moved` and the sweep with `nothing was swept`. They refuse rather than skip, unlike the `fugaro update` stages: each is the whole of what that job does. Each reads its environment's two names (Terraform sets `FUGARO_FIREBASE_PROJECT` and `FUGARO_GCP_PROJECT`, `history.tf`) and asks the mark whatever they are — not only where they differ, which is what the rollover used to do.
- **`fugaro update --firebase-project`** (`init_firebase.go`, which writes the mark and the project's name): the flag's own check (`checkFirebaseProject`). It is not a gate on a database that may be someone else's: this is the door whose subject is the adoption itself.
- **`project policy publish`, `project policy delete`** and the editors of the same node (`project policy repos`, `project enroll`, `user add`, `project_cmd.go`, `project_enroll.go`): `checkDBProject` — the name at `/fugaro/project` — and the deployed rules-generation marker (`requirePolicyRules`), and **no** mark check.
- **`fugaro budget set`** and **`fugaro budget kill`** (`budget.go`, `kill.go`): `checkDBProject`, the same name check.
- **`accounts clear`** (`accounts.go`, the credential's mark): `checkDBProject`, the same name check.
- **`pair`** (`pair.go`): `checkDBProject`, the same name check.
- the **coordinator's sweep** (`ee/coordinator/cli/coordinator.go`): the name at `/fugaro/project`, read directly.
- the **credentials service** (`internal/credsvc`): `checkDatabaseHost` — the URL is the Firebase project's own database — and nothing about the mark.
- the **gateway and the runner session** inside a job: nothing. The URL and the credential are the job's environment, which Terraform set.

The paths whose gate is `checkDBProject` rather than the mark are **PRE-EXISTING** — the name check is the one `fugaro budget set` has always made, and this round changed none of them. They run under the person's own credentials: an operator's identity, an operator-typed confirmation, the person's own `fugaro` login. A shared ownership check for them — the same predicate, in the same place — is the owner's **RULING of 2026-10-31**: **ONE shared ownership check covers every config-driven budget-database write path**, as a CLI 0.15.0 task. It is a behaviour change on commands that converge nothing, so it is not part of the fix this document follows; what this round did is make the paths that *do* write on the strength of the local config — `fugaro update`'s rules and policy stages, `fugaro upgrade`'s rules and policy steps — ask the one predicate, all four through the same function.

## Safety

- **The `claude` calls.** Every argv is one of seven fixed lists, or the marketplace add, whose one variable word is the marketplace repository: `dimipaun/fugaro`, or a fork that the checkout's pin names, passed only with `--allow-fork` and only after it matches a GitHub `owner/name` pattern that cannot start with `-`.

  An allowlist (`claudeplugin.Allowed`) rejects anything else before anything is executed. There is no shell, and stdin is the null device, so a prompt reads end of file and refuses. The command never passes `-y`, `--accept-command` or `--dangerously-skip-permissions`. Each call has a timeout: 30 s for a list, 3 min for a change.
- **Finding `claude`.** It is found with `exec.LookPath`, and a relative result (for example from a `.` entry in `PATH`) is refused. The resolved absolute path is printed before the first change.
- **Output.** At most 40 lines of `claude`'s output are printed, each one passed through `pluginwire.Printable`.
- **The marketplace already on the machine.** A `fugaro` marketplace on this machine that names another source is never replaced. The checkout stops and the output names `/plugin marketplace remove fugaro`.
- **Writes by Claude Code itself.** If a `claude` call changes `.claude/settings.json`, the step says so and asks the user to review it with `git diff`.
- **Credentials.** `--check` and `--local` need no credentials and make no cloud call. The tests pin this with a credentials path that does not exist, and with a build-record reader that fails the test if anything calls it.

## Non-goals

- Upgrading the binary.
- Pinning Claude Code's own marketplace to the tag: `claude plugin marketplace add` takes no ref, so it follows `dimipaun/fugaro`'s default branch. The release PR bumps the plugin version there before the tag, so it equals the latest release.
- `--json` (refresh has none either).
- Teammates' cloud steps.
- Changing `update-skills` or the `fugaro image refresh` hints that name a single workflow.
- A `fugaro.yaml` migration.

## Decisions (each one can be vetoed in the plan)

The plan lists U1 to U18. The ones that shape the behaviour:
- install at user scope, and update every install at its own scope (U2);
- the marketplace follows the default branch, and a plugin newer than the binary after the update is a note, not a failure (U3);
- `--check` reads local files only (U6);
- in an agent's session the command exits 0 (U7);
- a terminal is refused up front without `--yes` (U8);
- a teammate's checkout skips the cloud step (U9);
- the pin is never lowered (U10);
- the skill uses no `allowed-tools` frontmatter, because the lint allows only `name` and `description`, and `allowed-tools` pre-approves tools rather than restricting them; the lint gains one exception, `--yes` on `fugaro upgrade` inside a `user-runs` block (U14);
- every hint names `fugaro upgrade --local` (U15).

## Delivery

Release **0.5.2**, with `refresh --yes`. One PR holds Tasks 1 to 11 of the plan. Task 12 writes `docs/releases/v0.5.2.md` and cuts the release through `/new-release 0.5.2`. The new skill ships automatically: `scripts/release.sh` sets every skill header and `plugin.json` to 0.5.2 (`scripts/bump-plugin-version.sh`), and the plugin lint covers `plugin/skills/upgrade/` because it walks every skill.

The first upgrade *into* 0.5.2 needs the CLI command, because the 0.5.1 plugin has no `upgrade` skill. From then on, `/fugaro:upgrade` covers the local half.

What has not been verified live:
- the change calls of `claude plugin` (only `marketplace list --json` was run inside a session, on 2026-10-08);
- whether `claude plugin update --scope project` rewrites the committed settings file.

`docs/gcp-live-checklist.md` gets Check 31 for both.
