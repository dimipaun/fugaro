package cli

// `fugaro backlog add` (rigor modes, D26, D27, D28, plan T4.5): filing an item
// by hand. The red step is that there is no such command, so an item the owner
// wants filed has no command that files it and no id it can be cited by.
//
// Every case drives the command over a folder it builds in a temporary
// directory: nothing here reaches a network, a credential or an installation,
// and the date is always given rather than read from a clock, so the bytes the
// command writes are the same on every machine.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backlog"
)

// addArgs is the least a hand-filed item needs: a title, why it is left open,
// and who filed it.
func addArgs(dir string, extra ...string) []string {
	return append([]string{
		"backlog", "add", "--dir", dir,
		"--title", "Review round 2 left 3 findings open",
		"--why", "the round's findings are worth addressing, not blocking",
		"--filed-by", "octocat",
		"--date", "2026-10-24",
	}, extra...)
}

// TestBacklogAddWritesAnItemAndItsIndex: `add` writes the item the format
// describes, allocates its id from the folder's own state, and regenerates the
// index in the same command — so the folder's regenerate-and-compare pin is
// green on the commit that files an item, by hand as much as by a run (D26,
// D28).
func TestBacklogAddWritesAnItemAndItsIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	out, _, err := execute(t, addArgs(dir)...)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "20261024-01-review-round-2-left-3-findings-open.md")
	if !strings.Contains(out, path) {
		t.Errorf("fugaro backlog add printed %q, want the file it wrote (%s)", out, path)
	}
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fugaro backlog add wrote no item: %v", err)
	}
	item, err := backlog.Parse(path, doc)
	if err != nil {
		t.Fatalf("the item it wrote is not one the folder accepts: %v\n%s", err, doc)
	}
	if item.Mode != "none" {
		t.Errorf("mode = %q, want none: an item filed by hand records that no run chose a mode (D27)", item.Mode)
	}
	if item.Status != "open" || item.FiledIn != "hand" {
		t.Errorf("front matter = %+v, want an open item filed by hand", item)
	}
	index, err := os.ReadFile(filepath.Join(dir, backlog.IndexName))
	if err != nil {
		t.Fatalf("fugaro backlog add wrote no index: %v", err)
	}
	if !strings.Contains(string(index), item.ID) {
		t.Errorf("the index does not list %s:\n%s", item.ID, index)
	}
}

// TestBacklogAddAllocatesTheNextIDAndRefusesADuplicate: a second item the same
// day takes the next sequence, and the same title and day again is refused —
// the two ways D26's naming rule can be asked to write one thing twice.
func TestBacklogAddAllocatesTheNextIDAndRefusesADuplicate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	if _, _, err := execute(t, addArgs(dir)...); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, addArgs(dir, "--title", "Hotfix left the migration unrun")...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "20261024-02-") {
		t.Errorf("the second filing printed %q, want the day's next sequence (D26)", out)
	}
	if _, _, err := execute(t, addArgs(dir)...); err == nil {
		t.Fatal("a second filing of the same title and day was accepted")
	}
}

// TestBacklogAddRefusesFilingWithoutWhatTheFormatRequires: the front matter's
// fields are required and the body's first two headings are too (D27), so the
// command refuses a filing it cannot render rather than writing a file the
// folder's pin would then fail on.
func TestBacklogAddRefusesFilingWithoutWhatTheFormatRequires(t *testing.T) {
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no title", []string{"--title", ""}, "title"},
		{"no why", []string{"--why", ""}, "## Why"},
		{"no filer", []string{"--filed-by", ""}, "filed-by"},
		{"a mode outside the vocabulary", []string{"--mode", "turbo"}, "mode"},
		{"a date that is not a day", []string{"--date", "today"}, "date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := execute(t, addArgs(dir, tc.args...)...)
			if err == nil {
				t.Fatal("the filing was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name %q: %v", tc.want, err)
			}
		})
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		t.Errorf("a refused filing wrote %d files into %s", len(entries), dir)
	}
}

// TestBacklogAddRecordsTheRunAndAreaItIsFiledAbout: an item filed by hand for a
// run in a repository records the mode that run resolved and the area it is
// about, so a reader meets the values the item is filed under without the config
// that produced them (D27).
func TestBacklogAddRecordsTheRunAndAreaItIsFiledAbout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	out, _, err := execute(t, addArgs(dir,
		"--mode", "startup", "--area", "payments",
		"--filed-in", "run:20261024-120000-abcd",
		"--done", "clear what the round found", "--link", "run `20261024-120000-abcd`")...)
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(strings.TrimPrefix(strings.Split(out, "\n")[0], "filed "))
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := backlog.Parse(path, doc)
	if err != nil {
		t.Fatal(err)
	}
	if item.Mode != "startup" || item.Area != "payments" || item.FiledIn != "run:20261024-120000-abcd" {
		t.Errorf("front matter = %+v, want the mode, the area and the run the filing named", item)
	}
}
