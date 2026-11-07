package gateway

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
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

// TestGitForwardServesTheRouteOnly: a path that is not the proxy route's is
// refused here and never reaches the service. The forwarder is the one client
// that presents a run's identity to the service, and presenting it to any path
// a caller names would be a second way into the service's other routes — a way
// this listener exists to not be.
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
func TestGitForwardDropsCallerAuthorization(t *testing.T) {
	svc := newForwardService(t, nil)
	f := startForward(t, svc.url, nil)
	caller := forwardIdentity(t, "caller-credential")
	idToken := forwardIdentity(t, "job-id-token")
	runIdentity := forwardIdentity(t, "run-identity")

	resp, _ := forwardGet(t, f, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+caller)
		r.Header.Set("Proxy-Authorization", "Basic "+caller)
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
