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
// docs/git-providers.md → FAIL; the second stale sentence ("the long-lived
// credential behind each one never is") put back — the claim this test was
// extended for, false on Bitbucket → FAIL, on the ban and on the pin both; the
// Bitbucket bullet softened back to a short-lived credential of its own → FAIL;
// the "the runner still reads the key it is given
// and the agent can still reach that same mounted copy" clause deleted, with the
// held-secrets list still empty in internal/config/providers.go → FAIL, because
// the documents claim the job keeps the mount and the code is what decides it;
// `github.ServiceTokens` dropped from the code → FAIL (the document names a
// token source that is not there); a repository field added to
// credsvc.MintRequest → FAIL (the document says a mint call carries no
// repository field, and that is what makes "a run identity for X cannot mint for
// Y" true); the retired "Nothing on this tree puts that variable in a job's
// environment" paragraph put back → FAIL, on the ban — the document would claim
// the keyless path is in the tree and not in effect while
// internal/cli/run.go's credentialsServiceLaunchEnv sets the variable on a
// repository job; the "The launch is what puts it there" paragraph's
// "a repository's own job and on no other" clause changed to include shared
// project jobs → FAIL, because the service refuses a shared job's account
// (Global Constraint 6) and the variable is what opens the provider keyless; the
// "An installation that has not opted in gets no variable at all" sentence
// deleted → FAIL; the ErrMintNoAppKey sentence deleted → FAIL.
func TestDocsGitProvidersStateWhatReachesTheAgentExactly(t *testing.T) {
	providers := readDoc(t, "docs/git-providers.md")

	// The stale claim, in the shape it was written in.
	if strings.Contains(providers, "None of these variables reaches the agent") {
		t.Errorf("docs/git-providers.md still says \"None of these variables reaches the agent\": the long-lived variables do not reach it, and the run's own short-lived git credential does — on Bitbucket the access token itself, on GitHub the installation token minted from the pair (design §6.2, F34)")
	}
	// And the second stale claim, which replaced the first and was still false:
	// it said the long-lived credential behind each variable never reaches the
	// agent, which contradicts the sentence two lines later — on Bitbucket the
	// run's git credential IS this repository's access token, and the job's
	// environment holds the same value. Bitbucket is what C2.2 does not touch,
	// so no release of the credentials service makes that sentence true.
	if strings.Contains(providers, "the long-lived credential behind each one never is") {
		t.Errorf("docs/git-providers.md says \"the long-lived credential behind each one never is\" in the agent's environment: false on Bitbucket, where the run's git credential is this repository's access token (`FUGARO_GIT_TOKEN`), the same value the job's environment holds as `FUGARO_BITBUCKET_TOKEN` (`internal/runner/runner.go`'s `refreshGitAuth`, `internal/gitops/credentials.go`). State it per provider, as a known limit")
	}
	// And the third stale claim, retired by this release: the document used to
	// say the keyless path was "in the tree, not in effect" because nothing set
	// the variable. internal/cli/run.go's credentialsServiceLaunchEnv sets it on
	// a repository job's execution now, so every one of those sentences is a
	// reader being told a run behaves one way while it behaves another.
	for _, stale := range []string{
		"Nothing on this tree puts that variable in a job's environment",
		"Nothing on this tree names it, so this is the release's behaviour and not today's",
		"an installation's runner still reads the App key from its environment and mints the installation token in the job",
		"the release that wires it end to end — the one that sets the variable on a job — is the one after this",
	} {
		if strings.Contains(providers, stale) {
			t.Errorf("docs/git-providers.md still says %q: the launch of an opted-in installation's repository job sets `FUGARO_CREDENTIALS_SERVICE_URL` (`internal/cli/run.go`'s `credentialsServiceLaunchEnv`), so the keyless path is in effect and the runner mints at the service rather than in the job — this is the release that wires it, not the one after it", stale)
		}
	}

	// The exact one, and the four properties of the mint a reader of this
	// document has to be able to rely on.
	for _, want := range []string{
		// The credential the agent gets, per provider: the App's key is not in the
		// agent's environment on GitHub, and on Bitbucket the long-lived
		// credential's own value is, by design.
		"**None of these variables is in the agent's environment — but what stands behind each one is two different stories, and on Bitbucket the agent does get the long-lived credential's own value.**",
		"**On Bitbucket it IS this repository's access token**",
		"handed to the agent as `FUGARO_GIT_TOKEN` by the credential helper below",
		"This is a **known limit** of the Bitbucket path",
		"never the App's private key and never another repository's token",
		"### The App key when an installation has a credentials service",
		"`github.ServiceTokens`",
		"`github.RunTokenPermissions`",
		"`fugaro creds set github-app-key`",
		"A mint call carries **no repository field**",
		"a run identity for repository X cannot mint a token for Y",
		"Bitbucket is untouched",
		"`config.ServiceHeldSecretNames`",
		// The wiring, which is what this document had to be rewritten for: the
		// launch puts the variable on a repository job's execution, and on no
		// other, and an installation that has not opted in is unchanged. The
		// retired claim ("Nothing on this tree puts that variable in a job's
		// environment") is banned below.
		"**The launch is what puts it there**",
		"`internal/cli/run.go`'s `credentialsServiceLaunchEnv`",
		"a repository's own job and on no other",
		"An installation that has not opted in gets no variable at all",
		// The bundle has to hold the key, or the mint is a named refusal.
		"`ErrMintNoAppKey`",
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
// set, and the job reads no key to sign with — while the job's own accessor and
// mount stay until the release that flips config.ServiceHeldSecretNames, which
// has NOT landed on this tree. SECURITY.md has to state both halves exactly: a
// document that claimed the key was OUT OF the job today would be false (it is
// still mounted and still granted), and one that left the App's key as "the
// agent can read it from /proc, always" would hide what the opt-in changes.
//
// This test's second half moved with the wiring (0.10.1). It used to pin the
// sentence "nothing on this tree sets that variable, so today the key that
// signs an opted-in run's tokens is still the job's own" — true while no launch
// set `FUGARO_CREDENTIALS_SERVICE_URL`, and false the moment
// internal/cli/run.go's credentialsServiceLaunchEnv put it on a repository
// job's execution. What the document states instead is the two halves that
// remain: an opted-in installation mints at the service, and an installation
// that has NOT opted in still mints in-job.
//
// Mutations (verified, run and restored): the "that release has not landed on
// this tree" caveat deleted from the new section → FAIL, because
// config.ServiceHeldSecretNames is still empty in internal/config/providers.go
// and the document would claim a drop the code does not make; the caveat put
// back the other way (the list filled with GitHubAppKeySecret while the section
// still says the job mounts the key) → FAIL in the other direction; the
// "no code path in the job mints a token or reads the App key **when the job's
// environment names the service**" clause deleted → FAIL, because that is the
// property the keyless path gives and the sentence the residual in "Known
// limits" points at; the sentence about the installation that has not opted in
// deleted → FAIL, because the document would then read as if every installation
// minted at the service; the retired "nothing on this tree sets that variable"
// sentence put back → FAIL.
func TestSecurityMdStatesTheAppKeyIsTheServicesWhenTheServiceIsOn(t *testing.T) {
	sec := readDoc(t, "SECURITY.md")

	for _, want := range []string{
		"### The GitHub App's key, when the credentials service holds it",
		"**Signing is the service's, not the job's, from the release that tells a job where the service is.**",
		"`fugaro creds set github-app-key`",
		// The release that sets the variable is THIS one, and the document
		// says so in the terms the code does: the launch puts it on a
		// repository's own job, and on no other.
		"on an opted-in installation a run's installation token is minted by the service and the run's runner opens its git provider with no key at all",
		"a bundle that holds none is a named refusal rather than a fall back to the job's copy (D143)",
		"**A shared project job is deliberately not told**",
		"A mint call carries the App's ID and nothing else — there is no repository field to fill in.",
		"**calling job's account is bound to**",
		"(see \"The GitHub App's key, when the credentials service holds it\" above)",
		// And the reference back, in the direction it actually points: this
		// section sits under "### Trust model and known limits" *above* the
		// "### Known limits" bullet it hands the residual to, so that bullet is
		// below it and the sentence has to say so — it said "above" until the
		// review of this round, which is a reader's map to the wrong place.
		"the residual in \"What one run's agent can reach\" below",
		"no code path in the job mints a token or reads the App key **when the job's environment names the service**",
		// What is true of an installation that has NOT opted in, which is the
		// half the retired sentence was reaching for: no variable is set
		// there, and its jobs mint in-job exactly as before.
		"On an installation that has not opted in, no variable is set and the job reads the App key and mints in-job exactly as it did before the service existed.",
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
	// list names it, the same sentences are the ones that have to go. The
	// caveat moved with the wiring: the document used to say the release that
	// drops the mount "has not landed on this tree", and now that the launcher
	// sets the variable it says the mount stays while the runner no longer
	// reads the key, and names the live check the flip waits on.
	const caveat = "the flip is held until the owner's live Check 96 has proven the deployed service mints"
	if heldSecretsListNamesTheAppKey(t) {
		if strings.Contains(sec, caveat) || strings.Contains(sec, "still mounts `FUGARO_GITHUB_APP_PRIVATE_KEY`") {
			t.Errorf("internal/config/providers.go's ServiceHeldSecretNames names the App key — the job has given it up — and SECURITY.md still says the job keeps its accessor and its mount: the release landed and this document did not")
		}
	} else {
		if !strings.Contains(sec, caveat) || !strings.Contains(sec, "still mounts `FUGARO_GITHUB_APP_PRIVATE_KEY`") {
			t.Errorf("internal/config/providers.go's ServiceHeldSecretNames is still empty — the job keeps its accessor and its mount — and SECURITY.md does not say so plainly: this is the one claim in the document a reader would take as done today")
		}
	}
	// And the retired half of that claim, which said no job ever carries the
	// variable: the launcher sets it, so the sentence is banned rather than
	// merely unpinned.
	for _, stale := range []string{
		"nothing on this tree sets that variable",
		"nothing on this tree sets `FUGARO_CREDENTIALS_SERVICE_URL`",
		"no job is ever given its address",
	} {
		if strings.Contains(sec, stale) {
			t.Errorf("SECURITY.md still says %q: internal/cli/run.go's credentialsServiceLaunchEnv sets `FUGARO_CREDENTIALS_SERVICE_URL` on an opted-in installation's repository job, so the keyless path is in effect and this is the release that sets it", stale)
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
