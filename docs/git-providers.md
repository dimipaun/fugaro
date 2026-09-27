# Git providers

Fugaro opens pull requests on **Bitbucket Cloud** and **GitHub**. It picks the provider from `git.provider` in the repository's `fugaro.yaml`. The runner may need credentials before it has read that file, for its first `git fetch`. In that case it takes the provider from the origin URL's host (`bitbucket.org` or `github.com`), or from `fugaro exec --provider` (default `$FUGARO_GIT_PROVIDER`). Every source that names a provider must agree with `git.provider`, or the run fails with `infra_error`.

## Credentials

The platform injects credentials into the runner's environment from Secret Manager. They never go in `fugaro.yaml`.

| Variable | Provider | Value |
|---|---|---|
| `FUGARO_BITBUCKET_TOKEN` | bitbucket | Repository access token |
| `FUGARO_BITBUCKET_API_URL` | bitbucket | Optional API root (default `https://api.bitbucket.org/2.0`) |
| `FUGARO_GITHUB_APP_ID` | github | GitHub App ID (or client ID) |
| `FUGARO_GITHUB_APP_PRIVATE_KEY` | github | The App's private key, PEM (PKCS#1 as GitHub issues it, or PKCS#8) |
| `FUGARO_GITHUB_APP_PRIVATE_KEY_FILE` | github | A file holding the key, instead of the variable above |
| `FUGARO_GITHUB_API_URL` | github | Optional REST root (default `https://api.github.com`; GitHub Enterprise Server: `https://<host>/api/v3`) |
| `FUGARO_GIT_PROVIDER` | both | Default for `fugaro exec --provider` |

None of these variables reaches the agent. Every credential value is redacted from transcripts, logs, the pull request, the report and the run record.

### Bitbucket Cloud

Create a **repository access token** under *Repository settings → Security → Access tokens*, with these scopes:
- **Repositories: Read, Write** (to fetch and push)
- **Pull requests: Read, Write**

Git uses it with the username `x-token-auth`. The token is scoped to its repository by design, which makes it the per-repo credential that design §6.1 asks for.

- **Reviewers** (`git.pr.reviewers`) are account UUIDs (`{…}`) or account IDs, not usernames. If Bitbucket rejects one, the PR is opened without reviewers and the run logs a warning.
- **Labels** (`git.pr.labels`) are ignored. Bitbucket Cloud pull requests have no labels. The run logs a warning about this once, to its stderr log.
- **Repository names** are matched in lowercase when finding an existing pull request, since Bitbucket stores workspace and repository slugs in lowercase.

### GitHub

Create a **GitHub App** and install it on the repositories Fugaro serves (design §6.1). It needs these repository permissions:
- **Contents: Read & write**
- **Pull requests: Read & write**
- **Issues: Read**
- **Metadata: Read**

Store its App ID and private key as the variables above. At bootstrap, the runner mints an **installation token for this repository only**, restricted to the permissions above. It lasts about an hour. Before each stage, the runner replaces it with a fresh one if it would expire within that stage's timeout plus 5 minutes, but it never asks for more than 50 minutes, which is all GitHub can give. So with `timeouts.stage` above about 45 minutes, a stage can outlive its token: the agent's `git` and `gh` calls late in that stage fail, and only the refresh before the next stage (or before finalize's push) restores working credentials.

- **Reviewers** are user logins, or `org/team` for a team.
- **Labels** are added after the PR is created. If that fails, the PR stays and the run logs a warning.

## What the agent gets (design §6.2)

The agent's environment carries a short-lived, repository-scoped token. For Bitbucket this is the repository access token itself. It is a convenience, not a security boundary (design §6.1).

- `FUGARO_GIT_TOKEN` and `FUGARO_GIT_USERNAME`, used by the credential helper below.
- `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_n` and `GIT_CONFIG_VALUE_n`. These install a credential helper, scoped to the origin's host, that answers from the two variables above. They also reset any other helper configured for that host. Plain `git push` and `git fetch` just work.
- `GH_TOKEN` (GitHub only), so `gh pr view`, `gh pr edit` and `gh issue view` work.

The token is never written to `.git/config`, to a remote URL, or to a command line.

## Pushing and draft pull requests

- **Pushing.** Finalize pushes only the run's own `fugaro/<run-id>` branch, with `--force-with-lease`. It overwrites the remote branch only when the branch is absent or its tip is one of the run's own commits: an ancestor of the final HEAD, or a commit in HEAD's reflog. That covers the agent pushing the branch and then amending or rebasing. A tip merely present in the checkout, for example because a `git fetch` brought in someone else's push, does not count. A tip pushed from anywhere else is left alone, and the run fails with a clear reason. The base branch is never pushed. Protect it anyway (design §6.1).
- **Draft PRs on GitHub.** Draft state is changed through GraphQL, because REST cannot change it. Some plans have no draft PRs for private repositories. There, Fugaro opens a normal PR titled `[DRAFT] …`, and removes the prefix when a later run marks the PR ready.
- **Draft PRs on Bitbucket Cloud.** The REST API documents a `draft` boolean on create and update, which needs `pullrequest:write`. A repository access token can hold that scope. Fugaro sends `draft` and checks the response. If Bitbucket did not make the PR a draft, it uses the same `[DRAFT] ` title fallback.
- **Which direction a failure falls in.** When `EnsurePR` cannot fully apply the draft state, it still returns the pull request. The error it returns depends on which way the PR is wrong. If the PR is left looking *more* like a draft than asked, for example still a draft or still titled `[DRAFT] …` when ready was wanted, the error is a `*gitprov.PartialError`. The run then records a draft outcome, naming the failure, and never reports the PR as ready. If the PR is left looking *more ready* than asked, the error is a plain one, and the runner retries `EnsurePR`. The usual case is Bitbucket ignoring `draft: true`, after which the retitle that adds the `[DRAFT] ` prefix fails. Each retry finds the same PR and tries the prefix again. If the retries run out, the run ends in `infra_error`, the PR is kept in the run record, and it may still look ready. Check it by hand.

## Live check against a sandbox repository

Hermetic tests cover the adapters with recorded HTTP fixtures (`internal/gitprov/*/testdata`). Before a release, and once for each provider as soon as credentials exist, run against a throwaway repository:

1. **Create the sandbox.** Make a repository containing `testdata/fixture-repo`, with `git.provider` set to that provider, and create its credential as described above.
2. **Write a task file:**
   ```bash
   cat > /tmp/task.json <<'JSON'
   {"version":1,"repo":"<owner>/<sandbox>","ref":"main","task":"Add a line to README.md saying hello."}
   JSON
   ```
3. **Run the task:**
   ```bash
   export FUGARO_BITBUCKET_TOKEN=…   # or FUGARO_GITHUB_APP_ID and FUGARO_GITHUB_APP_PRIVATE_KEY_FILE
   export ANTHROPIC_API_KEY=…        # the sandbox's fugaro.yaml uses agent.auth: api-key
   export FIXTURE_FAILS_FILE=/tmp/fugaro-fails   # the fixture declares it as a workflow secret
   fugaro exec --bucket file:///tmp/fugaro-bucket --task-file /tmp/task.json \
     --workdir /tmp/fugaro-work --state-dir /tmp/fugaro-state \
     --remote https://bitbucket.org/<owner>/<sandbox>.git
   ```
4. **Check the result:**
   - the PR exists, is ready or draft as the run record says, and carries the report comment
   - `.git/config` in `/tmp/fugaro-work` holds no token
5. **Force a draft.** Run a failing task, for example with `FIXTURE_FAILS_FILE` naming a test. Check that the PR is a real draft. On Bitbucket this answers design §15. If the PR carries the `[DRAFT] ` prefix instead, record that in §15.
6. **Verify a title-only Bitbucket update preserves reviewers and description.** With an existing Bitbucket PR that has reviewers and a description set, trigger an update that only changes `title` and `draft` (for example, mark the run's task ready after it first went out as a draft). Confirm with `GET …/pullrequests/{id}` afterward that the reviewers list and description are unchanged — a `PUT` with only `{title, draft}` must not clear fields it did not mention. If Bitbucket's API turns out to require the full object on every `PUT` (clearing omitted fields), that is a live-run finding to fix, not something the hermetic fixtures can catch.
7. **Verify Bitbucket's `draft` field on create and on update.** Open a PR with `draft: true` and confirm `GET` shows it as a draft; then update it with `draft: false` and confirm `GET` shows it as ready. If either direction is ignored (the field round-trips but doesn't change PR state, or a plan/workspace tier doesn't support drafts), the adapter's `[DRAFT] ` title-prefix fallback is the one actually in effect for that workspace — record which case applies in your operational notes and treat step 5 above (forcing a draft) as the authority on which path this workspace uses.
8. **Check the Bitbucket labels warning.** With `git.pr.labels` set in the sandbox's `fugaro.yaml`, check that the run's stderr log carries the "labels aren't supported" warning exactly once.
9. **Check Bitbucket's repository name matching.** Run once with `repo` in the task file spelled in mixed case (for example `Owner/Sandbox`), with a PR already open for the run branch. The run must find and update that PR, not open a second one.
10. **Check GitHub token life against a long stage.** With `timeouts.stage` above an hour, check that a stage lasting more than an hour still gets a working token from the next stage onward: the log shows no "refreshing git credentials failed" warning, and finalize's push succeeds.
11. **Record fixtures.** To replace the hand-written ones with recorded ones, wrap the adapter's HTTP client in `httpfixture.Recorder` (the `HTTP` option of `bitbucket.Options` or `github.Options`), with the credential values in `Secrets`, and `Save` the exchanges. Do this for both providers so the hermetic test fixtures reflect real Bitbucket and GitHub responses rather than hand-written ones.
