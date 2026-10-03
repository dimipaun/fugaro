package budget_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runstore"
)

func putRecord(t *testing.T, b *blobx.Bucket, slug, id string, rec runstore.Record) {
	t.Helper()
	rec.RunID = id
	if err := runstore.Open(b.Bucket, slug, id).WriteRecord(context.Background(), &rec); err != nil {
		t.Fatal(err)
	}
}

func TestBucketFactsAttribution(t *testing.T) {
	b := blobx.Wrap(memblob.OpenBucket(nil))
	day := budget.Day(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	// Minted 23:59:50 on day, started 00:00:10 the next day: the start day counts.
	start := time.Date(2026, 10, 19, 0, 0, 10, 0, time.UTC)
	fin := start.Add(90 * time.Minute)
	c := runstore.NewCost(1.5, 0.25, runstore.BasisAPIList)
	putRecord(t, b, "acme-app", "20261018-235950-aaaa", runstore.Record{Repo: "acme/app", StartedAt: start, FinishedAt: &fin, Cost: &c})
	// Compute not estimated: hours, no dollars.
	un := runstore.ModelOnlyCost(1, runstore.BasisAPIList)
	putRecord(t, b, "acme-app", "20261018-100000-bbbb", runstore.Record{Repo: "acme/app", StartedAt: time.Date(2026, 10, 18, 10, 0, 5, 0, time.UTC),
		FinishedAt: ptrTo(time.Date(2026, 10, 18, 11, 0, 5, 0, time.UTC)), Cost: &un})
	// Out of range, and not yet recorded.
	putRecord(t, b, "acme-app", "20261001-100000-cccc", runstore.Record{StartedAt: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)})
	if err := b.WriteAll(context.Background(), "runs/acme-app/20261018-120000-dddd/task.json", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	// A corrupt record is skipped with a warning.
	if err := b.WriteAll(context.Background(), "runs/acme-app/20261018-130000-eeee/result.json", []byte("{nope"), nil); err != nil {
		t.Fatal(err)
	}
	var warns []string
	f := budget.BucketFacts{Bucket: b.Bucket, Warn: func(s string) { warns = append(warns, s) }}
	facts, repos, err := f.Facts(context.Background(), day, day+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts[day]) != 1 || facts[day][0].Run != "20261018-100000-bbbb" || facts[day][0].ComputeEstimated || facts[day][0].Hours != 1 {
		t.Fatalf("facts[day] = %+v", facts[day])
	}
	g := facts[day+1]
	if len(g) != 1 || g[0].Run != "20261018-235950-aaaa" || !g[0].ComputeEstimated || g[0].ComputeUSD != 0.25 || g[0].Hours != 1.5 {
		t.Fatalf("facts[day+1] = %+v", g)
	}
	if repos["acme-app"] != "acme/app" || len(warns) != 1 || !strings.Contains(warns[0], "eeee") {
		t.Fatalf("repos %v warns %v", repos, warns)
	}
}
