# Security policy

## Reporting a vulnerability

Please do not open a public issue for a vulnerability. Use GitHub's private vulnerability reporting: open the repository's **Security** tab, then **Report a vulnerability**. Include what you found, how to reproduce it, and the version (`fugaro version`) or commit.

We will acknowledge a report as soon as we can, work on a fix with you privately, and credit you in the advisory if you wish. This is a small project maintained by volunteers, so we cannot promise a fixed response time.

## Supported versions

Only the latest 0.x release is supported. Fugaro is pre-1.0; fixes land on `main` and ship in the next release.

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
