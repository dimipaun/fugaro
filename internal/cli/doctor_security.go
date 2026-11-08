package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The security list in force, as doctor says it (F11, D1513): which patterns a
// run of this repository escalates on, how many of them the engine's floor
// holds and how many a project layer or the repository wrote, and the one
// thing about that list the reader has to fix — a written row that matches
// nothing in the base, which escalates nothing and so does not do what its
// author thinks (D1503's second half).
//
// Counts and names, never a value: a pattern is a path glob the reader wrote
// in their own config, so doctor prints it back the way it prints any other
// repository text, through pluginwire.Printable. Nothing here reads a path's
// content, so nothing here can print one.
//
// The class is decided by doctorClassOf, from the ID and the severity, and
// both states are deliberate: a list that is sound is only measured (an info
// line with the security sweep's prefix, so the default view does not print it
// and --security does), and a pattern that matched nothing is a finding the
// reader acts on (a warning, an item of the default view, which fails --strict
// and never the plain run — a list may be written before the tree it names
// exists, which is exactly what D1503 refuses to call an error at write time).
//
// The floor's own rows are counted and never measured: the floor is 43 rows
// over trees most repositories do not have (internal/creds/**, mobile/**), so
// measuring it would put a finding on nearly every screen in a repository with
// nothing wrong in it — and the reader could not repair it, since the floor is
// not theirs to edit. A row a person wrote is the row a person can fix.
const (
	doctorSecurityID       = "security-list"
	doctorSecurityUnusedID = "security-list-unused"
)

// doctorSecurityChecks is the check doctor runs in a checkout: the list in
// force there, and the written row that matched nothing in its base.
func doctorSecurityChecks(ctx context.Context, root string) []doctorCheck {
	// Slice 1 (D1519): the list in force is the engine's floor alone — the
	// config key does not exist yet — so nothing was written and no base is
	// read. SC9 re-points this at config.SecurityPatterns' one resolution,
	// which is where the project's and the repository's rows come from; the
	// closure below is what reads the base then.
	return doctorSecurityListChecks(sensitivity.Floor(), nil, func() (string, []string) {
		ref := defaultBranchRef(ctx, root)
		return ref, doctorSecurityBaseTree(ctx, root, ref)
	})
}

// doctorSecurityListChecks is the check over a list in force: floor is the
// engine's rows, written the project's and the repository's, and baseTree
// answers the base branch's ref and its committed paths — lazily, since a list
// nothing was written to has no row of the reader's to measure and costs no
// git read at all. A checkout with no base ref says the floor alone: there is
// no tree to measure a row against, and a finding nobody can check is worse
// than none.
func doctorSecurityListChecks(floor, written []sensitivity.Pattern, baseTree func() (string, []string)) []doctorCheck {
	out := []doctorCheck{doctorSecurityInForce(floor, written)}
	if len(written) == 0 {
		return out
	}
	ref, paths := baseTree()
	if ref == "" {
		return out
	}
	matched := map[string]bool{}
	for _, row := range sensitivity.Match(paths, written) {
		matched[row.Name] = true
	}
	var unused []sensitivity.Pattern
	for _, row := range written {
		if !matched[row.Name] {
			unused = append(unused, row)
		}
	}
	if len(unused) == 0 {
		return out
	}
	return append(out, doctorSecurityUnused(unused, ref))
}

// doctorSecurityInForce is the measurement: the list in force by counts, and
// where each count comes from.
func doctorSecurityInForce(floor, written []sensitivity.Pattern) doctorCheck {
	problem := fmt.Sprintf("the security list in force is %s: %s from the engine's floor, and %s written by this project or by this repository",
		doctorSecurityPatternCount(len(floor)+len(written)), doctorSecurityPatternCount(len(floor)), doctorSecurityPatternCount(len(written)))
	return doctorCheck{
		ID:       doctorSecurityID,
		OK:       true,
		Severity: "info",
		Problem:  problem,
		Fix:      "nothing to do: the engine's floor is the least a run escalates on and cannot be lowered, and a project layer or this repository may only add rows to it",
	}
}

// doctorSecurityUnused is D1503's finding: the rows a person wrote that no
// path in ref matches.
func doctorSecurityUnused(rows []sensitivity.Pattern, ref string) doctorCheck {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = pluginwire.Printable(r.Name) + " (" + pluginwire.Printable(r.Match) + ")"
	}
	what := "the security pattern " + names[0]
	if len(names) > 1 {
		what = "the security patterns " + strings.Join(names, ", ")
	}
	return doctorCheck{
		ID:       doctorSecurityUnusedID,
		OK:       false,
		Severity: "warning",
		Problem: fmt.Sprintf("%s matches no path in %s: a row that matches nothing never escalates a run, so the list does not do what its author thinks",
			what, pluginwire.Printable(ref)),
		Fix: "check the row's match against that tree, or drop it from the list: a row may be written before the tree it names exists, which is why this is said here and not refused when it is written",
	}
}

// doctorSecurityPatternCount is n patterns as a sentence reads it, the shape
// auditRecordCount gives doctor's other counts.
func doctorSecurityPatternCount(n int) string {
	switch n {
	case 0:
		return "no patterns"
	case 1:
		return "1 pattern"
	default:
		return fmt.Sprintf("%d patterns", n)
	}
}

// doctorSecurityBaseTree is the path set of ref's tree in this checkout: the
// base branch's committed paths, read from git and never from the working tree
// (D1518 — the controls a run reads come from the base, and a working copy is
// what a change being reviewed can edit). `git ls-tree -r --name-only` is the
// same listing imagecheck uses to read a commit's tree.
//
// A ref this checkout cannot read the tree of is no base here, and the check
// then says only what it can stand behind: defaultBranchRef has already
// resolved a ref that exists, and a read that fails anyway is the caller's
// best effort, the way doctor's other reads of this checkout are.
func doctorSecurityBaseTree(ctx context.Context, root, ref string) []string {
	if root == "" || ref == "" {
		return nil
	}
	out, err := gitRead(ctx, root, "ls-tree", "-r", "--name-only", "-z", ref)
	if err != nil {
		return nil
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}
