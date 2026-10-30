// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The coordinator's own read of the Bitbucket API root (D474, the runner's rule
// of T2.8 held here as well). The coordinator opens its providers through
// providers.FromEnv, the same tool a run opens its own with, and that tool
// honours FUGARO_BITBUCKET_API_URL whatever it names: the refusal has to be the
// coordinator's, asked before the opener exists, or the project-wide read would
// send a Bitbucket token to the host a launcher of the job pointed it at.
//
// The tests drive the commands rather than the helper, because where the check
// sits is half of it: the refusal lands before the opener is built, and every
// read of the project -- the status command and the sweep job's tick -- asks it.

// coordinatorPolicyFixture points the fixture's project config at a database of its
// own and returns it, so a test decides what the published policy says.
func coordinatorPolicyFixture(t *testing.T, f *coordinatorFixture) *gcpfake.RTDB {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	db.Set("fugaro/project", "aurora")
	f.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+db.URL+" }\n")
	return db
}

// publishCoordinatorPolicy writes a published policy carrying roots into db, as
// policy publish leaves it: the rules generation that carries the policy nodes,
// and the document itself.
func publishCoordinatorPolicy(t *testing.T, db *gcpfake.RTDB, roots ...string) {
	t.Helper()
	doc := map[string]any{"version": projectpolicy.Version}
	if len(roots) > 0 {
		doc["bitbucket_api_roots"] = roots
	}
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
	db.Set(projectpolicy.PathShared(), doc)
}

// TestCoordinatorRefusesAnotherBitbucketAPIRoot is the red step: today the
// coordinator opens its provider with whatever root FUGARO_BITBUCKET_API_URL
// names, so a launcher who overrode the job's environment could have the
// project-wide read send its Bitbucket token to a host of their own. The
// refusal names the variable, the refused root by its host alone, and the
// remedy.
func TestCoordinatorRefusesAnotherBitbucketAPIRoot(t *testing.T) {
	f := newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	publishCoordinatorPolicy(t, coordinatorPolicyFixture(t, f))
	t.Setenv(providers.EnvBitbucketAPIURL, "https://bitbucket.example.com/2.0")

	_, _, err := execute(t, "coordinator", "status", "--json", "--since", "0")
	if err == nil {
		t.Fatal("the coordinator read the project with a Bitbucket API root no policy allows")
	}
	for _, want := range []string{providers.EnvBitbucketAPIURL, "bitbucket.example.com", "fugaro project policy publish"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "https://bitbucket.example.com/2.0") {
		t.Errorf("the refusal prints the whole root rather than its host: %v", err)
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("exit %d, want the owner's own error: %v", code, err)
	}
}

// TestCoordinatorAcceptsAnOwnerListedRoot: a root the owner published in
// bitbucket_api_roots is the one other place the token may go, so the
// project-wide read runs as it always did.
func TestCoordinatorAcceptsAnOwnerListedRoot(t *testing.T) {
	const root = "https://bitbucket.example.com/api"
	f := newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	publishCoordinatorPolicy(t, coordinatorPolicyFixture(t, f), root)
	t.Setenv(providers.EnvBitbucketAPIURL, root)

	out, errOut, err := execute(t, "coordinator", "status", "--json", "--since", "0")
	if err != nil {
		t.Fatalf("a coordinator whose root the policy allow-lists could not read the project: %v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, "acme/app") {
		t.Errorf("the view is not the fixture's:\n%s", out)
	}
}

// TestCoordinatorDefaultRootUnchanged: Bitbucket's own API is always allowed,
// whatever the policy says -- and this fixture's project has no database in its
// config at all, so a command that resolved the default root without reading a
// node is also what passes here.
func TestCoordinatorDefaultRootUnchanged(t *testing.T) {
	newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	t.Setenv(providers.EnvBitbucketAPIURL, bitbucket.DefaultBaseURL)

	if _, errOut, err := execute(t, "coordinator", "status", "--json", "--since", "0"); err != nil {
		t.Fatalf("a coordinator on Bitbucket's own API could not read the project: %v (stderr: %s)", err, errOut)
	}
}

// TestCoordinatorRefusalBeforeAnyRequest: the refusal lands before the opener
// is built, and building it is the only way this read could reach a host at
// all. The tripwire is the opener itself: reached, the test fails, and the
// error it hands back would be reported instead of the refusal.
func TestCoordinatorRefusalBeforeAnyRequest(t *testing.T) {
	f := newCoordinatorFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	publishCoordinatorPolicy(t, coordinatorPolicyFixture(t, f))
	t.Setenv(providers.EnvBitbucketAPIURL, "https://evil.example/2.0")

	var opened bool
	setCoordinatorHost(t, func([]string, func(string)) (coordinator.ProviderOpener, error) {
		opened = true
		return nil, errors.New("the provider opener was built")
	})

	_, _, err := execute(t, "coordinator", "status", "--json", "--since", "0")
	if opened {
		t.Error("the coordinator built its provider opener before refusing the API root")
	}
	if err == nil || !strings.Contains(err.Error(), providers.EnvBitbucketAPIURL) {
		t.Fatalf("the read did not stop on the refused root: %v", err)
	}
}

// TestCoordinatorSweepRefusesAnotherBitbucketAPIRoot: the aggregation job is
// the invocation that holds the environment a launcher writes, so the tick asks
// the same question -- and a tick that was refused reads nothing and writes
// nothing rather than sweeping the project with a root it may not use.
func TestCoordinatorSweepRefusesAnotherBitbucketAPIRoot(t *testing.T) {
	f := newCoordinatorSweepFixture(t, coordinatorSeeds(), coordinatorPRs()...)
	publishCoordinatorPolicy(t, f.db)
	t.Setenv(providers.EnvBitbucketAPIURL, "https://bitbucket.example.com/2.0")

	_, _, err := execute(t, "coordinator", "sweep")
	if err == nil || !strings.Contains(err.Error(), providers.EnvBitbucketAPIURL) {
		t.Fatalf("the tick did not stop on the refused root: %v", err)
	}
	if view, _ := f.view(); view != nil {
		t.Errorf("a refused tick wrote a view: %v", view)
	}
}
