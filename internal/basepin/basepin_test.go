package basepin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/mirror"
)

var testDigest = "sha256:" + strings.Repeat("c", 64) // var, not const: a const cannot be built from strings.Repeat

type fakeResolver struct {
	digest string
	err    error
}

func (f fakeResolver) ResolveDigest(context.Context, string, string, string) (string, error) {
	return f.digest, f.err
}

type fakeVerifier struct {
	wantRef string
	wantID  CertIdentity
	err     error
}

func (f fakeVerifier) Verify(_ context.Context, ref string, id CertIdentity) error {
	if ref != f.wantRef || id != f.wantID {
		return errors.New("verify called with unexpected ref or identity")
	}
	return f.err
}

func wantIdentity(owner, repoOnGitHub, version string) CertIdentity {
	return Identity(owner, repoOnGitHub, version)
}

// TestIdentityIsTheLiteralTagPushIdentity pins the exact strings a release's
// certificate must carry, written out rather than built by calling Identity
// again. Every other test here (and the fakeVerifier's own expectation)
// builds its want by calling Identity, so a change to the format — say
// "@refs/heads/main", or another repository — would move both sides of those
// comparisons together and pass; this test is the one that would not.
func TestIdentityIsTheLiteralTagPushIdentity(t *testing.T) {
	id := Identity("dimipaun", "fugaro", "0.7.5")
	if got, want := id.WorkflowRef, "https://github.com/dimipaun/fugaro/.github/workflows/images.yml@refs/tags/v0.7.5"; got != want {
		t.Errorf("WorkflowRef = %q, want %q (the tag push of this repository's images.yml)", got, want)
	}
	if got, want := id.Issuer, "https://token.actions.githubusercontent.com"; got != want {
		t.Errorf("Issuer = %q, want %q", got, want)
	}
}

func TestResolve(t *testing.T) {
	tr := fakeResolver{digest: testDigest}
	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	v := fakeVerifier{wantRef: ref, wantID: wantIdentity("dimipaun", "fugaro", "0.7.1")}
	// "base", not "fugaro-base": Resolve builds the full image name itself.
	got, err := Resolve(context.Background(), tr, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.1")
	want := "ghcr.io/dimipaun/fugaro-base:0.7.1@" + testDigest
	if err != nil || got != want {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
}

func TestResolveRefusesAFailedVerification(t *testing.T) {
	tr := fakeResolver{digest: testDigest}
	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	v := fakeVerifier{wantRef: ref, wantID: wantIdentity("dimipaun", "fugaro", "0.7.1"), err: errors.New("certificate identity mismatch")}
	if _, err := Resolve(context.Background(), tr, v, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.1"); err == nil || !strings.Contains(err.Error(), "certificate identity mismatch") {
		t.Errorf("got %v, want the injected verification error", err)
	}
}

func TestResolveRefusesAResolveError(t *testing.T) {
	tr := fakeResolver{err: errors.New("404")}
	if _, err := Resolve(context.Background(), tr, fakeVerifier{}, "dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.1"); err == nil {
		t.Error("want an error")
	}
}

// TestResolveNeverReturnsThePinnedSuffix: baseRefReleaseRE and every
// version-skew gate parse the returned reference, and "-pinned" would
// break them. The suffix belongs to the tag that was READ, not to the one
// that is written.
func TestResolveNeverReturnsThePinnedSuffix(t *testing.T) {
	got, err := Resolve(context.Background(), fakeResolver{digest: testDigest},
		fakeVerifier{wantRef: "ghcr.io/dimipaun/fugaro-base@" + testDigest, wantID: wantIdentity("dimipaun", "fugaro", "0.7.1")},
		"dimipaun", "fugaro", "ghcr.io/dimipaun", "base", "0.7.1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "-pinned") {
		t.Errorf("Resolve returned %q, which carries the -pinned suffix", got)
	}
}

// TestMirrorResolverReadsThePinnedTagNotThePlainOne: the whole point of
// the -pinned tag is that a nightly rebuild may move the plain version tag
// to an unsigned image. The resolver must ask for the other one, under the
// source's own owner (a fork's, not dimipaun's).
func TestMirrorResolverReadsThePinnedTagNotThePlainOne(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`)
	h := sha256.Sum256(body)
	d := "sha256:" + hex.EncodeToString(h[:])
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		w.Header().Set("Docker-Content-Digest", d)
		w.Write(body)
	}))
	defer srv.Close()

	m := &mirror.Mirror{Endpoint: func(string) string { return srv.URL }, Allow: []string{"ghcr.io/acme"}}
	got, err := MirrorResolver{M: m}.ResolveDigest(context.Background(), "ghcr.io/acme", "base", "0.7.1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != d {
		t.Errorf("digest = %q, want %q", got, d)
	}
	if want := "/v2/acme/fugaro-base/manifests/0.7.1-pinned"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

// TestMirrorResolverRefusesASourceThatIsNotRegistryAndOwner keeps a
// malformed --image-source from becoming a registry path.
func TestMirrorResolverRefusesASourceThatIsNotRegistryAndOwner(t *testing.T) {
	m := &mirror.Mirror{Allow: []string{"ghcr.io/acme"}}
	for _, bad := range []string{"ghcr.io", "", "/acme", "ghcr.io/acme@sha256:x"} {
		if _, err := (MirrorResolver{M: m}).ResolveDigest(context.Background(), bad, "base", "0.7.1"); err == nil {
			t.Errorf("source %q was accepted", bad)
		}
	}
}

// TestCosignVerifierPinsTheDigestAndTheIdentity is the one that keeps the
// shell-out honest: the reference must be a digest (cosign's own artifact
// policy), and the identity and issuer must be the release workflow's tag
// push — nothing looser, and no flag that would accept any signature.
func TestCosignVerifierPinsTheDigestAndTheIdentity(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "cosign")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := CosignBin
	CosignBin = bin
	t.Cleanup(func() { CosignBin = old })

	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	id := Identity("dimipaun", "fugaro", "0.7.1")
	if err := (CosignVerifier{}).Verify(context.Background(), ref, id); err != nil {
		t.Fatalf("verify: %v", err)
	}
	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{"verify", "--certificate-identity", id.WorkflowRef, "--certificate-oidc-issuer", id.Issuer, ref}
	if len(got) != len(want) {
		t.Fatalf("cosign argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cosign argv = %q, want %q", got, want)
		}
	}
	for _, a := range got {
		if a == "--insecure-ignore-tlog" || a == "--insecure-ignore-sct" || strings.Contains(a, "without") {
			t.Errorf("argv carries a relaxing flag: %q", a)
		}
	}
}

// TestCosignVerifierRefusesWhenCosignRejects: a non-zero exit is a
// refusal, and the message names what was expected and what cosign said.
func TestCosignVerifierRefusesWhenCosignRejects(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "cosign")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'no matching signatures' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := CosignBin
	CosignBin = bin
	t.Cleanup(func() { CosignBin = old })

	ref := "ghcr.io/dimipaun/fugaro-base@" + testDigest
	err := (CosignVerifier{}).Verify(context.Background(), ref, Identity("dimipaun", "fugaro", "0.7.1"))
	if err == nil {
		t.Fatal("a rejecting cosign must refuse")
	}
	for _, want := range []string{ref, "no matching signatures", "images.yml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q must name %q", err, want)
		}
	}
}

// TestCosignVerifierNamesAMissingBinary: the operator has to know it is
// cosign that is missing, not a signature.
func TestCosignVerifierNamesAMissingBinary(t *testing.T) {
	old := CosignBin
	CosignBin = filepath.Join(t.TempDir(), "not-here")
	t.Cleanup(func() { CosignBin = old })

	err := (CosignVerifier{}).Verify(context.Background(), "ghcr.io/dimipaun/fugaro-base@"+testDigest, Identity("dimipaun", "fugaro", "0.7.1"))
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("err = %v, want a missing-binary refusal", err)
	}
}
