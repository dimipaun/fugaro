package credsvc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// readConsumerEntry is the read consumer's document key, spelled as the
// operator types it (`fugaro creds set bitbucket-read-consumer`) and as the
// release notes speak it. It is a literal on purpose: the tests below hold the
// constant to it, so a rename of either cannot quietly move the credential's
// home in the bundle.
const readConsumerEntry = "bitbucket-read-consumer"

// readConsumerDoc is a document holding the read consumer: the OAuth consumer
// the credentials service exchanges for a read-scoped token (F21, D361). Its
// three fields are fixture values, built at run time (Convention 2).
func readConsumerDoc(t *testing.T) []byte {
	t.Helper()
	return []byte(readConsumerEntry + ":\n" +
		"  key: " + fakeCred(t, "consumer-key") + "\n" +
		"  secret: " + fakeCred(t, "consumer-secret") + "\n" +
		"  workspace: " + fakeCred(t, "workspace") + "\n")
}

// TestBundleBitbucketReadConsumer: the read consumer is a credential of the
// bundle, holding the consumer's key, its secret and the workspace the service
// mints for (F21, BB2). It is the first entry whose value is not one string —
// it is a mapping of three fields — so the loader reads it as one, and the
// accessor is the only thing that hands its fields out.
//
// It is also where the entry's two exclusions are pinned: it is in NEITHER
// Credentials (what the cascade is offered) nor Lookup (which answers model
// credentials), so the consumer can never become a candidate of a model call.
func TestBundleBitbucketReadConsumer(t *testing.T) {
	if BitbucketReadConsumerSecret != readConsumerEntry {
		t.Fatalf("BitbucketReadConsumerSecret = %q, want %q", BitbucketReadConsumerSecret, readConsumerEntry)
	}
	doc := readConsumerDoc(t)
	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatalf("ParseBundle(%q) = %v; the read consumer is a credential the service holds", doc, err)
	}
	key, secret, workspace, ok := b.BitbucketReadConsumer()
	if !ok {
		t.Fatal("BitbucketReadConsumer found nothing, and the document holds one")
	}
	if want := fakeCred(t, "consumer-key"); key != want {
		t.Errorf("the consumer's key = %q, want the field the document holds", key)
	}
	if want := fakeCred(t, "consumer-secret"); secret != want {
		t.Errorf("the consumer's secret = %q, want the field the document holds", secret)
	}
	if want := fakeCred(t, "workspace"); workspace != want {
		t.Errorf("the consumer's workspace = %q, want the field the document holds", workspace)
	}
	if b.Len() != 1 {
		t.Errorf("the document holds %d entries, want 1", b.Len())
	}
	e, ok := b.Lookup(readConsumerEntry)
	if ok {
		t.Errorf("Lookup answered the read consumer as a model credential: %+v", e)
	}
	if got := b.Credentials(); len(got) != 0 {
		t.Errorf("Credentials() = %v, want nothing: the read consumer is no model credential", got)
	}
	// Beside a model credential, the entry changes nothing the cascade is
	// offered: the model credential alone is a candidate.
	both := append(readConsumerDoc(t), []byte(config.ClaudeOAuthToken+": "+fakeCred(t, "default")+"\n")...)
	bb, err := ParseBundle(both)
	if err != nil {
		t.Fatalf("ParseBundle with the consumer beside an account = %v", err)
	}
	if got := bb.Credentials(); len(got) != 1 || got[0] != (creds.Credential{Name: config.DefaultClaudeAccount, Tier: creds.TierA}) {
		t.Errorf("Credentials() = %v, want the model credential alone", got)
	}
	if _, _, _, ok := bb.BitbucketReadConsumer(); !ok {
		t.Error("the read consumer is not reachable beside a model credential")
	}
}

// TestScrubRemovesTheReadConsumerKeyAndSecret: the read consumer is the one
// credential of the bundle whose material is more than the entry's value. The
// secret is the value (so every path that walks entries already covers it) and
// the OAuth client key sits beside it, in the same entry and in no value at
// all — so a scrub that walks values alone leaves the key readable in an audit
// line, a log line or an error, and the key is credential material: it is what
// the read mint names itself to Bitbucket with.
//
// The workspace is the control. It is a name and not a credential (the read
// mint refuses a repository of another workspace BY NAME, G11), so it stays
// readable: a scrub that redacted it would be redacting the words of ordinary
// prose.
//
// Mutation (run, restore): drop the key from what the scrub walks, and the
// line below comes back carrying it.
func TestScrubRemovesTheReadConsumerKeyAndSecret(t *testing.T) {
	key, secret, workspace := fakeCred(t, "consumer-key"), fakeCred(t, "consumer-secret"), fakeCred(t, "consumer-workspace")
	doc := []byte(readConsumerEntry + ":\n  key: " + key + "\n  secret: " + secret + "\n  workspace: " + workspace + "\n")
	l := &BundleLoader{Source: &scriptedSource{docs: [][]byte{doc}}}
	if _, err := l.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := l.Scrub("credential=" + key + " secret=" + secret + " workspace=" + workspace)
	for _, v := range []string{key, secret} {
		if strings.Contains(got, v) {
			t.Errorf("the scrub left the consumer's material in %q", got)
		}
	}
	if !strings.Contains(got, workspace) {
		t.Errorf("the scrub redacted the workspace, which is a name: %q", got)
	}
}

// TestBundleRefusesRunConsumerUntilS3: the second consumer (F21) is PARKED
// with S3, and the parked kind is not a credential of the bundle: the loader
// refuses a document that holds it, whole, as a key it does not know. It is
// the property the release notes lean on — a `bitbucket-run-consumer` entry
// written by hand halts every model call and every GitHub mint, which is why
// nobody may write one — and it is pinned rather than left to the day the run
// kind lands.
func TestBundleRefusesRunConsumerUntilS3(t *testing.T) {
	doc := []byte("bitbucket-run-consumer:\n" +
		"  key: " + fakeCred(t, "run-key") + "\n" +
		"  secret: " + fakeCred(t, "run-secret") + "\n" +
		"  workspace: " + fakeCred(t, "run-workspace") + "\n" +
		"  repositories: [" + fakeCred(t, "repo") + "]\n")
	if b, err := ParseBundle(doc); !errors.Is(err, ErrBundleUnknownKey) {
		t.Fatalf("ParseBundle of the parked run consumer = %v, %v; want ErrBundleUnknownKey", b, err)
	}
	// The control: the read kind beside it is a credential, so the refusal is
	// about the run kind and not about the shape.
	if _, err := ParseBundle(readConsumerDoc(t)); err != nil {
		t.Fatalf("the read consumer is refused: %v", err)
	}
}

// TestBundleRefusesConsumerWithoutWorkspace: the read consumer's value is
// closed. Each of its three fields is required, only those three may appear,
// and each has to be a non-empty string: the workspace is what the read mint
// refuses a repository of another workspace against (G11), so an entry that
// holds none is a credential the service cannot use and must not load as one.
//
// The first row is the red step: before the kind existed the document was
// refused whole (ErrBundleUnknownKey, "a key that is not a credential name"),
// which is a refusal of the wrong thing — the KEY is a credential of the
// bundle and the FIELD is what is wrong with it.
func TestBundleRefusesConsumerWithoutWorkspace(t *testing.T) {
	key, secret, workspace := fakeCred(t, "key"), fakeCred(t, "secret"), fakeCred(t, "workspace")
	for _, tc := range []struct {
		name string
		doc  string
		want error
	}{
		{
			name: "no workspace",
			doc:  readConsumerEntry + ":\n  key: " + key + "\n  secret: " + secret + "\n",
			want: ErrBundleBadValue,
		},
		{
			name: "no secret",
			doc:  readConsumerEntry + ":\n  key: " + key + "\n  workspace: " + workspace + "\n",
			want: ErrBundleBadValue,
		},
		{
			name: "an empty field",
			doc:  readConsumerEntry + ":\n  key: " + key + "\n  secret: \"\"\n  workspace: " + workspace + "\n",
			want: ErrBundleBadValue,
		},
		{
			name: "a field that is not a string",
			doc:  readConsumerEntry + ":\n  key: " + key + "\n  secret: " + secret + "\n  workspace: 1234\n",
			want: ErrBundleBadValue,
		},
		{
			name: "a field over the per-credential bound",
			doc:  readConsumerEntry + ":\n  key: " + key + "\n  secret: " + secret + "\n  workspace: " + strings.Repeat("x", MaxBundleValueBytes+1) + "\n",
			want: ErrBundleBadValue,
		},
		{
			name: "a value that is not a mapping",
			doc:  readConsumerEntry + ": " + secret + "\n",
			want: ErrBundleBadValue,
		},
		{
			name: "an empty mapping",
			doc:  readConsumerEntry + ": {}\n",
			want: ErrBundleBadValue,
		},
		{
			name: "a field the entry does not define",
			doc:  readConsumerEntry + ":\n  key: " + key + "\n  secret: " + secret + "\n  workspace: " + workspace + "\n  repositories: [" + workspace + "]\n",
			want: ErrBundleUnknownField,
		},
		{
			name: "a misspelt field",
			doc:  readConsumerEntry + ":\n  key: " + key + "\n  secret: " + secret + "\n  workspaces: " + workspace + "\n",
			want: ErrBundleUnknownField,
		},
		{
			name: "the entry twice",
			doc:  string(readConsumerDoc(t)) + readConsumerEntry + ":\n  key: " + key + "\n  secret: " + secret + "\n  workspace: " + workspace + "\n",
			want: ErrBundleUnknownKey,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseBundle([]byte(tc.doc))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseBundle(%q) = %v, %v; want %v", tc.doc, got, err, tc.want)
			}
			// A refusal names the ENTRY and never a field's content: the field
			// a person misspells is a name, and the one they paste a secret
			// into is not.
			for _, v := range []string{key, secret, workspace} {
				if strings.Contains(err.Error(), v) {
					t.Errorf("the refusal carries a value: %v", err)
				}
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
		// The read consumer (F21) is the one credential of the document whose
		// value is not a string, so it is the one key the schema names under
		// `properties` — which is what exempts it from `additionalProperties`
		// while every other key stays a string. The rows below are the shape
		// both sides have to agree on, and the parked run consumer's is the
		// pair of refusals S3 will have to unpark together.
		{"the read consumer", string(readConsumerDoc(t))},
		{"the parked run consumer", "bitbucket-run-consumer:\n  key: " + fakeCred(t, "x") + "\n  secret: " + fakeCred(t, "y") + "\n  workspace: " + fakeCred(t, "z") + "\n"},
		{"a read consumer whose value is a string", readConsumerEntry + ": " + fakeCred(t, "x") + "\n"},
		{"a read consumer missing its workspace", readConsumerEntry + ":\n  key: " + fakeCred(t, "x") + "\n  secret: " + fakeCred(t, "y") + "\n"},
		{"a read consumer with a field it does not define", readConsumerEntry + ":\n  key: " + fakeCred(t, "x") + "\n  secret: " + fakeCred(t, "y") + "\n  workspace: " + fakeCred(t, "z") + "\n  repositories: [r]\n"},
		{"a read consumer with an empty field", readConsumerEntry + ":\n  key: " + fakeCred(t, "x") + "\n  secret: \"\"\n  workspace: " + fakeCred(t, "z") + "\n"},
		{"a read consumer with a field that is not a string", readConsumerEntry + ":\n  key: " + fakeCred(t, "x") + "\n  secret: " + fakeCred(t, "y") + "\n  workspace: 1234\n"},
		// The account family's one reserved spelling: it resolves to the git
		// credential's own key, so the loader refuses it, and the schema has
		// to refuse it too even though the pattern alone admits it.
		{"an account named like the git credential", config.ClaudeAccountSecret(config.BitbucketSecret) + ": " + fakeCred(t, "x") + "\n"},
		{"an account that merely starts like it", config.ClaudeAccountSecret(config.BitbucketSecret+"-x") + ": " + fakeCred(t, "x") + "\n"},
		// The review-signing family (D1069): one entry per account plus the
		// rotation's previous one, all of them legal keys of the document and
		// none of them a model credential. The pattern is the widened half of
		// the agreement, so a spelling the loader reads and the schema does not
		// (or the reverse) is the drift this row set exists to catch.
		{"a review-signing entry", config.ReviewSigningKeySecret("alice") + ": " + reviewKey(t, "current") + "\n"},
		{"the rotation's previous review-signing entry", config.ReviewSigningKeyPreviousSecret("alice") + ": " + reviewKey(t, "previous") + "\n"},
		{"the default account's review-signing entry", config.ReviewSigningKeySecret(config.DefaultClaudeAccount) + ": " + reviewKey(t, "default") + "\n"},
		{"both review-signing entries beside the model credential they belong to", string(reviewSigningDoc(t))},
		// The key's own shape is the ACCESSOR's refusal and not the document's
		// (Bundle.ReviewSigningKey): the schema bounds a value the way it bounds
		// every other, so a document holding a bad key is a document both sides
		// load, and the read that needs the key is the one that refuses.
		{"a review-signing entry whose value is not a key", config.ReviewSigningKeySecret("alice") + ": " + fakeCred(t, "x") + "\n"},
		// A review-signing name whose account part is not a name an account may
		// have: the loader refuses the document whole and the pattern has to
		// refuse it too.
		{"a review-signing entry whose account is outside the rule", "claude-oauth-token-9alice" + config.ReviewSigningKeySuffix + ": " + fakeCred(t, "x") + "\n"},
		// The separator is a DOUBLED dash, so the single-dash spelling is an
		// ordinary account's name — "alice-review-signing" matches
		// ClaudeAccountNameRE — and both sides read it as the model credential
		// it is. This row is what pins the one separator the family chose.
		{"an account named nearly like a review-signing entry", config.ClaudeAccountSecret("alice-review-signing") + ": " + fakeCred(t, "x") + "\n"},
		// The whole-name bound at its edge and one byte past it: the longest
		// legal review-signing name is the prefix, MaxClaudeAccountNameLen and
		// both suffixes (75), so 76 is over — and the OVER-bound account here is
		// the dash-heavy spelling the account regex alone admits, which is why
		// the account half needs its own length rule on both sides.
		{"a review-signing name at the length bound", config.ReviewSigningKeyPreviousSecret(strings.Repeat("a", config.MaxClaudeAccountNameLen)) + ": " + fakeCred(t, "x") + "\n"},
		{"a review-signing name one byte over the length bound", config.ReviewSigningKeyPreviousSecret(strings.Repeat("a", config.MaxClaudeAccountNameLen+1)) + ": " + fakeCred(t, "x") + "\n"},
		{"a dash-heavy review-signing account over the account length bound", config.ReviewSigningKeySecret("a"+strings.Repeat("-a", 30)) + ": " + fakeCred(t, "x") + "\n"},
		// The account family's own bound, read as a whole-name length: 41
		// characters of dash-heavy account are a 60-byte key that the account
		// regex admits and MaxClaudeAccountNameLen refuses, so a schema whose
		// single bound covered the review-signing family too would accept it.
		{"a dash-heavy account name the regex admits and the account length rule refuses", "claude-oauth-token-a" + strings.Repeat("-a", 20) + ": " + fakeCred(t, "x") + "\n"},
		// The same, inside the family that made the bound a single number: a
		// 33-character account is a 68-byte review-signing key, which the
		// account length rule refuses and no bound of 75 can.
		{"a review-signing name whose account is over the account length bound", config.ReviewSigningKeySecret("a"+strings.Repeat("-a", 16)) + ": " + fakeCred(t, "x") + "\n"},
		// The one reserved ACCOUNT name is legal as a review-signing entry's
		// account part, and both sides read it that way: the reservation exists
		// because that account's derived SECRET would be the git entry's own
		// key, and the review-signing name is a different name — the service can
		// never ask for its key, no account of it can be configured, and
		// refusing it here would refuse a whole document over an entry no
		// caller reads.
		{"a review-signing entry for the reserved account name", config.ReviewSigningKeySecret(config.BitbucketSecret) + ": " + fakeCred(t, "x") + "\n"},
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
		// The Bitbucket read consumer (F21): the third key that is not a model
		// credential — a kind of its own, no tier, and the one entry of the
		// document whose value is a mapping rather than a string. The run
		// consumer beside it is parked with S3, so it is no key at all.
		{name: "the read consumer", key: BitbucketReadConsumerSecret, wantName: BitbucketReadConsumerSecret, wantKind: KindBitbucketReadConsumer, wantTier: "", wantOK: true},
		{name: "a consumer that is not the read consumer", key: "bitbucket-read-consumers", wantOK: false},
		{name: "the parked run consumer", key: "bitbucket-run-consumer", wantOK: false},
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

// reviewKey is a review-signing key's value, built at run time: the entry holds
// a 32-byte key rendered as lowercase hex (D1071), and a test derives one from
// its own name so no file carries a key-shaped literal (Convention 2). Each
// seed gives a different key, which is what lets a test tell the current entry
// from the rotation's previous one.
func reviewKey(t *testing.T, what string) string {
	t.Helper()
	sum := sha256.Sum256([]byte("fugaro-test-review-signing-key-" + t.Name() + "-" + what))
	return hex.EncodeToString(sum[:])
}

// reviewSigningDoc is a document holding one account's model credential and
// both of its review-signing entries: the current key and the rotation
// window's previous one (D1069). It is its own document rather than an
// addition to bundleDoc because the family's tests read the two entries side
// by side and the canonical fixture's counts are asserted by other tests.
func reviewSigningDoc(t *testing.T) []byte {
	t.Helper()
	return []byte("claude-oauth-token-alice: " + fakeCred(t, "alice") + "\n" +
		config.ReviewSigningKeySecret("alice") + ": " + reviewKey(t, "current") + "\n" +
		config.ReviewSigningKeyPreviousSecret("alice") + ": " + reviewKey(t, "previous") + "\n")
}

// TestReviewSigningEntryIsALegalBundleKeyAndIsNoModelCredential: the
// per-account review-signing key lives in the bundle, one entry per account
// plus the rotation window's previous one (D1069), because Q6 pins the
// service's Secret Manager reach to one container and one grant (F8). It is a
// legal key of the document — the loader refuses a document whole when it
// holds a key outside its closed set, so before this change the entry was not
// a key at all — and it is NOT a model credential: Credentials, which is the
// whole of what the cascade may see, never offers it, and Lookup, which
// answers model credentials by the name the cascade speaks, does not answer
// it either.
func TestReviewSigningEntryIsALegalBundleKeyAndIsNoModelCredential(t *testing.T) {
	b, err := ParseBundle(reviewSigningDoc(t))
	if err != nil {
		t.Fatalf("ParseBundle refused a document holding the review-signing entries: %v", err)
	}
	current, previousValue := reviewKey(t, "current"), reviewKey(t, "previous")
	for _, tc := range []struct {
		name     string
		previous bool
		want     string
		wantKey  string
	}{
		{name: "the current entry", want: config.ReviewSigningKeySecret("alice"), wantKey: current},
		{name: "the rotation's previous entry", previous: true, want: config.ReviewSigningKeyPreviousSecret("alice"), wantKey: previousValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotName, key, err := b.ReviewSigningKey("alice", tc.previous)
			if err != nil {
				t.Fatalf("ReviewSigningKey(alice, %v) = %v, want the entry at %q", tc.previous, err, tc.want)
			}
			if gotName != tc.want {
				t.Errorf("ReviewSigningKey(alice, %v) name = %q, want %q", tc.previous, gotName, tc.want)
			}
			if hex.EncodeToString(key) != tc.wantKey {
				t.Errorf("ReviewSigningKey(alice, %v) answered another key's bytes", tc.previous)
			}
			if len(key) != ReviewSigningKeyBytes {
				t.Errorf("ReviewSigningKey(alice, %v) answered a %d-byte key, want %d", tc.previous, len(key), ReviewSigningKeyBytes)
			}
		})
	}
	// The family is keyed to the account, so another account has no entry: the
	// read is one account's and is answered for that account alone.
	if _, _, err := b.ReviewSigningKey("bob", false); err == nil {
		t.Error("ReviewSigningKey(bob, false) answered an entry alice's document does not hold for bob")
	}
	// And one document key is one entry: the guard is per key, so a doubled
	// review-signing name is refused whole rather than collapsed to whichever
	// of the two values the walk read last — the reading a hand-edited bundle
	// would otherwise get, and the reason the guard keys on the KEY here rather
	// than on the account: an account's two entries share the account and are
	// both legal, which the document above is.
	dup := []byte(config.ReviewSigningKeySecret("alice") + ": " + reviewKey(t, "twice") + "\n" +
		config.ReviewSigningKeySecret("alice") + ": " + reviewKey(t, "again") + "\n")
	if dupBundle, err := ParseBundle(dup); !errors.Is(err, ErrBundleUnknownKey) || dupBundle != nil {
		t.Errorf("ParseBundle with one review-signing key twice = (%v, %v), want ErrBundleUnknownKey and no bundle", dupBundle, err)
	}
	// Not a cascade credential: the model path is offered the account's token
	// and nothing else, and the entry's kind is what says so.
	if got := b.Credentials(); len(got) != 1 || got[0] != (creds.Credential{Name: "alice", Tier: creds.TierA}) {
		t.Errorf("Credentials() = %v, want the account's tier A token alone", got)
	}
	for _, name := range []string{config.ReviewSigningKeySecret("alice"), config.ReviewSigningKeyPreviousSecret("alice")} {
		if _, ok := b.Lookup(name); ok {
			t.Errorf("Lookup(%q) answered, and it answers model credentials by the name the cascade speaks", name)
		}
	}
	// And the document's own account still resolves: the new family shadows no
	// model credential.
	if e, ok := b.Lookup("alice"); !ok || e.Kind != KindModel || e.Key != "claude-oauth-token-alice" {
		t.Errorf("Lookup(alice) = (%+v, %v), want the model entry the document holds", e, ok)
	}
}

// TestReviewSigningKeyIsRefusedAValueThatIsNotAUsableKey: an entry whose value
// is not the key it claims to be is refused — not hex, or not a 32-byte key in
// hex — and the refusal is the class and never the value (no refusal in this
// file may carry one).
//
// It is the READ that refuses it and not the loader, on purpose: the document
// holds an ordinary string, so refusing the document whole over one bad key
// would take the model credentials, the App key and the git token down with it,
// while the accessor is the only path to a key and answers nothing for a value
// it cannot use.
func TestReviewSigningKeyIsRefusedAValueThatIsNotAUsableKey(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"not hex at all", fakeCred(t, "notakey")},
		{"hex one byte short of a key", reviewKey(t, "short")[:2*ReviewSigningKeyBytes-2]},
		{"hex one byte over a key", reviewKey(t, "long") + "5a"},
		{"an odd number of hex digits", reviewKey(t, "odd")[:2*ReviewSigningKeyBytes-1]},
		{"a rune that is not a hex digit", strings.Repeat("z", 2*ReviewSigningKeyBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ParseBundle([]byte(config.ClaudeAccountSecret("alice") + ": " + fakeCred(t, "token") + "\n" +
				config.ReviewSigningKeySecret("alice") + ": " + tc.value + "\n"))
			if err != nil {
				t.Fatalf("the loader refused the whole document rather than the read: %v", err)
			}
			name, key, err := b.ReviewSigningKey("alice", false)
			if !errors.Is(err, ErrReviewSigningKeyUnusable) {
				t.Fatalf("ReviewSigningKey(alice, false) = (%q, %d bytes, %v), want ErrReviewSigningKeyUnusable", name, len(key), err)
			}
			if name != "" || len(key) != 0 {
				t.Errorf("an unusable read answered (%q, %d bytes): a refusal is nothing, never a zero key", name, len(key))
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Errorf("the refusal carries the entry's value: %v", err)
			}
			// The model read is untouched by it, which is the whole reason the
			// refusal is the read's: one account's bad key entry costs that
			// account's signing and no other credential read.
			if e, ok := b.Lookup("alice"); !ok || e.Kind != KindModel || e.Key != "claude-oauth-token-alice" {
				t.Errorf("Lookup(alice) = (%+v, %v), want the account's model entry", e, ok)
			}
		})
	}
	// A value of exactly the right shape is answered, so what is refused above
	// is the shape of the value and not the read.
	b, err := ParseBundle([]byte(config.ReviewSigningKeySecret("alice") + ": " + reviewKey(t, "legal") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, key, err := b.ReviewSigningKey("alice", false); err != nil || len(key) != ReviewSigningKeyBytes {
		t.Errorf("ReviewSigningKey(alice, false) = (%d bytes, %v), want the %d-byte key", len(key), err, ReviewSigningKeyBytes)
	}
}

// TestReviewSigningKeyIsNotOfferedToTheCascade: the entry's kind is not
// KindModel, and that is the whole of what keeps it out of the cascade — the
// model path is Credentials and nothing else, so a document holding NOTHING but
// review-signing entries offers the cascade nothing rather than offering it a
// key, and no entry of the family has a tier.
func TestReviewSigningKeyIsNotOfferedToTheCascade(t *testing.T) {
	doc := []byte(config.ReviewSigningKeySecret("alice") + ": " + reviewKey(t, "current") + "\n" +
		config.ReviewSigningKeyPreviousSecret("alice") + ": " + reviewKey(t, "previous") + "\n" +
		config.ReviewSigningKeySecret(config.DefaultClaudeAccount) + ": " + reviewKey(t, "default") + "\n")
	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Credentials(); len(got) != 0 {
		t.Errorf("Credentials() = %v, want nothing: a review-signing key is no model credential", got)
	}
	for _, e := range b.Entries() {
		if e.Kind != KindReviewSigning {
			t.Errorf("entry %q has kind %q, want %q", e.Key, e.Kind, KindReviewSigning)
		}
		if e.Tier != "" {
			t.Errorf("entry %q has tier %q: a review-signing key is no cascade tier", e.Key, e.Tier)
		}
		// Lookup answers model credentials by the name the cascade speaks, and
		// a review-signing name is not one — the token's own name is the
		// account's, never the key's document key.
		if _, ok := b.Lookup(e.Key); ok {
			t.Errorf("Lookup(%q) answered, and it answers model credentials only", e.Key)
		}
	}
	// The default account's entry is the unsuffixed secret plus the family's
	// suffix, and it is read back for the default account.
	name, key, err := b.ReviewSigningKey(config.DefaultClaudeAccount, false)
	if err != nil {
		t.Fatalf("ReviewSigningKey(default, false) = %v, want the default account's entry", err)
	}
	if want := config.ClaudeOAuthToken + config.ReviewSigningKeySuffix; name != want {
		t.Errorf("ReviewSigningKey(default, false) name = %q, want %q", name, want)
	}
	if len(key) != ReviewSigningKeyBytes {
		t.Errorf("ReviewSigningKey(default, false) answered %d bytes, want %d", len(key), ReviewSigningKeyBytes)
	}
}

// TestReviewSigningKeyReadIsFailClosed: every way the read can fail is a typed
// refusal — never a zero key, never a name a caller could sign under — which is
// what §6.4 of the review design requires: a service that cannot read its key
// refuses to sign rather than signing under something else, so two
// installations' verdicts can never become interchangeable.
func TestReviewSigningKeyReadIsFailClosed(t *testing.T) {
	// A document that holds other credentials and one account's current key but
	// no rotation's previous entry yet: the read names what is missing and
	// answers nothing.
	b, err := ParseBundle([]byte(config.ClaudeOAuthToken + ": " + fakeCred(t, "token") + "\n" +
		config.ReviewSigningKeySecret("alice") + ": " + reviewKey(t, "alice") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		account  string
		previous bool
	}{
		{"an account the document holds no key for", "bob", false},
		{"the rotation's previous entry before the rotation", "alice", true},
		{"the default account, whose key is not in this document", config.DefaultClaudeAccount, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, key, err := b.ReviewSigningKey(tc.account, tc.previous)
			if !errors.Is(err, ErrReviewSigningKeyAbsent) {
				t.Fatalf("ReviewSigningKey(%q, %v) = (%q, %d bytes, %v), want ErrReviewSigningKeyAbsent", tc.account, tc.previous, name, len(key), err)
			}
			if name != "" || len(key) != 0 {
				t.Errorf("an absent read answered (%q, %d bytes): a refusal is nothing, never a zero key", name, len(key))
			}
		})
	}
	// An empty document is no document: the loader refuses it, so there is no
	// bundle to read a key from, and the read on the zero bundle is the same
	// absent refusal rather than a panic.
	for _, raw := range [][]byte{nil, []byte(""), []byte("{}\n")} {
		if b, err := ParseBundle(raw); err == nil || b != nil {
			t.Fatalf("ParseBundle(%q) = (%v, %v), want a refusal and no bundle", raw, b, err)
		}
	}
	var none *Bundle
	if name, key, err := none.ReviewSigningKey("alice", false); !errors.Is(err, ErrReviewSigningKeyAbsent) || name != "" || len(key) != 0 {
		t.Errorf("a read on no bundle = (%q, %d bytes, %v), want the absent refusal", name, len(key), err)
	}
	// A document that does not parse is nothing to read either: the loader
	// refuses it whole and answers no bundle.
	unparseable, err := ParseBundle([]byte(config.ReviewSigningKeySecret("alice") + ": [unclosed\n"))
	if err == nil || unparseable != nil {
		t.Fatalf("ParseBundle of an unparseable document = (%v, %v), want a refusal and no bundle", unparseable, err)
	}
	if name, key, err := unparseable.ReviewSigningKey("alice", false); !errors.Is(err, ErrReviewSigningKeyAbsent) || name != "" || len(key) != 0 {
		t.Errorf("a read on a bundle that was refused = (%q, %d bytes, %v), want the absent refusal", name, len(key), err)
	}
	// And the closed set is the other half of failing closed: a name shaped
	// like the family whose account part is not a name an account may have is
	// not a key at all, so a clerk's typo in an entry NAME stops the service
	// whole rather than quietly reading no key at request time.
	typo := []byte(config.ClaudeOAuthTokenPrefix + "9alice" + config.ReviewSigningKeySuffix + ": " + reviewKey(t, "typo") + "\n")
	if _, err := ParseBundle(typo); !errors.Is(err, ErrBundleUnknownKey) {
		t.Errorf("ParseBundle with a review-signing name outside the account rule = %v, want ErrBundleUnknownKey", err)
	}
}
