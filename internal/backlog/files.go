package backlog

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// LoadFiles reads every item among fetched files — each file's
// repository-relative path to its bytes — the way Load reads a folder of them:
// the same `<id>-<slug>.md` name (D26), the index and the format document
// skipped, the same all-or-nothing Parse, and the same refusal of a folder
// holding two items with one id.
//
// It is for the caller that has the files and no directory: `fugaro update`
// folds each enrolled repository's `docs/backlog/` out of the code host's own
// read of the default branch, which answers the folder's files and nothing
// else (docs/update-from-anywhere R5, D3344). Only the files directly in Dir
// are items, exactly as Load's own listing of a directory sees only its files:
// a tree read answers the folder's subdirectories beside them, and a nested
// file is not an item of the folder.
//
// The paths are repository-relative with "/" separators, which is what a code
// host answers and what a caller's own map holds.
func LoadFiles(files map[string][]byte) ([]Item, error) {
	var items []Item
	seen := map[string]string{}
	for _, p := range sortedPaths(files) {
		name := itemName(p)
		if path.Dir(p) != Dir || !strings.HasSuffix(name, ".md") || name == IndexName || name == FormatName {
			continue
		}
		item, err := Parse(p, files[p])
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[item.ID]; dup {
			return nil, fmt.Errorf("%s: id %q is already used by %s: two items may not share an id (D26)", item.Path, item.ID, prev)
		}
		seen[item.ID] = item.Path
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// sortedPaths is a map's keys in path order, so that the answer names the same
// file every time: a map's own order is the runtime's, and a refusal that moved
// with it would be a different sentence each run.
func sortedPaths(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
