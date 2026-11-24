package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/routing"
)

// chainLaunchYAML is projectJobRepoYAML's checkout with the run's model written
// down: the gate is judged on the models the rungs can choose, and a fixture
// that leaves agent.model to the installation's default names a model this test
// cannot spell. It is a repository the credentials service does not serve (an
// api-key one, the fixture's own policy), so nothing but the table's own gate
// is asked about it.
const chainLaunchYAML = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
  model: claude-sonnet-5-5
  review_rounds: 2
workflows:
  web:
    checkout: clone
    commands:
      build: sh build.sh
      test: sh test.sh
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// chainTableYAML is a project's first table, written the way `fugaro routing
// set` writes one: one entry for the model the cascade fixture's run calls,
// over three sources of which the first is admitted to another repository
// alone. That source is the one the grant must leave out — the service refuses
// a grant naming a source the table does not give this repository — and the
// other two are the ones it must ask for.
const chainTableYAML = `model_routing:
    - match: claude-*
      chain:
        - kind: vertex
          admit:
            - other/repo
        - kind: pool
          pool: team
        - kind: launcher-pool
`

// chainUnroutedYAML routes a model no run of the fixture's repository calls,
// which is the state the launch has to refuse: the table is published, so the
// repository's models are the table's to route, and this one is routed nowhere.
const chainUnroutedYAML = `model_routing:
    - match: gpt-*
      chain:
        - kind: pool
          pool: team
`

// TestRunGrantsByTheTable: a launch on a project that publishes a routing table
// asks the credentials service for the sources this run's models can reach by
// it — the entries its models resolve to, each chain reduced to the sources
// that may serve this repository (C2's GrantSource, R4, D3171) — and hands the
// job the service's address beside the slip.
//
// A source this repository is not admitted to is left out of the request and
// not merely unused: the service refuses a grant naming a source the table does
// not give the repository (CS14), so one such name would be the whole grant
// refused rather than one source of it skipped. The pool kinds are asked for
// whatever the repository, since which member serves them is the walk's answer
// at the call and not a fact the launcher can read (D3180, D3182).
//
// Mutation (verified, run and restored): sending the entry's chain whole — the
// vertex source with it — asks the service for a source the table admits to
// other/repo alone.
func TestRunGrantsByTheTable(t *testing.T) {
	r := newSlipLaunchRigOver(t, func(t *testing.T, f *launchBudgetFixture) {
		projectJobCheckoutYAML(t, cascadeLaunchYAML)
		f.appendConfig(t, chainTableYAML)
	})
	plain := r.launch(t, func(req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		return credsvc.SlipGrantResponse{Slip: fakeSlipID, MaxRuntimeSeconds: req.MaxRuntimeSeconds}, nil
	})
	want := []creds.GrantSource{
		{Kind: string(routing.KindPool), Pool: "team"},
		{Kind: string(routing.KindLauncherPool)},
	}
	if got := r.req.Sources; !slices.Equal(got, want) {
		t.Errorf("the grant asked for %+v, want %+v: the sources the run's models reach by the table, minus the ones this repository is not admitted to", got, want)
	}
	if want := []string{envCredentialsServiceURL + "=" + launchCredsURL, envSlip + "=" + fakeSlipID}; !slices.Equal(plain, want) {
		t.Errorf("the launch handed the job %v, want %v: a run the table serves is told about the service and carries its slip", plain, want)
	}
	if r.warned.Len() != 0 {
		t.Errorf("the launch warned about a grant it was given: %s", r.warned)
	}
}

// TestRunGrantsNothingWithoutATable: an installation that publishes no table is
// the release-1 one, and its grant is the one it always was — the credentials
// the run's configuration selects, and no sources member at all (omitempty,
// which is what an empty list would fail to be). The table is read from the
// local config and a project that publishes none reaches the same answer twice
// over: the launch asks for no source, and what it sends carries no member for
// a service one release behind to refuse.
func TestRunGrantsNothingWithoutATable(t *testing.T) {
	r := newSlipLaunchRig(t)
	r.launch(t, func(req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		return credsvc.SlipGrantResponse{Slip: fakeSlipID, MaxRuntimeSeconds: req.MaxRuntimeSeconds}, nil
	})
	if got := r.req.Sources; len(got) != 0 {
		t.Errorf("an installation with no table had the grant ask for %+v, want no sources member at all", got)
	}
	// The wire shape and not only the field: a launcher that sent an empty list
	// would put `"sources":[]` on a service one release behind, which reads the
	// member as a grant naming no source of the new kinds.
	b, err := json.Marshal(r.req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sources") {
		t.Errorf("the grant this launch sent carries %s, want no sources member at all", b)
	}
}

// TestRunRefusesAnUnroutedModel: a launch of a project that publishes a table —
// and whose table serves this repository — is refused, with the fix and before
// the claim, where no entry matches every model the run can call (R22): the
// task's grade, a level's own model, a craft's model, a pinned role of
// agent.models, agent.model (craft.ModelRungs) and the model override a task
// carries (task.Overrides.Model, the lowest rung of the override family, which
// no launcher in this build writes and a task's own bytes may). A model the
// table does not route is a run with no source to be served from at all, and a
// run that started anyway would stop at its first call with nothing to fix.
//
// The override is the rung no file reaches: a table that routes the
// repository's own model and not the task's override is a run whose every stage
// but the override's would be served. Nothing on the command line writes that
// field, so that arm asks the gate the launch asks, with the spec a launch is
// handed.
//
// Mutation (verified, run and restored): dropping the gate from
// freshLaunchGates launches both arms — a run with no source to be served from
// is started, execution and all.
func TestRunRefusesAnUnroutedModel(t *testing.T) {
	const runID = "20261022-090000-ca5c"

	t.Run("the run's own model is unrouted", func(t *testing.T) {
		f := newProjectJobFixtureMode(t, "enforce")
		f.appendConfig(t, chainUnroutedYAML)
		projectJobCheckoutYAML(t, chainLaunchYAML)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", runID, "Add a feature")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("a launch whose model the table does not route: err = %v, want a refusal naming the model and the fix", err)
		}
		for _, want := range []string{"claude-sonnet-5-5", "fugaro routing set"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Errorf("the refused launch started %d executions, want none: the gate runs before the claim", n)
		}
	})

	t.Run("the task's model override is unrouted", func(t *testing.T) {
		r := newSlipLaunchRigOver(t, func(t *testing.T, f *launchBudgetFixture) {
			projectJobCheckoutYAML(t, cascadeLaunchYAML)
			f.appendConfig(t, chainTableYAML)
		})
		r.spec.Overrides.Model = "gpt-5"
		err := r.env.chainRoutingGate(context.Background(), r.spec)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("a launch whose override the table does not route: err = %v, want a refusal naming the override and the fix", err)
		}
		for _, want := range []string{"gpt-5", "fugaro routing set"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
	})
}
