# Shared Installation Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A teammate with a launcher or operator role, in a checkout of an onboarded repository, on a fresh machine, uses every cloud command with no setup, by reading a validated, cached, non-secret `fugaro/config.yaml` that `fugaro init` publishes to the runs bucket.

**Architecture:** `init` publishes a subset of the local config (`Config.Shared()`) to `fugaro/config.yaml`. Project selection (`localcfg.Select`) gets an optional fetch hook, used only by cloud commands, that runs when no local config exists and the checkout's `fugaro.yaml` (or `--gcp-project`) names the GCP project. The fetched file is validated against the bucket's marker and the GCP project (`ParseShared`), cached in `$XDG_CACHE_HOME/fugaro/shared-config/`, and refused, never repaired, on any mismatch.

**Tech Stack:** Go (the repo), `gocloud.dev/blob` via `internal/blobx`, yaml.v3 strict decoding, the existing `gcpfake`/file:// bucket test fixtures.

**Spec:** `docs/design/shared-config.md` (approved 2026-10-06; Task 0 amends it where the code read showed a problem).

## Global Constraints

- The published subset excludes `terraform:`, `user:`, `endpoints:`, `bucket_url` and the legacy `registry`. Never secrets.
- Shared file read cap: 64 KiB (`SharedMaxBytes = 64 << 10`). Marker read cap: 4 KiB (`markerMaxBytes`).
- Cache: `$XDG_CACHE_HOME/fugaro/shared-config/<name>.json` else `$HOME/.cache/...` (the `getenv` pattern of `localcfg/checkcache.go`, NOT `os.UserCacheDir`); fresh for 24 h, usable offline for 7 days with a warning.
- The GCP project ID comes only from the checkout's committed `fugaro.yaml` `gcp_project:` or the `--gcp-project` flag. **Never guessed from the project name, and no hint of a guess in any message.**
- The `gcp_project:` value is untrusted until it matches the GCP project ID pattern (`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`); it is validated BEFORE it is used to build a bucket name.
- A local `projects/<name>.yaml` and `--config` always win; `validate` and `config example` stay offline (no fetch).
- Release rollout order (spec §9) is documented, not automated: release, `init` (publish), rebuild images, then `init --repo` adds the line.
- Commits end with the attribution lines the session was given; one commit per task minimum, tests first.

## Review Focus

Failure modes the spec implies but no happy-path test exercises, most likely first. Each has a pinning test in the owning task:

1. A `gcp_project:` that is malformed or hostile (uppercase, `../`, a slash, 300 characters) must be refused before any bucket name is built (Task 1 and 3).
2. A marker or config object that exists but the caller cannot read (403) must give an error naming access, not a panic or a stale cache hit (Task 3).
3. A command that writes the local config (for example `budget set`) running against a shared-config selection must refuse, not write to an empty path (Task 4).
4. A negative cache age (clock skew) and a half-written cache file must be treated as a miss (Task 3).
5. A local `projects/<name>.yaml` that disagrees with the shared file must win silently, with `doctor` reporting the difference (Task 4).
6. A shared file with no repos, or at exactly the size cap, loads; one byte over is refused (Task 3).

---

## File Structure

| File | Responsibility |
|---|---|
| `docs/design/shared-config.md` | Amended in Task 0 |
| `internal/config/config.go`, `validate.go`, `project.go` | `gcp_project:` field, validation, lenient reader (Task 1) |
| `internal/localcfg/select.go` | `Checkout.GCPProject`, `SelectInput.GCPProject` and `.Shared` hook, fallback in `named` (Tasks 1, 4) |
| `internal/localcfg/shared.go` (new) | `Config.Shared()`, `SharedKey`, `SharedMaxBytes`, forbidden-key scan (Task 2) |
| `internal/localcfg/sharedcache.go` (new) | cache file read/write, TTLs (Task 3) |
| `internal/cli/sharedcfg.go` (new) | `ParseShared`, `fetchSharedConfig`, `publishShared` (Tasks 2, 3) |
| `internal/cli/project.go` | `checkoutProject` reads `gcp_project`; `selectNamed` wires the hook (Tasks 1, 4) |
| `internal/cli/init.go`, `init_repo_stage.go` | `--publish-config`, publish after `writeConfig`, write the `gcp_project:` line (Tasks 2, 5) |
| `internal/cli/doctor.go` | information line, skip the early "no installation" return (Task 4) |
| `docs/gcp-setup.md`, `docs/release.md`, `docs/gcp-live-checklist.md` | docs, rollout, Check 28 (Task 6) |

Run tasks **sequentially** in `/Users/dimi/git.lattica/Fugaro/.worktrees/shared-config` (branch `design-shared-config`). Signatures below come from a read of v0.3.1; each implementer must confirm them with a quick read before editing (line numbers drift).

---

### Task 0: Amend the spec

**Files:** Modify `docs/design/shared-config.md`.

- [ ] **Step 1: Edit §5 sequence step 1.** Replace the marker-read sentence with: "Read the marker with the same plain bucket read `checkCloudName` uses (`blobx`, at most 4 KiB, `Version == 1`), and require its name and GCP project to equal the checkout's. `infra.ReadProjectMarker` is not used: it needs the project number from Cloud Resource Manager (`projects.get`), which a plain launcher's custom role does not have, and the launcher is the primary user. The trust level is the status quo: every cloud command already trusts this marker through `checkCloudName`, which runs again after selection."
- [ ] **Step 2: Edit §6** to say a stale cache is refreshed by re-reading the object (at most 64 KiB) and comparing generations; there is no conditional-read primitive in `blobx`, and the extra read is small.
- [ ] **Step 3: Edit §12** to state the cache location follows `localcfg/checkcache.go` (`getenv`, `XDG_CACHE_HOME`, else `$HOME/.cache`), not `os.UserCacheDir`.
- [ ] **Step 4: Commit.**

```bash
git add docs/design/shared-config.md
git commit -m "docs: shared-config design: marker read without Cloud Resource Manager, cache location"
```

---

### Task 1: `gcp_project:` in fugaro.yaml and the checkout

**Files:**
- Modify: `internal/config/config.go:22-37` (field), `internal/config/validate.go:97-101` (rule), `internal/config/project.go` (reader)
- Modify: `internal/localcfg/select.go:14` (`Checkout`), `internal/cli/project.go:29-52` (`checkoutProject`)
- Test: `internal/config/project_test.go`, `internal/config/validate_test.go`, `internal/cli/project_test.go`

**Interfaces:**
- Produces: `config.Config.GCPProject string` (`yaml:"gcp_project,omitempty"`); `config.GCPProjectOf(data []byte) (string, error)`; `config.GCPProjectRE *regexp.Regexp`; `localcfg.Checkout.GCPProject string`.

- [ ] **Step 1: Write failing tests.**

```go
// internal/config/project_test.go
func TestGCPProjectOf(t *testing.T) {
	cases := []struct{ name, in, want string; wantErr bool }{
		{"absent", "project: belong\n", "", false},
		{"present", "project: belong\ngcp_project: fugaro-belong\n", "fugaro-belong", false},
		{"null", "project: belong\ngcp_project:\n", "", false},
		{"not a string", "project: belong\ngcp_project: [a]\n", "", true},
		{"duplicate", "project: belong\ngcp_project: a-b-cde\ngcp_project: a-b-cdf\n", "", true},
	}
	for _, c := range cases {
		got, err := GCPProjectOf([]byte(c.in))
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s: got %q, %v", c.name, got, err)
		}
	}
}

// internal/config/validate_test.go
func TestValidateGCPProject(t *testing.T) {
	for _, bad := range []string{"Fugaro-Belong", "../x", "a/b", "ab", strings.Repeat("a", 31), "1abcde"} {
		c := minimalValidConfig(t) // the file's existing helper for a valid Config
		c.GCPProject = bad
		if probs := Validate(&c); !hasField(probs, "gcp_project") {
			t.Errorf("%q accepted", bad)
		}
	}
	c := minimalValidConfig(t)
	c.GCPProject = "fugaro-belong"
	if probs := Validate(&c); hasField(probs, "gcp_project") {
		t.Errorf("valid ID refused: %v", probs)
	}
}
```

(Use the existing test helpers in `validate_test.go` for a valid config and a field lookup; if their names differ, adapt, do not add new global helpers.)

- [ ] **Step 2: Run to confirm failure:** `go test ./internal/config -run 'GCPProject' -count=1` (FAIL: undefined `GCPProjectOf`).
- [ ] **Step 3: Implement.** In `project.go` add, mirroring `ProjectOf`:

```go
// GCPProjectRE is the shape of a GCP project ID (the same pattern infra.ValidProjectID uses).
var GCPProjectRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// GCPProjectOf reads the top-level gcp_project: of a fugaro.yaml leniently
// (like ProjectOf), "" when absent. It does not validate the value.
func GCPProjectOf(data []byte) (string, error) { /* same node loop as ProjectOf, key "gcp_project", error text "gcp_project: must be a string (a GCP project ID)" */ }
```

Factor the shared node loop into one unexported helper `topLevelString(data []byte, key string) (string, error)` used by both. In `config.go` add `GCPProject string \`yaml:"gcp_project,omitempty"\`` beside `Project`. In `validate.go` after the project rule: `if c.GCPProject != "" && !GCPProjectRE.MatchString(c.GCPProject) { add("gcp_project", "must be a GCP project ID: 6 to 30 of a-z, 0-9 and '-', starting with a letter") }`. In `select.go` add `GCPProject string // its fugaro.yaml's gcp_project:, "" when absent` to `Checkout`. In `checkoutProject` after `ProjectOf`: `gcp, err := config.GCPProjectOf(data)`; on error `userErr("%s can't say which GCP project it names: %v", ...)`; return it in the `Checkout`. Also make the parse path call `GCPProjectOf` so strict and lenient agree, as `ProjectOf` does.
- [ ] **Step 4: Write and run the checkout test** (`internal/cli/project_test.go`): a temp git repo whose `fugaro.yaml` carries `gcp_project: fugaro-belong` yields `Checkout.GCPProject == "fugaro-belong"`; one with `gcp_project: ../x` returns a user error. Run `go test ./internal/config ./internal/localcfg ./internal/cli -run 'GCPProject|Checkout' -count=1`; expect PASS.
- [ ] **Step 5: Confirm the runner accepts the field:** `go test ./internal/config -count=1` (the strict parser now knows the key; an existing fixture with it parses with no problems). Add that fixture test if absent.
- [ ] **Step 6: Commit.** `git add internal && git commit -m "config: optional gcp_project in fugaro.yaml; the checkout carries it"`

---

### Task 2: The published subset and the publisher

**Files:**
- Create: `internal/localcfg/shared.go`, `internal/cli/sharedcfg.go` (publisher part)
- Modify: `internal/cli/init.go` (`--publish-config` flag at the `config-only` flag site, `check()` exclusion slice, call after `writeConfig`), `internal/infra/project.go` (add `SharedConfigObject = "fugaro/config.yaml"`)
- Test: `internal/localcfg/shared_test.go`, `internal/cli/sharedcfg_test.go`, a golden file `internal/localcfg/testdata/shared.golden.yaml`

**Interfaces:**
- Produces: `localcfg.SharedMaxBytes = 64 << 10`; `func (c *Config) Shared() *Config` (a copy with `Terraform`, `User`, `Endpoints`, `Bucket`, `Registry` zeroed); `infra.SharedConfigObject`; `func publishShared(ctx context.Context, lc *localcfg.Config) error`; `initOptions.publishConfig bool`.

- [ ] **Step 1: Write the failing golden and exclusion tests.**

```go
func TestSharedSubsetNeverLeaksOwnerOrPersonalFields(t *testing.T) {
	lc := fullLocalConfig(t) // a Config with terraform launchers/operators/alert_email, user:, endpoints.no_auth, bucket_url, registry, budget with api key
	out, err := lc.Shared().Marshal()
	if err != nil { t.Fatal(err) }
	for _, forbidden := range []string{"terraform:", "launchers", "alert_email", "user:", "endpoints:", "bucket_url", "registry:"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("published file contains %q:\n%s", forbidden, out)
		}
	}
	golden(t, "shared.golden.yaml", out) // use the package's existing golden helper, else compare to the file
	if _, err := Parse(out); err != nil { t.Errorf("published file does not parse: %v", err) }
}
```

- [ ] **Step 2: Run to confirm failure:** `go test ./internal/localcfg -run Shared -count=1` (FAIL: undefined `Shared`).
- [ ] **Step 3: Implement `shared.go`.**

```go
package localcfg

// SharedMaxBytes caps the published file when read back.
const SharedMaxBytes = 64 << 10

// Shared is the installation-wide, non-secret part of the config, the part
// that is published to the runs bucket. It drops what is owner-only
// (terraform:), personal (user:, endpoints:) or derived (bucket_url, the
// legacy registry). The receiver is not modified.
func (c *Config) Shared() *Config {
	s := *c
	s.Terraform = Terraform{}
	s.User = ""
	s.Endpoints = Endpoints{}
	s.Bucket = ""
	s.Registry = ""
	return &s
}
```

Deep-copy maps and the `Budget`, `Watch` and `Repos` values if `Config` has pointer or map fields that `Marshal` could alias (it only reads, so a shallow copy is enough; keep it shallow and say so in the comment).
- [ ] **Step 4: Write the publisher test** (`sharedcfg_test.go`) against a `file://` runs bucket (see `newCloudFixture`): after `publishShared`, `fugaro/config.yaml` exists in `<dir>/runs`, parses with `localcfg.Parse`, equals `lc.Shared().Marshal()`, and a second call overwrites it. Then implement:

```go
func publishShared(ctx context.Context, lc *localcfg.Config) error {
	data, err := lc.Shared().Marshal()
	if err != nil { return err }
	if len(data) > localcfg.SharedMaxBytes {
		return fmt.Errorf("the shared config is %d bytes, over the %d byte limit", len(data), localcfg.SharedMaxBytes)
	}
	b, err := sharedBucketOpener(ctx, lc.BucketURL())
	if err != nil { return err }
	defer b.Close()
	return b.Bucket.WriteAll(ctx, infra.SharedConfigObject, data, &blob.WriterOptions{ContentType: "application/yaml"})
}

// sharedBucketOpener is a test seam; production opens the bucket with blobx.
var sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) { return blobx.Open(ctx, url) }
```

- [ ] **Step 5: Wire into `init`.** Add `publishConfig bool` to `initOptions`, declare `--publish-config` beside `--config-only` ("publish the shared config to the runs bucket and stop"), add it to the mutual-exclusion slice in `check()` and its message, and make it require an existing local config. `writeConfig` takes the run's `ctx` (update its three callers at the sites the exploration listed) and, after a successful `writeLocalConfig` with no `--plan-only`, calls `publishShared`; a failure is `r.warn("could not publish the shared config: "+err.Error()+" (teammates will need fugaro init until it is published)")`, never fatal. `--publish-config` alone calls only `publishShared` for the selected config.
- [ ] **Step 6: Test the wiring:** a `config-only` init test asserts the object appears; `--publish-config` with `--config-only` is rejected with the exclusion message; a publish failure (read-only bucket dir) warns but exits 0. Run `go test ./internal/localcfg ./internal/cli -count=1 -run 'Shared|Publish|Init'`; expect PASS.
- [ ] **Step 7: Commit.** `git commit -m "init: publish the shared config to the runs bucket; --publish-config"`

---

### Task 3: Fetch, validate and cache (security-critical; gets a second independent reviewer)

**Files:**
- Create: `internal/localcfg/sharedcache.go`, extend `internal/cli/sharedcfg.go` (`ParseShared`, `fetchSharedConfig`)
- Test: `internal/localcfg/sharedcache_test.go`, `internal/cli/sharedcfg_validate_test.go`, `internal/cli/sharedcfg_fetch_test.go`

**Interfaces:**
- Consumes: `localcfg.Parse`, `infra.CheckFirebaseOutputsFor(FirebaseOutputs, "", false)`, `infra.ProjectMarker`, `infra.ProjectMarkerObject`, `infra.SharedConfigObject`, `blobx.Bucket.Read(ctx, key) ([]byte, int64, error)`, `blobx.ErrNotExist`, `config.GCPProjectRE`.
- Produces:
  - `type SharedAnchor struct{ Name, GCPProject, Bucket string }`
  - `func ParseShared(data []byte, a SharedAnchor) (*localcfg.Config, error)`
  - `func fetchSharedConfig(ctx context.Context, getenv func(string) string, now time.Time, name, gcpProject string) (*localcfg.Config, string, error)` (returns the config and a note such as "using the cached shared config, 3 days old"; the note is empty on a fresh read)
  - `localcfg.SharedCacheEntry{GCPProject, Bucket string; Generation int64; CheckedAt time.Time; YAML string}`, `LoadSharedCache(getenv, name) (SharedCacheEntry, bool)`, `SaveSharedCache(getenv, name, e) error`, consts `SharedFreshFor = 24 * time.Hour`, `SharedOfflineFor = 7 * 24 * time.Hour`.

- [ ] **Step 1: Write the validation table test first (the security core).**

```go
func validShared() string { /* the belong-shaped YAML from the design: name belong, gcp_project fugaro-belong,
   region us-east5, runs_bucket fugaro-runs-fugaro-belong, registry_host us-east5-docker.pkg.dev/fugaro-belong,
   one base image under it, log_view projects/fugaro-belong/..., budget (observe, rtdb_url
   https://fugaro-belong-default-rtdb.firebaseio.com, firebase_project fugaro-belong, token_signer
   fugaro-token-signer@fugaro-belong.iam.gserviceaccount.com, plus the firebase_api_key field), one repo */ }

var anchor = SharedAnchor{Name: "belong", GCPProject: "fugaro-belong", Bucket: "fugaro-runs-fugaro-belong"}

func TestParseSharedRefusals(t *testing.T) {
	cases := []struct{ name string; mutate func(string) string; want string }{
		{"other name", func(s string) string { return strings.Replace(s, "name: belong", "name: other", 1) }, "name"},
		{"other gcp project", func(s string) string { return strings.Replace(s, "gcp_project: fugaro-belong", "gcp_project: fugaro-other", 1) }, "gcp_project"},
		{"other bucket", func(s string) string { return strings.Replace(s, "runs_bucket: fugaro-runs-fugaro-belong", "runs_bucket: elsewhere-bucket", 1) }, "runs_bucket"},
		{"foreign registry", func(s string) string { return strings.ReplaceAll(s, "us-east5-docker.pkg.dev/fugaro-belong", "us-east5-docker.pkg.dev/evil-proj") }, "registry"},
		{"foreign base image", func(s string) string { return strings.Replace(s, "us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-web-node:0.3.1", "evil.example/x:1", 1) }, "base_images"},
		{"foreign log view", func(s string) string { return strings.Replace(s, "projects/fugaro-belong/", "projects/evil-proj/", 1) }, "log_view"},
		{"signer outside the firebase project", func(s string) string { return strings.Replace(s, "token-signer@fugaro-belong", "token-signer@evil-proj", 1) }, "token_signer"},
		{"database host foreign", func(s string) string { return strings.Replace(s, "https://fugaro-belong-default-rtdb.firebaseio.com", "https://evil.example.com", 1) }, "rtdb_url"},
		{"terraform section", func(s string) string { return s + "terraform:\n    state_bucket: x\n" }, "terraform"},
		{"user key", func(s string) string { return s + "user: a@b.example\n" }, "user"},
		{"endpoints section", func(s string) string { return s + "endpoints:\n    no_auth: true\n" }, "endpoints"},
		{"unknown key", func(s string) string { return s + "bogus: 1\n" }, "bogus"},
		{"oversize", func(s string) string { return s + "#" + strings.Repeat("x", localcfg.SharedMaxBytes) + "\n" }, "64 KiB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseShared([]byte(c.mutate(validShared())), anchor)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error mentioning %q, got %v", c.want, err)
			}
		})
	}
}

func TestParseSharedAcceptsTheValidFileAndTheSizeCap(t *testing.T) {
	c, err := ParseShared([]byte(validShared()), anchor)
	if err != nil || c.Name != "belong" { t.Fatalf("%v %v", c, err) }
	pad := localcfg.SharedMaxBytes - len(validShared()) - 2
	if _, err := ParseShared([]byte(validShared()+"#"+strings.Repeat("x", pad)+"\n"), anchor); err != nil {
		t.Errorf("a file exactly at the cap was refused: %v", err)
	}
}
```

- [ ] **Step 2: Run to confirm failure:** `go test ./internal/cli -run ParseShared -count=1` (FAIL: undefined).
- [ ] **Step 3: Implement `ParseShared`.**

```go
func ParseShared(data []byte, a SharedAnchor) (*localcfg.Config, error) {
	if len(data) > localcfg.SharedMaxBytes {
		return nil, fmt.Errorf("the shared config is over the 64 KiB limit")
	}
	// Forbidden sections are refused by key, before Parse, so a section that
	// decodes to its zero value cannot hide.
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("the shared config is not valid YAML: %w", err)
	}
	for _, k := range []string{"terraform", "user", "endpoints", "bucket_url", "registry"} {
		if _, ok := top[k]; ok {
			return nil, fmt.Errorf("the shared config carries %s:, which is never published; it was not written by fugaro init", k)
		}
	}
	c, err := localcfg.Parse(data) // strict: an unknown key is refused here
	if err != nil {
		return nil, fmt.Errorf("the shared config: %w", err)
	}
	bad := func(f, why string) (*localcfg.Config, error) {
		return nil, fmt.Errorf("the shared config's %s %s; ask an operator to run fugaro init again", f, why)
	}
	switch {
	case c.Name != a.Name:
		return bad("name", "is not the project's")
	case c.GCPProject != a.GCPProject:
		return bad("gcp_project", "is not the project's")
	case c.RunsBucketName() != a.Bucket:
		return bad("runs_bucket", "is not the bucket it was read from")
	case c.RegistryHost != c.Region+"-docker.pkg.dev/"+a.GCPProject:
		return bad("registry_host", "is not this project's registry")
	case c.LogView != "" && !strings.HasPrefix(c.LogView, "projects/"+a.GCPProject+"/"):
		return bad("log_view", "is not under this project")
	}
	for kind, ref := range c.BaseImages {
		if !strings.HasPrefix(ref, c.RegistryHost+"/") {
			return bad("base_images."+kind, "is not in this project's registry")
		}
	}
	if b := c.Budget; b != nil && (b.RTDBURL != "" || b.FirebaseProject != "" || b.TokenSigner != "") {
		o := infra.FirebaseOutputs{RTDBURL: b.RTDBURL, FirebaseAPIKey: b.FirebaseAPIKey, TokenSigner: b.TokenSigner, FirebaseProject: b.FirebaseProject}
		if err := infra.CheckFirebaseOutputsFor(o, "", false); err != nil {
			return bad("budget block", "fails the Firebase checks ("+err.Error()+")")
		}
	}
	return c, nil
}
```

The messages must contain the strings the table expects (`name`, `gcp_project`, `runs_bucket`, `registry`, `base_images`, `log_view`, `token_signer`, `rtdb_url`, the section names, `64 KiB`). If `CheckFirebaseOutputsFor`'s own error text lacks `token_signer` or `rtdb_url`, wrap so the field name appears.
- [ ] **Step 4: Run to confirm the table passes:** `go test ./internal/cli -run ParseShared -count=1`.
- [ ] **Step 5: Write the cache tests** (`sharedcache_test.go`) with `XDG_CACHE_HOME` in a temp dir: save then load is a hit within 24 h; a hit has the same generation and YAML; an entry older than `SharedFreshFor` reports not fresh but loadable (the caller decides on `SharedOfflineFor`); a negative age (`CheckedAt` in the future) and a truncated file are misses; the name is validated with `config.ProjectNameRE` (a name with `../` returns an error); the file is mode 0600 in a 0700 directory and written atomically. Implement `sharedcache.go` by copying the `checkcache.go` structure (temp file `.shared-*`, chmod, rename), storing `SharedCacheEntry` as JSON under `shared-config/<name>.json`.
- [ ] **Step 6: Write the fetch tests** (`sharedcfg_fetch_test.go`, using a `file://` runs bucket via the `sharedBucketOpener` seam and `isolateProjects`-style env isolation):

```go
func TestFetchSharedHappyPathCachesAndSecondCallDoesNotTouchTheBucket(t *testing.T) { /* write marker + config, fetch ok; delete the bucket files; fetch again within the day still ok from cache */ }
func TestFetchSharedRefusesAMarkerThatNamesAnotherProject(t *testing.T) { /* marker name differs -> error, nothing cached */ }
func TestFetchSharedMissingConfigObject(t *testing.T) { /* marker ok, config.yaml absent -> error "has not published a shared config; ask an operator to run fugaro init" */ }
func TestFetchSharedUnreadableBucketNamesAccess(t *testing.T) { /* opener returns a permission error -> error mentions access and the bucket, no panic, an existing cache older than 7d is NOT used */ }
func TestFetchSharedOfflineUsesCacheUpToSevenDays(t *testing.T) { /* stale cache + failing opener: day 3 -> config + note "cached"; day 8 -> error */ }
func TestFetchSharedStaleCacheRefreshesWhenGenerationChanged(t *testing.T) { /* rewrite config.yaml; after 25h the new content wins and the cache is updated */ }
func TestFetchSharedInvalidNewContentDropsTheCache(t *testing.T) { /* tampered new content -> error AND the cache file is gone */ }
func TestFetchSharedRejectsMalformedGCPProjectBeforeBuildingABucketName(t *testing.T) { /* gcpProject "../x" and "A-B" -> error, and the opener was never called */ }
```

Implement `fetchSharedConfig`:

```go
func fetchSharedConfig(ctx context.Context, getenv func(string) string, now time.Time, name, gcp string) (*localcfg.Config, string, error) {
	if !config.GCPProjectRE.MatchString(gcp) {
		return nil, "", userErr("%q is not a GCP project ID", gcp)
	}
	bucket := "fugaro-runs-" + gcp
	anchor := SharedAnchor{Name: name, GCPProject: gcp, Bucket: bucket}
	cached, haveCache := localcfg.LoadSharedCache(getenv, name)
	if haveCache && cached.GCPProject == gcp && cached.Bucket == bucket && now.Sub(cached.CheckedAt) < localcfg.SharedFreshFor {
		if c, err := ParseShared([]byte(cached.YAML), anchor); err == nil {
			return c, "", nil
		}
	}
	c, gen, yamlData, err := readSharedFromBucket(ctx, bucket, anchor) // marker (4 KiB, Version 1, Name+GCPProject equal) then config.yaml (at most 64 KiB), then ParseShared
	switch {
	case err == nil:
		_ = localcfg.SaveSharedCache(getenv, name, localcfg.SharedCacheEntry{GCPProject: gcp, Bucket: bucket, Generation: gen, CheckedAt: now, YAML: string(yamlData)})
		return c, "", nil
	case isUnreachable(err) && haveCache && cached.GCPProject == gcp && cached.Bucket == bucket && now.Sub(cached.CheckedAt) <= localcfg.SharedOfflineFor:
		if c, perr := ParseShared([]byte(cached.YAML), anchor); perr == nil {
			return c, fmt.Sprintf("using the cached shared config, %d days old: the bucket is unreachable", int(now.Sub(cached.CheckedAt).Hours()/24)), nil
		}
	}
	_ = localcfg.DropSharedCache(getenv, name) // a validation failure or a vanished object must not leave a usable cache
	return nil, "", err
}
```

`isUnreachable` is true for network and timeout errors and false for permission, not-found and validation errors (a 403 must not fall back to the cache). `readSharedFromBucket` reads the marker exactly as `checkCloudName` does (`b.Read` or `NewReader` with `io.LimitReader(r, markerMaxBytes+1)`, `Version == 1`, `ProjectNameRE`, name and `gcp_project` equal to the anchor) and returns precise messages: missing marker -> "no Fugaro installation at gs://<bucket>"; missing config object -> "project <name> has not published a shared config; ask an operator to run fugaro init". Add `DropSharedCache`.
- [ ] **Step 7: Run all of it:** `go test ./internal/localcfg ./internal/cli -count=1 -run 'Shared'`; expect PASS.
- [ ] **Step 8: Commit.** `git commit -m "shared config: validated, cached fetch from the runs bucket"`

---

### Task 4: Selection fallback, writers that must refuse, and doctor

**Files:**
- Modify: `internal/localcfg/select.go` (`SelectInput`, `named`), `internal/cli/project.go` (`selectNamed`, `selectProject`), `internal/cli/doctor.go`, the commands that write the local config (find with `grep -rn "SaveConfig\|WriteFileAtomic\|writeLocalConfig\|lc.Path\|sel.Path" internal/cli`)
- Test: `internal/localcfg/select_test.go`, `internal/cli/project_shared_test.go`, `internal/cli/doctor_shared_test.go`

**Interfaces:**
- Consumes: `fetchSharedConfig` (Task 3), `Checkout.GCPProject` (Task 1).
- Produces: `SelectInput.GCPProject string`, `SelectInput.Shared func(name, gcpProject string) (*Config, string, error)`; `Selection.From == "shared config"` with `Path == ""`; `cloudOptions.sharedOK bool` (set only by `selectProject`).

- [ ] **Step 1: Write failing selector tests** (pure, no cloud): with a stub `Shared` hook and no local config, the `checkout` case (`named(co.Project, "checkout")`) with `Checkout.GCPProject` set returns the stub's config with `From == "shared config"`; with `GCPProject` empty it returns the existing refusal plus the new line "add `gcp_project: <id>` next to `project:` in fugaro.yaml (whoever onboarded the repository can run fugaro init --repo to add it)" and no guess; `--project` and `FUGARO_PROJECT` selection with `--gcp-project` set also use the hook; a local `projects/<name>.yaml` wins and the hook is never called (assert with a counter); `Shared == nil` (the offline commands) behaves exactly as today.
- [ ] **Step 2: Implement in `select.go`.** In `named`, in the two non-creating `ErrMissing` cases (the checkout one and the generic one), after the `$FUGARO_CONFIG` fallback, call a new helper:

```go
// shared fetches the project's published config when the caller allows it
// (cloud commands) and the GCP project is known from the checkout or --gcp-project.
func (s *selector) shared(name, from string) (Selection, *Config, bool, error) {
	in := s.in
	if in.Shared == nil || in.GCPProject == "" {
		return Selection{}, nil, false, nil
	}
	c, note, err := in.Shared(name, in.GCPProject)
	if err != nil {
		return Selection{}, nil, true, err
	}
	sel := Selection{Name: name, From: "shared config"}
	if note != "" {
		sel.Notes = append(sel.Notes, note)
	}
	return sel, c, true, nil
}
```

If `in.Shared != nil && in.GCPProject == ""` and the case is the checkout one, return the refusal with the "add gcp_project:" line instead of `fugaro init --config-only --gcp-project <id>` only (keep that as the second suggestion for operators). Add `GCPProject` and `Shared` to `SelectInput`.
- [ ] **Step 3: Wire `cli/project.go`.** `selectNamed` fills `GCPProject` from `o.gcpProject` (the `--gcp-project` flag) else `co.GCPProject`, and fills `Shared` with a closure over `fetchSharedConfig(ctx, os.Getenv, time.Now(), ...)` only when `o.sharedOK`. `selectProject` (used by `openCloud`/`openBudgetDB`) sets `o.sharedOK = true` on its local copy; `selectFrom` callers `pricesConfig`, `selectedProjectConfig` (validate, `config example`) and init leave it false. Pass `ctx` through (add a parameter where needed).
- [ ] **Step 4: Writers must refuse.** For every command that writes the local config path, add at its entry: `if sel.From == "shared config" { return userErr("this project's config is the shared one published by an operator, so it can't be changed here; run fugaro init to create your own local config") }`. List the commands found by the grep in the commit message. Test one of them (`budget set` or whichever the grep finds) refuses with no file created.
- [ ] **Step 5: End-to-end test.** Fresh `XDG_CONFIG_HOME` and `XDG_CACHE_HOME`, a `file://` runs bucket holding the marker and a published config, a git checkout whose `fugaro.yaml` has `project: aurora` and `gcp_project: ...`; `execute(t, "ls")` (using the fixture's fake run service) succeeds with no local file and the header line says where the config came from. A second case with the line missing fails with the "add gcp_project:" message.
- [ ] **Step 6: Doctor.** In `runDoctor`, skip the early "no Fugaro installation is configured" return when the checkout names a `gcp_project:`; after selection, when `Selection.From == "shared config"`, append `doctorCheck{ID: "shared-config", Severity: "info", Problem: "the project's config is the shared file published to the runs bucket (generation N, checked <age> ago)", Fix: "fugaro init creates your own local config"}`; when a local file won and a published file exists that differs from it on installation-wide fields, an info line says so (read the published file through the same fetch, tolerating failure silently). Test both lines.
- [ ] **Step 7: Run:** `go test ./internal/localcfg ./internal/cli -count=1`; expect PASS (the full `./internal/cli` suite is slow, allow several minutes).
- [ ] **Step 8: Commit.** `git commit -m "select: fall back to the shared config; writers refuse; doctor reports it"`

---

### Task 5: `init --repo` writes the `gcp_project:` line and warns about old images

**Files:**
- Modify: `internal/cli/init_repo_stage.go` (the repository stage), a new `internal/cli/init_fugaroyaml.go`
- Test: `internal/cli/init_fugaroyaml_test.go`

**Interfaces:**
- Produces: `func setGCPProjectLine(data []byte, id string) (out []byte, changed bool, err error)`; `const gcpProjectFieldSince = "0.4.0"` (the release that adds the field; the release task bumps it if the number differs); `func imagePredates(recordVersion, since string) bool`.

- [ ] **Step 1: Write failing tests.** `setGCPProjectLine` inserts `gcp_project: <id>` on the line after the top-level `project:` line and preserves everything else byte for byte (comments, ordering, trailing newline); is a no-op when it already equals the id; **refuses** (error) when it holds a different value (never silently rewrites a reviewed value); handles `project:` as the last line without a trailing newline; and rejects an id that fails `config.GCPProjectRE`.

```go
func TestSetGCPProjectLine(t *testing.T) {
	in := "# fugaro config\nproject: belong\nworkflows:\n  web: {}\n"
	out, changed, err := setGCPProjectLine([]byte(in), "fugaro-belong")
	want := "# fugaro config\nproject: belong\ngcp_project: fugaro-belong\nworkflows:\n  web: {}\n"
	if err != nil || !changed || string(out) != want { t.Fatalf("%q %v %v", out, changed, err) }
	if _, ch, _ := setGCPProjectLine(out, "fugaro-belong"); ch { t.Error("second call changed the file") }
	if _, _, err := setGCPProjectLine(out, "fugaro-other"); err == nil { t.Error("a differing value was overwritten") }
	if _, _, err := setGCPProjectLine([]byte(in), "../x"); err == nil { t.Error("a bad id was accepted") }
}
func TestImagePredates(t *testing.T) { /* "0.3.1" vs "0.4.0" true; "0.4.0" false; "0.10.0" vs "0.4.0" false (numeric compare); "" true; "dev-abc" false (a non-release build is not warned about) */ }
```

- [ ] **Step 2: Run to confirm failure:** `go test ./internal/cli -run 'GCPProjectLine|ImagePredates' -count=1`.
- [ ] **Step 3: Implement** `setGCPProjectLine` by line scanning for the first line matching `^project:\s` at column 0, inserting after it; `imagePredates` with a numeric three-part compare (reuse the repo's existing semver helper if one exists in `internal/cli` or `scripts`, otherwise a 15-line one in this file).
- [ ] **Step 4: Wire the stage.** After the repository gate has confirmed the onboarding (the existing "writes to the checkout" confirmation covers this write), the stage reads `<root>/fugaro.yaml`, computes the edit with the installation's `lc.GCPProject`, prints `lineDiff(old, new)`, and writes it atomically (`writeFileAtomic`); an error from a differing value is surfaced as a user error naming the line. It also reads the repository's build record (`imagecheck.ReadStatus` or the `Record` path used by `cli/imagecheck.go`) and, when `imagePredates(rec.FugaroVersion, gcpProjectFieldSince)` is true or no record exists, prints a warning: "this repository's job image was built before fugaro.yaml could carry gcp_project: runs and the daily image check will refuse the file until the image is rebuilt; run fugaro image build --repo <r> --workflow <w> BEFORE merging this change". The line is written regardless (the owner merges it only after rebuilding); the warning is the guard. Test the stage with a fake build record on both sides of the version.
- [ ] **Step 5: Run** `go test ./internal/cli -count=1 -run 'GCPProjectLine|ImagePredates|InitRepo'`; expect PASS.
- [ ] **Step 6: Commit.** `git commit -m "init --repo: write gcp_project into fugaro.yaml and warn when the image predates it"`

---

### Task 6: Documentation, rollout and the live check

**Files:** Modify `docs/gcp-setup.md`, `docs/release.md`, `docs/gcp-live-checklist.md`, `docs/design/shared-config.md` (status line).

- [ ] **Step 1: `docs/gcp-setup.md`:** a section "Teammates: no setup" covering what the shared file is, what it excludes, that `init` publishes it, the `gcp_project:` line, the cache and its TTLs, the local-file-wins rule, and how an operator republishes (`fugaro init --publish-config`).
- [ ] **Step 2: `docs/release.md`:** the rollout order of spec §9 as a numbered runbook, with the warning about adding `gcp_project:` before the images are rebuilt.
- [ ] **Step 3: `docs/gcp-live-checklist.md`:** add **Check 28**: on a clean state (a new empty directory used for BOTH `XDG_CONFIG_HOME` and `XDG_CACHE_HOME`, exported explicitly, with a note that a stale `XDG_CONFIG_HOME` sent live config writes to a temp directory during D19), `fugaro init --publish-config` on an installation, then in the sandbox checkout with the line committed `fugaro doctor` shows the shared-config line and `fugaro ls` works; then tamper with the object (as an operator, in a scratch installation only) and see the refusal naming the field.
- [ ] **Step 3b: Spec status line:** "built; Task numbers per docs/plans/2026-10-06-shared-config.md".
- [ ] **Step 4: Verify docs build their checks:** `go test ./... -count=1 -run 'Docs|Doc'` if the repo has doc tests, and `go vet ./...`.
- [ ] **Step 5: Whole-branch gate:** `go build ./... && go test ./... -count=1` (long; the `scripts` package alone takes about 6 minutes locally).
- [ ] **Step 6: Commit.** `git commit -m "docs: shared config, rollout and Check 28"`

---

## Self-review (run by the author)

- **Spec coverage:** §3 approach (Tasks 2, 3), §4 published file and writer (Task 2), §5 sequence and cross-checks (Task 3, amended in Task 0), §6 cache (Task 3), §7 selection, doctor and offline commands (Task 4), §8 `gcp_project:` field and `init --repo` (Tasks 1, 5), §9 hazard and rollout (Tasks 5, 6), §10 tests (every task), §11 reviews (Task 3 second reviewer), live Check 28 (Task 6). The spec's "conditional read by generation" is deliberately a full small re-read (Task 0).
- **Placeholders:** none; the two places that say "the file's existing helper" name the code to read, because their exact test-helper names were not captured.
- **Type consistency:** `SharedAnchor`, `ParseShared`, `fetchSharedConfig`, `SharedCacheEntry`, `LoadSharedCache`/`SaveSharedCache`/`DropSharedCache`, `SelectInput.Shared`/`GCPProject`, `Checkout.GCPProject`, `setGCPProjectLine` and `imagePredates` are used with the same names in every task.
- **Review Focus:** each of the six lines has a pinning test in the owning task (hostile `gcp_project` Task 1 and 3, 403 Task 3, writers Task 4, clock skew and truncation Task 3, local-wins Task 4, size cap Task 3).
