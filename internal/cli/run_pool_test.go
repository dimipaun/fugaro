package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/credsvc"
)

// poolTableYAML is the table a launch of the fixture's repository is judged on
// where the test needs --pool to be a name that resolves: the run's models are
// claude-* (the installation's defaults), the entry's chain names the
// launcher's pool, and the other two sources serve nothing of what these tests
// read.
const poolTableYAML = `model_routing:
    - match: claude-*
      chain:
        - kind: pool
          pool: team
        - kind: launcher-pool
`

// poolLessTableYAML routes the run's models to a named pool alone: no chain of
// the run names the launcher's pool, which is the state --pool has nothing to
// resolve to (D3176).
const poolLessTableYAML = `model_routing:
    - match: claude-*
      chain:
        - kind: pool
          pool: team
`

// poolElsewhereTableYAML is a table that serves another repository alone: it
// serves none of acme/app's sources, which is the release-1 bridge (O7, R25,
// D3196) — the repository goes out the old way, and the flags of the old way
// mean what they always did.
const poolElsewhereTableYAML = `model_routing:
    - match: claude-*
      chain:
        - kind: vertex
          admit:
            - other/repo
`

// poolLaunchRepoYAML is the checkout the pool tests launch: the cascade
// document — the one agent.auth the credentials service serves, so the launch
// reaches its grant — with the run's model written down. The gates --pool is
// judged by read the models the run can call, and a document that leaves
// agent.model to layers this launcher does not read names none.
//
// agent.model is a lifted key (R1, D3237): a repository's file still writes it
// and this release still reads it, and a job image older than release A —
// l8Since, which is where it stops being honoured — is what L8's own gate
// refuses (l8LiftedKeyLaunch). poolLaunchFixture therefore moves the fixture's
// base image to l8Since, so the refusal a test reads is the pool's and not that
// one's.
const poolLaunchRepoYAML = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: cascade
  model: claude-sonnet-5-5
workflows:
  web:
    checkout: clone
    commands: { build: sh build.sh, test: sh test.sh }
`

// poolLaunchFixture is the installation the pool tests launch on: the cascade
// project job (the shared job a switched repository's run lands on), the
// credentials service's block and the repository's own record — the two halves
// of the switch (infra.CredentialsServiceServes) — and the project's routing
// table, which is the caller's.
func poolLaunchFixture(t *testing.T, table string) *projectJobFixture {
	t.Helper()
	f := newCascadeProjectJobFixture(t, poolLaunchRepoYAML)
	f.rewriteConfig(t, "fugaro-base/fugaro-base:"+nodeRelease, "fugaro-base/fugaro-base:"+l8Since)
	f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
	f.appendConfig(t, table)
	markRepoCredentialsServiceReady(t, f.cloudFixture)
	return f
}

// poolGrant is the one grant call a launch under test makes, recorded: what the
// launcher said about the run it is starting, which is where the pool this
// launch named is read (C2's named_pool).
type poolGrant struct {
	req   credsvc.SlipGrantRequest
	calls int
}

// recordPoolGrant answers the grant call the launch under test makes, and
// records it. The call is faked for the reason the slip tests fake it: the
// fixture's URL is a record nothing dials, so the request the launcher really
// builds is what a test can assert on.
func recordPoolGrant(t *testing.T) *poolGrant {
	t.Helper()
	g := &poolGrant{}
	prev := slipGrant
	slipGrant = func(_ context.Context, _ string, req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		g.req, g.calls = req, g.calls+1
		return credsvc.SlipGrantResponse{Slip: fakeSlipID, MaxRuntimeSeconds: req.MaxRuntimeSeconds}, nil
	}
	t.Cleanup(func() { slipGrant = prev })
	return g
}

// TestTokenFlagIsRejectedWithTheFix: --token picks the account a run's job
// mounts, which is the release-1 way of choosing a credential, and a repository
// the project's routing table serves takes its credential from the chain its
// models' entries name instead (D3176): the flag is refused there, with the fix
// naming --pool, before the claim and the task like every gate of the launch.
//
// A repository the table does not serve is the bridge (O7, R25, D3196): its run
// goes out the old way, so --token means what it always did — the second arm
// pins that, so the refusal is read as the table's own condition and not as the
// flag being gone.
func TestTokenFlagIsRejectedWithTheFix(t *testing.T) {
	t.Run("a repository the table serves", func(t *testing.T) {
		f := poolLaunchFixture(t, poolTableYAML)
		g := recordPoolGrant(t)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090000-c512", "--token", "acme-personal", "Add a feature")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("--token on a repository the table serves: err = %v, want a refusal naming --pool", err)
		}
		for _, want := range []string{"--token", "--pool"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Errorf("the refused launch started %d executions, want none: the refusal runs before the claim", n)
		}
		if g.calls != 0 {
			t.Errorf("the refused launch asked for %d grant(s), want none", g.calls)
		}
	})

	t.Run("a repository the table does not serve", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig)
		f.appendConfig(t, poolElsewhereTableYAML)
		oauthCheckout(t, f, "")
		writeBuildRecord(t, f, otherSlug, "svc", releaseSeal)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261126-090001-c513", "--token", "acme-overflow", "A task"); err != nil {
			t.Fatalf("--token on a repository the table serves none of: %v, want the release-1 launch the flag has always been", err)
		}
		if got := otherSpec(t, f, "20261126-090001-c513").OAuthToken; got != "acme-overflow" {
			t.Fatalf("OAuthToken = %q, want acme-overflow: the bridge keeps the flag's own meaning (O7, R25)", got)
		}
	})
}

// TestPoolFlagNeedsALaunchersPoolInAChain: --pool names the pool "the
// launcher's pool" resolves to for this run (D3176, C2's named_pool), so it is
// a name that resolves only where an entry some model of the run matches has a
// chain naming the launcher's pool. A launch whose models' chains name none is
// refused with the fix: the name would be carried into a grant no chain reads,
// and the run served from a pool its own project does not route.
//
// Where a chain does name it, the launch carries the name on the run's grant
// request and nowhere else — it reorders no chain, and it is not a credential.
func TestPoolFlagNeedsALaunchersPoolInAChain(t *testing.T) {
	t.Run("no chain names the launcher's pool", func(t *testing.T) {
		f := poolLaunchFixture(t, poolLessTableYAML)
		g := recordPoolGrant(t)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090002-c514", "--pool", "acme-pool", "Add a feature")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("--pool where no chain of the run's models names the launcher's pool: err = %v, want a refusal naming the pool and the fix", err)
		}
		for _, want := range []string{"acme-pool", "launcher's pool", "fugaro routing set"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Errorf("the refused launch started %d executions, want none: the gate runs before the claim", n)
		}
		if g.calls != 0 {
			t.Errorf("the refused launch asked for %d grant(s), want none", g.calls)
		}
	})

	t.Run("a chain names it", func(t *testing.T) {
		poolLaunchFixture(t, poolTableYAML)
		g := recordPoolGrant(t)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090003-c515", "--pool", "acme-pool", "Add a feature"); err != nil {
			t.Fatalf("--pool where a chain of the run's models names the launcher's pool: %v", err)
		}
		if g.calls != 1 {
			t.Fatalf("the launch made %d grant calls, want exactly one: a run has one slip", g.calls)
		}
		if g.req.NamedPool != "acme-pool" {
			t.Errorf("the grant named the pool %q, want acme-pool: --pool is the name the run's launcher's pool resolves to (D3176)", g.req.NamedPool)
		}
		if g.req.Run != "20261126-090003-c515" {
			t.Errorf("the grant is for %q, want this launch's run", g.req.Run)
		}
	})
}

// TestAFollowUpUsesItsOwnLaunchersPool: a follow-up is a launch of its own, so
// the pool it is served from is the one ITS launcher names — with --pool, or
// the person's own designation when the flag is absent (D3176). The pool a
// paused run's launch named is kept by the service's record of that run, which
// is what a resume goes on (D3191); the launcher never carries it into the next
// run, so a follow-up's grant says what this person's launch chose and never
// what the run before it chose.
func TestAFollowUpUsesItsOwnLaunchersPool(t *testing.T) {
	const fu2 = "20261126-090004-c516"
	// One chain per pull request: a follow-up leaves its pull request live, and
	// a second launch of the same one is refused while the first is going.
	namedRoot, unnamedRoot := wallRunIDAt(3, "090000", "aaaa"), wallRunIDAt(3, "090000", "bbbb")
	f := poolLaunchFixture(t, poolTableYAML)
	for _, root := range []struct {
		id string
		pr int
	}{{namedRoot, 7}, {unnamedRoot, 8}} {
		seedLaunchedOn(t, f.cloudFixture, appSlug, root.id, f.job)
		writeRecord(t, f.cloudFixture, root.id, prRecord(root.id, "", root.pr, 1))
	}

	named := recordPoolGrant(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--pool", "acme-pool", "more work"); err != nil {
		t.Fatalf("a follow-up that names its own launcher's pool: %v", err)
	}
	if named.req.NamedPool != "acme-pool" {
		t.Errorf("the follow-up's grant named the pool %q, want acme-pool: the pool is the one this launch named", named.req.NamedPool)
	}
	if named.req.Run != fuID || named.req.Run == namedRoot {
		t.Errorf("the follow-up's grant is for %q, want its own run %s: the pool a previous run named is the service's record of that run, never a name this launch hands on (D3191)", named.req.Run, fuID)
	}

	unnamed := recordPoolGrant(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "8", "--run-id", fu2, "more work"); err != nil {
		t.Fatalf("a follow-up that names no pool: %v", err)
	}
	if unnamed.req.NamedPool != "" {
		t.Errorf("the follow-up's grant named the pool %q, want none: a follow-up that names none is served from its launcher's own designation, and never from the pool the run before it named", unnamed.req.NamedPool)
	}
}
