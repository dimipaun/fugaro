package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
			"An issue that has been uploaded is printed as the project's store has\n" +
			"it: the store is the truth once an issue has a number (F13), so a\n" +
			"teammate's dismissal, or a fix the update pass recorded, shows here\n" +
			"rather than the copy this machine wrote when the failure happened. A\n" +
			"number also names a teammate's issue, which this machine has no file\n" +
			"of: the rows `list` prints are the whole team's, and a reference that\n" +
			"is a number is read from the project's store (F23). This machine's own\n" +
			"pending issues are uploaded first, as `list` does. A run that cannot\n" +
			"reach the store prints this machine's own copy and says why on stderr\n" +
			"— or, for a number it has no file of, refuses.\n\n" +
			"Nothing is uploaded in a coding agent's session, and the project is not\n" +
			"read (F10, D1734).\n\n" +
			"--json prints the record itself, in the shape\n" +
			"schemas/issue-record.schema.json describes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runIssueShow(cmd, args[0], asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runIssueShow(cmd *cobra.Command, ref string, asJSON bool) error {
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
	i, err := issueRecordForShow(ctx, sh, store, issueRefArg(ref))
	if err != nil {
		return userErr("nothing was shown: %v", err)
	}
	// The lines about what could not be reached go to stderr: stdout is the
	// record, and a note inside it would read as one of its fields.
	for _, n := range sh.notes {
		fmt.Fprintln(cmd.ErrOrStderr(), n)
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

// issueRecordForShow is the record `show` prints for a reference: this
// machine's own file, refreshed from the project's store when it has a number
// (F13) — and, for a number this machine has no file of, the project's record
// itself (F23). The second half is what makes the rows of `list` names a person
// can act on: a teammate's issue has no file here, and the store is where it is
// read.
//
// A file here that carries the number for another project is passed over, with
// a line: the number is this project's, and the row a listing prints for it is
// this project's issue (F8b). The line is the refusal when the store has no
// record of the number either, because the only other record this machine has
// of it is the one that was passed over.
func issueRecordForShow(ctx context.Context, sh *issueShared, store *issues.Store, ref string) (issues.Issue, error) {
	i, err := store.Get(ref)
	if err == nil {
		if i.Number != 0 && issueOtherProjects(i, sh.installation) {
			err = errors.New(issueRef(i) + " here is another project's record")
		} else {
			return refreshIssue(ctx, sh, i), nil
		}
	}
	fromStore, ok := sharedIssueRef(ctx, sh, ref)
	if !ok {
		return issues.Issue{}, err
	}
	return fromStore, nil
}

// refreshIssue answers with the record to print for i: the project's copy when
// there is one to read, and this machine's own otherwise (F13).
//
// An issue with no number has never been uploaded, so there is nothing to
// refresh from and the file here is the only record of it. A store that cannot
// be read is the same answer with a line saying so: a person asking to see an
// issue gets the issue, never a refusal.
func refreshIssue(ctx context.Context, sh *issueShared, i issues.Issue) issues.Issue {
	if sh.bucket == nil || i.Number == 0 {
		return i
	}
	cur, err := issues.SharedIssue(ctx, sh.bucket, i.Number)
	if err != nil {
		sh.notes = append(sh.notes, "note: the project's copy of "+issueRef(i)+" could not be read ("+oneLine(err.Error())+"), so this machine's own record is printed")
		return i
	}
	return cur
}

// issueURLShape is the one shape a stored URL is printed in: the GitHub issue
// an uploader here files (F28). A record is a file on this machine and the
// shared store can be rewritten by any launcher, so a URL that is not this
// shape is a line that looks like a link to the issue and is not one, and it
// is dropped rather than printed. The repository is updatecheck.Repo's, so the
// shape and the slug the updater knows cannot drift apart.
var issueURLShape = regexp.MustCompile(`^https://github\.com/` + regexp.QuoteMeta(updatecheck.Repo) + `/issues/[0-9]+$`)

// issueURL is the record's stored URL as far as a person may be shown it: the
// GitHub issue an uploader here files, and nothing else (F28). A record is a
// file on this machine and the shared store can be rewritten by any launcher,
// so a URL of any other shape is a line that reads like a link to the issue and
// is not one; it is dropped rather than printed, the way an empty header value
// is.
func issueURL(raw string) string {
	if !issueURLShape.MatchString(raw) {
		return ""
	}
	return raw
}

// printIssue prints one record for a person to read: the header a reader needs
// to tell one issue from another and to find it again, then the body's
// sections. A header value and the body's text are the record's own — a
// teammate's machine wrote them and a diagnosis read log text the user does not
// control — so every one goes through the terminal pass and never through the
// terminal as itself (F6). An empty section is left out: the body's shape is
// the file's, and a person does not need three empty headings.
func printIssue(w io.Writer, i issues.Issue) error {
	// The stored URL is the one header value with a shape of its own, and it is
	// the one a reader would click (F28).
	i.URL = issueURL(i.URL)
	if _, err := fmt.Fprintln(w, oneLine(issueRef(i))); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{
		{"class", string(i.Class)},
		{"status", string(i.Status)},
		{"resolved_in", i.ResolvedIn},
		{"closed_as", string(i.ClosedAs)},
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
