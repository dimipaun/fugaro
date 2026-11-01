# Project Claude accounts: one OAuth token per (profile, account)

Status: **0.9.0, released.** The released Fugaro is 0.14.1, and 0.9.0 is the first release any of this page works on:
the `claude_accounts` field inside a profile, the `accounts` block in the published policy and the
jobs they name all ship in 0.9.0 (policy version 3,
[`internal/projectpolicy/version.go`](../internal/projectpolicy/version.go)), and a 0.8.x binary
reads none of it. The page is kept out
of the docs hub on the site until the hub lists it, and every command below is a 0.9.0 or later
`fugaro` binary. The design it documents is
[design/project-named-oauth-tokens.md](design/project-named-oauth-tokens.md).

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

## 1. What a named account is

The Claude OAuth credential was one secret per repository: `claude-oauth-token`, mounted into the
job as `CLAUDE_CODE_OAUTH_TOKEN`
([design/named-oauth-tokens.md](design/named-oauth-tokens.md) §1). Named accounts
([design/named-oauth-tokens.md](design/named-oauth-tokens.md)) made *which* token a repository
uses a choice: `agent.oauth_token` in `fugaro.yaml`, or `fugaro run --token`.
[design/project-named-oauth-tokens.md](design/project-named-oauth-tokens.md) carries that choice to
the project job, where the credential is a fact of the **job** and not of the run.

A named account is:

- **a name**, `claude_accounts.<name>` in the project's local config, which decides the
  repositories that may use its token (`allow_data_to`). The reserved name `default` is the
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

## 2. Mint the tokens and store them project-wide

`claude setup-token` is Anthropic's own command and the only way to mint a token; Fugaro never
mints one, never prints one and never takes one as an argument.

1. **Mint — per installation machine, in your own terminal window.**

   ```bash
   claude setup-token
   ```

   Run it as the person or team whose subscription the token belongs to. One token per account:
   mint a separate token for each name you are about to declare, signing in to the account that name
   is for. What Claude Code does when a machine already holds a token for another account (whether
   it logs out first, takes a flag, or wants a separate profile) is not verified by Fugaro — no live
   check has been run ([design/named-oauth-tokens.md](design/named-oauth-tokens.md) §7, U2) — and
   `claude setup-token --help` in
   your own install is the authority. Nothing in Fugaro depends on the answer: what it stores is
   whatever value you paste.

2. **Store — per installation, in your own terminal window, in a checkout or not.** The value goes
   in on stdin or at a hidden prompt, never on the command line:

   ```bash
   fugaro secrets set claude-oauth-token-<name>
   ```

   With neither flag, a named account's token is stored **project-wide** — the project's own Secret
   Manager, the only scope a shared job can mount — and `fugaro secrets set claude-oauth-token-<name>
   --project-wide` says the same thing explicitly. It is accepted only when
   `claude_accounts.<name>.allow_data_to` in the project's local config lists at least one
   repository (§4); otherwise the command is refused and names that field, because a token whose
   repositories are undecidable is one no policy can bound.

   **Every command on this page carries placeholders, never a value:** `<name>` is the account's
   name and `<token>` is the value you paste, so `fugaro secrets set claude-oauth-token-<name>` is
   a command you run as written with the value arriving at the prompt.

   The **default** account's token is the unsuffixed `claude-oauth-token`, and it is stored
   project-wide only when `project_secrets` lists it:

   ```bash
   fugaro secrets set claude-oauth-token --project-wide
   ```

   Without a `project_secrets.claude-oauth-token` entry with a non-empty `allow_data_to`, that
   command is refused — the name has a project-wide meaning only when the project states which
   repositories it is for.

   **A repository copy does not reach a project job.** `fugaro secrets set claude-oauth-token-<name>
   --repo acme/webapp` (per installation, naming the repository; or per repository checkout without a
   value, which uses this checkout's origin) stores the repository's own copy, which overrides the
   project copy for that repository's *per-repository* jobs. An account job mounts the project-wide
   copy by its static `secret_env` entry, so the repository copy is not what its runs hold —
   `fugaro doctor` says exactly this when a token is missing (§9).

3. **Confirm — per installation.** Names only, never a value:

   ```bash
   fugaro secrets ls --project-wide
   ```

## 3. Declare the account in the project policy

Two edits, both in the operator's local config — `~/.config/fugaro/projects/<project>.yaml`, per
installation, operator-only, never published to the runs bucket.

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

## 4. The rules `publish` and `fugaro setup` enforce

Neither the `accounts` block nor the profile field is written by hand into the policy: both are
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
  (`internal/credsvc/bundle.go`, `schemas/creds-bundle.schema.json`), and `fugaro validate` and
  `fugaro project policy publish` refuse the account name itself, so the clash is met where the
  account is written rather than at `fugaro creds set` or at the service. **Fix:** rename the
  account in `claude_accounts` and store its token under the new name. Every *other* name a
  credential of the platform goes by stays an account name — `github-app-key` and
  `anthropic-api-key` among them, since the bundle finds the App key by its kind rather than in the
  index of names, and refuses the paid key only where one document carries both it and an account of
  its own name.

The subset rule is an **intersection, not a union**: a profile is not "some account of mine carries
this repository" — the repositories it admits are the ones **every** account it lists carries. So a
profile whose list names a repository that even one of its accounts does not carry is refused at
publish (`projectpolicy`'s `validProfileAccounts`), with a refusal naming both lists and the
repository in one and not the other: the repair is the account's `allow_data_to` (add the
repository there and publish again) or the profile's `allow_repos`, and the operator meets that
decision where they make it rather than at a run.

## 5. What `fugaro setup` deploys, and the version-3 rollout

**Per installation** (and per repository checkout for `fugaro update --repo .`), `fugaro setup` deploys
one Cloud Run job and one job service account per `(profile, provider, Claude account)`. The account
is the last segment of the job's name (`gcp.ProjectJobName`), and the job's service account holds
`secretAccessor` on its own account's secret and on no other account's — that is the whole of who
can read a value. A profile that lists no accounts deploys exactly what it deployed before.

The published policy carries the account axis in a node the previous release cannot read, so
**0.9.0's accounts need all four of these**, in this order:

1. **Upgrade the CLI, per installation machine:**

   ```bash
   brew upgrade dimipaun/tap/fugaro && fugaro upgrade
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
   hand with `fugaro project repos add` is dropped. The node's `clone_urls` block (no command of
   this release writes one) and any `routing` block a person wrote are dropped with it: a publish
   carries those only from a node it replaces. So run this **per installation** before the delete
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

It prints the profiles and, for each account some profile names, the repositories its token may be
used by. Only accounts a profile names are published: an account nothing lists is one no job
mounts, and a block entry for it would publish a list on a node every run of the project can read.

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
repository, sorted — that is the list `--token` accepts. `fugaro runs ls` grows an `ACCOUNT` column
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
  and the token, with the fix on the line:
  `claude setup-token; fugaro secrets set claude-oauth-token-<name> --project-wide`. The OK line
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
  job:** the check that would (the plan's own, `docs/plans/2026-10-14-project-centric.md` Task 3.7)
  has not been run. A Bitbucket repository never reaches a shared job at all.
- **No per-account budget dimension.** The budget schema is keyed by model, repository, project and
  person, never by credential: an OAuth run adds to the notional (subscription, unbilled) counters,
  and the account's own limits are Anthropic's, not Fugaro's
  ([design/named-oauth-tokens.md](design/named-oauth-tokens.md) §9).
- **Unverified without a live check:** whether one account's token serves several concurrent runs
  comfortably (a sizing question, design §7 U3), and whether the account jobs' IAM members plan as
  pure creates — Task 2's golden plans answer the second; no cloud call was made for either.
- **Minting a second token on a machine that already holds one** is Claude Code's behaviour, not
  Fugaro's, and is unverified here (§2, U2).

## See also

- [design/project-named-oauth-tokens.md](design/project-named-oauth-tokens.md) — the design, its
  decisions (D1 to D10) and its threat model.
- [design/named-oauth-tokens.md](design/named-oauth-tokens.md) — the repository-level feature this
  one carries to the project job.
- [project-layer.md](project-layer.md) — profiles, `job_profile` and the project layer.
- [gcp-setup.md](gcp-setup.md) — the page that creates the GCP project a shared job runs in.
- [SECURITY.md](../SECURITY.md) — what a project job holds, stated as this feature's cost.
