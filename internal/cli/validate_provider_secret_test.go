package cli

import (
	"os"
	"strings"
	"testing"
)

// A workflow secret whose name belongs to a configured provider's key, checked
// where fugaro.yaml meets the owner's providers: `fugaro validate` is the
// command that says whether a file is deployable, and until this rule it said
// yes to two names the job's own spec refuses (infra.SecretMounts) and `fugaro
// secrets set` refuses to store — the provider's own .secret, and the
// conventional <entry>-api-key name that belongs to that provider whatever its
// .secret is written as today (config.CheckWorkflowProviderSecrets).
//
// The rule needs the providers, which live in the project's local config and
// never in a fugaro.yaml, so it is config.CheckWorkflowProviderSecrets called
// from providerProblems — the same call site, and the same path spelling
// (workflows.<name>.secrets[i].name), as Validate's own reserved-name rules.

// validateTwoProviders is the project config's provider block with two entries
// whose keys are stored under names the convention does not give them, so each
// provider's own secret and its conventional name are two different names:
// `or-key` is openrouter's own and `openrouter-api-key` is only its
// reservation, while `deepseek-api-key` is deepseek's own — a name that is both
// its key and its convention, which infra.SecretMounts and
// CheckWorkflowProviderSecrets both read as the provider's key first.
const validateTwoProviders = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: or-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/sandbox"]
  deepseek:
    kind: anthropic-compat
    base_url: https://api.deepseek.com
    auth: bearer
    secret: deepseek-api-key
    models: ["qwen/*"]
    allow_data_to: ["acme/sandbox"]
`

// repoWithWorkflowSecret is the file under test: the fixture's repository
// config, its app workflow declaring one secret of the given name, over a
// project config that defines the two providers above.
func repoWithWorkflowSecret(t *testing.T, name string) string {
	t.Helper()
	path := budgetProject(t, "budget: { mode: observe }\n"+validateTwoProviders, pinnedAgent)
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

// Each of the two collisions is a problem, at the workflow secret's own path,
// naming the provider and the way out. The control beside them is what makes
// the test about the name: the same file with an ordinary secret name is valid.
func TestValidateRefusesAWorkflowSecretReservedForAProvider(t *testing.T) {
	t.Run("an ordinary name is valid", func(t *testing.T) {
		path := repoWithWorkflowSecret(t, "my-own-secret")
		if out, errOut, err := execute(t, "validate", path); err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("a workflow secret of its own was refused: %v\n%s%s", err, out, errOut)
		}
	})

	// A workflow declaring the name a provider's key is stored under would
	// mount the owner's provider credential into the job as a variable of the
	// workflow's own — the key leaving the local config that is its only home.
	t.Run("a configured provider's own secret", func(t *testing.T) {
		path := repoWithWorkflowSecret(t, "or-key")
		out, errOut, err := execute(t, "validate", path)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d, want %d (a name the deploy refuses is the file's to fix):\n%s%s",
				ExitCode(err), ExitUserError, out, errOut)
		}
		if !strings.Contains(out, "workflows.app.secrets[0].name: or-key is the secret of model provider openrouter in the project's local config") {
			t.Errorf("the file is not refused for the provider's own key, at the secret's own path:\n%s", out+errOut)
		}
	})

	// And the convention's name, which the provider may not even use as its own
	// .secret today: a value a workflow stored under it would be read as the
	// provider's credential the day that key is renamed to the convention.
	t.Run("the conventional name of a configured provider's key", func(t *testing.T) {
		path := repoWithWorkflowSecret(t, "openrouter-api-key")
		out, errOut, err := execute(t, "validate", path)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d, want %d:\n%s%s", ExitCode(err), ExitUserError, out, errOut)
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

	// A name that is both a provider's own key and its convention is refused as
	// the key, which is the sharper of the two truths about it — the order
	// infra.SecretMounts reads the same two conditions in.
	t.Run("a provider's own secret that is also its convention", func(t *testing.T) {
		path := repoWithWorkflowSecret(t, "deepseek-api-key")
		out, errOut, err := execute(t, "validate", path)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d, want %d:\n%s%s", ExitCode(err), ExitUserError, out, errOut)
		}
		if !strings.Contains(out, "workflows.app.secrets[0].name: deepseek-api-key is the secret of model provider deepseek in the project's local config") {
			t.Errorf("the name is refused as a reservation rather than as the provider's own key:\n%s", out+errOut)
		}
	})

	// Both collisions are relative to the owner's providers, so a project config
	// with no providers at all has nothing to reserve the name and the file is
	// valid.
	t.Run("no providers configured, nothing is reserved", func(t *testing.T) {
		path := budgetProject(t, "budget: { mode: observe }\n", pinnedAgent)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Replace(string(data),
			"  app: { commands: { build: sh build.sh, test: sh test.sh } }",
			"  app:\n    commands: { build: sh build.sh, test: sh test.sh }\n"+
				"    secrets:\n      - { name: or-key, env: WORKFLOW_SECRET }\n", 1)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		inCheckoutOf(t, "acme/sandbox")
		if out, errOut, err := execute(t, "validate", path); err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("a name no configured provider reserves was refused: %v\n%s%s", err, out, errOut)
		}
	})
}
