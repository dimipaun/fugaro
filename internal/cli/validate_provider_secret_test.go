package cli

import (
	"os"
	"strings"
	"testing"
)

// A workflow secret named after a configured provider's key, checked where
// fugaro.yaml meets the owner's providers: `fugaro validate` is the command
// that says whether a file is deployable, and until this rule it said yes to a
// name the job's own spec refuses (infra.SecretMounts) and `fugaro secrets set`
// refuses to store (config.ReservedProviderSecret). The name is the
// convention's, <provider entry>-api-key, and it belongs to that provider
// whatever its .secret is written as today, so a value a workflow stored under
// it would be read as the provider's credential the day the key is renamed to
// the convention.
//
// The rule needs the providers, which live in the project's local config and
// never in a fugaro.yaml, so it is config.CheckWorkflowProviderSecrets called
// from providerProblems — the same call site, and the same path spelling
// (workflows.<name>.secrets[i].name), as Validate's own reserved-name rules.

// repoWithWorkflowSecret is the file under test: the fixture's repository
// config, its app workflow declaring one secret of the given name, over the
// project config that defines the openrouter provider.
func repoWithWorkflowSecret(t *testing.T, name string) string {
	t.Helper()
	path := budgetProject(t, "budget: { mode: observe }\n"+validateProviderBlock, pinnedAgent)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(data),
		"  app: { commands: { build: sh build.sh, test: sh test.sh } }",
		"  app:\n    commands: { build: sh build.sh, test: sh test.sh }\n"+
			"    secrets:\n      - { name: "+name+", env: WORKFLOW_SECRET }\n", 1)
	if body == string(data) {
		t.Fatalf("the fixture did not gain a secrets block:\n%s", data)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	inCheckoutOf(t, "acme/sandbox")
	return path
}

// The refusal: a problem, at the workflow secret's own name, naming the
// provider the name is reserved for and the way out. The control beside it is
// what makes the test about the name: the same file with an ordinary secret
// name is valid.
func TestValidateRefusesAWorkflowSecretReservedForAProvider(t *testing.T) {
	t.Run("an ordinary name is valid", func(t *testing.T) {
		path := repoWithWorkflowSecret(t, "my-own-secret")
		if out, errOut, err := execute(t, "validate", path); err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("a workflow secret of its own was refused: %v\n%s%s", err, out, errOut)
		}
	})

	t.Run("the conventional name of a configured provider's key", func(t *testing.T) {
		path := repoWithWorkflowSecret(t, "openrouter-api-key")
		out, errOut, err := execute(t, "validate", path)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d, want %d (a name the deploy refuses is the file's to fix):\n%s%s",
				ExitCode(err), ExitUserError, out, errOut)
		}
		all := out + errOut
		if !strings.Contains(out, "workflows.app.secrets[0].name: openrouter-api-key is reserved for model provider openrouter's key") {
			t.Errorf("the file is not refused for the reserved name, at the secret's own path:\n%s", all)
		}
		// The fix is in the sentence, as Validate's sibling rules carry theirs.
		if !strings.Contains(all, "Name the workflow's own secret something else") {
			t.Errorf("the refusal does not say what to do about it:\n%s", all)
		}
	})

	// The rule is about the provider's own key name, so it is only a
	// reservation where that provider exists: the same file under a project
	// config with no providers at all has nothing to reserve the name, and it
	// is valid.
	t.Run("no providers configured, nothing is reserved", func(t *testing.T) {
		path := budgetProject(t, "budget: { mode: observe }\n", pinnedAgent)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Replace(string(data),
			"  app: { commands: { build: sh build.sh, test: sh test.sh } }",
			"  app:\n    commands: { build: sh build.sh, test: sh test.sh }\n"+
				"    secrets:\n      - { name: openrouter-api-key, env: WORKFLOW_SECRET }\n", 1)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		inCheckoutOf(t, "acme/sandbox")
		if out, errOut, err := execute(t, "validate", path); err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("a name no configured provider reserves was refused: %v\n%s%s", err, out, errOut)
		}
	})
}
