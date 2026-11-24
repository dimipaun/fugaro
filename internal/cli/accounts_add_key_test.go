//go:build darwin || linux

package cli

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/streams"
)

// accounts_add_key_test.go is `accounts add --type` for the two KEY
// credentials (MC22, D1669, D1670, D1671): the type is accepted, the value is
// read the one way `fugaro secrets set` reads one, and it lands under the
// credential's OWN derived secret name — never under the Claude account's,
// which is another credential's container and is what an implementation that
// reused the store step's name unchanged would fill.
//
// What a key credential is served by is not here: the declaration and the
// stored value are this task's, the half that carries them to a call is
// MC23's. The rules of the VALUE — stdin or a hidden prompt, never an
// argument, a flag or the environment, never echoed, never in the file — are
// `fugaro secrets set`'s own and are not restated by the command or by these
// tests: what is asserted below is this command's reuse of them, which is the
// half a second implementation could get wrong.
//
// The file carries the pty tests' build tag because the hidden-prompt test is
// one of them and the helpers it uses (newTTY, runOnTerminal) are in tagged
// files of their own.

// credentialSecretID is the secret a key credential's value lands under,
// through the one function that names it (gcp.ProjectSecretID), the way
// accountSecretID is the Claude account's.
func credentialSecretID(name string) string {
	return gcp.ProjectSecretID(initProjectName, config.CredentialSecret(name))
}

// TestAKeyCredentialsSecretStoresWhereItsDeclarationBoundsIt is the store path
// itself, on the name this command writes under: `fugaro secrets set
// credential-<name>-key --project-wide` is accepted exactly when the config
// declares that credential as a key type and bounds where its key may be sent,
// which is the state --type leaves behind — and refused in the states where
// nothing would read what it stored. The name is reserved everywhere, so the
// store is the one door it has, and it is the door this command re-runs.
//
// The declared case is a RUNNER-path credential, whose store is C1's entry in
// the bundle since KS2 (D1670 reversed): the declaration bounds the key through
// claude_accounts.<name>.allow_data_to, and what that field bounds is the set of
// entries one version carries. The container this command wrote under the
// credential's own derived name before this task is asserted absent — a key
// entered as a container is what F33 removed.
func TestAKeyCredentialsSecretStoresWhereItsDeclarationBoundsIt(t *testing.T) {
	// The declared state: a key type, the provider it serves — a provider key's
	// entry carries the provider's own address and auth (F45, D1418), so the name
	// has to name one — and an allow_data_to that names a repository.
	sm := secretsFixture(t)
	appendConfig(t, providerKeyCloudProvider+"credentials:\n  - { name: team-a, type: openrouter }\nclaude_accounts:\n  team-a: { allow_data_to: [acme/app] }\n")
	bundleID := bundleFixtureFor(t, sm, "aurora", nil)
	if _, _, err := executeStdin(t, addToken(), "secrets", "set", config.CredentialSecret("team-a"), "--project-wide"); err != nil {
		t.Fatalf("storing a declared key credential's own secret was refused: %v", err)
	}
	if got := bundleEntry(t, sm, bundleID, appSlug, "team-a").Value(); got != addToken() {
		t.Errorf("the bundle's entry for acme/app holds %q, want the value the command read: a runner-path key credential's store is the entry its declaration bounds", got)
	}
	if got := sm.Latest(credentialSecretID("team-a")); got != nil {
		t.Errorf("%s holds %q: a provider key is not entered as a container any more, and nothing reads one on this path", credentialSecretID("team-a"), got)
	}

	// No credential of that name: the container would be an orphan nothing
	// reads, and the refusal names the command that declares one.
	sm = secretsFixture(t)
	appendConfig(t, "claude_accounts:\n  team-a: { allow_data_to: [acme/app] }\n")
	out, errOut, err := executeStdin(t, addToken(), "secrets", "set", config.CredentialSecret("team-a"), "--project-wide")
	if err == nil || !strings.Contains(err.Error(), "declares no credential") {
		t.Fatalf("storing under a name no credential owns: err = %v", err)
	}
	if got := string(sm.Latest(credentialSecretID("team-a"))); got != "" {
		t.Errorf("the refused store wrote %q", got)
	}
	if strings.Contains(out+errOut, addToken()) {
		t.Errorf("the refusal printed the value:\n%s\n%s", out, errOut)
	}

	// Declared as the OAuth type: that credential's value is its Claude
	// account's own token, under the account family, so a value here would be
	// a second container for one credential.
	secretsFixture(t)
	appendConfig(t, "credentials:\n  - { name: team-a, type: claude-oauth }\nclaude_accounts:\n  team-a: { allow_data_to: [acme/app] }\n")
	if _, _, err := executeStdin(t, addToken(), "secrets", "set", config.CredentialSecret("team-a"), "--project-wide"); err == nil || !strings.Contains(err.Error(), "is declared claude-oauth") {
		t.Fatalf("storing under a claude-oauth credential's key name: err = %v", err)
	}

	// The bare command lands on the repository's own copy, which a credential
	// never has: the value is the project's, and its reach is the account's
	// allow_data_to rather than the repository a copy was stored for.
	secretsFixture(t)
	appendConfig(t, "credentials:\n  - { name: team-a, type: openrouter }\nclaude_accounts:\n  team-a: { allow_data_to: [acme/app] }\n")
	_, _, err = executeStdin(t, addToken(), "secrets", "set", config.CredentialSecret("team-a"), "--repo", "acme/app")
	if err == nil || !strings.Contains(err.Error(), "--project-wide") {
		t.Fatalf("a repository copy of a credential's secret: err = %v", err)
	}
}

// TestAKeyCredentialTakesNoProfile is the flag half of leaving the name out of
// profiles.<p>.claude_accounts: the list that would carry it is the one a run's
// Claude account TOKEN is served by, so `--profile` is refused for a key
// credential before anything is read or written — the person (or the script)
// whose habit adds the flag hears what the list serves and what to drop,
// rather than getting the job reading an empty token the flag used to write.
// The refusal follows the credential the config declares and not this run's
// flag, which is what the second half asserts: a re-run carries no --type.
func TestAKeyCredentialTakesNoProfile(t *testing.T) {
	for _, c := range []struct {
		what, local string
		args        []string
	}{
		{"the flag declares the type", addRigLocal, []string{"accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter", "--profile", "standard"}},
		{"the config already declares it", accountsAddKeyCredentialLocal, []string{"accounts", "add", "team-a", "--repos", "owner/repo-a", "--profile", "standard"}},
	} {
		t.Run(c.what, func(t *testing.T) {
			path := accountsAddFixture(t, c.local)
			before := readFileString(t, path)

			out, errOut, err := executeStdin(t, addToken()+"\n", c.args...)
			if err == nil {
				t.Fatalf("--profile was accepted for a key credential:\n%s\n%s", out, errOut)
			}
			want := "--profile standard: profiles.<p>.claude_accounts serves a Claude account TOKEN to a run, and team-a is a key credential"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not read %q: %v", want, err)
			}
			if !strings.Contains(err.Error(), config.CredentialSecret("team-a")) || !strings.Contains(err.Error(), "drop --profile") {
				t.Errorf("the refusal does not name the container the value lives in and the fix: %v", err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
			if strings.Contains(out+errOut, addToken()) {
				t.Errorf("the refusal printed the value:\n%s\n%s", out, errOut)
			}
		})
	}
}

// TestAKeyCredentialTakesNoServingProfile is the file's half of the same rule,
// and the half no flag of this run shows. `--profile` is refused because the
// list serves a Claude account TOKEN; a name a profile ALREADY lists is a list
// this process does not write, so refusing the flag alone would leave the very
// job that refusal is about reachable — the name an OAuth account before
// (the ordinary conversion --type exists for) still has a version in
// claude-oauth-token-<name>, and a run launched onto that job would
// authenticate as a token the owner has just declared to be a provider's key.
// The declaration below is written by claude_accounts.<name>.allow_data_to and
// by nothing else, so before this refusal the entry landed beside the list: the
// node carried accounts.<name>, the job set carried the variant, and the three
// sentences of the report said the opposite of what the file held. The refusal
// is on the name and not on this run's flag, which is what the second case
// asserts: a re-run of the declaration carries no --type.
func TestAKeyCredentialTakesNoServingProfile(t *testing.T) {
	for _, c := range []struct {
		what, local string
		args        []string
	}{
		{"the flag declares the type", accountsAddKeyCredentialServedLocal, []string{"accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter"}},
		{"the config already declares it", accountsAddKeyCredentialServedLocal + "credentials:\n  - { name: team-a, type: openrouter }\n", []string{"accounts", "add", "team-a", "--repos", "owner/repo-a"}},
	} {
		t.Run(c.what, func(t *testing.T) {
			r := newAddRigWith(t, c.local)
			before := readFileString(t, r.cfg)

			out, errOut, err := executeStdin(t, addToken()+"\n", c.args...)
			if err == nil {
				t.Fatalf("the key credential was declared beside a profile that still serves team-a:\n%s\n%s", out, errOut)
			}
			want := "profiles.standard.claude_accounts already lists team-a"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not read %q: %v", want, err)
			}
			if !strings.Contains(err.Error(), "fugaro accounts rm team-a --profile standard") || !strings.Contains(err.Error(), "another name") {
				t.Errorf("the refusal does not name the fix: %v", err)
			}
			if !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal does not say nothing was written: %v", err)
			}
			// Nothing was written means the file's bytes: the list that serves the
			// name, the credentials entry the run had resolved, and the declaration
			// the edit below would have added are all still as they were.
			if got := readFileString(t, r.cfg); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
			if got := string(r.sm.Latest(credentialSecretID("team-a"))); got != "" {
				t.Errorf("the refused run stored %q under %s", got, credentialSecretID("team-a"))
			}
			if strings.Contains(out+errOut, addToken()) {
				t.Errorf("the refusal printed the value:\n%s\n%s", out, errOut)
			}
		})
	}
}

// TestTheAccountsDoorKeepsTheJobOfANameTheConfigNowDeclaresAKey: the state the
// refusal above prevents is one a FILE can hold all the same — the refusal is
// this command's, and the config is the operator's — and the installation that
// file describes has the job already.
//
// The half MC22 left open is what a converge does with it. No command writes
// the pair below, and the record of an installation that met it holds the job
// the release behind composed for team-a: profiles.standard lists the name and
// claude-oauth-token-team-a is a project-wide container, which is exactly what
// projectAccountMount needs to build a job for it. This release's composer
// leaves the name out of the set (D1670, config.ClaudeAccountTokenNames), so a
// document written without that job is the plan of its destruction:
// RefuseProjectJobDeletes stops the whole update on it, and the remedy it
// prints — "restore the profile as it was" — is putting back the very name the
// rule drops the job for, so the installation never converges again
// (CLAUDE.md's HARD RULE, broken for exactly the config this rule is about).
// The door keeps the job, whole, from the record the run that deployed it left
// (infra.CarriedProjectJobAccounts).
//
// Mutation (run, restore): drop the CarriedProjectJobAccounts carry from
// init.go's installationVars, and the second converge writes a record without
// team-a's job.
func TestTheAccountsDoorKeepsTheJobOfANameTheConfigNowDeclaresAKey(t *testing.T) {
	r := newAddRigWith(t, accountsAddKeyCredentialServedLocal)
	// The file's own installation, converged once: team-a is served by
	// profiles.standard and its project-wide container is there, so the record
	// holds the job.
	r.add(t, "team-b", "--repos", "owner/repo-a")
	deployed := r.projectJobs(t)
	job := gcp.ProjectJobName("standard", "github", "team-a")
	if _, ok := deployed[job]; !ok {
		t.Fatalf("the fixture deployed no %s, so this test has no record to keep: %v", job, slices.Sorted(maps.Keys(deployed)))
	}

	// The hand-written act: team-a is retyped a KEY credential, which is the
	// entry `fugaro accounts add --type openrouter` writes — refused while a
	// profile lists the name (TestAKeyCredentialTakesNoServingProfile, above),
	// and so written into the file. The config goes through its own reader and
	// writer, so what this leaves is the file the run above left, one type
	// further on.
	lc := r.addLocal(t)
	retyped := false
	for i := range lc.Credentials {
		if lc.Credentials[i].Name == "team-a" {
			lc.Credentials[i].Type, retyped = config.CredentialTypeOpenRouter, true
		}
	}
	if !retyped {
		t.Fatalf("the run above declared no credential for team-a, so this test would not be retyping one: %v", lc.Credentials)
	}
	data, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg, data, 0o600); err != nil {
		t.Fatal(err)
	}

	// A second converge, by a run that does not touch team-a at all.
	// The retype loosens the shared node (craft_credentials team-a), which is a
	// grant-only publish: the gate judges it and no name is typed for it (N4).
	r.add(t, "team-c", "--repos", "owner/repo-a")

	kept := r.projectJobs(t)
	for name, want := range deployed {
		got, ok := kept[name]
		if !ok {
			t.Errorf("the record the second converge wrote holds %v, want %s among them: the document is what the next plan is made from, so a job the installation holds and this document does not is that job's destruction — which RefuseProjectJobDeletes stops the whole update on, over a remedy (put the name back into the profile's list) that this release's filter drops the job for",
				slices.Sorted(maps.Keys(kept)), name)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the second converge wrote the job %s as\n%+v\nwant the record's own entry\n%+v: a job the installation holds is stated exactly as it holds it, or the plan that follows proposes a change to it", name, got, want)
		}
	}
	// And the door's own work is beside them, undiminished: the job of the
	// account this run added.
	if _, ok := kept[gcp.ProjectJobName("standard", "github", "team-c")]; !ok {
		t.Errorf("the door deployed no job for the account it added: %v", slices.Sorted(maps.Keys(kept)))
	}
	// The file's own declaration is where the carry found the name, and it stays
	// where it is: a key credential keeps the claude_accounts entry whose
	// allow_data_to is the reach its key may be sent to (D1670), and the carry
	// moves a JOB and never an account.
	if _, ok := r.addLocal(t).ClaudeAccounts["team-a"]; !ok {
		t.Errorf("the claude_accounts entry is gone from the local config: %v, want team-a's — a key credential keeps the entry whose allow_data_to is the reach its key may be sent to",
			slices.Sorted(maps.Keys(r.addLocal(t).ClaudeAccounts)))
	}
}

// TestAKeyCredentialTakesNoClaudeLogin is the other flag a habit adds, and the
// one the value's own rule refuses: --claude runs Anthropic's own
// `claude setup-token` login and then reads the token it printed at the hidden
// prompt — so against a key credential it would store a Claude OAuth token
// under the credential's own derived name, as if it were the key the provider
// issues, and nothing would ever detect the swap. The run is refused before the
// login is screened and before a byte is written.
//
// The refusal follows the credential the config declares and not this run's
// flag, which is what the second case asserts: a re-run carries no --type, and
// accountsAddSecretType answers the type the file already declares — so the
// flag is refused for a credential declared by an earlier run too.
func TestAKeyCredentialTakesNoClaudeLogin(t *testing.T) {
	for _, c := range []struct {
		what, local string
		args        []string
	}{
		{"the flag declares the type", addRigLocal, []string{"accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter", "--claude"}},
		{"the config already declares it", accountsAddKeyCredentialLocal, []string{"accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude"}},
	} {
		t.Run(c.what, func(t *testing.T) {
			path := accountsAddFixture(t, c.local)
			before := readFileString(t, path)

			out, errOut, err := executeStdin(t, addToken()+"\n", c.args...)
			if err == nil {
				t.Fatalf("--claude was accepted for a key credential:\n%s\n%s", out, errOut)
			}
			if want := "--claude runs Anthropic's own"; !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not read %q: %v", want, err)
			}
			if !strings.Contains(err.Error(), config.CredentialSecret("team-a")) || !strings.Contains(err.Error(), "without --claude") {
				t.Errorf("the refusal does not name the container the value would land in and the fix: %v", err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
			if strings.Contains(out+errOut, addToken()) {
				t.Errorf("the refusal printed the value:\n%s\n%s", out, errOut)
			}
		})
	}
}

// TestAccountsAddAnthropicKey is the same for the other key type: the type is
// declared and the value lands under the credential's own derived secret name,
// which is the one container both the service-served and the runner-served
// type are stored in — what serves it is the type's half's business (MC23) and
// not this command's.
func TestAccountsAddAnthropicKey(t *testing.T) {
	r := newAddRig(t)
	writes := r.secretWrites(t)

	r.add(t, "team-key", "--repos", "owner/repo-a", "--type", "anthropic")

	id := credentialSecretID("team-key")
	if got := string(r.sm.Latest(id)); got != addToken() {
		t.Errorf("%s holds %q, want the value the command read", id, got)
	}
	if want := []string{id}; !slices.Equal(writes.created, want) || !slices.Equal(writes.added, want) {
		t.Errorf("the command wrote %v (created) and %v (versions), want only %v", writes.created, writes.added, want)
	}
	if got := string(r.sm.Latest(accountSecretID("team-key"))); got != "" {
		t.Errorf("the account's Claude token container holds %q, want nothing", got)
	}
	lc := r.addLocal(t)
	if want := []config.Credential{{Name: "team-key", Type: config.CredentialTypeAnthropic}}; !slices.Equal(lc.Credentials, want) {
		t.Errorf("the credentials block is %v, want %v", lc.Credentials, want)
	}
}

// TestKeyValueNeverFromArgvOrEnv is D1670's source rule as this command must
// hold it: the value comes from stdin or from the hidden prompt and from
// nowhere else, so neither a positional argument carrying it nor a variable
// exported under the name a key is usually passed in can become the value the
// credential ends up with.
//
// Both halves are asserted with an EMPTY stdin where the run gets as far as the
// read: a store that took a value from either would have something to report,
// and the assertion is that nothing was stored at all.
func TestKeyValueNeverFromArgvOrEnv(t *testing.T) {
	r := newAddRig(t)
	writes := r.secretWrites(t)

	// A positional argument is not a source: the command takes one NAME, and
	// the second argument is refused before anything is read or written.
	out, errOut, err := executeOwnerStdin(t, addToken()+"\n",
		"accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter", addToken())
	if err == nil {
		t.Fatalf("a value passed as an argument was accepted:\n%s\n%s", out, errOut)
	}
	if len(writes.created) > 0 || len(writes.added) > 0 {
		t.Errorf("the refused invocation wrote %v (created) and %v (versions): nothing may be stored for a value the command line carried", writes.created, writes.added)
	}

	// The environment is not one either. These are the names a key is exported
	// under in the wild, and a command that fell back to one would store a
	// value this run never read.
	t.Setenv("OPENROUTER_API_KEY", addToken())
	t.Setenv("ANTHROPIC_API_KEY", addToken())
	t.Setenv("FUGARO_OPENROUTER_KEY", addToken())
	t.Setenv("FUGARO_ANTHROPIC_API_KEY", addToken())
	out, errOut, err = executeOwnerStdin(t, "", "accounts", "add", "team-b", "--repos", "owner/repo-a", "--type", "openrouter")
	if err == nil {
		t.Fatalf("an empty stdin was accepted as the value:\n%s\n%s", out, errOut)
	}
	if got := string(r.sm.Latest(credentialSecretID("team-b"))); got != "" {
		t.Errorf("the credential's container holds %q, want nothing: the environment is not a source of the value", got)
	}
	if len(writes.created) > 0 || len(writes.added) > 0 {
		t.Errorf("the invocation wrote %v (created) and %v (versions), want nothing: a value no source carried is never stored", writes.created, writes.added)
	}
	if strings.Contains(out+errOut, addToken()) {
		t.Errorf("the command printed the value:\n%s\n%s", out, errOut)
	}
}

// TestKeyValueAtTheHiddenPromptIsNotEchoed is the terminal half of the same
// rule: on a real terminal the value is read at the hidden prompt — the one
// `fugaro secrets set` reads with, named for the secret it is stored under —
// and the terminal does not show what was typed.
func TestKeyValueAtTheHiddenPromptIsNotEchoed(t *testing.T) {
	r := providerKeyRig(t, providerKeyLocal)
	bundleID := bundleFixtureFor(t, r.sm, "aurora", nil)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter")
	pasteAtThePrompt(t, tty, config.CredentialSecret("team-a"), addToken())
	// The publish after the store loosens the shared node with the credential
	// this command declares — the grant members and nothing else — and a
	// grant-only publish is not confirmed by the project's name typed at a
	// terminal (N4, D1542/D1552, F24): the hidden prompt above is the only thing
	// this run waits at.
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("accounts add --type openrouter: %v\nterminal:\n%s", err, tty.displayed())
	}
	if strings.Contains(tty.displayed(), "Type "+initProjectName+" to confirm") {
		t.Errorf("the terminal asked for the project's name for a grant-only publish:\n%s", tty.displayed())
	}

	if strings.Contains(tty.displayed(), addToken()) {
		t.Errorf("the terminal shows the pasted value, so the hidden prompt echoed it:\n%s", tty.displayed())
	}
	if got := bundleEntry(t, r.sm, bundleID, mustSlug("github", "owner/repo-a"), "team-a").Value(); got != addToken() {
		t.Errorf("the bundle's entry holds %q, want the value pasted at the hidden prompt: the value reaches the entry and no terminal", got)
	}
}

// accountsAddKeyCredentialLocal is the state a run of this command WITH --type
// leaves behind, so the re-run below meets it: the name is declared a key
// credential, and the account it also is bounds where the key may be sent.
const accountsAddKeyCredentialLocal = addRigLocal + `credentials:
  - { name: team-a, type: openrouter }
claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
`

// accountsAddKeyCredentialServedLocal is the file's own half of the state
// TestAKeyCredentialTakesNoServingProfile refuses over, and it is what an
// earlier run of this command leaves: claude_accounts.team-a bounds the account
// to the repository and profiles.standard lists it, which is the pair that
// deploys the project job mounting claude-oauth-token-team-a as a run's
// CLAUDE_CODE_OAUTH_TOKEN. The name carries no credentials entry, so it is an
// OAuth account and not a key credential yet — and `--type` on it is the
// conversion the two key credentials exist for. Written out rather than
// appended to addRigLocal because the list the refusal is about is inside the
// profiles block, which an appended key cannot reach.
const accountsAddKeyCredentialServedLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
    claude_accounts:
      - team-a
repos:
  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }
`

// TestARerunWithoutTypeWritesOnlyTheCredentialsOwnSecret is the derive's other
// half, and the one the flag alone cannot answer. A re-run carries no --type
// for the ordinary reasons — a rotation, and the fix line `fugaro doctor`
// prints, which names no type — and what it meets is a name a previous run
// already declared a key credential. Reading the empty flag as "no type" would
// store the key under the ACCOUNT's own claude-oauth-token-<name>, the
// container a shared project job mounts as CLAUDE_CODE_OAUTH_TOKEN
// (infra/projectjobs.go): the account would be handed a key where its token
// belongs. So the store is the key credential's own, whatever this run's flag
// said, and the file it does not retype is the owner's.
//
// The key credential's own store is C1's entry in the bundle since KS2
// (D1670 reversed): the entry is the credential's, named after the credential's
// own allow_data_to, and the container the credential's derived name is the
// container OF is written by nobody (F33).
func TestARerunWithoutTypeWritesOnlyTheCredentialsOwnSecret(t *testing.T) {
	r := providerKeyRig(t, providerKeyLocal)
	bundleID := bundleFixtureFor(t, r.sm, "aurora", nil)
	writes := r.secretWrites(t)

	out, errOut := r.add(t, "team-a", "--repos", "owner/repo-a")

	entry := bundleEntry(t, r.sm, bundleID, mustSlug("github", "owner/repo-a"), "team-a")
	if entry.Value() != addToken() {
		t.Errorf("the bundle's entry holds %q, want the value the command read: the re-run stores the key where the credential's type says its key lives", entry.Value())
	}
	if got := r.sm.Latest(accountSecretID("team-a")); got != nil {
		t.Errorf("the account's Claude token container holds %q: a re-run without --type stored a KEY under the name the job mounts as CLAUDE_CODE_OAUTH_TOKEN", got)
	}
	if want := []string{credsvc.BundleSecretID("aurora")}; len(writes.created) != 0 || !slices.Equal(writes.added, want) {
		t.Errorf("the re-run wrote %v (created) and %v (versions), want no container created and one version of %v", writes.created, writes.added, want)
	}
	if shown := out + errOut; strings.Contains(shown, config.ClaudeAccountSecret("team-a")) {
		t.Errorf("the report names %s, the container the value did not land in:\n%s", config.ClaudeAccountSecret("team-a"), shown)
	}
	lc := r.addLocal(t)
	if want := []config.Credential{{Name: "team-a", Type: config.CredentialTypeOpenRouter}}; !slices.Equal(lc.Credentials, want) {
		t.Errorf("the credentials block is %v, want %v: a re-run without --type declares no entry and retypes none", lc.Credentials, want)
	}
}

// TestAccountsRmTakesAKeyCredentialsOwnContainer is the removal half of the
// pair `accounts add --type` writes, and the container is what it turns on: a
// key credential's value is the key its provider issued, under the credential's
// own derived secret name (config.CredentialSecret), so the account's own
// claude-oauth-token-<name> is another credential's container and holds nothing
// of this one's to delete. Asking Secret Manager for that one left the live
// provider key in the project with no command able to remove it, kept
// credentials.<name> beside it, and printed the account gone from an
// installation it was still in — the one state this command's own contract may
// not leave behind.
//
// The container the fixture seeds is the state an installation that stored the
// key before this task is in: since KS2 the writers enter the key in the
// bundle's entries and write no container of the credential's own, and the
// delete still has to take the container away wherever one is, which is what
// this test is about. What this task does NOT do is remove the bundle's
// entries: `accounts rm` is a deleter and its file is not one of KS2's, so a
// credential removed here can leave C1's entries behind, and the release that
// retires the containers (KS13-KS16's half) is where that has to land.
func TestAccountsRmTakesAKeyCredentialsOwnContainer(t *testing.T) {
	r := providerKeyRig(t, providerKeyLocal)
	bundleFixtureFor(t, r.sm, "aurora", nil)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	id := credentialSecretID("team-a")
	r.sm.Seed(id, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: "aurora", gcp.LabelSecret: config.CredentialSecret("team-a")}, []byte(addToken()))
	if r.sm.Latest(id) == nil {
		t.Fatalf("the fixture's %s holds no value, so the delete has nothing to take away", id)
	}

	out := r.rm(t, "team-a")

	if got, want := r.sm.Deleted(), []string{id}; !slices.Equal(got, want) {
		t.Errorf("the secret deletes were %v, want only %v, the container the credential's value is really in", got, want)
	}
	if v := r.sm.Latest(id); v != nil {
		t.Errorf("the secret %s still holds a value after the delete: %q", id, v)
	}
	if got := accountsAddRow(t, out, "secret"); !strings.Contains(got, config.CredentialSecret("team-a")) {
		t.Errorf("the block's secret line reads %q, and the container this run deleted is %s", got, config.CredentialSecret("team-a"))
	}
	if strings.Contains(out, config.ClaudeAccountSecret("team-a")) {
		t.Errorf("the report names %s, the container a key credential's value is never stored in:\n%s", config.ClaudeAccountSecret("team-a"), out)
	}
	lc := r.addLocal(t)
	if _, ok := lc.ClaudeAccounts["team-a"]; ok {
		t.Errorf("claude_accounts.team-a is still declared: the declaration and the value its allow_data_to bounds go together")
	}
	if slices.ContainsFunc(lc.Credentials, func(c config.Credential) bool { return c.Name == "team-a" }) {
		t.Errorf("the credentials block still declares team-a: %v, want the typed entry gone with the account it named", lc.Credentials)
	}
	if p := r.policy(t); len(p.Accounts) != 0 {
		t.Errorf("the published node still carries accounts = %v, want the account gone", slices.Sorted(maps.Keys(p.Accounts)))
	}
}

// TestNotYetTypesRefuse is D1623's half of the type rule, unchanged by this
// task: the two types the spec lists as future work refuse in words that name
// them, and a type that is no type of this release is refused by the config's
// own sentence — which is the one the loader prints for the same entry in the
// file, so the command and the loader cannot part company. Nothing is written
// for either.
func TestNotYetTypesRefuse(t *testing.T) {
	for _, tc := range []struct{ typ, want string }{
		{"openai", "openai is not yet supported"},
		{"codex-oauth", "codex-oauth is not yet supported"},
		{"bedrock", "must be one of openrouter, anthropic, claude-oauth"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			path := accountsPoolHome(t, "")
			before := readFileString(t, path)

			out, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--type", tc.typ)
			if err == nil {
				t.Fatalf("--type %q was accepted:\n%s", tc.typ, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestVertexStillRefusedAsBuiltIn is D1624's: vertex is the built-in
// credential — it needs no secret and exists in every installation — so it is
// never DECLARED, and the sentence is the config rule's own rather than a
// second one written here.
func TestVertexStillRefusedAsBuiltIn(t *testing.T) {
	path := accountsPoolHome(t, "")
	before := readFileString(t, path)

	out, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--type", "vertex")
	if err == nil {
		t.Fatalf("--type vertex was accepted:\n%s", out)
	}
	want := "vertex is built in: it needs no secret and exists in every installation, so it is never defined as an entry"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not say %q: %v", want, err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestACredentialTheConfigAlreadyDeclaresIsRefusedNotDuplicated: the entry a
// name already has is the owner's, so a key type may not be declared OVER it —
// a type decides which half of a run serves the name, and the sentence names
// the type the entry has. The refusal is what keeps the block one entry per
// name: an accepted retype would leave the owner's entry rewritten under them,
// and every pool that lists the name served on a type they did not write.
func TestACredentialTheConfigAlreadyDeclaresIsRefusedNotDuplicated(t *testing.T) {
	path := accountsAddFixture(t, accountsPoolTypedLocalSameType)
	before := readFileString(t, path)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "key-one", "--repos", "owner/repo-a", "--type", "openrouter")
	if err == nil {
		t.Fatalf("the credential key-one, defined as claude-oauth, was redeclared as openrouter:\n%s", out)
	}
	if !strings.Contains(err.Error(), "credentials.key-one is already defined as claude-oauth; nothing was written") {
		t.Errorf("the refusal reads %v, want the type the entry already has named", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lc.Credentials) != 1 || lc.Credentials[0].Name != "key-one" || lc.Credentials[0].Type != config.CredentialTypeClaudeOAuth {
		t.Errorf("the credentials block is %v, want key-one alone, still claude-oauth", lc.Credentials)
	}
}

// keyCredentialMounts is the job's own mounts for repo, built by internal/infra
// from a local config: SecretMounts is the one place that decides what a
// repository's job mounts, and the api-key auth is the shape a runner-path
// credential is spent under (the run's own gateway reads the provider key out of
// the job's environment).
func keyCredentialMounts(t *testing.T, lc *localcfg.Config, repo string) []infra.SecretMount {
	t.Helper()
	cfg := &config.Config{Agent: config.Agent{Auth: "api-key"}}
	mounts, err := infra.SecretMounts(config.GitHubAppKeySecret, cfg, "web", "", config.Workflow{}, lc, repo)
	if err != nil {
		t.Fatalf("SecretMounts(%s): %v", repo, err)
	}
	return mounts
}

// keyCredentialProviderLocal is the runner-path half of the test below: the
// provider a runner-path key credential is paired with — named after the
// credential it serves, which is the pairing internal/infra's runnerKeyCredential
// reads (config.CredentialSecret(provider) is the credential's derived name) —
// its own secret under the conventional spelling, and the account bound to the
// cloud fixture's one repository.
const keyCredentialProviderLocal = `providers:
  team-or:
    kind: anthropic-compat
    base_url: ` + providerKeyBaseURL + `
    auth: bearer
    secret: team-or-key
    models: ["deepseek/*"]
    allow_data_to: [acme/app]
credentials:
  - { name: team-or, type: openrouter }
claude_accounts:
  team-or: { allow_data_to: [acme/app] }
`

// TestKeyCredentialReachesItsServingHalf is MC23's red step (D1670) as KS2
// leaves it. The value of a key credential reaches the half of a run that
// serves its type, and the two halves are two documents: the bundle the
// credentials service reads — one MODEL entry per credential for a
// service-served type, at the tier the service offers it by, and one C1 entry
// per repository for a runner-path type (D1670 reversed) — and the job's own
// mounts, which internal/infra builds from the installation's config.
//
// The mounts are the release-A leftover KS2 did not touch: a repository whose
// config still pairs a provider with a key credential gets the credential's own
// container mounted, and since KS2 no writer gives that container a version —
// dropping the mount is the release that retires the containers (KS13-KS16,
// D3012). The assertion below is internal/infra's rule read as it is, so the
// task that drops the mounts is the one that changes it.
func TestKeyCredentialReachesItsServingHalf(t *testing.T) {
	t.Run("a service-served credential is a model entry of the bundle", func(t *testing.T) {
		// The skew fake with nothing deployed and no pin: this installation has
		// no credentials service yet, which is the state the entry is written in
		// (the guard's own test below is what an OLDER deployed service answers).
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, "none", skew)
		appendConfig(t, "credentials:\n  - { name: team-a, type: anthropic }\nclaude_accounts:\n  team-a: { allow_data_to: [acme/app] }\n")
		other := config.ClaudeOAuthToken + ": " + credsValue(t, "existing") + "\n"
		f.seed(t, []byte(other))
		value := credsValue(t, "keycred")

		if _, _, err := executeStdin(t, value+"\n", "creds", "set", config.CredentialSecret("team-a")); err != nil {
			t.Fatalf("creds set %s: %v", config.CredentialSecret("team-a"), err)
		}
		doc := f.value(t)
		b, err := credsvc.ParseBundle([]byte(doc))
		if err != nil {
			t.Fatalf("the document is one the service's own loader refuses: %v\n%s", err, doc)
		}
		entry, ok := b.Lookup("team-a")
		if !ok {
			t.Fatalf("the bundle holds no credential team-a, so the service offers a craft nothing for it:\n%s", doc)
		}
		if entry.Key != config.CredentialSecret("team-a") || entry.Kind != credsvc.KindModel {
			t.Errorf("the entry is %s/%s, want %s/%s", entry.Key, entry.Kind, config.CredentialSecret("team-a"), credsvc.KindModel)
		}
		if entry.Value() != value {
			t.Errorf("the entry does not hold the value this write read")
		}
		// The tier is the paid key's, which is what makes the service offer it
		// to a craft's call at all: creds.Select takes tier A first and B beside
		// the project's own Anthropic key.
		if got := b.Credentials(); !slices.Contains(got, creds.Credential{Name: "team-a", Tier: creds.TierB}) {
			t.Errorf("the service's credential list is %v, want team-a at tier B", got)
		}
		if !strings.Contains(doc, credsValue(t, "existing")) {
			t.Errorf("the write dropped the entry the document already held:\n%s", doc)
		}
	})

	t.Run("a runner-path credential is entered in the bundle", func(t *testing.T) {
		// The skew fake with nothing deployed and no pin: this installation has
		// no credentials service yet, which is the state the entry is written in
		// (the guard's own test is what an OLDER deployed service answers).
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, "none", skew)
		appendConfig(t, keyCredentialProviderLocal)
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		value := credsValue(t, "keycred")

		// The bundle takes it: this type's key is the key of the provider the
		// credential is named after, and C1's entry is where a routed call is
		// served from since KS2 (D1670 reversed).
		out, errOut, err := executeStdin(t, value+"\n", "creds", "set", config.CredentialSecret("team-or"))
		if err != nil {
			t.Fatalf("the bundle refused a runner-path credential's key: %v\n%s", err, errOut)
		}
		entry := bundleEntry(t, f.Secrets, f.id, appSlug, "team-or")
		if entry.Value() != value {
			t.Errorf("the entry for acme/app holds %q, want the value this write read", entry.Value())
		}
		if entry.BaseURL != providerKeyBaseURL || entry.Auth != "bearer" {
			t.Errorf("the entry carries base_url %q and auth %q, want the provider's own facts (F45, D1418)", entry.BaseURL, entry.Auth)
		}
		if doc := f.value(t); !strings.Contains(doc, credsValue(t, "existing")) {
			t.Errorf("the write dropped the entry the document already held:\n%s", doc)
		}
		if leak := out + errOut; strings.Contains(leak, value) {
			t.Errorf("the write's own output echoed the value: %q", leak)
		}

		lc := &localcfg.Config{
			Credentials:    []config.Credential{{Name: "team-or", Type: config.CredentialTypeOpenRouter}},
			ClaudeAccounts: map[string]config.ClaudeAccount{"team-or": {AllowDataTo: []string{"acme/app"}}},
			Providers: map[string]config.ModelProvider{"team-or": {
				Secret:      "team-or-key",
				BaseURL:     "https://openrouter.example/api/v1",
				Auth:        "bearer",
				Models:      []string{"deepseek/*"},
				AllowDataTo: []string{"acme/app"},
			}},
		}
		want := infra.SecretMount{Logical: config.CredentialSecret("team-or"), Env: lc.Providers["team-or"].SecretEnv()}
		if got := keyCredentialMounts(t, lc, "acme/app"); !slices.Contains(got, want) {
			t.Errorf("the job's mounts are %v, want %v among them: this is internal/infra's own rule and the release-A mount KS2 left in place, not the half a key is entered by", got, want)
		}
		// The credential's own bound decides as well: a repository
		// claude_accounts.team-or.allow_data_to does not name gets the
		// provider's own secret mounted as before and no credential key.
		if got := keyCredentialMounts(t, lc, "acme/other"); slices.Contains(got, want) {
			t.Errorf("a repository the credential does not admit mounts its key: %v", got)
		}
		// And a service-served credential is never a job's mount: the
		// credentials service serves it out of the bundle.
		lc.Credentials = []config.Credential{{Name: "team-or", Type: config.CredentialTypeAnthropic}}
		if got := keyCredentialMounts(t, lc, "acme/app"); slices.Contains(got, want) {
			t.Errorf("a service-served credential is mounted into the job: %v", got)
		}
	})
}

// TestAProviderKeyTheServiceCannotReadRefuses is AK10's guard over the new
// family (D1670, MC23): the bundle's loader refuses a document holding a key it
// does not know, and it refuses the WHOLE document rather than that entry — so a
// credentials service older than the release that reads the family must refuse
// the write, and `fugaro update` is the whole remedy.
//
// Both of the guard's triggers are here: the entry this write would add, and an
// entry the document already holds beside it (the write of ANY other credential
// into a document carrying the family needs a service that reads it).
func TestAProviderKeyTheServiceCannotReadRefuses(t *testing.T) {
	declared := "credentials:\n  - { name: team-a, type: anthropic }\nclaude_accounts:\n  team-a: { allow_data_to: [acme/app] }\n"
	older := "0.24.0" // the release before the family's reader

	t.Run("the entry this write adds", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, "none", skew)
		appendConfig(t, declared)
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		skew.deployedService(credentialsImage(older), credentialsImage(older))
		before := f.value(t)
		value := credsValue(t, "keycred")

		out, errOut, err := executeStdin(t, value+"\n", "creds", "set", config.CredentialSecret("team-a"))
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("a service that cannot read the family took the entry: err = %v", err)
		}
		for _, want := range []string{config.CredentialSecret("team-a"), keyCredentialSince, "fugaro update", "Nothing was read from you and nothing was written"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
		if n := f.writes(); n != 0 {
			t.Errorf("%d version(s) written, want none", n)
		}
		if got := f.value(t); got != before {
			t.Errorf("the refused write changed the document: %q", got)
		}
		if leak := out + errOut + err.Error(); strings.Contains(leak, value) {
			t.Errorf("the refusal echoed the value: %q", leak)
		}
	})

	t.Run("an entry the document already holds", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, "none", skew)
		f.seed(t, []byte(config.CredentialSecret("team-a")+": "+credsValue(t, "held")+"\n"))
		skew.deployedService(credentialsImage(older), credentialsImage(older))

		// Another credential's write: the document it would leave in place is
		// the one that decides, and this one carries the family.
		_, _, err := executeStdin(t, credsValue(t, "other")+"\n", "creds", "set", config.ClaudeOAuthToken)
		if err == nil {
			t.Fatal("a write into a document holding the family was admitted against a service that cannot read it")
		}
		if !strings.Contains(err.Error(), config.CredentialSecretPrefix+"<name>"+config.CredentialKeySuffix) {
			t.Errorf("the refusal does not name the family entry it judged: %v", err)
		}
		if n := f.writes(); n != 0 {
			t.Errorf("%d version(s) written, want none", n)
		}
	})

	t.Run("a service at the family's own release takes it", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		f, _ := credsSkewFixture(t, "none", skew)
		appendConfig(t, declared)
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		skew.deployedService(credentialsImage(keyCredentialSince), credentialsImage(keyCredentialSince))

		if _, _, err := executeStdin(t, credsValue(t, "keycred")+"\n", "creds", "set", config.CredentialSecret("team-a")); err != nil {
			t.Fatalf("creds set %s against a service that reads the family: %v", config.CredentialSecret("team-a"), err)
		}
		if n := f.writes(); n != 1 {
			t.Fatalf("%d version(s) written, want one", n)
		}
	})
}

// TestTheValueIsInNoDocumentTheRunRecordCarries: the value a key credential is
// stored with reaches the ONE half that serves it, and no document a run is
// built from carries it. The documents here are the ones a run's own record and
// the surfaces around it are assembled out of: the local config, the published
// node the runner and the credentials service both decode, the installation's
// job set (terraform.tfvars.json, which is the job spec's own source), and the
// command's own answer. Every one of them NAMES the credential — that is what
// makes the scan about the value and not about a document that says nothing —
// and none may hold a byte of the key.
func TestTheValueIsInNoDocumentTheRunRecordCarries(t *testing.T) {
	r := newAddRig(t)
	// add and no typed confirmation: a key credential's declaration is a
	// grant-only publish, which the gate judges and no name is typed for
	// (N4), and this is the helper every other key-credential test here uses.
	out, errOut := r.add(t, "team-a", "--repos", "owner/repo-a", "--type", "anthropic")
	value := addToken()
	// The documents below are worth scanning only because they are the ones a
	// run about this credential decodes: the config declares it, so the node and
	// the job set derived from it are this credential's own.
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := config.CredentialNamed(lc.Credentials, "team-a"); !ok || c.Type != config.CredentialTypeAnthropic {
		t.Fatalf("the config does not declare team-a as an anthropic credential (%v): this scan would pass on a document that says nothing about it", lc.Credentials)
	}

	node, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := infra.InstallationWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := infra.ReadWorkdirVars(dir, "installation")
	if err != nil {
		t.Fatalf("the installation's variables were not written: %v", err)
	}
	for what, doc := range map[string]string{
		"the local config":           readFileString(t, r.cfg),
		"the published node":         string(node),
		"the installation's job set": string(vars),
		"the command's own answer":   out + errOut,
	} {
		if strings.Contains(doc, value) {
			t.Errorf("%s carries the value:\n%s", what, doc)
		}
	}
	// And the credential's own container is the one place it IS, written by the
	// command's own call: the negative above is worth nothing unless something
	// stored it.
	if got := string(r.sm.Latest(credentialSecretID("team-a"))); got != value {
		t.Errorf("the credential's container holds %q, want the value this command read", got)
	}
}

// TestTheBundleWriteIsNotInAnAgentSession: an agent's session may write the
// DECLARATION and never the value (D1671, MC23). The bundle carries every model
// credential the service issues, so `fugaro creds set` is the owner's own step
// (credsOwnerOnly) — and the entry this task adds is not an exception to it: the
// value of a service-served key credential is read at the owner's terminal like
// every other entry, and an agent's session that reaches for it is refused
// before anything is read.
func TestTheBundleWriteIsNotInAnAgentSession(t *testing.T) {
	f := credsFixtureFor(t)
	appendConfig(t, "credentials:\n  - { name: team-a, type: anthropic }\nclaude_accounts:\n  team-a: { allow_data_to: [acme/app] }\n")
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	before := f.value(t)
	agentSession(t)
	value := credsValue(t, "keycred")

	out, errOut, err := executeStdin(t, value+"\n", "creds", "set", config.CredentialSecret("team-a"))
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a coding agent's session wrote a credential's key: err = %v", err)
	}
	for _, want := range []string{"nothing was written", "person at their own terminal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := f.writes(); n != 0 {
		t.Errorf("%d version(s) written, want none", n)
	}
	if got := f.value(t); got != before {
		t.Errorf("the refused write changed the document: %q", got)
	}
	if leak := out + errOut + err.Error(); strings.Contains(leak, value) {
		t.Errorf("the refusal echoed the value: %q", leak)
	}
}

// TestKeyCredentialSinceHasAReleaseBullet: keyCredentialSince names the release
// whose credentials service first reads the family, and it is the first such
// constant whose release is not tagged yet, so docs/release.md's Before-you-tag
// list is where a release verifies it against the tag actually cut — and where
// the direction that matters (a raise) is pinned.
func TestKeyCredentialSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `keyCredentialSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no keyCredentialSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+keyCredentialSince+"\"`") {
		t.Errorf("docs/release.md's keyCredentialSince bullet does not carry the constant's release (%q)", keyCredentialSince)
	}
	for _, want := range []string{"the release that ships the family", "fugaro creds set", "must not raise it", "not tagged yet"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's keyCredentialSince bullet does not say %q", want)
		}
	}
}

// TestKeyCredentialSinceIsNotAheadOfTheReleaseThatShipsIt: the sibling
// constants' direction, with the bound read off the release that ships the
// family rather than off the stream. Their tests hold the constant at or BELOW
// docs/release-streams.yaml's `current`, because what the guard judges is the
// service an installation has DEPLOYED: a value above the release whose service
// reads the family refuses `fugaro creds set credential-<name>-key` on every
// installation that already serves it, where the refusal's remedy (`fugaro
// update`) changes nothing. That form cannot be written here yet — the family
// has never been in a released image, so the stream is BELOW the constant, and
// the sibling's assertion would name the release that cannot read the family as
// the one the constant may not pass, turning a release bump into a demand for
// the raise the constant must never take.
//
// The same bound one release looser does hold in both regimes, and it is the
// direction that bites: the family is in this tree, so the release that ships it
// is the stream's own or the one the stream makes next (one name per minor, and
// a hotfix is cut from its own branch and never merged back), and a constant
// above THAT refuses writes on installations whose deployed service already
// reads the family. From the release that ships the family on, this is the
// sibling test's own assertion.
//
// The other direction — a constant naming a release BELOW the one that ships
// the family, which admits the write that halts that release's service — has no
// test here and cannot have one: it is invisible to a file a release bump reads
// while moving `current`, and it is what the constant's bullet in
// docs/release.md is verified against at the tag actually cut.
func TestKeyCredentialSinceIsNotAheadOfTheReleaseThatShipsIt(t *testing.T) {
	cat, err := streams.Parse("docs/release-streams.yaml", []byte(readDoc(t, "docs/release-streams.yaml")))
	if err != nil {
		t.Fatalf("docs/release-streams.yaml: %v", err)
	}
	cli, ok := cat.Lookup(streams.CLI)
	if !ok {
		t.Fatal("docs/release-streams.yaml carries no cli stream, so the release this tree is on cannot be read")
	}
	shipping := cli.Current
	if imagePredates(cli.Current.String(), keyCredentialSince) {
		shipping = streams.Version{Major: cli.Current.Major, Minor: cli.Current.Minor + 1}
	}
	if imagePredates(shipping.String(), keyCredentialSince) {
		t.Errorf("keyCredentialSince = %q is ahead of %s, the release that ships the family: the cli stream is at %s, this tree carries MC22's declaration and MC23's halves, and one release spends one minor, so a value above %s refuses `fugaro creds set credential-<name>-key` on installations whose deployed service already serves the family — a release verifies this constant against the tag actually cut and never raises it", keyCredentialSince, shipping, cli.Current, shipping)
	}
	if imagePredates(keyCredentialSince, providerEntrySince) {
		t.Errorf("keyCredentialSince = %q predates providerEntrySince = %q: the family's reader is newer than the entry that shipped before it", keyCredentialSince, providerEntrySince)
	}
}

// TestAccountsLsNamesTheServingPath is D1671's reading half for the key
// credentials: the credentials block is what a pool's members may list and a
// craft's access may name, and which of a project's two halves serves an entry
// is decided by its type alone (config.CredentialServingPath), so the listing
// answers it offline, out of the config. The name is where the need is
// sharpest: a key credential's name is deliberately NOT in the accounts above
// it (config.ClaudeAccountTokenNames, TestAccountsLsLeavesAKeyCredentialOut),
// so a read that listed the accounts alone left the block invisible and the
// owner opening the file to find out which half has to hold a credential's
// value.
func TestAccountsLsNamesTheServingPath(t *testing.T) {
	f := newCloudFixture(t)
	// One entry whose name is also a claude_accounts name — the state a key
	// credential is declared in — and one of the other type, so both halves are
	// read off one run.
	f.appendConfig(t, claudeAccountsConfig+`credentials:
  - { name: acme-overflow, type: openrouter }
  - { name: acme-key, type: anthropic }
`)
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	want := []accountsCredential{
		{Name: "acme-key", Type: config.CredentialTypeAnthropic, ServingPath: config.CredentialPathCredsvc},
		{Name: "acme-overflow", Type: config.CredentialTypeOpenRouter, ServingPath: config.CredentialPathRunner},
	}
	if !slices.Equal(doc.Credentials, want) {
		t.Errorf("credentials = %+v, want %+v: each entry with its type and the half that serves it", doc.Credentials, want)
	}
	// The two readings are separate answers about the same file: naming a
	// credential here does not put its name back among the accounts a --token
	// run may be given.
	if got := []string{"default", "acme-personal"}; !slices.Equal(doc.Accounts, got) {
		t.Errorf("accounts = %v, want %v: the credentials block is listed as what it is, not as accounts", doc.Accounts, got)
	}
	text, _, err := execute(t, "accounts", "ls", "--repo", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"acme-overflow (openrouter, served by a run's own gateway)",
		"acme-key (anthropic, served by the credentials service)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the text output does not read %q:\n%s", want, text)
		}
	}
}

// accountsAddKeyCredentialDeclaredLocal is the name already declared a key
// credential with nothing else written for it: the state a session re-running
// the declaration starts from, where the command's own type resolution
// (accountsAddSecretType) makes the run a key credential's without --type, and
// the account's entry is the one thing this run would add.
const accountsAddKeyCredentialDeclaredLocal = addRigLocal + `credentials:
  - { name: team-a, type: openrouter }
`

// TestKeyCredentialWritesRefuseInAgentSession is D1671's fence for a key
// credential. Making one is three writes and not one — the account's
// declaration, the entry in the credentials block, and the pool membership or
// craft access that spends through it — so what a session may do is decided per
// write rather than for the command: the declaration is the session's, and
// every write that would make the name a spending or data-egress grant is
// refused there whatever is passed (F12/D1628, F22/D1636), the value included.
//
// The four cases below are the four writes. Two of them are a world each: the
// credential entry is refused by the managed project's own rule where the gate
// is consulted and by the file's marker fence in an installation that is not
// managed, and a build that fenced only one of the two would pass a test that
// pinned the other. The pool's own write is TestPoolWritesRefuseInAgentSession's
// subject in full; it is here because a key credential is what its members are.
func TestKeyCredentialWritesRefuseInAgentSession(t *testing.T) {
	t.Run("the credential entry is refused where the gate is consulted", func(t *testing.T) {
		r := newManagedAddRig(t, addRigLocal, addMarksManaged)
		writes := r.secretWrites(t)
		before := readFileString(t, r.cfg)
		t.Setenv(accountsAddAgent, "1")

		// A value piped at the hidden prompt is passed on purpose: "whatever is
		// passed" is the claim, and the value is the one thing a session might
		// hope the command reads anyway.
		out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter")
		if err == nil {
			t.Fatalf("a session declared credentials.team-a:\n%s", out)
		}
		if err.Error() != accountsAddRefusalCredential {
			t.Errorf("the refusal is:\n%q\nwant:\n%q", err.Error(), accountsAddRefusalCredential)
		}
		if len(writes.created) != 0 || len(writes.added) != 0 {
			t.Errorf("the refusal stored %v (created) and %v (versions), want nothing", writes.created, writes.added)
		}
		if after := readFileString(t, r.cfg); after != before {
			t.Errorf("the refused write changed %s:\n%s", r.cfg, after)
		}
	})

	t.Run("the credential entry is refused by the file's own fence", func(t *testing.T) {
		path := accountsPoolHome(t, "")
		before := readFileString(t, path)
		t.Setenv(accountsAddAgent, "1")

		out, errOut, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "acme/app", "--type", "openrouter")
		if err == nil {
			t.Fatalf("a session declared credentials.team-a in a project that is not managed:\n%s\n%s", out, errOut)
		}
		if !strings.Contains(err.Error(), accountsAddAgent) || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("the refusal does not name the marker and say nothing was written: %v\n%s\n%s", err, out, errOut)
		}
		if got := readFileString(t, path); got != before {
			t.Errorf("the refused write changed the file:\n%s", got)
		}
	})

	t.Run("the declaration is admitted and the value is not", func(t *testing.T) {
		r := newManagedAddRig(t, accountsAddKeyCredentialDeclaredLocal, addMarksManaged)
		writes := r.secretWrites(t)
		t.Setenv(accountsAddAgent, "1")

		_, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a")
		if err == nil || err.Error() != accountsAddRefusalKey {
			t.Fatalf("a session's re-run of a declared key credential ended %v, want the key step's refusal:\n%q", err, accountsAddRefusalKey)
		}
		// The declaration is the session's to write: what it may not do is make
		// the name spendable, and the two halves are one run apart.
		if lc := r.addLocal(t); !slices.Equal(lc.ClaudeAccounts["team-a"].AllowDataTo, []string{"owner/repo-a"}) {
			t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the declared list: the session writes the declaration", lc.ClaudeAccounts["team-a"].AllowDataTo)
		}
		if len(writes.created) != 0 || len(writes.added) != 0 {
			t.Errorf("the session stored the key: created %v, added %v", writes.created, writes.added)
		}
	})

	// The pool's fence names the marker it refused under, so this leg is held
	// over agentenv.Markers itself — a marker the tree adds is one this write is
	// held to — and it is the cheap half of the test: a config and no cloud.
	t.Run("a pool that would list the credential is refused", func(t *testing.T) {
		for _, marker := range agentenv.Markers {
			t.Run(marker, func(t *testing.T) {
				path := accountsPoolHome(t, "")
				before := readFileString(t, path)
				t.Setenv(marker, "1")

				out, errOut, err := execute(t, "accounts", "pool", "add", "paid", "router-one")
				if err == nil {
					t.Fatalf("a session wrote a pool:\n%s\n%s", out, errOut)
				}
				for _, want := range []string{marker, "nothing was written"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not say %q: %v\n%s\n%s", want, err, out, errOut)
					}
				}
				if got := readFileString(t, path); got != before {
					t.Errorf("the refused pool changed the file:\n%s", got)
				}
			})
		}
	})

	t.Run("a craft's access that would name the credential is refused", func(t *testing.T) {
		path := accountsPoolHome(t, "")
		before := readFileString(t, path)
		t.Setenv(accountsAddAgent, "1")

		// The row is the accounts family's, so the generic path refuses it
		// wherever it runs — a session and the owner's own terminal alike — which
		// is the strongest form of this leg: no write of a craft's access exists
		// in this release at all (accountsRmPoolMember's own note).
		out, errOut, err := execute(t, "settings", "set", "agent.crafts.coder.access", "router-one")
		if err == nil {
			t.Fatalf("a session wrote a craft's access:\n%s\n%s", out, errOut)
		}
		if !strings.Contains(err.Error(), "fugaro accounts") {
			t.Errorf("the refusal does not name the family that writes the row: %v\n%s\n%s", err, out, errOut)
		}
		if got := readFileString(t, path); got != before {
			t.Errorf("the refused write changed the file:\n%s", got)
		}
	})
}

// TestAccountsLsNamesTheServingPathBesideTheStates is the same reading on the
// other text path: with a state column the accounts are a table, and the
// credentials block is written after it, one line each — so a line of the block
// is never read as an account's state, which is what a listing somebody diffs
// line by line turns on. The lines above it stay the accounts and their states.
func TestAccountsLsNamesTheServingPathBesideTheStates(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig+"credentials:\n  - { name: acme-key, type: openrouter }\n")

	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if got, want := lines[len(lines)-1], "acme-key (openrouter, served by a run's own gateway)"; got != want {
		t.Fatalf("the last line reads %q, want %q:\n%s", got, want, out)
	}
	for _, line := range lines[:len(lines)-1] {
		name, state, ok := strings.Cut(line, " ")
		if !ok || strings.TrimSpace(state) == "" {
			t.Fatalf("line %q carries no state: the account lines are the names and their states, and the credentials come after them:\n%s", line, out)
		}
		if name == "acme-key" {
			t.Fatalf("the credential is listed as an account with a state:\n%s", out)
		}
	}
}

// ---------------------------------------------------------------------------
// KS2: every writer of a provider key writes the bundle.
//
// A provider key has THREE writers (F27) — `fugaro creds set
// credential-<name>-key`, `fugaro accounts add --type openrouter` and `fugaro
// secrets set` of a provider's own secret (at repository scope and
// project-wide) — and since KS2 (D1670 reversed by D2975, R2) all three write
// the same thing: C1's entries repo-credential--<slug>--<credential> in the
// credentials service's bundle, one per repository the key is bound to, all of
// them in ONE version (O8, D3008), and no container of the credential's own
// (F33). The provider whose address and auth the entry carries must be declared
// (F45, D1418), and the write is a project owner's (D3006, F36).
//
// What the tests below are about is the three writers agreeing: the same entry
// name, the same value, the same list, the same guard and the same owner. A
// second implementation of "where does a provider key go" is what this section
// exists to fail.

// providerKeyBaseURL is the address the fixture's provider is declared with:
// deliberately not a provider the tree knows, so an assertion that reads it out
// of an entry is reading what the CONFIG said.
const providerKeyBaseURL = "https://openrouter.example/api/v1"

// providerKeyProviderLocal is the provider a runner-path key credential needs,
// its own secret, the credential itself, and the one account — all bound to the
// rig's single repository. The provider's secret is the conventional name
// (<name>-api-key), which is the spelling `fugaro secrets set` stores a
// provider's key under.
const providerKeyProviderLocal = `providers:
  team-a:
    kind: anthropic-compat
    base_url: ` + providerKeyBaseURL + `
    auth: bearer
    secret: team-a-api-key
    models: ["deepseek/*"]
    allow_data_to: [owner/repo-a]
credentials:
  - { name: team-a, type: openrouter }
claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
`

// providerKeyLocal is that block over the rig's own repository.
const providerKeyLocal = addRigLocal + providerKeyProviderLocal

// providerKeyTwoReposLocal is the same world with the credential, the provider
// and the account bound to TWO repositories: one command names several, and the
// several entries have to travel in one version (O8, D3008) — a version holding
// a shared key for some of its repositories is a key that serves a run on some
// of them and not on others.
const providerKeyTwoReposLocal = addRigLocal + `  owner/repo-b: { provider: github, github_app_id: "12345", workflows: [web] }
providers:
  team-a:
    kind: anthropic-compat
    base_url: ` + providerKeyBaseURL + `
    auth: bearer
    secret: team-a-api-key
    models: ["deepseek/*"]
    allow_data_to: [owner/repo-a, owner/repo-b]
credentials:
  - { name: team-a, type: openrouter }
claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
      - owner/repo-b
`

// providerKeyRig is newAddRigWith over local, with the Cloud Run endpoint
// answering the credentials service's own read the guard makes
// (providerKeyEndpoint) and no service deployed: what the guard then judges is
// the installation's own pin, and this fixture has none.
func providerKeyRig(t *testing.T, local string) *addRig {
	t.Helper()
	return providerKeyRigAt(t, local, "")
}

// providerKeyRigAt is providerKeyRig with the credentials service DEPLOYED and
// serving the release rel, which is what a guard test needs: the release a
// deployed service runs is the one read, whatever this tree's version is.
func providerKeyRigAt(t *testing.T, local, rel string) *addRig {
	t.Helper()
	r := newAddRigWith(t, local)
	skew := newRunServiceSkew(t)
	if rel != "" {
		skew.deployedService(credentialsImage(rel), credentialsImage(rel))
	}
	providerKeyEndpoint(t, r.run, skew)
	return r
}

// providerKeyCloudProvider is the same provider over the CLOUD fixture's own
// repository (acme/app, which is the one repository newCloudFixture's config
// lists): the `fugaro secrets set` half of this section, where the name stored
// under is the provider's own secret and the entry is acme/app's.
const providerKeyCloudProvider = `providers:
  team-a:
    kind: anthropic-compat
    base_url: ` + providerKeyBaseURL + `
    auth: bearer
    secret: team-a-api-key
    models: ["deepseek/*"]
    allow_data_to: [acme/app]
`

// TestCredsSetAcceptsAnOpenRouterKey is KS2's red step read the other way
// round: `creds set credential-<name>-key` refused a RUNNER-path key
// credential's name before this task (credsKeyCredentialHalfRefusal), and what
// the command does now is enter the key where the credentials service serves a
// routed call from — C1's family, one entry per repository the credential's own
// allow_data_to names, ALL of them in one version (O8, R2, D3008) — and write
// no container of the credential's own (F33).
//
// The entry carries the provider's own two routing facts and not the caller's
// (F45, D1418), which is what the BaseURL and Auth assertions are about: the
// person typed a key and nothing else.
func TestCredsSetAcceptsAnOpenRouterKey(t *testing.T) {
	r := providerKeyRig(t, providerKeyTwoReposLocal)
	bundleID := bundleFixtureFor(t, r.sm, "aurora", nil)
	value := addToken()

	if _, errOut, err := executeStdin(t, value+"\n", "creds", "set", config.CredentialSecret("team-a")); err != nil {
		t.Fatalf("creds set %s with a runner-path credential declared was refused: %v\n%s", config.CredentialSecret("team-a"), err, errOut)
	}
	for _, repo := range []string{"owner/repo-a", "owner/repo-b"} {
		entry := bundleEntry(t, r.sm, bundleID, mustSlug("github", repo), "team-a")
		if entry.Value() != value {
			t.Errorf("the entry for %s holds %q, want the value the command read: a provider key IS the bundle now, and this is the second spelling of its write", repo, entry.Value())
		}
		if entry.BaseURL != providerKeyBaseURL || entry.Auth != "bearer" {
			t.Errorf("the entry for %s carries base_url %q and auth %q, want the provider's own %q and bearer: the destination of a routed call is the entry's and never a caller's (F45, D1418)", repo, entry.BaseURL, entry.Auth, providerKeyBaseURL)
		}
		if !entry.Source.IsZero() {
			t.Errorf("the entry for %s carries a copy's source (%+v): a value typed at a terminal was copied from no container", repo, entry.Source)
		}
	}
	// One version for the two entries, and no container of the credential's own.
	if got := r.sm.Latest(credentialSecretID("team-a")); got != nil {
		t.Errorf("%s holds %q: a provider key is entered in the bundle since KS2 and no container of the credential's own is written (F33)", credentialSecretID("team-a"), got)
	}
}

// TestAccountsAddOpenRouterWritesTheBundle is MC22's declaration and KS2's
// write in one run: `accounts add --type openrouter` declares the credential
// and enters its key in the bundle, one entry per repository the credential is
// bound to — the list this very run wrote into claude_accounts.<name>.allow_data_to
// — and no container of the credential's own. The account's own Claude token
// container is left without a version either: a key credential is not another
// name for the account (MC22's own fence, which this test inherits).
func TestAccountsAddOpenRouterWritesTheBundle(t *testing.T) {
	r := providerKeyRig(t, providerKeyTwoReposLocal)
	writes := r.secretWrites(t)
	bundleID := bundleFixtureFor(t, r.sm, "aurora", nil)

	out, errOut := r.add(t, "team-a", "--repos", "owner/repo-a,owner/repo-b", "--type", "openrouter")
	value := addToken()

	for _, repo := range []string{"owner/repo-a", "owner/repo-b"} {
		entry := bundleEntry(t, r.sm, bundleID, mustSlug("github", repo), "team-a")
		if entry.Value() != value {
			t.Errorf("the entry for %s holds %q, want the value the command read", repo, entry.Value())
		}
		if entry.BaseURL != providerKeyBaseURL || entry.Auth != "bearer" {
			t.Errorf("the entry for %s carries base_url %q and auth %q, want the provider's own facts: providers.team-a is where the entry's destination comes from (F45, D1418)", repo, entry.BaseURL, entry.Auth)
		}
	}
	// The credential's declaration and the account's reach, both written by this
	// run and both what the entries were built from.
	lc := r.addLocal(t)
	if want := []config.Credential{{Name: "team-a", Type: config.CredentialTypeOpenRouter}}; !slices.Equal(lc.Credentials, want) {
		t.Errorf("the credentials block is %v, want %v", lc.Credentials, want)
	}
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a", "owner/repo-b"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the two repositories the entries were written for", got)
	}
	// Nothing was CREATED: the bundle container exists before this command
	// (C1.18's Terraform), and a container of the credential's own is what this
	// task removed.
	if len(writes.created) != 0 {
		t.Errorf("the command created %v: a provider key's write creates no container — the bundle exists before it and the credential has none of its own", writes.created)
	}
	if got := r.sm.Latest(credentialSecretID("team-a")); got != nil {
		t.Errorf("%s holds %q: no container of the credential's own is written (F33)", credentialSecretID("team-a"), got)
	}
	if got := r.sm.Latest(accountSecretID("team-a")); got != nil {
		t.Errorf("the account's Claude token container holds %q: a key credential is not the account's token", got)
	}
	if !strings.Contains(out+errOut, "no job: a key credential rides no job of its own") {
		t.Errorf("the command does not say that no job was deployed for it:\n%s\n%s", out, errOut)
	}
	// No profile lists the name, so no job of the account's own exists for it:
	// profiles.<p>.claude_accounts is the list that serves a Claude account
	// TOKEN, which is what deploys the job mounting it as CLAUDE_CODE_OAUTH_TOKEN.
	// The bundle entry is what carries this key now, and a job mounting an empty
	// token container is the leftover this task's writers do not create.
	if slices.Contains(lc.Profiles["standard"].ClaudeAccounts, "team-a") {
		t.Errorf("profiles.standard.claude_accounts = %v, want no team-a: a key credential is not a run's Claude account token", lc.Profiles["standard"].ClaudeAccounts)
	}
	jobs := r.projectJobs(t)
	if _, ok := jobs[gcp.ProjectJobName("standard", "github", "team-a")]; ok {
		t.Errorf("the job set carries a job for the key credential: jobs = %v", slices.Sorted(maps.Keys(jobs)))
	}
	for jobName, j := range jobs {
		for _, mounted := range j.SecretEnv {
			if mounted == config.ClaudeAccountSecret("team-a") {
				t.Errorf("job %s mounts %s, which nothing writes for a key credential: its key is the bundle's entry now", jobName, mounted)
			}
		}
	}
	// The confirmation and the block speak the key credential's words and not
	// the account's (accountsAddWhat): the name is served by no profile, so
	// "no profile serves it yet" would describe a state the command just left
	// behind on purpose, and what a run beyond this checkout may reach is the
	// key — a run cannot print a token nothing holds.
	shown := out + errOut
	for _, wrong := range []string{"no profile serves it yet", "print team-a's token", "fugaro run --token"} {
		if strings.Contains(shown, wrong) {
			t.Errorf("the command's answer uses the account's word %q for a key credential:\n%s", wrong, shown)
		}
	}
	if !strings.Contains(shown, "can have that run use team-a's key") {
		t.Errorf("the command does not say what the reach beyond this checkout means for a key:\n%s", shown)
	}
	// The value reaches no stream, no log and no document a run is built from:
	// the entry is the one place it IS.
	for what, doc := range map[string]string{
		"the command's own answer": out + errOut,
		"the local config":         readFileString(t, r.cfg),
	} {
		if strings.Contains(doc, value) {
			t.Errorf("%s carries the value:\n%s", what, doc)
		}
	}
}

// TestSecretsSetOfAProviderSecretWritesTheBundle: the third writer (F27). A
// model provider's own secret is stored under two spellings, and both write the
// bundle and no container: `--repo <r>` writes the one entry of the repository
// the value was scoped to, and `--project-wide` writes one entry for every
// repository providers.<p>.allow_data_to names.
//
// The container this command used to write — gcp.ProjectSecretID for the
// project copy, gcp.SecretID for the repository's — is asserted absent, which is
// what "and no container" means at a store that has written both for years.
func TestSecretsSetOfAProviderSecretWritesTheBundle(t *testing.T) {
	sm := secretsFixture(t)
	appendConfig(t, providerKeyCloudProvider)
	bundleID := bundleFixtureFor(t, sm, "aurora", nil)
	value := "or-not-a-real-key-1234"

	// The repository-scope spelling: one repository named, one entry.
	if _, errOut, err := executeStdin(t, value, "secrets", "set", "team-a-api-key", "--repo", "acme/app"); err != nil {
		t.Fatalf("a provider's own secret at repo scope was refused: %v\n%s", err, errOut)
	}
	entry := bundleEntry(t, sm, bundleID, appSlug, "team-a")
	if entry.Value() != value {
		t.Errorf("the entry for acme/app holds %q, want the value the command read", entry.Value())
	}
	if entry.BaseURL != providerKeyBaseURL || entry.Auth != "bearer" {
		t.Errorf("the entry carries base_url %q and auth %q, want the provider's own facts", entry.BaseURL, entry.Auth)
	}
	if got := sm.Latest(gcp.SecretID(appSlug, "team-a-api-key")); got != nil {
		t.Errorf("the repository's own container holds %q: a provider's key writes no container any more", got)
	}

	// The project-wide spelling: the provider's own allow_data_to, which is
	// acme/app here, and the same entry in a new version.
	second := "or-not-a-real-key-5678"
	if _, errOut, err := executeStdin(t, second, "secrets", "set", "team-a-api-key", "--project-wide"); err != nil {
		t.Fatalf("a provider's own secret project-wide was refused: %v\n%s", err, errOut)
	}
	if got := bundleEntry(t, sm, bundleID, appSlug, "team-a").Value(); got != second {
		t.Errorf("the entry holds %q, want the second value: the project-wide store writes the same entry", got)
	}
	if got := sm.Latest(gcp.ProjectSecretID("aurora", "team-a-api-key")); got != nil {
		t.Errorf("the project's own container holds %q: a provider's key writes no container any more", got)
	}
}

// TestNoWriterCreatesAContainer is the "and no container" half of the task, over
// all three writers at once: the bundle container is created by the
// installation's Terraform (C1.18) and by nothing else, and no writer of a
// provider key makes a container of the credential's own, of the provider's
// secret, or of the account's token. A container nothing reads is what F33 is
// about, and a write that created one would be the second home for a key this
// task removed.
//
// It is one test and not three assertions inside the three above because the
// claim is about the SET of writers: a fourth writer added beside them is the
// change that must meet this test.
func TestNoWriterCreatesAContainer(t *testing.T) {
	t.Run("creds set", func(t *testing.T) {
		r := providerKeyRig(t, providerKeyLocal)
		bundleFixtureFor(t, r.sm, "aurora", nil)
		writes := r.secretWrites(t)
		if _, errOut, err := executeStdin(t, addToken()+"\n", "creds", "set", config.CredentialSecret("team-a")); err != nil {
			t.Fatalf("creds set: %v\n%s", err, errOut)
		}
		if got := slices.Sorted(slices.Values(writes.created)); !slices.Equal(got, []string{}) {
			t.Errorf("creds set created %v, want no container at all", got)
		}
		if got := slices.Sorted(slices.Values(writes.added)); !slices.Equal(got, []string{credsvc.BundleSecretID("aurora")}) {
			t.Errorf("creds set added a version to %v, want the bundle alone", got)
		}
	})
	t.Run("accounts add", func(t *testing.T) {
		r := providerKeyRig(t, providerKeyLocal)
		bundleFixtureFor(t, r.sm, "aurora", nil)
		writes := r.secretWrites(t)
		r.add(t, "team-a", "--repos", "owner/repo-a", "--type", "openrouter")
		if got := slices.Sorted(slices.Values(writes.created)); !slices.Equal(got, []string{}) {
			t.Errorf("accounts add created %v, want no container at all", got)
		}
		if got := slices.Sorted(slices.Values(writes.added)); !slices.Equal(got, []string{credsvc.BundleSecretID("aurora")}) {
			t.Errorf("accounts add added a version to %v, want the bundle alone", got)
		}
	})
	t.Run("secrets set", func(t *testing.T) {
		sm := secretsFixture(t)
		appendConfig(t, providerKeyCloudProvider)
		bundleID := bundleFixtureFor(t, sm, "aurora", nil)
		if _, errOut, err := executeStdin(t, "or-not-a-real-key-1234", "secrets", "set", "team-a-api-key", "--project-wide"); err != nil {
			t.Fatalf("secrets set: %v\n%s", err, errOut)
		}
		var created []string
		for _, r := range sm.Requests() {
			if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/secrets") {
				created = append(created, r.Path)
			}
		}
		if len(created) != 0 {
			t.Errorf("secrets set created %v, want no container at all: the bundle is created by the installation's Terraform", created)
		}
		if sm.Latest(bundleID) == nil {
			t.Errorf("the bundle holds no version: this test asserts no container was created by a write that wrote nothing")
		}
	})
}

// TestAnOlderServiceIsNotWrittenANamedEntry: C1's family is a reader the
// credentials service has only from repoCredentialSince on, and the loader
// refuses the WHOLE document for a key it does not know — so a service older
// than that release must refuse every provider-key write, and `fugaro update` is
// the whole remedy (the shape providerEntrySince's and keyCredentialSince's own
// tests have).
//
// All three writers are here because the guard is in the ONE write they share
// (providerKeyBundleWrite, F27), and the release judged is the one the
// deployed service SERVES — the 0.26.0 service beside it reads the
// key-credential family and still cannot read this one, which is the case a
// constant of its own exists for.
func TestAnOlderServiceIsNotWrittenANamedEntry(t *testing.T) {
	older := keyCredentialSince // a service that reads the tier-B family and not C1's

	t.Run("creds set", func(t *testing.T) {
		r := providerKeyRigAt(t, providerKeyLocal, older)
		bundleID := bundleFixtureFor(t, r.sm, "aurora", nil)
		value := addToken()
		out, errOut, err := executeStdin(t, value+"\n", "creds", "set", config.CredentialSecret("team-a"))
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("creds set against a service that cannot read the family: err = %v\n%s", err, errOut)
		}
		assertOlderServiceRefusal(t, err, out+errOut, value, r.sm, bundleID)
	})
	t.Run("accounts add", func(t *testing.T) {
		r := providerKeyRigAt(t, providerKeyLocal, older)
		bundleID := bundleFixtureFor(t, r.sm, "aurora", nil)
		value := addToken()
		out, errOut, err := executeStdin(t, value+"\n", "accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter")
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("accounts add against a service that cannot read the family: err = %v\n%s", err, errOut)
		}
		assertOlderServiceRefusal(t, err, out+errOut, value, r.sm, bundleID)
		// The declaration is written before the store, and the refusal comes
		// with nothing typed: the value is the last question this command asks.
	})
	t.Run("secrets set", func(t *testing.T) {
		skew := newRunServiceSkew(t)
		sm := gcpfake.NewSecrets(t)
		f := newCloudFixture(t, "secret_manager: "+sm.URL+"/")
		skew.deployedService(credentialsImage(older), credentialsImage(older))
		providerKeyEndpoint(t, f.run, skew)
		appendConfig(t, providerKeyCloudProvider)
		bundleID := bundleFixtureFor(t, sm, "aurora", nil)
		value := "or-not-a-real-key-1234"
		out, errOut, err := executeStdin(t, value, "secrets", "set", "team-a-api-key", "--project-wide")
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("secrets set against a service that cannot read the family: err = %v\n%s", err, errOut)
		}
		assertOlderServiceRefusal(t, err, out+errOut, value, sm, bundleID)
	})
}

// assertOlderServiceRefusal is the one refusal the three writers share: the
// family's name, the release that reads it and the ONE command that moves the
// service, with the value nowhere on any stream and no version written.
func assertOlderServiceRefusal(t *testing.T, err error, shown, value string, sm *gcpfake.Secrets, bundleID string) {
	t.Helper()
	for _, want := range []string{credsvc.RepoCredentialPrefix, repoCredentialSince, "fugaro update", "Nothing was read from you and nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if strings.Contains(shown+err.Error(), value) {
		t.Errorf("the refusal echoed the value: %q", shown+err.Error())
	}
	if sm.Latest(bundleID) != nil {
		t.Errorf("the refused write left a version in %s: a refusal that judged the reader writes nothing", bundleID)
	}
}

// TestOnlyAProjectOwnerWritesAProviderKey is D3006 over the three writers: a
// provider key is the whole project's — one value, spent by every repository the
// owner bound it to — so the write is a PROJECT OWNER's, and the question is
// asked with ONE probe (testIamPermissions on the bundle's own container, F36),
// after the container is known to exist and before anything is read.
//
// The three ways credsOwnerOnly refuses stand in front of every writer too
// (providerKeyOwnerOnly, in the same order and with the same markers): a coding
// agent's session, a run's own process and the GODEBUG http2debug leak. The
// sentences are the file's own and say why the document is the one they guard.
func TestOnlyAProjectOwnerWritesAProviderKey(t *testing.T) {
	for _, tc := range []struct {
		what  string
		older func(*testing.T) (*gcpfake.Secrets, string)
		args  []string
	}{
		{
			what: "creds set",
			older: func(t *testing.T) (*gcpfake.Secrets, string) {
				r := providerKeyRig(t, providerKeyLocal)
				return r.sm, bundleFixtureFor(t, r.sm, "aurora", nil)
			},
			args: []string{"creds", "set", config.CredentialSecret("team-a")},
		},
		{
			what: "accounts add",
			older: func(t *testing.T) (*gcpfake.Secrets, string) {
				r := providerKeyRig(t, providerKeyLocal)
				return r.sm, bundleFixtureFor(t, r.sm, "aurora", nil)
			},
			args: []string{"accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter"},
		},
		{
			what: "secrets set",
			older: func(t *testing.T) (*gcpfake.Secrets, string) {
				sm := secretsFixture(t)
				appendConfig(t, providerKeyCloudProvider)
				return sm, bundleFixtureFor(t, sm, "aurora", nil)
			},
			args: []string{"secrets", "set", "team-a-api-key", "--project-wide"},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			t.Run("a caller who is not a project owner", func(t *testing.T) {
				sm, bundleID := tc.older(t)
				// Held: nothing. The caller may write versions of the container —
				// that is what every operator role grants — and not set its
				// policy, which is what tells an owner from an operator here.
				sm.SetHeldPermissions(bundleID)

				spy := &readerSpy{Reader: strings.NewReader(addToken() + "\n")}
				cmd := NewRootCmd()
				var out, errOut bytes.Buffer
				cmd.SetIn(spy)
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				cmd.SetArgs(tc.args)
				err := cmd.Execute()
				if err == nil || ExitCode(err) != ExitUserError {
					t.Fatalf("a caller who is not a project owner = %v, want a refusal:\n%s\n%s", err, out.String(), errOut.String())
				}
				for _, want := range []string{bundleOwnerPermission, "PROJECT OWNER", "project proj-1234", "Ask a project owner"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not say %q: %v", want, err)
					}
				}
				if spy.read {
					t.Error("the value was read from a caller who may not write the entry")
				}
				if sm.Latest(bundleID) != nil {
					t.Errorf("the refused write left a version in %s", bundleID)
				}
				if strings.Contains(out.String()+errOut.String()+err.Error(), addToken()) {
					t.Error("the refusal echoed the value")
				}
			})

			t.Run("a coding agent's session", func(t *testing.T) {
				sm, bundleID := tc.older(t)
				agentSession(t)

				spy := &readerSpy{Reader: strings.NewReader(addToken() + "\n")}
				cmd := NewRootCmd()
				var out, errOut bytes.Buffer
				cmd.SetIn(spy)
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				cmd.SetArgs(tc.args)
				err := cmd.Execute()
				if err == nil || ExitCode(err) != ExitUserError {
					t.Fatalf("a coding agent's session = %v, want a refusal:\n%s\n%s", err, out.String(), errOut.String())
				}
				for _, want := range []string{"(CLAUDECODE is set)", "own terminal"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not say %q: %v", want, err)
					}
				}
				if spy.read {
					t.Error("the value was read in a coding agent's session")
				}
				if sm.Latest(bundleID) != nil {
					t.Errorf("a coding agent's session wrote a version of %s", bundleID)
				}
			})

			t.Run("a run's own process", func(t *testing.T) {
				sm, bundleID := tc.older(t)
				t.Setenv("FUGARO_RUN", "aurora/run-1")

				spy := &readerSpy{Reader: strings.NewReader(addToken() + "\n")}
				cmd := NewRootCmd()
				var out, errOut bytes.Buffer
				cmd.SetIn(spy)
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				cmd.SetArgs(tc.args)
				err := cmd.Execute()
				if err == nil || ExitCode(err) != ExitUserError {
					t.Fatalf("a run's own process = %v, want a refusal:\n%s\n%s", err, out.String(), errOut.String())
				}
				for _, want := range []string{"FUGARO_RUN is set", "Fugaro run's"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not say %q: %v", want, err)
					}
				}
				if spy.read {
					t.Error("the value was read in a run's own process")
				}
				if sm.Latest(bundleID) != nil {
					t.Errorf("a run's own process wrote a version of %s", bundleID)
				}
			})
		})
	}
}

// TestRepoCredentialSinceHasAReleaseBullet is TestKeyCredentialSinceHasAReleaseBullet's
// sibling for the family this task adds: the constant names the release whose
// credentials service first reads C1's entries, and docs/release.md's
// Before-you-tag list is where a release verifies it against the tag actually
// cut — and where the direction that matters (never a raise) is pinned.
func TestRepoCredentialSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `repoCredentialSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no repoCredentialSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+repoCredentialSince+"\"`") {
		t.Errorf("docs/release.md's repoCredentialSince bullet does not carry the constant's release (%q)", repoCredentialSince)
	}
	for _, want := range []string{"the release that ships the family", "fugaro creds set", "fugaro accounts add", "fugaro secrets set", "must not raise it", "not tagged yet"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's repoCredentialSince bullet does not say %q", want)
		}
	}
}
