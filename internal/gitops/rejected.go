package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
)

// PushRejected is a push the host refused for a permanent reason. It wraps
// the git error it was read from.
type PushRejected struct {
	Kind RejectKind
	// Files are the workflow files the host named, when it named them.
	Files []string
	err   error
}

func (e *PushRejected) Error() string { return fmt.Sprintf("push rejected (%s): %v", e.Kind, e.err) }
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
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "without `workflows` permission"):
		rej := &PushRejected{Kind: RejectWorkflows, err: err}
		if m := workflowFileRE.FindStringSubmatch(msg); m != nil {
			rej.Files = []string{m[1]}
		}
		return rej
	case strings.Contains(msg, "GH013") || strings.Contains(low, "push cannot contain secrets") || strings.Contains(low, "push protection"):
		return &PushRejected{Kind: RejectSecretScanning, err: err}
	case strings.Contains(msg, "GH001") || strings.Contains(low, "exceeds github's file size limit"):
		return &PushRejected{Kind: RejectLargeFile, err: err}
	case strings.Contains(msg, "GH006") || strings.Contains(low, "protected branch hook declined"):
		return &PushRejected{Kind: RejectProtectedBranch, err: err}
	}
	return nil
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

// WorkflowFiles lists, sorted, the files under .github/workflows/ that the
// run's commits change against origin/<base>: what GitHub refuses to take
// from an App without the `workflows` permission.
func (r *Repo) WorkflowFiles(ctx context.Context, base string) ([]string, error) {
	out, err := r.gitRaw(ctx, "diff", "--name-only", "-z", "--no-renames", "origin/"+base+"...HEAD", "--", WorkflowDir)
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

// ErrBundleTooLarge means the run's commits are bigger than the cap.
var ErrBundleTooLarge = errors.New("the run's commits are too large to bundle")

// Bundle is BundleMax with MaxBundleBytes.
func (r *Repo) Bundle(ctx context.Context, base string) ([]byte, error) {
	return r.BundleMax(ctx, base, MaxBundleBytes)
}

// BundleMax packs the commits HEAD has and origin/<base> doesn't into a git
// bundle, so a run's work survives a push that can't succeed. It holds only
// those commits' objects (the base is a prerequisite, not included), never
// the checkout's config, credentials, reflog or untracked files.
func (r *Repo) BundleMax(ctx context.Context, base string, max int64) ([]byte, error) {
	dir, err := r.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	// Inside .git, never in the working tree, and removed after.
	tmp := filepath.Join(dir, "fugaro-work.bundle")
	defer os.Remove(tmp)
	if _, err := r.gitRaw(ctx, "bundle", "create", "--quiet", tmp, "HEAD", "^origin/"+base); err != nil {
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

// Patch is the run's diff against origin/<base>, as text: what a secret
// scan reads before the work is copied anywhere.
func (r *Repo) Patch(ctx context.Context, base string) (string, error) {
	out, err := r.gitRaw(ctx, "diff", "origin/"+base+"...HEAD")
	return string(out), err
}
