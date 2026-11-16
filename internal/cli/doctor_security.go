package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The security list in force, as doctor says it (F11, D1513): which patterns a
// run of this repository escalates on, how many of them the engine's floor
// holds and how many a project layer or the repository wrote, and the one
// thing about that list the reader has to fix — a row this repository's own
// file wrote that matches nothing in the base, which escalates nothing and so
// does not do what its author thinks (D1503's second half).
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
// The floor's own rows are counted and never measured: the floor names trees
// most repositories do not have (internal/creds/**, mobile/**), so measuring it
// would put a finding on nearly every screen in a repository with nothing wrong
// in it — and the reader could not repair it, since the floor is not theirs to
// edit. The project layer's rows are in the same state for the same reason: the
// layer is shared by every repository of the project, so a row naming a tree
// this repository does not have is the ordinary case, and the fix the finding
// asks for — drop the row — would take it away from the repository that does
// have that tree. A row this repository's own file wrote is the row its reader
// can fix.
const (
	doctorSecurityID       = "security-list"
	doctorSecurityUnusedID = "security-list-unused"
)

// doctorSecurityChecks is the check doctor runs in a checkout: the list in
// force there, and the written row that matched nothing in its base.
//
// The two written layers are read offline and at the base branch (D1518): the
// repository's own rows out of its fugaro.yaml at the default branch's ref, and
// the project layer's out of this machine's cached copy of the published layer
// (modelsLayer). A project layer's rows are missing from the count in two states,
// which this check cannot tell apart from a project that wrote none: this machine
// holds no cached layer for the project, or the cached one is not the base file's
// — a layer applies only to a file that names its project and gcp_project
// (config.ProjectLayer.Applies, the rule a run's ResolveConfig decides Applied
// by), and counting the rows of a layer no run of this checkout applies would
// report a list in force that is not the run's. Neither state is doctor's to
// leave unsaid, and neither is said here: doctor is an offline read (SC4), the
// first is the floor's own count moving, and the `project-layer` line beside this
// one is where a layer's state is reported (doctorLayerChecks: "no project layer
// applies to this checkout" is what an unanchored file gets).
//
// A row the matcher refuses is not in the count either: securityBaseFile drops
// it before this check sees the block, so that measuring a written row against
// the base's tree cannot be made to cost what the row's own text costs
// (securityMatchable, and sensitivity.Match's own note). The file that writes one
// is the `fugaro-yaml` line's subject, over the working copy's file — and a
// base-branch file that differs from it is `fugaro security paths`' to report,
// which names the row and sensitivity.Pattern.Validate's reason. This check says
// nothing about it rather than measuring a row no run holds.
func doctorSecurityChecks(ctx context.Context, root string) []doctorCheck {
	return doctorSecurityListChecks(
		func() (*config.Security, *config.ProjectLayer) {
			ref := defaultBranchRef(ctx, root)
			if ref == "" {
				return nil, nil
			}
			project, gcpProject, own, _, reason := securityBaseFile(ctx, root, ref)
			if reason != "" {
				// The repository's own file is not one this reader can read, or
				// writes a block no run holds: there are no rows of the
				// repository's, and no project to read a layer for either, so the
				// list in force is the floor.
				return nil, nil
			}
			// The cached layer is read by project name, and a run applies a layer
			// only to a file whose project: and gcp_project: are the layer's own
			// (config.ProjectLayer.Applies, the rule ResolveConfig's Applied is):
			// counting a layer this base file does not anchor to would report rows
			// in the list in force that no run of this checkout escalates on.
			layer := modelsLayer(project)
			if !layer.Applies(project, gcpProject) {
				return own, nil
			}
			return own, layer
		},
		func() (string, []string) {
			ref := defaultBranchRef(ctx, root)
			paths, ok := doctorSecurityBaseTree(ctx, root, ref)
			if !ok {
				return "", nil
			}
			return ref, paths
		})
}

// doctorSecurityListChecks is the check over the list in force: layers answers
// the two lists a person wrote — the repository's own block and the project
// layer, either of which may be nil — and baseTree answers the base branch's ref
// and its committed paths. Both are lazy, and the tree listing is the one that
// has to be: a list nothing was added to carries no row of the reader's to
// measure, so it costs no git read over the base at all.
//
// An answer of no ref at all — a checkout with no default branch, or a tree this
// checkout could not read — says the floor alone: there is no tree to measure a
// row against, and a finding nobody can check is worse than none.
func doctorSecurityListChecks(layers func() (*config.Security, *config.ProjectLayer), baseTree func() (string, []string)) []doctorCheck {
	own, layer := layers()
	list := config.SecurityPatterns(own, layer).List()
	out := []doctorCheck{doctorSecurityInForce(list)}
	written := doctorSecurityWritten(list)
	if len(written) == 0 {
		return out
	}
	ref, paths := baseTree()
	if ref == "" {
		return out
	}
	// A name is a row's identity in the list in force (sensitivity.Union), so
	// the names Match answered with are what says which written rows matched.
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

// doctorSecurityWritten is the rows of the list in force this reader can fix, in
// the list's own order: the repository's own, and no other layer's. The floor's
// rows are not the reader's to edit, and a project layer's row is not either: it
// is shared by every repository of the project, and the fix this finding asks
// for — drop the row — would take it away from the repositories that do have the
// tree it names. Both layers are counted by the line beside this one and neither
// is measured (D1503's finding is over a row written for the tree it is measured
// against).
func doctorSecurityWritten(list config.SecurityList) []sensitivity.Pattern {
	var written []sensitivity.Pattern
	for _, row := range list {
		if row.Source == config.SecuritySourceRepo {
			written = append(written, row.Pattern)
		}
	}
	return written
}

// doctorSecurityInForce is the measurement: the list in force by counts, and
// where each count comes from. The floor's rows and the two layers' are three
// counts rather than one, because a reader who is missing a whole layer's rows —
// a project whose layer this machine holds no cached copy of — has to be able to
// see the count that would have moved.
func doctorSecurityInForce(list config.SecurityList) doctorCheck {
	count := map[config.SecuritySource]int{}
	for _, row := range list {
		count[row.Source]++
	}
	problem := fmt.Sprintf("the security list in force is %s: %s from the engine's floor, %s written by this project's layer, and %s written by this repository",
		doctorSecurityPatternCount(len(list)), doctorSecurityPatternCount(count[config.SecuritySourceFloor]),
		doctorSecurityPatternCount(count[config.SecuritySourceProject]), doctorSecurityPatternCount(count[config.SecuritySourceRepo]))
	return doctorCheck{
		ID:       doctorSecurityID,
		OK:       true,
		Severity: "info",
		Problem:  problem,
		Fix:      "nothing to do: the engine's floor is the least a run escalates on and cannot be lowered, and a project layer or this repository may only add rows to it",
	}
}

// doctorSecurityUnused is D1503's finding: the rows this repository wrote that
// no path in ref matches.
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
// what a change being reviewed can edit). It is the listing internal/runner
// reads a base's paths with, so the two agree about what a tree's paths are.
//
// ok is false when there is no tree to read: no checkout, no ref, a read that
// failed, or a tree with no paths in it. None of those is a base a row can be
// measured against, and an empty listing that a failed read answered with
// would call every written row unmatched — a finding nobody can check is worse
// than none, which is the same rule a checkout with no default branch gets.
// gitCmd is used rather than gitRead because this listing is NUL-separated:
// trimming it would eat the leading byte of the base's first path.
func doctorSecurityBaseTree(ctx context.Context, root, ref string) ([]string, bool) {
	if root == "" || ref == "" {
		return nil, false
	}
	out, err := gitCmd(ctx, root, "ls-tree", "-r", "-z", "--name-only", "--end-of-options", ref).Output()
	if err != nil {
		return nil, false
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, len(paths) > 0
}
