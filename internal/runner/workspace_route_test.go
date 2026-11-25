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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
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

// wsAnswer is what the workspace's service answers a served model call with:
// the body, and the two headers a service settles a call by (the tier and the
// credential's name). An answer without them is one the gateway halts on, so a
// stand-in here answers as the real service does.
func wsAnswer() anthropicfake.Reply {
	rep := anthropicfake.MessageOK(sonnet, pricing.Usage{Input: 11, Output: 13})
	rep.Header = http.Header{
		gateway.HeaderTier:       []string{gateway.TierA},
		gateway.HeaderCredential: []string{"workspace-shared"},
	}
	return rep
}

// routePolicy is the project policy every run here is admitted under: this
// repository, and nothing else about the route.
const routePolicy = `{"version":9,"allow_repos":["github/acme/*"]}`

// TestTheGatewayReadsARecordTheRulesDenyAsNoRecord: the node exists and this run's token may
// not read it, which is the state of every member until WS22 deploys the rule
// that grants the read — WS22's own red step is "a run's token is refused the
// read". The denial is read as "no record", which is today's run, and this test
// is what holds that arm: the record here would move the model kind to the
// workspace, so a run that halted on the denial would stop (there is nothing to
// call), and a run that acted on the record anyway would call the workspace.
// Neither may happen: the model call reaches this run's own upstream, and the
// workspace's service is not called at all.
//
// contract-wire.md states the arm and its expiry: a denial is a state the
// contract's reader has no answer for, and until the rules land every member is
// in it, so the run behaves as it did before the record existed rather than
// stopping every run of every project for a feature none of them uses.
func TestTheGatewayReadsARecordTheRulesDenyAsNoRecord(t *testing.T) {
	// No script: a request to the workspace's service fails the test by name.
	wsFake, wsSrv := anthropicfake.New(t)
	b := admissionRun(t, "github", "acme/app", "observe", "", routePolicy, out4k())
	b.deps.Local = false
	b.deps.Project = "aurora"
	b.db.Set(creds.RoutePath(repoSlug), routeRecord(t, creds.Route{
		Version: creds.RouteVersion, Address: wsSrv.URL, Model: creds.HolderWorkspace,
	}))
	b.db.DenyRead(creds.RoutePath(repoSlug))

	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a run whose route record the rules refuse stopped: rec = %+v, err = %v", rec, err)
	}
	if len(st) != 1 || st[0] != 200 {
		t.Fatalf("the run's model call was answered %v", st)
	}
	if got := b.fake.Count(); got != 1 {
		t.Errorf("the run's own upstream served %d calls, want the one it made: a denial on the record is today's run, and today's run is the project for every kind", got)
	}
	if got := wsFake.Count(); got != 0 {
		t.Errorf("the workspace's service received %d calls on a record this run may not read, want none: a denial is not an answer to route a credential by", got)
	}
}

// TestTheGatewayCallsTheRecordsAddressAndNotTheEnvironments: the address the run
// calls is the one its own record carries, and no name in the job's environment
// moves it (R3, F32). The environment is the launcher's to write — the launcher
// is what sets this job's variables — and the run presents its own Firebase
// token to whatever address it calls, so an address from there is a token sent
// where a launcher chose.
//
// The stand-in the environment names answers as a service does, so a run that
// read the address from there would complete: this test fails on the counter
// rather than on a halt, which is the failure a fallback would produce.
func TestTheGatewayCallsTheRecordsAddressAndNotTheEnvironments(t *testing.T) {
	envFake, envSrv := anthropicfake.New(t)
	envFake.Func = func(*http.Request, []byte) anthropicfake.Reply { return wsAnswer() }
	wsFake, wsSrv := anthropicfake.New(t)
	wsFake.Func = func(*http.Request, []byte) anthropicfake.Reply { return wsAnswer() }

	// The job's Google identity, which the workspace's service verifies the
	// call by: the metadata server's answer, from a stand-in.
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "job-id-token-from-the-metadata-stand-in")
	}))
	t.Cleanup(meta.Close)

	b := admissionRun(t, "github", "acme/app", "observe", "", routePolicy, out4k())
	b.deps.Local = false
	b.deps.Project = "aurora"
	b.deps.Env = append(b.deps.Env,
		// The name a launcher would have to invent for the workspace's
		// service: nothing in this release reads one, and the run's own
		// service's name — which the run does read — is the member's own
		// address and is not what this is about.
		"FUGARO_WORKSPACE_SERVICE_URL="+envSrv.URL,
		runner.MetadataHostEnv+"="+strings.TrimPrefix(meta.URL, "http://"))
	b.db.Set(creds.RoutePath(repoSlug), routeRecord(t, creds.Route{
		Version: creds.RouteVersion, Address: wsSrv.URL, Model: creds.HolderWorkspace,
	}))

	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("the run routed to the record's address stopped: rec = %+v, err = %v", rec, err)
	}
	if got := envFake.Count(); got != 0 {
		t.Errorf("the address the run's environment named received %d calls, want none: the run's address is the one its record carries (R3, F32)", got)
	}
	if got := modelCalls(wsFake); got != 1 {
		t.Errorf("the record's address received %d calls, want the run's model call: the keep-warm beat the same run sends there (WS13) is not a call of this count", got)
	}
	if got := b.fake.Count(); got != 0 {
		t.Errorf("this run's own upstream served %d calls, want none: the record serves the model kind from the workspace (D2989)", got)
	}
}
