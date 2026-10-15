package mirror

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// remoteTransport sends every request to the test server, whatever host
// the caller named, so a test can serve the remote repository's path
// without a real <region>-docker.pkg.dev.
type remoteTransport struct{ to string }

func (r remoteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	u.Scheme, u.Host = "http", strings.TrimPrefix(r.to, "http://")
	rq := req.Clone(req.Context())
	rq.URL = &u
	return http.DefaultTransport.RoundTrip(rq)
}

// TestRemoteRefCarriesTheTagAndTheDigest: the reference keeps the version
// as a tag and the content as a digest, in the one order the grammar
// allows (distribution/reference's referencePat: name[:tag][@digest]).
func TestRemoteRefCarriesTheTagAndTheDigest(t *testing.T) {
	got := RemoteRef("us-east5-docker.pkg.dev", "fugaro-dev", "fugaro-ghcr",
		"dimipaun/fugaro-base", "0.7.1", "sha256:"+strings.Repeat("a", 64))
	want := "us-east5-docker.pkg.dev/fugaro-dev/fugaro-ghcr/dimipaun/fugaro-base:0.7.1@sha256:" + strings.Repeat("a", 64)
	if got != want {
		t.Errorf("RemoteRef = %q, want %q", got, want)
	}
}

// TestVerifyThroughRemoteReadsTheRemotePath: the read-back asks for the
// digest at the remote repository's own path, with the caller's client,
// and accepts bytes that hash to exactly that digest. The path is the
// shape design §2.2 infers and §2.12 a leaves to the owner's Check 43c.
func TestVerifyThroughRemoteReadsTheRemotePath(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json"}`)
	digest := sum(body)
	var path, accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, accept = r.URL.Path, r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		w.Header().Set("Docker-Content-Digest", digest)
		w.Write(body)
	}))
	defer srv.Close()

	c := &http.Client{Transport: remoteTransport{srv.URL}}
	if err := VerifyThroughRemote(context.Background(), c, "us-east5-docker.pkg.dev", "fugaro-dev", "fugaro-ghcr", "dimipaun/fugaro-base", digest); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if want := "/v2/fugaro-dev/fugaro-ghcr/dimipaun/fugaro-base/manifests/" + digest; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if accept != acceptManifest {
		t.Errorf("Accept = %q, want %q", accept, acceptManifest)
	}
}

// TestVerifyThroughRemoteRefusesAMismatchedDigest: a registry that serves
// a different digest than the one asked for is a failure, not a warning —
// the caller pins what this function proved, and nothing else.
func TestVerifyThroughRemoteRefusesAMismatchedDigest(t *testing.T) {
	want := "sha256:" + strings.Repeat("a", 64)
	other := sum([]byte("another manifest"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		w.Header().Set("Docker-Content-Digest", other)
		w.Write([]byte("another manifest"))
	}))
	defer srv.Close()

	c := &http.Client{Transport: remoteTransport{srv.URL}}
	err := VerifyThroughRemote(context.Background(), c, "us-east5-docker.pkg.dev", "p", "fugaro-ghcr", "dimipaun/fugaro-base", want)
	if err == nil || !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want a digest mismatch", err)
	}
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), other) {
		t.Errorf("refusal %q must name both digests", err)
	}
}

// TestVerifyThroughRemoteHashesTheBodyNotTheHeader is the one that makes
// the read-back worth doing: the server sends the digest the caller asked
// for in Docker-Content-Digest and a body that hashes to something else.
// A header-only implementation passes this test and pins bytes nobody
// verified; the body-hashing one refuses.
func TestVerifyThroughRemoteHashesTheBodyNotTheHeader(t *testing.T) {
	want := "sha256:" + strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		w.Header().Set("Docker-Content-Digest", want)
		w.Write([]byte("bytes nobody verified"))
	}))
	defer srv.Close()

	c := &http.Client{Transport: remoteTransport{srv.URL}}
	err := VerifyThroughRemote(context.Background(), c, "us-east5-docker.pkg.dev", "p", "fugaro-ghcr", "dimipaun/fugaro-base", want)
	if err == nil || !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want a digest mismatch: the header is the proxy's word about someone else's bytes", err)
	}
	if !strings.Contains(err.Error(), sum([]byte("bytes nobody verified"))) {
		t.Errorf("refusal %q must name the digest the bytes actually hash to", err)
	}
}

// TestVerifyThroughRemoteNamesAMissingUpstream: a 404 is the documented
// shape of "the requested artifact doesn't exist in the upstream
// repository" (design §2.10), so it reads as a clear statement about the
// remote repository and the digest rather than a raw status code.
func TestVerifyThroughRemoteNamesAMissingUpstream(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := &http.Client{Transport: remoteTransport{srv.URL}}
	err := VerifyThroughRemote(context.Background(), c, "us-east5-docker.pkg.dev", "p", "fugaro-ghcr", "dimipaun/fugaro-base", digest)
	if err == nil {
		t.Fatal("a 404 must refuse")
	}
	for _, want := range []string{"fugaro-ghcr", digest, "404"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q must name %q", err, want)
		}
	}
}
