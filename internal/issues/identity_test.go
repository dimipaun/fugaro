package issues

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The member strings these tests use are the plan's own sample address
// (user@example.test), in the "user:<address>" spelling access.Caller returns.
const (
	testMember      = "user:user@example.test"
	testOtherMember = "user:User@example.test"
)

// identityEnv is a cache home of its own for each test, so no test reads or
// writes the machine's own caches.
func identityEnv(t *testing.T) func(string) string {
	t.Helper()
	dir := t.TempDir()
	return func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
}

func identityDeps(env func(string) string, lookup func(context.Context) (string, error)) Deps {
	return Deps{Lookup: lookup, Getenv: env, GOOS: "linux"}
}

// identityCachePathFor is where the cache has to be — the user's cache
// directory, fugaro/identity.json — computed here rather than asked of the
// code under test.
func identityCachePathFor(env func(string) string) string {
	return filepath.Join(env("XDG_CACHE_HOME"), "fugaro", "identity.json")
}

func verifiedLookup(member string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return member, nil }
}

func refusedLookup(context.Context) (string, error) {
	return "", errors.New("no Google credentials")
}

func TestIdentityVerifiedWritesCache(t *testing.T) {
	env := identityEnv(t)
	asked := false
	deps := identityDeps(env, func(ctx context.Context) (string, error) {
		asked = true
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > identityLookupTimeout {
			t.Errorf("the lookup is not bounded by %s: %v, %v", identityLookupTimeout, deadline, ok)
		}
		return testMember, nil
	})
	got, err := ResolveIdentity(context.Background(), deps)
	if err != nil || !asked {
		t.Fatalf("ResolveIdentity = %+v, %v (asked=%v)", got, err, asked)
	}
	if got.Source != SourceVerified || got.Address != testMember {
		t.Fatalf("got %+v", got)
	}
	if got.Digest != Digest(testMember) {
		t.Fatalf("digest %q, not the member's own", got.Digest)
	}
	path := identityCachePathFor(env)
	var doc struct {
		Format  int    `json:"format"`
		Address string `json:"address"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &doc) != nil {
		t.Fatalf("%s: %s, %v", path, data, err)
	}
	if doc.Format != 1 || doc.Address != testMember {
		t.Fatalf("the cache holds %+v", doc)
	}
}

func TestIdentityOfflineUsesCache(t *testing.T) {
	env := identityEnv(t)
	if _, err := ResolveIdentity(context.Background(), identityDeps(env, verifiedLookup(testMember))); err != nil {
		t.Fatal(err)
	}
	// The credential cannot be read: the last verified answer is still who
	// "me" is, and it is marked as the cache it is.
	got, err := ResolveIdentity(context.Background(), identityDeps(env, refusedLookup))
	if err != nil {
		t.Fatalf("an offline read failed: %v", err)
	}
	if got.Source != SourceCached || got.Address != testMember || got.Digest != Digest(testMember) {
		t.Fatalf("got %+v", got)
	}
}

func TestIdentityNoCacheNoneWhenOffline(t *testing.T) {
	got, err := ResolveIdentity(context.Background(), identityDeps(identityEnv(t), refusedLookup))
	if got.Source != SourceNone || got.Address != "" || got.Digest != "" {
		t.Fatalf("got %+v", got)
	}
	if err == nil {
		t.Error("a failed lookup with no cache reported no reason")
	}
}

func TestIdentityRefusesServiceAccountAndWritesNoCache(t *testing.T) {
	env := identityEnv(t)
	got, err := ResolveIdentity(context.Background(), identityDeps(env, func(context.Context) (string, error) {
		// access.Caller's own refusal for a machine credential.
		return "", errors.New("the credential is a service account's (it is not a person the grants can name)")
	}))
	if err == nil || got.Source != SourceNone || got.Digest != "" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := os.Stat(identityCachePathFor(env)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused credential left a cache behind: %v", err)
	}
}

func TestIdentityNoLookupNeverCallsTheSeam(t *testing.T) {
	env := identityEnv(t)
	never := func(context.Context) (string, error) {
		t.Error("the lookup ran with NoLookup set")
		return testMember, nil
	}
	// A coding agent's session: no cache, so no identity, and the seam is
	// never asked.
	got, err := ResolveIdentity(context.Background(), Deps{NoLookup: true, Lookup: never, Getenv: env, GOOS: "linux"})
	if err != nil || got.Source != SourceNone || got.Address != "" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// The same session with a cache still knows who "me" is, and still does
	// not ask.
	if _, err := ResolveIdentity(context.Background(), identityDeps(env, verifiedLookup(testMember))); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveIdentity(context.Background(), Deps{NoLookup: true, Lookup: never, Getenv: env, GOOS: "linux"})
	if err != nil || got.Source != SourceCached || got.Address != testMember {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestVerifiedOnlyIgnoresCache(t *testing.T) {
	env := identityEnv(t)
	if _, err := ResolveIdentity(context.Background(), identityDeps(env, verifiedLookup(testMember))); err != nil {
		t.Fatal(err)
	}
	// The cache holds an address and the credential cannot be read: a write
	// must name nobody rather than the cached reporter (F3).
	got, err := VerifiedOnly(context.Background(), identityDeps(env, refusedLookup))
	if err == nil {
		t.Fatalf("VerifiedOnly answered from the cache: %+v", got)
	}
	if got.Source != SourceNone || got.Address != "" || got.Digest != "" {
		t.Fatalf("got %+v", got)
	}
	// A session that may not ask at all is refused the same way, cache or no
	// cache: an upload never names a reporter nobody verified.
	got, err = VerifiedOnly(context.Background(), Deps{NoLookup: true, Lookup: verifiedLookup(testOtherMember), Getenv: env, GOOS: "linux"})
	if err == nil || got.Source != SourceNone || got.Address != "" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// A credential that answers is what it names, cache or no cache.
	got, err = VerifiedOnly(context.Background(), identityDeps(env, verifiedLookup(testOtherMember)))
	if err != nil || got.Source != SourceVerified || got.Address != testOtherMember {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestIdentityCacheIsPrivateAndStrict(t *testing.T) {
	env := identityEnv(t)
	path := identityCachePathFor(env)
	if _, err := ResolveIdentity(context.Background(), identityDeps(env, verifiedLookup(testMember))); err != nil {
		t.Fatal(err)
	}
	// Private: the file 0600 in a 0700 directory.
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%s: %v, %v", path, fi, err)
	}
	if di, err := os.Stat(filepath.Dir(path)); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("the cache directory is not private: %v, %v", di, err)
	}
	for name, body := range map[string]string{
		"an unknown field": `{"format":1,"address":"user:user@example.test","extra":1}`,
		"another format":   `{"format":2,"address":"user:user@example.test"}`,
		"no format":        `{"address":"user:user@example.test"}`,
		"no address":       `{"format":1}`,
		"a second value":   `{"format":1,"address":"user:user@example.test"}{"format":1,"address":"user:user@example.test"}`,
		"not JSON":         `{`,
		"over the bound":   `{"format":1,"address":"` + strings.Repeat("a", maxIdentityCacheBytes) + `"}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveIdentity(context.Background(), identityDeps(env, refusedLookup))
		if got.Source != SourceNone || got.Address != "" || err == nil {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}
	// The shape this build writes is what it reads back.
	if err := os.WriteFile(path, []byte(`{"format":1,"address":"user:user@example.test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveIdentity(context.Background(), identityDeps(env, refusedLookup))
	if err != nil || got.Source != SourceCached || got.Address != testMember {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestIdentityDigestIsStableSixteenHexAndNotCaseFolded(t *testing.T) {
	// The literal pins the one function the shared store's key layout and
	// mine both build from: changing the digest changes both sides at once.
	if got := Digest(testMember); got != "aa202bb1fbb6eace" {
		t.Fatalf("Digest(%q) = %q", testMember, got)
	}
	if got := Digest(testMember); len(got) != 16 {
		t.Fatalf("the digest is %d characters", len(got))
	}
	// Two spellings of a local part are two members: folding them would merge
	// two people into one reporter (F3).
	if Digest(testOtherMember) == Digest(testMember) {
		t.Error("the digest folds case")
	}
}
