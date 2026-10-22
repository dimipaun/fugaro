package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

// This file is C1.25's tests: `fugaro creds set` and `fugaro creds ls`, the
// only writer and the only reader of the one bundle secret (Q6). The two
// properties every test here is about are the two the task is:
//
//   - nothing the commands print, and nothing a server's answer carries into
//     them, ever holds a byte of a value (or a prefix of one that narrows it);
//   - the write is a read-modify-write of ONE document whose other entries
//     keep their bytes, verified by a read-back, with the version it replaced
//     retired.
//
// Every fixture value is built at run time from a constant prefix and
// t.Name() (Convention 2): no secret-shaped literal, ever. That is also why
// the leak assertions are written over the VALUES the test built rather than
// over a pattern — a value this file invented is the only thing it can
// honestly claim to look for.

// credsValuePrefix is the constant every fixture value is built from. It is
// not secret-shaped on purpose: a literal that looked like a token would trip
// the plugin lint and the docs guards, and a test value needs no shape.
const credsValuePrefix = "ZQ7X4"

// credsValue is a fixture value: the constant prefix, a tag and the test's own
// name, so two tests never share a value and no value is a literal
// (Convention 2).
//
// It is upper-cased so that every byte of it is drawn from an alphabet this
// command's own output cannot contain — its output is lower-case prose, key
// names and RFC 3339 times, with a handful of upper-case words ("PRESENT",
// "DISABLED", a marker's name) — which is what makes assertNoValue's scan
// sound: a four-byte match is a leak and not a coincidence of the alphabet.
// (It was not, before: "oauth" inside a tag matched claude-oauth-token, and
// "read" inside a test name matched the prose of a refusal.)
func credsValue(t *testing.T, tag string) string {
	t.Helper()
	return credsValuePrefix + "-" + strings.ToUpper(tag+"-"+t.Name())
}

// credsFixture is the CLI's cloud fixture with a Secret Manager fake, plus the
// bundle container's ID as the loader names it (credsvc.BundleSecretID, which
// is what C1.18's Terraform creates).
type credsFixture struct {
	*gcpfake.Secrets
	id string
}

func credsFixtureFor(t *testing.T) *credsFixture {
	t.Helper()
	sm := gcpfake.NewSecrets(t)
	// The fake refuses a value read unless a test allows it, and these two
	// commands read the bundle document by design: `set` reads it to edit it
	// and `ls` reads it to list it. What they never do with it is print it,
	// which is what the assertions in this file are about. There is one
	// container in this fixture, so the allowance reaches nothing else.
	sm.AllowAccess()
	newCloudFixture(t, "secret_manager: "+sm.URL+"/")
	return &credsFixture{Secrets: sm, id: credsvc.BundleSecretID("aurora")}
}

// seed creates the container, as the installation's Terraform does, with doc
// as its first version. A nil doc is a container with no version: the state
// the first `creds set` starts from.
func (f *credsFixture) seed(t *testing.T, doc []byte) {
	t.Helper()
	f.Seed(f.id, map[string]string{"fugaro": "managed"}, doc)
}

// value is the newest version's bytes of the bundle.
func (f *credsFixture) value(t *testing.T) string {
	t.Helper()
	return string(f.Latest(f.id))
}

// writes is how many versions the fake was asked to add. The read-modify-write
// makes exactly one, and a lost update is never retried: both are asserted
// through this.
func (f *credsFixture) writes() int {
	n := 0
	for _, r := range f.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, ":addVersion") {
			n++
		}
	}
	return n
}

// reads is how many times the container itself was read.
func (f *credsFixture) reads() int {
	n := 0
	for _, r := range f.Requests() {
		if r.Method == http.MethodGet && strings.HasSuffix(r.Path, "/secrets/"+f.id) {
			n++
		}
	}
	return n
}

// The red step, and the reason the command re-reads at all: a read-modify-write
// with no conditional write behind it can lose another owner's entry. Another
// write lands between this command's write and its read-back — through the
// command's own seam, because nothing else can reach into that window — and the
// disagreement is a named failure: exit 1, nothing retried, nothing disabled,
// and the other owner's document left exactly as it is.
func TestCredsSetDetectsLostConcurrentUpdate(t *testing.T) {
	f := credsFixtureFor(t)
	first := credsValue(t, "first")
	ours := credsValue(t, "ours")
	other := credsValue(t, "other")
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+first+"\n"))

	restore := credsWriteBack
	t.Cleanup(func() { credsWriteBack = restore })
	credsWriteBack = func(ctx context.Context, s *bundleStore) (bundleDoc, error) {
		// Another terminal's `creds set` writes its document here, without a
		// client call of this command's: the fake's own state changes, so
		// writes() below is still this command's alone.
		f.AddVersion(f.id, []byte(config.AnthropicSecret+": "+other+"\n"))
		return s.access(ctx, "latest")
	}

	out, errOut, err := executeStdin(t, ours+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil {
		t.Fatalf("a lost concurrent update was not refused: %q", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, want %d: %v", ExitCode(err), ExitUserError, err)
	}
	msg := err.Error()
	for _, want := range []string{"not the document this command wrote", "another write landed", "No version was disabled"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %s", want, msg)
		}
	}
	if n := f.writes(); n != 1 {
		t.Errorf("%d versions were written, want 1 (nothing is retried)", n)
	}
	// The other owner's document is the newest version and is untouched: this
	// command did not write again, and its own version is behind it.
	if got := f.value(t); !strings.Contains(got, other) || strings.Contains(got, ours) {
		t.Errorf("the bundle is %q, want the other write's document", got)
	}
	// Nothing was retired: the version this command replaced is still enabled,
	// and so is the other owner's, because a disable here would be a disable
	// of a version this command did not verify.
	if got := f.Disabled(); len(got) != 0 {
		t.Errorf("a lost update disabled %q, want nothing", got)
	}
	if leak := out + errOut + msg; strings.Contains(leak, ours) || strings.Contains(leak, other) || strings.Contains(leak, first) {
		t.Errorf("a value leaked: %q", leak)
	}
}

// The other half of the same window, and the one the re-read cannot see: the
// concurrent write lands between this command's READ and its ADD — the long
// window, the one the hidden prompt sits in. This command's document is built
// on the older read, so the other writer's entry is not in it; its own write
// lands as the version after that writer's, so the newest version is this
// command's document and the re-read agrees with it. Nothing but the version
// number says a write was lost, which is why the number is checked: the
// version written must be the version read plus one.
func TestCredsSetDetectsAConcurrentWriteBeforeItsOwn(t *testing.T) {
	f := credsFixtureFor(t)
	first := credsValue(t, "first")
	ours := credsValue(t, "ours")
	other := credsValue(t, "other")
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+first+"\n"))

	// Another terminal's `creds set` writes its document inside the window,
	// through the fake's own hook on the add request: the fake's state changes
	// without a client call of this command's, so writes() below is still this
	// command's add alone.
	f.OnAddVersion = func(string) {
		f.AddVersion(f.id, []byte(config.AnthropicSecret+": "+other+"\n"))
	}

	out, errOut, err := executeStdin(t, ours+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil {
		t.Fatalf("a concurrent write that landed before this command's own was not refused: %q", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, want %d: %v", ExitCode(err), ExitUserError, err)
	}
	msg := err.Error()
	for _, want := range []string{"another write landed between this command's read and its write", "No version was disabled"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %s", want, msg)
		}
	}
	if n := f.writes(); n != 1 {
		t.Errorf("%d versions were written, want 1 (nothing is retried)", n)
	}
	// This command's document IS the newest version — it was written last — and
	// the other writer's entry is not in it. That is the lost update, and it is
	// why the re-read cannot be the thing that catches this.
	if got := f.value(t); !strings.Contains(got, ours) || strings.Contains(got, other) {
		t.Errorf("the newest version is %q, want this command's document and not the other writer's entry", got)
	}
	// Nothing was retired: the version this command read is still ENABLED, and
	// so is the one the other writer added. A disable here would be a disable
	// of a version this command did not verify, and the entry it lost would be
	// gone with it.
	if got := f.Disabled(); len(got) != 0 {
		t.Errorf("a lost update disabled %q, want nothing", got)
	}
	if leak := out + errOut + msg; strings.Contains(leak, ours) || strings.Contains(leak, other) || strings.Contains(leak, first) {
		t.Errorf("a value leaked: %q", leak)
	}
}

// The write is a read-modify-write of ONE document: the entry it was asked for
// changes, and every other entry keeps its bytes — its comments, its quoting,
// its block scalars, its order. A writer that rebuilt the document from a Go
// map would rewrite a credential nobody touched, which is the one thing a
// rotation must never do.
func TestCredsSetRoundTripsTheBundle(t *testing.T) {
	f := credsFixtureFor(t)
	existing := credsValue(t, "existing")
	quoted := credsValue(t, "quoted")
	pem := credsValue(t, "pem")
	ours := credsValue(t, "ours")
	f.seed(t, []byte("# the service's credentials\n"+
		config.ClaudeOAuthToken+": "+existing+"\n"+
		config.AnthropicSecret+": \""+quoted+"\"\n"+
		"claude-oauth-token-team: |\n  -----BEGIN FAKE-----\n  "+pem+"\n  -----END FAKE-----\n"))

	out, errOut, err := executeStdin(t, ours+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	got := f.value(t)
	for _, want := range []string{
		"# the service's credentials",
		config.ClaudeOAuthToken + ": " + ours,
		config.AnthropicSecret + ": \"" + quoted + "\"",
		"claude-oauth-token-team: |\n  -----BEGIN FAKE-----\n  " + pem + "\n  -----END FAKE-----",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the written document does not hold %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, existing) {
		t.Errorf("the value this command replaced is still in the document:\n%s", got)
	}
	// What was written is what the service's own loader reads: one schema, one
	// decoder, and no second opinion about the document's shape.
	b, err := credsvc.ParseBundle(f.Latest(f.id))
	if err != nil {
		t.Fatalf("the service's loader refuses the document this command wrote: %v", err)
	}
	if names := b.Names(); !slices.Equal(names, []string{config.DefaultClaudeAccount, config.AnthropicSecret, "team"}) {
		t.Errorf("the document holds %q", names)
	}
	if e, ok := b.Lookup(config.DefaultClaudeAccount); !ok || e.Value() != ours {
		t.Errorf("the default account's entry is %v, want the value just set", ok)
	}
	if leak := out + errOut; strings.Contains(leak, ours) || strings.Contains(leak, existing) || strings.Contains(leak, pem) {
		t.Errorf("a value leaked: %q", leak)
	}
}

// m4: a verified write DISABLES the version it replaced, and nothing else.
// A rotated-away bundle that stays enabled keeps every superseded credential
// readable, which is the one thing a rotation is for — and Terraform cannot do
// it, because it manages no version.
func TestCredsSetDisablesTheReplacedVersion(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, nil) // the container C1.18's Terraform creates: no version yet
	name := "projects/proj-1234/secrets/" + f.id + "/versions/"

	// The first write replaces nothing: a container C1.18 created holds no
	// version to retire.
	out, errOut, err := executeStdin(t, credsValue(t, "one")+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	if got := f.Disabled(); len(got) != 0 {
		t.Fatalf("the first write disabled %q, want nothing", got)
	}
	if !strings.Contains(out, "first version") {
		t.Errorf("the output does not say it was the first version: %q", out)
	}

	// The second replaces version 1, and names it.
	out, errOut, err = executeStdin(t, credsValue(t, "two")+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	if got := f.Disabled(); !slices.Equal(got, []string{name + "1"}) {
		t.Fatalf("Disabled = %q, want version 1 alone", got)
	}
	if !strings.Contains(out, "replaced version 1") || !strings.Contains(out, "now disabled") {
		t.Errorf("the output does not name the version it retired: %q", out)
	}
	if f.Deleted() != nil || f.Destroyed() != nil {
		t.Errorf("a rotation took something away: %q / %q", f.Deleted(), f.Destroyed())
	}

	// The third retires version 2 and leaves version 1 where it is: the
	// version retired is the one this write replaced, never the oldest, and
	// never all of them.
	if _, errOut, err = executeStdin(t, credsValue(t, "three")+"\n", "creds", "set", config.ClaudeOAuthToken); err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	if got := f.Disabled(); !slices.Equal(got, []string{name + "1", name + "2"}) {
		t.Fatalf("Disabled = %q, want versions 1 and 2", got)
	}
	// The version the write replaced is no longer readable, which is what the
	// disable is for: the credential it held is still in the container and
	// nothing can read it.
	// (AllowAccess lets the fake answer a value read at all; the command
	// itself never asks for one, which is what the rest of this file asserts.)
	f.AllowAccess()
	if err := accessVersion(t, f, "1"); err == nil {
		t.Error("a disabled version's value is still readable")
	}
}

// The retirement is best-effort and its failure is REPORTED, never swallowed:
// the new credential is live either way (the write was verified before the
// disable was attempted), so the command succeeds — with a warning that names
// the version still ENABLED and the command that retires it, because a
// superseded credential that stays readable is a leak, not a cosmetic problem.
func TestCredsSetReportsAFailedDisable(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, nil)
	if _, errOut, err := executeStdin(t, credsValue(t, "one")+"\n", "creds", "set", config.ClaudeOAuthToken); err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	f.FailDisableWith = http.StatusForbidden
	out, errOut, err := executeStdin(t, credsValue(t, "two")+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err != nil {
		t.Fatalf("a refused disable failed the command: %v (stderr %q)", err, errOut)
	}
	for _, want := range []string{"warning:", "still ENABLED", "gcloud secrets versions disable 1", "--secret=" + f.id} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the warning does not say %q: %q", want, errOut)
		}
	}
	if !strings.Contains(out, "still ENABLED") {
		t.Errorf("the output does not carry the warning's consequence: %q", out)
	}
	if got := f.Disabled(); len(got) != 0 {
		t.Errorf("Disabled = %q, want nothing (the call was refused)", got)
	}
}

// A name the bundle has no room for, and a document the loader would refuse,
// are each refused BEFORE anything is written — and the unknown name is
// refused before the cloud is even opened, because the argument position is
// one a pasted value can land in.
func TestCredsSetRefusesUnknownKeyAndOversizeDoc(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	value := credsValue(t, "ours")

	// The App key WAS refused here "until C2.1", and this list no longer
	// holds it: C2.1 landed and credsvc.CredentialKey answers KindGitHubApp
	// for it, so it is a bundle credential like any other and
	// TestCredsSetAcceptsTheAppKeyAsABundleEntry is where that is asserted.
	// What stays refused is a name no bundle key can be, whatever it looks
	// like.
	for _, name := range []string{
		"not-a-credential",
		// The namespace is reserved but the suffix is not a name any policy
		// can admit: a key no account can be named.
		config.ClaudeOAuthTokenPrefix + strings.Repeat("a", config.MaxClaudeAccountNameLen+1),
	} {
		_, _, err := executeStdin(t, value+"\n", "creds", "set", name)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("creds set %q = %v, want a user error", name, err)
		}
		if strings.Contains(err.Error(), name) {
			t.Errorf("the refusal echoes the name: %v", err)
		}
		if !strings.Contains(err.Error(), "Nothing was read and nothing was written") {
			t.Errorf("the refusal does not say nothing happened: %v", err)
		}
	}
	if n := len(f.Requests()); n != 0 {
		t.Fatalf("%d request(s) were made for a refused name: the refusal is not before the cloud", n)
	}

	// An oversize value: over the loader's per-credential bound, which is the
	// bound the service enforces, so it is refused here rather than stored and
	// refused there.
	big := strings.Repeat("a", credsvc.MaxBundleValueBytes+1)
	_, _, err := executeStdin(t, big+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("an oversize value = %v, want a refusal", err)
	}
	// The refusal names the bound the VALUE broke, not the document's: the two
	// are different limits, and the one an operator has to be told about is the
	// one their value is over (the document bound is the same code path's
	// answer for a document, below).
	if !strings.Contains(err.Error(), "per-credential limit") {
		t.Errorf("the refusal does not name the per-credential bound: %v", err)
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written for an oversize value", n)
	}

	// An oversize DOCUMENT: every entry is within its own bound and the
	// document they make together is over the loader's. The write is refused
	// because the loader would refuse what it produced — one schema, one
	// answer, whichever half of the read-modify-write is too big.
	var doc bytes.Buffer
	for _, key := range []string{config.ClaudeOAuthToken, config.AnthropicSecret, "claude-oauth-token-a1", "claude-oauth-token-a2", "claude-oauth-token-a3", "claude-oauth-token-a4", "claude-oauth-token-a5"} {
		fmt.Fprintf(&doc, "%s: %s\n", key, strings.Repeat("b", credsvc.MaxBundleValueBytes))
	}
	if doc.Len() > credsvc.MaxBundleBytes {
		t.Fatalf("the fixture document is already over the bound: %d bytes", doc.Len())
	}
	f.Seed(f.id, map[string]string{"fugaro": "managed"}, doc.Bytes())
	_, _, err = executeStdin(t, strings.Repeat("c", credsvc.MaxBundleValueBytes)+"\n", "creds", "set", "claude-oauth-token-a6")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("an oversize document = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "not a document the credentials service accepts") {
		t.Errorf("the refusal does not name the schema: %v", err)
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written for an oversize document", n)
	}
	if got := f.value(t); got != doc.String() {
		t.Errorf("the document changed: %q", got)
	}
}

// The App key is a bundle credential, and it is the ONE entry whose value is
// not one line: C2.1 made credsvc.CredentialKey answer KindGitHubApp for
// github-app-key (the service signs its installation-token mints with it), and
// the mint parses that entry with github.ParsePrivateKey — a PEM, many lines.
// So `creds set github-app-key` takes the value the way the mint can use it:
// a real multi-line key, piped in on stdin, written as any other entry (one
// version, the other entries' bytes kept, the document still one the service's
// loader accepts) and parsed back out by the mint's own parser. Nothing of it
// is printed.
//
// The value is a throwaway RSA key generated here, never a key of anyone's
// (Convention 2: no secret-shaped literal).
func TestCredsSetAcceptsTheAppKeyAsABundleEntry(t *testing.T) {
	f := credsFixtureFor(t)
	existing := credsValue(t, "existing")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	value := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+existing+"\n"))

	// stdin, byte for byte as a key file arrives: the PEM and the one newline
	// the file ends with, which the command drops and says it dropped.
	out, errOut, err := executeStdin(t, string(value), "creds", "set", config.GitHubAppKeySecret)
	if err != nil {
		t.Fatalf("creds set %s = %v (stderr %q)", config.GitHubAppKeySecret, err, errOut)
	}
	if !strings.Contains(errOut, "dropped the value's trailing newline") {
		t.Errorf("the dropped trailing newline was not said: %q", errOut)
	}
	if n := f.writes(); n != 1 {
		t.Fatalf("%d version(s) written, want 1", n)
	}
	doc := f.Latest(f.id)
	b, err := credsvc.ParseBundle(doc)
	if err != nil {
		t.Fatalf("the service's loader refuses the document this command wrote: %v", err)
	}
	e, ok := b.AppKey()
	if !ok {
		t.Fatal("the document holds no App key entry")
	}
	// Stored as the key that came in, minus the one trailing newline: the
	// document is the loader's, and the loader's value is what the mint reads.
	if want := strings.TrimSuffix(string(value), "\n"); e.Value() != want {
		t.Errorf("the stored App key is not the key that was piped in (%d bytes stored, %d piped)", len(e.Value()), len(want))
	}
	// And the mint can use it: the service's own parser, on the value the
	// document holds, gives back the key this test generated. This is the
	// assertion the whole test is for — a value this command accepts is one
	// the mint signs with, and the two cannot drift because the parser is the
	// same function (internal/credsvc/github.go's appKey).
	got, err := github.ParsePrivateKey([]byte(e.Value()))
	if err != nil {
		t.Fatalf("the mint's own parser refuses the App key this command stored: %v", err)
	}
	if !got.Equal(key) {
		t.Error("the mint's parser read a different key than the one piped in")
	}
	// The document is one block scalar holding the whole key, not a one-line
	// value with the PEM's first line in it: every line of the body is there.
	for _, line := range strings.Split(strings.TrimSuffix(string(value), "\n"), "\n") {
		if !strings.Contains(string(doc), line) {
			t.Errorf("the document does not hold the key's line %q", line)
		}
	}
	if e, ok := b.Lookup(config.DefaultClaudeAccount); !ok || e.Value() != existing {
		t.Errorf("the default account's entry is %v, want the bytes it already had", ok)
	}
	// Nothing of the value is printed. The scan is over the key's whole lines
	// rather than over every 3-byte window the way assertNoValue scans a
	// fixture value: a base64 body draws from an alphabet with lower-case
	// letters in it, so a three-byte window of it collides with ordinary
	// prose and a match would prove nothing. A whole base64 line of a
	// 2048-bit key is 64 bytes and cannot collide, and the PEM's own two
	// markers are fixed text a leak of this value would carry.
	for _, line := range strings.Split(strings.TrimSuffix(string(value), "\n"), "\n") {
		if strings.Contains(out+errOut, line) {
			t.Fatalf("a line of the App key was printed: %q", out+errOut)
		}
	}
	if strings.Contains(out+errOut, "PRIVATE KEY") {
		t.Fatalf("the value's shape was printed: %q", out+errOut)
	}
}

// The App key's shape is enforced in both directions: a value that is not a
// PEM is refused for that name (the rule before this change accepted a single
// line and left the mint to refuse it later, so the operator learned about it
// from the service and not from the command), and a multi-line value is still
// refused for every OTHER name — the one exception is the App key and nothing
// else. Every refusal names the class and repeats no byte of the value.
func TestCredsSetRefusesTheWrongShapeForTheKind(t *testing.T) {
	// secret is the part of the value that must not reach any answer: a
	// fixture value (whose alphabet is upper-case on purpose, so assertNoValue
	// can scan it in 3-byte windows) or one whole base64 line of a PEM's body.
	// The keys are generated once, here, so that the value a case sends and
	// the line it looks for are the same key's.
	ec, first, second := ecKeyPEM(t), appKeyPEM(t), appKeyPEM(t)
	cases := []struct {
		name   string
		entry  string
		value  string
		secret string
		want   string
	}{
		{
			// A single line is not a PEM block at all: refused before the
			// write, by this command, rather than by the mint after it.
			name:   "a single-line value for the App key",
			entry:  config.GitHubAppKeySecret,
			value:  credsValue(t, "oneline"),
			secret: credsValue(t, "oneline"),
			want:   "must be exactly one PEM block",
		},
		{
			name:  "an EC key for the App key",
			entry: config.GitHubAppKeySecret, value: ec, secret: pemBody(t, ec),
			want: "not a private key the credentials service can use",
		},
		{
			name:  "a PEM with a line of text before it",
			entry: config.GitHubAppKeySecret, value: "the key I downloaded:\n" + first, secret: pemBody(t, first),
			want: "must be exactly one PEM block",
		},
		{
			name:  "a PEM with a second block after it",
			entry: config.GitHubAppKeySecret, value: first + second, secret: pemBody(t, second),
			want: "must be exactly one PEM block",
		},
		{
			// pem.Decode is what pairs the two marker lines: an END line that
			// does not repeat its BEGIN line's type is not a block at all, so
			// the value is refused by the shape rule and not by the parser.
			name:   "a PEM whose END line does not match its BEGIN line",
			entry:  config.GitHubAppKeySecret,
			value:  strings.Replace(first, "-----END RSA PRIVATE KEY-----", "-----END PRIVATE KEY-----", 1),
			secret: pemBody(t, first),
			want:   "must be exactly one PEM block",
		},
		{
			name:   "a multi-line value for a model credential",
			entry:  config.ClaudeOAuthToken,
			value:  credsValue(t, "first") + "\n" + credsValue(t, "second"),
			secret: credsValue(t, "first"),
			want:   "spans several lines",
		},
		{
			name:   "a multi-line value for a named account",
			entry:  config.ClaudeOAuthTokenPrefix + "alice",
			value:  credsValue(t, "first") + "\n" + credsValue(t, "second"),
			secret: credsValue(t, "second"),
			want:   "spans several lines",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := credsFixtureFor(t)
			existing := credsValue(t, "existing")
			f.seed(t, []byte(config.ClaudeOAuthToken+": "+existing+"\n"))
			out, errOut, err := executeStdin(t, tc.value+"\n", "creds", "set", tc.entry)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("creds set %s = %v, want a user error", tc.entry, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is %q, want it to say %q", err, tc.want)
			}
			// The App key comes in on stdin, and every refusal of it says so:
			// the hidden prompt reads one line and cannot take a paste.
			if tc.entry == config.GitHubAppKeySecret && !strings.Contains(err.Error(), "< PATH-TO-THE-KEY-FILE") {
				t.Errorf("the refusal does not say the App key comes in on stdin: %q", err)
			}
			// The refusal is about the value's shape and never about its
			// content, and the class it names is the same one the service's
			// mint would refuse with.
			if n := f.writes(); n != 0 {
				t.Fatalf("%d version(s) written for a refused value", n)
			}
			if got := f.value(t); got != config.ClaudeOAuthToken+": "+existing+"\n" {
				t.Errorf("the document changed: %q", got)
			}
			assertNoValue(t, out+errOut+err.Error(), tc.secret)
		})
	}
}

// pemBody is one whole base64 line of a PEM's body: the part of a key that is
// unique to it, and long enough that finding it in an answer is a leak and not
// a coincidence of the alphabet.
func pemBody(t *testing.T, text string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if line != "" && !strings.HasPrefix(line, "-----") {
			return line
		}
	}
	t.Fatalf("the PEM holds no body line")
	return ""
}

// ecKeyPEM is a real EC private key in a PEM, the shape that is one PEM block
// of base64 and still not the App's key: the mint parses with
// github.ParsePrivateKey, which is RSA only, so this is refused by the same
// code that would refuse it at the service.
func ecKeyPEM(t *testing.T) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// A value in argv is not read, and neither is one in the environment: the
// command has one input, stdin (or the terminal), and the two places a value
// is most likely to be pasted into by mistake are refused rather than used.
func TestCredsSetValueNeverFromArgvOrEnv(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	value := credsValue(t, "argv")

	// argv, in the position a NAME goes in and after it: the first is an
	// unknown name, the second is a value where the command takes none.
	for _, args := range [][]string{
		{"creds", "set", value},
		{"creds", "set", config.ClaudeOAuthToken, value},
	} {
		out, errOut, err := executeStdin(t, "", args...)
		if err == nil {
			t.Fatalf("%v was accepted", args)
		}
		if leak := out + errOut + err.Error(); strings.Contains(leak, value) {
			t.Fatalf("the value in argv was echoed back: %q", leak)
		}
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written from argv", n)
	}

	// A value-shaped flag: pflag quotes an unknown flag back, which is why
	// every creds command sets its own flag error. The value is not echoed,
	// and nothing is written.
	out, errOut, err := execute(t, "creds", "set", config.ClaudeOAuthToken, "--"+value)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a value-shaped flag = %v, want a refusal", err)
	}
	if leak := out + errOut + err.Error(); strings.Contains(leak, value) {
		t.Fatalf("the value-shaped flag was echoed back: %q", leak)
	}
	if !strings.Contains(err.Error(), "unknown or malformed flag") {
		t.Errorf("the flag error is not the command's own: %v", err)
	}

	// The environment: a value exported as FUGARO_… is not read either. The
	// command still wants stdin, and says so.
	t.Setenv("FUGARO_CREDS_VALUE", value)
	out, errOut, err = executeStdin(t, "", "creds", "set", config.ClaudeOAuthToken)
	if err == nil || !strings.Contains(err.Error(), "the value is empty") {
		t.Fatalf("an empty stdin with the value in the environment = %v, want the empty-value refusal", err)
	}
	if leak := out + errOut + err.Error(); strings.Contains(leak, value) {
		t.Fatalf("the environment's value was echoed back: %q", leak)
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written from the environment", n)
	}
}

// creds ls takes no argument, and cobra's own answer to one — `unknown command
// "<value>" for "fugaro creds ls"` — is the argument quoted back. The argument
// position is one a pasted value lands in, and this command may not print one
// (its own help says so), so it refuses without echoing, before anything is
// read and before the cloud is opened. The container is never touched: the
// fixture would fail the test on any request it did not expect, and the
// assertion below says so.
func TestCredsLsNeverEchoesAnArgument(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	value := credsValue(t, "lsarg")

	for _, args := range [][]string{
		{"creds", "ls", value},
		{"creds", "ls", value, value},
	} {
		out, errOut, err := execute(t, args...)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("%v = %v, want a refusal", args, err)
		}
		if leak := out + errOut + err.Error(); strings.Contains(leak, value) {
			t.Fatalf("the argument was echoed back: %q", leak)
		}
		if !strings.Contains(err.Error(), "takes no argument") {
			t.Errorf("%v: the refusal is not the command's own: %v", args, err)
		}
	}
	if n := f.reads(); n != 0 {
		t.Fatalf("an argument was refused only after %d read(s) of the bundle", n)
	}
}

// Owner-run only, and the refusal comes first: before anything is read and
// before the cloud is opened, in a coding agent's session and in a run's own
// process. Nothing of the bundle — not even its existence — reaches either.
func TestCredsSetRefusedInAgentSession(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	t.Setenv("CLAUDECODE", "1")

	_, _, err := executeStdin(t, credsValue(t, "ours")+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("creds set in an agent's session = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "nothing was written") || !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker: %v", err)
	}
	_, _, err = execute(t, "creds", "ls")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was read") {
		t.Fatalf("creds ls in an agent's session = %v, want a refusal", err)
	}
	if n := len(f.Requests()); n != 0 {
		t.Fatalf("%d request(s) were made from an agent's session", n)
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) were written from an agent's session", n)
	}
}

func TestCredsSetRefusedInRunProcess(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	t.Setenv("FUGARO_RUN", "aurora/run-1")

	_, _, err := executeStdin(t, credsValue(t, "ours")+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("creds set in a run's process = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "nothing was written") || !strings.Contains(err.Error(), "FUGARO_RUN") {
		t.Errorf("the refusal does not name the marker: %v", err)
	}
	_, _, err = execute(t, "creds", "ls")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was read") {
		t.Fatalf("creds ls in a run's process = %v, want a refusal", err)
	}
	if n := len(f.Requests()); n != 0 {
		t.Fatalf("%d request(s) were made from a run's process", n)
	}
}

// The listing is names and metadata only. The fixture's values would be
// visible if they were printed, and the assertion is over every substring of
// them long enough to narrow a guess: not the value, not a prefix of it, not a
// length.
func TestCredsLsPrintsNamesAndMetadataOnly(t *testing.T) {
	f := credsFixtureFor(t)
	values := []string{credsValue(t, "oauth"), credsValue(t, "anthropic"), credsValue(t, "team")}
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+values[0]+"\n"+
		config.AnthropicSecret+": "+values[1]+"\n"+
		"claude-oauth-token-team: "+values[2]+"\n"))

	out, errOut, err := execute(t, "creds", "ls")
	if err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	for _, want := range []string{config.ClaudeOAuthToken, config.AnthropicSecret, "claude-oauth-token-team", "PRESENT", "LAST-CHANGED"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not hold %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, f.id+" (project aurora), version 1, last changed ") {
		t.Errorf("the header does not name the container, the project and the version:\n%s", out)
	}
	if n := strings.Count(out, "yes"); n != 3 {
		t.Errorf("%d rows are present, want 3:\n%s", n, out)
	}
	assertNoValue(t, out+errOut, values...)

	// The same listing as JSON: the same shape, the same names, and still no
	// value in it.
	out, errOut, err = execute(t, "creds", "ls", "--json")
	if err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	var doc credsList
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.Project != "aurora" || doc.Secret != f.id || doc.Version != "1" || doc.Changed == "" {
		t.Errorf("json = %+v", doc)
	}
	var names []string
	for _, r := range doc.Credentials {
		if !r.Present || r.Changed != doc.Changed {
			t.Errorf("row %+v", r)
		}
		names = append(names, r.Name)
	}
	if want := []string{config.AnthropicSecret, config.ClaudeOAuthToken, "claude-oauth-token-team"}; !slices.Equal(names, want) {
		t.Errorf("names = %q, want %q", names, want)
	}
	assertNoValue(t, out+errOut, values...)
}

// The plan's name for this test, read as what it can only mean: the --json
// shape never carries a value or a prefix of one. A JSON field is where a
// value would most easily ride along unnoticed — a struct field added later
// prints itself — so the shape is asserted field by field as well as byte by
// byte, and Secret Manager is asked for exactly the two things the listing
// needs: the version list (metadata) and the newest version's document.
func TestCredsLsNeverTouchesSecretManagerInJSONShape(t *testing.T) {
	f := credsFixtureFor(t)
	value := credsValue(t, "only")
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+value+"\n"))

	out, errOut, err := execute(t, "creds", "ls", "--json")
	if err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	var keys []string
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"changed", "credentials", "project", "secret", "version"}; !slices.Equal(keys, want) {
		t.Errorf("the JSON shape holds %q, want %q", keys, want)
	}
	rows, ok := fields["credentials"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("credentials = %v", fields["credentials"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("row = %v", rows[0])
	}
	var rowKeys []string
	for k := range row {
		rowKeys = append(rowKeys, k)
	}
	slices.Sort(rowKeys)
	if want := []string{"changed", "name", "present"}; !slices.Equal(rowKeys, want) {
		t.Errorf("a row holds %q, want %q", rowKeys, want)
	}
	assertNoValue(t, out+errOut, value)
	// The two reads the listing makes and no more: the container, the version
	// list and the newest version's document. A listing that read every
	// version would be reading values it has no use for.
	accesses := 0
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, ":access") {
			accesses++
		}
	}
	if accesses != 1 {
		t.Errorf("%d version reads, want 1", accesses)
	}
	if f.reads() != 1 {
		t.Errorf("%d container reads, want 1", f.reads())
	}
	if f.writes() != 0 {
		t.Errorf("the listing wrote %d version(s)", f.writes())
	}
}

// An unreadable, absent, empty or unparseable bundle is a NAMED failure and
// never an empty list: a listing that answered nothing for any of them would
// read as "the bundle holds no credential" while the service is in fact
// issuing from one this command could not read (fugaro accounts ls's
// Complete: false precedent).
func TestCredsLsRefusesAnUnreadableBundle(t *testing.T) {
	value := credsValue(t, "hidden")

	// The container is not there at all: the installation has not applied the
	// credentials service.
	f := credsFixtureFor(t)
	_, _, err := execute(t, "creds", "ls")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is not in project proj-1234") {
		t.Fatalf("an absent container = %v, want a named refusal", err)
	}
	if !strings.Contains(err.Error(), f.id) {
		t.Errorf("the refusal does not name the container: %v", err)
	}

	// The container is there with no version: nothing has been set yet. That
	// is a named refusal for the listing (there is no listing) and the
	// legitimate starting point for the first write.
	f.seed(t, nil)
	_, _, err = execute(t, "creds", "ls")
	if err == nil || !strings.Contains(err.Error(), "holds no version") {
		t.Fatalf("a container with no version = %v, want a named refusal", err)
	}
	if _, errOut, err := executeStdin(t, credsValue(t, "first")+"\n", "creds", "set", config.ClaudeOAuthToken); err != nil {
		t.Fatalf("the first write into an empty container failed: %v (stderr %q)", err, errOut)
	}
	if _, err := credsvc.ParseBundle(f.Latest(f.id)); err != nil {
		t.Fatalf("the first write is not a document the loader reads: %v", err)
	}

	// A body that is not a document the loader accepts — and which holds a
	// value, because a hand-edited document is where a value ends up in the
	// wrong place. The refusal names the class and the key, never the value.
	f.seed(t, []byte(config.ClaudeOAuthToken+": ["+value+"]\n"))
	out, errOut, err := execute(t, "creds", "ls")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not a document the credentials service accepts") {
		t.Fatalf("an unparseable bundle = %v, want a named refusal", err)
	}
	assertNoValue(t, out+errOut+err.Error(), value)

	// The newest version is DISABLED: the document is there and Secret
	// Manager will not serve it. That is not "no credential" either.
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+value+"\n"))
	f.DisableVersions(f.id)
	out, errOut, err = execute(t, "creds", "ls")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was read") ||
		!strings.Contains(err.Error(), "gcloud secrets versions enable") {
		t.Fatalf("a disabled bundle = %v, want the named refusal", err)
	}
	assertNoValue(t, out+errOut+err.Error(), value)

	// And `creds set` refuses it too, rather than editing a document it could
	// not read: a write on top of an unseen document would drop every
	// credential in it. The refusal is the named one — it says nothing was
	// written and names the owner's way back in — and not merely any error that
	// happens to contain the word DISABLED.
	writes := f.writes()
	out, errOut, err = executeStdin(t, credsValue(t, "ours")+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was written") ||
		!strings.Contains(err.Error(), "gcloud secrets versions enable") {
		t.Fatalf("creds set on a disabled bundle = %v, want the named refusal", err)
	}
	assertNoValue(t, out+errOut+err.Error(), value)
	if n := f.writes(); n != writes {
		t.Fatalf("%d version(s) were written over a document that could not be read", n-writes)
	}
}

// The redaction path: a server that echoes back what it was sent — the
// document, raw or base64 — must not get a value onto the operator's terminal
// or into an error. The fake has one hook that echoes a payload
// (EchoAddVersionPayload) and the re-read has a seam, so both halves of the
// write are exercised, and `creds ls` is exercised through a document the
// loader refuses that holds a value in the wrong place.
//
// The document is the payload, and the document holds EVERY credential in the
// bundle: the credential being set is the easy half. The fixture seeds a
// second one — the project's Anthropic API key — and it is the value that
// would leak, because the command holds it (it parsed the document it is about
// to rewrite) and a server that repeats its request body hands the whole thing
// back. Both echoes are the real bytes: the fake repeats the payload it
// received, so nothing here is a guess at what the command sends.
func TestCredsCommandsNeverLogAValue(t *testing.T) {
	f := credsFixtureFor(t)
	value := credsValue(t, "echoed")
	other := credsValue(t, "other")
	f.seed(t, []byte(config.AnthropicSecret+": "+other+"\n"+config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))

	// The write's own answer: a server that repeats the payload it received,
	// decoded and as it was sent. The other credential is in it either way.
	f.EchoAddVersionPayload = true
	out, errOut, err := executeStdin(t, value+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil {
		t.Fatal("a refused write succeeded")
	}
	if !strings.Contains(err.Error(), "the payload was") {
		t.Fatalf("the fake's echo did not reach the error, so this proves nothing: %v", err)
	}
	assertNoValue(t, out+errOut+err.Error(), value, other)
	assertRedacted(t, out+errOut+err.Error(), value, other)

	// The read-back's answer: a server that echoes the document it served.
	// The seam runs the real read first, so the bytes echoed are the document
	// the command just wrote and verified — not a copy the test keeps.
	f.EchoAddVersionPayload = false
	restore := credsWriteBack
	t.Cleanup(func() { credsWriteBack = restore })
	credsWriteBack = func(ctx context.Context, s *bundleStore) (bundleDoc, error) {
		back, err := restore(ctx, s)
		if err != nil {
			return bundleDoc{}, err
		}
		return bundleDoc{}, errors.New("the server echoed " + string(back.raw) + " and " + base64.StdEncoding.EncodeToString(back.raw))
	}
	out, errOut, err = executeStdin(t, value+"\n", "creds", "set", config.ClaudeOAuthToken)
	if err == nil {
		t.Fatal("a write whose read-back failed succeeded")
	}
	if !strings.Contains(err.Error(), "the server echoed") {
		t.Fatalf("the seam's echo did not reach the error, so this proves nothing: %v", err)
	}
	assertNoValue(t, out+errOut+err.Error(), value, other)
	assertRedacted(t, out+errOut+err.Error(), value, other)

	// The listing's answer: a document that is not one the loader accepts and
	// that holds the value where a name belongs.
	credsWriteBack = restore
	f.seed(t, []byte(value+": "+value+"\n"))
	out, errOut, err = execute(t, "creds", "ls")
	if err == nil {
		t.Fatal("a bundle holding a value as a key was listed")
	}
	assertNoValue(t, out+errOut+err.Error(), value)
}

// assertRedacted fails if s still carries a secret the way the production
// redactor would catch it — the raw bytes, the JSON escape, a line of a
// multi-line secret, and the encoded forms (the base64 cores of a secret of at
// least encodedMin bytes, which is how an unaligned base64 stream is read).
// assertNoValue is the hand-written half and this is the redactor's own notion
// of a form; the two disagreeing is the interesting failure.
func assertRedacted(t *testing.T, s string, secrets ...string) {
	t.Helper()
	if got := agent.Redact(s, secrets); got != s {
		t.Fatalf("the text still carries a secret the redactor would remove: %q -> %q", s, got)
	}
}

// `creds set --json` is the shape an owner's script reads, and it is pinned the
// way `secrets set --json`'s is: every field named, the COUNT of them fixed —
// a field added later prints itself unless a test counts — and no field
// carrying a value or anything derived from one. The count differs by case and
// both are pinned here: a first write has nothing to replace, so `replaced` is
// absent (omitempty) rather than empty.
func TestCredsSetJSONShape(t *testing.T) {
	f := credsFixtureFor(t)
	first := credsValue(t, "first")
	value := credsValue(t, "json")
	f.seed(t, nil) // the container, with no version: the first write's starting point

	fields := func(t *testing.T, out string) (map[string]json.RawMessage, string, string, string, string, bool) {
		t.Helper()
		var js map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &js); err != nil {
			t.Fatalf("the output is not a JSON object: %s", out)
		}
		var got struct {
			Project  string `json:"project"`
			Secret   string `json:"secret"`
			Version  string `json:"version"`
			Replaced string `json:"replaced"`
			Disabled bool   `json:"disabled"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		return js, got.Project, got.Secret, got.Version, got.Replaced, got.Disabled
	}

	// The first write: no version was replaced, so there is no replaced field.
	out, errOut, err := executeStdin(t, first+"\n", "creds", "set", config.ClaudeOAuthToken, "--json")
	if err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	js, project, secret, version, replaced, disabled := fields(t, out)
	if want := []string{"disabled", "project", "secret", "version"}; !slices.Equal(slices.Sorted(maps.Keys(js)), want) {
		t.Errorf("the first write's fields are %v, want %v", slices.Sorted(maps.Keys(js)), want)
	}
	if project != "aurora" || secret != f.id || version != "1" || replaced != "" || disabled {
		t.Errorf("the first write's json = %s", out)
	}

	// The second: the version it replaced, and that it was disabled.
	out, errOut, err = executeStdin(t, value+"\n", "creds", "set", config.ClaudeOAuthToken, "--json")
	if err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut)
	}
	js, project, secret, version, replaced, disabled = fields(t, out)
	if want := []string{"disabled", "project", "replaced", "secret", "version"}; !slices.Equal(slices.Sorted(maps.Keys(js)), want) {
		t.Errorf("the fields are %v, want %v (a field added later prints itself unless this test counts)", slices.Sorted(maps.Keys(js)), want)
	}
	if project != "aurora" || secret != f.id || version != "2" || replaced != "1" || !disabled {
		t.Errorf("json = %s", out)
	}
	// The value is nowhere in either shape, and neither is a length or a
	// prefix of one.
	assertNoValue(t, out+errOut, value, first)
}

// credsChunkBytes is the length of a middle run of a value that counts as a
// leak. Four bytes is a prefix's length (minSecretBytes, the redaction floor
// the CLI already uses: a truncated line shows a prefix, and a four-byte
// prefix already narrows a guess), but four bytes anywhere is too short to
// mean anything in a document of words — "NAME" is both a column header and a
// run of a test's name. Eight is long enough that a match is a credential's
// bytes and not a word the output happens to share.
const credsChunkBytes = 8

// assertNoValue fails if any of the values appears in s, or any run of one of
// at least credsChunkBytes bytes. The whole-value check covers every prefix of
// a value (a truncated line shows a prefix, which is the whole value's own
// beginning), and the run check covers a message that quoted a piece of what
// it was sent.
func assertNoValue(t *testing.T, s string, values ...string) {
	t.Helper()
	for _, v := range values {
		if strings.Contains(s, v) {
			t.Fatalf("a value leaked (%d bytes): %q", len(v), s)
		}
		for i := 0; i+credsChunkBytes <= len(v); i++ {
			if got := v[i : i+credsChunkBytes]; strings.Contains(s, got) {
				t.Fatalf("a value leaked (%d bytes of one, %q): %q", credsChunkBytes, got, s)
			}
		}
	}
}

// accessVersion reads a version of the bundle the way the service does. It is
// what makes the disable observable and not only recorded: a version this
// command retired must not be readable any more.
func accessVersion(t *testing.T, f *credsFixture, version string) error {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.URL+"/v1/projects/proj-1234/secrets/"+f.id+"/versions/"+version+":access", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("access version %s: %d", version, resp.StatusCode)
	}
	return nil
}
