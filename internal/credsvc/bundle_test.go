package credsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
)

// readSchemaFile reads a published schema, for the test that holds the
// loader to the document shape the schema publishes.
func readSchemaFile(t *testing.T, path string) ([]byte, error) {
	t.Helper()
	return os.ReadFile(path)
}

// fakeCred is a credential-shaped string that is not a credential: it is
// built at run time from the test's own name, so no test file holds a
// secret-shaped literal (the repository's Convention 2) and a value can be
// searched for in an error or a log line by a string that no other test
// writes.
func fakeCred(t *testing.T, what string) string {
	t.Helper()
	return "fake-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + what + "-not-a-real-credential"
}

// scriptedSource is a BundleSource a test drives: it answers the documents it
// was given, in order, and counts its reads.
type scriptedSource struct {
	mu    sync.Mutex
	docs  [][]byte
	errs  []error
	reads int
}

func (s *scriptedSource) Latest(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.reads
	s.reads++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	if i < len(s.docs) {
		return s.docs[i], nil
	}
	// Past the script: the container is still there and still holds the last
	// document, which is what a real container does between two reads.
	if len(s.docs) > 0 {
		return s.docs[len(s.docs)-1], nil
	}
	return nil, ErrBundleAbsent
}

func (s *scriptedSource) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// bundleDoc is a valid document holding every credential the platform
// defines: the two model families, the GitHub App's private key (C2.1) and
// the upstream Bitbucket token (D506). The last two are in it on purpose —
// each is in the document and NOT in the cascade, and a document without one
// would not show that.
func bundleDoc(t *testing.T) []byte {
	t.Helper()
	return []byte("claude-oauth-token: " + fakeCred(t, "default") + "\n" +
		"claude-oauth-token-alice: " + fakeCred(t, "alice") + "\n" +
		config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n" +
		config.GitHubAppKeySecret + ": " + fakeCred(t, "appkey") + "\n" +
		config.BitbucketSecret + ": " + fakeCred(t, "gittoken") + "\n")
}

// TestBundleLoadsOncePerCall: one read of the container answers a whole
// call's credential set (Q6: the load is one read of one container, not one
// read per credential), the parsed document is served from the cache inside
// the TTL, and a document REPLACED between two calls is the one read after
// the TTL — so a rotation by C1.25 is picked up quickly and a cache hit and
// a cache miss parse to the same thing.
//
// The last third of the test is the fail-closed half of the cache: a read
// that fails after a good document was cached is a refusal, never the cached
// document served again. A bundle an operator has replaced with one this
// loader refuses must stop issuance, not keep issuing from the copy read a
// minute ago.
func TestBundleLoadsOncePerCall(t *testing.T) {
	first := bundleDoc(t)
	second := []byte(config.AnthropicSecret + ": " + fakeCred(t, "rotated") + "\n")
	src := &scriptedSource{docs: [][]byte{first, second}}
	now := time.Unix(1700000000, 0)
	l := &BundleLoader{Source: src, Now: func() time.Time { return now }}

	b, err := l.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := b.Names(); len(got) != 5 {
		t.Fatalf("names = %v, want the five credentials of the document", got)
	}
	// Every credential of one call, and every credential of the next call
	// inside the TTL, comes from the one read.
	for i := 0; i < 4; i++ {
		if _, err := l.Load(context.Background()); err != nil {
			t.Fatalf("Load (cached, %d): %v", i, err)
		}
	}
	if n := src.readCount(); n != 1 {
		t.Errorf("the container was read %d times inside one TTL, want 1", n)
	}
	// A cache hit and a cache miss parse to the same thing.
	cached, err := l.Load(context.Background())
	if err != nil {
		t.Fatalf("Load (cached): %v", err)
	}
	fresh, err := ParseBundle(first)
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if fmt.Sprint(cached) != fmt.Sprint(fresh) {
		t.Errorf("the cached bundle %v is not the parsed document %v", cached, fresh)
	}

	// The TTL passes and the container holds a new document: that is the one
	// read, and it is the one served.
	now = now.Add(BundleCacheTTL + time.Second)
	b, err = l.Load(context.Background())
	if err != nil {
		t.Fatalf("Load (after the TTL): %v", err)
	}
	if n := src.readCount(); n != 2 {
		t.Errorf("the container was read %d times after one TTL, want 2", n)
	}
	if got := b.Names(); len(got) != 1 || got[0] != config.AnthropicSecret {
		t.Errorf("after the rotation the bundle holds %v, want the replaced document", got)
	}

	// And a read that fails after the TTL is a refusal: the document read
	// before it is NOT served.
	now = now.Add(BundleCacheTTL + time.Second)
	src.mu.Lock()
	src.errs = []error{nil, nil, ErrBundleUnreadable}
	src.mu.Unlock()
	if b, err := l.Load(context.Background()); err == nil {
		t.Fatalf("Load over a failing read answered %v, want a refusal", b)
	}
}

// TestBundleUnknownKeyRefused: a key that is not a credential name is a
// refusal and never an ignored entry. A typo'd key that parsed would be a
// credential nobody meant to store, read as nothing and never noticed.
func TestBundleUnknownKeyRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"a misspelt family", "antropic-api-key: " + fakeCred(t, "x") + "\n"},
		{"a name outside the account rule", "claude-oauth-token-9alice: " + fakeCred(t, "x") + "\n"},
		{"a name with no account at all", "claude-oauth-token-: " + fakeCred(t, "x") + "\n"},
		{"a provider's key", "openrouter-api-key: " + fakeCred(t, "x") + "\n"},
		{"the container's own name", "creds-bundle: " + fakeCred(t, "x") + "\n"},
		{"a non-string key", "1234: " + fakeCred(t, "x") + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ParseBundle([]byte(tc.doc))
			if !errors.Is(err, ErrBundleUnknownKey) {
				t.Fatalf("ParseBundle(%q) = %v, %v; want ErrBundleUnknownKey", tc.doc, b, err)
			}
		})
	}
	// A valid document is the control: the rule refuses the keys above and
	// nothing else.
	if _, err := ParseBundle(bundleDoc(t)); err != nil {
		t.Fatalf("the valid document was refused: %v", err)
	}
}

// TestBundleHoldsTheAppKeyAndAnAccountNamedAfterIt: the App key and the
// account family are two namespaces that can spell the same string, so a
// document may legally hold both config.GitHubAppKeySecret and the account
// config.ClaudeAccountSecret(config.GitHubAppKeySecret) — the latter resolves
// to the name "github-app-key", which is the former's document key. Both keys
// are legal (the published schema admits both), so the document loads, and the
// two entries stay tellable apart: AppKey answers the key by its kind, Lookup
// answers the account by its name, and the cascade is offered the account
// alone.
//
// This is the case a single by-name index gets wrong: it would refuse a valid
// document with ErrBundleUnknownKey, which is both a false diagnosis and the
// loader disagreeing with the schema it publishes.
func TestBundleHoldsTheAppKeyAndAnAccountNamedAfterIt(t *testing.T) {
	doc := []byte(config.GitHubAppKeySecret + ": " + fakeCred(t, "the-app-key") + "\n" +
		config.ClaudeAccountSecret(config.GitHubAppKeySecret) + ": " + fakeCred(t, "the-account") + "\n")
	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatalf("ParseBundle(%q) = %v; the document holds two legal keys", doc, err)
	}
	if b.Len() != 2 {
		t.Fatalf("the bundle holds %d entries, want 2 (%v)", b.Len(), b.Names())
	}
	app, ok := b.AppKey()
	if !ok {
		t.Fatal("AppKey found nothing, and the document holds the key")
	}
	if app.Kind != KindGitHubApp || app.Key != config.GitHubAppKeySecret {
		t.Errorf("AppKey() = %+v, want the github-app entry keyed %q", app, config.GitHubAppKeySecret)
	}
	if !strings.HasSuffix(app.Value(), "the-app-key-not-a-real-credential") {
		t.Errorf("AppKey() answered the account, not the key: %q", app.Value())
	}
	acct, ok := b.Lookup(config.GitHubAppKeySecret)
	if !ok {
		t.Fatal("Lookup found no account named after the App key")
	}
	if acct.Kind != KindModel || acct.Tier != creds.TierA || acct.Key != config.ClaudeAccountSecret(config.GitHubAppKeySecret) {
		t.Errorf("Lookup(%q) = %+v, want the tier-A account keyed %q", config.GitHubAppKeySecret, acct, config.ClaudeAccountSecret(config.GitHubAppKeySecret))
	}
	if !strings.HasSuffix(acct.Value(), "the-account-not-a-real-credential") {
		t.Errorf("Lookup answered the App key, not the account: %q", acct.Value())
	}
	// The cascade sees the account and not the key: one credential, and the
	// kind is what excludes the other.
	if got := b.Credentials(); len(got) != 1 || got[0] != (creds.Credential{Name: config.GitHubAppKeySecret, Tier: creds.TierA}) {
		t.Errorf("Credentials() = %v, want the account alone", got)
	}

	// The other direction: what is still a duplicate. A repeated App key is
	// one credential written twice, and two spellings of one account are one
	// credential written twice — both are documents no writer produces, and
	// both are refused rather than silently collapsed.
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"the app key twice", config.GitHubAppKeySecret + ": " + fakeCred(t, "x") + "\n" + config.GitHubAppKeySecret + ": " + fakeCred(t, "y") + "\n"},
		{"two spellings of one account", config.ClaudeOAuthToken + ": " + fakeCred(t, "x") + "\n" +
			config.ClaudeAccountSecret(config.DefaultClaudeAccount) + ": " + fakeCred(t, "y") + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseBundle([]byte(tc.doc))
			if !errors.Is(err, ErrBundleUnknownKey) {
				t.Fatalf("ParseBundle(%q) = %v, %v; want ErrBundleUnknownKey", tc.doc, got, err)
			}
		})
	}
}

// TestBundleBitbucketTokenStrict: the upstream Bitbucket credential is a
// bundle entry of its own, and it is held to the SAME strict decoding as
// every other entry — a key outside the platform's credentials is refused,
// and so is a value that is empty, not a string, or over the per-credential
// bound. Before this change the key was refused as unknown, which is the red
// step this test is.
func TestBundleBitbucketTokenStrict(t *testing.T) {
	ok, err := ParseBundle([]byte(config.BitbucketSecret + ": " + fakeCred(t, "bb") + "\n"))
	if err != nil {
		t.Fatalf("ParseBundle with %s = %v; the platform defines this credential", config.BitbucketSecret, err)
	}
	if ok.Len() != 1 {
		t.Fatalf("the document holds %d entries, want 1", ok.Len())
	}
	for _, tc := range []struct {
		name string
		doc  string
		want error
	}{
		{"an unknown key beside it", config.BitbucketSecret + ": " + fakeCred(t, "bb") + "\nbitbucket-tokens: " + fakeCred(t, "x") + "\n", ErrBundleUnknownKey},
		{"an empty value", config.BitbucketSecret + ": \"\"\n", ErrBundleBadValue},
		{"a value over the bound", config.BitbucketSecret + ": " + strings.Repeat("x", MaxBundleValueBytes+1) + "\n", ErrBundleBadValue},
		{"a value that is not a string", config.BitbucketSecret + ": 1234\n", ErrBundleBadValue},
		{"the key twice", config.BitbucketSecret + ": " + fakeCred(t, "x") + "\n" + config.BitbucketSecret + ": " + fakeCred(t, "y") + "\n", ErrBundleUnknownKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseBundle([]byte(tc.doc))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseBundle(%q) = %v, %v; want %v", tc.doc, got, err, tc.want)
			}
		})
	}
}

// TestBundleGitKindNeverACascadeCandidate: the Bitbucket credential is in the
// document and is NEVER a credential the cascade may select — the same
// posture the GitHub App's key has, and for the same reason: a candidate the
// cascade could select would be handed to an Anthropic call as the caller's
// credential. Credentials is the whole of what the cascade is offered, and
// the git kind is not in it; Lookup answers model credentials only, so the
// git entry is reachable solely through BitbucketToken, and that accessor
// selects by KIND and never by name.
func TestBundleGitKindNeverACascadeCandidate(t *testing.T) {
	doc := []byte(config.ClaudeOAuthToken + ": " + fakeCred(t, "default") + "\n" +
		config.BitbucketSecret + ": " + fakeCred(t, "bb") + "\n")
	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatal(err)
	}
	got := b.Credentials()
	if len(got) != 1 || got[0] != (creds.Credential{Name: config.DefaultClaudeAccount, Tier: creds.TierA}) {
		t.Errorf("Credentials() = %v, want the model credential alone", got)
	}
	for _, c := range got {
		if c.Name == config.BitbucketSecret {
			t.Errorf("Credentials() offers the git credential (%v) to the cascade", c)
		}
	}
	if _, ok := b.Lookup(config.BitbucketSecret); ok {
		t.Errorf("Lookup(%q) answered, and it answers model credentials only", config.BitbucketSecret)
	}
	e, ok := b.BitbucketToken()
	if !ok {
		t.Fatalf("BitbucketToken() found nothing, and the document holds %q", config.BitbucketSecret)
	}
	if e.Kind != KindGit || e.Tier != "" || e.Key != config.BitbucketSecret {
		t.Errorf("BitbucketToken() = %+v, want the git kind, no tier, keyed %q", e, config.BitbucketSecret)
	}
	if e.Value() != fakeCred(t, "bb") {
		t.Errorf("BitbucketToken() answered the wrong entry: %q", e.Value())
	}
	// A document without the entry is found=false and never an empty
	// credential, which is the state the service refuses on rather than
	// falling back to a token the job holds (D143).
	plain, err := ParseBundle([]byte(config.ClaudeOAuthToken + ": " + fakeCred(t, "default") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := plain.BitbucketToken(); ok || e.Value() != "" {
		t.Errorf("BitbucketToken() on a document without the entry = (%+v, %v), want nothing", e, ok)
	}
}

// TestBundleModelAccountNamedBitbucketTokenRefused: an account spelled
// claude-oauth-token-bitbucket-token resolves to the name the git entry's
// document key is, and that name is the one the platform reads as the
// upstream Bitbucket token elsewhere (config.BitbucketSecret is a
// ReservedSecret, the policy's block names it, and the proxy's audit line
// names the credential it used). A model account resolving to it would leave
// one name meaning two credentials, so the key is refused rather than loaded
// under a name that shadows the kind.
func TestBundleModelAccountNamedBitbucketTokenRefused(t *testing.T) {
	key := config.ClaudeAccountSecret(config.BitbucketSecret)
	if _, _, _, ok := CredentialKey(key); ok {
		t.Fatalf("CredentialKey(%q) accepted a name that shadows the git kind", key)
	}
	if _, err := ParseBundle([]byte(key + ": " + fakeCred(t, "x") + "\n")); !errors.Is(err, ErrBundleUnknownKey) {
		t.Fatalf("ParseBundle with %q = %v, want ErrBundleUnknownKey", key, err)
	}
	// The near misses stay legal: a name that merely starts with it is a name
	// of its own, and the git entry itself is a different key.
	if _, _, _, ok := CredentialKey(config.ClaudeAccountSecret(config.BitbucketSecret + "-x")); !ok {
		t.Errorf("CredentialKey refused %q, which is a name of its own",
			config.ClaudeAccountSecret(config.BitbucketSecret+"-x"))
	}
	if _, kind, _, ok := CredentialKey(config.BitbucketSecret); !ok || kind != KindGit {
		t.Errorf("CredentialKey(%q) = kind %q, %v; want the git kind", config.BitbucketSecret, kind, ok)
	}
}

// TestBundleOversizeRefused: a document over the bound is refused BEFORE it
// is parsed — the bound is a constant and not a policy field, and an
// oversize document is a mistake or an attack. A value over its own bound is
// refused too.
func TestBundleOversizeRefused(t *testing.T) {
	big := make([]byte, 0, MaxBundleBytes+2)
	big = append(big, (config.AnthropicSecret + ": " + fakeCred(t, "x") + "\n")...)
	for len(big) <= MaxBundleBytes {
		// A comment line, so the document is oversize without holding more
		// credentials than the count bound allows.
		big = append(big, "# padding\n"...)
	}
	if _, err := ParseBundle(big); !errors.Is(err, ErrBundleTooLarge) {
		t.Fatalf("a %d-byte document = %v, want ErrBundleTooLarge", len(big), err)
	}
	long := config.AnthropicSecret + ": " + strings.Repeat("x", MaxBundleValueBytes+1) + "\n"
	if _, err := ParseBundle([]byte(long)); !errors.Is(err, ErrBundleBadValue) {
		t.Fatalf("a value over the bound = %v, want ErrBundleBadValue", err)
	}
	// The bound is on the credentials, not only on the bytes: the wire
	// contract can report maxTriedEntries of them (C1.10).
	var many strings.Builder
	for i := 0; i <= MaxBundleCredentials; i++ {
		fmt.Fprintf(&many, "claude-oauth-token-a%03d: %s\n", i, fakeCred(t, "x"))
	}
	if _, err := ParseBundle([]byte(many.String())); !errors.Is(err, ErrBundleTooMany) {
		t.Fatalf("%d credentials = %v, want ErrBundleTooMany", MaxBundleCredentials+1, err)
	}
}

// TestBundleMissingOrUnparseableFailsClosed: a missing secret, an empty
// document and an unparseable body are failures and not empty sets. The
// service issues nothing for any of them — never a fallback, never an empty
// credential list, never the job's own key (D143) — and the refusal names
// what is wrong.
func TestBundleMissingOrUnparseableFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
		want error
	}{
		{"an absent container", nil, ErrBundleEmpty},
		{"an empty document", []byte(""), ErrBundleEmpty},
		{"a document that is null", []byte("null\n"), ErrBundleEmpty},
		{"an empty mapping", []byte("{}\n"), ErrBundleEmpty},
		{"a document that is a list", []byte("- anthropic-api-key\n"), ErrBundleUnparseable},
		{"a document that is a string", []byte("anthropic-api-key\n"), ErrBundleUnparseable},
		{"a document that does not parse", []byte("anthropic-api-key: [unclosed\n"), ErrBundleUnparseable},
		{"a value that is not a string", []byte(config.AnthropicSecret + ": 1234\n"), ErrBundleBadValue},
		{"a value that is empty", []byte(config.AnthropicSecret + ": \"\"\n"), ErrBundleBadValue},
		{"a value that is a nested map", []byte(config.AnthropicSecret + ":\n  a: b\n"), ErrBundleBadValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ParseBundle(tc.raw)
			if err == nil || b != nil {
				t.Fatalf("ParseBundle(%q) = %v, %v; want a refusal and no bundle", tc.raw, b, err)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("ParseBundle(%q) = %v, want %v", tc.raw, err, tc.want)
			}
		})
	}
	// The loader adds the source's own two refusals, and neither of them is
	// an empty bundle.
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"the container is absent", ErrBundleAbsent, ErrBundleAbsent},
		{"the container cannot be read", ErrBundleUnreadable, ErrBundleUnreadable},
		{"the container's payload is not a document", errors.New("boom"), nil},
	} {
		t.Run("source: "+tc.name, func(t *testing.T) {
			src := &scriptedSource{errs: []error{tc.err}}
			l := &BundleLoader{Source: src}
			b, err := l.Load(context.Background())
			if err == nil || b != nil {
				t.Fatalf("Load = %v, %v; want a refusal and no bundle", b, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("Load = %v, want %v", err, tc.want)
			}
			// A failure is not cached: the next call reads the container
			// again rather than serving the failure (or a document).
			if _, err := l.Load(context.Background()); err == nil {
				t.Error("the second Load succeeded over a failing source")
			}
			if n := src.readCount(); n != 2 {
				t.Errorf("a failing load was cached: %d reads, want 2", n)
			}
		})
	}
	// And a loader with no source refuses rather than answering nothing.
	l := &BundleLoader{}
	if b, err := l.Load(context.Background()); err == nil || b != nil {
		t.Fatalf("a loader with no source answered %v, %v", b, err)
	}
}

// TestBundleValueNeverInErrorOrLog: no credential value reaches an error, a
// log line or a rendering of the bundle. The refusal may name the KEY it is
// about — that is how an operator finds a typo — and never the value.
func TestBundleValueNeverInErrorOrLog(t *testing.T) {
	value := fakeCred(t, "must-not-appear")
	// Every document here is refused, and each refusal is one an operator
	// reads: the value must not be in any of them.
	docs := []string{
		"anthropic-api-key: " + value + "\n" + "claude-oauth-token-9bad: x\n",
		"anthropic-api-key: " + value + "\n" + config.AnthropicSecret + ": " + value + "\n",
		"anthropic-api-key: [" + value + "\n",
		"anthropic-api-key: " + value + "\n" + strings.Repeat("# pad\n", MaxBundleBytes/6+10),
	}
	for i, doc := range docs {
		b, err := ParseBundle([]byte(doc))
		if err == nil {
			t.Fatalf("document %d was accepted: %v", i, b)
		}
		if strings.Contains(err.Error(), value) {
			t.Errorf("document %d: the refusal carries the value: %v", i, err)
		}
	}
	// A bundle that parsed renders without its values, through every
	// formatting verb a log line, an error or a debug print uses.
	b, err := ParseBundle(bundleDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Info("the bundle", "bundle", b, "entries", b.Entries())
	for _, e := range b.Entries() {
		// The Entry itself carries the value in an unexported field: what
		// must not reach a log line is a rendering of it.
		if s := fmt.Sprintf("%v", e); strings.Contains(s, e.Value()) {
			t.Errorf("an entry renders its value: %s", s)
		}
	}
	rendered := []string{
		fmt.Sprintf("%v", b),
		fmt.Sprintf("%+v", b),
		fmt.Sprintf("%#v", b),
		fmt.Sprintf("%s", b),
		buf.String(),
	}
	for _, s := range rendered {
		if strings.Contains(s, fakeCred(t, "default")) || strings.Contains(s, fakeCred(t, "paid")) ||
			strings.Contains(s, fakeCred(t, "appkey")) {
			t.Errorf("a rendering of the bundle carries a credential: %s", s)
		}
	}
	if !strings.Contains(rendered[0], config.DefaultClaudeAccount) {
		t.Errorf("%%v = %q, want the credential names (it is what an operator reads)", rendered[0])
	}
}

// TestBundleRefusalNamesTheClassAndNeverTheKey: the loader's own error names
// the key it is about, because the person who hand-edits the document is the
// person who fixes it. The service relays its refusal to a RUN, and a run is
// not that person: the key is up to 80 bytes of a document an operator edited
// and can be a VALUE pasted where a name belongs, so what reaches the run is
// the class and never the text.
//
// The control is the same error read the other way. BundleError.Error() does
// carry the key — that is what C1.25's operator commands show — so this is a
// deliberate difference between the two readers and not a message that was
// lost on the way out.
//
// Mutation (run, restore): answer err.Error() instead of the class. The first
// row is red with the pasted value in the refusal, and the source-failure row
// is red with Secret Manager's own sentence about the container in it.
func TestBundleRefusalNamesTheClassAndNeverTheKey(t *testing.T) {
	// A key that is a credential value pasted where a name belongs: exactly the
	// hand-edit the loader clips its key for, and the case that makes the
	// difference between the two readers matter. It is built from the test's
	// name, so the file holds no secret-shaped literal (Convention 2), and it
	// is shorter than the loader's 80-byte clip so the control can find it
	// whole.
	pasted := "sk-ant-" + strings.ReplaceAll(t.Name(), "/", "-")
	value := fakeCred(t, "x")
	keyed, err := ParseBundle([]byte(pasted + ": " + value + "\n"))
	if keyed != nil || !errors.Is(err, ErrBundleUnknownKey) {
		t.Fatalf("a document keyed by a value = %v, %v; want ErrBundleUnknownKey", keyed, err)
	}
	if !strings.Contains(err.Error(), pasted) {
		t.Fatalf("the loader's own error does not name the key it is about: %v", err)
	}
	// The same for the source's failures, which are another package's sentence
	// and name the container and the request rather than a key.
	sourceErr := errors.New("secretmanager: permission denied on projects/aurora/secrets/creds-bundle/versions/latest")

	cases := []struct {
		name  string
		err   error
		want  error
		notIn []string
	}{
		{
			name:  "a key that is not a credential name",
			err:   err,
			want:  ErrBundleUnknownKey,
			notIn: []string{pasted, value},
		},
		{
			name:  "a key that is a legitimate name",
			err:   bundleErr(ErrBundleBadValue, config.ClaudeOAuthTokenPrefix+testAccount),
			want:  ErrBundleBadValue,
			notIn: []string{config.ClaudeOAuthTokenPrefix + testAccount},
		},
		{
			name: "an absent container",
			err:  ErrBundleAbsent,
			want: ErrBundleAbsent,
		},
		{
			name: "an oversize document, wrapped",
			err:  fmt.Errorf("loading the bundle: %w", ErrBundleTooLarge),
			want: ErrBundleTooLarge,
		},
		{
			name:  "a source that could not be read",
			err:   sourceErr,
			want:  ErrBundleUnreadable,
			notIn: []string{"secretmanager", "permission denied", "projects/aurora", "creds-bundle"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BundleRefusal(tc.err)
			if got != tc.want.Error() {
				t.Errorf("BundleRefusal(%v) = %q, want the class %q", tc.err, got, tc.want)
			}
			for _, s := range tc.notIn {
				if strings.Contains(got, s) {
					t.Errorf("the refusal carries %q, which a run is not told:\n%s", s, got)
				}
			}
		})
	}
}

// TestBundleSecretIDIsTheInstallationsContainer: the name the loader derives
// and the name `fugaro update`'s stage fills into
// `credentials_service.bundle_secret` are the same string. The stage
// (internal/infra's Credentials) writes gcp.ProjectSecretID of the literal
// "creds-bundle" — internal/infra's own test pins that side — and this pins
// the loader's side against the same literal, so a rename on either side
// fails a test rather than leaving the service reading a container nobody
// created.
func TestBundleSecretIDIsTheInstallationsContainer(t *testing.T) {
	const project = "aurora"
	want := gcp.ProjectSecretID(project, "creds-bundle")
	if got := BundleSecretID(project); got != want {
		t.Errorf("BundleSecretID(%q) = %q, want %q (internal/infra.CredentialsBundleName)", project, got, want)
	}
	if got := gcp.CredsBundleSecretID(project); got != want {
		t.Errorf("gcp.CredsBundleSecretID(%q) = %q, want %q", project, got, want)
	}
}

// TestBundleSchemaMatchesTheLoader: the published schema
// (schemas/creds-bundle.schema.json) and the loader accept and refuse the
// same documents. Neither can be widened alone: a key the loader takes and
// the schema rejects (or the other way round) is one of the two lying about
// what the service reads.
func TestBundleSchemaMatchesTheLoader(t *testing.T) {
	data, err := readSchemaFile(t, "../../schemas/creds-bundle.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("the schema has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	validates := func(t *testing.T, y string) bool {
		t.Helper()
		var v any
		if err := yaml.Unmarshal([]byte(y), &v); err != nil {
			t.Fatalf("the test document is not YAML: %v", err)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst) == nil
	}
	var many strings.Builder
	for i := 0; i < MaxBundleCredentials; i++ {
		fmt.Fprintf(&many, "claude-oauth-token-a%03d: %s\n", i, fakeCred(t, "x"))
	}
	var tooMany strings.Builder
	for i := 0; i <= MaxBundleCredentials; i++ {
		fmt.Fprintf(&tooMany, "claude-oauth-token-a%03d: %s\n", i, fakeCred(t, "x"))
	}
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"every credential the platform defines", string(bundleDoc(t))},
		{"the paid key alone", config.AnthropicSecret + ": " + fakeCred(t, "x") + "\n"},
		{"the app key alone", config.GitHubAppKeySecret + ": " + fakeCred(t, "x") + "\n"},
		// The App key AND an account whose resolved name is the same string:
		// two different credentials that share a name, both legal, and the
		// schema admits both — so the loader has to as well. It is the case a
		// duplicate check keyed on the resolved name gets wrong (it would
		// refuse a valid document with ErrBundleUnknownKey, the loader
		// disagreeing with the schema it publishes).
		{"the app key and an account named after it", config.GitHubAppKeySecret + ": " + fakeCred(t, "x") + "\n" +
			config.ClaudeAccountSecret(config.GitHubAppKeySecret) + ": " + fakeCred(t, "y") + "\n"},
		{"the app key with a credential-shaped key", "github-app-key-pem: " + fakeCred(t, "x") + "\n"},
		{"a key that is nearly the app key", "github-app-keys: " + fakeCred(t, "x") + "\n"},
		{"the upstream git credential", config.BitbucketSecret + ": " + fakeCred(t, "x") + "\n"},
		{"a key that is nearly the git credential", "bitbucket-tokens: " + fakeCred(t, "x") + "\n"},
		// The account family's one reserved spelling: it resolves to the git
		// credential's own key, so the loader refuses it, and the schema has
		// to refuse it too even though the pattern alone admits it.
		{"an account named like the git credential", config.ClaudeAccountSecret(config.BitbucketSecret) + ": " + fakeCred(t, "x") + "\n"},
		{"an account that merely starts like it", config.ClaudeAccountSecret(config.BitbucketSecret+"-x") + ": " + fakeCred(t, "x") + "\n"},
		{"the exact count bound", many.String()},
		{"an unknown key", "antropic-api-key: " + fakeCred(t, "x") + "\n"},
		{"an account name outside the rule", "claude-oauth-token-9alice: " + fakeCred(t, "x") + "\n"},
		{"an over-long account name", "claude-oauth-token-" + strings.Repeat("a", 32) + ": " + fakeCred(t, "x") + "\n"},
		// The one the regex alone admits: 30 repetitions of "-a" consume two
		// characters each, so this suffix is 61 bytes and still matches
		// config.ClaudeAccountNameRE. Only the separate
		// MaxClaudeAccountNameLen bound refuses it, and the loader and the
		// schema have to refuse it together.
		{"a dash-heavy account name over the length bound", "claude-oauth-token-a" + strings.Repeat("-a", 30) + ": " + fakeCred(t, "x") + "\n"},
		{"an account name at the length bound", "claude-oauth-token-" + strings.Repeat("a", 31) + ": " + fakeCred(t, "x") + "\n"},
		{"a value that is a number", config.AnthropicSecret + ": 1234\n"},
		{"a value that is empty", config.AnthropicSecret + ": \"\"\n"},
		{"a value over the bound", config.AnthropicSecret + ": " + strings.Repeat("x", MaxBundleValueBytes+1) + "\n"},
		{"one credential over the count bound", tooMany.String()},
		{"an empty document", "{}\n"},
		{"a document that is a list", "- a\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBundle([]byte(tc.doc))
			loaderOK := err == nil
			schemaOK := validates(t, tc.doc)
			if loaderOK != schemaOK {
				t.Errorf("the loader says ok=%v and the schema says ok=%v for:\n%s", loaderOK, schemaOK, tc.doc)
			}
		})
	}
}

// TestBundleCredentialKeyIsTheCascadesSpelling: the document's key is the
// secret's logical name and the credential's name is the account's, which is
// what the policy's accounts block, the marks and the wire contract speak.
//
// It is also where the exclusions are pinned from the document side: the App
// key (C2.1) and the upstream Bitbucket token (D506) are loaded, each is
// reachable through its own kind-based accessor, and each is in NEITHER
// Credentials (what the cascade is offered) nor a candidate — a bundle that
// holds them holds two credentials fewer than it holds entries.
func TestBundleCredentialKeyIsTheCascadesSpelling(t *testing.T) {
	b, err := ParseBundle(bundleDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []creds.Credential{
		{Name: config.DefaultClaudeAccount, Tier: creds.TierA},
		{Name: "alice", Tier: creds.TierA},
		{Name: config.AnthropicSecret, Tier: creds.TierB},
	}
	got := b.Credentials()
	if len(got) != len(want) {
		t.Fatalf("Credentials() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Credentials()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	for _, tc := range []struct{ name, key string }{
		{config.DefaultClaudeAccount, config.ClaudeOAuthToken},
		{"alice", config.ClaudeAccountSecret("alice")},
		{config.AnthropicSecret, config.AnthropicSecret},
	} {
		e, ok := b.Lookup(tc.name)
		if !ok {
			t.Fatalf("Lookup(%q) found nothing", tc.name)
		}
		if e.Key != tc.key {
			t.Errorf("Lookup(%q).Key = %q, want %q", tc.name, e.Key, tc.key)
		}
		if e.Value() == "" {
			t.Errorf("Lookup(%q) has no value", tc.name)
		}
	}
	if _, ok := b.Lookup("nobody"); ok {
		t.Error("Lookup found a credential the document does not hold")
	}
	// The App key: in the document, findable by its KIND, and not a model
	// credential. Lookup is the model family's accessor and does not answer it,
	// because "github-app-key" is a name a model account may also carry.
	app, ok := b.AppKey()
	if !ok {
		t.Fatalf("AppKey() found nothing, and the document holds %q", config.GitHubAppKeySecret)
	}
	if app.Kind != KindGitHubApp || app.Tier != "" || app.Key != config.GitHubAppKeySecret {
		t.Errorf("the App key's entry = %+v, want the github-app kind, no tier, keyed %q", app, config.GitHubAppKeySecret)
	}
	if app.Value() == "" {
		t.Error("the App key's entry has no value")
	}
	if _, ok := b.Lookup(config.GitHubAppKeySecret); ok {
		t.Errorf("Lookup(%q) answered, and it answers model credentials only", config.GitHubAppKeySecret)
	}
	for _, c := range got {
		if c.Name == config.GitHubAppKeySecret {
			t.Errorf("Credentials() offers the App key (%v) to the cascade", c)
		}
	}
	// The upstream Bitbucket token, the same way: in the document, findable by
	// its KIND, not a model credential, and not a candidate.
	git, ok := b.BitbucketToken()
	if !ok {
		t.Fatalf("BitbucketToken() found nothing, and the document holds %q", config.BitbucketSecret)
	}
	if git.Kind != KindGit || git.Tier != "" || git.Key != config.BitbucketSecret {
		t.Errorf("the git entry = %+v, want the git kind, no tier, keyed %q", git, config.BitbucketSecret)
	}
	if git.Value() == "" {
		t.Error("the git entry has no value")
	}
	if _, ok := b.Lookup(config.BitbucketSecret); ok {
		t.Errorf("Lookup(%q) answered, and it answers model credentials only", config.BitbucketSecret)
	}
	for _, c := range got {
		if c.Name == config.BitbucketSecret {
			t.Errorf("Credentials() offers the git token (%v) to the cascade", c)
		}
	}
	if want, got := b.Len(), len(got); want != got+2 {
		t.Errorf("the document holds %d entries and the cascade is offered %d, want two kind-only credentials", want, got)
	}
}

// TestBundleCredentialKeyHoldsTheAccountNameRuleInBothHalves: CredentialKey is
// the one place the document's spelling is turned into the cascade's, so the
// account-name rule it applies is the rule the rest of the platform applies —
// and that rule is two halves, not one. config.ClaudeAccountNameRE alone
// admits a name of up to 61 bytes (each of its {0,30} repetitions may consume
// one character or, via the dash alternative, two), while
// config.ValidateClaudeAccounts refuses anything over
// config.MaxClaudeAccountNameLen. A key that only the regex admits is a
// credential the config path can never produce, so it would load and then be
// unreachable: no policy could name it and no writer could rotate it.
//
// The last two rows are the reservation on the other side: a suffix that is
// the reserved DefaultClaudeAccount is accepted, and it names the default
// account, whose own key is the unsuffixed one. That is a spelling no writer
// produces either, but unlike an over-long name it names an account a policy
// CAN admit, so it is a live credential rather than a dead one; a document
// holding both spellings is refused as a duplicate name by ParseBundle.
func TestBundleCredentialKeyHoldsTheAccountNameRuleInBothHalves(t *testing.T) {
	for _, tc := range []struct {
		name     string
		key      string
		wantName string
		wantKind Kind
		wantTier creds.Tier
		wantOK   bool
	}{
		{name: "the paid key", key: config.AnthropicSecret, wantName: config.AnthropicSecret, wantKind: KindModel, wantTier: creds.TierB, wantOK: true},
		{name: "the default account", key: config.ClaudeOAuthToken, wantName: config.DefaultClaudeAccount, wantKind: KindModel, wantTier: creds.TierA, wantOK: true},
		{name: "a named account", key: config.ClaudeAccountSecret("alice"), wantName: "alice", wantKind: KindModel, wantTier: creds.TierA, wantOK: true},
		{name: "a dash inside a name", key: config.ClaudeAccountSecret("acme-personal"), wantName: "acme-personal", wantKind: KindModel, wantTier: creds.TierA, wantOK: true},
		{name: "the longest name the rule admits", key: config.ClaudeAccountSecret(strings.Repeat("a", config.MaxClaudeAccountNameLen)), wantName: strings.Repeat("a", config.MaxClaudeAccountNameLen), wantKind: KindModel, wantTier: creds.TierA, wantOK: true},
		{name: "a name one byte over the bound", key: config.ClaudeAccountSecret(strings.Repeat("a", config.MaxClaudeAccountNameLen+1)), wantOK: false},
		{name: "a dash-heavy name the regex alone admits", key: config.ClaudeAccountSecret("a" + strings.Repeat("-a", 30)), wantOK: false},
		{name: "a name starting with a digit", key: config.ClaudeAccountSecret("9alice"), wantOK: false},
		{name: "a name ending in a dash", key: config.ClaudeAccountSecret("alice-"), wantOK: false},
		{name: "an upper-case name", key: config.ClaudeAccountSecret("Alice"), wantOK: false},
		{name: "an empty suffix", key: config.ClaudeOAuthTokenPrefix, wantOK: false},
		{name: "the reserved name, spelled the long way", key: config.ClaudeAccountSecret(config.DefaultClaudeAccount), wantName: config.DefaultClaudeAccount, wantKind: KindModel, wantTier: creds.TierA, wantOK: true},
		{name: "a name outside both families", key: "anthropic-oauth-token", wantOK: false},
		// The App key is the one key that is not a model credential: it has a
		// name the mint and the audit line speak, and no tier at all.
		{name: "the app key", key: config.GitHubAppKeySecret, wantName: config.GitHubAppKeySecret, wantKind: KindGitHubApp, wantTier: "", wantOK: true},
		{name: "the app key under the account prefix", key: config.ClaudeAccountSecret(config.GitHubAppKeySecret), wantName: config.GitHubAppKeySecret, wantKind: KindModel, wantTier: creds.TierA, wantOK: true},
		{name: "an app key that is not the app key", key: "github-app-private-key", wantOK: false},
		// The upstream Bitbucket token: the second key that is not a model
		// credential — a kind of its own, no tier.
		{name: "the upstream git token", key: config.BitbucketSecret, wantName: config.BitbucketSecret, wantKind: KindGit, wantTier: "", wantOK: true},
		{name: "a git token that is not the git token", key: "bitbucket-tokens", wantOK: false},
		// The one account name the family may not take: it resolves to the
		// git entry's own key, so the name would mean two credentials.
		{name: "an account named like the git token", key: config.ClaudeAccountSecret(config.BitbucketSecret), wantOK: false},
		{name: "an account that merely starts like it", key: config.ClaudeAccountSecret(config.BitbucketSecret + "-x"), wantName: config.BitbucketSecret + "-x", wantKind: KindModel, wantTier: creds.TierA, wantOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotKind, gotTier, ok := CredentialKey(tc.key)
			if ok != tc.wantOK {
				t.Fatalf("CredentialKey(%q) ok = %v, want %v", tc.key, ok, tc.wantOK)
			}
			if !ok {
				if gotName != "" || gotKind != "" || gotTier != "" {
					t.Errorf("CredentialKey(%q) = (%q, %q, %q) for a key it refused, want empty", tc.key, gotName, gotKind, gotTier)
				}
				return
			}
			if gotName != tc.wantName {
				t.Errorf("CredentialKey(%q) name = %q, want %q", tc.key, gotName, tc.wantName)
			}
			if gotKind != tc.wantKind {
				t.Errorf("CredentialKey(%q) kind = %q, want %q", tc.key, gotKind, tc.wantKind)
			}
			if gotTier != tc.wantTier {
				t.Errorf("CredentialKey(%q) tier = %q, want %q", tc.key, gotTier, tc.wantTier)
			}
		})
	}
}

// TestReservedAccountNamesAgreeWithTheBundle: the account names the local
// config refuses and the account names this bundle refuses are ONE set, and
// this test reads both ends: CredentialKey on the one side,
// config.ValidClaudeAccountRef and config.ValidateClaudeAccounts on the other,
// over every name a credential of the platform goes by. The collision is one
// question seen from either end, because an account's key in the bundle is
// config.ClaudeAccountSecret(name) and CredentialKey resolves that key back to
// the bare name — the name the cascade, the policy's accounts block and the
// proxy's audit line all speak — so an account taking a name another credential
// of the platform already goes by would leave one name meaning two credentials.
//
// The rows are the repository's answer to which names those are, and each is
// true on both sides: bitbucket-token is reserved (the bundle's resolver refuses
// its key unconditionally, and the published schema's `not` pins the same one
// spelling), while the App key's name, the paid key's name and the default
// token's secret name are legal account names — the bundle holds the App key
// beside an account named after it (Bundle.byName), refuses a document that
// gives one model name to two credentials rather than the account's name, and
// speaks the default account's own entry as config.DefaultClaudeAccount.
//
// Red if either end moves alone: reserving another name in the config without
// the bundle refusing its key fails the row, and dropping the bundle's refusal
// fails the ParseBundle assertion below.
func TestReservedAccountNamesAgreeWithTheBundle(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reserved bool
		why      string
	}{
		{config.BitbucketSecret, true, "the upstream git credential: CredentialKey refuses the account key, the document key and the job's mount all this one name"},
		{config.GitHubAppKeySecret, false, "the App key's name: Bundle.byName holds the App key beside an account named after it"},
		{config.AnthropicSecret, false, "the paid key's name: the bundle refuses a document holding both, not the account's name"},
		{config.ClaudeOAuthToken, false, "the default token's secret: the bundle speaks its entry as config.DefaultClaudeAccount"},
		{config.CodexOAuthToken, false, "a harness name nothing mounts or holds yet"},
		{config.CredentialsBundleSecret, false, "the service's own container, which no job mounts and no bundle entry names"},
		{"acme-personal", false, "an ordinary name"},
	} {
		key := config.ClaudeAccountSecret(tc.name)
		_, _, _, admitted := CredentialKey(key)
		bundleReserved := !admitted
		refByName := config.ValidClaudeAccountRef(tc.name) != ""
		ps := config.ValidateClaudeAccounts(map[string]config.ClaudeAccount{tc.name: {}})
		refByKey := len(ps) == 1 && ps[0].Message == reservedAccountMessage()
		if bundleReserved != tc.reserved || refByName != tc.reserved || refByKey != tc.reserved {
			t.Errorf("%s: %q — the bundle refuses its account key %q: %v, a profile's reference is refused: %v, a claude_accounts key is refused: %v (%+v); all three must say reserved = %v",
				tc.why, tc.name, key, bundleReserved, refByName, refByKey, ps, tc.reserved)
		}
	}

	// The collision itself, on a document: the account's key beside the
	// credential it would be named after. The loader refuses the pair, which is
	// what makes the name unrepresentable rather than merely confusing.
	git := config.ClaudeAccountSecret(config.BitbucketSecret) + ": " + fakeCred(t, "the-account") + "\n" +
		config.BitbucketSecret + ": " + fakeCred(t, "the-git-token") + "\n"
	if _, err := ParseBundle([]byte(git)); !errors.Is(err, ErrBundleUnknownKey) {
		t.Errorf("ParseBundle(%q) = %v, want ErrBundleUnknownKey: the two names are one", git, err)
	}
	// And the pair the bundle DOES hold: an account named after the App key is
	// legal here, which is why the config leaves that name alone.
	appKey := config.GitHubAppKeySecret + ": " + fakeCred(t, "the-app-key") + "\n" +
		config.ClaudeAccountSecret(config.GitHubAppKeySecret) + ": " + fakeCred(t, "the-account") + "\n"
	if _, err := ParseBundle([]byte(appKey)); err != nil {
		t.Errorf("ParseBundle(%q) = %v, want an account named after the App key to stay legal: the config refuses only the one spelling the bundle refuses", appKey, err)
	}

	// The one name the two halves part on deliberately, and the only name the
	// definition refuses while the resolver admits its key: the default account,
	// which every repository already has and which a profile may therefore name.
	defaultKey := config.ClaudeAccountSecret(config.DefaultClaudeAccount)
	if _, _, _, ok := CredentialKey(defaultKey); !ok {
		t.Errorf("CredentialKey(%q) refused the default account's key", defaultKey)
	}
	if msg := config.ValidClaudeAccountRef(config.DefaultClaudeAccount); msg != "" {
		t.Errorf("ValidClaudeAccountRef(%q) = %q, want the default account to stay a valid reference", config.DefaultClaudeAccount, msg)
	}
	if ps := config.ValidateClaudeAccounts(map[string]config.ClaudeAccount{config.DefaultClaudeAccount: {}}); len(ps) != 1 {
		t.Errorf("ValidateClaudeAccounts(%q) = %+v, want the default account refused as a definition", config.DefaultClaudeAccount, ps)
	}
}

// reservedAccountMessage is the sentence the config answers a reserved name
// with, read through the validator rather than spelled here: the two ends of
// the rule must use one sentence, and a test that repeated it could not tell
// that they stopped.
func reservedAccountMessage() string { return config.ValidClaudeAccountRef(config.BitbucketSecret) }
