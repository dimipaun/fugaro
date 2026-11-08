package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// secretsPolicyFixture is a project whose local config holds the Bitbucket
// token's project entry: the entry the family's policy flags write, and the
// only name that may carry `proxy` at all (D493).
func secretsPolicyFixture(t *testing.T) string {
	t.Helper()
	secretsFixture(t)
	projectSecretFixture(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [\"acme/app\"] }\n")
	return os.Getenv("FUGARO_CONFIG")
}

// secretsPolicyEntry reads the entry the config now holds, so a test can ask
// what the write landed rather than how the YAML spells it.
func secretsPolicyEntry(t *testing.T, path, name string) config.ProjectSecret {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the config does not read back: %v", err)
	}
	return lc.ProjectSecrets[name]
}

// TestSecretsSetProxyIsATypedWidening: `secrets set NAME --proxy` writes
// project_secrets.NAME.proxy through the settings write path, and turning the
// switch on is a widening — the credentials service is handed the token
// instead of the job — so it is the owner's own step: it asks nothing at the
// owner's session, with a terminal or without one, and the coding agent's
// session is what refuses it (D1165, D1171, D1540, D1552). The narrowing half,
// --proxy=false, is neither: it prints no widening sentence, and it takes the
// key away rather than writing a second spelling of "off".
func TestSecretsSetProxyIsATypedWidening(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyFixture(t)
	noTerminal(t)
	before := readFileString(t, path)

	// The coding agent's session is the fence this widening has instead of a
	// question, and --yes does not lift it.
	t.Setenv(accountsAddAgent, "1")
	if _, _, err := execute(t, "secrets", "set", "bitbucket-token", "--proxy", "--yes"); err == nil {
		t.Fatal("a coding agent's session turned the proxy switch on")
	} else if !strings.Contains(err.Error(), accountsAddAgent) {
		t.Fatalf("err = %v, want the refusal to name the marker it saw", err)
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("the refusal wrote the config:\n%s", after)
	}
	t.Setenv(accountsAddAgent, "")

	out, errOut, err := execute(t, "secrets", "set", "bitbucket-token", "--proxy")
	if err != nil {
		t.Fatalf("the widening asked for an answer at the owner's session: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if !strings.Contains(out, "widens what the secret reaches") {
		t.Errorf("the write does not say it is a widening:\n%s", out)
	}
	if strings.Contains(out, "to confirm: ") {
		t.Errorf("the widening still asks a question:\n%s", out)
	}
	if got := secretsPolicyEntry(t, path, "bitbucket-token"); !got.Proxy {
		t.Errorf("proxy was not written: %+v", got)
	}
	if !strings.Contains(out+errOut, "project_secrets.bitbucket-token.proxy") {
		t.Errorf("the diff does not name the key it wrote:\n%s%s", out, errOut)
	}

	out, errOut, err = execute(t, "secrets", "set", "bitbucket-token", "--proxy=false", "--yes")
	if err != nil {
		t.Fatalf("secrets set --proxy=false --yes: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if strings.Contains(out, "widens what the secret reaches") {
		t.Errorf("turning the switch off was confirmed as a widening:\n%s", out)
	}
	off := secretsPolicyEntry(t, path, "bitbucket-token")
	if off.Proxy {
		t.Errorf("the switch is still on: %+v", off)
	}
	if !slices.Contains(off.AllowDataTo, "acme/app") {
		t.Errorf("turning the switch off took the entry's own allow-list with it: %v", off.AllowDataTo)
	}
}

// TestSecretsSetAllowDataToIsATypedWidening: adding a repository to
// project_secrets.NAME.allow_data_to is the widening D1171 names — one more
// repository the value is mounted in — and the sentence the write prints names
// the list the entry ends up with, not the flag's own values, because an entry
// that already names a repository keeps it. The widening asks nothing for
// itself (D1540, D1552): the coding agent's session is where it is refused.
func TestSecretsSetAllowDataToIsATypedWidening(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyFixture(t)
	noTerminal(t)
	before := readFileString(t, path)

	t.Setenv(accountsAddAgent, "1")
	if _, _, err := execute(t, "secrets", "set", "bitbucket-token", "--allow-data-to", "acme/web", "--yes"); err == nil {
		t.Fatal("a coding agent's session widened allow_data_to")
	} else if !strings.Contains(err.Error(), accountsAddAgent) {
		t.Fatalf("err = %v, want the refusal to name the marker it saw", err)
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("the refusal wrote the config:\n%s", after)
	}
	t.Setenv(accountsAddAgent, "")

	out, errOut, err := execute(t, "secrets", "set", "bitbucket-token", "--allow-data-to", "acme/web")
	if err != nil {
		t.Fatalf("the widening asked for an answer at the owner's session: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if !strings.Contains(out, "widens what the secret reaches") {
		t.Errorf("the write does not say it is a widening:\n%s", out)
	}
	if !strings.Contains(out, "acme/app, acme/web") {
		t.Errorf("the write does not print the list the entry ends up with:\n%s", out)
	}
	got := secretsPolicyEntry(t, path, "bitbucket-token").AllowDataTo
	if len(got) != 2 || got[0] != "acme/app" || got[1] != "acme/web" {
		t.Fatalf("allow_data_to = %v, want [acme/app acme/web]", got)
	}
}

// TestSecretsSetAllowDataToReachesAFlowStyleEntry: the entry
// docs/git-providers.md teaches the operator to write is a flow collection —
// project_secrets: { bitbucket-token: { allow_data_to: ["acme/app"] } } — and
// the only edit that splices a member into a list refuses a flow sequence. The
// flag has to reach that entry all the same, so the entry is written again in
// block style: the repository it already named is kept, the flag's is added,
// and the diff the confirmation shows is the reformatting, so the shape is
// never changed silently.
func TestSecretsSetAllowDataToReachesAFlowStyleEntry(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyFixture(t)
	noTerminal(t)
	before := readFileString(t, path)
	if !strings.Contains(before, "[") {
		t.Fatalf("the fixture is not the flow shape this test is about:\n%s", before)
	}

	out, errOut, err := execute(t, "secrets", "set", "bitbucket-token", "--allow-data-to", "acme/web", "--yes")
	if err != nil {
		t.Fatalf("secrets set --allow-data-to --yes: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	got := secretsPolicyEntry(t, path, "bitbucket-token").AllowDataTo
	if len(got) != 2 || got[0] != "acme/app" || got[1] != "acme/web" {
		t.Fatalf("allow_data_to = %v, want [acme/app acme/web]", got)
	}
	after := readFileString(t, path)
	if strings.Contains(after, "bitbucket-token: {") || !strings.Contains(after, "- acme/app") || !strings.Contains(after, "- acme/web") {
		t.Fatalf("the entry was not written in the block style this program writes a list in:\n%s", after)
	}
	if !strings.Contains(out, "-   bitbucket-token: { allow_data_to: [\"acme/app\"] }") {
		t.Errorf("the diff does not show the shape the write replaces:\n%s", out)
	}
}

// TestSecretsSetPolicyRefusesAnAgentSession: the family's policy flags are
// refused in a coding agent's session, gate or not (spec §5), before anything
// is read — and a no-op run is refused too, which the settings write path's own
// fence would not do: that one sits behind its "no change" return.
func TestSecretsSetPolicyRefusesAnAgentSession(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyFixture(t)
	before := readFileString(t, path)
	t.Setenv(accountsAddAgent, "1") // a coding agent's session

	for _, args := range [][]string{
		{"secrets", "set", "bitbucket-token", "--proxy", "--yes"},
		// The same write the file already holds: a no-op for applySettingsEdit,
		// and still the family's refusal here.
		{"secrets", "set", "bitbucket-token", "--allow-data-to", "acme/app", "--yes"},
	} {
		_, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%s wrote the policy in a coding agent's session", strings.Join(args, " "))
		}
		for _, want := range []string{accountsAddAgent, "fugaro secrets set", "nothing was written"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal does not carry %q:\n%v", strings.Join(args, " "), want, err)
			}
		}
	}
	if after := readFileString(t, path); after != before {
		t.Fatalf("the session wrote the config:\n%s", after)
	}
}

// TestSecretsSetPolicyRefusesWhatItCannotWrite: the block's own rules decide,
// on the bytes this would write — a name project_secrets cannot hold, a
// repository that is not owner/name, a proxy on a name that is not the Bitbucket
// token (D493), and --repo, which names a container no policy entry belongs to.
// Each leaves the file exactly as it was.
func TestSecretsSetPolicyRefusesWhatItCannotWrite(t *testing.T) {
	secretsRmFugaro(t)
	path := secretsPolicyFixture(t)
	noTerminal(t)
	before := readFileString(t, path)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"a name project_secrets cannot hold", []string{"secrets", "set", "web-token", "--proxy", "--yes"}, "project_secrets holds the secrets Fugaro stores project-wide"},
		// The other block's names land here too, and the sentence has to say
		// which block they belong to: workflow_secrets holds exactly the names
		// project_secrets cannot, so a person who wrote one there would read a
		// bare "not one of the reserved names" as a typo of their own.
		{"a workflow secret's name", []string{"secrets", "set", "deploy-token", "--allow-data-to", "acme/web", "--yes"}, "workflow_secrets"},
		{"a repository that is not owner/name", []string{"secrets", "set", "bitbucket-token", "--allow-data-to", "acme", "--yes"}, "must be a repository as owner/name"},
		{"an owner wildcard", []string{"secrets", "set", "bitbucket-token", "--allow-data-to", "*/app", "--yes"}, "the owner segment must be literal"},
		{"proxy on a name that is not the Bitbucket token", []string{"secrets", "set", "claude-oauth-token", "--proxy", "--yes"}, "the Bitbucket token's switch"},
		{"--repo", []string{"secrets", "set", "bitbucket-token", "--proxy", "--yes", "--repo", "acme/app"}, "--repo and the policy flags name different things"},
		{"both edits at once", []string{"secrets", "set", "bitbucket-token", "--proxy", "--allow-data-to", "acme/web", "--yes"}, "two different edits"},
	}
	for _, tc := range cases {
		_, _, err := execute(t, tc.args...)
		if err == nil {
			t.Errorf("%s was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
		if after := readFileString(t, path); after != before {
			t.Fatalf("%s wrote the config:\n%s", tc.name, after)
		}
	}
}

// TestSecretsNoValueEverAppearsInADiff is the property D1171 states as a rule
// of the family: a secret's value is not a setting, so no dry run and no diff
// of any write here prints one. Every dry run the family has is run with a
// value on stdin against a config that holds the secret's policy, and the value
// is looked for in every stream of every one of them.
func TestSecretsNoValueEverAppearsInADiff(t *testing.T) {
	secretsRmFugaro(t)
	sm, id, _ := secretsRmFixture(t)
	const value = "fake-policy-dry-run-value-EXAMPLE"

	runs := [][]string{
		{"secrets", "rm", "bitbucket-token", "--dry-run", "--json"},
		{"secrets", "rm", "bitbucket-token", "--dry-run"},
		{"secrets", "set", "bitbucket-token", "--proxy", "--dry-run", "--json"},
		{"secrets", "set", "bitbucket-token", "--allow-data-to", "acme/web", "--dry-run", "--json"},
		{"secrets", "set", "bitbucket-token", "--allow-data-to", "acme/web", "--dry-run"},
	}
	for _, args := range runs {
		out, errOut, err := executeStdin(t, value+"\n", args...)
		if err != nil {
			t.Fatalf("%s: %v\nstdout %s\nstderr %s", strings.Join(args, " "), err, out, errOut)
		}
		if strings.Contains(out+errOut, value) {
			t.Fatalf("%s printed the value:\nstdout %s\nstderr %s", strings.Join(args, " "), out, errOut)
		}
	}

	// The value write has no dry run and no document at all: one that carried
	// nothing would read as "the value is not stored" while the write stores it.
	out, errOut, err := executeStdin(t, value+"\n", "secrets", "set", "bitbucket-token", "--dry-run", "--json")
	if err == nil {
		t.Fatalf("a value write answered a dry run:\nstdout %s\nstderr %s", out, errOut)
	}
	if !strings.Contains(err.Error(), "a value is not a setting") {
		t.Errorf("err = %v, want the value-is-not-a-setting refusal", err)
	}
	if strings.Contains(out+errOut+err.Error(), value) {
		t.Fatalf("the refusal printed the value: %q %q %v", out, errOut, err)
	}
	if got := sm.Latest(id); string(got) != tokenValue {
		t.Fatalf("a dry run stored %q", got)
	}
}

// TestSecretsSetValueWriteRefusesThePolicyFlags: the two runs are never the
// same run (D1171). A value write refuses --dry-run and --yes — a document
// carrying nothing would read as "the value is not stored" while the write
// stores it, and a --yes accepted there would be a confirmation the person
// believes they gave for a prompt that never came — and --json is not one of
// them: the store's own result document is what that flag has always printed
// on this path, and the console's store reads it.
func TestSecretsSetValueWriteRefusesThePolicyFlags(t *testing.T) {
	sm := secretsFixture(t)
	secret := gcp.SecretID(appSlug, "claude-oauth-token")

	for _, flag := range []string{"--dry-run", "--yes"} {
		out, errOut, err := executeStdin(t, tokenValue+"\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app", flag)
		if err == nil {
			t.Fatalf("%s was accepted on a value write:\nstdout %s\nstderr %s", flag, out, errOut)
		}
		if !strings.Contains(err.Error(), "a value is not a setting") {
			t.Errorf("%s: err = %v, want the value-is-not-a-setting refusal", flag, err)
		}
		if strings.Contains(out+errOut+err.Error(), tokenValue) {
			t.Fatalf("%s printed the value: %q %q %v", flag, out, errOut, err)
		}
	}
	if got := sm.Latest(secret); got != nil {
		t.Fatalf("a refused run stored %q", got)
	}

	out, errOut, err := executeStdin(t, tokenValue+"\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatalf("secrets set --json: %v\nstdout %s\nstderr %s", err, out, errOut)
	}
	if got := string(sm.Latest(secret)); got != tokenValue {
		t.Fatalf("--json stored %q", got)
	}
	if !strings.Contains(out, `"secret"`) || strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("--json did not print the store's result document: %q %q", out, errOut)
	}
}
