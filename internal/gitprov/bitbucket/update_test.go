package bitbucket

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

const prLink = `"links": {"html": {"href": "https://bitbucket.org/acme/web/pull-requests/12"}}`

func openInline(t *testing.T, warn func(string), exchanges ...string) *Provider {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(file, []byte("["+strings.Join(exchanges, ",\n")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httpfixture.Serve(t, file)
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "bb-token-1234", BaseURL: srv.URL + "/2.0", Warn: warn})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// prJSON is pull request 12 as Bitbucket shows it. reviewers is the raw
// JSON array of its reviewers.
func prJSON(state string, draft bool, title, desc, reviewers string) string {
	return fmt.Sprintf(`{"type": "pullrequest", "id": 12, "title": %q, "description": %q, "state": %q, "draft": %t, "reviewers": %s, "source": {"branch": {"name": "fugaro/x"}}, %s}`,
		title, desc, state, draft, reviewers, prLink)
}

func getPR(state string, draft bool, title, desc, reviewers string) string {
	return `{"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests/12", "status": 200, "response": ` + prJSON(state, draft, title, desc, reviewers) + `}`
}

func put(req string, resp string) string {
	return `{"method": "PUT", "path": "/2.0/repositories/acme/web/pullrequests/12", "request": ` + req + `, "status": 200, "response": ` + resp + `}`
}

func ptr[T any](v T) *T { return &v }

func TestDraftCreateSendsNoReviewers(t *testing.T) {
	var warned []string
	p := openInline(t, func(s string) { warned = append(warned, s) },
		`{"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests?q=source.branch.name=\"fugaro/20260927-101500-abcd\" AND source.repository.full_name=\"acme/web\" AND state=\"OPEN\"", "status": 200, "response": {"values": []}}`,
		`{"method": "POST", "path": "/2.0/repositories/acme/web/pullrequests", "request": {"title": "Add search", "description": "Adds search.", "source": {"branch": {"name": "fugaro/20260927-101500-abcd"}}, "destination": {"branch": {"name": "main"}}, "draft": true, "close_source_branch": true}, "status": 201, "response": `+prJSON("OPEN", true, "Add search", "Adds search.", "[]")+`}`)
	pr, err := p.EnsurePR(ctx, spec(true, "{5b1d0c7e-0000-4000-8000-000000000001}"))
	if err != nil || pr != (gitprov.PR{Number: 12, URL: "https://bitbucket.org/acme/web/pull-requests/12", Draft: true}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
	if len(warned) != 0 {
		t.Fatalf("a draft drops its labels silently, warned: %v", warned)
	}
}

func TestUpdatePR(t *testing.T) {
	const url = "https://bitbucket.org/acme/web/pull-requests/12"
	rev := `[{"uuid": "{aaa}", "account_id": "1:a"}]`
	tests := []struct {
		name      string
		exchanges []string
		update    gitprov.PRUpdate
		want      gitprov.PR
		wantErr   error
		errText   string
	}{
		{name: "body: PUT re-sends title, description and the draft flag, no reviewers",
			exchanges: []string{getPR("OPEN", true, "T", "old", rev),
				put(`{"title": "T", "description": "new", "draft": true}`, prJSON("OPEN", true, "T", "new", rev))},
			update: gitprov.PRUpdate{Body: ptr("new")}, want: gitprov.PR{Number: 12, URL: url, Draft: true}},
		{name: "title: PUT keeps the current description",
			exchanges: []string{getPR("OPEN", false, "T", "keep me", rev),
				put(`{"title": "T2", "description": "keep me", "draft": false}`, prJSON("OPEN", false, "T2", "keep me", rev))},
			update: gitprov.PRUpdate{Title: ptr("T2")}, want: gitprov.PR{Number: 12, URL: url}},
		{name: "nothing changes: no PUT",
			exchanges: []string{getPR("OPEN", true, "T", "same", rev)},
			update:    gitprov.PRUpdate{Title: ptr("T"), Body: ptr("same")}, want: gitprov.PR{Number: 12, URL: url, Draft: true}},
		{name: "prefix-marked draft keeps its prefix and a false flag",
			exchanges: []string{getPR("OPEN", false, "[DRAFT] T", "d", "[]"),
				put(`{"title": "[DRAFT] T2", "description": "d", "draft": false}`, prJSON("OPEN", false, "[DRAFT] T2", "d", "[]"))},
			update: gitprov.PRUpdate{Title: ptr("T2")}, want: gitprov.PR{Number: 12, URL: url, Draft: true, DraftFallback: true}},
		{name: "closed PR is not written",
			exchanges: []string{getPR("DECLINED", false, "T", "d", "[]")},
			update:    gitprov.PRUpdate{Body: ptr("new")}, want: gitprov.PR{Number: 12, URL: url}, wantErr: gitprov.ErrPRNotOpen},
		{name: "ready flip after a body change is a second PUT",
			exchanges: []string{getPR("OPEN", true, "T", "old", "[]"),
				put(`{"title": "T", "description": "new", "draft": true}`, prJSON("OPEN", true, "T", "new", "[]")),
				put(`{"title": "T", "draft": false}`, prJSON("OPEN", false, "T", "new", "[]"))},
			update: gitprov.PRUpdate{Body: ptr("new"), Draft: ptr(false)}, want: gitprov.PR{Number: 12, URL: url}},
		{name: "PUT failure is a plain error",
			exchanges: []string{getPR("OPEN", false, "T", "old", "[]"),
				`{"method": "PUT", "path": "/2.0/repositories/acme/web/pullrequests/12", "status": 500, "response": {}}`},
			update: gitprov.PRUpdate{Body: ptr("new")}, want: gitprov.PR{Number: 12, URL: url}, errText: "updating pull request #12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := openInline(t, nil, tt.exchanges...).UpdatePR(ctx, 12, tt.update)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.errText != "":
				var partial *gitprov.PartialError
				if err == nil || !strings.Contains(err.Error(), tt.errText) || errors.As(err, &partial) {
					t.Fatalf("err = %v, want a plain error containing %q", err, tt.errText)
				}
			case err != nil:
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("pr = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestUpdatePRChangesBodyOnly(t *testing.T) {
	// Replays the recorded PR 16 (draft false, with a description) and
	// checks the PUT carries the title and draft flag it already has.
	p := openInline(t, nil,
		getPR("OPEN", false, "Add search", "Adds search.", "[]"),
		put(`{"title": "Add search", "description": "Adds search.\n\n### Status", "draft": false}`, prJSON("OPEN", false, "Add search", "Adds search.\n\n### Status", "[]")))
	pr, err := p.UpdatePR(ctx, 12, gitprov.PRUpdate{Body: ptr("Adds search.\n\n### Status")})
	if err != nil || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestPullRequestReturnsBody(t *testing.T) {
	p := openInline(t, nil, getPR("OPEN", false, "Add search", "Line one.\n\nLine two.", "[]"))
	info, err := p.PullRequest(ctx, 12)
	if err != nil || info.Body != "Line one.\n\nLine two." || info.Title != "Add search" {
		t.Fatalf("info = %+v, %v", info, err)
	}
}

func TestApplyReady(t *testing.T) {
	existing := `[{"uuid": "{aaa}", "account_id": "1:a"}]`
	tests := []struct {
		name      string
		exchanges []string
		reviewers []string
		labels    []string
		wantErr   error
		partial   string
		warns     int
	}{
		{name: "adds new reviewers beside the existing ones, keeps text and draft flag",
			exchanges: []string{getPR("OPEN", false, "T", "d", existing),
				put(`{"title": "T", "description": "d", "draft": false, "reviewers": [{"uuid": "{aaa}"}, {"uuid": "{bbb}"}, {"account_id": "2:b"}]}`, prJSON("OPEN", false, "T", "d", "[]"))},
			reviewers: []string{"{aaa}", "{bbb}", "2:b"}},
		{name: "already reviewers: no PUT, UUID case ignored",
			exchanges: []string{getPR("OPEN", false, "T", "d", existing)}, reviewers: []string{"{AAA}", "1:a"}},
		{name: "labels only: warned, no write",
			exchanges: []string{getPR("OPEN", false, "T", "d", "[]")}, labels: []string{"fugaro"}, warns: 1},
		{name: "closed", exchanges: []string{getPR("MERGED", false, "T", "d", "[]")}, reviewers: []string{"{bbb}"}, wantErr: gitprov.ErrPRNotOpen},
		{name: "Malformed reviewers list is partial and the PR stays as it is",
			exchanges: []string{getPR("OPEN", false, "T", "d", "[]"),
				`{"method": "PUT", "path": "/2.0/repositories/acme/web/pullrequests/12", "status": 400, "response": {"type": "error", "error": {"message": "reviewers: Malformed reviewers list"}}}`},
			reviewers: []string{"557058:gone"}, partial: "557058:gone"},
		{name: "other failure is partial too",
			exchanges: []string{getPR("OPEN", false, "T", "d", "[]"),
				`{"method": "PUT", "path": "/2.0/repositories/acme/web/pullrequests/12", "status": 503, "response": {}}`},
			reviewers: []string{"{bbb}"}, partial: "requesting reviewers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warns := 0
			err := openInline(t, func(string) { warns++ }, tt.exchanges...).ApplyReady(ctx, 12, tt.reviewers, tt.labels)
			var partial *gitprov.PartialError
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.partial != "":
				if !errors.As(err, &partial) || !strings.Contains(err.Error(), tt.partial) {
					t.Fatalf("err = %v, want a PartialError containing %q", err, tt.partial)
				}
			case err != nil:
				t.Fatal(err)
			}
			if warns != tt.warns {
				t.Fatalf("warnings = %d, want %d", warns, tt.warns)
			}
		})
	}
}

// TestApplyReadyIdempotent: after the first call the PR lists the
// reviewers, so the second call writes nothing; the labels warning is
// given once across both.
func TestApplyReadyIdempotent(t *testing.T) {
	warns := 0
	p := openInline(t, func(string) { warns++ },
		getPR("OPEN", false, "T", "d", "[]"),
		put(`{"title": "T", "description": "d", "draft": false, "reviewers": [{"uuid": "{bbb}"}]}`, prJSON("OPEN", false, "T", "d", `[{"uuid": "{bbb}"}]`)),
		getPR("OPEN", false, "T", "d", `[{"uuid": "{bbb}"}]`))
	for i := 0; i < 2; i++ {
		if err := p.ApplyReady(ctx, 12, []string{"{bbb}"}, []string{"fugaro"}); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if warns != 1 {
		t.Fatalf("warnings = %d, want 1", warns)
	}
}

// TestBitbucketReadyPutCarriesReviewers: the ready flip itself (EnsurePR)
// sends no reviewers; ApplyReady's PUT afterwards carries them together
// with the description and the now-ready draft flag. Replayed from the
// recorded sandbox PR shapes, with a hand-written reviewers PUT (A3).
func TestBitbucketReadyPutCarriesReviewers(t *testing.T) {
	p := openRecorded(t, "existing_to_ready.json", "acme", "sandbox", nil)
	pr, err := p.EnsurePR(ctx, recSpec(false))
	if err != nil || pr.Draft {
		t.Fatalf("flip: %+v, %v", pr, err)
	}
	// The recorded PR already lists the reviewer, so a second exchange set
	// is needed for the new one: serve it inline.
	q := openInline(t, nil,
		getPR("OPEN", false, "Fugaro live check", recBody, `[{"uuid": "{5a4d0b0e-0000-4000-8000-00000000a11c}"}]`),
		put(`{"title": "Fugaro live check", "description": `+fmt.Sprintf("%q", recBody)+`, "draft": false, "reviewers": [{"uuid": "{5a4d0b0e-0000-4000-8000-00000000a11c}"}, {"uuid": "{other}"}]}`, prJSON("OPEN", false, "Fugaro live check", recBody, "[]")))
	if err := q.ApplyReady(ctx, 12, []string{recReviewer, "{other}"}, nil); err != nil {
		t.Fatal(err)
	}
}

// TestBitbucketDraftFallbackReported: Bitbucket not honouring draft puts
// the prefix in the title and the PR says so.
func TestBitbucketDraftFallbackReported(t *testing.T) {
	pr, err := open(t, "draft_ignored.json").EnsurePR(ctx, spec(true))
	if err != nil || !pr.Draft || !pr.DraftFallback {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
	if pr, err = open(t, "create_draft.json").EnsurePR(ctx, spec(true)); err != nil || pr.DraftFallback {
		t.Fatalf("real draft: pr = %+v, %v", pr, err)
	}
}
