package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// RejectKind names a push refusal that retrying can't cure: the host will
// refuse the same commits every time.
type RejectKind string

const (
	// RejectWorkflows: GitHub refuses a GitHub App's push that creates or
	// updates .github/workflows/* without the App's `workflows` permission.
	// Fugaro never has it, on purpose: a run able to edit CI could change
	// the merge gates or reach secrets.
	RejectWorkflows       RejectKind = "workflows"
	RejectProtectedBranch RejectKind = "protected_branch"
	RejectLargeFile       RejectKind = "large_file"
	RejectSecretScanning  RejectKind = "secret_scanning"
	// RejectRepoRules is GitHub's generic "Repository rule violations
	// found" (GH013): signed commits, linear history, branch naming and so
	// on. Only a message that also speaks of secrets is RejectSecretScanning.
	RejectRepoRules RejectKind = "repository_rules"
)

// PushRejected is a push the host refused for a permanent reason. It wraps
// the git error it was read from.
type PushRejected struct {
	Kind RejectKind
	// Files are the workflow files the host named, when it named them.
	Files []string
	err   error
}

// Error is the git error's own text, so a caller that doesn't act on Kind
// reports what it always did.
func (e *PushRejected) Error() string {
	if e.err == nil {
		return "push rejected (" + string(e.Kind) + ")"
	}
	return e.err.Error()
}
func (e *PushRejected) Unwrap() error { return e.err }

var workflowFileRE = regexp.MustCompile("workflow `([^`]+)`")

// ClassifyPush returns the permanent rejection err reports, or nil for any
// other error (a network failure, a lease that lost): those stay plain.
func ClassifyPush(err error) *PushRejected {
	if err == nil {
		return nil
	}
	var already *PushRejected
	if errors.As(err, &already) {
		return already
	}
	// Only what the host said counts: git's own text carries the remote's
	// URL and the refspec, which can hold any word (a repository called
	// "secrets-api") and say nothing about why a push failed.
	msg := remoteText(err.Error())
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "without `workflows` permission"):
		rej := &PushRejected{Kind: RejectWorkflows, err: err}
		if m := workflowFileRE.FindStringSubmatch(msg); m != nil {
			rej.Files = []string{m[1]}
		}
		return rej
	case strings.Contains(low, "push cannot contain secrets") || strings.Contains(low, "github push protection") ||
		((strings.Contains(msg, "GH013") || strings.Contains(low, "push protection")) && strings.Contains(low, "secret")):
		return &PushRejected{Kind: RejectSecretScanning, err: err}
	case strings.Contains(msg, "GH013") || strings.Contains(low, "repository rule violations"):
		return &PushRejected{Kind: RejectRepoRules, err: err}
	case strings.Contains(msg, "GH001") || strings.Contains(low, "exceeds github's file size limit"):
		return &PushRejected{Kind: RejectLargeFile, err: err}
	case strings.Contains(msg, "GH006") || strings.Contains(low, "protected branch hook declined"):
		return &PushRejected{Kind: RejectProtectedBranch, err: err}
	}
	return nil
}

// remoteText is the lines of git's push output the remote wrote: its
// "remote:" lines and the "! [remote rejected]" status lines.
func remoteText(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if i := strings.Index(t, "remote:"); i >= 0 && (i == 0 || strings.HasSuffix(strings.TrimSpace(t[:i]), ":")) {
			keep = append(keep, t)
		} else if strings.Contains(t, "[remote rejected]") {
			keep = append(keep, t)
		}
	}
	return strings.Join(keep, "\n")
}

// classified wraps a failed push's error as a *PushRejected when it is a
// permanent refusal.
func classified(err error) error {
	if rej := ClassifyPush(err); rej != nil {
		return rej
	}
	return err
}

// WorkflowDir is where GitHub reads workflow files; its App permission
// guards exactly this directory at the repository root.
const WorkflowDir = ".github/workflows/"

// The check is the net diff since the merge base, which has two blind
// spots, both left to GitHub's own refusal: a follow-up that rebases
// commits from before its start can attribute a person's workflow commit
// to the run, and a workflow file added and removed within the run passes.
//
// WorkflowFiles lists, sorted, the files under .github/workflows/ that the
// commits since changed: since...HEAD, the changes on HEAD's side of their
// merge base. since is origin/<base> for a first run and the commit a
// follow-up started from, so a workflow edit a person made earlier on the
// branch is not this run's. It is what GitHub refuses to take from an App
// without the `workflows` permission.
func (r *Repo) WorkflowFiles(ctx context.Context, since string) ([]string, error) {
	out, err := r.gitRaw(ctx, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", since+"...HEAD", "--", WorkflowDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if strings.HasPrefix(f, WorkflowDir) {
			files = append(files, f)
		}
	}
	return files, nil // git lists paths in sorted order
}

// MaxBundleBytes caps the bundle of a run's work that is saved.
const MaxBundleBytes = 256 << 20

// MaxScanBytes caps how much of the commits' text ScanWork reads; more is
// reported as unscannable.
const MaxScanBytes = 1 << 30

// ErrBundleTooLarge means the run's commits are bigger than the cap.
var ErrBundleTooLarge = errors.New("the run's commits are too large to bundle")

// Bundle is BundleMax with MaxBundleBytes.
func (r *Repo) Bundle(ctx context.Context, since string) ([]byte, error) {
	return r.BundleMax(ctx, since, MaxBundleBytes)
}

// BundleMax packs the commits HEAD has and since doesn't into a git
// bundle, so a run's work survives a push that can't succeed. It holds only
// those commits' objects (since is a prerequisite, not included), never
// the checkout's config, credentials, reflog or untracked files.
func (r *Repo) BundleMax(ctx context.Context, since string, max int64) ([]byte, error) {
	dir, err := r.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	// Inside .git, never in the working tree, and removed after.
	tmp := filepath.Join(dir, "fugaro-work.bundle")
	defer os.Remove(tmp)
	if _, err := r.gitRaw(ctx, "bundle", "create", "--quiet", tmp, "HEAD", "^"+since); err != nil {
		return nil, err
	}
	fi, err := os.Stat(tmp)
	if err != nil {
		return nil, err
	}
	if fi.Size() > max {
		return nil, fmt.Errorf("%w (%d bytes, cap %d)", ErrBundleTooLarge, fi.Size(), max)
	}
	return os.ReadFile(tmp)
}

// WorkScan is what ScanWork found.
type WorkScan struct {
	// Hit: scan returned true for some text of the range.
	Hit bool
	// Unscannable: the range changes a file git treats as binary (by its
	// content or by .gitattributes, which the agent controls), so a secret
	// in it could go unseen.
	Unscannable bool
	// TooLarge: the range is more than MaxScanBytes of text; the scan
	// stopped without a verdict.
	TooLarge bool
}

// scanOverlap is how much of one chunk is read again with the next, so a
// secret split across two reads is still seen whole.
var scanOverlap = 64 << 10

// scanChunk is how much of git's output is read, and scanned, at a time.
var scanChunk = 1 << 20

// ScanWork streams every commit's message and patch in since..HEAD (each
// commit, not the net diff: a value committed and then removed is still in
// a bundle) through scan, which reports whether the text holds something
// that must not be copied. Nothing external runs for the diff: not
// diff.external, not a textconv, which the agent could set in .git/config
// or .gitattributes. The text is never held whole.
func (r *Repo) ScanWork(ctx context.Context, since string, scan func(text string) bool) (WorkScan, error) {
	return r.scanWork(ctx, since, scan, MaxScanBytes)
}

func (r *Repo) scanWork(ctx context.Context, since string, scan func(text string) bool, maxBytes int64) (WorkScan, error) {
	var res WorkScan
	rng := []string{"^" + since, "HEAD"}
	// A binary change shows as "-<TAB>-" in numstat.
	nums, err := r.gitRaw(ctx, append([]string{"log", "-m", "--numstat", "--format=", "--no-show-signature", "--no-ext-diff", "--no-textconv", "--no-renames"}, rng...)...)
	if err != nil {
		return res, err
	}
	for _, l := range strings.Split(string(nums), "\n") {
		if strings.HasPrefix(l, "-\t-\t") {
			res.Unscannable = true
			return res, nil
		}
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(sctx, "git", append(slices.Clone(noHooks), append([]string{"log", "-m", "-p", "--format=%an <%ae> %cn <%ce>%n%B", "--no-show-signature", "--no-ext-diff", "--no-textconv", "--no-renames"}, rng...)...)...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = r.Dir, r.processEnv(), gitWaitDelay
	out, err := cmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return res, err
	}
	var total int64
	tail := ""
	buf := make([]byte, scanChunk)
	for {
		// Whole chunks, so where one ends doesn't depend on the pipe.
		n, rerr := io.ReadFull(out, buf)
		if n > 0 {
			total += int64(n)
			window := tail + string(buf[:n])
			if scan(window) {
				res.Hit = true
				cancel()
				break
			}
			tail = window[max(0, len(window)-scanOverlap):]
			if total > maxBytes {
				res.TooLarge = true
				cancel()
				break
			}
		}
		if rerr != nil {
			break
		}
	}
	werr := cmd.Wait()
	if res.Hit || res.TooLarge {
		return res, nil
	}
	if werr != nil {
		return res, fmt.Errorf("git log -p: %w: %s", werr, strings.TrimSpace(stderr.String()))
	}
	return res, nil
}
