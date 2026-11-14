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
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/project"
	"github.com/dimipaun/fugaro/internal/agentenv"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The fixture's installation: a Fugaro project of its own, its GCP project, and
// the Firebase project its keys are bound to (TN2). None of the three is a real
// one; the verifier compares two strings and the fixture supplies both.
const (
	licenceTestProject  = "aurora"
	licenceTestGCP      = "proj-1234"
	licenceTestFirebase = "aurora-fp"
)

// licenceTestKeyPrefix is the key format's own prefix, written out because the
// constant is unexported (ee/licence/key.go): it is a fact of the format this
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
	// redirectTo, when set, is the Location every answer carries: the address a
	// client that follows a redirect would carry the renewal to next.
	redirectTo string
}

// redirect makes the server answer every call with a 308 to location. It takes
// the lock the answer is built under so that a test setting it and the server
// goroutine reading it are ordered.
func (s *renewServer) redirect(location string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redirectTo = location
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
		status, answer, location := http.StatusOK, `{"status":"ok"}`, s.redirectTo
		if s.reply != nil {
			status, answer = s.reply(call)
		}
		if location != "" {
			// A 308 keeps the method and the body: what a client that follows
			// one re-sends is this very renewal, secret and all.
			status, answer = http.StatusPermanentRedirect, ""
		}
		s.mu.Unlock()
		if location != "" {
			w.Header().Set("Location", location)
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
func (i *licenceIssuer) mint(t *testing.T, claims licence.Claims) string {
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
func (f *licenceFixture) claims(edit ...func(*licence.Claims)) licence.Claims {
	interval := "P1D"
	c := licence.Claims{
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

// bundle mints a key and the renewal secret that renews it, and answers both
// with the claim set the key carries. The set's renewal_secret_hash is the
// digest of that secret (D2135), which is what the licence server compares a
// renewal against and what `set` and `refresh` check before they write — so a
// test that installs a key and a secret installs a pair the real server would
// renew, rather than one minted apart (the claim set's own default hash names a
// secret no test holds, which is the pair this command exists to refuse).
func (f *licenceFixture) bundle(t *testing.T, edit ...func(*licence.Claims)) (key, secret string, claims licence.Claims) {
	t.Helper()
	secret = licenceTestSecret(t)
	claims = f.claims(append([]func(*licence.Claims){
		func(c *licence.Claims) { c.RenewalSecretHash = licenceSecretDigest(secret) },
	}, edit...)...)
	return f.issuer.mint(t, claims), secret, claims
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

// status runs `licence status --json` and answers it as the decoder's own map,
// which is what a test that asks what the installation reads back has to look
// at: the command's struct under test would agree with itself.
func (f *licenceFixture) status(t *testing.T) map[string]any {
	t.Helper()
	out, _, err := f.exec(t, "", "licence", "status", "--json")
	if err != nil {
		t.Fatalf("licence status --json = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("licence status --json wrote %q, which is not JSON: %v", out, err)
	}
	return got
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

// licenceRingPublished is whether this build's own keyring holds a key, read
// once as the package's value: the fixtures swap licenceRing for a pair they
// mint, so a test that asked later would be asking about a test's ring. The
// ring is empty in the repository (.claude/rules/ee.md: a later task, S2.9,
// publishes the production halves), and this is where a release that changes
// that is caught.
var licenceRingPublished = len(licenceRing) > 0

// TestTheRenewalContainerIsCreatedBeforeTheRingIsPublished holds the order
// S2.9 owes K4.2, which is the ground the container refusal stands on: `set`
// tells a person to run `fugaro update` when the installation has no renewal
// secret container, and that sentence is worth something only while `fugaro
// update` is the command that creates one (CLAUDE.md: update is the whole
// upgrade, and no release ships an operator step that is another command).
//
// Nothing creates that container today: K4.2's Terraform, the file
// deploy/terraform/gcp/modules/installation/licence.tf and the installation
// spec that feeds it, is not written yet. That is harmless exactly as long as
// no key verifies — every verb refuses a key before it looks for a container —
// and it stops being harmless the day the keyring is published: a valid key
// with its secret then reaches a refusal that names a command which cannot
// create the container, and the installation is stuck. So the gate is the
// publication itself, and this test fails the release that ships it first.
//
// The scan looks for the logical name the container's id is derived from
// (licenceRenewalSecretLogical) in the two places a container can be created
// from: the installation's Terraform and the Go that fills its variables. A
// name in a comment passes it, which is the fail-open direction: what it
// catches is a cloud side that names the container nowhere at all.
func TestTheRenewalContainerIsCreatedBeforeTheRingIsPublished(t *testing.T) {
	found, read := updateNamesTheRenewalContainer(t)
	if read == 0 {
		t.Fatalf("the scan read no Terraform or Go under deploy/terraform and internal/infra, so it cannot tell whether `fugaro update` creates the container the licence commands write into")
	}
	if !licenceRingPublished {
		return
	}
	if !found {
		t.Errorf("this build's keyring holds a key and neither deploy/terraform nor internal/infra names %s: `fugaro licence set` refuses a key and its secret with \"run `fugaro update`\" when the container is missing, and a published ring is what makes that refusal reachable — K4.2 has to create the container in the same release that publishes the keyring, or the refusal names a command that cannot fix what it found", licenceRenewalSecretLogical)
	}
}

// updateNamesTheRenewalContainer walks the cloud side of this repository for
// the renewal container's logical name, and answers how many files it read.
func updateNamesTheRenewalContainer(t *testing.T) (found bool, read int) {
	t.Helper()
	roots := []string{
		filepath.Join("..", "..", "deploy", "terraform"),
		filepath.Join("..", "..", "internal", "infra"),
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			switch filepath.Ext(path) {
			case ".tf", ".tfvars", ".go", ".json", ".yaml", ".yml":
			default:
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			read++
			found = found || strings.Contains(string(data), licenceRenewalSecretLogical)
			return nil
		})
		if err != nil {
			t.Fatalf("reading the cloud side under %s: %v", root, err)
		}
	}
	return found, read
}

// TestLicenceSetWritesTheProjectAndTheSecret is the happy path: a key read from
// standard input with the renewal secret on line 2 lands in the project's own
// object, the secret lands in the installation's container, and the answer
// carries the figures a person checks.
func TestLicenceSetWritesTheProjectAndTheSecret(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
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
	key, secret, _ := f.bundle(t)
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
	other := stranger.mint(t, f.claims(func(c *licence.Claims) { c.KID = licenceStrangerKey }))
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
	key, secret, _ := f.bundle(t)
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
	key, secret, _ := f.bundle(t)
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
		// A directory opens and then refuses to be read, which is the other
		// failure the --file value reaches: its own error carries the path, and
		// the refusal may not.
		{name: "a directory", args: []string{"licence", "set", "--file", dir}, path: dir, want: []string{"--file could not be read", "is a directory"}},
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
	key, secret, _ := f.bundle(t)
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

// TestLicenceSetRefusesAFlagShapedKey holds F5 at the other shape argv takes: a
// key pasted with a leading dash is a flag, and pflag quotes the flag it could
// not read back — "unknown shorthand flag: 'f' in -fgk_…" is the key in full. So
// every verb answers a flag error with one fixed sentence, and no argument, in
// either shape, is ever echoed.
func TestLicenceSetRefusesAFlagShapedKey(t *testing.T) {
	f := newLicenceFixture(t)
	key := f.issuer.mint(t, f.claims())
	f.seedSecretContainer(nil)

	for _, args := range [][]string{
		{"licence", "set", "-" + key},
		{"licence", "set", "--" + key},
		{"licence", "status", "-" + key},
		{"licence", "-" + key},
	} {
		out, errOut, err := f.exec(t, "", args...)
		if err == nil {
			t.Fatalf("%v was accepted with a key in a flag position", args)
		}
		if code := fcli.ExitCode(err); code != fcli.ExitUserError {
			t.Errorf("%v exited %d, want %d: %v", args, code, fcli.ExitUserError, err)
		}
		if !strings.Contains(err.Error(), "never the key") {
			t.Errorf("%v was refused with %q, which does not say what the command takes instead", args, err.Error())
		}
		for _, text := range []string{out, errOut, err.Error()} {
			if strings.Contains(text, key) {
				t.Errorf("%v repeated the key: pflag quotes the flag it could not read, and the flag is the key: %q", args, text)
			}
		}
		if out != "" {
			t.Errorf("%v wrote %q to stdout, want nothing", args, out)
		}
	}
	if f.object(t).Present {
		t.Error("a flag-shaped key was written")
	}
}

// TestLicenceRedactRemovesEverySpellingOfEveryValue pins the helper itself: the
// bytes as they were handed to a socket, the base64 AddVersion wraps them in,
// and nothing else — the text it was given stays readable, one line long and
// bounded to a printable field (safetext.Field).
func TestLicenceRedactRemovesEverySpellingOfEveryValue(t *testing.T) {
	secret := licenceTestSecret(t)
	key := licenceTestKeyPrefix + "not-a-key-this-test-minted"
	// A key of the same shape that no caller named: a server's own body can
	// carry one, and the value list cannot, so the shape is what has to take it
	// out (licenceKeyShape). Built here rather than written down, so the file
	// holds no key-shaped literal.
	elsewhere := licenceTestKeyPrefix +
		base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("p", 64))) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	text := "the body said " + secret + " then " + base64.StdEncoding.EncodeToString([]byte(secret)) +
		" then " + key + " then " + base64.StdEncoding.EncodeToString([]byte(key)) +
		" then " + elsewhere +
		" and " + strings.Repeat("x", 2*safetext.FieldMax)
	got := licenceRedact(text, secret, key, "")
	for _, form := range []string{
		secret, base64.StdEncoding.EncodeToString([]byte(secret)),
		key, base64.StdEncoding.EncodeToString([]byte(key)),
		elsewhere,
	} {
		if strings.Contains(got, form) {
			t.Errorf("licenceRedact left a %d-byte spelling of a value in its answer: %q", len(form), got)
		}
	}
	if head := licenceHead(elsewhere); strings.Contains(got, head) {
		t.Errorf("licenceRedact left the first %d runes of a key it was never given in its %d-rune answer, so the shape rule does not reach a body's own key (the answer is not printed: it is the leak)",
			licenceHeadRunes, utf8.RuneCountInString(got))
	}
	if !strings.Contains(got, "the body said") || !strings.Contains(got, "then") {
		t.Errorf("licenceRedact answered %q, want the text it was given with the values taken out", got)
	}
	if n := utf8.RuneCountInString(got); n > safetext.FieldMax {
		t.Errorf("licenceRedact answered %d runes, want at most %d (a store or a server may echo a 16 KiB body)", n, safetext.FieldMax)
	}
}

// TestLicenceRefusalsCarryNoKeyMaterial pins licenceRedact where the material
// would actually come back from: the licence server's own error body, which this
// command prints (up to 16 KiB of whatever it was sent), and Secret Manager's
// error, which echoes the payload it was handed in both spellings. Disabling the
// redaction has to fail here: it is the whole of what keeps a secret out of a
// terminal and out of a pasted ticket.
func TestLicenceRefusalsCarryNoKeyMaterial(t *testing.T) {
	t.Run("the licence server's error body", func(t *testing.T) {
		f := newLicenceFixture(t)
		key, secret, _ := f.bundle(t)
		f.seedSecretContainer([]byte(secret))
		f.secrets.AllowAccess()
		install(t, f, key)
		f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
			return http.StatusInternalServerError, `{"error":"the renewal secret ` + secret + ` is not one key ` + key + ` renews with"}`
		})

		out, errOut, err := f.exec(t, "", "licence", "refresh")
		if err == nil {
			t.Fatal("a server answering 500 renewed the key")
		}
		for _, text := range []string{out, errOut, err.Error()} {
			assertNoKeyMaterial(t, "the refusal for a server's own error body", text, key, secret)
		}
	})

	t.Run("the licence server's body echoing a key this installation never held", func(t *testing.T) {
		f := newLicenceFixture(t)
		key, secret, _ := f.bundle(t)
		f.seedSecretContainer([]byte(secret))
		f.secrets.AllowAccess()
		install(t, f, key)
		// A second key of the same issuer: what a server's own records hold
		// beside the installed one — the successor of a renewal it already
		// answered, for one. No value list can name it, because nothing here
		// has read it, so the shape is the whole of what keeps it out.
		other := f.issuer.mint(t, f.claims(func(c *licence.Claims) { c.KeyID = "lk_test_k32_0002" }))
		f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
			return http.StatusInternalServerError, `{"error":"the successor of ` + key + ` is ` + other + `"}`
		})

		out, errOut, err := f.exec(t, "", "licence", "refresh")
		if err == nil {
			t.Fatal("a server answering 500 renewed the key")
		}
		for _, text := range []string{out, errOut, err.Error()} {
			assertNoKeyMaterial(t, "the refusal for a body echoing a key this installation never held", text, other, secret)
			assertNoKeyMaterial(t, "the refusal for a body echoing the installed key", text, key, secret)
		}
	})

	t.Run("the licence server answering a key where a status belongs", func(t *testing.T) {
		f := newLicenceFixture(t)
		key, secret, _ := f.bundle(t)
		f.seedSecretContainer([]byte(secret))
		f.secrets.AllowAccess()
		install(t, f, key)
		// A 200 is not a refusal, so the status is the whole of what decides
		// what the command does with the answer, and it is a field of a body
		// this build did not write: a server whose records hold a key it
		// already answered for — the successor of a renewal, for one — can have
		// written one there, and nothing here has read it, so no value list can
		// name it. The status is printed through a 40-rune clip as well, and
		// that is the rest of why it needs the shape rule rather than a value
		// pass: the front of a key is the key.
		other := f.issuer.mint(t, f.claims(func(c *licence.Claims) { c.KeyID = "lk_test_k32_0003" }))
		f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
			return http.StatusOK, `{"status":"` + other + `"}`
		})

		out, errOut, err := f.exec(t, "", "licence", "refresh")
		if err == nil {
			t.Fatal("a server answering a status this Fugaro does not read renewed the key")
		}
		for _, text := range []string{out, errOut, err.Error()} {
			assertNoKeyMaterial(t, "the refusal for a status field carrying a key", text, other, secret)
		}
	})

	t.Run("a Secret Manager error echoing the payload", func(t *testing.T) {
		f := newLicenceFixture(t)
		key, secret, _ := f.bundle(t)
		f.seedSecretContainer(nil)
		f.secrets.EchoAddVersionPayload = true

		out, errOut, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set")
		if err == nil {
			t.Fatal("a write Secret Manager refused was accepted")
		}
		for _, text := range []string{out, errOut, err.Error()} {
			assertNoKeyMaterial(t, "the refusal for a Secret Manager error that echoes the payload", text, key, secret)
		}
		if f.object(t).Present {
			t.Error("a refused secret write still installed the key")
		}
	})
}

// licenceHeadRunes is how much of a spelling assertNoKeyMaterial reads for:
// long enough that the front of a key or a secret is unmistakable in a message,
// short enough that the message's own bound cannot be what hides it. The
// shortest bound any message here is written to is the 40-rune clip on the
// licence server's status (licence refresh refuses one it does not read), so a
// front longer than that would be blind to exactly the message that bound
// leaves — and the front of a key is the key.
const licenceHeadRunes = 40

// assertNoKeyMaterial fails when text carries any spelling of the key or the
// secret — the bytes as they are, and the base64 a payload is wrapped in — and
// prints only lengths, because a failure report is one more place key material
// would reach.
//
// Each spelling is read for twice: whole, and by its first licenceHeadRunes
// runes. The whole check is the exact one, and on its own it cannot fail for a
// key: every message here is bounded to safetext.FieldMax runes by
// licenceRedact, and a v1 key is longer than that bound, so all of it could
// never stand in a message even with the redaction switched off. What a leak
// leaves behind is its front — the bound cuts the tail and keeps the head — so
// the head is what has to be absent as well, and the head check is the one that
// fails when the redaction goes.
func assertNoKeyMaterial(t *testing.T, what, text, key, secret string) {
	t.Helper()
	for _, form := range []string{
		key, secret,
		base64.StdEncoding.EncodeToString([]byte(key)),
		base64.StdEncoding.EncodeToString([]byte(secret)),
	} {
		if form == "" {
			continue
		}
		if strings.Contains(text, form) {
			t.Errorf("%s carries all %d runes of a spelling of key material, in a %d-rune message (the message itself is not printed: it is the leak)",
				what, utf8.RuneCountInString(form), utf8.RuneCountInString(text))
		}
		if head := licenceHead(form); head != form && strings.Contains(text, head) {
			t.Errorf("%s carries the first %d runes of a %d-rune spelling of key material, in a %d-rune message (the message itself is not printed: it is the leak)",
				what, utf8.RuneCountInString(head), utf8.RuneCountInString(form), utf8.RuneCountInString(text))
		}
	}
}

// licenceHead is the first licenceHeadRunes runes of a spelling, and the
// spelling itself where it is no longer than that.
func licenceHead(form string) string {
	r := []rune(form)
	if len(r) <= licenceHeadRunes {
		return form
	}
	return string(r[:licenceHeadRunes])
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
	key, secret, _ := f.bundle(t)

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

// TestLicenceSetRefusesASecretThatIsNotTheKeys holds the pairing the licence
// server compares: the key carries hex(sha256(secret)) in renewal_secret_hash
// (D2135), and a renewal sends the secret for the server to hash and weigh
// against it. A mistyped line 2, or a secret from another bundle, would
// otherwise replace a secret that works — silently, the refusal that says so
// arriving at the next renewal, days later and possibly past the last window —
// so it is refused while both halves are still in front of the person, and
// neither the container nor the project's object is written.
func TestLicenceSetRefusesASecretThatIsNotTheKeys(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, claims := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	before := f.object(t).Raw

	other := licenceTestSecret(t)
	out, errOut, err := f.exec(t, key+"\n"+other+"\n", "licence", "set")
	if err == nil {
		t.Fatal("a renewal secret that is not the key's own was installed")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), claims.KeyID) {
		t.Errorf("the refusal %q does not say which line is wrong or which key it is about", err.Error())
	}
	for _, text := range []string{out, errOut, err.Error()} {
		if strings.Contains(text, other) || strings.Contains(text, secret) {
			t.Errorf("the refusal repeated the secret it refused: %q", text)
		}
	}
	if got := f.secrets.Latest(f.secretContainer()); string(got) != secret {
		t.Error("the container's newest version is not the secret the key renews with: a secret that cannot prove itself was written over the one that works")
	}
	if got := f.object(t).Raw; !bytes.Equal(got, before) {
		t.Error("the refusal rewrote the project's object")
	}
}

// TestLicenceSetOfTheSameTermIsTheKeyTheInstallationUses holds the reissue the
// licence server mints with the term of the key it replaces: a seat change
// reissues with the term's own end (design/key-signing-service.md section 5.3),
// so the pasted key and the installed one are paid through the same day — and
// the object holds the latest valid key (T26), decided for two of the same term
// by the write that came last. A store that kept the older one would report a
// change that did not happen, and the renewal would prove itself with the new
// key's secret beside the old key's id, which the server refuses (403) — days
// later, with the key that does apply one renewal closer to its end.
//
// The renewal is what reads the applied key back out: the body it sends carries
// the id and the secret, and both must be the ones just set.
func TestLicenceSetOfTheSameTermIsTheKeyTheInstallationUses(t *testing.T) {
	f := newLicenceFixture(t)
	first, firstSecret, _ := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, first+"\n"+firstSecret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}

	reissue, reissueSecret, claims := f.bundle(t, func(c *licence.Claims) {
		c.KeyID, c.Seats = "lk_test_k32_seats", 8
	})
	if _, _, err := f.exec(t, reissue+"\n"+reissueSecret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set of the reissue = %v", err)
	}
	obj := f.object(t)
	if len(obj.Keys) != 2 || obj.Keys[1] != reissue {
		t.Errorf("the project holds %d keys after the reissue, want the key it replaced and the reissue", len(obj.Keys))
	}
	view := f.status(t)
	if view["state"] != string(licence.Active) || view["seats"] != float64(8) {
		t.Errorf("licence status --json reads state %#v and seats %#v after the reissue, want active and 8: the key just set is the one the installation runs on", view["state"], view["seats"])
	}

	f.secrets.AllowAccess()
	server := f.serveLicenceServer(t, nil)
	if _, _, err := f.exec(t, "", "licence", "refresh"); err != nil {
		t.Fatalf("licence refresh after the reissue = %v", err)
	}
	call := server.call(t, 0)
	if call.KeyID != claims.KeyID {
		t.Errorf("the renewal named key %q, want the reissue %q: the installation is renewing with the key it replaced", call.KeyID, claims.KeyID)
	}
	if call.RenewalSecret != reissueSecret {
		t.Error("the renewal did not carry the secret the reissue renews with")
	}
}

// TestLicenceSetRefusesAKeyTheInstallationWouldNotApply holds the other half of
// the store's rule at the writer: a key whose term ends before the applied
// key's is written to the object and never used (T26), so the set reports a
// change that does not happen while the renewal secret written beside it takes
// the renewal away from the key the installation does run on. It is refused
// before either write, and neither the container nor the object moves.
func TestLicenceSetRefusesAKeyTheInstallationWouldNotApply(t *testing.T) {
	f := newLicenceFixture(t)
	applied, appliedSecret, appliedClaims := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, applied+"\n"+appliedSecret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	before := f.object(t).Raw

	older, olderSecret, olderClaims := f.bundle(t, func(c *licence.Claims) {
		c.KeyID, c.PaidThrough, c.IssuedAt = "lk_test_k32_older", f.now.Add(30*24*time.Hour), f.now.Add(-30*24*time.Hour)
	})
	out, errOut, err := f.exec(t, older+"\n"+olderSecret+"\n", "licence", "set")
	if err == nil {
		t.Fatal("a key the installation would never apply was installed")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	for _, want := range []string{appliedClaims.KeyID, olderClaims.KeyID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s: the person has both keys in front of them and the sentence has to say which one the installation runs on", err.Error(), want)
		}
	}
	for _, text := range []string{out, errOut, err.Error()} {
		if strings.Contains(text, older) || strings.Contains(text, olderSecret) {
			t.Errorf("the refusal repeated the key or the secret it refused: %q", text)
		}
	}
	if got := f.secrets.Latest(f.secretContainer()); string(got) != appliedSecret {
		t.Error("the container's newest version is not the secret the applied key renews with: a secret was written for a key the installation does not run on")
	}
	if got := f.object(t).Raw; !bytes.Equal(got, before) {
		t.Error("the refusal rewrote the project's object")
	}
}

// TestLicenceSetReinstatesTheInstallationAfterARevocation is the recovery the
// revoked-key refusal promises: the server revokes a leaked key and reissues it
// under a new key_id with the term of the key it replaces, the installation is
// Free until that reissue is set — and setting it has to be the change that
// brings the installation back, not a no-op that leaves it on the revoked key.
// `status` is what says so: it is the record the revocation lives in.
func TestLicenceSetReinstatesTheInstallationAfterARevocation(t *testing.T) {
	f := newLicenceFixture(t)
	leaked, leakedSecret, leakedClaims := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, leaked+"\n"+leakedSecret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()
	f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
		return http.StatusOK, `{"status":"` + keyservice.StatusRevoked + `"}`
	})
	if _, _, err := f.exec(t, "", "licence", "refresh"); err == nil {
		t.Fatal("a revoked key was renewed")
	}
	revoked := f.status(t)
	if revoked["revoked"] != true || revoked["state"] != string(licence.Expired) {
		t.Fatalf("licence status --json reads revoked %#v state %#v after the server revoked the key, want true and expired", revoked["revoked"], revoked["state"])
	}

	// The reissue carries the term of the key it replaced, which is what the
	// server mints for a leak: the same paid_through, a new key_id, its own
	// renewal secret.
	reissue, reissueSecret, reissueClaims := f.bundle(t, func(c *licence.Claims) {
		c.KeyID = "lk_test_k32_reissue"
	})
	if _, _, err := f.exec(t, reissue+"\n"+reissueSecret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set of the reissue = %v", err)
	}
	back := f.status(t)
	if back["revoked"] != false || back["state"] != string(licence.Active) {
		t.Errorf("licence status --json reads revoked %#v state %#v after the reissue was set, want false and active: the reissue is what restores the installation", back["revoked"], back["state"])
	}
	if obj := f.object(t); len(obj.Keys) != 2 || !slices.Contains(obj.Keys, leaked) {
		t.Errorf("the project holds %d keys after the reissue, want the revoked key beside it: nothing a revocation does deletes a key", len(obj.Keys))
	}

	// And the installation can renew again, with the reissue's id and secret:
	// this is what the revoked key could not do.
	successor, successorSecret, _ := f.bundle(t, func(c *licence.Claims) {
		c.KeyID, c.PaidThrough, c.IssuedAt = "lk_test_k32_0003", f.now.Add(600*24*time.Hour), f.now.Add(300*24*time.Hour)
	})
	server := f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
		answer, err := json.Marshal(keyservice.Answer{Status: keyservice.StatusOK, Key: successor, RenewalSecret: successorSecret})
		if err != nil {
			t.Fatal(err)
		}
		return http.StatusOK, string(answer)
	})
	if _, _, err := f.exec(t, "", "licence", "refresh"); err != nil {
		t.Fatalf("licence refresh after the reissue = %v", err)
	}
	call := server.call(t, 0)
	if call.KeyID != reissueClaims.KeyID {
		t.Errorf("the renewal named key %q, want the reissue %q: the installation is still proving itself with the revoked key %s", call.KeyID, reissueClaims.KeyID, leakedClaims.KeyID)
	}
	if call.RenewalSecret != reissueSecret {
		t.Error("the renewal did not carry the secret the reissue renews with")
	}
}

// TestLicenceSetRefusesAKeyBoundToAnotherWorkspace holds TN2 at the writer: a
// key whose workspace_id is not this installation's Firebase project is refused
// before anything is written, so a key copied out of another installation's
// bucket never licenses this one.
func TestLicenceSetRefusesAKeyBoundToAnotherWorkspace(t *testing.T) {
	f := newLicenceFixture(t)
	key := f.issuer.mint(t, f.claims(func(c *licence.Claims) { c.WorkspaceID = "another-workspaces-firebase" }))

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
	claims := f.claims(func(c *licence.Claims) {
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
	want := []string{"kind", "last_check", "paid_through", "project", "renewal_mode", "revoked", "seats", "source", "state", "tier", "warning"}
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
	key, secret, _ := f.bundle(t)
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
	key, secret, claims := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()

	next, nextSecret, _ := f.bundle(t, func(c *licence.Claims) {
		c.KeyID, c.PaidThrough, c.IssuedAt = "lk_test_k32_0002", f.now.Add(600*24*time.Hour), f.now.Add(300*24*time.Hour)
	})
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

// TestLicenceRefreshRefusesASecretThatIsNotTheKeys holds the same pairing at
// the server's own answer: the successor key names the digest of the secret
// that renews it, so an answer whose key and secret do not go together is
// refused rather than installed — the installation would otherwise hold a key
// it could never renew again, and would learn that at the next renewal, days
// later — and neither the container nor the project's object is written.
func TestLicenceRefreshRefusesASecretThatIsNotTheKeys(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()
	before := f.object(t).Raw

	// The successor is one this build accepts — the ring it verifies with minted
	// it — and the secret beside it belongs to another key: the answer
	// contradicts itself, which is the state that would leave the installation
	// holding a pair no renewal can prove.
	nextKeyID := "lk_test_k32_0002"
	next, _, _ := f.bundle(t, func(c *licence.Claims) {
		c.KeyID, c.PaidThrough, c.IssuedAt = nextKeyID, f.now.Add(600*24*time.Hour), f.now.Add(300*24*time.Hour)
	})
	elsewhere := licenceTestSecret(t)
	server := f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
		answer, err := json.Marshal(keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: elsewhere})
		if err != nil {
			t.Fatal(err)
		}
		return http.StatusOK, string(answer)
	})

	out, errOut, err := f.exec(t, "", "licence", "refresh")
	if err == nil {
		t.Fatal("an answer whose key and secret do not go together was installed")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitRemoteError {
		t.Errorf("the refusal exited %d, want %d: the answer is the server's and a person has nothing to correct: %v", code, fcli.ExitRemoteError, err)
	}
	if !strings.Contains(err.Error(), nextKeyID) || !strings.Contains(err.Error(), "renewal secret") {
		t.Errorf("the refusal %q does not name the key it is about or the secret that does not match it", err.Error())
	}
	for _, text := range []string{out, errOut, err.Error()} {
		if strings.Contains(text, elsewhere) || strings.Contains(text, secret) {
			t.Errorf("the refusal repeated a secret: %q", text)
		}
	}
	if server.count() != 1 {
		t.Errorf("the licence server was called %d times, want exactly one", server.count())
	}
	if got := f.secrets.Latest(f.secretContainer()); string(got) != secret {
		t.Error("the container took a version from a secret that is not the key's own")
	}
	if got := f.object(t).Raw; !bytes.Equal(got, before) {
		t.Error("the refusal rewrote the project's object")
	}
}

// TestLicenceRefreshRefusesARedirect holds the client's own rule: a 308 from
// the licence server's address is the answer, and it is not followed. A
// followed one re-sends the renewal — the key id and the renewal secret, which
// is the whole of the proof the server asks for — to whatever host and scheme
// the Location names, and reads that host's answer as the licence server's: a
// `revoked` among them is written to the project's object for good, so a
// redirect would be both a leak of the secret and a way to make an installation
// Free. The renewal goes to the licence server's address and nowhere else.
func TestLicenceRefreshRefusesARedirect(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()
	before := f.object(t).Raw

	// Where the redirect points. It must never be called, and it answers the
	// one status that would be recorded if it were.
	var followed atomic.Int64
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"`+keyservice.StatusRevoked+`"}`)
	}))
	t.Cleanup(elsewhere.Close)

	server := f.serveLicenceServer(t, nil)
	// Another host over plain http: a redirect changes the scheme as readily as
	// the address, and both are what the client refuses to take an answer from.
	server.redirect(elsewhere.URL + keyservice.RouteRenew)

	_, _, err := f.exec(t, "", "licence", "refresh")
	if err == nil {
		t.Fatal("a redirect was answered as though the licence server had renewed")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitRemoteError {
		t.Errorf("the redirect exited %d, want %d: %v", code, fcli.ExitRemoteError, err)
	}
	if !strings.Contains(err.Error(), "308") {
		t.Errorf("the refusal %q does not say what the licence server answered", err.Error())
	}
	if got := followed.Load(); got != 0 {
		t.Errorf("the redirect delivered the renewal %d time(s) to the host it named: the renewal secret goes to the licence server's address and nowhere else", got)
	}
	if server.count() != 1 {
		t.Errorf("the licence server was called %d times, want exactly one", server.count())
	}
	if got := f.object(t).Raw; !bytes.Equal(got, before) {
		t.Error("a redirect rewrote the project's object: what the licence server did not answer is not written")
	}
}

// TestLicenceRefreshOutsideTheWindowWritesNothing holds the answer that is not
// a renewal: the key is still further from its end than its own
// renew_before_days, the server answers no key, and nothing is written — an
// installation that renewed nothing is not an installation that failed.
func TestLicenceRefreshOutsideTheWindowWritesNothing(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
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
	claims := f.claims(func(c *licence.Claims) { c.RenewalMode = licence.RenewalNone })
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
// handle the specification says is not a secret — no key is deleted, and the
// revocation is recorded, which is what makes the refusal true where a person
// looks next. The licence is Free from then on and a new key restores
// everything.
func TestLicenceRefreshOfARevokedKey(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, claims := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()
	before := f.object(t)
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

	// Nothing is deleted: revocation is Free in behaviour, and the key the
	// answer named stays where it was.
	after := f.object(t)
	if !slices.Equal(after.Keys, before.Keys) {
		t.Errorf("the revocation changed the project's keys to %v, want %v: nothing a revocation does deletes one", after.Keys, before.Keys)
	}
	// And the answer is recorded, which is what the refusal's own sentence
	// needs to be true where a person looks next: `status` reads a revocation
	// out of the store's record and never out of the key, and an installation
	// with no coordinator has no other thing that talks to the server (TN5).
	if after.Check == nil || after.Check.Status != project.CheckRevoked || after.Check.KeyID != claims.KeyID {
		t.Errorf("the revocation was not recorded: last_check is %+v, want a %s check about %s", after.Check, project.CheckRevoked, claims.KeyID)
	}
	out, _, err := f.exec(t, "", "licence", "status", "--json")
	if err != nil {
		t.Fatalf("licence status --json = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("licence status --json wrote %q, which is not JSON: %v", out, err)
	}
	if got["revoked"] != true {
		t.Errorf("licence status --json reads revoked %#v after the server revoked the key, want true: the refusal says the installation is Free from now on", got["revoked"])
	}
	if got["state"] != string(licence.Expired) {
		t.Errorf("licence status --json reads state %#v after the server revoked the key, want %q", got["state"], licence.Expired)
	}
}

// TestLicenceRefreshReportsARevocationItCouldNotRecord holds the branch that
// exists because the record is the only place a revocation outlives the answer:
// the server answers revoked, the store refuses the write, and the command says
// the record did not land rather than telling an operator the installation is
// Free while `status` goes on reading the key as active.
func TestLicenceRefreshReportsARevocationItCouldNotRecord(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory, so the store cannot be made to refuse the write")
	}
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
	f.seedSecretContainer(nil)
	if _, _, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v", err)
	}
	f.secrets.AllowAccess()
	f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
		return http.StatusOK, `{"status":"` + keyservice.StatusRevoked + `"}`
	})

	// The object's own directory refuses a new file, which is where the bucket
	// driver puts the temporary one it renames over the object. The read the
	// command makes first is unaffected, so it is the record's write, and
	// nothing earlier, that fails.
	dir := filepath.Join(f.dir, "runs", filepath.Dir(licenceTestObjectPath))
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("making %s read-only = %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("restoring %s = %v", dir, err)
		}
	})

	_, _, err := f.exec(t, "", "licence", "refresh")
	if err == nil {
		t.Fatal("a revocation whose record could not be written was reported as recorded")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	if !strings.Contains(err.Error(), "could not be written") {
		t.Errorf("the refusal %q does not say the record of the revocation did not land", err.Error())
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
