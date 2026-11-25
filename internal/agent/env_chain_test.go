package agent

// CS7: the agent's environment where the credentials service serves the run
// (docs/model-credentials-routing, implementation-plan.md). One test per clause
// of the task's acceptance:
//
//   - TestTheAgentHoldsNoCredentialUnderAChain — where the gateway serves the
//     run (EnvSpec.ChainServed, C1's gateway.ChainServed), the environment
//     carries no subscription token and no key of the job's, whatever the
//     file's auth says, and presents the gateway's own per-run token instead.
//   - TestAnUnmigratedRepositoryKeepsTheOldEnvironment — the same spec with the
//     fact false is the environment this release already builds for a
//     repository the table does not serve: an oauth run's own token, and the
//     oauth arm's refusal of a gateway.
//
// The fact is the option's own name (C1) rather than a runner path: nothing in
// this package knows how a run reached its model path (the plan's ordering edge
// CS7 → CS6 is joined by the contract), and CS6 is what spells the same fact for
// the gateway itself.

import (
	"slices"
	"strings"
	"testing"
)

// TestTheAgentHoldsNoCredentialUnderAChain (CS7, D3171): a run the credentials
// service serves is built as the cascade's — no credential of the job's in the
// environment, the gateway required, and its per-run token what the agent
// presents — whatever auth the repository's file still names. Every arm that
// would hand the agent a credential of its own is out of reach: the oauth arm
// that mounts the subscription token, and the api-key arm that leaves a
// declared key's value in place.
//
// Red step: an oauth spec under a chain is refused with "auth: oauth never goes
// through the gateway" today, and without a gateway its arm puts the token in
// the environment.
//
// Hand mutation this fails on: reading spec.Auth instead of the chain's spelling
// (Auth: spec.Auth in place of auth), which puts the subscription token back in
// a served run's environment and points its calls at Anthropic rather than at
// the gateway the ledger is kept by.
func TestTheAgentHoldsNoCredentialUnderAChain(t *testing.T) {
	token := "oauth-" + strings.ReplaceAll(t.Name(), "/", "-")
	jobKey := "key-" + strings.ReplaceAll(t.Name(), "/", "-")
	parent := []string{"PATH=/bin", "ANTHROPIC_API_KEY=" + jobKey, "CLAUDE_CODE_OAUTH_TOKEN=" + token,
		"CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=chain-project"}

	for _, auth := range []string{"oauth", "api-key", "vertex"} {
		t.Run(auth, func(t *testing.T) {
			// The chain is behind the gateway: a served run has one in every
			// mode, and one that reached here without it is refused rather than
			// handed the credential its file names.
			if env, secretValues, err := BuildEnv(parent, EnvSpec{Auth: auth, ChainServed: true}); err == nil {
				t.Fatalf("a served %s run with no gateway was built: %v", auth, env)
			} else if !strings.Contains(err.Error(), "runs through the gateway") {
				t.Errorf("a served %s run with no gateway err = %v, want the gateway's own refusal", auth, err)
			} else if env != nil || secretValues != nil {
				t.Errorf("a served %s run with no gateway returned env %v, secrets %v", auth, env, secretValues)
			}
			// A workflow that declares a mount of either credential is refused
			// rather than mounted, the cascade arm's own rule (D143): the
			// credential is the service's to attach, and this run holds none of
			// it to declare.
			for _, declared := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
				if _, _, err := BuildEnv(parent, EnvSpec{Auth: auth, ChainServed: true,
					Gateway: testGateway, Secrets: []string{declared}}); err == nil || !strings.Contains(err.Error(), "is a model credential") {
					t.Errorf("a served %s run that declared %s err = %v, want the model-credential refusal", auth, declared, err)
				}
			}
			env, secretValues, err := BuildEnv(parent, EnvSpec{Auth: auth, ChainServed: true, Gateway: testGateway})
			if err != nil {
				t.Fatalf("a served %s run: %v", auth, err)
			}
			if got := envValue(env, "ANTHROPIC_API_KEY"); got != testGateway.Token {
				t.Errorf("ANTHROPIC_API_KEY = %q, want the gateway's per-run token", got)
			}
			if got := envValue(env, "ANTHROPIC_BASE_URL"); got != testGateway.BaseURL {
				t.Errorf("ANTHROPIC_BASE_URL = %q, want the gateway's address", got)
			}
			for _, kv := range env {
				if strings.Contains(kv, token) || strings.Contains(kv, jobKey) {
					t.Errorf("a served %s run's environment carries a credential: %s", auth, kv)
				}
			}
			if envValue(env, "CLAUDE_CODE_OAUTH_TOKEN") != "" {
				t.Errorf("a served %s run's environment carries CLAUDE_CODE_OAUTH_TOKEN: %v", auth, env)
			}
			// No arm of the file's own auth survives: a vertex run under the
			// chain runs no Vertex call of its own (the service makes them on
			// its own identity, D3174), so it carries neither the variable that
			// turns Vertex on nor the project it would be made in.
			if envValue(env, "CLAUDE_CODE_USE_VERTEX") != "" {
				t.Errorf("a served %s run's environment still uses Vertex: %v", auth, env)
			}
			// The job's key is registered for redaction and nothing else: it
			// reaches no environment. (The token the job mounted is the runner's
			// own to register, before this environment is built —
			// registerModelCredentials.)
			if !slices.Contains(secretValues, jobKey) {
				t.Errorf("a served %s run did not register the job's key for redaction: %v", auth, secretValues)
			}
		})
	}
}

// TestAnUnmigratedRepositoryKeepsTheOldEnvironment (CS7): with the fact false —
// a repository the installation's table does not serve, on the image this
// release ships — the environment is exactly the one this release built before
// it: an oauth run presents its own token, a gateway is refused for it, and a
// declared mount of the token is what the stage runs on. X1 (release 2) deletes
// this branch; until then the migration lands one repository at a time, and
// this is the half that has not migrated.
func TestAnUnmigratedRepositoryKeepsTheOldEnvironment(t *testing.T) {
	token := "oauth-" + strings.ReplaceAll(t.Name(), "/", "-")
	parent := []string{"PATH=/bin", "CLAUDE_CODE_OAUTH_TOKEN=" + token}

	env, secretValues, err := BuildEnv(parent, EnvSpec{Auth: "oauth"})
	if err != nil {
		t.Fatalf("an unmigrated oauth run: %v", err)
	}
	if got := envValue(env, "CLAUDE_CODE_OAUTH_TOKEN"); got != token {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want the run's own token", got)
	}
	if !slices.Contains(secretValues, token) {
		t.Errorf("the run's own token is not registered for redaction: %v", secretValues)
	}
	if envValue(env, "ANTHROPIC_BASE_URL") != "" || envValue(env, "ANTHROPIC_API_KEY") != "" {
		t.Errorf("an unmigrated oauth run carries a routing variable: %v", env)
	}
	if _, _, err := BuildEnv(parent, EnvSpec{Auth: "oauth", Gateway: testGateway}); err == nil || !strings.Contains(err.Error(), "oauth never goes through the gateway") {
		t.Errorf("oauth with a gateway err = %v, want the unchanged refusal", err)
	}
}
