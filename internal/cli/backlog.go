package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// backlogOptions are the two flags the pair shares: which folder to read, and
// whether to print the document instead of the index.
type backlogOptions struct {
	dir  string // the folder the items live in, relative to the working directory
	json bool   // print the machine-readable document instead of the index
}

// newBacklogCmd builds `fugaro backlog` (rigor modes, the owner's ruling of
// 2026-10-20: D26–D32, plan T4.3–T4.5). The folder is docs/backlog/: one
// Markdown file per item, named `<id>-<slug>.md`, and one generated index
// beside them, README.md.
//
// Nothing here touches a network, a credential, a cloud project or an
// installation: the items and the index are files in a checkout. `add` writes
// one item and regenerates the index beside it — the commit that files an item
// is the commit that regenerates the index (D28) — and `index` regenerates it
// alone; `list` writes nothing at all and prints what the index would say.
//
// There is no `close`: closing an item is the fixing pull request's step, which
// flips the status and regenerates the index in the same commit (D31), and a
// command that closed one would be a second way to do it. `index` exists because
// the index is derived state — regenerated, never merged by hand (D28) — and
// `list` exists because a reader who wants the backlog on a terminal should not
// have to open a file.
func newBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "File a backlog item, read the backlog, regenerate its index",
		Long: "Read the backlog in docs/backlog/, file an item into it, and regenerate\n" +
			"its index.\n\n" +
			"The backlog is one Markdown file per item, named `<id>-<slug>.md`, beside\n" +
			"the generated index README.md and the hand-written format FORMAT.md. The\n" +
			"naming rule is what lets two pull requests file an item at once: they write\n" +
			"two differently named files, and the merge is a union of disjoint files\n" +
			"rather than a conflict (D26). `add` writes one item under that rule and\n" +
			"refuses the one the folder already holds — the same title filed twice on\n" +
			"the same day. The index is derived state — regenerated,\n" +
			"never merged by hand — so two branches that regenerated it at once are\n" +
			"reconciled by re-running `fugaro backlog index`, and never by merging the\n" +
			"file (D28). `add` regenerates it in the same commit that files the item;\n" +
			"`index` regenerates it alone; `list` writes nothing and prints what the\n" +
			"index would say.\n\n" +
			"Nothing here reaches a network, a credential or an installation: the items\n" +
			"and the index are files in this checkout.",
	}
	cmd.AddCommand(newBacklogAddCmd(), newBacklogIndexCmd(), newBacklogListCmd())
	return cmd
}

// backlogAddOptions are `fugaro backlog add`'s flags: D27's eight fields, less
// the id and the status the folder sets itself.
type backlogAddOptions struct {
	backlogOptions
	title   string
	what    string
	why     string
	done    string
	links   []string
	mode    string
	area    string
	filedBy string
	filedIn string
	date    string
}

// newBacklogAddCmd builds `fugaro backlog add`: filing an item by hand.
//
// A run files its own items (D9, plan T4.5); this is the command a person uses
// for the items no run will file — a project-level one, a deferral the owner
// wrote down, anything the backlog's own practice asks for. What it writes is
// exactly what a run writes: D27's front matter, the id D26 allocates from the
// folder's own state, and the index regenerated beside it.
func newBacklogAddCmd() *cobra.Command {
	var o backlogAddOptions
	cmd := &cobra.Command{
		Use:   "add",
		Short: "File an item in docs/backlog/ and regenerate the index",
		Long: "File an item, and regenerate the index beside it.\n\n" +
			"The item takes the next id for the day it is filed (D26) and is written\n" +
			"as `<id>-<slug>.md`, with the eight front-matter fields the format\n" +
			"requires (D27). The index is regenerated in the same command, so the\n" +
			"commit that files an item is the commit that regenerates the index.\n\n" +
			"--title and --why are required, and so is --filed-by: an item is not a\n" +
			"title with no text, and who filed it is one of the eight fields. --what\n" +
			"defaults to the title, and --done and --link are written when given.\n\n" +
			"An item filed by hand records `mode: none` — no run chose a mode for it.\n" +
			"--mode names the mode a run resolved when the item is about one, and\n" +
			"--filed-in says where it came from: `hand`, `run:<run id>` or\n" +
			"`pr:<number>`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBacklogAdd(cmd, &o) },
	}
	f := cmd.Flags()
	addBacklogFlags(cmd, &o.backlogOptions)
	f.StringVar(&o.title, "title", "", "the item's title, one line (required)")
	f.StringVar(&o.what, "what", "", "what was left open; the title when empty")
	f.StringVar(&o.why, "why", "", "why it is left open (required)")
	f.StringVar(&o.done, "done", "", "what done looks like")
	f.StringArrayVar(&o.links, "link", nil, "a link for the item's Links section (repeatable)")
	f.StringVar(&o.mode, "mode", "none", "the mode a run resolved; none for an item filed by hand")
	f.StringVar(&o.area, "area", "repo", "the area the item is about: an area name, project or repo")
	f.StringVar(&o.filedBy, "filed-by", "", "who filed it: the run's launcher, or your login (required)")
	f.StringVar(&o.filedIn, "filed-in", "hand", "where it came from: hand, run:<run id> or pr:<number>")
	f.StringVar(&o.date, "date", "", "the day it is filed, as YYYY-MM-DD; today when empty")
	return cmd
}

// runBacklogAdd writes the item and says which file it wrote.
func runBacklogAdd(cmd *cobra.Command, o *backlogAddOptions) error {
	if o.date == "" {
		o.date = time.Now().UTC().Format("2006-01-02")
	}
	if err := backlogModeKnown(o.mode); err != nil {
		return userErr("%v", err)
	}
	what := o.what
	if strings.TrimSpace(what) == "" {
		what = o.title
	}
	item, err := backlog.Write(o.dir, backlog.New{
		Title: o.title, Mode: o.mode, Area: o.area, FiledBy: o.filedBy, FiledIn: o.filedIn,
		Date: o.date, Body: backlog.Body(what, o.why, o.done, o.links),
	})
	if err != nil {
		return userErr("%v", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "filed %s\nwrote %s\n", item.Path, filepath.Join(o.dir, backlog.IndexName))
	return nil
}

// backlogModeKnown holds --mode to the mode vocabulary plus `none`, which is
// what a hand-filed item records. A mode outside it is a typo a reader would
// find in the index months later, which is the one place a value nobody can
// check is worth refusing at the command that writes it (D27).
func backlogModeKnown(mode string) error {
	if mode == "none" || rigor.Known(rigor.Mode(mode)) {
		return nil
	}
	names := make([]string, 0, len(rigor.Modes)+1)
	names = append(names, "none")
	for _, m := range rigor.Modes {
		names = append(names, string(m))
	}
	return fmt.Errorf("mode %q is none of %s (D27)", mode, strings.Join(names, ", "))
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
