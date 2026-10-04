package mirror

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Limits bound what a copy will read and send, so a hostile or broken
// source cannot fill the disk, the registry or the bill.
type Limits struct {
	Manifest int64 // an index, an image manifest or a config
	Blob     int64 // one layer
	Total    int64 // every blob of the image
	Layers   int
}

// DefaultLimits fit the published images (about 1.5 GB, 2.1 GB for
// java-services, compressed) with headroom.
var DefaultLimits = Limits{Manifest: 4 << 20, Blob: 6 << 30, Total: 12 << 30, Layers: 256}

const (
	mtOCIManifest  = "application/vnd.oci.image.manifest.v1+json"
	mtOCIIndex     = "application/vnd.oci.image.index.v1+json"
	mtDockerManif  = "application/vnd.docker.distribution.manifest.v2+json"
	mtDockerList   = "application/vnd.docker.distribution.manifest.list.v2+json"
	acceptManifest = mtOCIManifest + ", " + mtOCIIndex + ", " + mtDockerManif + ", " + mtDockerList
)

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Mirror copies images. The zero value of every field but Token is usable.
type Mirror struct {
	// Token is the person's credential for the destination registry (their
	// Application Default Credentials). It is sent to the destination host
	// only; the source never sees it.
	Token oauth2.TokenSource
	// Allow are the allowed source prefixes; nil is DefaultSources.
	Allow  []string
	Limits Limits
	// Transport carries every request (nil: a default with sane timeouts).
	// It must not add credentials: the mirror adds the destination token
	// itself, to the destination host alone.
	Transport http.RoundTripper
	// Endpoint maps a registry host to its base URL; nil is https://<host>.
	// For tests.
	Endpoint func(host string) string
	// Log receives one line per blob and per manifest (digests and sizes,
	// never a credential or a URL's query).
	Log func(string)

	allowHTTPRedirect bool // tests only: the fake CDN is plain http
	tokens            map[string]string
}

func (m *Mirror) allow() []string {
	if m.Allow != nil {
		return m.Allow
	}
	return DefaultSources
}

func (m *Mirror) limits() Limits {
	if m.Limits == (Limits{}) {
		return DefaultLimits
	}
	return m.Limits
}

func (m *Mirror) logf(format string, a ...any) {
	if m.Log != nil {
		m.Log(fmt.Sprintf(format, a...))
	}
}

func (m *Mirror) base(host string) string {
	if m.Endpoint != nil {
		return strings.TrimSuffix(m.Endpoint(host), "/")
	}
	return "https://" + host
}

func (m *Mirror) transport() http.RoundTripper {
	if m.Transport != nil {
		return m.Transport
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 2 * time.Minute
	return t
}

// Blob is a layer or the config of the image being copied.
type Blob struct {
	Digest  string
	Size    int64
	Present bool // already in the destination
}

// Plan is what a copy would do, read without changing anything.
type Plan struct {
	Source, Dest Ref
	// SourceDigest is what the source tag resolved to (an index, or the
	// image); Digest is the image manifest that is copied (the linux/amd64
	// entry of an index).
	SourceDigest, Digest string
	MediaType            string
	Manifest             []byte
	Blobs                []Blob
	// Present: the destination tag already is Digest; nothing to copy.
	Present                   bool
	TotalBytes, TransferBytes int64
}

// Plan reads the source and the destination and says what Copy would do. A
// non-empty expect is the digest the source tag must resolve to (for
// example from the release notes); another is refused.
func (m *Mirror) Plan(ctx context.Context, src, dst Ref, expect string) (*Plan, error) {
	if err := m.checkSource(src); err != nil {
		return nil, err
	}
	if err := checkDest(dst); err != nil {
		return nil, err
	}
	top, topDigest, topType, err := m.getManifest(ctx, src, src.Tag)
	if err != nil {
		return nil, err
	}
	if expect != "" && topDigest != expect {
		return nil, fmt.Errorf("%w: %s resolves to %s, expected %s", ErrDigestMismatch, src, topDigest, expect)
	}
	p := &Plan{Source: src, Dest: dst, SourceDigest: topDigest, Digest: topDigest, MediaType: topType, Manifest: top}
	if isIndex(topType) {
		child, err := amd64Entry(top)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src, err)
		}
		p.Manifest, p.Digest, p.MediaType, err = m.getChild(ctx, src, child)
		if err != nil {
			return nil, err
		}
	}
	cfg, layers, err := parseImage(p.Manifest, p.MediaType, m.limits())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	// The platform is read from the config, whatever the index claimed.
	cfgBytes, err := m.getBlobBytes(ctx, src, cfg)
	if err != nil {
		return nil, err
	}
	plat := platformOf(cfgBytes)
	if plat.OS != "linux" || plat.Architecture != "amd64" {
		return nil, fmt.Errorf("%s: %w (it is %s/%s)", src, ErrNoAmd64, plat.OS, plat.Architecture)
	}
	for _, b := range append([]Blob{cfg}, layers...) {
		ok, err := m.destHasBlob(ctx, dst, b)
		if err != nil {
			return nil, err
		}
		b.Present = ok
		p.Blobs = append(p.Blobs, b)
		p.TotalBytes += b.Size
		if !ok {
			p.TransferBytes += b.Size
		}
	}
	if d, err := m.destDigest(ctx, dst); err != nil {
		return nil, err
	} else if d == p.Digest {
		p.Present = true
		p.TransferBytes = 0
	}
	return p, nil
}

func platformOf(cfg []byte) struct{ OS, Architecture string } {
	var v struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	}
	_ = json.Unmarshal(cfg, &v)
	return struct{ OS, Architecture string }{v.OS, v.Architecture}
}

// Result is what a copy did.
type Result struct {
	Digest      string
	Changed     bool
	BlobsSent   int
	BlobsKept   int
	BytesSent   int64
	Description string
}

// Copy makes the destination tag name the planned image: the missing blobs
// first (each checked against its digest before it is finalized), the
// source tag read once more (a move is refused), and the manifest last. The
// pushed tag is read back and compared. A plan whose destination already
// holds the digest does nothing.
func (m *Mirror) Copy(ctx context.Context, p *Plan) (Result, error) {
	if p.Present {
		return Result{Digest: p.Digest, BlobsKept: len(p.Blobs), Description: "No changes"}, nil
	}
	if err := m.checkSource(p.Source); err != nil {
		return Result{}, err
	}
	if err := checkDest(p.Dest); err != nil {
		return Result{}, err
	}
	var res Result
	res.Digest = p.Digest
	for _, b := range p.Blobs {
		if ok, err := m.destHasBlob(ctx, p.Dest, b); err != nil {
			return res, err
		} else if ok {
			res.BlobsKept++
			continue
		}
		if err := m.copyBlob(ctx, p.Source, p.Dest, b); err != nil {
			return res, err
		}
		res.BlobsSent++
		res.BytesSent += b.Size
	}
	// A tag that moved after the plan would mean the blobs are no longer the
	// image the source names: nothing is tagged.
	_, again, _, err := m.getManifest(ctx, p.Source, p.Source.Tag)
	if err != nil {
		return res, err
	}
	if again != p.SourceDigest {
		return res, fmt.Errorf("%w: %s was %s and is now %s; nothing was tagged", ErrTagMoved, p.Source, p.SourceDigest, again)
	}
	if err := m.putManifest(ctx, p); err != nil {
		return res, err
	}
	res.Changed = true
	res.Description = fmt.Sprintf("copied %s as %s (%d blobs sent, %d already there)", p.Source, p.Dest, res.BlobsSent, res.BlobsKept)
	return res, nil
}

func (m *Mirror) checkSource(r Ref) error {
	if _, err := ParseSource(m.allow(), r.String()); err != nil {
		return err
	}
	return nil
}

func checkDest(r Ref) error {
	m := regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+-docker\.pkg\.dev$`)
	parts := strings.Split(r.Repo, "/")
	if !m.MatchString(r.Host) || len(parts) != 3 || parts[1] != BaseRepository || !tagRE.MatchString(r.Tag) {
		return fmt.Errorf("destination %s is not an image in an Artifact Registry %s repository", r, BaseRepository)
	}
	return nil
}

func isIndex(mt string) bool { return mt == mtOCIIndex || mt == mtDockerList }

// amd64Entry is the digest of the linux/amd64 image in an index.
func amd64Entry(index []byte) (string, error) {
	var ix struct {
		Manifests []struct {
			Digest    string `json:"digest"`
			MediaType string `json:"mediaType"`
			Platform  *struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(index, &ix); err != nil {
		return "", fmt.Errorf("its index is not JSON: %w", err)
	}
	for _, e := range ix.Manifests {
		if e.Platform != nil && e.Platform.OS == "linux" && e.Platform.Architecture == "amd64" {
			if !digestRE.MatchString(e.Digest) {
				return "", fmt.Errorf("its linux/amd64 entry has the digest %q", e.Digest)
			}
			return e.Digest, nil
		}
	}
	return "", ErrNoAmd64
}

// parseImage reads an image manifest: its config and layers, checked for
// well-formed digests, sizes within the limits and no foreign layers.
func parseImage(raw []byte, mediaType string, lim Limits) (cfg Blob, layers []Blob, err error) {
	var im struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Config        struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"config"`
		Layers []struct {
			Digest string   `json:"digest"`
			Size   int64    `json:"size"`
			URLs   []string `json:"urls"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(raw, &im); err != nil {
		return cfg, nil, fmt.Errorf("its manifest is not JSON: %w", err)
	}
	if im.SchemaVersion != 2 || (im.MediaType != "" && im.MediaType != mediaType) {
		return cfg, nil, errors.New("its manifest is not a schema 2 image manifest of the type it was served as")
	}
	if len(im.Layers) == 0 || len(im.Layers) > lim.Layers {
		return cfg, nil, fmt.Errorf("its manifest has %d layers (1 to %d allowed)", len(im.Layers), lim.Layers)
	}
	var total int64
	check := func(d string, size int64, max int64) error {
		switch {
		case !digestRE.MatchString(d):
			return fmt.Errorf("a blob has the digest %q", d)
		case size <= 0 || size > max:
			return fmt.Errorf("blob %s is %d bytes (at most %d)", d, size, max)
		}
		total += size
		return nil
	}
	if err := check(im.Config.Digest, im.Config.Size, lim.Manifest); err != nil {
		return cfg, nil, err
	}
	cfg = Blob{Digest: im.Config.Digest, Size: im.Config.Size}
	seen := map[string]bool{cfg.Digest: true}
	for _, l := range im.Layers {
		if len(l.URLs) > 0 {
			return cfg, nil, fmt.Errorf("layer %s is a foreign layer (urls): not copied", l.Digest)
		}
		if err := check(l.Digest, l.Size, lim.Blob); err != nil {
			return cfg, nil, err
		}
		if !seen[l.Digest] {
			seen[l.Digest] = true
			layers = append(layers, Blob{Digest: l.Digest, Size: l.Size})
		}
	}
	if total > lim.Total {
		return cfg, nil, fmt.Errorf("the image is %d bytes (at most %d)", total, lim.Total)
	}
	return cfg, layers, nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// ---- source side: anonymous ----

// srcClient never carries a credential of ours; redirects (ghcr sends
// blobs to a CDN) are followed to https only, with no Authorization.
func (m *Mirror) srcClient(host string) *http.Client {
	base, _ := url.Parse(m.base(host))
	return &http.Client{Transport: m.transport(), CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && !m.allowHTTPRedirect && (base == nil || req.URL.Host != base.Host) {
			return fmt.Errorf("refusing a redirect to %s: not https", redact(req.URL))
		}
		req.Header.Del("Authorization")
		return nil
	}}
}

// redact is a URL without its query or credentials, safe to print.
func redact(u *url.URL) string {
	c := *u
	c.RawQuery, c.User, c.Fragment = "", nil, ""
	return c.String()
}

// srcGet reads path from the source anonymously, getting a pull token from
// the registry's own token endpoint when it asks for one (no credentials
// go to it). The caller closes the body.
func (m *Mirror) srcGet(ctx context.Context, src Ref, path, accept string) (*http.Response, error) {
	hc := m.srcClient(src.Host)
	u := m.base(src.Host) + "/v2/" + src.Repo + path
	do := func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return hc.Do(req)
	}
	key := src.Host + "/" + src.Repo
	resp, err := do(m.tokens[key])
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", src, urlErr(err))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		h := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return nil, fmt.Errorf("%w: %s asks for credentials", ErrUnreadableSource, src)
		}
		tok, err := m.anonymousToken(ctx, hc, h, src)
		if err != nil {
			return nil, err
		}
		if m.tokens == nil {
			m.tokens = map[string]string{}
		}
		m.tokens[key] = tok
		if resp, err = do(tok); err != nil {
			return nil, fmt.Errorf("reading %s: %w", src, urlErr(err))
		}
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp, nil
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		resp.Body.Close()
		return nil, fmt.Errorf("%w: %s answered HTTP %d (a release's images are public; if this is one, the package is not public, which is a release bug; a missing tag means this fugaro version has no published image)", ErrUnreadableSource, src, resp.StatusCode)
	}
	resp.Body.Close()
	return nil, fmt.Errorf("reading %s: HTTP %d", src, resp.StatusCode)
}

// urlErr drops the URL (and so any query) from a transport error.
func urlErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s %s: %w", ue.Op, redactStr(ue.URL), ue.Err)
	}
	return err
}

func redactStr(s string) string {
	if u, err := url.Parse(s); err == nil {
		return redact(u)
	}
	return "(url)"
}

func (m *Mirror) anonymousToken(ctx context.Context, hc *http.Client, header string, src Ref) (string, error) {
	params := parseChallenge(header[len("bearer "):])
	realm, err := url.Parse(params["realm"])
	reg, _ := url.Parse(m.base(src.Host))
	if err != nil || realm.Host == "" || (realm.Scheme != "https" && (reg == nil || realm.Scheme != reg.Scheme || realm.Host != reg.Host)) {
		return "", fmt.Errorf("%w: %s's token endpoint %q is not an https URL", ErrUnreadableSource, src.Host, params["realm"])
	}
	q := realm.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	q.Set("scope", "repository:"+src.Repo+":pull")
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req) // no Authorization: the source gets no credential
	if err != nil {
		return "", fmt.Errorf("getting an anonymous pull token from %s: %w", src.Host, urlErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: the anonymous pull token from %s was refused (HTTP %d)", ErrUnreadableSource, src.Host, resp.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return "", fmt.Errorf("the anonymous pull token: %w", err)
	}
	if body.Token == "" {
		body.Token = body.AccessToken
	}
	if body.Token == "" {
		return "", fmt.Errorf("%w: %s's token endpoint returned no token", ErrUnreadableSource, src.Host)
	}
	return body.Token, nil
}

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
		s = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest), ","))
	}
	return out
}

// readCapped reads at most max bytes of body and refuses more.
func readCapped(body io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("a response is larger than %d bytes", max)
	}
	return b, nil
}

// getManifest reads the manifest ref names (a tag or a digest) and returns
// its bytes, their digest (computed, and equal to the one a digest ref names
// and to the registry's header when it sends one) and its media type.
func (m *Mirror) getManifest(ctx context.Context, src Ref, ref string) (raw []byte, digest, mediaType string, err error) {
	resp, err := m.srcGet(ctx, src, "/manifests/"+ref, acceptManifest)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	raw, err = readCapped(resp.Body, m.limits().Manifest)
	if err != nil {
		return nil, "", "", fmt.Errorf("reading %s's manifest: %w", src, err)
	}
	digest = sum(raw)
	if digestRE.MatchString(ref) && digest != ref {
		return nil, "", "", fmt.Errorf("%w: %s's manifest %s hashes to %s", ErrDigestMismatch, src, ref, digest)
	}
	if h := resp.Header.Get("Docker-Content-Digest"); h != "" && h != digest {
		return nil, "", "", fmt.Errorf("%w: %s's manifest hashes to %s but the registry says %s", ErrDigestMismatch, src, digest, h)
	}
	mediaType = strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	switch mediaType {
	case mtOCIManifest, mtOCIIndex, mtDockerManif, mtDockerList:
	default:
		return nil, "", "", fmt.Errorf("%s's manifest has the media type %q", src, mediaType)
	}
	return raw, digest, mediaType, nil
}

func (m *Mirror) getChild(ctx context.Context, src Ref, digest string) ([]byte, string, string, error) {
	raw, d, mt, err := m.getManifest(ctx, src, digest)
	if err == nil && isIndex(mt) {
		err = fmt.Errorf("%s: the linux/amd64 entry is itself an index", src)
	}
	return raw, d, mt, err
}

// getBlobBytes reads a small blob (the config) and checks its digest.
func (m *Mirror) getBlobBytes(ctx context.Context, src Ref, b Blob) ([]byte, error) {
	resp, err := m.srcGet(ctx, src, "/blobs/"+b.Digest, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := readCapped(resp.Body, b.Size)
	if err != nil {
		return nil, fmt.Errorf("reading %s's config %s: %w", src, b.Digest, err)
	}
	if int64(len(raw)) != b.Size || sum(raw) != b.Digest {
		return nil, fmt.Errorf("%w: %s's config is not %s (%d bytes)", ErrDigestMismatch, src, b.Digest, b.Size)
	}
	return raw, nil
}

// ---- destination side: the person's token, this host only ----

// dstClient sends the person's token to the destination host and to nothing
// else; it follows no redirect.
func (m *Mirror) dstClient(host string) *http.Client {
	base, _ := url.Parse(m.base(host))
	return &http.Client{
		Transport: &authTransport{rt: m.transport(), host: base.Host, ts: m.Token},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type authTransport struct {
	rt   http.RoundTripper
	host string
	ts   oauth2.TokenSource
}

func (a *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != a.host {
		return nil, fmt.Errorf("refusing to send the registry credential to %s", req.URL.Host)
	}
	if a.ts == nil {
		return nil, errors.New("no credential for the destination registry")
	}
	tok, err := a.ts.Token()
	if err != nil {
		return nil, fmt.Errorf("no Google credentials for Artifact Registry: run gcloud auth application-default login (%v)", err)
	}
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	return a.rt.RoundTrip(r)
}

func (m *Mirror) dstDo(ctx context.Context, dst Ref, method, path string, hdr map[string]string, body io.Reader, size int64) (*http.Response, error) {
	u := m.base(dst.Host) + "/v2/" + dst.Repo + path
	return m.dstDoURL(ctx, dst, method, u, hdr, body, size)
}

func (m *Mirror) dstDoURL(ctx context.Context, dst Ref, method, u string, hdr map[string]string, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := m.dstClient(dst.Host).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, dst, urlErr(err))
	}
	return resp, nil
}

func authRefusal(dst Ref, status int) error {
	return fmt.Errorf("Artifact Registry refused %s (HTTP %d): your credentials need roles/artifactregistry.writer on the fugaro-base repository (the operator role has it), and gcloud auth application-default login must be current", dst, status)
}

func drain(r *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<16))
	r.Body.Close()
}

func (m *Mirror) destHasBlob(ctx context.Context, dst Ref, b Blob) (bool, error) {
	resp, err := m.dstDo(ctx, dst, http.MethodHead, "/blobs/"+b.Digest, nil, nil, 0)
	if err != nil {
		return false, err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		if cl := resp.ContentLength; cl >= 0 && cl != b.Size {
			return false, fmt.Errorf("the destination has a blob named %s of %d bytes, not %d: not touching it", b.Digest, cl, b.Size)
		}
		return true, nil
	case http.StatusNotFound:
		return false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, authRefusal(dst, resp.StatusCode)
	}
	return false, fmt.Errorf("reading %s's blob %s: HTTP %d", dst, b.Digest, resp.StatusCode)
}

// destDigest is the digest the destination tag has now, "" when absent.
func (m *Mirror) destDigest(ctx context.Context, dst Ref) (string, error) {
	resp, err := m.dstDo(ctx, dst, http.MethodHead, "/manifests/"+dst.Tag, map[string]string{"Accept": acceptManifest}, nil, 0)
	if err != nil {
		return "", err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		d := resp.Header.Get("Docker-Content-Digest")
		if !digestRE.MatchString(d) {
			return "", fmt.Errorf("%s answered without a digest", dst)
		}
		return d, nil
	case http.StatusNotFound:
		return "", nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", authRefusal(dst, resp.StatusCode)
	}
	return "", fmt.Errorf("reading %s: HTTP %d", dst, resp.StatusCode)
}

// copyBlob streams one blob from the source into the destination, hashing it
// on the way, and finalizes the upload only when the bytes are exactly the
// digest (a blob that does not match is never committed).
func (m *Mirror) copyBlob(ctx context.Context, src, dst Ref, b Blob) error {
	in, err := m.srcGet(ctx, src, "/blobs/"+b.Digest, "")
	if err != nil {
		return err
	}
	defer in.Body.Close()
	if in.ContentLength >= 0 && in.ContentLength != b.Size {
		return fmt.Errorf("%w: %s's blob %s is served as %d bytes, its manifest says %d", ErrDigestMismatch, src, b.Digest, in.ContentLength, b.Size)
	}
	m.logf("sending %s (%d bytes)", b.Digest, b.Size)

	start, err := m.dstDo(ctx, dst, http.MethodPost, "/blobs/uploads/", nil, nil, 0)
	if err != nil {
		return err
	}
	drain(start)
	switch start.StatusCode {
	case http.StatusAccepted:
	case http.StatusUnauthorized, http.StatusForbidden:
		return authRefusal(dst, start.StatusCode)
	default:
		return fmt.Errorf("starting an upload to %s: HTTP %d", dst, start.StatusCode)
	}
	loc, err := m.location(dst, start, m.base(dst.Host)+"/v2/"+dst.Repo+"/blobs/uploads/")
	if err != nil {
		return err
	}

	h := sha256.New()
	var n int64
	body := io.TeeReader(io.LimitReader(in.Body, b.Size), countingWriter{h, &n})
	patch, err := m.dstDoURL(ctx, dst, http.MethodPatch, loc, map[string]string{"Content-Type": "application/octet-stream"}, body, b.Size)
	if err != nil {
		return err
	}
	drain(patch)
	if n != b.Size {
		return fmt.Errorf("%w: %s's blob %s ended after %d of %d bytes", ErrDigestMismatch, src, b.Digest, n, b.Size)
	}
	if extra, _ := io.CopyN(io.Discard, in.Body, 1); extra > 0 {
		return fmt.Errorf("%w: %s's blob %s is longer than the %d bytes its manifest says", ErrDigestMismatch, src, b.Digest, b.Size)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != b.Digest {
		// Not finalized: the upload session is abandoned and nothing names it.
		return fmt.Errorf("%w: %s's blob %s hashes to %s; it was not committed", ErrDigestMismatch, src, b.Digest, got)
	}
	if patch.StatusCode == http.StatusUnauthorized || patch.StatusCode == http.StatusForbidden {
		return authRefusal(dst, patch.StatusCode)
	}
	if patch.StatusCode != http.StatusAccepted && patch.StatusCode != http.StatusNoContent && patch.StatusCode != http.StatusCreated {
		return fmt.Errorf("uploading %s to %s: HTTP %d", b.Digest, dst, patch.StatusCode)
	}
	next, err := m.location(dst, patch, loc)
	if err != nil {
		return err
	}
	pu, err := url.Parse(next)
	if err != nil {
		return err
	}
	q := pu.Query()
	q.Set("digest", b.Digest)
	pu.RawQuery = q.Encode()
	put, err := m.dstDoURL(ctx, dst, http.MethodPut, pu.String(), nil, nil, 0)
	if err != nil {
		return err
	}
	defer drain(put)
	if put.StatusCode != http.StatusCreated {
		return fmt.Errorf("finalizing %s in %s: HTTP %d (the destination did not accept the blob)", b.Digest, dst, put.StatusCode)
	}
	if d := put.Header.Get("Docker-Content-Digest"); d != "" && d != b.Digest {
		return fmt.Errorf("%w: the destination stored %s as %s", ErrDigestMismatch, b.Digest, d)
	}
	return nil
}

type countingWriter struct {
	h io.Writer
	n *int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	*c.n += int64(len(p))
	return c.h.Write(p)
}

// location resolves a response's Location against the request's URL and
// refuses one on another host: the credential only ever goes to the
// destination.
func (m *Mirror) location(dst Ref, resp *http.Response, rel string) (string, error) {
	l := resp.Header.Get("Location")
	if l == "" {
		return "", fmt.Errorf("%s gave no upload location", dst)
	}
	base, err := url.Parse(rel)
	if err != nil {
		return "", err
	}
	u, err := base.Parse(l)
	if err != nil {
		return "", fmt.Errorf("%s gave an unusable upload location", dst)
	}
	want, _ := url.Parse(m.base(dst.Host))
	if u.Host != want.Host || u.Scheme != want.Scheme {
		return "", fmt.Errorf("%s sent the upload to %s: refusing to send the credential to another host", dst, u.Host)
	}
	return u.String(), nil
}

// putManifest tags the image: the manifest bytes as the source served them,
// then a read-back of the tag, hashed.
func (m *Mirror) putManifest(ctx context.Context, p *Plan) error {
	resp, err := m.dstDo(ctx, p.Dest, http.MethodPut, "/manifests/"+p.Dest.Tag, map[string]string{"Content-Type": p.MediaType}, bytes.NewReader(p.Manifest), int64(len(p.Manifest)))
	if err != nil {
		return err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return authRefusal(p.Dest, resp.StatusCode)
	default:
		return fmt.Errorf("tagging %s: HTTP %d", p.Dest, resp.StatusCode)
	}
	rb, err := m.dstDo(ctx, p.Dest, http.MethodGet, "/manifests/"+p.Dest.Tag, map[string]string{"Accept": acceptManifest}, nil, 0)
	if err != nil {
		return err
	}
	defer rb.Body.Close()
	if rb.StatusCode != http.StatusOK {
		return fmt.Errorf("reading back %s: HTTP %d", p.Dest, rb.StatusCode)
	}
	raw, err := readCapped(rb.Body, m.limits().Manifest)
	if err != nil {
		return fmt.Errorf("reading back %s: %w", p.Dest, err)
	}
	if got := sum(raw); got != p.Digest {
		return fmt.Errorf("%w: %s reads back as %s, not %s (it was tagged: check it before use)", ErrDigestMismatch, p.Dest, got, p.Digest)
	}
	return nil
}
