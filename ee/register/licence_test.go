// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The tests of `fugaro licence set`, `status` and `refresh` (plan K3.2).
//
// They drive the tagged tree — fcli.NewRootCmd() with this package's
// registration in it — over a fixture that is hermetic in the repository's
// sense: a file:// runs bucket in a temp directory, gcpfake's Run, Logging and
// Secret Manager servers, the CLI's config directory under the same temp
// directory, and a project marker naming the project. Nothing here reaches a
// network, a home directory or a cloud.
//
// **Every key is minted in the process that reads it.** A fixture holding a
// private half would be key material in a repository whatever it is called
// (.claude/rules/ee.md), so the issuer below generates an Ed25519 pair per test
// and the private half never leaves memory; the ring the commands verify with
// is that pair's public half. A renewal secret is generated the same way, so no
// secret-shaped literal is written anywhere.
//
// The three properties the command owes are what the tests are shaped around,
// and each one has a test that fails if it is dropped:
//
//   - the key never reaches argv, an environment variable or a line of output
//     (TestLicenceSetRefusesAKeyInArgv, TestLicenceSetNeverPrintsTheKey);
//   - every write is refused inside a coding agent's session, and `status` —
//     a read — is not (TestLicenceWritesAreRefusedInAnAgentSession);
//   - `status` never answers Free for a refusal, and the licence belongs to the
//     installation rather than to one machine
//     (TestLicenceStatusNeverReadsARefusalAsFree,
//     TestLicenceStatusFallsBackToTheMachinesKey).

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/project"
	"github.com/dimipaun/fugaro/internal/agentenv"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// The fixture's installation: a Fugaro project of its own, its GCP project, and
// the Firebase project its keys are bound to (TN2). None of the three is a real
// one; the verifier compares two strings and the fixture supplies both.
const (
	licenceTestProject  = "aurora"
	licenceTestGCP      = "proj-1234"
	licenceTestFirebase = "aurora-fp"
)

// licenceTestKeyPrefix is the v1 key's own prefix, written out because the
// constant is unexported (ee/licence/keyv1.go): it is a fact of the format this
// test mints, not a detail of the verifier.
const licenceTestKeyPrefix = "fgk_"

// licenceTestObjectPath is where the project store's object lands in the
// file:// bucket the fixture opens.
var licenceTestObjectPath = filepath.FromSlash(project.Object)

// The two keyring entry names the tests mint under: the fixture's own, and one
// no ring in this package holds, which is how a test gets a key that does not
// verify. Neither is a production id, and the production keyring is empty.
const (
	licenceTestKeyID   = "licence-register-test-v1"
	licenceStrangerKey = "licence-register-test-stranger-v1"
)

// licenceFixture is one installation: its config, its bucket, its fakes and the
// keypair its keys are signed under.
type licenceFixture struct {
	dir     string
	xdg     string
	bucket  string
	config  string
	run     *gcpfake.Run
	logging *gcpfake.Logging
	secrets *gcpfake.Secrets
	issuer  *licenceIssuer
	now     time.Time
}

// newLicenceFixture builds the installation and points the package's three
// seams at it: the ring, the clock and the licence server's address. The
// defaults are the ones a test that does not install a server gets — a ring of
// this test's own key and licenceServerURL an address nothing listens on, so a
// call that should not happen fails instead of reaching a real host.
func newLicenceFixture(t *testing.T) *licenceFixture {
	t.Helper()
	// The run this test is itself in carries a coding agent's markers, and the
	// refusal they arm is a fact the agent test sets and no other test wants.
	for _, marker := range agentenv.Markers {
		t.Setenv(marker, "")
	}
	// The tests run inside this repository's own checkout, whose fugaro.yaml
	// names a project of its own, and project selection prefers the checkout
	// over $FUGARO_CONFIG — deliberately, since a config file may not overrule
	// the checkout a command runs in. A fixture outside a checkout is what
	// makes the project config below the one the command reads.
	t.Chdir(t.TempDir())
	f := &licenceFixture{
		dir:     t.TempDir(),
		now:     time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
		run:     gcpfake.NewRun(t),
		logging: gcpfake.NewLogging(t),
		secrets: gcpfake.NewSecrets(t),
	}
	f.run.Project, f.run.Region = licenceTestGCP, "us-east5"
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	f.bucket = "file://" + runs
	// The CLI's config directory is this test's own, so the licence key file
	// and the project config are read from nowhere a developer's own install
	// could reach.
	f.xdg = filepath.Join(f.dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", f.xdg)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(f.dir, "cache"))
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_CONFIG", "")
	t.Setenv(licence.EnvironmentKey, "")
	projects := filepath.Join(f.xdg, "fugaro", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	f.config = filepath.Join(projects, licenceTestProject+".yaml")
	cfg := "version: 1\nname: " + licenceTestProject + "\ngcp_project: " + licenceTestGCP + "\nregion: us-east5\n" +
		"runs_bucket: unused-bucket\nbucket_url: " + f.bucket + "\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + f.run.URL + "/, logging: " + f.logging.URL + "/, secret_manager: " + f.secrets.URL + "/, no_auth: true }\n" +
		"budget: { firebase_project: " + licenceTestFirebase + " }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	if err := os.WriteFile(f.config, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", f.config)
	f.writeMarker(t)

	f.issuer = newLicenceIssuer(t, licenceTestKeyID)
	ring, now, server := licenceRing, licenceNow, licenceServerURL
	licenceRing = f.issuer.ring
	licenceNow = func() time.Time { return f.now }
	licenceServerURL = "http://127.0.0.1:1"
	t.Cleanup(func() { licenceRing, licenceNow, licenceServerURL = ring, now, server })
	return f
}

// writeMarker gives the installation the project marker every cloud command
// checks before it writes anything.
func (f *licenceFixture) writeMarker(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: licenceTestProject, GCPProject: licenceTestGCP})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.dir, "runs", filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// run executes the tagged tree with a standard input and captures both streams.
func (f *licenceFixture) exec(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := fcli.NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(fcli.NormalizeArgs(args))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// secretContainer is the Secret Manager container the installation's renewal
// secret lives in, derived exactly as the command derives it.
func (f *licenceFixture) secretContainer() string { return licenceRenewalSecretID(licenceTestProject) }

// seedSecretContainer makes the container exist, with one version when value is
// not nil and with none when it is: the state `fugaro update`'s Terraform
// leaves behind before any key is set.
func (f *licenceFixture) seedSecretContainer(value []byte) {
	f.secrets.Seed(f.secretContainer(), nil, value)
}

// renewServer is a stand-in for the licence server's renewal route: it records
// what it was called with and answers what the test tells it to.
type renewServer struct {
	ts    *httptest.Server
	mu    sync.Mutex
	calls []keyservice.RenewalCall
	reply func(keyservice.RenewalCall) (int, string)
}

// serveLicenceServer points the command's licence server at a fake route and
// answers every call with reply (a 200 `{"status":"ok"}` where reply is nil).
func (f *licenceFixture) serveLicenceServer(t *testing.T, reply func(keyservice.RenewalCall) (int, string)) *renewServer {
	t.Helper()
	s := &renewServer{reply: reply}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != keyservice.RouteRenew {
			t.Errorf("the licence server was called %s %s, want POST %s", r.Method, r.URL.Path, keyservice.RouteRenew)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("the renewal was sent as Content-Type %q, want application/json", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the renewal's body: %v", err)
		}
		var call keyservice.RenewalCall
		if err := json.Unmarshal(body, &call); err != nil {
			t.Errorf("the renewal's body is not a RenewalCall: %v", err)
		}
		s.mu.Lock()
		s.calls = append(s.calls, call)
		s.mu.Unlock()
		status, answer := http.StatusOK, `{"status":"ok"}`
		if s.reply != nil {
			status, answer = s.reply(call)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(s.ts.Close)
	licenceServerURL = s.ts.URL
	return s
}

func (s *renewServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *renewServer) call(t *testing.T, i int) keyservice.RenewalCall {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.calls) {
		t.Fatalf("the licence server was called %d times, and call %d was asked for", len(s.calls), i)
	}
	return s.calls[i]
}

// licenceIssuer mints v1 keys for one test: an Ed25519 pair generated in this
// process, and the ring that verifies what it signs.
type licenceIssuer struct {
	kid  string
	ring licence.Keyring
	priv ed25519.PrivateKey
}

func newLicenceIssuer(t *testing.T, kid string) *licenceIssuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &licenceIssuer{kid: kid, ring: licence.Keyring{kid: pub}, priv: priv}
}

// mint renders a claim set as the key text the format writes: the prefix, the
// payload and the signature over exactly those payload bytes, both unpadded
// base64url.
func (i *licenceIssuer) mint(t *testing.T, claims licence.ClaimsV1) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claim set: %v", err)
	}
	return licenceTestKeyPrefix +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(i.priv, payload))
}

// claims is the claim set the fixture's keys carry: a paid Team key of five
// seats, bound to this installation's Firebase project, renewing itself.
func (f *licenceFixture) claims(edit ...func(*licence.ClaimsV1)) licence.ClaimsV1 {
	interval := "P1D"
	c := licence.ClaimsV1{
		V:                 1, // the one policy version this Fugaro reads
		KID:               f.issuer.kid,
		KeyID:             "lk_test_k32_0001",
		WorkspaceID:       licenceTestFirebase,
		Organisation:      "example.com",
		Tier:              licence.TierTeam,
		Seats:             5,
		IssuedAt:          f.now,
		PaidThrough:       f.now.Add(300 * 24 * time.Hour),
		GraceDays:         30,
		RenewalMode:       licence.RenewalAuto,
		RenewBeforeDays:   30,
		CheckInterval:     &interval,
		RenewalSecretHash: "a-hash-of-the-renewal-secret-and-not-the-secret",
		Warnings:          []licence.WarningV1{{Roles: []string{"admin"}, From: "grace_start"}},
		Kind:              licence.KindPaid,
		Note:              "a key this test minted",
	}
	for _, e := range edit {
		e(&c)
	}
	return c
}

// secret generates the renewal secret of one test: 32 random bytes, base64url,
// so that no test carries a secret-shaped literal.
func licenceTestSecret(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// objectShape is the project store's object as these tests read it back: the
// bytes the store wrote, decoded here rather than through the store, so that a
// write and a read of it are two different readers.
type objectShape struct {
	V       int            `json:"v"`
	Keys    []string       `json:"keys"`
	Check   *project.Check `json:"last_check"`
	Revoked []string       `json:"revoked_key_ids"`
	// Present says the bucket holds an object at all, and Raw is the bytes it
	// holds: a test that asserts a write changed nothing compares the bytes,
	// so a rewrite of the same key is still a write.
	Present bool            `json:"-"`
	Raw     json.RawMessage `json:"-"`
}

// object answers the project store's object as the bucket holds it, and whether
// there is one at all.
func (f *licenceFixture) object(t *testing.T) objectShape {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "runs", licenceTestObjectPath))
	if os.IsNotExist(err) {
		return objectShape{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var o objectShape
	if err := json.Unmarshal(data, &o); err != nil {
		t.Fatalf("the store's object does not decode: %v", err)
	}
	o.Present, o.Raw = true, data
	return o
}

// store is the project's own store over the fixture's bucket: what a test uses
// to write a revocation check the way the renewal job would.
func (f *licenceFixture) store(t *testing.T) *project.Store {
	t.Helper()
	s, err := project.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestLicenceRenewalSecretIDIsTheInstallationsContainer pins the derived name of
// the Secret Manager container `set` writes into and `refresh` reads.
//
// The literal is the coupling between this task and K4.2: Terraform creates that
// container, and a rename on either side — this file's logical name, or
// gcp.ProjectSecretID's derivation — would leave the command writing into a
// container nobody reads. A value pinned here is a rename that fails a test
// rather than a renewal that quietly stops working.
func TestLicenceRenewalSecretIDIsTheInstallationsContainer(t *testing.T) {
	if got, want := licenceRenewalSecretID(licenceTestProject), "fugaro-project-aurora-licence-renewal-0e382f732fd21bbd"; got != want {
		t.Errorf("the renewal secret container of %s is %q, want %q: K4.2's Terraform creates that container, and a derived name that moved is a write into one nobody reads", licenceTestProject, got, want)
	}
}

// TestLicenceSetWritesTheProjectAndTheSecret is the happy path: a key read from
// standard input with the renewal secret on line 2 lands in the project's own
// object, the secret lands in the installation's container, and the answer
// carries the figures a person checks.
func TestLicenceSetWritesTheProjectAndTheSecret(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims()
	key, secret := f.issuer.mint(t, claims), licenceTestSecret(t)
	f.seedSecretContainer(nil)

	out, errOut, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set")
	if err != nil {
		t.Fatalf("licence set = %v (stdout %q, stderr %q)", err, out, errOut)
	}
	obj := f.object(t)
	if !obj.Present || len(obj.Keys) != 1 || obj.Keys[0] != key {
		t.Errorf("the project's object holds %+v, want the one key that was set", obj.Keys)
	}
	if got := f.secrets.Latest(f.secretContainer()); string(got) != secret {
		t.Errorf("the renewal secret container holds %q, want the secret that came with the key", got)
	}
	for _, want := range []string{licence.TierTeam, "paid through", string(licence.Active), licence.RenewalAuto, "renewal secret written"} {
		if !strings.Contains(out, want) {
			t.Errorf("licence set printed %q, which does not name %q", out, want)
		}
	}
	if !strings.Contains(out, project.Object) {
		t.Errorf("licence set printed %q, which does not say where the key was written", out)
	}
}

// TestLicenceSetNeverPrintsTheKey holds the one thing `set` may never do: a
// person's terminal, their scrollback and a pasted ticket are all places the
// key must not reach. Both streams and the refusal are read for the key's text
// and for the base64 of it, which is the other spelling a store echoes.
func TestLicenceSetNeverPrintsTheKey(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret := f.issuer.mint(t, f.claims()), licenceTestSecret(t)
	f.seedSecretContainer(nil)
	f.secrets.AllowAccess()

	out, errOut, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set")
	if err != nil {
		t.Fatalf("licence set = %v", err)
	}
	if strings.Contains(out, key) || strings.Contains(errOut, key) {
		t.Fatalf("licence set printed the key:\n%s%s", out, errOut)
	}
	if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
		t.Fatalf("licence set printed the renewal secret:\n%s%s", out, errOut)
	}

	// The refusal a refused key gets is the other line that could carry one
	// back: a verifier's text is a sentence a person pastes into a ticket, and
	// it is built from a payload nobody has verified.
	stranger := newLicenceIssuer(t, licenceStrangerKey)
	other := stranger.mint(t, f.claims(func(c *licence.ClaimsV1) { c.KID = licenceStrangerKey }))
	out, errOut, err = f.exec(t, other+"\n", "licence", "set")
	if err == nil {
		t.Fatal("a key no ring in this build holds was installed")
	}
	for _, text := range []string{out, errOut, err.Error()} {
		if strings.Contains(text, other) {
			t.Errorf("the refusal repeats the key: %q", text)
		}
	}
}

// TestLicenceSetRefusesAKeyInArgv holds F5 and F41 at the one position a person
// pastes a key into. The refusal is the command's own fixed sentence, so the
// argument is never quoted back — cobra's own answer to an unexpected argument
// would be.
func TestLicenceSetRefusesAKeyInArgv(t *testing.T) {
	f := newLicenceFixture(t)
	key := f.issuer.mint(t, f.claims())
	f.seedSecretContainer(nil)

	for _, args := range [][]string{
		{"licence", "set", key},
		{"licence", key},
		{"licence", "status", key},
	} {
		out, errOut, err := f.exec(t, "", args...)
		if err == nil {
			t.Fatalf("%v was accepted with the key in argv", args)
		}
		if code := fcli.ExitCode(err); code != fcli.ExitUserError {
			t.Errorf("%v exited %d, want %d: %v", args, code, fcli.ExitUserError, err)
		}
		for _, text := range []string{out, errOut, err.Error()} {
			if strings.Contains(text, key) {
				t.Errorf("%v repeated the key in %q", args, text)
			}
		}
		if out != "" {
			t.Errorf("%v wrote %q to stdout, want nothing", args, out)
		}
	}
	if !strings.Contains(mustRun(t, f, "licence", "set", key), "never from an argument") {
		t.Error("licence set answers a key in argv with something other than the fixed sentence that says where the key does go")
	}
	if f.object(t).Present {
		t.Error("a key in argv was written: the position is refused before anything is read")
	}
}

// TestLicenceSetReadsTheBundleFromAFile drives the other route a bundle arrives
// by: the file an issue path writes, holding the key on line 1 and its renewal
// secret on line 2. Both halves land, and neither the bundle nor the path is
// printed — the value of --file is an argument, and this command echoes no
// argument position (F5).
func TestLicenceSetReadsTheBundleFromAFile(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret := f.issuer.mint(t, f.claims()), licenceTestSecret(t)
	f.seedSecretContainer(nil)
	path := filepath.Join(t.TempDir(), "licence.key")
	if err := os.WriteFile(path, []byte(key+"\n"+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := f.exec(t, "", "licence", "set", "--file", path)
	if err != nil {
		t.Fatalf("licence set --file = %v (stdout %q, stderr %q)", err, out, errOut)
	}
	obj := f.object(t)
	if !obj.Present || len(obj.Keys) != 1 || obj.Keys[0] != key {
		t.Errorf("the project's object holds %+v, want the one key the file held", obj.Keys)
	}
	if got := f.secrets.Latest(f.secretContainer()); string(got) != secret {
		t.Errorf("the renewal secret container holds %q, want the secret the file held on line 2", got)
	}
	for _, text := range []string{out, errOut} {
		if strings.Contains(text, key) || strings.Contains(text, secret) {
			t.Errorf("licence set --file printed the bundle: %q", text)
		}
		if strings.Contains(text, path) {
			t.Errorf("licence set --file printed %q, and the value of --file is an argument position this command does not echo: %q", path, text)
		}
	}

	// The same file through a path that is not there is the refusal a mistyped
	// path gets, and it names the reason rather than the path.
	missing := filepath.Join(t.TempDir(), "not-there.key")
	msg := mustRun(t, f, "licence", "set", "--file", missing)
	if !strings.Contains(msg, "no such file or directory") {
		t.Errorf("a path that is not there was refused with %q, which does not say why", msg)
	}
	if strings.Contains(msg, missing) {
		t.Errorf("the refusal repeated the path: %q", msg)
	}
}

// TestLicenceSetRefusesAKeyPastedIntoTheFileFlag is the --file position read the
// way argv is, because it is argv: a person who pastes the key where the path
// belongs gets a refusal that says why the file could not be opened and never
// the value it was given — which os.Open's own error carries verbatim, and which
// is the key itself here.
func TestLicenceSetRefusesAKeyPastedIntoTheFileFlag(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret := f.issuer.mint(t, f.claims()), licenceTestSecret(t)
	f.seedSecretContainer(nil)

	for _, pasted := range []string{key, key + "\n" + secret} {
		out, errOut, err := f.exec(t, "", "licence", "set", "--file", pasted)
		if err == nil {
			t.Fatal("a key pasted into --file was accepted as a path")
		}
		if code := fcli.ExitCode(err); code != fcli.ExitUserError {
			t.Errorf("a key pasted into --file exited %d, want %d: %v", code, fcli.ExitUserError, err)
		}
		if !strings.Contains(err.Error(), "not echoed") || !strings.Contains(err.Error(), "--file") {
			t.Errorf("a key pasted into --file was refused with %q, which does not say where the key does go", err.Error())
		}
		for _, text := range []string{out, errOut, err.Error()} {
			if strings.Contains(text, key) || strings.Contains(text, secret) {
				t.Errorf("the refusal repeated the pasted value: %q", text)
			}
		}
		if out != "" {
			t.Errorf("a refused --file wrote %q to stdout, want nothing", out)
		}
	}
	if f.object(t).Present {
		t.Error("a key pasted into --file was written")
	}
	if f.secrets.Latest(f.secretContainer()) != nil {
		t.Error("a key pasted into --file put a version in the renewal secret container")
	}
}

// TestLicenceReadBundleRefusals holds the three ways a bundle can be wrong —
// nothing read, more lines than a key and its secret, more bytes than the cap —
// on both routes a bundle arrives by, and what every one of them owes: nothing
// is written, and no line of what was read is repeated (the count of lines is
// what is wrong, and a line is a key).
func TestLicenceReadBundleRefusals(t *testing.T) {
	f := newLicenceFixture(t)
	f.seedSecretContainer(nil)
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	over := strings.Repeat("k", licenceMaxBundleBytes+1)
	empty, three, oversized := write("empty.key", ""), write("three.key", "one\ntwo\nthree\n"), write("over.key", over)
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		path  string
		want  []string
	}{
		{name: "nothing on stdin", args: []string{"licence", "set"}, want: []string{"no key was read from standard input"}},
		{name: "three lines on stdin", args: []string{"licence", "set"}, stdin: "one\ntwo\nthree\n", want: []string{"standard input holds 3 lines"}},
		{name: "over the cap on stdin", args: []string{"licence", "set"}, stdin: over, want: []string{"over 8192 bytes"}},
		{name: "an empty file", args: []string{"licence", "set", "--file", empty}, path: empty, want: []string{"no key was read from --file"}},
		{name: "three lines in a file", args: []string{"licence", "set", "--file", three}, path: three, want: []string{"--file holds 3 lines"}},
		{name: "a file over the cap", args: []string{"licence", "set", "--file", oversized}, path: oversized, want: []string{"over 8192 bytes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := f.exec(t, tc.stdin, tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted", tc.args)
			}
			if code := fcli.ExitCode(err); code != fcli.ExitUserError {
				t.Errorf("%v exited %d, want %d: %v", tc.args, code, fcli.ExitUserError, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v was refused with %q, which does not say %q", tc.args, err.Error(), want)
				}
			}
			for _, text := range []string{out, errOut, err.Error()} {
				if strings.Contains(text, "kkkk") {
					t.Errorf("the refusal repeated what it read: %q", text)
				}
				if tc.path != "" && strings.Contains(text, tc.path) {
					t.Errorf("the refusal repeated the value of --file: %q", text)
				}
			}
			if out != "" {
				t.Errorf("%v wrote %q to stdout, want nothing", tc.args, out)
			}
		})
	}
	if f.object(t).Present {
		t.Error("a refused bundle was written")
	}
	if f.secrets.Latest(f.secretContainer()) != nil {
		t.Error("a refused bundle put a version in the renewal secret container")
	}
}

// TestLicenceSetNamesAHalfWrittenPair drives the one failure the two writes can
// leave behind: the renewal secret lands and the project's key does not. The
// refusal has to say which half is missing and what writes the pair again —
// a person told only that a write failed would refresh into a 403 that outlasts
// the failure — and it may not repeat the bundle on the way out.
//
// The bucket is blocked rather than the store: a directory stands where the
// project's object goes, which is a write nothing can complete.
func TestLicenceSetNamesAHalfWrittenPair(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret := f.issuer.mint(t, f.claims()), licenceTestSecret(t)
	f.seedSecretContainer(nil)
	if err := os.MkdirAll(filepath.Join(f.dir, "runs", licenceTestObjectPath), 0o755); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set")
	if err == nil {
		t.Fatal("licence set wrote a key the bucket refused")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("a half-written pair exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	for _, want := range []string{"not a pair yet", "fugaro licence set", f.secretContainer()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err.Error(), want)
		}
	}
	if got := f.secrets.Latest(f.secretContainer()); string(got) != secret {
		t.Errorf("the renewal secret container holds %q, want the secret that landed before the key was attempted: the refusal describes a state, and the state has to be the one it describes", got)
	}
	for _, text := range []string{out, errOut, err.Error()} {
		if strings.Contains(text, key) || strings.Contains(text, secret) {
			t.Errorf("the refusal repeated the bundle: %q", text)
		}
	}
	if out != "" {
		t.Errorf("a refused write printed %q to stdout, want nothing", out)
	}
}

// TestLicenceSecretPermissionNamesTheCallsOwnPermissions holds what a permission
// refusal may not do: send an operator to a grant that leaves the refusal where
// it was. Reading the container is secrets.get and then versions.access on the
// version, and writing a version is versions.add; secretmanager.versions.get
// reads a version's metadata and is none of the three, so naming it costs a
// round trip to a grant that changes nothing.
func TestLicenceSecretPermissionNamesTheCallsOwnPermissions(t *testing.T) {
	s := &licenceSecrets{project: licenceTestGCP, id: licenceRenewalSecretID(licenceTestProject)}
	denied := &googleapi.Error{Code: http.StatusForbidden, Message: "Permission 'secretmanager.versions.access' denied on resource"}
	for _, tc := range []struct {
		op    string
		want  []string
		never []string
	}{
		{
			op:    "read",
			want:  []string{"secretmanager.secrets.get", "secretmanager.versions.access"},
			never: []string{"secretmanager.versions.get", "secretmanager.versions.add"},
		},
		{
			op:    "write",
			want:  []string{"secretmanager.versions.add"},
			never: []string{"secretmanager.versions.get", "secretmanager.versions.access"},
		},
	} {
		t.Run(tc.op, func(t *testing.T) {
			err := s.apiErr(tc.op, denied, nil)
			if code := fcli.ExitCode(err); code != fcli.ExitUserError {
				t.Errorf("a denied %s exited %d, want %d: %v", tc.op, code, fcli.ExitUserError, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal for a denied %s does not name %s: %q", tc.op, want, err.Error())
				}
			}
			for _, never := range tc.never {
				if strings.Contains(err.Error(), never) {
					t.Errorf("the refusal for a denied %s names %s, which its own call does not use: %q", tc.op, never, err.Error())
				}
			}
			if !strings.Contains(err.Error(), s.id) {
				t.Errorf("the refusal for a denied %s does not name the container: %q", tc.op, err.Error())
			}
		})
	}
}

// mustRun runs a command whose failure is the point, and answers its message.
func mustRun(t *testing.T, f *licenceFixture, args ...string) string {
	t.Helper()
	_, _, err := f.exec(t, "", args...)
	if err == nil {
		t.Fatalf("%v was accepted", args)
	}
	return err.Error()
}

// TestLicenceSetRefusesBeforeTheContainerExists holds the ordering K4.2 depends
// on: the command writes only into the container `fugaro update` created, and
// before it is there it refuses with the one line that names that command. What
// it must not do is create the container, which would give `update` a secret it
// did not make — so the key is not installed either: neither half of a bundle
// whose secret cannot be stored is written.
func TestLicenceSetRefusesBeforeTheContainerExists(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret := f.issuer.mint(t, f.claims()), licenceTestSecret(t)

	out, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set")
	if err == nil {
		t.Fatal("a bundle was installed with no renewal secret container in the installation")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	if !strings.Contains(err.Error(), "run `fugaro update`") {
		t.Errorf("the refusal %q does not name the one command that creates the container", err.Error())
	}
	if f.object(t).Present {
		t.Error("the key was installed even though its renewal secret could not be: the other order leaves a key that cannot renew")
	}
	if out != "" {
		t.Errorf("the refusal wrote %q to stdout, want nothing", out)
	}
}

// TestLicenceSetRefusesAKeyBoundToAnotherWorkspace holds TN2 at the writer: a
// key whose workspace_id is not this installation's Firebase project is refused
// before anything is written, so a key copied out of another installation's
// bucket never licenses this one.
func TestLicenceSetRefusesAKeyBoundToAnotherWorkspace(t *testing.T) {
	f := newLicenceFixture(t)
	key := f.issuer.mint(t, f.claims(func(c *licence.ClaimsV1) { c.WorkspaceID = "another-workspaces-firebase" }))

	_, _, err := f.exec(t, key+"\n", "licence", "set")
	if err == nil {
		t.Fatal("a key bound to another workspace was installed")
	}
	if !strings.Contains(err.Error(), "bound to a different workspace") {
		t.Errorf("the refusal %q does not say the key is bound elsewhere", err.Error())
	}
	if f.object(t).Present {
		t.Error("a key bound to another workspace was written")
	}
}

// TestLicenceSetWithAnOfflineKeyWritesNoSecret holds the air-gapped key's path
// (spec section 7): a `renewal_mode: none` key is one line, nothing contacts
// Secret Manager, and the command says which mode the key is in rather than
// asking for a secret it cannot use.
func TestLicenceSetWithAnOfflineKeyWritesNoSecret(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims(func(c *licence.ClaimsV1) {
		c.RenewalMode, c.CheckInterval = licence.RenewalNone, nil
	})
	key := f.issuer.mint(t, claims)

	out, errOut, err := f.exec(t, key+"\n", "licence", "set")
	if err != nil {
		t.Fatalf("licence set of an offline key = %v (%s)", err, errOut)
	}
	if !f.object(t).Present {
		t.Error("the offline key was not installed")
	}
	if got := f.secrets.Latest(f.secretContainer()); got != nil {
		t.Error("an offline key's install touched the renewal secret container")
	}
	if strings.Contains(out, "renewal secret written") {
		t.Errorf("the answer %q claims a secret was written for a key that has none", out)
	}
	if strings.Contains(errOut, "no renewal secret was read") {
		t.Errorf("the answer %q asks for a secret an offline key cannot use", errOut)
	}
}

// TestLicenceStatusJSONShape pins the machine-readable shape `status --json` has
// to keep: the field names are a format a reader outside this process shows
// (K3.6's console), so a rename is a wrong figure drawn without an error
// (CLAUDE.md, "A field never changes meaning"). The keys are compared as the
// decoder's own map, never through the struct under test, so a field that moved
// fails here rather than agreeing with itself.
func TestLicenceStatusJSONShape(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims()
	key := f.issuer.mint(t, claims)
	install(t, f, key)

	out, _, err := f.exec(t, "", "licence", "status", "--json")
	if err != nil {
		t.Fatalf("licence status --json = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("licence status --json wrote %q, which is not JSON: %v", out, err)
	}
	want := []string{"kind", "last_check", "paid_through", "project", "renewal_mode", "revoked", "seats", "source", "state", "tier"}
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, want) {
		t.Fatalf("licence status --json names %v, want %v", keys, want)
	}
	for field, value := range map[string]any{
		"project":      licenceTestProject,
		"state":        string(licence.Active),
		"tier":         licence.TierTeam,
		"seats":        float64(claims.Seats),
		"kind":         licence.KindPaid,
		"renewal_mode": licence.RenewalAuto,
		"source":       string(project.SourceProject),
		"revoked":      false,
		"paid_through": claims.PaidThrough.UTC().Format(time.RFC3339),
		"last_check":   nil,
	} {
		if got[field] != value {
			t.Errorf("licence status --json's %s is %#v, want %#v", field, got[field], value)
		}
	}

	// The last check is the store's own record, in the store's own shape: a
	// reader that has to be told which key a revocation was about reads these.
	if err := f.store(t).RecordCheck(context.Background(), f.now, project.CheckOK, claims.KeyID); err != nil {
		t.Fatal(err)
	}
	out, _, err = f.exec(t, "", "licence", "status", "--json")
	if err != nil {
		t.Fatalf("licence status --json = %v", err)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	check, ok := got["last_check"].(map[string]any)
	if !ok {
		t.Fatalf("licence status --json's last_check is %#v, want the check that was recorded", got["last_check"])
	}
	for field, value := range map[string]any{
		"key_id": claims.KeyID,
		"status": project.CheckOK,
		"at":     f.now.Format(time.RFC3339),
	} {
		if check[field] != value {
			t.Errorf("last_check's %s is %#v, want %#v", field, check[field], value)
		}
	}
}

// TestLicenceStatusPrintsTheFigures is the human rendering of the same view: a
// person reads the tier, the seats, the end of the paid period, the state and
// the last check, and no rendering of it carries the key.
func TestLicenceStatusPrintsTheFigures(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims()
	key := f.issuer.mint(t, claims)
	install(t, f, key)

	out, _, err := f.exec(t, "", "licence", "status")
	if err != nil {
		t.Fatalf("licence status = %v", err)
	}
	for _, want := range []string{
		"project:", licenceTestProject,
		"state:", string(licence.Active),
		"tier:", licence.TierTeam,
		"seats:", "5",
		"paid through:", claims.PaidThrough.UTC().Format(time.RFC3339),
		"source:", string(project.SourceProject),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("licence status printed %q, which does not carry %q", out, want)
		}
	}
	if strings.Contains(out, key) {
		t.Fatalf("licence status printed the key:\n%s", out)
	}
}

// TestLicenceStatusWithoutAKeyIsFree holds D2143: an installation that never
// had a key is Free and not a failure — the command succeeds and says so, and
// the JSON says it in the same words.
func TestLicenceStatusWithoutAKeyIsFree(t *testing.T) {
	f := newLicenceFixture(t)

	out, _, err := f.exec(t, "", "licence", "status", "--json")
	if err != nil {
		t.Fatalf("licence status --json on an installation with no key = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["state"] != "free" {
		t.Errorf("licence status --json answered state %#v, want free", got["state"])
	}
	if source, held := got["source"]; held {
		t.Errorf("an installation with no key answered source %#v: the field is absent where no store answered", source)
	}
	if got["paid_through"] != nil || got["last_check"] != nil {
		t.Errorf("an installation with no key answered paid_through %#v and last_check %#v, want null for both", got["paid_through"], got["last_check"])
	}

	// The note is the human rendering's: a person who runs `licence status` on
	// an installation with no key is told so, rather than left with a table.
	if _, errOut, err := f.exec(t, "", "licence", "status"); err != nil {
		t.Fatalf("licence status on an installation with no key = %v", err)
	} else if !strings.Contains(errOut, "no licence key is installed") {
		t.Errorf("the no-key answer %q does not say there is no key", errOut)
	}
}

// TestLicenceStatusNeverReadsARefusalAsFree is the rule that keeps a support
// call honest: a project whose object is one this Fugaro cannot use is refused,
// and never answered as Free, which is the one answer that tells a person their
// licence is fine.
func TestLicenceStatusNeverReadsARefusalAsFree(t *testing.T) {
	cases := []struct {
		name   string
		object string
		want   string
	}{
		{
			name:   "a key this build does not accept",
			object: `{"v":1,"keys":["fgk_not-a-key-at-all"]}`,
			want:   "does not accept",
		},
		{
			name:   "an object of another version",
			object: `{"v":2,"keys":[]}`,
			want:   "could not be used",
		},
		{
			name:   "an object that is not this store's",
			object: `{"v":1,"keys":[],"something_else":true}`,
			want:   "could not be used",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLicenceFixture(t)
			writeRawObject(t, f, tc.object)

			out, _, err := f.exec(t, "", "licence", "status", "--json")
			if err == nil {
				t.Fatalf("licence status --json answered %q for %s", out, tc.name)
			}
			if code := fcli.ExitCode(err); code != fcli.ExitUserError {
				t.Errorf("%s exited %d, want %d: %v", tc.name, code, fcli.ExitUserError, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal %q does not say %q", err.Error(), tc.want)
			}
			if strings.Contains(out, "free") {
				t.Errorf("%s was answered as Free: %q", tc.name, out)
			}
		})
	}
}

// writeRawObject puts an object the store would never write into the bucket, as
// a hand edit or another writer would.
func writeRawObject(t *testing.T, f *licenceFixture, object string) {
	t.Helper()
	path := filepath.Join(f.dir, "runs", licenceTestObjectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(object), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLicenceStatusFallsBackToTheMachinesKey holds the one fallback D2137
// allows: where the project holds no key and this machine's own `licence.key`
// does, that key answers and the answer says so — `set` is what upgrades every
// member at once. A key file any other local user can read is refused instead
// of read, which is the state `status` exists to make visible.
func TestLicenceStatusFallsBackToTheMachinesKey(t *testing.T) {
	f := newLicenceFixture(t)
	key := f.issuer.mint(t, f.claims())
	path := writeMachineKey(t, f, key, 0o600)

	out, _, err := f.exec(t, "", "licence", "status", "--json")
	if err != nil {
		t.Fatalf("licence status --json = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["source"] != string(project.SourceMachine) {
		t.Errorf("licence status --json answered source %#v with only a machine key installed, want %q", got["source"], project.SourceMachine)
	}
	if _, errOut, err := f.exec(t, "", "licence", "status"); err != nil {
		t.Fatalf("licence status = %v", err)
	} else if !strings.Contains(errOut, "this machine's own") {
		t.Errorf("the machine-key answer %q does not say which store answered", errOut)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.exec(t, "", "licence", "status", "--json"); err == nil {
		t.Error("a key file any other local user can read was read rather than refused")
	} else if !strings.Contains(err.Error(), "0600") {
		t.Errorf("the refusal %q does not say what mode the key file must have", err.Error())
	}
}

// writeMachineKey writes the CLI's own per-machine key file, which a console
// hands a child it spawns, and answers its path.
func writeMachineKey(t *testing.T, f *licenceFixture, key string, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(f.xdg, "fugaro")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "licence.key")
	if err := os.WriteFile(path, []byte(key+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLicenceWritesAreRefusedInAnAgentSession holds the rule every cloud write
// in this tree keeps: a coding agent's session applies nothing, and the refusal
// says why and names the marker. Both verbs that write are refused before
// anything is read — no bucket write, no secret version, no call to the licence
// server — and `status`, which is a read, is not refused at all.
func TestLicenceWritesAreRefusedInAnAgentSession(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret := f.issuer.mint(t, f.claims()), licenceTestSecret(t)
	f.seedSecretContainer(nil)
	server := f.serveLicenceServer(t, nil)
	t.Setenv("CLAUDECODE", "1")

	for _, tc := range []struct {
		args  []string
		stdin string
		want  string
	}{
		{args: []string{"licence", "set"}, stdin: key + "\n" + secret + "\n", want: "nothing was written"},
		{args: []string{"licence", "refresh"}, want: "nothing was renewed"},
	} {
		out, _, err := f.exec(t, tc.stdin, tc.args...)
		if err == nil {
			t.Fatalf("%v ran inside a coding agent's session", tc.args)
		}
		if code := fcli.ExitCode(err); code != fcli.ExitUserError {
			t.Errorf("%v exited %d, want %d: %v", tc.args, code, fcli.ExitUserError, err)
		}
		if !strings.Contains(err.Error(), "a coding agent's session is present (CLAUDECODE is set)") {
			t.Errorf("%v was refused with %q, which does not name the marker", tc.args, err.Error())
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v was refused with %q, which does not say %q", tc.args, err.Error(), tc.want)
		}
		if out != "" {
			t.Errorf("%v wrote %q to stdout", tc.args, out)
		}
	}
	if f.object(t).Present {
		t.Error("a refused write installed a key")
	}
	if f.secrets.Latest(f.secretContainer()) != nil {
		t.Error("a refused write put a version in the renewal secret container")
	}
	if server.count() != 0 {
		t.Error("a refused renewal called the licence server")
	}
	if _, _, err := f.exec(t, "", "licence", "status", "--json"); err != nil {
		t.Errorf("licence status was refused inside a coding agent's session (%v): it is a read, and a person has to be able to see what their installation holds from wherever they are", err)
	}
}

// TestLicenceAliasLicenseWorks holds the spelling the specification uses (spec
// section 11.2): `fugaro license …` is the same command as `fugaro licence …`,
// by cobra's own alias, and not a second command — not a second paid name, not
// a second section of the reference, and not a name the core's stub list
// carries.
func TestLicenceAliasLicenseWorks(t *testing.T) {
	f := newLicenceFixture(t)
	key := f.issuer.mint(t, f.claims())
	install(t, f, key)

	misspelled, _, err := f.exec(t, "", "license", "status", "--json")
	if err != nil {
		t.Fatalf("license status --json = %v", err)
	}
	spelled, _, err := f.exec(t, "", "licence", "status", "--json")
	if err != nil {
		t.Fatalf("licence status --json = %v", err)
	}
	if misspelled != spelled {
		t.Errorf("license status --json answered\n%s\nand licence status --json answered\n%s", misspelled, spelled)
	}

	root := fcli.NewRootCmd()
	target, _, err := root.Find([]string{"license"})
	if err != nil {
		t.Fatalf("license is not a command of the tagged tree: %v", err)
	}
	if target.Name() != "licence" {
		t.Errorf("license resolves to %q, want the licence command itself: the misspelling is an alias and not a command of its own", target.Name())
	}
	if !slices.Contains(fcli.PaidCommandNames(), "licence") {
		t.Error("the paid names do not carry `licence`")
	}
	if slices.Contains(fcli.PaidCommandNames(), "license") {
		t.Error("the paid names carry `license`: an alias is not a second name a build answers for")
	}
}

// TestLicenceRefreshCallsTheServerOnce is the renewal a person runs by hand
// (TN5): the project's key is read, its secret is proved to the licence server
// in exactly one call, and the answer's key and secret are installed — the
// secret first, then the key, so a key whose secret is missing is never
// installed.
func TestLicenceRefreshCallsTheServerOnce(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims()
	key, secret := f.issuer.mint(t, claims), licenceTestSecret(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()

	nextSecret := licenceTestSecret(t)
	next := f.issuer.mint(t, f.claims(func(c *licence.ClaimsV1) {
		c.KeyID, c.PaidThrough, c.IssuedAt = "lk_test_k32_0002", f.now.Add(600*24*time.Hour), f.now.Add(300*24*time.Hour)
	}))
	server := f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
		answer, err := json.Marshal(keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: nextSecret})
		if err != nil {
			t.Fatal(err)
		}
		return http.StatusOK, string(answer)
	})

	out, _, err := f.exec(t, "", "licence", "refresh")
	if err != nil {
		t.Fatalf("licence refresh = %v", err)
	}
	if server.count() != 1 {
		t.Fatalf("the licence server was called %d times, want exactly one", server.count())
	}
	call := server.call(t, 0)
	if call.KeyID != claims.KeyID || call.WorkspaceID != licenceTestFirebase {
		t.Errorf("the renewal named key %q workspace %q, want %q and %q", call.KeyID, call.WorkspaceID, claims.KeyID, licenceTestFirebase)
	}
	if call.RenewalSecret != secret {
		t.Error("the renewal did not carry the secret the installation holds")
	}
	obj := f.object(t)
	if len(obj.Keys) != 2 || obj.Keys[1] != next {
		t.Errorf("the project holds %d keys after the renewal, want the old one and the next", len(obj.Keys))
	}
	if got := f.secrets.Latest(f.secretContainer()); string(got) != nextSecret {
		t.Error("the renewal's new secret was not written to the container")
	}
	if strings.Contains(out, next) || strings.Contains(out, key) {
		t.Errorf("the renewal's answer carries a key:\n%s", out)
	}
	if !strings.Contains(out, project.Object) {
		t.Errorf("the renewal's answer %q does not say where the key was installed", out)
	}
}

// TestLicenceRefreshOutsideTheWindowWritesNothing holds the answer that is not
// a renewal: the key is still further from its end than its own
// renew_before_days, the server answers no key, and nothing is written — an
// installation that renewed nothing is not an installation that failed.
func TestLicenceRefreshOutsideTheWindowWritesNothing(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret := f.issuer.mint(t, f.claims()), licenceTestSecret(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()
	before := f.object(t).Raw
	f.serveLicenceServer(t, nil)

	out, _, err := f.exec(t, "", "licence", "refresh")
	if err != nil {
		t.Fatalf("licence refresh outside the window = %v", err)
	}
	if !strings.Contains(out, "nothing to renew yet") {
		t.Errorf("the answer %q does not say there was nothing to renew", out)
	}
	if got := f.object(t).Raw; !bytes.Equal(got, before) {
		t.Error("a renewal that installed nothing rewrote the project's object")
	}
}

// TestLicenceRefreshOfAnOfflineKeyNeverContactsTheServer holds spec section 7
// for the air-gapped key: nothing contacts the licence server at all — not even
// to read the secret container, which this installation may hold a version in
// from a key that was replaced.
func TestLicenceRefreshOfAnOfflineKeyNeverContactsTheServer(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims(func(c *licence.ClaimsV1) { c.RenewalMode = licence.RenewalNone })
	key := f.issuer.mint(t, claims)
	install(t, f, key)
	f.seedSecretContainer([]byte(licenceTestSecret(t)))
	f.secrets.AllowAccess()
	f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
		t.Error("an offline key's renewal called the licence server")
		return http.StatusOK, `{"status":"ok"}`
	})

	out, _, err := f.exec(t, "", "licence", "refresh")
	if err != nil {
		t.Fatalf("licence refresh of an offline key = %v", err)
	}
	if !strings.Contains(out, "offline") {
		t.Errorf("the answer %q does not say the key is offline", out)
	}
}

// TestLicenceRefreshOfARevokedKey holds the server's one answer that is not a
// renewal: the key is revoked, the refusal names it by its key_id — the support
// handle the specification says is not a secret — and nothing is written. The
// licence is Free from then on and a new key restores everything.
func TestLicenceRefreshOfARevokedKey(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims()
	key, secret := f.issuer.mint(t, claims), licenceTestSecret(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()
	before := f.object(t).Raw
	f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
		return http.StatusOK, `{"status":"` + keyservice.StatusRevoked + `"}`
	})

	_, _, err := f.exec(t, "", "licence", "refresh")
	if err == nil {
		t.Fatal("a revoked key was renewed")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the revocation exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	if !strings.Contains(err.Error(), claims.KeyID) {
		t.Errorf("the refusal %q does not name the key it is about", err.Error())
	}
	if got := f.object(t).Raw; !bytes.Equal(got, before) {
		t.Error("a revocation rewrote the project's object: revocation is Free in behaviour and deletes nothing")
	}
}

// TestLicenceRefreshWithoutAKeySaysWhatToDo holds the state an installation is
// in before anything is set: there is nothing to renew, and the refusal names
// the command that installs a key.
func TestLicenceRefreshWithoutAKeySaysWhatToDo(t *testing.T) {
	f := newLicenceFixture(t)

	_, _, err := f.exec(t, "", "licence", "refresh")
	if err == nil {
		t.Fatal("licence refresh renewed nothing and answered nothing")
	}
	if !strings.Contains(err.Error(), "`fugaro licence set`") {
		t.Errorf("the refusal %q does not name the command that installs a key", err.Error())
	}
}

// TestLicenceRefreshRefusesARenewalWithNoSecret is the other half of the K4.2
// coupling: the container is there and holds no version, which is what an
// installation looks like between the `fugaro update` that created it and the
// first `licence set`. A key that cannot prove itself is not sent to the server
// at all, and the refusal names the command that writes the secret.
func TestLicenceRefreshRefusesARenewalWithNoSecret(t *testing.T) {
	f := newLicenceFixture(t)
	install(t, f, f.issuer.mint(t, f.claims()))
	f.seedSecretContainer(nil)
	f.secrets.AllowAccess()
	server := f.serveLicenceServer(t, nil)

	_, _, err := f.exec(t, "", "licence", "refresh")
	if err == nil {
		t.Fatal("a renewal with no secret in the container was attempted")
	}
	if !strings.Contains(err.Error(), "`fugaro licence set`") {
		t.Errorf("the refusal %q does not name the command that writes the secret", err.Error())
	}
	if server.count() != 0 {
		t.Error("a renewal with no secret was sent to the licence server")
	}
}

// TestLicenceUnknownSubcommandNamesNoArgument holds the parent's own answer: a
// verb this build does not have is refused by naming the verbs it does, and the
// argument — which is where a pasted key lands — is never repeated.
func TestLicenceUnknownSubcommandNamesNoArgument(t *testing.T) {
	f := newLicenceFixture(t)
	key := f.issuer.mint(t, f.claims())

	out, _, err := f.exec(t, "", "licence", "not-a-verb-"+key)
	if err == nil {
		t.Fatal("an unknown licence subcommand was accepted")
	}
	if strings.Contains(err.Error(), key) || strings.Contains(out, key) {
		t.Fatalf("the unknown-subcommand refusal repeats its argument: %v", err)
	}
	for _, want := range []string{"set", "status", "refresh"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name the verb %q it does have", err.Error(), want)
		}
	}
}

// TestLicenceSubcommandsLandInTheTree holds the seam K3.3, K3.4 and K4.1 fill
// from their own files: cli.RegisterCommand is top-level only, so this file
// registers the command once and its verbs are added to a table that the tree
// reads when it is built. A verb added by addLicenceSubcommand has to be in the
// built tree, and the constructor has to be called at build time rather than
// once at registration, or a verb a later task adds would never appear.
func TestLicenceSubcommandsLandInTheTree(t *testing.T) {
	root := fcli.NewRootCmd()
	target, _, err := root.Find([]string{"licence"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sub := range target.Commands() {
		names = append(names, sub.Name())
	}
	for _, want := range []string{"refresh", "set", "status"} {
		if !slices.Contains(names, want) {
			t.Errorf("the built tree's `licence` has verbs %v, and %q is not one of them", names, want)
		}
	}
}

// install writes a key into the project's store the way `licence set` does,
// through the store itself, so that a test about `status` or `refresh` does not
// depend on `set` behaving.
func install(t *testing.T, f *licenceFixture, key string) {
	t.Helper()
	if err := f.store(t).Install(context.Background(), []byte(key)); err != nil {
		t.Fatal(err)
	}
}
