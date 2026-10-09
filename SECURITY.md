# Security policy

## Reporting a vulnerability

Please do not open a public issue for a vulnerability. Use GitHub's private vulnerability reporting: open the repository's **Security** tab, then **Report a vulnerability**. Include what you found, how to reproduce it, and the version (`fugaro version`) or commit.

We will acknowledge a report as soon as we can, work on a fix with you privately, and credit you in the advisory if you wish. This is a small project maintained by volunteers, so we cannot promise a fixed response time.

## Supported versions

Only the latest 0.x release is supported. Fugaro is pre-1.0; fixes land on `main` and ship in the next release.

## Trust model and known limits

Fugaro runs a coding agent with its permission prompts turned off, in a fresh cloud container per run, and ends every run with a pull request. This section says what that design protects, what it does not, and what you should do about the gaps. It describes the code as it is; the details, with file references, are in [docs/design/v1.md §6](docs/design/v1.md#6-security-model).

### The assumption

**The design assumes you trust the repository and the source of the task.** Everyone who can push to the repository, everyone who can launch a run, and (for follow-up runs) the people listed in `followup.trusted` can steer an agent that holds the credentials below. Fugaro is built for a team running it on its own code. It is not a sandbox for code you would not run on a developer's laptop.

### What limits the blast radius today

- **One container per run, discarded at the end.** Nothing in the container itself persists between runs. What does persist is in the runs bucket by design: a cache a later run restores (`cache/<slug>/`) and a session a follow-up may resume (see below). The agent runs as an unprivileged user with no sudo and no setuid escape (the image self-test checks this on every build).
- **Per-repository identities.** Each repository's jobs and builds run as their own service accounts. A job can read only the secrets its own workflow declares (not every secret the repository has), and it can read and write only its own repository's prefixes in the runs bucket. That scoping is the job account's alone; it does not mean the bucket itself is isolated between repositories or between launchers — see "What is not on by default" below.
- **Cloud keys stay in the cloud.** No command copies a secret value back to your machine, and `fugaro secrets` never prints one.
- **With the budget gateway on, model keys stay out of the agent's environment.** With budget `observe` or `enforce` and `auth: api-key`, the agent gets a per-run gateway token instead of the real key, and the gateway holds it, pins the models and charges every call against the caps. This is off by default, and it never applies to `auth: vertex` under `enforce` at all (see "What is not on by default" below).
- **Narrow git credentials.** On GitHub, a run gets an installation token for its one repository (contents and pull requests write, issues and metadata read) that expires within about an hour. The App has no `workflows` permission, so a run cannot change `.github/workflows/`. On Bitbucket, it is a repository access token, scoped to that one repository by Bitbucket's own design.
- **Logs are isolated** in their own log bucket, readable by the people you made launchers, and redacted of every mounted secret, including common encodings. Redaction is best effort. Project Owners and Editors can still read every log, isolated or not. Cloud Build's own logs are never moved there: they carry a repository's build output, which by design never includes a secret value. `fugaro init --no-log-isolation` leaves Fugaro's own logs in `_Default` too.

### What is not on by default

Three gaps are easy to miss, because nothing warns you about them:

- **Budget mode defaults to off.** With no `budget:` block, or an empty `mode`, there is no gateway and no per-call cap: the real model key sits in the agent's environment like any other secret, and the only limits are the provider account's own limit and the token cap.
- **Vertex has no dollar cap, in any mode.** `auth: vertex` with budget `enforce` is refused outright, before the run starts: a Vertex repository can only run `observe` or off. There is also no key to put a credit limit on: the job's Vertex calls are authenticated by the job's own service account through the metadata server, not a key you hold.
- **Every launcher can write the whole runs bucket.** Until the bucket IAM hardening ships (release 0.7.0, [docs/design/bucket-iam.md](docs/design/bucket-iam.md) §1 and §2.1), every launcher — not only operators — holds `roles/storage.objectAdmin` on the entire runs bucket, unconditioned, including the installation-wide project layer, recipes and shared config. A launcher can rewrite the project layer's commands or add an `image.setup` that runs in the next rebuild, replace a recipe or the shared config, or poison another repository's cache — all without launching a run, reaching that repository's secrets through its next build or job. The same role also lets any launcher list every secret in the project (names, labels and version counts; reading a value still needs `secretAccessor`, which only the job and build accounts hold).

### Known limits

These are real, current gaps. Each is a decision or a later hardening item, not an oversight.

- **What one run's agent can reach.** On a first run the agent's environment holds:
  - the repository's git token (as a credential helper, `FUGARO_GIT_TOKEN` and `GH_TOKEN`);
  - the per-run gateway token;
  - every secret the workflow declares.

  It shares the container and its user with the runner, so it can also read the runner's environment through `/proc`. That includes:
  - **the GitHub App's private key**, which can mint tokens for every repository the App is installed on (use one App per repository if that matters to you);
  - the real model and provider keys;
  - the job's service account, through the metadata server.

  Commands run through `fugaro verify` see the agent's full environment.
- **The managed settings file only guards against committed configuration.** The runner writes it before every stage, with the highest precedence, so a repository's own `.claude/settings.json` can't reroute the agent's model calls by itself. But the file is writable by the agent's own user — the runner's — so a compromised agent can edit it mid-stage, or read the gateway token and the real key from the runner's `/proc` directly, same as above.
- **A follow-up's configuration can be stale, but its working tree isn't.** A follow-up reads `fugaro.yaml`, `agent.instructions` and the review prompt from the base branch, but Claude Code still loads `CLAUDE.md`, `.claude/` (skills, settings, subagents) and `.mcp.json` from the pull request's own branch, so a hook or MCP server planted there runs with the job's full environment and secrets. Review a PR's diff with that in mind before launching a follow-up on it.
- **Any launcher can mint a budget identity for any repository.** The signing account that mints a run's Firebase custom token holds no roles itself, but every launcher — not only the ones who onboarded that repository — holds `signJwt` on it, so a launcher credential is budget write access up to the caps for every repository, not only their own.
- **A follow-up's protections are hygiene, not boundaries.** Pull request comments sit inside a nonce-delimited block, but a model can still follow instructions inside it; the delimiter only keeps a legitimate review comment from being mistaken for one. A resumed session is replayed as the agent's own earlier turns, outside that block, so anyone who could write to a previous run's session file — any run of the same repository, since they share the job's service account and bucket prefix — can plant context there.
- **A proxied network's credentials reach the agent.** `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` pass through verbatim so the agent works on proxied networks; a proxy URL with `user:password@` embedded is as readable to the agent as any other variable. Authenticate your proxy by network or identity, not a URL credential.
- **Network egress is not restricted.** A run can reach any host. Prompt injection through the repository, a dependency, an issue or a PR comment could send source code or the credentials above anywhere. A VPC with egress rules is a later hardening option, not built.
- **The base branch is protected only by your branch protection.** The runner pushes only to `fugaro/<run-id>` branches, but the agent holds a token with contents write and can run `git push` itself. Require reviews and status checks on your default branch: Fugaro relies on it.
- **The agent can open or edit pull requests itself** with `gh`. Its instructions tell it not to; nothing technical stops it.
- **Money.** The gateway's caps bound a run that goes through the gateway. An agent that reads the real key from `/proc` can spend outside it. The backstop is the provider account's own limit: set a credit limit on every key you give Fugaro (Anthropic workspace limits, OpenRouter credit limits; Vertex has none to set, see "What is not on by default" above). With `auth: oauth` there is no gateway, only the token cap and the kill switches. A run's budget identity can reserve, up to the caps, until it expires (job timeout plus about an hour), even after the run ended.
- **Runs of one repository can see each other, and the 0.7.0 bucket IAM work does not change that.** They share a service account and its bucket prefixes, so a hostile run can read or overwrite another run's records and caches in the same repository. [docs/design/bucket-iam.md](docs/design/bucket-iam.md) is explicit that a compromised agent inside a run is unchanged by that work; it narrows what *launchers* can write (see "What is not on by default" above), not what a run's own job account can reach.
- **Follow-up runs act on pull request comments** from the people in `followup.trusted` on the base branch (and, on GitHub, only owners, members and collaborators among them). A trusted person who quotes an untrusted comment passes it on. A follow-up's agent gets no git credential and no `GH_TOKEN` in its own environment — the runner fetches and pushes the branch itself, and the comments arrive in the prompt — but it still shares the container and user with the runner, so it can read the token from the runner's `/proc`, the same residual as the model and provider keys above.

### Running Fugaro on repositories with external contributors

- Do not launch runs whose task text you copied from an untrusted issue or comment without reading it: the task is an instruction the agent follows.
- Keep `followup.trusted` to people with write access.
- **A fork's pull request gets no special handling today.** If a run checks out and executes a fork's code — a build script, a test command — that code runs with the workflow's secrets, the same as any other code a run touches. Treat a fork's pull request with the same caution as an untrusted task. (A mode that refuses a fork's pull request by default is designed but not yet built.)
- Give Fugaro its own provider keys with credit limits, and its own GitHub App, installed only on the repositories it serves.
- Public repositories are refused for follow-ups unless `followup.allow_public` is set, for the same reason.

## Scope

In scope:

- The budget gateway, the Realtime Database rules and token handling that enforce the shared budget
- Credential handling: secrets in Secret Manager, `fugaro secrets`, redaction of logs and PRs, the runner's use of provider and model tokens
- The IAM and Terraform that `fugaro init` creates (per-repository isolation, custom roles, bucket conditions)
- The CLI and the runner (for example path or command injection from a task, a repository's `fugaro.yaml` or a PR comment)
- The release pipeline (checksums, signatures, the Homebrew cask)

Out of scope:

- An agent doing harmful things inside its own container with the credentials that job's service account is allowed to hold: the trust boundary is the container, and the design says so openly
- Problems that need an attacker who already holds your cloud project's owner credentials
- Vulnerabilities in Google Cloud, GitHub, Bitbucket or a model provider themselves; report those to the vendor
- Denial of service by spending your own budget

The trust boundary and what each credential can reach are described in [docs/design/v1.md](docs/design/v1.md#6-security-model) (section 6, Security model).
