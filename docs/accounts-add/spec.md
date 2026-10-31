# `fugaro accounts add`: the spec

Read [README.md](README.md) first for the owner's brief and the block this work holds. Where this file
disagrees with [reconciliation.md](reconciliation.md), the reconciliation wins.

## 1. The rule this serves

`CLAUDE.md`'s HARD RULE: **one command, `fugaro update`, is the whole upgrade, and it works perfectly.**
A release must not ship an operator step that is another command, a deletion or a hand edit.

[docs/project-claude-accounts.md](../project-claude-accounts.md) is the page that rule does not yet
cover. Its own happy path for one new account is **two hand edits and five commands** — the owner's
seven steps:

| # | The page's step | Where |
| --- | --- | --- |
| 1 | hand-edit `claude_accounts.<name>.allow_data_to` | §3 |
| 2 | hand-edit `profiles.<p>.claude_accounts` | §3 |
| 3 | `claude setup-token` | §2.1 |
| 4 | `fugaro secrets set claude-oauth-token-<name>` | §2.2 |
| 5 | `fugaro project policy publish` (derives `accounts.<name>.allow_repos`) | §4 |
| 6 | `fugaro update --firebase-project <gcp-project-id>` (deploys the job) | §5 |
| 7 | `fugaro accounts ls` (the read-back) | §6 |

Steps 1, 2, 5 and 6 are the four the owner called out: two files edited by hand, and two commands that
are not `fugaro update`. Steps 3 and 4 are Anthropic's login and Fugaro's own store, which the owner
wants **merged into one act** ("a `--claude` flag that runs `claude setup-token`, captures the token and
sets it"). **The answer is §3, and it is not a capture:** `--claude` is two acts — the login in the
owner's own terminal, then one hidden paste (D862, answered 2026-10-31).

Everything below replaces those seven with one invocation.

## 2. `fugaro accounts add <name>`

```
fugaro accounts add <name> [--repos owner/a,owner/b] [--profile NAME] [--claude] [--token-stdin] [--yes]
fugaro accounts rm <name>
```

`<name>` is a Claude account name: `config.ClaudeAccountNameRE` (`internal/config/claude_accounts.go`),
never `bitbucket-token`, and never `default` — but not for the same reason, and the difference is in the
code:

- **`bitbucket-token`** is refused by `reservedClaudeAccountName` (the same file, `name ==
  config.BitbucketSecret`), and `ValidClaudeAccountRef` refuses it too, with
  `reservedAccountNameMessage`: it is a name the bundle already holds a credential under, so an account
  of that name would leave one name meaning two credentials.
- **`default` is a legal *reference* and an illegal *definition*.** `ValidClaudeAccountRef` returns `""`
  for it — a profile may list `default`, and should: it is the unsuffixed `claude-oauth-token` every
  repository already has — while `ValidateClaudeAccounts` refuses it as a key of `claude_accounts`
  (`"default" is reserved for the unnamed claude-oauth-token secret and may not be configured here`).

`accounts add` **defines** an account, so it refuses `default` with `ValidateClaudeAccounts`'s own
sentence and refuses `bitbucket-token` through `ValidClaudeAccountRef`, whose shape half
(`ClaudeAccountNameRE`, `MaxClaudeAccountNameLen`) it reuses. It does not use
`ValidClaudeAccountRef` alone: that function's answer for `default` is the one case where the reference
and the definition differ, and this command is the definition.

### 2.1 The flags

- **`--repos`** — the repositories the account's token may be used by
  (`claude_accounts.<name>.allow_data_to`). Default: **the checkout the command runs in**, resolved the
  way `fugaro secrets set` resolves it (`resolveSecretRepo`, `internal/cli/secrets.go`). With no
  checkout, no default and the flag is required: a token whose repositories are undecidable is one no
  policy can bound, which is exactly what `projectWideRefusal` refuses today.
- **`--profile`** — the profile whose `claude_accounts` gains the name. Default: the **only** profile in
  the local config, when there is exactly one. Two or more profiles without the flag is a refusal that
  lists them (`config.JobProfile`, the type in `internal/config/config.go`; the `Profiles
  map[string]config.JobProfile` field it reads is in `internal/localcfg/localcfg.go`). Zero profiles is
  not a refusal: the
  account is declared and its token stored, and the command says in one line that no project job serves
  it (a per-repository job that selects it by `agent.oauth_token` is served by its own repository's
  root, §2.5).
- **`--claude`** — run Anthropic's own `claude setup-token` login in the owner's own terminal first,
  and then take the token from the hidden paste (§3). It changes nothing about how the token is read:
  the hidden prompt is still the only source.
- **`--token-stdin`** — take the token from stdin, exactly as `fugaro secrets set <name>` does
  (`readSecret`, `internal/cli/secrets.go`). It exists for the owner's own scripts; it is never a value
  on the command line, and the flag carries no value.
- **`--yes`** — answer the one confirmation for everything the command is about to write (§2.6),
  including a widening of `allow_data_to`. It does not apply in a coding agent's session: that refusal
  stands, and the command never works around it (§2.4).
- There is **no `--json`**: the command's whole output is a diff a person reads at a terminal, and it is
  refused where no person is (§2.4). The machine-readable surface stays `fugaro accounts ls --json`
  (`accountsListDoc`, `internal/cli/accounts.go`).
- There is **no flag that carries a token value**, in either spelling: `storeSecret`'s rule that a value
  comes only from stdin or a hidden prompt is the one this command keeps
  (`internal/cli/secrets.go`).

### 2.2 The order of the work

The order is forced by the checks that already exist, and each step is named so a failure says where it
stopped:

1. **Validate.** The name's shape and the two reserved names; the local config's project selection; the
   profile (or the refusal that lists them); the repository list (the flag, or the checkout's origin);
   and whether `claude_accounts.<name>` already exists (that is a **rotation**, and the command says so
   before it reads a value).
2. **The subset rule, before anything is written.** Every account the profile lists must cover every
   repository in `profiles.<p>.allow_repos` (`ValidateProfileClaudeAccounts`,
   `internal/config/claude_accounts.go`; the rule the page's §4 states). The command computes the
   post-edit config and refuses **before the local file is touched** when the new account does not cover
   the profile's list, naming each repository the account is missing and the `--repos` that would
   include it. The alternative — widening the account silently — is the one thing the subset rule
   exists to prevent.
3. **Read the token.** The hidden prompt, or stdin. With `--claude`, act one runs the login first, in
   the owner's own terminal, and this step is the one paste that follows it (§3) — the login's output
   is never a source. Nothing is written before this succeeds.
4. **Write the local config.** `claude_accounts.<name>.allow_data_to`, then the profile's
   `claude_accounts` list, through the shared comment-preserving editor (§2.3). The old file is backed
   up and the new one written atomically, through the update package's own helpers (§2.6).
5. **Store the secret** as a new version of `claude-oauth-token-<name>`, **project-wide** — the
   project's own Secret Manager, the only scope a shared job can mount — through `secretsSetProjectWide`
   and `storeSecret`, with `clear(value)` on the buffer afterwards
   (`internal/cli/secrets.go`). It runs after step 4 because `projectWideAccepted` refuses a named
   account whose `allow_data_to` lists no repository: the field has to exist first, which is also why
   today's owner must hand-edit before storing.
6. **Clear the account's mark.** A mark of `broken` is what a 401 leaves behind (D139,
   `internal/creds`), and the operator's own remedy has always been "replace the token, then clear the
   mark" (`newAccountsClearCmd`'s own `Long`, `internal/cli/accounts.go`). `add` has just replaced the
   token, so clearing is part of it. A mark this Fugaro does not know, and a `backed_off` mark, are
   **not** cleared: for those the command prints `accounts clear`'s own sentence and leaves the mark
   alone, because clearing them by hand is the decision that sentence refuses.
7. **Publish and deploy** (§2.5).
8. **Read back.** `fugaro accounts ls --repo <the first named repository>` runs in-process and its
   answer is printed, so the command's own last line is evidence rather than a claim.

A failure at step 4 or later leaves a state the command's own re-run repairs: re-running `add` for the
same name replaces the token, rewrites the same two lines (an edit that changes nothing writes nothing),
and runs the converge again.

### 2.3 The local config edit

The two keys are edited through the **shared comment-preserving editor**, never by marshalling
`localcfg.Config`: `yaml.Marshal` drops the file's comments, reorders its keys, and turns a
commented-out `allow_data_to` into an absent one (§F9 in [reconciliation.md](reconciliation.md)). The
editor is `internal/yamledit` (PR **#1467**, merged), whose `SetScalar`, `AddListItem` and
`UpsertSeqItemByKey` are the operations this needs; the settings design (#1464) makes the same package
the tree's one editor, and its own rule — a second hand-written splice fails the review, not a test —
binds this work too.

The edit is *the two keys only*. Everything else in the file — the other accounts, the other profiles,
`project_secrets`, comments, key order, quoting, line endings — is byte-identical afterwards, and the
command re-parses the result strictly (`localcfg.Parse`) before writing, so a file this Fugaro cannot
read back is never written.

### 2.4 Refusals

| Refusal | Sentence (shape) | Where the rule comes from |
| --- | --- | --- |
| a coding agent's session | names the marker, says the owner runs it in a normal terminal, and writes nothing — `--yes` included | `agentenv.Marker` as `agentEnv` (`internal/cli/initsecrets.go`); the models design's D789 and the settings design's SD8 are the precedent |
| `bitbucket-token` | `reservedAccountNameMessage`, naming the fix (a different name) | `ValidClaudeAccountRef` → `reservedClaudeAccountName`, `internal/config/claude_accounts.go` |
| `default` | `"default" is reserved for the unnamed claude-oauth-token secret and may not be configured here` — the definition's rule, not the reference's: a profile may list `default`, `claude_accounts` may not define it | `ValidateClaudeAccounts`, same file |
| a name that does not match the pattern | the pattern, never the name | `ValidClaudeAccountRef` |
| no checkout and no `--repos` | names `--repos` and the field it fills | `projectWideRefusal`'s posture |
| two or more profiles and no `--profile` | lists the profiles | `config.JobProfile` |
| the subset rule | names each repository the account is missing, and the `--repos` that would include it | `ValidateProfileClaudeAccounts` |
| a value that is empty, too short to redact, over 64 KiB, or multi-line | `validateSecret`'s own sentences | `internal/cli/secrets.go` |
| no published policy node to write | a skip with a sentence, never an error: a project with no budget backend is a state the command must not stop on | `policyNodeUnreachable` |

The command **never works around the agent refusal**: it does not unset `CLAUDECODE` or any other
marker, and its message is what tells the owner where to run it.

### 2.5 The publish and the deploy

This is the half that makes the command one command, and it is `fugaro update`'s own work:

| Work | The stage it is | Today's door |
| --- | --- | --- |
| re-derive the policy node from the local config — `profiles`, the derived `accounts` block, the per-repository admissions — and write it under the ETag it read | the `Policy` stage (`initflow.Policy`, `internal/cli/init_policy_migrate.go`) **once the settings design's S3 lands**, which is what turns that stage from a migration into a publish | `fugaro project policy publish` |
| one Cloud Run job and one job service account per `(profile, provider, account)`, with `secretAccessor` on that account's secret and no other account's | the `Installation` stage through `infra.ProjectJobs` (`internal/infra/projectjobs.go`), the one place that computes the job set with `config.ProjectJobAccounts` | `fugaro update`'s installation apply |
| the running checkout's own repository root, whose workflows mount the named secret when they select it | the `Repository` stage (`e.repo`, `internal/cli/init_stages.go`) | `fugaro update` in that checkout |

`fugaro accounts add` **invokes those stages in-process**, through the registered stage list
(`initStages`, `internal/cli/init_stages.go`), behind **one** confirmation that shows the diff of what
each will do. It does not fork `fugaro update` as a child: a child would re-run the door's whole list —
images, security rules, plugin, checkout upgrade — behind its own confirmation, and would make this
command's output a transcript of another command rather than its own.

Three consequences the implementation must hold:

- **The publish reaches the node through `policyMigrationFor`** — one gate read, one ETag, one write
  path — so it holds #1460's adoption gate (`budgetDBUnadopted`) and the settings design's
  `TestUpdatePolicyPublishSkipsTheDatabaseTheRulesStageSkips` — a test that comes with that stage work
  and is not on `main` yet. A second writer with its own weaker gate
  is exactly the drift that review found.
- **A repository named by `--repos` that has no checkout on this machine** is converged for the
  *installation* half (its project job exists and serves it) but not for its own repository root. The
  command names each such repository and says what is left; `fugaro doctor`'s
  `claude-accounts-stored` (`internal/cli/doctor.go`) is the read that says whether it matters. This is
  the same boundary `fugaro models rm` warns about (D783) and it is a **decision** below (D865), not an
  oversight.
- **A named repository whose checkout is recorded on this machine** (`localcfg.Checkouts`,
  `internal/localcfg/checkouts.go`) is converged for both halves, through the same re-converge the
  models design's D787 asks `fugaro update` to grow. Without D787's mechanism the command converges only
  the checkout it runs in.

### 2.6 What it prints, and what it never prints

**The confirmation (D868).** Before anything is written — before the local config, the secret and the
stages — the command prints the facts below as one confirmation, and this is the text **`--yes` stands
for**:

```console
$ fugaro accounts add team-a --repos owner/repo-a,owner/repo-b
This will, for account team-a:
  declare   claude_accounts.team-a.allow_data_to = [owner/repo-a, owner/repo-b]
  profile   profiles.standard.claude_accounts += team-a
  secret    write a new version of claude-oauth-token-team-a (project-wide)
  publish   policy node: accounts.team-a.allow_repos = [github/owner/repo-a, github/owner/repo-b]
  deploy    + standard/github/team-a (and its service account)
  allow     a launcher that may start a run on owner/repo-b can have that run print team-a's token
Write these changes? [y/N]
```

The last line is printed **only when `allow_data_to` gains a repository beyond the default** (the
checkout the command runs in), because that is the widening the typed confirmation used to gate; the
rest of the block is always shown, so a confirmation is never a bare `y`. The command proceeds on `y`
and writes nothing on anything else.

```console
account   team-a            claude-oauth-token-team-a (project-wide, version 2)
repos     owner/repo-a      claude_accounts.team-a.allow_data_to
profile   standard          profiles.standard.claude_accounts += team-a
mark      team-a            cleared (was broken)
publish   policy node       accounts.team-a.allow_repos = [github/owner/repo-a]
deploy    jobs              + standard/github/team-a (and its service account)
check     accounts ls       owner/repo-a may use: default, team-a
Nothing else to run: fugaro run --token team-a is ready.
```

- The diff of the local config file is printed as a line diff, through the update package's own
  `lineDiff`, `backupFile` (`<file>.bak-<timestamp>`, mode 0600) and `writeFileAtomic`.
- **Fugaro prints the token on no path of its own**, and no line of its output is derived from it. The
  secret's line carries the name and the version, which is what `fugaro secrets set` prints today. (The
  one place a token is printed is Anthropic's login in act one of `--claude`, on the owner's own
  terminal; §3.2 says what that does and does not promise.)
- The last line is the whole point of the command: it names the *one* thing the owner does next, and it
  is a `fugaro run`, not a `fugaro update`.
- `--yes` answers the confirmation above, the widening line included, and changes nothing else; the
  diff is still printed.

## 3. `--claude`: Anthropic's own login, then one paste

`--claude` puts Anthropic's own login into the command without Fugaro ever reading the login's output.
It is **two acts**, and the second is the path the command already has:

1. **Act one — the login, in the owner's own terminal.** Fugaro finds the binary, confirms it, and runs
   `claude setup-token` as a child that **inherits the owner's own stdin, stdout and stderr**. Nothing
   sits between the owner and the login: no pseudo-terminal, no copier, no raw-mode switch, no parse of
   what it prints. The owner answers whatever Anthropic asks — a browser, a code, a prompt — exactly as
   if they had typed `claude setup-token` themselves, because that is what is running.
2. **Act two — one paste.** When the login has printed the token and returned, the command asks for it
   at the **hidden prompt** it uses without `--claude` (§2.2 step 3). What the owner pastes there is
   the only token the command ever stores.

This is the owner's answer to D862 (alternative (a)): the capture — the pty child, the line-copier, the
raw-mode terminal and the token-run guard — is gone, with its dependency (E5) and its unverified
premise. Its cost is stated where it falls: the login prints the token on the owner's own terminal, and
the owner performs two acts rather than one.

### 3.1 Act one: the login

1. **Find the binary.** `claudeplugin.Find`'s own rule (`internal/claudeplugin/claudeplugin.go`):
   `exec.LookPath("claude")`, refusing `exec.ErrDot` and refusing anything not absolute. The resolved
   absolute path is printed and confirmed before it runs. The binary is executed **directly, with an
   argument vector** — never through a shell, never with `-c`.
2. **Require a terminal.** The login is interactive and the paste that follows is hidden, so `--claude`
   is refused when the owner's stdin or stdout is not a terminal, and the refusal names `--token-stdin`
   for the case that is genuinely scripted. Nothing else about the command needs a terminal; this flag
   does.
3. **The environment is the owner's, minus anything Fugaro added.** In particular the child is not
   given the token (it does not exist yet) and no variable is set for it. Two variables are *checked*
   first and, when either is present, the command stops and says so: an exported
   `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` decides which account a login lands on, and a login
   that lands on the wrong account is a token the owner did not mean to store. This is a check, not a
   deletion: the command does not edit the owner's environment, it declines to guess, and **no child
   runs at all**.
4. **Run it, and let it own the interaction.** The child is an ordinary child of Fugaro's own process,
   in the same process group and on the same terminal, so what the owner types reaches the login
   directly and the terminal's own signals reach it too. Fugaro holds its own `SIGINT` for the duration
   — it does not die on the owner's Ctrl-C and does not restore anything afterwards, because it changed
   nothing — so the login's own handling of a cancel decides, and Fugaro reads the exit status once the
   child is gone.
5. **Wait, then hand over to act two.** A non-zero exit stores nothing: the command reports the child's
   exit status and still offers the hidden prompt (§3.3), so a login the owner cancelled or that failed
   costs one paste rather than the command. Whatever the child printed is the login's own business:
   Fugaro keeps none of it — not a line, not a copy, not a buffer.

### 3.2 Act two: the one paste, and the value's whole lifetime

**The hidden prompt is the only token path.** On every path — with `--claude`, without it, after a
failed or cancelled login — the value is read by `readHidden` / `readHiddenLine`
(`internal/cli/secrets.go`), the echo-free terminal read the rest of the CLI already uses, and it is
the same read `fugaro secrets set <name>` performs. The login's output is never a source: Fugaro does
not parse it, does not capture it and keeps no copy of it.

- The value is held in a `[]byte`, `clear`ed after the store (`internal/cli/secrets.go`'s own idiom),
  and never converted to a `string` — a `string` copy cannot be zeroed and would sit in the heap until
  the GC moved it.
- Fugaro writes the token nowhere: not to a file, not to the local config, not to an error, not to a
  log, not to an argument, not to an environment variable. Its own output carries names and versions.
- Every error path from the store runs the value through `agent.Redact`, exactly as `storeSecret` does,
  because a server may echo back what it received.
- **What this path does not promise.** Act one is the login printing the token on the owner's **own
  terminal**, so the token is visible there — that is the price D862's answer accepted. What Fugaro
  holds is the paste at the hidden prompt, which is not echoed; what the terminal displayed, its
  scrollback and anything recording that terminal are the owner's, and Fugaro neither captured nor
  copied them. The design does not pretend the token never touched the terminal: it says where it did,
  which is act one, by the owner's own command.

### 3.3 The login is never a gate

The paste is the command's own path, not a fallback from a failed capture, so nothing about the login
can leave the owner stuck:

- A login that exits non-zero stores nothing, and the command reports the child's exit status — never
  its output tail — and still asks for the hidden paste. A cancelled login costs one paste.
- The login is a convenience for finding the token, and `fugaro accounts add <name>` without
  `--claude` is the same command for the owner who already has one or would rather mint it elsewhere.
  It is not a lesser path and its sentences do not say it is.

### 3.4 What is unverified, and what the live check settles

Three things are Anthropic's behaviour, not this repository's, and none of them is decided here. None
of them can change the code, because the code no longer reads the login's output — they decide the
**docs'** wording, and LC1 and LC2 are what settle it:

1. **What the login prints and asks** — whether a browser opens, whether a URL is printed, whether a
   code is pasted back, what each prompt says. This decides what `docs/project-claude-accounts.md`
   tells the owner to expect around act one; the code is indifferent, since the owner answers the login
   directly on their own terminal.
2. **Whether it logs out an existing login**, and whether it takes an account/profile flag. This is
   U2, and its answer is the page's **warning text** for an owner who already has another account on
   the machine.
3. **The exit status of a cancelled login.** The owner's Ctrl-C reaches the child directly (§3.1 step
   4), so what it does with it is Anthropic's behaviour. Every non-zero exit is "store nothing, still
   ask for the paste" (§3.3), and Fugaro has no terminal state to restore.

### 3.5 Platforms

**Every platform the CLI runs on**, Windows included. There is no pseudo-terminal to allocate, no
build tag and no ConPTY: the child inherits the owner's terminal, which is the one thing every
platform already has. D863 (the Windows refusal) is **moot** with D862's answer, and so is the
`go.mod` note that called the pty package test-only (E5) — nothing here touches it.

The only requirement is a terminal, for the login and for the hidden paste (§3.1 step 2), and a
missing one is a named refusal, not an error.

## 4. `fugaro accounts rm <name>`

Removes an account. The inverse of `add`, in the order that cannot strand a running job:

1. **Refuse while a profile lists it.** A profile's `claude_accounts` names a job the installation has
   deployed; removing the account under a live job leaves a job whose account the policy cannot
   describe, which is the state `ValidateProfileClaudeAccounts` refuses at publish. The refusal names
   each profile and says to take the name out of it first — through `fugaro accounts rm --profile NAME`,
   which is the same command with the same flag as `add`.
2. **Warn and remove elsewhere.** The `claude_accounts` entry goes, and the secret
   `claude-oauth-token-<name>` is **deleted** through the store's own `Delete`
   (`internal/backend/gcp/secrets.go`) — the call exists and **no command reaches it today**, so
   `accounts rm` is the first door to it. Leaving a live credential in Secret Manager with only the
   owner's admins able to read it is not what "remove the account" can mean, and Secret Manager's own
   delete is reversible within its grace period, which is the recovery an owner actually wants. The
   secret's ID is named in the confirmation before it goes, and the command **says plainly that
   deleting the secret does not revoke the token at the provider**: the token stays valid until it
   expires or the owner revokes it in Anthropic's own console, which no command here reaches (D866).
   A repository whose committed `fugaro.yaml`
   still names the account in `agent.oauth_token` is **warned about**, naming the repository and the
   run that will fail at launch — the shape D783 chose for `fugaro models rm`, and for the same reason:
   the owner's own edit should not be blocked by a file the command cannot see.
3. **Converge.** The same publish and deploy as `add` (§2.5), so the job the account owned is gone from
   the installation.

`rm` is refused in a coding agent's session for the same reason `add` is.

## 5. What the existing commands become

Nothing is removed, and nothing changes meaning:

| Command | After this design |
| --- | --- |
| `fugaro accounts add` | new |
| `fugaro accounts rm` | new |
| `fugaro accounts ls [--repo] [--json]` | unchanged. Its state column already reports a mark, so it is the read that shows what `add` cleared |
| `fugaro accounts clear NAME` | **stays.** `add` clears the mark it should clear as part of replacing the token (§2.2 step 6), so the common path no longer needs it — but a token replaced through `fugaro secrets set` (a scripted rotation, a teammate's machine) still needs it, and folding it away would leave that path with no door |
| `fugaro secrets set claude-oauth-token-<name>` | unchanged and still the low-level door. `add` is what a person uses; `set` stays what a script uses |
| `fugaro secrets ls --project-wide` | unchanged; the page's read-back step folds into `add`'s own last line |
| `fugaro project policy publish` | unchanged. It becomes the explicit door to what `fugaro update`'s policy stage does anyway (settings design, S3) |
| `fugaro update` | unchanged, and it remains the whole upgrade |

## 6. Threat model

**(a) The token in memory only.** The value lives in a `[]byte` for the length of one command: read
at the hidden prompt or from stdin, handed to `gcp.Secrets.Set`, and `clear`ed. It is
never a `string` (a `string` cannot be zeroed), never a file, never an argument, never an environment
variable, so it reaches no shell history — `fugaro accounts add team-a --claude` is the whole command
line, and it carries no value.

*What this does not promise:* Fugaro cannot keep the value out of swap or a core dump — no portable
`mlock` through the Go runtime, and the runtime may copy the buffer for its own reasons. The honest
statement is a **short lifetime and an explicit wipe**, not "never on disk". The design deliberately
does not add `mlock`: it is platform-specific, needs a locked-memory allowance, and would advertise a
guarantee the runtime does not let it keep. This is the same posture `fugaro secrets set` has today, and
the same residual risk.

**(b) The child process's environment.** The login child inherits the owner's environment. Fugaro adds
nothing to it and, in particular, never passes the token back. Two variables are *read* and stop the
command when set (`CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`): either one decides which account the
login lands on, and a token minted for the wrong account is a token the owner did not mean to store.
The check is a **refusal, not a deletion**: when either variable is set the command stops and **no child
runs at all**, so there is never an environment Fugaro has edited for the child to make its own
decisions in.
The child is `claude`'s process, on the owner's own terminal and in Fugaro's process group, so the
owner's keystrokes and the terminal's signals reach it directly; Fugaro holds its own `SIGINT` for the
duration, so the login's handling of a cancel decides and Fugaro only reads the exit status.

**(c) A hostile `claude` binary on `PATH`.** This is the sharpest one, and the mitigation is
`claudeplugin.Find`'s, unchanged: the binary must resolve through `PATH` to an **absolute** path, a
relative `PATH` entry (`exec.ErrDot`) is refused, and the resolved path is **printed and confirmed**
before it runs. Fugaro runs it with an argument vector, never a shell. Beyond that, the design is honest
about what it cannot do: a hostile `claude` on the owner's `PATH` can already steal anything the owner's
`claude` can reach — it is the owner's own binary, run as the owner. **The capture's power is gone with
the capture (D862 (a)):** the command stores only what the owner pastes at the hidden prompt, so a
hostile binary cannot make Fugaro store a token-shaped string it printed. What is left is the
**deception** channel, which the design does not pretend to close: a hostile login can print a decoy
token on the owner's terminal, in the hope that the owner copies that instead of the real one, and the
value the owner pastes is stored because the owner is the authority. The confirmation of the resolved
path — printed before the child runs, at an absolute path, through an argument vector, never a shell —
is the whole of the defence, and it is the same defence it always was. An owner who does not trust
`PATH` runs `fugaro accounts add <name>` and pastes a token they minted themselves, which never
executes anything.

**(d) Tokens in crash reports.** The token reaches no stream Fugaro writes, so it reaches no crash
report: the console's crash reporter is dormant behind its DSN gate
(`console/src/main/crash.ts`, guarded on that side by `console/test/unit/secret-hygiene.test.ts`) and it can only
report what the console saw, which is the CLI's stdout and stderr. Fugaro's own output carries names
and versions, never a value, and the hidden prompt is not echoed — so a recorded terminal session
holds no token from Fugaro. The one place a token is visible is **act one of `--claude`**: Anthropic's
login prints it on the owner's own terminal, so it reaches that terminal's scrollback and whatever
else records it, which is the price D862's answer accepted (§3.2). Fugaro never captures that output,
so it is not a place Fugaro can leak from. Any error from the store is passed through `agent.Redact`
before it is shown or logged, because a server may echo back what it was sent.

**(e) `allow_data_to` and who may widen it.** The default is the checkout the command runs in; `--repos`
widens it; the profile's own `allow_repos` is the ceiling the subset rule checks the widening against,
before anything is written. The field lives in `~/.config/fugaro/projects/<project>.yaml`, the file the
page's §3 already calls operator-only and never published to the runs bucket — so widening it is an
owner act on the owner's own machine, exactly as it is today. The command prints the before-and-after
list of every repository it adds, and the diff of the file, so a widening is visible rather than
implied.

*What widening still means:* an account's token is the blast radius of a hostile run — the sentence
[SECURITY.md](../../SECURITY.md) states under **Known limits** as "The account is the blast radius, not
the repository", and the page's own §1 — and a launcher that may start a run on an account job can have
that run print the token (design D2). Widening `allow_data_to` therefore widens who may
reach that token's account, and the confirmation's own text names it, with what it means (D868,
answered: `--yes` covers everything, so the warning is carried by the sentence rather than by a typed
gate; §2.6). The account job also mounts the profile's GitHub App key (`internal/infra/projectjobs.go`), so a hostile run on it reaches the forge as well as the Claude account (see #1472, F3).

## 7. Owner-only boundaries

- `claude_accounts`, `profiles` and `project_secrets` stay local-config-only: not `fugaro.yaml`, not
  the project layer, so neither a reviewed repository nor a published layer can add an account. This
  design changes none of that.
- The command is a **local** act on the owner's machine. It writes the cloud only through the stages
  §2.5 names, which are the same stages `fugaro update` runs.
- No new cloud call is introduced: Secret Manager (the store), the Realtime Database (the node and the
  mark), Terraform (the apply). Nothing else.

## 8. Docs, skills, goldens and pins that change

Everything below is listed so the implementing task's file list is complete; **none of it is edited by
this design PR**.

| File | What changes |
| --- | --- |
| `docs/project-claude-accounts.md` | The page becomes short: the seven steps of §1 above collapse to one command, and §2, §3, §4 and §5 shrink to what the command does not do (the version-3 rollout's history, the subset rule as a rule, and the job-per-(profile, account) shape). §6 and §7 mostly stand |
| `docs/design/named-oauth-tokens.md` | §7's U2 row: its second half is what §3.4 answers |
| `plugin/skills/setup/SKILL.md` | The named-accounts paragraph (`:141`) becomes "the owner runs `fugaro accounts add <name>` in their own terminal", and the `claude setup-token`-then-`fugaro secrets set` sequence for a *named* account stops being the skill's advice |
| `internal/cli/docs_project_named_oauth_tokens_test.go` | The pins on that page: `TestDocsProjectClaudeAccountsOperatorGuide`, `…CommandsExist` (every `fugaro …` line on the page must resolve), `…NoSecretShapes`, `…GcpSetupRollout`, `…SharedJobLimitsAreTrue`, `…ExamplesValidate`, `…StayOutOfSiteNav`, plus `TestDocsProjectNamedOAuthTokens*` where the page's sentences move |
| `internal/cli/docs_project_policy_reseed_test.go` | Cites the page's §5 ("the publish that follows rebuilds the list"); the sentence it pins moves with the page |
| `internal/cli/docs_named_oauth_tokens_test.go` | `TestDocsNamedOAuthTokensSetupSkill` pins that the skill never invents an unverified `claude setup-token` step. Answering U2 is what lets the skill name a sequence; until then the skill names the command and nothing more |
| `internal/cli/doctor.go`, `internal/cli/doctor_render.go` | The fixes that print `claude setup-token` + `fugaro secrets set …` become `fugaro accounts add NAME`, for the account checks (`claude-accounts-stored`, `project-account-stored`) and the project-wide one |
| `internal/cli/doctor_test.go` | Pins those fix strings |
| `internal/cli/initsecrets.go`, `internal/cli/initsecrets_test.go` | The setup flow's own "next steps" lines for a harness OAuth credential |
| `internal/infra/repo.go`, `internal/infra/repo_test.go` | The repository root's `step.Commands` for an OAuth credential |
| `internal/cli/testdata/init_commands.golden`, `internal/cli/testdata/init_retire.golden`, `internal/infra/testdata/repo_root_addresses.golden`, `internal/infra/testdata/repo_root_credentials_addresses.golden` | Regenerated, never hand-edited (their own `-update` flags) |
| `plugin/skillcli_test.go`, `plugin/setup_skill_test.go`, `plugin/skills_lint_test.go` | The plugin's coverage tests: every command and flag a skill names must exist in the CLI, so they turn green on the same branch as the command (the models design's M6 red step is the precedent) |
| `internal/cli/accounts.go` | Registers the two subcommands: the `accounts` parent already ends in `cmd.AddCommand(...)` for `ls` and `clear`, so `add` and `rm` register beside them. **`internal/cli/root.go` is not touched** — an earlier draft of this row named it, and reconciliation F19 shows why that was wrong. `root.go` is reserved for other work streams |
| `website/content/cli-reference.md`, `website/data/release-summaries.yaml`, `docs/releases/vX.Y.Z.md` | Generated or release-owned, regenerated by their own tooling |
| `docs/gcp-live-checklist.md` | The check in §9 lands here under the number free at that merge — **another work stream's file** |
| `schemas/` | **No change.** `claude_accounts` is a local-config field with no JSON Schema, and this design adds no wire shape |

## 9. The live check (owner-run, nothing secret typed into a chat)

[Check 35](../gcp-live-checklist.md) is **U2, written and never run**, and it is this check's second
part. Every part is run by the owner on the owner's machine, in the owner's own terminal; **Fugaro
prints no token on any path**, and any answer recorded in this repository is a *shape* — what the login
asks, what a prompt says, whether a value was echoed — and never a value.

| # | What it settles | How |
| --- | --- | --- |
| **LC1** | what the login prints and asks | On a machine that has never run it: run `claude setup-token` in a plain terminal and record only (a) whether a browser opens, (b) whether a URL is printed and whether a code is pasted back, (c) what each prompt says, and (d) whether the token it prints stands alone on its line or sits inside a sentence — so the page can tell the owner what to copy. **Never the token.** This is what the page's act-one wording is written against; the code reads none of it (§3.4) |
| **LC2** | multi-account (U2, Check 35) | On a machine already holding another account's login: whether `claude setup-token` logs out, whether it takes an account or profile flag (`claude setup-token --help` is the authority), and what it says about the existing login. This decides the **warning text** in `docs/project-claude-accounts.md`, nothing in the code |
| **LC3** | the two acts, and the hidden prompt | `fugaro accounts add <name> --claude` on a throwaway installation, in the owner's terminal: confirm the login **runs in that terminal** and answers the owner directly, that the one paste that follows is asked at the hidden prompt, that the prompt **does not echo** the value, that Fugaro's own output never holds it, that `~/.bash_history` holds only `fugaro accounts add <name> --claude`, that nothing under `~/.config/fugaro/` holds a value, and that a run with `--token <name>` then starts |
| **LC4** | the deploy half | After LC3: `gcloud run jobs describe` the new account's job and confirm exactly one account-token `secret_env` entry, beside the profile's own mounts (which include `github-app-key`), its service account holds `secretAccessor` on that secret and **no other account's**, and `fugaro doctor` is green |

LC3 and LC4 are the ones that can falsify the design rather than the code: the whole of `--claude` now
rests on the login taking the owner's own terminal and on one hidden paste being enough, and LC3 is
where a login that needed more, or a prompt that echoed, would show up.

## 10. The console and the mobile app

No change. Neither reads the local config; an account reaches the console only through the run record's
own account field (`fugaro watch --json`, `runstore.Record`), which this design does not touch. The
console's crash reporter is named in §6(d) as a leak path that is already closed, and nothing here
reopens it.

## 11. Release placement

**0.15.0.** The reasoning, and why 0.14.2 cannot carry it, is in
[implementation-plan.md](implementation-plan.md) §Release.
