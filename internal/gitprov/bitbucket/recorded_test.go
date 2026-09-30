package bitbucket

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

// The fixtures in testdata/recorded are real Bitbucket Cloud exchanges,
// recorded by live_test.go (build tag `live`) against the sandbox
// repository with httpfixture.Recorder. Every account identity in them
// (the reviewer's name, UUID, account ID and avatar hash, and the token
// user's UUID, account ID and avatar) was replaced with a placeholder
// before committing, which TestRecordedFixturesHoldNoIdentities enforces
// for any future re-recording. These
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

var (
	recPullPath = regexp.MustCompile(`/pullrequests/(\d+)$`)
	recShortSHA = regexp.MustCompile(`^[0-9a-f]{7,40}$`) // Bitbucket abbreviates source.commit.hash
)

// TestRecordedFollowUpReads replays follow_up_reads.json, which the live
// check records (as live_follow_up_reads.json) once it has been run
// against the sandbox: the repository, its pull request and the comments
// the live check posted, read through the adapter. Until that recording
// is committed there is nothing to replay, and the hand-written fixtures
// in testdata stand in for it.
func TestRecordedFollowUpReads(t *testing.T) {
	file := filepath.Join("testdata", "recorded", "follow_up_reads.json")
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("follow_up_reads.json has not been recorded yet (the live check records it)")
	}
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []httpfixture.Exchange
	if err := json.Unmarshal(data, &exchanges); err != nil {
		t.Fatal(err)
	}
	number := 0
	for _, e := range exchanges {
		if m := recPullPath.FindStringSubmatch(e.Path); m != nil {
			number, _ = strconv.Atoi(m[1])
			break
		}
	}
	if number == 0 {
		t.Fatalf("%s reads no pull request", file)
	}
	p := openRecorded(t, "follow_up_reads.json", "acme", "sandbox", nil)
	if repo, err := p.Repository(ctx); err != nil || !repo.Private {
		t.Fatalf("Repository = %+v, %v; the sandbox is private", repo, err)
	}
	info, err := p.PullRequest(ctx, number)
	if err != nil || info.State != gitprov.PROpen || !strings.HasPrefix(info.SourceBranch, "fugaro/live-") ||
		!strings.EqualFold(info.SourceRepo, "acme/sandbox") || info.AuthorID == "" || !recShortSHA.MatchString(info.HeadSHA) {
		t.Fatalf("PullRequest = %+v, %v", info, err)
	}
	comments, err := p.Comments(ctx, number)
	if err != nil {
		t.Fatal(err)
	}
	byBody := map[string]gitprov.Comment{}
	for _, c := range comments {
		byBody[c.Body] = c
	}
	for body, kind := range map[string]gitprov.CommentKind{liveGeneralBody: gitprov.CommentGeneral, liveInlineBody: gitprov.CommentInline, liveReplyBody: gitprov.CommentInline} {
		c, ok := byBody[body]
		if !ok || c.Kind != kind || (kind == gitprov.CommentInline && c.Path != liveInlinePath) {
			t.Errorf("comment %q = %+v (found %v)", body, c, ok)
			continue
		}
		// The token posted both the pull request and the comments, so
		// its comments carry the PR author's ID, and are its own when
		// the adapter could tell.
		if c.AuthorID != info.AuthorID || (c.SelfKnown && !c.Self) {
			t.Errorf("comment %q: author %q, self %v/%v; the PR author is %q", body, c.AuthorID, c.Self, c.SelfKnown, info.AuthorID)
		}
	}
}

func TestRecordedUnauthorized(t *testing.T) {
	p := openRecorded(t, "unauthorized.json", "acme", "sandbox", nil)
	if _, err := p.EnsurePR(ctx, recSpec(false)); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v", err)
	}
}

// Placeholder identities allowed in testdata/recorded: the reviewer
// ("Sandbox Reviewer") and the repository access token's own app user.
var (
	recAllowedUUIDs      = map[string]bool{recReviewer: true, "{00000000-0000-4000-8000-00000000b07e}": true}
	recAllowedAccountIDs = map[string]bool{"557058:00000000-0000-4000-8000-00000000a11c": true, "712020:00000000-0000-4000-8000-00000000b07e": true}
	recAllowedNames      = map[string]bool{"Sandbox Reviewer": true, "sandbox_reviewer": true, "fugaro-live-check": true}
	recEmail             = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	recGravatar          = regexp.MustCompile(`gravatar\.com/avatar/([0-9a-fA-F]+)`)
	recAvatarID          = "00000000-0000-4000-8000-000000000000" // the avatar image ID placeholder
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
)

// TestRecordedFixturesHoldNoIdentities keeps real account identities out
// of the recorded fixtures: a re-recording must be pseudonymized (see the
// comment at the top of this file) before it is committed. It fails on any
// email address, any user or app-user object (or any object carrying an
// account_id) whose uuid, account_id or name is not a placeholder, any
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
	  "bot": {"type": "app_user", "links": {"avatar": {"href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/712020:11111111-2222-4333-8444-555555555555/22222222-3333-4444-8555-666666666666/128"}}}}}]`)
	problems := strings.Join(identityProblems(bad), "\n")
	for _, want := range []string{"email", "real uuid", "real account_id", "real display_name", "real nickname", "gravatar", "avatar URL", "real sandbox", "other than acme/sandbox"} {
		if !strings.Contains(problems, want) {
			t.Errorf("the guard missed %q; it reported:\n%s", want, problems)
		}
	}
	if good := identityProblems([]byte(`[{"response": {"type": "user", "display_name": "Sandbox Reviewer", "uuid": "` + recReviewer + `"}}]`)); len(good) != 0 {
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
		if !strings.EqualFold(m[1], "acme") || !strings.EqualFold(m[2], "sandbox") {
			out = append(out, "names a repository other than acme/sandbox: "+m[0])
		}
	}
	if m := recEmail.FindString(text); m != "" {
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
		if typ == "user" || typ == "app_user" || typ == "team" || hasAccount {
			if u, ok := v["uuid"].(string); ok && !recAllowedUUIDs[u] {
				out = append(out, fmt.Sprintf("%s object with a real uuid %s", typ, u))
			}
			if a, ok := v["account_id"].(string); ok && !recAllowedAccountIDs[a] {
				out = append(out, fmt.Sprintf("%s object with a real account_id %s", typ, a))
			}
			for _, k := range []string{"display_name", "nickname"} {
				if n, ok := v[k].(string); ok && !recAllowedNames[n] {
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
