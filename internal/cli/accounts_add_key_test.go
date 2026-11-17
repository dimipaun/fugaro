//go:build darwin || linux

package cli

import (
	"encoding/json"
	"maps"
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
func TestAKeyCredentialsSecretStoresWhereItsDeclarationBoundsIt(t *testing.T) {
	// The declared state: a key type, and an allow_data_to that names a
	// repository.
	sm := secretsFixture(t)
	appendConfig(t, "credentials:\n  - { name: team-a, type: openrouter }\nclaude_accounts:\n  team-a: { allow_data_to: [acme/app] }\n")
	if _, _, err := executeStdin(t, addToken(), "secrets", "set", config.CredentialSecret("team-a"), "--project-wide"); err != nil {
		t.Fatalf("storing a declared key credential's own secret was refused: %v", err)
	}
	if got := string(sm.Latest(credentialSecretID("team-a"))); got != addToken() {
		t.Errorf("%s holds %q, want the value the command read", credentialSecretID("team-a"), got)
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

// TestAccountsAddOpenRouterKey is MC22's red step: the type is declared, the
// value read from stdin is stored under the credential's own derived secret
// name, and the account's Claude token container is left without a version —
// the credential is a key credential and not another name for the account.
func TestAccountsAddOpenRouterKey(t *testing.T) {
	r := newAddRig(t)
	writes := r.secretWrites(t)

	out, errOut := r.addTyped(t, "team-a", "--repos", "owner/repo-a", "--type", "openrouter")

	id := credentialSecretID("team-a")
	if got := string(r.sm.Latest(id)); got != addToken() {
		t.Errorf("%s holds %q, want the value the command read: a key credential's value lands under the credential's own derived secret name", id, got)
	}
	if got := string(r.sm.Latest(accountSecretID("team-a"))); got != "" {
		t.Errorf("the account's Claude token container holds %q: a key credential is not the account's token, and the job mounts that name as CLAUDE_CODE_OAUTH_TOKEN", got)
	}
	if want := []string{id}; !slices.Equal(writes.created, want) || !slices.Equal(writes.added, want) {
		t.Errorf("the command wrote %v (created) and %v (versions), want only %v: the credential's value is stored project-wide and nowhere else", writes.created, writes.added, want)
	}
	lc := r.addLocal(t)
	if want := []config.Credential{{Name: "team-a", Type: config.CredentialTypeOpenRouter}}; !slices.Equal(lc.Credentials, want) {
		t.Errorf("the credentials block is %v, want %v: the name is declared a credential a pool may list", lc.Credentials, want)
	}
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the repository the value may reach: the account entry is what bounds the credential's reach", got)
	}
	// No profile lists the name, and no job of the account's own exists for it:
	// profiles.<p>.claude_accounts is the list that serves a Claude account
	// TOKEN, which is what deploys the job mounting it as CLAUDE_CODE_OAUTH_TOKEN
	// — a container this credential's value is never stored in. Listing the name
	// there deployed a job reading an empty token, published the name into the
	// node's accounts block as a run's account, and handed doctor a fix line no
	// run of it could clear.
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
				t.Errorf("job %s mounts %s, which nothing writes for a key credential: its value is %s", jobName, mounted, config.CredentialSecret("team-a"))
			}
		}
	}
	if !strings.Contains(out+errOut, "no job: a key credential rides no job of its own") {
		t.Errorf("the command does not say that no job was deployed for it:\n%s\n%s", out, errOut)
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
	if read := readFileString(t, r.cfg); strings.Contains(read, addToken()) {
		t.Errorf("the local config carries the value:\n%s", read)
	}
	if strings.Contains(shown, addToken()) {
		t.Errorf("the command printed the value:\n%s", shown)
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
	// The retype loosens the shared node (craft_credentials team-a), so this publish confirms by the project name typed at a terminal.
	r.addTyped(t, "team-c", "--repos", "owner/repo-a")

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

	r.addTyped(t, "team-key", "--repos", "owner/repo-a", "--type", "anthropic")

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
	r := newAddRig(t)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter")
	pasteAtThePrompt(t, tty, config.CredentialSecret("team-a"), addToken())
	// The publish after the store loosens the shared node and waits at this
	// terminal for the project's name; without it the command never returns.
	waitTerminal(t, tty, "Type "+initProjectName+" to confirm")
	if _, err := tty.master.WriteString(initProjectName + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("accounts add --type openrouter: %v\nterminal:\n%s", err, tty.displayed())
	}

	if strings.Contains(tty.displayed(), addToken()) {
		t.Errorf("the terminal shows the pasted value, so the hidden prompt echoed it:\n%s", tty.displayed())
	}
	if got := string(r.sm.Latest(credentialSecretID("team-a"))); got != addToken() {
		t.Errorf("the credential's container holds %q, want the value pasted at the hidden prompt", got)
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
// belongs. So the container is the credential's, whatever this run's flag said,
// and the file it does not retype is the owner's.
func TestARerunWithoutTypeWritesOnlyTheCredentialsOwnSecret(t *testing.T) {
	r := newAddRigWith(t, accountsAddKeyCredentialLocal)
	writes := r.secretWrites(t)

	out, errOut := r.addTyped(t, "team-a", "--repos", "owner/repo-a")

	id := credentialSecretID("team-a")
	if got := string(r.sm.Latest(id)); got != addToken() {
		t.Errorf("%s holds %q, want the value the command read: the re-run stores the key where the credential's type says its key lives", id, got)
	}
	if got := string(r.sm.Latest(accountSecretID("team-a"))); got != "" {
		t.Errorf("the account's Claude token container holds %q: a re-run without --type stored a KEY under the name the job mounts as CLAUDE_CODE_OAUTH_TOKEN", got)
	}
	if want := []string{id}; !slices.Equal(writes.created, want) || !slices.Equal(writes.added, want) {
		t.Errorf("the re-run wrote %v (created) and %v (versions), want only %v", writes.created, writes.added, want)
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
func TestAccountsRmTakesAKeyCredentialsOwnContainer(t *testing.T) {
	r := newAddRigWith(t, accountsAddKeyCredentialLocal)
	r.addTyped(t, "team-a", "--repos", "owner/repo-a")
	id := credentialSecretID("team-a")
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

// TestKeyCredentialReachesItsServingHalf is MC23's red step (D1670): the value
// of a key credential reaches the HALF of a run that serves its type, and never
// the other one. config.CredentialServingPath answers which half a type is on,
// and the two halves are two documents the value has to be in: the bundle the
// credentials service reads (one model entry per credential, at the tier the
// service offers it by) and the job's own mounts, which internal/infra builds
// from the installation's config.
//
// After MC22 alone both halves are empty: the declaration exists and the value
// is stored under the credential's own derived secret name, and nothing reads
// that container on either path.
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

	t.Run("a runner-path credential is in the job's mounts and not in the bundle", func(t *testing.T) {
		f := credsFixtureFor(t)
		appendConfig(t, "credentials:\n  - { name: team-or, type: openrouter }\nclaude_accounts:\n  team-or: { allow_data_to: [acme/app] }\n")
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		before := f.value(t)
		value := credsValue(t, "keycred")

		// The bundle is refused: this type's half is the job's own mounts, and
		// an entry here would be a copy no call of it ever reads.
		out, errOut, err := executeStdin(t, value+"\n", "creds", "set", config.CredentialSecret("team-or"))
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("the bundle took a runner-path credential's key: err = %v", err)
		}
		if !strings.Contains(err.Error(), "the runner path serves that type") {
			t.Errorf("the refusal does not name the half that serves it: %v", err)
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
			t.Errorf("the job's mounts are %v, want %v among them: the credential's own container is what the job that spends it mounts", got, want)
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
	// addTyped and not add: declaring an anthropic credential puts
	// craft_credentials on the shared node, and a publish that loosens it is the
	// project's own name typed at a terminal (D846), which a rig whose command
	// carries no terminal is refused at the policy step (it is the one
	// TestAccountsAddAnthropicKey, above, stands at).
	out, errOut := r.addTyped(t, "team-a", "--repos", "owner/repo-a", "--type", "anthropic")
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
