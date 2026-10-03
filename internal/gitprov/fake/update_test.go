package fake

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

func ptr[T any](v T) *T { return &v }

func ops(p *Provider) []string {
	var out []string
	for _, c := range p.State.Calls {
		out = append(out, c.Op)
	}
	return out
}

func TestDraftCarriesNoReviewersUntilApplyReady(t *testing.T) {
	ctx := context.Background()
	p := &Provider{Path: filepath.Join(t.TempDir(), "p.json")}
	spec := gitprov.PRSpec{Branch: "fugaro/x", Title: "T", Body: "B", Draft: true, Reviewers: []string{"alice"}, Labels: []string{"fugaro"}}
	pr, err := p.EnsurePR(ctx, spec)
	if err != nil || !pr.Draft {
		t.Fatalf("create: %+v, %v", pr, err)
	}
	st, _ := Load(p.Path)
	if len(st.PRs[0].Reviewers)+len(st.PRs[0].Labels) != 0 {
		t.Fatalf("draft has reviewers or labels: %+v", st.PRs[0])
	}
	// ApplyReady on a draft is a runner bug the fake reports.
	if err := p.ApplyReady(ctx, pr.Number, []string{"alice"}, nil); err == nil {
		t.Fatal("ApplyReady on a draft should fail")
	}
	spec.Draft = false
	if pr, err = p.EnsurePR(ctx, spec); err != nil || pr.Draft {
		t.Fatalf("flip: %+v, %v", pr, err)
	}
	st, _ = Load(p.Path)
	if len(st.PRs[0].Reviewers) != 0 {
		t.Fatalf("the flip alone must not request reviewers: %+v", st.PRs[0])
	}
	for i := 0; i < 2; i++ { // idempotent
		if err := p.ApplyReady(ctx, pr.Number, []string{"alice", "bob"}, []string{"fugaro"}); err != nil {
			t.Fatal(err)
		}
	}
	st, _ = Load(p.Path)
	if !slices.Equal(st.PRs[0].Reviewers, []string{"alice", "bob"}) || !slices.Equal(st.PRs[0].Labels, []string{"fugaro"}) {
		t.Fatalf("after ApplyReady twice: %+v", st.PRs[0])
	}
}

func TestReadyCreateAppliesReviewers(t *testing.T) {
	p := &Provider{}
	if _, err := p.EnsurePR(context.Background(), gitprov.PRSpec{Branch: "b", Title: "T", Reviewers: []string{"alice"}, Labels: []string{"l"}}); err != nil {
		t.Fatal(err)
	}
	if got := p.State.PRs[0]; !slices.Equal(got.Reviewers, []string{"alice"}) || !slices.Equal(got.Labels, []string{"l"}) {
		t.Fatalf("%+v", got)
	}
}

func TestUpdatePRAndBody(t *testing.T) {
	ctx := context.Background()
	p := &Provider{}
	pr, _ := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "b", Title: "T", Body: "B", Draft: true})
	info, err := p.PullRequest(ctx, pr.Number)
	if err != nil || info.Body != "B" || info.Title != "T" || !info.Draft {
		t.Fatalf("read: %+v, %v", info, err)
	}
	if _, err := p.UpdatePR(ctx, pr.Number, gitprov.PRUpdate{Body: ptr("B2")}); err != nil {
		t.Fatal(err)
	}
	info, _ = p.PullRequest(ctx, pr.Number)
	if info.Body != "B2" || info.Title != "T" || !info.Draft {
		t.Fatalf("after body update: %+v", info)
	}
	if _, err := p.UpdatePR(ctx, pr.Number, gitprov.PRUpdate{Title: ptr("T2"), Draft: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	info, _ = p.PullRequest(ctx, pr.Number)
	if info.Body != "B2" || info.Title != "T2" || info.Draft {
		t.Fatalf("after title+ready: %+v", info)
	}
	p.State.PRs[0].State = gitprov.PRClosed
	if _, err := p.UpdatePR(ctx, pr.Number, gitprov.PRUpdate{Body: ptr("x")}); !errors.Is(err, gitprov.ErrPRNotOpen) {
		t.Fatalf("closed: %v", err)
	}
	if err := p.ApplyReady(ctx, pr.Number, nil, nil); !errors.Is(err, gitprov.ErrPRNotOpen) {
		t.Fatalf("closed ApplyReady: %v", err)
	}
	if _, err := p.UpdatePR(ctx, 99, gitprov.PRUpdate{}); err == nil {
		t.Fatal("unknown PR should fail")
	}
}

func TestNoDraftsFallback(t *testing.T) {
	ctx := context.Background()
	p := &Provider{NoDrafts: true}
	pr, _ := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "b", Title: "T", Draft: true})
	if !pr.Draft || !pr.DraftFallback {
		t.Fatalf("%+v", pr)
	}
	if _, err := p.UpdatePR(ctx, pr.Number, gitprov.PRUpdate{Title: ptr("T2")}); err != nil {
		t.Fatal(err)
	}
	if info, _ := p.PullRequest(ctx, pr.Number); info.Title != "[DRAFT] T2" || !info.Draft {
		t.Fatalf("%+v", info)
	}
	if pr, _ = p.EnsurePR(ctx, gitprov.PRSpec{Branch: "b", Draft: false}); pr.Draft || pr.DraftFallback {
		t.Fatalf("ready: %+v", pr)
	}
	if info, _ := p.PullRequest(ctx, pr.Number); info.Title != "T2" {
		t.Fatalf("prefix not stripped: %+v", info)
	}
}

func TestRejectedReviewerIsPartial(t *testing.T) {
	ctx := context.Background()
	p := &Provider{RejectReviewers: []string{"ghost"}}
	pr, _ := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "b", Title: "T"})
	var partial *gitprov.PartialError
	if err := p.ApplyReady(ctx, pr.Number, []string{"alice", "ghost"}, []string{"l"}); !errors.As(err, &partial) {
		t.Fatalf("err = %v", err)
	}
	if got := p.State.PRs[0]; !slices.Equal(got.Reviewers, []string{"alice"}) || !slices.Equal(got.Labels, []string{"l"}) {
		t.Fatalf("%+v", got)
	}
}

func TestInjectedFailures(t *testing.T) {
	ctx := context.Background()
	p := &Provider{FailUpdatePR: 1, FailApplyReady: 1}
	pr, _ := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "b", Title: "T", Body: "B"})
	if _, err := p.UpdatePR(ctx, pr.Number, gitprov.PRUpdate{Body: ptr("x")}); err == nil {
		t.Fatal("first UpdatePR should fail")
	}
	if info, _ := p.PullRequest(ctx, pr.Number); info.Body != "B" {
		t.Fatalf("failed update changed the body: %+v", info)
	}
	if err := p.ApplyReady(ctx, pr.Number, []string{"a"}, nil); err == nil {
		t.Fatal("first ApplyReady should fail")
	}
	if len(p.State.PRs[0].Reviewers) != 0 {
		t.Fatal("failed ApplyReady applied reviewers")
	}
	if err := p.ApplyReady(ctx, pr.Number, []string{"a"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCallLogOrder(t *testing.T) {
	ctx := context.Background()
	p := &Provider{Path: filepath.Join(t.TempDir(), "p.json")}
	pr, _ := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "b", Title: "T", Draft: true})
	_, _ = p.PullRequest(ctx, pr.Number)
	_, _ = p.UpdatePR(ctx, pr.Number, gitprov.PRUpdate{Body: ptr("x"), Draft: ptr(false)})
	_ = p.ApplyReady(ctx, pr.Number, []string{"a"}, nil)
	_ = p.Comment(ctx, pr, "report")
	want := []string{"EnsurePR", "UpdatePR", "ApplyReady", "Comment"}
	if got := ops(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	st, _ := Load(p.Path) // another process sees the same log
	if len(st.Calls) != len(want) || st.Calls[1].Detail != "body,draft=false" {
		t.Fatalf("persisted calls = %+v", st.Calls)
	}
}
