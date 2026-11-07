# The handoffs: the three rows, and the paragraph the setup skill owes

**Status: written by M9, after the build landed. Two designs' rows have landed, one is owed, and the skill's paragraph is a handoff — this folder does not edit the file it belongs in.**

[implementation-plan.md](implementation-plan.md) §2 writes one row for each of the **settings**, **rigor** and **accounts** designs, so that design's owner copies its row rather than reconstructing it, and §4 keeps `plugin/**` and every other design's files out of this build's reach. This note is where those rows are recorded, where each command's state is read against the tree, and where the paragraph `plugin/skills/setup/reference/managed.md` owes is written down for its owner to place. **Nothing here is a decision**: the decisions are D1011–D1027 in [reconciliation.md](reconciliation.md) §3, [spec.md](spec.md) §3.1 is the set they name, and this note changes neither.

## The three rows

Each row is one design's whole handoff: what that design's own task adds to its command, and what stays refused in a managed project once it lands. A command whose row has not landed keeps the refusal it has today — the gate is a call the command makes, so a command that does not make it is a command an agent's session still cannot run, which is the safe direction rather than a hole.

| Design | Its command | What its task adds | What stays refused in a managed project, and who refuses it |
| --- | --- | --- | --- |
| **settings** | `fugaro settings set` / `fugaro settings unset` | The gate call, with **its registry** as the rule for a key, **its D854 generation guard** as the rule for a stale local copy, and its scope check for `--layer local` | The reserved keys (`credentials.*`, `clone_urls`, `budget.*`, `agent.oauth_token`, `governance.*`); a **floor removed or a ceiling widened**, which keeps its typed project name (the gate never admits a `Typed` class, D1026); a repository-scoped `set`, which is allowed today and is **not** refused here |
| **rigor** | `fugaro rigor set` / `fugaro rigor unset` | The gate call, with **its rank rule** (at or above the resolved mode), **D834's hotfix refusal** and **D838's glob rule** as the rules, and R1's layer publish as the same run's write | `hotfix` in any of its four forms (D834, unchanged, naming `fugaro run --mode hotfix`); a duplicate area glob (D838); the missing base-branch opt-in the design's R7 owes, **which this plan does not close and must not be read as closing** |
| **accounts** | `fugaro accounts add` / `fugaro accounts rm` | The gate call, with **its reserved names** (`reservedClaudeAccountName`, and `default`'s own rule), its profile/reference coverage check, and its `allow_data_to` rule as the rules | `add` with a widening `allow_data_to` — **D868's "`--yes` covers everything" is a widening inside that design and is still refused in a coding agent's session**; `rm` while a profile or repository still references the account; **any token entry at all**, which is §3.2's never-set and no gate admits |

## What the tree says today

One line per command of the spec's §3.1, each read against the command tree — a row that landed and whose command is not in the gate's set, and a row that has not landed and whose command a session can already run, are the two mismatches worth a report. M9 read the tree rather than trusting the plan's order, and **nothing below is a mismatch**: the three rows are owed by their own designs, not by this build.

- `fugaro update` — **landed** (M1, `internal/cli/update_managed.go`): each stage's saved plan, and each non-Terraform stage's own dry-run document.
- `fugaro config publish` — **landed** (M2, `internal/cli/config_publish_managed.go`).
- `fugaro project policy publish` — **landed** (M3, `internal/cli/project_policy_managed.go`).
- `fugaro image refresh` — **landed** (M4, `internal/cli/image_refresh_managed.go`), with the recorded build bound of D1013.
- `fugaro settings set` — **landed** (`internal/cli/settings_managed.go`), the settings row's own build.
- `fugaro rigor set` — **owed**: no `fugaro rigor` command exists on `main`, so an agent's session is refused there exactly as it is today.
- `fugaro accounts add` — **landed** (`internal/cli/accounts_managed.go`), the accounts row's own build.
- `fugaro accounts rm` — **landed** (`internal/cli/accounts_rm.go`), the accounts row's inverse: it keeps the refusal its row above writes, and the same gate call admits it in a managed project's session **only when no profile and no repository still references the account** — the referrers are read rather than assumed (each profile's `claude_accounts`, and each enrolled repository's committed `agent.oauth_token`, read out of the commit a run of that repository checks out and never out of the working tree, which a session owns and may have edited without committing — and at a ref the installation config names and never one the checkout's own file names, since a session that moves that file can point the read at a ref this checkout does not hold, while the branch the committed file itself names is followed; a checkout whose committed file does not resolve, or whose ref is not held here, is an unresolved value and refuses the session, §4.1, while a repository with no checkout here stays the warning the owner's own run prints, D783). A session with no admission reads nothing, writes nothing and deletes nothing.

The handoff rows that landed — settings and accounts `add` in the 0.18.0 build, before this design's own M9, then `accounts rm` with the accounts design's own A4 — did so in their own designs; the rigor row is the one command of §3.1 a release of this gate still leaves refused, and a reader who finds it missing has found the state the plan's §2 predicted rather than a regression. **A row that lands later moves its line above in the same pull request**: the note is a claim about the tree, and the folder's pin (`internal/cli/docs_managed_agent_converge_test.go`) reads the two against each other.

## The skill's paragraph (a handoff)

`plugin/skills/setup/reference/managed.md` is where a user's agent reads what a managed project is, and it says — correctly — **never write that label yourself**. It is a Fugaro-owned skill file, shipped and replaced by the plugin, so this design writes the paragraph it owes **here**, for the plugin's own owner to place and pin at whatever release the plugin takes. Its subject is the one thing the file cannot say today, because the gate did not exist when it was written.

> **What you may run in a managed project (0.18.0 and later).** When both marks are present, the commands a managed project needs — `fugaro update`, `fugaro config publish`, `fugaro project policy publish`, `fugaro image refresh`, the settings and rigor `set` / `unset` and `accounts add` / `rm` — are yours to run with no typed confirmation: each is judged by a deterministic gate over the plan it has already produced, and no one asks a model whether the change is acceptable. Every one of those runs is recorded, an admitted plan and a refused one alike, and `fugaro doctor` shows the last of them. **Nothing else changes**: `fugaro managed flag` and `fugaro managed unflag`, `fugaro setup`, `fugaro creds set` and `fugaro secrets set` and every token entry, `fugaro allow-delete`, `fugaro restore`, and anything that writes the label stay the user's own. A command the gate refuses names the rule that refused it and the one command the user runs at their own terminal: read that refusal to them, verbatim, and stop — never look for a flag, a variable or a marker that reaches past it, because there is none.

The paragraph is handed over in this shape on purpose: it names the commands and the refusals, and it invents no rule of its own. A plugin owner who places it should keep the two halves together — the admission and the never-list — because a paragraph that carried only the first would read as a licence.

## What this note does not do

- **It does not edit another design's file.** The three rows belong to `docs/settings-commands/`, `docs/rigor-command/` and `docs/accounts-add/`, and each of those designs writes its own gate call, in its own task, with its own rules. Where this note names a rule, it names it as that design wrote it.
- **It does not close the rigor row** or the missing base-branch opt-in R7 owes, and it does not decide anything about them.
- **It does not touch `plugin/**`**, which the 2026-10-30 rulings reserve: the paragraph above is text for its owner, and the file it belongs in is unchanged by this folder.
