# M2 — Git Providers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `fugaro exec` opens and updates real pull requests on Bitbucket Cloud and GitHub. It authenticates the runner's git and the agent's git (plus `gh` on GitHub) with a short-lived, repo-scoped token that is never written to disk or logged. Draft PRs carry a bounded, redacted log tail.

**Architecture:** Each provider adapter is a small package behind the existing `gitprov.Provider` interface. The interface gains `GitAuth`, which returns the git credentials that design §6.2 calls for. The adapters share a tiny JSON client (`gitprov/httpjson`) and are tested against recorded HTTP fixtures (`gitprov/httpfixture`). A factory (`gitprov/providers`) builds an adapter from credentials in the runner's environment.

The runner's `Deps.Provider` becomes `Deps.OpenProvider`, a `gitprov.Opener`, which bootstrap calls as soon as the provider's kind is known:
- **Before the checkout,** when `--provider` or the origin URL's host names it. The clone and fetch may already need credentials.
- **Otherwise right after `fugaro.yaml` is read,** from `git.provider`.

Every source that names a kind must agree with `git.provider`. Git credentials travel only as environment variables: `GIT_CONFIG_COUNT` config and an env-reading credential helper. Finalize pushes only `fugaro/` branches, with `--force-with-lease`.

```
logtail ─► verify ────────────────────────────────────────────┐
gitops (+ credentials, lease push) ───────────────────────────┤
gitprov ─┬─ fake                                              ├─► runner ─► cli exec
         ├─ httpjson ─┬─ bitbucket ─┐                         │
         │            └─ github ────┴─► providers (FromEnv) ──┘
         └─ httpfixture (tests only)
```

**Tech Stack:**
- Go 1.27, standard library only. The GitHub App JWT is RS256, signed with `crypto/rsa` and `crypto/sha256`.
- `net/http/httptest` for recorded fixtures.
- `net/http/cgi` plus `git http-backend` for a hermetic authenticated git remote in tests.

**Spec:** [docs/design/v1.md](../design/v1.md). Before starting, read these sections:
- §3.1 and §3.2 (components, job model)
- §4.1 finalize, and §4.4
- §4.5 (log tail)
- §6.1 and §6.2 (trust boundary, agent git token)
- §15 (the Bitbucket draft question, which this plan answers in Task 8)

The M1 plan ([2026-09-26-m1-runner-core.md](2026-09-26-m1-runner-core.md)) explains the code this plan changes.

**Out of scope for M2:**
- Follow-up runs: fetching PR comments and checking out an existing branch (M6).
- The live end-to-end run against a sandbox repository. It needs credentials the user has not created yet. Task 8 writes its checklist, and the adapters are built for it: base URLs are injectable, credentials come from env, and `httpfixture.Recorder` turns a live run into fixtures.
- Terraform wiring of the credential secrets (M5).

## Global Constraints

- `go.mod` stays at `go 1.27`, and this plan adds **no new module dependencies**. The JWT uses `crypto/rsa` (RS256), not a JWT library.
- Credentials come only from the runner environment, which the platform injects from Secret Manager. They never come from `fugaro.yaml`. The variable names are fixed:
  - `FUGARO_BITBUCKET_TOKEN`
  - `FUGARO_GITHUB_APP_ID`
  - `FUGARO_GITHUB_APP_PRIVATE_KEY` (or `FUGARO_GITHUB_APP_PRIVATE_KEY_FILE`)
  - the optional overrides `FUGARO_BITBUCKET_API_URL` and `FUGARO_GITHUB_API_URL`
  - `FUGARO_GIT_PROVIDER`, the default for `exec --provider`
- Every credential value goes into the runner's redaction list, whether the agent sees it or not: the Bitbucket token, the App private key, and every minted installation token. Redaction covers transcripts, agent stderr logs, the PR title, body and report, and the `infra_error` reason.
- A git token is never written to `.git/config`, to a remote URL, or to a git command line. It travels only in `FUGARO_GIT_TOKEN`, which an env-reading credential helper answers from. That helper is configured through `GIT_CONFIG_COUNT` and scoped to the origin's `scheme://host`.
- The agent's token is a convenience, not a security boundary (design §6.1). Never add logic that treats it as one.
- The runner pushes only `fugaro/<run-id>` branches. It uses `--force-with-lease` against the run branch only, and never pushes the base branch.
- Design §4.2 is unchanged: the PR is ready if and only if the final HEAD has a passing, clean-tree `fugaro verify test` and the last review verdict is `ship`. Otherwise the PR is a draft, or a `[DRAFT] `-titled PR where drafts are unsupported.
- Tests need no credentials and no network: `httptest` fixtures, and `git http-backend` served locally. Every test that runs git calls `testutil.IsolateGit(t)` first.
- M3 is running in parallel. Do not modify `internal/config`, `images/`, `plugin/`, `internal/config/example.yaml`, or any `internal/cli` file except `exec.go` and `exec_test.go`.
- CI runs `gofmt -l`, `go vet ./...` and `go test -race ./...`. All three must pass after every task.
- Code style follows M1: `gofmt`, doc comments on exported identifiers, and errors wrapped with `%w` and context.

## Review Focus

The design implies these failure modes, but they are easy to miss. Each one is pinned by a test in the task named.

1. **The agent already pushed, or amended, the run branch.** Finalize's push must still land the final HEAD. A tip that anyone else pushed must not be overwritten, and the base branch can never be pushed.
   - Task 2: `TestPushOverwritesTheAgentsOwnPushAfterAmend`, `TestPushRefusesATipFromElsewhere`, `TestPushRefusesNonRunBranches`
   - Task 9: `TestAgentPushAndAmendIsTolerated`
   - Task 11: `TestBitbucketRunOverHTTP`
2. **The agent opened the PR itself** (`gh pr create`, or the Bitbucket API). `EnsurePR` must find it by branch and change only its draft state. It must not create a duplicate or overwrite the agent's title and body.
   - Task 5: `TestExistingPRIsOnlyToggled`
   - Task 7: `TestAgentOpenedPRIsOnlyToggled`
3. **Draft PRs are unavailable.** This covers a GitHub private repository on a plan without drafts (HTTP 422), and Bitbucket ignoring `draft`. The run must still get its PR, titled `[DRAFT] …`, and the prefix must go away once the PR is ready.
   - Task 5: `TestDraftIgnoredFallsBackToTitle`
   - Task 7: `TestDraftUnsupportedFallsBackToTitle`, `TestExistingPRDraftUnsupportedRetitles`, `TestReadyRemovesDraftPrefix`
4. **A transient provider failure at finalize** (a 5xx or a dropped connection) must not leave a pushed branch with no PR. The runner retries, and `EnsurePR` finds what an earlier attempt created, so the run ends with exactly one PR.
   - Task 9: `TestEnsurePRRetried`, `TestEnsurePRGivesUp`
5. **An oversized agent `pr.md`.** A title or body over the provider's limit must be clipped rather than make PR creation fail.
   - Task 10: `TestLongPRTextIsBounded`

## File map

| File | Responsibility | Task |
|---|---|---|
| `internal/logtail/logtail.go` | Bounded, concurrency-safe "last N lines" writer | 1 |
| `internal/verify/verify.go` | Also stores each record's output tail in `verify-logs/NNNN.log`; `LogTail` | 1 |
| `internal/testutil/httpgit.go` | Authenticated HTTP git remote (`git http-backend` over `net/http/cgi`) | 2 |
| `internal/gitops/credentials.go` | `CredentialVars`, `CredentialURL`, `WithVars` | 2 |
| `internal/gitops/gitops.go` | `OriginURL`; lease-based `Push` of `fugaro/` branches only | 2 |
| `internal/gitprov/gitprov.go` | `GitAuth`, `Provider.GitAuth`, `Opener`, `Static`, `PartialError`, `DraftTitle`, `KindForURL`, `SplitRepo` | 3 |
| `internal/gitprov/fake/fake.go` | `GitAuth` (`Auth` hook), `FailEnsure` | 3 |
| `internal/gitprov/httpjson/httpjson.go` | JSON REST client shared by the adapters | 4 |
| `internal/gitprov/httpfixture/httpfixture.go` | Fixture replay server (tests) and `Recorder` (for live runs) | 4 |
| `internal/gitprov/bitbucket/` | Bitbucket Cloud adapter and fixtures | 5 |
| `internal/gitprov/github/apptoken.go` | App JWT (RS256) and the installation-token source | 6 |
| `internal/gitprov/github/github.go` | GitHub adapter (REST, plus GraphQL for draft state) and fixtures | 7 |
| `internal/gitprov/providers/providers.go` | `FromEnv`: builds an adapter from env credentials | 8 |
| `docs/git-providers.md`, `docs/design/v1.md` §15 | Operator docs; the §15 answer | 8 |
| `internal/runner/runner.go` | Lazy provider selection, credentials, refresh, `ensurePR` retry, redacted reasons; log tail; PR text bounds | 9, 10 |
| `internal/runner/report.go` | `LogTail` section in the report | 10 |
| `internal/cli/exec.go` | `--provider` (github, bitbucket, fake, or empty for auto), wired to `providers.FromEnv` | 11 |
| `internal/e2e/bitbucket_test.go` | Binary end to end: Bitbucket adapter against a fake API, and git over authenticated HTTP | 11 |

**Contract with M3 (images).** Record this in the M3 plan if it is not there already. The derived image's baked `/work/repo` must satisfy two conditions:
- Its `origin` is the repository's plain HTTPS URL (`https://bitbucket.org/<ws>/<repo>.git` or `https://github.com/<owner>/<repo>.git`), with no credentials in it. `KindForURL` reads that host, and the credential helper is scoped to it.
- It has no `credential.helper` or `http.extraHeader` left in its config. Fugaro resets the helper anyway.

---
### Task 1: Bounded log tails (`logtail`) and verify output tails

A draft PR carries "the log tail" (design §4.5). A tail's source is either the failing stage's output or the output of the last failed `fugaro verify` run. This task adds the bounded tail writer, and makes `fugaro verify` keep one tail per record. The runner consumes both in Task 10.

**Files:**
- Create: `internal/logtail/logtail.go`, `internal/logtail/logtail_test.go`
- Modify: `internal/verify/verify.go` (constants, `ClearState`, `Run`, and new `tee`, `logPath`, `writeLog`, `LogTail`)
- Test: `internal/verify/verify_test.go` (append)

**Interfaces:**
- Produces:
  - `logtail.DefaultLines = 40`, `logtail.DefaultLineBytes = 300`
  - `logtail.New(maxLines, maxLineBytes int) *logtail.Writer`, whose methods are `Write([]byte) (int, error)`, `Lines() []string` and `String() string`. It is safe for concurrent use.
  - `verify.LogTail(stateDir string, n int) (string, error)` returns the raw (unredacted) tail of record `n`'s command output. A record without a stored tail yields `""`.
  - Tails are stored at `<stateDir>/verify-logs/NNNN.log`, outside `verify/`, so record numbering and `Records` are unchanged. `ClearState` removes the directory.

- [ ] **Step 1: Write the failing tests** — `internal/logtail/logtail_test.go`

```go
package logtail

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestKeepsLastLines(t *testing.T) {
	w := New(3, 100)
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	if got := w.Lines(); !slices.Equal(got, []string{"line 3", "line 4", "line 5"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestIncludesUnfinishedLine(t *testing.T) {
	w := New(2, 100)
	w.Write([]byte("a\nb\npart"))
	w.Write([]byte("ial"))
	if got := w.String(); got != "b\npartial" {
		t.Fatalf("tail = %q", got)
	}
}

func TestLongLineIsCutAndBounded(t *testing.T) {
	w := New(2, 10)
	big := strings.Repeat("x", 1<<20)
	w.Write([]byte(big))
	w.Write([]byte(big + "\nshort\n"))
	if len(w.partial) != 0 || cap(w.partial) > 64 {
		t.Fatalf("partial buffer grew to cap %d", cap(w.partial))
	}
	if got := w.Lines(); !slices.Equal(got, []string{"xxxxxxxxxx …", "short"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestCutNeverSplitsARune(t *testing.T) {
	w := New(1, 4)
	w.Write([]byte("abcé\n")) // é is 2 bytes; only its first fits
	if got := w.String(); got != "abc …" {
		t.Fatalf("tail = %q", got)
	}
}

func TestCarriageReturnKeepsFinalRedraw(t *testing.T) {
	w := New(5, 100)
	w.Write([]byte("progress 10%\rprogress 50%\rprogress 100%\r\ndone\r\n"))
	if got := w.Lines(); !slices.Equal(got, []string{"progress 100%", "done"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestConcurrentWrites(t *testing.T) {
	w := New(1000, 100)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				w.Write([]byte("line\n"))
			}
		}()
	}
	wg.Wait()
	if n := len(w.Lines()); n != 800 {
		t.Fatalf("got %d lines, want 800", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/logtail/`
Expected: FAIL, a compile error (`undefined: New`).

- [ ] **Step 3: Implement** — `internal/logtail/logtail.go`

```go
// Package logtail keeps the last lines written to it, bounded in count and
// in length, for the log tail a draft PR carries (design §4.5).
package logtail

import (
	"bytes"
	"strings"
	"sync"
)

// Bounds used for draft PR log tails: at most 40 lines of at most 300 bytes,
// so a tail never adds more than about 12 KB to a PR comment.
const (
	DefaultLines     = 40
	DefaultLineBytes = 300
)

// Writer is an io.Writer that remembers the last lines written to it. It is
// safe for concurrent use, so one Writer can take a command's stdout and
// stderr at once. Memory stays bounded however long a line is.
type Writer struct {
	mu       sync.Mutex
	maxLines int
	maxLine  int
	lines    []string
	partial  []byte
	cut      bool // the current line was longer than maxLine
}

// New returns a Writer keeping maxLines lines of at most maxLineBytes bytes each.
func New(maxLines, maxLineBytes int) *Writer {
	return &Writer{maxLines: maxLines, maxLine: maxLineBytes}
}

// Write implements io.Writer. It never fails.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if len(w.partial)+len(chunk) > w.maxLine {
			w.cut = true
		}
		if room := w.maxLine - len(w.partial); room > 0 {
			w.partial = append(w.partial, chunk[:min(room, len(chunk))]...)
		}
		if i < 0 {
			break
		}
		w.push()
		p = p[i+1:]
	}
	return n, nil
}

// push ends the current line. Only the text after the last carriage return
// is kept, so a progress bar redrawn with \r shows its final state.
func (w *Writer) push() {
	w.lines = append(w.lines, w.current())
	if len(w.lines) > w.maxLines {
		w.lines = append(w.lines[:0], w.lines[len(w.lines)-w.maxLines:]...)
	}
	w.partial, w.cut = w.partial[:0], false
}

func (w *Writer) current() string {
	line := strings.TrimRight(string(w.partial), "\r")
	if j := strings.LastIndexByte(line, '\r'); j >= 0 {
		line = line[j+1:]
	}
	if w.cut {
		line = strings.ToValidUTF8(line, "") + " …"
	}
	return line
}

// Lines returns the kept lines, oldest first, including an unfinished last line.
func (w *Writer) Lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]string(nil), w.lines...)
	if len(w.partial) > 0 || w.cut {
		out = append(out, w.current())
		if len(out) > w.maxLines {
			out = out[len(out)-w.maxLines:]
		}
	}
	return out
}

// String returns Lines joined by newlines.
func (w *Writer) String() string { return strings.Join(w.Lines(), "\n") }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/logtail/`
Expected: PASS.

- [ ] **Step 5: Write the failing verify tests** by appending to `internal/verify/verify_test.go`

```go
func TestLogTailKeepsLastOutput(t *testing.T) {
	f := setup(t)
	err := WriteSettings(f.stateDir, Settings{
		RepoDir: f.repoDir, Test: "sh test.sh", Reports: []string{"build/test-results/*.xml"}, TimeoutS: 60,
		Build: `i=0; while [ $i -lt 100 ]; do echo "step $i"; i=$((i+1)); done; echo "error: boom" >&2; exit 1`,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := run(t, f, KindBuild, false)
	tail, err := LogTail(f.stateDir, rec.N)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(tail, "\n")
	if len(lines) != 40 || lines[len(lines)-1] != "error: boom" || lines[0] != "step 61" {
		t.Fatalf("tail has %d lines, first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}
	// The tail is stored beside the records, not among them, so numbering
	// and Records are unaffected.
	if rec2 := run(t, f, KindBuild, false); rec2.N != 2 {
		t.Fatalf("second record N = %d, want 2", rec2.N)
	}
	if recs, err := Records(f.stateDir); err != nil || len(recs) != 2 {
		t.Fatalf("records = %+v, %v", recs, err)
	}
}

func TestLogTailMissingIsEmpty(t *testing.T) {
	if tail, err := LogTail(t.TempDir(), 7); err != nil || tail != "" {
		t.Fatalf("tail = %q, %v", tail, err)
	}
}

func TestClearStateRemovesLogs(t *testing.T) {
	f := setup(t)
	rec := run(t, f, KindBuild, false)
	if err := ClearState(f.stateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath(f.stateDir, rec.N)); !os.IsNotExist(err) {
		t.Fatalf("verify log survived ClearState: %v", err)
	}
}
```

- [ ] **Step 6: Run the tests to verify they fail**

Run: `go test ./internal/verify/ -run 'LogTail|ClearStateRemovesLogs'`
Expected: FAIL, a compile error (`undefined: LogTail`, `undefined: logPath`).

- [ ] **Step 7: Implement in `internal/verify/verify.go`**

Add the import `"github.com/dimipaun/fugaro/internal/logtail"` to the internal imports, between `gitops` and `procgroup`.

In the `const` block, below `recordsDir   = "verify"`, add:

```go
	logsDir      = "verify-logs" // output tails, kept apart so recordsDir holds only records
```

In `ClearState`, change the loop header to also remove the logs:

```go
	for _, name := range []string{settingsFile, recordsDir, logsDir} {
```

In `Run`, replace the `procgroup.Run` call:

```go
	code, runErr := procgroup.Run(runCtx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: s.RepoDir, Env: env,
		Stdout: o.Stdout, Stderr: o.Stderr, Grace: commandGrace,
	})
```

with:

```go
	tail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	code, runErr := procgroup.Run(runCtx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: s.RepoDir, Env: env,
		Stdout: tee(o.Stdout, tail), Stderr: tee(o.Stderr, tail), Grace: commandGrace,
	})
```

At the end of `Run`, replace:

```go
	if err := writeRecord(o.StateDir, &rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}
```

with:

```go
	if err := writeRecord(o.StateDir, &rec); err != nil {
		return Record{}, err
	}
	if err := writeLog(o.StateDir, rec.N, tail.String()); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// tee returns a writer that copies to w (which may be nil) and to tail.
func tee(w io.Writer, tail *logtail.Writer) io.Writer {
	if w == nil {
		return tail
	}
	return io.MultiWriter(w, tail)
}

func logPath(stateDir string, n int) string {
	return filepath.Join(stateDir, logsDir, fmt.Sprintf("%04d.log", n))
}

// writeLog stores the tail of verify record n's command output. It is the
// raw output: the runner redacts it before publishing it.
func writeLog(stateDir string, n int, tail string) error {
	if err := os.MkdirAll(filepath.Join(stateDir, logsDir), 0o755); err != nil {
		return fmt.Errorf("creating verify log directory: %w", err)
	}
	if err := os.WriteFile(logPath(stateDir, n), []byte(tail), 0o644); err != nil {
		return fmt.Errorf("writing verify log %d: %w", n, err)
	}
	return nil
}

// LogTail returns the last lines of verify record n's command output
// (at most logtail.DefaultLines lines of logtail.DefaultLineBytes bytes),
// unredacted. A record without a stored tail yields "".
func LogTail(stateDir string, n int) (string, error) {
	data, err := os.ReadFile(logPath(stateDir, n))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}
```

- [ ] **Step 8: Run the verify tests to verify they pass**

Run: `go test -race ./internal/verify/`
Expected: PASS, including every M1 verify test. Record numbering is unchanged because the tails live outside `verify/`.

- [ ] **Step 9: Commit**

```bash
gofmt -l internal/ && go vet ./internal/logtail/ ./internal/verify/
git add internal/logtail internal/verify
git commit -m "logtail: bounded output tails; verify keeps one per record"
```

---

### Task 2: Git credentials from the environment, an authenticated test remote, and the lease push

Git authenticates through environment variables alone. `GIT_CONFIG_COUNT` config installs a credential helper, scoped to the origin's `scheme://host`, that prints `FUGARO_GIT_USERNAME` and `FUGARO_GIT_TOKEN` when git asks. The first config entry resets any helper from system, global or repository config, so no keychain helper stores the token. The same variables work for the runner's git (`Repo.Env`) and for the agent's.

`Push` tolerates the agent having pushed and then amended the run branch. It forces, but only under a lease on the remote tip, and only when that tip is a commit this checkout already has. The tip therefore came from this run. It refuses anything that is not a `fugaro/` branch.

**Files:**
- Create: `internal/testutil/httpgit.go`, `internal/gitops/credentials.go`, `internal/gitops/credentials_test.go`
- Modify: `internal/gitops/gitops.go` (replace `Push`; add `RunBranchPrefix`, `OriginURL`, `remoteTip`)
- Test: `internal/gitops/gitops_test.go` (append)

**Interfaces:**
- Produces:
  - `gitops.CredentialVars(baseURL, username, token string) map[string]string` sets `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_0/1`, `GIT_CONFIG_VALUE_0/1`, `FUGARO_GIT_USERNAME` and `FUGARO_GIT_TOKEN`.
  - `gitops.CredentialURL(remote string) string` returns `scheme://host[:port]` for http(s), and `""` otherwise.
  - `gitops.WithVars(env []string, vars map[string]string) []string` replaces or appends entries, sorted.
  - `(*gitops.Repo).OriginURL(ctx) (string, error)`
  - `(*gitops.Repo).Push(ctx, branch string) error` keeps its M1 signature but now works as a lease push of `fugaro/` branches only.
  - `gitops.RunBranchPrefix = "fugaro/"`
  - Test helpers: `testutil.NewHTTPRemote(t, files, allow func(user, pass string) bool) *testutil.HTTPRemote` returns `{URL, Bare string}`. `testutil.Token(user string, tokens ...string)` and `testutil.Prefix(user, prefix string)` build `allow` functions.

- [ ] **Step 1: Write the test remote helper** — `internal/testutil/httpgit.go`

`git http-backend` ships with git, in `$(git --exec-path)`. It serves smart HTTP through CGI, so `net/http/cgi` can host it, and nothing is needed beyond git itself.

```go
package testutil

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// HTTPRemote is a git remote served over HTTP by `git http-backend`, behind
// basic authentication.
type HTTPRemote struct {
	URL  string // clone URL, such as http://127.0.0.1:1234/remote.git
	Bare string // path of the bare repository behind it
}

// NewHTTPRemote seeds a bare repository with files (as NewRemote does) and
// serves it over HTTP. A request is let through only when allow accepts its
// basic-auth username and password; every other request gets a 401, which
// is how git learns to ask its credential helper.
func NewHTTPRemote(t *testing.T, files map[string]string, allow func(user, pass string) bool) *HTTPRemote {
	t.Helper()
	bare := NewRemote(t, files)
	Git(t, bare, "config", "http.receivepack", "true")
	backend := &cgi.Handler{
		Path: filepath.Join(Git(t, bare, "--exec-path"), "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(bare), "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		user, pass, ok := req.BasicAuth()
		if !ok || !allow(user, pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	return &HTTPRemote{URL: srv.URL + "/" + filepath.Base(bare), Bare: bare}
}

// Token returns an allow function accepting exactly user and one of tokens.
func Token(user string, tokens ...string) func(string, string) bool {
	return func(u, p string) bool {
		if u != user {
			return false
		}
		for _, tok := range tokens {
			if p == tok {
				return true
			}
		}
		return false
	}
}

// Prefix returns an allow function accepting user with any password that
// starts with prefix, for tests whose tokens change during the run.
func Prefix(user, prefix string) func(string, string) bool {
	return func(u, p string) bool { return u == user && strings.HasPrefix(p, prefix) }
}
```

- [ ] **Step 2: Write the failing credential tests** — `internal/gitops/credentials_test.go`

```go
package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

const token = "tok-secret-1234"

func credEnv(remote string) []string {
	return WithVars(IdentityEnv(), CredentialVars(CredentialURL(remote), "x-token-auth", token))
}

func TestCredentialURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://bitbucket.org/acme/web.git":   "https://bitbucket.org",
		"http://127.0.0.1:8080/remote.git":     "http://127.0.0.1:8080",
		"git@github.com:acme/web.git":          "",
		"ssh://git@github.com/acme/web.git":    "",
		"/tmp/remote.git":                      "",
		"https://x-token-auth@bitbucket.org/a": "https://bitbucket.org",
	} {
		if got := CredentialURL(in); got != want {
			t.Errorf("CredentialURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithVarsReplacesAndAppends(t *testing.T) {
	got := WithVars([]string{"A=1", "B=2", "C=3"}, map[string]string{"B": "x", "D": "y"})
	if !slices.Equal(got, []string{"A=1", "C=3", "B=x", "D=y"}) {
		t.Fatalf("env = %q", got)
	}
}

func TestCredentialsCloneFetchAndPushOverHTTP(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewHTTPRemote(t, map[string]string{"README.md": "hi\n"}, testutil.Token("x-token-auth", token))
	dir := filepath.Join(t.TempDir(), "work")
	if _, err := OpenOrClone(ctx, dir, remote.URL, IdentityEnv()); err == nil {
		t.Fatal("clone without credentials succeeded")
	}
	repo, err := OpenOrClone(ctx, dir, remote.URL, credEnv(remote.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if got, want := testutil.Git(t, remote.Bare, "rev-parse", "refs/heads/fugaro/x"), testutil.Git(t, dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote branch %s, want %s", got, want)
	}
	for _, f := range []string{".git/config", ".git/FETCH_HEAD"} {
		if data, _ := os.ReadFile(filepath.Join(dir, f)); strings.Contains(string(data), token) {
			t.Fatalf("%s contains the token:\n%s", f, data)
		}
	}
	if u, err := repo.OriginURL(ctx); err != nil || u != remote.URL {
		t.Fatalf("OriginURL = %q, %v", u, err)
	}
}

func TestCredentialHelperIgnoresOtherHelpersAndHosts(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewHTTPRemote(t, map[string]string{"README.md": "hi\n"}, testutil.Token("x-token-auth", token))
	repo, err := OpenOrClone(ctx, filepath.Join(t.TempDir(), "work"), remote.URL, credEnv(remote.URL))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "asked")
	testutil.Git(t, repo.Dir, "config", "credential.helper", "!f() { echo asked >> "+marker+"; }; f")
	fill := func(host string) (string, error) {
		cmd := exec.Command("git", "credential", "fill")
		cmd.Dir = repo.Dir
		cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), credEnv(remote.URL)...)
		cmd.Stdin = strings.NewReader("protocol=http\nhost=" + host + "\n\n")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	host := strings.TrimPrefix(CredentialURL(remote.URL), "http://")
	out, err := fill(host)
	if err != nil || !strings.Contains(out, "username=x-token-auth\n") || !strings.Contains(out, "password="+token+"\n") {
		t.Fatalf("fill for the remote = %q, %v", out, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's own credential helper was consulted for the remote's host")
	}
	if out, _ := fill("elsewhere.example"); strings.Contains(out, token) {
		t.Fatalf("another host got the token: %q", out)
	}
}
```

- [ ] **Step 3: Write the failing push tests** by appending to `internal/gitops/gitops_test.go`. The M1 test `TestPushCreatesRemoteBranch` stays and must keep passing.

```go
func TestPushOverwritesTheAgentsOwnPushAfterAmend(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	// The agent pushes on its own, then amends the pushed commit.
	testutil.Git(t, repo.Dir, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "--amend", "-m", "first, amended")
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if got, want := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"), testutil.Git(t, repo.Dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote branch %s, want the amended %s", got, want)
	}
}

func TestPushRefusesATipFromElsewhere(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	// Someone else pushes to the run branch from their own clone.
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", remote, other)
	testutil.Git(t, other, "commit", "--quiet", "--allow-empty", "-m", "theirs")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	theirs := testutil.Git(t, other, "rev-parse", "HEAD")

	err := repo.Push(ctx, "fugaro/x")
	if err == nil || !strings.Contains(err.Error(), "something else pushed") {
		t.Fatalf("err = %v", err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != theirs {
		t.Fatalf("remote branch %s was overwritten (want %s)", got, theirs)
	}
}

func TestPushRefusesNonRunBranches(t *testing.T) {
	repo, remote := setup(t)
	before := testutil.Git(t, remote, "rev-parse", "refs/heads/main")
	if err := repo.CommitEmpty(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"main", "fugaro/", "feature/fugaro/x"} {
		if err := repo.Push(ctx, b); err == nil || !strings.Contains(err.Error(), "refusing to push") {
			t.Errorf("Push(%q) err = %v", b, err)
		}
	}
	if after := testutil.Git(t, remote, "rev-parse", "refs/heads/main"); after != before {
		t.Fatalf("main moved from %s to %s", before, after)
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/gitops/`
Expected: FAIL, compile errors (`undefined: WithVars`, `undefined: CredentialVars`, `repo.OriginURL undefined`).

- [ ] **Step 5: Implement the credentials** — `internal/gitops/credentials.go`

```go
package gitops

import (
	"maps"
	"net/url"
	"slices"
	"strings"
)

// credentialHelper answers git's credential "get" requests from the
// environment, and ignores "store" and "erase". The token therefore lives
// only in FUGARO_GIT_TOKEN: never in .git/config, a remote URL, or a
// command line that git or the runner might log.
const credentialHelper = `!f() { test "$1" = get || exit 0; printf 'username=%s\npassword=%s\n' "$FUGARO_GIT_USERNAME" "$FUGARO_GIT_TOKEN"; }; f`

// CredentialVars returns environment variables that authenticate git to
// baseURL (scheme://host[:port], see CredentialURL) as username with token.
// They use git's GIT_CONFIG_COUNT environment config, which outranks the
// system, global and repository config: the first entry resets any
// credential helper configured there for baseURL, so none of them is asked
// for, or stores, the token. Hosts other than baseURL never see it.
func CredentialVars(baseURL, username, token string) map[string]string {
	key := "credential." + baseURL + ".helper"
	return map[string]string{
		"GIT_CONFIG_COUNT":    "2",
		"GIT_CONFIG_KEY_0":    key,
		"GIT_CONFIG_VALUE_0":  "",
		"GIT_CONFIG_KEY_1":    key,
		"GIT_CONFIG_VALUE_1":  credentialHelper,
		"FUGARO_GIT_USERNAME": username,
		"FUGARO_GIT_TOKEN":    token,
	}
}

// CredentialURL returns scheme://host[:port] for an http or https remote
// URL, or "" for anything else (a local path, or ssh), which needs no token.
func CredentialURL(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// WithVars returns env with vars set: existing entries for those names are
// replaced, and the rest are appended in sorted order.
func WithVars(env []string, vars map[string]string) []string {
	out := make([]string, 0, len(env)+len(vars))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := vars[k]; !ok {
			out = append(out, kv)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		out = append(out, k+"="+vars[k])
	}
	return out
}
```

- [ ] **Step 6: Replace `Push` in `internal/gitops/gitops.go`**

Replace the M1 function:

```go
// Push pushes HEAD to branch on origin.
func (r *Repo) Push(ctx context.Context, branch string) error {
	_, err := r.git(ctx, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return err
}
```

with:

```go
// RunBranchPrefix starts every branch the runner pushes.
const RunBranchPrefix = "fugaro/"

// OriginURL returns the URL of the checkout's origin remote.
func (r *Repo) OriginURL(ctx context.Context) (string, error) {
	return r.git(ctx, "remote", "get-url", "origin")
}

// Push pushes HEAD to the run branch on origin. The agent may already have
// pushed the branch and then amended or rebased it, so the push is forced,
// but under a lease: the remote branch is overwritten only if it is absent
// or its tip is a commit this checkout has (so it came from this run). A
// tip pushed from anywhere else is left alone and Push fails. Only
// fugaro/ branches are ever pushed, so the base branch cannot be touched.
func (r *Repo) Push(ctx context.Context, branch string) error {
	if !strings.HasPrefix(branch, RunBranchPrefix) || len(branch) == len(RunBranchPrefix) {
		return fmt.Errorf("refusing to push %q: the runner only pushes %s<run-id> branches", branch, RunBranchPrefix)
	}
	ref := "refs/heads/" + branch
	tip, err := r.remoteTip(ctx, ref)
	if err != nil {
		return fmt.Errorf("reading %s on origin: %w", branch, err)
	}
	if tip != "" {
		if _, err := r.git(ctx, "cat-file", "-e", tip+"^{commit}"); err != nil {
			return fmt.Errorf("%s on origin is at %s, a commit this run never had: something else pushed to the branch, so it is not overwritten", branch, tip)
		}
	}
	_, err = r.git(ctx, "push", "--quiet", "--force-with-lease="+ref+":"+tip, "origin", "HEAD:"+ref)
	return err
}

// remoteTip returns the commit ref points at on origin, or "" if it does not exist.
func (r *Repo) remoteTip(ctx context.Context, ref string) (string, error) {
	out, err := r.git(ctx, "ls-remote", "origin", ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if sha, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return sha, nil
		}
	}
	return "", nil
}
```

The comparison against the remote tip does not use `refs/remotes/origin/<branch>`. That ref is updated only when the agent pushes through the `origin` remote with the default fetch refspec. `ls-remote` plus a local `cat-file -e` holds even when the agent pushed some other way.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -race ./internal/gitops/`
Expected: PASS. `TestCredentialHelperIgnoresOtherHelpersAndHosts` shows three things: the helper answers for the remote's host, a repository-local `credential.helper` is never consulted there, and another host never gets the token.

- [ ] **Step 8: Commit**

```bash
gofmt -l internal/ && go vet ./internal/gitops/ ./internal/testutil/
git add internal/gitops internal/testutil/httpgit.go
git commit -m "gitops: env-only git credentials, lease push of run branches only"
```

---

### Task 3: Provider interface: `GitAuth`, `Opener`, partial results, draft titles; fake provider

**Files:**
- Modify: `internal/gitprov/gitprov.go` (full replacement below)
- Create: `internal/gitprov/gitprov_test.go`
- Modify: `internal/gitprov/fake/fake.go`, `internal/gitprov/fake/fake_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (all in package `gitprov`):
  - `KindGitHub = "github"`, `KindBitbucket = "bitbucket"`
  - `GitAuth{Username, Token string; Expires time.Time; Env map[string]string}`
  - `Provider` gains `GitAuth(ctx, minValid time.Duration) (GitAuth, error)`
  - `type Opener func(ctx, kind, repo string) (Provider, []string, error)` returns the provider plus the credential values it holds, for redaction
  - `Static(p Provider) Opener`
  - `PartialError{Err error}`: the PR exists, but labels or reviewers failed
  - `DraftPrefix = "[DRAFT] "`, `DraftTitle(title string, draft bool) string`
  - `KindForURL(remote string) string`
  - `SplitRepo(repo string) (owner, name string, ok bool)`
  - Fake: `fake.Provider.Auth func(minValid time.Duration) gitprov.GitAuth`, `fake.Provider.FailEnsure int`, and `(*fake.Provider).GitAuth`.

M1's `runner` still compiles after this task: `*fake.Provider` gains the new method, and nothing else implements `Provider` yet.

- [ ] **Step 1: Write the failing tests** — `internal/gitprov/gitprov_test.go`

```go
package gitprov

import (
	"errors"
	"fmt"
	"testing"
)

func TestDraftTitle(t *testing.T) {
	for _, tc := range []struct {
		in    string
		draft bool
		want  string
	}{
		{"Add x", true, "[DRAFT] Add x"},
		{"[DRAFT] Add x", true, "[DRAFT] Add x"},
		{"[DRAFT] Add x", false, "Add x"},
		{"Add x", false, "Add x"},
	} {
		if got := DraftTitle(tc.in, tc.draft); got != tc.want {
			t.Errorf("DraftTitle(%q, %v) = %q, want %q", tc.in, tc.draft, got, tc.want)
		}
	}
}

func TestKindForURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://bitbucket.org/acme/web.git":      KindBitbucket,
		"https://x-token-auth@bitbucket.org/a/b":  KindBitbucket,
		"git@bitbucket.org:acme/web.git":          KindBitbucket,
		"https://github.com/acme/web.git":         KindGitHub,
		"ssh://git@GitHub.com/acme/web.git":       KindGitHub,
		"https://github.example.com/acme/web.git": "",
		"http://127.0.0.1:8080/remote.git":        "",
		"/tmp/remote.git":                         "",
	} {
		if got := KindForURL(in); got != want {
			t.Errorf("KindForURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitRepo(t *testing.T) {
	if o, n, ok := SplitRepo("acme/web"); !ok || o != "acme" || n != "web" {
		t.Fatalf("SplitRepo = %q %q %v", o, n, ok)
	}
	for _, bad := range []string{"acme", "/web", "acme/", "a/b/c"} {
		if _, _, ok := SplitRepo(bad); ok {
			t.Errorf("SplitRepo(%q) ok", bad)
		}
	}
}

func TestPartialErrorUnwraps(t *testing.T) {
	inner := errors.New("labels: HTTP 403")
	err := fmt.Errorf("ensure: %w", &PartialError{Err: inner})
	var pe *PartialError
	if !errors.As(err, &pe) || !errors.Is(err, inner) {
		t.Fatalf("err = %v", err)
	}
}
```

Append to `internal/gitprov/fake/fake_test.go`, and add `"time"` to its imports:

```go
func TestFailEnsureThenSucceed(t *testing.T) {
	ctx := context.Background()
	p := &Provider{FailEnsure: 1}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x"}); err == nil {
		t.Fatal("first EnsurePR should fail")
	}
	if pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x"}); err != nil || pr.Number != 1 {
		t.Fatalf("second EnsurePR = %+v, %v", pr, err)
	}
}

func TestGitAuth(t *testing.T) {
	ctx := context.Background()
	if a, err := (&Provider{}).GitAuth(ctx, time.Minute); err != nil || a.Token != "" {
		t.Fatalf("default GitAuth = %+v, %v", a, err)
	}
	var asked time.Duration
	p := &Provider{Auth: func(min time.Duration) gitprov.GitAuth {
		asked = min
		return gitprov.GitAuth{Username: "u", Token: "tok-1234"}
	}}
	if a, err := p.GitAuth(ctx, 45*time.Minute); err != nil || a.Token != "tok-1234" || asked != 45*time.Minute {
		t.Fatalf("GitAuth = %+v, %v (asked %s)", a, err, asked)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitprov/...`
Expected: FAIL, compile errors (`undefined: DraftTitle`, `unknown field FailEnsure`, `p.GitAuth undefined`).

- [ ] **Step 3: Implement** by replacing `internal/gitprov/gitprov.go` with:

```go
// Package gitprov abstracts the git hosting provider (design §3.1). The
// github and bitbucket subpackages implement it against the real REST APIs;
// fake is a file-backed stand-in for tests.
package gitprov

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// Provider kinds, as git.provider names them in fugaro.yaml.
const (
	KindGitHub    = "github"
	KindBitbucket = "bitbucket"
)

// PRSpec describes the pull request a run wants.
type PRSpec struct {
	Branch    string   `json:"branch"`
	Base      string   `json:"base"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Draft     bool     `json:"draft"`
	Labels    []string `json:"labels,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
}

// PR is a pull request on the provider.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
}

// GitAuth is how git, the runner's and the agent's, authenticates to the
// provider over HTTPS, plus any extra variables the agent's tools need.
type GitAuth struct {
	Username string            // HTTPS username, such as x-token-auth or x-access-token
	Token    string            // HTTPS password; empty means git needs no credentials
	Expires  time.Time         // zero when the token does not expire
	Env      map[string]string // extra agent variables, such as GH_TOKEN
}

// Provider is what the runner needs from the git host.
type Provider interface {
	// EnsurePR creates the pull request for spec.Branch or, if one already
	// exists (the agent may have opened it), updates only its draft state.
	// It is safe to call again after a failure: it finds what an earlier
	// call created. A *PartialError means the pull request exists (the
	// returned PR is valid) but some settings, such as labels or
	// reviewers, could not be applied.
	EnsurePR(ctx context.Context, spec PRSpec) (PR, error)
	// Comment posts a comment on the pull request.
	Comment(ctx context.Context, pr PR, body string) error
	// GitAuth returns git credentials that stay valid for at least
	// minValid, refreshing them first if needed (design §6.2).
	GitAuth(ctx context.Context, minValid time.Duration) (GitAuth, error)
}

// Opener opens the provider of kind (KindGitHub or KindBitbucket) for repo
// ("owner/name"). It returns the provider and the credential values it
// holds, which the runner adds to its redaction list.
type Opener func(ctx context.Context, kind, repo string) (Provider, []string, error)

// Static returns an Opener that always yields p, whatever the kind.
func Static(p Provider) Opener {
	return func(context.Context, string, string) (Provider, []string, error) { return p, nil, nil }
}

// PartialError reports that EnsurePR produced the pull request but could
// not apply all of its settings.
type PartialError struct{ Err error }

func (e *PartialError) Error() string { return "pull request opened, but " + e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// DraftPrefix marks a draft on a repository that cannot make real draft
// pull requests (design §15).
const DraftPrefix = "[DRAFT] "

// DraftTitle returns title with DraftPrefix when draft is true, and without
// it otherwise, so the prefix can be added and removed as a PR's state changes.
func DraftTitle(title string, draft bool) string {
	title = strings.TrimPrefix(title, DraftPrefix)
	if draft {
		return DraftPrefix + title
	}
	return title
}

// KindForURL names the provider hosting a git remote URL: KindGitHub for
// github.com, KindBitbucket for bitbucket.org, and "" for anything else
// (a local path, or another host).
func KindForURL(remote string) string {
	var host string
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		host = u.Hostname()
	} else if at, rest, ok := strings.Cut(remote, "@"); ok && !strings.Contains(at, "/") {
		host, _, _ = strings.Cut(rest, ":") // scp-like: git@host:owner/repo.git
	}
	switch strings.ToLower(host) {
	case "github.com":
		return KindGitHub
	case "bitbucket.org":
		return KindBitbucket
	}
	return ""
}

// SplitRepo splits "owner/name" into its two parts.
func SplitRepo(repo string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}
```

In `internal/gitprov/fake/fake.go`, add `"time"` to the imports, and replace the `Provider` struct:

```go
// Provider is a fake git host.
type Provider struct {
	Path string // optional; when set, State is loaded and saved on every call
	// Auth, when set, supplies GitAuth's result for each minValid asked
	// for. When nil, GitAuth returns no credentials (local remotes need none).
	Auth func(minValid time.Duration) gitprov.GitAuth
	// FailEnsure makes that many EnsurePR calls fail before any succeeds.
	FailEnsure int
	mu         sync.Mutex
	State      State
}
```

At the top of `EnsurePR`, right after `defer p.mu.Unlock()`, add:

```go
	if p.FailEnsure > 0 {
		p.FailEnsure--
		return gitprov.PR{}, errors.New("fake provider: injected EnsurePR failure")
	}
```

Append:

```go
// GitAuth implements gitprov.Provider.
func (p *Provider) GitAuth(_ context.Context, minValid time.Duration) (gitprov.GitAuth, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Auth == nil {
		return gitprov.GitAuth{}, nil
	}
	return p.Auth(minValid), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/gitprov/... && go build ./...`
Expected: PASS, and the whole module still builds.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ && go vet ./internal/gitprov/...
git add internal/gitprov
git commit -m "gitprov: GitAuth, Opener, partial results, draft titles, kind from URL"
```

---

### Task 4: Shared JSON client (`httpjson`) and recorded fixtures (`httpfixture`)

Both adapters talk JSON over REST. `httpjson` is the one place that handles the base URL, headers, `Authorization`, status errors (with a bounded body excerpt), and decoding.

`httpfixture` replays a fixture file (a JSON array of exchanges) strictly in order. The test fails on any mismatch, or if an exchange goes unused.

Fixtures in this plan are hand-written in the shape of the documented API responses, because no credentials exist yet. The same package's `Recorder` turns the later live run into real recorded fixtures. It never records `Authorization`, and it scrubs the secrets it is given.

**Files:**
- Create: `internal/gitprov/httpjson/httpjson.go`, `internal/gitprov/httpjson/httpjson_test.go`
- Create: `internal/gitprov/httpfixture/httpfixture.go`, `internal/gitprov/httpfixture/httpfixture_test.go`

**Interfaces:**
- Produces:
  - `httpjson.Client{BaseURL string; HTTP *http.Client; Auth func(ctx) (string, error); Header http.Header}`
  - `(*httpjson.Client).Do(ctx, method, path string, in, out any) error`. A path starting with `http://` or `https://` is used as is.
  - `httpjson.StatusError{Method, URL string; Status int; Body string}`. `URL` is the path only, without the query. `Body` is at most 512 bytes.
  - `httpjson.DefaultTimeout = 30s`
  - `httpfixture.Exchange{Method, Path, Auth string; Request json.RawMessage; Status int; Response json.RawMessage}`
  - `httpfixture.Serve(t, file) *httpfixture.Server`, which embeds `*httptest.Server`, so use `.URL`
  - `httpfixture.Recorder{Next http.RoundTripper; Secrets []string}` with `RoundTrip`, `Exchanges()` and `Save(path) error`

Fixture paths may be written unescaped, for example `?q=source.branch.name="fugaro/x" AND state="OPEN"`. They are compared after parsing, against the escaped form on the wire. Request bodies are compared as JSON values, so key order does not matter.

- [ ] **Step 1: Write the failing tests** — `internal/gitprov/httpjson/httpjson_test.go`

```go
package httpjson

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoSendsAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/api/things" || r.Header.Get("Authorization") != "Bearer tok" ||
			r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Extra") != "1" || string(body) != `{"name":"x"}` {
			t.Errorf("request = %s %s %v %s", r.Method, r.URL, r.Header, body)
		}
		w.Write([]byte(`{"id":7}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/api/", Header: http.Header{"X-Extra": {"1"}},
		Auth: func(context.Context) (string, error) { return "Bearer tok", nil }}
	var out struct{ ID int }
	if err := c.Do(context.Background(), "POST", "/things", map[string]string{"name": "x"}, &out); err != nil || out.ID != 7 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestDoStatusErrorIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat("e", 5000), http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	err := (&Client{BaseURL: srv.URL}).Do(context.Background(), "GET", "/x?secret=no", nil, nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 422 || len(se.Body) > 520 || se.URL != "/x" {
		t.Fatalf("err = %v", err)
	}
}

func TestDoAuthError(t *testing.T) {
	c := &Client{BaseURL: "http://unused.invalid", Auth: func(context.Context) (string, error) { return "", errors.New("no key") }}
	if err := c.Do(context.Background(), "GET", "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "no key") {
		t.Fatalf("err = %v", err)
	}
}
```

`internal/gitprov/httpfixture/httpfixture_test.go`:

```go
package httpfixture

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServeReplaysInOrder(t *testing.T) {
	srv := Serve(t, writeFixture(t, `[
	  {"method":"GET","path":"/prs?q=branch=\"fugaro/x\" AND state=\"OPEN\"","auth":"Bearer tok","status":200,"response":{"values":[]}},
	  {"method":"POST","path":"/prs","request":{"title":"T","draft":true},"status":201,"response":{"id":1}}
	]`))
	c := &httpjson.Client{BaseURL: srv.URL, Auth: func(context.Context) (string, error) { return "Bearer tok", nil }}
	ctx := context.Background()
	var list struct{ Values []any }
	if err := c.Do(ctx, "GET", `/prs?q=branch%3D%22fugaro%2Fx%22+AND+state%3D%22OPEN%22`, nil, &list); err != nil {
		t.Fatal(err)
	}
	var pr struct{ ID int }
	if err := c.Do(ctx, "POST", "/prs", map[string]any{"draft": true, "title": "T"}, &pr); err != nil || pr.ID != 1 {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestMismatchIsReported(t *testing.T) {
	e := Exchange{Method: "POST", Path: "/prs", Request: []byte(`{"draft":true}`), Status: 201}
	r := httptest.NewRequest("POST", "/prs", nil)
	if msg := mismatch(e, r, []byte(`{"draft":false}`)); !strings.Contains(msg, "body") {
		t.Fatalf("mismatch = %q", msg)
	}
	if msg := mismatch(e, httptest.NewRequest("PUT", "/prs", nil), nil); !strings.Contains(msg, "method") {
		t.Fatalf("mismatch = %q", msg)
	}
	e.Auth = "Bearer a"
	r = httptest.NewRequest("POST", "/prs", nil)
	r.Header.Set("Authorization", "Bearer b")
	if msg := mismatch(e, r, []byte(`{"draft":true}`)); !strings.Contains(msg, "authorization") {
		t.Fatalf("mismatch = %q", msg)
	}
}

func TestRecorderScrubsAndReplays(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"ghs_live_secret","expires_at":"2026-09-27T12:00:00Z"}`))
	}))
	defer live.Close()
	rec := &Recorder{Secrets: []string{"ghs_live_secret"}}
	c := &httpjson.Client{BaseURL: live.URL, HTTP: &http.Client{Transport: rec},
		Auth: func(context.Context) (string, error) { return "Bearer jwt-secret", nil }}
	if err := c.Do(context.Background(), "POST", "/app/installations/1/access_tokens", map[string]any{"repositories": []string{"web"}}, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "recorded.json")
	if err := rec.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "ghs_live_secret") || strings.Contains(string(data), "jwt-secret") || !strings.Contains(string(data), "REDACTED") {
		t.Fatalf("recording leaks a secret:\n%s", data)
	}
	srv := Serve(t, path)
	var out struct{ Token string }
	if err := (&httpjson.Client{BaseURL: srv.URL}).Do(context.Background(), "POST", "/app/installations/1/access_tokens", map[string]any{"repositories": []string{"web"}}, &out); err != nil || out.Token != "REDACTED" {
		t.Fatalf("replay = %+v, %v", out, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitprov/httpjson/ ./internal/gitprov/httpfixture/`
Expected: FAIL, compile errors (`undefined: Client`, `undefined: Serve`).

- [ ] **Step 3: Implement** — `internal/gitprov/httpjson/httpjson.go`

```go
// Package httpjson is the small JSON-over-HTTP client the provider adapters share.
package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout bounds one request when Client.HTTP is nil.
const DefaultTimeout = 30 * time.Second

// maxErrorBody is how much of an error response StatusError keeps.
const maxErrorBody = 512

var defaultHTTP = &http.Client{Timeout: DefaultTimeout}

// Client sends JSON requests to one REST API.
type Client struct {
	BaseURL string       // prefixed to request paths that are not absolute URLs
	HTTP    *http.Client // nil means a client with DefaultTimeout
	// Auth returns the Authorization header value for a request; nil or ""
	// sends none.
	Auth   func(ctx context.Context) (string, error)
	Header http.Header // added to every request
}

// StatusError is a response outside 2xx.
type StatusError struct {
	Method string
	URL    string
	Status int
	Body   string // at most 512 bytes of the response body
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Body)
}

// Do sends in, JSON-encoded unless nil, with method to path, and decodes a
// 2xx response body into out unless out is nil or the body is empty.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(data)
	}
	u := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u = strings.TrimSuffix(c.BaseURL, "/") + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	for k, vs := range c.Header {
		req.Header[k] = append([]string(nil), vs...)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Auth != nil {
		v, err := c.Auth(ctx)
		if err != nil {
			return fmt.Errorf("authenticating %s %s: %w", method, path, err)
		}
		if v != "" {
			req.Header.Set("Authorization", v)
		}
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("%s %s: reading response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		excerpt := strings.TrimSpace(string(data))
		if len(excerpt) > maxErrorBody {
			excerpt = strings.ToValidUTF8(excerpt[:maxErrorBody], "") + "…"
		}
		return &StatusError{Method: method, URL: req.URL.Path, Status: resp.StatusCode, Body: excerpt}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
		}
	}
	return nil
}
```

`internal/gitprov/httpfixture/httpfixture.go`:

```go
// Package httpfixture replays recorded HTTP exchanges for the provider
// adapter tests, and records new ones from a live API (design §13).
//
// A fixture file is a JSON array of exchanges, served strictly in order.
// Paths may be written unescaped: they are compared after parsing, so
// ?q=source.branch.name="fugaro/x" matches its escaped form on the wire.
package httpfixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Exchange is one HTTP request and its response.
type Exchange struct {
	Method string `json:"method"`
	Path   string `json:"path"` // path and query
	// Auth, when set, is the Authorization header the request must carry.
	Auth string `json:"auth,omitempty"`
	// Request, when set, is the JSON body the request must carry,
	// compared as JSON values rather than as text.
	Request  json.RawMessage `json:"request,omitempty"`
	Status   int             `json:"status"`
	Response json.RawMessage `json:"response,omitempty"`
}

// Server replays one fixture file.
type Server struct {
	*httptest.Server
	t         *testing.T
	mu        sync.Mutex
	exchanges []Exchange
	next      int
}

// Serve starts a server replaying the exchanges in file. The test fails if
// a request does not match the next exchange, or if any exchange is unused
// when the test ends.
func Serve(t *testing.T, file string) *Server {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{t: t}
	if err := json.Unmarshal(data, &s.exchanges); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(func() {
		s.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.next < len(s.exchanges) {
			e := s.exchanges[s.next]
			t.Errorf("%s: %d exchanges unused, starting with %s %s", file, len(s.exchanges)-s.next, e.Method, e.Path)
		}
	})
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.exchanges) {
		s.t.Errorf("unexpected request %s %s (all %d exchanges used)", r.Method, r.URL.RequestURI(), len(s.exchanges))
		http.Error(w, "no more exchanges", http.StatusInternalServerError)
		return
	}
	e := s.exchanges[s.next]
	s.next++
	if msg := mismatch(e, r, body); msg != "" {
		s.t.Errorf("exchange %d (%s %s): %s", s.next, e.Method, e.Path, msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	if len(e.Response) > 0 {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(e.Status)
	w.Write(e.Response)
}

func mismatch(e Exchange, r *http.Request, body []byte) string {
	if r.Method != e.Method {
		return "method " + r.Method
	}
	want, err := url.Parse(e.Path)
	if err != nil {
		return "bad fixture path: " + err.Error()
	}
	wantQ, _ := url.ParseQuery(want.RawQuery)
	if r.URL.Path != want.Path || !reflect.DeepEqual(r.URL.Query(), wantQ) {
		return "path " + r.URL.RequestURI()
	}
	if e.Auth != "" && r.Header.Get("Authorization") != e.Auth {
		return fmt.Sprintf("authorization %q", r.Header.Get("Authorization"))
	}
	if len(e.Request) > 0 {
		var got, exp any
		if err := json.Unmarshal(body, &got); err != nil {
			return "body is not JSON: " + string(body)
		}
		if err := json.Unmarshal(e.Request, &exp); err != nil {
			return "bad fixture request: " + err.Error()
		}
		if !reflect.DeepEqual(got, exp) {
			return "body " + string(body)
		}
	}
	return ""
}

// Recorder is an http.RoundTripper that forwards requests to Next and
// records each exchange, for turning a live run into fixture files. It
// never records the Authorization header, and replaces every value in
// Secrets with "REDACTED" in the bodies it records.
type Recorder struct {
	Next    http.RoundTripper // nil means http.DefaultTransport
	Secrets []string
	mu      sync.Mutex
	list    []Exchange
}

// RoundTrip implements http.RoundTripper.
func (rec *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		var err error
		if reqBody, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	next := rec.Next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	rec.mu.Lock()
	rec.list = append(rec.list, Exchange{
		Method: req.Method, Path: req.URL.RequestURI(), Status: resp.StatusCode,
		Request: rec.scrub(reqBody), Response: rec.scrub(respBody),
	})
	rec.mu.Unlock()
	return resp, nil
}

func (rec *Recorder) scrub(body []byte) json.RawMessage {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	s := string(body)
	for _, secret := range rec.Secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "REDACTED")
		}
	}
	if !json.Valid([]byte(s)) {
		quoted, _ := json.Marshal(s) // keep a non-JSON body as a JSON string
		return quoted
	}
	return json.RawMessage(s)
}

// Exchanges returns what has been recorded so far.
func (rec *Recorder) Exchanges() []Exchange {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]Exchange(nil), rec.list...)
}

// Save writes the recorded exchanges to path as a fixture file.
func (rec *Recorder) Save(path string) error {
	data, err := json.MarshalIndent(rec.Exchanges(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/gitprov/httpjson/ ./internal/gitprov/httpfixture/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ && go vet ./internal/gitprov/...
git add internal/gitprov/httpjson internal/gitprov/httpfixture
git commit -m "gitprov: shared JSON client; fixture replay and recording"
```

---

### Task 5: Bitbucket Cloud provider

Design §15 asked whether a Bitbucket Cloud repository access token can create and toggle draft PRs. Here is what the current API documents, from the OpenAPI schema at `https://api.bitbucket.org/swagger.json`, fetched 2026-09-27:
- The `pullrequest` object has `draft: boolean` ("A boolean flag indicating whether the pull request is a draft").
- `POST /repositories/{workspace}/{repo_slug}/pullrequests` and `PUT …/pullrequests/{id}` require the `pullrequest:write` scope.
- A repository access token can hold that scope ("Pull requests: Write").

So the adapter sends `draft` on create and update. It also checks the response, because none of this has been confirmed against a live repository. If Bitbucket did not honour `draft`, the adapter falls back to a `[DRAFT] ` title prefix. The live run (Task 8's checklist) settles the question. Two further details:
- Bitbucket Cloud pull requests have no labels, so `pr.labels` is ignored.
- A reviewer Bitbucket rejects fails the whole create with a 400. The adapter then retries without reviewers and reports a `PartialError`, so the run keeps its PR.

**Files:**
- Create: `internal/gitprov/bitbucket/bitbucket.go`, `internal/gitprov/bitbucket/bitbucket_test.go`
- Create: `internal/gitprov/bitbucket/testdata/{create_ready,create_draft,draft_ignored,existing_to_ready,reviewer_rejected,comment,unauthorized}.json`

**Interfaces:**
- Consumes: `httpjson.Client` and `StatusError` (Task 4); `gitprov.PRSpec`, `PR`, `GitAuth`, `PartialError` and `DraftTitle` (Task 3); `httpfixture.Serve` (Task 4, tests only).
- Produces:
  - `bitbucket.DefaultBaseURL = "https://api.bitbucket.org/2.0"`
  - `bitbucket.Options{Workspace, Slug, Token, BaseURL string; HTTP *http.Client}`
  - `bitbucket.New(Options) (*bitbucket.Provider, error)`, which implements `gitprov.Provider`
  - `GitAuth` returns `{Username: "x-token-auth", Token: <the repository access token>}`. The token never expires, and there is no extra env.
  - Reviewers in `fugaro.yaml` are account UUIDs (`{…}`) or account IDs.

- [ ] **Step 1: Write the fixtures** under `internal/gitprov/bitbucket/testdata/`

`create_ready.json`:

```json
[
  {"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests?q=source.branch.name=\"fugaro/20260927-101500-abcd\" AND state=\"OPEN\"",
   "auth": "Bearer bb-token-1234", "status": 200,
   "response": {"pagelen": 10, "size": 0, "page": 1, "values": []}},
  {"method": "POST", "path": "/2.0/repositories/acme/web/pullrequests", "auth": "Bearer bb-token-1234",
   "request": {"title": "Add search", "description": "Adds search.",
               "source": {"branch": {"name": "fugaro/20260927-101500-abcd"}}, "destination": {"branch": {"name": "main"}},
               "draft": false, "close_source_branch": true,
               "reviewers": [{"uuid": "{5b1d0c7e-0000-4000-8000-000000000001}"}, {"account_id": "557058:0000"}]},
   "status": 201,
   "response": {"type": "pullrequest", "id": 42, "title": "Add search", "draft": false, "state": "OPEN",
                "links": {"html": {"href": "https://bitbucket.org/acme/web/pull-requests/42"}}}}
]
```

`create_draft.json`:

```json
[
  {"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests?q=source.branch.name=\"fugaro/20260927-101500-abcd\" AND state=\"OPEN\"",
   "status": 200, "response": {"values": []}},
  {"method": "POST", "path": "/2.0/repositories/acme/web/pullrequests",
   "request": {"title": "Add search", "description": "Adds search.",
               "source": {"branch": {"name": "fugaro/20260927-101500-abcd"}}, "destination": {"branch": {"name": "main"}},
               "draft": true, "close_source_branch": true},
   "status": 201,
   "response": {"type": "pullrequest", "id": 43, "title": "Add search", "draft": true, "state": "OPEN",
                "links": {"html": {"href": "https://bitbucket.org/acme/web/pull-requests/43"}}}}
]
```

`draft_ignored.json`, where the response has no `draft`, as it would if Bitbucket ignored the field:

```json
[
  {"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests?q=source.branch.name=\"fugaro/20260927-101500-abcd\" AND state=\"OPEN\"",
   "status": 200, "response": {"values": []}},
  {"method": "POST", "path": "/2.0/repositories/acme/web/pullrequests",
   "request": {"title": "Add search", "description": "Adds search.",
               "source": {"branch": {"name": "fugaro/20260927-101500-abcd"}}, "destination": {"branch": {"name": "main"}},
               "draft": true, "close_source_branch": true},
   "status": 201,
   "response": {"type": "pullrequest", "id": 44, "title": "Add search", "state": "OPEN",
                "links": {"html": {"href": "https://bitbucket.org/acme/web/pull-requests/44"}}}},
  {"method": "PUT", "path": "/2.0/repositories/acme/web/pullrequests/44", "request": {"title": "[DRAFT] Add search"},
   "status": 200,
   "response": {"type": "pullrequest", "id": 44, "title": "[DRAFT] Add search", "state": "OPEN",
                "links": {"html": {"href": "https://bitbucket.org/acme/web/pull-requests/44"}}}}
]
```

`existing_to_ready.json`, a PR the agent opened, still carrying an earlier fallback prefix:

```json
[
  {"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests?q=source.branch.name=\"fugaro/20260927-101500-abcd\" AND state=\"OPEN\"",
   "status": 200,
   "response": {"values": [{"type": "pullrequest", "id": 45, "title": "[DRAFT] Agent's own title", "draft": true, "state": "OPEN",
                            "links": {"html": {"href": "https://bitbucket.org/acme/web/pull-requests/45"}}}]}},
  {"method": "PUT", "path": "/2.0/repositories/acme/web/pullrequests/45",
   "request": {"title": "Agent's own title", "draft": false},
   "status": 200,
   "response": {"type": "pullrequest", "id": 45, "title": "Agent's own title", "draft": false, "state": "OPEN",
                "links": {"html": {"href": "https://bitbucket.org/acme/web/pull-requests/45"}}}}
]
```

`reviewer_rejected.json`:

```json
[
  {"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests?q=source.branch.name=\"fugaro/20260927-101500-abcd\" AND state=\"OPEN\"",
   "status": 200, "response": {"values": []}},
  {"method": "POST", "path": "/2.0/repositories/acme/web/pullrequests",
   "request": {"title": "Add search", "description": "Adds search.",
               "source": {"branch": {"name": "fugaro/20260927-101500-abcd"}}, "destination": {"branch": {"name": "main"}},
               "draft": false, "close_source_branch": true, "reviewers": [{"account_id": "557058:gone"}]},
   "status": 400,
   "response": {"type": "error", "error": {"message": "reviewers: 557058:gone is not a valid user"}}},
  {"method": "POST", "path": "/2.0/repositories/acme/web/pullrequests",
   "request": {"title": "Add search", "description": "Adds search.",
               "source": {"branch": {"name": "fugaro/20260927-101500-abcd"}}, "destination": {"branch": {"name": "main"}},
               "draft": false, "close_source_branch": true},
   "status": 201,
   "response": {"type": "pullrequest", "id": 46, "title": "Add search", "draft": false, "state": "OPEN",
                "links": {"html": {"href": "https://bitbucket.org/acme/web/pull-requests/46"}}}}
]
```

`comment.json`:

```json
[
  {"method": "POST", "path": "/2.0/repositories/acme/web/pullrequests/42/comments", "auth": "Bearer bb-token-1234",
   "request": {"content": {"raw": "### Fugaro run report"}},
   "status": 201, "response": {"type": "pullrequest_comment", "id": 9001}}
]
```

`unauthorized.json`:

```json
[
  {"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests?q=source.branch.name=\"fugaro/20260927-101500-abcd\" AND state=\"OPEN\"",
   "status": 401, "response": {"type": "error", "error": {"message": "Token is invalid or expired"}}}
]
```

- [ ] **Step 2: Write the failing tests** — `internal/gitprov/bitbucket/bitbucket_test.go`

```go
package bitbucket

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

const branchName = "fugaro/20260927-101500-abcd"

var ctx = context.Background()

func open(t *testing.T, fixture string) *Provider {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "bb-token-1234", BaseURL: srv.URL + "/2.0"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func spec(draft bool, reviewers ...string) gitprov.PRSpec {
	return gitprov.PRSpec{Branch: branchName, Base: "main", Title: "Add search", Body: "Adds search.",
		Draft: draft, Labels: []string{"fugaro"}, Reviewers: reviewers}
}

func TestCreateReady(t *testing.T) {
	p := open(t, "create_ready.json")
	pr, err := p.EnsurePR(ctx, spec(false, "{5b1d0c7e-0000-4000-8000-000000000001}", "557058:0000"))
	if err != nil || pr != (gitprov.PR{Number: 42, URL: "https://bitbucket.org/acme/web/pull-requests/42"}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestCreateDraft(t *testing.T) {
	p := open(t, "create_draft.json")
	if pr, err := p.EnsurePR(ctx, spec(true)); err != nil || pr.Number != 43 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestDraftIgnoredFallsBackToTitle covers design §15: if Bitbucket does not
// make the pull request a real draft, the title says so instead.
func TestDraftIgnoredFallsBackToTitle(t *testing.T) {
	p := open(t, "draft_ignored.json")
	if pr, err := p.EnsurePR(ctx, spec(true)); err != nil || pr.Number != 44 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestExistingPRIsOnlyToggled covers a pull request the agent opened
// itself: it is found by branch, marked ready, its fallback prefix removed,
// and its title and description otherwise left alone.
func TestExistingPRIsOnlyToggled(t *testing.T) {
	p := open(t, "existing_to_ready.json")
	if pr, err := p.EnsurePR(ctx, spec(false)); err != nil || pr.Number != 45 || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestRejectedReviewerStillOpensPR(t *testing.T) {
	p := open(t, "reviewer_rejected.json")
	pr, err := p.EnsurePR(ctx, spec(false, "557058:gone"))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 46 || !strings.Contains(err.Error(), "557058:gone") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

func TestComment(t *testing.T) {
	p := open(t, "comment.json")
	if err := p.Comment(ctx, gitprov.PR{Number: 42}, "### Fugaro run report"); err != nil {
		t.Fatal(err)
	}
}

func TestUnauthorized(t *testing.T) {
	p := open(t, "unauthorized.json")
	if _, err := p.EnsurePR(ctx, spec(false)); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v", err)
	}
}

func TestGitAuth(t *testing.T) {
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "bb-token-1234"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.GitAuth(ctx, 0)
	if err != nil || a.Username != "x-token-auth" || a.Token != "bb-token-1234" || !a.Expires.IsZero() {
		t.Fatalf("auth = %+v, %v", a, err)
	}
}

func TestNewValidates(t *testing.T) {
	for _, o := range []Options{{Slug: "web", Token: "t"}, {Workspace: "acme", Token: "t"}, {Workspace: "acme", Slug: "web"}} {
		if _, err := New(o); err == nil {
			t.Errorf("New(%+v) succeeded", o)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/gitprov/bitbucket/`
Expected: FAIL, compile errors (`undefined: New`, `undefined: Options`).

- [ ] **Step 4: Implement** — `internal/gitprov/bitbucket/bitbucket.go`

```go
// Package bitbucket implements gitprov.Provider against the Bitbucket Cloud
// REST API (2.0), authenticated with a repository access token (design §6.1).
package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// DefaultBaseURL is the Bitbucket Cloud API root.
const DefaultBaseURL = "https://api.bitbucket.org/2.0"

// gitUsername is the HTTPS username git uses with a repository access token.
const gitUsername = "x-token-auth"

// Options configure a Provider.
type Options struct {
	Workspace string // the repository's workspace
	Slug      string // the repository slug
	Token     string // repository access token: Repositories read/write, Pull requests read/write
	BaseURL   string // API root; empty means DefaultBaseURL
	HTTP      *http.Client
}

// Provider is a Bitbucket Cloud repository.
type Provider struct {
	o   Options
	api *httpjson.Client
}

// New returns a Provider for o.
func New(o Options) (*Provider, error) {
	if o.Workspace == "" || o.Slug == "" {
		return nil, errors.New("bitbucket: workspace and repository slug are required")
	}
	if o.Token == "" {
		return nil, errors.New("bitbucket: a repository access token is required")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	token := o.Token
	return &Provider{o: o, api: &httpjson.Client{
		BaseURL: o.BaseURL, HTTP: o.HTTP,
		Header: http.Header{"Accept": {"application/json"}},
		Auth:   func(context.Context) (string, error) { return "Bearer " + token, nil },
	}}, nil
}

type pullRequest struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Draft bool   `json:"draft"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

type endpoint struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
}

func branch(name string) endpoint {
	var e endpoint
	e.Branch.Name = name
	return e
}

func (p *Provider) prPath(suffix string) string {
	return "/repositories/" + url.PathEscape(p.o.Workspace) + "/" + url.PathEscape(p.o.Slug) + "/pullrequests" + suffix
}

// EnsurePR implements gitprov.Provider. Bitbucket Cloud pull requests have
// no labels, so spec.Labels is ignored. spec.Reviewers are account UUIDs
// ("{…}") or account IDs.
func (p *Provider) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	existing, err := p.find(ctx, spec.Branch)
	if err != nil {
		return gitprov.PR{}, err
	}
	if existing != nil {
		var pr pullRequest
		body := map[string]any{"title": gitprov.DraftTitle(existing.Title, false), "draft": spec.Draft}
		if err := p.api.Do(ctx, "PUT", p.prPath(fmt.Sprintf("/%d", existing.ID)), body, &pr); err != nil {
			return gitprov.PR{}, fmt.Errorf("updating pull request #%d: %w", existing.ID, err)
		}
		return p.finish(ctx, pr, spec.Draft)
	}
	return p.create(ctx, spec)
}

func (p *Provider) find(ctx context.Context, branch string) (*pullRequest, error) {
	q := url.Values{"q": {fmt.Sprintf(`source.branch.name="%s" AND state="OPEN"`, branch)}}
	var page struct {
		Values []pullRequest `json:"values"`
	}
	if err := p.api.Do(ctx, "GET", p.prPath("?"+q.Encode()), nil, &page); err != nil {
		return nil, fmt.Errorf("finding the pull request for %s: %w", branch, err)
	}
	if len(page.Values) == 0 {
		return nil, nil
	}
	return &page.Values[0], nil
}

func (p *Provider) create(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	body := map[string]any{
		"title": spec.Title, "description": spec.Body,
		"source": branch(spec.Branch), "destination": branch(spec.Base),
		"draft": spec.Draft, "close_source_branch": true,
	}
	if len(spec.Reviewers) > 0 {
		body["reviewers"] = reviewers(spec.Reviewers)
	}
	var pr pullRequest
	err := p.api.Do(ctx, "POST", p.prPath(""), body, &pr)
	var rejected error
	var se *httpjson.StatusError
	if err != nil && len(spec.Reviewers) > 0 && errors.As(err, &se) && se.Status == http.StatusBadRequest {
		// Bitbucket rejects the whole pull request over one bad reviewer
		// (an unknown account, or the token's own). That must not cost the
		// run its pull request: open it without reviewers instead.
		rejected = fmt.Errorf("reviewers %s were rejected: %w", strings.Join(spec.Reviewers, ", "), err)
		delete(body, "reviewers")
		err = p.api.Do(ctx, "POST", p.prPath(""), body, &pr)
	}
	if err != nil {
		return gitprov.PR{}, fmt.Errorf("creating the pull request for %s: %w", spec.Branch, err)
	}
	out, err := p.finish(ctx, pr, spec.Draft)
	if err == nil && rejected != nil {
		err = &gitprov.PartialError{Err: rejected}
	}
	return out, err
}

func reviewers(ids []string) []map[string]string {
	out := make([]map[string]string, len(ids))
	for i, id := range ids {
		if strings.HasPrefix(id, "{") {
			out[i] = map[string]string{"uuid": id}
		} else {
			out[i] = map[string]string{"account_id": id}
		}
	}
	return out
}

// finish returns pr as a gitprov.PR in the wanted draft state. If
// Bitbucket did not make it a real draft, the draft is marked in the title
// instead (design §15); a title prefix left from that is removed once the
// pull request is ready.
func (p *Provider) finish(ctx context.Context, pr pullRequest, draft bool) (gitprov.PR, error) {
	if !draft && pr.Draft {
		return gitprov.PR{}, fmt.Errorf("pull request #%d is still a draft after asking Bitbucket to mark it ready", pr.ID)
	}
	if title := gitprov.DraftTitle(pr.Title, draft && !pr.Draft); title != pr.Title {
		if err := p.api.Do(ctx, "PUT", p.prPath(fmt.Sprintf("/%d", pr.ID)), map[string]any{"title": title}, nil); err != nil {
			return gitprov.PR{}, fmt.Errorf("retitling pull request #%d: %w", pr.ID, err)
		}
	}
	return gitprov.PR{Number: pr.ID, URL: pr.Links.HTML.Href, Draft: draft}, nil
}

// Comment implements gitprov.Provider.
func (p *Provider) Comment(ctx context.Context, pr gitprov.PR, body string) error {
	in := map[string]any{"content": map[string]string{"raw": body}}
	if err := p.api.Do(ctx, "POST", p.prPath(fmt.Sprintf("/%d/comments", pr.Number)), in, nil); err != nil {
		return fmt.Errorf("commenting on pull request #%d: %w", pr.Number, err)
	}
	return nil
}

// GitAuth implements gitprov.Provider. A repository access token is used
// as is (design §6.2); it does not expire during a run.
func (p *Provider) GitAuth(context.Context, time.Duration) (gitprov.GitAuth, error) {
	return gitprov.GitAuth{Username: gitUsername, Token: p.o.Token}, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/gitprov/bitbucket/`
Expected: PASS, with no "exchanges unused" errors. Every fixture exchange must be consumed, which proves, for example, that the ready path makes no extra `PUT`.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/ && go vet ./internal/gitprov/bitbucket/
git add internal/gitprov/bitbucket
git commit -m "gitprov/bitbucket: Bitbucket Cloud provider with draft fallback"
```

---

### Task 6: GitHub App JWT and repo-scoped installation tokens

This task builds the GitHub App side of design §6.1 and §6.2. The App JWT is signed RS256 with `crypto/rsa`, backdated 60 seconds and valid for 9 minutes (GitHub's maximum is 10). The installation is looked up once. Each installation token is minted for this repository only, with only the permissions the run needs, and cached until it would expire within the `minValid` the caller asks for.

**Files:**
- Create: `internal/gitprov/github/apptoken.go`, `internal/gitprov/github/apptoken_test.go`
- Create: `internal/gitprov/github/testdata/token_refresh.json`, `internal/gitprov/github/testdata/not_installed.json`

**Interfaces:**
- Consumes: `httpjson.Client` (Task 4); `httpfixture.Serve` (Task 4, tests only).
- Produces (package `github`):
  - `ParsePrivateKey(pem []byte) (*rsa.PrivateKey, error)` accepts PKCS#1 or PKCS#8.
  - Unexported, used by Task 7: `appJWT(appID, key, now) (string, error)`, `appAuth(appID, key, now func() time.Time) func(context.Context) (string, error)`, `tokenPermissions`, and `tokenSource{app *httpjson.Client; owner, repo string; now func() time.Time}` with `Token(ctx, minValid) (token string, expires time.Time, err error)`.
  - Test helpers used by Task 7's tests: `key(t) *rsa.PrivateKey` and `verifyJWT(t, jwt, pub)`.

- [ ] **Step 1: Write the fixtures**

`internal/gitprov/github/testdata/token_refresh.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200,
   "response": {"id": 777, "app_id": 1234, "target_type": "Organization"}},
  {"method": "POST", "path": "/app/installations/777/access_tokens",
   "request": {"repositories": ["web"], "permissions": {"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read"}},
   "status": 201,
   "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z", "repository_selection": "selected"}},
  {"method": "POST", "path": "/app/installations/777/access_tokens",
   "request": {"repositories": ["web"], "permissions": {"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read"}},
   "status": 201,
   "response": {"token": "ghs_fixture_token_2", "expires_at": "2026-09-27T11:20:00Z", "repository_selection": "selected"}}
]
```

`internal/gitprov/github/testdata/not_installed.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 404,
   "response": {"message": "Not Found", "documentation_url": "https://docs.github.com/rest/apps/apps#get-a-repository-installation-for-the-authenticated-app"}}
]
```

- [ ] **Step 2: Write the failing tests** — `internal/gitprov/github/apptoken_test.go`

The `jwtCheck` transport verifies the signature of every App-authenticated request's JWT with the public key. The fixture itself cannot match a JWT, because it changes with the clock.

```go
package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// key returns an RSA key shared by the package's tests (generating one is slow).
func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		if testKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return testKey
}

func pkcs1PEM(t *testing.T) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key(t))})
}

// verifyJWT checks jwt's RS256 signature against pub and returns its claims.
func verifyJWT(t *testing.T, jwt string, pub *rsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt %q does not have 3 parts", jwt)
	}
	enc := base64.RawURLEncoding
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("jwt signature: %v", err)
	}
	var header, claims map[string]any
	h, _ := enc.DecodeString(parts[0])
	c, _ := enc.DecodeString(parts[1])
	if json.Unmarshal(h, &header) != nil || json.Unmarshal(c, &claims) != nil || header["alg"] != "RS256" {
		t.Fatalf("jwt header %s, claims %s", h, c)
	}
	return claims
}

func TestParsePrivateKey(t *testing.T) {
	if k, err := ParsePrivateKey(pkcs1PEM(t)); err != nil || !k.Equal(key(t)) {
		t.Fatalf("PKCS#1: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key(t))
	if err != nil {
		t.Fatal(err)
	}
	if k, err := ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil || !k.Equal(key(t)) {
		t.Fatalf("PKCS#8: %v", err)
	}
	if _, err := ParsePrivateKey([]byte("not a key")); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestAppJWT(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	jwt, err := appJWT("1234", key(t), now)
	if err != nil {
		t.Fatal(err)
	}
	claims := verifyJWT(t, jwt, &key(t).PublicKey)
	if claims["iss"] != "1234" || claims["iat"] != float64(now.Unix()-60) || claims["exp"] != float64(now.Unix()+540) {
		t.Fatalf("claims = %v", claims)
	}
}

// jwtCheck is a RoundTripper that fails the test unless every request is
// authenticated with an App JWT signed by pub.
type jwtCheck struct {
	t   *testing.T
	pub *rsa.PublicKey
}

func (c jwtCheck) RoundTrip(r *http.Request) (*http.Response, error) {
	jwt, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		c.t.Errorf("%s %s: no bearer JWT", r.Method, r.URL.Path)
	} else {
		verifyJWT(c.t, jwt, c.pub)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func newSource(t *testing.T, fixture string, now *time.Time) *tokenSource {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	clock := func() time.Time { return *now }
	app := &httpjson.Client{BaseURL: srv.URL, HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}},
		Auth: appAuth("1234", key(t), clock)}
	return &tokenSource{app: app, owner: "acme", repo: "web", now: clock}
}

func TestTokenIsCachedThenRefreshedNearExpiry(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := newSource(t, "token_refresh.json", &now)
	ctx := context.Background()
	tok, exp, err := s.Token(ctx, 45*time.Minute)
	if err != nil || tok != "ghs_fixture_token_1" || !exp.Equal(time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("first token = %q %s %v", tok, exp, err)
	}
	now = now.Add(10 * time.Minute) // 50m left: still good for 45m
	if tok, _, err := s.Token(ctx, 45*time.Minute); err != nil || tok != "ghs_fixture_token_1" {
		t.Fatalf("cached token = %q %v", tok, err)
	}
	now = now.Add(10 * time.Minute) // 40m left: too short, and no second installation lookup
	if tok, _, err := s.Token(ctx, 45*time.Minute); err != nil || tok != "ghs_fixture_token_2" {
		t.Fatalf("refreshed token = %q %v", tok, err)
	}
}

func TestTokenAppNotInstalled(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := newSource(t, "not_installed.json", &now)
	if _, _, err := s.Token(context.Background(), time.Minute); err == nil || !strings.Contains(err.Error(), "installation for acme/web") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/gitprov/github/`
Expected: FAIL, compile errors (`undefined: ParsePrivateKey`, `undefined: tokenSource`).

- [ ] **Step 4: Implement** — `internal/gitprov/github/apptoken.go`

```go
package github

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// ParsePrivateKey parses a GitHub App private key in PEM form: PKCS#1
// ("RSA PRIVATE KEY", what GitHub issues) or PKCS#8 ("PRIVATE KEY").
func ParsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("github app private key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing github app private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app private key is not an RSA key")
	}
	return rk, nil
}

// appJWT returns the RS256 JSON Web Token that authenticates as the App.
// It is backdated a minute against clock drift and lives 9 minutes, under
// GitHub's 10-minute maximum.
func appJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("signing github app jwt: %w", err)
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

// appAuth returns an httpjson.Client Auth function that sends a fresh App JWT.
func appAuth(appID string, key *rsa.PrivateKey, now func() time.Time) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		jwt, err := appJWT(appID, key, now())
		if err != nil {
			return "", err
		}
		return "Bearer " + jwt, nil
	}
}

// tokenPermissions is what an installation token asks for (design §6.1):
// no more than the run needs, even if the App was granted more.
var tokenPermissions = map[string]string{
	"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read",
}

// tokenSource mints installation tokens scoped to one repository and
// caches the current one until it gets close to expiring.
type tokenSource struct {
	app          *httpjson.Client // authenticated as the App (JWT)
	owner, repo  string
	now          func() time.Time
	mu           sync.Mutex
	installation int64
	token        string
	expires      time.Time
}

// Token returns an installation token valid for at least minValid, minting
// a new one when the cached token would expire sooner. A new token lives
// about an hour, so a minValid beyond that mints on every call.
func (s *tokenSource) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.expires.Sub(s.now()) >= minValid {
		return s.token, s.expires, nil
	}
	if s.installation == 0 {
		var inst struct {
			ID int64 `json:"id"`
		}
		path := "/repos/" + url.PathEscape(s.owner) + "/" + url.PathEscape(s.repo) + "/installation"
		if err := s.app.Do(ctx, "GET", path, nil, &inst); err != nil {
			return "", time.Time{}, fmt.Errorf("finding the github app installation for %s/%s: %w", s.owner, s.repo, err)
		}
		s.installation = inst.ID
	}
	var tok struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	body := map[string]any{"repositories": []string{s.repo}, "permissions": tokenPermissions}
	if err := s.app.Do(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", s.installation), body, &tok); err != nil {
		return "", time.Time{}, fmt.Errorf("minting an installation token for %s/%s: %w", s.owner, s.repo, err)
	}
	if tok.Token == "" {
		return "", time.Time{}, errors.New("github returned an empty installation token")
	}
	s.token, s.expires = tok.Token, tok.ExpiresAt
	return s.token, s.expires, nil
}
```

`rsa.SignPKCS1v15` ignores its `random` argument since Go 1.20, so passing `nil` is correct.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/gitprov/github/`
Expected: PASS. `TestTokenIsCachedThenRefreshedNearExpiry` makes three token calls against a fixture with one installation lookup and two mints, which proves both the cache and the refresh.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/ && go vet ./internal/gitprov/github/
git add internal/gitprov/github
git commit -m "gitprov/github: stdlib RS256 App JWT and repo-scoped installation tokens"
```

---

### Task 7: GitHub provider

The REST API cannot change a pull request's draft state, so the adapter uses the GraphQL mutations `convertPullRequestToDraft` and `markPullRequestReadyForReview` (by `node_id`). Three behaviours need care:

- **No draft support.** Repositories on plans without draft PRs reject `draft: true` with a 422 ("Draft pull requests are not supported in this repository."), and reject the GraphQL conversion. The adapter then opens a normal PR titled `[DRAFT] …`.
- **Marking ready.** A failure to mark a real draft ready is an error. It must never silently leave a draft that the run record calls ready. The runner retries it (Task 9).
- **Labels and reviewers.** These go through separate calls after the create, and a failure there is a `PartialError`.

**Files:**
- Create: `internal/gitprov/github/github.go`, `internal/gitprov/github/github_test.go`
- Create under `internal/gitprov/github/testdata/`: `create_ready.json`, `draft_unsupported.json`, `existing_to_draft.json`, `existing_draft_unsupported.json`, `existing_to_ready.json`, `existing_prefix_to_ready.json`, `mark_ready_fails.json`, `labels_forbidden.json`, `comment.json`

**Interfaces:**
- Consumes: Task 6's `tokenSource`, `appAuth` and `key(t)`; Task 4's `httpjson` and `httpfixture`; Task 3's `gitprov` types.
- Produces:
  - `github.DefaultBaseURL = "https://api.github.com"`
  - `github.Options{Owner, Repo, AppID string; PrivateKey *rsa.PrivateKey; BaseURL string; HTTP *http.Client; Now func() time.Time}`
  - `github.New(Options) (*github.Provider, error)`, which implements `gitprov.Provider`
  - `GitAuth` returns `{Username: "x-access-token", Token, Expires, Env: {"GH_TOKEN": token}}`.
  - Reviewers in `fugaro.yaml` are user logins, or `org/team` for a team.

- [ ] **Step 1: Write the fixtures** under `internal/gitprov/github/testdata/`. Each exchange sits on one line. The first two exchanges of each file look up the installation and mint `ghs_fixture_token_1`.

`create_ready.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "auth": "Bearer ghs_fixture_token_1", "status": 200, "response": []},
  {"method": "POST", "path": "/repos/acme/web/pulls", "auth": "Bearer ghs_fixture_token_1", "request": {"title": "Add search", "head": "fugaro/20260927-101500-abcd", "base": "main", "body": "Adds search.", "draft": false}, "status": 201, "response": {"number": 12, "html_url": "https://github.com/acme/web/pull/12", "draft": false, "node_id": "PR_kwDOA12", "title": "Add search"}},
  {"method": "POST", "path": "/repos/acme/web/issues/12/labels", "request": {"labels": ["fugaro"]}, "status": 200, "response": [{"name": "fugaro"}]},
  {"method": "POST", "path": "/repos/acme/web/pulls/12/requested_reviewers", "request": {"reviewers": ["octocat"], "team_reviewers": ["platform"]}, "status": 201, "response": {"number": 12}}
]
```

`draft_unsupported.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "auth": "Bearer ghs_fixture_token_1", "status": 200, "response": []},
  {"method": "POST", "path": "/repos/acme/web/pulls", "request": {"title": "Add search", "head": "fugaro/20260927-101500-abcd", "base": "main", "body": "Adds search.", "draft": true}, "status": 422, "response": {"message": "Validation Failed", "errors": [{"resource": "PullRequest", "code": "custom", "message": "Draft pull requests are not supported in this repository."}]}},
  {"method": "POST", "path": "/repos/acme/web/pulls", "request": {"title": "[DRAFT] Add search", "head": "fugaro/20260927-101500-abcd", "base": "main", "body": "Adds search.", "draft": false}, "status": 201, "response": {"number": 16, "html_url": "https://github.com/acme/web/pull/16", "draft": false, "node_id": "PR_kwDOA16", "title": "[DRAFT] Add search"}}
]
```

`existing_to_draft.json`, a PR the agent opened with `gh`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "auth": "Bearer ghs_fixture_token_1", "status": 200, "response": [{"number": 13, "html_url": "https://github.com/acme/web/pull/13", "draft": false, "node_id": "PR_kwDOA13", "title": "Agent's own title"}]},
  {"method": "POST", "path": "/graphql", "auth": "Bearer ghs_fixture_token_1", "request": {"query": "mutation($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { pullRequest { isDraft } } }", "variables": {"id": "PR_kwDOA13"}}, "status": 200, "response": {"data": {"convertPullRequestToDraft": {"pullRequest": {"isDraft": true}}}}}
]
```

`existing_draft_unsupported.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "auth": "Bearer ghs_fixture_token_1", "status": 200, "response": [{"number": 17, "html_url": "https://github.com/acme/web/pull/17", "draft": false, "node_id": "PR_kwDOA17", "title": "Agent's own title"}]},
  {"method": "POST", "path": "/graphql", "request": {"query": "mutation($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { pullRequest { isDraft } } }", "variables": {"id": "PR_kwDOA17"}}, "status": 200, "response": {"data": null, "errors": [{"type": "UNPROCESSABLE", "message": "Draft pull requests are not supported in this repository."}]}},
  {"method": "PATCH", "path": "/repos/acme/web/pulls/17", "request": {"title": "[DRAFT] Agent's own title"}, "status": 200, "response": {"number": 17, "title": "[DRAFT] Agent's own title"}}
]
```

`existing_to_ready.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "auth": "Bearer ghs_fixture_token_1", "status": 200, "response": [{"number": 15, "html_url": "https://github.com/acme/web/pull/15", "draft": true, "node_id": "PR_kwDOA15", "title": "Add search"}]},
  {"method": "POST", "path": "/graphql", "request": {"query": "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }", "variables": {"id": "PR_kwDOA15"}}, "status": 200, "response": {"data": {"markPullRequestReadyForReview": {"pullRequest": {"isDraft": false}}}}}
]
```

`existing_prefix_to_ready.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "auth": "Bearer ghs_fixture_token_1", "status": 200, "response": [{"number": 16, "html_url": "https://github.com/acme/web/pull/16", "draft": false, "node_id": "PR_kwDOA16", "title": "[DRAFT] Add search"}]},
  {"method": "PATCH", "path": "/repos/acme/web/pulls/16", "request": {"title": "Add search"}, "status": 200, "response": {"number": 16, "title": "Add search"}}
]
```

`mark_ready_fails.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "auth": "Bearer ghs_fixture_token_1", "status": 200, "response": [{"number": 15, "html_url": "https://github.com/acme/web/pull/15", "draft": true, "node_id": "PR_kwDOA15", "title": "Add search"}]},
  {"method": "POST", "path": "/graphql", "request": {"query": "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }", "variables": {"id": "PR_kwDOA15"}}, "status": 200, "response": {"data": null, "errors": [{"type": "FORBIDDEN", "message": "Resource not accessible by integration"}]}}
]
```

`labels_forbidden.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "auth": "Bearer ghs_fixture_token_1", "status": 200, "response": []},
  {"method": "POST", "path": "/repos/acme/web/pulls", "request": {"title": "Add search", "head": "fugaro/20260927-101500-abcd", "base": "main", "body": "Adds search.", "draft": false}, "status": 201, "response": {"number": 18, "html_url": "https://github.com/acme/web/pull/18", "draft": false, "node_id": "PR_kwDOA18", "title": "Add search"}},
  {"method": "POST", "path": "/repos/acme/web/issues/18/labels", "request": {"labels": ["fugaro"]}, "status": 403, "response": {"message": "Resource not accessible by integration"}}
]
```

`comment.json`:

```json
[
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}},
  {"method": "POST", "path": "/repos/acme/web/issues/12/comments", "auth": "Bearer ghs_fixture_token_1", "request": {"body": "### Fugaro run report"}, "status": 201, "response": {"id": 5001}}
]
```

- [ ] **Step 2: Write the failing tests** — `internal/gitprov/github/github_test.go`

```go
package github

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

const branchName = "fugaro/20260927-101500-abcd"

var ctx = context.Background()

func open(t *testing.T, fixture string) *Provider {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func spec(draft bool, labels, reviewers []string) gitprov.PRSpec {
	return gitprov.PRSpec{Branch: branchName, Base: "main", Title: "Add search", Body: "Adds search.",
		Draft: draft, Labels: labels, Reviewers: reviewers}
}

func TestCreateWithLabelsAndReviewers(t *testing.T) {
	p := open(t, "create_ready.json")
	pr, err := p.EnsurePR(ctx, spec(false, []string{"fugaro"}, []string{"octocat", "acme/platform"}))
	if err != nil || pr != (gitprov.PR{Number: 12, URL: "https://github.com/acme/web/pull/12"}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestDraftUnsupportedFallsBackToTitle covers private repositories whose
// plan has no draft pull requests: the run still gets its PR, marked as a
// draft in the title (design §15).
func TestDraftUnsupportedFallsBackToTitle(t *testing.T) {
	p := open(t, "draft_unsupported.json")
	if pr, err := p.EnsurePR(ctx, spec(true, nil, nil)); err != nil || pr.Number != 16 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestAgentOpenedPRIsOnlyToggled covers a pull request the agent opened
// itself with gh: it is found by branch and converted to a draft, with no
// second pull request and no change to its title, body or labels.
func TestAgentOpenedPRIsOnlyToggled(t *testing.T) {
	p := open(t, "existing_to_draft.json")
	if pr, err := p.EnsurePR(ctx, spec(true, []string{"fugaro"}, []string{"octocat"})); err != nil || pr.Number != 13 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestExistingPRDraftUnsupportedRetitles(t *testing.T) {
	p := open(t, "existing_draft_unsupported.json")
	if pr, err := p.EnsurePR(ctx, spec(true, nil, nil)); err != nil || pr.Number != 17 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestExistingDraftMarkedReady(t *testing.T) {
	p := open(t, "existing_to_ready.json")
	if pr, err := p.EnsurePR(ctx, spec(false, nil, nil)); err != nil || pr.Number != 15 || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestReadyRemovesDraftPrefix(t *testing.T) {
	p := open(t, "existing_prefix_to_ready.json")
	if pr, err := p.EnsurePR(ctx, spec(false, nil, nil)); err != nil || pr.Number != 16 || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestMarkReadyFailureIsAnError(t *testing.T) {
	p := open(t, "mark_ready_fails.json")
	if _, err := p.EnsurePR(ctx, spec(false, nil, nil)); err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("err = %v", err)
	}
}

func TestLabelFailureIsPartial(t *testing.T) {
	p := open(t, "labels_forbidden.json")
	pr, err := p.EnsurePR(ctx, spec(false, []string{"fugaro"}, nil))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 18 || !strings.Contains(err.Error(), "403") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

func TestComment(t *testing.T) {
	p := open(t, "comment.json")
	if err := p.Comment(ctx, gitprov.PR{Number: 12}, "### Fugaro run report"); err != nil {
		t.Fatal(err)
	}
}

func TestGitAuthSetsGHToken(t *testing.T) {
	p := open(t, "token_refresh.json")
	// token_refresh.json holds two mints; the second is for a minValid the
	// first token cannot meet (it expires in 60m).
	a, err := p.GitAuth(ctx, 45*time.Minute)
	if err != nil || a.Username != "x-access-token" || a.Token != "ghs_fixture_token_1" || a.Env["GH_TOKEN"] != a.Token || a.Expires.IsZero() {
		t.Fatalf("auth = %+v, %v", a, err)
	}
	if a, err := p.GitAuth(ctx, 90*time.Minute); err != nil || a.Token != "ghs_fixture_token_2" {
		t.Fatalf("second auth = %+v, %v", a, err)
	}
}

func TestGraphQLURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.github.com":         "https://api.github.com/graphql",
		"https://ghe.example.com/api/v3": "https://ghe.example.com/api/graphql",
		"http://127.0.0.1:5555":          "http://127.0.0.1:5555/graphql",
	} {
		if got := graphqlURL(in); got != want {
			t.Errorf("graphqlURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewValidates(t *testing.T) {
	for _, o := range []Options{{Repo: "web", AppID: "1", PrivateKey: key(t)}, {Owner: "acme", Repo: "web", PrivateKey: key(t)}, {Owner: "acme", Repo: "web", AppID: "1"}} {
		if _, err := New(o); err == nil {
			t.Errorf("New(%+v) succeeded", o)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/gitprov/github/`
Expected: FAIL, compile errors (`undefined: New`, `undefined: Options`, `undefined: graphqlURL`).

- [ ] **Step 4: Implement** — `internal/gitprov/github/github.go`

```go
// Package github implements gitprov.Provider against the GitHub REST and
// GraphQL APIs, authenticated as a GitHub App through installation tokens
// scoped to one repository (design §6.1, §6.2).
package github

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// DefaultBaseURL is the GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

// gitUsername is the HTTPS username git uses with an installation token.
const gitUsername = "x-access-token"

// apiMinValid is how long a token used for an API call must stay valid.
const apiMinValid = 2 * time.Minute

// GraphQL mutations for the draft state, which the REST API cannot change.
const (
	markReadyMutation      = `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }`
	convertToDraftMutation = `mutation($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { pullRequest { isDraft } } }`
)

// Options configure a Provider.
type Options struct {
	Owner, Repo string
	AppID       string // the GitHub App's ID (or client ID)
	PrivateKey  *rsa.PrivateKey
	BaseURL     string // REST API root; empty means DefaultBaseURL
	HTTP        *http.Client
	Now         func() time.Time // nil means time.Now
}

// Provider is a GitHub repository reached through a GitHub App.
type Provider struct {
	owner, repo string
	api         *httpjson.Client // authenticated with the installation token
	graphqlURL  string
	tokens      *tokenSource
}

// New returns a Provider for o.
func New(o Options) (*Provider, error) {
	if o.Owner == "" || o.Repo == "" {
		return nil, errors.New("github: owner and repository are required")
	}
	if o.AppID == "" || o.PrivateKey == nil {
		return nil, errors.New("github: an App ID and private key are required")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	o.BaseURL = strings.TrimSuffix(o.BaseURL, "/")
	if o.Now == nil {
		o.Now = time.Now
	}
	header := http.Header{
		"Accept":               {"application/vnd.github+json"},
		"X-Github-Api-Version": {"2022-11-28"},
		"User-Agent":           {"fugaro"},
	}
	app := &httpjson.Client{BaseURL: o.BaseURL, HTTP: o.HTTP, Header: header, Auth: appAuth(o.AppID, o.PrivateKey, o.Now)}
	p := &Provider{
		owner: o.Owner, repo: o.Repo, graphqlURL: graphqlURL(o.BaseURL),
		tokens: &tokenSource{app: app, owner: o.Owner, repo: o.Repo, now: o.Now},
	}
	p.api = &httpjson.Client{BaseURL: o.BaseURL, HTTP: o.HTTP, Header: header, Auth: func(ctx context.Context) (string, error) {
		tok, _, err := p.tokens.Token(ctx, apiMinValid)
		return "Bearer " + tok, err
	}}
	return p, nil
}

// graphqlURL derives the GraphQL endpoint from the REST root: GitHub
// Enterprise Server serves REST under /api/v3 and GraphQL at /api/graphql.
func graphqlURL(base string) string {
	if strings.HasSuffix(base, "/api/v3") {
		return strings.TrimSuffix(base, "/v3") + "/graphql"
	}
	return base + "/graphql"
}

type pull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	NodeID  string `json:"node_id"`
	Title   string `json:"title"`
}

func (p *Provider) repoPath(suffix string) string {
	return "/repos/" + url.PathEscape(p.owner) + "/" + url.PathEscape(p.repo) + suffix
}

// EnsurePR implements gitprov.Provider. spec.Reviewers are user logins, or
// "org/team" for a team.
func (p *Provider) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	existing, err := p.find(ctx, spec.Branch)
	if err != nil {
		return gitprov.PR{}, err
	}
	if existing != nil {
		return p.update(ctx, *existing, spec.Draft)
	}
	pr, err := p.create(ctx, spec)
	if err != nil {
		return gitprov.PR{}, err
	}
	var errs []error
	if len(spec.Labels) > 0 {
		if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/issues/%d/labels", pr.Number)), map[string]any{"labels": spec.Labels}, nil); err != nil {
			errs = append(errs, fmt.Errorf("adding labels: %w", err))
		}
	}
	if len(spec.Reviewers) > 0 {
		if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/pulls/%d/requested_reviewers", pr.Number)), reviewers(spec.Reviewers), nil); err != nil {
			errs = append(errs, fmt.Errorf("requesting reviewers: %w", err))
		}
	}
	if len(errs) > 0 {
		return pr, &gitprov.PartialError{Err: errors.Join(errs...)}
	}
	return pr, nil
}

func (p *Provider) find(ctx context.Context, branch string) (*pull, error) {
	q := url.Values{"head": {p.owner + ":" + branch}, "state": {"open"}, "per_page": {"1"}}
	var pulls []pull
	if err := p.api.Do(ctx, "GET", p.repoPath("/pulls?"+q.Encode()), nil, &pulls); err != nil {
		return nil, fmt.Errorf("finding the pull request for %s: %w", branch, err)
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return &pulls[0], nil
}

func (p *Provider) create(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	body := map[string]any{"title": spec.Title, "head": spec.Branch, "base": spec.Base, "body": spec.Body, "draft": spec.Draft}
	var pr pull
	err := p.api.Do(ctx, "POST", p.repoPath("/pulls"), body, &pr)
	if err != nil && spec.Draft && draftUnsupported(err) {
		// Private repositories on some plans cannot have draft pull
		// requests: open a normal one marked as a draft in its title.
		body["draft"], body["title"] = false, gitprov.DraftTitle(spec.Title, true)
		err = p.api.Do(ctx, "POST", p.repoPath("/pulls"), body, &pr)
	}
	if err != nil {
		return gitprov.PR{}, fmt.Errorf("creating the pull request for %s: %w", spec.Branch, err)
	}
	return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: spec.Draft}, nil
}

// draftUnsupported reports whether err is GitHub refusing a draft pull
// request ("Draft pull requests are not supported in this repository.").
func draftUnsupported(err error) bool {
	var se *httpjson.StatusError
	return errors.As(err, &se) && se.Status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(se.Body), "draft")
}

// update sets an existing pull request's draft state, and nothing else
// apart from the draft title prefix.
func (p *Provider) update(ctx context.Context, pr pull, draft bool) (gitprov.PR, error) {
	title := gitprov.DraftTitle(pr.Title, false)
	switch {
	case draft && !pr.Draft:
		if err := p.graphql(ctx, convertToDraftMutation, pr.NodeID); err != nil {
			title = gitprov.DraftTitle(pr.Title, true) // no draft support: say so in the title
		}
	case !draft && pr.Draft:
		if err := p.graphql(ctx, markReadyMutation, pr.NodeID); err != nil {
			return gitprov.PR{}, fmt.Errorf("marking pull request #%d ready: %w", pr.Number, err)
		}
	}
	if title != pr.Title {
		if err := p.api.Do(ctx, "PATCH", p.repoPath(fmt.Sprintf("/pulls/%d", pr.Number)), map[string]any{"title": title}, nil); err != nil {
			return gitprov.PR{}, fmt.Errorf("retitling pull request #%d: %w", pr.Number, err)
		}
	}
	return gitprov.PR{Number: pr.Number, URL: pr.HTMLURL, Draft: draft}, nil
}

func (p *Provider) graphql(ctx context.Context, query, id string) error {
	var resp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{"query": query, "variables": map[string]string{"id": id}}
	if err := p.api.Do(ctx, "POST", p.graphqlURL, in, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, len(resp.Errors))
		for i, e := range resp.Errors {
			msgs[i] = e.Message
		}
		return errors.New("graphql: " + strings.Join(msgs, "; "))
	}
	return nil
}

func reviewers(names []string) map[string][]string {
	out := map[string][]string{"reviewers": {}, "team_reviewers": {}}
	for _, n := range names {
		if _, team, ok := strings.Cut(n, "/"); ok {
			out["team_reviewers"] = append(out["team_reviewers"], team)
		} else {
			out["reviewers"] = append(out["reviewers"], n)
		}
	}
	return out
}

// Comment implements gitprov.Provider.
func (p *Provider) Comment(ctx context.Context, pr gitprov.PR, body string) error {
	if err := p.api.Do(ctx, "POST", p.repoPath(fmt.Sprintf("/issues/%d/comments", pr.Number)), map[string]string{"body": body}, nil); err != nil {
		return fmt.Errorf("commenting on pull request #%d: %w", pr.Number, err)
	}
	return nil
}

// GitAuth implements gitprov.Provider: an installation token for git, and
// GH_TOKEN so the agent's gh works too.
func (p *Provider) GitAuth(ctx context.Context, minValid time.Duration) (gitprov.GitAuth, error) {
	tok, exp, err := p.tokens.Token(ctx, minValid)
	if err != nil {
		return gitprov.GitAuth{}, err
	}
	return gitprov.GitAuth{Username: gitUsername, Token: tok, Expires: exp, Env: map[string]string{"GH_TOKEN": tok}}, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/gitprov/github/`
Expected: PASS, with every fixture exchange used. In particular, `TestAgentOpenedPRIsOnlyToggled` makes no create, labels or reviewers calls.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal/ && go vet ./internal/gitprov/github/
git add internal/gitprov/github
git commit -m "gitprov/github: GitHub App provider with GraphQL draft toggling and title fallback"
```

---

### Task 8: `providers.FromEnv`, operator docs, and the §15 answer

**Files:**
- Create: `internal/gitprov/providers/providers.go`, `internal/gitprov/providers/providers_test.go`
- Create: `docs/git-providers.md`
- Modify: `docs/design/v1.md` §15 (the "Remaining" item)

**Interfaces:**
- Consumes: `bitbucket.New` and `Options` (Task 5); `github.New`, `Options` and `ParsePrivateKey` (Tasks 6 and 7); `gitprov.Opener` and `SplitRepo` (Task 3).
- Produces:
  - `providers.FromEnv(env []string, hc *http.Client) gitprov.Opener`. For Bitbucket, the secrets it returns are `[token]`. For GitHub, they are `[private key PEM]`.
  - The constants `providers.EnvBitbucketToken`, `EnvBitbucketAPIURL`, `EnvGitHubAppID`, `EnvGitHubAppKey`, `EnvGitHubAppKeyFile` and `EnvGitHubAPIURL`.

- [ ] **Step 1: Write the failing tests** — `internal/gitprov/providers/providers_test.go`

```go
package providers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

var ctx = context.Background()

func keyPEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

func TestBitbucketFromEnv(t *testing.T) {
	p, secrets, err := FromEnv([]string{EnvBitbucketToken + "=bb-token-1234"}, nil)(ctx, gitprov.KindBitbucket, "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*bitbucket.Provider); !ok || !slices.Equal(secrets, []string{"bb-token-1234"}) {
		t.Fatalf("provider %T, secrets %q", p, secrets)
	}
	a, err := p.GitAuth(ctx, 0)
	if err != nil || a.Token != "bb-token-1234" {
		t.Fatalf("auth = %+v, %v", a, err)
	}
}

func TestGitHubFromEnvKeyOrFile(t *testing.T) {
	pemData := keyPEM(t)
	file := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(file, []byte(pemData), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{
		"inline": {EnvGitHubAppID + "=1234", EnvGitHubAppKey + "=" + pemData},
		"file":   {EnvGitHubAppID + "=1234", EnvGitHubAppKeyFile + "=" + file},
	} {
		p, secrets, err := FromEnv(env, nil)(ctx, gitprov.KindGitHub, "acme/web")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := p.(*github.Provider); !ok || !slices.Equal(secrets, []string{pemData}) {
			t.Fatalf("%s: provider %T, %d secrets", name, p, len(secrets))
		}
	}
}

func TestFromEnvErrors(t *testing.T) {
	for _, tc := range []struct {
		name, kind, repo string
		env              []string
		want             string
	}{
		{"no bitbucket token", gitprov.KindBitbucket, "acme/web", nil, EnvBitbucketToken},
		{"short bitbucket token", gitprov.KindBitbucket, "acme/web", []string{EnvBitbucketToken + "=abc"}, "shorter"},
		{"no app id", gitprov.KindGitHub, "acme/web", []string{EnvGitHubAppKey + "=x"}, EnvGitHubAppID},
		{"no app key", gitprov.KindGitHub, "acme/web", []string{EnvGitHubAppID + "=1"}, EnvGitHubAppKeyFile},
		{"bad app key", gitprov.KindGitHub, "acme/web", []string{EnvGitHubAppID + "=1", EnvGitHubAppKey + "=junk"}, "not PEM"},
		{"bad repo", gitprov.KindBitbucket, "web", []string{EnvBitbucketToken + "=bb-token-1234"}, "owner/name"},
		{"unknown kind", "gitlab", "acme/web", nil, "unknown git provider"},
	} {
		if _, _, err := FromEnv(tc.env, nil)(ctx, tc.kind, tc.repo); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitprov/providers/`
Expected: FAIL, compile errors (`undefined: FromEnv`, `undefined: EnvBitbucketToken`).

- [ ] **Step 3: Implement** — `internal/gitprov/providers/providers.go`

```go
// Package providers opens the real git provider a repository's fugaro.yaml
// names, with credentials from the runner's environment, which the platform
// injects from Secret Manager (design §6). See docs/git-providers.md.
package providers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

// Environment variables the runner reads its provider credentials from.
// None of them ever reaches the agent: it gets a derived token instead
// (FUGARO_GIT_TOKEN, and GH_TOKEN on GitHub).
const (
	EnvBitbucketToken   = "FUGARO_BITBUCKET_TOKEN"             // repository access token
	EnvBitbucketAPIURL  = "FUGARO_BITBUCKET_API_URL"           // optional API root override
	EnvGitHubAppID      = "FUGARO_GITHUB_APP_ID"               // GitHub App ID (or client ID)
	EnvGitHubAppKey     = "FUGARO_GITHUB_APP_PRIVATE_KEY"      // the App's private key, PEM
	EnvGitHubAppKeyFile = "FUGARO_GITHUB_APP_PRIVATE_KEY_FILE" // or a file holding it
	EnvGitHubAPIURL     = "FUGARO_GITHUB_API_URL"              // optional API root override
)

// minSecretLen matches the agent package's redaction floor: a shorter
// value cannot be redacted without mangling ordinary text.
const minSecretLen = 4

// FromEnv returns an Opener that opens providers with credentials from env
// (KEY=VALUE pairs, usually os.Environ()). hc is the HTTP client adapters
// use; nil means their default.
func FromEnv(env []string, hc *http.Client) gitprov.Opener {
	vars := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	return func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		owner, name, ok := gitprov.SplitRepo(repo)
		if !ok {
			return nil, nil, fmt.Errorf("repository %q must look like owner/name", repo)
		}
		switch kind {
		case gitprov.KindBitbucket:
			token, err := secret(vars, EnvBitbucketToken, "a Bitbucket repository access token")
			if err != nil {
				return nil, nil, err
			}
			p, err := bitbucket.New(bitbucket.Options{Workspace: owner, Slug: name, Token: token, BaseURL: vars[EnvBitbucketAPIURL], HTTP: hc})
			return p, []string{token}, err
		case gitprov.KindGitHub:
			appID := vars[EnvGitHubAppID]
			if appID == "" {
				return nil, nil, fmt.Errorf("%s is not set: the github provider needs the GitHub App's ID (see docs/git-providers.md)", EnvGitHubAppID)
			}
			pemData, err := appKey(vars)
			if err != nil {
				return nil, nil, err
			}
			key, err := github.ParsePrivateKey([]byte(pemData))
			if err != nil {
				return nil, nil, err
			}
			p, err := github.New(github.Options{Owner: owner, Repo: name, AppID: appID, PrivateKey: key, BaseURL: vars[EnvGitHubAPIURL], HTTP: hc})
			return p, []string{pemData}, err
		default:
			return nil, nil, fmt.Errorf("unknown git provider %q (want %s or %s)", kind, gitprov.KindGitHub, gitprov.KindBitbucket)
		}
	}
}

func secret(vars map[string]string, name, what string) (string, error) {
	v := vars[name]
	if v == "" {
		return "", fmt.Errorf("%s is not set: the provider needs %s (see docs/git-providers.md)", name, what)
	}
	if len(v) < minSecretLen {
		return "", fmt.Errorf("%s is shorter than %d bytes, so it cannot be redacted safely", name, minSecretLen)
	}
	return v, nil
}

func appKey(vars map[string]string) (string, error) {
	if v := vars[EnvGitHubAppKey]; v != "" {
		return v, nil
	}
	if path := vars[EnvGitHubAppKeyFile]; path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", EnvGitHubAppKeyFile, err)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("neither %s nor %s is set: the github provider needs the GitHub App's private key (see docs/git-providers.md)", EnvGitHubAppKey, EnvGitHubAppKeyFile)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/gitprov/providers/`
Expected: PASS.

- [ ] **Step 5: Write `docs/git-providers.md`**

````markdown
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
- **Labels** (`git.pr.labels`) are ignored. Bitbucket Cloud pull requests have no labels.

### GitHub

Create a **GitHub App** and install it on the repositories Fugaro serves (design §6.1). It needs these repository permissions:
- **Contents: Read & write**
- **Pull requests: Read & write**
- **Issues: Read**
- **Metadata: Read**

Store its App ID and private key as the variables above. At bootstrap, the runner mints an **installation token for this repository only**, restricted to the permissions above. It lasts about an hour. Before each stage, the runner replaces it with a fresh one if it would expire within that stage's timeout plus 5 minutes.

- **Reviewers** are user logins, or `org/team` for a team.
- **Labels** are added after the PR is created. If that fails, the PR stays and the run logs a warning.

## What the agent gets (design §6.2)

The agent's environment carries a short-lived, repository-scoped token. For Bitbucket this is the repository access token itself. It is a convenience, not a security boundary (design §6.1).

- `FUGARO_GIT_TOKEN` and `FUGARO_GIT_USERNAME`, used by the credential helper below.
- `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_n` and `GIT_CONFIG_VALUE_n`. These install a credential helper, scoped to the origin's host, that answers from the two variables above. They also reset any other helper configured for that host. Plain `git push` and `git fetch` just work.
- `GH_TOKEN` (GitHub only), so `gh pr view`, `gh pr edit` and `gh issue view` work.

The token is never written to `.git/config`, to a remote URL, or to a command line.

## Pushing and draft pull requests

- **Pushing.** Finalize pushes only the run's own `fugaro/<run-id>` branch, with `--force-with-lease`. It overwrites the remote branch only when the branch is absent or its tip is a commit the run's checkout has, meaning the agent pushed it, perhaps before amending. A tip pushed from anywhere else is left alone, and the run fails with a clear reason. The base branch is never pushed. Protect it anyway (design §6.1).
- **Draft PRs on GitHub.** Draft state is changed through GraphQL, because REST cannot change it. Some plans have no draft PRs for private repositories. There, Fugaro opens a normal PR titled `[DRAFT] …`, and removes the prefix when a later run marks the PR ready.
- **Draft PRs on Bitbucket Cloud.** The REST API documents a `draft` boolean on create and update, which needs `pullrequest:write`. A repository access token can hold that scope. Fugaro sends `draft` and checks the response. If Bitbucket did not make the PR a draft, it uses the same `[DRAFT] ` title fallback.

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
6. **Record fixtures.** To replace the hand-written ones with recorded ones, wrap the adapter's HTTP client in `httpfixture.Recorder` (the `HTTP` option of `bitbucket.Options` or `github.Options`), with the credential values in `Secrets`, and `Save` the exchanges.
````

- [ ] **Step 6: Update design §15**

In `docs/design/v1.md`, replace the last two lines of §15:

```markdown
**Remaining (to decide during implementation, none blocking M0–M2):**

- Whether a Bitbucket repository access token can edit PR draft state through the API. If it can't, Bitbucket draft PRs fall back to a `[DRAFT]` title prefix.
```

with:

```markdown
**Resolved in M2:**

- **Bitbucket draft PRs.** The Bitbucket Cloud REST API documents a `draft` boolean on the pull request object, set on create (`POST …/pullrequests`) and update (`PUT …/pullrequests/{id}`). Both need the `pullrequest:write` scope, which a repository access token can hold. The adapter sends `draft`, checks the response, and falls back to a `[DRAFT] ` title prefix if Bitbucket ignored it. This is to be confirmed by the first live run ([git-providers.md](../git-providers.md#live-check-against-a-sandbox-repository)).
```

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/ && go vet ./internal/gitprov/providers/
git add internal/gitprov/providers docs/git-providers.md docs/design/v1.md
git commit -m "gitprov/providers: open providers from env credentials; document them"
```

---

### Task 9: Runner: lazy provider selection, git credentials, token refresh, `EnsurePR` retry

This task implements design §4.1 step 6 and §6.2, and makes the provider selectable before `fugaro.yaml` has been read.

`Deps.Provider` becomes `Deps.OpenProvider` (a `gitprov.Opener`). Bootstrap opens the provider as soon as its kind is known. If `Deps.ProviderKind` (the `--provider` flag) or the origin URL's host names the provider, that happens before the clone and fetch, which may need credentials. Otherwise it happens right after `fugaro.yaml` is read, from `git.provider`. A kind named before the config was read must equal `git.provider`, or the run fails with `infra_error`.

The provider's `GitAuth` is fetched at these points:
- at open, valid for `bootstrapAuthMinValid` = 10m
- before every stage, valid for `timeouts.stage + 5m`
- before the finalize push, valid for `timeouts.finalize_reserve`

Its variables go into both the runner's git env and the agent env. Every token and credential value is added to `r.secrets`. The `infra_error` reason and the provider warnings are redacted. Finalize makes up to 3 `EnsurePR` attempts (`Deps.RetryDelay` apart, default 3s), and a `*gitprov.PartialError` counts as success with a warning.

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go` (harness)
- Create: `internal/runner/provider_test.go`
- Modify: `internal/cli/exec.go`, one line only, to keep the build green. Task 11 wires the flag properly.

**Interfaces:**
- Consumes:
  - `gitprov.Opener`, `Static`, `GitAuth`, `PartialError` and `KindForURL` (Task 3)
  - `gitops.CredentialVars`, `CredentialURL`, `WithVars`, `OriginURL` and the lease `Push` (Task 2)
  - `fake.Provider.Auth` and `FailEnsure` (Task 3)
  - `testutil.NewHTTPRemote`, `Token` and `Prefix` (Task 2)
- Produces:
  - `runner.Deps{OpenProvider gitprov.Opener; ProviderKind string; RetryDelay time.Duration; …}`. The `Provider` field is removed.
  - The agent env now carries `FUGARO_GIT_TOKEN`, `FUGARO_GIT_USERNAME` and `GIT_CONFIG_*`, but only when the origin is http(s) and the provider returned a token. It also carries the provider's `GitAuth.Env`, such as `GH_TOKEN`.

- [ ] **Step 1: Update the test harness** in `internal/runner/runner_test.go`

Add `"github.com/dimipaun/fugaro/internal/gitprov"` to the imports, just above `…/gitprov/fake`.

Add a `files` field as the first field of `harness`:

```go
type harness struct {
	files     map[string]string
	deps      runner.Deps
```

In `newHarness`, change the `Deps` literal's first line from `Store: store, Provider: p, Agent: a,` to:

```go
			Store: store, OpenProvider: gitprov.Static(p), Agent: a,
```

Then change the returned struct's last line to `files: files, store: store, bucket: bucket, provider: p, agent: a, remote: remote, failsFile: failsFile,`.

After `newHarness`, add:

```go
// useHTTPRemote replaces the harness's remote with the same files served
// over HTTP behind basic auth that allow decides. Its host names no
// provider, so the run names one up front, as `--provider github` would:
// the clone itself needs credentials.
func (h *harness) useHTTPRemote(t *testing.T, allow func(user, pass string) bool) *testutil.HTTPRemote {
	t.Helper()
	remote := testutil.NewHTTPRemote(t, h.files, allow)
	h.deps.Remote, h.remote = remote.URL, remote.Bare
	h.deps.ProviderKind = "github"
	return remote
}
```

- [ ] **Step 2: Write the failing tests** — `internal/runner/provider_test.go`

```go
package runner_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const gitToken = "tok-git-secret-1234"

func staticAuth(token string) func(time.Duration) gitprov.GitAuth {
	return func(time.Duration) gitprov.GitAuth {
		return gitprov.GitAuth{Username: "x-token-auth", Token: token, Env: map[string]string{"GH_TOKEN": token}}
	}
}

// TestGitCredentialsOverHTTP runs against a remote that demands the
// provider's token, and checks that runner and agent both authenticate
// without the token ever being written to the checkout or a transcript.
func TestGitCredentialsOverHTTP(t *testing.T) {
	h := newHarness(t, "", nil)
	h.useHTTPRemote(t, testutil.Token("x-token-auth", gitToken))
	h.provider.Auth = staticAuth(gitToken)
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fmt.Fprintf(req.Transcript, "{\"token\":%q}\n", envValue(req.Env, "FUGARO_GIT_TOKEN"))
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, leak, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("remote branch %s, want %s", got, rec.HeadSHA)
	}
	env := h.agent.calls[0].Env
	for k, want := range map[string]string{"FUGARO_GIT_TOKEN": gitToken, "FUGARO_GIT_USERNAME": "x-token-auth", "GH_TOKEN": gitToken, "GIT_CONFIG_COUNT": "2"} {
		if got := envValue(env, k); got != want {
			t.Errorf("agent %s = %q, want %q", k, got, want)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(h.deps.WorkDir, ".git", "config")); strings.Contains(string(data), gitToken) {
		t.Fatalf(".git/config holds the token:\n%s", data)
	}
	transcript, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"transcripts/implement-1.jsonl")
	if err != nil || strings.Contains(string(transcript), gitToken) {
		t.Fatalf("transcript leaks the git token (%v): %s", err, transcript)
	}
}

// TestAgentPushAndAmendIsTolerated covers an agent that pushes the run
// branch itself and then amends it: finalize must still push the amended
// commit rather than fail.
func TestAgentPushAndAmendIsTolerated(t *testing.T) {
	h := newHarness(t, "", nil)
	h.useHTTPRemote(t, testutil.Token("x-token-auth", gitToken))
	h.provider.Auth = staticAuth(gitToken)
	pushThenAmend := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo v1 > feature.txt && git add -A && git commit -qm 'Add feature' && git push -q origin HEAD")
		shell(t, req, "echo v2 > feature.txt && git commit -qa --amend -m 'Add feature, amended'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, pushThenAmend, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "log", "-1", "--format=%s", "refs/heads/fugaro/"+runID); got != "Add feature, amended" {
		t.Fatalf("remote branch tip = %q, want the amended commit", got)
	}
}

// TestTokenRefreshedBetweenStages checks that each stage gets a token
// asked to outlive it, that the agent sees the new one, and that every
// token handed out is redacted.
func TestTokenRefreshedBetweenStages(t *testing.T) {
	h := newHarness(t, "", nil)
	h.useHTTPRemote(t, testutil.Prefix("x-token-auth", "tok-"))
	var mu sync.Mutex
	var asked []time.Duration
	h.provider.Auth = func(minValid time.Duration) gitprov.GitAuth {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, minValid)
		return gitprov.GitAuth{Username: "x-token-auth", Token: fmt.Sprintf("tok-%04d", len(asked))}
	}
	echoTokens := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fmt.Fprintf(req.Transcript, "{\"old\":\"tok-0002\",\"new\":%q}\n", envValue(req.Env, "FUGARO_GIT_TOKEN"))
		return review("ship", 0)(t, ctx, req)
	}
	if _, err := h.run(t, implement("feature"), echoTokens); err != nil {
		t.Fatal(err)
	}
	// bootstrap, implement, review, finalize; the fixture's stage timeout
	// is 2m and its finalize reserve 30s.
	want := []time.Duration{10 * time.Minute, 7 * time.Minute, 7 * time.Minute, 30 * time.Second}
	if !slices.Equal(asked, want) {
		t.Fatalf("GitAuth minValid = %v, want %v", asked, want)
	}
	if got := envValue(h.agent.calls[0].Env, "FUGARO_GIT_TOKEN"); got != "tok-0002" {
		t.Fatalf("implement token = %q", got)
	}
	if got := envValue(h.agent.calls[1].Env, "FUGARO_GIT_TOKEN"); got != "tok-0003" {
		t.Fatalf("review token = %q", got)
	}
	transcript, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"transcripts/review-1.jsonl")
	if err != nil || strings.Contains(string(transcript), "tok-000") {
		t.Fatalf("review transcript leaks a token (%v): %s", err, transcript)
	}
}

func TestProviderOpenedFromConfig(t *testing.T) {
	h := newHarness(t, "", nil)
	var kinds, repos []string
	h.deps.OpenProvider = func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		kinds, repos = append(kinds, kind), append(repos, repo)
		return h.provider, nil, nil
	}
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kinds, []string{"github"}) || !slices.Equal(repos, []string{"acme/app"}) {
		t.Fatalf("opened %v for %v, want github once for acme/app", kinds, repos)
	}
}

func TestProviderKindMustMatchConfig(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.ProviderKind = "bitbucket"
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError ||
		!strings.Contains(rec.Reason, "git.provider to github") || !strings.Contains(rec.Reason, "--provider flag says bitbucket") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatal("a PR was opened despite the mismatch")
	}
}

func TestProviderOpenFailureIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.OpenProvider = func(context.Context, string, string) (gitprov.Provider, []string, error) {
		return nil, []string{"s3cr3t-app-key"}, errors.New("rejected key s3cr3t-app-key")
	}
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "rejected key [REDACTED]") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestEnsurePRRetried covers a transient provider failure at finalize: the
// run retries, and ends with exactly one PR.
func TestEnsurePRRetried(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.RetryDelay = time.Millisecond
	h.provider.FailEnsure = 2
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	onlyPR(t, h.provider)
}

func TestEnsurePRGivesUp(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.RetryDelay = time.Millisecond
	h.provider.FailEnsure = 3
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "injected EnsurePR failure") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/runner/`
Expected: FAIL, compile errors (`unknown field OpenProvider in struct literal`, `h.deps.ProviderKind undefined`, `h.deps.RetryDelay undefined`).

- [ ] **Step 4: Implement in `internal/runner/runner.go`**

Add `"maps"` and `"slices"` to the imports.

Replace the first two fields of `Deps` (`Store` and `Provider`) with:

```go
	Store *runstore.Store
	// OpenProvider opens the git provider. It is called once, as soon as
	// the provider's kind is known: before the checkout when ProviderKind
	// or the origin URL's host names it, otherwise right after fugaro.yaml
	// is read, from git.provider.
	OpenProvider gitprov.Opener
	// ProviderKind, when set, names the provider before fugaro.yaml is
	// read, for remotes whose host does not identify it. git.provider
	// must agree with it.
	ProviderKind string
	// RetryDelay is the pause between attempts to open the pull request;
	// zero means 3 seconds.
	RetryDelay time.Duration
```

Replace the closing `}` of `type run struct` (after `cancelled    bool`) with the new fields, the closing brace, and the constants:

```go
	provider     gitprov.Provider
	providerKind string
	providerFrom string          // where providerKind came from, for mismatch errors
	credURL      string          // scheme://host of an HTTPS origin; "" when git needs no token
	auth         gitprov.GitAuth // current git credentials
}

// Git credential lifetimes (design §6.2). A stage must not outlive its
// token, so before each stage the runner asks for one valid for the stage
// timeout plus gitAuthSlack; bootstrap's clone and fetch need only
// bootstrapAuthMinValid.
const (
	gitAuthSlack          = 5 * time.Minute
	bootstrapAuthMinValid = 10 * time.Minute
)

// prAttempts is how many times finalize tries to open the pull request.
// EnsurePR finds what an earlier attempt created, so retrying is safe.
const prAttempts = 3
```

In `Run`'s deferred function, replace the `if err != nil { … }` block with:

```go
		if err != nil {
			// Provider and git errors can quote what they were sent, so
			// the reason is redacted like everything else published.
			reason := r.redact(err.Error())
			// A more specific outcome (such as a bootstrap cancellation) may
			// already be recorded; only fall back to infra_error when the
			// run never got far enough to decide anything else.
			if r.rec.Status == runstore.StatusRunning {
				r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, reason
			}
			d.Log.Error("run failed", "stage", r.rec.Stage, "err", reason)
		}
```

Directly above `// fail records the first reason the run cannot produce a ready PR.`, add:

```go
// redact removes every known secret value from s.
func (r *run) redact(s string) string { return agent.Redact(s, r.secrets) }

// addSecret adds v to the values redacted from everything the run publishes.
func (r *run) addSecret(v string) {
	if v != "" && !slices.Contains(r.secrets, v) {
		r.secrets = append(r.secrets, v)
	}
}

// openProvider opens the provider of kind, which from names the source of
// (for error messages), and fetches its first git credentials.
func (r *run) openProvider(ctx context.Context, kind, from string) error {
	p, secrets, err := r.d.OpenProvider(ctx, kind, r.spec.Repo)
	for _, s := range secrets {
		r.addSecret(s)
	}
	if err != nil {
		return fmt.Errorf("opening the %s provider: %w", kind, err)
	}
	r.provider, r.providerKind, r.providerFrom = p, kind, from
	if err := r.refreshGitAuth(ctx, bootstrapAuthMinValid); err != nil {
		return fmt.Errorf("getting git credentials from the %s provider: %w", kind, err)
	}
	return nil
}

// refreshGitAuth gets git credentials valid for at least minValid and puts
// them in the environment of the runner's git and of the agent (design
// §6.2). With the GitHub provider this is what replaces a near-expiry
// installation token between stages.
func (r *run) refreshGitAuth(ctx context.Context, minValid time.Duration) error {
	auth, err := r.provider.GitAuth(ctx, minValid)
	if err != nil {
		return err
	}
	r.addSecret(auth.Token)
	for _, v := range auth.Env {
		r.addSecret(v)
	}
	r.auth = auth
	vars := r.authVars()
	if r.repo != nil {
		r.repo.Env = gitops.WithVars(r.repo.Env, vars)
	}
	if r.env != nil {
		r.env = gitops.WithVars(r.env, vars)
	}
	return nil
}

// authVars is the environment that carries the current git credentials.
func (r *run) authVars() map[string]string {
	vars := map[string]string{}
	if r.auth.Token != "" && r.credURL != "" {
		maps.Copy(vars, gitops.CredentialVars(r.credURL, r.auth.Username, r.auth.Token))
	}
	maps.Copy(vars, r.auth.Env)
	return vars
}

// originURL returns the checkout's origin URL, or the remote it will be
// cloned from when there is no checkout yet.
func (r *run) originURL(ctx context.Context) string {
	if repo, err := gitops.Open(r.d.WorkDir, nil); err == nil {
		if u, err := repo.OriginURL(ctx); err == nil {
			return u
		}
	}
	return r.d.Remote
}
```

In `bootstrap`, replace:

```go
	repo, err := gitops.OpenOrClone(ctx, r.d.WorkDir, r.d.Remote, gitops.IdentityEnv())
```

with:

```go
	// The clone and fetch below may already need credentials, so the
	// provider is opened now if anything but fugaro.yaml names it.
	origin := r.originURL(ctx)
	r.credURL = gitops.CredentialURL(origin)
	if kind, from := r.d.ProviderKind, "the --provider flag"; kind != "" {
		if err := r.openProvider(ctx, kind, from); err != nil {
			return err
		}
	} else if kind := gitprov.KindForURL(origin); kind != "" {
		if err := r.openProvider(ctx, kind, "the origin URL "+origin); err != nil {
			return err
		}
	}
	repo, err := gitops.OpenOrClone(ctx, r.d.WorkDir, r.d.Remote, gitops.WithVars(gitops.IdentityEnv(), r.authVars()))
```

Still in `bootstrap`, directly after `r.cfg, r.wf, r.rec.Workflow = cfg, wf, name`, add:

```go
	switch {
	case r.provider == nil:
		if err := r.openProvider(ctx, cfg.Git.Provider, "git.provider"); err != nil {
			return err
		}
	case r.providerKind != cfg.Git.Provider:
		return fmt.Errorf("fugaro.yaml sets git.provider to %s, but %s says %s", cfg.Git.Provider, r.providerFrom, r.providerKind)
	}
```

Replace the `BuildEnv` call, which assigned `r.env, r.secrets` directly and so would drop the provider secrets, with:

```go
	maps.Copy(set, r.authVars())
	env, secrets, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{
		Auth: cfg.Agent.Auth, Secrets: secretEnvs, Set: set, PathPrepend: r.d.PathPrepend,
	})
	if err != nil {
		return fmt.Errorf("building agent environment: %w", err)
	}
	r.env = env
	for _, s := range secrets {
		r.addSecret(s)
	}
```

In `stage`, directly after `log := r.d.Log.With("stage", name)`, add the code below. It must come before `NewRedactor` is called, so the new token is redacted.

```go
	// Refresh before the redactors below are built, so they know the new token.
	if err := r.refreshGitAuth(ctx, r.budget.Stage+gitAuthSlack); err != nil {
		log.Warn("refreshing git credentials failed; the agent keeps the current ones", "err", r.redact(err.Error()))
	}
```

In `finalize`, directly before `if err := r.repo.Push(ctx, r.rec.Branch); err != nil {`, add:

```go
	if err := r.refreshGitAuth(ctx, r.wf.Timeouts.FinalizeReserve.Duration); err != nil {
		r.d.Log.Warn("refreshing git credentials failed; pushing with the current ones", "err", r.redact(err.Error()))
	}
```

Still in `finalize`, change `pr, err := r.d.Provider.EnsurePR(ctx, gitprov.PRSpec{` to `pr, err := r.ensurePR(ctx, gitprov.PRSpec{`. Then replace the comment-posting block with:

```go
	if err := r.provider.Comment(ctx, pr, report); err != nil {
		r.d.Log.Warn("posting the run report failed", "err", r.redact(err.Error()))
	}
```

Directly above `// uploadVerifyRecords stores each verify record`, add:

```go
// ensurePR opens or updates the pull request, retrying a failure: a
// transient provider error at this point would otherwise leave a pushed
// branch with no pull request. A *gitprov.PartialError still yields the PR.
func (r *run) ensurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	delay := r.d.RetryDelay
	if delay == 0 {
		delay = 3 * time.Second
	}
	for attempt := 1; ; attempt++ {
		pr, err := r.provider.EnsurePR(ctx, spec)
		var partial *gitprov.PartialError
		if errors.As(err, &partial) {
			r.d.Log.Warn("pull request settings not fully applied", "err", r.redact(err.Error()))
			return pr, nil
		}
		if err == nil || attempt == prAttempts {
			return pr, err
		}
		r.d.Log.Warn("opening the pull request failed; retrying", "attempt", attempt, "err", r.redact(err.Error()))
		select {
		case <-ctx.Done():
			return pr, err
		case <-time.After(delay):
		}
	}
}
```

- [ ] **Step 5: Keep `exec` building** by changing the `runner.Deps` literal in `internal/cli/exec.go` from `Provider: provider` to `OpenProvider: gitprov.Static(provider)`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `gofmt -l internal/ && go vet ./... && go test -race ./internal/runner/ ./internal/cli/ ./internal/e2e/`
Expected: PASS, including every M1 runner and e2e test. They use local-path remotes, which get no credentials and open the fake from `git.provider`.

- [ ] **Step 7: Commit**

```bash
git add internal/runner internal/cli/exec.go
git commit -m "runner: open the provider lazily, authenticate git for runner and agent, refresh tokens, retry EnsurePR"
```

---

### Task 10: Runner: draft log tail and PR text bounds

A draft PR's report carries a **log tail** (design §4.5), chosen in this order:
1. If a stage failed (an error or `is_error`), the end of that stage's stderr. If its stderr was empty, the end of its transcript. Both are already redacted, because they are teed after the `Redactor`.
2. Otherwise, if the last verify record failed, that record's output tail from Task 1, redacted here.
3. A ready PR carries no tail.

The tail is bounded by `logtail.DefaultLines` × `DefaultLineBytes`.

The PR title is clipped to 200 runes and the body to 60,000 bytes. This keeps well under GitHub's limits (256-character titles, 65,536-character bodies), so a long agent-written `pr.md` cannot fail finalize.

**Files:**
- Modify: `internal/runner/report.go` (`LogTail`, `fence`, and the `Report` signature)
- Modify: `internal/runner/runner.go` (`stage`, `keepTail`, `logTail`, `finalize`'s `Report` call, `prText`)
- Modify: `internal/runner/pure_test.go` (the two existing `Report(` calls gain a `nil` argument; one new test)
- Create: `internal/runner/tail_test.go`

**Interfaces:**
- Consumes: `logtail.New`, `DefaultLines`, `DefaultLineBytes` and `(*Writer).Lines` (Task 1); `verify.LogTail` (Task 1).
- Produces:
  - `runner.LogTail{Source string; Lines []string}`
  - `runner.Report(rec *runstore.Record, location string, tail *LogTail) string`. A nil `tail` renders no tail section.

- [ ] **Step 1: Write the failing tests**

In `internal/runner/pure_test.go`, change `Report(rec, "runs/acme-app/20260926-221530-abcd/")` to `Report(rec, "runs/acme-app/20260926-221530-abcd/", nil)` and `Report(rec, "location/")` to `Report(rec, "location/", nil)`. Then append:

```go
func TestReportLogTail(t *testing.T) {
	rec := &runstore.Record{RunID: "20260927-000000-abcd", Outcome: runstore.OutcomeDraft, Reason: "stage implement failed: boom"}
	got := Report(rec, "loc/", &LogTail{Source: "stage implement-1, stderr", Lines: []string{"a ``` b", "boom"}})
	// The fence is longer than any backtick run in the tail, so the tail
	// cannot close the code block early.
	want := "**Log tail** (stage implement-1, stderr):\n\n````text\na ``` b\nboom\n````\n\n"
	if !strings.Contains(got, want) {
		t.Fatalf("report lacks %q:\n%s", want, got)
	}
	if strings.Contains(Report(rec, "loc/", nil), "Log tail") {
		t.Fatal("a nil tail rendered a section")
	}
}
```

Create `internal/runner/tail_test.go`:

```go
package runner_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestFailedStageLogTailInDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	secret := envValue(h.deps.Env, "ANTHROPIC_API_KEY")
	crash := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for i := 1; i <= 50; i++ {
			fmt.Fprintf(req.Stderr, "line %02d\n", i)
		}
		fmt.Fprintf(req.Stderr, "fatal: bad key %s\n", secret)
		return agent.Result{}, errors.New("claude exited with status 1")
	}
	rec, err := h.run(t, crash)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "stage implement failed: claude exited with status 1" {
		t.Fatalf("record = %+v", rec)
	}
	report := onlyPR(t, h.provider).Comments[0]
	for _, want := range []string{"**Log tail** (stage implement-1, stderr):", "line 12", "line 50", "fatal: bad key [REDACTED]"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	// 51 lines were written; only the last 40 are kept.
	if strings.Contains(report, "line 11") || strings.Contains(report, secret) {
		t.Fatalf("report keeps too much, or leaks the secret:\n%s", report)
	}
}

func TestFailedVerifyLogTailInDraft(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "test: sh test.sh", "test: echo running the suite; sh test.sh", 1)
	h := newHarness(t, cfg, nil)
	h.fails(t, "beta")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Reason != "tests failing on the final commit" {
		t.Fatalf("record = %+v", rec)
	}
	report := onlyPR(t, h.provider).Comments[0]
	if !strings.Contains(report, "**Log tail** (fugaro verify test #1):") || !strings.Contains(report, "running the suite") {
		t.Fatalf("report lacks the verify tail:\n%s", report)
	}
}

func TestReadyReportHasNoLogTail(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if report := onlyPR(t, h.provider).Comments[0]; strings.Contains(report, "Log tail") {
		t.Fatalf("a ready PR's report has a log tail:\n%s", report)
	}
}

// TestReviewDraftWithPassingTestsHasNoLogTail: a draft caused by review
// findings, with the last verify passing, has no failure output to show.
func TestReviewDraftWithPassingTestsHasNoLogTail(t *testing.T) {
	h := newHarness(t, "", nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Try again'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("changes", 1))
	if err != nil || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if report := onlyPR(t, h.provider).Comments[0]; strings.Contains(report, "Log tail") {
		t.Fatalf("report has a log tail:\n%s", report)
	}
}

// TestLongPRTextIsBounded covers an agent whose pr.md exceeds what the
// providers accept: the PR still opens, with the text clipped.
func TestLongPRTextIsBounded(t *testing.T) {
	h := newHarness(t, "", nil)
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		text := "# " + strings.Repeat("t", 300) + "\n\n" + strings.Repeat("é", 50_000)
		if werr := os.WriteFile(filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md"), []byte(text), 0o644); werr != nil {
			t.Fatal(werr)
		}
		return res, err
	}
	if _, err := h.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	pr := onlyPR(t, h.provider)
	if title := []rune(pr.Spec.Title); len(title) != 200 || title[199] != '…' {
		t.Fatalf("title has %d runes, ends %q", len(title), string(title[len(title)-1]))
	}
	body := pr.Spec.Body
	if len(body) > 60000 || !strings.HasSuffix(body, "longer than a pull request allows.)*") || !strings.HasPrefix(body, "éé") {
		t.Fatalf("body is %d bytes, ends %q", len(body), body[len(body)-40:])
	}
	if !utf8Valid(body) {
		t.Fatal("clipping split a rune")
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "\uFFFD") == s }
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/runner/`
Expected: FAIL, compile errors (`undefined: LogTail`, `too many arguments in call to Report`).

- [ ] **Step 3: Implement the report section** in `internal/runner/report.go`

Replace `func Report(rec *runstore.Record, location string) string {` with:

```go
// LogTail is the end of the output that explains a draft PR (design §4.5).
type LogTail struct {
	Source string   // what the lines are from, such as "stage implement-1, stderr"
	Lines  []string // already redacted
}

// Report renders the run report posted to the PR and stored as report.md.
// tail, when non-nil, is shown in a code block.
func Report(rec *runstore.Record, location string, tail *LogTail) string {
```

Delete the old doc comment line above it (`// Report renders the run report posted to the PR and stored as report.md.`), because the new block carries it. Then, directly before the `fmt.Fprintf` that prints "Transcripts and verify records", which is the last line before `return b.String()`, add:

```go
	if tail != nil && len(tail.Lines) > 0 {
		text := strings.Join(tail.Lines, "\n")
		f := fence(text)
		fmt.Fprintf(&b, "**Log tail** (%s):\n\n%stext\n%s\n%s\n\n", tail.Source, f, text, f)
	}
```

At the end of the file, append:

```go
// fence returns a backtick fence longer than any backtick run in s, so s
// cannot end the code block early.
func fence(s string) string {
	longest, run := 0, 0
	for _, c := range s {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}
```

- [ ] **Step 4: Implement the runner side** in `internal/runner/runner.go`

Add `"io"` to the standard imports, and `"github.com/dimipaun/fugaro/internal/logtail"` to the internal ones.

Add a field at the end of `type run struct`, below `auth`:

```go
	tail         *LogTail        // output of the first failed stage, for the draft PR
```

In `stage`, replace:

```go
	var transcript bytes.Buffer
	tw := agent.NewRedactor(&transcript, r.secrets)
	sw := agent.NewRedactor(NewLineWriter(log, "agent"), r.secrets)
```

with:

```go
	var transcript bytes.Buffer
	stderrTail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	transcriptTail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	tw := agent.NewRedactor(io.MultiWriter(&transcript, transcriptTail), r.secrets)
	sw := agent.NewRedactor(io.MultiWriter(NewLineWriter(log, "agent"), stderrTail), r.secrets)
```

In the same function, replace the closing `switch`:

```go
	switch {
	case err != nil:
		r.fail(StageError(name, stageCtx, r.budget, err))
		r.cancelled = errors.Is(context.Cause(stageCtx), ErrCancelled)
		return res, false
	case res.IsError:
		r.fail(fmt.Sprintf("stage %s: the agent reported an error (%s)", name, res.Subtype))
		return res, false
	}
```

with:

```go
	switch {
	case err != nil:
		r.fail(StageError(name, stageCtx, r.budget, err))
		r.cancelled = errors.Is(context.Cause(stageCtx), ErrCancelled)
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false
	case res.IsError:
		r.fail(fmt.Sprintf("stage %s: the agent reported an error (%s)", name, res.Subtype))
		r.keepTail(fmt.Sprintf("%s-%d", name, n), stderrTail, transcriptTail)
		return res, false
	}
```

Add these methods after `stage`:

```go
// keepTail remembers the failing stage's output for the draft PR (design
// §4.5): the end of its stderr, or of its transcript when stderr was empty.
// Both were redacted on the way in. Only the first failure is kept, as
// with fail.
func (r *run) keepTail(stage string, stderr, transcript *logtail.Writer) {
	if r.tail != nil {
		return
	}
	if lines := stderr.Lines(); len(lines) > 0 {
		r.tail = &LogTail{Source: "stage " + stage + ", stderr", Lines: lines}
	} else if lines := transcript.Lines(); len(lines) > 0 {
		r.tail = &LogTail{Source: "stage " + stage + ", transcript", Lines: lines}
	}
}

// logTail picks the log tail a draft PR carries: the failing stage's
// output if a stage failed, otherwise the output of the last verify run if
// that run failed. A ready PR carries none.
func (r *run) logTail(ready bool, records []verify.Record) *LogTail {
	if ready {
		return nil
	}
	if r.tail != nil {
		return r.tail
	}
	if len(records) == 0 {
		return nil
	}
	last := records[len(records)-1]
	if last.Passed {
		return nil
	}
	text, err := verify.LogTail(r.d.StateDir, last.N)
	if err != nil {
		r.d.Log.Warn("reading the verify log tail failed", "n", last.N, "err", err)
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return &LogTail{Source: fmt.Sprintf("fugaro verify %s #%d", last.Kind, last.N), Lines: strings.Split(r.redact(text), "\n")}
}
```

In `finalize`, change:

```go
	report := agent.Redact(Report(r.rec, r.d.Store.Prefix()), r.secrets)
```

to:

```go
	report := agent.Redact(Report(r.rec, r.d.Store.Prefix(), r.logTail(ready, records)), r.secrets)
```

Rename the M1 `prText` to `rawPRText`, changing its doc comment's first words to `// rawPRText returns`. Then add this above it:

```go
// Pull request text bounds. They keep well under GitHub's limits (titles
// of 256 characters, bodies of 65,536), so a long pr.md cannot fail
// finalize.
const (
	maxTitleRunes = 200
	maxBodyBytes  = 60000
)

const truncatedNote = "\n\n*(Truncated by Fugaro: the description was longer than a pull request allows.)*"

// prText returns the pull request's title and body, redacted and clipped
// to what every provider accepts.
func (r *run) prText() (string, string) {
	title, body := r.rawPRText()
	if runes := []rune(title); len(runes) > maxTitleRunes {
		title = string(runes[:maxTitleRunes-1]) + "…"
	}
	if len(body) > maxBodyBytes {
		body = strings.ToValidUTF8(body[:maxBodyBytes-len(truncatedNote)], "") + truncatedNote
	}
	return title, body
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `gofmt -l internal/ && go vet ./internal/runner/ && go test -race ./internal/runner/`
Expected: PASS. `TestLongPRTextIsBounded` uses a two-byte rune (`é`), so a clip that split a rune would fail `utf8Valid`.

- [ ] **Step 6: Commit**

```bash
git add internal/runner
git commit -m "runner: bounded, redacted log tail on draft PRs; clip PR title and body"
```

---

### Task 11: `fugaro exec --provider` and the Bitbucket end-to-end test

`--provider` now accepts `github`, `bitbucket`, `fake`, or empty (the default, from `$FUGARO_GIT_PROVIDER`):
- `fake` behaves as in M1: the file-backed provider, whatever the kind.
- Empty, `github` and `bitbucket` use `providers.FromEnv`.
- A named kind is opened before the checkout, and must match `git.provider`.
- `--provider-state` applies only to `fake`.

The end-to-end test runs the real binary with the real Bitbucket adapter against three stand-ins: a stateful fake of the Bitbucket PR API, a git remote over HTTP that demands the token, and the fake `claude`. The agent pushes the run branch itself and then amends it.

**Files:**
- Modify: `internal/cli/exec.go` (the flag, `runExec`, and `openProvider` replaced by `providerOptions`)
- Modify: `internal/cli/exec_test.go` (replace `TestExecNeedsFakeProviderUntilM2`)
- Create: `internal/e2e/bitbucket_test.go`

**Interfaces:**
- Consumes: `providers.FromEnv` (Task 8); `gitprov.Static` (Task 3); `runner.Deps.OpenProvider` and `ProviderKind` (Task 9); `testutil.NewHTTPRemote` and `Token` (Task 2); and from M1's e2e package, the constants `runID`, `reviewShip` and `childTimeout`, plus `testutil.BuildFugaro`, `FakeClaude` and `FakeClaudeCalls`.
- Produces: the CLI contract `fugaro exec --provider github|bitbucket|fake|""`, with the env default `FUGARO_GIT_PROVIDER`.

- [ ] **Step 1: Write the failing CLI tests**

In `internal/cli/exec_test.go`, replace `TestExecNeedsFakeProviderUntilM2` with:

```go
func TestExecRejectsUnknownProvider(t *testing.T) {
	t.Setenv("FUGARO_RUN", "")
	t.Setenv("FUGARO_GIT_PROVIDER", "gitlab") // the flag's default comes from here
	_, _, err := execute(t, "exec", "--bucket", "file://"+t.TempDir(), "--run", "acme-app/20260926-221530-abcd")
	if err == nil || !strings.Contains(err.Error(), `github, bitbucket or fake, not "gitlab"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecProviderStateNeedsFake(t *testing.T) {
	t.Setenv("FUGARO_RUN", "")
	t.Setenv("FUGARO_GIT_PROVIDER", "")
	_, _, err := execute(t, "exec", "--bucket", "file://"+t.TempDir(), "--run", "acme-app/20260926-221530-abcd",
		"--provider", "bitbucket", "--provider-state", "state.json")
	if err == nil || !strings.Contains(err.Error(), "--provider-state only applies to --provider fake") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Write the failing end-to-end test** — `internal/e2e/bitbucket_test.go`

```go
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// bitbucketAPI is a minimal stateful stand-in for the Bitbucket Cloud pull
// request API: exactly the calls the bitbucket provider makes on a new run.
type bitbucketAPI struct {
	t        *testing.T
	token    string
	mu       sync.Mutex
	prs      []bbPR
	comments []string
}

type bbPR struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Draft  bool   `json:"draft"`
	Branch string `json:"-"`
	Links  struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

// bbRequest holds every request body field the provider sends.
type bbRequest struct {
	Title  string `json:"title"`
	Draft  bool   `json:"draft"`
	Source struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"source"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
}

func (a *bitbucketAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+a.token {
		a.t.Errorf("%s %s: wrong Authorization header", r.Method, r.URL.Path)
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	var in bbRequest
	if r.Method != http.MethodGet {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			a.t.Errorf("%s %s: %v", r.Method, r.URL.Path, err)
		}
	}
	const prs = "/2.0/repositories/acme/app/pullrequests"
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == prs:
		values := []bbPR{}
		for _, pr := range a.prs {
			if strings.Contains(r.URL.Query().Get("q"), `"`+pr.Branch+`"`) {
				values = append(values, pr)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"values": values})
	case r.Method == http.MethodPost && r.URL.Path == prs:
		pr := bbPR{ID: len(a.prs) + 1, Title: in.Title, Draft: in.Draft, Branch: in.Source.Branch.Name}
		pr.Links.HTML.Href = fmt.Sprintf("https://bitbucket.org/acme/app/pull-requests/%d", pr.ID)
		a.prs = append(a.prs, pr)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(pr)
	case r.Method == http.MethodPost && r.URL.Path == prs+"/1/comments":
		a.comments = append(a.comments, in.Content.Raw)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": len(a.comments)})
	default:
		a.t.Errorf("unexpected %s %s", r.Method, r.URL)
		http.Error(w, "unexpected", http.StatusNotFound)
	}
}

// TestBitbucketRunOverHTTP runs the fugaro binary against a git remote that
// demands the Bitbucket token over HTTP, and a stand-in Bitbucket API. The
// agent pushes the run branch itself and then amends it. The run must end
// with the amended commit on the remote, one ready PR with its report, and
// the token nowhere in what the run wrote.
func TestBitbucketRunOverHTTP(t *testing.T) {
	testutil.IsolateGit(t)
	const token = "bb-e2e-token-5678"
	files := testutil.FixtureFiles(t)
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "provider: github", "provider: bitbucket", 1)
	remote := testutil.NewHTTPRemote(t, files, testutil.Token("x-token-auth", token))
	api := &bitbucketAPI{t: t, token: token}
	apiSrv := httptest.NewServer(api)
	defer apiSrv.Close()

	implement := `{"shell":"echo v1 > feature.txt && git add -A && git commit -qm 'Add feature' && git push -q origin HEAD && echo v2 > feature.txt && git commit -qa --amend -m 'Add feature' && fugaro verify test; printf 'Add feature\\n\\nAdds feature.txt.\\n' > \"$FUGARO_STATE_DIR/pr.md\"","text":"pushed with ${FUGARO_GIT_TOKEN}","cost":1}`
	claude := testutil.FakeClaude(t, `{"calls":[`+implement+`,`+reviewShip+`]}`)
	fugaro := testutil.BuildFugaro(t)
	tmp := t.TempDir()
	bucket, work := filepath.Join(tmp, "bucket"), filepath.Join(tmp, "work")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(tmp, "task.json")
	if err := os.WriteFile(taskFile, []byte(`{"version":1,"run_id":"`+runID+`","repo":"acme/app","ref":"main","task":"Add a feature"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, fugaro, "exec",
		"--bucket", "file://"+bucket, "--task-file", taskFile,
		"--workdir", work, "--remote", remote.URL, "--state-dir", filepath.Join(tmp, "state"),
		"--provider", "bitbucket", "--claude", claude, "--cancel-poll", "100ms")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + tmp,
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"ANTHROPIC_API_KEY=test-key-1234", "FIXTURE_FAILS_FILE=" + filepath.Join(tmp, "fails"),
		"FUGARO_BITBUCKET_TOKEN=" + token, "FUGARO_BITBUCKET_API_URL=" + apiSrv.URL + "/2.0",
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGQUIT) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Logf("fugaro exec output (err=%v):\n%s", err, out)
	if err != nil {
		t.Fatal(err)
	}

	var rec runstore.Record
	data, err := os.ReadFile(filepath.Join(bucket, "runs", "acme-app", runID, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady || rec.PR == nil || rec.PR.URL != "https://bitbucket.org/acme/app/pull-requests/1" {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, remote.Bare, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("remote branch %s, want the final head %s", got, rec.HeadSHA)
	}
	if n := testutil.Git(t, remote.Bare, "rev-list", "--count", "main..refs/heads/fugaro/"+runID); n != "1" {
		t.Fatalf("%s commits ahead of main, want only the amended one", n)
	}
	api.mu.Lock()
	prs, comments := api.prs, api.comments
	api.mu.Unlock()
	if len(prs) != 1 || prs[0].Draft || prs[0].Title != "Add feature" || len(comments) != 1 || !strings.Contains(comments[0], "ready for review") {
		t.Fatalf("PRs %+v, comments %q", prs, comments)
	}
	env := testutil.FakeClaudeCalls(t, claude)[0].Env
	if !slices.Contains(env, "FUGARO_GIT_TOKEN="+token) || slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "FUGARO_BITBUCKET_TOKEN=") }) {
		t.Fatalf("agent env: want FUGARO_GIT_TOKEN and no FUGARO_BITBUCKET_TOKEN, got %q", env)
	}
	for _, f := range []string{
		filepath.Join(work, ".git", "config"),
		filepath.Join(bucket, "runs", "acme-app", runID, "result.json"),
		filepath.Join(bucket, "runs", "acme-app", runID, "report.md"),
		filepath.Join(bucket, "runs", "acme-app", runID, "transcripts", "implement-1.jsonl"),
	} {
		if data, err := os.ReadFile(f); err != nil || strings.Contains(string(data), token) {
			t.Errorf("%s: unreadable (%v) or holds the token", f, err)
		}
	}
	if strings.Contains(string(out), token) {
		t.Error("the runner's log output holds the token")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'Provider' && go test ./internal/e2e/ -run TestBitbucketRunOverHTTP`
Expected: FAIL. The CLI tests fail on the M1 message "real git providers are not implemented yet", and the e2e run exits 1 with the same message.

- [ ] **Step 4: Implement** in `internal/cli/exec.go`

Add `"github.com/dimipaun/fugaro/internal/gitprov/providers"` to the imports.

Replace the `--provider` flag line with:

```go
	f.StringVar(&o.provider, "provider", os.Getenv("FUGARO_GIT_PROVIDER"), `git provider: github or bitbucket to open it before fugaro.yaml is read (it must match git.provider), or "fake" for the file-backed test provider; empty lets the origin URL's host, then git.provider, decide`)
```

In `runExec`, replace:

```go
	provider, err := openProvider(o)
	if err != nil {
		return err
	}
```

with:

```go
	env := os.Environ()
	providerKind, openProvider, err := providerOptions(o, env)
	if err != nil {
		return err
	}
```

In the `runner.Deps` literal, replace `OpenProvider: gitprov.Static(provider)` with `OpenProvider: openProvider, ProviderKind: providerKind`, and `Env: os.Environ()` with `Env: env`.

Replace the M1 `openProvider` function with:

```go
// providerOptions turns --provider into the runner's provider settings: the
// kind to open before fugaro.yaml is read ("" lets the origin URL's host or
// git.provider decide), and how to open it.
func providerOptions(o execOptions, env []string) (string, gitprov.Opener, error) {
	switch o.provider {
	case "fake":
		return "", gitprov.Static(&fake.Provider{Path: o.providerState}), nil
	case "", gitprov.KindGitHub, gitprov.KindBitbucket:
		if o.providerState != "" {
			return "", nil, errors.New("--provider-state only applies to --provider fake")
		}
		return o.provider, providers.FromEnv(env, nil), nil
	default:
		return "", nil, fmt.Errorf("--provider must be github, bitbucket or fake, not %q", o.provider)
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/cli/ ./internal/e2e/`
Expected: PASS, including every M1 e2e scenario. Those still run with `--provider fake`.

- [ ] **Step 6: Run the whole suite as CI does**

Run: `test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...`
Expected: every package passes.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/exec.go internal/cli/exec_test.go internal/e2e/bitbucket_test.go
git commit -m "exec: --provider github|bitbucket|fake with env credentials; Bitbucket end-to-end over authenticated HTTP"
```
