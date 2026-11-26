package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// vertexLaunchYAML is oauthLaunchYAML with the auth the service does not serve
// (D3326): the repository, the workflow, the git provider and the project are
// byte for byte the same, so the pair is what a test about that one word needs.
// The auth is read live, from the document the run will read, exactly as it is
// for the two served ones.
var vertexLaunchYAML = strings.Replace(oauthLaunchYAML, "auth: oauth", "auth: vertex", 1)

// TestLaunchPutsAddressForVertexRepo is IS9's launch half (D3326 as amended by
// F17): a `vertex` repository of an installation whose credentials service is
// deployed is handed the service's address, and its runs fetch their workflow
// secrets through it — the model path is the one that does not move, because a
// vertex run reads no model credential of the service's and its model calls go
// out on the job's own Google credentials.
//
// It is handed on the installation's own block alone, and NOT on the
// repository's record: localcfg.Repo.CredentialsServiceReady is documented as
// the switch's own answer (localcfg.Repo.CredentialsServiceOn) and is written
// from infra.CredentialsServiceServes, which answers false for `vertex` by
// construction — so a launch that waited for the record would never hand a
// vertex repository anything, and the workflow-secret fetch that is the whole
// point of the address there would never happen. That is why the ready=false
// row below is ON and not off, and it is what the acceptance means by
// `whenever the installation has a service`.
//
// The held-off rows are the other half of F17: the address is what opens a
// repository's git provider keyless (providers.FromEnv) and what makes it
// proxied (proxiedRepo), so it reaches the one auth D3326 names and no other
// repository the switch holds off — no api-key or oauth repository whose own
// apply has not recorded it on the switch, and none of an installation whose
// block is not there at all. The published mode is carried through the rows
// and decides nothing: it is not a condition of the switch any more (AB15,
// D3009).
func TestLaunchPutsAddressForVertexRepo(t *testing.T) {
	want := envCredentialsServiceURL + "=" + launchCredsURL
	for name, tc := range map[string]struct {
		yaml      string
		url       string
		mode      string
		published bool
		ready     bool
		on        bool
	}{
		"a vertex repository whose root no apply recorded":     {vertexLaunchYAML, launchCredsURL, "", false, false, true},
		"a vertex repository whose record vouches":             {vertexLaunchYAML, launchCredsURL, config.BudgetEnforce, true, true, true},
		"a vertex repository of an installation with none":     {vertexLaunchYAML, "", config.BudgetEnforce, true, true, false},
		"an api-key repository whose root recorded no service": {apiKeyLaunchYAML, launchCredsURL, config.BudgetObserve, true, false, false},
		"an oauth repository whose root recorded no service":   {oauthLaunchYAML, launchCredsURL, "", false, false, false},
	} {
		plain, secret := launchWithCredentialsServiceMode(t, tc.yaml, tc.url, tc.ready, tc.mode, tc.published)
		for _, kv := range secret {
			if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
				t.Errorf("%s: the launch carried %q in its secret entries: the address is the service's public address and the audience every ID token is minted for, and Batch refuses a secret entry outright (backend.LaunchSpec.SecretEnv)", name, kv)
			}
		}
		if tc.on {
			if len(plain) != 1 || plain[0] != want {
				t.Errorf("%s: the launch's plain Env = %v, want exactly [%s]: a vertex repository of an installation that HAS the service is handed the address whatever its record and the project's mode say (its runner fetches its workflow secrets from there; D3326, F17)", name, plain, want)
			}
			continue
		}
		if len(plain) != 0 {
			t.Errorf("%s: the launch's plain Env = %v, want none: the address is what opens the git provider keyless and makes a repository proxied, and the one auth D3326 hands it to is `vertex` — a repository the switch holds off keeps the path it has", name, plain)
		}
	}
}

// TestLaunchPutsAddressForVertexWithoutADocument is the console half of the
// acceptance, and it is the half no document covers: `fugaro run --repo` from
// a directory holding no checkout of the repository — what the desktop console
// spawns — has no `agent.auth` to read, so credentialsServiceLaunchEnv stands
// one of two other answers in for it, the repository's own record and this
// machine's tfvars of its root (deployed). For a vertex repository both
// describe the MODEL switch and are false by construction — the record because
// infra.CredentialsServiceServes answers false for that auth, the tfvars
// because the console's machine applied no root of that repository at all — so
// a launcher reading either handed a vertex run nothing while the
// installation's own apply had just granted its account roles/run.invoker, and
// the run's bootstrap then fetched no workflow secret: exactly the shortfall
// the acceptance's `whenever the installation has a service` rules out. The
// auth it could not read comes off the entry the local config publishes for
// the repository instead (localcfg.Repo.Vertex, written from the auth the
// spec builder decides the address with), and the address is then the
// installation's own block and nothing else, as CredentialsServiceAddress's
// vertex arm has it.
//
// The second row is the designed record of a vertex repository — false — and
// the third is F17's floor: a vertex repository of an installation with no
// block at all is handed nothing, because there is no address to hand it. The
// last row is the control that the stand-in is the vertex auth's alone: a
// repository whose entry carries no vertex flag is judged on the two answers
// it always was, and with neither of them saying on it is handed nothing.
func TestLaunchPutsAddressForVertexWithoutADocument(t *testing.T) {
	want := envCredentialsServiceURL + "=" + launchCredsURL
	block := &localcfg.CredentialsService{URL: launchCredsURL}
	for name, tc := range map[string]struct {
		lc       *localcfg.Config
		deployed bool
		on       bool
	}{
		"a vertex repository, no record and no tfvars of its root": {
			lc: &localcfg.Config{CredentialsService: block, Repos: map[string]localcfg.Repo{"acme/app": {Vertex: true}}},
			on: true,
		},
		"a vertex repository whose record is the designed false": {
			lc: &localcfg.Config{CredentialsService: block, Repos: map[string]localcfg.Repo{"acme/app": {Vertex: true, CredentialsServiceReady: localcfg.RecordSwitch(false)}}},
			on: true,
		},
		"a vertex repository of an installation with no service": {
			lc: &localcfg.Config{Repos: map[string]localcfg.Repo{"acme/app": {Vertex: true}}},
		},
		"a repository that is not a vertex one, and neither stand-in": {
			lc: &localcfg.Config{CredentialsService: block, Repos: map[string]localcfg.Repo{"acme/app": {}}},
		},
	} {
		got := credentialsServiceLaunchEnv(tc.lc, "", "acme/app", "", "", tc.deployed, false)
		if tc.on {
			if len(got) != 1 || got[0] != want {
				t.Errorf("%s: credentialsServiceLaunchEnv = %v, want [%s]: a console launch reads no document, and a vertex repository's address is the installation's block alone — neither the record nor this machine's tfvars can answer for it, both being the model switch and both false for this auth (D3326, F17)", name, got, want)
			}
			continue
		}
		if len(got) != 0 {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want none: the vertex arm reaches the one auth D3326 names, and a repository the switch holds off keeps the path it has", name, got)
		}
	}
}

// TestLaunchAddressIsThePlanAddressForVertex is the one-function half of the
// acceptance: the launch answers from infra.CredentialsServiceAddress, the very
// function the repository root's tfvars are built from, so a job is handed an
// address exactly where its root granted its account roles/run.invoker — and is
// never left to fetch from a service its root granted it but no launch named.
//
// The table walks the states the two share — the installation's block, the
// repository's record and the auth — and reads the launch's own answer out of
// the helper rather than restating it. A documentless launch (auth "") is
// deliberately not a row here: that launcher reads a fourth stand-in beside the
// switch, this machine's tfvars of the root it applied
// (credentialsServiceLaunchEnv's `deployed`), which is a repository root's own
// answer and has no counterpart in the plan's function.
func TestLaunchAddressIsThePlanAddressForVertex(t *testing.T) {
	for name, tc := range map[string]struct {
		block bool
		ready bool
		auth  string
	}{
		"vertex, the service deployed":            {true, false, "vertex"},
		"vertex, a record that says ready too":    {true, true, "vertex"},
		"vertex, no service at all":               {false, false, "vertex"},
		"api-key, a record that vouches":          {true, true, config.AuthAPIKey},
		"api-key, no record":                      {true, false, config.AuthAPIKey},
		"cascade, the service deployed and ready": {true, true, config.AuthCascade},
		"cascade, no record":                      {true, false, config.AuthCascade},
		"oauth, a record that vouches":            {true, true, config.AuthOAuth},
		"oauth, no service at all":                {false, false, config.AuthOAuth},
	} {
		lc := &localcfg.Config{}
		if tc.block {
			lc.CredentialsService = &localcfg.CredentialsService{URL: launchCredsURL}
		}
		if tc.ready {
			lc.Repos = map[string]localcfg.Repo{"acme/app": {CredentialsServiceReady: localcfg.RecordSwitch(true)}}
		}
		ready := lc.Repos["acme/app"].CredentialsServiceOn()
		// The launcher's own call site, with the arguments the launch reads: the
		// block's presence and URL are "installed" here, the record's answer is
		// what localcfg reads off the repository's entry, the published mode is
		// empty because it decides nothing any more (D3009), and deployed is
		// false because the auth is in hand.
		env := credentialsServiceLaunchEnv(lc, "", "acme/app", tc.auth, "", false, false)
		want := infra.CredentialsServiceAddress(tc.block, ready, tc.auth)
		got := len(env) == 1 && env[0] == envCredentialsServiceURL+"="+launchCredsURL
		if got != want {
			t.Errorf("%s: the launch handed the job %v while infra.CredentialsServiceAddress says %v (installed=%v, auth=%q): the root's tfvars and this launch read one function, so a repository cannot be told about a service its account holds no roles/run.invoker on, nor left to fetch from one its root granted it (D3326)", name, env, want, tc.block, tc.auth)
		}
	}
}
