package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// fakeBudgetToken builds a string token.PutObject accepts for slug/run:
// token.checkNames only base64-decodes the middle segment as JSON and reads
// uid/claims.fs/claims.fr, never verifying a signature, so a real mint
// (which needs a signer) is not needed here.
func fakeBudgetToken(slug, run string) string {
	payload, _ := json.Marshal(map[string]any{
		"uid":    token.UID(slug, run),
		"claims": map[string]any{"fs": slug, "fr": run},
	})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
}

// Every write a launcher makes is under runs/ or tokens/, so the launchers'
// conditioned write (gcp.LauncherBucketCondition: runs/ and, since the budget
// token moved out of runs/, tokens/ — docs/design/budget-token-channel.md
// §7.2) with writes denied everywhere else leaves launching, the claim
// takeover and release, cancelling, the sealed token object and its cleanup
// working (bucket-iam.md §2.2).
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
	key, err := token.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := token.PutObject(ctx, b, slug, run, fakeBudgetToken(slug, run), key); err != nil {
		t.Fatalf("budget token: %v", err)
	}
	if err := token.DeleteObject(ctx, b, slug, run); err != nil {
		t.Fatalf("token cleanup: %v", err)
	}
	_, gen, _ = b.Read(ctx, s.ClaimKey())
	if err := b.DeleteIf(ctx, s.ClaimKey(), gen, nil); err != nil {
		t.Fatalf("claim release: %v", err)
	}
}
