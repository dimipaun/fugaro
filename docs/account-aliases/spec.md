# Account aliases

## The ruling

The owner's ruling, as received (the only spec that exists):

> a stage's `claude_account` may name an alias, repointed by one command; repointing affects NEW launches only, never runs in flight (recommended).

Everything below makes that ruling concrete against the tree at `d51ada6d5`. Where a choice had more
than one defensible answer it is written here as the recommendation and left open in
[reconciliation.md](reconciliation.md) as a D-number; **where this file and the reconciliation
disagree, the reconciliation wins**.

## 1. What an alias is

An **alias** is a name the owner points at one named Claude account, so the keys that choose an
account can name the alias rather than the account, and moving every stage that names it to another
account takes one command rather than an edit per key.

```yaml
# ~/.config/fugaro/projects/<project>.yaml (the owner's local project config)
claude_accounts:
  team-a: {allow_data_to: [owner/repo-a]}
  team-b: {allow_data_to: [owner/repo-a]}
claude_account_aliases:
  reviewer: team-a          # alias -> account
stages:
  review: {claude_account: reviewer}
```

```console
$ fugaro accounts alias set reviewer team-b
alias     reviewer          team-a -> team-b
publish   policy node       claude_account_stages.review = team-b
deploy    jobs              no change (team-b's job exists)
New launches use team-b. Runs already launched keep the account they launched with.
```

## 2. Where it lives (D2070)

In the **owner's local project config only**, as a new block `claude_account_aliases`, beside
`claude_accounts`, `default_claude_account` and `stages` — the keys it serves, which are local-config
only for the reason [docs/accounts-add/spec.md](../accounts-add/spec.md) §7 gives: a reviewed
repository and a published layer are writable by someone other than the owner, and a key there would
be a way around an account's own `allow_data_to`.

The alias **never reaches the published policy node, the task or the run record by its own name**.
Every reader resolves it to the account before it writes anything: the node carries the resolved
account in `default_claude_account` and `claude_account_stages` exactly as it does today, so the
node's format, its version and every job image that reads it are unchanged.

## 3. Names (D2072) and targets (D2073)

- An alias's name follows the account name rule (`config.ClaudeAccountNameRE`, at most
  `config.MaxClaudeAccountNameLen` characters) and the credential name rule's reserved names
  (`default`, `vertex`, `config.AnthropicKeyAccount`, `config.ProviderKeyAccount`, `bitbucket-token`,
  a `-review` suffix).
- It shares **one namespace** with `claude_accounts`, `credentials` and `pools`: an alias may not
  carry a name any of them defines, so a name written in a choice key always means exactly one thing.
- Its target is **one account defined in `claude_accounts`**, or `default` for the unsuffixed account.
  Never another alias (no chains), never a pool, never a credential of another type, never the
  account axis's non-account values (`config.AxisNonAccountValue`).

## 4. Which keys may name it (D2071)

| Key | May name an alias |
| --- | --- |
| `stages.<stage>.claude_account` | yes (the ruling) |
| `default_claude_account` | yes (recommended: the same indirection, one rule for both choice keys) |
| `fugaro run --token NAME`, `fugaro review --token NAME` | yes (recommended: resolved at the launch like the keys) |
| `agent.oauth_token` (the committed `fugaro.yaml`) | no: a repository's file may not depend on an owner-local name |
| `profiles.<p>.claude_accounts`, `job_profiles.*.claude_accounts` | no: those lists decide which jobs exist and which tokens they mount |
| `crafts.<craft>.access` | not in this plan (D2080; after model-crafts MC5) |

## 5. Resolution, and what a run records

**The launch resolves; nothing after it does.** `claudeAccountFor` (`internal/cli/run.go`) resolves
an alias on every rung it reads (the flag, the stage key, the project default) **before**
`claudeAccountAllowed` holds the result to `claude_accounts`'s allow-list, so the allow-list is always
asked about the account the run will hold and never about the alias. The resolved account is what the
task carries (`task.Spec.OAuthToken`), what picks the job variant (the one job that mounts that
account's token) and what the run record names (`oauth_account`, `stage_accounts`).

A run in flight therefore never re-resolves: the job it runs in mounts the account the launch
resolved, and the runner reads the account choice once, at bootstrap, from the node it memoizes. The
one window the tree leaves open — a mixed `api-key` run launched before a repoint and bootstrapped
after the publish that carries it — is closed by the runner serving the run's mixed stages with the
account its own launch resolved (D2074).

**Follow-ups** keep the account their previous run resolved (D2078), as they do today
([docs/accounts-add/spec.md](../accounts-add/spec.md) §11.2); `--pr N --token ALIAS` takes the alias's
current target.

## 6. The one command (D2076, D2077)

```
fugaro accounts alias set ALIAS ACCOUNT   # create or repoint
fugaro accounts alias rm ALIAS            # refused while a choice key names it
```

`set` writes the local config through the one settings write path (`applySettingsEdit`), then runs
the three stages `fugaro accounts add` already runs (policy, installation, repository —
`accountsAddRunStages`), so the node carries the new resolved account and, for an `api-key` mix, the
new target's job variant exists before the command returns. It **adds** the new target's job; it
**never deletes** the old target's (D2075). It is refused in a coding agent's session, as
`accounts add` is, and asks no typed confirmation (the no-prompt policy, D1540).

`fugaro accounts rm ACCOUNT` is refused while an alias targets the account, naming the
`accounts alias set` that moves it.

## 7. What the reads show (D2079)

- `fugaro accounts ls` prints each alias and its target; `--json` gains an additive `aliases` member
  (alias to account), which the console's existing `accounts ls --json` read tolerates.
- `fugaro settings` (and `settings get`) shows the `claude_account_aliases.<alias>` rows the registry
  gains, owned by the `accounts` family. `fugaro config list` lists installations, not keys, and is
  unchanged.
- The launch's own line names `ALIAS -> ACCOUNT` when the account came through an alias.
- The run record names the **resolved** account only; no alias field is added.

## 8. Failover, pools and crafts (D2080)

- `claude_account_auto` walks accounts, never aliases: the walk's list is `accounts ls`'s accounts,
  and an alias is not a candidate.
- A pool may not list an alias and an alias may not target a pool: a pool is resolved in the run,
  which is the re-resolution the ruling forbids.
- When model-crafts moves the stage keys into `crafts.<craft>.access`, an alias becomes a name that
  access may hold, resolved at the launch on the same terms; that is a later task, held behind MC5.

## 9. Security reading

An alias **adds no reach**. Its target must be an account the owner already defined, every name is
checked against the target's own `allow_data_to` after resolution, the alias lives only where the
owner alone writes, and it never appears in a layer the policy merge (`internal/policy`) reads — so
the tighten-only merge never sees one and no repository can name one. A repoint moves which admitted
account a stage uses; it cannot make an account usable by a repository that account does not admit.
What it does change is which subscription a stage spends, which is why it is refused in a coding
agent's session.

## 10. `fugaro update`

Nothing to migrate: the block is new and optional, the node and the task keep their formats, and an
installation without aliases converges byte for byte. No operator step.
