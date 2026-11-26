package infra

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// readiness_switch_test.go is AB15's own tests (docs/always-a-budget): the
// credentials switch a repository root's Readiness is gated on no longer reads
// the budget mode, so what a repository's runs may reach the service for is its
// auth and the bundle and nothing else (D3009, F15).
//
// The whole change is one argument leaving one function, and the two halves
// below are its two sides: the signature the callers follow, and the state the
// answer moves in — a repository a node holding the word `off` used to keep off
// the switch, mounts and all, and which the apply that follows now serves like
// any other.
func TestTheSwitchNoLongerReadsTheMode(t *testing.T) {
	// The red step of the task: the function still has four parameters, the
	// fourth the published node's budget mode.
	if got := reflect.TypeOf(CredentialsServiceServes).NumIn(); got != 3 {
		t.Fatalf("CredentialsServiceServes takes %d arguments, want 3 (installed, ready, auth): the budget mode is not a condition of the switch any more, and a caller that hands it one is a caller that still believes it is (AB15, D3009)", got)
	}

	// The state the change is about, through the spec a repository root is
	// deployed from: a node that says the word. Every `api-key` and `oauth`
	// repository of such a node was held off — no block, no invoker grant, every
	// mount kept — and is served now, which is the state the update's listing
	// names a model key for (F26).
	for _, tc := range []struct{ name, agent, lc string }{
		{name: "oauth", agent: repoRootOAuthAgent, lc: repoRootLC},
		{name: "api-key", agent: `agent: { auth: api-key }`, lc: repoRootAPIKeyLC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offIn := repoRootInputs(t, tc.agent)
			offIn.LC = parseLC(t, tc.lc)
			off, err := Repo(offIn)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"off", ""} {
				on := repoRootServed(t, tc.agent, tc.lc, mode)
				if on.Installation.CredentialsService == nil {
					t.Errorf("with the published mode %q the repository root carries no credentials service, want the block: the mode is not a condition of the switch, so a repository whose auth the service serves and whose bundle the record vouches for is served whatever the node says (AB15, D3009)", mode)
					continue
				}
				if on.ModelSecretsServed != config.ModelSecretServedAnthropic {
					t.Errorf("with the published mode %q the root plans model_secrets_served = %q, want %q: the job's two Claude mounts go with the block (D2072)", mode, on.ModelSecretsServed, config.ModelSecretServedAnthropic)
				}
				for key, offWS := range off.Workflows {
					got, want := modelMountsOf(on.Workflows[key]), withoutClaudeMounts(modelMountsOf(offWS))
					if len(claudeMountsOf(modelMountsOf(offWS))) == 0 {
						t.Fatalf("the %s job mounts no Claude credential with no service at all (%v): the assertion below would pass vacuously", key, got)
					}
					if !slices.Equal(got, want) {
						t.Errorf("with the published mode %q the %s job mounts %v, want %v: a switched repository gives up the two Claude mounts and keeps every other one (D2072, R6)", mode, key, got, want)
					}
				}
				if !slices.ContainsFunc(on.Addresses(), func(a string) bool {
					return strings.Contains(a, "google_cloud_run_v2_service_iam_member.credentials[0]")
				}) {
					t.Errorf("with the published mode %q no job carries the invoker grant on the service, want one per job whose mount was dropped: the switch's answer is what makes the call possible at all (D135)", mode)
				}
			}
		})
	}

	// The auth no mode ever moved: a vertex repository's calls are the project's
	// own GCP service and no credential of the service's.
	if CredentialsServiceServes(true, true, "vertex") {
		t.Error("CredentialsServiceServes(installed, ready, vertex) = true, want false: a vertex repository's calls are the project's own GCP service and no credential of the service's")
	}

	// The caller that read no node at all (fugaro image, fugaro doctor,
	// --print-vars): the mode used to be the fail-closed answer here, and the
	// switch asks the block and the record alone now.
	in := repoRootInputs(t, repoRootOAuthAgent)
	in.LC = parseLC(t, repoRootLC)
	in.Installation.CredentialsService = credentialsService()
	serviceReady(t, in.LC, in.Repo)
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Installation.CredentialsService == nil {
		t.Error("the caller that read no node carries no credentials service, want the block: the switch no longer answers a mode for a caller that read none (AB15, D3009)")
	}
}
