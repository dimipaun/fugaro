package ciselect

import (
	"context"
	"fmt"
	"strings"
)

// VerifySelection is the in-job verify step's answer: the packages a change
// reaches, or nothing at all when the selector cannot place it — which the
// caller reads as "run everything it would have run without a selector".
//
// It is Output's sibling and not a reuse of it, because the two callers ask
// different questions. A ci run wants a shard matrix: legs, the families
// internal/cli and ./scripts split into, the terraform and rules jobs. The
// verify step wants the packages themselves, since it runs them here, in the
// container the change was made in, with the two invocations its own command
// already knows how to split ./internal/cli into.
type VerifySelection struct {
	// Reason is why, for the caller's log: a doubt's own words (R8), or what
	// was selected and from what.
	Reason string
	// Packages are the packages the change reaches, repository-relative
	// ("./internal/cli"), sorted and deduplicated. Empty means the caller runs
	// everything: the change is a doubt, the diff could not be read, or
	// nothing it reaches is a package this step can run.
	Packages []string
}

// SelectVerify answers the in-job verify step's question about the change in
// in.Root, measured from in.Base: which of this module's packages does it
// reach. Only Root, Base and Command are read; there is no dispatch input, no
// shard budget and no run to ask, because the step this answers for runs in
// the container the change was made in.
//
// The change is measured from Base to the working tree and not to a commit:
// what the verify step tests is the tree its command runs in, uncommitted
// changes included. A tree with untracked files is left out on purpose — a new
// file that was never added is no part of a commit, and the run's own
// readiness already requires a clean tree.
//
// Every doubt is an empty selection and never an error: the one direction a
// selector may not fail in is running fewer tests than the change can break
// (R8), so what it cannot answer it hands back as "run everything".
func SelectVerify(ctx context.Context, in Input) VerifySelection {
	root := in.Root
	if root == "" {
		root = "."
	}
	if in.Base == "" {
		return VerifySelection{Reason: "no commit to measure the change from"}
	}
	files, err := worktreeChanges(ctx, in, root, in.Base)
	if err != nil {
		return VerifySelection{Reason: ReasonDiff + ": " + err.Error()}
	}
	if len(files) == 0 {
		return VerifySelection{Reason: "nothing changed against " + in.Base}
	}
	an := analyze(ctx, in, root, files)
	if an.doubt != "" {
		return VerifySelection{Reason: an.doubt}
	}
	if len(an.chosen) == 0 {
		return VerifySelection{Reason: "the change reaches no package this step can run"}
	}
	return VerifySelection{
		Packages: an.chosen,
		Reason: fmt.Sprintf("selected %s from %s against %s",
			selectCount(len(an.chosen), "package", "packages"),
			selectCount(len(files), "changed file", "changed files"), in.Base),
	}
}

// worktreeChanges is what the working tree differs from base in.
//
// It is a two-dot tree diff against the working tree, which keeps a renamed
// file's old name as well as its new one, where a rename-detecting diff would
// report the new name alone and lose the tests that read the old path (F11).
// One revision and no second one is the working tree, so a change the agent
// has made but not committed is measured too.
func worktreeChanges(ctx context.Context, in Input, root, base string) ([]string, error) {
	out, err := command(in).Output(ctx, root, "git", "diff", "--name-only", "--no-renames", base)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// VerifyLine is a selection as the verify step's own command reads it: the
// package paths on one line, and nothing at all where the caller must run
// everything.
func VerifyLine(s VerifySelection) string {
	return strings.Join(s.Packages, " ")
}
