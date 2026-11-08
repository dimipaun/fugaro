// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package licence

// The verifier's tests (subscriptions plan S2.3), and the generator of the one
// set of vectors all three verifiers share (S2.6).
//
// **Every key here is minted in the process that reads it.** A fixture file
// holding a private half would be key material in a repository whatever it is
// called (.claude/rules/ee.md, R5), so fixtureKey generates a keypair per call
// and the private half never leaves memory. That is also why the negative
// vectors can exist without the owner's production key: what the file carries
// is a public half and a signature over it, and TestGenerateVectors mints the
// pair it signs them with on the spot.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// envHelperVar marks the re-run of this test binary that
// TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts starts as its child. It is
// what TestMain reads to leave the environment alone in that child: the child's
// whole purpose is to report the environment its parent handed it.
const envHelperVar = "FUGARO_LICENCE_ENV_HELPER"

// TestMain keeps the tests off the developer's own machine: no handed-over key
// in the ambient environment decides a store's behaviour, and the two
// directories a store would look in are temporary ones. The child the
// environment test starts is the exception, above.
func TestMain(m *testing.M) {
	if os.Getenv(envHelperVar) == "" {
		os.Unsetenv(EnvironmentKey)
		for _, name := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "HOME"} {
			dir, err := os.MkdirTemp("", "fugaro-test-licence-")
			if err != nil {
				continue
			}
			os.Setenv(name, dir)
		}
	}
	os.Exit(m.Run())
}

// The two key ids the vectors are signed under. Neither is a production id and
// neither ever becomes one: TestTestKeyIDIsNotInTheProductionKeyring holds the
// fixture's id out of the compiled keyring, and the staging id is the one a
// sandbox installation is minted under (docs/licensing/licence-key.md,
// "Test-mode keys"), which every production build refuses for the id alone.
//
// They are base64url-safe because the key text's first part is base64url in a
// production key: an id with a `.` in it would split the key into four parts.
const (
	fixtureKeyID = "licence-test-fixture-1"
	stagingKeyID = "licence-staging-sandbox-1"
)

// fixtureKey is a throwaway Ed25519 keypair, generated per call: the private
// half is what mints the vectors, and it is never written anywhere.
type fixtureKey struct {
	id   string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newFixtureKey(t *testing.T, id string) fixtureKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the fixture key %s: %v", id, err)
	}
	return fixtureKey{id: id, priv: priv, pub: pub}
}

// public is the half a keyring holds, as the vectors file spells it.
func (f fixtureKey) public() string {
	return base64.RawURLEncoding.EncodeToString(f.pub)
}

// signClaims mints the key text for a claim set: the payload is the claim set
// marshalled as it is, and the signature is over exactly those bytes.
func (f fixtureKey) signClaims(t *testing.T, c Claims) string {
	t.Helper()
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshalling the claim set: %v", err)
	}
	return f.signRaw(t, payload)
}

// signRaw mints the key text over bytes that are not a claim set at all. The
// two vectors that need it (a payload that is not JSON, a truncated one) are
// the only way to reach the claims refusal: the signature has to verify over
// the bytes before anything parses them.
func (f fixtureKey) signRaw(t *testing.T, payload []byte) string {
	t.Helper()
	return encodeKey(f.id, payload, ed25519.Sign(f.priv, payload))
}

// encodeKey renders the three parts, base64url and unpadded, the way the
// design writes a key.
func encodeKey(id string, payload, signature []byte) string {
	return id + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

// ringOf is the keyring a verifier is asked with: the fixture's id and no
// other, which is what makes TestStagingKeyIDIsRefused a test of the keyring
// rather than of the staging key's own bytes.
func ringOf(f fixtureKey) Keyring {
	return Keyring{f.id: f.pub}
}

func TestVerifyRefusesBeforeParsing(t *testing.T) {
	fixture := newFixtureKey(t, fixtureKeyID)
	now := time.Now()

	// An id the keyring does not hold, with two parts that are not base64url at
	// all: the refusal is the id, because an id the build cannot place is a key
	// whose other parts are never decoded (docs/licensing/licence-key.md, "The
	// check"). A verifier that decoded first would report a malformed part for
	// a key whose only fault is that this build does not know it.
	_, _, err := verify([]byte("an-id-this-keyring-does-not-hold.%%%.%%%"), now, ringOf(fixture))
	if !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("a key whose id is not in the keyring answered %v, want ErrUnknownKeyID: the id leads, so the other two parts are not read at all", err)
	}

	// The id is known, the payload is not JSON, and the signature is not the
	// one that payload was signed with: the refusal is the signature. Nothing
	// parsed the payload, which is the rule the whole design rests on -- no
	// claim is read out of a payload whose signature did not verify.
	raw := encodeKey(fixture.id, []byte("this is not a claim set"), bytes.Repeat([]byte{7}, ed25519.SignatureSize))
	_, _, err = verify([]byte(raw), now, ringOf(fixture))
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("a payload that is not JSON with a signature that does not verify answered %v, want ErrSignature: the signature is checked over the raw bytes, before anything parses them", err)
	}
}

func TestVerifyUnknownKeyID(t *testing.T) {
	fixture := newFixtureKey(t, fixtureKeyID)
	staging := newFixtureKey(t, stagingKeyID)
	raw := staging.signClaims(t, Claims{
		Tier: TierTeam, Features: []string{"coordinator"}, Seats: 3,
		Organisation: "example.com", Issued: time.Now().Add(-24 * time.Hour).Unix(),
		Expires: time.Now().Add(300 * 24 * time.Hour).Unix(),
	})

	claims, state, err := verify([]byte(raw), time.Now(), ringOf(fixture))
	if !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("a key signed by an id outside the keyring answered %v (%s, %s), want ErrUnknownKeyID", err, claims.Organisation, state)
	}
}

func TestGraceIsThirtyDays(t *testing.T) {
	if grace != 30*24*time.Hour {
		t.Fatalf("the grace is %s, want 30 days: B17's number is a ruling rather than a tuning, and the 30-day issue lead of a renewal is measured against it", grace)
	}
	fixture := newFixtureKey(t, fixtureKeyID)
	expires := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	raw := fixture.signClaims(t, Claims{
		Tier: TierCommunity, Features: []string{}, Seats: 1, Organisation: "an-individual",
		Issued:  expires.Add(-365 * 24 * time.Hour).Unix(),
		Expires: expires.Unix(),
	})

	for _, tt := range []struct {
		name string
		now  time.Time
		want State
	}{
		{"a second inside the term", expires.Add(-time.Second), Valid},
		{"the second the term ends", expires, Valid},
		{"a second past the term", expires.Add(time.Second), Grace},
		{"the last second of the grace", expires.Add(30 * 24 * time.Hour), Grace},
		{"a second past the grace", expires.Add(30*24*time.Hour + time.Second), Expired},
		{"long past the grace", expires.Add(400 * 24 * time.Hour), Expired},
	} {
		t.Run(tt.name, func(t *testing.T) {
			claims, got, err := verify([]byte(raw), tt.now, ringOf(fixture))
			if err != nil {
				t.Fatalf("%v", err)
			}
			if got != tt.want {
				t.Fatalf("a key expiring at %s read at %s is %s, want %s", expires, tt.now, got, tt.want)
			}
			if claims.Organisation != "an-individual" {
				t.Fatalf("the claims came back as %+v", claims)
			}
		})
	}
}

func TestExpiredAfterGrace(t *testing.T) {
	fixture := newFixtureKey(t, fixtureKeyID)
	expires := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	raw := fixture.signClaims(t, Claims{
		Tier: TierEnterprise, Features: []string{"sso"}, Seats: 40, Organisation: "example.com",
		Issued:  expires.Add(-365 * 24 * time.Hour).Unix(),
		Expires: expires.Unix(),
	})

	// The state is a property of the key and the clock, so a key past its grace
	// stays expired however far the clock is wound on: nothing here is a
	// function of how long ago the lapse was.
	for _, now := range []time.Time{
		expires.Add(31 * 24 * time.Hour),
		expires.Add(365 * 24 * time.Hour),
		expires.Add(10 * 365 * 24 * time.Hour),
	} {
		got, state, err := verify([]byte(raw), now, ringOf(fixture))
		if err != nil {
			t.Fatalf("%v", err)
		}
		if state != Expired {
			t.Fatalf("a key expiring at %s read at %s is %s, want %s", expires, now, state, Expired)
		}
		if got.Tier != TierEnterprise {
			t.Fatalf("the claims of an expired key are still the key's own, got %+v", got)
		}
	}
}

func TestKeyBeyondTheTierLifetimeIsRefused(t *testing.T) {
	fixture := newFixtureKey(t, fixtureKeyID)
	issued := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tt := range []struct {
		name   string
		tier   string
		term   time.Duration
		refuse bool
	}{
		{"a year's term", TierTeam, 365 * 24 * time.Hour, false},
		{"a year and the 30-day issue lead", TierTeam, tierMaxTerm[TierTeam], false},
		{"a second past the tier's maximum", TierTeam, tierMaxTerm[TierTeam] + time.Second, true},
		{"the same maximum on the free tier", TierCommunity, tierMaxTerm[TierCommunity], false},
		{"past the free tier's maximum", TierCommunity, tierMaxTerm[TierCommunity] + time.Second, true},
		{"past the enterprise maximum", TierEnterprise, tierMaxTerm[TierEnterprise] + 24*time.Hour, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := fixture.signClaims(t, Claims{
				Tier: tt.tier, Features: []string{}, Seats: 1, Organisation: "example.com",
				Issued:  issued.Unix(),
				Expires: issued.Add(tt.term).Unix(),
			})
			claims, state, err := verify([]byte(raw), issued, ringOf(fixture))
			if tt.refuse {
				if !errors.Is(err, ErrLifetime) {
					t.Fatalf("a %s key with a term of %s verified as %s (%s), want ErrLifetime: the tier's maximum is the bound the design states, and the renewal's 30-day lead is why it is the term plus 30 days", tt.tier, tt.term, state, claims.Organisation)
				}
				return
			}
			if err != nil {
				t.Fatalf("a %s key with a term of %s was refused: %v", tt.tier, tt.term, err)
			}
			if state != Valid {
				t.Fatalf("a key minted at %s with a term of %s read at %s is %s, want %s", issued, tt.term, issued, state, Valid)
			}
		})
	}

	// A tier this verifier does not know is a claim set it cannot place, and
	// the refusal names the claim rather than repeating its value.
	raw := fixture.signClaims(t, Claims{
		Tier: "unlimited", Features: []string{}, Seats: 1, Organisation: "example.com",
		Issued: issued.Unix(), Expires: issued.Add(30 * 24 * time.Hour).Unix(),
	})
	if _, _, err := verify([]byte(raw), issued, ringOf(fixture)); !errors.Is(err, ErrClaims) {
		t.Fatalf("a key of an unknown tier answered %v, want ErrClaims", err)
	}
}

func TestTestKeyIDIsNotInTheProductionKeyring(t *testing.T) {
	file := loadVectors(t)
	for _, id := range []string{fixtureKeyID, stagingKeyID} {
		if _, held := productionKeyring[id]; held {
			t.Fatalf("the production keyring holds %s: a test or staging key must never license a build, and the keyring is what decides (docs/licensing/licence-key.md, \"Test-mode keys\")", id)
		}
	}
	for id := range file.Keyring {
		if _, held := productionKeyring[id]; held {
			t.Fatalf("testdata/vectors.json is signed under %s, which the production keyring holds", id)
		}
	}
}

func TestStagingKeyIDIsRefused(t *testing.T) {
	file := loadVectors(t)
	staging := file.vectorNamed(t, "a staging key")
	if staging.Refused != "unknown-key-id" {
		t.Fatalf("the staging vector is spelled %q, want unknown-key-id: production verifiers refuse a staging key for its id alone", staging.Refused)
	}
	claims, state, err := Verify([]byte(staging.Key), time.Unix(file.Now, 0))
	if !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("Verify answered %v (%s, %s) for a staging key, want ErrUnknownKeyID", err, claims.Organisation, state)
	}
}

func TestOverlappingKeysPickTheLatest(t *testing.T) {
	file := loadVectors(t)
	ring := file.ring(t)

	// The pair the design is about (T26): two keys installed at once, and the
	// verifier takes the one with the later expiry among those that verify.
	for _, c := range file.Installed {
		t.Run(c.Name, func(t *testing.T) {
			for name, raw := range c.Keys {
				if name == c.Applies {
					continue
				}
				// The pair is only interesting while both keys are good, so the
				// test would prove nothing if the other one were refused.
				if _, _, err := verify([]byte(raw), time.Unix(file.Now, 0), ring); err != nil {
					t.Fatalf("the %s key of the pair is refused too (%v), so the choice is not what this case measures", name, err)
				}
			}
			want, _, err := verify([]byte(c.Keys[c.Applies]), time.Unix(file.Now, 0), ring)
			if err != nil {
				t.Fatalf("the key this case says applies is refused: %v", err)
			}
			got, state, err := active(ring, rawKeys(t, c.Keys), time.Unix(file.Now, 0))
			if err != nil {
				t.Fatalf("%v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("the store applied %+v, want the %s key's %+v", got, c.Applies, want)
			}
			if wantState := State(c.State); state != wantState {
				t.Fatalf("the store answered %s, want %s", state, wantState)
			}
		})
	}

	// The file's order is not the order the store reads: a map has none. The
	// two keys are read the other way round here, and the answer is the same.
	pair := file.Installed[0]
	reversed := [][]byte{[]byte(pair.Keys[pair.Applies])}
	for name, raw := range pair.Keys {
		if name != pair.Applies {
			reversed = append(reversed, []byte(raw))
		}
	}
	want, _, err := verify([]byte(pair.Keys[pair.Applies]), time.Unix(file.Now, 0), ring)
	if err != nil {
		t.Fatalf("%v", err)
	}
	got, _, err := active(ring, reversed, time.Unix(file.Now, 0))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("with the keys the other way round the store applied %+v, want %+v", got, want)
	}
}

// TestTheKeyFileIsReadAtMostTwoLines holds the CLI's own storage to the design:
// one key per line, at most two, because two is what the renewal's overlap
// installs (T26, docs/licensing/licence-key.md, "Storage and hand-off").
func TestTheKeyFileIsReadAtMostTwoLines(t *testing.T) {
	fixture := newFixtureKey(t, fixtureKeyID)
	first := fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 1, Organisation: "example.com", Issued: 1, Expires: 2})
	second := fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 1, Organisation: "example.com", Issued: 1, Expires: 3})

	store := openTestStore(t)
	writeKeyFile(t, store, 0o600, first+"\n\n"+second+"\n")
	keys, err := store.Keys()
	if err != nil {
		t.Fatalf("a file with two keys and a blank line: %v", err)
	}
	if len(keys) != 2 || string(keys[0]) != first || string(keys[1]) != second {
		t.Fatalf("the store read %q, want the file's two keys in order", keys)
	}

	// A key file another local user can read is the file F41 is about, and the
	// design keeps it at 0600. The refusal is the store's because nothing else
	// writes the file: the operator does, or the console hands a key over in
	// the environment instead.
	writeKeyFile(t, store, 0o644, first+"\n")
	if _, err := store.Keys(); !errors.Is(err, ErrKeyFile) {
		t.Fatalf("a key file of mode 0644 answered %v, want ErrKeyFile", err)
	}
	writeKeyFile(t, store, 0o600, first+"\n"+second+"\n"+first+"\n")
	if _, err := store.Keys(); !errors.Is(err, ErrKeyFile) {
		t.Fatalf("a key file with three keys answered %v, want ErrKeyFile", err)
	}
}

// TestNoKeyIsTheStoreBeingEmpty holds F43: a fresh installation and an upgraded
// console 0.112.0 have no key, and that is a state rather than a failure.
func TestNoKeyIsTheStoreBeingEmpty(t *testing.T) {
	store := openTestStore(t)
	keys, err := store.Keys()
	if err != nil {
		t.Fatalf("a store with no key file: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("a store with no key file read %q", keys)
	}
	if _, _, err := store.Active(time.Now()); !errors.Is(err, ErrNoKey) {
		t.Fatalf("an empty store answered %v, want ErrNoKey", err)
	}
}

func TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts(t *testing.T) {
	// This test re-runs its own binary to be a child process: what a child of
	// the CLI sees is the environment as it stands when the child is started,
	// and printing it from another process is the only reading of that.
	if os.Getenv(envHelperVar) == "1" {
		fmt.Print(os.Getenv(EnvironmentKey))
		os.Exit(0)
	}

	fixture := newFixtureKey(t, fixtureKeyID)
	key := fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 2, Organisation: "example.com", Issued: 1, Expires: 4000000000})
	t.Setenv(EnvironmentKey, key)

	// The control: before the registration point a child of this process does
	// inherit the variable, so the reading below is not an empty child that
	// would have printed nothing whatever the CLI did.
	if got := childEnvironmentKey(t); got != key {
		t.Fatalf("the helper process read %q before the registration point, want the handed-over key: the reading itself is what this test measures", got)
	}

	// The registration point's own call, with no config directory in reach at
	// all: a handed-over key is the whole installation, so the store must be
	// usable without one.
	store, err := Open(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Open with a key handed over in the environment: %v", err)
	}
	if got := childEnvironmentKey(t); got != "" {
		t.Fatalf("a child started after the registration point read %q from %s: the CLI starts git, the plugin install, terraform and the runner, and one variable name left in the environment reaches all four", got, EnvironmentKey)
	}
	if _, still := os.LookupEnv(EnvironmentKey); still {
		t.Fatalf("%s is still in this process's environment after the registration point", EnvironmentKey)
	}
	keys, err := store.Keys()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(keys) != 1 || string(keys[0]) != key {
		t.Fatalf("the store read %q, want the key the environment handed over", keys)
	}
}

// childEnvironmentKey answers what a child of this process reads from
// FUGARO_LICENCE_KEY, by starting this test binary and having it print.
func childEnvironmentKey(t *testing.T) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts")
	cmd.Env = append(os.Environ(), envHelperVar+"=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the helper process: %v", err)
	}
	return string(out)
}

// TestLicencePackageImportsNoNetwork holds the rule the plan pins for this task:
// a key verifier makes no network call, so it cannot phone home, look a key up
// or check a revocation -- there is no server to ask (D308).
//
// The rule is two readings of the same thing. No file here, test or not, may
// import a net package. And the package's own files may import nothing outside
// the exact list below, so a call that reached a client of this project's own
// (an internal http package) is a deliberate change to the list rather than an
// import nobody noticed -- and so crypto/rand, which a verifier has no business
// with because it never mints, is refused too.
func TestLicencePackageImportsNoNetwork(t *testing.T) {
	allowed := []string{
		"bytes",           // the claim set is decoded from the payload's own bytes
		"crypto/ed25519",  // the one algorithm a key is signed with (D308)
		"encoding/base64", // the key text's three parts
		"encoding/json",   // the claim set
		"errors",          // the refusals
		"fmt",             // a refusal names the key id and the part that failed
		"io",              // the strict decode reads to the end of the payload
		"io/fs",           // the key file's absence is a state and not an error (F43)
		"os",              // the key file, and the one variable taken out of the environment
		"path/filepath",   // the key file's place under the config directory
		"runtime",         // the platform the config directory is the platform's own
		"strings",         // the key text's three parts, and the file's lines
		"time",            // the clock the caller passes and the grace against it
		"github.com/dimipaun/fugaro/internal/localcfg", // the CLI's own config directory
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("the scan read %d Go files, want the package's own three: a scan of nothing proves nothing", len(files))
	}
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, imp := range f.Imports {
			imported, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s imports %s, which is not a readable string", path, imp.Path.Value)
			}
			if imported == "net" || strings.HasPrefix(imported, "net/") {
				t.Errorf("%s imports %s: the key check answers offline and asks no server anything (D308)", path, imported)
				continue
			}
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			if !slices.Contains(allowed, imported) {
				t.Errorf("%s imports %s, which is not on the verifier's import list; a new import here is a decision a reviewer reads, and the list is what keeps a network client out", path, imported)
			}
		}
	}
}

func TestVectorsRoundTrip(t *testing.T) {
	file := loadVectors(t)
	ring := file.ring(t)
	now := time.Unix(file.Now, 0)

	if len(file.Keyring) != 1 {
		t.Fatalf("the vectors' keyring holds %d keys, want exactly the fixture's one: a second key would have to be there for a vector's reason", len(file.Keyring))
	}
	if len(file.Vectors) < 10 || len(file.Installed) < 2 {
		t.Fatalf("the vectors file carries %d vectors and %d installed cases, and it was written to carry both halves", len(file.Vectors), len(file.Installed))
	}

	seen := map[string]bool{}
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			claims, state, err := verify([]byte(v.Key), now, ring)
			if v.Refused != "" {
				want, ok := vectorRefusals[v.Refused]
				if !ok {
					t.Fatalf("the vector names the refusal %q, which is not one of the five the verifiers share", v.Refused)
				}
				if !errors.Is(err, want) {
					t.Fatalf("the vector is read as %v (%s, %s), want %v", err, claims.Organisation, state, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v", err)
			}
			if State(v.State) != state {
				t.Fatalf("the vector is %s, want %s", state, v.State)
			}
			if v.Claims == nil {
				t.Fatalf("an accepted vector carries no claim set, so nothing holds the payload to the terms it states")
			}
			if !reflect.DeepEqual(claims, *v.Claims) {
				t.Fatalf("the key's claims are %+v, want %+v", claims, *v.Claims)
			}
		})
		seen[v.State+v.Refused] = true
	}

	// Each of the five refusals is in the file: a vector set that lost the
	// negative half would still round-trip green.
	for name := range vectorRefusals {
		if !seen[name] {
			t.Errorf("no vector in the file is refused as %s", name)
		}
	}
	// The five states and boundaries the three verifiers must agree on: the
	// term, the two ends of the grace, a refusal past the tier's term, and a
	// claim set whose signature is over bytes nobody signed.
	for _, want := range []string{string(Valid), string(Grace), string(Expired)} {
		if !seen[want] {
			t.Errorf("no vector in the file is %s", want)
		}
	}
}

// TestGenerateVectors rewrites testdata/vectors.json. It is skipped unless the
// environment asks for it, so a plain `go test` never rewrites a file under a
// reviewer's feet: the vectors' signatures are freshly minted on every run, and
// a rewritten file is a diff nobody asked for. Run it when the claim set, a
// state or a refusal changes:
//
//	FUGARO_LICENCE_WRITE_VECTORS=1 go test -tags ee ./ee/licence/ -run TestGenerateVectors -count=1
func TestGenerateVectors(t *testing.T) {
	if os.Getenv("FUGARO_LICENCE_WRITE_VECTORS") == "" {
		t.Skip("set FUGARO_LICENCE_WRITE_VECTORS=1 to rewrite testdata/vectors.json")
	}
	data, err := json.MarshalIndent(generateVectors(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vectorsFile, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", vectorsFile)
}

// vectorsFile is the one set of vectors all three verifiers are read against
// (S2.6): the Go one, the console's (S2.4) and the app's (S2.5).
const vectorsFile = "testdata/vectors.json"

// vectorFile is that file's shape. It is JSON a TypeScript and a JavaScript
// verifier read without a Go type, so it carries strings, numbers and booleans
// only -- no Go spelling of a state or a refusal, just the names the three
// verifiers agree on.
type vectorFile struct {
	// Note is what the file is and what it is not: throwaway test keys, never
	// a production one.
	Note string `json:"note"`
	// Now is the clock every vector is read at, in Unix seconds. One clock for
	// the file rather than one per vector, because the boundaries are the
	// interesting part and they are easier to see as expiry dates against a
	// single now.
	Now int64 `json:"now"`
	// Keyring is the public half of each key a verifier is asked with, named by
	// key id.
	Keyring map[string]string `json:"keyring"`
	// Staging is the sandbox key, which is deliberately not in Keyring: a
	// production verifier holds production ids only.
	Staging vectorKey `json:"staging"`
	// Vectors is one key and what a verifier must answer for it.
	Vectors []vectorCase `json:"vectors"`
	// Installed is a store holding more than one key and the one that applies.
	Installed []installedCase `json:"installed"`
}

type vectorKey struct {
	Note      string `json:"note"`
	ID        string `json:"id"`
	PublicKey string `json:"public_key"`
}

type vectorCase struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	// State and Claims are what an accepted key answers; Refused is the part a
	// refused one fails on. Exactly one half is set.
	State   string  `json:"state,omitempty"`
	Claims  *Claims `json:"claims,omitempty"`
	Refused string  `json:"refused,omitempty"`
}

type installedCase struct {
	Name string `json:"name"`
	// Keys is every installed key, named so that Applies can point at one.
	Keys map[string]string `json:"keys"`
	// Applies is the installed key the verifier must take: the one with the
	// later expiry among those that verify (T26).
	Applies string `json:"applies"`
	State   string `json:"state"`
}

// vectorRefusals names each refusal as the file spells it. The names are what
// the three verifiers agree on; each answers its own error, which is why the
// file carries a name rather than this package's sentinel's text.
var vectorRefusals = map[string]error{
	"malformed":      ErrMalformed,
	"unknown-key-id": ErrUnknownKeyID,
	"signature":      ErrSignature,
	"claims":         ErrClaims,
	"lifetime":       ErrLifetime,
}

// loadVectors reads the file every verifier is read against.
func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	data, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatalf("%s: %v -- the vectors are the one set the three verifiers share, so a verifier whose tests cannot read them proves nothing", vectorsFile, err)
	}
	var file vectorFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("%s is not the JSON this file's shape declares: %v", vectorsFile, err)
	}
	return file
}

// ring is the keyring the vectors are asked with: the file's own keys, decoded.
func (f vectorFile) ring(t *testing.T) Keyring {
	t.Helper()
	ring := make(Keyring, len(f.Keyring))
	for id, encoded := range f.Keyring {
		key, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("the keyring's %s is not base64url: %v", id, err)
		}
		if len(key) != ed25519.PublicKeySize {
			t.Fatalf("the keyring's %s is %d bytes, and an Ed25519 public key is %d", id, len(key), ed25519.PublicKeySize)
		}
		ring[id] = ed25519.PublicKey(key)
	}
	return ring
}

// vectorNamed answers the vector a test names, so a test about one vector fails
// by name rather than by index if the file is reordered.
func (f vectorFile) vectorNamed(t *testing.T, name string) vectorCase {
	t.Helper()
	for _, v := range f.Vectors {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("%s carries no vector named %q", vectorsFile, name)
	return vectorCase{}
}

// rawKeys is an installed set as the store reads it: the file's keys, in
// whatever order the map hands them over.
func rawKeys(t *testing.T, keys map[string]string) [][]byte {
	t.Helper()
	raw := make([][]byte, 0, len(keys))
	for _, key := range keys {
		raw = append(raw, []byte(key))
	}
	return raw
}

// openTestStore answers the CLI's store over a config directory of the test's
// own: the environment is read through the caller-supplied getenv, which is
// what keeps this off the developer's own machine.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if dir := filepath.Dir(store.path); dir == "" || !strings.HasSuffix(filepath.ToSlash(dir), "/fugaro") {
		t.Fatalf("the store's key file is %s, want it under the CLI's own config directory", store.path)
	}
	return store
}

// writeKeyFile writes the store's key file at a mode the test chooses: the mode
// is half of what the store reads.
func writeKeyFile(t *testing.T, store *Store, mode fs.FileMode, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(store.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile keeps an existing file's mode, so a second call at a different
	// mode has to set it.
	if err := os.Chmod(store.path, mode); err != nil {
		t.Fatal(err)
	}
}

// generateVectors mints the file's vectors. Every signature is made here, in
// this process, with a keypair that is thrown away when the test ends: the file
// holds the public half and the signed key text and nothing a verifier could
// mint with (.claude/rules/ee.md).
func generateVectors(t *testing.T) vectorFile {
	t.Helper()
	fixture := newFixtureKey(t, fixtureKeyID)
	staging := newFixtureKey(t, stagingKeyID)

	// The clock the whole file is read at. A fixed date rather than time.Now:
	// the file is committed, and a vector's answer must not change with the day
	// the test runs.
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC).Unix()
	days := func(n int) int64 { return now + int64(n)*24*60*60 }

	file := vectorFile{
		Note:    "The claim sets, states and refusals the Go, console and app verifiers are all read against (subscriptions plan S2.6, design docs/licensing/licence-key.md). Every key here is minted by a throwaway test keypair whose private half was generated in the process that wrote this file and never written down; no build trusts it, and ee/licence/key.go's production keyring holds no id at all until S2.9 publishes the owner's production key. `now` is the clock every vector is read at; `state` and `claims` are what an accepted key answers and `refused` is the part a refused one fails on, named as the three verifiers name it.",
		Now:     now,
		Keyring: map[string]string{fixture.id: fixture.public()},
		Staging: vectorKey{
			Note:      "The sandbox key: minted under the staging id for tests and for a sandbox installation, and in no production keyring, so a production verifier refuses it for the id alone (docs/licensing/licence-key.md, \"Test-mode keys\").",
			ID:        staging.id,
			PublicKey: staging.public(),
		},
	}

	accept := func(name string, key string, state State, claims Claims) {
		file.Vectors = append(file.Vectors, vectorCase{Name: name, Key: key, State: string(state), Claims: &claims})
	}
	refuse := func(name, key, refusal string) {
		file.Vectors = append(file.Vectors, vectorCase{Name: name, Key: key, Refused: refusal})
	}

	// The term, and the two ends of the grace. The expiries are the boundaries
	// and the clock is the same for all of them, so each boundary is a key
	// whose expiry sits at that distance from now.
	accept("a team key inside its term",
		fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{"coordinator"}, Seats: 5, Organisation: "example.com", Issued: days(-30), Expires: days(335)}),
		Valid,
		Claims{Tier: TierTeam, Features: []string{"coordinator"}, Seats: 5, Organisation: "example.com", Issued: days(-30), Expires: days(335)})
	accept("a key at the second its term ends",
		fixture.signClaims(t, Claims{Tier: TierCommunity, Features: []string{}, Seats: 1, Organisation: "an-individual", Issued: days(-365), Expires: days(0)}),
		Valid,
		Claims{Tier: TierCommunity, Features: []string{}, Seats: 1, Organisation: "an-individual", Issued: days(-365), Expires: days(0)})
	accept("a key a second into its grace",
		fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 3, Organisation: "example.com", Issued: days(-366), Expires: days(0) - 1}),
		Grace,
		Claims{Tier: TierTeam, Features: []string{}, Seats: 3, Organisation: "example.com", Issued: days(-366), Expires: days(0) - 1})
	accept("a key at the last second of its grace",
		fixture.signClaims(t, Claims{Tier: TierEnterprise, Features: []string{"sso"}, Seats: 40, Organisation: "an-order-form-id", Issued: days(-395), Expires: days(-30)}),
		Grace,
		Claims{Tier: TierEnterprise, Features: []string{"sso"}, Seats: 40, Organisation: "an-order-form-id", Issued: days(-395), Expires: days(-30)})
	accept("a key a second past its grace",
		fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 3, Organisation: "example.com", Issued: days(-396), Expires: days(-30) - 1}),
		Expired,
		Claims{Tier: TierTeam, Features: []string{}, Seats: 3, Organisation: "example.com", Issued: days(-396), Expires: days(-30) - 1})
	accept("a key long past its grace",
		fixture.signClaims(t, Claims{Tier: TierCommunity, Features: []string{}, Seats: 1, Organisation: "an-individual", Issued: days(-455), Expires: days(-90)}),
		Expired,
		Claims{Tier: TierCommunity, Features: []string{}, Seats: 1, Organisation: "an-individual", Issued: days(-455), Expires: days(-90)})

	// A term past the tier's maximum, signed properly: the signature verifies
	// and the key is refused for its length alone.
	refuse("a term longer than its tier allows",
		fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 1, Organisation: "example.com", Issued: days(-500), Expires: days(100)}),
		"lifetime")

	// The non-canonical signature: the same claims, spelled differently, with
	// the signature over the spelling that was actually signed. Re-wrapping or
	// re-encoding a claim set is a different byte string and fails the
	// signature rather than verifying as the same claims.
	signed, err := json.Marshal(Claims{Tier: TierTeam, Features: []string{}, Seats: 1, Organisation: "example.com", Issued: days(-10), Expires: days(355)})
	if err != nil {
		t.Fatal(err)
	}
	respelled := bytes.Replace(signed, []byte(`":`), []byte(`": `), 1)
	refuse("a claim set signed as it is spelled, re-spelled",
		encodeKey(fixture.id, respelled, ed25519.Sign(fixture.priv, signed)),
		"signature")

	// A key id no keyring holds, with a payload and a signature that are good
	// under another id: the id alone is the refusal.
	foreignClaims := Claims{Tier: TierTeam, Features: []string{}, Seats: 1, Organisation: "example.com", Issued: days(-10), Expires: days(355)}
	foreignPayload, err := json.Marshal(foreignClaims)
	if err != nil {
		t.Fatal(err)
	}
	refuse("a key id this build does not hold",
		encodeKey("licence-someone-elses-key-1", foreignPayload, ed25519.Sign(fixture.priv, foreignPayload)),
		"unknown-key-id")

	refuse("a staging key",
		staging.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 1, Organisation: "example.com", Issued: days(-10), Expires: days(355)}),
		"unknown-key-id")

	// The three payloads that are signed but are not a claim set this verifier
	// can read. They are the only way to reach the claims refusal: the
	// signature has to verify over the bytes before anything parses them.
	refuse("a payload that is not a claim set",
		fixture.signRaw(t, []byte("this is not a claim set")),
		"claims")
	refuse("a claim set with a field this verifier does not know",
		fixture.signRaw(t, []byte(fmt.Sprintf(`{"tier":"team","features":[],"seats":1,"organisation":"example.com","issued":%d,"expires":%d,"covers_until":%d}`, days(-10), days(355), days(355)))),
		"claims")
	refuse("a truncated claim set",
		fixture.signRaw(t, []byte(`{"tier":"team","features":[],"seats":1,"organisation"`)),
		"claims")

	// Not three parts, and a part that is not base64url: the shape of the key
	// text is refused before any keyring is consulted.
	refuse("a key text that is not three parts",
		fixture.id+"."+base64.RawURLEncoding.EncodeToString(foreignPayload),
		"malformed")
	refuse("a part that is not base64url",
		fixture.id+".%%%.%%%",
		"malformed")

	// The overlap (T26): two keys installed at once, and the one with the later
	// expiry among those that verify is the one that applies.
	file.Installed = append(file.Installed,
		installedCase{
			Name: "a renewal arriving while the key it follows is still valid",
			Keys: map[string]string{
				"current": fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 5, Organisation: "example.com", Issued: days(-320), Expires: days(45)}),
				"renewal": fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 5, Organisation: "example.com", Issued: days(-1), Expires: days(364)}),
			},
			Applies: "renewal",
			State:   string(Valid),
		},
		installedCase{
			Name: "a key whose term has run out beside the key that follows it",
			Keys: map[string]string{
				"current": fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 5, Organisation: "example.com", Issued: days(-368), Expires: days(-3)}),
				"renewal": fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 5, Organisation: "example.com", Issued: days(-1), Expires: days(364)}),
			},
			Applies: "renewal",
			State:   string(Valid),
		},
	)

	return file
}
