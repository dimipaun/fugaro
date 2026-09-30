package followup

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSnapshotRoundTrip(t *testing.T) {
	sel := sample()
	fetched := t0.Add(10 * time.Minute)
	s := NewSnapshot(7, sel, fetched)
	if s.Version != 1 || s.PR != 7 || !s.Since.Equal(t0) || !s.Fetched.Equal(fetched) {
		t.Fatalf("snapshot header: %+v", s)
	}
	if len(s.Comments) != len(sel.Comments) || s.Comments[0].Path != "internal/app/app.go" || s.Comments[0].Line != 42 || !s.Comments[0].Outdated {
		t.Fatalf("snapshot comments: %+v", s.Comments)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"version":1`, `"fetched_at"`, `"comments"`, `"author_id"`, `"created_at"`, `"omitted"`, `"untrusted_authors"`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("comments.json lacks %s: %s", key, b)
		}
	}
	var back Snapshot
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, s) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, s)
	}

	// An empty selection still writes objects, never null.
	e, err := json.Marshal(NewSnapshot(7, Selection{}, fetched))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(e), "null") {
		t.Fatalf("empty snapshot has a null: %s", e)
	}
}
