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

- **One container per run, discarded at the end.** Nothing persists in it between runs. The agent runs as an unprivileged user with no sudo and no setuid escape (the image self-test checks this on every build).
- **Per-repository identities.** Each repository's jobs and builds run as their own service accounts. A job can read only its own repository's secrets and its own repository's prefixes in the runs bucket (the bucket's IAM is being tightened in 0.7.0: [docs/design/bucket-iam.md](docs/design/bucket-iam.md)).
- **Cloud keys stay in the cloud.** No command copies a secret value back to your machine, and `fugaro secrets` never prints one.
- **Model keys stay out of the agent's environment** when the budget gateway is on (`auth: api-key` or `vertex`, budget `observe` or `enforce`): the agent gets a per-run gateway token, and the gateway holds the real key, pins the models and charges every call against the caps.
- **Narrow git credentials.** On GitHub, a run gets an installation token for its one repository (contents and pull requests write, issues and metadata read) that expires within about an hour. The App has no `workflows` permission, so a run cannot change `.github/workflows/`.
- **Logs are isolated** in their own log bucket, readable by the people you made launchers, and redacted of every mounted secret, including common encodings. Redaction is best effort.

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
- **Network egress is not restricted.** A run can reach any host. Prompt injection through the repository, a dependency, an issue or a PR comment could send source code or the credentials above anywhere. A VPC with egress rules is a later hardening option, not built.
- **The base branch is protected only by your branch protection.** The runner pushes only to `fugaro/<run-id>` branches, but the agent holds a token with contents write and can run `git push` itself. Require reviews and status checks on your default branch: Fugaro relies on it.
- **The agent can open or edit pull requests itself** with `gh`. Its instructions tell it not to; nothing technical stops it.
- **Money.** The gateway's caps bound a run that goes through the gateway. An agent that reads the real key from `/proc` can spend outside it. The backstop is the provider account's own limit: set a credit limit on every key you give Fugaro (Anthropic workspace limits, OpenRouter credit limits). With `auth: oauth` there is no gateway, only the token cap and the kill switches. A run's budget identity can reserve, up to the caps, until it expires (job timeout plus about an hour), even after the run ended.
- **Runs of one repository can see each other.** They share a service account and its bucket prefixes, so a hostile run can read or overwrite another run's records and caches in the same repository. The 0.7.0 bucket IAM work narrows this; read [docs/design/bucket-iam.md](docs/design/bucket-iam.md) for what it changes.
- **Follow-up runs act on pull request comments** from the people in `followup.trusted` on the base branch (and, on GitHub, only owners, members and collaborators among them). A trusted person who quotes an untrusted comment passes it on.

### Running Fugaro on repositories with external contributors

- Do not launch runs whose task text you copied from an untrusted issue or comment without reading it: the task is an instruction the agent follows.
- Keep `followup.trusted` to people with write access.
- Never run a fork's pull request through an implementing recipe: its build scripts run with the workflow's secrets.
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
