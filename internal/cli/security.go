package cli

import (
	"context"
	"fmt"
	"path/filepath"
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
// Why that read costs no bucket. Every read here is the command's own and is
// offline: the working tree's file for the base branch's name
// (securityFileBaseBranch, which reads the text rather than resolving it over
// the project layer, because that resolution opens the runs bucket), the local
// config for the two layers below it (selectedProjectConfigReadOnly, the
// side-effect-free selection the update check itself uses), and git's own
// object database for the file at the ref. No bucket, no database, no
// credential: the command runs where a coding agent's session does, with
// nothing set up. The one layer it does not read is the published policy's
// base_branch — a database read — so a project whose default lives only there
// reads as main here, and that is the one case where a run could be cut from a
// branch this document does not name.
//
// Why `sensitive` is the list and not the classifier's complement (F22, D1512).
// The two questions a change is escalated by are not the same question. A run
// escalates on a row of the list in force that matches its diff (SC6, F7), so
// `sensitive` is true when a row of the list matched the path — and true when
// the reader cannot place the path at all, which is the one case the list can
// never speak about: absolute, escaping, uncleaned, outside ASCII, or naming no
// path in the checkout at all (`.` and `..`). Nothing else makes it true. `sensitivity.Classify` is *not* asked, and this is deliberate:
// its Sensitive is the complement of its routine trees, so asking it would
// answer true for most of any repository (`internal/cli/registry.go` is in no
// routine tree and on no floor row) and would tell the agent that a change
// forces level 3 when the run will not. The floor is a positive list, and Check
// 370's own ground is that a run whose diff touches nothing on it does not
// escalate. The `matched` member is what tells the two reasons apart.
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
	// matched the path, and true when the reader cannot place the path at all —
	// a spelling that is not a clean repository-relative path, or one that names
	// no path in a checkout (`.`, `..`) — because no list can speak about a
	// spelling that means more than one path or none (F22, D1512). A placeable
	// path no row matched is false, whatever the classifier calls its tree.
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

Every read this command makes is offline: it opens no bucket, no database and no
host, and it reads a file at a ref only out of the checkout's own object
database.`,
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
whether the list treats it as security-sensitive, and the row that decided. The
list is a positive one, so a path no row matches is answered not sensitive —
except a path the reader cannot place at all (absolute, escaping, uncleaned,
outside ASCII, or naming no path in the checkout at all: . and ..): that one is
answered sensitive, never routine, because a spelling that means more than one
path, or none, is one no list can speak about.

The list is read at the base branch — the ref the next run from this checkout is
cut from — and never from the working copy, so a change cannot carry the rule
that governs its own review. The ref's name is resolved from this checkout's own
fugaro.yaml and the local config, and the published policy's base_branch is not
consulted: it is a database read, and every read this command makes is offline.
A project whose default base branch lives only in the published policy therefore
reads as main here.`,
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

// securityProbeRow is a row the matcher is asked and no list ever holds: a glob
// doublestar matches against every path. It is built here rather than written
// anywhere, and `sensitivity.Pattern.Validate` refuses it (a row that matches
// the whole repository is the project's always_level_3 setting, D1503) — which
// is the point: it is not a rule, it is the question "can the reader place this
// path at all?", asked of the one function that decides it.
var securityProbeRow = sensitivity.Pattern{Name: "placeable", Match: "**", Category: sensitivity.Artifacts}

// securityPlaceable reports whether the reader can place a path inside the
// repository at all. Match asks cleanRelative of every path before it looks at
// any row, so the probe row — a glob that covers everything — matches exactly
// the paths the reader can place: an absolute, escaping, uncleaned or non-ASCII
// spelling is skipped before the glob is reached, and D1512 answers it sensitive.
//
// The rule is the package's own and is not restated here: a second spelling of
// "clean and repository-relative" in this file is the copy that drifts from the
// one the matcher and the classifier ask. The direction is the safe one — a path
// the probe does not match is answered sensitive, never routine.
//
// `.` and `..` are the two spellings the matcher's own rule accepts and that
// name no path in a checkout, and they are refused here rather than by that
// rule: cleanRelative compares the path with path.Clean's answer, and
// path.Clean leaves a bare `.` and a bare `..` exactly as they are, so both
// pass it while every other spelling that holds a `.` or `..` component —
// `./x`, `docs/..`, `a/./b` — is rewritten and refused. Neither names a file of
// the repository (`.` is the repository root as a directory reference and `..`
// is the tree above it), so a list can speak about neither, and answering
// `sensitive: false` for them would be the one answer D1512 keeps out of this
// field. Every spelling with a component to its name is left to the matcher:
// `.gitignore` and `...` are ordinary file names and are placed.
func securityPlaceable(path string) bool {
	if path == "." || path == ".." {
		return false
	}
	return len(sensitivity.Match([]string{path}, []sensitivity.Pattern{securityProbeRow})) > 0
}

// securityAnswer answers one path against the list in force. The first row of
// the list that matched is the one named, in the list's own order, so a path
// matched by the floor and by a layer above it names the floor's row rather than
// a later one. `sensitive` is a row match, or a path the reader cannot place at
// all: the classifier's class is deliberately not a third input, because a run
// escalates on the list and not on the class (see this file's header).
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
	answer.Sensitive = answer.Matched != nil || !securityPlaceable(path)
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
// else main. The published policy's base_branch is not consulted — reading it
// is a database read, and this command reaches nothing — so a project whose
// default lives only there reads as main here, exactly as it does in
// modelsBaseBranch. The repository is named by its origin rather than by a key
// of the local config, so it is looked up through RepoFor, the one lookup
// localcfg offers for a repository named from outside.
func securityBaseBranch(ctx context.Context, repo string) string {
	file, fileSets := securityFileBaseBranch(ctx, repo)
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

// securityFileBaseBranch is layer 1 read without a layer: the base branch the
// working directory's checkout of repo names, and whether its fugaro.yaml
// writes git.base_branch at all — the same two answers checkoutBaseBranch
// gives, out of the file's own text (config.GitBaseBranchOf) rather than out of
// the file resolved over the project layer.
//
// checkoutBaseBranch cannot be used here, and this is the whole reason the
// function exists: its value comes from checkoutConfig, which resolves the
// file over the published project layer — a read of the runs bucket
// (checkoutParse → findLayer, which opens gs://fugaro-runs-<gcp> and writes the
// local layer cache). This command's contract is that it opens no bucket
// (§6.23), and nothing is lost by reading the file instead: a project layer
// cannot set git.base_branch at all (config.LayerGit carries provider and pr
// and nothing else), so the resolution and the file's own text name the same
// branch. modelsBaseBranch makes the same offline choice for the same reason,
// with the parsed config it already holds.
//
// The file is the working tree's, as it is for a launcher resolving the ref of
// a launch that has none yet: the list is read at the base branch, and the
// branch's own name has to be answered before any ref can be read at all.
func securityFileBaseBranch(ctx context.Context, repo string) (string, bool) {
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return "", false
	}
	data, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	if err != nil {
		return "", false
	}
	return config.GitBaseBranchOf(data)
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
