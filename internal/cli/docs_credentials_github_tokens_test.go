package cli

import (
	"regexp"
	"strings"
	"testing"
)

// The GitHub App's key through the credentials service (D145, C2.1-C2.4), as
// the two documents state it.
//
// The claim this test was written for is reconciliation.md's F34: docs/
// git-providers.md said "None of these variables reaches the agent", which is
// true of the long-lived variables and false of the credential the agent is
// given — on Bitbucket the repository access token itself, on GitHub the
// installation token the platform mints from them, which is the agent's own git
// credential (design §6.2, "What the agent gets"). A security claim a reader
// takes the wrong way is the one thing this pair of documents may not carry, so
// the stale sentence is banned and the exact one is pinned, together with the
// symbols an operator can read the exact one out of.
//
// Mutations (verified, run and restored): the stale sentence put back into
// docs/git-providers.md → FAIL; the "the runner reads no key while the agent can
// still reach a mounted one" clause deleted, with the held-secrets list still
// empty in internal/config/providers.go → FAIL, because the documents claim the
// job keeps the mount and the code is what decides it; `github.ServiceTokens`
// dropped from the code → FAIL (the document names a token source that is not
// there); a repository field added to credsvc.MintRequest → FAIL (the document
// says a mint call carries no repository field, and that is what makes "a run
// identity for X cannot mint for Y" true).
func TestDocsGitProvidersStateWhatReachesTheAgentExactly(t *testing.T) {
	providers := readDoc(t, "docs/git-providers.md")

	// The stale claim, in the shape it was written in.
	if strings.Contains(providers, "None of these variables reaches the agent") {
		t.Errorf("docs/git-providers.md still says \"None of these variables reaches the agent\": the long-lived variables do not reach it, and the run's own short-lived git credential does — on Bitbucket the access token itself, on GitHub the installation token minted from the pair (design §6.2, F34)")
	}

	// The exact one, and the four properties of the mint a reader of this
	// document has to be able to rely on.
	for _, want := range []string{
		"**None of these variables is in the agent's environment, and the long-lived credential behind each one never is.**",
		"never the App's private key and never another repository's token",
		"### The App key when an installation has a credentials service",
		"`github.ServiceTokens`",
		"`github.RunTokenPermissions`",
		"`fugaro creds set github-app-key`",
		"A mint call carries **no repository field**",
		"a run identity for repository X cannot mint a token for Y",
		"Bitbucket is untouched",
		"`config.ServiceHeldSecretNames`",
	} {
		if !strings.Contains(providers, want) {
			t.Errorf("docs/git-providers.md does not say %q: with the credentials service on, the App's key is the service's and the run's installation token is minted by it, and this is the document an operator reads to find that out", want)
		}
	}

	// The code the document names, read rather than copied: a rename in either
	// half has to be a failing test and not a document that cites nothing.
	apptoken := readDoc(t, "internal/gitprov/github/apptoken.go")
	for _, want := range []string{"type ServiceTokens struct", "func RunTokenPermissions("} {
		if !strings.Contains(apptoken, want) {
			t.Errorf("docs/git-providers.md names the service's mint as the runner's token source, and internal/gitprov/github/apptoken.go no longer has %q", want)
		}
	}
	mint := readDoc(t, "internal/credsvc/github.go")
	if !strings.Contains(mint, "MintPath = ") {
		t.Errorf("docs/git-providers.md names the credentials service's mint (`POST /v1/github/installation-token`), and internal/credsvc/github.go no longer has MintPath")
	}
	// The repository is not the caller's to name: the request type carries the
	// App's ID alone. This is the half of the claim the document states most
	// strongly, so it is pinned against the struct rather than its comment.
	req := cenMintRequestStruct(t, mint)
	if strings.Contains(req, "epository") {
		t.Errorf("internal/credsvc/github.go's MintRequest now has a repository field (%q): docs/git-providers.md tells an operator the repository is the service's to decide and never the caller's to name, and that is exactly this absent field", strings.TrimSpace(req))
	}
	if !strings.Contains(req, "AppID") {
		t.Errorf("internal/credsvc/github.go's MintRequest no longer carries the App's ID, which the document says is the one thing a mint call supplies")
	}

	// And the honest half: while the held-secrets list is empty the job still
	// mounts the key, and both documents have to say so.
	if heldSecretsListNamesTheAppKey(t) {
		if strings.Contains(providers, "still mounts `FUGARO_GITHUB_APP_PRIVATE_KEY`") {
			t.Errorf("internal/config/providers.go's ServiceHeldSecretNames now names the App key — the job has given the key up — and docs/git-providers.md still says an opted-in installation mounts it: the two moved apart, and the document is the one that is wrong now")
		}
	} else if !strings.Contains(providers, "still mounts `FUGARO_GITHUB_APP_PRIVATE_KEY`") {
		t.Errorf("internal/config/providers.go's ServiceHeldSecretNames is still empty — the job keeps its accessor and its container keeps the mount — and docs/git-providers.md does not say so: the documents must not claim the key is out of the job while the code says otherwise")
	}
}

// TestSecurityMdStatesTheAppKeyIsTheServicesWhenTheServiceIsOn is the headline
// security claim of this task's documents: once an installation opts into the
// credentials service, the App's key is the service's — the run's installation
// token is minted by the service for the run's own repository and permission
// set, and the job holds no key to sign with — and the job's own accessor and
// mount leave in the release that also stops the runner reading them, which has
// NOT landed on this tree. SECURITY.md has to state both halves exactly: a
// document that claimed the key was out of the job today would be false, and one
// that left the App's key as "the agent can read it from /proc, always" would
// hide what the opt-in changes.
//
// Mutations (verified, run and restored): the "that release has not landed on
// this tree" caveat deleted from the new section → FAIL, because
// config.ServiceHeldSecretNames is still empty in internal/config/providers.go
// and the document would claim a drop the code does not make; the caveat put
// back the other way (the list filled with GitHubAppKeySecret while the section
// still says the job mounts the key) → FAIL in the other direction; the
// "no code path in the job mints a token or reads the App key once the service
// is on" clause deleted → FAIL, because that is the property the opt-in gives
// today and the sentence the residual in "Known limits" points at.
func TestSecurityMdStatesTheAppKeyIsTheServicesWhenTheServiceIsOn(t *testing.T) {
	sec := readDoc(t, "SECURITY.md")

	for _, want := range []string{
		"### The GitHub App's key, when the credentials service holds it",
		"**Signing is the service's, not the job's.**",
		"`fugaro creds set github-app-key`",
		"A run's installation token is **minted by the service**",
		"A mint call carries the App's ID and nothing else — there is no repository field to fill in.",
		"**calling job's account is bound to**",
		"(see \"The GitHub App's key, when the credentials service holds it\" above)",
		"no code path in the job mints a token or reads the App key once the service is on",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("SECURITY.md does not say %q: with the credentials service on the App's key is the service's and the run's token is minted by it (D145), and this document is where a reader is told what that reaches", want)
		}
	}

	// The permission set the document names is the code's, so a widening of
	// either the mint or the in-job mint is a failing test here.
	apptoken := readDoc(t, "internal/gitprov/github/apptoken.go")
	if !strings.Contains(apptoken, "func RunTokenPermissions(") || !strings.Contains(sec, "`github.RunTokenPermissions`") {
		t.Errorf("SECURITY.md tells a reader the minted token's reach is unchanged because it signs with `github.RunTokenPermissions`, and internal/gitprov/github/apptoken.go no longer spells that set out (or the document no longer cites it)")
	}

	// The caveat, held to the code: while the held-secrets list is empty the
	// job still mounts the key, and the document has to say so; the day the
	// list names it, the same sentence is the one that has to go.
	const caveat = "that release has not landed on this tree"
	if heldSecretsListNamesTheAppKey(t) {
		if strings.Contains(sec, caveat) || strings.Contains(sec, "still mounts `FUGARO_GITHUB_APP_PRIVATE_KEY`") {
			t.Errorf("internal/config/providers.go's ServiceHeldSecretNames names the App key — the job has given it up — and SECURITY.md still says the job keeps its accessor and its mount: the release landed and this document did not")
		}
	} else {
		if !strings.Contains(sec, caveat) || !strings.Contains(sec, "still mounts `FUGARO_GITHUB_APP_PRIVATE_KEY`") {
			t.Errorf("internal/config/providers.go's ServiceHeldSecretNames is still empty — the job keeps its accessor and its mount — and SECURITY.md does not say so plainly: this is the one claim in the document a reader would take as done today")
		}
	}
}

// cenMintRequestStruct reads credsvc.MintRequest's own struct body out of// internal/credsvc/github.go, so that the fields the document claims are absent
// are read from the type rather than from the prose beside it.
func cenMintRequestStruct(t *testing.T, src string) string {
	t.Helper()
	_, rest, ok := strings.Cut(src, "type MintRequest struct {")
	if !ok {
		t.Fatalf("internal/credsvc/github.go has no MintRequest struct: this test's marker has moved")
	}
	body, _, ok := strings.Cut(rest, "\n}")
	if !ok {
		t.Fatalf("internal/credsvc/github.go's MintRequest struct does not close: this test's marker has moved")
	}
	return body
}

// heldSecretsListNamesTheAppKey reports whether internal/config/providers.go's
// ServiceHeldSecretNames names the GitHub App's key — the one name C2.2's
// release adds to it, which is what turns the job's accessor and mount off. The
// slice is read from its source line, so the documents and the code move
// together.
func heldSecretsListNamesTheAppKey(t *testing.T) bool {
	t.Helper()
	src := readDoc(t, "internal/config/providers.go")
	m := regexp.MustCompile(`(?m)^var ServiceHeldSecretNames = \[\]string\{(.*)\}$`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("internal/config/providers.go has no `var ServiceHeldSecretNames = []string{...}` line: this test's marker has moved")
	}
	return strings.Contains(m[1], "GitHubAppKeySecret")
}
