package cli

import (
	"strings"
	"testing"
)

// oauthCheckoutYAML is a checkout whose runs hold a Claude OAuth credential,
// so the token the plan mounts is the one the account axis names.
const oauthCheckoutYAML = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n" +
	"agent: { auth: oauth }\n" +
	"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// The owner's report of 2026-11-24: a repository whose runs are on a named
// account, that account's token stored project-wide once, and the repository's
// own container of it left empty. The plan must ask for nothing: the named
// token is covered by the project copy, and the unsuffixed default account's
// token -- a name no run of this configuration holds -- is not a step at all,
// so the repository is not stranded on it and its first build is still offered.
//
// Before this, the bare claude-oauth-token was demanded for such an
// installation (an oauth repository's account set always carries the
// unsuffixed default, and that name is deliberately outside D2's sweep, so no
// project-wide copy can replace it), and the demand held the whole repository
// back: no build, no job, and the owner told to run claude setup-token.
func TestInitRepoNamedDefaultAccountNeedsNoDefaultToken(t *testing.T) {
	r := newProjectKeyRigWith(t, `["*"]`, oauthCheckoutYAML)
	r.appendConfig(t, "claude_accounts:\n  belong: { allow_data_to: [acme/sandbox] }\ndefault_claude_account: belong\n")
	r.seedRepoSecret(t, "bitbucket-token", []byte("tok"))
	r.seedProjectKey(t, "claude-oauth-token-belong")
	// The repository's own container exists with NO version, as Terraform
	// leaves it: the project copy is what covers the name.
	r.seedRepoSecret(t, "claude-oauth-token-belong", nil)
	fb := useFakeBuilder(t)
	e := rigEngine(t, r, &initOptions{})
	out := atTerminal(t, e, strings.Repeat(initProjectName+"\n", 3))
	if err := e.r.repoEngine(t.Context(), ".", "", false); err != nil {
		t.Fatalf("the account's project-wide token left the run unfinished: %v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "app" {
		t.Fatalf("specs = %+v, want the app workflow's first build: a token no run holds must not strand it", fb.specs)
	}
	assertNoDefaultTokenDemand(t, out.String(), e.r.res.Missing)
}

// The other direction, so the first test cannot pass by a demand that was
// deleted outright: a configuration that names no default account runs on the
// unsuffixed one, so its token is a real step, with the mint the owner is sent
// to.
func TestInitRepoDefaultAccountStillNeedsItsToken(t *testing.T) {
	r := newProjectKeyRigWith(t, `["*"]`, oauthCheckoutYAML)
	r.appendConfig(t, "claude_accounts:\n  belong: { allow_data_to: [acme/sandbox] }\n")
	r.seedRepoSecret(t, "bitbucket-token", []byte("tok"))
	r.seedProjectKey(t, "claude-oauth-token-belong")
	r.seedRepoSecret(t, "claude-oauth-token-belong", nil)
	e := rigEngine(t, r, &initOptions{})
	out := atTerminal(t, e, initProjectName+"\n")
	if err := e.r.repoEngine(t.Context(), ".", "", false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := missingLine(e.r.res.Missing, "claude setup-token"); got == "" {
		t.Fatalf("missing = %q, want the default account's token still a step when no default account is named", e.r.res.Missing)
	}
	if !strings.Contains(out.String(), "Still missing for acme/sandbox:") {
		t.Errorf("no missing block:\n%s", out.String())
	}
}

// assertNoDefaultTokenDemand is the covered case's whole assertion: nothing in
// what the run prints or carries into the result (--json reads the same list)
// asks for the unsuffixed default account's token, or for a repository copy of
// the named account's.
func assertNoDefaultTokenDemand(t *testing.T, out string, missing []string) {
	t.Helper()
	for _, m := range append(append([]string{}, missing...), strings.Split(out, "\n")...) {
		if defaultTokenStep(m) {
			t.Errorf("the run still asks for the default account's token: %q", m)
		}
		if strings.Contains(m, "accounts add belong") {
			t.Errorf("the run asks for a copy of the account the project already serves: %q", m)
		}
	}
}

// defaultTokenStep is whether one line asks for the unsuffixed default
// account's token: the mint, or a store whose name is exactly
// claude-oauth-token — never claude-oauth-token-<name>, which is a named
// account's own secret and a different step.
func defaultTokenStep(s string) bool {
	if strings.Contains(s, "setup-token") {
		return true
	}
	if strings.Contains(s, "secrets set claude-oauth-token ") {
		return true
	}
	return strings.HasSuffix(strings.TrimSpace(s), "secrets set claude-oauth-token")
}
