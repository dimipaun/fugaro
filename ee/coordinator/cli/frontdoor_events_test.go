// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// TestEventsAreReadOneWayByTheCLIAndTheFrontDoor is O16b's cross-surface test:
// `fugaro orchestrator events --json` and the front door's get_events answer one
// log the same way, because both reads are internal/orchestrator's
// (ReadAudit — the order the server stamped it in, D1230, and the --since cut
// taken on the database's own clock). The log is written by the log's own
// writer, so what the two surfaces are compared over is what a run would have
// left, and both are driven over one hermetic Realtime Database.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// frontDoorEventsNow is the fixture database's own clock and the stamp the log
// is written against, so the cut `--since` resolves is one this test knows.
var frontDoorEventsNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func TestEventsAreReadOneWayByTheCLIAndTheFrontDoor(t *testing.T) {
	f := newCloudFixture(t)
	fake := gcpfake.NewRTDB(t)
	fake.SetClock(func() time.Time { return frontDoorEventsNow })
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+fake.URL+" }\n")
	// The database's own project name, which every budget read checks before it
	// reads anything else.
	fake.Set("fugaro/project", "aurora")

	db, err := rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	// The log is written the way a run writes one: two entries, three and two
	// minutes before the database's now, so --since has something on both sides
	// of its cut to leave out and keep.
	at := frontDoorEventsNow.Add(-3 * time.Minute)
	audit, err := orchestrator.NewAudit(db, "aurora", func() (time.Time, bool) {
		at = at.Add(time.Minute)
		return at, true
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []orchestrator.Entry{
		{Subject: "web-fix", Transition: "approved", Text: "the person said yes"},
		{Subject: "web-fix", Transition: "launched", Text: "run 20261112-120000-aaaa"},
	} {
		if _, err := audit.Append(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}

	// The front door's half, over the same database: the registry the door
	// serves and the reach O16b builds for it.
	reach, err := mcpauth.NewProjectState(db)
	if err != nil {
		t.Fatal(err)
	}
	reg := mcpauth.NewRegistry()
	if err := mcpauth.RegisterStateTools(reg, reach); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		args  string
		cli   []string
		wants []string
	}{
		{"the whole log", `{"id":"aurora"}`, nil, []string{"approved", "launched"}},
		{"a cut at the server's clock", `{"id":"aurora","since":"1m30s"}`, []string{"--since", "1m30s"}, []string{"launched"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"orchestrator", "events", "--json"}, tc.cli...)
			stdout, stderr, err := execute(t, args...)
			if err != nil {
				t.Fatalf("%v\n%s%s", err, stdout, stderr)
			}
			var cliDoc map[string]any
			if err := json.Unmarshal([]byte(stdout), &cliDoc); err != nil {
				t.Fatalf("%v\n%s", err, stdout)
			}

			answer, err := reg.Call(t.Context(), mcpauth.Call{
				Tool:      "get_events",
				Arguments: json.RawMessage(tc.args),
				Scopes:    []string{mcpauth.ScopeStateRead},
			})
			if err != nil {
				t.Fatalf("get_events(%s): %v", tc.args, err)
			}
			text, ok := answer.(string)
			if !ok {
				t.Fatalf("get_events answered %T, and a tool's answer is JSON text", answer)
			}
			var doorDoc map[string]any
			if err := json.Unmarshal([]byte(text), &doorDoc); err != nil {
				t.Fatalf("%v\n%s", err, text)
			}

			// Every member the two documents both carry, and `events` above
			// all: the surfaces are one read, so a field that differed would be
			// two answers to one question. (`since` is the CLI's own: the cut
			// the door applied is on the server's clock and is not a value the
			// tool can state, which is eventsDoc's reason.)
			for _, member := range []string{"version", "kind", "id", "events"} {
				got, want := compactJSON(t, doorDoc[member]), compactJSON(t, cliDoc[member])
				if got != want {
					t.Errorf("the door answers %s: %s, and the CLI %s: %s", member, got, member, want)
				}
			}

			// And the comparison is over a log, not over two empty answers.
			events, _ := cliDoc["events"].([]any)
			if len(events) != len(tc.wants) {
				t.Fatalf("the CLI's document carries %d events, and %s keeps %d:\n%s", len(events), tc.name, len(tc.wants), stdout)
			}
			var transitions []string
			for _, e := range events {
				entry, ok := e.(map[string]any)
				if !ok {
					t.Fatalf("an event of the document is %T rather than an object", e)
				}
				transitions = append(transitions, entry["transition"].(string))
			}
			if !reflect.DeepEqual(transitions, tc.wants) {
				t.Errorf("%s: transitions are %v, want %v (the order is the server's stamp's, D1230)", tc.name, transitions, tc.wants)
			}
		})
	}
}

// compactJSON is one decoded member as JSON text, so two documents are
// compared as the bytes a consumer reads rather than as Go values.
func compactJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
