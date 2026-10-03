package runner_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestFailedStageLogTailInDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	secret := envValue(h.deps.Env, "ANTHROPIC_API_KEY")
	crash := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for i := 1; i <= 50; i++ {
			fmt.Fprintf(req.Stderr, "line %02d\n", i)
		}
		fmt.Fprintf(req.Stderr, "fatal: bad key %s\n", secret)
		return agent.Result{}, errors.New("claude exited with status 1")
	}
	rec, err := h.run(t, crash)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "stage implement failed: claude exited with status 1" {
		t.Fatalf("record = %+v", rec)
	}
	report := onlyPR(t, h.provider).Comments[0]
	for _, want := range []string{"**Log tail** (stage implement-1, stderr):", "line 12", "line 50", "fatal: bad key [REDACTED]"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	// 51 lines were written; only the last 40 are kept.
	if strings.Contains(report, "line 11") || strings.Contains(report, secret) {
		t.Fatalf("report keeps too much, or leaks the secret:\n%s", report)
	}
}

func TestFailedVerifyLogTailInDraft(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "test: sh test.sh", "test: echo running the suite; sh test.sh", 1)
	h := newHarness(t, cfg, nil)
	h.fails(t, "beta")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Reason != "tests failing on the final commit" {
		t.Fatalf("record = %+v", rec)
	}
	report := onlyPR(t, h.provider).Comments[0]
	if !strings.Contains(report, "**Log tail** (fugaro verify test #1):") || !strings.Contains(report, "running the suite") {
		t.Fatalf("report lacks the verify tail:\n%s", report)
	}
}

func TestReadyReportHasNoLogTail(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if report := onlyPR(t, h.provider).Comments[0]; strings.Contains(report, "Log tail") {
		t.Fatalf("a ready PR's report has a log tail:\n%s", report)
	}
}

// TestReviewDraftWithPassingTestsHasNoLogTail: a draft caused by review
// findings, with the last verify passing, has no failure output to show.
func TestReviewDraftWithPassingTestsHasNoLogTail(t *testing.T) {
	h := newHarness(t, "", nil)
	fix := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo more >> feature.txt && git commit -qam 'Try again'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, implement("feature"), review("changes", 1), fix, review("changes", 1))
	if err != nil || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if report := onlyPR(t, h.provider).Comments[0]; strings.Contains(report, "Log tail") {
		t.Fatalf("report has a log tail:\n%s", report)
	}
}

// TestLongPRTextIsBounded covers an agent whose pr.md exceeds what the
// providers accept: the PR still opens, with the text clipped.
func TestLongPRTextIsBounded(t *testing.T) {
	h := newHarness(t, "", nil)
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		text := "# " + strings.Repeat("t", 300) + "\n\n" + strings.Repeat("é", 50_000)
		if werr := os.WriteFile(filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md"), []byte(text), 0o644); werr != nil {
			t.Fatal(werr)
		}
		return res, err
	}
	if _, err := h.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	pr := onlyPR(t, h.provider)
	if title := []rune(pr.Title); len(title) != 200 || title[199] != '…' {
		t.Fatalf("title has %d runes, ends %q", len(title), string(title[len(title)-1]))
	}
	if len(pr.Body) > gitprov.GitHubBodyLimit {
		t.Fatalf("body with its status section is %d bytes, over the limit", len(pr.Body))
	}
	body := gitprov.StripStatus(pr.Body)
	if len(body) > 60000 || !strings.HasSuffix(body, "longer than a pull request allows.)*") || !strings.HasPrefix(body, "éé") {
		t.Fatalf("body is %d bytes, ends %q", len(body), body[len(body)-40:])
	}
	if !utf8Valid(body) {
		t.Fatal("clipping split a rune")
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "\uFFFD") == s }

// TestVerifyLogTailRedactedBeforeClipping covers a secret straddling the
// published line width: the verify log keeps the whole line, and the
// runner redacts it before clipping, so no prefix of the secret leaks.
func TestVerifyLogTailRedactedBeforeClipping(t *testing.T) {
	h := newHarness(t, "", nil)
	secret := envValue(h.deps.Env, "ANTHROPIC_API_KEY")
	pad := strings.Repeat("x", 296) // the secret spans bytes 296-303 of the line
	h.files["fugaro.yaml"] = strings.Replace(h.files["fugaro.yaml"], "test: sh test.sh", "test: echo "+pad+secret+"; sh test.sh", 1)
	h.deps.Remote = testutil.NewRemote(t, h.files)
	h.remote = h.deps.Remote
	h.provider.Remote = h.remote
	h.fails(t, "beta")
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	report := onlyPR(t, h.provider).Comments[0]
	if !strings.Contains(report, "**Log tail** (fugaro verify test #1):") {
		t.Fatalf("report lacks the verify tail:\n%s", report)
	}
	if strings.Contains(report, pad+secret[:1]) {
		t.Fatalf("report leaks a prefix of the secret:\n%s", report)
	}
	if !strings.Contains(report, pad+"[RED …") {
		t.Fatalf("report lacks the redacted, clipped line:\n%s", report)
	}
}
