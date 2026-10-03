package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// bitbucketAPI is a minimal stateful stand-in for the Bitbucket Cloud pull
// request API: exactly the calls the bitbucket provider makes on a new run.
type bitbucketAPI struct {
	t        *testing.T
	token    string
	mu       sync.Mutex
	prs      []bbPR
	comments []string
	log      []string // the writes, in order
}

type bbPR struct {
	ID          int          `json:"id"`
	Title       string       `json:"title"`
	Draft       bool         `json:"draft"`
	Description string       `json:"description"`
	State       string       `json:"state"`
	Branch      string       `json:"-"`
	Reviewers   []bbReviewer `json:"reviewers"`
	Links       struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	Source struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	} `json:"source"`
}

type bbReviewer struct {
	UUID      string `json:"uuid,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

// bbRequest holds every request body field the provider sends. Pointers
// tell a field that was sent from one that was left out.
type bbRequest struct {
	Title       *string       `json:"title"`
	Description *string       `json:"description"`
	Draft       *bool         `json:"draft"`
	Reviewers   *[]bbReviewer `json:"reviewers"`
	Source      struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"source"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func (a *bitbucketAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+a.token {
		a.t.Errorf("%s %s: wrong Authorization header", r.Method, r.URL.Path)
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	var in bbRequest
	if r.Method != http.MethodGet {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			a.t.Errorf("%s %s: %v", r.Method, r.URL.Path, err)
		}
	}
	const prs = "/2.0/repositories/acme/app/pullrequests"
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == prs:
		values := []bbPR{}
		for _, pr := range a.prs {
			if strings.Contains(r.URL.Query().Get("q"), `"`+pr.Branch+`"`) {
				values = append(values, pr)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"values": values})
	case r.Method == http.MethodPost && r.URL.Path == prs:
		pr := bbPR{ID: len(a.prs) + 1, Title: deref(in.Title), Draft: deref(in.Draft), Description: deref(in.Description), State: "OPEN", Branch: in.Source.Branch.Name,
			Reviewers: deref(in.Reviewers)}
		pr.Links.HTML.Href = fmt.Sprintf("https://bitbucket.org/acme/app/pull-requests/%d", pr.ID)
		pr.Source.Branch.Name, pr.Source.Repository.FullName = pr.Branch, "acme/app"
		a.prs = append(a.prs, pr)
		a.log = append(a.log, fmt.Sprintf("POST draft=%t reviewers=%d", pr.Draft, len(pr.Reviewers)))
		if pr.Draft && len(pr.Reviewers) > 0 {
			a.t.Errorf("a draft was created with reviewers")
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(pr)
	case r.Method == http.MethodGet && r.URL.Path == prs+"/1" && len(a.prs) == 1:
		json.NewEncoder(w).Encode(a.prs[0])
	case r.Method == http.MethodPut && r.URL.Path == prs+"/1" && len(a.prs) == 1:
		pr := &a.prs[0]
		if in.Title != nil {
			pr.Title = *in.Title
		}
		if in.Description != nil {
			pr.Description = *in.Description
		}
		if in.Draft != nil {
			pr.Draft = *in.Draft
		}
		if in.Reviewers != nil {
			pr.Reviewers = *in.Reviewers
		}
		a.log = append(a.log, fmt.Sprintf("PUT title=%t description=%t draft=%t reviewers=%t now-draft=%t", in.Title != nil, in.Description != nil, in.Draft != nil, in.Reviewers != nil, pr.Draft))
		if pr.Draft && len(pr.Reviewers) > 0 {
			a.t.Errorf("reviewers on a draft after PUT")
		}
		json.NewEncoder(w).Encode(pr)
	case r.Method == http.MethodPost && r.URL.Path == prs+"/1/comments":
		a.comments = append(a.comments, in.Content.Raw)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": len(a.comments)})
	default:
		a.t.Errorf("unexpected %s %s", r.Method, r.URL)
		http.Error(w, "unexpected", http.StatusNotFound)
	}
}

// TestBitbucketRunOverHTTP runs the fugaro binary against a git remote that
// demands the Bitbucket token over HTTP, and a stand-in Bitbucket API. The
// agent pushes the run branch itself and then amends it. The run must end
// with the amended commit on the remote, one ready PR with its report, and
// the token nowhere in what the run wrote.
func TestBitbucketRunOverHTTP(t *testing.T) {
	t.Run("finalize_only", func(t *testing.T) { bitbucketRun(t, false) })
	t.Run("early_draft", func(t *testing.T) { bitbucketRun(t, true) })
}

func bitbucketRun(t *testing.T, early bool) {
	testutil.IsolateGit(t)
	const token = "bb-e2e-token-5678"
	files := testutil.FixtureFiles(t)
	// Labels exist only to check that the Bitbucket adapter's "labels
	// aren't supported" warning reaches the runner's log, once.
	pr := "{ labels: [fugaro], early_draft: false }"
	if early {
		pr = "{ labels: [fugaro], reviewers: [\"{11111111-2222-3333-4444-555555555555}\"] }"
	}
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "provider: github", "provider: bitbucket\n  pr: "+pr, 1)
	remote := testutil.NewHTTPRemote(t, files, testutil.Token("x-token-auth", token))
	api := &bitbucketAPI{t: t, token: token}
	apiSrv := httptest.NewServer(api)
	defer apiSrv.Close()

	implement := `{"shell":"echo v1 > feature.txt && git add -A && git commit -qm 'Add feature' && git push -q origin HEAD && echo v2 > feature.txt && git commit -qa --amend -m 'Add feature' && fugaro verify test; printf 'Add feature\\n\\nAdds feature.txt.\\n' > \"$FUGARO_STATE_DIR/pr.md\"","text":"pushed with ${FUGARO_GIT_TOKEN}","cost":1}`
	claude := testutil.FakeClaude(t, `{"calls":[`+implement+`,`+reviewShip+`]}`)
	fugaro := testutil.BuildFugaro(t)
	tmp := t.TempDir()
	bucket, work := filepath.Join(tmp, "bucket"), filepath.Join(tmp, "work")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(tmp, "task.json")
	if err := os.WriteFile(taskFile, []byte(`{"version":1,"run_id":"`+runID+`","repo":"acme/app","ref":"main","task":"Add a feature"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, fugaro, "exec",
		"--bucket", "file://"+bucket, "--task-file", taskFile,
		"--workdir", work, "--remote", remote.URL, "--state-dir", filepath.Join(tmp, "state"),
		"--provider", "bitbucket", "--claude", claude, "--cancel-poll", "100ms")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + tmp,
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"ANTHROPIC_API_KEY=test-key-1234", "FIXTURE_FAILS_FILE=" + filepath.Join(tmp, "fails"),
		"FUGARO_BITBUCKET_TOKEN=" + token, "FUGARO_BITBUCKET_API_URL=" + apiSrv.URL + "/2.0",
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGQUIT) }
	cmd.WaitDelay = 5 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	out := append(stdout, stderr.String()...)
	t.Logf("fugaro exec output (err=%v):\n%s", err, out)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(stderr.String(), "labels aren't supported"); n != 1 {
		t.Errorf("stderr has the Bitbucket labels warning %d times, want once", n)
	}

	var rec runstore.Record
	data, err := os.ReadFile(filepath.Join(bucket, "runs", bitbucketSlug, runID, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady || rec.PR == nil || rec.PR.URL != "https://bitbucket.org/acme/app/pull-requests/1" {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, remote.Bare, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("remote branch %s, want the final head %s", got, rec.HeadSHA)
	}
	if n := testutil.Git(t, remote.Bare, "rev-list", "--count", "main..refs/heads/fugaro/"+runID); n != "1" {
		t.Fatalf("%s commits ahead of main, want only the amended one", n)
	}
	api.mu.Lock()
	prs, comments, writes := api.prs, api.comments, api.log
	api.mu.Unlock()
	if len(prs) != 1 || prs[0].Draft || prs[0].Title != "Add feature" || len(comments) != 1 || !strings.Contains(comments[0], "ready for review") {
		t.Fatalf("PRs %+v, comments %q", prs, comments)
	}
	if early {
		pr := prs[0]
		if len(writes) < 3 || writes[0] != "POST draft=true reviewers=0" || len(pr.Reviewers) != 1 || pr.Reviewers[0].UUID != "{11111111-2222-3333-4444-555555555555}" {
			t.Fatalf("writes %q, reviewers %+v", writes, pr.Reviewers)
		}
		if last := writes[len(writes)-1]; !strings.Contains(last, "reviewers=true now-draft=false") {
			t.Fatalf("the reviewers must be the last write, after the draft became ready: %q", writes)
		}
		if !strings.HasPrefix(pr.Description, "Adds feature.txt.") || strings.Count(pr.Description, "fugaro:status begin") != 1 || !strings.Contains(pr.Description, "**Ready for review**") {
			t.Fatalf("description:\n%s", pr.Description)
		}
	} else if len(writes) != 1 || strings.Contains(prs[0].Description, "fugaro:status") {
		t.Fatalf("the finalize-only flow wrote %q, description %q", writes, prs[0].Description)
	}
	env := testutil.FakeClaudeCalls(t, claude)[0].Env
	if !slices.Contains(env, "FUGARO_GIT_TOKEN="+token) || slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "FUGARO_BITBUCKET_TOKEN=") }) {
		t.Fatalf("agent env: want FUGARO_GIT_TOKEN and no FUGARO_BITBUCKET_TOKEN, got %q", env)
	}
	for _, f := range []string{
		filepath.Join(work, ".git", "config"),
		filepath.Join(bucket, "runs", bitbucketSlug, runID, "result.json"),
		filepath.Join(bucket, "runs", bitbucketSlug, runID, "report.md"),
		filepath.Join(bucket, "runs", bitbucketSlug, runID, "transcripts", "implement-1.jsonl"),
	} {
		if data, err := os.ReadFile(f); err != nil || strings.Contains(string(data), token) {
			t.Errorf("%s: unreadable (%v) or holds the token", f, err)
		}
	}
	if strings.Contains(string(out), token) {
		t.Error("the runner's log output holds the token")
	}
}
