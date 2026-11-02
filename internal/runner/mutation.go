package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The review job's mutation check (D885, D891(a), spec §2): one mutant, one
// named test, applied and reverted by the runner itself in the worktree the
// reviewer reads, run as a child of the runner's own with a network namespace
// and a /proc of its own around it and an environment that is an allow-list
// rather than the runner's.
//
// The mutant is applied, run and reverted here and never by the agent's own
// tools: Bash, Edit, Write, NotebookEdit and Read(/proc/**) are denied in the
// review stage's managed settings for the rest of the stage (agent.DenyRules,
// D891(a)), which is what leaves a reviewer reading a hostile pull request
// with no shell of its own to run anything in.
//
// This path reaches no push: nothing here calls git at all. The mutated file
// is read and rewritten through an os.Root on the worktree, so a path that
// leaves it — a symlink the head ships, an absolute path, a ".." — is refused
// rather than followed, and what the revert writes back is the bytes this code
// read into memory rather than what `git checkout --` would produce: a revert
// through git would let the reviewed head's own .gitattributes filters, its
// core.fsmonitor, a running fsmonitor daemon and its hooks choose what the
// revert runs (spec §2).

var (
	// errMutationRefused is the check's own refusal: the task asked for
	// something this code will not do, and the review ends with that as its
	// reason rather than running a check that is not the one asked for. A
	// check that RAN and ended badly is not a refusal — it is recorded with
	// observed "error" and the review carries on, which is the distinction
	// reviewartifact.go's reviewObserved turns on.
	errMutationRefused = errors.New("mutation check refused")

	// errMutationTimeout is the cause the child's deadline carries, so a
	// child this code killed is told apart from one that died because the
	// run itself was cancelled or halted under it.
	errMutationTimeout = errors.New("mutation check timed out")
)

// mutationFence is what the mutant's child starts under: a user, PID and
// network namespace with a /proc of its own. It is the mechanism the owner's
// standalone run measured in the deployed image (U7, spec §8's check 125 step
// 6), and all four of its parts are load-bearing:
//
//   - -n denies the child every network, the loopback and the instance
//     metadata server included. That is why only this child runs under it and
//     the AGENT never does: Claude Code's model calls go through the loopback
//     gateway, which -n would take away (U7's third caveat).
//   - -r maps this container's unprivileged user to root inside the namespace,
//     which is what makes unshare work at all where the job has no
//     capabilities (the probe's own finding: plain `unshare -n` is refused).
//   - -p --mount-proc gives the child a /proc of its own, so the runner's
//     environment is not readable through /proc/1/environ.
//
// --kill-child (-k is not a spelling util-linux gives it: the deployed image's
// unshare answers "invalid option -- 'k'") would make the deadline's kill
// reach the namespace's own init, which is the test itself: without it the
// orphan of a killed `unshare` runs on until it finishes, and the deadline is
// a bound on how long this code WAITS (mutationWaitDelay closes the pipes) and
// not on the orphan's own life. It is not part of what the probe measured, so
// it is not added here on the strength of an assumption — the mechanism is the
// argv the owner measured and nothing more.
//
// A fence that will not start is never skipped: the check is refused instead,
// because a mutant run unfenced is the exposure D891(a) exists to close.
//
// A var rather than a const so that a test can run a child without the fence:
// whether unshare is usable is a property of the execution environment (the
// probe measured it in Cloud Run gen 2; another container makes no such
// promise), and what the tests here pin is the check's own behaviour — and,
// separately, this argv.
var mutationFence = []string{"unshare", "-rnpf", "--mount-proc"}

// mutationDeadline bounds one mutant's child. It is generous for one package's
// test — the child compiles that package first, and a head's own tests can be
// slow — and it is a bound the artifact reports as "timeout" rather than a
// limit that silently ends the check.
var mutationDeadline = 5 * time.Minute

// mutationWaitDelay breaks the wait when the killed child's own children still
// hold the output pipe open. `go test` spawns the test binary, and a SIGKILL
// to the parent is not something it can forward; without this the deadline
// would be a bound on nothing, since Wait would sit on the pipe the orphan
// still holds (the same leaked-descendant class gitWaitDelay guards
// elsewhere).
const mutationWaitDelay = 5 * time.Second

// maxMutantBytes bounds the file the check reads and rewrites: the mutant is
// spliced into it in memory, so a file past this is refused rather than read.
// It is not a bound on the *head*, which is this job's business to check out
// whole — only on the one file the check will hold twice.
const maxMutantBytes = 1 << 20

// mutationOutputCap bounds what is kept of the child's output: the two facts
// read out of it (no test ran, a build failure) are in its first lines, and a
// test that prints a gigabyte must not be this process's memory.
const mutationOutputCap = 64 << 10

// mutationEnvNames is the whole of the child's environment, with
// mutationEnvFixed: these names, each at the value the runner's own
// environment holds for it, and nothing else. It is `env -i` plus an
// allow-list because the fence does not reach the variables a child is handed
// (U7's first caveat): a namespace stops the child reading the runner's
// /proc and reaching the network, and it does not stop the runner passing a
// token down. What is named here is the toolchain's own locations —
// reviewToolchainVars' set, without which `go` does not resolve under the
// review home — plus HOME, PATH and TMPDIR.
//
// PWD is deliberately not one of them: the `go` command sets it for the test
// binary it starts, out of that binary's own working directory, and a value
// this code passed would be the one thing the child's toolchain then corrects.
var mutationEnvNames = []string{
	"HOME", "PATH", "TMPDIR",
	"MISE_DATA_DIR", "MISE_CONFIG_DIR", "MISE_GLOBAL_CONFIG_FILE", "MISE_CACHE_DIR",
	"GOCACHE", "GOPATH", "GOMODCACHE",
}

// mutationEnvFixed are the two names the runner decides rather than inherits.
// The fence leaves the child no network, so a module the image's own cache
// does not hold is a fast, legible failure rather than a download that hangs
// until the deadline; and a head whose go.mod asks for a toolchain this image
// does not have fails at once instead of trying to fetch one.
var mutationEnvFixed = []string{"GOPROXY=off", "GOTOOLCHAIN=local"}

// mutationGoFiles is the shape of the file the check will mutate. The check
// runs `go test`, and its mutant is spliced in with go/parser, so the file it
// edits is Go source and the launcher's ask says so before the run.
var mutationGoFiles = regexp.MustCompile(`^[^/]+(?:/[^/]+)*\.go$`)

// mutationChildEnv is the environment the mutant's child starts with: the
// names mutationEnvNames holds, at the values env holds for them, plus
// mutationEnvFixed. env is the runner's own environment, and nothing in it
// that is not named here reaches the child.
func mutationChildEnv(env []string) []string {
	held := make(map[string]string, len(env))
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			held[name] = value
		}
	}
	out := make([]string, 0, len(mutationEnvNames)+len(mutationEnvFixed))
	for _, name := range mutationEnvNames {
		if value, ok := held[name]; ok {
			out = append(out, name+"="+value)
		}
	}
	return slices.Concat(out, mutationEnvFixed)
}

// runMutationChild runs one mutant's test: the fence, then `go test` for the
// named test in the mutated file's own package, in dir, with the child's
// environment and a deadline of its own. pkg is a package path relative to dir
// and test is a test name, quoted so that it can only ever match the test it
// names rather than a pattern.
func runMutationChild(ctx context.Context, env []string, dir, pkg, test string) mutationChildResult {
	argv := slices.Concat(slices.Clone(mutationFence),
		[]string{"go", "test", "-count=1", "-run", "^" + regexp.QuoteMeta(test) + "$", pkg})
	ctx, cancel := context.WithTimeoutCause(ctx, mutationDeadline, errMutationTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = mutationChildEnv(env)
	cmd.WaitDelay = mutationWaitDelay
	var out mutationOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exit *exec.ExitError
	result := mutationChildResult{
		output:   out.Bytes(),
		timedOut: errors.Is(context.Cause(ctx), errMutationTimeout),
	}
	switch {
	case err == nil:
		result.exit = 0
	case errors.As(err, &exit):
		result.exit = exit.ExitCode()
	default:
		// The child never started — no `go`, no fence — which is the same
		// answer to the reviewer as a build failure: nothing ran that could
		// have killed the mutant.
		result.exit = -1
		result.spawnErr = err
	}
	return result
}

// mutationChildResult is how one mutant's child ended: the exit status (or -1
// for a child that never started), what it printed, and whether the check's
// own deadline killed it.
type mutationChildResult struct {
	exit     int
	output   []byte
	timedOut bool
	spawnErr error
}

// mutationOutput keeps the first mutationOutputCap bytes written to it and
// drops the rest, reporting every byte as written so that the child is not
// told its output was short.
type mutationOutput struct{ buf bytes.Buffer }

func (w *mutationOutput) Write(p []byte) (int, error) {
	n := len(p)
	if room := mutationOutputCap - w.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		w.buf.Write(p)
	}
	return n, nil
}

func (w *mutationOutput) Bytes() []byte { return w.buf.Bytes() }

// mutationObservedOf is the artifact's four words as one child's ending:
// "timeout" for the bounded run that expired, "pass"/"fail" for a test that ran
// to a verdict, and "error" for everything else.
//
// The two "error" cases that look like verdicts are why this is a function
// rather than a mapping of exit codes. `go test` exits 1 for a build failure
// as well as for a failing test, and a mutant that does not compile has shown
// nothing about the test that was supposed to kill it; and it exits 0 when
// -run matched no test at all, which would otherwise be recorded as the mutant
// SURVIVING — the finding a coordinator reports — when in fact no test ran.
//
// A build failure is the package's, whole, and that is what makes "error" the
// answer for it whatever else the child's output carries: `go test` compiles
// every test file of the package before it runs one, so a compile error
// anywhere in it (measured: another test file with an undefined symbol, and a
// vet diagnostic, both print `FAIL <pkg> [build failed]` and no `--- FAIL`
// line at all) means no test binary was produced and the named test never ran.
// A word is also read out of the child's own text, which a head can print
// either phrase in, so the phrase that is not a verdict is the one to fall
// back to: recorded as "error" a reader sees that the check showed nothing,
// where "fail" is the finding a coordinator acts on.
func mutationObservedOf(exit int, output []byte, timedOut bool) string {
	text := string(output)
	switch {
	case timedOut:
		return "timeout"
	case exit == 0 && (strings.Contains(text, "no tests to run") || strings.Contains(text, "[no test files]")):
		return "error"
	case exit == 0:
		return "pass"
	case exit == 1 && (strings.Contains(text, "[build failed]") || strings.Contains(text, "[setup failed]")):
		return "error"
	case exit == 1:
		return "fail"
	}
	return "error"
}

// mutationChecksOf is the artifact's mutation_checks for one reviewed head:
// [] and never null for a review that ran none, which is what the schema
// requires and what a reader tells apart from a review that ran one.
func mutationChecksOf(h reviewHead) []ReviewMutationCheck {
	if len(h.Mutation) == 0 {
		return []ReviewMutationCheck{}
	}
	return h.Mutation
}

// recordMutationCheck is the one place a check is built, and it refuses a word
// outside the four: a reader routes on observed without parsing prose, so a
// fifth word is refused where it is written and not only where the artifact is
// read back (DecodeReviewArtifact makes the same refusal on the way in).
func recordMutationCheck(c ReviewMutationCheck) (ReviewMutationCheck, error) {
	if !slices.Contains(reviewObserved, c.Observed) {
		return c, fmt.Errorf("%w: mutation check observed %q, and the four words are %s", ErrReviewArtifact, c.Observed, strings.Join(reviewObserved, ", "))
	}
	return c, nil
}

// mutationFlip is the one mutant this check makes: the first boolean literal
// in the file, flipped. It is mechanical, it is the same edit for every
// language it is written in, and it is the shape a test is most likely to be
// reading — the mutation a reviewer would make by hand to see whether a test
// holds the behaviour it names. A file with no boolean literal, or one that
// will not parse, is no mutant at all: the caller records that as "error"
// rather than inventing an edit.
//
// Parsing rather than searching the bytes is what keeps a `true` inside a
// string or a comment from becoming the mutant: a mutation that changed
// nothing would be recorded as the test failing to kill it.
func mutationFlip(name string, src []byte) (mutated []byte, applied string, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, "", fmt.Errorf("the file is not Go source this check can parse: %v", err)
	}
	at, word := token.NoPos, ""
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || (id.Name != "true" && id.Name != "false") {
			return true
		}
		if at == token.NoPos || id.Pos() < at {
			at, word = id.Pos(), id.Name
		}
		return true
	})
	if at == token.NoPos {
		return nil, "", errors.New("the file holds no boolean literal to flip")
	}
	flipped := "true"
	if word == "true" {
		flipped = "false"
	}
	off := fset.Position(at).Offset
	out := make([]byte, 0, len(src)+1)
	out = append(out, src[:off]...)
	out = append(out, flipped...)
	out = append(out, src[off+len(word):]...)
	pos := fset.Position(at)
	return out, fmt.Sprintf("flipped the boolean literal on line %d: %s -> %s", pos.Line, word, flipped), nil
}

// mutationPackage is the package the named test runs in: the mutated file's
// own directory, since a Go test lives in its package. The check runs that one
// package rather than the whole repository — `go test ./...` would compile
// every package of a head this job has never seen, which is a cost the ask
// ("one named test") does not justify.
func mutationPackage(file string) string {
	dir := path.Dir(file)
	if dir == "." {
		return "."
	}
	return "./" + dir
}

// mutationFile is the file the ask names, confined to the worktree: a relative
// slash-separated path to Go source that stays inside it. The refusal is the
// spec §2 rule — no path is followed out of the worktree — applied before
// anything is opened.
func mutationFile(asked string) (string, error) {
	asked = strings.TrimSpace(asked)
	if asked == "" {
		return "", fmt.Errorf("%w: the mutation check names no file", errMutationRefused)
	}
	if filepath.IsAbs(asked) || strings.HasPrefix(asked, "/") {
		return "", fmt.Errorf("%w: %s is an absolute path, and the mutant is one file inside the worktree", errMutationRefused, asked)
	}
	if file := path.Clean(filepath.ToSlash(asked)); file != filepath.ToSlash(asked) || file == ".." || strings.HasPrefix(file, "../") {
		return "", fmt.Errorf("%w: %s is not a clean path inside the worktree", errMutationRefused, asked)
	}
	if !mutationGoFiles.MatchString(filepath.ToSlash(asked)) {
		return "", fmt.Errorf("%w: %s is not Go source, and the mutation check runs go test (one named test, one mutant)", errMutationRefused, asked)
	}
	return filepath.FromSlash(asked), nil
}

// readMutationFile reads one file inside rt, as a regular file of at most
// maxMutantBytes: the Lstat refuses a symlink or anything else that is not a
// regular file, and the handle's own fstat refuses one swapped in behind it
// (session.go's readSessionFile makes the same pair for the same reason).
func readMutationFile(rt *os.Root, name string) ([]byte, error) {
	li, err := rt.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !li.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (%s)", name, li.Mode().Type())
	}
	f, err := openRootNoFollow(rt, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || !os.SameFile(li, fi) {
		return nil, fmt.Errorf("%s changed while it was opened", name)
	}
	if fi.Size() > maxMutantBytes {
		return nil, fmt.Errorf("%s is %d bytes, past the %d a mutant is spliced into", name, fi.Size(), maxMutantBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxMutantBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMutantBytes {
		return nil, fmt.Errorf("%s is past the %d bytes a mutant is spliced into", name, maxMutantBytes)
	}
	return data, nil
}

// writeMutationFile rewrites one file inside rt with data, refusing a path
// that is no longer the regular file the caller read: O_NOFOLLOW where the
// platform has it, and the same Lstat the read made. It truncates in place
// rather than replacing the file, so anything else holding it open reads the
// bytes back rather than a removed inode.
func writeMutationFile(rt *os.Root, name string, data []byte) error {
	li, err := rt.Lstat(name)
	if err != nil {
		return err
	}
	if !li.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (%s)", name, li.Mode().Type())
	}
	f, err := rt.OpenFile(name, os.O_WRONLY|os.O_TRUNC|oNoFollow, li.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// runMutationCheck runs the mutation check the task asked for
// (fugaro review --mutate <file:test>, D885) against the reviewed head's
// worktree, and records what it observed on that head — which is where the
// artifact's mutation_checks reads it from. No ask: nothing runs at all.
//
// It runs at most one check, only in a review job, and only for a run that
// resolved exactly one head: the artifact is the only place a check is
// recorded and the review job is the only run that writes one, and one
// worktree holds the whole review only when one pull request was resolved.
//
// A check that ran is not an opinion: the review carries on whatever it
// observed, and the artifact is where a reader weighs it. What returns an
// error is a check that could not be run as asked (errMutationRefused) and a
// mutant that could not be put back — the second of which must end the run,
// because everything after it would read a worktree holding the mutant.
func (r *run) runMutationCheck(ctx context.Context) error {
	if r.spec == nil || len(r.spec.Mutate) == 0 {
		return nil
	}
	if !r.reviewJob() {
		return fmt.Errorf("%w: a mutation check is the independent review job's, and this run is not one", errMutationRefused)
	}
	if len(r.spec.Mutate) > 1 {
		return fmt.Errorf("%w: the task asks for %d mutation checks, and one mutant is all a review runs", errMutationRefused, len(r.spec.Mutate))
	}
	if len(r.reviewHeads) != 1 {
		return fmt.Errorf("%w: the run resolved %d pull requests, and a mutation check needs the one worktree a single head is checked out into", errMutationRefused, len(r.reviewHeads))
	}
	if len(mutationFence) > 0 {
		if _, err := exec.LookPath(mutationFence[0]); err != nil {
			return fmt.Errorf("%w: this container has no %s to fence the mutant with, and a mutant never runs unfenced (U7): %v", errMutationRefused, mutationFence[0], err)
		}
	}
	file, err := mutationFile(r.spec.Mutate[0].File)
	if err != nil {
		return err
	}
	work, err := filepath.Abs(r.d.WorkDir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(work)
	if err != nil {
		return fmt.Errorf("the reviewed head's worktree: %w", err)
	}
	defer root.Close()
	before, err := readMutationFile(root, file)
	if err != nil {
		return fmt.Errorf("%w: %v", errMutationRefused, r.redact(err.Error()))
	}

	check := ReviewMutationCheck{File: filepath.ToSlash(file), Test: r.spec.Mutate[0].Test, Reverted: true}
	mutated, applied, err := mutationFlip(file, before)
	if err != nil {
		check.Observed = "error"
		r.d.Log.Warn("the mutation check made no mutant", "file", check.File, "err", r.redact(err.Error()))
		return r.recordMutationCheck(check, err)
	}
	check.Applied = applied
	if err := writeMutationFile(root, file, mutated); err != nil {
		return fmt.Errorf("the mutant could not be applied to %s: %w", check.File, err)
	}
	result := runMutationChild(ctx, r.env, work, mutationPackage(check.File), check.Test)
	// The revert happens on every path from here, before anything reads the
	// worktree again: a check that failed, timed out or never started still
	// leaves the head's own bytes behind it.
	if err := writeMutationFile(root, file, before); err != nil {
		return fmt.Errorf("the mutant of %s could not be reverted, and the worktree still holds it: %w", check.File, err)
	}
	back, err := readMutationFile(root, file)
	if err != nil {
		return fmt.Errorf("the mutant of %s could not be read back after the revert: %w", check.File, err)
	}
	if !bytes.Equal(back, before) {
		return fmt.Errorf("the mutant of %s was not reverted: %s holds %d bytes where the check read %d", check.File, check.File, len(back), len(before))
	}
	check.Observed = mutationObservedOf(result.exit, result.output, result.timedOut)
	if result.spawnErr != nil {
		r.d.Log.Warn("the mutation check's child never started", "file", check.File, "err", r.redact(result.spawnErr.Error()))
	}
	return r.recordMutationCheck(check, nil)
}

// recordMutationCheck records one check on the head it ran against and stores
// nothing else: the artifact's writer reads the head, so the evidence travels
// with the head it is evidence about. why is the reason a check that made no
// mutant at all is worth a line in the log — it is "" for a check that ran.
func (r *run) recordMutationCheck(check ReviewMutationCheck, why error) error {
	recorded, err := recordMutationCheck(check)
	if err != nil {
		return err
	}
	r.reviewHeads[0].Mutation = []ReviewMutationCheck{recorded}
	if why != nil {
		r.d.Log.Warn("the mutation check ran to no verdict", "file", check.File, "test", check.Test, "err", r.redact(why.Error()))
	}
	return nil
}
