package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The pairing fixture: a project config whose budget backend is the RTDB fake,
// whose signer is the IAM fake, whose lists place person-1 as an operator and
// person-2 as a launcher, and whose clock and identity are pinned.
type pairFixture struct {
	db  *gcpfake.RTDB
	iam *gcpfake.IAMCredentials
	dir string
}

const (
	pairMe       = "person-1@example.com"
	pairLauncher = "person-2@example.com"
	pairSigner   = "fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
	pairAPIKey   = "AIzaSyFakeWebApiKeyForTests000000000"
	// pairDBPlaceholder stands in for the RTDB fake's port, which changes
	// with every run of the suite.
	pairDBPlaceholder = "http://127.0.0.1:PORT"
)

var pairT0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// newPairFixture builds the fixture; edit, where a test gives one, rewrites the
// config text before it is written, which is how a test asks for a config that
// is missing something.
func newPairFixture(t *testing.T, me string, edit ...func(*string, *pairFixture)) *pairFixture {
	t.Helper()
	f := &pairFixture{db: gcpfake.NewRTDB(t), iam: gcpfake.NewIAMCredentials(t), dir: t.TempDir()}
	f.iam.AddSigner(pairSigner)
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n" +
		"bucket_url: file://" + runs + "\nuser: " + me + "\nmax_parallel: 2\n" +
		"budget: { mode: observe, per_run_usd: 5, rtdb_url: " + f.db.URL + ", token_signer: " + pairSigner + ", firebase_api_key: " + pairAPIKey + " }\n" +
		"endpoints: { no_auth: true, iam_credentials: " + f.iam.URL + "/ }\n" +
		"terraform: { operators: [user:" + pairMe + "], launchers: [user:" + pairLauncher + "] }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	for _, e := range edit {
		e(&cfg, f)
	}
	path := isolateProjects(t, f.dir)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	f.db.Set("fugaro/project", "aurora")
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	mp := filepath.Join(runs, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(mp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mp, marker, 0o644); err != nil {
		t.Fatal(err)
	}

	prevNow, prevUser := pairNow, authenticatedUser
	pairNow = func() time.Time { return pairT0 }
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return me, nil }
	t.Cleanup(func() { pairNow, authenticatedUser = prevNow, prevUser })
	return f
}

// pairRun runs the command under a bare root, because the real tree has no
// `fugaro pair` line yet (T4 adds it) and cobra's usage-and-error printing is
// the root's, which is silenced here as NewRootCmd silences it. stdin is a
// reader this test owns and never the process's, so no pairing test can block
// on a terminal the suite happened to be started from.
func pairRun(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return pairRunIn(t, "", args...)
}

// pairRunIn is pairRun with the operator's typed code on stdin: the one line
// the command reads for step 5 (empty for a test that refuses before it).
func pairRunIn(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	return pairRunCtx(t, context.Background(), stdin, args...)
}

// pairRunCtx is pairRunIn under a caller's context, which is the only way a
// test can be the operator who interrupts: the command's context is the one
// main.go cancels on a signal.
func pairRunCtx(t *testing.T, ctx context.Context, stdin string, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "fugaro", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newPairCmd())
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"pair"}, args...))
	err := root.ExecuteContext(ctx)
	return out.String(), errOut.String(), err
}

// pairWriteState is what a paired device would have left behind: the device
// node and any pairing subtree. It is what every refusal test asserts is
// untouched.
func (f *pairFixture) pairWriteState() []any {
	return []any{f.db.Value("config/admin/devices"), f.db.Value("pairing")}
}

// pairKeyBytes is the phone's X25519 public key every fixture's request node
// carries: 32 fixed bytes, so the fingerprint, the request node and the golden
// stream are one value. pairKey is that key in the one spelling §2 (e) pins.
var pairKeyBytes = func() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}()

var pairKey = base64.RawURLEncoding.EncodeToString(pairKeyBytes)

// pairSeedRequest is the phone's half of the session (§7.2): the request node
// it writes once, at the uid the pairing minted, before the operator types
// anything. key is what the node's `key` child holds, so a test can seed the
// spelling another writer would use.
func (f *pairFixture) pairSeedRequest(uid, key string) {
	f.db.Set("pairing/"+uid+"/request", map[string]any{"key": key, "at": pairT0.UnixMilli()})
}

// pairPair is one whole pairing of the happy path: the phone's request node is
// seeded at the uid pairFixedRand pins, and the operator types code — which
// must be the code the (fixed) key shows for the pairing to complete.
func pairPair(t *testing.T, f *pairFixture, code string, args ...string) (string, string, error) {
	t.Helper()
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	return pairRunIn(t, code, args...)
}

// pairNoSideEffect holds a refused pairing to M3's rule: no device node, no
// delivery, no device token, and the phone's own request node exactly where
// the phone left it. The delivery and the keychain item are T3's and this
// build holds no writer for either, so what a test can hold is that nothing
// the pairing would have produced exists — and that the one signJwt call is
// the pairing token's, not a device token's.
func (f *pairFixture) pairNoSideEffect(t *testing.T, uid string) {
	t.Helper()
	if v := f.db.Value("config/admin/devices"); v != nil {
		t.Fatalf("a refused pairing wrote the device allowlist: %v", v)
	}
	if v := f.db.Value("pairing/" + uid + "/delivery"); v != nil {
		t.Fatalf("a refused pairing wrote a delivery: %v", v)
	}
	if v := f.db.Value("pairing/" + uid + "/request"); v == nil {
		t.Fatalf("a refused pairing deleted the phone's own request node: the CLI writes nothing and deletes nothing before the code matches")
	}
	if n := len(f.iam.SignCalls()); n != 1 {
		t.Fatalf("a refused pairing called signJwt %d times, want the pairing token's one call and no device token", n)
	}
}

func TestPairTimeoutAboveCapRefused(t *testing.T) {
	f := newPairFixture(t, pairMe)
	_, _, err := pairRun(t, "--timeout", "5m")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	for _, want := range []string{"4m30s", "--timeout 5m0s"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("a refused timeout still called signJwt %d times", n)
	}
}

func TestPairQRHoldsNoKeyNoIdentity(t *testing.T) {
	f := newPairFixture(t, pairMe)
	out, _, err := pairPair(t, f, token.Fingerprint(pairKeyBytes), "--json")
	if err != nil {
		t.Fatal(err)
	}
	events := pairLines(t, out)
	if len(events) != 4 || events[0].Event != pairEventQR || events[1].Event != pairEventWaiting {
		t.Fatalf("events = %v, want qr, waiting and then the completed pairing", events)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(events[0].Payload), &payload); err != nil {
		t.Fatalf("the QR payload is not JSON: %v", err)
	}
	want := []string{"api_key", "database_url", "expires_at", "gcp_project", "project", "token", "uid", "v"}
	got := make([]string, 0, len(payload))
	for k := range payload {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("the QR payload carries %v, want %v", got, want)
	}
	if payload["api_key"] != pairAPIKey || payload["database_url"] == "" || payload["project"] != "aurora" {
		t.Fatalf("the QR payload does not carry the bootstrap configuration: %s", events[0].Payload)
	}
	for _, absent := range []string{`"key"`, `"email"`, `"name"`, `"role"`, `"fingerprint"`, "m~", pairMe, pairLauncher} {
		if strings.Contains(events[0].Payload, absent) {
			t.Fatalf("the QR payload carries %q: %s", absent, events[0].Payload)
		}
	}
	if token, _ := payload["token"].(string); token == "" {
		t.Fatalf("the QR payload carries no pairing token: %s", events[0].Payload)
	}
	if uid, _ := payload["uid"].(string); !strings.HasPrefix(uid, "p~") {
		t.Fatalf("the QR payload's uid = %q, want p~…", uid)
	}
}

func TestPairRoleDefaultsToPairingUsersRole(t *testing.T) {
	f := newPairFixture(t, pairMe)
	out, _, err := pairPair(t, f, token.Fingerprint(pairKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "role operator") {
		t.Fatalf("output does not inherit the pairing user's role:\n%s", out)
	}
}

func TestPairRoleAboveCallerRefused(t *testing.T) {
	f := newPairFixture(t, pairMe)
	_, _, err := pairRun(t, "--role", "admin")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(err.Error(), "above your own role") {
		t.Fatalf("refusal = %q", err)
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("a refused role still called signJwt %d times", n)
	}
	for _, w := range f.pairWriteState() {
		if w != nil {
			t.Fatalf("a refused role wrote %v", w)
		}
	}
}

func TestPairRoleLowersToViewer(t *testing.T) {
	f := newPairFixture(t, pairMe)
	out, _, err := pairPair(t, f, token.Fingerprint(pairKeyBytes), "--role", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "role viewer") {
		t.Fatalf("--role viewer did not lower the device's role:\n%s", out)
	}
}

func TestPairUnplacedCallerRefused(t *testing.T) {
	newPairFixture(t, "person-3@example.com")
	_, _, err := pairRun(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	for _, want := range []string{"user:person-3@example.com", "terraform.operators", "terraform.launchers"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

func TestPairMinterRoleMissingNamed(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.iam.Refuse(403, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission 'iam.serviceAccounts.signJwt' denied")
	_, _, err := pairRun(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	for _, want := range []string{"fugaroTokenMinter", pairSigner} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
	for _, w := range f.pairWriteState() {
		if w != nil {
			t.Fatalf("a refused mint wrote %v", w)
		}
	}
}

func TestPairDatabaseAdminMissingNamed(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.db.DenyRead("config/admin/devices")
	out, _, err := pairRun(t, "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(err.Error(), "roles/firebasedatabase.admin") {
		t.Fatalf("refusal = %q, want the missing role named", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != pairRefusedDatabaseAdmin {
		t.Fatalf("events = %v, want one refused with code %q", events, pairRefusedDatabaseAdmin)
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("a database-admin refusal still minted (%d signJwt calls)", n)
	}
	for _, w := range f.pairWriteState() {
		if w != nil {
			t.Fatalf("a refused pairing wrote %v", w)
		}
	}
}

func TestPairUnverifiedEmailRefused(t *testing.T) {
	newPairFixture(t, pairMe)
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) {
		return "", errors.New("the email person-2@example.com is not verified")
	}
	_, _, err := pairRun(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("refusal = %q", err)
	}
}

func TestPairAPIKeyMissingRefused(t *testing.T) {
	f := newPairFixture(t, pairMe, func(cfg *string, _ *pairFixture) {
		*cfg = strings.Replace(*cfg, ", firebase_api_key: "+pairAPIKey, "", 1)
	})
	_, _, err := pairRun(t, "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	for _, want := range []string{"budget.firebase_api_key", "fugaro update --firebase-project proj-1234"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("a missing api key still minted (%d signJwt calls)", n)
	}
}

// TestPairRefusalCodesNameTheCase holds the wire codes to the case they report:
// a console switches on them, so a refusal that is not a database failure must
// not be spelled as one.
func TestPairRefusalCodesNameTheCase(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		edit  func(*string, *pairFixture)
		after func(*pairFixture)
		env   map[string]string
		code  string
	}{
		{name: "a wait that is not a wait", args: []string{"--timeout", "0"}, code: pairRefusedTimeoutNotPositive},
		{name: "a project that cannot be selected", args: []string{"--project", "no-such-project"}, code: pairRefusedProject},
		{
			name: "a config with no budget backend",
			edit: func(cfg *string, f *pairFixture) { *cfg = strings.Replace(*cfg, ", rtdb_url: "+f.db.URL, "", 1) },
			code: pairRefusedBudget,
		},
		{
			name: "a config with no api key",
			edit: func(cfg *string, _ *pairFixture) {
				*cfg = strings.Replace(*cfg, ", firebase_api_key: "+pairAPIKey, "", 1)
			},
			code: pairRefusedAPIKey,
		},
		{name: "the debug env", env: map[string]string{"GODEBUG": "http2debug=1"}, code: pairRefusedEnv},
		{
			name:  "a database that is not this project's",
			after: func(f *pairFixture) { f.db.Set("fugaro/project", "somewhere-else") },
			code:  pairRefusedMismatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var edits []func(*string, *pairFixture)
			if tc.edit != nil {
				edits = append(edits, tc.edit)
			}
			f := newPairFixture(t, pairMe, edits...)
			if tc.after != nil {
				tc.after(f)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			out, _, err := pairRun(t, append([]string{"--json"}, tc.args...)...)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want exit 1", err)
			}
			events := pairLines(t, out)
			if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != tc.code {
				t.Fatalf("events = %v, want one refused with code %q", events, tc.code)
			}
		})
	}
}

// TestPairJSONNeverReadsTTY holds --json to the rule the console depends on:
// the mode asks no terminal anything and writes no prompt — the code it needs
// comes down the stdin line the caller handed it, both for the code step and
// for the key that precedes it. stdinIsTerminal is a seam the rest of the CLI
// asks; a --json pairing that asked it would be a pairing that could hang on a
// terminal nobody is looking at.
func TestPairJSONNeverReadsTTY(t *testing.T) {
	f := newPairFixture(t, pairMe)
	prev := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool {
		t.Fatal("--json opened the terminal")
		return false
	}
	t.Cleanup(func() { stdinIsTerminal = prev })
	out, _, err := pairPair(t, f, token.Fingerprint(pairKeyBytes), "--json")
	if err != nil {
		t.Fatal(err)
	}
	events := pairLines(t, out)
	if len(events) != 4 || events[2].Event != pairEventCode || events[3].Event != pairEventDone {
		t.Fatalf("events = %v, want the code step and the done event after the wait", events)
	}
	if strings.Contains(out, "type that code") {
		t.Fatalf("--json wrote a prompt:\n%s", out)
	}
}

func TestPairJSONRefusalExitCodes(t *testing.T) {
	newPairFixture(t, pairMe)
	out, _, err := pairRun(t, "--json", "--role", "admin")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("operator-fixable refusal: err = %v, want exit 1", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != pairRefusedRoleAbove {
		t.Fatalf("events = %v, want one refused", events)
	}

	// A code that does not match is a refusal, not a retry, and it is exit 1:
	// the operator can scan again.
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	out, _, err = pairRunIn(t, "K7QM-3X2Y", "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a wrong code: err = %v, want exit 1", err)
	}
	if events = pairLines(t, out); len(events) != 4 || events[3].Code != pairRefusedCodeMismatch {
		t.Fatalf("events = %v, want the code event and then one refused with %q", events, pairRefusedCodeMismatch)
	}

	// A phone that never writes its key is exit 1 too, and names the wait
	// rather than the database.
	f = newPairFixture(t, pairMe)
	out, _, err = pairRun(t, "--json", "--timeout", "40ms")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("no key: err = %v, want exit 1", err)
	}
	if events = pairLines(t, out); len(events) != 3 || events[2].Code != pairRefusedNoKey {
		t.Fatalf("events = %v, want one refused with %q", events, pairRefusedNoKey)
	}

	budgetTransport = pairDownTransport{}
	t.Cleanup(func() { budgetTransport = nil })
	newPairFixture(t, pairMe)
	out, _, err = pairRun(t, "--json")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("unreachable database: err = %v, want exit 2", err)
	}
	events = pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != pairRefusedUnreachable {
		t.Fatalf("events = %v, want one refused", events)
	}
}

type pairDownTransport struct{}

func (pairDownTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("the database is unreachable")
}

// TestPairWaitEndingsAreToldApart holds the two ways the wait can end on its
// own to the events they are: the --timeout running out under a read already
// in flight, which is the path the phone never scanning takes and the one
// exit 1 the console documents, and the operator's own interrupt, which is
// neither the phone's failure nor an outage. Both used to be reported as a
// database that could not be reached (exit 2), and the interrupt as the
// phone's silence.
func TestPairWaitEndingsAreToldApart(t *testing.T) {
	cases := []struct {
		name      string
		timeout   string
		interrupt bool
		code      string
		reason    string
	}{
		{
			name:    "the deadline cancels the read in flight",
			timeout: "40ms",
			code:    pairRefusedNoKey,
			reason:  "nothing was written",
		},
		{
			name:      "the operator interrupts the wait",
			timeout:   "5s",
			interrupt: true,
			code:      pairRefusedInterrupted,
			reason:    "abandoned by the operator",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPairFixture(t, pairMe)
			// The request node is held unanswered, so the read that is in
			// flight is the one the wait's own ending cancels.
			uid := pairFixedRand(t)
			stamped, release := f.db.HoldNext("pairing/" + uid + "/request")
			defer release()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				out string
				err error
			}
			done := make(chan result, 1)
			go func() {
				out, _, err := pairRunCtx(t, ctx, "", "--json", "--timeout", tc.timeout)
				done <- result{out, err}
			}()
			select {
			case <-stamped:
			case <-time.After(30 * time.Second):
				t.Fatal("the wait never reached the phone's request node")
			}
			if tc.interrupt {
				cancel()
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("the command never returned")
			}
			if got.err == nil || ExitCode(got.err) != ExitUserError {
				t.Fatalf("err = %v, want exit 1", got.err)
			}
			events := pairLines(t, got.out)
			if len(events) != 3 || events[2].Event != pairEventRefused || events[2].Code != tc.code {
				t.Fatalf("events = %v, want one refused with code %q", events, tc.code)
			}
			if !strings.Contains(events[2].Reason, tc.reason) {
				t.Fatalf("the refusal does not say %q: %q", tc.reason, events[2].Reason)
			}
			// The phone wrote nothing here, so this is pairNoSideEffect's
			// rule without its request-node half: no device node, no
			// delivery, and the one signJwt call is the pairing token's.
			if v := f.db.Value("config/admin/devices"); v != nil {
				t.Fatalf("a refused pairing wrote the device allowlist: %v", v)
			}
			if v := f.db.Value("pairing/" + uid + "/delivery"); v != nil {
				t.Fatalf("a refused pairing wrote a delivery: %v", v)
			}
			if n := len(f.iam.SignCalls()); n != 1 {
				t.Fatalf("a refused pairing called signJwt %d times, want the pairing token's one call and no device token", n)
			}
		})
	}
}

// TestPairInterruptWhileTypingIsNotThePhone: the typed code's wait is the same
// --timeout, and an operator who stops it there is stopping it there — not a
// code that was never typed.
func TestPairInterruptWhileTypingIsNotThePhone(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)

	typing := &pairTyping{asked: make(chan struct{}), release: make(chan struct{})}
	defer close(typing.release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := &cobra.Command{Use: "fugaro", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newPairCmd())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetIn(typing)
	root.SetArgs([]string{"pair", "--json", "--timeout", "5s"})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	// The phone's key is seeded, so the command reaches the code step at
	// once and the read it is waiting on is the operator's; the interrupt
	// arrives while that read is unanswered.
	select {
	case <-typing.asked:
	case <-time.After(30 * time.Second):
		t.Fatal("the command never asked for the typed code")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v, want exit 1", err)
		}
		events := pairLines(t, out.String())
		if len(events) != 4 || events[3].Code != pairRefusedInterrupted {
			t.Fatalf("events = %v, want the code event and then %q", events, pairRefusedInterrupted)
		}
		if strings.Contains(events[3].Reason, "no line arrived") {
			t.Fatalf("an interrupt was reported as a code that never arrived: %q", events[3].Reason)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the command never returned")
	}
	if v := f.db.Value("config/admin/devices"); v != nil {
		t.Fatalf("an interrupted pairing wrote the device allowlist: %v", v)
	}
}

// pairTyping is the operator at the prompt: a reader that reports when the
// command asks it for the code and then answers nothing until it is released,
// so the interrupt lands while that read is in flight rather than at a sleep
// the test guessed at.
type pairTyping struct {
	asked   chan struct{}
	release chan struct{}
}

func (p *pairTyping) Read([]byte) (int, error) {
	select {
	case <-p.asked:
	default:
		close(p.asked)
	}
	<-p.release
	return 0, io.EOF
}

// TestPairUnreachableIdentityIsNotUnverifiedEmail: the email check reads from
// the identity endpoint, so it can fail remote as well as by answering "not
// verified" — and §4.6 puts the two in different exit rows. Reporting an
// outage as an unverified address sends the operator to fix an address that
// was never the problem.
func TestPairUnreachableIdentityIsNotUnverifiedEmail(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
		exit int
	}{
		{name: "an endpoint that cannot be reached", err: remote(errors.New("the identity endpoint is unreachable")), code: pairRefusedUnreachable, exit: ExitRemoteError},
		{name: "an address that is not verified", err: errors.New("the email person-1@example.com is not verified"), code: pairRefusedEmail, exit: ExitUserError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newPairFixture(t, pairMe)
			authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return "", tc.err }
			out, _, err := pairRun(t, "--json")
			if err == nil || ExitCode(err) != tc.exit {
				t.Fatalf("err = %v, want exit %d", err, tc.exit)
			}
			events := pairLines(t, out)
			if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != tc.code {
				t.Fatalf("events = %v, want one refused with code %q", events, tc.code)
			}
		})
	}
}

// TestPairDeviceWriteDeniedRefused is the other half of the database-admin
// refusal: the read the probe performs succeeds and the write the pairing
// needs is the one the rules deny, which is the case an installation with
// read access but no admin reaches. The device token is already minted by
// then, so what the test can hold is that the refusal names the role and that
// no node exists for the token to open.
func TestPairDeviceWriteDeniedRefused(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	f.db.DenyNext(1)

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(err.Error(), "roles/firebasedatabase.admin") {
		t.Fatalf("refusal = %q, want the missing role named", err)
	}
	events := pairLines(t, out)
	if len(events) != 4 || events[3].Event != pairEventRefused || events[3].Code != pairRefusedDatabaseAdmin {
		t.Fatalf("events = %v, want the refusal after the code event with code %q", events, pairRefusedDatabaseAdmin)
	}
	if v := f.db.Value("config/admin/devices"); v != nil {
		t.Fatalf("a write the rules denied left a device node: %v", v)
	}
	if v := f.db.Value("pairing/" + uid + "/delivery"); v != nil {
		t.Fatalf("a refused pairing wrote a delivery: %v", v)
	}
}

// pairLines decodes a --json stream into the events it carries, strictly: an
// unknown field is a failure, not something to ignore.
func pairLines(t *testing.T, out string) []pairEvent {
	t.Helper()
	var events []pairEvent
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var e pairEvent
		if err := dec.Decode(&e); err != nil {
			t.Fatalf("event %q does not decode strictly: %v", line, err)
		}
		if e.V != pairEventVersion {
			t.Fatalf("event %q has v = %d, want %d", line, e.V, pairEventVersion)
		}
		events = append(events, e)
	}
	return events
}

// pairEventLines is a stream's raw lines.
func pairEventLines(t *testing.T, out string) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

// pairFixedRand pins the pairing uid: crypto/rand.Reader is the one source
// pairing.go draws from, so replacing it fixes the uid, the token the fake
// signer returns and every byte of the golden stream. The reader repeats rather
// than running out, so a second draw in the same test returns the same bytes
// instead of an error.
func pairFixedRand(t *testing.T) string {
	t.Helper()
	fixed := make([]byte, 32)
	for i := range fixed {
		fixed[i] = byte(i)
	}
	prev := rand.Reader
	rand.Reader = pairCyclicRand{fixed}
	t.Cleanup(func() { rand.Reader = prev })
	return "p~" + base64.RawURLEncoding.EncodeToString(fixed)
}

type pairCyclicRand struct{ b []byte }

func (r pairCyclicRand) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b[i%len(r.b)]
	}
	return len(p), nil
}

// pairTokenRE finds the pairing token inside a stream line. The qr event
// carries the payload as a JSON string, so the token's own quotes are escaped.
var pairTokenRE = regexp.MustCompile(`(\\"token\\":\\"[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.)[A-Za-z0-9_-]*`)

// pairNoSignature replaces the token's signature with a fixed marker, in the
// stream and in the golden alike. The IAM fake keys its HMAC with the test's
// own name, so those bytes are the fake's and not the command's: what the
// golden pins is the header and the payload — the claims the command asked for
// — and a renamed or subtested test must not turn into an opaque byte diff.
func pairNoSignature(line string) string {
	return pairTokenRE.ReplaceAllString(line, "${1}signature")
}

// pairDeviceUIDRE finds the device uid in a stream line: m~ and the 22
// unpadded base64url characters deviceNewUID draws from crypto/rand. The token
// package replaces its own reader only for its own tests (export_test.go), so
// a device uid is the one value in this stream this suite cannot pin; the
// golden carries the marker below instead, and the width it normalises is the
// pin: 16 random bytes are 22 characters, and a uid of another shape would not
// be normalised at all.
var pairDeviceUIDRE = regexp.MustCompile(`m~[A-Za-z0-9_-]{22}`)

const pairDeviceUIDMarker = "m~" + "BBBBBBBBBBBBBBBBBBBBBB"

// pairNoUID replaces that one drawn value, in the stream and in the golden
// alike, exactly as pairNoSignature replaces the fake's signature bytes.
func pairNoUID(line string) string {
	return pairDeviceUIDRE.ReplaceAllString(line, pairDeviceUIDMarker)
}

// TestPairJSONEventsGolden pins the whole event set: the QR, the wait, the code
// that asks for the typed one, the document a completed pairing carries, and
// the refusal a photographed QR produces. The stream lines are the command's
// own output, not events this test builds, so what is pinned is what a console
// will actually read.
func TestPairJSONEventsGolden(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
	if err != nil {
		t.Fatal(err)
	}
	got := pairEventLines(t, out)

	refused, _, err := pairRunIn(t, "K7QM-3X2Y", "--json", "--name", "phone-a")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the mismatch run: err = %v, want exit 1", err)
	}
	got = append(got, pairEventLines(t, refused)...)
	if len(got) != 8 {
		t.Fatalf("the two streams carry %d events, want the four of a completed pairing and the four of a refused one:\n%s", len(got), strings.Join(got, "\n"))
	}
	// The fake's URL carries the port it was given, which is a new one every
	// run: the golden pins everything else about it.
	for i, line := range got {
		got[i] = pairNoUID(pairNoSignature(strings.ReplaceAll(line, f.db.URL, pairDBPlaceholder)))
	}

	want, err := os.ReadFile(filepath.Join("testdata", "pair-events.golden.jsonl"))
	if err != nil {
		t.Fatalf("testdata/pair-events.golden.jsonl is missing; the stream is:\n%s", strings.Join(got, "\n"))
	}
	lines := strings.Split(strings.TrimSuffix(string(want), "\n"), "\n")
	if len(lines) != len(got) {
		t.Fatalf("the golden holds %d events, the stream %d:\n%s", len(lines), len(got), strings.Join(got, "\n"))
	}
	for i := range lines {
		if lines[i] != got[i] {
			t.Fatalf("event %d:\n got %s\nwant %s", i, got[i], lines[i])
		}
	}

}

// pairOtherKeyBytes is a second X25519 public key: what whoever photographed
// the QR writes at the pairing uid's request node. pairOtherKey is it in the
// one spelling this build reads.
var pairOtherKeyBytes = func() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 32)
	}
	return key
}()

var pairOtherKey = base64.RawURLEncoding.EncodeToString(pairOtherKeyBytes)

// pairWrongCode is a code the fixture's key does not show, asserted to be one
// so that a change to the fingerprint cannot quietly turn "wrong" into "right".
func pairWrongCode(t *testing.T) string {
	t.Helper()
	const wrong = "K7QM-3X2Y"
	if token.CompareFingerprint(wrong, token.Fingerprint(pairKeyBytes)) {
		t.Fatalf("the mismatch code %s is the fixture key's own code", wrong)
	}
	return wrong
}

// TestNoNodeBeforeTheCodeMatches is M3's ordering, and it is the one a reader
// of a passing diff cannot see: a node written before the comparison leaves a
// device on D166's allowlist that nobody confirmed. The mutation T2b was
// written against writes the node as soon as the fingerprint is derived, and
// this test is what catches it.
func TestNoNodeBeforeTheCodeMatches(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)

	out, _, err := pairRunIn(t, pairWrongCode(t), "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if events := pairLines(t, out); len(events) != 4 || events[3].Event != pairEventRefused || events[3].Code != pairRefusedCodeMismatch {
		t.Fatalf("events = %v, want the code event and then one refused with %q", events, pairRefusedCodeMismatch)
	}
	if v := f.db.Value("config/admin/devices"); v != nil {
		t.Fatalf("the device node was written before the typed code matched: %v", v)
	}
	f.pairNoSideEffect(t, uid)
}

// TestPairPhotographedQRRefused is the case M3 exists for: the request node at
// the pairing uid was written by a key that is not the key of the phone in the
// operator's hand — a photographed QR, and whoever copied it wrote their own —
// so the code the operator types, off their own phone's screen, does not match
// what the CLI read. It is refused as that case, once: a request under a key
// whose code does not match is not retried.
func TestPairPhotographedQRRefused(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairOtherKey)

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	events := pairLines(t, out)
	if len(events) != 4 || events[3].Event != pairEventRefused || events[3].Code != pairRefusedCodeMismatch {
		t.Fatalf("events = %v, want one refusal and no retry", events)
	}
	if !strings.Contains(events[3].Reason, "photographed") {
		t.Fatalf("the refusal does not name the copied-code case: %q", events[3].Reason)
	}
	f.pairNoSideEffect(t, uid)
}

// TestPairDeviceNodeHoldsNoSecret holds M13 to the writer: the node holds the
// name, the administered email, the role and the pairing time, and no token,
// no key and no fingerprint. No test in this repository can check that against
// the deployed document — the node's children are unconstrained — so what this
// test can hold is the shape of what the command wrote.
func TestPairDeviceNodeHoldsNoSecret(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a", "--email", pairMe)
	if err != nil {
		t.Fatal(err)
	}
	allowlist, ok := f.db.Value("config/admin/devices").(map[string]any)
	if !ok || len(allowlist) != 1 {
		t.Fatalf("the allowlist is %v, want exactly the one device the pairing wrote", f.db.Value("config/admin/devices"))
	}
	var deviceUID string
	var node map[string]any
	for key, child := range allowlist {
		device, ok := child.(map[string]any)
		if !ok {
			t.Fatalf("the device node is %v, want an object", child)
		}
		deviceUID, node = key, device
	}
	if !strings.HasPrefix(deviceUID, "m~") || strings.HasPrefix(deviceUID, "r~") {
		t.Fatalf("the device uid is %q, want m~… (never r~, the runs bucket's family)", deviceUID)
	}
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"at", "email", "name", "role"}; !slices.Equal(keys, want) {
		t.Fatalf("the device node holds %v, want %v and nothing else", keys, want)
	}
	if node["name"] != "phone-a" || node["email"] != pairMe || node["role"] != "operator" || node["at"] == "" {
		t.Fatalf("the device node is %v", node)
	}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"token", "fingerprint", token.Fingerprint(pairKeyBytes), pairKey} {
		if strings.Contains(string(raw), absent) {
			t.Fatalf("the device node carries %q: %s", absent, raw)
		}
	}
	// M4: the node's key IS the token's uid, and the rules match that uid
	// against the token's mu claim.
	calls := f.iam.SignCalls()
	if len(calls) != 2 {
		t.Fatalf("the pairing minted %d tokens, want the pairing's and the device's", len(calls))
	}
	var claims map[string]any
	if err := json.Unmarshal([]byte(calls[1].Payload), &claims); err != nil {
		t.Fatalf("the device token's payload does not decode: %v", err)
	}
	inner, _ := claims["claims"].(map[string]any)
	if claims["uid"] != deviceUID || inner["mu"] != deviceUID {
		t.Fatalf("the device token carries uid %v and mu %v, want the node's %s", claims["uid"], inner["mu"], deviceUID)
	}
	if u, _ := claims["uid"].(string); !strings.HasPrefix(u, "m~") || strings.HasPrefix(u, "r~") {
		t.Fatalf("the device token's uid is %q, want m~… and never r~", u)
	}
	if !strings.Contains(out, deviceUID) {
		t.Fatalf("the command does not print what it wrote:\n%s", out)
	}
}

// TestPairCodeFromDecodedKey: the code both ends compare is derived from the
// bytes the request's key decodes to (M3), and a request that spells the same
// key another way is refused rather than re-spelled by the CLI (T2a's
// DecodeDeviceKey) — one key, one code, and no second way to name it.
func TestPairCodeFromDecodedKey(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	fingerprint := token.Fingerprint(pairKeyBytes)

	out, _, err := pairRunIn(t, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{fingerprint, "type that code"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the prompt does not print %q:\n%s", want, out)
		}
	}

	// The same 32 bytes, in the padded spelling the rules also admit.
	f2 := newPairFixture(t, pairMe)
	uid2 := pairFixedRand(t)
	f2.pairSeedRequest(uid2, base64.URLEncoding.EncodeToString(pairKeyBytes))
	out, _, err = pairRunIn(t, fingerprint, "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a request in another spelling: err = %v, want exit 1 (%s)", err, out)
	}
	if events := pairLines(t, out); len(events) != 3 || events[2].Code != pairRefusedRequestKey {
		t.Fatalf("events = %v, want one refused with %q", events, pairRefusedRequestKey)
	}
	f2.pairNoSideEffect(t, uid2)
}

func TestPairJSONReadsCodeFromStdinLine(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	fingerprint := token.Fingerprint(pairKeyBytes)

	// The line a person types: lower case, no hyphen, the spaces a terminal
	// leaves around it, and a second line that must never be read.
	typed := "  " + strings.ToLower(strings.ReplaceAll(fingerprint, "-", "")) + "  \nthis line is not the code\n"
	out, _, err := pairRunIn(t, typed, "--json")
	if err != nil {
		t.Fatal(err)
	}
	events := pairLines(t, out)
	if len(events) != 4 || events[3].Event != pairEventDone {
		t.Fatalf("events = %v, want the done event: the first line is the code", events)
	}
	if got := events[3].Document.Request.KeyFingerprint; got != fingerprint {
		t.Fatalf("the document's fingerprint is %q, want %q", got, fingerprint)
	}
}

// TestPairJSONEOFBeforeCodeRefused: the code is one stdin line, and a stdin
// that ends — or carries an empty line — before the wait runs out is a refusal
// with no side effect, not a prompt and not a retry.
func TestPairJSONEOFBeforeCodeRefused(t *testing.T) {
	for _, stdin := range []string{"", "\n", "   \n"} {
		t.Run("stdin "+strconv.Quote(stdin), func(t *testing.T) {
			f := newPairFixture(t, pairMe)
			uid := pairFixedRand(t)
			f.pairSeedRequest(uid, pairKey)

			out, _, err := pairRunIn(t, stdin, "--json")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want exit 1", err)
			}
			events := pairLines(t, out)
			if len(events) != 4 || events[3].Event != pairEventRefused || events[3].Code != pairRefusedCodeMissing {
				t.Fatalf("events = %v, want one refused with %q", events, pairRefusedCodeMissing)
			}
			f.pairNoSideEffect(t, uid)
		})
	}
}

// TestPairJSONCodeEventHoldsNoFingerprint: the code event asks for the code and
// carries nothing else — a console that learned the code there could type it
// back without anyone looking at the phone (M3). The done event may carry the
// fingerprint, and §4.5 says so: it is a hash of a public key and the operator
// has already read it off their own screen.
func TestPairJSONCodeEventHoldsNoFingerprint(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	fingerprint := token.Fingerprint(pairKeyBytes)

	out, _, err := pairRunIn(t, fingerprint, "--json")
	if err != nil {
		t.Fatal(err)
	}
	lines := pairEventLines(t, out)
	if !strings.Contains(lines[2], `"event":"code"`) {
		t.Fatalf("event 2 is not the code event: %s", lines[2])
	}
	for _, spelled := range []string{fingerprint, strings.ReplaceAll(fingerprint, "-", "")} {
		if strings.Contains(lines[2], spelled) {
			t.Fatalf("the code event carries the code it asks for: %s", lines[2])
		}
	}
	if !strings.Contains(lines[3], fingerprint) {
		t.Fatalf("the done event does not carry the fingerprint the CLI computed: %s", lines[3])
	}
}

// TestPairEmailNotOperatorsRefused is D226 as the plan's §12 reads it: the
// administered email on a device node is an address this installation has
// proved the pairing operator holds, so --email is not a free field. The
// operator's own address is the one that passes.
func TestPairEmailNotOperatorsRefused(t *testing.T) {
	f := newPairFixture(t, pairMe)
	out, _, err := pairRun(t, "--email", pairLauncher, "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != pairRefusedEmail {
		t.Fatalf("events = %v, want one refused with %q", events, pairRefusedEmail)
	}
	for _, want := range []string{pairLauncher, pairMe} {
		if !strings.Contains(events[0].Reason, want) {
			t.Fatalf("the refusal %q does not name %q", events[0].Reason, want)
		}
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("a refused email still minted (%d signJwt calls)", n)
	}
	for _, w := range f.pairWriteState() {
		if w != nil {
			t.Fatalf("a refused email wrote %v", w)
		}
	}

	// The operator's own address, spelled as the operator's provider spells
	// it, is the one the node takes.
	f2 := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f2.pairSeedRequest(uid, pairKey)
	if _, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--email", pairMe); err != nil {
		t.Fatal(err)
	}
	allowlist, _ := f2.db.Value("config/admin/devices").(map[string]any)
	if len(allowlist) != 1 {
		t.Fatalf("the allowlist is %v, want one device", f2.db.Value("config/admin/devices"))
	}
	for _, child := range allowlist {
		if node, _ := child.(map[string]any); node["email"] != pairMe {
			t.Fatalf("the device node's email is %v, want %s", node["email"], pairMe)
		}
	}
}

// TestPairMeFailureIsNotUnplaced: the identity the command reads has a refusal
// of its own. Dropping lc.Me's error told an operator whose identity could not
// be read that they were on neither of the installation's lists — a fixable
// credential problem reported as a list problem, and the wrong wire code for a
// console to act on.
func TestPairMeFailureIsNotUnplaced(t *testing.T) {
	f := newPairFixture(t, pairMe)
	prev := pairMeLookup
	t.Cleanup(func() { pairMeLookup = prev })

	pairMeLookup = func(context.Context, *localcfg.Config) (string, error) {
		return "", errors.New("cannot tell who you are: set user in the local config or git config user.email")
	}
	out, _, err := pairRun(t, "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Code != pairRefusedIdentity {
		t.Fatalf("events = %v, want one refused with %q", events, pairRefusedIdentity)
	}
	for _, absent := range []string{"terraform.operators", "terraform.launchers", "neither of this installation's lists"} {
		if strings.Contains(events[0].Reason, absent) {
			t.Fatalf("an identity failure reads as a list failure: %q", events[0].Reason)
		}
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("a failed identity lookup still minted (%d signJwt calls)", n)
	}

	// A failure the error itself classes as remote keeps exit 2.
	pairMeLookup = func(context.Context, *localcfg.Config) (string, error) {
		return "", remote(errors.New("asking who you are: the identity endpoint is unreachable"))
	}
	out, _, err = pairRun(t, "--json")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("a remote identity failure: err = %v, want exit 2", err)
	}
	if events = pairLines(t, out); len(events) != 1 || events[0].Code != pairRefusedUnreachable {
		t.Fatalf("events = %v, want one refused with %q", events, pairRefusedUnreachable)
	}
}

func TestPairJSONEventsDecodeStrictly(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "pair-events.golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(want), "\n"), "\n")
	if len(lines) != 8 {
		t.Fatalf("the golden holds %d events, want the two whole streams: a completed pairing and a refused one", len(lines))
	}
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var e pairEvent
		if err := dec.Decode(&e); err != nil {
			t.Fatalf("event %q does not decode strictly: %v", line, err)
		}
		if e.V != pairEventVersion {
			t.Fatalf("event %q has v = %d, want %d", line, e.V, pairEventVersion)
		}
		if e.Project != "aurora" || e.At == "" {
			t.Fatalf("event %q carries no project or time", line)
		}
		names = append(names, e.Event)
	}
	if want := []string{pairEventQR, pairEventWaiting, pairEventCode, pairEventDone, pairEventQR, pairEventWaiting, pairEventCode, pairEventRefused}; !slices.Equal(names, want) {
		t.Fatalf("the golden's events are %v, want %v", names, want)
	}
	for _, name := range []string{pairEventQR, pairEventWaiting, pairEventCode, pairEventDone, pairEventRefused} {
		if !slices.Contains(names, name) {
			t.Fatalf("the golden does not carry a %s event: %v", name, names)
		}
	}
}
