package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/streams"
)

// This file is T11 of docs/bitbucket-token-types: `fugaro creds set
// bitbucket-token--<name>`. What it pins is the order the acceptance states —
// the serving-release guard, then the record's own facts, then the value, then
// the check, and only then the three writes — and the two things that order is
// for: a token the fake Bitbucket refuses is written NOWHERE (the red step), and
// no refusal, no line and no error carries a byte of a value.

// bitbucketToken is the value this file's commands read. It is plain words and
// not a token-shaped string: a fixture carries no secret-shaped literal, and the
// tests that look for it in every output need it to be findable.
const bitbucketTokenValue = "plain-words-not-a-secret"

// bitbucketReply is one answer the fake bitbucket.org gives.
type bitbucketReply struct {
	status int
	body   string
}

// bitbucketSite stands in for api.bitbucket.org. Every call the CLI's check
// makes arrives through the transport below, which records where it was
// addressed and refuses a host that is not Bitbucket's own API root — so F22 is
// pinned here too: the operator's token leaves this process only towards the
// address the adapter's constant names, and a check that reached anywhere else
// is seen and failed.
type bitbucketSite struct {
	t       *testing.T
	srv     *httptest.Server
	answers map[string]bitbucketReply

	mu   sync.Mutex
	seen []string
}

func newBitbucketSite(t *testing.T, answers map[string]bitbucketReply) *bitbucketSite {
	t.Helper()
	f := &bitbucketSite{t: t, answers: answers}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *bitbucketSite) serve(w http.ResponseWriter, r *http.Request) {
	a, ok := f.answers[r.Method+" "+r.URL.Path]
	if !ok {
		f.t.Errorf("the check asked the fake Bitbucket for %s %s, which it has no answer for", r.Method, r.URL.Path)
		http.Error(w, "no answer", http.StatusInternalServerError)
		return
	}
	if a.body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(a.status)
	_, _ = w.Write([]byte(a.body))
}

func (f *bitbucketSite) record(host, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, host+" "+path)
}

// paths is every call the check made, as "host path".
func (f *bitbucketSite) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

// install points the CLI's check at this fake: the verifier itself is the real
// one, and the only thing stood in for is the client it reads with — which is
// the seam F22 leaves, since the check has no base URL to set.
func (f *bitbucketSite) install(t *testing.T) {
	t.Helper()
	base, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatalf("the fixture's URL is not a URL: %v", err)
	}
	restore := bitbucketCheck
	bitbucketCheck = func() *bbtokens.Verifier {
		return &bbtokens.Verifier{Client: &http.Client{Transport: &bitbucketTransport{base: base, site: f}}}
	}
	t.Cleanup(func() { bitbucketCheck = restore })
}

type bitbucketTransport struct {
	base *url.URL
	site *bitbucketSite
}

func (h *bitbucketTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	h.site.record(r.URL.Host, r.URL.Path)
	if r.URL.Scheme != "https" || r.URL.Host != "api.bitbucket.org" {
		return nil, fmt.Errorf("the check addressed %s://%s, and Bitbucket's API root is https://api.bitbucket.org alone", r.URL.Scheme, r.URL.Host)
	}
	c := r.Clone(r.Context())
	c.URL.Scheme, c.URL.Host, c.Host = h.base.Scheme, h.base.Host, h.base.Host
	return http.DefaultTransport.RoundTrip(c)
}

// repoTokenAnswers is a repository token's whole check: the repository read,
// the pull-request read inside it, and the identity.
func repoTokenAnswers() map[string]bitbucketReply {
	return map[string]bitbucketReply{
		"GET /2.0/repositories/acme/fugaro":              {status: 200, body: `{"full_name":"acme/fugaro","project":{"uuid":"{proj-1}"}}`},
		"GET /2.0/repositories/acme/fugaro/pullrequests": {status: 200, body: `{"values":[]}`},
		"GET /2.0/user": {status: 200, body: `{"uuid":"{aaaa-2222}","kind":"repository_access_token"}`},
	}
}

// projectTokenAnswers is a project token's, keyed by its KEY: the project read
// that resolves the key to the UUID, the project's repository listing, and the
// identity.
func projectTokenAnswers() map[string]bitbucketReply {
	return map[string]bitbucketReply{
		"GET /2.0/workspaces/acme/projects/FUG": {status: 200, body: `{"uuid":"{proj-1}"}`},
		"GET /2.0/repositories/acme":            {status: 200, body: `{"values":[{"project":{"uuid":"{proj-1}"}}]}`},
		"GET /2.0/user":                         {status: 200, body: `{"uuid":"{aaaa-3333}","kind":"project_access_token"}`},
	}
}

// bitbucketFixture is credsFixtureFor with the two backends a typed token's
// write needs and the fake bitbucket.org it is checked against: a budget
// database for the creds state store, and the check's own client.
type bitbucketFixture struct {
	*credsFixture
	db   *gcpfake.RTDB
	site *bitbucketSite
	skew *runServiceSkew
	// path is the project config file, which the record is written to.
	path string
}

// newBitbucketFixture is the fixture of every test below. serving is the
// release of the credentials service's serving revision, or "" for an
// installation with no service deployed and no pin — the state whose guard has
// nothing to judge, which is what keeps most of these tests about the token.
func newBitbucketFixture(t *testing.T, serving string, answers map[string]bitbucketReply) *bitbucketFixture {
	t.Helper()
	skew := newRunServiceSkew(t)
	if serving != "" {
		skew.deployedService(credentialsImage(serving), credentialsImage(serving))
	}
	f, _ := credsSkewFixture(t, "none", skew)
	db := gcpfake.NewRTDB(t)
	path := os.Getenv("FUGARO_CONFIG")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	budgetYAML := "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + db.URL + " }\n"
	if err := os.WriteFile(path, append(b, []byte(budgetYAML)...), 0o600); err != nil {
		t.Fatal(err)
	}
	db.Set(budget.PathProject, "aurora")
	site := newBitbucketSite(t, answers)
	site.install(t)
	return &bitbucketFixture{credsFixture: f, db: db, site: site, skew: skew, path: path}
}

// configBytes is the project config file as it stands, which is what a record
// is written into.
func (f *bitbucketFixture) configBytes(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// records is the config's bitbucket_tokens block, read back through the same
// parser the command writes with.
func (f *bitbucketFixture) records(t *testing.T) []config.BitbucketToken {
	t.Helper()
	lc, err := localcfg.Parse([]byte(f.configBytes(t)))
	if err != nil {
		t.Fatalf("the project config this command wrote is one the config's own reader refuses: %v", err)
	}
	return lc.BitbucketTokens
}

// status is the token's node in the credential state store, or nil when the
// store holds none. The path is built from the DOCUMENT KEY the caller passes
// (bitbucket-token--<name>) and not from a bare name: the store is keyed the
// way the node's identity set is (R5), so a mark stored under "ci" would be
// read as the model account ci.
func (f *bitbucketFixture) status(t *testing.T, entry string) map[string]any {
	t.Helper()
	v, _ := f.db.Value(creds.PathTokenStatus(entry)).(map[string]any)
	return v
}

// bundleEntryValue is the value the bundle document holds under the family entry
// key, or a fatal when the document holds no such entry. It reads the family's
// own accessor and not Lookup, because the family's keys are the document keys
// (R5) while Lookup answers by the name a model credential is spoken of by.
func bundleEntryValue(t *testing.T, b *credsvc.Bundle, key string) string {
	t.Helper()
	for _, e := range b.BitbucketTokens() {
		if e.Key == key {
			return e.Value()
		}
	}
	t.Fatalf("the document holds no %s entry: it holds %v", key, b.Names())
	return ""
}

// setArgs is `creds set NAME` with a typed token's record as flags, which is
// how every test below but the prompting one drives it.
func setArgs(name string, extra ...string) []string {
	return append([]string{"creds", "set", name}, extra...)
}

// repoTokenFlags is the repository token every success test writes.
func repoTokenFlags(name string) []string {
	return []string{"--type", "repo", "--coverage", "acme/fugaro"}
}

// TestCredsSetBitbucketTokenRefusesAndWritesNothing is the red step: the fake
// Bitbucket answers 404 for the repository the record states, which is what a
// token that does not reach it gets, and the write is refused with the check's
// own one sentence — no bundle version, no record, no status, and neither the
// local config nor the state store touched.
func TestCredsSetBitbucketTokenRefusesAndWritesNothing(t *testing.T) {
	const name = "bitbucket-token--ci"
	answers := repoTokenAnswers()
	// The repository the record names is not one this token reaches: Bitbucket
	// answers 404, which the check reads as "does not cover" rather than as the
	// read being missing.
	answers["GET /2.0/repositories/acme/fugaro"] = bitbucketReply{status: 404, body: `{"error":{"message":"Not found"}}`}
	f := newBitbucketFixture(t, "", answers)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	before := f.value(t)
	beforeCfg := f.configBytes(t)

	out, errOut, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, repoTokenFlags(name)...)...)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("a token the fake Bitbucket refused = %v, want a user error (stdout %q, stderr %q)", err, out, errOut)
	}
	if want := "the token does not cover repository acme/fugaro"; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not carry the check's own sentence (%q): %v", want, err)
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal does not say that nothing was written: %v", err)
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written, want none", n)
	}
	if got := f.value(t); got != before {
		t.Errorf("the bundle changed:\n%s", got)
	}
	if got := f.configBytes(t); got != beforeCfg {
		t.Errorf("the project config changed:\n%s", got)
	}
	if got := f.records(t); len(got) != 0 {
		t.Errorf("the config holds %d record(s) after a refused write: %v", len(got), got)
	}
	if st := f.status(t, name); st != nil {
		t.Errorf("the state store holds a status for a token that was never written: %v", st)
	}
	if seen := f.site.paths(); len(seen) != 1 || seen[0] != "api.bitbucket.org /2.0/repositories/acme/fugaro" {
		t.Errorf("the check read %v, want the record's own repository and nothing else", seen)
	}
}

// TestCredsSetBitbucketTokenWritesBundleRecordAndStatus is the acceptance's
// success half: the bundle entry (T1), the local config's record (T2) and the
// check's answer in the state store (R12) — the identity and the time it passed
// — all three of them, and the document the service reads back is one the
// loader accepts.
func TestCredsSetBitbucketTokenWritesBundleRecordAndStatus(t *testing.T) {
	const name = "bitbucket-token--ci"
	f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))

	before := time.Now().Add(-time.Minute)
	_, errOut, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, repoTokenFlags(name)...)...)
	if err != nil {
		t.Fatalf("creds set %s: %v (stderr %q)", name, err, errOut)
	}
	if n := f.writes(); n != 1 {
		t.Fatalf("%d version(s) written, want one", n)
	}
	// The bundle entry, at the family's own key, holding the value this run
	// read and nothing else that changed.
	doc := f.value(t)
	b, err := credsvc.ParseBundle([]byte(doc))
	if err != nil {
		t.Fatalf("the document this command wrote is one the service's loader refuses: %v\n%s", err, doc)
	}
	if got := bundleEntryValue(t, b, name); got != bitbucketTokenValue {
		t.Errorf("the entry's value is %q, and it is not the token this run read", got)
	}
	if !strings.Contains(doc, config.ClaudeOAuthToken+": "+credsValue(t, "existing")) {
		t.Errorf("the entry this write was not about changed:\n%s", doc)
	}
	// The record, in the config's own spelling: the three facts the operator
	// stated, with the workspace qualified as it was typed and nothing else.
	recs := f.records(t)
	if len(recs) != 1 {
		t.Fatalf("the config holds %d record(s), want one: %v", len(recs), recs)
	}
	rec := recs[0]
	if rec.Name != "ci" || rec.Type != config.BitbucketTokenTypeRepo || !slices.Equal(rec.Coverage, []string{"acme/fugaro"}) {
		t.Errorf("the record written is %+v, want ci/repo/[acme/fugaro]", rec)
	}
	if rec.ProjectUUID != "" || rec.ProjectKey != "" {
		t.Errorf("a repository token's record names a project: %+v", rec)
	}
	// The check's answer, under the token's DOCUMENT KEY rather than its bare
	// suffix (R5): an identity stored under "ci" would be read as the model
	// account ci.
	st := f.status(t, name)
	if st == nil {
		t.Fatalf("the state store holds no status for %s", name)
	}
	if got := st["identity"]; got != "{aaaa-2222}" {
		t.Errorf("the identity recorded is %v, want the UUID the check read ({aaaa-2222})", got)
	}
	at, err := time.Parse(time.RFC3339, fmt.Sprint(st["verified_at"]))
	if err != nil {
		t.Fatalf("the status carries no readable verified_at (%v): %v", st["verified_at"], err)
	}
	if at.Before(before) || at.After(time.Now().Add(time.Minute)) {
		t.Errorf("verified_at is %s, which is not the moment of this write", at)
	}
	if st["state"] != nil {
		t.Errorf("the write left a hold on a token it had just proved: %v", st)
	}
}

// TestCredsSetBitbucketTokenResolvesAProjectKeyAndKeepsIt: a project token may
// be recorded by the key an operator reads in Bitbucket's UI, and the check is
// what resolves it — R1 and R2 match a project by UUID and a key can be
// renamed, so the UUID is what the record carries and the key stays beside it
// for display.
func TestCredsSetBitbucketTokenResolvesAProjectKeyAndKeepsIt(t *testing.T) {
	const name = "bitbucket-token--proj"
	f := newBitbucketFixture(t, bitbucketTokenSince, projectTokenAnswers())
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))

	if _, _, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, "--type", "project", "--coverage", "acme", "--project-key", "FUG")...); err != nil {
		t.Fatalf("creds set %s with a project key: %v", name, err)
	}
	recs := f.records(t)
	if len(recs) != 1 {
		t.Fatalf("the config holds %d record(s), want one: %v", len(recs), recs)
	}
	rec := recs[0]
	if rec.ProjectUUID != "{proj-1}" {
		t.Errorf("the record's project_uuid is %q, want the UUID the check resolved ({proj-1})", rec.ProjectUUID)
	}
	if rec.ProjectKey != "FUG" {
		t.Errorf("the record's project_key is %q, want the key the operator gave, kept for display", rec.ProjectKey)
	}
	if rec.Type != config.BitbucketTokenTypeProject || !slices.Equal(rec.Coverage, []string{"acme"}) {
		t.Errorf("the record written is %+v, want project/[acme]", rec)
	}
	// The UUID and the key are both written: the check read the project with the
	// token itself, and the reads stayed inside the workspace the record names.
	if seen := f.site.paths(); !slices.Contains(seen, "api.bitbucket.org /2.0/workspaces/acme/projects/FUG") {
		t.Errorf("the check did not read the project the record's key names: %v", seen)
	}
}

// TestCredsSetBitbucketTokenReportsWriteNotVerified: D470's write scopes are
// reported as NOT verified — nothing here writes, because Bitbucket has no dry
// run for a push or a pull request (D2353) — and the write says so in the
// operator's own output and in --json's field, and says that a republish is
// what puts the new coverage in front of runs.
func TestCredsSetBitbucketTokenReportsWriteNotVerified(t *testing.T) {
	const name = "bitbucket-token--ci"
	f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))

	out, errOut, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, repoTokenFlags(name)...)...)
	if err != nil {
		t.Fatalf("creds set %s: %v (stderr %q)", name, err, errOut)
	}
	for _, want := range []string{"write scopes not verified", "repository:write", "fugaro update"} {
		if !strings.Contains(out, want) {
			t.Errorf("the write does not say %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "{aaaa-2222}") {
		t.Errorf("the write does not name the identity the check read:\n%s", out)
	}
}

// TestCredsSetBitbucketTokenJSONCarriesTheRecordAndTheUnverifiedWrite is the
// same fact in the shape a script reads: the field exists for the family alone,
// it holds the record, and write_verified is false — the machine-readable half
// of the line above, because a script cannot read prose.
func TestCredsSetBitbucketTokenJSONCarriesTheRecordAndTheUnverifiedWrite(t *testing.T) {
	const name = "bitbucket-token--ci"
	f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))

	out, errOut, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, append(repoTokenFlags(name), "--json")...)...)
	if err != nil {
		t.Fatalf("creds set %s --json: %v (stderr %q)", name, err, errOut)
	}
	var res struct {
		Version   string `json:"version"`
		Bitbucket *struct {
			Name          string   `json:"name"`
			Type          string   `json:"type"`
			Coverage      []string `json:"coverage"`
			Identity      string   `json:"identity"`
			WriteVerified bool     `json:"write_verified"`
		} `json:"bitbucket"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("the output is not the JSON document it claims: %v\n%s", err, out)
	}
	if res.Bitbucket == nil {
		t.Fatalf("--json carries no bitbucket record:\n%s", out)
	}
	if res.Bitbucket.Name != name || res.Bitbucket.Type != "repo" || !slices.Equal(res.Bitbucket.Coverage, []string{"acme/fugaro"}) {
		t.Errorf("the JSON record is %+v", *res.Bitbucket)
	}
	if res.Bitbucket.Identity != "{aaaa-2222}" {
		t.Errorf("the JSON record's identity is %q, want the UUID the check read", res.Bitbucket.Identity)
	}
	if res.Bitbucket.WriteVerified {
		t.Error("--json reports the write scopes as verified, and nothing here writes (D2353)")
	}
	// The record write's own line is not prose inside the document a script
	// parses: it goes to stderr.
	if strings.Contains(out, "wrote ") {
		t.Errorf("the config write's line landed inside the JSON document:\n%s", out)
	}
}

// TestCredsSetBitbucketTokenNeverPrintsTheToken: the value is read, checked and
// written, and no output, no refusal and no recorded field carries a byte of it
// — on the write that lands and on the write that is refused alike.
func TestCredsSetBitbucketTokenNeverPrintsTheToken(t *testing.T) {
	const name = "bitbucket-token--ci"
	answers := repoTokenAnswers()
	answers["GET /2.0/repositories/acme/fugaro"] = bitbucketReply{status: 404, body: `{"error":{"message":"Not found"}}`}
	f := newBitbucketFixture(t, bitbucketTokenSince, answers)
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))

	_, _, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, repoTokenFlags(name)...)...)
	if err == nil {
		t.Fatal("the fake Bitbucket refused the token and the write landed")
	}
	assertNoValue(t, err.Error(), bitbucketTokenValue)

	// The write that lands: the token is in the bundle by design, and it is in
	// no line this command prints.
	f2 := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
	f2.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	out, errOut, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, repoTokenFlags(name)...)...)
	if err != nil {
		t.Fatalf("creds set %s: %v", name, err)
	}
	assertNoValue(t, out, bitbucketTokenValue)
	assertNoValue(t, errOut, bitbucketTokenValue)
	assertRedacted(t, out, bitbucketTokenValue)
	// And the status the check's own answer is written as carries the identity
	// and never the value.
	if st := f2.status(t, name); st != nil {
		assertNoValue(t, fmt.Sprint(st), bitbucketTokenValue)
	}
	// The one place the value goes is bitbucket.org's own root (F22), and this
	// is the whole list of hosts the check addressed.
	for _, seen := range f2.site.paths() {
		if !strings.HasPrefix(seen, "api.bitbucket.org ") {
			t.Errorf("the check addressed %s, which is not Bitbucket's own API root", seen)
		}
	}
	if len(f2.site.paths()) == 0 {
		t.Error("the check made no call, so nothing above is the token's own proof")
	}
}

// TestCredsSetBitbucketTokenRefusedByAnOldService is the acceptance's first
// step: the guard `creds set` already applies to the provider key, applied to
// the family — a deployed credentials service too old to read the family is
// refused before anything is written, and before a value is read or a fact is
// asked for.
//
// The release judged is the one that SERVES, and the entry named is this write's
// own key, so the operator knows which entry makes the document unreadable.
func TestCredsSetBitbucketTokenRefusedByAnOldService(t *testing.T) {
	const name = "bitbucket-token--ci"
	f := newBitbucketFixture(t, "0.20.0", repoTokenAnswers())
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	beforeCfg := f.configBytes(t)

	out, errOut, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, repoTokenFlags(name)...)...)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("creds set %s against a 0.20.0 service = %v, want a user error (stdout %q, stderr %q)", name, err, out, errOut)
	}
	msg := err.Error()
	if !strings.Contains(msg, "fugaro update") {
		t.Errorf("the refusal names no way out: %v", err)
	}
	if !strings.Contains(msg, bitbucketTokenSince) {
		t.Errorf("the refusal does not name the release the entry needs (%s): %v", bitbucketTokenSince, err)
	}
	if !strings.Contains(msg, name) {
		t.Errorf("the refusal does not name the entry that makes the document unreadable: %v", err)
	}
	assertNoValue(t, msg, bitbucketTokenValue)
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written, want none", n)
	}
	if got := f.configBytes(t); got != beforeCfg {
		t.Errorf("the project config changed:\n%s", got)
	}
	if st := f.status(t, name); st != nil {
		t.Errorf("the state store holds a status for a refused write: %v", st)
	}
	// The guard stands before the value: nothing was asked of Bitbucket, and
	// the command read nothing of the token's own facts.
	if seen := f.site.paths(); len(seen) != 0 {
		t.Errorf("the check ran before the guard refused the write: %v", seen)
	}
	if len(f.skew.asked()) == 0 {
		t.Error("the guard judged no service: the refusal above is not this test's")
	}
}

// TestCredsSetBitbucketTokenAFamilyWriteBesideTheProviderEntry is the guard's
// newest-entry rule: a document holding the provider key needs the provider's
// release, and a write of a FAMILY entry into it needs whichever of the two is
// newer — the family's own release here — so the skew that refused the provider
// write is not the one this write is refused for.
func TestCredsSetBitbucketTokenAFamilyWriteBesideTheProviderEntry(t *testing.T) {
	const name = "bitbucket-token--ci"
	f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
	f.seed(t, []byte(providerEntryNode(t)))

	// The service reads both entries: the write lands, and the entry it was not
	// about is kept.
	if _, _, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, repoTokenFlags(name)...)...); err != nil {
		t.Fatalf("creds set %s into a bundle holding the provider entry: %v", name, err)
	}
	doc := f.value(t)
	if !bundleHoldsProviderEntry([]byte(doc)) {
		t.Errorf("the provider entry this write was not about is gone:\n%s", doc)
	}
	if !bundleHoldsFamilyEntry([]byte(doc)) {
		t.Errorf("the document holds no family entry:\n%s", doc)
	}
	if _, err := credsvc.ParseBundle([]byte(doc)); err != nil {
		t.Fatalf("the document written is one the loader refuses: %v", err)
	}
}

// TestCredsSetBitbucketTokenRefusesARecordItsOwnRuleRefuses: R1's agreement of
// type and coverage is the config's rule and this command's, asked BEFORE the
// value is read — so a record whose halves disagree is refused with the sentence
// an operator editing the block by hand would read, and the token is never
// typed at a terminal for it.
func TestCredsSetBitbucketTokenRefusesARecordItsOwnRuleRefuses(t *testing.T) {
	const name = "bitbucket-token--ci"
	refuses := func(t *testing.T, want string, flags ...string) {
		t.Helper()
		f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		before := f.value(t)
		beforeCfg := f.configBytes(t)

		_, _, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, flags...)...)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("creds set %s %v = %v, want a user error", name, flags, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
		if !strings.Contains(err.Error(), "bitbucket_tokens.") {
			t.Errorf("the refusal does not name the record an operator edits: %v", err)
		}
		if n := f.writes(); n != 0 {
			t.Fatalf("%d version(s) written, want none", n)
		}
		if got := f.value(t); got != before {
			t.Errorf("the bundle changed:\n%s", got)
		}
		if got := f.configBytes(t); got != beforeCfg {
			t.Errorf("the project config changed:\n%s", got)
		}
	}

	t.Run("a repository coverage on a workspace type", func(t *testing.T) {
		refuses(t, "the two state one fact", "--type", "workspace", "--coverage", "acme/fugaro")
	})

	t.Run("a workspace coverage on a repository type", func(t *testing.T) {
		refuses(t, "the two state one fact", "--type", "repo", "--coverage", "acme/*")
	})

	t.Run("a type that is none of the three", func(t *testing.T) {
		refuses(t, "type must be one of", "--type", "organisation", "--coverage", "acme/fugaro")
	})
}

// TestCredsSetBitbucketTokenFlagsAskNothing: a record fact that was not passed
// is refused by name rather than asked for, in both of the states where a
// question cannot be asked — --json, where a machine is on the other end, and a
// stream that is not a terminal, which is every managed project, every run and
// every pipe (D1540). No prompt is written in either.
func TestCredsSetBitbucketTokenFlagsAskNothing(t *testing.T) {
	const name = "bitbucket-token--ci"

	t.Run("--json names the flag rather than asking", func(t *testing.T) {
		f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		out, _, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, "--json")...)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("creds set %s --json with no record = %v, want a user error", name, err)
		}
		if !strings.Contains(err.Error(), "--type") {
			t.Errorf("the refusal does not name the flag that supplies the fact: %v", err)
		}
		if !strings.Contains(err.Error(), "--json asks nothing") {
			t.Errorf("the refusal does not say why it asked nothing: %v", err)
		}
		if strings.Contains(out, "Type ") {
			t.Errorf("--json wrote a prompt:\n%s", out)
		}
		if n := f.writes(); n != 0 {
			t.Fatalf("%d version(s) written, want none", n)
		}
	})

	t.Run("no terminal names the flag rather than waiting", func(t *testing.T) {
		f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		out, _, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, "--coverage", "acme/fugaro")...)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("creds set %s with no --type and no terminal = %v, want a user error", name, err)
		}
		if !strings.Contains(err.Error(), "--type") {
			t.Errorf("the refusal does not name the flag that supplies the fact: %v", err)
		}
		if strings.Contains(out, "Type ") {
			t.Errorf("a prompt was written with no terminal:\n%s", out)
		}
		if n := f.writes(); n != 0 {
			t.Fatalf("%d version(s) written, want none", n)
		}
	})

	t.Run("the record flags are refused for every other NAME", func(t *testing.T) {
		f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
		f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
		before := f.value(t)
		_, _, err := executeStdin(t, credsValue(t, "replacement")+"\n", "creds", "set", config.ClaudeOAuthToken, "--type", "repo", "--coverage", "acme/fugaro")
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("a record flag on %s = %v, want a user error", config.ClaudeOAuthToken, err)
		}
		if !strings.Contains(err.Error(), config.BitbucketTokenPrefix+"<name>") {
			t.Errorf("the refusal does not say which names the flags belong to: %v", err)
		}
		if n := f.writes(); n != 0 {
			t.Fatalf("%d version(s) written, want none", n)
		}
		if got := f.value(t); got != before {
			t.Errorf("the bundle changed:\n%s", got)
		}
	})
}

// TestCredsSetBitbucketTokenRefusesWithoutTheProjectsStateBackend: the identity
// is written to the project's credential state (R12) and the record to this
// machine's config, and both are opened BEFORE the bundle is touched — so a
// project whose state store cannot be reached, or whose config is the shared
// one, is refused with nothing written at all rather than after the value is in
// the bundle.
func TestCredsSetBitbucketTokenRefusesWithoutTheProjectsStateBackend(t *testing.T) {
	const name = "bitbucket-token--ci"
	f := newBitbucketFixture(t, bitbucketTokenSince, repoTokenAnswers())
	f.seed(t, []byte(config.ClaudeOAuthToken+": "+credsValue(t, "existing")+"\n"))
	before := f.value(t)
	beforeCfg := f.configBytes(t)
	// The database answers as another project's: the write would record an
	// identity in a project that never sees the token.
	f.db.Set(budget.PathProject, "somebody-else")

	_, _, err := executeStdin(t, bitbucketTokenValue+"\n", setArgs(name, repoTokenFlags(name)...)...)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("creds set %s against another project's database = %v, want a user error", name, err)
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal does not say that nothing was written: %v", err)
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written, want none", n)
	}
	if got := f.value(t); got != before {
		t.Errorf("the bundle changed:\n%s", got)
	}
	if got := f.configBytes(t); got != beforeCfg {
		t.Errorf("the project config changed:\n%s", got)
	}
	if seen := f.site.paths(); len(seen) != 0 {
		t.Errorf("the check ran for a write that could not be completed: %v", seen)
	}
}

// TestBitbucketTokenSinceHasAReleaseBullet: bitbucketTokenSince names the
// release whose credentials service first read the family, and the tag that
// shipped it is a fact about the past rather than a slot to fill, so
// docs/release.md's Before-you-tag list is what holds the constant to that
// release and to the direction that matters — a raise.
func TestBitbucketTokenSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `bitbucketTokenSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no bitbucketTokenSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+bitbucketTokenSince+"\"`") {
		t.Errorf("docs/release.md's bitbucketTokenSince bullet does not carry the constant's release (%q)", bitbucketTokenSince)
	}
	for _, want := range []string{"the release that shipped the family", "fugaro creds set", "must not raise it"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's bitbucketTokenSince bullet does not say %q", want)
		}
	}
}

// TestBitbucketTokenSinceIsNotAheadOfTheReleaseTheTreeIsOn: providerEntrySince's
// own direction, for the family. What the guard judges is the service an
// installation has DEPLOYED, so a value above the release the cli stream is at
// refuses the family's write on every installation whose service already reads
// it, with a remedy that changes nothing for them.
func TestBitbucketTokenSinceIsNotAheadOfTheReleaseTheTreeIsOn(t *testing.T) {
	cat, err := streams.Parse("docs/release-streams.yaml", []byte(readDoc(t, "docs/release-streams.yaml")))
	if err != nil {
		t.Fatalf("docs/release-streams.yaml: %v", err)
	}
	cli, ok := cat.Lookup(streams.CLI)
	if !ok {
		t.Fatal("docs/release-streams.yaml carries no cli stream, so the release this tree is on cannot be read")
	}
	if imagePredates(cli.Current.String(), bitbucketTokenSince) {
		t.Errorf("bitbucketTokenSince = %q is ahead of the cli stream's %s: the family is read by the credentials service of the release that shipped it, so a value above the release this tree is on refuses the write on installations whose service already reads it", bitbucketTokenSince, cli.Current)
	}
}
