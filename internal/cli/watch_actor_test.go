package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/watch"
)

// A kill from the screen is recorded as the identity budget kill uses; with
// nobody identified it refuses and writes nothing (never "unknown").
func TestWatchActorAndExec(t *testing.T) {
	f := newBudgetFixture(t, "")
	t.Chdir(t.TempDir()) // not a git checkout
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	old := authenticatedUser
	t.Cleanup(func() { authenticatedUser = old })
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return "adc@example.com", nil }

	ctx := context.Background()
	if got := watchActor(ctx, &localcfg.Config{User: "cfg@example.com"}); got != "cfg@example.com" {
		t.Fatalf("configured user: got %q", got)
	}
	if got := watchActor(ctx, &localcfg.Config{}); got != "adc@example.com" {
		t.Fatalf("authenticated fallback: got %q", got)
	}
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return "", errors.New("no credentials") }
	if got := watchActor(ctx, &localcfg.Config{}); got != "" {
		t.Fatalf("nobody known: got %q, want empty", got)
	}

	db, err := rtdb.New(f.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	req := watch.Request{Kind: watch.KillRepo, Project: "aurora", Target: watch.Target{Slug: appSlug, Name: "acme/app"}}
	out := watchExec(ctx, db, req, "")
	if out.Err == nil || out.Written || !strings.Contains(out.Notice, "who you are") {
		t.Fatalf("anonymous exec = %+v", out)
	}
	if f.db.Value(budget.PathKillRepo(appSlug)) != nil {
		t.Fatal("an anonymous kill wrote a switch")
	}
	if out = watchExec(ctx, db, req, "cfg@example.com"); !out.Written {
		t.Fatalf("exec = %+v", out)
	}
	k, _ := f.db.Value(budget.PathKillRepo(appSlug)).(map[string]any)
	if k["by"] != "cfg@example.com" {
		t.Fatalf("switch = %v", k)
	}
}
