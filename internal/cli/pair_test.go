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
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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
// the root's, which is silenced here as NewRootCmd silences it.
func pairRun(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "fugaro", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newPairCmd())
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"pair"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

// pairWriteState is what a paired device would have left behind: the device
// node and any pairing subtree. It is what every refusal test asserts is
// untouched.
func (f *pairFixture) pairWriteState() []any {
	return []any{f.db.Value("config/admin/devices"), f.db.Value("pairing")}
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
	newPairFixture(t, pairMe)
	out, _, err := pairRun(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	events := pairLines(t, out)
	if len(events) != 2 || events[0].Event != pairEventQR || events[1].Event != pairEventWaiting {
		t.Fatalf("events = %v", events)
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
	newPairFixture(t, pairMe)
	out, _, err := pairRun(t)
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
	newPairFixture(t, pairMe)
	out, _, err := pairRun(t, "--role", "viewer")
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

func TestPairJSONNeverReadsTTY(t *testing.T) {
	newPairFixture(t, pairMe)
	prev := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool {
		t.Fatal("--json opened the terminal")
		return false
	}
	t.Cleanup(func() { stdinIsTerminal = prev })
	out, _, err := pairRun(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pairLines(t, out)); n != 2 {
		t.Fatalf("--json wrote %d events, want 2", n)
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

	newPairFixture(t, pairMe)
	budgetTransport = pairDownTransport{}
	t.Cleanup(func() { budgetTransport = nil })
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

// pairGoldenDoc is the done event's example document, the shape T2b fills in.
func pairGoldenDoc() *pairDocument {
	return &pairDocument{
		Project:  "aurora",
		At:       pairT0.UTC().Format(time.RFC3339),
		Pairing:  pairPairing{UID: "p~" + strings.Repeat("A", 43), State: "completed", ExpiresAt: pairT0.Add(150 * time.Second).UTC().Format(time.RFC3339)},
		Request:  pairRequest{KeyFingerprint: "K7QM-3X2B", At: pairT0.UTC().Format(time.RFC3339)},
		Device:   pairDevice{UID: "m~" + strings.Repeat("B", 43), Name: "phone-a", Email: pairMe, Role: "operator"},
		Wrote:    []string{"config/admin/devices/m~" + strings.Repeat("B", 43), "pairing/p~" + strings.Repeat("A", 43) + "/delivery"},
		Refused:  []string{},
		Warnings: []string{},
	}
}

// pairEncoded is one event encoded exactly as the stream encodes it, so the
// golden pins the whole event set and not only what this command emits today.
func pairEncoded(t *testing.T, e pairEvent) string {
	t.Helper()
	var buf bytes.Buffer
	w := &pairEventWriter{w: &buf, project: "aurora", now: func() time.Time { return pairT0 }}
	if err := w.emit(e); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func TestPairJSONEventsGolden(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)

	out, _, err := pairRun(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := pairEventLines(t, out)
	got = append(got,
		pairEncoded(t, pairEvent{Event: pairEventCode, UID: uid, KeyFingerprint: "K7QM-3X2B"}),
		pairEncoded(t, pairEvent{Event: pairEventDone, UID: uid, Document: pairGoldenDoc()}),
	)
	refused, _, err := pairRun(t, "--json", "--role", "admin")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the refusal run: err = %v, want exit 1", err)
	}
	got = append(got, pairEventLines(t, refused)...)
	// The fake's URL carries the port it was given, which is a new one every
	// run: the golden pins everything else about it.
	for i, line := range got {
		got[i] = pairNoSignature(strings.ReplaceAll(line, f.db.URL, pairDBPlaceholder))
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

func TestPairJSONEventsDecodeStrictly(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "pair-events.golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(want), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("the golden holds %d events, want the whole set of 5", len(lines))
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
	if want := []string{pairEventQR, pairEventWaiting, pairEventCode, pairEventDone, pairEventRefused}; !slices.Equal(names, want) {
		t.Fatalf("the golden's events are %v, want %v", names, want)
	}
}
