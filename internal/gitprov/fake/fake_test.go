package fake

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

func TestEnsurePRCreatesThenUpdates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "provider.json")
	p := &Provider{Path: path}
	pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x", Title: "T", Draft: true})
	if err != nil || pr.Number != 1 || !pr.Draft {
		t.Fatalf("create = %+v, %v", pr, err)
	}
	pr2, err := (&Provider{Path: path}).EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x", Title: "ignored", Draft: false})
	if err != nil || pr2.Number != 1 || pr2.Draft {
		t.Fatalf("update = %+v, %v", pr2, err)
	}
	if err := p.Comment(ctx, pr2, "report"); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil || len(st.PRs) != 1 || st.PRs[0].Spec.Title != "T" || st.PRs[0].Draft || len(st.PRs[0].Comments) != 1 {
		t.Fatalf("state = %+v, %v", st, err)
	}
}

func TestFailEnsureThenSucceed(t *testing.T) {
	ctx := context.Background()
	p := &Provider{FailEnsure: 1}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x"}); err == nil {
		t.Fatal("first EnsurePR should fail")
	}
	if pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x"}); err != nil || pr.Number != 1 {
		t.Fatalf("second EnsurePR = %+v, %v", pr, err)
	}
}

func TestGitAuth(t *testing.T) {
	ctx := context.Background()
	if a, err := (&Provider{}).GitAuth(ctx, time.Minute); err != nil || a.Token != "" {
		t.Fatalf("default GitAuth = %+v, %v", a, err)
	}
	var asked time.Duration
	p := &Provider{Auth: func(min time.Duration) gitprov.GitAuth {
		asked = min
		return gitprov.GitAuth{Username: "u", Token: "tok-1234"}
	}}
	if a, err := p.GitAuth(ctx, 45*time.Minute); err != nil || a.Token != "tok-1234" || asked != 45*time.Minute {
		t.Fatalf("GitAuth = %+v, %v (asked %s)", a, err, asked)
	}
}
