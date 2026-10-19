package runstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestChangedFilesBoundsWhatIsStored: a run can change any number of paths,
// and the record keeps at most MaxChangedFiles of them — with the truncation
// recorded as a fact rather than hidden, so a reader can tell "this run
// changed 200 paths" from "this run changed 200 of 10,000".
func TestChangedFilesBoundsWhatIsStored(t *testing.T) {
	var paths []string
	for i := range 10_000 {
		paths = append(paths, fmt.Sprintf("src/pkg%05d/file.go", i))
	}
	cf := NewChangedFiles(paths)
	if len(cf.Paths) != MaxChangedFiles {
		t.Fatalf("stored %d paths, want the bound of %d", len(cf.Paths), MaxChangedFiles)
	}
	if cf.Total != len(paths) || !cf.Truncated {
		t.Fatalf("truncated record = %d paths, total %d, truncated %v; want %d, %d, true",
			len(cf.Paths), cf.Total, cf.Truncated, MaxChangedFiles, len(paths))
	}
	// The paths kept are the first ones git listed, in order: the same work
	// records the same set every time.
	if cf.Paths[0] != paths[0] || cf.Paths[MaxChangedFiles-1] != paths[MaxChangedFiles-1] {
		t.Fatalf("kept %q … %q, want the first %d in order", cf.Paths[0], cf.Paths[len(cf.Paths)-1], MaxChangedFiles)
	}
}

// TestChangedFilesBoundsThePathBytes: the count is not the only bound — 200
// paths of a kilometre each are as much of a record as 10,000 short ones, so
// the stored set stops at MaxChangedPathBytes too, and says so.
func TestChangedFilesBoundsThePathBytes(t *testing.T) {
	long := "d/" + strings.Repeat("x", 1000) + ".go"
	var paths []string
	for range 100 {
		paths = append(paths, long)
	}
	cf := NewChangedFiles(paths)
	if len(cf.Paths) >= len(paths) {
		t.Fatalf("stored all %d paths of %d bytes each", len(cf.Paths), len(long))
	}
	var bytes int
	for _, p := range cf.Paths {
		bytes += len(p)
	}
	if bytes > MaxChangedPathBytes {
		t.Fatalf("stored %d bytes of path, over the bound of %d", bytes, MaxChangedPathBytes)
	}
	if !cf.Truncated || cf.Total != len(paths) {
		t.Fatalf("truncated %v, total %d; want true and %d", cf.Truncated, cf.Total, len(paths))
	}
	// The byte bound is what stopped it, not the count.
	if len(cf.Paths) == MaxChangedFiles {
		t.Fatalf("stored the whole count bound (%d paths): the byte bound did not apply", len(cf.Paths))
	}
}

// TestChangedFilesKeepsASmallSetWhole: under both bounds the set is the run's
// own list, whole, and it is not called truncated.
func TestChangedFilesKeepsASmallSetWhole(t *testing.T) {
	paths := []string{"a.go", "b/c.go"}
	cf := NewChangedFiles(paths)
	if cf.Truncated || cf.Total != 2 {
		t.Fatalf("a two-path set: %+v, want it whole", cf)
	}
	if len(cf.Paths) != 2 || cf.Paths[0] != "a.go" || cf.Paths[1] != "b/c.go" {
		t.Fatalf("paths = %v, want the list as given", cf.Paths)
	}
}

// TestChangedFilesEmitsAnEmptyArrayNotEmptyNull: no paths is a fact and it is
// written as an empty array (the console's availability rule), and the
// record's own field is absent — never an empty object — when the run
// recorded none, so a reader from before this field sees the record it
// always saw.
func TestChangedFilesEmitsAnEmptyArrayNotEmptyNull(t *testing.T) {
	cf := NewChangedFiles(nil)
	if cf.Paths == nil {
		t.Fatalf("no paths came out as a nil slice: %+v", cf)
	}
	b, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"paths":[],"total":0}`; got != want {
		t.Fatalf("an empty set marshals to %s, want %s", got, want)
	}
	b, err = json.Marshal(Record{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "changed_files") {
		t.Fatalf("a record that recorded none names the field:\n%s", b)
	}
}

// TestChangedFilesRoundTripsThroughTheRecord: what the runner wrote is what a
// reader gets back, truncation included.
func TestChangedFilesRoundTripsThroughTheRecord(t *testing.T) {
	rec := Record{ChangedFiles: NewChangedFiles([]string{"a.go", "b.go"})}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var back Record
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ChangedFiles == nil || len(back.ChangedFiles.Paths) != 2 || back.ChangedFiles.Total != 2 || back.ChangedFiles.Truncated {
		t.Fatalf("round trip gave %+v", back.ChangedFiles)
	}
}

// TestChangedFilesBoundedReappliesTheBoundOnRead: the record is an object the
// job can write, so a reader never trusts its set to be bounded or honest —
// Bounded cuts an oversized list, drops empty paths, and never lets a record
// that lies about its total present a cut set as a whole one.
func TestChangedFilesBoundedReappliesTheBoundOnRead(t *testing.T) {
	if (*ChangedFiles)(nil).Bounded() != nil {
		t.Fatal("a record with no set must read as no set")
	}
	var paths []string
	for i := range 5000 {
		paths = append(paths, fmt.Sprintf("p/%05d.go", i))
	}
	// A record that carries 5000 paths, claims a total of 3 and no truncation.
	lying := &ChangedFiles{Paths: append([]string{""}, paths...), Total: 3}
	got := lying.Bounded()
	if len(got.Paths) != MaxChangedFiles {
		t.Fatalf("read back %d paths, want the bound of %d", len(got.Paths), MaxChangedFiles)
	}
	if got.Paths[0] == "" {
		t.Fatal("an empty path survived the read")
	}
	if !got.Truncated || got.Total != len(paths) {
		t.Fatalf("truncated %v, total %d; want true and %d", got.Truncated, got.Total, len(paths))
	}
	// A record whose total is larger than its list says so on read even
	// when it forgot the flag.
	short := (&ChangedFiles{Paths: []string{"a.go"}, Total: 9}).Bounded()
	if !short.Truncated || short.Total != 9 || len(short.Paths) != 1 {
		t.Fatalf("short record read back as %+v", short)
	}
	// A negative total is not a size.
	neg := (&ChangedFiles{Paths: []string{"a.go"}, Total: -4}).Bounded()
	if neg.Total != 1 || neg.Truncated {
		t.Fatalf("negative total read back as %+v", neg)
	}
	// A whole, honest record reads back unchanged.
	whole := NewChangedFiles([]string{"a.go", "b.go"}).Bounded()
	if whole.Truncated || whole.Total != 2 || len(whole.Paths) != 2 {
		t.Fatalf("honest record read back as %+v", whole)
	}
	// A record that says it was truncated stays truncated.
	if !(&ChangedFiles{Paths: []string{"a.go"}, Total: 1, Truncated: true}).Bounded().Truncated {
		t.Fatal("a record's own truncation was dropped on read")
	}
}
