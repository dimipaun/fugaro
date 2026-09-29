package imagecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"google.golang.org/api/option"
	htransport "google.golang.org/api/transport/http"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// ErrManifestUnknown means the registry has no such tag or digest.
var ErrManifestUnknown = errors.New("the registry has no such manifest")

// manifestAccept are the manifest types a digest may be of: a multi-arch
// index or a single-platform manifest, OCI or Docker.
var manifestAccept = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
}

var (
	repoNameRE  = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	tagRE       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	arHostRE    = regexp.MustCompile(`^[a-z0-9-]+-docker\.pkg\.dev$`)
	cloudScopes = []string{"https://www.googleapis.com/auth/cloud-platform"}
)

// Registry reads image digests with a HEAD of the manifest (the OCI
// distribution API). Artifact Registry (*-docker.pkg.dev) is read with
// Google's Application Default Credentials: the job account's metadata
// server token in the check job, the operator's ADC locally. Any other
// registry, ghcr.io included, is read anonymously, with a pull token
// from the registry's own token endpoint when it asks for one.
type Registry struct {
	// HTTP reads the anonymous registries; nil is a plain client.
	HTTP *http.Client
	// Google reads Artifact Registry; nil is an ADC client, made on first
	// use.
	Google *http.Client
	// Endpoint is a registry host's base URL; nil is https://<host>.
	Endpoint func(host string) string

	once      sync.Once
	googleErr error
}

// Digest is the digest ref's manifest has now: ref itself when it is
// pinned by digest. A missing tag is ErrManifestUnknown.
func (r *Registry) Digest(ctx context.Context, ref string) (string, error) {
	host, name, tag, err := parseImageRef(ref)
	if err != nil {
		return "", err
	}
	if gcp.IsDigest(tag) {
		return tag, nil
	}
	base := "https://" + host
	if r.Endpoint != nil {
		base = strings.TrimSuffix(r.Endpoint(host), "/")
	}
	u := base + "/v2/" + name + "/manifests/" + tag
	if arHostRE.MatchString(host) {
		hc, err := r.googleClient(ctx)
		if err != nil {
			return "", err
		}
		return headManifest(ctx, hc, u, "", ref)
	}
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	d, err := headManifest(ctx, hc, u, "", ref)
	var ch *challengeError
	if !errors.As(err, &ch) {
		return d, err
	}
	token, err := anonymousToken(ctx, hc, ch.header, name, base)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ref, err)
	}
	return headManifest(ctx, hc, u, token, ref)
}

func (r *Registry) googleClient(ctx context.Context) (*http.Client, error) {
	if r.Google != nil {
		return r.Google, nil
	}
	r.once.Do(func() {
		// The client outlives ctx: it serves every later read.
		r.Google, _, r.googleErr = htransport.NewClient(context.WithoutCancel(ctx), option.WithScopes(cloudScopes...))
	})
	if r.googleErr != nil {
		return nil, fmt.Errorf("Google credentials for Artifact Registry: %w", r.googleErr)
	}
	return r.Google, nil
}

// challengeError is a 401 that names a bearer token endpoint.
type challengeError struct{ header string }

func (e *challengeError) Error() string { return "the registry asks for a token" }

func headManifest(ctx context.Context, hc *http.Client, u, token, ref string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", strings.Join(manifestAccept, ", "))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("reading %s's manifest: %w", ref, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusOK:
		d := resp.Header.Get("Docker-Content-Digest")
		if !gcp.IsDigest(d) {
			return "", fmt.Errorf("the registry sent %s's manifest without a sha256 digest", ref)
		}
		return d, nil
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("%s: %w", ref, ErrManifestUnknown)
	case resp.StatusCode == http.StatusUnauthorized && token == "":
		if h := resp.Header.Get("WWW-Authenticate"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return "", &challengeError{header: h}
		}
	}
	return "", fmt.Errorf("reading %s's manifest: HTTP %d", ref, resp.StatusCode)
}

// anonymousToken fetches a pull token for name from the token endpoint a
// Bearer challenge names, without credentials. The endpoint must be https,
// or on the registry's own host (base): a plain-http URL elsewhere, such
// as the metadata server, is never asked.
func anonymousToken(ctx context.Context, hc *http.Client, header, name, base string) (string, error) {
	params := parseChallenge(header[len("bearer "):])
	realm, err := url.Parse(params["realm"])
	reg, _ := url.Parse(base)
	if err != nil || realm.Host == "" || (realm.Scheme != "https" && (reg == nil || realm.Scheme != reg.Scheme || realm.Host != reg.Host)) {
		return "", fmt.Errorf("the registry's token endpoint %q is not an https URL", params["realm"])
	}
	q := realm.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	q.Set("scope", "repository:"+name+":pull")
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("getting an anonymous pull token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("getting an anonymous pull token: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("the anonymous pull token: %w", err)
	}
	if body.Token == "" {
		body.Token = body.AccessToken
	}
	if body.Token == "" {
		return "", errors.New("the registry's token endpoint returned no token")
	}
	return body.Token, nil
}

// parseChallenge parses the key="value" pairs of a WWW-Authenticate
// challenge's parameters.
func parseChallenge(s string) map[string]string {
	out := map[string]string{}
	for s = strings.TrimSpace(s); s != ""; {
		k, rest, ok := strings.Cut(s, "=")
		if !ok {
			break
		}
		k = strings.ToLower(strings.TrimSpace(k))
		var v string
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				break
			}
			v, rest = rest[1:end+1], rest[end+2:]
		} else {
			v, rest, _ = strings.Cut(rest, ",")
		}
		out[k] = v
		s = strings.TrimLeft(strings.TrimSpace(rest), ",")
		s = strings.TrimSpace(s)
	}
	return out
}

// parseImageRef splits an image reference into its registry host, its
// repository name and its tag or digest (latest when neither is given).
// A reference without a registry host is Docker Hub's.
func parseImageRef(ref string) (host, name, tagOrDigest string, err error) {
	bad := func() (string, string, string, error) {
		return "", "", "", fmt.Errorf("the image %q is not a registry/name[:tag|@digest] reference", ref)
	}
	rest := ref
	if i := strings.Index(rest, "@"); i >= 0 {
		rest, tagOrDigest = rest[:i], rest[i+1:]
		if !gcp.IsDigest(tagOrDigest) {
			return bad()
		}
	}
	if slash := strings.LastIndex(rest, "/"); tagOrDigest == "" {
		if colon := strings.LastIndex(rest, ":"); colon > slash {
			rest, tagOrDigest = rest[:colon], rest[colon+1:]
			if !tagRE.MatchString(tagOrDigest) {
				return bad()
			}
		} else {
			tagOrDigest = "latest"
		}
	}
	first, remainder, ok := strings.Cut(rest, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		host, name = first, remainder
	} else {
		host, name = "registry-1.docker.io", rest
		if !strings.Contains(name, "/") {
			name = "library/" + name
		}
	}
	if !repoNameRE.MatchString(name) {
		return bad()
	}
	return host, name, tagOrDigest, nil
}
