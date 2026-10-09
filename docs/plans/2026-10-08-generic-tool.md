# Generic-Tool Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Fugaro usable by an adopter who is not its owner, inside release **0.7.0** (owner ruling R1). The release gets:
- **logs and the dashboard:** `fugaro logs --url`; a dashboard that shows finished runs, a "ready for your review" list, a per-run detail view and filtering;
- **naming:** `fugaro runs ls` in place of `fugaro ls`;
- **recipes:** the spec's starter recipe catalog, with "use when" texts and three format extensions (bounce-back, the coder on the reviewer's model, review-only), check steps run by the runner, and best-of-N at launch;
- **skills:** a routing skill that picks the recipe;
- **provisioning:** `checkout: clone`, a workflow that runs on the base image with no per-repository build;
- **docs:** the direct-provider documentation, and the control-plane seam;
- **security:** a SECURITY.md trust model with its known limits.

The spec's section 1, the Docker-capable backend, is **release 0.8.0** (ruling R2). Its measurement and its blocked phases are at the end of this plan and are not part of any 0.7.0 group or of the 0.7.0 release notes.

**Architecture:**
- **`internal/recipe`** gains:
  - `UseWhen`, `CoderIsReviewer` and `Mode`;
  - the `check` step kind with `Command` and `Autofix`;
  - `Step.Bounce`;
  - a `Renamed` map;
  - a `StageBound` function.
- **`internal/config`** gains `Commands.Lint`, `Commands.Fix`, `Review.AllowForks` and `Workflow.Checkout`, each entered in the scope table (`scope.go`).
- **`internal/runner`**:
  - `planOf` and `agentLoop` learn bounce, the check gate (`gate.go`, new) and the review mode (`reviewmode.go`, new);
  - `budget_session.go` writes `verify`, `prUrl`, `action` and `tokens` to the registry;
  - `StageTiming` gains `model_usd` and `model` (optional group).
- **`internal/verify`** gains the kind `lint`.
- **`internal/agent.Relay`** gains an `OnTool` hook.
- **`internal/budget`** gains `AgentEntry.Action` and `Tokens`, and their rules.
- **`internal/watch`** gains `FinishedRun`, a row cursor, the detail view, the filter and the review list.
- **`internal/cli`**:
  - `runs ls` (the renamed `ls`) and the `ls` tombstone;
  - `logs --url`;
  - `run --attempts` and repeated `--pr` for `pick`;
  - `budget preview`;
  - the watch flags.
- **`internal/infra` and the runner** handle `checkout: clone`: the job image is the base, the runner makes a blobless clone and a `provision` stage.
- **Docs and skills:**
  - `SECURITY.md`, `docs/recipes.md`, `docs/multi-model.md`, `docs/backends.md` and `docs/gcp-live-checklist.md` (Checks 32 and 33; Check 31 is 0.8.0);
  - the `routing` and `working` skills.

**Tech Stack:** Go 1.27, cobra, bubbletea (watch), yaml.v3. Tests use:
- the fake agent (`internal/agent/fakeclaude`);
- the fake git provider (`internal/gitprov/fake`);
- the runner's test harness (`runner_test.go`'s `newRunHarness`; the implementer reads its exact name);
- the watch golden renders (`internal/watch/testdata`);
- the RTDB rules golden and the emulator property test (`rules` CI job);
- the recipe corpus (`internal/recipe/corpus_test.go`);
- the config corpus (`TestFugaroSchemaCorpus`).

**Spec:** [docs/design/generic-tool.md](../design/generic-tool.md) (decisions G1 to G27).

**Depends on:**
- **Layered config Phase 1 merged** (0.6.0). Tasks 7, 9 and 15 edit its scope table and depend on `ls.go` after its Task 17.
- **Base image Group 2 (`base-kind`, Tasks 9 to 14) merged.** Tasks 9 and 15 add `fugaro.yaml` keys in the same files (`internal/config/config.go`, `validate.go`, `schemas/fugaro.schema.json`, `testdata/config/`), and Task 16 needs the `base` kind.
- Group A (Tasks 1 to 6) and Task 21 (SECURITY.md) depend on neither and can start at once.

**Checked before review:** every "today" statement in the design was read at `origin/main` 65a6a24. The code in this plan has **not** been compiled; each task's run command is its check. Where a task names a helper this plan did not read in full (a harness constructor, a golden updater flag), the implementer uses the real name and keeps the test's assertions.

## Decisions (veto any before execution starts)

The design's G1 to G27 are the decisions, each with its veto alternative in the design's §14. **The owner ruled on 2026-10-08: "merge #214 with the recommendations"** (design R3), so every one of G1 to G27 is built as recommended; no veto alternative below is implemented. The design's R4, the same day, additionally moved the cost preview (G20, design §1.1) to release 0.8.0, which this plan reflects by moving Tasks 22 and 23 (cost preview) out of the 0.7.0 groups and into the 0.8.0 section, renumbered 24 and 25 (see "Release 0.8.0" below). The plan adds:

- **P1. One branch per group.** Each group's tasks run as separate dogfood runs from git worktrees where the group's lanes allow. No run needs Docker or a cloud. Live checks are the owner's.
- **P2. Token economy (memory: subagent token economy).** Tasks 3, 11, 12 and 16 get their own review: they touch the database rules, run commands the runner did not run before, post to PRs, and change what a job runs. The rest are reviewed once per group, on the branch.
- **P3. The `ls` tombstone is deleted in 0.8.0.** That deletion is listed in the 0.8.0 section, not as a 0.7.0 task.

## Global Constraints

Every task's requirements include these.

From the design:
- A recipe never names a command or a model (recipes.md §7). `check.command` is an enum over `build`, `test` and `lint`. The command comes from the `fugaro.yaml` the run read at its ref, never from the bucket.
- Readiness is unchanged: a verified passing test on the final commit plus a senior `ship`. A gate that passes ends with no PR. Review mode never changes a PR's draft state.
- No IAM change in any task. If Check 32 shows `checkout: clone` needs a grant, it goes to the bucket-IAM plan (`docs/design/bucket-iam.md`).
- Every string from the agent, a PR or the bucket that reaches RTDB, a PR comment or the terminal is redacted, then clipped (`clipString`, `logtail.Clip`, `pluginwire.Printable` or `oneLine`, whichever the surrounding code uses).
- The agent's environment allowlist (`internal/agent/env.go`) gains nothing in 0.7.0. Review mode removes the git token, as follow-ups do.
- `fugaro ls` stays as a hidden tombstone that exits 2. There is no working alias.

Project rules:
- A `fugaro.yaml` key needs:
  - the Go struct;
  - `Validate`;
  - `schemas/fugaro.schema.json`;
  - a `testdata/config` corpus file, which `TestFugaroSchemaCorpus` holds to the other three;
  - **and, since layered config, a row in `internal/config/scope.go`**, which `TestScopeTableCoversEveryKey` (layered config Task 1) enforces.
- `internal/cli` and `internal/runner` are slow (about 15 and 19 minutes in full). Each task runs only its focused tests (`-run`) **in the foreground**. Each group ends with one full `go test ./...`. Focused runner tests run with `-race`.
- No live cloud in any test. Never touch EdgeWeb or EdgeServer. Handle no real secret. Live checks are user-run on the sandbox `belong` only.
- Every CI check (`test`, `rules`, `terraform`, `images`, `docker-tests`) is read before merge: each job's log, not only the summary.
- Docs must match behaviour. These stay green: `TestRecipesGuideMatchesTheCLI`, `TestReadmeNamesNoRetiredSkill`, the plugin lint (`plugin/skills_lint_test.go`, which checks every command and flag a skill names against `cli.NewRootCmd()`) and `TestSetupSkillAsksRecipe`.
- Releases go through `/new-release` with `docs/releases/v0.7.0.md` merged first. The base-image plan's Task 22 owns that file and the release itself; Task 23 here only adds sections.

## Review Focus

The failure modes most likely to hurt a user, each pinned by a test:

1. **A recipe escapes its bounds:**
   - a project recipe makes a job run a command;
   - a recipe names a model;
   - bounce loops forever;
   - a gate makes a PR ready.

   Pinned by:
   - `TestCheckCommandIsAnEnum`, `TestRecipeNamesNoModelEvenAsRole` and `TestBounceNeedsFirstLine` (Task 8);
   - `TestStageBoundIsFinite` (Task 8);
   - `TestGateCleanOpensNoPR` and `TestGateCommandsComeFromTheRef` (Task 11);
   - `TestBounceStopsAtReviewRounds` (Task 10).
2. **Review mode writes to someone's PR:** it pushes, flips draft, or runs a fork's code without opt-in. Pinned by:
   - `TestReviewModeNeverPushes`;
   - `TestReviewModeAgentHasNoGitToken`;
   - `TestReviewModeRefusesForkWithoutOptIn`;
   - `TestReviewModeLeavesDraftState` (Task 12).
3. **The dashboard loses or misplaces a run:** the cursor jumps, or a failure disappears before it was seen. Pinned by:
   - `TestCursorFollowsRunAcrossRebuild` and `TestSpaceTogglesSelectedRunOnly` (Task 5);
   - `TestFailedRunsStayUntilAcked` and `TestFilterStricterOfAgeAndCount` (Task 6).
4. **New registry keys halt runs on an installation with old rules.** Pinned by `TestRegistryDropsNewKeysOnceWhenRulesRefuse` (Task 3) and the rules golden.
5. **`checkout: clone` runs a workflow whose needs it cannot meet:** system packages, or a project Dockerfile. Pinned by `TestCloneRefusesImageBuildKeys` (Task 15) and `TestCloneProvisionRunsMiseInstall` (Task 17).

---

## File Structure

| Path | Responsibility | Task | Overlap with other 0.7.0 plans |
|---|---|---|---|
| `internal/cli/logs.go`, `logs_test.go` | `--url`, link in the hint | 1 | none |
| `internal/runner/budget_session.go`, `prflow.go`, `registry_fields_test.go` (new) | `verify` and `prUrl` in the registry | 2 | none |
| `internal/budget/model.go`, `registry.go`, `rules/rules.json.tmpl`, `rules/testdata/*.golden`, `internal/agent/relay.go`, `internal/runner/runner.go` (stage) | `action`, `tokens`, the drop-once fallback | 3 | none |
| `internal/watch/finished.go` (new), `build.go`, `internal/cli/watch_queued.go` | finished runs from the bucket | 4 | none |
| `internal/watch/tui.go`, `render.go`, `cursor.go` (new), `testdata/*` | row cursor, detail, stable order | 5 | none |
| `internal/watch/filter.go` (new), `acks.go` (new), `internal/cli/watch.go` | filter, acks, `--all`, `a`, footer, review list | 6 | none |
| `internal/cli/ls.go` → `runs.go`, `root.go`, `ls_tombstone.go` (new), `recipes.go`, docs and skills naming `fugaro ls` | `runs ls`, tombstone, aliases | 7 | **layered config Task 17 (`ls.go`)** must merge first |
| `internal/recipe/recipe.go`, `catalog.go`, `stages.go` (new), `recipe_test.go`, `testdata/corpus/*` | format extensions | 8 | none |
| `internal/config/config.go`, `validate.go`, `scope.go`, `schemas/fugaro.schema.json`, `testdata/config/valid/commands-lint.yaml` (new), `invalid/*` | `commands.lint`, `commands.fix`, `review.allow_forks` | 9 | **base-image Tasks 9 and 17** (same files); after Group 2 |
| `internal/runner/plan.go`, `runner.go` (`agentLoop`, `seniorLoop`), `recipe.go` (`ApplyRoles`), `runstore/runstore.go` (`ReviewSummary.Bounce`) | bounce, `coder: reviewer` | 10 | none |
| `internal/verify/verify.go`, `internal/runner/gate.go` (new), `gate_test.go` (new), `runstore.go` (`Gate`) | check gate, autofix | 11 | none |
| `internal/runner/reviewmode.go` (new), `reviewmode_test.go`, `internal/gitprov/github/github.go` (`FetchPRHead`), `fake`, `internal/task/task.go` (`ReviewPRs`), `internal/cli/run.go` | review mode, `pick` | 12 | none |
| `internal/cli/run.go`, `run_attempts_test.go` (new) | `--attempts`, repeated `--pr` | 13 | none |
| `internal/recipe/catalog/*.yaml`, `internal/cli/recipes.go` (`--verbose`), `recipes_skew.go`, `docs/recipes.md`, `docs_recipes_test.go` | catalog, `ls --verbose`, the 0.7.0 gate, user doc | 14 | none |
| `internal/config/config.go` (`Workflow.Checkout`), `validate.go`, `scope.go`, schema, corpus | `checkout` key | 15 | base-image Group 2 and Task 17; layered config scope table |
| `internal/infra/spec.go`, `internal/cli/init_repo*.go`, `image_refresh.go`, `imagecheck.go` | clone workflows: job image, no build, no check job | 16 | **base-image Tasks 13 and 19** (`spec.go`, `init`); after Group 4 |
| `internal/runner/runner.go` (bootstrap), `provision.go` (new), `internal/gitops/gitops.go` (`CloneBlobless`) | blobless clone, `mise install` | 17 | none |
| `docs/gcp-live-checklist.md` (Check 32), `plugin/skills/setup/reference/decisions.md` | live check, setup offers clone | 18 | **base-image Task 16** (setup skill), after it |
| `plugin/skills/routing/SKILL.md`, `plugin/skills/working/reference/launch.md`, `plugin/routing_skill_test.go` (new) | recipe-aware routing | 19 | upgrade Task 9 (skill set of five) |
| `docs/multi-model.md`, `docs/backends.md`, `docs/gcp-live-checklist.md` (Check 33) | direct providers, control-plane seam | 20 | none |
| `SECURITY.md`, `internal/cli/docs_security_test.go` (new) | trust model | 21 | bucket-IAM plan (its tasks update one row) |
| none | full suites, PRs | 22 | none |
| `docs/releases/v0.7.0.md` | sections and operator steps | 23 | **base-image Task 22** creates the file |
| `internal/runstore/runstore.go` (`StageTiming`), `internal/runner/runner.go` | per-stage cost | 24 | **release 0.8.0** (owner ruling R4, 2026-10-08) |
| `internal/cli/budget_preview.go` (new), `budget.go` | `budget preview` | 25 | **release 0.8.0** (owner ruling R4, 2026-10-08) |

## PR groups

Groups and their lanes (a lane is a sequence; separate lanes are independent dogfood runs):

- **Group A, branch `gt-visibility`: Tasks 1 to 6.** Starts at once.
  - Lane A1: Task 1.
  - Lane A2: Task 2, then Task 3.
  - Lane A3: Task 4, then 5, then 6.
  - Task 6's detail view uses Task 3's fields, so the review list's columns are merged last.
- **Group S, branch `gt-security`: Task 21.** Starts at once, independent. It can be its own small PR.
- **Group B, branch `gt-naming`: Task 7.** After layered config Task 17 merges. Independent of everything else here.
- **Group C, branch `gt-recipes`: Tasks 8 to 14.** After base-image Group 2 merges (Task 9 only; Task 8 can start earlier).
  - Lane C1: Task 8, then 10, then 11, then 12, then 14.
  - Lane C2: Task 9, which must merge before 11 (check commands) and 12 (`review.allow_forks`).
  - Lane C3: Task 13, after 12.
- **Group D, branch `gt-checkout`: Tasks 15 to 18.**
  - Task 15 runs after Task 9 merges (the same config files).
  - Task 16 runs after base-image Group 4.
  - Task 17 can run in parallel with Task 16.
  - Task 18 runs after base-image Task 16.
- **Group E, branch `gt-docs`: Tasks 19 and 20.** Task 19 after Group C merges (it names the catalog). Task 20 at any time.
- **Group F, cost preview, is release 0.8.0** (owner ruling R4, 2026-10-08), not a 0.7.0 group. Branch `gt-cost`, starting after 0.7.0 ships; see "Release 0.8.0" below, where it runs as Tasks 24 and 25, alongside the Docker backend.
- **Task 22** closes each group. **Task 23** runs once every included group has merged, before base-image Task 22's release.

The release freeze (base-image plan) starts at base-image Group 2's merge and ends with 0.7.0. Every group here merges inside it.

---
## Group A: visibility (branch `gt-visibility`)

### Task 0: Start

- [ ] Create the worktree from `origin/main`: `git worktree add -b gt-visibility .worktrees/gt-visibility origin/main`. Each later group does the same with its branch name, after its dependencies have merged.
- [ ] `go build ./... && go vet ./...` passes before any change.

### Task 1: `fugaro logs --url` and the link in the empty hint (G19)

**Files:**
- Modify: `internal/cli/logs.go`
- Test: `internal/cli/logs_test.go`

**Interfaces:**
- Consumes: `locateLaunched` (returns `*runstore.Launch`, whose `LogURL` `run` and `diagnose` already print).
- Produces: `logsOptions.url bool`; `emptyViewHint(w, env, url string)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/logs_test.go`:

```go
// TestLogsURL (G19): --url prints the execution's console link and reads
// no log; the empty-view hint names the same link.
func TestLogsURL(t *testing.T) {
	f := newCloudFixture(t)
	seedRun(t, f, "20260927-100000-abcd", "", "someone@example.com", true)
	f.logging.FailReads = true // any read fails the test below
	out, _, err := execute(t, "logs", "--url", "20260927-100000-abcd")
	if err != nil {
		t.Fatalf("logs --url: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "https://console.cloud.google.com/") || strings.Count(out, "\n") != 1 {
		t.Fatalf("logs --url = %q, want one console URL line", out)
	}
	if _, _, err := execute(t, "logs", "--url", "--follow", "20260927-100000-abcd"); ExitCode(err) != ExitUserError {
		t.Fatalf("--url with --follow: %v", err)
	}
	f.logging.FailReads = false
	const view = "projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs"
	f.appendConfig(t, "log_view: "+view+"\n")
	f.logging.Resource = view
	_, stderr, err := execute(t, "logs", "20260927-100000-abcd")
	if err != nil || !strings.Contains(stderr, "https://console.cloud.google.com/") {
		t.Fatalf("empty hint without the link: %q, %v", stderr, err)
	}
}
```

`f.logging.FailReads` may not exist on the Cloud Logging fake (`internal/gcpfake`). If it doesn't, add it there: a bool that makes every `entries.list` return HTTP 500. That is a three-line change in the fake's handler, and the fake's own test gets one case for it.

- [ ] **Step 2: Run it to see it fail**

Run: `go test -race -count=1 -run 'TestLogsURL' ./internal/cli/`
Expected: FAIL: `unknown flag: --url`.

- [ ] **Step 3: Implement**

In `internal/cli/logs.go`:
- add `url bool` to `logsOptions`;
- register `f.BoolVar(&o.url, "url", false, "print the execution's Cloud console link instead of the logs")`;
- in `runLogs`, after `locateLaunched`:

```go
	if o.url {
		if o.follow || o.asJSON {
			return userErr("--url prints only the link: it doesn't go with --follow or --json")
		}
		if l.LogURL == "" {
			return userErr("no console link was recorded for this run's execution")
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), oneLine(l.LogURL))
		return err
	}
```

The flag check runs before `openCloud`, so a bad flag combination fails without any cloud call. Change `emptyViewHint(cmd.ErrOrStderr(), env)` to `emptyViewHint(cmd.ErrOrStderr(), env, l.LogURL)`, and add to its message, when `url != ""`: `" The Cloud console's page for this execution: " + oneLine(url)`. Update `diagnose.go`'s call with its own `LogURL`.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 -run 'TestLogs|TestDiagnose' ./internal/cli/`
Expected: `ok`.

**Mutation check:** drop the `o.follow ||` condition, and the second `execute` in the test must fail.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 1: fugaro logs --url and the link in the empty hint"`

### Task 2: The runner writes `verify` and `prUrl` to the registry (G24, first half)

Both keys are in `AgentEntry` and the deployed rules (`rules.json.tmpl` lines 155 and 160) already accept them. Nothing writes them today.

**Files:**
- Modify: `internal/runner/prflow.go` (`notePR`), `internal/runner/runner.go` (`afterStage` caller), `internal/runner/budget_session.go`
- Test: `internal/runner/registry_fields_test.go` (new)

**Interfaces:**
- Produces: `func (r *run) noteRegistry(fn func(*budget.AgentEntry))`, a nil-safe wrapper over `r.sess.Update`. `beginBudgetStage` is refactored onto it.

- [ ] **Step 1: Write the failing test**

```go
package runner_test

// TestRegistryCarriesVerifyAndPR (G24): once the coder's verify record
// exists the entry's verify is its one-line summary; once the early draft
// PR exists, prUrl is its URL. Both reach the database through the
// heartbeat, which the fake RTDB records.
func TestRegistryCarriesVerifyAndPR(t *testing.T) {
	h := newBudgetHarness(t, budgetHarnessOptions{EarlyDraft: true}) // the M9e early-draft harness
	h.Agent.Script("implement", fakeclaude.Verify("test", true), fakeclaude.Commit("a.go"))
	h.Run(t)
	writes := h.RTDB.AgentWrites(h.Slug, h.RunID)
	var sawVerify, sawPR bool
	for _, e := range writes {
		if strings.HasPrefix(e.Verify, "fugaro verify test #1: passed") {
			sawVerify = true
		}
		if e.PRURL == h.Provider.PRURL(1) {
			sawPR = true
		}
	}
	if !sawVerify || !sawPR {
		t.Fatalf("registry writes never carried verify (%v) or prUrl (%v): %+v", sawVerify, sawPR, writes)
	}
}
```

The names `newBudgetHarness`, `fakeclaude.Verify`, `h.RTDB.AgentWrites` and `h.Provider.PRURL` stand for the existing budget-session and early-draft test harness (`budget_test.go`, `checkpoint_pr_test.go`). The implementer uses their real names and keeps the two assertions.

- [ ] **Step 2: Run it to see it fail**

Run: `go test -race -count=1 -run 'TestRegistryCarriesVerifyAndPR' ./internal/runner/`
Expected: FAIL: `registry writes never carried verify (false) or prUrl (false)`.

- [ ] **Step 3: Implement**

In `budget_session.go`:

```go
// noteRegistry changes the run's registry entry when a session exists; the
// next heartbeat sends it.
func (r *run) noteRegistry(fn func(*budget.AgentEntry)) {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess != nil {
		sess.Update(fn)
	}
}
```

`beginBudgetStage` calls `r.noteRegistry`.

In `prflow.go`'s `notePR`, after `r.rec.PR = …`:

```go
	url := r.rec.PR.URL
	r.noteRegistry(func(e *budget.AgentEntry) { e.PRURL = url })
```

Add `func (r *run) noteVerify()`. It reads `verify.Records(r.d.StateDir)`, takes the last record's `Summary()`, redacts it and clips it to 120 runes (the rules clip at 200), and sets `e.Verify`. Call it at the top of `afterStage`, which runs at every stage boundary, and once in finalize before `Finish`. A failed read logs a warning and changes nothing.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 -run 'TestRegistry|TestBudget|TestEarlyDraft' ./internal/runner/`
Expected: `ok`.

**Mutation check:** make `noteVerify` set `e.Verify` before redaction and add a secret to the fake's verify output. `TestRedaction*` must catch it; if none does, add one assertion to this test that a registered secret never appears in `writes`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 2: the registry carries the verify summary and the PR link"`

### Task 3: `action` and `tokens` in the registry, rules and the drop-once fallback (G24) — **own review**

**Files:**
- Modify:
  - `internal/budget/model.go` (`AgentEntry`), `registry.go` (`clipEntry`, `Start`, `Update`, the heartbeat's refusal handling);
  - `internal/budget/rules/rules.json.tmpl` and its golden (`internal/budget/rules/testdata/rules.golden.json`, or the real name);
  - `internal/agent/relay.go` (`OnTool`);
  - `internal/runner/runner.go` (the stage function, where `NewRelay` is called);
  - `internal/runner/budget_session.go`.
- Test: `internal/budget/registry_test.go`, `internal/agent/relay_test.go`, the rules emulator property test (`internal/budget/rules/*_test.go`, build tag as today)

**Interfaces:**
- Produces:
  - `AgentEntry.Action string` (`json:"action,omitempty"`) and `AgentEntry.Tokens int64` (`json:"tokens,omitempty"`);
  - `Relay.OnTool func(summary string)`, called with the same redacted text the relay logs (`"tool " + name + ": " + summary`) after redaction and before clipping;
  - `Session.newKeysRefused` (unexported).

- [ ] **Step 1: Write the failing tests**

In `internal/agent/relay_test.go`:

```go
// TestRelayOnToolGetsTheRedactedSummary: the hook sees exactly what the
// log line says, never a secret.
func TestRelayOnToolGetsTheRedactedSummary(t *testing.T) {
	const secret = "fake-secret-0123456789"
	var got []string
	r := NewRelay(slog.New(slog.NewTextHandler(io.Discard, nil)), []string{secret})
	r.OnTool = func(s string) { got = append(got, s) }
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo ` + secret + ` && go test ./..."}}]}}` + "\n"
	if _, err := r.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || strings.Contains(got[0], secret) || !strings.HasPrefix(got[0], "tool Bash: echo [REDACTED] && go test") {
		t.Fatalf("OnTool got %q", got)
	}
}
```

In `internal/budget/registry_test.go`:

```go
// TestRegistryDropsNewKeysOnceWhenRulesRefuse: rules deployed before 0.7.0
// refuse an entry with action or tokens (their $other). The session drops
// both for the rest of the run, warns once naming fugaro init, and the
// entry is still written.
func TestRegistryDropsNewKeysOnceWhenRulesRefuse(t *testing.T) {
	db := newFakeRTDB(t, fakeRulesWithout("action", "tokens"))
	s, logs := startSession(t, db, AgentEntry{Repo: "o/r", Stage: "bootstrap"})
	s.Update(func(e *AgentEntry) { e.Action, e.Tokens = "tool Bash: go test", 1200 })
	s.heartbeatNow(t)
	s.heartbeatNow(t)
	e := db.Agent(t, s.cfg.Slug, s.cfg.Run)
	if e.Action != "" || e.Tokens != 0 || e.Stage != "bootstrap" {
		t.Fatalf("entry = %+v", e)
	}
	if n := strings.Count(logs.String(), "fugaro init"); n != 1 {
		t.Fatalf("warned %d times, want once: %s", n, logs)
	}
}
```

`newFakeRTDB`, `fakeRulesWithout`, `startSession` and `heartbeatNow` stand for the helpers the 0.5.0 recipe-key test uses (`TestStartDropsRecipe…` in `registry_test.go`). Reuse them.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestRelayOnTool|TestRegistryDropsNewKeys' ./internal/agent/ ./internal/budget/`
Expected: compile errors (`OnTool`, `Action`, `Tokens` undefined).

- [ ] **Step 3: Implement**

1. `model.go`: add the two fields under `PRURL`, with the comment `// Action is the agent's last tool call, redacted (design generic-tool §10.2). Rules deployed before 0.7.0 refuse it and Tokens; the session then drops both.`
2. `registry.go`:
   - `clipEntry` clips `Action` (add it to the list);
   - `Update` clears both when `s.newKeysRefused` is set (like `noRecipe`);
   - in the heartbeat's write path, a `ErrPermissionDenied` while the entry carries either key sets `newKeysRefused`, retries once without them, and warns once: `the dashboard rules predate 0.7.0, so the last action and token count are not shown; run fugaro init --firebase <id> to update them`.
3. `rules.json.tmpl`, under `agents/$slug/$run`:
   ```json
   "action": { ".validate": "STR && RBOK" },
   "tokens": { ".validate": "newData.isNumber() && newData.val() >= 0 && newData.val() <= 1000000000 && RBOK" },
   ```
   Then regenerate the golden with the existing updater (`go test ./internal/budget/rules/ -run TestRulesGolden -update`, or the flag the test defines).
4. `relay.go`: the field `OnTool func(string)`. In the `tool_use` case, compute `s := r.redact("tool " + r.redact(c.Name) + ": " + r.toolSummary(c.Input))`, log `r.msg(s, relayToolBytes)` as now, and call `r.OnTool(logtail.Clip(s, 160))` when it is set.
5. `runner.go`, where the stage creates its relay:
   ```go
   relay.OnTool = func(s string) { r.noteRegistry(func(e *budget.AgentEntry) { e.Action = s }) }
   ```
   At the stage's end, with the result's usage, set `e.Tokens` to the stage's input plus output tokens, and clear `e.Action` at the next `beginBudgetStage`.

- [ ] **Step 4: Run the tests, the golden and the rules property test**

Run: `go test -race -count=1 ./internal/agent/ ./internal/budget/... && go test -race -count=1 -run 'TestRegistry|TestBudget|TestStage' ./internal/runner/`
Expected: `ok` lines. The rules property test runs in CI's `rules` job on the emulator; read its log.

**Mutation checks:**
- Remove the `RBOK` from `action`: the property test must fail (any launcher could write any run's action).
- Make the drop-once warn on every heartbeat: the `Count == 1` assertion fails.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 3: the registry carries the last action and the token count, with the old-rules fallback"`

### Task 4: Finished runs from the runs bucket (G22)

**Files:**
- Create: `internal/watch/finished.go`, `internal/watch/finished_test.go`
- Modify: `internal/cli/watch_queued.go` (the scanner's per-run read), `internal/watch/build.go` (`View`, `RepoBlock`)

**Interfaces:**
- Produces:
  ```go
  // FinishedRun is a run whose result.json exists, read from the runs bucket
  // by the same scanner as queued runs (design generic-tool §10.1).
  type FinishedRun struct {
      Run, Slug, Title, Workflow string
      Status     string    // runstore status: succeeded, failed, halted, cancelled, infra_error
      Outcome    string    // ready, draft, none, reviewed
      PRNumber   int
      PRURL      string
      FinishedAt time.Time
  }
  func MergeFinished(v View, fin []FinishedRun, now time.Time) View
  ```
  `RepoBlock.Finished []RunRow`, where a finished `RunRow` has `Finished: true`, `Outcome`, `PRURL` and `Failed` (when the status is not `succeeded`).
- The scanner's `scan` returns `(queued []watch.QueuedRun, finished []watch.FinishedRun, note string, err error)`. Every caller of `scan` and `fetchQueuedOnce` is updated.

- [ ] **Step 1: Write the failing test**

```go
package watch

func TestMergeFinishedAddsRowsBelowRunning(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	v := View{Repos: []RepoBlock{{Slug: "o-r", Runs: []RunRow{{Run: "r-live", StartedAt: now.Add(-time.Minute).UnixMilli()}}}}}
	fin := []FinishedRun{
		{Run: "r-old", Slug: "o-r", Status: "succeeded", Outcome: "ready", PRURL: "https://github.com/o/r/pull/7", PRNumber: 7, FinishedAt: now.Add(-2 * time.Hour)},
		{Run: "r-new", Slug: "o-r", Status: "failed", Outcome: "draft", FinishedAt: now.Add(-time.Hour)},
		{Run: "r-live", Slug: "o-r", Status: "succeeded", FinishedAt: now}, // still live in /agents: skipped
	}
	got := MergeFinished(v, fin, now)
	b := got.Repos[0]
	if len(b.Runs) != 1 || len(b.Finished) != 2 {
		t.Fatalf("runs %d finished %d", len(b.Runs), len(b.Finished))
	}
	if b.Finished[0].Run != "r-new" || !b.Finished[0].Failed || b.Finished[1].PRURL != "https://github.com/o/r/pull/7" {
		t.Fatalf("finished rows = %+v", b.Finished)
	}
}
```

In `internal/cli/watch_queued_scan_test.go`, add `TestScannerReturnsFinishedRuns`: a seeded run with `result.json` (`seedRun` plus `writeRecord`, as the `ls` tests do) comes back in `finished` with its PR URL and outcome, and not in `queued`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestMergeFinished' ./internal/watch/ && go test -race -count=1 -run 'TestScannerReturnsFinished|TestQueued' ./internal/cli/`
Expected: compile errors.

- [ ] **Step 3: Implement**

`finished.go`:

```go
func MergeFinished(v View, fin []FinishedRun, now time.Time) View {
	live := map[string]bool{}
	for _, b := range v.Repos {
		for _, r := range b.Runs {
			live[b.Slug+"/"+r.Run] = true
		}
	}
	idx := map[string]int{}
	for i, b := range v.Repos {
		idx[b.Slug] = i
	}
	for _, f := range fin {
		slug := budget.Key(f.Slug)
		if live[slug+"/"+clean(f.Run)] {
			continue
		}
		i, ok := idx[slug]
		if !ok {
			v.Repos = append(v.Repos, RepoBlock{Slug: slug, Name: clean(f.Slug)})
			i = len(v.Repos) - 1
			idx[slug] = i
		}
		v.Repos[i].Finished = append(v.Repos[i].Finished, finishedRow(f, now))
	}
	for i := range v.Repos {
		sort.SliceStable(v.Repos[i].Finished, func(a, b int) bool {
			return v.Repos[i].Finished[a].StartedAt > v.Repos[i].Finished[b].StartedAt // newest first
		})
	}
	return v
}

func finishedRow(f FinishedRun, now time.Time) RunRow {
	return RunRow{
		Run: clean(f.Run), Slug: budget.Key(f.Slug), Title: dash(clean(f.Title)), Stage: clean(f.Status),
		Round: "-", Verify: "-", Models: "-", Auth: "-", Workflow: clean(f.Workflow),
		Age: max(now.Sub(f.FinishedAt), 0), HasAge: true,
		Finished: true, Failed: f.Status != "succeeded", Outcome: clean(f.Outcome),
		PRURL: cleanURL(f.PRURL), PRNumber: f.PRNumber, StartedAt: f.FinishedAt.UnixMilli(),
	}
}
```

`cleanURL` accepts only `https://` URLs of at most 200 bytes, after `clean`; anything else becomes `""`. A PR URL is bucket text and so untrusted.

In `watch_queued.go`, the per-run read that already opens `task.json` and `launch.json` also reads `result.json` when present, through the same `runstore` reader `ls` uses (`runview.Join` inputs). It returns a `FinishedRun` within the scanner's lookback (24 h) instead of skipping the run.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/watch/ && go test -race -count=1 -run 'TestQueued|TestScanner|TestWatch' ./internal/cli/`
Expected: `ok`. Existing goldens change only where a fixture has finished runs; regenerate those with the package's update flag and read every diff.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 4: watch reads finished runs from the runs bucket"`

### Task 5: Row cursor, stable order and the detail view (G23)

**Files:**
- Create: `internal/watch/cursor.go`, `internal/watch/cursor_test.go`
- Modify: `internal/watch/tui.go` (`model.sel` → `model.cur`, `move`, `fold`, the space key), `render.go` (selection mark on rows, the detail lines, `selTop`), `build.go` (`lessRepoBlock` → by name), `testdata/*.golden`

**Interfaces:**
- Produces:
  ```go
  // Cursor is the selected row: a repository header (Run == "") or one run.
  type Cursor struct{ Slug, Run string }
  // Rows lists the selectable rows of v in screen order, honouring folds.
  func Rows(v View, collapsed map[string]bool) []Cursor
  // Resolve finds c in rows; if it is gone it returns its nearest neighbour in
  // the same block (the row now at its old position, else the header), and
  // the first row only when the whole block is gone.
  func Resolve(rows []Cursor, prev []Cursor, c Cursor) Cursor
  ```
  `model.expanded map[Cursor]bool`.

- [ ] **Step 1: Write the failing tests**

```go
func TestCursorFollowsRunAcrossRebuild(t *testing.T) {
	before := []Cursor{{"a", ""}, {"a", "r1"}, {"a", "r2"}, {"b", ""}, {"b", "r3"}}
	after := []Cursor{{"a", ""}, {"a", "r1"}, {"b", ""}, {"b", "r3"}} // r2 finished and was filtered out
	if got := Resolve(after, before, Cursor{"a", "r2"}); got != (Cursor{"a", "r1"}) {
		t.Fatalf("vanished run resolved to %+v, want its neighbour a/r1", got)
	}
	if got := Resolve(after, before, Cursor{"b", "r3"}); got != (Cursor{"b", "r3"}) {
		t.Fatalf("present run moved to %+v", got)
	}
}

func TestSpaceTogglesSelectedRunOnly(t *testing.T) {
	m := newTestModel(t, twoReposThreeRuns()) // tui_test.go's helper
	m.cur = Cursor{"a", "r1"}
	m.key(tea.KeyMsg{Type: tea.KeySpace})
	if !m.expanded[Cursor{"a", "r1"}] || m.collapsed["a"] || len(m.expanded) != 1 {
		t.Fatalf("expanded %v collapsed %v", m.expanded, m.collapsed)
	}
	m.help = true
	m.key(tea.KeyMsg{Type: tea.KeySpace})
	if !m.expanded[Cursor{"a", "r1"}] {
		t.Fatal("space with help open changed the view")
	}
	m.help = false
	m.cur = Cursor{"a", ""}
	m.key(tea.KeyMsg{Type: tea.KeySpace})
	if !m.collapsed["a"] {
		t.Fatal("space on a header did not fold")
	}
}

func TestBlocksKeepTheirOrderWhenSpendChanges(t *testing.T) {
	v1 := buildFrom(t, spend("a", 1), spend("b", 9))
	v2 := buildFrom(t, spend("a", 20), spend("b", 9))
	if v1.Repos[0].Slug != "a" || v2.Repos[0].Slug != "a" {
		t.Fatal("blocks re-sorted by spend")
	}
}
```

The detail view's golden is `testdata/detail-120.golden`, written in Step 3. It shows, under the selected run: stage and round, the action, verify, the PR, spent and tokens, the deadline, and the models with the recipe.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestCursor|TestSpaceToggles|TestBlocksKeep' ./internal/watch/`
Expected: compile errors.

- [ ] **Step 3: Implement**

- `build.go`: `lessRepoBlock` orders killed blocks first, then by repository name, then slug. Spend is still shown, but no longer orders.
- `cursor.go`: `Rows` and `Resolve` as specified (pure).
- `tui.go`:
  - `model.cur Cursor` replaces `sel`;
  - `move(d)` walks `Rows(m.view, m.collapsed)`;
  - `rebuild()` keeps the previous rows and calls `Resolve`;
  - the space key checks `m.help` first and returns, then toggles `expanded[m.cur]` for a run or `collapsed[m.cur.Slug]` for a header;
  - kill and resume prompts target `m.cur.Slug`, as `sel` did.
- `render.go`:
  - a selected run row gets the same reverse-video mark headers get;
  - an expanded run renders up to six indented detail lines from `RunRow`. `RunRow` gains `Action`, `Tokens`, `PRURL` and `Deadline`, filled by `runRow` from `AgentEntry`'s `Action`, `Tokens`, `PRURL` and `StageDeadline`; `runRow` ignored `PRURL` before;
  - `selTop` scrolls so that the selected row and its detail lines are visible, not only the block header.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/watch/`
Expected: `ok`. Regenerate the goldens and read every diff: only the block order (by name), the selection mark and the new detail golden may change.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 5: watch selects runs, keeps blocks in place and expands a run's detail"`

### Task 6: Filtering, acknowledgements and the review list (G22, G25)

**Files:**
- Create: `internal/watch/filter.go`, `filter_test.go`, `acks.go`, `acks_test.go`
- Modify: `internal/watch/tui.go` (keys `a`, `x`; `TUIOptions.All`, `Keep`, `KeepCount`, `AckPath`), `render.go` (footer, the "Ready for your review" section), `internal/watch/plain.go` (degraded mode applies the filter), `internal/cli/watch.go` (flags)

**Interfaces:**
- Produces:
  ```go
  type Filter struct {
      All        bool
      Keep       time.Duration // default 6h
      KeepCount  int           // default 15
      FailedKeep time.Duration // 24h, not a flag
  }
  // Apply hides finished rows per the filter and returns how many it hid.
  func (f Filter) Apply(v View, acked func(slug, run string) bool) (View, int)
  // ReadyForReview lists finished rows whose Outcome is "ready", newest first.
  func ReadyForReview(v View) []RunRow
  type Acks struct{ /* path, map[string]time.Time */ }
  func LoadAcks(path string) *Acks // never fails: a bad or missing file is empty
  func (a *Acks) Ack(slug, run string, now time.Time) error
  func (a *Acks) Has(slug, run string) bool
  ```
- Flags on `fugaro watch`: `--all`, `--keep` (duration, default `6h`), `--keep-count` (int, default 15). `--keep` must be positive and `--keep-count` at least 1, else a usage error.

- [ ] **Step 1: Write the failing tests**

```go
func TestFilterStricterOfAgeAndCount(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var fin []RunRow
	for i := 0; i < 20; i++ { // 20 successes, one every 10 minutes
		fin = append(fin, RunRow{Run: fmt.Sprintf("r%02d", i), Finished: true, Age: time.Duration(i) * 10 * time.Minute})
	}
	v := View{Repos: []RepoBlock{{Slug: "a", Finished: fin}}}
	got, hidden := Filter{Keep: 6 * time.Hour, KeepCount: 15}.Apply(v, nil)
	if n := len(got.Repos[0].Finished); n != 15 || hidden != 5 {
		t.Fatalf("kept %d hid %d, want 15 and 5 (count is stricter)", n, hidden)
	}
	got, _ = Filter{Keep: time.Hour, KeepCount: 15}.Apply(v, nil)
	if n := len(got.Repos[0].Finished); n != 6 { // ages 0..50 min
		t.Fatalf("kept %d, want 6 (age is stricter)", n)
	}
	got, hidden = Filter{All: true, Keep: time.Hour, KeepCount: 1}.Apply(v, nil)
	if len(got.Repos[0].Finished) != 20 || hidden != 0 {
		t.Fatal("--all filtered")
	}
}

func TestFailedRunsStayUntilAcked(t *testing.T) {
	failed := RunRow{Run: "f", Finished: true, Failed: true, Age: 10 * time.Hour}
	ok := RunRow{Run: "s", Finished: true, Age: 10 * time.Hour}
	v := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{failed, ok}}}}
	f := Filter{Keep: 6 * time.Hour, KeepCount: 15, FailedKeep: 24 * time.Hour}
	got, _ := f.Apply(v, func(string, string) bool { return false })
	if len(got.Repos[0].Finished) != 1 || got.Repos[0].Finished[0].Run != "f" {
		t.Fatalf("got %+v, want only the failure", got.Repos[0].Finished)
	}
	got, _ = f.Apply(v, func(_, run string) bool { return run == "f" })
	if len(got.Repos[0].Finished) != 0 {
		t.Fatal("an acknowledged failure stayed")
	}
}

func TestAcksSurviveABadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "watch-acks.json")
	os.WriteFile(p, []byte("{not json"), 0o600)
	a := LoadAcks(p)
	if a.Has("a", "r") {
		t.Fatal("bad file acked something")
	}
	if err := a.Ack("a", "r", time.Now()); err != nil || !LoadAcks(p).Has("a", "r") {
		t.Fatalf("ack did not persist: %v", err)
	}
}
```

Also add `TestFooterNamesTheView` (render golden at 60, 80 and 120 columns: `showing active + recent · a: all` appears at every width) and `TestReadyForReviewSection` (a golden with two `ready` rows and their URLs, and a `draft` row absent from the section).

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestFilter|TestFailedRuns|TestAcks|TestFooter|TestReadyFor' ./internal/watch/`
Expected: compile errors.

- [ ] **Step 3: Implement**

`Filter.Apply`:
- For each block, partition `Finished` into failures and successes.
- A failure is kept when `!acked(slug, run) && (All || Age <= FailedKeep)`.
- Successes, already newest first, are kept while `All || (Age <= Keep && kept < KeepCount)`.
- Return the blocks with `Finished` replaced and the hidden count.
- A block with no runs, no spend and no kept finished rows is dropped, as today.

Acks: `$XDG_STATE_HOME/fugaro/watch-acks.json` (default `~/.local/state`), mode 0600, written atomically (temp file plus rename). Entries older than 7 days are pruned on write. Every error is swallowed into the empty set on read and returned on write, and the TUI shows a write error as a notice.

Keys:
- `a` flips `m.filter.All`;
- `x` on a failed finished run calls `Ack`, and is a no-op elsewhere;
- both rebuild.

Footer: the view text comes first and survives every width.

The review section is rendered after the last block when `ReadyForReview` is non-empty: a header `Ready for your review (N)`, then one line per run with `slug  #N  url  title`. URLs go through `cleanURL` (Task 4).

`internal/cli/watch.go` passes the flags into `TUIOptions`, and into the plain and degraded paths. `--json` (the machine view) is unfiltered and gains `finished` and `ready` arrays.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/watch/ && go test -race -count=1 -run 'TestWatch' ./internal/cli/`
Expected: `ok`.

**Mutation checks:**
- Swap `&&` for `||` in the success rule: `TestFilterStricterOfAgeAndCount` fails.
- Remove the ack check: `TestFailedRunsStayUntilAcked` fails.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 6: watch filters finished runs, keeps failures until acknowledged, and lists PRs ready for review"`

---

## Group S: SECURITY.md (branch `gt-security`)

Task 21 is written out under Group E, because it is a docs task. It has no dependency and can run first.

---

## Group B: naming (branch `gt-naming`)

### Task 7: `fugaro runs ls`, the `ls` tombstone and singular aliases (G17)

**Depends on:** layered config Task 17 merged (it edits `ls.go`).

**Files:**
- Modify: `internal/cli/root.go`; rename `internal/cli/ls.go` → `runs.go` and its tests; `internal/cli/recipes.go` and `secrets.go` (aliases)
- Create: `internal/cli/ls_tombstone.go`, `internal/cli/runs_test.go` (the renamed `ls_test.go` plus the new cases)
- Modify every doc, skill and hint that says `fugaro ls`: `grep -rn 'fugaro ls\b' --include='*.md' --include='*.go' . | grep -v -e docs/plans -e docs/releases -e docs/design`

**Interfaces:**
- Produces: `newRunsCmd()` with one subcommand, `ls` (the old command, unchanged flags); `newLsTombstone()`, hidden.

- [ ] **Step 1: Write the failing tests**

```go
func TestRunsLsIsTheOldLs(t *testing.T) {
	f := newCloudFixture(t)
	seedRun(t, f, "20260927-100000-abcd", "", "someone@example.com", true)
	out, _, err := execute(t, "runs", "ls", "--json")
	if err != nil || !strings.Contains(out, "20260927-100000-abcd") {
		t.Fatalf("runs ls = %s, %v", out, err)
	}
}

func TestLsIsATombstone(t *testing.T) {
	newCloudFixture(t)
	_, _, err := execute(t, "ls", "--all", "--since", "1d") // its old flags are accepted and ignored
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro ls was renamed in 0.7.0: use fugaro runs ls") {
		t.Fatalf("ls: %v", err)
	}
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == "ls" && !c.Hidden {
			t.Fatal("the tombstone is listed in help")
		}
	}
}

func TestSingularNounAliases(t *testing.T) {
	for _, args := range [][]string{{"recipe", "validate", "--help"}, {"secret", "ls", "--help"}, {"run", "--help"}} {
		if _, _, err := execute(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
```

`fugaro run` stays the launch verb: there is no `run` alias for `runs`. The third case only checks that nothing shadowed it.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestRunsLs|TestLsIsATombstone|TestSingularNoun' ./internal/cli/`
Expected: FAIL: `unknown command "runs"`, and `ls` still lists runs.

- [ ] **Step 3: Implement**

`ls_tombstone.go`:

```go
// newLsTombstone keeps the 0.6 name answering for one release with the new
// one (design generic-tool §5, G17). It is deleted in 0.8.0.
func newLsTombstone() *cobra.Command {
	return &cobra.Command{
		Use:                "ls",
		Hidden:             true,
		DisableFlagParsing: true, // any old flag reaches the message, not a flag error
		RunE: func(*cobra.Command, []string) error {
			return userErr("fugaro ls was renamed in 0.7.0: use fugaro runs ls (same flags)")
		},
	}
}
```

`runs.go`: `newRunsCmd()` returns `&cobra.Command{Use: "runs", Short: "List and inspect runs"}`, with `newLsCmd()` (the old constructor, unchanged) added as its child. In `root.go`, replace `newLsCmd()` with `newRunsCmd(), newLsTombstone()`. Add `Aliases: []string{"recipe"}` to `recipes` and `Aliases: []string{"secret"}` to `secrets`.

Text sweep: every hint string in `internal/` that says `fugaro ls` becomes `fugaro runs ls`. `ls --pr` help, `lswarn.go`, `run.go`'s launch line and the follow-up messages are the known ones; the grep finds the rest. Then the same in `README.md`, `docs/*.md` (not `docs/plans`, `docs/releases` or design history) and `plugin/skills/**`. The plugin lint checks every command a skill names against the real tree. The tombstone keeps `ls` in that tree (hidden), so the lint would still accept a missed `fugaro ls`. Add a check to the lint: a skill may not name a hidden command, which also covers `exec` and `image gate`. Test it with `TestLintRefusesHiddenCommands`, a one-line skill fixture that says `fugaro ls`.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 -run 'TestRuns|TestLs|TestSingular|TestRoot|TestDocs|TestRecipesGuide' ./internal/cli/ && go test -count=1 ./plugin/`
Expected: `ok`.

- [ ] **Step 5: Full suite for the group (Task 22), then commit** `git commit -m "generic tool task 7: fugaro runs ls replaces fugaro ls, with a 0.7.0 tombstone and singular aliases"`

---
## Group C: recipes (branch `gt-recipes`)

### Task 8: The format extensions (`internal/recipe`) (G7 to G14)

**Files:**
- Modify: `internal/recipe/recipe.go`
- Create: `internal/recipe/stages.go`, `internal/recipe/stages_test.go`
- Test: `internal/recipe/recipe_test.go`; new corpus files under the recipe corpus directory (`corpus_test.go` names it), each valid or invalid with its expected message

**Interfaces:**
- Produces:
  ```go
  const (
      StepCheck     StepKind = "check"
      MaxUseWhen             = 300
      ModeImplement          = "implement"
      ModeReview             = "review"
  )
  type CheckCommand string // "build", "test", "lint"
  type Step struct {
      Kind      StepKind
      MaxRounds int
      Bounce    bool         // review only: bounce: first_line
      Command   CheckCommand // check only
      Autofix   bool         // check only, with lint
  }
  type Recipe struct {
      Version         int
      Name            string
      Description     string
      UseWhen         string
      ReviewerIsCoder bool
      CoderIsReviewer bool
      Mode            string // ModeImplement or ModeReview, never ""
      Steps           []Step
  }
  // Renamed are catalog names removed in 0.7.0 and what replaced them.
  var Renamed = map[string]string{"cheap-loop-senior": "standard", "claude-solo": "solo"}
  // StageBound is the most stages a run of r can start with first-line
  // rounds f and review rounds n (the knobs), implement included.
  func StageBound(r *Recipe, f, n int) int
  // UsesV07 reports whether r uses a key added in 0.7.0 (the image gate).
  func UsesV07(r *Recipe) bool
  ```
- `reserved` loses `checks` (now refused as "the step type is check, not checks") and keeps `goto`, `on_reject`, `extends`, `on_pass`, `on_fail`, `model` and `models`.

- [ ] **Step 1: Write the failing tests**

In `recipe_test.go`:

```go
func TestExtendedFormatParses(t *testing.T) {
	for name, src := range map[string]string{
		"standard": "version: 1\nname: standard\nuse_when: typical work\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n",
		"premium":  "version: 1\nname: premium\nroles: { coder: reviewer }\nsteps:\n  - review: {}\n",
		"review":   "version: 1\nname: review-only\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n",
		"testfix":  "version: 1\nname: test-and-fix\nsteps:\n  - check: { command: test }\n  - first_line: {}\n  - review: { bounce: first_line }\n",
		"lintfix":  "version: 1\nname: lint-fix\nroles: { reviewer: coder }\nsteps:\n  - check: { command: lint, autofix: true }\n  - review: {}\n",
	} {
		r, ps := Parse([]byte(src))
		if len(ps) != 0 {
			t.Errorf("%s: %s", name, ProblemsText(ps))
			continue
		}
		if !UsesV07(r) {
			t.Errorf("%s: UsesV07 false", name)
		}
	}
}

func TestCheckCommandIsAnEnum(t *testing.T) {
	for _, bad := range []string{"make test", "rm -rf /", "fix", "Test", ""} {
		src := "version: 1\nname: x\nsteps:\n  - check: { command: \"" + bad + "\" }\n  - review: {}\n"
		_, ps := Parse([]byte(src))
		if !strings.Contains(ProblemsText(ps), "steps[0].check.command: must be build, test or lint (it names a commands.* key of fugaro.yaml; a recipe never holds a command)") {
			t.Errorf("command %q: %s", bad, ProblemsText(ps))
		}
	}
	_, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - check: { command: test, autofix: true }\n  - review: {}\n"))
	if !strings.Contains(ProblemsText(ps), "autofix is only for command: lint") {
		t.Errorf("autofix on test: %s", ProblemsText(ps))
	}
}

func TestRecipeNamesNoModelEvenAsRole(t *testing.T) {
	for src, want := range map[string]string{
		"roles: { coder: claude-opus-4-1 }":           "a recipe never names a model",
		"roles: { coder: reviewer, reviewer: coder }": "roles: coder: reviewer and reviewer: coder exclude each other",
		"roles: { background: coder }":                "only the reviewer and coder roles can be mapped",
	} {
		_, ps := Parse([]byte("version: 1\nname: x\n" + src + "\nsteps:\n  - review: {}\n"))
		if !strings.Contains(ProblemsText(ps), want) {
			t.Errorf("%s: %s", src, ProblemsText(ps))
		}
	}
}

func TestBounceNeedsFirstLine(t *testing.T) {
	_, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - review: { bounce: first_line }\n"))
	if !strings.Contains(ProblemsText(ps), "bounce: first_line needs a first_line step before the review") {
		t.Fatal(ProblemsText(ps))
	}
	_, ps = Parse([]byte("version: 1\nname: x\nsteps:\n  - first_line: {}\n  - review: { bounce: review }\n"))
	if !strings.Contains(ProblemsText(ps), "bounce can only be first_line") {
		t.Fatal(ProblemsText(ps))
	}
}

func TestOrderWithChecksAndModes(t *testing.T) {
	for src, want := range map[string]string{
		"steps:\n  - first_line: {}\n  - check: { command: test }\n  - review: {}\n": "check steps must come first",
		"mode: review\nsteps:\n  - first_line: {}\n  - review: {}\n":                "mode: review allows exactly one review step and nothing else",
		"mode: review\nsteps:\n  - review: { max_rounds: 2 }\n":                     "mode: review reviews once: max_rounds must be 1",
		"mode: plan\nsteps:\n  - review: {}\n":                                       "mode must be implement or review",
		"use_when: " + strings.Repeat("x", 301) + "\nsteps:\n  - review: {}\n":       "use_when: must be at most 300 bytes",
	} {
		_, ps := Parse([]byte("version: 1\nname: x\n" + src))
		if !strings.Contains(ProblemsText(ps), want) {
			t.Errorf("%q: got %s", src, ProblemsText(ps))
		}
	}
}
```

In `stages_test.go`:

```go
// TestStageBoundIsFinite: the bound the cost preview and the docs quote,
// for every catalog recipe at the knobs' largest values.
func TestStageBoundIsFinite(t *testing.T) {
	std, _ := Parse([]byte("version: 1\nname: s\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n"))
	if got := StageBound(std, 3, 10); got != 1+2*3+10*(2+2*3) { // 87
		t.Fatalf("standard bound = %d", got)
	}
	solo, _ := Parse([]byte("version: 1\nname: s\nroles: { reviewer: coder }\nsteps:\n  - review: {}\n"))
	if got := StageBound(solo, 3, 2); got != 1+2*2 { // implement, then review+fix per round
		t.Fatalf("solo bound = %d", got)
	}
	ro, _ := Parse([]byte("version: 1\nname: r\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n"))
	if got := StageBound(ro, 3, 10); got != 1 {
		t.Fatalf("review-only bound = %d", got)
	}
}
```

(`StageBound` counts agent stages only. Check steps are runner commands with their own timeout, so they are not model stages.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 ./internal/recipe/`
Expected: compile errors (`UsesV07`, `StageBound` undefined).

- [ ] **Step 3: Implement**

In `recipe.go`'s `top`:
- the cases `"use_when"` (string, at most `MaxUseWhen` bytes) and `"mode"` (`implement` or `review`, default `implement` after the loop);
- `"roles"` now returns `(reviewerIsCoder, coderIsReviewer bool)`.

`roles`:

```go
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, val := v.Content[i], v.Content[i+1]
		path := "roles." + k.Value
		want := map[string]string{"reviewer": "coder", "coder": "reviewer"}[k.Value]
		switch {
		case want == "":
			p.add(path, k.Line, "only the reviewer and coder roles can be mapped (reviewer: coder, or coder: reviewer)")
		case val.Kind == yaml.ScalarNode && val.Tag == "!!str" && val.Value == want:
			if k.Value == "reviewer" {
				revIsCoder = true
			} else {
				coderIsRev = true
			}
		case val.Kind == yaml.ScalarNode && val.Tag == "!!str" && val.Value != "" && val.Value != "coder" && val.Value != "reviewer" && val.Value != "background":
			p.add(path, val.Line, "can only be %s: %s", want, modelMsg)
		default:
			p.add(path, val.Line, "must be %s", want)
		}
	}
	if revIsCoder && coderIsRev {
		p.add("roles", v.Line, "roles: coder: reviewer and reviewer: coder exclude each other")
	}
```

In `steps`:
- `check` is a step kind. Its body is a mapping with `command` (required; `build`, `test` or `lint`, with the exact message of the test) and `autofix` (a bool, only with `lint`).
- `review`'s body accepts `bounce`, a string that must be `first_line`. A body key outside each kind's list goes through `p.key`.
- The "must be one step" message lists the three kinds.

In `order`:
- check steps must all precede every other step;
- `bounce` needs a `first_line` step before the review;
- in `mode: review`, the steps must be exactly one `review`, with `max_rounds` 1 or unset (unset means 1);
- the existing rules stand: at most one `first_line` before the review, and exactly one review, last.

`stages.go`:

```go
func StageBound(r *Recipe, f, n int) int {
	if r.Mode == ModeReview {
		return 1
	}
	first, bounce, rounds := 0, false, n
	for _, s := range r.Steps {
		switch s.Kind {
		case StepFirstLine:
			first = f
			if s.MaxRounds > 0 {
				first = s.MaxRounds
			}
		case StepReview:
			bounce = s.Bounce
			if s.MaxRounds > 0 {
				rounds = s.MaxRounds
			}
		}
	}
	per := 2 // a senior review and its fix
	if bounce {
		per += 2 * first
	}
	return 1 + 2*first + rounds*per
}

func UsesV07(r *Recipe) bool {
	if r.UseWhen != "" || r.CoderIsReviewer || r.Mode == ModeReview {
		return true
	}
	for _, s := range r.Steps {
		if s.Kind == StepCheck || s.Bounce {
			return true
		}
	}
	return false
}
```

Corpus: add one valid file per catalog shape, and one invalid file per new message (each with its `# want:` line in the corpus's existing convention).

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/recipe/`
Expected: `ok`.

**Mutation checks:**
- Accept any string for `check.command`: `TestCheckCommandIsAnEnum` fails.
- Drop `bounce`'s first_line requirement: `TestBounceNeedsFirstLine` fails.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 8: recipes gain use_when, coder: reviewer, mode: review, check steps and bounce"`

### Task 9: `commands.lint`, `commands.fix` and `review.allow_forks` in `fugaro.yaml`

**Depends on:** base-image Group 2 merged (the same files).

**Files:**
- Modify: `internal/config/config.go` (`Commands.Lint`, `Commands.Fix`; a new `Review` block with `AllowForks`), `validate.go`, `scope.go` (rows), `schemas/fugaro.schema.json`, `internal/config/example.yaml`
- Create: `testdata/config/valid/commands-lint-fix.yaml`, `testdata/config/invalid/commands-fix-without-lint.yaml`

**Interfaces:**
- Produces:
  - `Commands.Lint`, `Commands.Fix` (strings, run through `sh -c` like `build` and `test`);
  - `Config.Review.AllowForks bool` (top level, `review: { allow_forks: true }`);
  - scope rows: `commands.lint` and `commands.fix` at repo scope (and profile, as `commands.*` are today: the implementer copies `commands.test`'s row); `review.allow_forks` at **repo scope only**, read from the base branch (like `followup.trusted`).

- [ ] **Step 1: Write the failing tests**

```go
func TestCommandsLintAndFix(t *testing.T) {
	cfg := mustParse(t, "commands:\n  build: make\n  test: make test\n  lint: make lint\n  fix: make fmt\n")
	if cfg.Commands.Lint != "make lint" || cfg.Commands.Fix != "make fmt" {
		t.Fatalf("%+v", cfg.Commands)
	}
	_, err := parse(t, "commands:\n  test: make test\n  fix: make fmt\n")
	if err == nil || !strings.Contains(err.Error(), "commands.fix: needs commands.lint (the check step runs fix, then lint)") {
		t.Fatalf("fix without lint: %v", err)
	}
}

func TestReviewAllowForksIsRepoScopeOnly(t *testing.T) {
	if s := ScopeOf("review.allow_forks"); !s.Repo || s.Project || s.Override {
		t.Fatalf("scope = %+v", s)
	}
}
```

`mustParse`, `parse` and `ScopeOf` stand for `internal/config`'s existing test helpers and the scope table's lookup from layered config Task 1. Use their real names.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestCommandsLintAndFix|TestReviewAllowForks|TestFugaroSchemaCorpus|TestScopeTable' ./internal/config/ ./...`
Expected: FAIL: unknown key `lint`.

- [ ] **Step 3: Implement** the struct fields, the `Validate` rule (fix needs lint), the schema properties (`lint` and `fix` as non-empty strings; `review` as an object with only the boolean `allow_forks`), the scope rows, the two corpus files and the example's commented lines.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/config/ && go test -race -count=1 -run 'TestFugaroSchemaCorpus|TestValidate' ./...`
Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 9: commands.lint, commands.fix and review.allow_forks"`

### Task 10: Bounce-back and `coder: reviewer` in the runner (G8, G9)

**Files:**
- Modify: `internal/runner/plan.go` (`planStep.Bounce`, `FirstRounds`), `runner.go` (`agentLoop`, `seniorLoop`), `recipe.go` (`ApplyRoles`), `internal/runstore/runstore.go` (`ReviewSummary.Bounce int`, `omitempty`)
- Test: `internal/runner/plan_internal_test.go`, `internal/runner/recipe_roles_test.go`, `internal/runner/bounce_test.go` (new)

**Interfaces:**
- `planStep{Kind, Rounds int, Bounce bool, FirstRounds int}`. `FirstRounds` is the plan's first-line rounds, copied onto the review step when it bounces.
- `seniorLoop(ctx, st planStep, tier, reviewPrompt, sys, sessionID)`. The signature takes the step.
- `ApplyRoles(cfg, rcp)` maps `CoderIsReviewer`: the coder's model and its `max_output_tokens` become the reviewer's. This is the mirror of today's `ReviewerIsCoder`, including the run log line and the report note when policy tightened the limit.

- [ ] **Step 1: Write the failing tests**

`bounce_test.go`, with the fake agent's scripted verdicts (as `firstline_test.go` scripts them):

```go
// TestBounceRunsTheCheapLoopAfterARejection: standard with first_line_rounds 1
// and review_rounds 2: implement, first-line(changes)+fix, senior(changes),
// fix, first-line(ship), senior(ship). The second cheap loop is recorded with
// Bounce 1.
func TestBounceRunsTheCheapLoopAfterARejection(t *testing.T) {
	h := newRecipeHarness(t, standardRecipe, agentKnobs{FirstLineRounds: 1, ReviewRounds: 2})
	h.Verdicts("review_first", "changes", "ship")
	h.Verdicts("review", "changes", "ship")
	h.Run(t)
	want := []string{"implement", "review_first", "fix", "review", "fix", "review_first", "review"}
	if got := h.StageNames(); !slices.Equal(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	if rs := h.Record().Reviews; rs[3].Bounce != 1 || rs[3].Tier != runstore.TierFirst {
		t.Fatalf("reviews = %+v", rs)
	}
}

// TestBounceStopsAtReviewRounds: with review_rounds 2 a second senior
// rejection ends the loop: no third cheap loop, and the PR is a draft.
func TestBounceStopsAtReviewRounds(t *testing.T) {
	h := newRecipeHarness(t, standardRecipe, agentKnobs{FirstLineRounds: 1, ReviewRounds: 2})
	h.Verdicts("review_first", "ship", "ship")
	h.Verdicts("review", "changes", "changes")
	h.Run(t)
	if n := h.Count("review"); n != 2 {
		t.Fatalf("senior reviews = %d", n)
	}
	if n := h.Count("review_first"); n != 2 {
		t.Fatalf("cheap reviews = %d, want 2 (one before, one bounce)", n)
	}
	if h.Record().Outcome != runstore.OutcomeDraft {
		t.Fatal("outcome not draft")
	}
}
```

In `recipe_roles_test.go`, add `TestCoderIsReviewerPinsTheReviewerModel`. It mirrors the existing `reviewer: coder` test: the implement stage's gateway pin is the reviewer's model, and the review stage's pin is unchanged.

In `plan_internal_test.go`, add a case: a bounce plan's review step carries `FirstRounds` equal to the first step's rounds, and `Bounce` true.

`newRecipeHarness`, `h.Verdicts`, `h.StageNames` and `h.Count` stand for the harness `recipe_equiv_test.go` and `firstline_test.go` use. Use their real names.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestBounce|TestCoderIsReviewer|TestPlan' ./internal/runner/`
Expected: compile errors, then the stage-order failure.

- [ ] **Step 3: Implement**

`planOf` copies `s.Bounce`. When it sees the review step, it sets `FirstRounds` to the rounds of the `first_line` step it already planned (0 when that step was skipped, under `derived` with first-line off). A bounce with `FirstRounds == 0` behaves like today's loop.

`seniorLoop`, after the fix:

```go
		if sessionID, ok = r.fix(ctx, v, sys, sessionID); !ok {
			return
		}
		if st.Bounce && st.FirstRounds > 0 {
			r.bounce++
			if sessionID, ok = r.firstLine(ctx, st.FirstRounds, reviewPrompt, sys, sessionID); !ok {
				return
			}
		}
```

`firstLine` stamps `sum.Bounce = r.bounce` on each summary it appends. `agentLoop` passes `st` instead of `st.Rounds`. `ApplyRoles` gains the `CoderIsReviewer` branch.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 -run 'TestBounce|TestCoderIsReviewer|TestPlan|TestRecipe|TestFirstLine|TestDefaultRecipeEquivalence' ./internal/runner/`
Expected: `ok`. The equivalence test (the `default` recipe through the old and new loops) must still pass unchanged.

**Mutation check:** run the bounce before the fix. `TestBounceRunsTheCheapLoopAfterARejection`'s stage order then fails.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 10: a senior rejection bounces back to the cheap loop; coder: reviewer"`

### Task 11: The check gate and autofix (G10, G11) — **own review**

**Depends on:** Tasks 8 and 9.

**Files:**
- Modify: `internal/verify/verify.go` (`KindLint`; `Settings.Lint`, written by the runner's `WriteSettings`), `internal/runner/runner.go` (`agentLoop` calls the gate before implement), `internal/runstore/runstore.go` (`Record.Gate string`, `omitempty`), `internal/runner/outcome.go` (a clean gate is `succeeded` with outcome `none`, no PR), `internal/runner/prompts.go` (`GatePrompt`)
- Create: `internal/runner/gate.go`, `internal/runner/gate_test.go`

**Interfaces:**
- Produces:
  ```go
  // gateResult is what the check steps found.
  type gateResult struct {
      Clean    bool   // every check passed and autofix changed nothing
      Fixed    bool   // autofix committed a change and every check then passed
      Failures []verify.Record
      Tails    []string // redacted log tails of the failures, 40 lines each
  }
  func (r *run) runGate(ctx context.Context, steps []recipe.Step) (gateResult, error)
  func GatePrompt(task string, g gateResult) string
  ```
- `verify.KindLint` runs `Settings.Lint`. `fugaro verify lint` becomes valid for the agent too, which the help text and `docs` say.

- [ ] **Step 1: Write the failing tests**

```go
// TestGateCleanOpensNoPR: test-and-fix on a green repository ends at once:
// succeeded, outcome none, gate "clean", no push, no PR, no agent stage.
func TestGateCleanOpensNoPR(t *testing.T) {
	h := newRecipeHarness(t, testAndFixRecipe, agentKnobs{})
	h.Repo.Commands(config.Commands{Test: "true"})
	h.Run(t)
	rec := h.Record()
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone || rec.Gate != "clean" {
		t.Fatalf("record = %+v", rec)
	}
	if len(h.StageNames()) != 0 || h.Provider.PRCount() != 0 || h.Remote.HasBranch("fugaro/"+h.RunID) {
		t.Fatal("a clean gate started a stage, opened a PR or pushed")
	}
}

// TestGateRedRunsImplementWithTheFailure: a failing test reaches the
// implement prompt as untrusted output, and the loop goes on as standard.
func TestGateRedRunsImplementWithTheFailure(t *testing.T) {
	h := newRecipeHarness(t, testAndFixRecipe, agentKnobs{FirstLineRounds: 1, ReviewRounds: 1})
	h.Repo.Commands(config.Commands{Test: "echo FAIL-MARKER; exit 1"})
	h.Run(t)
	p := h.Prompt("implement")
	if !strings.Contains(p, "FAIL-MARKER") || !strings.Contains(p, "untrusted output of commands.test") {
		t.Fatalf("implement prompt = %q", p)
	}
}

// TestGateCommandsComeFromTheRef: the gate runs the fugaro.yaml at the task's
// ref, never anything from the task or the recipe (a project recipe cannot
// carry a command at all, Task 8).
func TestGateCommandsComeFromTheRef(t *testing.T) {
	h := newRecipeHarness(t, testAndFixRecipe, agentKnobs{})
	h.Repo.Commands(config.Commands{Test: "touch " + h.Marker("ran-ref-test")})
	h.Task.Text = "commands.test: touch " + h.Marker("ran-task-text")
	h.Run(t)
	if !h.MarkerExists("ran-ref-test") || h.MarkerExists("ran-task-text") {
		t.Fatal("the gate ran something other than the ref's commands.test")
	}
}

// TestLintAutofixCommitsAndSkipsImplement: lint-fix where fix makes lint
// pass: one autofix commit, no implement stage, the runner's own verify
// test on that commit, then the review.
func TestLintAutofixCommitsAndSkipsImplement(t *testing.T) {
	h := newRecipeHarness(t, lintFixRecipe, agentKnobs{ReviewRounds: 1})
	h.Repo.Commands(config.Commands{Test: "true", Lint: "test -f fixed", Fix: "touch fixed"})
	h.Verdicts("review", "ship")
	h.Run(t)
	if got := h.StageNames(); !slices.Equal(got, []string{"review"}) {
		t.Fatalf("stages = %v", got)
	}
	if h.Remote.Log("fugaro/"+h.RunID)[0] != "fugaro: autofix (commands.fix)" || h.Record().Gate != "fixed" || h.Record().Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", h.Record())
	}
}
```

Plus `TestGateMissingCommandFailsTheRun`. A `check: {command: lint}` on a repository with no `commands.lint` ends the run as `infra_error` at bootstrap, with `the recipe's check step needs commands.lint in fugaro.yaml at <ref>`. It never silently skips.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestGate|TestLintAutofix' ./internal/runner/ && go test -race -count=1 ./internal/verify/`
Expected: compile errors.

- [ ] **Step 3: Implement**

`runGate`:
- For each check step, in order: with `Autofix`, run `Settings.Fix` through `procgroup.Run` (`sh -c`, the agent's environment `r.env`, the verify timeout, the checkout as the directory).
- If `repo.IsClean` is false afterwards, commit everything with the runner's identity as `fugaro: autofix (commands.fix)`, through the same `gitops` commit call finalize uses, hooks off.
- Then call `verify.Run` with the step's kind. A record that is not `Passed` is a failure, and its tail comes from `verify.LogTail`, redacted with `r.redact`.

`agentLoop`, before implement, when the plan starts with checks:

```go
	if g, err := r.runGate(ctx, r.gateSteps); err != nil {
		r.failInfra(ctx, err)
		return
	} else if g.Clean {
		r.rec.Gate = "clean"
		r.endWithoutPR(ctx, "every check passed: nothing to fix")
		return
	} else if g.Fixed {
		r.rec.Gate = "fixed"
		r.verifyOnce(ctx, verify.KindTest)
		// on to the review steps, skipping implement
	} else {
		req.Prompt = GatePrompt(req.Prompt, g)
	}
```

`endWithoutPR` and `verifyOnce` are new and small:
- `endWithoutPR` sets the status `succeeded` and the outcome `none`, writes the report line, and lets finalize run with `skipPR`. Finalize already handles a run without a PR on the halt path; reuse that branch.
- `verifyOnce` runs `verify.Run` as the agent's `fugaro verify` would, so the record is the readiness rule's ordinary input.

`GatePrompt` wraps each tail in the same nonce-delimited block that follow-up comments use, introduced as `untrusted output of commands.<kind>` (prompts.go has the helper).

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/verify/ && go test -race -count=1 -run 'TestGate|TestLintAutofix|TestOutcome|TestRecipe|TestBounce' ./internal/runner/`
Expected: `ok`.

**Mutation checks:**
- Treat `Fixed` like `Clean`: `TestLintAutofixCommitsAndSkipsImplement` fails, because there is no review and no PR.
- Skip the redaction of tails: add a secret to the failing command's output in `TestGateRedRunsImplementWithTheFailure`, which must then fail.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 11: check steps gate the run; lint autofix commits before the review"`

### Task 12: Review mode and `pick` (G12, G13) — **own review**

**Depends on:** Tasks 8 and 9.

**Files:**
- Modify:
  - `internal/task/task.go` (`ReviewPRs []int`, valid only with a review-mode recipe; 1 to 4 entries; strict);
  - `internal/gitprov/gitprov.go` (`FetchPRHead(ctx, n) (PRHead, error)`, where `PRHead{SHA string; Fork bool; Open bool}`), with implementations in `github/` (`refs/pull/N/head`, `head.repo.fork`, or head repo ≠ base repo), `bitbucket/` (`source.repository` ≠ destination) and `fake/`;
  - `internal/runner/runner.go` (bootstrap branches to review mode), `outcome.go` (`OutcomeReviewed`), `prompts.go` (`PickPrompt`), `agentenv.go` (no git variables in review mode);
  - `internal/cli/run.go` (`--pr` with a review-mode recipe targets any open PR, repeatable for `pick`).
- Create: `internal/runner/reviewmode.go`, `internal/runner/reviewmode_test.go`, `internal/cli/run_review_test.go`

**Interfaces:**
- `func (r *run) reviewMode(ctx context.Context)`: fetch each head, check fork policy, run one `review` stage (for `pick`, one stage that sees every head in its own directory), post the comment(s), record.
- `runstore.Record.Reviewed []ReviewedPR{Number int; SHA string; Verdict string}`.

- [ ] **Step 1: Write the failing tests**

```go
func TestReviewModeNeverPushes(t *testing.T) {
	h := newRecipeHarness(t, reviewOnlyRecipe, agentKnobs{})
	pr := h.Provider.OpenPR(t, "feature", "someone-elses-change") // a PR Fugaro did not open
	h.Task.ReviewPRs = []int{pr}
	h.Verdicts("review", "changes")
	h.Run(t)
	if h.Remote.PushCount() != 0 {
		t.Fatal("review mode pushed")
	}
	c := h.Provider.Comments(pr)
	if len(c) != 1 || !strings.Contains(c[0], "Fugaro review (review-only): changes") {
		t.Fatalf("comments = %q", c)
	}
	if h.Record().Outcome != runstore.OutcomeReviewed {
		t.Fatalf("outcome = %s", h.Record().Outcome)
	}
}

func TestReviewModeAgentHasNoGitToken(t *testing.T) {
	h := newRecipeHarness(t, reviewOnlyRecipe, agentKnobs{})
	h.Task.ReviewPRs = []int{h.Provider.OpenPR(t, "feature", "x")}
	h.Run(t)
	env := h.AgentEnv("review")
	for _, k := range []string{"GH_TOKEN", "FUGARO_GIT_TOKEN", "GIT_CONFIG_COUNT"} {
		if _, ok := env[k]; ok {
			t.Errorf("review-mode agent has %s", k)
		}
	}
}

func TestReviewModeRefusesForkWithoutOptIn(t *testing.T) {
	h := newRecipeHarness(t, reviewOnlyRecipe, agentKnobs{})
	h.Task.ReviewPRs = []int{h.Provider.OpenForkPR(t, "outsider/repo", "x")}
	h.Run(t)
	if h.Record().Status != runstore.StatusInfraError || !strings.Contains(h.Record().Error, "review.allow_forks") || len(h.StageNames()) != 0 {
		t.Fatalf("record = %+v", h.Record())
	}
	h2 := newRecipeHarness(t, reviewOnlyRecipe, agentKnobs{})
	h2.Repo.SetBaseYAML("review:\n  allow_forks: true\n")
	h2.Task.ReviewPRs = []int{h2.Provider.OpenForkPR(t, "outsider/repo", "x")}
	h2.Run(t)
	if h2.Record().Outcome != runstore.OutcomeReviewed {
		t.Fatal("allow_forks did not allow it")
	}
}

func TestReviewModeLeavesDraftState(t *testing.T) {
	h := newRecipeHarness(t, reviewOnlyRecipe, agentKnobs{})
	pr := h.Provider.OpenPR(t, "feature", "x")
	h.Provider.SetDraft(pr, true)
	h.Task.ReviewPRs = []int{pr}
	h.Verdicts("review", "ship")
	h.Run(t)
	if !h.Provider.IsDraft(pr) || h.Provider.UpdateCount(pr) != 0 {
		t.Fatal("review mode changed the PR")
	}
}

func TestPickCommentsOnEveryPR(t *testing.T) {
	h := newRecipeHarness(t, pickRecipe, agentKnobs{})
	a, b := h.Provider.OpenPR(t, "a", "x"), h.Provider.OpenPR(t, "b", "x")
	h.Task.ReviewPRs = []int{a, b}
	h.PickVerdict(b) // the fake reviewer's structured output names b
	h.Run(t)
	for _, n := range []int{a, b} {
		if c := h.Provider.Comments(n); len(c) != 1 || !strings.Contains(c[0], fmt.Sprintf("keep #%d", b)) {
			t.Fatalf("PR %d comments = %q", n, c)
		}
	}
}
```

In `internal/cli/run_review_test.go`:
- `fugaro run --recipe review-only --pr 7` writes `review_prs: [7]` in `task.json` and no branch.
- `--pr 7 --pr 8` without a review-mode recipe is refused with `several --pr need a review-mode recipe such as pick`.
- Five `--pr` are refused (`at most 4`).
- `--recipe review-only` without `--pr` is refused.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 -run 'TestReviewMode|TestPick' ./internal/runner/ && go test -race -count=1 -run 'TestRunReview' ./internal/cli/ && go test -race -count=1 ./internal/gitprov/... ./internal/task/`
Expected: compile errors.

- [ ] **Step 3: Implement**

Bootstrap: when the resolved recipe's `Mode == ModeReview`:
- skip `CheckoutNewBranch`;
- for each `ReviewPRs` entry, `FetchPRHead`. A closed PR, a fork without `review.allow_forks` read **from the base branch** (`readBaseConfig`, as follow-ups do) or a fetch failure ends the run `infra_error` with the reason;
- check out the head detached (a `pick` checks out each head into `/work/review/<n>` with `git worktree add --detach`);
- leave `r.env` without the git variables, exactly as the follow-up branch does at `runner.go:953`.

`reviewMode` runs one `review` stage with `ReviewPrompt`, or `PickPrompt` listing the directories. The verdict schema gains an optional `keep` (a PR number) for `pick`.

The comment:
- header `Fugaro review (<recipe>): <verdict>`;
- the findings as a list;
- for `pick`, `keep #N` and one line per other PR;
- the run ID.

It is redacted, capped at 60 KiB through the existing PR-body clipper, and posted with `Provider.Comment` once per PR. A comment failure is logged and recorded (`Reviewed[i].Verdict` stays set, and the report says the comment failed), and never retried in a loop.

The outcome is `reviewed`, the status `succeeded`. There is no finalize push, no `EnsurePR` and no status section.

- [ ] **Step 4: Run the tests**

Run: the Step 2 commands, then `go test -race -count=1 -run 'TestFollowUp|TestOutcome' ./internal/runner/`
Expected: `ok`.

**Mutation checks:**
- Read `allow_forks` from the PR's head instead of the base: add a case where the fork's own `fugaro.yaml` sets it, which must still be refused.
- Let the git variables through: `TestReviewModeAgentHasNoGitToken` fails.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 12: review-only and pick review existing PRs, comment, and never push"`

### Task 13: `fugaro run --attempts N` (G13)

**Depends on:** Task 12 (the `pick` follow-on is documented with it).

**Files:**
- Modify: `internal/cli/run.go`
- Create: `internal/cli/run_attempts_test.go`

**Interfaces:**
- `--attempts N` (2 to 4) launches N runs of the same task, workflow, ref and recipe, sequentially through the existing launch path (each claim, `task.json` and launch independent), under the batch label `bestof-<first run id>` unless `--batch` is given.
- It prints each run's line as `run` does, then `pick: fugaro run --recipe pick --pr <A> --pr <B> … once all have opened PRs (fugaro runs ls --batch <label>)`.
- `--json` gives `{"batch": label, "runs": [ ... ]}`.
- It is refused together with `--pr`, `--run-id`, `--retry` or a review-mode recipe.

- [ ] **Step 1: Write the failing test**

```go
func TestRunAttemptsLaunchesNWithOneBatch(t *testing.T) {
	f := newCloudFixture(t)
	out, _, err := execute(t, "run", "--attempts", "3", "--json", "fix the flaky test")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Batch string `json:"batch"`
		Runs  []struct{ RunID string `json:"run_id"` } `json:"runs"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil || len(doc.Runs) != 3 || !strings.HasPrefix(doc.Batch, "bestof-") {
		t.Fatalf("out = %s", out)
	}
	if n := f.run.LaunchCount(); n != 3 {
		t.Fatalf("launches = %d", n)
	}
	for _, bad := range [][]string{{"--attempts", "1"}, {"--attempts", "5"}, {"--attempts", "2", "--pr", "4"}, {"--attempts", "2", "--recipe", "review-only"}} {
		if _, _, err := execute(t, append([]string{"run"}, append(bad, "x")...)...); ExitCode(err) != ExitUserError {
			t.Errorf("%v: %v", bad, err)
		}
	}
}
```

Also check `max_parallel`: with `max_parallel: 2` in the local config, `--attempts 3` is refused before any launch, with the existing parallel-cap message plus `(--attempts 3 needs 3)`. A batch must never launch partially because of the cap.

- [ ] **Step 2: Run it to see it fail**

Run: `go test -race -count=1 -run 'TestRunAttempts' ./internal/cli/`
Expected: FAIL: unknown flag.

- [ ] **Step 3: Implement** the flag, the checks (the cap check counts N against the running total first) and a loop over the existing single-launch function. A failure after k launches stops the loop, prints the k launched runs and returns the error. Launched runs are never cancelled behind the user's back.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 -run 'TestRunAttempts|TestRun|TestMaxParallel' ./internal/cli/`
Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 13: fugaro run --attempts launches a best-of-N batch"`

### Task 14: The catalog, `recipes ls --verbose`, the 0.7.0 gate and `docs/recipes.md` (G14, G15)

**Depends on:** Tasks 8 and 10 to 13.

**Files:**
- Modify: `internal/recipe/catalog/` (delete `cheap-loop-senior.yaml` and `claude-solo.yaml`; add `solo.yaml`, `standard.yaml`, `premium.yaml`, `review-only.yaml`, `pick.yaml`, `test-and-fix.yaml` and `lint-fix.yaml`; `default.yaml` unchanged), `internal/recipe/catalog_test.go`, `internal/cli/recipes.go` (`--verbose`; the renamed message in resolution), `internal/cli/recipes_resolve.go`, `internal/cli/recipes_skew.go` (`recipesV07Since`), `docs/recipes.md`, `internal/cli/docs_recipes_test.go`, `plugin/skills/working/reference/launch.md`

- [ ] **Step 1: Write the failing tests**

```go
func TestCatalogIsTheStarterSet(t *testing.T) {
	want := []string{"default", "lint-fix", "pick", "premium", "review-only", "solo", "standard", "test-and-fix"}
	if got := CatalogNames(); !slices.Equal(got, want) {
		t.Fatalf("catalog = %v", got)
	}
	for _, n := range want {
		data, _ := CatalogText(n)
		r, ps := Parse(data)
		if len(ps) != 0 {
			t.Fatalf("%s: %s", n, ProblemsText(ps))
		}
		if n != DefaultName && r.UseWhen == "" {
			t.Errorf("%s has no use_when", n)
		}
		for _, s := range r.Steps {
			if s.MaxRounds != 0 && r.Mode != ModeReview {
				t.Errorf("%s pins max_rounds: catalog recipes take the knobs (G6)", n)
			}
		}
	}
}
```

In the CLI:
- `TestRenamedRecipeNamesTheReplacement`: `fugaro run --recipe claude-solo x` fails with `claude-solo was renamed in 0.7.0: use solo`. So does an `agent.recipe: cheap-loop-senior` in a checkout, at `fugaro validate`. A repository or project recipe that itself defines one of those names still wins: the rename applies only when the catalog would have been the match.
- `TestRecipesLsVerbose`: `fugaro recipes ls --verbose` prints, for each recipe, `use when:` and `steps:` lines. `--json` always carries `use_when` and `steps`.
- `TestRecipeV07NeedsA07Image`: the skew check refuses a `UsesV07` recipe on a build record whose `base_ref` predates 0.7.0, naming `fugaro image refresh`. It has the same shape as the 0.5.0 gate's test.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -race -count=1 ./internal/recipe/ && go test -race -count=1 -run 'TestRenamedRecipe|TestRecipesLs|TestRecipeV07|TestRecipesGuide' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Implement**

The catalog texts follow the design's §3.5 table. Each has a `description` (at most 200 bytes) and a `use_when` written for the routing skill, for example:

```yaml
# internal/recipe/catalog/solo.yaml
version: 1
name: solo
description: One model codes and reviews its own work in a fresh session.
use_when: Small, low-risk changes confined to one or two files, where a second model's review would cost more than it finds.
roles:
  reviewer: coder
steps:
  - review: {}
```

```yaml
# internal/recipe/catalog/standard.yaml
version: 1
name: standard
description: The cheap coder loops review and fix, then the senior reviews; a rejection goes back to the cheap loop.
use_when: Typical feature or bug-fix work that needs a real review. Choose it when nothing more specific fits.
steps:
  - first_line: {}
  - review:
      bounce: first_line
```

```yaml
# internal/recipe/catalog/premium.yaml
version: 1
name: premium
description: The reviewer's (strong) model does the whole loop; no cheap stage.
use_when: High-stakes or subtle work (concurrency, security, data migrations) where a cheap model's attempt is likely to waste the review.
roles:
  coder: reviewer
steps:
  - review: {}
```

```yaml
# internal/recipe/catalog/review-only.yaml
version: 1
name: review-only
description: No coding. The senior model reviews an existing pull request and comments on it.
use_when: Reviewing a human-written or external pull request. Launch with --pr N.
mode: review
steps:
  - review:
      max_rounds: 1
```

```yaml
# internal/recipe/catalog/pick.yaml
version: 1
name: pick
description: No coding. The senior model compares several pull requests for the same task and says which to keep.
use_when: After fugaro run --attempts N, once every attempt has a pull request. Launch with one --pr per attempt.
mode: review
steps:
  - review:
      max_rounds: 1
```

```yaml
# internal/recipe/catalog/test-and-fix.yaml
version: 1
name: test-and-fix
description: Runs the tests first; only when they fail does the cheap loop fix them, with a senior review.
use_when: Keeping a branch green, chasing a failing or flaky test, CI babysitting. A green run ends with no pull request.
steps:
  - check:
      command: test
  - first_line: {}
  - review:
      bounce: first_line
```

```yaml
# internal/recipe/catalog/lint-fix.yaml
version: 1
name: lint-fix
description: Runs the repository's autofix and linter first; a model fixes only what the tools could not.
use_when: Routine mechanical cleanup (formatting, lint and type-check errors) in a repository with commands.lint and commands.fix.
roles:
  reviewer: coder
steps:
  - check:
      command: lint
      autofix: true
  - review: {}
```

Resolution: before the catalog lookup, if `recipe.Renamed[name]` is set, return the renamed error. In `recipes ls --verbose`, the extra lines come from the parsed recipe. In `recipes_skew.go`, add `recipesV07Since = "0.7.0"` and check it when `recipe.UsesV07` is true.

`docs/recipes.md` is rewritten around the starter catalog:
- the "what is refused" section loses `checks` and gains the `check.command` enum;
- a "Loop knobs" section names `agent.first_line_rounds` and `agent.review_rounds` and the bounce arithmetic;
- review mode and `--attempts` are documented with their safety notes (fork PRs, `review.allow_forks`);
- the 0.7.0 rollout notes cover the renamed recipes and the image gate.

`docs_recipes_test.go` gains the new required phrases (`use_when`, `--attempts`, `review.allow_forks`, `commands.lint`, `0.7.0`). `launch.md` drops "use `--recipe` only when the user asks" in favour of "the routing skill chooses the recipe".

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/recipe/ && go test -race -count=1 -run 'TestRecipe|TestRenamed|TestRun' ./internal/cli/ && go test -count=1 ./plugin/`
Expected: `ok`.

- [ ] **Step 5: Full suite for Group C (Task 22), then commit** `git commit -m "generic tool task 14: the starter recipe catalog, recipes ls --verbose and the 0.7.0 recipe gate"`

---

## Group D: repository provisioning (branch `gt-checkout`)

### Task 15: `workflows.<n>.checkout` and its refusals (G3, G5)

**Depends on:** Task 9 merged (the same config files), base-image Group 2.

**Files:** `internal/config/config.go` (`Workflow.Checkout`, default `baked`), `validate.go`, `scope.go`, schema, `testdata/config/valid/checkout-clone.yaml`, `testdata/config/invalid/checkout-clone-apt.yaml`, `checkout-clone-dockerfile.yaml`.

- [ ] **Step 1: Write the failing test**

```go
func TestCloneRefusesImageBuildKeys(t *testing.T) {
	for key, src := range map[string]string{
		"image.apt":   "image:\n      apt: [jq]\n",
		"image.setup": "image:\n      setup: [\"make deps\"]\n",
		"image.tools": "image:\n      tools: { node: \"24\" }\n",
		"dockerfile":  "dockerfile: .fugaro/web.Dockerfile\n",
	} {
		_, err := parse(t, "workflows:\n  web:\n    checkout: clone\n    "+src)
		if err == nil || !strings.Contains(err.Error(), "workflows.web."+key+": checkout: clone runs the base image with no build; use checkout: baked to build an image") {
			t.Errorf("%s: %v", key, err)
		}
	}
	cfg := mustParse(t, "workflows:\n  web:\n    commands: { test: make test }\n")
	if cfg.Workflows["web"].Checkout != "baked" {
		t.Fatal("default is not baked")
	}
}
```

With the G5 veto (the spec's default), the default flips to `clone`, and the test's last case expects `clone` plus a refusal of `image:` without an explicit `checkout: baked`.

- [ ] **Step 2: Run it to see it fail**

Run: `go test -race -count=1 -run 'TestCloneRefuses|TestScopeTable|TestFugaroSchemaCorpus' ./internal/config/ ./...`
Expected: FAIL.

- [ ] **Step 3: Implement**
- The field is the string `checkout` with values `baked` and `clone`.
- The rule above, plus `image.skip_build_scripts`.
- The scope row: repo and project (through a profile), never override.
- The schema enum.
- The corpus files.

- [ ] **Step 4: Run the tests** (the Step 2 command). Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 15: workflows.<n>.checkout: baked or clone"`

### Task 16: A clone workflow's job runs the base image; no build and no check job — **own review**

**Depends on:** Task 15, base-image Group 4 merged (`base_images.base` is the only kind).

**Files:**
- `internal/infra/spec.go`: the workflow's job image is `base_images.base` for `clone`, not the derived image's `:latest`;
- `internal/cli/init_repo*.go`: no registry write is needed for a clone-only repository, and no check job is created when every workflow is `clone`;
- `internal/cli/image.go`: `image build` refuses a clone workflow, `this workflow runs the base image (checkout: clone): there is nothing to build`;
- `image_refresh.go`: refresh updates a clone workflow's job image to the new base, with no build;
- `imagecheck.go`: the check job skips clone workflows;
- tests next to each.

- [ ] **Step 1: Write the failing tests**
  - `TestSpecCloneWorkflowRunsTheBase`: `infra.Spec` for a repository with `web: {checkout: clone}` gives `web`'s job image as the local config's `base_images.base`, and the check job's spec lists no `clone` workflow.
  - `TestImageBuildRefusesClone`.
  - `TestRefreshMovesCloneJobsToTheNewBase`: on the refresh rig, a clone workflow gets a job-image update and no Cloud Build submission (`fakeSteps` records none).

- [ ] **Step 2: Run them to see them fail**: `go test -race -count=1 -run 'TestSpecClone|TestImageBuildRefusesClone|TestRefreshMovesClone' ./internal/infra/ ./internal/cli/`

- [ ] **Step 3: Implement** as listed. **No IAM change.** If the Terraform plan shows the workflow job's service agent needs `artifactregistry.reader` on the `fugaro-base` registry (Check 32 decides it), the grant is handed to the bucket-IAM plan's owner as a note in this PR's body, and this task's Terraform stays unchanged.

- [ ] **Step 4: Run the tests**: the Step 2 command plus `go test -count=1 ./deploy/...` (the Terraform golden plans, if the repository has them for `workflow`). Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 16: clone workflows run the base image with no build and no check job"`

### Task 17: The runner's blobless clone and the `provision` stage (G4)

**Depends on:** Task 15. It can run in parallel with Task 16.

**Files:** `internal/gitops/gitops.go` (`CloneBlobless(ctx, dir, remote, branch string, env []string, strip ...string)`), `internal/runner/runner.go` (bootstrap: an empty `/work/repo` and `FUGARO_CHECKOUT=clone` mean clone), `internal/runner/provision.go` (new: `mise install` when a root mise config exists), `runstore.StageTiming` for `provision`, tests.

- [ ] **Step 1: Write the failing tests**

```go
func TestCloneBloblessKeepsHistory(t *testing.T) {
	remote := testutil.GitRemoteWithCommits(t, 3)
	dir := filepath.Join(t.TempDir(), "repo")
	r, err := gitops.CloneBlobless(context.Background(), dir, remote, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := r.MustCount(t, "rev-list", "--count", "HEAD"); n != "3" {
		t.Fatalf("history = %s commits, want 3 (not shallow)", n)
	}
	if v := r.MustGit(t, "config", "remote.origin.partialclonefilter"); v != "blob:none" {
		t.Fatalf("filter = %q", v)
	}
}

func TestCloneProvisionRunsMiseInstall(t *testing.T) {
	h := newRunHarness(t, runOptions{Checkout: "clone"})
	h.Repo.WriteFile("mise.toml", "[tools]\nnode = \"24\"\n")
	h.FakeMise(t) // a mise on PATH that records its argv and cwd
	h.Run(t)
	if got := h.MiseCalls(); len(got) != 1 || got[0].Args[0] != "install" || got[0].Dir != h.WorkDir {
		t.Fatalf("mise calls = %+v", got)
	}
	if h.Record().StageTiming("provision") == nil {
		t.Fatal("no provision timing")
	}
}
```

Plus a case where there is no mise config: no `mise` call and no `provision` timing. And a case where `mise install` fails: the run ends `infra_error` with mise's last line, redacted.

- [ ] **Step 2: Run them to see them fail**: `go test -race -count=1 -run 'TestCloneBlobless' ./internal/gitops/ && go test -race -count=1 -run 'TestCloneProvision' ./internal/runner/`

- [ ] **Step 3: Implement**
- `CloneBlobless` runs `git clone --quiet --filter=blob:none --single-branch --branch <branch> -- <remote> <dir>`, with `noHooks` and the credential variables, as `OpenOrClone` does.
- Bootstrap uses it when the checkout directory is empty and the job's environment says `FUGARO_CHECKOUT=clone`. `internal/infra` sets that variable for clone workflows: add it to Task 16's spec change if it is not there.
- `provision.go` runs `mise install` (the same flags as the derived template) as the runner's user, with the runner's environment minus model credentials and with `GITHUB_TOKEN` set to the run's git token for mise's rate limit, through `procgroup.Run` with a 10-minute bound. It runs only when one of `config.MiseConfigPaths` (base-image Task 9) exists.

- [ ] **Step 4: Run the tests**: the Step 2 commands plus `go test -race -count=1 -run 'TestBootstrap|TestFollowUp' ./internal/runner/`. Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 17: a clone workflow clones blobless at start and installs its mise tools"`

### Task 18: Check 32 and the setup skill's offer

**Depends on:** Tasks 16 and 17, and base-image Task 16 (the setup skill's 0.7.0 rewrite).

**Files:** `docs/gcp-live-checklist.md` (Check 32), `plugin/skills/setup/reference/decisions.md`, `plugin/skills/setup/SKILL.md` (one line), `plugin/setup_skill_test.go`.

- [ ] **Step 1: Write the failing test.** `TestSetupSkillOffersClone`:
  - `decisions.md` has a "Checkout" topic that names `checkout: clone` and `checkout: baked`;
  - it says clone needs no system package and no Dockerfile;
  - it says the default is baked.

- [ ] **Step 2: Run it to see it fail**: `go test -count=1 -run 'TestSetupSkillOffersClone' ./plugin/`

- [ ] **Step 3: Write**

Check 32, "clone workflows (sandbox only, run by you; USER-RUN, NOT RUN)", in the format of Checks 29 and 30:
- **the setup:** a sandbox workflow switched to `checkout: clone` on a branch, merged in the sandbox repository, `fugaro init --repo` in your terminal;
- **step 1:** one run, which records the execution start, the `provision` stage's duration and the first stage's start;
- **step 2:** the job's image as `gcloud run jobs describe` shows it;
- **step 3:** whether the image pull needed a grant. If the execution fails with a pull permission error, record it, revert the branch, and hand the error to the bucket-IAM plan;
- **FACT lines** to record, the **paste-back** list and the **restore** steps.

Never run it against EdgeWeb or EdgeServer.

The skill topic: what clone gives (no image build, no daily check, start-up pays the clone and `mise install`), when to offer it (no `image.apt`, no services, no Dockerfile), and the rule that it is the user's decision.

- [ ] **Step 4: Run the tests**: `go test -count=1 ./plugin/`. Expected: `ok`.

- [ ] **Step 5: Full suite for Group D (Task 22), commit** `git commit -m "generic tool task 18: live check 32 and the setup skill offers checkout: clone"`

---
## Group E: skills and docs (branch `gt-docs`; Task 21 may go first, as Group S)

### Task 19: The routing skill chooses the recipe (G16)

**Depends on:** Group C merged (it names the catalog and `--attempts`), and upgrade Task 9 (the skill set of five) merged.

**Files:** `plugin/skills/routing/SKILL.md`, `plugin/skills/parallelism/SKILL.md` (the "Redundant attempts" paragraph names `--attempts` and `pick`), `plugin/skills/working/reference/launch.md`, `plugin/routing_skill_test.go` (new).

- [ ] **Step 1: Write the failing test**

```go
// TestRoutingSkillChoosesRecipes (design generic-tool §4): the skill reads
// the catalog from the CLI, matches use_when, states its choice for one
// task, summarises a batch, never blocks a batch, and honours a pinned
// recipe and agent.recipe.
func TestRoutingSkillChoosesRecipes(t *testing.T) {
	data, err := os.ReadFile("skills/routing/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"## Choosing a recipe",
		"fugaro recipes ls --json",
		"use_when",
		"one line",                 // the single-task statement
		"never wait for approval",  // batches
		"12 standard, 2 solo",      // the summary's example
		"agent.recipe",             // policy default respected
		"the user named",           // a pinned recipe wins
	} {
		if !strings.Contains(s, want) {
			t.Errorf("routing skill never says %q", want)
		}
	}
	for _, name := range []string{"standard", "solo", "premium", "review-only", "test-and-fix", "lint-fix"} {
		if strings.Contains(s, "`"+name+"`") {
			t.Errorf("routing skill hard-codes the recipe %s: it must read the catalog, not a decision tree", name)
		}
	}
}
```

The negative loop is deliberate. The example summary line may name recipes in prose ("12 standard, 2 solo"), but no back-quoted catalog names may appear, so the skill cannot grow a hard-coded table that drifts from the catalog.

- [ ] **Step 2: Run it to see it fail**: `go test -count=1 -run 'TestRoutingSkillChoosesRecipes' ./plugin/`

- [ ] **Step 3: Write the section** (about 25 lines, inside the lint's budgets):
- run `fugaro recipes ls --json` once per session;
- match the task against each recipe's `use_when`, falling back to `description`;
- a recipe the user named wins;
- when nothing is clearly better than the repository's `agent.recipe`, pass no `--recipe`;
- for one task, say the choice and reason in one line before launching;
- for a batch, choose per task, never wait for approval, and end with one summary line such as "15 runs: 12 standard, 2 solo, 1 best-of-3";
- for a hard, well-specified task, suggest `--attempts` and then `pick` (and say it costs N times);
- review mode needs `--pr`.

In `launch.md`, the recipe paragraph points at the routing skill. The skill headers keep the version banner that `scripts/bump-plugin-version.sh` rewrites.

- [ ] **Step 4: Run the tests**: `go test -count=1 ./plugin/`. Expected: `ok`, including the lint, which checks `--json`, `--attempts`, `--pr` and `--recipe` against the real command tree.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 19: the routing skill chooses the recipe from the catalog"`

### Task 20: Direct providers, Check 33 and the control-plane seam (G18, G26)

**Files:** `docs/multi-model.md`, `docs/gcp-live-checklist.md` (Check 33), `docs/backends.md`, `internal/cli/docs_providers_test.go` (new).

- [ ] **Step 1: Write the failing test.** `TestMultiModelNamesDirectProviders`:
  - `docs/multi-model.md` has a section `## Direct to the vendor`, with a `providers:` example whose `base_url` is not `openrouter.ai`;
  - it states that providers may not overlap and that Claude models never go through a provider;
  - it names Check 33.

  `TestBackendsNamesTheControlPlaneSeam`: `docs/backends.md` has `## The control plane seam` and names `internal/budget`, `internal/rtdb`, `internal/firestore` and `internal/watch`.

- [ ] **Step 2: Run them to see them fail**: `go test -count=1 -run 'TestMultiModelNames|TestBackendsNames' ./internal/cli/`

- [ ] **Step 3: Write**

`multi-model.md`, "Direct to the vendor":
- the same `providers:` shape with the vendor's Anthropic-compatible `base_url`, auth and secret;
- "the provider entry that claims a model is its route: to switch a model family between OpenRouter and its vendor, move its pattern from one entry to the other and run `fugaro init --repo`";
- the account-side settings (no fallbacks and the data policy are OpenRouter's; a vendor has its own);
- "unverified until Check 33".

Check 33, "a vendor's own endpoint (sandbox only, run by you; optional; USER-RUN, NOT RUN)": one run with the coder on the vendor's endpoint, then the gateway's `priced_as`, the settled charge and the vendor's reported usage as FACT lines, and the restore.

`backends.md`, "The control plane seam", following the design's §11.

- [ ] **Step 4: Run the tests**: the Step 2 command. Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 20: direct providers, check 33 and the control plane seam"`

### Task 21: SECURITY.md: the trust model and its known limits (G21)

**Independent:** it can run first (Group S). Two parts of the text below describe features of this plan that may not have merged yet:
- the `review-only` and `pick` bullet, with its `review.allow_forks` phrase (Task 12);
- the dashboard's "last action" bullet (Task 3).

If Task 21 runs before them, it leaves those two bullets out and drops `"review.allow_forks"` from the test's list. Task 12 then adds its bullet and that phrase to the test, and Task 3 adds its bullet, each in its own PR. So SECURITY.md never describes code that is not on `main`.

**Files:** `SECURITY.md`, `internal/cli/docs_security_test.go` (new).

- [ ] **Step 1: Write the failing test**

```go
// TestSecurityMdStatesTheModelAndItsLimits (design generic-tool §9): the
// trust model is in SECURITY.md itself, names each known gap, and says what
// the code does, not what it hopes.
func TestSecurityMdStatesTheModelAndItsLimits(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"## Trust model and known limits",
		"you trust the repository and the source of the task",
		"egress is not restricted",
		"GH_TOKEN",
		"/proc",
		"GitHub App's private key",
		"branch protection",
		"credit limit",
		"fugaro verify",
		"review.allow_forks",
		"followup.trusted",
		"docs/design/v1.md",
		"docs/design/bucket-iam.md",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SECURITY.md never says %q", want)
		}
	}
	for _, banned := range []string{"fully isolated", "secure by design", "cannot leak", "guarantees"} {
		if strings.Contains(strings.ToLower(s), banned) {
			t.Errorf("SECURITY.md says %q: state the model and its limits, not a claim", banned)
		}
	}
	// Every environment variable it names as reaching the agent is real.
	for _, v := range regexp.MustCompile("`([A-Z][A-Z0-9_]{2,})`").FindAllStringSubmatch(s, -1) {
		if !knownEnvName(v[1]) {
			t.Errorf("SECURITY.md names %s, which the code never sets or passes", v[1])
		}
	}
}
```

`knownEnvName` is a small helper in the test. It returns true for names that appear in `internal/agent/env.go`, `internal/runner/*.go`, `internal/gitops/credentials.go`, `internal/gitprov/github/github.go` or `deploy/terraform`, found by reading those files. So a renamed variable makes the doc test fail.

- [ ] **Step 2: Run it to see it fail**: `go test -count=1 -run 'TestSecurityMdStates' ./internal/cli/`

- [ ] **Step 3: Write the section.** Insert it before "## Scope", and keep the rest of the file. The full text, the deliverable:

```markdown
## Trust model and known limits

Fugaro runs a coding agent with its permission prompts turned off, in a fresh cloud container per run, and ends every run with a pull request. This section says what that design protects, what it does not, and what you should do about the gaps. It describes the code as it is; the details, with file references, are in [docs/design/v1.md §6](docs/design/v1.md#6-security-model).

### The assumption

**You trust the repository and the source of the task.** Everyone who can push to the repository, everyone who can launch a run, and (for follow-up runs) the people listed in `followup.trusted` can steer an agent that holds the credentials below. Fugaro is built for a team running it on its own code. It is not a sandbox for code you would not run on a developer's laptop.

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
- **The dashboard shows each run's last action** (a redacted command line or file path) to everyone who can read the budget database: your launchers.

### Running Fugaro on repositories with external contributors

- Do not launch runs whose task text you copied from an untrusted issue or comment without reading it: the task is an instruction the agent follows.
- Keep `followup.trusted` to people with write access.
- `review-only` and `pick` refuse a pull request from a fork unless the base branch sets `review.allow_forks: true`. A review runs no build by default, but the agent can run the fork's code, next to the workflow's secrets. Turn it on only for repositories whose job secrets you would hand to that contributor.
- Never run a fork's pull request through an implementing recipe: its build scripts run with the workflow's secrets.
- Give Fugaro its own provider keys with credit limits, and its own GitHub App, installed only on the repositories it serves.
- Public repositories are refused for follow-ups unless `followup.allow_public` is set, for the same reason.
```

Before committing, the implementer re-reads each claim against the code at that commit. `internal/agent/env.go`, `internal/runner/runner.go` (the agent env), `internal/gitprov/github/apptoken.go` (permissions), `deploy/terraform/gcp/modules/workflow/job.tf` (mounts) and `docs/design/v1.md` §6 are the sources. Any sentence that is no longer true is fixed, not kept. If `docs/design/bucket-iam.md` has merged by then, the two bucket sentences are made to match it exactly. If it has not, they stay as written and the bucket-IAM plan's tasks update them.

- [ ] **Step 4: Run the tests**: `go test -count=1 -run 'TestSecurityMd|TestReadmeNamesNoRetiredSkill' ./internal/cli/ ./plugin/`. Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 21: SECURITY.md states the trust model and its known limits"`

---

## Task 22: Each group's full suite and its PR

Run once at the end of each group (A, S, B, C, D, E), in that group's worktree.

- [ ] **Step 1:** `go build ./... && go vet ./... && go vet -tags live ./... && test -z "$(gofmt -l .)" && go test ./... 2>&1 | tail -40`, in the foreground (about 30 minutes). Expected: PASS.
- [ ] **Step 2:** For Groups B and C, `grep -rnE 'fugaro ls\b|cheap-loop-senior|claude-solo' --include='*.go' --include='*.md' --include='*.yaml' . | grep -v -e docs/plans -e docs/releases -e docs/design`. Expected: only the tombstone, the `Renamed` map and their tests.
- [ ] **Step 3:** Open the PR, titled `generic tool: <group> (tasks N-M)`. The body lists:
  - the decisions it implements;
  - what no run could verify (the live checks);
  - for Group A, the RTDB rules change and "rerun `fugaro init --firebase` after upgrading";
  - for Group D, Check 32 and the possible grant handed to the IAM plan.

  Read every CI job's log: `test`, `rules`, `terraform` and, when images changed, `images` and `docker-tests`.

## Task 23: Fold into the 0.7.0 release notes

**Depends on:** every included group merged, base-image Task 22's `docs/releases/v0.7.0.md` existing (create the file with the same header if this runs first, and base-image Task 22 merges into it).

**Files:** `docs/releases/v0.7.0.md`.

- [ ] **Step 1:** Add to the Highlights, after the base-image paragraphs:

```markdown
**Recipes for every kind of task.** The catalog is now `solo`, `standard`, `premium`, `review-only`, `pick`, `test-and-fix` and `lint-fix` (plus `default`, unchanged). Recipes describe when to use them (`use_when`), and the `/fugaro:routing` skill picks one per task and summarises batches. New: a senior rejection can go back to the cheap loop (`bounce: first_line`), check steps run your tests or linter before any model does (`commands.lint`, `commands.fix`), `review-only` comments on any pull request without pushing (fork PRs need `review.allow_forks`), and `fugaro run --attempts N` plus `pick` give best-of-N. `cheap-loop-senior` and `claude-solo` were renamed to `standard` and `solo`. See docs/recipes.md.

**The dashboard is a to-do list.** `fugaro watch` now shows finished runs (successes for 6 hours or the last 15, failures until you press `x`), lists the pull requests ready for your review, and expands a run (space) to its stage, last action, verify result, PR and cost. `--all` and `a` show everything.

**`fugaro runs ls` replaces `fugaro ls`**, which now only says so. `fugaro logs RUN --url` prints the console link.

**`checkout: clone`** runs a workflow on the base image with no per-repository build: the run clones and installs its mise tools at start. For repositories with no system packages.

**SECURITY.md** now states the trust model and its known limits.
```

Add to the operator steps, after the base-image upgrade order:
- `fugaro init --firebase <id>` once per installation. It deploys the dashboard rules for the run's last action and token count. Until then runs work, and the run log names the command.
- Replace `fugaro ls` with `fugaro runs ls` in scripts.
- Replace `agent.recipe: cheap-loop-senior` or `claude-solo` before upgrading. A 0.7.0 CLI refuses the old names with the new one.

**Not in these notes:** the Docker-capable backend and the cost preview (both 0.8.0, rulings R2 and R4).

- [ ] **Step 2:** `go test -count=1 -run 'TestRelease' ./...` (the release-notes checks, if any), and read the rendered file.
- [ ] **Step 3: Commit** `git commit -m "generic tool task 23: the 0.7.0 release notes"`. The release itself is base-image Task 22's `/new-release 0.7.0`.

---

## Release 0.8.0: the Docker-capable backend and the cost preview (owner rulings R2 and R4; not part of 0.7.0)

Starts right after 0.7.0 is released. **No task below is in any 0.7.0 group or in the 0.7.0 release notes.**

## Group F: cost preview (branch `gt-cost`)

Moved here from 0.7.0 by owner ruling R4 (2026-10-08, design §1.1): the design and the tasks are unchanged from the recommendation (R3), only the release is. It can start right away, independently of Check 31 and Tasks 26 to 28 below.

### Task 24: Cost per stage in `result.json` (G20)

**Files:** `internal/runstore/runstore.go` (`StageTiming.ModelUSD float64 json:"model_usd,omitempty"`, `Model string json:"model,omitempty"`), `internal/runner/runner.go` (the stage's end), tests.

- [ ] **Step 1: Write the failing test.** `TestStageTimingCarriesCost`. With the gateway on (the budget harness), each `StageTiming` has `ModelUSD` equal to the gateway's `StageReport.Used` for that stage, in USD, and `Model` equal to the stage's pinned model. With oauth, `ModelUSD` is the result event's `total_cost_usd`. The sum over stages equals `Cost.ModelUSD` within a micro-dollar.

- [ ] **Step 2: Run it to see it fail**: `go test -race -count=1 -run 'TestStageTimingCarriesCost' ./internal/runner/`

- [ ] **Step 3: Implement** at the point the stage's `StageReport` (or the oauth result) is already handled. Write nothing new to RTDB.

- [ ] **Step 4: Run the tests**: `go test -race -count=1 -run 'TestStageTiming|TestCost|TestBudget' ./internal/runner/ && go test -race -count=1 ./internal/runstore/`. Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 24: result.json records each stage's model cost"`

### Task 25: `fugaro budget preview` (G20)

**Depends on:** Task 8 (`StageBound`). Task 24 provides the history line only.

**Files:** `internal/cli/budget_preview.go` (new), `budget.go` (subcommand), `budget_preview_test.go`.

**Interfaces:** `fugaro budget preview --recipe NAME --runs N [--repo R] [--json]`.

- [ ] **Step 1: Write the failing test**

```go
func TestBudgetPreviewCeiling(t *testing.T) {
	f := newBudgetFixture(t) // budget_test.go's fake RTDB with caps
	f.SetCaps(budget.Caps{RepoDaily: usd(20), PerRun: usd(3)})
	f.SetSpentToday(usd(5))
	f.Checkout(t, "agent:\n  max_budget_usd: 0.5\n  first_line_rounds: 1\n  review_rounds: 2\n")
	out, _, err := execute(t, "budget", "preview", "--recipe", "standard", "--runs", "6")
	if err != nil {
		t.Fatal(err)
	}
	// standard with f=1, n=2: 1+2+2*(2+2) = 11 stages × $0.50 = $5.50, capped
	// by the $3 per-run cap: 6 × $3 = $18 ceiling, $15 headroom.
	for _, want := range []string{"rough worst case: $18.00", "6 runs × $3.00 (per-run cap)", "may exceed the repository's daily cap ($15.00 left today)"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview never says %q:\n%s", want, out)
		}
	}
}
```

Plus:
- with no per-stage and no per-run cap, the preview prints `unbounded: set budget.per_run_usd`;
- with at least 5 recorded runs, it prints a `typical:` line, and with fewer it prints none.

- [ ] **Step 2: Run it to see it fail**: `go test -race -count=1 -run 'TestBudgetPreview' ./internal/cli/`

- [ ] **Step 3: Implement**
- Resolve the recipe as `run` does (all three layers).
- Read the knobs from the checkout's resolved config (layered config's seam).
- `per := min(perRunCap, StageBound × max_budget_usd)`, treating an unset value as unbounded, and `ceiling := N × per`.
- Read the headroom as `budget show` does.
- History: the `ls` loader over 30 days of the repository's runs. Average `ModelUSD` by (stage kind, model) and multiply by the recipe's typical stage count, which is the bound with one round each. Clearly label it as typical.
- Read-only: no write anywhere.

- [ ] **Step 4: Run the tests**: `go test -race -count=1 -run 'TestBudget' ./internal/cli/`. Expected: `ok`.

- [ ] **Step 5: Commit** `git commit -m "generic tool task 25: fugaro budget preview, a rough worst case against the caps"`

---

### Task 26: Check 31 and its probe (the measurement, G1, G2)

**Files:** `deploy/sandbox/docker-probe/Dockerfile`, `deploy/sandbox/docker-probe/probe.sh`, `deploy/sandbox/docker-probe/batch-job.json`, `docs/gcp-live-checklist.md` (Check 31), `images/docker_probe_test.go` (static: the Dockerfile builds `FROM` the published `fugaro-base` by digest, pins every package, and the script prints only `FACT:` lines and timings).

- [ ] **Step 1: Write the static test**, then the probe:
  - `probe.sh` runs the design's §1 steps A and B in order, each wrapped in `date +%s.%N` stamps, and prints `FACT: <step> <ok|fail> <seconds> <first error line>`;
  - it exits 0 whatever it finds, since a failure is a finding.
- [ ] **Step 2: Write Check 31** in the format of Checks 29 and 30: `(sandbox belong only, run by you; USER-RUN, NOT RUN)`.
  - **Never** in `fugaro-dev` or the EdgeWeb or EdgeServer projects.
  - **The steps:** build and push the probe to the sandbox's registry (your terminal); create a throwaway Cloud Run job from it with the same execution environment, CPU and memory as the sandbox's workflow job; five executions; the Batch job from `batch-job.json`, five times; D from base-image Task 22's measurement.
  - **FACT lines** to record, the **paste-back** list, and the **restore** (delete the throwaway job, the Batch jobs and the probe image).
- [ ] **Step 3:** `go test -count=1 ./images/ -run TestDockerProbe`. Commit and open a docs PR. The owner runs the check.

### Task 27 (BLOCKED on Check 31): Docker on gen-2, if A and B pass the threshold

Outline only; the full tasks are written after the FACT lines:
- `workflows.<n>.docker: true` (scope table, schema);
- the base or derived image gets rootless podman and a `docker` shim;
- the runner starts the user-mode service and checks `docker info` before the first stage;
- `DOCKER_HOST` joins the agent allowlist (own review: the first allowlist addition since M9);
- the job's ephemeral storage is raised;
- the selftest checks the socket is user-owned;
- SECURITY.md is updated.

### Task 28 (BLOCKED on Check 31): a Cloud Batch backend, if only C passes

Outline only, about 12 tasks:
- `internal/backend/batch` passes `backendtest.Run` on a new Batch fake in `internal/gcpfake`;
- `backend: cloud-batch` in the project config;
- `fugaro init`'s Batch stage (job template, service account reuse, log routing into the existing log bucket);
- `LongestTaskTimeout` and `TaskTimeout` from the template;
- `docs/backends.md`;
- a new live check.

### Task 29: Delete the `fugaro ls` tombstone (P3)

One function and its test. It goes in 0.8.0 whatever Check 31 finds.

---

## Self-review

Checked against the design ([design/generic-tool.md](../design/generic-tool.md)):

| Design section | Covered by |
|---|---|
| §0 what is shipped | Global Constraints, the tasks' "today" notes |
| §1 Docker backend (0.8.0) | Tasks 26 to 28, outside every 0.7.0 group |
| §1.1 cost preview (0.8.0, moved from spec §8 by R4) | Tasks 24, 25, outside every 0.7.0 group |
| §2 checkout | Tasks 15 to 18 |
| §3.1 format extensions and safety | Task 8 (parser), 10 (bounce, roles), 11 (gate), 12 (review mode), 13 (attempts) |
| §3.2 self-description | Tasks 8 (`use_when`), 14 (`ls --verbose`) |
| §3.3 knobs outside | Task 14 (`TestCatalogIsTheStarterSet` refuses pinned rounds) |
| §3.4 layering | unchanged; Task 14 keeps the resolution order |
| §3.5 catalog | Task 14 |
| §4 routing skill | Task 19 |
| §5 naming | Task 7 (and 29 in 0.8.0) |
| §6 model routing | Task 20 |
| §7 logs | Task 1 |
| §9 SECURITY.md | Task 21 |
| §10.1 review-ready | Tasks 4, 6 |
| §10.2 detail | Tasks 2, 3, 5 |
| §10.3 filtering | Task 6 |
| §11 control plane | Task 20 |
| §12 order and release | PR groups, Task 23 |

**Type consistency:** these names are used the same way everywhere:
- `recipe.StepCheck`, `recipe.CheckCommand`, `Step.Bounce`, `Step.Autofix`, `Recipe.UseWhen`, `Recipe.CoderIsReviewer`, `Recipe.Mode`, `recipe.ModeReview`, `recipe.Renamed`, `recipe.StageBound`, `recipe.UsesV07`;
- `planStep.Bounce` and `FirstRounds`;
- `verify.KindLint`;
- `runstore.Record.Gate`, `Reviewed`, `OutcomeReviewed`, `ReviewSummary.Bounce`, `StageTiming.ModelUSD`;
- `budget.AgentEntry.Action` and `Tokens`;
- `agent.Relay.OnTool`;
- `watch.FinishedRun`, `MergeFinished`, `Cursor`, `Rows`, `Resolve`, `Filter`, `ReadyForReview`, `Acks`;
- `config.Commands.Lint` and `Fix`, `Config.Review.AllowForks`, `Workflow.Checkout`;
- `gitops.CloneBlobless`, `gitprov.Provider.FetchPRHead`;
- `task.ReviewPRs`.

**Placeholders:** the test helpers named as "stand for the existing harness" (budget harness, recipe harness, fake RTDB, config parse helpers) are the only names not read in full. Each task says so and keeps its assertions. Every new type, message and flag is written out.

**Known limits, stated in the tasks:**
- The live checks (31 for 0.8.0; 32 and 33 for 0.7.0) are the owner's.
- The `refs/pull/N/head` fetch for forks is proved only on the fake.
- The rules change ships with the release and needs `fugaro init --firebase`.
