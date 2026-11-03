package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The mutation check's tests (D885, D891(a), CSR4b). The child is started for
// real — the head is a real clone of a real remote and `go test` runs in it —
// but unfenced (mutationUnfenced): whether unshare is usable is a property of
// the execution environment, which the owner's probe measured in the deployed
// image and this one makes no promise about (U7). What the fence IS is pinned
// by TestMutationSubprocessCarriesNoTokenInItsEnvironment, so the argv the
// child would really be started with is a fact a test refuses to let drift.

// mutationSeed is the reviewed head these tests check out: a Go module whose
// package holds one boolean literal a test reads, a test that never finishes,
// and a test that reports the environment it was started with. The head's own
// tests are what a mutant runs — the check asks whether they would have caught
// the edit, which only they can answer.
var mutationSeed = map[string]string{
	"go.mod": "module example.invalid/head\n\ngo 1.21\n",
	"strict.go": `package head

const strict = true

func Strict() bool { return strict }
`,
	"strict_test.go": `package head

import "testing"

func TestStrict(t *testing.T) {
	if !Strict() {
		t.Fatal("strict is off")
	}
}
`,
	"env_test.go": `package head

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChildEnvironment(t *testing.T) {
	dir := os.Getenv("TMPDIR")
	if dir == "" {
		t.Fatal("TMPDIR is not set")
	}
	if err := os.WriteFile(filepath.Join(dir, "child-env.txt"), []byte(strings.Join(os.Environ(), "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	"slow_test.go": `package head

import (
	"testing"
	"time"
)

func TestSlow(t *testing.T) {
	time.Sleep(30 * time.Second)
}
`,
}

// mutationMarker is what every name the child must not be handed holds in the
// runner's environment here. It is deliberately not key-shaped: what the test
// proves is that a variable outside the allow-list does not reach the child,
// and a value that could be mistaken for a credential would be one.
const mutationMarker = "runner-environment-value-that-must-not-reach-the-child"

// mutationAsk is the task the review job was launched with, decoded through
// task.Parse so the ask travels the way it really does: the field, the strict
// decode that refuses a name this build does not know, and the validation. ask
// is the field's own JSON, ",mutate":[…] or "" for a review that asked for
// none.
func mutationAsk(t *testing.T, ask string) *task.Spec {
	t.Helper()
	spec, err := task.Parse([]byte(fmt.Sprintf(
		`{"version":1,"run_id":%q,"repo":"acme/app","ref":"main","review_prs":[7]%s}`, svcRunID, ask)))
	if err != nil {
		t.Fatalf("the task a review job is launched with: %v", err)
	}
	return spec
}

// mutationGoBin is the Go toolchain's own bin directory. It goes first on the
// child's PATH because this repository's own `go` is a mise shim that resolves
// a version out of the repository it is started in, and the mutant runs in a
// worktree that has no mise.toml to resolve one from; the job image's own
// environment holds the installed toolchain's bin directory directly
// (images/base/Dockerfile), so this is the PATH a review job has rather than a
// test-only arrangement. The test process's own PATH is pointed at it too, so
// that the child is started from the toolchain itself rather than from the
// shim.
func mutationGoBin(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("resolving this toolchain's GOROOT: %v", err)
	}
	bin := filepath.Join(strings.TrimSpace(string(out)), "bin")
	if _, err := os.Stat(filepath.Join(bin, "go")); err != nil {
		t.Fatalf("the toolchain's own go: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

// mutationReview is the review job's run as the check meets it: a real clone
// of a real remote (so a push would be a push), the head resolved, the
// review job's own environment — the job's marker, variables that must not
// reach the child, and a toolchain the child resolves through — and the
// worktree at r.d.WorkDir. It returns the run and the remote it cloned.
func mutationReview(t *testing.T, spec *task.Spec) (*run, string) {
	t.Helper()
	testutil.IsolateGit(t)
	bin := mutationGoBin(t)
	remote := testutil.NewRemote(t, mutationSeed)
	work := filepath.Join(t.TempDir(), "work")
	repo, err := gitops.OpenOrClone(context.Background(), work, remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	state, tmp, cache := t.TempDir(), t.TempDir(), t.TempDir()
	path := bin + string(os.PathListSeparator) + os.Getenv("PATH")
	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.repo, r.spec, r.review = repo, spec, true
		r.reviewHeads = []reviewHead{{Number: 7, SHA: testutil.Git(t, work, "rev-parse", "HEAD")}}
		r.d.WorkDir, r.d.StateDir = work, state
		r.env = []string{
			"PATH=" + path,
			"HOME=" + state,
			"TMPDIR=" + tmp,
			"GOCACHE=" + cache,
			"ANTHROPIC_API_KEY=" + mutationMarker,
			"CLAUDE_CODE_OAUTH_TOKEN=" + mutationMarker,
			"FUGARO_UNLISTED_VALUE=" + mutationMarker,
			EnvReviewJob + "=1",
		}
		r.d.Env = r.env
	})
	return r, remote
}

// mutationUnfenced runs the check's child without the fence. Whether unshare
// works is the execution environment's business and not this test's (U7), and
// what the check does with the child's ending is what these tests are for.
func mutationUnfenced(t *testing.T) {
	t.Helper()
	fence := mutationFence
	mutationFence = nil
	t.Cleanup(func() { mutationFence = fence })
}

// mutationWrite puts the runner's own copy of a seed file into the worktree:
// bytes the head's own commit does not hold, so that a revert which reaches
// for git rather than for what the runner read cannot pass for one.
func mutationWrite(t *testing.T, r *run, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.d.WorkDir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mutationRead is what the worktree holds for a path now.
func mutationRead(t *testing.T, r *run, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.d.WorkDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// mutationRequireReverted is the one claim every arm makes: the worktree holds
// the bytes the check read, whatever the child's ending was.
func mutationRequireReverted(t *testing.T, r *run, name, want string) {
	t.Helper()
	if got := mutationRead(t, r, name); got != want {
		t.Fatalf("%s holds %q after the check, want the bytes the check read (%q)", name, got, want)
	}
}

// mutationRefusePush installs a pre-receive hook on the remote that refuses
// every push and leaves a marker behind, so that a check which pushed at all
// cannot do it silently (refused_test.go's refuseAll is the same trick for the
// harness). It returns the marker's path.
func mutationRefusePush(t *testing.T, remote string) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "pushed")
	hook := "#!/bin/sh\ncat >/dev/null\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	return marker
}

// mutationRefs is every ref the remote holds: a push adds one or moves one.
func mutationRefs(t *testing.T, remote string) string {
	t.Helper()
	return testutil.Git(t, remote, "for-each-ref", "--format=%(refname) %(objectname)")
}

// mutationTaskSchema is the published task schema, compiled: the ask's own
// rules live in internal/task and in schemas/ as two copies, and a test that
// checks one of them here checks the other too.
func mutationTaskSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "task.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	id := doc.(map[string]any)["$id"].(string)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// mutationInstance is a task as JSON, for the schema to refuse or accept.
func mutationInstance(t *testing.T, ask string) any {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(fmt.Sprintf(
		`{"version":1,"run_id":%q,"repo":"acme/app","ref":"main","review_prs":[7]%s}`, svcRunID, ask)))
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

// TestMutationAppliesRunsRevertsAndPushesNothing: the check flips the head's
// own boolean literal, runs the head's own named test against it, reverts the
// file to the bytes it read, and reaches no push.
//
// The worktree is given bytes the head's commit does not hold before the check
// runs, so the revert is the runner's memory or it is nothing: `git checkout
// -- strict.go` would put the committed bytes back and fail the last assertion
// (spec §2 — a revert through git would let the reviewed head's own
// .gitattributes filters, core.fsmonitor and hooks choose what the revert
// runs). The remote's pre-receive hook refuses every push and leaves a marker,
// and its refs are compared before and after, so "never pushes" is a fact
// rather than the absence of an error.
func TestMutationAppliesRunsRevertsAndPushesNothing(t *testing.T) {
	mutationUnfenced(t)
	r, remote := mutationReview(t, mutationAsk(t, `,"mutate":[{"file":"strict.go","test":"TestStrict"}]`))
	own := mutationSeed["strict.go"] + "\n// the runner's own copy of this file\n"
	mutationWrite(t, r, "strict.go", own)
	marker := mutationRefusePush(t, remote)
	before := mutationRefs(t, remote)

	if err := r.runMutationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	checks := r.reviewHeads[0].Mutation
	if len(checks) != 1 {
		t.Fatalf("mutation_checks = %+v, want one check", checks)
	}
	c := checks[0]
	if c.File != "strict.go" || c.Test != "TestStrict" {
		t.Errorf("check = %+v, want strict.go's TestStrict", c)
	}
	if c.Observed != "fail" {
		t.Fatalf("observed = %q, want fail: the head's own TestStrict reads the flipped literal", c.Observed)
	}
	if !strings.Contains(c.Applied, "line 3") || !strings.Contains(c.Applied, "true -> false") {
		t.Errorf("applied = %q, want the boolean literal on line 3 flipped", c.Applied)
	}
	if !c.Reverted {
		t.Error("the artifact does not say the mutant was reverted")
	}
	mutationRequireReverted(t, r, "strict.go", own)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("the mutation check pushed: the remote's pre-receive hook ran")
	}
	if after := mutationRefs(t, remote); after != before {
		t.Errorf("the remote's refs changed under the check:\nbefore %q\nafter  %q", before, after)
	}
}

// TestMutationRefusesMoreThanOneMutant: one mutant is all a review runs, and
// the refusal happens before anything is read out of the worktree. The runner
// refuses it again for a hand-built task.json, and the launcher's own two
// boundaries — the strict decoder's validation and the published schema —
// refuse it before a job is ever started.
func TestMutationRefusesMoreThanOneMutant(t *testing.T) {
	one := `,"mutate":[{"file":"strict.go","test":"TestStrict"}]`
	two := `,"mutate":[{"file":"strict.go","test":"TestStrict"},{"file":"env_test.go","test":"TestChildEnvironment"}]`

	spec := mutationAsk(t, "")
	spec.Mutate = []task.Mutate{{File: "strict.go", Test: "TestStrict"}, {File: "env_test.go", Test: "TestChildEnvironment"}}
	r, _ := mutationReview(t, spec)
	err := r.runMutationCheck(context.Background())
	if !errors.Is(err, errMutationRefused) {
		t.Fatalf("err = %v, want the check's own refusal", err)
	}
	if len(r.reviewHeads[0].Mutation) != 0 {
		t.Errorf("mutation_checks = %+v, want none: a refused check records nothing", r.reviewHeads[0].Mutation)
	}

	if _, err := task.Parse([]byte(fmt.Sprintf(
		`{"version":1,"run_id":%q,"repo":"acme/app","ref":"main","review_prs":[7]%s}`, svcRunID, two))); err == nil {
		t.Error("task.Parse accepts two mutants, and one mutant is all a review runs")
	}
	schema := mutationTaskSchema(t)
	if err := schema.Validate(mutationInstance(t, one)); err != nil {
		t.Errorf("the published task schema refuses the ask this check exists for: %v", err)
	}
	if err := schema.Validate(mutationInstance(t, two)); err == nil {
		t.Error("the published task schema accepts two mutants, and one mutant is all a review runs")
	}
}

// TestMutationRevertsOnFailure: the revert is not the child's good ending. A
// test that never ran (the name matches nothing, so `go test` exits 0 having
// run nothing), one that will not finish, and an apply that failed partway
// through its own truncated write all leave the worktree holding the bytes the
// check read.
//
// The second arm's deadline is short enough to land while the child is still
// running: with an empty build cache the child compiles the package first, and
// either way TestSlow's own 30 seconds outlast it, which the elapsed bound
// below is what says.
func TestMutationRevertsOnFailure(t *testing.T) {
	unfenced := mutationUnfenced
	t.Run("a test that never ran", func(t *testing.T) {
		unfenced(t)
		r, _ := mutationReview(t, mutationAsk(t, `,"mutate":[{"file":"strict.go","test":"NoSuchTest"}]`))
		own := mutationSeed["strict.go"] + "\n// the runner's own copy of this file\n"
		mutationWrite(t, r, "strict.go", own)
		if err := r.runMutationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
		check := r.reviewHeads[0].Mutation[0]
		if check.Observed != "error" {
			t.Fatalf("observed = %q, want error: no test ran, and nothing was shown about the mutant", check.Observed)
		}
		if !check.Reverted {
			t.Error("a check whose child ran nothing still records the revert it made")
		}
		mutationRequireReverted(t, r, "strict.go", own)
	})
	t.Run("a test that will not finish", func(t *testing.T) {
		unfenced(t)
		deadline := mutationDeadline
		mutationDeadline = 2 * time.Second
		t.Cleanup(func() { mutationDeadline = deadline })
		r, _ := mutationReview(t, mutationAsk(t, `,"mutate":[{"file":"strict.go","test":"TestSlow"}]`))
		own := mutationSeed["strict.go"] + "\n// the runner's own copy of this file\n"
		mutationWrite(t, r, "strict.go", own)
		start := time.Now()
		if err := r.runMutationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
		check := r.reviewHeads[0].Mutation[0]
		if check.Observed != "timeout" {
			t.Fatalf("observed = %q, want timeout", check.Observed)
		}
		if elapsed := time.Since(start); elapsed > 20*time.Second {
			t.Fatalf("the check took %s: the deadline did not bound the child", elapsed)
		}
		mutationRequireReverted(t, r, "strict.go", own)
	})
	t.Run("an apply that fails", func(t *testing.T) {
		r, _ := mutationReview(t, mutationAsk(t, `,"mutate":[{"file":"strict.go","test":"TestStrict"}]`))
		own := mutationSeed["strict.go"] + "\n// the runner's own copy of this file\n"
		mutationWrite(t, r, "strict.go", own)
		// The apply opens with O_TRUNC and then writes, so a write that fails
		// leaves the file holding what it managed to put there — half a mutant,
		// which is the shape this test is about. No filesystem produces that on
		// request, so the write is the one the test replaces (mutationApply).
		apply := mutationApply
		mutationApply = func(rt *os.Root, name string, data []byte) error {
			f, err := rt.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := f.Write(data[:len(data)/2]); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			return errors.New("the write stopped halfway")
		}
		t.Cleanup(func() { mutationApply = apply })
		if err := r.runMutationCheck(context.Background()); err == nil {
			t.Fatal("an apply that never happened ended the check as if the mutant had been applied")
		}
		mutationRequireReverted(t, r, "strict.go", own)
		if got := len(r.reviewHeads[0].Mutation); got != 0 {
			t.Errorf("a check whose mutant was never applied recorded %d checks, want none: %+v", got, r.reviewHeads[0].Mutation)
		}
	})
}

// TestMutationAbsentUnlessAskedFor: a review that asked for no check runs
// none, records none, and says so in the artifact as [] rather than null — and
// an ask that reaches a run which is not the independent review job is
// refused rather than run, since the check is the review job's own business.
func TestMutationAbsentUnlessAskedFor(t *testing.T) {
	r, _ := mutationReview(t, mutationAsk(t, ""))
	if err := r.runMutationCheck(context.Background()); err != nil {
		t.Fatalf("a review that asked for no check ran something: %v", err)
	}
	if r.reviewHeads[0].Mutation != nil {
		t.Errorf("mutation_checks = %+v, want none", r.reviewHeads[0].Mutation)
	}
	b, err := json.Marshal(mutationChecksOf(r.reviewHeads[0]))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Errorf("mutation_checks marshals as %s, want []: a reader tells a review that ran none apart from one that ran one, and null is neither", b)
	}

	asked, _ := mutationReview(t, mutationAsk(t, `,"mutate":[{"file":"strict.go","test":"TestStrict"}]`))
	asked.d.Env = slices.DeleteFunc(asked.d.Env, func(kv string) bool {
		return strings.HasPrefix(kv, EnvReviewJob+"=")
	})
	asked.env = asked.d.Env
	if err := asked.runMutationCheck(context.Background()); !errors.Is(err, errMutationRefused) {
		t.Fatalf("err = %v, want a refusal: this run is not the independent review job", err)
	}
	if len(asked.reviewHeads[0].Mutation) != 0 {
		t.Errorf("mutation_checks = %+v, want none: the refusal happens before the worktree is touched", asked.reviewHeads[0].Mutation)
	}
}

// TestMutationSubprocessCarriesNoTokenInItsEnvironment: the child's
// environment is an allow-list and not the runner's. The fence does not reach
// the variables a child is handed — a namespace stops it reading the runner's
// /proc and reaching the network, and the runner still chooses what it passes
// down (U7's first caveat) — so what is handed down is env -i plus
// mutationEnvNames and mutationEnvFixed, pinned here from the child's own side:
// the head's own test writes the environment it was started with to the
// TMPDIR it was given.
//
// The fence's own argv is pinned here too. It is the one requirement these
// tests cannot exercise (see the file's comment), and it carries three things
// the design turns on: -n denies the child every network, which is why the
// AGENT never runs under it (the gateway is on the loopback); -p with
// --mount-proc leaves it no view of the runner's own /proc; and -r is what
// makes unshare work at all in a container with no capabilities (U7).
func TestMutationSubprocessCarriesNoTokenInItsEnvironment(t *testing.T) {
	want := []string{"unshare", "-rnpf", "--mount-proc"}
	if !slices.Equal(mutationFence, want) {
		t.Errorf("mutationFence = %v, want %v: the mutant's child is started with a network namespace, a user namespace and a /proc of its own, or it is not started at all", mutationFence, want)
	}
	mutationUnfenced(t)
	r, _ := mutationReview(t, mutationAsk(t, `,"mutate":[{"file":"strict.go","test":"TestChildEnvironment"}]`))
	if err := r.runMutationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if check := r.reviewHeads[0].Mutation[0]; check.Observed != "pass" {
		t.Fatalf("observed = %q, want pass: the head's own test runs and the mutant is in another file of its package", check.Observed)
	}
	witness := filepath.Join(envLookup(r.env, "TMPDIR"), "child-env.txt")
	data, err := os.ReadFile(witness)
	if err != nil {
		t.Fatalf("the child's own test did not run, so nothing is known about its environment: %v", err)
	}
	held := map[string]string{}
	for _, kv := range strings.Split(string(data), "\n") {
		if name, value, ok := strings.Cut(kv, "="); ok && name != "" {
			held[name] = value
		}
	}
	// PWD is the one name the child's own toolchain adds: the `go` command
	// sets it for the test binary it starts, out of the child's own working
	// directory — the worktree — and never out of the runner's environment.
	allowed := slices.Concat(mutationEnvNames, []string{"GOPROXY", "GOTOOLCHAIN", "PWD"})
	for name, value := range held {
		if !slices.Contains(allowed, name) {
			t.Errorf("the child holds %s=%q, which the allow-list does not name: its environment is the runner's rather than env -i plus this list", name, value)
		}
		if value == mutationMarker {
			t.Errorf("the child holds the runner's own value for %s: the fence does not reach the variables a child is handed", name)
		}
	}
	for _, name := range []string{"HOME", "PATH", "TMPDIR", "GOCACHE"} {
		if _, ok := held[name]; !ok {
			t.Errorf("the child does not hold %s, and the toolchain resolves through it", name)
		}
	}
	if held["GOPROXY"] != "off" || held["GOTOOLCHAIN"] != "local" {
		t.Errorf("GOPROXY=%q GOTOOLCHAIN=%q, want off and local: a fence that leaves the child no network turns a missing module into a fast, legible failure instead of a download that hangs", held["GOPROXY"], held["GOTOOLCHAIN"])
	}
}

// TestMutationObservedVocabularyIsClosed: the four words §3 fixes are the
// whole vocabulary a coordinator routes on, and no fifth is written or read.
// The two arms that look like verdicts are the reason the vocabulary is a
// function of the child's ending rather than of its exit status: `go test`
// exits 1 for a build failure as well as for a failing test, and exits 0 when
// -run matched nothing at all — which recorded as the mutant surviving would
// report the one finding a coordinator acts on when in fact no test ran.
//
// The build-failure arm keeps its word whatever else the child's output holds,
// because a compile error in ANY test file of the package stops the test binary
// from being produced, so the named test cannot also have run: the two cases
// below are the measured output of a package whose other test file does not
// compile (no `--- FAIL` line is printed at all) and, beside it, the words a
// head that wanted a verdict out of this check could print instead.
func TestMutationObservedVocabularyIsClosed(t *testing.T) {
	four := []string{"pass", "fail", "error", "timeout"}
	if !slices.Equal(reviewObserved, four) {
		t.Fatalf("reviewObserved = %v, want %v", reviewObserved, four)
	}
	cases := []struct {
		why      string
		exit     int
		output   string
		timedOut bool
		want     string
	}{
		{"a test that ran and passed", 0, "ok  \texample.invalid/head\t0.002s\n", false, "pass"},
		{"a test that ran and failed", 1, "--- FAIL: TestStrict (0.00s)\nFAIL\n", false, "fail"},
		{"a mutant that does not compile", 1, "# example.invalid/head\nstrict.go:3:13: undefined: x\nFAIL\texample.invalid/head [build failed]\n", false, "error"},
		{"a test name that matched nothing", 0, "testing: warning: no tests to run\nok  \texample.invalid/head\t0.002s [no tests to run]\n", false, "error"},
		{"a package with no test files", 0, "?   \texample.invalid/head\t[no test files]\n", false, "error"},
		{"a child this code's deadline killed", 0, "", true, "timeout"},
		{"a go tool that failed outright", 2, "go: cannot find main module\n", false, "error"},
		{"another test file of the package does not compile, as go test prints it", 1, "# example.invalid/mix [example.invalid/mix.test]\n./broken_test.go:6:2: undefined: undefinedSymbol\nFAIL\texample.invalid/mix [build failed]\nFAIL\n", false, "error"},
		{"a head printing both a failure and a build failure", 1, "--- FAIL: TestStrict (0.00s)\nFAIL\texample.invalid/head [build failed]\n", false, "error"},
	}
	for _, c := range cases {
		got := mutationObservedOf(c.exit, []byte(c.output), c.timedOut)
		if got != c.want {
			t.Errorf("%s: observed = %q, want %q", c.why, got, c.want)
		}
		if !slices.Contains(four, got) {
			t.Errorf("%s: observed = %q, which is not one of the four words", c.why, got)
		}
	}
	for _, word := range []string{"pass", "fail", "error", "timeout", "flaky", "PASS", "skipped", ""} {
		_, err := recordMutationCheck(ReviewMutationCheck{File: "strict.go", Test: "TestStrict", Observed: word})
		if in := slices.Contains(four, word); in && err != nil {
			t.Errorf("recordMutationCheck refused %q, one of the four: %v", word, err)
		} else if !in && err == nil {
			t.Errorf("recordMutationCheck accepted %q, and the four words are the whole vocabulary", word)
		}
	}
}

// TestMutationDenyRulesBelongToTheReviewJob: the five rules D891(a) names are
// the independent review job's, and they reach no other stage's settings. A
// review STAGE is not the review job — a coding run's own first-line and senior
// rounds are stages of the repository's own run, and `fugaro run --pr N
// --recipe review-only` is the same run reviewing someone else's head — so
// denying them a shell, an edit or a write for the whole stage would take the
// run's own work away and protect nothing: the one whose stage reads a head
// that is not its own is the job.
//
// This is the launch, not the list: it reads what writeManagedSettings writes
// for each kind of run, which is where the two were conflated. The review job's
// arm is built here rather than in agentenv_test.go because the job is told
// apart by its own environment and the file's harness cannot reach a run's
// gateway.
func TestMutationDenyRulesBelongToTheReviewJob(t *testing.T) {
	cases := []struct {
		name   string
		stage  string
		review bool
		job    bool
		want   bool
	}{
		{"a coding run's implement stage", "implement", false, false, false},
		{"a coding run's own first-line review", "review_first", false, false, false},
		{"a coding run's own senior review", "review", false, false, false},
		{"fugaro run --pr N --recipe review-only", "review", true, false, false},
		{"the independent review job", "review", true, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "managed-settings.json")
			r := svcRun(t, config.AuthCascade, func(r *run) {
				r.gwAgent = &agent.Gateway{BaseURL: "http://127.0.0.1:1", Token: "gateway-token-for-tests"}
				r.review = c.review
				r.d.Env = nil
				if c.job {
					r.d.Env = []string{EnvReviewJob + "=1"}
				}
				r.d.ManagedSettingsPath = path
			})
			if err := r.writeManagedSettings(c.stage, nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Permissions struct{ Deny []string }
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			for _, rule := range agent.ReviewJobDenyRules() {
				if have := slices.Contains(got.Permissions.Deny, rule); have != c.want {
					t.Errorf("%s: settings deny %q = %v, want %v: %v", c.name, rule, have, c.want, got.Permissions.Deny)
				}
			}
			if !c.want && config.IsReviewStage(c.stage) && !slices.Contains(got.Permissions.Deny, "Read(**/*.png)") {
				t.Errorf("%s: the stage lost its binary reads too: %v", c.name, got.Permissions.Deny)
			}
		})
	}
}

// mutationPlantSeed is the reviewed head the ordering test reviews: the seed
// module above, plus one test of the head's own that plants what a head which
// wanted to reach the reviewer would plant — a core.fsmonitor command in the
// checkout's own .git/config and a .claude settings file — into the worktree
// the runner holds while the review reads it.
func mutationPlantSeed() map[string]string {
	seed := maps.Clone(mutationSeed)
	seed["plant_test.go"] = `package head

import (
	"os"
	"testing"
)

func TestPlant(t *testing.T) {
	cfg, err := os.ReadFile(".git/config")
	if err != nil {
		t.Fatal(err)
	}
	planted := string(cfg) + "\n[core]\n\tfsmonitor = /bin/sh -c :\n"
	if err := os.WriteFile(".git/config", []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	settings := "{\"permissions\": {\"deny\": []}, \"hooks\": {\"SessionStart\": [{\"hooks\": [{\"type\": \"command\", \"command\": \"true\"}]}]}}\n"
	if err := os.WriteFile(".claude/settings.json", []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}
`
	return seed
}

// mutationPlantReads is what the head's own test leaves where the reviewer's
// session would find it, read the way that session reads it: the checkout's
// git configuration, and the settings file of the tree the stage runs in.
func mutationPlantReads(dir string) []string {
	var seen []string
	if cfg, err := os.ReadFile(filepath.Join(dir, ".git", "config")); err == nil && strings.Contains(string(cfg), "fsmonitor") {
		seen = append(seen, "the checkout's .git/config carries an fsmonitor command")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err == nil {
		seen = append(seen, "the worktree holds a .claude/settings.json")
	}
	return seen
}

// mutationStageAgent is the review stage as the ordering test meets it: it
// reads the two places a head's planted file would be read from and answers
// what it read, as a reviewer asked to judge a head would.
type mutationStageAgent struct {
	t      *testing.T
	answer agent.Result
	seen   []string
}

func (a *mutationStageAgent) Run(_ context.Context, req agent.Request) (agent.Result, error) {
	a.seen = mutationPlantReads(req.Dir)
	if len(a.seen) != 0 {
		// What a reviewer that found the head's own test writing into the
		// worktree would answer, and the answer the ordering exists to keep
		// off the artifact.
		return reviewResult(a.t, reviewAnswerOf("reject", strings.Join(a.seen, "; "))), nil
	}
	return a.answer, nil
}

// mutationOrderingRun is the review job's run as reviewMode meets it: the
// review job's own environment, one resolved head checked out into a real
// clone, a state directory and a store to write the artifact into, and the
// run's own clock and budget. It is not the whole run — reviewMode is the one
// stage it drives — so the fields bootstrap would have filled are the ones
// reviewMode's own path reads.
func mutationOrderingRun(t *testing.T, spec *task.Spec, stage agent.Agent) (*run, string) {
	t.Helper()
	testutil.IsolateGit(t)
	bin := mutationGoBin(t)
	remote := testutil.NewRemote(t, mutationPlantSeed())
	work := filepath.Join(t.TempDir(), "work")
	repo, err := gitops.OpenOrClone(context.Background(), work, remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	svc := newReviewService(t)
	setReviewSigner(t, svc.signer)
	provider := &fake.Provider{Repo: "acme/app", Remote: remote}
	managed := t.TempDir()
	state, tmp, cache := t.TempDir(), t.TempDir(), t.TempDir()
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Now = func() time.Time { return now }
		r.d.Store = runstore.Open(bucket, svcSlug, svcRunID)
		r.d.StateDir, r.d.WorkDir = state, work
		r.d.ManagedSettingsPath = filepath.Join(managed, "managed-settings.json")
		r.d.Agent = stage
		r.d.Env = []string{
			"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
			"HOME=" + state,
			"TMPDIR=" + tmp,
			"GOCACHE=" + cache,
			EnvReviewJob + "=1",
		}
		r.env = r.d.Env
		r.stageN = map[string]int{}
		r.haltDone = make(chan struct{})
		r.budget = Budget{Start: now, Total: time.Hour, Reserve: time.Minute, Stage: time.Minute, Now: func() time.Time { return now }}
		r.setLevelModel(policy.LevelL2, "claude-opus-5-5")
		r.levels = config.Levels{L2: config.ResolvedLevel{On: true, Model: "claude-opus-5-5", Effort: "high"}}
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Repo: "acme/app", Stage: "review"}
		r.d.OpenProvider = gitprov.Static(provider)
		r.provider, r.repo, r.spec, r.review = provider, repo, spec, true
		r.reviewHeads = []reviewHead{{Number: 7, SHA: testutil.Git(t, work, "rev-parse", "HEAD"), Base: "main"}}
	})
	return r, remote
}

// TestMutationRunsAfterTheReviewStage: D891(a)'s ordering, as the owner ruled
// it (2026-11-02) — the mutation check runs AFTER the review stage, and only
// the artifact write follows it.
//
// The head's own test plants a core.fsmonitor command in the checkout's
// .git/config and a .claude settings file into the worktree, and the mutant
// runs it there: those two are exactly what a reviewer's session would read —
// git resolves core.fsmonitor out of .git/config for every command the runner
// makes after the check, and Claude Code loads a settings file out of the
// directory it is started in. With the check before the stage, both are in
// front of the reviewer for the whole stage; with the check after it, the
// stage is over before either exists.
func TestMutationRunsAfterTheReviewStage(t *testing.T) {
	mutationUnfenced(t)
	stage := &mutationStageAgent{t: t, answer: reviewResult(t, reviewAnswerOf("ship", "the change is one boolean and one test"))}
	r, _ := mutationOrderingRun(t, mutationAsk(t, `,"mutate":[{"file":"strict.go","test":"TestPlant"}]`), stage)

	r.reviewMode(context.Background())

	if len(stage.seen) != 0 {
		t.Errorf("the review stage read what the head's own test planted: %s", strings.Join(stage.seen, "; "))
	}
	// The plant really happened, and it happened in the worktree the runner
	// read: without this the check above could pass because the mutant never
	// ran at all.
	if got := mutationPlantReads(r.d.WorkDir); len(got) != 2 {
		t.Fatalf("the head's own test planted %s, want both files it writes", strings.Join(got, "; "))
	}
	if checks := r.reviewHeads[0].Mutation; len(checks) != 1 || checks[0].Test != "TestPlant" {
		t.Fatalf("mutation_checks = %+v, want the one check this run asked for", checks)
	}
	// The artifact is what the run's own writer made out of the head the
	// check recorded against, so a check that ran after it would be missing
	// from the bytes a coordinator verifies.
	var artifact ReviewArtifact
	if err := json.Unmarshal(reviewObject(t, r, ReviewArtifactName), &artifact); err != nil {
		t.Fatal(err)
	}
	if len(artifact.MutationChecks) != 1 || artifact.MutationChecks[0].File != "strict.go" {
		t.Fatalf("the artifact's mutation_checks = %+v, want the check this run ran before writing it", artifact.MutationChecks)
	}
	if artifact.Verdict != "ship" {
		t.Errorf("the artifact's verdict = %q, want the answer the reviewer gave: a stage that found the head's plant answers what it read", artifact.Verdict)
	}
	if r.rec.Status != runstore.StatusSucceeded || r.rec.Outcome != runstore.OutcomeReviewed {
		t.Errorf("the run ended %s/%s (%s), want it reviewed: nothing the check left behind changed the meeting's ending", r.rec.Status, r.rec.Outcome, r.rec.Reason)
	}
	if len(r.rec.Reviews) != 1 || r.rec.Reviews[0].Verdict != "ship" {
		t.Errorf("the record's reviews = %+v, want the reviewer's verdict", r.rec.Reviews)
	}
}
