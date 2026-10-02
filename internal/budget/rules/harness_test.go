//go:build firebase

package rules

// The emulator harness: starts the Realtime Database emulator of the pinned
// firebase-tools (emulator/package-lock.json), loads the generated rules over
// the same REST call `fugaro init` uses, and drives them with internal/rtdb as
// a run would. Nothing here contacts Firebase: the project id is a demo- id,
// which the emulator treats as fully local, and tokens are unsigned.
//
// Without a JRE or firebase-tools the tests skip with a message saying what is
// missing; FUGARO_REQUIRE_EMULATOR=1 (set by the CI rules job) turns a skip
// into a failure, so the required check can never pass vacuously.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
	"golang.org/x/oauth2"
)

const project = "demo-fugaro"

var (
	emuURL    string // http://127.0.0.1:<port>
	emuSkip   string // why the emulator is not available ("" when it is)
	emuLog    string
	nsCounter atomic.Int64
)

func TestMain(m *testing.M) {
	stop, err := startEmulator()
	if err != nil {
		emuSkip = err.Error()
		if os.Getenv("FUGARO_REQUIRE_EMULATOR") == "1" {
			fmt.Fprintln(os.Stderr, "FUGARO_REQUIRE_EMULATOR=1 and the emulator is unavailable:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "SKIP emulator tests:", err, "(install a JRE and run npm ci in internal/budget/rules/emulator, or see the CI rules job)")
	}
	code := m.Run()
	if stop != nil {
		stop()
	}
	os.Exit(code)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// firebaseBin finds the pinned firebase-tools, then any on PATH.
func firebaseBin() (string, error) {
	if p := os.Getenv("FUGARO_FIREBASE_BIN"); p != "" {
		return p, nil
	}
	local, _ := filepath.Abs("emulator/node_modules/.bin/firebase")
	if _, err := os.Stat(local); err == nil {
		return local, nil
	}
	if p, err := exec.LookPath("firebase"); err == nil {
		return p, nil
	}
	return "", errors.New("no firebase-tools (run npm ci in internal/budget/rules/emulator)")
}

func startEmulator() (stop func(), err error) {
	if _, err := exec.LookPath("java"); err != nil {
		return nil, errors.New("no java on PATH (the emulator needs a JRE 21 or newer)")
	}
	bin, err := firebaseBin()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "fugaro-rules-emulator-")
	if err != nil {
		return nil, err
	}
	var ports [4]int
	for i := range ports {
		if ports[i], err = freePort(); err != nil {
			return nil, err
		}
	}
	cfg := fmt.Sprintf(`{"database":{"rules":"rules.json"},"emulators":{"singleProjectMode":true,"ui":{"enabled":false},`+
		`"hub":{"host":"127.0.0.1","port":%d},"logging":{"host":"127.0.0.1","port":%d},"database":{"host":"127.0.0.1","port":%d}}}`, ports[0], ports[1], ports[2])
	if err := os.WriteFile(filepath.Join(dir, "firebase.json"), []byte(cfg), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.json"), []byte(`{"rules":{".read":false,".write":false}}`), 0o644); err != nil {
		return nil, err
	}
	logf, err := os.Create(filepath.Join(dir, "emulator.log"))
	if err != nil {
		return nil, err
	}
	emuLog = logf.Name()
	cmd := exec.Command(bin, "emulators:start", "--only", "database", "--project", project, "--non-interactive")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting firebase-tools: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	stop = func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		logf.Close()
		os.RemoveAll(dir)
	}
	emuURL = "http://127.0.0.1:" + strconv.Itoa(ports[2])
	// The first start downloads the emulator jar (CI caches it).
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			b, _ := os.ReadFile(emuLog)
			stop()
			return nil, fmt.Errorf("the emulator exited early: %s", tail(b))
		default:
		}
		resp, err := http.Get(emuURL + "/?ns=" + project + "-ready")
		if err == nil {
			resp.Body.Close()
			return stop, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	b, _ := os.ReadFile(emuLog)
	stop()
	return nil, fmt.Errorf("the emulator did not come up in 3 minutes: %s", tail(b))
}

func tail(b []byte) string {
	if len(b) > 600 {
		b = b[len(b)-600:]
	}
	return string(b)
}

// nsTransport adds the database namespace the emulator routes on.
type nsTransport struct{ ns string }

func (n nsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	q := r.URL.Query()
	q.Set("ns", n.ns)
	r.URL.RawQuery = q.Encode()
	return http.DefaultTransport.RoundTrip(r)
}

// unsignedToken is what the emulator accepts: a JWT with alg none whose
// payload carries the uid and the custom claims at the top level.
func unsignedToken(claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now().Unix()
	p := map[string]any{"iat": now, "exp": now + 3600, "aud": project, "iss": "https://securetoken.google.com/" + project,
		"firebase": map[string]any{"sign_in_provider": "custom"}}
	for k, v := range claims {
		p[k] = v
	}
	if uid, ok := claims["uid"]; ok {
		p["sub"], p["user_id"] = uid, uid
	}
	return enc(map[string]any{"alg": "none", "typ": "JWT"}) + "." + enc(p) + "."
}

// harness is one test's own database namespace, with the generated rules
// deployed and the project marked.
type harness struct {
	t     *testing.T
	ns    string
	admin *rtdb.Client
	hc    *http.Client
}

const projectID = "aurora-fp"

// newHarness skips the test when there is no emulator. Near UTC midnight it
// waits for the new day so a test never straddles it.
func newHarness(t *testing.T) *harness {
	t.Helper()
	if emuSkip != "" {
		t.Skip("no Realtime Database emulator: " + emuSkip)
	}
	if ms := time.Now().UnixMilli() % 86_400_000; ms > 86_400_000-8_000 {
		time.Sleep(time.Duration(86_400_000-ms+200) * time.Millisecond)
	}
	rules, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ns: fmt.Sprintf("%s-%d-%d", project, time.Now().UnixNano()%1_000_000_000, nsCounter.Add(1))}
	h.hc = &http.Client{Transport: nsTransport{h.ns}, Timeout: 30 * time.Second}
	h.admin = h.client(rtdb.Auth{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "owner"})})
	// Deploy exactly as `fugaro init` does: PUT /.settings/rules.json.
	req, _ := http.NewRequest(http.MethodPut, emuURL+"/.settings/rules.json", bytes.NewReader(rules))
	req.Header.Set("Authorization", "Bearer owner")
	resp, err := h.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("deploying the generated rules: %d %s", resp.StatusCode, body)
	}
	h.set("fugaro/project", projectID)
	return h
}

func (h *harness) client(a rtdb.Auth) *rtdb.Client {
	c, err := rtdb.New(emuURL, a, rtdb.WithHTTPClient(h.hc))
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

// claims of a run's token. Zero fields take the usual values.
type claims struct {
	slug, run string
	fx        int64  // deadline in ms; 0 = an hour from now
	fp        string // "" = the project's id; "-" = no fp claim
	rb        string // "" = alice@example.invalid; "-" = no rb claim
}

func (c claims) token() string {
	m := map[string]any{"uid": "r~" + c.slug + "~" + c.run, "fs": c.slug, "fr": c.run}
	m["fx"] = c.fx
	if c.fx == 0 {
		m["fx"] = time.Now().Add(time.Hour).UnixMilli()
	}
	switch c.fp {
	case "":
		m["fp"] = projectID
	case "-":
	default:
		m["fp"] = c.fp
	}
	switch c.rb {
	case "":
		m["rb"] = "alice@example.invalid"
	case "-":
	default:
		m["rb"] = c.rb
	}
	return unsignedToken(m)
}

// as is a client holding the token of a run.
func (h *harness) as(c claims) *rtdb.Client {
	tok := c.token()
	return h.client(rtdb.Auth{IDToken: func() string { return tok }})
}

func (h *harness) run(slug, run string) *rtdb.Client { return h.as(claims{slug: slug, run: run}) }

// set writes a value with admin rights (which bypass the rules, as IAM does).
func (h *harness) set(path string, v any) {
	h.t.Helper()
	b, _ := json.Marshal(v)
	h.adminDo(http.MethodPut, path, b)
}

func (h *harness) adminDo(method, path string, body []byte) []byte {
	h.t.Helper()
	req, _ := http.NewRequest(method, emuURL+"/"+path+".json", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer owner")
	resp, err := h.hc.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		h.t.Fatalf("admin %s /%s: %d %s", method, path, resp.StatusCode, b)
	}
	return b
}

// wipe empties the database (not the rules) and marks the project again.
func (h *harness) wipe() {
	h.t.Helper()
	h.adminDo(http.MethodDelete, "", nil)
	h.set("fugaro/project", projectID)
}

// patch is an admin multi-path update.
func (h *harness) patch(updates map[string]any) {
	h.t.Helper()
	if err := h.admin.Patch(context.Background(), "", updates); err != nil {
		h.t.Fatal(err)
	}
}

// value reads a node with admin rights as JSON text ("null" when absent).
func (h *harness) value(path string) string {
	h.t.Helper()
	return string(bytes.TrimSpace(h.adminDo(http.MethodGet, path, nil)))
}

// snapshot is the whole database, for "nothing changed" assertions.
func (h *harness) snapshot() string { return h.value("") }

func (h *harness) mustAllow(c *rtdb.Client, why string, updates map[string]any) {
	h.t.Helper()
	if err := c.Patch(context.Background(), "", updates); err != nil {
		h.t.Fatalf("%s: the rules refused a write they must accept: %v\nupdates: %v", why, err, updates)
	}
}

// mustDeny asserts a permission error and that the write changed nothing.
func (h *harness) mustDeny(c *rtdb.Client, why string, updates map[string]any) {
	h.t.Helper()
	before := h.snapshot()
	err := c.Patch(context.Background(), "", updates)
	if err == nil {
		h.t.Fatalf("%s: the rules accepted a write they must deny\nupdates: %v\ndatabase now: %s", why, updates, h.snapshot())
	}
	if !errors.Is(err, rtdb.ErrPermission) {
		h.t.Fatalf("%s: want a permission error, got %v", why, err)
	}
	if after := h.snapshot(); after != before {
		h.t.Fatalf("%s: denied, but the database changed:\nbefore %s\nafter  %s", why, before, after)
	}
}

func (h *harness) mustReadDenied(c *rtdb.Client, path string) {
	h.t.Helper()
	var v json.RawMessage
	_, err := c.Get(context.Background(), path, &v)
	if !errors.Is(err, rtdb.ErrPermission) {
		h.t.Fatalf("read /%s: want permission denied, got %v (value %s)", path, err, v)
	}
}

func (h *harness) mustRead(c *rtdb.Client, path string) {
	h.t.Helper()
	var v json.RawMessage
	if _, err := c.Get(context.Background(), path, &v); err != nil {
		h.t.Fatalf("read /%s: %v", path, err)
	}
}

// rawDenied sends one non-multi-path request with the run's token (the REST
// client has no root PUT) and demands a 401.
func (h *harness) rawDenied(c claims, method, path, body string) {
	h.t.Helper()
	before := h.snapshot()
	tok := c.token()
	req, _ := http.NewRequest(method, emuURL+"/"+path+".json?auth="+tok, strings.NewReader(body))
	resp, err := h.hc.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		h.t.Fatalf("%s /%s with a run's token: status %d, want 401", method, path, resp.StatusCode)
	}
	if after := h.snapshot(); after != before {
		h.t.Fatalf("%s /%s denied but the database changed", method, path)
	}
}
