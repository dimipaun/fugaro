# Update from anywhere: reconciliation

Checked against `origin/main` at `486823a00` on 2026-11-24. The spec's own claims were checked at
`b8fdb53e2`; of the files it cites, only `init_stages.go` and `update_backlog.go` changed since, and
neither change touches a claim. L8 M1's reader was read from its pull request, #3816.

**Where this file disagrees with [spec.md](spec.md), this file wins.**

It is one capability, so there is no capability map: every part serves the same rule, and none
ships alone.

## Assumptions

- **A1.** "Every enrolled repository" is the list L8's reader draws (F1): the local config's
  repositories plus the concrete owner/name entries of the published policy's admits. A pattern
  admit is named and never expanded, as in L8's D3306. A published admit counts as enrolled for
  every stage (D3350).
- **A2.** "The default branch" is the branch the code host names as default, read through the
  host's API. It is not a local ref: today's `defaultBranchParse` reads the checkout's
  `origin/HEAD`, which is a local copy (F11).
- **A3.** "The hint" orders human output only. Picking the project from the working directory is
  the selector's existing default (`cloud.go`, `--project`: "default: the checkout's fugaro.yaml
  project:"), reused as it is (F26). It changes no `--json` byte, no event and no write.
- **A4.** L5 adds no new cloud write. Each per-repository write is one the per-checkout run already
  makes, so `docs/design/update-in-flight.md`'s analysis holds per repository. That document's rows
  are rewritten to say so (F24).
- **A5.** `--ask` keeps `docs/no-prompt-policy/`'s limits: the owner's own terminal only, never in
  an agent's session, never with `--json`. D3347, as amended, adds value entry to what it turns on.

## Findings

Rows F15–F30 come from review round 1.

- **F1 (Exists).** L8's task M1 (`repofiles.Read`, `docs/repo-override-rulings/`, in flight as
  #3816) is the reader of every listed repository.
  - It lists exactly A1's repositories, reads each default-branch `fugaro.yaml` through
    `gitprov.ContentsReader` and its `.fugaro/recipes/` through `gitprov.TreeReader`, and returns a
    one-line reason for each repository it could not read.
  - L8's O1 says the plan that lands second calls the first one's reader.

  → R1.
- **F2 (Wrong).** `update --check [PATH]` (`init.go`, the `check` flag) promises to "make no cloud
  call" and to read one checkout's own files. The spec drops PATH and gives it "the same
  per-repository read as the converge", but reading the published admits is a cloud call.
  → D3349.
- **F3 (Interaction).** Image-secrets (`docs/secrets-brokering/image-secrets/`, D3324, IS4 in
  flight as #3800) makes every workflow secret a project secret, and `fugaro secrets set NAME`
  stores into that grammar. The spec's `--repo` line now applies only where a name is still
  repository-scoped. → R2.
- **F4 (Interaction).** D3341 gates the `repository` stage on L2. Until then the stage is unchanged:
  `repoEngine(ctx, ".", …)` (`init_repo_stage.go`), behind `resolveRepoTarget`, which skips outside
  a checkout and refuses a working tree that differs from the default branch
  (`init_repo_target.go:306–342`). Acceptance 1, 2 and 8 cannot hold for that stage until U11.
  → R3.
- **F5 (Gap).** The RC39 facts (`internal/localcfg/localcfg.go`) are written by the repository
  engine as it applies a root. D3346 therefore lands in U11, the task D3341 gates. → R4.
- **F6 (Gap).** `backlog.Load` reads a local directory, and the fold names its repository with
  `checkoutRepo`. → R5.
- **F7 (Exists).** A derived build clones its source inside Cloud Build
  (`internal/backend/gcp/build.go` `embeddedBuild`, with the git token secret its embedded
  `cloudbuild.yaml` declares).
  - `refreshPreflight` (`image_refresh.go`) needs a checkout only to read `fugaro.yaml` and the
    origin, and it refuses a repository the local config lacks (`repoKnown`).
  - The cloud step also runs `jobConfigCopyNote(root)` (`upgrade_cloud.go:646`).

  → R6, V1.
- **F8 (Interaction).** Other plans' in-flight tasks write files L5 writes:
  - `init_stages.go`: L1 T2a (#3766) and L8 M2;
  - `init.go`: L1 T4 (#3786) and L8 DK2;
  - `update_in_flight_test.go`: image-secrets IS5 and L8;
  - `doctor.go`: image-secrets IS10 (#3802).

  *Amended in round 1:* L1 T3 (#3778) does not write `init.go`. → R7.
- **F9 (Exists).** `writeAnchor` (`init_fugaroyaml.go`) and the pin's `remove`
  (`init_plugin_stage.go`) print a diff, then gate it: an agent is refused; under the managed gate
  the owner is asked nothing; elsewhere the confirmation stays. → R8.
- **F10 (Gap).** The union of image kinds only adds. Holding every base image for one unread
  repository would make the update worse than today. → R9.
- **F11 (Exists).** `defaultBranchParse` (`init_repo_target.go`) reads the checkout's local
  `origin/HEAD`. Other commands keep it. → A2.
- **F12 (Wrong).** Acceptance 8's scan cannot ban every `gitRead(ctx, ".", …)` in `internal/cli`:
  `doctor.go`, `config_show.go`, `recipes.go` and others legitimately read the working directory.
  The working directory is also reached through `originRepo(ctx)` (`init_images_catalog.go:145`),
  `os.Getwd()` (`init_plugin_stage.go:96`) and `gitCmd(ctx, ".")` (`init_inputs.go:186`). → R10.
- **F13 (Exists).** Every provider implements both readers: github and bitbucket
  (`contents.go`, `commits.go`) and the fake (`fake.go`).
- **F14 (Interaction).** The HARD RULE's pin sentence is in `CLAUDE.md`, and the update skill is
  under `plugin/skills/update/`. Both are agent instructions, so their task gets the security
  reviewer. → R11.
- **F15 (Wrong).** Stages report through `initflow.Status` and `initflow.Outcome`
  (`internal/initflow/stage.go`). The `--json` element is `initflow.StageResult`
  (`internal/initflow/loop.go:133`), not `setup_events.go`.
  - A `repos` member also needs `schemas/setup-events.schema.json`,
    `internal/cli/testdata/setup_events.golden.ndjson` and `console/docs/json-contract.md`.
  - The steps after the stage loop (the `budget.mode` notice and the fold) are not stages and emit
    no stage event.

  → R12.
- **F16 (Wrong).** M1's `repofiles.Repo` (#3816) carries no default branch name and no head commit,
  and it treats a missing `fugaro.yaml` as `YAML == nil`, "a repository that overrides nothing". The
  spec lists a missing file as `unread`. → R13.
- **F17 (Gap).** The secrets stage's hostile-checkout gate (`secretsStage.authorize`,
  `initsecrets.go:520`; `authState`, `init_repo_gate.go`) admits only a repository the local config
  lists (`repoKnown`), or one opted in by `--onboard-repo` or by typing its name. → D3350.
- **F18 (Wrong premise of D3347).** `--ask` today restores only the confirmations a managed
  project's gate stands in for (`confirmNow`: `r.o.ask && r.gateRestores()`, `init.go:2628`; its
  help says it "changes nothing" elsewhere). Value entry is a separate rule, `canPrompt`: any real
  terminal, no flag (`initsecrets.go:262`, D1540). D3347 is amended (below).
- **F19 (Gap).** The token source is the environment only (`providers.FromEnv`). An admin signed in
  through `gh auth login` with no exported variable would see every repository `unread`, a new
  operator step. D3343 is amended (below).
- **F20 (Gap).** `--anchor` is shared with `fugaro setup` (`setup.go:137, 156, 266`), and text
  points at it from many places:
  - code: `doctor.go:500`, `doctor_render.go:921`, `image_refresh.go:387, 427, 592` and
    `update.go:91`;
  - tests and docs: `testdata/init_retire.golden:87`, `README.md:120`,
    `docs/how-to/teammates.md:13` and `.claude/skills/new-release/SKILL.md:79`;
  - the generated `website/content/cli-reference.md` (`go run ./ee/clidoc`).

  `repositoryStage.anchor` is called from `init_repo_stage.go:178, 198`. The `repos add` converge
  is `runProjectReposEnroll` (`project_enroll_converge.go:125`). It reads the working tree's
  `fugaro.yaml`, and its help promises "the repository's own fugaro.yaml: read, never written"
  (`project_enroll.go:151`). → D3351, R14.
- **F21 (Gap).** `doctor` already has a `plugin-pin` check (`doctor.go:393`; its fix,
  `doctor_render.go:750`, reads "run `fugaro update` in the repository's checkout") and a
  `budget-mode` check (`doctor_budget_mode.go`). Both read the working tree, and after L5 the first
  fix is a dead loop. `doctor --offline` and `--plugin` promise no network call. → R15.
- **F22 (Gap).** Doctor has no `*initEngine`, so a list offered only as an engine method never
  reaches it. → R16.
- **F23 (Gap).** The backlog fold upserts items by the file's own id, project-wide
  (`update_backlog.go:22–36`, `backlogUpsert` skips known ids). Two repositories with one id would
  lose an item silently. → R5.
- **F24 (Gap).** These documents describe the working-tree behaviour L5 changes:
  - `docs/design/update-in-flight.md`: its checkout, `budget.mode` and images rows;
  - `README.md:61`;
  - `docs/upgrade/spec.md:52`;
  - `scripts/live-checks/check-884-update-migrates.sh:567` and its `_test.sh:385` (the pin diff);
  - `plugin/skills/update/SKILL.md:35` ("`--check` needs no credentials").

  → U7a, U7b.
- **F25 (Gap).** `update --check`'s steps (`checkSteps`, `update_check.go:108`, pinned by
  `docs_project_centric_test.go`) are `plugin`, `cloud`, `rules` and `policy`. Parsing a
  default-branch `fugaro.yaml` resolves the project layer, which reads a bucket. `cloudCheck` runs
  offline. → R17.
- **F26 (Convention).** The hint's project pick would duplicate the selector's default
  (`cloud.go:58`). → A3.
- **F27 (Gap).** Repository-controlled text reaches the terminal and `--json`: branch names,
  validation problems, workflow names and backlog paths. M1 sanitises its `Unread`; nothing requires
  a stage's own lines to be sanitised. → R12.
- **F28 (Untestable).** Check 891's script prints PASS whenever every differing stage is a §4
  blocker (`check-891-update-trace.sh:49, 307–324`). "Only the repository stage differs" has to be
  read from each stage's mark. → K1.
- **F29 (Untestable).** A dirty checkout whose edit changes `project:` changes the selected project,
  and so the stream. → I1's test edits a key other than `project:`.
- **F30 (Convention).** These tasks reach security paths and get the security reviewer:
  - U2: provider tokens;
  - U9a: a write into a checkout;
  - U6a: how `claude` is spawned;
  - U8: backlog briefs become agents' tasks.

## Decisions

The owner answered D3340–D3348 on 2026-11-23, on the spec's draft. D3349–D3351 and the amendments
to D3343 and D3347 were answered on 2026-11-24. All are as recommended.

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D3340 | The HARD RULE says a leftover plugin pin "is offered its removal as a diff". May L5 reword it? | Reword: a leftover pin becomes a notice in `doctor` and `update`, naming the line to delete | **As recommended (2026-11-23).** The sentence and the update skill change in one pull request (R11) |
| D3341 | The `repository` stage needs L2's per-repository roots | Ship the rest first; that stage's half is a task gated on L2 | **As recommended (2026-11-23).** → R3, R4 |
| D3342 | Derived images (open question 1) | Refresh every stale one; each build keeps today's confirmation and names its repository | **As recommended (2026-11-23).** → R6 |
| D3343 | Which credential reads the default branches (open question 2) | The shell's token, as `image survey` uses | **As recommended (2026-11-23). Amended after review (F19), confirmed by the owner 2026-11-24:** with no token variable set, the reader falls back to `gh auth token` for GitHub and to the git credential helper where one answers, read-only. An unread line names the variable and `gh auth login` |
| D3344 | The backlog fold (open question 3) | Fold from every default branch for one release, then delete it | **As recommended (2026-11-23).** → R5. Its deletion is a follow-up (O2) |
| D3345 | `update --anchor` (open question 4) | Fold it into `project repos add` and remove the flag | **As recommended (2026-11-23).** → R14, with D3351 |
| D3346 | The RC39 facts (open question 5) | The repository stage writes them for every repository; installation state later | **As recommended (2026-11-23).** Lands in U11 (R4) |
| D3347 | `--ask` and value entry (open question 6) | Accept: a value is asked for only under `--ask` | **As recommended (2026-11-23). Amended after review (F18), confirmed by the owner 2026-11-24:** `--ask` also turns on secret value entry at the owner's own terminal, on every project, never for an agent or `--json`. The flag's help and `docs/no-prompt-policy/` say so |
| D3348 | Does the hint narrow `--plan-only`? (open question 7) | No | **As recommended (2026-11-23)** |
| D3349 | What does `update --check` read? (F2) | Drop PATH; read the local config's repositories from the code host, with no GCP call; one line points admit-only repositories to `update --plan-only` | **As recommended (2026-11-24).** → R17 |
| D3350 | Does a published admit count as enrolled for secrets, builds and the fold? (F17) | Yes: the list is the project's own, not a directory's, so the hostile-checkout gate has nothing to guard in `update`'s stages | **As recommended (2026-11-24).** Whoever can publish the allow-list can direct a value into that repository's secrets. The gate stays for `setup --repo` and `--onboard-repo` |
| D3351 | Does `setup --anchor` go too? (F20) | Remove it from both; the anchor lives in `project repos add` alone | **As recommended (2026-11-24).** → R14 |

## Resolutions

- **R1 (F1, F16).** L5 writes no reader of its own. U1 extends `repofiles.Read`, without changing
  its results for L8 when its new option is unset, so that it:
  - takes extra paths;
  - keeps the default branch's name and head commit.

  U1 and U9a wait on M1. That is an external edge, not a contract, because M1 is already running.
- **R2 (F3).** The secrets stage keeps the command its builder makes today, so it follows IS4 when
  IS4 lands. A name several repositories want is one line listing them.
- **R3 (F4, F28).** Until U11, the repository stage is the one exception. It may differ by
  directory; I1's identical-stream test and source scan exempt it by name; its line names L2. U11
  removes the exemption. K1 reads each stage's mark (F28).
- **R4 (F5).** D3346 lands in U11. Until then, the RC39 facts are written as today.
- **R5 (F6, F23).** `backlog.LoadFiles` takes fetched name-to-bytes files, with `backlog.Load`'s
  all-or-nothing rule, and the fold takes the repository from the list. Two repositories whose items
  share an id get one line naming both, and neither item is folded. An item already folded keeps
  its id, so the id format is unchanged.
- **R6 (F7).** For each repository, the checkout stage reads the default branch's `fugaro.yaml` and
  head commit through the view, then runs today's staleness test, build and confirmation. A
  repository it cannot build from this machine gets one line (V1). `jobConfigCopyNote` runs per
  repository, from the view.
- **R7 (F8).** No L5 task waits on another plan's task over a shared file. A task that finds one
  merged first rebases on it, as L8's O2 does.
- **R8 (F9, F20).** `project repos add` gets the anchor step with `writeAnchor`'s exact gate and no
  new question. It reads the repository's `fugaro.yaml` from the default branch, and its help says
  the file is "read, and written only for the anchor line, through the gate, in that repository's
  own checkout".
- **R9 (F10).** The images stage copies the union of the kinds it read. Only the default catalog
  entry is held while a repository is unread.
- **R10 (F12, F4).** The scan covers the functions `update` reaches (its stages and the two steps
  after them). It bans `gitRead(ctx, "."`, `gitCmd(ctx, "."`, `pluginwire.Locate(".")`,
  `loadCheckoutConfigAt(ctx, ".")`, `os.Getwd` and `originRepo(ctx)`. It allows the selector's
  project default and, until U11, the repository stage. A mutation that adds `Locate(".")` to a
  stage fails it.
- **R11 (F14, D3340).** The task that changes `CLAUDE.md`'s pin clause and the update skill gets the
  security reviewer, and it adds a Docs test that pins the new sentence. The rule's "the plugin
  pin: what `doctor` already reports" stays.
- **R12 (F15, F27).** C1 settles how a stage hands over its lines:
  - A stage records them on the engine with `e.reportRepos(stage, lines)`. A step after the loop
    reports under its own step name the same way.
  - U13 carries them to two places:
    - a `repos` member on `initflow.StageResult`, its stage event and its schema;
    - for the steps after the loop, a top-level `notices` member and a `notice` event.

    Both are additions.
  - Every line is built through C1's `newRepoLine`, which passes the repository and the detail
    through `pluginwire.Printable` and `safetext.OneLine`.
- **R13 (F16).** C1's view counts a missing `fugaro.yaml` as `unread` ("no fugaro.yaml on the
  default branch"); L8's own meaning of a nil `YAML` stays inside L8. U2 parses each file:
  - an invalid file is `unread`, naming its first problem;
  - the parse takes the project layer the run already read, and under `--check` the local layer
    file, so it makes no bucket read.
- **R14 (D3345, D3351, F20).**
  - U9a moves the anchor into `repos add`.
  - U9b removes the flag from `update` and `setup`, together with `noteAnchor` and the repository
    stage's anchor calls.
  - U9c moves every pointer to `fugaro project repos add`.
  - The generated CLI reference is regenerated by each task that changes a flag's help.
- **R15 (F21).** `doctor`'s `plugin-pin` and `budget-mode` checks keep their ids and read the
  default branch through the view, with the same text `update` prints. Their fix names the line to
  delete. Under `--offline` and `--plugin` they say "not read (offline)" and make no call.
- **R16 (F22).** C1's list is a free function, `enrolledRepos(ctx, in)`, and `(e *initEngine).repos`
  wraps it. Doctor calls the free function, so when I1 replaces the stub, doctor gets the real list
  too.
- **R17 (F25, D3349).** `update --check` without PATH:
  - the `plugin` step is unchanged (it is this machine's);
  - `rules` and `policy` stay skipped, with their notes;
  - the `cloud` step runs per repository from the view, through U4's per-repository staleness
    function, offline.

  The update skill's "needs no credentials" becomes "reads the code host with your token" (U7a).

## Open tensions

None.

## Open items

- **O1.** L2 (owned by the shared-jobs plan) gates U11 (D3341). Check 930's full pass waits for it
  (R3).
- **O2.** Two follow-up tasks are outside this plan: the backlog fold's deletion, one release after
  L5 ships (D3344), and moving the RC39 facts to installation state (D3346).

## UNVERIFIED

- **V1.** Whether a repository known only from the published admits can be refreshed without a
  local-config entry. `refreshPreflight` refuses one today, and the build's region and image names
  come from the local config. U4 verifies it; a repository it cannot build gets a named line (R6).
- **V2.** The cost of the reads. M1 reads repositories one at a time, with about four calls each,
  and U1 adds two. That is about six calls per repository per run, or 300 for 50 repositories,
  below GitHub's hourly limit for a token. No live measure was taken; Check 930's run shows the
  count.
- **V3.** Bitbucket's `Tree` under `docs/backlog/` at depth, within `TreeLimit`. The provider
  implements it (F13); a large folder was not tried.
