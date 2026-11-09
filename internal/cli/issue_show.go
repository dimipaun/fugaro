package cli

import (
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// newIssueShowCmd builds `fugaro issue show`: one saved issue, whole.
func newIssueShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show REF",
		Short: "Print one issue: its number (#0007) or its local ID",
		Long: "Print one issue the machine saved.\n\n" +
			"REF is the issue's number — `7`, or the `#0007` a listing prints — or\n" +
			"`local-<run stem>`, which is what it is called until it has been\n" +
			"uploaded. The header a listing shows and the body a person reads are\n" +
			"printed as data: the text came from a diagnosis that read log text this\n" +
			"machine did not write, so no escape sequence in it reaches the terminal.\n\n" +
			"--json prints the record itself, in the shape\n" +
			"schemas/issue-record.schema.json describes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runIssueShow(cmd, args[0], asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runIssueShow(cmd *cobra.Command, ref string, asJSON bool) error {
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	i, err := store.Get(issueRefArg(ref))
	if err != nil {
		return userErr("nothing was shown: %v", err)
	}
	if asJSON {
		doc, err := issueJSON(i)
		if err != nil {
			return userErr("nothing was shown: %v", err)
		}
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	return printIssue(cmd.OutOrStdout(), i)
}

// issueURLShape is the one shape a stored URL is printed in: the GitHub issue
// an uploader here files (F28). A record is a file on this machine and the
// shared store can be rewritten by any launcher, so a URL that is not this
// shape is a line that looks like a link to the issue and is not one, and it
// is dropped rather than printed. The repository is updatecheck.Repo's, so the
// shape and the slug the updater knows cannot drift apart.
var issueURLShape = regexp.MustCompile(`^https://github\.com/` + regexp.QuoteMeta(updatecheck.Repo) + `/issues/[0-9]+$`)

// printIssue prints one record for a person to read: the header a reader needs
// to tell one issue from another and to find it again, then the body's
// sections. A header value and the body's text are the record's own — a
// teammate's machine wrote them and a diagnosis read log text the user does not
// control — so every one goes through the terminal pass and never through the
// terminal as itself (F6). An empty section is left out: the body's shape is
// the file's, and a person does not need three empty headings.
func printIssue(w io.Writer, i issues.Issue) error {
	// The stored URL is the one header value with a shape of its own, and it is
	// the one a reader would click: only a URL this project's uploader could
	// have written is shown (F28).
	if !issueURLShape.MatchString(i.URL) {
		i.URL = ""
	}
	if _, err := fmt.Fprintln(w, oneLine(issueRef(i))); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{
		{"class", string(i.Class)},
		{"status", string(i.Status)},
		{"run", i.Run},
		{"reporter", i.Reporter},
		{"created", i.Created.UTC().Format(time.RFC3339)},
		{"fugaro", i.FugaroVersion + " (" + i.OS + ")"},
		{"command", i.FailedCommand},
		{"url", i.URL},
	} {
		if f.value == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "%-10s %s\n", f.name+":", oneLine(f.value)); err != nil {
			return err
		}
	}
	for _, s := range []struct{ name, text string }{
		{"Summary", i.Summary},
		{"Diagnosis", i.Diagnosis},
		{"Steps", i.Steps},
	} {
		if s.text == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "\n%s\n%s\n", s.name, multiLine(s.text)); err != nil {
			return err
		}
	}
	if len(i.Logs) > 0 {
		if _, err := fmt.Fprintln(w, "\nLog"); err != nil {
			return err
		}
		for _, excerpt := range i.Logs {
			if _, err := fmt.Fprintln(w, multiLine(excerpt)); err != nil {
				return err
			}
		}
	}
	return nil
}
