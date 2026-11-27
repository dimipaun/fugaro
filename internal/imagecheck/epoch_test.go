package imagecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// baseEpochTrees are the trees the epoch covers (F43): the base image's own
// files, the derived build's template and cloudbuild.yaml, and the shared
// checkout script. A catalog entry's key hashes a repository's packages, tools
// and base reference, so a change to any of these three reaches no image
// through it and has to move the epoch instead.
var baseEpochTrees = []string{"base", "derived", "common"}

// baseEpochHashes records the hash of those trees as of each epoch: the entry
// for BaseEpoch is what they must hash to now. A change to any of them fails
// TestBaseEpochTracksBaseImage until the epoch moves and the new hash is
// recorded here, which is what keeps BaseEpoch a fact about the trees rather
// than a number somebody remembers to bump.
var baseEpochHashes = map[int]string{
	1: "485459be1c7f19265dea414ed6b7fc242a92bc2ef9b70aa818ee148e564123c3",
}

// baseEpochHash hashes the three trees as one value: every file's path under
// its tree, then its bytes, with the lines naming FUGARO_VERSION left out —
// a release moves that line and nothing else in them, and the epoch is about
// what the images are built FROM rather than which fugaro they carry.
func baseEpochHash(t *testing.T) string {
	t.Helper()
	h := sha256.New()
	for _, tree := range baseEpochTrees {
		root := filepath.Join(testutil.ModuleRoot(), "images", tree)
		var files []string
		if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		}); err != nil {
			t.Fatalf("walking images/%s: %v", tree, err)
		}
		slices.Sort(files)
		for _, rel := range files {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("reading images/%s/%s: %v", tree, rel, err)
			}
			fmt.Fprintf(h, "%s/%s\n", tree, rel)
			for _, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "FUGARO_VERSION") {
					continue
				}
				fmt.Fprintf(h, "%s\n", line)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestBaseEpochTracksBaseImage: the base image, the derived template and the
// shared checkout script are not in a catalog entry's key, so a change to any
// of them is invisible to every image built before it unless the epoch moves.
// This hashes the three trees and fails when they changed without it, which is
// the one thing a person cannot be trusted to remember (D3446).
func TestBaseEpochTracksBaseImage(t *testing.T) {
	got := baseEpochHash(t)
	want, ok := baseEpochHashes[BaseEpoch]
	if !ok {
		t.Fatalf("no hash is recorded for imagecheck.BaseEpoch %d: the trees hash to %s, so record that in baseEpochHashes", BaseEpoch, got)
	}
	if got != want {
		t.Fatalf("images/base, images/derived and images/common hash to %s, and imagecheck.BaseEpoch %d was recorded with %s: bump BaseEpoch and record the new hash, so update rebuilds every image built before the change", got, BaseEpoch, want)
	}
}

// TestRecordCarriesEpoch: a catalog build's record says which epoch it was
// built from (D3446), so the check and update can tell an image built from the
// base image as it is now from one built before it changed. A record written
// before the field existed reads as 0 — no epoch stated — which is what makes
// release A's update rebuild every entry once (D3445).
func TestRecordCarriesEpoch(t *testing.T) {
	rec, err := ParseCatalogRecordState([]byte(`{"version":1,"entry":"go","epoch":7,"built_at":"2026-11-27T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Epoch != 7 {
		t.Fatalf("a record stating epoch 7 reads as epoch %d", rec.Epoch)
	}
	before, err := ParseCatalogRecordState([]byte(`{"version":1,"entry":"go","built_at":"2026-11-27T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	if before.Epoch != 0 {
		t.Fatalf("a record written before the epoch existed reads as epoch %d, want 0 (no epoch stated)", before.Epoch)
	}
}
