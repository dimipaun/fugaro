package github

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

const branchName = "fugaro/20260927-101500-abcd"

var ctx = context.Background()

func open(t *testing.T, fixture string) *Provider {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func spec(draft bool, labels, reviewers []string) gitprov.PRSpec {
	return gitprov.PRSpec{Branch: branchName, Base: "main", Title: "Add search", Body: "Adds search.",
		Draft: draft, Labels: labels, Reviewers: reviewers}
}

func TestCreateWithLabelsAndReviewers(t *testing.T) {
	p := open(t, "create_ready.json")
	pr, err := p.EnsurePR(ctx, spec(false, []string{"fugaro"}, []string{"octocat", "acme/platform"}))
	if err != nil || pr != (gitprov.PR{Number: 12, URL: "https://github.com/acme/web/pull/12"}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestDraftUnsupportedFallsBackToTitle covers private repositories whose
// plan has no draft pull requests: the run still gets its PR, marked as a
// draft in the title (design §15).
func TestDraftUnsupportedFallsBackToTitle(t *testing.T) {
	p := open(t, "draft_unsupported.json")
	if pr, err := p.EnsurePR(ctx, spec(true, nil, nil)); err != nil || pr.Number != 16 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestAgentOpenedPRIsOnlyToggled covers a pull request the agent opened
// itself with gh: it is found by branch and converted to a draft, with no
// second pull request and no change to its title, body or labels.
func TestAgentOpenedPRIsOnlyToggled(t *testing.T) {
	p := open(t, "existing_to_draft.json")
	if pr, err := p.EnsurePR(ctx, spec(true, []string{"fugaro"}, []string{"octocat"})); err != nil || pr.Number != 13 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestExistingPRDraftUnsupportedRetitles(t *testing.T) {
	p := open(t, "existing_draft_unsupported.json")
	if pr, err := p.EnsurePR(ctx, spec(true, nil, nil)); err != nil || pr.Number != 17 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestExistingPrefixedDraftUnsupportedSkipsPATCH covers a pull request
// already carrying the "[DRAFT] " fallback prefix (from an earlier run on a
// repository without draft support) when a draft is wanted again: the
// title is already right, so no retitle PATCH is sent. The fixture ends at
// the GraphQL call, so httpfixture would fail the test on a PATCH.
func TestExistingPrefixedDraftUnsupportedSkipsPATCH(t *testing.T) {
	p := open(t, "existing_prefixed_draft_unsupported.json")
	pr, err := p.EnsurePR(ctx, spec(true, nil, nil))
	if err != nil || pr != (gitprov.PR{Number: 18, URL: "https://github.com/acme/web/pull/18", Draft: true}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestExistingDraftMarkedReady(t *testing.T) {
	p := open(t, "existing_to_ready.json")
	if pr, err := p.EnsurePR(ctx, spec(false, nil, nil)); err != nil || pr.Number != 15 || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestReadyRemovesDraftPrefix(t *testing.T) {
	p := open(t, "existing_prefix_to_ready.json")
	if pr, err := p.EnsurePR(ctx, spec(false, nil, nil)); err != nil || pr.Number != 16 || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestMarkReadyFailureIsPartial covers the direction rule (gitprov.go,
// PartialError): a failed markPullRequestReadyForReview leaves the pull
// request looking like a draft, the conservative direction, so it is a
// *gitprov.PartialError with the populated (still-draft) PR rather than an
// empty one.
func TestMarkReadyFailureIsPartial(t *testing.T) {
	p := open(t, "mark_ready_fails.json")
	pr, err := p.EnsurePR(ctx, spec(false, nil, nil))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 15 || !pr.Draft || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestRetitleFailureStrippingPrefixIsPartial covers "ready wanted, not a
// draft, prefixed title": the shared retitle step only ever strips the
// "[DRAFT] " prefix, so a failure there leaves the title looking more
// draft-like than requested — the conservative direction — and must be a
// *gitprov.PartialError reporting the draft-looking state, not a plain
// error or an empty PR.
func TestRetitleFailureStrippingPrefixIsPartial(t *testing.T) {
	p := open(t, "existing_prefix_retitle_fails.json")
	pr, err := p.EnsurePR(ctx, spec(false, nil, nil))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 16 || !pr.Draft || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestDraftConversion5xxIsPlainError covers a GraphQL failure that is not
// GitHub saying drafts are unsupported (here, a 500): the title fallback
// must not be attempted (the fixture holds no PATCH exchange), and the
// existing, unmodified PR is returned alongside a plain, retryable error.
func TestDraftConversion5xxIsPlainError(t *testing.T) {
	p := open(t, "draft_conversion_5xx.json")
	pr, err := p.EnsurePR(ctx, spec(true, nil, nil))
	var partial *gitprov.PartialError
	if err == nil || errors.As(err, &partial) || pr.Number != 20 || pr.Draft {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestReviewerTeamMustBeOwnersTeam covers the "org/team" reviewer form: a
// team outside the repository's own owner cannot be requested, so it must
// be rejected clearly (as a PartialError alongside the created PR) rather
// than silently asking a same-named team in the wrong organization.
func TestReviewerTeamMustBeOwnersTeam(t *testing.T) {
	p := open(t, "reviewer_wrong_org.json")
	pr, err := p.EnsurePR(ctx, spec(false, nil, []string{"otherorg/platform"}))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 21 || !strings.Contains(err.Error(), "otherorg/platform") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestDraftAndRetitleFailIsPlainError covers the unsafe direction: neither
// the GraphQL conversion to a real draft nor the title-prefix fallback
// succeeds, so the pull request is left looking fully ready though a draft
// was requested. That must not be a *gitprov.PartialError (which the runner
// treats as done): it is a plain, retryable error, with the existing,
// still-unmodified PR populated for the caller.
func TestDraftAndRetitleFailIsPlainError(t *testing.T) {
	p := open(t, "draft_and_retitle_fail.json")
	pr, err := p.EnsurePR(ctx, spec(true, nil, nil))
	var partial *gitprov.PartialError
	if err == nil || errors.As(err, &partial) || pr.Number != 19 || pr.Draft {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

func TestLabelFailureIsPartial(t *testing.T) {
	p := open(t, "labels_forbidden.json")
	pr, err := p.EnsurePR(ctx, spec(false, []string{"fugaro"}, nil))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 18 || !strings.Contains(err.Error(), "403") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

func TestComment(t *testing.T) {
	p := open(t, "comment.json")
	if err := p.Comment(ctx, gitprov.PR{Number: 12}, "### Fugaro run report"); err != nil {
		t.Fatal(err)
	}
}

// TestGitAuthSetsGHToken uses a fixed clock (see open), so the second
// GitAuth's minValid must sit strictly between the two fixture tokens' full
// lifetimes (60m for the first, 80m for the second): long enough that the
// cached first token can no longer satisfy it, forcing a remint, but short
// enough that the freshly minted second token can (round-1 hardening,
// apptoken.go: a token minted with less life left than minValid is an
// error, so 90m — beyond even the second token's 80m — would not do).
func TestGitAuthSetsGHToken(t *testing.T) {
	p := open(t, "token_refresh.json")
	a, err := p.GitAuth(ctx, 45*time.Minute)
	if err != nil || a.Username != "x-access-token" || a.Token != "ghs_fixture_token_1" || a.Env["GH_TOKEN"] != a.Token || a.Expires.IsZero() {
		t.Fatalf("auth = %+v, %v", a, err)
	}
	if a, err := p.GitAuth(ctx, 75*time.Minute); err != nil || a.Token != "ghs_fixture_token_2" {
		t.Fatalf("second auth = %+v, %v", a, err)
	}
}

func TestGraphQLURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.github.com":         "https://api.github.com/graphql",
		"https://ghe.example.com/api/v3": "https://ghe.example.com/api/graphql",
		"http://127.0.0.1:5555":          "http://127.0.0.1:5555/graphql",
	} {
		if got := graphqlURL(in); got != want {
			t.Errorf("graphqlURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewValidates(t *testing.T) {
	for _, o := range []Options{{Repo: "web", AppID: "1", PrivateKey: key(t)}, {Owner: "acme", Repo: "web", PrivateKey: key(t)}, {Owner: "acme", Repo: "web", AppID: "1"}} {
		if _, err := New(o); err == nil {
			t.Errorf("New(%+v) succeeded", o)
		}
	}
}
