package cli

import (
	"context"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// Every write a launcher makes is under runs/, so the 0.7.0 grants (writes
// denied everywhere else) leave launching, the claim takeover and release,
// cancelling and the token object's cleanup working (bucket-iam.md §2.2).
func TestLauncherGrantsCoverEveryLauncherWrite(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	for _, p := range []string{"fugaro/", "builds/", "cache/", "locks/"} {
		fake.DenyWrites("fugaro-runs-proj-1234", p)
	}
	b := fake.Bucket(t, "fugaro-runs-proj-1234")
	const slug, run = "acme-web-0123456789abcdef", "20261008-120000-abcd"
	s := runstore.Open(b.Bucket, slug, run)
	if err := s.CreateTask(ctx, &task.Spec{RunID: run, Repo: "acme/web"}); err != nil {
		t.Fatalf("task.json: %v", err)
	}
	if ok, _, err := s.Claim(ctx, "me", time.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	data, gen, err := b.Read(ctx, s.ClaimKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReplaceIf(ctx, s.ClaimKey(), data, gen, data); err != nil {
		t.Fatalf("claim takeover: %v", err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{}); err != nil {
		t.Fatalf("launch.json: %v", err)
	}
	if err := s.RequestCancel(ctx); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := token.DeleteObject(ctx, b, slug, run); err != nil {
		t.Fatalf("token cleanup: %v", err)
	}
	_, gen, _ = b.Read(ctx, s.ClaimKey())
	if err := b.DeleteIf(ctx, s.ClaimKey(), gen, nil); err != nil {
		t.Fatalf("claim release: %v", err)
	}
}
