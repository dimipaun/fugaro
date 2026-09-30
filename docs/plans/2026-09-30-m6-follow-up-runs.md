# M6 — Follow-up Runs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `fugaro run --pr N ["extra instructions"]` continues an existing Fugaro PR. The CLI finds the PR's earlier runs in the runs bucket and launches a follow-up on the same job. The runner checks out the PR's branch, but reads its configuration from the base branch. It refuses a public repository unless the base config opts in. It reads the PR's unresolved review threads and the general comments posted since the last follow-up saw the PR, and keeps only those by authors the base branch's `fugaro.yaml` trusts. These reach the agent as untrusted data. It resumes the previous run's Claude Code session when it can, and otherwise starts fresh with the PR's diff as context. It runs the usual review rounds, then updates the **same** PR, never opening a second one: it pushes, re-evaluates draft or ready, and posts a new report that names whose comments drove the change. Fugaro's own comments are never fed back to the agent.

**Architecture:**

```
                 ┌──────────── laptop ───────────────────────────────────────────┐
fugaro run --pr N│ runs bucket only (the CLI has no provider credentials):       │
  [TEXT]    ────►│   read task.json + result.json of runs/<slug>/* (90 days),    │
                 │   keep the runs on PR N, join executions for those only       │
                 │   refuse: no Fugaro run on N · branch not fugaro/<run-id> ·   │
                 │           a run on N active or unreadable · live branch lock  │
                 │   previous_run = newest (started_at) run on N that pushed     │
                 │   task.json {branch, pr, previous_run, ref, workflow, task}   │
                 │   ─► the usual launch (claim, jobs.run, launch.json)          │
                 └──────────────────────────────┬────────────────────────────────┘
                                                ▼  same Cloud Run job, same image
 fugaro exec bootstrap (follow-up):
   checkout origin/<branch> · fetch origin/<ref> · fugaro.yaml, instructions and review
   prompt read from origin/<ref> (the base), never from the PR branch ─► lock <branch>
   provider.Repository(): public and no followup.allow_public ─► infra_error
   provider.PullRequest(N): open? source branch == <branch>? same repository? head?
   agent env built WITHOUT the git token or GH_TOKEN (secrets registered for redaction)
   provider.Comments(N) ─► stale check (Fugaro-identity markers after previous_run)
     ─► followup.Select: unresolved threads + general comments since the previous
        follow-up's fetch; only followup.trusted authors (and on GitHub collaborators);
        drop Fugaro's own; bound (newest kept); redact ─► comments.json
   session: runs/<slug>/<previous_run>/session/ ─► ~/.claude/projects/<wd>/<id>.jsonl
     when the previous head is an ancestor of HEAD; else fresh (the report says why)
 implement (resume S or new S) ─► review(1..n, fresh) ─► fix (resume S) …
 finalize: commit leftovers (no empty commit) ─► PR still open? else no push
   ─► push (the remote branch must exist) ─► pushed_head ─► EnsurePR{Number: N}
   (update draft state only; ErrPRNotOpen → no comment, failed/none) ─► report
 writeback: session/<id>.jsonl + session/session.json (every run) ─► caches ─► lock
```

- **The CLI resolves a PR through the runs bucket, not the provider.** Design §4.4 step 1 says the CLI resolves the PR through the provider, but the CLI has no provider credential: they live in Secret Manager, and launchers hold no `secretAccessor` (§6.1). Every run's `result.json` records its `branch`, `pr` and, from M6, `pushed_head`, so the bucket answers "which run last updated PR N, on which branch". The runner, which holds the credential, verifies the answer against the provider before it touches anything (ruling R1).
- **Follow-ups move the trust boundary, and the base branch decides who crosses it.** A first run acts on text only repository writers control. A follow-up also acts on PR comments, so it acts only on comments by the account IDs the base branch's `fugaro.yaml` lists in `followup.trusted`, refuses public repositories unless the base opts in, and reads every setting from the base branch (ruling R6).
- **One PR, always.** A follow-up's finalize never calls the create path, never pushes to a branch that is gone, and never comments on a PR that is no longer open (ruling R4).
- **Sessions are saved by every run from M6 on**, not only by follow-ups, so the first follow-up of a PR opened after M6 can resume. PRs opened before M6 always start fresh.

**Tech stack:** Go 1.27. No new Go module. The GitHub adapter adds one GraphQL query (review threads) and four REST reads to its `httpjson` client, which gains header-based paging; the Bitbucket adapter adds three REST reads. No Terraform, IAM or image-template change: follow-ups use the same jobs, service accounts, secrets and images as any run, and launch with `LaunchSpec.Timeout` when the task carries an override, as M5 left it.

**Spec:** [docs/design/v1.md](../design/v1.md). Read these first:
- §4.4 (follow-up runs), and what it touches: §4.1 (stages, bootstrap order, finalize), §4.2 (the PR outcome rule), §4.5 (cancel), §4.6 (the run record), §4.7 (launch protocol, `--retry`, `--run-id`)
- §3.3 (the run prefix: `task.json`, `result.json`, `session/`, `report.md`) and §5.1 and §5.3 (`fugaro.yaml`; the task spec's `branch`, `pr`, `previous_run`, `overrides`)
- §6.1 (the trust boundary, what the agent can reach, the redactor's limits, "the CLI treats everything under a run's prefix as written by that repository's agent")
- §9.1 (`fugaro run --pr N [TEXT]`, `ls`, `diagnose`, `cancel`) and §9.2 (`fugaro:followup`)
- [git-providers.md](../git-providers.md) (the App's and the repository token's scopes, the recorded fixtures and the Bitbucket live test)
- [gcp-live-checklist.md](../gcp-live-checklist.md) (guardrails, check 13, the sweep)

The M2 plan ([2026-09-27-m2-git-providers.md](2026-09-27-m2-git-providers.md)) explains the adapters and their fixtures, and the M4 plan ([2026-09-27-m4-gcp-backend.md](2026-09-27-m4-gcp-backend.md)) the launch protocol this plan extends.

**What M4 and M5 left ready, and what is missing:**

| Area | Ready | Missing |
|---|---|---|
| Task spec | `task.Spec{Branch, PR, PreviousRun}`, `IsFollowUp()`, "set together" validation, task text optional for a follow-up; the schema's `dependentRequired` | `branch` must be `fugaro/<run-id>` (code and schema) |
| `fugaro.yaml` | strict decoding, `fugaro validate`, `fugaro config example` | the `followup:` block (who is trusted, public repositories) |
| CLI | hidden `--pr` that exits 1 ("arrive in M6"); `--retry`, `--run-id`, `--batch`, `--total-timeout`, `launchTimeoutOf(spec)` | everything behind `--pr`; `launchResult.Branch` is hard-wired to `fugaro/<run-id>`; `taskText` refuses empty text |
| Runner | the branch lock keyed by `r.rec.Branch`; `gitops.Push` refusing a remote tip the run never had; `--resume` in `agent.Args`; fix stages already resume the implement session | bootstrap refuses follow-ups (`runner.go`, "not supported by this version"); config is read from the checked-out tree; nothing writes `session/`; finalize always may create a PR and add an empty commit; `rec.PR` is set only at finalize; no record of a successful push; `Push` recreates an absent remote branch; `ensurePR` retries every plain error |
| Providers | `EnsurePR`, `Comment`, `GitAuth` | reading the repository's visibility, a PR by number and its comments; an update-only `EnsurePR`; a marker on Fugaro's comments; the bot identity; header paging in `httpjson` |
| Fakes | file-backed fake provider; `fakeclaude` accepts `--resume` | the fake has no PR state, visibility, foreign comments or comment times; `fakeclaude` writes no session file and doesn't check one on `--resume` |
| Backend | `LaunchSpec.Timeout`, the same job per (repo, workflow) | nothing: no Terraform, IAM, gcpfake or image change in M6 |
| Plugin | `TestSkillCommandsExist` | `plugin/skills/followup/SKILL.md` |

**Decisions already made (user):**
- **Trust (binding, 2026-09-30):** a follow-up acts only on comments whose author's account ID is listed in the **base branch's** `fugaro.yaml` under `followup.trusted`, plus the PR's own author by default (`followup.trust_pr_author`, default `true`). On GitHub the author must also be `OWNER`, `MEMBER` or `COLLABORATOR`. The runner refuses a **public** repository unless the base `fugaro.yaml` sets `followup.allow_public: true`, checked through the provider at bootstrap. The report lists whose comments were used and how many were dropped as untrusted. §6.1 records that the trust boundary moves.
- **The live check uses no second credential (binding):** the controller pauses while the **user** posts a review comment on the sandbox PR by hand in the Bitbucket UI, then continues.
- The sandbox is `acme/sandbox` on Bitbucket, workflow `web`, model auth `oauth`. Live runs go to the sandbox only, never to the web repository.
- The user runs anything that costs money or changes real resources or repositories. Every such step is **⚠ CONFIRM**, each its own question.
- Hermetic tests are the default: no test needs a network, GCP, a provider or a model.

**Out of scope for M6:**
- **Replying to individual review comments or resolving threads.** The agent answers in the run's report (from `followup.md`); it resolves nothing.
- **Bitbucket PR tasks and reviewers' "changes requested" states,** and GitHub review states: only comment text is read.
- **Webhook- or schedule-triggered follow-ups.** A follow-up is launched by a person or their local agent.
- **A `bb` CLI for the agent on Bitbucket** (M8 backlog): comments reach the agent through the prompt.
- **Rebasing the PR onto a moved base branch.** The agent may merge the base if a comment asks for it, as it would locally.
- **Dropping the git token from a first run's agent** (Open question 3): M6 drops it for follow-ups only.
- **M7:** the other skills (`launch`, `status`, `logs`, `diagnose`), the plugin release.

## Global Constraints

- **Exit codes:** 0 ok, 1 a user error or a refusal (no Fugaro run on the PR, an active or unreadable run, a live lock, a flag conflict), 2 a remote failure (a bucket or backend error). `fugaro exec` keeps its codes (§4.1): a follow-up refused at bootstrap (public repository, PR not open, branch mismatch, stale) is an `infra_error`, exit 2.
- **The PR is never changed by a refused follow-up.** Any bootstrap failure leaves the PR, its branch and its comments as they were: no push, no draft change, no comment. The branch lock and `result.json` are the only writes.
- **One PR per branch, always.** A follow-up's finalize never creates a PR, never pushes to an absent remote branch, and never comments on a PR that isn't open. The only `EnsurePR` call a follow-up makes carries `PRSpec.Number`.
- **Configuration comes from the base branch.** A follow-up reads `fugaro.yaml`, `agent.instructions` and the `agent.review` prompt file from `origin/<spec.Ref>` with `git show`, never from the PR branch's working tree, which an earlier agent or a comment could have changed.
- **Only trusted authors steer a follow-up.** A comment reaches the agent only when its author passes R6's rule. The launcher's own TEXT is trusted.
- **Comments are untrusted data even then.** They reach the agent only inside the nonce-delimited block `followup.Block` builds, with the posture text of R6. They never reach the system prompt, a stage name, a file name or a git argument. The delimiter is not a security boundary (§6.1).
- **The runner's own log lines never quote comment text** (only counts, IDs and author names). The agent-event relay logs what the agent says, and the agent may quote comments; that output is redacted like any agent output and is out of this constraint's scope.
- **Redaction.** Comment bodies are selected, redacted and stored only after `agent.BuildEnv` has registered every secret. The session file is redacted before upload. `followup.md` is redacted, clipped and stripped of Fugaro markers and headings before it is quoted.
- **Bounded reads of untrusted objects.** `comments.json`, `followup.md` and `session/session.json` (`runstore.MaxRecordBytes`) and `session/<id>.jsonl` (`runstore.MaxSessionBytes`, 64 MiB) are read with caps. Provider paging stops at 20 pages, and follows only URLs on the adapter's own API scheme and host.
- **Filesystem.** The session file is read and written through `os.Root` on `~/.claude/projects/<escaped workdir>/`, as a regular file only (never through a symlink), mode 0600.
- **No company- or repo-specific values** in engine code, tests, fixtures or docs outside this plan and the live runbook: `acme/app`, `acme/sandbox`, `acme/webapp`, `<project>`, `example.invalid`, account IDs such as `1234567` and `557058:00000000-0000-0000-0000-000000000001`.
- **TDD.** Each task writes its failing tests first and runs them to see them fail. `go test ./...` needs no Docker, network or credentials.
- **Subprocesses** use `exec.CommandContext` with `cmd.WaitDelay = 5 * time.Second`, as everywhere.
- CI runs `gofmt -l`, `go vet` (plain, `-tags docker`, `-tags live`, `-tags terraform`) and `go test -race ./...`. All of them pass after every task.

## Rulings on the questions

Each ruling says what it costs if it's wrong.

**R1. The CLI resolves a PR through the runs bucket; the runner verifies it with the provider.**
- `fugaro run --pr N` lists `runs/<slug>/` for the last `followUpLookback` (90 days, the `runs/` lifecycle age). It reads each run's `task.json` and `result.json` and keeps the runs **on PR N**: the task says `pr: N`, or the record says `pr.number: N` with a branch of the form `fugaro/<run-id>`. Only for those does it join the backend's executions (one `List` over the window, then `Execution` for the few it misses), so a busy repository costs two object reads per run and no per-run backend call.
- "Newest" means the record's `started_at`, else the run ID's time: a run ID can be chosen with `--run-id`, so its timestamp alone isn't trusted for ordering.
- It refuses (exit 1) when: no run is on N ("not a Fugaro PR in this repository, or its runs are older than 90 days"); the runs on N disagree on the branch; the root run (the run the branch names) exists and its record's branch isn't `fugaro/<its run ID>`; a run on N isn't settled (`launching`, `pending`, `running`); a run on N is an `error` row (its state can't be known); no run on N has `pushed_head`; or the branch lock is live.
- When the root run's objects have expired but follow-ups on N remain, the branch is accepted when every remaining run on N agrees on it and it matches `fugaro/<run-id>`; the runner then has no root task for a fresh prompt.
- `previous_run` is the newest run on N whose record has **`pushed_head`**, whatever its status (I1): a run that pushed and then failed to set up the PR still updated it. An `unlaunched` run on N is skipped with a warning (and `--retry` of it is later refused, R8).
- The runner then checks, with the provider, that the repository is private or `allow_public`, that PR N is open, that its source branch is the task's branch, that its source repository is the task's repository, and that its head is the checked-out commit. Any mismatch is an `infra_error` that touches nothing.
- **Why not the provider in the CLI:** launchers have no provider credential and shouldn't get one; the user's own `gh` or token would make the CLI's behaviour depend on local tools and make hermetic tests harder.
- *Cost if wrong:* a PR whose runs expired can't be followed up; the user starts a fresh run. A launch the runner then refuses (PR merged since) costs one container start, about a minute.

**R2. Which comments, and since when.**
- **Kept candidates:** (1) unresolved review threads (inline comments), whatever their age, each with its path and line; outdated threads are kept and flagged `outdated`; a thread cut at 50 comments is flagged `truncated`. (2) Review summaries (a review's top-level body) and general comments created after **`since`**.
- **`since`** (I2): when `previous_run` was a follow-up, its `comments.json` `fetched_at`, so comments posted while it ran aren't lost; when it was a first run, its `started_at` (its PR didn't exist before its finalize). A missing or unreadable `comments.json` falls back to the previous record's `started_at`, with a warning: more comments rather than fewer.
- **Dropped, each class counted:** comments carrying a Fugaro marker from Fugaro's own identity (R7), comments by that identity (`Comment.Self`, which also catches anything a first run's agent posted with `gh`), deleted comments, resolved threads, empty bodies, and **untrusted authors** (R6), whose display names are kept for the report.
- **Bounds:** at most 60 comments, each body clipped to 4 KiB (on a rune boundary, with `…(clipped)`), 32 KiB of bodies in all. Unresolved threads get up to 40 of the 60 slots; general comments and review summaries fill the rest; over either bound the **oldest** are dropped first, so the newest remarks survive. The kept comments are rendered oldest first. What doesn't fit is counted in `omitted.over_limit`, and the prompt says how many were left out and where to read them (the PR URL).
- **What was already answered:** the prompt also carries the previous follow-up's `followup.md` (redacted, clipped to 4 KiB), so the agent knows which unresolved threads it has already addressed or declined.
- *Cost if wrong:* the filters are data in one pure function with a table test.

**R3. The task record and run objects.**
- `task.json`: `branch` (`fugaro/<run-id>`), `pr`, `previous_run`, `ref` (the previous run's `ref`: the base branch), `workflow` (the previous run's), `task` (the extra instructions, possibly empty), `requested_by`, `batch`, `overrides.total_timeout` (only from this launch's flag; nothing is inherited).
- `result.json` gains:
  - `pushed_head` (every run): the commit the runner pushed, set right after a successful push. It is what R1 uses to find the run that last updated a PR.
  - `follow_up`: `{pr, previous_run, start_sha, session: "resumed"|"fresh", session_note, comments, authors, omitted, untrusted_authors}`.
  - For a follow-up, `pr` (number and URL) is set at bootstrap, not only at finalize, so `ls` shows it while the run works.
- `comments.json` (new, in the run prefix): the selection the agent got, redacted and clipped, the omitted counts, `since` and `fetched_at`.
- `followup.md` (new, in the run prefix, follow-ups only): the agent's summary as the report quotes it.
- `session/session.json` and `session/<id>.jsonl` (new): written by every run in writeback (R5).
- `report.md` as before, with a follow-up section.
- *Cost if wrong:* fields are additive; old records parse (records decode leniently).

**R4. Finalize for a follow-up.**
1. Commit leftovers as today. **No empty commit**: the branch is ahead of base already, and "no new commits" is reported, not faked.
2. Read PR N again. If it is no longer open, **don't push**: status `failed`, outcome `none`, reason "PR #N was merged during the run; nothing was pushed", and no comment.
3. Push with `gitops.PushExisting`, which refuses when the remote branch is absent (a merge with `close_source_branch` deleted it after step 2), so a deleted branch is never recreated. `gitops.Push`'s existing rule already refuses a remote tip the run never had: a person who pushed during the run makes the push fail; the reason says so ("someone pushed to fugaro/<id> during the run; nothing was overwritten"), and a short note with the marker goes on the still-open PR.
4. Set `pushed_head`.
5. `EnsurePR(PRSpec{Number: N, Branch, Draft: !ready})`: update the draft state only; title and body are left as the human may have edited them. `ensurePR` returns at once on `gitprov.ErrPRNotOpen` (no retry). Finalize handles it before its generic error branch: no not-ready note, no report comment, status `failed`, outcome `none`, reason "PR #N was closed during finalize; the branch was pushed".
6. The outcome is §4.2 unchanged: ready only with a passing verified test on the final HEAD and a `ship` verdict in **this** run. A follow-up that changes nothing can still be ready if the agent verified and the review shipped. When the PR was ready before the run and ends as a draft, the report says "was ready; moved back to draft because <reason>".
7. Before posting the report, list the PR's comments and skip it when a comment by Fugaro's identity already carries this run's marker (follow-ups only; a failed listing posts anyway, with a warning). First runs don't list: `max_retries` is 0 and the claim protocol already stops a duplicate execution.
- *Cost if wrong:* taking the agent's `pr.md` for the title and body is one more field in the update-only path.

**R5. Sessions.**
- **Which session:** the ID in the implement stage's result event (`agent.Result.SessionID`), not the one the runner asked for, so a resume that forks a new ID is still saved correctly.
- **Save (every run, before the caches in writeback, even when cancelled; and, when finalize itself fails, right before `Run` returns that error):** `~/.claude/projects/<escape(workdir)>/<id>.jsonl`, read through `os.Root` as a regular file of at most 64 MiB, redacted line by line, uploaded as `session/<id>.jsonl`, then `session/session.json` `{version: 1, id, head_sha, workdir, bytes}`. The meta goes last, so its presence means the file is complete. A failure is a warning.
- **Restore (a follow-up, at bootstrap, after the lock):** read the previous run's `session/session.json`. Resume only if all hold: the ID is a UUID; `workdir` is this run's workdir; the file is there and within the cap; and the recorded `head_sha` is an ancestor of the PR's head (`git merge-base --is-ancestor`). Otherwise start fresh, with `session_note` saying which check failed.
- **Resumed, and the branch moved** (the previous head is an ancestor but not the head): the prompt lists `git log --oneline <prev>..HEAD` (at most 20 lines) as commits made since the session, by others, to re-read before editing.
- **Fresh:** the prompt carries the root run's task (from `runs/<slug>/<branch's run ID>/task.json`, when it still exists), `git diff --stat origin/<base>...HEAD` (clipped to 4 KiB), and the instruction to read the full diff and log before changing anything.
- **A failed resume:** only `agent.ErrNoSession` ("No conversation found with session ID") falls back to one fresh implement; any other resume error fails the stage like any stage error.
- `escape(workdir)` replaces every byte outside `[A-Za-z0-9]` with `-` (`/work/repo` → `-work-repo`), Claude Code's project-directory naming as observed; `TestSessionPathMatchesClaude` pins it, and T10 confirms it against the image's Claude Code.
- *Cost if wrong:* a wrong path shows as `ErrNoSession` on every resume: the run still finishes fresh, and T10 catches it.

**R6. Trust (binding user decision) and the prompt-injection posture.** A follow-up agent runs with permissions bypassed, the model credential (with `auth: oauth`, a person's subscription token), every workflow secret, the job's service account through the metadata server, and unrestricted egress (§6.1). A comment that asks for "a test helper" whose body sends `env` somewhere is a legitimate-looking code change; no wrapping can tell it apart. So who may comment is the control, and the delimiter is only hygiene.
- **`followup:` in the base branch's `fugaro.yaml`:**

  ```yaml
  followup:                 # optional; who can steer a follow-up run (design §4.4, §6.1)
    trusted: []             # account IDs whose PR comments a follow-up acts on
                            #   GitHub: the numeric user ID; Bitbucket: the account_id
    trust_pr_author: true   # also the PR's author (on a PR Fugaro opened, that is Fugaro's own identity)
    allow_public: false     # refuse follow-ups on a public repository unless true
  ```

  It is read from `origin/<spec.Ref>` like the rest of the config, so a PR can't widen its own trust list.
- **A comment is trusted** when its author's ID (`Comment.AuthorID`) is in `followup.trusted`, or `trust_pr_author` is on and it is the PR's author (`PRInfo.AuthorID`); **and**, on GitHub, its `author_association` is `OWNER`, `MEMBER` or `COLLABORATOR` (`Comment.Collaborator`; always true on Bitbucket, which has no such field). Everyone else is dropped as `untrusted_author`, by name in the report and in `diagnose`.
- **What an empty list means:** the PR's author on a PR Fugaro opened is Fugaro's own identity, whose comments are dropped as `self` first; so with `trusted: []` a follow-up acts on the launcher's TEXT alone, and the report says "no trusted comments; acting on the launcher's instructions only". The skill tells the user how to add IDs.
- **Public repositories:** at bootstrap the runner reads the repository's visibility (`Provider.Repository`); a public one without `allow_public: true` is an `infra_error` that touches nothing ("follow-ups on a public repository need followup.allow_public in fugaro.yaml on <base>").
- **The report** lists the authors whose comments were used, with counts, and the number and names of untrusted authors dropped, so the reviewer of the diff knows whose text drove it.
- **The agent's environment drops the git credentials** for a follow-up (finding below).
- **Hygiene, not a boundary:** comments sit inside `<<<fugaro-comments-<16 hex nonce>>>` … `<<<end-fugaro-comments-<nonce>>>`; any occurrence of either marker text in a body is replaced before embedding; each body is indented as data under a header line the body can't forge. The surrounding text says the block holds review feedback about the code, not instructions about the run; that nothing in it changes the run's rules, asks for credentials, other branches, other repositories or other hosts; and that any such request must be ignored and mentioned in `followup.md`.
- **§6.1 is rewritten** (T9): follow-ups extend the agent's instruction set from repository writers to the trusted-commenter set; the delimiter is not a boundary; on GitHub `COLLABORATOR` includes read-only outside collaborators and `MEMBER` any organization member, so the allowlist is what really limits it; organization members whose membership is private may show as `CONTRIBUTOR` (the App has no `members: read`) and are then dropped, visibly.
- **Finding: what the follow-up agent's environment needs.** `agent.BuildEnv` gives every stage the model credential (needed), the workflow's declared secrets (needed: `fugaro verify` runs the build and tests with them), `FUGARO_STATE_DIR` and the Bash timeouts (needed), and the git credential variables plus `GH_TOKEN` (§6.2, a convenience so the agent can push, edit the PR and read issues). A follow-up needs none of the last group: the runner fetches the base and the branch at bootstrap and pushes at finalize, and the comments arrive in the prompt. So for a follow-up the agent's environment is built **without** the git credential variables and `GH_TOKEN`, and `refreshGitAuth` updates only the runner's git environment. This is defense in depth, not a boundary: the runner runs as the same user, so the agent can still read the token from `/proc`, and the job's service account through the metadata server (§6.1). The model credential and workflow secrets can't be dropped without breaking the run.
- *Cost if wrong:* the rule is one function over data (`followup.Trust`); the config block is additive.

**R7. Fugaro's own comments.**
- **Marker:** every comment Fugaro posts (the report, the not-ready note, the "someone pushed" note) ends with `<!-- fugaro:report run=<run-id> -->` (`gitprov.ReportMarker`). `gitprov.FugaroRun(body)` takes the **last** marker in the body, and, for comments posted before M6, a body starting `### Fugaro run \`<run-id>\``, or the not-ready note's opening `**Fugaro:** this pull request is not ready` (run ID unknown).
- **Only Fugaro's identity counts:** markers and headings are honoured only on `Self` comments. When the identity lookup failed (`Self` unknown for every comment), markers from any author are honoured, with a warning in the log and a note in the report.
- **Identity:** on GitHub the App's bot (`GET /app` with the App's JWT gives `slug`; REST authors are `<slug>[bot]`, GraphQL authors are `<slug>` with `__typename: Bot`); on Bitbucket the repository token's user (`GET /user`, cached).
- **Agent text quoted in the report:** `followup.md` is stripped of every marker pattern and every line starting `### Fugaro run` before it is quoted, so the agent can't forge a report or another run's marker.
- **Stale check** (I1): among `Self` comments recognized by `FugaroRun` with a run ID, none may name a run other than `previous_run` and this run that started after `previous_run` (compared by `started_at` from its record through `Store.Sibling`, else by its run ID's time). Otherwise another run updated the PR after this one was launched: `infra_error`, "run X updated PR #N after this follow-up was launched; start a new follow-up". This closes the race between two CLIs that both passed R1's checks, and it no longer depends on which run posted last.
- *Cost if wrong:* if Bitbucket shows the HTML comment as text (T10 checks), it is a harmless last line; the heading recognizer still works.

**R8. `--retry`, `--run-id` and `--batch`.**
- `--pr` joins `taskFlags`, so `--retry` refuses it. `--retry` of a stored follow-up task re-runs R1's checks with the run itself excluded, and refuses if `previous_run` is no longer the newest run on the PR that pushed ("start a new follow-up").
- `--run-id X --pr N` when `runs/<slug>/X/task.json` already exists: if its `pr`, `task`, `batch` and `overrides` equal this call's, the **stored spec is reused as it is** (its `previous_run` and `requested_by` included), R1's resolution is skipped (the run would find itself active), and the usual path reports `already-launched` or launches an unlaunched one after R1's checks with itself excluded. Otherwise "run ID X already holds a different task" (exit 1).
- `--ref` and `--workflow` are refused with `--pr` (they come from the previous run). `--repo`, `--batch`, `--total-timeout`, `--task-file` and TEXT are allowed. TEXT and `--task-file` are optional with `--pr`, and an empty `--task-file -` is accepted with `--pr` (the runner's default instructions apply).
- `max_parallel` applies as to any launch.
- *Cost if wrong:* flag rules are one table test.

**R9. `ls`, `diagnose`, `cancel` and cost.**
- `ls` rows gain `branch`, `outcome`, `task_pr`, `record_pr`, `pr` (the record's, else the task's), `pushed` (the record has `pushed_head`), `previous_run` and `follow_up`. `ls --pr N` keeps the runs on PR N, newest first, and its totals line is the PR's total cost; it needs one repository (`--repo`, or the local config's single one).
- `diagnose` gains a `follow_up` block (from the record, else the task): the PR, the previous run, `session` and its note, the comment count, the authors used, the untrusted authors dropped, the other omitted counts, and `comments_path`.
- `cancel` is unchanged: the lock, the marker and the grace work on the branch. A cancelled follow-up finalizes like any run: it pushes what it has, the PR goes draft, and the report says cancelled and, if it was ready, that it moved back to draft (§4.5).
- **Cost** is per run, as today. A resumed session reports only its own invocations' cost (Claude Code's `total_cost_usd` is per process, as for the fix stage's resume today), so nothing is counted twice; `ls --pr N` sums the runs.

## Review Focus

These are the failure modes that are easiest to miss, most likely first. Each is pinned by a named test.

1. **A person who can only comment steering an agent that holds secrets.** Pinned by T2 `TestFollowupConfigValidation`, T5 `TestTrustAllowlist` (table: listed ID, PR author, unlisted, GitHub non-collaborator even when listed), T7 `TestFollowUpRefusesPublicRepo`, `TestFollowUpAllowPublic`, `TestFollowUpTrustFromBaseNotBranch` (the branch's `fugaro.yaml` lists an extra ID; it isn't honoured), `TestFollowUpReportNamesAuthors`, and `TestFollowUpAgentEnvHasNoGitToken`.
2. **A second PR, or a merged or declined PR brought back.** Pinned by T1 `TestFakeEnsureByNumberNeverCreates`, T3 `TestGitHubEnsureByNumberClosed`, T4 `TestBitbucketEnsureByNumberDeclined`, T7 `TestFollowUpPRMergedDuringRunNoPush`, `TestFollowUpPRClosedDuringFinalizeNoNote`, `TestFollowUpPushRefusedWhenBranchDeleted`, `TestFollowUpBootstrapRefusalsTouchNothing`, and T9's cloud test (exactly one PR in the fake's state after two follow-ups).
3. **A follow-up running with config its own branch changed.** Pinned by T7 `TestFollowUpConfigFromBase` (the branch sets `commands.test: "true"`, a larger budget and a new instructions file; the base's values are what run).
4. **A PR locked out of follow-ups, or a stale follow-up let through.** Pinned by T7 `TestFollowUpAfterUnpostedReport`, `TestFollowUpAfterGiveUpNote`, `TestFollowUpStaleWhenNewerRunPosted`, `TestFollowUpForgedMarkerIgnored` (a person's comment with a marker neither blocks nor hides), and T9 `TestRunPRPreviousIsNewestPushed`.
5. **Feeding Fugaro's own words back to the agent, or letting the agent forge them.** Pinned by T1 `TestFugaroRunLastMarkerWins`, `TestStripMarkers`, T5 `TestSelectDropsFugaroComments`, `TestSelectDropsSelf`, `TestStripMarkersInAnswer`, T3/T4 `…CommentsSelf`, and T7 `TestFollowUpPromptExcludesReports`, `TestFollowUpReportStripsForgedMarker`.
6. **Comments lost between follow-ups.** Pinned by T5 `TestSelectKeepsCommentsDuringPreviousRun` and `TestSelectKeepsNewestWhenOverBound`.
7. **Resuming a session from another lineage.** Pinned by T6 `TestRestoreRefusesRewrittenBranch`, `TestRestoreRefusesOtherWorkdir`, `TestRestoreNotesMovedBranch`, and T7 `TestFollowUpResumeFailureFallsBackFresh`.
8. **Secrets in comment text or session files, and session exfiltration.** Pinned by T5 `TestSelectRedacts`, T7 `TestFollowUpCommentsRedactedWithWorkflowSecrets` (a local run: a workflow secret only `BuildEnv` knows is redacted from `comments.json`), `TestFollowUpNeverLogsCommentBodies`, T6 `TestSaveSessionRefusesSymlink`, `TestSaveSessionRedacts`, `TestSaveSessionCap`, and T9's cloud secret scan over `comments.json`, `followup.md` and `session/`.
9. **The launch paths refusing or duplicating themselves.** Pinned by T9 `TestRunPRRepeatRunIDIsIdempotent`, `TestRetryFollowUpRefusedWhenSuperseded`, `TestRunPRPrintsPRBranch`.
10. **Cost counted twice.** Pinned by T7 `TestFollowUpCostIsOwnStagesOnly` and T8 `TestLsPRTotals`.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/task/task.go`, `schemas/task.schema.json`, `testdata/task/**` | `branch` must be `fugaro/<run-id>` | 1 |
| `internal/runstore/runstore.go`, `schemas/result.schema.json` | `Record.FollowUp`, `Record.PushedHead`; `Store.Sibling` | 1 |
| `internal/gitprov/gitprov.go`, `internal/gitprov/marker.go` (new) | `RepoInfo`, `PRInfo`, `Comment`, `Repository`, `PullRequest`, `Comments`, `PRSpec.Number`, `ErrPRNotOpen`, `ReportMarker`, `FugaroRun`, `StripMarkers` | 1 |
| `internal/gitprov/fake/fake.go` | PR state, visibility, foreign comments with times, update-only `EnsurePR` | 1 |
| `internal/config/{config,validate,example.yaml}`, `schemas/fugaro.schema.json`, `testdata/config/**` | the `followup:` block | 2 |
| `internal/gitprov/httpjson/httpjson.go`, `internal/gitprov/github/{github,comments}.go`, `testdata/*.json` | header paging pinned to the API host; the GitHub reads and the update-only path | 3 |
| `internal/gitprov/bitbucket/{bitbucket,comments}.go`, `testdata/*.json`, `live_test.go` | the Bitbucket reads and the update-only path | 4 |
| `internal/followup/` (new: `trust.go`, `select.go`, `prompt.go`, `snapshot.go`) | trust, comment selection, bounds, prompts, `comments.json` | 5 |
| `internal/agent/session.go` (new), `internal/agent/fakeclaude/main.go` | session paths; the fake writes and checks session files | 6 |
| `internal/runstore/session.go` (new), `internal/runner/session.go` (new), `internal/runner/lockcache.go` (`writeback`) | saving and restoring sessions | 6 |
| `internal/gitops/gitops.go` | `ShowFile`, `PushExisting` | 7 |
| `internal/runner/runner.go`, `internal/runner/followup.go` (new), `internal/runner/report.go`, `internal/runner/prompts.go` | the follow-up bootstrap, agent loop, finalize and report | 7 |
| `internal/runview/runview.go`, `internal/cli/{ls,diagnose}.go` | row fields; `ls --pr`; `diagnose`'s block | 8 |
| `internal/cli/run.go`, `internal/cli/followup.go` (new), `internal/e2e/cloud_test.go` | `run --pr`, its checks, `--retry`/`--run-id`; the hermetic cloud follow-up with a fixed workdir | 9 |
| `plugin/skills/followup/SKILL.md` (new), `plugin/.claude-plugin/plugin.json`, `docs/**`, `internal/e2e/live_gcp_test.go`, `deploy/sandbox/fugaro.yaml` | the skill, the docs, the live test code | 10 |
| — (controller-run) | live verification on the sandbox | 11 |

## Task dependency graph and lanes

| Task | Depends on | Why |
|---|---|---|
| T1 contracts | — | |
| T2 `followup:` config | — | |
| T3 GitHub | T1 | the interface and markers |
| T4 Bitbucket | T1 | the interface and markers |
| T5 `followup` package | T1, T2 | `gitprov.Comment`, `FugaroRun`, `config.Followup` |
| T6 sessions | T1 | `Store.Sibling` |
| T7 runner | T1, T2, T5, T6 | the fake provider, the config block, selection and prompts, session restore |
| T8 `ls`, `diagnose` | T1 | `Record.FollowUp`, `PushedHead` |
| T9 `run --pr`, cloud e2e | T7, T8 | the runner end to end; `lsFilter.pr` and the row fields |
| T10 skill, docs, live test code | T2, T3, T4, T9 | documents and exercises everything |
| T11 live verification | all | |

```
T1 ─┬─► T3 ──────────────────────────────┐
    ├─► T4 ──────────────────────────────┤
    ├─► T5 ─┐   (T5 also needs T2)       ├─► T10 ─► T11
    ├─► T6 ─┴─► T7 ─┐  (T7 also needs T2) │
    └─► T8 ─────────┴─► T9 ─────────────┘
T2 ───────────────────────────────────────▲
```

**One lane.** M6 is eleven tasks, and review, not typing, is the bottleneck. Run them in order T1, T2, T3, T4, T5, T6, T7, T8, T9, T10, T11 on one branch, `m6`. T2, T3, T4, T5, T6 and T8 touch disjoint files after T1, so a controller *may* run some concurrently in worktrees; nothing requires it, and the hot files below are safe either way.

**Hot files,** and how they're kept safe:
- `internal/gitprov/gitprov.go` and `internal/gitprov/fake/fake.go`: T1 only. Later tasks consume them; a later fake knob goes in its own commit on top, never reshaping T1's types.
- `internal/runner/runner.go`: three tasks, strictly in order. T1 appends the marker to the not-ready note (one line). T6 adds the `sessionID` field to `run`, sets it from the implement result in `agentLoop`, and calls `saveSession` before returning a finalize error (a few lines), and otherwise touches only `lockcache.go` and its new `session.go`. T7 owns every other change. T6 lands before T7 starts.
- `internal/runstore/runstore.go`: T1 (`FollowUp`, `PushedHead`, `Sibling`). T6 adds `session.go` beside it.
- `internal/config/*`, `schemas/fugaro.schema.json`: T2 only.
- `internal/gitprov/httpjson/httpjson.go`: T3 only (T4 uses the paging helper).
- `internal/gitops/gitops.go`: T7 only.
- `internal/cli/ls.go`: T8 only (`lsFilter.pr`, the pre-filter, `--pr`). T9 calls `loadRows` with it.
- `internal/cli/run.go`: T9 only.
- `schemas/result.schema.json`, `schemas/task.schema.json`: T1 only. `schemas/schemas_test.go`: T1 (task and result cases), then T2 (the `fugaro.yaml` corpus); each adds its own test functions, so a conflict is only ever adjacent additions: keep both.
- `docs/design/v1.md`, `internal/e2e/live_gcp_test.go`: T10 only.

---

### Task 1: Contracts: the task and record fields, the provider interface, markers, and the fake

**Files:**
- Modify: `internal/task/task.go`, `internal/task/task_test.go`, `schemas/task.schema.json`, `testdata/task/valid/followup.json`; Create: `testdata/task/invalid/followup-bad-branch.json`
- Modify: `internal/runstore/runstore.go`, `internal/runstore/runstore_test.go`, `schemas/result.schema.json`, `schemas/schemas_test.go`
- Modify: `internal/gitprov/gitprov.go`; Create: `internal/gitprov/marker.go`, `internal/gitprov/marker_test.go`
- Modify: `internal/gitprov/fake/fake.go`, `internal/gitprov/fake/fake_test.go`
- Modify: `internal/runner/runner.go` (the not-ready note gains the marker, one line), `internal/runner/report.go` (the report gains the marker, one line), and their tests

**Interfaces:**
- Produces:

```go
// internal/task
var BranchRE = regexp.MustCompile(`^fugaro/[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)
// Validate: a follow-up's branch must match BranchRE:
//   "task spec: branch %q must be fugaro/<run-id>"
func BranchRunID(branch string) (string, bool)

// internal/runstore
type FollowUp struct {
	PR               int            `json:"pr"`
	PreviousRun      string         `json:"previous_run"`
	StartSHA         string         `json:"start_sha,omitempty"`
	Session          string         `json:"session,omitempty"` // "resumed" | "fresh"
	SessionNote      string         `json:"session_note,omitempty"`
	Comments         int            `json:"comments"`
	Authors          map[string]int `json:"authors,omitempty"`           // display name → comments used
	UntrustedAuthors []string       `json:"untrusted_authors,omitempty"` // display names, sorted, at most 20
	Omitted          map[string]int `json:"omitted,omitempty"`           // followup.Select's reasons
}
// Record gains:
//   FollowUp   *FollowUp `json:"follow_up,omitempty"`
//   PushedHead string    `json:"pushed_head,omitempty"` // set right after a successful push, every run
func (s *Store) Sibling(runID string) *Store // the same slug and bucket, another run

// internal/gitprov
type RepoInfo struct{ Private bool }

type PRState string
const (PROpen PRState = "open"; PRMerged PRState = "merged"; PRClosed PRState = "closed")

type PRInfo struct {
	Number       int
	URL          string
	State        PRState
	Draft        bool
	AuthorID     string // the PR author's account ID (GitHub numeric user ID; Bitbucket account_id)
	SourceBranch string
	SourceRepo   string // owner/name of the head repository, as the provider spells it
	HeadSHA      string // may be abbreviated (Bitbucket gives 12 hex); compare with SameCommit
}

type CommentKind string
const (CommentInline CommentKind = "inline"; CommentReview CommentKind = "review"; CommentGeneral CommentKind = "general")

type Comment struct {
	ID           string
	Kind         CommentKind
	Author       string // display name or login, for the report
	AuthorID     string // stable account ID, for the trust rule
	Collaborator bool   // GitHub: author_association OWNER, MEMBER or COLLABORATOR; Bitbucket: always true
	Self         bool   // posted as the identity Fugaro uses
	SelfKnown    bool   // the adapter could tell (its identity lookup worked)
	Resolved     bool   // inline only: its thread is resolved
	Outdated     bool   // inline only: the line it was on has changed
	Truncated    bool   // inline only: its thread had more comments than were read
	Deleted      bool
	Path         string
	Line         int
	Body         string
	CreatedAt    time.Time
	URL          string
}

// PRSpec gains Number: when non-zero, EnsurePR updates pull request Number's
// draft state only. It never creates, and never touches the title or body
// beyond the draft prefix. It returns ErrPRNotOpen (wrapped, with the PR) when
// that PR isn't open, or its source branch isn't Branch.
//   Number int `json:"number,omitempty"`
var ErrPRNotOpen = errors.New("pull request is not open on this branch")

// Provider gains:
//   Repository(ctx context.Context) (RepoInfo, error)
//   PullRequest(ctx context.Context, number int) (PRInfo, error)
//   Comments(ctx context.Context, number int) ([]Comment, error) // every comment, oldest first; filtering is the caller's

func SameCommit(a, b string) bool // equal, or one an abbreviation (≥ 7 hex) of the other

// marker.go
func ReportMarker(runID string) string              // "<!-- fugaro:report run=<run-id> -->"
func FugaroRun(body string) (runID string, ok bool) // the LAST marker; else the legacy heading at the start; else the not-ready note ("" run ID)
func StripMarkers(s string) string                  // removes every marker and every line starting "### Fugaro run"
```

- The fake gains `PRState.State gitprov.PRState` (empty means open, so existing state files still load), `PRState.Source string` (the source repository; empty means the fake's own), `PRState.AuthorID string`, `PRState.Foreign []gitprov.Comment` (comments a test injects as if people wrote them), `PRState.CommentTimes []time.Time` (when each Fugaro-posted comment in `Comments []string` was posted; a missing time sorts first), `State.Public bool`, and `Provider.SelfID string`. `Comments()` returns foreign and Fugaro-posted comments merged by time, Fugaro's as `CommentGeneral` with `Self: true`, `SelfKnown: true` everywhere. `Provider.FailComments`, `FailPullRequest` and `FailRepository int` inject errors. Existing JSON state files still decode (the new fields are `omitempty`).

- [ ] **Step 1: Write the failing tests.**
  - `internal/task`: `TestFollowUpBranchMustBeRunBranch` (`fugaro/20260925-000000-0000` ok; `main`, `fugaro/x`, `fugaro/20260925-000000-0000/x` refused); `TestBranchRunID`. The schema corpus (`TestTaskSchemaCorpus`) gains `testdata/task/invalid/followup-bad-branch.json`, and `testdata/task/valid/followup.json` changes its `ref` from the branch to `main` (R3; the old value stays schema-valid). The schema's `branch` gets the `BranchRE` pattern.
  - `internal/runstore`: `TestRecordFollowUpRoundTrip` (marshal and parse; an old record without it parses); `TestRecordPushedHead`; `TestSibling`.
  - `schemas`: `result.schema.json` accepts a record with `follow_up` and `pushed_head`, and refuses `session: "maybe"`.
  - `internal/gitprov`: `TestFugaroRunRecognizesMarkerAndLegacyHeading` (the marker anywhere; the heading at the start only; the not-ready note; prose quoting a run ID isn't matched); `TestFugaroRunLastMarkerWins`; `TestStripMarkers`; `TestReportMarkerRoundTrip`; `TestSameCommit` (full vs 12-hex prefix; two different 12-hex prefixes; under 7 hex never matches).
  - `internal/gitprov/fake`: `TestFakeEnsureByNumberNeverCreates` (a closed PR 1 → `ErrPRNotOpen`, still one PR); `TestFakeEnsureByNumberBranchMismatch`; `TestFakeCommentsMergedByTime`; `TestFakeStateFileBackCompat` (an M5 state file loads); `TestFakeRepositoryVisibility`.
  - `internal/runner`: `TestReportCarriesMarker`; `TestNotReadyNoteCarriesMarker` (extend `TestEnsurePRGivesUpAfterDeadlineStillComments`).
- [ ] **Step 2:** Run `go test ./internal/task/ ./internal/runstore/ ./schemas/ ./internal/gitprov/... ./internal/runner/ -run 'FollowUp|Branch|Sibling|Pushed|FugaroRun|Marker|Strip|SameCommit|Fake|Report|NotReady'`. Expected: FAIL (undefined names; the GitHub and Bitbucket adapters no longer satisfy `Provider`).
- [ ] **Step 3: Implement.** Give the GitHub and Bitbucket adapters stub methods returning `errors.New("github: not implemented until M6 task 3")` (and `task 4` for Bitbucket), so the tree compiles; T3 and T4 replace them. `Report` and the not-ready note append `"\n" + gitprov.ReportMarker(runID) + "\n"`.
- [ ] **Step 4:** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "follow-up contracts: task branch rule, record follow_up and pushed_head, provider reads, report marker"`

---

### Task 2: The `followup:` block in `fugaro.yaml`

**Files:**
- Modify: `internal/config/config.go` (`Config.Followup`), `internal/config/validate.go`, `internal/config/example.yaml`, `internal/config/config_test.go`
- Modify: `schemas/fugaro.schema.json`, `schemas/schemas_test.go`
- Create: `testdata/config/valid/followup-full.yaml`, `testdata/config/invalid/followup-{bad-github-id,bad-bitbucket-id,duplicate-id,unknown-field}.yaml`

**Interfaces:**
- Produces:

```go
type Followup struct {
	Trusted       []string `yaml:"trusted"`
	TrustPRAuthor *bool    `yaml:"trust_pr_author"` // nil means true
	AllowPublic   bool     `yaml:"allow_public"`
}
// Config gains: Followup Followup `yaml:"followup"`
func (f Followup) TrustsPRAuthor() bool
```

- **Validation**, each problem at `followup.<field>` or `followup.trusted[i]`:
  - with `git.provider: github`, an ID is 1–20 digits (the numeric user ID, which a rename doesn't change; a login is refused, with a hint: `gh api users/<login> --jq .id`);
  - with `git.provider: bitbucket`, an ID matches `^[0-9]{1,10}:[0-9a-f-]{36}$` or `^[0-9a-f]{24}$` (the two `account_id` forms), or `^\{[0-9a-f-]{36}\}$` (a UUID);
  - no duplicates; no unknown field (strict decoding, as everywhere).
- The schema mirrors the shapes (the provider-dependent ID rule as `if`/`then` on `git.provider`). `fugaro config example` documents the block commented out, with the R6 explanation in two comment lines.

- [ ] **Step 1: Write the failing tests.** `TestFollowupDefaults` (no block: empty list, `TrustsPRAuthor()` true, `AllowPublic` false), `TestFollowupConfigValidation` (a table over the invalid fixtures, asserting each `Problem.Path`), `TestFugaroSchemaCorpus` gains the fixtures, and `TestExampleValidates` keeps passing.
- [ ] **Step 2:** `go test ./internal/config/ ./schemas/ -run 'Followup|Example|Schema'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./internal/config/ ./schemas/ ./internal/cli/ -run 'Validate|Example|Schema|Followup'`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "config: the followup block, who can steer a follow-up run"`

---

### Task 3: The GitHub adapter reads the repository, a PR and its comments, and updates a PR by number

**Files:**
- Modify: `internal/gitprov/httpjson/httpjson.go` and its test (`DoPage`)
- Modify: `internal/gitprov/github/github.go` (`Repository`, `PullRequest`, `EnsurePR`'s `Number` path); Create: `internal/gitprov/github/comments.go`
- Create fixtures: `internal/gitprov/github/testdata/{repo_private,repo_public,pull_open,pull_merged,pull_fork,comments,comments_paged,comments_self,comments_foreign_link,app_identity_forbidden,ensure_by_number,ensure_by_number_closed}.json` (the `httpfixture` format the existing fixtures use)
- Test: `internal/gitprov/github/github_test.go`

**Interfaces:**
- Consumes: T1's types, `PRSpec.Number`, `ErrPRNotOpen`.
- Produces:
  - `httpjson.(*Client).DoPage(ctx, method, path string, in, out any) (next string, err error)`: like `Do`, and returns the `Link` header's `rel="next"` URL as a path, **only** when that URL's scheme and host equal `BaseURL`'s; any other next URL is an error ("paging left the API host"), so the bearer token never goes elsewhere. T4 uses the same host check for Bitbucket's body `next`.
  - `Repository`: `GET /repos/{o}/{r}` → `private`.
  - `PullRequest`: `GET /repos/{o}/{r}/pulls/{n}` → `state` (`open`/`closed`) plus `merged` → `PRMerged`; `user.id` → `AuthorID`; `head.ref`, `head.repo.full_name` (null for a deleted fork: `SourceRepo` empty), `head.sha`, `draft`, `html_url`.
  - `Comments`, oldest first:
    - review threads through GraphQL, 50 threads per page (following `pageInfo`), 50 comments per thread (`Truncated` when `comments.pageInfo.hasNextPage`):
      ```graphql
      query($owner:String!,$name:String!,$n:Int!,$after:String){repository(owner:$owner,name:$name){pullRequest(number:$n){
        reviewThreads(first:50,after:$after){pageInfo{hasNextPage endCursor}nodes{isResolved isOutdated path line
          comments(first:50){pageInfo{hasNextPage}nodes{id body createdAt url authorAssociation
            author{__typename login ... on User{databaseId} ... on Bot{databaseId}}}}}}}}}
      ```
    - review summaries: `GET /pulls/{n}/reviews?per_page=100` (paged), non-empty `body` only, as `CommentReview`.
    - general comments: `GET /issues/{n}/comments?per_page=100` (paged), as `CommentGeneral`.
    - `AuthorID` from `user.id` / `databaseId`; `Collaborator` from `author_association` / `authorAssociation` ∈ {`OWNER`, `MEMBER`, `COLLABORATOR`}.
    - `Self` when the author is the App's bot: REST login `<slug>[bot]`, or GraphQL `__typename == "Bot"` and login `<slug>`; the slug from `GET /app` with the App JWT, fetched once per Provider. A failed `GET /app` leaves `SelfKnown` false everywhere; `Comments` still succeeds, and the adapter's `Warn` gets one message.
    - At most 20 pages per listing, else an error naming the cap.
  - `EnsurePR` with `Number`: `PullRequest(Number)`; not open, or `SourceBranch != spec.Branch`, gives `ErrPRNotOpen` wrapped with the state; else the existing `update(…, spec.Draft)`. `find`/`create` are never reached.
- The installation token's permissions are unchanged: `pull_requests: write` covers reviews and threads, `issues: read` covers issue comments, `metadata: read` covers the repository read (§6.1).

- [ ] **Step 1: Write the failing tests.** `TestDoPageFollowsLink`, `TestDoPageRefusesForeignHost` (a `Link` to another host: error, and the fake server of that host saw no request), `TestGitHubRepositoryVisibility`, `TestGitHubPullRequestOpen`, `TestGitHubPullRequestMerged`, `TestGitHubPullRequestFork`, `TestGitHubComments` (one resolved and one unresolved thread, an outdated one, a truncated one, a review summary, two issue comments, one by a `CONTRIBUTOR`; asserts every field), `TestGitHubCommentsPaged`, `TestGitHubCommentsPageCap`, `TestGitHubCommentsSelf` (the bot's issue comment through REST and its inline comment through GraphQL both have `Self`), `TestGitHubCommentsIdentityForbidden`, `TestGitHubEnsureByNumber` (no `POST /pulls` in the exchange), `TestGitHubEnsureByNumberClosed` (`ErrPRNotOpen`, no mutation sent), `TestGitHubEnsureByNumberBranchMismatch`.
- [ ] **Step 2:** `go test ./internal/gitprov/httpjson/ ./internal/gitprov/github/ -run 'DoPage|Repository|PullRequest|Comments|ByNumber'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Fixtures are hand-written from GitHub's REST and GraphQL documentation (cite the pages in a comment at the top of `comments.go`); no GitHub live check exists in M6 (Open question 4).
- [ ] **Step 4:** `go test -race ./internal/gitprov/...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "github: read the repository, a pull request and its threads and comments; update a PR by number"`

---

### Task 4: The Bitbucket adapter reads the repository, a PR and its comments, and updates a PR by number

**Files:**
- Modify: `internal/gitprov/bitbucket/bitbucket.go`; Create: `internal/gitprov/bitbucket/comments.go`
- Create fixtures: `internal/gitprov/bitbucket/testdata/{repo_private,repo_public,pull_open,pull_merged,pull_declined,comments,comments_paged,comments_foreign_next,comments_self,user_forbidden,ensure_by_number,ensure_by_number_declined}.json`
- Modify: `internal/gitprov/bitbucket/live_test.go` (`TestLiveBitbucket` reads back its PR, the repository and its comments, and records the new fixtures under `FUGARO_LIVE_RECORD_DIR`), `internal/gitprov/bitbucket/recorded_test.go`
- Test: `internal/gitprov/bitbucket/bitbucket_test.go`

**Interfaces:**
- Produces:
  - `Repository`: `GET /repositories/{ws}/{slug}` → `is_private`.
  - `PullRequest`: `GET …/pullrequests/{n}` → `state` `OPEN` → `PROpen`, `MERGED` → `PRMerged`, `DECLINED` and `SUPERSEDED` → `PRClosed`; `author.account_id` → `AuthorID`; `source.branch.name`, `source.repository.full_name`, `source.commit.hash` (12 hex: compare with `gitprov.SameCommit`), `draft`, `links.html.href`.
  - `Comments`: `GET …/pullrequests/{n}/comments?pagelen=100`, following the body's `next` only on the API's own scheme and host (same 20-page cap). `inline` present → `CommentInline` with `inline.path` and `inline.to` (else `inline.from`); `Resolved` when the comment, or the root of its `parent` chain, has a non-null `resolution`; `Deleted` from `deleted`; `Outdated` from `inline.outdated`; no `CommentReview` on Bitbucket. `Author` is `user.display_name`, `AuthorID` is `user.account_id`, `Collaborator` is always true (R6: the allowlist is Bitbucket's only author rule).
  - `Self` when `user.uuid` equals the token's user, from `GET /user`, fetched once. A 403 there leaves `SelfKnown` false, with one warning; markers then filter (R7).
  - `EnsurePR` with `Number`: as in T3, with `update` unchanged.
- **The live test confirms the field names** (`is_private`, `resolution`, `inline.outdated`, `deleted`, `author.account_id`, and `GET /user` with a repository access token) and records fixtures; until T11 runs it, each hand-written field is listed in a comment in `comments.go` as "confirm live (T11 step 2)".

- [ ] **Step 1: Write the failing tests.** `TestBitbucketRepositoryVisibility`, `TestBitbucketPullRequestOpen`, `TestBitbucketPullRequestMerged`, `TestBitbucketPullRequestDeclined`, `TestBitbucketComments` (a resolved inline thread with a reply, an unresolved one, a deleted comment, a general comment), `TestBitbucketCommentsPaged`, `TestBitbucketCommentsRefusesForeignNext`, `TestBitbucketCommentsSelf`, `TestBitbucketCommentsUserForbidden`, `TestBitbucketEnsureByNumber` (no `POST …/pullrequests` in the exchange), `TestBitbucketEnsureByNumberDeclined`.
- [ ] **Step 2:** `go test ./internal/gitprov/bitbucket/ -run 'Repository|PullRequest|Comments|ByNumber'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Extend `TestLiveBitbucket`: after its PR exists, post a general comment and an inline comment with its token, resolve the inline one if the API allows (`POST …/comments/{id}/resolve`; a `FACT` records the outcome), then read `Repository`, `PullRequest` and `Comments` and record them. A `FACT` records whether `GET /user` works with the token and whether the raw content keeps `<!-- … -->`.
- [ ] **Step 4:** `go test -race ./internal/gitprov/...` and `go vet -tags live ./internal/gitprov/...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "bitbucket: read the repository, a pull request and its comments; update a PR by number"`

---

### Task 5: The `followup` package: trust, selecting comments, bounding them, and the prompts

**Files:**
- Create: `internal/followup/trust.go`, `internal/followup/select.go`, `internal/followup/prompt.go`, `internal/followup/snapshot.go`, and `*_test.go` for each; `internal/followup/testdata/prompt_*.golden`

**Interfaces:**
- Consumes: `gitprov.Comment`, `gitprov.PRInfo`, `gitprov.FugaroRun`, `gitprov.StripMarkers`, `config.Followup`.
- Produces:

```go
// Trust is R6's rule: listed, or the PR's author when allowed; and a collaborator.
type Trust struct {
	IDs           map[string]bool
	PRAuthorID    string // empty when trust_pr_author is off
}
func NewTrust(f config.Followup, pr gitprov.PRInfo) Trust
func (t Trust) Allows(c gitprov.Comment) bool

type Limits struct{ MaxComments, MaxThreads, MaxBodyBytes, MaxTotalBytes int }
var DefaultLimits = Limits{MaxComments: 60, MaxThreads: 40, MaxBodyBytes: 4 << 10, MaxTotalBytes: 32 << 10}

// Selection is what the agent gets. Omitted counts the drops by reason:
// "resolved", "fugaro", "self", "untrusted_author", "deleted", "before_since", "empty", "over_limit".
type Selection struct {
	Since            time.Time
	Comments         []gitprov.Comment // bodies redacted and clipped; rendered oldest first
	Authors          map[string]int    // display name → comments kept
	UntrustedAuthors []string          // sorted, unique, at most 20
	Omitted          map[string]int
	MarkersFromAnyone bool             // SelfKnown was false: markers honoured from every author (R7)
}

// Select applies R2 with trust t. redact is the run's redactor, applied before
// clipping, so a secret straddling the cut is never half-kept.
func Select(all []gitprov.Comment, since time.Time, t Trust, l Limits, redact func(string) string) Selection

// FugaroRuns are the run IDs named by Fugaro's comments (R7): Self comments only,
// or every comment when no comment is SelfKnown. Comments without a run ID are skipped.
func FugaroRuns(all []gitprov.Comment) []string

type PromptData struct {
	PR               int
	PRURL            string
	Branch, Base     string
	StateDir         string
	Instructions     string   // the launcher's TEXT; empty gives DefaultInstructions
	Resumed          bool
	MovedCommits     []string // resumed, branch moved: `git log --oneline prev..HEAD`, at most 20
	RootTask         string   // fresh only; empty when the root run's task.json is gone
	DiffStat         string   // fresh only; clipped to 4 KiB
	PreviousAnswer   string   // the previous follow-up's followup.md, redacted, stripped, clipped to 4 KiB
	Nonce            string   // 16 hex, from crypto/rand
}
const DefaultInstructions = "Address the unresolved review comments on this PR."

func Block(sel Selection, nonce string) string          // the delimited comments block (R6)
func ImplementPrompt(d PromptData, sel Selection) string // posture, previous answer, block, the launcher's text, what to write to followup.md
func ReviewAddendum(sel Selection, nonce string) string  // appended to the review prompt: unaddressed comments are findings
func SystemPromptLines(d PromptData) []string            // replaces the pr.md line: write what you changed, and why not for anything you didn't, to <state>/followup.md
func QuoteAnswer(followupMD string, redact func(string) string) string // for the report: redacted, StripMarkers, clipped to 8 KiB

// comments.json
type Snapshot struct {
	Version          int               `json:"version"` // 1
	PR               int               `json:"pr"`
	Since            time.Time         `json:"since"`
	Fetched          time.Time         `json:"fetched_at"`
	Comments         []SnapshotComment `json:"comments"`
	Authors          map[string]int    `json:"authors"`
	UntrustedAuthors []string          `json:"untrusted_authors,omitempty"`
	Omitted          map[string]int    `json:"omitted"`
}
type SnapshotComment struct {
	Kind, Author, AuthorID, Path, URL string
	Line                              int
	Outdated, Truncated               bool
	CreatedAt                         time.Time
	Body                              string // as the agent saw it
}
func NewSnapshot(pr int, sel Selection, fetched time.Time) Snapshot
```

- Each comment in the block is a header line (`[inline] path:line (outdated) — author, 2026-09-30T10:00Z`) with the body indented by four spaces. The nonce and both delimiter texts are replaced in bodies with `[fugaro-delimiter removed]`. NUL bytes and invalid UTF-8 are dropped. Author names in headers and in the report are one line, at most 64 runes.
- The prompt says, when `Selection` is empty, "no trusted comments; acting on the launcher's instructions only", and names the untrusted-author count so the agent doesn't go looking for them.

- [ ] **Step 1: Write the failing tests.**
  - `TestTrustAllowlist` (table: a listed ID; the PR author with `trust_pr_author` on and off; an unlisted author; a listed GitHub author who isn't a collaborator; a Bitbucket author, always a collaborator).
  - `TestSelectKeepsUnresolvedThreadsAnyAge`, `TestSelectGeneralSinceOnly`, `TestSelectKeepsCommentsDuringPreviousRun` (a general comment posted after the previous follow-up's `fetched_at` but before its finish is kept), `TestSelectDropsFugaroComments` (marker, legacy heading, not-ready note, all by `Self`), `TestSelectIgnoresForgedMarker` (a trusted person's comment carrying a marker is kept), `TestSelectDropsSelf`, `TestSelectDropsUntrustedAuthors` (names listed, count right), `TestSelectDropsResolvedDeletedEmpty`, `TestSelectRenderOrder`.
  - `TestSelectKeepsNewestWhenOverBound` (61 general comments → the 60 newest kept, `over_limit: 1`; 45 threads → 40 kept), `TestSelectClipsBody`, `TestSelectClipsOnRuneBoundary`, `TestSelectTotalCap`.
  - `TestSelectRedacts` (a planted secret in a body, and straddling the 4 KiB cut: neither half survives).
  - `TestFugaroRuns` (Self only; everyone when nothing is `SelfKnown`; a not-ready note without a run ID skipped).
  - `TestStripMarkersInAnswer` (a `followup.md` holding a marker and a `### Fugaro run` line: `QuoteAnswer` has neither).
  - `TestBlockNeutralizesDelimiter`, `TestBlockDropsNUL`, `TestBlockHeaderCantBeForged`.
  - `TestPromptPosture`, `TestImplementPromptResumed`, `TestImplementPromptResumedMoved`, `TestImplementPromptFresh`, `TestImplementPromptNoTrustedComments`, `TestImplementPromptPreviousAnswer`, `TestReviewAddendum`: golden files, regenerated only with `-update`.
  - `TestSnapshotRoundTrip`.
- [ ] **Step 2:** `go test ./internal/followup/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Pure functions only; no I/O, no logging.
- [ ] **Step 4:** `go test -race ./internal/followup/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "followup: trust, select, bound and redact PR comments, and build the follow-up prompts"`

---

### Task 6: Sessions: saving every run's session, and restoring one for a follow-up

**Files:**
- Create: `internal/agent/session.go`, `internal/agent/session_test.go`
- Modify: `internal/agent/fakeclaude/main.go` (and its test in `internal/agent/agent_test.go`)
- Create: `internal/runstore/session.go`, `internal/runstore/session_test.go`
- Create: `internal/runner/session.go`, `internal/runner/session_test.go`
- Modify: `internal/runner/lockcache.go` (`writeback` calls `r.saveSession` first), `internal/runner/runner.go` (only: `run` gains `sessionID string`, set from the implement stage's `agent.Result.SessionID` in `agentLoop`; `Run` calls `r.saveSession` before returning a finalize error)

**Interfaces:**
- Produces:

```go
// internal/agent
// SessionDir is where Claude Code keeps workDir's sessions under home.
func SessionDir(home, workDir string) string // home/.claude/projects/<escaped workDir>
func ValidSessionID(id string) bool         // a lower-case UUID
var ErrNoSession = errors.New("claude found no conversation to resume")
// Claude.Run returns an error wrapping ErrNoSession when a --resume run's stderr
// says "No conversation found with session ID" (the text T11 records live).

// internal/runstore
const MaxSessionBytes = 64 << 20
type SessionMeta struct {
	Version int    `json:"version"` // 1
	ID      string `json:"id"`
	HeadSHA string `json:"head_sha"`
	WorkDir string `json:"workdir"`
	Bytes   int64  `json:"bytes"`
}
func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error // <id>.jsonl, then session.json
func (s *Store) ReadSession(ctx context.Context) (*SessionMeta, []byte, error)    // capped; ErrNotFound when absent

// internal/runner
func (r *run) saveSession(ctx context.Context)                      // never fails the run
type restored struct{ ID string; Resumed bool; Note string; Moved []string }
func (r *run) restoreSession(ctx context.Context, prev *runstore.Store, head string) restored
```

- **HOME:** the session directory is under the agent's `HOME` (`envLookup(r.d.Env, "HOME")`, which `agent.BuildEnv` passes through). `fakeclaude` writes session files only when `HOME` is set, so harnesses that don't set it are unaffected; every new test that exercises sessions sets `HOME` to `t.TempDir()`, so no test writes under the developer's real `~/.claude`.
- `fakeclaude`: with `--session-id X` it creates `$HOME/.claude/projects/<escaped cwd>/X.jsonl` and appends one JSON line per invocation (`{"prompt": …}`); with `--resume X` it fails like Claude Code (exit 1, stderr `No conversation found with session ID: X`, no result event) when that file is missing, and otherwise appends. Its result event carries the session ID it used. Each call's `cost` in `script.json` is that invocation's alone.
- `saveSession`: skipped when no implement stage returned a session ID. It opens `SessionDir` through `os.Root`, `Lstat`s `<id>.jsonl` (a symlink or non-regular file → warning, nothing uploaded), reads at most `MaxSessionBytes + 1` (over → warning "session too large to save; the next follow-up starts fresh"), redacts each line with `agent.Redact`, and calls `PutSession` with `r.rec.PushedHead` (else HEAD). It runs on writeback's context, before the caches, and also for a cancelled run; when finalize fails, `Run` calls it on a 30-second context of its own before returning.
- `restoreSession`: every failure is a `Note`, never an error: no `session.json` ("the previous run saved no session"; every pre-M6 run), a bad ID, another workdir, a missing or oversized file, an unknown or non-ancestor `head_sha` ("the branch was rewritten since the previous session"). It writes the file through `os.Root` with `O_CREATE|O_EXCL`, mode 0600, creating the project directory 0700; an existing file there (the image never has one) → fresh with a note.

- [ ] **Step 1: Write the failing tests.**
  - `internal/agent`: `TestSessionPathMatchesClaude` (`/work/repo` → `<home>/.claude/projects/-work-repo`; `/a/b.c_d` → `-a-b-c-d`), `TestValidSessionID`, `TestFakeClaudeWritesSession`, `TestFakeClaudeNoHomeNoSession`, `TestFakeClaudeResumeMissing` (the error wraps `ErrNoSession`).
  - `internal/runstore`: `TestSessionRoundTrip`, `TestReadSessionCap`, `TestReadSessionMetaWithoutData`.
  - `internal/runner`: `TestSaveSessionUploads` (through a harness run: `session/<id>.jsonl` and `session.json` exist, `head_sha` is `pushed_head`), `TestSaveSessionUsesResultID` (the result event reports another ID than the one asked for: that file is saved), `TestSaveSessionRedacts`, `TestSaveSessionRefusesSymlink` (the step replaces the file with a symlink to a file holding a canary; the canary is in no bucket object), `TestSaveSessionCap`, `TestSaveSessionOnCancel`, `TestSaveSessionOnFinalizeError`, `TestRestoreResumes`, `TestRestoreNotesMovedBranch`, `TestRestoreRefusesRewrittenBranch`, `TestRestoreRefusesOtherWorkdir`, `TestRestoreBadID` (`../../x`), `TestRestoreNoSession`.
- [ ] **Step 2:** `go test ./internal/agent/... ./internal/runstore/ ./internal/runner/ -run 'Session|Restore'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./internal/agent/... ./internal/runstore/ ./internal/runner/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "runner: save each run's Claude Code session, and restore one safely"`

---

### Task 7: The runner's follow-up path

**Files:**
- Modify: `internal/gitops/gitops.go`, `internal/gitops/gitops_test.go` (`ShowFile`, `PushExisting`)
- Create: `internal/runner/followup.go`, `internal/runner/followup_test.go`
- Modify: `internal/runner/runner.go` (`bootstrap`, `agentLoop`, `finalize`, `ensurePR`, `clearStateDir`, `refreshGitAuth`), `internal/runner/report.go` (the follow-up section), `internal/runner/prompts.go` (`SystemPrompt` takes the follow-up lines), `internal/runner/runner_test.go` (drop the "follow-up" case of `TestBootstrapRejections`), `internal/runner/lockcache.go` (restore caches and read repo files after the base-config read, unchanged otherwise)

**Interfaces:**
- Consumes: T1, T2 (`config.Followup`), T5, T6.
- Produces:
  - `gitops.(*Repo).ShowFile(ctx, rev, path string) ([]byte, error)`: `git show <rev>:<path>`, the path checked to be relative, clean and without `..`.
  - `gitops.(*Repo).PushExisting(ctx, branch string) error`: `Push`, but refusing when the remote branch is absent ("fugaro/<id> no longer exists on origin; not recreating it").
  - `run.follow *followState` (nil for a first run): the trust, the selection, the nonce, the `restored` session, the start SHA, the PR info, and whether the PR was ready at start.
  - `pushed_head` set after every successful push (first runs too).

**Bootstrap, for a follow-up** (each failure is an `infra_error` that changes nothing on the provider):
1. Read the task, claim the record, check for a cancel, as today. Open the provider when the origin names it, as today.
2. Check out `spec.Branch` with `repo.CheckoutNewBranch(ctx, spec.Branch, spec.Branch)`; a missing remote branch → "branch fugaro/<id> no longer exists on origin; the PR was merged or its branch deleted". `r.rec.Branch = spec.Branch`. `StartSHA` = HEAD.
3. Fetch `origin/<spec.Ref>` (`repo.FetchBase(ctx, spec.Ref)`), then read `fugaro.yaml` with `ShowFile(ctx, "origin/"+spec.Ref, "fugaro.yaml")`, not from the tree. Its `git.base_branch` must equal `spec.Ref` (else "fugaro.yaml on <ref> names base <b>"). Select the workflow, apply the overrides, set `deadline`, as today. `agent.instructions` and a file-valued `agent.review` are read the same way from `origin/<ref>`. (What the configured commands *invoke*, such as `package.json` scripts, is the branch's code, as in any run: the base pins the config, not the repository's scripts.)
4. Lock, as today (the key is the branch, so it serializes every run on the PR). Open the provider if not open yet, check its kind.
5. `Repository()`: public without `followup.allow_public` → refuse (R6).
6. `PullRequest(spec.PR)`: must be `PROpen` ("PR #N is merged"), `SourceBranch == spec.Branch`, `SourceRepo` equal to `spec.Repo` ignoring case, and `SameCommit(HeadSHA, StartSHA)` (else "PR #N's head moved during bootstrap; launch again"). Set `r.rec.PR` and `r.rec.FollowUp{PR, PreviousRun, StartSHA}`, and save.
7. Restore caches, clear the state directory (now also `followup.md`), write the verify settings, and build the agent's environment, as today, except that for a follow-up **no git credential variables and no `GH_TOKEN`** go into it (R6 finding). `refreshGitAuth` then updates only the runner's git environment for a follow-up. Every secret is registered with the redactor here.
8. `Comments(spec.PR)`: an error → "reading PR #N's comments: …" (redacted). Stale check with `followup.FugaroRuns` (R7). `since` (R2) from `d.Store.Sibling(previous_run)`: its `comments.json` `fetched_at` when it was a follow-up, else its record's `started_at`; an unreadable previous record → "previous run <id> has no readable record". `Select` with `NewTrust(cfg.Followup, pr)` and the run's redactor. Store `comments.json`; a failed upload is a warning. The previous run's `followup.md` object, read with a cap, becomes `PreviousAnswer`. The log gets only `comments`, `omitted`, `authors`, `untrusted_authors` counts and `since`.
9. `restoreSession(ctx, d.Store.Sibling(previous_run), StartSHA)`. For a fresh session, read the root run's `task.json` (`BranchRunID(spec.Branch)`; absent → no root task) and `git diff --stat origin/<base>...HEAD`.

**Agent loop:** `implement` is `followup.ImplementPrompt` with `Resume: restored.Resumed` and that ID, else a new ID. If a resumed implement fails with `agent.ErrNoSession`, run implement once more fresh (fresh prompt) and record `session: fresh`, note "the saved session could not be resumed"; any other error fails the stage as today. Review prompts get `followup.ReviewAddendum`. Fix resumes the implement session, as today. `review_rounds` is the base config's, or the task's override.

**Finalize, for a follow-up** (R4): no empty commit; `PullRequest` before the push (not open → no push, `failed`/`none`, no comment); `PushExisting` (someone else's push → the reason and a short marked note on the PR; an absent branch → `failed`/`none`, no comment); `pushed_head`; `ensurePR(PRSpec{Number: spec.PR, Branch, Base, Draft: !ready})`, where `ensurePR` returns at once on `ErrPRNotOpen` and finalize records `failed`/`none` with no comment before its generic error branch; the report, with the marker, the follow-up section (previous run; session and note; the authors used; the untrusted authors dropped; `QuoteAnswer(followup.md)`; "was ready; moved back to draft because …" when that happened), after the dedupe listing of R4 step 7. `followup.md` (as quoted) is stored in the run prefix.

- [ ] **Step 1: Write the failing tests** (the harness with the fake provider and the scripted agent; a helper `followUpHarness(t)` runs a first run, then builds the follow-up spec against the same bucket and remote; its base `fugaro.yaml` trusts the injected commenter's ID).
  - `internal/gitops`: `TestShowFile`, `TestShowFileRefusesTraversal`, `TestPushExistingRefusesAbsentBranch`.
  - `TestFollowUpUpdatesSamePR`: a first run opens PR 1 as a draft; a trusted comment is injected; the follow-up commits, verifies and ships; PR 1 is now ready, still one PR, two reports, the second with the follow-up section and the marker.
  - `TestFollowUpBootstrapRefusalsTouchNothing` (table): public repository; PR merged; PR closed; source branch differs; source repository differs (fork); head moved; stale; `Comments` fails; the previous run's record is missing; base names another base branch. Each: `infra_error`, the reason names the cause, and the fake's state file is byte-identical before and after.
  - `TestFollowUpRefusesPublicRepo`, `TestFollowUpAllowPublic`.
  - `TestFollowUpConfigFromBase` (the branch's `fugaro.yaml` sets `commands.test: "true"`, a larger budget and another instructions file; the verify settings, the budget and the system prompt come from the base).
  - `TestFollowUpTrustFromBaseNotBranch`.
  - `TestFollowUpReportNamesAuthors` (two trusted authors with counts; one untrusted author's name and the count).
  - `TestFollowUpNoTrustedComments` (only untrusted comments: the prompt says so; the run proceeds on the TEXT).
  - `TestFollowUpAgentEnvHasNoGitToken` (the scripted agent's env has neither the credential variables nor `GH_TOKEN`; the runner's push still works over the HTTP remote with credentials).
  - `TestFollowUpBranchGone`.
  - `TestFollowUpAfterUnpostedReport` (the previous follow-up pushed, then its report failed: this follow-up is not stale), `TestFollowUpAfterGiveUpNote` (the previous follow-up pushed and posted a not-ready note, recorded `infra_error`: it is `previous_run`, and this follow-up proceeds), `TestFollowUpStaleWhenNewerRunPosted`, `TestFollowUpForgedMarkerIgnored`.
  - `TestFollowUpPromptExcludesReports`.
  - `TestFollowUpReportStripsForgedMarker` (the agent writes a marker and a `### Fugaro run` line into `followup.md`: the posted report carries only this run's marker).
  - `TestFollowUpCommentsRedactedWithWorkflowSecrets` (a local run without `FUGARO_SECRET_ENVS`: a workflow secret in a comment body is redacted in `comments.json` and the prompt).
  - `TestFollowUpNeverLogsCommentBodies` (a comment body holding a canary and the planted secret: neither is in any log line the runner writes, filtering out `stream: agent` relay entries; the secret is in no bucket object; `comments.json` has the canary).
  - `TestFollowUpResumesSession`, `TestFollowUpFreshWithoutSession`, `TestFollowUpResumeFailureFallsBackFresh`, `TestFollowUpResumeOtherErrorFailsStage`.
  - `TestFollowUpReviewSeesComments`.
  - `TestFollowUpNoNewCommits` (no empty commit; "no new commits"; the outcome follows §4.2).
  - `TestFollowUpReadyToDraftSaysSo`.
  - `TestFollowUpPRMergedDuringRunNoPush` (the remote's tip unchanged, `failed`/`none`, no comment).
  - `TestFollowUpPRClosedDuringFinalizeNoNote` (`EnsurePR` returns `ErrPRNotOpen`: one attempt, no note, no report, `failed`/`none`, `pushed_head` set).
  - `TestFollowUpPushRefusedWhenBranchDeleted` (the remote branch deleted during implement: no push, no branch recreated).
  - `TestFollowUpSomeonePushed` (a commit pushed to the branch during implement: nothing overwritten, the reason says so, one marked note on the PR).
  - `TestFollowUpCancelled` (the PR goes draft, the report says cancelled, the lock is released).
  - `TestFollowUpCostIsOwnStagesOnly`, `TestFollowUpRecordHasPRFromBootstrap`, `TestPushedHeadOnFirstRun`, `TestReportDedupeOnlyForFollowUps` (a first run makes no `Comments` call; a follow-up whose report is already there doesn't post again).
- [ ] **Step 2:** `go test ./internal/gitops/ ./internal/runner/ -run 'ShowFile|PushExisting|FollowUp|PushedHead|ReportDedupe'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Keep the first-run path as it is apart from `pushed_head`: every existing runner test must pass unchanged.
- [ ] **Step 4:** `go test -race ./internal/gitops/ ./internal/runner/ ./internal/e2e/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "runner: follow-up runs update their PR from trusted comments, with the base branch's config"`

---

### Task 8: Follow-ups in `ls` and `diagnose`, and `ls --pr`

**Files:**
- Modify: `internal/runview/runview.go` and its test
- Modify: `internal/cli/ls.go`, `internal/cli/ls_test.go`, `internal/cli/diagnose.go`, `internal/cli/diagnose_test.go`, `internal/cli/cancel_test.go` (a test only)

**Interfaces:**
- Produces:
  - `runview.Row` gains `Branch string \`json:"branch,omitempty"\``, `Outcome string \`json:"outcome,omitempty"\``, `TaskPR int \`json:"task_pr,omitempty"\``, `RecordPR int \`json:"record_pr,omitempty"\``, `PR int \`json:"pr,omitempty"\`` (the record's, else the task's), `Pushed bool \`json:"pushed"\``, `StartedAt *time.Time \`json:"started_at,omitempty"\``, `PreviousRun string \`json:"previous_run,omitempty"\``, `FollowUp bool \`json:"follow_up"\``.
  - `lsFilter.pr int`: `loadRows` reads each run's `task.json` and `result.json` first, keeps only runs with `TaskPR == pr` or `RecordPR == pr`, and only then joins executions, so `--pr` over 90 days makes no per-run backend call for runs off the PR. An `error` row whose task says `pr: N` is kept (R1 refuses on it).
  - `lsOptions.pr` from `--pr N`, which needs exactly one repository (`--repo`, or the local config's single one; else exit 1 "ls --pr needs --repo"). With `--pr`, `--since` defaults to `90d`.
  - The human table's PR column prints `#N <url>`, or `#N` while the URL is unknown.
  - `diagnose --json` gains `follow_up` (the record's `FollowUp`, else `{pr, previous_run}` from the task) and `comments_path` (`<prefix>comments.json`, for a follow-up); the human output gains one line: `follow-up of PR #N after <previous run>; session resumed|fresh (<note>); N comments from <authors> (M untrusted: <names>)`.

- [ ] **Step 1: Write the failing tests.** `TestJoinFollowUpFields` (record, task-only, first run; `Branch`, `Outcome`, `TaskPR`, `RecordPR`, `Pushed`), `TestLsPRFilter`, `TestLsPRJoinsExecutionsOnlyForPR` (the Run fake counts `Execution` calls: none for runs off the PR), `TestLsPRNeedsOneRepo`, `TestLsPRTotals`, `TestLsPRColumn`, `TestDiagnoseFollowUp` (JSON and text), `TestCancelFollowUp`.
- [ ] **Step 2:** `go test ./internal/runview/ ./internal/cli/ -run 'FollowUp|LsPR'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./internal/runview/ ./internal/cli/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "ls, diagnose: show follow-ups, and list a PR's runs with ls --pr"`

---

### Task 9: `fugaro run --pr N`, its checks, and the hermetic cloud follow-up

**Files:**
- Modify: `internal/cli/run.go`, `internal/cli/run_test.go` (replace `TestRunPRPointsToM6`); Create: `internal/cli/followup.go`, `internal/cli/followup_test.go`
- Modify: `internal/e2e/cloud_test.go`:
  - a rig option, `fixedWorkdir`, that runs executions one after another in the same `--workdir` path (`<rig dir>/work`, emptied before each execution), mirroring `/work/repo` on Cloud Run, where every execution starts from the image at the same path. Without it restore refuses "another workdir" and the test could never see a resumed session. Each execution keeps its own `HOME`, as now.
  - a helper that injects a comment, with an author ID, into the fake provider's state file, and one that sets the base `fugaro.yaml`'s `followup.trusted`.
  - `TestCloudFollowUpOnePR`.

**Interfaces:**
- Consumes: T8's `loadRows` with `lsFilter{slugs, since, pr}` and the row fields; T1's `task.BranchRunID`, `lock.Key`.
- Produces:

```go
// followup.go
const followUpLookback = 90 * 24 * time.Hour // the runs/ lifecycle age (design §3.3)

type prChain struct {
	Branch   string
	Previous runview.Row   // newest (started_at) run on the PR with Pushed
	Runs     []runview.Row // every run on the PR, newest first
}
// resolvePR applies ruling R1; exclude is a run ID to leave out (a retry, or a
// repeated --run-id). Every refusal is a userErr naming the run and what to do.
func resolvePR(ctx context.Context, env *cloudEnv, slug string, pr int, exclude string, warn io.Writer) (*prChain, error)
func followUpSpec(ctx context.Context, env *cloudEnv, o *runOptions, slug string, c *prChain, text string, total time.Duration) (*task.Spec, error)

// run.go
// launchResult gains PR int `json:"pr,omitempty"` and PreviousRun string `json:"previous_run,omitempty"`;
// Branch is spec.Branch for a follow-up. taskText accepts empty text when --pr is set.
```

- `resolvePR` reads `previous_run`'s `task.json` for `ref` and `workflow` (its `Workflow`, else the row's). It reads the branch lock (`lock.Key(slug, branch)`) and refuses a live one ("branch busy: run X holds it until T"); an expired or unreadable lock is fine, since the runner takes it over.
- The human output: `launched <slug>/<id> (follow-up of PR #N, after <previous run>)`, then `branch fugaro/<root id>` and the logs line.
- `--pr` is unhidden; its help says "continue Fugaro PR N: act on its trusted review comments (TEXT adds instructions)".

- [ ] **Step 1: Write the failing tests** (`newCloudFixture`, a `file://` bucket seeded with run objects, the Run fake).
  - `TestRunPRLaunchesFollowUp` (`task.json` has `branch`, `pr: 7`, `previous_run`, the previous run's `ref` and `workflow`, `requested_by`; the JSON has `pr`, `previous_run` and the PR's branch), `TestRunPRPrintsPRBranch`.
  - `TestRunPRNotAFugaroPR`, `TestRunPRRefusesActiveRun`, `TestRunPRRefusesErrorRow`, `TestRunPRRefusesLiveLock`, `TestRunPRBranchDisagreement`, `TestRunPRRootBranchMustNameRoot`, `TestRunPRRootExpired` (only follow-ups remain and agree: accepted), `TestRunPRNeverPushed` (refused), `TestRunPRPreviousIsNewestPushed` (an `infra_error` run that pushed is chosen over an older `failed` one; a newer run that never pushed is skipped; ordering follows `started_at`, not a chosen run ID), `TestRunPRWarnsUnlaunched`.
  - `TestRunPRFlagConflicts` (`--ref`, `--workflow`, `--retry` with `--pr`; `--pr 0`).
  - `TestRunPRTextOptional`, `TestRunPREmptyTaskFileStdin`, `TestRunPRTaskFile`, `TestRunPRBatch`.
  - `TestRunPRTotalTimeout` (stored, and the Run fake saw `overrides.timeout` of it plus the slack).
  - `TestRunPRRepeatRunIDIsIdempotent` (the same `--run-id` twice → `already-launched`, one execution, the stored spec unchanged; a different TEXT → exit 1).
  - `TestRetryFollowUp`, `TestRetryFollowUpRefusedWhenSuperseded`.
  - `TestRunPRMaxParallel`.
  - `internal/e2e`: `TestCloudFollowUpOnePR` (with `fixedWorkdir`): the base `fugaro.yaml` trusts one author ID; `run` → the first run finishes with PR 1; the rig injects an unresolved inline comment and a general comment by that author, and one comment by an untrusted author; `run --pr 1 "also rename x"` → the follow-up finishes; the fake's state has **one** PR with two reports, the second naming the follow-up and both authors' status; the follow-up's `comments.json` has the two trusted comments and neither the first report nor the untrusted comment; `session/` of both runs exists and the follow-up's `result.json` says `session: resumed`; `ls --pr 1 --json` has two rows with totals; `diagnose --json` of the follow-up has `follow_up`; the rig's secret scan covers `comments.json`, `followup.md` and `session/`.
- [ ] **Step 2:** `go test ./internal/cli/ -run 'RunPR|RetryFollowUp'` and `go test ./internal/e2e/ -run CloudFollowUp`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "run: --pr N launches a follow-up of a Fugaro PR, found through the runs bucket"`

---

### Task 10: The `fugaro:followup` skill, the docs, and the live test code

**Files:**
- Create: `plugin/skills/followup/SKILL.md`; Modify: `plugin/.claude-plugin/plugin.json` (description)
- Modify: `docs/design/v1.md`, `docs/git-providers.md`, `docs/gcp-live-checklist.md`, `README.md` (the follow-up line, if it lists commands)
- Modify: `internal/e2e/live_gcp_test.go` (`TestLiveSandboxRun` asserts `session/` before its cleanup; new `TestLiveSandboxFollowUp`; the sweep covers follow-ups)
- Modify: `deploy/sandbox/fugaro.yaml` (a commented `followup:` block showing where the sandbox's trusted ID goes; the real ID is committed only to the sandbox repository, T11 step 5)

**The skill** (`fugaro:followup`), in the onboard skill's style:
- **When:** the user wants Fugaro to address review comments on a PR Fugaro opened, or to continue it with more instructions.
- **Trust first:** only comments by the account IDs in `followup.trusted` of the base branch's `fugaro.yaml` reach the remote agent. If the user expects someone's comments to count, check that list; adding an ID is a normal PR to the base branch. On a public repository the base must also set `followup.allow_public: true`.
- **Steps:** find the PR number (from the conversation, or `fugaro ls --pr N --repo <owner/name> --json` to confirm it is a Fugaro PR and none of its runs is active); write the extra instructions only for what isn't already in the PR's trusted comments, self-contained, with acceptance criteria, or pass none; choose a run ID (`date -u +%Y%m%d-%H%M%S` plus 4 hex) so a retry can't launch twice; run `fugaro run --pr N --repo <owner/name> --run-id <id> --json`, adding `--task-file -` only when there are instructions; report the run, the branch, and how to watch it (`fugaro ls --pr N --repo <owner/name>`).
- **Refusals:** an active run → wait, or `fugaro cancel`; "not a Fugaro PR" → a new run; a live lock → wait until its expiry; a launch whose outcome is unknown → `fugaro run --retry <run>`; a follow-up that ended `infra_error` → `fugaro diagnose <run>` (public repository, closed PR, stale).
- **Never:** paste secrets or tokens into the instructions; tell the remote agent to ignore reviewers; widen `followup.trusted` without the user asking.

**`docs/design/v1.md`:**
- **§3.3:** `comments.json`, `followup.md`, `session/session.json` and `session/<id>.jsonl`; every run from M6 on saves its session.
- **§4.1:** bootstrap for a follow-up (the branch; the config, instructions and review prompt from `origin/<ref>`; visibility; the PR checks; the agent env without git credentials; the comments and the stale check; the session); finalize for a follow-up (no empty commit, no push to a closed PR or an absent branch, update by number, `ErrPRNotOpen` without a comment, the dedupe, the "someone pushed" note); `pushed_head` for every run.
- **§4.4:** rewrite to rulings R1–R7, recording the conflict with the old step 1.
- **§4.6:** `follow_up` and `pushed_head`; `pr` set at bootstrap for a follow-up; a follow-up whose PR closed is `failed` with outcome `none`.
- **§5.1:** the `followup:` block and its validation. **§5.3:** `branch` is `fugaro/<run-id>`; `ref` and `workflow` come from the previous run; the task text is the launcher's extra instructions.
- **§6.1:** the trust boundary moves: follow-ups extend the agent's instruction set from repository writers to the trusted-commenter set of the base branch's `followup.trusted` (and, on GitHub, collaborators); the delimiter is not a boundary; GitHub's `COLLABORATOR`/`MEMBER` breadth and private members shown as `CONTRIBUTOR`; public repositories refused unless opted in; a follow-up's config comes from the base; the agent's environment in a follow-up has no git token (defense in depth only, given `/proc` and the metadata server); session files in the bucket are redacted like transcripts; the symlink rule for the session file.
- **§9.1:** the `run --pr` row (flags allowed and refused, output fields), `ls --pr` and the new row fields, `diagnose`'s `follow_up`.
- **§9.2:** the `fugaro:followup` row.
- **§13:** the follow-up unit, adapter and cloud tests. **§14:** M6 delivered, with a pointer to this plan; what it deferred. **§15:** "Resolved in M6": PR resolution without provider credentials in the CLI; the trust allowlist.

**`docs/git-providers.md`:** the new calls per provider and the permissions they use (unchanged scopes); finding an account ID for `followup.trusted` (GitHub: `gh api users/<login> --jq .id`; Bitbucket: the `account_id` shown by `TestLiveInspect` for a comment, or the Bitbucket API's user object); the identity lookups and their fallbacks; the live test's new steps.

**`docs/gcp-live-checklist.md`:** check 19 (below), including the pause for the user's comment; a "Results of the fourth live run (M6)" section.

**`TestLiveSandboxFollowUp`** (build tag `live`), with the same guardrails and caps as `TestLiveSandboxRun`, and a `-timeout` of 75m:
1. Launch a first run (`--total-timeout 15m`, batch `live-<stamp>`) and wait for its PR. Register the cleanup first, as check 13 does.
2. **Pause for the user.** Log `ACTION: post a review comment (one inline, one general) on <PR URL> in the Bitbucket UI now`, then poll the PR's comments with the repository token (read only) every 20 seconds, for up to `FUGARO_LIVE_COMMENT_WAIT` (default 20m), until a comment appears whose author is not the token's user. Log its author's `account_id` as a `FACT`. Time out with a clear failure (cleanup still runs).
3. Read the sandbox's base `fugaro.yaml` (as `checkCaps` does) and require that author's ID in `followup.trusted`; otherwise fail with "add <id> to followup.trusted on master (T11 step 5), then rerun". The test never edits the repository.
4. `fugaro run --pr <n> --batch <same> --json "Also add a line to the README saying the follow-up ran."` → `launched`, same branch, `pr` and `previous_run` set.
5. Wait for the follow-up; then assert: exactly **one** open PR from the branch; the PR has two reports, each with its run's marker, the second naming the user as a trusted author; the follow-up's `comments.json` holds the user's comments and neither report; `result.json` `follow_up.session` (a `FACT`; `resumed` expected); `ls --pr <n> --json` two rows and totals; the branch lock is gone.
6. Cleanup as for check 13: decline the PR, delete the branch, delete both runs' objects. The sweep already covers the batch.

- [ ] **Step 1: Make the edits.** Check every `§` reference this plan cites.
- [ ] **Step 2:** `go test ./internal/cli/ -run TestSkillCommandsExist`, `go test ./plugin/`, `go vet -tags live ./internal/e2e/`, `go test ./internal/config/ -run Example`.
- [ ] **Step 3: Commit.** `git commit -m "docs, plugin: follow-up runs, their trust rule, the fugaro:followup skill, and the live follow-up check"`

---

### Task 11: Live verification on the sandbox (controller-run, with user confirmations)

This task writes no code. The controller runs it one step at a time and asks the user before every **⚠ CONFIRM** step. Each step is its own question: one approval never covers the next. Record every outcome and `FACT` in the M6 PR. Anything that fails goes back to its task as a bug, with a hermetic test first. **The sandbox repository only**: never the web repository. **No second credential**: the only provider credential the tests hold is the sandbox's repository access token, as in M5.

**Preconditions (read-only, no confirmation needed):**
- The M5 preconditions of `gcp-live-checklist.md` hold: `FUGARO_LIVE_PROJECT`, `FUGARO_LIVE_REPO=acme/sandbox`, the local config naming both, ADC, `FUGARO` built from `m6`.
- `fugaro ls --since 1d` shows no active run.
- The sandbox repository is private (the follow-up refuses a public one; the sandbox's `fugaro.yaml` does not set `allow_public`).

**Steps:**
1. **⚠ CONFIRM** Rebuild the sandbox's image from `m6`, so its `fugaro` has the follow-up runner: build and push the base to `fugaro-base` (`images/build-base.sh web-node <region>-docker.pkg.dev/<project>/fugaro-base/fugaro-web-node:dev-<commit>`, `docker push`), then **⚠ CONFIRM** `fugaro init --base-image <that tag> --yes`, then **⚠ CONFIRM** `fugaro image build` in the sandbox checkout (about $0.05).
2. **⚠ CONFIRM** The Bitbucket adapter's live test with recording: `FUGARO_LIVE_RECORD_DIR=… go test -tags live -run TestLiveBitbucket ./internal/gitprov/bitbucket/`. It opens, comments on, reads and declines a PR in the sandbox. Record the `FACT`s of the "Unverified assumptions" list that it answers. Commit the recorded fixtures (scrubbed as the recorder does) in a follow-up commit, replacing T4's hand-written ones where they differ, with the tests updated first.
3. **⚠ CONFIRM** `TestLiveSandboxRun` (check 13), as the regression check for first runs; it now also asserts `session/` and `pushed_head` before its cleanup.
4. Find the user's Bitbucket `account_id`: the user posts any comment on a sandbox PR the controller names (or reads it from their Bitbucket profile), and **⚠ CONFIRM** `go test -tags live -run TestLiveInspect ./internal/gitprov/bitbucket/` with `FUGARO_LIVE_INSPECT_PRS` set prints it (read-only).
5. **⚠ CONFIRM** Commit `followup: {trusted: [<the user's account_id>]}` to the sandbox's `fugaro.yaml` on `master` (a push to a real repository; the ID is not a secret, but it stays out of this repository).
6. **⚠ CONFIRM** `TestLiveSandboxFollowUp` (check 19): two sandbox runs (about 2 × the sandbox's cap). When the log prints `ACTION: post a review comment…`, the controller **stops and asks the user** to post one inline and one general comment on that PR in the Bitbucket UI, and says so in one line; the test resumes on its own once the comment appears. The controller never posts the comment and holds no user credential.
7. Read-only, from step 6's log and objects (before its cleanup, via the `FACT`s it logs):
   - `session: resumed` (else the note says why; "no conversation found" means `agent.SessionDir`'s escaping or the resume assumption is wrong: stop and fix T6 with a test first);
   - the second report's marker and how Bitbucket renders it (hidden, or a visible last line); the report names the user as a trusted author;
   - the report's cost line is the follow-up's own cost;
   - `diagnose --json` of the follow-up.
8. **⚠ CONFIRM** A manual refusal check: `fugaro run --pr <a PR declined by an earlier step>` → the CLI launches (it can't see the state) and the runner ends `infra_error` "PR #N is closed" with the PR untouched; or, if its runs have expired, the CLI refuses. Record which.
9. **⚠ CONFIRM** The sweep, `TestLiveGCPCleanup`, if anything was left.
10. **⚠ CONFIRM**, optional: remove the `followup:` block from the sandbox's `fugaro.yaml` again, if the user prefers the sandbox not to trust anyone between live runs.
11. Fill in "Results of the fourth live run (M6)" in `gcp-live-checklist.md` with the `FACT`s, in the M6 PR.

**Rollback:** nothing in M6 changes infrastructure. To go back, point `base_image` at the M5 dev tag (**⚠ CONFIRM** `fugaro init --base-image <M5 tag> --yes`) and rebuild the sandbox image (**⚠ CONFIRM**). A follow-up launched by an M6 CLI against an M5 image fails at bootstrap with "follow-up runs are not supported by this version of fugaro", touching nothing. An M5 `fugaro validate` refuses the new `followup:` block (strict decoding), so revert step 5 before rolling a checkout's tooling back.

---

## Decisions recorded (formerly open)

- **Trust** (user, binding): R6. Formerly Open question 3.
- **The live comment** (user, binding): posted by hand by the user while the controller pauses; no second credential.
- **Resolving the PR without the provider:** R1 (the bucket, verified by the runner). A PR whose runs are older than 90 days can't be followed up.
- **The PR's title and body on a follow-up:** untouched; what changed goes into the report from `followup.md`.
- **Which comments:** R2 (unresolved threads of any age; review and general comments since the previous follow-up's fetch; newest kept over the bounds).
- **Overrides:** nothing is inherited from the previous run.
- **A cancelled or failed follow-up on a ready PR:** it becomes a draft (design §4.2, §4.5), and the report says it was ready and why it moved back.
- **A comment-read failure at bootstrap:** `infra_error`, the PR untouched, even when the launcher gave TEXT.
- **A PR merged or closed during the run:** no push (or no comment after a push), `failed` with outcome `none`, documented in §4.6.
- **The follow-up agent's environment:** no git credentials and no `GH_TOKEN` (R6 finding).

## Open questions for the user

Each has a recommended default, which the plan implements unless the user rules otherwise.

1. **"The PR's own author" is Fugaro itself.** On a PR Fugaro opened, the provider's PR author is Fugaro's identity (the App's bot or the repository token's user), whose comments are dropped as Fugaro's own. So `trust_pr_author: true` adds nobody in practice, and `followup.trusted` is the whole rule. *Default: keep the field as decided (default `true`) and document this plainly.* An alternative would trust the launcher, but `requested_by` is an email, not an account ID, so it would need a mapping in `fugaro.yaml`.
2. **ID forms in `followup.trusted`.** *Default:* GitHub numeric user IDs (a login can be renamed and then reused by someone else); Bitbucket `account_id` or a UUID. Logins and display names are refused with a hint.
3. **Dropping the git token from first runs too.** A first run's agent keeps the git credentials and `GH_TOKEN` today (§6.2: it may push and edit the PR). *Default: M6 drops them for follow-ups only*; dropping them everywhere is a separate change with its own live check.
4. **GitHub has no live check in M6.** The GitHub reads are tested against documented shapes only. *Default: ship it so*, and add a GitHub live check when a GitHub sandbox and App exist.

## Unverified assumptions to check live

Each is either answered by a `FACT` in T11 or, where no live check covers it, cited from vendor documentation in the task that relies on it:

1. Claude Code names a project directory by replacing every non-alphanumeric byte of the working directory with `-` (T6; T11 step 7).
2. `claude -p --resume <id>` keeps the session ID rather than forking a new one (T6 saves the result event's ID either way; T11 step 7 records which).
3. A missing session makes `claude` print "No conversation found with session ID" (T6's `ErrNoSession`; T11 step 7).
4. Bitbucket PR comment fields: `resolution`, `inline.outdated`, `deleted`, `user.account_id`; the PR's `author.account_id`; the repository's `is_private` (T4; T11 step 2).
5. Whether `GET /user` accepts a Bitbucket repository access token (T4's `Self`; T11 step 2).
6. How Bitbucket renders `<!-- … -->` in a raw comment (R7; T11 step 7).
7. GitHub GraphQL shapes: `reviewThreads{isResolved isOutdated path line}`, `comments{pageInfo}`, `authorAssociation`, `author{__typename … on User{databaseId}}`, and a bot's login without `[bot]` (T3; documentation only, Open question 4).
8. GitHub `author_association` for organization members whose membership is private (shown as `CONTRIBUTOR` without `members: read`; T3, §6.1; documentation only).
9. GitHub's convert-to-draft keeps requested reviewers (R4 step 6; documentation only).
10. Whether users with read access can edit a Bitbucket PR's reviewers: no longer load-bearing under the allowlist, recorded for §6.1 if T11 happens to show it.

## Conflicts between the design and the code (resolved by this plan)

- **§4.4 step 1** has the CLI resolve the PR through the provider; the CLI holds no provider credential (§6.1). Resolved by R1; §4.4 is rewritten in T10.
- **§3.3 lists `session/`,** but no code writes it, and the runner uses a new session ID each run. T6 saves every run's session from M6 on; PRs from before M6 always start fresh.
- **`EnsurePR` finds only open PRs by branch and otherwise creates one** (both adapters), so a follow-up through it could open a second PR on a merged one. Resolved by `PRSpec.Number` (T1, T3, T4) and R4.
- **`gitops.Push` pushes to an absent remote branch,** which would recreate a branch Bitbucket deleted at merge (`close_source_branch: true`). Resolved by `PushExisting` for follow-ups (T7).
- **`ensurePR` retries every plain error, and finalize comments on any PR it got back,** which would post on a closed PR. Resolved by the `ErrPRNotOpen` short-circuit (T7).
- **Finalize adds an empty commit** when the branch has no commits ahead of base, and takes the PR text from `pr.md` or the task; neither fits a follow-up. Resolved in T7.
- **The runner reads `fugaro.yaml` and the files it names from the checked-out tree;** for a follow-up that is the PR branch, which an earlier agent controls. Resolved by reading from `origin/<ref>` (T7).
- **`launchResult.Branch` is `fugaro/<run-id>`** for every launch. Resolved in T9.
- **`rec.PR` is set only at finalize,** so `ls` would show no PR for a running follow-up; and no record says whether a run pushed. `pr` at bootstrap and `pushed_head` (T1, T7).
- **§9.1 calls `--pr` a hidden M4 flag** and lists no flag rules for it; T10 documents R8.
- **§5.3's example and the task corpus's `valid/followup.json`** set a follow-up's `ref` to its own branch, while the runner reads `cfg.Git.BaseBranch` for the base anyway. R3 makes `ref` the base branch; T1 and T10 align the fixture and the example.

## Review disposition (2026-09-30 review)

- **C1, I8:** replaced by the user's binding decisions (R6; T10/T11 without a second credential).
- **I1–I7:** fixed (R1 and R7 with `pushed_head`; R2's `since`; R7's markers; T7's base-branch config; T8's row fields; T9's fixed workdir; R4 and T7's `ErrPRNotOpen` and `PushExisting`).
- **Minor, applied:** M1 (T3 `DoPage` with the host pin, T4's `next` pin), M2, M3, M4 (bootstrap step 7 before 8), M5 (dedupe only for follow-ups; the session saved on a finalize error), M6 (newest kept; truncated threads flagged; Bitbucket tasks and review states out of scope; the previous `followup.md` in the prompt), M7, M8, M9, M10, M11 (no-`HOME` skip; comment times in the fake), M12, M13 (the report's wording; the GitHub reviewer question under Unverified assumptions), M14, M15 (T8's pre-filter), M16 (the reason and a short note on the PR).
- **Minor, rejected:** none. One part of M5 is kept on purpose: the report dedupe stays for follow-ups, where the PR is the one people are watching and the listing reuses `Comments`; it is dropped for first runs, as the reviewer suggested.

## After M6

- **M7:** the `launch`, `status`, `logs` and `diagnose` skills (`fugaro:diagnose` suggests `fugaro:followup` for a draft PR with trusted review comments), docs and the v0.1.0 release.
- **Later:** replying to each review thread and resolving it when addressed; a follow-up trigger (a PR label or a comment command by a trusted author); a GitHub live check; dropping the git token from first runs' agents (Open question 3).

## Execution

Subagent-driven is recommended, with a fresh reviewer per task. The tasks are mostly sequential, and T1's contracts, T5's trust rule, T7's bootstrap order and R4's finalize are where a shipped mistake lets a commenter steer an agent that holds secrets, opens a second PR on a real repository, or feeds Fugaro's own report back to an agent; T1, T5, T7 and T9 deserve the closest review. Task 11 is controller-only: it changes a real repository and spends money, the user confirms every ⚠ step, and the user alone posts the review comment.
