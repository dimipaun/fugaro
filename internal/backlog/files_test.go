package backlog

// files_test.go is U8's half of the package's rules: LoadFiles reads a folder
// a code host answered as files, by the same rules Load reads one from disk.

import (
	"strings"
	"testing"
)

// second is another well-formed item, so a folder of two of them is one the
// assertions below can name by id.
func second(mutate func(*string)) string {
	return item(func(d *string) {
		*d = strings.Replace(*d, "id: 20261021-01", "id: 20261021-02", 1)
		*d = strings.Replace(*d, "title: a title", "title: another", 1)
		if mutate != nil {
			mutate(d)
		}
	})
}

// TestLoadFilesReadsWhatAListingWould: the files a tree read answers are read
// the way the folder's own listing reads them — the index, the format document,
// a nested file and a file of another folder are not items, the rest are, and
// the answer is sorted by id.
func TestLoadFilesReadsWhatAListingWould(t *testing.T) {
	files := map[string][]byte{
		itemPath:                                []byte(item(nil)),
		"docs/backlog/20261021-02-another.md":   []byte(second(nil)),
		"docs/backlog/" + IndexName:             []byte("not an item: no front matter at all\n"),
		"docs/backlog/" + FormatName:            []byte("not an item either\n"),
		"docs/backlog/notes/deep.md":            []byte("not an item either\n"),
		"docs/backlog/notes/README.md":          []byte("nor this\n"),
		"docs/elsewhere/20261021-03-a-title.md": []byte("nor this\n"),
		"docs/backlog":                          nil,
	}
	items, err := LoadFiles(files)
	if err != nil {
		t.Fatalf("LoadFiles refused a folder of well-formed items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("LoadFiles read %d items, want 2 (the index, the format, a nested file and another folder's file are not items)", len(items))
	}
	if items[0].ID != "20261021-01" || items[1].ID != "20261021-02" {
		t.Errorf("LoadFiles returned %s, %s: the items are sorted by id, which is to say by date", items[0].ID, items[1].ID)
	}
	if items[0].Path != itemPath {
		t.Errorf("LoadFiles read Path %q, want the path it was fetched at, %q", items[0].Path, itemPath)
	}
}

// TestLoadFilesAllOrNothing: one file that breaks the format, one file in the
// folder whose name is not an item's, and two items sharing one id each refuse
// the whole folder — the person wrote the folder, and the files that happened
// to parse are not the state to fold (R5).
func TestLoadFilesAllOrNothing(t *testing.T) {
	whole := func() map[string][]byte {
		return map[string][]byte{
			itemPath:                              []byte(item(nil)),
			"docs/backlog/20261021-02-another.md": []byte(second(nil)),
		}
	}
	if _, err := LoadFiles(whole()); err != nil {
		t.Fatalf("LoadFiles refused a folder of well-formed items: %v", err)
	}

	// A broken item is about that file, and no item comes back with it.
	broken := whole()
	broken["docs/backlog/20261021-03-broken.md"] = []byte(second(func(d *string) {
		*d = strings.Replace(*d, "20261021-02", "20261021-03", 1)
		*d = strings.Replace(*d, "area: repo\n", "", 1)
	}))
	items, err := LoadFiles(broken)
	if err == nil || !strings.Contains(err.Error(), `"area"`) {
		t.Errorf("LoadFiles accepted a broken item file: err = %v", err)
	}
	if items != nil {
		t.Errorf("LoadFiles answered %d items beside the file it refused, want none", len(items))
	}

	// A .md file in the folder that is neither the index, the format, nor an
	// item is a file the index would silently not list.
	stray := whole()
	stray["docs/backlog/notes.md"] = []byte("stray\n")
	if _, err := LoadFiles(stray); err == nil || !strings.Contains(err.Error(), "not an item file name") {
		t.Errorf("LoadFiles accepted a stray .md file: err = %v", err)
	}

	// D26's one collision, at its source: two branches each filed an item and
	// the ids met on this repository's default branch.
	twice := whole()
	twice["docs/backlog/20261021-02-a-title.md"] = []byte(item(func(d *string) {
		*d = strings.Replace(*d, "id: 20261021-01", "id: 20261021-02", 1)
	}))
	if _, err := LoadFiles(twice); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Errorf("LoadFiles accepted two items with one id: err = %v", err)
	}
}
