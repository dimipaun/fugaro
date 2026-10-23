package runner_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// pngReviewer is the model the failing run's project config pins as its
// reviewer: a provider route's model, priced by model_prices and by nothing
// else, so its price row carries token rates and no image rate (run
// 20261021-002046-31cc, `images aren't priced for model
// deepseek/deepseek-v4.1-flash`).
const pngReviewer = "deepseek/deepseek-v4.1-flash"

// pngFixture is a tiny generated PNG — the format's own signature, an IHDR
// header and a scrap of IDAT — and not a real image: what matters is the NUL
// byte in it, git's own test for a binary blob. It is the file the failing
// run's pull request added under mobile/assets/.
const pngFixture = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0aIDAT\x08\xd7\x63\x00\x01\x00\x00\x05\x00\x01\x0d\x0a\x2d\xb4\x00\x00\x00\x00IEND\xae\x42\x60\x82"

// implementPNG is the failing run's implement stage: it commits a PNG and a
// line of text beside it, the way a pull request that adds an app icon does,
// verifies and writes pr.md.
func implementPNG(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	icon := filepath.Join(req.Dir, "mobile", "assets", "icon.png")
	if err := os.MkdirAll(filepath.Dir(icon), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(icon, []byte(pngFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(req.Dir, "mobile", "README.md"), []byte("The app icon is in assets/.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shell(t, req, "git add -A && git commit -qm 'Add the app icon'")
	verifyTest(t, ctx, req)
	pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
	if err := os.WriteFile(pr, []byte("# The app icon\n\nAdds mobile/assets/icon.png."), 0o644); err != nil {
		t.Fatal(err)
	}
	return agent.Result{CostUSD: 1}, nil
}

// fixWithPNG is a fix stage that commits a PNG, the way a fix for a finding
// about a missing icon would, and reports what a fix stage reports.
func fixWithPNG(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	icon := filepath.Join(req.Dir, "mobile", "assets", "icon.png")
	if err := os.MkdirAll(filepath.Dir(icon), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(icon, []byte(pngFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	shell(t, req, "git add -A && git commit -qm 'Add the icon the review asked for'")
	verifyTest(t, ctx, req)
	return agent.Result{CostUSD: 0.2}, nil
}

// capturePrompt is a review step that records the prompt it was given and
// answers with verdict and findings.
func capturePrompt(prompts *[]string, verdict string, findings int) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		*prompts = append(*prompts, req.Prompt)
		return review(verdict, findings)(t, ctx, req)
	}
}

// TestReviewInputIsRebuiltForEveryRound: a fix between two review rounds
// changes the branch, so the second round's prompt carries the new file's
// marker — the input is built per round, never once for the whole loop. A
// stale diff would tell the reviewer the icon is missing in the very round
// that reviews the fix for it.
//
// Mutation (build the input once, in agentLoop, before the loops): the second
// round's prompt carries no marker and this test fails.
func TestReviewInputIsRebuiltForEveryRound(t *testing.T) {
	h := newHarness(t, "", nil)
	var prompts []string
	rec, err := h.run(t,
		implement("feature"),
		capturePrompt(&prompts, "changes", 1),
		fixWithPNG,
		capturePrompt(&prompts, "ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(prompts) != 2 {
		t.Fatalf("the run made %d review calls, want 2", len(prompts))
	}
	marker := fmt.Sprintf(`binary file "mobile/assets/icon.png" changed, %d bytes`, len(pngFixture))
	if strings.Contains(prompts[0], marker) {
		t.Fatalf("the first round's prompt names a file the branch did not have yet:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[1], marker) {
		t.Fatalf("the second round's prompt does not name the file the fix added (%q):\n%s", marker, prompts[1])
	}
}

// postPrompt sends the stage's prompt to the gateway as Claude Code would:
// the prompt is the user message's content, and that is the body the gateway
// prices and forwards.
func postPrompt(t *testing.T, req agent.Request, model string, maxTokens int64) (int, string) {
	t.Helper()
	base, key := gwEnv(req)
	content, err := json.Marshal(req.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"model":%q,"max_tokens":%d,"messages":[{"role":"user","content":%s}]}`, model, maxTokens, content)
	return postBody(t, base+"/v1/messages", key, body)
}

// sentText is what a request body actually asks the model: its user
// messages' content, decoded. A body that carries a file's bytes keeps them
// — NULs and all — only in here: the JSON around them escapes every byte
// that would end a string, so an assertion made on the encoded body cannot
// see them.
func sentText(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("the request body is not a Messages body: %v\n%s", err, body)
	}
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Content)
	}
	return b.String()
}

// reviewThePrompt is a review step that makes the call Claude Code would make
// — the stage's own prompt, as the user message — records the status and the
// body the model answered with, and returns a ship verdict.
func reviewThePrompt(status *int, body *string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		*status, *body = postPrompt(t, req, pngReviewer, 1000)
		fmt.Fprintf(req.Transcript, "{\"type\":\"user\",\"message\":%q}\n", *body)
		return review("ship", 0)(t, ctx, req)
	}
}

// TestReviewOfAPullRequestThatAddsAPngSendsNoImage is the failing run end to
// end: a pull request that adds a PNG, reviewed by a model priced only by
// model_prices, whose price row has no image rate. The review stage's call is
// accepted and forwarded, and what reaches the model is the diff as text —
// the PNG named in one line with its size, and no image block, no base64 and
// no raw bytes anywhere in the request.
//
// The reviewer's own price row is asserted to have no image rate, which is
// what makes "no refusal" mean something: this is the row an image block is
// refused for (internal/gateway/forward_test.go's
// TestImagesRefusedWithoutCeiling pins the refusal side of it).
//
// Mutation (hand the reviewer the file's bytes — make reviewinput.go's binary
// part a `git show` of the blob): the base64 and NUL assertions fail, and the
// gateway answers 400. Mutation (drop the built input from the review
// prompt): the marker assertion fails. Mutation (name every changed path in
// the diff, binaries included): the "Binary files ... differ" assertion
// fails.
func TestReviewOfAPullRequestThatAddsAPngSendsNoImage(t *testing.T) {
	cfg := gwConfig(t, "  first_line_review: off\n")
	cfg = strings.Replace(cfg, "coder: "+sonnet, "coder: "+pngReviewer, 1)
	cfg = strings.Replace(cfg, "reviewer: "+sonnet, "reviewer: "+pngReviewer, 1)
	cfg = strings.Replace(cfg, "background: "+haiku, "background: "+pngReviewer, 1)
	prices := `{"` + pngReviewer + `": {"input_per_m": 1, "output_per_m": 2}}`
	g := routeRun(t, cfg, "enforce", "50", []string{"deepseek/*"}, prices,
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(pngReviewer, pricing.Usage{Input: 1000, Output: 500})}, "acme/app")

	// The reviewer's price row: the owner's token rates, and no image rate.
	m, ok := g.deps.Spend.Prices.Lookup(pngReviewer)
	if !ok || m.ImageTokens != 0 {
		t.Fatalf("the reviewer's price row is %+v, want one with no image rate", m)
	}

	var status int
	var body string
	rec, err := g.run(t, implementPNG, reviewThePrompt(&status, &body))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if status != 200 {
		t.Fatalf("the review call got %d: %s", status, body)
	}
	if n := g.compat.Count(); n != 1 {
		t.Fatalf("the provider saw %d calls, want 1", n)
	}
	sent := string(g.compat.Body(0))
	text := sentText(t, g.compat.Body(0))
	marker := fmt.Sprintf(`binary file "mobile/assets/icon.png" changed, %d bytes`, len(pngFixture))
	if !strings.Contains(text, marker) {
		t.Errorf("the request does not name the PNG in one line (%q):\n%s", marker, text)
	}
	for _, bad := range []struct{ what, needle string }{
		{"an image block", `"type":"image"`},
		{"a media type", "media_type"},
		{"the PNG's bytes, base64", base64.StdEncoding.EncodeToString([]byte(pngFixture))},
		{"git's own binary notice", "Binary files"},
	} {
		if strings.Contains(sent, bad.needle) {
			t.Errorf("the request carries %s:\n%s", bad.what, sent)
		}
	}
	// The raw bytes and the NUL are asserted on what the model is actually
	// asked, not on the body's JSON: JSON escapes both (a NUL becomes
	// \u0000), so a body carrying the PNG's bytes verbatim passes a
	// strings.Contains on the encoded body and says nothing. Decoded, the
	// assertion is one that can fail.
	for _, bad := range []struct{ what, needle string }{
		{"the PNG's bytes, raw", pngFixture},
		{"a NUL byte", "\x00"},
	} {
		if strings.Contains(text, bad.needle) {
			t.Errorf("the request carries %s:\n%s", bad.what, sent)
		}
	}
	if !strings.Contains(text, "Fugaro built the changes under review into this prompt") {
		t.Errorf("the request carries no built input:\n%s", text)
	}
	if !strings.Contains(text, "+The app icon is in assets/.") {
		t.Errorf("the request carries no text diff:\n%s", text)
	}
}
