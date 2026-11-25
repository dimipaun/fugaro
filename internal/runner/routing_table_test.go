package runner

// CS6: the run's model path follows the table (docs/model-credentials-routing,
// implementation-plan.md). One test per clause of the task's acceptance:
//
//   - TestEveryStageGoesThroughTheChain — the runner chooses no whole-run
//     upstream from agent.auth: a run the project's routing table serves calls
//     the credentials service through its own gateway in every stage, sends the
//     stage it is in on every call (C1's stage header), and records the chain's
//     own answer — which source served the call, and whether it was the chain's
//     first — on the stage's row and in the live registry entry.
//   - TestAnUnmigratedRepositoryRunsAsItsFileSays — a repository the table does
//     not serve keeps the path its file names: its job carries no service
//     address, its calls are its own credential's, and a `vertex` file's run is
//     the job's own GCP identity, refused under `enforce` as it was (R3, kept).
//
// What decides between the two is one fact and not the auth: whether this run's
// model path is the credentials service's (serviceModelPath), which is the
// switch the launch's own address states for the auths the release-1 bridge
// serves (B2.2/D2072) and the value the gateway's ChainServed option is built
// from (CS6). A `vertex` run is not among them: the service walk's Vertex arm
// serves nothing until CS4 lands (credsvc's KindVertex), so a `vertex` file's
// run keeps the job's own GCP identity whatever its job carries (D1626, R3).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// chainModel is the model every call of these tests asks for, and the canonical
// id the service answers with: nothing here downgrades a model.
const chainModel = "claude-sonnet-5-5"

// chainPool is the source the stand-in service says it served with, in the
// shape a pool's answer carries (routing.SourceName: the kind, and the name of
// the credential that kind names).
var chainPool = routing.SourceName{Kind: routing.KindPool, Name: "team"}

// chainJob is the job of a repository the table serves: the launch's own entry
// for it (the service's address, which is what the switch's auths read), the
// credentials service as a stand-in that answers a served call the way one of
// this contract answers it — the tier and the credential's name it has always
// written, and the chain's own three members — plus the metadata server the
// job's own ID token is minted from. seen returns the calls the service
// received, in order.
func chainJob(t *testing.T) (env []string, seen func() []*http.Request) {
	t.Helper()
	fake, svc := anthropicfake.New(t)
	fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
		rep := anthropicfake.MessageOK(chainModel, pricing.Usage{Input: 1, Output: 3})
		rep.Header = http.Header{
			gateway.HeaderTier:       []string{gateway.TierB},
			gateway.HeaderCredential: []string{"team-shared"},
			gateway.HeaderSource:     []string{`{"kind":"` + string(chainPool.Kind) + `","name":"` + chainPool.Name + `"}`},
			gateway.HeaderFallback:   []string{gateway.FallbackNo},
			gateway.HeaderModel:      []string{chainModel},
		}
		return rep
	}
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "job-id-token-from-the-metadata-stand-in")
	}))
	t.Cleanup(meta.Close)
	return []string{
		credentialsServiceURLEnv + "=" + svc.URL,
		metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
	}, fake.Seen
}

// chainPost is one Messages call of a named stage, through the run's own
// gateway: the agent's own call, exactly as svcPost makes it and with the stage
// the caller names rather than svcPost's fixed one.
func chainPost(t *testing.T, r *run, stage string) {
	t.Helper()
	if r.gwAgent == nil || r.gwAgent.BaseURL == "" {
		t.Fatal("the run's gateway did not start")
	}
	r.gw.BeginStage(gateway.Stage{Name: stage, Model: chainModel})
	req, err := http.NewRequest("POST", r.gwAgent.BaseURL+"/v1/messages",
		strings.NewReader(`{"model":"`+chainModel+`","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", r.gwAgent.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the %s stage's call was answered %d: %s", stage, resp.StatusCode, body)
	}
}

// liveEntry is the run's live registry entry as it stands now: the session's
// own, read under its lock through the one accessor the runner writes it with.
func liveEntry(t *testing.T, sess *budget.Session) budget.AgentEntry {
	t.Helper()
	var got budget.AgentEntry
	sess.Update(func(e *budget.AgentEntry) { got = *e })
	return got
}

// tableRun is a run of a repository the table serves, at the rig the switch's
// own tests use (svcRun, svcSession) and ready to start its gateway.
func tableRun(t *testing.T, auth string) (*run, func() []*http.Request) {
	t.Helper()
	env, seen := chainJob(t)
	r := svcRun(t, auth, func(r *run) {
		r.d.Env = env
		r.spend = Spend{Mode: policy.ModeObserve, Prices: pricing.Embedded(), Cap: 5}
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Status: runstore.StatusRunning}
		r.spec = &task.Spec{Version: 1, RunID: "20261125-085817-abcd", Repo: "acme/app", Ref: "main",
			Workflow: "app", Task: "Add a feature"}
	})
	r.sess = svcSession(t)
	return r, seen
}

// TestEveryStageGoesThroughTheChain (CS6): every stage of a run the table
// serves goes through the run's gateway to the credentials service, and the
// chain's own answer about each call reaches the run — the stage's row in its
// record, and the live registry entry a watcher reads.
//
// The auths are the ones a file can name and the release-1 bridge serves, and
// they all take the same path: the job carries the launch's own entry for a
// repository the table serves (the address, CS10) and from there the runner
// chooses no upstream of its own. `vertex` is not among them: what the service
// serves a chain's Vertex source from is CS4's work, so a `vertex` file's run
// keeps the job's own GCP identity until it lands (D1626, D3174).
//
// What the run declares to the service is the repository's own auth; what
// served the call is the table's answer, and the two are different facts.
func TestEveryStageGoesThroughTheChain(t *testing.T) {
	for _, auth := range []string{"api-key", "oauth", config.AuthCascade} {
		t.Run(auth, func(t *testing.T) {
			r, seen := tableRun(t, auth)
			if !r.serviceModelPath() {
				t.Fatalf("a %s run whose job carries %s is not read as one the credentials service serves", auth, credentialsServiceURLEnv)
			}
			if want := auth != config.AuthCascade; r.serviceSwitched() != want {
				t.Errorf("serviceSwitched() = %v for a %s run, want %v", r.serviceSwitched(), auth, want)
			}
			// And the agent's environment reads the same fact under its own
			// name (the EnvSpec literal in runner.go, agentenv's chainServed):
			// a served run reaches internal/agent as the cascade's whatever its
			// file's auth says, so the agent holds no credential of the job's
			// (D3171).
			if got := r.servedAuth(); !r.chainServed() || got != config.AuthCascade {
				t.Errorf("a %s run the table serves reaches the agent as chainServed=%v auth=%q, want the cascade's environment", auth, r.chainServed(), got)
			}
			up, err := r.gatewayUpstream(context.Background())
			if err != nil {
				t.Fatalf("the upstream of a %s run the table serves: %v", auth, err)
			}
			if up.Kind != gateway.KindService || up.Service == nil {
				t.Fatalf("the upstream of a %s run the table serves is %+v, want the credentials service", auth, up)
			}
			wantMode := auth
			if auth == config.AuthCascade {
				// A cascade run declares nothing: the record the service holds
				// its calls to is that repository's own already (C1.15).
				wantMode = ""
			}
			if up.AuthMode != wantMode {
				t.Errorf("the run declares the auth %q, want %q", up.AuthMode, wantMode)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := r.startGateway(ctx); err != nil {
				t.Fatalf("the gateway of a %s run the table serves: %v", auth, err)
			}
			defer func() {
				c, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				_ = r.gw.Close(c)
			}()

			for i, stage := range []string{"implement", "review"} {
				chainPost(t, r, stage)
				// The service was told which stage the call belongs to: the
				// gateway's own header, set from the run's stage and never
				// from anything the agent wrote (C1's R24 guard).
				calls := seen()
				if len(calls) != i+1 {
					t.Fatalf("the credentials service received %d calls by the %s stage, want %d", len(calls), stage, i+1)
				}
				if got := calls[len(calls)-1].Header.Get(gateway.HeaderStage); got != stage {
					t.Errorf("the %s stage's call declares the stage %q", stage, got)
				}
				// And the answer reached the run: the stage's row carries the
				// source that served its calls.
				r.rec.Stages = append(r.rec.Stages, r.stageTiming(stage, r.d.Now(), 0, 0, 0))
				st := r.rec.Stages[len(r.rec.Stages)-1]
				if st.Source == nil || *st.Source != chainPool {
					t.Fatalf("the %s stage's row names the source %+v, want the service's answer %+v", stage, st.Source, chainPool)
				}
				if st.Fallback {
					t.Errorf("the %s stage's row is flagged a fallback: the answer said the chain's first source served it", stage)
				}
				// The live entry is cleared with the row, so the next stage is
				// never drawn on this one's source.
				if e := liveEntry(t, r.sess); e.Source != "" || e.Fallback {
					t.Errorf("the live entry is %q/%v once the %s stage's row was taken, want nothing recorded", e.Source, e.Fallback, stage)
				}
			}

			// And the entry the run registers with names no auth of its own:
			// the table is what says where its calls go, and every view prints
			// the dash for an absent member (D3070).
			if got := r.registryEntry().Auth; got != "" {
				t.Errorf("a %s run the table serves registers the auth %q, want none", auth, got)
			}
		})
	}
}

// TestTheLiveEntryCarriesTheSource (D3070, CS9's member and CS6's writer): the
// stage's source reaches the live registry entry as `fugaro watch` and the
// console read it — the kind, and the name after a colon when the kind names
// one — and it is the stage's own fold: the flag is true once any call of the
// stage came from a source that is not the chain's first, whatever came last.
func TestTheLiveEntryCarriesTheSource(t *testing.T) {
	r, _ := tableRun(t, "api-key")

	r.chainAnswered(gateway.ChainAnswer{
		Source: routing.SourceName{Kind: routing.KindProvider, Name: "openrouter"}, Fallback: true, Model: chainModel})
	if e := liveEntry(t, r.sess); e.Source != "provider:openrouter" || !e.Fallback {
		t.Fatalf("the live entry is %q/%v after a fallback call, want provider:openrouter/true", e.Source, e.Fallback)
	}
	r.chainAnswered(gateway.ChainAnswer{Source: routing.SourceName{Kind: routing.KindAnthropic}, Model: chainModel})
	if e := liveEntry(t, r.sess); e.Source != "anthropic" || !e.Fallback {
		t.Errorf("the live entry is %q/%v for a stage that fell back and was then served first, want anthropic/true", e.Source, e.Fallback)
	}
}

// TestAnUnmigratedRepositoryRunsAsItsFileSays (CS6, R3 kept): a repository the
// table does not serve — no chain of it reaches this repository, so its launch
// states nothing about the table — runs exactly as its file says. Its model
// path is its own credential's, its upstream is built from the auth the file
// names, a `vertex` file's run is the job's own GCP identity and is refused
// under `enforce` as it always was, and its live registry entry keeps the auth
// the file names.
//
// A job carrying the service's address is a row here, and the one that carries
// it on a `vertex` file is why: a launch hands the address to a job whose
// document it could not read, on the profile's own answer (B5.2, D505, D2078),
// and to a `vertex` repository the release-1 bridge does not serve the address
// is all it can carry. The run in it keeps the job's own GCP identity, which is
// the path that works whatever the profile dropped, and nothing about the
// address may move it (R3).
//
// A cascade file is not a row here: a cascade run has no credential of its own,
// its model path is the service's by construction, and a job that cannot reach
// one stops in bootstrap rather than falling back to a mount (D143).
func TestAnUnmigratedRepositoryRunsAsItsFileSays(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		auth        string
		upstream    string
		refusedPath bool
		// address is the shape a documentless launch produces on a served
		// profile: the job carries the service's address, handed on the
		// PROFILE's answer (internal/cli's credentialsServiceLaunchEnv). No
		// auth's path may move on it alone.
		address bool
	}{
		{name: "an api-key file keeps the job's own key", auth: "api-key", upstream: "anthropic"},
		{name: "a vertex file keeps the job's own identity", auth: "vertex", upstream: "vertex"},
		{name: "a vertex file on a job the address alone was handed", auth: "vertex", upstream: "vertex", address: true},
		{name: "an oauth file has no gateway and no upstream", auth: "oauth", refusedPath: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := codingRun(t, tc.auth, policy.ModeEnforce)
			if !tc.address {
				r = withoutCredentialsService(r)
			}
			if got := envLookup(r.d.Env, credentialsServiceURLEnv); (got != "") != tc.address {
				t.Fatalf("this row's job carries the service's address %q, want it carried=%v: the row is about the address and nothing else", got, tc.address)
			}
			r.d.Env = append(r.d.Env, "ANTHROPIC_API_KEY="+plantedKey)
			r.rec = &runstore.Record{Version: 1, RunID: r.spec.RunID, Status: runstore.StatusRunning}
			if r.serviceModelPath() || r.serviceSwitched() {
				t.Fatalf("an unmigrated %s run is read as one the credentials service serves", tc.auth)
			}
			// Its environment is its file's, too: the same fact the gateway is
			// denied is the one the agent's environment would change on (R3).
			if r.chainServed() || r.servedAuth() != tc.auth {
				t.Errorf("an unmigrated %s run reaches the agent as chainServed=%v auth=%q, want its file's own", tc.auth, r.chainServed(), r.servedAuth())
			}
			if got, want := r.gatewayOn(), !tc.refusedPath; got != want {
				t.Errorf("gatewayOn() = %v for an unmigrated %s run, want %v", got, tc.auth, want)
			}
			if got := r.vertexServedByTheService(); got {
				t.Errorf("an unmigrated %s run's Vertex calls are read as the service's", tc.auth)
			}
			r.sess = svcSession(t)
			up, err := r.gatewayUpstream(ctx)
			if tc.refusedPath {
				if err == nil {
					t.Fatalf("an unmigrated %s run was given the upstream %+v: its credential is the job's own and no gateway carries it", tc.auth, up)
				}
				if !strings.Contains(err.Error(), tc.auth) {
					t.Errorf("the refusal doesn't name the auth: %v", err)
				}
				if up.Kind != "" || up.Service != nil {
					t.Errorf("the refused %s run returned an upstream: %+v", tc.auth, up)
				}
			} else {
				if err != nil {
					t.Fatalf("the upstream of an unmigrated %s run: %v", tc.auth, err)
				}
				if up.Kind != tc.upstream {
					t.Errorf("the upstream kind of an unmigrated %s run is %q, want %q", tc.auth, up.Kind, tc.upstream)
				}
				if up.Service != nil || up.AuthMode != "" || up.Account != "" {
					t.Errorf("an unmigrated %s run reaches the credentials service: %+v", tc.auth, up)
				}
			}
			if tc.auth == "api-key" && up.APIKey != plantedKey {
				t.Errorf("an unmigrated api-key run's upstream carries %q, want the job's own key", up.APIKey)
			}
			if tc.auth == "vertex" && up.Token == nil {
				t.Error("an unmigrated vertex run's upstream has no token source: it authenticates to Vertex as the job itself")
			}
			// D3174 reaches no run the table does not serve: a `vertex` file's
			// Vertex calls are the job's own identity, which nothing in this
			// gateway caps (D1626), and the refusal stands at bootstrap.
			if tc.auth == "vertex" {
				r.spend = enforceSpend()
				if err := r.checkBudget(ctx); !vertexRefused(err) {
					t.Fatalf("an unmigrated vertex run was not refused under enforce: %v", err)
				}
			}
			// And the entry says so: a repository the table does not serve
			// keeps the auth its file names.
			if got := r.registryEntry().Auth; got != tc.auth {
				t.Errorf("an unmigrated %s run registers the auth %q, want its file's own", tc.auth, got)
			}
		})
	}
}
