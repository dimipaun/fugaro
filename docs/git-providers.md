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

**Name the token when you create it.** Pick a name you want people to see, such as `Fugaro <org>` (whether Bitbucket requires token names to be unique is unchecked; a bare `Fugaro` says nothing about whose it is). Bitbucket shows the token's name as the author of every pull request and comment Fugaro creates, and a token can't be renamed or edited after creation (it can be rotated or revoked, which keeps the name). To change the name, create a new token, store it as a new version of the repository's `bitbucket-token` secret (`fugaro secrets set bitbucket-token --repo <owner/name> < <token file>`), and then revoke the old one. Use the same name for every repository, so pull requests look the same everywhere. Commits are authored as `Fugaro` whatever the token is called.

- **Reviewers** (`git.pr.reviewers`) are requested only when the PR becomes ready (see "Early draft PRs" below). They are account UUIDs (`{…}`) or account IDs, not usernames. If Bitbucket rejects one, the PR stays ready without reviewers and the run's report says so. Bitbucket answers an unknown but well-formed UUID with HTTP 400 `reviewers: Malformed reviewers list`, so the message does not distinguish an unknown reviewer from a malformed one.
- **Labels** (`git.pr.labels`) are ignored. Bitbucket Cloud pull requests have no labels. The run logs a warning about this once, to its stderr log.
- **Repository names** are matched in lowercase when finding an existing pull request, since Bitbucket stores workspace and repository slugs in lowercase.

#### Finding a Bitbucket reviewer's UUID

`git.pr.reviewers` needs account UUIDs. The repository access token can read the participants of the repository's recent pull requests, with each one's UUID and display name, so this finds anyone who took part in one of the last 30 merged pull requests. This keeps the token out of argv: `curl --config` reads the header from a process substitution, and `printf` is a shell builtin, so the token never reaches a process's command line.

```bash
curl -sS --config <(printf 'header = "Authorization: Bearer %s"\n' "$(cat ~/.config/fugaro-<repo>-token)") 'https://api.bitbucket.org/2.0/repositories/<workspace>/<repo>/pullrequests?state=MERGED&pagelen=30&fields=values.participants.user.uuid,values.participants.user.display_name' | python3 -m json.tool
```

Pick the reviewer's `{…}` UUID by display name, and put it in the repository's own `git.pr.reviewers`. Never put it in Fugaro, its tests or its fixtures.

### GitHub

Create a **GitHub App** and install it on the repositories Fugaro serves (design §6.1). It needs these repository permissions:
- **Contents: Read & write**
- **Pull requests: Read & write**
- **Issues: Read**
- **Metadata: Read**

These four are all of it: never add **Workflows** (below). Then **install the App on each repository Fugaro serves** (the App's settings page, Install App). When you later change an installed App's permissions, GitHub asks the installation's owner to **accept the change**; until it is accepted the installation keeps the old permissions and runs fail with permission errors.

**Name the App when you create it, and expect the obvious name to be taken.** A GitHub App's name is unique across all of GitHub, and its URL handle (`github.com/apps/<slug>`) is derived from it: the bare name `Fugaro` is already taken (the first dogfooding install found out the hard way), so use `<yourname>-fugaro`, for example `acme-fugaro`. GitHub shows pull requests and comments the App makes as authored by `<slug>[bot]` (the name the App ends up with, so read the handle GitHub gives you), which is what your reviewers see on every PR. Treat the name as permanent: choose it before the first run, and use one App for all of a team's repositories so the pull requests look the same. The App's ID (a number on its settings page) is not a secret; `fugaro init` asks for it once in a terminal (or take `--github-app-id`) and keeps it in the local config. Generating a client secret is not needed (Fugaro uses the private key only); delete one if you made it.

Store its App ID and private key as the variables above. At bootstrap, the runner mints an **installation token for this repository only**, restricted to the permissions above. It lasts about an hour. Before each stage, the runner replaces it with a fresh one if it would expire within that stage's timeout plus 5 minutes, but it never asks for more than 50 minutes, which is all GitHub can give. So with `timeouts.stage` above about 45 minutes, a stage can outlive its token: the agent's `git` and `gh` calls late in that stage fail, and only the refresh before the next stage (or before finalize's push) restores working credentials.

- **Fugaro never changes workflow files.** Do not grant the App the **Workflows** permission, whatever the repository: a run able to edit `.github/workflows/*` could change the merge gates or reach the repository's secrets. GitHub then refuses any push of the App that creates or updates a workflow file. Fugaro knows this: on the github provider the agent is told not to touch `.github/workflows/`, and if the run's commits change one anyway, finalize skips the push, saves the run's commits and ends `infra_error` with the reason `GitHub refused the push: this run changed .github/workflows/x.yml and Fugaro never has permission to change workflow files; a person must make that change`. A person applies that change from the saved work (see "A refused push", below).
- **Reviewers** are user logins, or `org/team` for a team.
- **Labels** are added after the PR is created. If that fails, the PR stays and the run logs a warning.

## What the agent gets (design §6.2)

The agent's environment carries a short-lived, repository-scoped token. For Bitbucket this is the repository access token itself. It is a convenience, not a security boundary (design §6.1).

- `FUGARO_GIT_TOKEN` and `FUGARO_GIT_USERNAME`, used by the credential helper below.
- `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_n` and `GIT_CONFIG_VALUE_n`. These install a credential helper, scoped to the origin's host, that answers from the two variables above. They also reset any other helper configured for that host. Plain `git push` and `git fetch` just work.
- `GH_TOKEN` (GitHub only), so `gh pr view`, `gh pr edit` and `gh issue view` work.

The token is never written to `.git/config`, to a remote URL, or to a command line.

**A follow-up's agent gets none of this** (design §6.1): no `FUGARO_GIT_*` or `GIT_CONFIG_*` variables and no `GH_TOKEN`. The runner fetches the pull request's branch at bootstrap and pushes it at finalize with its own git environment, and the review comments reach the agent in its prompt. This is defense in depth only: the agent runs as the same user as the runner, so it could still read the token from the runner's `/proc`.

## Early draft PRs (`git.pr.early_draft`)

A run opens a **draft** pull request at its first verified push (a passing test run on a clean tree), not when it finishes, keeps a **Fugaro status** section in the description current (stage, verify state, model cost, update time) and marks the PR ready at the end only when it is (design §4.2a).

**Reviewers are now added when the PR becomes ready, not when it is created.** Before this change, `git.pr.reviewers` were requested at creation, so a reviewer was notified as soon as the draft appeared, whether or not the run would succeed. Now a draft carries no reviewers and no labels. They are requested by the same run when it flips the PR to ready, and a run that ends as a draft (failed, halted, cancelled) never notifies them. Anyone you want to see work in progress can watch the draft; nobody is paged by it. A follow-up requests them again only when it flips a draft to ready. On GitHub, a reviewer who has already reviewed is no longer in the PR's requested list, so a follow-up that flips a draft to ready requests them again (they are notified again). One invalid reviewer (an unknown login, or the PR's author) makes GitHub reject the whole request, so none of the reviewers are added: the PR stays ready and the run's report notes it.

`git.pr.early_draft` (default `true`; `false` for a repository whose host has no draft pull requests, or when you want the PR to appear only at the end) keeps the old flow: the PR opens at finalize and the reviewers are requested then, if it is ready. On GitHub plans that refuse drafts, Fugaro opens a normal PR titled `[DRAFT] …` (recorded as `draft_fallback`, shown by `fugaro diagnose`); a normal PR may auto-request CODEOWNERS reviewers, which Fugaro cannot prevent. The status section sits between two `[//]: # (fugaro:status begin|end)` lines, which Markdown renders as nothing; a human edit outside them is kept. If a run dies, its draft keeps saying `Running` with an old time: `fugaro ls` shows `(draft, stale)` and `fugaro diagnose` says so.

## Pushing and draft pull requests

- **Pushing.** Finalize pushes only the run's own `fugaro/<run-id>` branch, with `--force-with-lease`. It overwrites the remote branch only when the branch is absent or its tip is one of the run's own commits: an ancestor of the final HEAD, or a commit in HEAD's reflog. That covers the agent pushing the branch and then amending or rebasing. A tip merely present in the checkout, for example because a `git fetch` brought in someone else's push, does not count. A tip pushed from anywhere else is left alone, and the run fails with a clear reason. The base branch is never pushed. Protect it anyway (design §6.1). **A follow-up's push (`PushExisting`) is stricter:** the branch must still exist on origin (an absent one is refused, never recreated) and be where the run started, or at the run's own HEAD on a retried push. Any other tip, a rewind to an older commit included, means someone else pushed: the run pushes nothing, posts a short note on the PR, and ends `failed` with outcome `none` (design §4.1).
- **A refused push (github).** A permanent refusal is recognised, not retried: a workflow file (above), a protected branch, a file over GitHub's size limit, a repository rule (GH013: signed commits, linear history, ...), or secret-scanning push protection. The run ends as a failed push always did: the process exits non-zero, the job shows failed, the record is `infra_error` with outcome `none` (also when the run was halted or cancelled; the record keeps the halt). What is new is an explicit one-line reason (`fugaro ls`, `fugaro diagnose`, and a note on the PR when one is still open), and that the work is not lost: finalize writes a `git bundle` of the run's own commits (not those on the base branch; for a follow-up, not those already on the PR branch) to `work.bundle` in the run's prefix of the runs bucket, `runs/<repo>/<run-id>/work.bundle`, and the reason names it. There is no fetch command: download it (`gsutil cp gs://<runs bucket>/runs/<repo>/<run-id>/work.bundle .`) and run `git fetch work.bundle HEAD` in a clone, then push the result yourself. The bundle holds commits only, never the checkout's config, credentials or untracked files, and anyone with access to the runs bucket can read it, like the run's other files. It has the base as a prerequisite (`origin/<base>`, usually the base branch's tip when the run started; for a follow-up, the commit it started from), so `git fetch work.bundle HEAD` fails in a clone that lacks that commit (for a follow-up, one a person force-pushed away); `git bundle verify work.bundle` says what it needs. It is not saved when the scan finds a value Fugaro redacts everywhere else in any commit's author and committer, message or patch (not only the final diff), when a change is binary (it can't be scanned), when the text is over 1 GiB (not checked), when GitHub's secret scanning is what refused the push (it found something Fugaro doesn't know), or when the bundle is over 256 MiB; the reason says which. **What the scan covers:** only the secret values the runner knows (its credentials and the workflow's declared secrets) and their encoded forms. It does not detect other secrets: a token of a known format (`ghp_...`, `AKIA...`, a PEM block) that Fugaro never held, or any secret of the repository's own, can be in a saved bundle. GitHub's own secret scanning is the only other cover. The workflow check uses the net diff since the merge base, so a workflow file the run adds and then removes passes it (GitHub then decides), and a follow-up that rebases commits from before its start can have a person's workflow commit counted as the run's. Any other push failure, and any push to Bitbucket, stays a plain `infra_error: finalize: pushing ...` and saves nothing.
- **Draft PRs on GitHub.** Draft state is changed through GraphQL, because REST cannot change it. Some plans have no draft PRs for private repositories. There, Fugaro opens a normal PR titled `[DRAFT] …`, and removes the prefix when a later run marks the PR ready.
- **Draft PRs on Bitbucket Cloud.** The REST API documents a `draft` boolean on create and update, which needs `pullrequest:write`. A repository access token can hold that scope. Fugaro sends `draft` and checks the response. If Bitbucket did not make the PR a draft, it uses the same `[DRAFT] ` title fallback. The live check (2026-09-27, a repository access token on a private workspace repository) found `draft` honoured on create and on update in both directions, so there the PRs are real drafts and the fallback is not used. The recorded evidence is that a `PUT {title, draft}` keeps the description and reviewers. Whether a `PUT` that leaves out the description (or `draft`) keeps it is not established, so Fugaro never relies on it: every `PUT` re-sends the current title, description and draft state, read just before, and a reviewers update re-sends the existing reviewers plus the new ones. Live check 24 step 4 must confirm that a `PUT` with `reviewers` keeps the description and the draft state (A3).
- **Which direction a failure falls in.** When `EnsurePR` cannot fully apply the draft state, it still returns the pull request. The error it returns depends on which way the PR is wrong. If the PR is left looking *more* like a draft than asked, for example still a draft or still titled `[DRAFT] …` when ready was wanted, the error is a `*gitprov.PartialError`. The run then records a draft outcome, naming the failure, and never reports the PR as ready. If the PR is left looking *more ready* than asked, the error is a plain one, and the runner retries `EnsurePR`. The usual case is Bitbucket ignoring `draft: true`, after which the retitle that adds the `[DRAFT] ` prefix fails. Each retry finds the same PR and tries the prefix again. If the retries run out, the run ends in `infra_error`, the PR is kept in the run record, and it may still look ready. The runner then posts a comment on the PR (best effort, with credentials redacted) saying it is not ready and needs a human check, in place of the run report. Check it by hand.

## Follow-up runs (design §4.4)

A follow-up (`fugaro run --pr N`) reads the repository, the pull request and its comments, and updates the pull request by number. **The permissions are unchanged:** everything below is covered by the scopes and permissions listed above, and the CLI still holds no provider credential (the runner makes every call). **The one exception:** `fugaro init` and `fugaro doctor` can check that the GitHub App is installed on the repository with the permissions runs ask for. They use the App's private key only from memory: the key you typed at this run's `secrets` stage, or, only behind the explicit `--check-github-app` flag, read from Secret Manager with your own credentials (owners only) after a one-line notice. The key signs one JWT; only its PEM byte copy is cleared afterwards (the encoded payload and the parsed key live until garbage collection), it is never printed or logged, and GODEBUG=http2debug in your own environment would print the JWT's Authorization header (it lives 9 minutes), so unset it. `--check-github-app` is refused in a coding agent's session, and `fugaro doctor` without it never reads the key (the setup skill runs `fugaro doctor --json`).

| What | GitHub | Bitbucket Cloud |
|---|---|---|
| The repository's visibility | `GET /repos/{owner}/{repo}` → `private` | `GET /repositories/{workspace}/{repo}` → `is_private` |
| The pull request | `GET /repos/{owner}/{repo}/pulls/{n}`: `state` and `merged`, `user.id` (the author), `head.ref`, `head.repo.full_name`, `head.sha`, `draft` or the `[DRAFT] ` title prefix (the fallback on plans without draft PRs) | `GET …/pullrequests/{n}`: `state` (`OPEN`, `MERGED`, `DECLINED` or `SUPERSEDED`), `author.account_id`, `source.branch.name`, `source.repository.full_name`, `source.commit.hash` (abbreviated), `draft` or the `[DRAFT] ` title prefix |
| Inline comments | GraphQL `reviewThreads` (50 per page, 50 comments each): `isResolved`, `isOutdated`, `path`, `line`, and each comment's `authorAssociation` and author (`__typename`, `login`, `databaseId`) | `GET …/pullrequests/{n}/comments?pagelen=100`, the comments with `inline` (`path`, `to` else `from`, `outdated`); a reply carries its own `inline` (one without takes its thread's); a thread is resolved when its root has a `resolution` (live: `{}`, an empty object) |
| Review summaries | `GET …/pulls/{n}/reviews?per_page=100`, the reviews with a body and a `submitted_at` | none: Bitbucket has no review bodies |
| General comments | `GET /repos/{owner}/{repo}/issues/{n}/comments?per_page=100` | the same listing, the comments without `inline` |
| Fugaro's own identity | `GET /app` with the App's JWT, for its `slug`: REST authors are `<slug>[bot]`, GraphQL authors `<slug>` with `__typename: Bot` | `GET /user` with the repository token, for its `uuid`, compared with each comment's `user.uuid`; a repository access token gets HTTP 403 (observed live), so its identity is unknown |
| Update by number | read the PR, then the usual draft update (GraphQL `convertPullRequestToDraft` or `markPullRequestReadyForReview`, or the title prefix) | read the PR, then the usual `PUT {title, draft}` |

- **Scopes used.** GitHub: *Pull requests: Read* for the pull request, its threads and reviews, and *Issues: Read* (or *Pull requests*) for the general comments; `GET /app` needs only the App's JWT. Bitbucket: *Repositories: Read* and *Pull requests: Read*. `GET /user` does not accept a repository access token: the live follow-up check (2026-09-30) got HTTP 403, "This API is not accessible by this authentication mechanism", so with such a token the identity is unknown, as below.
- **Paging** follows GitHub's `Link: rel="next"` header, Bitbucket's body `next` and GraphQL cursors, and only to URLs on the adapter's own API scheme, host and path prefix, so the token can't be sent anywhere else. A listing stops with an error after 20 pages.
- **Authors.** Each comment carries its author's display name or login (for the report) and account ID (for the trust rule): GitHub's numeric user ID, Bitbucket's `account_id`. On GitHub, `authorAssociation` `OWNER`, `MEMBER` or `COLLABORATOR` counts as a collaborator; on Bitbucket, which has no such field, every author does. A deleted account has no ID and is never trusted.
- **Drafts are left out:** GitHub reviews not yet submitted and Bitbucket's `pending` comments. Deleted Bitbucket comments are dropped too.
- **The identity lookup and its fallbacks.** Fugaro's own comments are told apart by author, so its markers and headings are honoured only on those. On GitHub a definite failure of `GET /app` (an empty slug, or a 4xx other than 429) is kept for the provider's life and warned about once; any other failure isn't kept, and the read goes on with the identity unknown. On Bitbucket a 4xx from `GET /user` other than 408 and 429, or an empty `uuid`, is kept the same way, and any other failure fails the comment read, so the follow-up ends as `infra_error` before touching the PR. With the identity unknown, markers are honoured from every author, the log warns, and the report says so. The trust rule doesn't depend on the lookup: comments by the pull request's author, which is Fugaro's own identity, are always dropped (design §4.4). **Observed live on Bitbucket (2026-09-30):** with a repository access token `GET /user` is refused (HTTP 403), so this fallback is the normal case there; the PR's `author.account_id` equals the `user.account_id` of every comment the token posts, so Fugaro's own comments are dropped as the PR author's.
- **The update by number** never looks a PR up by branch and never creates one. It returns `gitprov.ErrPRNotOpen`, with the PR, when the PR is merged or closed or its source branch isn't the run's branch, and changes nothing then. It leaves the title and description alone, apart from the `[DRAFT] ` prefix the draft fallback uses.
- **The marker.** Every comment Fugaro posts ends with `<!-- fugaro:report run=<run-id> -->`. GitHub renders it hidden. Bitbucket shows it as a visible last line (observed live, 2026-09-30): its Markdown escapes the HTML comment in `content.html`, while the raw content, which is all Fugaro reads, keeps it. It is cosmetic; hiding it is in the design's backlog (§14).

### Finding an account ID for `followup.trusted`

`followup.trusted` in the base branch's `fugaro.yaml` lists the account IDs whose pull request comments a follow-up acts on (design §5.1, §6.1). Logins and display names aren't accepted: they can be renamed, and then reused by someone else.

- **GitHub:** the numeric user ID, for example with `gh api users/<login> --jq .id`.
- **Bitbucket:** the `account_id` (such as `557058:00000000-0000-0000-0000-000000000001`), not the `{…}` UUID. Any Bitbucket API response that names the person carries it in its user object: a comment's `user`, a PR's `author`, or a participant. The reviewer lookup above prints it with `account_id` added to its fields (`values.participants.user.account_id`). For a sandbox, `TestLiveInspect` (below) prints the `account_id` and display name of every comment on the PRs it inspects, so the person can post any comment on a sandbox PR and read their ID from its output.

An account ID isn't a secret, but it names a person, so keep real ones out of Fugaro, its tests and its fixtures: they belong in the repository's own `fugaro.yaml`.

## Live check against a sandbox repository

Hermetic tests cover the adapters with recorded HTTP fixtures (`internal/gitprov/*/testdata`; `internal/gitprov/bitbucket/testdata/recorded` holds real Bitbucket exchanges). Before a release, and once for each provider as soon as credentials exist, run against a throwaway repository.

For Bitbucket, most of the API checks below (steps 6, 7 and 9, the labels warning, comments, and the reviewer fallback) are automated in `internal/gitprov/bitbucket/live_test.go`, behind the `live` build tag. It runs against the one repository named in `FUGARO_LIVE_REPO` (`owner/name`, required: unset, or not of that form, every live test is skipped with a message, and nothing is defaulted), and touches no other. It pushes its own `fugaro/live-*` branches, declines every PR it opened and deletes those branches at the end, and records fixtures when `FUGARO_LIVE_RECORD_DIR` is set:

```bash
FUGARO_LIVE_REPO=<owner>/<sandbox> FUGARO_BITBUCKET_TOKEN="$(cat <token-file>)" FUGARO_LIVE_RECORD_DIR=/tmp/bb-fixtures \
  go test -tags live -timeout 600s -run 'TestLive(Bitbucket|Cleanup)' -v ./internal/gitprov/bitbucket/
```

Set `FUGARO_LIVE_REVIEWER` to the account UUID of a dedicated sandbox account to also check that reviewers survive every update; Bitbucket notifies that account, so never name a real person. Unset, those checks are skipped. A `-timeout` abort kills the test binary without running its cleanup (or the `TestLiveCleanup` in the same invocation), so after one, run `-run TestLiveCleanup` again.

`TestLiveCleanup` alone sweeps anything a crashed run left (plus the run branches named in `FUGARO_LIVE_SWEEP_BRANCHES`), and `TestLiveInspect` prints the state and comments of the PRs in `FUGARO_LIVE_INSPECT_PRS`, for checking a `fugaro exec` run, with the PR's `author.account_id` and each comment's `user.account_id` and display name.

**The follow-up reads,** since M6, are three more subtests of `TestLiveBitbucket`, on the test's own PR:
- `follow_up_reads` posts a general comment ending in an HTML comment, an inline comment and a reply, and resolves the thread. It then reads the repository, the PR and its comments through the adapter, recorded as `live_follow_up_reads.json`. Its `FACT` lines record what the adapter reads: the repository's visibility (`is_private`); the PR's state, `author.account_id`, source branch and repository, and head (`source.commit.hash`, and whether it matches the pushed head); whether resolving the thread (`POST …/comments/{id}/resolve`) worked; the warnings `GET /user` gave with the repository token; and, for each comment, its kind (so whether a reply carries `inline`), author and `account_id`, whether it is the PR's author, whether it is Fugaro's own (which shows whether the token user's `uuid` and the comments' `user.uuid` are spelled alike), and its resolved, outdated and deleted flags, path and line; and whether the raw content keeps `<!-- … -->`. `pending` isn't exercised, and stays as documented. The recording is committed as `testdata/recorded/follow_up_reads.json` (renamed and scrubbed as below), and `TestRecordedFollowUpReads` replays it; the 2026-09-30 results are in [gcp-live-checklist.md](gcp-live-checklist.md#results-of-the-fourth-live-run-m6).
- `ensure_by_number` updates the PR by number and checks that its title and description are unchanged (recorded as `ensure_by_number.json`, replayed by `TestRecordedEnsureByNumber`).
- `ensure_by_number_declined` declines the PR, then checks that it reads as closed, that an update by number returns `ErrPRNotOpen` and that no open PR exists for the branch (recorded as `ensure_by_number_declined.json`, replayed by `TestRecordedEnsureByNumberDeclined`).

The end-to-end follow-up, with review comments a person posts by hand, is check 19 of [gcp-live-checklist.md](gcp-live-checklist.md).

The full-run steps:

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
   fugaro exec --bucket file:///tmp/fugaro-bucket --task-file /tmp/task.json --provider bitbucket \
     --workdir /tmp/fugaro-work --state-dir /tmp/fugaro-state \
     --remote https://bitbucket.org/<owner>/<sandbox>.git
   ```
   `--task-file` needs `--provider` (or `FUGARO_GIT_PROVIDER`), `bitbucket` or `github`: the provider kind is part of the repository's storage slug.
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

   **Re-recording the Bitbucket fixtures in `internal/gitprov/bitbucket/testdata/recorded`.** `live_test.go` saves each check as `live_<name>.json` in `FUGARO_LIVE_RECORD_DIR`. Before copying one into `testdata/recorded`:
   1. Rename it to `<name>.json`.
   2. Replace every real identity with the placeholders in `recorded_test.go` (`recAllowedUUIDs`, `recAllowedAccountIDs`, `recAllowedNames`, the all-zero gravatar hash and `recAvatarID`). This covers the reviewer's name, nickname, UUID, account ID and gravatar hash, the token user's UUID, account ID and avatar URL, and, in a repository response, the workspace's (and its team owner's) name, slug and UUID (`recWorkspace`, "Acme", `acme`), the project's UUID, the repository's UUID and name, and the user in the HTTPS clone link (`recCloneUser`).
   3. Update the pinned `recStamp`, or the `recFollowUp*` constants for the follow-up recordings (and PR numbers, if they changed), in `recorded_test.go` to match the new recording.
   4. Run `go test -run 'TestRecorded' ./internal/gitprov/bitbucket/`. `TestRecordedFixturesHoldNoIdentities` must pass.

   > **Review every recorded fixture before committing it.** Committed Bitbucket recordings must also pass `TestRecordedFixturesHoldNoIdentities` (`internal/gitprov/bitbucket/recorded_test.go`), which fails on emails and on account identities that are not its placeholders. The recorder never records the `Authorization` header, replaces each `Secrets` value with `REDACTED` (in paths, queries and bodies), and replaces every JSON `token` or `*_token` field (such as `access_token`, `refresh_token`) in a response (for example a freshly minted `ghs_…` installation token, which you could not list in `Secrets`). Anything else sensitive in a response is kept as is: account names, emails, UUIDs, avatar URLs, or a credential under some other field name. Grep each file for the credential values with the pattern read from the credential file, so the value never reaches a command line (`grep -cFf <token-file> file.json` must print `0`) and read it through before `git add`.
