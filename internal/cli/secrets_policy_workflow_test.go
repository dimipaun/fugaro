package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// secretsWorkflowRecordFixture is the world a workflow secret's policy write
// lives in: a project whose local config holds the name's entry in the other
// block — workflow_secrets holds exactly the names project_secrets cannot
// (D1260), and its allow_data_to is the one member of that block the policy
// flag has anything to write — and the installation's own database, because
// every such write is made in two places at once (IS3, D3328): the local block
// and the project record at creds/projectsecrets.
//
// The database is a fixture of its own rather than the plain secretsFixture
// because a writer into it asks the ownership question first
// (requireOwnBudgetDB), and that reads budget.firebase_project out of the
// config: the block the shared harness writes names only the database's URL, so
// the fixture says which Firebase project it belongs to, the way
// `fugaro update --firebase-project` leaves it.
func secretsWorkflowRecordFixture(t *testing.T) (path string, db *gcpfake.RTDB) {
	t.Helper()
	secretsRmFugaro(t)
	sm := gcpfake.NewSecrets(t)
	f := newCloudFixture(t, "secret_manager: "+sm.URL+"/")
	providerKeyEndpoint(t, f.run, newRunServiceSkew(t))
	setBudgetBlock(t, os.Getenv("FUGARO_CONFIG"),
		"budget: { firebase_project: proj-1234, rtdb_url: "+f.budget.DB.URL+" }")
	projectSecretFixture(t, "workflow_secrets:\n  deploy-token: { allow_data_to: [\"acme/app\"] }\n")
	return os.Getenv("FUGARO_CONFIG"), f.budget.DB
}

// secretsPolicyWorkflowFixture is the same world for the tests that ask only
// what the local file holds.
func secretsPolicyWorkflowFixture(t *testing.T) string {
	t.Helper()
	path, _ := secretsWorkflowRecordFixture(t)
	return path
}

// secretsPolicyWorkflowEntry reads the entry the config now holds in
// workflow_secrets, so a test can ask what the write landed rather than how
// the YAML spells it.
func secretsPolicyWorkflowEntry(t *testing.T, path, name string) config.ProjectSecret {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the config does not read back: %v", err)
	}
	return lc.WorkflowSecrets[name]
}

// TestSecretsSetAllowDataToWritesTheWorkflowBlockEntry: naming one more
// repository in a workflow secret's allow_data_to is the same widening, by the
// same flag, as it is for a reserved name — one mechanism, one spelling, and
// the key it writes is the block the entry lives in. The write prints the list
// the entry ends up with, not the flag's own value, because the entry keeps the
// repositories it already named, and it asks nothing for itself (D1552): the
// coding agent's session is where it is refused.
func TestSecretsSetAllowDataToWritesTheWorkflowBlockEntry(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyWorkflowFixture(t)
	noTerminal(t)
	before := readFileString(t, path)

	t.Setenv(accountsAddAgent, "1")
	if _, _, err := execute(t, "secrets", "set", "deploy-token", "--allow-data-to", "acme/web"); err == nil {
		t.Fatal("a coding agent's session widened a workflow secret's allow_data_to")
	} else if !strings.Contains(err.Error(), accountsAddAgent) {
		t.Fatalf("err = %v, want the refusal to name the marker it saw", err)
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("the refusal wrote the config:\n%s", after)
	}
	t.Setenv(accountsAddAgent, "")

	out, errOut, err := execute(t, "secrets", "set", "deploy-token", "--allow-data-to", "acme/web")
	if err != nil {
		t.Fatalf("the widening asked for an answer at the owner's session: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if !strings.Contains(out, "acme/app, acme/web") {
		t.Errorf("the write does not print the list the entry ends up with:\n%s", out)
	}
	if !strings.Contains(out, "widens what the secret reaches") {
		t.Errorf("the write does not say it is a widening:\n%s", out)
	}
	if !strings.Contains(out+errOut, "workflow_secrets.deploy-token.allow_data_to") {
		t.Errorf("the diff does not name the key it wrote:\n%s%s", out, errOut)
	}
	got := secretsPolicyWorkflowEntry(t, path, "deploy-token").AllowDataTo
	if len(got) != 2 || got[0] != "acme/app" || got[1] != "acme/web" {
		t.Fatalf("workflow_secrets.deploy-token.allow_data_to = %v, want [acme/app acme/web]", got)
	}
	if after := readFileString(t, path); strings.Contains(after, "project_secrets") {
		t.Fatalf("the flag wrote the other block:\n%s", after)
	}
}

// TestSecretsSetAllowDataToOnAWorkflowSecretRefusesAnAgentSession: the
// widening a workflow secret's entry pays is the same one, so it meets the
// same refusal first — a coding agent's session, --yes included (spec §5), and
// no config write happens behind it. The name here is one project_secrets
// cannot hold, so this is the new block's own path and not the reserved names'
// fence.
func TestSecretsSetAllowDataToOnAWorkflowSecretRefusesAnAgentSession(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyWorkflowFixture(t)
	before := readFileString(t, path)
	t.Setenv(accountsAddAgent, "1") // a coding agent's session

	_, _, err := execute(t, "secrets", "set", "deploy-token", "--allow-data-to", "acme/web")
	if err == nil {
		t.Fatal("the widening was written in a coding agent's session")
	}
	for _, want := range []string{accountsAddAgent, "fugaro secrets set", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("the session wrote the config:\n%s", after)
	}
}

// TestSecretsSetProxyStaysRefusedForAWorkflowSecret: proxy is the Bitbucket
// token's switch, and the credentials service reads it from
// project_secrets.bitbucket-token alone — which is why D1275 refuses the field
// in the new block outright. A flag that wrote one there would be a setting
// nothing reads, and the operator who wrote it would believe their installation
// is proxied. Refused whichever way the flag is spelled, with nothing written.
func TestSecretsSetProxyStaysRefusedForAWorkflowSecret(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyWorkflowFixture(t)
	noTerminal(t)
	before := readFileString(t, path)

	for _, arg := range []string{"--proxy", "--proxy=false"} {
		_, _, err := execute(t, "secrets", "set", "deploy-token", arg)
		if err == nil {
			t.Fatalf("%s wrote a workflow secret's entry", arg)
		}
		for _, want := range []string{"workflow_secrets.deploy-token", "proxy"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal does not carry %q:\n%v", arg, want, err)
			}
		}
		if after := readFileString(t, path); after != before {
			t.Fatalf("%s wrote the config:\n%s", arg, after)
		}
	}
}
