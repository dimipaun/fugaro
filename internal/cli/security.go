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
// Which list, and where its three layers are read (SC9, D1506, D1511). The list
// in force is config.SecurityPatterns' one resolution — the engine's floor, the
// project layer's rows and the repository's own, unioned — and each row carries
// the layer it came from. The two written halves are read offline, like every
// other read here: the repository's own rows out of its fugaro.yaml at the base
// ref, and the project layer's out of this machine's cached copy of the
// published layer (modelsLayer, the read `fugaro models ls` makes), never out of
// the bucket the published layer lives in. The cached copy is read by project
// name, so it is used only when the file at that ref names the layer's project
// and gcp_project (config.ProjectLayer.Applies) — the rule a run applies a layer
// by (ResolveConfig's Applied). A machine that holds no cached layer for the
// project, and a machine whose cached layer is not the one this file anchors to,
// therefore print the floor's rows and the repository's without the project's,
// and the command says which of the two it is on stderr rather than letting an
// incomplete list read like a complete one. The run is not weakened by either
// gap: it resolves the published layer in the cloud, so `fugaro security paths`
// is the advisory read and the runner's own resolution is the control.
//
// A row a later layer writes under a name an earlier layer's row already spends
// is not in force and is said on stderr beside the list
// (config.SecurityIgnoredRow, D1506): the row is what its author believes and
// the list in force is not, which is a difference only the reader can settle.
//
// The repository's own rows are measured before any of them is matched
// (securityMatchable). This reader matches a file's own bytes, and
// sensitivity.Match's cost is the row's own: the alternation refusal is what
// keeps one row from costing minutes, and every row a caller matches is meant to
// have passed it. So a row sensitivity.Pattern.Validate refuses is dropped and
// said on stderr, and a block that writes more rows than a layer may hold is
// refused whole. Both commands that resolve the list read the file through
// securityBaseFile, so both are bounded by it.

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
	// scanning the list for one label, and it is always `floor`: the engine's
	// rows are the resolution's first writer, so no project layer and no
	// repository can relabel one (D1504, D1506).
	FloorSource string `json:"floor_source"`
	// Security is the list in force: the rows, and how many of them there are.
	Security securityListDoc `json:"security"`
	// Checked is one answer per --check path, in the order they were given, and
	// null when the command was asked for the list alone. A caller that asked
	// for no path gets no answers rather than an empty list that reads like
	// one.
	Checked []securityPathAnswer `json:"checked"`
	// Error is why the repository's own file could not be read at Base: this
	// checkout holds no such ref, holds no fugaro.yaml at it, holds one this
	// reader cannot place a project in, or holds one whose security: block no run
	// holds because it writes more rows than a layer may hold
	// (securityMatchable). It is a state inside the document and not a refusal,
	// because the list in force still answers — it is the floor alone — and a
	// caller that only wants to know what a path is treated as still gets its
	// answer. "" means the read answered, which a block whose individual rows are
	// refused still does: those are dropped from the list and said on stderr, not
	// carried here. The text names a ref or a line, never a value read out of the
	// file. It is about this file alone and never about the project layer: a file
	// that names no project names no layer either, and a project layer that is not
	// in force for this file — no cached copy of it, or one the file's own
	// project: and gcp_project: do not name — is said on stderr rather than here.
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
		FloorSource: string(config.SecuritySourceFloor),
	}
	var own *config.Security
	var refused []securityRefusedRow
	var gcpProject string
	doc.Project, gcpProject, own, refused, doc.Error = securityBaseFile(ctx, root, base)

	// The list in force is the one resolution (SC9, D1506, D1511): the engine's
	// floor, the project layer that applies to this repository and the
	// repository's own rows, each row carrying the layer it came from. The
	// cached layer is read by project name alone (modelsLayer), while a run
	// applies a layer only to a file whose project: and gcp_project: are the
	// layer's own (config.ProjectLayer.Applies, the rule ResolveConfig's Applied
	// is), so one this file does not anchor to is not the layer a run of this
	// checkout resolves and is dropped here rather than printed as in force.
	cached := modelsLayer(doc.Project)
	layer := cached
	if !cached.Applies(doc.Project, gcpProject) {
		layer = nil
	}
	resolved := config.SecurityPatterns(own, layer)
	list := resolved.List()
	doc.Security.Count = len(list)
	for _, row := range list {
		doc.Security.Patterns = append(doc.Security.Patterns, securityPattern{
			Name:     row.Pattern.Name,
			Match:    row.Pattern.Match,
			Category: string(row.Pattern.Category),
			Source:   string(row.Source),
		})
	}
	for _, path := range o.check {
		doc.Checked = append(doc.Checked, securityAnswer(path, list))
	}

	if doc.Error != "" {
		// The answer is still an answer — with the repository's own file
		// unreadable there is no project to read a layer for and no rows of the
		// repository's, so the list really is the floor alone — and a caller
		// piping stdout should be told what it is missing without a JSON member
		// in a table.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: the repository's own list could not be read: %s; what is printed is the floor alone\n", oneLine(doc.Error))
	} else if doc.Project != "" && layer == nil {
		// The floor and the repository's rows are printed and the project's are
		// not, which the document's shape cannot say: its every member is the
		// console's contract (§6.23) and none of them is this. stderr is where
		// the table already says what a read did not answer.
		//
		// Two states leave the layer out, and they are different to the reader:
		// no cached copy at all, or one that is not this file's — the second is
		// an anchor to fix in the repository's own fugaro.yaml, and the wording
		// is the one doctor's `project-layer-drift` line uses for the same
		// mismatch in a run's record.
		if cached != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: this machine's cached copy of project %s's layer is not in force for this repository: fugaro.yaml at the base ref does not name the layer's project and gcp_project, and a run applies the layer only to a file that names both; the rows its security: block writes are not in this list\n", oneLine(doc.Project))
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: this machine holds no cached copy of project %s's layer, so the rows its security: block writes are not in this list; a run resolves the published layer itself\n", oneLine(doc.Project))
		}
	}
	for _, ig := range resolved.Ignored() {
		// A row the union dropped is a row its author believes and the list in
		// force is not (config.SecurityIgnoredRow, F10, D1506). It is said here
		// for the same reason the two above are: the document's shape is the
		// console's contract and carries no member for it.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: this %s writes a security row named %s (%s), which is not in force: the %s row of that name (%s) is the one a run matches, because a name is a row's identity in the list and only the first layer that writes it keeps it\n",
			ig.Source, cellOneLine(ig.Name), cellOneLine(ig.Written.Match), ig.HeldBy, cellOneLine(ig.Kept.Match))
	}
	for _, r := range refused {
		// A row the matcher refuses is a row its author wrote and no run holds
		// (securityMatchable). It is not the row above: no union of a name kept
		// it out — the file it is written in is one a run's load refuses. Said on
		// stderr for the same reason, with the index `fugaro validate` names the
		// same row by, because the reader's fix is the row's own text.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: this repository's security.patterns[%d] writes a security row named %s (%s), which is not in force: %s; a row the matcher refuses is one a run's load refuses, so nothing here matches it and fugaro validate says the same where it is written\n",
			r.Index, cellOneLine(r.Row.Name), cellOneLine(r.Row.Match), r.Reason)
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
// `.` is the one spelling the matcher's own rule accepts and that names no path
// in a checkout, and it is refused here rather than by that rule: cleanRelative
// compares the path with path.Clean's answer, and path.Clean leaves a bare `.`
// exactly as it is, so it passes while every other spelling that holds a `.` or
// `..` component — `./x`, `docs/..`, `a/./b` — is rewritten and refused, and
// every spelling that escapes the checkout, `..` included, is refused by that
// rule itself. `.` names no file of the repository (it is the repository root
// as a directory reference), so a list can speak about it neither, and
// answering `sensitive: false` for it would be the one answer D1512 keeps out of
// this field. Every spelling with a component to its name is left to the
// matcher: `.gitignore` and `...` are ordinary file names and are placed.
func securityPlaceable(path string) bool {
	if path == "." {
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
func securityAnswer(path string, list config.SecurityList) securityPathAnswer {
	answer := securityPathAnswer{Path: path}
	matched := sensitivity.Match([]string{path}, list.Patterns())
	if len(matched) > 0 {
		answer.Matched = &securityMatch{
			Name:     matched[0].Name,
			Category: string(matched[0].Category),
			Source:   securityRowSource(list, matched[0].Name),
		}
	}
	answer.Sensitive = answer.Matched != nil || !securityPlaceable(path)
	return answer
}

// securityRowSource is the layer the matched row is in force from. A name is a
// row's identity in the list in force (sensitivity.Union), so the row Match
// answered with is the one row of that list carrying that name, and a caller
// that wants the layer the answer came from wants the one the row carries.
func securityRowSource(list config.SecurityList, name string) string {
	for _, row := range list {
		if row.Pattern.Name == name {
			return string(row.Source)
		}
	}
	return string(config.SecuritySourceFloor)
}

// securityBaseFile is what the repository's own fugaro.yaml says at ref: the
// project and the GCP project it names — the two keys a project layer is
// anchored to (config.LayerAnchor) — and the security: block it writes, plus
// the reason the read did not answer ("" when it did). The last return is the
// document's `error`: a ref this checkout does not hold, no fugaro.yaml at it, a
// file this reader cannot read as one, or a block of it no run holds
// (securityMatchable). Both texts name a ref or a line and never a value read
// out of the file. It is one read of one file, because the answers are all
// about the same file's bytes and a second read could only disagree with the
// first — and a caller that has to decide whether a project layer applies wants
// both keys out of the bytes the layer is being tested against, never out of a
// differently resolved file.
//
// The rows are the ones a reader may match: the fourth return is the rows of the
// block sensitivity.Pattern.Validate refuses, which are dropped from the third
// (securityMatchable), so a caller that matches the block it is handed cannot be
// made to spend what a row's own text costs.
//
// A file that writes no security: key answers with a nil block, which is the
// same list as an empty one: the resolution reads a nil half as "this layer
// wrote nothing" (config.SecurityPatterns, D1504).
func securityBaseFile(ctx context.Context, root, ref string) (string, string, *config.Security, []securityRefusedRow, string) {
	data, err := readFugaroYAMLAtRef(ctx, root, ref)
	if err != nil {
		return "", "", nil, nil, err.Error()
	}
	project, err := config.ProjectOf(data)
	if err != nil {
		return "", "", nil, nil, fmt.Sprintf("fugaro.yaml at %s: %v", ref, err)
	}
	gcpProject, err := config.GCPProjectOf(data)
	if err != nil {
		return "", "", nil, nil, fmt.Sprintf("fugaro.yaml at %s: %v", ref, err)
	}
	own, ok := config.SecurityOf(data)
	if !ok {
		return "", "", nil, nil, fmt.Sprintf("fugaro.yaml at %s: this reader cannot read the file as a fugaro.yaml", ref)
	}
	own, refused, problem := securityMatchable(own)
	if problem != "" {
		return "", "", nil, nil, fmt.Sprintf("fugaro.yaml at %s: %s", ref, problem)
	}
	return project, gcpProject, own, refused, ""
}

// securityRefusedRow is one row of the repository's own security block that no
// reader may match: the row, the index the file wrote it at — the index
// internal/config's own problems name the same row by, so this warning and
// `fugaro validate` point at the same row of the same block — and the reason
// sensitivity.Pattern.Validate gives.
type securityRefusedRow struct {
	Index  int
	Row    sensitivity.Pattern
	Reason string
}

// securityMatchable measures the repository's own block before a reader matches
// it, and is the one place that read is bounded.
//
// sensitivity.Match's cost is the row's own: doublestar matches most globs in
// work proportional to the path, and the one shape that is not — `{a,b}`
// alternation, where every group re-enters the matcher with the alternative
// spliced in — is refused by Pattern.Validate (hasAlternation) so that no row
// can cost minutes. That refusal, and the two bounds around it (MaxPatternMatch
// on a row's text, MaxPatterns on a layer's list), are what Match's own note
// rests on: "every row a caller matches has passed that check". A run's lists
// reach the matcher that way — the runner resolves its files through
// internal/config, whose rules are these — but this reader's repository half is
// a file's own bytes read at a ref (config.SecurityOf, D1518), which no run has
// measured. Matching those as written would hang the two commands a person runs
// to diagnose a security block, which is the opposite of what they are for: so
// the rules a run's load applies are applied here, to the same rows, by the same
// function, and what is dropped is said rather than silently passed over.
//
// A row Validate refuses is dropped. A block that writes more rows than a layer
// may hold is refused whole and its problem returned, because there is no
// per-row answer to a count: the caller answers with the floor (and the project
// layer) alone and carries the reason in the document's `error`. The floor's
// rows and the project layer's are never measured here — the floor is the
// engine's own, and a published layer is validated where it is published
// (internal/projectpolicy).
func securityMatchable(own *config.Security) (*config.Security, []securityRefusedRow, string) {
	if own == nil {
		return nil, nil, ""
	}
	if len(own.Patterns) > sensitivity.MaxPatterns {
		return nil, nil, fmt.Sprintf("security.patterns writes %d rows and a layer may hold %d: no run holds a block that long, so no row of it is read", len(own.Patterns), sensitivity.MaxPatterns)
	}
	kept := make([]sensitivity.Pattern, 0, len(own.Patterns))
	var refused []securityRefusedRow
	for i, row := range own.Patterns {
		if reason := row.Validate(); reason != "" {
			refused = append(refused, securityRefusedRow{Index: i, Row: row, Reason: reason})
			continue
		}
		kept = append(kept, row)
	}
	if len(refused) == 0 {
		return own, nil, ""
	}
	return &config.Security{Patterns: kept}, refused, ""
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
// with its layers, or one answer per checked path. A read that did not answer,
// and a project layer that is not in force for this file — no cached copy of it,
// or one the file's own project: and gcp_project: do not name — are said on
// stderr by runSecurityPaths rather than here, for both forms of the document,
// since none of them is something a table can carry and a caller piping stdout
// should be told what it is missing.
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
	return nil
}
