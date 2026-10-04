package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// seedWatch puts one capped repository with a killed switch-free api-key run
// and one subscription run into the fake.
func seedWatch(f *budgetFixture, title string) {
	seedCaps(f, 100, 20)
	d := today()
	now := time.Now().UnixMilli()
	f.db.Set(budget.PathSpendGlobal(d), map[string]any{"counted": 30 * usd1, "spent": 12 * usd1, "notional": 3 * usd1})
	f.db.Set(budget.PathSpendRepo(d, appSlug), map[string]any{"counted": 30 * usd1, "spent": 12 * usd1, "notional": 3 * usd1})
	f.db.Set(budget.PathAgent(appSlug, "20261002-090000-aaaa"), map[string]any{
		"repo": "acme/app", "title": title, "stage": "code", "round": 2, "auth": "api_key", "spent": 4 * usd1,
		"startedAt": now - 600_000, "updatedAt": now, "requestedBy": "someone@example.com"})
	f.db.Set(budget.PathAgent(appSlug, "20261002-090000-bbbb"), map[string]any{
		"repo": "acme/app", "title": "sub run", "stage": "review", "auth": "oauth", "spent": 2 * usd1,
		"startedAt": now - 300_000, "updatedAt": now, "requestedBy": "someone@example.com"})
}

func TestWatchOnceJSON(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "fix the bug")
	out, errOut, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(errOut, "project: aurora (GCP proj-1234)\n") {
		t.Fatalf("stderr = %q, want the project header first", errOut)
	}
	if strings.Count(strings.TrimSuffix(out, "\n"), "\n") != 0 {
		t.Fatalf("want one JSON line, got %q", out)
	}
	var doc struct {
		Project    string `json:"project"`
		Day        string `json:"day"`
		Connection struct {
			State string `json:"state"`
		} `json:"connection"`
		Total struct {
			CountedUSD  float64  `json:"counted_usd"`
			NotionalUSD float64  `json:"notional_usd"`
			DailyCapUSD *float64 `json:"daily_cap_usd"`
			Runs        int      `json:"runs"`
		} `json:"total"`
		Repos []struct{ Repo string } `json:"repos"`
		Runs  []struct {
			Run      string   `json:"run"`
			Title    string   `json:"title"`
			Notional bool     `json:"notional"`
			SpentUSD *float64 `json:"spent_usd"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Project != "aurora" || doc.Day == "" || doc.Connection.State != "live" || doc.Total.CountedUSD != 30 ||
		doc.Total.NotionalUSD != 3 || doc.Total.DailyCapUSD == nil || *doc.Total.DailyCapUSD != 100 || doc.Total.Runs != 2 || len(doc.Repos) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	notional := map[string]bool{}
	for _, r := range doc.Runs {
		notional[r.Title] = r.Notional
	}
	if len(doc.Runs) != 2 || notional["fix the bug"] || !notional["sub run"] {
		t.Fatalf("runs = %+v, want the oauth run flagged notional and only it", doc.Runs)
	}
}

func TestWatchPlainNoEscapes(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "fix \x1b]52;c;AAAA\x07the bug\x1b[2J\u202e")
	out, errOut, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{out, errOut} {
		if strings.ContainsAny(s, "\x1b\x07\u202e\r") {
			t.Fatalf("a control reached the output: %q", s)
		}
	}
	for _, want := range []string{"project aurora", "the bug", "NOTIONAL", "counted $30.00 of $100.00", "counted $30.00, no cap", "20261002-090000-aaaa"} {
		if !strings.Contains(out, want) {
			t.Errorf("plain frame lacks %q:\n%s", want, out)
		}
	}
	// A pipe is not a terminal: no --plain needed, same text.
	out2, _, err := execute(t, "watch", "--once")
	if err != nil || strings.ContainsRune(out2, 0x1b) || !strings.Contains(out2, "the bug") {
		t.Fatalf("err %v out %q", err, out2)
	}
	ascii, _, err := execute(t, "watch", "--once", "--ascii")
	if err != nil || strings.ContainsAny(ascii, "⚠·") {
		t.Fatalf("err %v ascii %q", err, ascii)
	}
}

func TestWatchRepoFilter(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	other := mustSlug("github", "acme/other")
	f.db.Set(budget.PathAgent(other, "r1"), map[string]any{"repo": "acme/other", "title": "theirs", "updatedAt": time.Now().UnixMilli(), "requestedBy": "x"})
	out, _, err := execute(t, "watch", "--once", "--json", "--repo", "acme/app")
	if err != nil || !strings.Contains(out, "mine") || strings.Contains(out, "theirs") {
		t.Fatalf("err %v out %s", err, out)
	}
	out, _, err = execute(t, "watch", "--once", "--json")
	if err != nil || !strings.Contains(out, "theirs") {
		t.Fatalf("err %v out %s", err, out)
	}
}

// deniedReads answers every read of the database with 403.
type deniedReads struct{ rt http.RoundTripper }

func (d deniedReads) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet {
		return &http.Response{StatusCode: 403, Status: "403 Forbidden", Body: io.NopCloser(strings.NewReader(`{"error":"Permission denied"}`)), Header: http.Header{}, Request: r}, nil
	}
	return d.rt.RoundTrip(r)
}

func TestWatchNeedsViewerRole(t *testing.T) {
	newBudgetFixture(t, "")
	budgetTransport = deniedReads{http.DefaultTransport}
	t.Cleanup(func() { budgetTransport = nil })
	for _, args := range [][]string{{"watch", "--once"}, {"watch", "--plain"}} {
		_, _, err := execute(t, args...)
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "Viewer role") {
			t.Fatalf("%v: err = %v (exit %d)", args, err, ExitCode(err))
		}
	}
}

func TestWatchWrongProjectDatabase(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("fugaro/project", "other")
	_, _, err := execute(t, "watch", "--once")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `belongs to project "other"`) {
		t.Fatalf("err = %v", err)
	}
	f.db.Set("fugaro/project", nil)
	if _, _, err = execute(t, "watch", "--once"); err == nil || !strings.Contains(err.Error(), "no /fugaro/project") {
		t.Fatalf("unmarked: err = %v", err)
	}
}

func TestWatchRefusesWithoutFirebaseConfigUsesDegraded(t *testing.T) {
	newCloudFixture(t) // no budget block
	out, errOut, err := execute(t, "watch", "--once")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no budget backend: showing run records only") || !strings.Contains(out, "RUN") {
		t.Fatalf("out = %q", out)
	}
	if !strings.HasPrefix(errOut, "project: aurora") || strings.ContainsRune(out, 0x1b) {
		t.Fatalf("stderr %q out %q", errOut, out)
	}
	js, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Project  string `json:"project"`
		Degraded bool   `json:"degraded"`
		Notice   string `json:"notice"`
		Runs     []any  `json:"runs"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil || !doc.Degraded || doc.Project != "aurora" || doc.Notice == "" || doc.Runs == nil {
		t.Fatalf("err %v doc %+v from %s", err, doc, js)
	}
}

// lockedBuf is a writer the test can poll.
type lockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestWatchStreamJSONThenCancel(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "streamed")
	cmd := NewRootCmd()
	out := &lockedBuf{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"watch", "--json"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "\n") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(out.String(), "\n", 2)[0]
	var doc struct {
		Runs []struct{ Title string } `json:"runs"`
	}
	if err := json.Unmarshal([]byte(first), &doc); err != nil || len(doc.Runs) != 2 {
		t.Fatalf("err %v first %q", err, first)
	}
}

func TestWatchTUIHook(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "x")
	typed(t)
	oldTTY, oldRun := watchStdoutTTY, runTUI
	watchStdoutTTY = func(io.Writer) bool { return true }
	t.Cleanup(func() { watchStdoutTTY, runTUI = oldTTY, oldRun })
	var got *WatchDeps
	runTUI = func(ctx context.Context, d *WatchDeps) error { got = d; return nil }
	if _, _, err := execute(t, "watch", "--repo", "acme/app"); err != nil || got == nil || got.Sup == nil || got.DB == nil || got.RepoKey == "" || got.LC.Name != "aurora" {
		t.Fatalf("err %v deps %+v", err, got)
	}
	// --once and --json never reach it, even at a terminal.
	got = nil
	for _, a := range []string{"--once", "--json"} {
		if _, _, err := execute(t, "watch", "--once", a); err != nil {
			t.Fatal(err)
		}
	}
	if got != nil {
		t.Fatal("a non-interactive mode started the screen")
	}
}
