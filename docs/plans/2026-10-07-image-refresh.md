# `fugaro image refresh` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run `fugaro image refresh` once in a repository's checkout to move it onto this release's base image. In order, it copies the base image if missing, points the daily image check job at it (image and `FUGARO_CHECK_SPEC`, with no Terraform), rebuilds every selected job image not yet built from it, and names `fugaro init --anchor` when the checkout still lacks `gcp_project:`. A rerun is idempotent, and one that stopped partway says what finished and what to run.

**Architecture:** The command lives in `internal/cli/image_refresh.go` and reuses init's pieces in one process. Preflight is read only and prints the whole plan. Step 2 is the images stage, `imagesStage` (`init_images.go`), restricted to the selected kinds through a new `only` field. Step 3 is a new direct Cloud Run job update in `internal/infra/checkjob.go`: GET the check job, rewrite its container image and the `base_images` of `FUGARO_CHECK_SPEC` with the same `CheckJobSpec` marshalling `infra.Repo` uses, then PATCH the same job object with its etag. Step 4 uses init's first-build submit-and-wait, extracted into `submitAndWait` behind a `newCloudBuilder` seam. The confirmations are the existing ones: `confirmOrdinary` for the copy, `r.confirm` for the job update, and `askTyped` for each build. Terraform is not run, and its module is unchanged (D7).

**Tech Stack:** Go 1.27, cobra, `google.golang.org/api/run/v2` (`Operations.Get`, and its HTTP client for the raw jobs get and patch of Task 2), `internal/mirror`, `internal/imagecheck`, `internal/initflow`, `internal/blobx`, the fakes `internal/gcpfake` (`Run`, registries, `Build`) and the init test rigs (`newInitRig`, `newImagesRig`, `newAnchorModeRig`).

**Spec:** [docs/design/image-refresh.md](../design/image-refresh.md)

## Decisions (veto any before execution starts)

- **D1. `--repo` checks the origin; it does not replace the checkout.** `init --repo`, the build spec (`infra.Repo` needs the checkout's `fugaro.yaml` and origin URL) and `image build` all read the checkout, and the local config does not store `fugaro.yaml`. So refresh runs in a checkout. `--repo owner/name` must equal its origin (compared with `task.CanonicalRepo`, as `image build --repo` does). Outside a checkout it refuses with `run it in a checkout of <repo>`. Running without a checkout (a temporary clone of the base branch with the user's git credentials) is a candidate for later.
- **D2. A real terminal is required up front.** The checks follow `initflow.CanConfirm(initflow.Typed, …)`: a coding agent's marker gives `fugaro image refresh applies cloud changes: ` + `initflow.AgentRefusal(marker)`, and anything else that is not a terminal gives `initflow.NoTerminalAdvice`. Both run before the local config, git, credentials or the network are touched. The command has no `--yes`, `--json`, `--non-interactive` or `--plan-only`, because every billable build is typed anyway.
- **D3. A development build (`releaseVersion() == ""`) is refused** in preflight: it has no published base image to copy.
- **D4. A custom base** is a `base_images.<kind>` of a selected kind that is set and not `<registry host>/fugaro-base/fugaro-<kind>:X.Y.Z` (`isManaged`). One refusal names every such kind, the local config's path and the fix: `remove base_images.<kind> from <path> (keep a backup), then rerun <the same command>`. It adds that `fugaro image build` keeps using your own base image. Nothing is copied, updated or read from the bucket before the refusal. An unset entry is not custom: step 2 copies the release image and records it.
- **D5. Only the selected workflows' kinds are copied.** `imagesStage.only`, when set, replaces `--base` and the checkout's kinds. Plain `init` in a checkout adds every kind of the checkout.
- **D6. The base step is the images stage alone.** It runs no installation Terraform, no Firebase stage, no secrets stage, no plugin wiring and no repository stage. Like `init --base`, it records `base_images` and republishes the shared config. `--image-source` and `--expect-digest` mean what they mean for `init`. `--replace-image` is not offered: a registry tag that names another image stops the run with init's own message, which names `fugaro init --base <kind> --replace-image <kind>`.
- **D7. The check job is updated directly, with no Terraform lifecycle change.** The requested `ignore_changes = [image]` was not adopted, for three reasons:
  - The base is also in `FUGARO_CHECK_SPEC.base_images`, an env value.
  - Terraform cannot ignore one entry of the `env` block list except by position, and the position shifts when, for example, `FUGARO_GITHUB_APP_ID` is added.
  - Ignoring all of `containers[0].env` would take `FUGARO_PROJECT` (checked by the module's preconditions), the spec's `workflows` and the secret env away from Terraform. Every `init --repo` would then have to write the env directly too.

  Instead, step 3 writes exactly what Terraform would render. `infra.RewriteCheckSpec` unmarshals the live spec into `infra.CheckJobSpec`, replaces `BaseImages[k]` with the local config's value for every kind the spec names, and marshals it with `json.Marshal`, the same code `infra.Repo` uses. A test pins it equal to `infra.Repo`'s rendering with the new base images (Task 2). The image becomes the new base of the kind whose old base it was (the check runs from its first workflow's kind).

  The write is a read-modify-write of the job's raw JSON (decoded with `UseNumber`), not of `run.GoogleCloudRunV2Job`: the Go type drops zero values such as `maxRetries: 0` (`omitempty`) and fields the client does not model, and the server would reset them. It reads the job, changes only the one container's `image` and that env entry's `value`, and sends the rest back as read. Cloud Run v2's `jobs.patch` takes the whole job and has no update mask. The read etag goes with it, so a concurrent change is refused. The operation is polled until done. The job must carry this repository's check-job labels, have exactly one container, and its spec must name the repository. A live spec that is not exactly Repo's rendering (unknown keys, other formatting) is refused when a base must move, and is no change when none does.

  Consequences:
  - No state migration and no one-time `init --repo`.
  - The next `init --repo` from this local config plans no change for the job.
  - Nothing reports drift: discovery records only workflow jobs' images, `doctor` does not read the check job, `ls` reads only the spec's `workflows`, and `tf/cover.go` sees no diff.
  - Generated roots, the tfvars golden and `TestGeneratedRootValidates` are unchanged.
  - An operator whose local config is older reverts the job on their next `init --repo`, image and spec together, as today.

  Unverified until the post-release sandbox check (Task 12): that the live API accepts the round-tripped output-only fields, that it enforces the body etag, that the patch starts no execution, and that the provider shows no diff afterwards. Veto alternative: `ignore_changes` on both `containers[0].image` and `containers[0].env`, with `init --repo` writing the whole check-job env directly after every apply (two more tasks: the module change with its generated-root test, and a `SyncCheckJob` run by the repo engine).
- **D8. The "current" rule for builds** (`refreshVerdict`):
  - no record: build;
  - unreadable record: rebuild, like a record that names no base;
  - `base_ref == base_images.<kind>` after step 2: skip;
  - `base_ref` is a managed release of the same kind newer than that: skip, with "an older CLI does not downgrade it";
  - anything else: build.

  A record read that fails in the cloud stops the run with exit 2. A workflow that had no record gets one closing note saying that `fugaro init --repo` deploys its job if it is not deployed yet.
- **D9. Builds are sequential.** Each has its own typed confirmation (`cloudBuildBanner`) and waits for its build. A declined build stops the run, so the builds after it are not offered. A rerun skips what was built.
- **D10. The end note** uses `needsAnchorHint`: the checkout lacks `gcp_project:`, the runs bucket has the default name, and the repository is listed. It prints `next: run fugaro init --anchor in this checkout …`. Refresh never writes `fugaro.yaml`.
- **D11. No overall "proceed?" prompt.** The plan is printed first, and every step keeps its own confirmation: the copy (`confirmOrdinary`, typed name), the job update (`r.confirm`, typed name), and each build (`askTyped`).
- **D12. Exit codes and the stop message.** A failure at step N returns the step's own code (1 refusal, declined or needs-you; 2 cloud) wrapped as `…; fugaro image refresh stopped at step N (<name>), steps finished: <list>; once that is fixed, rerun <command> in this checkout: finished steps say No changes`. The step names are `preflight`, `base`, `check job` and `builds`.
- **D13. Pointers move to the one command.** The changed texts:
  - `anchorProblemText`: `(1) remove base_images.<kind> …` when needed, `(n) fugaro image refresh --repo <repo> --workflow <wf> in the checkout, in your own terminal window (it copies this release's <kind> base image, points the daily image check job at it and rebuilds the image)`, then `fugaro init --anchor`. A workflow with no known kind keeps `fugaro image build`.
  - The recipe image refusal (`recipes_skew.go`): `Run fugaro image refresh --repo <repo> --workflow <wf> in its checkout, in your own terminal window (it copies this release's <kind> base image, points the daily image check job at it and rebuilds the image)` plus the `--recipe default` alternative.
  - `reasonConfigBaseCustom`: adds `or fugaro image refresh`.
  - `init --help` (init.go lines 170 to 178).
  - The docs lines listed in Task 10.

  `docs/releases/v0.4.x.md` and `v0.5.0.md` are history and are not changed. The setup skill names no part of the sequence and is not changed.
- **D14. Skills lint and release shape.** `fugaro image refresh` is treated like `fugaro image build`: not an owner command, it may be named inline, and the CLI refuses agents itself. Release 0.5.1 adds no version constant and no "Before you tag" item (no format change).

## Global Constraints

Every task's requirements include these.

From the design:
- The command and order: `fugaro image refresh [--repo <owner/name>] [--workflow <name>]...`, in a checkout. Steps: 1 preflight, 2 base copy, 3 direct check-job update, 4 builds, 5 anchor hint.
- A custom base stops it with the one-line fix, and it never offers replacement. It is refused inside a coding agent's session before any credential or network use.
- One repository per invocation (no `--all`; a candidate later). Every existing confirmation stays, and the whole plan (kinds, workflows, number of builds) is printed first.
- Not doing: replacing custom bases, Firebase rules, looping over repositories, writing `fugaro.yaml`, running Terraform.
- A stop names the finished steps and what to run next, and a rerun is idempotent. All of it runs in one process: no shelling out to `fugaro`.
- Release 0.5.1: a patch release, new command, no format change.

Project rules:
- No live cloud applies in tests: `gcpfake` servers, `file://` and `mem://` buckets, the fake terraform and the fake builder only. Never touch EdgeWeb or EdgeServer. No secrets are handled.
- Subagent-driven development in one git worktree for this PR group, a fresh implementer per task, and review between tasks.
- Every CI check (`test`, `terraform`, `rules`) is read before merge, not only its summary.
- Docs must match behaviour: the `rules` tests (`plugin/*_test.go` lint), the docs tests in `internal/cli` and every command or flag a doc names must be real.
- Releases go through `/new-release` with `docs/releases/vX.Y.Z.md` merged first.
- The `internal/cli` full suite is slow (about 6 minutes). Tasks run the focused tests named in them, with one full `go test ./...` at the end of the PR group (Task 11).
- One PR group: the change is small enough, and the tasks are sequential.

## Review Focus

The five failure modes most likely to hit a user, each pinned by a test:

1. **A custom or hand-pushed base is silently used or replaced.** Expected: a stop before anything is read from the bucket, copied or updated, naming the entry, the file and the fix. Pinned by `TestRefreshRefusesCustomBase` (Task 6).
2. **The check job is left half-moved.** Two variants: the image is new but `FUGARO_CHECK_SPEC.base_images` is old, so the next daily rebuild starts from the old base; or the next `init --repo` sees a diff. Expected: both move together, the bytes equal Terraform's rendering, and nothing else in the job changes. Pinned by `TestRewriteCheckSpecEqualsTerraformRendering` and `TestApplyCheckJobChangesOnlyImageAndSpec` (Task 2), and `TestRefreshCheckJobStep` (Task 7).
3. **A concurrent change to the check job** (an `init --repo` in another terminal) is overwritten. Expected: the stale etag is refused, and the error says to rerun. Pinned by `TestApplyCheckJobRefusesAConcurrentChange` (Task 2).
4. **A rebuild from the wrong base, a wasted rebuild, or a downgrade.** Expected: builds start from the base step's recorded base, current images are skipped, and an image built from a newer release is kept. Pinned by `TestRefreshVerdict` (Task 5) and `TestRefreshBuildsStartFromTheNewBase` (Task 7).
5. **A run that stops partway** (a declined copy, a failed build), or is started from a coding agent. Expected: the message names the finished steps and the exact rerun line with the step's exit code. In an agent session, nothing is touched. Pinned by `TestRefreshStopsSayWhatFinished` and `TestRefreshRefusedInAgentSession` (Task 8).

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/gcpfake/run.go`, `run_test.go` | jobs patch with etag; `Patches()` | 1 |
| `internal/infra/checkjob.go` (new), `checkjob_test.go` (new) | `RewriteCheckSpec`, `PlanCheckJob`, `ApplyCheckJob` | 2 |
| `internal/cli/init_images.go`, `init_images_test.go` | `imagesStage.only` | 3 |
| `internal/cli/init.go`, `init_review_test.go` | `cloudBuilder`, `newCloudBuilder`, `submitAndWait` | 4 |
| `internal/cli/image_refresh_rules.go` (new), test | `refreshBase`, `refreshVerdict`, `readRefreshRecord`, `customBaseRefusal` | 5 |
| `internal/cli/image_refresh.go` (new), `image_refresh_test.go` (new) | options, preflight, plan | 6 |
| `internal/cli/image_refresh_steps.go` (new), test | base, reload, check-job and build steps | 7 |
| `internal/cli/image_refresh.go`, `image.go` | command, orchestration, stop message, end note | 8 |
| `internal/cli/init_fugaroyaml.go`, `recipes_skew.go`, `init.go` and their tests | pointers to the one command | 9 |
| `docs/gcp-setup.md`, `docs/recipes.md`, `docs/release.md`, `.claude/skills/new-release/SKILL.md`, `plugin/skills/working/reference/followup.md`, `internal/cli/docs_refresh_test.go` (new) | docs | 10 |
| none | full suite, PR | 11 |
| `docs/releases/v0.5.1.md` (new) | release | 12 |

## PR group

One PR, branch `image-refresh`, Tasks 1 to 11 in order. Task 12 runs after the merge.

---

### Task 1: The Cloud Run fake updates a job (`jobs.patch` with etag)

**Files:**
- Modify: `internal/gcpfake/run.go`
- Test: `internal/gcpfake/run_test.go`

**Interfaces:**
- Consumes: `writeJSON`, `writeError`, `jobPath`, `isJobPath`, `runJob`.
- Produces:
  ```go
  // runJob gains: etag int
  func (f *Run) Patches() []map[string]any // every accepted PATCH body, in order
  // jobs get now reports "etag": "e<n>"; PATCH projects/*/locations/*/jobs/<j>
  // with the current etag sets the job's image and env from
  // template.template.containers[0] and answers a done operation; a stale
  // etag is 409 ABORTED; an unknown job 404.
  ```

- [ ] **Step 1: Write the failing test**

Add `"encoding/json"` to the imports of `internal/gcpfake/run_test.go`, then append:

```go
func TestRunFakePatchesAJobWithItsEtag(t *testing.T) {
	f := NewRun(t)
	f.SetJob("fugarochk-x", map[string]string{"fugaro": "managed"}, "img:1")
	f.SetJobEnv("fugarochk-x", map[string]string{"A": "1", "FUGARO_CHECK_SPEC": "old"})
	const path = "/v2/projects/proj-1/locations/r1/jobs/fugarochk-x"
	get := func() map[string]any {
		resp, err := http.Get(f.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var j map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	patch := func(j map[string]any) int {
		data, _ := json.Marshal(j)
		req, _ := http.NewRequest(http.MethodPatch, f.URL+path, strings.NewReader(string(data)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	j := get()
	etag, _ := j["etag"].(string)
	if etag == "" {
		t.Fatalf("no etag: %v", j)
	}
	c := j["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	c["image"] = "img:2"
	c["env"] = []any{map[string]any{"name": "A", "value": "1"}, map[string]any{"name": "FUGARO_CHECK_SPEC", "value": "new"}}
	if code := patch(j); code != http.StatusOK {
		t.Fatalf("patch: %d", code)
	}
	after := get()
	ac := after["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	if ac["image"] != "img:2" || after["etag"] == etag || len(f.Patches()) != 1 {
		t.Fatalf("after: %v, patches %d", after, len(f.Patches()))
	}
	if env := ac["env"].([]any); env[1].(map[string]any)["value"] != "new" {
		t.Fatalf("env %v", env)
	}
	// The etag read before the first patch is stale now.
	if code := patch(j); code != http.StatusConflict {
		t.Fatalf("stale etag: %d", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gcpfake/ -run TestRunFakePatchesAJobWithItsEtag`
Expected: FAIL: `no etag`.

- [ ] **Step 3: Write minimal implementation**

In `internal/gcpfake/run.go`:
- add `etag int` to `runJob` and `patches []map[string]any` to `Run`;
- in `getJob`, after `out := map[string]any{...}`, add `out["etag"] = fmt.Sprintf("e%d", j.etag)`;
- in `handle`'s switch, before `case r.Method == http.MethodGet && isJobPath(p):`, add `case r.Method == http.MethodPatch && isJobPath(p): f.patchJob(w, p, body)`;
- update the comment at line 21 to name jobs patch;
- add:

```go
// Patches are the bodies of the jobs patch requests the fake accepted.
func (f *Run) Patches() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.patches)
}

// patchJob is jobs.patch: the whole job comes back, carrying the etag jobs
// get gave; a stale one is refused (409 ABORTED), as the real API refuses a
// conflicting update. The fake keeps the container's image and plain env.
func (f *Run) patchJob(w http.ResponseWriter, p string, body []byte) {
	project, region, name, _ := jobPath(p)
	j := f.jobs[name]
	if j == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job "+name+" not found")
		return
	}
	var in struct {
		Etag     string `json:"etag"`
		Template struct {
			Template struct {
				Containers []struct {
					Image string `json:"image"`
					Env   []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"env"`
				} `json:"containers"`
			} `json:"template"`
		} `json:"template"`
	}
	var raw map[string]any
	if json.Unmarshal(body, &in) != nil || json.Unmarshal(body, &raw) != nil || len(in.Template.Template.Containers) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "not a job")
		return
	}
	if in.Etag != fmt.Sprintf("e%d", j.etag) {
		writeError(w, http.StatusConflict, "ABORTED", "the job was modified since it was read (etag mismatch)")
		return
	}
	c := in.Template.Template.Containers[0]
	j.image = c.Image
	j.env = map[string]string{}
	for _, e := range c.Env {
		j.env[e.Name] = e.Value
	}
	j.etag++
	f.patches = append(f.patches, raw)
	f.ops++
	writeJSON(w, http.StatusOK, map[string]any{
		"name": fmt.Sprintf("projects/%s/locations/%s/operations/%d", f.project(project), region, f.ops),
		"done": true,
	})
}
```

(`slices` and `encoding/json` are already imported by `run.go`. If not, add them.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gcpfake/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gcpfake/run.go internal/gcpfake/run_test.go
git commit -m "gcpfake: Cloud Run jobs patch with etag"
```

---

### Task 2: The direct check-job update (`internal/infra/checkjob.go`)

**Files:**
- Create: `internal/infra/checkjob.go`
- Test: `internal/infra/checkjob_test.go`

**Interfaces:**
- Consumes: `Clients.Run` (`*run.Service`) and `Clients.RunHTTP` (the HTTP client it sends through, from `newRunService`, for the raw job calls), `notFound(err)`, `CheckJobSpec`, `CheckSpecEnv`, `Repo`, the test fixtures `sandboxInputs`, `newCloud`, `managed`, `with`, and `gcp.LabelRepo`, `gcp.LabelRole`, `gcp.RoleCheck`.
- Produces:
  ```go
  var ErrCheckJobShape = errors.New("the check job is not as fugaro init --repo made it")
  func RewriteCheckSpec(raw, image string, bases map[string]string) (spec, newImage string, err error)
  type CheckJobOwner struct{ Repo, Label string } // RepoSpec.Name, RepoSpec.Label
  type CheckJobUpdate struct{ /* unexported: job name, old and new image and spec, the PATCH body (the job's raw JSON as read, etag included, image and spec changed, execution tokens dropped), applied flag */ }
  func (u *CheckJobUpdate) Job() string // projects/<p>/locations/<r>/jobs/<name>
  func (u *CheckJobUpdate) OldImage() string
  func (u *CheckJobUpdate) NewImage() string
  func (u *CheckJobUpdate) OldSpec() string
  func (u *CheckJobUpdate) NewSpec() string
  func (u *CheckJobUpdate) Changes() bool
  func PlanCheckJob(ctx context.Context, c *Clients, gcpProject, region, job string, owner CheckJobOwner, bases map[string]string) (*CheckJobUpdate, error) // nil, nil: no such job
  func ApplyCheckJob(ctx context.Context, c *Clients, u *CheckJobUpdate) error
  var checkJobPoll = 2 * time.Second // tests set 0
  ```

- [ ] **Step 1: Write the failing tests** in `internal/infra/checkjob_test.go`. The file is the spec; the code that follows must not be re-derived from older text. Tests (names are what the plan and the PR cite):
  - `TestRewriteCheckSpecEqualsTerraformRendering`: the rewrite equals `Repo`'s rendering for the new base, byte for byte; a kind `bases` lacks keeps its base. `TestRewriteCheckSpecRefusals`: not JSON, image naming no kind, unknown keys, other formatting when a base moves: `ErrCheckJobShape`.
  - Fixture: `checkJobJSON(t, rs, edit)` builds the job as the API returns it (labels, `etag`, `maxRetries: 0`, unknown fields at job and task level, a big number, a `valueSource` env entry); `checkJobCloud(t, edit)` stores it with `SetJobJSON` and returns `(*cloud, RepoSpec, CheckJobOwner)`.
  - `TestApplyCheckJobChangesOnlyImageAndSpec`: after plan and apply the stored job equals the one read except the image, the spec value and the etag (so `maxRetries: 0` and unknown fields survive); a re-plan is no change and writes nothing; a missing job is `nil, nil`.
  - `TestApplyCheckJobDropsExecutionTokens`: a job carrying `startExecutionToken` and `runExecutionToken` is patched without them and is otherwise unchanged.
  - `TestPlanCheckJobIgnoresASpecsFormWhenNothingMoves`; `TestPlanCheckJobRefusesAForeignOrMisshapenJob` (labels, container count, missing, doubled, secret-sourced and value-plus-`valueSource` spec entries, another repository's spec, an empty owner); `TestPlanCheckJobReportsAFailedRead`.
  - `TestApplyCheckJobRefusesAConcurrentChange` (stale etag: the error says it changed since it was read, and nothing lands), `TestApplyCheckJobKeepsOtherRefusalsPlain` (other errors do not carry the conflict hint), `TestApplyCheckJobWaitsForTheOperation`, `TestApplyCheckJobRefusesAForeignOrSpentPlan`.
  - `TestNewRunServiceKeepsTheEndpoint` and `TestRunServiceAndRunHTTPShareOneTransport` (a recording `RoundTripper` given as the options' HTTP client sees both the typed and the raw calls).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/infra/ -run 'TestRewriteCheckSpec|TestApplyCheckJob|TestPlanCheckJob|TestRunService'`
Expected: FAIL: `undefined: RewriteCheckSpec`.

- [ ] **Step 3: Write the implementation** in `internal/infra/checkjob.go`, as the file there (the Interfaces above are its signatures). What it must do, and what the tests above pin:
  - `RewriteCheckSpec`: unmarshal the spec into `CheckJobSpec`; the image names the kind whose base it is; move every kind `bases` has; no move returns `raw`, `image` untouched whatever the form; a move needs `raw` to be exactly `json.Marshal` of `CheckJobSpec` with no unknown keys (else `ErrCheckJobShape`, telling the operator to run `init --repo`).
  - `PlanCheckJob(ctx, c, gcpProject, region, job, owner, bases)`: refuses an empty `owner`; reads the job with a raw `GET` through `c.RunHTTP` (`runJSON`), decoded with `UseNumber` into `map[string]any` (never `run.GoogleCloudRunV2Job`: its `omitempty` drops `maxRetries: 0`, and every field the client does not model, and a patch without an update mask resets them); 404 is `nil, nil`; the job must carry `fugaro=managed`, `fugaro_role=check`, `fugaro_repo=owner.Label`, have exactly one container with an image, exactly one `FUGARO_CHECK_SPEC` that is a plain `value` (no `valueSource`, even beside a `value`) whose `repo` is `owner.Repo`, else `ErrCheckJobShape` (the message names the fix, `init --repo`). With a change it needs the read `etag`, sets the container's `image` and the entry's `value`, deletes the job-level `startExecutionToken` and `runExecutionToken` (sending one back can start an execution), and keeps the marshalled map as the PATCH body.
  - `CheckJobUpdate` has unexported fields and the accessors `Job()`, `OldImage()`, `NewImage()`, `OldSpec()`, `NewSpec()`, `Changes()` (nil-safe).
  - `ApplyCheckJob(ctx, c, u)`: no changes is a no-op; a plan not from `PlanCheckJob`, or already applied, is refused; the body goes by raw `PATCH` with no `updateMask`; only a 409 or 412 gets "it changed since it was read; rerun", other errors stay plain; the operation is polled (`checkJobPoll`, `checkJobPolls`) with the typed `Operations.Get`; an operation error is returned.
  - `runJSON` sends raw calls through `c.RunHTTP` to `c.Run.BasePath`; `newRunService` (in `discover.go`) builds `c.Run` on that same HTTP client, with `run.NewService`'s own defaults, so credentials, quota project and endpoint override are shared.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/infra/ ./internal/gcpfake/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/checkjob.go internal/infra/checkjob_test.go
git commit -m "infra: move the check job's base directly, as Terraform would render it"
```

---

### Task 3: The images stage copies only the kinds it is given

**Files:**
- Modify: `internal/cli/init_images.go` (`imagesStage`, `kinds`)
- Test: `internal/cli/init_images_test.go`

**Interfaces:**
- Produces: `imagesStage.only []string`. When it is non-nil, `kinds` returns exactly it, sorted, and neither `--base` nor the checkout's kinds are added.

- [ ] **Step 1: Write the failing test**

```go
// fugaro image refresh copies only its workflows' kinds, even in a checkout
// whose fugaro.yaml names others.
func TestImagesStageOnlyKinds(t *testing.T) {
	t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", "")))
	e, _ := stageEngine(t, "", &initOptions{baseKinds: []string{"java-services"}})
	s := newImagesStage(e)
	if got := strings.Join(s.kinds(t.Context()), ","); got != "java-services,web-node" {
		t.Fatalf("kinds %s, want --base's and the checkout's", got)
	}
	s.only = []string{"go"}
	if got := strings.Join(s.kinds(t.Context()), ","); got != "go" {
		t.Fatalf("only: kinds %s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestImagesStageOnlyKinds`
Expected: FAIL: `s.only undefined`.

- [ ] **Step 3: Implement**

```go
type imagesStage struct {
	e *initEngine
	// only, when set, is every base kind the stage copies (fugaro image
	// refresh's workflows'): --base and the checkout's kinds are not added.
	only []string
}
```

At the top of `kinds`:

```go
	if s.only != nil {
		return slices.Sorted(slices.Values(s.only))
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestImagesStageOnlyKinds|TestBaseImagesRecordedInConfig|TestPointedBaseImageIsNotMirrored|TestMirroredBaseMovesWithTheCLI'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init_images.go internal/cli/init_images_test.go
git commit -m "init: the images stage can be limited to given kinds"
```

---

### Task 4: One submit-and-wait behind a Cloud Build seam

**Files:**
- Modify: `internal/cli/init.go` (`buildImages`)
- Test: `internal/cli/init_review_test.go`

**Interfaces:**
- Consumes: `gcp.NewBuilder`, `gcp.BuildSpec`, `gcp.BuildResult`, `gcp.ErrBadBuildSpec`, `cloudBuildSpec`, `gcpOptions`.
- Produces:
  ```go
  type cloudBuilder interface {
      Submit(ctx context.Context, s gcp.BuildSpec) (gcp.BuildResult, error)
      Wait(ctx context.Context, id string, poll time.Duration) (gcp.BuildResult, error)
  }
  var newCloudBuilder = func(ctx context.Context, lc *localcfg.Config) (cloudBuilder, error)
  func (r *initRun) submitAndWait(ctx context.Context, b cloudBuilder, lc *localcfg.Config, cfg *config.Config, spec infra.RepoSpec, name, base string) (string, error)
  ```

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/init_review_test.go`, and add `"fmt"`, `"time"` and `gcp "github.com/dimipaun/fugaro/internal/backend/gcp"` to its imports if absent:

```go
// fakeBuilder is a Cloud Build that records the specs it is given and
// succeeds.
type fakeBuilder struct{ specs []gcp.BuildSpec }

func (f *fakeBuilder) Submit(_ context.Context, s gcp.BuildSpec) (gcp.BuildResult, error) {
	f.specs = append(f.specs, s)
	return gcp.BuildResult{ID: fmt.Sprintf("b%04d", len(f.specs)), Image: s.Image + ":latest", LogURL: "https://log/" + s.Workflow}, nil
}

func (f *fakeBuilder) Wait(_ context.Context, id string, _ time.Duration) (gcp.BuildResult, error) {
	return gcp.BuildResult{ID: id, Status: "SUCCESS", Digest: "sha256:" + strings.Repeat("d", 64)}, nil
}

func useFakeBuilder(t *testing.T) *fakeBuilder {
	t.Helper()
	f := &fakeBuilder{}
	old := newCloudBuilder
	newCloudBuilder = func(context.Context, *localcfg.Config) (cloudBuilder, error) { return f, nil }
	t.Cleanup(func() { newCloudBuilder = old })
	return f
}

// The first builds go through the seam and submitAndWait, after the typed
// confirmation, from the local config's base.
func TestFirstBuildsUseSubmitAndWait(t *testing.T) {
	fb := useFakeBuilder(t)
	fakeTerminal(t)
	e, out := stageEngine(t, initProjectName+"\n", nil)
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		BaseImages: map[string]string{"web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"}}
	e.r.setProject(lc)
	cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {Base: "web-node"}}}
	spec := infra.RepoSpec{Name: "acme/app", Slug: "acme-app", BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r",
		Workflows: map[string]infra.WorkflowSpec{"app": {}}}
	built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
	if err != nil || built != 1 || len(fb.specs) != 1 || fb.specs[0].Base != lc.BaseImages["web-node"] {
		t.Fatalf("built %d, err %v, specs %+v", built, err, fb.specs)
	}
	if !strings.Contains(out.String(), "built acme/app/app (Cloud Build build b0001)") || len(e.r.res.Builds) != 1 {
		t.Fatalf("output %s, builds %v", out.String(), e.r.res.Builds)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestFirstBuildsUseSubmitAndWait`
Expected: FAIL: `undefined: newCloudBuilder`.

- [ ] **Step 3: Implement**

In `internal/cli/init.go`, above `buildImages`:

```go
// cloudBuilder is the Cloud Build client the builds need. Tests replace
// newCloudBuilder.
type cloudBuilder interface {
	Submit(ctx context.Context, s gcp.BuildSpec) (gcp.BuildResult, error)
	Wait(ctx context.Context, id string, poll time.Duration) (gcp.BuildResult, error)
}

var newCloudBuilder = func(ctx context.Context, lc *localcfg.Config) (cloudBuilder, error) {
	b, err := gcp.NewBuilder(ctx, gcpOptions(lc), lc.BuildRegion())
	if err != nil {
		return nil, err
	}
	return b, nil
}

// submitAndWait submits workflow name's Cloud Build from base and waits for
// it: the one build fugaro init's first builds and fugaro image refresh run,
// each after its caller's typed confirmation. It returns the build's ID.
func (r *initRun) submitAndWait(ctx context.Context, b cloudBuilder, lc *localcfg.Config, cfg *config.Config, spec infra.RepoSpec, name, base string) (string, error) {
	bs, err := cloudBuildSpec(spec, cfg, name, base, lc.Build.MachineType, lc.RecordBucketURL())
	if err != nil {
		return "", err
	}
	res, err := b.Submit(ctx, bs)
	switch {
	case errors.Is(err, gcp.ErrBadBuildSpec):
		return "", userErr("%v", err)
	case err != nil:
		return "", remote(err)
	}
	fmt.Fprintf(r.w, "Cloud Build build %s of %s submitted; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
	done, err := b.Wait(ctx, res.ID, 0)
	if err != nil {
		return "", remote(err)
	}
	fmt.Fprintf(r.w, "built %s/%s (Cloud Build build %s)\n", spec.Name, name, oneLine(done.ID))
	r.res.Builds = append(r.res.Builds, done.ID)
	return done.ID, nil
}
```

In `buildImages`, replace `b, err := gcp.NewBuilder(ctx, gcpOptions(lc), lc.BuildRegion())` with `b, err := newCloudBuilder(ctx, lc)`. Replace everything from `bs, err := cloudBuildSpec(` to `built++` inclusive with:

```go
		if _, err := r.submitAndWait(ctx, b, lc, cfg, spec, name, base); err != nil {
			return built, err
		}
		built++
```

The typed confirmation stays in `buildImages`, before `submitAndWait`, which `TestCloudBuildConfirmationStaysOutsideTheCoveredPath` checks.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestFirstBuildsUseSubmitAndWait|TestTypedOnlyImageBuildMatrix|TestCloudBuildConfirmationStaysOutsideTheCoveredPath|TestRunConfirmation'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_review_test.go
git commit -m "init: one submit-and-wait behind a Cloud Build seam"
```

---

### Task 5: The refresh rules

**Files:**
- Create: `internal/cli/image_refresh_rules.go`
- Test: `internal/cli/image_refresh_rules_test.go`

**Interfaces:**
- Consumes: `infra.RegistryHost`, `mirror.Dest`, `isManaged`, `managedVersion`, `curVersion`, `newer`, `recordReadURL`, `fakeEndpointsOnGS`, `openRecordBucket`, `imagecheck.RecordKey`, `imagecheck.ParseRecord`, `blobx.ErrNotExist`, `pluginwire.Printable`, `quoteWord`.
- Produces:
  ```go
  func refreshBase(lc *localcfg.Config, kind, ver string) (string, error)
  func refreshVerdict(rec *imagecheck.Record, want, host, kind string) (build bool, why string)
  func readRefreshRecord(ctx context.Context, lc *localcfg.Config, slug, wf string) (*imagecheck.Record, error)
  func customBaseRefusal(lc *localcfg.Config, lcPath string, kinds []string, again string) error
  ```

- [ ] **Step 1: Write the failing tests**

```go
package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

const refreshHost = "us-east5-docker.pkg.dev/proj-1234"

func managedRef(kind, v string) string { return refreshHost + "/fugaro-base/fugaro-" + kind + ":" + v }

func TestRefreshBase(t *testing.T) {
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5", BaseImages: map[string]string{
		"go": managedRef("go", "0.4.0"), "web-node": managedRef("web-node", "0.6.0")}}
	for kind, want := range map[string]string{
		"go":            managedRef("go", "0.5.1"),            // an older copy moves on
		"web-node":      managedRef("web-node", "0.6.0"),      // a newer copy is kept
		"java-services": managedRef("java-services", "0.5.1"), // none yet: this release's
	} {
		if got, err := refreshBase(lc, kind, "0.5.1"); err != nil || got != want {
			t.Errorf("%s: %s, %v; want %s", kind, got, err, want)
		}
	}
}

func TestRefreshVerdict(t *testing.T) {
	want := managedRef("web-node", "0.5.1")
	for _, tc := range []struct {
		name  string
		rec   *imagecheck.Record
		build bool
		why   string
	}{
		{"no record", nil, true, "no build record"},
		{"current", &imagecheck.Record{BaseRef: want}, false, "current: built from " + want},
		{"older copy", &imagecheck.Record{BaseRef: managedRef("web-node", "0.4.0")}, true, "built from " + managedRef("web-node", "0.4.0")},
		{"ghcr release", &imagecheck.Record{BaseRef: "ghcr.io/dimipaun/fugaro-web-node:0.5.1"}, true, "rebuilt from " + want},
		{"no base_ref", &imagecheck.Record{}, true, "does not say which base image"},
		{"newer copy", &imagecheck.Record{BaseRef: managedRef("web-node", "0.6.0")}, false, "does not downgrade it"},
		{"newer, other kind", &imagecheck.Record{BaseRef: managedRef("go", "0.6.0")}, true, "rebuilt from"},
	} {
		build, why := refreshVerdict(tc.rec, want, refreshHost, "web-node")
		if build != tc.build || !strings.Contains(why, tc.why) {
			t.Errorf("%s: build %v, %q", tc.name, build, why)
		}
	}
}

func TestReadRefreshRecord(t *testing.T) {
	newAnchorModeRig(t, anchorModeYAML(), true)
	lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"}
	slug := mustSlug("github", "acme/app")
	if rec, err := readRefreshRecord(t.Context(), lc, slug, "app"); rec != nil || err != nil {
		t.Fatalf("none: %+v %v", rec, err)
	}
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	if rec, err := readRefreshRecord(t.Context(), lc, slug, "app"); err != nil || rec.BaseRef != managedRef("web-node", "0.5.1") {
		t.Fatalf("record: %+v %v", rec, err)
	}
	putRecordData(t, "not json")
	if rec, err := readRefreshRecord(t.Context(), lc, slug, "app"); err != nil || rec == nil || rec.BaseRef != "" {
		t.Fatalf("unreadable: %+v %v", rec, err)
	}
}

func TestCustomBaseRefusal(t *testing.T) {
	lc := &localcfg.Config{BaseImages: map[string]string{"go": refreshHost + "/fugaro-base/fugaro-go:dev-abc", "web-node": "ghcr.io/me/fugaro-web-node:mine"}}
	err := customBaseRefusal(lc, "/home/me/my config.yaml", []string{"go", "web-node"}, "fugaro image refresh --workflow app")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d", ExitCode(err))
	}
	for _, want := range []string{"base_images.go is " + refreshHost + "/fugaro-base/fugaro-go:dev-abc", "base_images.web-node is ghcr.io/me/fugaro-web-node:mine",
		"never replaces", "remove base_images.go, base_images.web-node from '/home/me/my config.yaml' (keep a backup), then rerun fugaro image refresh --workflow app",
		"fugaro image build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("lacks %q: %v", want, err)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRefreshBase|TestRefreshVerdict|TestReadRefreshRecord|TestCustomBaseRefusal'`
Expected: FAIL: `undefined: refreshBase`.

- [ ] **Step 3: Implement**

`internal/cli/image_refresh_rules.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// refreshBase is the base image kind's builds start from once the base step
// has run: this release's copy in the project's registry, or a newer copy an
// earlier, newer CLI made (never downgraded), exactly what the images stage
// records.
func refreshBase(lc *localcfg.Config, kind, ver string) (string, error) {
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return "", userErr("%v", err)
	}
	if cur := lc.BaseImage(kind); isManaged(cur, host, kind) && newer(curVersion(cur, host, kind), ver) {
		return cur, nil
	}
	dst, err := mirror.Dest(host, lc.GCPProject, "fugaro-"+kind, ver)
	if err != nil {
		return "", userErr("%v", err)
	}
	return dst.String(), nil
}

// refreshVerdict says whether a workflow's image is rebuilt (decision D8):
// not when its record's base_ref is want, nor when it is a newer copy of
// the same kind; otherwise yes. why is one line for the plan.
func refreshVerdict(rec *imagecheck.Record, want, host, kind string) (build bool, why string) {
	switch {
	case rec == nil:
		return true, "no build record: built from " + want
	case rec.BaseRef == want:
		return false, "current: built from " + want
	case rec.BaseRef == "":
		return true, "its build record does not say which base image it was built from: rebuilt from " + want
	}
	got := pluginwire.Printable(rec.BaseRef)
	if v, ok := managedVersion(rec.BaseRef, host, kind); ok {
		if w, ok := managedVersion(want, host, kind); ok && newer(v, w) {
			return false, fmt.Sprintf("kept: built from %s, newer than %s (an older CLI does not downgrade it)", got, want)
		}
	}
	return true, fmt.Sprintf("built from %s: rebuilt from %s", got, want)
}

// readRefreshRecord is workflow wf's build record: nil when there is none,
// an empty record when it cannot be parsed (rebuilt like one that names no
// base), and an error (exit 2) when the bucket cannot be read.
func readRefreshRecord(ctx context.Context, lc *localcfg.Config, slug, wf string) (*imagecheck.Record, error) {
	u := recordReadURL(lc)
	if fakeEndpointsOnGS(lc, u) {
		return nil, userErr("the build records are in %s and the local config's storage endpoint is a fake, so the real bucket is not read", u)
	}
	b, err := openRecordBucket(ctx, u)
	if err != nil {
		return nil, remote(fmt.Errorf("opening the build records' bucket %s: %w", u, err))
	}
	defer b.Close()
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, wf))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, remote(fmt.Errorf("reading the build record of workflow %s: %w", wf, err))
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		return &imagecheck.Record{}, nil
	}
	return rec, nil
}

// customBaseRefusal is decision D4's stop: the kinds whose base_images entry
// is not a release image fugaro init copied, with the one fix.
func customBaseRefusal(lc *localcfg.Config, lcPath string, kinds []string, again string) error {
	var refs, keys []string
	for _, k := range kinds {
		refs = append(refs, fmt.Sprintf("base_images.%s is %s", k, pluginwire.Printable(lc.BaseImage(k))))
		keys = append(keys, "base_images."+k)
	}
	return userErr("%s: not a release image fugaro init copied (a development or hand-pushed image), and fugaro image refresh never replaces one. To move to this release's base image: remove %s from %s (keep a backup), then rerun %s. To keep your own base image, rebuild from it with fugaro image build instead",
		strings.Join(refs, "; "), strings.Join(keys, ", "), quoteWord(lcPath), again)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestRefreshBase|TestRefreshVerdict|TestReadRefreshRecord|TestCustomBaseRefusal'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/image_refresh_rules.go internal/cli/image_refresh_rules_test.go
git commit -m "image refresh: the base, current-image and custom-base rules"
```

---

### Task 6: Preflight and the plan

**Files:**
- Create: `internal/cli/image_refresh.go`
- Test: `internal/cli/image_refresh_test.go`

**Interfaces:**
- Consumes: `releaseVersion`, `gitRead`, `loadCheckoutConfigAt`, `checkoutRepo`, `task.CanonicalRepo`, `loadRepoConfig`, `preflight.Environment`, `readOrigin`, `repoKnown`, `infra.RegistryHost`, `isManaged`, `task.Slug`, `selfCommand`, and Task 5's rules.
- Produces:
  ```go
  type refreshOptions struct {
      cloud         cloudOptions
      repo          string
      workflows     []string
      imageSource   string
      expectDigests []string
  }
  func (o refreshOptions) again() string
  type refreshPlan struct {
      root, repo, slug string
      lc               *localcfg.Config
      lcPath           string
      lcOld            []byte
      cfg              *config.Config
      workflows, kinds []string
      want             map[string]string // kind: the base builds start from after step 2
      why              map[string]string // workflow: why it is rebuilt or not
      builds           []string
  }
  func refreshPreflight(ctx context.Context, o refreshOptions, iopts *initOptions) (*refreshPlan, error)
  func (p *refreshPlan) print(w io.Writer)
  ```

- [ ] **Step 1: Write the failing tests**

```go
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// refreshYAML is acme/app with two workflows of two kinds.
const refreshYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { base: go, commands: { build: sh build.sh, test: sh test.sh } }\n"

func useVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func useSelf(t *testing.T) {
	t.Helper()
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
}

func noRecordReads(t *testing.T) {
	t.Helper()
	prev := openRecordBucket
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) {
		t.Fatal("a build record was read")
		return nil, nil
	}
	t.Cleanup(func() { openRecordBucket = prev })
}

func TestRefreshPreflightPlans(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	r.appendConfig(t, "base_images: {web-node: "+managedRef("web-node", "0.4.0")+"}\n")
	putWorkflowRecordFrom("app", "0.4.0", managedRef("web-node", "0.4.0"))
	p, err := refreshPreflight(t.Context(), refreshOptions{repo: "acme/app"}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.workflows, ",") != "api,app" || strings.Join(p.kinds, ",") != "go,web-node" ||
		p.want["web-node"] != managedRef("web-node", "0.5.1") || p.want["go"] != managedRef("go", "0.5.1") ||
		strings.Join(p.builds, ",") != "api,app" {
		t.Fatalf("plan %+v", p)
	}
	var out bytes.Buffer
	p.print(&out)
	for _, want := range []string{"fugaro image refresh of acme/app (project aurora, GCP project proj-1234)", "2. base go: " + managedRef("go", "0.5.1"),
		"3. the daily image check job", "4. builds: 2 of 2 workflow(s)", "app: built from " + managedRef("web-node", "0.4.0"), "5. then"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
	if calls := r.calls(t); len(calls) != 0 || len(r.ar.Requests()) != 0 {
		t.Errorf("preflight ran terraform or touched the registry: %q", calls)
	}
	// One workflow, already current.
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	p, err = refreshPreflight(t.Context(), refreshOptions{workflows: []string{"app"}}, &initOptions{})
	if err != nil || strings.Join(p.kinds, ",") != "web-node" || len(p.builds) != 0 || !strings.HasPrefix(p.why["app"], "current") {
		t.Fatalf("current: %v %+v", err, p)
	}
}

func TestRefreshRefusesCustomBase(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	r.appendConfig(t, "base_images: {go: "+refreshHost+"/fugaro-base/fugaro-go:dev-abc, web-node: "+managedRef("web-node", "0.4.0")+"}\n")
	noRecordReads(t)
	_, err := refreshPreflight(t.Context(), refreshOptions{workflows: []string{"api"}}, &initOptions{})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "base_images.go is "+refreshHost+"/fugaro-base/fugaro-go:dev-abc") ||
		strings.Contains(err.Error(), "base_images.web-node") || !strings.Contains(err.Error(), "then rerun fugaro image refresh --workflow api") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	r.check(t, refreshYAML)
}

func TestRefreshPreflightRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		onboarded     bool
		outside       bool
		o             refreshOptions
		want          string
	}{
		{"dev build", "dev", true, false, refreshOptions{}, "a development build"},
		{"not onboarded", "0.5.1", false, false, refreshOptions{}, "never onboards a repository"},
		{"another repo", "0.5.1", true, false, refreshOptions{repo: "acme/other"}, "--repo acme/other is not this checkout's origin"},
		{"unknown workflow", "0.5.1", true, false, refreshOptions{workflows: []string{"nope"}}, `fugaro.yaml has no workflow "nope" (it has: api, app)`},
		{"outside a checkout", "0.5.1", true, true, refreshOptions{repo: "acme/app"}, "run it in a checkout of acme/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useVersion(t, tc.version)
			newAnchorModeRig(t, refreshYAML, tc.onboarded)
			if tc.outside {
				t.Chdir(t.TempDir())
			}
			noRecordReads(t)
			_, err := refreshPreflight(t.Context(), tc.o, &initOptions{})
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRefreshPreflight|TestRefreshRefusesCustomBase'`
Expected: FAIL: `undefined: refreshPreflight`.

- [ ] **Step 3: Implement**

`internal/cli/image_refresh.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/preflight"
	"github.com/dimipaun/fugaro/internal/task"
)

// fugaro image refresh (design image-refresh.md): one repository onto this
// release's base image, in five steps, each with the confirmation it has
// today.

type refreshOptions struct {
	cloud         cloudOptions
	repo          string
	workflows     []string
	imageSource   string
	expectDigests []string
}

// again is the command line that reruns this refresh.
func (o refreshOptions) again() string {
	args := []string{selfCommand(), "image", "refresh"}
	if o.repo != "" {
		args = append(args, "--repo", quoteWord(o.repo))
	}
	for _, w := range o.workflows {
		args = append(args, "--workflow", quoteWord(w))
	}
	if o.cloud.project != "" {
		args = append(args, "--project", quoteWord(o.cloud.project))
	}
	return strings.Join(args, " ")
}

// refreshPlan is what preflight resolved, printed before anything changes.
type refreshPlan struct {
	root, repo, slug string
	lc               *localcfg.Config
	lcPath           string
	lcOld            []byte
	cfg              *config.Config
	workflows, kinds []string
	want             map[string]string
	why              map[string]string
	builds           []string
}

// refreshPreflight is step 1, read-only: the release, the checkout and its
// origin, the local config listing the repository, the selected workflows
// and their kinds, the custom-base stop (D4), and each workflow's verdict
// from its build record (the only cloud read).
func refreshPreflight(ctx context.Context, o refreshOptions, iopts *initOptions) (*refreshPlan, error) {
	ver := releaseVersion()
	if ver == "" {
		return nil, userErr("fugaro image refresh copies this release's base images, and this is a development build (%s) with none published: use a release build, or build a base from a checkout and point at it with fugaro init --base-image KIND=<tag>", Version)
	}
	if _, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel"); err != nil {
		target := "the repository"
		if o.repo != "" {
			target = o.repo
		}
		return nil, userErr("fugaro image refresh reads fugaro.yaml and the origin from the repository's checkout, and this directory is not in one: run it in a checkout of %s", target)
	}
	root, cfg, err := loadCheckoutConfigAt(ctx, ".")
	if err != nil {
		return nil, err
	}
	repo, err := checkoutRepo(ctx, root)
	if err != nil {
		return nil, err
	}
	if o.repo != "" {
		want, err := task.CanonicalRepo(o.repo)
		if err != nil {
			return nil, userErr("--repo %s: %v", o.repo, err)
		}
		if got, err := task.CanonicalRepo(repo); err != nil || got != want {
			return nil, userErr("--repo %s is not this checkout's origin (%s): run it in a checkout of %s", o.repo, repo, o.repo)
		}
	}
	lc, lcPath, old, err := loadRepoConfig(ctx, iopts, root)
	if err != nil {
		return nil, err
	}
	for _, c := range preflight.Environment(os.Getenv, lc.GCPProject) {
		if !c.OK {
			return nil, userErr("%s; %s", c.Problem, c.Fix)
		}
	}
	if oi, ok := readOrigin(ctx, root); !ok || !repoKnown(lc, oi) {
		return nil, userErr("%s is not one of project %s's repositories in the local config, and fugaro image refresh never onboards a repository: onboard it with fugaro init in this checkout first", pluginwire.Printable(repo), lc.Name)
	}
	all := slices.Sorted(maps.Keys(cfg.Workflows))
	wfs := all
	if len(o.workflows) > 0 {
		for _, w := range o.workflows {
			if _, ok := cfg.Workflows[w]; !ok {
				return nil, userErr("fugaro.yaml has no workflow %q (it has: %s)", w, strings.Join(all, ", "))
			}
		}
		wfs = slices.Compact(slices.Sorted(slices.Values(o.workflows)))
	}
	set := map[string]bool{}
	for _, w := range wfs {
		set[cfg.Workflows[w].Base] = true
	}
	kinds := slices.Sorted(maps.Keys(set))
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return nil, userErr("%v", err)
	}
	var custom []string
	for _, k := range kinds {
		if cur := lc.BaseImage(k); cur != "" && !isManaged(cur, host, k) {
			custom = append(custom, k)
		}
	}
	if len(custom) > 0 {
		return nil, customBaseRefusal(lc, lcPath, custom, o.again())
	}
	slug, err := task.Slug(cfg.Git.Provider, repo)
	if err != nil {
		return nil, userErr("%v", err)
	}
	p := &refreshPlan{root: root, repo: repo, slug: slug, lc: lc, lcPath: lcPath, lcOld: old, cfg: cfg,
		workflows: wfs, kinds: kinds, want: map[string]string{}, why: map[string]string{}}
	for _, k := range kinds {
		if p.want[k], err = refreshBase(lc, k, ver); err != nil {
			return nil, err
		}
	}
	for _, w := range wfs {
		kind := cfg.Workflows[w].Base
		rec, err := readRefreshRecord(ctx, lc, slug, w)
		if err != nil {
			return nil, err
		}
		build, why := refreshVerdict(rec, p.want[kind], host, kind)
		p.why[w] = why
		if build {
			p.builds = append(p.builds, w)
		}
	}
	return p, nil
}

// print is the whole plan (design: kinds, the check job, the builds),
// before anything changes.
func (p *refreshPlan) print(w io.Writer) {
	fmt.Fprintf(w, "fugaro image refresh of %s (project %s, GCP project %s), workflows %s:\n", p.repo, p.lc.Name, p.lc.GCPProject, strings.Join(p.workflows, ", "))
	fmt.Fprintln(w, "  1. preflight: done (nothing changed)")
	for _, k := range p.kinds {
		fmt.Fprintf(w, "  2. base %s: %s (copied into your registry if it lacks it, after its own confirmation)\n", k, p.want[k])
	}
	fmt.Fprintln(w, "  3. the daily image check job: its image and FUGARO_CHECK_SPEC's base images follow the local config's, through the Cloud Run Admin API after its own confirmation (no Terraform; No changes when current)")
	fmt.Fprintf(w, "  4. builds: %d of %d workflow(s), each billable and confirmed by typing the project's name:\n", len(p.builds), len(p.workflows))
	for _, wf := range p.workflows {
		fmt.Fprintf(w, "     %s: %s\n", wf, p.why[wf])
	}
	fmt.Fprintln(w, "  5. then, if this checkout's fugaro.yaml lacks gcp_project: the fugaro init --anchor command to run next (fugaro image refresh never writes fugaro.yaml)")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestRefreshPreflight|TestRefreshRefusesCustomBase|TestRefreshVerdict'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/image_refresh.go internal/cli/image_refresh_test.go
git commit -m "image refresh: preflight and the plan"
```

---

### Task 7: The steps: base, reload, check job, builds

**Files:**
- Create: `internal/cli/image_refresh_steps.go`
- Test: `internal/cli/image_refresh_steps_test.go`

**Interfaces:**
- Consumes: `newImagesStage`, `imagesStage.only` (Task 3), `infra.PlanCheckJob`, `infra.ApplyCheckJob`, `infra.ErrCheckJobShape` (Task 2), `newInitClients`, `gcp.CheckJobName`, `r.confirm`, `r.askTyped`, `cloudBuildBanner`, `newCloudBuilder`, `submitAndWait` (Task 4), `readRefreshRecord`, `refreshVerdict` (Task 5), `checkoutURL`, `image.BaseRef`, `infra.Repo`, `localcfg.Load`.
- Produces:
  ```go
  type refreshTarget struct {
      lc   *localcfg.Config // re-read after step 2
      cfg  *config.Config
      spec infra.RepoSpec
  }
  func (r *initRun) refreshBase(ctx context.Context, e *initEngine, kinds []string) error
  func refreshReload(ctx context.Context, p *refreshPlan) (*refreshTarget, error)
  func (r *initRun) refreshCheckJob(ctx context.Context, t *refreshTarget) error
  func (r *initRun) refreshBuilds(ctx context.Context, p *refreshPlan, t *refreshTarget) error
  ```

- [ ] **Step 1: Write the failing tests**

```go
package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	gcp "github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// atTerminal points e's run at a fake terminal whose input is stdin, with
// its output in the returned buffer.
func atTerminal(t *testing.T, e *initEngine, stdin string) *syncBuf {
	t.Helper()
	fakeTerminal(t)
	out := &syncBuf{}
	e.r.w = out
	e.r.in = bufio.NewReader(strings.NewReader(stdin))
	e.r.cmd.SetIn(strings.NewReader(stdin))
	return out
}

// Step 2 copies only the kinds it is given, after its own confirmation, and
// a rerun copies nothing.
func TestRefreshBaseStep(t *testing.T) {
	r := newImagesRig(t, "1.2.3")
	t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))) // names web-node
	e := rigEngine(t, r.initRig, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBase(t.Context(), e, []string{"go"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-go:1.2.3"] == "" || r.dst.tags[initProject+"/fugaro-base/fugaro-web-node:1.2.3"] != "" {
		t.Fatalf("copied %v", r.dst.tags)
	}
	if got := r.localConfig(t).BaseImages["go"]; got != "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-go:1.2.3" {
		t.Fatalf("base_images.go = %s", got)
	}
	puts := r.dst.puts
	out = atTerminal(t, e, "")
	if err := e.r.refreshBase(t.Context(), e, []string{"go"}); err != nil || r.dst.puts != puts || !strings.Contains(out.String(), "No changes") {
		t.Fatalf("rerun: %v, %d writes\n%s", err, r.dst.puts-puts, out.String())
	}
}

// Step 3 moves the image and the spec's base together, after the typed
// name, and says No changes on a rerun.
func TestRefreshCheckJobStep(t *testing.T) {
	r := newInitRig(t)
	e := rigEngine(t, r, &initOptions{})
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox") // label: gcp.RepoLabel(slug), what Task 2's owner check compares
	oldRef, newRef := managedRef("web-node", "0.4.0"), managedRef("web-node", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/sandbox", BaseImages: map[string]string{"web-node": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec), "FUGARO_PROJECT": "aurora"})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"web-node": newRef}
	tg := &refreshTarget{lc: lc, kinds: []string{"web-node"}, spec: infra.RepoSpec{Name: "acme/sandbox", Slug: slug, Label: label}}
	// Declined: nothing written.
	out := atTerminal(t, e, "nope\n")
	if err := e.r.refreshCheckJob(t.Context(), tg); err == nil || len(r.run.Patches()) != 0 {
		t.Fatalf("declined: %v, %d patches", err, len(r.run.Patches()))
	}
	out = atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"image " + oldRef + " -> " + newRef, "⚠ CONFIRM", "updated the daily image check job"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if len(r.run.Patches()) != 1 {
		t.Fatalf("%d patches", len(r.run.Patches()))
	}
	out = atTerminal(t, e, "")
	if err := e.r.refreshCheckJob(t.Context(), tg); err != nil || len(r.run.Patches()) != 1 || !strings.Contains(out.String(), "No changes") {
		t.Fatalf("rerun: %v\n%s", err, out.String())
	}
}

// Step 4 rebuilds the stale workflow only, from the base the base step
// recorded, after the typed name; a declined build stops it.
func TestRefreshBuildsStartFromTheNewBase(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e := rigEngine(t, r.initRig, &initOptions{})
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc.BaseImages = map[string]string{"web-node": managedRef("web-node", "0.5.1"), "go": managedRef("go", "0.5.1")}
	cfg, problems := config.Parse([]byte(refreshYAML))
	if cfg == nil {
		t.Fatal(problems)
	}
	slug := mustSlug("github", "acme/app")
	tg := &refreshTarget{lc: lc, cfg: cfg, spec: infra.RepoSpec{Name: "acme/app", Slug: slug, RegistryPath: "r", BuildServiceAccountEmail: "b@x.iam",
		Workflows: map[string]infra.WorkflowSpec{"app": {}, "api": {}}}}
	p := &refreshPlan{workflows: []string{"api", "app"}}
	putWorkflowRecordFrom("app", "0.5.1", managedRef("web-node", "0.5.1"))
	putWorkflowRecordFrom("api", "0.4.0", managedRef("go", "0.4.0"))
	atTerminal(t, e, "nope\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err == nil || !strings.Contains(err.Error(), "not confirmed") || len(fb.specs) != 0 {
		t.Fatalf("declined: %v, %d builds", err, len(fb.specs))
	}
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "api" || fb.specs[0].Base != managedRef("go", "0.5.1") {
		t.Fatalf("builds %+v", fb.specs)
	}
	if !strings.Contains(out.String(), "app: current: built from "+managedRef("web-node", "0.5.1")) {
		t.Errorf("output:\n%s", out.String())
	}
}

// The build confirmation is typed-only, never the run's or --yes's.
func TestRefreshBuildsConfirmationIsTyped(t *testing.T) {
	src, err := os.ReadFile("image_refresh_steps.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func (r *initRun) refreshBuilds(")
	if i < 0 {
		t.Fatal("refreshBuilds not found")
	}
	body = body[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	if !strings.Contains(body, "r.askTyped(") {
		t.Error("refreshBuilds does not ask the typed confirmation")
	}
	for _, bad := range []string{"confirmOrdinary", "askOrdinary", "runCovers", "r.confirm(", "r.ask("} {
		if strings.Contains(body, bad) {
			t.Errorf("refreshBuilds uses %s", bad)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRefreshBaseStep|TestRefreshCheckJobStep|TestRefreshBuilds'`
Expected: FAIL: `undefined: refreshTarget`.

- [ ] **Step 3: Implement**

`internal/cli/image_refresh_steps.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"

	gcp "github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// refreshTarget is the repository as the steps after the base step see it:
// the local config re-read (the base step recorded base_images), the
// checkout's fugaro.yaml, and the repository's spec.
type refreshTarget struct {
	lc   *localcfg.Config
	cfg  *config.Config
	spec infra.RepoSpec
}

// refreshBase is step 2: the images stage alone, for kinds (D5, D6), with
// its own confirmation and its own "No changes".
func (r *initRun) refreshBase(ctx context.Context, e *initEngine, kinds []string) error {
	s := newImagesStage(e)
	s.only = kinds
	e.images = s
	st, err := s.Check(ctx)
	if err != nil {
		return err
	}
	if st.State == initflow.Skipped {
		fmt.Fprintf(r.w, "  %s\n", st.Detail)
		return nil
	}
	out, err := s.Apply(ctx, initflow.Env{Interactive: true})
	if err != nil {
		return err
	}
	fmt.Fprintf(r.w, "  base: %s\n", out.Detail)
	return nil
}

// refreshReload re-reads the local config the base step wrote and computes
// the repository's spec, as fugaro image build does without the
// installation's outputs (a kind the local config has no base for stands in
// with the release's, for the spec only).
func refreshReload(ctx context.Context, p *refreshPlan) (*refreshTarget, error) {
	lc, err := localcfg.Load(p.lcPath)
	if err != nil {
		return nil, userErr("%s: %v", p.lcPath, err)
	}
	repoURL, err := checkoutURL(ctx, p.root)
	if err != nil {
		return nil, err
	}
	specLC := *lc
	specLC.BaseImages = maps.Clone(lc.BaseImages)
	if specLC.BaseImages == nil {
		specLC.BaseImages = map[string]string{}
	}
	for _, w := range p.cfg.Workflows {
		if specLC.BaseImages[w.Base] == "" {
			if specLC.BaseImages[w.Base], err = image.BaseRef(w.Base, Version); err != nil {
				return nil, userErr("%v", err)
			}
		}
	}
	spec, err := infra.Repo(infra.Inputs{LC: &specLC, Repo: p.repo, Cfg: p.cfg, RepoURL: repoURL})
	if err != nil {
		return nil, userErr("%v", err)
	}
	return &refreshTarget{lc: lc, cfg: p.cfg, spec: spec}, nil
}

// refreshCheckJob is step 3 (D7): the check job's image and
// FUGARO_CHECK_SPEC's base images moved to the local config's, directly,
// after the typed name; no Terraform.
func (r *initRun) refreshCheckJob(ctx context.Context, t *refreshTarget) error {
	c, err := newInitClients(ctx, t.lc)
	if err != nil {
		return err
	}
	u, err := infra.PlanCheckJob(ctx, c, t.lc.GCPProject, t.lc.Region, gcp.CheckJobName(t.spec.Slug),
		infra.CheckJobOwner{Repo: t.spec.Name, Label: t.spec.Label}, t.lc.BaseImages)
	switch {
	case errors.Is(err, infra.ErrCheckJobShape):
		return userErr("%v", err)
	case err != nil:
		return remote(err)
	case u == nil:
		fmt.Fprintln(r.w, "  no daily image check job (every workflow has rebuild.check: off, or init --repo has not deployed it yet): nothing to update")
		return nil
	case !u.Changes():
		fmt.Fprintf(r.w, "  No changes: the daily image check job already runs from %s\n", u.NewImage())
		return nil
	}
	fmt.Fprintf(r.w, "  %s: image %s -> %s\n  %s: %s\n    -> %s\n", u.Job(), u.OldImage(), u.NewImage(), infra.CheckSpecEnv, u.OldSpec(), u.NewSpec())
	if err := r.confirm(fmt.Sprintf("updates the daily image check job %s in place through the Cloud Run Admin API, as you: its image and %s's base images, exactly as listed above; nothing else in the job changes, and the next fugaro init --repo from this local config plans no change to it", u.Job(), infra.CheckSpecEnv),
		"the daily image check job was not updated"); err != nil {
		return err
	}
	if err := infra.ApplyCheckJob(ctx, c, u); err != nil {
		return remote(err)
	}
	fmt.Fprintln(r.w, "  updated the daily image check job")
	return nil
}

// refreshBuilds is step 4 (D8, D9): each selected workflow whose image is
// not built from its current base is rebuilt, after its own typed
// confirmation, from the base the local config now records.
func (r *initRun) refreshBuilds(ctx context.Context, p *refreshPlan, t *refreshTarget) error {
	host, err := infra.RegistryHost(t.lc)
	if err != nil {
		return userErr("%v", err)
	}
	var todo, unrecorded []string
	for _, wf := range p.workflows {
		kind := t.cfg.Workflows[wf].Base
		want := t.lc.BaseImage(kind)
		if want == "" {
			return userErr("the local config has no base image for kind %s after the base step", kind)
		}
		rec, err := readRefreshRecord(ctx, t.lc, t.spec.Slug, wf)
		if err != nil {
			return err
		}
		build, why := refreshVerdict(rec, want, host, kind)
		fmt.Fprintf(r.w, "  %s: %s\n", wf, why)
		if build {
			todo = append(todo, wf)
		}
		if rec == nil {
			unrecorded = append(unrecorded, wf)
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(r.w, "  No changes: every selected image is built from its current base")
		return nil
	}
	b, err := newCloudBuilder(ctx, t.lc)
	if err != nil {
		return remote(err)
	}
	for _, wf := range todo {
		ok, reachable, err := r.askTyped(cloudBuildBanner(t.spec.Name, wf, t.lc.Build.MachineType, t.spec.BuildServiceAccountEmail, t.spec.RegistryPath))
		if err != nil {
			return err
		}
		if !reachable || !ok {
			return userErr("the build of %s/%s was not confirmed (the project's name was not typed), so it and the builds after it were not submitted", t.spec.Name, wf)
		}
		if _, err := r.submitAndWait(ctx, b, t.lc, t.cfg, t.spec, wf, t.lc.BaseImage(t.cfg.Workflows[wf].Base)); err != nil {
			return err
		}
	}
	for _, wf := range unrecorded {
		fmt.Fprintf(r.w, "note: %s had no build record; if its job is not deployed yet, fugaro init --repo in this checkout deploys it\n", wf)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestRefreshBaseStep|TestRefreshCheckJobStep|TestRefreshBuilds|TestImagesStage'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/image_refresh_steps.go internal/cli/image_refresh_steps_test.go
git commit -m "image refresh: the base, check-job and build steps"
```

---

### Task 8: The command, its order, the stop message and the end note

**Files:**
- Modify: `internal/cli/image_refresh.go`, `internal/cli/image.go` (`newImageCmd`)
- Test: `internal/cli/image_refresh_test.go`

**Interfaces:**
- Consumes: Tasks 5 to 7, `newInitRun`, `installOptions`, `initEngine`, `refuseHTTP2Debug`, `agentMarker`, `stdinIsTerminal`, `initflow.CanConfirm`, `initflow.AgentRefusal`, `initflow.NoTerminalAdvice`, `checkoutProject`, `needsAnchorHint`, `addCloudFlags`.
- Produces:
  ```go
  func newImageRefreshCmd() *cobra.Command
  func runImageRefresh(cmd *cobra.Command, o refreshOptions) error
  type refreshSteps struct {
      base     func(ctx context.Context) error
      reload   func(ctx context.Context) (*refreshTarget, error)
      checkJob func(ctx context.Context, t *refreshTarget) error
      builds   func(ctx context.Context, t *refreshTarget) error
  }
  var newRefreshSteps = func(r *initRun, e *initEngine, p *refreshPlan) refreshSteps
  func refuseRefreshHere(cmd *cobra.Command) error
  func refreshStopped(step int, done []string, again string, err error) error
  func refreshAnchorNote(ctx context.Context, w io.Writer, root string, lc *localcfg.Config)
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/image_refresh_test.go`, adding `"errors"`, `"github.com/dimipaun/fugaro/internal/initflow"`, `"github.com/dimipaun/fugaro/internal/localcfg"` and `"github.com/dimipaun/fugaro/internal/mirror"` to its imports:

```go
// fakeSteps replaces steps 2 to 4; fail names the step that fails, with
// err. It records the steps that ran.
func fakeSteps(t *testing.T, fail string, err error) *[]string {
	t.Helper()
	ran := &[]string{}
	old := newRefreshSteps
	newRefreshSteps = func(r *initRun, e *initEngine, p *refreshPlan) refreshSteps {
		step := func(name string) error {
			*ran = append(*ran, name)
			if name == fail {
				return err
			}
			return nil
		}
		return refreshSteps{
			base: func(context.Context) error { return step("base") },
			reload: func(context.Context) (*refreshTarget, error) {
				return &refreshTarget{lc: p.lc, cfg: p.cfg}, nil
			},
			checkJob: func(context.Context, *refreshTarget) error { return step("check job") },
			builds:   func(context.Context, *refreshTarget) error { return step("builds") },
		}
	}
	t.Cleanup(func() { newRefreshSteps = old })
	return ran
}

func TestRefreshRefusedInAgentSession(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Chdir(t.TempDir())
	noRecordReads(t)
	prev := newMirror
	newMirror = func(context.Context, *localcfg.Config, []string) (*mirror.Mirror, error) {
		t.Fatal("an agent session reached the registry")
		return nil, nil
	}
	t.Cleanup(func() { newMirror = prev })
	fakeTerminal(t)
	_, _, err := executeStdin(t, "", "image", "refresh", "--repo", "acme/app")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro image refresh applies cloud changes: "+initflow.AgentRefusal("CLAUDECODE")) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestRefreshNeedsATerminal(t *testing.T) {
	t.Chdir(t.TempDir())
	noRecordReads(t)
	_, _, err := executeStdin(t, "", "image", "refresh")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), initflow.NoTerminalAdvice) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestRefreshStopsSayWhatFinished(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	newAnchorModeRig(t, refreshYAML, true)
	fakeTerminal(t)
	ran := fakeSteps(t, "check job", remote(errors.New("updating Cloud Run job x: 503")))
	out, _, err := executeStdin(t, "", "image", "refresh", "--workflow", "app")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	for _, want := range []string{"updating Cloud Run job x: 503", "stopped at step 3 (check job)", "steps finished: preflight, base",
		"rerun fugaro image refresh --workflow app in this checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Join(*ran, ",") != "base,check job" {
		t.Errorf("ran %v: the builds ran after a failed step", *ran)
	}
	if !strings.Contains(out, "4. builds:") || strings.Index(out, "fugaro image refresh of acme/app") > strings.Index(out, "step 2, base:") {
		t.Errorf("the plan is not printed first:\n%s", out)
	}
}

func TestRefreshEndsWithTheAnchorNote(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	for _, tc := range []struct {
		yaml string
		note bool
	}{{refreshYAML, true}, {withRigAnchor(refreshYAML), false}} {
		r := newAnchorModeRig(t, tc.yaml, true)
		fakeTerminal(t)
		ran := fakeSteps(t, "", nil)
		out, _, err := executeStdin(t, "", "image", "refresh")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if strings.Join(*ran, ",") != "base,check job,builds" || !strings.Contains(out, "fugaro image refresh of acme/app is done") {
			t.Fatalf("ran %v\n%s", *ran, out)
		}
		if got := strings.Contains(out, "next: run fugaro init --anchor in this checkout"); got != tc.note {
			t.Errorf("anchor note %v, want %v:\n%s", got, tc.note, out)
		}
		r.check(t, tc.yaml) // fugaro.yaml is never written
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRefreshRefusedInAgentSession|TestRefreshNeedsATerminal|TestRefreshStopsSayWhatFinished|TestRefreshEndsWithTheAnchorNote'`
Expected: FAIL: `unknown command "refresh" for "fugaro image"`.

- [ ] **Step 3: Implement**

In `internal/cli/image.go`, add `newImageRefreshCmd()` to `cmd.AddCommand(...)` in `newImageCmd`. Append to `internal/cli/image_refresh.go`, adding `"github.com/spf13/cobra"` and `"github.com/dimipaun/fugaro/internal/initflow"` to its imports:

```go
func newImageRefreshCmd() *cobra.Command {
	var o refreshOptions
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Move a repository onto this release's base image: copy it, point the daily image check job at it, rebuild what is stale",
		Long: "Run it in the checkout of an onboarded repository, in your own terminal window.\n\n" +
			"It prints the whole plan first, then, each step with its own confirmation:\n" +
			"(2) copies this release's base image of each selected workflow's kind into\n" +
			"your registry if it lacks it and records it, as fugaro init --base does;\n" +
			"(3) points the repository's daily image check job at it (its image and\n" +
			"FUGARO_CHECK_SPEC's base images) through the Cloud Run Admin API, with no\n" +
			"Terraform; (4) rebuilds each selected workflow whose build record says it\n" +
			"was built from another base (billable: the project's name typed for each);\n" +
			"(5) names fugaro init --anchor when fugaro.yaml lacks gcp_project.\n\n" +
			"A base_images entry that is not a release image fugaro init copied (a\n" +
			"development or hand-pushed image) stops it before anything changes: remove\n" +
			"the entry from the local config, then rerun. It never writes fugaro.yaml and\n" +
			"never onboards a repository. A coding agent's session is refused, and so is\n" +
			"a pipe: it needs a real terminal. A run that stops says which steps finished\n" +
			"and what to rerun; a rerun skips what is done. Exit codes: 0 done, 1 a\n" +
			"refusal or a confirmation not given, 2 a cloud failure.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImageRefresh(cmd, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "owner/name of the repository; it must be this checkout's origin (refresh reads fugaro.yaml from the checkout)")
	f.StringArrayVar(&o.workflows, "workflow", nil, "a workflow to refresh (repeatable; default: every workflow in fugaro.yaml)")
	f.StringVar(&o.imageSource, "image-source", "", "the registry and owner the release base images are copied from, as for fugaro init (default ghcr.io/dimipaun)")
	f.StringArrayVar(&o.expectDigests, "expect-digest", nil, "pin the digest of a base image copied, KIND=sha256:<hex>, as for fugaro init (repeatable)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// refuseRefreshHere is decision D2, before anything is read: a coding
// agent's session applies nothing, and anything but a real terminal cannot
// type the confirmations.
func refuseRefreshHere(cmd *cobra.Command) error {
	conds := initflow.Conditions{Terminal: stdinIsTerminal(cmd.InOrStdin()), Agent: agentMarker(os.Getenv)}
	switch {
	case conds.Agent != "":
		return userErr("fugaro image refresh applies cloud changes: %s", initflow.AgentRefusal(conds.Agent))
	case !initflow.CanConfirm(initflow.Typed, conds):
		return userErr("fugaro image refresh asks for the project's name at each step and before each billable build, so it needs a real terminal: %s", initflow.NoTerminalAdvice)
	}
	return nil
}

var refreshStepNames = []string{"preflight", "base", "check job", "builds"}

// refreshStopped is decision D12: the step's own error and exit code, which
// steps finished, and the line to rerun.
func refreshStopped(step int, done []string, again string, err error) error {
	finished := "none"
	if len(done) > 0 {
		finished = strings.Join(done, ", ")
	}
	return &ExitError{Code: ExitCode(err), Err: fmt.Errorf("%w; fugaro image refresh stopped at step %d (%s), steps finished: %s; once that is fixed, rerun %s in this checkout: finished steps say No changes",
		err, step, refreshStepNames[step-1], finished, again)}
}

// refreshSteps are steps 2 to 4; tests replace newRefreshSteps.
type refreshSteps struct {
	base     func(ctx context.Context) error
	reload   func(ctx context.Context) (*refreshTarget, error)
	checkJob func(ctx context.Context, t *refreshTarget) error
	builds   func(ctx context.Context, t *refreshTarget) error
}

var newRefreshSteps = func(r *initRun, e *initEngine, p *refreshPlan) refreshSteps {
	return refreshSteps{
		base:     func(ctx context.Context) error { return r.refreshBase(ctx, e, p.kinds) },
		reload:   func(ctx context.Context) (*refreshTarget, error) { return refreshReload(ctx, p) },
		checkJob: func(ctx context.Context, t *refreshTarget) error { return r.refreshCheckJob(ctx, t) },
		builds:   func(ctx context.Context, t *refreshTarget) error { return r.refreshBuilds(ctx, p, t) },
	}
}

// refreshAnchorNote is step 5 (D10): the next command when the checkout
// lacks gcp_project; refresh never writes it.
func refreshAnchorNote(ctx context.Context, w io.Writer, root string, lc *localcfg.Config) {
	if co, err := checkoutProject(ctx, root); err == nil && needsAnchorHint(ctx, co, lc) {
		fmt.Fprintln(w, "next: run fugaro init --anchor in this checkout to add the gcp_project line to fugaro.yaml (it checks the images first; fugaro image refresh never writes fugaro.yaml)")
	}
}

func runImageRefresh(cmd *cobra.Command, o refreshOptions) error {
	ctx := cmd.Context()
	if err := refuseRefreshHere(cmd); err != nil {
		return err
	}
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	again := o.again()
	iopts := &initOptions{cloud: o.cloud, imageSource: o.imageSource, expectDigests: o.expectDigests}
	r := newInitRun(cmd, iopts)
	p, err := refreshPreflight(ctx, o, iopts)
	if err != nil {
		return refreshStopped(1, nil, again, err)
	}
	r.setProject(p.lc)
	spec, err := installOptions(iopts, p.lc)
	if err != nil {
		return refreshStopped(1, nil, again, err)
	}
	e := &initEngine{r: r, lc: p.lc, spec: spec, path: p.lcPath, old: p.lcOld}
	p.print(r.w)
	s := newRefreshSteps(r, e, p)
	done := []string{"preflight"}
	fmt.Fprintln(r.w, "step 2, base:")
	if err := s.base(ctx); err != nil {
		return refreshStopped(2, done, again, err)
	}
	done = append(done, "base")
	fmt.Fprintln(r.w, "step 3, the daily image check job:")
	t, err := s.reload(ctx)
	if err == nil {
		err = s.checkJob(ctx, t)
	}
	if err != nil {
		return refreshStopped(3, done, again, err)
	}
	done = append(done, "check job")
	fmt.Fprintln(r.w, "step 4, builds:")
	if err := s.builds(ctx, t); err != nil {
		return refreshStopped(4, done, again, err)
	}
	refreshAnchorNote(ctx, r.w, p.root, t.lc)
	fmt.Fprintf(r.w, "fugaro image refresh of %s is done\n", p.repo)
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestRefresh|TestImagesStageOnlyKinds|TestFirstBuildsUseSubmitAndWait'`
Expected: PASS.

Two requirements the Task 7 review found, each pinned by a test:

- [ ] **Step 4a: `yes=false` for refresh.** `r.confirm` accepts `--yes`, so the check-job prompt (step 3) would be auto-confirmed under `initOptions{yes: true}`; a probe with that patched the job. Build the `initOptions` for refresh with `yes=false`, whatever the flags say, and test that a run started with `--yes` still asks the check-job question and patches nothing when it is declined.
- [ ] **Step 4b: one local-config path.** Pass the SAME path to the engine (`s.e.path`, where the base step's record is written) and to `refreshReload` (`p.lcPath`, where it is read back). Test the order base -> reload -> check job -> builds, that a decline stops the run before the later steps, and D12's stop message; the test must write the base record through the engine's path and see `refreshReload` read it.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/image_refresh.go internal/cli/image.go internal/cli/image_refresh_test.go
git commit -m "image refresh: the command"
```

---

### Task 9: The refusals point at the one command

**Files:**
- Modify: `internal/cli/init_fugaroyaml.go` (`anchorProblemText`, `reasonConfigBaseCustom`), `internal/cli/recipes_skew.go` (`checkRecipeImage`), `internal/cli/init.go` (Long text, lines 170 to 178)
- Test: `internal/cli/init_anchor_test.go` (lines 154, 264 and 265), `internal/cli/init_fugaroyaml_test.go` (line 399), `internal/cli/recipes_skew_test.go` (lines 46 and 58)

**Interfaces:** the signatures are unchanged; only the texts change (D13).

- [ ] **Step 1: Change the tests to the new texts (they fail)**

`init_anchor_test.go:154`, the second string becomes:

```go
"In order: (1) fugaro image refresh --repo acme/app --workflow app in the checkout, in your own terminal window (it copies this release's web-node base image, points the daily image check job at it and rebuilds the image), (2) fugaro init --anchor"
```

`init_anchor_test.go:264` and `:265`, the list becomes:

```go
[]string{"the local config's base image " + ref + " for kind go is not a release >= 0.4.0", "is never replaced by fugaro init --base or fugaro image refresh",
	"(1) remove base_images.go from " + quoteWord(r.cfg) + " (keep a backup), (2) fugaro image refresh --repo acme/app --workflow app in the checkout, in your own terminal window (it copies this release's go base image, points the daily image check job at it and rebuilds the image), (3) fugaro init --anchor"}
```

`init_fugaroyaml_test.go:399`, the suffix becomes:

```go
"(1) fugaro image refresh --repo acme/app --workflow app in the checkout, in your own terminal window (it copies this release's web-node base image, points the daily image check job at it and rebuilds the image), (2) fugaro init --anchor, before you merge a change that adds gcp_project\n"
```

`recipes_skew_test.go:46`, the list becomes:

```go
[]string{"recipe mine needs a job image whose runner knows recipes (fugaro 0.5.0 or later)",
	"Run fugaro image refresh --repo acme/app --workflow web in its checkout, in your own terminal window", "this release's web-node base image", "--recipe default"}
```

`recipes_skew_test.go:58`: replace `"fugaro init --base <kind>"` with `"it copies this release's base image, points"`.

Run: `go test ./internal/cli/ -run 'TestInitAnchor|TestRepoStageAnchor|TestCheckRecipeImage'`
Expected: FAIL on the old texts.

- [ ] **Step 2: Change the texts**

`anchorProblemText`: replace the `if kind != "" { … }` block and the following `steps = append(steps, fmt.Sprintf("fugaro image build …"), "fugaro init --anchor")` with:

```go
	if kind != "" {
		steps = append(steps, fmt.Sprintf("fugaro image refresh --repo %s --workflow %s in the checkout, in your own terminal window (it copies this release's %s base image, points the daily image check job at it and rebuilds the image)", repo, wf, kind))
	} else {
		steps = append(steps, fmt.Sprintf("fugaro image build --repo %s --workflow %s", repo, wf))
	}
	steps = append(steps, "fugaro init --anchor")
```

Update its doc comment to say: the custom entry removed first when needed, then `fugaro image refresh` (or `image build` when the kind is unknown), then `--anchor`.

`reasonConfigBaseCustom`: change `"is never replaced by fugaro init --base, so"` to `"is never replaced by fugaro init --base or fugaro image refresh, so"`.

`checkRecipeImage`: replace `if kind == "" { kind = "<kind>" }` and the `refuse` closure with:

```go
	base := "this release's base image"
	if kind != "" {
		base = "this release's " + kind + " base image"
	}
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows recipes (fugaro %s or later), but the job image of %s workflow %s %s. "+
			"Run fugaro image refresh --repo %s --workflow %s in its checkout, in your own terminal window (it copies %s, points the daily image check job at it and rebuilds the image)%s",
			subject, recipesSince, spec.Repo, spec.Workflow, why, spec.Repo, spec.Workflow, base, alt)
	}
```

`init.go` Long text: replace the lines from `a release image init copied, at or after that release, since the next build` through `fugaro init --anchor. The image checks trust the build records (written by` with:

```
a release image init copied, at or after that release, since the next build
starts from it (a development or hand-pushed one is never replaced by fugaro
init --base or fugaro image refresh: remove its base_images entry first). The
fix it names is: that removal when needed, fugaro image refresh --repo
<owner/name> --workflow <name> in the checkout (it copies the base image,
points the daily image check job at it and rebuilds the image), then fugaro
init --anchor. The image checks trust the build records (written by
```

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestInitAnchor|TestRepoStageAnchor|TestCheckRecipeImage|TestNeedsRecipeImage|TestAnchorProblemText'`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/init_fugaroyaml.go internal/cli/recipes_skew.go internal/cli/init.go internal/cli/init_anchor_test.go internal/cli/init_fugaroyaml_test.go internal/cli/recipes_skew_test.go
git commit -m "init --anchor, recipe check: name fugaro image refresh"
```

---

### Task 10: Docs and skills

**Files:**
- Modify: `docs/gcp-setup.md` (after line 161; lines 480 to 482), `docs/recipes.md:108`, `docs/release.md:107` and `:109`, `.claude/skills/new-release/SKILL.md:56`, `plugin/skills/working/reference/followup.md:121`
- Create: `internal/cli/docs_refresh_test.go`

- [ ] **Step 1: Write the failing docs test**

```go
package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsNameImageRefresh: the operator docs give the one command, not the
// four-step sequence, and every flag they give it is real.
func TestDocsNameImageRefresh(t *testing.T) {
	cmd := newImageRefreshCmd()
	for _, path := range []string{"../../docs/gcp-setup.md", "../../docs/recipes.md", "../../docs/release.md", "../../.claude/skills/new-release/SKILL.md",
		"../../plugin/skills/working/reference/followup.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(data)
		if !strings.Contains(doc, "fugaro image refresh") {
			t.Errorf("%s never names fugaro image refresh", path)
		}
		if strings.Contains(doc, "from outside the checkout") {
			t.Errorf("%s still gives the init --base from outside the checkout sequence", path)
		}
		for _, line := range strings.Split(doc, "\n") {
			_, rest, ok := strings.Cut(line, "fugaro image refresh")
			for ok {
				for _, f := range strings.Fields(strings.SplitN(rest, "`", 2)[0]) {
					if name, isFlag := strings.CutPrefix(f, "--"); isFlag && cmd.Flags().Lookup(strings.TrimRight(name, ",.;)")) == nil {
						t.Errorf("%s: fugaro image refresh has no flag %s", path, f)
					}
				}
				_, rest, ok = strings.Cut(rest, "fugaro image refresh")
			}
		}
	}
}
```

Run: `go test ./internal/cli/ -run TestDocsNameImageRefresh`
Expected: FAIL: `docs/gcp-setup.md never names fugaro image refresh`.

- [ ] **Step 2: Edit the docs**

- `docs/gcp-setup.md`, a new paragraph after line 161 (the "Release images" paragraph):

  > **Moving a repository to a new release: `fugaro image refresh`.** In the repository's checkout, in your own terminal window, `fugaro image refresh` (optionally `--repo <owner/name>`, which must be the checkout's origin, and `--workflow <name>`, repeatable) does the whole move. It prints its plan first. It copies this release's base image of each selected workflow's kind as `init --base` does, with that step's own confirmation. It points the repository's daily image check job at the new base, both its image and `FUGARO_CHECK_SPEC`'s base images, through the Cloud Run Admin API after a typed confirmation, without Terraform. It writes exactly what `fugaro init --repo` would render, so the next `init --repo` plans no change. It rebuilds each selected workflow whose build record says it was built from another base, each build billable and typed. It ends by naming `fugaro init --anchor` when `fugaro.yaml` lacks `gcp_project:`. A `base_images` entry that is not a release image `fugaro init` copied stops it before anything changes; remove the entry, keeping a backup, then rerun. It is refused in a coding agent's session and without a terminal. A run that stops says which steps finished and what to rerun, and a rerun skips what is done.
- `docs/gcp-setup.md:480` and `:481` (steps 1 and 2) are replaced by one step:

  > 1. **The base image and the job images.** `fugaro image refresh` in the checkout, in your own terminal window (see "Moving a repository to a new release" above). It copies this release's base image and records it, points the daily image check job at it, and rebuilds every workflow not yet built from it. If the local config sets `base_images.<kind>` to anything but a release image `fugaro init` copied (a `dev-<sha>` tag, a hand-pushed image), it stops and names the entry: remove it from the local config (keep a backup) and rerun.

  Line 482 becomes step 2, and its parenthetical fix list reads:

  > (… one line per workflow with its reasons and its fix in order: remove a custom `base_images` entry, `fugaro image refresh --repo <owner/name> --workflow <name>` in the checkout, `fugaro init --anchor`)
- `docs/recipes.md:108`: the sentence that starts `The fix is, in order:` becomes:

  > The fix is `fugaro image refresh --repo <owner/name> --workflow <name>` in the repository's checkout, in your own terminal window (it copies this release's base image, points the daily image check job at it and rebuilds the image).
- `docs/release.md:107`, step 3, becomes:

  > 3. For each repository, in its checkout and your own terminal window: `fugaro image refresh`. It copies the release's base image and records it, points the daily image check job at it (image and `FUGARO_CHECK_SPEC`, no Terraform), and rebuilds every workflow not yet built from it. A derived image takes the `fugaro` binary from the base image, which is why the order matters, and the command keeps it. If the local config sets `base_images.<kind>` to a development (`dev-<sha>`) or hand-pushed image, it stops and names the entry: remove it from the local config (keep a backup) and rerun.
- `docs/release.md:109`: the parenthetical `(exit 1, one ordered fix per workflow: …)` becomes:

  > (exit 1, one ordered fix per workflow: the base-image fix if needed, `fugaro image refresh` in the checkout, `fugaro init --anchor`)
- `.claude/skills/new-release/SKILL.md:56`: the parenthetical after `rebuilding job images` becomes:

  > (per repository, in its checkout: `fugaro image refresh`, which stops on a local config `base_images.<kind>` that is a dev or hand-pushed image until that entry is removed)
- `plugin/skills/working/reference/followup.md:121`: the row's last cell becomes:

  > The repository's image is older than the CLI. The user runs `fugaro image refresh` in the repository's checkout, in their own terminal window, not through the agent.

Every mention of the command is in backticks, which the skills lint (`TestSkillFilesQuoteCommands`) and this docs test's flag check (which reads up to the closing backtick) both rely on.

- [ ] **Step 3: Run the docs and rules tests**

Run: `go test ./internal/cli/ -run 'TestDocsNameImageRefresh|TestRecipesGuideMatchesTheCLI' && go test ./plugin/ ./schemas/`
Expected: PASS (the skills lint accepts `fugaro image refresh` inline, as it accepts `fugaro image build`).

- [ ] **Step 4: Commit**

```bash
git add docs/gcp-setup.md docs/recipes.md docs/release.md .claude/skills/new-release/SKILL.md plugin/skills/working/reference/followup.md internal/cli/docs_refresh_test.go
git commit -m "docs: one command, fugaro image refresh, instead of the four-step sequence"
```

---

### Task 11: The full suite and the PR

- [ ] **Step 1:** `go vet ./... && go test ./...`. Expected: PASS. The `internal/cli` package takes about 6 minutes. `go test -tags terraform ./internal/infra/` is unchanged by this PR (D7) and is run by CI's `terraform` job.
- [ ] **Step 2:** Push the branch and open the PR. In the PR body, give the decisions D1 to D14 in one line each and name D7's unverified live behaviour.
- [ ] **Step 3:** Read every CI check (`test`, `terraform`, `rules`), each job's log and not only the summary, before asking for the merge.

---

### Task 12: Release 0.5.1 (after the merge)

- [ ] **Step 1:** Write `docs/releases/v0.5.1.md` through a PR to `main`:

```markdown
- `fugaro image refresh`, run in a repository's checkout, moves it onto this release's base image in one command: it copies the base image, points the daily image check job at it (no Terraform), rebuilds each job image not yet built from it, and names `fugaro init --anchor` when `gcp_project:` is still missing.
- It prints its plan first, keeps every confirmation (each build is billable and typed), stops on a development or hand-pushed base image in your local config with the one fix, and resumes where it stopped.
- The `fugaro init --anchor` refusal and the recipe image check now name `fugaro image refresh` instead of the four-step sequence.
```

- [ ] **Step 2:** `/new-release 0.5.1`.
- [ ] **Step 3, the user's live check on the sandbox repository only** (never EdgeWeb or EdgeServer), in their own terminal:
  1. `fugaro image refresh`;
  2. `fugaro init --repo --plan-only`, which must show no change to the check job.

  This verifies D7's unverified points, on the sandbox only:
  - **The raw round-tripped body is accepted.** Cloud Run v2 takes the job as read, output-only fields included (`uid`, `generation`, `etag`, `terminalCondition` and the like), with no update mask. If it rejects them, the fix is to drop the offending output-only keys from the body in `PlanCheckJob` (as the execution tokens are), pinned by a test.
  - **The body etag is enforced.** `Job.etag` is output-only in the proto, so the server may ignore it: the fake always enforces it, which would hide that the concurrent-change refusal does nothing live. Test it by reading the job, changing it another way (for example `gcloud run jobs update` of a label), then sending the stale body: the expected answer is 409 or 412. If the server accepts it, the refusal is not real. Fallback: say so in the confirmation text and the docs (a concurrent `init --repo` can be overwritten, and the next `init --repo` restores its own rendering), or narrow the window by re-reading the job just before the patch and refusing if its `updateTime`, `generation` or `etag` moved; do not claim a guarantee the API does not give.
  - **No execution starts as a side effect.** After the patch, `gcloud run jobs executions list --job <check job>` shows no new execution, with the job's execution tokens dropped from the body as the code does.
  - **The provider sees no diff** afterwards (`init --repo --plan-only` above).
