package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runner"
	"golang.org/x/oauth2"
)

// The resident orchestrator's entry point (P5.1c) is not a command anyone is
// offered: it is what the orchestrator image's own entrypoint runs, in the
// Cloud Run service the installation module creates, and like the credentials
// service's and the intake's it takes no argument, no flag and no config file.
// So the SUBCOMMAND is hidden — the group, `fugaro orchestrator`, is a command
// a person uses and stays visible — and the hiding is the Hidden field rather
// than an accident of cobra's rules, which is what the last two assertions are
// for: a group whose help has to be read to know whether a name is offered is
// one line away from offering it.
func TestOrchestratorServeIsHidden(t *testing.T) {
	out, _, err := execute(t, "orchestrator", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "serve") {
		t.Fatalf("fugaro orchestrator --help lists serve:\n%s", out)
	}
	root := NewRootCmd()
	group := findSub(t, root, "orchestrator")
	serve := findSub(t, group, "serve")
	if !serve.Hidden {
		t.Error("orchestrator serve is not hidden: fugaro orchestrator --help would list it")
	}
	if serve.Flags().HasFlags() {
		t.Error("orchestrator serve takes a flag: this process's whole configuration is its environment, and a flag would be a second way to start it")
	}
}

// TestOrchestratorServiceEnvIsTheDeploymentsOwn holds the two spellings of this
// service's environment to each other, in both directions: the three names this
// process reads are the three orchestrator.tf sets on the container, and the
// module sets nothing beside them. The direction that matters is the module
// setting a name this command does not read — a fact an operator would believe
// was in effect — and the other is a name this command reads that nothing sets,
// which is a service that refuses to start on a deployment that is right.
//
// internal/infra/tf's orchestratorServiceEnv is the third side of the triangle,
// and it is held to the module by that package's own test (the cover's
// allow-list refuses a container setting a name outside it, which is what makes
// the module's list a boundary rather than a convention).
func TestOrchestratorServiceEnvIsTheDeploymentsOwn(t *testing.T) {
	tf := readRepoFile(t, "deploy/terraform/gcp/modules/installation/orchestrator.tf")
	inModule := map[string]bool{}
	for _, m := range regexp.MustCompile(`name\s*=\s*"(FUGARO_[A-Z0-9_]+)"`).FindAllStringSubmatch(tf, -1) {
		inModule[m[1]] = true
	}
	for _, n := range orchestratorServiceEnvNames {
		if !inModule[n] {
			t.Errorf("this command reads %s, which orchestrator.tf does not set on the service: the service would refuse to start on a deployment that is right", n)
		}
		delete(inModule, n)
	}
	for n := range inModule {
		t.Errorf("orchestrator.tf sets %s, which this command does not read: an operator would believe the fact was in effect and the process would never look at it", n)
	}
}

// orchestratorServeEnv is a complete, valid environment: the names
// orchestrator.tf sets on the service's container, with the golden fixtures'
// shapes.
var orchestratorServeEnv = map[string]string{
	envProject:    "aurora",
	envFirebaseFP: "aurora",
	envRTDBURL:    "https://aurora-default-rtdb.firebaseio.com",
}

// TestOrchestratorServiceConfigRefusesWhatItCannotKnow is the fail-closed half
// of the wiring: every name the module must set, and the two names that can be
// present and still useless. Each refusal names the VARIABLE and never a value
// (the no-echo rule), which the last check holds: the environment carries a
// project name nowhere in the message.
func TestOrchestratorServiceConfigRefusesWhatItCannotKnow(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "no project", env: map[string]string{}, want: envProject},
		{name: "no firebase project", env: map[string]string{envProject: "aurora"}, want: envFirebaseFP},
		{
			name: "no database",
			env:  map[string]string{envProject: "aurora", envFirebaseFP: "aurora"},
			want: envRTDBURL,
		},
		{
			name: "no project at all in the message of another refusal",
			env:  map[string]string{envProject: "", envFirebaseFP: "aurora", envRTDBURL: "https://aurora-default-rtdb.firebaseio.com"},
			want: envProject,
		},
		{
			name: "a project this release cannot address",
			env:  map[string]string{envProject: "Aurora/../belong", envFirebaseFP: "aurora", envRTDBURL: "https://aurora-default-rtdb.firebaseio.com"},
			want: envProject,
		},
		{
			name: "a database URL that is not one",
			env:  map[string]string{envProject: "aurora", envFirebaseFP: "aurora", envRTDBURL: "http://aurora-default-rtdb.firebaseio.com/state"},
			want: envRTDBURL,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := orchestratorServiceConfig(context.Background(), envFrom(tc.env), io.Discard)
			if err == nil {
				t.Fatalf("the configuration was built with %v, and the process would serve without the fact it names", tc.env)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want the refusal to name %s: a refusal that does not name the variable sends an operator to the wrong place", err, tc.want)
			}
			for _, value := range tc.env {
				if value != "" && strings.Contains(err.Error(), value) {
					t.Errorf("the refusal quotes %q, which is what the environment held: this process's log is Cloud Logging (the no-echo rule)", value)
				}
			}
		})
	}
}

// TestOrchestratorServiceConfigRefusesBelong is section 6's rule 6 with a real
// caller, and it is the one rule of the eleven this process can judge about
// itself: `belong` is the owner's own installation (F31), the resident
// orchestrator is a sandbox or a fixture installation, and a deployment pointed
// at the owner's own project refuses to start rather than serving it.
//
// The rule is about the INSTALLATION and not about one of its names, so both
// names this deployment is made of are held to it. The second case is the one
// that costs something to get wrong and is refused by the Firebase project
// alone: its own project is a sandbox's and the database it reads is `belong`'s,
// so the deployment would read and act on the owner's own node — and the URL
// agrees with the Firebase project it is named after, so rule 10 has nothing to
// say about it and rule 6 is the whole of the refusal.
func TestOrchestratorServiceConfigRefusesBelong(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{
			name: "the installation's own name",
			env: map[string]string{
				envProject:    "belong",
				envFirebaseFP: "belong",
				envRTDBURL:    "https://belong-default-rtdb.firebaseio.com",
			},
		},
		{
			name: "the family, in both names",
			env: map[string]string{
				envProject:    "belong-sandbox",
				envFirebaseFP: "belong-sandbox",
				envRTDBURL:    "https://belong-sandbox-default-rtdb.firebaseio.com",
			},
		},
		{
			name: "only the firebase project is the family's",
			env: map[string]string{
				envProject:    "fugaro-sandbox",
				envFirebaseFP: "belong-sandbox",
				envRTDBURL:    "https://belong-sandbox-default-rtdb.firebaseio.com",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := orchestratorServiceConfig(context.Background(), envFrom(tc.env), io.Discard)
			if err == nil {
				t.Fatalf("a deployment of %v was started: the resident orchestrator is never the owner's own installation (rule 6, F31)", tc.env)
			}
			if !strings.Contains(err.Error(), orchestrator.NeverRules()[5].Never) {
				t.Errorf("err = %v, want the rule's own sentence, so a reader is sent to section 6", err)
			}
		})
	}
}

// TestOrchestratorServiceConfigRefusesAnotherInstallationsDatabase is rule 10
// at the one place this deployment can get it wrong: the database is named
// after the Firebase project it belongs to, so a URL and a Firebase project
// that disagree are two installations, and a process pointed at a copied
// environment would read another installation's state and act on its items.
func TestOrchestratorServiceConfigRefusesAnotherInstallationsDatabase(t *testing.T) {
	env := map[string]string{
		envProject:    "aurora",
		envFirebaseFP: "aurora",
		envRTDBURL:    "https://somebody-else-default-rtdb.firebaseio.com",
	}
	_, err := orchestratorServiceConfig(context.Background(), envFrom(env), io.Discard)
	if err == nil {
		t.Fatal("a database of another installation was accepted: one service account serves one installation's nodes")
	}
	if !strings.Contains(err.Error(), envRTDBURL) || !strings.Contains(err.Error(), envFirebaseFP) {
		t.Errorf("err = %v, want both variables named: the pair is what disagrees", err)
	}
}

// TestOrchestratorServiceConfigBuildsFromTheEnvironment is the direction that
// matters for a deployment that is RIGHT: a full environment builds the
// configuration the host serves with, and the id it carries is the project the
// node is addressed by. The token source is the process's only reach for
// Google's own identity, and the seam replaces it so that this test needs no
// Application Default Credentials (orchestratorWiring).
func TestOrchestratorServiceConfigBuildsFromTheEnvironment(t *testing.T) {
	orchestratorTest = &orchestratorWiring{tokens: func(context.Context) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{}), nil
	}}
	t.Cleanup(func() { orchestratorTest = nil })

	cfg, err := orchestratorServiceConfig(context.Background(), envFrom(orchestratorServeEnv), io.Discard)
	if err != nil {
		t.Fatalf("a complete environment was refused: %v", err)
	}
	if cfg.id != orchestratorServeEnv[envProject] {
		t.Errorf("the configuration serves %q, and the environment named %q: the node is addressed by the project name", cfg.id, orchestratorServeEnv[envProject])
	}
	if cfg.db == nil || cfg.log == nil {
		t.Fatalf("the configuration carries db = %v, log = %v: a service built from either nil refuses every tick", cfg.db, cfg.log)
	}
}

// fakeOrchestratorStore is the store the pass is served from: the documents a
// node holds, and a write half that FAILS THE TEST if it is reached. That is
// this step's whole claim about the pass — it reads the node the previous pass
// left and writes nothing — held the only way a claim about a code path can be:
// by making the path that must not exist fail when it does.
type fakeOrchestratorStore struct {
	docs   map[string]string
	at     time.Time
	writes []string
}

func (f *fakeOrchestratorStore) Get(_ context.Context, path string, out any) (bool, error) {
	raw, ok := f.docs[path]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal([]byte(raw), out)
}

func (f *fakeOrchestratorStore) GetETag(_ context.Context, path string, out any) (string, bool, error) {
	found, err := f.Get(context.Background(), path, out)
	if !found {
		return "", false, err
	}
	return "etag-1", true, err
}

func (f *fakeOrchestratorStore) ServerNow() (time.Time, bool) { return f.at, !f.at.IsZero() }

func (f *fakeOrchestratorStore) PutIfMatch(_ context.Context, path, _ string, _ any) error {
	f.writes = append(f.writes, path)
	return fmt.Errorf("this store is a reader's: the pass wrote %s", path)
}

// tickForTest serves one tick against the store and returns the recorder and
// what the pass logged. It goes through ServeHTTP and not through tick, because
// the route is part of what is pinned: a pass that wrote nothing but answered
// the wrong request would still be a service.
func tickForTest(t *testing.T, store *fakeOrchestratorStore, id string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var log bytes.Buffer
	srv, err := newResidentOrchestrator(orchestratorConfig{id: id, db: store, log: runner.NewLogger(&log)})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, orchestratorTickPath, nil))
	if len(store.writes) != 0 {
		t.Fatalf("the tick wrote %v: this pass reads the node and writes nothing (the launcher is P5.1d and the consumer is P5.1f)", store.writes)
	}
	return rec, log.String()
}

// residentStateDoc is a state node as the previous pass left it: one work
// item and one intent, at revision 7. It is a literal rather than a marshalled
// State because the reader under test is the strict decoder of the node's own
// package (DecodeState), and a document this test built with the writer's own
// marshaller would agree with that decoder whatever either of them did.
var residentStateDoc = `{
	"schema_version": 5, "rev": 7, "agent_id": "aurora", "role": "orchestrator",
	"goal": "ship the resident orchestrator",
	"work_items": [{"id": "p5-1c", "title": "the resident service", "repo": "example/repo",
		"recipe": "default", "rigor_tier": "medium", "brief": "a brief", "depends_on": [], "phase": "queued", "attempts": 1}],
	"decisions": [], "facts": {}, "archived": [],
	"intents": [{"id": "p5-1c:1", "run_id": "20260101-000000-abcd", "spec_digest": "sha256:0000000000000000000000000000000000000000000000000000000000000000", "approval_at": "2026-11-17T00:00:00Z", "at": "2026-11-17T00:00:00Z"}],
	"counters": {"decisions_hour": 0, "decisions_day": 0}
}`

// TestOrchestratorTickReadsTheNodeThePreviousPassLeft is the pass itself: it
// reads the state node and the lease beside it, out of the store and never out
// of memory, answers the counts it read, and starts no step. This is what
// check 660 measures on a live installation, minus the store it reads there.
func TestOrchestratorTickReadsTheNodeThePreviousPassLeft(t *testing.T) {
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	leasePath, err := orchestrator.PathLease("aurora")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)
	store := &fakeOrchestratorStore{
		docs: map[string]string{
			statePath: residentStateDoc,
			leasePath: `{"holder": "daemon-abc", "at": "2026-11-17T05:00:00Z"}`,
		},
		at: at,
	}
	rec, logged := tickForTest(t, store, "aurora")
	if rec.Code != http.StatusOK {
		t.Fatalf("the tick answered %d (%s), and the node it read is this installation's: %s", rec.Code, rec.Body.String(), logged)
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatalf("the tick's answer is not the pass it read: %v (%s)", err, rec.Body.String())
	}
	if pass.Orchestrator != "aurora" || pass.Rev != 7 || pass.Items != 1 || pass.Intents != 1 {
		t.Errorf("the pass read %+v, and the node holds rev 7 with one item and one intent: a pass that reports anything else is reading something other than the node", pass)
	}
	if pass.LeaseHolder != "daemon-abc" || pass.LeaseAt != "2026-11-17T05:00:00Z" {
		t.Errorf("the pass read the lease as %q at %q, and the store holds daemon-abc at 05:00:00Z: the lease is read from the store and not remembered", pass.LeaseHolder, pass.LeaseAt)
	}
	if !strings.Contains(logged, "tick read the node and ran no step") {
		t.Errorf("the pass logged %q, and the line an operator reads to see a tick is the one naming that it ran no step", logged)
	}
}

// TestOrchestratorTickOnAnEmptyInstallationIsAPass is check 660's own first
// run: a forced tick over a mailbox nobody has written yet, on an installation
// whose node does not exist. The absent node is the orchestrator's EMPTY STATE
// and not an error (ReadState's own rule), so the pass answers, reads a lease
// nobody holds, and starts nothing.
func TestOrchestratorTickOnAnEmptyInstallationIsAPass(t *testing.T) {
	store := &fakeOrchestratorStore{docs: map[string]string{}, at: time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)}
	rec, _ := tickForTest(t, store, "aurora")
	if rec.Code != http.StatusOK {
		t.Fatalf("an empty installation answered %d (%s): a node nobody has written yet is an empty state, not a failure", rec.Code, rec.Body.String())
	}
	var pass orchestratorPass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatal(err)
	}
	if pass.Rev != 0 || pass.Items != 0 || pass.Intents != 0 || pass.LeaseHolder != "" {
		t.Errorf("the empty installation's pass read %+v, and there is nothing in the node to read", pass)
	}
	if pass.SchemaVersion != orchestrator.SchemaVersion {
		t.Errorf("the empty state came back at schema version %d, and the empty state is this release's (%d): a state a later write migrates from is a state a migration was never asked about", pass.SchemaVersion, orchestrator.SchemaVersion)
	}
}

// TestOrchestratorTickRefusesAnotherLoopsDocument is the fence under the pass:
// a node whose document says it is another orchestrator's or another loop's is
// refused (ErrNotThisNode) rather than acted on, and the tick says so to its
// caller rather than reporting a pass over somebody else's work.
func TestOrchestratorTickRefusesAnotherLoopsDocument(t *testing.T) {
	statePath, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []string{
		strings.Replace(residentStateDoc, `"agent_id": "aurora"`, `"agent_id": "somebody-else"`, 1),
		strings.Replace(residentStateDoc, `"role": "orchestrator"`, `"role": "coordinator"`, 1),
	} {
		store := &fakeOrchestratorStore{docs: map[string]string{statePath: doc}, at: time.Now()}
		rec, _ := tickForTest(t, store, "aurora")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("a document of another loop answered %d (%s): the pass must not read somebody else's node as its own", rec.Code, rec.Body.String())
		}
	}
}

// TestOrchestratorTickAnswersOnlyItsOwnRoute: the service has one route, one
// method and no body to read. A GET is a probe or a person and not a tick, and
// anything else is not this service's at all — and none of the refusals reads
// the request, because a route that read one would be one a caller could put
// text into.
func TestOrchestratorTickAnswersOnlyItsOwnRoute(t *testing.T) {
	store := &fakeOrchestratorStore{docs: map[string]string{}, at: time.Now()}
	srv, err := newResidentOrchestrator(orchestratorConfig{id: "aurora", db: store, log: runner.NewLogger(io.Discard)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, orchestratorTickPath, http.StatusOK},
		{http.MethodGet, orchestratorTickPath, http.StatusMethodNotAllowed},
		{http.MethodPost, "/", http.StatusNotFound},
		{http.MethodPost, "/tick/extra", http.StatusNotFound},
		{http.MethodPost, "/v1/messages", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s answered %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
		if len(store.writes) != 0 {
			t.Fatalf("%s %s wrote %v", tc.method, tc.path, store.writes)
		}
	}
}

// TestOrchestratorServeRefusesHTTP2DebugAndBeforeItsEnvironment is the order
// the three in-container processes share: GODEBUG=http2debug makes Go print
// HTTP requests, headers included, to stderr, and this process holds the
// installation's own state and, from P5.1e, the pairing key's one copy — so the
// refusal comes before the environment is read and before anything is served.
// Both ends are asserted: an empty environment is refused for http2debug and
// not for its first missing variable, and a COMPLETE one is refused too, with
// the listener never opened (the context is bounded so that a version of this
// code that served instead of refusing fails rather than hangs).
func TestOrchestratorServeRefusesHTTP2Debug(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "empty", env: map[string]string{}},
		{name: "complete", env: orchestratorServeEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"GODEBUG": "http2debug=2"}
			for k, v := range tc.env {
				env[k] = v
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := serveOrchestrator(ctx, newOrchestratorServeCmd(), envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "http2debug") {
				t.Fatalf("err = %v, want the http2debug refusal", err)
			}
		})
	}
}
