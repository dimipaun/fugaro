package digest

import (
	"context"
	"encoding/json"
	"testing"

	"gocloud.dev/blob/memblob"
)

// storedBucket is a bucket with the given objects in it, and the context the read
// runs under.
func storedBucket(t *testing.T, objects map[string]string) (*Store, context.Context) {
	t.Helper()
	b := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = b.Close() })
	for key, data := range objects {
		if err := b.WriteAll(context.Background(), key, []byte(data), nil); err != nil {
			t.Fatal(err)
		}
	}
	return NewStore(b, storedSlug), context.Background()
}

// storedSlug is the one repository these tests store reports for, and
// otherSlug a second one whose reports are never read by the first's store.
const (
	storedSlug = "acme-app-0123456789abcdef"
	otherSlug  = "acme-app-fedcba9876543210"
)

// storedReport is one report file as the bucket holds it: the fenced block the
// writers emit, with the period's own dates in it, so the store reads back a
// summary this package wrote.
func storedReport(t *testing.T, s Summary) []byte {
	t.Helper()
	dim, ok := periodDim(s.Period)
	if !ok {
		t.Fatalf("%q is not a period key this package writes", s.Period)
	}
	p, err := PeriodOfKey(dim, s.Period)
	if err != nil {
		t.Fatal(err)
	}
	s.Start, s.End = p.Start, p.End
	// The writers always carry a figure for each dollar field, zero included,
	// and a block without one is not one this package wrote.
	for _, f := range []*string{&s.Billed, &s.Notional, &s.Review} {
		if *f == "" {
			*f = "0.000000"
		}
	}
	block, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return []byte("## Summary\n\n```json\n" + string(block) + "\n```\n")
}

// storedRow is one report as a committed row a caller hands Series: the
// summary is the one its own bytes carry, so the row is what a folder read by
// the CLI's own reader would give.
func storedRow(t *testing.T, s Summary) Row {
	t.Helper()
	got, err := ReadSummary(storedReport(t, s))
	if err != nil {
		t.Fatal(err)
	}
	dim, key, ok := ReportName(PeriodPath(s.Period))
	if !ok {
		t.Fatalf("%q is not a period key this package writes", s.Period)
	}
	return Row{Dim: dim, Key: key, Rel: PeriodPath(key), Summary: got, Read: true}
}

// TestStoreReadsTheReportsUnderItsPrefix: the stored series is the reports of
// one repository's own prefix -- its periods, their keys and the summaries
// they carry -- and nothing else in the bucket is a report of it.
func TestStoreReadsTheReportsUnderItsPrefix(t *testing.T) {
	bucket, ctx := storedBucket(t, map[string]string{
		ReportsPrefix + "/" + storedSlug + "/2026/2026-10-26.md":   string(storedReport(t, weekBlock("2026-10-26", 5, 2, FlagHalt))),
		ReportsPrefix + "/" + storedSlug + "/2026/2026-W43.md":     string(storedReport(t, weekBlock("2026-W43", 7, 0, FlagHalt))),
		ReportsPrefix + "/" + storedSlug + "/2026/2026-11-01.md":   "{not json at all",
		ReportsPrefix + "/" + storedSlug + "/2026/notes.md":        "a file a person put here",
		ReportsPrefix + "/" + storedSlug + "/README.md":            "an index is not a report",
		ReportsPrefix + "/" + otherSlug + "/2026/2026-W43.md":      string(storedReport(t, weekBlock("2026-W43", 1, 9))),
		ReportsPrefix + "/2026/2026-W43.md":                        string(storedReport(t, weekBlock("2026-W43", 99, 0))),
		"runs/" + storedSlug + "/20261101-090000-abcd/result.json": "{}",
		"builds/" + storedSlug + "/catalog/entry/check.json":       "{}",
	})
	rows, err := bucket.Rows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("the store read %d rows, want 3 (the day, the week and the unreadable day): %+v", len(rows), rows)
	}
	// Key order, whatever order the bucket listed them in.
	if got := rows[0].Key; got != "2026-10-26" {
		t.Errorf("the first row is %s, want the day 2026-10-26", got)
	}
	if rows[0].Dim != ByDay || rows[0].Rel != "2026/2026-10-26.md" || !rows[0].Read {
		t.Errorf("the day's row = %+v, want a readable day row at 2026/2026-10-26.md", rows[0])
	}
	if got := rows[0].Summary.Halts; got != 2 {
		t.Errorf("the day's halts = %d, want 2", got)
	}
	// An object of a report's own name that does not hold a block this package
	// wrote is still the period's report: the file is there, which is what the
	// row says, and its figures are not (G5).
	if rows[1].Key != "2026-11-01" || rows[1].Read {
		t.Errorf("the unreadable day's row = %+v, want a row of that period with no summary", rows[1])
	}
	if got := rows[2].Summary.Runs; got != 7 {
		t.Errorf("the week's runs = %d, want 7", got)
	}
}

// TestStoreObjectIsTheLayoutInTheBucket: a report's object is a function of
// its key, at the path the committed layout names under the repository's
// prefix, so a writer that has the key needs no listing to address it.
func TestStoreObjectIsTheLayoutInTheBucket(t *testing.T) {
	b := NewStore(memblob.OpenBucket(nil), storedSlug)
	if got, want := b.Object("2026-W43"), "reports/"+storedSlug+"/2026/2026-W43.md"; got != want {
		t.Errorf("Object(2026-W43) = %q, want %q", got, want)
	}
	if got, want := b.Prefix(), "reports/"+storedSlug+"/"; got != want {
		t.Errorf("Prefix() = %q, want %q", got, want)
	}
	// The project's own store -- the aggregate's prefix (R8) -- is the one
	// prefix a repository's reports do not cover, and it is not a repository's.
	if got, want := NewStore(nil, "").Object("2026-10"), "reports/2026/2026-10.md"; got != want {
		t.Errorf("the project store's Object(2026-10) = %q, want %q", got, want)
	}
}

// TestSeriesPrefersTheStoredCopy: a period both holds is read from the bucket,
// which is the project's own series, and a period only the checkout holds is
// still read -- an installation whose job has never fired has no stored
// reports at all.
func TestSeriesPrefersTheStoredCopy(t *testing.T) {
	bucket, ctx := storedBucket(t, map[string]string{
		ReportsPrefix + "/" + storedSlug + "/2026/2026-W42.md": string(storedReport(t, weekBlock("2026-W42", 5, 0, FlagHalt))),
	})
	committed := []Row{storedRow(t, weekBlock("2026-W41", 4, 0)), storedRow(t, weekBlock("2026-W42", 4, 0))}
	rows, err := bucket.Series(ctx, committed)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("the series holds %d rows, want 2 (one period is in both): %+v", len(rows), rows)
	}
	if got := rows[0]; got.Key != "2026-W41" || got.Summary.Runs != 4 {
		t.Errorf("the committed-only week = %+v, want the folder's own figures", got)
	}
	if got := rows[1]; got.Key != "2026-W42" || got.Summary.Runs != 5 {
		t.Errorf("the week both hold = %+v, want the bucket's copy (runs 5)", got)
	}

	// No committed rows at all is the job's own read, and it is the stored
	// series rather than an empty one.
	only, err := bucket.Series(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Key != "2026-W42" {
		t.Errorf("the stored-only series = %+v, want the one stored week", only)
	}
}
