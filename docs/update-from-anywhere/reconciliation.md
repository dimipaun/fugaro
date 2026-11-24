# Update from anywhere: reconciliation

Checked against `origin/main` at `486823a00` on 2026-11-24 (the spec's own claims were checked at
`b8fdb53e2`; of the files it cites, only `init_stages.go` and `update_backlog.go` changed since, and
neither change touches a claim). **Where this file disagrees with [spec.md](spec.md), this file
wins.** One capability (no capability map): every part serves the same rule, and none ships alone.

## Assumptions

- **A1.** "Every enrolled repository" is the list that L8's reader draws (F1): the local config's
  repositories plus the concrete owner/name entries of the published policy's admits. A pattern
  admit is named and never expanded, as in L8's D3306.
- **A2.** "The default branch" is the branch the code host names as default, read through the
  host's API. It is not a local ref: today's `defaultBranchParse` reads the checkout's
  `origin/HEAD`, which is a local copy (F11).
- **A3.** "The hint" is a project picked from the working directory's `fugaro.yaml` when
  `--project` is absent, plus one reordering of human output. It changes no `--json` byte, no event
  and no write.
- **A4.** L5 adds no new cloud write. Each per-repository write is one the per-checkout run already
  makes. `docs/design/update-in-flight.md`'s analysis therefore holds per repository.
- **A5.** `--ask` keeps the meaning `docs/no-prompt-policy/` gives it: only at the owner's own
  terminal, never in an agent's session, never with `--json`.

## Findings

- **F1 (Exists).** L8's plan has task M1, `repofiles.Read` (`docs/repo-override-rulings/`,
  in flight as #3816). It lists exactly A1's repositories, reads each one's default-branch
  `fugaro.yaml` through `gitprov.ContentsReader` and its `.fugaro/recipes/` through
  `gitprov.TreeReader`, and returns a one-line reason for each repository it could not read. L8's
  open item O1 says the plan that lands second calls the first plan's reader instead of writing its
  own. The spec's "Reading a default branch" describes the same reader. → R1.
- **F2 (Wrong).** `update --check [PATH]` (`init.go`, the `check` flag) promises to "make no cloud
  call" and to read one checkout's own files. The spec drops PATH and gives it "the same
  per-repository read as the converge". But the full list includes the published policy's admits,
  and reading those is a cloud call. → D3349.
- **F3 (Interaction).** Image-secrets (`docs/secrets-brokering/image-secrets/`, D3324, task IS4 in
  flight as #3800) makes every workflow secret a project secret (`fugaro_p_<name>`), and
  `fugaro secrets set NAME` stores into that grammar. Spec §Secrets and acceptance 3 assume a
  `--repo` line for a repository-scoped name, which after IS4 exists only where a name is still
  repository-scoped. → R2.
- **F4 (Interaction).** D3341 gates the `repository` stage's new form on L2. Until then the stage
  is unchanged: `repoEngine(ctx, ".", …)` (`init_repo_stage.go`) behind `resolveRepoTarget`, which
  skips outside a checkout and refuses a working tree that differs from the default branch
  (`init_repo_target.go`). So acceptance 1 (Check 891 with no stage DIFFERS) cannot hold until L2
  lands. → R3.
- **F5 (Gap).** The RC39 facts (`internal/localcfg/localcfg.go`: `vertex`, `github_app_id`,
  `credentials_service_ready`, `provider_keys_served`, `stage_account_served`) are written by the
  repository engine as it applies a repository's root. D3346 has the repository stage write them
  for every repository, so D3346 lands in the task that D3341 gates on L2. → R4.
- **F6 (Gap).** `backlog.Load` reads a local directory (`update_backlog.go`), and the fold names
  the repository with `checkoutRepo` on that checkout. A fold from a host needs a loader over the
  fetched files, and the repository's name comes from the list, not from an origin remote. → R5.
- **F7 (Exists).** A derived build clones the source inside Cloud Build, using the git token secret
  that the embedded `images/derived/cloudbuild.yaml` declares (`internal/backend/gcp/build.go`
  `embeddedBuild`). `refreshPreflight` (`image_refresh.go`) needs a checkout only to read
  `fugaro.yaml` and the origin, and it refuses a repository that the local config lacks
  (`repoKnown`). So D3342's refresh is possible from a default branch. → R6, U1.
- **F8 (Interaction).** Other plans' in-flight tasks write files L5 writes:
  - `init_stages.go`: L1 T2a (#3766) and L8 M2;
  - `init.go`: L1 T3 (#3778) and T4 (#3786), and L8 DK2;
  - `update_in_flight_test.go`: image-secrets IS5 and L8.

  → R7.
- **F9 (Exists).** `writeAnchor` (`init_fugaroyaml.go`) and the pin's `remove`
  (`init_plugin_stage.go`) print a diff and then gate it: an agent's session is refused; under the
  managed gate the owner is asked nothing; every other session keeps its confirmation. The spec
  moves the anchor to `project repos add` "as `writeAnchor` does now". → R8.
- **F10 (Gap).** The image kinds' union over every repository (`imagesStage.kinds`) only adds. An
  unread repository means a kind may be missing, which is today's state from an empty directory.
  Holding every base image for one unread repository would make the update worse. → R9.
- **F11 (Exists).** `defaultBranchParse` (`init_repo_target.go`, memoized in `checkoutParseCache`)
  reads the checkout's local `origin/HEAD` ref, not the host. Once nothing reads a checkout, it has
  no caller in `update`'s stages, but other commands keep it. → A2, R3.
- **F12 (Wrong).** Acceptance 8's source scan cannot ban every `gitRead(ctx, ".", …)` in
  `internal/cli`:
  - `doctor.go`, `config_show.go`, `recipes.go` and others legitimately read the working
    directory;
  - the hint itself must read it once.

  → R10.
- **F13 (Exists).** Every provider in the tree implements both readers: github (`contents.go`,
  `commits.go`), bitbucket (`contents.go`, `commits.go`) and fake (`fake.go`). L8's U3 (a host
  with neither) is covered by the reader's own unread line.
- **F14 (Interaction).** The HARD RULE's pin sentence is in `CLAUDE.md`, and the `update` skill is
  under `plugin/skills/update/`. Both are paths for the security reviewer (agent instructions), and
  `plugin/` tests check every command and flag a skill names. → R11.

## Decisions

The owner's answers of 2026-11-23 (D3340–D3348) were given on the spec's draft, before this
reconciliation, and D3349 was answered on 2026-11-24. All are as recommended.

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D3340 | The HARD RULE says a leftover plugin pin "is offered its removal as a diff". May L5 reword it? | Reword: a leftover pin becomes a `doctor` notice naming the line to delete, and `update` prints the same line | **As recommended (2026-11-23).** The sentence and the `update` skill change in the same pull request (R11) |
| D3341 | The `repository` stage's new form needs L2's per-repository roots | Ship everything else first; the repository stage's half is a task gated on L2 | **As recommended (2026-11-23).** → R3, R4 |
| D3342 | Open question 1: derived images | Refresh every stale one; each build keeps today's confirmation, and its line names the repository | **As recommended (2026-11-23).** → R6 |
| D3343 | Open question 2: which credential reads the default branches | The shell's token, as `image survey` uses; an unread line names the variable to set | **As recommended (2026-11-23)** |
| D3344 | Open question 3: the backlog fold | Fold from every default branch for one release through `TreeReader`, then delete the step | **As recommended (2026-11-23).** → R5. Its deletion is a follow-up task, one release later (not in this plan's tasks) |
| D3345 | Open question 4: `update --anchor` | Fold it into `project repos add` and remove the flag | **As recommended (2026-11-23).** → R8 |
| D3346 | Open question 5: the RC39 facts | The repository stage writes them for every enrolled repository; moving them to installation state is a follow-up | **As recommended (2026-11-23).** Lands in the L2-gated task (F5, R4) |
| D3347 | Open question 6: `--ask` narrows today's value entry | Accept: values are asked only under `--ask`; otherwise one line names the repository and the fix | **As recommended (2026-11-23)** |
| D3348 | Open question 7: does the hint narrow `--plan-only`? | No | **As recommended (2026-11-23)** |
| D3349 | What does `update --check` read (F2)? | Drop PATH; read every repository in the local config from its code host (a network read, no GCP call); one line says admit-only repositories are covered by `update --plan-only` | **As recommended (2026-11-24).** The flag's help keeps "no cloud call" and adds that it reads the code host |

## Resolutions

- **R1 (F1).** L5 writes no reader of its own. It calls `repofiles.Read`. Its first task, after L8
  M1 merges, extends the reader with an option for extra paths (`.claude/settings.json`, and the
  tree under `docs/backlog/`). L8's results are unchanged when the option is unset, and L8's tests
  pin that. While M1 is open, L5's tasks that read a repository wait for it: an external edge, not
  a contract, because M1 is already running.
- **R2 (F3).** The secrets stage keeps the command its builder makes today (`initsecrets.go`), so
  the stage follows IS4 when IS4 lands. When several repositories want the same project-scoped
  name, that name gets one line listing every repository that wants it. Acceptance 3 reads
  "`fugaro secrets set <name>` (with `--repo <owner/name>` only for a name still
  repository-scoped)".
- **R3 (F4, F11).** Acceptance 1 is split:
  - *Before L2:* Check 891 prints PASS with the `repository` stage as the only stage marked
    DIFFERS. The stage's line names L2.
  - *After the gated task (U8 in the plan):* no stage differs.

  The tree comparison and the cwd read go with that task, not before.
- **R4 (F5).** D3346 lands in the gated task U8. Until then the RC39 facts are written as today.
- **R5 (F6).** `backlog` gains a loader over fetched files (name to bytes) that applies
  `backlog.Load`'s all-or-nothing rule, and the fold takes the repository's name from the list.
  A repository whose host cannot list a tree gets the reader's unread line (F13).
- **R6 (F7).** The checkout stage reads each repository's default-branch `fugaro.yaml` and head
  commit through the reader, then runs today's staleness test and build for each, with today's
  confirmation rule. If a repository can't be built from this machine (U1), it gets one line naming
  it and the reason; `update` never onboards a repository.
- **R7 (F8).** No L5 task waits on another plan's task over a shared file. A task that finds one
  merged first rebases on it, as L8's O2 does. The plan's shared-files table names each file.
- **R8 (F9, D3345).** `project repos add` gets the anchor step with `writeAnchor`'s exact gate:
  the agent refusal, the managed gate, and today's confirmation elsewhere. It adds no new question.
  `update --anchor` and `noteAnchor` go.
- **R9 (F10).** The images stage copies the union of the kinds it read. Each unread repository has
  its line already. Only the default catalog entry, a decision that could be wrong on partial
  information, is held (spec §Reading a default branch).
- **R10 (F12).** The scan covers the functions `update` reaches (its stages and the two steps after
  the stage loop) and allows one function, the hint (`updateHint`), to read the working directory.
  A mutation that adds a `Locate(".")` to a stage fails the scan.
- **R11 (F14, D3340).** The task that turns the pin into a notice also changes `CLAUDE.md`'s HARD
  RULE sentence and `plugin/skills/update/`. That makes its reviewer the security reviewer, and the
  `plugin/` tests run in that task.

## Open tensions

None.

## Open items

- **O1.** L2 (owned by the shared-jobs plan) gates task U8 (D3341). L5 ships without it, and Check
  891's full pass waits for it (R3).
- **O2.** The backlog fold's deletion, one release after L5 ships (D3344), and moving the RC39
  facts to installation state (D3346) are follow-up tasks. Neither is in this plan.

## UNVERIFIED

- **U1.** Whether a repository that only the published admits list (no local-config entry on this
  machine) can be refreshed without that entry. `refreshPreflight` refuses one today (`repoKnown`),
  and the build's region and image names come from the local config. Task U4 verifies it, and a
  repository it cannot build gets a named line (R6).
- **U2.** The spec's cost estimate of about three API calls per repository per run, against
  GitHub's hourly limit for a token. No live measure was taken. Check 891's re-run shows the
  count.
- **U3.** Bitbucket's `Tree` under `docs/backlog/` at depth, within `TreeLimit`. The provider
  implements it (F13); a large folder was not tried.
