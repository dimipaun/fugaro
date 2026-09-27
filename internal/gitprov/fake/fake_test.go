package fake

import (
	"context"
	"path/filepath"
	"testing"

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
