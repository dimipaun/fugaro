package credsvc

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file holds the bundle to C1's family (RepoCredentialPrefix): one entry per
// repository and named provider credential, and one per repository for its own
// Anthropic key (R2, D3008). What it pins is what a document may say about one,
// what the loader refuses, what the accessor answers a caller of a repository,
// and how large the whole document can get — the worst case the plan names,
// counted as repositories times credentials (U5, O8).

// repoEntryKeyFor is one member of the family, for a repository named here: the
// slug is task.Slug's own answer (slugFor, provider_wire_test.go), so no test
// file holds a slug literal a real repository could have.
func repoEntryKeyFor(t *testing.T, provider, repo, credential string) string {
	t.Helper()
	slug := slugFor(t, provider, repo)
	key, err := RepoCredentialKey(slug, credential)
	if err != nil {
		t.Fatalf("RepoCredentialKey(%s, %s) = %v; the test needs a member of the family to write into a document", slug, credential, err)
	}
	return key
}

// repoEntryLines is a named entry's document body, one field per line, in the
// shape `fugaro creds set` writes a structured entry (the provider key's and the
// read consumer's).
func repoEntryLines(t *testing.T, key string, lines ...string) []byte {
	t.Helper()
	doc := key + ":\n"
	for _, l := range lines {
		doc += "  " + l + "\n"
	}
	return []byte(doc)
}

// repoEntryMarker is the copy's marker (D3014) as the document spells it: the
// container the value was read out of and its version. The version is quoted
// because the entry's every field is a string — a bare 3 is a number, which the
// loader and the schema both refuse — and it is what a writer must spell.
func repoEntryMarker(container, version string) []string {
	return []string{"source:", "  container: " + container, "  version: \"" + version + "\""}
}

// repoEntryDoc is a named entry of the tests below: the credential, the address
// a call with it goes to and the auth form it is sent with, with the copy's
// marker appended when the entry is one a copy wrote (extra) and without one for
// an entry it did not (D3014, D3006's `creds set`).
func repoEntryDoc(t *testing.T, key, value, baseURL, auth string, extra ...string) []byte {
	t.Helper()
	return repoEntryLines(t, key, append([]string{"key: " + value, "base_url: " + baseURL, "auth: " + auth}, extra...)...)
}

// namedBundleDoc is the document the tests here start from: today's document —
// every credential the platform defines — with three members of the family
// beside it, which is what a bundle one copy has run against holds.
func namedBundleDoc(t *testing.T) (doc []byte, one, two string, values map[string]string) {
	t.Helper()
	one = slugFor(t, "github", "acme/one")
	two = slugFor(t, "github", "acme/two")
	mine := repoEntryKeyFor(t, "github", "acme/one", "openrouter")
	theirs := repoEntryKeyFor(t, "github", "acme/two", "openrouter")
	own := repoEntryKeyFor(t, "github", "acme/one", config.AnthropicKeyAccount)
	values = map[string]string{
		mine:   fakeCred(t, "one-openrouter"),
		theirs: fakeCred(t, "two-openrouter"),
		own:    fakeCred(t, "one-anthropic"),
	}
	doc = append([]byte{}, bundleDoc(t)...)
	doc = append(doc, repoEntryDoc(t, mine, values[mine], testProviderBase, providerAuthBearer, repoEntryMarker(config.AnthropicSecret, "3")...)...)
	doc = append(doc, repoEntryDoc(t, theirs, values[theirs], testProviderBase, providerAuthXAPIKey)...)
	doc = append(doc, repoEntryDoc(t, own, values[own], "https://api.anthropic.com", providerAuthXAPIKey)...)
	return doc, one, two, values
}

// schemaEntry is a named entry of the schema's agreement table
// (TestBundleSchemaMatchesTheLoader, bundle_test.go): a repository's entry for a
// credential, with the copy's marker when the row is about an entry a copy wrote.
func schemaEntry(t *testing.T, repo, credential, value string, written bool) []byte {
	t.Helper()
	key := repoEntryKeyFor(t, "github", repo, credential)
	if !written {
		return repoEntryDoc(t, key, value, testProviderBase, providerAuthBearer)
	}
	return repoEntryDoc(t, key, value, testProviderBase, providerAuthBearer, repoEntryMarker(config.AnthropicSecret, "3")...)
}

// schemaBoundEntry is a named entry whose whole name is the family's bound, or
// over it by the bytes asked for: the row that pins the one number both readers
// carry (MaxRepoCredentialBytes). The name is made of a readable half of 46 bytes
// — task's own bound for one — and the 16-hex hash, so the row below it is a name
// ONE byte too long rather than a slug shape either reader refuses for another
// reason.
func schemaBoundEntry(t *testing.T, over int) []byte {
	t.Helper()
	key, err := RepoCredentialKey(strings.Repeat("a", 46+over)+"-"+strings.Repeat("0", 16), config.AnthropicKeyAccount)
	if err != nil {
		t.Fatalf("RepoCredentialKey for a name at the family's bound: %v", err)
	}
	if want := MaxRepoCredentialBytes + over; len(key) != want {
		t.Fatalf("the row's name is %d bytes, want %d; the row is about the family's bound and nothing else", len(key), want)
	}
	return repoEntryDoc(t, key, fakeCred(t, "at-bound"), testProviderBase, providerAuthBearer)
}

// schemaLongSlugEntry is the one row of this family where the loader's rule is
// looser than a slug's own bound, and it is pinned here rather than left to be
// found by reading the two rules together.
//
// RepoCredentialByKey holds the slug half to task.ValidSlug's SHAPE, and
// task.ValidSlug bounds no length — a slug this tree DERIVES is at most
// maxSlugBytes (46 readable bytes and the hash), but nothing in the rule says so
// — while the number the schema and the loader both apply is the WHOLE NAME's
// (MaxRepoCredentialBytes). The schema cannot do better here: JSON Schema has no
// length for a group inside a pattern, and the credential half's own length
// varies (a provider's name is 1 to 20 bytes and the project key's spelling is
// 26), so no whole-name bound can bound the slug half. What the row asserts is
// that the two readers agree — both read this name — and the reason it is safe
// either way is that no writer can build it: RepoCredentialKey takes the slug
// from a verified run's identity, and an entry nothing can ask for is an entry
// nothing is served.
func schemaLongSlugEntry(t *testing.T) []byte {
	t.Helper()
	slug := strings.Repeat("b", 60) + "-" + strings.Repeat("0", 16)
	if len(slug) <= maxSlugBytes {
		t.Fatalf("the row's slug is %d bytes and a slug's own bound is %d; the row is about a slug over it", len(slug), maxSlugBytes)
	}
	key, err := RepoCredentialKey(slug, "a")
	if err != nil {
		t.Fatalf("RepoCredentialKey(%s, a): %v", slug, err)
	}
	if len(key) > MaxRepoCredentialBytes {
		t.Fatalf("the row's name is %d bytes and the family's bound is %d; the row is about a name WITHIN the bound", len(key), MaxRepoCredentialBytes)
	}
	return repoEntryDoc(t, key, fakeCred(t, "long-slug"), testProviderBase, providerAuthBearer)
}

// TestBundleServesANamedProviderEntry is the plan's red step and the whole of
// what the bundle owes C1's accessor: before this task every member of the
// family was refused whole as a key that is not a credential name, and now the
// document decodes one and the accessor serves it to the repository it is held
// for — with the address the call goes to, the form the key is sent in, the
// marker of the copy that wrote it (D3014) and the value, which nothing renders.
//
// The refusals beside it are the two the review of this task names: the auth
// form is the closed set of two words (D1418) and the address is the platform's
// own provider-URL rule (config.CheckProviderBaseURL: https, or http on a
// loopback host), each read from the config rather than respelled, so an entry
// the gateway's own routes would refuse is one this loader refuses too.
func TestBundleServesANamedProviderEntry(t *testing.T) {
	doc, one, two, values := namedBundleDoc(t)
	mine := repoEntryKeyFor(t, "github", "acme/one", "openrouter")
	theirs := repoEntryKeyFor(t, "github", "acme/two", "openrouter")
	own := repoEntryKeyFor(t, "github", "acme/one", config.AnthropicKeyAccount)

	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatalf("ParseBundle of a document holding the family: %v; the red step was that such a key is refused as an unknown key (R2)", err)
	}
	if got := b.Len(); got != 8 {
		t.Errorf("the document holds %d entries, want today's five and the family's three", got)
	}
	// The kind and the tier each member of the family is read as: a kind of its
	// own — never KindModel, which is the answer that would make an entry a
	// candidate a cascade may select (F33) — and a tier read off the credential
	// half, so the family's two credentials are not one entry with one tier.
	for _, tc := range []struct {
		key, credential string
		tier            creds.Tier
	}{
		{mine, "openrouter", creds.TierC},
		{theirs, "openrouter", creds.TierC},
		{own, config.AnthropicKeyAccount, creds.TierB},
	} {
		name, kind, tier, ok := CredentialKey(tc.key)
		if !ok || kind != KindRepoCredential || tier != tc.tier || name != tc.key {
			t.Errorf("CredentialKey(%s) = %q, %q, %q, %v; want the family's kind, the document key as its name and %q", tc.key, name, kind, tier, ok, tc.tier)
		}
	}

	t.Run("the entry a copy wrote", func(t *testing.T) {
		entry, ok := ReadProviderKey(b, one, "openrouter")
		if !ok {
			t.Fatalf("ReadProviderKey(%s, openrouter) answered none; the document holds it", one)
		}
		if entry.Slug != one || entry.Credential != "openrouter" {
			t.Errorf("the entry is held for %q and %q, want %q and openrouter", entry.Slug, entry.Credential, one)
		}
		if entry.BaseURL != testProviderBase {
			t.Errorf("the entry's address = %q, want %q (D1418)", entry.BaseURL, testProviderBase)
		}
		if entry.Auth != providerAuthBearer {
			t.Errorf("the entry's auth form = %q, want %q", entry.Auth, providerAuthBearer)
		}
		if entry.Source.Container != config.AnthropicSecret || entry.Source.Version != "3" {
			t.Errorf("the entry's source marker = %+v, want the container and version the copy read it from (D3014)", entry.Source)
		}
		if entry.Value() != values[mine] {
			t.Errorf("the entry's value is not the one the document holds for %s", one)
		}
		// Nothing renders the value: the entry is what a caller logs.
		for _, s := range []string{entry.String(), entry.GoString(), fmt.Sprintf("%v", entry.LogValue())} {
			if strings.Contains(s, values[mine]) {
				t.Errorf("the entry renders its credential: %q", s)
			}
		}
	})

	t.Run("the entry a copy did not write", func(t *testing.T) {
		entry, ok := ReadProviderKey(b, two, "openrouter")
		if !ok {
			t.Fatalf("ReadProviderKey(%s, openrouter) answered none; the document holds it", two)
		}
		if entry.Value() != values[theirs] {
			t.Errorf("the entry's value is not the one the document holds for %s", two)
		}
		if !entry.Source.IsZero() {
			t.Errorf("an entry with no marker was read as one a copy wrote: %+v; the zero marker is the whole of \"the copy did not write this\" (D3014, KS12)", entry.Source)
		}
	})

	t.Run("the repository's own Anthropic key", func(t *testing.T) {
		// D3008: the repository's own key is an entry of this family too, under
		// the account axis's spelling for the project's — a provider may legally
		// be named "anthropic-api-key", so one spelling for both would make the
		// repository's own key and that provider's key the same entry.
		entry, ok := ReadProviderKey(b, one, config.AnthropicKeyAccount)
		if !ok {
			t.Fatalf("ReadProviderKey(%s, %s) answered none; the document holds it (D3008)", one, config.AnthropicKeyAccount)
		}
		if entry.Value() != values[own] {
			t.Errorf("the repository's own Anthropic key is not the one the document holds for %s", one)
		}
	})

	t.Run("nothing is held of what the document does not say", func(t *testing.T) {
		for _, tc := range []struct{ what, slug, credential string }{
			{"a credential the repository holds nothing for", one, "deepseek"},
			{"a credential of the project's own key's name", one, config.AnthropicSecret},
			{"a repository the document holds nothing for", slugFor(t, "bitbucket", "acme/three"), "openrouter"},
			{"a credential name that is not one of the family's", one, "openrouter-production"},
		} {
			entry, ok := ReadProviderKey(b, tc.slug, tc.credential)
			if ok {
				t.Errorf("ReadProviderKey(%s, %s) answered %q; %s (C1: two answers, the entry or none)", tc.slug, tc.credential, entry.Value(), tc.what)
			}
			if entry.Slug != "" || entry.Credential != "" || entry.BaseURL != "" || entry.Auth != "" || !entry.Source.IsZero() || entry.Value() != "" {
				t.Errorf("a refusal answered a partly filled entry: %+v", entry)
			}
		}
	})

	t.Run("what every path that redacts walks", func(t *testing.T) {
		// The family's values are credentials: Secrets is what creds.go's write
		// forms and the service's own scrub walk, and an entry whose value was
		// not in it is the one entry a log line could carry.
		secrets := b.Secrets()
		for key, value := range values {
			if !slices.Contains(secrets, value) {
				t.Errorf("Secrets() does not carry the value of %s; every path that redacts walks it", key)
			}
		}
	})

	// The shapes both the loader and its caller must refuse, one row per check
	// the task names. A row that is a legal shape is asserted to LOAD, so a
	// refusal that became too wide fails here too.
	slugged := func(credential string) string { return repoEntryKeyFor(t, "github", "acme/one", credential) }
	cred, pw := fakeCred(t, "x"), fakeCred(t, "pw")
	for _, tc := range []struct {
		name    string
		doc     []byte
		want    error
		wantErr bool
	}{
		{"a loopback address on http, which the config check admits for a local test",
			repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), "http://127.0.0.1:8080/v1", providerAuthXAPIKey), nil, false},
		{"a provider name at the rule's length bound",
			repoEntryDoc(t, slugged(strings.Repeat("p", 20)), fakeCred(t, "x"), testProviderBase, providerAuthBearer), nil, false},
		{"a marker whose version is a number and not the string the field is",
			repoEntryLines(t, slugged("openrouter"), "key: "+fakeCred(t, "x"), "base_url: "+testProviderBase, "auth: bearer", "source:", "  container: "+config.AnthropicSecret, "  version: 3"), ErrBundleBadValue, true},
		{"a value that is a string and not the mapping a provider call needs",
			[]byte(slugged("openrouter") + ": " + fakeCred(t, "x") + "\n"), ErrBundleBadValue, true},
		{"no key", repoEntryLines(t, slugged("openrouter"), "base_url: "+testProviderBase, "auth: bearer"), ErrBundleBadValue, true},
		{"no base URL", repoEntryLines(t, slugged("openrouter"), "key: "+fakeCred(t, "x"), "auth: bearer"), ErrBundleBadValue, true},
		{"no auth form", repoEntryLines(t, slugged("openrouter"), "key: "+fakeCred(t, "x"), "base_url: "+testProviderBase), ErrBundleBadValue, true},
		{"an auth form outside the two the platform defines",
			repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), testProviderBase, "oauth"), ErrBundleBadValue, true},
		{"an auth form that is only nearly one of the two",
			repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), testProviderBase, "Bearer"), ErrBundleBadValue, true},
		{"an empty field", repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), testProviderBase, "\"\""), ErrBundleBadValue, true},
		{"an address with no scheme", repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), "provider.example", providerAuthBearer), ErrBundleBadValue, true},
		{"a plaintext address", repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), "http://provider.example/api", providerAuthBearer), ErrBundleBadValue, true},
		{"an address carrying a credential", repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), "https://user:"+fakeCred(t, "pw")+"@provider.example/api", providerAuthBearer), ErrBundleBadValue, true},
		{"an address with a query", repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), testProviderBase+"?key="+fakeCred(t, "x"), providerAuthBearer), ErrBundleBadValue, true},
		{"an address with a dot segment", repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), "https://provider.example/a/../api", providerAuthBearer), ErrBundleBadValue, true},
		{"an address that is not a string", repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), "1234", providerAuthBearer), ErrBundleBadValue, true},
		{"a field the entry does not define", repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), testProviderBase, providerAuthBearer, "models: [\"x/y\"]"), ErrBundleUnknownField, true},
		{"half a marker: a container and no version", repoEntryLines(t, slugged("openrouter"), "key: "+fakeCred(t, "x"), "base_url: "+testProviderBase, "auth: bearer", "source:", "  container: "+config.AnthropicSecret), ErrBundleBadValue, true},
		{"half a marker: a version and no container", repoEntryLines(t, slugged("openrouter"), "key: "+fakeCred(t, "x"), "base_url: "+testProviderBase, "auth: bearer", "source:", "  version: \"3\""), ErrBundleBadValue, true},
		{"a marker with a field it does not define", repoEntryLines(t, slugged("openrouter"), "key: "+fakeCred(t, "x"), "base_url: "+testProviderBase, "auth: bearer", "source:", "  container: "+config.AnthropicSecret, "  version: \"3\"", "  digest: x"), ErrBundleUnknownField, true},
		{"an empty marker", repoEntryLines(t, slugged("openrouter"), "key: "+fakeCred(t, "x"), "base_url: "+testProviderBase, "auth: bearer", "source: {}"), ErrBundleBadValue, true},
		{"a credential the family's rule does not admit", []byte(RepoCredentialPrefix + one + RepoCredentialSep + "open_router:\n  key: " + fakeCred(t, "x") + "\n  base_url: " + testProviderBase + "\n  auth: bearer\n"), ErrBundleUnknownKey, true},
		{"a slug that is not a slug", []byte(RepoCredentialPrefix + "acme-one" + RepoCredentialSep + "openrouter:\n  key: " + fakeCred(t, "x") + "\n  base_url: " + testProviderBase + "\n  auth: bearer\n"), ErrBundleUnknownKey, true},
		{"a slug carrying the family's separator", []byte(RepoCredentialPrefix + "acme" + RepoCredentialSep + "one-0123456789abcdef" + RepoCredentialSep + "openrouter:\n  key: " + fakeCred(t, "x") + "\n  base_url: " + testProviderBase + "\n  auth: bearer\n"), ErrBundleUnknownKey, true},
		{"the entry written twice", append(repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "x"), testProviderBase, providerAuthBearer), repoEntryDoc(t, slugged("openrouter"), fakeCred(t, "y"), testProviderBase, providerAuthBearer)...), ErrBundleUnknownKey, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ParseBundle(tc.doc)
			if tc.wantErr {
				if !errors.Is(err, tc.want) {
					t.Fatalf("ParseBundle(%q) = %v, %v; want %v", tc.doc, b, err, tc.want)
				}
				// The refusal names the ENTRY and carries no field's content: not
				// the value, not the address, not the auth form. (It does carry
				// the document's own key, which is the operator's to fix and is
				// already a name — BundleError's own rule.)
				for _, v := range []string{cred, pw, testProviderBase, providerAuthBearer, "Bearer", "oauth"} {
					if strings.Contains(err.Error(), v) {
						t.Errorf("the refusal carries document text (%q): %v", v, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBundle(%q) = %v; this is a shape the loader reads", tc.doc, err)
			}
		})
	}
}

// TestBundleOffersACallerItsOwnRepositorysEntries is the second half of the
// acceptance: a coding caller is offered the entries of its own repository, and
// never another repository's.
//
// Several repositories hold one provider name, so the names the caller speaks
// are the same and only the repository half tells the entries apart. The caller
// is served through C1's accessor, which derives the document key from the
// repository's VERIFIED slug (R2, HG1's reach rule): the offering of a
// repository is its own entries, and a repository the document holds nothing for
// is offered nothing rather than another's.
//
// The list a candidate is built from is deliberately NOT where they are offered
// (C1: "the list is built through the accessor and nothing in it is a tier").
// Credentials is built from the bundle alone while an entry of this family is
// one repository's, and it is a list a selection walks: a tier-C entry in it
// would refuse every call the caller makes (checkCandidates names Check 50)
// rather than be served. So the family is offered by name and this list is
// unchanged — which is what the account above it does too: an account's OAuth
// token is one entry for every repository that may go out on it (D3015).
func TestBundleOffersACallerItsOwnRepositorysEntries(t *testing.T) {
	doc, one, two, values := namedBundleDoc(t)
	mine := repoEntryKeyFor(t, "github", "acme/one", "openrouter")
	theirs := repoEntryKeyFor(t, "github", "acme/two", "openrouter")
	own := repoEntryKeyFor(t, "github", "acme/one", config.AnthropicKeyAccount)
	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatal(err)
	}

	offered := map[string]string{}
	for _, credential := range []string{"openrouter", config.AnthropicKeyAccount} {
		entry, ok := ReadProviderKey(b, one, credential)
		if !ok {
			t.Fatalf("a caller of %s was offered nothing for %s; the document holds that repository's entry", one, credential)
		}
		offered[credential] = entry.Value()
	}
	if offered["openrouter"] != values[mine] {
		t.Errorf("a caller of %s was offered the value held for another repository; that is the key HG1's reach rule exists to keep from it", one)
	}
	if offered[config.AnthropicKeyAccount] != values[own] {
		t.Errorf("a caller of %s was not offered its own Anthropic key (D3008)", one)
	}
	// The other repository's caller is offered the other entry, of the same
	// credential's name: one name, two entries, two repositories.
	if entry, ok := ReadProviderKey(b, two, "openrouter"); !ok || entry.Value() != values[theirs] {
		t.Errorf("a caller of %s is not offered its own entry of a shared provider name (D3008)", two)
	}
	// And the list a candidate is built from carries no member of the family:
	// the entries are offered by name, and a tier-C entry in this list would
	// refuse the whole call it appeared in.
	want := []creds.Credential{
		{Name: config.DefaultClaudeAccount, Tier: creds.TierA},
		{Name: "alice", Tier: creds.TierA},
		{Name: config.AnthropicSecret, Tier: creds.TierB},
	}
	got := b.Credentials()
	if len(got) != len(want) {
		t.Fatalf("Credentials() = %v; the family is not a candidate of a coding caller (C1, KS3)", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Credentials()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	for _, c := range got {
		if strings.Contains(c.Name, RepoCredentialPrefix) {
			t.Errorf("Credentials() offers %v; a named entry is offered by name and never as a candidate", c)
		}
	}
	// The account's own token is the same entry for every repository (D3015):
	// it is shared by design, and no repository's offering holds a copy of it.
	if got, ok := b.Lookup("alice"); !ok || got.Key != config.ClaudeAccountSecret("alice") || got.Tier != creds.TierA {
		t.Errorf("the account's OAuth token is not the model credential it was: %v, %v (D3015)", got, ok)
	}
	// The family is not reachable by a name either: Lookup answers model
	// credentials, and the document key of a named entry is not one.
	if got, ok := b.Lookup(theirs); ok {
		t.Errorf("Lookup(%s) = %v; a named entry is read through C1's accessor and never as a model credential", theirs, got)
	}
}

// TestTodaysBundleStillLoads is the shape the change must not break: a document
// holding nothing of the family loads exactly as it did, resolves every key it
// holds to the credential it always was, and answers the accessor "no such
// credential" rather than an error — which is the answer a run of a repository
// whose keys were never copied meets, and the one that keeps the copy (KS12) an
// addition rather than a migration.
func TestTodaysBundleStillLoads(t *testing.T) {
	b, err := ParseBundle(bundleDoc(t))
	if err != nil {
		t.Fatalf("today's document is refused: %v; the family is an addition and no format version moved", err)
	}
	if got := b.Len(); got != 5 {
		t.Errorf("today's document holds %d entries, want its five", got)
	}
	if got := len(b.Credentials()); got != 3 {
		t.Errorf("today's document offers %d model credentials, want its three", got)
	}
	// Every key today's document may hold still resolves to what it was: an
	// account's token is a model credential of tier A (D3015) and is not read as
	// a member of the family, whose names all begin RepoCredentialPrefix.
	for _, tc := range []struct {
		key  string
		kind Kind
		tier creds.Tier
	}{
		{config.ClaudeOAuthToken, KindModel, creds.TierA},
		{config.ClaudeAccountSecret("alice"), KindModel, creds.TierA},
		{config.AnthropicSecret, KindModel, creds.TierB},
		{config.GitHubAppKeySecret, KindGitHubApp, ""},
		{config.BitbucketSecret, KindGit, ""},
	} {
		if _, kind, tier, ok := CredentialKey(tc.key); !ok || kind != tc.kind || tier != tc.tier {
			t.Errorf("CredentialKey(%s) = %q, %q, %v; want %q, %q", tc.key, kind, tier, ok, tc.kind, tc.tier)
		}
	}
	// The accessor's answer for a repository of a bundle holding none of the
	// family: no entry, and a zero entry rather than a half-filled one.
	one := slugFor(t, "github", "acme/one")
	for _, credential := range []string{"openrouter", config.AnthropicKeyAccount} {
		entry, ok := ReadProviderKey(b, one, credential)
		if ok || entry.Value() != "" || entry.BaseURL != "" || entry.Auth != "" || !entry.Source.IsZero() {
			t.Errorf("ReadProviderKey answered %+v, %v for a bundle holding no member of the family", entry, ok)
		}
	}
	// A bundle that holds ONLY a member of the family is a document of its own
	// right: the family is a set of credentials, not a field of a document that
	// already holds something else.
	only, err := ParseBundle(repoEntryDoc(t, repoEntryKeyFor(t, "github", "acme/one", "deepseek"),
		fakeCred(t, "only"), testProviderBase, providerAuthBearer))
	if err != nil {
		t.Fatalf("a document holding one named entry is refused: %v", err)
	}
	if only.Len() != 1 || len(only.Credentials()) != 0 {
		t.Errorf("a document holding one named entry holds %d entries and offers %d candidates, want one and none", only.Len(), len(only.Credentials()))
	}
}

// TestAWorstCaseBundleFits is U5's settlement: the size of the document at the
// worst case the plan states — repositories times credentials (O8) — against
// Secret Manager's limit for one secret version.
//
// The worst case is COUNTED, and the count is the document's own bound: the
// loader refuses a document over MaxBundleCredentials entries, and D3008 is what
// can reach it, because a key several repositories share is entered once per
// repository. The stated worst case is therefore that count — sixteen
// repositories with four named credentials each — with every entry written at
// the size a real one has: the family's longest name (a slug of a repository's
// own 63 bytes and the longest credential's name the rule admits), a provider API
// key of 512 bytes (an Anthropic key is 108), an address the length of a real
// provider's, and the copy's marker.
//
// Two things fall out and both are asserted. The document fits with room to
// spare — it is the COUNT a real installation meets long before the byte bound —
// and a document that tried the byte bound instead, every entry's value at
// MaxBundleValueBytes, is refused by MaxBundleBytes before it is parsed, which is
// what keeps the container's own limit out of any writer's reach.
func TestAWorstCaseBundleFits(t *testing.T) {
	const (
		repos              = 16
		credentialsPerRepo = 4
		apiKeyBytes        = 512
		baseURL            = "https://api.openrouter.ai/api/v1"
		container          = "openrouter-api-key"
		version            = "42"
	)
	if repos*credentialsPerRepo != MaxBundleCredentials {
		t.Fatalf("the stated worst case is %d entries and the document's own bound is %d; this test's worst case is the bound, so the two numbers move together", repos*credentialsPerRepo, MaxBundleCredentials)
	}
	credentials := []string{"deepseek", "openrouter", strings.Repeat("p", 20), config.AnthropicKeyAccount}
	value := strings.Repeat("k", apiKeyBytes)
	var doc strings.Builder
	for i := 0; i < repos; i++ {
		// A slug at task.Slug's own bound: 46 readable bytes and the 16-hex hash.
		readable := fmt.Sprintf("%s-%02d", strings.Repeat("r", 43), i)
		slug := readable + "-" + fmt.Sprintf("%016x", i)
		if !task.ValidSlug(slug) {
			t.Fatalf("the test's own slug %q is not one task.Slug derives", slug)
		}
		for _, credential := range credentials {
			key, err := RepoCredentialKey(slug, credential)
			if err != nil {
				t.Fatalf("RepoCredentialKey(%s, %s): %v", slug, credential, err)
			}
			doc.Write(repoEntryDoc(t, key, value, baseURL, providerAuthBearer, repoEntryMarker(container, version)...))
		}
	}
	raw := []byte(doc.String())
	b, err := ParseBundle(raw)
	if err != nil {
		t.Fatalf("the worst case is refused: %v", err)
	}
	if got := b.Len(); got != MaxBundleCredentials {
		t.Fatalf("the worst case holds %d entries, want %d", got, MaxBundleCredentials)
	}
	if len(raw) >= MaxBundleBytes {
		t.Errorf("the worst case is %d bytes and the bundle's own bound is %d; an installation at the count bound would hold a document the loader refuses", len(raw), MaxBundleBytes)
	}
	if len(raw) >= 56<<10 {
		t.Errorf("the worst case is %d bytes, want well under the %d the bundle and the container both allow: one entry has grown past what a provider key's facts are", len(raw), MaxBundleBytes)
	}
	t.Logf("worst case: %d entries in %d bytes of the %d a secret version holds (%.0f%%)", b.Len(), len(raw), MaxBundleBytes, 100*float64(len(raw))/float64(MaxBundleBytes))

	// The byte bound is the other half of the same guard, and it is the one a
	// document of outsized VALUES meets: the same count, each value at the bound
	// the document allows one, is refused before it is parsed.
	var big strings.Builder
	for i := 0; i < MaxBundleCredentials; i++ {
		key := repoEntryKeyFor(t, "github", fmt.Sprintf("acme/one-%02d", i), "openrouter")
		big.Write(repoEntryDoc(t, key, strings.Repeat("k", MaxBundleValueBytes), baseURL, providerAuthBearer))
	}
	if _, err := ParseBundle([]byte(big.String())); !errors.Is(err, ErrBundleTooLarge) {
		t.Errorf("a document whose values are at the per-value bound = %v; want ErrBundleTooLarge, which is what keeps it out of the container", err)
	}
}
