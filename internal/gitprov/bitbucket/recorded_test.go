package bitbucket

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

// The fixtures in testdata/recorded are real Bitbucket Cloud exchanges,
// recorded by live_test.go (build tag `live`) against the sandbox
// repository with httpfixture.Recorder. Every account identity in them
// (the reviewer's name, UUID, account ID and avatar hash, the token
// user's UUID, account ID and avatar, the workspace's and project's names
// and UUIDs, and the HTTPS clone link's user) was replaced with a
// placeholder before committing, which TestRecordedFixturesHoldNoIdentities
// enforces for any future re-recording. These
// tests replay them, so the adapter is checked against Bitbucket's real
// response shapes, and not only the hand-written fixtures in testdata.
const (
	recStamp    = "20260927-183406"
	recBranch   = "fugaro/live-" + recStamp + "-draft"
	recTitle    = "Fugaro live check " + recStamp
	recBody     = "Live check description.\n\nSecond paragraph, which must survive every update."
	recReviewer = "{5a4d0b0e-0000-4000-8000-00000000a11c}"
	recPRURL    = "https://bitbucket.org/acme/sandbox/pull-requests/"
)

func openRecorded(t *testing.T, fixture, ws, slug string, warn func(string)) *Provider {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", "recorded", fixture))
	p, err := New(Options{Workspace: ws, Slug: slug, Token: "replay-token", BaseURL: srv.URL + "/2.0", Warn: warn})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func recSpec(draft bool) gitprov.PRSpec {
	return gitprov.PRSpec{Branch: recBranch, Base: "master", Title: recTitle, Body: recBody, Draft: draft, Reviewers: []string{recReviewer}}
}

// TestRecordedDraftLifecycle replays one pull request through create as a
// real draft, draft to ready, ready to draft, and a no-op: Bitbucket
// honours draft on create and on update in both directions, so none of
// these needs the "[DRAFT] " title fallback.
func TestRecordedDraftLifecycle(t *testing.T) {
	steps := []struct {
		fixture string
		draft   bool
	}{
		{"create_draft.json", true},
		{"existing_to_ready.json", false},
		{"existing_to_draft.json", true},
		{"existing_no_op.json", true}, // GET only: already a draft
	}
	for _, s := range steps {
		t.Run(strings.TrimSuffix(s.fixture, ".json"), func(t *testing.T) {
			p := openRecorded(t, s.fixture, "acme", "sandbox", nil)
			pr, err := p.EnsurePR(ctx, recSpec(s.draft))
			if err != nil || pr != (gitprov.PR{Number: 1, URL: recPRURL + "1", Draft: s.draft}) {
				t.Fatalf("pr = %+v, err = %v", pr, err)
			}
		})
	}
}

// TestRecordedMixedCaseFindsExisting replays a lookup with the workspace
// and slug in mixed case: Bitbucket accepts them in the URL path, and the
// lowercased full_name in the query finds the existing pull request.
func TestRecordedMixedCaseFindsExisting(t *testing.T) {
	p := openRecorded(t, "mixed_case.json", "Acme", "Sandbox", nil)
	pr, err := p.EnsurePR(ctx, recSpec(false))
	if err != nil || pr != (gitprov.PR{Number: 1, URL: recPRURL + "1"}) {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

func TestRecordedComment(t *testing.T) {
	p := openRecorded(t, "comment.json", "acme", "sandbox", nil)
	if err := p.Comment(ctx, gitprov.PR{Number: 1}, "### Fugaro live check\n\nA **markdown** comment."); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedLabelsWarnOnce(t *testing.T) {
	var warnings []string
	p := openRecorded(t, "labels_warning.json", "acme", "sandbox", func(m string) { warnings = append(warnings, m) })
	for i, name := range []string{"labels-a", "labels-b"} {
		pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/live-" + recStamp + "-" + name, Base: "master",
			Title: recTitle + " labels", Body: "Labels check.", Labels: []string{"fugaro"}})
		if want := (gitprov.PR{Number: 2 + i, URL: fmt.Sprint(recPRURL, 2+i)}); err != nil || pr != want {
			t.Fatalf("pr = %+v, err = %v, want %+v", pr, err, want)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want one", warnings)
	}
}

// TestRecordedReviewerRejected replays Bitbucket's real answer to an
// unknown reviewer UUID (HTTP 400, "Malformed reviewers list"): the pull
// request is opened again without reviewers.
func TestRecordedReviewerRejected(t *testing.T) {
	p := openRecorded(t, "reviewer_rejected.json", "acme", "sandbox", nil)
	pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/live-" + recStamp + "-reviewer-rejected", Base: "master",
		Title: recTitle + " reviewer", Body: "Reviewer check.", Reviewers: []string{"{00000000-0000-4000-8000-000000000000}"}})
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || !strings.Contains(err.Error(), "Malformed reviewers list") || pr != (gitprov.PR{Number: 4, URL: recPRURL + "4"}) {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// The comments the live check posts on its pull request before reading
// it back as a follow-up would (live_test.go, followUpReads). The general
// one ends with an HTML comment, to see whether Bitbucket keeps it in the
// raw content, as Fugaro's report marker needs.
const (
	liveGeneralBody = "Fugaro live check: a general comment.\n\n<!-- fugaro:live-check -->"
	liveInlineBody  = "Fugaro live check: an inline comment."
	liveReplyBody   = "Fugaro live check: a reply."
	liveInlinePath  = "live-draft.txt"
)

var recShortSHA = regexp.MustCompile(`^[0-9a-f]{7,40}$`) // Bitbucket abbreviates source.commit.hash

// The follow-up recordings (follow_up_reads.json, ensure_by_number.json
// and ensure_by_number_declined.json) come from a later live run, on its
// own pull request.
const (
	recFollowUpPR     = 16
	recFollowUpBranch = "fugaro/live-20260930-130729-draft"
	recFollowUpTitle  = "Fugaro live check 20260930-130729"
	recBotID          = "712020:00000000-0000-4000-8000-00000000b07e" // the repository access token's account_id
)

// TestRecordedFollowUpReads replays follow_up_reads.json, the live
// check's reads of its pull request (live_test.go, followUpReads): the
// repository, the pull request, GET /user and the comments. What it
// pins down, as Bitbucket really answered:
//
//   - is_private is present (true for the sandbox);
//   - author.account_id is set, <digits>:<uuid>, and source.commit.hash is
//     abbreviated to 12 hex;
//   - GET /user answers a repository access token with HTTP 403, so no
//     comment's Self is known, and the fallback (the PR author's account_id
//     equals Fugaro's comments' author_id) is what tells them apart;
//   - a reply carries parent {id, links} and an inline field of its own,
//     and the resolved thread's first comment carries "resolution": {}
//     (an empty object) while the others carry no resolution at all;
//   - inline has from, to, path, start_from and start_to, and no outdated
//     field while the comment is current;
//   - deleted and pending are always present, false here;
//   - the raw content keeps an HTML comment, while content.html escapes it
//     as text (which is why Fugaro's report marker shows in Bitbucket).
func TestRecordedFollowUpReads(t *testing.T) {
	var warnings []string
	p := openRecorded(t, "follow_up_reads.json", "acme", "sandbox", func(m string) { warnings = append(warnings, m) })
	if repo, err := p.Repository(ctx); err != nil || !repo.Private {
		t.Fatalf("Repository = %+v, %v; the sandbox is private", repo, err)
	}
	info, err := p.PullRequest(ctx, recFollowUpPR)
	want := gitprov.PRInfo{Number: recFollowUpPR, URL: recPRURL + "16", State: gitprov.PROpen, Title: recFollowUpTitle, Body: recBody, AuthorID: recBotID,
		SourceBranch: recFollowUpBranch, SourceRepo: "acme/sandbox", HeadSHA: "3bd808d407ef"}
	if err != nil || info != want || !recShortSHA.MatchString(info.HeadSHA) {
		t.Fatalf("PullRequest = %+v, %v\nwant %+v", info, err, want)
	}
	comments, err := p.Comments(ctx, recFollowUpPR)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "HTTP 403") || !strings.Contains(warnings[0], "not accessible by this authentication mechanism") {
		t.Errorf("warnings = %q, want the one about GET /user's 403", warnings)
	}
	type shape struct {
		kind     gitprov.CommentKind
		path     string
		line     int
		resolved bool
	}
	wantShapes := map[string]shape{
		"### Fugaro live check\n\nA **markdown** comment.": {kind: gitprov.CommentGeneral},
		liveGeneralBody: {kind: gitprov.CommentGeneral},
		liveInlineBody:  {gitprov.CommentInline, liveInlinePath, 1, true},
		liveReplyBody:   {gitprov.CommentInline, liveInlinePath, 1, true}, // resolved through its thread's first comment
	}
	if len(comments) != len(wantShapes) {
		t.Fatalf("got %d comments, want %d: %+v", len(comments), len(wantShapes), comments)
	}
	for _, c := range comments {
		w, ok := wantShapes[c.Body]
		if !ok {
			t.Errorf("unexpected comment %+v", c)
			continue
		}
		if c.Kind != w.kind || c.Path != w.path || c.Line != w.line || c.Resolved != w.resolved || c.Outdated || c.Deleted {
			t.Errorf("comment %q = %+v, want %+v", c.Body, c, w)
		}
		// The token posted both the pull request and the comments, so
		// its comments carry the PR author's ID; GET /user refused, so
		// none is known to be Fugaro's own.
		if c.AuthorID != recBotID || c.Author != "fugaro-live-check" || c.Self || c.SelfKnown || !c.Collaborator {
			t.Errorf("comment %q: author %q/%q, self %v/%v", c.Body, c.Author, c.AuthorID, c.Self, c.SelfKnown)
		}
		if !strings.HasPrefix(c.URL, recPRURL+"16/_/diff#comment-"+c.ID) || c.CreatedAt.IsZero() {
			t.Errorf("comment %q: url %q, created %v", c.Body, c.URL, c.CreatedAt)
		}
	}
}

// TestRecordedEnsureByNumber replays an update by number: one GET of the
// pull request and a PUT of its draft state with the title it already
// has; the spec's title and body are never sent.
func TestRecordedEnsureByNumber(t *testing.T) {
	p := openRecorded(t, "ensure_by_number.json", "acme", "sandbox", nil)
	pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: recFollowUpPR, Branch: recFollowUpBranch, Title: "must not be sent", Body: "must not be sent", Draft: true})
	if err != nil || pr != (gitprov.PR{Number: recFollowUpPR, URL: recPRURL + "16", Draft: true}) {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestRecordedEnsureByNumberDeclined replays a declined pull request
// (state DECLINED, closed_by set): it reads as closed, and an update by
// number refuses it with ErrPRNotOpen, writing nothing.
func TestRecordedEnsureByNumberDeclined(t *testing.T) {
	p := openRecorded(t, "ensure_by_number_declined.json", "acme", "sandbox", nil)
	info, err := p.PullRequest(ctx, recFollowUpPR)
	if err != nil || info.State != gitprov.PRClosed || info.AuthorID != recBotID {
		t.Fatalf("PullRequest = %+v, %v", info, err)
	}
	pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: recFollowUpPR, Branch: recFollowUpBranch})
	if !errors.Is(err, gitprov.ErrPRNotOpen) || pr.Number != recFollowUpPR || pr.URL != recPRURL+"16" {
		t.Fatalf("pr = %+v, err = %v; want ErrPRNotOpen with the PR", pr, err)
	}
}

func TestRecordedUnauthorized(t *testing.T) {
	p := openRecorded(t, "unauthorized.json", "acme", "sandbox", nil)
	if _, err := p.EnsurePR(ctx, recSpec(false)); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v", err)
	}
}

// Placeholder identities allowed in testdata/recorded: the reviewer
// ("Sandbox Reviewer"), the repository access token's own app user, and
// the acme workspace ("Acme"), which is also the repository's team owner.
var (
	recAllowedUUIDs      = map[string]bool{recReviewer: true, "{00000000-0000-4000-8000-00000000b07e}": true, recWorkspace: true}
	recAllowedAccountIDs = map[string]bool{"557058:00000000-0000-4000-8000-00000000a11c": true, "712020:00000000-0000-4000-8000-00000000b07e": true}
	recAllowedNames      = map[string]bool{"Sandbox Reviewer": true, "sandbox_reviewer": true, "fugaro-live-check": true, "Acme": true}
	recEmail             = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	recGravatar          = regexp.MustCompile(`gravatar\.com/avatar/([0-9a-fA-F]+)`)
	recAvatarID          = "00000000-0000-4000-8000-000000000000"   // the avatar image ID placeholder
	recWorkspace         = "{00000000-0000-4000-8000-0000000000ac}" // the acme workspace's (and its team owner's) uuid
	recAtlAvatar         = regexp.MustCompile(`atl-paas\.net/([^/"]+)/([^/"]+)/`)
	// The fixtures name only the generic acme/sandbox repository. The
	// pattern below is assembled from pieces so this file does not itself
	// hold the strings it forbids: the workspace and repository names of
	// the real sandbox the fixtures were recorded against, the reviewer's
	// and the repository's real UUIDs, and machine paths.
	recForbidden = regexp.MustCompile(`(?i:` + strings.Join([]string{
		"edge" + "appinc", "fugaro" + "sandbox", "edge" + "web", "edge-" + "devel", "46e89d" + "3a", "58b4b3" + "46",
	}, "|") + `)|/Users` + `/`)
	// Any repository named in an API path or a Bitbucket link must be the
	// generic one (case-insensitively: mixed_case.json spells it Acme/Sandbox).
	recRepoRef = regexp.MustCompile(`(?:/2\.0/repositories|://bitbucket\.org)/([^/"%?]+)/([^/"%?]+)`)
	// A repository's HTTPS clone link carries a user name before the
	// host, which must be the placeholder (it is not an email address).
	recCloneURL  = regexp.MustCompile(`://([^@/"]+)@bitbucket\.org/([^/"]+)/([^/"]+?)\.git`)
	recCloneUser = "00000000000000000000000000b07e"
)

// TestRecordedFixturesHoldNoIdentities keeps real account identities out
// of the recorded fixtures: a re-recording must be pseudonymized (see the
// comment at the top of this file) before it is committed. It fails on any
// email address, any user, app-user, team or workspace object (or any
// object carrying an account_id) whose uuid, account_id, name, username
// or slug is not a placeholder, any link naming another workspace, any
// gravatar hash that is not all zeros, and any Atlassian avatar URL that
// names a real account.
func TestRecordedFixturesHoldNoIdentities(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "recorded", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recorded fixtures (%v)", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range identityProblems(data) {
			t.Errorf("%s: %s", f, p)
		}
	}
}

// TestIdentityGuardCatchesRealIdentities keeps the guard above honest: a
// fixture carrying an email, a real-looking account and a real gravatar
// hash must be flagged on every count.
func TestIdentityGuardCatchesRealIdentities(t *testing.T) {
	bad := []byte(`[{"method": "GET", "path": "/x", "status": 200, "response": {
	  "author": {"type": "user", "display_name": "Jane Doe", "nickname": "jdoe",
	    "uuid": "{11111111-2222-4333-8444-555555555555}", "account_id": "557058:11111111-2222-4333-8444-555555555555",
	    "links": {"avatar": {"href": "https://secure.gravatar.com/avatar/0123456789abcdef0123456789abcdef?d=x"}}},
	  "summary": {"raw": "ping jane.doe@example.com"},
	  "links": {"self": {"href": "https://api.bitbucket.org/2.0/repositories/` + "edge" + `appinc/other/pullrequests/1"}},
	  "owner": {"type": "team", "display_name": "Jane's Company", "uuid": "{11111111-2222-4333-8444-666666666666}", "username": "janeco"},
	  "workspace": {"type": "workspace", "name": "Jane's Company", "slug": "janeco", "uuid": "{11111111-2222-4333-8444-666666666666}",
	    "links": {"html": {"href": "https://bitbucket.org/janeco/"}, "avatar": {"href": "https://bitbucket.org/workspaces/janeco/avatar/"}}},
	  "clone": [{"name": "https", "href": "https://jdoe1234@bitbucket.org/acme/sandbox.git"}],
	  "bot": {"type": "app_user", "links": {"avatar": {"href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/712020:11111111-2222-4333-8444-555555555555/22222222-3333-4444-8555-666666666666/128"}}}}}]`)
	problems := strings.Join(identityProblems(bad), "\n")
	for _, want := range []string{"email", "real uuid", "real account_id", "real display_name", "real nickname", "gravatar", "avatar URL", "real sandbox", "other than acme/sandbox",
		"team object with a real uuid", "team object with a real display_name", "team object with a real username",
		"workspace object with a real uuid", "workspace object with a real name", "workspace object with a real slug", "names a workspace other than acme", "clone URL"} {
		if !strings.Contains(problems, want) {
			t.Errorf("the guard missed %q; it reported:\n%s", want, problems)
		}
	}
	if good := identityProblems([]byte(`[{"response": {"type": "user", "display_name": "Sandbox Reviewer", "uuid": "` + recReviewer + `"},
	  "owner": {"type": "team", "display_name": "Acme", "uuid": "` + recWorkspace + `", "username": "acme"},
	  "workspace": {"type": "workspace", "name": "Acme", "slug": "acme", "uuid": "` + recWorkspace + `",
	    "links": {"avatar": {"href": "https://bitbucket.org/workspaces/acme/avatar/?ts=1"}, "html": {"href": "https://bitbucket.org/acme/workspace/projects/PER"}}},
	  "avatar": {"href": "https://bitbucket.org/account/acme/avatar/"},
	  "clone": [{"name": "https", "href": "https://` + recCloneUser + `@bitbucket.org/acme/sandbox.git"}]}]`)); len(good) != 0 {
		t.Errorf("the guard flagged a placeholder identity: %q", good)
	}
}

// identityProblems lists every real identity found in a fixture file's
// contents: email addresses, gravatar hashes that are not all zeros,
// Atlassian avatar URLs naming a real account, and user, app-user or team
// objects (or any object carrying an account_id) whose uuid, account_id,
// display_name or nickname is not a placeholder.
func identityProblems(data []byte) []string {
	var out []string
	text := string(data)
	if m := recForbidden.FindString(text); m != "" {
		out = append(out, fmt.Sprintf("holds the real sandbox's name or a real identifier %q", m))
	}
	for _, m := range recRepoRef.FindAllStringSubmatch(text, -1) {
		switch {
		case m[1] == "account" || m[1] == "workspaces": // a workspace's avatar
			if !strings.EqualFold(m[2], "acme") {
				out = append(out, "names a workspace other than acme: "+m[0])
			}
		case m[2] == "workspace": // a workspace's projects
			if !strings.EqualFold(m[1], "acme") {
				out = append(out, "names a workspace other than acme: "+m[0])
			}
		case !strings.EqualFold(m[1], "acme") || !strings.EqualFold(m[2], "sandbox"):
			out = append(out, "names a repository other than acme/sandbox: "+m[0])
		}
	}
	for _, m := range recCloneURL.FindAllStringSubmatch(text, -1) {
		if m[1] != recCloneUser || !strings.EqualFold(m[2], "acme") || !strings.EqualFold(m[3], "sandbox") {
			out = append(out, "clone URL names a real user or another repository: "+m[0])
		}
	}
	// Neither clone link holds an email address: the HTTPS one's user is
	// checked above, and the SSH one is always git@bitbucket.org.
	noClone := strings.ReplaceAll(recCloneURL.ReplaceAllString(text, "://"), `"git@bitbucket.org:`, `"`)
	if m := recEmail.FindString(noClone); m != "" {
		out = append(out, fmt.Sprintf("holds an email address %q", m))
	}
	for _, m := range recGravatar.FindAllStringSubmatch(text, -1) {
		if strings.Trim(m[1], "0") != "" {
			out = append(out, fmt.Sprintf("gravatar hash %s is not the all-zero placeholder", m[1]))
		}
	}
	for _, m := range recAtlAvatar.FindAllStringSubmatch(text, -1) {
		if !recAllowedAccountIDs[m[1]] || m[2] != recAvatarID {
			out = append(out, "avatar URL names a real account: "+m[0])
		}
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return append(out, "not JSON: "+err.Error())
	}
	return appendIdentities(out, v)
}

func appendIdentities(out []string, v any) []string {
	switch v := v.(type) {
	case map[string]any:
		typ, _ := v["type"].(string)
		_, hasAccount := v["account_id"]
		if typ == "user" || typ == "app_user" || typ == "team" || typ == "workspace" || hasAccount {
			if u, ok := v["uuid"].(string); ok && !recAllowedUUIDs[u] {
				out = append(out, fmt.Sprintf("%s object with a real uuid %s", typ, u))
			}
			if a, ok := v["account_id"].(string); ok && !recAllowedAccountIDs[a] {
				out = append(out, fmt.Sprintf("%s object with a real account_id %s", typ, a))
			}
			for _, k := range []string{"display_name", "nickname", "name"} {
				if n, ok := v[k].(string); ok && !recAllowedNames[n] {
					out = append(out, fmt.Sprintf("%s object with a real %s %q", typ, k, n))
				}
			}
			for _, k := range []string{"username", "slug"} { // a team's or workspace's
				if n, ok := v[k].(string); ok && n != "acme" {
					out = append(out, fmt.Sprintf("%s object with a real %s %q", typ, k, n))
				}
			}
		}
		for _, x := range v {
			out = appendIdentities(out, x)
		}
	case []any:
		for _, x := range v {
			out = appendIdentities(out, x)
		}
	}
	return out
}
