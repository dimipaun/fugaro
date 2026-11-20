package runner_test

// WS11, the runner's half of the credential route: the run reads its
// repository's record at bootstrap (run.routeGateway, internal/runner/gateway.go)
// and hands the gateway the two facts it acts on. What the gateway does with
// them — which service a call reaches, what it carries, how it halts — is
// pinned in internal/gateway/workspace_route_test.go; what is pinned here is the
// reading itself: which node, who reads it at all, and what an unreadable
// record does.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// routeRecord is the record the run reads, published through the contract's own
// writer: the bytes a member's own `fugaro update` leaves at
// creds.RoutePath(slug) (C2, WS9).
func routeRecord(t *testing.T, r creds.Route) json.RawMessage {
	t.Helper()
	raw, err := creds.RouteRecord(r)
	if err != nil {
		t.Fatalf("the record a member's own update would publish was refused by its own writer: %v", err)
	}
	return json.RawMessage(raw)
}

// TestALocalRunGatewayIsNeverRoutedToTheWorkspace: `fugaro exec` runs on the
// developer's own credentials and through no credentials service — the
// workspace's own included (D149, the ruling serviceSwitched and serviceUpstream
// already hold the run to). So a local run is not routed, and the record is not
// even read: the node seeded here moves every kind to the workspace AND carries
// a field this release does not read, and a local run must be stopped by
// neither.
func TestALocalRunGatewayIsNeverRoutedToTheWorkspace(t *testing.T) {
	// No script: a request to the workspace's service fails the test by name.
	wsFake, wsSrv := anthropicfake.New(t)
	b := newBK(t, gwConfig(t, ""), "observe", "", out4k())
	b.db.Set(creds.RoutePath(repoSlug), json.RawMessage(fmt.Sprintf(
		`{"version":%d,"address":%q,"model":"workspace","bitbucket":"workspace","github":"workspace","a_field_this_release_does_not_read":true}`,
		creds.RouteVersion, wsSrv.URL)))

	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a local run whose repository is routed stopped: rec = %+v, err = %v", rec, err)
	}
	if len(st) != 1 || st[0] != 200 {
		t.Fatalf("the run's model call was answered %v", st)
	}
	if got := b.fake.Count(); got != 1 {
		t.Errorf("the run's own upstream served %d calls, want the one it made", got)
	}
	if got := wsFake.Count(); got != 0 {
		t.Errorf("a local run was routed to the workspace's service (%d calls): fugaro exec runs on the developer's own credentials and through no credentials service (D149)", got)
	}
}

// TestTheGatewayStopsOnARouteRecordItCannotRead: the contract's own reader
// refuses a record this release does not read, and the run may not go on past it
// (RouteReader.Route: a caller that treated an error as "the project's" would
// serve a credential from a service an unreadable record may have moved). The
// run stops before any model call, and the reason names the record's path and
// the step that rewrites it: this repository's own `fugaro update`, whose route
// stage publishes the record.
//
// The record here is one a later release could write and this one does not
// model — the shape a member that updated its own CLI ahead of its run's image
// leaves behind.
func TestTheGatewayStopsOnARouteRecordItCannotRead(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "enforce", "",
		`{"version":9,"allow_repos":["github/acme/*"]}`, out4k())
	// A run the platform started: only a job run is routed at all.
	b.deps.Local = false
	b.deps.Project = "aurora"
	b.deps.Env = append(b.deps.Env, "CLOUD_RUN_EXECUTION=exec-1")
	b.db.Set(creds.RoutePath(repoSlug), json.RawMessage(fmt.Sprintf(
		`{"version":%d,"address":"https://fugaro-workspace-credentials.example.invalid","model":"workspace","kinds":[]}`,
		creds.RouteVersion)))

	rec, err := b.run(t)
	if err == nil {
		t.Fatalf("a run that read a record it cannot decode went on: rec = %+v", rec)
	}
	if rec.Status != runstore.StatusInfraError || rec.Halt != nil {
		t.Fatalf("an unreadable record must be a configuration refusal and not a policy halt: rec = %+v", rec)
	}
	for _, want := range []string{creds.RoutePath(repoSlug), "fugaro update", "kinds"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("the refusal does not name %q: %s", want, rec.Reason)
		}
	}
	if got := b.fake.Count(); got != 0 {
		t.Errorf("the run made %d model calls on a record it could not read, want none", got)
	}
}
