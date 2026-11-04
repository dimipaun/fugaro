package cli

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"
	"rsc.io/qr"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// The pairing fixture: a project config whose budget backend is the RTDB fake,
// whose signer is the IAM fake, whose lists place person-1 as an operator and
// person-2 as a launcher, and whose clock and identity are pinned.
type pairFixture struct {
	db  *gcpfake.RTDB
	iam *gcpfake.IAMCredentials
	dir string
	// keys is the OS keychain, as a Fake: M5's rule is that no test in this
	// repository touches a real keychain, so the command's pairKeychain seam is
	// pointed here and a test reads what the pairing stored.
	keys *keychain.Fake
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
	f := &pairFixture{db: gcpfake.NewRTDB(t), iam: gcpfake.NewIAMCredentials(t), dir: t.TempDir(), keys: &keychain.Fake{}}
	f.iam.AddSigner(pairSigner)
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n" +
		"bucket_url: file://" + runs + "\nuser: " + me + "\nmax_parallel: 2\n" +
		"budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + f.db.URL + ", token_signer: " + pairSigner + ", firebase_api_key: " + pairAPIKey + " }\n" +
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
	f.db.Set(budget.PathMark, auroraMark())
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

	prevNow, prevUser, prevKeys := pairNow, authenticatedUser, pairKeychain
	pairNow = func() time.Time { return pairT0 }
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return me, nil }
	pairKeychain = func() keychain.Store { return f.keys }
	t.Cleanup(func() { pairNow, authenticatedUser, pairKeychain = prevNow, prevUser, prevKeys })
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

// pairPhoneSeed is the phone's X25519 private key, and the one value every
// other value in this file is derived from. It is a real key pair and not 32
// arbitrary bytes because the delivery is sealed *to* this key (§8): the only
// way a test can hold the command to "the phone can open it and nobody else
// can" is to open it as the phone does, with crypto/ecdh over the private half
// the phone never sends.
var pairPhoneSeed = func() []byte {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	return seed
}()

// pairKeyBytes is the phone's public key every fixture's request node carries,
// derived from that seed as the phone derives it, so the fingerprint, the
// request node, the delivery's AAD and the golden stream are one value. pairKey
// is that key in the one spelling §2 (e) pins.
var pairKeyBytes = func() []byte {
	priv, err := ecdh.X25519().NewPrivateKey(pairPhoneSeed)
	if err != nil {
		panic("the fixture's phone key is not a usable X25519 private key: " + err.Error())
	}
	return priv.PublicKey().Bytes()
}()

var pairKey = base64.RawURLEncoding.EncodeToString(pairKeyBytes)

// pairSeedRequest is the phone's half of the session (§7.2): the request node
// it writes once, at the uid the pairing minted, before the operator types
// anything. key is what the node's `key` child holds, so a test can seed the
// spelling another writer would use.
func (f *pairFixture) pairSeedRequest(uid, key string) {
	f.db.Set("pairing/"+uid+"/request", map[string]any{"key": key, "at": pairT0.UnixMilli()})
}

// pairSealedDelivery is the envelope the command wrote at uid's delivery child,
// taken from the fake's own record of the requests rather than from the node:
// §7.1 step 9 deletes the whole pairing subtree before the command returns,
// which is the point of the step, so a test reads the delivery where it was
// sent. The node's shape is checked here too — the version byte outside the
// envelope is the one the phone refuses a foreign build's payload on.
func (f *pairFixture) pairSealedDelivery(t *testing.T, uid string) []byte {
	t.Helper()
	want := "/" + pairPath(uid) + ".json"
	for _, r := range f.db.Requests() {
		if r.Method != http.MethodPatch || r.Path != want {
			continue
		}
		var patch map[string]json.RawMessage
		if err := json.Unmarshal(r.Body, &patch); err != nil {
			t.Fatalf("the patch to %s does not decode: %v", want, err)
		}
		raw, ok := patch["delivery"]
		if !ok {
			continue
		}
		var node struct {
			V      int    `json:"v"`
			Sealed string `json:"sealed"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatalf("the delivery node does not decode: %v", err)
		}
		if node.V != 1 {
			t.Fatalf("the delivery node's version is %d, want 1", node.V)
		}
		sealed, err := base64.RawURLEncoding.DecodeString(node.Sealed)
		if err != nil {
			t.Fatalf("the delivery node's sealed value is not unpadded base64url: %v", err)
		}
		return sealed
	}
	t.Fatalf("nothing was written at %s", want)
	return nil
}

// pairOpenDelivery is the phone's half of §8's envelope, written out here
// rather than called: the token package's own opener is an internal test
// export and is not reachable from this package, and a test that asked the
// command's own package to open what the command sealed would not be testing
// that *the phone* can. Every byte below is §8's — the layout, the HKDF info
// string, the AAD's four parts in their order — and the seed is the phone's
// private key, which the phone never sends.
func pairOpenDelivery(t *testing.T, uid string, envelope []byte) []byte {
	t.Helper()
	plain, err := pairOpenWith(pairPhoneSeed, uid, envelope)
	if err != nil {
		t.Fatalf("the phone could not open the delivery sealed for it: %v", err)
	}
	return plain
}

// pairOpenWith is pairOpenDelivery with the phone's private key and the uid
// named: the two things §8 binds the envelope to, so a test can hold the
// command to both by trying the other key and the other uid.
func pairOpenWith(seed []byte, uid string, envelope []byte) ([]byte, error) {
	if len(envelope) < 1+token.DeviceKeySize+12+16 {
		return nil, fmt.Errorf("the delivery is %d bytes, shorter than an envelope", len(envelope))
	}
	if envelope[0] != token.DeliveryEnvelopeVersion {
		return nil, fmt.Errorf("the envelope's first byte is %#x, want %#x", envelope[0], token.DeliveryEnvelopeVersion)
	}
	priv, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		return nil, err
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(envelope[1 : 1+token.DeviceKeySize])
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(ephemeral)
	if err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, shared, nil, "fugaro-pairing-v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	aad := append([]byte("pairing/"+uid), 0)
	aad = append(aad, priv.PublicKey().Bytes()...)
	aad = append(aad, 0, envelope[0])
	body := envelope[1+token.DeviceKeySize:]
	return g.Open(nil, body[:12], body[12:], aad)
}

// pairStoredKey is the one key the fixture's keychain holds, with the item name
// it is filed under: M5's rule is one item per pairing, so a completed pairing
// leaves exactly one.
func (f *pairFixture) pairStoredKey(t *testing.T) (string, string) {
	t.Helper()
	if len(f.keys.Keys) != 1 {
		t.Fatalf("the keychain holds %d items, want the one this pairing wrote: %v", len(f.keys.Keys), f.keys.Keys)
	}
	for name, value := range f.keys.Keys {
		return name, value
	}
	return "", ""
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
// delivery, no device token, no keychain item, and the phone's own request
// node exactly where the phone left it. Everything past the typed code is
// behind the comparison, so a refusal before it must leave none of it — and
// the one signJwt call is the pairing token's, not a device token's.
func (f *pairFixture) pairNoSideEffect(t *testing.T, uid string) {
	t.Helper()
	if v := f.db.Value("config/admin/devices"); v != nil {
		t.Fatalf("a refused pairing wrote the device allowlist: %v", v)
	}
	if v := f.db.Value("pairing/" + uid + "/delivery"); v != nil {
		t.Fatalf("a refused pairing wrote a delivery: %v", v)
	}
	if len(f.keys.Keys) != 0 {
		t.Fatalf("a refused pairing wrote the keychain: %v", f.keys.Keys)
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

// pairTTYReader is a terminal that fails the test when it is read. The two
// seams below are different failures and both are needed: stdinIsTerminal is
// the question "is this a terminal", which a --json pairing that asked it
// would hang on, and this is the read itself, which catches the path that
// reaches for the terminal without asking — the one that would sit on a
// prompt nobody can see. The code step's reader is stdin in --json and
// pairTerminal's otherwise, so pointing pairTerminal here is what makes the
// test able to fail.
type pairTTYReader struct{ t *testing.T }

func (r pairTTYReader) Read([]byte) (int, error) {
	r.t.Errorf("--json read the terminal")
	return 0, io.EOF
}

// TestPairJSONNeverReadsTTY holds --json to the rule the console depends on:
// the mode asks no terminal anything and writes no prompt — the code it needs
// comes down the stdin line the caller handed it, both for the code step and
// for the key that precedes it.
func TestPairJSONNeverReadsTTY(t *testing.T) {
	f := newPairFixture(t, pairMe)
	prev, prevTerminal := stdinIsTerminal, pairTerminal
	stdinIsTerminal = func(io.Reader) bool {
		t.Fatal("--json opened the terminal")
		return false
	}
	pairTerminal = func(*cobra.Command) io.Reader { return pairTTYReader{t} }
	t.Cleanup(func() { stdinIsTerminal, pairTerminal = prev, prevTerminal })
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

// pairItemNameRE finds the keychain item name the done document carries. The
// name is derived from the device uid, so it is drawn from crypto/rand too and
// is normalised the same way: the 22 characters after the project are the uid's
// own, and the marker keeps the golden readable. The project comes first and
// there is no other word in the name — pairItemName's own comment carries the
// arithmetic (a project name may be 40 characters and ParseItem's ceiling is
// 64) — so the pattern is the project, one dash, and the uid's body.
var pairItemNameRE = regexp.MustCompile(`aurora-[A-Za-z0-9_-]{22}`)

const pairItemNameMarker = "aurora-" + "BBBBBBBBBBBBBBBBBBBBBB"

// pairNoUID replaces those drawn values, in the stream and in the golden
// alike, exactly as pairNoSignature replaces the fake's signature bytes.
func pairNoUID(line string) string {
	return pairItemNameRE.ReplaceAllString(pairDeviceUIDRE.ReplaceAllString(line, pairDeviceUIDMarker), pairItemNameMarker)
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

	// The completed pairing deleted the pairing node (step 9), so the second
	// run has no request to read: the phone's key goes back where the phone put
	// it, which is what makes this a mismatch and not a wait that runs out.
	f.pairSeedRequest(uid, pairKey)
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

// pairStrangerSeed is a second X25519 private key: a phone that was not the one
// whose key the request node holds, which is the half §8's envelope is bound to
// and the half a test must hold it to.
var pairStrangerSeed = func() []byte {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i + 64)
	}
	return seed
}()

// pairStrangerUID is a second pairing uid, in the shape MintPairing mints: the
// other half of §8 — the AAD's first part, so an envelope opened under the
// wrong uid is refused even by the phone that could open it.
const pairStrangerUID = "p~AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// pairDeviceUID reads the one device node the pairing wrote and returns its
// uid, which is the key M4 matches the token's mu claim against and the name
// the keychain item is derived from.
func (f *pairFixture) pairDeviceUID(t *testing.T) string {
	t.Helper()
	allowlist, ok := f.db.Value("config/admin/devices").(map[string]any)
	if !ok || len(allowlist) != 1 {
		t.Fatalf("the allowlist is %v, want exactly the one device a completed pairing writes", f.db.Value("config/admin/devices"))
	}
	for uid := range allowlist {
		return uid
	}
	return ""
}

// TestPairDeliversSealedConfigurationThenDeletesTheNode is §7.1 steps 8 and 9
// together, which is the whole of T3b: what the phone stores, and what the
// laptop kept. The delivery is read back out of the fake's own record of the
// requests, because step 9 deletes the node it was written at before the
// command returns — so this test is also the one that says the ordering held:
// a deletion that ran before the write would leave the record of the write in
// the requests and no delivery anywhere, and a deletion that never ran would
// leave the node.
func TestPairDeliversSealedConfigurationThenDeletesTheNode(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a", "--email", pairMe)
	if err != nil {
		t.Fatal(err)
	}

	if v := f.db.Value(pairPath(uid)); v != nil {
		t.Fatalf("the pairing node is still there after a completed pairing: %v", v)
	}
	if v := f.db.Value("pairing"); v != nil {
		t.Fatalf("the pairing tree is still there after a completed pairing: %v", v)
	}
	deviceUID := f.pairDeviceUID(t)

	payload := pairOpenDelivery(t, uid, f.pairSealedDelivery(t, uid))
	var got struct {
		Config token.DeliveryConfig `json:"config"`
		Device struct {
			UID   string `json:"uid"`
			Token string `json:"token"`
		} `json:"device"`
		MACKey string `json:"mac_key"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("the payload the phone decodes does not: %v", err)
	}
	// D199: the delivery's configuration is the one the QR carried, field for
	// field, because P6.8 refuses a payload that disagrees with the scan.
	want := token.DeliveryConfig{Project: "aurora", GCPProject: "proj-1234", DatabaseURL: f.db.URL, APIKey: pairAPIKey}
	if got.Config != want {
		t.Fatalf("the delivery's configuration is %+v, want the QR's %+v", got.Config, want)
	}
	if got.Device.UID != deviceUID {
		t.Fatalf("the delivery names device %s, the allowlist holds %s", got.Device.UID, deviceUID)
	}
	// The token is the one M4 minted for this node: its own claims carry the
	// uid, and a delivery carrying another device's token would be a phone
	// admitted as someone else.
	parts := strings.Split(got.Device.Token, ".")
	if len(parts) != 3 {
		t.Fatalf("the delivered token is not a JWT: %q", got.Device.Token)
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("the delivered token's claims are not base64url: %v", err)
	}
	if !strings.Contains(string(claims), deviceUID) {
		t.Fatalf("the delivered token's claims do not name %s: %s", deviceUID, claims)
	}

	// M5 and decision 9: one item for this pairing, named after the device, and
	// holding the very key the phone was given — one value at both ends, which
	// is what the sealed payload above is checked against.
	name, value := f.pairStoredKey(t)
	if want := pairItemName("aurora", deviceUID); name != want {
		t.Fatalf("the MAC key is at %s, want %s", name, want)
	}
	if value != got.MACKey {
		t.Fatalf("the keychain holds a different key from the one delivered to the phone")
	}
	if n := len(value); n != 43 {
		t.Fatalf("the stored key is %d characters, want EncodeKey's 43", n)
	}

	events := pairLines(t, out)
	done := events[len(events)-1]
	if done.Event != pairEventDone || done.Document == nil {
		t.Fatalf("the stream ends with %v, want the done document", done)
	}
	if done.Document.KeychainItem != name {
		t.Fatalf("the document names item %q, want %q", done.Document.KeychainItem, name)
	}
	if want := []string{rules.PathDevices + "/" + deviceUID, pairPathDelivery(uid)}; !slices.Equal(done.Document.Wrote, want) {
		t.Fatalf("the document's wrote is %v, want %v", done.Document.Wrote, want)
	}
}

// TestPairItemNameFitsTheKeychain is the arithmetic behind the name's shape, at
// both ends of what a local config accepts. The ceiling is keychain.ParseItem's
// 64 characters, the body of the uid `fugaro pair` draws is 22 of them, and a
// project name is 1 to 40 (localcfg's own rule) — so the longest name a project
// can produce is 63 characters, and one fixed word in the name would put the
// longest project names past the ceiling: ParseItem would refuse it, the
// command would refuse the pairing as keychain_write_failed, and no keychain
// would ever have been asked anything (pair.go's own refusal says so). The
// rules that meet here are the config's name rule and this command's
// derivation, so the boundary belongs in this command's tests and not only in
// the keychain package's.
func TestPairItemNameFitsTheKeychain(t *testing.T) {
	body := strings.Repeat("B", 22)
	deviceUID := "m~" + body
	for _, project := range []string{"a", strings.Repeat("a", 40)} {
		name := pairItemName(project, deviceUID)
		if len(name) > keychain.ItemMax {
			t.Fatalf("a %d-character project makes a %d-character item name, past ParseItem's %d", len(project), len(name), keychain.ItemMax)
		}
		if _, err := keychain.ParseItem(name, project); err != nil {
			t.Fatalf("the name for a %d-character project is not one a key can be stored under: %v", len(project), err)
		}
		// Decision 9: `revoke` is given the device uid and nothing else, so the
		// name has to carry the uid's body, and it has to carry the project's
		// name, which is what keeps one project out of another's item (M5).
		if !strings.HasSuffix(name, body) || !strings.Contains(name, project) {
			t.Fatalf("the name %q is not derived from the project and the uid the pairing minted", name)
		}
	}
}

// TestPairDeliveryOpensOnlyWithThePhoneKey holds §8's two bindings: the ECDH
// key agreement and the AAD. The phone's own key opens it; a second key does
// not; and the phone's own key under another uid does not, which is what makes
// a delivery moved to a node another pairing owns unreadable rather than
// merely misplaced.
func TestPairDeliveryOpensOnlyWithThePhoneKey(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)

	if _, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a"); err != nil {
		t.Fatal(err)
	}
	sealed := f.pairSealedDelivery(t, uid)
	if len(pairOpenDelivery(t, uid, sealed)) == 0 {
		t.Fatal("the phone opened an empty payload")
	}

	if _, err := pairOpenWith(pairStrangerSeed, uid, sealed); err == nil {
		t.Fatal("a key that is not the phone's opened the delivery: the envelope is not sealed to the key that wrote the request")
	}
	if _, err := pairOpenWith(pairPhoneSeed, pairStrangerUID, sealed); err == nil {
		t.Fatal("the delivery opened under another pairing's uid: the AAD does not bind the node the payload was written at")
	}

	// The envelope's own first byte is outside the AAD by design (the AAD
	// carries the version, not the byte), so a test that never moved it would
	// not notice a seal that wrote a version the phone does not read.
	flipped := slices.Clone(sealed)
	flipped[0] = token.DeliveryEnvelopeVersion + 1
	if _, err := pairOpenWith(pairPhoneSeed, uid, flipped); err == nil {
		t.Fatal("the delivery opened after its version byte was changed")
	}
}

// TestPairMACKeyNeverPrinted is M5, over every output this command has: the
// two secret values — the MAC key the keychain holds and the device token the
// delivery carries — appear in stdout, in stderr (which is where the CLI's
// logger writes), in every event of a --json stream, and in the golden the
// stream is pinned by. The item's *name* is expected in the stream and is
// checked for: the name is what the operator records, and the rule that
// matters is that the value never joins it.
func TestPairMACKeyNeverPrinted(t *testing.T) {
	for _, mode := range []struct {
		name string
		args []string
	}{
		{"interactive", nil},
		{"json", []string{"--json"}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			f := newPairFixture(t, pairMe)
			uid := pairFixedRand(t)
			f.pairSeedRequest(uid, pairKey)

			args := append([]string{token.Fingerprint(pairKeyBytes), "--name", "phone-a"}, mode.args...)
			out, errOut, err := pairRunIn(t, args[0], args[1:]...)
			if err != nil {
				t.Fatal(err)
			}
			item, value := f.pairStoredKey(t)
			raw, err := base64.RawURLEncoding.DecodeString(value)
			if err != nil {
				t.Fatalf("the stored key is not the spelling the delivery carries: %v", err)
			}
			token64 := pairDeliveredToken(t, f, uid)

			secret := map[string]string{
				"the stored MAC key":         value,
				"the MAC key's raw bytes":    string(raw),
				"the MAC key in hex":         hex.EncodeToString(raw),
				"the delivered device token": token64,
			}
			for _, where := range []struct{ what, text string }{
				{"stdout", out},
				{"stderr", errOut},
			} {
				for what, s := range secret {
					if strings.Contains(where.text, s) {
						t.Fatalf("%s carries %s:\n%s", where.what, what, where.text)
					}
				}
			}
			// The name is the one thing about the item that may be printed, so
			// a stream that printed nothing of it would be a stream a console
			// cannot act on: this asserts the item is named where it must be,
			// which is what makes the absences above about the value.
			if !strings.Contains(out, item) {
				t.Fatalf("the command does not name the keychain item it wrote:\n%s", out)
			}
			for what, s := range secret {
				if strings.Contains(out, s) {
					t.Fatalf("stdout carries %s", what)
				}
			}
		})
	}

	// The golden is a fixture in the repository, and the reviewer's question is
	// whether a secret can reach one: the MAC key is drawn from crypto/rand by
	// a reader this suite does not pin, so nothing in the stream depends on its
	// bytes — and this is that, checked rather than assumed.
	golden, err := os.ReadFile(filepath.Join("testdata", "pair-events.golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"mac_key", "sealed", "keychain_item\":\"\""} {
		if strings.Contains(string(golden), absent) {
			t.Fatalf("the golden carries %q: %s", absent, golden)
		}
	}
}

// pairDeliveredToken opens the delivery the pairing wrote and returns the
// device token out of it: the other secret M5 covers, which leaves through the
// sealed payload and no other way.
func pairDeliveredToken(t *testing.T, f *pairFixture, uid string) string {
	t.Helper()
	var got struct {
		Device struct {
			Token string `json:"token"`
		} `json:"device"`
	}
	if err := json.Unmarshal(pairOpenDelivery(t, uid, f.pairSealedDelivery(t, uid)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Device.Token == "" {
		t.Fatal("the delivery carries no device token")
	}
	return got.Device.Token
}

// TestPairKeychainFailureDeliversNothing is the first undo (M5): a keychain
// that cannot hold the key means this pairing has no key, so nothing is
// delivered to the phone and the device node written a moment earlier comes
// back off the allowlist.
func TestPairKeychainFailureDeliversNothing(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	f.keys.Backend = keychain.ErrBackend

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1", err)
	}
	events := pairLines(t, out)
	if last := events[len(events)-1]; last.Event != pairEventRefused || last.Code != pairRefusedKeychain {
		t.Fatalf("events = %v, want one refused with %q", events, pairRefusedKeychain)
	}
	if v := f.db.Value("config/admin/devices"); v != nil {
		t.Fatalf("a keychain that could not be written left a device on the allowlist: %v", v)
	}
	f.pairNoDelivery(t, uid)
	if len(f.keys.Keys) != 0 {
		t.Fatalf("the keychain holds %v, want nothing", f.keys.Keys)
	}
	// The pairing is not over: the phone's own request node is where the phone
	// left it, and the pairing token it holds is untouched, so the operator can
	// fix the keychain and scan again while the wait lasts.
	if v := f.db.Value(pairPathRequest(uid)); v == nil {
		t.Fatal("the refusal deleted the phone's own request node")
	}
}

// TestPairDeliveryFailureRemovesTheItem is the second undo, and it is the whole
// of it: the key is in the keychain and the delivery cannot be written, so the
// item goes again — a key at an item nothing will ever read is a key that
// outlives the pairing — and the device node goes with it, because a node with
// no delivery behind it is a device that fails USER on every request while it
// sits on the allowlist (pairRemoveDeviceNode's own reason, and the same state
// a keychain failure must not leave either).
//
// The two ways the write can fail are §4.6's two rows, and the refusal code is
// what tells them apart: the rules refusing the write is exit 1 and
// database_admin_missing (the operator needs roles/firebasedatabase.admin), and
// a database that does not answer is exit 2 and database_unreachable. The undo
// is the same either way and the code is not, which is why both are here: an
// operator sent after a missing role when the database was simply down, or the
// other way round, is sent to fix the wrong thing.
func TestPairDeliveryFailureRemovesTheItem(t *testing.T) {
	cases := []struct {
		name string
		code string
		exit int
		// fail makes the delivery write fail this way, given the pairing's uid.
		fail func(t *testing.T, f *pairFixture, uid string)
	}{
		{
			name: "the rules refuse the write",
			code: pairRefusedDatabaseAdmin,
			exit: ExitUserError,
			fail: func(t *testing.T, f *pairFixture, uid string) { f.db.Deny(pairPathDelivery(uid)) },
		},
		{
			name: "the database cannot be reached",
			code: pairRefusedUnreachable,
			exit: ExitRemoteError,
			fail: func(t *testing.T, f *pairFixture, uid string) {
				budgetTransport = pairPathWriteFails{rt: http.DefaultTransport, path: "/pairing/" + uid + ".json"}
				t.Cleanup(func() { budgetTransport = nil })
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newPairFixture(t, pairMe)
			uid := pairFixedRand(t)
			f.pairSeedRequest(uid, pairKey)
			c.fail(t, f, uid)

			out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
			if err == nil || ExitCode(err) != c.exit {
				t.Fatalf("err = %v, want exit %d", err, c.exit)
			}
			events := pairLines(t, out)
			last := events[len(events)-1]
			if last.Event != pairEventRefused {
				t.Fatalf("events = %v, want one refused", events)
			}
			if last.Code != c.code {
				t.Fatalf("code = %q, want %q", last.Code, c.code)
			}
			f.pairNoDelivery(t, uid)
			if len(f.keys.Keys) != 0 {
				t.Fatalf("the keychain holds %v, want the item removed again", f.keys.Keys)
			}
			if v := f.db.Value("config/admin/devices"); v != nil {
				t.Fatalf("a delivery that could not be written left a device on the allowlist: %v", v)
			}
			// The pairing is not over: the phone's request node and the token it holds
			// are untouched, so the operator can fix what refused the write and scan
			// again while the wait lasts.
			if v := f.db.Value(pairPathRequest(uid)); v == nil {
				t.Fatal("the refusal deleted the phone's own request node")
			}
		})
	}
}

// pairPathWriteFails is the database going away for one write and answering
// every other request: the difference between "the rules refused this" and "the
// database is not there" is a difference of error class and of exit code, so a
// test that wants the second has to fail the transport rather than the rules.
type pairPathWriteFails struct {
	rt   http.RoundTripper
	path string
}

func (c pairPathWriteFails) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, c.path) {
		return nil, errors.New("the database is unreachable")
	}
	return c.rt.RoundTrip(r)
}

// TestPairDatabaseErrorIsUnreachableNotDeliveryFailed is §4.6's split at the
// delivery write: the code and the exit code must agree, and a database error
// is the unreachable row. `delivery_write_failed` says the operator's own write
// was refused and sends them looking for a fix in it, while the exit code
// already says exit 2 — so the console acts on one thing and the operator reads
// another.
func TestPairDatabaseErrorIsUnreachableNotDeliveryFailed(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	budgetTransport = pairPathWriteFails{rt: http.DefaultTransport, path: "/pairing/" + uid + ".json"}
	t.Cleanup(func() { budgetTransport = nil })

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, want exit 2: the database could not be reached", err)
	}
	events := pairLines(t, out)
	last := events[len(events)-1]
	if last.Code != pairRefusedUnreachable {
		t.Fatalf("code = %q, want %q: a database that did not answer is §4.6's exit-2 row, and %q names a local write to fix",
			last.Code, pairRefusedUnreachable, pairRefusedDelivery)
	}
	if last.Reason == "" {
		t.Fatalf("the refusal carries no reason: %v", last)
	}
}

// TestPairNodeDeleteFailureAfterPairingSaysPairedAndNamesItem is the last step
// of §7.1 failing after every step before it succeeded. The device IS paired —
// the node is written and the key is in the keychain — so the refusal must say
// so, name the keychain item the operator has to record in the local config
// (nothing else tells them what `mailbox.keychain_item` should say), and say
// that the history sweep removes the leftover node with the p~ Auth user
// (decision 2). An operator who reads this as "the pairing failed" would pair
// the phone again and leave the first device on the allowlist.
func TestPairNodeDeleteFailureAfterPairingSaysPairedAndNamesItem(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)
	budgetTransport = pairPathWriteFails{rt: http.DefaultTransport, path: "/pairing.json"}
	t.Cleanup(func() { budgetTransport = nil })

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, want exit 2", err)
	}
	events := pairLines(t, out)
	last := events[len(events)-1]
	if last.Event != pairEventRefused || last.Code != pairRefusedUnreachable {
		t.Fatalf("events = %v, want one refused with %q", events, pairRefusedUnreachable)
	}
	deviceUID := f.pairDeviceUID(t)
	for _, want := range []string{"the device is paired", "history sweep", pairItemName("aurora", deviceUID)} {
		if !strings.Contains(last.Reason, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, last.Reason)
		}
	}
	// The pairing stands: the device node is on the allowlist and the phone's
	// payload was written. Only the node the payload sits under is left behind.
	if f.db.Value("config/admin/devices/"+deviceUID) == nil {
		t.Fatal("a pairing that was completed was undone because the pairing node could not be removed")
	}
	f.pairSealedDelivery(t, uid)
	if len(f.keys.Keys) != 1 {
		t.Fatalf("the keychain holds %v, want the key of a pairing that completed", f.keys.Keys)
	}
}

// pairNoDelivery asserts nothing reached the phone: the delivery node holds no
// sealed payload, whether the write was refused by the rules or never
// attempted. Both undos also take the device node back and each caller asserts
// that; this is the half they share.
func (f *pairFixture) pairNoDelivery(t *testing.T, uid string) {
	t.Helper()
	if v := f.db.Value(pairPathDelivery(uid)); v != nil {
		t.Fatalf("a pairing that was refused wrote a delivery: %v", v)
	}
}

// TestPairAcceptsUppercaseEmail is T2b's leftover: the provider an address of
// this shape belongs to resolves the local part case-insensitively, so an
// operator who capitalised a letter of their own address typed their own
// address. The comparison is between canonical forms and refusing this one
// would send them to change an account rather than a keystroke.
func TestPairAcceptsUppercaseEmail(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a", "--email", "Person-1@Example.COM")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"email":"person-1@example.com"`) {
		t.Fatalf("the device node's email is not the operator's own canonical address:\n%s", out)
	}
	allowlist, _ := f.db.Value("config/admin/devices").(map[string]any)
	for _, child := range allowlist {
		node, _ := child.(map[string]any)
		if node["email"] != pairMe {
			t.Fatalf("the device node's email is %v, want %s", node["email"], pairMe)
		}
	}

	// An address that is not the operator's is still refused after the fold:
	// folding is not a way to pair a device under an address nobody proved.
	// The refusal comes before the wait, so this second run costs no clock.
	if _, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--email", "PERSON-2@example.com"); err == nil {
		t.Fatal("an address that is not the operator's own was accepted because it folded")
	}
}

// TestPairNonNumericAtIsMalformedNotOutage is T2b's other leftover: the rules
// validate `at` as an integer count of epoch-milliseconds, so a reply carrying
// anything else there was not written by the phone this command pairs — and it
// is a refusal of the reply, not of the database. §4.6 puts a database that
// could not be reached in the exit-2 row, and an operator sent after an outage
// for a reply nobody can read would be sent to fix the wrong thing.
func TestPairNonNumericAtIsMalformedNotOutage(t *testing.T) {
	for _, at := range []any{"1761742800000", 1.5, true, nil} {
		t.Run(fmt.Sprint(at), func(t *testing.T) {
			f := newPairFixture(t, pairMe)
			uid := pairFixedRand(t)
			f.db.Set(pairPathRequest(uid), map[string]any{"key": pairKey, "at": at})

			out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want exit 1: a malformed reply is not an outage", err)
			}
			events := pairLines(t, out)
			last := events[len(events)-1]
			if last.Event != pairEventRefused || last.Code != pairRefusedRequestMalformed {
				t.Fatalf("events = %v, want one refused with %q and not %q", events, pairRefusedRequestMalformed, pairRefusedUnreachable)
			}
			f.pairNoSideEffect(t, uid)
		})
	}
}

// ------------------------------------------------------- pair ls and revoke

const (
	pairDevA = "m~BBBBBBBBBBBBBBBBBBBBBB"
	pairDevB = "m~CCCCCCCCCCCCCCCCCCCCCC"
	pairDevC = "m~DDDDDDDDDDDDDDDDDDDDDD"
	// pairDevBare is a node with no readable child: an older or a hand-written
	// entry, which §4.2 prints as its uid alone.
	pairDevBare = "m~FFFFFFFFFFFFFFFFFFFFFF"
)

// pairSeedDevice writes a device node as a completed pairing leaves it. The
// fields are M13's four and nothing else: a node holding a token or a key would
// be the bug these commands must never produce.
func (f *pairFixture) pairSeedDevice(uid, name string) {
	f.db.Set(rules.PathDevices+"/"+uid, map[string]any{
		"name": name, "email": pairMe, "role": "operator", "at": pairT0.UTC().Format(time.RFC3339),
	})
}

// pairSeedItem stores a MAC key at the item this pairing wrote, and returns the
// item's name and its value: what `pair revoke` must delete beside the node,
// and what `pair ls` must never print.
func (f *pairFixture) pairSeedItem(t *testing.T, uid string) (string, string) {
	t.Helper()
	name := pairItemName("aurora", uid)
	item, err := keychain.ParseItem(name, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	key, err := token.NewMACKey()
	if err != nil {
		t.Fatal(err)
	}
	value := token.EncodeKey(key)
	if err := f.keys.Put(context.Background(), item, keychain.NewKey([]byte(value))); err != nil {
		t.Fatal(err)
	}
	return name, value
}

// pairKeychainSpy is the keychain with the moment of its Delete recorded. M4's
// order is the assertion, and it is not "the item was deleted" but "the item was
// deleted when the device node was already gone": the two are different
// databases, so the test has to read the first while the second is happening.
type pairKeychainSpy struct {
	keychain.Store
	onDelete func()
}

func (s *pairKeychainSpy) Delete(ctx context.Context, item keychain.Item) error {
	if s.onDelete != nil {
		s.onDelete()
	}
	return s.Store.Delete(ctx, item)
}

// TestRevokeDeletesTheNodeFirst is M4: the device node is deleted before the
// keychain item. The node is what the rules read (D166), so its deletion is the
// revocation, and the order is what a revoke that fails between the two steps
// leaves behind — a phone off the allowlist rather than a phone on it with no
// operator left holding its key. The mutation this test was written against
// deletes the item first, and it is a real difference, not a cosmetic one.
func TestRevokeDeletesTheNodeFirst(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, "phone-a")
	itemName, _ := f.pairSeedItem(t, pairDevA)

	nodeWasThere := false
	seen := false
	prev := pairKeychain
	pairKeychain = func() keychain.Store {
		return &pairKeychainSpy{Store: f.keys, onDelete: func() {
			seen = true
			nodeWasThere = f.db.Value(rules.PathDevices+"/"+pairDevA) != nil
		}}
	}
	t.Cleanup(func() { pairKeychain = prev })

	out, _, err := pairRun(t, "revoke", "--uid", pairDevA)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !seen {
		t.Fatal("the keychain item was never deleted")
	}
	if nodeWasThere {
		t.Fatal("the keychain item was deleted while the device node was still there: revoke deletes the node first (M4), so a failure between the two steps leaves a phone off the allowlist rather than one that is on it with no key left to sign with")
	}
	if v := f.db.Value(rules.PathDevices + "/" + pairDevA); v != nil {
		t.Fatalf("the device node survived the revoke: %v", v)
	}
	if _, ok := f.keys.Keys[itemName]; ok {
		t.Fatal("the keychain item survived the revoke")
	}
	if !strings.Contains(out, itemName) {
		t.Fatalf("the output does not name the item it deleted:\n%s", out)
	}
}

// TestRevokeNeverDeletesTheAuthUser is decision 1: no role a Terraform grant
// gives an operator can delete an Auth user (the history job's account is the
// only holder of roles/firebaseauth.admin), so this command does not try. What
// it does instead is say so and name the sweep, which is what removes the
// orphan — and what it did delete is the node and the item, which is the whole
// of what it may touch.
func TestRevokeNeverDeletesTheAuthUser(t *testing.T) {
	jsonFixture := newPairFixture(t, pairMe)
	jsonFixture.pairSeedDevice(pairDevA, "phone-a")
	jsonFixture.pairSeedItem(t, pairDevA)

	out, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRevoked {
		t.Fatalf("events = %v, want one %s", events, pairEventRevoked)
	}
	doc := events[0].Revoke
	if doc == nil {
		t.Fatalf("the %s event carries no document: %v", pairEventRevoked, events[0])
	}
	if want := []string{rules.PathDevices + "/" + pairDevA}; !slices.Equal(doc.Wrote, want) {
		t.Fatalf("wrote = %v, want the device node alone: the Auth user is not this command's to delete", doc.Wrote)
	}
	if len(doc.Warnings) != 0 || len(doc.Refused) != 0 {
		t.Fatalf("document = %+v, want no warnings and no refusals", doc)
	}

	textFixture := newPairFixture(t, pairMe)
	textFixture.pairSeedDevice(pairDevA, "phone-a")
	textFixture.pairSeedItem(t, pairDevA)
	text, _, err := pairRun(t, "revoke", "--uid", pairDevA)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !strings.Contains(text, "Auth user is not deleted") || !strings.Contains(text, "sweep") {
		t.Fatalf("the text output does not say the Auth user survives and that the sweep removes it:\n%s", text)
	}
}

// TestRevokeRefusesNothingOrTwo is §4.3's last sentence: a revoke that names
// nothing, or that names two devices, is refused rather than performed
// ambiguously — and a refusal leaves the allowlist and the keychain exactly
// where they were.
func TestRevokeRefusesNothingOrTwo(t *testing.T) {
	seed := func(t *testing.T) *pairFixture {
		f := newPairFixture(t, pairMe)
		f.pairSeedDevice(pairDevA, "phone-a")
		f.pairSeedDevice(pairDevB, "phone-a")
		f.pairSeedDevice(pairDevC, "phone-b")
		f.pairSeedItem(t, pairDevA)
		f.pairSeedItem(t, pairDevB)
		f.pairSeedItem(t, pairDevC)
		return f
	}
	for _, tc := range []struct {
		name string
		args []string
		code string
	}{
		{"nothing named", []string{"revoke"}, pairRefusedNoSelector},
		{"both selectors", []string{"revoke", "--uid", pairDevA, "--name", "phone-b"}, pairRefusedTwoSelectors},
		{"a uid that is no node", []string{"revoke", "--uid", "m~EEEEEEEEEEEEEEEEEEEEEE"}, pairRefusedNoPairing},
		{"a name that is no device", []string{"revoke", "--name", "phone-c"}, pairRefusedNoPairing},
		{"a name two devices share", []string{"revoke", "--name", "phone-a"}, pairRefusedTwoPairings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := seed(t)
			before := f.db.Value(rules.PathDevices)
			items := map[string]string{}
			maps.Copy(items, f.keys.Keys)

			out, _, err := pairRun(t, append(tc.args, "--json")...)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want exit 1", err)
			}
			events := pairLines(t, out)
			last := events[len(events)-1]
			if last.Event != pairEventRefused || last.Code != tc.code {
				t.Fatalf("events = %v, want one refused with %q", events, tc.code)
			}
			if !reflect.DeepEqual(before, f.db.Value(rules.PathDevices)) {
				t.Fatal("a refused revoke changed the device allowlist")
			}
			if !reflect.DeepEqual(items, f.keys.Keys) {
				t.Fatal("a refused revoke touched the keychain")
			}
		})
	}

	// The shared name is refused by class and count, not by echoing the typed
	// selector or the node's uids: `pair ls` prints the uids the operator picks
	// from, and this sentence prints neither.
	f := seed(t)
	_, _, err := pairRun(t, "revoke", "--name", "phone-a")
	if err == nil {
		t.Fatal("a name two devices share was revoked")
	}
	if !strings.Contains(err.Error(), "matched 2 devices by name") {
		t.Fatalf("the refusal %q does not name the class and the count", err)
	}

	// And the same fixture revokes the unambiguous one, by name.
	if _, _, err := pairRun(t, "revoke", "--name", "phone-b"); err != nil {
		t.Fatalf("revoke --name phone-b: %v", err)
	}
	if f.db.Value(rules.PathDevices+"/"+pairDevC) != nil {
		t.Fatal("revoke --name did not delete the one device it matched")
	}
}

// TestPairCellSanitises: nothing in the deployed document constrains a device
// node's children (no `.validate`, no `$other` under config/admin/devices), so
// a hand-written entry may hold a newline, an ANSI escape or a name longer than
// any cell should be — and both printers of a node print what the node holds.
// The cell is the one place that keeps such a name from opening a column,
// moving the cursor or burying the rest of the listing.
func TestPairCellSanitises(t *testing.T) {
	long := strings.Repeat("x", 210)
	cases := []struct{ name, in, want string }{
		{"a newline", "phone\na", `phone\u000aa`},
		{"a carriage return", "phone\ra", `phone\u000da`},
		{"a tab", "phone\ta", `phone\u0009a`},
		{"a NUL", "phone\x00a", `phone\u0000a`},
		{"an ANSI escape", "\x1b[31mphone\x1b[0m", `\u001b[31mphone\u001b[0m`},
		{"an overlong name", long, strings.Repeat("x", 200) + "... (truncated)"},
	}
	for _, c := range cases {
		if got := pairCell(c.in); got != c.want {
			t.Errorf("%s: pairCell(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestPairRevokeSanitisesDeviceName is that same cell at the other printer:
// `revoke` names the device it revoked in its text output, and the name comes
// out of the node, so it goes through pairCell — the function `pair ls` uses —
// and the two commands print one spelling of a hostile name rather than two.
func TestPairRevokeSanitisesDeviceName(t *testing.T) {
	const name = "phone-a\x1b[2K\nevil"
	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, name)
	f.pairSeedItem(t, pairDevA)

	out, _, err := pairRun(t, "revoke", "--uid", pairDevA)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !strings.Contains(out, pairCell(name)) {
		t.Fatalf("revoke does not print the device name the way pair ls prints it:\n%s", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatalf("revoke printed a raw escape out of the node:\n%q", out)
	}

	// The same name through `pair ls`: one function, one spelling.
	f.pairSeedDevice(pairDevB, name)
	ls, _, err := pairRun(t, "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(ls, pairCell(name)) {
		t.Fatalf("ls does not print the device name the way revoke prints it:\n%s", ls)
	}
}

// TestPairRevokeTwoMatchesDoesNotEchoSelector: a --name that matches more than
// one node is refused by class and count, and the refusal prints neither the
// value the operator typed nor the uids of the nodes it matched. The typed
// value is the caller's own input and the uids come out of a node no rule
// constrains, so a sentence quoting either would put a hostile string on the
// terminal — while `pair ls`, whose listing is sanitised, is the step this
// refusal is supposed to send the operator back to.
func TestPairRevokeTwoMatchesDoesNotEchoSelector(t *testing.T) {
	const name = "phone-a\x1b[2K"
	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, name)
	f.pairSeedDevice(pairDevB, name)
	f.pairSeedItem(t, pairDevA)
	f.pairSeedItem(t, pairDevB)

	for _, args := range [][]string{
		{"revoke", "--name", name},
		{"revoke", "--name", name, "--json"},
	} {
		out, _, err := pairRun(t, args...)
		if err == nil {
			t.Fatalf("%v: a name two devices share was revoked:\n%s", args, out)
		}
		said := out + err.Error()
		if !strings.Contains(said, "matched 2 devices by name") {
			t.Errorf("%v: the refusal does not name the class and the count: %v", args, err)
		}
		for _, echoed := range []string{"phone-a", pairDevA, pairDevB} {
			if strings.Contains(said, echoed) {
				t.Errorf("%v: the refusal echoes %q: %s", args, echoed, said)
			}
		}
	}
	if f.db.Value(rules.PathDevices+"/"+pairDevA) == nil || f.db.Value(rules.PathDevices+"/"+pairDevB) == nil {
		t.Fatal("a refused revoke deleted a device node")
	}
}

// TestPairLsPrintsNoSecretAndNoAuthColumn is §4.2 read as a prohibition, which
// is how the operator's contract is written: no token, no key, no keychain
// item's value, and no column saying whether the Auth user still exists
// (decision 11 — database admin cannot reach the Auth user at all). The
// item's *name* is not printed either, because it is not in the node and `ls`
// prints what the node holds.
func TestPairLsPrintsNoSecretAndNoAuthColumn(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, "phone-a")
	itemName, itemValue := f.pairSeedItem(t, pairDevA)
	f.db.Set(rules.PathDevices+"/"+pairDevBare, "not-a-node")

	out, _, err := pairRun(t, "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	at := pairT0.UTC().Format(time.RFC3339)
	for _, want := range []string{pairDevA, "phone-a", pairMe, "operator", at, pairDevBare} {
		if !strings.Contains(out, want) {
			t.Fatalf("ls does not print %q:\n%s", want, out)
		}
	}
	for _, bad := range []struct{ what, s string }{
		{"the keychain item's name", itemName},
		{"the keychain item's value", itemValue},
		{"an Auth-user column", "AUTH"},
		{"a token", "eyJ"},
	} {
		if strings.Contains(out, bad.s) {
			t.Fatalf("ls printed %s (%q):\n%s", bad.what, bad.s, out)
		}
	}
	header, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if got, want := strings.Fields(header), []string{"DEVICE", "NAME", "EMAIL", "ROLE", "PAIRED"}; !slices.Equal(got, want) {
		t.Fatalf("the header is %v, want %v: a column added here is a column the contract has to name", got, want)
	}
}

// TestPairLsJSON is §4.2 under --json: the same rows, as §4.5's document for
// the act, with warnings present and empty — and the same prohibition, since a
// console reads this and must not be handed a key.
func TestPairLsJSON(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, "phone-a")
	_, itemValue := f.pairSeedItem(t, pairDevA)
	f.pairSeedDevice(pairDevB, "phone-b")

	out, _, err := pairRun(t, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventListed {
		t.Fatalf("events = %v, want one %s", events, pairEventListed)
	}
	list := events[0].List
	if list == nil || list.Project != "aurora" || list.At == "" {
		t.Fatalf("the document is %+v, want the project and the time", list)
	}
	if list.Warnings == nil || len(list.Warnings) != 0 {
		t.Fatalf("warnings = %v, want present and empty", list.Warnings)
	}
	at := pairT0.UTC().Format(time.RFC3339)
	want := []pairDeviceRow{
		{UID: pairDevA, Name: "phone-a", Email: pairMe, Role: "operator", At: at},
		{UID: pairDevB, Name: "phone-b", Email: pairMe, Role: "operator", At: at},
	}
	if !reflect.DeepEqual(list.Devices, want) {
		t.Fatalf("devices = %+v, want %+v", list.Devices, want)
	}
	if strings.Contains(out, itemValue) {
		t.Fatalf("the stream carries the keychain item's value:\n%s", out)
	}
}

// TestPairLsEmptyInstallation is the listing every operator sees before they
// have paired anything: no device node at all. The read answers that the node
// is absent rather than failing, so the branch is a sentence and one event with
// no rows in it — and an empty array rather than null, because a console
// iterating the document should not need a case for a listing with nothing in
// it.
func TestPairLsEmptyInstallation(t *testing.T) {
	f := newPairFixture(t, pairMe)
	if f.db.Value(rules.PathDevices) != nil {
		t.Fatal("the fixture has a device node: this test is about an installation with none")
	}

	out, _, err := pairRun(t, "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if want := "no device is paired with aurora\n"; out != want {
		t.Fatalf("ls printed %q, want %q", out, want)
	}

	asJSON, _, err := pairRun(t, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	events := pairLines(t, asJSON)
	if len(events) != 1 || events[0].Event != pairEventListed {
		t.Fatalf("events = %v, want one %s", events, pairEventListed)
	}
	if list := events[0].List; list == nil || list.Devices == nil || len(list.Devices) != 0 {
		t.Fatalf("the document is %+v, want no devices, present and empty", events[0].List)
	}
	if !strings.Contains(asJSON, `"devices":[]`) {
		t.Fatalf("the stream wrote %s, want an empty array rather than null", asJSON)
	}
}

// TestPairQRDrawsAScannableCode holds the drawing to the encoder, not to a
// screenshot: the text renderPairQR writes is read back into a module grid and
// compared with the grid rsc.io/qr encodes the payload to, which is the thing a
// phone reads. A renderer that transposed the code, dropped a row or drew the
// inverse would pass a test that only looked for block characters.
func TestPairQRDrawsAScannableCode(t *testing.T) {
	const payload = `{"v":1,"project":"aurora","uid":"p~AAAA","token":"eyJhbGciOiJIUzI1NiJ9.e30.x"}`
	var buf bytes.Buffer
	if err := renderPairQR(&buf, payload); err != nil {
		t.Fatal(err)
	}
	code, err := qr.Encode(payload, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pairReadQR(buf.String(), pairQRQuiet, code.Size)
	if err != nil {
		t.Fatal(err)
	}
	for y := range code.Size {
		for x := range code.Size {
			if got.Black(x, y) != code.Black(x, y) {
				t.Fatalf("module (%d,%d) is drawn %v, the encoder's code has %v", x, y, got.Black(x, y), code.Black(x, y))
			}
		}
	}
	// The negative control: a different payload encodes to a different grid, so
	// the comparison above is not one a constant drawing could pass.
	other, err := qr.Encode(payload+" ", qr.L)
	if err != nil {
		t.Fatal(err)
	}
	same := other.Size == code.Size
	for y := 0; same && y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if other.Black(x, y) != code.Black(x, y) {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatal("two payloads encode to one grid: the comparison above would pass for any drawing of that size")
	}
}

// TestPairQRTooWidePrintsThePayload is the case the real command is in: the
// payload is a bootstrap configuration and a JWT, so its code is over a hundred
// columns wide and wraps in the terminal this is normally run in — and a
// wrapped QR code is not a QR code. What is written then is the payload line
// and the width the code needs, which is what this command printed before it
// could encode at all; a drawing is never printed narrower than the code, and
// never printed past the terminal that would wrap it.
func TestPairQRTooWidePrintsThePayload(t *testing.T) {
	payload := `{"v":1,"project":"aurora","gcp_project":"proj-1234",` +
		`"database_url":"https://aurora-default-rtdb.europe-west1.firebasedatabase.app",` +
		`"api_key":"` + pairAPIKey + `","uid":"p~AAAA","expires_at":"2026-10-29T12:02:00Z",` +
		`"token":"eyJhbGciOiJSUzI1NiJ9.` + strings.Repeat("B", 400) + `.` + strings.Repeat("A", 342) + `"}`
	code, err := qr.Encode(payload, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	columns := code.Size + 2*pairQRQuiet
	if columns <= pairQRMaxColumns {
		t.Fatalf("this payload's code is %d columns, inside the %d limit: it is not the case this test is about", columns, pairQRMaxColumns)
	}

	var buf bytes.Buffer
	if err := renderPairQR(&buf, payload); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(buf.String(), "█▀▄") {
		t.Fatalf("a code too wide for the terminal was drawn anyway:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), payload) {
		t.Fatalf("the payload line is not in the output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), strconv.Itoa(columns)) {
		t.Fatalf("the output does not say the terminal must be %d columns wide:\n%s", columns, buf.String())
	}
}

// TestPairQRWideTerminalDrawsTheRealPayload is the other side of the width
// gate: a payload the size of a real one is drawn, module for module, in a
// terminal wide enough for it, so the gate is the terminal's width and not a
// constant that no real payload fits under.
func TestPairQRWideTerminalDrawsTheRealPayload(t *testing.T) {
	payload := `{"v":1,"project":"aurora","gcp_project":"proj-1234",` +
		`"database_url":"https://aurora-default-rtdb.europe-west1.firebasedatabase.app",` +
		`"api_key":"` + pairAPIKey + `","uid":"p~AAAA","expires_at":"2026-10-29T12:02:00Z",` +
		`"token":"eyJhbGciOiJSUzI1NiJ9.` + strings.Repeat("B", 400) + `.` + strings.Repeat("A", 342) + `"}`
	code, err := qr.Encode(payload, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	columns := code.Size + 2*pairQRQuiet
	if columns <= pairQRMaxColumns {
		t.Fatalf("this payload's code is %d columns, inside the default %d: it is not the case this test is about", columns, pairQRMaxColumns)
	}
	prev := pairTerminalColumns
	pairTerminalColumns = func(io.Writer) int { return columns }
	t.Cleanup(func() { pairTerminalColumns = prev })

	var buf bytes.Buffer
	if err := renderPairQR(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := pairReadQR(buf.String(), pairQRQuiet, code.Size)
	if err != nil {
		t.Fatalf("a terminal %d columns wide did not get the drawing: %v\n%s", columns, err, buf.String())
	}
	for y := range code.Size {
		for x := range code.Size {
			if got.Black(x, y) != code.Black(x, y) {
				t.Fatalf("module (%d,%d) is drawn %v, the encoder's code has %v", x, y, got.Black(x, y), code.Black(x, y))
			}
		}
	}

	pairTerminalColumns = func(io.Writer) int { return columns - 1 }
	buf.Reset()
	if err := renderPairQR(&buf, payload); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(buf.String(), "█▀▄") {
		t.Fatalf("a terminal one column narrower than the code got the drawing:\n%s", buf.String())
	}
}

// TestPairQRJSONDrawsNothing is §4.5's third rule for the QR: under --json the
// payload goes in the qr event and the terminal drawing is not written at all,
// so a console reading stdout is not also handed a screenful of blocks.
func TestPairQRJSONDrawsNothing(t *testing.T) {
	f := newPairFixture(t, pairMe)
	uid := pairFixedRand(t)
	f.pairSeedRequest(uid, pairKey)

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "█▀▄") {
		t.Fatalf("--json drew the QR in the terminal:\n%s", out)
	}
	events := pairLines(t, out)
	if events[0].Event != pairEventQR || events[0].Payload == "" {
		t.Fatalf("the first event is %+v, want the payload in a qr event", events[0])
	}
}

// pairReadQR reads renderPairQR's half blocks back into a module grid of n
// modules on a side: U+2588 is two dark modules, U+2580 and U+2584 the top and
// the bottom one, and a space two light ones. It is the inverse of the
// renderer, written out here rather than shared with it, because a reader that
// reused the renderer's own code would agree with it about a transposed row. It
// refuses what the renderer must not write: a cell that is not one of the four
// half blocks, a row of the wrong width, and a dark module outside the code's
// own square, which is what the quiet zone's light modules are for.
func pairReadQR(drawn string, quiet, n int) (pairModuleGrid, error) {
	rows := strings.Split(strings.TrimSuffix(drawn, "\n"), "\n")
	if len(rows) == 0 {
		return pairModuleGrid{}, errors.New("the drawing is empty")
	}
	width := n + 2*quiet
	for i, r := range rows {
		if len([]rune(r)) != width {
			return pairModuleGrid{}, fmt.Errorf("row %d is %d cells wide, want %d (the code's %d modules and %d of quiet zone on each side)", i, len([]rune(r)), width, n, quiet)
		}
	}
	// The renderer pairs module rows two at a time, so a code of an odd height
	// draws one module row more than it needs and the last line's bottom half
	// is quiet.
	if h := len(rows)*2 - 2*quiet; h != n && h != n+1 {
		return pairModuleGrid{}, fmt.Errorf("the drawing is %d modules tall, want the code's %d (or one more)", h, n)
	}
	grid := pairModuleGrid{Size: n}
	dark := map[rune][2]bool{
		'█': {true, true},
		'▀': {true, false},
		'▄': {false, true},
		' ': {false, false},
	}
	for i, row := range rows {
		for j, cell := range []rune(row) {
			cellDark, ok := dark[cell]
			if !ok {
				return grid, fmt.Errorf("row %d cell %d is %q, not one of the four the renderer writes", i, j, cell)
			}
			for k, on := range cellDark {
				if !on {
					continue
				}
				x, y := j-quiet, i*2-quiet+k
				if x < 0 || y < 0 || x >= n || y >= n {
					return grid, fmt.Errorf("module (%d,%d) is dark, outside the code's %dx%d square: the quiet zone is light", x, y, n, n)
				}
				grid.set(x, y, true)
			}
		}
	}
	return grid, nil
}

// pairModuleGrid is a decoded drawing: the modules, with the quiet zone already
// removed.
type pairModuleGrid struct {
	Size  int
	drawn map[[2]int]bool
}

func (g *pairModuleGrid) set(x, y int, dark bool) {
	if x < 0 || y < 0 || x >= g.Size || y >= g.Size {
		return
	}
	if g.drawn == nil {
		g.drawn = map[[2]int]bool{}
	}
	g.drawn[[2]int{x, y}] = dark
}

func (g pairModuleGrid) Black(x, y int) bool { return g.drawn[[2]int{x, y}] }

// pairSchema compiles schemas/pair-event.schema.json, the way the schema
// package's own tests do: under its own $id, so a $ref inside it resolves.
func pairSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "pair-event.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("the pair event schema has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// TestPairEventsMatchTheSchema holds the stream's schema to the stream. The
// golden carries the five events a pairing session produces; the listing and
// the revocation are produced here, so every event name the schema declares is
// checked against a line the command actually wrote rather than against one
// this test composed.
func TestPairEventsMatchTheSchema(t *testing.T) {
	sch := pairSchema(t)

	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, "phone-a")
	f.pairSeedItem(t, pairDevA)
	listed, _, err := pairRun(t, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	revoked, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json")
	if err != nil {
		t.Fatalf("revoke --json: %v", err)
	}

	golden, err := os.ReadFile(filepath.Join("testdata", "pair-events.golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := append(pairEventLines(t, string(golden)), pairEventLines(t, listed)...)
	lines = append(lines, pairEventLines(t, revoked)...)

	names := map[string]bool{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		v, err := jsonschema.UnmarshalJSON(strings.NewReader(line))
		if err != nil {
			t.Fatalf("event %q does not parse: %v", line, err)
		}
		if err := sch.Validate(v); err != nil {
			t.Fatalf("event %q does not match schemas/pair-event.schema.json: %v", line, err)
		}
		var e pairEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		names[e.Event] = true
	}
	for _, want := range []string{pairEventQR, pairEventWaiting, pairEventCode, pairEventDone, pairEventRefused, pairEventListed, pairEventRevoked} {
		if !names[want] {
			t.Fatalf("no %s event was checked against the schema: %v", want, names)
		}
	}

	// A uid the command did not write is still a row it prints: the lister
	// lists every child of the allowlist, whose keys are unconstrained, so the
	// published schema must admit what the command emits rather than the shape
	// `fugaro pair` happens to write. A console validating the stream against a
	// stricter pattern would refuse a listing the command legitimately printed.
	handWritten := newPairFixture(t, pairMe)
	handWritten.db.Set(rules.PathDevices+"/hand-written-entry", map[string]any{"name": "phone-a"})
	handOut, _, err := pairRun(t, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	if !strings.Contains(handOut, "hand-written-entry") {
		t.Fatalf("the listing dropped the hand-written child, so this check is empty:\n%s", handOut)
	}
	for _, line := range pairEventLines(t, handOut) {
		v, err := jsonschema.UnmarshalJSON(strings.NewReader(line))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(v); err != nil {
			t.Fatalf("pair ls --json emitted a document the published schema refuses: %v", err)
		}
	}

	// And the schema is strict about the field a console must never be handed:
	// a document carrying a key is refused.
	bad := `{"v":1,"event":"listed","at":"2026-10-29T12:00:00Z","project":"aurora","list":{"project":"aurora","at":"2026-10-29T12:00:00Z","devices":[{"uid":"m~B","name":"phone-a","email":"person-1@example.com","role":"operator","at":"2026-10-29T12:00:00Z","key":"AAAA"}],"warnings":[]}}`
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(v); err == nil {
		t.Fatal("the schema accepts a device row carrying a key: a console reads this stream and must never be handed one")
	}
}
