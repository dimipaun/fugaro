# Project Claude accounts: one OAuth token per (profile, account)

Status: **0.9.0, released.** The released Fugaro is 0.29.0, and 0.9.0 is the first release any of this page works on:
the `claude_accounts` field inside a profile, the `accounts` block in the published policy and the
jobs they name all ship in 0.9.0 (policy version 3,
[`internal/projectpolicy/version.go`](../internal/projectpolicy/version.go)), and a 0.8.x binary
reads none of it. The page is kept out
of the docs hub on the site until the hub lists it, and every command below is a 0.9.0 or later
`fugaro` binary. The design it documents is
[project-named-oauth-tokens/spec.md](project-named-oauth-tokens/spec.md).

Every command on this page is labelled with **where it runs**, because the two are different
machines with different files:

- **per installation** — on the machine that holds the project config
  (`~/.config/fugaro/projects/<project>.yaml`, this page's `fugaro project policy …`,
  `fugaro secrets set … --project-wide`). An owner's machine, usually; a teammate's machine holds
  the same file but not the rights.
- **per repository checkout** — inside a checkout of the repository, in your own terminal
  (`fugaro update --repo .`, `fugaro run --token …`, `fugaro image refresh`).

Neither is the agent's: `fugaro secrets set` reads a value only from stdin or a hidden prompt, and
never from an argument, a flag or the environment.

**§12 to §14 are the vocabulary built on top of this page.** An account is one type of a
**credential** (§12), credentials group into ordered **pools** (§13), and the five **crafts** of a
run — coder, first-line review, senior review, security review and the escalation ladder's merge
judge — choose the model and the credential or pool their calls go out on (§14). §1 to §11 describe the account itself, and every rule in them
still holds; §15 adds one further indirection over the account-choice keys of §8, without changing any
of them, and §16 is the one command that moves those keys off one account and onto another.

## 1. What a named account is

The Claude OAuth credential was one secret per repository: `claude-oauth-token`, mounted into the
job as `CLAUDE_CODE_OAUTH_TOKEN`
([named-oauth-tokens/spec.md](named-oauth-tokens/spec.md) §1). Named accounts
([named-oauth-tokens/spec.md](named-oauth-tokens/spec.md)) made *which* token a repository
uses a choice: `agent.oauth_token` in `fugaro.yaml`, or `fugaro run --token`.
[project-named-oauth-tokens/spec.md](project-named-oauth-tokens/spec.md) carries that choice to
the project job, where the credential is a fact of the **job** and not of the run.

A named account is:

- **a name**, `claude_accounts.<name>` in the project's local config (§2 writes it), which decides
  the repositories that may use its token (`allow_data_to`). The reserved name `default` is the
  unsuffixed `claude-oauth-token` every repository already used, and it is defined by the
  `project_secrets` entry alone.
- **a secret**, `claude-oauth-token-<name>`, **stored project-wide** — one copy for the whole
  Fugaro project, in the project's own Secret Manager. That is the copy a shared job can mount;
  a repository's own copy does not reach it (§2).
- **a job**: `fugaro setup` deploys one Cloud Run job and one job service account per
  `(profile, provider, account)`. The profile's no-account job — the job every profile keeps — holds
  the `default` account's token exactly when the profile lists `default`; every named account gets a
  job of its own.
- **one token per job, mounted by the platform**: the account's token is a static `secret_env`
  entry of the job's container, so it is there from the start of every run of that job and no run
  can decline it. **No launcher's own credentials read a token** — the value is not in the job's
  plain environment, and reading it needs `secretAccessor`, which only the job and build accounts
  hold — **but a launcher can have the run do the reading**: it writes the task text of the runs
  it starts, and a run on an account job holds the token, so a launcher that may start one can
  have it print the token. That is why a launcher is trusted (design D2), and what bounds the
  reach is the `allow_repos` list below, not the launcher's own credentials. The account is the
  blast radius of a hostile run ([SECURITY.md](../SECURITY.md), design §6.1 and its threat-model
  row for a launcher: "that reach is the per-repository job's today, unchanged").
- **a list**: `accounts.<name>.allow_repos` in the published policy is the exact, per-repository
  list of repositories whose runs may use that token. It is derived at publish, never hand-written
  (§4, §8), and both the launcher and the runner check it.

Which credential a run holds is therefore decided **before** it starts, by which job it is launched
onto. A repository that must run as another account belongs on a profile that serves it.

## 2. Add an account: one command

**Per installation**, in your own terminal — in a checkout or not:

```bash
fugaro accounts add <name>
```

That one command is the whole of it, and it is the path this page is about. It writes both
declarations of §3 — the account's `claude_accounts.<name>.allow_data_to` and the name in one
profile's `claude_accounts` — and does the rest in the same run: the token is stored **project-wide**
as a new version of `claude-oauth-token-<name>`, and the installation is converged on the account —
the policy node published, the account's job deployed, this checkout's repository root moved —
through the stages `fugaro update` runs, behind one confirmation. Its last line says what is left,
which is a `fugaro run` and never an update.

`--repos owner/a,owner/b` names the repositories the token may be sent to (the default is this
checkout's origin), and `--profile NAME` names the profile whose jobs serve the account (the default
is the only profile, when the config holds exactly one). The rules §4 states are applied to the
config this would write, before anything is written.

**The token is read from stdin or at one hidden prompt, and neither is an argument, a flag or an
environment variable:** the same read `fugaro secrets set` performs — the echo-free prompt on a
terminal, and stdin when there is none, so a script pipes the value rather than passing it — held in
memory, cleared after the store, and never written to a file, a log, a message, an argument or an
environment variable.

`--claude` (`fugaro accounts add acme-team --claude`) runs Anthropic's own login first, on that same
terminal — two acts: the login, resolved on your `PATH` and run directly with your own stdin, stdout
and stderr, which answers you and not Fugaro (what it prints and asks is its own, and none of it is
read), and then the one paste at the hidden prompt above, which is the only token ever stored — so a
login you cancelled or that failed costs one paste rather than the command. `--claude` needs a
terminal, and its refusal for one that is not names the same command without the flag; it also stops
before running anything when `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` is exported, because
either one decides which account a login lands on, and a login that lands on the wrong account is a
token you did not mean to store.

Without `--claude` it is the same command for an owner who already has a token or would rather mint
it elsewhere, and it is not a lesser path. `claude setup-token` is Anthropic's own command and the
only way to mint a token: Fugaro never mints one, never prints one and never takes one as an
argument. Run it as the person or team whose subscription the token belongs to — one token per
account, signing in to the account that name is for. What Claude Code does when a machine already
holds a token for another account (whether it logs out first, takes a flag, or wants a separate
profile) is not verified by Fugaro, no live check having been run
([named-oauth-tokens/spec.md](named-oauth-tokens/spec.md) §7, U2): `claude setup-token --help` in
your own install is the authority, and nothing here depends on the answer, because what is stored is
whatever value you paste.

**Every command on this page carries placeholders, never a value:** `<name>` is the account's name
and `<token>` is the value you paste. `fugaro secrets set` reads a value only from stdin or a hidden
prompt, never from an argument, a flag or the environment.

**What the command does not do:**

- **The `default` account's token**, which the unsuffixed name carries and `accounts add` will not
  define — `default` is a legal reference for a profile and an illegal definition — so it is stored
  by hand, project-wide, and `project_secrets` is what says which repositories it is for:

  ```bash
  fugaro secrets set claude-oauth-token --project-wide
  ```

  Without a `project_secrets.claude-oauth-token` entry with a non-empty `allow_data_to`, that
  command is refused: the name has a project-wide meaning only when the project states which
  repositories it is for.
- **A named account's token, stored by hand.** `fugaro secrets set claude-oauth-token-<name>` is
  what `accounts add` runs for you, and on its own — as §8's rotation does — it is accepted only
  when `claude_accounts.<name>.allow_data_to` lists at least one repository (§4). Otherwise it is
  refused and names that field, because a token whose repositories are undecidable is one no policy
  can bound.
- **A repository copy.** `fugaro secrets set claude-oauth-token-<name> --repo acme/webapp` (per
  installation, naming the repository; or per repository checkout without a value, which uses this
  checkout's origin) stores the repository's own copy, which overrides the project copy for that
  repository's *per-repository* jobs. **A repository copy does not reach a project job.** An account
  job mounts the project-wide copy by its static `secret_env` entry, so the repository copy is not
  what its runs hold — `fugaro doctor` says exactly this when a token is missing (§9).
- **Reading the names back** — per installation, names only, never a value:

  ```bash
  fugaro secrets ls --project-wide
  ```
- **Removing an account.** `fugaro accounts rm <name>` is the inverse and converges the same way: it
  refuses while a profile still lists the account — `fugaro accounts rm <name> --profile PROFILE`
  takes the name out of one profile's list alone — and once no profile lists it, the declaration,
  the secret and the job go together. A `repos.<owner/name>.claude_accounts` that names the account
  is cleared with the declaration (§2, §3): the entry is what admits that repository's own runs, and
  left behind it would name an account the file no longer defines, which the next
  `fugaro project policy publish` refuses. A committed `fugaro.yaml` that still names the account in
  `agent.oauth_token` is warned about rather than edited — that file belongs to the repository, not
  to this installation.

## 3. What the command writes

Both blocks live in the operator's local config — `~/.config/fugaro/projects/<project>.yaml`, per
installation, operator-only, never published to the runs bucket.

`fugaro accounts add` writes two keys in them: the account's `allow_data_to` and the name in one
profile's `claude_accounts`. The rest is written by hand — the `project_secrets` entry the `default`
account's token is declared by (§2), and the profile's own `allow_repos`, which the command requires
to be there but never writes — and §4's rules hold either way.

**The account**, next to `providers` and `project_secrets`:

```yaml
# per installation: the project's local config
claude_accounts:
  acme-team:
    allow_data_to:
      - acme/webapp
      - acme/tools

project_secrets:
  claude-oauth-token:
    allow_data_to:
      - acme/webapp
      - acme/tools
```

`allow_data_to` is the same owner/name list a model provider carries: the repositories whose runs
may use that token. It is also what decides whether the token's project-wide copy may exist at all
(§2), and what `accounts.<name>.allow_repos` is derived from (§5). `default` is reserved and may
not appear as a key of `claude_accounts`; its list is the `project_secrets` entry above — here the
same two repositories as `acme-team`, which is what lets the profile below list both accounts (the
subset rule, §4).

**The profile**, which is where the project says which accounts a shared job serves:

```yaml
# per installation: the project's local config
profiles:
  standard:
    provider: github
    claude_accounts:
      - default
      - acme-team
    allow_repos:
      - github/acme/webapp
      - github/acme/tools
```

`profiles.<p>.claude_accounts` is a new field inside a profile entry, and it is the whole of how a
profile states accounts: `fugaro setup` deploys one job per entry (plus the profile's no-account
job), and a Claude OAuth token in a profile's `secrets:` is still refused — that field is not how a
profile mounts a token. The profile's `allow_repos` is the repositories that profile admits, and
**every account it lists has to cover every one of them** (§4): the example above is valid because
both `default` and `acme-team` carry both repositories, and a profile that admits a repository one
of its accounts does not carry is refused at publish rather than deployed.

**The repository's own entry**, which is how a repository whose runs are *not* on a profile job is
admitted to an account:

```yaml
# per installation: the project's local config
repos:
  acme/tools:
    provider: bitbucket
    workflows:
      - implement
    claude_accounts:
      - acme-team
```

`repos.<owner/name>.claude_accounts` is a list of account names inside the repository's own entry —
the entry `fugaro update --repo .` already writes for an onboarded repository. It exists because a
profile is not the only kind of job a repository's runs land on: a **per-repository job** — every
Bitbucket repository, which never reaches a shared job, and every repository no workflow sends to
one — has no profile to name its accounts in. Before this key the published `accounts` block was
derived from the profiles alone, so such a repository had no way to be admitted to an account at
all: the block carried no entry for its account, the credentials service's intersection with its
admission (`config/policy/repos/$slug.credentials.accounts`) came out empty, the cascade offered no
candidate and every model call of its runs was refused `503 no_candidate_credentials` — the class that
says the state *was* read and names the empty candidate list, with every credential it dropped and
the filter that dropped it (the same halt was answered `503 state_unavailable` before that class
existed, which read as a state that could not be read).

The entry adds **the repository's own canonical name** — `bitbucket/acme/tools` above — to each
account it names, and the `provider` on the entry is what qualifies it (the same provider a profile
carries for its `allow_repos`); an entry that names accounts and no provider is refused rather than
guessed at. A profile and a repository entry may both name one account: the published
`accounts.<name>.allow_repos` is the **union** of what the profiles name and what the repository
entries add, so nothing already admitted is narrowed. Each account a repository entry names must
**cover that repository** (§4, the same subset rule a profile meets), and the same `publish` writes
the repository's child admission under `config/policy/repos/$slug` from the account list it
published. The list lives in this entry alone — it is in no `fugaro.yaml` and in no Terraform input
— so the `fugaro update --repo .` that rebuilds the entry from those two **carries it over**:
writing the key once and updating the repository afterwards keeps the admission, and only `fugaro
accounts rm` (§2) or an edit of the file takes it away.

## 4. The rules `publish` and `fugaro setup` enforce

Neither the `accounts` block nor the profile field nor a repository's own entry is written by hand
into the policy: all three are
derived and checked by `fugaro project policy publish` (per installation) and by `fugaro setup`
(per installation, and `fugaro update --repo .` per repository checkout), which validate the profiles
they are about to deploy against the same block. The rules, and what a refusal says:

- **Every listed account is a defined account.** An account named by a profile but missing from
  `claude_accounts` (or, for `default`, from `project_secrets`) names a token whose repositories
  are undecidable, and a job whose account nothing describes. The refusal names the profile and the
  block to edit.
- **The subset rule (D3).** A profile that lists accounts must write **its own exact
  `allow_repos`**, with no wildcard, and **every account it lists must cover every repository on
  that list**. A profile's list may only narrow what its accounts cover — the account is the
  credential, and a repository the account does not cover is one whose run the token must never
  reach. An absent list means "the project's", which no account is required to cover, so it cannot
  be decided and is refused.
- **A repository's own entry obeys the subset rule too.** `repos.<owner/name>.claude_accounts`
  naming an account that does not carry the repository — a named account whose `allow_data_to`, or
  the `default` account's `project_secrets` entry, does not list it — is refused at publish
  (`projectpolicy`'s `repoAccountCovers`), with the same repair: add the repository to the account's
  own list and publish again, or take the account off the repository's entry. The provider is
  required on such an entry, because the repository's canonical name is what the account's
  `allow_repos` gains, and this entry is the only place a per-repository job's provider is stated.
- **No Anthropic API key beside the accounts (D4).** A profile that lists `claude_accounts` may not
  also mount `anthropic-api-key` (in either spelling): an account job holds its account's token, and
  a key beside it is a credential no OAuth run of that job can use — and which credential the job
  serves would stop being answerable from the profile.
- **An account may not be named `bitbucket-token`.** It is the one name a credential of the platform
  goes by that an account may not take. `bitbucket-token` is the upstream git token's
  (`ReservedSecrets`: the credential the proxy forwards with, and one a repository's job mounts); an
  account's key in the credentials bundle resolves back to the bare account name; and that bare name
  is what the policy's blocks and every audit line speak a credential by — so an account taking it
  would leave one name meaning two credentials. The bundle refuses that account's key outright
  (`internal/credsvc/bundle.go`, `schemas/creds-bundle.schema.json`), and `fugaro accounts add`,
  `fugaro validate` and `fugaro project policy publish` refuse the account name itself, so the clash
  is met where the account is written rather than at `fugaro creds set` or at the service. **Fix:**
  rename the account in `claude_accounts` and store its token under the new name. Every *other* name a
  credential of the platform goes by stays an account name — `github-app-key` and
  `anthropic-api-key` among them, since the bundle finds the App key by its kind rather than in the
  index of names, and refuses the paid key only where one document carries both it and an account of
  its own name.
- **An account may not be named so that its name ends in `-review`.** The suffix is the review-only
  job variant's (`internal/config`'s `ReviewVariantSuffix`): `fugaro update` deploys, beside every
  `(profile, provider, account)` job, a variant of that job whose account segment carries the suffix
  — the container a project's own senior review runs on, which mounts no credential at all. Since
  0.19.0 that container has a reader: the independent cloud senior review of a pull request runs on
  an account's variant, and it refuses until the installation's credentials service is enabled and
  admits the review job's caller (E7), which no `fugaro update` supplies. A
  coding account named `acme-personal-review` therefore derives the job name and the service account
  of `acme-personal`'s variant, and the variant would run as an account whose coding job mounts the
  GitHub App key — the one thing it exists not to be. The name is refused where it is written:
  `fugaro accounts add` and `fugaro validate` refuse the key of `claude_accounts`, and `fugaro
  project policy publish` refuses a profile that lists it or an `accounts` entry of that name.
  **Fix:** rename the account (and store its token under the new name). Only the suffix is refused —
  a name that merely mentions review is an ordinary account name.

The subset rule is an **intersection, not a union**: a profile is not "some account of mine carries
this repository" — the repositories it admits are the ones **every** account it lists carries. So a
profile whose list names a repository that even one of its accounts does not carry is refused at
publish (`projectpolicy`'s `validProfileAccounts`), with a refusal naming both lists and the
repository in one and not the other: the repair is the account's `allow_data_to` (add the
repository there and publish again) or the profile's `allow_repos`, and the operator meets that
decision where they make it rather than at a run.

**A repository entry and a profile are one answer about one credential, and their published lists
are unioned**: a repository admitted by a profile keeps that admission and gains the repository
entry's, and neither narrows the other. What the entry may never do is widen the *account*: the
account's own list is still the bound, and the entry is refused when the account does not cover the
repository (§3, and the subset-rule bullet above).

## 5. What `fugaro setup` deploys, and the version-3 rollout

**Per installation** (and per repository checkout for `fugaro update --repo .`), `fugaro setup` deploys
one Cloud Run job and one job service account per `(profile, provider, Claude account)`. The account
is the last segment of the job's name (`gcp.ProjectJobName`), and the job's service account holds
`secretAccessor` on its own account's secret and on no other account's — that is the whole of who
can read a value. A profile that lists no accounts deploys exactly what it deployed before.

**The rest of this section is history:** a 0.8.x installation moving onto 0.9.0, or one that has just
done so, was the case this rollout was written for, and an installation already past it has nothing
here to re-run. The published policy carries the account axis in a node the previous release cannot
read, so **0.9.0's accounts need all four of these**, in this order:

1. **Upgrade the CLI, per installation machine:**

   ```bash
   brew upgrade dimipaun/tap/fugaro && fugaro update
   ```

2. **Republish the policy, per installation.** A publish refuses to overwrite a node written in
   another version (that is what the version field is for), so the older node goes first:

   ```bash
   fugaro project policy publish --delete
   fugaro project policy publish
   ```

   `--delete` puts the project back on the ceiling its jobs' environments carry, so make it a
   step of its own and not a repair. A re-run of `fugaro update --firebase-project` is deliberately **not**
   the remedy: it deploys the rules template and marker and never writes `config/policy/shared`.

   **Read the list out first: the publish that follows rebuilds it.** With no node to replace,
   there is no list to keep, and the publish seeds `allow_repos` from this machine's config — its
   `repos:` map and the shared config (`seedAllowRepos`) — so a repository you had removed with
   `fugaro project repos rm` comes back when the config still names it, and one you had added by
   hand with `fugaro project repos add` is dropped. The node's `clone_urls` block and any
   `routing` block a person wrote are dropped with it: a publish carries those only from a node it
   replaces. `clone_urls` is the half you can put back, because it now has a writer outside the
   console — `fugaro project clone-url set` writes the block in your own config file, and the
   publish that follows takes it from there — while a hand-written `routing` block is yours to
   write again. So run this **per installation** before the delete
   and keep what it prints:

   ```bash
   fugaro project policy show
   ```

   then, after the publish, read the diff it prints and repair the list with `fugaro project repos
   add <owner/name>` and `fugaro project repos rm <owner/name>`. Runs of a shared job halt in
   between — a run on a project job whose published policy defines no such job stops before the
   agent starts, which is the fail-safe direction and why this is a step of the upgrade rather
   than a repair to reach for.

3. **Refresh each checkout's runner image, per repository checkout:**

   ```bash
   fugaro image refresh
   ```

   A job runs the installation's base image, which can lag the CLI that published the policy. An
   0.9.0 policy read by an 0.8.x runner is refused (`the project policy is version 3; this Fugaro
   understands version 2`), and the fix is the image, not the policy.

4. **Move the shared jobs onto it, per installation**, in your own terminal, on a machine where
   step 3 ran:

   ```bash
   fugaro update --firebase-project <gcp-project-id>
   ```

   `fugaro image refresh` re-pins `base_images` and moves each repository's own jobs, but never a
   project job: those are the installation's, and only a re-apply of the installation moves them
   onto the base image this machine's `base_images` now pins. Until it runs, a shared job keeps
   its 0.8.x runner, which refuses the version-3 node, and neither the launch check nor `fugaro
   doctor` says so: both read the pin, not the deployed job.

Read it back per installation:

```bash
fugaro project policy show
```

It prints the profiles and, for each account the config names, the repositories its token may be
used by. Both sources fill that block (§3): the profiles that list the account, and a repository's
own `repos.<owner/name>.claude_accounts`. An account neither source names is not published: it is
one no job mounts, and a block entry for it would publish a list on a node every run of the project
can read.

## 6. Launch a run as a named account

**Per repository checkout**, either in the repository's committed configuration or on the command
line:

```yaml
# per repository checkout: fugaro.yaml
agent:
  auth: oauth
  oauth_token: acme-team
```

```bash
# per repository checkout
fugaro run --token acme-team
```

`--token` overrides `agent.oauth_token`; with neither, the repository's `default` account is used.
A follow-up (`--pr N`) keeps its previous run's account unless `--token` is given explicitly, and an
explicit one overrides it. The selection is checked the same way as a first run's.

The `fugaro.yaml` that name is read from is the one **at the ref the run checks out** — the launcher
reads it out of its own checkout's git objects and fetches nothing — and never the working tree's
copy: the run resolves `agent.auth` from that same document, and a checkout left on another branch,
or carrying an uncommitted `agent.oauth_token` edit, cannot choose the account the launch runs as.
A ref the launcher's checkout does not hold leaves the working tree's value standing, which is what
a launch had before the ref was read for this; the run then resolves the ref's own file after
cloning it. The same is true of the two other ways that read can come up short, and they are worth
knowing because the launcher's answer — *unlike the run's* — is a judgment made from what this
machine happens to hold: a `fugaro.yaml` at the ref that does not decode as one, and `agent.auth`
at a ref judged without the project layer that applies to it (the layer this command resolved for
that checkout, which a fresh machine or a cache that has moved on may not have). In both the
launcher falls back to the working tree's value, or to the file's own value where the layer would
have changed it — so a launch can be judged on a document, or on an `agent.auth`, other than the
one the run resolves. The run is the one that decides: it resolves the file it clones over the
layer its task carries, and refuses a launch it resolves differently (and on a shared project job
the launcher re-resolves that same credential before the claim). A launcher's refusal is therefore
never wrong in the direction of starting a run the file does not permit; what it can be is a
refusal, or an acceptance, that the run's own resolution then settles.

To see what a repository may choose, **per repository checkout** (offline, names only, no Secret
Manager call and no value):

```bash
fugaro accounts ls
```

It prints `default` first, then every `claude_accounts` name whose `allow_data_to` lists this
repository, sorted — that is the list `--token` accepts. A name the `credentials` block declares a
key credential is not in that list: it is no Claude account's token, so `--token` may not name it.

After the names it prints the `credentials` block, one line per entry, as
`NAME (TYPE, served by HALF)` — the half that carries a credential of that type to a call, which is
`the credentials service` for `claude-oauth` and `anthropic` and `a run's own gateway` for
`openrouter`. Those entries are what a pool's members may list and a craft's access may name, so
which of a project's two halves serves a credential is readable offline, without opening the file.

`fugaro runs ls` grows an `ACCOUNT` column
once one of the runs it lists has an account, and `fugaro diagnose` prints an `Account:` line when
that account is not `default`.

A named account is only ever selected by a workflow whose `agent.auth` is `oauth`. Naming one on a
workflow with another `agent.auth` is refused, and so is naming one this repository's
`allow_data_to` does not list.

## 7. How a refusal reads, and what to do

There are two doors, and one is not a duplicate of the other: the launcher refuses where the job is
chosen, **before the claim and before the budget token**, and the runner re-decides the same rules
from the policy it read itself, **before any stage, lease or budget spend**, independently of the
launcher that started it.

**The launcher (per repository checkout).** Both refusals end with the same clause — *"Nothing was
launched: no claim was taken, no budget token was minted and Cloud Run was not called"* — and each
names the fix:

- *The profile does not serve the account this run's credential selects.* The message says which
  account the run's own credential is, that a project job's account is fixed where the job is
  deployed (one job per (profile, provider, account), mounted as `CLAUDE_CODE_OAUTH_TOKEN`), and
  what the profile's `claude_accounts` does list. **Fix:** add the account to
  `profiles.<p>.claude_accounts` in the project's local config, store its token and publish again
  (per installation) — or run this repository on a profile that serves the account it selects.
- *The account's list does not cover this repository.* The message names the shared job, the account
  whose token it holds, `accounts.<name>.allow_repos` and the repository that is not on it, and says
  the block is derived at publish from the project's local config. **Fix:** the project's owner's —
  add the repository to that account's `allow_data_to` and publish the policy again (per
  installation), or run the repository on a profile whose account covers it.

**The runner (inside the job).** Four rules, each a way for the job and the run to disagree:

- *The job holds no Claude token* (the no-account job of a profile that does not list `default`)
  while the run's own auth is `oauth`: the run would start with no credential at all. The run stops
  rather than authenticating as something its own configuration did not choose or falling back to an
  API key — this release writes no API key for an OAuth run.
- *The job holds another account's token*: the run would hold a credential its own configuration
  does not use. The fix is the launcher's.
- *The policy describes no list for the account*: fails closed rather than using an unbounded token
  (unreachable through a decoded policy, kept as a halt).
- *This repository is not on `accounts.<name>.allow_repos`*: the run stops before the agent starts,
  with the sentence above and `"The run stops before the agent starts, independently of the launcher
  that started it"`.

Every one of these is a job-side fact, so a run that hits one has already been placed, claimed and
paid for on the launch side. That is why the launcher checks first and why the runner checks again:
the launcher's answer travels in the run's environment and its own `task.json`, which is a bucket
object the launcher writes and other things can too.

## 8. Rotation, revocation and per-account lists

**Rotation is a new secret version and nothing else.** Store the new value under the same name (per
installation, in your own terminal):

```bash
claude setup-token
fugaro secrets set claude-oauth-token-<name> --project-wide
```

The job mounts `version = "latest"` of the secret
([`deploy/terraform/gcp/modules/project/job.tf`](../deploy/terraform/gcp/modules/project/job.tf)),
so the next execution of the account's job reads the new version: **no redeploy, no policy publish
and no image refresh**. Runs that have already started keep the value they started with.

**Revocation is disabling the version, not deleting anything.** Fugaro never deletes a secret or a
version. To take a token out of service reversibly, disable its versions (per installation):

```bash
gcloud secrets versions list claude-oauth-token-<name>
gcloud secrets versions disable <version> --secret=claude-oauth-token-<name>
```

Runs of that job then start without a usable credential. `gcloud secrets versions enable` puts it
back. `fugaro doctor`'s `project-account-stored` check (§9) reports a token with no enabled
version, which is the state a disabled-last-version leaves.

**Changing which account a stage runs on** is a setting, not an edit of the local config by hand.
The local config keys one account per stage — `stages.<stage>.claude_account`, one key for each
stage name a run spends a model call on (`plan`, `implement`, `review_first`, `review`,
`adversarial`, `triage`, `fix`) — and `fugaro settings` reads it with the layer that decided it (per
installation):

```bash
fugaro settings get stages.review.claude_account
fugaro settings set stages.review.claude_account acme-team
fugaro settings unset stages.review.claude_account
```

The value is a name the project's `claude_accounts` declares, or `default` for the unsuffixed
account. A name nothing declares is refused with the declared names, and a name whose `allow_data_to`
does not cover every repository this project onboards is refused naming the repositories it misses:
the account a stage runs on is chosen for the repository a run is for, so an account that covers
less would have every run on the rest refused at the launch, and the write stops before it happens.
`unset` is the way back — the stage then runs on `default_claude_account`, the account every stage
uses when nothing more specific does (§3). This is the command that repoints a stage when the
account it runs on has run out; §6's `--token` and the committed `agent.oauth_token` stay the run's
own and the repository's own ways of naming an account.

**Changing which repositories an account may reach** is an edit to `allow_data_to` in the project's
local config (per installation), followed by a publish:

```bash
fugaro project policy publish
```

The list is derived from that field at publish — never hand-edited on the node — and both gates
read the published list. The profile's own `allow_repos` must still be covered by every account it
lists (§4), so widening an account alone changes nothing about what a profile admits; narrowing one
below the profile's list is refused.

## 9. What `fugaro doctor` reports

Per repository checkout, `fugaro doctor` carries three checks for this feature:

- **`project-account-stored`** — every account job the installation serves has a stored project-wide
  version of its token. A missing one is a failing check naming the job, the profile, the account
  and the token, with the fix on the line `fugaro accounts add <name> --repos <owner/name> --profile <profile>` for a
  named account a profile serves — §2's one command, spelled with the account's own `allow_data_to`
  and the profile that already serves it, so pasting it neither widens the account with the checkout
  it happens to run in nor picks a profile for itself — `claude setup-token; fugaro secrets set claude-oauth-token-<name>` for a
  named account no profile's `claude_accounts` lists, where `accounts add` would refuse in a project
  with several profiles and otherwise serve the account from a profile that did not — and
  `claude setup-token; fugaro secrets set claude-oauth-token --project-wide` for
  the default account, whose name is reserved and which no `accounts add` form takes. The OK line
  lists each account job with the repositories its token may be used by, and says that the mount is
  static — so the container holds the *project-wide* copy, and a repository copy does not reach it.
  It prints **no line at all** when the installation serves no account job — there is nothing to
  check, so the check returns nothing rather than a line saying so — and a warning when the
  project's secrets could not be listed at all.
- **`claude-accounts-stored`** — every named account this repository may choose has a stored token,
  in either scope (the repository's own copy if it has one, the project's otherwise). It is silent
  when this repository may choose no named account, and a warning — never silence — when the
  listings could not be made.
- **`oauth-token-allowed`** — the `agent.oauth_token` this checkout sets is one the local config
  allows for this repository, on a workflow whose `agent.auth` is `oauth`. It is silent when the
  key is unset. This is the check that catches a name the local config does not know, one whose
  `allow_data_to` omits the repository, or one set for a non-oauth workflow — all of which
  `fugaro run` refuses anyway, but only once a merged `fugaro.yaml` change has put you there.

The job and launcher reach lines cover account jobs like any other Cloud Run job: an account job is
a job, and who may launch it is the launcher's own reach.

## 10. The interim carrier, and what changes later

The token reaches the job as a **static `secret_env` entry** built by one function,
`projectAccountMount` in [`internal/infra/projectjobs.go`](../internal/infra/projectjobs.go), and
Terraform mounts it as a `secret_key_ref` with `version = "latest"`. This is a stopgap and it is the
design's D1/D2: it needs no new service, it puts the token in the job's container rather than in the
run, and it gives each account job's service account `secretAccessor` on its own account's secret
and on no other's.

The planned **security service** ([credentials-service](credentials-service/README.md), design §4.6)
replaces this carrier: the service holds the tokens and hands one to a run that the policy admits,
and the launcher stops being able to place a credential in a job's definition at all. Until then,
what the seam costs is stated where the operator meets it: **every run of an account's job holds
that account's token**, whatever the run does and whoever launched it — the account is the blast
radius (§1), and `accounts.<name>.allow_repos` plus the two gates are what bound it.

## 11. Limits of this release

- **0.8.x does not read any of it.** The profile field, the `accounts` block and the jobs ship in
  0.9.0; an older binary meeting a version-3 policy refuses it and says which release reads it
  (§5).
- **A GitHub run on a shared job reaches its provider, and it is still not a per-repository job.**
  Every job of a **github** profile carries the App's **ID** in its environment —
  `FUGARO_GITHUB_APP_ID` (`providers.EnvGitHubAppID`), resolved from the installation's local config
  by `projectJobGitHubAppID` ([`internal/infra/projectjobs.go`](../internal/infra/projectjobs.go),
  owner ruling of 2026-10-17, plan task 3.5 step 4) — and the key is the profile's project-wide
  `github-app-key` mount, so the provider the run opens has both halves of the credential it is
  built from; `fugaro setup` refuses a config that records no App ID, or two different ones, rather
  than deploy a job whose runs would halt. `checkout: clone` is **shipped in 0.9.0** (`cloneShipped`,
  [`internal/cli/recipes_skew.go`](../internal/cli/recipes_skew.go)) and was not in the released
  0.8.x, where a clone launch is refused — so on the released CLI no clone run reaches a shared job
  at all. What a shared job still does not carry is the table in
  [project-layer.md §"Which runs work on a shared job today"](project-layer.md#which-runs-work-on-a-shared-job-today):
  no `FUGARO_CHECKOUT` (a full clone rather than a blobless one, and no `mise install`), no
  owner-configured model providers, no local compute-price override, the default budget grace, and
  no repository-level `workflows.<n>.secrets` — only the profile's project-wide ones. **The account
  gates are not what admits such a run, and no live check has proved one end to end on a shared
  job:** the check that would (the plan's own, `docs/project-centric/implementation-plan.md` Task 3.7)
  has not been run. A Bitbucket repository never reaches a shared job at all.
- **No per-account budget dimension.** The budget schema is keyed by model, repository, project and
  person, never by credential: an OAuth run adds to the notional (subscription, unbilled) counters,
  and the account's own limits are Anthropic's, not Fugaro's
  ([named-oauth-tokens/spec.md](named-oauth-tokens/spec.md) §9).
- **Unverified without a live check:** whether one account's token serves several concurrent runs
  comfortably (a sizing question, design §7 U3), and whether the account jobs' IAM members plan as
  pure creates — Task 2's golden plans answer the second; no cloud call was made for either.
- **Minting a second token on a machine that already holds one** is Claude Code's behaviour, not
  Fugaro's, and is unverified here (§2, U2).

## 12. Credentials: the account names, typed

The account above is one type of a more general thing: a **credential** — a name and a type, in the
same local config, in a `credentials:` block. The types the engine reads are:

| Type | What it authenticates with | Served by | Declared by |
| --- | --- | --- | --- |
| `claude-oauth` | a Claude subscription's OAuth token, which is what every account of this page is | the credentials service | `fugaro accounts add <name> --type claude-oauth` |
| `anthropic` | an Anthropic API key | the credentials service | `fugaro accounts add <name> --type anthropic` |
| `openrouter` | an OpenRouter key; the credential's name **is** the provider entry's name ([multi-model.md](multi-model.md) §2) | the run's own gateway | `fugaro accounts add <name> --type openrouter` |
| `vertex` | nothing — it is built in, and it is never defined | the run's own gateway | no command: it exists in every installation |

`fugaro accounts add <name> --type claude-oauth` writes the entry beside the account block it has
always written (§3), and that is what makes the name one a pool (§13) may list. It also puts the
token in a **pool of one named after the account** — `pools.<name>.members = [<name>]` — unless
`--pool <name>` names another pool, which the token joins with the members that pool already has. A
key credential gets no pool: no pool walks a provider key. The pool takes the credential's own name,
which is the one collision §13's namespace rule admits. Without the flag the command writes exactly
what it always wrote — `claude_accounts` and the profile — so nothing on this page changes for an
installation that wants the account alone. A name the config already defines with another type is
**refused** rather than retyped: a type decides which half of a run serves the name, and the file is
where a type is changed.

A credential carries a **name and a type and no grant of its own**. `allow_data_to` (§2, §4) stays
the account's, and every reader that scopes the token's reach still reads it there: a credential a
pool lists is bounded by the account entry the same name holds, not by the pool.

**Three of the four types are declarable by this command, and each of the three is one whose value
it stores.** `--type claude-oauth` stores the account's own token as above; `--type openrouter` and
`--type anthropic` read a **key exactly as the token is read** — from stdin, or at the hidden prompt
on your terminal, and never from an argument, a flag or the environment — and store it project-wide
under the credential's own derived name, `credential-<name>-key`, rather than the account's. A key
credential is added to **no** profile's list and rides no job of its own: what serves its key is the
half its type names ([multi-model.md](multi-model.md) §"Crafts, credentials and pools"), which is
also what the command's last line says. **What serves the key is not the `add` alone.** The
credentials service reads the bundle and nothing else, so an `anthropic` key is served once
`fugaro creds set credential-<name>-key` — the bundle's one writer, refused in a coding agent's
session — has written the value there, and the run's own gateway mounts the key in the job of the
provider of the same name, so an `openrouter` key is served once a `providers.<name>` entry exists.
So `--profile` is **refused** with the two key types, and so
is a name a profile already lists — that list is what deploys the job mounting the account's token,
and the job would read a container nothing writes. Take the name off the list first
(`fugaro accounts rm <name> --profile <profile>`) or declare the key credential under another name.
`vertex` stays **built in** and is refused as such, and `openai` and `codex-oauth` keep refusing as
**not yet**: Claude Code speaks the Anthropic Messages API and the gateway never rewrites a request
body, so a type whose provider does not answer that API needs a design of its own. Nothing stops the
owner defining any of the four by hand in this prefix's `credentials:` block, and the project's own
key path is unchanged: `fugaro secrets set` stores a key the project holds (§5).

**The older keys keep their meaning, and a write converges them.** Every write of this local config
converges the craft vocabulary first
([`internal/cli/init.go`](../internal/cli/init.go)'s `writeLocalConfig`): each `claude_accounts`
entry gains a `credentials` entry of the type every one of them is, and the crafts below (§14) gain
the accesses its stage keys named. It **adds and removes nothing** — `claude_accounts`,
`default_claude_account` and `stages.<stage>.claude_account` (§8) are read exactly as before — so an
installation that never writes a `credentials:` block itself is served by the same names it always
was. `fugaro update` is the command that runs it, and no second command is one.

## 13. Pools: an ordered list of credentials

A **pool** is a name, a strategy and an ordered list of members, where each member is a credential
the same file defines or the built-in `vertex`. Pools and credentials **share one namespace**: a pool
may not take a credential's name, except as that credential's **own pool of one** — the pool whose
only member is the credential the name holds, which is what a subscription token is put in
(D3176).

```yaml
# per installation: the project's local config
credentials:
  - name: acme-main
    type: claude-oauth
  - name: acme-spare
    type: claude-oauth
pools:
  - name: acme-claude
    strategy: failover
    members: [acme-main, acme-spare]
```

`fugaro accounts pool add <name> <member>...` declares the **whole list**, in the order given, so a
second run of the command replaces the members rather than adding to them;
`--strategy failover|round-robin` says how the list is walked, and a new pool with no strategy is
`failover`. `fugaro accounts pool rm <name>` removes the pool and **clears the crafts whose access
named it**, on the same bytes, rather than leave an access pointing at nothing.
`fugaro accounts pool ls` prints each pool's strategy, its members in order and which half of a run
serves them.

All three are **offline** — they read and write this config file and reach no bucket, no database
and no host — and the two writes are the **owner's**: a pool decides where a project's calls may be
spent, so a coding agent's session is refused, whatever it passes (§7's rule, and the same one
`fugaro accounts add` states for the token).

**A pool's members are all served by one half of a run, and a pool may not name members of both.**
An `openrouter` or `vertex` member is served by the run's own gateway; a `claude-oauth` or
`anthropic` member is served by the credentials service, which **selects by tier within a run — a
subscription before an API key** (D154). Neither half can walk an order that spans them — the
service serves by tier, with the listed order only breaking a tie, and a gateway holds one upstream
per stage — so a pool that names a member of each is **refused where it is declared**: `fugaro
accounts pool add`, the one door a new member list comes through, turns it down in one sentence that
names the two members and the half each is served by. The fix the sentence names is two pools, one
per half, with each craft's access naming the one its stages need; `fugaro accounts pool ls` says
which half serves a pool's members. A pool an earlier release wrote is read as it stands — an
installation's own config keeps loading, `fugaro update`'s own load included — and the step that
splits such a pool into one pool per half ships in the same release as this rule.

**A pool of two or more Claude OAuth credentials serves nothing until the owner says otherwise.**
Fugaro spreads one run across subscriptions only once the project's `subscription_spread` switch is
on — `fugaro settings set subscription_spread true`, off by default — and while it is off a craft
whose access names such a pool is **inert**: `fugaro craft ls` marks the row — it is offline, so it
reads the switch from this machine's own config, the value `fugaro update` publishes into the node —
and a call that would have been served by it is refused with the switch named. That is the switch §1's terms question is
about, kept explicit for the reason the accounts' own agreement gives.

## 14. Crafts: which model, and whose credential

There are five **crafts**: `coder`, `review` (the first-line review), `senior-review`,
`security-review` and the escalation ladder's `merge-judge`, which a judge run's single stage is
charged to. Each craft has **two halves** — the model it runs, and the **access**: the credential or
pool its calls go out on — and the two halves **inherit separately**, so a craft that sets one half
keeps the other half it follows:

| Craft | Follows |
| --- | --- |
| `review` | `coder` |
| `security-review` | `senior-review` |
| `merge-judge` | `coder` |

A craft no layer sets an access for resolves to the project's **default credential**, which is the
built-in `vertex` at the floor of the layers.

**Where each half may be written:**

- a craft's **access** is the repository's own act: `agent.crafts.<craft>.access` in `fugaro.yaml`
  (§6's file), and only there. A project layer may set a craft's **model** for every repository of
  it and never its access, because an access names a credential or pool the owner's own config
  holds, and a file anyone who can write the runs bucket can write is not where that choice lives
  ([project-layer.md](project-layer.md)).
- the name must be one the installation **holds** — a credential or a pool of §12 and §13, or
  `vertex`. A name no layer holds is reported by `fugaro craft ls` and by `fugaro validate`, and it
  is refused at the launch, before the run's first call.

`fugaro craft ls` is the read that answers the question the three documents answer only by hand:

Its columns are `CRAFT`, `MODEL`, `MODEL FROM`, `ACCESS`, `ACCESS FROM` and `SERVES`, one row per
craft: the **model** and where it came from (the layer that wrote it — `repo`, `local` or `project`
— the craft it was inherited from, or `default`), the **access** and where that came from, and
whether it **serves** anything yet, which is `yes` or `no: the subscription-spread switch is off`
for the inert pool of §13. It is **offline**: it reads this checkout's
`fugaro.yaml`, this installation's own config and the project layer file the last cloud read cached,
and reaches no bucket, no database and no host. `--json` prints the versioned document the console
reads.

**What a run resolves and what serves it** — the two limits this page carried while the crafts were
being read only, and what stands in their place:

- **A craft's model is what its stages run.** A stage's model is the first of these rungs that
  answers, most specific first: a task's model grade, a review level's own model
  (`agent.levels.l1/l2/l3.model`), **the craft's model**, `agent.models.<role>` and `agent.model`. A
  craft that sets no model therefore resolves exactly as it did before, and both `agent.models` and
  `agent.model` keep every meaning they have. The resolved model is written where the run's own
  readers already read a stage's model, so the pins the agent is given, the gateway's allow-list, the
  price row `cost.go` charges and the run record's `StageTiming.Model` name one answer
  ([multi-model.md](multi-model.md), [models-commands/spec.md](models-commands/spec.md)).
- **The published node is what serves a run.** `fugaro project policy publish` writes the crafts, the
  credentials and the pools **from this installation's own config**, and a run is served from the
  node: the credentials service reads a pool's members there on every call, and the run's own gateway
  reads the craft whose stages it runs. So the pool an access names is the list that serves the
  craft's calls, in the order the node lists its members, on the one half §13 keeps a pool on
  ([project-layer.md](project-layer.md)).

## 15. Aliases: one name the choice keys may hold

An **alias** is another name for one account, so the keys that choose an account can name the alias
instead of the account, and moving every key that names it takes one command rather than an edit per
key. It lives in **your local project config alone** — the block `claude_account_aliases`, beside
`claude_accounts` — and it never reaches the published policy node, a task, a run record or a job image
by its own name (D2090): every reader resolves it first, so what it writes down is the account.

It is local-only in the same sense the choice keys of §8 are, and for the same reason: the block joins
`default_claude_account`, `stages` and `claude_account_auto` in the keys a **shared file** in the runs
bucket may not carry, so a launcher on another machine reads no alias, exactly as it reads no
`default_claude_account` today. It is in no `fugaro.yaml` and in no project layer either — which is why
the tighten-only policy merge never sees one, and why no repository can name one.

```yaml
# per installation: the project's local config
claude_account_aliases:
  reviewer: acme-team       # alias -> one account of claude_accounts (§3), or default
default_claude_account: reviewer
stages:
  review: {claude_account: reviewer}
```

**The one command**, per installation, in your own terminal:

```bash
fugaro accounts alias set <alias> <account>
fugaro accounts alias rm <alias>
```

`set` declares the alias or repoints it and then converges in the same command — the policy node with
the account the keys now resolve to, the job set, and this checkout's repository root, through the
stages `fugaro update` runs (§5) — so its last line is what is left, which is nothing. It **adds** the
new target's job and **never deletes** the old target's (D2095): the account may still be served by
another profile or named by another alias, and a job delete takes the executions on it. It asks nothing
— no typed name and no confirmation — and a coding agent's session is refused before it reads anything
(D2097), as `fugaro accounts add`'s token step is, because a repoint decides which credential this
project's stages spend.

`alias rm` is the inverse, and it is **refused while a choice key still names the alias**, naming every
key: the key would resolve to a name this config no longer defines. Point those keys at the account they
should choose instead (`fugaro accounts alias set <alias> <account>`) or change them, and remove the
alias once nothing names it. `fugaro accounts rm <account>` is refused the same way while an alias
stands for the account, naming the `alias set` that moves it (D2101).

**What may name an alias (D2091):** `default_claude_account`, every `stages.<stage>.claude_account`
(§8), and `fugaro run --token` / `fugaro review --token`. Nothing else does: a committed
`agent.oauth_token` in `fugaro.yaml` names an account and never an alias, because a repository's own
file may not depend on a name only your config holds, and neither may the lists that decide which jobs
exist — `profiles.<p>.claude_accounts` and `job_profiles.*.claude_accounts`.

**What an alias may stand for (D2093):** one account `claude_accounts` defines, or `default` for the
unsuffixed account. Never another alias (a chain would need a cycle check and a reader that prints the
path a name came through), never a pool (its members are chosen inside the run, at the stage boundary,
which is the re-resolution a repoint must not cause), and never a credential of another type. Its own
name follows the account name rule and the credential names that are reserved, in **one namespace** with
`claude_accounts`, `credentials` and `pools` (D2092), so a name written in a choice key always means
exactly one thing.

**A repoint changes new launches only.** The launch resolves the alias, and it does so **before** the
account's own `allow_data_to` is asked about it: the allow-list is asked about the account the run will
hold and never about the alias, so an alias is no way around an account's own list. What the run then
carries is the resolved account — the task's account, the job variant it lands on, and the record's
`oauth_account` and `stage_accounts` all name the account, and no alias field is added anywhere
(D2099). **A run already in flight keeps the account its launch resolved**: its job mounts that
account's token, and the runner reads the account choice once, at bootstrap, so a repoint never rewrites
a record, a ledger or a counter. A follow-up keeps its previous run's account too, as it does today;
`--pr N --token <alias>` is what moves one, and it takes the alias's current target (D2098).

**What the reads show (D2099).**

```bash
fugaro accounts ls --json
```

`fugaro accounts ls` prints each alias and the account it stands for, one `alias -> account` line per
alias after the account names, and `--json` gains an additive `aliases` member (alias to account) — a
field added within the document's own version, so a reader of an older release sees exactly the
document it always did. `fugaro settings` shows the `claude_account_aliases.<alias>` rows, owned by the
`accounts` family. A launch whose account came through an alias prints the pair, `ALIAS -> ACCOUNT`, on
its own line. The run record names the resolved account only.

**What an alias is not.** `claude_account_auto`'s walk is a walk over the accounts, and an alias is
never one of its candidates; a pool may not list an alias, and a craft's access may name one only in a
later release, after the stage keys move into it (D2100). Nothing about this adds reach: the target must
be an account you already defined, every name is checked against that account's own `allow_data_to`
after resolution, and the block lives only where you alone write — so a repoint moves which admitted
account a stage uses, and cannot make an account usable by a repository that account does not admit.

## 16. Moving every key from one account to another: one command

Retiring an account is one command rather than an edit per key, and the keys it moves are the whole of
what names an account as a choice, in **your local project config**:

- `default_claude_account`;
- each `stages.<stage>.claude_account`;
- each `crafts.<craft>.access` (§14);
- each `repos.<repo>.claude_accounts` entry (§3);
- each `profiles.<profile>.claude_accounts` entry (§3).

```bash
# per installation, in your own terminal
fugaro accounts switch <from> <to>
fugaro accounts switch <from> <to> --dry-run
```

`switch` prints one line per key it rewrites — the key as the file spells it and the move it takes, as
in `default_claude_account: belong -> bmb` — and writes them in one edit. Then it converges, in the
same command and after the write, exactly as `fugaro accounts alias set` does (§15): the policy node,
the job set and this checkout's repository root, through the stages `fugaro update` runs, so its last
line is what is left, which is nothing. It asks nothing, and a coding agent's session is refused
before it reads anything, as every command that decides which credential this project's stages spend
is. `--dry-run` prints the keys and the diff and writes nothing.

**The two blocks that are not the choice are left alone.** `claude_accounts` keeps both names and
`credentials` keeps both credentials — the account moved off stays declared, with its token and its
job — so a switch is reversible by running it the other way, and nothing this command does deletes a
secret, a job or an execution.

**`<to>` must already be declared here**, as an account of `claude_accounts` — never an alias standing
for one, and never `default`, which is the unsuffixed secret rather than a declaration. Its own
`allow_data_to` must cover every repository the keys it takes over serve: each repository whose own
`claude_accounts` list names it, and every `allow_repos` entry of a profile that lists it (§4's
intersection rule, asked of the account the keys would now name). A repository a switch would move
onto an account whose token may not reach it is refused before anything is written, naming the one
command that widens the list — `fugaro accounts allow <to> <owner/name>`.

**`<from>` may be a declared account, `default`, or a craft's own credential.** It may not be an
**alias**: the keys naming one are meant to keep naming it, and what moves is the block's single entry
— which is what `fugaro accounts alias set <alias> <account>` does, in one edit, for every key (§15).
It may not be a **pool** or one of a pool's **members** either (§13): a pool's members are chosen
inside a run, at the stage boundary, so pointing a craft that names a pool at one account would change
what this project spends without changing the pool's own answer. Both refusals name the command that
settles them.

**A switch changes new launches only.** A run already in flight keeps the account its launch resolved:
its job mounts that account's token and the runner read the account choice once, at bootstrap, so
nothing here rewrites a record, a ledger or a counter
([design/update-in-flight.md](design/update-in-flight.md)).

## See also

- [project-named-oauth-tokens/spec.md](project-named-oauth-tokens/spec.md) — the design, its
  decisions (D1 to D10) and its threat model.
- [named-oauth-tokens/spec.md](named-oauth-tokens/spec.md) — the repository-level feature this
  one carries to the project job.
- [project-layer.md](project-layer.md) — profiles, `job_profile` and the project layer, and which
  layer may write each craft key.
- [multi-model.md](multi-model.md) — the provider route a craft's access may name, and the gateway
  that serves it.
- [models-commands/spec.md](models-commands/spec.md) — `fugaro models` and the model keys a run
  reads today.
- [gcp-setup.md](gcp-setup.md) — the page that creates the GCP project a shared job runs in.
- [SECURITY.md](../SECURITY.md) — what a project job holds, stated as this feature's cost.
