package cli

// `fugaro project repos ls`: the enrolment view (repos_view.go) rendered, one
// row per repository, with the versioned document D2410 gives the console.
//
// It lives in a file of its own because the command stopped being a rendering
// of the published node alone: the rows are the view's answer — this machine's
// config's repositories and the concrete names the allow-list admits, each with
// its image, its workflows, its Claude accounts and both halves of its
// enrolment — and this file adds no read of its own to that answer (D2409).
//
// The seen list stays a section of its own and is never merged into the rows
// (D2409): it is a reading of what has RUN here, and a repository seen running
// that the allow-list no longer admits would read as enrolled, while one
// admitted with no run would read as unenrolled.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// reposLsDoc is the `ls --json` document (D2410): the view's members at the top
// level, in the order repos_view.go tags them, and the version this document
// owns.
//
// The version belongs to the document and not to the view: `status --json`
// prints the same view under a version of its own (reposStatusDocVersion), and
// one view printed under two documents would have to carry two. The embedding is
// what makes the two the same shape rather than two spellings of it — the JSON
// tags in repos_view.go are written for this flattening — so a member the view
// gains arrives in this document without an edit here, and a member this
// document must not carry is a member the view must not have.
type reposLsDoc struct {
	Version int `json:"version"`
	*reposView
}

// runProjectReposLs is `fugaro project repos ls`.
//
// The gate is the caller's and the read is the view's (D2414): the group's own
// read gate is taken after the cloud open and before anything is read, the way
// budget.go's project-level read takes it.
func runProjectReposLs(cmd *cobra.Command, o *projectReposOptions, asJSON bool) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloud)
	if err != nil {
		return err
	}
	if err := accessGateProjectRead(ctx, lc, db, "the project's repositories are a project-level read, so they are not listed", "listing them"); err != nil {
		return err
	}
	v, err := newReposView(ctx, lc, db, "")
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	// A read of what has run here that did not answer is an error in this
	// command, in both output forms, and never a state the listing carries: the
	// seen list is drawn as the whole of what has run here, and a listing of the
	// part of that read which answered is a shorter history drawn as the
	// complete one (the fail-closed rule docs/project-layer.md §7 states for
	// this listing). Nothing is printed before it — a listing that printed half
	// of itself and then refused would read as one that had finished — and
	// `status` is where a caller reads the state instead, because its rows are
	// diagnoses of one repository rather than a listing of all of them.
	if v.Seen.State != "" {
		return remote(errors.New(v.Seen.State))
	}
	if asJSON {
		return writeJSON(out, reposLsDoc{Version: reposLsDocVersion, reposView: v})
	}
	// Where each allow-list entry came from is this machine's config's answer
	// and not the view's: it is a label of the text rendering, and the document
	// D2410 names carries the entries with the generation they were read at and
	// no source beside them.
	sources, err := allowSources(ctx, lc)
	if err != nil {
		return err
	}
	printReposLs(out, v, sources)
	return nil
}

// reposLsRowsHeader introduces the rows. It names what a row is drawn from — a
// repository this machine knows or a concrete name the allow-list admits — and
// never that the repository has run: what has run here is the section below,
// with a header of its own (D2409).
const reposLsRowsHeader = "repositories (one row per repository this machine's config holds and the concrete names the allow-list admits):"

// printReposLs is the text form: the rows, the published allow-list, and the
// repositories that have run here in a section of their own.
//
// The seen half is rendered exactly as it was before the rows existed — the same
// header, the same bounded printable names and the same note per entry — because
// a name on it was written by a run and not by this program: a runs/<slug>/
// prefix is a directory name in the runs bucket and spendDaily's slug is a field
// of a document the rollup writes, and neither path checks the shape of the
// name. The admission the note asks about is the view's allow-list, so this
// listing reads the node once.
func printReposLs(out io.Writer, v *reposView, sources map[string]string) {
	fmt.Fprintln(out, reposLsRowsHeader)
	if v.Local.State != "" {
		fmt.Fprintf(out, "  note: %s\n", v.Local.State)
	}
	if len(v.Repos) == 0 {
		fmt.Fprintln(out, "  (none: this machine's config holds no repository, and no concrete name admits one)")
	}
	for _, r := range v.Repos {
		fmt.Fprintln(out, reposLsRowLine(r))
	}
	fmt.Fprintf(out, "allow_repos (%s):\n", projectpolicy.PathShared())
	switch {
	case v.Allow.State != "":
		// The state the node's own read left, and never the empty-list line
		// below it: "nobody could read the list" and "a published policy admits
		// nothing" are different facts about the project, and only the second
		// one is a list (reposViewAllow's own sentence).
		fmt.Fprintf(out, "  %s\n", v.Allow.State)
	case len(v.Allow.Entries) == 0:
		fmt.Fprintln(out, "  (empty: a published policy with an empty allow-list admits no repository at all)")
	default:
		for _, e := range v.Allow.Entries {
			fmt.Fprintf(out, "  %s%s\n", e, sourceSuffix(sources[e]))
		}
	}
	fmt.Fprintln(out, seenHeader)
	for _, n := range v.Seen.Notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
	if len(v.Seen.Entries) == 0 {
		fmt.Fprintln(out, "  (none: no run of any repository has been recorded here)")
	}
	// The listing is bounded twice, and both bounds are needed. One entry is
	// printableName (bounded to one inert line, and distinct from every other
	// name, so the entry the allow-list refuses cannot print as the one it
	// admits): every value on this list was written by a run, not by this
	// program — a runs/<slug>/ prefix is a directory name in the runs bucket
	// and spendDaily's slug is a field of a document the rollup writes, and
	// neither path checks the shape of the name. Raw, an escape sequence in
	// one rewrites the operator's screen, a newline adds a line that reads as
	// another entry, and a long one pushes the note off the column. And the
	// count is bounded (safetext.FieldListMax) because the list is as long as
	// whoever wrote those names made it: a listing that grew with its input
	// would push this command's own words — the header, the sources, the way
	// to enroll — off the screen. What is left out is counted in this
	// program's own words (safetext.FieldListNote), so a listing that stopped
	// at twenty does not read as a list of twenty. The admission question
	// above is asked of the name itself; only what is printed is bounded.
	for i, e := range v.Seen.Entries {
		if i == safetext.FieldListMax {
			fmt.Fprintf(out, "  (%s)\n", safetext.FieldListNote(len(v.Seen.Entries)-i))
			break
		}
		r := seenRepo{Name: e.Name, Provider: e.Provider, Sources: e.Sources}
		fmt.Fprintf(out, "  %s%s\n", printableName(r.Name), seenNote(r, reposViewPublishedOf(v.Allow, r.Name).Admitted))
	}
}

// reposLsRowLine is one repository's row: the columns of D2409, in the order a
// person asks them — what the repository is, then what its job runs, then both
// halves of its enrolment.
//
// Both halves are printed as their own cell and never folded into one word: a
// repository the allow-list admits that this machine has never configured is a
// different state from one configured here that the list does not admit — the
// first runs, the second does not — and "enrolled" would draw them as one
// figure. The published cell names the entry that admits it, which is the
// repository's own name where the list names it and the wildcard otherwise, so
// an operator reading a row can tell which of the two admitted it.
func reposLsRowLine(r reposViewRepo) string {
	return "  " + printableName(r.Canonical) +
		"  [" + safetext.Field(r.Provider) + "]" +
		"  base " + safetext.Field(r.BaseBranch) +
		"  " + reposLsImageCell(r) +
		"  workflows: " + reposLsListCell(reposLsWorkflowNames(r.Workflows)) +
		"  claude accounts: " + reposLsListCell(r.ClaudeAccounts) +
		"  published: " + reposLsPublishedCell(r.Published) +
		"  local: " + reposLsLocalCell(r.Local)
}

// reposLsImageCell is a row's image cell: what its job runs and which of the
// seven answers chose it.
//
// It is written from the source and never from the entry alone, because the
// three sources whose image is not a catalog entry's — `repo`, `base` and
// `shared` — have no entry to name: catalog.Selection.Entry is the zero value
// whenever its Source is not SourceCatalog (catalog/select.go), so the row's
// ImageEntry is "" there too, and reading that "" as the resolver's refusal
// would draw the ordinary case — the image this project builds for the
// repository — as a row whose image nothing resolved. The `""` is a fact about
// the job and not a read that did not answer (D2410, and the console contract's
// §6.26 states the member that way); ImageSource is what tells the two apart,
// and it is what this cell reads.
func reposLsImageCell(r reposViewRepo) string {
	switch r.ImageSource {
	case reposViewImageRepo:
		return "image this project builds for the repository (repo)"
	case reposViewImageBase:
		return "image the installation ships as its base (base)"
	case reposViewImageShared:
		return "image the shared dependency layer (shared)"
	case reposViewImageUnresolved:
		return "image unresolved (no answer chose one)"
	}
	return fmt.Sprintf("image %s (%s)", safetext.Field(r.ImageEntry), r.ImageSource)
}

// reposLsWorkflowNames is a row's workflow names, in the view's own order.
func reposLsWorkflowNames(ws []reposViewWorkflow) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, safetext.Field(w.Name))
	}
	return out
}

// reposLsListCell is one cell of a row that holds a list: the values as the
// config spells them, and this program's own word for none, so an empty list is
// not a column a reader has to notice is empty.
func reposLsListCell(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

// reposLsPublishedCell is a row's published half: the entry that admits it, or
// the word for an allow-list that does not.
func reposLsPublishedCell(p reposViewRepoPublished) string {
	if !p.Admitted {
		return "not admitted"
	}
	return printableName(p.Pattern)
}

// reposLsLocalCell is a row's local half: the key this machine's config holds
// the repository under — the operator's own spelling, which is what an operator
// greps their config for — or the word for a machine that does not hold it.
func reposLsLocalCell(l reposViewRepoLocal) string {
	if !l.Present {
		return "not in this machine's config"
	}
	return "repos: " + printableName(l.Key)
}
