package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backlog"
)

// backlogOptions are the two flags the pair shares: which folder to read, and
// whether to print the document instead of the index.
type backlogOptions struct {
	dir  string // the folder the items live in, relative to the working directory
	json bool   // print the machine-readable document instead of the index
}

// newBacklogCmd builds `fugaro backlog` (rigor modes, the owner's ruling of
// 2026-10-20: D26–D32, plan T4.3 and T4.4). The folder is docs/backlog/: one
// Markdown file per item, named `<id>-<slug>.md`, and one generated index
// beside them, README.md.
//
// The pair is read-only in the sense that matters for a review: nothing here
// touches a network, a credential, a cloud project or an installation. `index`
// writes exactly one file, the folder's own index, from the item files beside
// it; `list` writes nothing at all and prints what the index would say.
//
// There is no `add` and no `close`: filing is a run's step and closing is the
// fixing pull request's step (D31), and neither is a command a person needs a
// tool for. `index` exists because the index is derived state — it is
// regenerated, never merged by hand (D28) — and `list` exists because a reader
// who wants the backlog on a terminal should not have to open a file.
func newBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Read the repository's backlog and regenerate its index",
		Long: "Read the backlog in docs/backlog/ and regenerate its index.\n\n" +
			"The backlog is one Markdown file per item, named `<id>-<slug>.md`, beside\n" +
			"the generated index README.md and the hand-written format FORMAT.md. The\n" +
			"naming rule is what lets two pull requests file an item at once: they write\n" +
			"two differently named files, and the merge is a union of disjoint files\n" +
			"rather than a conflict (D26). The index is derived state — regenerated,\n" +
			"never merged by hand — so two branches that regenerated it at once are\n" +
			"reconciled by re-running `fugaro backlog index`, and never by merging the\n" +
			"file (D28).\n\n" +
			"Nothing here reaches a network, a credential or an installation: the items\n" +
			"and the index are files in this checkout.",
	}
	cmd.AddCommand(newBacklogIndexCmd(), newBacklogListCmd())
	return cmd
}

// newBacklogIndexCmd builds `fugaro backlog index`: it writes the folder's
// README.md from the item files beside it.
func newBacklogIndexCmd() *cobra.Command {
	var o backlogOptions
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Regenerate docs/backlog/README.md from the item files beside it",
		Long: "Regenerate the backlog's index from the item files.\n\n" +
			"The index lists one row per item — the id linked to its file, the title,\n" +
			"the mode, the area, the status, who filed it and the date — sorted by id,\n" +
			"with the closed and dropped rows grouped under the open ones, under a\n" +
			"count line (D28). It is written to <dir>/README.md, and it is the only\n" +
			"file this command touches.\n\n" +
			"The document is a pure function of the item files: every date in it comes\n" +
			"from the item that carries it and no clock is read, so regenerating an\n" +
			"unchanged folder rewrites the same bytes and a regeneration in review\n" +
			"diffs only where an item changed (D27). A pull request that adds an item\n" +
			"runs this in the same commit, and a test in internal/cli fails on an index\n" +
			"that is not current — which is why the file is never merged by hand.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBacklogIndex(cmd, &o) },
	}
	addBacklogFlags(cmd, &o)
	return cmd
}

// newBacklogListCmd builds `fugaro backlog list`: it prints what the index
// would say, and writes nothing.
func newBacklogListCmd() *cobra.Command {
	var o backlogOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print the backlog's items as the index lists them",
		Long: "Print the backlog's items.\n\n" +
			"The output is the index itself, rendered from the item files rather than\n" +
			"read back from README.md: a stale index on disk cannot change what this\n" +
			"prints, and a reader who sees a difference between the two has found the\n" +
			"file that needs `fugaro backlog index`.\n\n" +
			"--json prints one document instead: {dir, items[{id, title, mode, area,\n" +
			"status, filed_by, filed_in, date, path}], open, closed, dropped}, indented.\n" +
			"The rows are the index's own rows in the index's own order, and the three\n" +
			"counts are the count line's own figures, so a screen shows what the\n" +
			"Markdown says rather than a second reading of the folder.\n\n" +
			"Nothing is written, and nothing is reached over the network: a folder that\n" +
			"does not exist is an error rather than an empty backlog.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBacklogList(cmd, &o) },
	}
	addBacklogFlags(cmd, &o)
	f := cmd.Flags()
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	return cmd
}

// addBacklogFlags adds the flags both commands share. --dir is the folder, not
// the repository root, so the commands work wherever they are run from and a
// test can point them at a folder of its own.
func addBacklogFlags(cmd *cobra.Command, o *backlogOptions) {
	cmd.Flags().StringVar(&o.dir, "dir", backlog.Dir, "the folder the item files live in")
}

// runBacklogIndex writes the index and says which file it wrote.
func runBacklogIndex(cmd *cobra.Command, o *backlogOptions) error {
	items, err := backlog.Load(o.dir)
	if err != nil {
		return userErr("%v", err)
	}
	path := filepath.Join(o.dir, backlog.IndexName)
	if err := os.WriteFile(path, []byte(backlog.Render(items)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	return nil
}

// runBacklogList prints the index, or the document --json asks for.
func runBacklogList(cmd *cobra.Command, o *backlogOptions) error {
	items, err := backlog.Load(o.dir)
	if err != nil {
		return userErr("%v", err)
	}
	if !o.json {
		_, err := fmt.Fprint(cmd.OutOrStdout(), backlog.Render(items))
		return err
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(backlogDocument(items, o.dir))
}

// backlogListDoc is `fugaro backlog list --json`: the index's rows as data,
// in the index's order. The console's JSON contract describes it in its
// backlog section, and reads this file's symbols rather than a line number.
type backlogListDoc struct {
	Dir     string       `json:"dir"`
	Items   []backlogRow `json:"items"`
	Open    int          `json:"open"`
	Closed  int          `json:"closed"`
	Dropped int          `json:"dropped"`
}

// backlogRow is one item as the index lists it. The names are the index's own
// columns, so a screen and the Markdown cannot disagree about what a row says.
type backlogRow struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Mode    string `json:"mode"`
	Area    string `json:"area"`
	Status  string `json:"status"`
	FiledBy string `json:"filed_by"`
	FiledIn string `json:"filed_in"`
	Date    string `json:"date"`
	Path    string `json:"path"`
}

// backlogDocument assembles the document: the rows sorted by id — the index's
// order, which is the date order an id carries — and the count line's three
// figures.
func backlogDocument(items []backlog.Item, dir string) backlogListDoc {
	sorted := make([]backlog.Item, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	doc := backlogListDoc{Dir: dir, Items: make([]backlogRow, 0, len(sorted))}
	for _, it := range sorted {
		switch it.Status {
		case "open":
			doc.Open++
		case "closed":
			doc.Closed++
		case "dropped":
			doc.Dropped++
		}
		doc.Items = append(doc.Items, backlogRow{
			ID: it.ID, Title: it.Title, Mode: it.Mode, Area: it.Area,
			Status: it.Status, FiledBy: it.FiledBy, FiledIn: it.FiledIn,
			Date: it.Date, Path: it.Path,
		})
	}
	return doc
}
