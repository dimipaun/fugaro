// Command chk190 is Check 190's throwaway reverse proxy: the service the check
// deploys to bitbucket.org for the length of one run, and nothing else.
//
// It exists to answer one question the design is built on and cannot answer
// offline (secrets-brokering/reconciliation.md F13, F14, U2, U7, U8): what
// Cloud Run's front end does with a git fetch and, above all, with a chunked
// push above 32 MiB on HTTP/1.1. It is deployed by scripts/live-checks/check190.sh
// with IAM required and no public invoker, and it is deleted by that check's
// Restore.
//
// The sandbox repository's token comes from the file the service mounts out of
// its own Secret Manager container (CHK190_TOKEN_FILE), never from an
// environment variable — an environment variable is printed back by
// `gcloud run services describe`, and this is a real token. It is attached to
// every forwarded request as Bitbucket's git credential (`x-token-auth` Basic)
// and appears in no log line, no error and no response: this program logs
// counts and sizes, and for a refusal by Bitbucket its path and challenge
// scheme, and nothing else.
//
// The caller's own Authorization header is dropped rather than forwarded: the
// only credential on the wire to Bitbucket is this service's own.
//
// Every answer it forwards carries X-Chk190-Upstream with Bitbucket's status,
// and a 401 or 403 from Bitbucket is logged with its path and challenge
// scheme: git reports every 401 as "could not read Username", whoever sent it,
// and the mark is what tells the check's row a refusal by Bitbucket from one
// by Cloud Run's front end, which never reaches this program.
//
// It is standard library only, and it is a throwaway: the production proxy is
// internal/credsvc's Bitbucket route, with the repository, the run branch and
// the audit lines this one deliberately does not have.
package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	healthPath   = "/chk190/health"
	statsPath    = "/chk190/stats"
	upstreamMark = "X-Chk190-Upstream"
)

var (
	started = time.Now()

	// The counters the check reads back through statsPath: requests forwarded
	// upstream, the bytes the container received and the bytes it sent. The
	// check's own two routes are not counted — they are this program talking
	// about itself, and counting them would add one request to every row the
	// check measures.
	requests atomic.Int64
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
)

// countingBody counts what the upstream actually reads out of the caller's
// body: a request the front end refuses never arrives, and a request that is
// cut half way counts what arrived before the cut, which is exactly the figure
// the large-push row is read with.
type countingBody struct {
	rc io.ReadCloser
	n  *atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.n.Add(int64(n))
	return n, err
}

func (b *countingBody) Close() error { return b.rc.Close() }

// countingWriter counts what was written back to the caller.
type countingWriter struct {
	http.ResponseWriter
	n *atomic.Int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.n.Add(int64(n))
	return n, err
}

func main() {
	tokenPath := os.Getenv("CHK190_TOKEN_FILE")
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		log.Fatalf("chk190: the mounted token cannot be read from CHK190_TOKEN_FILE: %v", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		log.Fatal("chk190: the mounted token file is empty")
	}
	upstream := envOr("CHK190_UPSTREAM", "https://bitbucket.org")
	stallPrefix := os.Getenv("CHK190_STALL_PREFIX")
	if !validStallPrefix(stallPrefix) {
		log.Fatalf("chk190: CHK190_STALL_PREFIX is %q and it must be empty or start and end with '/': the slow route's prefix is stripped off the request path before the request is forwarded, so one without both separators takes one of them with it and the upstream is asked for a path that is not there", stallPrefix)
	}
	stallSeconds, err := strconv.Atoi(os.Getenv("CHK190_STALL_SECONDS"))
	if err != nil || stallSeconds < 0 {
		stallSeconds = 0
	}
	port := envOr("PORT", "8080")

	// Redirects are not followed: Bitbucket answers a git request itself, and a
	// followed redirect would be a second request the check did not measure.
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			ForceAttemptHTTP2:   false,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// The two routes the check itself calls. They are answered here, in front of
	// the proxy, and are not counted.
	mux := http.NewServeMux()
	mux.HandleFunc(healthPath, func(w http.ResponseWriter, r *http.Request) {
		host, _ := os.Hostname()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "start=%d instance=%s\n", started.UnixNano(), host)
	})
	mux.HandleFunc(statsPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "requests=%d in=%d out=%d\n", requests.Load(), bytesIn.Load(), bytesOut.Load())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveProxy(w, r, client, upstream, token, stallPrefix, stallSeconds)
	})

	server := &http.Server{
		Addr: ":" + port,
		// No read, write or idle deadline: the slow push this check measures is
		// a request Cloud Run's front end holds open for an hour, and a deadline
		// here would be this container cutting it first — the one thing that
		// would make the measurement meaningless.
		Handler: mux,
	}
	log.Printf("chk190: listening on %s, upstream %s, token from %s", server.Addr, upstream, tokenPath)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("chk190: %v", err)
	}
}

func serveProxy(w http.ResponseWriter, r *http.Request, client *http.Client, upstream, token, stallPrefix string, stallSeconds int) {
	path := r.URL.Path
	// The check's slow push goes through a prefix of its own: the proxy holds
	// that receive-pack POST open for the configured time before it reads it,
	// which is what makes a push slow rather than large. The prefix is stripped
	// for every method, so the same path reaches Bitbucket either way, and only
	// the POST is held. upstreamPath keeps the separator the prefix ends with:
	// without it the request is forwarded to the upstream plus a path with no
	// leading "/", which is not a URL and never reaches Bitbucket.
	stalled := 0
	if stallPrefix != "" && strings.HasPrefix(path, stallPrefix) {
		path = upstreamPath(path, stallPrefix)
		if r.Method == http.MethodPost && stallSeconds > 0 {
			stalled = stallSeconds
			time.Sleep(time.Duration(stallSeconds) * time.Second)
		}
	}
	target := upstream + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	// The per-request counts, added to the totals when the request is over. They
	// are the two figures every row of the check's block is read with.
	var in, out atomic.Int64
	body := &countingBody{rc: r.Body, n: &in}
	wout := &countingWriter{ResponseWriter: w, n: &out}
	begin := time.Now()
	code := 0
	defer func() {
		requests.Add(1)
		bytesIn.Add(in.Load())
		bytesOut.Add(out.Load())
		if stalled > 0 {
			log.Printf("chk190 method=%s status=%d in=%d out=%d ms=%d stall=%d",
				r.Method, code, in.Load(), out.Load(), time.Since(begin).Milliseconds(), stalled)
			return
		}
		log.Printf("chk190 method=%s status=%d in=%d out=%d ms=%d",
			r.Method, code, in.Load(), out.Load(), time.Since(begin).Milliseconds())
	}()

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		code = http.StatusBadGateway
		http.Error(w, "chk190: bad upstream request", code)
		return
	}
	// The wrapper hides the body's length from http.NewRequest, which would
	// send every body chunked; a request git sent with a Content-Length goes on
	// with it, and only git's own chunked push stays chunked.
	req.ContentLength = r.ContentLength
	if r.ContentLength == 0 {
		req.Body = http.NoBody
	}
	for name, values := range r.Header {
		if hopByHop(name) || callerCredential(name) {
			continue
		}
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	// Bitbucket's git credential: the token is this service's own, out of its
	// own secret, and the caller's Authorization is gone above.
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("x-token-auth:"+token)))

	resp, err := client.Do(req)
	if err != nil {
		code = http.StatusBadGateway
		// The error names the target, never a header: http.Client's own error
		// for a URL carrying no user info and no query is safe to log.
		log.Printf("chk190: upstream request failed: %v", err)
		http.Error(w, "chk190: upstream request failed", code)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		log.Printf("chk190 upstream refused method=%s status=%d path=%s challenge=%s",
			r.Method, resp.StatusCode, path, challengeScheme(resp.Header.Get("Www-Authenticate")))
	}
	for name, values := range resp.Header {
		if hopByHop(name) {
			continue
		}
		for _, v := range values {
			wout.Header().Add(name, v)
		}
	}
	code = resp.StatusCode
	wout.Header().Set(upstreamMark, strconv.Itoa(code))
	wout.WriteHeader(code)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	_, _ = io.Copy(wout, resp.Body)
}

// upstreamPath is the path the request is forwarded with: the slow route's
// prefix off, and the separator that follows it — the one the prefix ends with —
// left in place. Without it the upstream URL is the upstream plus a path with no
// leading "/" ("https://bitbucket.org" + "workspace-a/repo-a.git"), which is not
// a URL: the request never reaches Bitbucket and the proxy answers its own 502
// instead, which is what the check's live run of 2026-10-30 saw on its slow
// endpoint after 0 s. A prefix the check would never deploy — one without both
// separators — is not a prefix this proxy strips at all, so a misconfigured one
// cannot eat a separator either.
func upstreamPath(path, stallPrefix string) string {
	if !validStallPrefix(stallPrefix) || !strings.HasPrefix(path, stallPrefix) {
		return path
	}
	return "/" + strings.TrimPrefix(path, stallPrefix)
}

// validStallPrefix: no slow route at all (empty), or a prefix with both
// separators. Anything else is refused at startup rather than silently mangling
// the path of every request that carries it.
func validStallPrefix(prefix string) bool {
	return prefix == "" || (strings.HasPrefix(prefix, "/") && strings.HasSuffix(prefix, "/"))
}

// callerCredential is a header that carries the caller's own identity token:
// Authorization, and the header Cloud Run reads one from when Authorization is
// the application's. Neither is Bitbucket's business.
func callerCredential(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Authorization", "X-Serverless-Authorization":
		return true
	}
	return false
}

// challengeScheme is the scheme of a WWW-Authenticate challenge and nothing
// after it, so the log line says which kind of credential Bitbucket wanted
// without copying anything else it said.
func challengeScheme(v string) string {
	scheme, _, _ := strings.Cut(strings.TrimSpace(v), " ")
	if scheme == "" {
		return "none"
	}
	return scheme
}

// hopByHop is the set a proxy must not copy in either direction.
func hopByHop(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
