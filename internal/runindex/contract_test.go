package runindex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// TestContractShapes is the pin on the index's two documents and its cursor.
//
// The index is written by the history job and read by the CLI a person runs,
// and those two are not always the same release: the job's image is rebuilt
// behind `fugaro update`'s own confirmation, so an installation can carry a
// job older than its CLI for as long as the operator puts off the rebuild.
// A field that does not survive its own JSON is therefore a field two releases
// disagree about, and the disagreement is silent -- the reader sees the zero
// value. The round trips below are what makes that a test failure instead.
//
// The cursor is the same argument about the one value that crosses a process
// boundary whole: a page's continuation, and the reindex walk's position in
// the writer lease (F39). It encodes to a token and decodes back to itself,
// and a token that is not one of ours is refused rather than read as the
// start of the listing -- a walk that resumed at the wrong run would fold
// history twice or skip it, and the second is invisible.
func TestContractShapes(t *testing.T) {
	started := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	finished := started.Add(21 * time.Minute)
	updated := finished.Add(30 * time.Second)
	folded := time.Date(2026, 11, 22, 4, 15, 0, 0, time.UTC)
	builtAt := started.Add(-72 * time.Hour)
	compute := int64(41_000)

	t.Run("entry", func(t *testing.T) {
		want := Entry{
			RunID:        "20261120-093000-a1b2",
			Slug:         "acme-widgets",
			Repo:         "acme/widgets",
			UpdatedAt:    updated,
			Status:       runstore.StatusSucceeded,
			PlanTask:     "C-INDEX",
			PR:           3540,
			RequestedBy:  "dev@example.invalid",
			LaunchedBy:   "dev@example.invalid",
			Batch:        "20261120-093000",
			Tier:         tier.Medium,
			Rigor:        rigor.Pro,
			StartedAt:    &started,
			FinishedAt:   &finished,
			Workflow:     "go",
			Title:        "the index contract",
			Recipe:       "coder",
			OAuthAccount: "bmb",
			PreviousRun:  "20261119-120000-0f0f",
			FollowUp:     true,
			FollowUpOf:   &task.FollowUpOf{Run: "20261118-080000-1234", Keys: []string{"f1", "f2"}},
			Launch: &runstore.Launch{
				Version: 1, RunID: "20261120-093000-a1b2", Backend: "cloud-run",
				Execution: "projects/acme/locations/europe-west1/jobs/fugaro/executions/abc",
				Job:       "fugaro", LogURL: "https://example.invalid/logs",
				LaunchedBy: "dev@example.invalid", LaunchedAt: started, PlanTask: "C-INDEX",
			},
			Record: &runstore.Record{
				Version: 1, RunID: "20261120-093000-a1b2", Status: runstore.StatusSucceeded,
				Stage: "finalize", Outcome: "ready", Branch: "fugaro/20261120-093000-a1b2",
				StartedAt: started, FinishedAt: &finished,
				Tier:  &runstore.TierRecord{Tier: tier.Medium, ExpectedUSD: 3, ExpectedMinutes: 40},
				Rigor: &runstore.RigorRecord{Mode: rigor.Pro, Level: rigor.Pro, From: rigor.FromRepo},
				Image: &runstore.ImageInfo{BuiltAt: &builtAt},
				Cost:  &runstore.Cost{ModelUSD: 1.25, ComputeUSD: 0.41, ComputeEstimated: true, TotalUSD: 1.66, ModelBasis: runstore.BasisAPIList},
				PR:    &runstore.PRRef{Number: 3540, URL: "https://example.invalid/acme/widgets/pull/3540"},
			},
		}
		roundTrip(t, want)
	})

	t.Run("summary", func(t *testing.T) {
		want := Summary{
			Version:             Version,
			Complete:            true,
			FoldedThrough:       &folded,
			UpdatedAt:           folded.Add(2 * time.Second),
			Runs:                349_999,
			States:              map[string]int64{"succeeded": 300_000, "failed": 49_999},
			SpentMicros:         412_500_000,
			NotionalMicros:      91_000_000,
			ComputeMicros:       &compute,
			ComputeRuns:         349_998,
			ComputeNotEstimated: 1,
			Estimate: &estimate.Table{
				Version: 1, WrittenAt: folded,
				Groups: []estimate.Group{{
					Tier: tier.Medium, Rigor: rigor.Pro, Runs: 12,
					DurationMinutes: estimate.Quartiles{P25: 18, P50: 24, P75: 31},
					CostUSD:         &estimate.Quartiles{P25: 1.1, P50: 1.4, P75: 1.9},
					CostRuns:        12,
				}},
			},
		}
		roundTrip(t, want)
	})

	t.Run("cursor", func(t *testing.T) {
		for _, want := range []Cursor{
			{UpdatedAt: &updated, RunID: "20261120-093000-a1b2"},
			{Slug: "acme-widgets", RunID: "20261120-093000-a1b2"},
		} {
			got, err := DecodeCursor(want.Encode())
			if err != nil {
				t.Fatalf("DecodeCursor(%q): %v", want.Encode(), err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("cursor round trip: got %+v, want %+v", got, want)
			}
		}
		if a, b := (Cursor{UpdatedAt: &updated, RunID: "a"}).Encode(), (Cursor{UpdatedAt: &updated, RunID: "b"}).Encode(); a == b {
			t.Errorf("two cursors at different runs encode alike: %q", a)
		}
	})

	t.Run("cursor refuses a token that is not one", func(t *testing.T) {
		for _, tok := range []string{
			"",
			"not a token",
			base64.RawURLEncoding.EncodeToString([]byte("not json")),
			base64.RawURLEncoding.EncodeToString([]byte(`{"run_id":""}`)),
			base64.RawURLEncoding.EncodeToString([]byte(`{"updated_at":"2026-11-20T09:30:00Z"}`)),
		} {
			if got, err := DecodeCursor(tok); err == nil {
				t.Errorf("DecodeCursor(%q) = %+v, want an error: a token that is not a position must not read as one", tok, got)
			}
		}
	})
}

// roundTrip is one document through its own JSON and back, and it compares the
// bytes rather than the Go values: what two releases have to agree about is the
// document, and a value that reads back equal while its JSON moved (a tag
// renamed, an omitempty dropped) is exactly the disagreement this pins.
func roundTrip[T any](t *testing.T, want T) {
	t.Helper()
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshalling %T: %v", want, err)
	}
	var got T
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshalling %T: %v\n%s", want, err, data)
	}
	back, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling %T back: %v", want, err)
	}
	if !bytes.Equal(data, back) {
		t.Errorf("%T did not survive its own JSON:\n wrote %s\n read  %s", want, data, back)
	}
}
