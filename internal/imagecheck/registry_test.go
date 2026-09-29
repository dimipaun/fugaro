package imagecheck

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// bearer adds an OAuth token, as the ADC client does.
type bearer struct {
	token string
	used  *int
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	*b.used++
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// refuse fails the test when a registry is reached with the wrong client.
type refuse struct{ t *testing.T }

func (f refuse) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("the wrong client called %s", r.URL)
	return nil, errors.New("refused")
}

func wantAccepts(t *testing.T, accepts []string) {
	t.Helper()
	if len(accepts) == 0 {
		t.Fatal("no manifest request")
	}
	for _, a := range accepts {
		for _, mt := range []string{
			"application/vnd.oci.image.index.v1+json",
			"application/vnd.docker.distribution.manifest.list.v2+json",
			"application/vnd.docker.distribution.manifest.v2+json",
		} {
			if !strings.Contains(a, mt) {
				t.Errorf("Accept %q lacks %s", a, mt)
			}
		}
	}
}

// TestBaseDigestAR: an Artifact Registry image is read with the ADC
// client's bearer token.
func TestBaseDigestAR(t *testing.T) {
	fake := gcpfake.NewRegistry(t)
	fake.Bearer = "ya29.test"
	const name = "proj-1234/fugaro-base/fugaro-web-node"
	fake.SetManifest(name, "dev-abc", digestA)
	used := 0
	r := &Registry{
		HTTP:     &http.Client{Transport: refuse{t}},
		Google:   &http.Client{Transport: bearer{"ya29.test", &used}},
		Endpoint: func(string) string { return fake.URL },
	}
	got, err := r.Digest(context.Background(), "us-east5-docker.pkg.dev/"+name+":dev-abc")
	if err != nil || got != digestA {
		t.Fatalf("Digest = %q, %v", got, err)
	}
	if used == 0 {
		t.Error("the ADC client was not used")
	}
	wantAccepts(t, fake.Accepts())
	if _, err := r.Digest(context.Background(), "us-east5-docker.pkg.dev/"+name+":missing"); !errors.Is(err, ErrManifestUnknown) {
		t.Fatalf("missing tag: %v", err)
	}

	// Without the token, the registry refuses: that is an error, not a
	// missing image.
	r.Google = &http.Client{}
	if _, err := r.Digest(context.Background(), "us-east5-docker.pkg.dev/"+name+":dev-abc"); err == nil || errors.Is(err, ErrManifestUnknown) {
		t.Fatalf("unauthenticated: %v", err)
	}
}

// TestBaseDigestAnonymous: ghcr.io (or any registry but Artifact
// Registry) is read with an anonymous pull token from its token endpoint.
func TestBaseDigestAnonymous(t *testing.T) {
	fake := gcpfake.NewRegistry(t)
	fake.Anonymous = true
	fake.SetManifest("acme/fugaro-base", "1.2.3", digestB)
	r := &Registry{
		HTTP:     &http.Client{},
		Google:   &http.Client{Transport: refuse{t}},
		Endpoint: func(string) string { return fake.URL },
	}
	got, err := r.Digest(context.Background(), "ghcr.io/acme/fugaro-base:1.2.3")
	if err != nil || got != digestB {
		t.Fatalf("Digest = %q, %v", got, err)
	}
	if fake.TokenRequests() != 1 {
		t.Errorf("token requests = %d", fake.TokenRequests())
	}
	wantAccepts(t, fake.Accepts())

	// A token endpoint on plain http elsewhere than the registry (such as
	// the metadata server) is never asked.
	other := &Registry{HTTP: &http.Client{}, Endpoint: func(string) string { return strings.Replace(fake.URL, "127.0.0.1", "localhost", 1) }}
	if _, err := other.Digest(context.Background(), "ghcr.io/acme/fugaro-base:1.2.3"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("foreign http realm: %v", err)
	}
	if fake.TokenRequests() != 1 {
		t.Errorf("token requests = %d", fake.TokenRequests())
	}

	// A pinned reference is its own digest.
	if got, err := r.Digest(context.Background(), "ghcr.io/acme/fugaro-base@"+digestA); err != nil || got != digestA {
		t.Fatalf("pinned = %q, %v", got, err)
	}
}

func TestParseImageRef(t *testing.T) {
	cases := []struct{ ref, host, name, tag string }{
		{"us-east5-docker.pkg.dev/p/r/img:dev-1", "us-east5-docker.pkg.dev", "p/r/img", "dev-1"},
		{"ghcr.io/acme/base", "ghcr.io", "acme/base", "latest"},
		{"localhost:5000/a/b:1", "localhost:5000", "a/b", "1"},
		{"ubuntu:24.04", "registry-1.docker.io", "library/ubuntu", "24.04"},
		{"acme/base:2", "registry-1.docker.io", "acme/base", "2"},
		{"ghcr.io/acme/base@" + digestA, "ghcr.io", "acme/base", digestA},
	}
	for _, c := range cases {
		host, name, ref, err := parseImageRef(c.ref)
		if err != nil || host != c.host || name != c.name || ref != c.tag {
			t.Errorf("%s: %s %s %s %v", c.ref, host, name, ref, err)
		}
	}
	for _, bad := range []string{"", "ghcr.io/", "ghcr.io/a b:1", "ghcr.io/a:"} {
		if _, _, _, err := parseImageRef(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if !slices.Contains(manifestAccept, "application/vnd.oci.image.index.v1+json") {
		t.Error("no OCI index in Accept")
	}
}
