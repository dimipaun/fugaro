//go:build darwin || linux

package cli

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

	out, errOut := r.add(t, "team-a", "--repos", "owner/repo-a", "--type", "openrouter")

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
	r := newAddRig(t)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--type", "openrouter")
	pasteAtThePrompt(t, tty, config.CredentialSecret("team-a"), addToken())
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

	out, errOut := r.add(t, "team-a", "--repos", "owner/repo-a")

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
