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

// bundleDoc is a valid document holding the two credential families.
func bundleDoc(t *testing.T) []byte {
	t.Helper()
	return []byte("claude-oauth-token: " + fakeCred(t, "default") + "\n" +
		"claude-oauth-token-alice: " + fakeCred(t, "alice") + "\n" +
		config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n")
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
	if got := b.Names(); len(got) != 3 {
		t.Fatalf("names = %v, want the three credentials of the document", got)
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
		if strings.Contains(s, fakeCred(t, "default")) || strings.Contains(s, fakeCred(t, "paid")) {
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
// and the name `fugaro init`'s stage fills into
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
		{"the two families", string(bundleDoc(t))},
		{"the paid key alone", config.AnthropicSecret + ": " + fakeCred(t, "x") + "\n"},
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
		wantTier creds.Tier
		wantOK   bool
	}{
		{name: "the paid key", key: config.AnthropicSecret, wantName: config.AnthropicSecret, wantTier: creds.TierB, wantOK: true},
		{name: "the default account", key: config.ClaudeOAuthToken, wantName: config.DefaultClaudeAccount, wantTier: creds.TierA, wantOK: true},
		{name: "a named account", key: config.ClaudeAccountSecret("alice"), wantName: "alice", wantTier: creds.TierA, wantOK: true},
		{name: "a dash inside a name", key: config.ClaudeAccountSecret("acme-personal"), wantName: "acme-personal", wantTier: creds.TierA, wantOK: true},
		{name: "the longest name the rule admits", key: config.ClaudeAccountSecret(strings.Repeat("a", config.MaxClaudeAccountNameLen)), wantName: strings.Repeat("a", config.MaxClaudeAccountNameLen), wantTier: creds.TierA, wantOK: true},
		{name: "a name one byte over the bound", key: config.ClaudeAccountSecret(strings.Repeat("a", config.MaxClaudeAccountNameLen+1)), wantOK: false},
		{name: "a dash-heavy name the regex alone admits", key: config.ClaudeAccountSecret("a" + strings.Repeat("-a", 30)), wantOK: false},
		{name: "a name starting with a digit", key: config.ClaudeAccountSecret("9alice"), wantOK: false},
		{name: "a name ending in a dash", key: config.ClaudeAccountSecret("alice-"), wantOK: false},
		{name: "an upper-case name", key: config.ClaudeAccountSecret("Alice"), wantOK: false},
		{name: "an empty suffix", key: config.ClaudeOAuthTokenPrefix, wantOK: false},
		{name: "the reserved name, spelled the long way", key: config.ClaudeAccountSecret(config.DefaultClaudeAccount), wantName: config.DefaultClaudeAccount, wantTier: creds.TierA, wantOK: true},
		{name: "a name outside both families", key: "anthropic-oauth-token", wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotTier, ok := CredentialKey(tc.key)
			if ok != tc.wantOK {
				t.Fatalf("CredentialKey(%q) ok = %v, want %v", tc.key, ok, tc.wantOK)
			}
			if !ok {
				if gotName != "" || gotTier != "" {
					t.Errorf("CredentialKey(%q) = (%q, %q) for a key it refused, want empty", tc.key, gotName, gotTier)
				}
				return
			}
			if gotName != tc.wantName {
				t.Errorf("CredentialKey(%q) name = %q, want %q", tc.key, gotName, tc.wantName)
			}
			if gotTier != tc.wantTier {
				t.Errorf("CredentialKey(%q) tier = %q, want %q", tc.key, gotTier, tc.wantTier)
			}
		})
	}
}
