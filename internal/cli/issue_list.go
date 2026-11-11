package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/issues"
)

// newIssueListCmd builds `fugaro issue list`: the issues the project holds, one
// row per issue — or, with --mine, the ones this user reported.
func newIssueListCmd() *cobra.Command {
	var asJSON, mine, all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's issues, and the ones this machine has not uploaded yet",
		Long: "List the issues the project holds, one row each.\n\n" +
			"An issue is the project's, not one machine's (spec 4b), so the rows are\n" +
			"the whole team's: the ones this machine saved and has not uploaded yet,\n" +
			"named by their local ID, and every issue of the project's store, named\n" +
			"by its number. A row names the issue the way `show`, `dismiss` and\n" +
			"`submit` take it, with the class the diagnosis gave it, where it stands,\n" +
			"and its one-line summary. Where the project's copy and this machine's\n" +
			"file disagree, the project's is printed: the store is the truth once an\n" +
			"issue is uploaded (F13).\n\n" +
			"Listing uploads this machine's own pending issues first, exactly as\n" +
			"`mine` does: the same failure reported twice is one issue, and the one\n" +
			"still waiting here has to reach the store before it can be shown beside\n" +
			"the team's. A run that cannot reach the store lists this machine's own\n" +
			"records alone and says why on stderr, which leaves the table itself\n" +
			"clean for a reader and a script.\n\n" +
			"--mine lists the issues this user reported to the project instead: the\n" +
			"same rows `fugaro issue mine` prints, from the same read, so the two\n" +
			"commands answer with one listing (F12). --all goes with --mine and only\n" +
			"with it: this machine's own folder keeps every record until it is\n" +
			"deleted, and the 30-day rule is about the project's issues, not about\n" +
			"the files here.\n\n" +
			"Nothing is uploaded in a coding agent's session: the rows are this\n" +
			"machine's own records and the project is not read (F10, D1734).\n\n" +
			"--json prints the records themselves, in the shape\n" +
			"schemas/issue-record.schema.json describes — or, with --mine, the\n" +
			"document schemas/issue-mine.schema.json describes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if mine {
				return runIssueMine(cmd, asJSON, all)
			}
			if all {
				return userErr("--all goes with --mine: this machine's own records are listed whole, and only the project's issues are hidden by age")
			}
			return runIssueList(cmd, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	cmd.Flags().BoolVar(&mine, "mine", false, "list the issues this user reported to the project")
	cmd.Flags().BoolVar(&all, "all", false, "with --mine: show the issues finished more than 30 days ago")
	return cmd
}

func runIssueList(cmd *cobra.Command, asJSON bool) error {
	ctx := cmd.Context()
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	agent := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	sh := openIssueShared(ctx, agent)
	defer sh.Close()
	if sh.installation != "" {
		store.Installation = sh.installation
	}
	sh.uploadPending(ctx, store)
	var shared []issues.SharedRow
	if sh.bucket != nil {
		if shared, err = issues.ListShared(ctx, sh.bucket); err != nil {
			sh.notes = append(sh.notes, "note: the project's issues could not be read ("+oneLine(err.Error())+")")
			shared = nil
		}
	}
	all, err := store.List()
	if err != nil {
		return userErr("nothing was listed: %v", err)
	}
	// The lines about what could not be reached go to stderr: stdout is the
	// table, and a note inside it would read as a row.
	for _, n := range sh.notes {
		fmt.Fprintln(cmd.ErrOrStderr(), n)
	}
	rows := issueRows(all, shared)
	out := cmd.OutOrStdout()
	if asJSON {
		docs := make([]map[string]any, 0, len(rows))
		for _, i := range rows {
			doc, err := issueJSON(i)
			if err != nil {
				return userErr("nothing was listed: %v", err)
			}
			docs = append(docs, doc)
		}
		return writeJSON(out, docs)
	}
	// A machine with nothing saved and no project store prints nothing: a header
	// with no rows under it reads like a listing that was cut short.
	if len(rows) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ISSUE\tCLASS\tSTATUS\tSUMMARY")
	for _, i := range rows {
		// Every cell comes from a file this machine's store holds or from the
		// project's store, and the summary is the diagnosis' own words about log
		// text the user does not control, so each one goes through the terminal
		// pass (F6).
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			cellOneLine(issueRef(i)), cellOneLine(string(i.Class)),
			cellOneLine(string(i.Status)), cellOneLine(i.Summary))
	}
	return tw.Flush()
}

// issueRows is the listing: this machine's own issues and the project's, one
// row per issue (F13). A number is the identity of an issue once it has one,
// and the project's copy of it is the truth — a teammate's dismissal, a resolve
// the update pass recorded — so where both sides have a record of one number the
// store's is the row. A record only this machine's folder has is still a row:
// the project's listing is what it is, and dropping a local file would hide the
// issue its owner is waiting on.
//
// The order is the store's own: the issues with no number yet — this machine's,
// the ones a person has to look at — first, then the numbered ones ascending.
func issueRows(local []issues.Issue, shared []issues.SharedRow) []issues.Issue {
	var unnumbered []issues.Issue
	numbered := make(map[int]issues.Issue, len(local)+len(shared))
	for _, i := range local {
		if i.Number == 0 {
			unnumbered = append(unnumbered, i)
			continue
		}
		numbered[i.Number] = i
	}
	for _, r := range shared {
		numbered[r.Number] = r.Issue
	}
	numbers := make([]int, 0, len(numbered))
	for n := range numbered {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	out := unnumbered
	for _, n := range numbers {
		out = append(out, numbered[n])
	}
	return out
}
