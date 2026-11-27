package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// This file is `fugaro image ls --shared`'s own test (design
// docs/forge-authorship-images/shared-images.md §7, §8): the table's four
// columns, the count-and-cap line, the over-cap warning, and --json's shape.
// The flag's own listing — that --shared prints the lock and not the catalog —
// is image_catalog_test.go's.

// TestImageLsListsHashesAndRepos: §7's table is the image, the key's first 12
// hex, the repositories on it and its last use's date in UTC, and the last
// line carries the count and the cap.
func TestImageLsListsHashesAndRepos(t *testing.T) {
	f := catalogFixture(t)
	first := lockedImage(keyA, time.Date(2026, 10, 29, 9, 40, 11, 0, time.UTC), map[string]string{"acme/app": "web"})
	second := lockedImage(keyB, time.Date(2026, 11, 2, 1, 5, 0, 0, time.UTC), map[string]string{"acme/other": "svc"})
	writeSharedLock(t, f, map[string]imagecheck.LockedImage{keyA: first, keyB: second})

	out, _, err := execute(t, "image", "ls", "--shared")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("image ls --shared printed %d lines, want the header, two rows and the count:\n%s", len(lines), out)
	}
	for _, want := range []string{"IMAGE", "HASH", "REPOSITORIES", "LAST USE"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the header lacks %q: %q", want, lines[0])
		}
	}
	for _, want := range []string{sharedImageRef(keyA), shortHash(keyA), "acme/app", "2026-10-29"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("the first row lacks %q: %q", want, lines[1])
		}
	}
	// The repositories are comma-separated and sorted, and the full key is not
	// the table's: HASH is the 12 characters the image's own name carries.
	for _, want := range []string{"acme/other", "2026-11-02"} {
		if !strings.Contains(lines[2], want) {
			t.Errorf("the second row lacks %q: %q", want, lines[2])
		}
	}
	if strings.Contains(out, keyA) {
		t.Errorf("the table printed the full key:\n%s", out)
	}
	if !strings.Contains(lines[3], "2 images (cap 20)") {
		t.Errorf("the count line = %q", lines[3])
	}
}

// TestImageLsWarnsOverImageCap: §8's warning — the count, the cap and the two
// remedies — when the lock holds more images than the cap, and nothing but the
// count when it does not. It is a warning and no refusal: the listing prints
// either way.
func TestImageLsWarnsOverImageCap(t *testing.T) {
	f := catalogFixture(t)
	images := map[string]imagecheck.LockedImage{}
	for i := range imagecheck.ImageCap + 1 {
		key := fmt.Sprintf("%064x", i)
		images[key] = lockedImage(key, time.Date(2026, 11, 2, 1, 5, 0, 0, time.UTC), nil)
	}
	writeSharedLock(t, f, images)

	out, _, err := execute(t, "image", "ls", "--shared")
	if err != nil {
		t.Fatal(err)
	}
	last := out[strings.LastIndex(strings.TrimRight(out, "\n"), "\n")+1:]
	for _, want := range []string{fmt.Sprintf("%d images (cap %d)", imagecheck.ImageCap+1, imagecheck.ImageCap), "fugaro image ls", "fugaro image prune"} {
		if !strings.Contains(last, want) {
			t.Errorf("the warning lacks %q: %q", want, last)
		}
	}
	if !strings.Contains(out, sharedImageRef(fmt.Sprintf("%064x", 0))) {
		t.Errorf("the over-cap listing printed no rows:\n%s", out)
	}

	var doc catalogSharedOut
	if out, _, err := execute(t, "image", "ls", "--shared", "--json"); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if doc.Version != imagecheck.LockVersion || doc.Count != imagecheck.ImageCap+1 || doc.Cap != imagecheck.ImageCap || !doc.OverCap {
		t.Errorf("--json = %+v", doc)
	}
	if len(doc.Images) != imagecheck.ImageCap+1 {
		t.Fatalf("--json lists %d images", len(doc.Images))
	}
	// The document carries what the table rounds away: the whole key, the
	// digest and the two timestamps.
	row := doc.Images[0]
	if row.Key != fmt.Sprintf("%064x", 0) || row.Digest == "" || row.BuiltAt.IsZero() || row.LastUsedAt.IsZero() {
		t.Errorf("the first row = %+v", row)
	}
}
