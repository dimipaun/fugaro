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
