package credsvc

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// slugFor is a repository's slug as a run's verified identity carries it
// (creds.Identity.Slug), which is the half of an entry's name the service
// derives and never the half a call names (R2). It is computed here rather
// than written down, so the test holds the naming rule to the slug rule the
// tree really applies and not to a literal this file could get wrong.
func slugFor(t *testing.T, provider, repo string) string {
	t.Helper()
	slug, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatalf("task.Slug(%q, %q) = %v; the test needs a slug it can build an entry name from", provider, repo, err)
	}
	if !task.ValidSlug(slug) {
		t.Fatalf("task.Slug(%q, %q) = %q, which is not a slug", provider, repo, slug)
	}
	return slug
}

// TestStubAccessorHoldsNothing pins the accessor's refusing direction: a
// bundle that holds no member of the named family answers "no such
// credential", which the caller turns into ErrorProviderNotServed.
//
// It is named for the stub because that is what answers it while C1 is the
// only thing that has landed, but what it asserts is the accessor's contract
// and not the stub's body: the read that must refuse is the one for a
// repository and a credential the bundle holds nothing for, and that is the
// same read once KS1 teaches the bundle to hold a named entry (which then
// answers for the entries it holds and refuses for the rest) and once I1
// deletes the stub. So it stays true across both, which is why it lives in
// this file and not in one I1 deletes.
func TestStubAccessorHoldsNothing(t *testing.T) {
	if ReadProviderKey == nil {
		t.Fatal("ReadProviderKey is nil; the contract declares the accessor the service reads an entry through, and a stub assigns it while nothing else has")
	}
	// A bundle of today's shape — the project's own Anthropic key and no
	// member of the named family — which is the state of every installation
	// until the copy has written one (KS12).
	b, err := ParseBundle([]byte(config.AnthropicSecret + ": " + fakeCred(t, "project") + "\n"))
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	one := slugFor(t, "github", "acme/one")
	two := slugFor(t, "bitbucket", "acme/two")
	for _, tc := range []struct{ slug, credential string }{
		{one, "openrouter"},
		{one, config.AnthropicKeyAccount},
		{two, "deepseek"},
	} {
		entry, ok := ReadProviderKey(b, tc.slug, tc.credential)
		if ok {
			t.Errorf("ReadProviderKey(%s, %s) answered an entry; a bundle holding no member of the family holds no credential to serve (R2, D3008)", tc.slug, tc.credential)
		}
		if entry.Slug != "" || entry.Credential != "" || entry.BaseURL != "" || entry.Auth != "" || !entry.Source.IsZero() || entry.Value() != "" {
			t.Errorf("ReadProviderKey(%s, %s) refused with a partly filled entry; a refusal answers the zero entry", tc.slug, tc.credential)
		}
	}
}

// TestAnEntryNameCarriesItsRepository is R2's naming rule (U2's answer): the
// name of one repository's own provider credential carries the repository and
// the credential, and it is readable back into exactly those two halves.
//
// The first assertion is the red step the plan states: two repositories whose
// runs use one provider name must not share an entry name. One name for both
// is one key served to the other repository's run — the thing HG1's reach rule
// exists to prevent — and it is also what D3008 forbids: a key several
// repositories share is entered once per repository, so the entry names them
// apart.
func TestAnEntryNameCarriesItsRepository(t *testing.T) {
	one := slugFor(t, "github", "acme/one")
	two := slugFor(t, "github", "acme/two")

	shared, err := RepoCredentialKey(one, "openrouter")
	if err != nil {
		t.Fatalf("RepoCredentialKey(%s, openrouter): %v", one, err)
	}
	other, err := RepoCredentialKey(two, "openrouter")
	if err != nil {
		t.Fatalf("RepoCredentialKey(%s, openrouter): %v", two, err)
	}
	if shared == other {
		t.Fatalf("two repositories with one provider name answer the one entry name %s; the entry would serve a key held for another repository (R2, D3008)", shared)
	}
	if !strings.HasPrefix(shared, RepoCredentialPrefix) {
		t.Errorf("RepoCredentialKey(%s, openrouter) = %s, which is not of the family %s", one, shared, RepoCredentialPrefix)
	}
	if !strings.Contains(shared, one) || !strings.Contains(other, two) {
		t.Errorf("the entry name does not carry its repository: %s for %s, %s for %s", shared, one, other, two)
	}

	// What is written is what is read: every name this rule builds splits back
	// into the two halves it was built from, and no two pairs share a name.
	// The pair that matters most is the provider NAMED after the project's own
	// container beside the project's own key (D3008, D3015): a provider name
	// and the project key's spelling are the two credentials one repository
	// can hold of this family, and one name for both would serve a provider's
	// key where the repository's own Anthropic key was meant.
	cases := []struct{ slug, credential string }{
		{one, "openrouter"},
		{one, "deepseek"},
		{one, "anthropic-api-key"},
		{one, config.AnthropicKeyAccount},
		{two, "openrouter"},
		{two, config.AnthropicKeyAccount},
	}
	seen := map[string]string{}
	for _, c := range cases {
		name, err := RepoCredentialKey(c.slug, c.credential)
		if err != nil {
			t.Errorf("RepoCredentialKey(%s, %s): %v; this pair is a repository and a credential of the family", c.slug, c.credential, err)
			continue
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("the entry name %s is built for both %s and %s", name, prev, c.slug+"/"+c.credential)
		}
		seen[name] = c.slug + "/" + c.credential
		slug, credential, ok := RepoCredentialByKey(name)
		if !ok {
			t.Errorf("RepoCredentialByKey(%s) answered no member of the family; RepoCredentialKey just built it", name)
			continue
		}
		if slug != c.slug || credential != c.credential {
			t.Errorf("RepoCredentialByKey(%s) = %s, %s; want %s, %s", name, slug, credential, c.slug, c.credential)
		}
	}

	// And what the rule refuses: a half that is not one of the two shapes it
	// builds from is refused rather than named, because a name built out of one
	// is a name the loader would read as another repository's or another
	// family's credential. The refusal never repeats the half it was handed —
	// a slug is a repository's name and a provider's name is a config name, and
	// this package's refusals carry neither (the credentials-service rule).
	for _, c := range []struct{ slug, credential, why string }{
		{"", "openrouter", "no repository at all"},
		{one, "", "no credential at all"},
		{"acme/one", "openrouter", "a repository path and not a slug"},
		{"acme--one-0123456789abcdef", "openrouter", "a slug carrying the family's own separator, which the read-back could not place"},
		{one, "OpenRouter", "a credential name the provider rule does not admit"},
		{one, "openrouter-production", "a provider name longer than the rule admits"},
		{one, "1openrouter", "a provider name the rule does not admit"},
		{one, "open_router", "a provider name the rule does not admit"},
	} {
		name, err := RepoCredentialKey(c.slug, c.credential)
		if err == nil {
			t.Errorf("RepoCredentialKey(%s, %s) = %s; %s, and the name is refused rather than built", c.slug, c.credential, name, c.why)
			continue
		}
		if c.slug != "" && strings.Contains(err.Error(), c.slug) {
			t.Errorf("the refusal repeats the repository it refused; a refusal names the field and never its content")
		}
		if c.credential != "" && strings.Contains(err.Error(), c.credential) {
			t.Errorf("the refusal repeats the credential name it refused; a refusal names the field and never its content")
		}
	}
}

// TestAProviderEntryIsNeverAnAnthropicKeyName is F33 and D3008 read where they
// bite: a name of the named family can never be the name of a service-served
// key credential, `credential-<name>-key`.
//
// That is what makes the family safe to add to a document a running service
// already reads. The deployed service resolves `credential-<name>-key` as a
// tier B Anthropic key (MC23), so an entry a copy wrote under such a name would
// change what that name means on a service nobody re-deployed — the older
// service would serve a provider's key as the project's own. The names are
// held apart by construction rather than by a check: this family begins
// `repo-`, the key credential's family begins `credential-`, and a prefix rule
// is what both KeyCredentialSecret and CredentialBySecret read.
func TestAProviderEntryIsNeverAnAnthropicKeyName(t *testing.T) {
	one := slugFor(t, "github", "acme/one")
	var names []string
	for _, credential := range []string{"openrouter", "deepseek", "anthropic-api-key", "provider-key", config.AnthropicKeyAccount} {
		name, err := RepoCredentialKey(one, credential)
		if err != nil {
			t.Fatalf("RepoCredentialKey(%s, %s): %v", one, credential, err)
		}
		names = append(names, name)
	}
	for _, name := range names {
		if config.KeyCredentialSecret(name) {
			t.Errorf("%s is a service-served key credential's name; a provider's key entered under it would be served as an Anthropic key by a service that is already running (F33)", name)
		}
		if got, ok := config.CredentialBySecret(name); ok {
			t.Errorf("CredentialBySecret(%s) = %s, true; a name of the provider family is never a credential's derived secret name (F33)", name, got)
		}
		// The bundle's own reading, held the same way for the day the family is
		// added to it (KS1): whatever kind the loader answers for these names,
		// it is never the model kind, because the model kind is the answer that
		// would make the entry a candidate the cascade can select.
		if answer, kind, _, ok := CredentialKey(name); ok && kind == KindModel {
			t.Errorf("CredentialKey(%s) = %s, a model credential; a provider entry is never read as one (F33)", name, answer)
		}
	}
}
