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
// An issue is the project's once it has a number, so the commands that name one
// read and write the project's store as well (F13): `sync` uploads this
// machine's pending records, `list` and `show` refresh from the store, and
// `dismiss` sets a numbered issue aside there. All of it is best effort — a run
// with no project, no credential or no network still answers with this
// machine's own files — and all of it is a cloud write, so a coding agent's
// session uploads nothing, reads the local files alone and is refused the two
// commands whose subject is the store (F10, D1734). `dismiss` refuses in a
// session even for a local-only issue (D1730): the dismissal it makes is what a
// steered agent would use to bury a real bug. `submit` builds the post from the
// record and files it with the person's own `gh`.
func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Read the issues saved on this machine, the ones you reported, and file one on GitHub",
		Long: "Read the issues saved on this machine.\n\n" +
			"An issue is one Markdown file under the user's state directory, holding\n" +
			"the whole issue: a strict header and the diagnosis' own words. `list`\n" +
			"shows what is saved, `show` prints one, `dismiss` sets one aside so\n" +
			"nothing is filed for it, `sync` uploads the ones this machine has not\n" +
			"uploaded yet, and `mine` reads the project's own store for the issues\n" +
			"this user reported. An issue is named by its local ID (`local-<run\n" +
			"stem>`) until it has been uploaded, and by its number (`#0007`) from\n" +
			"then on; `show`, `dismiss` and `submit` take either.\n\n" +
			"`submit` shows exactly what would be filed on GitHub — the post and the\n" +
			"sha256 of its bytes — and files nothing until `--ask` is answered with a\n" +
			"yes at your own terminal; without `gh`, it prints how to install it and\n" +
			"a new-issue link holding the same post instead.\n\n" +
			"An issue belongs to the project, not to one machine: once it has been\n" +
			"uploaded it is the project's store that says where it stands, so `list`\n" +
			"shows the whole team's issues and `show` prints the project's copy. To\n" +
			"get there, `sync` uploads this machine's own pending records to the\n" +
			"project's store, and `list`, `show` and `mine` — and `list --mine`,\n" +
			"which is the same read — do the same on the way to answering; a run\n" +
			"that cannot reach the store answers from the files here and says why.\n" +
			"Nothing is uploaded in a coding agent's session, and `sync` and\n" +
			"`dismiss` are refused there. Either way the text of a record is printed\n" +
			"as data: it comes from log text this machine did not write.",
	}
	cmd.AddCommand(newIssueListCmd(), newIssueShowCmd(), newIssueDismissCmd(), newIssueSyncCmd(), newIssueMineCmd(), newIssueSubmitCmd())
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
	return issueRefOfNumber(i.Number)
}

// issueRefOfNumber is that spelling for a number with no record in hand: the
// store's own reader answers with a number before there is a record to print,
// and a line about one is written in the same shape as a row.
func issueRefOfNumber(n int) string {
	return fmt.Sprintf("#%04d", n)
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
