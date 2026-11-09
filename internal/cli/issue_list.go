package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newIssueListCmd builds `fugaro issue list`: what this machine saved, one row
// per issue.
func newIssueListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the issues saved on this machine",
		Long: "List the issues saved on this machine, one row each.\n\n" +
			"A row names the issue the way `show`, `dismiss` and `submit` take it —\n" +
			"its number once it has been uploaded, its local ID before — with the\n" +
			"class the diagnosis gave it, where it stands, and its one-line summary.\n" +
			"Nothing is uploaded and nothing is filed: the listing is this machine's\n" +
			"own folder, read.\n\n" +
			"--json prints the records themselves, in the shape\n" +
			"schemas/issue-record.schema.json describes, which is the document a\n" +
			"reader outside this binary consumes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runIssueList(cmd, asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runIssueList(cmd *cobra.Command, asJSON bool) error {
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	all, err := store.List()
	if err != nil {
		return userErr("nothing was listed: %v", err)
	}
	out := cmd.OutOrStdout()
	if asJSON {
		docs := make([]map[string]any, 0, len(all))
		for _, i := range all {
			doc, err := issueJSON(i)
			if err != nil {
				return userErr("nothing was listed: %v", err)
			}
			docs = append(docs, doc)
		}
		return writeIssueJSON(out, docs)
	}
	// A machine that saved nothing prints nothing: a header with no rows under
	// it reads like a listing that was cut short, and there is no command here
	// that hides a row the way `list --mine` will.
	if len(all) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ISSUE\tCLASS\tSTATUS\tSUMMARY")
	for _, i := range all {
		// Every cell comes from a file this machine's store holds, and the
		// summary is the diagnosis' own words about log text the user does not
		// control, so each one goes through the terminal pass (F6).
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			cellOneLine(issueRef(i)), cellOneLine(string(i.Class)),
			cellOneLine(string(i.Status)), cellOneLine(i.Summary))
	}
	return tw.Flush()
}
