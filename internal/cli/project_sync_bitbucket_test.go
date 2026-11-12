package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// bitbucketSyncRig is the Bitbucket API `project sync --bitbucket` lists a
// workspace through: it answers the workspace's repositories, and records what
// it was asked and with which credential.
type bitbucketSyncRig struct {
	srv   *httptest.Server
	names []string
	// status answers every request with this status instead of a listing.
	status int
	// calls is how many requests reached the rig, so a refusal made before
	// the listing can be shown to have made none.
	calls int
	// auth is every request's Authorization header, in order.
	auth []string
}

func newBitbucketSyncRig(t *testing.T, names ...string) *bitbucketSyncRig {
	t.Helper()
	g := &bitbucketSyncRig{names: names, status: http.StatusOK}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.calls++
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		if r.URL.Path != "/2.0/repositories/acme" {
			t.Errorf("the listing asked %s, want the workspace's repositories", r.URL.Path)
		}
		if g.status != http.StatusOK {
			w.WriteHeader(g.status)
			return
		}
		values := make([]map[string]any, 0, len(g.names))
		for _, n := range g.names {
			_, slug, _ := strings.Cut(n, "/")
			values = append(values, map[string]any{"full_name": n, "slug": slug})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"values": values})
	}))
	t.Cleanup(g.srv.Close)
	oldURL, oldClient := bitbucketAPIURL, bitbucketAPIClient
	bitbucketAPIURL, bitbucketAPIClient = g.srv.URL+"/2.0", g.srv.Client()
	t.Cleanup(func() { bitbucketAPIURL, bitbucketAPIClient = oldURL, oldClient })
	return g
}

// bitbucketPolicyFixture is the project the Bitbucket syncs below run against:
// the fixture's installation with a published policy whose allow-list is allow.
func bitbucketPolicyFixture(t *testing.T, allow ...string) *projectFixture {
	t.Helper()
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": allow,
	})
	return f
}

// `project sync --bitbucket` lists the workspace's repositories with the token
// read from stdin, offers the ones the allow-list does not admit, and writes
// them through the same ETag-guarded path `repos add` uses. It never removes,
// and it names no token: what is printed is the workspace, the difference and
// the write.
//
// Mutation (verified, run and restored): list with no token (drop it from the
// listing's options) — this test fails, the listing refused before it asked.
func TestProjectSyncBitbucketOffersUnadmitted(t *testing.T) {
	const token = "token-not-a-real-credential-abcdef"
	f := bitbucketPolicyFixture(t, "bitbucket/acme/app")
	g := newBitbucketSyncRig(t, "acme/app", "acme/new", "acme/other")
	out, errOut, err := executeStdin(t, token+"\n", "project", "sync", "--bitbucket", "acme")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	offer, wrote, ok := strings.Cut(out, "allow_repos: ")
	if !ok {
		t.Fatalf("the sync did not say what it wrote:\n%s", out)
	}
	for _, want := range []string{"bitbucket/acme/new", "bitbucket/acme/other"} {
		if !strings.Contains(offer, want) {
			t.Errorf("the offer does not name %q:\n%s", want, out)
		}
		if !strings.Contains(wrote, want) {
			t.Errorf("the write does not name %q:\n%s", want, out)
		}
	}
	if strings.Contains(offer, "bitbucket/acme/app") {
		t.Errorf("the offer names a repository the allow-list already admits:\n%s", out)
	}
	want := []string{"bitbucket/acme/app", "bitbucket/acme/new", "bitbucket/acme/other"}
	if got := allowReposOf(t, f.db); !slices.Equal(got, want) {
		t.Errorf("allow_repos = %v, want %v", got, want)
	}
	// The token is the credential of the listing, and it is nowhere a reader
	// of this command's output could find it.
	for _, stream := range []string{out, errOut} {
		if strings.Contains(stream, token) {
			t.Errorf("the token is printed:\n%s", stream)
		}
	}
	if len(g.auth) != 1 || g.auth[0] != "Bearer "+token {
		t.Errorf("the listing sent %v, want the token read from stdin as a bearer credential", g.auth)
	}
}

// The token comes from stdin or a hidden prompt and never from an argument: an
// argument is in the process's command line, where every process on the
// machine reads it, and in the shell's history. So the command registers no
// flag that could carry one — a run that names one is refused as an unknown
// flag, before anything is read — and the value typed at the prompt is in
// neither stream, on the answer's way back or on a refusal's.
//
// Mutation (verified, run and restored): add a --bitbucket-token flag — this
// test fails, the flag the token must never travel through existing.
func TestProjectSyncBitbucketTokenNeverFromArgs(t *testing.T) {
	const token = "token-not-a-real-credential-abcdef"
	var names []string
	newProjectSyncCmd().Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	for _, name := range names {
		if strings.Contains(name, "token") {
			t.Errorf("`project sync` registers --%s: a Bitbucket token is read from stdin or a hidden prompt, and a flag would put it in the command line", name)
		}
	}

	f := bitbucketPolicyFixture(t, "bitbucket/acme/app")
	g := newBitbucketSyncRig(t, "acme/new")
	before := nodeOf(t, f.db)
	out, errOut, err := execute(t, "project", "sync", "--bitbucket", "acme", "--bitbucket-token", token)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err = %v, want the refusal of an argument carrying a token", err)
	}
	if g.calls != 0 {
		t.Errorf("the refused run listed the workspace: %d calls", g.calls)
	}
	if nodeOf(t, f.db) != before {
		t.Error("the refused run changed the policy node")
	}
	for _, stream := range []string{out, errOut, err.Error()} {
		if strings.Contains(stream, token) {
			t.Errorf("the refused run echoes the token:\n%s", stream)
		}
	}

	// The same token, typed at the prompt, on the one path that may fail after
	// it was read: the API's refusal is the answer that quotes the most of
	// what it was sent, and it must not quote the credential.
	g.status = http.StatusUnauthorized
	before = nodeOf(t, f.db)
	out, errOut, err = executeStdin(t, token+"\n", "project", "sync", "--bitbucket", "acme")
	if err == nil {
		t.Fatalf("the sync accepted a refused listing:\n%s", out)
	}
	for _, stream := range []string{out, errOut, err.Error()} {
		if strings.Contains(stream, token) {
			t.Errorf("the refusal quotes the token:\n%s", stream)
		}
	}
	if nodeOf(t, f.db) != before {
		t.Error("a refused listing changed the policy node")
	}
}

// `--check` is the report-only half: it prints exactly the difference a plain
// sync would write and writes nothing at all — the published allow-list is the
// same before and after, which is what makes the flag safe to run against a
// workspace before deciding.
//
// Mutation (verified, run and restored): print the difference and fall through
// to the write — this test fails, the node changed.
func TestProjectSyncCheckWritesNothing(t *testing.T) {
	const token = "token-not-a-real-credential-abcdef"
	f := bitbucketPolicyFixture(t, "bitbucket/acme/app")
	g := newBitbucketSyncRig(t, "acme/app", "acme/new", "acme/other")
	before := nodeOf(t, f.db)
	out, errOut, err := executeStdin(t, token+"\n", "project", "sync", "--bitbucket", "acme", "--check")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"bitbucket/acme/new", "bitbucket/acme/other"} {
		if !strings.Contains(out, want) {
			t.Errorf("--check does not print %q as the difference:\n%s", want, out)
		}
	}
	if strings.Contains(out, "allow_repos: ") {
		t.Errorf("--check printed a write it did not make:\n%s", out)
	}
	if !strings.Contains(out, "nothing was written") {
		t.Errorf("--check does not say it wrote nothing:\n%s", out)
	}
	if nodeOf(t, f.db) != before {
		t.Error("--check wrote to the policy node")
	}
	if len(g.auth) != 1 || g.auth[0] != "Bearer "+token {
		t.Errorf("--check listed with %v, want the token read from stdin", g.auth)
	}
	for _, stream := range []string{out, errOut} {
		if strings.Contains(stream, token) {
			t.Errorf("--check prints the token:\n%s", stream)
		}
	}

	// A workspace whose repositories the allow-list admits every one of says
	// so and writes nothing either.
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"bitbucket/acme/*"},
	})
	before = nodeOf(t, f.db)
	out, _, err = executeStdin(t, token+"\n", "project", "sync", "--bitbucket", "acme", "--check")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to add") {
		t.Errorf("--check does not say the pattern admits them all:\n%s", out)
	}
	if nodeOf(t, f.db) != before {
		t.Error("--check wrote to the policy node")
	}
}

// The refusal in a coding agent's session comes before the listing and before
// the token is read: the write is not an agent's to make, and a Bitbucket token
// is a workspace credential a session must never hold.
//
// Mutation (verified, run and restored): drop the agentEnv refusal — this test
// fails, the token read inside a session that sets CLAUDECODE.
func TestProjectSyncBitbucketRefusedInAgentSession(t *testing.T) {
	const token = "token-not-a-real-credential-abcdef"
	f := bitbucketPolicyFixture(t, "bitbucket/acme/app")
	g := newBitbucketSyncRig(t, "acme/new")
	t.Setenv("CLAUDECODE", "1")
	before := nodeOf(t, f.db)
	out, _, err := executeStdin(t, token+"\n", "project", "sync", "--bitbucket", "acme")
	if err == nil || !strings.Contains(err.Error(), "coding agent") {
		t.Fatalf("err = %v, want the refusal of a coding agent's session", err)
	}
	if !strings.Contains(err.Error(), syncBitbucketFlag) {
		t.Errorf("the refusal does not name the flag whose credential it refuses: %v", err)
	}
	if g.calls != 0 {
		t.Errorf("the session listed the workspace: %d calls", g.calls)
	}
	if nodeOf(t, f.db) != before {
		t.Error("a refused sync changed the policy node")
	}
	for _, stream := range []string{out, err.Error()} {
		if strings.Contains(stream, token) {
			t.Errorf("the refusal echoes the token:\n%s", stream)
		}
	}
}

// --bitbucket-project narrows the listing to one project of the workspace, and
// without a workspace there is no listing for it to narrow: a project key on
// its own is refused rather than sent to GitHub's path.
func TestProjectSyncBitbucketProjectNeedsAWorkspace(t *testing.T) {
	f := bitbucketPolicyFixture(t, "bitbucket/acme/app")
	g := newBitbucketSyncRig(t, "acme/new")
	before := nodeOf(t, f.db)
	_, _, err := execute(t, "project", "sync", "--bitbucket-project", "PROJ")
	if err == nil || !strings.Contains(err.Error(), syncBitbucketFlag) {
		t.Fatalf("err = %v, want a refusal naming the workspace flag", err)
	}
	if g.calls != 0 {
		t.Errorf("the refused run listed a workspace: %d calls", g.calls)
	}
	if nodeOf(t, f.db) != before {
		t.Error("the refused run changed the policy node")
	}
}

// A workspace the operator left empty, and the two providers named at once,
// are both refused rather than resolved: `--bitbucket ""` is a script whose
// variable did not fill in, and reading it as "no Bitbucket" would list
// GitHub's repositories and offer them, while the GitHub flags beside
// --bitbucket name a provider the listing will not ask.
//
// Mutation (verified, run and restored): read the flag by its value alone
// (drop the Changed check) — the empty-workspace case fails, GitHub's listing
// reached through a flag that names Bitbucket.
func TestProjectSyncBitbucketRefusesAnUnnamedOrDoubledProvider(t *testing.T) {
	f := bitbucketPolicyFixture(t, "bitbucket/acme/app")
	g := newBitbucketSyncRig(t, "acme/new")
	for _, args := range [][]string{
		{"project", "sync", "--bitbucket", ""},
		{"project", "sync", "--bitbucket", "acme", "--github-app-id", "12345"},
		{"project", "sync", "--bitbucket", "acme", "--github-app-key"},
	} {
		before := nodeOf(t, f.db)
		_, _, err := execute(t, args...)
		// The refusal names the flag the operator wrote: a run that answered
		// this with GitHub's own missing-App-ID sentence would send them to a
		// flag they did not type.
		if err == nil || !strings.Contains(err.Error(), syncBitbucketFlag) {
			t.Errorf("%v: err = %v, want a refusal naming %s", args, err, syncBitbucketFlag)
		}
		if nodeOf(t, f.db) != before {
			t.Errorf("%v changed the policy node", args)
		}
	}
	if g.calls != 0 {
		t.Errorf("a refused run listed a workspace: %d calls", g.calls)
	}
}

// The project filter reaches the API as the workspace listing's own query, so
// the narrowing is Bitbucket's and not this command's: a filter applied here
// would be a second answer to "which repositories are in this project".
func TestProjectSyncBitbucketProjectFiltersTheListing(t *testing.T) {
	const token = "token-not-a-real-credential-abcdef"
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("q"))
		fmt.Fprint(w, `{"values":[{"full_name":"acme/new","slug":"new"}]}`)
	}))
	t.Cleanup(srv.Close)
	oldURL, oldClient := bitbucketAPIURL, bitbucketAPIClient
	bitbucketAPIURL, bitbucketAPIClient = srv.URL+"/2.0", srv.Client()
	t.Cleanup(func() { bitbucketAPIURL, bitbucketAPIClient = oldURL, oldClient })

	f := bitbucketPolicyFixture(t, "bitbucket/acme/app")
	out, _, err := executeStdin(t, token+"\n", "project", "sync", "--bitbucket", "acme", "--bitbucket-project", "PROJ")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(queries) != 1 || queries[0] != `project.key="PROJ"` {
		t.Errorf("the listing asked q=%v, want the project filter", queries)
	}
	if got := allowReposOf(t, f.db); !slices.Contains(got, "bitbucket/acme/new") {
		t.Errorf("allow_repos = %v, want the project's repository added", got)
	}
}
