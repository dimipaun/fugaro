package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/issues"
)

// newIssueCmd builds `fugaro issue`: the commands over the issues
// `fugaro troubleshoot` saved on this machine — the record and its local store
// are internal/issues (T-IS1).
//
// The folder is the user's own state directory, so nothing here reads a
// credential, a project or a bucket: `list`, `show` and `dismiss` are reads
// and one local write. The shared store, the upload and the filing on GitHub
// are later slices, which is why `dismiss` refuses in a coding agent's session
// even though it writes no cloud record (D1730): the dismissal it makes is
// what a steered agent would use to bury a real bug.
func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Read, dismiss and file the issues fugaro troubleshoot saved",
		Long: "Read the issues `fugaro troubleshoot` saved on this machine.\n\n" +
			"An issue is one Markdown file under the user's state directory, holding\n" +
			"the whole issue: a strict header and the diagnosis' own words. `list`\n" +
			"shows what is saved, `show` prints one, and `dismiss` sets one aside so\n" +
			"nothing is filed for it. An issue is named by its local ID\n" +
			"(`local-<run stem>`) until it has been uploaded, and by its number\n" +
			"(`#0007`) from then on; `show` and `dismiss` take either.\n\n" +
			"Nothing here files anything: the issue stays on this machine until a\n" +
			"person runs the submit command, and the text of a record is printed as\n" +
			"data — it comes from log text this machine did not write.",
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
// none (D1723, F23). Both are the argument `show`, `dismiss` and `submit`
// take, so what a listing prints is what a reader types back.
func issueRef(i issues.Issue) string {
	if i.Number == 0 {
		return i.LocalID
	}
	return fmt.Sprintf("#%04d", i.Number)
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

// writeIssueJSON writes one document — an array of records for `list`, one
// record for `show` — the way the other `--json` outputs of this CLI write
// theirs.
func writeIssueJSON(w io.Writer, doc any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
