package cli

// U2's tests: the enrolled list read through the credentials service's read
// mint. What is worth pinning here is the list (it is the project's and not a
// directory's, D3306/R16), the two whole shapes a view has (R13), the reasons
// and the fixes they name (R1), and the credential every read is made with
// (D3343, D3352). No literal here is, or looks like, a credential.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/repofiles"
)

// enrolledRepoYAML is a fugaro.yaml a repository of this project may hold and
// the stages may plan from: one that parses against the project layer.
const enrolledRepoYAML = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
	"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// stubEnrolledRead replaces the two seams the reader reads the world through --
// the published allow-list and the provider opener -- and restores them when the
// test ends. A nil admits means the project's policy is not read at all, which
// is the same answer an installation with no database gets.
func stubEnrolledRead(t *testing.T, admits []string, open func(*cloudEnv) gitprov.Opener) {
	t.Helper()
	oldAdmits, oldOpen := enrolledAdmits, enrolledReadOpener
	t.Cleanup(func() { enrolledAdmits, enrolledReadOpener = oldAdmits, oldOpen })
	enrolledAdmits = func(context.Context, enrolledIn) []string { return admits }
	enrolledReadOpener = open
}

// enrolledFakeOpener answers the opener with the provider a test pinned for one
// repository, and a failed open for any other: the reader must never open one
// it did not list.
func enrolledFakeOpener(m map[string]gitprov.Provider) func(*cloudEnv) gitprov.Opener {
	return func(*cloudEnv) gitprov.Opener {
		return func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
			p, ok := m[kind+"/"+repo]
			if !ok {
				return nil, nil, fmt.Errorf("no provider is pinned for %s/%s in this test", kind, repo)
			}
			return p, nil, nil
		}
	}
}

// held is a repository a test can read: the files its default branch holds,
// answered with a host's own 404 for anything else (which is what
// internal/repofiles reads as "the repository holds none").
func held(repo string, files map[string]string, yaml string) gitprov.Provider {
	if files == nil {
		files = map[string]string{}
	}
	if yaml != "" {
		files[repofiles.YAML] = yaml
	}
	return absentFiles{Provider: &fake.Provider{Repo: repo, State: fake.State{
		DefaultBranch: "main",
		Files:         files,
	}}}
}

// absentFiles is the fake with a host's answer for a file it does not hold: the
// 404 internal/repofiles reads as absence rather than as a repository nobody
// could read.
type absentFiles struct{ *fake.Provider }

func (p absentFiles) Contents(ctx context.Context, path, ref string) ([]byte, error) {
	data, err := p.Provider.Contents(ctx, path, ref)
	if err != nil {
		return nil, &httpjson.StatusError{Method: http.MethodGet, URL: "https://example.invalid/" + path, Status: http.StatusNotFound, Body: "Not Found"}
	}
	return data, nil
}

// enrolledLC is a project whose repositories a test names.
func enrolledLC(repos map[string]localcfg.Repo) *localcfg.Config {
	return &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Repos: repos}
}

// TestEnrolledReposAreTheListReposLsDraws: the reader lists exactly the
// repositories `fugaro project repos ls` draws -- this machine's config's and
// the concrete names the published allow-list admits -- so the two agree on
// what "the project's repositories" means (spec §The enrolled list). The list
// comes from no directory: the same call from anywhere answers this.
func TestEnrolledReposAreTheListReposLsDraws(t *testing.T) {
	lc := enrolledLC(map[string]localcfg.Repo{
		"acme/app":       {Provider: gitprov.KindGitHub, BaseBranch: "main"},
		"Acme/Tools.git": {Provider: gitprov.KindGitHub, BaseBranch: "main"},
	})
	admits := []string{"github/acme/extra", "github/acme/*"}
	stubEnrolledRead(t, admits, enrolledFakeOpener(map[string]gitprov.Provider{
		"github/acme/app":   held("acme/app", nil, enrolledRepoYAML),
		"github/acme/tools": held("acme/tools", nil, enrolledRepoYAML),
		"github/acme/extra": held("acme/extra", nil, enrolledRepoYAML),
	}))

	views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
	// repos ls's own rows, canonically spelled "<provider>/<owner>/<name>"
	// (repos_view.go), stripped to the owner/name a stage line carries.
	_, keys := reposViewLocalOf(lc)
	var rows []string
	for _, name := range reposViewRowNames(reposViewAllow{Entries: admits}, keys) {
		rows = append(rows, strings.TrimPrefix(name, "github/"))
	}
	// The list, not the order: C1 puts the views in canonical order itself
	// (canonicalViews), so here the two have to agree on which repositories are
	// the project's.
	got, want := reposOf(views), rows
	slices.SortFunc(got, func(a, b string) int { return strings.Compare(repoSortKey(a), repoSortKey(b)) })
	slices.SortFunc(want, func(a, b string) int { return strings.Compare(repoSortKey(a), repoSortKey(b)) })
	if !slices.Equal(got, want) {
		t.Fatalf("the enrolled list is %v, want the %v `fugaro project repos ls` draws: update and repos ls must agree on the project's repositories", got, want)
	}
	for _, v := range views {
		if !v.Read() || v.Cfg == nil || v.Branch == "" || v.Head == "" {
			t.Errorf("%s: %+v, want the read shape: its default branch, its head commit and its parse", v.Repo, v)
		}
		if len(v.Files) != 0 {
			t.Errorf("%s carries %v: a repository that holds neither extra path carries no file", v.Repo, v.Files)
		}
	}
}

// TestEnrolledReposMissingOrInvalidIsUnread: a repository whose default branch
// holds no fugaro.yaml, or one that does not parse, is unread with the one
// reason and NOTHING else -- no branch, no head, no bytes, no parse, no extra
// files -- so a stage that reads Read() alone can never be handed half a
// repository to plan from (R13).
func TestEnrolledReposMissingOrInvalidIsUnread(t *testing.T) {
	lc := enrolledLC(map[string]localcfg.Repo{
		"acme/gone":  {Provider: gitprov.KindGitHub, BaseBranch: "main"},
		"acme/bad":   {Provider: gitprov.KindGitHub, BaseBranch: "main"},
		"acme/none":  {Provider: gitprov.KindGitHub, BaseBranch: "main"},
		"acme/other": {Provider: gitprov.KindGitHub, BaseBranch: "main"},
	})
	stubEnrolledRead(t, nil, enrolledFakeOpener(map[string]gitprov.Provider{
		"github/acme/gone": held("acme/gone", nil, ""),
		"github/acme/bad":  held("acme/bad", nil, "version: 1\nproject: aurora\nnope: 1\n"),
		// A file that is there and is not a document the reader may plan from
		// either: the extra paths are read beside it, and neither is kept.
		"github/acme/none": held("acme/none", map[string]string{
			enrolledSettingsPath:            "{ not json",
			enrolledBacklogPath + "12-x.md": "item",
		}, ""),
	}))

	views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
	byRepo := map[string]repoView{}
	for _, v := range views {
		byRepo[v.Repo] = v
	}
	if got := byRepo["acme/gone"].Unread; got != enrolledNoYAMLReason {
		t.Errorf("a repository with no fugaro.yaml reads %q, want %q", got, enrolledNoYAMLReason)
	}
	if got := byRepo["acme/none"].Unread; got != enrolledNoYAMLReason {
		t.Errorf("a repository with no fugaro.yaml reads %q, want %q", got, enrolledNoYAMLReason)
	}
	if got := byRepo["acme/bad"].Unread; !strings.HasPrefix(got, enrolledInvalidYAML) {
		t.Errorf("a repository with an invalid fugaro.yaml reads %q, want it to open %q and name its first problem", got, enrolledInvalidYAML)
	}
	if got := byRepo["acme/other"].Unread; !strings.HasPrefix(got, "its provider could not be opened") {
		t.Errorf("a repository no provider is pinned for reads %q, want the open's own failure", got)
	}
	for _, v := range views {
		if v.Read() {
			t.Fatalf("%s reads as read: nothing this test pinned for it can be read", v.Repo)
		}
		if v.Branch != "" || v.Head != "" || v.YAML != nil || v.Cfg != nil || len(v.Files) != 0 {
			t.Errorf("%s: %+v, want the reason alone: an unread view carries no half a repository (R13)", v.Repo, v)
		}
		if strings.ContainsAny(v.Unread, "\n\r") {
			t.Errorf("%s reads %q, want one line", v.Repo, v.Unread)
		}
	}
}

// TestEnrolledReposNameTheFix: every reason a repository gets names what to do
// about it (R1, spec §Reading a default branch). The five an admin meets first:
// an installation that publishes no credentials service (R22), a GitHub
// repository no App ID can be read for anywhere (F43), a service that refuses
// the repository itself, a mint it refuses for who the caller is, and an
// identity the reader's own gcloud login could not mint.
func TestEnrolledReposNameTheFix(t *testing.T) {
	t.Run("no credentials service", func(t *testing.T) {
		lc := enrolledLC(map[string]localcfg.Repo{
			"acme/app": {Provider: gitprov.KindGitHub, GitHubAppID: reviewHeadAppID},
		})
		stubEnrolledRead(t, nil, enrolledProvider)
		views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
		reason := onlyView(t, views).Unread
		for _, want := range []string{"credentials service", "fugaro update"} {
			if !strings.Contains(reason, want) {
				t.Errorf("the reason %q does not name %q", reason, want)
			}
		}
		if strings.Contains(reason, "truncated") {
			t.Errorf("the reason %q is cut, and a reason a stage prints has to carry its fix whole", reason)
		}
	})

	t.Run("no app id anywhere", func(t *testing.T) {
		lc := enrolledLC(map[string]localcfg.Repo{
			"acme/app": {Provider: gitprov.KindGitHub},
		})
		lc.CredentialsService = &localcfg.CredentialsService{URL: "https://service.example.invalid"}
		stubEnrolledRead(t, nil, enrolledProvider)
		views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
		reason := onlyView(t, views).Unread
		if !strings.Contains(reason, "fugaro project repos add") {
			t.Errorf("the reason %q does not name the door that records an App ID", reason)
		}
		if strings.Contains(reason, "truncated") {
			t.Errorf("the reason %q is cut, and a reason a stage prints has to carry its fix whole", reason)
		}
	})

	t.Run("the mint refuses", func(t *testing.T) {
		mint := startReadMintStandIn(t)
		mint.status = http.StatusForbidden
		setReviewReadIdentity(t)
		lc := enrolledLC(map[string]localcfg.Repo{
			"acme/app": {Provider: gitprov.KindGitHub, GitHubAppID: reviewHeadAppID},
		})
		lc.CredentialsService = &localcfg.CredentialsService{URL: mint.url}
		stubEnrolledRead(t, nil, enrolledProvider)
		views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
		reason := onlyView(t, views).Unread
		// The reason is the short sentence and not the service's chain, which is
		// several times longer than a repository's reason is printed in: what a
		// person has to act on is the fix, and a truncated line carries none.
		for _, want := range []string{"credentials service", "the installation's people", "fugaro update"} {
			if !strings.Contains(reason, want) {
				t.Errorf("the reason %q does not name %q", reason, want)
			}
		}
		if strings.Contains(reason, "truncated") {
			t.Errorf("the reason %q is cut, and a reason a stage prints has to carry its fix whole", reason)
		}
		if mint.calls() == 0 {
			t.Error("the read never asked the installation's credentials service for a token")
		}
	})

	t.Run("no job serves it", func(t *testing.T) {
		// The service read the call and refused the repository: its inventory
		// holds no job for it (credsvc's request_refused).
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"request_refused","message":"refused"}`))
		}))
		t.Cleanup(srv.Close)
		setReviewReadIdentity(t)
		lc := enrolledLC(map[string]localcfg.Repo{
			"acme/app": {Provider: gitprov.KindGitHub, GitHubAppID: reviewHeadAppID},
		})
		lc.CredentialsService = &localcfg.CredentialsService{URL: srv.URL}
		stubEnrolledRead(t, nil, enrolledProvider)
		views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
		reason := onlyView(t, views).Unread
		// R1's second fix: what unsticks it is enrolling the repository, and not
		// the people list the caller IS on.
		for _, want := range []string{"no job of this installation serves it", "fugaro project repos add", "in its checkout"} {
			if !strings.Contains(reason, want) {
				t.Errorf("the reason %q does not name %q", reason, want)
			}
		}
		if strings.Contains(reason, "the installation's people") {
			t.Errorf("the reason %q sends the reader to the people list, and the repository is what the service refused", reason)
		}
		if strings.Contains(reason, "truncated") {
			t.Errorf("the reason %q is cut, and a reason a stage prints has to carry its fix whole", reason)
		}
	})

	t.Run("no gcloud login", func(t *testing.T) {
		mint := startReadMintStandIn(t)
		lc := enrolledLC(map[string]localcfg.Repo{
			"acme/app": {Provider: gitprov.KindGitHub, GitHubAppID: reviewHeadAppID},
		})
		lc.CredentialsService = &localcfg.CredentialsService{URL: mint.url}
		prev := serviceReadIdentity
		serviceReadIdentity = func(context.Context, string, string) (string, error) {
			return "", errors.New("the gcloud command that mints one is not on PATH")
		}
		t.Cleanup(func() { serviceReadIdentity = prev })
		stubEnrolledRead(t, nil, enrolledProvider)
		views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
		reason := onlyView(t, views).Unread
		// The caller's own identity is the reader's own fix, and the sentence
		// carrying it is the reader's short one: the identity mint's own chain
		// names the login only past the runes a reason is printed in.
		for _, want := range []string{"Google ID token could not be minted", "gcloud auth login"} {
			if !strings.Contains(reason, want) {
				t.Errorf("the reason %q does not name %q", reason, want)
			}
		}
		if strings.Contains(reason, "truncated") {
			t.Errorf("the reason %q is cut, and a reason a stage prints has to carry its fix whole", reason)
		}
	})
}

// onlyView is the single view a one-repository case answered.
func onlyView(t *testing.T, views []repoView) repoView {
	t.Helper()
	if len(views) != 1 {
		t.Fatalf("the reader answered %d views, want the one repository this case pinned", len(views))
	}
	return views[0]
}

// TestEnrolledReposReadThroughTheMint: the whole read path. The reader presents
// the operator's own identity to the installation's credentials service, the
// service mints a token for the ONE repository named and for the project's
// recorded App, and every read of the default branch carries that token. No
// environment variable decides any of it: a machine that exports an App ID
// reads with the project's own (R1, F23).
func TestEnrolledReposReadThroughTheMint(t *testing.T) {
	mint := startReadMintStandIn(t)
	forge := startEnrolledForge(t, map[string]string{
		"acme/app": enrolledRepoYAML,
	})
	setReviewReadIdentity(t)
	// This machine's environment names another App. The read must use the
	// project's, and the mint's body says which one it asked for.
	t.Setenv("FUGARO_GITHUB_APP_ID", "999999999")
	lc := enrolledLC(map[string]localcfg.Repo{
		"acme/app": {Provider: gitprov.KindGitHub, BaseBranch: "main", GitHubAppID: reviewHeadAppID},
	})
	lc.CredentialsService = &localcfg.CredentialsService{URL: mint.url}
	stubEnrolledRead(t, nil, enrolledProvider)

	views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
	v := onlyView(t, views)
	if !v.Read() {
		t.Fatalf("acme/app reads as unread: %s", v.Unread)
	}
	if v.Branch != "main" || v.Head != reviewHeadSHA {
		t.Errorf("the view names branch %q and head %q, want the default branch and the commit the forge resolved", v.Branch, v.Head)
	}
	if string(v.YAML) != enrolledRepoYAML || v.Cfg == nil {
		t.Errorf("the view carries %q and %v, want the default branch's fugaro.yaml and its parse", v.YAML, v.Cfg)
	}
	if calls := mint.calls(); calls != 1 {
		t.Errorf("the read mint was called %d times, want 1: one repository is one mint", calls)
	}
	if got := mint.bodies[0]; got["app_id"] != reviewHeadAppID || got["repository"] != "acme/app" {
		t.Errorf("the mint was asked for %v, want the project's App (%s) for the repository itself", got, reviewHeadAppID)
	}
	if auths := forge.auth(); len(auths) == 0 {
		t.Fatal("the forge was never read")
	} else {
		for _, a := range auths {
			if !strings.Contains(a, mint.token) {
				t.Errorf("a forge read carried %q, want the token the service minted", a)
			}
		}
	}
}

// TestEnrolledReposMintNothingForAnAgent: in a coding agent's session no
// identity is minted and nothing is read at all (D3352, D902). Every repository
// this machine's config names is unread with the one line that says where the
// read belongs, so the stages report needs-you rather than converging on no
// repository -- and the project's policy is not read either: an agent's update
// converges the project-wide parts alone.
func TestEnrolledReposMintNothingForAnAgent(t *testing.T) {
	mint := startReadMintStandIn(t)
	lc := enrolledLC(map[string]localcfg.Repo{
		"acme/app": {Provider: gitprov.KindGitHub, BaseBranch: "main", GitHubAppID: reviewHeadAppID},
		"acme/web": {Provider: gitprov.KindGitHub, BaseBranch: "main", GitHubAppID: reviewHeadAppID},
	})
	lc.CredentialsService = &localcfg.CredentialsService{URL: mint.url}
	oldAdmits, oldOpen := enrolledAdmits, enrolledReadOpener
	t.Cleanup(func() { enrolledAdmits, enrolledReadOpener = oldAdmits, oldOpen })
	enrolledAdmits = func(context.Context, enrolledIn) []string {
		t.Error("the project's policy was read in a coding agent's session")
		return nil
	}
	enrolledReadOpener = func(*cloudEnv) gitprov.Opener {
		t.Error("a provider was opened in a coding agent's session")
		return nil
	}
	t.Setenv("CLAUDECODE", "1")

	views, patterns := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
	if got, want := reposOf(views), []string{"acme/app", "acme/web"}; !slices.Equal(got, want) {
		t.Fatalf("the list is %v, want this machine's own %v: every repository is reported, none is silently skipped", got, want)
	}
	for _, v := range views {
		if v.Read() || v.Unread != enrolledAgentReason {
			t.Errorf("%s reads as %+v, want the one line %q", v.Repo, v, enrolledAgentReason)
		}
	}
	if len(patterns) != 0 {
		t.Errorf("patterns = %v, want none: a session expands no allow-list", patterns)
	}
	if mint.calls() != 0 {
		t.Errorf("the read mint was called %d times in a coding agent's session, want 0", mint.calls())
	}
}

// TestEnrolledReposBorrowTheProjectAppID: a GitHub repository this machine's
// config records no App ID for -- an admit-only one above all -- takes
// projectGitHubAppID's answer (F43), so a second admin's machine reads the
// project's repositories without onboarding them again.
func TestEnrolledReposBorrowTheProjectAppID(t *testing.T) {
	mint := startReadMintStandIn(t)
	startEnrolledForge(t, map[string]string{
		"acme/base":  enrolledRepoYAML,
		"acme/extra": enrolledRepoYAML,
	})
	setReviewReadIdentity(t)
	lc := enrolledLC(map[string]localcfg.Repo{
		"acme/base": {Provider: gitprov.KindGitHub, BaseBranch: "main", GitHubAppID: reviewHeadAppID},
	})
	lc.CredentialsService = &localcfg.CredentialsService{URL: mint.url}
	// acme/extra is admitted by the published list and this machine's config
	// does not hold it at all.
	stubEnrolledRead(t, []string{"github/acme/extra"}, enrolledProvider)

	views, _ := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
	if got, want := reposOf(views), []string{"acme/base", "acme/extra"}; !slices.Equal(got, want) {
		t.Fatalf("the list is %v, want %v: a repository of the published list this machine lacks is still covered", got, want)
	}
	for _, v := range views {
		if !v.Read() {
			t.Fatalf("%s reads as unread: %s", v.Repo, v.Unread)
		}
	}
	if len(mint.bodies) != 2 {
		t.Fatalf("the read mint was called %d times, want one per repository", len(mint.bodies))
	}
	for _, b := range mint.bodies {
		if b["app_id"] != reviewHeadAppID {
			t.Errorf("the mint for %s was signed as %q, want the project's own App %s", b["repository"], b["app_id"], reviewHeadAppID)
		}
	}
}

// TestEnrolledReposNamePatterns: an admit that is a pattern cannot be expanded
// into names, so it is answered beside the list for a notice line of its own
// and never guessed at (R19, F36). It is not a repository: nothing is opened
// for it.
func TestEnrolledReposNamePatterns(t *testing.T) {
	opened := []string{}
	lc := enrolledLC(map[string]localcfg.Repo{
		"acme/app": {Provider: gitprov.KindGitHub, BaseBranch: "main"},
	})
	stubEnrolledRead(t, []string{"github/acme/*", "*", "acme/other", "github/acme/app"}, func(*cloudEnv) gitprov.Opener {
		return func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
			opened = append(opened, kind+"/"+repo)
			return held("acme/app", nil, enrolledRepoYAML), nil, nil
		}
	})

	views, patterns := readEnrolledRepos(context.Background(), enrolledIn{LC: lc})
	if got, want := patterns, []string{"github/acme/*", "*", "acme/other"}; !slices.Equal(got, want) {
		t.Errorf("the patterns are %v, want %v: a pattern is answered unexpanded and each is named once", got, want)
	}
	if got, want := reposOf(views), []string{"acme/app"}; !slices.Equal(got, want) {
		t.Errorf("the list is %v, want %v: a pattern is not a repository", got, want)
	}
	if got, want := opened, []string{"github/acme/app"}; !slices.Equal(got, want) {
		t.Errorf("the reader opened %v, want only the %v it listed", got, want)
	}
}

// startEnrolledForge is a stand-in code host answering the reads
// internal/repofiles makes of one repository: the repository itself, its
// default branch's fugaro.yaml, the commit the branch resolves to, and an empty
// tree (a repository that keeps neither recipes nor a backlog, and pins no
// plugin is the ordinary one). files maps owner/name to that fugaro.yaml.
//
// Any other read is a 404, which is the answer a repository that holds nothing
// at that path gives. Every authorization the reads carried is recorded, so a
// test can say which credential the forge was asked with.
type enrolledForge struct {
	mu    sync.Mutex
	auths []string
	files map[string]string
}

func startEnrolledForge(t *testing.T, files map[string]string) *enrolledForge {
	t.Helper()
	f := &enrolledForge{files: files}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		files := f.files
		f.mu.Unlock()
		rest, repo := splitRepoPath(r.URL.Path)
		yaml, ok := files[repo]
		switch {
		case ok && rest == "":
			_ = json.NewEncoder(w).Encode(map[string]any{"private": false, "default_branch": "main"})
		case ok && rest == "/contents/"+repofiles.YAML:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "file", "encoding": "base64", "size": len(yaml),
				"content": base64.StdEncoding.EncodeToString([]byte(yaml)),
			})
		case ok && rest == "/commits/HEAD":
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": reviewHeadSHA})
		case ok && strings.HasPrefix(rest, "/git/trees/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": []any{}, "truncated": false})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	prev := serviceReadGitHubAPIURL
	serviceReadGitHubAPIURL = srv.URL
	t.Cleanup(func() { serviceReadGitHubAPIURL = prev })
	return f
}

// splitRepoPath is a REST path under /repos/ as the repository and the suffix
// after it: "/repos/acme/app/contents/fugaro.yaml" is ("/contents/fugaro.yaml",
// "acme/app").
func splitRepoPath(path string) (rest, repo string) {
	parts := strings.SplitN(strings.TrimPrefix(path, "/repos/"), "/", 3)
	if len(parts) < 2 {
		return "", ""
	}
	repo = parts[0] + "/" + parts[1]
	if len(parts) == 3 {
		rest = "/" + parts[2]
	}
	return rest, repo
}

func (f *enrolledForge) auth() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auths...)
}
