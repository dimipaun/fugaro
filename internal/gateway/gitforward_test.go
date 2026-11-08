package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The stand-in for the installation's credentials service is an httptest
// server this file records: what the forwarder sent it, and what it answers
// with, are the whole of what a git forward is to this package. Its base URL
// is loopback, so checkBaseURL admits it exactly as it admits the fake
// Anthropic and the model path's service upstream.
type forwardService struct {
	t      *testing.T
	url    string
	answer func(w http.ResponseWriter, r *http.Request)

	mu     sync.Mutex
	seen   []*http.Request
	bodies [][]byte
}

func newForwardService(t *testing.T, answer func(http.ResponseWriter, *http.Request)) *forwardService {
	t.Helper()
	s := &forwardService{t: t, answer: answer}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *forwardService) serve(w http.ResponseWriter, r *http.Request) {
	if s.answer != nil {
		// A case that answers for itself reads the body itself: a streaming
		// case's body is still arriving when its answer is written, and a fake
		// that read the body whole first would be the buffering this package's
		// forwarder must not do.
		s.record(r, nil)
		s.answer(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.t.Errorf("forwardService: reading the request body: %v", err)
		return
	}
	s.record(r, body)
	w.WriteHeader(http.StatusOK)
}

func (s *forwardService) record(r *http.Request, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, r.Clone(r.Context()))
	s.bodies = append(s.bodies, body)
}

// call is request i as the service received it, and the body it carried (nil
// for a case that answered for itself).
func (s *forwardService) call(t *testing.T, i int) (*http.Request, []byte) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.seen) {
		t.Fatalf("the service was sent %d requests, want at least %d", len(s.seen), i+1)
	}
	return s.seen[i], s.bodies[i]
}

func (s *forwardService) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// forwardIdentity builds one of the values a case's identities carry, from the
// test's name and a suffix (Convention 2): no literal in this file is, or looks
// like, a credential, and a case that compares what it sent with what arrived
// compares two spellings of the same name.
func forwardIdentity(t *testing.T, what string) string {
	t.Helper()
	return what + "-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
}

// forwardPath is a git fetch of the shape git puts on the wire, relative to the
// forwarder's URL — which is the proxy route's own prefix, so the path the
// service reads is the route's with the repository's after it.
const forwardPath = "acme/sandbox.git/info/refs?service=git-upload-pack"

// startForward starts the forwarder this file examines, pointed at svc, with
// identities and a job name built from the test's name. edit changes the
// options before the start; the forwarder is closed when the test ends.
func startForward(t *testing.T, svc string, edit func(*GitForwardOptions)) *GitForward {
	t.Helper()
	o := GitForwardOptions{
		Service: svc,
		Identity: Service{
			IDToken:    func(context.Context) (string, error) { return forwardIdentity(t, "job-id-token"), nil },
			RunIDToken: func() string { return forwardIdentity(t, "run-identity") },
			Job:        serviceJob,
		},
		Log: slog.New(slog.DiscardHandler),
	}
	if edit != nil {
		edit(&o)
	}
	f, err := StartGitForward(context.Background(), o)
	if err != nil {
		t.Fatalf("StartGitForward: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := f.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return f
}

// forwardGet sends one fetch to the forwarder and reads the answer whole.
func forwardGet(t *testing.T, f *GitForward, edit func(*http.Request)) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.URL()+forwardPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the forwarder: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

// TestGitForwardLoopbackOnly: the forwarder listens where only this job can
// reach it, on a port of its own, and the URL a runner hands git is that
// address with the proxy route's prefix on it — which is what makes the
// url.<URL>.insteadOf rewrite keep the path it rewrote (B1.7's rewrite) and
// what the contract test holds to credsvc's own spelling of the route.
func TestGitForwardLoopbackOnly(t *testing.T) {
	svc := newForwardService(t, nil)
	f := startForward(t, svc.url, nil)

	u, err := url.Parse(f.URL())
	if err != nil {
		t.Fatalf("URL() = %q: %v", f.URL(), err)
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != GitProxyPrefix {
		t.Errorf("URL() = %q, want http://127.0.0.1:<port>%s", f.URL(), GitProxyPrefix)
	}
	if f2 := startForward(t, svc.url, nil); f2.URL() == f.URL() {
		t.Errorf("two forwarders share the address %q", f.URL())
	}
}

// rawAnswer sends one request line this file writes itself and reads the answer
// whole. A dot segment is a path no client is obliged to keep as written, and
// what this case is about is the bytes that arrive.
func rawAnswer(t *testing.T, addr, target string) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dialing the forwarder: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target, addr); err != nil {
		t.Fatalf("writing the request: %v", err)
	}
	b, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return string(b)
}

// TestGitForwardServesTheRouteOnly: a path that is not the proxy route's is
// refused here and never reaches the service. The forwarder is the one client
// that presents a run's identity to the service, and presenting it to any path
// a caller names would be a second way into the service's other routes — a way
// this listener exists to not be.
//
// The route is the prefix and then a path with no dot segment in it, which a
// prefix is not enough to say: "/v1/bitbucket/proxy/../v1/mint" is prefixed by
// the route and is not on it, and a hop that normalized the path before routing
// it would carry this job's identity to the service's mint route. The service's
// own classifier refuses the spelling (that is why it refuses dot segments at
// all), but the check that the identity never leaves the route is this
// listener's, so it is made where the identity is written.
func TestGitForwardServesTheRouteOnly(t *testing.T) {
	svc := newForwardService(t, nil)
	f := startForward(t, svc.url, nil)

	base := strings.TrimSuffix(f.URL(), GitProxyPrefix)
	resp, err := http.Get(base + "/v1/messages")
	if err != nil {
		t.Fatalf("the forwarder: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a path outside %s answered %d, want 404", GitProxyPrefix, resp.StatusCode)
	}
	if n := svc.count(); n != 0 {
		t.Errorf("the service was sent %d requests for a path outside the route, want none", n)
	}

	// A path that walks out of the route, in the spellings it has: the dot
	// segments a client may write and the same ones escaped as %2e, and then a
	// step hidden inside a segment by an escaped separator — %2f, or %5c, a
	// backslash being a separator to some servers. Each is a path to another
	// route of the service for a hop that decodes before it normalizes, and the
	// service refuses every one of them as spelling.
	u, err := url.Parse(f.URL())
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/v1/bitbucket/proxy/../../v1/mint",
		"/v1/bitbucket/proxy/%2e%2e/%2e%2e/v1/mint",
		"/v1/bitbucket/proxy/%2E%2e/",
		"/v1/bitbucket/proxy/./info/refs",
		"/v1/bitbucket/proxy/..%2f..%2fv1%2fmint",
		"/v1/bitbucket/proxy/%2e%2e%2f%2e%2e%2fv1%2fmint",
		"/v1/bitbucket/proxy/..%5C..%5Cv1%5Cmint",
		`/v1/bitbucket/proxy/..\..\v1\mint`,
		// An escaped separator that leads nowhere, refused for the same reason
		// the service refuses it: this listener reads the path's spelling the
		// way the classification will, so the two never disagree about which
		// path was asked for.
		"/v1/bitbucket/proxy/repo.git%2finfo%2frefs",
	} {
		ans := rawAnswer(t, u.Host, target)
		if line, _, _ := strings.Cut(ans, "\r\n"); !strings.HasPrefix(line, "HTTP/1.1 404") {
			t.Errorf("%s answered %q, want 404", target, line)
		}
	}
	if n := svc.count(); n != 0 {
		t.Errorf("the service was sent %d requests for a path that walks out of the route, want none", n)
	}

	// The route itself is served, so the refusal above is the prefix and not
	// the listener being deaf.
	if resp, _ := forwardGet(t, f, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the route answered %d, want 200", resp.StatusCode)
	}
	if n := svc.count(); n != 1 {
		t.Errorf("the service was sent %d requests, want 1", n)
	}
}

// TestGitForwardAttachesFreshTokens: every request carries a job ID token
// minted for it and the run identity the run has at that moment, so no git
// command outlives the credential it was admitted with (G11, D495) and a run
// whose identity token was refreshed pushes with the new one.
func TestGitForwardAttachesFreshTokens(t *testing.T) {
	svc := newForwardService(t, nil)
	idName := forwardIdentity(t, "job-id-token")
	runName := forwardIdentity(t, "run-identity")
	var id, run int
	f := startForward(t, svc.url, func(o *GitForwardOptions) {
		o.Identity.IDToken = func(context.Context) (string, error) {
			id++
			return fmt.Sprintf("%s-%d", idName, id), nil
		}
		o.Identity.RunIDToken = func() string {
			run++
			return fmt.Sprintf("%s-%d", runName, run)
		}
	})

	for i := 0; i < 2; i++ {
		if resp, _ := forwardGet(t, f, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("fetch %d answered %d, want 200", i, resp.StatusCode)
		}
	}
	if n := svc.count(); n != 2 {
		t.Fatalf("the service was sent %d requests, want 2", n)
	}
	for i := 0; i < 2; i++ {
		req, _ := svc.call(t, i)
		wantID := fmt.Sprintf("%s-%d", idName, i+1)
		if got := req.Header.Get("Authorization"); got != "Bearer "+wantID {
			t.Errorf("fetch %d: Authorization = %q, want the ID token minted for it (Bearer %s)", i, got, wantID)
		}
		wantRun := fmt.Sprintf("%s-%d", runName, i+1)
		if got := req.Header.Get(HeaderRunIdentity); got != wantRun {
			t.Errorf("fetch %d: %s = %q, want the run's identity as it stands now (%q)", i, HeaderRunIdentity, got, wantRun)
		}
		if got := req.Header.Get(HeaderRunJob); got != serviceJob {
			t.Errorf("fetch %d: %s = %q, want %q", i, HeaderRunJob, got, serviceJob)
		}
	}
}

// TestGitForwardDropsCallerAuthorization: the credential the caller presented
// is dropped and the forwarder's own identity is written in its place (G5). The
// caller here is the run's own agent, which holds the run identity and can mint
// the job's ID token (the plan's attacker's view): everything it hand-writes
// into the contract's own headers is dropped, by name and by the X-Fugaro-
// prefix, and none of it reaches the service.
//
// The credential headers are both of the ones Cloud Run IAM reads an identity
// from — Authorization, and X-Serverless-Authorization, which IAM takes ahead of
// it — because a caller that put its own token in the second one would be the
// one deciding which identity the service admits.
func TestGitForwardDropsCallerAuthorization(t *testing.T) {
	svc := newForwardService(t, nil)
	f := startForward(t, svc.url, nil)
	caller := forwardIdentity(t, "caller-credential")
	idToken := forwardIdentity(t, "job-id-token")
	runIdentity := forwardIdentity(t, "run-identity")

	resp, _ := forwardGet(t, f, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+caller)
		r.Header.Set("Proxy-Authorization", "Basic "+caller)
		// Cloud Run IAM reads a Google ID token from this header ahead of
		// Authorization, so a caller that put one here would be the one
		// deciding which identity the service admits (reconciliation.md U1,
		// and check190's callerCredential reads it the same way). It is a
		// credential header and goes the way of the other two.
		r.Header.Set("X-Serverless-Authorization", "Bearer "+caller)
		r.Header.Set(HeaderRunIdentity, caller)
		r.Header.Set(HeaderRunJob, caller)
		r.Header.Set(HeaderTier, TierA)
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch answered %d, want 200", resp.StatusCode)
	}

	req, _ := svc.call(t, 0)
	if got := req.Header.Get("Authorization"); got != "Bearer "+idToken {
		t.Errorf("Authorization = %q, want the job's own ID token (Bearer %s)", got, idToken)
	}
	if got := req.Header.Get(HeaderRunIdentity); got != runIdentity {
		t.Errorf("%s = %q, want %q", HeaderRunIdentity, got, runIdentity)
	}
	if got := req.Header.Get(HeaderRunJob); got != serviceJob {
		t.Errorf("%s = %q, want %q", HeaderRunJob, got, serviceJob)
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			if strings.Contains(v, caller) {
				t.Errorf("the service was sent %s: %q, carrying the caller's own credential", k, v)
			}
		}
	}
	if got := req.Header.Get(HeaderTier); got != "" {
		t.Errorf("the caller's %s = %q reached the service", HeaderTier, got)
	}
}

// TestGitForwardRefusesToStart: every check validate makes is a forwarder that
// could not work, refused at the start rather than once per git command. What
// each one would cost is the reason it is a check and not a comment: without a
// run-identity func the first request panics reading it (upstream), and that
// panic lands in a handler whose ErrorLog is discarded; without a job name the
// header the service reads this job's claim from is written empty; a service
// URL with a path on it sends every fetch somewhere no route of the service is.
// The last case pins the other half — the options the rest of this file starts
// a forwarder with are admitted, so the guard is these four and not everything.
func TestGitForwardRefusesToStart(t *testing.T) {
	valid := func() GitForwardOptions {
		return GitForwardOptions{
			Service: "http://127.0.0.1:1",
			Identity: Service{
				IDToken:    func(context.Context) (string, error) { return forwardIdentity(t, "job-id-token"), nil },
				RunIDToken: func() string { return forwardIdentity(t, "run-identity") },
				Job:        serviceJob,
			},
			Log: slog.New(slog.DiscardHandler),
		}
	}
	cases := []struct {
		name  string
		edit  func(*GitForwardOptions)
		start bool
		want  string
	}{
		{
			name: "no service URL",
			edit: func(o *GitForwardOptions) { o.Service = "" },
			want: "no service URL",
		},
		{
			name: "a service URL with a path on it",
			edit: func(o *GitForwardOptions) { o.Service = "https://creds.example/fugaro/v1" },
			want: "scheme and a host only",
		},
		{
			name: "a service URL that is http on a routable host",
			edit: func(o *GitForwardOptions) { o.Service = "http://creds.example" },
			want: "http on a loopback address",
		},
		{
			name: "no job identity",
			edit: func(o *GitForwardOptions) { o.Identity.IDToken = nil },
			want: "no job identity",
		},
		{
			name: "no run identity",
			edit: func(o *GitForwardOptions) { o.Identity.RunIDToken = nil },
			want: "no run identity",
		},
		{
			name: "no job name",
			edit: func(o *GitForwardOptions) { o.Identity.Job = "" },
			want: "no Cloud Run job name",
		},
		{
			name: "a job name that is not one",
			edit: func(o *GitForwardOptions) { o.Identity.Job = "Not A Job Name" },
			want: "no Cloud Run job name",
		},
		{
			name:  "the options this file's other cases use",
			start: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := valid()
			if c.edit != nil {
				c.edit(&o)
			}
			f, err := StartGitForward(context.Background(), o)
			if c.start {
				if err != nil {
					t.Fatalf("StartGitForward refused the options this file's other cases start with: %v", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := f.Close(ctx); err != nil {
					t.Errorf("Close: %v", err)
				}
				return
			}
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = f.Close(ctx)
				t.Fatalf("StartGitForward started a forwarder for %s", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("StartGitForward = %q, want it to name %q", err, c.want)
			}
		})
	}
}

// TestGitForwardRefusesWithoutIdentity: a forward that cannot present the run
// is refused here, before anything is sent — an unauthenticated call to the
// service under this job's identity is not a call this forwarder makes, and
// there is nothing else it could fall back to (G5, the model path's D143).
// Whatever went wrong is the run's own log line, and the caller is answered a
// status and a reason class.
func TestGitForwardRefusesWithoutIdentity(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*GitForwardOptions)
		class string
	}{
		{
			name:  "the run has no identity token",
			edit:  func(o *GitForwardOptions) { o.Identity.RunIDToken = func() string { return "" } },
			class: "no_run_identity",
		},
		{
			name: "the job's ID token cannot be minted",
			edit: func(o *GitForwardOptions) {
				o.Identity.IDToken = func(context.Context) (string, error) {
					return "", fmt.Errorf("metadata: the job's identity is not available")
				}
			},
			class: "no_job_identity",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := newForwardService(t, nil)
			logs := &logBuf{}
			f := startForward(t, svc.url, func(o *GitForwardOptions) {
				c.edit(o)
				o.Log = slog.New(slog.NewJSONHandler(logs, nil))
			})

			resp, body := forwardGet(t, f, nil)
			if resp.StatusCode != http.StatusBadGateway {
				t.Errorf("the fetch answered %d, want 502", resp.StatusCode)
			}
			if n := svc.count(); n != 0 {
				t.Errorf("the service was sent %d requests for a forward with no identity, want none", n)
			}
			// The class and nothing else: the cause is the run's own log line
			// and never the caller's answer, which is text git shows whoever
			// ran it.
			if !strings.Contains(body, c.class) {
				t.Errorf("the answer = %q, want the class %q", body, c.class)
			}
			if strings.Contains(body, "metadata") {
				t.Errorf("the answer carried the cause: %q", body)
			}
			lines := logs.lines(t)
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d log lines, want 1", len(lines))
			}
			if got, _ := lines[0]["reason"].(string); got != c.class {
				t.Errorf("the log line's reason = %q, want %q", got, c.class)
			}
			if s, _ := lines[0]["msg"].(string); strings.Contains(s, "metadata") {
				t.Errorf("the log line's message carries the mint's own text: %q", s)
			}
		})
	}
}

// TestGitForwardFiltersTheAnswerHeaders: the service's answer reaches the
// caller as it was answered, with the headers that are not this listener's to
// hand back left out. The redirect destination is the one that matters: this
// listener follows no redirect (upstreamClient), so a Location handed to the
// caller is a hop the CALLER would make, to a host no rule named — the service
// drops bitbucket.org's own redirects for the same reason (B1.5's
// stripRedirect) and this is the outside hop of that chain. A listener that
// writes no per-request line and discards its ErrorLog would lose all of this
// silently, which is what this case exists to not happen.
func TestGitForwardFiltersTheAnswerHeaders(t *testing.T) {
	// A hop's own headers, the seven of RFC 9110 a service can put on an answer
	// and net/http will carry: Transfer-Encoding is the eighth and is not here,
	// because a transport reads it off the answer itself and refuses one it did
	// not write — there is no answer this listener could be sent that has it.
	hopHeaders := []string{"Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Te", "Trailer", "Upgrade"}

	t.Run("a redirect and the hop's own headers are not handed back", func(t *testing.T) {
		// Anything that followed the redirect would arrive here, and this
		// counts what did.
		elsewhere := newForwardService(t, nil)
		body := "moved"
		svc := newForwardService(t, func(w http.ResponseWriter, r *http.Request) {
			for _, k := range hopHeaders {
				w.Header().Set(k, "probe")
			}
			w.Header().Set("Location", elsewhere.url+"/"+forwardPath)
			w.Header().Set("Content-Location", elsewhere.url+"/"+forwardPath)
			// The service's claim about a body, for a body it does not have:
			// relaying it would leave the caller waiting for bytes that never
			// come, so the length the caller reads is this listener's count.
			w.Header().Set("Content-Length", "999999")
			w.WriteHeader(http.StatusFound)
			_, _ = io.WriteString(w, body)
		})
		f := startForward(t, svc.url, nil)

		resp, got := forwardGet(t, f, nil)
		if resp.StatusCode != http.StatusFound {
			t.Errorf("the answer's status = %d, want the service's 302", resp.StatusCode)
		}
		if got != body {
			t.Errorf("the answer's body = %q, want %q", got, body)
		}
		if v := resp.Header.Get("Location"); v != "" {
			t.Errorf("the caller was handed Location: %q", v)
		}
		if v := resp.Header.Get("Content-Location"); v != "" {
			t.Errorf("the caller was handed Content-Location: %q", v)
		}
		if n := elsewhere.count(); n != 0 {
			t.Errorf("the redirect was followed: the caller reached the destination %d times", n)
		}
		for _, k := range hopHeaders {
			if v := resp.Header.Get(k); v != "" {
				t.Errorf("the caller was handed %s: %q, a header of this hop", k, v)
			}
		}
		if resp.ContentLength != int64(len(body)) {
			t.Errorf("the answer's length = %d, want the %d bytes this listener copied", resp.ContentLength, len(body))
		}
	})

	t.Run("everything else is relayed", func(t *testing.T) {
		// A pack, gzipped by whoever answered and unpacked by nobody: the
		// caller here asks for nothing and unpacks nothing, as the forwarder's
		// own client does (upstreamClient). A client that sent Accept-Encoding
		// would have the header taken off its answer and the body opened, and
		// both are what this case reads.
		var pack bytes.Buffer
		zw := gzip.NewWriter(&pack)
		_, _ = zw.Write([]byte("a pack"))
		_ = zw.Close()
		const contentType = "application/x-git-upload-pack-advertisement"
		svc := newForwardService(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("X-Request-Id", forwardIdentity(t, "request-id"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pack.Bytes())
		})
		f := startForward(t, svc.url, nil)

		req, err := http.NewRequest(http.MethodGet, f.URL()+forwardPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("the forwarder: %v", err)
		}
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pack.Bytes()) {
			t.Errorf("the answer carried %d bytes, want the %d the service answered", len(got), pack.Len())
		}
		for k, want := range map[string]string{
			"Content-Type":     contentType,
			"Content-Encoding": "gzip",
			"X-Request-Id":     forwardIdentity(t, "request-id"),
		} {
			if got := resp.Header.Get(k); got != want {
				t.Errorf("the answer's %s = %q, want %q", k, got, want)
			}
		}
	})
}

// TestGitForwardUnreachableCarriesNoPath: a forward that could not reach the
// service is answered a class and logged the transport's own cause — never the
// *url.Error net/http wraps that cause in, whose text is the URL this listener
// went to with the caller's escaped path and query on it. That URL is the one
// way a path could reach a line this listener writes, and the file's own
// contract, which later tasks read as theirs, is that none does.
func TestGitForwardUnreachableCarriesNoPath(t *testing.T) {
	logs := &logBuf{}
	f := startForward(t, closedPort(t), func(o *GitForwardOptions) {
		o.Log = slog.New(slog.NewJSONHandler(logs, nil))
	})

	resp, body := forwardGet(t, f, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("the fetch answered %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(body, "unreachable") {
		t.Errorf("the answer = %q, want the class %q", body, "unreachable")
	}
	lines := logs.lines(t)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d log lines, want 1", len(lines))
	}
	if got, _ := lines[0]["reason"].(string); got != "unreachable" {
		t.Errorf("the log line's reason = %q, want %q", got, "unreachable")
	}
	// The cause is kept: a line that named nothing would satisfy the check
	// below and leave the run's operator with a class and no dial.
	if e, _ := lines[0]["error"].(string); e == "" {
		t.Errorf("the log line carries no cause: %v", lines[0])
	}
	// The whole line, its cause included: what the caller asked for is what a
	// *url.Error would have said, and it is not here.
	for _, s := range []string{forwardPath, "/v1/bitbucket/proxy/"} {
		if text := logs.String(); strings.Contains(text, s) {
			t.Errorf("the log line carries %q: %s", s, text)
		}
	}
}

// TestGitForwardStreams: the request body is copied through as it arrives and
// the answer as it is answered — nothing here holds a pack (Check 190's 33 MiB
// push is what this exists for), and the service sees the first bytes of a body
// that is still being sent. A forwarder that buffered the body would deliver
// them only once the caller had finished, which is what this test's ordering
// says.
func TestGitForwardStreams(t *testing.T) {
	const head = 32 << 10
	const tail = 1 << 20

	body := make([]byte, head+tail)
	for i := range body {
		body[i] = byte(i * 7)
	}
	headRead := make(chan struct{})
	release := make(chan struct{})
	var released sync.Once
	svc := newForwardService(t, func(w http.ResponseWriter, r *http.Request) {
		got := make([]byte, head)
		if _, err := io.ReadFull(r.Body, got); err != nil {
			t.Errorf("the service could not read the body's first %d bytes: %v", head, err)
			return
		}
		close(headRead)
		rest, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("the service could not read the rest of the body: %v", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(got)
		_, _ = w.Write(rest)
	})

	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.CloseWithError(io.EOF) })
	go func() {
		_, _ = pw.Write(body[:head])
		<-release
		_, _ = pw.Write(body[head:])
		_ = pw.Close()
	}()

	f := startForward(t, svc.url, nil)
	req, err := http.NewRequest(http.MethodPost, f.URL()+forwardPath, pr)
	if err != nil {
		t.Fatal(err)
	}
	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	select {
	case <-headRead:
		// The body is still being sent: the service read its first bytes
		// before the caller finished writing them, which is what streaming
		// means here.
	case err := <-errCh:
		released.Do(func() { close(release) })
		t.Fatalf("the forwarder: %v", err)
	case <-time.After(5 * time.Second):
		released.Do(func() { close(release) })
		t.Fatal("the service never saw the body's first bytes: the forwarder held the request")
	}
	released.Do(func() { close(release) })

	var resp *http.Response
	select {
	case resp = <-respCh:
	case err := <-errCh:
		t.Fatalf("the forwarder: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the answer never arrived")
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the push answered %d, want 200", resp.StatusCode)
	}
	if len(got) != len(body) {
		t.Fatalf("the answer carried %d bytes, want %d", len(got), len(body))
	}
	if sha256.Sum256(got) != sha256.Sum256(body) {
		t.Error("the bytes the forwarder was answered with are not the bytes it sent")
	}
	// The whole body travelled as the caller spelled it, path and query
	// included: the forwarder adds its route and copies the rest (G13 is the
	// service's to decide on, and it can only decide on what it was sent).
	sent, _ := svc.call(t, 0)
	wantPath, wantQuery, _ := strings.Cut(forwardPath, "?")
	if sent.URL.Path != GitProxyPrefix+wantPath || sent.URL.RawQuery != wantQuery {
		t.Errorf("the service was sent %q, want %q", sent.URL.RequestURI(), GitProxyPrefix+forwardPath)
	}
}
