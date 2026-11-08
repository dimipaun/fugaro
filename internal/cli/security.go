package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
)

// fugaro security paths (the security-classification design's SC3, D1512) is
// the reader an agent calls instead of guessing: what this repository's runs
// treat as security-sensitive, and what any one path is treated as.
//
// Why a command and not a `config` verb (D1512). `config`'s subcommands read
// the repository's own configuration; this reads the resolution a run is
// governed by, whose base-branch half a `config` verb does not reach at all.
//
// Why the base branch and never the working copy (F14, D1518). The list a run
// is reviewed against is read from its base branch, so a change can never carry
// the rule that governs its own review. This command is that read, one layer
// up: it prints what the next run from this checkout will resolve. The one
// thing read at the ref is the repository's fugaro.yaml, out of the checkout's
// own object database (readFugaroYAMLAtRef) — never the network, never the
// working tree — and the base branch's *name* is resolved the way every
// launcher resolves it, from the working tree's file and the local config, as
// modelsBaseBranch's own read does.
//
// Why a path no row matches is still answered. `sensitivity.Match` answers the
// level question and `sensitivity.Classify` the class one; this command asks
// both, and answers Sensitive when either does (F22). A path the matcher cannot
// place — absolute, escaping, uncleaned, outside ASCII — is placed by no row
// and is Sensitive, which is D1512's own rule, and a path outside every tree
// the classifier knows is Sensitive for the reason classify.go's package doc
// gives: a tree nobody classified yet is not a tree a reader may call routine.
// The `matched` member is what tells the two apart.
//
// What it never prints. Nothing here is a credential: the list is names,
// globs and categories, the checked paths are the caller's own argv, and the
// document carries no argv of its own. A refusal names a path or a ref and
// never a value read out of a file.
//
// What is not here yet. This slice is the floor alone (D1504, D1519): no
// project key and no repository key exists, so every row's `source` is `floor`
// and the resolution has one layer to merge. SC7 and SC8 add the keys, SC9
// re-points this read at the one resolution, and nothing in the document's
// shape has to move for either.

// securitySourceFloor is the source label the floor's own rows carry (D1512's
// three words: floor, project, repo).
const securitySourceFloor = "floor"

// securityPathsDoc is D1512's document, and it is the contract the desktop
// console's §6.23 describes. The shape is closed in the way every wire shape in
// this program is: a member added is a new member, and a field whose meaning
// has to change gets a new name rather than a second meaning under the old one.
type securityPathsDoc struct {
	// Project is the project the repository's own file names at Base, and ""
	// when that file does not name one — or could not be read at all, which is
	// Error's own case.
	Project string `json:"project"`
	// Repo is owner/name of the checkout this answered for.
	Repo string `json:"repo"`
	// Base is the branch the list was read at. It is the ref the next run from
	// this checkout is cut from, resolved through config.EffectiveBaseBranch.
	Base string `json:"base"`
	// FloorSource is the source label the floor's rows carry in Security. It is
	// what lets a reader tell the engine's own rows from a layer's without
	// scanning the list for one label, and it is "floor" in every answer this
	// slice can give (D1504).
	FloorSource string `json:"floor_source"`
	// Security is the list in force: the rows, and how many of them there are.
	Security securityListDoc `json:"security"`
	// Checked is one answer per --check path, in the order they were given, and
	// null when the command was asked for the list alone. A caller that asked
	// for no path gets no answers rather than an empty list that reads like
	// one.
	Checked []securityPathAnswer `json:"checked"`
	// Error is why the repository's own file could not be read at Base: this
	// checkout holds no such ref, holds no fugaro.yaml at it, or holds one this
	// reader cannot place a project in. It is a state inside the document and
	// not a refusal, because the list in force still answers — it is the floor
	// alone — and a caller that only wants to know what a path is treated as
	// still gets its answer. "" means the read answered. The text names a ref or
	// a line, never a value read out of the file.
	Error string `json:"error"`
}

// securityListDoc is the list in force: its rows and its size. The count is a
// member rather than len(patterns) at each reader because a consumer that draws
// a figure wants one number to draw, and because it is the one thing a
// truncated read can be compared against.
type securityListDoc struct {
	// Patterns is the list in force, floor first, in the order a run matches
	// it: each row is a name, the doublestar glob it covers and the category it
	// labels those paths with (sensitivity.Pattern).
	Patterns []securityPattern `json:"patterns"`
	// Count is how many rows the list in force has.
	Count int `json:"count"`
}

// securityPattern is one row of the list in force, with the layer it came from.
type securityPattern struct {
	// Name is the row's name: what the run record, the pull request body and
	// fugaro watch print when they say which rule decided (D24).
	Name string `json:"name"`
	// Match is the row's glob, repository-relative.
	Match string `json:"match"`
	// Category is what the paths this row matches are labelled with, out of
	// sensitivity.Categories.
	Category string `json:"category"`
	// Source is the layer the row came from: floor, project or repo (D1512).
	Source string `json:"source"`
}

// securityPathAnswer is one path's answer: whether the list in force treats it
// as security-sensitive, and the row that decided when one did.
type securityPathAnswer struct {
	// Path is the path as it was asked for, and never a cleaned or rewritten
	// spelling of it: the answer is keyed by what the caller typed.
	Path string `json:"path"`
	// Sensitive is the fail-closed answer: true when a row of the list in force
	// matched the path, and true when the path is one the classifier cannot
	// place inside a tree it knows (F22, D1512) — which is every path that is
	// not clean, repository-relative and inside a routine tree.
	Sensitive bool `json:"sensitive"`
	// Matched is the row that decided the answer, and null when no row did —
	// which is a real answer and not a missing one: the list in force says
	// nothing about this path.
	Matched *securityMatch `json:"matched"`
}

// securityMatch names the row that decided one path's answer.
type securityMatch struct {
	// Name is the row's name.
	Name string `json:"name"`
	// Category is the row's category.
	Category string `json:"category"`
	// Source is the layer the row came from.
	Source string `json:"source"`
}

// newSecurityCmd is `fugaro security`.
func newSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "security",
		Short: "Answer what this repository treats as security-sensitive",
		Long: `Answer what this repository's runs treat as security-sensitive.

The list in force is a positive one: the trees, files and file classes a change
has to touch to be reviewed as a security-sensitive change, one named row each.
It is read at the base branch, so a change can never carry the list that governs
its own review.

Every read here is offline: it touches no bucket, no database and no host, and
it reads a file at a ref only out of the checkout's own object database.`,
	}
	cmd.AddCommand(newSecurityPathsCmd())
	return cmd
}

// securityPathsOptions are `fugaro security paths`' own two flags.
type securityPathsOptions struct {
	// check is the paths to answer, in the order they were given.
	check []string
	// json is the document rather than the table.
	json bool
}

// newSecurityPathsCmd is `fugaro security paths`.
func newSecurityPathsCmd() *cobra.Command {
	o := &securityPathsOptions{}
	cmd := &cobra.Command{
		Use:   "paths",
		Short: "Print the pattern list in force, and answer a path against it",
		Long: `Print the list of paths this repository's runs treat as security-sensitive.

Without --check the whole list in force is printed, each row with the layer it
came from. With --check, each named path is answered against that list instead:
whether it is security-sensitive, and the row that decided. A path no row
matches and that the classifier cannot place inside a tree it knows is answered
sensitive, never routine: an unknown path fails closed.

The list is read at the base branch — the ref the next run from this checkout is
cut from — and never from the working copy, so a change cannot carry the rule
that governs its own review.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runSecurityPaths(cmd, o) },
	}
	cmd.Flags().StringArrayVar(&o.check, "check", nil, "answer a repository-relative path against the list in force instead of printing it (repeatable)")
	cmd.Flags().BoolVar(&o.json, "json", false, "print the document as JSON")
	return cmd
}

// runSecurityPaths answers for the repository the working directory is a
// checkout of. It is deliberately not a repo-flag command: the list it reads is
// a file at a ref of the checkout it is standing in, and a --repo would name a
// repository whose refs this process cannot read.
func runSecurityPaths(cmd *cobra.Command, o *securityPathsOptions) error {
	ctx := cmd.Context()
	repo, err := originRepo(ctx)
	if err != nil {
		return userErr("fugaro security paths answers for one repository: run it from that repository's checkout (this directory's origin remote names none)")
	}
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return userErr("fugaro security paths answers for one repository: run it from the checkout of %s (this directory is not one)", repo)
	}

	base := securityBaseBranch(ctx, repo)
	doc := securityPathsDoc{
		Repo:        repo,
		Base:        base,
		FloorSource: securitySourceFloor,
	}
	doc.Project, doc.Error = securityBaseProject(ctx, root, base)

	// The list in force is the floor alone in this slice (D1504): an absent
	// project list, an absent repository list and an explicitly empty list all
	// mean the floor, and no key exists to write yet.
	list := sensitivity.Floor()
	doc.Security.Count = len(list)
	for _, row := range list {
		doc.Security.Patterns = append(doc.Security.Patterns, securityPattern{
			Name:     row.Name,
			Match:    row.Match,
			Category: string(row.Category),
			Source:   securitySourceFloor,
		})
	}
	for _, path := range o.check {
		doc.Checked = append(doc.Checked, securityAnswer(path, list))
	}

	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	return writeSecurityPaths(cmd, doc)
}

// securityAnswer answers one path against the list in force. The two questions
// are SC1's own two: Match names the rows of the list the path matches, and
// Classify is what places it inside a tree at all — a path it cannot place is
// Sensitive, and so is a path outside every routine tree (classify.go's package
// doc). The first row of the list that matched is the one named, in the list's
// own order, so a path matched by the floor and by a layer above it names the
// floor's row rather than a later one.
func securityAnswer(path string, list []sensitivity.Pattern) securityPathAnswer {
	answer := securityPathAnswer{Path: path}
	matched := sensitivity.Match([]string{path}, list)
	if len(matched) > 0 {
		answer.Matched = &securityMatch{
			Name:     matched[0].Name,
			Category: string(matched[0].Category),
			Source:   securitySourceFloor,
		}
	}
	answer.Sensitive = answer.Matched != nil || sensitivity.Classify([]string{path}) == sensitivity.Sensitive
	return answer
}

// securityBaseProject is the project the repository's own fugaro.yaml names at
// ref, and the reason that read did not answer ("" when it did). The second
// return is the document's `error`: a ref this checkout does not hold, no
// fugaro.yaml at it, or a file with no place for a project name in it. Both
// texts name a ref or a line and never a value read out of the file.
func securityBaseProject(ctx context.Context, root, ref string) (string, string) {
	data, err := readFugaroYAMLAtRef(ctx, root, ref)
	if err != nil {
		return "", err.Error()
	}
	project, err := config.ProjectOf(data)
	if err != nil {
		return "", fmt.Sprintf("fugaro.yaml at %s: %v", ref, err)
	}
	return project, ""
}

// securityBaseBranch is the ref the list in force is read at, resolved through
// the one function every layer resolves through (config.EffectiveBaseBranch):
// the checkout's working-tree file when it writes git.base_branch, else the
// local config's repos.<r>.base_branch, else the project's own base_branch,
// else main. It is modelsBaseBranch's own resolution for the same reason — the
// published policy's base_branch is a cloud read and this command reaches
// nothing — with one difference: the repository is named by its origin rather
// than by a key of the local config, so it is looked up through RepoFor, the
// one lookup localcfg offers for a repository named from outside.
func securityBaseBranch(ctx context.Context, repo string) string {
	file, fileSets := checkoutBaseBranch(ctx, repo)
	repoBranch, projectBranch := "", ""
	if lc := selectedProjectConfigReadOnly(ctx); lc != nil {
		projectBranch = lc.BaseBranch
		if r, ok := lc.RepoFor(repo); ok {
			repoBranch = r.BaseBranch
		}
	}
	base, _ := config.EffectiveBaseBranch(file, fileSets, repoBranch, projectBranch)
	return base
}

// writeSecurityPaths prints the document as the table a person reads: the list
// with its layers, or one answer per checked path. The error, when there is
// one, goes to stderr beside the answer rather than inside it, because the
// answer is still an answer — the floor alone — and a caller piping stdout
// should be told what it is missing without a JSON member in a table.
func writeSecurityPaths(cmd *cobra.Command, doc securityPathsDoc) error {
	w := cmd.OutOrStdout()
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if len(doc.Checked) > 0 {
		fmt.Fprintf(w, "security answers for %s at origin/%s\n", oneLine(doc.Repo), oneLine(doc.Base))
		fmt.Fprintln(tw, "PATH\tSENSITIVE\tMATCHED")
		for _, a := range doc.Checked {
			sensitive, matched := "no", "-"
			if a.Sensitive {
				sensitive = "yes"
			}
			if a.Matched != nil {
				matched = fmt.Sprintf("%s (%s, %s)", a.Matched.Name, a.Matched.Category, a.Matched.Source)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", cellOneLine(a.Path), sensitive, cellOneLine(matched))
		}
	} else {
		fmt.Fprintf(w, "security patterns for %s at origin/%s (%d rows, source: %s)\n",
			oneLine(doc.Repo), oneLine(doc.Base), doc.Security.Count, oneLine(doc.FloorSource))
		fmt.Fprintln(tw, "NAME\tMATCH\tCATEGORY\tSOURCE")
		for _, p := range doc.Security.Patterns {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", cellOneLine(p.Name), cellOneLine(p.Match), cellOneLine(p.Category), cellOneLine(p.Source))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if doc.Error != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: the repository's own list could not be read: %s; what is printed is the floor alone\n", oneLine(doc.Error))
	}
	return nil
}
