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

// claims of a token. Zero fields take the usual values.
//
// The first four are a run's; the rest are the mailbox identities (mobile plan
// D112, D113b, D165), which are what the rules *read* and never mint. A token
// normally carries none of them, and that is the point of keeping them separate
// fields rather than a mode: a phone's token is one whose mu names an account,
// a consumer's is one whose mc does, a pairing session's is one whose pu names
// the one-shot pairing uid, and a plain signed-in user's is one with none of
// them — admitted nowhere.
type claims struct {
	slug, run string
	fx        int64  // deadline in ms; 0 = an hour from now
	fp        string // "" = the project's id; "-" = no fp claim
	rb        string // "" = alice@example.invalid; "-" = no rb claim
	mu        string // "" = no mu claim; otherwise the mailbox the token owns
	mc        string // "" = no mc claim; otherwise the mailbox it serves
	pu        string // "" = no pu claim; otherwise the pairing uid it names
	uid       string // "" = derived (mu, pu, else r~slug~run); the raw uid
}

func (c claims) token() string {
	uid := c.uid
	if uid == "" {
		uid = "r~" + c.slug + "~" + c.run
		if c.mu != "" {
			uid = c.mu
		}
		if c.pu != "" {
			uid = c.pu
		}
	}
	m := map[string]any{"uid": uid, "fs": c.slug, "fr": c.run}
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
	if c.mu != "" {
		m["mu"] = c.mu
	}
	if c.mc != "" {
		m["mc"] = c.mc
	}
	if c.pu != "" {
		m["pu"] = c.pu
	}
	return unsignedToken(m)
}

// as is a client holding the token these claims describe: a run's, the
// phone's or the consumer's.
func (h *harness) as(c claims) *rtdb.Client {
	tok := c.token()
	return h.client(rtdb.Auth{IDToken: func() string { return tok }})
}

func (h *harness) run(slug, run string) *rtdb.Client { return h.as(claims{slug: slug, run: run}) }

// phone is the mobile app's client for the account mb: a user token whose mu
// claim names the mailbox it owns (D112), with auth.uid equal to it — which
// is the only thing that makes the claim usable.
//
// fp is "-" because a phone's token is a Firebase Auth user's, not a
// Fugaro-minted one: it carries the claim the owner set by hand (D114) and no
// run's fp/fs/fr. That matters to the tests, not just to the picture — a token
// that also carried fp would satisfy TOKEN and read the project's families as
// a run does, which would make a read case pass for the wrong reason.
//
// It pairs the account first (pairDevice), because D166's USER conjunct means
// an account with no device node is admitted nowhere: a phone that could not
// read its own caps would make every positive control in these tests fail for
// a reason that has nothing to do with the case it names. The unpaired case has
// its own helper, unpaired.
func (h *harness) phone(mb string) *rtdb.Client {
	h.pairDevice(mb)
	return h.as(claims{mu: mb, fp: "-"})
}

// unpaired is a phone token for mb whose device node was never written: the
// account signed in, and its device is not in the allowlist (D166). Every
// family USER reaches is refused, which is the state P6.4's verification calls
// "a session whose device node was never written denied everything".
func (h *harness) unpaired(mb string) *rtdb.Client { return h.as(claims{mu: mb, fp: "-"}) }

// pairDevice writes the device allowlist node of D166 as the operator does
// (`fugaro pair` over IAM, plan task P6.6): config/admin/devices/<uid>. The
// rules deny every client a write there, so only this admin path can create
// one — and only a delete through it revokes.
func (h *harness) pairDevice(uid string) {
	h.set(PathDevices+"/"+uid, map[string]any{"pairedAt": msgAt})
}

// unpairDevice is `fugaro pair revoke`'s write: the child is deleted, and the
// account's every grant is gone from the next request on, with no ID token
// expiring first.
func (h *harness) unpairDevice(uid string) {
	h.adminDo(http.MethodDelete, PathDevices+"/"+uid, nil)
}

// pairing is a pairing session's client (D165): a token whose pu claim names
// the one-shot pairing uid, with auth.uid equal to it. There is no device node
// for it and none is needed — the pairing nodes are what a phone reaches
// *before* it has a device, and they are the only two locations this claim
// opens.
func (h *harness) pairing(uid string) *rtdb.Client { return h.as(claims{pu: uid, fp: "-"}) }

// consumer is the mailbox consumer's client for the mailbox it serves
// (D113b): an mc claim and no mailbox of its own, and no fp for the same
// reason.
func (h *harness) consumer(mb string) *rtdb.Client { return h.as(claims{mc: mb, fp: "-"}) }

// aMac is a well-formed tag: 64 lowercase hex characters, the shape both the
// shared schema and the rules require of every message and of the heartbeat
// (D169, D171, O7). It is not a valid tag over anything — nothing here has the
// key, and neither do the rules: they check the field's shape, and P6.7 is what
// verifies the value.
const aMac = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

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

// rawDenied sends one non-multi-path request with the token these claims
// describe (the REST client has no root PUT) and demands a 401.
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
		h.t.Fatalf("%s /%s with this token: status %d, want 401", method, path, resp.StatusCode)
	}
	if after := h.snapshot(); after != before {
		h.t.Fatalf("%s /%s denied but the database changed", method, path)
	}
}
