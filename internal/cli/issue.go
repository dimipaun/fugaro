package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/issues"
)

// newIssueCmd builds `fugaro issue`: the commands over the issues a diagnosis
// saved on this machine — the record and its local store are internal/issues
// (T-IS1).
//
// The folder is the user's own state directory, so nothing here reads a
// credential, a project or a bucket: `list`, `show` and `dismiss` are reads
// and one local write. The shared store, the upload and the filing on GitHub
// are later slices, which is why `dismiss` refuses in a coding agent's session
// even though it writes no cloud record (D1730): the dismissal it makes is
// what a steered agent would use to bury a real bug.
//
// The help names no command that saves an issue, because none is in this tree
// yet: the diagnosis that writes one is another plan's, and a reference that
// told a reader to run it would name a command they cannot type.
func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Read and dismiss the issues saved on this machine",
		Long: "Read the issues saved on this machine.\n\n" +
			"An issue is one Markdown file under the user's state directory, holding\n" +
			"the whole issue: a strict header and the diagnosis' own words. `list`\n" +
			"shows what is saved, `show` prints one, and `dismiss` sets one aside so\n" +
			"nothing is filed for it. An issue is named by its local ID\n" +
			"(`local-<run stem>`) until it has been uploaded, and by its number\n" +
			"(`#0007`) from then on; `show` and `dismiss` take either.\n\n" +
			"Nothing here is uploaded: the issue stays on this machine, and the text\n" +
			"of a record is printed as data — it comes from log text this machine did\n" +
			"not write.",
	}
	cmd.AddCommand(newIssueListCmd(), newIssueShowCmd(), newIssueDismissCmd())
	return cmd
}

// openIssueStore opens the issues folder the CLI keeps, under the state
// directory every other local file of this command family lives in
// (issues.OpenStore). It reads os.Getenv and runtime.GOOS, so the command and
// the store's own tests resolve one directory.
func openIssueStore() (*issues.Store, error) {
	store, err := issues.OpenStore(os.Getenv, runtime.GOOS)
	if err != nil {
		return nil, userErr("%v", err)
	}
	return store, nil
}

// issueRef is the name an issue is printed under: `#0007` once the shared
// store gave it a number, and its local ID `local-<run stem>` while it has
// none (D1723, F23). It is the name a listing's first column shows, so it is
// the name a reader copies; issueRefArg is what makes it an argument the
// commands take, and the two together are the round trip.
func issueRef(i issues.Issue) string {
	if i.Number == 0 {
		return i.LocalID
	}
	return fmt.Sprintf("#%04d", i.Number)
}

// issueRefArg is a reference as the store takes it: the `#` that marks a
// printed name as a number, taken off.
//
// The store's own reference is a bare number or a local ID (issues.parseRef),
// and `list` prints a number with the `#` that says which of the two it is. A
// name that is printed and then refused is a trap the listing sets, so `#0007`
// — what a reader copies out of the row — and `7` are one reference here. A
// local ID carries no `#` and is handed on unchanged.
func issueRefArg(ref string) string {
	return strings.TrimPrefix(ref, "#")
}

// issueJSON is one record as the `--json` documents of `issue list` and
// `issue show` carry it: the header the codec writes, plus the four body
// members schemas/issue-record.schema.json names (F23). The record's own
// `version` is the version of both documents and of the record file, so a
// reader tells one shape from another by it.
//
// The header is taken from the record's own YAML tags rather than from a
// second list of member names written here: the header, the schema and this
// document are one shape, and a field added to the record cannot go missing
// from the document without the schema test that pins the three together
// saying so.
func issueJSON(i issues.Issue) (map[string]any, error) {
	i.Version = issues.RecordVersion
	head, err := yaml.Marshal(i)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal(head, &doc); err != nil {
		return nil, err
	}
	doc["summary"], doc["diagnosis"], doc["steps"] = i.Summary, i.Diagnosis, i.Steps
	// An empty body is an empty array and not null: the schema types `logs` as
	// an array, and a reader outside this binary reads the same document.
	logs := i.Logs
	if logs == nil {
		logs = []string{}
	}
	doc["logs"] = logs
	return doc, nil
}
